---
title: "Drain Pass and Successor Reference"
status: accepted
stage: CURRENT
verified: 2026-11-24
covers:
  - tools/tillr-drain/drain/pass.go
  - tools/tillr-drain/drain/plan.go
  - tools/tillr-drain/drain/loop.go
  - tools/tillr-drain/drain/trigger.go
  - tools/tillr-drain/drain/generation.go
  - tools/tillr-drain/drain/dispatches.go
  - tools/tillr-drain/drain/run_id.go
  - tools/tillr-drain/drain/timing.go
  - tools/tillr-drain/drain/looprecord.go
  - tools/tillr-drain/drain/boundary.go
  - tools/tillr-drain/drain/once.go
  - tools/tillr-drain/holdrules/holdrules.go
  - tools/tillr-run/run/lease.go
tags:
  - tillr
  - drain
  - state-machine
  - reference
summary: "How tillr-drain advances the queue: a pure per-item successor function, derived positions, clock-free identities, compare-and-swap application, asynchronous settlement, store-counted generations, and a trigger-driven loop."
depends-on:
  - ../design/board-utilities.md
  - ../design/landing-principles.md
  - ../design/momentum-principle.md
  - ./tillr-build-commit.md
vantage:
  status-chip: true
---

# Drain Pass and Successor Reference

> **Delivered copy.** This is `docs/reference/tillr-drain-successor.md` from this repository at commit `033b21d403` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-drain-successor.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Verified 2026-11-24 by reading the code named in `covers`.
UNMEASURED: the Go suites test replay-invariant identities, zero-write empty
passes, non-blocking sibling dispatch and trigger wakeups; this document
records no measurement of a live drain.

> **In short.** `tillr-drain` advances the queue through a **pass**: one read
> of the store and repository, at most one transition per item, each applied
> compare-and-swap. Workers and candidate gates run asynchronously, their
> outcomes return through the store, and the loop sleeps on triggers once a
> pass changes nothing.

The detailed operating reference — every hold reason, coordinator and flag —
is the [`tillr-drain` README](../../tillr-drain/README.md). This document
states the model that README builds on and the rules a change must not break.

