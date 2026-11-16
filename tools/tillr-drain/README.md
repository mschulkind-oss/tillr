# tillr-drain — the pass, and the loop that advances items

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

`tillr-drain` turns board state into per-item successor decisions. A **pass** visits
items in queue order and applies at most one transition per item; dispatched runs and
candidate examinations proceed independently, without waiting for a batch to finish.
`plan` computes one pass's positions and transitions and writes them to the file the caller named, and writes nothing else anywhere;
`once` does the same planning and records the decisions in the store's own dispatch table,
handing each dispatch's run request to a caller-named runner; `retry` records one
operator's authorization to re-dispatch a run the harness refused; `clear-fault`
acknowledges a diagnosed item-local fault so its retained work may advance; `boundary` records the
write boundary before and after a run and compares the pair; `resume` continues a run that
died without a verdict from its own harness session; `loop` is the unattended supervisor.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](../../docs/design/tillr-distribution.md).

## Pass observations and report identity

The [successor design](../../docs/design/tillr-drain-successor.md#25-one-pass-what-it-advances-and-what-bounds-it)
defines the item's positions and transitions. Each plan record prints `position` and
`transition`: `dispatch`, `retry`, `settle`, or `judge` when work can advance, and `none`
with a `reason` otherwise. Item holds and resource deferrals (`no-lane`, `fork-held`)
are separate groups in the loop's stop detail. A missing usable policy is a fault,
not an item hold.

The input kind is `drain.pass`. Its payload needs no `now` or `available_lanes`:
the envelope's creation time supplies observation metadata, and registered lanes are
offered in lane-id order. Callers may still supply those fields for a simulation.
`max_dispatches` is an optional simulation cap; reaching it simulates `no-lane`.
The live loop supplies no cap: lane leases bound dispatches.

An applied pass receives a store-counted **generation**, the ordinal defined by the
[trigger design](../../docs/design/tillr-drain-successor.md#44-s4--the-trigger-wait-replaces-the-ticks-clock).
An empty pass allocates none. Requests carry that generation as their correlation.
Timing measurements live in `drain_passes`, with the existing `tick_id` column
carrying the generation. On write-open, historical `drain_ticks` rows are copied
without replacing new measurements or inventing missing historical values. The old
table remains a migration source; new measurements never write it.

Loop reports are named by their inclusive first and last generations, not by the
clock. A loop that allocated none uses zero endpoints, so a startup fault remains
readable. For example:

```text
drain-1-3.json  -> record id loop-1-3
drain-4-6.json  -> record id loop-4-6
drain-0-0.json  -> no applied generation
```

## State invariants

- **`plan` writes nothing.** A preview that moved state would make "what would happen" and
  "what happened" the same question.
- **The loop owns the order and the stopping conditions, never a step's judgment.** The
  gate, the land and the verdict are programs the caller names.
- **There is no pass, spend or token budget.** The pile is the only throttle: if work should
  not be done, it does not go in the pile. The loop is a daemon by default: when
  nothing is dispatchable and no run is in flight, it waits for a new item, a changed
  hold, or a trunk move. It prints the held count and the first five items with their
  reasons once, then stays quiet until something changes. SIGINT or SIGTERM stops it
  with exit code 0 and a final operator-stop line. `--exit-when-drained` opts into
  stopping with `queue-drained` and exit code 0; the former `--stay` flag is removed.
- **The loop drains the queue the documents declare.** Before deciding anything at a
  new trunk commit, `loop` runs `tillr-item sync` (the `--item-bin` binary) against its
  `--root` and `--store`. The result's `synced_sha` and a report line name that commit.
  Passes at the same commit reuse both the sync and the lazily read open-question index;
  a new commit refreshes them. An item block committed during a trigger wait is therefore
  dispatchable without restarting the loop. A block with a finding is reported on the
  loop's output and in its result record. A finding or frozen definition attributed to
  an item holds that item instead of running its stale stored definition; other items
  continue. A sync that cannot run at all is a fault before any decision from that commit.
- **A loop that cannot start leaves a record.** Four lines on a stderr nobody kept is the
  failure this exists to prevent.
- **A held lane is never reported as a drained queue.** The two stop reasons are different
  answers and the loop says which it is.
- **A seat is a role, not a model.** The policy's `seats` map is where a role reaches a
  backend, and the item names the seat and never a model id. A plan whose items ask for a
  seat the policy does not map is refused before it builds a decision, so a seat is never
  defaulted to the pass's backend; the decision and the store's dispatch row record the seat
  beside the backend that answered. A failover that leaves the backend the item's seat names
  is a model switch, so it is dispatched with the reason `seat-degraded` and both halves in
  the text — the seat that fell back and the backend that answered — rather than reading as
  an ordinary failover.
- **A seat may name its own fallback order.** A seat's value is one backend name or an
  ordered list of them, such as `"design": ["claude-designer", "pi-designer"]`. The item
  dispatches to the list's first backend, and a failover walks that seat's list forward and
  stops at its end, so design work falls back to a design backend and never onto the build
  seat's. A step along the list is an ordinary `failover`, not `seat-degraded`: every
  backend on it was named for that seat. A seat with one backend, and an item with no seat,
  walk the policy's `preference` as before. Every name in a list must be one of the policy's
  backends; the check refuses the policy otherwise and names the seat
  ([the claude backend design](../../docs/design/tillr-claude-backend.md#4-falling-back-when-the-window-is-spent)).

## Automatic second attempts after an input changes

A held item's next pass compares its current item definition (including the referenced
body section), allowed paths, backend policy, and running tillr build with the inputs
recorded when its failed run was dispatched. A change permits one automatic dispatch;
its recorded decision names every changed input. This is the
[design's re-admission rule](../../docs/design/tillr-convergence.md#32-every-hold-names-a-mover-that-can-act),
not a retry of unchanged work. Queue rank, timestamps, criterion verdicts, and an advancing
base commit do not grant another attempt.

If that automatic attempt fails, later input changes do not dispatch it again.
`tillr-view waiting` says that the automatic attempt failed and names `tillr drain retry`
as the owner's next act. An authorized retry begins a fresh allowance, but unchanged
inputs still hold. Older runs with no recorded inputs need an owner retry rather than a
guessed comparison.

A loop-owned run stopped with its loop, or closed by reclaiming its abandoned lease,
also qualifies after examination proves it left no commit. For an unbounded claim with
no terminal record, replacement loops compare the recorded process ID and start time
within the same Linux boot, process namespace, and user identity, then check that no worker carrying
that run and store identity survives. Only a proven-dead owner with no live worker is
reclaimed. Foreign or legacy claims without that evidence remain protected.
Its decision says the earlier
run was orphaned by a loop stop. A worker signal without evidence of a loop stop, a time
limit, or a run that left a commit does not qualify on this basis. The same one-attempt
allowance applies.

## Owner edits and waiting landings

Uncommitted content in the main checkout belongs to the owner, including edits
made while runs are in flight. Boundary snapshots do not attribute that content
to any run. Writes into another lane's worktree still convict the writing run.

When the lander reports uncommitted content on a path its change touches, the
drain retains the already-gated candidate and its lane without a live worker
lease. It records `trunk-path-dirty: ` followed by the sorted dirty paths as an
ordinary item decision. Unrelated dirty paths do not delay a landing.

Commit or restore the named paths. The next pass retries only the landing with
the original candidate and verdict, including after a supervisor restart; it
neither dispatches a replacement run nor reruns the gate. Each wait check uses the
change reapplied onto the current trunk, so an already-applied path does not keep
blocking the remainder; a merge conflict reaches the lander's conflict handling.
An unchanged wait is not a candidate refusal or a failing verdict. Even
`--exit-when-drained` keeps
waiting while this retained landing can reopen. If the dirty path set changes,
the decision changes with it and the waiting clock restarts.

## Faults that hold one item

A gate that cannot judge (`gate-harness-error`), a recorded boundary conviction
(`host-boundary-violation`), a lane whose HEAD differs from the recorded candidate
(`candidate-record-mismatch`), or an item definition that cannot be synced
(`definition-sync-failed`) holds only the affected item. Other items keep running
and landing. A sync finding skips its entire document, so every stored item whose
definition comes from that file is held, not just the item named by the finding.
These holds survive a supervisor restart and do not record a failing
criterion verdict or spend a new model attempt. A retained candidate keeps its lane
reserved without a live worker lease.

The waiting face shows the fault, its evidence, any run and lane involved, and one
complete command to clear it. Diagnose and correct the cause first: fix the gate,
inspect the boundary violation, restore the recorded candidate, or fix the item
block. Then use the command shown by waiting, for example:

```sh
tillr view waiting --store /path/to/state.db
tillr drain clear-fault --store /path/to/state.db --item item-example --fault 123
```

The fault number is the event sequence displayed in that command. A stale command
is refused if newer evidence now holds the item. Clearing one fault does not clear
another kind of fault on the same item or erase the original evidence. Gate recovery
rejudges the retained candidate; it does not dispatch another model run. When the loop
rebases a fixer onto a newer trunk, it records the prepared candidate before gating,
so clearing a gate fault rejudges that bound head instead of convicting its own rebase.
Clearing a
sync fault forces document sync again even at the same trunk commit, so a definition
that is still broken is held again rather than run from stale store content.

A dispatch whose staging failed retracts its dispatch row and releases its claim
in one transaction. Its aborted staging remains in the append-only event history,
but a later plan may assign the same unstarted attempt to another lane without
colliding with that row.

## Faults that stop the whole loop

Fault stops are for failures that cannot be attributed to one item:

- The store cannot be opened, read, or written, including failure to persist a
  judgment, hold, or pass measurement.
- The trunk cannot be read, so no pass can select a trustworthy base.
- Settings are invalid or required shared machinery cannot start, such as a missing
  usable backend policy, an unmapped seat, or a document sync program that cannot run.

An item-local fault is not one of these stops. Operator cancellation and the explicit
`--exit-when-drained` setting remain normal ways to end the loop; an indefinite foreign
lane lease is still reported separately as `lanes-leased`, not a drained queue.

## Conflict repair that cannot be staged

A conflict-repair staging failure holds only its affected item as
`conflict-repair-staging-failed`; it does not cancel sibling workers. The candidate
commit is pinned before the lane is reset. A `drain.repair-held` event retains the
candidate, original conflict reports, exact staging error, input comparison, and next
action. The lane remains reserved for that candidate, without a live worker lease.

Unchanged item definition, fence, backend policy, brief template, and running build
keep that repair held across passes and supervisor restarts. Queue rank, timestamps,
and trunk movement do not reopen it. Correct the reported staging cause in one of
those inputs; the next pass stages repair of the retained candidate against the current
trunk, using the current configuration of its original backend. It does not rerun the
original model attempt or rejudge the same original conflict. A successful staging still
spends at most the existing one conflict-fix run. The waiting view names the item,
staging cause, and this correction as the next action.

Successful staging transfers pending ownership to the repair request and its new base
in one store transaction before execution starts. After an interruption, the next loop
settles that existing repair's terminal record and commit; it never starts the same
repair ID cold or truncates its transcript. Settlement and judgment use the ordinary
pass transitions, so spend is counted once and the launching examination cannot race
the repair's settlement.

Measured 2026-11-16 with the
[repair-staging regression](./conflict_hold_test.go): before containment, a staging
error propagated through examination completion to the loop's deferred cancellation,
interrupting two active siblings. The stored candidate lacked a completed-examination
event, so a replacement loop selected the same candidate for judgment again. The
regression now requires both siblings to land, two unchanged restarts to stage nothing,
and a corrected policy input to recover the original repair. The original failure event
and candidate commit remain available. The
[interrupted-recovery regression](./conflict_recovery_restart_test.go) also stops a
recovered worker after it commits, then requires an unchanged restart to land that
commit with zero new dispatches and an unchanged repair transcript.

## The fence, and the session

A run's fence is derived from the item's `allowed_paths`, in one function, and every reader
of it — staging, the planner, the escape check, a resume — reads that derivation rather than
repeating a rule. The item names the paths its change is about; the derivation adds the ones
the change is *obliged* to touch, because a fence that omits them refuses compliant work
after it is done:

- the pack's copy of every tillr module the fence names
  (`yolo-packs/tillr/files/local/share/tillr/<module>/`), which `just pack-sync` rewrites;
- for a tillr module, the `tillr-operating` skill and the documentation suite, plus the
  module's own `README.md` when the fence names a sub-path inside the module rather than its
  directory;
- `tools/tillr-loop/` as a whole when the fence reaches that module, whose `test.py` and
  `README.md` sit beside the program;
- `tests/fork-check.test.py` for `tools/fork-check.py`, and
  `tests/tillr-muse-backend.test.py` for `tools/tillr-run/backends/`.

A fence that names none of those derives nothing: this is a rule about the paths given, not a
widening of every fence.

**A lane is not a session.** Staging mints a fresh harness session id for every dispatch and
writes it beside the brief as `session-id`; it never reads that file back. The lane directory
is reused by unrelated runs, so its `session-id` describes whatever ran there last, and
reusing it hands one item's transcript to the next. The one caller that keeps a lane's id is
`resume`, which names the id it continues — that is what makes a continuation a continuation
rather than a cold start.

## Ask the planner why

```sh
tillr drain why --store /path/to/.tillr/state.db --policy /path/to/policy.json --lanes lane-1,lane-2
tillr drain why <item-id> --store /path/to/.tillr/state.db --policy /path/to/policy.json
```

`why` runs the same planner as `plan` without a pass input file, writes nothing,
prints rank, item id, position, reason, and mover for every item not stored as
`landed`, and ends with a summary. Naming an item prints its full explanation,
including the planner decision. `--json` gives rows and summary as one object,
or the selected item's record alone. The JSON answer also carries the event
sequence at which the planner read the store.

A mover is the question, blocking item, run, lane, or operator action that can
advance the item. The summary partitions the rows into running items, items the
next pass could advance, and holds counted by reason. Busy lanes include unfinished
candidate examinations, not just live leases. Capacity is consumed in rank order,
so an earlier predicted dispatch can leave a later item waiting on a lane.
Closed and resolved-elsewhere items remain visible with no further work required.

Use the loop's policy and lane offers to reproduce its next decision: those inputs
are not retained in the store. With no policy, readiness is held as `no-policy`,
not assigned a guessed backend. With no lane list, the planner offers every lane
in the store. The default clock is the current time; `--now` supplies a fixed
observation time. Document questions use the same root and index command as `plan`.

## Standalone invocation

```sh
tillr-drain plan
tillr-drain resume --run <run-id>
tillr-drain loop
```

`plan` is the read-only half an external caller composes against its own reporter; `loop` is
the whole service and needs the three judging programs named.

`resume` is one attempt continued and not a second attempt: the run id, the lane and the brief
are the ones the run already had, the session the lane carries is continued so the provider's
prefix cache is warm, and the store gains a `run.resume` event on the same run rather than a
second `runs` row. It re-takes the boundary bracket at the resume and
re-derives the fence from the item's `allowed_paths`, because the killed segment may have
committed before it died. It refuses a run the gate judged: continuing judged work is a
retry, which a person authorizes.

## What it refuses

- **A loop with no gate, land or verdict program** — it judges nothing itself, so it cannot
  start.
- **A root it cannot write a record under** — the record is the answer, not the exit code.
- **An unchanged re-dispatch nobody authorized** — automatic dispatch needs a recorded
  input change and an unspent allowance; otherwise a retry is an operator's act.
- **A resume with no session to continue, or of a judged run** — the first would pay for the
  whole transcript again while recording itself as a continuation, and the second spends a
  second attempt on work a verdict already answered.
- **A seat it cannot route** — an unmapped seat, or a seat outside the item model's closed
  vocabulary, refuses the whole plan with the seat and the mapped seats named; it is never
  silently replaced by the pass's default backend.

## Test

`go test ./...` in this directory builds the binary and drives each face against a scratch
store and scratch lane roots; the drain package's own suite pins the decision rules.
