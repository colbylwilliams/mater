# Agent guide — `mater`

Standing rules for AI coding agents working in this repository. These are the things that are easy to get wrong without being told; everything else — layout, build, test — is discoverable from the tree and the [`README.md`](README.md).

## Working principles

- **Validate every assumption yourself.** Issues, PR descriptions, review comments, and task briefs are starting points, not ground truth. Check each claim against the actual code and the current state of `main` before you write anything. Don't rubber-stamp a brief.
- **Prefer correctness over a small diff.** Ship the full, correct change rather than a stop-gap that defers the hard part.
- **The safety model in the README is a set of invariants, not a description.** `mater` deletes things on a user's machine, so ["How it decides what is safe"](README.md#how-it-decides-what-is-safe) governs new work: an orphan is proven from the index and never guessed, output a live process depends on is held back, and a delete in flight stays whole until the worker reaches each item. Move toward those properties, never away. Widening what a command may remove — or narrowing what can be restored — is a deliberate, called-out change, never a side effect.
- **The detached worker is a real execution context.** `mater reap` is a hidden re-exec entry point that runs with no terminal and no inherited state, so it must resolve the same configuration as the process that spawned it. A new persistent flag that affects paths has to reach the worker too (see `globalFlags` in [`root.go`](cmd/root.go)), and worker-side code must not reach into the display layer.

## Pull requests

The review loop is the same every time; run it without being asked.

1. **Open the PR unless told otherwise**, and keep the title and description current as the change evolves — a reader shouldn't have to reconstruct the PR from its commit log.
2. **Don't request Copilot as a reviewer.** It is auto-assigned shortly after the PR opens; wait for that review rather than racing it.
3. **Triage review feedback by materiality — you are the bot's editor, not its patch-applier.** Fix what affects correctness, safety, or the PR's stated goal. For style nits, speculative suggestions, and things already handled, reply with why you're declining and resolve the thread. Don't grow the diff to satisfy a non-material comment.
4. **Reply to and resolve every thread** you address *and* every one you decline. Keep replies terse and factual — the reviewer is a bot.
5. **Guard scope.** A genuinely separate concern found mid-review becomes a follow-up issue or a stacked PR, not more diff here. Stack a child branch off the parent PR's branch, not `main`, and don't rebase it onto `main` while the parent is open.
6. **Give a direct readiness verdict, then stop.** Once the checks are green and every material thread is resolved, say so plainly. Leave the merge to a maintainer unless one explicitly asks you to perform it.

## Code and comments

- **Comment only where a comment adds value**: non-obvious invariants, and the *reason* for an unusual choice. Don't narrate trivial code. [`inuse.go`](internal/inuse/inuse.go) and [`worker.go`](internal/reap/worker.go) set the established tone — prose that explains why the code is shaped the way it is.
- **Write comments for the current code, not its history.** A reader new to a comment should grasp it without git history, the issue tracker, the review thread, or any session context. Keep out: process meta-attribution (`review flagged this`, `per the plan`), "used to / after the fix" setups (state the current invariant instead), and prompt-era references. Legitimately keep `TODO` / `FIXME` / "for now", and `legacy` when it's the real name of a field or flag.

## Documentation

- **Don't hard-wrap prose.** Write one line per paragraph or list item and let the renderer wrap it; only break where it's semantically meaningful.
- **Write docs as current fact, not as a proposal or a history.** Present tense, no "will", and none of the session- or PR-era narration you'd strip from a comment.
- **Reference files as basename links** — [`reap.go`](internal/reap/reap.go), not a bare inline-code path — so the reader gets a click-through.
- **Keep real repository and account names out of samples.** Config templates, documentation examples, and test fixtures use placeholders like `~/GitHub/example/my-repo`, so nothing `mater` writes into a user's config — or ships in the repo — names someone's actual private repo.
- **Behaviour and the README move together.** The README documents the safety model users rely on; a change to what `mater` deletes, holds back, or can restore updates it in the same PR.
