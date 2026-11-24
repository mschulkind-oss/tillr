---
title: "Drain Pass and Successor Reference"
status: accepted
stage: CURRENT
verified: 2026-11-20
verified_commit: 1552b7c90c8fdfd6cfac469dd8e476536c974d6e
created: 2026-11-15
updated: 2026-11-20
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
  - tools/tillr-drain/drain/schema.go
  - tools/tillr-drain/holdrules/holdrules.go
  - tools/tillr-run/run/lease.go
tags:
  - tillr
  - drain
  - state-machine
  - reference
summary: "Reference for tillr-drain's pure per-item successor function, derived positions and clock-free transition identities, compare-and-swap pass application, asynchronous run and candidate settlement, generation-keyed timing, and trigger-driven fixed-point loop."
depends-on:
  - ../design/board-utilities.md
  - ../design/landing-principles.md
  - ../design/momentum-principle.md
  - ./tillr-build-commit.md
vantage:
  status-chip: true
---

# Drain Pass and Successor Reference

> **Delivered copy.** This is `docs/reference/tillr-drain-successor.md` from this repository at commit `3b6faafdae` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-drain-successor.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Current reference, verified 2026-11-20 against `1552b7c90c8fdfd6cfac469dd8e476536c974d6e`. UNMEASURED: Go test suites verify replay-invariant identities, zero-write empty passes, non-blocking sibling dispatch, and trigger wakeups; live-night lane-idle reduction against the retired batch-tick loop is not measured in this document.

> **In short.** `tillr-drain` advances the queue through a pure per-item successor pass rather than a clock-stamped batch tick. Each pass reads one snapshot of the SQLite store and repository, computes at most one transition per item with clock-free identities, compare-and-swaps those transitions onto live leases, runs workers and candidate gates asynchronously without a batch barrier, and sleeps on triggers once the pass reaches a fixed point.

---

## Architecture and Component Boundaries

```text
  SQLite Store (.tillr/state.db) + Git Trunk / Open Questions
     items, item_edges, runs, lanes, events, retries, conflicts,
     node_runs, read_only_runs, workflow_runs, drain_item_faults
       |                                                    ^
       | 1. Reap expired/orphaned leases + 1 ReadTx         | 2. Per-item CAS WriteTx
       v                                                    |    + lazy gen-<n> allocation
  +---------------------------------------------------------+-----------------------+
  | Pure Planner (drain.Plan / Loop.passPlansGeneration in plan.go, pass.go)        |
  |   Derives Position -> Decision (dispatch | retry | settle | judge | hold)       |
  +---------------------------------------------------------+-----------------------+
                                                            |
          +-------------------------------------------------+--------------------+
          | dispatch / retry (ClaimLease)                   | settle / judge     |
          v                                                 v (NULL lease claim) |
  +-----------------------------------+       +----------------------------------+--+
  | Worker Goroutine (launchRun)      |       | Examination Goroutine               |
  |   TakeBoundary -> tillr-run exec  |       |   (launchExamination -> settle /    |
  |   -> closeAsyncBracket            |       |    verify -> tillr-gate / land.lock)|
  +-----------------+-----------------+       +------------------+------------------+
                    | run.Apply + completion                     | events + completion
                    +----------------------+---------------------+
                                           v
                    +--------------------------------------------+
                    | Trigger Wait (Loop.waitForTrigger)         |
                    |   completions | min(lease, 100ms) | stop   |
                    +--------------------------------------------+
```

