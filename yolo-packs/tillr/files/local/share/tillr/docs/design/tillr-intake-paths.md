---
title: "Work reaches tillr through several doors, and every door ends in the same shape"
status: draft
stage: DESIGN
updated: 2026-11-17
tags:
  - tillr
  - design
  - intake
  - triage
  - boundaries
summary: "The owner's idea that each way work enters tillr (an agent files an item, the owner dumps a ramble, a worker raises a conflict) runs through its own small graph of steps that ends in one fixed item shape, so planning, build, gate and landing never learn which door was used. It records what the owner said and when, restates the idea, measures from the code what the planner needs today and what each door has, and lists what is not yet settled."
next: "Talk through the points under Not yet settled; nothing here is built or queued as its own item."
depends-on:
  - tillr-item-graphs.md
  - tillr-idea-pipeline.md
  - tillr-core-and-git-landing.md
---

# Work reaches tillr through several doors, and every door ends in the same shape

> **Delivered copy.** This is `docs/design/tillr-intake-paths.md` from this repository at commit `bdc40d858b` (2026-11-16), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-intake-paths.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** 2026-11-17. A proposal written from the owner's statements; nothing here is built as its own item. Code facts read on this checkout on 2026-11-17 (`tools/tillr-item/item/prose.go`, `sync.go`, `tools/tillr-drain/drain/plan.go`).

> **In short.** Work should enter tillr by whichever door suits who or what is bringing it, each door should run a small graph of steps that makes the request ready, and every graph should end in one fixed item shape, so everything after the doors depends on that shape and never on the door.

**Why it matters.** Today an item that did not come through the one door the loop expects is dispatched anyway or waits on nobody, and the checks that exist on the main door only warn.

