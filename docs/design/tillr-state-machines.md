---
title: "Declared state machines for tillr"
status: in-review
stage: DESIGN
tags: [tillr, state, store, design]
summary: "Eight things in tillr change state: item, run, lane, candidate, hold, landing, trunk proof and backend closure. Each gets one declared machine, one transition writer and database refusals generated from the declaration, so a contradictory state is refused where it would be written. Nothing is built, and four questions wait on the owner."
next: "The owner rules OQ-TSM1 to OQ-TSM4. Step 1 of the build order waits on none of them."
---

# Declared state machines for tillr

**Status:** 2026-11-24. Nothing is built: `rg -n 'RAISE\(ABORT\)' tools -g '*.go'` finds nothing, and `rg -n 'TestDeclaredItemMachineRejectsUndeclaredSQLMove' tools` finds nothing. The inventory below was checked against `tools/` on 2026-11-24.

> **In short.** The same state is represented several ways, and nothing refuses a move between two valid labels that no rule allows. Each thing that changes state gets one declaration of its legal moves, one function that makes them, and SQLite constraints generated from that declaration, so an illegal move cannot be committed by any process.

**Why it matters.** A stored state, a lease, an event stream and a read-time projection can each say something different about one item, and the displays then show contradictions such as an item that is blocked and running at once.

**The shape.** Eight machines, a declaration kernel shared by all of them, a reconciler that names impossible combinations, and one display partition every reader uses.

**Cost.** Every writer of a state column moves behind a transition function. Existing rows are adjudicated explicitly before a machine is switched on.

