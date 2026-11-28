---
title: "Splitting the 2026-11-27 broken trunk drain repair into sixteen per-cause build items"
status: in-review
stage: DESIGN
updated: 2026-11-27
tags:
  - tillr
  - drain
  - gate
  - trunk-proof
  - testing
  - design
summary: "Executes the tools/tillr-drain/drain suite at trunk tip 83580e31504c (30 commits ahead of green pointer 895c6cb31b66), separates all 61 failing tests (all 38 proof failures from 81bf66eb plus 23 additional full-suite failures at 83580e31504c) across landed regressions, load or neighbor-interference failures, and stale test expectations, attributes each of the 16 root causes to the landed commit that introduced it, rules on stopping monolithic fixer-main-81cd910659bb, and specifies 16 ordered single-cause build items with exact green-test criteria and zero retries, timeout bumps, or skips."
next: "Close fixer-main-81cd910659bb (or hold it until only C13 remains) and run the Section 5 filing script against .tillr/state.db to queue build items C01 through C16 in descending failure-count order."
depends-on:
  - landing-principles.md
  - trunk-repair-on-moving-trunk.md
  - tillr-scheduling-and-notifications.md
  - tillr-queue-mirrors-designs.md
  - ../tillr/decision-provenance-review.md
related:
  - fast-gate-same-tool-proof-misses.md
vantage:
  status-chip: true
---

# Splitting the 2026-11-27 broken trunk drain repair into sixteen per-cause build items

