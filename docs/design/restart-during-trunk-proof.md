---
title: "Starting detached candidate gates while a pending loop restart waits on an active trunk proof"
status: in-review
stage: DECIDED
stage-since: 2026-11-25
updated: 2026-11-25
tags:
  - tillr
  - drain
  - restart
  - gate
  - proof
  - why
  - design
summary: "On 2026-11-25 on loop build 79bdcbbf, two landings under tools/ requested a drain restart nine and eleven minutes into a 33-to-104-minute trunk proof; the restart waited on restartProofActive while acquireGateStart blocked every new candidate gate on waitForRestartAnswer, leaving six finished candidates holding their lanes at load ~45 while tillr drain why said only 'lane kept: candidate awaits pin verification and lane release'. This design settles which wait gives way: the restart keeps waiting on the active trunk proof so proof work is never discarded and a red proof names its landing before exec, while detached ProcessSteps candidate gate starts stop waiting on the pending restart during an active trunk proof and tillr drain why names the pending loop restart whenever a candidate waits on one."
next: "Build the single item in Section 6 covering tools/tillr-drain/drain/, targeted drain tests, and just pack-sync; the owner may review or overturn designer decisions D-RTP1 and D-RTP2."
depends-on:
  - landing-principles.md
  - ../reference/tillr-restart.md
  - ../reference/gate.md
  - tillr-judge-and-lifecycle.md
  - tillr-watch/decision-record-files.md
  - ../tillr/decision-provenance-review.md
vantage:
  status-chip: true
---

# Starting detached candidate gates while a pending loop restart waits on an active trunk proof

