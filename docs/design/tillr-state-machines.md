# Declared state machines for tillr

> I want you to pull out of the archives a document that I think I told you that you could retire, which is the board state machine document about how we need to define valid state machines and transitions. And I think that it's time for us to bring that back here. We seem to be having issues like this, and this seems to be just the thing. So see what we can design out of this.

— Owner, 2026-11-20

> We're clearly representing the same state multiple ways. We don't have a clear state machine for a thing. For all of these things that can transit between states. just like we did for some of the PyFork work, I need you to like find a state machine library that we can define our state machines, only have valid transitions, have verifications that throw exceptions, whatever, fatal logging when you have bad things happening with these state machines, or just make it so it can't even happen.

— Owner, 2026-10-29

## History and scope

The October design and its first slice describe the retired board, not the tillr schema. The later `docs/design/board-state-machine.md` survives in history (`git show 698517e1f^:docs/design/board-state-machine.md`). This proposal changes neither the store nor behavior. It preserves the [landing principles](landing-principles.md): a landing binds evidence about trees, not patch identity; proof binds a revision, not a mutable branch name.

## Inventory at the design baseline

Baseline: `cbb7755e82c43c92cb7ca9b44a4b36a85edf1987`. Counts distinguish physical state-writing functions from callers and immutable receipt producers. A lease, timestamp or reason can influence a decision without being a second lifecycle column.

| Domain | Stored authority and writes | Read-time state |
|---|---|---|
| Item | `items.state`: four writers in `tools/tillr-item/item`: `ApplyWithReceipt`, `SetStateWithReceipt`, `Close`, `Reopen`. `lifecycle_policy.go` checks definition membership and move legality, including close. The git-landing definition vocabulary is `not-started`, `blocked`, `in-queue`, `landed`, `resolved-elsewhere`, `closed`; it is an item definition, not the publication machine. `item_criteria.state`: three writers, `replaceCriteria`, `RecordVerdict`, `WithdrawVerdicts`; judgments and resets are distinct operations. | `tools/tillr-view/serveread.go:ProjectItemState` combines disposition, queue/candidate evidence, live execution, dependency edges and questions. `boardCountsOf` distinguishes that projection from dispatchability. Drain planning reconstructs waiting and available work independently. |
| Run | `runs.state`: one writer, `tools/tillr-run/run/apply.go:Apply`, materializing `exited`, `failed-to-start`, `timed-out` or `paused`. `end_cause` adds provenance, not another transition; normal exit with nonzero code is still an exit. `quota_pauses.state` distinguishes `requested` and `waiting`; paused `Apply` advances it. `read_only_runs.settled` and workflow `node_runs.outcome` track separate settlement boundaries. | Live work is inferred from reservations and execution evidence, not a live `runs` row; a paused record can coexist with newer resume evidence. Workflow continuation is calculated from graph revision and node receipts, including next-node and missing-return cases. |
| Lane | `lanes.state` stores `idle` or `leased`: 21 SQL sites in 20 functions including initialization. Run writers: `Apply`, `ClaimLease`, `freeLane`. Drain writers: `stageComment`, `replaceFailedDesigner`, `claimCandidateRelease`, `clearCandidateRelease`, `registerLanePools`, `reclaimOffRotationLease`, `reclaimOrphanedResume`, `reclaimStuckLease`, `reapDeadLoopRuns` (two sites), `applyPlanRecorded`, `releaseExamination`, `markExamined`, `Examine`, `SetAside`, `releaseRestartExaminations`, `supersedeCandidate`, `retractClaim`. Lease refreshes without state writes are excluded. | `lanestate.Read` reconstructs reservation and keep reasons; view `laneWorkState` projects idle, running or settling from lease, return and resume evidence. `no-lane` is an allocation result, not a state of the item or lane. |
| Candidate | Current candidates have no dedicated state writer. `candidates.state` and `landing_attempts.result` in the land schema are retired history. Current evidence includes lane/ref retention, node returns and gate judgments (`recordGateJudgment`); these are not substitutes for a candidate lifecycle. | Drain finds unlanded work from run and lane evidence, classifies retained/pinned work, examination, no-op, unchecked, gate result and readiness; `lanestate.KeepReason` projects retention and the view separately reasons about held lanes. |
| Hold | No common state column or writer. Sources include obstacle and fault event streams, owner questions, conflicts, grant expiry and gate-stall events. Examples of producer boundaries: `recordConflict`, `holdItemFault`, `recordGateStallHold`, `holdBehindDefect`, `holdGateNoVerdict`. | `obstacles.Read`, `faults.Read`, `patchmoves.Pending`, grant and backend readers reconstruct active causes and remedies. Planner combines them with allocation, dependency and trunk facts; view waiting sections and drain why are consumers, not authorities. |
| Landing | `tools/tillr-land/land/landing.go` has one receipt insert site, `recordLanding`, for `landings.result` (`landed`, `squash-conflict`, `refused`); `landing_claims` has one insert, `claimLanding`, and three delete sites, `recordPublished`, `abandonClaim` and `dropStaleClaims`. Claim presence means in-flight, not completion. | Publication recovery compares the claim to Git; item completion is inferred separately. Landing history can include prerequisite commits, not just deliverables. |
| Trunk proof | Evidence is revision-bound in Git and proof records, not a single SQLite state; zero trunk-proof state-column writers. | Drain `readGreenProof` and `proofPastRed` compare proof, failure and ancestry; heavy status reports freshness independently of a hold's saved reason. An old failure does not describe a later proved revision. |
| Backend closure | `backend_windows.status` stores `rejected`, written by one upsert in `drain/quota_window.go:recordBackendWindow`; `readBackendWindows` computes whether a recorded window still closes the backend at the read clock. No expiry writer is required today; quota pause rows and restart/resume evidence serve different purposes. | `describeBackendHold` and backend availability combine closure evidence, backend selection and time to decide unavailable, waiting or eligible. Expiry is not worker completion. |