| Subsystem | Owning files | Responsibility |
| :--- | :--- | :--- |
| Pure planner and identities | `tools/tillr-drain/drain/plan.go`, `run_id.go`, `schema.go` | Derives item positions, selects transitions or holds, assigns lanes/backends/forks, and computes clock-free run, transition, and decision IDs (`drain.Plan`). |
| Pass application and async brackets | `tools/tillr-drain/drain/pass.go`, `once.go`, `boundary.go` | Reaps expired and orphaned leases, snapshots per-item `inputVersion`, applies compare-and-swap transitions (`Loop.pass`, `Once`), and brackets each run's filesystem/Git scope. |
| Settlement and verification | `tools/tillr-drain/drain/loop.go`, `tools/tillr-run/run/lease.go` | Settles terminal worker runs (`Loop.settle`), verifies and lands candidates concurrently (`Loop.Verify`), and manages finite and `NULL`-expiry lane leases (`ClaimLease`, `ReapExpiredLeases`). |
| Dispatch ledger, generations, timing, and reports | `tools/tillr-drain/drain/dispatches.go`, `generation.go`, `timing.go`, `looprecord.go` | Enforces immutable `dispatches` rows, allocates monotonic `gen-<n>` ordinals (`drain_generations`), records pass phase timings (`drain_passes`), and writes `drain-<first>-<last>.json`. |
| Trigger loop and hold classification | `tools/tillr-drain/drain/trigger.go`, `tools/tillr-drain/holdrules/holdrules.go` | Blocks idle passes until worker completion, lease expiry, store poll, or stop request, and maps every `Reason` constant to an operator waiting class and mover. |

