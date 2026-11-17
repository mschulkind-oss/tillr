---
name: tillr-operating
description: Compose the eight `tillr*` binaries across the seven-step loop — plan, stage, run, judge, land, verify, audit — in a repository that has a tillr store. Covers the order, the one command per step, and which exit code means what; every face's flags are learned from the binary, not from this skill.
---

# Operating the tillr loop

> [!WARNING]
> **Known Gaps (verified 2026-11-12 at `32aed3dc31`).** `tillr` has landed a live
> candidate on an unattended tick, but three parts are unfinished:
> 1. **Item-state moves just landed** — queue item-state transitions are newly wired and have minimal production mileage.
> 2. **Two-item seed** — the seed currently holds only two dispatchable items.
> 3. **Recording emitters unbuilt** — the recording/event schema exists in `tools/tillr`, but the binaries' event emitters are not yet wired (`events` faces see only what is currently emitted).
>
> **Do not wire `tillr` into another repository's gate while the emitters are unbuilt.**
> The toolset ships early so it can be reviewed, not so it can be depended on.

`tillr` is eight binaries that act on one store. `tillr` is the umbrella dispatcher; the
seven `tillr-<noun>` binaries are the tools it dispatches to, and each also runs on its
own. **Learn a face's flags from the binary**: a face invoked without its required flags
names them in its refusal, and the flags are the tool's own — this skill never restates
one.

Every face takes the store it acts on, and a face invoked without one says so. `tillr store
init` creates a store; nothing here initializes a repository on your behalf, and no binary
does it implicitly.

`tillr drain loop` is a daemon by default: it keeps running when nothing is
dispatchable, waiting for a new item, a changed hold, or a trunk move. Its waiting
line names the held count and the first five items with their reasons, then stays
quiet until something changes. SIGINT or SIGTERM stops it with exit code 0 and a
final line saying it was stopped by the operator. The runs are left alone on a
signal, a fault, or any other ordinary exit. A restarted loop settles and judges
those runs once, including ones that finished while it was absent.

