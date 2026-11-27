---
title: "The item graph is the whole path, and one pure function decides what comes next"
status: accepted
stage: DECIDED
updated: 2026-11-27
tags:
  - tillr
  - graphs
  - dag
  - drain
  - policy
  - landing
  - design
summary: "Every act on a tillr item (build, rewrite, design answer, judge, join, gate, landing, revert, owner question, sub-agent) is a node in that item's graph. Execution data belongs to its node, a node is fixed once it starts and done once it ends, and one pure function over the graph and a short list of facts adds whatever nodes are needed next. An item is scheduled against the running-agent limit and acquires its own worktree; parallel writing nodes run in per-node checkouts that join onto the item branch."
next: "Build the running-agent limit and item-owned worktrees, then the policy-function migration steps of Section 13."
depends-on:
  - ../reference/tillr-item-graphs.md
  - ../reference/tillr-drain-successor.md
  - ../reference/gate.md
  - landing-principles.md
---

# The item graph is the whole path, and one pure function decides what comes next

**Status:** 2026-11-27. Every question is ruled. Built: the static workflow machinery ([item graphs](../reference/tillr-item-graphs.md)), with writing nodes serialized by `claimParallelWithShut` in [`tools/tillr-drain/drain/parallel.go`](../../tools/tillr-drain/drain/parallel.go); item-owned worktrees, count/disk bounds and the Observatory's Running work view (slices 2–4 of [Section 13](#13-migration-order)); and, as rules of the drain with no node behind them, the routing rules listed in [Section 12](#12-what-this-replaces), including automatic revert and the waiting rule for a red trunk. Not built: the whole-life graph (gate, judge, landing and later acts as nodes), the policy function, the store's fog checks, the running-agent limit, per-node writer checkouts and item-branch joins ([`D-GP16`](#D-GP16), [`D-GP17`](#D-GP17), [`D-GP19`](#D-GP19), [`D-GP20`](#D-GP20)), subagent hoisting and the refusal for a check the trunk no longer has ([`D-GP18`](#D-GP18)). In [the ledger](#decision-ledger), `OQ-GP` and `R-GP` rows are owner rulings and `D-GP` rows are the designer's engineering decisions, which the owner may overturn.

> **In short.** An item's graph stops being a template for its build and becomes the record of everything that happened to it. Nothing routes work except one pure function that reads the graph and adds the nodes it lacks, so every "what next" is a table row with a test, and every repetition has a stated cause.

**Why it matters.** The built graph ends before the gate. What follows the gate (refusals, conflicts, design conflicts, rewrites, a red trunk) is routed by separate rules of the drain, each with its own state, so no one place says what happens next or why.

**The shape.** Four parts: a per-item graph of nodes with stated endings and execution data belonging directly to each node; the fog-of-war invariant (a node that has started can never change), which the store enforces; a pure policy function that transforms only what has not started; and a scheduler that chooses who runs each ready node against the running-agent limit while each item owns its branch and worktree (and concurrent writers run in per-node checkouts that join back onto the item branch). A queue-level pass and the trunk's proof sit above the items under the same invariant.

**Cost.** It overturns seven rows of the item-graph ledger, including the rule that a sub-agent is never a node (`D-IG11`, `D-IG12`) and the rule that writing nodes of one item run strictly one at a time ([`OQ-IG3`](../reference/tillr-item-graphs.md#why-its-this-way), overturned by [`R-GP1`](#R-GP1)). It drops the words "attempt" and "lane", the numbered lane pool and the hold vocabulary. It needs each adapter to state how its node ended instead of leaving that to a reading of the log. There is no landing cap ([OQ-GP7](#OQ-GP7)).

**Start at [Section 5.3](#53-the-starting-rule-table)**, the rule table. The model before it exists to make that table safe, and everything after it is a consequence.

**Needs your ruling:** None.

**Reads with:** [item graphs](../reference/tillr-item-graphs.md) (the built graph machinery this extends and partly overturns), [the drain's successor function](../reference/tillr-drain-successor.md) (the earlier pure function this splits in two), [the core and the landing definition](./tillr-core-and-git-landing.md) (why the rule table belongs to a definition), [the gate reference](../reference/gate.md) (the full-suite tier, the green pointer and the revert setting), [the landing principles](./landing-principles.md) (rulings a landing change is checked against), [early design landing](./tillr-early-design-landing.md) (how a question and its design reach the trunk ahead of their item), [the landing call](./tillr-landing-call.md) (how a worker states its ending), [the storage design](./tillr-storage.md) (the structure behind the store), [the vocabulary](./tillr-vocabulary.md) (one meaning for item, node, run, session and worktree; [Section 6](#6-scheduling) carries its text on worktrees).

---

## 1. The direction and the rules

The owner's direction, restated:

- **Everything is a node.** The item graph shows the entire path an item took: judging, landing, rewrites, gate runs, failures and successes. A function runs over the graph, often and cheaply, and adds whatever nodes are required. When a worker thinks it is done, a gate node is added after it; when the gate ends, the function runs again and sends the work to landing or back to the session that produced it.
- **A node cannot be restarted.** A resume node continues an earlier node's session from where it stopped, and a fork node starts from a copy of it.
- **Fog of war.** Any transformation of the graph is possible except one that changes something already acted on. There are no loops, and a done node stays done; its assets may be reused.
- **A node's definition is fixed as it starts.** Crash recovery resumes the same node. New nodes are created for events, not for exceptions; exceptions are logged for later analysis.
- **Sub-agents are hoisted into nodes** wherever the harness allows.
- **A design answer commits on the item's branch**, so the worker that asked resumes from it and the answer cannot be out of date for that worker.
- **A red trunk has a full remedy:** reverting what broke it, and a high-priority mechanic item when a revert is not the answer.
- <a id="R-GP1"></a>**[`R-GP1`](#decision-ledger).** Execution data belongs to the node: an item has a graph, the graph has nodes, and each node holds the data about what it executed. Whether writing nodes run in parallel is up to the item's plan.
- <a id="R-GP2"></a>**[`R-GP2`](#decision-ledger).** A lane has no life of its own. An item is scheduled, it runs, and its execution has a worktree.

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
| **R8** | Work is built against the latest trunk, never a past commit. A design that does not fit the trunk is a redesign, stated as one. Items that churn the same ground get dependencies. By [gate reference `D-GR24`](../reference/gate.md#why-its-this-way), a new item starts from the last proved trunk commit, and only after its blocker's landing is proved; a trunk fixer starts from the tip. See [Section 3.7](#37-the-item-branch-and-the-latest-trunk). |
| **R9** | A pass over the whole queue may fix dependencies and order that no single item can see, without becoming a guess trigger. |
| **R10** | The red cap is not accepted. Reds must not pile up, and there must be a way to pull things out and land them. An unproven trunk is never treated as red. |
| **R11** | A design answer, a rewrite and a triage commit on the item's own branch. The item's deliverable reaches the trunk in one landing. A document that a design answer changed, or that carries a question for the owner, reaches it earlier, by an early landing ([early design landing](./tillr-early-design-landing.md)). |
| **R12** | Nothing depends on a git trace record. The shell-command record stays. |

---

## 2. The words

Each term is used throughout with exactly this meaning. Terms marked *(coined here)* are new in this document.

| Term | Meaning | What it is not |
| :--- | :--- | :--- |
| **Item graph** | The one directed, cycle-free graph an item has for its whole life. It holds every act taken on the item. | Not one graph per attempt. This design does not use the word "attempt". |
| **Node** | One act: one invocation of a harness, one command, one landing attempt, or one question put to the owner. It has a definition, a start and an ending. | Not a stage of a template. A template is expanded into nodes and then is gone. |
| **In fog** | The state of a node that has not started. "In fog" and "not started" are the same thing; [item graphs](../reference/tillr-item-graphs.md#the-words) already uses both. | Not "pending in a queue": a node in fog may still be changed or removed. |
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
| **Proof** | One run of the full suite on one trunk commit. The gate reference calls its checks the heavy tier. | Not the gate. The gate judges a candidate before it lands. |
| **Mechanic** | The owner's word for the high-priority item filed to make a red trunk green. The code calls it a trunk fixer. | Not a revert. A revert is a landing with no model in it. |
| **Observed node** and **dispatched child** *(coined here)* | A sub-agent that tillr recorded but did not start, and one that tillr started. Both are nodes inside their parent. | An observed node is never the producer of a checked input. |
| **Identical repeat** *(coined here)* | An ending whose cause and evidence key equal those of an earlier ending in the same chain. Defined in [Section 7](#7-what-stops-repetition). | Not "the second failure". Two different failures are not a repeat. |
| **Cut** and **early landing** | A cut is a command node that builds a documents-only candidate on the trunk tip; an early landing is the landing of that candidate before the item's deliverable. Both are defined in [early design landing](./tillr-early-design-landing.md#2-the-words). | A cut is not a rebase: it writes nothing to the item branch. |
| **Landing call** | The tool call by which a worker ends its run and offers its commit; a refusal comes back as that call's result. Defined in [the landing call](./tillr-landing-call.md#2-the-words). | Not a node. It is the stated ending of a work node. |

---

## 3. The model

### 3.1 Node kinds

Every kind is one of three executions: an agent (a harness invocation), a command (a tracked program with no model), or a person. The lander, the gate and the full suite stay separate programs; a node is the record and the scheduling of one run of them.

| Kind | Execution | What it does | Endings (the closed cause list) |
| :--- | :--- | :--- | :--- |
| **work** | agent | Builds or designs the deliverable on the item branch | `submitted`, `objection`, `backend-closed`, `missing-output`, `refused-by-check` |
| **rewrite** | agent, cheap seat, small context | Proposes wording that follows the writing guide without changing meaning or criteria ids; the worker may not edit the item comment | as work |
| **triage** | agent, design seat | Writes the block for an item that arrived with none | as work |
| **design answer** | agent, design seat | Answers an objection, or diagnoses an identical repeat, by amending the design on the item branch | `answered` (names the actor to resume), `objection`, and the three machinery causes of work |
| **judge** | agent, read-only | Evaluates a candidate it did not write | `pass`, `fail` with findings, and the machinery causes |
| **command** | command | Runs one tracked command from the registry | `pass`, `fail` |
| **join** | command | Three-way merges the committed trees of finished parallel writing nodes onto their shared item-branch frontier ([Section 3.8](#38-concurrent-writers-checkouts-and-the-join)) | `joined` with the new head, `conflict` with the base commit, sibling node heads and conflicting paths |
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

**Execution data belongs to the node, once for its life ([`R-GP1`](#R-GP1), confirming [`D-VO3`](./tillr-vocabulary.md#D-VO3)).** What the stored tables `runs` and `node_runs` (written by [`tools/tillr-run/`](../../tools/tillr-run/) and [`tools/tillr-drain/`](../../tools/tillr-drain/)) call a run is not a peer entity standing beside the graph: it is the node's own execution record (process segments, lease, log, declared outputs, ending and spend). Crash recovery adds a process segment on that same node under the same identity. From the graph, a node that was interrupted five times and one that never was look the same; the difference is in the node's segments and exception log.

### 3.3 Endings

Every node ends with an explicit cause. The rules that apply to all kinds:

1. **An ending is written, never inferred.** An agent node states its ending in a structured record the adapter reads. That record is written by the tool the worker calls to end: `land` for `submitted`, `obstacle` for an objection ([the landing call](./tillr-landing-call.md)). Where the adapter's ending capability (what it declares about how its harness can end a run) is `footer`, the adapter writes the record from the report's footer. No result is read from the tail of a log.
2. **A process that ends by itself and states nothing has ended `missing-output`.** That is an event: the worker stopped without saying why. It returns to the same session with a message naming what is missing.
3. **A process that was stopped has no ending.** That is an exception. The node resumes.
4. **tillr's own checks at the end of a node can refuse it.** A dirty worktree or a missing commit is answered inside the `land` call and ends nothing. A write to protected state, or a missing or malformed declared output, ends the node `refused-by-check` with the check's output; so do the first two on an adapter with no tool. It returns to the same session.
5. **An objection names its next actor.** One of: the design seat; the owner, with the anchor of the open question it wrote; another item, by id; the tooling, with evidence of the defect; a stronger seat, with what the worker tried and what it lacks. An objection that names none of these, or names the owner without a question, is `missing-output`. A worker's objections are the obstacle kinds of [`D-JL11`](./tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11). Its owner question reaches the owner through the design seat, which writes the question; the worker does not decide it.
6. **`backend-closed` is stated by the adapter** when the harness reports that the backend refuses work, with the reopening time when the backend gives one. It is never guessed from an exit code.

### 3.4 Assets and their reuse

- **A session** is reused by a resume (the same session continues, on the backend that holds it), a fork (a copy continues, and the original may be resumed or forked again), or not at all. A backend move reuses the transcript as a file to read, not as a session.
- **A commit** is reused by starting from it. A sequential writing node starts from the item-branch head the previous writer left, after a take-in; concurrent writing nodes placed in parallel by the item's plan start from their shared item-branch frontier commit in isolated per-node checkouts and reconverge at a `join` node ([Section 3.8](#38-concurrent-writers-checkouts-and-the-join)).
- **An output** is reused by an edge that names its producer and its name, as [item graphs](../reference/tillr-item-graphs.md#why-its-this-way) rules in `D-IG6`.
- **How a message is delivered to a resumed session** is an adapter capability: as the result of the suspended call where the harness supports it, otherwise as the session's next message. The model needs only that a session can be continued with one message.
- **A session that cannot be resumed** (its transcript is gone, or its backend is not in the policy) is a fact with a stated reason. The rule table turns a resume in fog into a backend move ([Section 5.3](#53-the-starting-rule-table), row 18).

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

**A new item starts from the last proved commit** ([gate reference `D-GR24`](../reference/gate.md#why-its-this-way), owner ruling, 2026-11-21). The first node of a new item starts on the last proved trunk commit (the green pointer, `refs/tillr/green`), and it does not start until that commit contains the landing of every blocker; a trunk fixer's first node starts on the tip. The ruling covers starting an item. What a later node of a started item takes in is the designer's `D-GP8`: the latest trunk, as this section states. The drain applies the start rule through the gate policy's `lane_base` setting; the take-in node is not built.

**A check the trunk no longer has is not the candidate's defect** (the designer's [`D-GP18`](#D-GP18), from a filed request; the owner may overturn it). On 2026-11-24 a repository worked on the pack's programs dispatched its first run 30 seconds after the loop started. The last proved commit still held tillr's source; the trunk tip was two commits ahead, unproved, and had removed that source and the `pack-sync` check with it. The candidate was built on the proved commit, gated with that tree's checks, and refused on `pack-sync`.

- **The start worked as ruled.** `D-GR24` puts a new item on the last proved commit and holds it only for a blocker's landing, and this item had no blocker. An unproved tip is not a reason to wait (R10).
- **The check set worked as built.** The gate judges a candidate with the declarations in the candidate's own tree ([the landing gate](../reference/gate.md#the-landing-gate)), and it runs once, without requiring the candidate to be on the tip ([landing principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once)). The owner's words in `D-GR24` are about where an item starts and say nothing about which tree's checks judge it.
- **The refusal was wrong.** It listed `pack-sync` as a failing check and nothing else, so it read as a fault in the change. The drain asks the tip about a failing check only after the check is red on the base (`trunkAlreadyRed` in [`trunk.go`](../../tools/tillr-drain/drain/trunk.go)), and a check the tip does not declare cannot be judged there (`revisionCheck` in [`tillr-loop`](../../tools/tillr-loop/loop/loop.go) answers "could not judge"), so neither path reported that the check was gone.

What the loop does instead: when a gate run ends `refused`, each failing check is looked up in the declaration directory of the trunk tip, read from the tip commit without a checkout. For a failing check the tip does not declare:

- **The refusal leads with the cause.** Before the list of failing checks it says, with the values filled in: "Check `pack-sync` failed on this candidate's tree, but the trunk tip `<tip>` no longer declares it. This run started from `<base>`, the last proved commit at the time, and the trunk has removed the check since. This is not a defect in the change." The check's output stays attached below as evidence.
- **The lane is taken onto the tip**, the way it is for a failure the trunk has repaired since the base (`moveLaneOntoTip`, `repairedAtTipDetail`), and the refusal ends by saying so: "The loop rebased this lane's commits onto `<tip>`, so the lane's base is now `<tip>`. Continue on the latest trunk." When the rebase conflicts the lane stays on its base and the refusal says that instead. The take-in node replaces this move when it is built.
- **No fixer is filed and nothing is held.** The trunk is not red, and the refusal returns to its producer uncounted (R4, row 9).
- **Other failing checks stay the candidate's.** When some failing checks are still declared at the tip, the refusal names the two groups apart: those the trunk no longer has, and those "still declared at the trunk tip and this change's to fix".
- **Doubt keeps the ordinary refusal.** A tip equal to the base, a declaration directory that cannot be read at the tip, or a failing id that cannot be matched to a declaration changes nothing: the check is treated as still declared.

**The first build slice** is one behavior: a gate refusal whose failing checks are all undeclared at the trunk tip returns to its worker with the lane rebased onto the tip and a refusal that opens with the sentence above, and files no fixer. Its test sits beside `TestAGateFailureRedOnlyOnAnOldBaseFilesNoFixer` in [`tools/tillr-drain/drain`](../../tools/tillr-drain/drain/). The mixed case, where some failing checks are still declared at the tip, is the second slice.

A running node is never interrupted because the trunk moved. The take-in happens only when a node is about to start, so it is not a trigger (R2).

**Definition edits live on the item branch (R11).** A rewrite, a triage or a design answer that edits the item's block or its design document commits that edit on the item branch. The worker resumes from that commit. The edit reaches the trunk in the item's landing, or earlier when its document goes ahead in an early landing ([early design landing](./tillr-early-design-landing.md)). A cut and its gate run are not preceded by a take-in: the cut is made on the trunk tip.

| Consequence | How it is settled |
| :--- | :--- |
| **Which definition is in force** | The one on the item branch. When a node that may edit the definition ends, its ending carries the block as revised, and the store records it against the item with the node's id. Later briefs, the judge, the gate and the verdicts use the latest revision in the graph. Sync still reads the trunk's documents for intake, and the store keeps both: "on the trunk" and "in force on the item branch, revised by node N" |
| **Who may change what** | A design answer may revise decisions in prose, not alter an item declaration. The common deny-list refuses an edit or a removal of an item comment from every worker, including a rewrite or triage. It refuses an addition except in the two turns whose outcome is a new declaration: a saved idea's turn, and a triage that declares the item it triages (the designer's [`D-VO13`](./tillr-vocabulary.md#D-VO13), which the owner may overturn) |
| **A trunk edit to the same block** | Arrives at the next take-in as an ordinary three-way merge. It re-runs nothing by itself (R2). If both sides changed the same lines the take-in ends `conflict`, and the design seat's session that wrote the block resolves it |
| **Shared files** | Most blocks share one roadmap file, and design documents are shared. Concurrent items' edits meet at the take-in before the gate and again in the lander's three-way merge. Disjoint blocks merge cleanly |
| **A real conflict in a design document** | Two items changed the same ruling. It goes to the design session that wrote it, which reconciles the two, or objects naming the other item. That objection is evidence that the queue-level pass should have ordered them, and it triggers that pass ([Section 8.1](#81-the-queue-planner)) |
| **A provisional design** | If the item is abandoned its branch never lands. Documents that went ahead in an early landing are on the trunk; what becomes of them waits on a design of how an item is given up ([early design landing](./tillr-early-design-landing.md#7-an-item-that-is-given-up)) |
| **A lasting ruling** | Goes in the design document's ledger, outside the item's block, on the same branch. Retiring the block at landing would otherwise delete it |
| **Another item that depends on the decision** | Reads it on the trunk once the design answer's documents land. An item that depends on the work, and not only on the decision, gets an inter-item edge until this item lands; the design answer's ending may name those items, and the queue level adds the edges. No item reads another item's unlanded branch |
| **A question for the owner** | Must be visible while the branch is unlanded. The question lands on the trunk together with the design text that changed and that the question is about, so the owner reads both in Vantage ([OQ-GP6](#OQ-GP6)). A cut of those documents is gated and landed ahead of the item, and the branch takes it in ([early design landing](./tillr-early-design-landing.md#3-the-operation)) |

### 3.8 Concurrent writers, checkouts and the join

**Why parallel writers are up to the item's plan ([`R-GP1`](#R-GP1), overturning [`OQ-IG3`](../reference/tillr-item-graphs.md#why-its-this-way)).** The built workflows (`claimParallelWithShut` in [`tools/tillr-drain/drain/parallel.go`](../../tools/tillr-drain/drain/parallel.go)) let read-only nodes share one checkout and run an item's writing nodes strictly one at a time. The model drops that blanket serialization: when an item's planner coordinates independent pieces (or, once Step 13 of [Section 13](#13-migration-order) lands, a parent node dispatches parallel child writers), the item's graph decides whether those writers are ordered with `after` edges or run concurrently. How concurrent writers get checkouts, declare piece dependencies, derive `just pack-sync` companions, join the item branch, and handle conflicts — both in the target whole-life graph and in pre-migration Slice 5 — is the designer's <a id="D-GP16"></a>[`D-GP16`](#decision-ledger), <a id="D-GP17"></a>[`D-GP17`](#decision-ledger), [`D-GP19`](#D-GP19) and [`D-GP20`](#D-GP20) below (not the owner's ruling, and the owner may overturn any of them):

- **Per-writer checkouts and disjoint node refs (`D-GP16`, amended by `D-GP19`).** Every item still owns **one item branch** pinned in the store at `refs/tillr/items/<item-id>` (`ItemPinRef` in `run_pin.go`, fallback `refs/tillr/items-long/<sha256>`; `D-IG2`, [`OQ-IG1`](../reference/tillr-item-graphs.md#why-its-this-way): one item, one deliverable, one squashed trunk commit) and **one primary item worktree** (`<lane-root>/<item-id>/worktree` on branch `tillr/<scope>/<item-id>` via `LaneBranch`, with `<lane-root>/<item-id>/scratch`). A writing node with no concurrent sibling writer in the graph runs directly in the primary item worktree on the item branch. When two or more writing nodes in the graph have no ordering edge between them (parallel `piece` stages proposed by `plan` in Slice 5, plus parallel dispatched `hoisted child` writers once Step 13 is built), claiming each node stages **its own isolated Git worktree** at `<lane-root>/<item-id>/nodes/<node-key>/worktree` (with node-scoped run scratch at `<lane-root>/<item-id>/nodes/<node-key>/scratch/<run-id>` and metadata beside it) on branch `tillr/<scope>-nodes/<item-key>/<node-key>` (`NodeLaneBranch`), cut at the shared item-branch frontier commit `<base>` left by their predecessors (after a single `take-in` if the trunk moved). `<node-key>` (`NodeKey(nodeID)`) is a single ref and path component formed by sanitizing `nodeID` (`[a-z0-9_-]`, collapsing `/` and other characters to `-`) and suffixing `-<sha256(nodeID)[:8]>` within `MaxRefComponent` bytes, and each concurrent writer commits to its own pinned node ref under the **disjoint sibling ref namespace `refs/tillr/item-nodes/<item-id>/<node-key>`** (`NodePinRef`, fallback `refs/tillr/item-nodes-long/<sha256(itemID + "\x00" + nodeID)>` when `itemID` fails `safeRefComponent`) — never under `refs/tillr/items/<item-id>` or `refs/heads/tillr/<scope>/<item-id>`, where Git's directory/file ref rule forbids child refs, and never splitting expanded stage IDs like `work/piece-a` on `/`. `LanderRef` and `loopPlaces` in `boundary.go` excuse `NodePinRef` prefixes, `NodeLaneBranch` refs and `<lane-root>/<item-id>/nodes/<node-key>/worktree` checkouts; `sweepRefs` retains `NodePinRef` prefixes alongside `ItemPinPrefix`; `collectIdleWorktrees` never collects an in-use or interrupted node checkout; and `worktreebudget.MeasureLaneRoot` walks active `<lane-root>/<item-id>/nodes/*` checkouts and scratch toward [`D-VO10`](./tillr-vocabulary.md#34-the-bound-and-what-happens-at-it). Read-only nodes at the same frontier still share one checkout. If two concurrent writers of one item touch the same fork, the planner either orders them with an `after` edge or stages node-keyed fork checkouts whose commits join before fork export; cross-item fork exclusivity (`fork-held`) is unchanged. Each running writer consumes one slot of the running-agent limit ([`D-VO6`](./tillr-vocabulary.md#35-what-limits-how-many-agents-run-at-once)); when the limit is `1`, ready fan-out writers claim one after another across passes into their per-node checkouts from `<base>` rather than advancing the primary worktree, and still reconverge through the same `join`.
- **Piece ordering in `build-pieces@v1`, `FencePaths` `pack-sync` companions, and `join` synthesis (`D-GP19`, `D-GP20`).** `WorkflowPiece` (`workflow_arguments.go`) carries an optional `after` list (`After *[]string`, `json:"after,omitempty"`). When a piece **omits `after`** (`After == nil`), `repeat: "pieces"` expansion (`workflow_check.go`) preserves backwards-compatible sequential list-order execution: a `"read_only"` piece depends on the most recent preceding `"write"` piece (if any), and a `"write"` piece depends on the preceding `"write"` piece(s) without an outgoing `"write"` edge plus any `"read_only"` pieces since the last `"write"` piece, running serially on the primary item worktree with **no** `join` stage synthesized. When a piece **declares `after` explicitly** (`"after": []` to start at the enclosing stage's entry frontier with no dependency on earlier pieces, or `"after": ["<piece-id>", ...]` to add `WorkflowEdge`s from those sibling piece IDs in the same `pieces` list, validated as distinct in-list IDs with no cycle), the piece participates in explicit DAG ordering; `planned-build@v1`'s `plan` prompt documents optional `after` on `args.pieces`. In `tools/tillr-drain/workflow/workflow.go`, `FencePaths` derives `just pack-sync`'s workflow mirror and `support-revision` companions idempotently (`rg -n 'support-revision|config/tillr/workflows' tools/tillr-drain/workflow/` returns nothing today): naming `tools/tillr/buildstamp/support-revision` derives `yolo-packs/tillr/files/local/share/tillr/tillr/buildstamp/support-revision` before `tillrModule` so `FencePaths(FencePaths(allowed))` never widens into `tools/tillr/`; naming `config/tillr/workflows` (or a path under it) derives `yolo-packs/tillr/files/local/share/tillr/workflows/`, `tools/tillr/buildstamp/support-revision` and `yolo-packs/tillr/files/local/share/tillr/tillr/buildstamp/support-revision`; and naming any `tillrModule` (`tools/tillr*` other than `tillrDocsTest` and `support-revision`) or `yolo-packs/tillr/programs*` also derives `tools/tillr/buildstamp/support-revision` and `yolo-packs/tillr/files/local/share/tillr/tillr/buildstamp/support-revision` (covered by `TestFencePathsDeriveTheGateCompanions` in `tools/tillr-drain/drain/fence_test.go`). While the pre-Slice-5 deployed loop built from `refs/tillr/green` executes Slice 5's own `planned-build@v1` graph, every writing piece in `args.pieces` that edits `tools/tillr*`, `config/tillr/workflows/`, or `yolo-packs/tillr/` must explicitly list `"tools/tillr/buildstamp/support-revision"` and `"yolo-packs/tillr/"` in its `write_scope` (`D-GP20`). Whenever an expanded workflow graph (from `repeat: "pieces"` with explicit `after` or from an authored workflow JSON) leaves two or more `"write"` stages unordered in a parallel fan-out without an explicit reconverging `kind: "join"` stage, `expandWorkflow` (and `replaceProposalGraph`) synthesizes a **`kind: "join"` stage** (`access: "write"`, no `seat`, no `command_ref`) wired after every writer in that fan-out and before their downstream dependents and `exit.requires_nodes` (`DecodeWorkflow` and `tools/tillr/watch/lint_workflow.go` likewise accept explicit `kind: "join"` stages with `access: "write"` and no `seat` or `command_ref`).
- **Joining onto the item branch (`D-GP16`, `D-GP19`, `D-GP20`).** Every parallel writer fan-out reconverges at its **`join` stage** (`after` every sibling writer in the fan-out, and `before` downstream `verify`, `judge`, `gate run` or dependent writing nodes; for parallel dispatched child writers in Step 13, before the parent resumes). All started sibling writers run to their own endings before `join` claims — if one writer objects or fails a post-run fence/scope check (`fenceViolationAt` over `<base>..nodeHead` against the deny-list, clean worktree and its own `write_scope`) while a sibling is still running, the running sibling finishes and pins its commit at `NodePinRef` rather than being killed (`R1`, `R5`), and any per-writer failure, refusal or obstacle is settled on that writer before `join` runs. Once every sibling writer in the fan-out has passed, `join` runs in-process in `tillr-drain` as Git plumbing (consuming zero `running_agents` slots, recording `runs` with `backend = "command"` and `node_runs`, and skipped as a non-agent stage in `evaluatorCandidate`'s session/writer list so `verify` judges the agent producer sessions). It folds changed sibling commits (`head != <base>`) onto `<base>` in deterministic stage order via `git merge-tree --write-tree --merge-base=<base> <accumulated-tree> <node-head>` and `git commit-tree -p <base>` using the repository's configured Git identity (never typing an invented author, preserving the first changed sibling's `GIT_AUTHOR_DATE`, and formatting the commit message with the first changed sibling's message plus `- <subject>` bullets for remaining changed siblings to match `Trunk.Squash` and [landing principle 2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch)), filtering `CONFLICT` lines with `boundary.FilterMirrorConflictReports` (`PackMirrorModule` in `internal/boundary/pack_mirror.go`):
  - If zero writers changed files, the item branch stays at `<base>` and `join` ends `joined` (`node_runs.outcome = 'passed'` ahead of Step 2).
  - If one writer changed files, the item branch fast-forwards to that writer's commit (`WriteItemPin` and primary worktree head updated) and `join` ends `joined` (`node_runs.outcome = 'passed'` ahead of Step 2).
  - If two or more writers changed disjoint files or cleanly merging regions (including when `git merge-tree --write-tree` reports conflicts exclusively on generated `PackMirrorModule` paths such as `tools/tillr/buildstamp/support-revision` or `yolo-packs/tillr/files/local/share/tillr/`, which `join` resets to `<base>` via `restorePackMirrorPaths`), `join` runs `boundary.StagePackMirror` on the primary item worktree when `HasPackManifest` holds so `support-revision` and pack mirrors match the combined source tree before `verify` runs, writes the single-parent merged commit onto the item branch, advances `refs/tillr/items/<item-id>`, the item's lane branch and the primary item worktree to it, ends `joined` (`node_runs.outcome = 'passed'` ahead of Step 2), and deletes the per-node writer checkouts, their node scratch and their `NodeLaneBranch` refs immediately (keeping each node's `NodePinRef`, `WriteRunPin`, declared outputs, transcript and execution data).
- **What a conflict at the join does (`D-GP17`, `D-GP19`, `D-GP20`).** When sibling writers make overlapping non-mirror source edits (`len(nonMirror) > 0` after `boundary.FilterMirrorConflictReports`) that `git merge-tree --write-tree` cannot merge cleanly, `join` records `<base>`, every sibling node's id, `NodePinRef` and commit SHA, and the sorted conflicting non-mirror file paths. **Neither writer's commit is discarded, and tillr never guesses or forces a resolution.** Per-node checkouts, node scratch and temporary `NodeLaneBranch` refs are deleted once their commits are pinned on `refs/tillr/item-nodes/<item-id>/<node-key>`. Ahead of Migration Steps 2 and 3, `tillr-drain` records `node_runs.outcome = 'handled-fail'` and splices an intra-item **conflict-reconcile `"agent"` stage** (`id: "<join-id>-reconcile"` or `"-reconcile-<n>"`, `seat: "build"`, `access: "write"`, `write_scope` equal to the union of the fan-out siblings' `write_scope`s or `"inherit"` if any sibling used `"inherit"`, appended in `graph_revisions` + `drain.graph-changed` on the pattern of `insertRepairWorkStage` in `verify_return.go`) immediately after `join` and before `join`'s downstream stages on the primary item worktree starting from `<base>`. Its brief gives `<base>`, each sibling's `node_id`, `NodePinRef`, commit SHA and diff, and the conflicting non-mirror paths (omitting generated pack-mirror conflict markers via `skipPackMirror = true`, matching `conflict.go`) so the worker commits the reconciled tree onto the item branch before `verify` and `gate` proceed — or ends blocked with `OBSTACLE: definition` and `EVIDENCE: plan-wrong: …` (`builderPlanWrong` in `verify_return.go`), which returns to `plan`. Under `D-GP11`, an identical repeated join-conflict key (computed over `<base>`, ordered sibling `(node_id, tree_sha)` pairs and sorted conflicting paths) routes to `design` via a recorded design conflict rather than splicing another reconcile stage — never to the owner unless the design seat writes an explicit owner question (`R3`). Once Migration Steps 2 and 3 land, `'passed'` / `'handled-fail'` and the in-graph splice become stated endings `joined` / `conflict` and rule row 28; parallel `hoisted child` writers (`rg -n 'hoisted' tools/tillr-drain/` returns nothing today) land in Step 13 ([Section 9](#9-subagent-hoisting)) atop the same `NodePinRef`, per-node checkouts and `join`.

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
9. Write access for a child of a read-only node. A writer needs no predicted path list; see the [shared protections](./tillr-vocabulary.md#51-the-work-and-its-statement).
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
| Intake findings | The block has prose findings; no document declares the item | Sync |
| Owner release | The owner named this item to land despite a named red check | The owner, by a command |

Not on the list, and refused if a rule names them: the clock; any content hash; the trunk tip as a trigger; the number of earlier runs; the policy file's own contents. The trunk tip is read by the take-in and the lander when they run. It is not an input that makes the function produce something new.

An edit to an item's description is not a fact any rule reads. Later nodes read the new text; running nodes keep the brief they were claimed with (R2).

### 5.3 The starting rule table

This is the rule table for this repository's landing definition. "Producer" means the session of the node that last wrote the thing in question. Every resume is uncounted.

| # | When the graph holds | The function adds |
| :--- | :--- | :--- |
| 1 | No nodes; the item has a block with no prose finding | The nodes of the item's template, in fog |
| 2 | No nodes; the block has prose findings | A rewrite node, then the template after it |
| 3 | No nodes; no document declares the item | A triage node, then the template after it |
| 4 | A sequential work, rewrite, triage or resume node ended `submitted` (parallel writers feed a `join` node instead; row 28) | A judge node when the item asks for one, otherwise a gate run; a take-in first when the trunk moved |
| 5 | Any node ended `refused-by-check` | A resume of its session with the check's output |
| 6 | An agent node ended `missing-output` | A resume of its session saying what is missing. Where the harness has a boundary hook, the worker is told inside the running node and no ending is recorded; the row applies only when the process exits anyway |
| 7 | A judge ended `fail`; or `pass` | A resume of the producer with the findings, delivered as the answer to the producer's open landing call; or a gate run |
| 8 | A gate run ended `pass` | A land attempt, unless the trunk facts make it wait ([Section 8.2](#82-proof-and-a-red-trunk)) |
| 9 | A gate run ended `refused` | A resume of the producer with the gate's output (R4), delivered as the answer to the producer's open landing call |
| 10 | A land attempt for the item's deliverable ended `landed` | Nothing. The graph is complete. The producer's landing call stays open until the proof covers the landing, then closes; when the item's worktree is released is [the landing call's question](./tillr-landing-call.md#OQ-LC1) |
| 11 | A land attempt or a take-in ended `conflict` | A resume, on the latest trunk, of the session that last wrote the conflicting paths, with the paths, delivered as the answer to the producer's open landing call |
| 12 | A node ended with an objection naming the design seat | A design answer that starts at the objector's head and forks the document's designer session; when it ends `answered`, a resume of the objector from the design's commit, which does not wait for row 27 |
| 13 | A node ended with an objection naming the owner, with its question | A cut of the question's documents, its gate run and its land attempt, then an owner question node after the landing, or directly when the cut ends `empty`. When the owner question ends `answered`, a resume of the objector with the answer |
| 14 | A node ended with an objection naming another item | A queue edge request; the objector resumes after that item lands and a take-in |
| 15 | A node ended with an objection naming the tooling | A defect item filed through intake, a queue edge to it, and a resume after it lands |
| 16 | A node ended `backend-closed` | A backend move on the seat's next open backend; with none open, a wait naming the backend and its reopening |
| 17 | A resume in fog whose backend closed | A wait naming the backend, since the session lives there |
| 18 | A resume in fog whose session is not available | It is replaced by a backend move that reads the transcript where one exists |
| 19 | The ending just recorded is an identical repeat | A design answer as diagnosis, in place of the resume the row above would add ([Section 7](#7-what-stops-repetition)) |
| 20 | An ending carries a request to replace a later node in fog | The replacement, when the requesting node's template allows it; otherwise the refusal is recorded and the graph is unchanged |
| 21 | A running node asks for a child the harness routes to tillr | A dispatched child within it |
| 22 | A landing of the item, its deliverable or an early landing, was then reverted for a named check | A resume of the producer, with the answer `reverted` to its open landing call carrying that check's output; its next gate run includes that check. For an early landing the producer is the session that wrote the documents, and it is told to restore them from the head it recorded |
| 23 | A cut ended `cut` | A gate run on its candidate, with no take-in and no judge (documents-only landings; [the diff-review design](./tillr-diff-review.md#decision-ledger), `D-DR2`). Rows 8, 9 and 11 apply to it; the producer is the session that last wrote the documents |
| 24 | A cut ended `conflict` | As row 11, then a new cut |
| 25 | A cut ended `empty`, or its land attempt ended `landed` | Nothing for the cut. A node that waited on it is ready |
| 26 | A node ended `submitted` while a cut of the item is in fog | The cut and its followers are removed; the item's landing carries the documents |
| 27 | A design answer ended `answered` having changed documents | A cut of those documents. A resume that repairs a refused or conflicting cut is claimed after the item's running writer ends |
| 28 | A `join` ended `joined`; or `conflict` | The downstream `verify`, `judge`, `gate run` or dependent writer in fog becomes ready on the joined item-branch head; or a conflict-reconcile `work` node on the primary item worktree given `<base>`, the sibling commit SHAs on `refs/tillr/item-nodes/<item-id>/<node-key>` and the conflicting paths ([Section 3.8](#38-concurrent-writers-checkouts-and-the-join); ahead of Step 3, `tillr-drain` splices `<join-id>-reconcile` directly via `graph_revisions` per [`D-GP19`](#D-GP19)) |

**Degenerate cases.** An item with no template gets the tracked one-node template. A missing or empty path list does not cause a wait. A template that expands to nothing is refused when it is loaded. Two rules never match one ending: the table is checked for overlap when it is loaded, and row 19 is the one stated precedence.

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
- **Another definition** supplies a program in the hook form [the core and the landing definition](./tillr-core-and-git-landing.md#5-the-extension-points) already chose: the core gives it the graph and the facts and reads back a transformation, which the store checks like any other. The program may be written in any language.
- **Purity of a foreign program** cannot be enforced. It is tested: the definition check evaluates each of its fixtures twice and refuses a program whose two answers differ.
- **What the core does not preclude**, by the owner ruling recorded in [the graph-change ruling](./tillr-separable-parts.md#72-graph-change): a node inserted directly downstream of a running one, and nodes appended to a finished graph. Both are transformations of the fog.

### 5.7 Relation to the successor function

[The drain's successor function](../reference/tillr-drain-successor.md#the-successor-function-and-derived-positions) is one pure function, `next`, that answers which single transition an item takes, or `none` with a hold. This design keeps its properties (total, deterministic, order-independent, no clock) and splits its job in two:

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

**The running-agent limit ([`R-GP2`](#R-GP2), [`D-VO6`](./tillr-vocabulary.md#35-what-limits-how-many-agents-run-at-once)).** The model has no numbered pool slots: the drain's `AvailableLanes`, `freeLanes`, `ordinary_lanes`, the `[lanes] express` setting in `.tillr.toml` and the hold reason `no-lane` have no counterpart. Concurrency is governed by **the running-agent limit** (`running_agents` / `--max-agents`, with `--lanes` and `tillr drain scale --lanes` kept as documented aliases per `D-VO9`): each running agent node consumes one slot up to the limit, ready ordinary nodes past the limit stay in fog with wait reason `agent-limit` naming the running agent nodes as mover, and owner-awaited work (`express = true`) runs outside the ordinary limit without pre-allocated `express-N` directories (`D-IG10`).

**Worktrees ([`R-GP1`](#R-GP1), [`R-GP2`](#R-GP2)).** Lanes do not exist as independent entities: an item is scheduled, runs, and acquires its primary worktree (`<lane-root>/<item-id>/worktree`) at the claim of its first node that needs files. When a node ends, the primary worktree is idle, not free: it stays the item's, and it may be collected and later restored from the pinned item branch. Sequential writing nodes reuse the primary worktree; read-only nodes at one frontier may share it; concurrent writing nodes placed in parallel by the item's plan each stage an isolated per-node checkout (`<lane-root>/<item-id>/nodes/<node-key>/worktree`), commit to a pinned node ref (`refs/tillr/item-nodes/<item-id>/<node-key>`), reconverge at `join`, and delete their per-node checkouts once joined ([Section 3.8](#38-concurrent-writers-checkouts-and-the-join)). An owner question needs no worktree. The lifecycle, the `2x` count and `4 GB x` disk bounds, and collection order are in [the vocabulary, Section 3](./tillr-vocabulary.md#3-what-a-lane-was-and-the-worktree-as-a-resource).

**Order, when ready nodes exceed resources.** Work the owner is waiting on first, outside the running-agent limit, as `D-IG10` rules. Then, nearest to done first: land attempts, gate runs, joins and judges, take-ins, resumes and backend moves, design answers, rewrites and triage, and last the first nodes of items not yet started, by rank. The mechanic and proof nodes are placed by [Section 8.2](#82-proof-and-a-red-trunk). The exact tie-break within a class is the implementer's, provided it is stable.

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
| `conflict` | The conflicting paths and the other side's commits (for a `join` conflict, the sibling node commit trees and conflicting paths) |
| Judge `fail` | The criterion ids judged unmet |
| Objection | The next actor and the normalized objection text |
| Design answer `answered` | The normalized instruction it gives |

An ending is an identical repeat when its cause and key equal those of **any** earlier ending in the chain, not only the last. A producer that alternates between two failures is caught on the third.

**What happens.** Row 19 adds a design answer as diagnosis in place of the resume. It reads both endings and the work between them, and it must end in one of three ways: an instruction to the producer that differs from every earlier one in the chain, possibly naming a stronger seat; an objection naming the owner, with its question; or an objection naming the tooling, with a defect. An instruction equal to an earlier one is `missing-output` and returns to the diagnosing session saying which instruction it repeats.

**For an exception**, the same idea without a node: when a node's exception record repeats the earlier one with the same observation, the machinery is at fault. A defect item is filed through intake with both records, the node waits on it with the tooling as mover, and every other item proceeds.

**The weakest point, stated plainly.** Nothing here stops a chain whose every ending is new. A producer that fails a different check each time, or a diagnosis that rewords the same useless instruction past the normalization, runs until someone objects. That is the cost of R1, and it is the right cost: a count stops work that is one message from done and diagnoses nothing. What the design offers instead is information:

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

**The mechanical part** is a pure function over the items' definitions in force and the queue's facts, evaluated like the policy function. It adds edges the definitions already imply: an authored dependency; an objection naming another item; items a design answer names as affected; a defect item an objection filed. Path lists do not decide which items run together.

**The judgment part** is a queue-plan node: an agent on a planning seat that reads the whole queue and proposes dependencies and order.

| | The queue-plan node |
| :--- | :--- |
| **May read** | Every item's definition in force and its graph, including which paths each done node changed; the queue edges; the trunk facts; the order |
| **May change** | Edges of its own class between items, each with a written reason; the rank of items, which affects only future claims. It may remove only edges a queue-plan node added |
| **May never** | Touch a node inside an item; cancel or pause anything running; edit an item's definition; remove an authored dependency; add an edge that makes a cycle |
| **How its proposal is applied** | Its ending carries the proposal as data. The queue's function checks it against the rows above and applies what passes, recording each refusal |
| **Its premises** | The item facts each edge was predicated on. An edge whose premise is gone (the other item landed or was abandoned) is not produced again |

**It must not become a guess trigger.** It runs on events that mean "something crossed items", never on a timer and never because the queue's contents changed in some unread way: a conflict whose other side is a queued or just-landed item; an objection naming another item; the owner asking. A batch of newly admitted items is not such an event ([OQ-GP4](#OQ-GP4)).

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

**Attribution.** When a proof ends red on K, bisect proof nodes run K alone on the landings between the green pointer and the red commit, as [the gate reference](../reference/gate.md#a-red-proof) designed.

| What bisection finds | The fact recorded | Why it matters |
| :--- | :--- | :--- |
| Green at the pointer, red from landing L on | Red on K, attributed to L | L can be pulled out |
| Red at the pointer too, with unchanged inputs | Red on K, unattributed: **wrong judge** | No landing is at fault. The check or its environment is broken, and no revert repairs it |
| Red, then green, with no change to K's inputs | K is flaky, and K is disabled | A defect in the check. By owner ruling (2026-11-21), K is disabled and one defect item is filed. The trunk is not red on K, K is not reported green, and nothing waits on K. The mechanism is in [the gate reference](../reference/gate.md#a-contradicted-check-is-disabled) |
| A bisection step has no verdict | Attribution is pending | An exception; the step resumes |

**The remedies, in order.**

1. **Revert L**, when it is safe. tillr does this by itself ([OQ-GP1](#OQ-GP1)); the drain does so as a drain rule, with `auto_revert` on in `config/tillr/gate-policy.json`. The lander builds the inverse of L on the tip. It lands, as a land attempt with no model in it, only when all of these hold: it applies cleanly; the fast gate passes on the tip with the revert; K passes on the tip with the revert; no landed item declares a dependency on L's item; and L is not itself a revert or a mechanic's landing. The third condition is the test that the attribution was right. The last prevents a remedy from being undone by a remedy.
2. **L's item gets its work back.** Row 22 resumes the session that built L with K's output. Its next gate run includes K, because the fast gate has shown it cannot see this failure. It lands again as one commit when it passes.
3. **The mechanic**, when a revert is not the answer: the revert is unsafe by one of the conditions above; K still fails with L reverted; or the red is unattributed. The mechanic is an ordinary item with an ordinary graph. It starts from the tip, is given K's output and the attribution, and outranks every item that is not itself a remedy. Its gate run includes K. One mechanic exists per failing check and attribution; a later red tip for the same check joins the open one.
4. **How the two combine.** The mechanic is filed only when the revert path has ended in "not possible", so no model run is spent on a red that a revert clears. If a mechanic is already running when another candidate clears the red, its node finishes; its next take-in finds nothing left to do, and it says so.
5. **A mechanic that cannot proceed objects like any other actor.** It names the design seat, L's item, the tooling, or the owner with a question. It never goes to the owner by default (R3).

**What may land while the trunk is red or unproven** ([OQ-GP7](#OQ-GP7), `D-GP14`). There is no landing cap.

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
- **Each check declares its inputs.** Every check file in `tools/gate/checks/` declares `reads`. Eight declare the whole tree: `citations`, `commit-identity`, `design-citations`, `documents`, `elapsed-time`, `file-size`, `pack-sync` and `scratch`.

**What is built.** The drain applies the waiting rule as a drain rule, described in [the drain's guide](../../tools/tillr-drain/README.md):

- **A second passing verdict.** A candidate refused only by checks that also fail on its base and on the trunk tip, and whose changed paths intersect no red check's declared `reads`, is promoted to `pass-inherited` (`VerdictPassInherited` in `tools/tillr-land/land/verdict.go`), with each inherited failure recorded, and lands.
- **The wait.** A candidate whose changed paths intersect a red check's `reads`, and that does not make the check pass, is recorded `trunk-already-red` with the red commit, the checks, the fixer and the intersecting paths, and waits on the fixer's item. A red on a whole-tree check holds every item but the remedies.
- **No cap in the drain.** The drain ignores a `red_cap` policy key, and the tracked gate policy sets none. The lander keeps its cap code ([the lander's guide](../../tools/tillr-land/README.md)).

Proof as scheduled nodes, and the trunk panel below, are not built.

**Publishing.** Only a proven commit is pushed: the push sends the green pointer, never the tip (`[push] what = "green"` in `.tillr.toml`).

**Pulling work out** covers four moves, all of them above:

- **Out of the trunk:** the revert pulls L out, and L's own session lands it again once repaired.
- **Past the red, by itself:** a built candidate disjoint from K's inputs lands with no one's action.
- **Past the red, as the fix:** a built candidate that makes K pass lands, and the red ends.
- **Past the red, by the owner's word:** the owner releases a named candidate for a named check. It is an event with the owner's reason, it lands, and the trunk panel shows the attribution range it lengthened.

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

**Target level for each harness.** A capability marked "unverified" has no probe in this repository behind it.

| Harness | Target | What makes it possible | What it needs |
| :--- | :--- | :--- | :--- |
| **pi with pi-subagents, asynchronous children** | **L2** for every child; **L3** for the roles that need it | Each asynchronous run writes a status file and an event file as it goes, with each step's agent, model, tokens, session file and worktree. An agent definition can route its launch to an external job provider or an external command (both unverified) | L2: a reader of those files keyed by the parent's session, and the status file naming the tillr run. L3: a tillr job provider, and agent definitions that use it |
| **pi, foreground children** | L1 | They run inside the parent's own process | The parent's tool-call records |
| **Claude Code** | **L2** | Stream lines of a sub-agent carry the parent tool call's id, and start and stop hooks fire with an agent id (unverified) | The adapter to read what it discards. L3 waits on a trial showing that a hook can answer a spawn with tillr's own result |
| **agy, muse** | none | No child handling was found | Their nodes are shown as "children: cannot be known" |

**What cannot be dispatched by tillr, and why.**

- **A foreground, in-process child.** There is no launch boundary to route.
- **A child inside a workflow script that awaits it and reads its output.** The control flow lives in the script's runner; tillr would have to answer inside another program's call stack.
- **A child that forks its parent's context.** It inherits the parent's transcript and prompt cache; started by tillr it would be a different, colder thing. It is shown as an observed fork.
- **Any child on a harness with no interception point.**

**The write scope of a child.**

- **A dispatched child cannot turn read-only access into write access.** There is no item path list to inherit ([Section 4.3](#43-what-the-store-refuses), row 9).
- **A dispatched writer** runs while its parent is suspended on the call and is checked at its own end as any writing node is. A single dispatched writer runs in its parent's checkout; when a parent dispatches multiple writing children in parallel (`R-GP1`), each concurrent child writer gets its own per-node checkout cut from the parent's head and reconverges through a `join` before the parent resumes ([Section 3.8](#38-concurrent-writers-checkouts-and-the-join)). Parallel read-only children share the parent's checkout, and a commit or changed file from a read-only child is refused.
- **An observed child is checked by nothing of its own.** Its changes are covered only by its parent's check at the parent's end, and only for what reached the parent's worktree. A child that the harness placed in a worktree of its own is outside tillr's sight until its parent brings the work in. The item page says which children were checked and which were only observed.

**What an observed node may be used for.** It is drawn, its transcript opens, and its spend is counted against the item. It cannot be the producer of an `input` edge and no rule fires on it, because its result is whatever its parent says it was.

**What this overturns in `D-IG12`.** That row made one node with child sessions the default and ruled that a sub-agent is recorded and shown but never a node. Under this design:

- every sub-agent tillr can see is a node, inside its parent;
- the row's three tests (planned, fenced or judged apart) do not decide node or not-node. They decide dispatched or observed;
- the row's cost argument stands for that choice: a dispatched child pays a staging and a settlement and starts without its parent's cache, which is why the ruling in [OQ-GP5](#OQ-GP5) is not "dispatch everything";
- its worked example survives: a designer may still ask, in its ending, for its remaining node to be replaced by scouts and a join. It may now also simply start scouts with its harness's own tool and have them dispatched on the scout seat.

The trace of shell commands cannot tell one child's commands from another's, since children inherit the parent's run id. Nothing here depends on it doing so (R12).

---

## 10. Storage

What follows is what the graph model needs from storage. Which structure provides it is decided in [the storage design](./tillr-storage.md). The rest of this design depends only on the interface that document defines, not on the structure behind it.

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
| **No flush on the write path** | Durability need only survive a process kill (owner ruling), so no write has to wait on the file system's slowest operation |

**What no structure fixes.** A caller that does git, file or process work while it holds the store's write lock stalls every other writer, whatever the structure: a single writer process would block in the same place, with every other write queued behind it. The repair is to do nothing else under the lock, which is the owner's standing rule (minimal work under locks; every lock failure gets a root cause and a fix, R6). No such fault is a reason to prefer one structure over another; that choice is [the storage design's](./tillr-storage.md#1-the-verdict) to show, by measurement.

---

## 11. The view

**What stands in for lanes in the Observatory ([`D-VO16`](./tillr-vocabulary.md#36-what-the-observatory-shows-instead-of-lanes), designer's decision; the owner may overturn it).** In [`tools/tillr-view/`](../../tools/tillr-view/), the header's `LanePill` (`N/M lanes running`) becomes **`R of L running`** against the running-agent limit (plus `, E express` when owner-awaited nodes are running outside the limit, or `0 of L running · idle`), never counting idle worktrees on disk as slots. Home's `LaneStrip` becomes **Running work**: a summary of `R of L running`, active express nodes, worktrees on disk, and worktree and scratch bytes against `D-VO10`, followed by one row per running node execution. `#/lane/<id>` (`LanePage`) is not a primary screen; the route stays so that stored lane records still open (`D-VO9`). [`glossary.json`](../../tools/tillr-view/web/src/models/glossary.json) defines `run` as a node's execution data on that node, and defines `node` and `path`. None of this is built: the view shows lanes.

**The top level has two lists.** "Needs you" holds exactly the items with an owner question that is not answered. "Draining" holds everything else, including every item that waits on something other than the owner. Within "draining", items are ordered by spend against target, largest first, so that a chain that is going nowhere is the first thing seen.

**An item's page is its graph, and each node holds its own execution data ([`R-GP1`](#R-GP1), [`D-VO3`](./tillr-vocabulary.md#D-VO3)).**

- Nodes are drawn in causal order, with chains of `resumes` shown as one continuing session and parallel nodes (including concurrent writers feeding their `join` node) drawn side by side.
- Each node carries its execution record directly on `#/item/<id>` (`#/item/<id>?node=<node-id>`): its kind, seat, backend and model, segments, elapsed time, lease/liveness, spend, ending cause and next actor, and links to its log, transcript and declared outputs.
- Hoisted children are drawn inside their parent, marked dispatched or observed, and a parent whose harness cannot report children says "children: cannot be known", never "none".
- Nodes in fog are drawn as such, each with the rule that added it. A wait is drawn where the next node would be, with its reason and its mover as a link.
- Exceptions are marks on the node they interrupted, opening the exception record. Their number is shown as a fact.
- The definition in force is shown beside the definition on the trunk when they differ, with the node that revised it.
- The revision record shows every transformation: what was added, replaced or removed, by which rule or request.

**An item that ran as one node is drawn as one node.** The page never shows an empty graph.

**The queue's page** adds the trunk panel of [Section 8.2](#82-proof-and-a-red-trunk) and the inter-item edges, each with the reason and the node or author that added it.

The built observatory view that [draws an item's nodes on the item page](../reference/tillr-item-graphs.md#observatory) is the starting point. Its rule that a child session is never drawn as a node changes when [subagent hoisting](#9-subagent-hoisting) is built.

---

## 12. What this replaces

The drain applies several of this design's rules as rules of its single-run path, with no node behind them ([the drain's guide](../../tools/tillr-drain/README.md)):

- **A gate refusal returns to its worker's own session**, uncounted (row 9; R4).
- **A reverted landing and a squash conflict return to the session that wrote the change** when its worktree and session remain; when they do not, a fresh session starts and the reason is recorded (rows 11 and 22).
- **A repeated ending goes to the design seat.** The drain compares an ending's producer, cause and evidence with every earlier ending of the item (`repeatedEnding`; row 19).
- **Automatic revert is on** (`auto_revert` in `config/tillr/gate-policy.json`; [OQ-GP1](#OQ-GP1)).
- **The waiting rule for a red trunk**, with no landing cap ([Section 8.2](#82-proof-and-a-red-trunk); [OQ-GP7](#OQ-GP7)).
- **A gate that returns no verdict runs again** on the same commit, under a wait named `gate-no-verdict` whose mover is the loop.
- **The proof after a merge judges every check**, and a fixer that is closed releases the items behind it once a proof passes its checks.
- **A worker's run ends with a commit or a named obstacle** ([`D-JL11`](./tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11)).

The drain is still a single-run path beside the graph executor: an item that names no workflow, and whose seat has no default, runs as one implicit worker.

### 12.1 The item graph ledger

Every row of [the item-graph ledger](../reference/tillr-item-graphs.md#why-its-this-way), and what this design does to it.

| Row | Verdict | What changes |
| :--- | :--- | :--- |
| `D-IG1` | Kept | Templates still expand into ordinary nodes. Expansion now happens in the fog at any time, not only before dispatch |
| `D-IG2` | Amended | The outside still sees one item, one deliverable and one landing of that deliverable. The graph does not stop before the gate. The item's documents may reach the trunk ahead of it, in early landings ([early design landing](./tillr-early-design-landing.md)) |
| `D-IG3` | Kept, widened | Fixed at the claim. The policy function, and not only a planning node's proposal, transforms the fog |
| `D-IG4` | Amended | Path ids and the parent stage are kept. The depth and size caps go |
| `D-IG5` | Kept | Tracked templates and commands. The rule table is tracked and versioned the same way |
| `D-IG6` | Amended | Producer-bound inputs and checked outputs are kept. Edges gain kinds and lose their role in routing; `when_outcome` is shorthand |
| `D-IG7` | Amended | No item or child path lists. Item comments are protected even in the worker's own design document, and a rewrite has no exception |
| `D-IG8` | Amended | The item keeps one item branch and one primary worktree across sequential nodes, as a resource it acquires and that is collected and restored (`R-GP2`), while concurrent writers placed in parallel by the item's plan stage isolated per-node checkouts that join back onto the item branch (`R-GP1`, `D-GP16`). The gate and the landing are nodes and run as often as the rule table adds them, so "once at the exit" is withdrawn. What survives: a graph runs correctly with a running-agent limit of `1` |
| `D-IG9` | Amended | See [Section 4.5](#45-relation-to-the-item-graph-rulings) |
| [OQ-IG1](../reference/tillr-item-graphs.md#why-its-this-way) | Kept | One commit on the trunk for an item's deliverable, by the lander's squash |
| [OQ-IG2](../reference/tillr-item-graphs.md#why-its-this-way) | Kept | Refinement by a running node's request is row 20 |
| [OQ-IG3](../reference/tillr-item-graphs.md#why-its-this-way) | Overturned (2026-11-23) | Read-only nodes still share a checkout. The serial-writer rule, enforced by `claimParallelWithShut` in [`tools/tillr-drain/drain/parallel.go`](../../tools/tillr-drain/drain/parallel.go), is overturned by [`R-GP1`](#R-GP1): the item's plan decides when writing nodes (planned pieces or dispatched children) run in parallel in per-node checkouts and join onto the item branch ([Section 3.8](#38-concurrent-writers-checkouts-and-the-join)) |
| `D-IG10` | Kept | It is a scheduling rule and sits in [Section 6](#6-scheduling) |
| `D-IG11` | Amended | A node is one act, which adds the owner question and the hoisted child. "Attempt" is not used. "Never a node" for a child session is withdrawn |
| `D-IG12` | Superseded | See [Section 9](#9-subagent-hoisting) |
| `D-IG13` | Amended | The `workflow` attribute and tracked files stand. A seat is declared by the policy, not drawn from a closed list |

### 12.2 Decisions in other documents

Each of these is an amendment owed in the document that holds the row.

| Where | Row | What this design does |
| :--- | :--- | :--- |
| [The successor function](../reference/tillr-drain-successor.md#why-its-this-way) | `D-DS1`, `D-DS6` | One transition per item per pass becomes: evaluate the function, apply its transformation |
| | `D-DS2` | Identity by attempt ordinal becomes the node's identity |
| | `D-DS4` | The `held` position and the hold vocabulary become waits |
| | `D-DS10` | Kept |
| [Planning and tiering](./tillr-planning-node-model-tiering.md) | `D-PT1` | The seat list is open, not a longer closed list |
| | `D-PT3`, `D-PT6` | Two rungs, one fix per rung and one re-plan are counts and are withdrawn. A stronger seat is named by a diagnosis |
| | `D-PT7` | Kept as a scheduling choice: whether a resumed session is compacted |
| [Designer sessions](../reference/tillr-designer-sessions.md#why-its-this-way) | `D-CD10` | "One automatic fallback" becomes row 18: each reason a session cannot resume is a stated fact |
| [The judge and the lifecycle](./tillr-judge-and-lifecycle.md#return-a-design-runs-gate-refusal-to-its-own-session) | The gate refusal's return to its own session | Kept, and made the rule for every producer |
| [The gate reference](../reference/gate.md#why-its-this-way) | `D-GR21`, a contradicted check is disabled | Kept; the attribution table of [Section 8.2](#82-proof-and-a-red-trunk) follows it |
| | [OQ-GR1](../reference/gate.md#why-its-this-way), the red cap | Overturned by [OQ-GP7](#OQ-GP7) and replaced by the one waiting rule of [Section 8.2](#82-proof-and-a-red-trunk) |
| | [OQ-GR2](../reference/gate.md#why-its-this-way), new work cut from the green pointer | Kept for new work by [`D-GR24`](../reference/gate.md#why-its-this-way): a new item starts from the green pointer once its blocker's landing is proved, and a trunk fixer starts from the tip. R8 states the same |
| | `D-GR6`, automatic revert | On ([OQ-GP1](#OQ-GP1)); stated in [the gate reference](../reference/gate.md#a-red-proof) |
| [The landing call](./tillr-landing-call.md) | Suspend and resume by a tool result | Designs "done is a suspended tool call" for tillr |

### 12.3 Mechanisms of the drain

Each mechanism below acts on a count, a hold or a guess and has no counterpart in the model. Some are already removed from the drain.

| Mechanism | Becomes | Rule |
| :--- | :--- | :--- |
| Rewrite items, with the original held | A rewrite node at the head of the item's graph | Row 2 |
| Conflict-fix runs | A resume of the producer after a `conflict` ending | Row 11 |
| Design-conflict rows, a separate design run and landing, then a new worker | An objection, a design answer on the item branch, a resume of the objector | Row 12 |
| Re-admission when an input hash changes | Nothing. No rule reads a hash | [Section 5.2](#52-permitted-facts) |
| Second-failure routing to the design seat, then the owner | A diagnosis on an identical repeat; the owner only by a named objection | Rows 19 and 13 |
| The one-continuation rule | A resume after `refused-by-check`, uncounted | Row 5 |
| The judge bracket that swaps the lane's brief and session | A judge node with its own run and session | Row 7 |
| Item-level holds and the hold vocabulary | Waits, each with a mover | [Section 5.4](#54-waits) |
| The red cap, and the whole-queue hold while a fixer is open | The one waiting rule; proof as scheduled nodes ([OQ-GP7](#OQ-GP7)) | [Section 8.2](#82-proof-and-a-red-trunk) |
| Stale-base detection and re-staging | The take-in before every node that needs the worktree | [Section 3.7](#37-the-item-branch-and-the-latest-trunk) |
| The failover budget | A backend move, or a wait naming the backend | Rows 16 to 18 |
| One triage run per version of a request | A triage node; an unusable result returns to it | Rows 3 and 6 |
| Invalidating an in-flight workflow when the block changes | Nothing. Fixed nodes do not change, and the edit is no fact | [Section 5.2](#52-permitted-facts) |
| A stalled full-suite run marked red on its second stall | An exception; the proof resumes | [Section 8.2](#82-proof-and-a-red-trunk) |
| A blocked fixer report sent to the owner | The mechanic objects and names its next actor | [Section 8.2](#82-proof-and-a-red-trunk) |
| Classifying a run from its exit code and its log's tail | A stated ending | [Section 3.3](#33-endings) |

### 12.4 Audit findings

The catalog of drain mechanisms that act on a count, a clock or a guess, with what this design does to each. The `F-` ids are cited from code comments and other documents. Severity A can strand work or burn expensive runs; B is noise or owner load; C is harmless.

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
| F-16 B | A lane out of rotation, rechecked every ten minutes | Gone. The model has no lane pool ([`R-GP2`](#R-GP2), [`OQ-VO1`](./tillr-vocabulary.md#OQ-VO1)) |
| F-17 B | Recovery of claims that never launched | Partly. A claim is one transaction; the repair of unlaunched claims stays while such rows exist |
| F-18 B | The held-candidate lane state machine | Gone once item-owned worktrees land ([`R-GP2`](#R-GP2), [`D-VO5`](./tillr-vocabulary.md#decision-ledger)): an item owns its branch and worktree, so no lane slot is held by a candidate |
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

From the built graph code:

- **Kept as they are:** the template loader, expander and check; typed template arguments; the check and record of declared outputs; the command registry and command nodes; each node's own output directory; the group that lets read-only nodes share one checkout; the wiring of an evaluator node, which becomes the judge node.
- **Kept and generalized:** the proposal transaction, which already applies one change to nodes that have not started, against the graph it read, in one write, and records refusals. It is the pattern every transformation follows. The append-only revision record is the graph's history.
- **Kept as plumbing:** the session resume built for restarts and for comment runs; the record of each document's last designer session, which design answers fork; the refs that pin a node's head; the definition package's hook shape, which carries another definition's policy function.
- **Removed when the model is built:** the single-run path beside the graph executor; the walk that runs nodes in a fixed order until an exit condition; the hand-off from the graph's exit to the old admission path; identity by attempt; the numbered lane pool (`AvailableLanes` / `freeLanes`) and the blanket serial-writer gate (`claimParallelWithShut`).

### 12.6 What this does not cover

- **How ideas enter.** [Intake paths](./tillr-intake-paths.md) owns the doors. This design gives each door's steps a place (rows 2 and 3) and settles nothing else there.
- **The lander's internals.** A land attempt is a node; how the lander squashes, binds verdicts and normalizes authorship is unchanged and stays with [the landing principles](./landing-principles.md).
- **The fast gate's checks and budgets.** A gate run is a node; what it runs is [the gate reference](../reference/gate.md)'s.
- **Review-comment items and designer desks.** They stay separate owner-facing deliverables.
- **Spend ceilings.** A budget is a target that is shown. Nothing here enforces one.
- **A worktree's lifecycle, its bound and its collection.** [The vocabulary](./tillr-vocabulary.md#3-what-a-lane-was-and-the-worktree-as-a-resource) holds those.
- **Which session writes an answer the owner gave in a Vantage review into the document.** Vantage never writes the repository, so an agent does; which one, inside tillr, is not designed.
- **Per-file build detail.** None is given. No implementation plan is seeded by this document.

---

## 13. Migration order

The running drain keeps working at every step. Two principles order the work.

**Record first, route later.** Each act becomes a node in the record before the rule table decides anything about it. The item page shows the whole path early, and each routing rule then moves into the table alone, with the old mechanism deleted in the same item.

**Largest waste first.** After the base, mechanisms move in by the runs they waste. A count of runs in one store found 65 conflict-fix runs, 30 runs of 22 rewrite items and 11 judge runs through the bracket; each design conflict also discards a warm worker session and starts a new one from the trunk.

**Ahead of the policy-function migration.** Dropping lanes, attaching worktrees and execution records to items and nodes, and letting an item's plan run writers in parallel do not wait on the steps in the table below. They are five build slices, each waiting on the one before:

1. Replace fixed lane-pool scheduling with the running-agent limit (`D-VO6`).
2. Move worktree and scratch ownership onto items (`D-VO5`, `R-GP2`) — **Built.**
3. Bound idle item worktrees and scratch by count and disk budget (`D-VO10`) — **Built.**
4. Replace Observatory lane views with Running work and node-level execution records (`D-VO3`, `D-VO16`) — **Built.**
5. Run planned parallel workflow writing nodes in per-node checkouts (`<lane-root>/<item-id>/nodes/<node-key>/worktree`, pinned at `refs/tillr/item-nodes/<item-id>/<node-key>`) with optional `WorkflowPiece.After` DAG ordering, idempotent `FencePaths` `just pack-sync` companion derivation, built-in `kind: "join"` `git merge-tree --write-tree` item-branch joins (with mechanical mirror-only conflict regeneration), and pre-Step-3 intra-item `<join-id>-reconcile` stage splicing (`R-GP1`, `D-GP16`, `D-GP17`, [`D-GP19`](#D-GP19), [`D-GP20`](#D-GP20); parallel `hoisted child` writers stay in Step 13 below).

| Step | What moves | Waits on |
| :--- | :--- | :--- |
| 1 | Every item runs on the graph executor as a one-node template; the single-run path is deleted | nothing |
| 2 | Stated endings; exceptions logged apart from endings | step 1 |
| 3 | The policy function and the store's checks, with a table that reproduces the drain's routing. The checks are pure functions behind [the storage design's interface](./tillr-storage.md#6-the-interface-the-rest-of-tillr-sees), and from here on graph state is written only through it | step 2 |
| 4 | Take-in, gate run and land attempt recorded as nodes | step 3 |
| 5 | A landing or take-in conflict returns to its producer | step 4 |
| 6 | The prose rewrite is a node; definition edits live on the item branch | step 4 |
| 7 | A gate refusal returns to its producer | step 4 |
| 8 | An objection goes to a design answer on the item branch and back | step 6 |
| 9 | Backend move; identical repeat and diagnosis; the old count and hash paths deleted | steps 5, 7, 8 |
| 10 | The judge and triage are nodes | step 4 |
| 11 | The item page draws the graph | step 4 |
| 12 | Proof nodes, attribution, the waiting rule with its "passed, with inherited failures" verdict and its comparison of paths, the removal of the red cap, revert and mechanic. The revert and the waiting rule are built ahead of this step, as drain rules | step 4 |
| 13 | Observed children for pi, then dispatched children | step 3 |
| 14 | The cut; a question and its documents land ahead of the item; the owner question node, its surface and the answer's return | step 8, [early design landing](./tillr-early-design-landing.md) |
| 15 | The queue planner | step 9 |
| 16 | The storage structure behind the interface: one writer, then a log per item, as [the storage design](./tillr-storage.md#92-the-order-against-the-graph-designs-sixteen-steps) stages them | step 3; [the storage design's first question](./tillr-storage.md#OQ-TS1) |
| 17 | Every design answer's documents land when it ends | step 14 |

**State that already exists.** Runs recorded before step 3 are imported as done nodes, one per run in order, with endings mapped from their records and marked as imported. An imported ending with no successor is answered by the rule table like any other, so items parked under the drain's holds resume; the running-agent limit bounds how fast. A session that no longer exists becomes a backend move by row 18.
---

## 14. Risks

| Risk | What is done about it |
| :--- | :--- |
| **A chain that never repeats and never objects** runs without limit | Shown at the top of "draining", which is ordered by spend; the owner can object at any time. There is no spend target. See [Section 7](#7-what-stops-repetition) and [OQ-GP3](#OQ-GP3) |
| **Resumed sessions grow** until each turn is expensive | The scheduler may compact before a resume; a diagnosis may name a fresh start on a stronger seat. Cost is recorded per node, so the growth is visible |
| **The rule table becomes a second program** as rows accumulate | Every row has a fixture, rows may not overlap, and a row can read only the graph and the listed facts. A row that needs a new fact is a design change |
| **Adapters that cannot state an ending** leave everything `missing-output` | The first slice keeps the report footer as one source of the ending record for each adapter, replaced adapter by adapter |
| **A design answer on a branch conflicts at landing** with another item's | It is routed to the design session, and its objection adds the dependency. The queue planner exists for the cases worth preventing |
| **The waiting rule is only as good as each check's declared inputs** | A check with no declared inputs is treated as reading the whole tree, which holds more, never less |
| **A revert removes a landing that was right**, because a check is wrong | A revert lands only when the failing check passes with it, and a red at the green pointer is never attributed to a landing |
| **Hoisting changes fork behavior** and a pi upgrade breaks the reader | Observed nodes degrade to "not recorded" for that run; nothing routes on them |
| **Migration releases every parked item at once** | The running-agent limit bounds it, and the release is the point: those items were parked by the mechanisms being removed |

---

## Decision Ledger

The `OQ-GP` and `R-GP` rows are owner rulings, except [OQ-GP2](#OQ-GP2), which was closed without one. The `D-GP` rows are the designer's engineering decisions, made so that the design is complete; the owner may overturn any of them, and by [`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21) they may be built on meanwhile. The Built column is read from the tree.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-GP1` | An item has one graph for its life, and every act on it is a node. "Attempt" is not used. **Rejected:** a new graph for each attempt chosen by the last one's cause, which keeps the gate and the landing outside the graph and throws finished nodes away at each reset. | 2026-11-17 | [Section 3](#3-the-model) | — |
| `D-GP2` | A node is in fog, then fixed at its claim, then done at its ending. Liveness bookkeeping is never fixed while it runs. **Rejected:** fixing a node only when it emits output, which leaves a window in which a running process can have its definition changed. | 2026-11-17 | [Section 3.2](#32-what-is-fixed-and-when) | — |
| `D-GP3` | An exception resumes the same node and is logged; an event adds nodes. A process that ends by itself and states nothing is an event. **Rejected:** a node for each interruption, which fills the path with noise the owner ruled out. | 2026-11-17 | [Section 3.6](#36-events-and-exceptions) | — |
| `D-GP4` | "Read" means recorded as a premise, and the store checks premises in the write that applies a change. **Rejected:** treating everything a process loaded as read, which cannot be known and would freeze the whole graph. | 2026-11-17 | [Section 4.1](#41-premises) | — |
| `D-GP5` | All routing is one pure function per definition, returning a transformation and waits. Waits are derived and never stored. **Rejected:** conditions on edges, which hide routing in templates and need a second language. | 2026-11-17 | [Section 5](#5-the-policy-function) | — |
| `D-GP6` | Scheduling is apart from policy. A node names a seat; seats are declared in the backend policy; no rungs. **Rejected:** a model or backend field on a node, which breaks one-edit provider changes. | 2026-11-17 | [Section 6](#6-scheduling) | — |
| `D-GP7` | The gate, the judge and the landing are nodes. The lander stays the one program that moves the trunk. **Rejected:** keeping them as a bracket at the graph's exit, which is why refusals and conflicts have no producer to return to. | 2026-11-17 | [Section 3.1](#31-node-kinds) | — |
| `D-GP8` | Every node that needs the worktree starts on the latest trunk, by a take-in that is itself a node. A conflict is routed, never forced. **Rejected:** taking the trunk in whenever it moves, which is a trigger. **Amended on 2026-11-21 by the owner's ruling recorded as [gate reference `D-GR24`](../reference/gate.md#why-its-this-way):** the first node of a new item starts on the last proved trunk commit, after its blocker's landing is proved, and a trunk fixer's starts on the tip. Whether a later node's take-in brings in the latest trunk or the last proved commit is not in the owner's words; the designer keeps the latest trunk as proposed and the owner may overturn it. | 2026-11-17 | [Section 3.7](#37-the-item-branch-and-the-latest-trunk) | — |
| `D-GP9` | Definition edits are committed on the item branch, and the worker resumes from them there; the definition in force is the latest revision recorded in the graph; design answers and triage may revise a definition. **Amended:** no path list is required, and this is not permission for a worker to edit an item comment. A copy of a document may land ahead of the item. **Rejected:** cutting a new worker from the trunk after a design lands by itself, which is the source of stale workers. | 2026-11-17 | [Section 3.7](#37-the-item-branch-and-the-latest-trunk) | — |
| `D-GP10` | An objection is the one ending that changes the actor, and it names the next one. The owner is named only with a written question. **Rejected:** any default mover. | 2026-11-17 | [Section 3.3](#33-endings) | — |
| `D-GP11` | An identical repeat, by cause and evidence key against the whole chain, is answered by a diagnosis that must change something. **Rejected:** comparing only to the previous ending, which a two-step alternation defeats. | 2026-11-17 | [Section 7](#7-what-stops-repetition) | Partly: the drain compares an ending with every earlier ending of the item (`repeatedEnding`). No diagnosis node |
| `D-GP12` | A sub-agent tillr can see is a node within its parent: observed when tillr only recorded it, dispatched when tillr started it. A child's scope is inside its parent's. **Rejected:** ending the parent's node at every spawn and resuming it after, which is purer and pays a suspend and a resume for every child. | 2026-11-17 | [Section 9](#9-subagent-hoisting) | — |
| `D-GP13` | The queue planner owns one class of inter-item edge and the rank. Its edges constrain only future claims. It runs on events that cross items. **Rejected:** a planner that may pause or re-plan running work. | 2026-11-17 | [Section 8.1](#81-the-queue-planner) | — |
| `D-GP14` | Only an ended proof makes the trunk red. A candidate waits only when it changes the inputs of a failing check and does not make it pass. Proof outranks new work. **Rejected:** the red cap, which holds on elapsed time and on "unproven"; and no rule at all, which lets changes land into a failing check's inputs. The waiting rule is the owner's as [OQ-GP7](#OQ-GP7). | 2026-11-17 | [Section 8.2](#82-proof-and-a-red-trunk) | Partly: the waiting rule, as a drain rule. No proof nodes |
| `D-GP15` | Migration records each act as a node before the rule table routes it, and moves mechanisms in by the runs they waste. **Rejected:** building the whole model beside the drain and switching over. | 2026-11-17 | [Section 13](#13-migration-order) | — |
| `D-GP16` | **Designer's decision; the owner may overturn this.** When an item's plan places two or more writing nodes in parallel ([`R-GP1`](#R-GP1)), each concurrent writer stages an isolated checkout at `<lane-root>/<item-id>/nodes/<node-key>/worktree` on branch `tillr/<scope>-nodes/<item-key>/<node-key>` (with node scratch at `<lane-root>/<item-id>/nodes/<node-key>/scratch/<run-id>` counted against [`D-VO10`](./tillr-vocabulary.md#34-the-bound-and-what-happens-at-it)) cut from their shared item-branch frontier commit `<base>`, commits to a pinned node ref under the disjoint prefix `refs/tillr/item-nodes/<item-id>/<node-key>` (fallback `refs/tillr/item-nodes-long/<sha256>`, amended by [`D-GP19`](#D-GP19) so it never collides with `refs/tillr/items/<item-id>`), and reconverges at an explicit `join` stage that three-way merges (`git merge-tree --write-tree`) the completed sibling trees onto the item branch in deterministic graph order and deletes the per-node checkouts once joined. **Rejected:** sharing one working tree across concurrent writers, which races on the Git index; pinning beneath `refs/tillr/items/<item-id>` or `refs/heads/tillr/<scope>/<item-id>`, which Git forbids as a D/F ref collision; and creating a separate queue item per parallel piece, which breaks one-item-one-deliverable (`D-IG2`). | 2026-11-23, amended 2026-11-27 | [Section 3.8](#38-concurrent-writers-checkouts-and-the-join) | — |
| `D-GP17` | **Designer's decision; the owner may overturn this.** When `join` hits overlapping edits that `git merge-tree --write-tree` cannot merge cleanly, `join` records `<base>`, every sibling node's id and pinned commit SHA on `refs/tillr/item-nodes/<item-id>/<node-key>`, and the conflicting paths (discarding neither writer's commit), and routes to an intra-item conflict-reconcile writer node on the primary item worktree before `verify`, `judge` and `gate run` — spliced directly onto the workflow graph as `<join-id>-reconcile` ahead of Step 3 per [`D-GP19`](#D-GP19), and added by rule row 28 once the policy function lands (with `plan-wrong:` objections returning to `plan`/`design` and identical repeats routing to `design` under `D-GP11`). **Rejected:** auto-picking one sibling's edit or discarding a sibling commit; and sending a first intra-item join conflict straight to the owner (`R3`). | 2026-11-23, amended 2026-11-27 | [Section 3.8](#38-concurrent-writers-checkouts-and-the-join), [Section 5.3](#53-the-starting-rule-table) | — |
| <a id="D-GP18"></a>`D-GP18` | **Designer's decision, from a filed request; the owner may overturn this.** A candidate that started on the last proved commit is gated with its own tree's checks, as built; that is not changed. When a gate refusal names a failing check the trunk tip no longer declares, the refusal opens by saying the trunk has removed the check since the run's base and that this is not a defect in the change, the lane is taken onto the tip, and no fixer is filed. Failing checks still declared at the tip stay the candidate's and are named apart. **Rejected:** starting a new item on the tip, which contradicts the owner's ruling in `D-GR24`; holding a new item until the tip is proved, which treats an unproved trunk as red (R10); gating with the tip's check set or dropping the absent check's failure and landing, which gives a verdict that does not cover what it ran; and changing the text alone, which leaves the next gate run to fail the same check on the same base. | 2026-11-25 | [Section 3.7](#37-the-item-branch-and-the-latest-trunk) | — |
| <a id="D-GP19"></a>`D-GP19` | **Designer's decision answering the Slice 5 conflict against a filed request (not an owner ruling; the owner may overturn this).** Settles the pre-migration workflow, ref and drain interfaces for Slice 5 of [Section 13](#13-migration-order) ([`D-VO19`](./tillr-vocabulary.md#D-VO19)): (1) concurrent writer pins use `NodePinRef` at `refs/tillr/item-nodes/<item-id>/<node-key>` (fallback `refs/tillr/item-nodes-long/<sha256>`) and worktree branches use `NodeLaneBranch` at `tillr/<scope>-nodes/<item-key>/<node-key>` with single-component `NodeKey(nodeID)` (`<sanitized>-<sha256[:8]>`), excused in `LanderRef` and `loopPlaces`, retained in `sweepRefs`, protected while active in `collectIdleWorktrees`, and counted in `worktreebudget.MeasureLaneRoot`; (2) `WorkflowPiece` adds optional `after` (`*[]string`) where omitted `after` preserves sequential list-order piece edges in `repeat: "pieces"` (`build-pieces@v1`, `planned-build@v1`) with no `join`, explicit `"after": []` / `"after": ["<piece-id>", ...]` declares DAG/parallel piece edges, and `expandWorkflow` (plus `replaceProposalGraph`) synthesizes a `kind: "join"` stage (`access: "write"`, no `seat` or `command_ref`, accepted by `DecodeWorkflow` and `lint_workflow`) across unordered `"write"` fan-outs; (3) `hoisted child` writers stay in Step 13 (`rg -n 'hoisted' tools/tillr-drain/` returns nothing today), while `tillr-drain` executes `kind: "join"` in-process (`git merge-tree --write-tree --merge-base=<base>` + `git commit-tree -p <base>` with the repository's configured Git identity and `Trunk.Squash`-format combined commit message, excluded from `evaluatorCandidate`'s agent-session writer list), recording `node_runs.outcome = 'passed'` on clean merge or `'handled-fail'` on overlap while splicing `<join-id>-reconcile` (`seat: "build"`, union `write_scope`) via `graph_revisions` + `drain.graph-changed` (or routing an identical repeat join-conflict key to `design`). Supplemented by [`D-GP20`](#D-GP20). **Rejected:** moving shipped `ItemPinRef` off `refs/tillr/items/<item-id>`; treating `pieces` without `after` as unordered parallel writers; externalizing `join` into `config/tillr/commands.json`; or holding Slice 5 on Steps 2, 3 and 13. | 2026-11-27, designer | [Section 3.8](#38-concurrent-writers-checkouts-and-the-join), [Section 13](#13-migration-order) | — |
| <a id="D-GP20"></a>`D-GP20` | **Designer's decision from an operator retry instruction and a Slice 5 `write_scope` conflict against a filed request (not an owner ruling; the owner may overturn this).** Settles `just pack-sync` `write_scope` derivation and `join` mirror-conflict handling for Slice 5 ([`D-VO20`](./tillr-vocabulary.md#D-VO20)): (1) while the pre-Slice-5 deployed loop built from `refs/tillr/green` executes Slice 5's own `planned-build@v1` graph (`rg -n 'support-revision\|config/tillr/workflows' tools/tillr-drain/workflow/` returns nothing today), every writing piece in `args.pieces` that edits `tools/tillr*`, `config/tillr/workflows/`, or `yolo-packs/tillr/` must explicitly include `"tools/tillr/buildstamp/support-revision"` and `"yolo-packs/tillr/"` in its `write_scope`; (2) in Slice 5, `FencePaths` (`tools/tillr-drain/workflow/workflow.go`) idempotently derives `yolo-packs/tillr/files/local/share/tillr/tillr/buildstamp/support-revision` from `tools/tillr/buildstamp/support-revision` before `tillrModule` (without widening into `tools/tillr/`), derives `yolo-packs/tillr/files/local/share/tillr/workflows/`, `tools/tillr/buildstamp/support-revision` and `yolo-packs/tillr/files/local/share/tillr/tillr/buildstamp/support-revision` from `config/tillr/workflows*`, and derives `tools/tillr/buildstamp/support-revision` and `yolo-packs/tillr/files/local/share/tillr/tillr/buildstamp/support-revision` alongside existing companions for `tillrModule` (`tools/tillr*` other than `tillrDocsTest` and `support-revision`) and `yolo-packs/tillr/programs*` (`TestFencePathsDeriveTheGateCompanions` in `tools/tillr-drain/drain/fence_test.go`); (3) `join` filters `git merge-tree --write-tree` `CONFLICT` lines with `boundary.FilterMirrorConflictReports`, resets mirror-only conflicts (`len(nonMirror) == 0`) to `<base>` (`restorePackMirrorPaths`), and runs `boundary.StagePackMirror` on the primary item worktree (when `HasPackManifest` holds) before writing the single-parent joined commit across two or more changed siblings, omitting generated mirror markers (`skipPackMirror = true`) on genuine `<join-id>-reconcile` conflicts. **Rejected:** omitting `pack-sync` in piece runs; letting `support-revision` fall into `tillrModule` and break `FencePaths` idempotence; or splicing `<join-id>-reconcile` on generated `support-revision` / pack-mirror conflicts. | 2026-11-27, designer | [Section 3.8](#38-concurrent-writers-checkouts-and-the-join), [Section 13](#13-migration-order) | — |
| `OQ-GP1` | **Owner ruling.** tillr reverts a landing by itself when the full suite proves it broke the trunk: the revert lands once it applies cleanly and the failing check passes with it, and the session that wrote the change gets the failure back. A mechanic is filed only when a revert is unsafe, does not repair, or the red is unattributed. The gate reference records it as `D-GR6`. | 2026-11-17 | [Section 8.2](#82-proof-and-a-red-trunk) | Yes, as a drain rule (`auto_revert`). Not as nodes |
| `OQ-GP2` | **Closed as superseded; not a ruling on the structure.** The question asked whether one process should own every write to the store. [The storage design](./tillr-storage.md) holds that choice as [its first question](./tillr-storage.md#OQ-TS1). This design depends only on that document's interface. | 2026-11-18 | [Section 10](#10-storage) | — |
| `OQ-GP3` | **Owner ruling.** There is no spend target. Spend is recorded for each node and orders the "draining" list; nothing acts on it. | 2026-11-17 | [Section 7](#7-what-stops-repetition) | Partly: spend is measured without stopping dispatch. The ordering of the list was not checked |
| `OQ-GP4` | **Owner ruling.** The queue planner's agent runs on evidence that crosses items (a conflict between items, a worker naming another item) and on the owner's request, not at intake. | 2026-11-17 | [Section 8.1](#81-the-queue-planner) | — |
| `OQ-GP5` | **Owner ruling.** tillr watches every sub-agent it can see and starts scouts and writers itself; the rest are watched only. | 2026-11-17 | [Section 9](#9-subagent-hoisting) | — |
| `OQ-GP6` | **Owner ruling.** A question for the owner lands on the trunk with the design text that changed. The operation is designed in [early design landing](./tillr-early-design-landing.md), whose [first question](./tillr-early-design-landing.md#OQ-EL1) is ruled "always": every design answer's documents land this way (R11, rule rows 12 and 27, migration step 17). | 2026-11-17 | [Section 3.7](#37-the-item-branch-and-the-latest-trunk) | — |
| `OQ-GP7` | **Owner ruling.** When a check is already red on the trunk, finished work that did not cause it lands only if it does not touch what the red check reads. Each check declares the paths it reads; an item whose changes fall outside them lands with the verdict "passed, with inherited failures", and one that changes them waits unless it makes the check pass. There is no landing cap. A waiting item is shown with the paths that put it there. A red on a whole-tree check still holds every item. This is the rule of `D-GP14`. | 2026-11-18 | [Section 8.2](#82-proof-and-a-red-trunk) | Yes, as a drain rule (`pass-inherited`, `trunk-already-red`) |
| `R-GP1` | **Owner ruling.** Execution data (what the stored tables call a run) belongs directly to the node in the item's graph and is not an entity beside the graph (confirming [`D-VO3`](./tillr-vocabulary.md#D-VO3)). Whether writing nodes of one item run in parallel is up to the item's plan (overturning the serial-writer rule of [`OQ-IG3`](../reference/tillr-item-graphs.md#why-its-this-way)). Per-writer checkouts, joins and conflict reconciliation are the designer's `D-GP16` and `D-GP17`. | 2026-11-23 | [Section 3.2](#32-what-is-fixed-and-when), [Section 3.8](#38-concurrent-writers-checkouts-and-the-join), [Section 12.1](#121-the-item-graph-ledger) | — |
| `R-GP2` | **Owner ruling.** Lanes have no life of their own: an item is scheduled against the running-agent limit ([`D-VO6`](./tillr-vocabulary.md#35-what-limits-how-many-agents-run-at-once)), runs, and acquires its worktree when its execution needs one (confirming [`OQ-VO1`](./tillr-vocabulary.md#OQ-VO1) and [`OQ-VO2`](./tillr-vocabulary.md#OQ-VO2)). Worktree bounds, eviction order and the Observatory surfaces are the designer's [`D-VO5`](./tillr-vocabulary.md#decision-ledger), [`D-VO10`](./tillr-vocabulary.md#decision-ledger) and [`D-VO16`](./tillr-vocabulary.md#36-what-the-observatory-shows-instead-of-lanes). | 2026-11-23 | [Section 6](#6-scheduling), [Section 11](#11-the-view), [Section 12.4](#124-audit-findings) | — |

---

## Open Questions

None open. Every ruling is in [the ledger](#decision-ledger) above.