**Out of scope for this document:** uncommitted build-run continuation rules live in [`tillr-build-commit.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-build-commit.md), three-way squash landing rules live in [`landing-principles.md`](../design/landing-principles.md), and `tillr-view` pass/waiting projections live in [`observatory-redesign.md`](observatory-redesign.md).

---

## Principles and Invariants

- **Clock-free transition, decision, and run identities (`identifyDecision` in `plan.go`, `markRunID` in `run_id.go`).** Every `TransitionID`, `DecisionID`, and `RunID` is a pure function of item ID, attempt ordinal, workflow stage, run marker, positions, lane ID, and hold attributes (`TestReplayIsInvariantToClockRateAndTriggerSuppression`). Wall-clock `stamp(at)` survives only outside item transitions: rolling-trunk run IDs (`recordRollingRun` in `trunk.go`) and manual `tillr-drain resume` boundary correlations (`Resume` in `resume.go`).
- **Derived positions, never stored (`planItems` in `plan.go`, `readPendingHistory` in `pass.go`).** `items.state` holds only the owner's item states (`not-started`, `in-queue`, `blocked`, `landed`, `wont-do`, `resolved-elsewhere`). Each pass reconstructs an item's drain position (`queued`, `running`, `candidate`, `landed`, `held`) from `items`, `runs`, `lanes`, and the event fold.
- **Durable outcomes return through the store, never the call stack (`launchRun` and `readPendingHistory` in `pass.go`).** `dispatch` and `retry` claim the lane lease, launch the worker goroutine, and return immediately. `l.completions` is an in-process wakeup channel only; if the loop restarts mid-run or mid-gate, the next pass derives the exact position from stored `runs`, `lanes`, and `events`.
- **At most one transition per item per pass, applied compare-and-swap (`applyPlanRecorded` in `pass.go`).** A pass never chains `dispatch -> settle -> judge` synchronously for one item while others wait. Each item's write transaction verifies its `inputVersion` and definition digest before claiming resources, skipping (`stale`) any item whose inputs moved since planning.
- **Concurrent candidate gates, serialized ref updates (`launchExamination` in `pass.go`, `Loop.Verify` in `loop.go`).** Settled candidates run `tillr-gate` concurrently on their leased lanes without a loop-wide judge lock (`TestSettledLaneDispatchesNextItemWhileSiblingRuns`). Only trunk squash/fast-forward ref writes serialize under `land.lock`.
- **Empty fixed-point passes write nothing (`passPlansGeneration` in `pass.go`, `allocateGeneration` in `generation.go`).** A pass that neither reaps a lease nor applies a transition allocates no generation and writes zero rows to `dispatches`, `drain_generations`, `drain_passes`, or `events` (`TestRepeatedEmptyPassesWriteNothing`).

> [!WARNING]
> Do not add wall-clock timestamps to `TransitionID`, `DecisionID`, or `RunID`, and never `UPDATE` an existing `dispatches` row. `recordPassDecision` (`dispatches.go`) treats identical `decision_id` replays as no-ops and raises `decisionCollisionError` when any column differs, isolating the item under `decision-collision` (`settle-failed` during post-run settlement) rather than overwriting history.

> [!WARNING]
> Do not inspect or gate a finished run's worktree without reclaiming its lane lease first. `run.Apply` clears the worker's lease when recording the terminal `run.record`; `applyPlanTrunk` (`pass.go`) immediately reclaims `lanes` with `leased_until = NULL` for `settle` and `judge` so a concurrent pass cannot stage another item onto the candidate's worktree.

---

## The Successor Function and Derived Positions

`drain.Plan` (`plan.go`) and `Loop.passPlansGeneration` (`pass.go`) evaluate one `drain.pass` (`PassKind`) over a single `store.Reader` snapshot plus open document questions and trunk Git facts. For standalone simulation (`tillr-drain plan` and `tillr-drain once`), `PassInput` accepts optional `available_lanes`, `now`, and `max_dispatches` overrides; when omitted (and in the live loop), registered lanes are offered in `lane_id` order, the envelope clock checks finite lease expiries, and lane leases are the sole dispatch bound.

Every stored item derives one of five positions (`Decision.Position` / `Decision.From`):

| Position | Derived store condition |
| :--- | :--- |
| `landed` | `items.state` is terminal (`landed`, `wont-do`, or `resolved-elsewhere`). |
| `running` | An active `lanes` lease or `read_only_runs` group holds a run for the item, a terminal `runs` row has not yet been settled by the loop (`!work.Examined`), or a multi-node workflow is between stages (`work.Ready`, `WorkflowRestart`, or `workflow-invalidated`). |
| `candidate` | A settled attempt produced a pinned candidate commit (`refs/tillr/runs/<run_id>`) that has not yet been judged by `tillr-gate`, with no active lease on the item. |
| `queued` | `items.state == "in-queue"`, all `item_edges` blockers are satisfied (and proved in `refs/tillr/green` when `lane_base == "green"`), no open question or item hold applies, and either a transition (`dispatch`) is ready or only pass-scoped resources (`no-lane`, `fork-held`, comment-desk capacity) defer it. |
| `held` | Any non-terminal item blocked by an item-level fact (`not-started`, `blocked`, `blocked-failed`, `question-open`, `owner-only`, `trunk-unproved`, `item-fault`, or an unretried terminal failure/refusal). |

---

## Transition Table

Before planning items, `passPlansGeneration` acquires `withRunStartLock` and reaps two classes of stale leases in one write transaction: `run.ReapExpiredLeases` (`tools/tillr-run/run/lease.go`, freeing `lanes` with finite `leased_until <= now` and closing their `runs` rows as `timed-out`, while skipping `leased_until IS NULL`) and `reapDeadLoopRuns` (`pass.go`, closing orphaned `executing` runs whose `drain_sessions` loop owner is dead). Each item then takes at most one step:

| From | Action (`Transition`) | Condition | Effect and target position (`To`) |
| :--- | :--- | :--- | :--- |
| `queued` | `dispatch` | Item is ready, backend seat is open, fork is unclaimed, and a lane is free in the item's pool (express pool first for `item.Express`, `commentDesk` for comment items, `desk-trunk-fixer` fallback for fixers, or ordinary pool). | Stages the lane worktree, claims `lanes` (`leased_until = started_at + TimeoutMS + 300_000` ms, or `NULL` when `TimeoutMS == 0` or parallel `ReadOnlyGroup != ""`), records `drain.started` and `dispatches`, and launches the worker asynchronously -> `running`. |
| `running` | `dispatch` | Multi-node workflow has a completed stage and its next node (or evaluator restart) is ready (`workflow-next-node`). | Stages the next node (or parallel `read_only_runs` group) on the workflow's retained base commit and launches it -> `running`. |
| `running` | `settle` (`workflow-invalidated`) | Multi-node workflow item became `owner-only`, left `in-queue`, gained an unmet blocker, or opened a question while its lane lease is free. | Releases the workflow reservation (`WorkflowRelease`) without running another node -> `held`. |
| `running` | `settle` | Latest worker run wrote its terminal `runs` row and has not been examined (`!work.Examined`), and its lane is free to reclaim. | Reclaims `lanes` (`leased_until = NULL`) and runs `Loop.settle` asynchronously: closes/fails over on `AttemptInfrastructure`; continues uncommitted fenced build work in place ([`tillr-build-commit.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-build-commit.md)); checks `HEAD != base` **before** `AttemptUnjudged` so any committed run enters `Loop.Verify` (`candidate` -> `landed` or `held`/`running`); resumes an uncommitted walled run once (`!record.Resumed` via `resumeAfterWall`); and returns worker refusals (`returned-with-evidence`) until an identical repeat routes to design or owner. |
| `candidate` | `judge` | Pinned candidate commit (`refs/tillr/runs/<run_id>`) awaits verification, trunk paths/red-cap permit landing, and a lane is free. | Reclaims `lanes` (`leased_until = NULL`), checks fence scope, runs `tillr-gate` concurrently, and squashes onto trunk under `land.lock` (`landed`), or pins the candidate and returns/holds on refusal (`running` or `held`). |
| `held` | `retry` | Store holds an unconsumed `drain.retry` authorization (`retries.retry_id` absent from `dispatches.retry_id`) and lane/backend/fork resources are available. | Claims `lanes` under a new attempt ordinal, links `dispatches.retry_id`, and launches the worker -> `running`. |
| `queued` / `held` / `landed` | `hold` (`Transition: "none"`) | Resource deferral on a ready item (`Position: "queued"`), item-level hold (`Position: "held"`), or terminal state (`Position: "landed"`). | Mutates no lane or run state. |

