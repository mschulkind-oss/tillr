---
title: "What tillr will change for Product QA, what it promises, and what it will not change"
status: in-review
stage: DESIGN
updated: 2026-11-18
tags:
  - tillr
  - design
  - store
  - boundaries
  - product-qa
summary: "tillr's proposal to the Product QA pipeline and its answer to their second round: what changed, what tillr promises as a command-and-record contract, which earlier no's turned out to be real tensions, and a table mapping each of the fourteen second-round requests, the surface corrections and four questions to the item that built it or the unfiled work that will. The first round's assessment is kept below as a dated record."
next: "The owner reviews the designer's decisions D-FQ1 to D-FQ5. Four small pieces of work (P2, P3, P4, P6) and the build plan's next tranche are unfiled. Product QA's agent runs the worked example and writes round three. OQ-FQ7 and OQ-FQ8 stay blocked until a consumer needs them."
depends-on:
  - tillr-core-and-git-landing.md
  - tillr-product-qa-requests.md
  - tillr-definitions-plan.md
  - ../reference/tillr-item-graphs.md
  - tillr-judge-and-lifecycle.md
  - tillr-idea-pipeline.md
---

# What tillr will change for Product QA, and what it will not

**Status:** 2026-11-18. tillr's answer to Product QA's second round
([Section 7](#7-the-second-round-tillrs-answer)), under the owner's direction in
[tillr-core-and-git-landing.md](./tillr-core-and-git-landing.md): tillr is a general core plus one
definition per use, and Git landing is the first definition. The owner said to build on 2026-11-17,
and the eighteen items of [the build plan](./tillr-definitions-plan.md) have all landed. The
requests they answer were re-checked on 2026-11-18 with the installed binaries on throwaway stores;
the first round's facts are of 2026-11-16 and the second round's of 2026-11-17, each dated where it
stands. Commands and results are in [Evidence](#evidence). Each claim is marked **[ran]**,
**[read]** (file and section) or **[inferred]**. Five decisions here were the designer's, not the
owner's; they are in [the ledger](#decision-ledger) and await his review.

> **In short.** tillr's store, items, verdicts and leased runs work outside Git, and a store that
> never lands now runs end to end. Nothing Product QA asked for was declined: each request is either
> built or mapped to unfiled work in [Section 7](#7-the-second-round-tillrs-answer) (except R2-11,
> where `D-RW1` and `D-GR15` removed the run and gate kill clocks, while `LeaseGraceMS` on
> explicit-timeout runs stays in `tillr-run` without a plan).

**Why it matters.** Product QA's agent builds against what this document promises. A promise that is
stale costs it a wrong build; [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now) is
the part it relies on.

**Cost.** tillr keeps a stable surface of commands and records for a second project, and announces
every change to it.

**Start at [Section 7](#7-the-second-round-tillrs-answer)**, the answer to the second round, then
[the build plan](./tillr-definitions-plan.md).
[Section 2](#2-the-contract-between-tillr-and-product-qa-for-now) is the contract Product QA's agent
builds against. [Sections 5 and 6](#5-the-requests-restated) are the first round, kept as a dated
record.

**Needs your ruling:** None now. [OQ-FQ7](#OQ-FQ7) and [OQ-FQ8](#OQ-FQ8) are blocked, each on
something named in it, and ask nothing of you until then.

**Awaits your review:** the designer's decisions `D-FQ1` to `D-FQ5` in
[the ledger](#decision-ledger).

**Reads with:** [tillr-core-and-git-landing.md](./tillr-core-and-git-landing.md) (the direction
this proposal sits under), [tillr-definitions-plan.md](./tillr-definitions-plan.md) (the built
items and the unfiled next tranche), [tillr-product-qa-requests.md](./tillr-product-qa-requests.md)
(Product QA's second-round reply, as received), [tillr-item-graphs.md](../reference/tillr-item-graphs.md)
(graphs inside one item), [tillr-judge-and-lifecycle.md](./tillr-judge-and-lifecycle.md) (item
states and the terminal-state rulings), [tillr-idea-pipeline.md](./tillr-idea-pipeline.md) (item
kinds and child items), [board-utilities.md](./board-utilities.md) (the process-and-record seam),
[the vocabulary](./tillr-vocabulary.md) (which retires the word "lane" and keeps this contract's
lane names as aliases).

<a id="for-the-autoqa-projects-agent"></a>

## For the Product QA project's agent

**Read first, for round three**, in this order:

1. [The tillr design index](../tillr/design-status.md), which the tillr pack ships.
2. [Section 7](#7-the-second-round-tillrs-answer) of this document: each of your requests, what
   held when you reported it, and what is built now.
3. [The build plan](./tillr-definitions-plan.md): the eighteen built items and what you can try
   with each.
4. [The direction, Section 8](./tillr-core-and-git-landing.md#8-building-it-the-owners-go-ahead-and-the-working-choices):
   the choices the build rests on, and which of them the owner has not yet reviewed.
5. The worked example, in the pack's copy of the core library under `examples/recorded-verdict/`:
   a store that lands nothing, run from an empty directory.

**React to:** what you find when you run it. Where the real thing differs from what Section 7 says,
that is a defect in tillr or in this document, and either is worth a line.

**Write the reply** in your own repository and ask the owner to relay it. tillr records each round
as received in [tillr-product-qa-requests.md](./tillr-product-qa-requests.md), replacing the last; git
history keeps every round. [Section 2.4](#24-how-requests-and-answers-travel) says how it is
answered.

**Two stand-in words** are used here as in the direction document: a **definition** is the layer
that turns tillr's core into one use, and the **landing definition** is the one for Git landing.
The owner named both on 2026-11-18 in the vocabulary: the layer is a **charter**
([OQ-VO4](./tillr-vocabulary.md#OQ-VO4)) and the first use is **`git-landing`**
([OQ-VO5](./tillr-vocabulary.md#OQ-VO5)), so "definition" here is the older spelling of
"charter". Shipped code is not renamed, and the flag `--definition` keeps working.

---

## The words this document uses

- **Product QA:** a product bug-finding pipeline, with the fuzzer Mangle as its prober, in
  a repository of its own. Its agent and the owner wrote both rounds of requests. Its word for
  itself; its own coined terms are listed in its reply.
- **AutoQA:** not Product QA. [tillr-autoqa.md](./tillr-autoqa.md) is tillr's own design for a
  standalone exploratory-testing tool that runs its own tillr store and is aimed first at tillr
  itself. The owner sometimes calls Product QA's requests "the AutoQA stuff"; the two are different
  projects.
- **Consumer:** a program other than tillr's own loop that uses tillr's commands on a store. Here,
  Product QA.
- **Store:** tillr's SQLite database of items, runs and events.
- **Face:** one subcommand of a tillr program, such as `tillr item apply`. **Record:** the JSON
  document a face reads or writes, in the envelope
  [board-utilities.md §5.1](./board-utilities.md#51-the-common-contract) defines.
- **Drain:** the loop that plans, runs, gates and lands items. **Keeper:** the program that restarts
  the drain when it exits ([tillr-keep](../../tools/tillr-roll/tillr-keep)).
- **The drain proof** *(coined here)*: the owner's goal, at the first round, of landing the
  existing queue through the drain unattended before anything else. The first round ordered work
  against it; the owner's go-ahead of 2026-11-17 set that ordering aside.
- **Lane:** a leased slot with its own directory, where one run executes. **Lease:** the claim on a
  lane, which expires at the run's wall plus a grace.
- **Fence:** the set of paths an item may change.
- **Gate:** the check run on a candidate commit. **Lander:** the program that puts a gated commit
  on the trunk.
- **Workflow, graph and node:** as defined in
  [tillr-item-graphs.md, The words](../reference/tillr-item-graphs.md#the-words). A workflow is a tracked
  template of stages. Running it gives an item a graph, and each node is one agent or one command
  call.
- **Criterion:** one testable condition on an item. **Verdict:** a ruling on one criterion.
- **Hold:** the planner's named reason for not dispatching an item.
- **`no_commit` exit:** a workflow's declaration that its item finishes without a commit, so no
  gate or landing runs ([exit requirements](../reference/tillr-item-graphs.md#exit-requirements-and-no-commit-completion)).
- **Unshared item:** an item in a store that no tracked document declares. Sync is the only intake
  ([tillr-convergence.md §3](./tillr-convergence.md#3-the-target)), so such an item cannot be
  rebuilt by deleting the store and syncing.

---

## 1. What tillr will change

Each entry says what changes, why, and its size. Each then says what Product QA may rely on today
(2026-11-18), what is promised, and what is not promised.

### 1.1 Now, or queued at once

Five changes, each hours of work. A1 is built and A2 is half built. A3 to A5 are unfiled: they
need no ruling ([`D-FQ1`](#decision-ledger)), and [Section 3](#3-proposed-work) says what holds
them.

**A1. Every read face and the planner answer on a store the drain never opened.**

- **Why:** on a store built with `tillr-store init` and `tillr-item apply`, `tillr view waiting`
  and `tillr drain why` exited 3 with `no such table: lanes`, because each utility added its tables
  only when it first opened a store for writing. A fresh machine hit this too.
- **Today:** built (`item-def-tables-at-init`). After `tillr store init` alone, `tillr view
  waiting`, `tillr drain why` and `tillr run lanes` exit 0 **[ran]** 2026-11-18.
- **Promised:** every read face exits 0 on a store holding only `init` and `apply`.
- **Not promised:** that a write face creates another utility's tables.

**A2. A raw `tillr item state` can no longer move a finished item.**

- **Why:** a raw `tillr item state` moved an item to `landed` with an open criterion and no
  landing, moved it back out to `in-queue`, and moved it to `resolved-elsewhere` with no pointer.
- **Today:** the move out is closed (`item-def-finished-states-final`): a raw write that would
  change a finished item is rejected, through `state` and through `apply` **[ran]** 2026-11-18. The
  other two still succeed **[ran]**: a raw write to `landed` with an open criterion, and to
  `resolved-elsewhere` with no pointer. The queued
  [terminal-state block](./tillr-judge-and-lifecycle.md#prevent-landed-and-resolved-states-without-their-evidence)
  closes them in its criteria c1 and c2.
- **Promised:** a finished item stays finished against a raw writer; c1 and c2 when that block
  lands.
- **Not promised:** a check of *who* may write a state; actors are recorded, not authorized.

**A3. The explicit-store route is written down.**

- **Why:** every face takes `--store` **[read]**, and a plain command ran as a leased run with an
  end cause on a store the drain never opened **[ran]**. Nobody outside this repository knows that.
- **Size:** an hour; documentation only. Unfiled, as P2.
- **Today:** it works, undocumented. A store has one dispatcher: the drain holds an item whose run
  it did not dispatch as `run-link-missing`, and an item with no fence as `no-fence` **[ran]**.
- **Promised:** a recipe whose every command is checked to answer `--help` by
  [tests/tillr-docs.test.py](../../tests/tillr-docs.test.py).
- **Not promised:** two dispatchers on one store, ever.

**A4. A stock adapter for plain commands.**

- **Why:** `tillr run exec` needs an adapter directory holding `pause-kind` and `extract-report`
  beside the command, and refuses a working directory that is not relative to the lane **[ran]**.
  Every caller writes the same stub today.
- **Size:** hours. Unfiled, as P3.
- **Today:** works with a hand-written stub.
- **Promised:** one tracked adapter that `--backends-dir` can name, which says `cannot-resume`.
- **Not promised:** resuming a plain command.

**A5. The contract is published, and each change to it is recorded.**

- **Why:** no changelog or breaking-change record exists for tillr (`fd -i changelog tools docs`
  finds nothing) **[ran]**. Each module README has a dated "Known Gaps" callout **[read]**.
- **Size:** hours. Unfiled, as P4.
- **Today:** [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now) of this document.
- **Promised:** the contract in a tracked reference, a test that every face and record kind it
  names exists, and a dated line in it for every change to the stable surface.
- **Not promised:** that the surface stops changing; only that a change is announced.

### 1.2 On the existing design path

These were already designed when Product QA asked, and all but B9 have landed. For B1 to B3 the
first round proposed an adjustment to one criterion so the item would also serve Product QA; whether
what landed carries each adjustment was not checked on 2026-11-18, and one that is missing becomes
follow-up work with the same criterion.

**B1. Command nodes.** Built (`item-ig-command-node`).

- **Adjustment proposed, its c1:** the command registry is read from under the drain's `--root`,
  tested with a root outside this checkout. Product QA's re-check command, `mangle triage rejudge`,
  lives in Product QA's root, not here.
- **Promised:** the block's criteria. **Not promised:** skipping a node whose inputs did not
  change.

**B2. Node outputs.** Built (`item-ig-node-outputs`).

- **Adjustment proposed, its c4:** each accepted output is also an event in the log, and a read
  face prints a node's outputs as records, so a consumer never reads the drain's table. This is
  also what "complete events" (B6 below) needs for a node's inputs.
- **Promised:** name, type, sha256 and location per output. **Not promised:** that the table is
  readable by SQL.

**B3. Finishing with no commit.** Built (`item-ig-exit-condition`). A definition that does not land
finishes its items on a hook's recorded verdict instead (`item-def-finish-on-hook`).

- **Adjustment proposed, its c3:** name the state such an item ends in and the receipt it ends on,
  and a read face that prints the receipt.
- **Promised:** an investigation-only item that finishes. **Not promised:** child items from it
  ([OQ-FQ7](#OQ-FQ7)).

**B4. The evaluator slot.** Built (`item-ig-evaluator-node`).

- **Adjustment:** none on tillr's side. Product QA's judge is an independent verifying agent, a
  different actor from the investigator, which the no-self-judging rule already enforces.
- **Promised:** the block's criteria. **Not promised:** a judge that is not the judge step.

**B5 and B6. Leases without the drain, and complete events.**

- **Leases** are carried by [board-utilities.md §5.6](./board-utilities.md#56-run-alone), and are
  built: `tillr run exec` claims a lane, leases it until the run's wall plus a 300-second grace,
  writes the end cause and releases it, and `tillr run release` frees a lane by hand **[read]**
  **[ran]**. [§5.6](./board-utilities.md#56-run-alone) refuses a heartbeat on purpose, because `run` is one process with nothing resident
  to renew it **[read]**.
- **Events** are partly built: a throwaway store recorded `item.add`, `item.update`,
  `run.allocate`, `run.usage` and `run.finish` **[ran]**. Node inputs and outputs arrive with B2 and
  its adjustment.
- **Today:** leases by wall and end causes; events as listed.
- **Promised:** node output events, where B2's adjustment is in what landed.
- **Not promised:** a heartbeat; a "purpose" field on run events beyond the node a run belongs to;
  telling a dead run from a live one without the drain. That last one is unexported drain code
  ([orphan.go](../../tools/tillr-drain/drain/orphan.go)) **[read]**, no design carries it, and
  [P6](#3-proposed-work) proposes it.

**B7. Dispatch policy.** Built: seat shares (`item-policy-seat-shares`), lanes outside the cap
(`item-drain-express-lanes`) and a spend ceiling (`item-drain-spend-ceiling`), beside the lane
count, the backend policy, and pluggable gate, land and verdict programs.

- **Adjustment:** none. A second dispatcher is A3's documented separate store.
- **Not promised:** deadlines, which are [C5](#13-what-tillr-will-not-change).

**B8. Items fed by `item apply` keep working.** Built (`item-sync-hold-unshared-items`): an
undeclared item goes to triage instead of a hold, and a store where no document block declares any
item is left alone, as the owner asked
([the direction, Section 2](./tillr-core-and-git-landing.md#2-the-workflow-the-landing-definition-wraps-as-it-runs-today)).
That covers Product QA's own stores.

**B9. An owner acknowledgment point.** No design carries it. `tillr view events --since` takes a
cursor **[ran]**, and mail has an acknowledgment **[read]**; a stored "the owner has seen up to here"
does not exist. It is item 7 of the build plan's unfiled next tranche. Product QA keeps its own cursor
in its own ledger for now.

### 1.3 What tillr will not change

Each row says what holds inside the landing definition. By the owner's
decision rule, a choice that suits Git landing and chafes against Product QA belongs to each
definition and not to the core, so several of these open up for Product QA's own definition;
[the direction, Section 6](./tillr-core-and-git-landing.md#6-what-this-changes-in-what-was-said-before)
says which. The arguments from both sides are in
[Section 6.3](#63-the-requests-that-cut-against-the-design).

| | Request | Holds today, and inside the landing definition | Meanwhile, Product QA | Product QA, round two |
| :--- | :--- | :--- | :--- | :--- |
| C1 | New item states per kind | The closed seven-state model ([D-IP1](./tillr-idea-pipeline.md#61-settled-decisions)). The owner places lifecycle in the definition, so another definition names its own, which is built | names its own finished states in its definition file, as the worked example does | Real tension: inconclusive must be countable (R2-8) |
| C2 | Reopening a landed or resolved-elsewhere item | Both are terminal ([lifecycle §3.2](./tillr-judge-and-lifecycle.md#32-transition-preconditions-and-attempt-holds-d-jl4)); a lifecycle rule, so the definition's | lists the move in its own definition's valid moves, which is built. Reopening a `closed` item works in the landing definition **[ran]** | Real tension: the owner, as reported, rules it the definition's policy (R2-3) |
| C3 | A running step that rewrites the graph freely, and placeholders for work of unknown shape | Bounded replacement only, no model-written wiring (`D-IG2`, `D-IG3`, `D-IG5`, `D-IG9`, [proposals and graph revisions](../reference/tillr-item-graphs.md#proposals-and-graph-revisions)) | keeps its graph in its dispatcher, or one tillr item per finding | Real tension, narrowed: free rewriting withdrawn; insertion downstream and appending remain (R2-9) |
| C4 | An uncertain outside effect, such as a lost send, as a state inside a graph | No node changes anything outside its worktree ([what a workflow node may never be](../reference/tillr-item-graphs.md#what-a-workflow-node-may-never-be)) | keeps its send step and its uncertain state. Outside a graph, `blocked` with a waiter works today **[ran]** | Real tension: a report send that lost its session (R2-12) |
| C5 | Deadlines and clocks in the plan | No timers ([what a workflow node may never be](../reference/tillr-item-graphs.md#what-a-workflow-node-may-never-be), the retired agent runtime design, section 6) | orders and times work in its dispatcher | Withdrawn: the time box is the fuzzer run's own business |
| C6 | A Go library as the connection | Processes and records ([board-utilities.md §5.9](./board-utilities.md#59-the-composed-pipeline)) | calls the commands in [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now) | Withdrawn: processes and records are enough |
| C7 | Completion by "an artifact recorded and judged" in general | A landing completes an item; another definition completes on its hook's recorded verdict, which is built | declares `completes: "hook"` in its definition file | Real tension: a recorded, judged artifact (R2-2) |
| C8 | A second loop and keeper beside the first, in one root | The keeper finds a loop by its working directory and locks per root ([tillr-keep](../../tools/tillr-roll/tillr-keep), `find_loop`) | runs a loop with its own root, which gets its own lock and lanes **[inferred]**, not run | Withdrawn: it runs its own store and root |

---

<a id="2-the-contract-between-tillr-and-autoqa-for-now"></a>

## 2. The contract between tillr and Product QA, for now

### 2.1 The stable surface

Product QA may rely on these. A change to any of them is announced as
[Section 2.3](#23-how-a-consumer-learns-of-a-breaking-change) says.

- **Commands:** each program as `tillr-<noun> <face>`, or through the umbrella as
  `tillr <noun> <face>`.
- **Faces a consumer may call** (**[ran]** for `--help` on 2026-11-16; `list` added since):
  - `tillr-store init`, `check`, `events`;
  - `tillr-item apply`, `list`, `show`, `state`, `close`, `reopen`, `verdict`, `grant`, `decision`,
    `mail`;
  - `tillr-run exec`, `status`, `lanes`, `release`;
  - `tillr-view status`, `events`, `waiting`, `passes`, `lanes`, `running`;
  - `tillr-drain why` and `plan`, which only read; and `tillr-drain once` and `loop` on the
    consumer's own store and root.
- **Flags:** `--store`, `--input`, `--now`, `--root`, `--item`, `--lane-root`, `--backends-dir`,
  `--since`, and `--definition` on `tillr-store init`. No face defines `--format`. Store access is
  always explicit: no standalone face opens the live store
  on its own ([board-utilities.md §5.1](./board-utilities.md#51-the-common-contract)).
- **Exit codes** ([exit.go](../../tools/tillr/record/exit.go)): 0 done, 1 a negative decision, 2
  usage, 3 store or record version incompatible, 4 an outside dependency prevented a decision,
  5 an internal error.
- **Record envelope:** `record_version`, `kind`, `id`, `producer`, `created_at`, `subject`,
  `payload` and an optional `idempotency_key`. The version is 1 (`SupportedVersion` in
  [envelope.go](../../tools/tillr/record/envelope.go)), and a reader refuses a version it does not
  know.
- **Record kinds a consumer writes:** `item.record` (to `apply`) and `run.request` (to `exec`).
  `apply` refuses a record whose `producer.system` is not `tillr` ("record from another system"),
  so a consumer stamps that value **[ran]** 2026-11-17.
- **What `tillr run exec` sets in a run's environment** ([exec.go](../../tools/tillr-run/run/exec.go),
  **[read]** 2026-11-18): `TILLR_RUN_ID`, `TILLR_ITEM_ID`, `TILLR_NODE_ID`, `TILLR_LANE_ID`,
  `TILLR_SESSION_MODE`, `TILLR_RESUME_PROMPT_FILE`, `TILLR_PAUSE_REQUEST`, `TILLR_PROGRESS_FILE`,
  `TILLR_READ_ONLY_GROUP`, `TILLR_CHILD_SESSIONS_ROOT` and `TILLR_CHILD_SESSIONS_MARKER`. It also
  replaces any inherited value of the scratch-directory variable of this repository's jail, the one
  Product QA's reply names, with the run's own scratch directory, `<lane dir>/durable/<run id>`
  (**[read]** 2026-11-17; not re-read since `item-def-run-item-env` landed).
- **Names that carry the word "lane"** (`--lane-root`, `TILLR_LANE_ID`, `tillr-run lanes`,
  `tillr-view lanes`) stay as they are. [The vocabulary](./tillr-vocabulary.md#6-renames) retires
  the word from documents and keeps these as aliases.
- **Kinds a consumer reads:** `run.record`, the item and verdict records `show` prints, and the
  event kinds in the log.
- **Why the face is `tillr item apply` and not `item add`,** which the owner asked: it applies an
  `item.record` read from `--input` as either an add or an update, and writes the event `item.add`
  or `item.update` accordingly (`Apply` in [apply.go](../../tools/tillr-item/item/apply.go),
  `AddKind` and `UpdateKind` in [schema.go](../../tools/tillr-item/item/schema.go)) **[read]**. The
  name stays ([`D-FQ4`](#decision-ledger)).

### 2.2 What is internal, and may change daily

- **Go packages.** Every package can be imported, because no module has an `internal/` directory
  and siblings resolve by relative `replace` **[ran]**, but none is supported. From 2026-11-13 to
  2026-11-16, 167 commits touched `tools/tillr*` **[ran]**.
- **The store's schema:** table names, columns and migrations. Read through a face, never SQL.
- **Hold reason names, view text, and file layout** under the drain's root.
- **Anything a module README's "Known Gaps" callout names.**

### 2.3 How a consumer learns of a breaking change

Today there is no mechanism **[ran]**. Proposed, as [A5](#11-now-or-queued-at-once):

- A change to a record's fields gets a new `record_version`, and readers of the old version are kept
  for one round.
- Every change to the stable surface adds a dated line to the published contract in the same
  commit, so `git log` on that one file is the change feed.
- The module READMEs' Known Gaps callouts keep saying what is unfinished.

### 2.4 How requests and answers travel

- **Product QA's requests** are written by its agent with the owner, in Product QA's repository, one
  numbered request per entry, each with the behavior it checked and the date. The owner relays
  them. The first round arrived as an untracked draft, restated in
  [Section 5](#5-the-requests-restated); the second is recorded as received in
  [tillr-product-qa-requests.md](./tillr-product-qa-requests.md), and each later round replaces it
  there.
- **tillr's answers** are this document: [Section 1](#1-what-tillr-will-change) and
  [Section 7](#7-the-second-round-tillrs-answer) for each request's fate,
  [the build plan](./tillr-definitions-plan.md) for what was built and what is next,
  [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now) for the contract, and the
  [Open Questions](#open-questions) and [the ledger](#decision-ledger) for what waits on the owner.
- **tillr's designs** reach Product QA's agent through [the design index](../tillr/design-status.md),
  which the tillr pack ships.
- **Product QA's own design** is in its repository. [tillr-autoqa.md](./tillr-autoqa.md) is not it:
  that is tillr's own exploratory-testing design.

---

## 3. Proposed work

Six pieces of work the first round proposed. P1 and P5 are built under other ids. P2, P3, P4 and
P6 are unfiled and need no ruling ([`D-FQ1`](#decision-ledger)): the first round held them behind
the drain proof, and that ordering is gone. They are written as prose, and how they become items
waits on the owner's review, begun on 2026-11-18, of how work written in a document enters the
queue.

**P1. Let every read face and the planner answer on a store the drain never opened.** *(A1. Built
as `item-def-tables-at-init`.)*

**P2. Document how a program that is not the drain uses tillr on its own store.** *(A3. Unfiled.)*
Why: the route works and nobody outside this repository can find it.

- The tillr operating documentation gains a recipe: an explicit `--store`, a lane root outside the
  checkout, `tillr run exec` for runs, and no drain or keeper on that store unless it is the only
  dispatcher.
- The recipe states that one store has one dispatcher, and names the two holds the drain gives a
  store with a second one.
- [tests/tillr-docs.test.py](../../tests/tillr-docs.test.py) checks each command in it.

**P3. Ship a stock adapter for plain commands run through `tillr run exec`.** *(A4. Unfiled.)*
Why: every caller of a plain command writes the same stub today; the worked example carries one of
its own.

- A tracked adapter directory holds `pause-kind`, which says `cannot-resume`, and an
  `extract-report` that writes an empty report.
- A test runs `/bin/true` and a command that exits 1 through it, with no git and no drain, and
  asserts both end causes and the three run events.

**P4. Publish tillr's consumer contract and record each change to it.** *(A5. Unfiled.)*
Why: a consumer cannot tell a change from a defect without a stated contract.

- A tracked reference holds [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now), and
  this document links to it instead of carrying it.
- A test fails when a face it names does not answer `--help`, or a record kind it names is not a
  constant in the code.
- A dated "Changes" list in it gains a line in the same commit as any change to the stable surface.

**P5. Refuse a move out of `landed` or `resolved-elsewhere`.** *(A2. Built as
`item-def-finished-states-final`, widened to the `apply` face.)*

**P6. Let `tillr run status` say when a run's process is gone.** *(B5. Unfiled; days, and it
touches the drain's interrupted-run code, so it goes after P2 to P4.)*
Why: a consumer with no drain cannot tell a dead run from a slow one until its lease expires.

- `tillr run status` reports a run whose recorded process no longer exists as interrupted, using
  the same rule the drain uses.
- The drain calls the run package for that rule instead of its own copy.
- A test kills a stub run and asserts the status, with no drain running.

---

## 4. Where the documents disagree

These seven compare two other documents with the code: [tillr-autoqa.md](./tillr-autoqa.md), which
is tillr's own design for exploratory testing of tillr, and
[tillr-distribution.md](./tillr-distribution.md). They say nothing about Product QA, which files into
stores of its own and has no `qa-probe` graph, no `verify_repro` step and no `D-AQ` decisions.
Neither document was edited: the first is parked by the owner, and the second needs more than a
one-line fix.

1. **[tillr-autoqa.md §1.1](./tillr-autoqa.md#11-what-already-exists-at-head)**, the fresh-store
   row: "**Unchanged by design.** … read-only faces fail with missing-table errors". They no longer
   fail (A1), so the row and the bootstrap in
   [§2.2](./tillr-autoqa.md#22-two-stores-and-sandboxed-sut-snapshots) (`tillr-drain once` plus a
   `/bin/true` run) describe a workaround nobody needs.
2. **[tillr-autoqa.md D-AQ2](./tillr-autoqa.md#decision-ledger)** crosses into the system under
   test's store, here tillr's own, "solely via `item apply`". Sync is the only intake
   ([tillr-convergence.md §3](./tillr-convergence.md#3-the-target)), and such an item now goes to
   triage for its block. Which is right is [OQ-FQ8](#OQ-FQ8), blocked until that design is picked
   up.
3. **[tillr-autoqa.md D-AQ3](./tillr-autoqa.md#decision-ledger)** says its two graphs work
   "immediately on W1 Slice-1 static flattening". They needed command nodes, outputs, the
   `no_commit` exit and the evaluator, all of which have since landed
   ([B1 to B4](#12-on-the-existing-design-path)).
4. **[tillr-autoqa.md §3.1](./tillr-autoqa.md#31-test-idea-items-and-the-two-workflow-funnel)**:
   `verify_repro` "spawn[s] a child `qa-confirm@v1` item linked via `derived_from`" from a
   `no_commit` graph. `derived_from` edges are not built, and the drain writes child items only
   after `tillr-land pass` succeeds ([D-IP6](./tillr-idea-pipeline.md#61-settled-decisions)), which a
   `no_commit` graph never reaches **[read]**. Neither is queued. [OQ-FQ7](#OQ-FQ7).
5. **[tillr-autoqa.md §3.1](./tillr-autoqa.md#31-test-idea-items-and-the-two-workflow-funnel)**:
   `triage_judge` names `role: "evaluator"`. An agent stage names a `seat`
   ([D-IG13](../reference/tillr-item-graphs.md#why-its-this-way)). The item-graph reference is right; the spelling is AutoQA's to change.
6. **[tillr-autoqa.md](./tillr-autoqa.md)**, "In short" and its Cost paragraph: "Zero new states,
   DAG primitives, or judge/grant rules inside `tillr` core". This document agrees on all three and
   adds small changes that are not of those kinds ([A1 to A5](#11-now-or-queued-at-once)).
7. **[tillr-distribution.md](./tillr-distribution.md)**, the handoff skill text in [§5](./tillr-distribution.md#5-the-autoqa-handoff-through-the-pack):
   "The recording layer's emitters … are unbuilt", face lists that omit `tillr-view waiting`,
   `tillr-run pause` and `tillr-store recover`, and a step `tillr land enqueue`. The pack exists
   under `yolo-packs/tillr`, run events are emitted **[ran]**, and `tillr land enqueue` answers
   `unknown face "enqueue": want pass or one` **[ran]**. The code is right. Too much of that section
   is stale for a one-line fix, and the status index replaces its role.

[The planning-node design](./tillr-planning-node-model-tiering.md) agrees with item 5: an agent
stage names a seat and only the policy names a model.

---

## 5. The requests, restated

> [!NOTE]
> This section and [Section 6](#6-the-assessment-behind-the-proposal) are the first round, kept
> as it stood on 2026-11-16. "Today", "queued" and "the drain proof" in them are of that date. What
> is true now is in [Section 1](#1-what-tillr-will-change) and
> [Section 7](#7-the-second-round-tillrs-answer).

Requested by the Product QA pipeline design conversation, 2026-11-16, written by another agent with
the owner. Each request said it was "checked at the installed binaries".

1. A store that needs neither git nor the drain, where every view works.
2. Several stores in one workspace, each addressed explicitly, each with its own loop and keeper.
3. Outcomes defined by the kind of item, such as confirmed, rejected, inconclusive, repeat or
   tooling fault, with inconclusive as a first-class outcome.
4. Reopening from any finished state, with a reason.
5. Graph changes as recorded operations that a running step can request: add items, add and remove
   edges, split, fold, and hold a place for work of unknown shape ("fog of war"), each applied
   atomically with its event.
6. Steps that are not agents, with declared inputs and outputs, retried only when an input
   changed.
7. Completion that is not a landing, keeping "judged negative" apart from "could not judge".
8. An uncertain outside effect (a send that lost its session) as a state that blocks a retry.
9. Leases on their own: at-most-once launch, heartbeat, end causes and interrupted-run detection,
   with no worktree, brief template or drain.
10. Dispatch policy they can supply: which items, in what order, how many, and against a deadline.
11. An owner acknowledgment point: "the owner has seen events up to here", append-only.
12. Complete events: each step attempt with its purpose, inputs, actor, time and result.
13. A Go library as the connection point.

Two questions came with them:

- Is an item meant to hold the record, or to point at files that are the record?
- Which requests cut against tillr's design, as opposed to being unbuilt?

Where each went: 1 is [A1](#11-now-or-queued-at-once); 2 is [A3](#11-now-or-queued-at-once) and
[C8](#13-what-tillr-will-not-change); 3 is [C1](#13-what-tillr-will-not-change); 4 is
[A2](#11-now-or-queued-at-once) and [C2](#13-what-tillr-will-not-change); 5 is
[C3](#13-what-tillr-will-not-change); 6 is [B1 and B2](#12-on-the-existing-design-path); 7 is
[B3](#12-on-the-existing-design-path) and [C7](#13-what-tillr-will-not-change); 8 is
[C4](#13-what-tillr-will-not-change); 9 is [B5](#12-on-the-existing-design-path) and
[A4](#11-now-or-queued-at-once); 10 is [B7](#12-on-the-existing-design-path) and
[C5](#13-what-tillr-will-not-change); 11 is [B9](#12-on-the-existing-design-path); 12 is
[B6](#12-on-the-existing-design-path); 13 is [C6](#13-what-tillr-will-not-change).

---

## 6. The assessment behind the proposal

This is the first round's assessment, kept as the evidence for Section 1.

### 6.1 The judgment

**Should tillr become separable parts?** Some of it already is, and that part should be made
dependable. tillr's designed seam is processes and records. Each face reads a record and writes a
record, and no utility calls another inside one process
([board-utilities.md §5.9](./board-utilities.md#59-the-composed-pipeline)) **[read]**. The store,
the item face and `tillr run exec` already work without git or the drain **[ran]**. Making those
pieces reliable on their own also helps tillr: Product QA, a fresh machine and tests all use them that
way.

**What should not happen is tillr turning into a general workflow engine.** The designs draw that
line on purpose and enforce it with loader checks:

- no cycles, timers, or wiring a model wrote;
- no node that changes anything outside its worktree;
- one item, one outcome.

These come from [what a workflow node may never be](../reference/tillr-item-graphs.md#what-a-workflow-node-may-never-be) and
the retired agent runtime design, section 6 **[read]**. Five of the thirteen
requests cross that line: requests 3, 4, 5, 8 and 13.

**Cost to the current goal.** Most of what Product QA would use is already queued on the drain's own
path: the item-graph blocks, the lifecycle blocks, recovery commands, lanes outside the cap, and
seat shares **[read]**. Building those costs nothing extra as long as they land in their existing
order. The extra cost comes from a second loop per root, an outcome vocabulary, a stable library
and a general graph algebra. Each needs a design round, and some reopen settled rulings
**[inferred]**.

### 6.2 The thirteen requests

| Bucket | Count | Requests |
| :--- | :--- | :--- |
| Works today | 0 | — |
| Small defect | 1 | 1 |
| Unbuilt but consistent with the design | 7 | 2, 6, 7, 9, 10, 11, 12 |
| Cuts against the design | 5 | 3, 4, 5, 8, 13 |

Several requests are split cases, and the table says which part is which.

| # | Status today | Size | What tillr gains |
| :--- | :--- | :--- | :--- |
| 1 | **Small defect.** Two read faces fail on a store the drain never opened **[ran]**; see A1. | Hours | Fresh-machine and Product QA stores stop failing |
| 2 | **Unbuilt, partly works.** Every face takes `--store` **[read]**. The loop's other defaults, its lanes and its records sit under its root ([main.go](../../tools/tillr-drain/main.go), [looprecord.go](../../tools/tillr-drain/drain/looprecord.go)). The keeper finds a loop by process name and working directory, and locks per root **[read]**. Run ids are `run-<item>-a<N>`, so two stores under one root could collide on record directories **[inferred]**. | Days | Little for this repository |
| 3 | **Cuts against** the closed seven-state model (D-IP1). The four verdicts the request names belong to criteria, not items ([verdict.go](../../tools/tillr-item/item/verdict.go)) **[read]**. | Design round | A countable "why it ended" |
| 4 | **Cuts against** an accepted ruling: `landed` and `resolved-elsewhere` are terminal. The defect the request found is real, and worse than it says **[ran]**; see A2. `reopen` refuses a landed item and reopens a closed one **[ran]**. | Defect: already queued | Finished items stop quietly re-entering the queue |
| 5 | **Cuts against in its general form; the bounded parts are designed.** Replacing a later node that has not started is a queued proposal block, under `D-IG3`. The drain writes child items from a manifest after landing (D-IP6). Folding is `resolved-elsewhere` with an item pointer **[read]**. None of the three is built **[ran]**. | Bounded: queued | Already wanted |
| 6 | **Unbuilt but consistent.** Command nodes and outputs bound to their producer by sha256 are queued; see B1 and B2. Re-admitting an item only when a stored basis changed exists ([lifecycle §4.2](./tillr-judge-and-lifecycle.md#42-sending-a-failed-item-again-under-its-own-id-d-jl6-d-jl8)). Skipping a node whose input digests did not change is not designed **[read]**. | Queued | Cheaper deterministic steps |
| 7 | **Unbuilt but consistent, in the narrow form.** A `no_commit` exit runs neither gate nor lander and still settles the item; see B3. The two exit codes exist: 1 is negative, 4 is "an outside dependency prevented a decision" **[read]**. | Narrow form: queued | Investigation-only items stop being held as "did nothing" |
| 8 | **Cuts against, inside a graph** ([what a workflow node may never be](../reference/tillr-item-graphs.md#what-a-workflow-node-may-never-be)) **[read]**. Outside a graph it works today as a `blocked` hold **[ran]**. That is how tillr settles its own uncertain landing: by reading the trunk ([already_on_trunk.go](../../tools/tillr-drain/drain/already_on_trunk.go)) **[read]**. | Hours, as a convention | Little |
| 9 | **Unbuilt but consistent; mostly there.** See B5. `tillr run exec` needs a lane directory and an adapter directory **[ran]**. | Days | A run face that can say "dead" without a loop |
| 10 | **Unbuilt but consistent, except deadlines.** See B7. A second dispatcher cannot share a store with the drain **[ran]**. | Hooks: design round | Shares and caps are already wanted |
| 11 | **Unbuilt but consistent** with visibility first. See B9. | Hours | "New since you last looked" here too |
| 12 | **Partly works.** The "emitters unbuilt" claim traces to [tillr-distribution.md](./tillr-distribution.md) and is out of date **[ran]**. A run event carries no purpose or inputs **[read]**. | Days | Fuller reconstruction in the observatory |
| 13 | **Cuts against** the process-and-record seam, and against the rate of change. The packages can be imported **[ran]**. | Weeks, then ongoing | None now |

### 6.3 The requests that cut against the design

#### 6.3.1 Outcomes per kind, and completion that is not a landing

**For the request.** `landed` means a gated commit moved the trunk, and a finding never makes one.
Product QA needs "inconclusive" kept apart from both "failed step" and "rejected". If every ending must
be one of seven states, the real outcome lives in free-text reasons that no view can count.

**For the design.** The seven states are not started, in queue, working, blocked, landed, resolved
elsewhere and closed. The set is closed on purpose: one projector keeps the board, the counts and
the planner in agreement ([D-IP1](./tillr-idea-pipeline.md#61-settled-decisions)) **[read]**. The
projector is the function that turns stored state into displayed state. Each new state needs new
planner and waiting-face rules and a new landing precondition. Most finding outcomes already fit
**[inferred]**:

| Finding outcome | Existing form |
| :--- | :--- |
| Repeat of another item | `resolved-elsewhere` with an `item:` pointer (queued) |
| Rejected | `closed` with a reason, recorded with who closed it and from which state **[ran]** |
| Tooling fault | An attempt hold such as `run-infra-exhausted`, not an ending ([lifecycle §4.1](./tillr-judge-and-lifecycle.md#41-attempt-outcomes-are-not-criterion-verdicts-d-jl5)) |
| Inconclusive | `blocked` with a waiter and a reason, or `closed` with a reason |
| Confirmed | `landed` on a `no_commit` receipt once that exit is built, or `resolved-elsewhere` with a `doc:` pointer to the report |

**Is landing a special case of "an artifact is recorded and judged"?** Logically, yes. A landing
is a tree that was judged by the gate and recorded as a landing row, and the `no_commit` exit
already accepts an ending with no trunk move **[read]**. Generalizing fully would change three
things **[inferred]**:

- **The gate and the lander** keep their git meaning and become one kind of acceptor among
  several. The landing principles bind only the lander
  ([landing-principles.md, Scope](./landing-principles.md#scope-which-lander-a-principle-binds)),
  so they would mostly survive.
- **Verdicts** would bind to an artifact digest instead of a head. The payload already has
  `evidence` and `evaluated_head` fields **[read]**.
- **The planner's assumptions** would have to go: every item has a fence, and every lane is a git
  worktree reset to the trunk. This is the expensive part, and it is the code the drain proof
  depends on.

The narrow choice, whether to record an outcome at all, was asked as [OQ-FQ2](#OQ-FQ2), and the
general one as [OQ-FQ5](#OQ-FQ5); [the ledger](#decision-ledger) says how each closed.

#### 6.3.2 Reopening a finished item

The accepted lifecycle ruling makes `landed` and `resolved-elsewhere` final so that a finished item
never quietly returns to the queue **[read]**. The reason was this repository's `-v2` clones and
the hand-written state moves the lifecycle design records. That ruling still holds. A finding that
comes back after it was confirmed is a new item that names the old one. Reopening a closed item
already works **[ran]**. The raw-state hole was the queued terminal-state block's to close, and
[OQ-FQ6](#OQ-FQ6) asked whether part of it went first; it did.

#### 6.3.3 Steps that change the graph

**What the design already allows.** A node is fixed once its run is recorded. A planning node may
propose replacing one later node that has not started with a tracked workflow, within that
workflow's stated bounds. The drain applies the proposal in one transaction against the graph it
read, and keeps the old definition in an append-only revisions table. A refused proposal is
recorded with its reason (`D-IG3`) **[read]**. That meets the request's own condition, "atomically
with its event, one writer", for one bounded operation.

**Where the two words are written down.** Both are in this repository's documents:

- "In fog" is Guild's word for a node that has not started
  ([The words](../reference/tillr-item-graphs.md#the-words),
  [proposals and graph revisions](../reference/tillr-item-graphs.md#proposals-and-graph-revisions)).
- Guild is described in the retired agent runtime design, section 14
  as "a Go implementation of dynamic graph transformation" from another of the owner's projects
  **[read]**.

The Guild archive those documents cite is no longer in this checkout **[ran]**.

**Why the general form cuts against the design.** Two rulings stand in its way:

1. **One item, one outcome (`D-IG2`).** Steps inside an item are nodes, and separate deliverables
   are separate items. One item per finding is the second kind. So "the findings of a run that has
   not ended" is a manifest of child items applied after the producer settles (D-IP6, unbuilt),
   not a node placeholder **[read]**.
2. **No model writes wiring (`D-IG5`, `D-IG9`).** A step that edits edges freely is the
   "workflow-engine creep" that the retired agent runtime design, section 14
   names as the first warning sign **[read]**.

**For the request.** The findings graph really does change shape as it runs. Four operations are
already designed: replacement, the child manifest, fold as a pointer, and split as a parent
resolved into its children. Product QA should show a case those four cannot express before anything
wider is considered.

#### 6.3.4 Several stores in one workspace

These parts assume one store per root **[read]**:

- the keeper's discovery and lock;
- the loop's records and lanes under the root;
- the default store path.

There is one more. The queued unshared-item hold would hold every item that no tracked document
declares, which is every imported finding ([B8](#12-on-the-existing-design-path)). The land lock is
per git repository, so it does not matter to a store that never lands.

**Does "state in documents" apply to a findings store?** The rule exists so that deleting the store
and running sync rebuilds the same queue ([tillr-convergence.md §3](./tillr-convergence.md#3-the-target)).
Product QA leans toward keeping bug records and evidence as files reviewed in git. That meets the same
invariant if each finding's record file can be re-imported **[inferred]**. So the principle
applies, but the mechanism does not, because the source documents are finding files and not
roadmap blocks. That argues for keeping the importer in Product QA, not for a second sync dialect.

#### 6.3.5 A Go library as the connection point

Every package can be imported today. The designed seam, though, is the CLI and its JSON records,
with exit codes as the contract, and that seam is what the conformance tests pin. A second
consumer that links the Go types would freeze names that change daily: 167 commits touched
`tools/tillr*` from 2026-11-13 to 2026-11-16, and 37 touched the core store, item and run packages
**[ran]**. Records carry a `record_version` and can be versioned. Go APIs here cannot be versioned
cheaply. The choice was asked as [OQ-FQ4](#OQ-FQ4) and closed when Product QA withdrew the request.

### 6.4 The two questions

**Does an item hold the record, or point at files?** It does both, and today the split is
consistent **[read]**:

- **The store holds what a decision was made against.** Criterion text is stored and frozen, and
  a verdict names its sha256. Verdicts, decisions, states and actors are rows and events.
- **Files hold the bytes.** The item's prose is a document section, named by `body_ref`. Run
  reports, logs and briefs are files the run record points to. Node outputs will be files recorded
  with name, type, sha256 and location. An artifact is "a digest plus a locator"
  (the retired agent runtime design, section 14).

Product QA's lean toward pointing is right. It needs one more rule: whatever a verdict relied on is
bound by its digest, so an edited file cannot silently change what was judged.

**Which requests cut against the design?** Requests 3, 4, 5 (in its general form), 8 and 13, for
the reasons in [Section 6.3](#63-the-requests-that-cut-against-the-design). Requests 2, 6, 7 (in
its narrow form), 9, 10 (apart from deadlines and hooks), 11 and 12 are unbuilt but consistent.
Request 1 is a small defect.

### 6.5 Risks to tillr

- **Scope creep away from the owner's goals.** Each request is reasonable alone. Together they
  redirect the coming weeks from the drain proof to generalizing the item model. The roadmap puts
  loop-stopping defects before any build
  ([roadmap, Ordering basis](../tillr/roadmap.md#ordering-basis)), and none of this work stops the
  loop by staying undone.
- **A second consumer freezing interfaces too early.** Record schemas and exit codes are designed
  to be a contract, and Go packages are not. Once another binary links them, every rename has to
  be negotiated **[inferred]**.
- **A general workflow engine instead of a landing tool.** The item-graph design names this exact
  trap and holds the line with loader checks. The findings graph wants loops, waits on the owner
  inside a graph, outside effects and clocks. Each is refused there for a stated reason. Taking
  them would weaken the one-fence, one-gate, one-landing guarantee the drain proof depends on
  **[read]**.
- **Two dispatchers on one store.** The planner would hold Product QA's items **[ran]**. Keep the
  stores separate.
- **A queued item landing against Product QA before the ruling.** The unshared-item hold was the one
  case; its criteria were narrowed on 2026-11-16 so Product QA's own stores are not held ([B8](#12-on-the-existing-design-path)).

---

## 7. The second round: tillr's answer

Product QA's second-round reply is recorded as received in
[tillr-product-qa-requests.md](./tillr-product-qa-requests.md). On 2026-11-17 the owner said to build
"as much of the new tillr as possible", so that Product QA can try the real thing and comment on it
in the next round, rather than read a written answer. tillr's answer is therefore the items of
[the build plan](./tillr-definitions-plan.md), all of which have landed, and this section is the
map from each request to the item that built it or the unfiled work that will. The middle column
of each table is what was re-run on 2026-11-17 on binaries built from `2a35749f6`; the last is the
state on 2026-11-18, read from the store and re-run where the row says so. The commands are in
[Evidence](#evidence). Nothing is declined ([`D-FQ5`](#decision-ledger)): by the owner's decision
rule a tension between Git landing and another use puts the choice in the definition, so the answer
is the extension point.

### 7.1 The fourteen requests

| Request | Answer | What held on 2026-11-17 | State on 2026-11-18 |
| :--- | :--- | :--- | :--- |
| R2-1 Every table created at `store init` | Accept | `drain why` and `view waiting` exited 3 on `items`, `run lanes` and `run status` on `lanes` and `runs` | **Built** (`item-def-tables-at-init`); re-run, all exit 0 |
| R2-2 A store declared not to land | Accept: the first slice of the split | The loop exited 4 at `git rev-parse main` off a repository and 2 below a repository's top level, and `drain why` held `no-fence` | **Built** (`item-def-store-definition`, `item-def-planner-lifecycle`, `item-def-loop-start-without-git`, `item-def-stage-plain-directory`, `item-def-finish-on-hook`), and shown end to end by `item-def-recorded-verdict-example` |
| R2-3 Finished states keep their reasons, and leaving one is an event | Accept | `--reason` on a move to `resolved-elsewhere` was accepted and stored nowhere | **Built** (`item-def-finished-state-receipts`; leaving a finished state where a definition allows it, `item-def-item-lifecycle`) |
| R2-4 The raw `state` and `apply` paths closed | Accept, at once: a defect in tillr's own use | `apply` rewrote a landed item to `in-queue` with a new title | **Built for moves out** (`item-def-finished-states-final`); re-run, rejected. Writing `landed` with an open criterion still succeeds; the queued `item-jl-terminal-state-receipts` closes it |
| R2-5 Command steps, with the item id passed to the adapter | Accept | The loader refused a stage that is not an agent, and no item id reached the adapter | **Built** (`item-ig-command-node`, `item-def-run-item-env`, `item-def-command-node-plain-directory`) |
| R2-6 Runs, verdict reasons, decisions, close and reopen on the item page | Accept | Attempts came only from landing candidates, and no state move was shown | **Built** (`item-def-item-page-history`) |
| R2-7 A detail panel resolved from `body_ref` by a hook | Accept as an extension point | `body_ref` round-tripped as an opaque string | **Half built:** the `detail` hook is in the contract (`item-def-hook-contract`); no page draws it. The panel is the next tranche's item 2, unfiled |
| R2-8 Outcomes per kind, countable | Accept | `inconclusive` was refused as a state and as a verdict, exit 2 | **Half built:** finished states per definition (`item-def-item-lifecycle`), used by the worked example. Counts in the views are the next tranche's item 1, unfiled |
| R2-9 A core that does not preclude a definition's graph changes | Accept as the owner's direction | Not built: one replacement operation was queued | Unbuilt; the next tranche's item 3, unfiled |
| R2-10 No fixed graph size limits | Accept as the owner's direction | As design: `D-IG4` and four criteria carried caps | Unbuilt; the next tranche's item 4, unfiled (see the note below the table) |
| R2-11 No clock whose only consequence is a kill | Accept as the owner's direction | `D-GR13`'s ceiling and the lease's 300-second grace | **Partly built:** default run walls are removed (`D-RW1`; `timeout_ms: 0` holds a `NULL`-expiry lease in `tools/tillr-run/run/lease.go`) and `D-GR13`'s kill ceiling is replaced by the stall watchdog (`D-GR15` in `tools/tillr-gate/watchdog.go`, `tools/fork-check.py`). `LeaseGraceMS` (300 s) remains for explicit `timeout_ms > 0` runs, with no plan to remove it |
| R2-12 An uncertain outward effect as a state that blocks a retry | Accept | Outside a graph, `blocked` with waiter `owner` worked; inside a graph nothing existed | Unbuilt; the next tranche's item 5, unfiled |
| R2-13 Time and tokens per step attempt, with their source | Accept | An agent run's record carried its times and `usage`; a command step's record had no source | Unbuilt; the next tranche's item 6, unfiled |
| R2-14 An owner acknowledgment point | Accept | Nothing built; `tillr view events --since` with a cursor is the meanwhile | Unbuilt; the next tranche's item 7, unfiled |

**Criteria that carry a graph cap**, which the next tranche's item 4 must amend: the workflow
check's c2 ("more than 100 stages", `item-ig-workflow-check`), the nested-workflow block's c3 and c4 (`item-ig-stage-names-workflow`), and the
scouted-design block's c1 ("at most 8 scout prompts", `item-ig-scouted-design-workflow`).

### 7.2 The stable-surface corrections

| Their correction | What held on 2026-11-17 | State on 2026-11-18 |
| :--- | :--- | :--- |
| `--format` is listed and no face defines it | Exit 2, `flag provided but not defined` | The contract no longer lists it, and no face needs it |
| No face lists items | True | **Built** (`item-def-list-items`): `tillr item list` |
| `item apply` drops an unknown field | True; and `apply` refused any `producer.system` but `tillr`, undocumented | **Built** (`item-def-apply-unknown-fields`); the `producer.system` rule is in the contract and was not re-run |
| `run exec` overwrites a scratch-directory variable, undocumented | True | Documented in the contract; `item-def-run-item-env` landed and its effect on that variable was not re-read |
| The waiting class disagrees between the command and the served answer | "Waits on work" against "needs you" | Unbuilt; the next tranche's item 8, unfiled |
| A run requested but not started by the drain is held `run-link-missing` | Read, not re-run | Unbuilt; the next tranche's item 9, unfiled |
| An agent step's brief has nine sections shaped for Git | True | **Built** (`item-def-stage-plain-directory`) |

### 7.3 Their four questions

1. **A person as a step.** Two shapes, without choosing, since the owner said "I don't know that it
   is one answer". A hold that names him between items is built: `blocked` with waiter `owner`, the
   `question-open` hold and `conflict-owner`. A node whose result is his recorded answer is not:
   the nearest parts are a human verdict under a grant, the decision ledger, and the queued
   [owner-criterion hold](./tillr-judge-and-lifecycle.md#hold-an-item-that-still-owes-an-owners-ruling).
   The three writes their trial joined by text can be joined today only by naming the decision's
   id in the verdict's evidence, which nothing checks.
2. **The concern behind the caps.** `D-IG4` gives one reason, "Expansion would not end, and a graph
   would become a state machine": runaway expansion, and the spend that follows it. Expansion is
   already finite another way, because a cycle among workflow files is refused. The guard proposed
   in its place is the graph's size shown before its first node runs, and a budget a definition
   may set that leads to a named next step for the owner, never a silent refusal.
3. **Leases for `run exec` today.** Both, by the request. With `timeout_ms` above 0 the run is
   killed at its wall and its lane is leased to the start plus the wall plus 300 seconds
   (`LeaseGraceMS` in [lease.go](../../tools/tillr-run/run/lease.go)), after which a reaper may free
   it as `lease-expired`. With `timeout_ms` 0 the lease has no expiry, and only `tillr run release`
   frees the lane. The drain's runs take the second rule since `D-RW1`; their trial's runs took the
   first, because their policy named walls of 120 and 600 seconds.
4. **The item page for an item with no workflow.** Yes. The item-page block's c3 draws such an item
   as one node from its run, and the page lists every run, whether or not it made a landing
   candidate (`item-def-item-page-history`, built).

---

## Evidence

Run on 2026-11-16 with the binaries `just tillr-build` produces, the environment's `BOARD_DB` and
`XDG_STATE_HOME` cleared, and every experimental store a new file outside this repository. `$S`
names that store. The last two lines read the live store through read-only faces.

```text
tillr-store init --store $S                                   exit 0; tables: events, meta, schema_migrations
tillr-item apply --store $S --input <one item.record> --now … exit 0
tillr view status|events|passes --store $S                    exit 0
tillr view waiting --store $S --root <dir>                    exit 3: "no such table: lanes"
tillr drain why --store $S --root <dir>                       exit 3: "no such table: lanes"
tillr item state --item X --state landed  (criterion open)    exit 0, state landed
tillr item reopen --item X --reason … --by owner              exit 1, rule item-not-closed
tillr item state --item X --state in-queue                    exit 0 (out of landed)
tillr item state --item X --state resolved-elsewhere          exit 0, no pointer asked for
tillr item close …; tillr item reopen …                       exit 0 both; reopened to in-queue
tillr run exec --store $S --input <run.request> \
  --lane-root <dir> --backends-dir <stub adapters>            exit 0; end_cause normal-exit;
                                                              events run.allocate, run.usage, run.finish
  (absolute working_dir)                                      refused: must be relative to the lane
  (no adapter directory)                                      exit 2: reading adapter pause-kind
tillr drain why  (after that run; second item has no fence)   held run-link-missing; held no-fence
ls <the Guild archive directory the item-graph design cites>  no such directory
fd -t d internal tools/ (tillr modules)                       nothing
fd -i changelog tools docs                                    nothing
tillr land enqueue --help                                     unknown face "enqueue": want pass or one
git log --oneline --since=2026-11-13 -- 'tools/tillr*' | wc -l          167
git log --oneline --since=2026-11-13 -- tools/tillr \
  tools/tillr-item/item tools/tillr-run/run | wc -l                     37
tillr drain why (live store, backend-policy.bedrock.json)     command-node, node-outputs, seat-shares,
                                                              unshared-item hold: queued, waiting for a lane;
                                                              exit-condition: blocked on node-outputs;
                                                              terminal-state block: blocked on owner-criterion-hold
tillr-item sync --dry-run (live store)                        created 0, unchanged 81, updated 0
```

**Round two**, run on 2026-11-17 with binaries built from `2a35749f6` by `go build` in each
module, on throwaway stores outside this repository; `$S` names the store and `$R` a root with Git
discovery stopped above it. The last lines read this repository's store through read-only faces.

```text
tillr-store init --store $S                                   exit 0; tables: events, meta, schema_migrations
tillr-drain why / tillr-view waiting --store $S               exit 3: "no such table: items"
tillr-run lanes / tillr-run status --store $S                 exit 3: "no such table: lanes" / "runs"
tillr-item apply (producer.system "product-qa")                 exit 2: record from another system
tillr-item apply (payload with "kind": "finding")             exit 0; show prints no kind
tillr-item state --state resolved-elsewhere --reason …        exit 0; no reason stored
tillr-item reopen / close (resolved-elsewhere)                exit 1: item-not-closed / close-state-invalid
tillr-item state --state in-queue (from resolved-elsewhere)   exit 0
tillr-item state --state landed (no landing)                  exit 0; repeated: exit 0, no new event
tillr-item apply (in-queue, new title, item landed)           exit 0; title and state rewritten
tillr-item state --state inconclusive                         exit 2: not a state
tillr-drain loop --root $R --exit-when-drained …              exit 4: git rev-parse main: not a git repository
  (root inside a repository but not its top level)            exit 2: tillr-item sync refuses the root
tillr-drain why --store $S (two items, no paths)              held no-fence, no-fence
tillr-item state --state blocked --waiter owner --reason …    tillr-view waiting: "waits on work"
tillr-view serve: /api/v1/waiting for the same item           class "needs you"
tillr-view serve: /api/v1/items/<id>                          keys attempts, verdicts, decisions, … ; no state moves
tillr-view serve: /                                           "this binary carries no built front end …"
tillr-view status --format json                               exit 2: flag provided but not defined: -format
git log --oneline bdc40d858..HEAD -- tools                    6 commits; none in item, run, store or view faces
tillr-item show --item <each reconciled item> (this store)    terminal-state block, command node, node outputs,
                                                              item page: in-queue, criteria unjudged
tillr drain why (this store)                                  command node, node outputs, claim class,
                                                              unshared-item block: queued, waiting for a lane
```

**Re-checked on 2026-11-18** at `068774045`, with the installed binaries, on a throwaway store
outside this repository; the first line reads this repository's store through a read-only face.

```text
tillr view status (this repository's store)                   280 items; every item-def-* item landed, and
                                                              command node, node outputs, exit condition,
                                                              evaluator, seat shares, express lanes, spend
                                                              ceiling, unshared-item hold: landed;
                                                              terminal-state block: in-queue
tillr store init --store $S; tillr view waiting --store $S    "definition: git-landing ... held []", exit 0
tillr drain why / tillr run lanes --store $S                  exit 0
tillr item state --state landed (criterion open, no landing)  exit 0, state landed
tillr item state --state in-queue (on that landed item)       item.rejection: raw writes cannot change a
                                                              finished item
tillr item state --state resolved-elsewhere (same item)       item.rejection, the same
tillr item state --state resolved-elsewhere (fresh item,
  no pointer)                                                 exit 0
```

**Not determined:**

- whether what landed for command nodes, node outputs and the exit condition carries the
  adjustments [Section 1.2](#12-on-the-existing-design-path) proposed;
- whether `tillr land enqueue` and the `producer.system` rule still behave as on 2026-11-17;
- whether the record sink's `run-link-missing` comes from the run id Product QA used or from the
  sink path itself, since the sink was not re-run;
- whether the older installed binaries behave differently on these checks, since only the current
  build was run;
- whether the modules are published anywhere, since no network check was made;
- whether two drain loops with different roots can run on one machine, since no loop was started
  next to the live one;
- where the Product QA pipeline's code and requests live outside this repository;
- what the Guild material contained.

---

## Open Questions

Two questions, both blocked. Neither asks anything of the owner until what it waits on exists.

7. 🔒 **OQ-FQ7: Should work that finishes without a commit be able to file child items inside tillr?**

   A Product QA investigation finds that one reported finding is really two bugs. Its work never lands
   a commit, and tillr files child items only after a landing, so tillr cannot file the two bugs
   from that work. Product QA files them itself today with `tillr item apply`, and that works.

   - **A — Yes.** tillr files the children when the work settles, the same single-writer moment a
     landing is.
   - **B — No.** The consumer's own dispatcher files them, as Product QA does now.

   <!-- vantage: question id=OQ-FQ7 blocked-on="the owner taking the AutoQA design out of the Icebox" -->

   Waits on a consumer that needs tillr, and not its own dispatcher, to file the children. There is
   none: Product QA files them itself, and tillr's own AutoQA, which has the same need, is parked by
   the owner until he takes up [its first question](./tillr-autoqa.md#OQ-AQ2). When one appears, the question belongs to
   [the idea pipeline](./tillr-idea-pipeline.md), whose ruling on child items it would amend, and
   it is asked there against the graph design's rule that a node may add nodes.

   **Answer:**

   > _(empty — fill in when decided)_

8. 🔒 **OQ-FQ8: How does a bug that tillr's own AutoQA confirms get into tillr's queue?**

   tillr's own AutoQA design files the bugs it confirms into the store of the system it tests,
   which for tillr is tillr's own queue. It is not a Product QA question: Product QA files only into
   stores of its own.

   - **A — As a document block** that AutoQA writes and sync imports.
   - **B — Through `tillr item apply`, with a store setting** that exempts such items from needing
     a block.
   - **C — Through `tillr item apply` as today,** so the bug arrives without a block and triage
     writes one.

   <!-- vantage: question id=OQ-FQ8 blocked-on="the owner taking the AutoQA design out of the Icebox" -->

   Waits on two things. The owner parked the AutoQA design on 2026-11-17 ("I don't want to design
   this yet"), and no AutoQA code exists. And option A depends on how work written in a document
   becomes an item, which the owner began reviewing on 2026-11-18. When AutoQA is picked up, this
   question moves to [tillr-autoqa.md](./tillr-autoqa.md).

   **Answer:**

   > _(empty — fill in when decided)_

---

## Decision Ledger

Three kinds of row, kept apart on purpose.

Until 2026-11-18 this document's questions carried a prefix that
seat-profiles.md had used first, for fifteen closed questions
that code, tests and pack data cite. This document's eight were renumbered under a prefix of their
own, keeping each number:

```text
OQ-SP1 → OQ-FQ1    OQ-SP2 → OQ-FQ2    OQ-SP3 → OQ-FQ3    OQ-SP4 → OQ-FQ4
OQ-SP5 → OQ-FQ5    OQ-SP6 → OQ-FQ6    OQ-SP7 → OQ-FQ7    OQ-SP8 → OQ-FQ8
```

### The owner's rulings

Each row quotes words the owner said. This is the only ruling in this document.

| ID | Ruling | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-FQ1 | tillr answers Product QA by building, not by a written proposal and a further round of talk. The owner: "file item(s) to build as much of the new tillr as possible so autoqa can play with the real thing and comment for real the next round. rather than your responses right now." By "autoqa" he means Product QA. | 2026-11-17 | [Section 7](#7-the-second-round-tillrs-answer) | ✅ |

### The designer's decisions, awaiting the owner's review

Each row is a decision the designer made from the evidence named. None is the owner's ruling, and
the owner may overturn any of them.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-FQ3 | "Are P1 to P4 filed now, and P6 after the drain proof?" Closed by the designer's `D-FQ1`, not by the owner. | 2026-11-18 | [Section 3](#3-proposed-work) | — |
| `D-FQ1` | P2, P3, P4 and P6 need no ruling and are ready to file, P6 last. **Evidence:** P1 and P5 are already built; the three small ones are hours each; and the reason for waiting, a proof of the drain on a quiet queue, is gone, since the drain ran twenty lanes and landed the eighteen items of this very work. **Rejected:** holding them for the owner, who decides direction and leaves item filing to the engineer. The owner may overturn this. | 2026-11-18 | [Section 3](#3-proposed-work) | — |
| OQ-FQ6 | "Should the raw state face stop moving items out of a finished state before the rest of the terminal-state block lands?" Closed by the designer's `D-FQ2`, not by the owner. | 2026-11-17 | [Section 1.1](#11-now-or-queued-at-once) | ✅ |
| `D-FQ2` | The move out of a finished state was closed ahead of the rest, and widened to the `apply` face, where Product QA showed the same hole. **Evidence:** it is the move that puts finished work back in the queue, and it waited on nothing. **Rejected:** leaving it behind two other lifecycle items; and an operator-only flag for writing `landed`, an authorization rule no design has. The owner may overturn this. | 2026-11-17 | [Section 1.1](#11-now-or-queued-at-once) | ✅ |
| `D-FQ3` | Whether what landed for command nodes, node outputs and the exit condition carries the first round's three adjustments is checked by whoever files P4, and a missing one is follow-up work with the same criterion. **Rejected:** asking the owner to accept adjustments to items that have already landed. The owner may overturn this. | 2026-11-18 | [Section 1.2](#12-on-the-existing-design-path) | — |
| `D-FQ4` | The face stays `tillr item apply`. **Evidence:** it adds or updates, and records which as the event. **Rejected:** `item add`, which misnames an update. The owner asked why it is named so; he may overturn this. | 2026-11-18 | [Section 2.1](#21-the-stable-surface) | ✅ |
| `D-FQ5` | Every one of Product QA's fourteen second-round requests is accepted, and none is declined. **Evidence:** the owner's decision rule, that a tension between Git landing and another use puts the choice in the definition. The owner may overturn this, row by row. | 2026-11-17 | [Section 7.1](#71-the-fourteen-requests) | — |

### Closed without a decision

| ID | What happened | Date | Now lives in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-FQ2 | "Should a finished item record how it ended, in words its kind defines?" Superseded: the owner put lifecycle in the definition, so each definition names its own finished states, and the worked example ends items as confirmed, rejected, inconclusive, repeat or tooling fault. What is left is counting them in the views, which is unfiled work. | 2026-11-18 | [The direction's ledger](./tillr-core-and-git-landing.md#OQ-CG2) | ✅ |
| OQ-FQ4 | "Are tillr's commands and records the only supported connection for another program?" Nothing left to decide: Product QA withdrew its request for a Go library in round two, and commands and records are the seam [board-utilities.md §5.9](./board-utilities.md#59-the-composed-pipeline) already sets. | 2026-11-17 | [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now) | ✅ |
| OQ-FQ5 | "Does tillr hold a design round on an artifact recorded and judged as a second way an item completes?" Superseded by the build: a definition may complete its items on a hook's recorded verdict, with no landing. Anything wider is not asked until a second definition shows a case the hook cannot express. | 2026-11-18 | [Section 1.3](#13-what-tillr-will-not-change) | ✅ |
