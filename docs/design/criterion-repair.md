---
title: "Repairing a frozen criterion — the freeze protects the bar and traps a wrong one"
status: accepted
stage: DECIDED
next: "Every open question is ruled (OQ-CR2, OQ-CR3, OQ-CR5 and OQ-CR6 on 2026-11-17, and OQ-CR7 on 2026-11-20 naming the design seat; OQ-CR4 settled by a designer on 2026-11-18 and OQ-CR1 carried without an owner ruling remain open to the owner's overturn). No questions remain open; plan and build the criterion supersession record, its judge and item-page display, and routing a defective close to the design seat — nothing of criterion supersession is built."
depends-on:
  - tillr-judge-and-lifecycle.md
tags: [tillr, criterion, judge, design]
summary: "Once a verdict is recorded against a criterion its text is frozen, so a criterion that was wrong when written, or true and later stale, cannot be corrected, and the only exit, a defective verdict, closes the item. The repair is a criterion supersession: an append-only record written by the design seat that replaces the whole text, keeps the replaced text and the reason in front of the judge, and refuses a verdict written against the old text. The owner ruled its shape on 2026-11-17 and who writes it on 2026-11-20. No question is open, and nothing of criterion supersession is built."
vantage:
  status-chip: true
---

# Repairing a frozen criterion