---

## Identities, Records, and Holds

### Deterministic IDs

- **Attempt ordinal (`nextAttempt` in `plan.go`):** 1-based integer equal to `max(MAX(node_runs.attempt), maxAttemptFromRunIDs(runs), len(nonAcquittedPriors)) + 1`. Numeric parsing (`attemptFromRunID`) keeps ordinals monotonic past attempt 9 and across acquitted refusals (`identity_order_test.go`).
- **Run ID (`attemptRunID` in `plan.go`, `markRunID` in `run_id.go`):** `run-<item>-a<attempt>` for single-node runs and `run-<item>-a<attempt>-n<stage>` (1-based stage) for workflow nodes. In-attempt continuations append bounded suffixes via `markRunID` (`-<kind>` on the first turn, `-002-<kind>` through `-999-<kind>` thereafter, for `failover`, `restart`, `fresh`, and `interrupted`).
- **Transition ID (`identifyDecision` in `plan.go`):** `transition-<item>-a<attempt>[-n<stage>][-<run_id>]-<from>[-<to>]`, omitting `-<to>` when `Action == "hold"`.
- **Decision ID (`identifyDecision` in `plan.go`):**
  - Active transitions (`dispatch`, `retry`, `settle`, `judge`): `decision-<item>-a<attempt>[-n<stage>][-<run_id>]-<from>-<to>`, appending `-lane-<lane_id>` when `From == "candidate"` so re-examining a pinned candidate on another lane produces a distinct key.
  - Holds (`Action == "hold"`): `decision-<item>-a<attempt>[-n<stage>]-<from>-<sha256>`, where `<sha256>` hashes the JSON array `[reason, deferred_by, seat, question_id, retry_id, supersedes_run_id]`.

### Dispatch Rows and Hold Classes

`recordPassDecision` (`dispatches.go`) inserts into `dispatches` keyed by `decision_id`. In the live loop (`applyPlanRecorded` in `pass.go`), rows are written only for applied transitions (`dispatch`, `retry`, `settle`, `judge`) and the three candidate-blocking trunk holds (`trunk-path-dirty: …`, `trunk-rewritten: …`, and `red-cap: …` via `recordDirtyDecision`); ordinary holds write nothing so idle waits leave the store untouched. By contrast, `tillr-drain once` (`Once` in `once.go`) records every planned decision whenever at least one `decision_id` in the pass is new.

Holds divide into three operational categories:

