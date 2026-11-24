---
title: "Tillr convergence: three structural causes, one target"
status: accepted
stage: DECIDED
updated: 2026-11-24
tags:
  - tillr
  - drain
  - queue
  - observability
  - design
summary: "Names three structural causes of a queue that stops — derived copies that are checked instead of derived, failures that become holds only a person can clear, and decisions that are recorded but not shown — and sets one target: a cold machine rebuilds the queue from documents, drains it, and shows every stopped item with the act that moves it. Document sync, the waiting and why faces and hold classes are built; post-run cause statements, the two-unrelated-items stop, removed-block retirement, derived-copy regeneration and profile-owned model ids are unbuilt."
next: "Build the two D-TC3 units in Section 4: the cause statement, then the stop on two unrelated items. Settle the removed-block retirement rule in D-TC2 against the triage of unshared open items; D-TC5 waits on OQ-SH2, and OQ-TC2 waits on harness profiles and OQ-PT7/OQ-PT8."
depends-on:
  - ../reference/tillr-queue-persistence.md
  - landing-principles.md
  - ../reference/tillr-drain-successor.md
  - momentum-principle.md
vantage:
  status-chip: true
---

# Tillr convergence: three structural causes, one target

> **Delivered copy.** This is `docs/design/tillr-convergence.md` from this repository at commit `08df476656` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-convergence.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Partly built. Built: the item-block reader (`D-TC1`), sync as the document intake
(`D-TC2`, apart from removed-block retirement), the "why is it not moving" face (`D-TC4`), and hold
classes and movers (`D-TC3`, in part). Not built: the post-run hold cause statement and the
two-unrelated-items stop, both drafted in [Section 4](#4-what-is-left-to-build); removed-block
retirement (`D-TC2`), which waits on a design decision this document owes; regenerating derived
copies (`D-TC5`) and moving model ids out of the policy files ([`OQ-TC2`](#OQ-TC2)), which wait on
other documents' open questions. The [Decision Ledger](#decision-ledger) names what each one waits
on. `D-TC1` to `D-TC5`, [`OQ-TC1`](#OQ-TC1) and [`OQ-TC2`](#OQ-TC2) are designer's decisions that the
owner may overturn.

> **In short.** A queue that keeps stopping is not a complexity problem. Three structural causes
> account for it, and each has a principle written down in this tree. Enforce those three,
> and the loop stops needing repair.

**The shape.** One invariant to build toward — *delete `.tillr/`, sync, drain* — and three rules
that make it hold: derive every copy, give every hold a mover, show every decision.

**Start at [Section 2](#2-the-three-causes)** — the causes. The target falls out of it.

**Needs your ruling:** No open `OQ-*` directive in this document. Removed-block retirement versus
triage of an unshared open item (`D-TC2`) is a design decision owed here,
`D-TC5` waits on [`OQ-SH2`](tillr-selfhosted-build.md#OQ-SH2), and the agent-authored decisions in
the [Decision Ledger](#decision-ledger) remain open to the owner's review and overturn (tracked in
[`decision-provenance-review.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md):
[`OQ-PR6`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR6),
[`OQ-PR8`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR8),
[`OQ-PR10`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR10)).

**Reads with:** [queue persistence](../reference/tillr-queue-persistence.md) (documents are the
intake), [`tillr-queue-mirrors-designs.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md)
(the ruling on [`OQ-QM1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#OQ-QM1) that puts build tasks in companion
`<topic>-plan.md` files), [`landing-principles.md`](landing-principles.md) (the principles the three
rules enforce), [`tillr-drain-successor.md`](../reference/tillr-drain-successor.md) (the
loop model the hold classes project onto), [`tillr-claude-backend.md`](../reference/tillr-claude-backend.md)
(the harness a seat may name in [Section 3.5](#35-a-seat-names-its-harness-and-the-harness-names-the-model)), and
[the failure-diagnosis section](tillr-judge-and-lifecycle.md#424-diagnosing-a-repeated-failure-the-unbuilt-mechanism) (the mechanism
[Section 3.2](#32-every-hold-names-a-mover-that-can-act) reuses).

---

## 1. The cold-machine test

The tenet is that state lives in documents. A machine with no `.tillr/` directory measures the tenet
directly: whatever the queue is, it must be rebuildable there from tracked files alone.

> [!WARNING]
> A scanner that only skips fenced code is not enough. Design documents write the item directive
> inside inline code when they discuss it, and a parser that reads each mention as a block reports
> them all as malformed. A directive counts only when its opening `<!--` starts a line outside a
> fence (`D-TC1`).

## 2. The three causes

### 2.1 A derived copy is checked instead of derived

A **derived copy** *(coined here)* is a tracked file whose whole content follows from another
tracked file. It is not a cache: a cache is untracked and can be deleted. A derived copy with a
check that compares it to its source refuses correct work until someone repairs the copy by hand.

| Derived copy | Source |
| :--- | :--- |
| The pack mirror under `yolo-packs/tillr/` | `tools/tillr*` |
| The module list, kept in more than one file | the `tools/` directory |

The fix has one shape: the copy becomes a function the pipeline computes, and nothing asks a worker
to remember it ([Section 3.4](#34-a-derived-copy-is-produced-by-the-pipeline)).

### 2.2 A failure becomes a hold only a person can clear

After a run, the planner holds an item for one of six reasons: `boundary-scope-escape`,
`candidate-no-op`, `conflict-exhausted`, `gate-failed`, `run-unjudged`, `run-link-missing`
(`tools/tillr-drain/drain/schema.go`). A hold that records only its reason does not record what
would have to be different for a second run to succeed, so a failure reads as "stuck" and the only
door is a retry command.

[`landing-principles.md`](landing-principles.md) rules this:
[every outcome is state-changing](landing-principles.md#4-every-outcome-is-state-changing),
[only verdicts charge](landing-principles.md#5-only-verdicts-charge),
[fix the machinery; never hand-carry the work](landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work)
and [a failure is diagnosed, not waited out](landing-principles.md#11-a-failure-is-diagnosed-not-waited-out).

### 2.3 A decision is recorded and not shown

The store holds what the loop decided: `dispatches` has every dispatch and hold with its reason. A
surface that shows only counts and a stop word leaves the question the owner actually asks — *why is
this item not moving, and what moves it* — unanswered, and each wrong count is then fixed one field
at a time.

## 3. The target

One invariant, checkable on any machine:

> **Delete `.tillr/`, run sync, run the drain. The queue is the same queue, the drain proceeds, and
> one command lists every item that is not moving with the act that will move it.**

Three rules make it hold. Each is stated as behavior; mechanism is the implementer's unless named.

### 3.1 The queue is read from documents, and from nothing else

- **One reader.** `tillr item sync` reads item blocks from tracked Markdown and writes missing items
  to the store. It is the only way a document-declared item enters a store. It runs at drain start,
  after each trunk change, and on demand.
- **Idempotent.** A second run with unchanged documents writes nothing and appends no event.
- **Never destroys local facts.** Sync does not change the rank, the runs or the `landed` state of
  an item the store already holds.
- **A changed block is a changed item.** When a block's paths or criteria differ from the store's,
  sync updates the definition and records that it changed. Editing the block is the door back; no
  cloned item is needed to revise one.
- **A removed block retires the item** in the store unless it has a live lease, in which case sync
  reports it and changes nothing. *(Not built; see `D-TC2`.)*
- **Malformed input stops sync, not the drain.** A block with no `id`, no path, no criterion, a
  duplicate id, or a `blocked_by` naming no block is reported with file and heading, and no item is
  written from that file. Items from other files are unaffected.
- **Zero blocks is a valid queue.** Sync exits 0 and the drain idles.
- **No trailer.** The lander stamps no item trailer on a landed commit; the receipt is the block's
  absence.
- **No second intake file.** There is no seed file and no `seed` face
  ([OQ-TC1](#OQ-TC1)).

### 3.2 Every hold names a mover that can act

A **mover** is the act that will next change a held item, as
[`landing-principles.md`](landing-principles.md) uses the word. Every hold belongs to one of three
classes, and the class is part of the hold's record:

| Class | Holds | Mover |
| :--- | :--- | :--- |
| **Waits on the owner** | an open question in the design document; a block the owner iceboxed | the owner's ruling, read from the document |
| **Waits on work** | an unfinished blocker; a red trunk; a fork delivery in flight; no free lane | another item's transition, which the loop watches |
| **Needs a cause** | the six post-run holds of [Section 2.2](#22-a-failure-becomes-a-hold-only-a-person-can-clear) | a named change to an input, or a diagnosis |

The third class is the change. A post-run hold records a **cause statement**: the stage that
failed, the normalized error, and the **inputs** the run had — the item's definition, the tillr
build, the backend policy, and the base commit.

- **Unchanged inputs never re-run.**
- **A changed input does not re-run an item either.** Under the owner's ruling
  (`D-JL10` in [the judge and lifecycle design](tillr-judge-and-lifecycle.md#decision-ledger)),
  nothing re-runs because an input changed, and a worker run's ending is never counted: a refused
  run goes back to its worker's own session with its evidence.
- **The same failure on two unrelated items is the machinery's.** When one stage and error holds
  items that share no path, the loop stops dispatching, says so, and files one defect. It does not
  hold the items one by one. Two is the count because `RepeatedRefusalThreshold` is 2.

### 3.3 Every decision is shown where the owner looks

- **One answer to "why is it not moving".** A read face lists every item that is not landed with
  its position, its hold, the hold's class, the mover, and how long it has waited. It reads only
  the store. It is text first, so it works in a terminal and can be pasted into a conversation; the
  board draws the same rows.
- **One answer to "what did the loop just do".** Each pass's decisions are listed in order: what was
  dispatched, what was held and why. The data is in `dispatches`.
- **A stop says its sentence.** The loop's stop detail is shown wherever the stop reason is.
- **Visibility ships with the behavior.** A change that adds a hold or a stop adds its row to these
  faces in the same commit.

### 3.4 A derived copy is produced by the pipeline

- **The pipeline regenerates, the gate compares.** Between a run's commit and its gate, the loop
  regenerates every derived copy in the lane and commits the result into the candidate. A worker is
  never asked to; a brief never mentions it.
- **A count is asserted structurally or not at all.** A test may assert that every item has a fence
  and a criterion. It may not assert how many items exist.
- **A list has one home.** The module list is read from the directory by both of its readers.

This is the direction [`tillr-selfhosted-build.md`](tillr-selfhosted-build.md) leans for the
mirror; that document's questions stay there and are not reopened here.

### 3.5 A seat names its harness, and the harness names the model

A **seat** is a role an item may ask for (design, build, review, scout). A **harness** is the
backend adapter that runs a seat's work — pi, or the `claude -p` backend of
[`tillr-claude-backend.md`](../reference/tillr-claude-backend.md).

- **Tillr's tracked policy names seats and, for each, the harness that fills it.** Which harness
  fills a seat stays a tillr choice, because the owner chooses which seats run on the Claude
  subscription.
- **The model comes from the harness's own profile on the machine.** Tillr's tree names no model
  id; a machine changes models by changing its profile.
- **The per-machine-group policy files are the stopgap.** The `config/tillr/backend-policy*.json`
  files stay until a harness profile can answer for the design seat, then the model ids leave them.
  ([OQ-TC2](#OQ-TC2).)

## 4. What is left to build

Two units of `D-TC3` are drafted below. Neither is filed as an item: each becomes a task in a
companion plan under [`OQ-QM1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#OQ-QM1), or is filed with
`tillr item request`.

### Record a cause statement with each post-run hold

When a run fails after it starts, the loop holds its item for one of six reasons. The hold
says which reason, but not what went wrong or what would have to change for a second run to
succeed. So a person who reads a held item must open the run's log to find the stage that
failed and the error it printed. The loop records the run's inputs when it dispatches
the run: the item's block, its allowed paths, the backend policy and the tillr build
(`RunInputs` in `tools/tillr-drain/drain/readmit.go`), and
`tools/tillr-drain/drain/returned.go` returns a worker refusal with normalized check-log digests.
The hold row itself does not point at those inputs or carry a normalized cause statement.
[Section 3.2](#32-every-hold-names-a-mover-that-can-act)
asks that each post-run hold carry a **cause statement**, made of three parts: the stage that
failed, the error in a normalized form that two runs can share, and the run's recorded inputs.
The waiting face then shows that statement beside the mover, so the reader sees what to change.

**Outcomes:**

- Record what failed, and with which inputs, whenever a run's item is held.
- Show the cause beside the hold wherever the owner reads why an item is not moving.
- Give the next unit a normalized error it can compare across items.

It waits on nothing.

- **c1:** Each of the six post-run holds named in
  [Section 2.2](#22-a-failure-becomes-a-hold-only-a-person-can-clear) records a cause statement
  with the hold. The statement names the stage, the normalized error and the inputs the held run
  had.
- **c2:** The normalized error has no run id, lane path, worktree path, timestamp or temporary
  directory in it. Two runs that fail the same check with the same message, in different lanes
  and at different times, produce the same normalized error.
- **c3:** The statement's inputs are the ones the loop recorded when it dispatched that run, not
  a second reading taken at hold time.
- **c4:** `tillr drain why` and the waiting face show the stage and the normalized error on the
  held item's row, and a hold recorded without a cause statement shows that it has none.
- **c5:** The drain README states the cause statement's fields, and the convergence Decision
  Ledger's `D-TC3` cell names where it is built.

**Agent Details:**

- **Proposed id:** `item-drain-hold-cause-statement`.
- **Paths:** `tools/tillr-drain/`, `tools/tillr-view/`,
  `yolo-packs/tillr/files/local/share/tillr/tillr-drain/`,
  `yolo-packs/tillr/files/local/share/tillr/tillr-view/`, and this document.
- **Ref:** [Section 3.2](#32-every-hold-names-a-mover-that-can-act) and `D-TC3` in the
  [Decision Ledger](#decision-ledger).
- **Where:** the six reasons are in `tools/tillr-drain/drain/schema.go`. The recorded inputs are
  the `drain.inputs` event, whose shape is `RunInputs` in `tools/tillr-drain/drain/readmit.go`.
  The waiting rows are built in `tools/tillr-view/waiting.go` and `tools/tillr-view/why.go`.
  Run `just pack-sync` after the last source edit and commit the regenerated mirror.
- **Verify:** Add drain tests that drive each of the six holds with a stub run and check the
  recorded statement. Add a test that two failures in different lanes normalize to the same
  error. Add a view test for the row text and for a hold that has no statement. Run the Go suites
  of `tools/tillr-drain` and `tools/tillr-view`.

### Stop dispatch when one failure holds two unrelated items

A failure that holds two items which share no file is unlikely to be either item's fault. It is
far more often the machinery's: a broken gate, a missing tool, a backend that refuses every
request. The loop holds each such item on its own and keeps dispatching more work into
the same failure, so the queue fills with held items while the real defect goes unnamed. The
loop counts refused items, but that count only reports and holds nothing.
[Section 3.2](#32-every-hold-names-a-mover-that-can-act) sets the rule: when one stage and one
normalized error hold two items whose allowed paths share nothing, the loop stops dispatching,
says why, and files one defect item. It does not hold the items one by one.

**Outcomes:**

- Stop new dispatch when one failure reaches two unrelated items.
- File one defect item that names the stage, the error and both items.
- Resume dispatch once that defect item lands, with no command to run.

It waits on the cause statement unit above, whose normalized error it compares.

- **c1:** When two post-run holds carry the same stage and normalized error, and their items'
  allowed paths share no path, the loop dispatches no new run. Runs already in flight finish.
- **c2:** The loop files exactly one defect item for that stage and error, through its existing
  defect path. The item's text names the stage, the normalized error and both held items. A
  third item held by the same failure files nothing new.
- **c3:** Two items whose allowed paths overlap, held by the same failure, are held one by one,
  and dispatch continues.
- **c4:** The loop's status and `tillr drain why` say in one line that dispatch is stopped, why,
  and which defect item will release it.
- **c5:** When the defect item lands, the next pass dispatches again.
- **c6:** The repeated-refusal report keeps its meaning, as a report that holds nothing.

**Agent Details:**

- **Proposed id:** `item-drain-unrelated-failure-stop`.
- **Paths:** `tools/tillr-drain/`, `tools/tillr-view/`,
  `yolo-packs/tillr/files/local/share/tillr/tillr-drain/`,
  `yolo-packs/tillr/files/local/share/tillr/tillr-view/`, and this document.
- **Waits on:** `item-drain-hold-cause-statement`, the unit above.
- **Ref:** [Section 3.2](#32-every-hold-names-a-mover-that-can-act) and
  [the risks table](#6-risks), whose first row relies on this rule.
- **Where:** the defect path is `fileDefectItem`, with `DefectItemPrefix` and
  `ReasonToolingDefect`, in `tools/tillr-drain/drain/defect.go`. The report is
  `RepeatedRefusalThreshold` in `tools/tillr-drain/drain/refusal.go`. A new stop reason is
  entered in `tools/tillr-drain/drain/definition.go`, and its row goes in
  `tools/tillr-view/waiting.go` in the same commit. Run `just pack-sync` after the last source
  edit and commit the regenerated mirror.
- **Verify:** Add drain tests: two unrelated items held by one failure stop dispatch and file
  one defect; two overlapping items do not stop it; a third item files nothing new; and landing
  the defect resumes dispatch. Add a view test for the stopped line. Run the Go suites of
  `tools/tillr-drain` and `tools/tillr-view`.

## 5. What this does not propose

- No new item state.
- No change to the gate's checks or the lander's squash.
- No retry budget or cooldown, and no counted endings (`D-JL10`).
- No committed queue snapshot and no rank in a document.

## 6. Risks

| Risk | Mitigation |
| :--- | :--- |
| A drain on new models fails for reasons that are not the items' | It runs supervised on one lane, and a machinery failure stops it under the two-item rule |

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-TC1` | A directive counts only when its opening `<!--` starts a line outside a fenced block. **Rejected:** skipping fences alone, which reads inline mentions as malformed blocks. | 2026-11-15 | [Section 1](#1-the-cold-machine-test) | ✅ (`tools/tillr-item/item/sync.go`, `TestSyncIgnoresInlineMentionsAndFencedDirectives`) |
| `D-TC2` | Sync is the only document intake, is idempotent, never rewrites rank, runs or `landed`, and treats a changed block as a changed item. A block may name a `seat` ([block grammar](../reference/tillr-queue-persistence.md#block-grammar-and-attributes)), which is part of the definition a changed block changes. **Rejected:** a cloned item as the way to revise a judged item. | 2026-11-15 | [Section 3.1](#31-the-queue-is-read-from-documents-and-from-nothing-else) | Partial. Built: `tillr-item sync`, which the loop runs at start and after each trunk change, is the only document-block intake (`tillr-item` also accepts store-declared CLI filings `idea`, `doc-triage` and `request`). Sync is idempotent, never moves state, rank or runs, and updates a changed definition through an `item.update` event; once a criterion carries a state, the row is left alone and reported `definition-frozen`. **Not built, and not filed:** a removed block retiring its item. An open item with no block (`UnsharedOpenIDs` in `tools/tillr-item/item/sync.go`) goes to triage on the design seat (`triageDecision` in `tools/tillr-drain/drain/document_prerequisite.go`, called from `tools/tillr-drain/drain/plan.go`) and is not retired. Which of the two rules stands is a design decision this document owes, so no item is filed for it |
| `D-TC3` | Every hold carries one of three classes and a mover; a post-run hold records a cause statement with its inputs. **Rejected:** a retry door per hold reason. | 2026-11-15 | [Section 3.2](#32-every-hold-names-a-mover-that-can-act) | Partial. Built: the classes and movers, in the definitions every hold reason is entered in (`tools/tillr-drain/drain/definition.go`, `TestAllDefinedHoldReasonsAreClassified`) and the waiting face that shows them (`tools/tillr-view/waiting.go`, `TestWaitingEveryDrainReasonHasARule`); worker refusals also return with normalized check-log digests (`tools/tillr-drain/drain/returned.go`) and worker `tooling` obstacles file defect items (`tools/tillr-drain/drain/defect.go`). Not built: the post-run hold cause statement and the two-unrelated-items rule, written as the last two units of [Section 4](#4-what-is-left-to-build) |
| `D-TC4` | One read face answers why each unlanded item is not moving, text first; a new hold or stop ships its row in the same commit. **Rejected:** fixing the board one field at a time. | 2026-11-15 | [Section 3.3](#33-every-decision-is-shown-where-the-owner-looks) | ✅ (`tools/tillr-view/waiting.go` and `tools/tillr-view/why.go`) |
| `D-TC5` | The pipeline regenerates derived copies between commit and gate; tests assert structure and never a count. **Rejected:** asking the worker to regenerate, and a pre-commit hook, which fixes the human path only. | 2026-11-15 | [Section 3.4](#34-a-derived-copy-is-produced-by-the-pipeline) | — Not filed. It waits on [OQ-SH2](tillr-selfhosted-build.md#OQ-SH2) in [the self-hosted build design](tillr-selfhosted-build.md), which [Section 3.4](#34-a-derived-copy-is-produced-by-the-pipeline) leaves there. That question decides what becomes of the pack mirror and its check. Until then the module list keeps multiple homes (`MODULES` in `tests/pack-sync.test.py`, `MIRRORED_MODULES` in `tools/pack-docs.py`, and `MODULES` in `tests/tillr-docs.test.py` and `tests/tillr-build.test.py`) |
| OQ-TC1 | Sync is the only intake: there is no seed file and no working `seed` face of `tillr-item`. Designer's decision on recorded principle: the owner's standing goal that state lives in documents, and [queue persistence](../reference/tillr-queue-persistence.md)'s ruling that documents are the only intake. The owner may overturn it. **Rejected:** a second intake file kept beside the documents. | 2026-11-15 | [Section 3.1](#31-the-queue-is-read-from-documents-and-from-nothing-else) | ✅ (`tillr-item seed` refuses in `tools/tillr-item/main.go`) |
| OQ-TC2 | Tillr's tracked policy names seats and, for each seat, the harness that fills it; the model id comes from that harness's own profile on the machine, so tillr's tree names no model. The per-machine-group policy files stay as the stopgap until a harness profile can answer for the design seat. Which harness fills a seat stays a tillr choice, because the owner chooses which seats run on the Claude subscription. Designer's decision on recorded principle: the backend policy's rule that no model id is copied into `tools/tillr*`. The owner may overturn it. **Rejected:** per-group policy files as the target; an untracked per-machine file. | 2026-11-15 | [Section 3.5](#35-a-seat-names-its-harness-and-the-harness-names-the-model) | Partial (the stopgap: the `config/tillr/backend-policy*.json` files, each naming model ids). Not filed: the model ids stay until a harness profile can answer for the design seat, and [the planning-node model tiering design](tillr-planning-node-model-tiering.md), where only [`OQ-PT7`](tillr-planning-node-model-tiering.md#OQ-PT7) and [`OQ-PT8`](tillr-planning-node-model-tiering.md#OQ-PT8) remain open, maps seats to backend rows that still carry model ids |
| OQ-TC3 | **Withdrawn.** The proposal was that a held item is dispatched again by itself when an input it failed on has changed. The owner's ruling (`D-JL10` in [the judge and lifecycle design](tillr-judge-and-lifecycle.md#decision-ledger)) decides the opposite: nothing re-runs because an input's hash changed, and no ending is counted. | 2026-11-17 | [Section 3.2](#32-every-hold-names-a-mover-that-can-act) | ✅ as ruled: `tools/tillr-drain/drain/readmit.go` only records a run's inputs (`TestAChangedInputReDispatchesNothing` in `tools/tillr-drain/drain/readmit_test.go`) |
