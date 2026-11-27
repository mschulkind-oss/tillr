---
title: "The queue mirrors the roadmap's outstanding designs — implementation plan"
status: draft
stage: SKETCH
planning-actions: none
depends-on:
  - tillr-queue-mirrors-designs.md
summary: "Research notes, ground-truth symbol map, lifecycle traps, and candidate task cuts for Slices 2 to 5 of tillr-queue-mirrors-designs.md, surveyed at commit 81bf66eb after Slice 1 landed in f9f5129a and 7aa1b377."
---

# The queue mirrors the roadmap's outstanding designs — implementation plan

**Design:** [`tillr-queue-mirrors-designs.md`](./tillr-queue-mirrors-designs.md)
**Tree surveyed at:** `81bf66eb` (2026-11-27)

## Ground truth on `HEAD` (`81bf66eb`)

Slice 1 is built and live (`f9f5129a` and `7aa1b377`):

- **Four-valued stage roles and live question reads:** `StageRole`, `Status`, `DependsOn`, and `DesignUnansweredQuestions` in [`tools/tillr/vantage/vantage.go`](../../tools/tillr/vantage/vantage.go).
- **Roadmap-link mirroring (`item-design-<slug>`):** `collectRoutedDesigns`, `designIndexNeeded`, `NeedsDesignIndex`, and `planDesignMirror` in [`tools/tillr-item/item/sync_design.go`](../../tools/tillr-item/item/sync_design.go), wired into `Sync` in [`tools/tillr-item/item/sync.go`](../../tools/tillr-item/item/sync.go).
- **Zero-question and single-commit dispatch ([`OQ-QM2`](./tillr-queue-mirrors-designs.md#OQ-QM2), [`OQ-QM3`](./tillr-queue-mirrors-designs.md#OQ-QM3)):**
  - `decide` in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) dispatches every unparked zero-question `item-design-*` row (`seat = "design"` when role `open`/unstaged; `seat = "plan"` via [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json) in [`tools/tillr-drain/drain/workflow_defaults.go`](../../tools/tillr-drain/drain/workflow_defaults.go) when role `ready`).
  - `workflowNodeBrief` in [`tools/tillr-drain/drain/node_brief.go`](../../tools/tillr-drain/drain/node_brief.go), `needsCompanionPlanSliceHold` and `stampMirroredDesignBuiltOnNoChange` in [`tools/tillr-drain/drain/workflow_exit_record.go`](../../tools/tillr-drain/drain/workflow_exit_record.go), and `settleNodeFormat` / `settleEvaluator` in [`tools/tillr-drain/drain/workflow.go`](../../tools/tillr-drain/drain/workflow.go) and [`tools/tillr-drain/drain/workflow_evaluator.go`](../../tools/tillr-drain/drain/workflow_evaluator.go) build single-commit designs, stamp `stage: BUILT` on `no_change`, and hold multi-commit designs on `ReasonNeedsCompanionPlanSlice` (`needs-companion-plan-slice`).

Slices 2 to 5 are unbuilt (`rg -n 'waits-on-pieces' tools -g '*.go'` returns nothing).

## Map and traps for Slices 2 to 5

1. **Trap — `recordLandedJudge` marks `item-design-<slug>` `landed` and freezes `c1` on intermediate commits (Slice 3 must clear both).**
   - In [`tools/tillr-drain/drain/judge.go`](../../tools/tillr-drain/drain/judge.go), `recordLandedJudge` calls `item.RecordJudge` (recording `done` on `c1`) and `item.SetState(..., StateLanded, ...)` whenever a run on `item-design-<slug>` lands a commit on `main` — including an [`OQ-QM2`](./tillr-queue-mirrors-designs.md#OQ-QM2) `design` run that moves `<topic>.md` from `DESIGN` to `DECIDED` (or adds `OQ-*`), and a Slice 4 `plan` run that commits `<topic>-plan.md` while `<topic>.md` stays `DECIDED`.
   - Today `planDesignMirror` in [`tools/tillr-item/item/sync_design.go`](../../tools/tillr-item/item/sync_design.go) skips `if exists && finished(st.state) { continue }`.
   - Even after reopening `state` from `landed` to `in-queue` via `SetStateWithReceipt` in [`tools/tillr-item/item/state.go`](../../tools/tillr-item/item/state.go), `judgedCriterion(stored)` in `planDesignMirror` will remain `"c1"` unless reopening also calls `item.WithdrawVerdicts` ([`tools/tillr-item/item/withdraw.go`](../../tools/tillr-item/item/withdraw.go)) for `item-design-<slug>` when `<topic>.md` is still outstanding. Withdrawing `c1`'s prior stage-run verdict lets `planDesignMirror` update `seat` (`"design"` -> `"plan"`) and lets the subsequent `plan` / verification run record a fresh verdict on `c1`.
   - **Sequencing consequence:** Slice 3 (`item-design-*` reopening + verdict withdrawal) has zero dependency on Slice 2 and should land first or alongside Slice 2 so [`OQ-QM2`](./tillr-queue-mirrors-designs.md#OQ-QM2) `design` landings immediately transition to `seat = "plan"`.

2. **Trap — `TestRealQuestionIndexReaderRefusesToRunUnderGoTest` (`8d1bb0ee`).**
   - `RunIndexCommand` in [`tools/tillr/vantage/vantage.go`](../../tools/tillr/vantage/vantage.go) refuses default `uvx` invocations under `go test`. Every new `tillr-item` or `tillr-drain` test exercising companion-plan sync or reopening must pass an explicit `SyncInput.Index` (`vantage.ParseIndex`) or stub `QuestionsCmd`, mirroring [`tools/tillr-item/item/sync_test.go`](../../tools/tillr-item/item/sync_test.go) (`TestSyncOverThisRepositorysDocuments`) and [`tools/tillr-drain/drain/sync_design_hold_test.go`](../../tools/tillr-drain/drain/sync_design_hold_test.go).

3. **Trap — Per-step question holds alignment ([`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8), `2b1c42cd`).**
   - Under [`tillr-per-step-question-holds.md`](./tillr-per-step-question-holds.md) (`D-PSH1`, `D-PSH2`), `tillr item sync` must import `## Tasks` from a `DECIDED` `<topic>-plan.md` whenever `<topic>.md` is outstanding and unparked — whether `<topic>.md` has stage role `ready` (`DECIDED`), `open` (`DESIGN` / `SKETCH`), or `""` (unstaged) — and project `- **Waits on questions:**` references into `item_question_waits` without copying question `open`/`answered` state into SQLite.
   - `vantage-check` checks `planning/stage-disagrees` per file, so `<topic>-plan.md` with zero questions and `planning-actions: none` is valid at `stage: DECIDED` while `<topic>.md` remains `stage: DESIGN`.

4. **Trap — `RetireItemBlock` post-landing commit must skip companion-plan pieces (Slice 2).**
   - `retireDocumentBlock` in [`tools/tillr-drain/drain/retire.go`](../../tools/tillr-drain/drain/retire.go) runs after landing a document-backed item. Pieces imported from `<topic>-plan.md` (`body_ref = <topic>-plan.md#<slug>`, no HTML item comment) must skip `RetireItemBlock` (`D-QM19`) rather than failing or making a `chore: retire landed document block` commit.

5. **Trap — `deny-list.json` `item-declaration` guard blocks Slice 5 HTML comment removal unless updated in the same slice.**
   - [`config/tillr/deny-list.json`](../../config/tillr/deny-list.json) declares `{"content": "item-declaration", ...}`, enforced by `declarationViolation` in [`tools/tillr-drain/internal/boundary/deny_list.go`](../../tools/tillr-drain/internal/boundary/deny_list.go), which refuses any candidate diff that edits or removes an unfenced item declaration in `.md` files.
   - In Slice 5, removing live HTML item declarations from `docs/` and deleting `docshare` must simultaneously replace `item-declaration` in `deny-list.json` / `deny_list.go` (and `tools/gate/`) with the one-way gate refusal of newly added unfenced `<!-- tillr:` comments.

6. **Trap — Pack mirror sync on every `tools/tillr*` edit.**
   - Any task editing `tools/tillr*` or `config/tillr/workflows/*` must run `just format` (after `git add` on new Go files) and `just pack-sync` in the same commit (`yolo-packs/tillr/files/local/share/tillr/`). Also update [`tools/tillr/packdocs/packdocs.go`](../../tools/tillr/packdocs/packdocs.go) (`D-QM17`, `D-QM22`) so `*-plan.md` files are never exported to external workspace snapshots.

## Candidate slice breakdown (for the `plan` run to formalize into `## Tasks`)

- **Slice 3 (buildable immediately, 1 task) — Reopen finished `item-design-<slug>` rows when `<topic>.md` is outstanding (`D-QM6`):**
  - Extend `SetStateWithReceipt` / `checkMove` in [`tools/tillr-item/item/state.go`](../../tools/tillr-item/item/state.go) and `planDesignMirror` in [`tools/tillr-item/item/sync_design.go`](../../tools/tillr-item/item/sync_design.go) so a `landed`, `closed`, or `resolved-elsewhere` `item-design-<slug>` row whose routed `<topic>.md` has an outstanding stage role (`""`, `open`, `ready`) is reopened with receipt `By: "tillr-item/sync"`, withdraws prior `c1` verdicts via `item.WithdrawVerdicts`, updates `seat` (`"plan"` vs `"design"`) and `blocked: icebox` state, and ranks after existing ranked spine items if unranked. Also release `needs-companion-plan-slice` fault holds once Slice 2 lands or `<topic>-plan.md` updates.

- **Slice 2 (2–3 tasks) — Companion-plan `## Tasks` reader, `plan-has-question` / `plan-task-invalid` checks, piece Icebox & question-wait projection, and skipping `RetireItemBlock` (`D-QM10`, `D-QM11`, `D-QM16`–`D-QM19`):**
  - Task 2a: Parse and validate `DECIDED` `<dir>/<stem>-plan.md` (`depends-on: [<stem>.md]`, `planning-actions: none`, zero `question`/`oq` directives -> `plan-has-question`, `## Tasks` -> `### Task <local_id>: <Imperative title>` metadata bullets and `c1..c8` criteria -> `plan-task-invalid`) in `tools/tillr-item/item/`, materialize `item-<plan-slug>-<local_id>` (or explicit `- **ID:**`) rows with `from` edge to `item-design-<slug>`, park/unpark pieces on `icebox` alongside `<stem>.md`, and add `tillr item sync --dry-run` (plus excluding `*-plan.md` in `tools/tillr/packdocs`).
  - Task 2b: Project `- **Waits on questions:**` into `item_question_waits` (coordinating with [`tillr-per-step-question-holds.md`](./tillr-per-step-question-holds.md) `D-PSH2`), evaluate piece-scoped `question-open` / `question-removed` holds in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go), and skip `retireDocumentBlock` in [`tools/tillr-drain/drain/retire.go`](../../tools/tillr-drain/drain/retire.go) for companion-plan pieces.

- **Slice 4 (2 tasks) — Two-run `item-design-<slug>` lifecycle, `waits-on-pieces`, and waiting/`why`/sync surfaces (`D-QM8`, `D-QM13`, `D-QM14`, `D-QM20`):**
  - Task 4a: Replace Slice 1's temporary `needs-companion-plan-slice` hold in [`tools/tillr-drain/drain/node_brief.go`](../../tools/tillr-drain/drain/node_brief.go) and [`tools/tillr-drain/drain/workflow.go`](../../tools/tillr-drain/drain/workflow.go) with the companion-plan `plan` run (writing `DECIDED` `<topic>-plan.md` when work remains, or stamping `stage: BUILT` on `<topic>.md` and `<topic>-plan.md` when verified on `HEAD`), and hold `item-design-<slug>` on `ReasonWaitsOnPieces` (`waits-on-pieces`) in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) while any non-closed piece of `<topic>.md` is unfinished.
  - Task 4b: Order `icebox` -> `question-open` / `questions-unknown` -> `waits-on-pieces` ahead of loop-wide holds in [`tools/tillr-drain/drain/why.go`](../../tools/tillr-drain/drain/why.go) and [`tools/tillr-view/`](../../tools/tillr-view/), render companion-plan counts in `tillr item sync`, and render the three quiet states in `tillr view waiting`.

- **Slice 5 (2 tasks) — Migrate live HTML item blocks and delete comment-scanning machinery (`D-QM15`, `D-QM21`, `D-QM22`):**
  - Task 5a: Migrate any remaining unfinished unfenced item blocks in `docs/` into their parent design's `<topic>-plan.md` (preserving `- **ID:**`) or standalone `tillr item request` rows, strip landed HTML item blocks, and update [`docs/reference/tillr-queue-persistence.md`](../reference/tillr-queue-persistence.md) and [`docs/design/tillr-intake-paths.md`](./tillr-intake-paths.md).
  - Task 5b: Delete `tools/tillr-item/docshare/`, `parseItemBlocks`, `retire.go` / `drain/retire.go`, `owner_question.go`, `reconcile.go`, `design_without_item.go`, `prose.go` / `prose_rewrite.go`, and `packdocs` directive neutralization; replace `item-declaration` in `config/tillr/deny-list.json` / `deny_list.go` with a gate check refusing unfenced `<!-- tillr:` comments in tracked Markdown.
