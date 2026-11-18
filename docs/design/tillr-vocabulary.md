---
title: "One word per thing: the vocabulary of tillr's items, nodes, runs, sessions and worktrees"
status: in-review
stage: DESIGN
updated: 2026-11-18
tags:
  - tillr
  - vocabulary
  - glossary
  - worktrees
  - design
summary: "Settles one meaning for each word the tillr work queue uses, so that the owner, the documents and the code say the same thing. An item is a tracked piece of work with its own deliverable, whether or not a document declares it yet. The unit of progress is the node, the whole of what happened to an item is its path, and a run is only the bookkeeping of one node. The lane is retired: a worktree is a resource an item acquires, with a lifecycle and collection in place of a fixed pool."
next: "The owner rules OQ-VO1 to OQ-VO4. A link from the tillr roadmap is owed so those questions are routed. The amendments in Section 7 are then applied to the graph design by its author, and the drafted items in Section 9 get their directives."
depends-on:
  - tillr-graph-is-the-path.md
---

# One word per thing: the vocabulary of tillr's items, nodes, runs, sessions and worktrees

**Status:** written 2026-11-18 from a read of the tracked documents and the code's tables, ids and event kinds on that date. Nothing here is built; where the code uses a word differently, the entry says so.

> **In short:** Three words carried several meanings each (item, run, lane), and that is why the stages of a change could not be told in one breath. This document gives each word one meaning, retires the words that had none left, and names the stages of a change once.

