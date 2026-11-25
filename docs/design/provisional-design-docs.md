---
title: "Turn an idea into a provisional design doc in minutes, then work the design later"
status: draft
stage: DESIGN
tags:
  - tillr
  - design
  - intake
  - latency
  - vantage
summary: "Defines the provisional design document: the owner's prompt, bounded project context and initial open questions landed on the trunk within two to three minutes with no upfront research. It separates that capture from the later worked design pass, shows the provisional state in tillr's queue views, and asks the owner who writes provisional documents and how they are marked."
next: "The owner rules on OQ-PD1 through OQ-PD4; the worked design pass and the build wait on those rulings."
depends-on:
  - tillr-idea-pipeline.md
  - tillr-intake-paths.md
  - tillr-early-design-landing.md
  - tillr-designer-scouts.md
vantage:
  status-chip: true
---

# Turn an idea into a provisional design doc in minutes, then work the design later

**Status:** Nothing here is built. `rg -i 'provisional' tools/tillr-item tools/tillr-view tools/tillr-drain/drain/plan.go`
finds no provisional state. This document is itself written in the provisional form it
specifies: its claims about the tree are limited to the files it links, and the worked
completeness pass is still owed.

> **In short.** A design thought reaches the store in seconds, then stays invisible to
> Vantage and to other agents until a design run has researched the tree and written a file.
> Splitting design into **Step 1, provisional capture** (the prompt, bounded context and
> initial `OQ-*` questions, with no new research) and **Step 2, the worked design pass** puts
> a real, marked planning document on the trunk within two to three minutes without
> interrupting other work.

**Why it matters.** Until a file exists in `docs/design/`, the owner cannot read it or answer
its questions in Vantage, and other items cannot link to it.

**The shape.** A capture pass writes the file under a no-research instruction, lands it via
[early design landing](./tillr-early-design-landing.md), attaches it to the queue item as
`design_doc` with a visible `provisional` badge, and leaves the research-and-completeness pass
as separate ordinary-priority work.

**Cost.** A design-bound item lands twice (capture, then worked design), and the `design-doc`
skill's completeness floor needs an explicit waiver while a document is provisional.

