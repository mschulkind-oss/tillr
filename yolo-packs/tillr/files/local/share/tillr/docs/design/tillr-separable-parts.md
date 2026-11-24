---
title: "What tillr will change for Product QA, what it promises, and what it will not change"
status: in-review
stage: DESIGN
updated: 2026-11-24
tags:
  - tillr
  - design
  - store
  - boundaries
  - product-qa
summary: "What tillr promises a program other than its own loop: the stable surface of commands and records, what is internal, how a breaking change is announced, and the fate of each request the Product QA pipeline made of tillr."
next: "The owner reviews the designer's decisions D-FQ1 to D-FQ5. P2, P3, P4 and P6 are not built. OQ-FQ7 and OQ-FQ8 stay blocked until a consumer needs them."
depends-on:
  - tillr-core-and-git-landing.md
  - tillr-product-qa-requests.md
  - tillr-definitions-plan.md
  - ../reference/tillr-item-graphs.md
  - tillr-judge-and-lifecycle.md
  - tillr-idea-pipeline.md
---

# What tillr will change for Product QA, and what it will not

> **Delivered copy.** This is `docs/design/tillr-separable-parts.md` from this repository at commit `0275c51d52` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-separable-parts.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** 2026-11-24. The store, items, verdicts and leased runs work outside Git, and a store
that never lands runs end to end (the worked example under
[`tools/tillr/examples/recorded-verdict/`](../../tillr/examples/recorded-verdict/)). Four
small pieces of work ([Section 3](#3-proposed-work)) and the build plan's next tranche are not
built. Five decisions here are the designer's and await the owner's review
([the ledger](#decision-ledger)). The commands behind the behavior claims are in
[Evidence](#evidence).

> **In short.** tillr is a general core plus one definition per use, and Git landing is the first
> definition. A second project uses the core through commands and records, never through Go
> packages or SQL, and tillr announces every change to that surface.

**Why it matters.** A consumer builds against what this document promises. A promise that is stale
costs it a wrong build; [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now) is the
part it relies on.

**Cost.** tillr keeps a stable surface of commands and records for a second project, and announces
every change to it.

**Start at [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now)**, the contract, then
[Section 7](#7-the-second-round-tillrs-answer) for the fate of each request.

**Needs your ruling:** None. [OQ-FQ7](#OQ-FQ7) and [OQ-FQ8](#OQ-FQ8) are blocked, each on something
named in it.

**Awaits your review:** the designer's decisions `D-FQ1` to `D-FQ5` in
[the ledger](#decision-ledger).

**Reads with:** [tillr-core-and-git-landing.md](tillr-core-and-git-landing.md) (the direction
this sits under), [tillr-definitions-plan.md](tillr-definitions-plan.md) (the build plan and its
next tranche), [tillr-product-qa-requests.md](tillr-product-qa-requests.md) (the consumer's
requests, as received), [tillr-item-graphs.md](../reference/tillr-item-graphs.md) (graphs inside
one item), [tillr-judge-and-lifecycle.md](tillr-judge-and-lifecycle.md) (item states),
[tillr-idea-pipeline.md](tillr-idea-pipeline.md) (item kinds and child items),
[board-utilities.md](board-utilities.md) (the process-and-record seam),
[the vocabulary](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md) (which keeps this contract's lane names as aliases).

<a id="for-the-autoqa-projects-agent"></a>

## For the Product QA project's agent

Read, in this order:

1. [The tillr design index](../tillr/design-status.md), which the tillr pack ships.
2. [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now): what you may rely on.
3. [Section 7](#7-the-second-round-tillrs-answer): each request and its state.
4. The worked example under `examples/recorded-verdict/` in the core library: a store that lands
   nothing, run from an empty directory.

Where the real thing differs from what this document says, that is a defect in tillr or in this
document. [Section 2.4](#24-how-requests-and-answers-travel) says how a reply travels.

A **definition** is the layer that turns tillr's core into one use, and the **landing definition**
is the one for Git landing. [The vocabulary](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md) names the layer a **charter**
and the first use **`git-landing`**; the code and the flag `--definition` keep the older word.

---

## The words this document uses

- **Product QA:** a bug-finding pipeline that is a separate project and runs tillr stores of its
  own. It is the consumer whose requests this document answers.
- **AutoQA:** not Product QA. [tillr-autoqa.md](tillr-autoqa.md) is tillr's own design for a
  standalone exploratory-testing tool, aimed first at tillr itself.
- **Consumer:** a program other than tillr's own loop that uses tillr's commands on a store.
- **Store:** tillr's SQLite database of items, runs and events.
- **Face:** one subcommand of a tillr program, such as `tillr item apply`. **Record:** the JSON
  document a face reads or writes, in the envelope [board-utilities.md](board-utilities.md)
  defines.
- **Drain:** the loop that plans, runs, gates and lands items. **Keeper:** the program that restarts
  the drain when it exits ([tillr-keep](../../tillr-roll/tillr-keep)).
- **Lane:** a leased slot with its own directory, where one run executes. **Lease:** the claim on a
  lane.
- **Gate:** the check run on a candidate commit. **Lander:** the program that puts a gated commit
  on the trunk.
- **Workflow, graph and node:** as defined in [tillr-item-graphs.md](../reference/tillr-item-graphs.md).
- **Criterion:** one testable condition on an item. **Verdict:** a ruling on one criterion.
- **Hold:** the planner's named reason for not dispatching an item.
- **`no_commit` exit:** a workflow's declaration that its item finishes without a commit, so no
  gate or landing runs.
- **Unshared item:** an item in a store that no tracked document declares, so it cannot be rebuilt
  by deleting the store and syncing.

---

## 1. What tillr will change

### 1.1 Now, or queued at once

**A1. Every read face and the planner answer on a store the drain never opened.** Built: after
`tillr store init` alone, `tillr view waiting`, `tillr drain why` and `tillr run lanes` exit 0.

- **Promised:** every read face exits 0 on a store holding only `init` and `apply`.
- **Not promised:** that a write face creates another utility's tables.

**A2. A raw `tillr item state` cannot move a finished item.** Built for the move out: a raw write
that would change a finished item is rejected, through `state` and through `apply`. A raw write
*to* `landed` with an open criterion, or to `resolved-elsewhere` with no pointer, is the
terminal-state block's to close in [the lifecycle design](tillr-judge-and-lifecycle.md).

- **Promised:** a finished item stays finished against a raw writer.
- **Not promised:** a check of *who* may write a state; actors are recorded, not authorized.

**A3. The explicit-store route is written down.** Not built (P2). Every face takes `--store`, and
a plain command runs as a leased run with an end cause on a store the drain never opened. A store
has one dispatcher: the drain holds an item whose run it did not dispatch as `run-link-missing`.

- **Promised:** a recipe whose every command is checked to answer `--help` by
  [tests/tillr-docs.test.py](https://github.com/mschulkind-oss/tillr/blob/main/tests/tillr-docs.test.py).
- **Not promised:** two dispatchers on one store, ever.

**A4. A stock adapter for plain commands.** Not built (P3). `tillr run exec` needs an adapter
directory holding `pause-kind` and `extract-report` beside the command, and refuses a working
directory that is not relative to the lane, so each caller writes the same stub.

- **Promised:** one tracked adapter that `--backends-dir` can name, which says `cannot-resume`.
- **Not promised:** resuming a plain command.

**A5. The contract is published, and each change to it is recorded.** Not built (P4). The contract
is [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now) of this document; there is no
changelog (`fd -i changelog tools docs` finds nothing).

- **Promised:** the contract in a tracked reference, a test that every face and record kind it
  names exists, and a dated line in it for every change to the stable surface.
- **Not promised:** that the surface stops changing; only that a change is announced.

### 1.2 On the existing design path

- **B1. Command nodes.** Built. **Not promised:** skipping a node whose inputs did not change.
- **B2. Node outputs.** Built: name, type, sha256 and location per output. **Not promised:** that
  the drain's table is readable by SQL.
- **B3. Finishing with no commit.** Built: the `no_commit` exit, and for a definition that does not
  land, completion on a hook's recorded verdict (`completes: "hook"`). **Not promised:** child items
  from such an item ([OQ-FQ7](#OQ-FQ7)).
- **B4. The evaluator slot.** Built. A judge is a different actor from the worker it judges.
- **B5. Leases without the drain.** Built: `tillr run exec` claims a lane, leases it, writes the
  end cause and releases it, and `tillr run release` frees a lane by hand. There is no heartbeat,
  on purpose: `run` is one process with nothing resident to renew it. **Not promised:** telling a
  dead run from a live one without the drain ([P6](#3-proposed-work)).
- **B6. Events.** A store records `item.add`, `item.update`, `run.allocate`, `run.usage` and
  `run.finish`. **Not promised:** a "purpose" field on run events beyond the node a run belongs to.
- **B7. Dispatch policy.** Built: seat shares, lanes outside the cap and a spend ceiling, beside
  the lane count, the backend policy, and pluggable gate, land and verdict programs. A second
  dispatcher is A3's separate store. **Not promised:** deadlines
  ([C5](#13-what-tillr-will-not-change)).
- **B8. Items fed by `item apply` keep working.** Built: an undeclared item goes to triage instead
  of a hold, and a store where no document block declares any item is left alone.
- **B9. An owner acknowledgment point.** Not built, and no design carries it.
  `tillr view events --since` takes a cursor; a stored "the owner has seen up to here" does not
  exist. A consumer keeps its own cursor.

### 1.3 What tillr will not change

Each row says what holds inside the landing definition. By the owner's decision rule, a choice
that suits Git landing and chafes against another use belongs to each definition and not to the
core, so several of these open up in a consumer's own definition. The reasons are in
[Section 6](#6-why-the-boundary-is-where-it-is).

| | Request | Holds inside the landing definition | What a consumer does | Standing |
| :--- | :--- | :--- | :--- | :--- |
| C1 | New item states per kind | The closed seven-state model. Lifecycle belongs to the definition, so another definition names its own | Names its own finished states in its definition file, as the worked example does | Open: counting outcomes in the views is not built |
| C2 | Reopening a landed or resolved-elsewhere item | Both are terminal | Lists the move in its own definition's valid moves. Reopening a `closed` item works in the landing definition | The definition's policy |
| C3 | A running step that rewrites the graph freely | Bounded replacement only, no model-written wiring (`D-IG2`, `D-IG3`, `D-IG5`, `D-IG9`) | Keeps its graph in its dispatcher, or one tillr item per finding | Narrowed to insertion downstream and appending; not built |
| C4 | An uncertain outside effect as a state inside a graph | No node changes anything outside its worktree | Keeps its send step and its uncertain state. Outside a graph, `blocked` with a waiter works | Open; not built |
| C5 | Deadlines and clocks in the plan | No timers | Orders and times work in its dispatcher | Withdrawn |
| C6 | A Go library as the connection | Processes and records | Calls the commands in [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now) | Withdrawn |
| C7 | Completion by "an artifact recorded and judged" | A landing completes an item; another definition completes on its hook's recorded verdict | Declares `completes: "hook"` in its definition file | Built |
| C8 | A second loop and keeper beside the first, in one root | The keeper finds a loop by its working directory and locks per root (`find_loop` in [tillr-keep](../../tillr-roll/tillr-keep)) | Runs a loop with its own store and root | Withdrawn |

---

<a id="2-the-contract-between-tillr-and-autoqa-for-now"></a>

## 2. The contract between tillr and Product QA, for now

### 2.1 The stable surface

A consumer may rely on these. A change to any of them is announced as
[Section 2.3](#23-how-a-consumer-learns-of-a-breaking-change) says.

- **Commands:** each program as `tillr-<noun> <face>`, or through the umbrella as
  `tillr <noun> <face>`.
- **Faces a consumer may call:**
  - `tillr-store init`, `check`, `events`;
  - `tillr-item apply`, `list`, `show`, `state`, `close`, `reopen`, `verdict`, `grant`, `decision`,
    `mail`;
  - `tillr-run exec`, `status`, `lanes`, `release`;
  - `tillr-view status`, `events`, `waiting`, `passes`, `lanes`, `running`;
  - `tillr-drain why` and `plan`, which only read; and `tillr-drain once` and `loop` on the
    consumer's own store and root.
- **Flags:** `--store`, `--input`, `--now`, `--root`, `--item`, `--lane-root`, `--backends-dir`,
  `--since`, and `--definition` on `tillr-store init`. No face defines `--format`. Store access is
  always explicit: no standalone face opens the live store on its own.
- **Exit codes** ([exit.go](../../tillr/record/exit.go)): 0 done, 1 a negative decision, 2
  usage, 3 store or record version incompatible, 4 an outside dependency prevented a decision,
  5 an internal error.
- **Record envelope:** `record_version`, `kind`, `id`, `producer`, `created_at`, `subject`,
  `payload` and an optional `idempotency_key`. The version is 1 (`SupportedVersion` in
  [envelope.go](../../tillr/record/envelope.go)), and a reader refuses a version it does not
  know.
- **Record kinds a consumer writes:** `item.record` (to `apply`) and `run.request` (to `exec`).
  `apply` refuses a record whose `producer.system` is not `tillr` ("record from another system"),
  so a consumer stamps that value.
- **What `tillr run exec` sets in a run's environment**
  ([exec.go](../../tillr-run/run/exec.go)): `TILLR_RUN_ID`, `TILLR_ITEM_ID`, `TILLR_NODE_ID`,
  `TILLR_LANE_ID`, `TILLR_SESSION_MODE`, `TILLR_RESUME_PROMPT_FILE`, `TILLR_PAUSE_REQUEST`,
  `TILLR_PROGRESS_FILE`, `TILLR_READ_ONLY_GROUP`, `TILLR_CHILD_SESSIONS_ROOT` and
  `TILLR_CHILD_SESSIONS_MARKER`. It also replaces any inherited scratch-directory variable with the
  run's own scratch directory, `<lane dir>/durable/<run id>` (`DurableDir`).
- **Names that carry the word "lane"** (`--lane-root`, `TILLR_LANE_ID`, `tillr-run lanes`,
  `tillr-view lanes`) stay as they are; [the vocabulary](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md) keeps them as
  aliases.
- **Kinds a consumer reads:** `run.record`, the item and verdict records `show` prints, and the
  event kinds in the log.
- **The face is `tillr item apply`, not `item add`:** it applies an `item.record` read from
  `--input` as either an add or an update, and writes the event `item.add` or `item.update`
  accordingly (`Apply` in [apply.go](../../tillr-item/item/apply.go), `AddKind` and
  `UpdateKind` in [schema.go](../../tillr-item/item/schema.go)). The name stays
  ([`D-FQ4`](#decision-ledger)).

### 2.2 What is internal, and may change daily

- **Go packages.** Every package can be imported, because no module has an `internal/` directory
  and siblings resolve by relative `replace`, but none is supported.
- **The store's schema:** table names, columns and migrations. Read through a face, never SQL.
- **Hold reason names, view text, and file layout** under the drain's root.
- **Anything a module README's "Known Gaps" callout names.**

### 2.3 How a consumer learns of a breaking change

There is no mechanism yet. Proposed, as [A5](#11-now-or-queued-at-once):

- A change to a record's fields gets a new `record_version`, and readers of the old version are kept
  for one round.
- Every change to the stable surface adds a dated line to the published contract in the same
  commit, so `git log` on that one file is the change feed.
- The module READMEs' Known Gaps callouts keep saying what is unfinished.

### 2.4 How requests and answers travel

- **A consumer's requests** are written by its agent with the owner, one numbered request per
  entry, each with the behavior it checked. The owner relays them. The latest round is recorded as
  received in [tillr-product-qa-requests.md](tillr-product-qa-requests.md), and each later round
  replaces it there.
- **tillr's answers** are this document: [Section 1](#1-what-tillr-will-change) and
  [Section 7](#7-the-second-round-tillrs-answer) for each request's fate,
  [the build plan](tillr-definitions-plan.md) for what is next,
  [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now) for the contract, and the
  [Open Questions](#open-questions) and [the ledger](#decision-ledger) for what waits on the owner.
- **tillr's designs** reach a consumer's agent through
  [the design index](../tillr/design-status.md), which the tillr pack ships.

---

## 3. Proposed work

Four pieces of work, none built and none needing a ruling ([`D-FQ1`](#decision-ledger)).

**P2. Document how a program that is not the drain uses tillr on its own store.** *(A3.)*

- The tillr operating documentation gains a recipe: an explicit `--store`, a lane root outside the
  checkout, `tillr run exec` for runs, and no drain or keeper on that store unless it is the only
  dispatcher.
- The recipe states that one store has one dispatcher, and names the holds the drain gives a store
  with a second one.
- [tests/tillr-docs.test.py](https://github.com/mschulkind-oss/tillr/blob/main/tests/tillr-docs.test.py) checks each command in it.

**P3. Ship a stock adapter for plain commands run through `tillr run exec`.** *(A4.)*

- A tracked adapter directory holds `pause-kind`, which says `cannot-resume`, and an
  `extract-report` that writes an empty report.
- A test runs `/bin/true` and a command that exits 1 through it, with no git and no drain, and
  asserts both end causes and the three run events.

**P4. Publish tillr's consumer contract and record each change to it.** *(A5.)*

- A tracked reference holds [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now), and
  this document links to it instead of carrying it.
- A test fails when a face it names does not answer `--help`, or a record kind it names is not a
  constant in the code.
- A dated "Changes" list in it gains a line in the same commit as any change to the stable surface.

**P6. Let `tillr run status` say when a run's process is gone.** *(B5. It touches the drain's
interrupted-run code in [orphan.go](../../tillr-drain/drain/orphan.go), so it goes last.)*

- `tillr run status` reports a run whose recorded process does not exist as interrupted, using the
  same rule the drain uses.
- The drain calls the run package for that rule instead of its own copy.
- A test kills a stub run and asserts the status, with no drain running.

---

## 4. Where the documents disagree

[tillr-autoqa.md](tillr-autoqa.md), tillr's own design for exploratory testing of tillr, is
parked and differs from the code in these places. The code and
[the item-graph reference](../reference/tillr-item-graphs.md) are right in each. None of this
concerns Product QA, which files into stores of its own.

1. It describes read faces failing on a fresh store, and a bootstrap that works around it. They do
   not fail (A1).
2. It files confirmed bugs into the tested system's store "solely via `item apply`". Sync is the
   only intake ([tillr-convergence.md](tillr-convergence.md)), and such an item goes to triage
   for its block. Which is right is [OQ-FQ8](#OQ-FQ8).
3. Its `verify_repro` step files a child item from a `no_commit` graph through a `derived_from`
   edge. Those edges are not built, and the drain writes child items only after a landing, which a
   `no_commit` graph never reaches ([OQ-FQ7](#OQ-FQ7)).
4. Its judge stage names `role: "evaluator"`. An agent stage names a `seat` (`D-IG13`), and only
   the policy names a model.

---

## 5. The requests, restated

The first round asked for thirteen things. Each is listed with where it went.

| # | Request | Where it went |
| :--- | :--- | :--- |
| 1 | A store that needs neither git nor the drain, where every view works | [A1](#11-now-or-queued-at-once) |
| 2 | Several stores in one workspace, each addressed explicitly, each with its own loop and keeper | [A3](#11-now-or-queued-at-once), [C8](#13-what-tillr-will-not-change) |
| 3 | Outcomes defined by the kind of item, with inconclusive as a first-class outcome | [C1](#13-what-tillr-will-not-change) |
| 4 | Reopening from any finished state, with a reason | [A2](#11-now-or-queued-at-once), [C2](#13-what-tillr-will-not-change) |
| 5 | Graph changes as recorded operations that a running step can request, each applied atomically with its event | [C3](#13-what-tillr-will-not-change) |
| 6 | Steps that are not agents, with declared inputs and outputs | [B1 and B2](#12-on-the-existing-design-path) |
| 7 | Completion that is not a landing, keeping "judged negative" apart from "could not judge" | [B3](#12-on-the-existing-design-path), [C7](#13-what-tillr-will-not-change) |
| 8 | An uncertain outside effect as a state that blocks a retry | [C4](#13-what-tillr-will-not-change) |
| 9 | Leases on their own, with no worktree, brief template or drain | [B5](#12-on-the-existing-design-path), [A4](#11-now-or-queued-at-once) |
| 10 | Dispatch policy a consumer can supply | [B7](#12-on-the-existing-design-path), [C5](#13-what-tillr-will-not-change) |
| 11 | An owner acknowledgment point | [B9](#12-on-the-existing-design-path) |
| 12 | Complete events: each step attempt with its purpose, inputs, actor, time and result | [B6](#12-on-the-existing-design-path) |
| 13 | A Go library as the connection point | [C6](#13-what-tillr-will-not-change) |

---

## 6. Why the boundary is where it is

**tillr is separable where it already is, and that part is made dependable.** The designed seam is
processes and records: each face reads a record and writes a record, and no utility calls another
inside one process. The store, the item face and `tillr run exec` work without git or the drain. A
consumer, a fresh machine and the tests all use them that way.

**tillr does not become a general workflow engine.** The item-graph design draws that line and
enforces it with loader checks: no cycles, no timers, no wiring a model wrote, no node that changes
anything outside its worktree, and one item with one outcome.

**Outcomes per kind.** The landing definition's seven states are closed on purpose: one projector
(the function that turns stored state into displayed state) keeps the board, the counts and the
planner in agreement. A consumer that needs "inconclusive" kept apart from "rejected" names its own
finished states in its own definition; the core does not grow a state for each use.

**Reopening.** In the landing definition `landed` and `resolved-elsewhere` are final, so that a
finished item never quietly returns to the queue. A finding that comes back is a new item that
names the old one, unless the consumer's definition lists the move.

**Graph changes.** A node is fixed once its run is recorded. A planning node may propose replacing
one later node that has not started with a tracked workflow; the drain applies the proposal in one
transaction and records a refused one with its reason (`D-IG3`). Separate deliverables are separate
items (`D-IG2`), and no model writes wiring (`D-IG5`, `D-IG9`).

**Several stores.** The keeper's discovery and lock, the loop's records and lanes, and the default
store path each assume one store per root. A second store gets its own root.

**State in documents.** The rule exists so that deleting the store and running sync rebuilds the
same queue. A consumer whose records are files it can re-import meets the same invariant with its
own importer; tillr does not grow a second sync dialect.

**A Go library.** Records carry a `record_version` and can be versioned. Go packages here change
daily and cannot be versioned cheaply, so linking them would freeze names that are not a contract.

**Does an item hold the record, or point at files?** Both:

- **The store holds what a decision was made against.** Criterion text is stored and frozen, and a
  verdict names its sha256. Verdicts, decisions, states and actors are rows and events.
- **Files hold the bytes.** The item's prose is a document section, named by `body_ref`. Run
  reports, logs and briefs are files the run record points to. Node outputs are files recorded with
  name, type, sha256 and location.

Whatever a verdict relied on is bound by its digest, so an edited file cannot silently change what
was judged.

**Two dispatchers on one store** are never supported: the planner holds the other's items. Keep
the stores separate.

---

## 7. The second round: tillr's answer

The second round's requests are recorded as received in
[tillr-product-qa-requests.md](tillr-product-qa-requests.md). tillr answers by building
([OQ-FQ1](#decision-ledger)), so this section maps each request to its state. Nothing is declined
([`D-FQ5`](#decision-ledger)): a tension between Git landing and another use puts the choice in the
definition, so the answer is the extension point. "Next tranche" is the unbuilt list in
[the build plan](tillr-definitions-plan.md).

### 7.1 The fourteen requests

| Request | State |
| :--- | :--- |
| R2-1 Every table created at `store init` | Built |
| R2-2 A store declared not to land | Built, and shown end to end by the worked example |
| R2-3 Finished states keep their reasons, and leaving one is an event | Built |
| R2-4 The raw `state` and `apply` paths closed | Built for moves out of a finished state. A raw write of `landed` with an open criterion is the terminal-state block's ([A2](#11-now-or-queued-at-once)) |
| R2-5 Command steps, with the item id passed to the adapter | Built |
| R2-6 Runs, verdict reasons, decisions, close and reopen on the item page | Built |
| R2-7 A detail panel resolved from `body_ref` by a hook | Half built: the `detail` hook is in the hook contract; no page draws it. Next tranche |
| R2-8 Outcomes per kind, countable | Half built: finished states per definition. Counts in the views are next tranche |
| R2-9 A core that does not preclude a definition's graph changes | Not built. Next tranche |
| R2-10 No fixed graph size limits | Not built. Next tranche |
| R2-11 No clock whose only consequence is a kill | Partly built: default run walls are removed (`D-RW1`; `timeout_ms: 0` holds a lease with no expiry, in `tools/tillr-run/run/lease.go`) and the gate's kill ceiling is a stall watchdog (`D-GR15`, `tools/tillr-gate/watchdog.go`). `LeaseGraceMS` (300 s) remains for runs with an explicit `timeout_ms`, with no plan to remove it |
| R2-12 An uncertain outward effect as a state that blocks a retry | Not built. Next tranche |
| R2-13 Time and tokens per step attempt, with their source | Not built. Next tranche |
| R2-14 An owner acknowledgment point | Not built. Next tranche; `tillr view events --since` with a cursor meanwhile |

### 7.2 The stable-surface corrections

| Correction | State |
| :--- | :--- |
| `--format` is listed and no face defines it | The contract does not list it |
| No face lists items | Built: `tillr item list` |
| `item apply` drops an unknown field | Built; the `producer.system` rule is in the contract |
| `run exec` overwrites a scratch-directory variable, undocumented | Documented in the contract |
| The waiting class disagrees between the command and the served answer | Not built. Next tranche |
| A run requested but not started by the drain is held `run-link-missing` | Not built. Next tranche |
| An agent step's brief has sections shaped for Git | Built: plain-directory staging for a definition that does not land |

### 7.3 Their four questions

1. **A person as a step.** Two shapes. A hold that names the owner between items is built:
   `blocked` with waiter `owner`, the `question-open` hold and `conflict-owner`. A node whose result
   is the owner's recorded answer is not built; the nearest parts are a human verdict under a
   grant, the decision ledger, and the owner-criterion hold in
   [the lifecycle design](tillr-judge-and-lifecycle.md).
2. **The concern behind the graph caps.** `D-IG4` gives one reason: runaway expansion, and the
   spend that follows it. Expansion is already finite another way, because a cycle among workflow
   files is refused. The guard proposed in its place is the graph's size shown before its first
   node runs, and a budget a definition may set that leads to a named next step for the owner,
   never a silent refusal.
3. **Leases for `run exec`.** With `timeout_ms` above 0 the run is killed at its wall and its lane
   is leased to the start plus the wall plus 300 seconds (`LeaseGraceMS` in
   [lease.go](../../tillr-run/run/lease.go)), after which a reaper may free it as
   `lease-expired`. With `timeout_ms` 0 the lease has no expiry, and only `tillr run release` frees
   the lane. The drain's runs take the second rule (`D-RW1`).
4. **The item page for an item with no workflow.** Such an item is drawn as one node from its run,
   and the page lists every run, whether or not it made a landing candidate.

---

## Evidence

The behavior claims in this document were run with the binaries `just tillr-build` produces, on a
new store file outside the repository (`$S`), last on 2026-11-18:

```text
tillr store init --store $S; tillr view waiting --store $S    "definition: git-landing ... held []", exit 0
tillr drain why / tillr run lanes --store $S                  exit 0
tillr item state --state landed (criterion open, no landing)  exit 0, state landed
tillr item state --state in-queue (on that landed item)       item.rejection: raw writes cannot change a
                                                              finished item
tillr item state --state resolved-elsewhere (fresh item,
  no pointer)                                                 exit 0
tillr item reopen (landed item)                               exit 1, rule item-not-closed
tillr item close …; tillr item reopen …                       exit 0 both; reopened to in-queue
tillr run exec --store $S --input <run.request> \
  --lane-root <dir> --backends-dir <stub adapters>            exit 0; end_cause normal-exit;
                                                              events run.allocate, run.usage, run.finish
  (absolute working_dir)                                      refused: must be relative to the lane
  (no adapter directory)                                      exit 2: reading adapter pause-kind
tillr-item apply (producer.system not "tillr")                exit 2: record from another system
tillr-view status --format json                               exit 2: flag provided but not defined: -format
fd -t d internal tools/ (tillr modules)                       nothing
fd -i changelog tools docs                                    nothing
```

**Not determined:**

- whether the built command nodes, node outputs and exit condition carry the three adjustments of
  [`D-FQ3`](#decision-ledger);
- whether two drain loops with different roots can run on one machine;
- whether the modules are published anywhere.

---

## Open Questions

Two questions, both blocked. Neither asks anything of the owner until what it waits on exists.

7. 🔒 **OQ-FQ7: Should work that finishes without a commit be able to file child items inside tillr?**

   A consumer's investigation finds that one reported finding is really two bugs. Its work never
   lands a commit, and tillr files child items only after a landing, so tillr cannot file the two
   bugs from that work. The consumer files them itself with `tillr item apply`, and that works.

   - **A — Yes.** tillr files the children when the work settles, the same single-writer moment a
     landing is.
   - **B — No.** The consumer's own dispatcher files them.

   <!-- vantage: question id=OQ-FQ7 blocked-on="the owner taking the AutoQA design out of the Icebox" -->

   Waits on a consumer that needs tillr, and not its own dispatcher, to file the children. There is
   none: Product QA files them itself, and tillr's own [AutoQA](tillr-autoqa.md), which has the
   same need, is parked. When one appears, the question belongs to
   [the idea pipeline](tillr-idea-pipeline.md), whose ruling on child items it would amend.

   **Answer:**

   > _(empty — fill in when decided)_

8. 🔒 **OQ-FQ8: How does a bug that tillr's own AutoQA confirms get into tillr's queue?**

   tillr's own AutoQA design files the bugs it confirms into the store of the system it tests,
   which for tillr is tillr's own queue. It is not a Product QA question: Product QA files only into
   stores of its own.

   - **A — As a document block** that AutoQA writes and sync imports.
   - **B — Through `tillr item apply`, with a store setting** that exempts such items from needing
     a block.
   - **C — Through `tillr item apply`,** so the bug arrives without a block and triage writes one.

   <!-- vantage: question id=OQ-FQ8 blocked-on="the owner taking the AutoQA design out of the Icebox" -->

   Waits on the AutoQA design, which is parked and has no code. When it is picked up, this question
   moves to [tillr-autoqa.md](tillr-autoqa.md).

   **Answer:**

   > _(empty — fill in when decided)_

---

## Decision Ledger

Three kinds of row, kept apart on purpose.

### The owner's rulings

| ID | Ruling | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-FQ1 | tillr answers a consumer's requests by building as much as possible for it to try, not by a written proposal and a further round of talk. | 2026-11-17 | [Section 7](#7-the-second-round-tillrs-answer) | ✅ |

### The designer's decisions, awaiting the owner's review

None is the owner's ruling, and the owner may overturn any of them.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-FQ3 | "Are P1 to P4 filed now, and P6 after?" Closed by the designer's `D-FQ1`. | 2026-11-18 | [Section 3](#3-proposed-work) | — |
| `D-FQ1` | P2, P3, P4 and P6 need no ruling and are ready to build, P6 last. **Evidence:** each of the first three is hours of work, and nothing they waited on remains. **Rejected:** holding them for the owner, who decides direction and leaves item filing to the engineer. | 2026-11-18 | [Section 3](#3-proposed-work) | — |
| OQ-FQ6 | "Should the raw state face stop moving items out of a finished state before the rest of the terminal-state block lands?" Closed by the designer's `D-FQ2`. | 2026-11-17 | [Section 1.1](#11-now-or-queued-at-once) | ✅ |
| `D-FQ2` | The move out of a finished state is closed ahead of the rest, and on the `apply` face too. **Evidence:** it is the move that puts finished work back in the queue, and it waited on nothing. **Rejected:** leaving it behind two other lifecycle items; and an operator-only flag for writing `landed`, an authorization rule no design has. | 2026-11-17 | [Section 1.1](#11-now-or-queued-at-once) | ✅ |
| `D-FQ3` | Three adjustments were proposed to the criteria of command nodes (the registry is read from under the drain's `--root`), node outputs (each accepted output is also an event, and a read face prints them) and the exit condition (the end state and its receipt are named, and a read face prints the receipt). Whether what is built carries them is checked by whoever builds P4, and a missing one is follow-up work with the same criterion. | 2026-11-18 | [Section 1.2](#12-on-the-existing-design-path) | — |
| `D-FQ4` | The face stays `tillr item apply`. **Evidence:** it adds or updates, and records which as the event. **Rejected:** `item add`, which misnames an update. | 2026-11-18 | [Section 2.1](#21-the-stable-surface) | ✅ |
| `D-FQ5` | Every one of the fourteen second-round requests is accepted, and none is declined. **Evidence:** the owner's decision rule, that a tension between Git landing and another use puts the choice in the definition. | 2026-11-17 | [Section 7.1](#71-the-fourteen-requests) | — |

### Closed without a decision

| ID | What happened | Date | Now lives in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-FQ2 | "Should a finished item record how it ended, in words its kind defines?" Superseded: lifecycle is the definition's, so each definition names its own finished states, and the worked example ends items as confirmed, rejected, inconclusive, repeat or tooling fault. Counting them in the views is not built. | 2026-11-18 | [The direction](tillr-core-and-git-landing.md) | ✅ |
| OQ-FQ4 | "Are tillr's commands and records the only supported connection for another program?" Nothing left to decide: the request for a Go library was withdrawn, and commands and records are the seam [board-utilities.md](board-utilities.md) sets. | 2026-11-17 | [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now) | ✅ |
| OQ-FQ5 | "Does tillr hold a design round on an artifact recorded and judged as a second way an item completes?" Superseded: a definition may complete its items on a hook's recorded verdict, with no landing. Anything wider is not asked until a second definition shows a case the hook cannot express. | 2026-11-18 | [Section 1.3](#13-what-tillr-will-not-change) | ✅ |
