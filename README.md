<p align="center">
  <img src="docs/assets/mater.png" alt="Mater" width="250">
</p>

# mater

Sir Tow Mater MBE, better known as Mater, makes rust look good (ironically written in go)

`mater` funnels Rust build output into a single build root and reclaims it on demand.

## Why

Cargo writes intermediates into a `target/` directory inside every checkout. Across dozens of worktrees that is two problems:

- **Size.** Build output dwarfs the source it came from, and a deleted worktree leaves its intermediates behind forever.
- **Scanning.** Real-time malware scanning follows every file a compiler writes. Worktree paths change constantly, so an exclusion list goes stale as soon as one is created.

Pointing `build.build-dir` at one stable root fixes both. You exclude that root from scanning once, and `mater` tracks which workspace produced each directory so abandoned output can be removed safely.

Already using a build cache? See [Why not just use sccache, kache, or mbx?](docs/alternatives.md)

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

Exclude that one path from real-time scanning. `mater` prints the command but never runs it:

```sh
mdatp exclusion folder add --path ~/.rust-build
```

Then check the setup:

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

**Orphans are proven, not guessed.** Nothing inside a build directory names the workspace that produced it, so `mater` records the link from Cargo as each workspace is seen. A directory is an orphan only when the index holds a path that no longer exists. Output from a repo `mater` has never seen is never called an orphan, so plain `prune` leaves it alone. Only `--stale`, which selects on idle time instead of attribution, can reach it.

**Live output is held back.** Age is not proof of idleness, since an app can run for days from a `target/debug` it built long ago. `mater` checks every process's arguments and working directory before moving anything. `clean` also refuses while a compiler is running.

**Deletes do not hold the terminal.** Each directory is renamed into staging on the same volume, which is O(1), and a detached worker unlinks it afterwards. Staging lives inside the build root, so the unlink churn is not scanned either. A killed worker is adopted on the next run.

**Undo works mid-delete.** Staged items stay whole until the worker reaches them. `mater restore` stops it between items and moves back the rest, leaving alone anything whose old path has since been rebuilt.

## Configuration

Optional, at `~/.config/mater/config.yaml`. Run `mater config init` to write a commented template, or `mater config show` to see what is in effect.

```yaml
build_root: ~/.rust-build

# Extra checkouts to scan. Copilot worktrees are discovered automatically from
# session state, so list only repos outside them.
roots:
  - ~/GitHub/example/my-repo

stale_age: 5d
scan_node_modules: true
```
