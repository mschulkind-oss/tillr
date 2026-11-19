---
title: "The item graph is the whole path, and one pure function decides what comes next"
status: in-review
stage: DESIGN
updated: 2026-11-18
tags:
  - tillr
  - graphs
  - dag
  - drain
  - policy
  - landing
  - design
summary: "A proposal that every act on a tillr item (build, rewrite, design answer, judge, gate, landing, revert, owner question, sub-agent) is a node in that item's graph, that a node is fixed once it starts and done once it ends, and that one pure function over the graph and a short list of facts adds whatever nodes are needed next. Counts, clocks, hash triggers and item-level holds are replaced by stated endings and named waits."
next: "The owner ruled OQ-GP7 on 2026-11-18: while a check is red, work lands only if it does not touch what that check reads, with no landing cap. The designer's decisions D-GP1 to D-GP15 await the owner's review (the waiting rule of D-GP14 is now ruled as OQ-GP7), and no migration step is filed."
depends-on:
  - tillr-item-graphs.md
  - tillr-drain-successor.md
  - gate-rework.md
  - landing-principles.md
---

# The item graph is the whole path, and one pure function decides what comes next

**Status:** 2026-11-19. The static per-attempt workflow machinery ([item graphs](./tillr-item-graphs.md)) is built (`config/tillr/workflows/` holds six workflow files and `tools/tillr-drain/drain/` runs multi-node workflows), though `.tillr.toml` names no default workflow per seat so items without a `workflow` attribute still run as one implicit single worker. This document's whole-life graph model (gate, judge, landing and later acts as nodes, and the pure policy function) is not built. Eight of its rules are built outside the graph, as rules of the single-run drain, and [Section 12](#12-what-this-replaces) lists them. The `D-GP` rows of the ledger are the designer's decisions and await the owner's review, except that the waiting rule of `D-GP14` is now ruled as [OQ-GP7](#OQ-GP7); the `OQ-GP` rows record what the owner said. The design is written to his direction of 2026-11-17, restated in [Section 1](#1-the-direction-and-the-rules). What was not verified is marked so.

> **In short.** An item's graph stops being a template for its build and becomes the record of everything that happened to it. Nothing routes work except one pure function that reads the graph and adds the nodes it lacks, so every "what next" is a table row with a test, and every repetition has a stated cause.

**Why it matters.** The built graph ends before the gate, and `.tillr.toml` sets no default workflow per seat, so items without an explicit `workflow` attribute still run as one worker. What follows the gate (refusals, conflicts, design conflicts, rewrites, a red trunk) is routed by separate mechanisms of the drain, each with its own state, so no one place says what happens next or why. A scout counted from the store on 2026-11-17 (the query was not kept): 65 conflict-fix runs, 30 runs of 22 rewrite items, and no node run among 801 runs. The drain has since dropped most of its counts and hash triggers; the structure is unchanged.

**The shape.** Four parts: a per-item graph of nodes with stated endings; the fog-of-war invariant (a node that has started can never change), which the store enforces; a pure policy function that transforms only what has not started; and a scheduler that chooses who runs each ready node. A queue-level pass and the trunk's proof sit above the items under the same invariant.

**Cost.** It overturns six rows of the item-graph ledger, including the owner's own rule of 2026-11-15 that a sub-agent is never a node. It retires the word "attempt" and the hold vocabulary. It needs each adapter to state how its run ended instead of leaving that to a reading of the log. It also retires the red cap (the stop on all landing once 20 landings sit past the last proven commit): the owner ruled on 2026-11-18 that there is no landing cap ([OQ-GP7](#OQ-GP7)), and the cap stays live in the drain until the rule that replaces it is built ([Section 8.2](#82-proof-and-a-red-trunk)).