New lanes normally start at the last proved trunk commit, selected by the
repository's `lane_base` gate setting. A `trunk-unproved` hold means a finished
blocker's landing still needs proof from the heavy runner, not more work from
the blocker. The `tip` setting opts out; a trunk fixer always starts at the tip.
With no proved pointer yet, the loop uses the tip and says so once. The
[lane-base contract](../../../../tools/tillr-drain/README.md#the-commit-a-new-lane-starts-from)
explains recorded bases and safe reuse of idle lanes.

Use the stop face for an explicit operator request:

```sh
tillr drain stop --root <repository>
tillr drain stop --root <repository> --end-runs
```

Without the flag, stop ends supervision only. With it, the drain records the
interruption before sending SIGTERM to each in-flight run face. It does not ask the
runs to pause, so those items start over on the next pass without
spending a retry, failover, or automatic re-admission allowance. Repeated stops
need no authorization.

Stop acts on what its own process domain can see and says what it left alone on
stderr, one `loop: stop left alone: ...` line each: a session in another process
domain (stale heartbeat: treated as dead; current heartbeat: a live loop to stop
there), a session whose loop process is gone, and a run owned by another domain. Its
exit code: 0 when a loop was stopped, or with `--end-runs` when the runs were ended
(it prints "in-flight runs were ended" only then); 4 when a live loop in another
process domain was left running, after the local loops were stopped and, with
`--end-runs`, the local runs ended; 2 when no live loop is registered and `--end-runs`
was not given. With `--end-runs`, a worker whose run face has died is ended too. Stop the keeper first if supervision must stay stopped.

A caller that needs a finite
drain instead of a waiting service should learn the exit option from `loop --help`.
The former stay option is removed; waiting no longer needs an option.

## Keep the repository loop running

In this repository, `just tillr-keep` runs the **keeper**: the process that starts
an absent loop, waits for its exit, and starts it again. It does not authorize or
retry an item. It composes the repository's gate, land, and verdict adapters and
leaves the loop's after-merge check enabled. Learn its options from
`just tillr-keep --help`; additional loop options pass through after `--`.

The keeper runs in the foreground. On Linux it finds a loop by its own process
name, `tillr-drain` (including the pack's source-digest suffix), owned by the same
user and running in the same checkout. Its arguments must be the `loop` face for that
checkout: a `tillr-drain why`, `plan` or other face beside the loop is not the loop. The
keeper parses those arguments rather than searching them. More than one matching process
is a refusal,
not a reason to choose one. Start the loop in its repository checkout, not from
another directory with a different root named in its arguments.

After a loop runs for less than five minutes, the waits are 60 seconds, 120,
240, and so on, capped at thirty minutes. A run of at least five minutes resets
the wait to 60 seconds. For an adopted loop, time is measured from adoption.
The wait and each start are printed; startup errors stop the keeper explicitly.
The keeper's per-checkout lock prevents two keepers from starting duplicate loops.

`just tillr-keep --stop` stops only the keeper; the loop stays running. Ctrl-C
on the keeper does the same. Stopping the loop itself is a separate operator
action: stop the keeper first if the loop must stay stopped.

Use ordinary Git on the repository while the loop runs. That covers commits, pulls,
branches, tags, stashes, fetches, `git worktree add` and `remove`, fork exports, and
hand-written pins. None of it convicts a run. A run's boundary check convicts only what it
can attribute to the run: a write into another lane's worktree, or the run's own lane
worktree going away. Every other movement it sees is recorded as an observation on the
run's `boundary.check`. Leave the lane root's own worktrees alone, because they belong to
the loop.

A lane whose own state refuses staging, such as a change nobody landed or a branch another
worktree holds, leaves rotation for the session. The loop records it once and offers the
item the next lane. `tillr view waiting` shows it as `lane:<id>` with its remedy. Reset such
a lane only as the tillr-drain README's "Resetting a lane" describes.

## Exit codes, once

Every binary answers with the same contract, so a caller reads a decision rather than a
failure:

| Code | Meaning |
| :--- | :--- |
| `0` | completed; where the face decides, the decision is positive |
| `1` | completed with a negative decision — the record says why |
| `2` | usage error, or a malformed/unsupported input record; nothing ran |
| `3` | store, schema or record version incompatible, or the store stayed locked past the bound |
| `4` | an external dependency prevented a decision (distinct from a failed check) |
| `5` | a bug, not an outcome; any write transaction was rolled back |
| `6` | gate only: `stalled`, no verdict about the candidate |

`1` and `4` are the pair that matters unattended: `1` is a verdict about the candidate,
`4` is the tool saying it could not judge. Retrying a `1` is not a fix.

## 1. Plan — preview dispatchable work
`tillr drain plan`

What the loop would dispatch, and why each item is or is not dispatchable. Read it before
starting a pass: `1` here means there is nothing to dispatch, which is an answer.

## 2. Stage — sync item state from the documents
`tillr item sync`

The item state a pass will act on, read from the item blocks in the repository's tracked
documents: sync is how an item enters a store from a document, and `tillr drain loop` runs it
before its first plan. A second run over unchanged documents writes nothing. `1` names a
block with a finding, and every other document was still written. `tillr item` also carries
the apply, decision, mail, grant and verdict faces; staging is what the loop needs before it
runs anything.

### Take a review batch by hand

`tillr item comments take`

This reads one document's pending review threads from Vantage and prints the new item id,
or nothing when none is pending. The loop runs that batch on the design seat at a fixed
[document desk](../../../../docs/design/tillr-designer-sessions.md#the-desk), reusing its last
recorded designer rather than a numbered lane's unrelated session. A desk has a worktree
only during its run. Session selection follows the ordered
[dispatch table](../../../../docs/design/tillr-designer-sessions.md#choosing-at-dispatch):
warm sessions resume unchanged; expired ones over 40,000 tokens compact only when the adapter
provides that step. The compaction receipt records reported cost against the comment item.
A resume that fails before any model response is dispatched once more to a new session,
with the old transcript named in its brief. Deleted or done-role documents lose their rows,
not their transcripts. Automatic intake remains a later build slice.

A committed revision delivers replies after its recorded landing and after the served
checkout matches it. An answer-only run records `answered`, skips gate and landing, delivers
its replies, and closes the item. Delivery waiting on an owner's edit remains in the store
for a later pass; repeated delivery retains the same nonce.

To file fork work, name a checkout in the fence, for example
`.forks/lanes/lane-1/pi/` (or a subdirectory of it). The filed lane is a placeholder;
staging materializes the dispatched lane's checkout and names it in the brief and
fence, starting from the delivered patch tip. The worker commits there only, never
runs `bin/pi-forks export`, and never edits the shared cache. The fork gate's
`fork-build` check builds a clean copy of that exact head with the pack command; only
then does the lander move `refs/heads/patch/pi` to the built head, write its landed
marker, and refresh the shared cache's `main`. Items for the same fork are serialized.

## 3. Run — execute a candidate in an isolated lane
`tillr run exec`

`run pause --run <id>` requests a pause without judging or retrying the attempt.
`run exec --resume` continues that run in the same lane; the adapter declaration
determines whether it reopens the session or starts a fresh one with the prior
transcript and worktree named in its first prompt. The drain continues a paused
run on its next settlement pass and judges only its completed candidate.

One candidate, one lane, one record of what the run cost. `tillr run lanes`, `status` and
`release` inspect and clean up the lanes a pass left behind; a lane released is not a
candidate retried.

While working on Go code, use a targeted `-run` for the behavior being changed. Run the
full suite once before committing. In the drain module, drop `-count=1` for the working
loop: the tests declare the sibling sources as inputs Go's result cache can see, so a
sibling change reruns tests and an unchanged tree still caches. The drain README explains
the mechanism and names the permanent cache regression check. A worker runs tests, not
the landing gate (`just done`); the gate owns its landing lock.

## 4. Judge — evaluate the candidate against the gate
`tillr gate run`

A working check keeps its own verdict past its measured target. The gate samples
output and process-group CPU every second; sixty samples with neither is `stalled`,
not pass, fail or a harness error. The first stall retains the candidate as
`gate-stall-rerun`, waiting for fewer gates in flight, or none other, then gates
the same head without another worker or charged attempt. A second stall on the
same head tree holds it as `gate-stalled`, naming both observations. Diagnose it
and use `tillr drain clear-fault`. `gate-harness-error` remains a program that
could not start or crashed. If group CPU cannot be read, `watchdog unavailable`
is reported and no stall is declared.

The verdict about the candidate. `tillr-gate dualrun` runs two gates and compares them,
which is the evidence a disputed verdict is settled with.

## 5. Land — squash the passing candidate onto the trunk
`tillr land pass`

The trunk moves only through this step, and only for a candidate that passed. One landing
is one stateless squashed commit for ordinary repository work; a fork delivery keeps
the built fork head and refreshes its shared cache instead. A land that refuses records the item's verdict and is
not retried by the loop. A land that conflicts — `squash-conflict`, because another
landing moved the trunk first — is not a refusal: the loop stages one conflict-fix run for
the same candidate, carrying the conflicting paths and the three-way markers in its brief,
and judges that run like any other change. A second conflict on the same candidate holds
the item as `conflict-exhausted` instead of spending another run. If repair staging
fails, the affected item holds as `conflict-repair-staging-failed` without stopping
siblings. Its original candidate and conflict evidence remain retained across restarts.
Use the waiting view's cause to correct the item, backend policy, brief template, or
build; the next pass retries the retained repair only when those inputs change.
Queue rank, timestamps, and advancing trunk do not authorize that retry.

## 6. Verify — check store and invariant integrity
`tillr store check`

Whether the store still satisfies its own invariants. Run it after a pass and before
trusting anything `tillr view` says. Its report's `log` says how large the write-ahead
log (SQLite's `state.db-wal`) has grown.

The store's recent work lives in `state.db-wal`, not in `state.db`: never copy, hash or
hand over `state.db` alone. To make that one file current, run `tillr store checkpoint`
with no drain loop running (it refuses while one runs, because the loop checkpoints on
its own). A log file left large by a long run without a loop shrinks only with
`tillr store checkpoint --truncate`, which refuses while any other process has the store
open. Otherwise copy through SQLite (`.backup` or `VACUUM INTO` on a read-only
connection). The supported ways are listed in tillr-store's README, under "Backing up or
copying the store".

## 7. Audit — inspect recorded status and events
`tillr view events`

The record of what happened, and `tillr view status` for where the store stands now.
Where a record was never emitted (see Known Gaps), the absence is the finding — not a
reason to reconstruct the history from memory.

## Recovery

Use `tillr drain acquit` when a run's recorded fault was not its doing. It closes
that conviction with the operator's explanation, keeps the evidence, and makes
the retained candidate eligible for examination again. A run with no conviction
is refused; acquittal does not erase a failing gate verdict.

Use `tillr drain examine` to judge one retained candidate through the loop's own
gate, landing and verdict steps. Supply the same step programs and backend policy
as the loop. It refuses a lane that is held: stop its loop first, then examine the
run. Nothing new is dispatched merely to choose the candidate.

Use `tillr drain set-aside` when a committed candidate cannot land and needs
rework. It retains the head at the run's named Git reference, releases the lane,
and holds the original item with the operator's reason. The command prints the
reference. Uncommitted lane work and patch-branch candidates are refused. After
correcting the cause, clear the recorded hold with `tillr drain clear-fault` and
authorize rework through `tillr drain retry`, still under the same item id.
Learn each face's required inputs from its `--help`.

Retry a failed item under its own id with `tillr drain retry`. Read the failed
run's record, correct the cause, and authorize the retry through that face; learn
its required inputs from `tillr drain retry --help`. Keep the original item so
its run history and other items' dependencies remain attached to the same id.
Do not file a replacement item to escape a hold or mark the original resolved
elsewhere when its intent has not been satisfied.

Sync reports `clone-of-held-item` when a new id appends a numeric version suffix
to an unfinished item already in the store. The finding names both ids and the
retry command, and that document writes nothing. Final items and ids already in
the store are not refused by this rule.

## Stop conditions

Stop the loop and read the record, rather than retrying, when:

- the gate answers `4` — it could not judge, and a retry does not make it able to;
- `tillr store check` answers non-zero — nothing built on that store is trustworthy;
- a land refuses — the loop records it and moves on; a second attempt is a decision for
  a human, not for the loop. Two negative land answers are not stops. On a squash
  conflict the loop spends its one conflict-fix run and holds the item only if that run
  conflicts too. On `already-on-trunk` (the trunk already carries the whole change) the
  loop records the verdict and settles the item as `resolved-elsewhere`. A pending run whose
  item was already landed or closed by hand is not a stop either: the loop settles it as
  `item-already-terminal`, which judges nothing and releases the lane.
