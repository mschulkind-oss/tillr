---
title: "The Item Judge and Lifecycle in Tillr"
status: in-review
stage: DESIGN
updated: 2026-11-20
tags:
  - tillr
  - judge
  - verdict
  - lifecycle
  - state-machine
  - grants
  - drain
  - design
summary: "Who may record a verdict on an item's criterion, what an item's stored state means, and what each ending of a run does to its item. Eleven of the fourteen build blocks have landed; three queue items remain."
next: "Build the three queued items in Section 8: the owner-criterion hold, the evidence for finished states, and the design run's same-session refusal."
depends-on:
  - tillr-item-graphs.md
  - board-item-state-model.md
  - work-control-judge-in-tillr.md
  - escalation-ladder.md
---

# The Item Judge and Lifecycle in Tillr

**Status:** 2026-11-18. Eleven of the fourteen build blocks are on the trunk and three are queued ([Section 7](#7-what-is-built-and-what-is-owed)), read that day from the store with `tillr view status` and from the tree. No question is open. The status is in-review, not accepted, because four ledger rows ([OQ-JL1](#OQ-JL1), [OQ-JL2](#OQ-JL2), [OQ-JL4](#OQ-JL4) and `D-JL8`) are designer's decisions the owner has not ruled on; each row says so.

> **In short.** A criterion is retired only by an actor with authority over its class, and an item's stored state says what its owner wants done with it, never what a run is doing. Everything a run does short of landing is evidence returned to whoever can act on it: it is never a verdict on a criterion, and it is never counted.

**Why it matters.** Three defects came from one missing seam between the drain, the loop and the item store, and each is closed by a rule here ([Section 1](#1-the-defects-this-design-closes)): a gate pass retired a decision only the owner could make, a failed attempt froze criteria nobody had judged, and one client had a sign-off path with no grantor.

**The shape.** One verdict intake shared by every judge; a claim class stored on each criterion and checked against the actor; six stored item states with `working` derived from live runs; and, for every way a run can end, one named mover.

**Cost.** The `autoqa` actor kind, the stored `in-progress` state and the `-v2` clone workaround are gone. A `tillr drain rework` command was designed and is not added.

**Start at [Section 4](#4-a-failed-attempt-is-never-a-verdict-and-never-a-count)**, which carries the owner's four rulings on run endings.

**Needs your ruling:** None.

**Reads with:** [tillr-item-graphs.md](./tillr-item-graphs.md) (the graph and evaluator contract this judges inside), [the graph design](./tillr-graph-is-the-path.md#33-endings) (the closed set of next actors an objection may name), [board-item-state-model.md](./board-item-state-model.md) (the seven displayed states), [work-control-judge-in-tillr.md](./work-control-judge-in-tillr.md) (why the judge moved into tillr), [criterion-repair.md](./criterion-repair.md) (repairing a criterion a verdict has frozen), and [tillr-autoqa.md](./tillr-autoqa.md) (the external client of [Section 5](#5-core-boundary-for-external-autoqa-d-jl7)).

**Words.** This document uses the names the built code uses. [The vocabulary](./tillr-vocabulary.md#5-the-words) defines item, criterion, fence, gate, candidate, judge, seat, backend, objection and mover. It also retires two words the code and this document still say: a *lane* is a pool slot holding one worktree and one run, and a *hold* is a wait together with the mover that ends it. Four more are used here:

- **Worker**: the run that builds an item's change.
- **Design seat**: the role that answers a question about an item's definition by editing the item's block or its design document. A **design run** is one agent run in that seat. It is not the worker and never builds the item.
- **Objection**: the ending by which a run says it should not continue and names who acts next.
- **Operator**: whoever runs tillr's commands at a shell. It is not a seat, and not an actor a run's report can name.

---

## 1. The Defects This Design Closes

| Defect, as measured on 2026-11-14 | Cause | Closed by |
| :--- | :--- | :--- |
| **A gate pass retired an owner's decision.** The loop stamped every criterion of an item `done` when the gate passed, so a five-second Markdown check retired a criterion reading "Owner decision recorded". | The claim class lived only on the incoming verdict, so a mechanical actor could claim `verification-only` for any criterion. | The class is stored on the criterion and checked against the actor ([Section 2.2](#22-when-generic-judging-survives--and-closing-the-mechanical-stamp-hole-d-jl2)). Built. |
| **A failed attempt made an item unrepairable.** Every candidate that did not land wrote `not-done` against each criterion, which froze the criterion's text before any judge had read it. Six items were cloned under `-v2` ids to get around it. | An attempt's outcome was recorded as a criterion verdict. | An attempt writes no verdict ([Section 4.1](#41-attempt-outcomes-are-not-criterion-verdicts-d-jl5)), and a held item is never cloned ([Section 4.2](#42-sending-a-failed-item-again-under-its-own-id-d-jl6-d-jl8)). Built. |
| **AutoQA signed off without a grant.** A store-level `autoqa` actor kind skipped the delegation check every other agent passes. | A client's identity was encoded as an authority kind. | The kind is retired; a client is named in the verdict's `actor` field ([Section 5](#5-core-boundary-for-external-autoqa-d-jl7)). Built. |

---

## 2. The Judge Runs Before the Gate, and Its Verdicts Are Recorded After the Landing

### 2.1 Topology Relative to Gate and Land (`D-JL1`)

Per [tillr-item-graphs.md](./tillr-item-graphs.md) ([§3.3](./tillr-item-graphs.md#33-producer-bound-artifacts-exit-conditions-and-the-evaluator-slot), [§4.2](./tillr-item-graphs.md#42-worktree-sequencing-at-single-lane-concurrency), `D-IG8`, `D-IG9`), intra-graph landing is forbidden and `Loop.Verify` runs at most once at root graph exit. In any pre-approved graph carrying an `evaluator_slot` (such as `build-judged@v1` or `design-draft@v1`), the judge leaf sits **topologically after all `access: "write"` leaves and any in-graph preflight `check` leaves, and strictly before root `exit` and `Loop.Verify` (`tillr-gate` -> `tillr-land pass`)**:

```
[write leaves: edit, pack-sync] ──► [optional check leaf (command, read_only)]
                                              │
                                              ▼
                                    [evaluator_slot leaf (agent, read_only)]
                                              │ emits .tillr/loop/<run_id>/artifacts/eval.output
                     ┌────────────────────────┴────────────────────────┐
          outcome == "fail"                                  outcome == "pass" (all criteria `done`)
    (not-done | blocked | defective)                                   │
                     │                                                 ▼
                     ▼                                       Serial `Loop.Verify`
     Settlement Hook (NO Gate, NO Land;                      1. `fenceViolation` (outer `AllowedPaths`)
     `main` untouched; lane reset)                           2. `tillr-gate` (`gate.verdict`)
       • `not-done`  -> `in-queue` (held `eval-failed`,      3. `tillr-land pass` (fast-forward or carry)
                        `item_criteria` untouched)           4. Settlement Hook commits `done`
       • `blocked`   -> `blocked` (`waiting_on` set,            `item.verdict` records (citing landed sha
                        `item_criteria` untouched)              + `gate.verdict`) -> `SetState(landed)`
       • `defective` -> `item.verdict(defective)` +
                        `Close(item, "defective: ...")`
```

**Why pre-land evaluation paired with post-land verdict commit is mandatory:**
- Running the judge **before** `Loop.Verify` catches spec drift, unaddressed clauses, and `defective` premises in the scratch lane worktree before `tillr-gate` spends full suite wall time and before `tillr-land pass` moves `main`.
- Committing `done` `item.verdict` rows to `.tillr/state.db` **only after `tillr-land pass` succeeds** prevents [board-item-state-model.md](./board-item-state-model.md) [§2](./board-item-state-model.md#2-classification-of-every-status-the-board-can-show)'s pre-land completion leak ([OQ-1](./board-item-state-model.md#9-open-questions)). If `tillr-gate` fails or `tillr-land pass` isolates on a carry conflict after the judge passed, **zero** `item.verdict` rows are written, the item stays `in-queue`, and the refusal goes back to the worker ([Section 4.4](#44-how-a-worker-runs-ending-moves-its-item-d-jl10)). When `tillr-land pass` carries (`Reapply`) onto an advanced `main`, `tillr-land` has already verified tree/patch equivalence (`D-UT18`), so the settlement hook binds both `evaluated_head` (candidate sha) and `landed: [<trunk_after>]` onto each `done` `item.verdict`.

**Judge leaf output contract and deterministic settlement intake.** Lanes never open `.tillr/state.db` directly. The `evaluator_slot` leaf (`role: "evaluator"`, `access: "read_only"`) writes `.tillr/loop/<run_id>/artifacts/eval.output` validated by `tillr-drain`'s settlement hook before calling `RecordVerdict`:

| `eval.output` / intake field | Requirement enforced by `tillr-drain` + `RecordVerdict` |
| :--- | :--- |
| **`evaluated_head` & `evaluated_tree`** | Must match `Candidate.HeadSHA` and `Candidate.HeadSHA^{tree}` (or `item_base_sha` when `requires_candidate == "no_commit"`). |
| **`verdicts[]` completeness** | Exactly one entry per stored criterion of `item_id`, each carrying `criterion_id`, `criterion_hash == CriterionHash(frozen_text)`, `verdict` in `{done, not-done, blocked, defective}`, `claim_class`, non-empty `reason`, and non-empty `evidence[]` (artifact/diff/commit locators, never prose-only). |
| **Provenance-stamped `actor` & `judged_actor`** | Never trusted from model output. `tillr-drain` stamps `actor = "<evaluator_node_id>:<evaluator_run_id>:<evaluator_route>"` and `judged_actor = "<writer_run_ids>:<writer_routes>"` from W1's `node_runs` table ([tillr-item-graphs.md](./tillr-item-graphs.md) [§6](./tillr-item-graphs.md#6-boundary-with-judge-policy-and-item-lifecycle)), refusing any shared `run_id` or session handle (`checkIndependence`; [OQ-JL2](#decision-ledger)). |
| **Store-enforced `grant_id`** | Resolved from active `item_grants` (`tillr-item/item/grant.go`); `RecordVerdict` refuses missing, expired, or out-of-scope grants (`grant-absent`, `grant-expired`, `grant-scope`) inside the SQLite transaction. |
| **Multi-criterion aggregation** | `outcome == "pass"` iff **every** criterion is `done` (any claim of `"pass"` with a non-`done` entry is refused as malformed intake). On `"fail"`, precedence is: any `defective` -> record `defective` verdict(s) and `Close` item (`close_reason: "defective: <id>: <reason>"`); else any `blocked` -> move item to `blocked` with structured `waiting_on` from the judge's blocker payload; else (`not-done`) -> keep item `in-queue` held as `eval-failed` with `eval.output` indexed on the run. |

### 2.2 When Generic Judging Survives — and Closing the Mechanical-Stamp Hole (`D-JL2`)

In-graph judging and generic retirement partition cleanly without duplicate judge passes:

1. **Pre-approved graph with `evaluator_slot != nil` (`build-judged@v1`, `design-draft@v1`):** No generic judge runs after `Gate`/`Land`. The pre-exit `evaluator_slot` leaf already judged the criteria; `Gate` + `Land` verify and publish the tree, and the settlement hook commits the pre-exit `done` verdicts.
2. **Graph with `evaluator_slot == nil` (`single-worker@v1`) and pure `verification-only` criteria:** By default ([OQ-JL1](#decision-ledger)), `Gate` + `Land` exit `0` triggers mechanical retirement (`actor_kind: "mechanical"`, `claim_class: "verification-only"`, citing `gate.verdict` + `land.land`) at zero judge token spend, matching [work-control-judge-in-tillr.md](./work-control-judge-in-tillr.md).
3. **Opt-in and non-pipeline generic judging (same `evaluator` role, outside the build graph):** Generic judging survives as a standalone read-only `evaluator` invocation over the **exact same `eval.output` intake contract** — never a second codebase and never an automatic Pi turn-end hook ([work-control-judge-in-tillr.md](./work-control-judge-in-tillr.md) `Ruling: MIGRATE`) — in four cases:
   - `single-worker@v1` items carrying `judge_mode: "agent"` or a delegable non-mechanical criterion (`direction`, `red-gate-triage`) under an active `item.grant`;
   - foreground or out-of-band commits on `main` (`tillr item judge --item <id>`), where no drain pipeline landing occurred;
   - pre-dispatch premise triage (`requires_candidate: "no_commit"`) to retire a stale item as `defective` without running a worker; and
   - post-hoc audit sampling of mechanically retired items ([agent-runtime.md](./agent-runtime.md) [§12](./agent-runtime.md#12-the-three-owner-rulings)).

**A mechanical actor cannot retire an `owner-only` or `direction` criterion.** Three rules hold that:

1. **The class is stored on the criterion.** `item_criteria.claim_class` defaults to `verification-only` and is declared in the item's own block with the attribute `claims` (`D-JL8`). It may harden (`verification-only` to `red-gate-triage` or `direction`, and any of them to `owner-only`) per [criterion-repair.md](./criterion-repair.md) [§1](./criterion-repair.md#1-the-invariant-the-freeze-protects) `P1`, and is never weakened through `item apply`.
2. **The store checks the actor against the class.** `RecordVerdict` reads the criterion's text and stored class, refuses a verdict that claims a different class (`claim-class-mismatch`), and checks the actor's authority before writing:

   | Stored `item_criteria.claim_class` | `actor_kind: mechanical` | `actor_kind: agent` | `actor_kind: human` |
   | :--- | :--- | :--- | :--- |
   | `verification-only` | **Allowed** (with `gate.verdict` + commit receipt) | **Allowed** (`checkIndependence` + unexpired `item.grant`) | **Allowed** |
   | `red-gate-triage` | **Refused** (`claim-class-actor`) | **Allowed** (`checkIndependence` + `item.grant` naming `red-gate-triage`) | **Allowed** |
   | `direction` | **Refused** (`claim-class-actor`) | **Allowed** (`checkIndependence` + `item.grant` naming `direction`) | **Allowed** |
   | `owner-only` | **Refused** (`claim-class-actor`) | **Refused** (`claim-class-actor`; `IssueGrant` also refuses `owner-only` as `grant-owner-only`) | **Allowed** (sole writer) |

3. **Untyped owner prose is refused, and an item that owes an owner's ruling is held.** `Apply` refuses a `verification-only` criterion whose text begins "Owner decision recorded" (`owner-criterion-untyped`), and rows of that kind already in a store were typed by one migration (`D-JL8`). The planner holds any `in-queue` item with an unretired `owner-only` criterion as `owner-only-owed`, so that an item which cannot finish without the owner spends no lane. That hold is the one part not built: it is [the second block](#hold-an-item-that-still-owes-an-owners-ruling) of the build order.

---

---

## 3. The Owner-Disposition State Machine and Derived Execution State

### 3.1 Stored Owner Disposition vs Derived Execution Projection (`D-JL3`)

An item's stored state says what its owner wants done with it. Whether a run is working on it is derived, never stored.

- **Six stored states.** `not-started` (a draft, not admitted), `in-queue` (admitted to dispatch), `blocked` (waiting on a named waiter, with the state it was in remembered), `landed` (this item's work is on the trunk), `resolved-elsewhere` (its intent was satisfied by another path) and `closed` (taken out of work with a reason, and reopenable). The legacy `abandoned` and `deleted` stay readable and cannot be written.
- **`in-progress` cannot be written.** The store refuses it with the rule `state-frozen`. A row that still holds it is read as queued.
- **One projector.** `ProjectItemState` in `tools/tillr-view` emits the seven displayed states of [board-item-state-model.md](./board-item-state-model.md) [§7](./board-item-state-model.md#7-the-structural-rule-one-projector-a-closed-enum). It answers `working` only for an item that is unfinished, unblocked and has a running run, and the header's dispatchable count is the planner's own rule, so the page and `tillr drain plan` cannot disagree.

### 3.2 Transition Preconditions and Attempt Holds (`D-JL4`)

Every move between stored states has a precondition the store checks, and a refusal names its rule.

| Move | Allowed from | Precondition (rule on refusal) | Built |
| :--- | :--- | :--- | :--- |
| to `in-progress` | none | Always refused (`state-frozen`). | ✅ |
| to `blocked` | `not-started`, `in-queue`, `blocked` | A waiter and a reason (`blocked-waiter-missing`). The waiter is `owner`, `design`, `plan` or `item:<id>` for a stored item (`blocked-waiter-invalid`). The prior state is remembered so that unblocking restores it. | ✅ |
| to `landed` | `not-started`, `in-queue`, `blocked` | At least one criterion and every criterion `done` (`landed-criteria-unmet`), and a current `landed` landing record or `drain.graph-completed` event naming the item (`landed-receipt-missing`). | ✅ |
| to `resolved-elsewhere` | `not-started`, `in-queue`, `blocked` | A pointer to what resolved it: `item:<id>` for another stored item, `commit:<sha>` or `doc:<path>` (`resolved-pointer-missing`). Refused when the item already has a current receipt (`already-landed`). | ✅ |
| out of `landed` or `resolved-elsewhere` | `landed` | Refused (`state-terminal`) except the return of a reverted landing to `in-queue` via `state` with reason and actor. | ✅ |
| to `closed` | `not-started`, `in-queue`, `blocked` | A reason (`close-reason-missing`). Refused once the item has landed (`already-landed`): the landing wins. The lander refuses a candidate for a `closed` or `resolved-elsewhere` item (`item-not-landable`). | ✅ |
| out of `closed` | `closed` | Only by the reopen command, with a reason, which restores the state the item was closed from. A plain state write is refused (`closed-use-reopen`). | ✅ |

A receipt is current only if it follows the latest return from `landed` to `in-queue`, or the item has never returned.

**A failed run never changes the stored state.** `in-queue` says the item is wanted. It does not say what the last run did, and the planner derives that on every pass from the store's records, with a reason and a mover for each. The reasons are one table, `tools/tillr-drain/holdrules/holdrules.go`, which a test keeps complete: `returned-with-evidence`, `gate-failed`, `candidate-no-op`, `eval-failed`, `run-unjudged`, `retry-unjudged`, `run-infra-exhausted`, `backend-closed`, `question-open` and `judge-defective` are the ones this design touches. None of them is a count, and none expires. `owner-only-owed` is the one reason this design adds that is not built ([Section 7](#7-what-is-built-and-what-is-owed)).

---

## 4. A Failed Attempt Is Never a Verdict and Never a Count

### 4.1 Attempt Outcomes Are Not Criterion Verdicts (`D-JL5`)

A criterion's text freezes once a verdict is recorded against it, so that nobody lowers a bar a judge has already graded. An attempt that did not land is not such a grading, so it writes no criterion verdict and leaves the text editable.

| How the attempt ended | What is recorded | Where the item goes |
| :--- | :--- | :--- |
| The worker names a complete obstacle | The obstacle, on the run | The route of its kind ([Section 4.5](#45-what-a-worker-may-end-with-d-jl11)) |
| No commit, `RESULT: failed`, or an incomplete obstacle | A missing output, on the run | Back to the same worker session ([Section 4.4](#44-how-a-worker-runs-ending-moves-its-item-d-jl10), ending 1) |
| The candidate escapes its fence, the gate refuses it, or the lander refuses it | A `drain.refusal` event with the evidence | Back to the worker with the evidence (ending 1) |
| The landing conflicts | The conflicting paths | Back to the producer on the latest trunk, restarted fresh from the current trunk on the second identical conflict, and parked for the operator if that fresh attempt conflicts again (ending 1a) |
| A judge finds a criterion `not-done` | The judge's file, on the run | A workflow's evaluator returns it to the workflow (ending 1b). Any other judge leaves the item waiting as `eval-failed` |
| A judge finds a criterion `blocked` | The judge's waiter and reason | The item moves to `blocked`, waiting on that waiter |
| The run was killed or interrupted | Nothing against the item | It is run again (ending 5) |

Two endings do write a criterion verdict, because they are gradings: `done`, recorded only after the landing succeeds, and `defective`, a judge's finding that the criterion's premise is false, which is recorded and closes the item with the judge's reason. The planner then shows the closed item as `judge-defective`. [Criterion repair](./criterion-repair.md) is the design for what happens to its frozen text.

### 4.2 Sending a Failed Item Again Under Its Own Id (`D-JL6`, `D-JL8`)

A failed item is always worked again under the id it has. Its run history and its place in other items' dependencies stay with it.

- **Returned by the loop.** A refused or empty run goes back to its worker with the evidence, with no command and no count ([Section 4.4](#44-how-a-worker-runs-ending-moves-its-item-d-jl10)).
- **Returned by a person.** `tillr drain retry` is the one explicit command. No `tillr drain rework` command exists: a second command for the same act would be a second place to be wrong (`D-JL8`).
- **Never cloned.** `tillr item sync` refuses a copy of a held item filed under a new id. `resolved-elsewhere` therefore means only what [board-item-state-model.md](./board-item-state-model.md) [§4.1](./board-item-state-model.md#41-semantics) says: the intent was satisfied by another path.

### 4.3 How a Design Run's Ending Moves Its Conflict (`D-JL9`)

When a worker reports `RESULT: blocked`, the drain records a design conflict and dispatches one design-seat run to answer it. Until 2026-11-17 the drain counted that run's endings: a design run that ended without answering was an attempt, the conflict was raised once more only if the item's block, its design document or a proven trunk commit had changed, and otherwise the item went to the owner. The owner ruled on 2026-11-17 that this counting is wrong and must go. **Rule: no numeric threshold applies to design runs, and nothing is re-dispatched because something changed. An item reaches the owner only through an explicit objection that names the owner. Each ending does the one thing it means:**

| Ending | What moves the conflict | Built |
| :--- | :--- | :--- |
| 1. The gate refuses the design run's commit (for example the `documents` lint) | The failing checks and output return to the **same designer session**, even when the refusal is identical. The next turn has a new run record for its own fence, gate, and landing. A refusal the trunk caused is the gate's to recognize, not a reason to re-run the designer on a file-change guess. | Built. |
| 2. The loop misread a committed answer | A bug, fixed: the settlement reads the block by its id and section. No guard or count is kept around it. | Built. |
| 3. The run was killed or interrupted (a loop stop, a lease reclaimed after its loop died, a signal) | The drain continues the recorded session when available; otherwise the conflict explains why it starts a fresh one. Nothing is counted. | Built. |
| 4. The run committed, but not to the item's block | The design seat did what it judged right. The check that called this "did not amend" was the bug and is **removed**: a design commit that passes the gate and lands is the answer, and the item goes back to its worker. A commit that removes or renames the block leaves no item to build, so it goes back to the design seat naming the commit. | Built. |
| 5. The run committed nothing | A legitimate objection. Its report names the next actor with a line `NEXT-ACTOR: <actor>`, and policy routes on it: `build` (the worker was wrong; build as written) answers the conflict and returns the item to its worker; `owner` (a ruling only the owner can make) holds it for the owner. A run that commits nothing and names no actor, even one whose report ends blocked, is a missing output rather than an objection: it goes back to the design seat, told the contract, and never defaults to the owner. | Built for `build` and `owner`, which is the whole set ([OQ-JL4](#OQ-JL4) in the ledger). An objection naming the owner is published as a numbered question in the item's design document, where the owner answers it ([Owner decisions in documents](../../tools/tillr-drain/README.md#owner-decisions-in-documents)). |
| 6. The run "ran out of time" | There are no timeouts: a budget is a target, never a cause of failure. No backend policy in `config/tillr/` sets `timeout_seconds`, so no run has a wall. Every `timed-out` run in the store on 2026-11-17 had the end cause `lease-reclaimed`: a replacement loop reclaimed the claim of a loop a jail relaunch had killed, and stamped the reclaim time as the finish. That label is a mislabel, not a limit; such a run is a killed run (ending 3). | Nothing to remove. The label is reported, not changed. |
| 7. The ending is not a gate refusal and repeats an earlier design ending of the item exactly: the same cause and the same evidence, such as a second run that names no actor for the same conflict and the same block | Returning it to the design seat would send the design seat the same thing again, and the design seat is already the seat that diagnoses. Before this rule, a run that named no actor was dispatched again without end. One test spun for 59 minutes on it, and in production each round is a paid design run. The loop raises an objection naming the owner, with a question written from the two records, and nothing is counted. The objection is recorded the way a design run's own `NEXT-ACTOR: owner` is: a `conflict-owner` hold whose conflict text carries the question and the command that moves it. Two different endings are never a repeat, however many there are. | Built, including the question's publication in the design document. |

The owner also ruled out every re-dispatch on a guessed change. A metadata write, a prose rewrite or any edit to the item's block is not a reason to spend a design run, so the drain no longer compares the block, the design document or the trunk to raise a design conflict, and an item built on the design seat is no longer re-admitted on a changed input. The explicit operator command, `tillr drain retry --item <id> --run <design run>`, stays: it is explicit. An owner's answer to an objection is the event that moves the item to the actor the answer implies. The answer is the ruling recorded on the published question, read from the trunk at the next sync.

An identical repeat (ending 7) is the comparison [`D-JL10`](#44-how-a-worker-runs-ending-moves-its-item-d-jl10) uses on the worker path, the same pure function over the design seat's records. An ending's evidence key is its cause plus its evidence:

- **No actor named, or the block removed:** the conflict and a digest of the item's block on the trunk when the ending is settled, which is the block the next design run would be given.
- **A fence escape or a refused landing:** the refusal's own words.

An interrupted or killed run has no key and never repeats. The objection's mover is the owner, not the design seat: the next actor on a design-seat repeat cannot be the seat that just produced the same ending twice. The owner moves it by changing what the design seat is given and running `tillr drain retry --item <id> --run <the later design run>`, or by closing the item.

The drain README's section [A design run that ends without answering](../../tools/tillr-drain/README.md#a-design-run-that-ends-without-answering) states the built rule and its tests.

### 4.4 How a Worker Run's Ending Moves Its Item (`D-JL10`)

The same ruling of 2026-11-17 covers the worker seat, the run that builds an item rather than designs it. Until then the drain counted a worker's endings too. The first refused run held the item for a person. An edit that changed a hash of the item's block, fence, backend policy or tillr build re-admitted it once, and a second change was held as `readmission-exhausted`. A second failure went to the design seat, and to the owner when the item had no design document. Uncommitted work got one continuation of its session before it was refused. An authorization that named an older run held the item as `retry-stale`. The operator's audit of that day lists these as findings F-01, F-02, F-03, F-07 and F-09; [the graph design's audit table](./tillr-graph-is-the-path.md#124-audit-findings) restates them. **Rule: a worker's ending is never counted, and nothing re-runs because a hash changed. Each ending does the one thing it means, and an identical repeat goes to the design seat:**

| Ending | What moves the item | Built |
| :--- | :--- | :--- |
| 1. The gate refuses the candidate, the run commits nothing, the candidate escapes its fence, or the landing is refused | The ending goes back to the item's worker with its evidence, every time and uncounted. The next turn continues the same worker session on the retained lane when the adapter can resume it, with a distinct run record and the refusal's own words, each failing gate check with its log, and the refs that keep the earlier run's work. A workflow continues its producing node with the original boundaries and accepted inputs. An unavailable session, unsupported adapter, changed seat or unavailable lane starts a fresh run with that evidence and a recorded reason. | Built. |
| 2. An identical repeat: the same producer, the same cause and the same evidence as any earlier ending of the item (other than `squash-conflict`, which follows ending 1a) | The item goes to the design seat with both runs' records. Two endings that differ are never a repeat, however many there are. The evidence is the sorted ids of the failing checks with a digest of each check's normalized output, or the uncommitted paths with a digest of their content, or the refusal's normalized words. Normalizing drops the run id, timestamps, durations, full hashes and temporary directories. | Built. |
| 1a. The landing conflicts (`squash-conflict`) | Before any conflict restart in the current retry cycle, a conflict goes back to the producer with the conflicting paths, uncounted, on the latest trunk: at once as a conflict-fix run with the three-way markers when the trunk moved under the candidate, and otherwise as the next pass's returned run. On the second identical conflict within the current retry cycle — the same conflicting paths and normalized hunks digest as an earlier conflict ending since the latest `tillr drain retry` — the drain discards the candidate (clearing any replayed dirty or repair holds on it and closing any open landing call on it), resets the lane to the current trunk, and returns the item so the planner starts a fresh attempt of its workflow from the first stage on the current trunk (`D-JL12`). That fresh attempt's brief carries the item's normal source reference (`body_ref` or saved request words) with no conflict-fix instructions, states that the earlier attempt conflicted twice at landing, and names the conflicting paths. If that fresh attempt conflicts again at landing, the item parks for the operator (`WaitingOn: "owner"`, `conflict-owner`) regardless of whether the conflicting paths or hunks changed and regardless of whether the item has a document block or a mapped `design` seat. The removed rule spent one fix run per candidate and then held the item as `conflict-exhausted`; an item it parked is returned at the next loop start. | Partly: the conflict-fix return is built; the fresh-attempt restart and post-restart operator park (`D-JL12`) are queued. |
| 1b. A workflow evaluator finds a criterion `not-done` | The refusal `verify-not-done` returns the criterion ids and reasons to a fresh workflow entry; the same set of ids found again is ending 2. A non-workflow judge retains the `eval-failed` hold. | Built. |
| 3. The run exits with uncommitted work inside its fence | Its session is continued, for as long as each continuation changes that work. A continuation that leaves the work as the previous one did is refused as `candidate-no-op` (ending 1), and a repeat of that refusal is ending 2. | Built. |
| 4. The run reports `RESULT: blocked` | Since [`D-JL11`](#45-what-a-worker-may-end-with-d-jl11), it must name a complete obstacle, and each obstacle kind has its own route. A blocked report without one is a missing output: ending 1, run that committed nothing. | Built. |
| 5. The run was killed or interrupted | It is run again, uncounted. A killed continuation of uncommitted work is continued again. | Built. |
| 6. An input changed: the item's block, its fence, the backend policy or the tillr build | Nothing. A prose or metadata edit to an item's block does nothing to its work, and an in-flight workflow is not invalidated. The operator's `tillr drain retry`, a design answer and the recovery of a killed run still move an item. | Built. |
| 7. An authorization names a run that is no longer the item's latest | The authorization no longer speaks, and the item's latest ending routes it as if there were none. | Built. |
| 8. The machinery failed (infrastructure class) | The ruling: each failure moves to another open backend, and when none is open the item waits on the backend. | Built as [recorded backend availability](../../tools/tillr-drain/README.md#recorded-backend-availability). An infrastructure ending closes that backend, and a later run of any other class or a successful probe reopens it. An item with no open backend waits with the backend as its mover (`backend-closed`). A restart, a policy edit or elapsed time is not evidence that a broken harness works again. |

The rule has the shape of [`D-JL9`](#43-how-a-design-runs-ending-moves-its-conflict-d-jl9). A count was a guard built around bugs, so the bugs it guarded are fixed instead, and each ending names who moves it. The returned ending is the planner's reason `returned-with-evidence`. Items the removed rules held are planned again at the next loop start with no repair, because holds are derived from the store on every pass. The drain README's section [A refused run goes back to its worker](../../tools/tillr-drain/README.md#a-refused-run-goes-back-to-its-worker) states the built rule and its tests.

**Why a repeated landing conflict restarts from the current trunk (`D-JL12`).** On 2026-11-19 a document retirement conflicted with the trunk at landing, was sent back, and produced the same conflict again; parking it on the second identical conflict left the operator only one useful move, closing and refiling the item so work started fresh from the current trunk. On 2026-11-19 and 2026-11-20 the owner ruled that the drain takes that one fresh start automatically: on the second identical `squash-conflict` within the current retry cycle, discard the candidate (releasing any replayed dirty or repair holds and settling any open landing call on that candidate), reset the lane to the current trunk, and return the item through the planner as a fresh workflow attempt from its first stage rather than an inline `-cfix` copy of the last stage's request and seat. Its brief keeps the item's source reference (`body_ref` or saved request words), omits conflict-fix `Settled decisions` and marker-reapply instructions, states that the earlier attempt conflicted twice, and names the conflicting paths. Only when that fresh attempt conflicts again at landing does the drain park the item for the operator (`WaitingOn: "owner"`, `conflict-owner`), whether or not the hunks changed and whether or not a document block or `design` seat exists; an explicit `tillr drain retry` starts a new retry cycle. (A build attempt of this change on 2026-11-20 reached the workflow's `verify` node and stopped on a `trunk` obstacle when `TestAcceptARebaseOntoAMovedBase` failed after `aed03075b`; because multi-node workflow blocked reports route to the design seat under [Section 4.5](#45-what-a-worker-may-end-with-d-jl11), that report opened a design conflict. Commit `515e28a36` repaired the trunk test, and recording `D-JL12` here aligns this ledger with the request before the workflow runs again.)

### 4.5 What a Worker May End With (`D-JL11`)

The owner ruled on 2026-11-18 that a worker must not be handed a sanctioned way to end without delivering. Until then the brief offered `RESULT: failed` and a list of "stop and report" conditions, and the drain answered a run that took one by sending the same brief again. Measured on 2026-11-18, Sol 6.2 ended 19 of 61 drain runs `RESULT: failed`, most of them after a few read-only commands. The owner's words: "if you don't want that path, don't expose that path. And if it is a valid path — they can't make forward progress — we need a way to take another action rather than just telling them to do the same thing again." **Rule: a worker run has two endings. The goal is the commit. The only other ending is a named obstacle: a fact the worker cannot change, of one closed set of kinds, together with what it needs. Each kind leads to a different action, and none of them sends the same worker the same brief again.**

**Whose decision is which.** The principle and the quotation above are the owner's. The six kinds below, their required fields and the route of each were chosen by the session's coordinator on the owner's delegation; they are not his words, and he may overturn any of them.

The report names the obstacle with a line `OBSTACLE: <kind>`, then that kind's fields, each on its own line, and ends `RESULT: blocked`. A field's value runs to the next field or the footer, so evidence may span lines.

| Kind | When | Required fields | What the loop does | Where it lands |
| :--- | :--- | :--- | :--- | :--- |
| `definition` | The item's words are wrong or contradict the tree | `CRITERIA`, `EVIDENCE` | Raises a design-seat conflict. The design seat amends the block or objects ([`D-JL9`](#43-how-a-design-runs-ending-moves-its-conflict-d-jl9)) | The conflict route existed. The kind and its instruction are new |
| `fence` | The change needs paths outside the fence | `PATHS`, `EVIDENCE` | Raises a design-seat conflict telling the design seat to widen the block's paths over the named ones or split the item | Same route as `definition`. The instruction is new |
| `trunk` | The base is red, and this change did not cause it | `CHECKS` (gate check ids), `BASE` | Replays the named checks on the run's base. Every one red there: the trunk fixer is filed or attached to, and the item waits on it, released when it lands. Otherwise the run goes back to its worker with what the replay found | The replay, fixer and release existed for a red gate. Reading the claim from a worker is new |
| `tooling` | A tool or the harness is defective | `COMMAND`, `EVIDENCE`, `PATHS` | Files a defect item, ranked with trunk fixers, whose criterion is that the command completes and the evidence's failure no longer reproduces. The item waits on it, with the tooling as mover, and is released when it lands or someone closes it. The defect item is named by a digest of the command, so a second report of the same command, from any item, attaches to the one item and widens its fence | New |
| `seat` | An attempt the worker made and tested shows the task exceeds this model | `TRIED`, `MISSING`, and a changed worktree | A seat obstacle from a run that left the worktree unchanged from its base is a missing output, because its evidence is the attempt: three Sol 6.2 runs of one item, measured on 2026-11-18, each named `seat` after two or three read-only commands. Otherwise the item moves to the next backend on the backend policy's `escalate` list for its seat, for every later run until it lands. That next run is a fresh run with the obstacle in its brief, because the seat changed. Past the end of the list, a design-seat conflict asks for a plan or a split. The same model is never run again for the item after it names `seat` | New. The list is backend names. Seat semantics are not defined here. They are [the planning design's](./tillr-planning-node-model-tiering.md#4-kinds-of-work), whose seats landed in every backend policy on 2026-11-18 |
| `owner` | A decision only the owner can make | `QUESTION` | Raises a design-seat conflict carrying the question. The design seat answers it from the documents, or objects `NEXT-ACTOR: owner` and writes the question in its report, where the owner reads it. A worker never raises a direct owner hold | The conflict route existed. The instruction is new |

**What is not an ending.** `RESULT: failed`, a `blocked` report without a complete obstacle, a kind outside the set, and a run that stops with neither commit nor obstacle are each a missing output. Without a commit, the run goes back to its own session as ending 1 of [`D-JL10`](#44-how-a-worker-runs-ending-moves-its-item-d-jl10), and the refusal says what is missing and states the two endings. In the pi adapter the same check runs inside the session before it settles, so the worker is answered in place without a new run. With a commit, the candidate is judged like any other. The identical-repeat rule is what ends that loop, never a count. The brief lists, as work to continue rather than obstacles, a patch that does not apply cleanly, a merge conflict, a failing test inside the fence, a long suite, and work not yet done. Records written before this rule are still read: `failed` and `trunk-broken` footers keep their old meaning in workflow nodes and in history, and a `trunk-broken` footer with the trunk fields reads as a `trunk` obstacle.

**A generated repair item**, meaning a trunk fixer or a defect item, has no design block, so the design seat cannot widen its fence or amend its criterion. A `fence`, `definition`, `owner` or `tooling` obstacle from such an item goes to the owner. The loop writes the question from the obstacle's own fields, and for `fence` that question names the paths and asks whether the item's `allowed_paths` may grow. A trunk fixer may not name `trunk`, since the red trunk is its work. A repair item's `tooling` obstacle files no second defect item, because a repair waiting on another repair could not pass its own gate on a red trunk. A legacy `NEXT-ACTOR: owner` line from a fixer is still read.

**Not built.**

- The report footer is to be replaced by one tool call, the design of [the landing call](./tillr-landing-call.md). Until that is built, the footer above is the contract.
- An obstacle naming another item that must land first (graph-doc rule 14) is not a kind yet. The route would be a queue edge plus a resume after that item lands.
- In a multi-node workflow, a node's blocked report still takes the design-seat route whatever its kind. The `trunk`, `tooling` and `seat` routes apply to single-worker runs.
- The trunk replay runs off the store's write lock and in a scratch worktree of its own. It still runs inside the settling lane's turn, because a refuted claim continues that lane's session.

The code is `tools/tillr-drain/drain/obstacle.go` and `obstacle_route.go`, the brief's **Report shape** in `tools/tillr-run/backends/brief-template.md`, and the in-session check in `tools/tillr-run/backends/pi/keep-going.ts`. Both readers are tested against one case file, `tools/tillr-run/backends/pi/testdata/obstacle-cases.json`.

---

---

## 5. Core Boundary for External AutoQA (`D-JL7`)

AutoQA is a client of tillr, not a part of it. The item store has no vocabulary of its own for it: the `autoqa` actor kind, the `qa-verification` claim class and the `autoqa` decider are refused on new writes with the rule `actor-retired`, and rows that already carry them stay readable. The standalone `gate change`, `run request` and `run exec` contracts of [autoqa-tillr-seams.md](./autoqa-tillr-seams.md) (`D-AQA2` to `D-AQA4`) stay.

1. **Authority kind and client identity are two fields.** A verdict's `actor_kind` (`mechanical`, `agent` or `human`) names the rule the store checks. Its `actor` names who ran. A client's name in `actor`, such as `autoqa/<suite>`, marks its verdicts on every read surface without a fourth kind, and the item page shows both fields.
2. **An external client crosses into tillr through four existing faces only.** [The AutoQA design](./tillr-autoqa.md) is the first such client; it keeps its own queue, coverage state and false-positive memory outside the item store.
   - **File a bug or remedy item:** `tillr item apply`.
   - **Record a deterministic test result:** `tillr item verdict record` with `actor_kind: "mechanical"`, `claim_class: "verification-only"` and evidence.
   - **Record an agent's review:** `tillr item verdict record` with `actor_kind: "agent"`, the judged actor, and an unexpired grant.
   - **Escalate to a person:** `tillr item mail send`, or a move to `blocked` waiting on the owner. Never a fifth verdict value and never a decision row decided by the client.

---

## 6. Decisions, Open Questions, and Amendments Owed

<a id="61-decision-ledger"></a>

### Decision Ledger

**Who decided each row.** A row dated "owner" is the owner's ruling, recorded in the section it links. Every other row is a designer's decision: `D-JL1` to `D-JL7` are the design as written on 2026-11-14, and the rows marked "designer's decision" were settled without the owner. He may overturn any of them.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-JL1` | A judge runs after the writers and before the gate. Its file is checked by one intake, the judge's identity is stamped by the drain and never read from the file, and `done` verdicts are recorded only after the landing succeeds. | 2026-11-14, designer | [Section 2.1](#21-topology-relative-to-gate-and-land-d-jl1) | ✅ |
| `D-JL2` | No second judge runs after the gate when a workflow has an evaluator. The claim class is stored on the criterion; a mechanical actor retires only `verification-only` criteria, and an `owner-only` criterion is retired by a person and cannot be granted. | 2026-11-14, designer | [Section 2.2](#22-when-generic-judging-survives--and-closing-the-mechanical-stamp-hole-d-jl2) | Partly: the planner's `owner-only-owed` hold is queued |
| `D-JL3` | `in-progress` is not a stored state. Six stored states say what the owner wants; `working` is derived from running runs. | 2026-11-14, designer | [Section 3.1](#31-stored-owner-disposition-vs-derived-execution-projection-d-jl3) | ✅ |
| `D-JL4` | Each move between stored states has a precondition the store checks, a landing wins over a close, and a failed attempt waits under a derived reason beside the stored state. | 2026-11-14, designer | [Section 3.2](#32-transition-preconditions-and-attempt-holds-d-jl4) | Built |
| `D-JL5` | An attempt that did not land writes no criterion verdict, so unjudged criteria stay editable. | 2026-11-14, designer | [Section 4.1](#41-attempt-outcomes-are-not-criterion-verdicts-d-jl5) | ✅ |
| `D-JL6` | A failed item is worked again under its own id and is never cloned. The `tillr drain rework` command this row first added is withdrawn by `D-JL8`, and its one bounded re-admission by `D-JL10`. | 2026-11-14, designer | [Section 4.2](#42-sending-a-failed-item-again-under-its-own-id-d-jl6-d-jl8) | ✅ |
| `D-JL7` | The store's AutoQA vocabulary is retired; an external client uses the ordinary verdict kinds and is named in the `actor` field, which the item page shows. This supersedes `D-AQA1`. | 2026-11-14, designer | [Section 5](#5-core-boundary-for-external-autoqa-d-jl7) | ✅ |
| OQ-JL1 | **Designer's decision, 2026-11-15; not ruled by the owner; the owner may overturn it.** An item with one node whose criteria are all checkable by a test is retired mechanically when the gate and the landing succeed, at no judge spend. An agent judge runs only where a workflow or the item asks for one. **Rejected:** an agent judge on every item by default. The reason given: [the runtime design](./agent-runtime.md#1-purpose-and-scope) says that what can be mechanical is mechanical, and the owner asks for token efficiency throughout. **Overturned for code landings by the owner's requirement of 2026-11-18**: before an item's commits land, a fresh agent with no memory of writing them reviews the change, with the diff as one of the things it reads; documents-only landings are not reviewed (`D-DR1`, `D-DR2` in [the diff-review design](./tillr-diff-review.md#decision-ledger), which designs that review; not built). Measured that day: no agent has ever judged a landing, with 0 judge events and 1,221 criterion verdicts, all by the mechanical actor. | 2026-11-15, designer | [Section 2.2](#22-when-generic-judging-survives--and-closing-the-mechanical-stamp-hole-d-jl2) | ✅ |
| OQ-JL2 | **Designer's decision, 2026-11-15; not ruled by the owner; the owner may overturn it.** The store refuses a judge that shares a run or a session with the writer. Whether the judge also uses a different model is set in the backend policy, never refused by the store. **Rejected:** refusing a verdict when the judge and the writer used the same model route. The reason given: the owner changes providers often, and a hard refusal would stop every judged item on a machine with one route. | 2026-11-15, designer | [Section 2.1](#21-topology-relative-to-gate-and-land-d-jl1) | ✅ |
| OQ-JL3 | **Designer's decision, 2026-11-15; not ruled by the owner; overturned by the owner's ruling `D-JL10` on 2026-11-17.** It allowed the loop one rework of an item on a recorded basis and sent a second failure up the escalation ladder. Nothing of it stands. | 2026-11-15, designer | [Section 4.4](#44-how-a-worker-runs-ending-moves-its-item-d-jl10) | — |
| `D-JL8` | **Designer's decision, 2026-11-16, made by the design seat while cutting the build order; not ruled by the owner; the owner may overturn it.** A criterion's claim class is declared in its item block with the attribute `claims`, and an item asks for an agent judge with `judge="agent"`. Owner-ruling rows already in a store are typed by one migration. No `tillr drain rework` command is added. The judge-before-gate step is built in the loop for an item that opts in, and a workflow's evaluator connects to that same step; it has never run on a real item (see [OQ-JL1](#decision-ledger)). Once the owner's review requirement of 2026-11-18 is built ([the diff-review design](./tillr-diff-review.md)), asking for a judge no longer decides whether a code landing is reviewed. **Rejected:** a second re-queue command beside the one that exists; waiting for item graphs before any judge runs. | 2026-11-16, designer | [Section 8](#8-build-order) | ✅ |
| `D-JL9` | A design run's ending is never counted and nothing is re-dispatched because an input changed. A gate refusal goes back to the design seat, a killed run is run again, a gated design commit is the answer whatever it changed, and a run that commits nothing objects by naming the next actor. An item reaches the owner only by an objection naming the owner: the design run's own, or the loop's on an identical repeat other than a gate refusal. **Rejected:** one automatic re-raise per input change, the trunk-suspect re-raise, and handing a second unanswered design run to the owner by count. | 2026-11-17, owner | [Section 4.3](#43-how-a-design-runs-ending-moves-its-conflict-d-jl9) | Built. |
| `D-JL10` | A worker run's ending is never counted and nothing re-runs because an input's hash changed. A gate refusal, a run with no commit, a fence escape, a refused landing and a landing conflict go back to the worker with their evidence every time. Only an identical repeat goes to the design seat (amended for repeated `squash-conflict` endings on 2026-11-19 / 2026-11-20 by `D-JL12`). Uncommitted fenced work is continued while each continuation changes it. An infrastructure failure moves the item to another open backend. **Rejected:** the one input-change re-admission, `readmission-exhausted`, `retry-stale`, the one conflict-fix run and `conflict-exhausted`, routing a second failure to the design seat and then the owner, the one continuation, and a numeric threshold of any kind. | 2026-11-17, owner | [Section 4.4](#44-how-a-worker-runs-ending-moves-its-item-d-jl10) | ✅ |
| `D-JL11` | A worker run ends with its commit or a named obstacle of one closed set of kinds (`definition`, `fence`, `trunk`, `tooling`, `seat`, `owner`), each with required fields and its own route. `RESULT: failed` and an incomplete obstacle are missing outputs, returned to the same session. **Rejected:** `RESULT: failed` as an ending, "stop and report" conditions, a direct owner hold from a worker, and escalation by count, timer or no-op. The principle that a worker is handed no sanctioned way to end without delivering is the owner's, quoted in Section 4.5. The six kinds, their fields and the item-level route of each were chosen by the session's coordinator on the owner's delegation, and the owner may overturn those. | 2026-11-18, owner (principle); coordinator (kinds and routes) | [Section 4.5](#45-what-a-worker-may-end-with-d-jl11) | Partly: see that section's "Not built" list |
| OQ-JL4 | **Closed as stale by the designer, 2026-11-18; not ruled by the owner; the owner may overturn it.** The question asked whether a design run's objection may name "the whale or the operator" as the next actor. A design run's objection names `build` or `owner`, and any other name is read as naming none. The set widens only when [the graph design's rule on next actors](./tillr-graph-is-the-path.md#33-endings) is built for the design seat; that rule lists the design seat, the owner with a question, another item, the tooling and a stronger seat, and neither of the two this question asked about. The whale, a coordinating session of the deleted board, no longer exists, and the operator runs commands and is not an actor a report can name. No item was filed behind the question and none of this document's three queued items depends on it. The question's sentence that the owner's ruling of 2026-11-17 named the two actors had no recorded source: `rg -n -i 'whale or operator' docs tools` on 2026-11-18 matched only this document. | 2026-11-18, designer | [Section 4.3](#43-how-a-design-runs-ending-moves-its-conflict-d-jl9) | ✅ |
| `D-JL12` | On the second identical `squash-conflict` within the current retry cycle (matching conflicting paths and normalized hunks digest since the latest `tillr drain retry`), discard the candidate — clearing replayed dirty or repair holds and settling any open landing call on it — reset the lane to the current trunk, and return the item so the planner starts a fresh attempt of its workflow from the first stage on the current trunk whose brief keeps the item's source reference (`body_ref` or saved request words) with no conflict-fix instructions, states that the earlier attempt conflicted twice, and names the conflicting paths. Park the item for the operator (`WaitingOn: "owner"`, `conflict-owner`) only if that fresh attempt conflicts again at landing, whether or not the hunks changed and whether or not a document block or `design` seat exists. Amending `D-JL10` on repeated landing conflicts. **Rejected:** parking on the second identical conflict without restarting from the current trunk; dispatching an inline `-cfix` copy of the last stage's request and seat; staging another `-cfix` or routing to `design` when the restarted attempt conflicts again; keeping conflict-fix instructions or omitting `body_ref` on the restarted brief; leaving replayed repair holds or open landing calls on the discarded candidate. | 2026-11-19 / 2026-11-20, owner | [Section 4.4](#44-how-a-worker-runs-ending-moves-its-item-d-jl10) | Queued |

### 6.2 Open Questions

None.

### 6.3 Amendments Owed

[board-utilities.md](./board-utilities.md) [§5.3](./board-utilities.md#53-item-alone) states the preconditions for `landed` and `resolved-elsewhere`. The amendments this design owed to [autoqa-tillr-seams.md](./autoqa-tillr-seams.md), to `D-UT16` of [board-utilities.md](./board-utilities.md#14-decision-ledger) and to [work-control-judge-in-tillr.md](./work-control-judge-in-tillr.md) are made.

---

## 7. What Is Built and What Is Owed

Read from the store on 2026-11-18. Each block is one queue item, and a block's number is its place in the build order.

| # | Block | Item | Waits on | State |
| :--- | :--- | :--- | :--- | :--- |
| 1 | A gate pass does not retire an owner's criterion | `item-jl-criterion-claim-class` | nothing | landed |
| 2 | An item that owes an owner's ruling is held | `item-jl-owner-criterion-hold` | 1 | **queued** |
| 3 | A failed landing freezes no criterion | `item-jl-land-refusal-no-verdict` | nothing | landed |
| 4 | A blocked item names its waiter and reason | `item-jl-blocked-names-its-waiter` | nothing | landed |
| 5 | `landed` and `resolved-elsewhere` need their evidence | `item-jl-terminal-state-receipts` | 2 | landed |
| 6 | Close and reopen commands | `item-jl-close-and-reopen` | 4 | landed |
| 7 | AutoQA uses the ordinary verdict kinds | `item-jl-retire-autoqa-vocabulary` | nothing | landed |
| 8 | The item page shows who recorded a verdict | `item-jl-view-verdict-actor` | nothing | landed |
| 9 | One intake for a judge's written verdicts | `item-jl-judge-intake` | 1 | landed |
| 10 | An opted-in item is judged before the gate | `item-jl-judge-before-gate` | 3, 9 | landed |
| 11 | A judge's blocked and defective findings | `item-jl-judge-blocked-and-defective` | 4, 6, 10 | landed |
| 12 | A second failed attempt goes to the design seat | `item-jl-second-failure-to-design` | nothing | landed, then replaced by `D-JL10` |
| 13 | A held item is not cloned under a new id | `item-jl-refuse-clone-ids` | 12 | landed |
| 14 | A design run's gate refusal returns to its own session | `item-design-refusal-resumes-session` | nothing | **queued** |

**Finished-state evidence is built.** Block 5 requires completed criteria and a current receipt for `landed`, and a pointer for `resolved-elsewhere`; the reverted-landing return remains the only way out of either finished state.

**Two pieces outside the blocks.** A workflow's evaluator node belongs to [tillr-item-graphs.md](./tillr-item-graphs.md) and is built there; it hands its file to the same intake. What a worker's obstacle does not yet do is listed in [Section 4.5](#45-what-a-worker-may-end-with-d-jl11).

---

## 8. Build Order

Fourteen blocks, in the order of [the table above](#7-what-is-built-and-what-is-owed). Each is one run inside one fence. A block's fence includes the copy of the tillr source under `yolo-packs/tillr/`, because every change under `tools/tillr*` is delivered there too. The queued blocks keep their full text and criteria; landed blocks generally keep their heading and a summary of what they built. The finished-state block also retains its decisions and criteria.

### Prevent a gate pass from retiring an owner's criterion

Landed. Each criterion has a class, an author states it in the item's own block, and both the store and the loop's verdict step respect it: a criterion marked as the owner's is retired by a person and by nothing else.

### Hold an item that still owes an owner's ruling

Once the store knows which criteria are the owner's, the planner can stop spending on items that cannot finish. An item whose only unmet criterion is an owner's ruling gains nothing from a worker: the run would end, the gate would pass, and the item would still be waiting for the owner. On 2026-11-14 an item like that was unblocked early and dispatched. This block makes the planner hold such an item before any lane is used, and makes the waiting face say plainly that the owner is the one it waits on.

A note for the next run on this block: commit `e476f99f5` [unlanded] never reached the trunk, so do not cite it as evidence; describe the behavior it built in words instead (the planner holds the item with `owner-only-owed` before leasing a lane, and the waiting face names the owner and the owed criterion), and build afresh from the current trunk tip, because the earlier candidate is stale against trunk.

The answer to the conflict the last worker raised: nothing in this block's design blocked that run. It reported blocked because it started the full test suites as a background task and ended its turn while they ran, with the work uncommitted. A run's final message ends the run, and nothing resumes it afterwards, so a background task outlives nothing. Run every verification command in the foreground and wait for it to exit, with a command timeout long enough for the suite (the sharded runner, `python3 tools/go-test-shards.py <module> -k 6`, for `tools/tillr-drain` and for `tools/tillr-view`). Then run `just pack-sync`, commit once, and only then write the report. Report blocked only for a design question this block does not answer; a suite that is still running is not one, and a suite that fails is a repair to make or a failure to report.

What "build afresh" means, measured on 2026-11-17 with `git merge-tree --write-tree` replaying the earlier attempt (kept at `refs/tillr/preserved/run-item-jl-owner-criterion-hold-a1-cfix`) onto the trunk tip: the replay conflicts in `tools/tillr-drain/drain/plan.go` and `tools/tillr-view/waiting.go` (and in the pack copy of each), and it modifies `tools/tillr-drain/seed_test.go`, which the trunk has since deleted. So do not cherry-pick or merge that ref. Read it as a reference for the shape of the change (a planner hold in `tools/tillr-drain/drain/plan.go`, its reason in `why.go`, the waiting-face class in `tools/tillr-view/waiting.go`, the count in `tools/tillr-view/dispatchable.go`), write the change by hand against the files as they are now, and put the tests it carried in `seed_test.go` in a test file that exists. Where the waiting face's `trunk-broken` wording differs, the trunk's wording stays. Never hand-edit the pack copy; `just pack-sync` regenerates it.



### Prevent a failed landing from freezing the item's criteria

Landed. A landing that did not take is recorded against the run like a failed gate, and the item's criteria are left alone.

### Add the waiter and reason to every blocked item

Landed. A move to blocked stores who the item waits on and why, and remembers the state the item was in so that unblocking restores it.

### Prevent landed and resolved states without their evidence

The board's two "finished" states are claims about the world. `landed` claims this item's work is on the trunk, and `resolved-elsewhere` claims something else satisfied it. Today either can be written by one command with nothing to back it. This block makes the store ask for the evidence: a landing record and finished criteria for the first, a pointer to the other path for the second.

Both states stay final, with the one exception the tree already has. When the full suite proves a landing broke the trunk, tillr reverts that landing, and the claim `landed` makes is no longer true: the work is off the trunk. The owner-ruled revert design in [tillr-graph-is-the-path.md](./tillr-graph-is-the-path.md) says the item "gets its work back" and "lands again", so the loop moves it from `landed` to `in-queue` through the state command, with a reason and the name of who moved it (`ReturnLanded` in `tools/tillr-drain/drain/heavy_revert.go`). That return is built and tested (`TestRevertedLandingReturnsToTheQueueWithAReceipt` in `tools/tillr-item/terminal_state_test.go`), and this block keeps it. Every other move out of a finished state stays refused.

Two conflicts have been raised against this block, and both are answered here.

The first: the third criterion used to demand that every move out of `landed` be refused, which contradicted that return. It now names the return as the one allowed move. The return also changes what counts as evidence. A landing that was reverted proves nothing about the trunk, so the first two criteria count only a landing record written after the item's latest return from `landed`. Without that, a returned item could be marked `landed` again on the strength of the landing that was undone.

The second, raised on 2026-11-19: the third criterion also said the tests in `tools/tillr-item/terminal_state_test.go` pass unchanged, and they cannot. Three of them reach a finished state in the way this block forbids. `TestTerminalProductQAReopening` moves an item with an open criterion and no landing record to `landed`. `TestTerminalSyncSkipsStaleBlockAndSyncsSibling` moves an item to `resolved-elsewhere` with no pointer, and to `landed` with no landing record. `TestTerminalLoopVerdictsThenLand` lands with no landing record. None of the three tests how the item got there; each tests what happens afterwards. So their setup changes and their assertions stay: the setup records the verdicts, inserts the landing record or passes `--resolves-via`, and the test then asserts what it asserts today. That is the rule for every test the new refusals break, in any module. Give the fixture the evidence, keep every assertion, and delete no test. The third criterion no longer says "unchanged", and the eighth states this rule.

Reading the tree for that answer found four more things the block left open. They are settled here so that the next run does not stop on them.

**Where the rules apply.** They apply to a store under the Git-landing definition, which is where `state-terminal` already applies. They cover every write that changes a stored item's state to `landed` or `resolved-elsewhere`: `tillr item state`, the library calls behind it that the loop also makes directly (`SetState` and `SetStateWithReceipt` in `tools/tillr-item/item/state.go`), and `tillr item apply` when its record changes the state of an item the store already holds. A record that creates an item already in a finished state is an import and is still accepted. Old stores and most test fixtures are seeded that way, and the fifth criterion depends on it.

**What a landing record is, to the item store.** The `landings` table is created by the lander's schema (`tools/tillr-land/land/schema.go`), and a store that only the item utility has written has no such table. That store holds no landing record, so the move to `landed` is refused as `landed-receipt-missing`; it is not an internal error. The close command already probes for the table this way (`Close` in `tools/tillr-item/item/lifecycle.go`). A test in `tools/tillr-item` creates the table itself with the columns the check reads, `item_id`, `result` and `event_seq`, and inserts the row; `lifecycle_test.go` creates a smaller one the same way for the close command. "Written after" compares event sequence numbers: the lander stores the sequence number of the landing's own event in the row's `event_seq`, and the record is current when that number is greater than the sequence number of the item's latest `item.update` event that took it from `landed` to `in-queue`.

**An item whose graph ends without a commit.** A workflow can declare that its item finishes with no commit (`requires_candidate: no_commit`). The loop moves such an item to `landed` although nothing landed (`completeGraphWithoutCommit` in `tools/tillr-drain/drain/workflow_exit_record.go`), so it has no landing record and never will. Before that move the loop records the criteria as `done` and appends a `drain.graph-completed` event that names the item. That event is this item's receipt: the first criterion accepts it in place of a landing record, under the same "current" rule. This is the designer's decision, and the owner may overturn it. The alternative is a different finished state for these items, which changes what the board shows for every such item and is larger than this block.

**The loop's other writes of a finished state.** The fence now includes `tools/tillr-drain/`, because a rule that lands without its callers stops the drain. There are three other writers.

- A landed candidate (`completeItem` in `tools/tillr-drain/drain/workflow_exit.go` and `recordLandedJudge` in `tools/tillr-drain/drain/judge.go`). The lander has written the landing record and the verdicts are recorded before the move, so the move passes and the code does not change. A refused move fails the step, as any move that does not take does today.
- A change the trunk already carries (`resolveElsewhere` in `tools/tillr-drain/drain/terminal_item.go`). It moves the item to `resolved-elsewhere` with no pointer today. It now passes `commit:<sha>` with the candidate's head, a reason and the loop's own name as the actor. When that move is refused as `already-landed`, an earlier landing of this item was recorded and its state move was lost, so the loop moves the item to `landed` instead.
- A trunk fixer resolved by a passing check (`tools/tillr-drain/drain/fixer_resolve.go` and `fixer_rebase.go`). Both already pass a `commit:` pointer and do not change.

The fence also includes `tools/tillr-land/`, `tools/tillr-loop/` and `tools/tillr-view/`, for test fixtures only: `tools/tillr-land/lifecycle_test.go` moves an item to `resolved-elsewhere` with no pointer, and any other test in those modules that the new refusals break gets the same fixture change.



### Add commands to close an item and reopen it

Landed. `tillr item close` records why an item was closed, who closed it and the state it was in; `tillr item reopen` restores that state; the lander refuses work for a closed item; and an item that has landed cannot be closed.

### Move AutoQA onto the ordinary verdict kinds

Landed. The store's AutoQA actor kind, claim class and decider are refused on new writes, and old records stay readable.

### Add the verdict's author to the item page

Landed. The item page shows who recorded each verdict, and raises the one alarm the state model asks for: an item waiting on something that has been closed.

### Add one intake for a judge's written verdicts

Landed. One command checks a judge's file before any of it becomes a record: that it covers every criterion, read the text the store holds, looked at the candidate it claims to, and cites evidence. The caller states who the judge was; the file is never trusted to.

### Route an opted-in item through a judge before the gate

Landed. An item that asks for a judge in its own block is judged after the worker commits and before the gate. The judge reads and never writes, and its rulings are recorded only after the landing.

### Route a judge's blocked and defective findings to their states

Landed. A judge's blocked finding moves the item to blocked, waiting on the waiter the judge named. A defective finding is recorded, and the item is closed with the judge's reason.

### Route a second failed attempt to the design seat

Landed, then replaced on 2026-11-17 by [`D-JL10`](#44-how-a-worker-runs-ending-moves-its-item-d-jl10). It sent an item to the design seat on its second failure. A failure is not counted: only an identical repeat goes to the design seat.

### Prevent cloning a held item under a new id

Landed. `tillr item sync` refuses a copy of a held item filed under a new id, so the `-v2` workaround of 2026-11-14 cannot recur.

<a id="return-a-design-runs-gate-refusal-to-its-own-session"></a>
<a id="route-a-design-runs-gate-refusal-to-its-own-session"></a>

### Route design gate refusals back to the same session

When the repo gate refuses a design commit, the designer needs the failing checks and their output to repair it. Starting a fresh design session for that repair forces another designer to reread the documents and reconstruct reasoning that may never have been written down. The owner ruled on 2026-11-17 that this refusal is feedback, not a failed design run. Return it to the session that wrote the commit so the designer can continue with the context already in hand.

Today the drain raises the conflict again and starts a fresh design run. This work connects the existing submission-and-response flow with the ability to resume a finished worker's session, applying both to the design seat. Each revised commit must go through the same allowed-path checks and repo gate as any other design answer. Return every refusal without a retry limit; only an objection that names the owner should reach the owner. If a design run is killed, resume its recorded session when it is still available. If that session is gone, start a fresh one and explain that choice in the conflict text.

- **Outcomes:**
  - Return each gate refusal to the original designer session and check its revised commit before landing it and answering the conflict.
  - Resume interrupted design work when its session remains available, or explain why a fresh session was needed.
  - Continue repairs without a retry limit and send only objections naming the owner to the owner.

**Agent Details:**

- **Ref:** [Section 4.3](#43-how-a-design-runs-ending-moves-its-conflict-d-jl9) of this document; [the landing call design](./tillr-landing-call.md); [the build-commit continuation](../reference/tillr-build-commit.md); [the designer sessions reference](../reference/tillr-designer-sessions.md#the-desk), whose desk already reuses a document's designer session.
- **Where:** the gate-refusal branch of `verify` and the continuation in `settle` (`resumeAfterWall`, `continuationStarted`) in `tools/tillr-drain/drain/loop.go`, whose one-continuation-per-attempt rule must not apply to a design run's refusals; `settleUnansweredDesignRuns` and `designReraiseText` in `tools/tillr-drain/drain/design_unanswered.go`, which raise a fresh design run today; `conflictDecision` in `tools/tillr-drain/drain/blocked.go`; the designer session choice in `tools/tillr-drain/drain/designer.go`.
- **Lane:** the refused run's lane is reset after the refusal today, so the block either keeps that lane and its session for the design run's next turn, or stages the continuation on another lane from the pinned commit at `refs/tillr/runs/<run>` with the recorded session. It states which and why.


