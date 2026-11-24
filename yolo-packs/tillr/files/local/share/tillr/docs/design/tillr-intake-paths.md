---
title: "Work reaches tillr through several doors, and every door ends in the same shape"
status: draft
stage: DESIGN
updated: 2026-11-24
tags:
  - tillr
  - design
  - intake
  - triage
  - boundaries
summary: "Each way work enters tillr (an agent files an item, the owner saves a rough idea, a worker raises a conflict) runs through its own small graph of steps that ends in one fixed item shape, so planning, build, gate and landing never learn which door was used. States what sync and the planner check, what each door does, and what is not settled."
next: "Talk through the points under Not yet settled. Each door is built on its own; one shared contract for every door's output is not."
depends-on:
  - ../reference/tillr-item-graphs.md
  - tillr-idea-pipeline.md
  - tillr-core-and-git-landing.md
---

# Work reaches tillr through several doors, and every door ends in the same shape

> **Delivered copy.** This is `docs/design/tillr-intake-paths.md` from this repository at commit `0275c51d52` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-intake-paths.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** a proposal. Each door below is built as its own rule; the shared contract this document proposes, one declared output shape that every door's steps must end in, is not built.

> **In short.** Work should enter tillr by whichever door suits who or what is bringing it, each door should run a small graph of steps that makes the request ready, and every graph should end in one fixed item shape, so everything after the doors depends on that shape and never on the door.

**Why it matters.** Each intake path is wired as its own rule across sync and the drain, and some checks depend on a repository setting or on whether documents feed the store. Nothing states what "ready to build" means once, for every door.

