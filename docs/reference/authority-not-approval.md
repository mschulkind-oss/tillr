---
title: "Authority, not approval — who decides, who judges, and what the owner is asked"
status: accepted
stage: CURRENT
verified: 2026-11-21
verified_commit: e167325c21f77151e9eeab22df542b3837e0f631
covers:
  - tools/tillr-item/item/verdict.go
  - tools/tillr-item/item/judge.go
  - tools/tillr-item/item/grant.go
  - tools/tillr-item/item/waiting.go
  - tools/tillr-item/item/decision.go
  - tools/tillr-drain/drain/blocked.go
tags: [tillr, authority, owner, judge, verdict, reference]
summary: "The rules for who may decide what: an operation is mechanical, delegated or the owner's by three ordered tests; the owner is asked a question with real options and never asked to run a command; and an agent that judges is never the agent whose work is judged. Each rule is listed with the code that enforces it today, or marked as a convention where nothing does."
---

# Authority, not approval

**Status:** Source-verified 2026-11-21 against `e167325c21f77151e9eeab22df542b3837e0f631`.
UNMEASURED: this reference was written by reading the code, not by observing a live drain.

This page states who may decide what in this repository's agent work: which operations the
system performs with nobody asked, which an agent performs and records, which wait for the
owner, and who may judge whose work. It also says, rule by rule, whether code refuses a
violation today or whether the rule is a convention agents follow.

| Component | Lives in |
| :--- | :--- |
| Claim classes, the actor-and-class refusal, the independence check, the evidence requirement | `tools/tillr-item/item` (`RecordVerdict`, `RecordJudge`, `checkIndependence`, `ClaimOwnerOnly`) |
| The waiter a blocked item must name | `tools/tillr-item/item` (`WaitingOn`, `checkWaitingOn`) |
| Who decided a recorded decision | `tools/tillr-item/item` (`RecordDecision`, `decided_by`) |
| Routing a worker's obstacle to the design seat and then to the owner | `tools/tillr-drain/drain` (`ObstacleOwner`, `DesignNextActorField`) |
| The operator's reporting rule | [`drain-timed-checkin`](../../.agents/skills/drain-timed-checkin/SKILL.md), *Reporting to the owner* |

