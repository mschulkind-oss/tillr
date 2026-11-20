---
title: "Work reaches tillr through several doors, and every door ends in the same shape"
status: draft
stage: DESIGN
updated: 2026-11-19
tags:
  - tillr
  - design
  - intake
  - triage
  - boundaries
summary: "The owner's idea that each way work enters tillr (an agent files an item, the owner dumps a ramble, a worker raises a conflict) runs through its own small graph of steps that ends in one fixed item shape, so planning, build, gate and landing never learn which door was used. It records what the owner said and when, restates the idea, measures from the code what sync and the planner check today and what each door has, and lists what is not yet settled."
next: "Talk through the points under Not yet settled; the prose-rewrite, idea-save, no-block triage and command-line request paths are built separately, while a unified cross-door intake graph is not."
depends-on:
  - ../reference/tillr-item-graphs.md
  - tillr-idea-pipeline.md
  - tillr-core-and-git-landing.md
---

# Work reaches tillr through several doors, and every door ends in the same shape

**Status:** 2026-11-17; updated against the tree on 2026-11-19. A proposal written from the owner's statements; the general cross-door intake graph is not built as its own item, while the individual prose-rewrite, idea-save, no-block triage and command-line request paths have landed separately (`tools/tillr-item/item/prose.go`, `prose_rewrite.go`, `sync.go`, `idea.go`, `request.go`, `tools/tillr-drain/drain/plan.go`, `document_prerequisite.go`, `prose_rewrite.go`, `idea.go`, `request.go`).

> **In short.** Work should enter tillr by whichever door suits who or what is bringing it, each door should run a small graph of steps that makes the request ready, and every graph should end in one fixed item shape, so everything after the doors depends on that shape and never on the door.

**Why it matters.** Each intake path is currently wired as its own rule or store-declared special case across sync and the drain rather than as one shared intake-subgraph contract, and some checks are gated by repository settings or by whether the store is document-fed.

