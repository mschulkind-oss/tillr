---
title: "Repairing a frozen criterion — the freeze protects the bar and traps a wrong one"
status: in-review
stage: DESIGN
next: "Rule [OQ-CR4](#OQ-CR4) (whether the judge step looks for an expired premise); then design the supersession record for tillr's item store, which holds none today, under the five rulings in Section 5"
board: none
board-none-reason: not-yet-ruled
depends-on:
  - whale-recovery.md#decision-ledger
tags: [pi, work-control, pile, criterion, judge, design, whale]
summary: "A frozen criterion can be appended to but never retracted, so a criterion that is wrong when written, or true and later stale, is permanent — and the only exit, a defective verdict, retires the item. Measured in three filings: one decided against its own criterion's text, one whose premise expired, and one blocked on a missing verdict kind. The repair is a supersession record in the append-only shape reopened already uses. Five of its six questions are ruled: the whale supersedes with a required reason, the judge sees both texts, an unsatisfiable criterion is repaired by reopen then supersede, and a supersession refuses a verdict in flight. Whether to re-check a premise at judge time is open, and nothing is built."
vantage:
  status-chip: true
---

# Repairing a frozen criterion

**Status:** 2026-11-17. The owner ruled four of the five open questions on this date and
[tillr today](./tillr-today.md) settled the first on 2026-11-16; the rulings are in
[§5](#5-the-shape-any-repair-has-to-have) and the [Decision Ledger](#decision-ledger). Nothing is
built: tillr refuses an edit to frozen criterion text, but holds no supersession record. The
symbols were read in `work-control.ts` at `e7bdc147` on 2026-10-21, and that extension has since
been deleted; the pile evidence is its journal's newest snapshot and the items' own filed text, both
read that day.

**Needs your ruling:** [OQ-CR4](#OQ-CR4).

**Reads with:** [whale-recovery.md](whale-recovery.md) (`WR-D3`, the ruling that made criteria
additive and the reason they are additive only), [approver-agent.md](approver-agent.md) (the
approver's non-goals: it may not unfreeze a criterion), [pile-roadmap-division.md](pile-roadmap-division.md)
(the retirement-first sequence this repair must not pre-empt), [roadmap.md](../pi/roadmap.md) (routing).

> **In short.** The freeze is right and the repair path is missing. A criterion can grow but never
> shrink, so one that is wrong when written, or true and then stale, is permanent — and the only
> exit the pile has, a `defective` verdict, destroys the item to fix its bar. The repair belongs in
> the append-only item record beside `reopened`: a supersession that keeps the replaced text and
> the reason in front of the judge.

**Why it matters.** A correct decision cannot currently reach a wrong criterion. The first case
below was decided against its own criterion's text — the landed change deliberately keeps the profile the
criterion orders deleted — and the row sits `blocked-on-owner` waiting for a ruling that, when it
arrives, will change nothing in the words the judge applies.

**The shape.** One more item-record event beside `criterion-frozen`, `criterion-appended`,
`reopened`, `verdict` and `no-verdict`; one more clause in the write-time refusal; one more section
in the judge prompt. No new store, no second pile.

**Cost.** A supersession changes the criteria hash, so it throws away any judge pass in flight
against the old text; and the whale, the actor that most wants an item retired, is the actor that
may move its bar. The freeze exists to prevent that collusion, so the record answers it with
visibility rather than permission: a required reason, and the replaced text in front of the judge
([§5](#5-the-shape-any-repair-has-to-have)).

**Start at** [§5](#5-the-shape-any-repair-has-to-have), which holds the rulings, then the one open
question.

## 1. The invariant the freeze protects

> **P1. A criterion may become harder to satisfy, never easier — and no part of it may move once a
> result can be seen.**

The reasoning is `DEC-AW1`'s and it is sound: the judge applies the criterion, so a writer free to
rewrite the criterion after seeing the result could pass anything. Four mechanisms enforce it
today, and a repair has to sit inside all four rather than beside them:

| Mechanism | Symbol | What it stops |
| :--- | :--- | :--- |
| First freeze wins | `reduceItemEvent`'s `criterion-frozen` branch | A later freeze re-declaring the text |
| Write-time refusal | `criterionRewriteRefusal` | A pile write that edits, deletes or reorders settled text |
| Additions only | `settledCriterionText`, `recordCriterionAppends`, `CriterionAppend` | Growth that never subtracts |
| Snapshot identity | `approverCriteriaRefFor`, `approverSnapshotIdFor`, `verifyApproverEvidencePacket` | A criterion that moved under a verdict already computed |

The approver's own non-goals state the same rule from the other side: it may not unfreeze a
criterion, so an approver that dislikes one "says so in a verdict reason, and the owner rules"
([approver-agent.md](approver-agent.md)). What the design never added is the step after the owner
rules.

## 2. What the pile can do about a criterion today

Four mechanisms exist. Each is a real escape, and none of them repairs the text.

| Mechanism | Effect on the criterion | What it costs |
| :--- | :--- | :--- |
| Append (`criterion-appended`) | Adds words after the settled text | Cannot retract a false clause, so every re-judge re-applies the same premise |
| `defective` verdict | Retires the item, terminally | Destroys the item to fix its bar, and a wrong `defective` is as stuck as the wrong criterion was |
| `/whale-reopen` | Clears a `done` or `defective` verdict, with a required reason | Re-arms the item against the same text |
| A new item, the old one dropped | A genuinely new criterion | The old id's verdicts, dispatches and receipts stay attached to a dead row, and nothing links the two |

The fourth is not a fallback in theory — it is the recommended path in the refusal text itself,
which tells the writer to "append a new item whose text states what it replaces". That sentence is
the design's own admission that supersession is the operation needed, and it is currently done by
hand, outside the record, with no link back.

## 3. The defect, measured

**The first case — a correct decision cannot reach the text.** The criterion's operative clause requires
that "the `openrouter-200k` provider entry and its `or-200k` profile are deleted". The landed change
is `f57809b7`, *"feat(personal): land the 200k collapse with or-200k kept, children on the 1M row"*,
and `or-200k` is still a declared profile in
`personal.jsonc`. The row records
`state: blocked-on-owner`, and it is right to be: the missing input is a ruling. But the ruling is the
cheap half. Even a ruling that says "keep it" leaves the criterion ordering a deletion, so the judge
keeps returning NOT-DONE against a decision the owner has already made. That is the sharpest case in
the pile, because nothing is wrong with the item, the work, or the judge.

**The second case — a criterion whose premise expires.** Filed at 00:20 on 2026-10-21 from tip `706f74fa`,
where `work-board-storybook` really was red; fixed at 00:51 by `8260c429`, thirty-one minutes later.
The criterion asks for the check to pass *and* for evidence about the red — the rendered board quoted
before and after, the gate's verbatim output committed beside the change, the cause named rather than
inferred. Those are evidence of a state of the world that no longer exists, so the item became
unworkable without becoming wrong. Its own closing note names the pattern six times: a row filed from
a gate run or a reading stops being true when the tree moves, and nothing re-checks a premise.

**The third case — the same defect from the blocking side.** An item whose criterion needs a durable read
re-parks forever: `BLOCKED_RESUME_ON` names `head-move`, `run-completion` and `timeout`, and no
read-grant or config-change kind exists, while `BLOCKED_HORIZON_MS` re-surfaces the item every 24
hours. The instance: a criterion that names an artifact the cleanup deleted, a `waitReason` that
needs a read the session is disabled for, and `head-move` resuming it into the same park. The bill is
`attempts: 35`, each attempt with a packet of about 60 KB. That is a resume-vocabulary defect and this
doc does not solve it; it is cross-referenced because a
supersession whose only trigger is a human is the same shape one step over — a state nothing in the
pile can leave on its own.

**What is quoted rather than measured here.** The counts above, and the `defective` retirement
recorded for a criterion asserting a trunk sha the landed evidence contradicts, come from the three
filings' own text. This workspace's journal carries no verdict event for any of the three —
`rg '"kind":"verdict"' events.jsonl` over
`~/.pi/agent/work-control/--workspace--/events.jsonl` returns 1,034 verdict events over 130 item ids,
none of them these three — so the doc repeats those figures as reported, not as re-derived.

## 4. Why `defective` is not the repair

It is the closest thing the pile has, and it fails on four counts:

- **It retires.** The finding is about the criterion, but the consequence is the item — its work,
  its receipts and its id all end. Fixing a bar should not cost the thing behind it.
- **It is terminal.** A `defective` is as frozen as a criterion: `/whale-reopen` clears the verdict
  and leaves the text, so re-opening an item with a false criterion re-judges it against the same
  false clause.
- **It asks the judge to move the bar.** The approver is explicitly forbidden to unfreeze anything,
  and `defective` is the one verdict that is a judgement about the criterion rather than the work.
  Leaving repair to it makes the judge the criterion's author by another route.
- **It cannot be reviewed as a repair.** A `defective` reason says what is false; it does not say
  what the bar should be instead, so the next item starts from nothing.

## 5. The shape any repair has to have

A repair supersedes the whole criterion: it writes a new frozen record that names the text it
replaced and why. It never strikes a clause out of the additive log, and it never edits the frozen
text, because the text being repaired is precisely the text a reader has to be able to quote. The
record has to be six things, and the fourth is the one that is easy to lose:

1. **Append-only.** Nothing rewrites a `FrozenCriterion` in place; the record grows, as `reopened`
   and the additive log already do.
2. **Written by the whale, and attributable.** The whale performs a supersession itself; it does
   not wait on the owner. The record says who did it, when, and against which prior text. A repair
   only the owner could perform would reproduce the owner-only-resume shape the pile already
   refuses ([§6](#6-what-must-not-change)), so the guard against a whale lowering a bar it wants
   retired is items 3 and 4, not a narrower permission.
3. **Reasoned, with the reason required.** `/whale-reopen` refuses an empty reason for exactly this
   reason; a supersession that can be written without one is a silent rewrite.
4. **Visible to the judge, with both texts.** The packet carries the replaced text, the current
   text, the reason, and which replaced which, and the board shows the same. The defect being
   repaired is a criterion nobody could check, and the prior text is the only thing that makes a
   weakening visible to the judge. The prompt must say *replaced*, not *added*: the append sentence
   already tells the judge to apply the original together with its additions, and the wrong word
   turns a supersession into a second requirement.
5. **One standing text per item.** However many supersessions an item accumulates, exactly one text
   is current, and every reader — judge, board, prod — derives the same one.
6. **A verdict names the text it judged.** A supersession changes the criteria hash, so a judge pass
   in flight against the old text is refused by the snapshot-identity mismatch
   `verifyApproverEvidencePacket` already computes from content, and the item is judged again
   against the current text. That discards the running pass — a packet of about 60 KB per attempt —
   and needs no new code, no per-item lease and no mark for a verdict recorded against a stale text.

`reopened` is the precedent worth copying rather than inventing beside: an append-only record that
clears state, carries a required reason, is written by one command, and is read by the reducer
without a new store.

**An unsatisfiable criterion is a judge finding, and its repair is reopen, then supersede.** A judge
that finds a criterion cannot be satisfied says so with the finding it already has, `defective`;
there is no new verdict value. The repair is two steps: reopen the item, with a reason, and
supersede the criterion, after which the item is judged against the new text. The ruling holds on
the condition that this sequence is legal and cheap, and that is established for only half of it:

- **Reopen exists.** In the pile, `/whale-reopen` cleared a `defective` verdict with a required
  reason. In tillr, `tillr-item reopen` restores a closed item's prior state and refuses an empty
  reason (`Reopen` in `tools/tillr-item/item/lifecycle.go`). The judge outcome that closes an item
  as `defective` is not built yet: it is `item-jl-judge-blocked-and-defective` in
  [the judge and lifecycle design](./tillr-judge-and-lifecycle.md), so whether reopen accepts the
  close it writes is not yet checked.
- **Supersede does not exist.** tillr holds no supersession record. A reopened item's criterion
  still carries the state its verdict recorded, and `carryCriteria` in
  `tools/tillr-item/item/apply.go` keeps such a criterion's text frozen, so the supersession has to
  be the one write allowed to replace it.
- **Cheap is unmeasured.** The intent is two commands and no re-filing: no new item id, and the
  old id's runs and verdicts stay attached to it.

Until the supersession record exists and is accepted on a reopened item whose criterion carries a
verdict, the only legal repair is still a new item that states what it replaces
([§2](#2-what-the-pile-can-do-about-a-criterion-today)).

## 6. What must not change

P1 stands, and it is the test the rulings in [§5](#5-the-shape-any-repair-has-to-have) and the
open question below are run against. Four plausible
designs break it, and they are named here so they are not re-derived:

- **A bare retraction append** that does not record the text it removed. The item subtracts its own
  bar and the record cannot show what was subtracted, so a weakened criterion and a repaired one are
  indistinguishable after the fact.
- **A supersession the judge never sees.** If the packet carries only the current text, the one actor
  applying the bar cannot tell a repair from a lowering; only a human reading the item record can.
- **A supersession that rewrites `FrozenCriterion.text` in place.** It is the cheapest to implement
  and it deletes the audit trail the freeze exists to be.
- **A repair only the owner can perform.** It reproduces the shape the pile already refuses: a resume
  only a human can supply is why `BLOCKED_RESUME_ON` excludes `user-message`, and an item parked on
  an owner-only repair is the same dead end one level up. That is why the whale performs the
  repair itself ([§5](#5-the-shape-any-repair-has-to-have)).

## 7. Open Questions

4. 💬 **OQ-CR4: Should the pile re-check a criterion's premise at judge time?**

   The second case is exactly this: true when written, its premise expired unread.
   [§7.1](#71-how-a-premise-expires-before-the-judge-reads-it) walks through how that happens and
   what each option would do.

   - **A — No re-check; supersession is the only repair.** Nothing changes for that class until
     someone notices, and the six-times pattern says nobody notices at judge time.
   - **B — A structural premise check at judge time**, reusing the criterion locators the packet
     already extracts. A locator that resolves is not a premise that holds, so only the
     deleted-artifact class is caught.
   - **C — Surface, never decide:** a premise that no longer resolves becomes a note beside the
     verdict and a board mark, and the item parks on a resume the pile can honor. One more state to
     explain.

   <!-- vantage: question id=OQ-CR4 leaning="Surface it, never decide it (C) — the judge must not move the bar, but the packet can say the premise moved, which is what nothing does today." -->

   _Leaning:_ Surface it, never decide it (C). The judge must not move the bar, but the packet can say
   the premise moved, which is exactly what nothing does today.

   **Answer:**
   > _(empty — fill in when decided)_

### 7.1 How a premise expires before the judge reads it

This has happened once, in the pile: the second case in [§3](#3-the-defect-measured), on
2026-10-21. No tillr record shows it happening in tillr yet, so the walk-through below is
anticipated. It uses an item that is queued today.

1. **An item is filed from a measurement.** On 2026-11-17 [the gate design](./gate-rework.md)
   filed `item-gate-fast-checks-fit-budgets`. Its criterion `c1` asks the run to report *why* the
   gate's documents check overruns its 2,000 ms budget on a change to the tillr roadmap, which was
   measured that day. When the drain syncs the block, that text freezes in the store.
2. **The tree moves first.** Before a lane takes the item, another landing removes the cause. For
   example, [entry 9 of the tillr roadmap](../tillr/roadmap.md#9-move-the-queues-items-off-this-page)
   moves about forty item blocks off that page, and the check comes in under budget. Nothing reads
   `c1` again, and no record links a criterion to the measurement it quotes.
3. **The run cannot do what `c1` says.** The lane is briefed with `c1` as frozen. It finds no
   overrun to explain. It either stops with a conflict report, which holds the item, or it reports
   that the overrun no longer reproduces, which `c1` does not accept.
4. **The judge applies the text.** Once the judge step exists (`item-jl-judge-before-gate`, in
   [the judge and lifecycle design](./tillr-judge-and-lifecycle.md), is unbuilt), it reads `c1` as
   written. It returns not-done, or `defective`, which closes the item. Today there is no judge, so
   the item sits on the worker's conflict until someone reads it.

What each option changes at steps 3 and 4:

- **A** changes nothing. The item stays held or closed until the whale or the owner notices. Then
  the whale supersedes `c1` ([§5](#5-the-shape-any-repair-has-to-have)).
- **B** checks at judge time that what `c1` names still exists: the documents check and the
  roadmap file. Both do, so B catches nothing here. It catches only a criterion that names a
  deleted file or check.
- **C** has the judge note, beside its verdict, that the premise `c1` quotes no longer holds. It
  does not decide the item. The item parks with a board mark on a resume the pile can honor, and the
  whale then supersedes `c1`. The judge never moves the bar.

**What you are asked:** whether the judge step should look for an expired premise and report it
(B or C), or whether noticing one is left to the whale and to you (A).

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-CR1 | A repair supersedes the whole criterion with a new frozen record that names the text it replaced and why. It never retracts a clause from the additive log and never edits the frozen text. Settled in [tillr today](./tillr-today.md), whose first task built tillr's refusal of an edit | 2026-11-16 | [§5](#5-the-shape-any-repair-has-to-have) | — |
| OQ-CR2 | The whale supersedes a criterion itself, with a required reason, and the replaced text goes in the judge's packet and on the board. Visibility, not a narrower permission, is the guard against the whale lowering a bar it wants retired, because an owner-only repair is the owner-only-resume shape the pile already forbids | 2026-11-17, owner | [§5](#5-the-shape-any-repair-has-to-have), item 2 | — |
| OQ-CR3 | The judge sees both texts, the reason, and which replaced which, and its prompt says *replaced*, not *added*. The prior text is the only thing that makes a weakening visible to the judge | 2026-11-17, owner | [§5](#5-the-shape-any-repair-has-to-have), item 4 | — |
| OQ-CR5 | An unsatisfiable criterion stays a judge finding (`defective`), with no new verdict value, and the repair is to reopen the item and then supersede the criterion. The owner's condition is that this sequence is legal and cheap. It is not yet: tillr's reopen exists, but the supersession record and the judge's `defective` close do not | 2026-11-17, owner | [§5](#5-the-shape-any-repair-has-to-have), the unsatisfiable-criterion paragraph | — |
| OQ-CR6 | A supersession refuses a verdict in flight: the snapshot-identity mismatch rejects it and the item is judged again against the current text, because a verdict names the world it judged | 2026-11-17, owner | [§5](#5-the-shape-any-repair-has-to-have), item 6 | — |

## 8. What this does not decide

- **The surface.** Who performs a supersession is settled: the whale
  ([§5](#5-the-shape-any-repair-has-to-have)). Whether it does so through a command, a store write,
  or both is not decided here.
- **The blocking defect.** A resume vocabulary with no read-grant kind is its own item; this
  doc only notes that an owner-gated repair would reproduce its shape.
- **Retirement and the pile's size.** [pile-roadmap-division.md](pile-roadmap-division.md) owns the
  retirement-first sequence, and a repair path must not become a reason to keep residue.
- **Whether any criterion should be re-verified at filing.** The expired-premise class would be caught
  earlier by measuring a quoted red before the item is filed; that is a filing rule, not a repair.
- **Any implementation.** No event name, field name, command name or migration is proposed here.

## 9. Where the code lives, by symbol

| Symbol | What a repair touches |
| :--- | :--- |
| `FrozenCriterion` | The record a supersession extends; `text`, `source`, `at` and `appended` |
| `CriterionAppend`, `CRITERION_APPEND_AUTHOR` | The additive log and its single recorded author (`whale`) |
| `settledCriterionText` | The one derivation every comparison and the deliverable read use |
| `criterionRewriteRefusal`, `settledCriterionRefusal` | The write-time guard, and the refusal text that today recommends a new item |
| `recordCriterionAppends`, `freezeLandedCriteria` | Where an allowed change is observed on the landed result |
| `ItemEvent`, `reduceItemEvent`, `readItemLedger` | The event union and the reducer that makes a record state |
| `defectiveItemIds`, `ItemVerdict` | The terminal `defective` finding and its retirement |
| `WORK_CONTROL_REOPEN_COMMAND` | The append-only, reason-required precedent a supersession should follow |
| `itemCriterionForJudge`, `renderItemEvaluationPrompt` | What the judge is shown, and the sentence that tells it how to combine texts |
| `canonicalApproverCriteriaSection`, `approverCriteriaRefFor`, `approverSnapshotIdFor`, `verifyApproverEvidencePacket` | Snapshot identity, and the refusal a moved criterion already triggers |
| `descriptionLocatorCandidates` | The locators a criterion already yields, which a premise check would read |
| `BLOCKED_RESUME_ON`, `resumeOnKind`, `BLOCKED_HORIZON_MS`, `blockedVerdictExpired` | The resume vocabulary the third case is about |

## 10. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Leave it; `defective` is the repair | Rejected. The first case is a correct decision that cannot reach the text, and `defective` answers a wrong bar by deleting the item |
| Append a retraction sentence under the current rules | Rejected as the mechanism. It is legal today and it produces a self-contradicting packet the judge is told to apply in full |
| File a replacement item and drop the old one | Today's only true supersession, and it stays legal — but as a standing mechanism it orphans the old id's verdicts, receipts and dispatches with no link |
| Hand-edit the frozen record | Rejected. It rewrites the freeze, and the snapshot identity would then disagree with the branch |
| Expire a criterion after N verdicts | Rejected. It makes the bar a function of effort rather than of the world, and a criterion with eight verdicts behind it would have retired a live item |
| Let the approver propose the replacement text | Rejected for now. The approver may not author criteria, and a proposal is authorship even when the whale applies it |
