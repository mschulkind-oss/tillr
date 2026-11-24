---
title: "Plan first, then spend each model where it is enough"
status: in-review
stage: DESIGN
tags:
  - tillr
  - graphs
  - routing
  - cost
  - models
  - design
summary: "A build item can be planned first: a planning node cuts it into pieces, each piece names a seat, the backend policy puts each seat on a model, and a verify node checks the joined result against the item's criteria before the gate. The planning stage, its seats and the cost report are built. Whether every build item should be planned is open until the cost report can compare planned and unplanned items."
next: "Land ten build items through planned-build@v1 so that the cost report prints the planned median OQ-PT8 waits on beside the ordinary build median."
depends-on:
  - ../reference/tillr-item-graphs.md
  - tillr-judge-and-lifecycle.md
---

# Plan first, then spend each model where it is enough

> **Delivered copy.** This is `docs/design/tillr-planning-node-model-tiering.md` from this repository at commit `282386b687` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-planning-node-model-tiering.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Partly built. The plan seat, the seats in every backend policy, the piece list, the `planned-build@v1` workflow, its verify node, the seat list in the worker's brief, delivery classification and the cost report are built ([Section 5](#5-the-planning-stage-as-built)). A size rule, per-piece command checks and the `delivery` obstacle are not ([Section 5.7](#57-what-nothing-selects-and-what-is-not-built)). The [Decision Ledger](#decision-ledger) holds designer's decisions the owner has not reviewed, each labeled as one.

> **In short.** What an item costs depends more on how its failures are caught than on which model runs it. So a large item is planned first and cut into pieces, each piece goes to the cheapest model that is enough for its kind of work, and one stronger model checks the joined result before the gate.

**Why it matters.** Measured over this repository's own run records, fewer than half of spend went to runs that landed, and the median landed item ran three times ([Section 1](#1-what-the-run-history-says)). The owner's rule is that work is split by kind across seats, with cheaper models wherever they can do the work.

**The shape.** Three nodes inside one item: a planner that proposes pieces, the pieces on the seats they name, and a verify node. Seats are names the backend policy maps to models, so a provider change is one policy edit.

**Cost.** Every planned item pays for a planner and a verify node on the mid model before any saving. That cost is unmeasured.

