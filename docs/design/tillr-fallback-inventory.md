---
title: "Every fallback in the drain, the run adapters and the gate, and a ruling on each"
status: in-review
stage: DESIGN
updated: 2026-11-21
tags:
  - tillr
  - drain
  - run
  - gate
  - fallbacks
  - inventory
  - design
summary: "One table of 168 paths in the tillr drain, its run adapters, the gate and the operator's own tooling that run because the intended path failed or its input was missing. Each row names the file and function, the failure it hides, how often the store recorded it, and a ruling: remove it with a named loud failure and mover, keep it with a justification that waits on the owner, or not a fallback with the reason."
next: "The owner rules OQ-FB1 to OQ-FB8. Then the remove rulings of Section 4 are filed as build items, the restart and harness rows first."
depends-on:
  - ./tillr-judge-and-lifecycle.md
  - ./failure-diagnosed-not-waited-out.md
  - ./landing-principles.md
---

# Every fallback in the drain, the run adapters and the gate, and a ruling on each

**Status:** 2026-11-21. Nothing here is built. Function names were checked with `rg -F` against the tree at `d6b3786a2`, and counts were read from the store that night ([Section 2](#2-how-the-counts-were-taken)). Every ruling in the table is the designer's unless it quotes the owner, and every "keep" waits on him.

> **In short.** The drain, the run adapters and the gate hold 168 paths that let work continue after the intended path failed or its input was missing, and most of them leave no record. The designer's ruling removes 107 of them in favor of four loud failures that already exist in tillr or are already designed, leaves 39 with a written justification for the owner to accept or refuse, points 6 at a decision or question in another design, and shows why the remaining 16 are not fallbacks.

**Why it matters.** The fallbacks are doing the work the intended paths were built for. In five days of records, the pi harness a run is meant to use (a build verified for the current patch series) ran once in 748 recorded pi runs, a run whose process vanished was continued in its own session 0 times in 225, and 373 gate checks ran with no stall detection. None of those three facts was visible as a failure.

**The shape.** One definition ([Section 1](#1-what-counts-and-what-a-loud-failure-is)), four loud failures every "remove" lands on (refuse, hold, alarm, red), one table ([Section 3](#3-the-inventory)), and eight questions that carry every "keep".

**Cost.** Removing these makes the drain stop more often until the hidden causes are fixed. The restart rows alone would have held 225 runs for the operator in five days. That is the point of the ruling, and [Section 4](#4-what-the-remove-rulings-ask-for-in-order) orders the work so the commonest causes are fixed first.

**Start at [Section 3](#3-the-inventory).** The rows the owner named are `S1`, `S13`, `B2`, `B5`, `D1`, `P8`, `G2`, `D6` and `O1`.

**Needs your ruling:** [OQ-FB1](#OQ-FB1), [OQ-FB2](#OQ-FB2), [OQ-FB3](#OQ-FB3), [OQ-FB4](#OQ-FB4), [OQ-FB5](#OQ-FB5), [OQ-FB6](#OQ-FB6), [OQ-FB7](#OQ-FB7), [OQ-FB8](#OQ-FB8).

**Reads with:** [the judge and lifecycle design](./tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27) (it decides model failover as `D-JL24` to `D-JL27` and asks about escalation; this document links those rows and does not repeat them), [a failure is diagnosed, not waited out](./failure-diagnosed-not-waited-out.md) (the rule that nothing re-runs without evidence), and [the landing principles](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out). No implementation plan is seeded.

---

## 1. What counts, and what a loud failure is

**The owner's ruling**, 2026-11-20 about 10:56 PM ET, verbatim: "That fallback isn't just models. That is everything. Like, fallbacks are the devil. It is never, almost, almost, almost never, without huge justification and bringing it by me to have a fallback. If something fails, you fix it. If it fails loudly, you fix it. You don't hide it with a fallback."

**The definition**, from the operator's request that filed this work: a **fallback** is any path that runs because the intended path failed or its input was missing, and that lets work continue without the failure being fixed. That includes a default substituted for a missing value, a retry on a different path, a move to another backend, model or session, a check recorded as skipped or unavailable instead of failed, an error dropped or reduced to a log line, and a reader that accepts an older record shape.

**What is not a fallback.** Recovery that restores exactly the intended state after a crash, by replaying an intent the store recorded before the crash, is not one. The table does not assume this for any path: each such row says which record is replayed and why the restored state is the same. A rule a person wrote that selects what runs (a check not selected because nothing it reads changed, a tier that defers a check to the other tier) is also not one, and those rows say so.

**The four loud failures.** Every "remove" ruling replaces its fallback with one of these. All four exist in tillr today or are already designed, so no new mechanism is proposed here.

| Loud failure | What the operator sees | Who moves it | Where it comes from |
| :--- | :--- | :--- | :--- |
| **Refuse** | The tool or adapter exits before any work with `refused:` and the missing input in words. An item it was running holds as `adapter-refused` with those words, and no backend closes. | Operator | `AdapterRefusal` and `ClassifyAttempt`, built under `D-JL17` of [the judge and lifecycle design](./tillr-judge-and-lifecycle.md#decision-ledger) |
| **Hold** | One item waits with a named reason, the measured cause and a mover. Other items keep running. | Named in the row | The hold rules in `tools/tillr-drain/holdrules/` |
| **Alarm** | A statement that part of tillr itself cannot run, shown ahead of every item on every operator face until the fact that raised it is gone. The loop keeps running. | Operator | `D-JL25` of the same design, not built |
| **Red** | The check's result is `error` or `fail`, and the gate's verdict is not a pass. | Whoever owns the check | The gate's verdict today |

One rule sits under all four, and it is the designer's: **the failure is recorded where it happened, in words, with the path or value that was missing.** A row marked "writes no record" in the table is a path where that is not true today.

## 2. How the counts were taken

- **The store.** `/workspace/.tillr/state.db`, opened read-only. Its events run from 2026-11-15 4:42 PM ET to 2026-11-20 11:53 PM ET: 1,335,058 events, 2,655 runs, 1,096 gate verdicts carrying 1,100 check results. Counts quoted from the scout step were read at 11:09 PM ET; the designer's own were read at 11:53 PM ET. Each count names its table or event kind so it can be read again.
- **`NR` (no record)** means the path writes nothing that distinguishes it, so the store cannot say how often it fired. That is a finding, not a zero.
- **A count is of records, not incidents.** One incident can write several records, and a record that was never written is not counted.
- **Two files outside the store** were read once: the retained heavy record at `.git/tillr-gate/heavy.json` in the main checkout, which is overwritten on each heavy run, and the retained run directories under `.tillr/loop/`.

## 3. The inventory

**Reading the table.**

- **Paths** are shortened: `drain/` is `tools/tillr-drain/drain/`, `run/` is `tools/tillr-run/run/`, `backends/` is `tools/tillr-run/backends/`, `pi/` is `tools/tillr-run/backends/pi/`, `gate/` is `tools/tillr-gate/`, `checks/` is `tools/gate/checks/`. A script's top-level body is called `main`.
- **Ruling** is one of: **Remove** (with the loud failure of [Section 1](#1-what-counts-and-what-a-loud-failure-is) and its mover), **Keep, awaiting owner** (with the question that carries the justification), **Decided elsewhere** (with the link), or **Not a fallback** (with the reason).
- **Row letters:** `S` session continuity and restart, `B` backend and seat selection, `D` the rest of the drain, `P` the pi adapter, `R` the run face and the other adapters, `G` the gate, `O` operator tooling, `X` recovery paths examined.

| ID | Fallback | Where it lives | The failure it hides | Fired | Ruling |
| :--- | :--- | :--- | :--- | :--- | :--- |
| S1 | A resume that fails is replaced by a new session | `drain/designer.go`, `failedDesignerResume` and `replaceFailedDesigner` | The harness could not resume a recorded session (for example pi's "Agent is already processing") | 1 dispatch with reason `resume-failed`; 0 in `comment_runs` | **Remove.** Hold `resume-failed` with the harness's error words and the session ID; the session is kept. Mover: operator. This overturns the fresh-session clause of step 5 of `D-JL24` for a resume that failed. |
| S2 | The replacement session also moves to another backend | `drain/designer.go`, `replaceFailedDesigner` with `quotaReplacementBackend` | The same, plus a shut quota window | Inside S1's count | **Decided elsewhere:** removed by `D-JL24` and `D-JL26`. |
| S3 | A designer whose transcript is gone gets a fresh session | `drain/designer.go`, `chooseDesigner` (`transcript-missing`) | The recorded transcript is missing or unreadable | 0 events carry that `designer_reason` | **Remove.** Hold `session-lost` naming the path. Mover: operator. |
| S4 | A designer starts fresh because its harness cannot resume or a person changed its seat | `drain/designer.go`, `chooseDesigner` (`cannot-resume`, `seat-changed`) | Nothing failed: the adapter declares it, or the policy changed | 0 events carry those `designer_reason` values | **Keep, awaiting owner:** [OQ-FB1](#OQ-FB1). |
| S5 | An unreadable pause declaration is read as "can resume" | `drain/designer.go`, `chooseDesigner` (the `pause-kind` read) | The adapter's declaration file is missing or unreadable | NR | **Remove.** Refuse the dispatch, naming the file. Mover: operator. |
| S6 | A large session resumes uncompacted when the compaction program is missing | `drain/designer.go`, `chooseDesigner` and `compactStep` | The adapter's `compact` executable is absent or not executable | NR (reads as `resumed-expired`) | **Remove.** The loop refuses to start with an adapter that declares compaction and has no program. Mover: operator. |
| S7 | Returned work goes to a fresh worker when its session cannot be found | `drain/worker_session.go`, `workerSessionReason` (`session-missing`) | The recorded worker session is missing, unreadable or mismatched | 281 decisions, against 92 that resumed the worker | **Remove.** Hold `session-lost`. Mover: operator. First split the reason: "no session was ever owed" is not a loss, and 281 against 92 says the two are mixed today or capture is failing. |
| S8 | Returned work goes to a fresh worker because a person changed the seat or the lane was reassigned | `drain/worker_session.go`, `workerSessionReason` (`seat-changed`); `drain/plan.go`, the planner (`lane-unavailable`) | Nothing failed in the session | 17 `seat-changed`, 31 `lane-unavailable` | **Keep, awaiting owner:** [OQ-FB1](#OQ-FB1). |
| S9 | An older candidate with no saved worker session is treated as session-missing | `drain/worker_session.go`, `landedWorkerSession` and `retainedWorkerSession` | A record written before worker sessions were saved | NR (inside S7's 281) | **Remove.** The older reader goes; an item that still depends on such a record holds for the operator. |
| S10 | The worker's session, head or base is looked up in second and third places, with read errors ignored | `drain/worker_session.go`, `captureWorkerSession` and `captureLandedWorkerSession` | The run's archived session ID, the candidate's worker head, or the staged whole-item base is missing | NR | **Remove.** One source for each fact; a missing one is a hold `candidate-record-incomplete`. Mover: operator. |
| S11 | A missing reuse mapping reads as "this attempt" | `drain/worker_session.go`, `nodeSourceAttempt` | The table is absent (an older store), or no row exists | NR | Absent table: **Remove** with row D40. No row: **Not a fallback**, because "no reuse recorded" is what an absent row means. |
| S12 | The terminal record of a reclaimed resumed run is filled with invented values | `drain/orphan.go`, `reapDeadLoopRuns` (start time set to now, seat and node from the request) | The resumed run face died without a terminal record | 94 runs ended `lease-reclaimed`; how many had fields invented is NR | **Remove.** A field nobody recorded reads `not recorded`. |
| S13 | A run whose process vanished across a loop restart is run again from the start | `drain/orphan.go`, `reapDeadLoopRuns`; `drain/pass.go`, `pendingDecisionExcept` | The loop's restart ended the run face, and "no resumable session was confirmed" | 225 `drain.orphaned` events; 0 of them say the session was continued. The restart-from-scratch branch itself is NR | **Remove.** Hold `run-lost`, stating which confirmation failed (session ID, log, brief or worktree). Mover: operator. The cause to fix is that a loop restart ends its runs, which [the roadmap's entry 3](../tillr/roadmap.md#3-roll-onto-new-code-without-ending-runs) already owns. This overturns the restart clause of step 5 of `D-JL24` for this case. |
| S14 | A read-only parallel node whose process vanished is run again | `drain/parallel_restart.go`, `reapParallel` and `restartParallel` | The same, for a parallel reader | Inside S13's 225 | **Remove**, as S13. |
| S15 | A resumed run whose face died is resumed again from its last pause | `drain/orphan.go`, `reclaimOrphanedResume`; `drain/parallel_restart.go`, `reapParallel` | The run face died after `run.resume` with no end recorded | 12 `drain.lane-released` events with cause `reap`, shared with X2 and X3 | **Keep, awaiting owner:** [OQ-FB8](#OQ-FB8). The session is the same one, but nothing shows the interrupted segment left no effects. |
| S16 | A run interrupted before any pause continues in its staged session | `drain/orphan.go`, `hasInterruptedWorkflowSession`; `drain/pass.go`, settlement | The run face vanished; the session ID, log, brief and worktree all match | 0 of 225 `drain.orphaned` events | **Keep, awaiting owner:** [OQ-FB8](#OQ-FB8). |
| S17 | Preserved work is compared against the lane's base when the recorded base cannot be read | `drain/preserved.go`, `baseInCheckout` | Any git error reading the recorded base commit | NR | **Remove.** Hold `preserve-failed` with git's words. Mover: operator. |
| S18 | A missing fence file is read as an empty fence | `drain/preserved.go`, `preserveWorkFromBase` and `snapshotWork` | The lane's fence file does not exist | NR | **Remove**, as S17. |
| S19 | An unreadable staged index is saved as the working copy | `drain/preserved.go`, `stagedSnapshot` and `snapshotWork`; `preserveWorkFromBase` (the empty index ref) | The lane's index is absent, unreadable or has unmerged paths | 0 of 160 `drain.work-preserved` summaries carry the "could not be read" note; an absent index is NR | **Remove.** The lane is not reset; hold `preserve-incomplete`. Mover: operator. |
| S20 | A missing preserved ref is skipped, and a missing staged ref loses the staging | `drain/preserved.go`, `applyPreservedWork` | The ref the store recorded is gone | NR | **Remove.** Hold `preserved-work-missing`, naming the ref. Mover: operator. |
| S21 | A patch that does not apply is tried again three-way | `drain/preserved.go`, `applyPreservedWork` | The saved diff does not apply to the new lane | NR | **Remove** the two-step. One method is chosen and recorded (three-way, by [landing principle 2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch)); a conflict is a hold. |
| S22 | Unparseable line counts in a preserved-work summary become zero | `drain/preserved.go`, `snapshotSummary` | `git diff --numstat` output that is not a number | NR | **Remove.** The summary step fails with the line it could not read. |
| S23 | A run's item is looked up in two other tables when its run row is missing | `drain/preserved.go`, `preservationItem` | The `runs` row does not exist | NR | **Remove.** The dispatch record, written first, is the one source. |
| S24 | An earlier predecessor, or one with no staged files, stands in for the latest | `drain/preserved.go`, `workflowPredecessorRun` | The latest predecessor has no staged run directory | NR | **Remove.** Hold `predecessor-unstaged`. Mover: operator. |
| S25 | Preserved work is found by other keys when none exists for the run asked for | `drain/preserved.go`, `preservedWorkForDecision` | No preserved work under the run ID given | NR | **Remove.** The decision names the run whose work it continues; none found is none. |
| S26 | A missing item body is compared as a made-up sentence | `drain/lane_base.go`, `blockBodyAt` | The item's body source is unnamed or absent at that commit | NR | **Remove.** Hold as an item-definition fault. Mover: design seat. |
| S27 | With no commit carrying the wanted body, or no landing record, the trunk tip is used | `drain/lane_base.go`, `blockBase` and `designAnswerLanded` | The base the function describes cannot be found | NR | **Remove.** Hold `stale-base` with what was searched. Mover: design seat. |
| S28 | A failed read of the rewrite aliases leaves the original commit IDs in place | `drain/trunk_rewrite.go`, `rewriteTranslate` | The store could not be opened or read | NR | **Remove.** The error is returned. The alias lookup itself is row X11. |
| S29 | A refused run whose report cannot be read records no closed window | `drain/quota_window.go`, `refusedWindow` and `recordBackendWindow` | The report is missing, unreadable or lacks its header | NR | **Remove.** The backend's state is unknown, which is an alarm. Mover: operator. |
| S30 | With no launcher named, the configured executable is the restart candidate | `tools/tillr-drain/restart/restart.go`, `LauncherCandidate` | `TILLR_LAUNCHER` is unset | NR | **Keep, awaiting owner:** [OQ-FB4](#OQ-FB4). Starting the binary directly is a supported way to run. |
| S31 | An older restart answer is matched by its display line instead of its digest | `tools/tillr-drain/restart/restart.go`, `Restarter.Owing` | The answer was written before digests | NR | **Remove.** The older reader goes. |
| S32 | A restart candidate is accepted unchecked when no check is wired | `tools/tillr-drain/restart/restart.go`, `Restarter.Settle` and `Replace` | The check callback is nil | NR | **Remove.** A restarter without a check is refused when it is built. |
| B1 | An item with no seat runs on the policy's default backend | `drain/policy.go`, `Policy.firstBackend` | The item names no seat | NR (the reason reads `ready`) | **Keep, awaiting owner:** [OQ-FB4](#OQ-FB4). The default is a line a person wrote in the policy. |
| B2 | A failed or closed backend is replaced by the next on the seat's list or the policy's `preference` | `drain/policy.go`, `Policy.nextBackend`; `drain/plan.go`, `planner.attempt`; `drain/quota_window.go`, `availableBackend` and `nextAvailableRequest`; `drain/pass.go`, `pendingDecisionExcept` | A run ended in the infrastructure class, or the backend's window is shut | 14 dispatches `failover`; 37 `seat-degraded` (13 build, 12 design, 9 plan, 2 verify, 1 scout) | **Decided elsewhere:** removed by `D-JL24`. |
| B3 | A closed design or review backend is replaced by the next open one on that seat | `drain/blocked.go`, `planner.conflictDecision`; `drain/seat_review.go`, `firstOpenSeatBackend`; `drain/seat_shares.go`, `selectSeatDispatch` | The seat's first backend is shut | NR (no reason names the skipped backend) | **Decided elsewhere:** removed by `D-JL24`. |
| B4 | A session pi ended for a repeating call moves to another backend | `drain/repeating_call.go`, `repeatingRoute` (`failover`) | The fresh session repeated the same call | 1 `drain.repeating-call` event | **Decided elsewhere:** removed by `D-JL24`. |
| B5 | A worker's `seat` obstacle moves the item along the policy's `escalate` list | `drain/seat_escalation.go`, `escalationNext` and `escalateSeat` | The worker reports the task exceeds its model | 2 `drain.seat-escalated` events | **Awaiting owner elsewhere:** [OQ-JL5](./tillr-judge-and-lifecycle.md#OQ-JL5). |
| B6 | A recorded escalation target the policy no longer names is ignored | `drain/plan.go`, `planner.firstBackend` | The policy was edited after the escalation | NR | **Remove**, if escalation stays. Hold `escalation-target-missing`. Mover: operator. |
| B7 | A session pi ended for a repeating call gets one fresh session on the same backend | `drain/repeating_call.go`, `repeatingRoute` (`fresh`) | The session repeated one identical tool call | 6 `drain.repeating-call` events | **Keep, awaiting owner:** [OQ-FB1](#OQ-FB1). `D-JL24` step 5 kept it as the designer's decision. |
| B8 | An adapter path that cannot be made absolute is used as written | `drain/policy.go`, `resolveAdapterPath` | The working directory cannot be resolved | NR | **Remove.** The policy fails to load. |
| B9 | The "why" face drops an error and prints a plainer answer | `drain/why.go`, `Why`, `acceptedConflictRetryCommand` and `LiveLoopLanes`; `drain/retry_brief.go`, `acceptedBuildRetryCommand` and `runLinkMissingHold` | A lookup behind the explanation failed, or the loop's presence could not be checked | NR | **Remove.** The face prints the error beside the row, and presence it cannot check reads "unknown". |
| D1 | A run with no pinned ending capability is treated as a footer session | `drain/brief_ending.go`, `stagedEndingCapability` | The `ending-capability` file is absent from the run or lane directory | NR. Today 1,325 of 2,517 exited runs' retained directories have no file, which counts files, not firings | **Remove.** Hold `ending-capability-missing`. Runs staged before pinning get `footer` written once, by a recorded migration, so the reader needs no default. Mover: operator. |
| D2 | An adapter with no ending declaration is recorded as footer | `drain/landing_call.go`, `recordEndingCapabilities` | The adapter ships no declaration file | NR | **Remove.** The loop refuses to start with that adapter. Mover: operator. |
| D3 | A failed capability probe selects footer | `drain/landing_call.go`, `probePiEndingCapability` and `probeClaudeEndingCapability` | The harness is not on `PATH`, the probe timed out or failed, or its output was malformed | 0 of 78 `drain.pi-features` events recorded footer; the probe's error is NR | **Remove.** The loop refuses to start, with the probe's error. A harness that truthfully answers "no such feature" still gets footer: that is its declared capability, not a fallback. Mover: operator. |
| D4 | The adapter and its preflight read a missing staged capability as footer | `pi/run-pi`, `main`; `backends/preflight.sh`, `preflight_brief` | The staged file is missing, unreadable or holds another word | NR | **Remove.** Refuse. |
| D5 | Two report parsers disagree, and the drain's reads a footer the extractor rejects | `drain/failover.go`, `reportFooter` | The worker's last line is not exactly the footer | NR | **Remove.** One parser, the extractor's strict last line. Anything else is a missing output, which goes back to the worker under the existing rule. |
| D6 | A heavy check that answers `unavailable` counts toward green | `drain/heavy.go`, `HeavyRunner.judgeTip`; `drain/heavy_steps.go`, `ProcessSteps.runHeavyLegacy` | Two different things share the word: a landing-tier check the heavy tier does not owe, and a check whose tool is missing here | The one retained heavy record holds 33: 32 not owed, and 1 `waybar` check whose `ai-usagebar` is not installed. History is NR (the file is overwritten) | **Remove** for a missing tool or a quarantined check: red, and the green pointer does not move. The not-owed kind is **not a fallback** (a tier rule) and is recorded as `not-owed`. |
| D7 | A stalled heavy check is run once more, and a pass on the retry counts | `drain/heavy.go`, `HeavyRunner.judgeTip` | The check made no progress | NR in the store | **Awaiting owner elsewhere:** [OQ-PR19](../tillr/decision-provenance-review.md#OQ-PR19). |
| D8 | When the culprit search names no landing, the fixer is filed against the tip | `drain/heavy.go`, `attribute`; `drain/trunk.go`, `culpritLanding` | The first-red search errored or could not single out a commit | NR (a log line and a note in the fixer) | **Keep, awaiting owner:** [OQ-FB3](#OQ-FB3). |
| D9 | A revert that cannot be made becomes a fixer | `drain/heavy.go`, `HeavyRunner.offerRevert`; `drain/heavy_revert.go`, `buildRevert` | The revert conflicts, or the revert machinery itself errored | 12 `drain.revert` events; refusals are NR | A revert that conflicts: **Keep, awaiting owner:** [OQ-FB3](#OQ-FB3). A machinery error: **Remove**, alarm. |
| D10 | A check runs with no full log, or one log stands in for another | `drain/heavy_steps.go`, `legacyLogPath` and `runHeavyLegacy`; `drain/steps.go`, `ProcessSteps.Rolling`; `drain/heavy_loop.go`, `startHeavy` | The log directory or file could not be created, or the per-check log is missing | NR | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). |
| D11 | A rolling run that could not judge is logged and dispatch continues | `drain/steps.go`, `ProcessSteps.Rolling`; `drain/trunk.go`, the rolling handler | The program did not start, exited without a verdict, or named no failing check | 1 of 95 `rolling_runs` has outcome `unknown` | **Remove.** Alarm: the trunk proof cannot run. Mover: operator. |
| D12 | A red trunk whose fixer could not be filed is logged and dispatch continues | `drain/trunk.go`, the rolling handler (`red-unfiled`) | Filing the fixer failed | NR (the outcome field is overwritten) | **Remove.** Alarm. |
| D13 | A heavy runner fault is logged and the loop carries on | `drain/heavy_loop.go`, `startHeavy` | The after-merge proof errored | NR | **Remove.** Alarm. |
| D14 | A failed landing-count write is logged after the landing | `drain/loop.go`, landing completion (`bumpLandings`) | The count that triggers the proof was not written | NR | **Remove.** Alarm: the proof's trigger is lost. |
| D15 | A fixer's fence widens to every tracked file when no base is known | `drain/trunk.go`, `rangePaths` (no base) | The rolling run named no paths and no proved base exists | NR | **Keep, awaiting owner:** [OQ-FB3](#OQ-FB3). |
| D16 | A failed path listing is read as no paths | `drain/trunk.go`, `rangePaths`; `drain/steps.go`, `ProcessSteps.changedPaths` | `git log`, `ls-tree` or `diff` failed | NR | **Remove.** The error is returned and the step fails. |
| D17 | A base replay that cannot judge leaves the candidate refused as if the trunk were green | `drain/trunk.go`, `trunkAlreadyRed`; `drain/steps.go`, `ProcessSteps.Replay` | The replay is missing, or exited without a verdict | NR | **Remove.** The refusal records "trunk replay could not judge" with the cause, and the proof alarm is raised. |
| D18 | A red gate's evidence that cannot be read is dropped | `drain/steps.go`, `gateFailureContext` and `readRecordPayload` | The verdict, plan or span files are missing or malformed | 29 of 1,096 `gate.verdict` events: `spans_state` is `absent` on 28 and `unreadable` on 1 | **Remove.** The verdict is `error`, naming the file. Mover: operator. |
| D19 | A gate verdict is recorded with zero spans when the span file cannot be read | `drain/events.go`, `recordGateJudgment` | The same files | The same 29 | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). The record already says `spans_state`. |
| D20 | A check that declares no reads is taken to read the whole tree | `drain/steps.go`, `gateCheckReads` | The declaration has no `reads` | NR | **Keep, awaiting owner:** [OQ-FB3](#OQ-FB3). |
| D21 | A lander that prints no record is judged by its exit code | `drain/steps.go`, `ProcessSteps.Land` | The land program wrote nothing to stdout | NR | **Remove.** Hold `land-record-missing`. Mover: operator. |
| D22 | A landing with no lock measurement is counted as unmeasured | `drain/landing_measurements.go`, `saveLandObservation` | The lander sent no observation | NR in the store (a log line counts them) | **Remove.** The current lander always sends one; a missing one is D21's fault. |
| D23 | A staged item fence that is missing is read from the older lane fence | `drain/loop.go`, `itemFencePath` | The `item-fence` file is absent | NR | **Remove.** The existing refusal "the lane has no fence" applies. |
| D24 | A candidate whose head cannot be read is not pinned, and a failed pin is only logged | `drain/loop.go`, `headOf`, `pin` and `pinQuietly` | Git could not read the lane's HEAD or write the ref | NR | **Remove.** Hold: the work is unprotected. Mover: operator. |
| D25 | An over-long run ID is pinned under a digest that ordinary readers do not find | `drain/loop.go`, `pinRef` | The run ID exceeds a ref component's length | NR | **Remove** once `D-JL17` bounds every run ID; until then the branch stays. |
| D26 | A retried candidate that is gone starts fresh from the trunk | `drain/retry_candidate.go`, `planner.retryCandidate` and `retryCandidateBrief` | The recorded base or head commit cannot be read | NR in the store (the brief says so) | **Remove.** Hold `candidate-lost` with the recorded commits. The operator may then start fresh, which is his choice. |
| D27 | An older hold's missing commits are taken from other records | `drain/retry_candidate.go`, `planner.retryCandidate` | The hold predates recorded base and head | NR | **Remove.** The older reader goes. |
| D28 | A fixer is gated by the ordinary gate when no fixer gate is configured | `drain/steps.go`, `ProcessSteps.Gate` (`FixerGateProgram`) | The flag was not given | NR | **Remove.** The loop refuses to start: its own help says the ordinary gate "cannot prove the sibling breakage its range reaches". |
| D29 | A missing or unreadable saved replay or progress file means the check runs again | `drain/heavy_progress.go`, `readHeavyReplay` and `loadHeavyProgress` | The saved file is missing or malformed, or git could not compare tips | NR | **Keep, awaiting owner:** [OQ-FB3](#OQ-FB3). |
| D30 | A block-declared item whose document block is not found continues | `drain/retire.go`, `retireDocumentBlock` | No document holds the item's block | NR in the store (a log line) | For a filed request: **not a fallback**, because it has no block by definition. For a block-declared item: **Remove**, hold. Mover: design seat. |
| D31 | A lock holder that is not JSON is read as a bare process ID | `drain/trunk_lock.go`, `historyHolderDead` | An older lock record | NR | **Remove.** The older reader goes. |
| D32 | An unreadable HEAD on a pinned lane triggers a fresh checkout of the pin | `drain/held_lane.go`, `stagePinnedCandidate` | `git rev-parse HEAD` failed in the lane | NR | **Remove.** The error is returned; the checkout runs only when HEAD was read and differs. |
| D33 | Cleanup errors are ignored before a scratch checkout, a rolling run or a revert | `drain/steps.go`, `detachedWorktree` and `Rolling` (the old spans file); `drain/heavy_revert.go`, `removeRevertTree`; `drain/pass.go`, `pipelineCloseGraph` | A removal failed, so an old file may remain | NR | **Remove.** A spans file that could not be removed fails the rolling run, since a stale one could be read as this run's verdict. The others return their error. |
| D34 | The build's identity prints placeholders | `drain/build.go`, `ReadBuildStamp` and `fileDigest` | Build metadata or the executable's digest is unavailable | NR | **Remove** for the digest: the restart logic compares it, so the loop refuses to start without one. The other fields: [OQ-FB2](#OQ-FB2). |
| D35 | Spend and landing counts print `unavailable` | `drain/loop.go`, `Loop.Run`; `drain/heavy_loop.go`, `startHeavy` | The store read behind a report line failed | NR in the store | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). |
| D36 | Options a caller omits take a default | `tools/tillr-drain/main.go`, `loopFace` and the other faces (store path, lane root, policy path, binary names, 2 lanes, backoff, poll interval); `drain/loop.go`, `NewLoop`; `drain/heavy_steps.go`, `JudgeHeavy` | The flag or field was not given | NR | **Keep, awaiting owner:** [OQ-FB4](#OQ-FB4). |
| D37 | A store read or decode error is treated as "no such record" | `drain/seat_escalation.go`, `readEscalations`; `drain/retry_brief.go`, `itemGuidanceBrief` and `acceptedBuildRetryCommand`; `drain/loop.go`, `holdLocal` and `heldFor`; `drain/trunk.go`, `rollingUnknownStreak`; `drain/stage.go`, `LaneStager.Stage` (the trunk-red notice); `drain/blocked.go`, `isObstacleCallRun`; `run/read.go`, `StatusRecord` (a missing finish event) | A query failed, or a stored payload is not valid JSON | NR | **Remove.** The error is returned. An item-scoped read holds that item as `store-read-failed`; a loop-scoped one stops the pass. Mover: operator. |
| D38 | A store older than the binary is read with missing columns and tables taken as empty | `drain/start_version.go`, `readVersionSchema` and `readItemContentVersions`; `drain/plan.go`, `readItems` and `readRequestIDs`; `drain/quota_window.go`, `readBackendWindows`; `drain/retry_brief.go`, `itemGuidanceBrief`; `drain/why.go`, `LiveLoopLanes`; `drain/lane_base.go`, `designAnswerLanded`; `run/read.go`, `StatusRecord` | The store has not been migrated to this binary's schema | NR | **Remove.** Every face, read-only ones included, refuses a store whose schema is older than the binary, with both versions in the message. Mover: operator, by opening it once with the current binary. |
| P1 | The harness is the first of three: the verified build for the current series, the previous build, the `pi` on `PATH` | `pi/pi-harness.sh`, `pi_resolve_harness`, `_pi_current_series_key`, `_pi_read_current_json` and `_pi_installed_version` | No verified build exists for the current patch series | Of 748 runs with a recorded harness: 1 series build, 747 "installed" (737 of them the operator's wrapper of row O1) | **Remove.** Refuse `pi-build-missing`, with the build command. Mover: operator. |
| P2 | The builds and agent-directory roots default to the durable scratch directory, then to a path under the repository | `pi/pi-harness.sh`, `pi_resolve_harness`; `pi/pi-agent-dir.sh`, `main`; `pi/build-agent-dir`, `resolve_agent_dirs_root` | The run face did not pass the root | NR | **Remove.** The run face always passes it; unset is a refusal. |
| P3 | A patch branch missing from the repository is read from the fork cache | `pi/pi-harness.sh`, `_pi_current_series_key`; `pi/build-agent-dir`, `resolve_patched_packages` | The repository's `patch/<fork>` ref does not resolve | NR | **Remove.** Refuse, naming the ref. |
| P4 | Provider credentials come from the shared directory, and a missing file is skipped | `pi/pi-harness.sh`, `pi_prepare_harness_env` and `pi_apply_yolo_session_home` | The session's credential directory or file is absent | NR | **Remove.** Refuse before pi starts, naming the file. Workers have otherwise failed later with "No API key found for amazon-bedrock". |
| P5 | A failed write of the harness record is ignored | `pi/pi-harness.sh`, `pi_prepare_harness_env` (the write ends in an or-true) | The record file cannot be written | NR; 1,904 of 2,655 runs record no harness, which includes backends that never write one | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). |
| P6 | A compile cache that cannot be created is skipped | `pi/pi-harness.sh`, `pi_prepare_harness_env` | `mkdir` failed | NR | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). |
| P7 | A failed extension-set key, with its error discarded, selects the deployed agent directory | `pi/pi-agent-dir.sh`, `main` (`build-agent-dir --key` with stderr discarded) | The key could not be computed | 4 of 574 runs with a recorded extension set ran `deployed` | **Remove.** Refuse `pi-extensions-key-failed` with the builder's error. |
| P8 | The agent directory defaults to `$HOME/.pi/agent` | `pi/run-pi`, `main` (`${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}`); `pi/pi-agent-dir.sh`, `main` (`${TILLR_DEPLOYED_PI_AGENT_DIR:-...}`); `pi/build-agent-dir`, `resolve_deployed_dir` | The variable is unset | NR | **Remove.** Refuse `agent directory not set`. The run face is the one writer of that variable. |
| P9 | A failed extensions build runs the previous set, or the deployed directory | `pi/pi-agent-dir.sh`, `main` (the `held pi-extensions-build-failed` branches, and the ignored status and previous-set metadata) | The extensions for the current key did not build | 0 of 574 recorded extension sets say `held` | **Remove.** Refuse `pi-extensions-build-failed`; the mover line the adapter already prints becomes the refusal. Mover: operator. |
| P10 | The extension builder marks a set verified without verifying it | `pi/build-agent-dir`, `verify_extensions_load` and `find_pi_coding_agent_pkg` | `node` or pi's package index was not found, so the load check was skipped | NR | **Remove.** The build fails; no `.verified` marker is written. |
| P11 | The builder reads git and settings failures as "nothing there" | `pi/build-agent-dir`, `git_obj_hash`, `git_ls_tree_names`, `git_ls_pack_jsons`, `non_matt_pack_extension_names`, `read_deployed_settings_packages`, `collect_deployed_non_matt`, `resolve_trunk_ref`, `materialize_and_build_package`, `read_active_key` | A git command failed, a manifest or settings file is malformed, the trunk ref is absent, or a checkout or fetch failed | NR | **Remove.** Each is a build failure with the command's words, which P9 then refuses on. |
| P12 | A hard link that fails becomes a copy | `pi/build-agent-dir`, `copy_tree_hardlink_or_copy` | `cp -al` failed for a file | NR | **Keep, awaiting owner:** [OQ-FB3](#OQ-FB3). The bytes are the same. |
| P13 | A package contribution with no build command or outputs is marked built | `pi/build-agent-dir`, `materialize_and_build_package` | The pack's contribution omits `build` or `produces` | NR | **Remove.** The pack is refused; a package with nothing to build says so in its contribution. |
| P14 | A resume with no resume prompt is sent the original brief | `pi/run-pi`, `backends/claude/run-claude`, `backends/muse/run-muse` and `backends/agy/run-agy`, `main` (`${TILLR_RESUME_PROMPT_FILE:-$brief}`) | The prompt variable is empty in resume mode | NR | **Remove** in resume mode: refuse. In a new run the brief is the prompt, which is not a fallback. |
| P15 | Adapter options a caller omits take a default | The four adapters' `main` (`brief`, `target`, session mode `new`, the `tillr-lane` markers); `backends/preflight.sh`, `resolve_target`; `pi/stop-at-gap`, `journal_path` and `seconds`; `pi/compact`, `main`; `tools/tillr-run/main.go`, `execFace` and `pauseFace` (120 seconds); `run/exec.go`, `Exec` (the derived lane) | The option was not given | NR | **Keep, awaiting owner:** [OQ-FB4](#OQ-FB4). An invalid value (for example a grace period that is not a number) is different: **Remove**, refuse. |
| P16 | Shell tracing is skipped when its directory or shim is unavailable | `pi/run-pi` and `backends/claude/run-claude`, `main` | The trace directory or shim cannot be used | NR | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). |
| P17 | A writer runs without its goal check when its base file is missing | `pi/run-pi`, `main` (the `base_sha` test); `pi/keep-going.ts`, `isReadOnlyBrief` | The staged `base_sha` is absent, or the brief cannot be read | NR | **Remove.** Refuse: a writer has a base. |
| P18 | The goal check reads a git failure as an empty, clean tree | `pi/keep-going.ts`, `readTree`; `backends/claude/landing`, `stop` | `git rev-parse`, `status` or `diff` failed | NR | **Remove.** The check reports the git error and does not settle. |
| P19 | An older `trunk-broken` footer is read as a trunk obstacle | `pi/keep-going.ts`, `obstacleProblem` | The report uses the footer that predates `OBSTACLE:` | NR | **Remove.** The older reader goes. |
| P20 | A landing answer the session is not holding is delivered as a plain message | `pi/run-pi`, `main` with `pi/landing-call-held`; `backends/claude/run-claude`, `main` | The parked call cannot be found, or the harness refused a promptless resume | 0 of 25 `call.delivered` events have form `message`; all 25 are `tool-result` | **Remove.** Hold `call-not-held` with the harness's words. Mover: operator. |
| P21 | A trimmed or read-only worker starts without extensions that are missing | `pi/run-pi`, `main` (the `yolo-patched` packages, the settings-derived list, the built-in extension directory) | A package directory, settings file or `python3` is absent | NR | **Remove.** Refuse, naming what is missing. |
| P22 | A session that finished but whose process would not exit is ended by signal and counted as ended | `pi/stop-at-gap`, `end_settled` and `end_parked` | pi stayed alive after its session settled or parked | 75 runs with end cause `settled-idle` | **Keep, awaiting owner:** [OQ-FB6](#OQ-FB6). |
| P23 | A worker that ends unchanged a second time is allowed to settle | `pi/keep-going.ts`, `decide` (`identical repeat`) | The nudge changed nothing | NR in the store | **Not a fallback.** Nothing is substituted: the unmet goal is reported to the drain, which holds the item under its identical-repeat rule. |
| P24 | A partial sum of token counters is reported as the total | `pi/extract-report`, `dialect`; `backends/claude/extract-report`, `_usage_total`; the same in `muse` and `agy`; `backends/usage.py`, `counters` | Some counters are missing from the stream | NR | **Remove.** The report says `partial` beside the number. |
| P25 | A report is assembled from other stream shapes, and missing figures read `unknown` | `pi/extract-report`, `dialect`; `backends/claude/extract-report`, `dialect` and `_normalize_window`; `backends/usage.py`, `details`, `new_state` and `stamp`; `backends/report.py`, `render` | The stream lacks the event or field the extractor reads first | NR | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). |
| P26 | An ending offer is saved with an empty transcript path | `pi/land.ts`, `land`; `pi/obstacle.ts`, `obstacle` | pi's session manager reports no session file | NR | **Remove.** The call is refused in the session, with the reason. |
| R1 | A failed repository lookup leaves the run face with no backend directory, which also skips its pause check | `tools/tillr-run/main.go`, `defaultBackendsDir` | `git rev-parse --git-common-dir` failed | NR | **Remove.** Refuse. |
| R2 | A run that timed out exits zero | `tools/tillr-run/main.go`, `runExit` | The run did not end in state `exited` | NR | **Remove.** The exit code is distinct for a timeout. A pause is an ordinary ending and stays zero. |
| R3 | An adapter path that does not resolve is tried as written | `run/exec.go`, `resolveExecAdapter` | The resolved path is missing or a directory | NR | **Remove.** Refuse `failed-to-start` with the resolved path. |
| R4 | The older `node_id` field is read when `node` is absent | `run/exec.go`, `Request.effectiveNode` | A request from an older emitter | NR | **Remove.** The older reader goes. |
| R5 | A run whose spend cannot be extracted still records its ending | `run/exec.go`, `Exec`, `readSpend` and `parseSpend`; `run/spend.go`, `DescribeSpend` | No extractor, no report, an extractor that exited nonzero, or a header with no figures | 140 `run.usage` error events against 2,873 `run.finish`: 81 "wrote no report", 59 "exit status 4" | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). |
| R6 | Missing harness and extension-set evidence is left out of the run record | `run/exec.go`, `Exec` | The evidence files were not written | 1,904 runs with no harness and 2,078 with no extension set, of 2,655, including backends that write neither | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). |
| R7 | A resumed log whose size cannot be read is scanned from the start | `run/exec.go`, `Exec` (the stream offset) | `Stat` failed on the log | NR | **Remove.** The run fails to start: an old ending in the log could be read as this segment's. |
| R8 | A worker whose CPU priority could not be lowered runs at normal priority | `run/exec.go`, `Exec` (`Setpriority`) | The system call failed | NR in the store (a line on stderr) | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). |
| R9 | The ending monitor's last failed observation is dropped | `run/exec.go`, `Exec` (`monitor.observe`) | The last read of the stream failed | NR for this call; 154 `run.observation-error` events show other observations are recorded | **Remove.** It is recorded like the others. |
| R10 | A continuation with no notice gets a generic "continue" prompt | `run/pause.go`, `prepareContinuationSession` | No pause notice and no resume prompt exist | NR | **Keep, awaiting owner:** [OQ-FB8](#OQ-FB8), with the same-session rows. |
| R11 | A harness that cannot resume continues in a fresh session given the transcript path | `run/pause.go`, `prepareContinuationSession`; `backends/muse/pause-kind` and `backends/agy/pause-kind` | Nothing failed: the adapter declares `cannot-resume` | NR | **Keep, awaiting owner:** [OQ-FB1](#OQ-FB1). |
| R12 | A missing brace allowance is read as zero | `backends/preflight.sh`, `preflight_brief` | The staged `brace-literals` file is absent | NR | **Remove.** Staging always writes it; absent is a refusal. |
| R13 | An unreadable transcript reads as "no answer yet", and malformed rows are skipped | `backends/claude/landing`, `answered`, `rows` and `serve` | The transcript is missing, or a line is not JSON | NR | **Remove.** The hook fails with the path. A JSON-RPC message with no ID is an ordinary notification, which is not a fallback. |
| R14 | The quota token is read from the environment, then one of three credential locations | `backends/claude/quota`, `access_token` | The environment token is unset | NR | **Keep, awaiting owner:** [OQ-FB4](#OQ-FB4). These are the harness's own documented locations. |
| R15 | A quota window the provider returns as null is left out of the reading | `backends/claude/quota`, `normalize` | One of the two windows is null | NR | **Remove.** The reading fails, which `D-JL25` already makes an alarm. A window at zero use with no reset time is a valid answer, not a fallback. |
| R16 | A stream with no final result is completed from earlier fragments | `backends/claude/extract-report`, `dialect` | The final `result` event is absent | NR | **Remove.** The report is incomplete; a footer in an earlier fragment does not finish it. |
| R17 | Noisy `agy` output is scanned for any JSON object, and denied tool calls only warn | `backends/agy/extract-report`, `_envelope` and `dialect` | The stream is not the one JSON envelope, or the harness auto-denied writes | NR | **Remove.** Both make the report incomplete: a run whose writes were denied did not do the work. |
| R18 | Unparseable stream lines are skipped and counted in the report header | `backends/report.py`, `records` | A line is not a JSON object | NR in the store (the header's `unparseable lines`) | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). |
| R19 | A landing offer that cannot be read is replaced by the stream's text | `backends/report.py`, `run`; `run/exec.go`, `readSpend` (the offer `Stat`) | The offer file is unreadable or malformed | NR | **Remove.** The report is incomplete, naming the file. |
| R20 | A report's footer is written from a valid landing offer | `backends/report.py`, `run` | Nothing failed: a call-suspends session ends with a call, not a footer | NR | **Not a fallback.** The offer is that protocol's ending. The report should label the footer as rendered from the offer, which it does not today. |
| G1 | A check that needs a ref this clone lacks is `unavailable`, and the verdict still passes | `gate/memory.go`, `(*treeMemory).run`; `gate/needs.go`, `resolveRefNeeds`; `gate/run.go`, `RunWith` | The ref was not fetched | 0 `unavailable` among 1,100 check results; `rg -n -F 'ref:' tools/gate/checks/*.json` finds no declaration that uses a ref need | **Remove.** Red, printing the `git fetch` command it already knows. Mover: operator. |
| G2 | A selected check whose tool is missing is dropped from the plan, and the run still says `VERIFIED` | `tools/fork-check.py`, `build_plan` and `Plan.unavailable`, for 16 kinds (`node`, `npm`, `go`, `yolo`, `pi`, `sh`, `bash`, `just`, `hivemind`, the storybook runner) | A program the check needs is not installed here | 0 gate spans with status `unavailable`, `skip` or `quarantined`; a `just done` run outside the drain writes nothing to the store | **Remove.** Red: `NOT VERIFIED`. A check that cannot run on a kind of machine says so in its declaration, which a person writes, instead of being probed for at run time. Mover: operator. |
| G3 | The waybar suite exits 77 and its check is dropped | `tools/fork-check.py`, `sentinel_skip` | `ai-usagebar` is not installed | 1 in the retained heavy record (row D6) | **Remove**, as G2. |
| G4 | A failing test on a quarantine list is removed from the verdict | `tools/fork-check.py`, `fork_quality_skip`, `applied_quarantine`, `UNANSWERABLE_FORK_QUALITY` (one entry, expiring 2027-01-26) | Two timing cases in `pi-subagents` fail | NR | **Remove.** The test is fixed or deleted by a person; until then it is red. Mover: the quarantine's own queue item. |
| G5 | A series check that cannot read the host's patch-branch record is turned into a pass | `tools/fork-check.py`, `read_series_transcript` | The record is written on the host and never reaches a jail | NR | **Remove.** The check declares the machine it runs on, as G2; where it is declared to run, a missing record is red. |
| G6 | `--no-build` drops the build check | `tools/fork-check.py`, `build_plan` | Nothing failed: a person passed the flag | NR | **Not a fallback.** It is a person's choice, printed in the result, and the kind stays in the required list so a landing can refuse it. |
| G7 | Stall detection reads process accounting when the kernel counter is refused | `gate/cpu_linux.go`, `startWithTaskClock`; `tools/fork-check.py`, `_run_cancellable` | The kernel refuses the task-clock counter | 533 of 1,100 check results record `process accounting`; none records the kernel counter | **Keep, awaiting owner:** [OQ-FB5](#OQ-FB5). |
| G8 | A check runs with no stall detection at all | `gate/watchdog.go`, `(*watchdogState).observe`; `gate/cpu_other.go`, `startAccountedCommand` | Neither CPU source could be read | 373 of 1,100 check results record `watchdog unavailable: kernel task-clock counter: permission denied`; 340 of them passed | **Remove.** The check is `error`. Mover: operator. |
| G9 | A failed read of a check's output tail is ignored | `gate/watchdog.go`, `sampleOutput` | The log read failed | NR | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). |
| G10 | A `must_exist` path that cannot be examined is read as absent, so the check leaves the plan | `gate/registry.go`, `Pattern.Matches` | Any `stat` error, including a wrong root | NR | **Remove.** Only "does not exist" means absent; any other error fails the plan. |
| G11 | An entry with no tier is classed as landing | `gate/shadow.go`, `tierOf` | An older registry entry | NR | **Remove.** The older reader goes. |
| G12 | The dual-run comparison uses the planned list, or `none`, when a result cannot be read | `gate/dualrun.go`, `observePython` and `verdictResult` | The plan line or verdict file is missing or malformed | NR | **Remove.** The comparison records the read error. This face is a diagnostic and gives no landing verdict. |
| G13 | A cache that cannot be read or written means the work is done again | `gate/goenvmemory.go`, `readCachedModuleIdentity`, `writeCachedModuleIdentity` and `goModuleIdentity`; `checks/change_common.py`, `stored_result`, `store_result` and `judge_document_set`; `tools/fork-check.py`, `main` (the verdict cache) | The cache file is missing, malformed or unwritable | NR | **Keep, awaiting owner:** [OQ-FB3](#OQ-FB3). |
| G14 | A tracked Markdown file that cannot be read is left out of the citation check | `checks/tree_common.py`, `design_citation_findings` | The file read failed | NR | **Remove.** It is a finding. |
| G15 | A base timing declaration that cannot be parsed is read as empty | `checks/elapsed-time.py`, `elapsed_time_findings` | The base file is malformed | NR | **Remove.** It is a finding. |
| G16 | With no usable baseline, the gate plans as a first run | `tools/fork-check.py`, `select_baseline` and `plan_gate` | Its own verified point is not in this history | NR | **Keep, awaiting owner:** [OQ-FB3](#OQ-FB3). |
| G17 | A record log that is unreadable or torn is read as empty or partial | `tools/fork-check.py`, `read_record_lines` and `parse_record_bytes` | The log read failed, or a line is not JSON | NR | **Keep, awaiting owner:** [OQ-FB3](#OQ-FB3). It leads to a first-run plan, and says nothing while doing so. |
| G18 | An absent record log is read from the older `state.json` | `tools/fork-check.py`, `Record.__init__`, `read_legacy_record` and `migrate_legacy_record` | The log predates the current format | NR | **Remove.** The older reader goes after one recorded migration. |
| G19 | A record line is appended a ninth time without checking it reached the file | `tools/fork-check.py`, `append_record_line` | The log was replaced during all eight tries | NR | **Remove.** The append fails, and the run says so. |
| G20 | Pi's package is looked for in a second variable, then the global install | `tools/fork-check.py`, `pi_package_root` | The first location is unset or stale | NR | **Remove.** One declared location; unset is red for the suites that need it. |
| G21 | An unresolved launch target is fingerprinted from the tag list | `tools/fork-check.py`, `resolve_targets` | `bin/pi-forks patch-branch target --fetch` could not resolve it | NR | **Remove.** Red: the fingerprint would describe a release the launch does not build. |
| G22 | The gate's scratch directory falls to `TMPDIR`, then `/tmp` | `tools/fork-check.py`, `residue_scratch`, `build_plan` and `preflight_scratch` | The durable scratch variable is unset | NR | **Remove.** Refuse: the house rule forbids `/tmp`. |
| G23 | The shard pool's lock directory is derived when not named | `tools/go-test-shards.py`, `shared_cache_dir` and `default_lock_dir` | The lock-directory variable is unset | NR | **Keep, awaiting owner:** [OQ-FB4](#OQ-FB4). Two runners that derive different paths split the one pool, which the question states. |
| G24 | A slot-wait trace that cannot be written is dropped, and unreadable holder files become a count | `tools/go-test-shards.py`, `record_slot_wait`, `read_slot_holders` and `describe_slot_holders` | The trace directory or a holder file is unreadable | NR | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). |
| G25 | A failed verdict can be rewritten as `pass-inherited` | `gate/run.go`, `ApplyInheritedFailures` | Every failing check is named as inherited | Never: `rg -n -F 'ApplyInheritedFailures(' --glob '!**/*_test.go' --glob '!yolo-packs/**' .` finds only its definition | **Remove.** It has no caller; delete it. |
| G26 | A check is not selected, is deferred to the other tier, or runs only in the shadow measurement | `tools/fork-check.py`, `Plan.not_planned` and `apply_tier`; `gate/registry.go`, `Registry.Plan`; `gate/shadow.go`, `Shadow` | Nothing failed | Not counted | **Not a fallback.** Each is a selection rule a person wrote, and each is printed (`not run`, `owed`). Shard-time estimates and the reserved proof slots in `tools/go-test-shards.py` are the same kind: no test is skipped. |
| O1 | The operator's wrapper runs a frozen pi build in place of the installed one | A script named `pi` in the `bin` directory of the jail's durable scratch, outside the repository; put first on `PATH` by `.agents/skills/drain-timed-checkin/SKILL.md` | The installed pi lacks patch-series changes the drain needs | 737 runs record the wrapper as their harness, labelled `installed` | **Keep, awaiting owner:** [OQ-FB7](#OQ-FB7). |
| O2 | The wrapper selects an agent-directory overlay, shared credentials and a compile cache, each skipped silently when absent | The same script's top-level body | The overlay, credential files or cache directory is missing | NR | **Keep, awaiting owner:** [OQ-FB7](#OQ-FB7), with O1. |
| O3 | A roll that fails can lose its failure event | `tools/tillr-roll/tillr-roll.py`, `record_failure` | The store write failed | 45 `drain.roll-failed` events were written; losses are NR | **Keep, awaiting owner:** [OQ-FB2](#OQ-FB2). The roll itself still fails. |
| O4 | A roll with no proof credit or no known revision tests everything | `tools/tillr-roll/tillr-roll.py`, `evidence`, `gated_modules` and `test_commit` | The green ref, the landing proof or the running binary's revision cannot be read | NR | **Keep, awaiting owner:** [OQ-FB3](#OQ-FB3). |
| O5 | A roll builds the view without its web interface when the built pages are missing | `tools/tillr-roll/tillr-roll.py`, `roll_commit` | `web/dist/index.html` is absent from the clean checkout | NR | **Remove.** The roll fails. Mover: operator. |
| X1 | A claim that never launched is retracted and the item offered again | `drain/orphan.go`, `reapDeadLoopRuns`; `drain/unlaunched_claim.go`, `restorePendingBeforeClaim` and `retractClaim` | The loop died after claiming and before starting the run | Not counted | **Not a fallback.** The replayed record is the `drain.node-advanced` or `drain.graph-changed` event from before the claim, appended again with the same payload. No output stream was ever opened, and streams are opened before a run face starts, so no run existed to lose. |
| X2 | A run that ended on a lane outside the rotation is settled where it ended | `drain/orphan.go`, `reclaimOffRotationLease` | The loop that held the lane is gone | 12 `reap` releases, shared with S15 and X3 | **Not a fallback.** The run's own terminal record is settled; nothing runs again. |
| X3 | A lane still leased to an examined run is released | `drain/orphan.go`, `reclaimStuckLease` | The loop died between examining and releasing | The same 12 | **Not a fallback.** The examination is recorded done, and the release is its recorded last step. An examination that was still in progress is run again; that part is not shown to be exact and is asked in [OQ-FB8](#OQ-FB8). |
| X4 | A preservation event lost in a crash is appended from the ref | `drain/preserved.go`, `preserveWorkFromBase` | The loop died after writing the ref and before the event | Not counted | **Not a fallback.** The event's payload is stored in the ref's commit message, and the ref's coverage of the dirty paths is checked before it is appended. |
| X5 | A restart request is answered by the replacement process | `tools/tillr-drain/restart/restart.go`, `ReadState`, `Settle`, `Replace` and `Adopt` | Nothing failed: the old process is replaced on request | 30 requests, 32 answers | **Not a fallback.** The recorded `drain.restart-request` is the intent; an identical binary answers `already-current` and a failed check answers `refused`, with no other executable tried. |
| X6 | A document retirement interrupted by a crash is confirmed, not redone | `drain/retire.go`, `recoverRetirement` | The loop died around the retirement commit | 323 `drain.retirement` events | **Not a fallback.** The event records the worker's head and the retirement tree; recovery accepts the commit only when its one parent and its tree equal those, and fails otherwise. |
| X7 | A heavy check that passed on this same tip is not run again | `drain/heavy_progress.go`, `reuseHeavyCheck` | The heavy run was interrupted | Not counted | **Not a fallback** for the same tip: the saved verdict names the tip and the check. Reuse across tips is a gate memory rule (unchanged reads), not crash recovery, and is not claimed as exact. |
| X8 | A session ID claimed before a crash is written to the lane file | `run/pause.go`, `continuationSessionID`; `run/exec.go`, `Exec` | The executor died between the claim and the file | Not counted | **Not a fallback.** The `run.resume` event names the session, and it is accepted only when run, item, lane and backend all match. |
| X9 | A parked ending call seen again is parked again, not offered twice | `backends/claude/landing`, `pre_tool_use`; `pi/land.ts`, `executeLand`; `pi/obstacle.ts`, `executeObstacle` | The session replays its own call | Not counted | **Not a fallback.** The staged offer holds the same call ID. |
| X10 | A saved backend choice is used again instead of drawn again | `drain/seat_shares.go`, `selectSeatDispatch`; `drain/plan.go`, `attemptForInterruption` | The claim is retried after an interruption | Not counted | **Not a fallback** for the choice: the saved backend is the recorded one, and a shut backend holds. It restores the choice only, not the session. |
| X11 | A commit the trunk rewrote is found through its recorded alias | `drain/lane_base.go`, `containsCommit`; `drain/trunk_rewrite.go`, `rewriteTranslate` | An accepted history rewrite changed commit IDs | 3 `drain.trunk-rewrite-accepted` events | **Not a fallback.** The alias is a record written when the rewrite was accepted. Row S28 is the part of this path that is one. |
| X12 | A gate record appended during compaction is appended again to the new file | `tools/fork-check.py`, `_compact_log` and `append_record_line` | The log was replaced under a writer | Not counted | **Not a fallback** when the append succeeds: the same bytes are written and a repeated run ID is folded. Rows G17 and G19 are the parts that are. |

**The totals, by ruling.** Remove: 107, counting a row that splits as one remove (`S11`, `D6`, `D9`, `D30`, `D34` and `P15` split). Keep, awaiting owner: 39, plus the kept part of `D9`, `D34` and `P15`. Decided or awaiting owner in another design: 6 (`S2`, `B2`, `B3`, `B4` under `D-JL24`; `B5` and `D7` in open questions). Not a fallback: 16 whole rows (`P23`, `R20`, `G6`, `G26` and `X1` to `X12`), with six more rows partly so and counted under their other ruling, for 168 rows in all (`S11`, `D3`, `D6`, `D30`, `P14`, `R15`).

## 4. What the remove rulings ask for, in order

This is the designer's order, by how often the hidden failure fired, and the owner may reorder it. Each line is a class of rows that one build item can remove together; none is filed by this document.

1. **Runs survive a loop restart, or hold when they do not** (`S13`, `S14`, with [OQ-FB8](#OQ-FB8) deciding `S15` and `S16`). 225 orphaned runs in five days, none continued in its own session.
2. **The pi harness is the verified build or the run refuses** (`P1` to `P4`, `P7` to `P11`, `P13`, with [OQ-FB7](#OQ-FB7) deciding the wrapper). 747 of 748 recorded pi runs used something else.
3. **A check that cannot watch for a stall is an error** (`G8`, with [OQ-FB5](#OQ-FB5) deciding `G7`). 373 check results.
4. **A returned worker's session is found or the item holds** (`S1`, `S3`, `S5` to `S7`, `S9`, `S10`). 281 decisions read `session-missing`.
5. **A check that cannot run here is red, and where it runs is declared** (`D6`, `G1` to `G5`, `G20`, `G21`).
6. **The trunk proof that cannot run is an alarm** (`D11` to `D14`, `D17`, `D18`).
7. **One ending protocol per run, pinned and never guessed** (`D1` to `D5`, `P14`, `P19`, `P20`, `P26`, `R16`, `R19`).
8. **No reader for an older record** (`D38`, `S9`, `S31`, `D23`, `D27`, `D31`, `R4`, `G11`, `G18`). Each goes with a one-time migration where live records still have the old shape.
9. **A store error is an error** (`D37`, `S28`, `B9`, `D16`).
10. **The rest**, each small: the preserved-work rows (`S17` to `S25`), the base rows (`S26`, `S27`), and the remaining adapter and gate rows.

**What must never happen while these are built.** A fallback is not replaced by a quieter one. A row is removed only when its loud failure exists and a test shows the old path no longer runs.

## 5. Earlier rulings this inventory meets

These were found by `rg -n -i 'fallback|fall back|failover' docs/design docs/tillr` on 2026-11-20. None is changed here. Where an earlier ruling is the owner's own, the row says which question now asks whether it still holds.

| Earlier ruling | Whose | What this inventory does with it |
| :--- | :--- | :--- |
| `D-JL24` to `D-JL27`, [no automatic move to another model](./tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27), 2026-11-20 | Owner's quoted words; the designer's mechanism | Linked from `S2`, `B2`, `B3`, `B4`. Two clauses of its step 5 are the designer's and are overturned by `S1` and `S13`, also the designer's; a third is asked as [OQ-FB1](#OQ-FB1). |
| [Quota fallback only where the seat set enables it](./quota-resume.md#decision-ledger) ([`OQ-QR5`](./quota-resume.md#decision-ledger)), 2026-10-20 | Owner, as that document records it | Untouched. It is about the pi session layer, it is not built, and `D-JL26` already leaves it to him. |
| [A worker's refused ending goes to its own session, "fall back to the fresh run with the evidence when it cannot"](../tillr/roadmap.md#filed-2026-11-17-a-workers-failure-goes-back-to-the-worker), 2026-11-17 | Recorded under an owner ruling, not as his quoted words | Asked as [OQ-FB1](#OQ-FB1) for the cases where nothing failed; overturned by `S7` for a session that is lost. |
| [Stall detection by process accounting where the kernel counter is refused](./gate-rework.md#65-ten-seconds-is-a-failing-test), with the [2026-11-17 roadmap section](../tillr/roadmap.md#filed-2026-11-17-stall-detection-where-the-kernel-counter-is-refused) | Designer's | Asked as [OQ-FB5](#OQ-FB5); its "runs without detection" branch is removed by `G8`. |
| [A second stall on the same tip fails](../tillr/decision-provenance-review.md#OQ-PR19) | Open with the owner | Linked from `D7`. |
| Shims with isolation where supported, shims alone elsewhere ([`OQ-LS3`](./tillr-lane-shims.md#OQ-LS3)), 2026-11-20 | Owner's selection | Not in the table: it is not built. The build item should show him this ruling before building the weaker branch. |

## 6. What this does not cover

- **Other programs.** `tillr-land`, `tillr-view`, `tillr-item`, `tillr-judge` and `tillr-keep` were not inventoried: the request names the drain, the run adapters and the gate. `tillr-roll` appears only where the scouts met it.
- **Recovery paths nobody examined.** `recoverJudgePublication`, `recoverJudgeHandoff` and `recoverSecondGateStall` in the drain, the reconciliation in `drain/designer_compaction.go`, and what `tillr-keep` restores were not read closely enough to say whether they replay a recorded intent exactly. They are not in the table, and they are not assumed to be recovery.
- **Completeness.** The scouts read the named files and followed their callers. Three reported that they did not read every branch: the rest of `drain/loop.go`, `drain/pass.go` and `drain/trunk.go`; the roughly 10,500 lines of `tools/fork-check.py`; and the helpers under `tools/gate/checks/`. A fallback in those that nobody met is not in the table. The first build item of each class in [Section 4](#4-what-the-remove-rulings-ask-for-in-order) should search its own files again.
- **Fallbacks in unbuilt designs.** A design that proposes a fallback (the scout-tool unlock, the watcher's safety timer, the session-continuity "readable memory") is not running code. Each should be checked against the owner's ruling when it is built.
- **How each removal is built.** No file plan is given; the rows name where the path lives so a planner can find it.

## Open Questions

Each question carries the justification for a group of "keep" rows. The designer accepts none of them: a row stays a fallback awaiting the owner until he answers.

1. 💬 **OQ-FB1: When nothing failed but a session cannot be continued, may the work start in a fresh session by itself?**

   A worker on Muse is paused for quota. Muse cannot resume a session, and its adapter says so. Today the drain starts a new session and hands it the old transcript's path, the worktree and the brief. The same happens when you change a seat's backend in the policy while work is out, when the lane was given to other work, and once when pi ends a session for repeating one tool call. In none of these did a resume fail; there was never one to try.

   - **A — Yes, for these declared cases only.** You see the dispatch with its reason (`cannot-resume`, `seat-changed`, `lane-unavailable`, or the repeating-call line). The new session has the files and no memory of the old one's reasoning.
   - **B — No, hold each one.** The item waits as `session-not-continuable` and you start the fresh session with one command. You see every case; work on harnesses that cannot resume stops at every pause.

   <!-- vantage: question id=OQ-FB1 leaning="A (Yes, for these declared cases only) — the adapter or a person's policy edit decided it in advance, which is a choice and not a failure." -->

   _Leaning:_ A (Yes, for these declared cases only) — the adapter or a person's policy edit decided it in advance, which is a choice and not a failure.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-FB2: When a side duty of a run fails (a measurement, a log, a trace, a priority change), does the run stop?**

   A build worker finishes and commits. The extractor that reads its token spend then writes no report. Today the run's ending is recorded with spend `unreported` and a `run.usage` error event, and the item goes on to the gate. That happened 140 times in five days. The same shape covers a full log that could not be opened, a shell trace that could not start, a harness record that could not be written and a priority that could not be lowered.

   - **A — The run continues and an alarm is raised.** The alarm names the duty that failed and stays up until it works again. The item's record says what is missing. Paid work is not thrown away.
   - **B — The run fails.** Nothing unmeasured ever lands. A broken extractor stops every run on that backend until it is fixed.
   - **C — As today.** A record or a log line, and no alarm.

   <!-- vantage: question id=OQ-FB2 leaning="A (The run continues and an alarm is raised) — it makes the failure loud without discarding finished work; today's silence is the part that is wrong." -->

   _Leaning:_ A (The run continues and an alarm is raised) — it makes the failure loud without discarding finished work; today's silence is the part that is wrong.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-FB3: When the precise thing cannot be done, may tillr do a broader thing that checks or covers more, never less?**

   The gate starts and its saved results file is corrupt. Today it says nothing and runs every check as if for the first time. The same shape appears elsewhere: a cache that cannot be read means the work is redone; a roll that cannot tell what changed tests every module; a trunk failure whose culprit cannot be found gets a repair item against the tip; a revert that conflicts becomes a repair item; a failed hard link becomes a copy.

   - **A — Yes, and it says so.** Each prints one line naming what it could not read and what it did instead. You wait longer; nothing is verified less.
   - **B — No, stop.** A corrupt cache or an unfindable culprit halts that step until someone fixes it. You learn of every one; the gate and the trunk repair stop on a damaged file.

   <!-- vantage: question id=OQ-FB3 leaning="A (Yes, and it says so) — the substitute never weakens a verdict, so the only thing hidden today is the damaged input, and one printed line fixes that." -->

   _Leaning:_ A (Yes, and it says so) — the substitute never weakens a verdict, so the only thing hidden today is the damaged input, and one printed line fixes that.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-FB4: May a tool run by hand fill in an option you left out with a documented default?**

   You run `tillr-drain` without `--lanes`. Today it runs two lanes, and without `--store` it uses the repository's own store. About thirty options across the drain, the run face, the adapters and the shard runner behave this way, and each default is in the tool's help. The drain passes every one of these explicitly when it starts a run, so the defaults serve a person at a terminal. One has a known risk: two shard runners that each derive the lock directory differently split the one pool.

   - **A — Yes, where the help states it.** You type less. A run started by the drain still refuses when the drain failed to pass a value (rows `P2`, `P8`, `P14`).
   - **B — No, require every option.** Each tool refuses with the missing flag's name. Nothing is ever implied; every hand command gets longer.
   - **C — Yes, except where two processes must agree.** As A, but the shard pool's lock directory must be given.

   <!-- vantage: question id=OQ-FB4 leaning="C (Yes, except where two processes must agree) — a stated default for a person is a choice, and the lock directory is the one place a wrong guess silently breaks a shared limit." -->

   _Leaning:_ C (Yes, except where two processes must agree) — a stated default for a person is a choice, and the lock directory is the one place a wrong guess silently breaks a shared limit.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-FB5: In the jail, the kernel refuses the counter the gate's stall detector was built on. Is process accounting its detector there?**

   A lane's change reaches the gate. The gate asks the kernel for a CPU counter to tell a hung check from a busy one, and inside the jail the kernel says no. Today the gate reads the processes' accounting under `/proc` instead, and 533 check results in five days were watched that way; none used the kernel counter. Before that substitute existed, 373 ran unwatched, which row `G8` removes.

   - **A — Yes, declare it.** The gate's configuration names process accounting as the detector for this machine. A refused kernel counter is then not a failure here, and a failure of process accounting is an error.
   - **B — No, fix the kernel permission.** The jail is changed to allow the counter, and until then every check is an error.

   <!-- vantage: question id=OQ-FB5 leaning="A (Yes, declare it) — a detector a person names for a machine is a choice; probing for one at run time is the fallback." -->

   _Leaning:_ A (Yes, declare it) — a detector a person names for a machine is a choice; probing for one at run time is the fallback.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-FB6: When pi has finished its work but its process will not exit, may the run be ended and counted as finished?**

   A worker commits and its session settles. The pi process then stays alive. Today a watcher waits 30 seconds, checks pi's journal to prove no background work is owed, ends the process by signal, and records the run as ended with the cause `settled-idle` and the signals it sent. That happened 75 times in five days, so the defect in pi that leaves it running has been absorbed 75 times.

   - **A — Yes, and each one raises an alarm.** The work counts. The alarm names the pi defect and stays until a run exits by itself.
   - **B — No, the run fails.** The finished commit is not offered to the gate until pi is fixed.
   - **C — As today.** Recorded on the run, with no alarm.

   <!-- vantage: question id=OQ-FB6 leaning="A (Yes, and each one raises an alarm) — the work is proved complete before the process is ended, and the alarm is what gets the pi defect fixed." -->

   _Leaning:_ A (Yes, and each one raises an alarm) — the work is proved complete before the process is ended, and the alarm is what gets the pi defect fixed.

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 **OQ-FB7: Does the operator's frozen-pi wrapper stay until the drain can build pi from the patch series itself?**

   The operator starts the drain with a script named `pi` first on `PATH`. It runs a pi build frozen on 2026-11-19 in place of the installed one, because the installed pi lacks patch-series changes the drain needs. The script's own comment says you approved it as a stopgap; no document quotes you. 737 runs record it as their harness, labelled `installed`, so the records do not say which build ran. It also picks an extensions overlay and credentials, each skipped silently when absent.

   - **A — It stays, labelled.** Until the proper build path lands, the wrapper remains and runs record it as the frozen build. Removing row `P1`'s chain waits on that.
   - **B — It goes now.** The drain refuses to run until a verified build for the current series exists.

   <!-- vantage: question id=OQ-FB7 leaning="A (It stays, labelled) — removing it today stops every pi run, and the record's wrong label is the part that can be fixed at once." -->

   _Leaning:_ A (It stays, labelled) — removing it today stops every pi run, and the record's wrong label is the part that can be fixed at once.

   **Answer:**
   > _(empty — fill in when decided)_

8. 💬 **OQ-FB8: After the loop restarts, may a run whose process died continue in its own recorded session without a person?**

   You roll the drain onto new code while a worker is mid-task. Its process dies with the old loop. The store still holds its session ID, brief and worktree. Today the new loop is built to continue that same session when all three match, with a generic "continue" prompt, and to resume again a session whose resume was cut off. The records show the first has never happened: in 225 orphaned runs none was continued. Continuing is not a different path, but nothing proves what the cut-off step did before it died.

   - **A — Yes, when session, brief and worktree all match.** The run continues and its record says it was interrupted and continued. A half-finished tool call may be repeated by the model.
   - **B — No, hold it.** The run waits as `run-lost` and you continue or restart it with one command. Every restart of the loop needs you once per run in flight.

   <!-- vantage: question id=OQ-FB8 leaning="A (Yes, when session, brief and worktree all match) — it is the intended path run again on recorded facts, weakly held because it has never once fired and so has never been observed." -->

   _Leaning:_ A (Yes, when session, brief and worktree all match) — it is the intended path run again on recorded facts, weakly held because it has never once fired and so has never been observed.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-FB1` | **The owner's words:** "That fallback isn't just models. That is everything. Like, fallbacks are the devil. It is never, almost, almost, almost never, without huge justification and bringing it by me to have a fallback. If something fails, you fix it. If it fails loudly, you fix it. You don't hide it with a fallback." | 2026-11-20, owner (quoted words) | [Section 1](#1-what-counts-and-what-a-loud-failure-is) | — |
| `D-FB2` | The definition of a fallback, and that exact replay of a recorded intent is not one. **From an operator request; the owner may overturn it.** | 2026-11-20, operator request | [Section 1](#1-what-counts-and-what-a-loud-failure-is) | — |
| `D-FB3` | Every "remove" lands on one of four existing loud failures (refuse, hold, alarm, red), and the failure is recorded where it happened. **Designer's decision; the owner may overturn it.** **Rejected:** a new failure mechanism; a count of failures before failing. | 2026-11-21, designer | [Section 1](#1-what-counts-and-what-a-loud-failure-is) | — |
| `D-FB4` | The 107 "remove" rulings of the table, each with its loud failure and mover. **Designer's decisions; the owner may overturn any row.** Two of them overturn clauses of step 5 of `D-JL24`, which was also the designer's: a failed resume holds (`S1`), and a run lost across a loop restart holds (`S13`). | 2026-11-21, designer | [Section 3](#3-the-inventory) | — |
| `D-FB5` | No "keep" is accepted by the designer. Each of the 39 rows, and the kept part of three more, waits on the owner under one of the eight questions above. **From the operator request.** | 2026-11-20, operator request | [Open Questions](#open-questions) | — |
| `D-FB6` | The order in which the removals are built. **Designer's decision; the owner may reorder it.** | 2026-11-21, designer | [Section 4](#4-what-the-remove-rulings-ask-for-in-order) | — |