1. **Pass-resource deferrals (`Position = "queued"`):** `no-lane`, `fork-held` (`DeferredBy` naming the item holding `.forks/lanes/<lane>/<fork>/` via `liveForks` across both live leases and unfinished `pendingWork`), `waits on a desk`, and `waits on the document's desk`. Ordinary repository items never block each other on path overlap at dispatch; only fork items sharing `refs/heads/patch/<fork>` serialize before landing. In `heldDetail` (`loop.go`), `no-lane` and `fork-held` report in a separate `deferred item(s)` summary group.
2. **Item holds (`Position = "held"`):** Facts about the item, its blockers, open questions, backend/quota windows, or unretried run endings. Every exported `Reason` constant maps to a waiting class and mover in `holdrules.Rules` (`tools/tillr-drain/holdrules/holdrules.go`); retired historical reasons (`capacity`, `inputs-changed`, `readmission-exhausted`, `retry-stale`, `conflict-exhausted`, `run-infra-exhausted`) stay in `holdrules.Rules` solely so historical `dispatches` rows still render.
3. **Faults:** `NewLoop` (`loop.go`) and `passPlansGeneration` (`pass.go`) treat a missing usable backend policy (`ReasonNoPolicy`) as a startup/pass error (`StopFault`) rather than holding items (`ReasonNoPolicy` appears only in standalone `Plan` calls without a policy). Per-item invariant faults (`decision-collision`, `settle-failed`, `workflow-invalid`) record in `drain_item_faults` and hold only the affected item as `item-fault: …` until cleared with `tillr-drain clear-fault`.

---

## One Pass and Compare-and-Swap Application

`readItems` (`plan.go`) orders every pass deterministically: `fixer-main-*` items first, `rewrite-item-*` items second, ranked items in lexical `rank` order, and unranked items in `item_id` order. When an item is owed an in-place worker return or design continuation on a specific lane, `planItems` reserves that lane (`workerLanes`) ahead of general `queued` dispatches, releasing unused reservations if another ready item would otherwise be `laneShort`.

Application (`applyPlanRecorded` in `pass.go`) separates external I/O from short SQLite write transactions:

1. **Snapshot version (`inputVersionFrom` in `pass.go`):** During the pass's read transaction, each planned item records its `definitionSHA` and an `inputVersion` digest covering item columns, `updated_at`, latest `runs` state and `event_seq`, target lane `state`/`run_id`/`leased_until`, `pendingWork`, `drain_item_faults`, triage version, and `owner-only` criteria. In-memory staging refusals (`Loop.heldFor`) suppress re-staging an item with an unchanged `updated_at` when `IsItemContent` holds.
2. **External preparation (`writeGathered` in `pass.go`):** Git base resolution, worktree staging (`LaneStager.Stage`), and file reads happen outside SQLite locks.
3. **Per-item compare-and-swap (`applyPlanTrunk` in `pass.go`):** A short `WriteTx` re-reads `currentDefinition` and `inputVersion`, re-verifies lane availability, `liveForks`, comment-desk caps, and fast-forward trunk state (`trunkAdvancedUntouched`), lazily allocates the pass's `gen-<n>` on the first applied transition, claims the lease, appends `drain.started`, and calls `recordPassDecision`. Any version or resource mismatch aborts only that item's claim (`applied = false`, `stale = true`) so the next pass replans from fresh facts.
4. **Per-run boundary bracket (`TakeBoundary` in `boundary.go`, `closeAsyncBracket` in `pass.go`):** Each worker goroutine snapshots its own target worktree, watched idle sibling lanes, `SiblingDirs`, `LaneRoot` (`BoundaryScopeRun`), and `PatchRefs` (omitting `Store` while `tillr-run exec` writes legitimately). Before diffing snapshots at run exit, `closeAsyncBracket` clears worktree status for any sibling lane whose `laneChanges` epoch advanced while this worker was running, preventing concurrent legitimate dispatches from convicting a sibling run.

---

## Generations, Timing, and the Trigger Loop

### Generations, `drain_passes`, and Loop Reports