**Start at** [Section 5](#5-the-planning-stage-as-built), what is built.

**Needs your ruling:** None. Two questions are blocked: [OQ-PT7](#OQ-PT7), until a planned item with a split has landed and storage's [`OQ-TS1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-storage.md#OQ-TS1) is answered, and [OQ-PT8](#OQ-PT8), until ten planned items have landed.

**Reads with:**

- [`tillr-item-graphs.md`](../reference/tillr-item-graphs.md): workflows, nodes, proposals and fences.
- [`tillr-judge-and-lifecycle.md`](tillr-judge-and-lifecycle.md): what a worker may end with, which is how a planner reports a wrong criterion.
- [`tillr-graph-is-the-path.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-graph-is-the-path.md): the rule against counts, and seats declared in the policy.
- [`model-benchmarks-from-drain-records.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/model-benchmarks-from-drain-records.md): replaying recorded tasks, the other way to compare models.

---

## 1. What the run history says

The design rests on one reading of the drain's own records: 334 recorded agent runs over 92 items, each turn priced from the harness's usage record, with every build run as one session on one model. The figures are shares, because prices and models change.

- **Most spend did not land.** 41% of spend went to runs that landed. Of 72 items with a landing, 21 landed on their first run, and the median landed item took three runs.
- **Spend is concentrated in large items.** The most expensive half of items held 87% of spend.
- **Failed runs are expensive, not cheap.** Of the build runs that edited files, 28% ended with their edits uncommitted, and runs that edited and then did not commit or were killed made up 43% of what build runs cost.
- **A build run's cost by phase** *(the phase names are coined here)*: editing 28%, diagnosing after the first edit 26%, running tests and reading their output 22%, orientation 13%, everything else 11%.
- **Orientation**, the reading a run does before its first edit, was 15% to 20% of a build run. It is the cost a split pays again for every node it adds.
- **Context was written to the provider cache more than once.** Build runs wrote about twice as many tokens to the cache as the sum of their largest contexts, which was about a quarter of build-run spend. Idle expiry does not explain it: turns that started five or more minutes after the previous turn carried under 2% of the writes.

### 1.5 What the history cannot say

| Unmeasured | Why | Consequence |
| :--- | :--- | :--- |
| A cheaper model's failure rate and turn count against a stronger one on equivalent work | The history has no side-by-side comparison | Every saving from a cheaper model is an estimate |
| What child sessions spend | A run's recorded cost leaves sub-agent usage out | Spend is understated by an unknown amount |
| The exact test wait | Background-task waits are capped and partly hidden | The test share is a lower bound |
| Why runs write their context to the cache twice | Inferred from totals, not diagnosed | [Section 1.6](#16-what-the-history-means-three-levers-ranked), lever 2 |

### 1.6 What the history means: three levers, ranked

1. **Failed and repeated runs are the biggest lever.** A lever that makes each failure cheaper, or catches it earlier, beats any price ratio.
2. **Context written to the cache more than once is the second lever.** It is independent of planning, except that smaller contexts make each rewrite cheaper.
3. **Routing to cheaper models is the third lever.** It saves the price ratio on whatever moves, provided the cheaper model does not fail more often or take more turns. It is the only lever that needs a planning node: a session cannot change models without losing its cache ([Section 9](#9-alternatives-considered)).

The design follows from this order. A planning node is worth having mainly because it makes failures cheap and early (lever 1) and keeps contexts small (part of lever 2). Giving pieces to cheaper models (lever 3) rides on the same structure.

---

## 2. Principles

- **P1. Each kind of work goes to the cheapest model that is enough for it.** This is the owner's rule, and the planner reads it in its brief from [`plan-guidance.md`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/plan-guidance.md): "Use cheap models for well-specified implementation and mechanical work", "Have a mid model verify the work", "Keep the expensive model for root cause, design and risky core changes."
- **P2. Kinds of work name seats, and models live only in the policy.** An item, a workflow and a plan name seats, never a model, so a provider change is one policy edit.
- **P3. One item outside.** Planning changes only the inside of an item. The queue, the gate and the landing still see one item ([`D-IG2`](../reference/tillr-item-graphs.md#why-its-this-way)).
- **P4. A split pays for its seams.** Each extra node pays a first turn and a partial re-orientation ([Section 1](#1-what-the-run-history-says)). A split is made only where it pays more than that.
- **P5. Failures come first.** Failed and repeated runs are the largest lever ([Section 1.6](#16-what-the-history-means-three-levers-ranked)), so planning is judged first by whether a stop loses one node instead of a whole run.
- **P6. A planner never edits what it is held to.** It names criteria by id. It never rewrites a criterion's text; when a criterion is wrong it says so and the design seat acts ([Section 5.4](#54-when-the-planner-cannot-plan)).
- **P7. Nothing counts attempts.** No node has a retry budget, no item has a number of plans it may use up, and no time limit fails work ([the graph design's rules](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-graph-is-the-path.md)). A node that cannot do its piece names who acts next.

---

## 3. The words

[Item graphs](../reference/tillr-item-graphs.md#the-words) defines workflow, stage, graph, node, node run and child session, and this document uses them unchanged.

| Term | Meaning | What it is not |
| :--- | :--- | :--- |
| **Seat** | The name of a role a node asks for, such as `build` or `verify`. The backend policy in force maps each seat to a backend, and a backend names its harness, model and effort. Any well-formed name the policy maps is a seat | Not a model, and not a fixed list in code |
| **Read-only seat** | A seat the policy lists as one whose nodes may not write. A workflow that gives such a seat a writing node is refused when it loads | Not a promise about the model, only about what the node may do |
| **Piece** | One node's share of a planned item, as the planner proposes it: an id, a seat, whether it writes, a prompt, and the outputs it leaves | Not an item, and it has no place in the queue. Not a trunk commit: the item still lands as one |
| **Kind of work** *(coined here)* | What a node is for, named by what can tell whether it was done right: plan, design ruling, implement, settled edit, run a check, verify | Not a model and not a size. Each kind maps to one seat ([Section 4](#4-kinds-of-work)) |
| **Seam cost** *(coined here)* | What a new node pays before its first useful edit: its first turn and the orientation it repeats | Not the node's work |
| **Cheap, mid and expensive model** | The three levels the owner's guidance names. Which model each is, is a fact of the policy file in force | Not fixed |

---

## 4. Kinds of work

Each kind is defined by what the work needs and what can tell whether it is right. The seat is where the policy places it. The last column is the backend each seat maps to in one shipped policy, [`backend-policy.bedrock.json`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/backend-policy.bedrock.json), whose four backends are `pi-designer` (expensive), `pi` (mid), `pi-build` (cheap) and `pi-scout` (cheapest); other policies under [`config/tillr/`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/) map the same seats to other backends.

| Kind | What it needs | What tells whether it is right | Seat | Backend there |
| :--- | :--- | :--- | :--- | :--- |
| **Plan** | Judgment over the whole item; reads the tree; writes the piece list | The loader's refusals, then the verify node | `plan` | `pi` |
| **Design ruling** | Judgment that no test can check; writes prose that changes what gets built | The owner, or a judge | `design` | `pi-designer` |
| **Conflict repair** | A fix to an item's own definition: correct a criterion that contradicts the tree | The next build run and the documentation checks | `design` | `pi-designer` |
| **Implement** | Code against criteria | The gate; in a planned item, the verify node first | `build` | `pi-build` |
| **Settled edit** | Apply an edit the plan already decided | The same | `edit` | `pi-build` |
| **Write tests** | Tests written from the criteria | The same | `test-write` | `pi-build` |
| **Docs** | READMEs and documents | `vantage-check` and the documentation tests, which check structure and not substance | `docs` | `pi-build` |
| **Run a check** | Run a named check and report its result without fixing it | Its output existing; it may not write | `test-run` | `pi-scout` |
| **Scout** | Read and report; change nothing | The read-only rule and its output existing | `scout` | `pi-scout` |
| **Classify delivery** | Decide whether an item is delivered to the repository, a fork, or both ([Section 5.9](#59-deciding-fork-versus-repository-delivery-at-plan-time)) | The validator; it may not write | `classify` | `pi-scout` |
| **Verify** | Join the pieces and check the result against every criterion | Nothing checks it but the gate that follows | `verify` | `pi` |
| **Review** | An independent verdict on a candidate | Nothing: the reviewer is the check | `review` | `pi` |
| **Command** | No model | The exit code | none: a command node | none |

Four facts about that table:

- **The cheapest backend never writes.** The policy puts it only on the read-only seats (`scout`, `classify`, `test-run`).
- **A stronger model is reached by a diagnosis, never a count.** A build worker that has made and tested an attempt may report that the task exceeds its model; the item then moves to the next backend the policy's `escalate` list names for that seat.
- **A failed check has no seat of its own.** It goes back to the session of the worker that made the change.
- **Work that exists only for landing is never planned.** Conflict repair and the fixers for a broken trunk or a landing conflict run as one node; planning is for build items.

A policy that maps every seat except `design` to one backend has nothing to save by splitting.

---

## 5. The planning stage as built

The drain's [README](../../tillr-drain/README.md) is the reference; this section states the design it implements.

### 5.1 The workflow

[`planned-build@v1`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/workflows/planned-build@v1.json) is a tracked workflow of three stages:

```text
plan    (seat plan, read-only)     may propose replacing `work` with a list of pieces
work    (seat build, writes)       the single node an unsplit item runs
verify  (seat verify, read-only)   checks the joined result against the item's criteria
```

- **An accepted proposal** replaces `work` with the pieces, one node each, through [`build-pieces@v1`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/workflows/build-pieces@v1.json).
- **A planner that proposes nothing** leaves `work` as it is, and the item is built by one worker.
- **A refused proposal** is recorded with its reason and leaves the graph unchanged.
- **Planning can make an item cheaper, and it never stops one for want of a plan.**

### 5.2 What a piece is, and what refuses one

A proposal holds at most 12 pieces. Each has exactly an id, a seat, an access (`read_only` or `write`), a prompt and optional named outputs. A writing piece needs no list of allowed paths.

| The proposal | Recorded as |
| :--- | :--- |
| names a seat the policy in force does not map | `proposal-seat-unmapped` |
| gives a read-only seat a writing piece | `proposal-read-only-seat-writes` |

Writers run one at a time, in the plan's order. Read-only pieces that are ready together run together. A later piece reads an earlier one's named outputs from the piece that produced them. Every writing piece owns real source or documentation edits (`D-PT8`, [Section 5.4](#54-when-the-planner-cannot-plan)).

### 5.3 The verify node

The verify node runs after the pieces and before the gate. It reads the criteria, the joined diff and the pieces' outputs, and for each criterion it calls satisfied it names the piece that satisfied it.

- **A criterion no piece covers** is marked not done, and the item goes back to the plan node with that criterion named. Nothing counts these returns.
- **The same finding twice** raises a conflict for the design seat. The finding is identified by the set of criteria not done, so a planner that keeps missing the same thing cannot loop.
- **The verifier is a model, not a command.** Whether a named piece really satisfies a criterion is its judgment; the gate still runs after it.

[The diff-review design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-diff-review.md) proposes that this node become the one review every code landing gets, run after the gate.

### 5.4 When the planner cannot plan

A planner that cannot cut a piece without a design decision proposes nothing and ends its report with an obstacle naming the owner and its one question. The design seat receives it as a conflict, answers from the documents, or passes the question to the owner in a document.

A worker reports `definition` when the item's words are wrong or contradict the tree. Needing a file the planner did not predict is not an obstacle. That is [what a worker may end with](tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11), and in a workflow of several nodes every blocked report takes the design-seat route. Who may change a frozen criterion is ruled in [criterion repair](criterion-repair.md#decision-ledger).

> [!NOTE]
> The planner's prompt names only the owner obstacle. Naming the `definition` kind there too is a prompt edit with no decision in it.

**How a plan cuts writing pieces (`D-PT8`).** A piece is assigned a result, not a path list. A piece narrowed to one directory still reads the item's whole criteria in its brief, so a path fence per piece makes a worker raise an obstacle for a file its own criterion requires. All writers follow the same [deny-list](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md#51-the-work-and-its-statement), and verification checks each piece's result. The rules a plan follows:

- **Coupled work stays with one writer.** When an item's criteria share one repair and one verification run across a module, its test runner and `tests/`, `plan` may leave `work` unsplit (`write_scope: "inherit"`); `plan`'s report is carried into `work`'s brief.
- **Every writing piece owns real source or documentation edits.** A piece that changes `tools/tillr*` runs `just pack-sync` and includes the regenerated copies under `yolo-packs/tillr/` in its own commit, as [`brief-template.md`](../../tillr-run/backends/brief-template.md) requires. A piece prompt that forbids editing the mirror forbids hand edits, not regeneration.
- **No standalone `pack-sync` piece, and no verification-only writing piece.** Such a piece starts with nothing left to commit and falsely raises an obstacle. This overrides the line "Send `pack-sync` to an `edit` piece" in [`plan-guidance.md`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/plan-guidance.md). Verification-only steps go to `read_only` seats.
- **The last writer can repair the joined candidate.** A `verify-not-done` return resumes the last writer, so a split of code and documents does not end on a documents-only writer narrowed away from the implementation.

### 5.5 A worker that still works alone

The worker's brief lists every seat the policy in force maps, one line each with its purpose, and marks the read-only ones. A fixed paragraph tells the worker to give mechanical searching, editing and checking to the cheaper sub-agents its own harness offers. Those sub-agents' models are chosen by the harness's configuration, not by tillr's policy, and their spend is inside the run's.

### 5.6 The cost report

`tillr drain why`, with no item named, prints:

- the recorded cost of each landed item, and the sum for each seat;
- the median cost of the last twenty landed build items;
- once ten items have landed through `planned-build@v1`, the same median for those items.

A run with no recorded cost makes every sum it belongs to print `unknown`, never zero.

### 5.7 What nothing selects, and what is not built

| Thing | State | Evidence |
| :--- | :--- | :--- |
| Which items run through `planned-build@v1` | `tillr item request` defaults a `build` filing to it. A document-declared item runs through it only when its block names the workflow | [`request.go`](../../tillr-item/item/request.go) |
| A default workflow for every build item | Not set | `rg workflows .tillr.toml` finds nothing |
| A rule that plans only large items | Not built ([Section 6.2](#62-a-size-rule-tested-on-the-history)) | — |
| A command that checks each piece the moment it finishes | Not built. The registry of commands a node may run holds one entry, the pack sync | [`commands.json`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/commands.json) |
| A size, a file count or an interface sketch on a piece | Not built; a piece has the fields of [Section 5.2](#52-what-a-piece-is-and-what-refuses-one) and no others | [`build-pieces@v1`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/workflows/build-pieces@v1.json) |
| The `delivery` obstacle of [Section 5.9](#59-deciding-fork-versus-repository-delivery-at-plan-time) | Not built | `rg -n 'delivery' tools/tillr-drain/drain/obstacle.go` finds nothing |

### 5.8 One writer for each piece of state

| State | Its one writer |
| :--- | :--- |
| The item, its criteria and place in the queue | The item utility, from the documents or from a filing through its command line. Planning writes none of it |
| The proposal | The planning node's run, as an output the drain checks and records |
| The graph and its revisions | The drain, in the one transaction that applies or refuses a proposal ([`D-IG3`](../reference/tillr-item-graphs.md#proposals-and-graph-revisions)) |
| A piece's commits | That piece's node run, in the item's worktree, one writer at a time |
| A node's seat, model, outcome and usage | The drain, when the node starts and settles |

### 5.9 Deciding fork versus repository delivery at plan time

Owner ruling (`D-PT9`): *"There should not be a way to mis-file an item such that it leads to bad downstream work. That should be decided later in the process."* The case behind it is an item whose change belongs in a fork the repository tracks rather than in repository files. A filer should not have to know a lane's fork checkout path to file fork work, and fork work filed with repository paths must not be staged as repository work.

The delivery contract that applies the ruling (the implementation choices are the designer's):

1. **File new work from its words.** The request face ignores `--path` with a notice. Fork delivery is never inferred from a path list.
2. **Classification decides the delivery target and piece order.** Before any writer is dispatched, a read-only step on the `classify` seat decides from the item's words and the tree whether the change is delivered through repository files, one named fork from the repository's tracked fork list, or both as two ordered pieces: the fork piece first, then the repository files that depend on it. The validator accepts `repo` with no fork, and `fork` or `both` with exactly one known fork. The drain stages each piece accordingly: a fork piece gets an isolated fork checkout starting from the fork head, non-delivery worker instructions, the fork's gate, and lander-owned advancement of the fork's delivery branch before any dependent repository piece dispatches or lands.
3. **A mid-run `delivery` obstacle restages without an operator.** A worker that discovers mid-run that its change belongs to a fork it was not staged for ends with `OBSTACLE: delivery` naming the fork ([`D-JL15`](tillr-judge-and-lifecycle.md#48-planning-time-fork-delivery-the-delivery-obstacle-and-patch-branch-refusal-d-jl15)); the drain restages the same item as a fork piece and continues it. This is not built.
4. **Workers can never move a delivery branch.** The drain checks every candidate before the gate and refuses any run that moved a fork's delivery branch, excluding lander-marked deliveries.

Classification before writer dispatch, a target that does not come from the fence, and continuation of the repository piece after a restart are covered by `TestDeliveryClassificationPrecedesWriterDispatch`, `TestDeliveryTargetDoesNotComeFromFence` and `TestDeliveryMixedContinuesAfterLoopRestart` in `tools/tillr-drain/drain/delivery_routing_test.go`.

---

## 6. What planning costs, and what would choose it

### 6.1 The estimate

Planning's cost is modeled, not measured. The model starts from one successful single-worker run and splits it into three pieces using the phase shares of [Section 1](#1-what-the-run-history-says), adding a plan, one seam per piece, and a full rerun on the stronger model for any cheap piece that fails. It says three things:

- **Against one cheap-model run that succeeds, planning costs more on every small and medium item.** The planner and the seams exceed the saving until the item is several times the cost of a plan.
- **Planning's case is failure, not price.** The model ignores retries. The items a size rule would pick landed on their first run 16% of the time ([Section 6.2](#62-a-size-rule-tested-on-the-history)), and under a split a stop loses one piece instead of a run.
- **The model leaves out the verify node.**

So the estimate cannot settle whether planning pays. Ten real items can ([Section 7](#7-how-the-owner-learns-whether-it-pays)).

### 6.2 A size rule, tested on the history

No rule decides which items are planned. One was drafted and tested against 85 build items in the run history: plan a build item when it has many criteria or its paths span three or more module directories.

| Rule at first dispatch | Items selected | Share of spend | Landed on the first run: selected, others |
| :--- | ---: | ---: | :--- |
| 5+ criteria, or 3+ modules | 56 | 81% | 9 of 56 (16%), 12 of 29 (41%) |
| 6+ criteria, or 3+ modules | 40 | 57% | 5 of 40, 16 of 45 |
| 4+ criteria, or 3+ modules | 75 | 97% | 15 of 75, 6 of 10 |

The rule is a weak predictor of cost: it separates mean spend per item by about a factor of two. It is a better predictor of trouble. Criteria count alone barely discriminates, because most items have four or more. Whether such a rule is built at all is [OQ-PT8](#OQ-PT8).

### 6.3 How many pieces

A proposal is capped at 12 pieces. Each added node pays a seam, so many small pieces spend their saving on seams, and every handoff loses context that a piece's outputs only partly restore. The smallest useful split is one package of code plus its documents. This is advice for whoever edits the planner's guidance, not a rule the loader enforces.

---

## 7. How the owner learns whether it pays

1. **Keep the build baseline priced.** The median of the last twenty landed build items prints a figure only when each of their runs recorded a cost.
2. **Land ten build items through `planned-build@v1`.** The 16%-against-41% split of [Section 6.2](#62-a-size-rule-tested-on-the-history) says where to look: items with five or more criteria, or paths in three or more modules.
3. **Read the two medians** that `tillr drain why` then prints, and the per-seat sums beside them.
4. **Ask [OQ-PT8](#OQ-PT8).**

A second way to compare models is designed: replaying recorded tasks for several models side by side, in [the model benchmarks design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/model-benchmarks-from-drain-records.md). The cost report compares what landed items cost in production; a replay compares models on the identical task. Neither replaces the other.

The cost medians are the one comparison computed inside tillr. Every other comparison between backends is computed outside it, from the run records (`D-PT5`).

---

## 8. Worked examples

Four shapes of item recur in the run history, and each says something about when to plan:

- **A blocker outside the fence.** An item whose build runs each found that it needed a capability its fence did not include cost several design answers. A planner cannot see such a blocker before trying; it would have found it for about what the build runs did.
- **A small change in one package.** One clean run, mostly orientation and tests. A plan costs more than the whole item, so a small item should never be planned.
- **A large item over several modules.** Most of its cost was runs that stopped or were killed after hundreds of turns with a context near the model's window, and ended without a commit. This is the case planning is for: under a split, each stop costs one piece.
- **A single long design run.** Most of its cost was context written to the cache many times over. Neither routing nor planning touches that.

---

## 9. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **A cheap model only behind a command that checks it the moment it finishes**, with each piece carrying its own registered check | **Not adopted.** The owner's rule puts cheap models on well-specified work and has a mid model verify. A per-piece command check could be added on top of the verify node; it is not proposed until planned items show what the verifier misses |
| **A ladder of models for each seat**, climbed one rung per failed check, with a fixed number of rungs per piece and re-plans per item | **Rejected.** Each of those is a count, and the owner ruled counts out. A stronger model is reached when a worker's tested attempt shows the task exceeds its model |
| **One session that switches model mid-run** | **Rejected.** A new model has none of the old one's cache, so a switch writes the whole context again. A node is one invocation on one route ([`D-IG12`](../reference/tillr-item-graphs.md#why-its-this-way)) |
| **Many tiny nodes** | **Rejected as a goal; the cap is 12 pieces.** Each node pays a seam, and every handoff loses context |
| **Cheaper sub-agents inside one worker** | **Adopted beside planning, not in place of it.** The worker's brief encourages it ([Section 5.5](#55-a-worker-that-still-works-alone)). tillr cannot fence, check or count a sub-agent apart, which is what a piece is for |
| **Planning on the expensive model** | **Rejected.** The expensive model's orientation alone costs more than most build items cost in total |
| **Committing the plan as a document** | **Open, and blocked:** [OQ-PT7](#OQ-PT7). The plan is kept with its run and shown on the item's page |

---

## 10. What this design does not do

- It does not split one item into several items, and does not change the queue, the gate or the landing (P3).
- It does not let any node edit criteria (P6).
- It does not count attempts, plans or fixes, and it sets no spending limit (P7).
- It does not turn planning on for every build item. That is [OQ-PT8](#OQ-PT8).
- It does not route design rulings below the design seat.
- It does not run writing pieces in parallel ([`OQ-IG3`](../reference/tillr-item-graphs.md#why-its-this-way)).
- It does not touch the sub-agent seat profiles of [the seat-profile design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-seat-profiles.md). They govern child sessions, and this design governs nodes.

---

## 11. Risks

| Risk | What limits it |
| :--- | :--- |
| A cheap model takes many more turns than the mid model, so its real price ratio is near 1 | No side-by-side comparison exists ([Section 1.5](#15-what-the-history-cannot-say)); the cost report compares medians once ten planned items have landed |
| A cheap piece is wrong in a way the verifier misses | The gate still runs, and automatic revert is on for a landing that turns the trunk red. A command check per piece is the unbuilt remedy ([Section 9](#9-alternatives-considered)) |
| A cheap model's change breaks the loop's own code | The same: the module's suite, the gate and automatic revert, as for any model ([`OQ-PT5`](#OQ-PT5) in the Decision Ledger) |
| The planner splits along the wrong seams, so pieces fight | The verify node returns the item to the planner with the uncovered criteria named; the same finding twice goes to the design seat |
| Planning costs more than it saves on most items | It is not the default for every build item until ten planned items have been measured ([OQ-PT8](#OQ-PT8)) |
| The before-and-after comparison is made against `unknown` | Step 1 of [Section 7](#7-how-the-owner-learns-whether-it-pays) |

---

## 12. Rulings this builds on

| Ruling | What it means here |
| :--- | :--- |
| Cheaper models do build work, with a mid model verifying ([`OQ-PT1`](#OQ-PT1) in the Decision Ledger) | The build, edit, test-write and docs seats sit on a cheap backend; plan, verify and review on the mid one |
| The cheapest model is not a worker | It fills only read-only seats |
| [`D-IG3`](../reference/tillr-item-graphs.md#why-its-this-way), [`D-IG4`](../reference/tillr-item-graphs.md#why-its-this-way), [`D-IG12`](../reference/tillr-item-graphs.md#why-its-this-way) and [`OQ-IG3`](../reference/tillr-item-graphs.md#why-its-this-way) | Planning is a proposal; nothing loops; writers run one at a time |
| No attempt counts, retry budgets or exhausted states; seats are declared in the policy, with no rungs ([the graph design's ledger](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-graph-is-the-path.md#decision-ledger)) | No ladders and no caps on plans |
| A worker ends with its commit or a named obstacle ([`D-JL11`](tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11)) | A wrong criterion is a `definition` obstacle; there is no `fence` obstacle kind |
| Comparisons between backends are computed outside tillr | Except the cost medians of [Section 5.6](#56-the-cost-report) |
| The observatory shows usage, never a price | The cost report is the drain's own text output, not the observatory's |

---

## Decision Ledger

Each row says which kind it is. An **owner ruling** records words the owner said. A **designer's decision** was made by a design seat from the evidence named; the owner has not reviewed it and may overturn it.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-PT1 | **Owner ruling.** Cheaper models do build work: a cheap model implements, the cheapest scouts, and the mid model is used "where appropriate", with work split "into different seats as we see fit" | 2026-11-18 | [Section 4](#4-kinds-of-work) | ✅ the shipped policies |
| OQ-PT2 | **Closed without a ruling.** A trial of the cheap model on a few named items before it builds anything else is moot once the owner put it on the build seat | 2026-11-18 | [Section 7](#7-how-the-owner-learns-whether-it-pays) | — nothing to build |
| OQ-PT3 | **Owner ruling, made elsewhere.** A planner that finds a criterion it cannot meet reports it and the design seat acts; it never rewrites the criterion. This is the `definition` obstacle of [`D-JL11`](tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11); who may change a criterion is [`OQ-CR2`](criterion-repair.md#decision-ledger) | 2026-11-18 | [Section 5.4](#54-when-the-planner-cannot-plan) | ✅ the route; the planner's prompt does not name the kind |
| OQ-PT4 | **Designer's decision.** A planner need not predict paths; a needed path absent from a plan neither invalidates the proposal nor calls for a design answer | 2026-11-19 | [Section 5.4](#54-when-the-planner-cannot-plan) | ✅ a writing piece carries no path list |
| OQ-PT5 | **Designer's decision.** A cheaper model may change the loop's own code, with no special rule for it: the owner's instruction made no exception, and the gate and automatic revert apply to every model. **Rejected:** starting loop-code work on the stronger model, which would keep most of the queue off the cheaper one | 2026-11-18 | [Section 11](#11-risks) | ✅ nothing to build |
| OQ-PT6 | **Owner ruling.** No item has a number of plans it may use: "No attempt counts, retry budgets or 'exhausted' states." A failed verify returns the item to the planner uncounted, and the same finding twice goes to the design seat | 2026-11-17 | [Section 5.3](#53-the-verify-node) | ✅ |
| D-PT1 | **Designer's decision.** Each kind of work is a seat, and the seat list is open: any name the policy maps | 2026-11-16, amended 2026-11-18 | [Section 4](#4-kinds-of-work) | ✅ |
| D-PT2 | **Designer's decision.** A planner's list of pieces is a bounded workflow argument: `pieces`, at most 12 entries | 2026-11-16, amended 2026-11-18 | [Section 5.2](#52-what-a-piece-is-and-what-refuses-one) | ✅ |
| D-PT3 | **Withdrawn.** Ladders and rungs per piece are counts, which the owner ruled out. The policy's `escalate` list moves an item to a stronger backend on a worker's diagnosis | 2026-11-18 | [Section 9](#9-alternatives-considered) | — |
| D-PT4 | **Designer's decision.** The planner is the first node of the workflow, and a planner that proposes nothing leaves the item to one worker | 2026-11-16, amended 2026-11-18 | [Section 5.1](#51-the-workflow) | ✅ `planned-build@v1` |
| D-PT5 | **Designer's decision.** tillr records each node's seat, model and cost, and comparisons between backends stay outside it, with one exception: `tillr drain why` computes cost medians | 2026-11-16, amended 2026-11-18 | [Section 5.6](#56-the-cost-report) | ✅ |
| D-PT6 | **Withdrawn.** "One re-plan, as the next attempt" is a count. A re-plan is the verify node's return to the planner | 2026-11-18 | [Section 5.3](#53-the-verify-node) | — |
| D-PT7 | **Superseded.** A refused run goes back to its worker's own session with no token bound ([how a worker run's ending moves its item](tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)) | 2026-11-18 | [Section 4](#4-kinds-of-work) | ✅ elsewhere |
| D-PT8 | **Designer's decision.** A piece is assigned a result, not a path list. Coupled cross-module work may stay unsplit; every writing piece owns real edits and runs `just pack-sync` in its own commit when it changes `tools/tillr*`; the last writer can repair the joined candidate. **Rejected:** a path fence per piece, and a standalone `pack-sync`, verification-only or trailing documents-only writing piece | 2026-11-20 | [Section 5.4](#54-when-the-planner-cannot-plan) | Guidance; [`plan-guidance.md`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/plan-guidance.md) still carries the overridden line |
| D-PT9 | **Owner ruling**, with the designer's implementation choices. Whether an item is fork work, repository work or both is decided at classification, never at filing: filing takes words; a read-only step decides the target and piece order; a mid-run `delivery` obstacle restages the item; no worker moves a fork's delivery branch. **Rejected:** requiring a filer to write a lane's fork checkout into a fence, and blocking for the operator on a `delivery` obstacle | 2026-11-19 | [Section 5.9](#59-deciding-fork-versus-repository-delivery-at-plan-time) | Partly: classification and staging are built; the `delivery` obstacle is not |

---

## Open Questions

1. 🔒 **OQ-PT7: Do you want an item's plan committed as a document, as well as shown on the item's page?**

   You open a planned item's page to see how the planner split it. The page shows the pieces, any refusal and each revision, read from the run's record. Nothing about the plan is committed. A committed copy could be read in Vantage and would outlive the store; it would also add a file to every planned item, including plans that were abandoned.

   - **A — The item's page only**, as built. Nothing extra lands with the item.
   - **B — Committed too.** A short plan document rides in the item's one commit.

   <!-- vantage: question id=OQ-PT7 blocked-on="first-planned-item-with-pieces-landed, ./tillr-storage.md#OQ-TS1" leaning="The item's page only (A): a plan records how one attempt went, and the documents hold what must be built." -->

   _Leaning:_ The item's page only (A): a plan records how one attempt went, and the documents hold what must be built.

   Blocked until a planned item that split into pieces has landed, so there is a landed plan to look at, and until [the storage design's first question](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-storage.md#OQ-TS1) says where an item's record lives.

   **Answer:**
   > _(empty — fill in when decided)_

2. 🔒 **OQ-PT8: Should every build item be planned first, or only the ones that ask for it?**

   An author declares a build item in a document, with five criteria across two Go modules. The loop gives it to one worker, because no default workflow is set. A second way is built: a planner hands pieces to cheaper workers, and a verifier checks the result. Until ten planned items have landed, the planned-workflow median cost is unknown.

   - **A — Only when the author asks**, by naming the workflow on the item; a build request filed from the command line already defaults to it. Nothing to build. Large document-declared items nobody marked keep losing whole runs.
   - **B — Every build item.** One settings line. Small items pay for a planner and a verifier that cost more than they do.
   - **C — Only large items.** Needs a rule built. The drafted one picked 56 of 85 past items, which held 81% of spend.

   <!-- vantage: question id=OQ-PT8 blocked-on="ten-planned-items-landed" leaning="Only when the author asks (A) until the figures exist; then C if planning is cheaper only on large items, B if it is cheaper overall." -->

   _Leaning:_ Only when the author asks (A) until the figures exist; then C if planning is cheaper only on large items, B if it is cheaper overall.

   Blocked until ten items have landed through the planned workflow. `tillr drain why` then prints their median cost beside the ordinary build median ([Section 7](#7-how-the-owner-learns-whether-it-pays)).

   **Answer:**
   > _(empty — fill in when decided)_
