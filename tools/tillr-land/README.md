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

## The red cap

The [gate policy](../../docs/design/gate-rework.md#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer)
bounds the unproved trunk range under `after-merge`. With the landing lock held,
landing refuses as `red-cap` when either enabled bound is reached: recorded
landings since `refs/tillr/green`, or minutes since that commit's committer time,
measured with a live clock after the gate and landing-lock wait. `--now` stamps
records; it never freezes the minute cap. Library fixtures can inject the cap
clock independently of the record timestamp.
Owner commits do not count as landings. Each zero disables that bound;
`before-merge` and fork patch deliveries do not apply the cap.

The refusal's `red_cap` payload names the bound, known failing checks, and their
open fixer item. When proof is pending rather than known red, it says that no red
check or open fixer has been recorded. With no green pointer yet, there is no
measured green range and bootstrap is not capped. Fixer items are always exempt.

The drain records a separate hold, retaining the candidate and its passed gate.
The next pass rechecks the live policy and proof pointer: once the cap no longer
holds, the same candidate lands without another run or gate. A changed trunk tip
also reopens the locked landing check, because another landing may now carry the
candidate's change. An empty squash is found before the cap: work already on the
trunk resolves elsewhere instead of waiting for proof.

When retained candidates occupy every offered lane, the planner can dispatch one
trunk fixer in `desk-trunk-fixer`, a separate worktree used by one fixer at a time.
Ordinary items cannot use it, and the held candidates keep their original worktrees.
`waiting` names the fixer item as the next mover; the loop still reports landings
since green with both bounds disabled.

## State invariants

- **The trunk moves only through a landing, and only for a verdict that passed.** A landing
  with no verdict behind it is refused; the record is the authority for the decision, not
  the caller's word for it.
- **The owner's disposition is checked immediately before landing.** An item stored as
  `closed` or `resolved-elsewhere` is refused as `item-not-landable`, even if its gate
  already passed. The store write lock covers this check, the trunk move, and the
  receipt, so a concurrent close either prevents the move or sees the successful
  landing receipt and refuses. Standalone stores with no item table remain usable.
- **One trunk landing, one squash.** `Trunk.Squash` writes exactly one commit —
  `git merge-tree --write-tree` to compute the tree, `git commit-tree` to write the commit —
  with the repository's scope-checked identity and the head's own message. The commit
  names no queue item: there is no `Tillr-Item:` trailer (ruling `D-RS3` in
  [`queue-persistence-ruling.md`](../../docs/design/queue-persistence-ruling.md#3-landing-receipts-without-commit-trailers)), and
  `Trunk.Move` fast-forwards the trunk onto it. The landed tree is the tree the verdict
  judged unless the trunk moved, and the row records both so a reader can tell.
- **Preparation does not hold SQLite's writer.** Under `land.lock`, ancestry, squash,
  clash checks and range preservation happen outside SQLite transactions. The final
  write transaction rechecks disposition, history inputs and trunk expectations before
  Move and receipt. A changed preparation input is an explicit error, not automatic
  replay. The final Move, fork-cache refresh and history-retention refs can still be slow; see the
  [contention recovery boundary](../../docs/recovery/sqlite-contention/README.md#exact-residual-limitation).
- **A retry of a run does not squash or publish its change a second time.** Git's Move and
  the SQLite receipt are two writes, and a crash can keep the first and lose the second.
  Before Move, the lander points `refs/tillr/landed/<run_id>` at the squash commit. That ref
  is the lander's own marker: it is local, it is never pushed, and the commit itself names
  no item. A run id must be a valid ref name component, and one that is not is refused
  (exit 2) before any Git read. A retry of the same run is answered in one of three ways:
  - The store already holds a `landed` row for this item, run and head at the marker's
    commit. The retry answers that row with exit 0 and writes nothing: no row, no event, no
    ref.
  - No row names the marker's commit, the commit is in the trunk's history, and it is
    exactly this head's squash onto its own parent. The retry records that commit as the
    landing and does not move the trunk. Neither an unrelated commit with the same tree nor
    a different head for the same run passes that check.
  - Otherwise the retry squashes as a first landing would. A squash that would only
    reproduce the trunk's tree is refused as `already-on-trunk` and is never published as
    an empty commit.

  A `trunk_history` tip whose object Git no longer has is an external failure that names
  the commit, and nothing moves. A Git failure while reading that tip is reported as the
  Git failure, not as a lost commit. The cases are pinned in
  [`idempotent_test.go`](idempotent_test.go), and the writer-side recheck in
  [`contention_test.go`](contention_test.go). The [known cases](#known-cases-the-marker-does-not-settle)
  below are where this answer is not what an operator might expect.
- **The lease on the trunk excludes other landers, never the gate.** `land.lock` spans
  preparation and the final Move/receipt window. The gate runs in another process before
  `pass` is called.
- **A non-landing pins its head.** A squash conflict or a refusal, `already-on-trunk` included, answers
  `refs/tillr/runs/<run_id>` at the head before the lock is released, so a head the gate saw
  cannot be garbage-collected before a conflict-fixer or a person reads it.
- **Authorship follows the trunk landing.** The squashed commit carries the identity the
  repository's git config states, never the lane branch's, and a repository that states one
  in a scope a lane can write is refused rather than trusted.

## Fork delivery

A fork item delivers to `refs/heads/patch/<fork>`, not the repository's trunk. Its
passing verdict must bind the candidate's base and tree and carry a passing
`fork-build` check, which runs the pack's build command on that exact head. The
lander fast-forwards the patch branch to the built commit without squashing or
rewriting fork history, and writes its landed marker before the move. The marker
names that same head, so sibling run boundaries recognize the delivery. The shared
cache's `main` and copy of the patch ref are refreshed before the receipt is written.
A missing cache is first prepared privately from the gated fork's upstream metadata
and published only when complete, at the current delivery tip. A retry after a crash
between move and receipt completes the refresh without a second delivery; if a later
fork item already delivered, recovery records the earlier head while keeping the
cache at that later tip. Ancestry is rechecked against the tip read under the landing
lock, so an external patch advance while the request waits cannot be overwritten.
Dirty or divergent cache work is never reset. A passing verdict
without the build check is `verdict-mismatch`, not permission to deliver. The
regressions are in [fork_safety_test.go](fork_safety_test.go).

## Known cases the marker does not settle

The landed marker (`refs/tillr/landed/<run_id>`, above) is evidence that this run published
a commit. It is not evidence that the trunk still carries the change, or that the run id
belongs to only one store. Two cases follow from that.

- **An owner reverts a landing the store never recorded.** The marker's commit is still in
  the trunk's history under the revert. A retry of that run records it as `landed`, although
  the trunk no longer carries the change. *Operator:* a revert means the item's change is not
  on the trunk. Re-open or re-dispatch the item under a new run, and correct the recorded
  `landed` row by hand if a reader needs it gone.
- **Two stores share one repository and reuse a run id.** Markers are per repository and
  receipts are per store. A second store that lands a candidate with the same content under
  the same run id finds the first store's marker. It records the first store's commit as its
  own landing. *Operator:* do not reuse run ids across stores over one repository. If it
  happened, correct the second store's row by hand.

Markers accumulate as run pins do: nothing deletes `refs/tillr/landed/*` or
`refs/tillr/runs/*`, and pruning either is an operator's decision. Deleting a marker loses
recovery for that run's unrecorded landing. A retry then squashes again, and it is refused as
`already-on-trunk` when the trunk still carries the change.

## Standalone invocation

```sh
tillr-land pass --store .tillr/state.db --repo . --item item-042 --run run-1 --base <base-sha> --head <head-sha> --verdict gate.verdict.json
tillr-land one --store .tillr/state.db --repo . --branch feat/thing --trunk main --gate 'tillr-gate run --gate python3 tools/fork-check.py'
```

`pass` is the landing on its own; an external caller that already has a verdict composes it
without the loop. `one` gates a branch and then lands it, so a manual landing a person runs
by hand produces the same row as an unattended one.

## What it refuses

- **A trunk that no longer contains the loop's observed tip** — `pass --observed-trunk`
  checks ancestry under the landing lock and records `trunk-rewritten`. For a drain
  store it also reconciles the durable observation and lost tip, then revalidates that
  snapshot in the final write transaction, so a stale request cannot bypass a newly
  recorded hold. Successful
  landings advance the stored observation and its Git object-retention ref. This is not a
  comparison with the candidate's base: a reset to that base can still remove history
  the loop observed later. The drain retains the candidate and its verdict until history
  is restored; ordinary forward movement is reapplied without another gate. Manual
  callers that omit the observation do not claim to detect a loop's lost history.
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
