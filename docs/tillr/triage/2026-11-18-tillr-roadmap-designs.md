---
title: "Triage of the 43 tillr roadmap designs, 2026-11-18"
status: accepted
stage: CURRENT
tags: [tillr, triage, design, status, report]
summary: "A one-time triage of the 43 design documents the tillr roadmap links to. Each was read by a read-only scout against the tree and given a verdict, a recommended action and its evidence. 5 are current, 34 are stale and 4 are obsolete. The report recommends only: no triaged document was edited and no recommendation was acted on."
---

# Triage of the 43 tillr roadmap designs, 2026-11-18

> **History note, 2026-11-21.** This is a dated record. It names `docs/design/agent-runtime.md` as that file stood when the record was written; the file
> was deleted on 2026-11-21, and [the retirement record](../../design/agent-runtime-retirement.md) says what became of it.

This report triages the 43 design documents that the tillr roadmap links to. One read-only scout
read each document and checked it against the repository. The 43 findings were written on
2026-11-18, between 19:31 and 22:20 by the host clock (the modification times `ls -l` prints for
the finding files), and this report was assembled from them on 2026-11-19 on the tree at
`502b099f6`. Each finding answers five things: whether the document is still relevant, whether
what it says is built or true matches the tree, whether its status line is true, whether each open
question is still open, and which one action it recommends.

The report changes nothing else. No triaged document was edited, and no recommended action was
carried out; each action below is a recommendation for a later change.

## How to read the table

The verdict is one of three words:

- **current**: the document is still relevant and the finding found nothing inaccurate in it.
- **stale**: the document is still relevant, and the finding names at least one statement, status
  line or open question in it that no longer matches the tree.
- **obsolete**: the document is no longer a design to build from. Its finding calls it superseded
  or historical; it may still be worth keeping as a record, and the action says whether its text
  needs a correction.

The verdicts and actions are the findings' own. The table counts 5 current, 34 stale
and 4 obsolete documents. Every recommended action is either *keep as is* or *correct
facts*; no finding recommended marking a document built, superseding it, retiring it or merging
it. A stale verdict on a document whose stage is `BUILT` means its build status is right and its
body is not: seven built designs still describe, in the present tense, the tree from before they
were built.

Where a finding says it could not establish something, the row says so in its final clause and
gives no verdict on that point. The number in the first column links to the document's detail.

## The 43 documents

