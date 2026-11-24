---
title: "tillr is a core of queues and graphs, and landing changes in Git is one charter on top of it"
status: in-review
stage: DESIGN
updated: 2026-11-24
tags:
  - tillr
  - design
  - boundaries
summary: "tillr is general machinery of queues and graphs whose nodes are agents and commands, with its tracking, plus one small, separate charter ('definition' in code) per use. Landing changes in a Git repository is the first charter. This document states the workflow that charter wraps, where the Git-landing assumption still lives in the core's programs, the shape a charter takes (a data file plus one hook program), and the extension points a second use fills."
next: "Move the landing behavior that is still Go inside the drain (intake, staging, the candidate rule, the boundary check, trunk observation, conflicts) behind the charter's hook program. The owner reviews D-CG1 to D-CG5."
depends-on:
  - ../reference/tillr-item-graphs.md
---

# tillr is a core of queues and graphs, and landing in Git is one charter on top of it

> **Delivered copy.** This is `docs/design/tillr-core-and-git-landing.md` from this repository at commit `0275c51d52` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-core-and-git-landing.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** The direction is the owner's ([Section 1](#1-the-direction-in-the-owners-words)). The
charter file, the store that records it and a second charter that lands nothing are built
([Section 8.2](#82-what-is-built-and-what-is-not)). Five decisions the build rests on are the
designer's and are the owner's to overturn ([the ledger](#decision-ledger)).

> **In short.** tillr is a core of queues and graphs plus one charter per use, and a charter is a
> data file and one hook program. Landing changes in Git is the first charter (`git-landing`); a
> second one that lands nothing runs end to end. Most of the landing workflow's behavior is still
> Go inside the drain, and moving it behind the charter is the work that remains.

**Why it matters.** A use that wants tillr's queues, runs and tracking without its landing writes
a second charter instead of asking the core for exceptions.

**Cost.** Every rule about items that suits Git landing and chafes against another use leaves the
core: lifecycle, prerequisites, retention and the completion rule are each charter's to state.

**Start at [Section 5](#5-the-extension-points)** for what a charter supplies, and
[Section 3](#3-where-the-git-landing-assumption-is-wired-in-today) for what is still wired into
the core's programs.

**Needs your ruling:** None.

**Reads with:** [the build plan](tillr-definitions-plan.md) (the units of work),
[the definition package's README](../../tillr/definition/README.md) (the file's keys and the
hook contract), [tillr-item-graphs.md](../reference/tillr-item-graphs.md) (graphs inside one
item), [the graph design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-graph-is-the-path.md) (landing as nodes of an item's graph),
[the vocabulary](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md) (one meaning per word),
[tillr-separable-parts.md](tillr-separable-parts.md) and
[tillr-product-qa-requests.md](tillr-product-qa-requests.md) (what a non-landing use asked of
the core).

---

## The words this document uses

- **The core:** the general machinery. Queues of items, graphs of nodes inside an item where each
  node is an agent or a command, and the tracking behind them: the store, items, criteria, runs,
  leases, events and views. Not anything that knows what Git is.
- **A charter** *(the owner's word, ruled in [the vocabulary](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md); the code and
  the rest of this document say "a definition")*: the layer that turns the core into one use. It
  states that use's lifecycle, prerequisites, retention, completion rule and vocabulary as a data
  file, with one hook program for its judgments. Not an item's definition, which is its fence and
  criteria.
- **The `git-landing` charter** *("the landing definition" below)*: the charter that lands changes
  in a Git repository, from item blocks in documents through a gate. Its data file is
  [landing.json](../../tillr/definition/landing.json) and its hook program is
  [tillr-loop](https://github.com/mschulkind-oss/tillr/blob/main/tools/tillr-loop/tillr-loop).
- **A step program:** a program the drain runs for one step and reads by its exit code
  ([steps.go](../../tillr-drain/drain/steps.go)).

Store, item, drain, gate, lander, fence, criteria, candidate, hold and lane are defined in
[the vocabulary](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md).

---

## 1. The direction, in the owner's words

Owner ruling, 2026-11-16, restated:

1. tillr is two things: the core, and the landing definition.
2. The landing definition is one file, as small and as separate as possible, and it injects what
   it needs into every part of tillr from that one place.
3. Reading that file shows the whole landing workflow and every extension point.
4. An item's lifecycle (its states and moves), its prerequisites (what it must have before
   dispatch) and retention (how long finished things are kept) belong to the definition, not the
   core.
5. No item waits without a named next step and an actor who takes it.
6. **The decision rule.** When a choice suits Git landing and chafes against another use, or the
   reverse, that tension is the signal that the choice does not belong in the core: each
   definition supplies it. [Section 6](#6-what-this-changes-in-what-was-said-before) applies it.

### 1.1 The owner's words, as reported by Product QA

Four further positions of the owner reached this design through the requests of a second use, a
pipeline that records findings and lands nothing
([tillr-product-qa-requests.md](tillr-product-qa-requests.md)), and stand as follows:

| Position | Standing |
| :--- | :--- |
| A started node is fixed; anything not started may be changed, and the core must not preclude a definition's policy | The owner's direct ruling, in [the graph design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-graph-is-the-path.md) |
| Whether a finished item may be reopened is a definition's policy | Reported only. Built as each definition's list of valid moves, on the designer's `D-CG4` |
| No fixed graph size limits | The owner's direct ruling; an expanded graph reports its size and is refused for none (`D-IG4` in [tillr-item-graphs.md](../reference/tillr-item-graphs.md#why-its-this-way)) |
| No clock that fails work: budgets are targets and stalls are watched | The owner's direct ruling |

---

## 2. The workflow the landing definition wraps, as it runs today

One line per step, with the program that owns it.

1. **Intake.** An item block (an HTML comment under a heading of a tracked document) is read
   from the tracked files under `docs`, and `tillr-item sync` writes it to the store
   ([sync.go](../../tillr-item/item/sync.go)). The drain syncs at each new trunk commit.
2. **Plan.** The drain's planner picks ready items in rank order and names a hold for the rest
   ([plan.go](../../tillr-drain/drain/plan.go)).
3. **Lane.** A lane is a leased directory holding a git worktree based on the trunk; staging writes
   the worktree, the brief, the fence and the base commit
   ([stage.go](../../tillr-drain/drain/stage.go)).
4. **Run.** `tillr run exec` runs a worker session through the backend adapter the backend policy
   picks by seat ([exec.go](../../tillr-run/run/exec.go),
   [backend-policy.json](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/backend-policy.json)).
5. **Candidate.** A run whose lane head moved off its base is a candidate; one that did not is
   refused as `candidate-no-op`.
6. **Boundary check.** Refs and worktrees outside the lane's own are snapshotted around the run
   ([boundary.go](../../tillr-drain/drain/boundary.go)).
7. **Block retirement.** The loop commits the removal of the item's own block before the gate
   ([retire.go](../../tillr-drain/drain/retire.go)).
8. **Gate.** `tillr-loop gate` runs [change-gate.py](https://github.com/mschulkind-oss/tillr/blob/main/tools/gate/change-gate.py) within the
   budget and strategy of [gate-policy.json](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/gate-policy.json). A red check is
   replayed on the base before the candidate is blamed.
9. **Land.** `tillr-loop land` runs `tillr-land`, which takes the land lock, squashes `base..head`
   onto the trunk, writes the marker ref `refs/tillr/landed/<run_id>` before moving the trunk, and
   writes a `landings` row binding the gate verdict to the base commit and the head tree. A retry
   finds the marker and does not land twice
   ([landing.go](../../tillr-land/land/landing.go)).
10. **Verdict.** Only after the landing, `tillr-loop verdict` records each criterion `done`.
11. **End state.** The item moves to `landed`; other ends are `resolved-elsewhere` with a pointer,
    `closed` with a reason, and `blocked` with a waiter
    ([state.go](../../tillr-item/item/state.go)).
12. **Rolling tier and trunk fixer.** After landings a rolling check runs over the trunk range; a
    red trunk files a fixer item and holds the rest as `trunk-broken`
    ([trunk.go](../../tillr-drain/drain/trunk.go)).
13. **Conflicts.** A squash conflict returns to the worker that produced the candidate
    ([conflict.go](../../tillr-drain/drain/conflict.go)).
14. **Trunk observation.** The drain records each observed trunk tip, holds on a rewritten trunk,
    and settles an uncertain landing by reading the trunk
    ([trunk_history.go](../../tillr-drain/drain/trunk_history.go)).
15. **The owner's checkout.** The trunk moves in the checked-out worktree by fast-forward only. A
    path holding uncommitted content stops the move before it starts and holds the item as
    `trunk-path-dirty` ([dirty_hold.go](../../tillr-drain/drain/dirty_hold.go)).
16. **After the landing.** Optional roll and push actions are read from the settings file
    ([settings.go](../../tillr/settings/settings.go)); the keeper
    ([tillr-keep](../../tillr-roll/tillr-keep)) restarts the loop.

---

## 3. Where the Git-landing assumption is wired in today

The coupling is dense in the drain and the lander, moderate in the gate and the views, and a few
lines in the store, the item face and the run face.

| Component | General part | Landing-specific part |
| :--- | :--- | :--- |
| Store (`tools/tillr`) | store, composed schema, records, events, exit codes | roll and push settings |
| `tillr-store` | init, check, recover, checkpoint, events | none |
| `tillr-item` | items, criteria, edges, decisions, mail, verdicts, close and reopen | the `allowed_paths` column; sync reading tracked documents |
| `tillr-run` | lane leases, `run exec`, adapters, end causes, usage | the adapters' directory is found from the git common directory |
| `tillr-drain` | planner, general holds, dispatch, backend policy, quotas, passes, workflows, designer sessions, open-question holds | the candidate of worktree, base and head; staging a worktree; the boundary check; block retirement; the rolling tier and fixer; trunk history; conflicts; the dirty-checkout hold; the holds only landing raises |
| `tillr-gate` | a check runner: registry, plan, run, tiers, budgets, result memory | change checks read a diff of `base..head`; memory is keyed by tree |
| `tools/gate` | none | this repository's own gate: already definition content |
| `tillr-land` | the land lock | all of it: already a separate program |
| `tillr-loop` | the adapter pattern | gate, land and verdict, translated onto the loop's exit contract |
| `tillr-view` | status, events, passes, lanes, running, waiting, trace, serve | the landings and trunk pages, gate spans, waiting rules for landing holds |
| Item block and sync | `id`, `blocked_by`, `seat`, `workflow` | documents are git-tracked files under `docs/`; a landed item's block is retired |
| Policy files | seats to backends | landing strategy, red cap, lane base |
| Workflows (`config/tillr/workflows/`) | stages, seats, access, edges | none in the file |

**Lifecycle, prerequisites and retention, by the owner's rule:**

- **Lifecycle is the definition's.** The states, the ready state, the finished states and the
  valid moves come from the definition file. The core keeps only that an item has a state from a
  closed set and that each move is an event.
- **Prerequisites are the definition's.** For Git landing: a design document with no open
  question, a clean owner path, and the fork and trunk holds. General queue mechanics stay in the
  core: no free lane, a live run, a closed backend window, and `blocked` by an unfinished
  dependency. The dependency edge is the core's; what counts as finished is the definition's.
- **Retention is the definition's.** The file has a `retention` key, and `keep` is the only value
  it accepts. The event log is append-only, and each run's record directory is kept.

`tillr-loop` supplies three of the workflow's steps (gate, land, verdict). The rest are Go in the
drain, the item face and the view.

---

## 4. What "one file" could mean

A definition is **a data file plus one hook program** (`D-CG1`). Three alternatives were
considered:

| Shape | Verdict |
| :--- | :--- |
| **A data file alone**, read by a core that knows nothing of Git | Adopted as the data half. Alone it separates words and no behavior: a rule such as "a rewritten trunk holds everything" stays in the core. |
| **One program that answers the core's hooks** | Adopted as the hook half. Alone it hides the vocabulary inside a program, where no view or planner can read it without running it. |
| **A workflow graph**, where landing is one workflow among others | Not a rival. The gate and the landing become nodes of an item's graph ([the graph design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-graph-is-the-path.md)); the definition is how the core sees a use, and the graph is how one item's path runs inside it. Intake, staging and trunk observation are outside any graph. |

---

## 5. The extension points

The places a second use plugs in something other than Git landing.

| Extension point | Git landing fills it with | Status |
| :--- | :--- | :--- |
| Intake | item blocks in tracked documents, through sync | Built; `tillr item apply` also exists, and a store not fed by documents is not held for lacking blocks |
| What a node is | an agent | Agent and command nodes are built |
| Where a node runs | a git worktree in a lane | Built; a plain directory, for a definition that does not land, is built too |
| What completes a node | the run ends and passes | Built, with declared outputs |
| What completes an item | a landing | Built; finishing with no commit and finishing on a hook's recorded verdict are built |
| Lifecycle | six states, final `landed` and `resolved-elsewhere`, reopen from `closed` | Built as data in the definition file |
| Prerequisites | a design document with no open question, finished dependencies, a clean owner path | Named in the definition file; a next step for each is not built |
| Retention | nothing is pruned | The `retention` key accepts only `keep` |
| Outcome vocabulary | the finished states, close reasons and pointers | Finished states per definition are built; counting them in the views is not |
| Dispatch policy | lane count, seats, backend policy | Built, with seat shares, express lanes and a spend ceiling |
| The judge | the gate program and the verdict program | Built, with a judge before the gate and an evaluator node |
| Trunk observation | the rolling program, trunk history | Built, as step programs and Go |
| After completion | roll and push settings | Built as settings; an `after-completion` hook is named in the contract and not built |
| The views' vocabulary | landings and trunk pages, landing holds | Hard-coded |
| An owner acknowledgment point | none | Not built |

---

## 6. What this changes in what was said before

[The separable-parts proposal](tillr-separable-parts.md) lists requests the core declines. The
owner's decision rule reads each as a possible tension between Git landing and another use. The
Judgment column is the designer's (`D-CG5`).

| Request | A tension? | Judgment |
| :--- | :--- | :--- |
| C1 Outcomes per kind | Yes | The definition's. Core protection: each definition's set is closed and projected one way. |
| C2 Reopening a finished item | Yes | The definition's. Final inside Git landing. Core protection: a move out of a finished state is an event, never silent. |
| C3 Steps reshaping the graph | Partly | Which reshaping operations exist is the definition's. That each is applied by one writer, atomically with its event, and never to a started node, is a protection every use needs. |
| C4 An uncertain outside effect as a state | Yes | The worktree rule is Git landing's. Core protection: an effect a definition declares is recorded before and after, so a retry can see it. |
| C5 Clocks and deadlines in the plan | Partly | A deadline as an ordering input is the definition's. The planner staying a replayable function of the store is a protection. |
| C6 A library as the connection | No | A protection: processes and records are how a definition injects anything without linking the core. |
| C7 Completion that is not a landing | Yes | The completion rule is the definition's. Core protection: an item completes once, on a recorded receipt. |
| C8 A second loop in one root | No | One writer per store and at-most-once launch are protections; a second use runs its own store and root. |
| The undeclared-item hold | Yes | A prerequisite, so the definition's, and applied only to stores that documents feed. |

---

## 8. Building it: the owner's go-ahead and the working choices

The owner's go-ahead of 2026-11-17 was to build the separation at once, beside the running drain.
The choices the build rests on:

| # | Choice | Whose | Ledger |
| :--- | :--- | :--- | :--- |
| 1 | The one file is a data file plus one hook program | The designer's | `D-CG1` |
| 2 | The core runs a generic lifecycle; each definition names its states, ready state, finished states and valid moves. Leaving a finished state is always an event with a reason and an actor | The owner's | [OQ-CG2](#OQ-CG2) |
| 3 | The kind of layer is a charter and the first instance is `git-landing`; the code keeps "definition" | The owner's | [OQ-CG5](#OQ-CG5) |
| 4 | The store records the definition it runs, at `tillr store init --definition`; a store with none recorded runs `git-landing` | The designer's | `D-CG2` |
| 5 | Separation runs beside the drain under a safety rule: the Git-landing queue behaves identically, the existing suites pass unchanged, and an older store opens and drains | The owner's go-ahead; the rule is the designer's | [OQ-CG3](#decision-ledger), `D-CG3` |
| 6 | Reopening and graph change are a definition's policy; no fixed graph caps; no clock whose only consequence is a kill | The owner's, except reopening ([Section 1.1](#11-the-owners-words-as-reported-by-product-qa)) | `D-CG4` |

### 8.1 What a store that does not land needs from the core

A store declared not to land avoids four things:

- **Holds only landing raises:** among them `candidate-no-op`, `gate-failed`, `land-refused`,
  `trunk-broken`, `trunk-path-dirty`, `trunk-rewritten` and the conflict holds.
- **Git at the loop's start:** the trunk read, the startup sync, trunk history, the rolling tier,
  and roll and push after a landing.
- **Staging:** a worktree, a base commit and a fence file.
- **The end of a run:** the candidate rule, the boundary check, the gate, the lander and the move
  to `landed`.

What stays in the core for every definition: the store and its events, items, criteria, verdicts,
edges, lanes and leases, `run exec`, the planner's general holds, backend policy and quotas,
workflows and their nodes, and the views.

### 8.2 What is built, and what is not

**Built:**

- **The definition file and the store that records it.**
  [landing.json](../../tillr/definition/landing.json) names the states, the ready state, the
  finished states, the valid moves, the prerequisites, the completion rule and retention.
  `tillr store init --definition <file>` records it.
- **The hook program contract,** in [the package's README](../../tillr/definition/README.md).
- **A definition that lands nothing,** in
  [examples/recorded-verdict](../../tillr/examples/recorded-verdict/README.md): it runs from
  an empty directory with no Git repository, and its items finish on a hook's recorded verdict.
- **Finished-state evidence.** Moving an item into `landed` or `resolved-elsewhere` requires
  completed criteria and a current receipt, or a pointer
  ([terminal_evidence.go](../../tillr-item/item/terminal_evidence.go)).

**Not built:**

- **Most of the landing behavior behind the hook.** Intake, staging, the candidate rule, the
  boundary check, trunk observation and conflicts are Go in the drain.
- **The remaining extension points** marked not built in [Section 5](#5-the-extension-points);
  [the build plan](tillr-definitions-plan.md) lists them as units of work.

---

## Decision Ledger

No question is open. A row dated "owner" is the owner's ruling; every other row is the designer's
decision, which the owner may overturn.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-CG1 | The shape of the one file: closed by `D-CG1`. | 2026-11-17, designer | [Section 4](#4-what-one-file-could-mean) | ✅ |
| OQ-CG2 | Item states belong to the definition: the core runs a generic lifecycle and each definition names its own states and moves. | 2026-11-16, owner | [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices) | ✅ |
| OQ-CG3 | Separation is not held behind a proof of the drain on the existing queue; it is built at once. | 2026-11-17, owner | [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices) | ✅ |
| OQ-CG4 | Closed without a decision: it asked how to brief another project's agent, not about this design. | 2026-11-17 | — | — |
| OQ-CG5 | The kind of layer is a **charter** and the Git instance is **`git-landing`**, ruled in [the vocabulary](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md). | 2026-11-18, owner | [The words](#the-words-this-document-uses) | ✅ |
| `D-CG1` | The one file is a data file plus one hook program, and item graphs stay the inside of an item. It is the seam tillr already enforces, processes and records. **Rejected:** the data file alone; the hook program alone; a workflow graph as a rival shape. | 2026-11-17, designer | [Section 4](#4-what-one-file-could-mean) | ✅ |
| `D-CG2` | The store records the definition it runs; every utility opens the store, so none can disagree. **Rejected:** a flag on each utility. | 2026-11-17, designer | [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices) | ✅ |
| `D-CG3` | The safety rule for separating code the drain runs on. **Rejected:** a freeze on the drain while it is rebuilt. | 2026-11-17, designer | [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices) | ✅ |
| `D-CG4` | Whether an item may leave a finished state is each definition's policy, stated in its valid moves. **Rejected:** reopening refused core-wide. | 2026-11-17, designer | [Section 1.1](#11-the-owners-words-as-reported-by-product-qa) | ✅ |
| `D-CG5` | The Judgment column of Section 6: which declined requests are tensions a definition settles and which are protections every definition keeps. | 2026-11-17, designer | [Section 6](#6-what-this-changes-in-what-was-said-before) | — |