**Status:** Verified at `79bdcbbf` on 2026-11-25; nothing specified here is built yet. In [`restart_work.go`](../../tools/tillr-drain/drain/restart_work.go), `acquireGateStart` calls `waitForRestartAnswer(ctx, false)` whenever `h.loop.restartProofActive()` is true, holding every new detached candidate gate for the rest of the running trunk proof; in [`why.go`](../../tools/tillr-drain/drain/why.go), `WhyForItem` never reads `restart.ReadState(r, restart.RoleDrain)` for a held candidate and falls through to `"candidate awaits pin verification and lane release"`. Decisions [`D-RTP1`](#D-RTP1) and [`D-RTP2`](#D-RTP2) in the [Decision Ledger](#decision-ledger) are designer decisions made on a filed request; neither is an owner ruling, and the owner may overturn either of them.

> **In short.** When a `drain.restart-request` arrives during an active trunk proof, **gate starts waiting on the restart give way** while **the restart waiting on the active trunk proof stays** ([`D-RTP1`](#D-RTP1)). Detached candidate gates (`--tillr-gate-supervisor`) hold `restartMechanical` only for milliseconds at spawn, survive `syscall.Exec` when a proof ends mid-gate, and already settle and free their lanes during a proof when they finish early — whereas replacing the loop mid-proof would kill 33 to 104 minutes of active proof work on every `tools/` landing and cut off red-proof culprit attribution before it names the breaking commit. Whenever a candidate actually waits on an unanswered `drain` restart, `tillr drain why` names that pending loop restart instead of reporting pin verification ([`D-RTP2`](#D-RTP2)).

- **Why it matters.** Under [`D-RT6`](../reference/tillr-restart.md#why-its-this-way), every landing touching `roll.paths` (`tools/` here) files a `drain.restart-request`. Because almost every change in this repository touches `tools/` and trunk proofs run 33 to 104 minutes, holding detached gate starts behind a proof-deferred restart freezes candidate gating and parks lanes idle for most of every trunk proof — dropping machine load from ~150 to ~45 on 2026-11-25 while six finished candidates sat unexamined ([Section 1](#1-the-2026-11-25-measurement-and-confirmed-source-reading)).
- **The shape.** Two changes in [`tools/tillr-drain/drain/`](../../tools/tillr-drain/drain/) ([Section 3](#3-which-wait-gives-way-and-the-state-machine-across-a-trunk-proof) and [Section 4](#4-reporting-a-candidates-pending-restart-wait-in-tillr-drain-why)):
  1. `acquireGateStart` in [`restart_work.go`](../../tools/tillr-drain/drain/restart_work.go) stops waiting on `waitForRestartAnswer` while `restartProofActive()` is true, while keeping `restartHoldWaiting` armed across the proof so the instant `heavyProofActive` clears, post-proof gate starts and post-gate settlements yield to `gateRestartWith` (`D-RTP1`).
  2. `WhyForItem` in [`why.go`](../../tools/tillr-drain/drain/why.go) checks `restart.ReadState(r, restart.RoleDrain)` and reports `pending loop restart <request_id>` in `TableReason()`, `WaitingOn`, `Mover`, `LaneReason`, and `Why` when an examining candidate is held behind an unanswered restart (`D-RTP2`).
- **Cost.** One self-contained build item ([Section 6](#6-follow-on-build-item)) touching [`restart_work.go`](../../tools/tillr-drain/drain/restart_work.go), [`why.go`](../../tools/tillr-drain/drain/why.go), drain tests, and `just pack-sync`, with zero schema migrations, zero new flags, and zero timers or fallbacks.
- **Needs your ruling:** None. Governing owner rulings are cited in [Section 2](#2-governing-rulings-and-landing-principles); [`D-RTP1`](#D-RTP1) and [`D-RTP2`](#D-RTP2) are designer decisions that the owner may overturn.

---

## 1. The 2026-11-25 measurement and confirmed source reading

### 1.1 Production measurement on build `79bdcbbf`

Measured on 2026-11-25 (EST) on loop build `79bdcbbf`:

- **`12:11`:** `tillr-drain` restarted in place onto `79bdcbbf` and started a trunk proof of `79bdcbbf`. Trunk proofs in this repository take 33 to 104 minutes.
- **`12:17:09`:** the last `gate.started` event before gating froze. The five candidate gates already running finished and settled by `12:39`.
- **`12:20:47` and `12:22:15`:** two landings touching `tools/` each recorded a `drain.restart-request` for role `drain`. Neither request was answered at `12:47`.
- **From `12:26` onward:** six finished worker runs recorded candidates (`4d1ca1e121`, `7913681e82`, `a7d9ba017d`, `26f0d1b1d4`, `d570a35de8`, and `a2f8117307`) and none started a gate [unlanded]. Each candidate kept its lane lease, and `tillr drain why` reported only `"lane kept: candidate awaits pin verification and lane release"`.
- **Machine utilization:** load average fell from ~150 to ~45 while six finished changes waited on idle processors.

### 1.2 Confirmed source reading at `79bdcbbf`

All three parts of the request's source reading hold in the tree at `79bdcbbf`:

1. **The pending restart waits while a trunk proof is active (`gateRestartWith` in [`restart.go`](../../tools/tillr-drain/drain/restart.go) and `startHeavy` in [`heavy_loop.go`](../../tools/tillr-drain/drain/heavy_loop.go)).**
   When `restarterPendingWith` sees an unanswered `drain.restart-request`, `gateRestartWith` calls `l.markRestartPending()`, checks `if l.restartProofActive()`, calls `l.markRestartWaitingOnHold()`, and returns `false, "", nil`. `restartProofActive()` reads `l.heavyProofActive`, which `beginHeavyProof` sets before `HeavyRunner.RunAttempt` and clears only after `RunAttempt` finishes — including both the tip's tree checks and, since commit `d7595176` removed `BeforeReplay`, any first-red bisection across `refs/tillr/green..main`.

2. **Detached candidate gate starts wait on the pending restart across the entire proof (`acquireGateStart` in [`restart_work.go`](../../tools/tillr-drain/drain/restart_work.go)).**
   - Commit `08cf6934` introduced `lockRestartGateStart` and `acquireGateStart` so `ProcessSteps` candidates in `Loop.verify` ([`loop.go`](../../tools/tillr-drain/drain/loop.go)) acquire a brief `restartMechanical.RLock` across `prepareCandidate`, `judgeCandidate`, and `startOrAttachDetachedGate`, then immediately release `restartMechanical` inside `restartWork` while `--tillr-gate-supervisor` runs.
   - Commit `15494d44` added `restartHoldWaiting` gated on `!h.loop.restartProofActive()` (`waitForRestartAnswer(ctx, true)`) so back-to-back gate starts could not starve `gateRestartWith`'s `restartMechanical.TryLock()` **outside** an active trunk proof, while still letting detached gates start during a proof.
   - Commit `f0eb8fa0` closed `beginHeavyProof` against starting a second trunk proof ahead of a pending restart, pre-armed `markRestartWaitingOnHold()` when `restartProofActive()` was true, **and** rewrote `acquireGateStart` to block on `waitForRestartAnswer(ctx, false)` whenever `h.loop.restartProofActive() || h.loop.isRestartWaitingOnHold() || h.loop.restartDefersToSettlement()`. Because `claimSettledCandidateJudge` ([`settle_judge.go`](../../tools/tillr-drain/drain/settle_judge.go)) still admits `ProcessSteps` candidates into `runCandidateExamination`, each of the six candidates claimed its lane's unbounded examination lease (`leased_until IS NULL`) and parked inside `acquireGateStart` -> `waitForRestartAnswer(ctx, false)` before `startOrAttachDetachedGate` could run.

3. **`tillr drain why` hides the pending restart behind the generic pin-verification fallback (`pendingDecisionInternal` in [`pass.go`](../../tools/tillr-drain/drain/pass.go) and `WhyForItem` in [`why.go`](../../tools/tillr-drain/drain/why.go)).**
   Each parked candidate had `work.Candidate != nil`, `!work.LaneReleased`, and an active examination lease on `d.LaneID`. In [`pass.go`](../../tools/tillr-drain/drain/pass.go), `pendingDecisionInternal` planned `ActionJudge` (`From: "candidate", To: "landed"`), saw the active lease on `d.LaneID` via `activeLeasesExcept`, and rewrote the decision to `Decision{From: "candidate", Action: ActionHold, Reason: ReasonNoLane, LaneID: d.LaneID}`. In [`why.go`](../../tools/tillr-drain/drain/why.go), `WhyForItem` set `row.WaitingOn = []string{d.LaneID}` (`row.Mover = "drain; " + d.LaneID`) and, under `work != nil && !work.Done && d.Action == ActionHold && !liveWorker`, fell through empty `work.LaneKeepReason` and `candidateKeepReason` to `row.LaneReason = "candidate awaits pin verification and lane release"` without consulting `restart.ReadState(r, restart.RoleDrain)`.

---

## 2. Governing rulings and landing principles

### 2.1 Settled owner rulings that constrain this design

No new owner question is opened because five settled owner rulings bound the solution space directly:

- **A red proof names its landing before a restart** (enforced in commit `d7595176` by removing `BeforeReplay` from [`heavy_loop.go`](../../tools/tillr-drain/drain/heavy_loop.go), and paired with automatic culprit revert in [`D-GR6`](../reference/gate.md#D-GR6)). Once a trunk proof fails on a tip, its first-red search (`--heavy-tree --only <id>`) completes, names `first_red_landing`, and reverts or files `fixer-main-<sha>` before `heavyProofActive` clears and `gateRestartWith` replaces the loop.
- **No fallbacks** ([`R-TW-DR2`](./tillr-watch/decision-record-files.md#R-TW-DR2), 2026-11-16: `"Explicitly, we NEVER want fallbacks anywhere in this project."`). Neither wait is worked around with a secondary mode or degraded path.
- **No time limits, retry caps, or load cutoffs** ([`D-JL9`](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9) and [`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10), 2026-11-17; [`D-GR15`](../reference/gate.md#D-GR15), 2026-11-17: `"target budgets, watch for stalls, but don't let 'load is high' cause failures that would otherwise finish just fine."`; [`D-GR17`](../reference/gate.md#D-GR17), 2026-11-19). Coordination between gate starts, proofs, and `exec` uses exact state transitions, never grace timers or numeric caps.
- **In-place loop replacement without waiting on detached runs, triggered on every `roll.paths` landing** ([`D-RT3`](../reference/tillr-restart.md#why-its-this-way) and [`D-RT6`](../reference/tillr-restart.md#why-its-this-way), 2026-11-15). Landings under `tools/` keep requesting a restart on every commit, and running detached work survives `exec` rather than holding replacement or being restarted from scratch.
- **Separation of owner rulings from engineering detail** ([`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger) and `D-PR2` in [`decision-provenance-review.md`](../tillr/decision-provenance-review.md#6-how-this-stops-happening), 2026-11-19). Engineering choices within settled rulings are marked as the designer's so the owner may overturn them without holding the build.

### 2.2 Check against Landing Principles

| Principle in [`landing-principles.md`](./landing-principles.md) | How this design satisfies it |
| :--- | :--- |
| **Opening rule: cost must not rise with trunk activity** | Landings under `tools/` neither freeze candidate gating across 33-to-104-minute proofs nor abort active trunk proofs as landing rate rises. |
| **[§3 Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once)** | A detached candidate gate started during a trunk proof runs once; if the proof finishes first and the loop `exec`s, `reclaimDetachedGateExaminationsWith` reattaches to the running `--tillr-gate-supervisor` instead of running a second gate. |
| **[§7 A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report)** and **[§13 A held state names a mover that can still act](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)** | `tillr drain why` names `pending loop restart <request_id>` as what holds a candidate waiting on a restart (`D-RTP2`), rather than misreporting `"candidate awaits pin verification and lane release"`. |
| **[§11 A failure is diagnosed, not waited out](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out)** | Uses lock-state predicates (`!restartProofActive()` with pre-armed `restartHoldWaiting`), with zero wall-clock timeouts, sleeps, or retry counters. |
| **[§12 A principle cites the measurement that produced it](./landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it)** | Grounded in the 2026-11-25 `79bdcbbf` timeline (`12:11`–`12:47` EST, six held candidates, load ~150 to ~45). |
| **[§15 Land the smallest functional change](./landing-principles.md#15-land-the-smallest-functional-change)** | Ships as one focused build item in [`tools/tillr-drain/drain/`](../../tools/tillr-drain/drain/) ([Section 6](#6-follow-on-build-item)). |

---

## 3. Which wait gives way, and the state machine across a trunk proof

### 3.1 Why gate starts waiting on the restart give way (`D-RTP1`)

Two waits met on 2026-11-25:

1. **The pending restart waiting on the active trunk proof** (`restartProofActive` in [`restart.go`](../../tools/tillr-drain/drain/restart.go)); and
2. **Detached candidate gate starts waiting on the pending restart** (`acquireGateStart` in [`restart_work.go`](../../tools/tillr-drain/drain/restart_work.go)).

By the designer's decision ([`D-RTP1`](#D-RTP1), which the owner may overturn), **detached candidate gate starts waiting on the pending restart give way while a trunk proof is active (`restartProofActive() == true`)**, and **the pending restart keeps waiting on the active trunk proof**:

| Question | If the restart's wait on the trunk proof gave way | When gate starts' wait on the restart gives way during an active proof (`D-RTP1`) |
| :--- | :--- | :--- |
| **Effect on the 33–104 min trunk proof** | `HeavyRunner` runs `--tillr-heavy-supervisor` bound to a close-on-exec `rollingOwnership` pipe ([`restart_rolling.go`](../../tools/tillr-drain/drain/restart_rolling.go)). `syscall.Exec` closes that pipe and kills the active proof; because nearly every landing touches `tools/`, repeated restarts discard proof work and starve `refs/tillr/green`. | The active trunk proof runs uninterrupted to its green or red verdict; `beginHeavyProof`, `BeforeTip` (after the first tip), `startHeavy`'s retry loop, and `offerGreenAdvance` (`a52e8d0c`) still prevent any **second** proof or post-proof green push from starting before `exec`. |
| **Red-proof culprit attribution** | Replacing before `RunAttempt` finishes cuts off first-red bisection (`firstRed`) before the breaking landing is named — or requires re-architecting multi-step bisection, auto-revert, and fixer filing out of process. | `heavyProofActive` stays `true` through `RunAttempt` (`d7595176`), so a red proof finishes naming its first red landing and reverting or filing `fixer-main-<sha>` before `gateRestartWith` replaces the loop. |
| **Candidate gates during the proof** | Candidates wait on restart ceremonies or re-run interrupted proofs. | `--tillr-gate-supervisor` ([`D-GR43`](../reference/gate.md#D-GR43)) starts immediately in its own session (`Setsid: true`); gates finishing during the proof (typically 1–8 minutes against a 33–104 minute proof, as the five gates ending by `12:39` showed) settle, land or return refusal, and release their lanes immediately via `enableRestartDuringProof` / `lockRestartLanding` (`b96012c8`, `f3c48d25`). |
| **Loop replacement when the proof ends** | — | Running detached gates hold **no** `restartMechanical` lock (`restartWork` releases `RLock` right after `startOrAttachDetachedGate`). When the proof ends, `gateRestartWith` takes `TryLock()` immediately, `releaseRestartExaminations` retains the lane lease (`leased_until = NULL`), `exec` runs at once, and `reclaimDetachedGateExaminationsWith` reattaches in the new process. |

### 3.2 Lock protocol in `restart_work.go` from proof start through `exec` (`D-RTP1`)

Keeping `f0eb8fa0`'s `beginHeavyProof` guard and pre-armed `markRestartWaitingOnHold()` while restoring `!h.loop.restartProofActive()` in `acquireGateStart` closes both the 2026-11-25 gate freeze and the post-proof `TryLock` race without timers or counters:

1. **While a trunk proof is active (`restartProofActive() == true`):**
   - When `gateRestartWith` ([`restart.go`](../../tools/tillr-drain/drain/restart.go)) or `armRestartPending` ([`restart_work.go`](../../tools/tillr-drain/drain/restart_work.go)) observes a pending restart during an active proof, it keeps calling `markRestartWaitingOnHold()` (`restartPendingWait != nil` and `restartHoldWaiting = true`), then defers replacement until the proof finishes.
   - In `acquireGateStart` ([`restart_work.go`](../../tools/tillr-drain/drain/restart_work.go)), both the pre-lock wait and the post-`RLock` recheck run **only when `!h.loop.restartProofActive()`** (and `!h.loop.restartActionDeferred()`):
     - While `restartProofActive()` is `true`, `acquireGateStart` skips `waitForRestartAnswer`, acquires `restartMechanical.RLock` (`h.settling = true`, so `restartDefersToSettlement()` is also `true`), runs `prepareCandidate` and `judgeCandidate` (where `buildRestartPending` already returns `false, nil` because `restartDeferred() || restartDefersToSettlement()` holds, validating saved workflow `JudgeReceipt` files without `errRestartPending`), and spawns or attaches `--tillr-gate-supervisor` via `startOrAttachDetachedGate`.
     - `awaitDetachedGate` runs inside `restartWork`, which calls `h.release()` (`restartMechanical.RUnlock` and `restartSettlements.Add(-1)`) for the entire lifetime of the gate process.
     - If the detached gate finishes (pass or fail) while `restartProofActive()` is still `true`, `enableRestartDuringProof(stepCtx)` sets `h.allowDuringProof = true`, so `restartWork`'s re-acquisition (`waitForRestartAnswer(ctx, true)`) returns `nil` immediately and the candidate settles, lands (or records its refusal), and frees its lane during the proof.

2. **At the exact instant the trunk proof ends (`heavyProofActive` clears to `false`):**
   - Because `restartHoldWaiting` was **already** armed to `true` while the proof ran, `!h.loop.restartProofActive() && h.loop.isRestartWaitingOnHold()` becomes `true` at the moment `heavyProofActive` flips to `false` — even before the next pass calls `gateRestartWith`.
   - Any new candidate reaching `acquireGateStart` after `heavyProofActive` clears sees `!h.loop.restartProofActive() && h.loop.isRestartWaitingOnHold()`, calls `waitForRestartAnswer(ctx, true)`, and waits behind the pending restart instead of taking a new `RLock`.
   - Any detached gate that was spawned during the proof and finishes in the narrow gap after `heavyProofActive` clears sees `allowDuringProof && l.restartProofActive() == false` in `waitForRestartAnswer(ctx, true)` and likewise waits behind the pending restart, with its `gate-done.json` persisted on disk.
   - Any candidate that was mid-spawn or mid-landing inside its brief `RLock` at the instant `heavyProofActive` cleared finishes that millisecond/second step and releases `RLock`; with new `RLock` acquisitions blocked by `isRestartWaitingOnHold()`, `gateRestartWith`'s `restartMechanical.TryLock()` is guaranteed to succeed on the next wake without starvation.
   - `beginHeavyProof`, `BeforeTip` (after the first tip), `startHeavy`, and `offerGreenAdvance` (`restartStopsNextHeavyProof`) refuse to start a second trunk proof or green push before `gateRestartWith` answers the restart.
   - `gateRestartWith` calls `releaseRestartExaminations` (retaining `leased_until = NULL` for every live or finished-on-disk detached gate) and `syscall.Exec`s into the new binary, which answers `restarted` in `adoptRestart` and reclaims all live and finished-on-disk detached gates via `reclaimDetachedGateExaminationsWith`.

3. **Outside a trunk proof (`restartProofActive() == false`) and on non-detached `Steps`:**
   - Outside a trunk proof, `acquireGateStart` behaves identically to `15494d44`: when `isRestartWaitingOnHold() || restartDefersToSettlement()` holds, new gate starts wait on `waitForRestartAnswer(ctx, true)` so overlapping `RLock`s never starve `TryLock()`; when neither holds, a ready candidate starts its detached gate and drops `RLock` so `gateRestartWith` can replace while the gate runs (`TestReadyCandidateGateStartsWhileRestartPendingAndLoopReplacesWhileGateRuns` in [`detached_gate_restart_test.go`](../../tools/tillr-drain/drain/detached_gate_restart_test.go)).
   - Non-`ProcessSteps` implementations in `Loop.verify` ([`loop.go`](../../tools/tillr-drain/drain/loop.go)) run gates synchronously while holding `restartMechanical` and continue using `lockRestartSettlement` (`allowDuringProof = false`, `allowPendingStart = false`).

---

## 4. Reporting a candidate's pending-restart wait in `tillr drain why`

### 4.1 When a candidate row waits on a pending loop restart (`D-RTP2`)

Once `D-RTP1` is built, candidates no longer stall during an active trunk proof under `ProcessSteps`, yet a candidate can still legitimately wait on an unanswered `drain.restart-request`:

- in the drain window **after** a trunk proof ends (or outside a proof while existing `restartMechanical` holds drain) before `gateRestartWith` runs `exec` and `adoptRestart` records `drain.restart-answer`;
- across `releaseRestartExaminations` while `gate-done.json` awaits reattachment on a not-yet-answered restart; or
- whenever an examining candidate holds its lane lease with no detached gate running while a pending restart blocks its examination (including non-detached `Steps` or direct store fixtures reproducing the 2026-11-25 state).

By the designer's decision ([`D-RTP2`](#D-RTP2), which the owner may overturn), `WhyForItem` in [`why.go`](../../tools/tillr-drain/drain/why.go) reads `restart.ReadState(r, restart.RoleDrain)` once per `WhyForItem` call. Let `pendingDrainRestartID` be `state.Pending[len(state.Pending)-1].ID` when `len(state.Pending) > 0` (the latest unanswered `drain` request, matching `Restarter.Settle` in [`tools/tillr-drain/restart/restart.go`](../../tools/tillr-drain/restart/restart.go)).

A row in `WhyForItem` represents a **candidate waiting on that pending loop restart** when all of the following hold:

1. `pendingDrainRestartID != ""` (an unanswered `drain.restart-request` for role `drain` exists in the store);
2. `work != nil && !work.Done && work.Candidate != nil && !work.LaneReleased && !liveWorker`;
3. `d.From == "candidate" && d.Action == ActionHold && d.Reason == ReasonNoLane && d.LaneID != "" && !work.LaneBusyByOther`, `!details.retryLaneWait[it.ItemID]`, `d.Returned == nil`, and `!paused` (the candidate is held behind an active unbounded examination lease on its own lane `d.LaneID`, i.e., `len(ownLeases) > 0 && ownLeases[0].LeasedUntil == nil` or `work.Examining || work.Examiner != nil`);
4. `!activeCandidateGateStart(tick.Root, tick.StorePath, work.Candidate.Worktree, work.Candidate.RunID)` — a candidate whose `--tillr-gate-supervisor` is actively running is executing its gate, **not** waiting on the pending restart; and
5. `!(hasSettledDetachedGateOnDisk && liveLoopTrunkProofActive)` — if `gate-done.json` already records a passed or failed verdict for `(work.Candidate.RunID, work.Candidate.BaseSHA, work.Candidate.HeadSHA)` **and** the live loop currently has an active trunk proof (`HeavyProgressKind` `started` without `ended` since `runningLoop.startedAt`), `lockRestartLanding` settles that candidate during the proof rather than waiting on the restart; once no trunk proof is active, a candidate with `gate-done.json` **does** wait on `waitForRestartAnswer` and therefore matches.

### 4.2 Exact `WhyRow` presentation (`D-RTP2`)

Following [`why.go`](../../tools/tillr-drain/drain/why.go)'s rule on `TableReason` (`"The machine-readable reason code remains unchanged"`), `row.Reason` stays `ReasonNoLane` (`"no-lane"`), while every human-facing and explanation field names the pending loop restart `reqID` (`pendingDrainRestartID`):

| `WhyRow` field / CLI column | Current output at `79bdcbbf` | Output under `D-RTP2` |
| :--- | :--- | :--- |
| `row.Reason` (`--json`) | `"no-lane"` | `"no-lane"` *(unchanged machine code)* |
| `row.TableReason()` (table column 4) | `"no-lane"` | `"waiting on pending loop restart"` |
| `row.WaitingOn` (`--json`) | `["<lane_id>"]` | `["pending loop restart " + reqID]` |
| `row.Mover` (table column 5 and `--json`) | `"drain; <lane_id>"` | `"drain; pending loop restart " + reqID` |
| `row.LaneStatus` (`--json`) | `"kept"` | `"kept"` |
| `row.LaneReason` (`--json`) | `"candidate awaits pin verification and lane release"` | `"candidate waits on pending loop restart (" + reqID + ")"` |
| `row.Why` (`tillr drain why <item>` and `--json`) | `"no-lane; lane kept: candidate awaits pin verification and lane release"` | `"waiting on pending loop restart: pending loop restart " + reqID + "; lane kept: candidate waits on pending loop restart (" + reqID + ")"` *(plus any existing draining-lane or workflow node suffix)* |

A candidate with no unanswered `drain.restart-request`, or with a live running detached gate (`activeCandidateGateStart == true`), keeps its existing `WhyRow` output unchanged.

---

## 5. What this does not change (negative space and risks)

### 5.1 Out of scope

- **No change to the restart's wait on an active trunk proof** (`restartProofActive` in [`restart.go`](../../tools/tillr-drain/drain/restart.go)) or to red-proof culprit bisection (`d7595176` in [`heavy_loop.go`](../../tools/tillr-drain/drain/heavy_loop.go)).
- **No change to blocking a second trunk proof or post-proof green push** while a restart is pending (`beginHeavyProof`, `BeforeTip`, `startHeavy`, and `offerGreenAdvance` in [`heavy_loop.go`](../../tools/tillr-drain/drain/heavy_loop.go) and [`post_landing.go`](../../tools/tillr-drain/drain/post_landing.go)).
- **No time limits, retry counts, load thresholds, or fallbacks** anywhere in gate admission, proof execution, or restart replacement.
- **No change to `staleLoopBuild` (`stale-loop-build` alarm rules in [`why.go`](../../tools/tillr-drain/drain/why.go) and [`alarm_why_test.go`](../../tools/tillr-drain/drain/alarm_why_test.go)),** post-landing `roll` / `push` action deferrals, or [`lanestate/read.go`](../../tools/tillr-drain/lanestate/read.go) (which intentionally avoids reading restart protocol events).
- **No item directive comments** added, edited, or relied upon in any document.

### 5.2 Risks and edge cases

| Case | Behavior |
| :--- | :--- |
| Workflow candidate with a saved `JudgeReceipt` (`c.Judge != nil`, e.g. `scouted-build@v1`, `planned-build@v1`, `scouted-design@v1`) enters `verify` during a proof while a restart is pending | `acquireGateStart` acquires `RLock` with `h.settling = true`; inside `judgeCandidate` ([`judge.go`](../../tools/tillr-drain/drain/judge.go)), `buildRestartPending` returns `false, nil` because both `restartDeferred()` (`restartProofActive()`) and `restartDefersToSettlement()` are true, so the receipt validates and `--tillr-gate-supervisor` starts immediately. |
| Trunk proof finishes while three detached candidate gates started during the proof are still running | None of the three holds `restartMechanical` (`restartWork` released `RLock` on spawn); `gateRestartWith` acquires `TryLock()`, `releaseRestartExaminations` retains their lane leases, `exec` replaces the loop immediately, and `reclaimDetachedGateExaminationsWith` reattaches to all three without re-running any gate. |
| Candidate arrives in `acquireGateStart` 1 ms **after** `heavyProofActive` clears to `false` and **before** `gateRestartWith` runs | `restartHoldWaiting` was already set to `true` when the pending restart deferred on the proof, so `!restartProofActive() && isRestartWaitingOnHold()` is immediately `true` and parks the candidate in `waitForRestartAnswer(ctx, true)` until `exec`. |
| Non-replacement restart answer (`already-current` when disk binary digest matches running build) | `gateRestartWith` settles the request as `already-current` once the proof ends, calls `clearRestartPending()` (closing `restartPendingWait` and clearing `restartHoldWaiting`), and wakes any post-proof waiters to proceed without `exec`. |
| Installed `tillr-drain` binary on disk was rebuilt during an active trunk proof, and a detached gate starts after the binary's `mtime` while the pre-install proof is still running | `staleLoopBuild` in [`why.go`](../../tools/tillr-drain/drain/why.go) is untouched here (`alarm_why_test.go` case `4c`); if aligning `staleLoopBuild` with proof-deferred detached gates is wanted later, that belongs in its own scoped request. |

---

## 6. Follow-on build item

This design adds no item directive block and ends in **one** build item (`D-RTP1`, `D-RTP2`):

1. **Build item: Allow detached candidate gates to start while a pending loop restart waits on an active trunk proof, and name pending-restart waits in `tillr drain why`.**
   - **Where:** [`tools/tillr-drain/drain/restart_work.go`](../../tools/tillr-drain/drain/restart_work.go), [`tools/tillr-drain/drain/why.go`](../../tools/tillr-drain/drain/why.go), [`tools/tillr-drain/drain/restart_work_test.go`](../../tools/tillr-drain/drain/restart_work_test.go), [`tools/tillr-drain/drain/heavy_restart_pending_test.go`](../../tools/tillr-drain/drain/heavy_restart_pending_test.go), [`tools/tillr-drain/drain/detached_gate_restart_test.go`](../../tools/tillr-drain/drain/detached_gate_restart_test.go) (and/or `why_test.go`), plus `just pack-sync` updating the mirror under `yolo-packs/tillr/files/local/share/tillr/tillr-drain/`.
   - **Testable criteria:**
     1. **Detached gate starts during an active proof (`D-RTP1`):** While an unanswered `drain.restart-request` for `drain` is pending and a trunk proof is active (`restartProofActive() == true`), ready `ProcessSteps` candidates — including candidates carrying a saved workflow `JudgeReceipt` (`judge.json`) — pass `claimSettledCandidateJudge` and `lockRestartGateStart` (`acquireGateStart`), start `--tillr-gate-supervisor` without waiting on `waitForRestartAnswer`, release `restartMechanical` while the gate runs, and settle, land (or record gate refusal), and release their lanes if the gate finishes before the trunk proof ends.
     2. **Immediate replacement at proof end with running detached gates (`D-RTP1`):** If one or more detached candidate gates started during that active trunk proof are still running when the trunk proof finishes, the pending restart does not wait for those gates to finish: no second trunk proof and no post-proof green push starts, new candidate gate starts and post-gate settlements arriving after `heavyProofActive` clears wait on `waitForRestartAnswer(ctx, true)` behind pre-armed `restartHoldWaiting`, `gateRestart` replaces the loop via `exec` while `--tillr-gate-supervisor` stays alive, and the replacement loop reclaims the detached gates and lands/settles them with a gate invocation count of `1` per candidate.
     3. **Red-proof culprit attribution and no timers or fallbacks:** An active trunk proof that fails while a `drain.restart-request` is pending still runs first-red attribution, names the breaking landing, and records its red verdict / fixer before `heavyProofActive` clears and `gateRestart` replaces the loop; zero timeouts, retry caps, or fallback paths are introduced.
     4. **`tillr drain why` names the pending restart (`D-RTP2`):** When `restart.ReadState(r, restart.RoleDrain)` has an unanswered request `<reqID>` and an examining candidate (`work.Candidate != nil`, `!work.LaneReleased`, `d.From == "candidate"`, `d.Action == ActionHold`, `d.Reason == ReasonNoLane` on its own lane) has no live running detached gate (`!activeCandidateGateStart`) and is not settling an already-finished `gate-done.json` during an active trunk proof, `tillr drain why` (queue table, single-item `tillr drain why <item>`, and `--json`) keeps `row.Reason == "no-lane"` and sets `TableReason() == "waiting on pending loop restart"`, `WaitingOn == ["pending loop restart <reqID>"]`, `Mover == "drain; pending loop restart <reqID>"`, `LaneReason == "candidate waits on pending loop restart (<reqID>)"`, and `Why` naming `pending loop restart <reqID>` without `"candidate awaits pin verification and lane release"`, while leaving rows with a live running detached gate or no pending restart unchanged.
     5. **Verification and pack sync:** All updated and new targeted tests in `tools/tillr-drain/drain` pass, `just format` and `just pack-sync` leave `yolo-packs/tillr/` in sync with `tools/tillr-drain/`, and any touched Markdown passes `uvx vantage-check`.

---

## Open Questions

None.

---

## Decision Ledger

Governing owner rulings (`d7595176` on red-proof attribution before restart, [`D-GR6`](../reference/gate.md#D-GR6), [`R-TW-DR2`](./tillr-watch/decision-record-files.md#R-TW-DR2), [`D-JL9`](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9), [`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10), [`D-GR15`](../reference/gate.md#D-GR15), [`D-GR17`](../reference/gate.md#D-GR17), [`D-RT3`](../reference/tillr-restart.md#why-its-this-way), [`D-RT6`](../reference/tillr-restart.md#why-its-this-way), and [`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger)) remain in their home ledgers and are cited in [Section 2.1](#21-settled-owner-rulings-that-constrain-this-design). Every row below is a designer's decision made on a filed request; none is an owner ruling, and the owner may overturn any of them.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-RTP1"></a>`D-RTP1` | **Designer's decision from a filed request; the owner may overturn this.** Keep the pending loop restart waiting on an active trunk proof (`restartProofActive()` in [`restart.go`](../../tools/tillr-drain/drain/restart.go), including red-proof culprit attribution under `d7595176` and blocking any second proof or post-proof green push under `f0eb8fa0` and `a52e8d0c`), while letting detached `ProcessSteps` candidate gates start during an active trunk proof: in `acquireGateStart` ([`restart_work.go`](../../tools/tillr-drain/drain/restart_work.go)), gate the `waitForRestartAnswer(ctx, true)` wait and post-`RLock` recheck on `!h.loop.restartProofActive()` while keeping `restartHoldWaiting = true` pre-armed during the proof so post-proof gate starts and post-gate settlements yield immediately when `heavyProofActive` clears. **Rejected:** replacing `tillr-drain` mid-proof (kills 33–104 min active proofs on every `tools/` landing, starves `refs/tillr/green`, and cuts off red-proof culprit naming); holding detached gate starts across an active proof (`f0eb8fa0`, measured on 2026-11-25 at `79bdcbbf` to park six finished candidates and drop load from ~150 to ~45); adding timeouts, retry counts, or fallbacks. | 2026-11-25, designer | [Section 3.1](#31-why-gate-starts-waiting-on-the-restart-give-way-d-rtp1), [Section 3.2](#32-lock-protocol-in-restart_workgo-from-proof-start-through-exec-d-rtp1) | No ([Section 6](#6-follow-on-build-item)) |
| <a id="D-RTP2"></a>`D-RTP2` | **Designer's decision from a filed request; the owner may overturn this.** In `WhyForItem` ([`why.go`](../../tools/tillr-drain/drain/why.go)), read `restart.ReadState(r, restart.RoleDrain)` and, whenever an unanswered `drain` restart request `<reqID>` exists and an examining candidate (`work.Candidate != nil`, `!work.LaneReleased`, `d.From == "candidate"`, `d.Action == ActionHold`, `d.Reason == ReasonNoLane` on its own lane) has no live running detached gate (`!activeCandidateGateStart`) and is not settling an already-finished `gate-done.json` during an active trunk proof, preserve `row.Reason == "no-lane"` and report `TableReason() = "waiting on pending loop restart"`, `WaitingOn = ["pending loop restart " + reqID]`, `Mover = "drain; pending loop restart " + reqID`, `LaneReason = "candidate waits on pending loop restart (" + reqID + ")"`, and `Why` naming `pending loop restart <reqID>` instead of `"candidate awaits pin verification and lane release"`. **Rejected:** changing `row.Reason` away from `"no-lane"`; labeling candidates whose `--tillr-gate-supervisor` is actively running as waiting on the restart; importing `restart` into read-only [`lanestate/read.go`](../../tools/tillr-drain/lanestate/read.go). | 2026-11-25, designer | [Section 4.1](#41-when-a-candidate-row-waits-on-a-pending-loop-restart-d-rtp2), [Section 4.2](#42-exact-whyrow-presentation-d-rtp2) | No ([Section 6](#6-follow-on-build-item)) |
