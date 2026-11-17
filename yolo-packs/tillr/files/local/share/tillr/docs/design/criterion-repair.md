---
title: "Repairing a frozen criterion — the freeze protects the bar and traps a wrong one"
status: in-review
stage: DESIGN
next: "Rule [OQ-CR2](#OQ-CR2) (who may supersede) — it decides whether the record is a small append or carries an abuse surface, and every other question is priced against that answer"
board: none
board-none-reason: not-yet-ruled
depends-on:
  - whale-recovery.md#decision-ledger
tags: [pi, work-control, pile, criterion, judge, design, whale]
summary: "A frozen criterion can be appended to but never retracted, so a criterion that is wrong when written, or true and later stale, is permanent — and the only exit, a defective verdict, retires the item. Measured in three filings: one decided against its own criterion's text, one whose premise expired, and one blocked on a missing verdict kind. The repair is a supersession record in the append-only shape reopened already uses; six questions decide its shape and none is ruled."
vantage:
  status-chip: true
---

# Repairing a frozen criterion

> **Delivered copy.** This is `docs/design/criterion-repair.md` from this repository at commit `b3690673a0` (2026-11-16), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/criterion-repair.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** 2026-10-21. Nothing built, and this doc proposes no patch. Symbols read in
`work-control.ts` at `e7bdc147`; the pile
evidence is the live journal's newest snapshot and the items' own filed text, both read the same day.