**Reads with:** [item graphs](../reference/tillr-item-graphs.md) (the graphs the doors are made of), [the idea pipeline](./tillr-idea-pipeline.md#2-the-pipeline-and-one-writer-rules) (the earlier flow from an idea to a landed commit), [the observatory's first two actions](../reference/observatory-actions.md#idea-intake-and-the-design-turn) (the owner's idea door in the browser), [the core and the landing definition](./tillr-core-and-git-landing.md) (where the use case ends and the core begins).

## Words this document uses

- **Door** *(coined here)*: a way work enters tillr, such as an agent filing an item or the owner dumping an idea.
- **Item shape** *(coined here)*: the fixed form an item has when it leaves its door's steps, described in [section 2](#2-the-shape-every-door-must-end-in).
- **Guide mark**: the attribute `prose="2"` on an item's directive. The only document that tells an author to add it is the writing guide, the `tillr-prose-style` skill; sync and rewrite checks verify that the mark is `"2"` and that the block passes the guide's observable shape rules, not that the author read the guide. The owner called this a shibboleth, a password that proves the writer was told it.
- **Triage**: reading a request nobody has shaped yet and deciding what it needs next. In this document it is one step of a door's graph, not a separate system.

## 1. What the owner said

Times are the owner's local time. The quotes come from design conversations with a coordinating agent; the first four are the owner, transcribed from speech.

**The evening of 2026-11-15, an earlier design conversation about the observatory.** On the writing guide not being applied:

> the skill ... can have some sort of shibboleth ... if the agent doesn't use that in the description, we know that it has not read that guide. And then the first thing that should happen ... after that item hits the queue even is it should get a high priority item to get rewritten correctly.

That became the guide mark, a sync check, and the queued rewrite item described below.

**The same evening, a few minutes later.** On the first step of a ticket:

> I ramble into a ticket. You then create an item. The first thing is a designer because it's like a rambling ticket. And then that designer writes a design doc or maybe that designer decides it needs scouting first. So it replaces itself with four scouts and a designer that joins them.

That became the scouted-design workflow of [item graphs](../reference/tillr-item-graphs.md#shipped-workflows-and-command-registry) and the ruling [`OQ-IP1`](./tillr-idea-pipeline.md#61-settled-decisions) of the idea pipeline.

**Just after midnight, 2026-11-16.** On a button that saves a ramble:

> I want to be able to hit a button, give some indication of a priority, and then just dump in a whole bunch of rambling thoughts and hit save, and then have that go through that designer loop ... it'll add real title, description, all that stuff for my words, it'll of course save my words, make a design doc if it needs me ... I feel like we can just have an intake path that works real well like that.

That became [the idea save and design turn](../reference/observatory-actions.md#idea-intake-and-the-design-turn).

**2026-11-17.** On an item that arrives with no document block:

> if it ends up like this, it's probably that's some half-baked request for me and it should be able to go to ... the next step, which is a design agent or a triage agent or something like that. Like there should always be a next step that's clear for these items and we should handle them somehow.

**2026-11-17, asking for this dig.** The two statements this document answers:

> we had this shibboleth idea where if an agent files a ticket without having read the skill, it should first go to a rewriting agent to make nice and then there's a similar thing here if I dump in some half-baked idea with ramblings it should go to a rewriting agent which then writes up a real ticket ... and then it should go to an agent that or maybe it's the same agent ... that turns it into some idea that can actually be designed ... and then hands it off to designer scouts whatever is down the line there. different intake paths lead to different shapes of DAGs that process them or subsections of those DAGs.

> You can imagine a triage subgraph which has the same output shape as like a human half-baked triage has the same output shape as like an agent filed item, even though they go through different paths to get there. The downstream stuff doesn't care because it's ultimately aligned in the interface.

What the search did not find: before this document, the word "shibboleth" appeared in no tracked document, only in the owner's message of the first evening and the assistant's reply to it. No earlier conversation or design proposed one agent for both rewriting and shaping; that comes only from the 2026-11-17 statement.

## 2. The shape every door must end in

Under this proposal, document-declared work leaves the doors as a document block that a build worker can be sent to. Store-declared items — saved ideas (`idea_intake`), command-line requests (`request_words`), items carrying `workflow_args` such as `doc-triage`, and generated items (`IsGeneratedItem`, including `rewrite-*`, `fixer-main-*`, `defect-*`, `revert-*` and legacy `gate-budget-*`) — deliberately carry no document block of their own and are excluded from the missing-block prerequisite (`tools/tillr-item/item/sync.go`, `tools/tillr-drain/drain/document_prerequisite.go`). For document blocks, the parts and who checks each today:

| Part | Checked by sync (before the item is stored) | Checked by the planner (before dispatch) |
| :--- | :--- | :--- |
| An id | Refused when missing or duplicated | Not rechecked |
| A title | Refused when missing; warns `prose-shape` when it does not start with an imperative verb from the guide or exceeds 12 words | With `prose.rewrite = true`, holds `awaits-rewrite` while the open `rewrite-<id>` item has not landed |
| A why paragraph | Warns `prose-shape` when the section has no prose paragraph before the directive | With `prose.rewrite = true`, holds `awaits-rewrite` while the open `rewrite-<id>` item has not landed |
| A fence of allowed paths | Refused when missing | Holds `no-fence` when the fence admits nothing |
| Dependencies | Refused for an unknown or unavailable blocker | Holds `blocked` until each blocker is done |
| Checkable criteria | Refused when there is none or one is empty; warns `prose-shape` above eight | With `prose.rewrite = true`, holds the original on `awaits-rewrite` and holds `rewrite-<id>` on `prose-criteria-count` for the owner when criteria exceed eight |
| The guide mark | Warns `prose-unmarked` when absent or not `2` | With `prose.rewrite = true` (default `false`; enabled in `.tillr.toml`), holds `awaits-rewrite` while the open `rewrite-<id>` item has not landed |
| No open question on its document | Not read | Holds `question-open` or `questions-unknown` |
| A document block at all | Counted as `unshared_open` unless generated or store-declared | In a document-fed Git-landing store, routes an unshared open item (excluding generated items and saved ideas) to `triage-no-block` on the `design` seat; in a document-free store, not held |

Checks remain split across sync and the drain's prerequisites rather than enforced by one universal end-of-intake checker. With `prose.rewrite = false` (the default in `settings.go`), `prose-unmarked` and `prose-shape` stay advisory warnings and the planner dispatches the item unchanged; in this repository `.tillr.toml` sets `prose.rewrite = true`, so sync creates `rewrite-<id>` on the `build` seat and `documentPrerequisites` holds the original on `awaits-rewrite` until that rewrite lands or the block passes the check (`TestProseRewriteHoldAndRelease`), while `validateProseRewrite` refuses contract changes before the gate (`TestProseRewriteContractRefusedBeforeGate`). In a document-fed Git-landing store, an unshared open item is routed through `triageDecision` to `triage-no-block` on the `design` seat before its stored seat or workflow runs (`TestDocumentFedPrerequisiteRoutesBeforeSeatAndWorkflow`), rather than being sent straight to a build worker as happened on 2026-11-16; a document-free store still dispatches without requiring blocks (`TestDocumentFreeStoreKeepsItsOwnSeats`). The mark `prose="2"` remains a constant in `prose.go`, not a setting, so changing the guide means changing the code, and an item written to an earlier guide shows `prose-unmarked` too.

## 3. The doors: what exists, what is queued, what is missing

| Door | Mechanism today | Covered by | Gap |
| :--- | :--- | :--- | :--- |
| **An agent files an item without the guide mark** | With `prose.rewrite = false` (default), sync warns `prose-unmarked` and `prose-shape` and the item dispatches unchanged. With `prose.rewrite = true` (set in `.tillr.toml`), sync creates `rewrite-<id>` on the `build` seat, the planner ranks it ahead of ordinary items and holds the original on `awaits-rewrite` until the rewrite lands or the block passes, and `validateProseRewrite` protects the block's contract before the gate (`tools/tillr-item/item/prose_rewrite.go`, `tools/tillr-drain/drain/document_prerequisite.go`, `prose_rewrite.go`) | Landed (`item-prose-rewrite-before-build`, [`roadmap.md`](../tillr/roadmap.md)) | Opt-in via `prose.rewrite`; blocks with more than eight criteria hold `rewrite-<id>` on `prose-criteria-count` for the owner because preserving every criterion cannot pass the guide |
| **The owner dumps a rough idea** | Built: `tillr-item idea` (`tools/tillr-item/item/idea.go`) and `POST /api/v1/ideas` (`tools/tillr-view/serveidea.go`) save the words verbatim and record the chosen place (`TestIdeasHTTPSavesWordsAndShowsIntake`), then one `design`-seat turn (`tools/tillr-drain/drain/idea.go`) copies the words into the tree and writes `from`-marked item blocks or a design document with open questions (`TestIdeaStageCarriesWordsGuideAndInstructions`, `TestIdeaLandingImportsOutcomesAtItsPlace`) | Landed (`item-oa-idea-save` and `item-oa-idea-design-turn` in [the observatory's first actions](../reference/observatory-actions.md#idea-intake-and-the-design-turn)); `intake.dir` and `intake.paths` are unset in `.tillr.toml` today, so `tillr-item idea` currently refuses with `intake-not-configured` after preserving the words file | One `design`-seat turn rather than separate rewrite-then-shape steps; the idea item itself lands without a block of its own and produces `from`-marked blocks |
| **An item has no document block** (filed with `apply`, vs store-declared `request` / `doc-triage`) | Built for `apply`: in a document-fed Git-landing store, `documentPrerequisites` routes an unshared open item (excluding generated items and saved ideas) through `triageDecision` to `triage-no-block` on the `design` seat, and `validateTriage` checks the resulting block (`tools/tillr-drain/drain/document_prerequisite.go`, `TestDocumentFedPrerequisiteRoutesBeforeSeatAndWorkflow`). Separately, `tillr-item request` (`tools/tillr-item/item/request.go`) and `tillr-item doc-triage` (`doc_triage.go`) file store-declared items that never declare or touch a document block | Landed (`item-sync-hold-unshared-items`, [`roadmap.md`](../tillr/roadmap.md)), plus `tillr-item request` and `tillr-item doc-triage` | Scoped to document-fed Git-landing stores; store-declared requests finish without a document block rather than converging on the [section 2](#2-the-shape-every-door-must-end-in) block shape |
| **A worker raises a conflict** | Built: a blocked report becomes a conflict routed through `tools/tillr-drain/drain/blocked.go`, `stage.go` and `owner_question.go`; the `design` seat answers by amending the block or writing an owner question that lands through the gate | None needed for the hold-and-ruling path itself | Remains a conflict/hold route rather than a separate intake graph drawn as a door |
| **An item arrives complete with the mark** | Built: sync stores it, the planner dispatches it | None | None; this is the document-block door the [section 2](#2-the-shape-every-door-must-end-in) shape is modeled on |

## 4. How this sits in the larger direction

[Item graphs](../reference/tillr-item-graphs.md) give an item a graph of nodes, each one agent or one command, and say a node may replace a later unstarted node with several. Each door's steps are exactly that: a rewrite node, a triage node, a designer that fans out to scouts. The item shape of [section 2](#2-the-shape-every-door-must-end-in) is the interface between those intake subgraphs and the build graph: a subgraph with a declared output shape that downstream nodes depend on is the general mechanism, and it belongs to the core. Which doors exist, what shape they end in, and which checks a shape needs belong to the layer that describes a use case, as [the core and the landing definition](./tillr-core-and-git-landing.md#1-the-direction-in-the-owners-words) records: the owner put "item prerequisites" there. Landing a change in Git wants documents, blocks and the guide mark. Another use has other doors and another shape; the Product QA pipeline files an item with `apply` into a store of its own, and a bug it confirms is not a rambling request at all.

## Not yet settled

These are for the design conversation, not for an agent to decide. They are not question directives because the documents that hold queued items would be held by them.

- **One triage agent or two.** Today the built paths split differently by door: `prose.rewrite` runs a separate form-preserving rewrite item before build, while saved-idea design and `triage-no-block` each use one `design`-seat run that shapes the work. No ruling settles whether normalization and shaping across doors belong to one shared triage agent or two separate steps.
- **Which seat.** Today `rewrite-<id>` uses the `build` seat, saved ideas and `triage-no-block` use the `design` seat, and the `plan` seat and `planned-build@v1` workflow of [planning and model tiering](./tillr-planning-node-model-tiering.md#5-the-planning-stage-as-built) landed on 2026-11-18 (`tillr-item request` defaults `build`-seat requests to `planned-build@v1`, though no document-declared item directive selects it yet). Whether one seat should unify intake across doors remains unset.
- **What the guide mark means once a rewrite step exists.** Operationally, with `prose.rewrite = true`, `prose-unmarked` and `prose-shape` findings hold the item on `awaits-rewrite` until the rewrite lands or the block passes those observable checks (`TestProseRewriteHoldAndRelease`). The check verifies `prose="2"` and shape rules rather than proving the author read the guide, so whether an agent can satisfy the check without reading the guide — and whether anything stronger is wanted — remains open.
- **What the owner types.** For the built idea door ([the observatory's first actions](../reference/observatory-actions.md#idea-intake-and-the-design-turn)), the owner uses `tillr-item idea` or the browser form (`POST /api/v1/ideas`), with the words saved verbatim and a selected place; `tillr-item request` separately saves command-line request words verbatim in `request_words`. Whether dropped documents or coordinator chat should also be first-class intake doors, and whether every door keeps verbatim words beside a shaped block, remains unset.
- **Whether a deterministic checker enforces the shape.** Today checks stay distributed across `sync.go`, `document_prerequisite.go` (`validateTriage`), `prose_rewrite.go` (`validateProseRewrite`) and `idea.go` (`validateIdea`). Whether one universal deterministic checker should gate the output of every intake door remains open.
- **Whether the conflict door is drawn as a door**, with its own intake graph and the same declared output shape as the other doors, or stays what it is today (`blocked.go`, `stage.go`, `owner_question.go`), a conflict hold with a design or owner ruling.
