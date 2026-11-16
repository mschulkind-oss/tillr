# tillr-land — the only path the trunk moves through

> [!WARNING]
> **Known Gaps (verified 2026-11-14 at `04957a8a83`).** `tillr` has landed a live
> candidate on an unattended tick, but three parts are unfinished:
> 1. **The AutoQA handoff skill is not shipped** — `yolo-packs/tillr/skills/` holds
>    `tillr-operating` only, so the design's `tillr-autoqa` handoff has no skill to dispatch.
> 2. **The observatory front end is not built** — `tillr view serve` answers 404 for the UI
>    until the front end under `tools/tillr-view/web` is built and embedded, which is a
>    separate Node step and not part of a Go build.
> 3. **The `tillr` on `PATH` is the pack's rendered tree, not this working tree** — an edit
>    under `tools/tillr*` reaches the launcher only after the pack is mirrored and rendered,
>    so a session that must run its own change builds the working tree first.
>
> **Do not wire `tillr` into another repository's gate while these stand.** The toolset
> ships early so it can be reviewed, not so it can be depended on.

## What this module owns

`tillr-land` performs the landing transaction. It takes a `gate.verdict` that already binds
a head and a base, squashes the change onto the trunk, and moves the trunk ref. It has no
queue: there is no candidate to enqueue, list, show or cancel between the gate and the
trunk, and no state is carried across the landing except the `landings` row it writes and
the head it pins when nothing lands.

The design that splits this surface into `--help`, this README and the composition skill is
[`docs/design/tillr-distribution.md`](../../docs/design/tillr-distribution.md).

## State invariants

- **The trunk moves only through a landing, and only for a verdict that passed.** A landing
  with no verdict behind it is refused; the record is the authority for the decision, not
  the caller's word for it.
- **One landing, one squash.** `Trunk.Squash` writes exactly one commit —
  `git merge-tree --write-tree` to compute the tree, `git commit-tree` to write the commit —
  with the repository's scope-checked identity and the head's own message. The commit
  names no queue item: there is no `Tillr-Item:` trailer (ruling `D-RS3` in
  [`queue-persistence-ruling.md`](../../docs/design/queue-persistence-ruling.md#3-landing-receipts-without-commit-trailers)), and
  `Trunk.Move` fast-forwards the trunk onto it. The landed tree is the tree the verdict
  judged unless the trunk moved, and the row records both so a reader can tell.
- **The lease on the trunk is short.** `land.lock` is held only for the landing window —
  reading the tip, writing the squash commit, moving the ref and recording the row — and
  never across the gate. The gate runs in another process before `pass` is called.
- **A non-landing pins its head.** A squash conflict or a refusal answers
  `refs/tillr/runs/<run_id>` at the head before the lock is released, so a head the gate saw
  cannot be garbage-collected before a conflict-fixer or a person reads it.
- **Authorship follows the landing.** The squashed commit carries the identity the
  repository's git config states, never the lane branch's, and a repository that states one
  in a scope a lane can write is refused rather than trusted.

## Standalone invocation

```sh
tillr-land pass --store .tillr/state.db --repo . --item item-042 --run run-1 --base <base-sha> --head <head-sha> --verdict gate.verdict.json
tillr-land one --store .tillr/state.db --repo . --branch feat/thing --trunk main --gate 'tillr-gate run --gate python3 tools/fork-check.py'
```

`pass` is the landing on its own; an external caller that already has a verdict composes it
without the loop. `one` gates a branch and then lands it, so a manual landing a person runs
by hand produces the same row as an unattended one.

## What it refuses

- **A verdict that does not bind the head's tree and the base sha** — there is no judgment
  about this change to act on.
- **A head that does not squash onto the trunk** — the row records `squash-conflict` and the
  paths git reported; the head is pinned and the verdict stands.
- **A trunk worktree that holds uncommitted content on a path the squash changes** — a
  fast-forward would stop on it, and a landing that went through anyway would leave it mixed
  into what the trunk now holds.
- **A repository that states no landing identity, or states one a lane can write** — the
  identity a commit carries is not a value a lane may choose.
- **A patch branch that is already at the head, or that the head does not descend from** — a
  patch branch is a delivery, and moving it back would drop commits another writer published.

## Test

`go test ./...` in this directory builds the binary and drives the faces against scratch
repositories. The land package's own suite pins the squash, the identity refusals, the pin,
and the lock: a gate that probes the lock with a non-blocking `flock` finds it free, and a
second writer is refused while a landing holds it.