**Reads with:** [the judge design](../design/tillr-judge-and-lifecycle.md#6-the-judge-is-a-step-in-the-flow)
(the owner's ruling that a judge is a step in the flow, and the changes that ruling still
owes the code), [item states](./tillr-item-states.md) (what `blocked` stores), and
[landing principle 13](../design/landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)
(a held state names a mover).

---

## Terms

- **The owner** is the one human who owns this repository and its policy.
- **The operator** is the agent session that coordinates the queue: it files items, watches
  the drain and reports to the owner. The store's `decided_by` field spells this actor
  `whale`, the older name for the same role.
- **Approval-monkey work** is asking the owner to execute or bless an action an agent has
  already decided, where the owner contributes keystrokes and no information.
- **Owner-only** is the reservation of a decision to the owner because that decision needs
  information, values or accountability only the owner holds. It is not a label for
  operations whose names sound destructive.
- A **mechanically decidable** operation is one whose outcome is fixed by settled policy
  applied to checkable, recorded facts.
- A **delegated action** is one an agent performs under settled policy and records with an
  evidence packet.
- An **evidence packet** is the record stored with a delegated or mechanical action so that
  a later reader can re-judge it without trusting the actor. It names the actor, the
  operation and its target, the policy or predicate invoked, the primary facts relied on as
  pointers to records (hashes, commit identifiers, record identifiers) and not as a narrative
  summary, the expected effect, and the reversal path or a statement that none exists.
- A **verdict** is a recorded judgment that one acceptance criterion of an item is met or
  not met. A **claim class** is the stored kind of a criterion, which decides which kind of
  actor may record a verdict on it.

## What owner-only is for

A decision is worth the owner's attention only where the owner adds something the system
cannot have: a preference between legitimate outcomes, risk appetite, outside context, or
personal accountability for an act done in the owner's name. Four classes qualify.

| Class | Why only the owner can decide | Examples |
| :--- | :--- | :--- |
| Irreversible loss | The lost thing exists nowhere else, and accepting the loss is a value judgment | Discarding unpublished work that is not in the trunk and not otherwise preserved; destroying unique history, credentials or state with no backup |
| Spending and commitment | Money and obligation are the owner's | Purchases, paid resources beyond an authorized amount, contracts, commitments to third parties |
| External publication | The act speaks as the owner to the world and cannot be unsent | Releasing, pushing to a public remote, posting, mailing, disclosing private material |
| Policy and risk acceptance | The rules themselves, and knowingly accepting a defect or risk, are the owner's to set | Changing seat permissions or safety boundaries, waiving a failed gate, choosing between two defensible product directions |

Nothing else is owner-only because it sounds alarming. Irreversibility is assessed against
actual recoverability (a retained git object, a landed equivalent, a kept record) and never
against the verb in a command's name. Closing a record whose content is already in the trunk
destroys nothing and is bookkeeping. A blandly named command that deletes the only copy of
unpublished work is owner-only whatever it is called.

## Placing an operation

Every operation is placed by three questions, asked in order. The first that decides,
decides.

| # | Test | Placement |
| :--- | :--- | :--- |
| 1 | Is the outcome determined by settled policy plus checkable recorded facts, with both outcomes defined? | **Mechanical.** The system acts and records. Nobody is asked. |
| 2 | Is it reversible or recoverable, bounded in what it can damage, within existing policy, and recordable? | **Delegated.** The agent acts, with an evidence packet written before or atomically with the action. The owner can audit and reverse afterwards. |
| 3 | Is it irreversible, external, spending, policy, a value choice, or founded on ambiguous or conflicting evidence? | **Owner.** The agent asks a question in the form given under [What the owner is asked](#what-the-owner-is-asked). |

Ambiguity moves a decision exactly one step toward the owner and never the other way: a
mechanical predicate that cannot decide cleanly becomes a delegated judgment if the action is
reversible, and an owner question if it is not.

An operation is mechanically decidable only when all of these hold: its predicate is
deterministic over recorded state; settled policy defines the action for every outcome of the
predicate; a false positive is impossible by construction, detectable, or reversible; and a
reader who was not present can reproduce the evidence from stored identifiers. Recognizing a
new mechanical class is a policy decision, made once, and is a legitimate question for the
owner. Once a class is recognized, its individual instances are never asked about again.

An action whose evidence packet can only be written afterwards was not ready to perform.

## What the owner is asked

**"Please run X" is a forbidden form of governance.** If a request to the owner contains
exactly one sensible option, or the owner's only possible contribution is running a named
command, the request is malformed. It is converted: down to a mechanical rule or a delegated
action, or up to a one-time policy question that settles the class. Where a tool's seat
restriction blocks an agent from an action this page delegates, the remedy is to fix the
restriction in the tool. Routing the command through the owner's hands to get around it is
the approval-monkey work this page exists to prevent.

An owner request is a question, never a command. It states:

- the question in one sentence, naming the choice;
- at least two real options, including doing nothing where that is real, with the
  consequences and reversibility of each;
- the evidence, summarized with pointers to the primary records;
- the agent's recommendation, and what would change it.

One request carries one decision. The owner answers with a choice and the system carries it
out: the owner is never asked to type a command the system could run, and a record must not
present the person who typed as the one who decided, or the reverse.

A request that cannot meet this bar (one option, no information the owner holds, evidence
already conclusive) does not go to the owner in a weaker form. Asking anyway "to be safe"
spends the owner's attention and trains the reflexive approval that makes the owner's answer
worthless when a real decision arrives.

While an owner question is open, the item it holds is stored as `blocked` with the owner
named as the one who must act and the reason recorded, so the wait is visible and names who
ends it.

## A judge never judges its own work

An agent may record a verdict. It may not record one on its own work: the judging actor is
never the actor whose work the criterion judges. A sign-off anyone can grant themselves is a
status field, not a check.

- A verdict recorded by an agent names both the actor who judged and the actor whose work
  was judged. The store compares the run and session identities behind the two names and
  refuses the verdict as `self-judging` when they are the same or share a run or a session.
  Two different sessions on the same model route are independent; sharing a route is not
  sharing a session.
- A **mechanical** verdict is exempt, because its evidence and not its actor is what carries
  it. A **human** verdict is exempt, because the rule is about delegation.
- Every verdict records its actor kind (`mechanical`, `agent` or `human`) and the evidence it
  read, as pointers to records. A verdict that names no evidence is refused as
  `verdict-evidence`. That record is what makes a wrong verdict reviewable afterwards.
- An agent cannot record a verdict on an `owner-only` criterion, and a mechanical actor can
  record one only on a `verification-only` criterion. Both are refused as `claim-class-actor`.

> [!WARNING]
> **The code still requires a grant, and this page does not describe it as a rule.** At the
> verified commit an agent verdict is also refused unless a stored *grant* (a row naming the
> classes an agent may judge, with an expiry time) covers it. The owner ruled on 2026-11-20
> that the judge is a step in the workflow and that nothing is granted or expires; the removal
> is specified in [the judge design](../design/tillr-judge-and-lifecycle.md#6-the-judge-is-a-step-in-the-flow)
> and is not built yet. Do not extend the grant, and do not cite the independence rule as
> support for it.

## What enforces each rule today

A rule with "nothing" in the second column is a convention: agents are told to follow it and
no program refuses a violation.

| Rule | Enforced by |
| :--- | :--- |
| An agent never judges its own work | `checkIndependence` in `tools/tillr-item/item`, called by both verdict writers, `RecordVerdict` and `RecordJudge`; rejection rule `self-judging` |
| A verdict records actor kind and evidence | `RecordVerdict` in `tools/tillr-item/item`; rejection rule `verdict-evidence`, and a usage refusal for an agent verdict with no `actor` or no `judged_actor` |
| An agent cannot judge an owner-only criterion; a mechanical actor judges verification-only criteria alone | `RecordVerdict` and `RecordJudge`; rejection rule `claim-class-actor` |
| An item waiting on the owner says so | `checkWaitingOn` in `tools/tillr-item/item`: a blocked item must name a waiter (`owner`, `design`, `plan` or another stored item) and a reason; rejection rules `blocked-waiter-missing` and `blocked-waiter-invalid` |
| A worker cannot settle an owner decision by guessing | The worker brief's `owner` obstacle, which requires a `QUESTION:` line, and `tools/tillr-drain/drain`, which sends a worker's obstacle to the design seat first and to the owner only on that seat's `NEXT-ACTOR: owner` |
| A recorded decision says who decided it | `RecordDecision` in `tools/tillr-item/item`: `decided_by` is `owner` or `whale` |
| The four owner-only classes, and the three placement tests | Nothing. The `owner-only` claim class is the only stored form, and it is set per criterion by whoever writes the item |
| "Please run X" is forbidden; an owner request carries real options, evidence and a recommendation | Nothing in code. [`drain-timed-checkin`](../../.agents/skills/drain-timed-checkin/SKILL.md) instructs the operator, and the worker brief asks for the question and its options |
| A delegated action carries an evidence packet written before the action | Nothing beyond the verdict's own evidence requirement above. No program checks a packet for other delegated actions |
| The decider and the executor are recorded separately | Nothing. `decided_by` records the decider; no field records a separate executor |

## Refusals

These hold whatever tool is in use.

- No rule reduces a recorded decision to a bare approval, or to the owner executing a command
  an agent chose. That transfers liability and adds no oversight.
- No agent performs an irreversible action silently, or with its evidence packet written
  after the fact.
- Owner-only is not widened by verb or by blanket category ("all deletions") without looking
  at whether the specific operation can be recovered.
- A help-text label is not a security boundary. A restriction that matters is enforced by the
  tool against the recorded actor, and an unenforced label is either enforced or reworded as
  the delegation it is.
- An owner question is not used to launder an agent's uncertainty: undecided or conflicting
  evidence is stated as such in the request, never compressed into a yes or no.
- The owner's reservation is not removed. The four classes are real, and a system in which
  nothing reaches the owner has removed accountability along with the approval-monkey work.

## Current values

Verified at `e167325c21f77151e9eeab22df542b3837e0f631`. The prose above explains what each
of these is for.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Claim classes | `verification-only`, `red-gate-triage`, `direction`, `owner-only` | `tools/tillr-item/item` (`claimClasses`) |
| Actor kinds | `mechanical`, `agent`, `human` | `tools/tillr-item/item` (`actorKinds`) |
| Waiters a blocked item may name | `owner`, `design`, `plan`, `item:<id>` | `tools/tillr-item/item` (`checkWaitingOn`) |
| Deciders a decision may name | `owner`, `whale` | `tools/tillr-item/item` (`decidedBys`) |