**Needs your ruling:** [OQ-CR1](#7-open-questions), [OQ-CR2](#OQ-CR2), [OQ-CR3](#OQ-CR3), [OQ-CR4](#OQ-CR4),
[OQ-CR5](#OQ-CR5), [OQ-CR6](#OQ-CR6).

**Reads with:** [whale-recovery.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/whale-recovery.md) (`WR-D3`, the ruling that made criteria
additive and the reason they are additive only), [approver-agent.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/approver-agent.md) (the
approver's non-goals: it may not unfreeze a criterion), [pile-roadmap-division.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/pile-roadmap-division.md)
(the retirement-first sequence this repair must not pre-empt), [roadmap.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/pi/roadmap.md) (routing).

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

**Cost.** A supersession changes the criteria hash, so it invalidates any judge pass in flight
against the old text ([OQ-CR6](#OQ-CR6)); and the actor who most wants an item retired becomes the
actor who can move its bar, which is the collusion the freeze exists to prevent ([OQ-CR2](#OQ-CR2)).

**Start at** the open questions — the body earns them and nothing is decided before them.

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
([approver-agent.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/approver-agent.md)). What the design never added is the step after the owner
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

Whatever the rulings below, a repair record has to be five things, and the fourth is the one that
is easy to lose:

1. **Append-only.** Nothing rewrites a `FrozenCriterion` in place; the record grows, as `reopened`
   and the additive log already do.
2. **Attributable.** Who did it, when, and against which prior text.
3. **Reasoned, with the reason required.** `/whale-reopen` refuses an empty reason for exactly this
   reason; a supersession that can be written without one is a silent rewrite.
4. **Visible to the judge.** The actor that applies the bar is the actor best placed to notice a bar
   that was quietly lowered. If the packet carries both texts, the prompt must say *replaced*, not
   *added*: the append sentence already tells the judge to apply the original together with its
   additions, and the wrong word turns a supersession into a second requirement.
5. **One standing text per item.** However many supersessions an item accumulates, exactly one text
   is current, and every reader — judge, board, prod — derives the same one.

`reopened` is the precedent worth copying rather than inventing beside: an append-only record that
clears state, carries a required reason, is written by one command, and is read by the reducer
without a new store.

## 6. What must not change

P1 stands, and it is the test each option is run against in the questions below. Four plausible
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
  an owner-only repair is the same dead end one level up. Whatever the ruling on
  [OQ-CR2](#OQ-CR2), the whale has to be able to *reach* the repair — by performing it or by
  surfacing it into a state that clears without the owner.

## 7. Open Questions

1. ✅ **OQ-CR1: Does a repair retract a clause, or supersede the whole criterion?**

   The record shape decides the audit trail and every other answer's cost.

   - **A — Retract inside the additive log.** A `CriterionAppend` mode strikes a clause. Cheapest,
     reuses the reducer, but settled text becomes a computed subtraction and a strike chain reads
     worse than one text.
   - **B — Supersede with a new frozen record** naming the text it replaced and why. Keeps the audit
     trail and one text to apply; costs a new event kind, a second first-wins rule and a new
     snapshot identity.
   - **C — Append prose saying the earlier clause no longer binds.** No schema change; the packet
     then carries a contradiction the judge is told to apply in full.

   _Closed 2026-11-16:_ answered in [tillr today](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-today.md): a frozen criterion is repaired by a supersession record and never by an edit.

   _Leaning:_ Supersession (B). Retraction makes the settled text a computed subtraction, and the
   thing being repaired is precisely the text a reader has to be able to quote.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-CR2: Who may supersede a criterion, and what stops abuse?**

   The freeze exists so the item cannot rewrite its own bar; this decides whether the mechanism is
   compatible with [P1](#1-the-invariant-the-freeze-protects) at all.

   - **A — The owner only, through a command.** Strongest guarantee; it reproduces the
     owner-only-resume shape `BLOCKED_RESUME_ON` excludes, so every repair waits on a turn.
   - **B — The whale, with a required reason, shown to the judge and the board.** Self-repairing;
     the collusion guard weakens, and the record makes the move visible after the fact rather than
     impossible.
   - **C — Both, with different powers:** the whale hardens freely and supersedes a false premise only
     against a recorded ruling. Two rules to explain, and "false premise" is a judgement the record
     cannot check.

   <!-- vantage: question id=OQ-CR2 leaning="The whale, with a required reason and the replaced text in the packet (B) — an owner-only repair is the owner-only-resume shape the pile already forbids." -->

   _Leaning:_ The whale, with a required reason and the replaced text in the packet (B) — an
   owner-only repair is the owner-only-resume shape the pile already forbids.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-CR3: Does the judge see both texts and the reason, or only the current one?**

   This decides whether a weakening is catchable at judge time or only by a human reading the record.

   - **A — Both texts, the reason, and which replaced which.** Mirrors the append treatment; one more
     field per item, and the prompt must say *replaced*, not *added*, or the judge applies both.
   - **B — Only the current text.** Smallest packet and one bar to apply; a lowering is invisible to
     the actor applying it.
   - **C — Current text plus a note that it replaced an earlier one**, the prior text reachable
     through the judge's read tools. No guarantee it looks, so the packet proves nothing.

   <!-- vantage: question id=OQ-CR3 leaning="Both texts and the reason (A) — the defect being repaired is a criterion nobody could check, and the prior text is the only thing that makes a weakening visible to the judge." -->

   _Leaning:_ Both texts and the reason (A). The defect being repaired is a criterion nobody could
   check, and the prior text is the only thing that makes a weakening visible to the actor applying
   it.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-CR4: Should the pile re-check a criterion's premise at judge time?**

   The second case is exactly this: true when written, its premise expired unread.

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

5. 💬 **OQ-CR5: How is an unsatisfiable criterion told apart from "not yet done"?**

   This decides whether the pile prods or repairs; `defective` is today's only signal, and it
   retires.

   - **A — Keep it a judge finding; make reopen-then-supersede the repair.** No new verdict
     vocabulary, provided that sequence is ruled legal and cheap.
   - **B — A fifth verdict, `unsatisfiable`, which parks instead of retiring.** The id survives; it
     needs a new verdict value and a supersession resume kind, which the third case shows is missing for
     reads.
   - **C — No new state; the whale supersedes and the next verdict decides.** Cheapest, and the whale
     must notice.

   <!-- vantage: question id=OQ-CR5 leaning="Keep it a judge finding and make reopen-then-supersede the repair (A) — no new verdict vocabulary, provided that sequence is ruled legal and cheap." -->

   _Leaning:_ Keep it a judge finding and make reopen-then-supersede the repair (A), provided that
   sequence is ruled legal and cheap.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-CR6: Does a supersession invalidate a verdict already in flight?**

   A superseded criterion changes the criteria hash, so a running pass's fate is a decision, not a
   consequence.

   - **A — Yes; the mismatch refuses it.** `verifyApproverEvidencePacket` already recomputes from
     content, so the item re-judges. No new code, but a mid-pass supersession throws a judge pass
     away — 60 KB per attempt makes that real.
   - **B — No; the packet carries the chain and the verdict is recorded as superseded.** No work
     wasted, but a `done` can land against a stale text unless the board marks it.
   - **C — Serialize: no supersession while a pass holds the item.** No lost work, at the price
     of a per-item lease the pile lacks.

   <!-- vantage: question id=OQ-CR6 leaning="The mismatch refuses it (A) — a verdict is supposed to name the world it judged, and that is what the snapshot id already enforces." -->

   _Leaning:_ The mismatch refuses it (A). A verdict is supposed to name the world it judged, and that
   is what the snapshot id already enforces.

   **Answer:**
   > _(empty — fill in when decided)_

## 8. What this does not decide

- **The surface.** Whether a supersession is a `/whale-*` command, a pile write, or both is
  downstream of [OQ-CR2](#OQ-CR2): the ruling on *who* decides the surface, not the other way round.
- **The blocking defect.** A resume vocabulary with no read-grant kind is its own item; this
  doc only notes that an owner-gated repair would reproduce its shape.
- **Retirement and the pile's size.** [pile-roadmap-division.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/pile-roadmap-division.md) owns the
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
