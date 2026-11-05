# chonk design

chonk answers "what is eating my disk, why does it exist, and what is the right way to get it back". It is a TUI and a CLI for macOS first, Linux second.

## Why another disk tool

Directory-size viewers (dust, dua, gdu, ncdu) show paths and sizes. Cleaners (Mole, cleanmymac-cli, kondo, npkill) delete a fixed list of known-safe caches. Neither answers the questions that actually came up on a real machine:

- An 81 GiB file under `~/Library/Application Support/rancher-desktop/lima/0/diffdisk` is Rancher Desktop's VM disk. Inside the VM, Docker uses 3.4 GiB and the filesystem 3.6 GiB. About 77 GiB is space the guest already freed and never handed back to macOS. `docker system prune` does not touch it; `fstrim` inside the guest does.
- A 54 GiB `podman-machine-default-arm64.raw` takes 16 GiB on disk because it is sparse. Tools that add up file lengths report 54.
- `podman system prune` fails with "connection refused" because the machine is stopped, which is the moment you most want to know what it holds.

## What sweep taught us

kanywst/sweep had a Caches tab. It did not help, and the reasons shape chonk:

1. **A fixed list misses the big things.** sweep knew eight caches (Go, npm, pnpm, yarn, uv, pip, Homebrew). The machine's top consumers were a VM disk, a Podman machine, a Rust `target/`, a project `.cache`, the Nix store. chonk walks everything first and attributes second. Whatever no rule claims is still shown, as "unclaimed", at the directory where its size concentrates. Nothing big can hide.
2. **Apparent size lies.** sweep summed `st_size`. VM disks are sparse, so that over-reports by 3x. chonk counts allocated blocks (`st_blocks * 512`) and counts hard links once (the Nix store is full of them).
3. **Acting before understanding.** sweep went straight to "clean". chonk leads with why each thing exists, who made it, when it was last touched, and what it costs to regenerate. Reclaiming is the second step.
4. **Kept from sweep:** run each tool's own command instead of deleting its files; move to the Trash instead of `rm`; never act without a `y` confirmation; `--dry-run`, `--json`, `NO_COLOR`; the same Go + Bubble Tea v2 stack, lint and release setup.
5. **Scope.** sweep mixed ports, caches and dead clones. chonk is about disk. Process and memory views can come later, joined to the same owners.

## Model

```text
walk     roots -> tree of directories with allocated size, file count, newest mtime, marker files
owner    rules claim subtrees -> Item{Owner, Path, Size, Touched}; unclaimed remainder -> Item{Owner: unclaimed}
inspect  opaque blobs (VM disks, container stores) -> what is inside, measured by asking the tool
reclaim  Action{Cmd or Trash} -> run, measure free space before and after, append to the ledger
history  each full scan stores owner totals -> "since last week" deltas
```

### Walk

- Roots: `$HOME`, `/Applications`, `/opt/homebrew`, `/usr/local`, `/nix` (whichever exist). A root never crosses into another device, except that each root is its own device.
- Parallel, bounded by a semaphore; symlinks are not followed.
- Size is allocated bytes. A file with more than one link is counted the first time its (dev, inode) is seen.
- Each directory records a bitmask of marker files it contains (`.git`, `Cargo.toml`, `package.json`, `go.mod`, `pyproject.toml`, `CACHEDIR.TAG`, `pyvenv.cfg`, ...). Rules match on these instead of re-reading the disk.
- Directories smaller than 1 MiB keep their size but drop their children, so memory stays proportional to what matters.
- Unreadable directories (TCC-protected Mail, Messages, Safari) are reported as such, not silently counted as zero.

### Owners

An owner is who put the bytes there: a VM, a toolchain, an app, a project, the system. Each rule carries:

- `Kind`: vm, containers, toolchain, cache, build, app, project, user, trash, system, unclaimed
- `Why`: one sentence on what it is and why it exists
- `Regen`: what it costs to get back if removed ("re-downloaded on the next `go build`")
- `Safety`: safe (regenerates by itself), careful (loses state you may want), danger (loses data)
- `Actions`: the native ways to reclaim it, cheapest first
- `Inspect`: an optional inspector that looks inside

