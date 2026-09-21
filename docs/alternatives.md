# Why not just use sccache, kache, or mbx?

They are build-time caches. They make compilation cheaper. They do not change where Cargo
writes or how many files it touches, and that is the problem `mater` exists to solve.

## The two problems

One `copilot-host` build measures 7.8 GB across 27,412 intermediate files, against 615 MB
in 26 final artifacts. About 93% of the bytes and 99.9% of the file churn is intermediates.
Five concurrent worktrees is roughly 42 GB and 137,000 files. Fifty-five checkouts is
several hundred GB.

Defender follows that churn. During concurrent builds `wdavdaemon_unprivileged` sat at 36%
CPU and `wdavdaemon_enterprise` at 17%.

A cache can reduce the bytes. Only an exclusion reduces the file events, and an exclusion
needs one stable path to point at.

## sccache

Mozilla's ccache-style `rustc` wrapper. Mature, cross-platform, MPL-2.0.

It changes whether the compiler runs, not what lands in `target/`. A cache hit still
materializes every file, because Cargo's fingerprint check requires them on disk. It cannot
cache incremental compilation, so using it well means setting `CARGO_INCREMENTAL=0`. It
cannot cache anything that invokes the linker: `bin`, `dylib`, `cdylib`, `proc-macro`. Cache
hits also need matching absolute paths unless you configure `SCCACHE_BASEDIRS`.

Rebuilding a workspace crate and linking two large binaries is exactly what it cannot cache.

## kache

Content-addressed cache for Rust, C/C++, and CUDA. Apache-2.0, about seven months old when
compared.

It handles cross-worktree duplication best of the three. It normalizes local paths and
restores through hardlinks, reflinks, or CoW clones, so a second worktree costs far fewer
physical blocks. Its own benchmark claims ~3 GB for a second Firefox worktree against
~16.7 GB for sccache, though that number is vendor-published.

Cheaper in bytes is not fewer file events. Each worktree still gets a Cargo-visible tree
with the same directory entries and file creations. The cache store is itself a large,
active location that would need excluding too. There is no Defender integration, and its GC
cleans by cache policy rather than by which worktree produced what.

## mr-boxington (mbx)

A Rust build cache and scheduler by jdx, the `mise` maintainer. MIT, about one month old
when compared.

It is the closest in intent. It advertises "a tidier `target/`", has a disk budget, adopts
existing target directories, and can collect entries when a checkout disappears. The
budgeted `gc` is a genuine draw.

It is still a build cache with no AV-aware behaviour. Its README tells you to "give each
command its own target directory to avoid Cargo's directory lock", so the per-worktree trees
remain. Cleanup is budget- and age-driven rather than attribution-driven. At one month old
it was too new to sit under five concurrent builds on a machine already at its limit.

## What about a shared `CARGO_TARGET_DIR`?

Cargo takes a per-directory lock, so concurrent builds serialize on "Blocking waiting for
file lock on build directory". Working around the lock risks corrupt `.rmeta` and `.rlib`
output. Cargo maintainers treat a concurrent shared build cache as future work.

Symlinking each `target/` into a shared directory hits the same lock, and Defender's
handling of links through exclusions is not worth relying on.

## What mater does instead

Cargo 1.91 stabilized `build.build-dir`, which separates intermediates from final artifacts.
Pointing it at `~/.rust-build/{workspace-path-hash}` gives every worktree its own isolated
build directory under one stable root. No shared lock, 7.8 GB of churn out of each checkout,
and a single path to exclude.

That fixes placement, not lifecycle. A deleted worktree leaves its hashed directory behind,
and Cargo has no idea the worktree is gone. `mater` supplies the missing piece: an index
from build directory to workspace path, so abandoned output is identified by attribution
rather than guessed at by age.

None of this rules out a build cache. If you want faster compiles, add one. It is a separate
concern from where the bytes live and what scans them.
