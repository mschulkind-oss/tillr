---
title: "What the operator settled without the owner is settled inside tillr: seven kinds of hand action, and who takes each"
status: in-review
stage: DESIGN
updated: 2026-11-24
tags: [tillr, drain, operator, holds, faults, design]
summary: "The owner ruled that a problem the operating agent could solve without him goes to a similarly responsible agent, process or ticket inside tillr, and not to the operating agent. This design sorts the operator's hand actions into seven kinds and gives each one of three takers inside tillr: the drain when a recorded fact decides, the design seat when an item's words or shape must be judged, and a filed defect item when tillr itself is wrong. It lists what stays with the owner. Everything but his quoted words is the designer's decision. Three questions are open. Only the `no_change` close and defect filing for document-backed items are built."
next: "Rule OQ-OD1, OQ-OD2 and OQ-OD3. Slices 1 to 9 and 11 of Section 12 wait on none of them and can be filed now."
depends-on:
  - landing-principles.md
  - tillr-item-split.md
  - unattended-drain.md
  - tillr-judge-and-lifecycle.md
vantage:
  status-chip: true
---

# What the operator settled without the owner is settled inside tillr

**Status:** Not built, with two exceptions: an investigation that finds nothing to change closes through `verify` ([§5.3](#53-an-item-whose-fix-already-landed)), and a `tooling` obstacle on a document-backed item files a defect item ([§3](#3-what-exists-today)). The owner's statement quoted in [§1](#1-the-ruling) is the only ruling written here; every other decision is the designer's and is marked so in the [Decision Ledger](#decision-ledger).

> **In short.** Every decision the operator takes by hand has one of three natural takers inside tillr: the drain, when a recorded fact decides it; the design seat, when an item's words or shape must be judged; and a filed defect item, when tillr itself is wrong. The operator keeps none of them, and the owner keeps only what changes what he asked for.

**Why it matters.** Work waits for the operator's next check-in each time one of these decisions is needed, and a hand decision can be wrong in a way nothing checks: an item set to landed as a duplicate of work that does not deliver it ([§5.3](#53-an-item-whose-fix-already-landed)).

**The shape.** Six rules, then one section for each of the seven kinds of action, each naming who takes the decision, what that taker is given, and what still goes to the owner.

**Cost.** More design-seat runs: one for each obstacle on a filed request and one for each fault whose cause is not recorded. One read-only verifier run for each item finished without a landing of its own.

**Start at** [§4](#4-the-rules), then the table in [§5](#5-the-seven-kinds).

**Needs your ruling:** [OQ-OD1](#OQ-OD1) (may a tillr program repair its own stuck state), [OQ-OD2](#OQ-OD2) (what tillr does when no ordered work remains), [OQ-OD3](#OQ-OD3) (which seat takes these decisions).

**Awaits your review:** the designer's decisions `D-OD1` to `D-OD12` in the [Decision Ledger](#decision-ledger).

**Reads with:** [landing principles](./landing-principles.md#16-what-the-operator-can-settle-is-settled-inside-tillr) (where the ruling is recorded as a principle), [the split design](./tillr-item-split.md) (which owns the second kind whole), [the unattended-drain design](./unattended-drain.md#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) (whose seventh rule this design fills in), and [the judge and lifecycle design](./tillr-judge-and-lifecycle.md) (how a worker's ending and a design run's ending move an item).

**Words.** [The vocabulary](./tillr-vocabulary.md#5-the-words) defines item, criterion, candidate, lane, seat and verdict. [The unattended-drain design](./unattended-drain.md#2-terms) defines drain, hold, mover, fixer and operator. These are used here:

- **Hand action** *(coined here)*: a change to the store or the working tree that the operator makes with a command, as opposed to one a tillr program makes while it runs.
- **Obstacle**: one of the five named reasons a worker may end without a commit: `definition`, `trunk`, `tooling`, `seat` and `owner` ([the drain's reference](../../tools/tillr-drain/README.md#a-worker-ends-with-its-commit-or-a-named-obstacle)).
- **Fault**: a recorded failure of tillr's own machinery that holds one item ([the drain's reference](../../tools/tillr-drain/README.md#faults-that-hold-one-item)).
- **Clearing fact** *(coined here)*: the recorded fact that ends a fault, which is either a named check being green at a commit that contains a named commit, or a named item having landed.
- **Closing check**: the read-only verification of an item's own criteria against the trunk, defined in [the split design](./tillr-item-split.md#75-when-the-parent-is-finished).

---

## 1. The ruling

The owner ruled on 2026-11-21, verbatim:

> "I guess a working principle should be that if you ever feel that you as [the operator] could solve some problem, and it wouldn't need to be escalated to me, then that problem should escalate to... a similarly responsible agent, process, ticket, whatever, within Tiller, rather than popping up to you."

What I take as ruled, because the words state it:

1. The test is whether the problem needs the owner. A problem the operator could solve without him does not belong to the operator either.
2. Its taker is inside tillr, and may be an agent, a process or a ticket.
3. The taker is "similarly responsible": it carries the decision as the operator would have, with the same care and the same answerability.

What the words leave open: which taker gets which problem, what it is given, and where the line to the owner runs. Those are settled below as the designer's decisions, and he may overturn any of them.

## 2. The day, measured

The basis of this design is one calendar day of the store's events, read with `tillr-store events -store <store> -output <file>` and filtered to that day. The table names the event that records each hand action and how many that day held.

| Action | Where the store records it | Count |
| :--- | :--- | ---: |
| An item closed by the operator | `item.update` with `lifecycle_action: close` and `lifecycle_by: operator` | 29 |
| An item set to landed by the operator without a landing of its own | `item.update` with `lifecycle_action: finish-existing-landing` and `lifecycle_by: operator` | 5 |
| A hold addressed to the operator | `drain.obstacle-hold` with `mover: operator` | 29, on 21 items |
| A design retry requested by the operator | `drain.retry.design` with `requested_by: operator` | 2 |
| A fault recorded, and a fault cleared | `drain.item-fault`, `drain.item-fault-cleared` | 30 recorded, 28 cleared |
| An item placed in the order | `item.update` with `placed_by: operator`, `placed_via: cli` | 338 events on 207 items |
| An item added | `item.add` | 192, of which 176 are requests |

The events leave three things unrecorded:

- **A fault clearing names no actor.** `drain.item-fault-cleared` has no field for who cleared it.
- **An added item names no filer.** `item.add` has no field for who filed it.
- **A placement does not say why.** Filing a request writes a placement, and the events do not mark which placements moved a blocking defect to the front.

Slice 1 of [§12](#12-the-build-in-slices-that-each-land-alone) closes those three gaps.

The 34 closes and finishes each carry a reason the operator wrote. Read by reason, they fall into four groups: 8 items too big or refused on changing findings and filed again as smaller items, 5 whose fix had landed from another item, 11 whose words were wrong and were filed again corrected, and 10 in a stuck state or with a returning fault that no command could move. The eleven closes for wrong words belong to the request-amendment design ([§9.2](#92-the-request-rewrite-design)).

## 3. What exists today

| Mechanism | What it does | Where the operator is still the mover |
| :--- | :--- | :--- |
| A worker's obstacle on an item backed by a design document | `definition` and `owner` go to the design seat, which answers from the documents or objects naming the owner ([`D-JL9`](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9), [`D-JL11`](./tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11)). `tooling` files one defect item keyed by its command, and the item waits on it (`fileDefectItem` in `tools/tillr-drain/drain/defect.go`) | — |
| A worker's obstacle on a filed request | `trunk` waits on the drain, `owner` on the owner | `tooling`, `seat`, and `definition` when no design seat is mapped, each set `Mover = "operator"` in `tools/tillr-drain/obstacles/read.go`. The worker's brief (`tools/tillr-run/backends/brief-template.md`) tells the worker something else: that for `tooling` "The loop files a defect item for the tool, and this item waits on it" |
| The per-item path list (`fence`) | Retired by the owner. The obstacle reader only releases `fence` holds that are in the store | — |
| The table of hold reasons | Each reason declares its mover | `rg -n 'operator' tools/tillr-drain/holdrules/holdrules.go` finds seven reasons whose mover text names the operator: `backend-window`, `backend-closed`, `backend-missing`, `judge-backend-unavailable`, `infrastructure-repeat`, `verify-triage` and `repeating-call` |
| A trunk obstacle | The drain releases the hold when the named checks are green, and records why (`drain.obstacle-hold-cleared`) | — |
| A blocker that has landed | The drain releases what waited on it ([rule 7](./unattended-drain.md#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) records it) | A blocker that is closed and filed again |
| Item faults | A fault holds one item, and `tillr drain clear-fault` clears it | Every fault: no reason declares what clears it |
| A split | Decided in [the split design](./tillr-item-split.md) | The operator closes the item and files the parts by hand |
| Finishing an item that landed elsewhere | `tillr item state --state landed --finish-existing-landing`, a hand command ([the item reference](../../tools/tillr-item/README.md#finish-an-item-that-already-landed)) | Every use |

[Rule 7 of the unattended-drain design](./unattended-drain.md#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) rules that a hold names the operator only for a decision the rulebook reserves, and its designer's decision `D-UD7` makes the table of hold reasons enforce that. It does not say which decisions are reserved, and it covers holds only. This design supplies the reserved list (it is empty: [§7](#7-what-the-operator-still-does)) and covers the hand actions that were not answers to a hold.

## 4. The rules

- **T1. Three takers, chosen by what decides the matter.**
  - *A recorded fact decides it:* **the drain** acts, and records the fact it acted on.
  - *The item's words or its shape must be judged:* **the design seat** decides, in a run with the evidence in its brief.
  - *Tillr itself is wrong:* **a defect item** is filed, and the work that met the defect waits on it.
- **T2. The owner is asked only for what is his** ([§6](#6-what-is-the-owners)). A taker that meets such a decision says so by naming the owner, as a design run does ([`D-JL9`](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9)). Nothing reaches him because a taker failed or took long.
- **T3. The evidence travels with the decision.** Each taker is given the record the operator had to go and read: the obstacle's fields, the fault's detail, the verdicts, the run's log, the commits named. [§5](#5-the-seven-kinds) lists it per kind.
- **T4. Nothing here counts or times.** No rule in this design reads a number of attempts, refusals, clearings or minutes. Every trigger is a single recorded fact or a comparison of two records.
- **T5. A taker never changes the bar.** No seat or process weakens, narrows or drops what the owner's words ask for. An item leaves the queue without landing only by a closing check that passes — for work already on the trunk ([§5.3](#53-an-item-whose-fix-already-landed)) or, on `planned-build@v1`, when `verify` passes a worker's `no_change` finding that reproductions show the repository needs no change ([`D-JL34`](./tillr-judge-and-lifecycle.md#421-an-investigation-that-finds-nothing-to-change-ends-inside-tillr-d-jl34), a designer's decision the owner may overturn) — or by the owner's own words.
- **T6. A hand action is evidence of a missing rule.** The operator can still act with ordinary commands. Each hand action is recorded with its kind, and a hand action of a kind whose slice has landed means a taker failed, which is a defect to file and not a routine.

## 5. The seven kinds

| # | The operator's action | Taker | The owner's part |
| ---: | :--- | :--- | :--- |
| 1 | Answering a hold with a technical decision | The design seat for `definition` and `seat`; a defect item for `tooling` | A question the design seat hands on by naming him |
| 2 | Closing an item refused on unrelated findings and filing it again in slices | The split design: the verifier calls, the design seat divides, the drain files | Dropping a required part |
| 3 | Closing an item whose fix had already landed | The drain raises it and a read-only verifier decides; the drain alone for a fixer | Nothing |
| 4 | Clearing a fault, and closing an item whose fault returned | The drain when the fault's clearing fact holds; the design seat when no cause is recorded or the fault returns | Nothing |
| 5 | Clearing a stuck state by hand | A defect item, filed by the program that finds the state | [OQ-OD1](#OQ-OD1) |
| 6 | Moving a blocking defect to the front | The drain, from the recorded wait | Every other change of order |
| 7 | Filing ready work when no item waited for a lane | Each source files its own work | [OQ-OD2](#OQ-OD2) |

### 5.1 A hold answered with a technical decision

**`fence`: nobody.** The owner retired the path list. [Rule 7](./unattended-drain.md#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) records that this hold has no successor, and this design adds nothing to it.

**`definition` and `seat` on a filed request: the design seat**, by the route a document-backed item takes. A filed request has no design block to amend, so the answer takes one of four forms:

| The design seat's answer | When | What happens next |
| :--- | :--- | :--- |
| **An answer on the item** | The obstacle is a question of fact or of reading, and the request can be built as written once it is answered | The answer is recorded on the item and quoted in the brief of every later run of that item. Where the answer states a rule that outlives the item, the design seat also commits it to the design document that owns the subject, and that commit is gated like any design commit |
| **A split statement** | Part of the request can land alone | [The split design](./tillr-item-split.md#71-the-split-statement) takes over |
| **A rewrite** | The request's words are wrong about the tree | The request-amendment design takes over ([§9.2](#92-the-request-rewrite-design)) |
| **An objection naming the owner** | The decision is one of [§6](#6-what-is-the-owners) | The item is held on the owner with the question and its options |

The seat is given the owner's words, the obstacle's fields, the run's report and candidate if it made one, the item's earlier answers, and the design documents the words reach.

**`tooling`: a defect item.** A worker that names a broken tool has found a defect in tillr or its harness. The drain files one item from the obstacle's `COMMAND`, `EVIDENCE` and `PATHS` fields, and the held item waits on it through a `blocked_by` edge. The drain releases the wait when the defect item lands.

- **One defect, one item.** Filing is keyed by the tool paths and the command, so a second worker that meets the same defect adds its item to the waiters and files nothing.
- **The worker's claim is not a verdict.** The defect item's own worker tests it. A worker that finds the tool sound ends with a `definition` obstacle, the design seat answers, and the answer goes onto the items that waited.
- **The defect item is dispatched first** ([§5.6](#56-a-blocking-defect-moved-to-the-front)).

### 5.2 An item refused on unrelated findings

[The split design](./tillr-item-split.md) owns this kind whole, and this design changes none of it. Its verifier's call ([slice 8](./tillr-item-split.md#14-the-build-in-slices-that-each-land-alone)) takes the item out of the repair loop, its design seat writes the division, the drain files the parts, and carving (slice 9) keeps the candidate.

Two hand habits differ from that design, and the design wins:

- **The item is not closed.** Closing the item and filing new ones loses the parent. The split design keeps it open as the parent until its closing check passes, and rejects closing it ([its alternatives](./tillr-item-split.md#12-alternatives-considered)).
- **A request is not narrowed.** Under rule T5 the cases a smaller refiling leaves out are parts of the same parent, and only the owner drops one ([slice 12 of the split design](./tillr-item-split.md#14-the-build-in-slices-that-each-land-alone)).

### 5.3 An item whose fix already landed

The failure this kind guards against: an item is set to landed as a duplicate of work that landed, and that work delivers something else. Nothing compares the item's criteria with the trunk.

**The rule: an item is finished without a landing of its own only by a closing check that passes.** Similar titles, a reason written by hand, and a worker's statement are claims. The check is the one [the split design defines for a parent](./tillr-item-split.md#75-when-the-parent-is-finished), run on an item that was never split: a read-only verifier judges the item's own criteria against the trunk.

| Who raises it | On what recorded fact | Who decides |
| :--- | :--- | :--- |
| A worker | It ends its run stating that the trunk already satisfies the request, and names the commits | The closing check |
| The design seat | It holds the item for another reason and states the same, naming the commits | The closing check |
| The drain, for a fixer | Every check the fixer was filed to repair is green at the trunk's tip | The drain alone: a fixer's whole statement is those checks, so running them is the closing check |

- **The verifier is given** the item's words and criteria, the commits named, and the trunk.
- **It passes:** the item is finished as resolved elsewhere, with no commit of its own, and its record lists the commits that satisfied it.
- **It does not pass:** the unmet criteria are named, and the item goes on as ordinary work with that finding in its brief. Nothing is closed.
- **Items that waited on it** are released when it finishes, as when it lands.

**An investigation that finds nothing to change (`no_change` on `planned-build@v1`).** This is a designer's decision ([`D-JL34`](./tillr-judge-and-lifecycle.md#421-an-investigation-that-finds-nothing-to-change-ends-inside-tillr-d-jl34)) that the owner may overturn, and it is built. It differs from an item satisfied by earlier trunk commits: the worker's reproductions show that the reported failure comes from outside the trunk. `plan` or unsplit `work` ends with `no_change` (`cause`, `evidence`, `why_no_change`). The read-only `verify` judge checks that finding independently against the item's words and criteria at `base_sha`. A pass stores `finding` on the item and closes it with `closed_by` naming the judge run, with no `owner` obstacle and no hand action. A `not-done` verdict keeps the item `in-queue` and returns the verifier's findings to the worker.

### 5.4 A fault cleared, and an item whose fault returned

No fault reason declares what ends it, so each clearing by hand is a judgment that the cause is gone.

**Every fault reason declares its clearing fact**, in the table of hold reasons, as one of three:

| The reason declares | Who ends the fault | Example |
| :--- | :--- | :--- |
| **A check and a commit**: the named check is green at a commit that contains the named commit | The drain, the next time it reads the proof record, recording the fact as a trunk obstacle's release does today | A fault raised because the trunk was red |
| **An item**: the named item has landed | The drain, by the `blocked_by` edge | A fault whose detail names a defect that has been filed |
| **Nothing**: the cause is not known when the fault is raised | The design seat diagnoses it | A decision collision |

**A fault with no recorded cause goes to the design seat.** The seat is given the fault's reason and detail, the run's log, the item's earlier faults, and the trunk commits since the run's base. It answers with one of:

- **The cause is a defect in tillr.** It names the defect; the drain files the defect item as in [§5.1](#51-a-hold-answered-with-a-technical-decision), and the fault takes that item as its clearing fact.
- **The cause is in the item**: its words, its size or its candidate. The answer is one of the four forms in [§5.1](#51-a-hold-answered-with-a-technical-decision).
- **The fault judged nothing and the step can simply run again.** The seat says why, and the drain runs the step. This is the answer for a landing that stopped because the trunk moved while it was being prepared.

**A fault that returns goes back to the design seat.** When the same reason and detail are recorded on the same item after its clearing fact held, the diagnosis was wrong. The seat is given both occurrences and the clearing fact. One return is the trigger, because it is new evidence about the diagnosis and not a count of tries.

**Nobody closes an item and files it again to get away from a fault.** Under [principle 9](./landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work) the fault is the thing to fix: the item waits on the defect item, and the defect item is dispatched first.

`verify-triage` holds on a repeated finding ([the drain's reference](../../tools/tillr-drain/README.md#a-refused-run-goes-back-to-its-worker)), and [rule 7](./unattended-drain.md#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) sends that hold to the item's designer step. This design keeps both.

### 5.5 A stuck state cleared by hand

Examples: a proof worktree that is missing from disk and still locked in Git's registration, a loop that has closed its store and not exited, an item that no command can release from blocked, and an item that waits on a design run that has died.

Each is a state a tillr program put itself in and has no transition out of. By [principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act) that is a defect in the program, and no seat's judgment is needed to see it.

- **The program that finds the state files the defect.** The drain checks its own invariants when it starts and when it settles a run: every registered worktree it owns exists, every lock it reads has a live holder, every run an item waits on is live or has an ending. Each violation files one deduplicated defect item carrying the state as found. The keeper, the process that starts the loop, does the same for a loop that has closed its store and is still running.
- **What waits on the state waits on the defect item.**
- **Whether the program also repairs the state it found is [OQ-OD1](#OQ-OD1).** Removing a dead holder's lock at start is either a designed transition or a remedy that covers a defect, and the owner's rulings can be read both ways.

A stuck state that stops the whole loop is a fault of the kind [rule 5 of the unattended-drain design](./unattended-drain.md#65-rule-5-one-items-fault-holds-that-item-and-nothing-else) removes. This design does not restate that rule.

### 5.6 A blocking defect moved to the front

**The drain dispatches an item ahead of everything that waits on it.** An item's place for dispatch is the earliest of its own place and the places of the items that wait on it, through any number of `blocked_by` edges. The stored order is not rewritten, so what the owner or a filing placed stays where it was put, and the inherited place ends when the waiters are released.

- **Express standing is inherited the same way.** A defect that an express item waits on may run on an express lane.
- **A defect that stops the loop or leaves the trunk red goes first of all.** That is the owner's standing instruction, recorded as the second rule of [the roadmap's ordering basis](../tillr/roadmap.md#ordering-basis).
- **The drain is given** the `blocked_by` edges, which kinds 1, 4 and 5 write when they file a defect.

No seat reorders the queue. Any change of order that is not derived from a recorded wait is the owner's.

### 5.7 Ready work filed when no item waited for a lane

When a lane is idle with nothing to take, work comes from four sources, and each source files for itself:

| Source of the work | Who files it |
| :--- | :--- |
| A decided design whose build is not in the queue | The mechanism of [the queue mirrors the designs](./tillr-queue-mirrors-designs.md) |
| A defect a worker, a fault or a program found | The program that found it ([§5.1](#51-a-hold-answered-with-a-technical-decision), [§5.4](#54-a-fault-cleared-and-an-item-whose-fault-returned), [§5.5](#55-a-stuck-state-cleared-by-hand)) |
| The parts of a split | The drain, from the split statement |
| The owner's new words | The operator files them verbatim. This is relaying, not deciding, and it stays the operator's ([§7](#7-what-the-operator-still-does)) |

When those four are empty, no ordered work remains. What tillr does then is [OQ-OD2](#OQ-OD2).

A lane that is idle while a ready item waits is a different matter: it is a dispatch defect, found by the invariant check of [§5.5](#55-a-stuck-state-cleared-by-hand) and filed as one.

## 6. What is the owner's

A taker hands a decision to the owner only when it is one of these. The list is the designer's reading of his recorded rulings, and he may change it.

1. **Changing what he asked for.** Weakening, narrowing or dropping his words, a criterion or a required part ([rule P1 of the split design](./tillr-item-split.md#4-the-rules-a-split-keeps)).
2. **Choosing between two of his own statements** that cannot both hold, or overturning a ledger row that quotes him.
3. **The order of work**, except the order derived from a recorded wait ([§5.6](#56-a-blocking-defect-moved-to-the-front)).
4. **What work is worth doing** when nothing he ordered remains ([OQ-OD2](#OQ-OD2)).
5. **Money and access outside the repository**: a subscription, a credential, a spending limit, a host setting.
6. **An act that cannot be undone**: deleting kept work, rewriting the trunk's history, publishing outside the repository.
7. **Which model fills a seat**, and whether a seat exists.

Not on the list, and so never his: a technical reading of the tree, a division of work into parts, a diagnosis of a fault, a judgment that the trunk already satisfies an item, and the order in which a blocker and its waiters run.

## 7. What the operator still does

- **Relays the owner's words** into the queue, verbatim.
- **Watches**, and files a defect for a fault it sees that no program filed. A fault found that way is also a missing check in [§5.5](#55-a-stuck-state-cleared-by-hand), and the defect says so.
- **Starts and stops the drain** at the owner's word.
- **May still run any command.** Nothing here removes a command or refuses the operator. [Principle 14](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran) keeps ordinary tools free to use.

No hold reason reserves a decision for the operator. Under `D-UD7` a reason whose mover is the operator must declare the decision it reserves, and after this design no reason has one to declare, so the table ends with no operator rows.

## 8. Failure and edge cases

| Case | What happens |
| :--- | :--- |
| The design seat's run dies or ends without an answer | The route for a design run's ending applies ([`D-JL9`](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9)): it goes back to the design seat by how the run ended, never by a count, and reaches the owner only by an objection that names him |
| The design seat's subscription is exhausted | The designs stop until it returns, which the owner accepted for now ([`OQ-UD3`](./unattended-drain.md#OQ-UD3)). Holds waiting on the seat name it as their mover and stay visible |
| A defect item is itself held | Its hold names its own mover by the same rules. What waits on it keeps waiting, and the queue shows the chain |
| Two workers name the same broken tool | One defect item; the second item joins the waiters |
| A worker names a tool that is sound | The defect item's worker ends with `definition`; the design seat answers, and the answer goes onto every item that waited |
| The closing check and a worker disagree about "already landed" | The check decides. The item goes on as work with the unmet criteria named |
| A fault's clearing fact holds and the fault is recorded again | It goes to the design seat with both occurrences ([§5.4](#54-a-fault-cleared-and-an-item-whose-fault-returned)) |
| Two items each wait on the other | The invariant check files it as a defect; neither inherits a place from the other |
| A decision the design seat was given is one of [§6](#6-what-is-the-owners) | It objects naming the owner, with the question and its options |
| Holds and faults already in the store on the day a slice lands | Each is read by the new rule at its next settlement. A `fence` hold is released as today. Nothing is migrated in bulk |
| The operator takes a hand action of a kind whose slice has landed | It is recorded with its kind, and [tillr-watch](./tillr-watch.md) alerts on it. The alert notifies and does nothing else |

## 9. How this fits what exists

### 9.1 The split design

Kind 2 is that design. Kind 3 reuses its closing check on an item that was never split, and slice 10 here follows its slice 7 for that reason. Rule T5 is its rule P1. The design seat's four answers in [§5.1](#51-a-hold-answered-with-a-technical-decision) include its split statement, so a split may be reached from any obstacle and from a fault's diagnosis, which that design allows: its design seat may write a statement "whenever an item is in its hands for another reason".

### 9.2 The request-rewrite design

[The request-amendment design](./tillr-request-amendment.md) owns this subject. This design assumes one boundary and decides nothing on that design's side of it:

- **That design owns** when a request is amended, who writes the amendment, how the owner's original words are kept, and what happens to the item's candidate.
- **This design only routes to it.** When the design seat finds a request's words wrong about the tree, its answer is "a rewrite", and that design says what that is.

If that design gives the rewrite to a different seat, the row in [§5.1](#51-a-hold-answered-with-a-technical-decision) changes to name it and nothing else here moves.

### 9.3 A held state names a mover

[Principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act) requires a mover that can still act. The operator is a mover that acts only at a check-in, and not at all when its session or its subscription is gone. Every hold this design touches gets a mover that runs inside the drain: the design seat, a named defect item, a clearing fact, or the owner.

### 9.4 The standing rulings

- **No fallbacks** ([`D-FB1`](./tillr-fallback-inventory.md#decision-ledger), the owner: "If something fails, you fix it."). No taker here tries a second way when a first fails. A fault's cause is diagnosed and fixed as an item, and a step runs again only when the design seat states that the fault judged nothing.
- **No count of attempts and no time limit fails work** ([`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)). Rule T4.
- **Tillr-watch applies no remedy** ([its standing ruling](./tillr-watch.md#32-standing-ruling-an-alert-never-fails-benches-or-caps-work-d-tw2), and the owner: "We don't cover issues. We use this to find the issues and fix them."). Every action in this design is taken by the drain, a seat or an item's worker. Tillr-watch only alerts, in slice 12.

## 10. What this does not do

- **It does not add a seat.** Unless the owner rules otherwise in [OQ-OD3](#OQ-OD3).
- **It does not remove or refuse any operator command.**
- **It does not let any taker close an item on a reason alone.**
- **It does not change the gate, the lander, the verifier's bar or the split design.**
- **It does not decide what a request rewrite is.**
- **It does not name commands, fields, events or store tables** beyond those that exist today. Those belong to the implementation plan.

## 11. Alternatives considered, and risks

| Alternative | Verdict |
| :--- | :--- |
| A new operator seat inside tillr that takes every hold the operator took | Rejected by the designer: it moves the queue's narrow point without removing it, and most of these decisions need no judgment once the fact that decides them is recorded. Offered to the owner as option B of [OQ-OD3](#OQ-OD3) |
| The drain closes an item when another landing touches the same paths or has a similar title | Rejected: the wrong close described in [§5.3](#53-an-item-whose-fix-already-landed) is this judgment made by hand |
| Clear a fault after it has stayed quiet for some time, or retry a set number of times | Rejected: the owner ruled out counts and time limits |
| Tillr-watch clears faults and moves blockers | Rejected: the owner ruled that it applies no remedy |
| Keep the operator as the mover and shorten the check-in | Rejected by the owner's words in [§1](#1-the-ruling) |

| Risk | Mitigation |
| :--- | :--- |
| The design seat becomes the narrow point | Three kinds need no seat at all (3 for fixers, 6, and 4 when the clearing fact is recorded). How long holds wait on the seat is visible in the queue's reasons. [OQ-OD3](#OQ-OD3) lets the owner choose a different seat |
| A defect item is filed for a tool that is sound | Its own worker tests the claim; the cost is one run |
| A wrong diagnosis sends an item to wait on the wrong defect | The fault returns after the defect lands and goes back to the seat with both records |
| The closing check passes work that does not satisfy the item | It is a verifier judging written criteria against a tree, the same bar as any landing. It replaces a reason typed by hand |
| Hand actions continue out of habit | Each is recorded with its kind and alerted on after its slice lands |

## 12. The build, in slices that each land alone

Each slice is filed as its own request and lands as its own commit. Each is useful on the trunk if no later slice lands, and none makes the trunk worse while the rest are queued. The slices and their order are the designer's decision (`D-OD12`).

| Slice | What lands | Why it is useful alone | Follows |
| ---: | :--- | :--- | :--- |
| 1 | Every close, finish, retry, placement, fault clearing and filing records who did it and, for the operator, which of the seven kinds it was. The queue's status shows the day's hand actions by kind | The three gaps of [§2](#2-the-day-measured) close, and this design's effect can be measured | — |
| 2 | A `tooling` obstacle on a filed request files one deduplicated defect item, and the held item waits on it | A broken tool is repaired without a check-in, and the brief's statement to the worker becomes true | — |
| 3 | A `definition` or `seat` obstacle on a filed request goes to the design seat. An answer on the item is recorded and quoted in every later brief for it | The technical answers of kind 1 are given inside the drain | — |
| 4 | An item is dispatched ahead of everything that waits on it, and inherits express standing from its waiters | A blocking defect runs first with no placement by hand | — |
| 5 | Every fault reason declares its clearing fact or declares none, and a test fails for a reason that does neither. The item page shows the fact. Nothing is cleared by it yet | A reader of a fault sees what would end it | — |
| 6 | The drain ends a fault whose clearing fact is a green check at a containing commit, and records the fact | Faults caused by a red trunk end when it is green | 5 |
| 7 | The drain ends a fault whose clearing fact is a landed item | An item held by a filed defect resumes when the defect lands | 5 |
| 8 | A fault with no declared cause, and a fault that returns after its clearing fact held, goes to the design seat with the records of [§5.4](#54-a-fault-cleared-and-an-item-whose-fault-returned) | Faults are diagnosed inside the drain | 3, 5 |
| 9 | A fixer is finished by the drain when every check it was filed to repair is green at the trunk's tip | A fixer whose repair landed elsewhere stops occupying a lane | — |
| 10 | A worker or the design seat may state that the trunk already satisfies an item, naming the commits. The closing check decides, and a pass finishes the item as resolved elsewhere | No item is set to landed on a reason typed by hand | Slice 7 of [the split design](./tillr-item-split.md#14-the-build-in-slices-that-each-land-alone) |
| 11 | The drain checks its own invariants at start and at each settlement, and the keeper checks for a loop that closed its store and still runs. Each violation files one deduplicated defect item. Nothing is repaired | A stuck state is found and filed when it happens, under either answer to [OQ-OD1](#OQ-OD1) | — |
| 12 | A tillr-watch rule alerts on a hand action of a kind whose slice has landed. It notifies and does nothing else | A taker that failed is noticed | 1 |
| 13 | A program repairs the stuck states it owns when it starts, one state per request. Blocked on [OQ-OD1](#OQ-OD1), and built only under its option A | A stuck state no longer waits for a person | 11 |

Kind 2 has no slice here: it is slices 8 and 9 of the split design. Kind 7's first source is the build of [the queue mirrors the designs](./tillr-queue-mirrors-designs.md), and its last is [OQ-OD2](#OQ-OD2).

**What done looks like.** The status of slice 1 shows no hand action of kinds 1 to 6; every hold names the design seat, a defect item, a clearing fact or the owner; and no item is set to landed or closed with `lifecycle_by: operator` unless the reason quotes the owner.

## Open Questions

1. 💬 **OQ-OD1: May a tillr program repair its own stuck state when it starts?**

   The drain starts and finds a proof worktree that Git still lists as locked, though its directory is gone and the process that locked it is dead. Today the operator removes the registration by hand. Under any answer the drain files a defect item for how the state arose.

   - **A — It repairs what it owns.** The drain removes the dead registration, records what it found and did, and carries on. You see the defect item and a running drain.
   - **B — It stops and says so.** The drain files the defect, raises an alarm and does not use that worktree until a landed fix removes the cause. You see the proof stopped until then.

   <!-- vantage: question id=OQ-OD1 leaning="A, it repairs what it owns: a lock whose holder is provably dead is a state with an exit the program should own, and the defect is still filed, so nothing is covered." -->

   _Leaning:_ A, it repairs what it owns: a lock whose holder is provably dead is a state with an exit the program should own, and the defect is still filed, so nothing is covered.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-OD2: What does tillr do when no work you ordered remains?**

   Every lane is idle. Every decided design has its build filed, no defect is open, and the remaining roadmap entries wait on your rulings. Today the operator chooses something and files it.

   - **A — It idles and says why.** The status reads "no ordered work: N entries wait on your rulings", with links to them. Nothing is spent.
   - **B — The design seat prepares the next entry.** It writes or tightens the design for the next roadmap entry, up to its open questions. You find more designs waiting for you, and design runs spent.
   - **C — It takes from a list you keep.** Lanes take items from a standing list of approved filler work.

   <!-- vantage: question id=OQ-OD2 leaning="A, it idles and says why: what is worth doing next is yours to choose, and an idle drain that names the rulings it waits on is an honest report." -->

   _Leaning:_ A, it idles and says why: what is worth doing next is yours to choose, and an idle drain that names the rulings it waits on is an honest report.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-OD3: Which seat takes the judgments the operator made?**

   A worker on a filed request stops and asks whether SQLite's sidecar files count as a write to the store. Today the hold waits for the operator.

   - **A — The design seat.** The same seat that answers obstacles on document-backed items and writes splits. You pay for more design runs on that seat's model.
   - **B — A new seat for these decisions.** You name a model for it. Design runs stay for designs, and one more seat can be wrong about the bar.

   <!-- vantage: question id=OQ-OD3 leaning="A, the design seat: three accepted designs send these decisions there, and an answer that changes how a request is read is writing the bar, which is that seat's work." -->

   _Leaning:_ A, the design seat: three accepted designs send these decisions there, and an answer that changes how a request is read is writing the bar, which is that seat's work.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

**Who decided each row.** `R-OD1` quotes the owner. Every other row is the designer's decision, made without him, and he may overturn it.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| R-OD1 | **Owner ruling.** A problem the operator could solve without the owner escalates to a similarly responsible agent, process or ticket inside tillr, and not to the operator. His words are quoted in full in [§1](#1-the-ruling) | 2026-11-21, owner | [§1](#1-the-ruling) | — |
| D-OD1 | **Designer's decision; the owner may overturn it.** Three takers, chosen by what decides the matter: the drain for a recorded fact, the design seat for a judgment of an item's words or shape, a defect item where tillr is wrong. **Rejected:** one operator seat inside tillr for all of it | 2026-11-21, designer | [§4](#4-the-rules) | — |
| D-OD2 | **Designer's decision; the owner may overturn it.** No rule here counts or times, and no taker changes the bar: an item leaves the queue without landing only by a closing check that passes or by the owner's words. On `planned-build@v1`, `verify` passing a worker's `no_change` finding ([`D-JL34`](./tillr-judge-and-lifecycle.md#421-an-investigation-that-finds-nothing-to-change-ends-inside-tillr-d-jl34)) is a passing closing check that closes (`closed`) an investigation item whose reproductions show nothing in the repository needs changing | 2026-11-21, designer; amended 2026-11-22 | [§4](#4-the-rules), [§5.3](#53-an-item-whose-fix-already-landed) | The `no_change` close only |
| D-OD3 | **Designer's decision; the owner may overturn it.** A `definition` or `seat` obstacle on a filed request goes to the design seat, which answers on the item, with a split statement, with a rewrite, or by naming the owner | 2026-11-21, designer | [§5.1](#51-a-hold-answered-with-a-technical-decision) | — |
| D-OD4 | **Designer's decision; the owner may overturn it.** A `tooling` obstacle files one deduplicated defect item and the held item waits on it. The worker's claim is tested by the defect item's own worker | 2026-11-21, designer | [§5.1](#51-a-hold-answered-with-a-technical-decision) | For a document-backed item only; not for a filed request |
| D-OD5 | **Designer's decision; the owner may overturn it.** Kind 2 belongs to the split design unchanged: the item stays open as a parent and a request is never narrowed by a taker | 2026-11-21, designer | [§5.2](#52-an-item-refused-on-unrelated-findings) | — |
| D-OD6 | **Designer's decision; the owner may overturn it.** An item is finished without a landing of its own only by a closing check that passes. For a fixer the drain's run of its named checks is that check. When an investigation on `planned-build@v1` establishes with reproductions that the trunk never needed a change, the worker ends with `no_change` (`cause`, `evidence`, `why_no_change`), `verify` checks the finding against the item's criteria, and a pass stores `finding` on `tillr item show` and closes the item (`state: "closed"`, `closed_by` naming the judge run) without an `owner` obstacle, `tillr-watch` hold alert, or operator close ([`D-JL34`](./tillr-judge-and-lifecycle.md#421-an-investigation-that-finds-nothing-to-change-ends-inside-tillr-d-jl34)). **Rejected:** closing on a similar title, a typed reason, or an unjudged worker claim | 2026-11-21, designer; amended 2026-11-22 | [§5.3](#53-an-item-whose-fix-already-landed) | The `no_change` close only |
| D-OD7 | **Designer's decision; the owner may overturn it.** Every fault reason declares its clearing fact. The drain ends a fault whose fact holds; the design seat diagnoses a fault with no declared cause and a fault that returns. Nobody closes and refiles an item to escape a fault | 2026-11-21, designer | [§5.4](#54-a-fault-cleared-and-an-item-whose-fault-returned) | — |
| D-OD8 | **Designer's decision; the owner may overturn it.** A stuck state is a defect in the program that owns it. The program that finds it files the defect item. Whether it also repairs the state is left to the owner in [OQ-OD1](#OQ-OD1) | 2026-11-21, designer | [§5.5](#55-a-stuck-state-cleared-by-hand) | — |
| D-OD9 | **Designer's decision; the owner may overturn it.** An item is dispatched ahead of everything that waits on it and inherits their express standing; the stored order is not rewritten. No seat reorders the queue | 2026-11-21, designer | [§5.6](#56-a-blocking-defect-moved-to-the-front) | — |
| D-OD10 | **Designer's decision; the owner may overturn it.** Each source of ready work files its own. What happens when none remains is left to the owner in [OQ-OD2](#OQ-OD2) | 2026-11-21, designer | [§5.7](#57-ready-work-filed-when-no-item-waited-for-a-lane) | — |
| D-OD11 | **Designer's decision; the owner may overturn it.** The seven things that are the owner's, and the statement that no hold reason reserves a decision for the operator. The operator keeps relaying, watching, and starting and stopping the drain, and no command is removed | 2026-11-21, designer | [§6](#6-what-is-the-owners), [§7](#7-what-the-operator-still-does) | — |
| D-OD12 | **Designer's decision; the owner may overturn it.** The build is thirteen slices that each land alone, in the order of [§12](#12-the-build-in-slices-that-each-land-alone): the record first, then each taker | 2026-11-21, designer | [§12](#12-the-build-in-slices-that-each-land-alone) | — |