- **Generations (`generation.go`):** `drain_generations (generation INTEGER PRIMARY KEY)` allocates `"gen-<n>"` inside the first write transaction of a pass that mutates state (lease/orphan reaping or the first applied transition, or `Once` recording a new decision). Every `Decision.PassID`, `run.Request.CorrelationID`, and `PassTiming.TickID` in that pass shares that `"gen-<n>"`, and the sequence continues monotonically across loop processes (`trigger_test.go`).
- **Pass timing (`timing.go`, `pass_observation.go`):** `drain_passes` (keyed by `tick_id`) records `stage_ms`, `decision_counts`, and `applied_count` when the pass finishes planning (`observePass`), and accumulates asynchronous worker `dispatch_ms` plus candidate `gate_ms`, `gate_max_ms`, `land_ms`, `gate_count`, and runner-change `runner_*` columns back onto the originating generation row (`recordAccumulatedPassTiming`). `DefaultGateBudget` (`330s`) compares against `GateMaxMS` (longest single ordinary gate run, excluding `RunnerGateMS`), setting `gate_over_budget = 1` and logging a warning without filing an item or stopping the loop. On write-open, trigger `drain_copy_legacy_runner_passes` copies any legacy `drain_ticks` rows into `drain_passes` via `INSERT OR IGNORE`.
- **Loop result files (`looprecord.go`):** `RecordLoopResult` writes `<root>/.tillr/loop/drain-<first>-<last>.json` with envelope `ID: "loop-<first>-<last>"` using numeric generation endpoints (`drain-0-0.json` / `loop-0-0` when a loop exits before allocating any generation, archiving any prior `drain-0-0.json` to `drain-0-0-<unixNano>.json`).

### Fixed-Point Loop and `waitForTrigger`

`Loop.Run` (`pass.go`) repeats `Loop.pass` without sleeping whenever a pass applies work (`timing.Applied > 0`), reaps a lease (`timing.TickID != ""`), hits a compare-and-swap skip (`stale`), or runs a rolling check. When a pass reaches a fixed point with nothing applied:

- **In-flight work or timed hold:** If workers or examinations are active in-process (`len(l.active) > 0`), a surviving foreign run holds `run-live` / `no-lane`, a quota-interrupted run holds `backend-window`, or a heavy proof is running (`l.heavyInFlight()`), `pass` calls `waitForTrigger`.
- **Drained fixed point (`planNothing` in `loop.go`):**
  - When `len(leases) == 0`: `tillr-drain loop` is a daemon by default (`ExitWhenDrained == false`), logging the waiting summary once per distinct detail and calling `waitForTrigger`; passing `--exit-when-drained` (`ExitWhenDrained == true`) exits 0 with `StopQueueDrained` (`"queue-drained"`).
  - When `len(leases) > 0` and any foreign lease has `LeasedUntil == nil` (with no active or surviving loop run): returns `StopLanesLeased` (`"lanes-leased"`) naming `tillr run release <lane>`.
  - When all remaining foreign leases have finite deadlines: logs the held/deferred summary and calls `waitForTrigger`.

`waitForTrigger` (`trigger.go`) blocks until one of three wakeups fires:

1. **In-process completion (`<-l.completions`):** drains all ready `runCompletion` notifications (`takeCompletion`) and immediately starts the next pass.
2. **Store poll or earliest finite lease expiry (`time.NewTimer`):** wakes after `min(StorePollInterval, earliest(lease.LeasedUntil - now))` where `DefaultStorePollInterval` is `100ms`, checks `drain_sessions.stop_requested` (`checkStopRequested`), and starts the next pass so external store writes (`tillr item`, `tillr drain retry`, foreign `tillr run` completions) and expiring leases are observed promptly (`TestNewStoreFactDispatchesBeforeOwnedRunEnds`).
3. **Operator stop (`<-ctx.Done()` or `stop_requested`):** cancels active workers, waits for in-flight completions, records `ended_at` on `drain_sessions`, and exits with `StopOperatorStop` (`"operator-stop"`).

---

## Why it's this way