- **Why it matters:** the owner asked on 2026-11-18 what an item that lands a change delivers, and the answer needed "run" in three senses and "lane" in four.
- **The shape:** an **item** has one **graph**; the graph's started nodes are the item's **path**; a **node** has at most one **run**; an agent run speaks in one **session**, which later nodes resume or fork; an item acquires one **worktree**, which is collected and restored.
- **Cost:** "lane", "attempt", "node run", "hold" and four other words are retired, and the graph design's lane paragraph is replaced ([Section 7](#7-amendments-to-the-graph-design)).
- **Start at:** [Section 4](#4-the-stages-of-a-change) for the stages, then the three sections before it for the three words.
- **Needs your ruling:** [OQ-VO1](#OQ-VO1), [OQ-VO2](#OQ-VO2), [OQ-VO3](#OQ-VO3), [OQ-VO4](#OQ-VO4).
- **Reads with:** [the graph design](./tillr-graph-is-the-path.md) (the accepted direction these words must fit; its own terms are linked here, not redefined).

---

## 1. What an item is

**An item is one piece of work the queue tracks under one id, with one deliverable and one ending.** It exists from the moment it is recorded. A document block is not what makes it an item: the block is the written form of the item's definition, and an item gains one during the definition stage.

That removes the error in the earlier definition, "work declared in tracked Markdown" (the ["Read this first" paragraph](../tillr/design-status.md)). The store already holds items that no document declares, and the triage procedure exists to give such an item its block.

**The test for "is this an item?"** Does it have a deliverable of its own that lands on the trunk, and an ending of its own? Then it is an item. Does it act on another item's deliverable? Then it is a node in that item's graph.

| What exists today | In this vocabulary | Why |
| :--- | :--- | :--- |
| Something filed with no block (`tillr item apply`) | An item; specifically a **request** | It is the work. Giving it a block is a triage node in its own graph |
| The owner's saved words (`item-idea-…`) | An item; specifically an **idea** | Its deliverable is the blocks or the questions it lands |
| An item declared by a block | An item | The ordinary case |
| Trunk fixer (`fixer-main-…`) | An item; a **generated item** called the **mechanic** | It lands its own commit and has its own criterion |
| Tooling-defect item (`defect-…`) | An item; a generated item | It lands its own commit, and other items wait on it |
| Comment item (`item-comments-…`) | An item; a generated item | It lands its own replies |
| Rewrite item (`rewrite-<id>`) | **Not an item.** A rewrite node in the original item's graph | It changes the original's block and delivers nothing of its own. The stored item is a stand-in until the graph design's rewrite kind is built |
| Revert (`revert-main-…`) | **Not an item.** A land attempt node in the graph of the item it reverts | It carries no model and no deliverable. The code already stores no item for it |
| Triage, design answer, conflict fix | **Not items.** Nodes | Each acts on its item's definition or branch |
| Gate-budget alarm (`gate-budget-…`) | **Neither.** A reported measurement | The loop files none now, and a measurement has no deliverable |

So a generated item is an item in every respect but one: it has a definition (its criteria, held in the store) and never a block. Whether the owner wants that work under the same word is [OQ-VO3](#OQ-VO3).

---

## 2. What a run is

**A run is the execution of one node by a program tillr starts. A node has at most one, for its whole life.** It is bookkeeping: the processes, the log, the lease and the spend. It is never the unit in which an item's progress is told.

"Run" had three meanings: one process execution (the `runs` table), an item's whole try (the observatory's help text: "one recorded attempt by an agent to work on an item in a lane"), and a node's single execution across crashes ([the graph design](./tillr-graph-is-the-path.md#32-what-is-fixed-and-when)). This document keeps the third, which contains the first, and gives the second its own word.

| The question | The word | Notes |
| :--- | :--- | :--- |
| What is the whole thing that happened to an item? | Its **path** | The started nodes of its graph, in order |
| What is the unit of progress? | The **node** | One act. "Item X is at node 7 of 10" |
| What is one agent's execution? | An **agent run** | The run of an agent node |
| What is a command's execution? | A **command run** | The run of a command node. No model, no session, no spend |
| What is the conversation? | A **session** | One harness conversation. It outlives the node that started it |
| What is one process lifetime? | A **segment** | A run that was killed twice has three segments and is still one run |

**How a session spans nodes.** A session is an asset a node leaves behind. A **resume** node continues it with one new message: same session, new node, new run. A **fork** node starts from a copy: a new session whose history begins as the old one's. A **backend move** starts a new session that reads the old transcript as a file. Recovery after a crash is none of these: it is the same node, the same run and, where the harness can reopen it, the same session.

**The owner's example: one item whose graph has ten nodes, eight agents and two commands.**

- **One item, one graph, one path.** That is the answer to "how is it going": the path so far, and the stage it is in.
- **Ten nodes:** eight agent nodes and two command nodes.
- **Ten runs:** eight agent runs and two command runs, one per node. That is correct as bookkeeping and it is what the store holds today: ten `runs` rows and ten `node_runs` rows, with ids `run-<item>-a1-n1` to `-n10`. Nobody counts them. "Eight runs for one item" sounded wrong because "run" was also the word for the item's whole try; that sense is now the path.
- **Between one and eight sessions.** Each agent node either starts a session or resumes one. The two command nodes have none.
- **If node 6 is killed twice by a host restart:** still ten nodes and ten runs. Run 6 has three segments and two exception records.
- **If the gate refuses the result:** an eleventh node is added, a resume of the node that wrote the change, with an eleventh run and no new session. The path grows; nothing restarts, and there is no second "attempt".

Under the graph design the take-in, the gate run and the land attempt are nodes too, so the same item's path shows them after the ten. Today they are a bracket outside the graph with no node of their own.

---

## 3. What a lane was, and the worktree as a resource

**The word "lane" is retired.** It named four things at once: a slot in a fixed pool, a git worktree, a directory of per-run scratch, and a lease. Each now has its own word: the **running-agent limit**, the **worktree**, the **scratch** and the **lease**. Whether the word goes outright is [OQ-VO1](#OQ-VO1).

The owner ruled on 2026-11-18 that a worktree is a resource acquired by the entity that needs it, most likely the item, and that the bound on outstanding worktrees is met by a lifecycle and collection, not by a hard cap that work is shuffled through. This section designs that. It supersedes the graph design's "a lane belongs to a running node"; the exact passages and their replacements are in [Section 7](#7-amendments-to-the-graph-design).

### 3.1 Where the disk goes

Measured on 2026-11-18 in this repository, with twenty lanes:

| Measurement | Command | Result |
| :--- | :--- | :--- |
| One lane's worktree | `du -sh .tillr/lanes/lane-1/worktree` (and lanes 2 and 3) | 62 MB each |
| One lane's per-run scratch | `du -sh .tillr/lanes/lane-1/durable` (and lanes 2 and 3) | 18 GB, 14 GB, 12 GB |
| All lanes | `du -sh .tillr/lanes` | 133 GB |
| Worktrees registered | `git worktree list` | 21, the main checkout and twenty lanes |
| Code that deletes per-run scratch or run logs | `rg 'RemoveAll' tools/tillr-drain tools/tillr-run tools/tillr-land --glob '!*_test.go'` | None: only worktree clearing, the full-suite worktree and temporary directories |

Twenty worktrees are about 1.2 GB. The other 130 GB is scratch: test binaries and build output of runs that ended long ago. A lifecycle that covers only worktrees would fix one percent of the problem, so the scratch is part of the resource.

### 3.2 Who holds what

- **A worktree has exactly one owner.** For item work the owner is the item. The full suite and a revert each use a worktree owned by their own node, deleted at that node's ending. A worktree with no owner is a defect and is collected.
- **An item has at most one worktree,** a checkout of its item branch at a path derived from the item's id. Every node of the item that needs files runs there. Two writing nodes of one item never run at once; read-only nodes of one item may share it.
- **An item has one scratch directory beside its worktree,** outside the tracked tree. It is a cache. Nothing in it is evidence, and a node's declared outputs are copied out of it at the node's ending. Nothing in scratch is promised to survive the node that wrote it.
- **The item branch is not the worktree.** The store pins the branch head, so deleting the worktree loses no commit.

### 3.3 The life of a worktree

| State | Meaning | Enters when |
| :--- | :--- | :--- |
| **None** | The item has no worktree | The item is recorded |
| **In use** | A node of the item is running in it | The claim of a node that needs it. The claim creates it from the latest trunk, or restores it at the item-branch head, then takes the trunk in |
| **Idle** | It exists and no node of the item is running | The last node running in it ends |
| **Collected** | The directory and the scratch are deleted. The branch head is pinned and the worktree can be restored | Collection (below) |

Restoring puts the worktree back at the same path, because some harnesses find a session by its working directory (not verified per adapter on 2026-11-18). A session must not live only in a directory that collection deletes: its id and transcript location are part of the ending of the node that produced it.

**Collection runs at three moments and on no timer:** when a node ends, when an item ends, and before a claim that needs a worktree. It deletes, in this order:

1. **The worktree and scratch of an ended item** (proven, resolved elsewhere or closed). Always, at once.
2. **The worktree and scratch of an idle item whose wait names the owner or another item.** Always, at once: those waits are long, and restoring costs one checkout.
3. **Other idle worktrees, least recently used first,** only while the bound of [Section 3.4](#34-the-bound-and-what-happens-at-it) is exceeded. An item between its build and its gate keeps a warm worktree unless disk is short.

A landed item's worktree is idle, not ended: a revert sends the failure back to the session that wrote the change, which resumes there. It is collected for good when the landing is proven.

| Kept after collection | Deleted by collection |
| :--- | :--- |
| The item-branch head, pinned by a ref | The working directory |
| The path: every node, ending and exception record | The scratch: build output, test binaries, temporary files |
| Each node's declared outputs and its report | Untracked files in the worktree |
| The run logs, and the sessions with their transcripts | |

Run logs of ended items are records, not scratch. They are kept, and their total is reported beside the worktree figures; a bound on them is not designed here.

**Failure paths.** A collection that cannot delete a directory leaves it, reports it, and tries again at the next collection; nothing is counted. A restore that fails is an exception: the node has no ending and the same node resumes. Collection and claim are serialized per item, and a claim wins: an in-use worktree is never collected, and neither is one whose running node was interrupted, because that node has no ending yet.

**State that already exists.** Every per-run scratch directory under `.tillr/lanes` belongs to a run that has ended or to a lane no item owns. All of it is collectable on the first day, which frees about 130 GB here.

### 3.4 The bound, and what happens at it

The bound is on **disk first and count second**: a byte budget over all worktrees and scratch, and a count of worktrees on disk. The disk budget is the one that binds in practice, by the measurements above. The starting values and the choice itself are [OQ-VO2](#OQ-VO2).

**At the bound, no work fails.** Idle worktrees are collected until the total is under the bound. If every worktree is in use, no new one is created: the node that needs one stays in fog, and the wait it produces names its mover, the ending of a node now running in a worktree, by that node's id. A running node is never stopped for exceeding the budget; the overshoot is reported with the largest scratch directories named.

### 3.5 What limits how many agents run at once

The pool used to do this by accident: `--lanes N` meant N agents because it meant N worktrees. The two are now separate settings.

- **The running-agent limit** is the number of agent runs in progress at once, overall and per backend. It is a scheduling setting in the sense of [the graph design's scheduling section](./tillr-graph-is-the-path.md#6-scheduling): it ends, refuses and re-routes nothing. Its default is today's `--lanes` value.
- **At the limit,** ready nodes stay in fog, and the wait names the next ending of a running agent node as its mover.
- **Work the owner is waiting on** runs outside the limit, as `D-IG10` of [item graphs](./tillr-item-graphs.md#71-decision-ledger) rules. That replaces "express lanes".
- **Command runs** do not count against it. Landing is one at a time because one program moves the trunk; how many gate runs go together is a resource setting whose default is the implementer's to measure.

---

## 4. The stages of a change

**What an item that lands a change delivers:** one squashed commit on the trunk, inside the item's fence, that makes every criterion true, passes the gate and survives the proof. Its block is deleted in that same commit, because a block declares open work only.

**Stage** *(coined here for this sense)* is one of the eight named steps below. A step of a workflow, which item graphs called a stage, is a **step**.

| # | Stage | Who acts | What it produces | Each kind of "no", and where it goes |
| :--- | :--- | :--- | :--- | :--- |
| 1 | **Recorded** | The owner, an agent, or the loop for a generated item | An item: an id and what was asked | None |
| 2 | **Definition** | The design seat (triage, design answer); a cheap seat (rewrite); the owner, by answering | The item's definition: fence and criteria, as a block | An **objection** naming the owner, with a written question: the item is in "needs you" until he answers. A finding of duplicate or already done: the item ends **resolved elsewhere** or **closed** |
| 3 | **Work** | The build or design seat | Commits on the item branch; the head is the **candidate** | An **objection**, with one **obstacle kind**: definition, fence, trunk, tooling, seat, owner. It goes to the actor it names. The work itself ends `submitted` |
| 4 | **Gate** | Commands: the take-in, then the gate run | A **gate verdict** bound to the candidate as taken in | A **refusal**, with the failing checks, back to the session that wrote the change. A **conflict** at the take-in, with paths, back to the session that last wrote them |
| 5 | **Judgment** | A judge: a read-only agent that did not write the candidate | A **criterion verdict** for each criterion | A fail with findings, back to the session that wrote the change |
| 6 | **Landing** | A command: the lander | One commit on the trunk tip. The item is **landed** | A **conflict** with paths, or a lander **refusal** with a reason from its closed list. Both go back to the session that wrote the change |
| 7 | **Proof** | A command: the full suite on a trunk commit | A green pointer that has moved to or past the landing | **Reverted:** the proof named this landing, an automatic revert landed, and the failing check's output goes back to the session that wrote the change. If a revert is unsafe or does not repair, the mechanic is filed instead |
| 8 | **Proven** | Nobody | The item's ending, **done**. The proven commit is pushed | None |

Where the judge sits relative to the gate is the workflow's choice; both come before the landing. A gate or a suite that could not run is not a "no": it is an exception, and the same node resumes.

```mermaid
flowchart TD
    R[1 Recorded] --> D[2 Definition]
    D --> W[3 Work]
    W --> G[4 Gate]
    G --> J[5 Judgment]
    J --> L[6 Landing: landed]
    L --> P[7 Proof]
    P --> V[8 Proven: done]
    D -. objection naming the owner .-> O[Needs you]
    O -. answer .-> D
    W -. objection .-> D
    G -. refusal or conflict .-> W
    J -. fail .-> W
    L -. conflict or refusal .-> W
    P -. reverted .-> W
```

**Landed and proven are different, because the landing strategy is after-merge.**

| | Landed | Proven |
| :--- | :--- | :--- |
| What is true | The lander put the item's commit on the local trunk | A proof passed on a trunk commit that contains the landing, and no revert removed it |
| What the item has delivered | The change, provisionally: later work builds on it, and it can still be reverted | The change, finally |
| Pushed | No | Yes. Only the proven commit is pushed (assumed for now) |
| The item | Not ended. Its worktree is idle | Ended **done**. Its worktree and scratch are collected |
| Where it shows | "Draining" | Finished |

Today the stored item state `landed` is final and there is no state for proven; the difference lives only in the green pointer. That is the largest gap between these words and the store ([Section 6](#6-renames)).

---

## 5. The words

Each term has one meaning. A term owned by another document is linked, not redefined. "Today" marks where built code or an accepted document uses the word differently.

### 5.1 The work and its statement

| Term | What it is | What it is not | Identified by; belongs to; has | Today's usage differs |
| :--- | :--- | :--- | :--- | :--- |
| **Item** | One piece of work the queue tracks under one id, with one deliverable and one ending | Not a document block, and not anything that acts on another item's deliverable | Its id, chosen by its author or made by the tool that filed it. Has one definition, one graph, one item branch, at most one worktree | [design-status](../tillr/design-status.md): "declared as items in tracked Markdown" |
| **Request** | An item that has no block yet | Not the record handed to `tillr run exec`, and not a row of the idempotency table | Any item id | The code also says request for `run.Request` and `item_requests` |
| **Idea** | A request saved from the owner's own words, whose deliverable is the blocks or questions it lands | Not a kind column; none is built | `item-idea-` and ten hash digits | — |
| **Generated item** | An item the loop files for work nobody asked for by hand: the mechanic, a defect item, a comment item | Not a rewrite, a revert or an alarm ([Section 1](#1-what-an-item-is)) | An id prefix. Has criteria and no block | The code's generated set also holds `rewrite-`, `revert-main-` and `gate-budget-` |
| **Mechanic** | The owner's word for the generated item that makes a red trunk green | Not a revert | `fixer-main-` and twelve hex digits of the red commit | Code and ids say "fixer" |
| **Defect item** | The generated item filed when a worker's objection names broken tooling | Not a failure of the item that reported it | `defect-` and twelve hex digits | — |
| **Definition** | What an item is to deliver: title, reason, fence, criteria and dependencies | Not the layer that turns the core into one use (the **rulebook**), and not a node's kind and brief | Belongs to one item. The one in force is the latest revision in the item's graph | Three meanings; see [Section 6](#6-renames) |
| **Block** | The written form of a definition: a heading and an item directive in a tracked document | Not the item. An item can exist without one | The item id in the directive | — |
| **Criterion** | One testable condition of a definition | Not a gate check | An id within its item | — |
| **Fence** | The path prefixes an item may change | Not a node's write scope, which is a subset | The block's `paths` attribute | — |
| **Deliverable** | The one squashed commit an item puts on the trunk | Not a fork's delivery, which is the patch series that deploys | The landing's trunk commit | — |
| **Rulebook** *(coined here)* | The data that turns tillr's general machinery into one use: item states, allowed moves, prerequisites, the completion rule and the rule table | Not an item's definition | A name, such as `git-landing` | Called "a definition", as a stated placeholder, in [the direction document](./tillr-core-and-git-landing.md). [OQ-VO4](#OQ-VO4) |

### 5.2 The graph, and what executes

`Item graph`, `in fog`, `fixed`, `done`, `claim`, `ending`, `asset`, `event`, `exception`, `premise`, `fact`, `policy function`, `take-in` and `seat` keep the meanings of [the graph design's word table](./tillr-graph-is-the-path.md#2-the-words).

| Term | What it is | What it is not | Identified by; belongs to; has | Today's usage differs |
| :--- | :--- | :--- | :--- | :--- |
| **Path** *(coined here; the word is from the graph design's title)* | The nodes of an item's graph that have started, in order: what has happened to the item | Not the fog, and not one pass of several | The item. Has nodes | The grouping the code calls an attempt (`a1`, `a2`) |
| **Workflow** | A tracked, reviewed file of steps that an item's graph starts as | Not the graph, which changes as nodes are added | A file name. Has steps | The graph design says "template" |
| **Step** | One entry of a workflow; it becomes one node or names another workflow | Not a stage of a change | An id within its workflow | Item graphs and the code say "stage" |
| **Node** | One act on an item: an agent's invocation, a command, a landing, or a question put to the owner | Not a step, and not a run | A path id within its item. Has a kind, a claim, an ending and at most one run | The gate and the landing are not nodes yet |
| **Agent node, command node, person node** | A node executed by a harness, by a tracked command, or by a person | — | — | — |
| **Run** | The execution of one agent or command node, from its claim to its ending, across every interruption | Not the item's whole try, and not a unit of progress | The same id as its node. Has one or more segments, a log, a lease and a spend | [Section 2](#2-what-a-run-is). The observatory's help text defines it as an attempt |
| **Segment** | One process lifetime within a run | Not a run. A new segment is never a new node | An ordinal within its run | The code records `run.resume` events, not segments |
| **Recovery** | Carrying on the same node after an exception: a new segment of the same run | Not a resume, which is a new node | — | `tillr run exec --resume` and `run.resume` mean this |
| **Resume** | A node that continues an earlier node's session with one new message | Not recovery | Its own node id | The code's "returned turn" (`session_turn`): a new run id in the retained session |
| **Fork** | A node that starts from a copy of an earlier node's session | Not a resume: the original is untouched | Its own node id | — |
| **Session** | One conversation with a harness: the transcript a model continues from | Not the loop's process lifetime, and not the owner's own interactive session | The harness's session id. Produced by one node, used by any number of later ones | Four other senses; see [Section 6](#6-renames) |
| **Hoisted child** | A sub-agent of a running node, recorded as a node within it ([the graph design](./tillr-graph-is-the-path.md#9-subagent-hoisting)) | — | — | Item graphs: "child session", never a node |
| **Harness** | The agent program that holds a session: pi, Claude Code and the like | Not a backend | — | — |
| **Backend** | One row of the backend policy: a harness, a model route and its limits | Not a seat. A seat is the role; the backend fills it | A name in the backend policy | — |
| **Adapter** | The program that starts one harness for tillr and turns its output into one report | Not the harness | — | — |

### 5.3 Places and capacity

| Term | What it is | What it is not | Identified by; belongs to; has | Today's usage differs |
| :--- | :--- | :--- | :--- | :--- |
| **Item branch** | The commits an item's nodes have made, on top of the trunk commit last taken in | Not the worktree | A head pinned in the store. Belongs to one item | Today the branch is the lane's: `tillr/<scope>/<lane>` |
| **Worktree** | A [git worktree](https://git-scm.com/docs/git-worktree) checked out at an item branch, acquired by the item | Not a slot. It can be deleted and restored without loss | Its item's id | Part of a lane; deleted and recreated at each staging |
| **Scratch** *(coined here)* | The directory beside a worktree where nodes put build output, test binaries and temporary files | Not evidence, and not where a declared output lives | Its item's id | One directory per run under the lane, never deleted |
| **Collection** *(coined here)* | Deleting a worktree and its scratch while keeping the item branch and the records | Not the end of the item | — | Only refs are swept today |
| **Running-agent limit** | The number of agent runs in progress at once | Not a bound on worktrees or on disk | A setting, overall and per backend | `--lanes N` |
| **Lease** | The liveness record of a running run | Not a claim, and not a hold on anything. Its lapse is an exception | Its run | A lane's lease, with an expiry |
| **Lane** | **Retired.** | — | — | A pool slot with a worktree, a scratch directory and a lease |

### 5.4 Checking and landing

| Term | What it is | What it is not | Identified by; belongs to; has | Today's usage differs |
| :--- | :--- | :--- | :--- | :--- |
| **Candidate** | An item-branch head offered for landing, together with the trunk commit it was taken in on | Not a stored row and not a queue entry | A base commit and a head commit | — |
| **Gate** | The fast checks a candidate must pass before it lands, inside a tested ten-second budget ([gate rework](./gate-rework.md)) | Not the full suite, and not the repository's `just done` | — | Also used for the full suite ("the full, untiered gate") and for the loop step |
| **Check** | One declared test the gate or the full suite runs | Not a criterion | A file under the checks directory | — |
| **Gate verdict** | The gate's recorded answer on one candidate: pass, fail, or could not judge | Not a criterion verdict. "Verdict" is never used bare | Bound to a base commit and a head tree | — |
| **Criterion verdict** | A judge's or the owner's recorded answer on one criterion: done, not-done, blocked or defective | Not a gate verdict | The item and the criterion id | — |
| **Judge** | A read-only agent node that evaluates a candidate it did not write | Not the gate, and not the loop's examination of a finished run | Its node id | The loop action `judge` also means examination |
| **Lander** | The one program that moves the trunk | Not a queue | — | — |
| **Landing** | One act of the lander that put a commit on the trunk | Not the item's ending | A landing id. Belongs to one land attempt node | One row for every landed commit, design answers and triage included |
| **Landed** | An item whose deliverable is on the trunk and not yet proven: a **provisional landing** | Not finished | — | The final stored state |
| **Proof** | One run of the full suite on one trunk commit | Not the gate | The trunk commit | "Heavy tier", "rolling", "after-merge heavy run", "rolling audit" |
| **Proven** | A landing contained in a trunk commit whose proof passed, and not reverted: a **proven landing** | Not merely unreverted so far | The green pointer at or past the landing | No stored state |
| **Full suite** | Every check, as the proof runs them | Not the gate | — | "Heavy tier" |
| **Trunk; tip** | The repository's shared branch; its newest commit | — | — | — |
| **Green pointer** | The ref naming the newest proven trunk commit ([gate rework](./gate-rework.md)) | Not a branch | `refs/tillr/green` | — |
| **Red** | The trunk after a proof ended in failure | Not "unproven". An unproven trunk is not red | The failing trunk commit | The red cap also holds on "unproven" |
| **Revert** | A landing, with no model in it, that removes one proven-bad landing | Not an item, and not the mechanic | `revert-main-` and twelve hex digits. A land attempt node of the reverted item | Also an item state move out of `landed` |
| **Pin** | A git ref whose only job is to keep a commit reachable for a record | Not a model pin and not a session pin | `refs/tillr/…` | — |

### 5.5 Saying no, and waiting

| Term | What it is | What it is not | Identified by; belongs to; has | Today's usage differs |
| :--- | :--- | :--- | :--- | :--- |
| **Objection** | The ending by which an actor says it should not continue and names who acts next ([the graph design](./tillr-graph-is-the-path.md#33-endings)) | Not a failure, and not a refusal | Its node | "Blocked", and a stored "conflict" (`conflict-<run id>`) |
| **Obstacle kind** | The kind of a worker's objection, from a closed set: definition, fence, trunk, tooling, seat, owner ([`D-JL11`](./tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11)) | Not an open list | The `OBSTACLE:` line of a report | — |
| **Refusal** | A checker's "no" about what the candidate is, with evidence: from the gate, from tillr's checks at a node's end, or from the lander | Not an objection (the worker's "no"), not an exception (the machinery failed), not a rejection | The ending of the checking node | Also used for a lane that staging refuses and for a store command that is rejected |
| **Rejection** | The store declining a command | Not about a candidate | — | `item.rejection`; READMEs also say "refusal" |
| **Conflict** | Two changes to the same lines, found at a take-in or at a landing, with the paths | Not an objection | The paths | Also the stored record of an objection |
| **Return** | Sending a refusal, a conflict or a revert back to the session that wrote the change, as a resume node carrying the evidence | Not a retry: nothing is counted | The resume node | Reason `returned-with-evidence` |
| **Reverted** | The state of a landing that a proof proved bad and a revert removed | Not red | — | Reason `landing-reverted` |
| **Wait** | A derived statement that nothing is added after some ending yet, with the reason and the mover (the graph design) | Not stored, and not cleared by anyone | — | A stored "hold" with a reason, the item state `blocked`, the position `held` |
| **Mover** | The actor or fact a wait names ([landing principles](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)) | Never "someone" | — | — |
| **Draining; needs you** | The owner's two categories of unfinished items: every item whose wait names the owner by a question is "needs you"; every other is "draining" | Not states of the store | — | The view shows more categories |
| **Owner question** | A numbered question in a design document that only the owner rules on; in the graph, a person node from asked to answered | Not a ledger row, though both carry an `OQ-` id | Its `OQ-` id | — |
| **Supersession** | The finding that an item's change is already on the trunk | Not a refusal of the work | Lander reason `already-on-trunk` | Also a retry replacing a run, and a document replacing a document |

### 5.6 The machinery

| Term | What it is | What it is not | Today's usage differs |
| :--- | :--- | :--- | :--- |
| **The drain** | The program that moves items along their paths; "draining" is the queue emptying | Not one process | — |
| **Loop** | One long-running process of the drain | Not a session | Its lifetime is called a "session" (`drain_sessions`) |
| **Pass** | One review of the queue by the loop | Not a tick | "Tick" survives in a table and a column name |
| **Generation** | The ordinal of a pass ([drain successor](./tillr-drain-successor.md)) | — | — |
| **Dispatch** | Starting a node's run | Not a decision to start one | — |
| **Backend policy; gate policy** | The file mapping seats to backends; the file of landing settings | "Policy" is never used bare; neither is the graph design's policy function | — |
| **Gate plan; queue plan** | The list of checks a gate run will run; the graph design's queue-level node | "Plan" is never used bare | Also the loop's pass plan and `tillr drain plan` |
| **Operator retry** | The owner's or an operator's command that adds a node after an ending the rule table adds nothing for | Not a count and not a budget | A stored authorization that starts a new attempt |
| **Pause** | An exception requested on purpose: the run stops between tool calls and recovers later | Not an ending | "An attempt outcome" |

### 5.7 Words for "a landing is a suspended tool call"

A separate design works out that mechanism. These are the words it needs; nothing here decides how it works.

| Term | What it is | What it is not |
| :--- | :--- | :--- |
| **The call** *(coined here)* | The tool call by which a worker's session says its candidate is ready and asks for it to be landed. It is the written form of the work node's `submitted` ending | Not a command the worker runs itself, and not the landing |
| **Suspension** | The state of a session whose call has no answer yet. No process runs and no node is running; the session is an asset with an open call | Not a pause, which is an exception, and not a wait, which belongs to the policy function |
| **The answer** | The result delivered to the call. It arrives as the first message of a resume node | Not an event the session must poll for |
| **A refusal** (as an answer) | An answer that carries a "no" from [Section 4](#4-the-stages-of-a-change): a gate refusal, a conflict, a lander refusal or a revert. It is the only answer that makes the session work again | Not an objection, which is the worker's own ending |
| **Provisional landing; proven landing** | The landed and proven of [Section 4](#4-the-stages-of-a-change), as answers | — |

Which of the two landings ends the suspension is that design's question, not this one's.

### 5.8 Retired words

| Word | What to say now |
| :--- | :--- |
| **Attempt** | Nothing. Returned work is new nodes on the same path |
| **Lane; express lane; lane cap** | Worktree, scratch, running-agent limit; "work the owner is waiting on" |
| **Desk** | The comment item's worktree and the document's session. [Designer sessions](./tillr-designer-sessions.md) owns the word and decides whether it stays |
| **Node run** | Run |
| **Child session** | Hoisted child |
| **Hold; held; blocked** | Wait, with its mover |
| **Tick** | Pass |
| **Fixer; repair item** | Mechanic; generated item |
| **Template** (of a graph) | Workflow |
| **Stage** (of a workflow) | Step |
| **Continuation** | Recovery, or a resume, whichever it was |
| **Heavy tier; rolling; landing tier** | Full suite and proof; the gate |

---

## 6. Renames

"Alias" means the old spelling stays in code or in stored data and is documented as meaning the new word. No rename is planned here.

| Today | Settled word | Where it appears | Rename, or alias? |
| :--- | :--- | :--- | :--- |
| "run = one recorded attempt by an agent" | Run = one node's execution; the whole is the **path** | The observatory's [`glossary.json`](../../tools/tillr-view/web/src/models/glossary.json); view labels | **Rename the text.** It is what the owner reads |
| `runs` table, `run_id`, `run.*` events | Run | Store, events | Alias. The meaning already matches |
| `node_runs`, "node run" | Run | Store; [item graphs](./tillr-item-graphs.md#the-words) | Alias in the store; reword the document |
| `a<N>` in `run-<item>-a<N>-n<k>`, `node_runs.attempt`, `Decision.Attempt`, `AttemptClass` | None: attempt is retired | Run and transition ids, code | Alias for stored ids. New ids stop carrying it when the graph design's storage is built |
| `exec --resume`, `run.resume` | **Recovery** | [tillr-run README](../../tools/tillr-run/README.md), events | Alias, documented. Worth renaming only with the resume node kind, when two things would otherwise share the word |
| `session_turn`, "returned turn", `returned-with-evidence` | **Resume** node; **return** | Run request, planner reason | Alias until the graph design's resume kind is built |
| "session" = the loop's process lifetime; `drain_sessions` | A **loop**'s lifetime | The observatory's glossary, store | **Rename the text**; alias the table |
| `lane-<n>`, `express-<n>`, `desk-<doc>`, `lanes` table, `.tillr/lanes/`, `--lanes`, `lanes leased: M/K`, `drain.lane-*` | Worktree, scratch, running-agent limit | Flags, store, paths, events, view | **Rename the flag and the view label** when the worktree lifecycle is built; alias stored rows and old events |
| `<lane>/durable/<run_id>` | **Scratch** | Paths, the worker's brief | **Rename with the lifecycle.** It is the directory that fills the disk |
| Item state `landed` as final | **Landed** (provisional), then **proven** | The landing rulebook, view | **Worth changing:** a derived "proven" shown in the view first; a stored state is the rulebook's decision |
| `fixer-main-…`, "trunk fixer" | **Mechanic** | Ids, code, events `drain.fixer-*` | Alias the id; say mechanic in the view and in documents |
| `item.Conflict`, `conflict-<run id>`, reasons `conflict-design`, `conflict-owner` | **Objection** | Store, ids, hold reasons | Alias. They disappear when waits replace holds |
| `squash-conflict`, `conflict-fix`, `-cfix` | **Conflict** | Lander result, run-id suffix | Keep. This is the surviving meaning |
| Hold reasons, `ActionHold`, position `held`, state `blocked` | **Wait** | Planner, view, store | Alias until the rule table is built; the view says "waiting on" now |
| `rewrite-<id>` item | A rewrite **node** | Sync, store | Alias until the rewrite kind is built |
| "definition" = the use-case layer; `store.definition`, the `definition` package | **Rulebook** | [The direction document](./tillr-core-and-git-landing.md), code | Pending [OQ-VO4](#OQ-VO4); alias in code either way |
| "definition" of a node | The node's kind, inputs and brief | The graph design | Reword when that document is next edited |
| `WorkflowStage`, "stage" | **Step** | Code, item graphs | Alias in code; reword the document |
| "template" | **Workflow** | The graph design | Reword |
| `rolling_*` tables, `--tier rolling`, "heavy tier" | **Proof**; **full suite** | Store, flags, [gate rework](./gate-rework.md) | Alias |
| `drain_ticks`, `tick_id` | **Pass**; **generation** | Store | Alias |
| "Work is declared as items in tracked Markdown" | [Section 1](#1-what-an-item-is) | [design-status](../tillr/design-status.md) | **Rewrite the paragraph** to link here |

---

## 7. Amendments to the graph design

[The graph design](./tillr-graph-is-the-path.md) is not edited by this document. Its author applies these once the owner has ruled [OQ-VO1](#OQ-VO1). Each block quotes the current text exactly, then gives the replacement. They supersede that design's reading of `D-IG8` as "a lane belongs to a running node".

**The word table, row "Item branch".**

```text
| **Item branch** | The commits the item's nodes have made, on top of the trunk commit last taken in. The store pins its head, so no lane owns it. The owner's word is "the branch". | Not a lane's worktree. A worktree is where one node runs. |
```

```text
| **Item branch** | The commits the item's nodes have made, on top of the trunk commit last taken in. The store pins its head, so the branch survives the collection of the item's worktree. The owner's word is "the branch". | Not the item's worktree. The worktree is a checkout of the branch that the item acquires and that can be deleted and restored. |
```

**Node kinds, row "owner question".**

```text
| **owner question** | person | Carries one question that names the owner, from asked to answered. It holds no lane and no process | `answered` |
```

```text
| **owner question** | person | Carries one question that names the owner, from asked to answered. It needs no worktree and no process | `answered` |
```

**Scheduling, "What the scheduler chooses".**

```text
**What the scheduler chooses, and fixes at the claim:** the lane where the node needs a worktree; the backend, from the seat's list; the model and effort, from that backend's row; whether a resumed session is compacted first.
```

```text
**What the scheduler chooses, and fixes at the claim:** the backend, from the seat's list; the model and effort, from that backend's row; whether a resumed session is compacted first. It does not choose a worktree: a node that needs one runs in its item's, which the claim creates or restores.
```

**Scheduling, "Lanes".**

```text
**Lanes.** A lane is a worktree and belongs to one running node. When the node ends the lane is free, because the item branch is pinned in the store and any lane can restore it. Two writing nodes of one item never run at once, since each starts from the head the last one left; read-only nodes of one item may run together. An owner question takes no lane.
```

```text
**Worktrees.** A worktree belongs to an item, which acquires it at the claim of its first node that needs one. When a node ends the worktree is idle, not free: it stays the item's, and it may be collected and later restored, because the item branch is pinned in the store. Two writing nodes of one item never run at once, since each starts from the head the last one left; read-only nodes of one item may run together in it. An owner question needs no worktree. The lifecycle, the bound and the running-agent limit are in the vocabulary document, Section 3.
```

**Scheduling, "Order", first sentence.**

```text
Work the owner is waiting on first, on lanes outside the cap, as `D-IG10` rules.
```

```text
Work the owner is waiting on first, outside the running-agent limit, as `D-IG10` rules.
```

**The item graph ledger, row `D-IG8`.**

```text
| `D-IG8` | Superseded | A lane belongs to a running node, not to an item. The gate and the landing are nodes and run as often as the rule table adds them. What survives: a graph runs correctly on one lane |
```

```text
| `D-IG8` | Amended | The item keeps one worktree across its nodes, as a resource it acquires and that is collected and restored, not as a slot in a pool. The gate and the landing are nodes and run as often as the rule table adds them, so "once at the exit" is withdrawn. What survives: a graph runs correctly with one agent running at a time |
```

---

## 8. What this does not cover

- **How any rename is carried out.** [Section 6](#6-renames) judges; it does not plan.
- **The storage of nodes, runs and segments.** That is the graph design's storage section and its open question on the store.
- **The mechanism of the suspended landing call,** and **how a design answer reaches the trunk early** ([early design landing](./tillr-early-design-landing.md)). Only their words are fixed here.
- **A bound on run logs and transcripts.** They are records; [Section 3.3](#33-the-life-of-a-worktree) only requires that they are kept and reported.
- **Fork checkouts under `.forks/`.** They are not item worktrees and keep their own rules.

**Not verified on 2026-11-18,** and stated as such where used: whether each harness finds a session by its working directory; which run id a workflow's gate record attaches to; the route by which a design answer's commit is recorded as a landing of its item; whether anything outside tillr prunes the per-run scratch.

---

## 9. Drafted items

These are proposals. Each is written as an item block with everything except the item directive, so that sync reads none of them and nothing can be dispatched.

#### Delete an ended item's scratch and worktree

Per-run scratch is never deleted: on 2026-11-18 twenty lanes held 133 GB, almost all of it test binaries of runs that had ended. This item makes the scratch and worktree of an ended item go when the item ends, and reports what is on disk, which is the part of the worktree lifecycle that needs no ruling.

- **Paths:** `tools/tillr-drain/`, `tools/tillr-run/`, `tools/tillr-view/`, `docs/`
- **Waits on:** nothing.
- **c1:** When an item ends, its per-run scratch directories and its worktree are deleted, and its branch head, records and run logs remain. A test ends a fixture item and asserts both halves.
- **c2:** Scratch of a run that is still running, or of an item that has not ended, is never deleted. A test asserts it for a running fixture run.
- **c3:** A command lists scratch whose item has ended or is unknown, with sizes, and deletes it when asked. A test runs it against a fixture tree holding one live and one ended item.
- **c4:** The status view shows the number of worktrees on disk and the bytes of worktrees and scratch.

#### Say run, session and path one way in the observatory

The observatory's help text defines a run as an item's attempt and a session as the loop's lifetime, which matches neither the store nor the documents. This item makes the words the owner reads match the vocabulary.

- **Paths:** `tools/tillr-view/`, `docs/`
- **Waits on:** nothing.
- **c1:** The help text defines run, session, lane and hold as the vocabulary document does, and adds path and node. A test asserts each entry's text.
- **c2:** An item's page shows its nodes in order as its path, and no label counts runs as progress.
- **c3:** The "Read this first" paragraph of the design status page defines an item without requiring a document block and links the vocabulary document.

#### Split the lane cap into a running-agent limit and a worktree bound

One flag sets both how many agents run and how many worktrees exist. This item separates them, so that idle worktrees are collected against a bound while the number of running agents is set by itself.

- **Paths:** `tools/tillr-drain/`, `tools/tillr-run/`, `tools/tillr/settings/`, `tools/tillr-view/`, `docs/`
- **Waits on:** Delete an ended item's scratch and worktree; the owner's rulings on the lane word and on the bound.
- **c1:** A worktree is keyed by its item and is restored at the same path after collection. A test collects and restores a fixture item's worktree and asserts the head and the path.
- **c2:** Past the bound, idle worktrees are collected in the order the vocabulary document states, and no in-use worktree is. A test asserts the order.
- **c3:** With every worktree in use at the bound, a node that needs a new one is not started and is not failed, and `drain why` names a running node as its mover. A test asserts both.
- **c4:** The running-agent limit is a setting of its own, and work the owner is waiting on runs outside it. A test asserts each.

---

## Decision Ledger

The `D-VO` rows are the designer's decisions of 2026-11-18, each open to the owner's overturn. None is built.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-VO1` | An item is tracked work with its own deliverable and ending; a block is the written form of its definition and is not required for it to exist. **Rejected:** "an item is a block in a document", which the store already contradicts. | 2026-11-18 | [Section 1](#1-what-an-item-is) | — |
| `D-VO2` | What acts on another item's deliverable is a node, not an item: rewrites, reverts, triage and design answers. **Rejected:** an item per act, which gives one change several ids and several endings. | 2026-11-18 | [Section 1](#1-what-an-item-is) | — |
| `D-VO3` | A run is one node's execution and is never a unit of progress; the whole is the path; a process lifetime is a segment. **Rejected:** "run" for the item's whole try, which leaves the store's ten rows without a name. | 2026-11-18 | [Section 2](#2-what-a-run-is) | — |
| `D-VO4` | "Resume" is a node; carrying on the same node after an exception is "recovery". **Rejected:** one word for both, which is today's usage. | 2026-11-18 | [Section 5.2](#52-the-graph-and-what-executes) | — |
| `D-VO5` | A worktree and its scratch belong to an item, are collected at stated moments and restored on demand; the bound never fails work. **Rejected:** a fixed pool that any node may take, which is the shuffling the owner ruled out; and a lifecycle for worktrees alone, which leaves 99 percent of the disk. | 2026-11-18 | [Section 3](#3-what-a-lane-was-and-the-worktree-as-a-resource) | — |
| `D-VO6` | How many agents run at once is a setting of its own. **Rejected:** deriving it from the worktree bound. | 2026-11-18 | [Section 3.5](#35-what-limits-how-many-agents-run-at-once) | — |
| `D-VO7` | Landed is provisional and proven is final; the item ends at proven. **Rejected:** ending the item at landed, which lets a reverted change count as delivered. | 2026-11-18 | [Section 4](#4-the-stages-of-a-change) | — |
| `D-VO8` | "Stage" names the eight stages of a change, and a workflow's unit is a "step". "Conflict" means a merge conflict, and the worker's "no" is an objection. "Verdict", "policy" and "plan" are never used bare. | 2026-11-18 | [Section 5](#5-the-words) | — |

---

## Open Questions

1. 💬 **OQ-VO1: Does the word "lane" go away entirely?**

   You start the drain with `--lanes 20` and the status line reads `lanes leased: 7/20`. Today that one number is four things: how many agents run, how many worktrees exist, where each run's scratch goes, and which slot a run holds. This document gives each its own word, and nothing is left for "lane" to mean.

   - **A — Retire it.** The status reads `agents running: 7 of 20` and `worktrees: 12, 3.1 GB`. You see `lane-3` only in old records and old paths.
   - **B — Keep it for a running agent's use of its item's worktree.** The status still says lanes. The word means something new, and every old record uses it the old way.

   <!-- vantage: question id=OQ-VO1 leaning="Retire it: a word that is kept with a new meaning has to be explained every time an old record is read." -->

   _Leaning:_ Retire it: a word that is kept with a new meaning has to be explained every time an old record is read.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-VO2: Is the limit on worktrees a disk budget, a count, or both?**

   An item finishes its build and waits for a judge. Its worktree sits idle at 62 MB, with up to 2 GB of test binaries beside it. On 2026-11-18 twenty lanes held 133 GB this way and nothing deleted any of it. Idle worktrees and their scratch will now be deleted and restored on demand, so the choice is which number makes deletion start.

   - **A — Disk only.** The status reads `worktrees: 3.1 GB of 25 GB`. Past the budget, idle ones are deleted, least recently used first. Their number is unlimited.
   - **B — Count only.** The status reads `worktrees: 12 of 40`. A few items with large scratch can still fill the disk.
   - **C — Both.** Deletion starts at whichever is reached first, and you read two numbers.

   <!-- vantage: question id=OQ-VO2 leaning="Both, starting at 25 GB and 40 worktrees. Disk is the one that was measured hurting; the count is a backstop for the performance cost you named." -->

   _Leaning:_ Both, starting at 25 GB and 40 worktrees. Disk is the one that was measured hurting; the count is a backstop for the performance cost you named.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-VO3: Is work that tillr files for itself an "item"?**

   A proof goes red and tillr files work to make the trunk green. A worker reports a broken tool and tillr files work to fix it. Both show in `tillr view` beside the items you wrote, with ids starting `fixer-main-` and `defect-`, and neither has a document block. What you call them decides whether they count in "draining" and sit in the same list as yours.

   - **A — They are items.** Each lands a commit of its own, so it is listed with yours and carries a "generated" mark.
   - **B — They are something else, say "repairs".** They are listed apart, and your item counts show only work that somebody asked for.

   <!-- vantage: question id=OQ-VO3 leaning="They are items: each has its own commit and its own ending, and a second list is a second place to look for why the queue is not moving." -->

   _Leaning:_ They are items: each has its own commit and its own ending, and a second list is a second place to look for why the queue is not moving.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 🤷 **OQ-VO4: What is the layer that makes tillr a Git landing tool called?**

   tillr's general machinery becomes "land changes in a Git repository" through one data file: item states, allowed moves, prerequisites and the completion rule. The direction document called that layer "a definition" as a placeholder and said you had not chosen its name. This document uses "definition" for what an item is to deliver, so one of the two needs another word.

   - **A — "Rulebook".** You read "the landing rulebook", and "definition" always means an item's.
   - **B — Keep "definition" for the layer.** An item's fence and criteria are then called its "spec", and the graph design's "definition edits" become "spec edits".

   <!-- vantage: question id=OQ-VO4 leaning="Rulebook, by preference: the layer is named in one document and one package, while an item's definition is named everywhere." -->

   _Leaning:_ Rulebook, by preference: the layer is named in one document and one package, while an item's definition is named everywhere.

   **Answer:**
   > _(empty — fill in when decided)_
