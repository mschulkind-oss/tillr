---
title: "The Item Judge and Lifecycle in Tillr"
status: in-review
stage: DESIGN
updated: 2026-11-24
tags:
  - tillr
  - judge
  - verdict
  - lifecycle
  - state-machine
  - drain
  - design
summary: "Who may record a verdict on an item's criterion, what an item's stored state means, and what each ending of a run does to its item. A judge is a step of the workflow and needs no grant, and a failed run is never moved to another model automatically. One question on seat escalation is open."
next: "Rule OQ-JL5 (does a worker's seat obstacle still move the item to a stronger model). Build the `delivery` obstacle of Section 4.8."
depends-on:
  - ../reference/tillr-item-graphs.md
  - escalation-ladder.md
---

# The Item Judge and Lifecycle in Tillr

> **Delivered copy.** This is `docs/design/tillr-judge-and-lifecycle.md` from this repository at commit `4f503801bb` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-judge-and-lifecycle.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** in review. The Built column of the [decision ledger](#decision-ledger) says what is in the code, and [Section 7](#7-what-is-built-and-what-is-owed) lists what is not. One question is open, [OQ-JL5](#OQ-JL5).

> **In short.** A criterion is retired only by an actor with authority over its class, and an item's stored state says what its owner wants done with it, never what a run is doing. Everything a run does short of landing is evidence returned to whoever can act on it: it neither retires a criterion nor spends a retry budget.

**Why it matters.** Without these rules a gate pass retires a decision only the owner can make, and a failed attempt freezes criteria nobody has judged.

**The shape.** One verdict intake shared by every judge; a claim class stored on each criterion and checked against the actor; six stored item states with `working` derived from live runs; and, for every way a run can end, one named mover.

**Cost.** There is no `autoqa` actor kind, no stored `in-progress` state, no clone of a held item under a new id, no `tillr drain rework` command, no retry count and no automatic move to another backend.

**Start at [Section 4](#4-a-failed-attempt-is-never-a-verdict-and-never-a-count)**, which says what each ending of a run does.

**Needs your ruling:** [OQ-JL5](#OQ-JL5).

**Reads with:** [tillr-item-graphs.md](../reference/tillr-item-graphs.md) (the graph and evaluator contract this judges inside), [the graph design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-graph-is-the-path.md#33-endings) (the closed set of next actors an objection may name), [the item-states reference](../reference/tillr-item-states.md) (the seven displayed states), [criterion-repair.md](criterion-repair.md) (repairing a criterion a verdict has frozen), and [tillr-autoqa.md](tillr-autoqa.md) (the external client of [Section 5](#5-core-boundary-for-external-autoqa-d-jl7)).

**Words.** [The vocabulary](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md) defines item, criterion, fence, gate, candidate, judge, seat, backend, objection and mover. This document also uses:

- **Lane**: a pool slot holding one worktree and one run.
- **Hold**: a wait together with the mover that ends it. A hold is derived from the store's records on every planner pass and is never a stored state.
- **Worker**: the run that builds an item's change.
- **Design seat**: the role that answers a question about an item's definition by editing the item's block or its design document. A **design run** is one agent run in that seat. It never builds the item.
- **Objection**: the ending by which a run says it should not continue and names who acts next.
- **Operator**: whoever runs tillr's commands at a shell. It is not a seat, and not an actor a run's report can name.
- **Trunk fixer**: an item the drain files to repair a check that is red on the trunk.

**Who decided.** A ledger row dated "owner" is the owner's ruling. Every other decision here is the designer's, and the owner may overturn it.

---

## 1. The Defects This Design Closes

| Defect | Cause | Closed by |
| :--- | :--- | :--- |
| **A gate pass retires an owner's decision.** | A claim class that lives only on the incoming verdict lets a mechanical actor claim `verification-only` for any criterion. | The class is stored on the criterion and checked against the actor ([Section 2.2](#22-when-generic-judging-survives--and-closing-the-mechanical-stamp-hole-d-jl2)). |
| **A failed attempt makes an item unrepairable.** | An attempt's outcome recorded as a criterion verdict freezes the criterion's text before any judge has read it. | An attempt writes no verdict ([Section 4.1](#41-attempt-outcomes-are-not-criterion-verdicts-d-jl5)), and a held item is never cloned ([Section 4.2](#42-sending-a-failed-item-again-under-its-own-id-d-jl6-d-jl8)). |
| **A client signs off without the check other agents pass.** | A client's identity encoded as an authority kind. | No such kind exists; a client is named in the verdict's `actor` field ([Section 5](#5-core-boundary-for-external-autoqa-d-jl7)). |

---

## 2. The Judge Runs Before the Gate, and Its Verdicts Are Recorded After the Landing

### 2.1 Topology Relative to Gate and Land (`D-JL1`)

Landing inside a graph is forbidden, and the gate and the lander run at most once, at the root graph's exit ([graph exit and evaluator routing](../reference/tillr-item-graphs.md#graph-exit-and-evaluator-routing)). In a workflow with an `evaluator_slot`, the judge leaf sits after every writing leaf and any in-graph check leaf, and before the exit:

```
[write leaves] ──► [optional check leaf] ──► [evaluator_slot leaf, read-only]
                                                   │ writes eval.output
                    ┌──────────────────────────────┴───────────────┐
              outcome "fail"                                 outcome "pass"
        (no gate, no landing)                                       │
   • not-done  → item stays in-queue; findings return               ▼
   • blocked   → item moves to blocked on the named waiter    gate → lander
   • defective → verdict recorded, item closed                      │
                                                    done verdicts recorded,
                                                    item moves to landed
```

- **Before the gate**, so that a judge catches an unaddressed criterion or a false premise before the gate spends suite time and before the trunk moves.
- **Recorded after the landing**, so that an item never reads as complete before it lands. If the gate or the lander refuses after the judge passed, no verdict is written, the item stays `in-queue`, and the refusal returns to the worker ([Section 4.4](#44-how-a-worker-runs-ending-moves-its-item-d-jl10)). Each `done` verdict carries the judged candidate head and the landed trunk commit.

**The judge's file and its intake.** A lane never opens the store. The judge writes `eval.output` in its run's artifacts, and the drain checks it before recording anything:

| Field | Requirement |
| :--- | :--- |
| `evaluated_head`, `evaluated_tree` | Match the candidate's head and tree, or the item's base when the workflow ends without a commit. |
| `verdicts[]` | Exactly one entry per stored criterion, each with the criterion id, the hash of its stored text, a verdict in `done`, `not-done`, `blocked`, `defective`, the claim class, a reason, and evidence that is a locator (artifact, diff, commit), never prose alone. |
| `actor`, `judged_actor` | Never read from the file. The drain stamps the judge's and the writers' run identities from its own records and refuses a judge that shares a run or a session with a writer (`checkIndependence`). |
| Judging step | The selected workflow's judging step and its declared classes bound an agent's verdicts ([Section 6](#6-the-judge-is-a-step-in-the-flow)). A verdict citing a `grant_id` is refused (`grant-retired`). |
| Aggregation | `pass` only if every criterion is `done`. On `fail`: any `defective` records that verdict and closes the item; else any `blocked` moves the item to `blocked`; else the item stays `in-queue`. |

### 2.2 When Generic Judging Survives — and Closing the Mechanical-Stamp Hole (`D-JL2`)

Exactly one of three routes judges an item:

1. **A workflow with an evaluator** (`planned-build@v1`, `build-judged@v1`, `design-draft@v1`): the evaluator leaf judges before the exit. No second judge runs after the landing.
2. **A workflow with no evaluator and only `verification-only` criteria** (`single-worker@v1`): a passing gate and landing retire the criteria mechanically (`actor_kind: "mechanical"`), at no judge spend.
3. **A standalone judge over the same intake**: an item that asks for one with `judge="agent"`, a commit made on the trunk outside the drain (`tillr item judge`), a premise check before any worker runs, or an audit of mechanically retired items.

**A mechanical actor cannot retire an `owner-only` or `direction` criterion.** Three rules hold that:

1. **The class is stored on the criterion.** It defaults to `verification-only` and is declared in the item's block with the attribute `claims`. It may harden toward `owner-only` and is never weakened by `tillr item apply` ([criterion-repair.md](criterion-repair.md)).
2. **The store checks the actor against the class.** A verdict claiming a different class than the stored one is refused (`claim-class-mismatch`); an actor without authority is refused (`claim-class-actor`):

   | Stored class | `mechanical` | `agent` | `human` |
   | :--- | :--- | :--- | :--- |
   | `verification-only` | Allowed, with a gate verdict and a commit receipt | Allowed, with independence and the judging step's class | Allowed |
   | `red-gate-triage` | Refused | Allowed only if the judging step names it | Allowed |
   | `direction` | Refused | Allowed only if the judging step names it | Allowed |
   | `owner-only` | Refused | Refused | Allowed (sole writer) |

3. **Untyped owner prose is refused, and an item that owes an owner's ruling is held.** `Apply` refuses a `verification-only` criterion whose text begins "Owner decision recorded" (`owner-criterion-untyped`). The planner holds an `in-queue` item with an unretired `owner-only` criterion as `owner-only-owed` ([below](#hold-an-item-that-still-owes-an-owners-ruling)).

---

## 3. The Owner-Disposition State Machine and Derived Execution State

### 3.1 Stored Owner Disposition vs Derived Execution Projection (`D-JL3`)

- **Six stored states.** `not-started` (a draft), `in-queue` (admitted to dispatch), `blocked` (waiting on a named waiter, with the prior state remembered), `landed` (this item's work is on the trunk), `resolved-elsewhere` (its intent was satisfied by another path) and `closed` (taken out of work with a reason, reopenable).
- **`in-progress` cannot be written** (`state-frozen`). Whether a run is working on an item is derived, never stored.
- **One projector.** `ProjectItemState` in `tools/tillr-view` emits the seven displayed states of [the item-states reference](../reference/tillr-item-states.md); `working` means unfinished, unblocked and with a running run. The dispatchable count is the planner's own rule, so the page and `tillr drain plan` cannot disagree.

### 3.2 Transition Preconditions and Attempt Holds (`D-JL4`)

Every move between stored states has a precondition the store checks, and a refusal names its rule.

| Move | Allowed from | Precondition (rule on refusal) |
| :--- | :--- | :--- |
| to `in-progress` | none | Always refused (`state-frozen`). |
| to `blocked` | `not-started`, `in-queue`, `blocked` | A waiter and a reason (`blocked-waiter-missing`). The waiter is `owner`, `design`, `plan` or `item:<id>` (`blocked-waiter-invalid`). |
| to `landed` | `not-started`, `in-queue`, `blocked` | Every criterion `done` (`landed-criteria-unmet`) and a current receipt (`landed-receipt-missing`). |
| to `resolved-elsewhere` | `not-started`, `in-queue`, `blocked` | A pointer: `item:<id>`, `commit:<sha>` or `doc:<path>` (`resolved-pointer-missing`). Refused when the item has a current receipt (`already-landed`). |
| out of `landed` or `resolved-elsewhere` | `landed` | Refused (`state-terminal`), except the return of a reverted landing to `in-queue` with a reason and an actor. |
| to `closed` | `not-started`, `in-queue`, `blocked` | A reason (`close-reason-missing`). Refused while the item has a current receipt (`already-landed`). The lander refuses a candidate for a `closed` or `resolved-elsewhere` item (`item-not-landable`). |
| out of `closed` | `closed` | Only by `tillr item reopen`, which restores the prior state (`closed-use-reopen`). |

A receipt is current only if it follows the item's latest return from `landed` to `in-queue`.

**Rework after a revert (`D-JL23`).** An item returned to `in-queue` after a revert keeps its landed row as a record of the original run. That row neither bars a close nor marks the item finished for the ref sweep, which keeps the pins of a retryable item.

**A failed run never changes the stored state.** `in-queue` says the item is wanted. What the last run did is a hold, with a reason and a mover from one table (`tools/tillr-drain/holdrules/holdrules.go`) that a test keeps complete. No reason is a count and none expires.

---

<a id="4-a-failed-attempt-is-never-a-verdict-and-never-a-count"></a>

## 4. A Failed Attempt Is Neither a Verdict nor a Charge Against a Retry Budget

### 4.1 Attempt Outcomes Are Not Criterion Verdicts (`D-JL5`)

A criterion's text freezes once a verdict is recorded against it, so that nobody lowers a bar a judge has graded. An attempt that did not land is not a grading: it writes no verdict and leaves the text editable.

Two endings do write a verdict: `done`, recorded only after the landing, and `defective`, a judge's finding that the criterion's premise is false, which closes the item with the judge's reason. [Criterion repair](criterion-repair.md) covers its frozen text.

### 4.2 Sending a Failed Item Again Under Its Own Id (`D-JL6`, `D-JL8`)

A failed item is worked again under the id it has, keeping its run history and its place in other items' dependencies.

- **Returned by the loop:** a refused or empty run goes back to its worker with the evidence ([Section 4.4](#44-how-a-worker-runs-ending-moves-its-item-d-jl10)).
- **Returned by a person:** `tillr drain retry` is the one command. A second command for the same act would be a second place to be wrong.
- **Never cloned:** `tillr item sync` refuses a copy of a held item under a new id.

### 4.3 How a Design Run's Ending Moves Its Conflict (`D-JL9`)

When a worker reports `RESULT: blocked`, the drain records a **design conflict** (the worker's report and who answers it) and dispatches one design run. **Owner ruling, 2026-11-17: no count applies to design runs, nothing is re-dispatched because an input changed, and an item reaches the owner only through an objection that names the owner.**

| Ending | What moves the conflict |
| :--- | :--- |
| 1. The gate refuses the design run's commit | The failing checks and output return to the same designer session, even when the refusal is identical. |
| 2. The run committed an answer to the item's block | The settlement reads the block. No guard or count is kept. |
| 3. The run was killed or interrupted | The recorded session is continued when available; otherwise a fresh one starts and the conflict says why. |
| 4. The run committed, but not to the item's block | A design commit that passes the gate and lands is the answer, and the item returns to its worker. A commit that removes the block returns to the design seat. |
| 5. The run committed nothing | An objection. Its report names the next actor with `NEXT-ACTOR: build` (build as written) or `NEXT-ACTOR: owner` (held for the owner, published as a numbered question in the item's design document). Naming no actor is a missing output and returns to the design seat. |
| 6. The run "ran out of time" | There are no timeouts. A run labeled `timed-out` with end cause `lease-reclaimed` is a killed run (ending 3). |
| 7. An identical repeat of an earlier design ending other than a gate refusal | The loop raises an objection naming the owner, with a question written from the two records. Two different endings are never a repeat. |

- **Identical** means the same cause and the same evidence: for a run naming no actor, the conflict and a digest of the block the next design run would be given; for a refusal, its normalized words. A killed run has no key.
- **No guessed change re-dispatches.** The drain compares neither the block, the document nor the trunk to raise a conflict. `tillr drain retry --item <id> --run <design run>` and the owner's recorded answer are the events that move it.
- **Every design-seat brief states the provenance rule (`D-JL19`)**: a designer's or operator's decision is never presented as the owner's ruling; a decision is attributed to the owner only by quoting a dated ruling ([the provenance review](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#6-how-this-stops-happening)).

The built rule and its tests are in [A design run that ends without answering](../../tillr-drain/README.md#a-design-run-that-ends-without-answering).

### 4.4 How a Worker Run's Ending Moves Its Item (`D-JL10`)

**Owner ruling, 2026-11-17: a worker's ending never spends a retry budget, and nothing re-runs because a hash changed. Each ending does the one thing it means, and an identical repeat goes to the design seat.**

| Ending | What moves the item | Built |
| :--- | :--- | :--- |
| 1. The gate refuses the candidate, the run commits nothing, the candidate touches protected state, or the landing is refused | The ending returns to the item's worker with its evidence, without a retry limit: the same session on the retained lane where the adapter can resume it, under a distinct run record. An unavailable session or lane starts a fresh run with the evidence and a recorded reason. | Built. |
| 1a. The landing conflicts (`squash-conflict`) | The conflict returns to the producer with the conflicting paths on the latest trunk. On the second identical conflict in a retry cycle the drain discards the candidate and starts the workflow fresh from the current trunk; if that attempt conflicts again the item parks for the operator (`D-JL12`). | Built. |
| 1b. A workflow evaluator finds a criterion `not-done` | Every unanswered finding returns to the worker with its full reason. The item is held for the operator only when a finding repeats on a candidate whose cited files did not change. | Built. |
| 2. An identical repeat: the same producer, cause and evidence as an earlier ending | The item goes to the design seat with both runs' records. Evidence is the failing check ids with a digest of each normalized output, the uncommitted paths with a digest, or the refusal's normalized words. | Built. |
| 3. The run exits with uncommitted work | Its session is continued while each continuation changes that work; one that changes nothing is refused as `candidate-no-op` (ending 1). | Built. |
| 4. The run reports `RESULT: blocked` | It must name a complete obstacle ([Section 4.5](#45-what-a-worker-may-end-with-d-jl11)); otherwise it is ending 1. | Built. |
| 5. The run was killed or interrupted | It runs again, uncounted. | Built. |
| 6. An input changed (block, fence, policy, tillr build) | Nothing. | Built. |
| 7. An authorization names a run that is not the item's latest | It is ignored. | Built. |
| 8. The machinery failed | The item holds on its own backend with the measured cause ([Section 4.15](#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27)). An adapter's refusal of a brief holds the item, not the backend (`D-JL17`). | Built. |

A count is a guard built around bugs; the bugs are fixed instead. The built rule and its tests are in [A refused run goes back to its worker](../../tillr-drain/README.md#a-refused-run-goes-back-to-its-worker).

**A repeated landing conflict restarts from the current trunk (`D-JL12`, owner ruling).** A fresh attempt's brief carries the item's source reference, no conflict-fix instructions, and the paths that conflicted. `tillr drain retry` starts a new retry cycle.

### 4.5 What a Worker May End With (`D-JL11`)

**Owner ruling, 2026-11-18: a worker is handed no sanctioned way to end without delivering.** Measured over 61 drain runs of one backend whose brief offered `RESULT: failed`: 19 ended that way, most after a few read-only commands. So a worker run has two endings: the commit, or a named obstacle of one closed set of kinds, each with required fields and its own route. None sends the same worker the same brief again.

The report names the obstacle with `OBSTACLE: <kind>`, the kind's fields each on its own line, and `RESULT: blocked`.

| Kind | When | Required fields | What the loop does |
| :--- | :--- | :--- | :--- |
| `definition` | The item's words are wrong or contradict the tree | `CRITERIA`, `EVIDENCE` | Raises a design conflict ([Section 4.3](#43-how-a-design-runs-ending-moves-its-conflict-d-jl9)). |
| `trunk` | The base is red, and this change did not cause it | `CHECKS`, `BASE` | Replays the named checks on the base. All red: a trunk fixer is filed or joined and the item waits on it. Otherwise the run returns to its worker with the replay's result. |
| `tooling` | A tool or the harness is defective | `COMMAND`, `EVIDENCE`, `PATHS` | Files a defect item named by a digest of the command, and the item waits on it. |
| `seat` | A tested attempt shows the task exceeds this model | `TRIED`, `MISSING`, and a changed worktree | Moves the item to the next backend on the policy's `escalate` list; past the end, a design conflict asks for a plan or a split. Whether this route stays is [OQ-JL5](#OQ-JL5). |
| `owner` | A decision only the owner can make | `QUESTION` | Raises a design conflict carrying the question. A worker never raises a direct owner hold. |
| `delivery` (not built) | The change belongs to a fork the run was not staged for | `FORK` | Restages the item as a fork piece ([Section 4.8](#48-planning-time-fork-delivery-the-delivery-obstacle-and-patch-branch-refusal-d-jl15)). |

- **`fence` is not a kind.** A report naming it is a missing output. The common deny-list is a pre-gate check on the candidate's committed change, not an obstacle.
- **Not an ending:** `RESULT: failed`, an incomplete obstacle, a kind outside the set, or stopping with neither commit nor obstacle. Each is a missing output returned to the same session; in the pi adapter the check runs inside the session. A patch that does not apply, a merge conflict, a failing test in the changed work and a long suite are work to continue.
- **A generated repair item** (a trunk fixer or a defect item) has no design block, so its `definition`, `owner` or `tooling` obstacle goes to the owner. A trunk fixer may not name `trunk`.
- **A request that says when nothing is to change.** When the request's words name that condition and the worker's report shows it with commands and results, the item is closed with that evidence and no commit. On `planned-build@v1` the condition need not be pre-stated ([Section 4.21](#421-an-investigation-that-finds-nothing-to-change-ends-inside-tillr-d-jl34)).
- **In a multi-node workflow**, a node's blocked report takes the design route whatever its kind; the `trunk`, `tooling` and `seat` routes apply to single-worker runs.

The code is `obstacle.go` and `obstacle_route.go` in `tools/tillr-drain/drain/`, the **Report shape** of `tools/tillr-run/backends/brief-template.md`, and `tools/tillr-run/backends/pi/keep-going.ts`; both readers are tested against `tools/tillr-run/backends/pi/testdata/obstacle-cases.json`.

### 4.6 A Held Item Never Occupies a Lane (`D-JL13`)

**Owner ruling, 2026-11-19: every lane is full while anything is schedulable.** So a held item does not reserve a lane. When an item goes on hold with unlanded work in its lane, the drain saves the work under a ref it owns and records the ref on the item, returns the lane to rotation in the same pass, and restages the work on whichever lane the item next starts on. Every view shows where the held work is kept.

- **Released with their work saved:** a pinned candidate whose gate reached no verdict, held multi-node workflows with their artifacts, fork checkouts with their base refs, and quota-paused runs.
- **Kept, with the reason reported** (`KeepReason` in `tools/tillr-drain/lanestate`): a boundary violation, a lane head no record names, and a hold with no recorded candidate to restore.
- **Kept under an alarm:** a finished change waiting for its judge ([`D-JL27`](#the-lane-release-for-judge-waits-is-dropped-d-jl27)).
- **Never** reset or release a lane under a live run. A failure to save, reset or reap is a lane fault, not a reservation.

### 4.7 Offered Commits, Own-Candidate Lanes, Unlaunched Claims, and Named Hold Movers (`D-JL14`)

An item is not stranded when its next step is offered the lane holding its own unlanded change.

1. **Every offered commit is pinned when offered**, at `refs/tillr/runs/<run_id>`, so none is reachable from a lane branch alone.
2. **An item's next node may start on, or restage from, its own candidate.** `lane-holds-unlanded-change` is never raised against the item's own commit.
3. **A claim that could not launch is retracted**, with every store effect it wrote; the item is offered again in the same pass and the lane returns to rotation.
4. **Every hold names what was found and a concrete mover.** "Cause not recorded" is neither. A change to a mover changes what the waiting views display, so those change in the same commit.

### 4.8 Planning-Time Fork Delivery, the `delivery` Obstacle, and Patch-Branch Refusal (`D-JL15`)

**Owner ruling, 2026-11-19: an item cannot be mis-filed in a way that leads to bad downstream work; fork handling is decided in planning and execution** (paired with [`D-PT9`](tillr-planning-node-model-tiering.md#59-deciding-fork-versus-repository-delivery-at-plan-time)).

1. **No path list at filing.** `tillr item request` ignores `--path` with a notice. Built.
2. **Delivery is classified before the build.** The `plan` step of `planned-build@v1`, or a short read-only first step on a single-worker item, decides whether the change delivers through repository files, one named fork, or both in order (`delivery.go`). Built.
3. **`OBSTACLE: delivery` restages automatically.** Not built: `rg -n delivery tools/tillr-drain/drain/obstacle.go` returns nothing.
4. **A worker never delivers a fork itself.** A candidate whose run moved a fork's delivery branch is refused before the gate. Built.

### 4.9 Keeping a Live Trunk Fixer's Work When a Rerun Passes (`D-JL16`)

A check that fails and then passes with no change between is an intermittent failure, not a repair.

1. **A live fixer finishes.** When a proof rerun covers an open fixer's checks, the passing proof is recorded once and releases the items waiting behind the fixer. A fixer whose run is live, or whose latest run left a committed candidate, stays open: a commit is gated and landed as the fixer's own change, titled as a fix for an intermittent check; no commit resolves the fixer as `resolved-elsewhere`.
2. **A resolved fixer's runs are settled**, so none shows as live.

[Section 4.23](#423-a-later-green-proof-resolves-a-trunk-fixer-whose-worker-has-finished-and-whose-candidate-waits-to-gate-d-jl36) narrows the first rule to a rerun on the red commit itself.

### 4.10 Bounded Run IDs, Per-Item Staging Holds, and Adapter Preflight Refusals (`D-JL17`)

1. **A run ID's length is bounded.** Continuations are one numeric counter over a digest-bounded stem (`markRunID`, `boundRunIDBase`), never repeated words; a test pins an ID under 120 characters after 100 continuations.
2. **A staging failure of one run holds one item.** The claim is retracted, the lane freed, and the item held as `stage-failed` with the operator as mover; the loop keeps running.
3. **An adapter's refusal of a brief is the item's fault, not the backend's.** An adapter that exits 2 with `run-<adapter>: refused: …` holds the item as `adapter-refused` and closes no backend. No attempt limit is involved.

### 4.11 Instant Start Decisions From a Background-Maintained Queue View (`D-JL18`)

**Owner ruling, 2026-11-19: what to start is an instant decision from a list that may be up to 30 seconds old, and a re-rank never cancels a decided start.**

1. **A start view is kept in memory** (`start_view.go`): queue order, each item's holds, and free lanes, updated from the loop's writes, other processes' store writes, a filesystem watcher on tracked documents, and ref updates. Reads happen off the view's lock.
2. **A start is decided when a lane frees or an item is filed**, without waiting for a planning pass. Everything that must be exact (the item is not running, no hold was recorded, the backend is open, the lane is free) is checked in the one short write transaction that claims the lane. The claim latency is recorded as `claim_ms` and shown by `tillr view passes`.
3. **Re-ranking or a trunk advance affects only later decisions.** Settling, judging and proof stay off the start path.

Measured by filing an item against a copied store in a scratch repository: filing-to-start took 406–752 ms with the start view and 2.4–3.6 s without. No fixed millisecond target applies. A latency measurement never constructs a loop against a live checkout or its store.

#### What to split and how to judge it

Work of this size lands as independently landable slices, each naming its files, behavior and proof.

### 4.12 Verify exits without a verdict; gate refusals without detail (`D-JL20`)

A missing `eval.output` is a consequence, never a cause. The drain records the verify run, its exit code and that it wrote no verdict, keeps the candidate, and resumes at verify, not at plan:

- **The adapter refused the brief:** hold the item as `adapter-refused` (`D-JL17`); resume at verify once cleared.
- **The verifier ran and wrote nothing:** return the candidate to verify. An absent verdict is not an unreadable one and not a finding against the candidate.

Every gate refusal has detail: the failing check, or that the gate produced no verdict and where its log is.

### 4.13 What the attempt number and refusal counts tell the operator (`D-JL21`)

- **The attempt number** starts at 1 and advances by one on each redispatch after a refusal, conflict or hold. Waiting, a planner pass, progress to the next node and a restart of an interrupted run do not advance it. Only a node record with a recorded run behind it counts; abandoned claims are cleared at the next claim.
- **Refusal tallies** for verify, gate, fence and conflict are shown per item by `tillr drain why` and the item page, counted when the refusal occurs.
- **Neither routes anything.** Routing follows the ending and its evidence.
- **The delivered copy is not a separate piece.** The copy of `tools/tillr*` under `yolo-packs/tillr/` is regenerated in the commit that changes the source; a piece asked only to regenerate it and finding nothing to change is finished.

### 4.14 When a workflow restart is claimed but never starts (`D-JL22`)

A claim is not a running successor. An interrupted node restarts on the same backend, or holds as `backend-closed` until that backend reopens. A claim that did not launch is recovered under `D-JL14`: its lane is released and the node offered again without an operator retry.

### 4.15 No Automatic Move to Another Model (`D-JL24` to `D-JL27`)

**Owner ruling, 2026-11-20: no fallbacks. A failure is diagnosed and fixed, not routed around by trying something else, and a judge whose backend cannot run is an alarm that gets fixed.** Escalation on a worker's `seat` obstacle is a different trigger and is [OQ-JL5](#OQ-JL5).

#### A failed run holds with its cause (`D-JL24`)

The drain never moves a run, a node or an item to another backend because something failed. A seat runs on one backend the policy names, and changing it is a person's edit.

1. **An infrastructure ending holds the item on its own backend** as `backend-closed`, stating the run, the backend, the exit code or end cause, and the harness's last error words. It closes the backend ([recorded backend availability](../../tillr-drain/README.md#recorded-backend-availability)).
2. **The operator is the mover.** A successful `tillr-drain probe-backend`, or a later run on that backend ending outside the infrastructure class, reopens it. A restart, a policy edit or elapsed time reopens nothing ([Section 4.24](#424-diagnosing-a-repeated-failure-the-unbuilt-mechanism)).
3. **When the backend reopens, the same node runs again on it.**
4. **An identical infrastructure ending of the same item holds for the operator** with both runs' records: the fault is that item's run, not the backend.
5. **The policy.** A seat names one backend; there is no `preference` key, and a seat listing several backends without shares is refused. Shares ([the drain README](../../tillr-drain/README.md#seat-backend-shares-and-selection)) are a written choice of where a new item starts.

Same-backend restarts are ruled separately, in [the fallback inventory](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-fallback-inventory.md#3-the-inventory).

#### A backend or a judge that cannot run is an alarm (`D-JL25`)

An **alarm** *(coined here)* is a statement that part of tillr itself cannot run, shown ahead of every item on every operator face until the fact that raised it is gone. A wait is about one item; an alarm is about the machinery and lists the items behind it.

| Alarm | Raised by | Cleared by |
| :--- | :--- | :--- |
| **Backend closed** | The backend's latest recorded ending is in the infrastructure class | A successful probe, or a later run ending outside that class |
| **Backend out of quota** | Its quota window is shut, or its reading is stale or failing | The stated reset time, or a fresh reading under the reserve |
| **Judge cannot run** | The review seat's backend is under one of the two alarms above | That backend's alarm clearing |

- **Derived, not stored.** Nobody raises or clears one by hand.
- **Shown first** in `tillr view status`, `tillr drain why`, `tillr view waiting` and the Observatory header, with the cause, the items and lanes behind it, and the operator's command. `tillr view alarms` exits 1 when any is active.
- **The hold rules have a class `alarm`**, holding `backend-closed` and `backend-window`.
- **The loop keeps running**, and nothing is sent elsewhere to get around an alarm.

#### Quota exhaustion is not an exception (`D-JL26`)

A run refused for quota waits for its own backend's window and resumes there; a first-message refusal waits as `backend-window`. No other backend continues it. The alternative is a person's change to the policy ([`OQ-LD2`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-live-drain-config.md#OQ-LD2), [seat profiles](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-seat-profiles.md)). No drain policy enables an automatic alternative.

#### The lane release for judge waits is dropped (`D-JL27`)

A finished change waiting for a judge under an alarm keeps its lane and its inputs, and the alarm prints the lanes it holds. Releasing risks losing inputs the judge needs, for a state meant to last minutes. Every other hold of [Section 4.6](#46-a-held-item-never-occupies-a-lane-d-jl13) is released.

### 4.16 An operator retry that waits for a lane says so (`D-JL29`)

- **An authorized retry carries its own reason**, `operator-retry`.
- **A retry the plan can start names its lane.** One it cannot start says "operator retry waiting for a lane" and names each eligible lane with its holder, or says none is offered and that the mover is `--lanes`.
- **The JSON reason of a lane wait stays `no-lane`**, so a reader of that field is not broken.

### 4.17 A backend wait holds a lane only for work that is in it (`D-JL30`)

One fact decides: an item waiting for a closed backend holds a lane only when it has something in that lane (an unlanded commit, an uncommitted change, staged workflow output, fork work, or a live run).

- **An item that has never run holds nothing**; the plan holds it before any lane is offered.
- **A first-message refusal that changed nothing releases its lane** in the pass that records it, and the node is planned onto any free lane at the reset.
- **No other backend and no timer.**
- **A backend wait is never reported as `no-lane`**; `no-lane` means every eligible lane has a named holder.

### 4.18 A fork piece's brief states the write scope that staging enforces (`D-JL31`)

A **fork piece** is a node staged on the lane's fork checkout. Staging narrows the node's write scope to paths inside that checkout (`forkFence` in `delivery.go`).

1. **On a fork piece the fork checkout fence is the write scope.** A repository path in the node's recorded scope grants and forbids nothing there.
2. **The brief states one scope, the one staging enforces.** When narrowing leaves no path the brief prints no node line. A repair node's brief follows the same rule.
3. **A brief that contradicts itself is a tool defect**, routed as `tooling`, not `definition`: nothing in a design document reaches a build worker's brief.
4. **A planned delivery names exactly one fork.**

### 4.19 A worker returned by a design answer starts from a trunk that holds the answer (`D-JL32`)

A **landing conflict** is a candidate that does not apply when the lander squashes it; it returns to its producer and never dispatches the design seat. A **design conflict** is the record of a blocked worker ([Section 4.3](#43-how-a-design-runs-ending-moves-its-conflict-d-jl9)).

A worker returned by a landed design answer is staged on a commit that contains the answer, whichever run landed it, including an answer that landed through conflict-fix runs. `designAnswerLanded` in `lane_base.go` finds that commit by the head of the candidate that answered the conflict, and does not read a run id suffix.

Two causes are not closed by this rule:

- The returned worker's brief does not name the design answer.
- A repeated obstacle is recognized only when its `EVIDENCE` text is identical (`repeatedAnsweredObstacle`).

### 4.20 Leftover Work in a Lane Is an Alert, Never an Automatic Sweeper (`D-JL33`)

**Owner ruling, 2026-11-21: leftover work is an alert to clean by hand and trace to its cause; a sweeper would cover over the defect that left it.**

1. **Nothing in tillr clears a lane's leftover work automatically.** Saving a live item's work when it goes on hold is a known transition, not a sweep.
2. **Leftover work raises a `tillr-watch` alert.** Staging refuses the lane (`drain.lane-refused`), the watch rule `drain-lane-refused` in `config/tillr/watch/` reports it ([OQ-TW2](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-watch.md#OQ-TW2)), and the lane returns to rotation once the condition clears.
3. **Fixing a transition that leaves work behind is wanted**, and is distinct from sweeping.

Owed: `clearWorktree` in `stage.go` still removes uncommitted files from a lane whose head the trunk contains, and `discardTerminalLane` in `preserve_hold.go` resets a terminal item's lane.

### 4.21 An Investigation That Finds Nothing to Change Ends Inside Tillr (`D-JL34`)

A worker whose reproductions show the repository needs no change ends with `no_change` on `planned-build@v1`, whether or not the request pre-stated that outcome.

1. **Where it is offered.** The `plan` stage and the unsplit `work` stage of `planned-build@v1`, as the `no_change` tool ([the landing-call design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-landing-call.md#37-a-no-change-finding-is-a-third-tool-on-the-planned-build-workflow)) or the footer `RESULT: no_change`. A commit ending still requires a commit.
2. **What it carries.** Three non-empty fields: `cause`, `evidence` and `why_no_change`. It is refused when a commit exists or the worktree is dirty.
3. **It never self-closes.** The finding goes to `verify`, which reproduces the claim at the base commit against every criterion and may not cite the worker's finding as its own evidence.
4. **On a pass** the drain records the verdicts, stores the finding on the item (shown by `tillr item show`), and closes the item with reason `no_change: …`. The gate and the lander do not run, and no alert fires. **On `not-done`** the findings return to the stage that made the claim.

### 4.22 Post-Verdict Gate-Failure Replay Never Reports `run-live` or Runs Whole-Tree Go Tests (`D-JL35`)

When a candidate's gate fails, the drain replays the failing checks on the candidate's base, in a scratch checkout, to tell the candidate's failure from a trunk that is already red. The candidate's lane is released first.

1. **The Go replay is scoped.** It runs only the failing tests the gate log names; without names, the affected-package selection over the candidate's changed paths. Replay children carry `TILLR_RUN_ID` and `TILLR_ITEM_ID`.
2. **The wait has its own reason, `gate-replay`.** A run with a terminal record or a final gate verdict and no live worker never reports `run-live`.
3. **No timer.** When the replay settles, the refusal is recorded and the next pass returns the item to a worker.

### 4.23 A Later Green Proof Resolves a Trunk Fixer Whose Worker Has Finished and Whose Candidate Waits to Gate (`D-JL36`)

A **later green proof** is a proof on a commit that descends from the fixer's red commit; a **same-commit rerun** is a proof on the red commit itself.

- **Later green proof:** commits have landed between red and green, so a fixer whose worker has finished and whose candidate waits to gate is marked `resolved-elsewhere` and is never gated or landed. Its candidate is preserved under `refs/tillr/preserved/<run_id>` and its lane is reset before the examination is recorded.
- **Same-commit rerun, or a fixer still running a worker:** [Section 4.9](#49-keeping-a-live-trunk-fixers-work-when-a-rerun-passes-d-jl16) applies unchanged.

### 4.24 Diagnosing a Repeated Failure: the Unbuilt Mechanism

[Landing principle 11](landing-principles.md#11-a-failure-is-diagnosed-not-waited-out) rules that
nothing re-runs until a named change has happened, or a clearing condition stated by the failure
itself has been verified. Its *Enforced by* paragraph is the statement of record for what the drain
does. The worker-run path is built as [Section 4.4](#44-how-a-worker-runs-ending-moves-its-item-d-jl10):
a refusal goes back to the worker with its evidence, and only an identical repeat goes to the
design seat. This section holds the rest of the mechanism, none of which is built
(`rg -n 'inputs_hash|InputsHash|inputsHash|fleetTrip|FleetTrip' tools -g '*.go'` returns
nothing): the fingerprint store, the fleet trip, the drain's own probes and the diagnosis
ticket. [Roadmap entry 13](../tillr/roadmap.md) sequences it.

Two terms are coined here. The **fingerprint** is a hash over the failing stage and the
normalized error lines, with timestamps, run ids, ports and paths containing uuids stripped, so
two runs whose errors differ only in when they happened share one. The **inputs hash** is a hash
over the lane model string, the provider, the names (never values) of relevant environment keys,
the item's brief revision, the base tip and the gate's identity.

#### The four tests

On every failed run the drain records the failing stage, the child's final error lines quoted
from its log, the exit code, the duration, whether a candidate was produced, the inputs hash and
the fingerprint. Then, in order:

| # | Test | Question | If yes |
| :--- | :--- | :--- | :--- |
| T1 | Literal cause | Does the quoted error name a precondition of running at all (model resolution, authentication, a missing binary, a permission, a full disk)? | Machinery fault: diagnosis. The item is charged nothing |
| T2 | Repetition | Has this fingerprint occurred on this item with no change to the inputs hash since? | Deterministic by observation: diagnosis |
| T3 | Fleet recurrence | Has this fingerprint occurred on three or more distinct items in the current drain run? | Common cause: one fleet diagnosis, and admission of further lanes under the same inputs hash is suspended until the ticket clears |
| T4 | Verdict | Did the lane produce work that a gate or judge assessed and refused? | A verdict about the work: the existing path for a judged refusal |

- **T1 is a table of quoted strings, not an inference.** A row is added only by quoting a real
  child log and naming the component that emitted it.
- **T2 and T3 make determinism a measurement.** No classifier labels a failure transient or
  deterministic in advance.
- **A first failure that passes no test is unknown, and is not re-run as a test.** It goes back to
  its own session with its evidence, as Section 4.4 states. On the worker-run path a changed inputs
  hash on its own re-runs nothing.

#### Admission after a failure

An item whose last run failed is admissible only if one of these holds:

- a named change has happened since that failure, in a field its diagnosis ticket named as the
  clearing condition;
- the failure itself stated a clearing condition the loop can check (a `Retry-After` value, a
  quota reset time the provider quoted, a lock held by a named live holder), and the check found
  it clear. The wait lasts as long as the source stated. A stated duration with nothing to check
  is treated as unstated. If the same fingerprint returns after a verified-clear condition, the
  failure is diagnosed and not waited out again. The quota skip is this rule, and it is built.

Every other failed item is held, visibly, blocked by its fingerprint's ticket. The stored condition
of a hold is a ticket reference, never a timestamp. An item found held by a stored timestamp is
converted once: its last failed run is fingerprinted from the stored log and it is set blocked by
that fingerprint's ticket.

#### Diagnosis

Diagnosis produces a cause, not a fix, in three tiers, cheapest first:

1. **The drain's own probes, no agent.** Before a lane is admitted under an inputs hash the drain
   has not seen succeed, and whenever T3 trips, the drain checks once per inputs hash that the lane
   model string resolves, that the provider credential is present in the child's environment, and
   that the tools a lane calls exist. A failed probe is a complete diagnosis.
2. **One diagnosis run per fingerprint,** never per item, on the design seat, with its cost
   reported per failure. It may reproduce the failure and may not land a fix. It never runs under
   the inputs hash it is diagnosing.
3. **[The escalation ladder](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/escalation-ladder.md),** when the run cannot conclude or the remedy
   is a policy decision. A diagnosis that fails with the fingerprint it was sent to diagnose
   escalates at once; there is no second diagnosis run.

A diagnosis is complete when the fingerprint's ticket records the cause in one claim, the quoted
evidence, which inputs-hash fields the cause implicates, a checkable clearing condition ("wait a
while" is not one), and the item, configuration change or owner decision that would produce it.
When the clearing condition verifies, the ticket closes and every item blocked by it becomes
admissible. If no tier can run, the ticket stays open and the drain's output names the
fingerprint, the items held and "undiagnosed".

**Measured basis.** Under a stored 30-minute hold, 55 items were held behind one lane model string
that could not resolve; 383 of 1,083 child logs carried the same error line, and a re-admitted
item died 8 seconds after launch.

**Decisions.** All are the designer's except the first, which is the owner's `D-JL10`:

- No automatic retry as a test; a worker run's failure is never counted.
- Retry, retry budget, cooldown and expiring bench are one mechanism and are all removed. A
  durable hold is kept; its clock is not. **Rejected:** a cooldown kept "only for transient
  failures", because nothing can know in advance that a failure is transient.
- Determinism is established by observation (T1 to T3), never classified in advance.
- The loop dispatches the diagnosis itself, on the design seat.
- A wait exists only where the loop can check that the condition has cleared.
- Destinations are existing machinery: the deduplicated ticket of
  [principle 9](landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work) keyed by
  fingerprint, and the ladder of
  [principle 6](landing-principles.md#6-a-budget-escalates-it-does-not-loop). No ticket kind,
  failure vocabulary or escalation path is added.

---

## 5. Core Boundary for External AutoQA (`D-JL7`)

AutoQA is a client of tillr, not a part of it. The `autoqa` actor kind, the `qa-verification` claim class and the `autoqa` decider are refused on new writes (`actor-retired`); stored rows that carry them stay readable.

1. **Authority kind and client identity are two fields.** `actor_kind` (`mechanical`, `agent` or `human`) names the rule the store checks; `actor` names who ran, such as `autoqa/<suite>`. The item page shows both.
2. **An external client uses four existing faces only** ([the AutoQA design](tillr-autoqa.md)):
   - file an item with `tillr item apply`;
   - record a deterministic result with `tillr item verdict record` as `mechanical` and `verification-only`;
   - record an agent's review tied to the selected workflow's judging step;
   - escalate with `tillr item mail send` or a move to `blocked` on the owner.

---

## 6. The judge is a step in the flow

**Owner ruling, 2026-11-20: a judge is a step of the workflow that judges by that step's rulebook. Nothing is granted, and nothing expires.** There is no grant table, no grant command and no hold for an absent grant.

### Rulebook and its enforcement

The rulebook is the selected workflow definition in `config/tillr/workflows/` and its expanded graph, the judging step's instructions and inputs, and each criterion's stored class. A selected graph is persisted; editing the source does not rewrite it.

- **An evaluator stage carries a non-empty `judge_classes` list** drawn from `verification-only`, `red-gate-triage` and `direction`; `owner-only` is rejected. The shipped evaluators list only `verification-only`. A class is added by a reviewed change to the workflow definition, never by a request, a policy, model output or a clock.
- **The evaluator is read-only, required, and after every writer.**
- **Intake binds the verdict** to the persisted selection, the evaluator node, the item and the exact candidate head and tree, and checks that the class is in `judge_classes` and matches the stored class. Dry-run and write intake, `tillr item verdict record` and `tillr item judge` enforce the same rule.
- **A missing or incompatible judging step is a definition fault** naming the workflow and class, never a request to the owner. No verdict is invented when the evaluator does not answer.
- **A passed evaluator receipt survives the gate and the landing** without rejudging ([landing principles](landing-principles.md)).
- **No-slot workflows.** A workflow without an evaluator is judged by an agent only when the item asks with `judge="agent"`; that request is materialized as a read-only review evaluator in the selected graph before dispatch. An agent verdict with no judging step behind it is refused.

### Stores that hold grant rows

A store holding an `item_grants` table is migrated once, atomically: the rows are archived as inert data in one event, the table is dropped, and stored `item.grant` and `item.verdict` events stay readable byte for byte (`tools/tillr/store/judge_migration.go`). No criterion is reclassified or rejudged.

---

## 7. What Is Built and What Is Owed

The Built column of the [decision ledger](#decision-ledger) is the record. What is owed:

- The `delivery` obstacle kind ([Section 4.8](#48-planning-time-fork-delivery-the-delivery-obstacle-and-patch-branch-refusal-d-jl15)).
- An obstacle naming another item that must land first, which is not a kind.
- The two automatic-wipe gaps of [Section 4.20](#420-leftover-work-in-a-lane-is-an-alert-never-an-automatic-sweeper-d-jl33).
- The two unclosed causes of [Section 4.19](#419-a-worker-returned-by-a-design-answer-starts-from-a-trunk-that-holds-the-answer-d-jl32).
- The failure fingerprint, the fleet trip, the drain's probes and the diagnosis ticket ([Section 4.24](#424-diagnosing-a-repeated-failure-the-unbuilt-mechanism)).
- A worker's ending as one tool call on every adapter ([the landing call](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-landing-call.md)); where an adapter has none, the report footer is the contract.

A workflow's evaluator node is specified in [tillr-item-graphs.md](../reference/tillr-item-graphs.md#workflow-evaluator) and hands its file to the intake of [Section 2.1](#21-topology-relative-to-gate-and-land-d-jl1).

---

## 8. Build Order

The lifecycle rules are built. Four of them are cited by name from other documents and from the loop's messages, and are stated here.

### Hold an item that still owes an owner's ruling

An item whose only unmet criterion is the owner's gains nothing from a worker. The planner holds it as `owner-only-owed` before any lane is leased, and the waiting view names the owner and the owed criterion.

### Prevent a failed landing from freezing the item's criteria

A landing that did not take is recorded against the run like a failed gate, and the item's criteria are left alone.

### Prevent landed and resolved states without their evidence

`landed` claims this item's work is on the trunk; `resolved-elsewhere` claims something else satisfied it. The store asks for the evidence.

- **The rules cover every write** that changes a stored item's state to either: `tillr item state`, the library calls behind it, and `tillr item apply` on an existing item. A record that creates an item already in a finished state is an import and is accepted.
- **A landing record** is a row of the lander's `landings` table naming the item. A store without that table holds none, and the move is refused as `landed-receipt-missing`.
- **A reverted landing is not evidence.** When the full suite proves a landing broke the trunk and tillr reverts it, the loop returns the item from `landed` to `in-queue` with a reason and an actor (`ReturnLanded`). Only a record whose event follows that return is current.
- **An item whose workflow ends without a commit** (`requires_candidate: no_commit`) has no landing record. The loop records its criteria `done` and appends a `drain.graph-completed` event naming the item, which is its receipt.
- **A change the trunk already carries** is resolved with `commit:<sha>`. If that is refused as `already-landed`, the item is moved to `landed`.

<a id="return-a-design-runs-gate-refusal-to-its-own-session"></a>

### Route design gate refusals back to the same session

**Owner ruling, 2026-11-17:** a gate refusal of a design commit is feedback, not a failed design run. The failing checks and their output return to the session that wrote the commit, without a retry limit, and each revised commit passes the same checks as any design answer. A killed design run resumes its recorded session when available; otherwise a fresh one starts and the conflict says why ([Section 4.3](#43-how-a-design-runs-ending-moves-its-conflict-d-jl9), [the designer sessions reference](../reference/tillr-designer-sessions.md#the-desk)).

---

## Open Questions

1. 💬 **OQ-JL5: When a worker reports that a task exceeds its model, does the drain still move the item to a stronger model by itself?**

   A build worker that has made and tested an attempt can end with a `seat` obstacle, and the item then runs on the next model on a list written in the policy file (`escalate`). You ruled that a model that errors is never swapped for another. This is a different trigger: the first model did not fail, it said the work is beyond it, and the list it climbs is one a person wrote. Three of the tracked policy files list one backend there, so nothing escalates under them; the others list two or more.

   - **A — Remove it.** A `seat` obstacle with a tested attempt goes to the design seat, which answers with a plan or a split, as it does past the end of a list. A stronger model is used only when a person changes the policy. The `escalate` key is removed.
   - **B — Keep it.** A tested `seat` obstacle moves the item to the next model on the list. The list is the deliberate choice, made once, and the worker's report is the diagnosis that uses it.
   - **C — Keep the list, ask each time.** A tested `seat` obstacle holds the item for the operator, showing what was tried and the next model, and one command sends it there.

   <!-- vantage: question id=OQ-JL5 leaning="A — Remove it: a seat obstacle goes to the design seat for a plan or a split" -->

   _Leaning:_ **A**, because it is what happens under a one-entry list and it is the closest reading of the ruling that a failure is fixed and not routed around. It is a leaning only: B is a defensible reading of an escalate list as a deliberate choice.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

A row dated "owner" is the owner's ruling. Every other row is the designer's decision, which the owner may overturn.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-JL1` | A judge runs after the writers and before the gate. One intake checks its file, the drain stamps the judge's identity, and `done` verdicts are recorded only after the landing. | 2026-11-14, designer | [Section 2.1](#21-topology-relative-to-gate-and-land-d-jl1) | ✅ |
| `D-JL2` | No second judge runs after the gate when a workflow has an evaluator. The claim class is stored on the criterion; a mechanical actor retires only `verification-only`, and `owner-only` is retired by a person. | 2026-11-14, designer | [Section 2.2](#22-when-generic-judging-survives--and-closing-the-mechanical-stamp-hole-d-jl2) | ✅ |
| `D-JL3` | `in-progress` is not a stored state. Six stored states say what the owner wants; `working` is derived. | 2026-11-14, designer | [Section 3.1](#31-stored-owner-disposition-vs-derived-execution-projection-d-jl3) | ✅ |
| `D-JL4` | Each move between stored states has a checked precondition, a landing wins over a close, and a failed attempt waits under a derived reason. | 2026-11-14, designer | [Section 3.2](#32-transition-preconditions-and-attempt-holds-d-jl4) | ✅ |
| `D-JL5` | An attempt that did not land writes no criterion verdict. | 2026-11-14, designer | [Section 4.1](#41-attempt-outcomes-are-not-criterion-verdicts-d-jl5) | ✅ |
| `D-JL6` | A failed item is worked again under its own id and is never cloned. | 2026-11-14, designer | [Section 4.2](#42-sending-a-failed-item-again-under-its-own-id-d-jl6-d-jl8) | ✅ |
| `D-JL7` | The store has no AutoQA vocabulary; an external client uses the ordinary verdict kinds and is named in `actor`. Replaces `D-AQA1`. | 2026-11-14, designer | [Section 5](#5-core-boundary-for-external-autoqa-d-jl7) | ✅ |
| OQ-JL1 | A single-node item whose criteria are all test-checkable is retired mechanically when the gate and the landing succeed. An agent judge runs only where a workflow or the item asks. For code landings this is overridden by the owner's review requirement of 2026-11-18 ([the diff-review design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-diff-review.md#decision-ledger)). **Rejected:** an agent judge on every item. | 2026-11-15, designer | [Section 2.2](#22-when-generic-judging-survives--and-closing-the-mechanical-stamp-hole-d-jl2) | ✅ |
| OQ-JL2 | The store refuses a judge that shares a run or a session with the writer. Whether the judge uses a different model is backend policy. **Rejected:** refusing a verdict on a shared model route, which would stop every judged item on a one-route setup. | 2026-11-15, designer | [Section 2.1](#21-topology-relative-to-gate-and-land-d-jl1) | ✅ |
| OQ-JL3 | Withdrawn: `D-JL10` rules that no count routes a worker's endings. | 2026-11-17, owner | [Section 4.4](#44-how-a-worker-runs-ending-moves-its-item-d-jl10) | — |
| `D-JL8` | A criterion's class is declared in its block with `claims`; an item asks for an agent judge with `judge="agent"`. No `tillr drain rework` command. | 2026-11-16, designer | [Section 4.2](#42-sending-a-failed-item-again-under-its-own-id-d-jl6-d-jl8) | ✅ |
| `D-JL9` | A design run's ending is never counted and nothing is re-dispatched because an input changed. An item reaches the owner only by an objection naming the owner. **Rejected:** a re-raise per input change; handing a second unanswered design run to the owner by count. | 2026-11-17, owner | [Section 4.3](#43-how-a-design-runs-ending-moves-its-conflict-d-jl9) | ✅ |
| `D-JL10` | No count of worker endings routes anything and nothing re-runs because a hash changed. Every refusal returns to the worker with evidence; only an identical repeat goes to the design seat. The verifier-repeat hold is the designer's. **Rejected:** bounded re-admission, a `conflict-exhausted` hold, any numeric threshold. | 2026-11-17, owner | [Section 4.4](#44-how-a-worker-runs-ending-moves-its-item-d-jl10) | ✅ |
| `D-JL11` | A worker run ends with its commit or a named obstacle of a closed set of kinds. `fence` is not a kind; `RESULT: failed` is a missing output. The principle is the owner's; the kinds, fields and routes are the designer's. **Rejected:** "stop and report" conditions; a direct owner hold from a worker; escalation by count or timer. | 2026-11-18, owner | [Section 4.5](#45-what-a-worker-may-end-with-d-jl11) | ✅ except `delivery` |
| OQ-JL4 | A design run's objection names `build` or `owner`; any other name is read as none. The set widens only with [the graph design's next actors](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-graph-is-the-path.md#33-endings). | 2026-11-18, designer | [Section 4.3](#43-how-a-design-runs-ending-moves-its-conflict-d-jl9) | ✅ |
| `D-JL12` | The second identical landing conflict in a retry cycle discards the candidate and restarts the workflow from the current trunk; a further conflict parks the item for the operator. **Rejected:** parking on the second conflict; another conflict-fix run. | 2026-11-20, owner | [Section 4.4](#44-how-a-worker-runs-ending-moves-its-item-d-jl10) | ✅ |
| `D-JL13` | Held work is saved under a drain-owned ref, the lane is released in the same pass, and the work is restaged on the next lane. A lane is kept only where the work cannot be restored, with the reason reported. The goal is the owner's; the mechanism is the designer's. | 2026-11-19, owner | [Section 4.6](#46-a-held-item-never-occupies-a-lane-d-jl13) | ✅ |
| `D-JL14` | Offered commits are pinned at once; an item's next node may use its own candidate's lane; an unlaunched claim is retracted in full; every hold names a mover. | 2026-11-20, designer | [Section 4.7](#47-offered-commits-own-candidate-lanes-unlaunched-claims-and-named-hold-movers-d-jl14) | ✅ |
| `D-JL15` | Fork or repository delivery is decided in planning and execution, never at filing. A worker never delivers a fork itself. | 2026-11-19, owner | [Section 4.8](#48-planning-time-fork-delivery-the-delivery-obstacle-and-patch-branch-refusal-d-jl15) | Partly: the `delivery` obstacle kind is not built |
| `D-JL16` | A live trunk fixer finishes when a rerun passes its check, and lands any commit it produces; a resolved fixer's runs are settled. Narrowed by `D-JL36`. | 2026-11-20, owner | [Section 4.9](#49-keeping-a-live-trunk-fixers-work-when-a-rerun-passes-d-jl16) | ✅ |
| `D-JL17` | Run IDs are bounded; one item's staging failure holds that item; an adapter's refusal of a brief holds the item and closes no backend. **Rejected:** an attempt limit. | 2026-11-19, owner | [Section 4.10](#410-bounded-run-ids-per-item-staging-holds-and-adapter-preflight-refusals-d-jl17) | ✅ |
| `D-JL18` | Starts are decided at once from a background-maintained view and claimed in one short transaction; a re-rank never cancels a decided start. No fixed latency target. | 2026-11-20, owner | [Section 4.11](#411-instant-start-decisions-from-a-background-maintained-queue-view-d-jl18) | ✅ |
| `D-JL19` | Every design-seat brief states the provenance rule. | 2026-11-20, designer | [Section 4.3](#43-how-a-design-runs-ending-moves-its-conflict-d-jl9) | ✅ |
| `D-JL20` | A verify run that wrote no verdict is recorded as such and resumes at verify; every gate refusal has detail. | 2026-11-20, designer | [Section 4.12](#412-verify-exits-without-a-verdict-gate-refusals-without-detail-d-jl20) | ✅ |
| `D-JL21` | The attempt number counts redispatches backed by a run; refusal tallies are shown per kind; neither routes anything. **Rejected:** a cap or reset. | 2026-11-21, designer | [Section 4.13](#413-what-the-attempt-number-and-refusal-counts-tell-the-operator-d-jl21) | ✅ |
| `D-JL22` | A restart claim without a start is not a running node; it is retracted under `D-JL14`. | 2026-11-20, designer | [Section 4.14](#414-when-a-workflow-restart-is-claimed-but-never-starts-d-jl22) | ✅ |
| `D-JL23` | A reverted landing is a record of the original run, not grounds to refuse a close or sweep rework pins. | 2026-11-20, designer | [Section 3.2](#32-transition-preconditions-and-attempt-holds-d-jl4) | ✅ |
| `D-JL24` | No failover and no preference walk. An infrastructure ending holds the item on its own backend until evidence reopens it. The principle is the owner's; the mechanism is the designer's. Replaces `D-CB3` of [the Claude backend reference](../reference/tillr-claude-backend.md#why-its-this-way). **Rejected:** a walk behind a switch; a re-run without evidence; a failure count. | 2026-11-20, owner | [Section 4.15](#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27) | ✅ |
| `D-JL25` | A backend or judge that cannot run is an alarm: derived, shown first, testable through `tillr view alarms`. The loop keeps running. **Rejected:** a quiet wait for a quota window; a stored alarm. | 2026-11-20, owner | [Section 4.15](#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27) | ✅ |
| `D-JL26` | A quota-refused run waits for its own backend's window. Replaces `D-CB4` of [the Claude backend reference](../reference/tillr-claude-backend.md#why-its-this-way). | 2026-11-20, designer | [Section 4.15](#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27) | ✅ |
| `D-JL27` | A change waiting for a judge under an alarm keeps its lane. | 2026-11-20, designer | [Section 4.15](#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27) | ✅ |
| `D-JL28` | The judge is a step in the flow and follows its rulebook; no grant is issued or expires. The `judge_classes` field and the store migration are the designer's. | 2026-11-20, owner | [Section 6](#6-the-judge-is-a-step-in-the-flow) | ✅ |
| `D-JL29` | An operator retry is recorded as `operator-retry` and, when it waits, names each lane and its holder. The JSON reason stays `no-lane`. | 2026-11-21, designer | [Section 4.16](#416-an-operator-retry-that-waits-for-a-lane-says-so-d-jl29) | ✅ |
| `D-JL30` | A backend wait holds a lane only for work in it. **Rejected:** a timed release; another backend. | 2026-11-21, designer | [Section 4.17](#417-a-backend-wait-holds-a-lane-only-for-work-that-is-in-it-d-jl30) | ✅ |
| `D-JL31` | A fork piece's brief states the one write scope staging enforces. **Rejected:** widening the item's fence; a retry reason; another seat. | 2026-11-21, designer | [Section 4.18](#418-a-fork-pieces-brief-states-the-write-scope-that-staging-enforces-d-jl31) | ✅ |
| `D-JL32` | A worker returned by a design answer is staged on a commit containing the answer; landing-conflict routing is unchanged. **Rejected:** a limit on design answers. | 2026-11-21, designer | [Section 4.19](#419-a-worker-returned-by-a-design-answer-starts-from-a-trunk-that-holds-the-answer-d-jl32) | ✅ |
| `D-JL33` | Leftover work in a lane is an alert, never swept automatically. The principle is the owner's; the mechanism is the designer's. | 2026-11-21, owner | [Section 4.20](#420-leftover-work-in-a-lane-is-an-alert-never-an-automatic-sweeper-d-jl33) | Partly: two wipe gaps are owed |
| `D-JL34` | `no_change` on `planned-build@v1` ends an investigation that finds nothing to change, after `verify` checks it. **Rejected:** a separate workflow; `commit_optional`; closing without `verify`. | 2026-11-22, designer | [Section 4.21](#421-an-investigation-that-finds-nothing-to-change-ends-inside-tillr-d-jl34) | ✅ |
| `D-JL35` | Post-verdict replay is scoped to the named failing Go tests and waits as `gate-replay`. **Rejected:** a re-examination timer; keeping the lane across replay. | 2026-11-23, designer | [Section 4.22](#422-post-verdict-gate-failure-replay-never-reports-run-live-or-runs-whole-tree-go-tests-d-jl35) | ✅ |
| `D-JL36` | A later green proof resolves a fixer whose finished candidate waits to gate; a same-commit rerun keeps `D-JL16`. | 2026-11-23, designer | [Section 4.23](#423-a-later-green-proof-resolves-a-trunk-fixer-whose-worker-has-finished-and-whose-candidate-waits-to-gate-d-jl36) | ✅ |
