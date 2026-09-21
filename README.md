# mater

Sir Tow Mater MBE, better known as Mater, makes rust look good (ironically written in go)

`mater` funnels Rust build output into a single build root and reclaims it on demand.

## Why

Cargo writes intermediates into a `target/` directory inside every checkout. Across
dozens of worktrees that is two problems at once:

1. **Size.** Build output dwarfs the source it came from, and a worktree that has been
   deleted leaves its intermediates behind forever.
2. **Scanning.** Real-time malware scanning follows every file a compiler writes. Paths
   inside worktrees change constantly, so they cannot be exempted — a static exclusion
   list goes stale the moment a worktree is created.

Pointing `build.build-dir` at one stable root fixes both. The bytes land in a single
place, that place is excluded from scanning once, and `mater` keeps track of which
workspace produced each directory so abandoned output can be removed safely.

## Install

```sh
go install github.com/colbylwilliams/mater@latest
```

Or from a checkout:

```sh
go build -o ~/.local/bin/mater .
```

## Setup

Point Cargo at a single build root in `~/.cargo/config.toml`:

```toml
[build]
build-dir = "/Users/you/.rust-build/{workspace-path-hash}"
```

Exclude that one path from real-time scanning. `mater` never talks to your security
tooling; it prints the command for you to run:

```sh
mdatp exclusion folder add --path ~/.rust-build
```

Then confirm everything is wired up:

```sh
mater doctor
```

## Commands

### Inspecting

| Command | Purpose |
| --- | --- |
| `mater status` | Size on disk, orphan count, background deletes, recent activity |
| `mater list` | One row per directory: size, age, state, and the work that produced it |
| `mater logs -f` | Follow a background delete |

### Reclaiming

| Command | Purpose |
| --- | --- |
| `mater prune` | Remove output whose workspace has been deleted |
| `mater prune --stale 1w` | Also remove output idle past a threshold |
| `mater clean` | Remove everything |
| `mater restore` | Stop a delete in progress and put back what it has not reached |

`prune` and `clean` accept `--dry-run`, `--yes`, `--include-running`, and `--force`.

### Setup

| Command | Purpose |
| --- | --- |
| `mater doctor` | Check Cargo config, build root, index health, and probe availability |
| `mater config init` | Write a commented configuration file |
| `mater index show` | List recorded build directory to workspace mappings |
| `mater index bootstrap` | Recover ownership of directories that predate the index |

## How it decides what is safe

**An orphan is proven, not guessed.** Nothing inside a build directory names the
workspace that produced it, so `mater` records the link — obtained from Cargo itself —
as each workspace is seen. A directory is only ever called an orphan when the index
holds a path for it that no longer exists. Output from a repo `mater` has never scanned
stays unattributed and out of reach of `prune`.

**Live output is held back.** A running app executes from its own `target/debug` and
serves from its own `node_modules`, and it may have been built days before it was
launched, so age alone is not proof of idleness. Every process's arguments and working
directory are checked before anything moves. `mater clean` additionally refuses while
any compiler is running, because per-directory detection cannot see a `rustc` that
starts moments from now.

**Deletes do not hold the terminal.** Each victim is renamed into a staging directory on
the same volume, which is O(1), and a detached worker unlinks it afterwards. Your tree
is free the moment staging completes. Staging lives inside the build root, so the unlink
churn of a mass delete is not scanned either. If a worker is killed, the next run adopts
what it left behind.

**A delete can be undone while it runs.** Staging is not just a speed trick: until the
worker reaches an item it is still whole, and the manifest records where it came from.
`mater restore` asks the worker to stop — it only ever checks between whole items, so it
is never interrupted part-way through one — and moves everything it has not reached back
into place. An item whose old path has since been rebuilt is left alone; the fresh output
is the one to keep.

## Configuration

Optional, at `~/.config/mater/config.yaml`. Run `mater config init` to write a commented
template, or `mater config show` to see what is in effect.

```yaml
build_root: ~/.rust-build

# Extra checkouts to scan. Copilot worktrees are discovered automatically from
# session state, so list only repos outside them.
roots:
  - ~/GitHub/github/copilot-host

stale_age: 5d
scan_node_modules: true
```