## Proposed contract

The eight domain machines below are a designer proposal, not eight versions of item status. A machine's identity includes the generation that makes stale evidence distinguishable: run attempt, lane lease epoch, candidate tree and base, hold cause and source generation, publication claim, trunk tip/proof epoch, or backend eligibility window. Secondary lifecycles such as criterion verdicts, comment replies and node outcomes can use the same declaration kernel independently; none gets to assign the containing item's state by implication.

| Machine | Proposed legal moves (all unlisted moves refused) |
|---|---|
| Item | Keep the selected definition's edges. Additionally require no incompatible active execution at any move into a blocked or terminal disposition. A graph advance is a node transition, not an item reopen. |
| Run | Reserved to live or cancelled; live to returned, interrupted or stopping; stopping to returned or interrupted; interrupted to paused only after the allocation settles. A resume reserves a new attempt linked to the old one. |
| Lane | Free to reserved; reserved to running or free; running to settling; settling to retained or free; retained to examining or reserved; examining to retained or free. Require the expected owner and epoch on release. |
| Candidate | Captured to checking or superseded; checking to eligible, rejected or awaiting-evidence; awaiting-evidence to checking or superseded; rejected to superseded; eligible to publishing or superseded; publishing to published or eligible after publication is proved absent. Changed tree creates a new identity. |
| Hold | Active to cleared or superseded. Insert only with a typed cause, mover and release predicate. An item may have several independent holds; clearing one grants no general permission. |
| Landing | Prepared to publishing or abandoned; publishing to recorded or abandoned only after Git reconciliation. Recorded receipts are immutable and typed by completion scope. |
| Trunk proof | Unproved to proving; proving to proved, failed or unproved on interruption; failed to proving; new tip creates an unproved generation. Accept a proof only for its bound revision. |
| Backend closure | Open to closed on accepted refusal evidence; closed to open on expiry, validated recovery or authorized clear. Never reopen a newer window using an old timer. |