**Start at [Section 5.3](#53-the-starting-rule-table)**, the rule table. The model before it exists to make that table safe, and everything after it is a consequence.

**Needs your ruling:** nothing now. **Ruled by the owner on 2026-11-18:** [OQ-GP7](#OQ-GP7). **Ruled by the owner on 2026-11-17:** [OQ-GP1](#OQ-GP1), [OQ-GP3](#OQ-GP3), [OQ-GP4](#OQ-GP4), [OQ-GP5](#OQ-GP5), [OQ-GP6](#OQ-GP6). **Closed as superseded:** [OQ-GP2](#OQ-GP2), which [the storage design](./tillr-storage.md) now asks.

**Reads with:** [item graphs](./tillr-item-graphs.md) (the built graph machinery this extends and partly overturns), [the drain's successor function](./tillr-drain-successor.md) (the earlier pure function this splits in two), [the core and the landing definition](./tillr-core-and-git-landing.md) (why the rule table belongs to a definition), [the gate rework](./gate-rework.md) (the full-suite tier, the green pointer and the revert setting), [the landing principles](./landing-principles.md) (rulings a landing change is checked against), [early design landing](./tillr-early-design-landing.md) (how a question and its design reach the trunk ahead of their item), [the landing call](./tillr-landing-call.md) (how a worker states its ending), [the storage design](./tillr-storage.md) (the structure behind the store), [the vocabulary](./tillr-vocabulary.md) (one meaning for item, node, run, session and worktree; [Section 6](#6-scheduling) carries its text on worktrees).

---

## 1. The direction and the rules

The owner dictated the model on 2026-11-17. The quotes are cleaned from dictation.

> I want everything possible to be a node in the item graph and let it show the entire path something took: judging, landing, rewrites, gate runs, failures, successes, everything. You take a graph and you run a function over it to add any new nodes that are required, and you can do this frequently and cheaply. If something has just finished an implementation where it thinks it's done — that suspended tool call — you stick another node on the end, and that is the gate. The gate runs with whatever actor needs to run it; it gets scheduled however it gets scheduled. The gate succeeds or fails; then you run that scheduling again, and it sends it either to the landing queue or back to resume the previous session. You can't restart a node, but you can have a resume node that resumes from that previous point and continues it. You could even fork sessions this way, and it should be representable and trivially so.

> Nodes are not necessarily append-only, but we need to follow the fog-of-war principle: you can't have causality issues based on graph transformations, but any transformation is possible. You can't have loops. You can't go back after a node is done. It's done. Those assets can be reused and resume a session, but that node itself is done.

> A node's definition becomes fixed — enough of it — as it starts running. If there is crash recovery, sure, you can resume that node; that is functionally unknowable from a normal execution, which is fine. We create new nodes for actual events, not exceptions. You can resume after an exception, though we should note these exceptions in the logs so we can analyze post hoc. You can't change nodes underneath something that's already launched; that violates causality. The whole point is this is supposed to appear to be functional, with certain affordances because the pure version is impossible. If anything has acted on data that it has read from this graph, that is set. If any action in the graph has been predicated on a part of that graph, that's the fog-of-war principle: it cannot change.

> I would love to hoist subagents from Pi and others into these nodes as much as possible.

On where a design answer's commit lives:

> You send something back for design because the design is not clear. The actual design doc gets updated — good first principles. But where does that commit land? ... I guess it could land with that item. You would hope there's not conflicting designs going on. You could have a provisional design in an item that you might not want to land. So the designer runs on the branch, lands on the branch, which then means I'm not sure it can get out of date.

On a red trunk:

> Didn't we have plans to revert things on a red tree? We need a full story around remedying this. Filing a high-priority mechanic item seems reasonable.

The rules this design is held to, numbered so later sections can cite them:

| Rule | What it requires |
| :--- | :--- |
| **R1** | No attempt counts, retry budgets or "exhausted" states. No time limit fails work; a budget is a target that is shown. |
| **R2** | No guess trigger. Nothing re-runs because a hash, a document, a policy file or the trunk tip changed. Each event does only the one thing it means. An edit to an item's description does not by itself re-run a designer or cancel work. |
| **R3** | An item reaches the owner only through an explicit objection that names him, recorded as an open question in a design document. A run that states nothing returns to its producer. The owner sees two categories: "draining" and "needs you". |
| **R4** | A gate refusal returns to the same producing session with the gate's output, uncounted, as often as needed. |
| **R5** | A killed or interrupted run resumes as the same node. Nothing is counted. The interruption is logged. |
| **R6** | Bugs are fixed, not guarded. A held state names who moves it. The loop never stops on one item's condition. |
| **R7** | The prose rewrite stays, as a node in the item's own graph on a cheap, fast model with a small context. |
| **R8** | Work is built against the latest trunk, never a past commit. A design that no longer fits the trunk is a redesign, stated as one. Items that churn the same ground get dependencies. |
| **R9** | A pass over the whole queue may fix dependencies and order that no single item can see, without becoming a guess trigger. |
| **R10** | The red cap is not accepted. Reds must not pile up, and there must be a way to pull things out and land them. An unproven trunk is never treated as red. |
| **R11** | A design answer, a rewrite and a triage commit on the item's own branch. The item's deliverable reaches the trunk in one landing. A document that a design answer changed, or that carries a question for the owner, reaches it earlier, by an early landing ([early design landing](./tillr-early-design-landing.md)). |
| **R12** | Nothing depends on the git trace record, which the owner ruled out on 2026-11-17 and `d09016bba` stopped recording (it reverses [`OQ-QP4`](./tillr-queue-persistence.md#91-decision-ledger), which had kept the trace on). The shell-command record stays. |

---

## 2. The words

Each term is used throughout with exactly this meaning. Terms marked *(coined here)* are new in this document.

| Term | Meaning | What it is not |
| :--- | :--- | :--- |
| **Item graph** | The one directed, cycle-free graph an item has for its whole life. It holds every act taken on the item. | Not one graph per attempt. The word "attempt" is retired by this design. |
| **Node** | One act: one invocation of a harness, one command, one landing attempt, or one question put to the owner. It has a definition, a start and an ending. | Not a stage of a template. A template is expanded into nodes and then is gone. |
| **In fog** | The state of a node that has not started. "In fog" and "not started" are the same thing; [item graphs](./tillr-item-graphs.md#the-words) already uses both. | Not "pending in a queue": a node in fog may still be changed or removed. |
| **Fixed** | The state of a node from its start. The part of its definition listed in [Section 3.2](#32-what-is-fixed-and-when) can never change again. | Not finished. A fixed node may still be running. |
| **Done** | The state of a node that has an ending. Nothing about it changes again, and it never runs again. | Not "succeeded". A refused gate run is done. |
| **Claim** *(coined here)* | The single store transaction that starts a node: it moves the node from in fog to fixed and records who runs it. | Not a lease. A lease is liveness bookkeeping and can lapse; a claim cannot be undone. |
| **Ending** *(coined here)* | The record a node is done with: a cause from a closed list for its kind, the evidence, and where the cause requires it, the next actor. | Not an exit code and not a line read from a log. |
| **Objection** | The ending by which an actor says it should not continue and names who must act next: the design seat, the owner, another item, or the tooling. It replaces "blocked". | Not a failure. Nothing is counted against the objector. |
| **Asset** | Something a node leaves that a later node may use: a session, a commit, an output file, a transcript. | Not the node. Reusing a session does not reopen the node that made it. |
| **Item branch** | The commits the item's nodes have made, on top of the trunk commit last taken in. The store pins its head, so the branch survives the collection of the item's worktree. The owner's word is "the branch". | Not the item's worktree. The worktree is a checkout of the branch that the item acquires and that can be deleted and restored. |
| **Event** | Something a participant did or decided that the rule table has a row for: a node ended, the owner answered, a backend closed, a proof ended. | Not an exception. |
| **Exception** | The machinery failing to carry a node to an ending: a killed process, a host restart, a stopped loop, a dropped connection, a stall. It is logged and the same node resumes. | Never a node, never counted, never seen by the rule table. |
| **Premise** *(coined here)* | A part of the graph or a fact that a claim or a rule recorded having read. A premise can never change afterward. | Not everything a program happened to load. Only what was recorded as read is protected, and reading without recording is refused. |
| **Fact** | A typed statement about the world outside the item graph that the policy function is permitted to read, such as "backend X is closed". The list is closed, in [Section 5.2](#52-permitted-facts). | Never a hash or "something changed". |
| **Transformation** | A list of changes to the fog: add a node, remove one, replace one, add or remove an edge into one. | Never a change to a fixed or done node. |
| **Policy function** *(coined here)* | The pure function from an item graph and the facts to a transformation and a set of waits. It holds every routing rule. | Not the scheduler, and not a process that runs anything. |
| **Wait** *(coined here)* | The policy function's statement that it deliberately adds nothing after some ending yet, with the reason and the mover. It is derived on every evaluation and never stored. | Not a hold. Nothing clears it; it stops being produced when its fact changes. |
| **Mover** | The actor or fact a wait names: a node, another item, a backend's reopening, the owner by a named question. | Never "someone". A wait with no mover is a defect in the rule table. |
| **Seat** | The name a node uses for the kind of worker it needs. The repository's backend policy maps each seat to backends and models. | Not a model. No node, template or rule names a model. |
| **Take-in** *(coined here)* | The act of bringing the latest trunk under the item branch before a node that needs the worktree starts. | Not a re-run. It happens only when a node is about to start anyway. |
| **Proof** | One run of the full suite on one trunk commit. The gate rework calls this the heavy tier. | Not the gate. The gate judges a candidate before it lands. |
| **Mechanic** | The owner's word for the high-priority item filed to make a red trunk green. Today's code calls it a trunk fixer. | Not a revert. A revert is a landing with no model in it. |
| **Observed node** and **dispatched child** *(coined here)* | A sub-agent that tillr recorded but did not start, and one that tillr started. Both are nodes inside their parent. | An observed node is never the producer of a checked input. |
| **Identical repeat** *(coined here)* | An ending whose cause and evidence key equal those of an earlier ending in the same chain. Defined in [Section 7](#7-what-stops-repetition). | Not "the second failure". Two different failures are not a repeat. |
| **Cut** and **early landing** | A cut is a command node that builds a documents-only candidate on the trunk tip; an early landing is the landing of that candidate before the item's deliverable. Both are defined in [early design landing](./tillr-early-design-landing.md#2-the-words). | A cut is not a rebase: it writes nothing to the item branch. |
| **Landing call** | The tool call by which a worker ends its run and offers its commit; a refusal comes back as that call's result. Defined in [the landing call](./tillr-landing-call.md#2-the-words). | Not a node. It is the stated ending of a work node. |

---

## 3. The model

### 3.1 Node kinds

Every kind is one of three executions: an agent (a harness invocation), a command (a tracked program with no model), or a person. The lander, the gate and the full suite stay the separate programs they are today; a node is the record and the scheduling of one run of them.

| Kind | Execution | What it does | Endings (the closed cause list) |
| :--- | :--- | :--- | :--- |
| **work** | agent | Builds or designs the deliverable on the item branch | `submitted`, `objection`, `backend-closed`, `missing-output`, `refused-by-check` |
| **rewrite** | agent, cheap seat, small context | Rewrites the item's own block to the writing guide; meaning, criteria ids and fence must not change | as work |
| **triage** | agent, design seat | Writes the block for an item that arrived with none | as work |
| **design answer** | agent, design seat | Answers an objection, or diagnoses an identical repeat, by amending the design on the item branch | `answered` (names the actor to resume), `objection`, and the three machinery causes of work |
| **judge** | agent, read-only | Evaluates a candidate it did not write | `pass`, `fail` with findings, and the machinery causes |
| **command** | command | Runs one tracked command from the registry | `pass`, `fail` |
| **take-in** | command | Brings the latest trunk under the item branch | `clean` with the new head, `conflict` with paths |
| **cut** | command | Builds a candidate on the trunk tip that holds only documents: an item's changed documents. It writes nothing to the item branch ([early design landing](./tillr-early-design-landing.md#3-the-operation)) | `cut` with the candidate and its paths, `empty`, `conflict` with paths |
| **gate run** | command | Runs the gate on the candidate as taken in | `pass`, `refused` with the failing checks |
| **land attempt** | command | Asks the lander to put the candidate, a cut, or a revert, on the trunk | `landed` with the trunk commit, `conflict` with paths |
| **owner question** | person | Carries one question that names the owner, from asked to answered. It needs no worktree and no process | `answered` |
| **resume** | agent | Continues an earlier node's session with one new message | the endings of the kind it continues |
| **fork** | agent | Starts from a copy of an earlier node's session, leaving the original untouched | the endings of the kind it continues |
| **backend move** | agent | Continues the work from the item branch and the earlier transcript on another backend, because the session cannot move | the endings of the kind it continues |
| **hoisted child** | agent | A sub-agent of a running node, observed or dispatched ([Section 9](#9-subagent-hoisting)) | `returned`, and for a dispatched child the endings of work |

Two kinds live in the queue's graph and not in an item's: **proof** and **queue plan** ([Section 8](#8-the-queue-level)).

A kind not in this table is refused when a template or a rule table is loaded. A new kind is a change to this design.

### 3.2 What is fixed and when

| Moment | What becomes unchangeable |
| :--- | :--- |
| **The claim** | The kind; the inputs, each by producer node, output name and content hash; the assets consumed (the session resumed or forked, the item-branch head started from, the trunk commit taken in); the seat, backend, model and effort the scheduler chose; the brief; the write scope; the rule that created the node and the facts that rule read |
| **The ending** | The cause, evidence and next actor; every declared output by content hash; the assets produced (session, head, transcript location); the spend |
| **Never fixed while running** | Liveness bookkeeping: the lease, the process segments, the exceptions logged against the node, progress notes, and the list of hoisted children, which only grows |

"Enough of it" is therefore everything another party could have acted on. A running node's bookkeeping changes freely because nothing may be predicated on it: the store offers no way to record it as a premise.

**A node has one run for its life.** Crash recovery continues that run under the same identity, as the built resume already does ("a resumed attempt is one attempt continued"). From the graph, a node that was interrupted five times and one that never was look the same; the difference is in the exception log.

### 3.3 Endings

Every node ends with an explicit cause. The rules that apply to all kinds:

1. **An ending is written, never inferred.** An agent node states its ending in a structured record the adapter reads. That record is written by the tool the worker calls to end: `land` for `submitted`, `obstacle` for an objection ([the landing call](./tillr-landing-call.md)). Where the adapter's ending capability (what it declares about how its harness can end a run) is `footer`, the adapter writes the record from the report's footer. Reading a result word from the tail of a log is removed.
2. **A process that ends by itself and states nothing has ended `missing-output`.** That is an event: the worker stopped without saying why. It returns to the same session with a message naming what is missing.
3. **A process that was stopped has no ending.** That is an exception. The node resumes.
4. **tillr's own checks at the end of a node can refuse it.** A dirty worktree or a missing commit is answered inside the `land` call and ends nothing. A change outside the write scope, or a missing or malformed declared output, ends the node `refused-by-check` with the check's output; so do the first two on an adapter with no tool. It returns to the same session. This replaces the rule that allowed one continuation and then refused the candidate.
5. **An objection names its next actor.** One of: the design seat; the owner, with the anchor of the open question it wrote; another item, by id; the tooling, with evidence of the defect; a stronger seat, with what the worker tried and what it lacks. An objection that names none of these, or names the owner without a question, is `missing-output`. A worker's objections are the obstacle kinds of [`D-JL11`](./tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11). Its owner question reaches the owner through the design seat, which writes the question, because a worker cannot write outside its fence.
6. **`backend-closed` is stated by the adapter** when the harness reports that the backend refuses work, with the reopening time when the backend gives one. It is never guessed from an exit code.

### 3.4 Assets and their reuse

- **A session** is reused by a resume (the same session continues, on the backend that holds it), a fork (a copy continues, and the original may be resumed or forked again), or not at all. A backend move reuses the transcript as a file to read, not as a session.
- **A commit** is reused by starting from it. Every writing node starts from the item-branch head the previous writer left, after a take-in.
- **An output** is reused by an edge that names its producer and its name, as [item graphs](./tillr-item-graphs.md#71-decision-ledger) rules in `D-IG6`.
- **How a message is delivered to a resumed session** is an adapter capability: as the result of the suspended call where the harness supports it, otherwise as the session's next message. The model needs only that a session can be continued with one message.
- **A session that cannot be resumed** (its transcript is gone, or its backend is no longer in the policy) is a fact with a stated reason. The rule table turns a resume in fog into a backend move ([Section 5.3](#53-the-starting-rule-table), row 18).

### 3.5 Edges

| Edge | Meaning | The source must be |
| :--- | :--- | :--- |
| `after` | The target may not start until the source is done | done |
| `input` | As `after`, and the target reads the named output of the source | done |
| `resumes` | The target continues the source's session | done |
| `forks` | The target starts from a copy of the source's session | done, or running for an observed fork |
| `within` | The target is a hoisted child of the source and ends before it | running |

A `within` edge is the one edge whose source is still running. It keeps the graph cycle-free because it depends on the parent's start, which is already fixed, and the child's result reaches the parent as a recorded delivery on the parent node, not as an edge.

Edges carry no conditions. Which node follows which ending is decided by the rule table. A template's `when_outcome` is kept as shorthand: when the outcome it names can no longer happen, the policy function removes the nodes that depended on it from the fog.

### 3.6 Events and exceptions

The line between the two decides what the graph shows and what the log shows.

| | Event | Exception |
| :--- | :--- | :--- |
| What happened | A participant acted or decided | The machinery failed to carry a node to an ending |
| Examples | The gate refused; the worker objected; the lander reported a conflict; a backend closed; the owner answered | The process was killed; the host restarted; the loop was stopped; a check stalled; a lease was lost |
| What follows | A rule table row adds nodes | The same node resumes. A command node is run again from its remembered partial results |
| Where it is recorded | As an ending in the graph | As an exception record against the node, with what was running and what was observed |
| Counted | No | No |

A stall detector is liveness, not a time limit: it may stop a process that makes no progress, and what follows is a resume. It never produces an ending.

### 3.7 The item branch and the latest trunk

**Every node that needs the worktree starts on the latest trunk (R8).** Before a work, rewrite, triage, design answer, judge or gate node is claimed, if the trunk has moved since the item branch last took it in, a take-in node runs first.

- **Clean.** The item branch now sits on the trunk tip, and the node starts. The trunk commit taken in is part of the node's claim.
- **Conflict.** The take-in ends `conflict` with the paths. Nothing is forced. The rule table resumes the session that last wrote those paths, on the latest trunk, with the conflict (row 11). That session resolves it, or objects: `redesign` to the design seat when the trunk has moved so far that the design no longer fits, or an objection naming the item it collides with.
- **The take-in merges.** It adds a merge commit to the item branch and rewrites nothing, so every head a done node recorded stays an ancestor of the branch. The landed result is one squashed commit.

A running node is never interrupted because the trunk moved. The take-in happens only when a node is about to start, so it is not a trigger (R2).

**Definition edits live on the item branch (R11).** A rewrite, a triage or a design answer that edits the item's block or its design document commits that edit on the item branch. The worker resumes from that commit. The edit reaches the trunk in the item's landing, or earlier when its document goes ahead in an early landing ([early design landing](./tillr-early-design-landing.md)). A cut and its gate run are not preceded by a take-in: the cut is made on the trunk tip.

| Consequence | How it is settled |
| :--- | :--- |
| **Which definition is in force** | The one on the item branch. When a node that may edit the definition ends, its ending carries the block as revised, and the store records it against the item with the node's id. Later briefs, the judge, the gate and the verdicts use the latest revision in the graph. Sync still reads the trunk's documents for intake, and the store keeps both: "on the trunk" and "in force on the item branch, revised by node N" |
| **Who may change what** | A design answer or a triage may change criteria and the fence, as the design seat does today. A rewrite or a work node may not: a changed criterion id or fence in its commit is `refused-by-check` |
| **A trunk edit to the same block** | Arrives at the next take-in as an ordinary three-way merge. It re-runs nothing by itself (R2). If both sides changed the same lines the take-in ends `conflict`, and the design seat's session that wrote the block resolves it |
| **Shared files** | Most blocks share one roadmap file, and design documents are shared. Concurrent items' edits meet at the take-in before the gate and again in the lander's three-way merge. Disjoint blocks merge cleanly |
| **A real conflict in a design document** | Two items changed the same ruling. It goes to the design session that wrote it, which reconciles the two, or objects naming the other item. That objection is evidence that the queue-level pass should have ordered them, and it triggers that pass ([Section 8.1](#81-the-queue-planner)) |
| **A provisional design** | If the item is abandoned its branch never lands. Documents that went ahead in an early landing are on the trunk; what becomes of them waits on a design of how an item is given up ([early design landing](./tillr-early-design-landing.md#7-an-item-that-is-given-up)) |
| **A lasting ruling** | Goes in the design document's ledger, outside the item's block, on the same branch. Retiring the block at landing would otherwise delete it |
| **Another item that depends on the decision** | Reads it on the trunk once the design answer's documents land. An item that depends on the work, and not only on the decision, gets an inter-item edge until this item lands; the design answer's ending may name those items, and the queue level adds the edges. No item reads another item's unlanded branch |
| **A question for the owner** | Must be visible while the branch is unlanded. The owner ruled on 2026-11-17 that the question lands on the trunk, together with the design text that changed and that the question is about, so he reads both in Vantage ([OQ-GP6](#OQ-GP6)). A cut of those documents is gated and landed ahead of the item, and the branch takes it in ([early design landing](./tillr-early-design-landing.md#3-the-operation)) |

---

## 4. The fog-of-war invariant

**Statement.** Once anything has acted on a part of the graph, that part cannot change. Everything else may be transformed in any way that leaves the graph cycle-free and its promises to fixed nodes kept.

Two things make this enforceable: "acted on" is defined by recorded premises, and every change to a graph goes through one writer that checks them.

### 4.1 Premises

Something has **read** a part of the graph when, and only when, it recorded that part as a premise. There are three recorders:

1. **A claim** records the node's own definition, every producer output it reads, every asset it consumes, and the rule and facts that created it.
2. **A rule application** records the endings it answered and the facts it read. A node added by a rule carries those as its premises from birth, so the graph says why each node exists.
3. **A queue-level edge** records the item facts it was predicated on ([Section 8.1](#81-the-queue-planner)).

Reading the graph to draw it, or to compute a plan nobody applies, records nothing and protects nothing. That is the affordance the impure world needs: a view can read anything, and only an act pins what it read.

### 4.2 What a transformation may touch

| Operation | Allowed when |
| :--- | :--- |
| Add a node | Always. It is born in fog. This includes adding nodes after a graph whose every node is done, where the definition permits leaving a finished state |
| Remove a node | It is in fog, and no fixed node names it as a premise |
| Replace a node's definition | It is in fog, and every output promised to another node is still promised |
| Add an edge | Its target is in fog. A `within` edge is added only with its child |
| Remove an edge | Its target is in fog |

A replaced or removed definition is kept in the append-only revision record the built graph code already has. The graph is not append-only; its history is.

### 4.3 What the store refuses

Each refusal is a check in the one transaction that applies a transformation or a claim. A refused transformation is recorded with its reason and changes nothing.

1. Any change to a field that [Section 3.2](#32-what-is-fixed-and-when) lists as fixed, on a fixed or done node.
2. An edge whose target is fixed or done: a new dependency for something already launched.
3. Removing or changing anything a fixed node holds as a premise.
4. A cycle.
5. A transformation computed against an older revision of the item's graph than the one stored. The caller recomputes; the function is cheap.
6. A node in fog whose input names an output no node produces.
7. A claim on a node that is not in fog, or whose `after` sources are not all done. This is what makes a claim happen exactly once.
8. A second ending for a node, or an ending whose cause is not in its kind's list.
9. A write scope outside the item's fence, or a child's scope outside its parent's.
10. A node whose seat the backend policy does not declare, or a template or command that is not tracked.

### 4.4 How it is tested

- **A table of refused transformations**, one case per row above, each asserting the refusal reason and an unchanged graph.
- **A property over generated graphs:** apply any sequence of accepted transformations and claims, and assert that the fixed fields of every fixed node, and every recorded premise, read back byte for byte.
- **A race:** two writers apply different transformations computed from the same revision; exactly one is accepted and the other is refused by row 5.
- **A kill:** the writer is killed inside the transaction; on reopening, the graph is at the old revision or the new one, never between, and the event that announces the change exists if and only if the change does.

### 4.5 Relation to the item-graph rulings

- **`D-IG3` is kept and widened.** "A node is fixed when its run is recorded" is the claim. "No node rewrites itself" stands. What widens is who may transform the fog: the policy function, not only a planning node's proposal.
- **`D-IG5` is kept.** What may be inserted is still a tracked template, a tracked command or a kind from [Section 3.1](#31-node-kinds). No model writes wiring. A running node asks for a change in its ending, as data, and the policy function decides.
- **`D-IG9` is amended.** Its refusals of cycles, a language on an edge, timers, undo steps, in-process nodes and self-rewriting stand. Its refusals of a landing inside the graph and of a child session as a node are withdrawn. Its "bounded repair written out as nodes" is withdrawn: an unrolled repair is a count spelled as a template.

---

## 5. The policy function

### 5.1 Signature and properties

```text
policy(item graph, facts) -> (transformation, waits)
```

- **Pure.** No clock, no store handle, no file, no process. The same inputs give the same output.
- **Total.** Every ending in the graph is either answered by a node, answered by a wait that names a mover, or final (the graph is complete). An ending in none of the three states is a defect in the table, reported against the table and not held against the item.
- **Idempotent.** Each node a rule adds records the ending it answers. An answered ending matches no rule, so evaluating again after applying the result returns nothing.
- **Local.** It reads one item's graph. What crosses items arrives as facts.
- **Cheap.** It is a table lookup per unanswered ending. It is evaluated after every event that concerns the item, and may be evaluated at any other time without effect.

**One caller applies it.** The loop evaluates the function and hands the transformation to the store's writer. The planning face and the views evaluate it to show what would happen and apply nothing.

**A fault in evaluation** is an exception against that one item. The item shows it with the tooling as mover, and every other item proceeds (R6).

### 5.2 Permitted facts

A fact is a typed statement with a known meaning. A rule may consume a fact only for that meaning.

| Fact | Meaning | Who writes it |
| :--- | :--- | :--- |
| Backend open or closed | A backend refuses work, until a stated time where it gives one | The adapter's stated ending; the quota probe |
| Session available or not | A session can or cannot be resumed, with the reason | The scheduler, when a resume cannot be claimed |
| Question answered | The owner answered the question with a given id in a given document, with the answer's text and the trunk commit that holds it | The question index, reading the committed trunk. Written once for each question |
| Item landed | Another item's deliverable is on the trunk | That item's land attempt |
| Trunk state per check | Proven green at a commit; red on a named check, attributed to a landing or not; the inputs of that check | Proof nodes ([Section 8.2](#82-proof-and-a-red-trunk)) |
| Queue edges | This item's fog waits on another item's landing | The queue level |
| Intake findings | The block has prose findings; no document declares the item; the fence admits nothing | Sync |
| Owner release | The owner named this item to land despite a named red check | The owner, by a command |

Not on the list, and refused if a rule names them: the clock; any content hash; the trunk tip as a trigger; the number of earlier runs; the policy file's own contents. The trunk tip is read by the take-in and the lander when they run. It is not an input that makes the function produce something new.

An edit to an item's description is not a fact any rule reads. Later nodes read the new text; running nodes keep the brief they were claimed with (R2).

### 5.3 The starting rule table

This is the rule table for this repository's landing definition. "Producer" means the session of the node that last wrote the thing in question. Every resume is uncounted.

| # | When the graph holds | The function adds | Replaces, in the drain as audited on 2026-11-17 |
| :--- | :--- | :--- | :--- |
| 1 | No nodes; the item has a block with no prose finding | The nodes of the item's template, in fog | The dispatch decision |
| 2 | No nodes; the block has prose findings | A rewrite node, then the template after it | A separate rewrite item with its own lane, gate and landing |
| 3 | No nodes; no document declares the item | A triage node, then the template after it | One triage run per version of the request |
| 4 | A work, rewrite, triage or resume node ended `submitted` | A judge node when the item asks for one, otherwise a gate run; a take-in first when the trunk moved | The exit bracket of the single-run path |
| 5 | Any node ended `refused-by-check` | A resume of its session with the check's output | One continuation, then `candidate-no-op` |
| 6 | An agent node ended `missing-output` | A resume of its session saying what is missing. Where the harness has a boundary hook, the worker is told inside the running node and no ending is recorded; the row applies only when the process exits anyway | Classifying the run from its log; the owner by default |
| 7 | A judge ended `fail`; or `pass` | A resume of the producer with the findings, delivered as the answer to the producer's open landing call; or a gate run | The judge bracket that swaps the lane's brief and session |
| 8 | A gate run ended `pass` | A land attempt, unless the trunk facts make it wait ([Section 8.2](#82-proof-and-a-red-trunk)) | The landing step; the red cap; the whole-queue hold |
| 9 | A gate run ended `refused` | A resume of the producer with the gate's output (R4), delivered as the answer to the producer's open landing call | A hold until an input hash changes or a person retries |
| 10 | A land attempt for the item's deliverable ended `landed` | Nothing. The graph is complete. The producer's landing call stays open until the proof covers the landing, then closes; when the item's worktree is released is [the landing call's question](./tillr-landing-call.md#OQ-LC1) | The landing record |
| 11 | A land attempt or a take-in ended `conflict` | A resume, on the latest trunk, of the session that last wrote the conflicting paths, with the paths, delivered as the answer to the producer's open landing call | One conflict-fix run per candidate, then `conflict-exhausted`; stale-base re-staging |
| 12 | A node ended with an objection naming the design seat | A design answer that starts at the objector's head and forks the document's designer session; when it ends `answered`, a resume of the objector from the design's commit, which does not wait for row 27 | A conflict row, a separate design run and landing, then a new worker from the trunk |
| 13 | A node ended with an objection naming the owner, with its question | A cut of the question's documents, its gate run and its land attempt, then an owner question node after the landing, or directly when the cut ends `empty`. When the owner question ends `answered`, a resume of the objector with the answer | The owner as default mover of unrecognized holds |
| 14 | A node ended with an objection naming another item | A queue edge request; the objector resumes after that item lands and a take-in | Hash-based re-admission |
| 15 | A node ended with an objection naming the tooling | A defect item filed through intake, a queue edge to it, and a resume after it lands | Second-failure routing to the owner |
| 16 | A node ended `backend-closed` | A backend move on the seat's next open backend; with none open, a wait naming the backend and its reopening | The failover budget, then `run-infra-exhausted` |
| 17 | A resume in fog whose backend closed | A wait naming the backend, since the session lives there | The same budget |
| 18 | A resume in fog whose session is not available | It is replaced by a backend move that reads the transcript where one exists | One automatic fallback |
| 19 | The ending just recorded is an identical repeat | A design answer as diagnosis, in place of the resume the row above would add ([Section 7](#7-what-stops-repetition)) | Every count |
| 20 | An ending carries a request to replace a later node in fog | The replacement, when the requesting node's template allows it; otherwise the refusal is recorded and the graph is unchanged | The proposal transaction, kept as built |
| 21 | A running node asks for a child the harness routes to tillr | A dispatched child within it | Nothing; new |
| 22 | A landing of the item, its deliverable or an early landing, was then reverted for a named check | A resume of the producer, with the answer `reverted` to its open landing call carrying that check's output; its next gate run includes that check. For an early landing the producer is the session that wrote the documents, and it is told to restore them from the head it recorded | The fixer filed against the landing |
| 23 | A cut ended `cut` | A gate run on its candidate, with no take-in and no judge, as the owner ruled for documents-only landings on 2026-11-18 ([the diff-review design](./tillr-diff-review.md#decision-ledger), `D-DR2`). Rows 8, 9 and 11 apply to it; the producer is the session that last wrote the documents | Nothing; new |
| 24 | A cut ended `conflict` | As row 11, then a new cut | Nothing; new |
| 25 | A cut ended `empty`, or its land attempt ended `landed` | Nothing for the cut. A node that waited on it is ready | Nothing; new |
| 26 | A node ended `submitted` while a cut of the item is in fog | The cut and its followers are removed; the item's landing carries the documents | Nothing; new |
| 27 | A design answer ended `answered` having changed documents | A cut of those documents. A resume that repairs a refused or conflicting cut is claimed after the item's running writer ends | Nothing; new |

**Degenerate cases.** An item with no template gets the tracked one-node template. An item whose fence admits nothing gets a wait naming the author. A template that expands to nothing is refused when it is loaded, as today. Two rules never match one ending: the table is checked for overlap when it is loaded, and row 19 is the one stated precedence.

### 5.4 Waits

A wait is what a hold becomes. It is produced by the function each time, names its reason from a closed list and its mover, and is never stored, cleared or retried.

| Wait | Mover |
| :--- | :--- |
| On a backend | The backend's reopening, with the time when known |
| On another item | That item, shown with its own current node or wait |
| On a red check | The revert or the mechanic that will clear it |
| On the owner | The named question. This is the only wait that puts an item under "needs you" |
| On intake | The author of the block, with the finding |
| On the tooling | The defect item, or the exception record |

### 5.5 How it is tested

- **The table as a table.** One fixture per row: a graph and facts in, the exact transformation and waits out. A row with no fixture fails the check that loads the table.
- **Idempotence**, for every fixture: apply the result and evaluate again; the second result is empty.
- **Totality**, over generated graphs: every ending is answered, waited on with a mover, or final.
- **No forbidden input:** the function's inputs are the two documents it is given; a test evaluates it with the clock, the network and the file system unavailable.
- **Replay:** the stored sequence of events for a finished item, evaluated from an empty graph, reproduces the stored graph.

### 5.6 Another definition supplies its own

The rule table is the landing definition's, not the core's. The core owns the graph, the invariant, the store's checks and the evaluation loop. A definition supplies the kinds it uses, their ending lists, its facts and its policy function.

- **This repository's function** is the table above, tracked and versioned beside the templates.
- **Another definition**, such as the Product QA pipeline's findings store, supplies a program in the hook form [the core and the landing definition](./tillr-core-and-git-landing.md#5-the-extension-points) already chose: the core gives it the graph and the facts and reads back a transformation, which the store checks like any other. The program may be written in any language.
- **Purity of a foreign program** cannot be enforced. It is tested: the definition check evaluates each of its fixtures twice and refuses a program whose two answers differ.
- **What the core does not preclude**, per the owner's ruling of 2026-11-17 recorded in [the Product QA requests](./tillr-product-qa-requests.md#3-graph-change): a node inserted directly downstream of a running one, and nodes appended to a finished graph. Both are transformations of the fog.

### 5.7 Relation to the successor function

[The drain's successor function](./tillr-drain-successor.md#21-the-function-and-its-inputs) is one pure function, `next`, that answers which single transition an item takes, or `none` with a hold. This design keeps its properties (total, deterministic, order-independent, no clock) and splits its job in two:

| `next` answered | Now answered by |
| :--- | :--- |
| Which transition does this item earn? | The policy function: which nodes should exist |
| Is there a lane, a backend, a lease? | The scheduler ([Section 6](#6-scheduling)) |
| `none`, with a reason from the hold vocabulary | A wait, with a mover |
| The transition's identity by item, attempt, from and to | The node's identity in the graph |

The plan face survives as `D-DS10` ruled: it shows the function's result and the schedule that would follow, applying neither.

---

## 6. Scheduling

Scheduling is a separate concern with a separate input: the ready nodes of every item, the resources, and the backend policy. It adds no nodes and reads no rule.

**Ready** means in fog, with every `after`, `input`, `resumes` and `forks` source done.

**What the scheduler chooses, and fixes at the claim:** the backend, from the seat's list; the model and effort, from that backend's row; whether a resumed session is compacted first. It does not choose a worktree: a node that needs one runs in its item's, which the claim creates or restores.

**How a node names what it needs.**

- **A seat, as data.** A node names a seat. A seat is any name the repository's backend policy declares, and loading a template or a rule table that names an undeclared seat is refused. This replaces the closed list of four. This repository would declare `design`, `build`, `review`, `scout` and `prose` to start, and add one per kind of work it wants routed apart.
- **Never a model.** The policy file is the only place a model is named, so a provider change is one edit.
- **Constraints the node carries.** A resume must run on the backend that holds its session. A writing node needs a worktree to itself. A rewrite states a small context: its brief holds the block and the guide and nothing else.
- **A stronger seat is named, not climbed.** There are no rungs. When a diagnosis decides the work needs a stronger worker, its ending names the seat.

**Worktrees.** A worktree belongs to an item, which acquires it at the claim of its first node that needs one. When a node ends the worktree is idle, not free: it stays the item's, and it may be collected and later restored, because the item branch is pinned in the store. Two writing nodes of one item never run at once, since each starts from the head the last one left; read-only nodes of one item may run together in it. An owner question needs no worktree. The lifecycle, the bound and the running-agent limit are in [the vocabulary, Section 3](./tillr-vocabulary.md#3-what-a-lane-was-and-the-worktree-as-a-resource).

**Order, when ready nodes exceed resources.** Work the owner is waiting on first, outside the running-agent limit, as `D-IG10` rules. Then, nearest to done first: land attempts, gate runs and judges, take-ins, resumes and backend moves, design answers, rewrites and triage, and last the first nodes of items not yet started, by rank. The mechanic and proof nodes are placed by [Section 8.2](#82-proof-and-a-red-trunk). The exact tie-break within a class is the implementer's, provided it is stable.

**Capacity limits are scheduling, not policy.** How many read-only nodes run together is a resource setting. No capacity setting ends, refuses or re-routes a node.

---

## 7. What stops repetition

With no counts, three things stop a chain of resumes: an explicit objection, an identical repeat, or the owner.

**An objection** is the ordinary stop. The producer, the judge or the design seat says it should not continue and names who must act. Nothing else is needed when actors state what they see.

**An identical repeat** is the stop for actors that do not. A **chain** is the sequence of nodes linked by `resumes`, backend move and the endings between them. Each ending has an **evidence key**, declared with its cause:

| Ending | Evidence key |
| :--- | :--- |
| Gate `refused` | The set of failing check ids, with the hash of each check's normalized failure output |
| `refused-by-check` | The check, and the paths or outputs it names |
| `missing-output` | What was missing |
| `conflict` | The conflicting paths and the other side's commits |
| Judge `fail` | The criterion ids judged unmet |
| Objection | The next actor and the normalized objection text |
| Design answer `answered` | The normalized instruction it gives |

An ending is an identical repeat when its cause and key equal those of **any** earlier ending in the chain, not only the last. A producer that alternates between two failures is caught on the third.

**What happens.** Row 19 adds a design answer as diagnosis in place of the resume. It reads both endings and the work between them, and it must end in one of three ways: an instruction to the producer that differs from every earlier one in the chain, possibly naming a stronger seat; an objection naming the owner, with its question; or an objection naming the tooling, with a defect. An instruction equal to an earlier one is `missing-output` and returns to the diagnosing session saying which instruction it repeats.

**For an exception**, the same idea without a node: when a node's exception record repeats the earlier one with the same observation, the machinery is at fault. A defect item is filed through intake with both records, the node waits on it with the tooling as mover, and every other item proceeds.

**The weakest point, stated plainly.** Nothing here stops a chain whose every ending is new. A producer that fails a different check each time, or a diagnosis that rewords the same useless instruction past the normalization, runs until someone objects. That is the cost of R1, and I think it is the right cost: every count the audit found stopped real work that was one message from done, and none of them diagnosed anything. What the design offers instead is information:

- The item page shows the chain.
- The "draining" list is ordered by spend, so a long chain is at the top.
- The owner is the third stop: he can object on any item at any time, which is an event like any other.

There is no spend target for an item to cross, and nothing acts on spend ([OQ-GP3](#OQ-GP3)).

The second weak point is the evidence key. A key that is too fine (raw output with timestamps) never repeats; one that is too coarse (check ids alone) calls real progress a repeat. The normalization for each check's output belongs with the check, and a key that proves wrong is fixed there. It is not compensated with a count.

---

## 8. The queue level

The queue has a graph of its own. Its nodes are proof runs, queue-plan runs and landings of reverts; its edges between items are `after` edges that say "no node of this item is claimed until that item lands". The same invariant governs it.

**An inter-item edge constrains only future claims.** It may be added while the target item has running nodes; they finish. It takes effect at the target's next claim. It never cancels, pauses or re-runs anything.

### 8.1 The queue planner

Two parts, kept apart because one is mechanical and one is judgment.

**The mechanical part** is a pure function over the items' definitions in force and the queue's facts, evaluated like the policy function. It adds edges the definitions already imply: an authored dependency; an objection naming another item; items a design answer names as affected; a defect item an objection filed. It also carries today's rule that two items whose fences overlap do not have writing nodes running at once, as a scheduling constraint read from the fences in force.

**The judgment part** is a queue-plan node: an agent on a planning seat that reads the whole queue and proposes dependencies and order.

| | The queue-plan node |
| :--- | :--- |
| **May read** | Every item's definition in force and its graph, including which paths each done node changed; the queue edges; the trunk facts; the order |
| **May change** | Edges of its own class between items, each with a written reason; the rank of items, which affects only future claims. It may remove only edges a queue-plan node added |
| **May never** | Touch a node inside an item; cancel or pause anything running; edit an item's definition; remove an authored dependency; add an edge that makes a cycle |
| **How its proposal is applied** | Its ending carries the proposal as data. The queue's function checks it against the rows above and applies what passes, recording each refusal |
| **Its premises** | The item facts each edge was predicated on. An edge whose premise is gone (the other item landed or was abandoned) is no longer produced |

**It must not become a guess trigger.** It runs on events that mean "something crossed items", never on a timer and never because the queue's contents changed in some unread way: a conflict whose other side is a queued or just-landed item; an objection naming another item; the owner asking. Whether a batch of newly admitted items is also such an event is [OQ-GP4](#OQ-GP4).

**Degenerate cases.** An empty queue produces nothing. A proposal that names an item that has since landed is dropped for that edge. An item made to wait on one that is itself waiting on the owner shows that chain as its mover, and stays under "draining".

### 8.2 Proof and a red trunk

The trunk's health is a set of facts written by proof nodes. Nothing else may call the trunk red.

**The states.**

| State of a trunk commit | What makes it so | What it is not |
| :--- | :--- | :--- |
| **Proven green** | A proof node on it ended `green`: every full-suite check passed. The newest such commit is the green pointer | |
| **Unproven** | No proof on it has ended. A proof may be pending or running | Never red. Nothing waits because a commit is unproven |
| **Red on check K** | A proof node on it ended `red`, naming K and its output | Not a stall, not a cancelled run, not elapsed time |
| **No verdict** | The proof was stalled, stopped or cut off by a restart. This is an exception: the same proof node resumes, running only the checks without a remembered result | Not red. No mechanic is filed for it. A repeated identical stall is a tooling defect ([Section 7](#7-what-stops-repetition)) |

**Proof is scheduled, not timed.** While any landing is unproven, one proof node on the newest tip is ready, and it is scheduled ahead of every first node of an unstarted item. A proof that ends while newer landings exist is followed by another. A loop restart resumes the proof; it does not cancel it. This is how reds are kept from building up at the source: the distance between the tip and the green pointer is bounded by how fast proof runs, and proof is never starved by new work.

**Attribution.** When a proof ends red on K, bisect proof nodes run K alone on the landings between the green pointer and the red commit, as [the gate rework](./gate-rework.md#68-a-red-heavy-check) designed.

| What bisection finds | The fact recorded | Why it matters |
| :--- | :--- | :--- |
| Green at the pointer, red from landing L on | Red on K, attributed to L | L can be pulled out |
| Red at the pointer too, with unchanged inputs | Red on K, unattributed: **wrong judge** | No landing is at fault. The check or its environment is broken, and no revert repairs it |
| Red, then green, with no change to K's inputs | K is flaky | A defect in the check. It is filed as a defect item, and the trunk is not red on K |
| A bisection step has no verdict | Attribution is pending | An exception; the step resumes |

**The remedies, in order.**

1. **Revert L**, when it is safe. tillr does this by itself ([OQ-GP1](#OQ-GP1)); the drain has done so since 2026-11-18, with `auto_revert` on. The lander builds the inverse of L on the tip. It lands, as a land attempt with no model in it, only when all of these hold: it applies cleanly; the fast gate passes on the tip with the revert; K passes on the tip with the revert; no landed item declares a dependency on L's item; and L is not itself a revert or a mechanic's landing. The third condition is the test that the attribution was right. The last prevents a remedy from being undone by a remedy.
2. **L's item gets its work back.** Row 22 resumes the session that built L with K's output. Its next gate run includes K, because the fast gate has shown it cannot see this failure. It lands again as one commit when it passes.
3. **The mechanic**, when a revert is not the answer: the revert is unsafe by one of the conditions above; K still fails with L reverted; or the red is unattributed. The mechanic is an ordinary item with an ordinary graph. It starts from the tip, is given K's output and the attribution, and outranks every item that is not itself a remedy. Its gate run includes K. One mechanic exists per failing check and attribution; a later red tip for the same check joins the open one, as built.
4. **How the two combine.** The mechanic is filed only when the revert path has ended in "not possible", so no model run is spent on a red that a revert clears. If a mechanic is already running when another candidate clears the red, its node finishes; its next take-in finds nothing left to do, and it says so.
5. **A mechanic that cannot proceed objects like any other actor.** It names the design seat, L's item, the tooling, or the owner with a question. It never goes to the owner by default (R3). The drain already works this way for one case: a fixer whose obstacle is its fence has the fence widened and returns to its worker.

**What may land while the trunk is red or unproven.** The table is the rule this design proposed as `D-GP14`. The owner ruled on 2026-11-18 that this is the rule to build ([OQ-GP7](#OQ-GP7)), over the drain's rule today and the operator's proposal of 2026-11-18, and that there is no landing cap.

| The candidate | Unproven trunk | Red on K |
| :--- | :--- | :--- |
| A revert of the attributed landing | lands | lands |
| The mechanic's candidate | lands | lands, when K passes with it |
| A candidate whose changed paths are disjoint from K's inputs | lands | lands |
| A candidate that changes K's inputs, and K passes on the tip with it | lands | lands: it is the fix, whoever wrote it |
| A candidate that changes K's inputs, and K still fails with it | lands | waits, with the revert or the mechanic as mover |
| A candidate the owner released by name for K | lands | lands, with his reason on the record |

One rule covers every row: **a candidate waits only when it changes the inputs of a check that is failing on the tip and does not make that check pass.**

- **The reason is attribution.** A change landed into a failing check's inputs cannot be proven by that check, and makes the range a remedy must read longer.
- **No count and no clock appears in the rule.** A red on a narrow check holds a narrow part of the queue. A red on a check whose inputs are the whole tree holds everything but the remedies, which is honest.
- **Work is not held from starting; only landing waits.** A worker started while the trunk is red is told so in its brief, with the check and the remedy, so that it does not spend its run on a failure it did not cause.
- **Each check declares its inputs.** On 2026-11-18 all 25 check files in `tools/gate/checks/` declare `reads`. Eight declare the whole tree: `citations`, `commit-identity`, `design-citations`, `documents`, `file-size`, `pack-sync`, `scratch` and `structural`.

**What the drain does today, and what was proposed on 2026-11-18.** This was the evidence behind [OQ-GP7](#OQ-GP7), kept as the record of what the ruling chose between.

- **Today.** A candidate refused only by checks that also fail on its base (the trunk commit it was built on) is acquitted: the loop records `trunk-already-red` and blocks the item on the fixer's item, with the reason `trunk-broken`, until the fixer lands ([the drain's guide](../../tools/tillr-drain/README.md)). So one red check holds every item that trips it. Beside that, the red cap stops all landing once 20 landings sit past the green pointer (`red_cap` in `config/tillr/gate-policy.json`; its time bound is off).
- **The operator's proposal.** An acquitted candidate lands, with the inherited red checks recorded as inherited. The fixer, the search for the landing at fault and the revert run beside it. Only the red cap holds the queue.
- **An engineer's reading of that proposal.** It is not a contained change. The lander accepts only a pass verdict, so the gate, the loop and the lander must agree on a verdict of "pass, with inherited checks". And while a check is inherited red, a candidate's own regression on that same check is hidden, which blurs the attribution of later reds.
- **How the rule above compares.** It avoids the hidden regression, because a candidate that changes a red check's inputs still waits. I read it as needing the same new verdict, since such a candidate's gate still fails on the inherited check; that reading was not checked in the lander.
- **The cap.** R10 records the red cap as not accepted, and the operator's proposal kept it. The ruling removes it.
- **Not measured:** how many items an acquittal has held, and for how long.

**What must be built for the ruling.** None of it is built; until it is, the drain keeps the acquittal hold and the red cap described above.

1. **A verdict of "passed, with inherited failures".** The lander accepts only a pass verdict today, so the gate, the loop and the lander must all agree on this second passing verdict: a candidate whose only failing checks also fail on the trunk tip, each recorded as inherited with its output.
2. **The comparison of paths.** For each check failing on the tip, the candidate's changed paths are compared with the paths that check declares it reads (`reads` in its file under `tools/gate/checks/`). A candidate whose changes fall outside every failing check's declared paths lands with the inherited verdict; one that changes them waits unless that check passes with it, and the waiting item is shown with the paths that put it there. A red on one of the eight checks that declare the whole tree still holds every item but the remedies.
3. **The red cap removed.** The `red_cap` setting in `config/tillr/gate-policy.json` (20 landings, its time bound already off) and the code behind it in the drain and the lander, with their tests, go, with [the gate rework's `OQ-GR1` row](./gate-rework.md#decision-ledger), once the waiting rule replaces them; the commit that turned off the cap's clock (`d53580bfd`) records that the landing bound stays until then.

**Publishing.** Only a proven commit is pushed: the push sends the green pointer, never the tip (`[push] what = "green"` in `.tillr.toml`).

**Pulling work out.** The owner's phrase covers four moves, all of them above:

- **Out of the trunk:** the revert pulls L out, and L's own session lands it again once repaired.
- **Past the red, by itself:** a built candidate disjoint from K's inputs lands with no one's action.
- **Past the red, as the fix:** a built candidate that makes K pass lands, and the red ends.
- **Past the red, by the owner's word:** the owner releases a named candidate for a named check. It is an event with his reason, it lands, and the trunk panel shows the attribution range it lengthened.

**What the owner sees.** One trunk panel, always present:

- the green pointer, and the landings since it, each marked unproven or red;
- the proof that is running: which commit, which checks have ended and which have not;
- for each red check: its name, the first red landing or "unattributed: wrong judge", the remedy in progress (the revert, or the mechanic with its current node), and the exceptions logged against the proof;
- the candidates waiting on that check, each with the paths that put it there, and the candidates that landed past it;
- the history of reds with the time each took to return to green, as a measurement and not a limit.

A red trunk is under "draining" unless a remedy has objected naming the owner.

---

## 9. Subagent hoisting

A sub-agent is hoisted at one of three levels: **L1**, recorded after it ended; **L2**, shown while it runs; **L3**, started by tillr, so that the spawn request becomes a node tillr schedules. L1 and L2 produce observed nodes. L3 produces dispatched children.

**Target level for each harness.** The facts are from an investigation on 2026-11-17; "source" means read in the fork's source, "unverified" means general knowledge with no probe.

| Harness | Target | What makes it possible | What it needs |
| :--- | :--- | :--- | :--- |
| **pi with pi-subagents, asynchronous children** | **L2** for every child; **L3** for the roles that need it | Each asynchronous run writes a status file and an event file as it goes, with each step's agent, model, tokens, session file and worktree (source). An agent definition can route its launch to an external job provider or an external command (source) | L2: a reader of those files keyed by the parent's session, and one small patch so the status file names the tillr run. L3: a tillr job provider, and agent definitions that use it |
| **pi, foreground children** | L1 | They run inside the parent's own process (source) | The parent's tool-call records |
| **Claude Code** | **L2** | Stream lines of a sub-agent carry the parent tool call's id, and start and stop hooks fire with an agent id (unverified here; probed elsewhere on one version) | The adapter to read what it now discards. L3 waits on a trial showing that a hook can answer a spawn with tillr's own result |
| **agy, muse** | none | No child handling was found | Their nodes are shown as "children: cannot be known" |

**What cannot be dispatched by tillr, and why.**

- **A foreground, in-process child.** There is no launch boundary to route.
- **A child inside a workflow script that awaits it and reads its output.** The control flow lives in the script's runner; tillr would have to answer inside another program's call stack.
- **A child that forks its parent's context.** It inherits the parent's transcript and prompt cache; started by tillr it would be a different, colder thing. It is shown as an observed fork.
- **Any child on a harness with no interception point.**

**The write scope of a child.**

- **A dispatched child's scope is inside its parent's**, which is inside the item's fence. The store refuses a wider one ([Section 4.3](#43-what-the-store-refuses), row 9).
- **A dispatched writer** runs while its parent is suspended on the call, in the parent's worktree, and is checked at its own end as any writing node is. Two dispatched writers of one parent never run at once. Parallel dispatched children are read-only, and a commit or a changed file from one is refused.
- **An observed child is checked by nothing of its own.** Its changes are covered only by its parent's check at the parent's end, and only for what reached the parent's worktree. A child that the harness placed in a worktree of its own is outside tillr's sight until its parent brings the work in. The item page says which children were checked and which were only observed.

**What an observed node may be used for.** It is drawn, its transcript opens, and its spend is counted against the item. It cannot be the producer of an `input` edge and no rule fires on it, because its result is whatever its parent says it was.

**What this overturns in `D-IG12`.** That row made one node with child sessions the default and ruled that a sub-agent is recorded and shown but never a node. Under this design:

- every sub-agent tillr can see is a node, inside its parent;
- the row's three tests (planned, fenced or judged apart) no longer decide node or not-node. They decide dispatched or observed;
- the row's cost argument stands for that choice: a dispatched child pays a staging and a settlement and starts without its parent's cache, which is why the ruling in [OQ-GP5](#OQ-GP5) is not "dispatch everything";
- its worked example survives: a designer may still ask, in its ending, for its remaining node to be replaced by scouts and a join. It may now also simply start scouts with its harness's own tool and have them dispatched on the scout seat.

The trace of shell commands cannot tell one child's commands from another's, since children inherit the parent's run id. Nothing here depends on it doing so, and nothing depends on the git trace record (R12).

---

## 10. Storage

What follows is what the graph model needs from storage. Which structure provides it is decided in [the storage design](./tillr-storage.md), which the owner asked for on 2026-11-17 with the words "do the right thing here, not the easy thing". It recommends one writer process over an append-only log per item, subject to trials. The rest of this design depends only on the interface that document defines, not on the structure behind it.

| Need | Why |
| :--- | :--- |
| **A change and its event are one atomic write** | "Node fixed" with no event, or an event with no node, breaks replay and the view |
| **An ordered history for each item** | The policy function, replay and the item page all read one item's events in order |
| **A claim happens exactly once** | Two schedulers must not both start a node; the loser must learn it at once |
| **Compare against the revision read** | A transformation computed from an older graph is refused, not merged |
| **Kill safety** | A writer killed at any point leaves the old state or the new one |
| **No git, file or process work inside a write** | A caller that does anything else while it holds the write lock stalls every other writer, whatever the structure. The interface must leave a caller no way to do it |
| **A store for a test costs almost nothing** | The table tests of [Section 5.5](#55-how-it-is-tested) need hundreds of small stores |
| **Premises are checked in the same write** | The invariant is only as strong as the transaction that checks it |
| **A store-wide order with a cursor** | The view and replay resume from a position; a reader must never skip a write that committed late |
| **No flush on the write path** | Durability need only survive a process kill (the owner, 2026-11-16), so no write has to wait on the file system's slowest operation |

**What no structure fixes.** A landing holds the store's write lock while git moves a ref. That is a usage fault, and no choice of structure repairs it: a single writer process would block in the same place, with every other write queued behind it. The repair is to do nothing else under the lock, which is the owner's standing rule (minimal work under locks; every lock failure gets a root cause and a fix). It is being fixed now, separately from any structure decision and ahead of it, and nothing in this design offers it as a reason for one structure over another.

Three more defects stand on 2026-11-18 and are fixed the same way, at their cause (R6):

- the loop opens the store for each operation, from many goroutines;
- a planning step computes while it holds the write lock;
- one event kind was written 1.27 million times in one day, which grew the file from about 36 MB to 1.16 GB.

A status command is a read and needs no write lock. It used to take one, because every open of the store did, for a migration check; that was fixed on 2026-11-17 (commit `ef7ed5ff1`). What is left to choose once these faults are gone is [the storage design's](./tillr-storage.md#1-the-verdict) to show, by measurement.

---

## 11. The view

**The top level has two lists.** "Needs you" holds exactly the items with an owner question that is not answered. "Draining" holds everything else, including every item that waits on something other than the owner. Within "draining", items are ordered by spend against target, largest first, so that a chain that is going nowhere is the first thing seen.

**An item's page is its graph.**

- Nodes are drawn in causal order, with chains of `resumes` shown as one continuing session and nodes that ran together side by side.
- Each node shows its kind, its seat, backend and model, how long it ran, its spend, its ending's cause and next actor, and links to its transcript and outputs.
- Hoisted children are drawn inside their parent, marked dispatched or observed, and a parent whose harness cannot report children says "children: cannot be known", never "none".
- Nodes in fog are drawn as such, each with the rule that added it. A wait is drawn where the next node would be, with its reason and its mover as a link.
- Exceptions are marks on the node they interrupted, opening the exception record. Their number is shown as a fact.
- The definition in force is shown beside the definition on the trunk when they differ, with the node that revised it.
- The revision record shows every transformation: what was added, replaced or removed, by which rule or request.

**An item that ran as one node is drawn as one node.** The page never shows an empty graph.

**The queue's page** adds the trunk panel of [Section 8.2](#82-proof-and-a-red-trunk) and the inter-item edges, each with the reason and the node or author that added it.

The queued block that [adds an item's nodes to the item page](./tillr-item-graphs.md#add-an-items-nodes-and-their-runs-to-the-item-page) is the starting point. Its fifth criterion, that a child session is never drawn as a node, is overturned by [Section 9](#9-subagent-hoisting) and needs amending before it is dispatched.

---

## 12. What this replaces

The tables below describe the drain as it was audited on 2026-11-17. By 2026-11-18 the drain had taken up several of this design's rules as rules of its single-run path, with no node behind them. Read from [the drain's guide](../../tools/tillr-drain/README.md) and the tree on 2026-11-18:

- **A gate refusal returns to its worker's own session**, uncounted (row 9; R4).
- **A reverted landing and a squash conflict return to the session that wrote the change** when its lane and session remain; when they do not, a fresh session starts and the reason is recorded (rows 11 and 22).
- **A repeated ending goes to the design seat.** The drain compares an ending's producer, cause and evidence with every earlier ending of the item (`repeatedEnding`; row 19).
- **Automatic revert is on** (`auto_revert` in `config/tillr/gate-policy.json`; [OQ-GP1](#OQ-GP1)).
- **Most counts are gone.** The failover budget, `conflict-exhausted`, `run-infra-exhausted`, re-admission on a changed input hash and the one-continuation rule are no longer written. That removes findings F-01, F-02, F-03, F-04, F-07, F-09 and F-12 of [Section 12.4](#124-audit-findings).
- **A gate that returns no verdict runs again** on the same commit, under a wait named `gate-no-verdict` whose mover is the loop.
- **The proof after a merge judges every check**, and a fixer that is closed releases the items behind it once a proof passes its checks.
- **A worker's run ends with a commit or a named obstacle** ([`D-JL11`](./tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11)).

Still as audited: the red cap, now 20 landings with its time bound off, which the owner has ruled out and which stays until its replacement is built ([OQ-GP7](#OQ-GP7)); a second stall of the full suite counted as red (F-06); a separate rewrite item for each item with prose findings (F-08); and the single-run path itself. Findings F-10, F-13 to F-19 and F-21 to F-32 were not read again.

### 12.1 The item graph ledger

Every row of [the item-graph ledger](./tillr-item-graphs.md#71-decision-ledger), and what this design does to it.

| Row | Verdict | What changes |
| :--- | :--- | :--- |
| `D-IG1` | Kept | Templates still expand into ordinary nodes. Expansion now happens in the fog at any time, not only before dispatch |
| `D-IG2` | Amended | The outside still sees one item, one fence, one deliverable and one landing of that deliverable. The graph no longer stops before the gate. The item's documents may reach the trunk ahead of it, in early landings ([early design landing](./tillr-early-design-landing.md)) |
| `D-IG3` | Kept, widened | Fixed at the claim. The policy function, and not only a planning node's proposal, transforms the fog |
| `D-IG4` | Amended | Path ids and the parent stage are kept. The depth and size caps go, as the owner already directed |
| `D-IG5` | Kept | Tracked templates and commands. The rule table is tracked and versioned the same way |
| `D-IG6` | Amended | Producer-bound inputs and checked outputs are kept. Edges gain kinds and lose their role in routing; `when_outcome` is shorthand |
| `D-IG7` | Amended | Scopes stay inside the fence. Added: the item's own block and design document are inside the scope of a rewrite, a triage and a design answer; a child's scope is inside its parent's |
| `D-IG8` | Amended | The item keeps one worktree across its nodes, as a resource it acquires and that is collected and restored, not as a slot in a pool. The gate and the landing are nodes and run as often as the rule table adds them, so "once at the exit" is withdrawn. What survives: a graph runs correctly with one agent running at a time |
| `D-IG9` | Amended | See [Section 4.5](#45-relation-to-the-item-graph-rulings) |
| [OQ-IG1](./tillr-item-graphs.md#71-decision-ledger) | Kept | One commit on the trunk for an item's deliverable, by the lander's squash |
| [OQ-IG2](./tillr-item-graphs.md#71-decision-ledger) | Kept | Refinement by a running node's request is row 20 |
| [OQ-IG3](./tillr-item-graphs.md#71-decision-ledger) | Kept | Readers together, writers one at a time, now also for dispatched children |
| `D-IG10` | Kept | It is a scheduling rule and sits in [Section 6](#6-scheduling) |
| `D-IG11` | Amended | A node is one act, which adds the owner question and the hoisted child. "Attempt" is retired. "Never a node" for a child session is withdrawn |
| `D-IG12` | Superseded | See [Section 9](#9-subagent-hoisting) |
| `D-IG13` | Amended | The `workflow` attribute and tracked files stand. A seat is declared by the policy, not drawn from a closed list |

### 12.2 Decisions in other documents

Each of these is an amendment owed in the document that holds the row, to be made when this design is accepted, not before.

| Where | Row | What this design does |
| :--- | :--- | :--- |
| [The successor function](./tillr-drain-successor.md) | `D-DS1`, `D-DS6` | One transition per item per pass becomes: evaluate the function, apply its transformation |
| | `D-DS2` | Identity by attempt ordinal becomes the node's identity |
| | `D-DS4` | The `held` position and the hold vocabulary become waits |
| | `D-DS10` | Kept |
| [Planning and tiering](./tillr-planning-node-model-tiering.md) | `D-PT1` | The seat list is open, not a longer closed list |
| | `D-PT3`, `D-PT6` | Two rungs, one fix per rung and one re-plan are counts and are withdrawn. A stronger seat is named by a diagnosis |
| | `D-PT7` | Kept as a scheduling choice: whether a resumed session is compacted |
| [Designer sessions](./tillr-designer-sessions.md) | `D-CD10` | "One automatic fallback" becomes row 18: each reason a session cannot resume is a stated fact |
| [The judge and the lifecycle](./tillr-judge-and-lifecycle.md#return-a-design-runs-gate-refusal-to-its-own-session) | The gate refusal's return to its own session | Kept, and made the rule for every producer |
| [The gate rework](./gate-rework.md#decision-ledger) | [OQ-GR1](./gate-rework.md#decision-ledger), the red cap | Ruled out by the owner on 2026-11-18 ([OQ-GP7](#OQ-GP7)) and replaced by the one waiting rule of [Section 8.2](#82-proof-and-a-red-trunk); the cap is live until that rule is built |
| | [OQ-GR2](./gate-rework.md#decision-ledger), lanes cut from the green pointer | Conflicts with R8. Withdrawn: every node starts on the latest trunk, and a worker is told when that trunk is red |
| | `D-GR6`, automatic revert off | Overturned by the owner on 2026-11-17 ([OQ-GP1](#OQ-GP1)); amended in [gate rework Section 6.8](gate-rework.md#68-a-red-heavy-check) and built 2026-11-18 |
| [The retired board state machine](./board-state-machine.md#7-suspend-and-resume) | Suspend and resume by a tool result | The only earlier design of "done is a suspended tool call". It is retired with the board; [the landing call](./tillr-landing-call.md) designs the mechanism for tillr |

### 12.3 Mechanisms of the drain

| The mechanism, as audited on 2026-11-17 | Becomes | Rule |
| :--- | :--- | :--- |
| Rewrite items, with the original held | A rewrite node at the head of the item's graph | Row 2 |
| Conflict-fix runs | A resume of the producer after a `conflict` ending | Row 11 |
| Design-conflict rows, a separate design run and landing, then a new worker | An objection, a design answer on the item branch, a resume of the objector | Row 12 |
| Re-admission when an input hash changes | Nothing. No rule reads a hash | [Section 5.2](#52-permitted-facts) |
| Second-failure routing to the design seat, then the owner | A diagnosis on an identical repeat; the owner only by a named objection | Rows 19 and 13 |
| The one-continuation rule | A resume after `refused-by-check`, uncounted | Row 5 |
| The judge bracket that swaps the lane's brief and session | A judge node with its own run and session | Row 7 |
| Item-level holds and the hold vocabulary | Waits, each with a mover | [Section 5.4](#54-waits) |
| The red cap, and the whole-queue hold while a fixer is open | The one waiting rule, as the owner ruled; proof as scheduled nodes ([OQ-GP7](#OQ-GP7)) | [Section 8.2](#82-proof-and-a-red-trunk) |
| Stale-base detection and re-staging | The take-in before every node that needs the worktree | [Section 3.7](#37-the-item-branch-and-the-latest-trunk) |
| The failover budget | A backend move, or a wait naming the backend | Rows 16 to 18 |
| One triage run per version of a request | A triage node; an unusable result returns to it | Rows 3 and 6 |
| Invalidating an in-flight workflow when the block changes | Nothing. Fixed nodes do not change, and the edit is no fact | [Section 5.2](#52-permitted-facts) |
| A stalled full-suite run marked red on its second stall | An exception; the proof resumes | [Section 8.2](#82-proof-and-a-red-trunk) |
| A blocked fixer report sent to the owner | The mechanic objects and names its next actor | [Section 8.2](#82-proof-and-a-red-trunk) |
| Classifying a run from its exit code and its log's tail | A stated ending | [Section 3.3](#33-endings) |

### 12.4 Audit findings

An operator's audit of the drain on 2026-11-17 listed every mechanism that acts on a count, a clock or a guess. It is not a tracked document, so its findings are restated here with what this design does to each. Severity A can strand work or burn expensive runs; B is noise or owner load; C is harmless.

| Finding | What it is | Under this design |
| :--- | :--- | :--- |
| F-01 A | Re-admission on a whole-input hash | Gone |
| F-02 A | Second failure goes to the design seat, then the owner | Gone |
| F-03 A | First failure of any kind parks the item for the owner | Gone |
| F-04 A | Failover budget of one, then `run-infra-exhausted` | Gone |
| F-05 A | Red cap: 20 landings or 60 minutes past green | Gone |
| F-06 A | A second stall of the full suite is red and holds the queue | Gone |
| F-07 A | An in-flight workflow invalidated when the block or fence hash changes | Gone |
| F-08 A | A rewrite item per item with prose findings | Gone |
| F-09 A | One continuation for uncommitted work, then `candidate-no-op` | Gone |
| F-10 A | Attempt class inferred from an exit code and a log tail | Gone |
| F-11 A | A two-minute wall on the question index holds every item with a design document | Not covered. The question index becomes a fact source, and its failure should be an exception with a mover; that is a separate fix |
| F-12 B | One conflict-fix run, then `conflict-exhausted` | Gone |
| F-13 B | One triage run per version of a request | Gone |
| F-14 B | A gate run killed after 60 silent seconds, rerun once, then `gate-stalled` | Partly. The stall detector stays as liveness. The rerun count and the owner hold go: a stall is an exception |
| F-15 B | `candidate-record-mismatch` held for the owner | Not covered. A guard around a bug, to be fixed at its cause |
| F-16 B | A lane out of rotation, rechecked every ten minutes | Not covered |
| F-17 B | Recovery of claims that never launched | Partly. A claim is one transaction; the legacy repair stays until old rows are gone |
| F-18 B | The held-candidate lane state machine | Partly. A worktree that belongs to its item and a pinned item branch remove its reason to exist. Not verified against the code |
| F-19 B | A run convicted from before-and-after snapshots | Not covered |
| F-20 B | A rewritten trunk holds all landings for the owner | Not covered. It reaches the owner with no objection naming him, which R3 forbids; it needs its own design |
| F-21 B | Stale-base detection by a hash of the block | Gone |
| F-22 B | Prose limits park work; the criteria count goes to the owner | Gone. Findings feed the rewrite node and none routes to the owner |
| F-23 B | The whole queue held while any fixer is open | Gone |
| F-24 B | Infrastructure backoff on elapsed time | Not covered. It is pacing of exceptions and fails nothing |
| F-25 B | An unrecognized hold reads "needs a cause" and names the owner | Gone. A wait without a mover fails the table's check |
| F-26 B | Rolling cadence by landings or minutes | Inactive under this repository's strategy; not covered |
| F-27 to F-32 C | Quota probe ages, shadow-tool and cleanup timeouts, capacity limits, the compaction threshold | Not covered. None fails or re-routes work |

### 12.5 What is reused

From the built graph code, as the scouts read it on 2026-11-17:

- **Kept as they are:** the template loader, expander and check; typed template arguments; the check and record of declared outputs; the command registry and command nodes; each node run's own directory; the group that lets read-only nodes share one lane; the wiring of an evaluator node, which becomes the judge node.
- **Kept and generalized:** the proposal transaction, which already applies one change to nodes that have not started, against the graph it read, in one write, and records refusals. It is the pattern every transformation follows. The append-only revision record is the graph's history.
- **Kept as plumbing:** the session resume built for restarts and for comment runs; the record of each document's last designer session, which design answers fork; the refs that pin a run's head; the definition package's hook shape, which carries another definition's policy function.
- **Deleted:** the single-run path beside the graph executor; the walk that runs nodes in a fixed order until an exit condition; the hand-off from the graph's exit to the old admission path; identity by attempt.

### 12.6 What this does not cover

- **How ideas enter.** [Intake paths](./tillr-intake-paths.md) owns the doors. This design gives each door's steps a place (rows 2 and 3) and settles nothing else there.
- **The lander's internals.** A land attempt is a node; how the lander squashes, binds verdicts and normalizes authorship is unchanged and stays with [the landing principles](./landing-principles.md).
- **The fast gate's checks and budgets.** A gate run is a node; what it runs is the gate rework's.
- **Review-comment items and designer desks.** They stay separate owner-facing deliverables.
- **Spend ceilings.** A budget is a target that is shown. Nothing here enforces one.
- **A worktree's lifecycle, its bound and its collection.** [The vocabulary](./tillr-vocabulary.md#3-what-a-lane-was-and-the-worktree-as-a-resource) holds those.
- **Which session writes an answer the owner gave in a Vantage review into the document.** Vantage never writes the repository, so an agent does; which one, inside tillr, is not designed.
- **Per-file build detail.** None is given. No implementation plan is seeded by this document.

---

## 13. Migration order

The running drain keeps working at every step. Two principles order the work.

**Record first, route later.** Each act becomes a node in the record before the rule table decides anything about it. The item page shows the whole path early, and each routing rule then moves into the table alone, with the old mechanism deleted in the same item.

**Largest waste first.** After the base, mechanisms move in by the runs they waste. Measured on 2026-11-17: 65 conflict-fix runs; 30 runs of 22 rewrite items; 11 judge runs through the bracket; and design conflicts, each of which discards a warm worker session and starts a new one from the trunk.

| Step | What moves | Waits on |
| :--- | :--- | :--- |
| 1 | Every item runs on the graph executor as a one-node template; the single-run path is deleted | nothing |
| 2 | Stated endings; exceptions logged apart from endings | step 1 |
| 3 | The policy function and the store's checks, with a table that reproduces today's routing. The checks are pure functions behind [the storage design's interface](./tillr-storage.md#6-the-interface-the-rest-of-tillr-sees), and from here on graph state is written only through it | step 2 |
| 4 | Take-in, gate run and land attempt recorded as nodes | step 3 |
| 5 | A landing or take-in conflict returns to its producer | step 4 |
| 6 | The prose rewrite is a node; definition edits live on the item branch | step 4 |
| 7 | A gate refusal returns to its producer | step 4 |
| 8 | An objection goes to a design answer on the item branch and back | step 6 |
| 9 | Backend move; identical repeat and diagnosis; the old count and hash paths deleted | steps 5, 7, 8 |
| 10 | The judge and triage are nodes | step 4 |
| 11 | The item page draws the graph | step 4 |
| 12 | Proof nodes, attribution, the waiting rule with its "passed, with inherited failures" verdict and its comparison of paths, the removal of the red cap, revert and mechanic. The revert is built ahead of this step, as a drain rule | step 4. The waiting rule is ruled ([OQ-GP7](#OQ-GP7)) |
| 13 | Observed children for pi, then dispatched children | step 3, [OQ-GP5](#OQ-GP5) |
| 14 | The cut; a question and its documents land ahead of the item; the owner question node, its surface and the answer's return | step 8, [early design landing](./tillr-early-design-landing.md) |
| 15 | The queue planner | step 9, [OQ-GP4](#OQ-GP4) |
| 16 | The storage structure behind the interface: one writer, then a log per item, as [the storage design](./tillr-storage.md#92-the-order-against-the-graph-designs-sixteen-steps) stages them | step 3; [the storage design's first question](./tillr-storage.md#OQ-TS1) |
| 17 | Every design answer's documents land when it ends | step 14, [early design landing](./tillr-early-design-landing.md) ruled "always" |

**State that already exists.** Runs recorded before step 3 are imported as done nodes, one per run in order, with endings mapped from their records and marked as imported. An imported ending with no successor is answered by the rule table like any other, so items parked under today's holds resume; the running-agent limit bounds how fast. A session that no longer exists becomes a backend move by row 18.

### 13.1 Drafted items

These are drafted items: proposals in prose, written on 2026-11-17, and none is filed. Nothing reads them, and nothing can be dispatched from them. How a drafted item becomes a queue item is under the owner's review, so no filing step is stated here. Two of them, on a gate refusal and on a landing conflict, describe as "today" what the drain has since changed outside the graph ([Section 12](#12-what-this-replaces)); a planner reads the tree again before any is filed. The later steps are drafted when their questions are ruled.

#### Move every item onto the graph executor

The graph machinery is built and no live work uses it: on 2026-11-17 no item named a workflow and none of 801 runs was a node run. An item with no workflow is supposed to be a graph of one node, and in code it is a second executor with its own copy of every rule. This item makes the sentence true. Every item runs as nodes, the one-node case is an ordinary tracked template, and the second executor is deleted so that each later rule is written once.

- **Paths:** `tools/tillr-drain/`, `tools/tillr/settings/`, `config/tillr/`, `.tillr.toml`, `yolo-packs/tillr/`, `docs/`
- **Waits on:** nothing.
- **c1:** A tracked one-node template exists for the build seat and one for the design seat, and the repository's settings name them as each seat's default. The workflow check accepts both.
- **c2:** An item whose block names no workflow runs as one node run, recorded as a node. A test dispatches a fixture item and asserts the node run and its outcome.
- **c3:** No run is settled outside the graph executor. A test fails when a dispatch decision carries no workflow, and the trunk-fixer path runs through the same executor.
- **c4:** A one-node item gets the same brief, the same continuation after a wall, the same failover and the same landing as before. A test compares a fixture pass before and after.
- **c5:** A store written before this item opens and drains. A test opens a fixture store holding runs from the single-run path and dispatches the next item.

#### Add a stated ending to every node run

A run's result is read today from an exit code and a word near the end of its log, and a killed run and a run that gave up can look alike. Every later rule in this design routes on why a node ended, so the reason has to be written down by the party that knows it. This item makes each run state its ending and keeps interruptions out of that record.

- **Paths:** `tools/tillr-run/`, `tools/tillr-drain/`, `yolo-packs/tillr/`, `docs/`
- **Waits on:** Move every item onto the graph executor.
- **c1:** Each agent node's run leaves an ending record with a cause from its kind's list, its evidence and, for an objection, its next actor. A test asserts the record for a submitted run, an objecting run and a run whose backend closed.
- **c2:** A run whose process ended by itself with no ending record ends `missing-output`. A run whose process was stopped has no ending and resumes under the same run id. A test asserts the two cases differ.
- **c3:** Each interruption of a node is written to an exception record naming the node, what was running and what was observed. It does not appear among the node's endings. A test kills a fixture run twice and reads two exception records and one ending.
- **c4:** A dirty worktree, a change outside the write scope, a missing declared output or a missing required commit ends the node `refused-by-check` with the check's output. A test asserts each of the four.
- **c5:** No code decides a run's result from the text of its log. A test runs a fixture whose log ends with a result word and whose ending record says otherwise, and asserts the record wins.

#### Add the policy function and the store's fog checks

What happens after a node ends is decided today in many places, each with its own count or hold. This item adds the one function that reads an item's graph and says which nodes it lacks, and the store checks that keep anything already started from changing. Its first rule table reproduces today's routing exactly, so the item changes where decisions are made and not what they are.

- **Paths:** `tools/tillr-drain/`, `tools/tillr/store/`, `config/tillr/`, `yolo-packs/tillr/`, `docs/`
- **Waits on:** Add a stated ending to every node run.
- **c1:** The function takes a graph and a set of facts and returns a transformation and a set of waits. A test evaluates it with the clock, the network and the file system unavailable.
- **c2:** The rule table is a tracked file with a fixture for every row, and loading it is refused when a row has none or when two rows match one ending.
- **c3:** For every fixture, applying the result and evaluating again returns nothing.
- **c4:** The store refuses each transformation this design lists as refused, records the reason, and leaves the graph unchanged. A test asserts each refusal.
- **c5:** Of two transformations computed from one revision of a graph, exactly one is applied. A writer killed inside the write leaves the old graph or the new one, with its event present only in the second case.
- **c6:** Every wait names a mover. Loading a table with a wait that names none is refused.
- **c7:** Runs recorded before this item are imported as done nodes in order, and a fixture store from before the change evaluates without a fault.

#### Add take-in, gate runs and land attempts to the graph

An item's page shows its build and nothing of what the gate and the lander did to it afterward. This item records each of those acts as a node with its ending, so the path is whole, while the existing programs go on doing the work exactly as they do now.

- **Paths:** `tools/tillr-drain/`, `tools/tillr-view/`, `yolo-packs/tillr/`, `docs/`
- **Waits on:** Add the policy function and the store's fog checks.
- **c1:** Each gate run is a node whose ending is `pass` or `refused` with the failing check ids. Each landing attempt is a node whose ending is `landed` with the trunk commit or `conflict` with the paths. A test asserts both for a fixture item.
- **c2:** Before a node that needs the worktree starts, when the trunk has moved, a take-in node brings the trunk under the item's commits and ends `clean` or `conflict`. A test moves a fixture trunk between two nodes and asserts the node and the new head.
- **c3:** When a node ends, the item's worktree stays the item's. It may be collected, and the item's commits are then restored from the store when its next node needs a worktree. A test runs two nodes of one item with a collection between them.
- **c4:** The gate and the lander are the same programs with the same arguments as before. A test compares their recorded command lines before and after.

#### Route a landing conflict back to the session that wrote the work

When a landing conflicts, a new run is started today to fix it, once, and a second conflict parks the item. There were 65 such runs by 2026-11-17, each starting cold on work another session wrote. This item sends the conflict to the session that wrote the conflicting change, on the latest trunk, as often as it takes.

- **Paths:** `tools/tillr-drain/`, `config/tillr/`, `yolo-packs/tillr/`, `docs/`
- **Waits on:** Add take-in, gate runs and land attempts to the graph.
- **c1:** A landing or take-in that ends `conflict` is followed by a resume of the session that last wrote the conflicting paths, with the paths and the trunk commit in its message. A test asserts the session id and the message.
- **c2:** A second and a third conflict are routed the same way. No state named for exhaustion exists, and a test conflicts a fixture three times and lands it on the fourth.
- **c3:** When that session cannot be resumed, a new session on the same seat is given the transcript's location and the item's commits, and the reason is recorded. A test removes the transcript and asserts the reason.
- **c4:** The conflict-fix run and its run-id suffix are deleted. A test fails if a run id carries the suffix.

#### Move the prose rewrite into the item's own graph

An item filed without the writing guide's mark gets a second item to rewrite it, with its own lane, gate and landing, and the original waits: 22 such items and 30 runs by 2026-11-17. The rewrite is a small job on the item's own text. This item makes it the first node of the item's graph, on a cheap seat that reads only the block and the guide, and lets its edit ride with the item's one landing.

- **Paths:** `tools/tillr-drain/`, `tools/tillr-item/`, `config/tillr/`, `yolo-packs/tillr/`, `docs/`
- **Waits on:** Add take-in, gate runs and land attempts to the graph.
- **c1:** An item whose block has prose findings gets a rewrite node before its template's nodes, on the seat the policy declares for prose. No separate rewrite item is created. A test syncs a fixture block with findings and asserts one item and its first node.
- **c2:** The rewrite node's brief holds the block and the guide and no other file. A test asserts the brief's contents.
- **c3:** The item's own block is inside the rewrite node's write scope even when its document is outside the item's fence. A rewrite that changes a criterion id or the fence ends `refused-by-check`. A test asserts both.
- **c4:** The nodes after the rewrite read the rewritten block from the item's commits, and the store shows the definition in force with the node that revised it beside the definition on the trunk. A test asserts the two differ until landing.
- **c5:** The rewritten block reaches the trunk in the item's one landing, and a rewrite of an item that never lands reaches the trunk never.

#### Route a gate refusal back to the session that wrote the work

A gate refusal parks the item today until an input hash changes or a person retries it, and nothing tells the worker what failed. This item gives the gate's output to the session that built the candidate and lets it submit again, without a count.

- **Paths:** `tools/tillr-drain/`, `config/tillr/`, `yolo-packs/tillr/`, `docs/`
- **Waits on:** Add take-in, gate runs and land attempts to the graph.
- **c1:** A gate run that ends `refused` is followed by a resume of the producing session with the failing checks and their output. A test asserts the session id and the message.
- **c2:** A refused item is under "draining" and names its resume as its next step. It is never shown as needing the owner. A test asserts the waiting face.
- **c3:** A fifth refusal is routed as the first was. A test refuses a fixture five times with five different failures and passes it on the sixth.
- **c4:** A changed item block, fence or policy file re-runs nothing. A test edits each while a fixture item waits and asserts no new node.

#### Route an objection to a design answer on the item's own branch

When a worker reports that the design is unclear, the design seat answers today in a separate run that lands by itself, and a new worker is then cut from the trunk, so the answer can be newer than the worker's base and the first worker's session is lost. This item has the designer commit its answer where the worker stopped, and the same worker continue from that commit.

- **Paths:** `tools/tillr-drain/`, `tools/tillr-item/`, `config/tillr/`, `yolo-packs/tillr/`, `docs/`
- **Waits on:** Move the prose rewrite into the item's own graph.
- **c1:** A node that ends with an objection naming the design seat is followed by a design answer node that starts at the objector's head and commits there. A test asserts the answer's parent commit.
- **c2:** When the design answer ends `answered`, the objecting session is resumed from the design's commit with the answer. No new worker session is started. A test asserts the session id.
- **c3:** The design answer's commit reaches the trunk only in the item's landing. A test asserts the trunk does not hold it while the item is unlanded.
- **c4:** Two items that edit different blocks of one file both land. Two that edit the same lines produce a `conflict` that is routed to the design session that wrote them. A test asserts both cases.
- **c5:** An objection that names no next actor, or names the owner without a question, is returned to its session as `missing-output`. A test asserts both.
- **c6:** The conflict rows, the separate design landing and the design-run counting are deleted.

---

## 14. Risks

| Risk | What is done about it |
| :--- | :--- |
| **A chain that never repeats and never objects** runs without limit | Shown at the top of "draining", which is ordered by spend; the owner can object at any time. There is no spend target. See [Section 7](#7-what-stops-repetition) and [OQ-GP3](#OQ-GP3) |
| **Resumed sessions grow** until each turn is expensive | The scheduler may compact before a resume; a diagnosis may name a fresh start on a stronger seat. Cost is recorded per node, so the growth is visible |
| **The rule table becomes a second program** as rows accumulate | Every row has a fixture, rows may not overlap, and a row can read only the graph and the listed facts. A row that needs a new fact is a design change |
| **Adapters that cannot state an ending** leave everything `missing-output` | The first slice keeps today's footer as one source of the ending record for each adapter, replaced adapter by adapter |
| **A design answer on a branch conflicts at landing** with another item's | It is routed to the design session, and its objection adds the dependency. The queue planner exists for the cases worth preventing |
| **The waiting rule is only as good as each check's declared inputs** | A check with no declared inputs is treated as reading the whole tree, which holds more, never less |
| **A revert removes a landing that was right**, because a check is wrong | A revert lands only when the failing check passes with it, and a red at the green pointer is never attributed to a landing |
| **Hoisting changes fork behavior** and a pi upgrade breaks the reader | Observed nodes degrade to "not recorded" for that run; nothing routes on them |
| **Migration releases every parked item at once** | The running-agent limit bounds it, and the release is the point: those items were parked by the mechanisms being removed |

---

## Decision Ledger

The `OQ-GP` rows record what the owner said, or, for [OQ-GP2](#OQ-GP2), why a question was closed without a ruling. The `D-GP` rows are the designer's decisions of 2026-11-17, made so that the proposal is complete. None of them is the owner's ruling; the owner may overturn any of them, and all await his review except the waiting rule of `D-GP14`, which the owner ruled on 2026-11-18 as [OQ-GP7](#OQ-GP7). `D-GP1`, `D-GP2`, `D-GP3`, `D-GP5`, `D-GP7`, `D-GP8`, `D-GP9`, `D-GP10` and `D-GP12` carry out a direction quoted in [Section 1](#1-the-direction-and-the-rules); `D-GP4`, `D-GP6`, `D-GP11`, `D-GP13`, `D-GP14` and `D-GP15` are the designer's own. The Built column was read from the tree on 2026-11-18.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-GP1` | An item has one graph for its life, and every act on it is a node. "Attempt" is retired. **Rejected:** a new graph for each attempt chosen by the last one's cause, which keeps the gate and the landing outside the graph and throws finished nodes away at each reset. | 2026-11-17 | [Section 3](#3-the-model) | — |
| `D-GP2` | A node is in fog, then fixed at its claim, then done at its ending. Liveness bookkeeping is never fixed while it runs. **Rejected:** fixing a node only when it emits output, which leaves a window in which a running process can have its definition changed. | 2026-11-17 | [Section 3.2](#32-what-is-fixed-and-when) | — |
| `D-GP3` | An exception resumes the same node and is logged; an event adds nodes. A process that ends by itself and states nothing is an event. **Rejected:** a node for each interruption, which fills the path with noise the owner ruled out. | 2026-11-17 | [Section 3.6](#36-events-and-exceptions) | — |
| `D-GP4` | "Read" means recorded as a premise, and the store checks premises in the write that applies a change. **Rejected:** treating everything a process loaded as read, which cannot be known and would freeze the whole graph. | 2026-11-17 | [Section 4.1](#41-premises) | — |
| `D-GP5` | All routing is one pure function per definition, returning a transformation and waits. Waits are derived and never stored. **Rejected:** conditions on edges, which hide routing in templates and need a second language. | 2026-11-17 | [Section 5](#5-the-policy-function) | — |
| `D-GP6` | Scheduling is apart from policy. A node names a seat; seats are declared in the backend policy; no rungs. **Rejected:** a model or backend field on a node, which breaks one-edit provider changes. | 2026-11-17 | [Section 6](#6-scheduling) | — |
| `D-GP7` | The gate, the judge and the landing are nodes. The lander stays the one program that moves the trunk. **Rejected:** keeping them as a bracket at the graph's exit, which is why refusals and conflicts have no producer to return to. | 2026-11-17 | [Section 3.1](#31-node-kinds) | — |
| `D-GP8` | Every node that needs the worktree starts on the latest trunk, by a take-in that is itself a node. A conflict is routed, never forced. **Rejected:** taking the trunk in whenever it moves, which is a trigger. | 2026-11-17 | [Section 3.7](#37-the-item-branch-and-the-latest-trunk) | — |
| `D-GP9` | Definition edits are committed on the item branch, and the worker resumes from them there; the definition in force is the latest revision recorded in the graph; design answers and triage may change criteria and the fence, and rewrites and work may not. A copy of a document may land ahead of the item. **Rejected:** cutting a new worker from the trunk after a design lands by itself, which is the source of stale workers. | 2026-11-17 | [Section 3.7](#37-the-item-branch-and-the-latest-trunk) | — |
| `D-GP10` | An objection is the one ending that changes the actor, and it names the next one. The owner is named only with a written question. **Rejected:** any default mover. | 2026-11-17 | [Section 3.3](#33-endings) | — |
| `D-GP11` | An identical repeat, by cause and evidence key against the whole chain, is answered by a diagnosis that must change something. **Rejected:** comparing only to the previous ending, which a two-step alternation defeats. | 2026-11-17 | [Section 7](#7-what-stops-repetition) | Partly: the drain compares an ending with every earlier ending of the item (`repeatedEnding`). No diagnosis node |
| `D-GP12` | A sub-agent tillr can see is a node within its parent: observed when tillr only recorded it, dispatched when tillr started it. A child's scope is inside its parent's. **Rejected:** ending the parent's node at every spawn and resuming it after, which is purer and pays a suspend and a resume for every child. | 2026-11-17 | [Section 9](#9-subagent-hoisting) | — |
| `D-GP13` | The queue planner owns one class of inter-item edge and the rank. Its edges constrain only future claims. It runs on events that cross items. **Rejected:** a planner that may pause or re-plan running work. | 2026-11-17 | [Section 8.1](#81-the-queue-planner) | — |
| `D-GP14` | Only an ended proof makes the trunk red. A candidate waits only when it changes the inputs of a failing check and does not make it pass. Proof outranks new work. **Rejected:** the red cap, which holds on elapsed time and on "unproven"; and no rule at all, which lets changes land into a failing check's inputs. The waiting rule was put to the owner as [OQ-GP7](#OQ-GP7), and he ruled it on 2026-11-18. | 2026-11-17 | [Section 8.2](#82-proof-and-a-red-trunk) | — |
| `D-GP15` | Migration records each act as a node before the rule table routes it, and moves mechanisms in by the runs they waste. **Rejected:** building the whole model beside the drain and switching over. | 2026-11-17 | [Section 13](#13-migration-order) | — |
| `OQ-GP1` | **Owner ruling.** tillr reverts a landing by itself when the full suite proves it broke the trunk: the revert lands once it applies cleanly and the failing check passes with it, and the session that wrote the change gets the failure back. A mechanic is filed only when a revert is unsafe, does not repair, or the red is unattributed. This overturns `D-GR6` of the gate rework. | 2026-11-17 | [Section 8.2](#82-proof-and-a-red-trunk) | Yes, as a drain rule: `auto_revert` is on, and five reverts ran on 2026-11-18. Not yet as nodes |
| `OQ-GP2` | **Closed as superseded; not a ruling on the structure.** The question asked whether one process should own every write to the store, and argued from a write lock held while git moves a ref. That argument was wrong: the lock held across git is a usage fault that one owner would not remove, and it is being fixed at its cause. The owner directed on 2026-11-17: "do the right thing here, not the easy thing." [The storage design](./tillr-storage.md) was written to that direction and holds what is left of the choice, as [its first question](./tillr-storage.md#OQ-TS1), which is blocked until its trials are run again. This design depends only on that document's interface. | 2026-11-18 | [Section 10](#10-storage) | — |
| `OQ-GP3` | **Owner ruling.** There is no spend target. Spend is recorded for each node and orders the "draining" list; nothing acts on it. | 2026-11-17 | [Section 7](#7-what-stops-repetition) | Partly: spend is measured without stopping dispatch. The ordering of the list was not checked |
| `OQ-GP4` | **Owner ruling.** The queue planner's agent runs on evidence that crosses items (a conflict between items, a worker naming another item) and on the owner's request, not at intake. | 2026-11-17 | [Section 8.1](#81-the-queue-planner) | — |
| `OQ-GP5` | **Owner ruling.** tillr watches every sub-agent it can see and starts scouts and writers itself; the rest are watched only. | 2026-11-17 | [Section 9](#9-subagent-hoisting) | — |
| `OQ-GP6` | **Owner ruling.** A question for the owner lands on the trunk with the design text that changed: "we definitely need to land the questions and the designs that changed when they have questions for me." The operation is designed in [early design landing](./tillr-early-design-landing.md). Whether every design answer lands this way was left open ("We may even want to do this all the time") and is [that document's first question](./tillr-early-design-landing.md#OQ-EL1), which the owner ruled "always" on 2026-11-18; the amendments that ruling owed to this design are made (R11, rule rows 12 and 27, the dependent-item row of [Section 3.7](#37-the-item-branch-and-the-latest-trunk) and migration step 17). | 2026-11-17 | [Section 3.7](#37-the-item-branch-and-the-latest-trunk) | — |
| `OQ-GP7` | **Owner ruling.** When a check is already red on the trunk, finished work that did not cause it lands only if it does not touch what the red check reads. Each check declares the paths it reads; an item whose changes fall outside them lands, and one that changes them waits unless it makes the check pass. There is no 20-landing stop. A waiting item is shown with the paths that put it there. The owner took the stated leaning in review: "C, weakly: it lets through exactly the items that cannot have made the red worse, so nothing is hidden and no 20-landing stop is needed; it is weak because a red on one of the eight whole-tree checks still holds the queue, and only B moves it then." To build: a verdict "passed, with inherited failures" that the gate, the loop and the lander agree on, and the comparison of an item's changed paths with the red check's declared paths. A red on a whole-tree check still holds every item. This is the rule of `D-GP14`, now his. | 2026-11-18 | [Section 8.2](#82-proof-and-a-red-trunk) | — |

---

## Open Questions

None open. [OQ-GP7](#OQ-GP7) was ruled by the owner on 2026-11-18 and is a row of the ledger above.