**Reads with:** [item graphs](tillr-item-graphs.md) (the graphs the doors are made of), [the idea pipeline](tillr-idea-pipeline.md#2-the-pipeline-and-one-writer-rules) (the earlier flow from an idea to a landed commit), [the observatory's first two actions](observatory-actions.md#5-adding-an-idea) (the owner's idea door in the browser), [the core and the landing definition](tillr-core-and-git-landing.md) (where the use case ends and the core begins).

## Words this document uses

- **Door** *(coined here)*: a way work enters tillr, such as an agent filing an item or the owner dumping an idea.
- **Item shape** *(coined here)*: the fixed form an item has when it leaves its door's steps, described in [section 2](#2-the-shape-every-door-must-end-in).
- **Guide mark**: the attribute `prose="2"` on an item's directive. The only document that tells an author to add it is the writing guide, the `tillr-prose-style` skill, so a block that has it was written by someone who read the guide. The owner called this a shibboleth, a password that proves the writer was told it.
- **Triage**: reading a request nobody has shaped yet and deciding what it needs next. In this document it is one step of a door's graph, not a separate system.

## 1. What the owner said

Times are the owner's local time. The quotes come from design conversations with a coordinating agent; the first four are the owner, transcribed from speech.

**The evening of 2026-11-15, an earlier design conversation about the observatory.** On the writing guide not being applied:

> the skill ... can have some sort of shibboleth ... if the agent doesn't use that in the description, we know that it has not read that guide. And then the first thing that should happen ... after that item hits the queue even is it should get a high priority item to get rewritten correctly.

That became the guide mark, a sync check, and the queued rewrite item described below.

**The same evening, a few minutes later.** On the first step of a ticket:

> I ramble into a ticket. You then create an item. The first thing is a designer because it's like a rambling ticket. And then that designer writes a design doc or maybe that designer decides it needs scouting first. So it replaces itself with four scouts and a designer that joins them.

That became the scouted-design workflow of [item graphs](tillr-item-graphs.md) and the ruling [`OQ-IP1`](tillr-idea-pipeline.md#61-settled-decisions) of the idea pipeline.

**Just after midnight, 2026-11-16.** On a button that saves a ramble:

> I want to be able to hit a button, give some indication of a priority, and then just dump in a whole bunch of rambling thoughts and hit save, and then have that go through that designer loop ... it'll add real title, description, all that stuff for my words, it'll of course save my words, make a design doc if it needs me ... I feel like we can just have an intake path that works real well like that.

That became [the idea save and design turn](observatory-actions.md#5-adding-an-idea).

**2026-11-17.** On an item that arrives with no document block:

> if it ends up like this, it's probably that's some half-baked request for me and it should be able to go to ... the next step, which is a design agent or a triage agent or something like that. Like there should always be a next step that's clear for these items and we should handle them somehow.

**2026-11-17, asking for this dig.** The two statements this document answers:

> we had this shibboleth idea where if an agent files a ticket without having read the skill, it should first go to a rewriting agent to make nice and then there's a similar thing here if I dump in some half-baked idea with ramblings it should go to a rewriting agent which then writes up a real ticket ... and then it should go to an agent that or maybe it's the same agent ... that turns it into some idea that can actually be designed ... and then hands it off to designer scouts whatever is down the line there. different intake paths lead to different shapes of DAGs that process them or subsections of those DAGs.

> You can imagine a triage subgraph which has the same output shape as like a human half-baked triage has the same output shape as like an agent filed item, even though they go through different paths to get there. The downstream stuff doesn't care because it's ultimately aligned in the interface.

What the search did not find: before this document, the word "shibboleth" appeared in no tracked document, only in the owner's message of the first evening and the assistant's reply to it. No earlier conversation or design proposed one agent for both rewriting and shaping; that comes only from the 2026-11-17 statement.

## 2. The shape every door must end in

An item leaves the doors as a document block that a build worker can be sent to. The parts, and who checks each today:

| Part | Checked by sync (before the item is stored) | Checked by the planner (before dispatch) |
| :--- | :--- | :--- |
| An id | Refused when missing or duplicated | Not rechecked |
| A title | Refused when missing; warns when it does not start with an imperative verb from the guide or exceeds 12 words | No |
| A why paragraph | Warns when the section has no prose paragraph before the directive | No |
| A fence of allowed paths | Refused when missing | Holds `no-fence` when the fence admits nothing |
| Dependencies | Refused for an unknown or unavailable blocker | Holds `blocked` until each blocker is done |
| Checkable criteria | Refused when there is none or one is empty; warns above eight | No |
| The guide mark | Warns `prose-unmarked` when absent or not `2` | No |
| No open question on its document | Not read | Holds `question-open` or `questions-unknown` |
| A document block at all | Counted as `unshared_open` and nothing more | No: an item that names no document is not held |

The last column is the finding that matters. **The planner does not read the guide mark, the why paragraph, the criteria, or whether any document declares the item.** An item with no mark is stored and dispatched like any other, and an item applied straight into the store was dispatched to a build worker on 2026-11-16. The mark is a constant in `prose.go`, not a setting, so changing the guide means changing the code, and an item written to an earlier guide shows `prose-unmarked` too.

## 3. The doors: what exists, what is queued, what is missing

| Door | Mechanism today | Queued item that covers it | Gap |
| :--- | :--- | :--- | :--- |
| **An agent files an item without the guide mark** | Sync warns `prose-unmarked` and `prose-shape`; the item is stored and dispatched unchanged | [`item-prose-rewrite-before-build`](../tillr/roadmap.md): a high-priority rewrite item on the cheap seat holds the original until it lands | Not built; its fifth criterion adds a repository setting, `prose.rewrite`, that no code reads yet |
| **The owner dumps a rough idea** | Nothing in the tree; the coordinator re-types it into an item or a document | `item-oa-idea-save` and `item-oa-idea-design-turn` in [the observatory's first actions](observatory-actions.md#5-adding-an-idea): the words are saved verbatim, then one design run writes blocks that carry `from` the idea, or a document of questions | Not built; the run is one designer, and the rewrite and the shaping are not separate steps |
| **An item has no document block** (filed with `apply`) | Dispatched to a build worker that finds nothing to read; `unshared_open` is counted and nothing acts | `item-sync-hold-unshared-items`, being rewritten on 2026-11-17 to send the item to a triage run on the design seat | Not built; the gap is exactly the item's subject |
| **A worker raises a conflict** | Built: a blocked report becomes a conflict, the design seat answers it by amending the block, and the ruling lands through the gate (observed on 2026-11-17) | None needed for the path itself | A conflict about something only the owner can rule goes to the owner by the same route; that route is not drawn as a door anywhere |
| **An item arrives complete with the mark** | Built: sync stores it, the planner dispatches it | None | None; this is the door the shape is modeled on |

## 4. How this sits in the larger direction

[Item graphs](tillr-item-graphs.md) give an item a graph of nodes, each one agent or one command, and say a node may replace itself with several. Each door's steps are exactly that: a rewrite node, a triage node, a designer that fans out to scouts. The item shape of [section 2](#2-the-shape-every-door-must-end-in) is the interface between those intake subgraphs and the build graph: a subgraph with a declared output shape that downstream nodes depend on is the general mechanism, and it belongs to the core. Which doors exist, what shape they end in, and which checks a shape needs belong to the layer that describes a use case, as [the core and the landing definition](tillr-core-and-git-landing.md#1-the-direction-in-the-owners-words) records: the owner put "item prerequisites" there. Landing a change in Git wants documents, blocks and the guide mark. Another use has other doors and another shape; AutoQA's findings pipeline files an item with `apply` into a store of its own, and a bug it confirms is not a rambling request at all.

## Not yet settled

These are for the design conversation, not for an agent to decide. They are not question directives because the documents that hold queued items would be held by them.

- **One triage agent or two.** One that rewrites a badly written item and shapes a half-baked idea, or a rewrite step that only fixes form and keeps meaning, and a shaping step that may change what is asked.
- **Which seat.** The cheap seat for rewrites and the design seat for shaping, as the queued items assume, or one seat for both, or the proposed planning seat of [planning and model tiering](tillr-planning-node-model-tiering.md).
- **What the guide mark means once a rewrite step exists.** Whether the mark routes an item past the rewrite (a marked item skips it, an unmarked one does not), and so whether an agent can pass the check by writing the mark without reading the guide.
- **What the owner types.** A browser button, a command, a document the owner drops somewhere, or chat to the coordinator, and whether the owner's words are kept verbatim beside the shaped item in every case.
- **Whether a deterministic checker enforces the shape.** One program the planner runs on every item, so a door that ends short of the shape holds the item with a named reason, or the checks stay split between sync and the planner as in section 2.
- **Whether the conflict door is drawn as a door**, with its own graph and the same shape, or stays what it is today, a hold with a ruling.
