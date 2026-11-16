---
title: "The Escalation Ladder — conflict routing for lanes"
date: 2026-11-03
status: proposed
stage: DESIGN
next: "A companion implementation plan settles per-file build detail; open questions are settled by the user in the Open Questions section"
tags: [design, board, escalation, conflict-routing, policy, agents]
summary: "Board item 250: a lane that finds reality disagreeing with its plan or design records the conflict and routes it to the plan owner or the design seat, and never resolves it by inventing. This document settles the conflict record, the fence around design and policy artifacts, the cheap plan-owner seat distinct from the design seat, and routing as a recorded transition whose evidence is the conflict itself. Date is an assumption taken from the repository's latest verification stamp."
---

# The Escalation Ladder

## 1. Purpose and the frozen criterion

This document answers board item 250. Its frozen criterion is:

> A lane that finds reality disagreeing with its plan or design records the conflict and routes it to the plan owner or design seat; no lane changes a design or policy to make its change apply; the plan owner is a cheap seat distinct from the design seat; and the routing is a recorded transition with the conflict as its evidence.

It is a design document: it settles what to build and why, including the questions only the user can settle. Per-file build detail belongs to a companion implementation plan, which this lane does not write.

Conflict routing is one rung-to-rung motion on an escalation ladder. The ladder generalises the same rule upward: at every rung, the forbidden move is resolving a higher-kind question by a lower-kind action. The ladder already has a downstream consumer: [landing principle 6](landing-principles.md#6-a-budget-escalates-it-does-not-loop) sends an exhausted repair budget up "the escalation ladder — the repair lane, then a contingency, then a recorded exception with an expiry" and cites this document's four rungs and its `exceptions` ledger. This design must therefore keep the four rungs and the expiry-bearing exception, and add what item 250 demands: the conflict record, the fence, the cheap distinct plan owner, and the recorded transition.

### 1.1 The motivating case

A fork patch series is maintained against upstream, and established policy — implemented in both the launcher and the gate — is that the reference is the **newest release tag**. A brief told a lane to "stay evergreen, base on upstream's latest." The lane rebased the series onto the branch head. The gate then failed because the series did not merge into the release tag. The coordinator's response was to declare `follow: "head"`, changing the policy to match the mistake so the gate asked about the branch head too. The owner caught and reversed this.

That case contains two distinct failures, and this design must prevent both:

1. **The brief conflicted with an established policy and nobody named the conflict.** The correct response to "base on latest" was that it contradicts the recorded policy to follow the newest release; the work was to rebase onto the release, not to change the rule.
2. **An implementer-level seat resolved a policy conflict by inventing a mechanism.** The rule the owner then stated — if there is a conflict with reality in the implementation plan, the implementer is not the one to fix it — is the conflict-routing rule settled in [section 7](#7-the-conflict-routing-rule).

### 1.2 What already exists, and why it is insufficient

This document does not propose machinery beside machinery that already does the job. What was found in the tree and in the brief for this lane:

| Existing thing | What it already does | Why it is insufficient for item 250 |
| --- | --- | --- |
| The prior version of this document (board item 249) | Settles the four rungs, escalation as a state-machine overlay, classification tests, a proposed policy ledger, checks, a casebook, and refusals | It is a design, not yet a mechanism; it does not state the fence as an editable-artifact list, does not enforce plan-owner distinctness as a dispatch rule, and does not map itself clause-by-clause to item 250's criterion. This revision keeps its settled decisions and adds those three things |
| [Landing principles](landing-principles.md#6-a-budget-escalates-it-does-not-loop) | Principle 6 relies on this ladder for budget exhaustion and on an `exceptions` ledger with expiry; the library's discipline is that a principle with nothing in its enforcement column is a claim, not a rule | It governs landing budgets, not plan-versus-reality conflicts inside a running lane |
| [`AGENTS.md`](../../AGENTS.md#landing-a-lane-shared-forks-verified-candidates) | Describes tracks, lane workflow, checkpoints, one writer per worktree, serial landings, and the landing gate; the lane rules live in the `lane-work` skill (per the brief for this lane) | It describes how a lane runs and lands, not what a lane must do when its plan is wrong |
| [Evidence lands in the repository](../pi/operating-lessons-2026-10-17.md#5-evidence-lands-in-the-repository) | Durable scratch is not citable evidence; a lane transcript or scratch artifact expires and cannot be checked later | It settles where evidence may not live, not the record a conflict becomes |
| Board items with frozen criteria, and verdicts recorded with an actor kind (per the brief for this lane) | An item's criterion is frozen against the lane executing it; an independent judge already refuses a lane signing off its own work | A verdict judges finished work. Nothing yet records a mid-flight conflict, routes it to an owner, and holds the lane's contested work while it waits |

**Assumption, not asserted fact:** whether the board's current schema can store a first-class conflict record linked to an item, and whether a distinct plan-owner agent definition already exists, were not verified for this revision; a scout dispatch for exactly that question returned no usable result. This design is therefore written to survive either answer: if the board cannot store the record today, the record is a new table or row kind (an implementation-plan question), and if no plan-owner seat exists today, creating one is part of the build ([OQ-5](#open-questions)). The date in the frontmatter is likewise an assumption, taken from the `verified` stamp in [landing principles](landing-principles.md).

### 1.3 The criterion, clause by clause

| Frozen clause | Settled in | How it is checkable |
| --- | --- | --- |
| "records the conflict and routes it to the plan owner or design seat" | [4.1](#41-the-conflict-record), [4.2](#42-routing-how-the-record-reaches-the-owner) | A record with required fields exists on the board, linked to the item, addressed to a named rung; a raise missing a field is refused at write time |
| "no lane changes a design or policy to make its change apply" | [5.2](#52-the-fence-what-a-lane-may-not-edit), [7](#7-the-conflict-routing-rule) | A diff touching a fenced artifact from a lane that does not own it is flagged mechanically; "inventing" has an operational definition a judge can apply |
| "the plan owner is a cheap seat distinct from the design seat" | [3.2](#32-the-plan-owner-a-cheap-seat-distinct-from-the-design-seat) | Distinctness is a dispatch rule keyed on seat identity and model class, refused by the dispatcher, not a prose hope |
| "the routing is a recorded transition with the conflict as its evidence" | [4](#4-routing-as-a-recorded-transition), [4.3](#43-answers-amend-versus-proceed-as-is) | Every state change of a conflict appends a transition citing the conflict record; every answer cites the record it answers |

## 2. Terms

| Term | Meaning |
| --- | --- |
| **Item** | One unit of work on the board, with frozen criteria and dependency edges. |
| **Lane** | One independent piece of work with its own worktree, run for a lead, a whale, or a board. |
| **Rung** | One level of judgment on the ladder in [section 3](#3-the-ladder). A rung is a seat with defined authority, not a specific person or model. |
| **Implementer** | The rung that works in a lane and produces the diff. |
| **Plan owner** | The cheap rung that authors and owns implementation plans: decomposition of an item into steps a lane can execute. Explicitly not the designer ([3.2](#32-the-plan-owner-a-cheap-seat-distinct-from-the-design-seat)). |
| **Designer** | The expensive rung (the design seat) that decides questions of design and digests questions of policy for the human. |
| **Human** | The owner. The only rung that decides policy. |
| **Conflict** | A disagreement, discovered by a lane, between its brief, plan, or design on one side and reality — code behaviour, gate output, or a recorded policy — on the other. |
| **Conflict record** | The recorded, board-held form of a conflict ([4.1](#41-the-conflict-record)). The record, not a transcript, is what gets routed and answered. |
| **Routing** | The recorded transition that carries a conflict record to the lowest rung with authority to answer it ([section 4](#4-routing-as-a-recorded-transition)). |
| **Policy** | A human-decided rule about what counts as correct that binds work beyond the item in which it is raised. |
| **Design** | A choice of structure or mechanism made within policy, which shapes how multiple items or components fit together. |
| **Plan** | The mapping of one item onto steps, files, and order, within its design and criteria. |
| **Implementation** | A choice inside one lane's diff that changes neither the item's criteria nor any externally visible contract. |
| **Taint** | Work already done under an assumption that a conflict has put in doubt. Tainted work is never silently resumed over and never silently reverted ([4.4](#44-the-lanes-already-made-change)). |
| **Policy ledger** | The proposed recorded, machine-readable form of policies ([6.1](#61-the-policy-ledger)). |
| **Canonical parameter** | The one named place where a parameterised policy's value lives ([6.1](#61-the-policy-ledger)). |
| **Casebook** | The proposed record of decided conflicts and owner corrections that the designer reads ([9.1](#91-the-casebook)). |

## 3. The ladder

The ladder has four rungs. Authority is defined by the *kind* of question (classified in [5.1](#51-the-classification-tests)), not by seniority in the abstract: a rung may decide any question of its own kind alone, must route any question of a higher kind, and may always **apply** a recorded decision of a higher kind without routing. Applying recorded policy is not deciding policy. In the motivating case, every rung already had authority to say "the recorded policy is newest-release, so rebase onto the release"; nobody below the human had authority to change it.

Routing sits between working and stopping. A lane that routes a conflict has not stopped the system: it has stopped only the contested work, recorded why, and handed the decision to its owner, while the drain dispatches other items and any separable work continues ([4.2](#42-routing-how-the-record-reaches-the-owner)). The ladder's rungs, from lowest to highest, are: decide it yourself (implementation), route to the plan owner (plan), route to the designer (design), route through the designer to the human (policy). Stopping work entirely — abandoning the item — is not a rung a lane chooses; it is one typed answer an owner may give ([4.3](#43-answers-amend-versus-proceed-as-is)).

### 3.1 Authority by rung

| Rung | May decide alone | Must route | May never do |
| --- | --- | --- | --- |
| **Implementer** (lane) | Implementation choices inside the plan that change no criterion and no externally visible behaviour; which of several plan-sanctioned options to take; recording an assumption and proceeding where the plan explicitly allows discretion | Any conflict between the plan or brief and reality; any ambiguity where the options differ in observable behaviour; any need to touch criteria, the gate, or a canonical parameter | Invent a mechanism to resolve a conflict ([section 7](#7-the-conflict-routing-rule)); edit a fenced artifact ([5.2](#52-the-fence-what-a-lane-may-not-edit)) to make its own work pass; sign off its own work |
| **Plan owner** (cheap seat) | Plan decomposition, sequencing, and which files or functions a lane changes; re-planning an item within unchanged criteria and design; applying recorded policy and design to a brief, including rejecting a brief phrase that contradicts them | Anything requiring a criteria change; a design ambiguity (two structures both satisfy the criteria); a policy silence or conflict the ledger does not already answer | Change criteria to fit a plan; treat a policy or design question as a plan question to keep work moving; answer a conflict it raised itself |
| **Designer** (expensive seat) | Design questions within recorded policy; interpretation of design precedent; reclassifying and rerouting conflicts up **or down**; drafting proposed policy wording for the human; amending a design document as a real design action ([4.3](#43-answers-amend-versus-proceed-as-is)) | Creating, changing, or retiring policy; interpreting an ambiguous policy where the readings change item criteria; any design whose blast radius crosses a boundary the human has reserved ([OQ-4](#open-questions)) | Originate policy on its own authority; silently absorb a plan-level surprise without rerouting it down (that burns the expensive seat and starves the cheap one of signal) |
| **Human** (owner) | Policy: create, change, retire; one-off exceptions, recorded with an expiry; expanding or contracting any rung's authority | Nothing above; the human is terminal. The human's obligation is the dual of routing: record the decision as a rule (or an explicit one-off), not as chat | Decide off-board. A policy decision that never reaches the ledger does not exist for the ladder |

Two properties deserve emphasis. First, **rerouting down** is a first-class act, because the expensive failure mode is a high rung doing a low rung's work at a high rung's price. Second, authority to decide is paired at every rung with the duty to leave the decision in a recorded form the next rung down can apply: a design decision the plan owner cannot read back is a future conflict bought at designer prices.

### 3.2 The plan owner: a cheap seat, distinct from the design seat

The criterion's third clause is an enforcement requirement, not a description. "Cheap" and "distinct" are settled as follows.

- **Who it is.** The plan owner is the seat that owns the implementation plan for an item: it decomposes the item, sequences the steps, and is the first owner of any conflict between that plan and reality. Most conflicts a lane meets are plan-kind — a function moved, a file differs from the plan, an approach does not survive contact with the code — and answering them needs the plan and the code, not design judgment.
- **Why it is cheap.** Plan-kind answers are high-volume and low-reach ([5.1](#51-the-classification-tests)). In this environment the design seat runs on an expensive model (Muse Wonder) while research and cheap mechanical seats run on a DeepSeek floor model (per the brief for this lane); routing every plan surprise to the design seat would pay design prices for plan work and teach lanes to misroute upward. The plan owner therefore runs on the cheap floor model class.
- **How distinctness is enforced, not merely stated.** Distinctness is a dispatch rule with three mechanically checkable parts:
  1. The plan owner is a named seat (an agent definition or dispatch role) separate from the designer seat, with its own model pin in the cheap class.
  2. The dispatcher refuses to fill a plan-owner transition with the designer seat, and refuses to fill a design transition with the plan-owner seat acting alone — a misaddressed packet is rerouted ([8.2](#82-when-the-wrong-seat-gets-it)), never silently answered by whoever received it.
  3. No seat may answer a conflict it raised, and the seat that authored a brief may not be the sole triage of a conflict against that brief's own ambiguity.

  Which existing seat fills the rung is [OQ-5](#open-questions); the three refusal rules are settled regardless of that answer.

### 3.3 What a routing carries by rung

A routing is an evidence packet, not a question in prose. Each rung adds its layer before the packet moves; a rung that cannot add its layer has not exhausted itself and may not pass the packet up.

| Rung raising or forwarding | Evidence it must attach before the packet moves |
| --- | --- |
| Implementer | The observed conflict: the brief or plan passage quoted, the contradicting reality (gate output summary, code behaviour, recorded policy ID), what work is blocked, and what work already done is tainted |
| Plan owner | The classification per [5.1](#51-the-classification-tests); the policy lookup result (ledger IDs checked, and what each said, or that none applied); costed plan options under each possible answer; a recommendation |
| Designer | Options analysis at the design level; precedent from the casebook ([9.1](#91-the-casebook)); if a policy change is proposed, the exact new policy wording and its ledger form; a recommendation |
| Human (deciding) | The verdict plus the distilled rule for the ledger or casebook, or an explicit one-off exception with expiry |

Recommendations are non-binding — they exist so the higher rung starts from the lower rung's thinking instead of from zero, which is where most of the token saving lives — and a higher rung that rejects a recommendation records why, because that rejection is casebook material.

## 4. Routing as a recorded transition

Needing a decision is not an item state; it is an **overlay** any item state can enter and leave. The item keeps its primary state (whatever the board calls ready, running, gated, and so on — this design is deliberately state-name-agnostic about the board's existing states); opening a conflict freezes a snapshot of that state into the record and makes the item not-ready for the drain until the conflict closes, unless triage marks it non-blocking under [4.2](#42-routing-how-the-record-reaches-the-owner).

The conflict itself has a small lifecycle, the same from every origin state. **Every entry into a state is a recorded transition, and the conflict record is its evidence**: a transition that does not cite a conflict record does not exist, and an answer that does not cite the record it answers is not an answer.

| Conflict state | Entered when | Left when | Record written on entry |
| --- | --- | --- | --- |
| `raised` | A rung records a conflict it cannot decide | A rung with routing authority classifies it | The raising rung's evidence ([4.1](#41-the-conflict-record)) |
| `triaged` | Classification per [5.1](#51-the-classification-tests) assigns kind and target rung | The target rung accepts, or reroutes | Kind, target rung, policy IDs implicated, blocking or non-blocking |
| `awaiting` | The target rung accepts the packet | That rung answers, reroutes, or a timeout reroutes upward ([8.2](#82-when-the-wrong-seat-gets-it)) | Accepting rung and time; the item is visibly blocked-on-decision, with rung and age |
| `answered` | The deciding rung records an answer | The owning lower rung applies the answer ([4.3](#43-answers-amend-versus-proceed-as-is), [4.4](#44-the-lanes-already-made-change)) | Answer kind, answering seat, the decision, taint assessment |
| `closed` | The answer has been applied and the item re-enters its flow | Terminal | Resume target or rework disposition; casebook entry if the decision sets precedent |

Rerouting is a transition `awaiting → triaged` that re-enters triage with the accumulated packet; it is not a new conflict and never discards evidence. Withdrawal (`raised → closed` by the raising rung, recording that the record answered the question after all) is permitted and counted, because it is the visible signature of an avoidable routing ([section 10](#10-refusals), R3).

### 4.1 The conflict record

**What it is.** One board-held record per conflict, linked to the item. The board is its home because the conflict is a fact about the item, and a lane transcript dies with the lane; [evidence that must survive belongs in the repository's durable surfaces](../pi/operating-lessons-2026-10-17.md#5-evidence-lands-in-the-repository), not in scratch or session history. Required fields are enforced by the board, not by convention: a raise that cannot fill them is refused at write time, which is the mechanical core of the refusal against routing-as-avoidance (R3).

**Lifetime.** A record is created at `raised`, is append-only through its transitions (a transition adds; it never rewrites an earlier field), and is retained after `closed` as the item's evidence and as casebook raw material. A closed record is never deleted or edited down to a summary; correcting one is a new appended note citing it.

| Field | Written by | Content |
| --- | --- | --- |
| `item` and `origin_state` | Board, automatically | The item and a snapshot of its primary state at raise time; this is the state the flow returns to on resume |
| `raised_by` | Board, automatically | The raising rung and seat |
| `question` | Raising rung | One decision, stated so that answering it does not require reading a transcript; evidence is linked, not pasted |
| `conflict` | Raising rung | The two sides quoted: brief, plan, or design passage on one side; recorded policy, gate output, or observed code behaviour on the other |
| `kind_claimed` | Raising rung | `implementation`, `plan`, `design`, or `policy`, per [5.1](#51-the-classification-tests); triage may overturn it |
| `policies_checked` | Raising rung, extended at triage | Ledger IDs consulted and their bearing, or an explicit "none apply" — the board refuses a blank |
| `blocked_work` and `tainted_work` | Raising rung | What cannot proceed, and what already-done work rests on the contested assumption |
| `blocking` | Triage | Whether the item waits entirely or separable work may continue |
| `recommendation` | Each forwarding rung | The rung's proposed answer and reasoning; non-binding |
| `answer` | Deciding rung | The answer kind per [4.3](#43-answers-amend-versus-proceed-as-is), the answering seat, the decision, and the rule to record in the ledger or casebook if any |

**What triggers routing.** A lane must raise a record when any of these fires: the plan or brief contradicts observed code, gate output, or a recorded policy; the only ways forward differ in observable behaviour or in what a criterion would judge; or proceeding would require editing a fenced artifact ([5.2](#52-the-fence-what-a-lane-may-not-edit)). A lane *may* attach a recommendation. What it may not do is keep the conflict in its own context and pick a side — an unrecorded conflict resolved in-lane is indistinguishable, afterwards, from inventing.

### 4.2 Routing: how the record reaches the owner

A record reaches its owner because the board is the notification and dispatch surface, not because anyone reads a transcript. On `triaged`, the record names a target rung; the board surfaces the item as blocked-on-decision with the awaiting rung and the packet's age, and the drain (or the coordinator acting on the board's surface) dispatches that rung's seat with the record as its brief. Nothing pings the human at raise time unless triage routes the packet to the human: packets below the human rung are the ladder working as intended, not noise.

While a blocking conflict is open, the item is not ready: the drain dispatches other work, and dependants stay not-ready through their existing dependency edges. **Routing is therefore not stopping work** — it stops one item's contested work, visibly, while the system works on everything else. That is the difference between routing and a stall: a stall is invisible and unbounded; a routed conflict names its owner, its age, and exactly which work waits on it.

A non-blocking routing is the narrow exception: triage may mark a conflict non-blocking only where the contested assumption is separable — no tainted work exists and no blocked work shares files, criteria, or a base with it. The motivating case would never qualify: a contested base ref taints everything built on it. When in doubt, blocking is the default, because continuing over a contested assumption manufactures taint.

### 4.3 Answers: amend versus proceed-as-is

An answer does not simply unfreeze the item. The deciding rung names one answer kind, and the kind — together with the taint assessment — determines where the item flows back to. The kinds fall into the criterion's two outcomes:

- **Proceed as-is** — the plan or design was right all along, and the lane is told so with the record as the reason: `apply-recorded`, `clarify`.
- **Amend** — a rung with authority changes the artifact that was wrong, as a real, recorded action by its owner, never as a lane edit: `amend-plan`, `amend-design`, `amend-policy`.

| Answer kind | Outcome | Meaning | Item flows back to | Worktree |
| --- | --- | --- | --- | --- |
| `apply-recorded` | Proceed as-is | A recorded policy or design already answered this; apply it | Origin state (resume) if no taint; else rework as below | Retained if untainted |
| `clarify` | Proceed as-is | The brief or plan is clarified within unchanged criteria and design | Origin state (resume) | Retained if untainted |
| `amend-plan` | Amend | The plan owner changes the plan; criteria and design do not change | Planning: plan version bumps, a lane executes the amended plan | Discarded where tainted; clean portions carried only by recorded plan-owner decision |
| `amend-design` | Amend | The designer changes the design under unchanged policy | Planning under the new design version; criteria re-read against it | Discarded by default; carry-over is the exception and is recorded |
| `amend-policy` | Amend | The human changed policy; criteria for this and future items change | Item creation review: criteria are rewritten against the new policy before any lane runs; other open items citing the old policy are flagged by ledger ID | Discarded |
| `abandon` | Neither | The item should not be done | Terminal, with the verdict recorded | Discarded |

An `amend-design` or `amend-policy` answer is a design or policy action in its own right: it is performed by the owning seat, versioned like any other change to that artifact, and the conflict record is cited as its evidence and reason. It is never performed by the lane that raised the conflict, in the same diff as the contested work — that conflation is exactly the `follow: "head"` move. An answered conflict never bypasses the judge or the gate on the way back: the answer decides the question, and the ordinary verdict machinery still rules on whether the resulting work satisfies the criteria. As a design decision extending the no-self-sign-off rule, the rung that answered a contested question is not the sole judge of the criterion it decided; mechanical verdicts remain exempt, as today, because the evidence carries them.

### 4.4 The lane's already-made change

A lane usually discovers a conflict mid-flight, with a diff already in its worktree. Neither of the two easy dispositions is allowed: the lane may not land or keep building on the contested part (that is resolving by inventing), and it may not silently revert or discard its work (that destroys evidence, loses possibly-good untainted work, and makes the conflict uninspectable). The settled disposition is **freeze and retain**:

1. The lane stops the contested work, leaves the worktree exactly as it stands, and records `blocked_work` and `tainted_work` against the actual diff.
2. The worktree is retained while the conflict is open. Nothing from it lands, and no other lane writes in it (one writer per worktree already holds).
3. The answer disposes of it, per [4.3](#43-answers-amend-versus-proceed-as-is): untainted work under a proceed-as-is answer resumes in place; tainted portions under any answer are discarded by recorded owner decision; under an amend answer the default is discard, with carry-over of demonstrably clean portions only where the owner records that decision and its basis.

Discarding is therefore an owner's recorded act with the conflict as its evidence, never a lane's quiet cleanup. A reader can always answer, afterwards: what had the lane done, what was contested, who decided its fate, and why.

## 5. Policy, design, plan, and implementation

### 5.1 The classification tests

Everything in this design stands or falls on a rung's ability to tell a policy question ("which ref do we follow") from a design question ("how do the two consumers share one resolver") from an implementation question ("which function changes"). The core intuition is **reach**: how much future work does the answer bind? A policy answer binds unrelated future items. A design answer binds future items that touch the same subsystem. A plan answer binds this item's execution. An implementation answer binds nothing beyond this diff. Reach is what the motivating case got wrong twice over: "which ref do we follow" binds every future rebase of every series, so it is policy, and it was treated first as a brief-writing matter and then as a configuration tweak.

Triage applies these tests in order; the first that fires classifies the question. The order is deliberate — the most mechanical test runs first, and judgment is spent only where the record does not already decide.

| # | Test | If yes | How mechanical |
| --- | --- | --- | --- |
| T1 | Does any answer contradict, or require changing, an entry in the policy ledger — including changing the value at a canonical parameter? | `policy` | Mechanical where the policy is parameterised: value comparison. Agent-classified where the policy is still prose |
| T2 | Does the answer change this item's criteria, or what the gate asks, or any externally visible contract? | `policy` if the change would also bind other items; otherwise `design` | Mechanical in part: criteria and gate definitions are artifacts, so "does this diff touch them" is checkable. The binding-other-items half uses T3 |
| T3 | Would the answer still bind a future item — an unrelated one (`policy`), or one touching the same subsystem (`design`)? | As indicated | Agent judgment, but recorded: triage names the concrete future item it imagines, or the test does not fire. "It might matter someday" without a named case does not classify upward |
| T4 | Does the question concern decomposition, sequencing, or which files or functions change, within unchanged criteria and design? | `plan` | Agent judgment by the plan owner, who owns the artifact in question |
| T5 | Is it decidable inside this lane, reversible within this diff, and invisible outside it? | `implementation` | The implementer's default; if T1–T4 did not fire, presume `implementation` and record the assumption |

**The boundary where inventing happens: "the design is wrong" versus "the design is incomplete and the lane must pick something."** The tests draw it concretely. A gap is a lane-decidable incompleteness only if *all* of these hold: every choice the lane might pick satisfies the frozen criteria as written; the choices do not differ in externally visible behaviour, in what the gate checks, or in any recorded policy's parameter; the choice binds no future item (T3 does not fire); and the choice is reversible within this diff. Picking under those conditions is implementation, and the lane records the assumption and proceeds (T5). If *any* condition fails — the choices differ observably, one of them would need a fenced artifact to change, or a future item would inherit the pick — then the design is, for routing purposes, *wrong or silent at a level above the lane*, and the lane records and routes. "The design didn't say, so I chose" is legitimate exactly when the choice cannot be observed outside the diff; the moment it can, choosing is deciding, and deciding belongs to the owner of that kind.

Worked mini-classifications, from the motivating case's neighbourhood: "base on upstream's latest" against a ledger entry fixing the follow reference fires T1 — `policy`. Declaring `follow: "head"` fires T1 directly, as a canonical-parameter change. "The launcher and the gate each implement the follow rule; make them share one resolver" fires T3 at subsystem reach — `design`. "Rebase the series, then re-export, then run the gate" is T4 — `plan`. "Rename this local variable while rebasing" is T5 — `implementation`.

### 5.2 The fence: what a lane may not edit

The criterion's second clause — no lane changes a design or policy to make its change apply — is enforced as a fence: a named set of artifacts that a lane executing an item may not edit *in that item's diff* unless the item itself is the owning seat's work on that artifact (a design lane writing its one design document is inside the fence by assignment, not by exception).

| Fenced artifact | Owner who may change it | Why it is fenced |
| --- | --- | --- |
| The item's frozen criteria, and any other item's criteria | The rung that froze them; policy-level changes, the human via `amend-policy` | A lane that edits its own criterion grades its own work |
| Design documents (`docs/design/` and equivalents) | The design seat, as an `amend-design` action | Editing the design to match the diff reverses the authority the design exists to hold |
| Policy artifacts: the policy ledger ([6.1](#61-the-policy-ledger)), and the canonical parameter a policy names | The human, via `amend-policy` | The `follow: "head"` move is precisely an implementer editing a canonical parameter |
| `AGENTS.md` and other standing instruction prose a launch reads | Its owning process, never a lane mid-item | A lane that edits the instructions it runs under can exempt itself from any rule in this document |
| Pack configuration and briefing/skill prose that a launch reads (`yolo-packs/`, skill definitions), where the edit would change how this lane or its judges are dispatched, modelled, or checked | The seat that owns that pack or skill, as its own item | Changing the dispatcher, the model pin, or the checker mid-flight changes who answers and what counts as passing |
| Gate definitions: what the landing gate runs and what it refuses | The gate's owner, as its own item | Weakening the check that is failing is fitting the rule to the work |

The fence is about *purpose and assignment*, not about file paths in the abstract: an item whose frozen criterion is "amend this design document" is executed by the design seat and edits that document legitimately. What the fence forbids is a lane assigned work *under* an artifact editing that artifact so the work applies. The mechanical form (check C2, [6.2](#62-the-checks-in-order)) flags a lane diff that touches a fenced artifact the item does not own; the judgment form is the inventing test in [section 7](#7-the-conflict-routing-rule).

**The alternative when the lane believes the design is wrong** is not obedience-in-silence and not editing: it is the conflict record, with the lane's belief stated as its `recommendation` and its evidence quoted in `conflict`. A lane that is right about a wrong design gets the design amended by its owner, with the lane's record cited as the reason — which is a stronger outcome for the lane's own work than a quiet edit, because the amendment then binds future lanes too.

## 6. Making contradiction a check

[5.1](#51-the-classification-tests) makes T1 mechanical exactly where policy is recorded in a form a checker can compare against. The aim is precise: "does this brief contradict a recorded decision?" should be a check the board or gate runs, not a judgment a lane makes under time pressure. Where the check cannot decide, it routes to an agent inside the state machine (triage) rather than passing silently or pinging the human.

### 6.1 The policy ledger

**Proposal (new artifact):** one Markdown file per policy under `docs/policy/`, with machine-readable YAML frontmatter and a prose rationale in the body — the same document discipline this design file itself follows, so policies get diff review and version history along with the code they govern. The board references policies by ID; it does not own their text. (The home and format are [OQ-1](#open-questions); the field semantics below are the settled part and survive any home.)

| Frontmatter field | Purpose |
| --- | --- |
| `id` | Stable dotted slug, e.g. `fork.follow-ref`. Criteria, briefs, conflict records, and casebook entries cite this ID |
| `status` | `active`, `superseded`, or `retired`; supersession names the replacing ID |
| `authority` | Always `human` for an active policy, with the deciding verdict or commit cited in the body |
| `statement` | The rule in one normative sentence: "The reference for a fork patch series is the newest upstream release tag." |
| `applies_to` | Scope that selects this policy without reading prose: repository paths and item tags. The primary matching key in [6.2](#62-the-checks-in-order) |
| `parameter` | Optional but preferred: `name`, `value`, and `canonical_source` — the one configuration key or symbol where the value lives. For `fork.follow-ref`: name `follow`, value `newest-release-tag`, canonical source to be named during implementation ([OQ-2](#open-questions)) |
| `check` | How conformance is verified: `kind: gate` with the gate step, `kind: parameter` for value comparison, or `kind: judge` with the exact question the judge is asked |
| `exceptions` | Explicit, enumerated, each with an expiry. An unlisted exception does not exist |
| `triggers` | Brief phrases that *hint* this policy may be engaged, e.g. `latest`, `evergreen`, `base on`. Hints route to triage; they never decide |

The body carries what frontmatter cannot: the rationale, the case that produced the policy, and links to casebook entries. A brief or plan, in turn, declares the policy-relevant parameters it engages as structured fields rather than prose, wherever an applicable policy defines a `parameter`. A plan to rebase a series declares its base reference as a value of `fork.follow-ref`'s parameter; "latest" is then not a valid value at all, and `branch-head` is a valid value that fails comparison. Ambiguity is refused at the type level before contradiction is even checked.

### 6.2 The checks, in order

Checks run at three points, cheapest and earliest first. Each failing check produces either a refusal or a pre-filled conflict record — never a silent pass and never a human ping.

| # | When | Check | On failure |
| --- | --- | --- | --- |
| C1 | Brief and plan authoring | Select applicable policies by `applies_to` against the item's declared paths and tags; require every applicable parameterised policy's parameter to be declared in the plan, and compare values | Plan is refused back to the plan owner with the ledger ID and expected value. This is `apply-recorded` territory and needs no routing unless the plan owner believes the policy itself wrong |
| C2 | Lane running, at diff and gate time | Flag any diff that touches a canonical source, item criteria, gate definitions, or any other fenced artifact the item does not own ([5.2](#52-the-fence-what-a-lane-may-not-edit)) | Automatic conflict record, pre-filled with the diff hunk, the fenced artifact, and any ledger ID, classified `policy` or `design` by T1/T2. In the motivating case, the `follow: "head"` declaration dies here even if it somehow passed C1 |
| C3 | Brief authoring | Scan brief prose against `triggers` of policies whose scope is near the item but not selected by C1 | Not a failure: a triage task for the plan owner to classify. The honest home for what keyword matching can and cannot do |
| C4 | Policy amendment | On an `amend-policy` answer, find open items and criteria citing the amended ID | Each is flagged for re-read against the new policy before its next lane |

C1 is where the motivating case should have ended: before any lane ran, at plan-owner prices, in one comparison. C2 exists because the case also proved that a policy can be subverted downstream of the brief, by editing the mechanism that implements it.

### 6.3 What stays judgment

This design does not claim that contradiction becomes wholly mechanical; it claims the judgment surface shrinks to a named residue:

- **Unparameterised policies.** A policy recorded only as prose cannot be value-compared. C1 degrades to "this policy applies; plan owner, attest conformance" — an agent inside the state machine, recording the attestation. Every policy conflict that reaches the designer or human must end, as part of its answer, by parameterising the policy if it can be parameterised, or recording why it cannot. The prose residue therefore only shrinks.
- **Scope selection.** Whether an item is inside a policy's `applies_to` can itself be disputed. That dispute is a `design`-kind classification question at most; it is never decided by the lane whose work depends on the answer.
- **Novel questions.** Where no policy applies and T3 fires, there is nothing to check against — that is a genuine routing to designer and human, and its answer becomes a new ledger entry. The ladder's purpose is to make these the *only* policy questions that cost human judgment, because they are the only ones that are actually undecided.

## 7. The conflict-routing rule

The owner's rule is settled here as the ladder's load-bearing constraint:

> **If there is a conflict with reality in the implementation plan, the implementer is not the one to fix it. The implementer records and routes; it never invents.**

"Inventing" is defined operationally, so the rule is checkable rather than homiletic. An implementer invents when, faced with a conflict, it does any of: changes policy, criteria, the gate, a canonical parameter, or any fenced artifact so the conflict disappears (the `follow: "head"` move); adds a new mechanism, mode, or exception whose purpose is to make the conflicting work acceptable; or quietly reinterprets the brief so the conflict is defined away. C2 mechanically catches the first; the judge and triage catch the others by the same test — *does this change exist to make contested work pass, rather than because a rung with authority asked for it?*

What the implementer does instead is exactly two things. **Record:** write the conflict record of [4.1](#41-the-conflict-record) on the board, linked to the item. **Route:** freeze the contested work in place ([4.4](#44-the-lanes-already-made-change)) and let the transition carry the packet to the classifying rung. The implementer may attach a recommendation; a recommendation is thinking offered upward, which the ladder wants, and is precisely distinguished from a fix applied downward, which it forbids.

The rule generalises one rung up by the same shape: the plan owner, faced with a design or policy conflict, records and routes rather than re-planning around it invisibly; the designer, faced with a policy question, records options and routes rather than settling policy by drafting around it.

## 8. Cost, routing order, and failure modes

The ladder exists to spend judgment where it is cheapest adequate: the designer is expensive, the plan owner is cheap, and the human's attention is the scarcest resource of all. Cost discipline is therefore a routing property, not a plea for frugality.

### 8.1 Routing order

1. **Classify first, mechanically where possible** ([5.1](#51-the-classification-tests), [6.2](#62-the-checks-in-order)). Classification is the routing decision; there is no second step that second-guesses it.
2. **Route to the lowest rung whose authority covers the kind**: `implementation` stays in the lane and is not a routing at all; `plan` goes to the plan owner; `design` goes to the designer; `policy` goes to the designer *for digestion* and then to the human for decision — except where C1 or C2 fired against an active parameterised policy, where the policy is already decided and any rung applies it via `apply-recorded` without involving the human at all.
3. **Exhaust before forwarding.** A rung forwards only by adding its evidence layer ([3.3](#33-what-a-routing-carries-by-rung)) and stating why the question exceeds its authority or why the record is silent. The required fields enforce this: `policies_checked` may not be blank, so "I didn't look" cannot be forwarded.
4. **Plan-level surprises never reach the designer.** A function that moved, a file that differs from the plan, an approach that does not survive contact with the code — these are the plan owner's daily bread and the reason that rung is a separate cheap seat. A designer that receives one reroutes it down rather than answering it, because answering teaches the system to misroute and bills designer rates for plan work.

### 8.2 When the wrong seat gets it

Wrong-seat delivery is expected and must be cheap; a ladder that punishes misrouting gets caution-inflation instead.

- **Two-phase receipt.** The receiving seat's first act is triage-only: accept, or reroute up or down with a one-line classification correction. Substantive work begins only after acceptance. A designer's misdelivered plan question therefore costs a triage read, not a designer engagement. A seat that answers a packet outside its authority has committed the same offence as an inventing lane, one rung up; the dispatch refusals in [3.2](#32-the-plan-owner-a-cheap-seat-distinct-from-the-design-seat) exist to make that a refused action rather than a discovered one.
- **The packet travels.** Rerouting re-enters `triaged` with all accumulated evidence; no rung re-investigates what a lower rung already established. The marginal cost of a wrong seat is one triage, not a restart.
- **Misrouting is measured, not blamed.** Reroute counts by raising rung and by kind are board-visible signals ([9.2](#92-signals)): persistent upward misrouting means the classification tests or the ledger's `applies_to` need repair; persistent downward rerouting from the designer means plan-owner coverage has a hole.

### 8.3 Failure modes this design accepts or refuses

| Failure mode | Accepted or refused | How it is bounded |
| --- | --- | --- |
| **A conflict nobody answers** (owner absent, seat dies, packet forgotten) | Refused as a silent state; accepted as a visible wait | The item stays visibly blocked-on-decision with rung and age on the board surface — never quietly ready, never quietly dropped. An `awaiting` packet that exceeds its rung's time bound reroutes upward with its age recorded ([OQ-3](#open-questions) sets the bounds). A timeout **never** expires into a default answer: a timed-out policy question answered by default is a silent policy decision (R1). The terminal state of true abandonment is an explicit `abandon` answer or a human closing the item — both recorded, neither inferred from silence. Meanwhile the drain works other items, so one orphaned conflict cannot stall the system |
| **A conflict answered by the wrong seat** | Refused | Two-phase receipt ([8.2](#82-when-the-wrong-seat-gets-it)) plus the dispatch refusals of [3.2](#32-the-plan-owner-a-cheap-seat-distinct-from-the-design-seat): the wrong seat's only permitted first act is reroute. An answer recorded by a seat without authority over the kind is invalid on its face — the record names the answering seat and the kind, so the mismatch is structurally checkable after the fact ([section 11](#11-observability-and-testing)) |
| **A lane that routes everything to avoid deciding anything** | Refused | Required fields at write time make a content-free routing unwritable; T3 requires a named future item to classify upward; `implementation`-kind questions are bounced back down with a pointer to the record that answers them, and withdrawals and bounce-backs are counted per raising seat ([9.2](#92-signals)). Persistent over-routing is therefore data — a briefing, ledger, or seat defect to repair — not a behaviour the ladder silently subsidises. The bound is economic as well as structural: an over-routing lane spends its own time writing evidence packets for questions it could have decided in one recorded assumption under T5 |
| **A lane that routes nothing and invents quietly** | Refused | The fence check C2 catches edits to owned-elsewhere artifacts regardless of the lane's intent; the judge applies the inventing test of [section 7](#7-the-conflict-routing-rule) to mechanism-shaped diffs. This design is honest that a purely semantic invention inside unfenced files is caught by judgment, not by a check ([section 11](#11-observability-and-testing)) |
| **Two conflicts on one item, or one conflict spanning items** | Accepted | Records are per-conflict, not per-item; an item with two open records is blocked on both, and each closes on its own answer. A policy-level answer flags sibling items via C4 rather than pretending the conflict was local |

## 9. Convergence

The owner's ambition — the more the designer decides as the owner would, the fewer decisions reach the owner — is a learning loop, and a learning loop needs recorded corrections, not impressions. Every correction in this loop is an artifact the designer reads at launch.

### 9.1 The casebook

**Proposal (new artifact):** a casebook of decided conflicts, one entry per decision that set precedent, stored with the policy ledger and cited by ID. Each entry records: the situation in the packet's terms (question, kind, policies checked); the deciding rung's answer; and, where the human overruled or amended a designer recommendation, the correction and the distilled rule it implies. The motivating case is the casebook's first entry by construction: brief phrase "base on upstream's latest," policy `fork.follow-ref`, wrong moves (rebase onto head; declare `follow: "head"`), owner correction, distilled rules — *an ambiguous superlative in a brief does not amend a parameterised policy*, and *changing the checked value to match the work is inventing*.

Corrections flow in a fixed direction: casebook entry first (the raw precedent), then promotion. A correction that states a general rule is promoted to a policy ledger entry when it is policy, or to the designer's standing guidance when it is design judgment — the distinction being T3's reach test again. The designer reads the ledger and casebook as part of its seat briefing; that reading is the mechanism by which decided cases become future behaviour. The human's part shrinks correspondingly — each step taken only when the signals in [9.2](#92-signals) say it is earned, and each expansion of designer authority recorded as a human decision ([OQ-4](#open-questions)).

### 9.2 Signals

Convergence is claimed from measurement or not at all. The board holds the necessary raw material in conflict records:

| Signal | Converging looks like | Source |
| --- | --- | --- |
| Owner overturn rate on designer recommendations | Falling, and staying low as volume rises | `answer` versus `recommendation` in closed records |
| Policy-kind conflicts resolved without the human | Rising share closed by `apply-recorded` at plan-owner rung | Answer kinds in [4.3](#43-answers-amend-versus-proceed-as-is) |
| Repeat conflicts per policy ID | Falling toward zero; a second conflict on one ID means parameterisation failed and is itself routed | `policies_checked` across records |
| Reroutes from the designer downward | Falling share of designer-received packets (plan surprises stop arriving) | Transition history |
| Bounce-backs and withdrawals per raising seat | Falling; a rising count is over-routing made visible ([8.3](#83-failure-modes-this-design-accepts-or-refuses)) | Transition history, withdrawal records |
| Age of the oldest `awaiting` packet, by rung | Bounded; an ageing tail is abandonment made visible before it becomes loss | Board surface |
| Cost per closed conflict, by kind | Falling for `plan` and `design`; human-touched count falling overall | Answering seats and rung times on records |
| Cases the owner had to catch outside the ladder | Zero. One owner-catch of an invented fix is a ladder failure, reviewed as such | Owner-reported; the absence of a signal here is why the other signals must be honest |

If the overturn rate does not fall, the honest conclusion is that the casebook is not capturing the owner's judgment in readable form, and the remedy is better distillation of corrections — not more designer authority.

## 10. Refusals

The ladder must never do the following. Each refusal is stated with its enforcement, because an unenforceable refusal is a hope — the same discipline [landing principles](landing-principles.md) applies when it says a principle whose enforcement column says "nothing" is a claim, not a rule.

| ID | Refusal | Enforced by |
| --- | --- | --- |
| R1 | **No silent decision above authority.** No rung decides a higher-kind question by acting as if it were its own kind — including by timeout default or by editing the mechanism that checks it | C2 on fenced artifacts; timeout-reroutes-up in [8.3](#83-failure-modes-this-design-accepts-or-refuses); judge review against the inventing test |
| R2 | **No fitting the rule to the work.** Policy, criteria, design, and the gate are never changed so that contested work passes; they change only through an amend answer by the rung that owns them | The fence ([5.2](#52-the-fence-what-a-lane-may-not-edit)); C2; answer kinds in [4.3](#43-answers-amend-versus-proceed-as-is) |
| R3 | **No routing as avoidance.** A rung may not route a question the record answers, nor route without its evidence layer, nor classify upward without T3's named future item | Required fields at board write time; withdrawal and bounce-back counted in [9.2](#92-signals) |
| R4 | **No blind resume, no silent revert.** An answer that changes the question reworks; tainted work is disposed of only by recorded owner decision | Answer kinds require a taint assessment; freeze-and-retain in [4.4](#44-the-lanes-already-made-change) |
| R5 | **No self-adjudication.** The rung that raises a conflict never answers it; the rung that answers a contested question is not sole judge of the criterion it decided; the plan owner and the designer may not fill each other's transitions | Dispatch refusals in [3.2](#32-the-plan-owner-a-cheap-seat-distinct-from-the-design-seat); extends the existing judge refusal of lane self-sign-off |
| R6 | **No off-board decisions.** A policy or design decision made in conversation binds nothing until it is in the ledger or casebook | The checks read the ledger, not transcripts; what the checker cannot see does not exist |
| R7 | **No raw questions to the human.** The human receives policy packets digested by the designer (or, where C1/C2 fired, no packet at all because the policy was already decided) | Routing order, [8.1](#81-routing-order) step 2 |
| R8 | **No unrecorded assumptions.** Proceeding under an assumption a rung had discretion to make still records it, as a decision pending review | T5's record-and-proceed in [5.1](#51-the-classification-tests) |
| R9 | **No bypass for urgency.** Expedited priority changes queue order, never authority | Absence of any expedite path in the transition table is deliberate |

## 11. Observability and testing

This is a workflow and policy mechanism, not software with a single behaviour to assert, so this section is honest about the split. The repository's convention that new or changed behaviour gets a test applies to everything here that *is* behaviour; the rest is observed, not tested.

### 11.1 What can be tested

| Behaviour | Test shape |
| --- | --- |
| A conflict record refuses to be written with a required field blank (`question`, `conflict`, `policies_checked`, `blocked_work`, `tainted_work`) | Structural: attempt the write, expect refusal, per field |
| Illegal transitions are refused (`closed → awaiting`, an `answer` with no cited record, a self-answer by the raising seat, a plan-owner transition filled by the designer seat) | State-machine tests over the transition table in [section 4](#4-routing-as-a-recorded-transition) and the dispatch refusals in [3.2](#32-the-plan-owner-a-cheap-seat-distinct-from-the-design-seat) |
| C1 refuses a plan whose declared parameter contradicts an active parameterised policy, and refuses a non-value ("latest") | Fixture brief and ledger entry; the motivating case is the first fixture ([section 12](#12-the-motivating-case-replayed)) |
| C2 flags a lane diff touching a fenced artifact the item does not own, and does not flag the owning seat's assigned edit to the same file | Fixture diffs in both directions; the negative case matters as much as the positive |
| C4 flags open items citing an amended policy ID | Fixture board state before and after an `amend-policy` answer |
| A timed-out `awaiting` packet reroutes upward and never acquires a default answer | Clock-controlled transition test |
| The board surface shows blocked-on-decision items with rung and age | The same honesty test [landing principle 7](landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report) demands of parked work: what is not moving must be visible as not moving, with the reason |

### 11.2 What cannot be tested, and what stands in its place

- **Whether a given question is really `plan` or really `design`.** T3 is judgment with a recording duty (name the future item), not a computation. What stands in its place: reroute rates per classifier ([9.2](#92-signals)) — a seat whose classifications are constantly overturned is measured, then its briefing or the tests are repaired.
- **Whether a prose brief "really" contradicts a prose policy.** C3 only hints. What stands in its place: the plan owner's recorded attestation, and the shrinking-residue rule of [6.3](#63-what-stays-judgment).
- **Whether an invention inside unfenced files "exists to make contested work pass."** That is the judge's question, asked in exactly those words. What stands in its place: the casebook, which turns each judged instance into precedent the next judge reads.
- **Whether the designer "decides as the owner would."** Not testable in advance, by construction. What stands in its place: the overturn rate, claimed only from measurement ([9.2](#92-signals)).

The dividing line is the one this whole design rests on: a documented transition with fields can be checked structurally, and must be; a judgment call cannot, and must be recorded so it can be reviewed, counted, and corrected. A test suite that claims to verify the second kind would be inventing, in this document's own sense.

## 12. The motivating case, replayed

Under this design the case that motivated it runs as follows, and ends earlier, cheaper, and without the human being the only working check:

1. A brief is written saying "stay evergreen, base on upstream's latest." C3 notices the trigger word "latest" near a series-rebasing item and opens a triage task for the plan owner; C1 independently selects `fork.follow-ref` by `applies_to` and requires the plan to declare its base reference as a value of that policy's parameter.
2. The plan owner cannot write "latest" into that field — it is not a value. Writing `branch-head` fails comparison against `newest-release-tag`. The plan owner's correct, unremarkable act is to apply the recorded policy: the plan says rebase onto the newest release tag. No routing, no designer, no human. The conflict is named by a check, in writing, at plan prices.
3. Suppose instead the plan owner believes the policy is wrong — that head really should be followed. That belief is a `policy` question by T1. The plan owner raises a conflict record with the policy ID, the options, and a recommendation. The designer digests it — precedent, cost of tracking a moving head, gate implications — and the human decides. The item waits, visibly, blocked-on-decision. What no rung does is declare `follow: "head"`: C2 flags the canonical-parameter diff automatically even if attempted.
4. Suppose the lane had already rebased onto head before the conflict was named. That work is tainted in full: it freezes in its worktree, is recorded as `tainted_work`, and is discarded by the recorded `apply-recorded` answer — not quietly rebased over, and not landed in pieces.
5. The lane rebases onto the release tag under the amended-or-clarified plan. If the gate still fails for an unrelated reason, that is a fresh, ordinary conflict, routed by its own kind — most likely `plan`, decided by the plan owner without ceremony.

The replay exposes the design's central exchange: judgment the owner already exercised once (follow the newest release) is never paid for again — not in designer tokens, not in a doomed lane, and not in an owner catch after the fact.

## 13. Decision ledger

| ID | Decision | Settled in |
| --- | --- | --- |
| D1 | Four rungs — implementer, plan owner, designer, human — with authority defined by question kind; applying a recorded higher-kind decision is within every rung's authority | [3.1](#31-authority-by-rung) |
| D2 | The plan owner is a named cheap-model seat distinct from the designer; distinctness is enforced by three dispatch refusals, not by prose | [3.2](#32-the-plan-owner-a-cheap-seat-distinct-from-the-design-seat) |
| D3 | A conflict is a board-held, item-linked, append-only record with board-enforced required fields; it outlives the lane and is retained after closing | [4.1](#41-the-conflict-record) |
| D4 | Routing is a recorded transition over `raised → triaged → awaiting → answered → closed`, an overlay on any item state, with the conflict record as every transition's cited evidence | [section 4](#4-routing-as-a-recorded-transition) |
| D5 | Answers are typed and split into proceed-as-is (`apply-recorded`, `clarify`) versus amend (`amend-plan`, `amend-design`, `amend-policy`), plus `abandon`; an amendment is a real action by the owning seat, never a lane edit | [4.3](#43-answers-amend-versus-proceed-as-is) |
| D6 | A lane's mid-flight diff on raising a conflict is frozen and retained in its worktree; it is neither landed nor silently reverted, and its disposal is a recorded owner decision | [4.4](#44-the-lanes-already-made-change) |
| D7 | Classification is by ordered tests T1–T5, most mechanical first; the wrong-versus-incomplete boundary is observability outside the diff, tested by four named conditions | [5.1](#51-the-classification-tests) |
| D8 | The fence names the artifacts a lane may not edit to make its change apply: criteria, design docs, policy ledger and canonical parameters, standing instruction prose, dispatch-affecting pack and skill config, gate definitions | [5.2](#52-the-fence-what-a-lane-may-not-edit) |
| D9 | Policy is recorded in a ledger with machine-readable scope, parameterised values, and canonical sources; contradiction checks C1–C4 run at plan, lane/gate, and amendment time; every high-rung policy answer parameterises what it touched or records why it cannot | [6.1](#61-the-policy-ledger), [6.2](#62-the-checks-in-order), [6.3](#63-what-stays-judgment) |
| D10 | The conflict-routing rule: the implementer records and routes, never invents, with "inventing" operationally defined; the shape generalises up the ladder | [section 7](#7-the-conflict-routing-rule) |
| D11 | Routing is lowest-adequate-rung with exhaust-before-forwarding; two-phase receipt and travelling packets make wrong-seat delivery cost one triage; an unanswered conflict reroutes upward and never defaults | [8.1](#81-routing-order), [8.2](#82-when-the-wrong-seat-gets-it), [8.3](#83-failure-modes-this-design-accepts-or-refuses) |
| D12 | Convergence runs through a casebook of decided conflicts and owner corrections, promoted into ledger entries or designer guidance, and is claimed only from the signals in [9.2](#92-signals) | [9.1](#91-the-casebook) |
| D13 | Refusals R1–R9 are enforced by checks, required fields, dispatch rules, and transition shape, not by instruction prose alone | [section 10](#10-refusals) |
| D14 | Structure is tested (fields, transitions, dispatch refusals, C1/C2/C4, timeout behaviour, surface honesty); judgment is recorded and measured instead | [section 11](#11-observability-and-testing) |

## Open Questions

Each question is the user's to settle, carries the default this design assumes if it is never answered, and blocks nothing until it is settled in this section.

| ID | Question | Default if unanswered |
| --- | --- | --- |
| OQ-1 | Where does the policy ledger live: per-policy files under `docs/policy/` as proposed in [6.1](#61-the-policy-ledger), a single ledger file, or a board table? | Per-policy Markdown files with YAML frontmatter in the repository they govern, board referencing by ID — policies then get diff review and travel with the code; only the location, not the field semantics, is open |
| OQ-2 | What is the canonical source for `fork.follow-ref` today, given the policy is implemented in both the launcher and the gate? This names the one symbol or key for C2 to watch | Whichever of the two implementations the gate reads is canonical until the two consumers share one resolver (a `design` question per [5.1](#51-the-classification-tests)); C2 watches both locations in the meantime |
| OQ-3 | What time bound applies to an `awaiting` packet per rung before it reroutes upward, and does the human rung get a bound at all — and if so, who is notified when it passes, since there is no rung above to reroute to? | No bound expires into an answer, at any rung. Until bounds are set, the board surfaces packet age by rung and the drain works other items; an ageing human-rung packet is visible on the same surface rather than escalated by interruption |
| OQ-4 | What blast-radius boundary, beyond policy itself, reserves a `design` decision to the human, and what measured threshold (per [9.2](#92-signals)) earns the designer provisional authority in a scoped domain? | None and none: the designer recommends on all policy matters and the human ratifies, until the human records a scoped grant as a policy. Convergence is measured first; authority follows |
| OQ-5 | Which existing seat type fills the plan-owner rung, and is it the same seat that authors briefs? | A cheap planning seat distinct from both lane and designer, per [3.2](#32-the-plan-owner-a-cheap-seat-distinct-from-the-design-seat). Brief author and plan owner may coincide for small items only where C1 passed without flags, since the author's own ambiguities are what triage exists to catch |
| OQ-6 | Are designer decisions on `design` questions subject to an asynchronous owner-review window, after which silence ratifies? | Yes for `design` only, reviewed in batch from the casebook; silence never ratifies `policy` (R1) |
| OQ-7 | In what order do existing prose policies get parameterised into ledger form? | On first touch: any item whose scope selects a prose policy parameterises it as part of that item's plan (D9), starting with `fork.follow-ref`, whose value, authority, and refutation case are already fully recorded in [section 12](#12-the-motivating-case-replayed) |