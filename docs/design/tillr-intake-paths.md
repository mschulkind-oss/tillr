---
title: "Work reaches tillr through several doors, and every door ends in the same shape"
status: in-review
stage: DESIGN
updated: 2026-11-27
tags:
  - tillr
  - design
  - intake
  - items
summary: "Work enters tillr through mirrored roadmaps and companion plans, saved ideas, owner review comments, requests, alert triage, document triage, split parts, loop repairs and structured records. Every door ends in one stored item record, not in a hidden item comment in a document. States what each door stores, what retiring the item comments removes from intake beyond the queue-mirrors deletion list, what a saved idea's turn must produce afterwards, and one field that records the door."
next: "The owner rules OQ-IN1. Slices 1 and 2 of Section 6 wait on no question in this document; Slice 2 follows Slice 5 of tillr-queue-mirrors-designs.md."
depends-on:
  - tillr-core-and-git-landing.md
  - tillr-queue-mirrors-designs.md
  - tillr-plan-step-and-seat-selection.md
  - tillr-request-amendment.md
vantage:
  status-chip: true
---

# Work reaches tillr through several doors, and every door ends in the same shape

**Status:** Partly built, checked against the tree on 2026-11-27. Every door in [Section 2](#2-the-doors) exists except companion-plan task sync and the command-line comment relay, which their own designs track. Nothing this document adds is built: `rg 'intake_source|IntakeSources' tools/` returns nothing.

> **In short.** Work enters tillr by whichever door suits who is bringing it. Every door ends in one **stored item record**: a row in the store with criteria, dependencies, a first seat and, where a person wrote the request, their words kept beside it. Nothing after the doors depends on which door was used.

**Why it matters.** The earlier draft of this document treated a hidden `<!-- tillr: item -->` comment in a document as the shape every door had to reach, then listed an exception for each door that never writes one. The owner has since ruled those comments out ([`R-IN3`](#R-IN3)). The design that retires them, [the queue mirrors the designs](./tillr-queue-mirrors-designs.md), lists what it deletes, and that list leaves three pieces of intake unassigned. This document assigns them.

**The shape.** One stored record per item ([Section 1](#1-the-shape-every-door-ends-in)); nine doors that write it ([Section 2](#2-the-doors)); what changes for intake when the item comments go ([Section 3](#3-what-retiring-the-item-comments-changes-for-intake)); and one field that records the door ([Section 4](#4-one-field-records-the-door)).

**Cost.** One column on `items`, a changed finishing rule for the saved-idea turn, and the deletion of the missing-block triage route with the code that exempts items from it.

**Needs your ruling:** [`OQ-IN1`](#OQ-IN1), whether this repository's queue keeps accepting a bare `tillr item apply` once the missing-block triage route is gone. Six designer's decisions (`D-IN1` to `D-IN6`) are in the [ledger](#8-decision-ledger); the owner may overturn any of them.

**Reads with:** [the core and the `git-landing` charter](./tillr-core-and-git-landing.md) (what belongs to a charter), [the queue mirrors the designs](./tillr-queue-mirrors-designs.md) (roadmap sync, companion plans, and the deletion list), [plan step and seat selection](./tillr-plan-step-and-seat-selection.md) (which seat a request starts on), [request amendment](./tillr-request-amendment.md), [the idea pipeline](./tillr-idea-pipeline.md), [provisional design documents](./provisional-design-docs.md), [owner review comments](./tillr-review-comments.md), [alert triage inside tillr](./tillr-alert-triage-seat.md) and [the escalation ladder](./escalation-ladder.md).

## Words this document uses

**Item**, **criterion**, **seat**, **hold** and **charter** (`definition` in code) keep their meanings from [the vocabulary](./tillr-vocabulary.md). Three more:

- **Door** *(coined here)*: a way work enters tillr, such as a roadmap sync or a saved idea. A door is where an item came from. It is not a seat and not a workflow.
- **Item comment**: the hidden `<!-- tillr: item ... -->` comment in a tracked Markdown file that sync reads as an item's declaration. The code calls it a block.
- **Missing-block triage route**: the planner rule that sends an open item with no item comment to the `design` seat, with hold reason `triage-no-block`, to write one.

## 1. The shape every door ends in

Under **`D-IN1`** (*designer's decision; the owner may overturn this*), the shape every door ends in is the stored item record. It is not an item comment in a document.

| Part | Stored in | Rule |
| :--- | :--- | :--- |
| Identity and title | `items.item_id`, `items.title` | The door derives the id, so filing the same thing twice names the same item ([Section 5.3](#53-duplicates)) |
| State and rank | `items.state`, `items.rank`, `items.waiting_on` | A state of the charter's lifecycle; every wait names its next step and actor ([`R-IN1`](#R-IN1)) |
| Criteria | `item_criteria` | At least one, each checkable; no item finishes without a recorded verdict on each ([`R-IN1`](#R-IN1)) |
| Dependencies and design link | `item_edges`, `items.design_doc` | An unfinished blocker holds the item; the design link is what question holds and comment holds read |
| First seat and workflow | `items.seat`, `items.workflow`, `items.workflow_args` | Set by the door ([Section 5.1](#51-no-single-seat-owns-intake)) |
| The door | `items.intake_source` (`D-IN2`, not built) | Written once, when the row is created ([Section 4](#4-one-field-records-the-door)) |
| The source's own words | The door's side record: `idea_intake` and its words file, `request_words`, `comment_batches`, `item_splits`, `alert_item_links` | Written in the same transaction as the row; never rewritten ([Section 5.2](#52-the-words-are-kept-and-nothing-rewrites-them)) |

**One writer.** One function maps a record to an item row (`Apply` in [`tools/tillr-item/item/apply.go`](../../tools/tillr-item/item/apply.go)). The loop's own filings go through it too (`fileDefectItem` in [`tools/tillr-drain/drain/defect.go`](../../tools/tillr-drain/drain/defect.go) calls `item.Apply`). The item's own row, criteria and edges are therefore checked in one place at filing. The earlier draft asked whether one checker should enforce the shape; that function is it, and no second checker runs at the end of intake.

**There is no per-item path list in the shape.** `items.allowed_paths` exists in the schema, and the owner retired it as a fence ([`R-IN4`](#R-IN4)). No door is required to fill it.

## 2. The doors

| Door | Who uses it, and how | First seat | The source it keeps | In the tree |
| :--- | :--- | :--- | :--- | :--- |
| **A design a roadmap routes** | `tillr item sync` reads a mirrored roadmap and keeps one item per unfinished design | `design` while the design is open; `plan` once it is accepted with no unanswered question ([`R-IN8`](#R-IN8)) | The design document at `items.design_doc` | Built (`sync_design.go`) |
| **A task in a companion plan** | `tillr item sync` reads `## Tasks` in `<topic>-plan.md` | `build` | The task's section in the plan | Not built; Slice 2 of [queue mirrors designs](./tillr-queue-mirrors-designs.md#11-rollout-sequence-high-level) |
| **A saved idea** | `tillr item idea`, or the observatory's idea box (`POST /api/v1/ideas`) | `design`, one turn | `idea_intake` and the words file, byte for byte | Built (`idea.go`). Its turn must still end in item comments ([Section 3.2](#32-what-a-saved-ideas-turn-produces)) |
| **An owner review comment or ruling** | Vantage's review batch through `tillr item comments take`; the command-line relay `tillr item comment` | `design`, at the document's own desk | `comment_batches` | Vantage path built (`comments.go`); the relay and its track are not ([owner review comments](./tillr-review-comments.md#53-build-outline)) |
| **A request** | `tillr item request`, from the owner, the operator, or the `triage` seat | `build`, or `design` when its paths lie inside `docs/design/` ([`R-IN6`](#R-IN6)) | `request_words`, byte for byte | Built (`request.go`); unseated filing and amendments are not |
| **A standing alert** | The drain hands an alert whose rule delivers to `triage` to the read-only `triage` seat, which links an open item or files a request | `triage` ([`R-IN9`](#R-IN9)) | `alert_item_links`, and `request_words.dedup_key` on a request it files | Built (`alert_triage.go`) |
| **A document triage** | `tillr item doc-triage` names up to 48 documents | One `scout` reader per document, then one `design` report writer (`doc-triage@v1`) | The document list in `items.workflow_args` | Built (`doc_triage.go`) |
| **Split parts and loop repairs** | A `design` run's split statement; the loop's trunk fixer, defect, flaky-check and revert filings | `build` | `item_splits` and each part's `request_words`; a repair's evidence is in its criteria | Built (`split.go`, `defect.go`, `trunk.go`) |
| **A structured record** | `tillr item apply` with an `item.record` | Whatever the record names | The record itself | Built (`apply.go`); whether this repository keeps it open is [`OQ-IN1`](#OQ-IN1) |

Three things the earlier draft listed as doors are not doors:

- **An item comment written to the prose guide** (the `prose="2"` mark) and **the rewrite of one that lacks the mark** (`rewrite-<id>`). Both retire with the item comments ([`R-IN3`](#R-IN3), [`R-IN5`](#R-IN5)). Until Slice 5 of [queue mirrors designs](./tillr-queue-mirrors-designs.md#82-what-is-deleted-d-qm22) lands, they still run: this repository's `.tillr.toml` sets `[prose] rewrite = true`.
- **A worker raising a conflict.** A conflict creates no item. It is a hold on an existing item with a ruling to follow, and [the escalation ladder](./escalation-ladder.md) owns it.

## 3. What retiring the item comments changes for intake

[Section 8.2 of queue mirrors designs](./tillr-queue-mirrors-designs.md#82-what-is-deleted-d-qm22) (`D-QM22`) deletes the comment scanner (`tools/tillr-item/docshare/`), `prose.go` and `prose_rewrite.go`. Three pieces of intake rest on those and are not on its list. That design mentions `triage-no-block` once, as a route its design items are exempt from, and never as something to delete; `rg -n 'validateIdea|validateTriage|doc-triage|document_prerequisite' docs/design/tillr-queue-mirrors-designs.md docs/design/tillr-queue-mirrors-designs-plan.md` returns nothing.

### 3.1. The missing-block triage route goes, with every exemption from it

Today the `git-landing` charter names the prerequisite `design-document` ([`landing.json`](../../tools/tillr/definition/landing.json)). Under it, an open item that no item comment declares is sent to the `design` seat to have one written (`triageDecision` in [`tools/tillr-drain/drain/document_prerequisite.go`](../../tools/tillr-drain/drain/document_prerequisite.go)). Every door that never writes an item comment needed its own exemption, and they sit in five places:

1. `SharingWithFinished` in [`docshare.go`](../../tools/tillr-item/docshare/docshare.go) skips comment items by id (`IsCommentItem`).
2. `readStoredStates` in [`sync.go`](../../tools/tillr-item/item/sync.go) marks an item store-declared when it carries `workflow_args` (document triage) or has a `request_words` row (requests and split parts).
3. `unsharedOpenFromDocs` in the same file also skips rewrite items (`ProseRewriteOriginal`) and design items (`isDesignItemID`).
4. `documentPrerequisites` in `document_prerequisite.go` skips loop-generated items (`IsGeneratedItem`) and the `item-design-` prefix a second time.
5. The same function then skips an item with an `idea_intake` row (`readIdeaIntake`). Sync has no such skip, so by the code an open idea is still counted in the `unshared_open` figure of a store that documents feed. That is a reading of the code and was not measured.

Under **`D-IN4`** (*designer's decision; the owner may overturn this*), all of it is deleted in the same slice that deletes the scanner:

- the route itself (`triageDecision`, `validateTriage`, and the `triage-no-block` hold reason);
- the `awaits-rewrite` and `prose-criteria-count` holds and the `[prose] rewrite` setting;
- the five exemptions above, and the `unshared_open` count they feed.

**What the `design-document` prerequisite means afterwards.** It gates four hold reasons today (`landingHolds` and `generalHolds` in [`tools/tillr-drain/drain/definition.go`](../../tools/tillr-drain/drain/definition.go)): the three above, and `ReasonDesignDocMissing`, which holds an item whose named design document is absent. Only the fourth remains. The prerequisite stays in `landing.json` with that one meaning: an item that names a design document needs that document to exist. It no longer means that every item needs a document.

**Items held on that route on the day it is deleted** are dispatched from their stored record, on the seat and criteria the record names. None is closed or refused after the fact.

**What replaces the route** for an item that arrives with no source is the owner's to rule: [`OQ-IN1`](#OQ-IN1).

### 3.2. What a saved idea's turn produces

Today a saved idea's `design` turn must write item comments. Its second criterion says "Every piece of work the words ask for is an item block that carries from this idea" (`Idea` in [`tools/tillr-item/item/idea.go`](../../tools/tillr-item/item/idea.go)), and the loop refuses a candidate in which "no new item block carries from=" the idea (`validateIdea` in [`tools/tillr-drain/drain/idea.go`](../../tools/tillr-drain/drain/idea.go)). Once the scanner is deleted, nothing reads those comments.

Under **`D-IN5`** (*designer's decision; the owner may overturn this*), the turn's candidate must show at least one of these outcomes, and the loop refuses a candidate that shows none, back to the same turn with that reason:

| Outcome | What the candidate holds | What becomes an item |
| :--- | :--- | :--- |
| **A design** | A design document that a mirrored roadmap routes and that the base did not route | The next sync creates its design item, as for any routed design |
| **Requests** | A statement, in the run's scratch, of each bounded piece of work the words ask for that needs no design: title, words, criteria | The loop files each as a request. A worker never writes the store, so the loop files them, as it files the parts of a split statement today (`tools/tillr-drain/drain/split.go`) |
| **Nothing to queue** | A stated reason in the run's report | Nothing; the idea finishes with the reason recorded |

- **The words file stays.** The first criterion, that the owner's words are in the tree byte for byte, is unchanged.
- **Lineage is recorded in the store.** The link from an idea to what it produced is written on the idea's own row when it finishes, as the pointer the idea pipeline already describes ([item kinds, states and edges](./tillr-idea-pipeline.md#1-item-kinds-states-and-edges)). No `from` attribute in a document carries it.
- **A request in the statement that fails request validation** refuses the whole candidate with that request's rejection, and files none of them. Filing is one transaction.
- **A second turn on the same idea** produces the same request ids for the same words, so a repeated statement files nothing twice ([Section 5.3](#53-duplicates)).
- The exact fields of the statement file follow the request filing's own fields; their spelling is the planner's.

Two owner questions already open elsewhere bound this and are not asked again here: whether the turn queues work before the owner has seen it ([`OQ-PR11`](../tillr/decision-provenance-review.md#OQ-PR11)), and how fast a first provisional design lands ([`OQ-PD1`](./provisional-design-docs.md#OQ-PD1) to [`OQ-PD4`](./provisional-design-docs.md#OQ-PD4)).

### 3.3. Document triage is not affected

A document triage writes one report and "recommends and changes no other file" (its third criterion, in `DocTriage` in [`tools/tillr-item/item/doc_triage.go`](../../tools/tillr-item/item/doc_triage.go)). It writes no item comment and reads none: `rg -n 'tillr: item|item block' tools/tillr-item/item/doc_triage.go config/tillr/workflows/doc-triage@v1.json` returns nothing. It needs no change when the comments retire. Its only tie to the deleted code is exemption 2 above, which goes with the route it guards against.

## 4. One field records the door

After [Section 3.1](#31-the-missing-block-triage-route-goes-with-every-exemption-from-it), the tree still works out which door made an item from the item's id or from a lookup in a side table. Counted on 2026-11-27 with `rg` over the non-test Go files of `tools/tillr`, `tools/tillr-item`, `tools/tillr-drain`, `tools/tillr-view` and `tools/tillr-watch`: `IsCommentItem` appears 27 times in 11 files, `IsGeneratedItem` 13 times in 7, and a literal `item-design-` prefix test 8 times in 6. Each new door adds another.

Under **`D-IN2`** (*designer's decision; the owner may overturn this*), `items` gains one column, `intake_source`, that names the door:

| Value | Written by |
| :--- | :--- |
| `sync-design` | Roadmap sync |
| `sync-plan` | Companion-plan task sync |
| `sync-block` | Item-comment sync, until the comments retire |
| `idea` | `tillr item idea` and the idea box |
| `comment` | The comment intake |
| `request` | `tillr item request`, including a request the `triage` seat or an idea's turn files |
| `doc-triage` | `tillr item doc-triage` |
| `split` | Split filing |
| `generated` | The loop's own repairs |
| `apply` | `tillr item apply` from outside the loop |

- **One writer, once.** The code that creates the row writes the value in the same transaction. No later write changes it: a record that names a different value for an existing item is refused as `intake-source-immutable`, and a record that names none leaves the stored value alone.
- **The command-line `apply` face always writes `apply`** on a row it creates, and refuses a record that claims any other value (`intake-source-reserved`). Otherwise any caller could claim to be the loop. Callers inside tillr's own programs pass their own value.
- **Rows that exist on the day this ships** are given a value by one fixed rule, first match wins: a comment item id is `comment`; an `idea_intake` row is `idea`; a part named in `item_splits` is `split`; a `request_words` row is `request`; the `doc-triage@v1` workflow is `doc-triage`; a design item id is `sync-design`; a loop-generated id is `generated`; an item an item comment declares is `sync-block`; anything else is `apply`. Split comes before request because a split part also has a `request_words` row. Whether a migration writes the column or a reader applies the rule to an empty value is the implementer's choice, as long as one function holds the rule.
- **It is not the item's kind.** The idea pipeline's unbuilt `items.kind` says what an item owns (an idea, a design or a build). `intake_source` says only which door filed it. A request and a companion-plan task are both build work from different doors.
- **Where a person sees it.** `tillr item show` and the observatory's item page name the door in words.
- **Done looks like:** each prefix test and side-table lookup counted above is replaced by a read of the column, and the one backfill function is the only place those id patterns remain.

**Rejected:** rewiring the five exemptions of [Section 3.1](#31-the-missing-block-triage-route-goes-with-every-exemption-from-it) onto the column first. They are deleted outright, so the column is stamped before that deletion and read only by the code that survives it.

## 5. Rules every door shares

### 5.1. No single seat owns intake

The earlier draft asked whether one seat should. The owner's rulings give each door the seat its content needs:

- An idea, an open design and an owner comment need a document written or changed, so they start on `design` ([`R-IN3`](#R-IN3), [`R-IN9`](#R-IN9)).
- An accepted design with no unanswered question needs cutting into tasks, so it starts on `plan` ([`R-IN8`](#R-IN8)).
- A standing alert needs a cheap read-only look and a match against open items, so it starts on `triage` ([`R-IN9`](#R-IN9)).
- A request, a plan task, a split part and a repair already state checkable criteria, so they start on `build`. They reach `design` only when the builder reports an obstacle that needs it ([`R-IN6`](#R-IN6), [`R-IN7`](#R-IN7)).

### 5.2. The words are kept, and nothing rewrites them

Every door a person types into keeps the bytes typed: the idea's words file, `request_words.words`, and the comment batch. A design document and a roadmap are kept by Git. When a request's wording makes the work hard, the `design` seat writes an amendment beside the words and leaves the words alone ([`R-IN7`](#R-IN7); the amendment record is designed in [request amendment](./tillr-request-amendment.md) and not built). No cheap model rewrites prose in a document the owner reads ([`R-IN5`](#R-IN5)), so intake has no rewrite step, and the earlier draft's question of one triage agent or two has no second agent left to count.

### 5.3. Duplicates

Filing is deterministic and calls no model. What each door does with a repeat is built:

| Door | A repeat is | Result |
| :--- | :--- | :--- |
| Roadmap sync | The same design path | The same `item-design-<slug>` row; nothing new |
| Saved idea | The same bytes (`idea_intake.words_hash` is unique; the bytes are not normalized) | The existing item is answered |
| Request | The same bytes and the same choices (`requestInputHash`) | The existing item is answered. The same words with different choices are refused as `request-differs`; a match on a closed item is refused as `request-closed` and names `tillr item reopen` |
| Alert triage | The same `--dedup-key` on an open request | The alert is linked to the existing item |
| Loop defect | The same failing command (`DefectItemID`) | The same item |

Overlap in meaning, where two differently worded asks want the same work, is not a filing concern. The run that notices finishes its item as resolved elsewhere, with a pointer to the item or commit that covers it.

### 5.4. When an intake turn stops

No run spends a retry budget ([`R-IN2`](#R-IN2)). Under **`D-IN6`** (*designer's decision; the owner may overturn this*), that rule covers `design`, `plan` and `triage` turns at a door exactly as it covers build runs:

- An interrupted turn runs again and is not counted.
- A refused candidate returns to the same turn with the refusal's reason.
- An ending identical to the one before it goes to `design` for diagnosis with both runs' records (`repeatedEnding` in [`tools/tillr-drain/drain/runhistory_bridge.go`](../../tools/tillr-drain/drain/runhistory_bridge.go)).
- A choice only the owner can make lands as an open question in a design document, and holds only the work that names it ([`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8)).
- An alert the `triage` seat cannot settle is handed upward ([alert triage, Section 6](./tillr-alert-triage-seat.md#6-what-must-be-handed-upward-to-whom-and-how)).

## 6. How this lands

Each slice lands alone. A slice that touches `tools/tillr*` carries the pack mirror in the same commit.

1. **Slice 1 — stamp the door (`D-IN2`).** Add `items.intake_source`, write it at every creation site, apply the backfill rule to existing rows, and show it in `tillr item show` and on the item page. Waits on nothing.
2. **Slice 2 — retire the route and change the idea turn (`D-IN4`, `D-IN5`).** Lands with or directly after Slice 5 of [queue mirrors designs](./tillr-queue-mirrors-designs.md#11-rollout-sequence-high-level), because the idea turn's old finishing rule stops working the moment the scanner is deleted. Replace the remaining id-prefix and side-table readers with the column. Waits on no question here.
3. **Slice 3 — the charter's answer on bare `apply` (`D-IN3`).** Waits on [`OQ-IN1`](#OQ-IN1), and is empty if the owner picks option B.

## 7. Open questions

Questions that touch one door stay in the document that owns them, so each is asked once: a tracked list for work with no document ([`OQ-PR10`](../tillr/decision-provenance-review.md#OQ-PR10)), an idea's turn queueing work unseen ([`OQ-PR11`](../tillr/decision-provenance-review.md#OQ-PR11)), provisional designs ([`OQ-PD1`](./provisional-design-docs.md#OQ-PD1) to [`OQ-PD4`](./provisional-design-docs.md#OQ-PD4)), and a reader at request filing ([`OQ-RA1`](./tillr-request-amendment.md#OQ-RA1)). One question belongs here.

1. 💬 **OQ-IN1: Once nothing sends an unsourced item to the design seat, should this repository's queue still accept a bare `tillr item apply`?**

   `tillr item apply` writes an item straight from a structured record. It keeps no words, names no design and belongs to no roadmap. Today such an item is caught: the missing-block triage route sends it to the `design` seat before anything is built. That route is deleted with the item comments ([Section 3.1](#31-the-missing-block-triage-route-goes-with-every-exemption-from-it)), so afterwards such an item would be built from its record as filed. A store under another charter, where `apply` is the normal door, is not affected either way.

   - **A — Refuse it here.** The `git-landing` charter lists the doors it accepts and leaves `apply` out. Work reaches this queue only through a door that keeps its source: a roadmap, a plan, an idea, a comment or a request. A script that files through `apply` today must switch to `tillr item request`. The loop's own repairs and split parts are not affected.
   - **B — Keep it open.** Nothing new is built. An item can then be queued and built with no record of what was asked or by whom.

   <!-- vantage: question id=OQ-IN1 leaning="A: in this repository a bare apply only ever worked by being sent to the design seat for a document, and every other door keeps what was asked. Refusing it costs one command change for a script." -->

   _Leaning:_ A: in this repository a bare `apply` only ever worked by being sent to the `design` seat for a document, and every other door keeps what was asked. Refusing it costs one command change for a script.

   **Answer:**

   > _(empty — fill in when decided)_

**How option A is built** (`D-IN3`, *designer's decision; the owner may overturn this*). A charter file gains an optional list, `intake_sources`, of the [Section 4](#4-one-field-records-the-door) values it accepts. A charter without the list accepts every value, so every existing charter file keeps its meaning. With the list, `tillr-item` refuses to create an item whose door is not on it, as `intake-source-not-allowed`, and the refusal names the commands the charter does accept. `landing.json` lists every value except `apply`. Rows already stored are untouched. The rule lives in the charter, not in `tillr-item`'s code, because which doors exist is one use's choice ([`R-IN1`](#R-IN1)).

## 8. Decision ledger

### 8.1. Owner rulings this design rests on

Each row summarizes a ruling recorded in the document it links, where the owner's dated words are. None is first claimed here.

| ID | Owner ruling, and where it is recorded | Date |
| :--- | :--- | :--- |
| <a id="R-IN1"></a>`R-IN1` | An item's lifecycle, prerequisites and retention belong to the charter and not the core, and no item waits without a named next step and an actor ([the core and `git-landing`, Section 1](./tillr-core-and-git-landing.md#1-the-direction-in-the-owners-words)). | 2026-11-16 |
| <a id="R-IN2"></a>`R-IN2` | A worker's ending never spends a retry budget; each ending does the one thing it means, and an identical repeat goes to the `design` seat ([judge and lifecycle, Section 4.4](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)). | 2026-11-17 |
| <a id="R-IN3"></a>`R-IN3` | tillr reads no markup of its own from documents: *"tillr decided on its own markdown tags and is silently loading them everywhere"* was rejected. A roadmap sync creates one item per unfinished design, and work definitions live in the companion plan (`D-QM1`, `D-QM3` and [`OQ-QM1`](./tillr-queue-mirrors-designs.md#OQ-QM1) in [that ledger](./tillr-queue-mirrors-designs.md#decision-ledger)). | 2026-11-18 |
| <a id="R-IN4"></a>`R-IN4` | There is no per-item path fence ([`R-VO2`](./tillr-vocabulary.md#R-VO2)). | 2026-11-19 |
| <a id="R-IN5"></a>`R-IN5` | No cheap model rewrites wording inside the owner's design documents ([`OQ-PR14`](../tillr/decision-provenance-review.md#decision-ledger) in the provenance review's ledger). | 2026-11-20 |
| <a id="R-IN6"></a>`R-IN6` | A request does not choose its workflow; it starts on `build`, or on `design` when its paths lie inside `docs/design/`, and there is no separate upfront plan step ([`OQ-PSS1`](./tillr-plan-step-and-seat-selection.md#OQ-PSS1), [`OQ-PSS2`](./tillr-plan-step-and-seat-selection.md#OQ-PSS2)). | 2026-11-20 |
| <a id="R-IN7"></a>`R-IN7` | How a request is worded should not decide how hard the work is; some part of the design may rewrite a request that is hard to deliver ([request amendment, Section 1](./tillr-request-amendment.md#1-the-ruling)). | 2026-11-21 |
| <a id="R-IN8"></a>`R-IN8` | *"if I don't want to build it, it shoudl be in the icebox, otherwise, if it has no OQs, it's ready to build."* ([`OQ-QM2`](./tillr-queue-mirrors-designs.md#OQ-QM2); see also [`OQ-QM3`](./tillr-queue-mirrors-designs.md#OQ-QM3) and [the 2026-11-22 ruling](./tillr-design-to-planning-transition.md#11-the-2026-11-22-ruling)). An unanswered question holds only the work that depends on it ([`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8)). | 2026-11-22 |
| <a id="R-IN9"></a>`R-IN9` | Comments on a design become work for that document's design agent, and unstarted work on that design waits meanwhile ([`OQ-PR12`](../tillr/decision-provenance-review.md#decision-ledger) in the provenance review's ledger). A standing alert goes first to a dedicated read-only `triage` seat ([`OQ-AT1`](./tillr-alert-triage-seat.md#OQ-AT1)). | 2026-11-20, 2026-11-23 |

### 8.2. Designer's decisions

Every row is the designer's decision, not an owner ruling. The owner may overturn any of them.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-IN1"></a>`D-IN1` | **Designer's decision; the owner may overturn this.** The shape every door ends in is the stored item record, checked at filing by the one function that writes a row. **Rejected:** making doors that have no document write an item comment to reach a shared shape. | 2026-11-27 | [Section 1](#1-the-shape-every-door-ends-in) | Yes, except the `intake_source` part |
| <a id="D-IN2"></a>`D-IN2` | **Designer's decision; the owner may overturn this.** `items.intake_source` names the door, is written once at creation, and replaces working the door out from an item's id or a side table. The command-line `apply` face can write only `apply`. **Rejected:** another id-prefix test for each new door; reusing the idea pipeline's `items.kind`, which answers a different question. | 2026-11-27 | [Section 4](#4-one-field-records-the-door) | No |
| <a id="D-IN3"></a>`D-IN3` | **Designer's decision; the owner may overturn this.** Built only if the owner picks option A of [`OQ-IN1`](#OQ-IN1). A charter may list the doors it accepts; with no list it accepts all. **Rejected:** a `git-landing` refusal written into `tillr-item`'s code. | 2026-11-27 | [Section 7](#7-open-questions) | No |
| <a id="D-IN4"></a>`D-IN4` | **Designer's decision; the owner may overturn this.** The missing-block triage route, the rewrite holds, the `[prose] rewrite` setting and the five exemptions are deleted in the slice that deletes the comment scanner. Items held on the route are dispatched from their record. The `design-document` prerequisite keeps only its missing-document hold. **Rejected:** keeping the route for bare `apply` items, which would keep a `design` turn whose only product is a comment nothing reads. | 2026-11-27 | [Section 3.1](#31-the-missing-block-triage-route-goes-with-every-exemption-from-it) | No |
| <a id="D-IN5"></a>`D-IN5` | **Designer's decision; the owner may overturn this.** A saved idea's turn ends in a routed design, a statement of requests the loop files, or a stated reason that nothing is queued; lineage is recorded in the store. **Rejected:** keeping a `from`-marked comment for ideas alone, which is the markup [`R-IN3`](#R-IN3) rules out; letting the worker file requests itself, since a worker never writes the store. | 2026-11-27 | [Section 3.2](#32-what-a-saved-ideas-turn-produces) | No |
| <a id="D-IN6"></a>`D-IN6` | **Designer's decision; the owner may overturn this.** [`R-IN2`](#R-IN2) covers intake turns as it covers build runs. **Rejected:** a separate attempt cap for intake turns. | 2026-11-27 | [Section 5.4](#54-when-an-intake-turn-stops) | Partly: the repeat rule and alert hand-up are built; holds scoped to one question are designed in [per-step question holds](./tillr-per-step-question-holds.md) |