| # | Document | Verdict | Recommended action | Evidence |
|---|---|---|---|---|
| [1](#1-tillr-queue-mirrors-designs) | [`docs/design/tillr-queue-mirrors-designs.md`](../../design/tillr-queue-mirrors-designs.md) | current | keep as is | Sync still only warns about a linked design that has no item (`designWithoutItems` in `tools/tillr-item/item/design_without_item.go`, pinned by `TestSyncDesignWithoutItem`), and `docs/tillr/roadmap.md` still lists [OQ-QM1](../../design/tillr-queue-mirrors-designs.md#OQ-QM1) as the ruling to answer first. |
| [2](#2-tillr-diff-review) | [`docs/design/tillr-diff-review.md`](../../design/tillr-diff-review.md) | stale | correct facts | The status says “none of it is built”, but the opt-in judge path the design reuses exists and runs before the gate (`tools/tillr-drain/drain/judge.go`, `TestJudgeRouteBeforeGate`); the review of every code landing is still unbuilt and [OQ-DR1](../../design/tillr-diff-review.md#OQ-DR1) and [OQ-DR2](../../design/tillr-diff-review.md#OQ-DR2) have empty answers. |
| [3](#3-criterion-repair) | [`docs/design/criterion-repair.md`](../../design/criterion-repair.md) | current | keep as is | The freeze is tested (`TestAFrozenCriterionsTextCannotBeRewritten`), `rg -n -i 'criterion[_ -]supersession\|supersedecriterion\|criterion.*supersed' tools/tillr-item` printed no matches, and `docs/tillr/roadmap.md` still lists [OQ-CR7](../../design/criterion-repair.md#OQ-CR7) as waiting on the owner. |
| [4](#4-agent-context-delivery) | [`docs/design/agent-context-delivery.md`](../../design/agent-context-delivery.md) | stale | correct facts | Sections 1 to 5 describe `tools/board-cli` code as present although the document's own 2026-11-18 note says it was deleted, and its “2 remedy / 3 round caps” were removed by `D-JL10` in `docs/design/tillr-judge-and-lifecycle.md`; the brief now comes from `tools/tillr-run/backends/brief-template.md`. |
| [5](#5-tillr-judge-and-lifecycle) | [`docs/design/tillr-judge-and-lifecycle.md`](../../design/tillr-judge-and-lifecycle.md) | current | keep as is | `tillr item show` printed `"state":"in-queue"` for each of the three items the document names as queued, and the partly built finished-state receipt matches `tools/tillr-item/item/finished_receipt.go` and `TestFinishedStateReceipt`. |
| [6](#6-tillr-autoqa) | [`docs/design/tillr-autoqa.md`](../../design/tillr-autoqa.md) | stale | correct facts | The proposal is iceboxed in `docs/tillr/roadmap.md` and `tools/autoqa` is absent, but five of its claims (store bootstrap, `tillr item apply` intake, the `derived_from` child, the `role` stage key, “graph template” and “leaf”) are contradicted by “Where the documents disagree” in `docs/design/tillr-separable-parts.md`. |
| [7](#7-tillr-selfhosted-build) | [`docs/design/tillr-selfhosted-build.md`](../../design/tillr-selfhosted-build.md) | stale | correct facts | It calls sibling resolution wholly unbuilt although `tools/tillr-view/siblings.go` resolves the planner (`TestPlannerResolutionPrefersTheSiblingOverPATH`), and it counts twelve pack modules where `.justfile` and `tests/pack-sync.test.py` name eleven; the main-tree launcher is still missing and [OQ-SH2](../../design/tillr-selfhosted-build.md#OQ-SH2) is still open. |
| [8](#8-tillr-graph-is-the-path) | [`docs/design/tillr-graph-is-the-path.md`](../../design/tillr-graph-is-the-path.md) | stale | correct facts | Its status says every item still runs as one implicit single worker, which `config/tillr/workflows/` (six files) and `TestWorkflowRunsTwoNodesAndLandsOnce` contradict; the whole-life model is unbuilt and [OQ-GP7](../../design/tillr-graph-is-the-path.md#OQ-GP7) has an empty answer. |
| [9](#9-tillr-storage) | [`docs/design/tillr-storage.md`](../../design/tillr-storage.md) | stale | correct facts | It says the fault of moving the ref inside an open store write is “being fixed now”, but `TestFinalMoveHoldsNoStoreWriteAndTheClaimRefusesClose` in `tools/tillr-land/contention_test.go` shows it fixed, while the proposed store is unbuilt, [OQ-TS1](../../design/tillr-storage.md#OQ-TS1) and [OQ-TS3](../../design/tillr-storage.md#OQ-TS3) stay blocked, and the finding did not rerun the 2026-11-17 storage trials. |
| [10](#10-tillr-early-design-landing) | [`docs/design/tillr-early-design-landing.md`](../../design/tillr-early-design-landing.md) | stale | correct facts | The frontmatter `summary` and `next` still treat [OQ-EL1](../../design/tillr-early-design-landing.md#OQ-EL1) as open and name only D-EL3 to D-EL7 for review, against the document's own ledger row recording the owner's 2026-11-18 “always” ruling; a scoped `rg` over `tools/tillr-*` found no early-landing code. |
| [11](#11-tillr-vocabulary) | [`docs/design/tillr-vocabulary.md`](../../design/tillr-vocabulary.md) | stale | correct facts | The frontmatter summary says three questions remain although the document's ledger records [OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4) ruled as “charter” on 2026-11-18, and “Nothing here is built” is contradicted by `tools/tillr-drain/drain/landing_call_lifecycle.go` and `TestLandingCallClosesOnProvenSupersededOrWithdrawn`. |
| [12](#12-tillr-landing-call) | [`docs/design/tillr-landing-call.md`](../../design/tillr-landing-call.md) | stale | correct facts | The `D-LC5` ledger row says nothing is built, but closing a call on proof and answering a revert are implemented (`landing_call_lifecycle.go`, `TestLandingCallClosesOnProvenSupersededOrWithdrawn`, `TestLandingCallAnswersJudgeAndRevertFromTheirDurableRecords`); the `land` tool is unshipped and [OQ-LC1](../../design/tillr-landing-call.md#OQ-LC1) is unanswered. |
| [13](#13-tillr-separable-parts) | [`docs/design/tillr-separable-parts.md`](../../design/tillr-separable-parts.md) | stale | correct facts | Section 7.1's R2-11 row says the run wall is unbuilt and in no plan, but `config/tillr/backend-policy.json` has no `timeout_seconds` and `TestWallTimeoutDefaultIsUnbounded` in `tools/tillr-run/run/wall_default_test.go` pins the unbounded default. |
| [14](#14-tillr-product-qa-requests) | [`docs/design/tillr-product-qa-requests.md`](../../design/tillr-product-qa-requests.md) | stale | correct facts | Its frontmatter says `status: draft`, `stage: DESIGN` and a `next` that answers R2-1 to R2-6 first, while `docs/tillr/design-status.md` classes it “Received; answered” and Section 7 of `docs/design/tillr-separable-parts.md` answers R2-1 to R2-14; the finding lacks evidence on the Product QA project's own state outside this repository. |
| [15](#15-tillr-definitions-plan) | [`docs/design/tillr-definitions-plan.md`](../../design/tillr-definitions-plan.md) | stale | correct facts | “The words this plan uses” says the owner has chosen neither name, but `docs/design/tillr-vocabulary.md` records [OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4) ruled as “charter”; the first tranche itself is in the tree (`tools/tillr/definition/landing.json`, `TestNonLandingLoopStartsWithoutGit`). |
| [16](#16-tillr-core-and-git-landing) | [`docs/design/tillr-core-and-git-landing.md`](../../design/tillr-core-and-git-landing.md) | stale | correct facts | Its `next`, “Needs your ruling” and [OQ-CG5](../../design/tillr-core-and-git-landing.md#OQ-CG5) row treat both names as unanswered, but “The owner's rulings” in `docs/design/tillr-vocabulary.md` answers [OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4) as “charter” and leaves only [OQ-VO5](../../design/tillr-vocabulary.md#OQ-VO5) open. |
| [17](#17-tillr-intake-paths) | [`docs/design/tillr-intake-paths.md`](../../design/tillr-intake-paths.md) | stale | correct facts | Its “nothing here is built” account is contradicted by the prose-rewrite hold (`TestProseRewriteHoldAndRelease`), the idea save (`TestIdeasHTTPSavesWordsAndShowsIntake`) and the no-block triage route (`TestDocumentFedPrerequisiteRoutesBeforeSeatAndWorkflow`). |
| [18](#18-tillr-planning-node-model-tiering) | [`docs/design/tillr-planning-node-model-tiering.md`](../../design/tillr-planning-node-model-tiering.md) | stale | correct facts | “Luna has not run at all” and “Every stored item runs as one worker” are contradicted by a 2026-11-19 Luna session and by a store snapshot holding one `doc-triage@v1` selection, while the finding lacks evidence for the live count of `planned-build@v1` landings because its snapshot read 0 without the write-ahead log. |
| [19](#19-model-benchmarks-from-drain-records) | [`docs/design/model-benchmarks-from-drain-records.md`](../../design/model-benchmarks-from-drain-records.md) | current | keep as is | `rg -n -i "task record\|replay root\|reading packet\|replay arm\|OQ-MB\|model benchmark" tools/tillr-*` printed no matches and `docs/tillr/roadmap.md` still lists the replay as wanted with its three questions waiting on the owner; the finding lacks live store evidence to recompute the dated run counts because its worktree has no store. |
| [20](#20-tillr-queue-persistence) | [`docs/reference/tillr-queue-persistence.md`](../../reference/tillr-queue-persistence.md) (graduated reference) | stale | correct facts | `stage: BUILT` was accurate at triage, where the ledger still called [`OQ-QP2`](../../reference/tillr-queue-persistence.md#why-its-this-way) settled although reopened as [`OQ-QM1`](../../design/tillr-queue-mirrors-designs.md#OQ-QM1), said the Git trace stayed on although commit `d09016bba` removed it (`tools/tillr-run/backends/pi/run-pi` unsets `GIT_TRACE2_EVENT`), and claimed a `D-QP4` badge that an `rg` over the item, view and drain tools did not find; graduated to `docs/reference/tillr-queue-persistence.md` on 2026-11-20. |
| [21](#21-tillr-item-graphs) | [`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md) (graduated reference) | stale | correct facts | `stage: BUILT` was accurate at triage, where `D-IG4` still said graphs above depth 8 or 100 nodes are refused against `tools/tillr-drain/drain/workflow_check.go` (“Graph size is reported, never refused”) and Section 7.3 called two amendments owed that were present in `agent-runtime.md` and `tillr-idea-pipeline.md`; graduated to `docs/reference/tillr-item-graphs.md` on 2026-11-20. |
| [22](#22-observatory-redesign) | [`docs/reference/observatory-redesign.md`](../../reference/observatory-redesign.md) (graduated reference) | stale | correct facts | `stage: BUILT` was accurate at triage, where Section 5.8 still said workflows were not built and child sessions were not recorded against `TestWorkflowRunsTwoNodesAndLandsOnce` and `TestProgressPartialResumeAndNestedChildren` and gaps G2, G3, G4 and G8 were closed in the tree; graduated to `docs/reference/observatory-redesign.md` on 2026-11-20. |
| [23](#23-tillr-restart) | [`docs/reference/tillr-restart.md`](../../reference/tillr-restart.md) (graduated reference) | stale | correct facts | `stage: BUILT` was accurate at triage (`TestRestartReplacesTheLoopInPlaceWithoutSignalingARun`), where “Needs your ruling: None” sat beside an owner decision on patch-branch publishing and `next` named a reference not yet under `docs/reference/`; graduated to `docs/reference/tillr-restart.md` on 2026-11-20. |
| [24](#24-tillr-claude-backend) | [`docs/design/tillr-claude-backend.md`](../../design/tillr-claude-backend.md) | stale | correct facts | The status paragraph says no loop on the real store has dispatched to this backend, but “Route design work past a design seat that is out of quota” in `docs/tillr/roadmap.md` records thirteen `run.finish` records on `claude-designer`; the adapter and quota admission are built and tested. |
| [25](#25-gate-rework) | [`docs/design/gate-rework.md`](../../design/gate-rework.md) | stale | correct facts | Section 6.6 gives a 60-minute red cap and a `green` lane base where `config/tillr/gate-policy.json` says `minutes: 0`, `lane_base: "tip"` and `auto_revert: true`, and Section 7 describes a carried check that `tools/tillr-gate/run.go` refuses. |
| [26](#26-observatory-actions) | [`docs/reference/observatory-actions.md`](../../reference/observatory-actions.md) (graduated reference) | stale | correct facts | `stage: BUILT` was accurate at triage, where Section 2 (“What exists today”) still said the server registered only `GET` routes and the item tool had nine verbs (`tools/tillr-view/serve.go` registers two `POST` routes and `tools/tillr-item/main.go` dispatches `place` and `idea`); graduated to `docs/reference/observatory-actions.md` on 2026-11-20. |
| [27](#27-tillr-drain-successor) | [`docs/reference/tillr-drain-successor.md`](../../reference/tillr-drain-successor.md) (graduated reference) | stale | correct facts | `stage: BUILT` was accurate at triage, where the decision ledger opened by saying nothing in the document was built while marking D-DS1 to D-DS16 built, and the serial-judge and deleted-`stamp` claims were contradicted by `Loop.Verify` in `tools/tillr-drain/drain/loop.go` and by `stamp` in `tools/tillr-drain/drain/plan.go`; graduated to `docs/reference/tillr-drain-successor.md` on 2026-11-20. |
| [28](#28-tillr-board-reshape) | `docs/design/tillr-board-reshape.md` | obsolete | correct facts | `docs/tillr/design-status.md` classes it “Historical: done” and the triage list it says is owed exists in `docs/design/tillr-seed-triage.md`, yet its frontmatter still says `in-review`, `DESIGN` and that the list is owed; the finding lacks evidence on whether the owner approved the list row by row or whether the five question texts were reconciled. |
| [29](#29-tillr-today) | `docs/design/tillr-today.md` | obsolete | keep as is | Its frontmatter already says `stage: SUPERSEDED` and its body “Retired 2026-11-16”, and `docs/tillr/design-status.md` lists it under superseded records; the finding recommends leaving the dated plan untouched. |
| [30](#30-observatory-design) | [`docs/reference/observatory-design.md`](../../reference/observatory-design.md) (graduated reference) | stale | correct facts | The summary at triage listed the pause control, archive, now band, state glyph and refusal component as unbuilt although each existed under `tools/tillr-view/web/src/components/`, and Section 11 argued for `DECIDED` against the frontmatter's `BUILT`; its layout was superseded by [`docs/reference/observatory-redesign.md`](../../reference/observatory-redesign.md) while its provenance rules stayed in force; graduated to `docs/reference/observatory-design.md` on 2026-11-20. |
| [31](#31-mq-retirement) | [`docs/design/mq-retirement.md`](../../design/mq-retirement.md) | stale | correct facts | The frontmatter says `in-review` and `DESIGN` although commit `7af65bf66` deleted the merge queue and `TestOneGatesAndLandsABranch` passed; `.workboard.toml` and the `mq` readers in `tools/pipeline-evidence/sources.py` are still present, so the plan is not wholly carried out. |
| [32](#32-scheduled-tasks) | [`docs/design/scheduled-tasks.md`](../../design/scheduled-tasks.md) | stale | correct facts | Scheduling is unbuilt (`docs/tillr/roadmap.md`, “Older designs”), but the design is grounded in the absent `tools/board-cli`, and its `status` and `next` treat [OQ-ST1](../../design/scheduled-tasks.md#12-open-questions) as open although Section 12 settles it as C; the experiment [OQ-ST3](../../design/scheduled-tasks.md#12-open-questions) asks for has no recorded result. |
| [33](#33-tillr-muse-backend) | [`docs/reference/tillr-muse-backend.md`](../../reference/tillr-muse-backend.md) (graduated reference) | stale | correct facts | At triage, the shared adapter contract was built (`TestTheBackendFieldTravelsFromRequestToRecordAndRow`), Muse was iceboxed (`preference` is `["pi"]` in `config/tillr/backend-policy.json`), and the design's boundary and one-failover rules were contradicted by `TestOrdinaryGitUseDuringARunNeverConvictsIt` and `TestLegacyFailoverBudgetIsIgnored`; graduated and reconciled in `docs/reference/tillr-muse-backend.md` on 2026-11-20. |
| [34](#34-tillr-goal-monitor) | [`docs/design/tillr-goal-monitor.md`](../../design/tillr-goal-monitor.md) | current | keep as is | `docs/tillr/roadmap.md` keeps it under “Icebox”, an `rg` for `due_at\|recur_interval_ms\|not-due` found no scheduling code, `config/tillr/workflows` has no `goal-checkin@v1.json`, and [OQ-GM1](../../design/tillr-goal-monitor.md#OQ-GM1) and [OQ-GM2](../../design/tillr-goal-monitor.md#OQ-GM2) are still unanswered and blocked. |
| [35](#35-escalation-ladder) | [`docs/design/escalation-ladder.md`](../../design/escalation-ladder.md) | stale | correct facts | Sections 4 and 5 of `docs/tillr/decision-provenance-review.md` record the four-rung ladder, policy ledger and fence as unbuilt, while the route that exists sends a blocked report to the design seat and then the owner (`TestBlockedReportRoutesOnceToDesignThenOwner`); Section 1.2 still calls the conflict record an unverified assumption although `tools/tillr-item/item/conflict.go` exists. |
| [36](#36-autonomous-landing) | [`landing-principles.md`](../../design/landing-principles.md) (retired board design) | obsolete | correct facts | Its frontmatter says `stage: SUPERSEDED` by `landing-principles.md`, and `test -d` printed `absent` for `tools/board-cli/` and `tools/mq/`, yet `next` still waits on a ruling for item 225 and a plan against item 130. |
| [37](#37-tillr-designer-sessions) | [`docs/reference/tillr-designer-sessions.md`](../../reference/tillr-designer-sessions.md) (graduated reference) | stale | correct facts | `stage: BUILT` was accurate at triage and three named drain tests passed, where the Cost sentence gave about $0.40 as the premium for an expired session while the document's table gave $1.11 against $0.84 ($0.27); graduated to `docs/reference/tillr-designer-sessions.md` on 2026-11-20 with the live two-comment run still marked UNMEASURED. |
| [38](#38-momentum-principle) | [`docs/design/momentum-principle.md`](../../design/momentum-principle.md) | stale | correct facts | The ruling is still cited by `tools/tillr-drain/drain/loop.go`, but “currently dispatches one run at a time” is contradicted by `TestTwoLanesRunConcurrentlyInOneBatch`, and the failed-candidate transition by `TestAFailedCandidateGoesBackToItsWorkerUntilItRepeats`. |
| [39](#39-tillr-idea-pipeline) | [`docs/design/tillr-idea-pipeline.md`](../../design/tillr-idea-pipeline.md) | stale | correct facts | Sections 1 to 5 describe a `derived_from` graph, a `derived-items.json` manifest and a live lineage view as current, but `tools/tillr-item/item/schema.go` has only `item_edges(item_id, blocked_by)` and the document's own “Slice reconciliation” marks Slice 3 unbuilt. |
| [40](#40-tillr-convergence) | [`docs/design/tillr-convergence.md`](../../design/tillr-convergence.md) | stale | correct facts | [OQ-TC2](../../design/tillr-convergence.md#OQ-TC2) says [OQ-PT1](../../design/tillr-planning-node-model-tiering.md#OQ-PT1) to [OQ-PT8](../../design/tillr-planning-node-model-tiering.md#OQ-PT8) are open where `docs/design/tillr-planning-node-model-tiering.md` leaves only [OQ-PT7](../../design/tillr-planning-node-model-tiering.md#OQ-PT7) and [OQ-PT8](../../design/tillr-planning-node-model-tiering.md#OQ-PT8), and “Needs your ruling: None” sits beside a removed-block decision the D-TC2 row says is owed; the seed retirement is built (`config/tillr/seed-items.json` is absent). |
| [41](#41-landing-principles) | [`docs/design/landing-principles.md`](../../design/landing-principles.md) | stale | correct facts | Section 3 cites `TestSquashWritesOneCommitWithTheItemTrailerAndTheDeployedIdentity`, which does not exist (the tree has `TestSquashWritesOneCommitWithNoItemTrailerAndTheDeployedIdentity`), Section 7 says nothing enforces a rule that `TestWaitingEveryDrainReasonHasARule` covers, and the budgets in Sections 5 and 6 are overturned by `D-JL10`. |
| [42](#42-board-state-machine) | [`landing-principles.md`](../../design/landing-principles.md) (retired board design) | obsolete | correct facts | `docs/design/board-docs-retirement.md` classes it superseded by `landing-principles.md` and `test -e tools/board-cli` printed `tools/board-cli absent`, yet its `next` still says “Build from the plan...” and its status reads as a current build count. |
| [43](#43-seat-profiles) | [`docs/design/seat-profiles.md`](../../design/seat-profiles.md) | stale | correct facts | Section 2.4 maps the CODEX delegate to `openai-codex/gpt-6.1-sol` where `yolo-packs/seat-profiles/files/seat-profiles.json` and `tests/seat-profiles.test.mjs` pin `openai-codex/gpt-6-luna`, and `next` treats the `mode:` tag writer as built although `design-seat.ts` is absent. |

## Detail for each document

Each section below is the finding for one document, in the order of the table. The five
labels are the five things each finding was asked to answer.

### 1. tillr-queue-mirrors-designs

[`docs/design/tillr-queue-mirrors-designs.md`](../../design/tillr-queue-mirrors-designs.md): **current**; recommended action: **keep as is**.

**Relevant**

Still wanted, not built or superseded. The Tillr roadmap's “1. Rulings that are waiting on the owner” section explicitly puts this design first and says none of it is built until [OQ-QM1](../../design/tillr-queue-mirrors-designs.md#OQ-QM1) is answered (`docs/tillr/roadmap.md`). Current sync still records missing design items as warnings rather than creating design items (`tools/tillr-item/item/design_without_item.go`, `designWithoutItems`). The newer `doc-triage@v1` workflow is a one-time report over named/roadmap-linked documents, not a persistent item for each outstanding design (`tools/tillr-item/README.md`, “Filing a document triage”; `config/tillr/workflows/doc-triage@v1.json`), so it does not supersede this proposal.

**Accurate**

The core current-state claim remains accurate: sync can report a linked design without an item but does not create one. `TestSyncDesignWithoutItem` in `tools/tillr-item/item/design_without_item_test.go` asserts that such a warning leaves the item rows unchanged; the focused command `cd tools/tillr-item && go test ./... -run '^TestSyncDesignWithoutItem'` passed for `tools/tillr-item/item` (other packages reported no matching tests). The related details in “What sync does today” are scoped to sync; they should not be read as saying sync is Tillr's only item-intake path, since `tillr-item doc-triage` now creates a store-declared triage item directly (`tools/tillr-item/item/doc_triage.go`, `DocTriage`). The proposal's roadmap-mirroring behavior itself is still absent: the source continues to emit `design-without-item`, and `TestSyncDesignWithoutItem` pins that behavior. I found no other present-day factual contradiction in the proposal; its counts and observations are explicitly dated 2026-11-18 snapshots, not current measurements.

**Status line**

True. The frontmatter says `status: in-review` and `stage: DESIGN`, and the in-document “Status” says this is a proposal awaiting [OQ-QM1](../../design/tillr-queue-mirrors-designs.md#OQ-QM1). The roadmap likewise says none of it is built before the owner answers (“1. Rulings that are waiting on the owner”), while current sync still implements the warning behavior rather than the proposed mirror (`tools/tillr-item/item/design_without_item.go`).

**Open questions**

[OQ-QM1](../../design/tillr-queue-mirrors-designs.md#OQ-QM1) is still open and askable. Its `💬` marker and empty “Answer” remain in “Open Questions”; `docs/design/queue-persistence-ruling.md`, “Shared Item Block Shape,” says the owner objected to the earlier D-RS1 choice on 2026-11-18 and reopened this question. The Tillr roadmap still identifies this as the ruling that must be answered first (“1. Rulings that are waiting on the owner”). The objection rejects the unapproved hidden-comment choice but does not select among the remaining mechanisms in [OQ-QM1](../../design/tillr-queue-mirrors-designs.md#OQ-QM1).

**Action**

keep as is — its persistent queue-mirroring goal remains wanted and unbuilt, while its outstanding owner question is still recorded as open.

### 2. tillr-diff-review

[`docs/design/tillr-diff-review.md`](../../design/tillr-diff-review.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant. The roadmap’s “A review before every code landing” entry keeps this design among rulings waiting on the owner and says no agent had read a change before tillr landed it (`docs/tillr/roadmap.md`, “1. Rulings that are waiting on the owner”). The requested universal, post-gate review is not present: `tools/tillr-drain/drain/judge.go` selects a judge only for `judge_mode == "agent"` or an evaluator, and `tools/tillr-drain/drain/loop.go` calls `judgeCandidate` before the gate. The existing opt-in judge and planned workflow evaluator are substrate, not an implementation of this design’s default review.

**Accurate**

The main implementation distinction is accurate: the judge infrastructure exists, but does not implement this design’s proposed review for every code landing after the gate. `tools/tillr-drain/drain/judge.go` checks `mode == "agent" || c.Evaluator != nil`; `tools/tillr-drain/drain/loop.go` invokes `judgeCandidate` before `cfg.Steps.Gate`; and `tools/tillr-drain/drain/judge_route_test.go`, `TestJudgeRouteBeforeGate`, asserts the opted-in order `worker, judge, gate, landing` and the mechanical order `worker, gate, landing`. The judge still requires a grant for an agent judge/evaluator (`judgeCandidate` and `TestJudgeRouteBeforeGate`'s `no-grant` case), unlike the proposal’s planned default `verification-only` review.

One wording/fact needs correction: the status sentence “A proposal; none of it is built” is too broad if “it” includes the existing judge path the design reuses. That path is built, as Section 4.2 itself acknowledges and the files/test above confirm. The intended universal after-gate behavior remains unbuilt. The file’s detailed distinction should be preserved in its status line rather than describing the proposal and its existing substrate as one all-or-nothing state. Historical counts and “never run” claims are explicitly dated to 2026-11-18; I could not re-check current event counts because this worktree has no `.tillr` store (`ls -la .tillr` printed “No such file or directory”). The tracked roadmap repeats the no-review claim but is not a live event-store measurement.

**Status line**

The stage remains DESIGN and `status: in-review` is supported by the unanswered questions: both [OQ-DR1](../../design/tillr-diff-review.md#OQ-DR1) and [OQ-DR2](../../design/tillr-diff-review.md#OQ-DR2) have empty answer blocks in “Open Questions,” and the ledger says the designer’s decisions await owner review (“Decision Ledger”). The proposal’s main behavior is not built, but the status line should distinguish that from the already-built opt-in judge/evaluator substrate as noted above. `docs/design/tillr-planning-node-model-tiering.md`, “Status,” confirms a `planned-build@v1` workflow with a verify node exists; `config/tillr/workflows/planned-build@v1.json` defines it, and that evaluator is not evidence of a default review for all code landings.

**Open questions**

Both remain open and askable. [OQ-DR1](../../design/tillr-diff-review.md#OQ-DR1)’s answer is empty and still asks which model will review; the code’s `review` seat can be selected through policy, but no policy selection answers whether it should serve every landing. [OQ-DR2](../../design/tillr-diff-review.md#OQ-DR2)’s answer is empty and still asks whether a design agent may dismiss a repeated objection. The design’s “When the reviewer and the worker disagree” section leaves dismissal to this ruling. A search of `docs` for either question id outside this design (the command is below) returned no matches, so no other tracked document records an answer; the local `.tillr` store is absent, so I cannot check for a private-store answer.

```bash
rg -n '[OQ-DR1](../../design/tillr-diff-review.md#OQ-DR1)|[OQ-DR2](../../design/tillr-diff-review.md#OQ-DR2)' docs --glob '!tillr-diff-review.md'
```

**Action**

correct facts — retain the relevant, unbuilt universal-review proposal, but clarify the status wording to separate it from the already-built opt-in judge path and keep the dated no-run measurement explicitly time-bounded.

### 3. criterion-repair

[`docs/design/criterion-repair.md`](../../design/criterion-repair.md): **current**; recommended action: **keep as is**.

**Relevant**

Still wanted, not superseded or iceboxed. The design remains the active roadmap entry: `docs/tillr/roadmap.md` under “Rulings that are waiting on the owner” lists [OQ-CR7](../../design/criterion-repair.md#OQ-CR7) and says the criterion-repair work is not built; the same file’s “Criterion repair” entry in `docs/tillr/design-status.md` describes the remaining supersession record. `docs/design/tillr-judge-and-lifecycle.md` (“A Failed Attempt Is Never a Verdict and Never a Count”) links this design as the missing handling for frozen text, and `docs/design/tillr-planning-node-model-tiering.md` (“When the Planner Cannot Plan”) relies on it for who may change a frozen criterion.

**Accurate**

The material claims match the current source and design records:

- The freeze exists, while repair-by-supersession does not: `tools/tillr-item/item/apply.go` refuses rewrites of presented criterion text; `tools/tillr-item/criterion_state_test.go` has `TestAFrozenCriterionsTextCannotBeRewritten` and `TestAFrozenCriterionCannotBeDropped`; `tools/tillr-item/verdict_test.go` has `TestAVerdictNamingAnotherCriterionHashIsRefused`. The bounded command `rg -n -i 'criterion[_ -]supersession|supersedecriterion|criterion.*supersed' tools/tillr-item` printed no matches and exited 1. The roadmap’s “Criterion repair” entry also says the supersession record is still owed.
- `defective` closes the item and the current derived hold names the owner as mover: `docs/design/tillr-judge-and-lifecycle.md` (“A Failed Attempt Is Never a Verdict and Never a Count”) describes the `defective` verdict and `judge-defective` hold; `tools/tillr-drain/holdrules/holdrules.go` maps `judge-defective` to “owner; reopen the closed item only if its premise is corrected.”
- The proposed sequence of reopening then superseding, and the unbuilt supersession record, are accurately described in `docs/design/criterion-repair.md` (“5.1 An unsatisfiable criterion is reopened, then superseded”) and corroborated by the roadmap’s “Criterion repair” entry. Existing reopen support is covered by `tools/tillr-item/lifecycle_test.go`, `TestCloseAndReopenRestoreEveryStartingState`.
- The document says [OQ-CR7](../../design/criterion-repair.md#OQ-CR7) is unanswered and its answer is empty: that remains true in its “Open Questions” section; `docs/tillr/roadmap.md` still lists [OQ-CR7](../../design/criterion-repair.md#OQ-CR7) as awaiting the owner. The ledger’s [OQ-CR4](../../design/criterion-repair.md#OQ-CR4) is explicitly a designer’s decision, not an owner ruling, and the roadmap’s “Rulings that are waiting on the owner” identifies it as a designer decision the owner may overturn.

No contradictory built/unbuilt statement was found. “Nothing is built” in the status/summary is scoped by the following text to the criterion-repair feature; the freeze itself is expressly identified as existing and is tested.

**Status line**

The frontmatter `status: in-review`, `stage: DESIGN`, and `next` pointing to [OQ-CR7](../../design/criterion-repair.md#OQ-CR7) remain true: the question is still listed on the current roadmap and its answer block remains empty in “Open Questions”; no criterion-supersession implementation was found in `tools/tillr-item`. The document’s dated status distinguishes the existing freeze from the missing repair record, consistent with the implementation tests and `docs/tillr/design-status.md`’s “Criterion repair” entry.

**Open questions**

[OQ-CR7](../../design/criterion-repair.md#OQ-CR7) (“Who may replace a criterion's text after a verdict has frozen it?”) remains open and askable: its answer is empty in the “Open Questions” section, and `docs/tillr/roadmap.md` still asks the owner who may replace the frozen text. [OQ-CR1](../../design/criterion-repair.md#OQ-CR1), [OQ-CR2](../../design/criterion-repair.md#OQ-CR2), [OQ-CR3](../../design/criterion-repair.md#OQ-CR3), [OQ-CR5](../../design/criterion-repair.md#OQ-CR5), and [OQ-CR6](../../design/criterion-repair.md#OQ-CR6) are ledger decisions rather than open questions; the ledger records their settled shapes. [OQ-CR4](../../design/criterion-repair.md#OQ-CR4) is also not an open question: its ledger row records a designer’s decision, marked as subject to owner review, and the roadmap identifies it as such.

**Action**

**keep as is** — the repair design is still wanted, the implementation gap and remaining [OQ-CR7](../../design/criterion-repair.md#OQ-CR7) are accurately reported, and the roadmap and dependent designs continue to rely on it.

### 4. agent-context-delivery

[`docs/design/agent-context-delivery.md`](../../design/agent-context-delivery.md): **stale**; recommended action: **correct facts**.

**Relevant**

The design is still relevant as a rationale for matching instruction scope to delivery scope, and it still owns the unresolved Pi startup-profile question: `docs/pi/roadmap.md` lists I4 as “A `pi --agent <name>` startup profile” and says it is visibly not being built; `docs/tillr/roadmap.md` says the question left the Tillr roadmap and is routed from the Pi roadmap. The durable principle that run-specific prose belongs in a run's brief is reflected in `tools/tillr-run/backends/brief-template.md`. However, most of this document's implementation narrative is about the deleted board drain and is no longer an active Tillr design: `docs/design/agent-context-delivery.md` itself says the board drain and `board-cli` were deleted and names the retired autonomous-landing design as superseded by `landing-principles.md`.

**Accurate**

The 2026-11-18 introductory note is broadly accurate: `tools/tillr-run/backends/brief-template.md` has the ten required sections, `tools/tillr-run/backends/preflight.sh` enforces them, and `TestBriefPreflightRequiresSeats` in `tools/tillr-run/backends_test.go` demonstrates refusal when a section is missing. `tools/tillr-run/run/exec.go` and `TestExecRequestEnvironment` in `tools/tillr-run/run/environment_test.go` support the `TILLR_RUN_ID` / `TILLR_LANE_ID` identity claim. `tools/tillr-run/backends/pi/run-pi` retains `BOARD_DRAIN_BRIEF`, `BOARD_DRAIN_RUN`, and `BOARD_DRAIN_SESSION` as adapter compatibility names while constructing Pi's invocation.

Several concrete claims in the body no longer match the tree:

- Sections 1–5 repeatedly describe `tools/board-cli`, `drain.go`, `design.go`, `resume.go`, `laneRules`, `childBrief`, `designChildBrief`, `drainChildEnv`, and `design-lane.ts` as current implementation. The board CLI and its implementation are described as deleted in the document's own 2026-11-18 note; the current brief path is `tools/tillr-run/backends/brief-template.md`, and the Pi adapter is `tools/tillr-run/backends/pi/run-pi`. The old implementation-level descriptions should be historical, not present-tense evidence.
- D-AC10 and Section 4 propose extracting the lane rules to one canonical Markdown source with a golden test. The current template is generic and does not contain those extracted lane rules; the preflight in `tools/tillr-run/backends/preflight.sh` checks headings and placeholders, not semantic rule content. The criterion is not shown as built by those files.
- Section 4's statement that “2 remedy / 3 round caps” are settled is stale. The document's own [OQ-AC5](../../design/agent-context-delivery.md#OQ-AC5) entry points to `D-JL10` in `docs/design/tillr-judge-and-lifecycle.md`, which records the 2026-11-17 ruling that a run's ending is never counted; the current roadmap at `docs/tillr/roadmap.md` also says [OQ-AC5](../../design/agent-context-delivery.md#OQ-AC5) was closed as stale because the caps were removed.
- Section 3's assertion that the staged brief is the single run-scoped prose delivery is incomplete for resumable Pi execution: `tools/tillr-run/backends/pi/run-pi` selects `TILLR_RESUME_PROMPT_FILE` as the prompt on continuation and explicitly says it replaces the first prompt. The staged brief remains available by path, but a resume also has a continuation prompt.

The design correctly labels D-AC7 as not built in its introductory note: `yolo-packs/matt/pack.json` currently documents `subagents.maxThinking` as a fleet-wide ceiling and says there is no per-agent escape. The general placement principle is still plausible, but the old evidence about the deleted drain cannot establish current Tillr behavior.

**Status line**

`status: draft` and `stage: SKETCH` remain defensible: the document says its D-AC ledger is a designer's recommendation not reviewed by the owner, and `docs/tillr/roadmap.md` lists the owner-review/provenance item while routing [OQ-AC3](../../design/agent-context-delivery.md#OQ-AC3) to the Pi roadmap. Its `next` line is substantially current: the same roadmap says [OQ-AC3](../../design/agent-context-delivery.md#OQ-AC3) is routed from the Pi roadmap's Icebox. The status is therefore not a claim that the old board implementation remains built; the factual problem is the unmarked present-tense implementation narrative in the body.

**Open questions**

[OQ-AC3](../../design/agent-context-delivery.md#OQ-AC3) (“Should `pi` gain a `--agent <name>` startup profile?”) is still open and askable, but deliberately iceboxed rather than a current Tillr dependency: `docs/pi/roadmap.md` lists it as I4 and says it is visibly not being built. No evidence found here that a Section 2.6 trigger fired. [OQ-AC1](../../design/agent-context-delivery.md#OQ-AC1), [OQ-AC2](../../design/agent-context-delivery.md#OQ-AC2), [OQ-AC4](../../design/agent-context-delivery.md#OQ-AC4) and [OQ-AC5](../../design/agent-context-delivery.md#OQ-AC5) are recorded as closed in the design's Decision Ledger; the Tillr roadmap independently reiterates [OQ-AC1](../../design/agent-context-delivery.md#OQ-AC1) and [OQ-AC5](../../design/agent-context-delivery.md#OQ-AC5) as designer decisions and [OQ-AC5](../../design/agent-context-delivery.md#OQ-AC5)'s stale closure. The older prose tying [OQ-AC1](../../design/agent-context-delivery.md#OQ-AC1) to `design-lane.ts` is historical because that extension was deleted, but the question's updated policy—test lane-specific extension behavior rather than add a manifest activation key—remains an askable decision for the owner to overturn. [OQ-AC2](../../design/agent-context-delivery.md#OQ-AC2) is moot with the deleted board command, and [OQ-AC4](../../design/agent-context-delivery.md#OQ-AC4)'s shared-home answer is no longer needed to justify a Tillr mode-scoped briefing because the brief template is already run-specific.

**Action**

correct facts — retain the still-relevant placement principle and the routed [OQ-AC3](../../design/agent-context-delivery.md#OQ-AC3), but rewrite or explicitly mark the board-drain-era implementation claims and stale cap decisions as historical, and distinguish the current template/preflight behavior from the unbuilt D-AC10 canonical-rule-source recommendation.

### 5. tillr-judge-and-lifecycle

[`docs/design/tillr-judge-and-lifecycle.md`](../../design/tillr-judge-and-lifecycle.md): **current**; recommended action: **keep as is**.

**Relevant**

Still relevant; not superseded or iceboxed. The current store still reports the three named work items as `in-queue`: `item-jl-owner-criterion-hold`, `item-jl-terminal-state-receipts`, and `item-design-refusal-resumes-session` (`tillr item show --store /workspace/.tillr/state.db --item <id> --root /workspace` prints `"state":"in-queue"` for each). The drain README's section “A design run that ends without answering” says a fresh design run is still dispatched until `item-design-refusal-resumes-session` is built. The document remains the design basis for these outstanding lifecycle rules.

**Accurate**

I found no current mismatch in the document's built/unbuilt claims. Its three queued blocks match the store records above. The partially built finished-state block is described accurately: `tools/tillr-item/item/state.go` calls `checkStateReceipt`, while `tools/tillr-item/item/finished_receipt.go` validates and records an explicitly supplied pointer; it does not require that pointer when omitted or enforce the block's outstanding landed-state criteria/landing-record preconditions. `tools/tillr-item/finished_receipt_test.go` (`TestFinishedStateReceipt`, `TestFinishedStatePointerUnknown`) covers the implemented receipt behavior. The owner-only hold is not present in the implementation: a search of `tools/tillr-drain`, `tools/tillr-view`, and `tools/tillr-item` for `owner-only-owed` returned no match. The design-session refusal remains unbuilt as the README states; `tools/tillr-drain/drain/design_unanswered.go` still contains `settleUnansweredDesignRuns` and `designReraiseText` for raising the conflict again. The stated count of eleven built and three queued blocks therefore still matches the documented build table and current queue state.

**Status line**

`in-review` / `DESIGN` remains accurate: the design still has queued implementation blocks, and `docs/tillr/design-status.md` continues to list this document as partly built with those three item IDs. The dated 2026-11-18 status snapshot remains consistent with the live store records queried above. The line “No question is open” is consistent with the ledger's distinction between open questions and designer decisions that the owner may optionally overturn; see below.

**Open questions**

Section 6.2's “None” remains accurate as a list of pending questions. [`OQ-JL1`](../../design/tillr-judge-and-lifecycle.md#OQ-JL1) and [`OQ-JL2`](../../design/tillr-judge-and-lifecycle.md#OQ-JL2) are explicitly designer decisions not ruled by the owner, with an option for the owner to overturn them; they are not stated as waiting on a ruling (`**Needs your ruling:** None`). [`OQ-JL3`](../../design/tillr-judge-and-lifecycle.md#OQ-JL3) is explicitly overturned by `D-JL10`. [`OQ-JL4`](../../design/tillr-judge-and-lifecycle.md#OQ-JL4) is explicitly closed as stale by the designer, and its text says no item was filed behind it. Thus none is an unanswered, currently actionable question, though the owner could still choose to revisit the two unoverturned designer decisions.

**Action**

keep as is — its implementation counts and remaining work still match the current tree and store, and its ledger distinguishes pending questions from optional owner review of designer decisions.

### 6. tillr-autoqa

[`docs/design/tillr-autoqa.md`](../../design/tillr-autoqa.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant as a proposal, but explicitly wanted later rather than now: `docs/tillr/roadmap.md` under **Icebox** records the owner's 2026-11-17 request to park tillr's exploratory testing, says it is neither the Product QA pipeline nor abandoned, and says pickup requires admitting `item-design-autoqa-slices` and answering [OQ-AQ2](../../design/tillr-autoqa.md#OQ-AQ2). The standalone implementation remains absent: `tools/autoqa` and `yolo-packs/autoqa` do not exist in the current tree. The separate Product QA design is explicitly distinguished in `docs/design/tillr-separable-parts.md` under **Where the documents disagree**.

**Accurate**

The high-level status that no standalone AutoQA runner/refill/dedup product is built is accurate: `tools/autoqa` and `yolo-packs/autoqa` are absent, and `rg -i 'autoqa|qa-probe|qa-confirm' tools --glob '*.go'` finds only legacy-vocabulary retirement code and tests, not a runner. The design's wording “No AutoQA code exists” should be read in that narrow sense; `tools/tillr-item/item/verdict.go`, `decision.go`, and `autoqa_retirement_test.go` are AutoQA-specific compatibility/retirement code.

The following claims do not match the current design/tree:

- In [§1.1](../../design/tillr-autoqa.md#11-what-already-exists-at-head) **Fresh-store DDL registration**, and its bootstrap rationale in [§2.2](../../design/tillr-autoqa.md#22-two-stores-and-sandboxed-sut-snapshots), it says read-only faces fail until an owning utility opens the store for writing. `docs/design/tillr-separable-parts.md` under **Where the documents disagree**, item 1, says they no longer fail and identifies this as a workaround that is no longer needed.
- In [§2.2](../../design/tillr-autoqa.md#22-two-stores-and-sandboxed-sut-snapshots) **Crossing into the SUT board**, D-AQ2 and [§6](../../design/tillr-autoqa.md#6-decisions-open-questions-and-amendments-owed)'s D-AQ2 specify `tillr item apply` as the intake for confirmed bugs. `docs/design/tillr-separable-parts.md`, item 2 under **Where the documents disagree**, says sync is the only intake and that an item goes to triage for its block; `docs/design/tillr-idea-pipeline.md` [§1](../../design/tillr-idea-pipeline.md#1-item-kinds-states-and-edges) and its 2026-11-18 **Slice reconciliation** likewise describe sync/landed-document blocks and say the typed `derived_from` edge is not built.
- In [§3.1](../../design/tillr-autoqa.md#31-test-idea-items-and-the-two-workflow-funnel), `verify_repro` is said to spawn a `qa-confirm@v1` child linked by `derived_from` from a `no_commit` graph. `docs/design/tillr-separable-parts.md`, item 4 under **Where the documents disagree**, says that edge is not built and child items are written only after a successful `tillr-land pass`, which a `no_commit` graph never reaches. The current idea-pipeline design's **Slice reconciliation** says child blocks are introduced through the landed document instead.
- In [§3.1](../../design/tillr-autoqa.md#31-test-idea-items-and-the-two-workflow-funnel), the `triage_judge` stage is specified with `role: "evaluator"`; item 5 under **Where the documents disagree** in `docs/design/tillr-separable-parts.md` says agent workflow stages name a `seat`, as specified by [`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md#schema-stage-kinds-and-static-expansion).
- The terms “graph template” and “leaf” recur throughout the design. [`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md#the-words) under **The words** explicitly retires those terms in favor of “workflow” and “node”.

The design's statement that the upstream item-graph and judge machinery exists is supported by [`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md) (`stage: CURRENT`). Its [stage kinds](../../reference/tillr-item-graphs.md#schema-stage-kinds-and-static-expansion), [producer-bound artifacts](../../reference/tillr-item-graphs.md#producer-bound-artifacts), and [evaluator routing](../../reference/tillr-item-graphs.md#workflow-evaluator) specify the current agent/command and evaluator contracts. The artifact ledger in [§5.1](../../design/tillr-autoqa.md#51-artifact-disposition-ledger) is also mostly borne out: `yolo-packs/tillr/tasks/autoqa-handoff.md` exists; `yolo-packs/tillr/pack.json` still describes an “AutoQA design-review handoff”; all eight `tools/tillr-*/README.md` contain the cited Known Gap; and `docs/design/tillr-distribution.md` marks [OQ-TD2](../../design/tillr-distribution.md#open-questions) settled as superseded by D-AQ1/D-AQ7, although the old handoff prose remains. D-AQ7 also calls for closing `item-decided-by-autoqa`: the current `config/tillr/seed-items.json` search found no such ID, but I did not inspect the live `.tillr` store, so its current stored state cannot be confirmed here.

**Status line**

Yes. `status: in-review` and `stage: DESIGN` are consistent with the still-open [OQ-AQ2](../../design/tillr-autoqa.md#OQ-AQ2); the prose **Status** says it was iceboxed on 2026-11-17, and `docs/tillr/roadmap.md` under **Icebox** still records that decision and next action. The roadmap also says `item-design-autoqa-slices` is `not-started` and never admitted, matching the design's queue-item statement. “Nothing below is being built” is accurate for the standalone AutoQA proposal, not for the existing general Tillr substrate it references.

**Open questions**

[OQ-AQ2](../../design/tillr-autoqa.md#OQ-AQ2) remains genuinely unanswered and askable when the owner resumes the design; both `docs/design/tillr-autoqa.md` [§6.2](../../design/tillr-autoqa.md#62-open-questions) and `docs/tillr/roadmap.md` under **Icebox** say it is blocked until pickup. [OQ-AQ1](../../design/tillr-autoqa.md#OQ-AQ1) and [OQ-AQ3](../../design/tillr-autoqa.md#OQ-AQ3) are not live questions in the document: its [Decision Ledger](../../design/tillr-autoqa.md#decision-ledger) records each as a designer's decision with a rejected alternative, subject to owner review. [OQ-TD2](../../design/tillr-distribution.md#open-questions) in `docs/design/tillr-distribution.md` is marked settled as superseded by D-AQ1/D-AQ7, not still open, though the design correctly notes that the old prose has not been compacted. A related intake question remains parked elsewhere: `docs/design/tillr-separable-parts.md` [OQ-FQ8](../../design/tillr-separable-parts.md#OQ-FQ8) is explicitly blocked until AutoQA is picked up and asks whether each filed bug is represented by a block or another form; the inaccurate D-AQ2 intake assumption should be reconciled with that question before implementation.

**Action**

correct facts — keep the iceboxed proposal, but correct its stale store bootstrap, SUT intake, child-lineage, evaluator-stage, and workflow-terminology claims before it is picked up.

### 7. tillr-selfhosted-build

[`docs/design/tillr-selfhosted-build.md`](../../design/tillr-selfhosted-build.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant, not superseded: the repository-specific launcher that builds from the main source tree is still missing, and the pack-mirror policy remains undecided. The `.justfile` recipe `tillr-build` instead builds from the commit named by `refs/tillr/green` into a `bin` directory under the jail's durable scratch directory and requires that directory be prepended to `PATH`; `yolo-packs/tillr/programs/tillr/run` sets its source root to `~/.local/share/tillr`. The still-open mirror decision is listed on `docs/tillr/roadmap.md` under “Rulings that are waiting on the owner.” Restart behavior itself is now owned by [`docs/reference/tillr-restart.md`](../../reference/tillr-restart.md).

**Accurate**

Mostly accurate about the central gap and the built restart/question/store behaviors, with these corrections needed:

- Section 2 groups “the source launcher and sibling resolution” as wholly unbuilt. The main-checkout source launcher remains unbuilt (see `.justfile` `tillr-build` and `yolo-packs/tillr/programs/tillr/run` above), but sibling resolution is partly built: `tools/tillr-view/siblings.go` resolves the planner beside the server or by same-digest pack sibling before trying `PATH`, covered by `TestPlannerResolutionPrefersTheSiblingOverPATH` in `tools/tillr-view/planner_internal_test.go`. The full `D-SH1` behavior is not built: `tools/tillr-cli/main.go` still dispatches with `exec.LookPath`, so it does not ensure siblings beside itself or refuse a partial sibling set.
- Section 5 says `just pack-sync` copies twelve tillr modules. The current recipe in `.justfile` names eleven modules, and `tests/pack-sync.test.py`’s `MODULES` tuple also lists eleven (including source-only `tillr-trace` and `tillr-roll`).

The other material status claims match the current tree: [`docs/reference/tillr-restart.md`](../../reference/tillr-restart.md) records the built restart, session-pause, and post-landing roll/push behavior; `tools/tillr-drain/drain/plan.go` implements the Vantage question hold (`questionState`), and `tools/tillr-view/apierror.go` plus `tools/tillr-view/source_internal_test.go` cover unreadable/unsupported store refusal. The distinction between the pack’s source launcher and the missing launcher that selects main-tree source should be made explicit rather than describing the pack launchers as not source launchers at all.

**Status line**

“Partly built” is true: restart and other named behaviors are built, while the launcher that selects main-tree source is not. `docs/tillr/design-status.md` repeats that assessment and identifies [OQ-SH2](../../design/tillr-selfhosted-build.md#OQ-SH2). The source launcher being absent is corroborated by `.justfile` `tillr-build` (an explicit build plus PATH-prepend workflow) and the pack launcher’s `root=~/.local/share/tillr`. Clarify that sibling resolution is partial, as noted above; the section’s combined status currently hides that built slice.

**Open questions**

[OQ-SH2](../../design/tillr-selfhosted-build.md#OQ-SH2) is still open and askable: `docs/tillr/roadmap.md` lists it among owner-waiting decisions, and `docs/design/tillr-convergence.md` says its derived-copy decision waits on [OQ-SH2](../../design/tillr-selfhosted-build.md#OQ-SH2). The alternatives remain meaningful in the current tree: `.justfile` `pack-sync` mirrors source, `tests/pack-sync.test.py` checks parity, and `tools/tillr-run/backends/brief-template.md` still tells workers to run and commit the mirror; deciding automatic regeneration versus publish-time refresh would change that policy. [OQ-SH1](../../design/tillr-selfhosted-build.md#OQ-SH1) and [OQ-SH3](../../design/tillr-selfhosted-build.md#OQ-SH3) are ledger decisions, not additional open questions; [`docs/reference/tillr-restart.md`](../../reference/tillr-restart.md) is accepted and records the restart/roll behavior they describe as built.

**Action**

correct facts — keep this live design because the main-tree launcher and [OQ-SH2](../../design/tillr-selfhosted-build.md#OQ-SH2) remain unresolved, but distinguish the built planner-sibling resolution from the missing full D-SH1 behavior and correct the current pack module count.

### 8. tillr-graph-is-the-path

[`docs/design/tillr-graph-is-the-path.md`](../../design/tillr-graph-is-the-path.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant as a proposed extension from the built, finite per-attempt workflows to a whole-item lifecycle graph: [`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md) records the static workflow machinery as built and names this document as the broader model it extends; `docs/tillr/roadmap.md` (“The item graph is the whole path”) still lists that model as future work. The tree does not establish whether the owner still wants the full migration implemented: the design remains `in-review`, its `D-GP14` has no built entry, and the roadmap still lists [`OQ-GP7`](../../design/tillr-graph-is-the-path.md#OQ-GP7) as open.

**Accurate**

The proposal's defining distinction remains accurate: the built graph is a finite workflow for an attempt, whereas this proposal puts gate, judge, landing, and later acts into a graph for the item's whole life. [`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md) describes workflow expansion, node runs, and one gate/landing; `tools/tillr-drain/drain/workflow_loop_test.go` has `TestWorkflowRunsTwoNodesAndLandsOnce`.

Two current claims need correction or qualification:
- In the opening `Status` paragraph, “every item still runs as one implicit single worker” does not follow from the absence of a default in `.tillr.toml`: `config/tillr/workflows/` contains six workflow files, and `tools/tillr-drain/drain/workflow_loop_test.go` (`TestWorkflowRunsTwoNodesAndLandsOnce`) verifies a multi-node item workflow. The narrower claim that this document's whole-lifecycle policy model is not built remains consistent with [`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md), which documents the built static model rather than the proposed whole-life model.
- In the same paragraph and the Decision Ledger preface, “the D-GP rows ... await the owner's review” is too broad. [`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md#why-its-this-way) (`D-IG11`) records an operator ruling accepting the graph design's sub-agent direction and says it overturns [`OQ-GP5`](../../design/tillr-graph-is-the-path.md#OQ-GP5)/`D-GP12`; distinguish that accepted direction from decisions still awaiting review.

The statements that [`OQ-GP1`](../../design/tillr-graph-is-the-path.md#OQ-GP1), [`OQ-GP3`](../../design/tillr-graph-is-the-path.md#OQ-GP3)–[`OQ-GP6`](../../design/tillr-graph-is-the-path.md#OQ-GP6) record owner rulings and that [`OQ-GP2`](../../design/tillr-graph-is-the-path.md#OQ-GP2) was superseded are corroborated by their Decision Ledger rows and, for GP2, `docs/design/tillr-storage.md` (“Status”). `D-GP11`'s “Partly” entry also matches the named `repeatedEnding` mechanism in the row; no diagnosis node is established by the checked-in design/build evidence.

**Status line**

The frontmatter `status: in-review` and `stage: DESIGN` remain reasonable for the whole-life proposal: [`OQ-GP7`](../../design/tillr-graph-is-the-path.md#OQ-GP7) still has an empty Answer, and `D-GP14` is not marked built in the Decision Ledger. The body status needs updating to separate the unbuilt whole-life policy from the already-built static workflow graphs ([`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md)). Its blanket statement that all `D-GP` decisions await review also needs correction for `D-GP12`, per the evidence above.

**Open questions**

[`OQ-GP1`](../../design/tillr-graph-is-the-path.md#OQ-GP1) and [`OQ-GP3`](../../design/tillr-graph-is-the-path.md#OQ-GP3)–[`OQ-GP6`](../../design/tillr-graph-is-the-path.md#OQ-GP6) are already answered in the document's Decision Ledger as owner rulings; [`OQ-GP2`](../../design/tillr-graph-is-the-path.md#OQ-GP2) is explicitly closed as superseded and moved to `docs/design/tillr-storage.md` ([`OQ-TS1`](../../design/tillr-storage.md#OQ-TS1)). [`OQ-GP7`](../../design/tillr-graph-is-the-path.md#OQ-GP7) is unanswered in this document (the “Answer” remains empty), and `docs/tillr/roadmap.md` (“The item graph is the whole path”) still calls it open. However, `docs/design/agent-runtime.md` [retired section 19.2](../../design/agent-runtime-retirement.md#what-happened-to-each-section) and its Decision Ledger (`D-G1`, `D-G7`) say non-remedy landing is absolutely blocked on a red trunk and reject scoped landing, which appears to answer GP7's central choice in favor of waiting. Because that file is marked “draft — for outside review,” the evidence does not establish that this newer position is a settled owner ruling; reconcile the documents before asking GP7 again unchanged. The document's statement that the remaining designer decisions await review is also no longer wholly true for `D-GP12`, as cited above.

**Action**

**correct facts** — distinguish the built static workflow graph from the unbuilt whole-life model, update the overbroad review-status claim, and reconcile [`OQ-GP7`](../../design/tillr-graph-is-the-path.md#OQ-GP7) with `docs/design/agent-runtime.md` [retired section 19](../../design/agent-runtime-retirement.md#what-happened-to-each-section) before treating it as open.

### 9. tillr-storage

[`docs/design/tillr-storage.md`](../../design/tillr-storage.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant; not superseded or iceboxed. The graph design continues to delegate its storage structure to this document: `docs/design/tillr-graph-is-the-path.md`, migration-order row 16, depends on the storage design's question. `docs/tillr/roadmap.md` says [OQ-TS1](../../design/tillr-storage.md#OQ-TS1) and [OQ-TS3](../../design/tillr-storage.md#OQ-TS3) remain blocked pending trials on a healthy file system. The live implementation remains SQLite (`tools/tillr-store/README.md`, “State invariants” and “Durability and checkpoints”), so the proposed graph-store interface and alternative record format still represent unsettled future work.

**Accurate**

The core proposal is still unbuilt: the live store uses SQLite and `Store.WriteTx` accepts a callback (`tools/tillr/store/write.go`); its in-process write queue only serializes writers in one process (`tools/tillr/store/writequeue.go`), rather than implementing the proposed single writer process and per-item logs. `tools/tillr-store/README.md` describes the current WAL, checkpoint and transaction behavior.

One statement no longer matches the tree: Section 9.1 item 3 says `finishLanding` moves the ref with the transaction open, and the opening status and [OQ-TS1](../../design/tillr-storage.md#OQ-TS1) prose say this fault is “being fixed now.” `tools/tillr-land/contention_test.go`, `TestFinalMoveHoldsNoStoreWriteAndTheClaimRefusesClose`, now asserts that no write transaction is open during the final Git move, a heartbeat can commit while that move is blocked, and the landing claim refuses a concurrent close. The implementation separates the short claim and receipt writes from the Git move in `tools/tillr-land/land/landing.go` (`claimLanding` and `recordPublished`). Thus that usage fault is fixed, not still in progress; the listed “four defects” and “three remain” snapshot should be updated accordingly. I did not independently rerun the 2026-11-17 storage trials or verify every date-stamped historical count in the document; I cannot tell from this inspection whether those measurements/counts remain reproducible.

**Status line**

The frontmatter `status: in-review` and `stage: DESIGN` remain accurate: the storage choice is unresolved, the ledger still marks the choices as designer decisions rather than built work, and the owner-facing questions remain blocked (`docs/design/tillr-storage.md`, Decision Ledger and Open Questions; `docs/tillr/roadmap.md`, graph/storage status paragraphs). The dated 2026-11-18 status paragraph is only partly current: its statement that the landing-lock fault is being fixed is stale, as shown by `TestFinalMoveHoldsNoStoreWriteAndTheClaimRefusesClose`; its statement that the proposed storage structure is not built remains consistent with the SQLite implementation.

**Open questions**

- **[OQ-TS1](../../design/tillr-storage.md#OQ-TS1):** Still open and not yet ready for an answer. Its blocked-on comment requires fixing the usage faults and rerunning the storage trials on a healthy file system. The roadmap still reports both [OQ-TS1](../../design/tillr-storage.md#OQ-TS1) and [OQ-TS3](../../design/tillr-storage.md#OQ-TS3) as blocked pending those trials (`docs/tillr/roadmap.md`); the question has no recorded answer in the document.
- **[OQ-TS3](../../design/tillr-storage.md#OQ-TS3):** Still open, but not yet askable on its stated terms: its blocked-on comment requires a single-writer decision and implementation plus the stuck-writer trial. The current SQLite `WriteTx` path (`tools/tillr/store/write.go`) is not that writer, and the document's answer remains empty.
- **[OQ-TS2](../../design/tillr-storage.md#OQ-TS2)** is not listed as an open question; the Decision Ledger identifies it as the designer's decision, not an owner's ruling. I found no owner answer in the inspected roadmap references, so whether the owner has reviewed or overturned it cannot be established from this tree inspection.

**Action**

correct facts — the document remains the active, unresolved storage design, but its account that landing still moves the ref under a store write is contradicted by the current implementation and regression test.

### 10. tillr-early-design-landing

[`docs/design/tillr-early-design-landing.md`](../../design/tillr-early-design-landing.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant as an unbuilt design dependency, not as a description of a shipped early-landing mechanism. The migration table in `docs/design/tillr-graph-is-the-path.md` lists the cut and early landing in step 14; `docs/tillr/roadmap.md` says the graph model is not built. A scoped search of production tillr sources, `rg -n -i 'early landing|early-landing|OQ-EL|cut candidate|documents-only candidate' tools/tillr-* --glob '!**/*_test.go' --glob '!**/README.md'`, printed no matches (`rg_exit=1`). The owner has chosen “always” (the document's [OQ-EL1](../../design/tillr-early-design-landing.md#OQ-EL1) ledger row), so the direction remains wanted; the implementation and detailed D-EL decisions remain unbuilt/unreviewed.

**Accurate**

The central built/unbuilt claim is accurate: the graph migration still lists the cut as unbuilt step 14, and the production-source search above found no early-landing mechanism. The underlying rule that an owner answer counts from committed trunk content is built: `tools/tillr-drain/README.md`, “Owner decisions in documents,” and `TestUncommittedOwnerAnswerDoesNotChangeTheConflict` in `tools/tillr-drain/drain/owner_question_test.go` cover it; that does not implement a cut or early landing.

Three status statements are stale or inconsistent. First, frontmatter `summary` still calls whether every design change lands early “the owner's open question,” while Section 1 and the [OQ-EL1](../../design/tillr-early-design-landing.md#OQ-EL1) ledger row record the owner's 2026-11-18 ruling for “always.” Second, frontmatter `next` says only D-EL3 through D-EL7 await owner review, while the Decision Ledger says all D-EL1 through D-EL7 are designer decisions, none individually ruled by the owner, and all await review. Third, the opening roadmap paragraph in `docs/tillr/roadmap.md` still says it asks [OQ-EL1](../../design/tillr-early-design-landing.md#OQ-EL1) whether every design change lands early; `docs/design/tillr-graph-is-the-path.md`, [OQ-GP6](../../design/tillr-graph-is-the-path.md#OQ-GP6), repeats that stale description. The early-landing document's statement that the graph depends on an unfiled/unbuilt graph model is consistent with the roadmap and the graph design's status statement.

The first broad search attempt failed with `unexpected EOF while looking for matching \`` because of shell quoting; the scoped search above was rerun successfully. No source files were changed; `git status --short` printed nothing.

**Status line**

`status: in-review`, `stage: DESIGN`, and the body statement that the early-landing mechanism is unbuilt are true: the graph design marks its graph model unbuilt, step 14 still proposes building the cut, and the production-source search found no implementation. The owner has settled the high-level “always” choice, but that does not make the design built or settle the detailed designer decisions; the ledger says those await review. The metadata summary and `next` need correction as detailed above, but the status/stage themselves need not change.

**Open questions**

- **[OQ-EL1](../../design/tillr-early-design-landing.md#OQ-EL1):** Already answered by the owner on 2026-11-18: always land design changes early. Evidence: Section 1 and its Decision Ledger row. The roadmap overview and graph design's [OQ-GP6](../../design/tillr-graph-is-the-path.md#OQ-GP6) still describe it as open and are stale.
- **[OQ-EL2](../../design/tillr-early-design-landing.md#OQ-EL2):** Closed without a ruling, and its underlying rule is built: an uncommitted owner answer does not change the conflict. Evidence: the [OQ-EL2](../../design/tillr-early-design-landing.md#OQ-EL2) ledger row and `TestUncommittedOwnerAnswerDoesNotChangeTheConflict` in `tools/tillr-drain/drain/owner_question_test.go`.
- **[OQ-EL3](../../design/tillr-early-design-landing.md#OQ-EL3):** Still open, but blocked and not askable yet under its stated prerequisite: a design must settle what giving up an item does to its early-landed documents. Evidence: the [OQ-EL3](../../design/tillr-early-design-landing.md#OQ-EL3) blocked-on annotation and answer text; the graph design's Section 3.7 also says the consequence for provisional designs awaits the giving-up design. `tillr item close` exists (`tools/tillr-item/README.md`, close/reopen commands), but the question is specifically the fate of documents already landed on trunk, not whether close exists.

**Action**

Correct facts — the frontmatter summary and `next` contradict the resolved [OQ-EL1](../../design/tillr-early-design-landing.md#OQ-EL1) and the ledger's review status for D-EL1 through D-EL7, while the roadmap and parent graph also still present [OQ-EL1](../../design/tillr-early-design-landing.md#OQ-EL1) as open.

### 11. tillr-vocabulary

[`docs/design/tillr-vocabulary.md`](../../design/tillr-vocabulary.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant as a terminology and decision record; it is not superseded or iceboxed. Current designs continue to rely on it: `docs/design/tillr-graph-is-the-path.md` (“Reads with” the vocabulary), `docs/design/tillr-landing-call.md` (“Reads with” the vocabulary and blocks [OQ-LC1](../../design/tillr-landing-call.md#OQ-LC1) on item-owned worktrees), and `docs/design/criterion-repair.md` (“Words”) link to its definitions. The definitions and drafted work in Section 9 are not themselves an implementation plan or filed work; the roadmap still includes the design among the proposals in `docs/tillr/roadmap.md`.

**Accurate**

Not wholly accurate as a current status account; the vocabulary rollout remains incomplete, but some machinery it describes is now built.

- The frontmatter summary says “Three questions remain for the owner: two names, and whether an item is finished at landing or at proof.” That is stale: the document itself says [OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4), the charter name, was ruled on 2026-11-18 in its “Needs your ruling” summary and “The owner's rulings” ledger; only [OQ-VO5](../../design/tillr-vocabulary.md#OQ-VO5) and [OQ-VO6](../../design/tillr-vocabulary.md#OQ-VO6) remain in its “Open Questions” section. The ledger defines charter as the file's term for one use of tillr.
- The body’s “Nothing here is built” status statement is too broad now. [`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md) identifies the built workflow/node-run machinery; `tools/tillr-drain/drain/landing_call_lifecycle.go` records landing-call lifecycle outcomes, and `tools/tillr-drain/drain/landing_call_test.go` includes `TestLandingCallClosesOnProvenSupersededOrWithdrawn`. This does not mean the vocabulary’s whole proposal is built: the item-owned worktree lifecycle and the proposed renames remain unbuilt, as `docs/design/tillr-landing-call.md` still blocks [OQ-LC1](../../design/tillr-landing-call.md#OQ-LC1) on worktrees belonging to items.
- Section 4 says the distinction between landed and proven “lives only in the green pointer,” and Section 5.4 says “Proven” has “No stored state.” That needs narrowing to “no proven item state”: `landing_call_lifecycle.go` persists a landing-call closure with outcome `proven`, and `TestLandingCallClosesOnProvenSupersededOrWithdrawn` covers that call outcome. The green pointer remains the proof source; the call event is a stored derived outcome, not a new item state.
- Cross-document references have not caught up with [OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4): `docs/tillr/design-status.md` lists [OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4) as open, and `docs/design/tillr-core-and-git-landing.md` still describes both names as unanswered. Those conflict with the vocabulary’s 2026-11-18 charter ruling in its ledger; they are not evidence that [OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4) remains open.

The dated measurements in Section 3.1 are explicitly measurements from 2026-11-18 and should be read as historical, not as current measurements.

**Status line**

The frontmatter `status: in-review` and `stage: DESIGN` are still true: [OQ-VO5](../../design/tillr-vocabulary.md#OQ-VO5) and [OQ-VO6](../../design/tillr-vocabulary.md#OQ-VO6) have empty answers in “Open Questions,” and the designer decisions D-VO1 through D-VO12 remain reviewable in the Decision Ledger. The dated body’s “Nothing here is built” claim needs correction as noted above; it does not make the frontmatter stage itself false. The broader built state is evidenced by [`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md) and `tools/tillr-drain/drain/landing_call_lifecycle.go`.

**Open questions**

- **[OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4) is answered, not open:** the owner’s “charter” ruling is recorded in the ledger and in the “Charter” entry of Section 5.1. It should not appear in a current open-question list. The “git-landing” instance name is a different question, [OQ-VO5](../../design/tillr-vocabulary.md#OQ-VO5).
- **[OQ-VO5](../../design/tillr-vocabulary.md#OQ-VO5) remains open and askable:** its “Answer” is empty. It asks what the first use is called; `tools/tillr/definition/definition.go` declares `GitLandingName = "git-landing"` and explicitly calls it a placeholder pending the owner's naming decision, but the document records no owner ruling selecting a final name.
- **[OQ-VO6](../../design/tillr-vocabulary.md#OQ-VO6) remains open and askable:** its “Answer” is empty. The `proven` landing-call outcome in `landing_call_lifecycle.go` does not decide whether the item’s finished state is reached at landing or proof; the item-state question remains distinct.
- [OQ-VO1](../../design/tillr-vocabulary.md#OQ-VO1) through [OQ-VO3](../../design/tillr-vocabulary.md#OQ-VO3) are recorded as closed by designer decisions in “The owner's rulings” / “The designer's decisions, awaiting the owner's review.” The decisions can still be overturned by the owner, but the questions are not listed as open questions in the document.

**Action**

correct facts — keep this relevant in-review reference, but update its stale question summary and distinguish the partly built graph/call machinery and persisted call outcome from the still-unbuilt vocabulary rollout and item-owned worktree lifecycle.

### 12. tillr-landing-call

[`docs/design/tillr-landing-call.md`](../../design/tillr-landing-call.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still wanted and active as a design: its “The direction and what exists” section records the owner's 2026-11-18 request to design the suspended tool call separately from gate contents; `docs/tillr/roadmap.md`, “Filed 2026-11-18: a landing is a suspended tool call,” files five related build items, and `docs/tillr/queue-order.txt` still lists `item-landing-call-session-returns`, `item-landing-call-record`, `item-landing-call-pi-fork-park`, `item-landing-call-land-tool`, and `item-landing-call-obstacle-tool`.

**Accurate**

The design correctly distinguishes the unshipped `land` tool from built call-record groundwork: `tools/tillr-run/backends/pi/ending-capability` contains `footer`, `tools/tillr-drain/drain/landing_call.go` says the shipped adapters are not wired to its answer hand-off, and the roadmap's “Add the land tool and its brief ending to pi workers” item remains a filed implementation task. One status detail is stale: the `D-LC5` row in “Decision Ledger” says `Built —`, but `tools/tillr-drain/drain/landing_call_lifecycle.go` implements silent closure once proof reaches the landing, and `tools/tillr-drain/drain/landing_call_test.go` tests that in `TestLandingCallClosesOnProvenSupersededOrWithdrawn`; `answerLandingRefusal` in `landing_call.go` also records reverted answers, covered by `TestLandingCallAnswersJudgeAndRevertFromTheirDurableRecords`. These are partial implementation of step 5, not completion of its full scope: answer delivery is not wired to a shipped adapter, and the roadmap still has no filed step-5 item. The claim in “Delivery, and how the drain knows” that the remote fork and installed host lack the patches is a dated 2026-11-18 observation; this read-only checkout shows a local `patch/pi` branch (`git branch --list 'patch/pi' 'origin/patch/pi'` printed only `patch/pi`), but I did not fetch or inspect the host, so their state on 2026-11-19 cannot be confirmed.

**Status line**

`status: in-review` / `stage: DESIGN` remains accurate: the “Decision Ledger” says the `D-LC` decisions await owner review, while the roadmap files implementation work rather than declaring this design complete. Its count of three of seven build steps landed remains accurate as a count of completed steps: “Build order” still has step 4 in the queue, step 5 drafted but not filed, step 6 in the queue, and step 7 not drafted; the partial step-5 lifecycle implementation noted above does not fulfill the whole drafted item. The `D-LC5` `Built` cell should nevertheless be updated to describe that partial implementation (see Accurate).

**Open questions**

[`OQ-LC1`](../../design/tillr-landing-call.md#OQ-LC1) remains unanswered: the question asks whether the item worktree stays through proof; `docs/design/tillr-vocabulary.md`, “The life of a worktree” and its `D-VO5` ledger row propose retaining a landed item's worktree until proof, but its status text says those designer decisions await owner review and its “Open Questions” section still leaves [`OQ-VO6`](../../design/tillr-vocabulary.md#OQ-VO6) unanswered. It is still askable as confirmation or rejection of that proposal. Its stated blocker also remains operationally true: the vocabulary's “Drafted items” section has not filed a worktree-lifecycle build item, and the current drain still uses lane worktrees (`tools/tillr-drain/drain/loop.go`, `laneTarget`); item-owned worktrees are not built.

**Action**

correct facts — amend `D-LC5`'s `Built` entry to record the implemented proof-closure and reverted-answer behavior as partial, while keeping the suspended-call design in review because the worker tool and adapter delivery remain unbuilt.

### 13. tillr-separable-parts

[`docs/design/tillr-separable-parts.md`](../../design/tillr-separable-parts.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant as the answer and consumer contract for Product QA: `docs/tillr/design-status.md` keeps it in the cross-project reading order and says its section 2 is the stable CLI/record seam; `docs/design/tillr-definitions-plan.md` still calls for Product QA's agent to run the worked example and provide round three. The document also preserves dated first- and second-round evidence, so those sections remain useful as history.

**Accurate**

Most implementation claims are explicitly snapshots dated 2026-11-16 through 2026-11-18, and the current design index corroborates the key build-plan claim: `docs/tillr/design-status.md` says all eighteen definition-plan items landed by 2026-11-18. One material claim is false even against the document's stated 2026-11-18 state: Section 7.1's R2-11 row says “Unbuilt, and in no plan: both are still in the tree.” The accepted run-wall ruling is recorded as carried out in commit `adc9b1269` in `docs/design/run-wall-ruling.md`; `config/tillr/backend-policy.json` has no `timeout_seconds`, the three backend adapters initialize `wall=` empty, and `tools/tillr-run/run/wall_default_test.go` contains `TestWallTimeoutDefaultIsUnbounded`. The `timeout_ms == 0` behavior is also pinned by `tools/tillr-run/main_test.go`'s `TestAnUnboundedRequestRunsAndHoldsANullExpiryLease`. Thus R2-11 is not unbuilt and without a plan; the row needs correction. Its opening summary's claim that every request is built or mapped to unfiled work is also incomplete: R2-11 was neither at that snapshot, although it has since been implemented.

**Status line**

`status: in-review`, `stage: DESIGN`, and `updated: 2026-11-18` remain plausible: the document says `D-FQ1` to `D-FQ5` await the owner's review and calls for Product QA's third round; `docs/tillr/design-status.md` independently records those five designer decisions as awaiting review and the P2/P3/P4/P6 pieces as unfiled. The “updated” date is a dated document snapshot, not an assertion that it was reverified on 2026-11-19. No evidence found that the document has been graduated or superseded.

**Open questions**

[`OQ-FQ7`](../../design/tillr-separable-parts.md#OQ-FQ7) and [`OQ-FQ8`](../../design/tillr-separable-parts.md#OQ-FQ8) remain unanswered and askable only when their stated consumer need arises: both answer blocks are empty in this document, and `docs/design/tillr-autoqa.md` says AutoQA remains iceboxed by the owner, has no code, and its [`OQ-AQ2`](../../design/tillr-autoqa.md#OQ-AQ2) is blocked until pickup; `docs/tillr/roadmap.md` still lists AutoQA in the Icebox. The other Product QA questions are recorded as closed or superseded in this document's Decision Ledger. The five `D-FQ1`–`D-FQ5` decisions still await owner review, as also stated in `docs/tillr/design-status.md`.

**Action**

correct facts — Section 7.1's R2-11 status is contradicted by the accepted and implemented run-wall ruling, so update the row and any summary that depends on it.

### 14. tillr-product-qa-requests

[`docs/design/tillr-product-qa-requests.md`](../../design/tillr-product-qa-requests.md): **stale**; recommended action: **correct facts**.

**Relevant**

**Still relevant as a historical input and audit trail; not a current design to build from.** It records the Product QA pipeline's second-round requests and the evidence from its November 16–17, 2026 trial (Sections 1, 4, and 5). The tillr design index, under “Status of every design,” classifies it as “Received; answered” and says its requests were built by the definitions plan; that index also lists it as background for the Product QA agent's next round. The actual response is in `docs/design/tillr-separable-parts.md`, Section 7, which maps R2-1 through R2-14 to built work or unfiled next-tranche work. The first tranche's 18 items are reported landed in `docs/tillr/design-status.md`, “Read this first,” and in the separable-parts answer's Section 7.

The original trial findings remain useful as dated evidence, but the action state is mixed: R2-1 through R2-6 are marked built in `docs/design/tillr-separable-parts.md`, Section 7.1; R2-7 and R2-8 are only partly built; R2-9 through R2-14 remain unbuilt there, with the planned work in `docs/design/tillr-definitions-plan.md`, “What the tranche waits on” and its next-tranche list. In particular, the design index says R2-4's remaining terminal-state validation was still queued as `item-jl-terminal-state-receipts`.

**Accurate**

The document is explicitly a reproduction of a reply received on 2026-11-17, and its trial statements are scoped to the dates and snapshot it names in Sections 1 and 4–5. I found no contradiction to those historical trial results: `docs/design/tillr-separable-parts.md`, Section 7.1, repeats the original R2 findings and records the subsequent status; its Section 7.2 does the same for the stable-surface corrections. Those later outcomes are not evidence that the older trial results were false.

The statements that do not match the current repository status are in the frontmatter:

- `status: draft` and `stage: DESIGN` describe this as an active design draft, while `docs/tillr/design-status.md`, “Status of every design,” classifies it as a received reply that has been answered, and the document itself says “Received, reproduced here by tillr.”
- `next:` says tillr answers R2-1 to R2-6 first. That is superseded by the answer already in `docs/design/tillr-separable-parts.md`, Section 7.1, which covers R2-1 through R2-14 and gives each a current status. It also points to `docs/design/tillr-autoqa-requests.md`; the current answer document instead identifies `docs/design/tillr-product-qa-requests.md` as the recorded second-round reply in its opening “In short” and Section 7.

I cannot verify whether the Product QA team still runs its fuzzer by hand or still keeps its own dispatcher and acknowledgment cursor (the “Meanwhile” paragraph in Section 6), or whether its external question QS4 remains open: that project's current repository/state is not present here. The tillr-side answer in `docs/design/tillr-separable-parts.md`, Section 7.3, offers two shapes for a person as a step without choosing between them.

**Status line**

**Not true as current document metadata.** The frontmatter's `status: draft` / `stage: DESIGN` conflicts with `docs/tillr/design-status.md`, “Status of every design,” which says the reply was received and answered. The document's body is a preserved received reply, not an active design; its frontmatter `next:` is also stale as detailed above. Its dated body status (“Our second round”) is accurate as a statement of what the document records, not as the current workflow status.

**Open questions**

The four questions in Section 7 have tillr-side answers in `docs/design/tillr-separable-parts.md`, Section 7.3:

1. **A person as a step:** answered with two possible shapes, without choosing. Whether Product QA or its owner still needs to choose, and whether QS4 remains open on their side, cannot be determined from this repository.
2. **The concern behind the caps:** answered as runaway expansion and resulting spend; the proposed replacement is a pre-dispatch size display and a definition-set budget with a named next step (`tillr-separable-parts.md`, Section 7.3, question 2).
3. **Leases:** answered: positive `timeout_ms` uses the wall plus 300-second grace; zero timeout gives a non-expiring lease (`tillr-separable-parts.md`, Section 7.3, question 3).
4. **Runs on an item page without a workflow:** answered yes, with `item-def-item-page-history` identified as built (`tillr-separable-parts.md`, Section 7.3, question 4).

So questions 2–4 are no longer open as questions to tillr. Question 1 has a tillr response but no selected shape; whether it is still askable or open for the external consumer is not knowable here. The design index's “Status of every design” independently records the reply as answered.

**Action**

**correct facts** — keep the dated reply as historical evidence, but update its frontmatter status/stage and next-step text to reflect that it has been received and answered rather than presenting R2-1 to R2-6 as a pending response.

### 15. tillr-definitions-plan

[`docs/design/tillr-definitions-plan.md`](../../design/tillr-definitions-plan.md): **stale**; recommended action: **correct facts**.

**Relevant**

Yes. This remains the build hand-off for the eighteen-item first tranche and the prose-only next tranche; it is not replaced by the companion direction document, which links back to it for per-item build detail (`docs/design/tillr-core-and-git-landing.md`, “Reads with” and “Section 8”). The first tranche has concrete implementations in `tools/tillr/definition/landing.json`, `tools/tillr-store/definition.go`, `tools/tillr-item/item/lifecycle_policy.go`, and `tools/tillr-drain/drain/definition.go`. Its worked example remains present at `tools/tillr/examples/recorded-verdict/README.md`, and `tools/tillr-drain/recorded_verdict_example_test.go` contains `TestRecordedVerdictExampleREADME`. The next tranche is still relevant as proposed work: `docs/design/tillr-separable-parts.md`, “Section 7,” continues to label its requests (including R2-7 through R2-14) unbuilt or unfiled. It is not a record of completed work for those later units.

**Accurate**

The central first-tranche account is consistent with the tree: the definition package and embedded landing definition are in `tools/tillr/definition/`; store persistence is in `tools/tillr/definition/store.go` and `tools/tillr-store/definition.go`; lifecycle and planner routing are in `tools/tillr-item/item/lifecycle_policy.go` and `tools/tillr-drain/drain/definition.go`; and non-Git operation is covered by `TestNonLandingLoopStartsWithoutGit` in `tools/tillr-drain/definition_startup_test.go`. The runnable no-Git example and `TestRecordedVerdictExampleREADME` are also present.

One statement is out of date. Under “The words this plan uses,” it says the owner “has not chosen either word” and that both names are asked at [`OQ-VO4`](../../design/tillr-vocabulary.md#OQ-VO4) in `tillr-vocabulary.md`. The vocabulary states in its opening summary and “Needs your ruling” that [OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4) was ruled on 2026-11-18: the owner chose **charter** for the layer (`docs/design/tillr-vocabulary.md`, “[OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4)”). The Git-landing instance name is still open as [OQ-VO5](../../design/tillr-vocabulary.md#OQ-VO5) (`docs/design/tillr-vocabulary.md`, “[OQ-VO5](../../design/tillr-vocabulary.md#OQ-VO5)”). Thus only the instance name remains unsettled, and the plan should point to both the ruling and the remaining question accurately.

The plan's statement that the Product QA reply on file is the second round is true of `docs/design/tillr-product-qa-requests.md` (title and “Status”). Whether Product QA actually ran the example and supplied a third round outside this repository cannot be established from the current tree; no third-round document is present under `docs/`, but that absence does not prove no external exchange occurred.

**Status line**

The frontmatter `status: accepted` and `stage: DECIDED` remain consistent with a settled build hand-off whose first tranche has landed while its next tranche remains prose, rather than a claim that every proposed unit is built. The dated body status (“2026-11-17. Written against `4df6e426f`”) is an explicitly historical account of the plan's origin; it is not itself a current status assertion. The current design index still says all eighteen items landed and the ten next-tranche units are unfiled (`docs/tillr/design-status.md`, row “The build plan for definitions”), and the request mapping still calls the next units unbuilt/unfiled (`docs/design/tillr-separable-parts.md`, “Section 7”). The waiting-on statements about a third round and the owner's review were last stated as of 2026-11-18; their present-day status cannot be independently confirmed from repository evidence. The owner-review part is consistent with the direction document's “The designer's decisions, awaiting the owner's review” ledger, but that document alone does not establish whether a later review happened outside the tree.

**Open questions**

The plan correctly says it has no open question of its own: it introduces no `OQ-` entry and says the questions live in the direction document. Its naming reference needs correction: [OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4) is answered (charter), while [OQ-VO5](../../design/tillr-vocabulary.md#OQ-VO5) remains open (`docs/design/tillr-vocabulary.md`, “[OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4)” and “[OQ-VO5](../../design/tillr-vocabulary.md#OQ-VO5)”). The next-tranche paragraph also distinguishes the owner's ruling [OQ-CG2](../../design/tillr-core-and-git-landing.md#OQ-CG2) from designer decisions D-CG1 through D-CG5; the direction document records [OQ-CG2](../../design/tillr-core-and-git-landing.md#OQ-CG2) as settled by the owner and separately places D-CG1–D-CG5 under “The designer's decisions, awaiting the owner's review” (`docs/design/tillr-core-and-git-landing.md`, decision ledger). Those designer decisions remain reviewable as recorded there, though an owner review after the documents' 2026-11-18 update cannot be verified from this tree.

**Action**

Action: correct facts — keep the useful build plan, but correct the stale naming passage to say that “charter” was chosen for the layer and only the Git-landing instance name remains open.

### 16. tillr-core-and-git-landing

[`docs/design/tillr-core-and-git-landing.md`](../../design/tillr-core-and-git-landing.md): **stale**; recommended action: **correct facts**.

**Relevant**

Yes. It remains the source document for the owner's direction to separate tillr's general machinery from the Git-landing use: the current `docs/design/tillr-definitions-plan.md` still points to it for that direction, and `docs/design/tillr-graph-is-the-path.md` still reads with it while proposing a broader, not-yet-built graph model. The first build tranche and the second, no-landing example are present in `tools/tillr/definition/landing.json` and `tools/tillr/examples/recorded-verdict/README.md`; `TestRecordedVerdictExampleREADME` in `tools/tillr-drain/recorded_verdict_example_test.go` exercises the example. It is neither wholly built nor superseded: `tillr-definitions-plan.md`, “The next tranche, in prose,” still lists ten units as work to file, while “The graph model is not built” in `tillr-graph-is-the-path.md` confirms the larger graph proposal is not implementation replacing this design.

**Accurate**

Most implementation-state claims still match the checkout. `tools/tillr/definition/landing.json` contains the Git-landing states, transitions, prerequisites, completion rule, and `retention: keep`; `tools/tillr/definition/README.md`, “Completion and detail hooks,” says `after-completion` invocation and negative-answer routing are future integration work. The separate no-landing example remains under `tools/tillr/examples/recorded-verdict/`. The described Git-specific work is still in programs including `tools/tillr-drain/drain/stage.go`, `boundary.go`, `loop.go`, and `tools/tillr-land/land/landing.go`, rather than being wholly supplied by the charter file. The historical baseline in Section 3 is explicitly dated 2026-11-16 and identified as pre-build, so it is not presented as a current measurement.

There is one material facts/terminology mismatch, repeated in several places: the document treats both the abstraction's name and the Git instance's name as unanswered. In the current `docs/design/tillr-vocabulary.md`, “The owner's rulings” answers [OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4) as **charter**; [OQ-VO5](../../design/tillr-vocabulary.md#OQ-VO5) remains unanswered in “Open Questions.” Therefore the frontmatter `next` (“The two names are asked…”), the “Needs your ruling” text and “The words this document uses” should say that the abstraction is now called a charter and only the Git-instance name remains open. The [OQ-CG5](../../design/tillr-core-and-git-landing.md#OQ-CG5) ledger row's statement that both names “are asked there now” is also only partly true. Shipped code still uses `definition` (for example, `--definition`, `store.definition`, and `tools/tillr/definition/`), as `tillr-vocabulary.md`, [OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4), explicitly records; that compatibility spelling is not evidence that the conceptual name remains undecided.

**Status line**

`status: in-review` and `stage: DESIGN` remain appropriate: the direction is still referenced by the current build plan, the ten-unit follow-on remains prose in `tillr-definitions-plan.md`, and the five designer decisions D-CG1 through D-CG5 remain labeled as awaiting review in the document's “Decision Ledger.” The inline status dated 2026-11-18 is broadly true about the first tranche and the remaining work, but its statement that both names remain open is stale: `tillr-vocabulary.md`, “The owner's rulings,” settled [OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4) on 2026-11-18. The Product QA requests file still identifies itself as the second-round reply, supporting the statement that round three has not been recorded here.

**Open questions**

There is no live OQ in this document itself: its “Decision Ledger” marks [OQ-CG1](../../design/tillr-core-and-git-landing.md#OQ-CG1) through [OQ-CG5](../../design/tillr-core-and-git-landing.md#OQ-CG5) closed, moved, or replaced by decisions/review. [OQ-CG5](../../design/tillr-core-and-git-landing.md#OQ-CG5) was split into vocabulary questions; its kind-of-layer part is answered by `docs/design/tillr-vocabulary.md`, “The owner's rulings” ([OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4): charter), while its Git-landing-instance part remains askable as [OQ-VO5](../../design/tillr-vocabulary.md#OQ-VO5) in that file's “Open Questions.” The separate D-CG1–D-CG5 entries are designer decisions awaiting the owner's review, not open questions; `docs/tillr/decision-provenance-review.md`, “Already asked elsewhere,” independently lists D-CG1 to D-CG5 for that review.

**Action**

**correct facts** — retain this still-relevant direction design, but update its conceptual terminology and all references that imply [OQ-VO4](../../design/tillr-vocabulary.md#OQ-VO4) is still open, while preserving `definition` where it names the shipped compatibility API.

### 17. tillr-intake-paths

[`docs/design/tillr-intake-paths.md`](../../design/tillr-intake-paths.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant as an account of distinct ways work reaches the queue and the common item-block contract, but several concrete paths it describes as future work are now implemented in separate designs and code. It is not wholly superseded: `docs/design/tillr-queue-mirrors-designs.md` still assigns the doors “by which work with no design enters” to this design, and `docs/design/tillr-idea-pipeline.md` still reads it as describing the other intake doors. The specific owner-idea path is now owned by [`docs/reference/observatory-actions.md`](../../reference/observatory-actions.md) and its reconciliation in `docs/design/tillr-idea-pipeline.md`; generic item-graph behavior is owned by [`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md). The document is therefore useful as a cross-door synthesis, but its current-mechanism and “nothing built” claims need correction. It is not marked iceboxed in `docs/tillr/roadmap.md` or `docs/tillr/design-status.md`.

**Accurate**

The owner’s original statements and the historical account in Section 1 remain relevant as the dated provenance of the proposal; the implementation claims in Sections 2–3 are partly stale:

- Section 2 says a missing guide mark is only warned about and “stored and dispatched like any other,” and Section 3 says such an item is dispatched unchanged. That is no longer true for this repository: `.tillr.toml` enables `[prose] rewrite = true`; `tools/tillr-item/item/sync.go` creates rewrite work from prose findings, `tools/tillr-drain/drain/document_prerequisite.go` holds the original until its rewrite lands, and `tools/tillr-drain/drain/prose_rewrite.go` builds a rewrite brief from the guide and current findings. `TestProseRewriteHoldAndRelease` and `TestProseRewriteContractRefusedBeforeGate` in `tools/tillr-drain/drain/prose_rewrite_test.go` cover the hold, release, and contract protection. The claim needs the setting’s scope: the setting defaults false, but it is true in this repository.
- Section 3 says the owner’s rough idea has “Nothing in the tree” and that the save/design work is “Not built.” [`docs/reference/observatory-actions.md`](../../reference/observatory-actions.md#idea-intake-and-the-design-turn) documents the landed idea-save and design-turn blocks and names `tools/tillr-item/item/idea.go`, `tools/tillr-view/serveidea.go`, and the page tests; `TestIdeasHTTPSavesWordsAndShowsIntake` in `tools/tillr-view/idea_internal_test.go` verifies saving and displaying intake. `tools/tillr-drain/drain/idea.go` supplies the design-seat turn and asks it to preserve the words and produce `from`-marked item blocks; `TestIdeaStageCarriesWordsGuideAndInstructions` and `TestIdeaLandingImportsOutcomesAtItsPlace` in `tools/tillr-drain/drain/idea_test.go` cover it. The one-turn idea design is built, but it is not a separate rewrite-then-shape sequence, as the row also notes.
- Section 3 says an item with no document block is “dispatched to a build worker” and that no action follows. For document-fed Git landing, `tools/tillr-drain/drain/document_prerequisite.go` now detects unshared open items and routes them through `triageDecision`; `tools/tillr-drain/drain/triage_test.go` contains `TestDocumentFedPrerequisiteRoutesBeforeSeatAndWorkflow`, which expects a design-seat `triage-no-block` dispatch. `tools/tillr-drain/drain/document_prerequisite.go` also explicitly excludes generated items and saved ideas from that prerequisite, so the behavior is scoped rather than a universal rule for every store.
- The Section 2 statement that the planner does not check whether a document declares an item is false as a blanket statement for the document-fed path, for the reason above. The original sync findings/warnings still exist; the stale implication is that those findings have no dispatch consequence. Its other claims about checks being divided between sync and planner are still broadly descriptive: current behavior is implemented across `tools/tillr-item/item/sync.go` and the drain prerequisite/planner, not as one universal end-of-intake checker.
- The conflict row’s description of a blocked report being routed to design and, when needed, the owner remains consistent with the conflict machinery in `tools/tillr-drain/drain/blocked.go`, `stage.go`, and `owner_question.go`. It is still a conflict/hold route, not evidence of a separately built general intake graph.

The proposed fixed item-block interface remains a useful abstraction for document-declared work, but the document should distinguish that block shape from store-declared ideas and generated work: `tools/tillr-drain/drain/idea.go` and `document_prerequisite.go` explicitly special-case those items.

**Status line**

The frontmatter `status: draft` and `stage: DESIGN` remain reasonable for the overall cross-door proposal: the concrete mechanisms have landed, but this document’s general relationship among the doors, common end shape, and unresolved design choices has not been accepted as a whole. Its `next` field (“Talk through the points under Not yet settled; nothing here is built or queued as its own item”) is stale: the cited idea-save/design work is built, and the no-block triage and prose-rewrite paths have working code and tests. `docs/tillr/design-status.md` also repeats the false “nothing built” summary for this document. The updated date and body status record the historical 2026-11-17 proposal, but should be revised when its current facts are corrected.

**Open questions**

- **One triage agent or two:** Still open and askable for a shared normalization/shaping policy. The mechanisms now differ by door—prose rewrite is separate from implementation, while idea design and no-block triage use design-seat work—but no general ruling says whether those functions should share an agent. The roadmap’s `item-sync-hold-unshared-items` details explicitly say the owner “has not said whether making a request well-formed and shaping rough text into designable work are one agent’s job or two.”
- **Which seat:** Answered for the implemented paths, not settled as a universal future policy. `TestProseRewriteHoldAndRelease` asserts rewrite work uses the `build` seat; `TestDocumentFedPrerequisiteRoutesBeforeSeatAndWorkflow` asserts no-block triage uses `design`; `tools/tillr-drain/drain/idea.go` prepares the idea’s design-seat task. The planning-seat alternative is no longer merely proposed: `docs/design/tillr-planning-node-model-tiering.md` documents the plan seat and `planned-build@v1`. Whether one seat should unify all intake paths remains askable only if the broader synthesis is retained.
- **Meaning of the guide mark:** The operational routing question is answered: with this repository’s `prose.rewrite = true`, prose findings cause a rewrite hold; once the rewrite lands or the block passes the check, the hold is released (`TestProseRewriteHoldAndRelease`). The check establishes whether the block passes the guide’s rules, not proof that its author read the guide; no evidence makes the mark an attestation.
- **What the owner types:** Answered for the supported idea-save path: [`docs/reference/observatory-actions.md`](../../reference/observatory-actions.md#idea-intake-and-the-design-turn) specifies both the terminal `idea` command and browser form, preserves the words, and records the selected place; `TestIdeasHTTPSavesWordsAndShowsIntake` verifies the browser path. The design does not establish that chat or dropped documents are supported intake surfaces; those are not built alternatives in this path.
- **Whether a deterministic checker enforces shape:** The current implementation answers that checks remain distributed, rather than establishing a single checker run for every intake. Sync produces the findings, `document_prerequisite.go` handles prose and missing-block prerequisites, and `validateTriage` in that file validates triage output. A universal checker shared by all doors remains a design choice if the proposed common interface is meant to require one.
- **Whether conflict is a door with its own graph:** Still open at the level asked here. Conflict reports already use a design/owner hold-and-ruling path (`tools/tillr-drain/drain/blocked.go`, `stage.go`, and `owner_question.go`), but those files do not establish it as a separate intake graph with the same declared output shape as the other doors.

**Action**

**correct facts** — retain the cross-door proposal, but update the built/unbuilt account, the stale next step and the still-open questions to reflect the implemented rewrite, idea and no-block triage paths without implying a universal intake graph exists.

### 18. tillr-planning-node-model-tiering

[`docs/design/tillr-planning-node-model-tiering.md`](../../design/tillr-planning-node-model-tiering.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant, partly built and not superseded as a whole: [`config/tillr/workflows/planned-build@v1.json`](../../../config/tillr/workflows/planned-build@v1.json) and [`build-pieces@v1.json`](../../../config/tillr/workflows/build-pieces@v1.json) provide the proposed plan, work, verify and piece stages; [`docs/tillr/roadmap.md`, “Add a planning stage that fans implementation out by seat”](../roadmap.md#add-a-planning-stage-that-fans-implementation-out-by-seat) records the owner's direction and the implementation split. The later [`tillr-diff-review.md`, “A workflow's evaluator is this review”](../../design/tillr-diff-review.md#4-one-reader-and-what-stood-in-its-way) proposes changing when and how the verify stage is reviewed, but its status calls it an unbuilt proposal, not a shipped replacement. Whether the owner still wants the ten-item cost experiment is not established by a later ruling in the evidence inspected.

**Accurate**

- The checked-in design's core implementation is supported by the two workflow files, [`config/tillr/backend-policy.bedrock.json`](../../../config/tillr/backend-policy.bedrock.json), and the test definitions `TestPlannedBuildProposal`, `TestPlannedBuildPieceScheduling`, `TestPlannedBuildPieceArguments`, and `TestPlannedVerifyReturnsAndRepeats` in `tools/tillr-drain/drain/planned_build_test.go` and `tools/tillr-drain/drain/planned_verify_route_test.go`; these tests were inspected, not run. `tools/tillr-drain/cost_test.go` checks the ten-item threshold and `unknown` cost behavior.
- The undated present-tense statement “Luna has not run at all” in [Section 7](../../design/tillr-planning-node-model-tiering.md#7-how-the-owner-learns-whether-it-pays) is stale. The `session_search` tool, queried for `item-doc-triage-2026-11-18-tillr-roadmap-designs read-18 Luna scout` in project `lane-7`, returned a 2026-11-19 Pi session `5628c1aa-c83e-4cda-bd52-7cd711273980` with model `amazon-bedrock/global.openai.gpt-6-luna`, 16 assistant messages and 15 reads. This is evidence Luna ran, not evidence that the `planned-build@v1` workflow ran or landed. The historical statement “no run had used either when this was measured” in Section 1.5 is dated 2026-11-17 and is not contradicted by that later session.
- The general statement “Every stored item runs as one worker” in [Section 5.7](../../design/tillr-planning-node-model-tiering.md#57-what-nothing-selects-and-what-is-not-built) is stale: a read-only immutable SQLite snapshot query, `SELECT item_id,workflow FROM workflow_selections WHERE workflow <> 'single-worker@v1'`, returned `item-doc-triage-2026-11-18-tillr-roadmap-designs | doc-triage@v1`. This particular triage node's brief identifies `read-18` as a scout node. That does not establish use of the distinct `planned-build@v1` workflow.
- The statement that no item selects `planned-build@v1` is not proved current by its original roadmap search. An immutable, read-only snapshot query `SELECT count(*) FROM landings l JOIN workflow_selections w USING(item_id) WHERE w.workflow='planned-build@v1' AND l.result='landed'` returned `0`; the same snapshot grouped selections as 127 `single-worker@v1` and one `doc-triage@v1`. Immutable SQLite ignores the write-ahead log, so this is a possibly stale snapshot, not a definitive current count. `rg -n --hidden 'workflow="planned-build@v1"' docs config/tillr` found only a quoted historical example in `docs/tillr/roadmap.md`, not an item directive; it cannot rule out selections elsewhere in the live store.
- The claimed absence of a default workflow and registered per-piece command remains consistent with the checked-in [`.tillr.toml`](../../../.tillr.toml), which has no workflows table, and [`config/tillr/commands.json`](../../../config/tillr/commands.json), which lists only `pack-sync`. The planner's prompt still names only the `owner` obstacle in [`planned-build@v1.json`](../../../config/tillr/workflows/planned-build@v1.json), as Section 5.4 says. The cost baseline and other historical measurements were not independently remeasured.

**Status line**

`status: in-review`, `stage: DESIGN`, and “Partly built” remain appropriate for a document combining the checked-in workflow and unresolved owner questions: the workflow files above exist, while the document's [Open Questions](../../design/tillr-planning-node-model-tiering.md#open-questions) still have empty answers. Its status sentence that no item selects the workflow is not independently established for the live store; the snapshot and its write-ahead-log limitation are stated above. The sentence that all the author's decisions still await owner review could not be independently verified from the files inspected.

**Open questions**

- **[OQ-PT7](../../design/tillr-planning-node-model-tiering.md#OQ-PT7)**, whether to commit the plan as a document: still marked blocked and unanswered in [Open Questions](../../design/tillr-planning-node-model-tiering.md#open-questions); [`tillr-storage.md`, OQ-TS1](../../design/tillr-storage.md#OQ-TS1) is also marked blocked and its answer is empty. The snapshot has zero landed `planned-build@v1` items but omits the write-ahead log, so whether the first-landing condition has since been met is unknown; it is not safe to declare the question askable or answered.
- **[OQ-PT8](../../design/tillr-planning-node-model-tiering.md#OQ-PT8)**, opt-in versus universal versus size-based planning: still marked blocked and unanswered in the same [Open Questions](../../design/tillr-planning-node-model-tiering.md#open-questions). The immutable snapshot reports zero planned-workflow landings, but omits the write-ahead log; whether the ten-landing condition has been met in the live store and whether the actual comparison is available were not verified.

**Action**

**correct facts** — Retain the partly built design and its unanswered decisions, but date or correct its now-stale claims that Luna has never run and that every stored item uses one worker, and verify the live planned-workflow count before updating its experiment and question blockers.

### 19. model-benchmarks-from-drain-records

[`docs/design/model-benchmarks-from-drain-records.md`](../../design/model-benchmarks-from-drain-records.md): **current**; recommended action: **keep as is**.

**Relevant**

Still wanted as a proposal, not built, superseded, or iceboxed. `docs/tillr/roadmap.md` under “Rulings that are waiting on the owner” still describes this as the owner's request to compare models on identical recorded work, says its questions await him, and calls copying the shortlist “work to file.” `docs/design/tillr-planning-node-model-tiering.md`, under “A second way to compare models exists on paper,” explicitly distinguishes this replay proposal from its production cost report and says neither replaces the other. As a code check, `rg -n -i "task record|replay root|reading packet|replay arm|OQ-MB|model benchmark" tools/tillr-*` printed `No matches in tools/tillr-*`; there is no indicated replay implementation in those tool trees.

**Accurate**

No concrete contradiction was found in the document's central, current claims. Under “What this design does not do,” it says it is a proposal that replays nothing; the roadmap's “Rulings that are waiting on the owner” still says the freeze is work to file, and the tool-source search above found no replay/task-record/packet implementation. The design's “The request, as the owner said it” is explicitly dated 2026-11-17, so that historical quotation is not contradicted by newer repository evidence. The run-count, price, and environment measurements in “What a finished run keeps,” “What is not kept,” and “A first shortlist” are dated to 2026-11-17/18; they were not independently recomputed because this worktree has no `.tillr` store (the scoped search attempt reported `.tillr: No such file or directory`). The currently visible reachability evidence is consistent with the warning ([unlanded]: the commit named next is cited because HEAD does not contain it): `git cat-file -t 6ddc4b643` printed `commit`, `git merge-base --is-ancestor 6ddc4b643 HEAD` exited 1, and `git for-each-ref --contains=6ddc4b643` showed only `refs/backup/main-before-reauthor-2026-11-17` and `refs/tillr/trunk-history/accepted/b15d48ce6aa47a3e4dcc7ef6d1e695d6e919248b`; this is the starting commit singled out in “Design tasks,” not proof that every shortlisted run's result commit remains available. No specific error in the historical measurements can be established from the current worktree.

**Status line**

The frontmatter `status: in-review` and `stage: DESIGN`, and the “Status: 2026-11-18” statement that this is a proposal with no replay built, are consistent with `docs/tillr/roadmap.md` under “Rulings that are waiting on the owner,” which still lists it as a request with an unfiled freeze step. The “Decision Ledger” also labels its design-seat decisions as unreviewed by the owner. The repository does not independently establish that the owner's readiness has not changed since the dated statement.

**Open questions**

- **[OQ-MB1](../../design/model-benchmarks-from-drain-records.md#OQ-MB1) — first-round size, models, and spending limit:** still open and unanswered. Its answer is empty and its blocked-on marker is `owner-ready-to-run-a-round` in “Open Questions”; `docs/tillr/roadmap.md` says the replay's questions await the owner and the freeze precedes the round. The repository does not say the owner is ready now, so it is not yet askable on the document's stated condition.
- **[OQ-MB5](../../design/model-benchmarks-from-drain-records.md#OQ-MB5) — blinded or named results:** still open and unanswered, with an empty answer and the same `owner-ready-to-run-a-round` blocker in “Open Questions.” No packet exists according to the document's “What this design does not do,” and the roadmap continues to defer the questions until the owner is ready; whether he has since become ready cannot be determined from the tree.
- **[OQ-MB10](../../design/model-benchmarks-from-drain-records.md#OQ-MB10) — stop an overspending replay or let it run:** still open and unanswered; its answer is empty and its blocker is `owner-ready-to-run-a-round` in “Open Questions.” The roadmap still says the round questions await the owner. No later answer or changed blocker appears in the current roadmap or in `docs/design/tillr-planning-node-model-tiering.md`'s reference to replay benchmarks as existing only on paper.

**Action**

keep as is — the roadmap still records this as a wanted, unbuilt proposal whose next copying step is unfiled, and no current source evidence contradicts its design or answers its three blocked questions.

### 20. tillr-queue-persistence

[`docs/reference/tillr-queue-persistence.md`](../../reference/tillr-queue-persistence.md) *(graduated 2026-11-20 from the design evaluated below)*: **stale** at 2026-11-18 triage; recommended action: **correct facts**.

**Relevant**

Still relevant as the built reference for tracked-document item sync and the epoch record: its `D-QP2` ledger entry points to `tools/tillr-item/item/sync.go`, and sync behavior is covered by `TestSyncOverThisRepositorysDocuments` and `TestSyncRoadmapEntriesInterleaveAndPark` in `tools/tillr-item/item/`. The record-inventory part also remains relevant: `tools/tillr-store/main.go` exposes `inventory`, implemented in `tools/tillr-store/inventory.go`. It is not wholly superseded, but the queue-mirrors proposal explicitly says it amends this document's [`OQ-QP2`](../../reference/tillr-queue-persistence.md#why-its-this-way), `D-QP5`, and reconciliation section, and is waiting on [`OQ-QM1`](../../design/tillr-queue-mirrors-designs.md#OQ-QM1) (`docs/design/tillr-queue-mirrors-designs.md`, “What this supersedes or amends” and “[OQ-QM1](../../design/tillr-queue-mirrors-designs.md#OQ-QM1)”).

**Accurate**

The core claim that `tillr item sync` reads tracked Markdown item blocks and materializes them is supported by `tools/tillr-item/item/sync.go` and `TestSyncOverThisRepositorysDocuments`. The inventory is built, while automatic epoch retirement remains deferred: `tools/tillr-store/main.go` lists `inventory` but no `retire-epoch` face, and `D-QP7` records the owner's hold pending review of the inventory.

The following claims need correction:

- **[`OQ-QP2`](../../reference/tillr-queue-persistence.md#why-its-this-way) is not settled as an owner-approved design choice.** The document itself notes the owner's objection and reopening, but still labels the ledger row “Settled” and says “Needs your ruling: None.” `docs/design/tillr-queue-mirrors-designs.md`, “[OQ-QM1](../../design/tillr-queue-mirrors-designs.md#OQ-QM1)” and “What this supersedes or amends,” says the item-block question is open; `docs/tillr/decision-provenance-review.md`, “The hidden comment itself,” confirms that [`OQ-QM1`](../../design/tillr-queue-mirrors-designs.md#OQ-QM1) governs it. The parser is implemented in `tools/tillr-item/item/sync.go`; implementation does not establish owner acceptance.
- **The `D-QP5` parking rule is out of date as policy.** `docs/tillr/decision-provenance-review.md` explicitly says the owner's 2026-11-18 words were that Icebox items should be loaded and marked blocked. `docs/design/tillr-queue-mirrors-designs.md`, “Step 3. The Icebox is `blocked`, reason `icebox`,” proposes that behavior. Current code still implements the old queued/not-started behavior (`TestSyncParkingStoredItems` in `tools/tillr-item/item/sync_parking_test.go`), so the new policy is proposed, not built.
- **`D-QP4` overstates what intake built.** Its ledger says the bootstrap-gap badge, thresholds and lock are built with intake, but `rg -n 'uncommitted intake|bootstrap-overdue|uncommitted_intake|oldest.*intake|intake.*badge' tools/tillr-item tools/tillr-view tools/tillr-drain` returned no matches (and [`docs/reference/observatory-actions.md`](../../reference/observatory-actions.md#words-files-and-preservation) notes that `D-QP4`'s badge and lock are not built). The claimed badge/overdue behavior is therefore not evidenced in those implementation paths; do not present the full D-QP4 behavior as built.
- **[`OQ-QP4`](../../reference/tillr-queue-persistence.md#why-its-this-way) and Section 7's “trace stays on” statements are false now.** The owner removed Git Trace2 recording in landed commit `d09016bba`; `tools/tillr-run/backends/pi/run-pi` unsets `GIT_TRACE2_EVENT` and related variables, and `docs/design/session-recording.md` begins with the 2026-11-17 removal ruling. This contradicts the claim in the [`OQ-QP4`](../../reference/tillr-queue-persistence.md#why-its-this-way) ledger row and Section 7.1 that the trace remains on.
- **The `D-QP8` account is internally inconsistent and its “files” wording is wrong.** The opening of Section 10 says `D-QP11` amended reconciliation into a report that writes nothing, and `tools/tillr-item/item/reconcile.go` implements the reconcile face; however, the `D-QP8` ledger row and Section 10's earlier decision text still say it “files” derived items. The report-only behavior should be the stated current contract. The queue-mirrors proposal further says that this reconcile command is to be removed with its step, but that proposal is explicitly not built before [`OQ-QM1`](../../design/tillr-queue-mirrors-designs.md#OQ-QM1) is answered (`docs/design/tillr-queue-mirrors-designs.md`, “Status” and “What this supersedes or amends”).

`D-QP6`'s attrition plan is properly marked superseded by [`OQ-TC1`](../../design/tillr-convergence.md#OQ-TC1) in its ledger row; `docs/design/tillr-convergence.md` records `item-seed-retire`, so its retained migration table reads as historical measurement rather than the current plan.

**Status line**

`status: accepted` and `stage: BUILT` remain accurate for the original sync/build-order work: `D-QP2` is marked built with the implementation and tests above, and Section 12 says its eight blocks landed. The built stage does not make the factual ledger current: `docs/design/tillr-queue-mirrors-designs.md` says this design was restaged BUILT on 2026-11-18 but also reopens [`OQ-QP2`](../../reference/tillr-queue-persistence.md#why-its-this-way). The separate “Needs your ruling: None” line and Section 9.2 “None” are false because [`OQ-QM1`](../../design/tillr-queue-mirrors-designs.md#OQ-QM1) is open and awaits the owner (`docs/design/tillr-queue-mirrors-designs.md`, “[OQ-QM1](../../design/tillr-queue-mirrors-designs.md#OQ-QM1)”).

**Open questions**

- [`OQ-QP1`](../../reference/tillr-queue-persistence.md#why-its-this-way): answered; the row records the 2026-11-16 owner ruling, and `TestSyncRoadmapEntriesInterleaveAndPark` plus `TestSyncRoadmapAppendPreservesStoredKeys` in `tools/tillr-item/item/sync_roadmap_order_test.go` exercise the order behavior.
- [`OQ-QP2`](../../reference/tillr-queue-persistence.md#why-its-this-way): reopened and still askable as [`OQ-QM1`](../../design/tillr-queue-mirrors-designs.md#OQ-QM1); the owner has not accepted the hidden item-comment mechanism (`docs/design/tillr-queue-mirrors-designs.md`, “[OQ-QM1](../../design/tillr-queue-mirrors-designs.md#OQ-QM1)” and “The hidden comment itself” in `docs/tillr/decision-provenance-review.md`).
- [`OQ-QP3`](../../reference/tillr-queue-persistence.md#why-its-this-way): the present ruling remains “no automatic retirement or bound”; its inventory deliverable is built in `tools/tillr-store/inventory.go`. The later retention decision is deliberately deferred until the owner reviews that inventory, rather than being an answerable open question in this document today (`D-QP7` and [`OQ-QP3`](../../reference/tillr-queue-persistence.md#why-its-this-way)).
- [`OQ-QP4`](../../reference/tillr-queue-persistence.md#why-its-this-way): answered differently by the owner, not still open as written: `d09016bba` removes Git Trace2 and `tools/tillr-run/backends/pi/run-pi` unsets its environment variables. The ledger should record the reversal rather than the 2026-11-15 designer settlement.

**Action**

**correct facts** — the document remains useful for the implemented sync and store-inventory design, but its reopened item-format decision, reversed trace decision, outdated Icebox policy, unbuilt bootstrap-gap claim and report-only reconciliation contract need to be reconciled with current evidence.

### 21. tillr-item-graphs

[`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md) *(graduated 2026-11-20 from the design evaluated below)*: **stale** at 2026-11-18 triage; recommended action: **correct facts**.

**Relevant**

The design remains relevant as the record of the shipped workflow-graph model: `docs/tillr/design-status.md` lists “Item graphs” as built, with all fifteen blocks landed and nothing of its own left to build; `tools/tillr-drain/README.md` still documents the live workflow system and links back to this design. The successor proposal `docs/design/tillr-graph-is-the-path.md` describes itself as extending and partly overturning this built machinery, but its status line says the graph model is not built and its designer decisions await owner review. Its proposed changes therefore do not make this document obsolete as a description of the current system.

**Accurate**

The build-status claims are supported: `config/tillr/workflows/` contains tracked workflows, including `scout-then-build@v1.json` and `design-from-scouts@v1.json`; `tools/tillr-drain/drain/workflow_loop_test.go` has `TestWorkflowRunsTwoNodesAndLandsOnce`; `tools/tillr-drain/drain/workflow_evaluator_test.go` has `TestWorkflowEvaluatorJudgeRoute`; and `tools/tillr-view/item_nodes_test.go` has `TestItemGraphNodesAndSingleWorker`.

Three claims need correction:
- Under “Why it matters” and Section 1.1, current-tense claims that the loop cannot run a second node, scouts, or an intervening command, and that a no-commit run is refused, no longer describe the tree. The tests `TestWorkflowRunsTwoNodesAndLandsOnce`, `TestCommandWorkflowOutcomesAndPreservation` in `tools/tillr-drain/drain/command_loop_test.go`, and `TestWorkflowExitCandidatePolicies` in `tools/tillr-drain/drain/workflow_exit_test.go` cover multi-node, command-node and no-commit graph behavior. Section 8 is explicitly a dated historical measurement and is not the problem.
- Section 5’s cycles row and `D-IG4` still state that the loader refuses expansion above depth 8 or 100 nodes. The current command in `tools/tillr-drain/drain/workflow_check.go` says “Graph size is reported, never refused”; `TestWorkflowExpansionDeepAndWide` in `tools/tillr-drain/drain/workflow_expansion_test.go` covers deep expansion. The document itself later says these caps were superseded in the blocks “Add a check that refuses a malformed workflow file” and “Add stages that name another workflow.”
- Section 7.3 still says the amendments to `agent-runtime.md` and `tillr-idea-pipeline.md` are owed. They are present: `docs/design/agent-runtime.md` describes expansion before dispatch in its Section 6 and Section 14, and `docs/design/tillr-idea-pipeline.md` settles [OQ-IP1](../../design/tillr-idea-pipeline.md#61-settled-decisions) on the `scouted-design@v1` workflow. The document’s opening status paragraph correctly says both amendments were made, so Section 7.3 contradicts it.

The child-session distinction in Section 3.4 still describes current behavior: `docs/design/tillr-graph-is-the-path.md` labels hoisting as a proposed, unbuilt change, and `tools/tillr-run/run/progress.go` records child sessions separately from workflow node runs.

**Status line**

The frontmatter `status: accepted` and `stage: BUILT` are accurate. The main status paragraph’s claim that all fifteen blocks landed is corroborated by `docs/tillr/design-status.md`, which says “Built 2026-11-18: all fifteen blocks landed.” The “nothing is owed; graduate…” next-step wording is still relevant as a documentation task: `tools/tillr-drain/README.md` is now a substantial workflow reference, but the design-status entry still lists this design as the shipped record and no graduation/replacement reference is named there. The stale Section 7.3 text and cap claims should be corrected to match the status paragraph.

**Open questions**

Section 7.2 says none remain. The three `OQ-IG` rows in Section 7.1 are explicitly recorded as settled rulings; their evidence is in those ledger rows. No still-open question is identified in this document. Later open questions in `docs/design/tillr-graph-is-the-path.md` belong to that separate, in-review proposal, not this design.

**Action**

correct facts — keep this as the relevant built-system design, but reconcile its present-tense baseline, graph-size limits and owed-amendments text with the implementation and its own later status notes.

### 22. observatory-redesign

[`docs/reference/observatory-redesign.md`](../../reference/observatory-redesign.md) *(graduated 2026-11-20 from the design evaluated below)*: **stale** at 2026-11-18 triage; recommended action: **correct facts**.

**Relevant**

Still relevant as the design and decision record for the built observatory, not as a queue of work to implement. `docs/tillr/design-status.md` lists it as “Built 2026-11-18: all ten slices landed” with “Nothing of its own” next; `tools/tillr-view/web/README.md` still says the page layout follows this design. The older three-pane [`docs/reference/observatory-design.md`](../../reference/observatory-design.md) (graduated 2026-11-20) is explicitly superseded in layout by this redesign in `docs/tillr/design-status.md`.

**Accurate**

The overall “built” claim is supported, but several present-tense descriptions and implementation details need correction:

- Under “5.8 Lanes — what is running right now,” the “The words” table says workflows and graphs are not built and the only node is a single agent node; the sample lane page also says “1 node (item graphs are not built).” The document's later “Ruled 2026-11-17” note itself says these statements are no longer true and supersedes the drawing by c2. The current tree has multi-node workflow tests `TestWorkflowRunsTwoNodesAndLandsOnce` and `TestWorkflowNextAttemptCountsItemsNotNodes` in `tools/tillr-drain/drain/workflow_loop_test.go`; `tools/tillr-view/web/src/components/LanePage.tsx` renders the recorded `lane.nodes`.
- In “What is not recorded today” under Section 5.8, “Child sessions” says tillr does not record sessions started by a node. That is stale: `tools/tillr-run/run/progress.go` records `run.progress` and child-session events; `TestProgressPartialResumeAndNestedChildren` in `tools/tillr-run/run/progress_internal_test.go` exercises both, and `TestWorkerRecordsClaimsAndChildTranscript` in `tools/tillr-view/worker_records_test.go` verifies the view API and child transcript. The UI in `LanePage.tsx` displays the recorded child sessions and distinguishes unrecorded from none.
- Section 4's Pass definition and G2 in Section 7.2 describe `drain_ticks` as recorded only when a pass gated a run, and G2 says `dispatches` has no tick identity. Current timing storage is `drain_passes` with `tick_id`; `tools/tillr-drain/drain/timing.go` defines and writes that table. `TestSnapshotCountsRecordedPassesAndKeepsUngatedPassesOutOfGateTrend` in `tools/tillr-view/heartbeat_internal_test.go` covers recorded ungated passes. The boundary is not “every empty pass”: `TestRepeatedEmptyPassesWriteNothing` in `tools/tillr-drain/drain/trigger_test.go` confirms unchanged empty passes remain unrecorded.
- G3's “no start or heartbeat record” is stale: `tools/tillr-drain/drain/pass_observation.go` creates and refreshes `drain_sessions`; `TestLoopLivenessUsesTheSessionDeadlineAndStop` in `tools/tillr-view/heartbeat_internal_test.go` exercises the view's liveness projection.
- G4's claims that end cause is absent and signals cannot be distinguished are stale: `tools/tillr-run/run/end_cause_test.go` has `TestTerminalEndCauses` and `TestReclaimedLeaseEndCause`, and the run schema records `end_cause` in `tools/tillr-run/run/schema.go`.
- G8's claim that the view reads `landing_attempts` and `candidates` is stale: `tools/tillr-view/landings.go` reads the `landings` table, and `TestLandingsHistory` in `tools/tillr-view/landings_internal_test.go` verifies that history.
- Section 2's “What is served” route inventory is incomplete as a description of the current server: `tools/tillr-view/serve.go`, in `routes()`, also registers `waiting`, `passes`, `landings`, `lanes`, performance, trace, and other routes. The redesign's later route/build-order sections describe some of these, but the earlier present-tense inventory does not.

I did not independently re-measure every visual token, performance percentile, or provenance rule in the design; the items above are the inaccuracies verified against current source and tests, not a claim that every remaining detail was audited.

**Status line**

`status: accepted` and `stage: BUILT` are accurate for implementation status: the current `docs/tillr/design-status.md` row says all ten slices landed, and its “What is changing right now” section records later observatory work. The body also correctly labels the operator-session validation “UNMEASURED.” The `next` field says “Nothing is owed” and then names graduation to a system reference; that reference is not present in the inspected `docs/reference`/`docs/design` inventory (`fd . docs/reference docs/design | rg 'observatory|tillr-view'` returned observatory documents only under `docs/design`). Clarify whether that graduation is optional, rather than leaving “nothing owed” beside an uncompleted next step.

**Open questions**

No active open question remains: the document says “Needs your ruling: None.” In its Decision Ledger, [OQ-OR1](../../reference/observatory-redesign.md#why-its-this-way) is explicitly ruled by the owner on 2026-11-15 and implemented by `Usage.tsx`; [OQ-OR2](../../reference/observatory-redesign.md#why-its-this-way) and [OQ-OR3](../../reference/observatory-redesign.md#why-its-this-way) are recorded as settled by the designer under the owner's requested principle and implemented by `ItemRunPages.tsx`, `Home.tsx`, and `ItemsPasses.tsx`. The ledger says the owner may overturn the latter decisions, so they remain revisable, but they are not currently open questions awaiting a ruling.

**Action**

**correct facts** — retain this relevant built design, but reconcile the explicitly superseded workflow/child-session text and the stale current-state/API/storage claims, and clarify whether system-reference graduation is still owed.

### 23. tillr-restart

[`docs/reference/tillr-restart.md`](../../reference/tillr-restart.md) *(graduated 2026-11-20 from the design evaluated below)*: **stale** at 2026-11-18 triage; recommended action: **correct facts**.

**Relevant**

Still relevant and wanted, and its core design is built rather than superseded: the checked-in `.tillr.toml` enables rolling of `tools/tillr*/` and green-pointer publishing, while `tools/tillr-drain/restart_live_test.go` (`TestRestartReplacesTheLoopInPlaceWithoutSignalingARun`), `tools/tillr-view/restart_test.go` (`TestViewRestartReplacesTheServerInPlaceAndTheHeaderReportsIt`), and `tools/tillr-run/pi_pause_test.go` (`TestPiPauseAtGapAndStraggler`) cover central parts of the described behavior. `docs/reference/` has no Tillr restart reference (the scoped `rg --files docs/reference | rg -i 'tillr.*restart|restart.*tillr'` printed “no matching Tillr restart reference under docs/reference”), so no reference document has superseded this design yet.

**Accurate**

The main built-status claim is supported by the implementation and tests, including `tools/tillr-drain/drain/build_restart.go` and `TestAMigrationPauseIsOnlyForAGapAdapterAndHasNoKillBound` in `tools/tillr-drain/drain/build_restart_test.go`. One statement needs scoping: “A harness that cannot stop at a gap is stopped where it stands” in “Pausing a session,” and the unqualified wording of ledger `D-RS9`, can be read to include build migration; in fact `migrateBuildRuns` requests a pause only when `adapterPauseKind(...) == run.PauseAtGap`, and its comment says a non-gap worker “finishes on the build it started under.” The later “The loop's own build migration has no straggler bound” section and the test named above give the actual exception. Also, “Needs your ruling: None” is contradicted by “Patch branches are not gated by green,” which explicitly leaves whether to narrow patch-branch publishing to the owner.

**Status line**

`status: accepted` and `stage: BUILT` are accurate: `.tillr.toml`, the restart and build-migration implementation, and the named drain/run/view tests show the described system is present. However, `next: "Nothing is owed; graduate the restart request and the settings file into a system reference."` is internally inconsistent: graduation is stated as the next task, and the scoped search of `docs/reference/` found no such reference. The dated status paragraph under the title is a historical reconciliation, not evidence of a current open-question-free design.

**Open questions**

There are no enumerated OQ identifiers in this document, but not every decision is closed: “Whether to narrow this is the owner's decision, not made here” under “Pushing what the proof has passed” remains an open and askable owner decision about patch branches published by the pre-push hook. `.tillr.toml` confirms the current choice is `push.what = "green"`; it does not answer that separate narrowing question. `D-RS10` is reflected by that config, though its quoted “assume that for now” means the choice is reversible rather than a permanent prohibition on revisiting it.

**Action**

**correct facts** — preserve the BUILT status, but reconcile the contradictory “nothing is owed”/graduation metadata, record the still-open patch-branch question, and scope the non-gap SIGTERM rule to pauses other than build migration.

### 24. tillr-claude-backend

[`docs/design/tillr-claude-backend.md`](../../design/tillr-claude-backend.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant and wanted, not superseded or iceboxed: `config/tillr/backend-policy.claude.json` and `config/tillr/backend-policy.mixed.json` still declare Claude-backed seats, while `tests/tillr-muse-backend.test.py` includes `test_claude_policy` and Claude adapter coverage. `docs/tillr/roadmap.md`'s “Route design work past a design seat that is out of quota” records actual `claude-designer` runs and the fallback seat, confirming use beyond the document's scratch experiment. The adapter contract and its operational detail remain useful in `tools/tillr-run/backends/claude/README.md`.

**Accurate**

The adapter, stream extraction, seat routing, and Claude harness-argument descriptions match `tools/tillr-run/backends/claude/README.md` and `tools/tillr-run/backends/claude/run-claude`; `tests/tillr-muse-backend.test.py::test_claude_argv` checks the Claude invocation and `tools/tillr-drain/seat_test.go::TestPlanStagesTheSeatListsHeadWithItsHarnessArgs` and `::TestPlanFailsAListedSeatOverAlongItsOwnList` cover staged arguments and seat failover. Quota admission is also implemented: `tools/tillr-drain/README.md` has “Backend quota admission,” and `tools/tillr-drain/quota_window_test.go` covers `TestPlanHoldsWhenAllSeatWindowsAreShut` and `TestLoopSkipsSpentWindowAcrossRestartAndReopens`.

The dated status paragraph is no longer accurate in two respects: it says no loop on the real trunk/store has dispatched to this backend and that starting such a loop remains the owner's step, but `docs/tillr/roadmap.md`'s “Route design work past a design seat that is out of quota” records thirteen actual `run.finish` records on `claude-designer`, each refused after the real weekly limit was reached, and identifies `config/tillr/backend-policy.mixed.json` as the policy in use. That evidence also means the statement that only a loop explicitly naming the Claude-first policy reaches Claude is stale: the cited mixed policy also routes design work to Claude. By contrast, real-store quota admission remains unconfigured in the tracked policies: `rg -n '"quota"|"reserve"' config/tillr/backend-policy*.json` printed no matches. Section 6's third build step remains deferred rather than built: `tools/tillr-run/backends/pi/run-pi` rejects unknown arguments, including the harness-argument separator that the Claude adapter supports.

**Status line**

The frontmatter `status: accepted` remains consistent with “Needs your ruling: nothing.” `stage: DESIGN` is still defensible because Section 6 explicitly leaves Pi-adapter `harness_args` for a later item, and `tools/tillr-run/backends/pi/run-pi` still refuses unknown arguments. However, the in-body dated status paragraph needs correction: the actual Claude real-store runs are recorded in `docs/tillr/roadmap.md` under “Route design work past a design seat that is out of quota.” The overview `docs/tillr/design-status.md`, “Status of every design,” independently calls the Claude backend built, so the primary adapter is not merely proposed.

**Open questions**

No formal open questions are listed; the document says “Needs your ruling: nothing,” and the Decision Ledger records implementation decisions rather than OQ items. The real-store dispatch is no longer open: the actual `claude-designer` refusals are recorded in `docs/tillr/roadmap.md` under “Route design work past a design seat that is out of quota.” Quota opt-in is still absent from the tracked backend-policy JSON files, as shown by the `rg` command above, but the tree does not establish that the owner has decided to opt in; do not report that implied next step as a resolved owner decision.

**Action**

**correct facts** — retain the useful accepted design and its deferred Pi-adapter work, but update its dated status and next-step prose to include the documented real-store Claude dispatches and distinguish built quota admission from the still-unconfigured policy opt-in.

### 25. gate-rework

[`docs/design/gate-rework.md`](../../design/gate-rework.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant as the reference for the implemented gate architecture, but not all of its policy remains current. `tools/tillr-gate/checkfiles.go` points readers to the design's “Section 6.4” for check declarations, `tools/tillr-gate/README.md` links its fingerprint and base sections, and `tools/tillr-drain/README.md` links its gate policy and green-pointer sections. The roadmap and `docs/tillr/design-status.md` both describe the design as built except for the D-GR12 upstream-drift probe (`docs/tillr/roadmap.md`, “The ten-second gate”; `docs/tillr/design-status.md`, “Gate rework”). That probe remains unbuilt: the roadmap says so, `item-gate-pinned-fork-inputs` is still at the top of `docs/tillr/queue-order.txt`, and `tools/fork-check.py` still assigns both `series` and `patch-branch-inv2` to `TIER_LANDING`.

**Accurate**

The broad implementation status is supported: `tools/tillr-gate/watchdog.go`, `tools/tillr-drain/drain/gate_policy.go`, `tools/tillr-drain/drain/trunk_release.go`, and `tools/tillr-drain/drain/lane_base.go` implement named design mechanisms; `config/tillr/gate-policy.json` has `auto_revert: true`; and `tools/gate/runner-first.test.py` covers the runner-first rule. The dated measurements in “Section 3. What exists today, measured” are explicitly historical, not claims about today's timings.

Statements needing correction:

- In “Section 6.6 The strategy setting, the heavy tier and the green pointer,” the policy table says this repository has `red_cap` of 20 landings and 60 minutes, and `lane_base` of `green`. Current `config/tillr/gate-policy.json` says `minutes: 0` and `lane_base: "tip"`. The graph design independently describes the cap's time bound as off and says every node starts on the latest trunk (“R8” and “What the drain does today, and what was proposed on 2026-11-18” in `docs/design/tillr-graph-is-the-path.md`).
- “Section 7. Checked against the landing principles” says an overrun carries a check forward once. “Section 6.5 Ten seconds is a failing test” and D-GR15 say nothing is carried at a budget; `tools/tillr-gate/run.go` rejects `--tree-overrun carry` with “stopping at a budget is not supported.” The “What this costs” text in “Section 9” also says automatic revert is off, whereas `config/tillr/gate-policy.json` sets it to `true` and the D-GR6 ledger row says it was enabled on 2026-11-18. “Section 10. Alternatives and risks” says every carry is recorded on its landing, which conflicts with the same D-GR15 rule and `tools/tillr-gate/run.go`.
- The “What” bullet in “Section 6.6” includes fast checks carried into the heavy run. That is no longer the implemented behavior: “Section 6.5” says “Nothing is carried at its budget,” and `tools/tillr-gate/run.go` refuses the carry mode.
- The Decision Ledger's former open questions are not all current decisions. The later graph design explicitly marks [OQ-GR1](../../design/gate-rework.md#OQ-GR1) (red cap) withdrawn, conditionally replaced by its still-open [OQ-GP7](../../design/tillr-graph-is-the-path.md#OQ-GP7), and [OQ-GR2](../../design/gate-rework.md#OQ-GR2) (green lane base) withdrawn because it conflicts with R8 (“12.2 Decisions against the ledger” in `docs/design/tillr-graph-is-the-path.md`; [OQ-GP7](../../design/tillr-graph-is-the-path.md#OQ-GP7) is in that document's “Open questions”). [OQ-GR3](../../design/gate-rework.md#OQ-GR3) remains settled and implemented: “A change to the gate's own runner lands only after the runner's suite passes” is the title of the relevant roadmap block, and `tools/gate/runner-first.test.py` covers it.

**Status line**

`status: accepted` and `stage: DECIDED` remain reasonable for the still-used gate reference: `docs/tillr/design-status.md` labels it `shipped`, and `docs/tillr/roadmap.md` says all build-order slices and the three extra behaviors landed while D-GR12 remains unbuilt. The prose status says it was reconciled against `dcb76fe41` on 2026-11-18; that is a dated historical statement, but it is no longer a sufficient current reconciliation because the policy-table and carried-check statements above conflict with the current config, implementation, and later graph design. Evidence: `config/tillr/gate-policy.json`, `tools/tillr-gate/run.go`, and “12.2 Decisions against the ledger” in `docs/design/tillr-graph-is-the-path.md`.

**Open questions**

The document has no separate “Open questions” section; its three OQ-GR entries are in the Decision Ledger. [OQ-GR1](../../design/gate-rework.md#OQ-GR1) and [OQ-GR2](../../design/gate-rework.md#OQ-GR2) are not open questions to preserve as settled choices: the later graph design marks them withdrawn, with [OQ-GP7](../../design/tillr-graph-is-the-path.md#OQ-GP7) still awaiting the owner's choice on red-trunk landing behavior (“12.2 Decisions against the ledger” and “Open questions” in `docs/design/tillr-graph-is-the-path.md`). [OQ-GR3](../../design/gate-rework.md#OQ-GR3) is answered and askable only as a possible overturn: `tools/gate/runner-first.test.py` and the roadmap block “A change to the gate's own runner lands only after the runner's suite passes” evidence the implemented rule. D-GR12 is not an open design question; its design is stated, but the probe is still unbuilt and queued behind `item-gate-pinned-fork-inputs` (`docs/tillr/roadmap.md`, “The ten-second gate”; `docs/tillr/queue-order.txt`).

**Action**

correct facts — retain this relevant implementation reference, but reconcile its stale policy values, carry/revert claims, and withdrawn ledger decisions with the current configuration and the later graph design.

### 26. observatory-actions

[`docs/reference/observatory-actions.md`](../../reference/observatory-actions.md) *(graduated 2026-11-20 from the design evaluated below)*: **stale** at 2026-11-18 triage; recommended action: **correct facts**.

**Relevant**

The design remains relevant and is not superseded: it records the implemented queue-order and idea-intake behavior, and current code and tests still implement those actions (`tools/tillr-item/item/place.go`, `tools/tillr-item/item/idea.go`, `tools/tillr-view/serveplace.go`, `tools/tillr-view/serveidea.go`; tests `TestPlaceHTTPRunsTheItemCommand` and `TestIdeasHTTPSavesWordsAndShowsIntake`). `docs/tillr/design-status.md` lists “Observatory actions” as built and shipped. The document's system-design rationale remains useful, though its baseline inventory in Section 2 is stale.

**Accurate**

The core design and its build record match the tree: the place and idea routes invoke the item command (`tools/tillr-view/serveplace.go`, `tools/tillr-view/serveidea.go`); order-record import and landing have implementation and tests (`tools/tillr-item/item/order_record.go`, `tools/tillr-drain/drain/order_record.go`, `TestSyncOrderRecordRebuildAndPrecedence`, `TestOrderRecordPassSettlesAndLandsOnlyTheRecord`); idea intake and its design turn are implemented (`tools/tillr-item/item/idea.go`, `tools/tillr-drain/drain/idea.go`, `TestIdeaBytesDefinitionRetryAndPlace`, `TestIdeaLandingImportsOutcomesAtItsPlace`).

Statements in Section 2 headed “What exists today” that no longer match the tree:

- “The token is shown as it is stored: the `rank` column of `tillr-view waiting` and of the Items page.” Faces now expose place numbers rather than stored keys: see `tools/tillr-view/waiting.go`, `tools/tillr-view/web/src/models/api.ts`, and `TestReadFacesCountPlacesBeforeAndAfterKeying` in `tools/tillr-view/order_places_test.go`.
- “Sync creates a declared item with no rank and never changes the rank of one it holds.” Sync now assigns keys to new and unkeyed in-order items; see `tools/tillr-item/item/sync.go` and `TestSyncPlacesNewAndUnparkedItemsLast` in `tools/tillr-item/item/sync_places_test.go`.
- “The server registers only `GET` routes and opens the store read-only.” `tools/tillr-view/serve.go` registers `POST /api/v1/items/{id}/place` and `POST /api/v1/ideas`; write requests are gated by `--write` and dispatched to the item utility in `tools/tillr-view/serveplace.go` and `tools/tillr-view/serveidea.go`. `TestPlaceHTTPReadOnlyAndValidation` and `TestIdeasHTTPSavesWordsAndShowsIntake` cover the write behavior. The server's own store handle remains read-only, as the design says.
- “The item tool has nine verbs: `apply`, `seed`, `sync`, `show`, `state`, `decision`, `mail`, `grant`, `verdict`. None sets a place and none takes in an idea.” `tools/tillr-item/main.go` now dispatches `place` and `idea` (among other newer faces); their implementations are `tools/tillr-item/place.go` and `tools/tillr-item/idea.go`. Tests include `TestPlaceDestinations` and `TestIdeaBytesDefinitionRetryAndPlace`.

The remaining Section 2 facts about sparse `items.rank` ordering, the item-block grammar, WAL locking, and the planner's open-question hold are consistent with the referenced implementation and tests. The document itself marks the usage evidence as unmeasured in its status paragraph; I found no evidence here that contradicts that limitation.

**Status line**

The frontmatter `stage: BUILT` and the status paragraph's claim that the eight build-order blocks landed are still true. The ledger in Section 10 marks each block built, the Section 10 order-record and idea-turn implementations are present in `tools/tillr-item/`, `tools/tillr-view/`, and `tools/tillr-drain/`, and their tests include `TestOrderRecordPassSettlesAndLandsOnlyTheRecord` and `TestIdeaLandingImportsOutcomesAtItsPlace`. `docs/tillr/design-status.md` independently records “Built 2026-11-18: all eight blocks landed” and “Nothing of its own.” The status paragraph's cited commit/tree reads are dated evidence, not claims about the current HEAD; they do not invalidate the built status.

**Open questions**

The “Needs your ruling” line says none and the “Open Questions” section says none; that remains accurate as a count of formal open questions. The same section explicitly preserves D-OA4, D-OA6, and D-OA8 as designer decisions the owner may overturn. Those are recorded decisions rather than currently unanswered questions; their entries remain in the Decision Ledger, and there is no evidence in the current design-status entry that they were overturned.

**Action**

correct facts — the design remains relevant and built, but its Section 2 present-tense baseline incorrectly says ranks are displayed, sync leaves new items unranked, the server has only GET routes, and the item utility lacks the place and idea faces now implemented.

### 27. tillr-drain-successor

[`docs/reference/tillr-drain-successor.md`](../../reference/tillr-drain-successor.md) *(graduated 2026-11-20 from the design evaluated below)*: **stale** at 2026-11-18 triage; recommended action: **correct facts**.

**Relevant**

Still relevant as the accepted design lineage for the drain's pass model: `tools/tillr-drain/README.md`, under “Pass observations and report identity,” links this document for the positions and transitions, and `docs/tillr/roadmap.md`, “7. The drain's own model,” says all five steps were built by 2026-11-18. It is partly superseded/extended by `docs/design/tillr-graph-is-the-path.md`, “5.7 Relation to the successor function,” which preserves the successor model's properties but splits its work between a policy function and a scheduler. It is not iceboxed.

**Accurate**

The high-level claim that the pass, generation identity, and replay-invariant behavior were implemented is supported by `tools/tillr-drain/drain/pass.go` (the `Loop.pass` implementation and its fixed-point comment), `tools/tillr-drain/drain/generation.go` (store-counted generations), and `tools/tillr-drain/drain/trigger_test.go` tests `TestReplayIsInvariantToClockRateAndTriggerSuppression` and `TestRepeatedEmptyPassesWriteNothing`. The roadmap's “7. The drain's own model” independently records all five migration steps as built.

These specific claims in the document do not match the current tree or contradict other sections:

- The opening of “6. Decision ledger” says every Built cell is “— today because nothing in this document is built,” while the same table marks D-DS1 through D-DS16 with ✅ and the roadmap says all five migration steps landed. This statement is false.
- The “Cost” paragraph and “2.5 One pass” describe a surviving serial judge and a serial bound. “4.3 S3” instead says “Gone: the serial judge section,” and `Loop.Verify` in `tools/tillr-drain/drain/loop.go` delegates directly to `verify` without a judge-wide lock. The serial-judge statements are stale.
- The “Shape” paragraph lists only `dispatch`, `settle`, `expire`, and `judge`. The current reference `tools/tillr-drain/README.md`, “Pass observations and report identity,” lists `dispatch`, `retry`, `settle`, and `judge` as the plan's transitions; `tools/tillr-drain/drain/plan.go` also assigns `ReasonRetry` to retry decisions. The transition inventory needs reconciliation with the current model.
- The “Cost” paragraph says `stamp` is deleted, but `stamp` remains in `tools/tillr-drain/drain/plan.go` and is used by `tools/tillr-drain/drain/trunk.go` for rolling-run identity and by `tools/tillr-drain/drain/resume.go` for resume boundary correlation. “4.5 S5” already acknowledges these surviving callers, so the unqualified summary overstates the deletion.

The document's “UNMEASURED” qualification is specifically about measurements against a live night. The replay and empty-fixed-point tests above provide test evidence, but do not establish the stated live-night lane-idle measurement.

**Status line**

The frontmatter `stage: BUILT` is true: `docs/tillr/roadmap.md`, “7. The drain's own model,” says all five steps landed, and the current pass/generation code and replay/fixed-point tests provide implementation evidence. `status: accepted` is consistent with a settled design. The body status paragraph is explicitly dated and anchored to older trees (`dcb76fe41` and `5758a937a9`); it is historical, not a current-tree reconciliation. Its statement that all five slices landed remains supported, but the document's unqualified ledger and design details need updating as listed above.

**Open questions**

No open questions are recorded: the document says “Needs your ruling: None,” its decision ledger contains D-DS1 through D-DS16 rather than OQ entries, and frontmatter `next` says “Nothing is owed.” Those claims remain consistent with `docs/tillr/roadmap.md`, “7. The drain's own model,” which treats the design as built and records no pending successor work. There is no currently stated question here to answer or keep open.

**Action**

correct facts — retain this still-referenced built design as lineage, but reconcile its ledger, transition inventory, serial-judge claims, and `stamp` summary with the current implementation and README.

### 28. tillr-board-reshape

`docs/design/tillr-board-reshape.md` (retired): **obsolete**; recommended action: **correct facts**.

**Relevant**

The document is still useful as the historical rationale for preserving the old board as evidence and re-authoring only work verified against the current tree. The preservation outcome exists at `docs/archive/board-2026-11-12/README.md` (“Taken 2026-11-11” and the committed `board-content.json` with 623 items); the first 56-item disposition list and five-item concordance exist in `docs/design/tillr-seed-triage.md` (“The list” and “Concordance”). It is no longer active design work: `docs/tillr/design-status.md` classifies Board reshape as “Historical: done” and Seed triage as a frozen historical file.

**Accurate**

The archived-board counts and the existence of the preserved content dump are consistent with `docs/archive/board-2026-11-12/README.md`, which identifies the committed dump and its 623 items. The statement in Section 1 that criterion text is not yet frozen is stale: `tools/tillr-item/item/apply.go` implements `carryCriteria`, and `tools/tillr-item/criterion_state_test.go` has `TestAnEditLeavesAFrozenCriterionFrozen`; `tools/tillr-item/terminal_state_test.go` also asserts the `criterion-frozen` refusal for changed criterion text. The claim that the first triage list is still owed is stale: `docs/design/tillr-seed-triage.md` contains the 56 dispositions and the five-entry archived-id-to-item concordance. Section 6's separate obligation to reconcile the five question texts that did not match their documents is not demonstrated by that triage list; I cannot tell from the inspected documents whether that specific check was completed.

**Status line**

No. The frontmatter says `status: in-review`, `stage: DESIGN`, and its `next` says the first triage list is still owed, while `docs/tillr/design-status.md` explicitly calls Board reshape “Historical: done”; the list is now present in `docs/design/tillr-seed-triage.md`. The document's 2026-11-12 inline status describes its original state, but does not make the current frontmatter/next accurate.

**Open questions**

The document's Open Questions section lists its second question as ruled A; the Decision Ledger records both board-reshape questions as settled on 2026-11-12, so neither design decision remains open. A separate execution point remains unclear: `docs/design/tillr-seed-triage.md` still says the owner rules `KEEP`/`DROP` per row under that second ruling (option A) in its `next` field and frames the list as awaiting approval. I found no inspected record confirming that per-row approval occurred. Also, Section 6 says the five non-verbatim question/document matches still need checking, and I found no evidence in the triage list that this particular reconciliation was done; whether it remains outstanding cannot be established from these sources.

**Action**

correct facts — the current status and next-step claims are stale, and the criterion-freeze statement is contradicted by the implementation and regression test, while the outstanding approval/reconciliation evidence should be stated without assuming it happened.

### 29. tillr-today

`docs/design/tillr-today.md`: **obsolete**; recommended action: **keep as is**.

**Relevant**

This is still useful as a dated record of the 2026-11-12 plan and its assumptions, but not as a current plan: the required supervised-drain milestone was overtaken by the unattended loop and later work. `docs/tillr/design-status.md`, under “Superseded and historical records,” lists “tillr today” as superseded and not shipped; `docs/design/tillr-drain-acceptance-2026-11-13.md`, “What ran,” records the first unattended two-lane run; and `docs/tillr/roadmap.md` records subsequent build items. The acceptance record says one candidate landed and the other timed out, so the plan’s broad “one real drain that lands a change” goal did occur, but its stricter Section 1 definition (two items each carried through landing and verdict) did not: acceptance “The six predicates, as measured” reports only one item carried the whole way and predicate 4 failed.

**Accurate**

The document clearly dates its tree snapshot to 2026-11-12, so its descriptions of then-current conditions are historical rather than claims about today. As current statements, these no longer match the tree:

- Section 3 says frozen criterion *text* was not enforced and rewording or omission could succeed. It is now enforced: `tools/tillr-item/criterion_state_test.go` contains `TestAFrozenCriterionsTextCannotBeRewritten`, `TestAFrozenCriterionCannotBeDropped`, and `TestACriterionMayBeAppendedAfterAFrozenOne`.
- Sections 1 and 4 describe the `question-open` hold as unbuilt and performed by the driver. It is implemented and tested: `tools/tillr-drain/main_test.go::TestPlanHoldsAnItemWhoseDesignDocumentCarriesAnOpenQuestion`; numbered questions are covered by `tools/tillr-item/questions_test.go::TestShowReadsNumberedVantageQuestion`.
- Sections 2–5 and 6 describe manual lane staging and composition, and defer the subprocess/supervision loop and unattended dispatch. The current design-status index describes the drain as staging, dispatching, judging and landing; the current loop’s staged dispatch is exercised by `tools/tillr-drain/stage_test.go::TestATickStagesBeforeItDispatches`, and multi-tick operation by `tools/tillr-drain/loop_test.go::TestTwoConsecutiveTicksDispatchAndRecord`.
- Section 1’s “Ember stopped answering” paragraph records the Muse outage and a contemporaneous cutover. The current backend configuration and selection are not established by that dated paragraph; this finding does not infer a present provider from it. `docs/tillr/design-status.md` currently labels the Muse adapter “built and dormant” and separately describes the Claude backend as built.

The document itself distinguishes its historical claims: its “Status” paragraph says it was written against `1085c3f990`, and “Precedence” says the tree wins on fact. The goal-completion banner is broader than its own formal measurement; the acceptance evidence does not satisfy all five Section 1 conditions.

**Status line**

The stage/status claim is true for archival use: frontmatter says `stage: SUPERSEDED`, the body opens with “Retired 2026-11-16,” and `docs/tillr/design-status.md` lists it as superseded and not shipped. `status: accepted` describes the accepted historical plan, not a live stage; the body and the index make that distinction clear. The separate claim that the fully specified two-item real-drain definition was met is not supported: `docs/design/tillr-drain-acceptance-2026-11-13.md`, “The six predicates, as measured,” says only one item completed the full path and marks predicate 4 fail.

**Open questions**

“Open questions: None” remains true for this plan’s own two questions: the two `tillr-today` questions are explicitly ruled in its “Decision Ledger,” and the body says both were compacted there. They are not still askable as unresolved decisions in this record. The owner’s 2026-11-12 ruling on the first is “B with A first”; the second is “A.” The document separately says later questions should be newly filed, not silently treated as gaps.

**Action**

keep as is — it is already marked superseded, retired, and not shipped, and remains valuable as a dated record of the plan and its divergence from the eventual measured run, so updating it would blur that history.

### 30. observatory-design

[`docs/reference/observatory-design.md`](../../reference/observatory-design.md) *(graduated 2026-11-20 from the design evaluated below)*: **stale** at 2026-11-18 triage; recommended action: **correct facts**.

**Relevant**

The truthfulness contract remains wanted: [`docs/reference/observatory-redesign.md`](../../reference/observatory-redesign.md#principles-and-invariants), principle P3, explicitly keeps the earlier design's five provenance states, and the current implementation includes shared state/refusal components (`tools/tillr-view/web/src/components/StateGlyph.tsx`, `RefusalState.tsx`). The earlier three-pane layout and one-item navigation are superseded by [`docs/reference/observatory-redesign.md`](../../reference/observatory-redesign.md#entities-and-urls), which documents the built Home, Items, Passes, Run, Trunk, and other entity pages. The source design itself marks the operator-session evidence `UNMEASURED`; the graduated reference's status does too, so code/tests establish implementation, not measured operator outcomes.

**Accurate**

The dated 2026-11-13 API and implementation inventory is historical, but several present-tense claims need correction:

- The opening summary calls the pause control, archive, metadata/link pane, now band, state glyph, refusal component, and CLI counts “unbuilt,” although these are present as `ObservatoryHeaderBar.tsx`, `FinishedBand.tsx`, `DocumentPane.tsx`, `NowBand.tsx`, `StateGlyph.tsx`, `RefusalState.tsx`, and `tools/tillr-view/status.go`. Tests include `tools/tillr-view/web/tests/state-glyph.test.tsx`, `loop-band.test.tsx`, `document-pane.test.tsx`, and `observatory.test.tsx`; the count-parity subtest in `tools/tillr-view/serve_internal_test.go` is named `status_and_the_snapshot_answer_the_same_counts`. The claim “Everything else is shipped and measured” also overstates the evidence: both this document and [`docs/reference/observatory-redesign.md`](../../reference/observatory-redesign.md) say no timed operator session is recorded.
- Section 8.3 says the question parser reads dash bullets, fails on numbered questions, and reports zero for `item-drain-2a`. The current implementation gets question state through the Vantage index (`tools/tillr-view/questions.go`, `vantagecache.go`); `tools/tillr-view/serve_internal_test.go`, `TestTheDocumentRouteAnswersTheDesignDocument`, asserts the indexed count and `questions-ok`, while `TestTheDocumentRouteReportsUnknownQuestions` checks the unavailable-index case. The blanket claim that the current parser is known wrong is stale.
- Section 10 says the per-run boundary record is absent and only a tick-level bracket exists. That is contradicted by `tools/tillr-drain/events_test.go`, `TestTheLoopRecordsItsJudgmentsAsEvents` and `TestABoundaryViolationIsRecordedAsAnEvent`, and by `tools/tillr-drain/README.md`'s run-boundary recording description; the graduated observatory design also explicitly noted the run-scoped `boundary.check` event.
- Section 11 says “Six rows say `—`,” and concludes the stage is DECIDED rather than BUILT. The same document's frontmatter says `stage: BUILT`, its status introduction says every ledger row is built or superseded, and the ledger entries shown carry built/superseded outcomes rather than six unbuilt rows. That paragraph is internally stale.
- Section 6 and the [`OQ-OBD4`](../../reference/observatory-design.md#why-its-this-way) ledger row say the board shows no spend at all and leave how to show it unanswered. [`docs/reference/observatory-redesign.md`](../../reference/observatory-redesign.md#usage-without-dollar-prices) and [`OQ-OR1`](../../reference/observatory-redesign.md#why-its-this-way) settle that the UI shows token/model/time usage but no dollar price; `tools/tillr-view/web/src/components/Usage.tsx` implements the usage display. The old no-price constraint remains, but the no-usage wording and open-question claim do not.

**Status line**

The frontmatter `status: accepted` and `stage: BUILT` are broadly true for the original design's implemented scope, while [`docs/reference/observatory-redesign.md`](../../reference/observatory-redesign.md) explicitly replaces its pane layout. The status prose needs factual repair: its unbuilt list and the Section 11 “DECIDED” explanation conflict with the current code, tests, and its own BUILT metadata. The document's `UNMEASURED` caveat remains accurate; neither document records a timed operator session.

**Open questions**

No `OQ-OBD` question remains open in the source design: Section 11 records [OQ-OBD1](../../reference/observatory-design.md#why-its-this-way), 2, 3, 4, 5, and 6 as ruled on 2026-11-13 and ties each to a decision/body section. [OQ-OBD4](../../reference/observatory-design.md#why-its-this-way) was later reopened and resolved as [OQ-OR1](../../reference/observatory-redesign.md#why-its-this-way) in [`docs/reference/observatory-redesign.md`](../../reference/observatory-redesign.md#usage-without-dollar-prices): usage is shown, pricing stays iceboxed. The redesign also replaces the earlier pane/layout navigation decisions (Sections 2, 4, and 5) and builds document/question pages; those earlier rulings are historical, not currently open questions. The source document's “Needs your ruling: None” is accurate.

**Action**

correct facts — retain this as the source of the truthfulness rules that the redesign explicitly keeps, but fix the stale implementation inventory, boundary claim, parser description, and contradictory ledger status.

### 31. mq-retirement

[`docs/design/mq-retirement.md`](../../design/mq-retirement.md): **stale**; recommended action: **correct facts**.

**Relevant**

The core retirement is built, but the document remains useful as a closeout record until its uncompleted cleanup and stale status are reconciled. The deletion is present in history as `7af65bf66 refactor(land): delete the merge queue, because it was never a queue`; `tools/mq/`, `yolo-packs/merge-queue/`, and `bin/land` are absent, while `tools/tillr-land/` remains. The manual replacement is implemented by `tools/tillr-land/land/one.go` and `TestOneGatesAndLandsABranch` in `tools/tillr-land/main_test.go` (the module-scoped test passed). However, `.workboard.toml` still exists, and `tools/pipeline-evidence/sources.py` still contains the legacy `mq` and `land-queue` readers. `docs/design/landing-principles.md` [§1](../../design/landing-principles.md#1-a-refusal-must-be-a-verdict) still points to this document as the retirement ruling, so I found no evidence that another document supersedes it.

**Accurate**

Most implementation claims about removing the old queue and adding `tillr-land one` match the tree: the three legacy paths are absent, `tools/tillr-land/land/one.go` exists, and `TestOneGatesAndLandsABranch` passes (`cd tools/tillr-land && go test ./... -run '^TestOneGatesAndLandsABranch$'`). The pipeline-evidence decision to switch tests to fixtures is also reflected in `tests/pipeline-evidence.test.py`: it writes synthetic `mq` evidence records and no longer invokes `bin/land`; the suite ended `12 passed, 0 failed, 0 skipped`.

Current claims that do not match the tree:

1. Frontmatter `next` and [§2.1](../../design/mq-retirement.md#21-ordered-removal-sequence-and-point-of-no-return) Step 7 say to delete `.workboard.toml`, and [§6](../../design/mq-retirement.md#6-repository-configuration-after-workboardtoml) describes its fate after deletion, but the file is still present. `tests/rcm-deployment.test.py` still lists it as repository-only, and `docs/reference/landing-system.md` still describes it as committed gate configuration.
2. The header still calls the document “in-review” and says the four questions, authorship normalization, and deletion sequence remain future work, although the questions are marked settled, the lander test exists, and the principal deletion is recorded in commit `7af65bf66`. The deletion plan is not wholly complete because `.workboard.toml` remains.
3. The [OQ-MQR3](../../design/mq-retirement.md#9-open-questions) decision correctly records that the tests use fixtures, but the replacement of the old evidence readers is still not implemented: `tools/pipeline-evidence/sources.py` retains `mq` and `land-queue` readers. I did not establish from the repository files whether its stated follow-up is still a live queued item.

**Status line**

The frontmatter `status: in-review`, `stage: DESIGN`, and `next` field are stale as a description of the work's current state: the central retirement landed, and all four OQs have `_Settled_` entries. The remaining `.workboard.toml` deletion and legacy evidence-reader disposition mean the document cannot accurately be marked wholly built without first recording what happened to those leftovers. Evidence: `git log --all --diff-filter=D --format='%h %s' -- tools/mq bin/land yolo-packs/merge-queue` printed `7af65bf66 refactor(land): delete the merge queue, because it was never a queue`; `test -e .workboard.toml` succeeds; `tools/pipeline-evidence/sources.py` still defines `mq` and `land` readers.

**Open questions**

None of [OQ-MQR1](../../design/mq-retirement.md#9-open-questions) through [OQ-MQR4](../../design/mq-retirement.md#9-open-questions) remains an open design question in the document: each has a `_Settled_` answer, so the corresponding lingering `_Leaning_` and empty `Answer` blocks are inconsistent and should not be treated as unresolved. [OQ-MQR1](../../design/mq-retirement.md#9-open-questions) is answered by `tools/tillr-land/land/one.go` and `TestOneGatesAndLandsABranch`; [OQ-MQR2](../../design/mq-retirement.md#9-open-questions) by `.tillr.toml` and [`docs/reference/tillr-restart.md`](../../reference/tillr-restart.md#the-settings-file) (“The settings file”); [OQ-MQR3](../../design/mq-retirement.md#9-open-questions) by the fixture-backed cases in `tests/pipeline-evidence.test.py` (the separate reader migration remains unbuilt in `tools/pipeline-evidence/sources.py`); and [OQ-MQR4](../../design/mq-retirement.md#9-open-questions) by `Trunk.Squash` / `LandingIdentity` in `tools/tillr-land/land/trunk.go` and identity tests `TestALandingRefusesARepositoryThatConfiguresNoIdentity` and `TestALandingRefusesARepositoryLocalIdentityOverride` in `tools/tillr-land/main_test.go`. Whether a current follow-up item exists for [OQ-MQR3](../../design/mq-retirement.md#9-open-questions) cannot be confirmed from these checked-in files, so I do not infer that it does.

**Action**

**correct facts** — reconcile the stale in-review/next and leaning/empty-answer text with the landed retirement, and explicitly disposition the still-present `.workboard.toml` and legacy pipeline-evidence readers before calling the design fully built.

### 32. scheduled-tasks

[`docs/design/scheduled-tasks.md`](../../design/scheduled-tasks.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant, but deferred rather than active: `docs/tillr/roadmap.md` under “Older designs” says Scheduled tasks is “fully settled and unbuilt,” says Tillr has no due date or interval on an item, and identifies it as the substrate of the goal monitor in “Icebox.” The roadmap does not name a superseding design. The weekly fork-roll-forward remains described as designed and unbuilt in `docs/pi/roadmap.md` under “Fork upkeep.”

**Accurate**

The central “Nothing built” claim remains accurate for Tillr scheduling: `docs/tillr/roadmap.md` under “Older designs” explicitly says no item due date or interval exists; the item table in `tools/tillr/store/item_pilot_test.go` contains `item_id`, `title`, `body_ref`, `state`, `design_doc`, and `updated_at`, but no schedule fields. However, the design’s implementation grounding is stale: its opening “Status” names `tools/board-cli/internal/board/` and its “Where the code lives, by symbol” section specifies `SchemaVersion`, `readyItems`, `admitItem`, `RecordVerdict`, and other board-cli seams, while `tools/board-cli` is absent from this tree and current Tillr scheduling is explicitly unbuilt in the roadmap. Those are not current Tillr implementation locations/contracts and need re-grounding before build.

Two other facts are stale or internally inconsistent. In [§7](../../design/scheduled-tasks.md#7-the-first-user-the-weekly-evergreen-roll-forward), “Two triggers exist for this work” says which trigger survives is still [OQ-ST1](../../design/scheduled-tasks.md#12-open-questions), but [§12](../../design/scheduled-tasks.md#12-open-questions)’s [OQ-ST1](../../design/scheduled-tasks.md#12-open-questions) `_Settled_` paragraph chooses C (both, with calendar as floor), and `docs/tillr/roadmap.md` says the design is fully settled. In [§5.4](../../design/scheduled-tasks.md#54-failure-does-not-advance-the-schedule) and [OQ-ST2](../../design/scheduled-tasks.md#12-open-questions), failure is described as remaining due and being retried under the existing machinery; the current rule in `docs/design/tillr-convergence.md` [§3.2](../../design/tillr-convergence.md#32-every-hold-names-a-mover-that-can-act) (“A changed input re-admits the item once, by itself”) is input-keyed, not a time-based retry. `docs/tillr/decision-provenance-review.md` under the Scheduled tasks entry also says [OQ-ST2](../../design/scheduled-tasks.md#12-open-questions) should drop “re-dispatched once on a changed input,” so this part of the rationale itself has been flagged for correction. The report `docs/tillr/roadmap.md` supports the no-schedule finding; it does not validate the design’s proposed API or migration details against the current Tillr architecture.

**Status line**

The metadata `status: in-review` and `next:` instruction to rule [OQ-ST1](../../design/scheduled-tasks.md#12-open-questions) are stale: [OQ-ST1](../../design/scheduled-tasks.md#12-open-questions) has a settled C answer in [§12](../../design/scheduled-tasks.md#12-open-questions) and the roadmap calls the design fully settled. The bold status paragraph still correctly says no scheduling implementation is built, but its claim that it is a buildable design reconciled against `tools/board-cli` is no longer current because that implementation tree is absent and Tillr is the active queue architecture (`docs/tillr/roadmap.md`, “Older designs”). [OQ-ST3](../../design/scheduled-tasks.md#12-open-questions) remains an unresolved verification prerequisite, so “fully settled” should not be read as evidence that the dropped-patch experiment was performed.

**Open questions**

- **[OQ-ST1](../../design/scheduled-tasks.md#12-open-questions):** Already answered in [§12](../../design/scheduled-tasks.md#12-open-questions)’s `_Settled_` paragraph: C, both triggers, calendar as the floor. The `Answer` block is empty and [§7](../../design/scheduled-tasks.md#7-the-first-user-the-weekly-evergreen-roll-forward)/metadata still treat it as open; correct those contradictory remnants.
- **[OQ-ST2](../../design/scheduled-tasks.md#12-open-questions):** The decision is stated as settled A in [§12](../../design/scheduled-tasks.md#12-open-questions), but its failure/re-admission rationale is outdated against `docs/design/tillr-convergence.md` [§3.2](../../design/tillr-convergence.md#32-every-hold-names-a-mover-that-can-act) and is explicitly called out in `docs/tillr/decision-provenance-review.md` under Scheduled tasks. The settled policy remains askable for owner reversal, but the design must restate it using the current input-change rule rather than the old retry description.
- **[OQ-ST3](../../design/scheduled-tasks.md#12-open-questions):** Still open and askable. [§12](../../design/scheduled-tasks.md#12-open-questions) settles the next action as “Demonstrate it,” not whether the gap is real. Searching `docs` and `tools` for dropped-commit/rebase experiment wording (excluding the two design descriptions) found no record of the requested demonstration; the command returned only unrelated occurrences, such as yolo patch replay and general landing-rebase material. No result of the experiment is documented here.
- **[OQ-ST4](../../design/scheduled-tasks.md#12-open-questions):** Already answered in [§12](../../design/scheduled-tasks.md#12-open-questions)’s `_Settled_` paragraph: A, seven days. Its `Answer` block is empty, and [§12](../../design/scheduled-tasks.md#12-open-questions) retains a contradictory `_Leaning_` paragraph; those are document inconsistencies, not evidence that the decision is still open.

**Action**

**correct facts** — scheduling remains a deferred, unbuilt substrate, but the design’s status/next fields, contradictory OQ remnants, failure-policy account, and board-cli implementation grounding no longer match the current roadmap and Tillr architecture.

### 33. tillr-muse-backend

[`docs/reference/tillr-muse-backend.md`](../../reference/tillr-muse-backend.md) *(graduated 2026-11-20 from the design evaluated below)*: **stale** at 2026-11-18 triage; recommended action: **correct facts**.

**Relevant**

The shared adapter contract remains relevant and is implemented: `tools/tillr-run/backends/claude/README.md` explicitly points to this design for the contract every backend shares, while `tools/tillr-run/run/exec.go` carries the required `backend` field. The Muse backend itself is not currently wanted for dispatch: the Icebox entry in `docs/tillr/roadmap.md` says the owner is not subscribing to a Meta plan, and `config/tillr/backend-policy.json` declares Muse but sets `preference` to only `pi` and says Muse is not dispatched. Thus the document is still a useful shared-contract reference, but its Muse-specific plan is iceboxed rather than active.

**Accurate**

The original MM-1S/MM-1A implementation claim is supported: `docs/design/tillr-coverage-2026-11-13.md`, section “2.6 tillr-muse-backend.md,” says both were built and names the adapter tree, policy, and test; `tests/tillr-muse-backend.test.py` still exists. The request/record backend contract also remains in `tools/tillr-run/run/exec.go` and is covered by `TestTheBackendFieldTravelsFromRequestToRecordAndRow` in `tools/tillr-run/run/conformance_test.go`.

Statements that no longer match the current tree or plan:

- The frontmatter `next` and the opening status paragraph say the first live Ember run still owes the canary. The coverage audit’s “2.6” row says the canary now rides the first live run of whichever backend answers: Pi exercises the snapshot half, and the Muse-specific half returns when Muse is switched back on. The current policy and roadmap Icebox entry keep Muse out of dispatch.
- “The staged path,” Sections 2 and 8, still present MM-2, MM-3, the cold-prefix measurement, and Stage 4 as future work. The coverage audit’s “2.6” rows mark MM-2, MM-3, and the D-MM7 cold-prefix measurement deleted; its chunking-plan rows mark DRAIN-2A/DRAIN-2B covered and per-item selection built. `tools/tillr-drain/drain/policy.go` defines preference, seats, and overrides, and the current policy uses those dispatch choices. The document should distinguish this completed generic machinery from the Muse-specific stages that were removed.
- Section 1 / D-MM9 and Section 2 say any non-empty boundary delta stops the tick (including ref movement). That is broader than the current boundary contract: comments in `tools/tillr-drain/drain/boundary.go` say ordinary refs and worktree registrations are observed rather than convictions; `CompareRunObserved` returns separate `violations` and `observed` results. `TestOrdinaryGitUseDuringARunNeverConvictsIt` and `TestAWorktreeOutsideTheLoopsReservedPlacesIsObserved` in the drain boundary tests cover this distinction. Therefore the ledger's claim that `StopHostBoundaryViolation` makes every non-empty snapshot difference stop the tick, and the surrounding ref-restoration procedure, no longer describes current behavior.
- The failover prose in Section 2 and D-MM11 says preference is Muse then Pi and limits each item to one failover. `config/tillr/backend-policy.json` has `default_backend: pi` and `preference: ["pi"]`, with Muse excluded from dispatch. Also, `tools/tillr-drain/drain/loop.go` walks while the attempt remains infrastructure-class, and `TestLegacyFailoverBudgetIsIgnored` in `tools/tillr-drain/drain/policy_test.go` confirms a legacy failover budget is ignored. The stated Muse-first order and one-failover cap are not current policy.
- Section 8 says the recorded refusal to schedule Claude is not lifted. Claude now has an adapter in `tools/tillr-run/backends/claude/`, a Claude-first policy in `config/tillr/backend-policy.claude.json`, and its own design in `docs/design/tillr-claude-backend.md`; `docs/tillr/roadmap.md` says the Claude backend is built and has had a hand run. The old refusal is superseded.

**Status line**

`status: accepted` and `stage: BUILT` are partly supported: the coverage audit records MM-1S/MM-1A built, and the contract and adapters remain in the tree. They are misleading if read as saying the whole staged Muse path is active or built: the current roadmap explicitly iceboxes Muse, and the audit deletes MM-2/MM-3 and the cold-prefix measurement. The body acknowledges dormancy, but the metadata should align with the roadmap’s current status.

**Open questions**

The document's “Open questions” section says none; that remains true as a live decision state. Historical OQs are answered in the Decision Ledger: D-MM4 through D-MM6 and D-MM8 record their rulings; D-MM9 records the boundary decision; and D-MM7's measurement was deleted with Stage 3 rather than left pending, as recorded in `docs/design/tillr-coverage-2026-11-13.md` section “2.6.” Reopening Muse would create new implementation questions, but the current roadmap makes that contingent on picking the iceboxed work back up, not an unanswered question in this design today.

**Action**

correct facts — preserve the still-used shared adapter contract, but reconcile the Muse icebox status, removed stages, boundary semantics, current failover policy, and superseded Claude scheduling statement with the current roadmap and implementation.

### 34. tillr-goal-monitor

[`docs/design/tillr-goal-monitor.md`](../../design/tillr-goal-monitor.md): **current**; recommended action: **keep as is**.

**Relevant**

Still wanted, but explicitly not now: `docs/tillr/roadmap.md`, heading “Icebox,” says “Wanted, not now, by the owner's call,” and its “The goal monitor” entry records the owner's 2026-11-17 request to icebox the work until there is a use. It is not superseded or abandoned. The generic graph substrate is built, but the monitor remains unbuilt: [`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md) documents the built workflow runtime; `docs/tillr/roadmap.md`, “Scheduled tasks” and “The goal monitor,” says tillr has no due date or interval and none of the goal-monitor slices is in the queue.

**Accurate**

No material mismatch found in the status-bearing claims checked. `docs/design/tillr-goal-monitor.md`, [§4.2](../../design/tillr-goal-monitor.md#42-the-invariant-and-what-satisfies-it) “The invariant, and what satisfies it,” says the loop waits by default and polls at least every 100 milliseconds; `docs/tillr/roadmap.md`, “The loop waits when nothing is dispatchable and exiting is the option,” records the 2026-11-15 ruling and landing `82e1ba7196`. The design's [§10](../../design/tillr-goal-monitor.md#10-the-three-downstream-slices) “The three downstream slices” correctly distinguishes the unbuilt clock/check-in/retirement work from the built graph engine: `config/tillr/workflows/` contains existing workflows, and `tools/tillr-drain/drain/workflow_test.go` and `tools/tillr-drain/drain/workflow.go` exist. Negative check: `rg -n --glob '!docs/**' --glob '!yolo-packs/**' 'due_at|recur_interval_ms|not-due' tools config tests` found no scheduled-item implementation (only unrelated steering-text and store checkpoint “not-due” matches); `rg --files config/tillr/workflows` listed six workflows and no `goal-checkin@v1.json`. `docs/design/queue-persistence-ruling.md`, passage “5 pre-cut slices on `in-review` drafts,” describes the goal-monitor slices as drafts held on unanswered questions, not admitted queue work. Thus “Nothing is built” in the status paragraph is accurate for the monitor's three slices, not a claim that its graph-engine substrate is absent.

**Status line**

True: the frontmatter says `status: in-review` and `stage: DESIGN`; the body status records the owner's 2026-11-17 icebox decision. `docs/tillr/roadmap.md`, “The goal monitor” under “Icebox,” and `docs/tillr/design-status.md`, the “Goal monitor” row, independently show it parked with nothing due while parked. The design status also correctly identifies the graph engine and default-wait behavior as built while keeping the monitor slices unbuilt ([`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md); `docs/tillr/roadmap.md`, “The loop waits when nothing is dispatchable and exiting is the option”).

**Open questions**

Both remain open, and neither is currently askable while the design is iceboxed. `docs/design/tillr-goal-monitor.md`, “[OQ-GM1](../../design/tillr-goal-monitor.md#OQ-GM1): What should the first scheduled check-in watch, and how often?,” has an empty answer and is blocked until a use for a scheduled check-in exists. “[OQ-GM2](../../design/tillr-goal-monitor.md#OQ-GM2): Should something outside the loop page you when a check-in is overdue?” also has an empty answer and says it is blocked until the design is picked up and a check-in has run. `docs/tillr/roadmap.md`, “The goal monitor” under “Icebox,” independently says both are unanswered. [OQ-GM2](../../design/tillr-goal-monitor.md#OQ-GM2)'s stated leaning is not an answer.

**Action**

keep as is — the roadmap and current code agree that the goal monitor is wanted but iceboxed, its monitor-specific slices and questions remain pending, and the document distinguishes those from the built graph substrate.

### 35. escalation-ladder

[`docs/design/escalation-ladder.md`](../../design/escalation-ladder.md): **stale**; recommended action: **correct facts**.

**Relevant**

The core concern remains relevant: a worker must surface a real conflict instead of silently changing the rule it was asked to follow. `docs/design/agent-runtime.md`, under “The escalation ladder, as ruled,” still says an agent finding a conflict records and routes it rather than inventing an escalation; `docs/design/tillr-judge-and-lifecycle.md`, under “What a worker may end with,” implements a closed set of named obstacles and routes. `docs/design/landing-principles.md`, “6. A budget escalates, it does not loop,” also still cites this document as its escalation destination. The implementation is not this design’s full four-rung system: `docs/tillr/decision-provenance-review.md`, “4. Not yet built, high significance” and “5.2. Medium, not built,” identifies its four-rung authority, separate plan-owner dispatch, fence, and ledger decisions as unbuilt, while “5.1. Medium, live or partly live” calls conflict recording, typed answers, retained work, and routing “partly, in a different shape.” So keep it as relevant design context, but treat parts of it as an unbuilt proposal rather than the current operating contract.

**Accurate**

The following claims are outdated or too broad for the current tree:

- **“What already exists, and why it is insufficient” (section 1.2)** calls the board’s conflict-record schema and a distinct plan-owner seat unverified assumptions. The current store has an `item.conflict` event and `item.Conflict` in `tools/tillr-item/item/conflict.go`; `RecordConflict` appends changed records and updates the item’s state in the same transaction. `TestConflictRecordsOnlyChangedTransitions` in `tools/tillr-item/item/conflict_test.go` verifies that changed conflict fields become event transitions. A `plan` seat also exists in `config/tillr/workflows/planned-build@v1.json`. But neither fact implements the proposed full contract: `item.Conflict` stores fields such as `Text`, `BlockedCriteria`, `WaitingOn`, and `Status`, not the proposed required policy checks, blocked/tainted work, recommendations, or typed answer. The design’s “raised → triaged → awaiting → answered → closed” lifecycle is not its store’s state vocabulary; the store documents `raised`, `awaiting`, `owner`, `question`, `answered`, and `closed`.
- **The plan-owner rung and routing to it (sections 3.1–4.2)** are not the current routing mechanism. `config/tillr/workflows/planned-build@v1.json` uses `plan` for an optional read-only node that proposes a piece list; if it cannot split without a design decision, it reports an owner obstacle. `docs/design/tillr-planning-node-model-tiering.md`, “12. Rulings this builds on,” says seats are declared in policy with no rungs. In contrast, `TestBlockedReportRoutesOnceToDesignThenOwner` in `tools/tillr-drain/blocked_route_test.go` demonstrates the actual blocked-report path: the worker’s report goes to the design seat, and only an explicit `NEXT-ACTOR: owner` reaches the owner. The `plan` workflow seat is not evidence that the proposed plan-owner conflict-dispatch rung exists.
- **The fence/check distinction (sections 5.2 and 6.2)** needs to distinguish the item’s actual write fence from proposed C2. The drain enforces allowed paths and prevents a worker from changing its own item block before the gate (`TestWorkerCannotEditItsOwnBlockBeforeGate` in `tools/tillr-drain/blocked_route_test.go`). But the broader fence over design, policy, briefing/skill, and gate artifacts, and the C1–C4 policy-ledger checks, are not built: `docs/tillr/decision-provenance-review.md`, “4. Not yet built, high significance” and “5.2. Medium, not built,” records the fence and policy-ledger decisions as unbuilt.
- **Typed answer kinds, frozen-and-retained in-flight changes, and the casebook/convergence loop (sections 4.3–4.4 and 9)** are proposed semantics, not the current conflict record contract. The current conflict struct has no answer-kind or tainted-work fields; `docs/tillr/decision-provenance-review.md`, “5.1. Medium, live or partly live,” explicitly characterizes D5 (typed answers) and D6 (retained work) as only partly built “in a different shape,” and a scoped search, `rg -n -i 'casebook|owner overturn rate|repeat conflicts per policy|oldest.*awaiting' tools/tillr-item tools/tillr-drain`, printed no matches (exit 1), so the casebook and those named signals are not established implementation surfaces there.
- **Timeout escalation (section 8.3)** is not current behavior. `docs/design/tillr-judge-and-lifecycle.md`, “4.3 How a Design Run’s Ending Moves Its Conflict,” says design runs have no timeouts and the owner’s rule is that no item reaches the owner except through an explicit objection naming the owner. The design’s time-bound reroute remains a proposal, not an implemented default.
- **The `fork.follow-ref` example ([OQ-2](../../design/escalation-ladder.md#open-questions) and section 12)** has moved on in its stated policy and resolver design. `docs/design/agent-runtime.md`, “21. One base: the series follows the newest release,” records the newest-release rule and calls for a shared resolver, while its sequence says to verify that both resolution sites agree. That gives a current desired rule, but does not show that this design’s proposed canonical-source watch or the shared resolver has been built.

**Status line**

`status: proposed` and `stage: DESIGN` remain defensible: the full design has not been built, and the explicit inventory in `docs/tillr/decision-provenance-review.md`, “4. Not yet built, high significance” and “5.2. Medium, not built,” leaves major decisions and mechanisms unbuilt. However, the `next` field (“A companion implementation plan settles per-file build detail; open questions are settled by the user”) is stale as a complete handoff: the current architecture has an optional planning-node model, explicit obstacle routes, and owner-review questions across documents, not a companion plan that would implement this four-rung mechanism as written. The frontmatter’s `date: 2026-11-03` also predates the tree facts recorded in the November 18 provenance review; the stage is still design, but its “assumption” and “unverified” passages need a current read.

**Open questions**

The questions remain askable, but some defaults or premises have changed. Evidence below is cross-checked against `docs/tillr/decision-provenance-review.md`, which lists [OQ-1](../../design/escalation-ladder.md#open-questions), [OQ-4](../../design/escalation-ladder.md#open-questions), [OQ-5](../../design/escalation-ladder.md#open-questions) and [OQ-6](../../design/escalation-ladder.md#open-questions) among unbuilt design decisions.

- **[OQ-1](../../design/escalation-ladder.md#open-questions) — policy-ledger home:** Still open. `docs/tillr/decision-provenance-review.md`, “5.2. Medium, not built,” names [OQ-1](../../design/escalation-ladder.md#open-questions) with the unbuilt ledger. `docs/design/agent-runtime.md`, “29.3 The verbal decision problem,” separately proposes typed operational decision records in the store and says whether that table exists is an open question; it does not settle this document’s policy-ledger schema or location. `fd . docs/policy -t f` reports that `docs/policy` is not a directory, so the proposed per-policy directory is absent.
- **[OQ-2](../../design/escalation-ladder.md#open-questions) — canonical `fork.follow-ref` source:** Partly overtaken, not fully answered. `docs/design/agent-runtime.md`, “21. One base,” settles the newest-release rule and specifies a shared resolver reading the pack manifest, but its implementation sequence still says to confirm the resolver and the two consumers’ agreement. The value is no longer the open question; whether the proposed single source/resolver is actually established, and what C2 would watch today, remains unresolved.
- **[OQ-3](../../design/escalation-ladder.md#open-questions) — awaiting time bound and human notification:** Still open as an implementation/design question. `docs/design/tillr-judge-and-lifecycle.md`, “4.3 How a Design Run’s Ending Moves Its Conflict,” states there are no timeouts and that no time limit fails work. This rules out silently converting elapsed time into an answer, but does not choose or implement a non-failing reroute/notification policy for an aged conflict.
- **[OQ-4](../../design/escalation-ladder.md#open-questions) — blast-radius boundary and measured delegation threshold:** Still open. `docs/tillr/decision-provenance-review.md`, “5.2. Medium, not built,” lists [OQ-4](../../design/escalation-ladder.md#open-questions) as unbuilt. `docs/design/agent-runtime.md`, “28. Authority, not approval,” identifies owner-only classes and routes ambiguity upward, but does not settle this question’s proposed measured threshold for provisional design authority.
- **[OQ-5](../../design/escalation-ladder.md#open-questions) — plan-owner seat and brief authorship:** Partly answered, not closed. A `plan` seat exists for the optional `planned-build@v1` workflow (`config/tillr/workflows/planned-build@v1.json`), and `docs/design/agent-runtime.md`, “The escalation ladder, as ruled,” says a plan owner is a cheap seat distinct from the designer. But the existing plan node writes a piece-list proposal, not the design’s plan-owner conflict disposition, and the current blocked route sends worker objections to design then to an explicitly named owner (`TestBlockedReportRoutesOnceToDesignThenOwner`). Whether the plan node/seat is the conflict-resolution rung and whether it authors the relevant briefs are still open; the provenance review also lists [OQ-5](../../design/escalation-ladder.md#open-questions) as unbuilt.
- **[OQ-6](../../design/escalation-ladder.md#open-questions) — asynchronous owner-review window and silence ratification:** Still open and still askable. `docs/tillr/decision-provenance-review.md`, “4. Not yet built, high significance,” marks this row as an open question, and its [OQ-PR21](../decision-provenance-review.md#OQ-PR21)/[OQ-PR22](../decision-provenance-review.md#OQ-PR22) ask whether designs containing unseen designer decisions may be built from and whether “fill them in through principles” still stands. No cited ruling closes the proposed review window or says silence ratifies.
- **[OQ-7](../../design/escalation-ladder.md#open-questions) — order for parameterizing prose policies:** Still open. The policy ledger and its checks remain unbuilt (`docs/tillr/decision-provenance-review.md`, “5.2. Medium, not built”); `docs/design/agent-runtime.md`, “29.3 The verbal decision problem,” instead proposes typed operational decision records and leaves their storage question open. Neither source supplies a completed policy-ledger migration order.

**Action**

**correct facts** — the conflict-routing purpose remains relevant, but the document needs to separate the partial event-backed conflict and obstacle machinery from its unbuilt ladder, policy-ledger, and casebook proposals, and update its stale implementation handoff and open-question premises.

### 36. autonomous-landing

Retired board landing design (superseded by [`docs/design/landing-principles.md`](../../design/landing-principles.md)): **obsolete**; recommended action: **correct facts**.

**Relevant**

The general goal—unattended work that reports outcomes and handles landing safely—is still relevant and is implemented through a different system: `tools/tillr-drain/README.md` describes `loop` as the unattended supervisor, and `tools/tillr-land/README.md` identifies tillr-land as the sole landing path. This particular board/merge-queue architecture is superseded, not an active build proposal: the document's retirement banner says the board, drain, merge queue and whale mode were deleted and tillr replaced them; `docs/design/landing-principles.md` says the board landing pass and merge queue are gone and identifies `tools/tillr-land` as the only lander. The design's own claims about a still-open item-225 ruling and a later item-130 build plan are no longer the path forward.

**Accurate**

The explicitly dated historical measurements and proposals remain useful as a record, but several present-tense/current statements no longer describe the tree:

- The frontmatter `next` says to wait for an owner ruling on item 225 and then plan against item 130. That is stale against the document's own retirement banner (`Retired 2026-11-16`) and `docs/design/mq-retirement.md`, which describes removal of `tools/mq` and rebuilding missing pieces inside tillr.
- In “What exists today” (Section 2.1), “Both current landers” refers to the board landing pass and `tools/land`/`mq`. They are not current: `docs/design/landing-principles.md`, “Scope: which lander a principle binds,” says the board pass and merge queue were retired and `tools/tillr-land` is the one trunk lander. The repository also has no `tools/board-cli/` or `tools/mq/` directories (checked with `test -d`; both printed `absent`).
- The board-pass implementation described in Section 5.3 (“What the board's own landing pass implements”) and item-130 `Notifier`/queue-store claims in Section 6.3 are historical, not current implementation facts. The current boundaries are documented in `tools/tillr-drain/README.md` and `tools/tillr-land/README.md`; the latter says tillr-land has no candidate queue. `docs/design/mq-retirement.md` records mq's deletion.
- `board drain verify` in Section 9 is not a current command: `rg -n 'board drain verify|mq submit|generation\.submitted|GATE-BLOCKED|lineage_id' tools --glob '!**/*_test.go'` returned no matches. The current drain is described in `tools/tillr-drain/README.md`.
- The proposed race/patch-id rules are themselves marked superseded in Sections 5.2–5.3. Their replacements are described in [`docs/design/landing-principles.md`](../../design/landing-principles.md) (“Re-application preserves the change, not the patch” and “Gate what will be the tip, once”). Those supersession notices are accurate as historical annotations; the old rules should not be read as current behavior.

**Status line**

`stage: SUPERSEDED`, `superseded-by: ./landing-principles.md`, and `archived: 2026-11-16` agree with the opening retirement banner and with `docs/design/landing-principles.md`, which names the current landing principles and one lander. `status: draft` describes the document's original status, but is ambiguous as a current status label beside `stage: SUPERSEDED`. More importantly, `next` still reads as active work despite the retirement. The file is also absent from the “Superseded / historical records” table in `docs/tillr/design-status.md`; its own retirement markers, rather than that index, establish its present disposition.

**Open questions**

- Its first open question (resident drain vs exit) is answered, not open: its answer in “Open questions” says it was ruled 2026-10-30; `docs/design/agent-runtime.md`, [OQ-RR4](../../design/agent-runtime-retirement.md#question-ids-that-outlive-the-document), records the resident-drain ruling.
- Its second and third open questions (notification target and branch/worktree retention) have no answer text in this document and are not answered elsewhere in the cited retirement material. They are no longer askable as active design questions because the opening retirement notice says the design's open questions were closed with its retirement. Therefore they are unruled historical questions, not unresolved work for item 225. The proposed answers remain visible as history and should not be mistaken for owner decisions.
- Its repair-budget and flake-rerun rulings are owner rulings in the decision ledger (Section 12), so they are answered within the document; their current effect should be read through the superseding [`docs/design/landing-principles.md`](../../design/landing-principles.md), not as a revived build plan.

**Action**

Correct facts: replace the stale active `next` pointer and clarify that the two unruled open questions were closed as askable questions by the 2026-11-16 retirement, because the document is already superseded while its metadata still directs readers to item 225 and item 130.

### 37. tillr-designer-sessions

[`docs/reference/tillr-designer-sessions.md`](../../reference/tillr-designer-sessions.md) *(graduated 2026-11-20 from the design evaluated below)*: **stale** at 2026-11-18 triage; recommended action: **correct facts**.

**Relevant**

Still relevant and wanted: `tools/tillr-drain/README.md`, “Hand-driven comment delivery,” says comment items reuse a document's recorded designer at its fixed desk; `docs/tillr/design-status.md` lists this design as shipped. The README and the design-status entry continue to reference the design, and no superseding document is identified in those references. Its central behavior is built, not superseded or iceboxed.

**Accurate**

The core build claims match the tree: `tools/tillr-drain/drain/comments.go`, `comment_intake.go`, `designer.go`, and `designer_compaction.go` implement delivery, intake, session choice/recording, and compaction; `tools/tillr-view/designers.go` and `tools/tillr-view/web/src/components/Designers.tsx` implement the designers view. Tests include `TestCommentPathTwice`, `TestWritingSessionAnswersFirstComment`, `TestDesignerCompactRunsOnlyAtColdDispatch`, and `TestReplyConfirmationFromReview`. The focused command `cd tools/tillr-drain && go test ./drain -run '^(TestCommentPathTwice|TestWritingSessionAnswersFirstComment|TestDesignerCompactRunsOnlyAtColdDispatch)$'` printed `ok   github.com/mschulkind-oss/tillr-drain/drain 20.870s`.

One factual mismatch: under **Cost**, the sentence “The first comment after the cache has expired costs about $0.40 more than starting cold” overstates the measured un-compacted expired-resume difference. The “What a turn costs, measured” table reports $1.11 for an expired 35,000-token session resumed as-is and $0.84 cold, a $0.27 difference; the later cost table repeats those figures. About $0.40 applies to the separate compacted-resume comparison ($1.21 versus $0.84), described under **The keep-warm rule** and `D-CD7`, not to every expired session. Other implementation claims checked above match the cited code/tests. The historical token/cost measurements were not independently reproduced in this triage.

**Status line**

True as written: frontmatter says `status: accepted`, `stage: BUILT`, and the body says all six build-order items landed, with the live run still owed. `docs/tillr/design-status.md` independently records “Built 2026-11-18: all six items landed; the live run is owed,” and identifies the owner's live run of two comments as the remaining work. The repository does not establish whether the owner performed that run outside the tracked record, so it remains unverified here.

**Open questions**

No owner ruling remains open: **Needs your ruling: None** and the Decision Ledger records D-CD1 through D-CD11 as decisions made by the designer. The empirical question “whether a resumed session answers better than a cold one” remains explicitly UNMEASURED in the design; its one paired comment is explicitly said to prove nothing. The live two-comment run also remains owed in `docs/tillr/design-status.md`. These are measurement/acceptance gaps, not unresolved design rulings; the repo provides no evidence that either was answered since the stated status.

**Action**

Correct facts — change the **Cost** summary so its roughly $0.40 premium is attributed to the compacted-resume case, since the measured expired un-compacted case is $0.27 above cold.

### 38. momentum-principle

[`docs/design/momentum-principle.md`](../../design/momentum-principle.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still wanted as an operative ruling, not superseded or iceboxed: `tools/tillr-drain/drain/loop.go` cites it for the loop's no-budget stop contract and `tools/tillr-drain/main.go` cites it for the deleted token ceiling; the roadmap still invokes it in `docs/tillr/roadmap.md` under the spend-measurement item, and `docs/design/run-wall-ruling.md` relies on it to reject total-run walls. The loop's direct references and those later decisions show continued use, not merely an archival link.

**Accurate**

Two implementation claims need correction, and one list needs clarification:

- Under **“Consequences to build,” item 4**, the statement that the loop “currently dispatches one run at a time” and that concurrency is still the highest-value work is stale. `TestTwoLanesRunConcurrentlyInOneBatch` in `tools/tillr-drain/loop_test.go` makes the two runs rendezvous and asserts both are in flight together; `tools/tillr-drain/drain/loop.go` implements the multi-lane loop and boundary checks. Parallel dispatch is built, not outstanding.
- Under **“What this does not change,”** the statement that a failed candidate's item “still moves to `in-progress` rather than being retried forever” no longer describes the tree. `TestAFailedCandidateGoesBackToItsWorkerUntilItRepeats` in `tools/tillr-drain/loop_test.go` pins the current gate-refusal behavior: it returns to the worker once, records no failed verdict, and stops repeating after an identical refusal; `TestADrainThatRefusedEveryCandidateNamesItAsARepeatedRefusal` covers the repeated-refusal outcome. The quoted state transition is therefore outdated.
- Under **“Consequences to build,” item 2**, the three categories omit the separately named `lanes-leased` hard stop: `StopLanesLeased` is documented in `tools/tillr-drain/drain/loop.go`, and `TestALaneHeldWithNoExpiryStopsTheLoopAsLanesLeased` in `tools/tillr-drain/loop_test.go` asserts it. This is an explicit operational stop requiring a lane release, even though it fits the ruling's broader allowance for a fault the loop cannot route around.

The other listed consequences match the tree: the three budget flags are refused by `TestTheLoopCommandRefusesTheDeletedBudgets` in `tools/tillr-drain/spend_line_test.go`, and `TestTheLoopHasNoTickBudget` verifies work continues until drained; `BackoffDelay` and `TestBackoffDelayDoublesUpToItsCap` in `tools/tillr-drain/drain/loop.go` and `tools/tillr-drain/drain/spend_test.go` implement reported infrastructure backoff; `TestTheSpendLineNamesTheMeasurement` and `spendLine` in `tools/tillr-drain/spend_line_test.go` and `tools/tillr-drain/main.go` preserve spend reporting without a ceiling. The default loop waits when the queue is empty (`Run` in `tools/tillr-drain/drain/loop.go`); `--exit-when-drained` is an explicit opt-out in `tools/tillr-drain/main.go`, not the default service behavior.

**Status line**

“Status: ruling, 2026-11-12” remains true: the current loop implementation cites this document as the ruling for budget-free operation in `tools/tillr-drain/drain/loop.go`, while the spend-ceiling removal remains covered by `TestTheLoopCommandRefusesTheDeletedBudgets` in `tools/tillr-drain/spend_line_test.go`. The stale implementation details above do not supersede the principle itself.

**Open questions**

The document contains no explicit open-question entries. The scoped check `rg -n -i 'open question|\bOQ[- ]|\?$' docs/design/momentum-principle.md` printed `(no matches in docs/design/momentum-principle.md)`, so there is no listed question to mark answered, still open, or unaskable.

**Action**

correct facts — the ruling remains active, but its parallelism, failed-candidate, and stop-category descriptions have drifted from the current loop, as shown by the cited implementation and tests.

### 39. tillr-idea-pipeline

[`docs/design/tillr-idea-pipeline.md`](../../design/tillr-idea-pipeline.md): **stale**; recommended action: **correct facts**.

**Relevant**

The core idea-intake and designer workflow remains wanted and is already partly built: [`docs/reference/observatory-actions.md`](../../reference/observatory-actions.md#the-saved-idea-item) ([The saved idea item](../../reference/observatory-actions.md#the-saved-idea-item) and [Staging, gate refusals, and outcomes](../../reference/observatory-actions.md#staging-gate-refusals-and-outcomes)) defines the current idea save and `from`-marked outcomes, while `tools/tillr-item/item/idea_test.go` (`TestIdeaUniqueIDAndTitle`) and `tools/tillr-drain/drain/idea_test.go` (`TestIdeaStageCarriesWordsGuideAndInstructions`, `TestIdeaCandidateChecksBeforeGate`) cover intake and the designer turn. The remaining typed lineage edge and lineage presentation are still described as needed: the idea-pipeline’s “Slice reconciliation” marks Slice 1b and Slice 3 unbuilt, and [`docs/reference/tillr-queue-persistence.md`](../../reference/tillr-queue-persistence.md#why-its-this-way) (`D-QP3`, `D-QP4`) assigns intake to this design and [`docs/reference/observatory-actions.md`](../../reference/observatory-actions.md#staging-gate-refusals-and-outcomes) notes that outcomes currently read the `placed_from` event receipt because no `derived_from` table exists yet. The lineage work is not filed as an item: the target’s Section 8 says neither unit is filed while the owner reviews how documents declare work. The queue-persistence decision `D-QP3` also supersedes the pipeline’s original `derived-items.json` mechanism; child definitions are Markdown item blocks materialized by sync ([`docs/reference/tillr-queue-persistence.md`](../../reference/tillr-queue-persistence.md#why-its-this-way)).

**Accurate**

The reconciliation at “Slice reconciliation, 2026-11-18” is substantially consistent with current code: `tools/tillr-item/item/schema.go` defines `items` without a `kind` column and `item_edges(item_id, blocked_by)`; `tools/tillr-item/item/place.go` records `placed_from`; `tools/tillr-view/idea_words.go` derives outcomes from that receipt; and `tools/tillr-view/idea_internal_test.go` (`TestIdeasHTTPSavesWordsAndShowsIntake`) verifies idea display and those receipt-linked outcomes. The claimed built pieces are evidenced by the idea tests above, `tools/tillr-drain/drain/scouted_design_test.go` (`TestScoutedDesignWorkflowLoop`), and the `question-open` handling in `tools/tillr-drain/drain/plan.go` / `tools/tillr-drain/drain/schema.go`.

Earlier sections still state incompatible or superseded facts as current: the “In short” paragraph and Sections 1–4 describe a typed `derived_from` graph and `LineageTree` as the pipeline/view shape, but the table and query in `tools/tillr-item/item/schema.go`, the `blocked_by` reads in `tools/tillr-view/serve.go`, and the “Slice reconciliation” entry for Slice 3 show that lineage is not built. Sections 2–3 describe emitting `derived-items.json` and returning answers via `tillr item answer`/Vantage harvesting; these are superseded by queue-persistence D-QP3 (“4.1 Step-by-Step Motion”) and the target’s own “Slice 4” and D-IP6 reconciliation, which say answer return is superseded and the manifest is no longer used. Section 5 calls Slice 3 a “Browsable milestone live” and Slice 2 an “Existing item”; this conflicts with the later Slice 3 “not built” reconciliation and with [`docs/reference/observatory-redesign.md`](../../reference/observatory-redesign.md#entities-and-urls), which describes the built dependency graph but no idea-lineage tree. These contradictions mean the dated reconciliation is a better account of build state than the unreconciled early sections, but the document does not consistently label the earlier material historical.

**Status line**

The frontmatter `status: accepted` and `stage: DECIDED` remain true for the design decisions: Section 6.2 says there are no open questions, with the prior OQ rows recorded as rulings or withdrawals in Section 6.1. The body’s “partly built” status is also supported by its slice reconciliation and the current implementation/tests listed above. Section 8 accurately says the two lineage units are not filed. The frontmatter `next` points toward those remaining units and explicitly conditions new item directives on the owner’s ruling, matching Section 8’s statement that the owner is reviewing how documents declare work. However, the status line does not resolve the stale current-tense claims identified under Accurate.

**Open questions**

Section 6.2 (“Open Questions”) says “None,” and that is still accurate as a record of unresolved questions: [OQ-IP1](../../design/tillr-idea-pipeline.md#61-settled-decisions) is settled as a workflow within one item unless work splits into separate documents, supported by `config/tillr/workflows/scouted-design@v1.json` and `tools/tillr-drain/drain/scouted_design_test.go` (`TestScoutedDesignWorkflowLoop`); [OQ-IP3](../../design/tillr-idea-pipeline.md#61-settled-decisions) is withdrawn because the observatory redesign replaces the queue rail, as documented in [`docs/reference/observatory-redesign.md`](../../reference/observatory-redesign.md); [OQ-IP4](../../design/tillr-idea-pipeline.md#61-settled-decisions) is recorded as settled by D-IG10 in [`docs/reference/tillr-item-graphs.md`](../../reference/tillr-item-graphs.md#why-its-this-way) (“Why it's this way”); and [OQ-IP2](../../design/tillr-idea-pipeline.md#61-settled-decisions) is withdrawn by the owner’s statement recorded in the target’s Section 6.1. These are closed ledger entries, not outstanding asks. The target says the owner may overturn the designer-settled rulings, so they remain reopenable by the owner, but that does not make them currently open questions.

**Action**

correct facts — retain the still-relevant, partly built intake and unbuilt lineage design, but reconcile or label the earlier pipeline, answer-return, and “live” rollout claims that conflict with its own November 18 status and the current code.

### 40. tillr-convergence

[`docs/design/tillr-convergence.md`](../../design/tillr-convergence.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant as a partial design, not superseded wholesale: document-backed intake, hold/mover visibility, and the remaining derived-copy question have corresponding current surfaces in `tools/tillr-item/item/sync.go`, `tools/tillr-view/waiting.go`, and `docs/design/tillr-selfhosted-build.md` (which still links back to convergence as the source of D-TC5). The seed-retirement unit is built: `config/tillr/seed-items.json` is absent, and `tools/tillr-item/main.go` says the `seed` face is retired in favor of `tillr item sync`. The input-change re-admission design is superseded by owner ruling D-JL10 in `docs/design/tillr-judge-and-lifecycle.md`; `tools/tillr-drain/drain/readmit_test.go` includes `TestAChangedInputReDispatchesNothing`. I found no successor document replacing convergence; the self-hosted build design continues to depend on it for D-TC5.

**Accurate**

- D-TC1 and much of D-TC2 match the current implementation: `tools/tillr-item/item/sync.go` handles document sync; `tools/tillr-item/item/sync_test.go` has `TestSyncIgnoresInlineMentionsAndFencedDirectives`, `TestSyncIsIdempotent`, `TestSyncUpdatesAnUnjudgedDefinitionAndKeepsStateAndRank`, and `TestSyncLeavesAJudgedDefinitionFrozen`.
- The removed-block case is accurately identified as unresolved in the D-TC2 ledger: `tools/tillr-item/item/sync.go` reports `UnsharedOpenIDs`, and `tools/tillr-drain/drain/plan.go` routes such items to triage rather than retiring them. `tools/tillr-item/item/sync_test.go` has `TestSyncCountsUnsharedOpenAndStaleDocBlocks`.
- The built portions of D-TC3/D-TC4 have current test evidence: `tools/tillr-drain/drain/definition_test.go` contains `TestAllDefinedHoldReasonsAreClassified`; `tools/tillr-view/waiting_internal_test.go` contains `TestWaitingEveryDrainReasonHasARule` and `TestWaitingMoversAndLeaseOnlyRun`. The exact post-run cause-statement and two-unrelated-items behavior remains described as unbuilt in Section 4, but I did not establish whether newer, differently named machinery covers any subset: `tools/tillr-drain/drain/defect.go` files tooling-defect items for reported tooling obstacles, a distinct path from the proposed same-stage/same-error threshold.
- D-TC5 being unbuilt and dependent on [OQ-SH2](../../design/tillr-selfhosted-build.md#OQ-SH2) remains consistent with `docs/design/tillr-selfhosted-build.md`: its “What Is Built and What Is Left” section says the source launcher is not built, and its “Open Questions” section still asks [OQ-SH2](../../design/tillr-selfhosted-build.md#OQ-SH2) about when the pack copy is refreshed.
- One statement is stale: [OQ-TC2](../../design/tillr-convergence.md#OQ-TC2) says `tillr-planning-node-model-tiering.md` has [OQ-PT1](../../design/tillr-planning-node-model-tiering.md#OQ-PT1) through [OQ-PT8](../../design/tillr-planning-node-model-tiering.md#OQ-PT8) open. That document’s Decision Ledger marks [OQ-PT1](../../design/tillr-planning-node-model-tiering.md#OQ-PT1) through [OQ-PT6](../../design/tillr-planning-node-model-tiering.md#OQ-PT6) ruled or closed; its “Open Questions” section contains only [OQ-PT7](../../design/tillr-planning-node-model-tiering.md#OQ-PT7) and [OQ-PT8](../../design/tillr-planning-node-model-tiering.md#OQ-PT8).
- The “three open questions were settled” statement describes [OQ-TC1](../../design/tillr-convergence.md#OQ-TC1)/2/3, but the ledger no longer presents them as three current open questions: [OQ-TC1](../../design/tillr-convergence.md#OQ-TC1) is settled and built; [OQ-TC2](../../design/tillr-convergence.md#OQ-TC2) is a designer’s decision with downstream work still pending; [OQ-TC3](../../design/tillr-convergence.md#OQ-TC3) is superseded. Evidence: the Decision Ledger in this file, `tools/tillr-item/main.go`, and `TestAChangedInputReDispatchesNothing` in `tools/tillr-drain/drain/readmit_test.go`.

**Status line**

The dated measurement in the opening status paragraph is explicitly historical (measured against `main` at `35a3dc9f1`, reconciled at `dcb76fe41`), so it is not itself a claim about today. The frontmatter says `status: accepted` and `stage: DECIDED`, but D-TC2 in the Decision Ledger says the removed-block retirement rule is “a design decision this document owes,” while the opening says “Needs your ruling: None.” Those statements do not establish a single settled design state. I cannot verify from the repository whether `accepted` permits that unresolved design decision; the document needs to reconcile its status with the decision it says remains owed. The owner’s 2026-11-18 no-new-item-directive rule is recorded in both D-TC3 prose units in Section 4, but no later answer to that rule appears in the inspected documents.

**Open questions**

- **[OQ-TC1](../../design/tillr-convergence.md#OQ-TC1):** answered and built, not still open. The ledger names `item-seed-retire`; `config/tillr/seed-items.json` is absent and `tools/tillr-item/main.go` directs callers from `seed` to `sync`.
- **[OQ-TC2](../../design/tillr-convergence.md#OQ-TC2):** the policy direction is recorded as a designer’s decision, not an unanswered question. The downstream question of model ownership is not settled by the linked model-tiering ledger: `docs/design/tillr-planning-node-model-tiering.md` says only [OQ-PT7](../../design/tillr-planning-node-model-tiering.md#OQ-PT7)/8 remain open. The current `config/tillr/backend-policy.json` still has backend model IDs, so the convergence ledger’s claim that model IDs remain is accurate, but its “[OQ-PT1](../../design/tillr-planning-node-model-tiering.md#OQ-PT1) to [OQ-PT8](../../design/tillr-planning-node-model-tiering.md#OQ-PT8) are open” explanation is not.
- **[OQ-TC3](../../design/tillr-convergence.md#OQ-TC3):** answered, then superseded by D-JL10; it is not askable as written. Evidence is D-JL10 in `docs/design/tillr-judge-and-lifecycle.md` and `TestAChangedInputReDispatchesNothing` in `tools/tillr-drain/drain/readmit_test.go`.
- **Unnumbered decision:** whether removing a block retires an item or leaves it for triage is still unresolved in the inspected tree: the D-TC2 ledger says the design owes a decision, and `tools/tillr-item/item/sync.go` plus `tools/tillr-drain/drain/plan.go` implement the triage path. The two D-TC3 units are also explicitly not filed while the owner reviews how documents declare work (Section 4). I found no later answer in the repository, so whether that review has concluded outside the tree cannot be determined.
- **Dependency [OQ-SH2](../../design/tillr-selfhosted-build.md#OQ-SH2):** remains open in `docs/design/tillr-selfhosted-build.md` under “Open Questions”; it is still a live dependency for D-TC5, not an OQ owned by this document.

**Action**

correct facts — keep convergence as a relevant partial design, but reconcile its status with the unresolved removed-block decision and update the [OQ-TC2](../../design/tillr-convergence.md#OQ-TC2) cross-reference to the model-tiering questions that remain open.

### 41. landing-principles

[`docs/design/landing-principles.md`](../../design/landing-principles.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still wanted as the repository's landing reference: `AGENTS.md` under “Landing a lane: shared forks, verified candidates” directs changes to the gate and landing path to check this document, and `docs/design/mq-retirement.md` under “Compliance with Durable Landing Principles” uses its first three principles to state the guarantees required of `tools/tillr-land`. Several principles remain directly evidenced by the current lander: `tools/tillr-land/main_test.go` contains `TestAMovedTrunkSquashesTheChangeOntoTheNewTip` and `TestASquashConflictIsRecordedAndPinsTheHead`, while `tools/tillr-land/idempotent_test.go` contains `TestAnEmptySquashIsRefusedAsAlreadyOnTrunk`. However, the file is not wholly current: its per-attempt budget principles (5 and 6) are superseded by the 2026-11-17 owner ruling D-JL10 in `docs/design/tillr-judge-and-lifecycle.md`, Section 4.4, which says worker endings are never counted and rejects numeric thresholds.

**Accurate**

The core landing claims in Sections 1–3 and 10 match the present lander: `tools/tillr-land/land/landing.go` defines the refusal reasons; `tools/tillr-land/land/trunk.go` performs `Trunk.Squash`; and the moved-trunk, conflict, and already-on-trunk cases are pinned by the tests named above. Two statements do not match current evidence:

- In Section 3, the cited test `TestSquashWritesOneCommitWithTheItemTrailerAndTheDeployedIdentity` does not exist in the current tree. `tools/tillr-land/land/trunk_test.go` instead names it `TestSquashWritesOneCommitWithNoItemTrailerAndTheDeployedIdentity`, and `tools/tillr-land/README.md` under “Landing” confirms that landed commits have no `Tillr-Item:` trailer.
- Section 7 says “Enforced by: nothing. The heading is the defect.” This is stale: `tools/tillr-view/README.md` under “Waiting” says the view reports why each unlanded item is not moving and names the next actor/action for held rows; `tools/tillr-view/owner_waiting_test.go` includes `TestWaitingSummarySeparatesDocumentAnswersFromOtherNextActors`, and `tools/tillr-view/waiting_internal_test.go` includes `TestWaitingEveryDrainReasonHasARule`.
- Section 5's “Item 418 is where the charge event is redefined; until it lands” and Section 6's per-distinct-cause repair budget no longer describe the current rule. `docs/design/tillr-judge-and-lifecycle.md`, Section 4.4 and its D-JL10 decision-ledger entry, says the prior counted-failure design was overturned and “a worker's ending is never counted”; the drain README's section “A refused run goes back to its worker” describes the built uncounted return behavior. The current `docs/tillr/roadmap.md` contains no reference to item 418 (checked with `rg -n 'item 418|418' docs/tillr/roadmap.md`).

**Status line**

`status: accepted` and `stage: CURRENT` remain reasonable for the document's enduring landing rulings: `AGENTS.md` still establishes it as the reference, and the current lander tests implement principles 1–3 and 10. The `verified: 2026-11-15` stamp is stale as a verification of the current contents: `git log` shows the document was changed on 2026-11-18 (commit `0ca6b1c21`), and its Section 11 includes a 2026-11-17 ruling. The stale test citation and Section 7 and Sections 5–6 discrepancies above also show that the contents have not all been checked against the current tree.

**Open questions**

There is no dedicated open-questions section in this document. Its explicit “nothing yet” / “not enforced yet” statements function as unresolved implementation gaps, not as a list of questions. The budget/charge question in Sections 5–6 is no longer open: D-JL10 in `docs/design/tillr-judge-and-lifecycle.md`, Section 4.4, settles it by eliminating counts and numeric thresholds. The enforcement claim in Section 7 is answered by the current waiting view and tests cited above. Section 4's acceptance-test wording and Section 9's machinery-ticket/hand-carry rule remain stated gaps here, but this document does not identify an owner or a concrete pending decision for either; whether either remains askable cannot be established from this document. The broader unbuilt mechanisms in Section 11 are pointed to `docs/design/failure-diagnosed-not-waited-out.md`; this triage did not establish that each mechanism's design status or implementation has changed.

**Action**

**correct facts** — keep the still-used landing reference, but update the obsolete test name, the already-built waiting-surface enforcement claim, the superseded budget principles, and the verification stamp to match the current tree and D-JL10 ruling.

### 42. board-state-machine

Retired board landing design (superseded by [`docs/design/landing-principles.md`](../../design/landing-principles.md)): **obsolete**; recommended action: **correct facts**.

**Relevant**

Still relevant as a historical record of the deleted board's landing design and decisions, but not as a current implementation design. `docs/design/board-docs-retirement.md` [§1](../../design/board-docs-retirement.md#1-the-ruling-and-the-deletion) records the owner's deletion ruling and the deletions in `3f02e3b916` and `afdc9a9571`; its [§2](../../design/board-docs-retirement.md#2-the-inventory-and-classification) classifies this document as superseded by `docs/design/landing-principles.md`. The latter names `tools/tillr-land` as the current lander and records which landing principles it enforces.

**Accurate**

The retirement banner and historical framing are accurate: `docs/design/board-docs-retirement.md` [§1](../../design/board-docs-retirement.md#1-the-ruling-and-the-deletion) says the board CLI and session extensions were deleted, and [§2](../../design/board-docs-retirement.md#2-the-inventory-and-classification) lists this document as superseded. The current implementation is different: command `test -e tools/board-cli` printed `tools/board-cli absent`, while `test -e tools/tillr-land` printed `tools/tillr-land exists`; `docs/design/landing-principles.md` [§3](../../design/landing-principles.md#3-gate-what-will-be-the-tip-once) names the current `tools/tillr-land` squash-and-land behavior and its tests.

The remaining misleading present-tense facts are:

1. Frontmatter `next: "Build from the plan..."` directs readers to build this design, contrary to its retirement banner and the retirement inventory.
2. The opening `**Status**` says P0–P4, P7, P9 and P5 are built and P6/P8 are not, which is only a historical snapshot from before deletion, not current implementation status.
3. The same paragraph calls its figures measurements against the “live board store,” although `docs/design/board-docs-retirement.md` [§3](../../design/board-docs-retirement.md#3-the-absorption-ledger) says the old board measurement queries concern a schema no program opens and identifies tillr's event log as the surviving measurement surface.

The later design sections describe intended/then-built machinery, not current machinery, as the retirement banner explicitly warns.

**Status line**

`stage: SUPERSEDED` and `superseded-by: ./landing-principles.md` are true: both are recorded for this exact document in `docs/design/board-docs-retirement.md` [§2](../../design/board-docs-retirement.md#2-the-inventory-and-classification). `status: accepted` is historical acceptance, not currentness; the document itself marks its acceptance date as 2026-10-31 and opens with a retirement banner. The separate `next` field is stale, as noted above.

**Open questions**

The document's decision ledger lists its first two open questions (rework verb and hand-off ownership) as closed, and its third (deletion-procedure approval) as “Closed — withdrawn, not declined”; its opening says no question is open. That account is accurate for the design's own questions. [OQ-BSM4](../../design/board-state-machine-plan.md#OQ-BSM4) remains labeled as a question in `docs/design/board-state-machine-plan.md`, under the bold line “Needs your ruling”, and in its [OQ-BSM4](../../design/board-state-machine-plan.md#OQ-BSM4) section, but that withdrawn approval entry says it has nothing to authorize until the deletion-procedure section (Section 14) is approved. Since the board system and landing pass were subsequently deleted (`docs/design/board-docs-retirement.md` [§1](../../design/board-docs-retirement.md#1-the-ruling-and-the-deletion)), [OQ-BSM4](../../design/board-state-machine-plan.md#OQ-BSM4) is now moot rather than a live, askable implementation question; the retirement record does not say that its specific approval was later granted.

**Action**

correct facts — remove or qualify the stale build-forward `next` pointer and the unqualified current-sounding build/store status, while preserving the document as a superseded historical record.

### 43. seat-profiles

[`docs/design/seat-profiles.md`](../../design/seat-profiles.md): **stale**; recommended action: **correct facts**.

**Relevant**

Still relevant as a partially implemented design, not superseded or complete. `yolo-packs/seat-profiles/README.md` identifies this document as the source of the encoded set fillings and bounds; `tests/seat-profiles.test.mjs` pins the data against it. The README also says the picker and child resolver consume the sets while the bounds remain unenforced, and that the launch path still does not pass the configured `seat_set` to `createAgentSession({ setInForce })`. Those are unfinished design obligations, so retirement or graduation would be premature.

**Accurate**

The broad implementation split in [§5](../../design/seat-profiles.md#5-how-a-set-governs-children) is substantially accurate: the pack README confirms that the picker and per-seat child resolution read the registry, but says bounds are not mechanically enforced and the launch option is not read into the session. The profile config also carries `seat_set` defaults (`tag-personal/config/yolo-jail/config.d/personal.toml`, `tag-work/config/yolo-jail/config.d/work.toml`), and `tests/seat-profiles.test.mjs` has coverage named `every profile names the encoding's set for it, and a profile with no set names none` and `a rendered config yields the set a profile names, and none when it names none`.

These statements do not match the current tree:

- [§2.4](../../design/seat-profiles.md#24-set-codex-codex-quota-single-model-until-the-catalog-is-read) says every CODEX seat, including delegate, maps to `openai-codex/gpt-6.1-sol`, and describes Luna as not encoding-ready. The encoded CODEX delegate is `openai-codex/gpt-6-luna` in `yolo-packs/seat-profiles/files/seat-profiles.json`; `tests/seat-profiles.test.mjs`'s `expectedRows.CODEX.delegate` pins Luna. The rule in [OQ-SP4](../../design/seat-profiles.md#OQ-SP4) to move delegate to Luna when the catalog row exists has therefore been acted on, but the prose was not updated.
- [§2.5](../../design/seat-profiles.md#25-bedrock-work-aws--approved-as-narrowed-2026-10-20-only-solluna-rows-encode)'s Bedrock catalog/narrative still says `gpt-6.1-sol` and `gpt-6-astra`, and says only the Sol/Luna rows encode. The current profile default and encoded BEDROCK rows use `global.openai.gpt-6.2-sol`; the current source catalog in `tag-work/config/yolo-jail/config.d/work.toml` declares 6.2 Sol and 6.2 Astra, with 6.2 Astra priced at $8/$40 rather than the section's old 6/Astra $10/$50. The document's own BEDROCK tables use 6.2 ids, contradicting their surrounding narrative. `git log -8 --format='%h %ad %s' --date=short -- docs/design/seat-profiles.md` reports `2f451dda7 2026-11-17 chore(bedrock): point the bedrock profile and tillr policies at Sol 6.2 and Astra 6.2`, but the surrounding catalog text and summaries were not brought into agreement.
- The frontmatter `next` summary treats the `mode:` session-tag half as built and says only `seat:`, `item:`, `set:` writers and search filtering remain. It also says the `design-seat.ts` implementation was deleted. Checking `test -e yolo-packs/matt/files/extensions/design-seat.ts` printed `absent`; the current-source search for `mode:design`, `mode:whale`, or the proposed tag values under `yolo-packs/matt/files`, `yolo-packs/pi-fork`, and `tests` returned no matches. Thus the mode-tag writer is not currently built; if [§6.4](../../design/seat-profiles.md#64-session-tags-finding-a-session-by-what-it-was) remains the target behavior, it belongs among the remaining work, not only in the historical account.

**Status line**

The frontmatter `status: in-review` and `stage: DESIGN` remain plausible: the configured launch default is not passed into the session, the bounds are still data only, and the tag behavior is incomplete (`yolo-packs/seat-profiles/README.md`). The bold status paragraph says “2026-10-20, updated 2026-11-01,” which is stale: the command `git log -8 --format='%h %ad %s' --date=short -- docs/design/seat-profiles.md` shows later document changes on 2026-11-05, 2026-11-09, 2026-11-13, and 2026-11-17. Its substance also needs the factual corrections above; the date and frontmatter stage do not establish that the implementation is complete.

**Open questions**

The OQ-SP/OQ-SB questions listed in [§8](../../design/seat-profiles.md#8-open-questions) are marked answered or agent-decided, and their answers are recorded there and in the Decision Ledger; there is no listed unanswered question in this document. [OQ-SP4](../../design/seat-profiles.md#OQ-SP4) is no longer merely a future permission: its delegated Luna upgrade is reflected in `yolo-packs/seat-profiles/files/seat-profiles.json` and the `expectedRows.CODEX.delegate` assertion in `tests/seat-profiles.test.mjs`.

A related, still-askable owner question remains outside this document: `docs/design/design-seat-replacement-ruling.md` [“OQ-RS5 — Settle Wonder Full”](../../design/design-seat-replacement-ruling.md#oq-rs5--settle-wonder-full) asks whether the `excludedModels` Wonder-Full ban applies to the design seat, and its B-RS7 row explicitly requires that interpretation to be settled in this document and the encoding. So “no owner question in this document” is literally accurate about its own ledger, but it should not be read as meaning every question affecting this design is settled.

**Action**

**correct facts** — keep the design because its launch integration, tag behavior, and bounds enforcement remain unfinished, but reconcile the CODEX delegate, Bedrock model/catalog narrative and prices, current mode-tag implementation status, and the dated status paragraph with the current tree and the related [OQ-RS5](../../design/design-seat-replacement-ruling.md#OQ-RS5) settlement.
