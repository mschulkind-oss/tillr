---
title: "The queue mirrors the roadmap's outstanding designs: one item for every design that is not finished"
status: in-review
stage: DESIGN
updated: 2026-11-18
tags:
  - tillr
  - queue
  - sync
  - roadmap
  - icebox
  - design
summary: "After a sync, every design document the roadmap routes that is not finished has exactly one item in the queue, so an empty queue means no design is outstanding. Defines outstanding and finished from the stage roles the planning index already reports, the life of that item (held on its questions, read live; parked in the Icebox; planned; waiting on its pieces; stamped built by its own run), a question hold that reaches only what names the question, and what the owner sees. Asks the owner how work written in a document becomes work tillr can start, which reopens the hidden item comment he never ruled on."
next: "The owner rules OQ-QM1. Nothing in this document is built before he has."
---

# The queue mirrors the roadmap's outstanding designs: one item for every design that is not finished

**Status:** 2026-11-18. A proposal; none of it is built, and nothing in it may be built before the owner has answered [OQ-QM1](#OQ-QM1). Written to the owner's three statements of 2026-11-18, quoted in [Section 1](#1-the-direction-and-what-exists-today). Facts about the tree, the store and the planning index were gathered by a scout on 2026-11-18 at `068774045`, and the stages and counts were recounted at `8c6194abf` later that evening, after fifteen designs were restaged and three had questions closed; they are stated with the document, symbol or command they came from; I read no code myself, and what the scout inferred without running it is marked as unverified where it is used.

> **In short.** The queue's unit becomes the design document. Sync gives every unfinished design the roadmap routes one item, the item's state says what the document owes, and nothing about a question is ever copied into the store.

**Why it matters.** Today an item exists only where an agent wrote a hidden comment into a document. Of the 27 unfinished designs the tillr roadmap routes, this one included, 25 have no unfinished item, so the queue can be empty while 25 designs are outstanding. Nine more were fully built and said they were not until a hand sweep on 2026-11-18 (`0cfe5ad1c`), because nothing moves the stage word.

**The shape.** Four parts: a **mirror** rule in sync that creates, finishes and reopens one **design item** per routed design; a pure function that reads the document's stage role, its questions and its place on the roadmap and says what the item owes; two runs the item can start, a design run and a plan run, the second of which stamps the document built; and a question hold that reaches only the items that name the question.

**Cost.** The queue starts work nobody filed: on the first mirror of the tillr roadmap, nine design runs and four plan runs become dispatchable. The Icebox becomes the only way to say "not now". `blocked` regains a meaning [queue persistence](./tillr-queue-persistence.md) took from it. Depending on [OQ-QM1](#OQ-QM1), the hidden item comment and everything that reads it is removed.

**Start at [Section 4](#4-the-life-of-a-design-item)**, the life of the item. [Section 7](#7-how-work-in-a-document-becomes-work-tillr-can-start) is the background to the question.

**Needs your ruling:** [OQ-QM1](#OQ-QM1).

**Reads with:** [queue persistence](./tillr-queue-persistence.md) and [its ruling](./queue-persistence-ruling.md) (what this amends; [Section 11](#11-what-this-supersedes-or-amends) lists the amendments owed), [the graph design](./tillr-graph-is-the-path.md) (every act is a node; a pure function adds the nodes that are needed), [planning and model tiering](./tillr-planning-node-model-tiering.md) (the plan seat this design's plan run uses), [early design landing](./tillr-early-design-landing.md) (how a changed design reaches the trunk ahead of its build), [the item module's guide](../../tools/tillr-item/README.md) and [the drain's guide](../../tools/tillr-drain/README.md) (what is built today).

---

## 1. The direction and what exists today

The owner said three things on 2026-11-18, and they are the brief.

On what the queue holds:

> I don't want tillr to mirror this question like in a permanent store or anything, like open questions. But what I'm expecting is that when we sync a roadmap into tillr, we create an item for every design doc that is still out for any reason. And then we expect some of those to complete and some of them have open questions and that should be fully tracked. So if the queue is empty, that means there are no design docs [outstanding], regardless of open questions.

On the Icebox:

> Icebox items should be loaded too, marked blocked for icebox.

On learning that tillr reads a hidden Markdown comment of its own from his documents:

> tillr decided on its own markdown tags and is silently loading them everywhere? I don't want that.

That comment was settled as [`OQ-QP2`](./tillr-queue-persistence.md#91-decision-ledger) by a design agent "on recorded principle" on 2026-11-15 and built the same day. The owner did not rule on it. This design treats it as not accepted and asks him ([OQ-QM1](#OQ-QM1)).

Earlier rulings of his bind this design throughout: nothing re-runs on a guess about what a change means; no attempt counts and no clocks that fail work; a held state names who moves it; the owner is reached only by a question written in a document; he sees "draining" or "needs you"; an item's fate and engineering detail are the agents' to decide.

**What sync does today**, read 2026-11-18:

| Fact | Where it comes from |
| :--- | :--- |
| Only a hidden comment creates an item. Sync scans tracked Markdown under `docs/` for a comment that opens `<!-- tillr: item`, and a document without one yields nothing | `parseItemBlocks` in the item module's sync |
| A design the roadmap links that holds no such comment gets a warning row, not an item. The row holds a document path and the roadmap line that links it | The `design_without_item` table; shown by `tillr view waiting` |
| A design counts as covered when it holds any comment at all, landed or not, so one landed step hides a design whose other work was never filed | `designWithoutItems`, same module |
| An open question holds every unfinished item whose comment sits in the same file, related or not, and no item in any other file | Sync sets an item's design document to the file holding its comment; the drain's planner holds on that document's first unanswered question |
| A design linked under a roadmap's Icebox heading and holding no comment creates nothing in the store | The parking rule; `D-QP5` in [queue persistence](./tillr-queue-persistence.md#5-the-icebox-and-the-narrowed-meaning-of-blocked-decision-5) |
| Nobody moves a stage word. The loop reads a stage for one purpose, to retire a designer's session when its document is done | The shared Vantage reader and its one caller |
| A report-only command, `tillr item reconcile`, already classifies every indexed document, and it does the opposite of the owner's words: a document with an open question is never an item. The loop never calls it | The item module's guide: "a read-only report, not intake" |

**The size of it**, for the tillr roadmap alone, from the planning index joined to the store's 280 items:

| What | Count |
| :--- | ---: |
| Distinct documents the roadmap links | 51 |
| Of those, under `docs/design/` | 41 |
| Unfinished: stage `SKETCH` or `DESIGN` | 23 |
| Unfinished: stage `DECIDED` | 4 |
| Finished: `BUILT` | 9 |
| Finished: `SUPERSEDED` or `CURRENT` | 4 |
| No stage at all ([the momentum principle](./momentum-principle.md)) | 1 |
| Unfinished designs with an unfinished item today | 2 |
| Unfinished designs with none | 25 |
| Unfinished designs with at least one unanswered question | 14 |
| `DECIDED` designs with unbuilt work and no item for it | 4 |
| Unfinished designs linked from the Icebox | 3 |

The four decided designs with unbuilt work are gate rework, convergence, the definitions plan and the idea pipeline. The nine built ones were stamped by hand on 2026-11-18 (`0cfe5ad1c`) after an audit found them built and still saying `DECIDED`: the observatory's actions, design and redesign, designer sessions, the drain's successor, item graphs, the Muse backend, queue persistence and restart. Queue persistence is among them, and this design reopens one of its questions; [Section 9](#9-edge-and-failure-behavior) says what that does.

## 2. The words

| Word | Meaning | What it is not |
| :--- | :--- | :--- |
| **Sync** | `tillr item sync`: the one command that reads tracked documents and brings the store's items into line with them. From [queue persistence](./tillr-queue-persistence.md). | Not the loop's planning pass, which reads the store and decides what to start. |
| **Routes** | Vantage's word: a roadmap routes a document when it links it. | Not "mentions": a document named in prose with no link is not routed. |
| **Stage role** | One of `open`, `ready`, `built`, `done`: what the repository's [Vantage configuration](../../.vantage.toml) says a document's `stage` word means. `SKETCH` and `DESIGN` are open, `DECIDED` is ready, `BUILT` is built, the rest are done. From Vantage. | Not the stage word itself. tillr reads roles, so a repository may rename its words. |
| **Mirrored roadmap** *(coined here)* | A roadmap the repository's tillr settings name for this rule. | Not every roadmap sync reads. |
| **Design** | A document a mirrored roadmap routes that either carries a stage or lives under `docs/design/`. | Not a module guide, a research note or another roadmap, which a roadmap also links. |
| **Outstanding** *(coined here, from the owner's "still out")* | A design whose stage role is open or ready, or that has no stage. | Not "has open questions": a decided, unbuilt design is outstanding too. |
| **Finished** *(coined here)* | A design whose stage role is built or done, or that no mirrored roadmap routes any longer. | Not "all its items landed", which is an inference from the store and is never used. |
| **Design item** *(coined here)* | The one queue item that stands for an outstanding design. | Not a step of the design's build; that is a piece. |
| **Piece** | One buildable step of a design: an ordinary item with a fence and criteria, landing one commit. The word is the `build-pieces` workflow's. | Not a design item: it stands for work, not for a document. |
| **Fence**, **criteria** | The paths an item's run may change, and the testable conditions its verdict is judged against. From [the status index](../tillr/design-status.md#read-this-first). | |
| **Hold**, **mover** | A hold is a named reason an unfinished item cannot start. Its mover is who ends it and by what act. From the drain's hold rules. | A hold is not a failure and is never counted. |
| **Parked** | Linked under a roadmap heading that starts with `Icebox`. The rule is `D-QP5`'s and is unchanged. | Not blocked by other work. |

## 3. The invariant

After every whole sync that succeeds, for every mirrored roadmap:

1. **Every outstanding design has exactly one unfinished design item**, and that item names the design as its document.
2. **Every unfinished design item has an outstanding design.** A design that became finished has no unfinished item.
3. **So a queue with no unfinished item means no design is outstanding**, whatever questions exist anywhere.

The three are testable from two lists: the designs a roadmap routes with their roles, and the store's unfinished items. A test builds a roadmap with one design in each role, one unstaged, one iceboxed and one with questions, syncs, and compares.

**What makes it hold:**

- **The item's id is derived from the document's path**, by the derivation `tillr item reconcile` already uses: drop `docs/` and `.md`, lowercase, turn every other run of characters into a hyphen, and prefix `item-design-`. Two documents cannot share an item and one document cannot have two. Two paths that derive the same id are refused: sync writes nothing and names both.
- **Sync is the only writer of a design item's existence.** It creates one when a design is outstanding and has none, finishes one when its design is finished, and reopens one when a finished design is outstanding again ([Section 4.4](#44-finishing-and-reopening)).
- **A document with no stage is outstanding, not skipped.** Unknown is never read as finished. Its item is dispatchable, and its first run's job is to give the document a stage.
- **An unreadable planning index is not an empty roadmap.** Sync then creates nothing and finishes nothing, says so, and the faces report the queue's state as unknown ([Section 9](#9-edge-and-failure-behavior)).

**Which roadmaps mirror.** A setting names them; the default is none, so another repository that runs tillr sees no change until it opts in. This repository names the tillr roadmap only, because the owner has said that is the one he cares about for now. A roadmap sync reads but does not mirror keeps today's warning rows.

## 4. The life of a design item

### 4.1. What the item is, at each point

One pure function decides. It reads four recorded facts and nothing else: the document's stage role, the document's unanswered questions, whether the roadmap parks the document, and the item's own pieces in the store. The first row that matches wins.

| # | Recorded facts | The item | Shown as | Who moves it |
| :--- | :--- | :--- | :--- | :--- |
| 1 | Role built or done, or no mirrored roadmap routes it | Finished by sync | Not in the queue | Nobody; nothing is owed |
| 2 | The roadmap parks it | `blocked`, reason `icebox` | Parked | The owner, by moving its entry out of the Icebox |
| 3 | The planning index cannot be read | Held, reason `questions-unknown`, as today | Needs a cause | Whoever repairs the index read |
| 4 | At least one unanswered question | Held, reason `question-open`, naming every one | Needs you | The owner, by answering in the document |
| 5 | Role open or no stage, no unanswered question | Dispatchable: a design run | Draining | The drain |
| 6 | Role ready, and it has unfinished pieces | Held, waiting on those pieces | Draining (waits on work) | The drain, as the pieces land |
| 7 | Role ready, no unfinished piece | Dispatchable: a plan run | Draining | The drain |

Three properties follow.

- **Nothing about a question is stored.** Row 4 is computed on every planning pass from the planning index, through the one question reader the planner already uses. The store holds no question id, text or state for it. Answer the question and land the answer, and the next pass sees row 5 or 7.
- **The Icebox outranks questions.** An iceboxed design with open questions is parked, not "needs you". The owner parked it so that it would stop asking.
- **A design outside the Icebox is worked without being asked.** The roadmap's own definition of the Icebox is "wanted, not now", so everything else on it is wanted now. Rows 5 and 7 start runs nobody filed.

### 4.2. The two runs

**The design run** (row 5) is a design-seat run whose fence is the document alone. It ends in exactly one of three ways:

- the document's stage moves to the ready role, with its header verified against its own questions;
- a question for the owner is written into the document's Open Questions, which is row 4 on the next pass;
- an objection that names who acts next, by the route a design run's objection takes today.

A run that changes nothing and names nobody is not an ending; the existing rule that "committed nothing" is an objection with a named next actor applies.

**The plan run** (row 7) compares the design with the tree and does one of two things:

- **Something is unbuilt:** it cuts the unbuilt remainder into pieces, each with a fence inside the repository, criteria, and the questions it waits on if any. The item then waits on them (row 6). How the pieces are recorded is [OQ-QM1](#OQ-QM1).
- **Nothing is unbuilt:** it fills the design's ledger from the tree, moves the stage to the built role with the date, the commit and a `MEASURED:` or `UNMEASURED:` clause, and lands that. Sync then sees row 1.

The plan run fires again when the last piece finishes, because that event means the remainder may now be empty. It never files again a piece that was closed without being built; if closed work is all that remains, it ends in an objection naming the design seat, because the design now describes something nobody will build.

This is what moves the stage word. The nine designs that were built and said `DECIDED` until the hand sweep of 2026-11-18 would each have taken one plan run to say so.

**Fence and criteria are fixed per run kind**, not written per document: the fence is the document, and the criteria are the endings above. They live in tillr's configuration beside the workflows, where the owner can edit them.

### 4.3. Pieces, and the items that exist today

A piece is an ordinary item: one fence, its criteria, one landed commit. It belongs to a design item when its design document is that design.

- **The ten unfinished items filed by comment today** keep their ids, fences and criteria. The four whose document is a routed design (three in the judge design, one in AutoQA) become pieces of that design's item. The other six, filed in the tillr roadmap, the cost-levers notes and the status-bar plan, belong to no design and stay standalone items.
- **`item-design-autoqa-slices`** asks for exactly what a plan run does. It is closed when the AutoQA design gets its design item.
- **Work with no design** still has a queue item of its own. The invariant says an empty queue means no outstanding design; it does not say every item is a design.
- **A design item may land more than once**: a design run's answer, then a plan run's stamp. Each landing finishes the item as any landing does, and the next sync reopens it because its document is still outstanding. Pieces land once each.

### 4.4. Finishing and reopening

| What sync observes | What it does to the design item |
| :--- | :--- |
| The document's role is built or done, and the item is unfinished | Finishes it as resolved elsewhere: someone moved the stage outside the item's own run |
| The item's own run landed the stage change | Nothing; the landing already finished it |
| No mirrored roadmap routes the document any longer, or the document is gone | Closes it. Removing the link is the disposition, as deleting a comment is today |
| The document is outstanding and its item is finished | Reopens it under the same id, after every item the store already ranks. Its earlier runs stay as its record |
| The document was renamed | Closes the old item and creates a new one; the id follows the path |

Each trigger is a recorded fact with one meaning: a role changed, a link was removed, a link was added. None is "the document changed".

## 5. A question holds only what names it

Today's rule is "an item is held by any unanswered question in the file its comment sits in". It holds too much (two unrelated steps in the judge design wait on a question about a third) and too little (a step filed in the roadmap is held by nothing, which is why designs file steps there).

The rule becomes:

- **A design item is held by every unanswered question in its own document**, and by every question its document's `depends-on` header names with a question fragment. That header is Vantage's, is visible in the document's card, and is already in the planning index. This is the hold that reaches across files.
- **A piece is held only by the questions it names**, as pairs of a document and a question id, in any document. A piece that names none is held by none.
- **A plan run waits for every question.** A design with a question open stays in the open role, so row 7 cannot match. Work the owner wants ahead of a ruling is filed as a piece by whoever he tells, naming the questions it truly waits on.

Naming a question is a reference, not a copy: the pair is stored, and the question's state is read live each pass. A named question that has been answered and compacted is found by its id in the document's ledger and holds nothing. A named question that is in neither place holds the piece with today's `question-removed` reason.

Where a piece writes that reference depends on [OQ-QM1](#OQ-QM1). Whatever the answer, the four held pieces that exist today each state their questions, or none, in the change that builds this rule.

A question marked as blocked on something else holds like an open one. The item is "needs you" only when at least one of its unanswered questions is open for the owner; when all of them wait on something else, the row says what each waits on.

## 6. The Icebox

**What changes.** A design linked from a mirrored roadmap's Icebox gets its design item like any other, and the item sits in the queue in state `blocked` with the reason `icebox`. That is the owner's wording, and it reverses two points of `D-QP5`: that parking a design with no item creates nothing, and that parked work leaves the `blocked` state.

- **One representation for everything parked.** An existing item whose document is parked moves to the same state and reason, so the two parked items that sit in `not-started` today move with it.
- **The reason is a new hold reason** with its own row in the drain's hold rules: where `queue`, mover the owner. Its class is **parked** *(coined here)*: wanted, not now; counted neither as draining nor as needing the owner.
- **The mover is the owner and the act is a roadmap edit.** He moves the entry out of the Icebox section; the next whole sync sees the document unparked and the item takes a place after every item the store already ranks. No agent may icebox or un-icebox anything; that part of `D-QP5` stands.
- **A finished design is finished even in the Icebox.** Row 1 comes first. The Muse backend is the case today: built, dormant, linked from the Icebox, and so not in the queue.
- **An Icebox entry that links no document gets no item.** The roadmap's entry for prices on the observatory is one. Sync names such entries in its report line, so they are visible as not loaded. The invariant is about designs, and an entry with no document is not one yet.

`Exploration` sections park as they do today and are not touched; the tillr roadmap has none.

## 7. How work in a document becomes work tillr can start

This is the background to [OQ-QM1](#OQ-QM1).

**What exists.** The hidden comment carries nine things no other part of a document carries. Where each could live instead:

| What the comment carries | Another home |
| :--- | :--- |
| The item's id | For a design item, the document's path. For a piece, an id tillr assigns when the piece is recorded |
| The fence | For a design item, the document. For a piece, chosen by the plan run and checked to lie inside the repository |
| What it waits on | Between designs, the `depends-on` header. Between pieces, recorded with the piece |
| The seat | A default per kind: design for a design item, chosen by the plan run for a piece |
| Whether it was written to the prose guide | The store, with the rewrite node |
| Workflow, judge and express overrides | Defaults per kind in tillr's configuration |
| Criteria | Written by the plan run |
| Title | The document's title, or the piece's |
| Where it came from | The store's record of which run made it |

On 2026-11-18 the unbuilt work of gate rework, convergence and the idea pipeline was written into those designs as prose units with no comment (`5aadd1004`), so that it can become items whichever way this is answered.

There is a second hidden comment of tillr's, written beside a question that a design run raises for the owner, which ties the question to the item it holds. A search of `docs/` on 2026-11-18 (`rg '<!-- tillr-owner-question' docs`) finds none live. It follows whatever is ruled for the first.

**What Vantage offers.** Its own comments are a closed set of four names (a section, a block, a question, a fallback) that decorate and never carry meaning; anything else is ignored without a word. It has headers for a document's state and what it depends on, and plain task lists. It has nothing that means a task or a work item. Measured 2026-11-18 on `vantage-check` 0.12.0 with no configuration: a document with unknown top-level header keys passes with no finding, so a header list would be tolerated and never understood.

**The alternatives.** A search on 2026-11-18 at `8c6194abf` finds 17 live comments in 8 files: ten belong to unfinished items and seven are left behind by items that landed.

| | What the owner sees in a document | Built | Removed | The 17 live comments |
| :--- | :--- | :--- | :--- | :--- |
| **A. Nothing in the document** | No tillr markup anywhere | The mirror; the plan run recording pieces in the store; a page that lists a design's pieces | The comment's scanner, its retirement commit after each landing, the block the reconcile report prints, the copy step that neutralizes comments in the pack's documents, the gate's allowance for them, and about forty test files that use the literal | All 17 deleted in one change. The ten unfinished items carry on from the store, which already holds their definitions |
| **B. A list in the document's header** | A visible list of steps in the YAML at the top of each document | The mirror; a header reader and writer; the same retirement, now editing the header | The comment's scanner and what depends on its shape | Ten rewritten as header entries, seven deleted |
| **C. A Vantage construct** | Whatever Vantage would define | Nothing here until Vantage ships one | Nothing until then | Stay as they are meanwhile |
| **D. Keep the comment** | Nothing in a rendered page; the comment in raw Markdown | The mirror; a line from sync naming each document it read items from | Nothing | Seven left by landed work deleted; ten stay |

**What each costs beyond the build.**

- **A** gives up reading and editing a step in git before it runs. Steps exist on the machine that planned them; a second machine plans its own from the same design and tree. The plan run chooses each fence, so the fence is only as careful as that run. Work with no design enters through tillr's command for filing an item, not a document.
- **B** puts a step's paths and conditions far from the prose that explains it, and makes long headers: the judge design was cut into thirteen steps.
- **C** is a wait of unknown length on another tool.
- **D** costs nothing to build and keeps the thing objected to. The seven stale comments are what a second home for one fact costs when its cleanup is missed.

Under every alternative, sync stops being silent: its report line names the documents it created or changed items from.

**My read.** A. The owner's rulings on the item graph already say that a planning stage adds the implementation as nodes at run time and that anything not started may be reshaped freely. A step marked in a document is a second home for that fact, and a step that can be reshaped until it starts is a poor fit for text that must be edited and landed to change. The weakness is real: `D-RS1` chose documents so that work is shared and reviewable in git, and A keeps that for the design and gives it up for the steps.

## 8. What the owner sees

**`tillr view waiting`.** One row per design item; pieces are rows of their own as today. The warning rows with an empty item id go away for a mirrored roadmap, because each is now an item. The summary line gains the parked count:

```text
summary: owner-waiting 12; parked 3; next [drain=13]

 #  item_id                                 class          where  why
 1  item-design-design-tillr-storage        needs you      queue  question-open: 3 unanswered in docs/design/tillr-storage.md: OQ-TS1, OQ-TS2, OQ-TS3
 2  item-design-design-tillr-convergence    ready          queue  plan run: decided, nothing planned yet
 3  item-design-design-tillr-idea-pipeline  waits on work  queue  2 of 2 pieces unfinished: item-ip-a, item-ip-b
 4  item-design-design-tillr-autoqa         parked         queue  icebox: "AutoQA" in docs/tillr/roadmap.md
```

**`tillr drain why <item>`.** For a design item the document's own facts come first: icebox, then questions, then anything loop-wide such as a red trunk. A design that waits on the owner still waits on him while the trunk is red, and today the red trunk is printed in its place.

**Movers.** Every mover reads `who; act`, with `who` one of the actor names the summary line already uses, and the owner always spelled `owner`. The tree and the installed build disagree today on one row ("owner: file the document's item block" against "operator; file the document's item block"); that row is the warning this design retires.

```text
question-open   owner; answer OQ-TS1, OQ-TS2, OQ-TS3 in docs/design/tillr-storage.md
icebox          owner; move "AutoQA" out of the Icebox in docs/tillr/roadmap.md
waits on work   drain; lands item-ip-a, item-ip-b
```

**Status.** The JSON status gains a count of outstanding designs, of those held on a question (the `question_holds` field exists and is null today), and of those parked.

**The three quiet states**, each with its next step:

```text
queue empty: every design docs/tillr/roadmap.md routes is finished (synced 5:54 PM ET). Nothing needs you.
next: link a new design from the roadmap.

nothing draining, nothing needs you; 3 designs parked in the Icebox of docs/tillr/roadmap.md.
next: move an entry out of the Icebox to start it.

queue state unknown: the last sync could not read the planning index (<the reader's error>).
next: tillr item sync
```

The first is printed only after a whole sync that succeeded; a sync that failed or never ran prints the third. "Empty" over a broken sync is the one false statement this design must never make.

## 9. Edge and failure behavior

| Case | What happens |
| :--- | :--- |
| A mirrored roadmap routes no design | No design items; the empty statement names the roadmap |
| A design is linked several times | One item; its place is its first link outside a parked section |
| A design is linked from two mirrored roadmaps | One item, belonging to the first roadmap that reaches it, as blocks do today |
| A design is linked both inside and outside the Icebox | Today's parking rule decides, unchanged |
| The planning index cannot be read at sync | Sync creates, finishes and reopens nothing, reports the reader's error, and existing items are untouched |
| Two syncs at once | Each writes in one transaction and the ids are derived, so the second finds nothing to do |
| A sync limited to named roadmaps | Creates and parks; only a whole sync finishes, reopens or unparks, as only a whole sync unparks today |
| A person edits a stage by hand | Honored at the next sync, in either direction |
| A built design gains an open question, as queue persistence does through this document | Nothing by itself: the question lives in this document and holds this document's item. The built design's item reopens only when someone moves its stage back to an open or ready role, which the answer to the question may require |
| A plan run and a hand edit both stamp the stage | The second is an empty change; nothing lands twice |
| The store refuses to reopen a finished item | Not known whether it can; see [Section 12](#12-risks) |

**Never:** tillr the program does not edit a document outside a run's own commit; sync does not write to any document; nothing is retried on a count or failed on a clock; a design item is never created from a guess that a design "looks built".

## 10. Migration and build order

Nothing below starts before [OQ-QM1](#OQ-QM1) is answered. Steps 1 to 5 are the same under every answer; step 6 depends on it. The stage words themselves need no migration: the hand sweep of 2026-11-18 made them true. Each step changes the module, its tests, its guide and the pack's mirror of the module together; the mirror is the copy under [the tillr pack](../../yolo-packs/tillr/), refreshed by `just pack-sync` and held to its source by [the pack sync suite](../../tests/pack-sync.test.py).

**Step 1. One reader for a document's stage role.**
- *What changes:* the shared Vantage reader keeps each document's role, not only whether it is done. The second stage reader in the warning code and the literal stage words in the reconcile report are replaced by it.
- *Files:* [the shared reader](../../tools/tillr/vantage/vantage.go), [the item module's question reader](../../tools/tillr-item/item/questions.go), [the warning code](../../tools/tillr-item/item/design_without_item.go), [the reconcile report](../../tools/tillr-item/item/reconcile.go), and their tests.
- *Waits on:* the answer.
- *Done when:* a test repository that renames `DECIDED` in its Vantage configuration gets the same roles; no tillr source compares a stage word.

**Step 2. Sync mirrors the named roadmaps.**
- *What changes:* the mirror setting; creation, finishing and reopening of design items; the id derivation and its collision refusal; the sync report line, which names what it loaded and any Icebox entry with no document.
- *Files:* [sync](../../tools/tillr-item/item/sync.go), [its roadmap reading](../../tools/tillr-item/item/sync_roadmap.go), [the store schema](../../tools/tillr-item/item/schema.go), [the loop's sync line](../../tools/tillr-drain/drain/sync.go), [the repository's tillr settings](../../.tillr.toml), and the sync tests.
- *Waits on:* step 1.
- *Done when:* the test of [Section 3](#3-the-invariant) passes; a second sync changes nothing; an unreadable index creates and finishes nothing.

**Step 3. The Icebox is `blocked`, reason `icebox`.**
- *What changes:* the new hold reason and its rule row; the parked class; parking and unparking move items between `in-queue` and `blocked`; the stored parked items move.
- *Files:* [sync](../../tools/tillr-item/item/sync.go), [the parking tests](../../tools/tillr-item/item/sync_parking_test.go), [the hold reasons](../../tools/tillr-drain/drain/schema.go), [the hold rules](../../tools/tillr-drain/holdrules/holdrules.go), [the view's ordering](../../tools/tillr-view/order.go).
- *Waits on:* step 2.
- *Done when:* the three unfinished Icebox designs of the tillr roadmap each show one parked row and the built one shows none; moving an entry out and syncing queues its item; the rule that every reason has a row still passes.

**Step 4. A question holds only what names it.**
- *What changes:* the planner's question hold as [Section 5](#5-a-question-holds-only-what-names-it) states it; the four existing held pieces state their questions.
- *Files:* [the planner](../../tools/tillr-drain/drain/plan.go), [its blocked handling](../../tools/tillr-drain/drain/blocked.go), the drain's guide, and their tests.
- *Waits on:* step 2, and the answer for where a piece names a question.
- *Done when:* in the judge design, only the pieces that name its open question are held by it; a piece in one file is held by a question it names in another.

**Step 5. The faces.**
- *What changes:* everything in [Section 8](#8-what-the-owner-sees); the warning rows and their table stop being written for a mirrored roadmap.
- *Files:* [the waiting view's design rows](../../tools/tillr-view/waiting_design.go), [the why view](../../tools/tillr-view/why.go), the status view, and their tests.
- *Waits on:* steps 2 to 4.
- *Done when:* each specimen in [Section 8](#8-what-the-owner-sees) is asserted by a test, including that "queue empty" is never printed over a failed sync, and that every command a next step names exists.

**Step 6. The two runs, and the answer's own work.**
- *What changes:* the design run and the plan run with their fixed fences and criteria; then, by the answer: recording pieces in the store and removing the comment and its readers (A), a header reader and writer (B), or deleting the seven stale comments (D). `item-design-autoqa-slices` is closed.
- *Files:* [the workflows](../../config/tillr/workflows/); under A also [the comment grammar](../../tools/tillr-item/docshare/docshare.go), [block retirement](../../tools/tillr-item/item/retire.go), [the owner-question marker](../../tools/tillr-item/item/owner_question.go), [the designer's answer placement](../../tools/tillr-drain/drain/design_answer.go), [the pack's document copier](../../tools/pack-docs.py) and [the gate's roadmap section check](../../tools/gate/checks/roadmap_sections.py).
- *Waits on:* steps 1 to 5.
- *Done when:* a decided test design with one unbuilt step ends with that step landed and the document stamped built by its own item; under A, `rg '<!-- tillr: item' docs` finds only fenced specimens.

## 11. What this supersedes or amends

These are owed once the owner has ruled; none is made now, except one sentence in each of the two queue persistence documents saying that the comment is reopened here.

- **Queue persistence, the [`OQ-QP2`](./tillr-queue-persistence.md#91-decision-ledger) row.** Reopened as [OQ-QM1](#OQ-QM1). The row and the text it settled are rewritten to the answer.
- **[Queue persistence](./tillr-queue-persistence.md#5-the-icebox-and-the-narrowed-meaning-of-blocked-decision-5), `D-QP5`.** Point 3's "never-materialized items create zero rows" and its move to `not-started`, and point 4's "what `blocked` means once parked work leaves it", are replaced by [Section 6](#6-the-icebox). The rule that only the owner iceboxes stands.
- **[Queue persistence](./tillr-queue-persistence.md#102-the-function-total-partition-three-outcomes-and-the-fourth-case), the reconcile function.** Its case that a document with an open question is never an item contradicts the owner's words, and its "inferred from landed items" case is a guess. The mirror replaces the report, and the command is removed with step 2.
- **[The ruling](./queue-persistence-ruling.md#1-shared-item-block-shape), `D-RS1`.** The comment's grammar stands as built until [OQ-QM1](#OQ-QM1) is answered; under A it is retired with the comment.
- **The warning that a routed design has no item**, built from [this roadmap entry](../tillr/roadmap.md#filed-2026-11-15-a-design-on-the-roadmap-always-has-an-item-in-the-queue). Replaced by real items for a mirrored roadmap.
- **The count of unfinished items that no document declares.** Under A every piece is one, so the count loses its meaning and is removed.
- **The tillr roadmap's Icebox entry for AutoQA**, which says an open question is what keeps its item from running. The Icebox does that now.

## 12. Risks

| Risk | What limits it |
| :--- | :--- |
| The first mirror makes thirteen design and plan runs dispatchable at once, on the most expensive seats | They take their places in roadmap order, and the Icebox is the owner's lever |
| A plan run stamps a design built that is not | The stamp is a landed commit judged against the plan run's criteria, and it must carry a measurement clause. A wrong stamp is reversed by a stage edit, which reopens the item |
| Whether the store can move a finished item back to the queue was not checked | The plan confirms it before step 2. If it cannot, a reopened design takes a new id with a version suffix, which the existing clone rule allows once the original is finished |
| Under A, a fence chosen by a run is wider than a person would have written | The fence is recorded and shown on the item's page before the piece starts, and a piece not yet started may be reshaped |
| A design that depends on another by its header waits forever on a question nobody will answer | The row names the question and its document, so the wait is visible and has a mover |
| The scope of today's sync is inferred, not measured: the settings name no roadmaps, so all three are presumably read | Step 2's first test states the scope it found |

## 13. What this does not cover

- **Turning a built design into a reference page.** A built design owes nothing by the design standard, so it is finished for the queue; Vantage lists it as ready to graduate.
- **Ordering between designs beyond the roadmap's order.** A `depends-on` entry with no question fragment holds nothing here.
- **The other two roadmaps.** They mirror when the setting names them.
- **How a changed design reaches the trunk ahead of its build**, which is [early design landing](./tillr-early-design-landing.md)'s.
- **The doors by which work with no design enters**, which is [the intake design](./tillr-intake-paths.md)'s.
- **A kind column on items.** The idea pipeline designs one; a design item is recognized here by its derived id.

## Decision Ledger

Rows marked as the owner's quote his words. Every other row is a designer's decision, made from the evidence above, and the owner may overturn it.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D-QM1 | **The owner's ruling:** "when we sync a roadmap into tillr, we create an item for every design doc that is still out for any reason … if the queue is empty, that means there are no design docs, regardless of open questions." Questions are not mirrored into a store. | 2026-11-18 | [Section 3](#3-the-invariant) | — |
| D-QM2 | **The owner's ruling:** "Icebox items should be loaded too, marked blocked for icebox." | 2026-11-18 | [Section 6](#6-the-icebox) | — |
| D-QM3 | **The owner's objection:** "tillr decided on its own markdown tags and is silently loading them everywhere? I don't want that." The hidden item comment is treated as not accepted and is asked again. | 2026-11-18 | [Section 7](#7-how-work-in-a-document-becomes-work-tillr-can-start) | — |
| D-QM4 | **Designer's decision; the owner may overturn this.** Outstanding is stage role open or ready, or no stage; finished is role built or done, or no longer routed. A built design is finished without being graduated. | 2026-11-18 | [Section 2](#2-the-words) | — |
| D-QM5 | **Designer's decision; the owner may overturn this.** A setting names the roadmaps that mirror; the default is none; this repository names the tillr roadmap only. | 2026-11-18 | [Section 3](#3-the-invariant) | — |
| D-QM6 | **Designer's decision; the owner may overturn this.** A design item's id is derived from its document's path, and a document keeps one item for life: it is reopened, never cloned. | 2026-11-18 | [Section 4.4](#44-finishing-and-reopening) | — |
| D-QM7 | **Designer's decision; the owner may overturn this.** Sync alone creates, finishes and reopens design items, each on a recorded fact; tillr never edits a document outside a run. | 2026-11-18 | [Section 4.4](#44-finishing-and-reopening) | — |
| D-QM8 | **Designer's decision; the owner may overturn this.** The stage word is moved by the item's own runs: a design run moves it to decided, and a plan run that finds nothing unbuilt stamps it built. | 2026-11-18 | [Section 4.2](#42-the-two-runs) | — |
| D-QM9 | **Designer's decision; the owner may overturn this.** A design outside the Icebox is worked without being asked; the Icebox is the only "not now". | 2026-11-18 | [Section 4.1](#41-what-the-item-is-at-each-point) | — |
| D-QM10 | **Designer's decision; the owner may overturn this.** A question holds its own document's design item and only the pieces that name it; a plan run waits for every question. | 2026-11-18 | [Section 5](#5-a-question-holds-only-what-names-it) | — |
| D-QM11 | **Designer's decision; the owner may overturn this.** Everything parked is state `blocked` with reason `icebox` and class parked, counted neither as draining nor as needing the owner. | 2026-11-18 | [Section 6](#6-the-icebox) | — |
| D-QM12 | **Designer's decision; the owner may overturn this.** An Icebox entry that links no document gets no item and is named in sync's report. | 2026-11-18 | [Section 6](#6-the-icebox) | — |
| D-QM13 | **Designer's decision; the owner may overturn this.** A design item's fence is its document and its criteria are fixed per run kind, kept in tillr's configuration. | 2026-11-18 | [Section 4.2](#42-the-two-runs) | — |
| D-QM14 | **Designer's decision; the owner may overturn this.** On the faces: one row per design, the document's own holds ahead of loop-wide ones, movers as `who; act`, and a sync line that names what it loaded. | 2026-11-18 | [Section 8](#8-what-the-owner-sees) | — |
| D-QM15 | **Designer's decision; the owner may overturn this.** The warning rows and the reconcile report are retired where a roadmap mirrors. | 2026-11-18 | [Section 11](#11-what-this-supersedes-or-amends) | — |

## Open Questions

1. 💬 **OQ-QM1: How should work written in a design document become work tillr can start?**

   You approve a design that lists six build steps. Today an agent then adds a hidden comment under each step's heading, giving the step an id and the folders it may change, with its pass conditions as bullets beneath. `tillr item sync` reads those comments from every document under `docs/` and says nothing about the ones it loads. You never ruled on this; an agent settled it on 2026-11-15. There are 17 live comments in 8 files, 7 of them left behind by work that already landed.

   - **A — Nothing in the document.** The design is the queue item. Once it is decided, a planning run cuts it into steps and records each step's folders and pass conditions in tillr's store; you read them on the item's page. Your documents carry no tillr markup, and all 17 comments are deleted. The largest build. Steps can no longer be read or edited in git before they run.
   - **B — A visible list in the document's header.** Each step is an entry in the block at the top of its document. You see it in the raw file. Ten comments become header entries and seven are deleted. Step details sit far from the prose, and headers grow long.
   - **C — Wait for Vantage to offer a marker for work.** It has one for questions and none for work. Nothing changes until it does, and the comments stay meanwhile.
   - **D — Keep the hidden comment, now with your consent.** Nothing is rebuilt. Sync starts naming every document it loads from, and the seven leftovers are deleted.

   <!-- vantage: question id=OQ-QM1 leaning="A: you have already ruled that a planning stage adds the build steps at run time, so a marker in the document is a second home for the same fact. It gives up reviewing steps in git, which is why I hold it less firmly than its reason suggests." -->

   _Leaning:_ A: you have already ruled that a planning stage adds the build steps at run time, so a marker in the document is a second home for the same fact. It gives up reviewing steps in git, which is why I hold it less firmly than its reason suggests.

   **Answer:**
   > _(empty — fill in when decided)_