**Needs your ruling:** [OQ-TSM1](#OQ-TSM1), [OQ-TSM2](#OQ-TSM2), [OQ-TSM3](#OQ-TSM3), [OQ-TSM4](#OQ-TSM4).

**Reads with:** [landing principles](landing-principles.md), which this design keeps: a landing binds evidence about trees, not patch identity, and proof binds a revision, not a mutable branch name.

## Scope

This proposal changes neither the store nor behavior until its build order is carried out. It decides the shape of the machines, the refusals and the adoption path. It does not decide the item definition's vocabulary, which stays the selected definition's.

## What stores state today, and what derives it

A lease, timestamp or reason can influence a decision without being a second lifecycle column. The table separates what is stored and written from what is computed at read time.

| Domain | Stored authority and writes | Read-time state |
|---|---|---|
| Item | `items.state` is written in `tools/tillr-item/item` by `ApplyWithReceipt`, `SetStateWithReceipt`, `Close` and `Reopen`. `lifecycle_policy.go` checks definition membership and move legality, including close. The Git-landing definition's vocabulary is `not-started`, `blocked`, `in-queue`, `landed`, `resolved-elsewhere`, `closed`; it is an item definition, not the publication machine. `item_criteria.state` is written by `replaceCriteria`, `RecordVerdict` and `WithdrawVerdicts`; judgments and resets are distinct operations. | `ProjectItemState` in `tools/tillr-view/serveread.go` combines disposition, queue and candidate evidence, live execution, dependency edges and questions. `boardCountsOf` distinguishes that projection from dispatchability. Drain planning reconstructs waiting and available work independently. |
| Run | `runs.state` has one writer, `Apply` in `tools/tillr-run/run/apply.go`, which records `exited`, `failed-to-start`, `timed-out` or `paused`. `end_cause` adds provenance, not another transition; a normal exit with a nonzero code is still an exit. `quota_pauses.state` distinguishes `requested` and `waiting`. `read_only_runs.settled` and the workflow's `node_runs` track separate settlement boundaries. | Live work is inferred from reservations and execution evidence, not a live `runs` row; a paused record can coexist with newer resume evidence. Workflow continuation is calculated from the graph revision and node receipts. |
| Lane | `lanes.state` stores `idle` or `leased`. `rg -c "UPDATE lanes SET\|INSERT (OR \w+ )?INTO lanes" tools -g '*.go' -g '!*_test.go'` reports 26 sites in 13 files. The run package writes it in `Apply`, `ClaimLease` and `freeLane`. The drain writes it in, among others, `stageComment`, `replaceFailedDesigner`, `claimCandidateRelease`, `clearCandidateRelease`, `registerLanePools`, `reclaimOffRotationLease`, `reclaimOrphanedResume`, `reclaimStuckLease`, `reapDeadLoopRuns`, `applyPlanRecorded`, `releaseExamination`, `markExamined`, `Examine`, `SetAside`, `releaseRestartExaminations`, `supersedeCandidate` and `retractClaim`. | `lanestate.Read` reconstructs reservation and keep reasons; the view's `laneWorkState` projects idle, running or settling from lease, return and resume evidence. `no-lane` is an allocation result, not a state of the item or lane. |
| Candidate | A candidate has no dedicated state writer. The land schema declares `candidates` and `landing_attempts`, and nothing writes them. Current evidence includes lane and ref retention, node returns and gate judgments (`recordGateJudgment`); these are not substitutes for a candidate lifecycle. | The drain finds unlanded work from run and lane evidence and classifies retained work, examination, no-op, unchecked, gate result and readiness; `lanestate.KeepReason` projects retention and the view separately reasons about held lanes. |
| Hold | No common state column or writer. Sources include obstacle and fault event streams, owner questions, conflicts, grant expiry and gate-stall events. Producers include `recordConflict`, `holdItemFault`, `recordGateStallHold`, `holdBehindDefect` and `holdGateNoVerdict`. | `obstacles.Read`, `faults.Read`, `patchmoves.Pending`, and the grant and backend readers reconstruct active causes and remedies. The planner combines them with allocation, dependency and trunk facts; the view's waiting sections and the drain's `why` are consumers, not authorities. |
| Landing | `tools/tillr-land/land/landing.go` has one receipt insert, `recordLanding`, for `landings.result` (`landed`, `squash-conflict`, `refused`). `landing_claims` has one insert, `claimLanding`, and three deletes: `recordPublished`, `abandonClaim` and `dropStaleClaims`. A claim's presence means in flight, not complete. | Publication recovery compares the claim to Git; item completion is inferred separately. Landing history can include prerequisite commits, not just deliverables. |
| Trunk proof | Evidence is bound to a revision in Git and in proof records, not held in a single SQLite state column. | The drain's `readGreenProof` and `proofPastRed` compare proof, failure and ancestry; heavy status reports freshness independently of a hold's saved reason. An old failure does not describe a later proved revision. |
| Backend closure | `backend_windows.status` stores `rejected`, written by one upsert, `recordBackendWindow` in `tools/tillr-drain/drain/quota_window.go`. `readBackendWindows` computes whether a recorded window still closes the backend at the read clock, so no writer records expiry. | `describeBackendHold` and backend availability combine closure evidence, backend selection and time to decide unavailable, waiting or eligible. Expiry is not worker completion. |

## Proposed contract

The eight domain machines are not eight versions of item status. A machine's identity includes the generation that makes stale evidence distinguishable: run attempt, lane lease epoch, candidate tree and base, hold cause and source generation, publication claim, trunk tip and proof epoch, or backend eligibility window. Secondary lifecycles such as criterion verdicts, comment replies and node outcomes can use the same declaration kernel independently; none assigns the containing item's state by implication.

| Machine | Legal moves (all unlisted moves refused) |
|---|---|
| Item | Keep the selected definition's edges. Additionally require no incompatible active execution at any move into a blocked or terminal disposition. A graph advance is a node transition, not an item reopen. |
| Run | Reserved to live or cancelled; live to returned, interrupted or stopping; stopping to returned or interrupted; interrupted to paused only after the allocation settles. A resume reserves a new attempt linked to the old one. |
| Lane | Free to reserved; reserved to running or free; running to settling; settling to retained or free; retained to examining or reserved; examining to retained or free. Require the expected owner and epoch on release. |
| Candidate | Captured to checking or superseded; checking to eligible, rejected or awaiting-evidence; awaiting-evidence to checking or superseded; rejected to superseded; eligible to publishing or superseded; publishing to published or eligible after publication is proved absent. A changed tree creates a new identity. |
| Hold | Active to cleared or superseded. Insert only with a typed cause, mover and release predicate. An item may have several independent holds; clearing one grants no general permission. |
| Landing | Prepared to publishing or abandoned; publishing to recorded or abandoned only after Git reconciliation. Recorded receipts are immutable and typed by completion scope. |
| Trunk proof | Unproved to proving; proving to proved, failed or unproved on interruption; failed to proving; a new tip creates an unproved generation. Accept a proof only for its bound revision. |
| Backend closure | Open to closed on accepted refusal evidence; closed to open on expiry, validated recovery or authorized clear. Never reopen a newer window using an old timer. |

**The declaration and its one writer.**

- A declaration provides initial states, named edges, guards and terminal states.
- One transition function per machine consumes the declaration, checks the expected state and generation under a write transaction, and emits evidence in the same transaction. Coordinators may compose these functions, not bypass them.
- SQLite `NOT NULL`, `CHECK` and insert and update triggers are generated from the same declaration. Triggers use `RAISE(ABORT)` for undeclared edges and disallow delete and reinsert as a transition. A process-local check alone cannot protect the store from a second process.
- Exclusive reservations and publication claims use partial unique indexes.

**Failure.**

- A refusal rolls back the whole transaction. One refusal diagnostic is persisted after the rollback, and it never claims the rejected transaction committed.
- A lost compare-and-swap is a stale decision to recompute, not corruption.
- An illegal edge is a typed error; the command exits nonzero, and the drain ends the pass.

**Effects outside SQLite.** Git and process operations are not SQLite transactions: the intent is persisted before the external effect, then settled against evidence and an epoch.

**The reconciler.** It runs before new admission and after interrupted publication recovery. It names impossible tuples and stops new work, without repairing rows or revoking workers blindly. Legitimate in-flight tuples have explicit states and recovery rules. Ordinary transitions check only the identities they change; startup scans or incremental checkpoints must not add a full historical scan per landing.

**One display partition.** Every display and the drain's `why` consume one snapshot partition with one primary disposition per item: terminal, active, settling a candidate, held, ready, or not ready. Within legal tuples, live work outranks an allocation question, and a retained candidate uses its own settlement path before fresh allocation. Secondary evidence can be displayed but cannot emit a second primary disposition. An invalid tuple gets a diagnostic, not a precedence rule that hides corruption. The same action guards offer a remedy and execute it.

## What each machine refuses

Eight contradictory states the current tree can show, each with the rule that is missing and the refusal that replaces it.

| Contradiction | Missing rule and proposed refusal |
|---|---|
| An item is blocked for design and has a live run | Item and run allocation lack a joint invariant. Refuse blocking with a live incompatible run, and refuse starting one on a blocked item. If design work is permitted, grant it a distinct declared phase, not a second label. |
| An item is running, reports no lane, and holds its own commit | Lane and candidate settlement is treated as fresh allocation. Refuse a fresh dispatch while a retained candidate owns the continuation; route it to settlement and show only that disposition. |
| A landing exists but the held item offers close | Landing scope, item state and remedy eligibility diverge. A final receipt settles disposition and supersedes incompatible holds in the same transaction; a prerequisite receipt does not close the item. Reject close using the same predicate as remedy rendering. |
| The trunk is proved again but a trunk-broken hold remains | The hold lacks a revision-bound release. Refuse creation against obsolete proof evidence; the proof update clears holds whose predicates it satisfies, never those for unrelated failures. |
| A design answer lands before the build | Item and landing lack completion scope and a graph cursor binding. Refuse a terminal item transition on a prerequisite publication; record the receipt and advance the bound node once, without restarting planning. |
| A restart records a run as paused but keeps its lane and lease | Run and lane lack a settlement boundary. Refuse paused with an active execution lease; record stopping or interrupted until death is confirmed, then release or explicitly retain a candidate, atomically with settlement. |
| A refusal for writing outside the allowed paths is sent to design repeatedly | The hold lacks a mover and a changed-input predicate. Record a hold that awaits a decision on the allowed paths; refuse the same planner or design input until an authorized revision, a cancellation or a new plan inside the allowed paths. |
| A verify step returns without a verdict and restarts | Run and candidate confuse missing evidence with a negative judgment. Refuse a verdict-based advance or refusal without a matching receipt; retain the candidate and retry or escalate only the incomplete verification node. |

## Open Questions

1. 💬 **OQ-TSM1: Who may widen an item's allowed paths?**

   A worker on an item finishes with a change outside the item's allowed paths, and the gate refuses it. Today nothing records who may widen the paths, so the item can be sent back to design again with the same input. The machine records a hold that awaits that decision, and someone has to be able to release it. Neither option lets a retry silently widen the paths.

   - **A — The operator.** The hold stays until the operator records a new revision of the allowed paths. Every widening is a person's decision.
   - **B — A design seat, by explicit delegation.** A design run with a separate scope grant may record the revision. Items move without a person, and a second grant has to be issued and checked.

   <!-- vantage: question id=OQ-TSM1 leaning="A (The operator) — require the operator to authorize a new revision of the allowed paths." -->

   _Leaning:_ A (The operator) — require the operator to authorize a new revision of the allowed paths.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-TSM2: What can complete an item with intermediate publications?**

   An item's graph lands a design answer first and the build later. Today the first landing can read as the item being finished. The machine needs a rule for which publication closes the item, and it must not infer intent from the number of commits.

   - **A — Only the graph's designated deliverable.** The first landing is recorded as a prerequisite and the item stays open until the deliverable lands.
   - **B — One publication per item.** Any prerequisite publication has to be made its own item, so every landing closes the item it belongs to. People write more items.

   <!-- vantage: question id=OQ-TSM2 leaning="A (Only the graph's designated deliverable) — only the graph's explicit deliverable publication completes a landing item." -->

   _Leaning:_ A (Only the graph's designated deliverable) — only the graph's explicit deliverable publication completes a landing item.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-TSM3: May unrelated work continue after a reconciliation fault?**

   The reconciler finds a combination of rows that no machine allows, for one item. Today nothing stops the loop from dispatching and landing other items while that fault stands. Running processes need safe settlement in either case.

   - **A — Stop everything.** Admission and publication stop for every item until a person adjudicates the fault. One bad row idles the whole queue.
   - **B — Quarantine and proceed.** Work that is proved independent of the faulty rows continues. The proof of independence has to be built and trusted.

   <!-- vantage: question id=OQ-TSM3 leaning="A (Stop everything) — stop admission and publication until adjudicated." -->

   _Leaning:_ A (Stop everything) — stop admission and publication until adjudicated.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-TSM4: What should happen to existing rows that map to no declared state?**

   A machine is about to be switched on for a store that already holds rows, and the dry run reports some that fit no declared state. Neither option silently repairs or drops history.

   - **A — Refuse to apply.** The machine stays off until a person has adjudicated every such row.
   - **B — Quarantine them.** The rows are exported read-only and given no dispatch rights, and the machine is switched on for the rest.

   <!-- vantage: question id=OQ-TSM4 leaning="A (Refuse to apply) — refuse apply until every row is adjudicated." -->

   _Leaning:_ A (Refuse to apply) — refuse apply until every row is adjudicated.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

Every row is the designer's proposal; the owner may overturn it. Nothing is built.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D1 | Declarative tables and generated constraints, not a runtime state-machine library: a process-local check cannot protect SQLite from a second process | 2026-11-20, designer | [Proposed contract](#proposed-contract) | — |
| D2 | Eight machines, each with one stored state and one transition writer. Lane, hold, publication and backend windows have independent identities and recovery, so they are machines of their own. Admission is a derived predicate, not a machine | 2026-11-20, designer | [Proposed contract](#proposed-contract) | — |
| D3 | Verdict evidence is immutable, and a verdict is not a synonym for item disposition. Criterion state stays distinct from the verdict receipt | 2026-11-20, designer | [Proposed contract](#proposed-contract) | — |
| D4 | A candidate has its own identity, and a run has one liveness contract. Rows already in `candidates` and `landing_attempts` stay as history and drive no work | 2026-11-20, designer | [Proposed contract](#proposed-contract) | — |
| D5 | A receipt and a membership are separate: the presence of a queue receipt does not make a candidate landable | 2026-11-20, designer | [Proposed contract](#proposed-contract) | — |
| D6 | Compare-and-swap, generated database refusal, atomic allocation, cross-table checks and a single display partition. No guard is installed over rows that have not been adjudicated | 2026-11-20, designer | [Proposed contract](#proposed-contract) | — |
| D7 | Typed refusal, rollback, one diagnostic and a reconciler that stops and does not repair | 2026-11-20, designer | [Proposed contract](#proposed-contract) | — |
| D8 | Activation has an explicit dry run and an explicit apply. The dry run reports each existing tuple and its evidence; apply checks that the inspected snapshot still matches, records each adjudication and refuses unmapped rows. Terminal states are never inferred when a store is opened for reading | 2026-11-20, designer | [Build order](#build-order) | — |
| D9 | Adoption is incremental, one machine at a time, with no permanent second registry of states | 2026-11-20, designer | [Build order](#build-order) | — |

## Build order

1. Land a standalone declaration-to-SQL slice for the existing item definition on a fresh fixture database. Keep the current runtime and existing store untouched. `TestDeclaredItemMachineRejectsUndeclaredSQLMove` inserts a valid initial state, performs a legal move, then attempts an unlisted move between two valid labels by direct SQL and asserts SQLite refuses it and leaves the row unchanged. Also test unknown labels and declaration and trigger parity.
2. Add a read-only reconciler and shared snapshot partition with fixture tests for contradictory tuples and legitimate in-flight work. Shadow the partition before changing admission.
3. Unify lane and run allocation and restart settlement under transactional transitions; test stale lease epochs and interrupted launch and reap. Then promote the reconciler to admission refusal once the supported tuples are exhaustively tested.
4. Introduce candidate and publication machines with revision-bound evidence and scoped receipts; test prerequisite publication, a missing verify verdict and a crash after the Git update before the receipt.
5. Introduce typed hold, proof and backend transitions, using generation guards to test obsolete timers and proof recovery. Switch remedies and `why` to the shared partition.
6. Ship explicit adjudication and activation per machine. Remove superseded writers and readers only after replay, refusal and rollback tests pass for that slice. Keep receipts written before activation readable without allowing them to drive work.