Each row preserves an engineering decision (`D-DS1`–`D-DS16`) that a maintainer reading only the current code might otherwise undo. On 2026-11-20 the owner confirmed `D-DS1`–`D-DS16` as settled engineering detail in [`OQ-PR4`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR4) (with future node-policy amendments tracked in [`tillr-graph-is-the-path.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-graph-is-the-path.md#122-decisions-in-other-documents)).

| ID | Decision | Why it stays |
| :--- | :--- | :--- |
| D-DS1 | Structure the drain as a total per-item successor function (`Plan` / `passPlansGeneration`) where each pass applies at most one transition per item | Finer wall-clock tick stamps (nanoseconds or run counters) still embed observer timing into identities and keep the batch barrier. |
| D-DS2 | Key transitions by `(<item>, <attempt>, [stage], [run_id], <from>, <to>)` with attempt ordinals derived from prior runs rather than a store counter | A shared store counter is a pass fact rather than an item fact, burning numbers on crashed passes and diverging on replay. |
| D-DS3 | Reconstruct each item's drain position (`queued`, `running`, `candidate`, `landed`, `held`) from `items`, `runs`, `lanes`, and `events` instead of storing a `drain_state` column | `items.state` belongs to the owner's vocabulary; a second stored state column creates a dual-write drift hazard after crashes. |
| D-DS4 | Keep a five-position state set where `held` carries a closed `Reason` rather than promoting every hold reason to its own state | Promoting ~30 hold reasons into top-level states bloats the transition table into dozens of identical `none` rows. |
| D-DS5 | Return from `dispatch`/`retry` as soon as the worker starts and derive completion from stored `runs` and `events` rather than call-stack state | In-memory-only run state vanishes if the loop restarts mid-run, leaving no way to tell live work from lost work. |
| D-DS6 | Apply transitions in deterministic priority/rank order with per-item `inputVersion` compare-and-swap, skipping stale items to the next pass | Running `dispatch -> settle -> judge` to completion inside one synchronous pass starves sibling lanes for an entire attempt. |
| D-DS7 | Bound live dispatches only by lane leases, backend windows, and desk caps, keeping `max_dispatches` strictly as an optional `drain.pass` simulation cap *(originally paired with a serial judge, since widened to concurrent `tillr-gate` runs serialized only at `land.lock`)* | Artificial per-pass dispatch caps throttle ready work in violation of [`momentum-principle.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/momentum-principle.md). |
| D-DS8 | Compute `fork-held` (`liveForks`) from live lane leases and unfinished `pendingWork`, not merely dispatches within the current pass | Because passes overlap in-flight runs from earlier passes, a pass-only fork set would let two workers mutate `refs/heads/patch/<fork>` concurrently. |
| D-DS9 | Bracket filesystem and Git scope per worker run (`BoundaryScopeRun` in `boundary.go` and `pass.go`, excusing sibling `laneChanges` epochs) rather than across a whole batch | A batch-wide bracket cannot attribute an out-of-fence write to the run that caused it once runs execute asynchronously. |
| D-DS10 | Preserve the pure `Plan` / `apply` split and the read-only `tillr-drain plan` CLI face with optional simulation overrides | Without a pure `Plan` seam, operators and tests cannot inspect what the next pass would do without mutating the store. |
| D-DS11 | Replace `drain_ticks` with generation-keyed `drain_passes` (migrating legacy rows via `INSERT OR IGNORE` and flagging `gate_over_budget` against `GateMaxMS`, reporting overruns without filing tickets) | Dropping pass-level phase timing with the retired tick would make slow gates invisible until timed by hand. |
| D-DS12 | Name loop result files by inclusive generation range (`drain-<first>-<last>.json`, `drain-0-0.json` on zero-generation exits) rather than wall-clock timestamps | Second-stamped report filenames collide when two loops start within one second and change across deterministic replays. |
| D-DS13 | Separate item holds (`Position = "held"`), pass-resource deferrals (`no-lane`, `fork-held`, desk waits at `Position = "queued"`), and policy/item faults (`ReasonNoPolicy`, `drain_item_faults`), retiring `capacity` | Collapsing resource waits, item blockers, and configuration faults into one undifferentiated hold bucket hides whether the loop will retry on its own. |
| D-DS14 | Land the successor architecture incrementally (starting from derived transition IDs before replacing the loop driver and retiring tick names) | Rewriting the drain in one flag day would halt nightly autonomous landings while the replacement is incomplete. |
| D-DS15 | Exclude wall-clock time from identities while retaining it for finite lease-expiry comparisons (`ReapExpiredLeases`) and record `created_at` metadata | Leases without wall-clock expiry deadlines cannot reap crashed foreign runners that hold a finite timeout. |
| D-DS16 | Reclaim `lanes` with `leased_until = NULL` before `settle` and `judge` inspect or gate a candidate worktree | Once `run.Apply` releases the worker lease on exit, an unleased candidate worktree could be claimed and reset by a concurrent pass's dispatch while `tillr-gate` is running. |
