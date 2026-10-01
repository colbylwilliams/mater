<p align="center">
  <img src="docs/assets/mater.png" alt="Mater" width="250">
</p>

# mater

Sir Tow Mater MBE, better known as Mater, makes rust look _so good_ (ironically written in go)

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
| `mater status` | Size on disk, free space, orphan count, background deletes, recent activity |
| `mater list` | One row per directory: size, age, state, and the work that produced it, then the total and free space |
| `mater logs -f` | Follow a background delete |

### Reclaiming

| Command | Purpose |
| --- | --- |
| `mater prune` | Remove output whose workspace has been deleted |
| `mater prune --stale 1w` | Also remove output idle past a threshold |
| `mater prune -s 8h -o` | Remove only idle output, leaving deleted worktrees in place |
| `mater prune --watch` | Stay until the background delete has finished |
| `mater nuke` | Remove everything |
| `mater restore` | Stop a delete in progress and put back what it has not reached |

`prune` and `nuke` accept `--dry-run`, `--yes`, `--include-running`, `--force`, and `--watch`.

`--stale`/`-s` takes an age — `45m`, `6h`, `2d`, `1w`, or a bare number of days — and falls back to `stale_age` from config when given on its own. `--skip-orphans`/`-o` narrows a stale run to output that has not already been abandoned.

`--watch`/`-w` prints the background delete's progress as it goes and returns once it finishes, failing if the delete does not end cleanly. Ctrl+C stops the watching, never the delete.

### Setup

| Command | Purpose |
| --- | --- |
| `mater doctor` | Check Cargo config, build root, index health, and probe availability |
| `mater config init` | Write a commented configuration file |
| `mater index show` | List recorded build directory to workspace mappings |
| `mater index bootstrap` | Recover ownership of directories the index never saw |

## How it decides what is safe

**Orphans are proven, not guessed.** Nothing inside a build directory names the workspace that produced it, so `mater` records the link from Cargo as each workspace is seen: every run that reports on or removes build output, other than `list --fast`, first asks Cargo about each checkout it scans and keeps the answers. `mater` never installs a toolchain to ask, so a checkout pinning one this machine lacks goes unanswered and the index keeps what it already holds. A directory is an orphan only when the index holds a path that no longer exists. A link missed while the workspace existed — a worktree built and deleted between two runs of `mater` — can be recovered by `mater index bootstrap`: Copilot session state, git's worktree registry, and the missing paths named in the directory's own dep-info suggest where the workspace lived, and the link is recorded only when Cargo, asked about that deleted path as a workspace of its own, answers with the same directory. Output whose workspace cannot be proven this way stays unattributed, and a bare `prune` leaves it alone — `--stale` still reaches it, since idleness needs no attribution.

**Live output is held back.** Age is not proof of idleness, since an app can run for days from a `target/debug` it built long ago. `mater` checks every process's arguments and working directory before moving anything. `nuke` also refuses while a compiler is running.

**Deletes do not hold the terminal.** Each directory is renamed into staging on the same volume, which is O(1), and a detached worker unlinks it afterwards. Staging lives inside the build root, so the unlink churn is not scanned either. A killed worker is adopted on the next run. `--watch` keeps the terminal only to show progress: the worker stays detached, so interrupting the watch or closing the terminal leaves the delete running.

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