**Out of scope:** continuing an uncommitted build run
([`tillr-build-commit.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-build-commit.md)), squash landing
([`landing-principles.md`](../design/landing-principles.md)), and the
observatory's projections ([`observatory-redesign.md`](observatory-redesign.md)).

---

## Principles and Invariants

- **Identities are clock-free.** A transition id, decision id and run id are
  pure functions of the item, its attempt number, its workflow stage, its
  positions, and for a hold its reason. Replaying the same store facts yields
  the same ids at any clock rate
  (`TestReplayIsInvariantToClockRateAndTriggerSuppression`).
- **Positions are derived, never stored.** `items.state` holds only the
  [item states](tillr-item-states.md). Each pass reconstructs where an item
  stands in the drain from items, runs, lanes and the event log.
- **Outcomes return through the store, never the call stack.** A dispatch
  claims a lane, starts the worker and returns. In-process completion
  notices only wake the loop early; a loop that restarts mid-run or mid-gate
  derives the same position from stored rows.
- **At most one transition per item per pass, applied compare-and-swap.** A
  pass never runs dispatch, settle and judge to completion for one item while
  others wait. An item whose inputs moved after planning is skipped and
  replanned.
- **Candidate gates run concurrently; trunk writes are serialized.** Settled
  candidates are gated on their own leased lanes with no loop-wide judge lock
  (`TestSettledLaneDispatchesNextItemWhileSiblingRuns`). Only the landing's
  trunk ref update takes the repository's land lock.
- **A pass that changes nothing writes nothing.** It allocates no generation
  and adds no dispatch, timing or event row
  (`TestRepeatedEmptyPassesWriteNothing`).

> [!WARNING]
> Do not add a wall-clock value to a transition, decision or run id, and never
> update an existing `dispatches` row. Recording an identical decision again is
> a no-op; recording a different payload under an existing decision id is a
> `decision-collision` fault that holds that one item instead of overwriting
> the row.

> [!WARNING]
> Do not inspect or gate a finished run's worktree without reclaiming its lane
> first. The worker's lease is cleared when its terminal record is written; the
> settle and judge transitions immediately re-lease the lane with no expiry so
> a concurrent pass cannot stage another item onto the candidate's worktree.

---

## The Successor Function and Derived Positions

`drain.Plan` (package `drain` in `tools/tillr-drain`) is the pure planner. It
reads one store snapshot plus the open document questions and trunk facts, and
returns one decision per item. `tillr-drain plan` prints that plan and writes
nothing to the store; `tillr-drain once` and `tillr-drain loop` apply it.

The input record kind is `drain.pass`. Its optional `now`, `available_lanes`
and `max_dispatches` fields exist for simulation. The live loop supplies no
dispatch cap: registered lanes are offered in lane-id order and lane leases are
the only bound.

Every item derives one of five positions:

| Position | Derived condition |
| :--- | :--- |
| `landed` | The item's state is a finished state. |
| `running` | A live lane lease holds a run for the item, a finished run has not yet been settled, or a multi-node workflow is between stages. |
| `candidate` | A settled attempt produced a pinned candidate commit that has not yet been judged, and no lease is live on the item. |
| `queued` | The item is `in-queue`, its blockers are satisfied, no question or item hold applies, and either a dispatch is ready or only a pass-scoped resource (a lane, a fork, a desk) defers it. |
| `held` | A fact about the item itself holds it: its state, a blocker, an open question, an item fault, or a run ending nothing has retried. |

---

## Transition Table

Before planning, a pass reaps two kinds of stale lease in one write
transaction: lanes whose finite lease expiry has passed (their runs are closed
as `timed-out`; a lease with no expiry is never reaped by the clock), and
executing runs whose owning loop process is gone. Each item then takes at most
one step:

| From | Transition | Condition | Effect |
| :--- | :--- | :--- | :--- |
| `queued` | `dispatch` | The item is ready, its seat's backend is open, its fork is unclaimed, and a lane is free in the item's pool. | Stages the lane, leases it, records the dispatch and starts the worker. The item is `running`. |
| `running` | `dispatch` | A multi-node workflow finished a stage and its next node is ready. | Stages and starts the next node on the workflow's retained base. The item stays `running`. |
| `running` | `settle` | The latest worker run has written its terminal record, has not been examined, and its lane can be reclaimed. | Re-leases the lane and examines the run asynchronously: a run that committed goes on to the gate; an infrastructure ending, a refusal or an uncommitted run is continued, returned or held by its own rule. |
| `running` | `settle` | A workflow item stopped being runnable (it left `in-queue`, gained a blocker or opened a question) while its lane is free. | Releases the workflow's reservation without running another node. The item is `held`. |
| `candidate` | `judge` | A pinned candidate awaits verification, the trunk permits landing, and a lane is free. | Re-leases the lane, runs the gate, and lands under the land lock. The item is `landed`, or the candidate is returned or held on refusal. |
| `held` | `retry` | The store holds an unconsumed retry authorization and the resources are available. | Leases a lane under a new attempt number and starts the worker. The item is `running`. |
| any | `none` | A resource deferral, an item hold, or a finished item. | Changes no lane or run. |

A worker lease expires at the run's start plus its timeout plus a fixed grace
([Current values](#current-values)). A run with no timeout, and every settle
and judge lease, carries no expiry.

---

## Identities, Records, and Holds

### Deterministic ids

- **Attempt number.** One more than the highest attempt the item's prior runs
  record. It is derived from the runs, not from a store counter, so a crashed
  pass consumes none.
- **Run id.** `run-<item>-a<attempt>` for a single-node run and
  `run-<item>-a<attempt>-n<stage>` for a workflow node. A continuation within
  an attempt appends `-<kind>`, then `-<count>-<kind>` with the count padded to
  three digits. A digest bounds a long item id so the run id stays under its
  length limit.
- **Transition id.** `transition-<item>-a<attempt>[-n<stage>][-<run id>]-<from>[-<to>]`;
  a hold has no `<to>`.
- **Decision id.** The transition id with the `decision-` prefix. A transition
  from `candidate` appends `-lane-<lane>`, so examining one candidate on a
  second lane is a distinct decision. A hold appends a SHA-256 of its reason
  and the names that explain it, so two different holds at one position never
  share an id.

### Dispatch rows and hold classes

The `dispatches` table is keyed by decision id and is append-only. The live
loop writes a row for each applied transition and for the three holds that
block a candidate on the trunk (a dirty trunk path, a rewritten trunk, and the
red cap); ordinary holds write nothing, so an idle wait leaves the store
untouched. `tillr-drain once` records every planned decision when at least one
decision id in the pass is new.

Holds fall into three classes:

1. **Resource deferrals** — `no-lane`, `fork-held`, `waits on a desk`,
   `waits on the document's desk`. The item stays `queued`. Ordinary items never
   defer each other on overlapping paths at dispatch; only items sharing one
   fork branch serialize. The fork claim is computed from live leases and
   unfinished work, not from the current pass alone.
2. **Item holds.** The item is `held`. Every hold reason maps to a waiting
   class and a mover in `Rules` (package `holdrules` in `tools/tillr-drain`),
   which is the list of record; the README's *Adding a hold reason* says how
   one is added.
3. **Faults.** A missing usable backend policy stops the loop instead of
   holding items. An item-local invariant fault (`decision-collision`,
   `settle-failed`, `workflow-invalid`) is recorded as a `drain.item-fault`
   event and holds only that item until `tillr-drain clear-fault` acknowledges
   it.

---

## One Pass and Compare-and-Swap Application

Items are visited in a fixed order: trunk-fixer items, then prose-rewrite
items, then ranked items in rank order, then unranked items by id. A lane owed
to an item's returning worker is reserved for it ahead of new dispatches.

Application keeps external work out of the store's write lock:

1. **Snapshot.** While planning, each item records a digest of its definition
   and of the inputs its decision read: its row, its latest run, its target
   lane's lease, its unfinished work and its faults.
2. **Prepare.** Base resolution, worktree staging and file reads happen with no
   write transaction open.
3. **Compare and swap.** One short write transaction per item re-reads those
   inputs, re-checks lane and fork availability, allocates the pass's
   generation if this is its first applied transition, claims the lease and
   records the decision. A mismatch abandons only that item's claim; the next
   pass replans it.
4. **Bracket the run.** Each worker records the write boundary of its own
   worktree and the neighboring lanes before and after it runs
   (`TakeBoundary`). A neighbor that the loop itself staged or reset meanwhile
   is excused, so one run is never blamed for another's legitimate change.

---

## Generations, Timing, and the Trigger Loop

### Generations, pass timing and loop reports

- **Generation.** A store-counted ordinal, `gen-<n>`, allocated in the first
  write transaction of a pass that changes state (`allocateGeneration`). Every
  decision and run request of that pass carries it as its correlation, and the
  count continues across loop processes.
- **Pass timing.** `drain_passes` holds one row per generation: planning and
  staging time when the pass finishes, with worker, gate and landing time
  added as that asynchronous work completes. A gate run longer than the gate
  budget ([Current values](#current-values)) is flagged and logged; it files
  nothing and stops nothing.
- **Loop report.** A loop writes `<root>/.tillr/loop/drain-<first>-<last>.json`,
  named by its inclusive first and last generation and not by the clock. A
  loop that allocated none writes `drain-0-0.json`, archiving an existing one
  first.

### The trigger loop

`Loop.Run` starts the next pass immediately whenever a pass applied work,
reaped a lease or skipped a stale item. When a pass changes nothing:

- **Work is in flight or a hold will lift by itself:** the loop waits for a
  trigger.
- **Nothing is in flight and no lane is leased:** the loop is a daemon by
  default, so it reports what is waiting once and waits for a trigger.
  `--exit-when-drained` stops it instead with `queue-drained` and exit code 0.
- **A lane is leased with no expiry and no live run owns it:** the loop stops
  with `lanes-leased` and names `tillr run release <lane>`, because no clock can
  free that lane.
- **Every remaining lease has an expiry:** the loop waits for a trigger.

A trigger wait (`waitForTrigger`) ends on the first of:

1. **An in-process completion** of a worker or examination.
2. **A store-change notification** from the local notification relay, which
   the loop starts when absent. `--no-notify` disables it.
3. **A timer** set to the fallback poll interval or the earliest finite lease
   expiry, whichever is sooner. Polling bounds how late a store change is
   seen; it never bounds how much work runs.
4. **A stop request**, by signal or `tillr-drain stop`. The loop exits with
   `operator-stop`.

---

## Current values

| Value | Literal | Defined in |
| :--- | :--- | :--- |
| Lease grace past a run's timeout | `300000` ms | `LeaseGraceMS`, package `run` in `tools/tillr-run` |
| Fallback store poll interval | `15s` | `DefaultFallbackPollInterval`, package `drain` |
| Gate budget for the over-budget flag | `330s` | `DefaultGateBudget`, package `drain` |

---

## Why it's this way

Each row is a decision a maintainer reading only the code might undo. The
owner confirmed `D-DS1`–`D-DS16` as settled in
[`OQ-PR4`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR4), 2026-11-20.

| ID | Decision | Why it stays |
| :--- | :--- | :--- |
| D-DS1 | The drain is a total per-item successor function; each pass applies at most one transition per item | A clock-stamped batch embeds the observer's timing into identities and makes every item wait for the batch. |
| D-DS2 | A transition is keyed by item, attempt, stage, run and positions, with the attempt derived from prior runs | A store counter is a fact about passes, not items: crashed passes consume numbers and a replay diverges. |
| D-DS3 | Drain positions are reconstructed each pass and not stored in a column | `items.state` is the owner's vocabulary; a second stored state drifts from it after a crash. |
| D-DS4 | Five positions, with `held` carrying a reason from a closed set | One position per hold reason turns the transition table into dozens of identical rows. |
| D-DS5 | A dispatch returns when the worker starts; completion is read from stored runs and events | State held only in memory vanishes when the loop restarts, and live work cannot be told from lost work. |
| D-DS6 | Transitions apply in a fixed order with a per-item compare-and-swap; a stale item waits for the next pass | Running one item to completion inside a pass starves every other lane for a whole attempt. |
| D-DS7 | Live dispatch is bounded only by lane leases, backend windows and desk caps; `max_dispatches` is a simulation input | A per-pass dispatch cap throttles ready work, against [`momentum-principle.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/momentum-principle.md). |
| D-DS8 | The fork claim is computed from live leases and unfinished work | Passes overlap runs started by earlier passes; a pass-only claim lets two workers write one fork branch. |
| D-DS9 | The write boundary is bracketed per run, excusing lanes the loop itself changed | A batch-wide bracket cannot attribute an out-of-fence write to the run that made it once runs are asynchronous. |
| D-DS10 | The planner stays pure, with a read-only `tillr-drain plan` face that accepts simulation inputs | Without it, nobody can see what the next pass would do without changing the store. |
| D-DS11 | Pass timing is kept per generation in `drain_passes`, and a slow gate is flagged, not ticketed | Without pass timing a slow gate is invisible until someone times it by hand. |
| D-DS12 | Loop reports are named by generation range | A clock-named report collides when two loops start in the same second and changes across replays. |
| D-DS13 | Item holds, resource deferrals and faults are separate classes | One undifferentiated bucket hides whether the loop will move the item by itself. |
| D-DS14 | The successor model replaces the loop driver in increments, never in one change | A drain that is down while its replacement is incomplete lands nothing. |
| D-DS15 | Wall-clock time is excluded from identities and kept for finite lease expiry and record timestamps | A lease with no deadline cannot reap a crashed runner that had a timeout. |
| D-DS16 | The lane is re-leased with no expiry before settle and judge touch a candidate's worktree | Once the worker's lease is cleared, a concurrent dispatch could reset the worktree while the gate runs. |