**Status:** Open design record from an operator request; **this document records no new owner ruling**. Every root-cause grouping, three-way classification, recommendation on `fixer-main-81cd910659bb`, and ordered build-item specification below (`D-SBT1` to `D-SBT4` in [Section 6](#6-decision-ledger)) is recorded as **the designer's decision from an operator request, which the owner may overturn** ([decision provenance review](../tillr/decision-provenance-review.md)). No Go code or gate configuration is modified in this commit, and no HTML comment is added to this document.

> **In short.** Running the full `tools/tillr-drain/drain` suite (`2,254` top-level tests) on **2026-11-27** at trunk tip `83580e31504c` (`30` commits ahead of proved green commit `895c6cb31b66`) and re-running every candidate failure solo establishes **16 distinct causes** across **61 tests** (**all 38** named proof failures from `81bf66eb` plus **23** additional full-suite failures at `83580e31504c`):
>
> 1. **Tests a landed change really broke:** **9 causes (`C01`, `C05`, `C06`, `C07`, `C08`, `C09`, `C12`, `C13`, `C14`)** covering **30 solo-failing tests** (`23` in the `81bf66eb` list + `7` additional at `83580e31504c`), introduced across `81cd910659bb`, `345d995c4c22`, and `1827b94da801`.
> 2. **Tests that fail only under machine load or beside other tests:** **2 causes (`C02`, `C15`)** covering **11 solo-passing tests** (`1` in the `81bf66eb` list + `10` additional at `83580e31504c`), caused by missing `LaneRoot` in `testRemoteSyncLandingEvidence` (`19f56bb872f0`) polluting package working directory `.requests/` during sharded runs (`C02`, `10` tests) and a hardcoded `30 s` synthetic timestamp in `TestEveryNonLandingOutcomeRecovers` (`74afc09f`) overtaken by background coordinator clock reads under shard load (`C15`, `1` test).
> 3. **Tests whose expectation is stale:** **5 causes (`C03`, `C04`, `C10`, `C11`, `C16`)** covering **20 solo-failing tests** (`14` in the `81bf66eb` list + `6` additional at `83580e31504c`), asserting retired `-cfix` run IDs, pre-coordinator `LoopResult.Ticks` totals, deleted `claim_settled_judge` sub-phases, synchronous `l.pass()` trunk-observation hooks, or `WHERE id = ?` instead of `WHERE item_id = ?` (`5ff3da946501`).
>
> Monolithic `fixer-main-81cd910659bb` cannot converge while multiple causes remain red because its `trunk-green` gate requires the whole `go-test-tillr-drain` suite to pass in one candidate commit while preempting ordinary build items (`D-SBT3`). Close `fixer-main-81cd910659bb` (or hold it until only `C13`, `81cd910659bb`'s single-test `gateland.ReadGatedSeqs` regression, remains) and file the **16 ordered single-cause `build` items (`C01`–`C16`)** in [Section 5](#5-ordered-per-cause-build-item-filings), ordered from `11` failing tests down to `1`, with zero retries, zero timeout increases, and zero skipped tests (`D-SBT4`).

- **Start at:** [Section 1](#1-suite-execution-at-83580e31504c-and-three-way-separation) for the suite run and three-way summary, [Section 2](#2-complete-assignment-of-all-38-named-failures-and-23-tip-extras) for the complete mapping of all `38 + 23 = 61` tests, [Section 3](#3-causes-ordered-from-most-failing-tests-to-fewest) for mechanisms, introducing commits, and fixes, [Section 4](#4-whether-fixer-main-81cd910659bb-should-continue) for the ruling on `fixer-main-81cd910659bb`, and [Section 5](#5-ordered-per-cause-build-item-filings) for the 16 ordered `tillr item request` filings.

---

## 1. Suite execution at `83580e31504c` and three-way separation

### 1.1 Measurement method and reproduction commands

On **2026-11-27**, the `tools/tillr-drain/drain` test inventory (`2,254` top-level `Test*` functions) was executed at trunk tip `83580e31504c4033c1c50677b3c4d407dfc29465` (`30` commits ahead of proved green commit `895c6cb31b66f63751f8bc2cb3cce17a8b3f85e5`, and `6` commits ahead of `81bf66ebd46a` where the trunk proof reported `38` failing top-level tests), followed by isolated single-test runs (`-run '^TestName$'`) to separate deterministic regressions and stale expectations from parallel-shard load and neighbor interference:

```sh
go run -C tools/tillr-gate . shards tools/tillr-drain -k 6

cd tools/tillr-drain && go test -count=1 ./drain -run '^TestName$'
```

Across the `38` tests named at `81bf66eb`, **`37` fail deterministically when run solo** and **`1` passes solo** (`TestEveryNonLandingOutcomeRecovers`, `12.99 s` solo). Across the rest of `tools/tillr-drain/drain` at `83580e31504c`, **`23` additional top-level tests** failed or timed out in full-suite shards: **`13` fail or deadlock deterministically when run solo**, and **`10` pass solo** (tripped by shared package-directory `.requests/run-sync-remote-*.request.json` pollution from `testRemoteSyncLandingEvidence`).

### 1.2 Three-way separation across all 61 failing tests

Under **`D-SBT1`** (*designer's decision from an operator request; the owner may overturn this*), every failing test is assigned to a cause of **one single kind** so no build item mixes production bugfixes with stale-assertion updates or test-isolation fixes:

| Three-way kind | Causes (ordered by test count) | In `81bf66eb` (`38`) | Tip extras at `83580e31504c` (`23`) | Total (`61`) | Solo behavior |
|---|---|---:|---:|---:|---|
| **1. Broken by a landed code change** | `C01` (`11`), `C05` (`5`), `C06` (`4`), `C07` (`3`), `C08` (`2`), `C09` (`2`), `C12` (`1`), `C13` (`1`), `C14` (`1`) | `23` | `7` | **`30`** | Fails or deadlocks solo |
| **2. Fails only under machine load or beside other tests** | `C02` (`10`), `C15` (`1`) | `1` | `10` | **`11`** | Passes solo; fails in parallel shard or shared package directory |
| **3. Test expectation is stale (or broken stub SQL)** | `C03` (`8`), `C04` (`7`), `C10` (`2`), `C11` (`2`), `C16` (`1`) | `14` | `6` | **`20`** | Fails solo on retired contract or broken test SQL |
| **Total** | **`16` causes (`C01`–`C16`)** | **`38`** | **`23`** | **`61`** | `50` solo-red, `11` solo-green |

---

## 2. Complete assignment of all 38 named failures and 23 tip extras

### 2.1 All 38 failing top-level tests from the `81bf66eb` proof

Every one of the `38` top-level tests listed in the request is accounted for below:

| # | Failing test at `81bf66eb` | Test file (`tools/tillr-drain/drain/`) | Cause | Kind | Solo | Introducing commit |
|---:|---|---|---|---|---|---|
| 1 | `TestACandidateFailureDoesNotStopTheLoopAndDoesNotFailOver` | `loop_test.go` | [`C04`](#34-c04-standalone-coordinator-generations-counted-in-loopresult-ticks) | 3 (stale expectation) | Fail | `81cd910659bb`, `345d995c4c22` |
| 2 | `TestAConflictFixCarryingTheLoopsRetirementIsNotTheWorkersEdit` | `retire_conflict_pin_test.go` | [`C01`](#31-c01-stale-trunkcache-after-returned-conflicts-and-design-amendments) | 1 (landed regression) | Fail | `1827b94da801` |
| 3 | `TestARepeatedSquashConflictGoesToItsMoverInsteadOfAnotherRun` | `conflict_test.go` | [`C01`](#31-c01-stale-trunkcache-after-returned-conflicts-and-design-amendments) | 1 (landed regression) | Fail | `1827b94da801` |
| 4 | `TestASquashConflictStagesOneConflictFixRunWithTheMarkersInItsBrief` | `conflict_test.go` | [`C01`](#31-c01-stale-trunkcache-after-returned-conflicts-and-design-amendments) | 1 (landed regression) | Fail | `1827b94da801` |
| 5 | `TestAlertTriageConcurrencyCapWhileBuildLanesBusy` | `alert_triage_test.go` | [`C05`](#35-c05-housekeeping-actor-extraction-dropped-four-pre-actor-pass-contracts) | 1 (landed regression) | Fail | `1827b94da801` |
| 6 | `TestAnInfrastructureFailureHoldsAndRepeatsOnlyOnTheBuildBackend` | `loop_test.go` | [`C12`](#312-c12-coordinator-examination-examineclaimed-omits-recordbackendoutcomes) | 1 (landed regression) | Fail | `1827b94da801` |
| 7 | `TestClaimSettledJudgeSkipsFinishedDesignDocsAndReusesSavedQuestions` | `settle_judge_test.go` | [`C05`](#35-c05-housekeeping-actor-extraction-dropped-four-pre-actor-pass-contracts) | 1 (landed regression) | Fail | `1827b94da801` |
| 8 | `TestConflictFixEndedAtRestartResumesConflictRepairNotFirstNode` | `conflict_fix_restart_test.go` | [`C03`](#33-c03-sibling-conflict-repair-tests-assert-retired-synchronous-cfix-runs) | 3 (stale expectation) | Fail | `81cd910659bb` |
| 9 | `TestConflictFixFailureAndRetryHaveTheirOwnAttempts` | `conflict_identity_test.go` | [`C03`](#33-c03-sibling-conflict-repair-tests-assert-retired-synchronous-cfix-runs) | 3 (stale expectation) | Fail | `81cd910659bb` |
| 10 | `TestConflictFixRunSurvivesInPlaceLoopRestart` | `conflict_fix_restart_test.go` | [`C03`](#33-c03-sibling-conflict-repair-tests-assert-retired-synchronous-cfix-runs) | 3 (stale expectation) | Fail | `81cd910659bb` |
| 11 | `TestConflictRepairReappliesWorkerHeadThenRegeneratesRetirement` | `retire_conflict_test.go` | [`C01`](#31-c01-stale-trunkcache-after-returned-conflicts-and-design-amendments) | 1 (landed regression) | Fail | `1827b94da801` |
| 12 | `TestConflictReturnsToWorkerThreeTimesOnItsBranch` | `landing_session_returns_test.go` | [`C03`](#33-c03-sibling-conflict-repair-tests-assert-retired-synchronous-cfix-runs) | 3 (stale expectation) | Fail | `81cd910659bb` |
| 13 | `TestConflictWithToolsAndMirrorHandsOnlyToolsPathToWorkerAndRegeneratesMirror` | `conflict_mirror_test.go` | [`C01`](#31-c01-stale-trunkcache-after-returned-conflicts-and-design-amendments) | 1 (landed regression) | Fail | `1827b94da801` |
| 14 | `TestDocumentSyncAndQuestionsAreReusedUntilTrunkMoves` | `document_world_test.go` | [`C05`](#35-c05-housekeeping-actor-extraction-dropped-four-pre-actor-pass-contracts) | 1 (landed regression) | Fail | `1827b94da801` |
| 15 | `TestDrainNotifyAndNoNotifyParityUnderSteppedClock` | `trigger_notify_test.go` | [`C09`](#39-c09-coordinator-fallback-timers-bypass-triggertimer-and-direct-exit-handoff) | 1 (landed regression) | Fail | `345d995c4c22` |
| 16 | `TestEveryFoldReadsTheKindsItSelects` | `whole_log_test.go` | [`C13`](#313-c13-gateland-readgatedseqs-selects-event-kind-without-checking-kind) | 1 (landed regression) | Fail | `81cd910659bb` |
| 17 | `TestEveryNonLandingOutcomeRecovers` | `loop_test.go` | [`C15`](#315-c15-testeverynonlandingoutcomerecovers-hardcodes-synthetic-recovery-timestamp) | 2 (fails under load) | Pass (`12.99 s`) | `74afc09f` + `81cd910659bb` |
| 18 | `TestExaminationRecordsSubPhasesAndAccountsForWallTime` | `handoff_test.go` | [`C10`](#310-c10-examination-and-worker-gate-tests-assert-deleted-sub-phases) | 3 (stale expectation) | Fail | `81cd910659bb` |
| 19 | `TestInterruptedRecoveredRepairSettlesExistingCommitWithoutColdRestart` | `conflict_recovery_restart_test.go` | [`C03`](#33-c03-sibling-conflict-repair-tests-assert-retired-synchronous-cfix-runs) | 3 (stale expectation) | Fail | `81cd910659bb` |
| 20 | `TestItemFaultHoldsOnlyItsItem` | `item_fault_test.go` | [`C08`](#38-c08-finishcandidateoutcome-and-coordinator-gate-faults-leave-lanes-leased) | 1 (landed regression) | Fail | `81cd910659bb` |
| 21 | `TestItemWorktreesConflictFixDispatchedToItemDirectory` | `item_worktrees_return_ext_test.go` | [`C01`](#31-c01-stale-trunkcache-after-returned-conflicts-and-design-amendments) | 1 (landed regression) | Fail | `1827b94da801` |
| 22 | `TestParallelReadersHoldThenRestartOnlyFailedHarness` | `parallel_semantics_test.go` | [`C06`](#36-c06-partial-parallel-reader-restart-skipped-when-first-node-finished) | 1 (landed regression) | Fail | `345d995c4c22` |
| 23 | `TestParallelReadersOrphanedUnjudgedReaderRestartsOnlyIt` | `parallel_continuation_test.go` | [`C06`](#36-c06-partial-parallel-reader-restart-skipped-when-first-node-finished) | 1 (landed regression) | Fail | `345d995c4c22` |
| 24 | `TestParallelReadersRestartOnlyInterruptedNodes` | `parallel_restart_test.go` | [`C06`](#36-c06-partial-parallel-reader-restart-skipped-when-first-node-finished) | 1 (landed regression) | Fail | `345d995c4c22` |
| 25 | `TestPassStartsAfterMergeProofBeforeASyncFault` | `heavy_fault_retry_test.go` | [`C05`](#35-c05-housekeeping-actor-extraction-dropped-four-pre-actor-pass-contracts) | 1 (landed regression) | Fail | `1827b94da801` |
| 26 | `TestReapingWithoutDispatchIsAnAppliedPassBeforeQueueDrained` | `trigger_test.go` | [`C07`](#37-c07-reapdeadandexpired-in-rungcstep-omits-passtiming-and-generation-bounds) | 1 (landed regression) | Fail | `1827b94da801` |
| 27 | `TestRestartGatesAndLandsTheRetiredHead` | `retire_ext_test.go` | [`C08`](#38-c08-finishcandidateoutcome-and-coordinator-gate-faults-leave-lanes-leased) | 1 (landed regression) | Fail | `81cd910659bb` |
| 28 | `TestSeatDefaultConflictFixRunsOneNode` | `workflow_default_conflict_test.go` | [`C01`](#31-c01-stale-trunkcache-after-returned-conflicts-and-design-amendments) | 1 (landed regression) | Fail | `1827b94da801` |
| 29 | `TestSeatSharesParallelRestartWaitsForClosedSibling` | `seat_shares_lifecycle_test.go` | [`C06`](#36-c06-partial-parallel-reader-restart-skipped-when-first-node-finished) | 1 (landed regression) | Fail | `345d995c4c22` |
| 30 | `TestSeparateLeaseExpiriesEachRecordTheirGenerationTiming` | `trigger_test.go` | [`C07`](#37-c07-reapdeadandexpired-in-rungcstep-omits-passtiming-and-generation-bounds) | 1 (landed regression) | Fail | `1827b94da801` |
| 31 | `TestSquashConflictRestagesOnTheTrunkTipWhenTheReplayConflicts` | `conflict_test.go` | [`C01`](#31-c01-stale-trunkcache-after-returned-conflicts-and-design-amendments) | 1 (landed regression) | Fail | `1827b94da801` |
| 32 | `TestTheLoopHasNoTickBudget` | `loop_test.go` | [`C04`](#34-c04-standalone-coordinator-generations-counted-in-loopresult-ticks) | 3 (stale expectation) | Fail | `81cd910659bb`, `345d995c4c22` |
| 33 | `TestTheLoopMeasuresWhatEveryRunSpentAndRunsOn` | `loop_test.go` | [`C04`](#34-c04-standalone-coordinator-generations-counted-in-loopresult-ticks) | 3 (stale expectation) | Fail | `81cd910659bb`, `345d995c4c22` |
| 34 | `TestTheLoopPrintsOneSpendLinePerRunAndComparesNoCeiling` | `loop_test.go` | [`C04`](#34-c04-standalone-coordinator-generations-counted-in-loopresult-ticks) | 3 (stale expectation) | Fail | `81cd910659bb`, `345d995c4c22` |
| 35 | `TestTwoConsecutiveTicksDispatchAndRecord` | `loop_test.go` | [`C04`](#34-c04-standalone-coordinator-generations-counted-in-loopresult-ticks) | 3 (stale expectation) | Fail | `81cd910659bb`, `345d995c4c22` |
| 36 | `TestTwoLoopsInOneSecondKeepBothGenerationReports` | `retire_test.go` | [`C07`](#37-c07-reapdeadandexpired-in-rungcstep-omits-passtiming-and-generation-bounds) | 1 (landed regression) | Fail | `1827b94da801` |
| 37 | `TestWorkerEndStartsGateWithoutPassOrSiblingLanding` | `worker_gate_test.go` | [`C10`](#310-c10-examination-and-worker-gate-tests-assert-deleted-sub-phases) | 3 (stale expectation) | Fail | `81cd910659bb` |
| 38 | `TestWorkflowSecondIdenticalConflictRestartsFromFirstStageAndThirdParksForOwner` | `conflict_restart_e2e_test.go` | [`C03`](#33-c03-sibling-conflict-repair-tests-assert-retired-synchronous-cfix-runs) | 3 (stale expectation) | Fail | `81cd910659bb` |

### 2.2 The 23 additional full-suite failures observed at `83580e31504c`

Because `go-test-tillr-drain` cannot turn green until every failing test in `tools/tillr-drain/drain` at `83580e31504c` is green, the `23` additional full-suite failures at `83580e31504c` are assigned to the same cause table:

| # | Additional failing test at `83580e31504c` | Test file (`tools/tillr-drain/drain/`) | Cause | Kind | Solo | Introducing commit |
|---:|---|---|---|---|---|---|
| 39 | `TestADrainThatRefusedEveryCandidateNamesItAsARepeatedRefusal` | `loop_test.go` | [`C04`](#34-c04-standalone-coordinator-generations-counted-in-loopresult-ticks) | 3 (stale expectation) | Fail | `81cd910659bb`, `345d995c4c22` |
| 40 | `TestAGateFailureRedOnTheBaseAcquitsTheItem` | `trunk_ext_test.go` | [`C02`](#32-c02-empty-laneroot-in-testremotesynclandingevidence-pollutes-package-requests) | 2 (neighbor interference) | Pass | `19f56bb872f0` |
| 41 | `TestALandedCandidateMovesItsItemToLanded` | `loop_test.go` | [`C04`](#34-c04-standalone-coordinator-generations-counted-in-loopresult-ticks) | 3 (stale expectation) | Pass/Fail (`Ticks`) | `81cd910659bb`, `345d995c4c22` |
| 42 | `TestAnIdenticalRepeatDesignAmendmentDispatchesOriginalSeat` | `blocked_answer_test.go` | [`C01`](#31-c01-stale-trunkcache-after-returned-conflicts-and-design-amendments) | 1 (landed regression) | Fail | `1827b94da801` |
| 43 | `TestBaseReplayEventsAreRegisteredAndRecordedThroughDrain` | `base_replay_events_test.go` | [`C02`](#32-c02-empty-laneroot-in-testremotesynclandingevidence-pollutes-package-requests) | 2 (neighbor interference) | Pass | `19f56bb872f0` |
| 44 | `TestCompletionHandsOffWorkflowWithoutAnotherFullPass` | `completion_handoff_test.go` | [`C09`](#39-c09-coordinator-fallback-timers-bypass-triggertimer-and-direct-exit-handoff) | 1 (landed regression) | Fail | `345d995c4c22` |
| 45 | `TestDesignAmendmentLandsThenDispatchesOriginalSeat` | `blocked_answer_test.go` | [`C01`](#31-c01-stale-trunkcache-after-returned-conflicts-and-design-amendments) | 1 (landed regression) | Fail | `1827b94da801` |
| 46 | `TestGateRefusalUsesExactTrunkProof` | `gate_proof_test.go` | [`C02`](#32-c02-empty-laneroot-in-testremotesynclandingevidence-pollutes-package-requests) | 2 (neighbor interference) | Pass | `19f56bb872f0` |
| 47 | `TestItemWorktreesRepairStagingFailureSurvivesRestartWithoutCancelingSiblings` | `item_worktrees_restart_ext_test.go` | [`C03`](#33-c03-sibling-conflict-repair-tests-assert-retired-synchronous-cfix-runs) | 3 (stale expectation) | Fail | `81cd910659bb` |
| 48 | `TestLoopHoldsSpentWindowAcrossRestartAndReopens` | `quota_window_ext_test.go` | [`C02`](#32-c02-empty-laneroot-in-testremotesynclandingevidence-pollutes-package-requests) | 2 (neighbor interference) | Pass | `19f56bb872f0` |
| 49 | `TestLoopUsesBriefTemplateFromSettings` | `brief_template_test.go` | [`C02`](#32-c02-empty-laneroot-in-testremotesynclandingevidence-pollutes-package-requests) | 2 (neighbor interference) | Pass | `19f56bb872f0` |
| 50 | `TestMaxAgentsFlagAndScaleAlias` | `live_scale_stop_test.go` | [`C02`](#32-c02-empty-laneroot-in-testremotesynclandingevidence-pollutes-package-requests) | 2 (neighbor interference) | Pass | `19f56bb872f0` |
| 51 | `TestNonLandingLoopStartsWithoutGit` | `definition_startup_test.go` | [`C05`](#35-c05-housekeeping-actor-extraction-dropped-four-pre-actor-pass-contracts) | 1 (landed regression) | Fail | `1827b94da801` |
| 52 | `TestNonLandingPreservesCriteriaAndAllowsSync` | `landing_refusal_test.go` | [`C01`](#31-c01-stale-trunkcache-after-returned-conflicts-and-design-amendments) | 1 (landed regression) | Fail | `1827b94da801` |
| 53 | `TestPassRecordsSharedCoreWorktreeWithoutChangingConfig` | `trunk_worktree_test.go` | [`C11`](#311-c11-direct-pass-unit-tests-omit-runtrunkproofstep-trunk-observation) | 3 (stale expectation) | Fail | `1827b94da801` |
| 54 | `TestPendingRestartAnsweredDuringSlowExamination` | `worker_gate_test.go` | [`C14`](#314-c14-claimsettledcandidatedirect-calls-now-inside-writetx) | 1 (landed regression) | Timeout | `81cd910659bb` |
| 55 | `TestRepairStagingFailureSurvivesRestartWithoutCancelingSiblings` | `conflict_hold_test.go` | [`C03`](#33-c03-sibling-conflict-repair-tests-assert-retired-synchronous-cfix-runs) | 3 (stale expectation) | Fail | `81cd910659bb` |
| 56 | `TestSiblingWriteConvictsOnlyItsRun` | `owner_edits_test.go` | [`C02`](#32-c02-empty-laneroot-in-testremotesynclandingevidence-pollutes-package-requests) | 2 (neighbor interference) | Pass | `19f56bb872f0` |
| 57 | `TestStopMidPassWhileUpdateRefHoldsLockLeavesNextLoopClean` | `stop_prompt_test.go` | [`C11`](#311-c11-direct-pass-unit-tests-omit-runtrunkproofstep-trunk-observation) | 3 (stale expectation) | Fail | `1827b94da801` |
| 58 | `TestTrunkBranchFullLoopWithoutMain` | `trunk_branch_setting_test.go` | [`C16`](#316-c16-testtrunkbranchfullloopwithoutmain-stub-queries-id-instead-of-item_id) | 3 (stale/broken test SQL) | Fail | `5ff3da946501` |
| 59 | `TestTrunkBranchMainCheckoutOnOtherBranch` | `trunk_branch_setting_test.go` | [`C02`](#32-c02-empty-laneroot-in-testremotesynclandingevidence-pollutes-package-requests) | 2 (neighbor interference) | Pass | `19f56bb872f0` |
| 60 | `TestWhyAfterRestartOverLiveEvaluator` | `restart_evaluator_report_test.go` | [`C02`](#32-c02-empty-laneroot-in-testremotesynclandingevidence-pollutes-package-requests) | 2 (neighbor interference) | Pass | `19f56bb872f0` |
| 61 | `TestWorkflowExitCandidatePolicies` | `workflow_exit_test.go` | [`C02`](#32-c02-empty-laneroot-in-testremotesynclandingevidence-pollutes-package-requests) | 2 (neighbor interference) | Pass | `19f56bb872f0` |

---

## 3. Causes ordered from most failing tests to fewest

Under **`D-SBT2`** (*designer's decision from an operator request; the owner may overturn this*), the 16 causes are ordered from breaking the most tests (`11`) to the fewest (`1`), breaking ties by count in the `81bf66eb` proof list first and then by kind (`Kind 1` before `Kind 2` before `Kind 3`). Under **`D-SBT4`**, every fix repairs production code or test logic directly — never by adding retries, raising time limits, or skipping tests — and every edit under `tools/tillr*` runs `just pack-sync` in the same commit.

### 3.1 C01 stale TrunkCache after returned conflicts and design amendments

- **Failing tests:** `11` (`8` in `81bf66eb` + `3` at `83580e31504c`).
- **Kind:** `1` — Broken by a landed code change.
- **Introduced by:** `1827b94da801` (`feat(tillr-drain): pure scheduling pass, background housekeeping actors, and retired-test manifest`).
- **Mechanism:** Under `l.actorsActive()`, `passPlansMode` in `tools/tillr-drain/drain/pass.go` reads `l.cachedTrunkRefs()` instead of re-reading git refs inline. When `conflictFix` in `tools/tillr-drain/drain/conflict.go` returns a squash conflict under `isLandCoordinatorActive()` after `TrunkRef` moved (for instance via a test `Steps.Land` stub or concurrent landing) or `settleDesignAnswer` in `tools/tillr-drain/drain/blocked.go` lands a design amendment onto `TrunkRef`, neither path updates `l.actors.SetCachedTrunk` before waking the scheduling pass. The returned or next run stages against the stale `BaseSHA` and immediately parks on `conflict-owner`.
- **Fix:** Refresh `l.actors.SetCachedTrunk` from the newly observed trunk SHA (and call `signalTrunkProofWake`) inside `conflictFix`'s coordinator return path (`conflict.go`) and after `settleDesignAnswer` (`blocked.go`) advances `TrunkRef`, then run `just pack-sync`.
- **Tests to turn green (`11`):**
  - `TestAConflictFixCarryingTheLoopsRetirementIsNotTheWorkersEdit`
  - `TestARepeatedSquashConflictGoesToItsMoverInsteadOfAnotherRun`
  - `TestASquashConflictStagesOneConflictFixRunWithTheMarkersInItsBrief`
  - `TestConflictRepairReappliesWorkerHeadThenRegeneratesRetirement`
  - `TestConflictWithToolsAndMirrorHandsOnlyToolsPathToWorkerAndRegeneratesMirror`
  - `TestItemWorktreesConflictFixDispatchedToItemDirectory`
  - `TestSeatDefaultConflictFixRunsOneNode`
  - `TestSquashConflictRestagesOnTheTrunkTipWhenTheReplayConflicts`
  - `TestNonLandingPreservesCriteriaAndAllowsSync`
  - `TestDesignAmendmentLandsThenDispatchesOriginalSeat`
  - `TestAnIdenticalRepeatDesignAmendmentDispatchesOriginalSeat`

### 3.2 C02 empty LaneRoot in testRemoteSyncLandingEvidence pollutes package requests

- **Failing tests:** `10` at `83580e31504c` (plus neighbor `TestRemoteSyncLandingSettlesRealItemEvidence`).
- **Kind:** `2` — Fails only beside other tests in the same shard/working directory.
- **Introduced by:** `19f56bb872f0` (`feat(tillr-loop): port loop adapter from Python to Go`), amplified across standalone staging in `345d995c4c22`.
- **Mechanism:** `testRemoteSyncLandingEvidence` in `tools/tillr-drain/drain/remote_sync_landing_test.go` constructs `&ProcessSteps{Root: p.f.root, Store: p.f.storePath, Now: p.l.cfg.Now, ItemBin: bin, VerdictProgram: "tillr-loop verdict --tillr-item " + bin}` without `LaneRoot: p.l.cfg.LaneRoot`. When `ProcessSteps.Stage` (`tools/tillr-drain/drain/steps.go`) or `stageAssignedRun` (`tools/tillr-drain/drain/start_stage.go`) evaluates `filepath.Join("", ".requests")`, it writes `.requests/run-sync-remote-*.request.json` into the Go package directory (`tools/tillr-drain/drain/.requests/`), dirtying the checkout and failing boundary/worktree checks in sibling tests in the same shard.
- **Fix:** Set `LaneRoot: p.l.cfg.LaneRoot` in `testRemoteSyncLandingEvidence` (`remote_sync_landing_test.go`) and reject an empty `LaneRoot` in `ProcessSteps.Stage` (`steps.go`) and `stageAssignedRun` (`start_stage.go`) with a unit test verifying no `.requests` directory is ever created under CWD when `LaneRoot` is unset, then run `just pack-sync`.
- **Tests to turn green (`10`):**
  - `TestAGateFailureRedOnTheBaseAcquitsTheItem`
  - `TestBaseReplayEventsAreRegisteredAndRecordedThroughDrain`
  - `TestGateRefusalUsesExactTrunkProof`
  - `TestLoopHoldsSpentWindowAcrossRestartAndReopens`
  - `TestLoopUsesBriefTemplateFromSettings`
  - `TestMaxAgentsFlagAndScaleAlias`
  - `TestSiblingWriteConvictsOnlyItsRun`
  - `TestTrunkBranchMainCheckoutOnOtherBranch`
  - `TestWhyAfterRestartOverLiveEvaluator`
  - `TestWorkflowExitCandidatePolicies`

### 3.3 C03 sibling conflict repair tests assert retired synchronous cfix runs

- **Failing tests:** `8` (`6` in `81bf66eb` + `2` at `83580e31504c`).
- **Kind:** `3` — Test expectation is stale.
- **Introduced by:** `81cd910659bb` (`feat(tillr-drain): extract standalone gate and FIFO land coordinators`).
- **Mechanism:** `81cd910659bb` (`conflictFix` in `tools/tillr-drain/drain/conflict.go` and `resetLaneCheckoutToTrunk` in `tools/tillr-drain/drain/worker_session.go`) deliberately replaced synchronous `-cfix` runs under `isLandCoordinatorActive()` with returned resume nodes staged on current trunk (`Returned.Cause == RefusalConflict`, `spec.Continue = false`), updating `conflict_test.go` and `conflict_mirror_test.go` while leaving `8` tests in sibling files asserting `-cfix` run IDs, `ConflictFixes > 0`, synchronous `RepairHold`, or un-reset candidate `HEAD`.
- **Fix:** Update those `8` tests to assert the returned-conflict resume contract (`Returned.Cause == RefusalConflict`, `ConflictFixes == 0`, trunk-reset checkout on `RefusalConflict`, and stage-failure holds on returned conflict runs), sequenced `--after-item` `C01` and mirrored with `just pack-sync`.
- **Tests to turn green (`8`):**
  - `TestConflictFixEndedAtRestartResumesConflictRepairNotFirstNode`
  - `TestConflictFixFailureAndRetryHaveTheirOwnAttempts`
  - `TestConflictFixRunSurvivesInPlaceLoopRestart`
  - `TestConflictReturnsToWorkerThreeTimesOnItsBranch`
  - `TestInterruptedRecoveredRepairSettlesExistingCommitWithoutColdRestart`
  - `TestWorkflowSecondIdenticalConflictRestartsFromFirstStageAndThirdParksForOwner`
  - `TestRepairStagingFailureSurvivesRestartWithoutCancelingSiblings`
  - `TestItemWorktreesRepairStagingFailureSurvivesRestartWithoutCancelingSiblings`

### 3.4 C04 standalone coordinator generations counted in LoopResult Ticks

- **Failing tests:** `7` (`5` in `81bf66eb` + `2` at `83580e31504c`).
- **Kind:** `3` — Test expectation is stale.
- **Introduced by:** `81cd910659bb` (`feat(tillr-drain): extract standalone gate and FIFO land coordinators`) and `345d995c4c22` (`feat(tillr-drain): extract standalone stage and launch coordinators`).
- **Mechanism:** Standalone stage, gate, and land claims (`start_coordinator.go`, `worker_gate.go`, `settle_judge.go`, `judge_landing.go`) allocate store generations and call `countGeneration(result, generation)`, increasing `result.Ticks` (`8` instead of `4`, `11` instead of `8`) in `7` tests in `tools/tillr-drain/drain/loop_test.go` written when dispatch, gate, and land shared a single pass generation.
- **Fix:** Update those `7` assertions in `loop_test.go` to check generation monotonicity, `FirstGeneration`/`LastGeneration` span, and landed/refused counts under standalone coordinator generations, sequenced `--after-item` `C01` (because `TestACandidateFailureDoesNotStopTheLoopAndDoesNotFailOver` exercises returned conflict staging) and mirrored with `just pack-sync`.
- **Tests to turn green (`7`):**
  - `TestACandidateFailureDoesNotStopTheLoopAndDoesNotFailOver`
  - `TestTheLoopHasNoTickBudget`
  - `TestTheLoopMeasuresWhatEveryRunSpentAndRunsOn`
  - `TestTheLoopPrintsOneSpendLinePerRunAndComparesNoCeiling`
  - `TestTwoConsecutiveTicksDispatchAndRecord`
  - `TestALandedCandidateMovesItsItemToLanded`
  - `TestADrainThatRefusedEveryCandidateNamesItAsARepeatedRefusal`

### 3.5 C05 housekeeping actor extraction dropped four pre-actor pass contracts

- **Failing tests:** `5` (`4` in `81bf66eb` + `1` at `83580e31504c`).
- **Kind:** `1` — Broken by a landed code change.
- **Introduced by:** `1827b94da801` (`feat(tillr-drain): pure scheduling pass, background housekeeping actors, and retired-test manifest`).
- **Mechanism:** Extracting sync, trunk proof, and triage from `passPlansMode` into `seedActorCaches` and `runGCStep` (`tools/tillr-drain/drain/pass.go` and `tools/tillr-drain/drain/settle_judge.go`) dropped four pre-existing contracts:
  - `seedActorCaches` and `startBackgroundActors` probe git and sync even when `l.nonLanding` is true;
  - `seedActorCaches` returns a sync fault before starting after-merge trunk proof;
  - `consumeTriageWake()` wakes the scheduling pass without running `dispatchAlertTriage` or waking the GC actor;
  - direct `l.pass()` and `claimSettledCandidateDirect` stopped reusing `syncedTrunkSHA` and saved questions across an unchanged trunk SHA.
- **Fix:** Guard git/sync startup on `!l.nonLanding`, start after-merge trunk proof before returning sync errors, invoke `dispatchAlertTriage` (or wake the GC actor) when `consumeTriageWake()` fires, and preserve `syncedTrunkSHA` and cached questions across unchanged trunk SHAs in `pass.go` and `settle_judge.go`, then run `just pack-sync`.
- **Tests to turn green (`5`):**
  - `TestAlertTriageConcurrencyCapWhileBuildLanesBusy`
  - `TestClaimSettledJudgeSkipsFinishedDesignDocsAndReusesSavedQuestions`
  - `TestDocumentSyncAndQuestionsAreReusedUntilTrunkMoves`
  - `TestPassStartsAfterMergeProofBeforeASyncFault`
  - `TestNonLandingLoopStartsWithoutGit`

### 3.6 C06 partial parallel reader restart skipped when first node finished

- **Failing tests:** `4` (all in `81bf66eb`).
- **Kind:** `1` — Broken by a landed code change.
- **Introduced by:** `345d995c4c22` (`feat(tillr-drain): extract standalone stage and launch coordinators`).
- **Mechanism:** `restartParallel` in `tools/tillr-drain/drain/parallel_restart.go` appends `-restart` only to interrupted reader nodes while keeping already-completed `nodes[0]` as `d.RunRequest.RunID`. Both `findStageableRuns` and `stageAssignedRun` in `tools/tillr-drain/drain/start_stage.go` check `stagelaunch.RunRecordExists(r, d.RunRequest.RunID)` alone, see `nodes[0]`'s terminal `runs` row, and skip staging and launching the restarted sibling readers.
- **Fix:** Check `stagelaunch.RunRecordExists` across all nodes in `parallelNodes(d)` in `findStageableRuns` and `stageAssignedRun` (`start_stage.go`) so any parallel decision with an unrecorded restarted node is staged and launched, then run `just pack-sync`.
- **Tests to turn green (`4`):**
  - `TestParallelReadersHoldThenRestartOnlyFailedHarness`
  - `TestParallelReadersOrphanedUnjudgedReaderRestartsOnlyIt`
  - `TestParallelReadersRestartOnlyInterruptedNodes`
  - `TestSeatSharesParallelRestartWaitsForClosedSibling`

### 3.7 C07 reapDeadAndExpired in runGCStep omits PassTiming and generation bounds

- **Failing tests:** `3` (all in `81bf66eb`).
- **Kind:** `1` — Broken by a landed code change.
- **Introduced by:** `1827b94da801` (`feat(tillr-drain): pure scheduling pass, background housekeeping actors, and retired-test manifest`).
- **Mechanism:** Moving `reapDeadAndExpired` (`tools/tillr-drain/drain/worktree_collect.go`) into `runGCStep` (`tools/tillr-drain/drain/pass.go`) stopped counting a reap-only generation as an applied pass before `queue-drained`, stopped appending a `PassTiming` entry per separate lease expiry, and left `FirstGeneration` and `LastGeneration` unset (`drain-0-0.json`).
- **Fix:** Record `PassTiming`, `countGeneration(result, generation)`, and applied-pass progression whenever `reapDeadAndExpired` allocates a generation in `runGCStep` and direct `passPlansMode`, then run `just pack-sync`.
- **Tests to turn green (`3`):**
  - `TestReapingWithoutDispatchIsAnAppliedPassBeforeQueueDrained`
  - `TestSeparateLeaseExpiriesEachRecordTheirGenerationTiming`
  - `TestTwoLoopsInOneSecondKeepBothGenerationReports`

### 3.8 C08 finishCandidateOutcome and coordinator gate faults leave lanes leased

- **Failing tests:** `2` (both in `81bf66eb`).
- **Kind:** `1` — Broken by a landed code change.
- **Introduced by:** `81cd910659bb` (`feat(tillr-drain): extract standalone gate and FIFO land coordinators`).
- **Mechanism:** In `tools/tillr-drain/drain/settle_judge.go`, when an item faults onto `settle-failed` or `gate-harness-error` without `stopLaneRetained`, `finishCandidateOutcome` and coordinator gate-error settlement hold the item while leaving the lane lease and examination reservation active, causing the loop to stop with `lanes-leased` instead of isolating the fault to the held item.
- **Fix:** Release the lane lease and examination reservation in `finishCandidateOutcome` and coordinator gate-error settlement (`settle_judge.go`) whenever the candidate is preserved or abandoned rather than `stopLaneRetained`, then run `just pack-sync`.
- **Tests to turn green (`2`):**
  - `TestItemFaultHoldsOnlyItsItem`
  - `TestRestartGatesAndLandsTheRetiredHead`

### 3.9 C09 coordinator fallback timers bypass triggerTimer and direct exit handoff

- **Failing tests:** `2` (`1` in `81bf66eb` + `1` at `83580e31504c`).
- **Kind:** `1` — Broken by a landed code change.
- **Introduced by:** `345d995c4c22` (`feat(tillr-drain): extract standalone stage and launch coordinators`).
- **Mechanism:** Standalone coordinators use wall-clock `time.NewTimer` fallback polls rather than `l.triggerTimer` and miss in-process coordinator wake signals under stepped clocks (`TestDrainNotifyAndNoNotifyParityUnderSteppedClock`), while direct `waitForTrigger` worker-exit handoff (when standalone examine/stage coordinators are inactive) stops after recording unsettled exit instead of settling the completed node and dispatching the next workflow node (`TestCompletionHandsOffWorkflowWithoutAnotherFullPass`).
- **Fix:** Drive coordinator fallback timers through `l.triggerTimer` and signal coordinator wakes on in-process transitions, and let direct `waitForTrigger` completion handoff complete settlement and next-node dispatch when standalone coordinators are inactive, then run `just pack-sync`.
- **Tests to turn green (`2`):**
  - `TestDrainNotifyAndNoNotifyParityUnderSteppedClock`
  - `TestCompletionHandsOffWorkflowWithoutAnotherFullPass`

### 3.10 C10 examination and worker gate tests assert deleted sub-phases

- **Failing tests:** `2` (both in `81bf66eb`).
- **Kind:** `3` — Test expectation is stale.
- **Introduced by:** `81cd910659bb` (`feat(tillr-drain): extract standalone gate and FIFO land coordinators`).
- **Mechanism:** `81cd910659bb` replaced `claim_settled_judge` and `claim_judge_plan` with `candidate_gate` and `post_gate`, leaving `TestExaminationRecordsSubPhasesAndAccountsForWallTime` (`handoff_test.go`) and `TestWorkerEndStartsGateWithoutPassOrSiblingLanding` (`worker_gate_test.go`) asserting the deleted sub-phase keys.
- **Fix:** Update `handoff_test.go` and `worker_gate_test.go` to assert `candidate_gate` and `post_gate` sub-phases and wall-time accounting, then run `just pack-sync`.
- **Tests to turn green (`2`):**
  - `TestExaminationRecordsSubPhasesAndAccountsForWallTime`
  - `TestWorkerEndStartsGateWithoutPassOrSiblingLanding`

### 3.11 C11 direct pass unit tests omit runTrunkProofStep trunk observation

- **Failing tests:** `2` (at `83580e31504c`).
- **Kind:** `3` — Test expectation is stale.
- **Introduced by:** `1827b94da801` (`feat(tillr-drain): pure scheduling pass, background housekeeping actors, and retired-test manifest`).
- **Mechanism:** `1827b94da801` moved shared core-worktree recording and trunk observation out of synchronous `l.pass()` into `runTrunkProofStep`, leaving `TestPassRecordsSharedCoreWorktreeWithoutChangingConfig` (`trunk_worktree_test.go`) and `TestStopMidPassWhileUpdateRefHoldsLockLeavesNextLoopClean` (`stop_prompt_test.go`) calling `l.pass()` alone.
- **Fix:** Update `trunk_worktree_test.go` and `stop_prompt_test.go` to exercise the trunk-observation / `runTrunkProofStep` step alongside `l.pass()`, then run `just pack-sync`.
- **Tests to turn green (`2`):**
  - `TestPassRecordsSharedCoreWorktreeWithoutChangingConfig`
  - `TestStopMidPassWhileUpdateRefHoldsLockLeavesNextLoopClean`

### 3.12 C12 coordinator examination examineClaimed omits RecordBackendOutcomes

- **Failing tests:** `1` (in `81bf66eb`).
- **Kind:** `1` — Broken by a landed code change.
- **Introduced by:** `1827b94da801` (`feat(tillr-drain): pure scheduling pass, background housekeeping actors, and retired-test manifest`).
- **Mechanism:** `examineClaimed` in `tools/tillr-drain/drain/pass.go` settles terminal backend failures without calling `RecordBackendOutcomes(ctx, l.cfg.Store)` as `settleWorker` in `tools/tillr-drain/drain/loop.go` did, leaving build-backend failure holds unrecorded.
- **Fix:** Call `RecordBackendOutcomes(ctx, l.cfg.Store)` after recording terminal failure in `examineClaimed` (`pass.go`), then run `just pack-sync`.
- **Test to turn green (`1`):**
  - `TestAnInfrastructureFailureHoldsAndRepeatsOnlyOnTheBuildBackend`

### 3.13 C13 gateland ReadGatedSeqs selects event kind without checking Kind

- **Failing tests:** `1` (in `81bf66eb`).
- **Kind:** `1` — Broken by a landed code change.
- **Introduced by:** `81cd910659bb` (`feat(tillr-drain): extract standalone gate and FIFO land coordinators`).
- **Mechanism:** `ReadGatedSeqs(r *store.Reader, kind string)` in `tools/tillr-drain/internal/gateland/coordinator.go` calls `r.EventsOfKind(0, kind)` and iterates `for _, e := range events` without checking `e.Kind == kind` (or selecting a concrete kind constant matched in a `switch`/condition), failing `TestEveryFoldReadsTheKindsItSelects` in `tools/tillr-drain/drain/whole_log_test.go` with `ReadGatedSeqs selects kind but never reads it`.
- **Fix:** Guard `e.Kind == kind` (or bind the concrete `CandidateGatedKind` constant and read `e.Kind`) in `gateland.ReadGatedSeqs` (`tools/tillr-drain/internal/gateland/coordinator.go`), then run `just pack-sync`.
- **Test to turn green (`1`):**
  - `TestEveryFoldReadsTheKindsItSelects`

### 3.14 C14 claimSettledCandidateDirect calls Now inside WriteTx

- **Failing tests:** `1` (at `83580e31504c`).
- **Kind:** `1` — Broken by a landed code change.
- **Introduced by:** `81cd910659bb` (`feat(tillr-drain): extract standalone gate and FIFO land coordinators`).
- **Mechanism:** `claimSettledCandidateDirect` in `tools/tillr-drain/drain/settle_judge.go` invokes `l.cfg.Now()` inside `s.WriteTx(...)`. In `TestPendingRestartAnsweredDuringSlowExamination` (`tools/tillr-drain/drain/worker_gate_test.go`), the test's `Now` hook blocks to simulate a slow examination while `restartHarness.request` attempts a concurrent store write on the same SQLite store, deadlocking in `store.(*writeQueue).enter`.
- **Fix:** Sample `now := l.cfg.Now()` before opening `s.WriteTx` in `claimSettledCandidateDirect` (and any sibling claim helper in `settle_judge.go`), then run `just pack-sync`.
- **Test to turn green (`1`):**
  - `TestPendingRestartAnsweredDuringSlowExamination`

### 3.15 C15 TestEveryNonLandingOutcomeRecovers hardcodes synthetic recovery timestamp

- **Failing tests:** `1` (in `81bf66eb`).
- **Kind:** `2` — Fails only under machine load.
- **Introduced by:** Synthetic per-call clock in `74afc09f` (`test(drain): vary candidate.txt per run in TestEveryNonLandingOutcomeRecovers`, present since `3973c2a0`), exposed under parallel shard load by background coordinator `Now()` calls in `81cd910659bb`, `345d995c4c22`, and `1827b94da801`.
- **Mechanism:** `TestEveryNonLandingOutcomeRecovers` in `tools/tillr-drain/drain/loop_test.go` advances a shared synthetic clock `now()` by `1 s` on every `now()` invocation starting from `2026-11-12T00:00:00Z`, then hardcodes `"2026-11-12T00:00:30Z"` in `driveOK(t, item, "apply", ..., "--now", "2026-11-12T00:00:30Z")`, `authorizeRetryWith(..., "2026-11-12T00:00:30Z", ...)`, and `tick(t, "2026-11-12T00:00:30Z", ...)`. Solo (`12.99 s`), `first.Run` finishes in fewer than `30` `now()` calls; under parallel shard CPU and I/O contention, `first.Run`'s `6` serial subprocess executions take longer in wall time, so background coordinator poll ticks call `l.cfg.Now()` more than `30` times, stamping run and conflict timestamps after `2026-11-12T00:00:30Z` and invalidating the hardcoded recovery authorizations.
- **Fix:** Sample `recoveryAt := now().UTC().Format(time.RFC3339)` from `TestEveryNonLandingOutcomeRecovers`'s own monotonic `now()` clock after `first.Run` returns (passing `recoveryAt` to `apply`, `authorizeRetryWith`, and `tick`), with **no retry, no longer time limit, and no skipped test**, then run `just pack-sync`.
- **Test to turn green (`1`):**
  - `TestEveryNonLandingOutcomeRecovers`

### 3.16 C16 TestTrunkBranchFullLoopWithoutMain stub queries id instead of item_id

- **Failing tests:** `1` (at `83580e31504c`).
- **Kind:** `3` — Test expectation / inline SQL stub is broken.
- **Introduced by:** `5ff3da946501` (`feat(settings,drain,land,view,run,watch): configure landing branch via [trunk] branch`).
- **Mechanism:** In `TestTrunkBranchFullLoopWithoutMain` (`tools/tillr-drain/drain/trunk_branch_setting_test.go`), the inline `ItemState` test stub executes `UPDATE items SET state = ? WHERE id = ?`, failing with `no such column: id` because `items` uses primary key `item_id`.
- **Fix:** Replace `WHERE id = ?` with `WHERE item_id = ?` in `TestTrunkBranchFullLoopWithoutMain` (`trunk_branch_setting_test.go`), then run `just pack-sync`.
- **Test to turn green (`1`):**
  - `TestTrunkBranchFullLoopWithoutMain`

---

## 4. Whether `fixer-main-81cd910659bb` should continue

Under **`D-SBT3`** (*designer's decision from an operator request; the owner may overturn this*):

1. **Do not continue `fixer-main-81cd910659bb` while multiple causes remain red.** In [`tools/tillr-drain/fixer/fixer.go`](../../tools/tillr-drain/fixer/fixer.go) (`TrunkFixerPrefix = "fixer-main-"`) and [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) (`queueOrderSQL`), an open `fixer-main-*` item ranks at `TrunkFixerRank` ahead of ordinary `build` items, absorbs check-overlapping red proofs via `fixerNamedChecks` ([`tools/tillr-drain/drain/fixer_rebase.go`](../../tools/tillr-drain/drain/fixer_rebase.go)), and must pass the entire `go-test-tillr-drain` check named in its `trunk-green` criterion in a single candidate commit. Because `345d995c4c22` and `1827b94da801` landed independent regressions after `81cd910659bb` (alongside `19f56bb872f0` and `5ff3da946501`), any single-cause candidate on `fixer-main-81cd910659bb` is guaranteed to fail `trunk-green` on the remaining `15` causes while starving the per-cause `build` items behind it.
2. **Recommended action — close `fixer-main-81cd910659bb` now and drain `C01`–`C16` as ordinary `build` items:** Closing `fixer-main-81cd910659bb` (`tillr item close --item fixer-main-81cd910659bb --by operator --reason "split into 16 per-cause build items C01-C16"`) lets `C01` through `C16` dispatch and land one cause per commit under `go-affected` (`DeferredProofPackages` defers full `tools/tillr-drain/drain` to after-merge proof on ordinary `build` items), so each landed fix monotonically shrinks the failing set until the after-merge proof turns `refs/tillr/green` green.
3. **If kept for a single cause at the end, run it only on `C13` (`gateland.ReadGatedSeqs`):** If the operator prefers to retain `fixer-main-81cd910659bb` rather than closing it, hold it parked until `C01`–`C12` and `C14`–`C16` have landed, then release `fixer-main-81cd910659bb` **solely for `C13`** (`TestEveryFoldReadsTheKindsItSelects` — a `1`-test landed regression introduced by `81cd910659bb` itself in `tools/tillr-drain/internal/gateland/coordinator.go`), at which point one narrow commit on `fixer-main-81cd910659bb` can turn the full `go-test-tillr-drain` check green.

---

## 5. Ordered per-cause build item filings

A lane worker on `scouted-design@v1` is forbidden by the run boundary from mutating `.tillr/state.db` outside its worktree, Criterion `c3` forbids HTML item declaration comments, and companion-plan `## Tasks` sync is unbuilt until Slice 2 of [the queue mirrors the designs](./tillr-queue-mirrors-designs.md) (`rg 'planCompanionTasks' tools/tillr-item/` returns nothing). The script below closes `fixer-main-81cd910659bb` and files **one `build` item per cause (`C01` through `C16`)** in exact descending failure-count order (`11, 10, 8, 7, 5, 4, 3, 2, 2, 2, 2, 1, 1, 1, 1, 1`), respecting `MaxRequestCriteria = 8` (`4` fixed criteria + at most `4` `--criterion` flags per request in [`tools/tillr-item/item/request.go`](../../tools/tillr-item/item/request.go)), naming every test each item must turn green in its `--criterion` flags, sequencing `C03` and `C04` after `C01` with `--after-item`, and using `--dedup-key` so re-running the script is idempotent:

```sh
#!/bin/sh
set -eu

STORE_DB="${STORE_DB:-.tillr/state.db}"
REPO_ROOT="${REPO_ROOT:-.}"

# Stop monolithic fixer-main-81cd910659bb so per-cause build items are not preempted.
tillr item close \
  --store "$STORE_DB" \
  --item fixer-main-81cd910659bb \
  --by operator \
  --reason "split into 16 per-cause build items C01-C16 (docs/design/split-broken-trunk-drain-repair.md)" || true

# C01 (11 tests: 8 in 81bf66eb + 3 at tip) — Kind 1, introduced by 1827b94da801
C01_JSON=$(tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c01" \
  --title "fix(tillr-drain): refresh TrunkCache after returned squash conflicts and landed design amendments" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestAConflictFixCarryingTheLoopsRetirementIsNotTheWorkersEdit|TestARepeatedSquashConflictGoesToItsMoverInsteadOfAnotherRun|TestASquashConflictStagesOneConflictFixRunWithTheMarkersInItsBrief|TestConflictRepairReappliesWorkerHeadThenRegeneratesRetirement)$' passes" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestConflictWithToolsAndMirrorHandsOnlyToolsPathToWorkerAndRegeneratesMirror|TestItemWorktreesConflictFixDispatchedToItemDirectory|TestSeatDefaultConflictFixRunsOneNode|TestSquashConflictRestagesOnTheTrunkTipWhenTheReplayConflicts)$' passes" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestNonLandingPreservesCriteriaAndAllowsSync|TestDesignAmendmentLandsThenDispatchesOriginalSeat|TestAnIdenticalRepeatDesignAmendmentDispatchesOriginalSeat)$' passes" \
  --criterion "just pack-sync leaves yolo-packs/tillr/files/local/share/tillr/tillr-drain clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C01 in docs/design/split-broken-trunk-drain-repair.md (introduced by 1827b94da801):
refresh l.actors.SetCachedTrunk (and signalTrunkProofWake) from the newly observed trunk SHA inside conflictFix's coordinator return path (tools/tillr-drain/drain/conflict.go) and after settleDesignAnswer (tools/tillr-drain/drain/blocked.go) advances TrunkRef, then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF
)
C01_ID=$(printf '%s\n' "$C01_JSON" | sed -n 's/.*"item_id":"\([^"]*\)".*/\1/p' | head -n 1)

# C02 (10 tests at tip) — Kind 2, introduced by 19f56bb872f0
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c02" \
  --title "fix(tillr-drain): isolate .requests staging under LaneRoot and fix testRemoteSyncLandingEvidence" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestRemoteSyncLandingSettlesRealItemEvidence|TestAGateFailureRedOnTheBaseAcquitsTheItem|TestBaseReplayEventsAreRegisteredAndRecordedThroughDrain|TestGateRefusalUsesExactTrunkProof|TestLoopHoldsSpentWindowAcrossRestartAndReopens)$' passes and leaves no tools/tillr-drain/drain/.requests directory" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestLoopUsesBriefTemplateFromSettings|TestMaxAgentsFlagAndScaleAlias|TestSiblingWriteConvictsOnlyItsRun|TestTrunkBranchMainCheckoutOnOtherBranch|TestWhyAfterRestartOverLiveEvaluator|TestWorkflowExitCandidatePolicies)$' passes" \
  --criterion "ProcessSteps.Stage and stageAssignedRun refuse an empty LaneRoot instead of writing .requests into the process working directory, covered by a unit test, and just pack-sync is clean" <<'EOF'
Fix Cause C02 in docs/design/split-broken-trunk-drain-repair.md (introduced by 19f56bb872f0):
set LaneRoot: p.l.cfg.LaneRoot in testRemoteSyncLandingEvidence (tools/tillr-drain/drain/remote_sync_landing_test.go) and refuse an empty LaneRoot in ProcessSteps.Stage (tools/tillr-drain/drain/steps.go) and stageAssignedRun (tools/tillr-drain/drain/start_stage.go), then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C03 (8 tests: 6 in 81bf66eb + 2 at tip) — Kind 3, introduced by 81cd910659bb
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --after-item "$C01_ID" \
  --dedup-key "trunk-drain-20261127:c03" \
  --title "test(tillr-drain): update sibling conflict-repair tests for returned-conflict resume contract" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestConflictFixEndedAtRestartResumesConflictRepairNotFirstNode|TestConflictFixFailureAndRetryHaveTheirOwnAttempts|TestConflictFixRunSurvivesInPlaceLoopRestart|TestConflictReturnsToWorkerThreeTimesOnItsBranch)$' passes" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestInterruptedRecoveredRepairSettlesExistingCommitWithoutColdRestart|TestWorkflowSecondIdenticalConflictRestartsFromFirstStageAndThirdParksForOwner|TestRepairStagingFailureSurvivesRestartWithoutCancelingSiblings|TestItemWorktreesRepairStagingFailureSurvivesRestartWithoutCancelingSiblings)$' passes" \
  --criterion "just pack-sync is clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C03 in docs/design/split-broken-trunk-drain-repair.md (stale expectations after 81cd910659bb):
update the 8 sibling conflict-repair tests (conflict_fix_restart_test.go, conflict_identity_test.go, landing_session_returns_test.go, conflict_recovery_restart_test.go, conflict_restart_e2e_test.go, conflict_hold_test.go, item_worktrees_restart_ext_test.go) to assert the returned-conflict resume contract (Returned.Cause == RefusalConflict, ConflictFixes == 0, trunk-reset checkout on RefusalConflict, and stage-failure holds on returned conflict runs), then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C04 (7 tests: 5 in 81bf66eb + 2 at tip) — Kind 3, introduced by 81cd910659bb & 345d995c4c22
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --after-item "$C01_ID" \
  --dedup-key "trunk-drain-20261127:c04" \
  --title "test(tillr-drain): align loop_test.go LoopResult.Ticks assertions with standalone coordinator generations" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestACandidateFailureDoesNotStopTheLoopAndDoesNotFailOver|TestTheLoopHasNoTickBudget|TestTheLoopMeasuresWhatEveryRunSpentAndRunsOn|TestTheLoopPrintsOneSpendLinePerRunAndComparesNoCeiling)$' passes" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestTwoConsecutiveTicksDispatchAndRecord|TestALandedCandidateMovesItsItemToLanded|TestADrainThatRefusedEveryCandidateNamesItAsARepeatedRefusal)$' passes" \
  --criterion "just pack-sync is clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C04 in docs/design/split-broken-trunk-drain-repair.md (stale LoopResult.Ticks expectations after 81cd910659bb and 345d995c4c22):
update the 7 tests in tools/tillr-drain/drain/loop_test.go to assert generation monotonicity, FirstGeneration/LastGeneration bounds, and landed/refused outcomes under standalone stage, gate, and land coordinator generations, then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C05 (5 tests: 4 in 81bf66eb + 1 at tip) — Kind 1, introduced by 1827b94da801
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c05" \
  --title "fix(tillr-drain): restore non-landing git skip, pre-sync proof start, triage wake dispatch, and sync cache reuse" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestAlertTriageConcurrencyCapWhileBuildLanesBusy|TestClaimSettledJudgeSkipsFinishedDesignDocsAndReusesSavedQuestions|TestDocumentSyncAndQuestionsAreReusedUntilTrunkMoves|TestPassStartsAfterMergeProofBeforeASyncFault|TestNonLandingLoopStartsWithoutGit)$' passes" \
  --criterion "just pack-sync is clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C05 in docs/design/split-broken-trunk-drain-repair.md (introduced by 1827b94da801):
in tools/tillr-drain/drain/pass.go and tools/tillr-drain/drain/settle_judge.go, guard git/sync seeding when l.nonLanding is true, start after-merge trunk proof before returning a sync fault, invoke dispatchAlertTriage (or wake the GC actor) when consumeTriageWake fires, and preserve syncedTrunkSHA and saved questions across unchanged trunk SHAs, then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C06 (4 tests in 81bf66eb) — Kind 1, introduced by 345d995c4c22
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c06" \
  --title "fix(tillr-drain): stage and launch restarted sibling parallel readers when nodes[0] already finished" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestParallelReadersHoldThenRestartOnlyFailedHarness|TestParallelReadersOrphanedUnjudgedReaderRestartsOnlyIt|TestParallelReadersRestartOnlyInterruptedNodes|TestSeatSharesParallelRestartWaitsForClosedSibling)$' passes" \
  --criterion "just pack-sync is clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C06 in docs/design/split-broken-trunk-drain-repair.md (introduced by 345d995c4c22):
in tools/tillr-drain/drain/start_stage.go (findStageableRuns and stageAssignedRun), check stagelaunch.RunRecordExists across parallelNodes(d) rather than d.RunRequest.RunID alone so restarted parallel sibling readers stage and launch when nodes[0] already has a terminal runs row, then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C07 (3 tests in 81bf66eb) — Kind 1, introduced by 1827b94da801
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c07" \
  --title "fix(tillr-drain): record PassTiming, applied pass, and generation bounds when reaping dead runs" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestReapingWithoutDispatchIsAnAppliedPassBeforeQueueDrained|TestSeparateLeaseExpiriesEachRecordTheirGenerationTiming|TestTwoLoopsInOneSecondKeepBothGenerationReports)$' passes" \
  --criterion "just pack-sync is clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C07 in docs/design/split-broken-trunk-drain-repair.md (introduced by 1827b94da801):
when reapDeadAndExpired allocates a generation in runGCStep and direct passPlansMode (tools/tillr-drain/drain/pass.go and worktree_collect.go), record PassTiming, countGeneration(result, generation) for FirstGeneration/LastGeneration, and applied-pass progression before queue-drained, then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C08 (2 tests in 81bf66eb) — Kind 1, introduced by 81cd910659bb
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c08" \
  --title "fix(tillr-drain): release lane lease and examination reservation when an item faults" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestItemFaultHoldsOnlyItsItem|TestRestartGatesAndLandsTheRetiredHead)$' passes" \
  --criterion "just pack-sync is clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C08 in docs/design/split-broken-trunk-drain-repair.md (introduced by 81cd910659bb):
in tools/tillr-drain/drain/settle_judge.go (finishCandidateOutcome and coordinator gate-error settlement), release the lane lease and examination reservation whenever an item holds on settle-failed or gate-harness-error without stopLaneRetained, then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C09 (2 tests: 1 in 81bf66eb + 1 at tip) — Kind 1, introduced by 345d995c4c22
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c09" \
  --title "fix(tillr-drain): route coordinator fallback polls through triggerTimer and complete direct worker-exit handoff" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestDrainNotifyAndNoNotifyParityUnderSteppedClock|TestCompletionHandsOffWorkflowWithoutAnotherFullPass)$' passes" \
  --criterion "just pack-sync is clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C09 in docs/design/split-broken-trunk-drain-repair.md (introduced by 345d995c4c22):
drive standalone coordinator fallback timers through l.triggerTimer (and signal coordinator wakes on in-process state transitions), and let direct waitForTrigger completion handoff settle and dispatch the next workflow node when standalone coordinators are inactive, then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C10 (2 tests in 81bf66eb) — Kind 3, introduced by 81cd910659bb
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c10" \
  --title "test(tillr-drain): update examination and worker-gate tests for candidate_gate and post_gate sub-phases" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestExaminationRecordsSubPhasesAndAccountsForWallTime|TestWorkerEndStartsGateWithoutPassOrSiblingLanding)$' passes" \
  --criterion "just pack-sync is clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C10 in docs/design/split-broken-trunk-drain-repair.md (stale expectations after 81cd910659bb):
update tools/tillr-drain/drain/handoff_test.go and tools/tillr-drain/drain/worker_gate_test.go to assert candidate_gate and post_gate instead of deleted claim_settled_judge and claim_judge_plan sub-phases, then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C11 (2 tests at tip) — Kind 3, introduced by 1827b94da801
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c11" \
  --title "test(tillr-drain): exercise runTrunkProofStep in direct pass trunk-observation unit tests" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^(TestPassRecordsSharedCoreWorktreeWithoutChangingConfig|TestStopMidPassWhileUpdateRefHoldsLockLeavesNextLoopClean)$' passes" \
  --criterion "just pack-sync is clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C11 in docs/design/split-broken-trunk-drain-repair.md (stale direct l.pass() unit test hooks after 1827b94da801):
update tools/tillr-drain/drain/trunk_worktree_test.go and tools/tillr-drain/drain/stop_prompt_test.go to invoke the trunk-observation / runTrunkProofStep step alongside l.pass(), then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C12 (1 test in 81bf66eb) — Kind 1, introduced by 1827b94da801
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c12" \
  --title "fix(tillr-drain): call RecordBackendOutcomes on terminal failure in examineClaimed" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^TestAnInfrastructureFailureHoldsAndRepeatsOnlyOnTheBuildBackend$' passes" \
  --criterion "just pack-sync is clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C12 in docs/design/split-broken-trunk-drain-repair.md (introduced by 1827b94da801):
call RecordBackendOutcomes(ctx, l.cfg.Store) after recording terminal failure in examineClaimed (tools/tillr-drain/drain/pass.go), matching settleWorker, then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C13 (1 test in 81bf66eb) — Kind 1, introduced by 81cd910659bb
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c13" \
  --title "fix(tillr-drain): check event kind in gateland.ReadGatedSeqs fold" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^TestEveryFoldReadsTheKindsItSelects$' passes" \
  --criterion "just pack-sync is clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C13 in docs/design/split-broken-trunk-drain-repair.md (introduced by 81cd910659bb):
in tools/tillr-drain/internal/gateland/coordinator.go (ReadGatedSeqs), check e.Kind == kind (or select and switch on the concrete CandidateGatedKind constant) so TestEveryFoldReadsTheKindsItSelects passes, then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C14 (1 test at tip) — Kind 1, introduced by 81cd910659bb
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c14" \
  --title "fix(tillr-drain): sample Now before opening WriteTx in claimSettledCandidateDirect" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^TestPendingRestartAnsweredDuringSlowExamination$' passes" \
  --criterion "just pack-sync is clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C14 in docs/design/split-broken-trunk-drain-repair.md (introduced by 81cd910659bb):
sample l.cfg.Now() before opening s.WriteTx in claimSettledCandidateDirect (tools/tillr-drain/drain/settle_judge.go) so a blocking Now hook never holds the SQLite write lock against AnswerPendingRestart, then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C15 (1 test in 81bf66eb) — Kind 2, synthetic clock in 74afc09f overtaken under load after 81cd910659bb
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c15" \
  --title "test(tillr-drain): derive recovery timestamp from monotonic Now clock in TestEveryNonLandingOutcomeRecovers" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^TestEveryNonLandingOutcomeRecovers$' passes" \
  --criterion "TestEveryNonLandingOutcomeRecovers samples recoveryAt from its monotonic now() clock after first.Run instead of hardcoding 2026-11-12T00:00:30Z, with no retry, longer timeout, or skipped test, and just pack-sync is clean" <<'EOF'
Fix Cause C15 in docs/design/split-broken-trunk-drain-repair.md (load-sensitive synthetic clock in TestEveryNonLandingOutcomeRecovers, tools/tillr-drain/drain/loop_test.go):
sample recoveryAt := now().UTC().Format(time.RFC3339) after first.Run(context.Background()) completes instead of hardcoding "2026-11-12T00:00:30Z" for apply, authorizeRetryWith, and tick, then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF

# C16 (1 test at tip) — Kind 3, introduced by 5ff3da946501
tillr item request --store "$STORE_DB" --root "$REPO_ROOT" \
  --seat build --ordinary --to last --by operator \
  --dedup-key "trunk-drain-20261127:c16" \
  --title "test(tillr-drain): query WHERE item_id in TestTrunkBranchFullLoopWithoutMain ItemState stub" \
  --criterion "cd tools/tillr-drain && go test -count=1 ./drain -run '^TestTrunkBranchFullLoopWithoutMain$' passes" \
  --criterion "just pack-sync is clean with no retry, longer timeout, or skipped test" <<'EOF'
Fix Cause C16 in docs/design/split-broken-trunk-drain-repair.md (introduced by 5ff3da946501):
change UPDATE items SET state = ? WHERE id = ? to WHERE item_id = ? in TestTrunkBranchFullLoopWithoutMain (tools/tillr-drain/drain/trunk_branch_setting_test.go), then run just pack-sync. Do not add retries, raise time limits, or skip tests.
EOF
```

---

## 6. Decision Ledger

| ID | Decision | Date | Where | Owner ruling? |
|---|---|---|---|---|
| `D-SBT1` | **Designer's decision (from an operator request); the owner may overturn this.** Partition all `61` failing `tools/tillr-drain/drain` tests at `83580e31504c` (`38` named proof failures at `81bf66eb` plus `23` additional full-suite failures at `83580e31504c`) into `16` pure-kind causes (`9` landed code regressions covering `30` tests, `2` load or neighbor-interference causes covering `11` tests, and `5` stale-expectation or broken-stub causes covering `20` tests) so no build item mixes production bugfixes with stale-assertion updates or test-isolation fixes. | 2026-11-27 | [§1](#1-suite-execution-at-83580e31504c-and-three-way-separation), [§2](#2-complete-assignment-of-all-38-named-failures-and-23-tip-extras) | No |
| `D-SBT2` | **Designer's decision (from an operator request); the owner may overturn this.** Order the `16` per-cause `build` items (`C01` through `C16`) strictly from most failing tests (`11`) to fewest (`1`), breaking ties by count in the `81bf66eb` proof list first and then by kind (`Kind 1` before `Kind 2` before `Kind 3`), with `--after-item` sequencing `C03` and `C04` behind `C01`. | 2026-11-27 | [§3](#3-causes-ordered-from-most-failing-tests-to-fewest), [§5](#5-ordered-per-cause-build-item-filings) | No |
| `D-SBT3` | **Designer's decision (from an operator request); the owner may overturn this.** Do not continue monolithic `fixer-main-81cd910659bb` while multiple causes remain red; close it in favor of `C01`–`C16` (or hold it parked until every other cause has landed and run it solely on `C13`, `81cd910659bb`'s single-test `gateland.ReadGatedSeqs` regression). | 2026-11-27 | [§4](#4-whether-fixer-main-81cd910659bb-should-continue) | No |
| `D-SBT4` | **Designer's decision (from an operator request); the owner may overturn this.** Fix both Kind-2 causes (`C02` empty-`LaneRoot` `.requests/` CWD pollution and `C15` `TestEveryNonLandingOutcomeRecovers` hardcoded `30 s` synthetic recovery timestamp) in code and test clock derivation with zero retries, zero timeout increases, and zero skipped tests, and sync `yolo-packs/tillr/files/local/share/tillr/tillr-drain` via `just pack-sync` in every commit. | 2026-11-27 | [§3.2](#32-c02-empty-laneroot-in-testremotesynclandingevidence-pollutes-package-requests), [§3.15](#315-c15-testeverynonlandingoutcomerecovers-hardcodes-synthetic-recovery-timestamp), [§5](#5-ordered-per-cause-build-item-filings) | No |