Rules are checked top down; the first match claims the subtree. Projects are the exception: a git working tree under `$HOME` is claimed as a project, and its build artifacts (`target/`, `node_modules/`, `.next/`, `.venv/`, any `CACHEDIR.TAG` dir, ...) are claimed inside it as separate items.

Unclaimed space is reported at its concentration point: starting from an unclaimed directory, descend while one child holds most of it.

### Inspectors

Inspectors run on demand with a timeout, because they shell out.

- **Rancher Desktop / Lima / Colima**: host-side allocated size of the disk image vs. the guest's `df` of its data filesystem, plus `docker system df`. The gap is "trapped": freed inside, still held on the host.
- **Podman machine**: `podman machine inspect` for the image path and state. When stopped, chonk says so and shows the allocated vs. logical size.
- **Docker Desktop**: `Docker.raw` allocated size vs. `docker system df`.
- **Nix**: number of profile generations.

### Reclaim

An action is a command (`go clean -cache`, `rdctl shell sudo fstrim -av`, `podman machine rm`) or "move to Trash". chonk records free space on the volume (statfs) before and after, rescans the item, and appends `{time, owner, action, expected, freed}` to `$XDG_STATE_HOME/chonk/ledger.jsonl`. When freed is far below expected, it says why if it knows (a sparse disk image that needs a trim, an APFS snapshot holding the blocks).

### History

Every full scan writes owner totals to `$XDG_STATE_HOME/chonk/snapshots.jsonl`. The TUI shows the change against the newest snapshot that is at least a day old, so "what grew since last week" is one glance.

A history only exists if someone scans, so `chonk schedule on` writes a launchd agent that runs `chonk snapshot` daily (`StartCalendarInterval`, which launchd also fires on wake when the Mac slept through the hour) as a `Background` process with `LowPriorityIO`. The plist points at the path chonk was run from, not its symlink target, so Homebrew's `bin/chonk` keeps working across upgrades; a `go run` build is refused. `XDG_STATE_HOME` is copied into the job so it writes the history the user reads.

## Interface

```text
chonk              interactive view
chonk ls           owners, biggest first (--json, --all for small ones)
chonk why PATH     which owner claims PATH, why it exists, how to reclaim it
chonk diff         change since the previous snapshot
chonk snapshot     scan and record, one line of output (what the daily job runs)
chonk schedule on  install the daily launchd job (off removes it; no argument shows the status)
chonk version
```

The TUI has two levels: owners grouped by kind, and a drill-down (`enter`) into an item's directory tree with the inspector's report on top. `x` opens the reclaim menu for the item under the cursor.

### Processes

Owners also run things. `ps` gives every process's executable and memory, and one `lsof -d cwd` call gives working directories. A process belongs to the owner that names it (a VM's host agent, `nix-daemon`), else to the deepest item its executable is under, else to the deepest item its working directory is under. A Virtualization.framework guest runs in an XPC service whose parent is launchd, so it goes to the VM whose host agent started just before it. The confirmation dialog warns when processes run from what an action removes.

### Outside the scan

Two things no directory shows:

- **APFS local snapshots** (Time Machine) keep every block deleted after they were taken. They are listed as one item of unknown size; thinning them is an action, and the ledger then records what came back.
- **The rest of the volume**: statfs's used bytes minus what the walk counted is swap, logs, other users and `/Library`, shown in the header so the scan's total is not mistaken for the disk's.

### Speed

On macOS directories are read with `getattrlistbulk(2)`, which returns names and allocated sizes for a batch of entries in one call, with pooled buffers and two workers per CPU. A 3M-file home takes about 30 s, against 38 s for ReadDir plus lstat; the rest is APFS metadata I/O, which more workers only contend on.

## Not now

- Purgeable space (needs Foundation's volume capacity keys)
- Per-snapshot sizes, which APFS does not expose
