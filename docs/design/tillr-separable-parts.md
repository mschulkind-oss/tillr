---
title: "Which of tillr's parts can another program use without the whole loop, and which requests would turn it into a workflow engine"
status: in-review
stage: DESIGN
updated: 2026-11-16
tags:
  - tillr
  - design
  - store
  - boundaries
summary: "An assessment of the Product QA pipeline's thirteen requests to use tillr's parts separately. The store, the item face and the run face already work without git or the drain and should be made dependable. Five requests go against settled rulings. Four decisions are the owner's."
next: "The owner rules OQ-SP1 to OQ-SP4; the first slice is filed as items only after OQ-SP3."
depends-on:
  - tillr-item-graphs.md
  - tillr-judge-and-lifecycle.md
  - tillr-idea-pipeline.md
---

# Which of tillr's parts can another program use without the whole loop?

**Status:** 2026-11-16. This is an assessment, and nothing has been built for it. On 2026-11-16 I
re-ran each fact the requests state about current behavior. I used the binaries built from this
tree by `just tillr-build`, on throwaway stores, with commands and results in
[Evidence](#evidence). The older copies on `PATH` were not run. Each claim is marked
**[ran]**, **[read]** (file and section) or **[inferred]**.

> **In short.** tillr's store, item face and run face already work without git or the drain, and
> making them dependable costs hours. A general workflow engine for a bug-finding pipeline would
> cost the drain proof weeks and undo rulings that the item-graph and lifecycle designs enforce
> with checks.

**Why it matters.** The Product QA pipeline asks for thirteen changes. Built as asked, they would
compete directly with the owner's current goal: land the whole existing queue through the drain
unattended, then run 10 lanes.

**Start at [the judgment](#the-judgment).** The [request table](#1-the-thirteen-requests) gives the
status of each request. [Section 2](#2-the-requests-that-cut-against-the-design) argues the five
that go against the design.

**Needs your ruling:** [OQ-SP1](#OQ-SP1), [OQ-SP2](#OQ-SP2), [OQ-SP3](#OQ-SP3), [OQ-SP4](#OQ-SP4).

**Reads with:** [tillr-item-graphs.md](./tillr-item-graphs.md) (graphs inside one item, and what a
node may never be), [tillr-judge-and-lifecycle.md](./tillr-judge-and-lifecycle.md) (item states
and terminal-state rulings), [tillr-idea-pipeline.md](./tillr-idea-pipeline.md) (item kinds and
child items), [board-utilities.md](./board-utilities.md) (the process-and-record seam).

---

## The words this document uses

- **Store:** tillr's SQLite database of items, runs and events.
- **Drain:** the loop that plans, runs, gates and lands items.
- **Keeper:** the program that restarts the drain when it exits
  ([tillr-keep](../../tools/tillr-roll/tillr-keep)).
- **Lane:** a leased slot with its own directory, where one run executes.
- **Fence:** the set of paths an item may change.
- **Gate:** the check run on a candidate commit. **Lander:** the program that puts a gated commit
  on the trunk.
- **Workflow, graph and node:** as defined in
  [tillr-item-graphs.md, The Words](./tillr-item-graphs.md#the-words). A workflow is a tracked
  template of stages. Running it gives an item a graph, and each node is one agent or one command
  call.
- **Criterion:** one testable condition on an item. **Verdict:** a ruling on one criterion.
- **Hold:** the planner's named reason for not dispatching an item.
- **Product QA pipeline:** a bug-finding pipeline around a fuzzer called Mangle. It wants two
  stores. A *findings store* holds one item per finding, and each item's graph runs re-check,
  replay, reduce, a bounded investigation, a verdict, an assessment, a report draft, the owner's
  approval and the send. A *fuzzing-ideas store* holds ideas that a planner turns into fuzzer
  runs.

## The requests, restated

These were requested by the Product QA pipeline design conversation on 2026-11-16, written by
another agent with the owner. Each request says it was "checked at the installed binaries".

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

---

## The judgment

**Should tillr become separable parts?** Some of it already is, and that part should be made
dependable. tillr's designed seam is processes and records. Each face reads a record and writes a
record, and no utility calls another inside one process
([board-utilities.md §5.9](./board-utilities.md#59-the-composed-pipeline)) **[read]**. The store,
the item face and `tillr run exec` already work without git or the drain. A plain command ran as a
run, with a lease and an end cause, on a store the drain never opened **[ran]**. Making those
pieces reliable on their own also helps tillr: AutoQA, a fresh machine and tests all use them that
way.

**What should not happen is tillr turning into a general workflow engine.** The designs draw that
line on purpose and enforce it with loader checks:

- no cycles, timers, or wiring a model wrote;
- no node that changes anything outside its worktree;
- one item, one outcome.

These come from [tillr-item-graphs.md §5](./tillr-item-graphs.md#5-what-a-node-may-never-be) and
[agent-runtime.md §6](./agent-runtime.md#6-the-task-graph) **[read]**. Five of the thirteen
requests cross that line: requests 3, 4, 5, 8 and 13.

**Cost to the current goal.** Most of what the pipeline would use is already queued on the drain's
own path: the item-graph blocks, the lifecycle blocks, recovery commands, lanes outside the cap,
and seat shares **[read]**. Building those costs nothing extra as long as they land in their
existing order. The extra cost comes from a second loop per checkout, an outcome vocabulary, a
stable library and a general graph algebra. Each needs a design round, and some reopen settled
rulings **[inferred]**.

**The recommended answer to the other agent** is [OQ-SP1](#OQ-SP1)'s leaning:

> Build the pipeline's dispatcher and graph outside tillr for now. Use tillr for what is stable: an
> item store addressed with an explicit `--store`, items whose prose and evidence are files you
> point at, `tillr run exec` for leased runs with end causes, and the event log. The defect that
> blocks you (views and the planner fail on a store the drain never touched) gets fixed, and a
> second store gets documented. Command nodes, node outputs and a "finished with no commit" exit
> are already queued. Outcomes per kind, reopening landed items, open graph rewriting and a Go
> library go against settled rulings. Bring each back with a concrete case from your prototype.

---

## 1. The thirteen requests

**Buckets:**

| Bucket | Count | Requests |
| :--- | :--- | :--- |
| Works today | 0 | — |
| Small defect | 1 | 1 |
| Unbuilt but consistent with the design | 7 | 2, 6, 7, 9, 10, 11, 12 |
| Cuts against the design | 5 | 3, 4, 5, 8, 13 |

Several requests are split cases, and the table says which part is which.

| # | Status today | Size | What tillr gains | Recommendation |
| :--- | :--- | :--- | :--- | :--- |
| 1 | **Small defect.** `tillr view status`, `events`, `lanes`, `running` and `passes` work on a store built with `tillr-store init` and `tillr-item apply`. `tillr view waiting` and `tillr drain why` exit 3 with `no such table: lanes` **[ran]**. The cause: each utility adds its tables when it first opens the store ([registry.go](../../tools/tillr/store/registry.go)), and the planner reads the run utility's tables without checking that they exist. `Close` already guards a missing table this way ([lifecycle.go](../../tools/tillr-item/item/lifecycle.go)) **[read]**. | Hours | Fresh-machine and AutoQA stores stop failing | Fix it in the first slice ([OQ-SP3](#OQ-SP3)) |
| 2 | **Unbuilt, partly works.** Every face takes `--store` **[read]**. The loop's other defaults, its lanes and its records sit under the checkout ([main.go](../../tools/tillr-drain/main.go), [looprecord.go](../../tools/tillr-drain/drain/looprecord.go)). The keeper finds a loop by process name `tillr-drain` and a working directory equal to the checkout, and takes one lock per checkout ([tillr-keep](../../tools/tillr-roll/tillr-keep), `find_loop`) **[read]**. Run ids are `run-<item>-a<N>`, so two stores in one checkout could collide on record directories **[inferred]**. | Days | Little for this repository | Document `--store` for use without the loop now. A second loop waits for the drain work |
| 3 | **Cuts against** the closed seven-state model ([idea pipeline, D-IP1](./tillr-idea-pipeline.md#61-settled-decisions)). The four verdicts the request names belong to criteria, not items ([verdict.go](../../tools/tillr-item/item/verdict.go)) **[read]**. | Design round | A countable "why it ended" | [OQ-SP2](#OQ-SP2) |
| 4 | **Cuts against** an accepted ruling: `landed` and `resolved-elsewhere` are terminal ([lifecycle §3.2](./tillr-judge-and-lifecycle.md#32-transition-preconditions-and-attempt-holds-d-jl4)), enforced by the queued terminal-state block. The defect the request found is real, and worse than it says. A raw `tillr item state` writes `landed` on an item with an open criterion and no landing, moves it back to `in-queue`, and writes `resolved-elsewhere` with no pointer **[ran]**. `reopen` refuses a landed item and reopens a closed one **[ran]**. | Defect: already queued | Finished items stop quietly re-entering the queue | Keep the ruling; see [Section 2.2](#22-reopening-a-finished-item) |
| 5 | **Cuts against in its general form; the bounded parts are queued.** Replacing a later node that has not started is the queued proposal block, under `D-IG3` ([§2.2](./tillr-item-graphs.md#22-in-fog-refinement-and-the-flattening-projection)). The drain writes child items from a manifest after landing ([D-IP6](./tillr-idea-pipeline.md#61-settled-decisions)). Folding is `resolved-elsewhere` with an item pointer **[read]**. None of the three is built **[ran]**. | Bounded: queued | Already wanted | Keep the rulings; see [Section 2.3](#23-steps-that-change-the-graph) |
| 6 | **Unbuilt but consistent.** Command nodes and outputs bound to their producer with a sha256 are queued item-graph blocks ([command nodes](./tillr-item-graphs.md#add-command-nodes-that-run-a-registered-program), [named outputs](./tillr-item-graphs.md#add-named-outputs-that-a-later-node-reads-from-its-producer)). Today the loader accepts agent stages only ([workflow.go](../../tools/tillr-drain/drain/workflow.go)) **[read]**. Re-admitting an item only when a stored basis changed already exists ([lifecycle §4.2](./tillr-judge-and-lifecycle.md#42-inspected-rework-tillr-drain-rework-and-the--v2-cutover-d-jl6)). Skipping a node whose input digests did not change is not designed **[read]**. | Queued | Cheaper deterministic steps | Wait for the queued blocks |
| 7 | **Unbuilt but consistent, in the narrow form.** A workflow exit of `no_commit` runs neither gate nor lander, and still settles the item ([exit condition](./tillr-item-graphs.md#add-an-exit-condition-that-decides-when-an-items-graph-is-done)). `landed` accepts a verified `no_commit` receipt (D-JL4). The two exit codes exist: 1 is negative, 4 is "an outside dependency prevented a decision" ([exit.go](../../tools/tillr/record/exit.go)) **[read]**. | Narrow form: queued | Investigation-only items stop being held as "did nothing" | Narrow form: wait for it. General form: [Section 2.1](#21-outcomes-per-kind-and-completion-that-is-not-a-landing) |
| 8 | **Cuts against, inside a graph.** No node may change anything outside its worktree, and the landing is the one outward act, done once after the exit ([§5](./tillr-item-graphs.md#5-what-a-node-may-never-be)) **[read]**. Outside a graph it works today as a `blocked` hold with a named waiter and reason **[ran]**. That is how tillr settles its own uncertain landing: by reading the trunk ([already_on_trunk.go](../../tools/tillr-drain/drain/already_on_trunk.go)) **[read]**. | Hours, as a convention | Little | Keep it in the pipeline's dispatcher |
| 9 | **Unbuilt but consistent; mostly there.** The lease claim, reaper and end cause live in the run package ([lease.go](../../tools/tillr-run/run/lease.go)). `tillr run exec` ran a plain command with no git and no drain **[ran]**. It needs a lane directory, and an adapter directory holding `pause-kind` and `extract-report` **[ran]**. The heartbeat belongs to the loop. Detecting an interrupted run is unexported drain code ([orphan.go](../../tools/tillr-drain/drain/orphan.go)) **[read]**. | Days | A run face that can say "dead" without a loop | Wait for the drain work |
| 10 | **Unbuilt but consistent, except deadlines.** Lane count, backend policy, and pluggable gate, land and verdict programs exist **[ran]**. Seat shares, lanes outside the cap and a spend ceiling are queued on [the tillr roadmap](../tillr/roadmap.md) **[read]**. A deadline puts a clock in the plan, which [§5](./tillr-item-graphs.md#5-what-a-node-may-never-be) refuses **[read]**. A second dispatcher cannot share a store with the drain: the planner holds an item whose run it did not dispatch as `run-link-missing`, and an item with no fence as `no-fence` **[ran]**. | Hooks: design round | Shares and caps are already wanted | Use the queued policy items; the pipeline's own dispatcher runs on its own store |
| 11 | **Unbuilt but consistent** with visibility first. `tillr view events --since` exists **[ran]**, and mail has an acknowledgment **[read]**. No queued item covers an owner cursor **[read]**. | Hours | "New since you last looked" here too | Build it outside first |
| 12 | **Partly works.** The "emitters unbuilt" claim traces to [tillr-distribution.md](./tillr-distribution.md) and is partly out of date. A throwaway store recorded `item.add`, `item.update`, `run.allocate`, `run.usage` and `run.finish` **[ran]**. A run event carries no purpose or inputs, and node inputs arrive with the named-outputs block **[read]**. | Days | Fuller reconstruction in the observatory | Wait for the drain work |
| 13 | **Cuts against** the process-and-record seam, and against the rate of change. The packages can be imported: no module under `tools/` has an `internal/` directory, and each resolves its siblings with a relative `replace` **[ran]**. | Weeks, then ongoing | None now | [OQ-SP4](#OQ-SP4) |

---

## 2. The requests that cut against the design

### 2.1 Outcomes per kind, and completion that is not a landing

**For the request.** `landed` means a gated commit moved the trunk, and a finding never makes one.
The pipeline needs "inconclusive" kept apart from both "failed step" and "rejected". If every
ending must be one of seven states, the real outcome lives in free-text reasons that no view can
count.

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

The narrow choice, whether to record an outcome at all, is [OQ-SP2](#OQ-SP2).

### 2.2 Reopening a finished item

The accepted lifecycle ruling makes `landed` and `resolved-elsewhere` final so that a finished item
never quietly returns to the queue **[read]**. The reason was this repository's `-v2` clones and
the hand-written state moves the lifecycle design records. That ruling still holds. A finding that
comes back after it was confirmed is a new item that names the old one. Reopening a closed item
already works **[ran]**. This document proposes no change. The raw-state hole is the queued
terminal-state block's to close.

### 2.3 Steps that change the graph

**What the design already allows.** A node is fixed once its run is recorded. A planning node may
propose replacing one later node that has not started with a tracked workflow, within that
workflow's stated bounds. The drain applies the proposal in one transaction against the graph it
read, and keeps the old definition in an append-only revisions table. A refused proposal is
recorded with its reason (`D-IG3`) **[read]**. That meets the request's own condition, "atomically
with its event, one writer", for one bounded operation.

**Where the two words are written down.** Both are in this repository's documents, not in the
installed pack:

- "In fog" is Guild's word for a node that has not started
  ([The Words](./tillr-item-graphs.md#the-words),
  [§2.2](./tillr-item-graphs.md#22-in-fog-refinement-and-the-flattening-projection)).
- Guild is described in [agent-runtime.md §14](./agent-runtime.md#14-adopting-guilds-graph-algebra)
  as "a Go implementation of dynamic graph transformation" from another of the owner's projects
  **[read]**.

The Guild archive those documents cite is no longer in this checkout **[ran]**.

**Why the general form cuts against the design.** Two rulings stand in its way:

1. **One item, one outcome (`D-IG2`).** Steps inside an item are nodes, and separate deliverables
   are separate items. One item per finding is the second kind. So "the findings of a run that has
   not ended" is a manifest of child items applied after the producer settles (D-IP6, unbuilt),
   not a node placeholder **[read]**.
2. **No model writes wiring (`D-IG5`, `D-IG9`).** A step that edits edges freely is the
   "workflow-engine creep" that [agent-runtime.md §14](./agent-runtime.md#14-adopting-guilds-graph-algebra)
   names as the first warning sign **[read]**.

**For the request.** The findings graph really does change shape as it runs. Four operations are
already designed: replacement, the child manifest, fold as a pointer, and split as a parent
resolved into its children. The pipeline should show a case those four cannot express before
anything wider is considered.

### 2.4 Several stores in one workspace

These parts assume one store per checkout **[read]**:

- the keeper's discovery and lock;
- the loop's records and lanes under the checkout;
- the default store path.

There is one more. The queued unshared-item hold on [the tillr roadmap](../tillr/roadmap.md) would
hold every item that no tracked document declares, which is every imported finding. The land lock
is per git repository, so it does not matter to a store that never lands.

**Does "state in documents" apply to a findings store?** The rule exists so that deleting the store
and running sync rebuilds the same queue ([tillr-convergence.md §3](./tillr-convergence.md#3-the-target)).
The pipeline leans toward keeping bug records and evidence as files reviewed in git. That meets the
same invariant if each finding's record file can be re-imported **[inferred]**. So the principle
applies, but the mechanism does not, because the source documents are finding files and not
roadmap blocks. That argues for keeping the importer outside tillr, not for adding a second sync
dialect.

### 2.5 A Go library as the connection point

Every package can be imported today. The designed seam, though, is the CLI and its JSON records,
with exit codes as the contract, and that seam is what the conformance tests pin. A second
consumer that links the Go types would freeze names that change daily. From 2026-11-13 to
2026-11-16, 167 commits touched `tools/tillr*` and 37 touched the core store, item and run packages
**[ran]**. Records carry a `record_version` and can be versioned. Go APIs here cannot be versioned
cheaply. The choice is [OQ-SP4](#OQ-SP4).

---

## 3. The two questions

**Does an item hold the record, or point at files?** It does both, and today the split is
consistent **[read]**:

- **The store holds what a decision was made against.** Criterion text is stored and frozen, and
  a verdict names its sha256. Verdicts, decisions, states and actors are rows and events.
- **Files hold the bytes.** The item's prose is a document section, named by `body_ref`. Run
  reports, logs and briefs are files the run record points to. Node outputs will be files recorded
  with name, type, sha256 and location. An artifact is "a digest plus a locator"
  ([agent-runtime.md §14](./agent-runtime.md#14-adopting-guilds-graph-algebra)).

The pipeline's lean toward pointing is right. It needs one more rule: whatever a verdict relied on
is bound by its digest, so an edited file cannot silently change what was judged.

**Which requests cut against the design?** Requests 3, 4, 5 (in its general form), 8 and 13, for
the reasons in [Section 2](#2-the-requests-that-cut-against-the-design). Requests 2, 6, 7 (in its
narrow form), 9, 10 (apart from deadlines and hooks), 11 and 12 are unbuilt but consistent.
Request 1 is a small defect.

---

## 4. A proposed first slice

This is proposed work, not queued work. Whether to file it now is [OQ-SP3](#OQ-SP3). Three
pieces, all small and within the design:

1. **Views and the planner on a store the drain never opened.** `tillr view waiting` and
   `tillr drain why` treat a missing utility table as empty, as `Close` already does. A test builds
   a store with only `tillr-store init` and `tillr-item apply`. Hours.
2. **A second store, documented.** Every face runs with an explicit `--store`, a lane root
   outside the checkout, and no drain or keeper on that store. A store has one dispatcher, because
   the drain holds items whose runs it did not dispatch **[ran]**. An hour.
3. **Plain commands through `tillr run exec`, documented.** A plain command needs an adapter
   directory with `pause-kind` and `extract-report`, and a working directory relative to its lane;
   an absolute one is refused **[ran]**. A stock command adapter would save every caller the stub.
   Hours, and it moves the queued command-node block a little closer.

**What the pipeline builds outside tillr for now:**

- its dispatcher, with its ordering and deadlines;
- each finding's graph and its rewriting;
- the importer from a fuzzer run directory to finding files plus `tillr item apply`;
- the send step and its uncertain state;
- the owner's acknowledgment cursor.

As command nodes, node outputs, the `no_commit` exit and the child-item manifest land, pieces can
move back in where the fit is real.

---

## 5. Risks to tillr

- **Scope creep away from the owner's goals.** Each request is reasonable alone. Together they
  redirect the coming weeks from landing the queue unattended to generalizing the item model. The
  roadmap puts loop-stopping defects before any build
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
- **Two dispatchers on one store.** The planner would hold the pipeline's items **[ran]**. Keep the
  stores separate.

---

## Evidence

Run on 2026-11-16 with the binaries `just tillr-build` produces, the environment's `BOARD_DB` and
`XDG_STATE_HOME` cleared, and every store a new file outside this repository. `$S` names that
store.

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
ls scratch/board-guild-graph-material/                        no such directory
fd -t d internal tools/ (tillr modules)                       nothing
git log --oneline --since=2026-11-13 -- 'tools/tillr*' | wc -l          167
git log --oneline --since=2026-11-13 -- tools/tillr \
  tools/tillr-item/item tools/tillr-run/run | wc -l                     37
```

**Not determined:**

- whether the older installed binaries behave differently on these checks, since only the current
  build was run;
- whether the modules are published anywhere, since no network check was made;
- whether two drain loops with different stores can run in one checkout, since no loop was started
  next to the live one;
- what the Guild material contained.

---

## Open Questions

1. 💬 **OQ-SP1: What should the Product QA pipeline be told to build on tillr now?**

   The pipeline's agent is about to prototype a findings store and its own dispatcher. They ask
   whether to build on tillr, and today `tillr view waiting` on their store exits 3. Whatever we
   answer, they will start building on it this week.

   - **A — Use the stable parts, build the rest outside.** They use the item store with an
     explicit `--store`, `tillr run exec` and the event log, and write their own dispatcher and
     graph. tillr fixes one defect and documents two things.
   - **B — Wait and build on tillr's graphs.** They wait for command nodes, outputs and child
     manifests, which land behind the drain proof.
   - **C — Don't use tillr.** They write their own store, and none of tillr's lease or event work
     transfers.

   <!-- vantage: question id=OQ-SP1 leaning="A — the stable parts work without git or the drain today, and nothing in it waits on the drain proof." -->

   _Leaning:_ A — the stable parts work without git or the drain today, and nothing in it waits on the drain proof.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-SP2: Should a finished item say how it ended, in words its kind defines?**

   A finding ends as a repeat, rejected, inconclusive or confirmed. Today the owner sees `closed`
   with a free-text reason, or `resolved-elsewhere` with a pointer. Nothing can count "how many
   findings were inconclusive this week". The seven states are closed on purpose, and the question
   is whether to add anything beside them.

   - **A — An outcome field on the move to a finished state.** Its words are defined per kind,
     the way a close reason is stored now. The seven states and the board stay as they are, and
     views can count outcomes. It costs a design round after the drain proof.
   - **B — No new field.** Outcomes stay in reasons and pointers. The pipeline counts them in its
     own files.
   - **C — New states per kind.** Every view, the planner and the landing rules learn each new
     state.

   <!-- vantage: question id=OQ-SP2 leaning="B now and A later — A keeps the closed state set, but it is not worth a design round before the drain proof." -->

   _Leaning:_ B now and A later — A keeps the closed state set, but it is not worth a design round before the drain proof.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-SP3: Should the three small first-slice items be filed ahead of the drain proof?**

   Three small pieces would unblock the pipeline: views and the planner working on a store the
   drain never opened, a documented second store, and a documented plain-command run. Filed now,
   they enter the queue the drain is working through, at whatever rank they get. Filed later, the
   pipeline works around the failing views until the queue is landed.

   - **A — File them now, at the bottom of the queue.** They land when the drain reaches them,
     and the queue grows by three small items.
   - **B — File them after the drain proof.** The proof's queue stays as it is, and the pipeline
     waits.

   <!-- vantage: question id=OQ-SP3 leaning="A — they are hours of work, fix a defect fresh stores hit too, and the drain should prove it can take new work." -->

   _Leaning:_ A — they are hours of work, fix a defect fresh stores hit too, and the drain should prove it can take new work.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-SP4: Should another program connect to tillr through its commands and records, or link its Go packages?**

   The pipeline asks to link tillr's Go packages into its own binary. Today they can, by pointing a
   `replace` at this checkout, and the next day's commit may rename what they called. From
   2026-11-13 to 2026-11-16, 167 commits touched the tillr tools.

   - **A — Commands and records are the only supported connection.** A breaking record change gets
     a new `record_version`. Linking Go code is allowed at the linker's own risk, pinned to a
     commit.
   - **B — Support a Go library.** tillr keeps exported APIs stable, and every refactor checks a
     second consumer.

   <!-- vantage: question id=OQ-SP4 leaning="A — records are already versioned and pinned by the conformance tests, and a stable Go API would slow a tool that changes daily." -->

   _Leaning:_ A — records are already versioned and pinned by the conformance tests, and a stable Go API would slow a tool that changes daily.

   **Answer:**
   > _(empty — fill in when decided)_