A declaration provides initial states, named edges, guards and terminal states. One transition function per machine consumes the declaration, checks expected state and generation under a write transaction, and emits evidence in the same transaction. Coordinators may compose these functions, not bypass them. Generate SQLite `NOT NULL`, `CHECK` and insert/update triggers from the same declaration; triggers use `RAISE(ABORT)` for undeclared edges and disallow delete/reinsert as a transition. Roll back the whole transaction on refusal. Compare-and-swap loss is a stale decision to recompute, not corruption. An illegal edge is a typed error; the command exits nonzero, and the drain ends the pass. Persist a single refusal diagnostic after rollback; do not claim the rejected transaction committed.

Use partial unique indexes for exclusive reservations and publication claims. Git and process operations are not SQLite transactions: persist the intent before the external effect, then settle against evidence and an epoch. The reconciler runs before new admission and after interrupted publication recovery. It names impossible tuples and stops new work, without repairing rows or revoking workers blindly. Legitimate in-flight tuples have explicit states and recovery rules. Check only changed identities during ordinary transitions; startup scans or incremental checkpoints must not add a full historical scan per landing.

All displays and drain why consume one snapshot partition with one primary disposition per item: terminal, active, settling a candidate, held, ready, or not ready. Within legal tuples, live work outranks an allocation question; a retained candidate uses its own settlement path before fresh allocation. Secondary evidence can be displayed but cannot emit a second primary disposition. Invalid tuples get a diagnostic, not a precedence rule that hides corruption. Use the same action guards to offer remedies and execute them.

## Refusals for the reported contradictions

| Operator observation, 2026-11-20 | Missing rule and proposed refusal |
|---|---|
| Blocked for design and live | Item/run allocation lacked a joint invariant. Refuse blocking with a live incompatible run or starting it on a blocked item. If design work is permitted, grant it a distinct declared phase, not a second label. |
| Running and no lane while holding own commit | Lane/candidate settlement was treated as fresh allocation. Refuse a fresh dispatch while a retained candidate owns the continuation; route it to settlement and show only that disposition. |
| Landing exists but held item offers close | Landing scope, item state and remedy eligibility diverged. A final receipt must settle disposition and supersede incompatible holds in the same transaction; a prerequisite receipt does not close the item. Reject close using the same predicate as remedy rendering. |
| Trunk proved again but trunk-broken remains | Hold lacked revision-bound release. Refuse creation against obsolete proof evidence; the proof update clears holds whose predicates it satisfies, never those for unrelated failures. |
| Design answer landed before build | Item/landing lacked completion scope and graph cursor binding. Refuse a terminal item transition on a prerequisite publication; record the receipt and advance the bound node once, without restarting planning. |
| Restart records paused but keeps lane and lease | Run/lane lacked a settlement boundary. Refuse paused with an active execution lease; record stopping or interrupted until death is confirmed, then release or explicitly retain a candidate, atomically with settlement. |
| Fence refusal sent to design four times | Hold lacked a mover and changed-input predicate. Record an awaiting-fence-decision hold; refuse the same planner/design input until authorized fence revision, cancellation or a new in-fence plan. |
| Verify returns without verdict and restarts | Run/candidate confused missing evidence with negative judgment. Refuse verdict-based advance or refusal without a matching receipt; retain the candidate and retry or escalate only the incomplete verification node. |

## Decision Ledger

All dispositions below are the designer's proposals dated 2026-11-20; the owner may overturn them. The opening quotations, not these proposals, are owner statements.