**Start at [The provisional state](#the-provisional-state).**

**Needs your ruling:** [OQ-PD1](#OQ-PD1), [OQ-PD2](#OQ-PD2), [OQ-PD3](#OQ-PD3), [OQ-PD4](#OQ-PD4).

**Reads with:** [the idea pipeline](./tillr-idea-pipeline.md) and
[intake paths](./tillr-intake-paths.md) (how ideas and requests enter the store),
[early design landing](./tillr-early-design-landing.md) (landing a `docs/` change ahead of its
item), and [designer scouts](./tillr-designer-scouts.md) (why the two-node design workflow
starts slowly).

---

## Owner prompt

The owner's words, 2026-11-20:

> "we want that gap to be as short as possible, even if we don't want it to interrupt other
> work. So maybe we need the concept of ... a provisional design doc ... it should be like the
> quickest lightweight way of turning an idea into a design doc so that we can capture it in
> the right form without spending time up front. ... you would hand the idea with just a
> little context about the project to an agent and tell them write the design doc right now.
> Don't delay. Don't do more research. We will come back to it and be able to mark that as the
> state."

> "if we know that an item will be attached to a design doc, we need to get it to that state
> as soon as possible, even if we're not ready to do the follow-up work immediately."

---

## The provisional state

A **provisional design document** *(coined here)* is a valid Vantage planning file at its
final `docs/design/<topic>.md` path that captures an idea before any research into the tree or
failure-mode analysis has run. It is not a sketch nobody should act on: it owes a worked
design pass and initial rulings.

| Contract aspect | Rule |
| :--- | :--- |
| **Must contain** | 1. **The owner's prompt:** an opening `## Owner prompt` section quoting the owner's dated words.<br>2. **Bounded context:** two to five links to documents or code already in hand and a short statement of the problem, with no new tree-wide research.<br>3. **Initial open questions:** the choices the prompt raises, written as Vantage question directives with options and a leaning. |
| **Must not claim** | No settled rulings or Decision Ledger rows; no `stage: DECIDED` or `stage: BUILT`; no assertion about code nobody read; no build plan or companion `-plan.md`. The `design-doc` skill's completeness floor is waived until promotion. |
| **How it is marked** | Frontmatter sets `status: draft` and `stage: DESIGN` under the vocabulary in [`.vantage.toml`](../../.vantage.toml), or `stage: PROVISIONAL` if [OQ-PD2](#OQ-PD2) adds that word. The prose status line carries the word `PROVISIONAL`, so a human and `parseFrontmatter` in [`frontmatter.go`](../../tools/tillr-view/frontmatter.go) can both detect it. |
| **Promotion** | The worked design pass edits the same file in place: it researches the tree, walks the completeness list, refines the questions, sets `status: in-review` and removes `PROVISIONAL` from the status line. The `## Owner prompt` section is exempt from compaction until a built design graduates to `docs/reference/`. |

---

## From idea to landed file

### What the gap costs

Writing is not the slow part. Measured on this repository's drain: a single-pass run that
writes a Markdown design change takes 31.6 to 39.7 seconds of generation, while a two-stage
workflow plus the non-document gate checks takes 10 to 28 minutes of wall time. The read-only
`open` stage of [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json)
alone ran 229 and 285 seconds on two items before any writer started. The document checks
([`change_common.go`](../../tools/gate/change_common.go) and `uvx vantage-check`) finish in
seconds.

### The capture instruction and target latency

Every capture run receives one directive beside the owner's words and the context links:

> *Write the provisional design doc at `docs/design/<topic>.md` right now from the owner's
> words and the supplied context. Do not delay. Do not do more research. Mark the status line
> `PROVISIONAL`, state only verified context, and file an open question for every unsettled
> choice.*

**Target latency**, from the owner's message to a landed file on the trunk: two to three
minutes through an express lane (a single-pass write, the document checks, an immediate
landing), or 60 to 90 seconds if written in the foreground ([OQ-PD1](#OQ-PD1)).

### Who writes it

`DesignDocumentRule` in [`stage.go`](../../tools/tillr-drain/drain/stage.go) states the
standing rule: "Only an item's designer changes a design document". Three writer paths trade
latency against that rule ([OQ-PD1](#OQ-PD1)):

| Path | Who writes Step 1 | Effect on the rule | Expected wall time |
| :--- | :--- | :--- | :--- |
| **A: one-run `design` workflow** | One `design`-seat node on an express lane (`[lanes] express` in [`.tillr.toml`](../../.tillr.toml)), research tools off | Unchanged: the `design` seat stays the sole writer of `docs/design/*.md`. | 2 to 3 min; waits only when every express lane is busy |
| **B: a dedicated `provisional` seat** | A fast mid-tier seat that only does Step 1 | A narrow exception: that seat may create a new `PROVISIONAL` file; editing an existing design stays `design`-only. | 1.5 to 2.5 min, without spending `design` quota |
| **C: the foreground agent** | The interactive session's agent writes at once and hands a docs-only change to `tillr-land` | An explicit exception: foreground creation of new `PROVISIONAL` files, serialized through `tillr-land` so the trunk is never written directly. | 60 to 90 s, with no lane wait |

---

## Attaching Step 1 to the item, and showing it

### Step 1 before and separate from Step 2

Intake knows an item needs a design document when `tillr item idea` marks it so
([`serveidea.go`](../../tools/tillr-view/serveidea.go)) or when a request names the `design`
seat (`RequestDefaultWorkflow` in [`request.go`](../../tools/tillr-item/item/request.go) maps
`design` to `scouted-design@v1`). Such an item runs as two steps:

1. **Step 1, provisional capture (express, immediate):** creates `docs/design/<topic>.md`,
   checks it with `uvx vantage-check`, lands it via
   [early design landing](./tillr-early-design-landing.md), and sets the item's `design_doc`.
2. **Step 2, the worked design pass (ordinary priority):** runs later on the `design` seat
   (or after the owner's triage, [OQ-PD3](#OQ-PD3)) to research the tree, complete the gap
   test and promote the document. Plan and build items attached to the document are held
   while it is provisional.

### How the queue shows a provisional document

The planner in [`plan.go`](../../tools/tillr-drain/drain/plan.go) and the views distinguish a
missing document (`ReasonDesignDocMissing`) from a present one with open questions
(`ReasonQuestionOpen`). Reading the `PROVISIONAL` marker adds a third state, shown by
`tillr view status`, `tillr view waiting` and the web queue rows:

- **`design-doc-missing`**: no file on the trunk yet; Step 1 is owed.
- **`provisional`**: the file is on the trunk and readable in Vantage; the worked pass is
  owed, and a new hold reason keeps dependent builds waiting.
- **`question-open`**: the worked pass finished; the owner's rulings are owed.

### What would change in the `design-doc` skill

The `design-doc` skill is not part of this repository. Making provisional capture official
there adds three rules ([OQ-PD4](#OQ-PD4)):

1. **A provisional-capture operation** beside drafting, review iteration, compacting and
   auditing, which waives the completeness floor while `PROVISIONAL` holds and requires it on
   promotion.
2. **The marker convention:** the `PROVISIONAL` status line, and `stage: PROVISIONAL` if
   [OQ-PD2](#OQ-PD2) adds the word.
3. **A compaction exemption for `## Owner prompt`** until the built design graduates.

---

## What this document does not claim

- No inventory of the code and no companion plan; both belong to the worked pass once
  [OQ-PD1](#OQ-PD1) to [OQ-PD4](#OQ-PD4) are ruled.
- No change to requests that need no design document ([intake paths](./tillr-intake-paths.md)).

---

## Open Questions

1. 💬 **OQ-PD1: Who writes Step 1, and does the design-seat writer rule change?**

   Only the drain's `design` seat may change a design document, and a `design` request runs
   the two-node `scouted-design@v1` workflow.

   - **A: a one-run design workflow on the `design` seat.** Keeps the writer rule intact;
     lands in two to three minutes on an express lane with research off.
   - **B: a dedicated mid-tier `provisional` seat.** May create new `PROVISIONAL` files so
     capture never waits on `design` quota; edits to existing designs stay `design`-only.
   - **C: the foreground agent writes at once and hands a docs-only change to `tillr-land`.**
     Fastest (60 to 90 seconds), at the cost of an exception for foreground creation.

   <!-- vantage: question id=OQ-PD1 leaning="A first (or C when dictated live in an interactive session) — a one-run express design workflow hits ~2 minutes with zero writer-rule erosion, and an optional foreground cut helper can later shave the remaining minute." -->

   _Leaning:_ A first (or C when dictated live in an interactive session) — a one-run express design workflow hits ~2 minutes with zero writer-rule erosion, and an optional foreground cut helper can later shave the remaining minute.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-PD2: Is `PROVISIONAL` a status-line marker under `stage: DESIGN`, or a new stage in `.vantage.toml`?**

   [`.vantage.toml`](../../.vantage.toml) declares seven stages. `SKETCH` means nobody should
   act on the document, whereas a provisional document owes a worked pass and rulings.

   - **A: keep `stage: DESIGN` and `status: draft`, and mark the status line.** No change to
     the stage vocabulary; the view reads the marker for the queue badge.
   - **B: add `PROVISIONAL = "open"` to `[planning.stages]`.** The stage becomes first-class
     in Vantage badges and indexes.
   - **C: reuse `stage: SKETCH` with the status-line marker.** An existing word, though it
     signals that nobody should act.

   <!-- vantage: question id=OQ-PD2 leaning="B if the design-doc skill adopts provisional docs officially (else A locally first) — stage answers 'what does this doc owe someone?', and 'owes a worked design pass' is distinct from both SKETCH and worked DESIGN." -->

   _Leaning:_ B if the design-doc skill adopts provisional docs officially (else A locally first) — stage answers 'what does this doc owe someone?', and 'owes a worked design pass' is distinct from both SKETCH and worked DESIGN.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-PD3: Once Step 1 has landed, when does Step 2 run?**

   Step 1 carries initial questions. Treated like a finished design's questions, they would
   pause the item before the designer has surveyed the tree.

   - **A: queue Step 2 automatically at ordinary priority unless the owner holds the item.**
     Step 1 gives visibility at once; Step 2 researches and refines the questions before the
     item waits on the owner.
   - **B: wait after Step 1 until the owner answers the initial questions or asks for the
     worked pass.** Saves design spend on thoughts the owner wants to steer or drop first.

   <!-- vantage: question id=OQ-PD3 leaning="A by default, with an intake flag (`--provisional-only`) for B — most dictated ideas want both instant trunk capture now and a worked design when idle background capacity frees up." -->

   _Leaning:_ A by default, with an intake flag (`--provisional-only`) for B — most dictated ideas want both instant trunk capture now and a worked design when idle background capacity frees up.

   **Answer:**

   > _(empty — fill in when decided)_

4. 💬 **OQ-PD4: Does the `design-doc` skill adopt provisional capture as an official operation?**

   The skill requires its completeness list on every drafted design and compacts
   deliberation.

   - **A: add provisional capture to the skill.** Waives completeness while `PROVISIONAL`,
     standardizes the marker, and exempts `## Owner prompt` from compaction.
   - **B: keep provisional documents a tillr-local convention.** Leaves the skill untouched
     and scopes the waiver to drain intake.

   <!-- vantage: question id=OQ-PD4 leaning="A — the gap between dumping a raw design thought and having a clean, marked doc in the right shape happens in interactive non-tillr repos too, and preserving the verbatim owner prompt helps every design doc." -->

   _Leaning:_ A — the gap between dumping a raw design thought and having a clean, marked doc in the right shape happens in interactive non-tillr repos too, and preserving the verbatim owner prompt helps every design doc.

   **Answer:**

   > _(empty — fill in when decided)_
