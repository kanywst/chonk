# chonk

[![CI](https://github.com/kanywst/chonk/actions/workflows/ci.yml/badge.svg)](https://github.com/kanywst/chonk/actions/workflows/ci.yml)

See what is eating your disk, why it exists, and the right way to get it back.

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
- **Looks inside VMs** (Rancher Desktop, Docker Desktop, Podman machine) by asking the tool, and shows the space the guest freed but the host still holds.
- **Reclaims with the owner's own command** (`cargo clean`, `go clean -cache`, `brew cleanup`, `fstrim` in the guest, `podman machine rm`), or moves things to the Trash. It measures free space before and after and reports what actually came back, and runs the trim itself after pruning inside a VM.
- **Remembers past scans**, so you can see what grew since last week.

## Install

```sh
go install github.com/kanywst/chonk/cmd/chonk@latest
```

## Use

```sh
chonk                 # interactive view
chonk ls              # everything, grouped by owner (--json, --all)
chonk why PATH        # who owns PATH, why it exists, how to reclaim it
chonk diff            # what grew or shrank since the previous scan
```

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

chonk looks under your home, `/Applications`, `/opt/homebrew`, `/usr/local` and `/nix`. It skips iCloud and file-provider folders, which mostly hold placeholders for files that live elsewhere. Scan history and the reclaim ledger are in `$XDG_STATE_HOME/chonk` (default `~/.local/state/chonk`). `NO_COLOR` is respected.

## Safety

- Nothing runs without a `y` on the confirmation, which shows the exact command and its risk.
- chonk never deletes files itself. It runs the owner's command, or moves the directory to the Trash, which frees nothing until you empty it (and chonk says so).
- Commands come from the rules compiled into chonk, never from what it finds on disk.

## Development

```sh
make test       # go test -race ./...
make lint       # golangci-lint run
make vulncheck  # go tool govulncheck ./...
```

[DESIGN.md](DESIGN.md) explains the model and what was learned from kanywst/sweep.

## License

Apache-2.0