| October decision | Designer's disposition and reason |
|---|---|
| D1 | Carry forward declarative tables and generated constraints, not a runtime FSM dependency: process-local checks cannot protect SQLite from a second process. |
| D2 | Reject the count of four plus admission for tillr: lane, hold, publication and backend windows have independent identities and recovery. Carry forward one stored state and one transition writer per machine. Admission remains a derived predicate. |
| D3 | Carry forward immutable verdict evidence; do not use a verdict as a synonym for item disposition. Keep criterion state distinct from the verdict receipt. |
| D4 | Carry forward independent candidate identity and one run liveness contract. Reject literal old column deletions: tillr has different storage and legacy candidate rows are history. |
| D5 | Carry forward receipt versus membership separation. Queue receipt presence does not make a candidate landable. |
| D6 | Carry forward CAS, generated database refusal, atomic allocation, cross-table checks and a single display partition. Do not install guards on unadjudicated legacy rows. |
| D7 | Carry forward typed refusal, rollback, a diagnostic and fail-stop reconciliation; no automatic doctor repair. |
| D8 | Carry forward explicit dry-run and apply. Dry-run reports each legacy tuple and evidence; apply checks the inspected snapshot still matches, records each adjudication and refuses unmapped rows. Never infer terminal states at read-open. |
| D9 | Reject board item numbers as build dependencies: they were for the retired board. Carry forward incremental adoption without a permanent second registry. |

## Open Questions

### Who authorizes a fence change?

<!-- vantage: question id=OQ-TSM1 leaning="Require the operator to authorize a new fence revision." -->

Options: operator authorization, or explicit delegation to a design seat with a separate scope grant. Neither option lets retry silently widen the existing fence.

_Leaning:_ Require the operator to authorize a new fence revision.

**Answer:**

### What can complete an item with intermediate publications?

<!-- vantage: question id=OQ-TSM2 leaning="Only the graph's explicit deliverable publication completes a landing item." -->

Options: only a designated deliverable publication, or require users to split any prerequisite publication into another item. Do not infer intent from the number of commits.

_Leaning:_ Only the graph's explicit deliverable publication completes a landing item.

**Answer:**

### May unrelated work continue after a reconciliation fault?

<!-- vantage: question id=OQ-TSM3 leaning="Stop admission and publication until adjudicated." -->

Options: stop admission and publication globally, or quarantine a proven independent subgraph and proceed. Existing running processes still need safe settlement in either case.

_Leaning:_ Stop admission and publication until adjudicated.

**Answer:**

### What should happen to unmapped legacy rows?

<!-- vantage: question id=OQ-TSM4 leaning="Refuse apply until every row is adjudicated." -->

Options: refuse apply, or permit an explicit quarantine with a read-only export and no dispatch rights. Neither silently repairs or drops history.

_Leaning:_ Refuse apply until every row is adjudicated.

**Answer:**

## Build order

1. Land a standalone declaration-to-SQL slice for the existing item definition on a fresh fixture database. Keep the current runtime and existing store untouched. `TestDeclaredItemMachineRejectsUndeclaredSQLMove` inserts a valid initial state, performs a legal move, then attempts an unlisted move between two valid labels by direct SQL and asserts SQLite refuses it and leaves the row unchanged. Also test unknown labels and declaration/trigger parity.
2. Add a read-only reconciler and shared snapshot partition with fixture tests for contradictory tuples and legitimate in-flight work. Shadow the partition before changing admission.
3. Unify lane and run allocation and restart settlement under transactional transitions; test stale lease epochs and interrupted launch/reap. Then promote the reconciler to admission refusal once the supported tuples are exhaustively tested.
4. Introduce candidate and publication machines with revision-bound evidence and scoped receipts; test prerequisite publication, missing verify verdict and crash after Git update before receipt.
5. Introduce typed hold, proof and backend transitions, using generation guards to test obsolete timers and proof recovery. Switch remedies and why to the shared partition.
6. Ship explicit adjudication and activation per machine. Remove superseded writers and readers only after replay, refusal and rollback tests pass for that slice. Keep legacy receipts readable without allowing them to drive work.