**Reads with:** [item graphs](../reference/tillr-item-graphs.md) (the graphs the doors are made of), [the idea pipeline](tillr-idea-pipeline.md#2-the-pipeline-and-one-writer-rules) (the flow from an idea to a landed commit), [the observatory's first two actions](../reference/observatory-actions.md#idea-intake-and-the-design-turn) (the owner's idea door in the browser), [the core and the landing definition](tillr-core-and-git-landing.md) (where a use ends and the core begins).

## Words this document uses

- **Door** *(coined here)*: a way work enters tillr, such as an agent filing an item or the owner saving an idea. Not a seat and not a workflow: a door is where the work came from, and it may run several of each.
- **Item shape** *(coined here)*: the fixed form an item has when it leaves its door's steps, described in [section 1](#1-the-shape-every-door-must-end-in).
- **Guide mark**: the attribute `prose="2"` on an item's directive. The only document that tells an author to add it is the writing guide, the `tillr-prose-style` skill. The checks verify the mark and the block's observable shape, not that the author read the guide.
- **Triage**: reading a request nobody has shaped yet and deciding what it needs next. Here it is one step of a door's graph, not a separate system.

## 1. The shape every door must end in

Work declared in a document leaves its door as a document block that a build worker can be sent to. Items the store declares itself carry no block and are exempt from the missing-block prerequisite: saved ideas, command-line requests, items filed with workflow arguments such as `doc-triage`, and generated items (`IsGeneratedItem`). For document blocks, the parts and who checks each:

| Part | Checked by sync, before the item is stored | Checked by the planner, before dispatch |
| :--- | :--- | :--- |
| An id | Refused when missing or duplicated | Not rechecked |
| A title | Refused when missing; warns `prose-shape` when it does not start with an imperative verb from the guide or exceeds 12 words | Held `awaits-rewrite` (see below) |
| A why paragraph | Warns `prose-shape` when the section has no prose paragraph before the directive | Held `awaits-rewrite` |
| Dependencies | Refused for an unknown or unavailable blocker | Held `blocked` until each blocker is done |
| Checkable criteria | Refused when there is none or one is empty; warns `prose-shape` above eight | Held `awaits-rewrite`; above eight, the rewrite item is held `prose-criteria-count` for the owner |
| The guide mark | Warns `prose-unmarked` when absent or not `2` | Held `awaits-rewrite` |
| No open question on its document | Not read | Held `question-open` or `questions-unknown` |
| A document block at all | Counted as `unshared_open` unless generated or store-declared | In a store fed by documents, routed to `triage-no-block` on the `design` seat; in a store with no documents, not held |

The checks are split across sync and the drain's prerequisites; no single checker runs at the end of intake.

- **The rewrite hold is a setting.** With `prose.rewrite = false`, the default, `prose-unmarked` and `prose-shape` are warnings and the planner dispatches the item unchanged. With `prose.rewrite = true`, which this repository's `.tillr.toml` sets, sync creates a `rewrite-<id>` item on the `build` seat and the planner holds the original on `awaits-rewrite` until that rewrite lands or the block passes (`TestProseRewriteHoldAndRelease`). `validateProseRewrite` refuses a rewrite that changes the block's contract before the gate (`TestProseRewriteContractRefusedBeforeGate`).
- **The missing-block route depends on the store.** In a store fed by documents, an open item with no block goes to `triage-no-block` on the `design` seat before its stored seat or workflow runs (`TestDocumentFedPrerequisiteRoutesBeforeSeatAndWorkflow`). A store with no documents dispatches without requiring blocks (`TestDocumentFreeStoreKeepsItsOwnSeats`).
- **The guide mark is a constant, not a setting.** Changing the guide means changing the code, and a block written to an earlier guide shows `prose-unmarked` too.

## 2. The doors

| Door | Mechanism | Gap against one shared shape |
| :--- | :--- | :--- |
| **An agent files an item without the guide mark** | The rewrite hold above: a `rewrite-<id>` item ranks ahead of ordinary items, and the original waits on `awaits-rewrite` (`tools/tillr-item/item/prose_rewrite.go`, `tools/tillr-drain/drain/prose_rewrite.go`) | Opt-in through `prose.rewrite`. A block with more than eight criteria waits for the owner, because keeping every criterion cannot pass the guide |
| **The owner saves a rough idea** | `tillr-item idea` and `POST /api/v1/ideas` save the words verbatim with the chosen place (`TestIdeasHTTPSavesWordsAndShowsIntake`). One `design`-seat turn copies the words into the tree and writes item blocks marked `from` the idea, or a design document with open questions (`TestIdeaStageCarriesWordsGuideAndInstructions`, `TestIdeaLandingImportsOutcomesAtItsPlace`) | One turn does both the rewriting and the shaping. The idea item itself has no block. This repository's `.tillr.toml` sets no `intake` table, so `tillr-item idea` saves the words file and refuses with `intake-not-configured` |
| **An item has no document block** | For an item filed with `tillr-item apply` into a store fed by documents: the `triage-no-block` route above, whose resulting block `validateTriage` checks. Separately, `tillr-item request` and `tillr-item doc-triage` file items the store declares, which never gain a block | The triage route applies only to stores fed by documents. A store-declared request finishes without converging on the [section 1](#1-the-shape-every-door-must-end-in) shape |
| **A worker raises a conflict** | A blocked report becomes a conflict (`tools/tillr-drain/drain/blocked.go`). The `design` seat answers by amending the block or by writing an owner question that lands through the gate (`owner_question.go`) | It is a hold with a ruling, not an intake graph drawn as a door |
| **An item arrives complete with the mark** | Sync stores it and the planner dispatches it | None. This is the door the item shape is modeled on |

## 3. How this sits in the larger direction

[Item graphs](../reference/tillr-item-graphs.md) give an item a graph of nodes, each one agent or one command, and let a node replace a later unstarted node with several. Each door's steps are exactly that: a rewrite node, a triage node, a designer that fans out to scouts. The item shape of [section 1](#1-the-shape-every-door-must-end-in) is the interface between those intake subgraphs and the build graph.

A subgraph with a declared output shape that downstream nodes depend on is a general mechanism, and it belongs to the core. Which doors exist, what shape they end in, and which checks a shape needs belong to the layer that describes one use, where [the core and the landing definition](tillr-core-and-git-landing.md#1-the-direction-in-the-owners-words) places item prerequisites. Landing a change in Git wants documents, blocks and the guide mark. Another use has other doors and another shape: a store whose items are findings is fed by `apply`, and a confirmed bug is not a rough request at all.

## Not yet settled

These are for the design conversation, not for an agent to decide. They are not question directives, because a document with an open question holds the queued items it declares.

- **One triage agent or two.** The rewrite door runs a separate, form-preserving rewrite before build; the idea door and `triage-no-block` each use one `design`-seat run that shapes the work. Whether normalizing and shaping belong to one shared step or two is not ruled.
- **Which seat.** `rewrite-<id>` uses the `build` seat; saved ideas and `triage-no-block` use the `design` seat; `tillr-item request` defaults a `build`-seat request to the `planned-build@v1` workflow of [planning and model tiering](tillr-planning-node-model-tiering.md#5-the-planning-stage-as-built). Whether one seat should own intake across doors is not set.
- **What the guide mark means once a rewrite step exists.** The check verifies `prose="2"` and the shape rules, so an agent can satisfy it without reading the guide. Whether anything stronger is wanted is open.
- **What the owner types.** The idea door and `tillr-item request` both keep the owner's words verbatim. Whether a dropped document or a chat with a coordinating agent should also be a door, and whether every door keeps verbatim words beside a shaped block, is not set.
- **Whether one deterministic checker enforces the shape.** The checks live in `sync.go`, `document_prerequisite.go` (`validateTriage`), `prose_rewrite.go` (`validateProseRewrite`) and `idea.go` (`validateIdea`). Whether one checker should gate the output of every door is open.
- **Whether the conflict door is drawn as a door,** with its own intake graph and the same declared output shape, or stays a hold with a design or owner ruling.
