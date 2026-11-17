---
title: "What tillr will change for Product QA, what it promises, and what it will not change"
status: in-review
stage: DESIGN
updated: 2026-11-17
tags:
  - tillr
  - design
  - store
  - boundaries
  - product-qa
summary: "tillr's proposal to the Product QA pipeline and its answer to their second round: what changes now, what waits on queued design, which provisional no's are real tensions, the command-and-record contract, and a table mapping each of the fourteen second-round requests, the surface corrections and four questions to the queued item that builds it. The first round's assessment is kept below as evidence."
next: "The drain builds the items of tillr-definitions-plan.md. Product QA's agent runs the worked example when it lands and writes round three against the real thing."
depends-on:
  - tillr-core-and-git-landing.md
  - tillr-product-qa-requests.md
  - tillr-definitions-plan.md
  - tillr-item-graphs.md
  - tillr-judge-and-lifecycle.md
  - tillr-idea-pipeline.md
---

# What tillr will change for Product QA, and what it will not

> **Delivered copy.** This is `docs/design/tillr-separable-parts.md` from this repository at commit `20c3847b83` (2026-11-17), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-separable-parts.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** 2026-11-17. A proposal and tillr's answer to Product QA's second round
([Section 7](#7-the-second-round-tillrs-answer)). The owner said to build on 2026-11-17, and the
answer is queued items in [the build plan](tillr-definitions-plan.md). It
sits under the owner's direction in [tillr-core-and-git-landing.md](tillr-core-and-git-landing.md): tillr is a
general core plus one definition per use, and Git landing is the first definition. This document
lists only what can change now without prejudging that design, and its "no"s are provisional
until the owner rules on them; the second round says which are real tensions for Product QA. The
first round's facts were re-run on 2026-11-16 with the binaries `just tillr-build` produces, on throwaway
stores, and the planner's holds were read from the live store through `tillr drain why`. Commands
and results are in [Evidence](#evidence). Each claim is marked **[ran]**, **[read]** (file and
section) or **[inferred]**.

> **In short.** tillr's store, items, verdicts and leased runs work outside Git, as Product QA's own
> trial found. Every defect they reported still holds on the current build. Each request, surface
> correction and question maps to a queued item in [Section 7](#7-the-second-round-tillrs-answer);
> nothing is declined. One, refusing writes that move an item out of a finished state, is a defect
> in tillr's own use and has no dependency.

**Why it matters.** The owner's priority is to prove the drain by landing the existing queue
unattended, then to run 10 lanes. Measured against that:

- [What changes now](#11-now-or-queued-at-once) is hours of work.
- [What is on the existing design path](#12-on-the-existing-design-path) is already on the drain's
  own path.
- [What tillr will not change now](#13-what-tillr-will-not-change) is the part that would cost
  the proof weeks, and is provisional.

**Start at [Section 7](#7-the-second-round-tillrs-answer)**, the answer to the second round, then
[the build plan](tillr-definitions-plan.md).
[Section 2](#2-the-contract-between-tillr-and-product-qa-for-now) is the contract Product QA's agent
builds against. Sections 1 to 6 are the first round, kept as it was answered.

**Needs your ruling:** nothing holds the build. Please confirm or correct the statements Product QA
reported in your name and the working choices the build rests on
([the direction, Section 1.1](tillr-core-and-git-landing.md#11-the-owners-words-as-reported-by-product-qa)
and [Section 8](tillr-core-and-git-landing.md#8-building-it-the-owners-go-ahead-and-the-working-choices)),
after the next round. [OQ-SP3](#OQ-SP3) decides the small documentation items P2 to P4. The rest
can wait: [OQ-SP1](#OQ-SP1), [OQ-SP2](#OQ-SP2), [OQ-SP4](#OQ-SP4), [OQ-SP5](#OQ-SP5),
[OQ-SP6](#OQ-SP6), [OQ-SP7](#OQ-SP7), [OQ-SP8](#OQ-SP8).

**Reads with:** [tillr-core-and-git-landing.md](tillr-core-and-git-landing.md) (the direction
this proposal sits under), [tillr-definitions-plan.md](tillr-definitions-plan.md) (the queued
items), [tillr-product-qa-requests.md](tillr-product-qa-requests.md) (Product QA's
second-round reply, as received), [tillr-item-graphs.md](tillr-item-graphs.md) (graphs inside one item, and what a node may
never be), [tillr-judge-and-lifecycle.md](tillr-judge-and-lifecycle.md) (item states and the
terminal-state rulings), [tillr-idea-pipeline.md](tillr-idea-pipeline.md) (item kinds and child
items), [board-utilities.md](board-utilities.md) (the process-and-record seam).

<a id="for-the-autoqa-projects-agent"></a>

## For the Product QA project's agent

**Read first, for round three**, in this order:

1. [The tillr design index](../tillr/design-status.md), which the tillr pack ships.
2. [Section 7](#7-the-second-round-tillrs-answer) of this document: each of your requests, whether
   it still holds on the current build, and the item that builds it.
3. [The build plan](tillr-definitions-plan.md): the queued items, in order, and what you can try
   once each lands.
4. [The direction, Section 8](tillr-core-and-git-landing.md#8-building-it-the-owners-go-ahead-and-the-working-choices):
   the working choices the build rests on.
5. Once it lands, the worked example the plan's last item ships, in the pack's copy of the core
   library under `examples/recorded-verdict/`.

**React to:** what you find when you run it. Where the real thing differs from what Section 7 says,
that is a defect in tillr or in this document, and either is worth a line.

**Write the reply** in your own repository and ask the owner to relay it. tillr records each round
as received in [tillr-product-qa-requests.md](tillr-product-qa-requests.md), replacing the last; git
history keeps every round. [Section 2.4](#24-how-requests-and-answers-travel) says how it is
answered.

**Two placeholder words** are used here as in the direction document: a **definition** is the layer
that turns tillr's core into one use, and the **landing definition** is the one for Git landing.
The owner has not named either yet.

---

## The words this document uses

- **Product QA:** a product bug-finding pipeline, with the fuzzer Mangle as its prober, in
  a repository of its own. Its agent and the owner wrote both rounds of requests. Its word for
  itself; its own coined terms are listed in its reply.
- **AutoQA:** not Product QA. [tillr-autoqa.md](tillr-autoqa.md) is tillr's own design for a
  standalone exploratory-testing tool that runs its own tillr store and is aimed first at tillr
  itself. The first round of this document took it for Product QA's design, from the owner calling
  the requests "the AutoQA stuff"; Product QA corrected that in round two
  ([their reply](tillr-product-qa-requests.md)).
- **Consumer:** a program other than tillr's own loop that uses tillr's commands on a store. Here,
  Product QA.
- **Store:** tillr's SQLite database of items, runs and events.
- **Face:** one subcommand of a tillr program, such as `tillr item apply`. **Record:** the JSON
  document a face reads or writes, in the envelope
  [board-utilities.md §5.1](board-utilities.md#51-the-common-contract) defines.
- **Drain:** the loop that plans, runs, gates and lands items. **Keeper:** the program that restarts
  the drain when it exits ([tillr-keep](../../tillr-roll/tillr-keep)).
- **The drain proof:** this document's name for the owner's first goal, the existing queue landed
  through the drain unattended.
- **Lane:** a leased slot with its own directory, where one run executes. **Lease:** the claim on a
  lane, which expires at the run's wall plus a grace.
- **Fence:** the set of paths an item may change.
- **Gate:** the check run on a candidate commit. **Lander:** the program that puts a gated commit
  on the trunk.
- **Workflow, graph and node:** as defined in
  [tillr-item-graphs.md, The Words](tillr-item-graphs.md#the-words). A workflow is a tracked
  template of stages. Running it gives an item a graph, and each node is one agent or one command
  call.
- **Criterion:** one testable condition on an item. **Verdict:** a ruling on one criterion.
- **Hold:** the planner's named reason for not dispatching an item.
- **`no_commit` exit:** a workflow's declaration that its item finishes without a commit, so no
  gate or landing runs ([the exit-condition block](tillr-item-graphs.md#add-an-exit-condition-that-decides-when-an-items-graph-is-done)).
- **Unshared item:** an item in a store that no tracked document declares. Sync is the only intake
  ([tillr-convergence.md §3](tillr-convergence.md#3-the-target)), so such an item cannot be
  rebuilt by deleting the store and syncing.

---

## 1. What tillr will change

Each entry says what changes, why, its size, and where it sits against the drain proof. Each then
says what Product QA may rely on today, what is promised, and what is not promised.

### 1.1 Now, or queued at once

Five changes. Each is hours of work, and none waits on the drain proof. Whether they are filed now
is [OQ-SP3](#OQ-SP3).

**A1. Every read face and the planner answer on a store the drain never opened.**

- **Why:** `tillr view waiting` and `tillr drain why` exit 3 with `no such table: lanes` on a store
  built with `tillr-store init` and `tillr-item apply` **[ran]**. Each utility adds its tables when
  it first opens a store for writing ([registry.go](../../tillr/store/registry.go)), and the
  planner reads the run utility's tables without checking. `Close` already guards a missing table
  ([lifecycle.go](../../tillr-item/item/lifecycle.go)) **[read]**. A fresh machine hits this
  too.
- **Size and place:** hours; at the bottom of the queue if filed now.
- **Today:** `tillr view status`, `events`, `passes`, `lanes` and `running` work on such a store
  **[ran]**. The workaround is to run one write face of each utility first, as tillr's own
  [AutoQA design](tillr-autoqa.md#22-two-stores-and-sandboxed-sut-snapshots) also does.
- **Promised:** once it lands, every read face exits 0 on a store holding only `init` and `apply`.
- **Not promised:** that a write face creates another utility's tables.

**A2. A raw `tillr item state` can no longer move a finished item.**

- **Why:** a raw `tillr item state` moves an item to `landed` with an open criterion and no landing,
  moves it back out to `in-queue`, and moves it to `resolved-elsewhere` with no pointer **[ran]**.
  The queued [terminal-state block](tillr-judge-and-lifecycle.md#prevent-landed-and-resolved-states-without-their-evidence)
  closes all three in its criteria c1 to c3, and it is held behind two other lifecycle items
  **[ran]**. The move out of a finished state (its c3) needs neither of them **[inferred]**.
- **Size and place:** hours; [OQ-SP6](#OQ-SP6) decides whether c3 goes ahead on its own.
- **Today:** the face moves any item to any writable state. A consumer must not treat `landed` as
  final against a raw writer, only against the drain, which records the landing first.
- **Promised:** c1 to c3 of the queued block, when it lands.
- **Not promised:** a check of *who* may write a state; actors are recorded, not authorized.

**A3. The explicit-store route is written down.**

- **Why:** every face takes `--store` **[read]**, and a plain command ran as a leased run with an
  end cause on a store the drain never opened **[ran]**. Nobody outside this repository knows that.
- **Size and place:** an hour; documentation only.
- **Today:** it works, undocumented. A store has one dispatcher: the drain holds an item whose run
  it did not dispatch as `run-link-missing`, and an item with no fence as `no-fence` **[ran]**.
- **Promised:** a recipe whose every command is checked to answer `--help` by
  [tests/tillr-docs.test.py](https://github.com/mschulkind-oss/tillr/blob/main/tests/tillr-docs.test.py).
- **Not promised:** two dispatchers on one store, ever.

**A4. A stock adapter for plain commands.**

- **Why:** `tillr run exec` needs an adapter directory holding `pause-kind` and `extract-report`
  beside the command, and refuses a working directory that is not relative to the lane **[ran]**.
  Every caller writes the same stub today.
- **Size and place:** hours. It is also a step toward the queued command-node block.
- **Today:** works with a hand-written stub.
- **Promised:** one tracked adapter that `--backends-dir` can name, which says `cannot-resume`.
- **Not promised:** resuming a plain command.

**A5. The contract is published, and each change to it is recorded.**

- **Why:** no changelog or breaking-change record exists for tillr (`fd -i changelog tools docs`
  finds nothing) **[ran]**. Each module README has a dated "Known Gaps" callout **[read]**.
- **Size and place:** hours.
- **Today:** [Section 2](#2-the-contract-between-tillr-and-autoqa-for-now) of this document.
- **Promised:** the contract in a tracked reference, a test that every face and record kind it
  names exists, and a dated line in it for every change to the stable surface.
- **Not promised:** that the surface stops changing; only that a change is announced.

### 1.2 On the existing design path

These are already designed, and most are queued. Each lands in queue order, so building it costs
the drain proof nothing extra. For each, the adjustment says what should change so it also serves
Product QA, and which criterion; no item block is edited by this document. The adjustments are
[OQ-SP1](#OQ-SP1)'s to accept.

**B1. Command nodes.** Carried by the queued
[command-node block](tillr-item-graphs.md#add-command-nodes-that-run-a-registered-program),
which is ready and waiting only for a free lane **[ran]**.

- **Adjustment, its c1:** state that the command registry is read from under the drain's `--root`,
  and test it with a root outside this checkout. Product QA's re-check command, `mangle triage
  rejudge`, lives in Product QA's root, not here.
- **Today:** nothing. The loader accepts agent stages only
  ([workflow.go](../../tillr-drain/drain/workflow.go)) **[read]**.
- **Promised:** the block's criteria. **Not promised:** skipping a node whose inputs did not
  change.

**B2. Node outputs.** Carried by the queued
[named-outputs block](tillr-item-graphs.md#add-named-outputs-that-a-later-node-reads-from-its-producer),
also ready **[ran]**.

- **Adjustment, its c4:** each accepted output is also an event in the log, and a read face prints a
  node's outputs as records, so a consumer never reads the drain's table. This is also what
  "complete events" (B6 below) needs for a node's inputs.
- **Today:** nothing.
- **Promised:** name, type, sha256 and location per output. **Not promised:** that the table is
  readable by SQL.

**B3. Finishing with no commit.** Carried by the queued
[exit-condition block](tillr-item-graphs.md#add-an-exit-condition-that-decides-when-an-items-graph-is-done),
held behind B2 **[ran]**.

- **Adjustment, its c3:** name the state such an item ends in and the receipt it ends on: `landed` on
  a `no_commit` receipt, which the lifecycle design already accepts
  ([§3.2](tillr-judge-and-lifecycle.md#32-transition-preconditions-and-attempt-holds-d-jl4)), and
  a read face that prints the receipt. c3 today says the item is settled "by the same step a landed
  item's are", which leaves the end state for the builder to guess.
- **Today:** a run that commits nothing is held as `candidate-no-op` **[read]**.
- **Promised:** an investigation-only item that finishes. **Not promised:** an outcome word
  ([OQ-SP2](#OQ-SP2)), or child items from it ([OQ-SP7](#OQ-SP7)).

**B4. The evaluator slot.** Carried by the queued
[evaluator block](tillr-item-graphs.md#route-a-workflows-evaluator-node-through-the-judges-intake).

- **Adjustment:** none on tillr's side. Product QA's judge is an independent verifying agent, a
  different actor from the investigator, which the no-self-judging rule already enforces. (The
  `role: "evaluator"` spelling this entry used to discuss is in tillr's own AutoQA design, not Product
  QA's; [Section 4](#4-where-the-documents-disagree), item 5.)
- **Promised:** the block's criteria. **Not promised:** a judge that is not the judge step.

**B5 and B6. Leases without the drain, and complete events.**

- **Leases** are carried by [board-utilities.md §5.6](board-utilities.md#56-run-alone), and are
  built: `tillr run exec` claims a lane, leases it until the run's wall plus a 300-second grace,
  writes the end cause and releases it, and `tillr run release` frees a lane by hand **[read]**
  **[ran]**. [§5.6](board-utilities.md#56-run-alone) refuses a heartbeat on purpose, because `run` is one process with nothing resident
  to renew it **[read]**.
- **Events** are partly built: a throwaway store recorded `item.add`, `item.update`,
  `run.allocate`, `run.usage` and `run.finish` **[ran]**. Node inputs and outputs arrive with B2 and
  its adjustment.
- **Today:** leases by wall and end causes; events as listed.
- **Promised:** node output events, if B2's adjustment is accepted.
- **Not promised:** a heartbeat; a "purpose" field on run events beyond the node a run belongs to;
  telling a dead run from a live one without the drain. That last one is unexported drain code
  ([orphan.go](../../tillr-drain/drain/orphan.go)) **[read]**, no design carries it, and
  [P6](#3-proposed-work) proposes it for after the drain proof.

**B7. Dispatch policy.** Carried by three queued items on
[the tillr roadmap](../tillr/roadmap.md): seat shares (ready **[ran]**), lanes outside the cap, and a
spend ceiling. Lane count, backend policy, and pluggable gate, land and verdict programs exist
**[ran]**.

- **Adjustment:** none. A second dispatcher is A3's documented separate store.
- **Not promised:** deadlines, which are [C5](#13-what-tillr-will-not-change).

**B8. Items fed by `item apply` keep working once unshared items are held.** Carried by the queued
[unshared-item hold](../tillr/roadmap.md#hold-an-item-that-has-no-document-block).

- **Adjustment:** made on 2026-11-16 in commit `eed553151`: the hold's c1 now applies only in a
  store that documents feed, and its c5 says a store no sync has written to is never held for
  lacking a block **[read]**. That covers Product QA's own stores. The item has since been rewritten so
  an undeclared item goes to triage instead of a hold, and its c7 leaves alone a store where no
  document block declares any item, as the owner asked ([the direction, Section 2](tillr-core-and-git-landing.md#2-the-workflow-the-landing-definition-wraps-as-it-runs-today)).
- **Today:** apply works on any store, and only the sync report's `unshared_open` count notices.

**B9. An owner acknowledgment point.** No design carries it. `tillr view events --since` takes a
cursor **[ran]**, and mail has an acknowledgment **[read]**; a stored "the owner has seen up to here"
does not exist. It fits visibility first, so it is consistent, but it is not promised before the
drain proof. Product QA keeps its own cursor in its own ledger for now.

### 1.3 What tillr will not change

**Provisional.** Each row says what holds today, and inside the landing definition. By the owner's
decision rule, a choice that suits Git landing and chafes against Product QA belongs to each
definition and not to the core, so several of these may open up for Product QA's own definition;
[the direction, Section 6](tillr-core-and-git-landing.md#6-what-this-changes-in-what-was-said-before)
says which. The arguments from both sides are in
[Section 6.3](#63-the-requests-that-cut-against-the-design).

| | Request | Holds today, and inside the landing definition | Meanwhile, Product QA | Product QA, round two |
| :--- | :--- | :--- | :--- | :--- |
| C1 | New item states per kind | The closed seven-state model ([D-IP1](tillr-idea-pipeline.md#61-settled-decisions)). The owner places lifecycle in the definition, so another definition may name its own | keeps outcomes in its ledger, and uses close reasons and pointers. [OQ-SP2](#OQ-SP2) | Real tension: inconclusive must be countable (R2-8) |
| C2 | Reopening a landed or resolved-elsewhere item | Both are terminal ([lifecycle §3.2](tillr-judge-and-lifecycle.md#32-transition-preconditions-and-attempt-holds-d-jl4)); a lifecycle rule, so the definition's | files a new item that names the old one. Reopening a `closed` item works today **[ran]** | Real tension: the owner, as reported, rules it the definition's policy (R2-3) |
| C3 | A running step that rewrites the graph freely, and placeholders for work of unknown shape | Bounded replacement only, no model-written wiring (`D-IG2`, `D-IG3`, `D-IG5`, `D-IG9`, [§2.2](tillr-item-graphs.md#22-in-fog-refinement-and-the-flattening-projection)) | keeps its graph in its dispatcher, or one tillr item per finding | Real tension, narrowed: free rewriting withdrawn; insertion downstream and appending remain (R2-9) |
| C4 | An uncertain outside effect, such as a lost send, as a state inside a graph | No node changes anything outside its worktree ([§5](tillr-item-graphs.md#5-what-a-node-may-never-be)) | keeps its send step and its uncertain state. Outside a graph, `blocked` with a waiter works today **[ran]** | Real tension: a report send that lost its session (R2-12) |
| C5 | Deadlines and clocks in the plan | No timers ([§5](tillr-item-graphs.md#5-what-a-node-may-never-be), [agent-runtime.md §6](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md#6-the-task-graph)) | orders and times work in its dispatcher | Withdrawn: the time box is the fuzzer run's own business |
| C6 | A Go library as the connection | Processes and records ([board-utilities.md §5.9](board-utilities.md#59-the-composed-pipeline)) | calls the commands in Section 2. [OQ-SP4](#OQ-SP4) | Withdrawn: processes and records are enough |
| C7 | Completion by "an artifact recorded and judged" in general | Not refused; it reaches the planner's assumptions the drain proof rests on | waits for `no_commit`. [OQ-SP5](#OQ-SP5) | Real tension: a recorded, judged artifact (R2-2) |
| C8 | A second loop and keeper beside the first, in one root | The keeper finds a loop by its working directory and locks per root ([tillr-keep](../../tillr-roll/tillr-keep), `find_loop`) | runs a loop with its own root, which gets its own lock and lanes **[inferred]**, not run | Withdrawn: it runs its own store and root |

---

<a id="2-the-contract-between-tillr-and-autoqa-for-now"></a>

## 2. The contract between tillr and Product QA, for now

### 2.1 The stable surface

Product QA may rely on these. A change to any of them is announced as
[Section 2.3](#23-how-a-consumer-learns-of-a-breaking-change) says.

- **Commands:** each program as `tillr-<noun> <face>`, or through the umbrella as
  `tillr <noun> <face>`.
- **Faces a consumer may call** (all **[ran]** for `--help` on 2026-11-16):
  - `tillr-store init`, `check`, `events`;
  - `tillr-item apply`, `show`, `state`, `close`, `reopen`, `verdict`, `grant`, `decision`, `mail`;
  - `tillr-run exec`, `status`, `lanes`, `release`;
  - `tillr-view status`, `events`, `waiting`, `passes`, `lanes`, `running`;
  - `tillr-drain why` and `plan`, which only read; and `tillr-drain once` and `loop` on the
    consumer's own store and root.
- **Flags:** `--store`, `--input`, `--now`, `--root`, `--item`, `--lane-root`, `--backends-dir`,
  `--since`. No face defines `--format`; the first round listed it in error, and Product QA found it
  (corrected 2026-11-17). Store access is always explicit: no standalone face opens the live store
  on its own ([board-utilities.md §5.1](board-utilities.md#51-the-common-contract)).
- **Exit codes** ([exit.go](../../tillr/record/exit.go)): 0 done, 1 a negative decision, 2
  usage, 3 store or record version incompatible, 4 an outside dependency prevented a decision,
  5 an internal error.
- **Record envelope:** `record_version`, `kind`, `id`, `producer`, `created_at`, `subject`,
  `payload` and an optional `idempotency_key`. The version is 1 (`SupportedVersion` in
  [envelope.go](../../tillr/record/envelope.go)), and a reader refuses a version it does not
  know.
- **Record kinds a consumer writes:** `item.record` (to `apply`) and `run.request` (to `exec`).
  `apply` refuses a record whose `producer.system` is not `tillr` ("record from another system"),
  so a consumer stamps that value **[ran]** 2026-11-17.
- **What `tillr run exec` sets in a run's environment:** `TILLR_RUN_ID`, `TILLR_LANE_ID`,
  `TILLR_SESSION_MODE`, `TILLR_RESUME_PROMPT_FILE`, `TILLR_PAUSE_REQUEST`, and the scratch-directory
  variable of this repository's jail that Product QA's reply names, which replaces any inherited value
  with the run's own scratch directory, `<lane dir>/durable/<run id>`
  ([exec.go](../../tillr-run/run/exec.go)) **[read]** 2026-11-17. A consumer whose own tools
  read that variable resets it in its adapter, as Product QA did, until `item-def-run-item-env` lands.
  **Kinds it reads:** `run.record`, the item and verdict records `show` prints, and the event kinds
  in the log.
- **A naming point to settle before this surface is published to another project.** The owner asked
  why the face is `tillr item apply` and not `item add`. It applies an `item.record` read from
  `--input` as either an add or an update, and writes the event `item.add` or `item.update`
  accordingly (`Apply` in [apply.go](../../tillr-item/item/apply.go), `AddKind` and
  `UpdateKind` in [schema.go](../../tillr-item/item/schema.go)) **[read]**. Nothing is renamed
  here.

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
  [tillr-product-qa-requests.md](tillr-product-qa-requests.md), and each later round replaces it
  there.
- **tillr's answers** are this document: [Section 1](#1-what-tillr-will-change) and
  [Section 7](#7-the-second-round-tillrs-answer) for each request's fate,
  [the build plan](tillr-definitions-plan.md) for what is being built,
  [Section 2](#2-the-contract-between-tillr-and-product-qa-for-now) for the contract, and the
  [Open Questions](#open-questions) for what waits on the owner.
- **tillr's designs** reach Product QA's agent through [the design index](../tillr/design-status.md),
  which the tillr pack ships.
- **Product QA's own design** is in its repository. [tillr-autoqa.md](tillr-autoqa.md) is not it:
  that is tillr's own exploratory-testing design.

---

## 3. Proposed work

Proposed, not yet filed. Each is filed as an item only after the owner rules on the question it
names. They are written in prose so that no item enters the queue from this document.

**P1. Let every read face and the planner answer on a store the drain never opened.** *(A1.
Proposed, not yet filed; replaced on 2026-11-17 by the queued `item-def-tables-at-init`.)*
Why: a fresh store fails two faces that Product QA and a new machine both call first.

- A test builds a store with only `tillr-store init` and `tillr-item apply`, and asserts that
  `tillr view waiting`, `tillr drain why` and every other read face exit 0.
- A table a utility has not created reads as empty, the way `Close` already treats it.
- No read face creates a table.

**P2. Document how a program that is not the drain uses tillr on its own store.** *(A3. Proposed,
not yet filed; filed when the owner rules on [OQ-SP3](#OQ-SP3).)*
Why: the route works and nobody outside this repository can find it.

- The tillr operating documentation gains a recipe: an explicit `--store`, a lane root outside the
  checkout, `tillr run exec` for runs, and no drain or keeper on that store unless it is the only
  dispatcher.
- The recipe states that one store has one dispatcher, and names the two holds the drain gives a
  store with a second one.
- [tests/tillr-docs.test.py](https://github.com/mschulkind-oss/tillr/blob/main/tests/tillr-docs.test.py) checks each command in it.

**P3. Ship a stock adapter for plain commands run through `tillr run exec`.** *(A4. Proposed, not
yet filed; filed when the owner rules on [OQ-SP3](#OQ-SP3).)*
Why: every caller of a plain command writes the same stub today.

- A tracked adapter directory holds `pause-kind`, which says `cannot-resume`, and an
  `extract-report` that writes an empty report.
- A test runs `/bin/true` and a command that exits 1 through it, with no git and no drain, and
  asserts both end causes and the three run events.

**P4. Publish tillr's consumer contract and record each change to it.** *(A5. Proposed, not yet
filed; filed when the owner rules on [OQ-SP3](#OQ-SP3) and [OQ-SP4](#OQ-SP4).)*
Why: a consumer cannot tell a change from a defect without a stated contract.

- A tracked reference holds [Section 2](#2-the-contract-between-tillr-and-autoqa-for-now), and this
  document links to it instead of carrying it.
- A test fails when a face it names does not answer `--help`, or a record kind it names is not a
  constant in the code.
- A dated "Changes" list in it gains a line in the same commit as any change to the stable surface.

**P5. Refuse a move out of `landed` or `resolved-elsewhere`, ahead of the rest of the
terminal-state block.** *(A2. Proposed, not yet filed; replaced on 2026-11-17 by
the queued `item-def-finished-states-final`, which widens it to `apply`.)*
Why: the move out is the one that puts finished work back in the queue, and it needs nothing the
block is waiting for.

- The terminal-state block's c3, unchanged, as its own item with nothing in `blocked_by`.
- The block's c3 is removed from it in the same commit, so the rule has one owner.

**P6. Let `tillr run status` say when a run's process is gone.** *(B5. Proposed, not yet filed;
filed after the drain proof, when the owner rules on [OQ-SP3](#OQ-SP3).)*
Why: a consumer with no drain cannot tell a dead run from a slow one until its lease expires.

- `tillr run status` reports a run whose recorded process no longer exists as interrupted, using
  the same rule the drain uses.
- The drain calls the run package for that rule instead of its own copy.
- A test kills a stub run and asserts the status, with no drain running.

**Adjustments to queued items.** *(Proposed, not yet filed; each is made by the owner or a design
seat editing the item's own document when [OQ-SP1](#OQ-SP1) is ruled. If the item lands first, the
adjustment becomes a follow-up item with the same criterion.)*

- **Command nodes, c1:** the registry is read under the drain's `--root`; a test uses a root
  outside this checkout. ([B1](#12-on-the-existing-design-path))
- **Node outputs, c4:** each accepted output is also an event, and a read face prints a node's
  outputs as records. ([B2](#12-on-the-existing-design-path))
- **Exit condition, c3:** an item whose graph finishes with nothing committed ends `landed` on a
  `no_commit` receipt, and a read face prints the receipt. ([B3](#12-on-the-existing-design-path))
- **Unshared-item hold, c1:** the hold applies only to a store whose items come through sync. Done:
  the rewritten item's c1 and c7 say so. ([B8](#12-on-the-existing-design-path))

---

## 4. Where the documents disagree

Read on 2026-11-16 against the tree at the commit this document lands in. Nothing here was edited
in the other documents: each disagreement sits in a document that holds a queued item, or is not a
one-line fix.

**Corrected 2026-11-17.** Items 1 to 6 were written when this document took
[tillr-autoqa.md](tillr-autoqa.md) for Product QA's design. They compare that document, which is
tillr's own design for exploratory testing of tillr, with the code, and they stay true of it. They
say nothing about Product QA, which files into stores of its own and has no `qa-probe` graph, no
`verify_repro` step and no `D-AQ` decisions.

1. **[tillr-autoqa.md §1.1](tillr-autoqa.md#11-what-already-exists-at-head)**, the fresh-store
   row: "**Unchanged by design.** … read-only faces fail with missing-table errors". The code does
   behave that way **[ran]**. This document calls it a defect and proposes [P1](#3-proposed-work);
   if that lands, the row and the bootstrap in
   [§2.2](tillr-autoqa.md#22-two-stores-and-sandboxed-sut-snapshots) (`tillr-drain once` plus a
   `/bin/true` run) become a workaround nobody needs.
2. **[tillr-autoqa.md D-AQ2](tillr-autoqa.md#61-decision-ledger)** crosses into the system under
   test's store, here tillr's own, "solely via `item apply`". Sync is the only intake
   ([tillr-convergence.md §3](tillr-convergence.md#3-the-target)), and the queued unshared-item
   hold would hold every such item. Today `apply` works and only the unshared count notices
   **[ran]**. Which is right is [OQ-SP8](#OQ-SP8).
3. **[tillr-autoqa.md D-AQ3](tillr-autoqa.md#61-decision-ledger)** says its two graphs work
   "immediately on W1 Slice-1 static flattening". Only agent stages load today
   ([workflow.go](../../tillr-drain/drain/workflow.go)) **[read]**; command nodes, outputs,
   the `no_commit` exit and the evaluator are queued. The code is right: that design's graphs wait for
   [B1 to B4](#12-on-the-existing-design-path).
4. **[tillr-autoqa.md §3.1](tillr-autoqa.md#31-test-idea-items-and-the-two-graph-funnel)**:
   `verify_repro` "spawn[s] a child `qa-confirm@v1` item linked via `derived_from`" from a
   `no_commit` graph. `derived_from` edges are not built, and the drain writes child items only
   after `tillr-land pass` succeeds ([D-IP6](tillr-idea-pipeline.md#61-settled-decisions)), which a
   `no_commit` graph never reaches **[read]**. Neither is queued. [OQ-SP7](#OQ-SP7).
5. **[tillr-autoqa.md §3.1](tillr-autoqa.md#31-test-idea-items-and-the-two-graph-funnel)**:
   `triage_judge` names `role: "evaluator"`. An agent stage names a `seat`
   ([D-IG13](tillr-item-graphs.md#71-decision-ledger)). The item-graph design is right; the spelling is that design's to change.
6. **[tillr-autoqa.md](tillr-autoqa.md)**, "In short" and its Cost paragraph: "Zero new states,
   DAG primitives, or judge/grant rules inside `tillr` core". This document agrees on all three and
   adds small changes that are not of those kinds ([A1 to A5](#11-now-or-queued-at-once)).
7. **[tillr-distribution.md](tillr-distribution.md)**, its status line "Pack unbuilt", and the
   handoff skill text in [§5](tillr-distribution.md#5-the-autoqa-handoff-through-the-pack):
   "The recording layer's emitters … are unbuilt", face lists that omit `tillr-view waiting`,
   `tillr-run pause` and `tillr-store recover`, and a step `tillr land enqueue`. The pack exists
   under `yolo-packs/tillr`, run events are emitted **[ran]**, and `tillr land enqueue` answers
   `unknown face "enqueue": want pass or one` **[ran]**. The code is right. Too much of that section
   is stale for a one-line fix, and the status index replaces its role.

The roadmap, [landing-principles.md](landing-principles.md) and the lifecycle design agree with
this proposal: the roadmap puts tillr's own AutoQA build last, which this document leaves as it is;
the landing principles bind only the lander, which a `no_commit` item never reaches. A design of
planning nodes and model tiering was in the working tree but not committed when this was written.
Read as it stood, it agrees: an agent stage names a seat and only the policy names a model, which
is the spelling item 5 asks of tillr's own AutoQA design, and its registered checks are entries of the same command
registry B1 adjusts.

---

## 5. The requests, restated

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
([board-utilities.md §5.9](board-utilities.md#59-the-composed-pipeline)) **[read]**. The store,
the item face and `tillr run exec` already work without git or the drain **[ran]**. Making those
pieces reliable on their own also helps tillr: Product QA, a fresh machine and tests all use them that
way.

**What should not happen is tillr turning into a general workflow engine.** The designs draw that
line on purpose and enforce it with loader checks:

- no cycles, timers, or wiring a model wrote;
- no node that changes anything outside its worktree;
- one item, one outcome.

These come from [tillr-item-graphs.md §5](tillr-item-graphs.md#5-what-a-node-may-never-be) and
[agent-runtime.md §6](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md#6-the-task-graph) **[read]**. Five of the thirteen
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
| 2 | **Unbuilt, partly works.** Every face takes `--store` **[read]**. The loop's other defaults, its lanes and its records sit under its root ([main.go](../../tillr-drain/main.go), [looprecord.go](../../tillr-drain/drain/looprecord.go)). The keeper finds a loop by process name and working directory, and locks per root **[read]**. Run ids are `run-<item>-a<N>`, so two stores under one root could collide on record directories **[inferred]**. | Days | Little for this repository |
| 3 | **Cuts against** the closed seven-state model (D-IP1). The four verdicts the request names belong to criteria, not items ([verdict.go](../../tillr-item/item/verdict.go)) **[read]**. | Design round | A countable "why it ended" |
| 4 | **Cuts against** an accepted ruling: `landed` and `resolved-elsewhere` are terminal. The defect the request found is real, and worse than it says **[ran]**; see A2. `reopen` refuses a landed item and reopens a closed one **[ran]**. | Defect: already queued | Finished items stop quietly re-entering the queue |
| 5 | **Cuts against in its general form; the bounded parts are designed.** Replacing a later node that has not started is a queued proposal block, under `D-IG3`. The drain writes child items from a manifest after landing (D-IP6). Folding is `resolved-elsewhere` with an item pointer **[read]**. None of the three is built **[ran]**. | Bounded: queued | Already wanted |
| 6 | **Unbuilt but consistent.** Command nodes and outputs bound to their producer by sha256 are queued; see B1 and B2. Re-admitting an item only when a stored basis changed exists ([lifecycle §4.2](tillr-judge-and-lifecycle.md#42-inspected-rework-tillr-drain-rework-and-the--v2-cutover-d-jl6)). Skipping a node whose input digests did not change is not designed **[read]**. | Queued | Cheaper deterministic steps |
| 7 | **Unbuilt but consistent, in the narrow form.** A `no_commit` exit runs neither gate nor lander and still settles the item; see B3. The two exit codes exist: 1 is negative, 4 is "an outside dependency prevented a decision" **[read]**. | Narrow form: queued | Investigation-only items stop being held as "did nothing" |
| 8 | **Cuts against, inside a graph** ([§5](tillr-item-graphs.md#5-what-a-node-may-never-be)) **[read]**. Outside a graph it works today as a `blocked` hold **[ran]**. That is how tillr settles its own uncertain landing: by reading the trunk ([already_on_trunk.go](../../tillr-drain/drain/already_on_trunk.go)) **[read]**. | Hours, as a convention | Little |
| 9 | **Unbuilt but consistent; mostly there.** See B5. `tillr run exec` needs a lane directory and an adapter directory **[ran]**. | Days | A run face that can say "dead" without a loop |
| 10 | **Unbuilt but consistent, except deadlines.** See B7. A second dispatcher cannot share a store with the drain **[ran]**. | Hooks: design round | Shares and caps are already wanted |
| 11 | **Unbuilt but consistent** with visibility first. See B9. | Hours | "New since you last looked" here too |
| 12 | **Partly works.** The "emitters unbuilt" claim traces to [tillr-distribution.md](tillr-distribution.md) and is out of date **[ran]**. A run event carries no purpose or inputs **[read]**. | Days | Fuller reconstruction in the observatory |
| 13 | **Cuts against** the process-and-record seam, and against the rate of change. The packages can be imported **[ran]**. | Weeks, then ongoing | None now |

### 6.3 The requests that cut against the design

#### 6.3.1 Outcomes per kind, and completion that is not a landing

**For the request.** `landed` means a gated commit moved the trunk, and a finding never makes one.
Product QA needs "inconclusive" kept apart from both "failed step" and "rejected". If every ending must
be one of seven states, the real outcome lives in free-text reasons that no view can count.

**For the design.** The seven states are not started, in queue, working, blocked, landed, resolved
elsewhere and closed. The set is closed on purpose: one projector keeps the board, the counts and
the planner in agreement ([D-IP1](tillr-idea-pipeline.md#61-settled-decisions)) **[read]**. The
projector is the function that turns stored state into displayed state. Each new state needs new
planner and waiting-face rules and a new landing precondition. Most finding outcomes already fit
**[inferred]**:

| Finding outcome | Existing form |
| :--- | :--- |
| Repeat of another item | `resolved-elsewhere` with an `item:` pointer (queued) |
| Rejected | `closed` with a reason, recorded with who closed it and from which state **[ran]** |
| Tooling fault | An attempt hold such as `run-infra-exhausted`, not an ending ([lifecycle §4.1](tillr-judge-and-lifecycle.md#41-attempt-outcomes-are-not-criterion-verdicts-d-jl5)) |
| Inconclusive | `blocked` with a waiter and a reason, or `closed` with a reason |
| Confirmed | `landed` on a `no_commit` receipt once that exit is built, or `resolved-elsewhere` with a `doc:` pointer to the report |

**Is landing a special case of "an artifact is recorded and judged"?** Logically, yes. A landing
is a tree that was judged by the gate and recorded as a landing row, and the `no_commit` exit
already accepts an ending with no trunk move **[read]**. Generalizing fully would change three
things **[inferred]**:

- **The gate and the lander** keep their git meaning and become one kind of acceptor among
  several. The landing principles bind only the lander
  ([landing-principles.md, Scope](landing-principles.md#scope-which-lander-a-principle-binds)),
  so they would mostly survive.
- **Verdicts** would bind to an artifact digest instead of a head. The payload already has
  `evidence` and `evaluated_head` fields **[read]**.
- **The planner's assumptions** would have to go: every item has a fence, and every lane is a git
  worktree reset to the trunk. This is the expensive part, and it is the code the drain proof
  depends on.

The narrow choice, whether to record an outcome at all, is [OQ-SP2](#OQ-SP2). The general one is
[OQ-SP5](#OQ-SP5).

#### 6.3.2 Reopening a finished item

The accepted lifecycle ruling makes `landed` and `resolved-elsewhere` final so that a finished item
never quietly returns to the queue **[read]**. The reason was this repository's `-v2` clones and
the hand-written state moves the lifecycle design records. That ruling still holds. A finding that
comes back after it was confirmed is a new item that names the old one. Reopening a closed item
already works **[ran]**. The raw-state hole is the queued terminal-state block's to close, and
[OQ-SP6](#OQ-SP6) asks whether part of it goes first.

#### 6.3.3 Steps that change the graph

**What the design already allows.** A node is fixed once its run is recorded. A planning node may
propose replacing one later node that has not started with a tracked workflow, within that
workflow's stated bounds. The drain applies the proposal in one transaction against the graph it
read, and keeps the old definition in an append-only revisions table. A refused proposal is
recorded with its reason (`D-IG3`) **[read]**. That meets the request's own condition, "atomically
with its event, one writer", for one bounded operation.

**Where the two words are written down.** Both are in this repository's documents:

- "In fog" is Guild's word for a node that has not started
  ([The Words](tillr-item-graphs.md#the-words),
  [§2.2](tillr-item-graphs.md#22-in-fog-refinement-and-the-flattening-projection)).
- Guild is described in [agent-runtime.md §14](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md#14-adopting-guilds-graph-algebra)
  as "a Go implementation of dynamic graph transformation" from another of the owner's projects
  **[read]**.

The Guild archive those documents cite is no longer in this checkout **[ran]**.

**Why the general form cuts against the design.** Two rulings stand in its way:

1. **One item, one outcome (`D-IG2`).** Steps inside an item are nodes, and separate deliverables
   are separate items. One item per finding is the second kind. So "the findings of a run that has
   not ended" is a manifest of child items applied after the producer settles (D-IP6, unbuilt),
   not a node placeholder **[read]**.
2. **No model writes wiring (`D-IG5`, `D-IG9`).** A step that edits edges freely is the
   "workflow-engine creep" that [agent-runtime.md §14](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md#14-adopting-guilds-graph-algebra)
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
and running sync rebuilds the same queue ([tillr-convergence.md §3](tillr-convergence.md#3-the-target)).
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
cheaply. The choice is [OQ-SP4](#OQ-SP4).

### 6.4 The two questions

**Does an item hold the record, or point at files?** It does both, and today the split is
consistent **[read]**:

- **The store holds what a decision was made against.** Criterion text is stored and frozen, and
  a verdict names its sha256. Verdicts, decisions, states and actors are rows and events.
- **Files hold the bytes.** The item's prose is a document section, named by `body_ref`. Run
  reports, logs and briefs are files the run record points to. Node outputs will be files recorded
  with name, type, sha256 and location. An artifact is "a digest plus a locator"
  ([agent-runtime.md §14](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md#14-adopting-guilds-graph-algebra)).

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
[tillr-product-qa-requests.md](tillr-product-qa-requests.md). On 2026-11-17 the owner said to build
"as much of the new tillr as possible", so that Product QA can try the real thing and comment on it
in the next round, rather than read a written answer. tillr's answer is therefore the queued items
of [the build plan](tillr-definitions-plan.md), and this section is only the map from each
request to the item that builds it. Every claim was re-run on 2026-11-17 on binaries built from
`2a35749f6` unless the row says otherwise; the commands are in [Evidence](#evidence). Nothing is
declined: by the owner's decision rule a tension between Git landing and another use puts the
choice in the definition, so the answer is the extension point.

### 7.1 The fourteen requests

| Request | Answer | Still holds on the current build? | Built by |
| :--- | :--- | :--- | :--- |
| R2-1 Every table created at `store init` | Accept | Yes: `drain why` and `view waiting` exit 3 on `items`, `run lanes` and `run status` on `lanes` and `runs` | `item-def-tables-at-init` |
| R2-2 A store declared not to land | Accept: the first slice of the split | Yes: the loop exits 4 at `git rev-parse main` off a repository and 2 below a repository's top level, and `drain why` holds `no-fence` | `item-def-store-definition`, `item-def-planner-lifecycle`, `item-def-loop-start-without-git`, `item-def-stage-plain-directory`, `item-def-finish-on-hook`; proven end to end by `item-def-recorded-verdict-example` |
| R2-3 Finished states keep their reasons, and leaving one is an event | Accept | Yes: `--reason` on a move to `resolved-elsewhere` is accepted and stored nowhere | `item-def-finished-state-receipts`; leaving a finished state where a definition allows it, `item-def-item-lifecycle` |
| R2-4 The raw `state` and `apply` paths closed | Accept, at once: a defect in tillr's own use | Yes, all of it: `apply` rewrote a landed item to `in-queue` with a new title | `item-def-finished-states-final`, with no dependency |
| R2-5 Command steps, with the item id passed to the adapter | Accept | Yes: the loader refuses a stage that is not an agent, and no item id reaches the adapter | queued `item-ig-command-node`; `item-def-run-item-env`; `item-def-command-node-plain-directory` |
| R2-6 Runs, verdict reasons, decisions, close and reopen on the item page | Accept | Yes: attempts come only from landing candidates, and no state move is shown | `item-def-item-page-history` |
| R2-7 A detail panel resolved from `body_ref` by a hook | Accept as an extension point | Yes: `body_ref` round-trips as an opaque string | the `detail` hook in `item-def-hook-contract`; the panel in the plan's next tranche, item 2 |
| R2-8 Outcomes per kind, countable | Accept | Yes: `inconclusive` is refused as a state and as a verdict, exit 2 | finished states per definition in `item-def-item-lifecycle`, counted in `item-def-recorded-verdict-example`; the views' counts in the next tranche, item 1 |
| R2-9 A core that does not preclude a definition's graph changes | Accept as the owner's direction | Not built: one replacement operation is queued | the next tranche, item 3, inside the four core rules |
| R2-10 No fixed graph size limits | Accept as the owner's direction | Yes, as design: `D-IG4` and four queued criteria carry caps; none is built | the next tranche, item 4 (see the note below the table) |
| R2-11 No clock whose only consequence is a kill | Accept as the owner's direction | Yes: `D-GR13`'s ceiling and the lease's 300-second grace | filed separately by the owner's direct principle of 2026-11-17; not in this plan |
| R2-12 An uncertain outward effect as a state that blocks a retry | Accept | Outside a graph, `blocked` with waiter `owner` works; inside a graph nothing exists | the next tranche, item 5 |
| R2-13 Time and tokens per step attempt, with their source | Accept | An agent run's record carries its times and `usage`; a command step's record has no source | the next tranche, item 6 |
| R2-14 An owner acknowledgment point | Accept | Nothing is built; `tillr view events --since` with a cursor is the meanwhile | the next tranche, item 7 |

**Queued criteria that carry a graph cap**, which the next tranche's item 4 must amend before they
are dispatched: the workflow check's c2 ("more than 100 stages", `item-ig-workflow-check`, which is
ready now), the nested-workflow block's c3 and c4 (`item-ig-stage-names-workflow`), and the
scouted-design block's c1 ("at most 8 scout prompts", `item-ig-scouted-design-workflow`).

### 7.2 The stable-surface corrections

| Their correction | Still holds? | Answer |
| :--- | :--- | :--- |
| `--format` is listed and no face defines it | Yes: exit 2, `flag provided but not defined` | This document was wrong; [Section 2.1](#21-the-stable-surface) no longer lists it, and no face needs it |
| No face lists items | Yes | `item-def-list-items` |
| `item apply` drops an unknown field | Yes; and `apply` refuses any `producer.system` but `tillr`, undocumented | `item-def-apply-unknown-fields` |
| `run exec` overwrites a scratch-directory variable, undocumented | Yes | `item-def-run-item-env` |
| The waiting class disagrees between the command and the served answer | Yes: "waits on work" against "needs you" | the next tranche, item 8 |
| A run requested but not started by the drain is held `run-link-missing` | Read, not re-run | the next tranche, item 9 |
| An agent step's brief has nine sections shaped for Git | Yes | `item-def-stage-plain-directory` |

### 7.3 Their four questions

1. **A person as a step.** Two shapes, without choosing, since the owner said "I don't know that it
   is one answer". A hold that names him between items is built: `blocked` with waiter `owner`, the
   `question-open` hold and `conflict-owner`. A node whose result is his recorded answer is not:
   the nearest parts are a human verdict under a grant, the decision ledger, and the queued
   [owner-criterion hold](tillr-judge-and-lifecycle.md#hold-an-item-that-still-owes-an-owners-ruling).
   The three writes their trial joined by text can be joined today only by naming the decision's
   id in the verdict's evidence, which nothing checks.
2. **The concern behind the caps.** `D-IG4` gives one reason, "Expansion would not end, and a graph
   would become a state machine": runaway expansion, and the spend that follows it. Expansion is
   already finite another way, because a cycle among workflow files is refused. The guard proposed
   in its place is the graph's size shown before its first node runs, and a budget a definition
   may set that leads to a named next step for the owner, never a silent refusal.
3. **Leases for `run exec` today.** Both, by the request. With `timeout_ms` above 0 the run is
   killed at its wall and its lane is leased to the start plus the wall plus 300 seconds
   (`LeaseGraceMS` in [lease.go](../../tillr-run/run/lease.go)), after which a reaper may free
   it as `lease-expired`. With `timeout_ms` 0 the lease has no expiry, and only `tillr run release`
   frees the lane. The drain's runs take the second rule since `D-RW1`; their trial's runs took the
   first, because their policy named walls of 120 and 600 seconds.
4. **The item page for an item with no workflow.** Yes. The queued item-page block's c3 draws such
   an item as one node from its run, and `item-def-item-page-history` lists every run, whether or
   not it made a landing candidate.

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

**Not determined:**

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

1. 💬 **OQ-SP1: Does tillr answer Product QA with this proposal, shared documentation, and a second round of conversation from both sides?**

   Product QA's agent is about to build on tillr, and it builds on whatever we say this week. The
   proposal is Section 1; the contract is Section 2; the documentation is the status index of
   tillr's designs shipped through the tillr pack. Accepting this also accepts the four adjustments
   to queued items in [Section 3](#3-proposed-work), and names where Product QA writes its next round
   of requests.

   - **A — Yes: this proposal, the index, then a second round.** Product QA builds on the stable
     surface and keeps its dispatcher and graph on its own side; its next requests are relayed by
     the owner, recorded as received in [tillr-product-qa-requests.md](tillr-product-qa-requests.md),
     and answered here.
   - **B — Wait and build on tillr's graphs.** Product QA waits for command nodes, outputs and child
     manifests, which land behind the drain proof.
   - **C — Product QA does not use tillr.** It writes its own store, and none of tillr's lease or event
     work transfers.

   Waits on it: the four adjustments, and Product QA's agent starting work. **Can wait.** The second
   round has happened in the form A describes, so a ruling of A records what was done.

   <!-- vantage: question id=OQ-SP1 leaning="A — the stable parts work without git or the drain today, the adjustments cost nothing the queue is not already paying, and a second round is where Product QA's prototype can show what the proposal missed." -->

   _Leaning:_ A — the stable parts work without git or the drain today, the adjustments cost
   nothing the queue is not already paying, and a second round is where Product QA's prototype can show
   what the proposal missed.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-SP2: Should a finished item record how it ended, in words its kind defines?**

   A finding ends as a repeat, rejected, inconclusive or confirmed, and nothing in tillr can count
   them; the seven states are closed on purpose ([Section 6.3.1](#631-outcomes-per-kind-and-completion-that-is-not-a-landing)).

   - **A — An outcome field on the move to a finished state.** Words defined per kind, stored the
     way a close reason is; the states and the board unchanged; a design round after the drain
     proof.
   - **B — No new field.** Outcomes stay in reasons and pointers, and Product QA counts them in its own
     ledger.
   - **C — New states per kind.** Every view, the planner and the landing rules learn each one.

   Waits on it: nothing now; B3's receipt is shaped either way. **Can wait**: it is R2-8 now, and
   [OQ-CG2](tillr-core-and-git-landing.md#OQ-CG2) reshapes it.

   <!-- vantage: question id=OQ-SP2 leaning="B now and A later — A keeps the closed state set, but it is not worth a design round before the drain proof." -->

   _Leaning:_ B now and A later — A keeps the closed state set, but it is not worth a design round
   before the drain proof.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-SP3: Are P1 to P4 filed now, at the bottom of the queue, and P6 after the drain proof?**

   P1 to P4 are hours each and unblock Product QA; filed now, they join the queue the drain is working
   through. P6 is days and touches the drain's own interrupted-run code.

   - **A — P1 to P4 now, P6 after the drain proof.** The queue grows by four small items.
   - **B — All of them after the drain proof.** The proof's queue stays as it is, and Product QA works
     around the failing faces.
   - **C — P1 only now.** The defect is fixed; the documentation waits.

   Waits on it: P2, P3, P4 and P6; P1 is now the queued `item-def-tables-at-init`. **Can wait**: it
   decides only the documentation items, and nothing in the build plan waits on it.

   <!-- vantage: question id=OQ-SP3 leaning="A — four small items fix a defect fresh stores hit too, and the drain should prove it can take new work; P6 touches code the proof depends on." -->

   _Leaning:_ A — four small items fix a defect fresh stores hit too, and the drain should prove it
   can take new work; P6 touches code the proof depends on.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-SP4: Are tillr's commands and records the only supported connection for another program?**

   Product QA asked to link tillr's Go packages, and withdrew the request in round two; it can today, and the next commit may rename what it
   calls ([Section 6.3.5](#635-a-go-library-as-the-connection-point)).

   - **A — Commands and records only.** A breaking record change gets a new `record_version` and a
     line in the contract; linking Go code is at the linker's own risk, pinned to a commit.
   - **B — Support a Go library.** tillr keeps exported APIs stable, and every refactor checks a
     second consumer.

   Waits on it: P4, and Section 2 becoming the contract. **Can wait**; the request is withdrawn.

   <!-- vantage: question id=OQ-SP4 leaning="A — records are already versioned and pinned by the conformance tests, and a stable Go API would slow a tool that changes daily." -->

   _Leaning:_ A — records are already versioned and pinned by the conformance tests, and a stable
   Go API would slow a tool that changes daily.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-SP5: After the drain proof, does tillr hold a design round on "an artifact recorded and judged" as a second way an item completes?**

   A landing is one case of an artifact judged and recorded, and the `no_commit` exit already ends
   an item with no trunk move; generalizing it reaches the planner's rule that every item has a
   fence and every lane is a worktree ([Section 6.3.1](#631-outcomes-per-kind-and-completion-that-is-not-a-landing)).

   - **A — Yes, a design round after the drain proof**, starting from B3's receipt and the cases
     Product QA's prototype brings.
   - **B — No.** `no_commit` is the only other ending, and anything wider stays in Product QA.
   - **C — Now.** The round runs beside the drain proof.

   Waits on it: nothing queued; [C7](#13-what-tillr-will-not-change). **Can wait**: R2-2's store
   that does not land is its first slice, which the build plan carries.

   <!-- vantage: question id=OQ-SP5 leaning="A — the idea is sound and Product QA will bring real cases, but it touches the planner the drain proof rests on." -->

   _Leaning:_ A — the idea is sound and Product QA will bring real cases, but it touches the planner
   the drain proof rests on.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-SP6: Should the raw `tillr item state` face stop moving items out of a finished state before the rest of the terminal-state block lands?**

   The face moves anything anywhere today **[ran]**; the queued block fixes it, behind two other
   lifecycle items, and its c3 (no move out of `landed` or `resolved-elsewhere`) needs neither.

   - **A — Leave the block as queued.** The hole closes when the chain lands.
   - **B — Split c3 out as P5, with nothing in `blocked_by`.** The block loses c3 in the same commit.
     Since round two, the queued `item-def-finished-states-final` is this option widened to the
     `apply` face, where Product QA showed the same hole; the block keeps its c3, which that item
     makes true.
   - **C — Restrict the raw face further:** only an operator flag may write `landed` at all.

   Waits on it: nothing now; the owner's go-ahead filed the widened B. **Can wait**: a ruling
   records the choice.

   <!-- vantage: question id=OQ-SP6 leaning="B — the move out is the one that re-queues finished work, it waits on nothing, and C adds an authorization rule no design has." -->

   _Leaning:_ B — the move out is the one that re-queues finished work, it waits on nothing, and C
   adds an authorization rule no design has.

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 **OQ-SP7: Should a graph that finishes with no commit be able to write child items?**

   Product QA wants child items from a step, such as a finding that turns out to be two bugs or a
   report per recipient, from work that never lands ([their Section 3](tillr-product-qa-requests.md#3-graph-change)).
   D-IP6 writes child items only after a land pass, and no item queues it. tillr's own AutoQA design
   has the same need ([Section 4, item 4](#4-where-the-documents-disagree)).

   - **A — Yes: amend D-IP6 so the manifest also applies when a `no_commit` graph settles**, in the
     idea pipeline's own next round.
   - **B — No.** The consumer's dispatcher files the child with `tillr item apply`, which Product QA
     does today.

   Waits on it: child items inside tillr for both Product QA and tillr's own AutoQA; nothing queued.
   **Can wait**: Product QA files children with `apply` meanwhile.

   <!-- vantage: question id=OQ-SP7 leaning="A — settling is the same one-writer moment a land pass is, and B gives a consumer a second writer of tillr's lineage." -->

   _Leaning:_ A — settling is the same one-writer moment a land pass is, and B gives a consumer a
   second writer of tillr's lineage.

   **Answer:**
   > _(empty — fill in when decided)_

8. 💬 **OQ-SP8: When unshared items are routed to triage, how does a bug tillr's own AutoQA confirms reach tillr's store?**

   This question is about tillr's own AutoQA design, which files the bugs it confirms into the
   store of the system it tests, here tillr's own (its `D-AQ2`). It is not a Product QA question:
   Product QA files only into stores of its own, which the queued block leaves alone
   ([B8](#12-on-the-existing-design-path)).

   - **A — A document block.** Into tillr's own store, AutoQA writes a tracked document block and
     sync imports it, so D-AQ2's `item apply` becomes a block.
   - **B — A store-level setting** that turns the hold off, with `item apply` everywhere.
   - **C — `item apply` as today.** The bug enters as an undeclared item, and the rewritten hold
     routes it to triage or design for its block.

   Waits on it: D-AQ2 in tillr's own AutoQA design. **Can wait**: no AutoQA code exists, and the
   rewritten item already gives an undeclared item a next step.

   <!-- vantage: question id=OQ-SP8 leaning="A — tillr's own queue keeps one intake, and a store nobody syncs has no documents to be missing from." -->

   _Leaning:_ A — tillr's own queue keeps one intake, and a store nobody syncs has no documents to
   be missing from.

   **Answer:**
   > _(empty — fill in when decided)_