**Status:** 2026-11-20. Nothing of criterion supersession is built: tillr refuses an edit to frozen criterion text and holds no record that replaces it (`rg -n -i 'supersession|superseded' tools/tillr-item` on 2026-11-20 finds only the unrelated record for owner decisions and planning-stage constants). The owner ruled four questions on 2026-11-17 and the fifth, [OQ-CR7](#OQ-CR7), on 2026-11-20. One designer's decision of 2026-11-18, [OQ-CR4](#OQ-CR4), and one unattributed settlement carried from [board utilities](./board-utilities.md#53-item-alone), [OQ-CR1](#OQ-CR1), remain open to his overturn, and no question is open.

> **In short.** The freeze is right and the repair path is missing. A criterion's text cannot change once a verdict has been recorded against it, so one that is wrong when written, or true and then stale, is permanent, and the only exit tillr has, a `defective` verdict, closes the item to fix its bar. The repair is one more append-only record beside the item's verdicts: a supersession that keeps the replaced text and the reason in front of the judge.

**Why it matters.** A correct decision cannot reach a wrong criterion. In the system tillr replaced, an item was decided against its own criterion's text and then waited on a ruling that, when it came, could change nothing in the words the judge applied ([§3](#3-the-defect-measured)).

**The shape.** One append-only record on the item, one clause in the store's refusal of edits to frozen text, and one section in what the judge is shown. No new store and no new verdict value.

**Cost.** A supersession discards any judge's verdict in flight against the old text. And it lets an agent that wants an item finished move that item's bar; the freeze exists to prevent exactly that, so the record answers it with visibility: a required reason, and the replaced text in front of the judge.

**Start at** [§5](#5-the-shape-any-repair-has-to-have), which holds the rulings, and the [Decision Ledger](#decision-ledger).

**Needs your ruling:** None.

**Reads with:** [the judge and lifecycle design](./tillr-judge-and-lifecycle.md) (the verdicts, the judge step and the close and reopen commands this builds on), and [planning and model tiering](./tillr-planning-node-model-tiering.md), whose rule that a planner never edits what it is held to matches [OQ-CR7](#OQ-CR7)'s ruling that the design seat supersedes a frozen criterion.

**Words.** [The vocabulary](./tillr-vocabulary.md#5-the-words) defines item, criterion, criterion verdict and judge. Four more are used here:

- **Freeze**: the store's refusal to change a criterion's text once a criterion verdict has been recorded against it.
- **Criterion supersession** *(coined in this document, 2026-10-21)*: a record that replaces a frozen criterion's whole text and names the text it replaced and why. It is not an edit, and it is not the vocabulary's *supersession*, which is the lander's finding that a change is already on the trunk.
- **Design seat**: the role that answers a question about an item's definition — editing the item's block or its design document before a verdict, and superseding a frozen criterion after one ([OQ-CR7](#OQ-CR7)). It is neither the worker that builds the item nor the judge.
- **The whale**: the coordinating session of the board that tillr replaced. It was deleted with the board on 2026-11-11 and nothing in tillr holds that role.

## 1. The invariant the freeze protects

> **P1. A criterion may become harder to satisfy, never easier — and no part of it may move once a
> result can be seen.**

The judge applies the criterion, so a writer free to rewrite the criterion after seeing the result could pass anything. Three mechanisms enforce it in tillr, and a repair has to sit inside all three:

| Mechanism | What it stops |
| :--- | :--- |
| **The freeze.** `tillr item apply` refuses a change to a criterion's text once a verdict is recorded against it | A writer changing the bar after it was graded |
| **Hardening only.** A criterion's claim class may move toward `owner-only` and never back | A criterion being reclassified so that a weaker actor may retire it |
| **A verdict names the text it judged.** Every entry a judge writes carries a hash of the criterion's text, and the intake refuses one that does not match the stored text | A verdict computed against one text being recorded against another |

An attempt that did not land writes no verdict ([the judge design, section 4.1](./tillr-judge-and-lifecycle.md#41-attempt-outcomes-are-not-criterion-verdicts-d-jl5)), so the freeze begins only at a real grading: `done` after a landing, or `defective`.

## 2. What tillr can do about a criterion today

| When | Mechanism | Effect on the criterion | What it costs |
| :--- | :--- | :--- | :--- |
| Before any verdict | The design seat edits the item's block. A worker that finds the item's words wrong ends with a `definition` obstacle, which sends the item there | The text is corrected in place | Nothing. This path works, and it is why most wrong criteria never freeze |
| At judging | A `blocked` finding, waiting on the design seat | None; the item waits | The text is still editable, so the design seat can correct it |
| At judging | A `defective` finding | The verdict is recorded and the text freezes | The item is closed to fix its bar |
| After a verdict | `tillr item reopen`, with a reason | None: the item returns to the state it was closed from, with the same frozen text | The item is judged again against the clause that was wrong |
| After a verdict | A new item that states what it replaces | A new criterion | The old id's runs and verdicts stay on a closed row with no link, and `tillr item sync` refuses a copy of a held item under a new id |

So a criterion that carries a `done` or `defective` verdict has no repair path. That is the gap.

## 3. The defect, measured

**In tillr, not yet after a verdict.** No tillr record shows a frozen criterion that needed repair. The nearest observation is before the freeze: in the pass of 2026-11-17 a build run blocked on a criterion that named a path upstream no longer had, and the design seat rewrote it on its own authority ([planning and model tiering](./tillr-planning-node-model-tiering.md#OQ-PT3) records it). The case below is therefore measured in the predecessor and anticipated in tillr.

**In the system tillr replaced, three filings of 2026-10-21.** They are quoted from the filings' own text; that system's journal held no verdict event for any of the three when it was read.

- **A correct decision could not reach the text.** A criterion required that a provider entry and its `or-200k` profile be deleted. The landed change, `f57809b7`, deliberately kept the profile. The item waited on the owner, and rightly, but a ruling of "keep it" would still have left the criterion ordering a deletion, so the judge would have kept returning not-done against a decision already made. Nothing was wrong with the item, the work or the judge.
- **A premise expired.** An item was filed at 00:20 from a trunk where one check really was red, and the check was fixed at 00:51 by `8260c429`. The criterion asked for evidence about the red: output quoted before and after, the cause named. That evidence described a state of the world that no longer existed, so the item became unworkable without becoming wrong. Its closing note names the pattern six times: an item filed from a measurement stops being true when the tree moves.
- **A criterion that could never be judged.** A criterion named an artifact a cleanup had deleted. The item was parked, resumed and parked again 35 times.

## 4. Why `defective` is not the repair

It is the closest thing tillr has, and it fails on four counts:

- **It closes the item.** The finding is about the criterion, but the consequence falls on the item: the queue stops working on it. Fixing a bar should not cost the thing behind it.
- **It freezes what it found wrong.** A `defective` verdict is a verdict, so the text it condemns can no longer be edited, and reopening the item re-judges it against the same clause.
- **It asks the judge to move the bar.** `defective` is the one verdict that is a judgement about the criterion and not the work. Leaving repair to it makes the judge the criterion's author by another route.
- **It cannot be reviewed as a repair.** A `defective` reason says what is false. It does not say what the bar should be instead, so the next writer starts from nothing.

## 5. The shape any repair has to have

A repair supersedes the whole criterion: it writes a new record that names the text it replaced and why. It never strikes a clause out, and it never edits the frozen text, because the text being repaired is the text a reader has to be able to quote. The record is six things:

1. **Append-only.** Nothing rewrites a frozen criterion in place. The record grows, as the item's verdicts and its close and reopen records do.
2. **Attributable, and written by the design seat ([OQ-CR7](#OQ-CR7)).** The record says who wrote it, when, and against which prior text. The design seat writes it: it already rewrites a criterion before a verdict, and it is neither the worker nor the judge. An agent can lower a bar; the rulings of 2026-11-17 and 2026-11-20 ([OQ-CR2](#OQ-CR2), [OQ-CR7](#OQ-CR7)) put the guard in visibility, in items 3 and 4, and not in a narrower permission.
3. **Reasoned, with the reason required.** `tillr item reopen` and `tillr item close` refuse an empty reason for the same purpose. A supersession that can be written without one is a silent rewrite.
4. **Visible to the judge, with both texts.** The judge is shown the replaced text, the current text, the reason, and which replaced which, and the item's page shows the same. The prior text is the only thing that makes a weakening visible to the judge. The judge's brief says *replaced*, never *added*: the wrong word turns a supersession into a second requirement.
5. **One standing text per item and criterion.** However many supersessions a criterion accumulates, exactly one text is current, and every reader (the judge, the item page, the worker's brief) derives the same one.
6. **A verdict names the text it judged.** A supersession changes the criterion's hash. A judge's file written against the old text is refused by the intake's existing hash check, and the item is judged again against the current text. That discards the judging in flight and needs no new mechanism.

`tillr item reopen` is the precedent to copy: an append-only record with a required reason and a named author, written by one command.

### 5.1 An unsatisfiable criterion is reopened, then superseded

A judge that finds a criterion cannot be satisfied says so with the finding it already has, `defective`; there is no new verdict value ([OQ-CR5](#OQ-CR5)). When a judge closes an item as `defective`, the loop sends the item to the design seat, which reopens the item with a reason and writes the replacement text and its reason ([OQ-CR7](#OQ-CR7)). The item returns to the queue with both texts and the reason shown on its page and to the judge, and the owner is not asked. The owner's condition on [OQ-CR5](#OQ-CR5) is that this sequence is legal and cheap:

- **Reopen is built.** The judge's `defective` finding closes the item through the same path as `tillr item close`, which records the state the item was in, and `tillr item reopen` restores that state.
- **Supersede and the design-seat route are not built.** A reopened item's criterion still carries its verdict, so its text stays frozen, and a `defective` close still names the owner as mover until the supersession record and the loop's route to the design seat are built.
- **Cheap is unmeasured.** The intent is two steps and no new item: the id, its runs and its verdicts stay together.

### 5.2 An expired premise is reported with the verdicts the judge already has

The judge step gets no separate check of a criterion's premise ([OQ-CR4](#OQ-CR4), a designer's decision). A judge that finds the premise gone already has two ways to say so: `blocked`, waiting on the design seat, with its reason, when the criterion can be corrected; and `defective`, when no work could satisfy it. Before any verdict the worker says it first, with a `definition` obstacle. What a structural check would add is small: it can tell that a file or a check a criterion names was deleted, and it cannot tell that a measurement the criterion quotes no longer holds, which is the case that was measured.

## 6. What must not change

P1 stands. Four plausible designs break it, and they are named here so they are not re-derived:

- **A bare retraction** that does not record the text it removed. The item subtracts from its own bar and the record cannot show what was subtracted, so a weakened criterion and a repaired one look the same afterwards.
- **A supersession the judge never sees.** If the judge is shown only the current text, the one actor applying the bar cannot tell a repair from a lowering.
- **A supersession that rewrites the frozen text in place.** It is the cheapest to build and it deletes the audit trail the freeze exists to be.
- **A repair only a person can perform.** The owner rejected it on 2026-11-17 ([OQ-CR2](#OQ-CR2)) and again on 2026-11-20 ([OQ-CR7](#OQ-CR7)): an item parked until one person acts is a dead end.

## 7. Open Questions

None. [OQ-CR7](#OQ-CR7) was ruled by the owner on 2026-11-20 and compacted into the [Decision Ledger](#decision-ledger) alongside the rulings of 2026-11-17 ([OQ-CR2](#OQ-CR2), [OQ-CR3](#OQ-CR3), [OQ-CR5](#OQ-CR5), [OQ-CR6](#OQ-CR6)), the designer's decision of 2026-11-18 ([OQ-CR4](#OQ-CR4)), and [OQ-CR1](#OQ-CR1).

## Decision Ledger

**Who decided each row.** A row dated "owner" is the owner's ruling. A row marked "designer's decision" was settled without him, and he may overturn it.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-CR1 | A repair supersedes the whole criterion with a new frozen record that names the text it replaced and why. It never retracts a clause and never edits the frozen text. Settled in [board utilities](./board-utilities.md#53-item-alone) (`D-UT16`), which is not recorded as an owner ruling; tillr's refusal of an edit to frozen text was built with it | 2026-11-16 (no owner ruling recorded) | [§5](#5-the-shape-any-repair-has-to-have) | — |
| OQ-CR2 | The whale supersedes a criterion itself, with a required reason, and the replaced text goes in the judge's packet and on the board. Visibility, not a narrower permission, is the guard against the whale lowering a bar it wants retired, because an owner-only repair is the owner-only-resume shape the pile already forbids. **The actor this ruling names no longer exists; [OQ-CR7](#OQ-CR7) gives the act to the design seat. The rest stands** | 2026-11-17, owner | [§5](#5-the-shape-any-repair-has-to-have), items 2 to 4 | — |
| OQ-CR3 | The judge sees both texts, the reason, and which replaced which, and its prompt says *replaced*, not *added*. The prior text is the only thing that makes a weakening visible to the judge | 2026-11-17, owner | [§5](#5-the-shape-any-repair-has-to-have), item 4 | — |
| OQ-CR4 | **Designer's decision, 2026-11-18; not ruled by the owner; the owner may overturn it.** The judge step gets no separate check of a criterion's premise. A judge reports an expired premise with the `blocked` or `defective` verdict it already has, and a worker with a `definition` obstacle. **Rejected:** a structural check that the things a criterion names still exist, which catches only a deleted file or check and has nothing in the tillr judge to reuse; and a new "premise moved" note and state, which is what `blocked` with a waiter already is. Evidence: both judge outcomes are built and routed, and the one case measured was a quoted measurement that stopped holding, which a structural check cannot see | 2026-11-18, designer | [§5.2](#52-an-expired-premise-is-reported-with-the-verdicts-the-judge-already-has) | ✅ |
| OQ-CR5 | An unsatisfiable criterion stays a judge finding (`defective`), with no new verdict value, and the repair is to reopen the item and then supersede the criterion. The owner's condition is that this sequence is legal and cheap. Reopen and the judge's `defective` close are built; the supersession record is not | 2026-11-17, owner | [§5.1](#51-an-unsatisfiable-criterion-is-reopened-then-superseded) | Partly: no supersession record |
| OQ-CR6 | A supersession refuses a verdict in flight: the mismatch between the judged text and the current text rejects it and the item is judged again against the current text, because a verdict names the world it judged | 2026-11-17, owner | [§5](#5-the-shape-any-repair-has-to-have), item 6 | — |
| OQ-CR7 | **Owner ruling:** The design seat replaces a criterion's text after a verdict has frozen it: when a judge finds a criterion `defective`, the loop sends the item to the design seat, which reopens it and writes the replacement and its reason, returning the item to the queue with both texts and the reason shown on its page and to the judge without asking the owner. Ruled on 2026-11-20 as a review comment: "A — the design seat: it already rewrites a criterion before a verdict, it is neither the worker nor the judge, and your ruling of 2026-11-17 chose visibility over a narrower permission." | 2026-11-20, owner | [§5](#5-the-shape-any-repair-has-to-have), item 2; [§5.1](#51-an-unsatisfiable-criterion-is-reopened-then-superseded) | — |

## 8. What this does not decide

- **The surface.** Whether the design seat writes a supersession ([OQ-CR7](#OQ-CR7)) through a command, a store write, or both is left to the implementation plan. No event, field or command name is proposed here.
- **Whether a criterion should be re-verified at filing.** The expired-premise class would be caught earlier by measuring a quoted red before the item is filed. That is a filing rule, not a repair.
- **How the hold view spells the route before the design seat runs.** Today `judge-defective` names the owner as mover; routing a `defective` close to the design seat ([OQ-CR7](#OQ-CR7)) replaces that mover, and the exact reason and wording belong to the implementation plan.

## 9. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Leave it; `defective` is the repair | Rejected. A correct decision could not reach the text, and `defective` answers a wrong bar by closing the item |
| Append a retraction sentence to the criterion | Rejected. It produces a self-contradicting text the judge is told to apply in full |
| File a replacement item and drop the old one | Rejected as the standing mechanism. It orphans the old id's runs and verdicts with no link |
| Hand-edit the frozen record | Rejected. It rewrites the freeze, and recorded verdicts would then name a text that no longer exists |
| Expire a criterion after some number of verdicts | Rejected. It makes the bar a function of effort and not of the world |
| Let the judge propose the replacement text | Rejected. A proposal is authorship, and the judge may not author the bar it applies |
