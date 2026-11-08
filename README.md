<img src="assets/logo-256.png" alt="chonk logo" width="128" align="right">

# chonk

[![CI](https://github.com/kanywst/chonk/actions/workflows/ci.yml/badge.svg)](https://github.com/kanywst/chonk/actions/workflows/ci.yml)

See what is eating your disk, why it exists, and the right way to get it back.

<img src="demo.gif" alt="chonk finding 77G trapped in a VM disk and getting it back" width="800">

Disk viewers list directories. Cleaners delete a fixed list of caches. Neither tells you that the 81G file in `~/Library/Application Support/rancher-desktop` is a VM disk whose guest uses 3.6G, and that the other 77G comes back with one `fstrim`, not with `docker system prune`. chonk does:

```text
▲ 77.5G trapped in Rancher Desktop VM: freed inside the VM, still held on this Mac

■ VMs and containers                                     82.3G
▌   81.4G ██████████████ Rancher Desktop VM     77.5G trapped
■ Projects                                               58.5G
    10.7G █▊············ ~/wtfi                10.7G build output
```

## What it does

- **Groups everything by who put it there**: VMs and container engines, toolchains, caches, build output inside projects, app data, your files. Whatever no rule knows is still listed, as "unclaimed", at the directory where its size concentrates, so nothing large hides.
- **Counts space on disk**, not file lengths. A sparse 54G VM image that holds 16G counts as 16G. Hard links count once.
- **Says why each thing exists**, what removing it costs, and how safe that is (safe, careful, danger).
- **Asks git what a project holds.** A clean tree whose history is on a remote is safe, with `git clone` as the way back; one with uncommitted work, a commit no remote has, or a stash is danger, and chonk names which. Ignored local state that a fresh clone would not restore makes it careful.
- **Says which installed versions nothing pins.** It reads the pins in your projects (`.nvmrc`, `rust-toolchain.toml`, `.tool-versions`…) and each version manager's own default, and points at the toolchains neither asks for.
- **Finds the state left behind for folders that are gone**: the per-workspace history a VS Code-family editor keeps for a directory you deleted months ago.
- **Looks inside VMs** (Rancher Desktop, Docker Desktop, Podman machine) by asking the tool, and shows the space the guest freed but the host still holds.
- **Reclaims with the owner's own command** (`cargo clean`, `go clean -cache`, `brew cleanup`, `fstrim` in the guest, `podman machine rm`), or moves things to the Trash. It measures free space before and after and reports what actually came back, and runs the trim itself after pruning inside a VM.
- **Ties processes to what they own**: the VM's host agent and guest memory, the dev server running in a project. Before removing something, chonk warns when processes are running from it.
- **Shows what no directory explains**: Time Machine local snapshots (which keep deleted files' blocks alive), a running app's code-sign clone in `/var/folders` (a second copy of its whole bundle), and how much of the volume still lies outside the scan.
- **Remembers past scans**, so you can see what grew since last week.

## Install

```sh
# Homebrew (macOS, Linux)
brew install --cask kanywst/tap/chonk

# Go
go install github.com/kanywst/chonk/cmd/chonk@latest
```

Linux `.deb` / `.rpm` packages and archives are on the [releases page](https://github.com/kanywst/chonk/releases). Verify an archive with `gh attestation verify <archive> -R kanywst/chonk`.

On macOS, give your terminal Full Disk Access (System Settings, Privacy & Security) so chonk can measure Mail, Messages and other protected folders; without it they are listed as locked rather than counted as empty.

## Use

```sh
chonk                 # interactive view
chonk ls              # everything, grouped by owner (--json, --all)
chonk why PATH        # who owns PATH, why it exists, how to reclaim it
chonk diff            # what grew or shrank since the previous scan
chonk schedule on     # record a snapshot every day (macOS, launchd; --hour N)
```

`chonk diff` compares against the newest scan that is at least a day old. `chonk schedule on` installs a launchd agent (`~/Library/LaunchAgents/io.github.kanywst.chonk.snapshot.plist`) that runs `chonk snapshot` once a day at low I/O priority, so there is always a baseline; `chonk schedule off` removes it. The job is the chonk binary itself, not your terminal, so it needs Full Disk Access of its own to count protected folders. On Linux, run `chonk snapshot` from cron or a systemd timer.

`chonk why` does not scan the whole disk: it asks the rules about each directory from the top down to PATH and measures only what the owner claims, so it answers in a second.

| Key | Action |
| --- | --- |
| `↑` `↓` / `j` `k` | move |
| `enter` | open: why it exists, what is inside, how to reclaim it |
| `1`-`9` | pick a reclaim action on the detail screen |
| `y` | confirm (nothing else does, `enter` included) |
| `/` | filter by name or path |
| `r` | rescan |
| `esc` | back |
| `q` | quit |

A full scan of a home with three million files takes about 30 seconds on an M-series Mac; on macOS chonk reads directories with `getattrlistbulk`, one call per batch of entries instead of one per file.

chonk looks under your home, `/Applications`, `/opt/homebrew`, `/usr/local`, `/nix`, `/Library/Updates`, `/private/var/vm` and this login's own `/var/folders` container — never another account's. It skips iCloud and file-provider folders, which mostly hold placeholders for files that live elsewhere. Scan history and the reclaim ledger are in `$XDG_STATE_HOME/chonk` (default `~/.local/state/chonk`). `NO_COLOR` is respected.

## Safety

- Nothing runs without a `y` on the confirmation, which shows the exact command and its risk.
- chonk never deletes files itself. It runs the owner's command, or moves the directory to the Trash, which frees nothing until you empty it (and chonk says so).
- Commands come from the rules compiled into chonk, never from what it finds on disk.

## Development

```sh
make test       # go test -race ./...
make lint       # golangci-lint run
make vulncheck  # go tool govulncheck ./...
CHONK_DEMO=1 go run ./cmd/chonk   # the canned machine the demo is recorded on
```

CI builds for macOS and Linux on amd64 and arm64, runs the tests with the race detector on both, lints Go and Markdown, checks the release config and known vulnerabilities, and re-records `demo.gif` when the view changes. Pull requests get a review from Claude (`.github/workflows/claude-review.yml`), which needs a `CLAUDE_CODE_OAUTH_TOKEN` secret.

[DESIGN.md](DESIGN.md) explains the model and what was learned from kanywst/sweep.

## Releases

chonk follows [semantic versioning](https://semver.org). Versions are computed from [conventional commits](https://www.conventionalcommits.org) by release-please, which keeps a release pull request open on main with the next version and its [CHANGELOG](CHANGELOG.md) entry:

| Commit | Release |
| --- | --- |
| `fix:` | patch (`0.1.0` to `0.1.1`) |
| `feat:` | minor (`0.1.0` to `0.2.0`) |
| `feat!:` or a `BREAKING CHANGE:` footer | minor below `1.0.0`, major from then on |
| `chore:` `ci:` `docs:` `test:` `refactor:` | none on their own |

Merging the release pull request tags `vX.Y.Z`; goreleaser then builds for macOS and Linux, signs the checksums with cosign, attaches SBOMs and build provenance, and updates the Homebrew cask. Pull requests are squash-merged, so their titles must be conventional commits; a check enforces it.

## License

Apache-2.0
