---
title: "tillr is a core of queues and graphs, and landing changes in Git is one charter on top of it"
status: accepted
stage: DECIDED
updated: 2026-11-27
tags:
  - tillr
  - design
  - boundaries
summary: "tillr is general machinery of queues and graphs whose nodes are agents and commands, with its tracking, plus one small, separate charter ('definition' in code) per use. Landing changes in a Git repository is the first charter. This document states the workflow that charter wraps, where the Git-landing assumption still lives in the core's programs, the shape a charter takes (a data file plus one hook program), what is built, the five remaining units owned here, and the sibling designs that own downstream charter extensions."
next: "Plan and build the five remaining units owned by this document in Section 8.5; sibling designs carry item-kind lifecycles, external-effect release hooks, and graph-node landing steps."
depends-on:
  - ../reference/tillr-item-graphs.md
  - tillr-graph-is-the-path.md
  - tillr-vocabulary.md
  - tillr-separable-parts.md
  - ../tillr/decision-provenance-review.md
---

# tillr is a core of queues and graphs, and landing in Git is one charter on top of it

**Status:** 2026-11-27. Every question in this direction document is settled. The direction is the owner's
([Section 1](#1-the-direction-in-the-owners-words)). The charter file, the store that records it,
finished-state projection on item and snapshot views, `not-applicable` command spend, and a second
charter that lands nothing are built ([Section 8.2](#82-what-is-built-and-what-is-not));
[Section 7](#7-where-follow-up-charter-and-non-landing-gaps-live) maps the sibling designs that
own downstream charter extensions, and [Section 8.5](#85-the-unbuilt-units) specifies the five
remaining units owned here. Ten engineering decisions in [the ledger](#decision-ledger) (`D-CG1`
to `D-CG10`) are the designer's and may be overturned by the owner.

> **In short.** tillr is a core of queues and graphs plus one charter per use, and a charter is a
> data file and one hook program. Landing changes in Git is the first charter (`git-landing`); a
> second one that lands nothing runs end to end. Five remaining core-and-view units live in
> [Section 8.5](#85-the-unbuilt-units), while downstream charter extensions (item kinds,
> external-effect release hooks, and landing steps as item-graph nodes) live in dedicated sibling
> designs ([Section 7](#7-where-follow-up-charter-and-non-landing-gaps-live)).

**Why it matters.** A use that wants tillr's queues, runs and tracking without its landing writes
a second charter instead of asking the core for exceptions.

**Cost.** Every rule about items that suits Git landing and chafes against another use leaves the
core: lifecycle, prerequisites, retention and the completion rule are each charter's to state.

**Start at [Section 5](#5-the-extension-points)** for what a charter supplies, and
[Section 3](#3-where-the-git-landing-assumption-is-wired-in-today) for what is still wired into
the core's programs.

**Needs your ruling:** None in this document; ten designer's decisions in [the ledger](#decision-ledger) (`D-CG1` to `D-CG10`) may be overturned by the owner and do not hold planning under [`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21).

**Reads with:** [the definition package's README](../../tools/tillr/definition/README.md) (the file's keys and the
hook contract), [tillr-item-graphs.md](../reference/tillr-item-graphs.md) (graphs inside one
item), [the graph design](./tillr-graph-is-the-path.md) (landing as nodes of an item's graph),
[the vocabulary](./tillr-vocabulary.md) (one meaning per word),
[tillr-separable-parts.md](./tillr-separable-parts.md) (what a use that lands nothing asks of
the core, and the contract it gets), [charter kinds, moves, and owner handoff](./tillr-charter-kinds-moves-and-owner-handoff.md),
[non-landing workflow gaps](./tillr-non-landing-workflow-gaps.md), and
[the decision provenance review](../tillr/decision-provenance-review.md). No companion plan exists yet.

---

## The words this document uses

- **The core:** the general machinery. Queues of items, graphs of nodes inside an item where each
  node is an agent or a command, and the tracking behind them: the store, items, criteria, runs,
  leases, events and views. Not anything that knows what Git is.
- **A charter** *(the owner's word, ruled in [`OQ-VO4`](./tillr-vocabulary.md#OQ-VO4); the code and
  the rest of this document say "a definition")*: the layer that turns the core into one use. It
  states that use's lifecycle, prerequisites, default criteria, observability, retention,
  completion rule and vocabulary as a data file, with one optional hook program for its judgments.
  Not an item's definition, which is its title, task, criteria and dependencies
  ([`tillr-vocabulary.md` §5.1](./tillr-vocabulary.md#51-the-work-and-its-statement); per-item
  fences were retired in favor of the shared deny-list).
- **The `git-landing` charter** *("the landing definition" below)*: the charter that lands changes
  in a Git repository, from item blocks in documents through a gate. Its data file is
  [landing.json](../../tools/tillr/definition/landing.json) and its hook program is
  [tillr-loop](../../tools/tillr-loop/).
- **Definition file:** the JSON file that states a definition's vocabulary and rules. Not a
  workflow file, which states the steps of one item's graph.
- **Hook program:** the one program a definition names, which the core calls for each judgment it
  cannot make itself. Not a step flag on the drain's command line, which names one program per
  step and wins over the hook when given.
- **Ready state, finished states:** the definition's names for the state the planner dispatches
  from and the states that end an item. Under `git-landing` they are `in-queue`, and `landed`,
  `resolved-elsewhere` and `closed`.
- **Prerequisite:** a named condition the planner checks before it dispatches an item, such as
  having a design document. A definition names which ones apply to it.
- **Store that does not land:** a store whose definition completes items some way other than a
  landing.
- **A step program:** a program the drain runs for one step and reads by its exit code
  ([steps.go](../../tools/tillr-drain/drain/steps.go)).

Store, item, drain, gate, lander, fence, criteria, candidate, hold and lane are defined in
[the vocabulary](./tillr-vocabulary.md).

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

### 1.1 Positions a second use raised

Four further positions reached this design through the requests of a second use, a pipeline that
records findings and lands nothing
([its requests](./tillr-separable-parts.md#7-a-consumers-requests-and-the-state-of-each)), and
stand as follows:

| Position | Standing |
| :--- | :--- |
| A started node is fixed; anything not started may be changed, and the core must not preclude a definition's policy | The owner's direct ruling, in [the graph design](./tillr-graph-is-the-path.md) |
| Whether a finished item may be reopened is a definition's policy | Not ruled by the owner directly. Built as each definition's list of valid moves, on the designer's `D-CG4` |
| No fixed graph size limits | The owner's direct ruling; an expanded graph reports its size and is refused for none (`D-IG4` in [tillr-item-graphs.md](../reference/tillr-item-graphs.md#why-its-this-way)) |
| No clock that fails work: budgets are targets and stalls are watched | The owner's direct ruling |

---

## 2. The workflow the landing definition wraps, as it runs today

One line per step, with the program that owns it.

1. **Intake.** `tillr-item sync` reads HTML item blocks in tracked documents
   ([sync.go](../../tools/tillr-item/item/sync.go)) and mirrored design links on opted-in roadmaps
   ([sync_design.go](../../tools/tillr-item/item/sync_design.go)) and writes them to the store.
   The drain syncs at each new trunk commit.
2. **Plan.** The drain's planner picks ready items in rank order and names a hold for the rest
   ([plan.go](../../tools/tillr-drain/drain/plan.go)).
3. **Lane.** Under `git-landing`, staging prepares an isolated Git worktree (`<root>/.tillr/items/<item-id>/worktree`
   on fresh checkouts), writes the brief and records the base commit
   ([stage.go](../../tools/tillr-drain/drain/stage.go)), while shared path protections come from
   [deny-list.json](../../config/tillr/deny-list.json)
   ([deny_list.go](../../tools/tillr-drain/internal/boundary/deny_list.go)).
4. **Run.** `tillr run exec` runs a worker session through the backend adapter the backend policy
   picks by seat ([exec.go](../../tools/tillr-run/run/exec.go),
   [backend-policy.json](../../config/tillr/backend-policy.json)).
5. **Candidate.** A run whose lane head moved off its base is a candidate; one that did not is
   refused as `candidate-no-op`.
6. **Boundary check.** Refs, worktrees outside the lane's own, and denied paths are checked around
   the run ([boundary.go](../../tools/tillr-drain/internal/boundary/boundary.go)).
7. **Block retirement.** The loop commits the removal of the item's own block before the gate
   ([retire.go](../../tools/tillr-drain/drain/retire.go)).
8. **Gate.** `tillr-loop gate` runs [repo-gate](../../tools/gate/repo-gate) within the
   budget and strategy of [gate-policy.json](../../config/tillr/gate-policy.json). A red check is
   replayed on the base before the candidate is blamed.
9. **Land.** `tillr-loop land` runs `tillr-land`, which takes the land lock, squashes `base..head`
   onto the trunk, writes the marker ref `refs/tillr/landed/<run_id>` before moving the trunk, and
   writes a `landings` row binding the gate verdict to the base commit and the head tree. A retry
   finds the marker and does not land twice
   ([landing.go](../../tools/tillr-land/land/landing.go)).
10. **Verdict.** Only after the landing, `tillr-loop verdict` records each criterion `done`.
11. **End state.** The item moves to `landed`; other ends are `resolved-elsewhere` with a pointer,
    `closed` with a reason, and `blocked` with a waiter
    ([state.go](../../tools/tillr-item/item/state.go)).
12. **Rolling tier and trunk fixer.** After landings a rolling check runs over the trunk range; a
    red trunk files a fixer item and holds the rest as `trunk-broken`
    ([trunk.go](../../tools/tillr-drain/drain/trunk.go)).
13. **Conflicts.** A squash conflict returns to the worker that produced the candidate
    ([conflict.go](../../tools/tillr-drain/drain/conflict.go)).
14. **Trunk observation.** The drain records each observed trunk tip, holds on a rewritten trunk,
    and settles an uncertain landing by reading the trunk
    ([trunk_history.go](../../tools/tillr-drain/drain/trunk_history.go)).
15. **The owner's checkout.** The trunk moves in the checked-out worktree by fast-forward only. A
    path holding uncommitted content stops the move before it starts and holds the item as
    `trunk-path-dirty` ([dirty_hold.go](../../tools/tillr-drain/drain/dirty_hold.go)).
16. **After the landing.** Optional roll and push actions are read from the settings file
    ([settings.go](../../tools/tillr/settings/settings.go)); the keeper
    ([`tillr-drain keep`](../../tools/tillr-drain/keep/keep.go)) restarts the loop.

---

## 3. Where the Git-landing assumption is wired in today

The coupling is dense in the drain and the lander, moderate in the gate and the views, and a few
lines in the store, the item face and the run face.

| Component | General part | Landing-specific part |
| :--- | :--- | :--- |
| Store (`tools/tillr`) | store, composed schema, records, events, exit codes | roll and push settings |
| `tillr-store` | init, check, recover, checkpoint, events | none |
| `tillr-item` | items, criteria, edges, decisions, mail, verdicts, close and reopen | legacy `allowed_paths` column ([schema.go](../../tools/tillr-item/item/schema.go)); sync reading tracked documents and roadmaps ([sync.go](../../tools/tillr-item/item/sync.go), [sync_design.go](../../tools/tillr-item/item/sync_design.go)); `landed` receipt check ([terminal_evidence.go](../../tools/tillr-item/item/terminal_evidence.go)) |
| `tillr-run` | lane leases, `run exec`, install-root adapters, end causes, `not-applicable` command spend ([spend.go](../../tools/tillr-run/run/spend.go)) | Git common-dir adapter fallback ([adapter.go](../../tools/tillr-run/run/adapter.go)); `GIT_CEILING_DIRECTORIES` isolation for non-landing stores ([exec.go](../../tools/tillr-run/run/exec.go)) |
| `tillr-drain` | planner, general holds, dispatch, backend policy, quotas, passes, workflows, designer sessions, open-question holds, plain-directory staging ([plain_stage.go](../../tools/tillr-drain/drain/plain_stage.go)), `complete-item` hook completion ([hook_completion.go](../../tools/tillr-drain/drain/hook_completion.go)) | under `completes: "landing"`: the candidate of worktree, base and head; staging a worktree; the Git boundary and deny-list check; block retirement; the rolling tier and fixer; trunk history; conflicts; the dirty-checkout hold; the holds only landing raises |
| `tillr-gate` | a check runner: registry, plan, run, tiers, budgets, result memory | change checks read a diff of `base..head`; memory is keyed by tree |
| `tools/gate` | none | this repository's own gate: already definition content |
| `tillr-land` | the land lock | all of it: already a separate program |
| `tillr-loop` | the adapter pattern | gate, land and verdict, translated onto the loop's exit contract |
| `tillr-view` | status (skipping push and heavy-proof checks when `Completes != "landing"` in [main.go](../../tools/tillr-view/main.go)), charter finished-state projection ([serveread.go](../../tools/tillr-view/serveread.go)), events, passes, lanes, running, waiting, trace, serve, command-vs-agent spend ([usage.go](../../tools/tillr-view/usage.go)) | the landings and trunk pages ([serve.go](../../tools/tillr-view/serve.go)), gate spans, waiting rules for landing holds; per-state finished counts remain unbuilt ([Section 8.5](#85-the-unbuilt-units)) |
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
| **A workflow graph**, where landing is one workflow among others | Not a rival. The gate and the landing become nodes of an item's graph ([the graph design](./tillr-graph-is-the-path.md)); the definition is how the core sees a use, and the graph is how one item's path runs inside it. Intake, staging and trunk observation are outside any graph. |

---

## 5. The extension points

The places a second use plugs in something other than Git landing.

| Extension point | Git landing fills it with | Status |
| :--- | :--- | :--- |
| Intake | item blocks in tracked documents and mirrored roadmaps, through sync | Built; `tillr item apply` and `default_criteria` ([definition.go](../../tools/tillr/definition/definition.go)) also exist, and a store not fed by documents is not held for lacking blocks |
| What a node is | an agent | Agent and command nodes are built, and command-only loops run without an agent backend policy ([command.go](../../tools/tillr-drain/drain/command.go)) |
| Where a node runs | a git worktree in a lane | Built; a plain directory `<lane>/work` for `completes: "hook"` is built too ([plain_stage.go](../../tools/tillr-drain/drain/plain_stage.go)) |
| What completes a node | the run ends and passes | Built, with declared outputs |
| What completes an item | a landing | Built for `completes: "hook"` (`complete-item` with `--artifact-dir` and structured decline vs. `complete-item-fault` in [hook_completion.go](../../tools/tillr-drain/drain/hook_completion.go)); moving `git-landing`'s post-worker steps into item-graph nodes belongs to [tillr-graph-is-the-path.md](./tillr-graph-is-the-path.md#56-another-definition-supplies-its-own) |
| Lifecycle | six states; finished `landed`, `resolved-elsewhere` and `closed`; reopen from `closed`, and one move out of `landed` (to `in-queue`), which a revert uses | Built as data in the definition file; per-kind lifecycles and recorded decision moves belong to [tillr-charter-kinds-moves-and-owner-handoff.md](./tillr-charter-kinds-moves-and-owner-handoff.md) |
| Prerequisites | `design-document`, `design-questions`, `owner-clean` (`fence` remains accepted for legacy files) | Built ([landing.json](../../tools/tillr/definition/landing.json), [plan.go](../../tools/tillr-drain/drain/plan.go)) |
| Graph change and size policy | single-target `may_replace`, declared on a stage ([workflow.go](../../tools/tillr-drain/workflow/workflow.go)) and applied in [graph_proposal.go](../../tools/tillr-drain/drain/graph_proposal.go); uncapped expansion ([workflow_check.go](../../tools/tillr-drain/workflow/workflow_check.go)) | Core rules built; charter `graph_policy` (`allow_downstream_insert`, `allow_append_after_finished`, `max_expanded_nodes`) is Unit 3 in [Section 8.5](#85-the-unbuilt-units) |
| Uncertain outward effect | none needed for an isolated worktree | Owned by [tillr-non-landing-workflow-gaps.md](./tillr-non-landing-workflow-gaps.md) (`release-run` hook and `run-release-failed` hold, [OQ-NLW2](./tillr-non-landing-workflow-gaps.md#OQ-NLW2)) |
| Spend per step | wall time, model tokens, and `spend_status: "not-applicable"` on commands | Built ([spend.go](../../tools/tillr-run/run/spend.go), [spend.go](../../tools/tillr-drain/internal/drainmodel/spend.go), [usage.go](../../tools/tillr-view/usage.go)) |
| Observability | `progress_event` and `rule_pack` in the charter file | Built ([definition.go](../../tools/tillr/definition/definition.go), [lint_charter.go](../../tools/tillr/watch/lint_charter.go)) |
| Retention | nothing in the store is pruned (`retention: "keep"`); bounded worktree/scratch collection in `worktree_collect.go` ([`D-VO10`](./tillr-vocabulary.md#34-the-bound-and-what-happens-at-it)) | `"keep"` is built; no store-event pruning rule is requested (`D-CG9`) |
| Outcome vocabulary and views | `ProjectItemState` projects charter `finished` states ([serveread.go](../../tools/tillr-view/serveread.go)) and `view status` skips push/heavy checks when `Completes != "landing"` ([main.go](../../tools/tillr-view/main.go)) | Projection built; per-state finished counts and hiding Landings/Trunk tabs are Unit 1 in [Section 8.5](#85-the-unbuilt-units), and the `detail` hook panel is Unit 2 |
| Dispatch policy | lane count, seats, backend policy | Built, with seat shares, express lanes and a spend ceiling |
| The judge | the gate program and the verdict program | Built, with a judge before the gate and an evaluator node; non-landing `evaluator_slot` belongs to [tillr-non-landing-workflow-gaps.md](./tillr-non-landing-workflow-gaps.md) |
| Trunk observation | the rolling program, trunk history | Built, as step programs and Go |
| After completion | roll and push settings ([settings.go](../../tools/tillr/settings/settings.go)) | Built as settings; calling the `after-completion` hook is Unit 5 in [Section 8.5](#85-the-unbuilt-units) |
| An owner acknowledgment point | withdrawn by the consumer, which keeps its own cursor ([tillr-separable-parts.md §7.3](./tillr-separable-parts.md#73-the-first-list)) | Withdrawn |

---

## 6. What this changes in what was said before

[The separable-parts proposal](./tillr-separable-parts.md) lists requests the core declines. The
owner's decision rule reads each as a possible tension between Git landing and another use. The
Judgment column is the designer's (`D-CG5`).

| Request | A tension? | Judgment |
| :--- | :--- | :--- |
| C1 Outcomes per kind | Yes | The definition's. Core protection: each definition's set is closed and projected one way. |
| C2 Reopening a finished item | Yes | The definition's. Inside Git landing only a revert leaves `landed`, and `closed` reopens. Core protection: a move out of a finished state is an event, never silent. |
| C3 Steps reshaping the graph | Partly | Which reshaping operations exist is the definition's. That each is applied by one writer, atomically with its event, and never to a started node, is a protection every use needs. |
| C4 An uncertain outside effect as a state | Yes | The worktree rule is Git landing's. Core protection: an effect a definition declares is recorded before and after, so a retry can see it. |
| C5 Clocks and deadlines in the plan | Partly | A deadline as an ordering input is the definition's. The planner staying a replayable function of the store is a protection. |
| C6 A library as the connection | No | A protection: processes and records are how a definition injects anything without linking the core. |
| C7 Completion that is not a landing | Yes | The completion rule is the definition's. Core protection: an item completes once, on a recorded receipt. |
| C8 A second loop in one root | No | One writer per store and at-most-once launch are protections; a second use runs its own store and root. |
| The undeclared-item hold | Yes | A prerequisite, so the definition's, and applied only to stores that documents feed. |

---

## 7. Where follow-up charter and non-landing gaps live

Because this document records the architectural split between the core and charters, later consumer
reports and roadmap entries opened dedicated designs for downstream charter extensions that need
their own owner questions or graph machinery. Keeping that ownership explicit prevents companion
plans from filing duplicate build items:

- **Item kinds, recorded decision moves, and non-finishing `complete-item` owner handoffs**
  ([R3-1 and R3-2](./tillr-separable-parts.md#74-further-requests)): owned by
  [tillr-charter-kinds-moves-and-owner-handoff.md](./tillr-charter-kinds-moves-and-owner-handoff.md)
  ([OQ-CK1](./tillr-charter-kinds-moves-and-owner-handoff.md#OQ-CK1) to
  [OQ-CK3](./tillr-charter-kinds-moves-and-owner-handoff.md#OQ-CK3)).
- **Non-landing `evaluator_slot`, conditional stage skips, run-death external-effect cleanup
  (`release-run` for `R2-12` / `C4`), and plain-directory boundary checks (`TakePlainBoundary`)**:
  owned by [tillr-non-landing-workflow-gaps.md](./tillr-non-landing-workflow-gaps.md)
  ([OQ-NLW1](./tillr-non-landing-workflow-gaps.md#OQ-NLW1) and
  [OQ-NLW2](./tillr-non-landing-workflow-gaps.md#OQ-NLW2)).
- **Moving post-worker Git-landing steps (gate, land attempt, take-in, cut, conflict return) into
  item-graph nodes governed by a charter's policy table**: owned by
  [tillr-graph-is-the-path.md](./tillr-graph-is-the-path.md#56-another-definition-supplies-its-own).
- **Declaring repository-specific landing steps and removing fork-specific code from the core**:
  tracked as [Roadmap entry 3](../tillr/roadmap.md#2-a-core-that-carries-only-its-own-code), which
  awaits its own design starting from this document.
- **Consumer packaging, single-dispatcher recipes, and the `tillr-watch` non-landing rule pack**:
  tracked in [tillr-separable-parts.md](./tillr-separable-parts.md#3-proposed-work).

The five units that remain directly owned by *this* document — finishing the charter-driven views,
wiring the already-specified `detail` and `after-completion` hooks, adding charter-declared
`graph_policy` and `max_expanded_nodes`, and pinning `blocked`-item waiting parity — are specified
in [Section 8.5](#85-the-unbuilt-units).

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
| 6 | Reopening and graph change are a definition's policy; no fixed graph caps; no clock whose only consequence is a kill | The owner's, except reopening ([Section 1.1](#11-positions-a-second-use-raised)) | `D-CG4` |

### 8.1 What a store that does not land needs from the core

A store declared not to land avoids four things:

- **Holds only landing raises:** among them `candidate-no-op`, `gate-failed`, `land-refused`,
  `trunk-broken`, `trunk-path-dirty`, `trunk-rewritten` and the conflict holds.
- **Git at the loop's start:** the trunk read, the startup sync, trunk history, the rolling tier,
  and roll and push after a landing.
- **Staging:** a Git worktree and a base commit (staging a plain `<lane>/work` directory instead via [plain_stage.go](../../tools/tillr-drain/drain/plain_stage.go)).
- **The end of a run:** the candidate rule, the Git boundary check, the gate, the lander and the move
  to `landed`.

What stays in the core for every definition: the store and its events, items, criteria, verdicts,
edges, lanes and leases, `run exec`, the planner's general holds, backend policy and quotas,
workflows and their nodes, and the views.

### 8.2 What is built, and what is not

**Built:**

- **The definition file and the store that records it.**
  [landing.json](../../tools/tillr/definition/landing.json) names the states, the ready state, the
  finished states, the valid moves, the prerequisites (`design-document`, `design-questions`,
  `owner-clean`), the completion rule and retention; custom charters may also declare
  `default_criteria` and `observability` ([definition.go](../../tools/tillr/definition/definition.go)).
  `tillr store init --definition <file>` records it and `tillr store definition --set <file>`
  replaces it atomically.
- **The hook program contract and non-landing completion.** [The package's README](../../tools/tillr/definition/README.md)
  defines `describe` and the nine hook operations; `complete-item` receives `--item`, `--run`, `--store`, `--root`,
  and `--artifact-dir`, distinguishes a structured decline (`{"complete": false, "reason": "..."}`)
  from a crash or malformed answer (`complete-item-fault` in
  [hook_completion.go](../../tools/tillr-drain/drain/hook_completion.go)), and command-only loops run
  without an agent backend policy ([command.go](../../tools/tillr-drain/drain/command.go)).
- **A definition that lands nothing,** in
  [examples/recorded-verdict](../../tools/tillr/examples/recorded-verdict/README.md), verified end
  to end without Git by `TestRecordedVerdictExampleREADME` in
  [recorded_verdict_example_test.go](../../tools/tillr-drain/drain/recorded_verdict_example_test.go).
- **Finished-state projection and non-applicable command spend.** `ProjectItemState` in
  [serveread.go](../../tools/tillr-view/serveread.go) projects charter finished states on item and
  snapshot pages, `tillr view status` in [main.go](../../tools/tillr-view/main.go) omits push and
  heavy-proof checks when `Completes != "landing"`, and command runs record
  `spend_status: "not-applicable"` ([spend.go](../../tools/tillr-run/run/spend.go),
  [non_landing_test.go](../../tools/tillr-view/non_landing_test.go)).
- **Finished-state evidence.** Moving an item into `landed` or `resolved-elsewhere` requires
  completed criteria and a current receipt, or a pointer
  ([terminal_evidence.go](../../tools/tillr-item/item/terminal_evidence.go)).

**Not built:**

- **The five core-and-view units owned by this document** in [Section 8.5](#85-the-unbuilt-units):
  per-state finished counts and charter-gated Landings/Trunk tabs, the `detail` hook panel,
  charter-declared `graph_policy` and `max_expanded_nodes`, `blocked`-item waiting parity across
  CLI and HTTP, and calling the `after-completion` hook.
- **Downstream charter and landing-graph work owned by sibling designs** ([Section 7](#7-where-follow-up-charter-and-non-landing-gaps-live)):
  moving `git-landing`'s post-worker steps into item-graph nodes
  ([tillr-graph-is-the-path.md](./tillr-graph-is-the-path.md#56-another-definition-supplies-its-own)),
  per-kind lifecycles and decision moves
  ([tillr-charter-kinds-moves-and-owner-handoff.md](./tillr-charter-kinds-moves-and-owner-handoff.md)),
  and `release-run` / `evaluator_slot` / `TakePlainBoundary`
  ([tillr-non-landing-workflow-gaps.md](./tillr-non-landing-workflow-gaps.md)).

### 8.3 Rules the build settled

- **"Ready" governs an item's own work, not the answer to a conflict.** The item's own work is dispatched only from the definition's ready state; a conflict's answer run is dispatched from the definition's blocked state. Under a definition that does not complete on a landing, the conflict holds are not raised, and an item in the blocked state is held `blocked`.
- **One finished-state rule.** Every reader in the drain and the view asks the store's definition which states are finished; none keeps a list of its own.
- **Each prerequisite name gates its own holds, including the checks that come before them.** A prerequisite the definition does not name is not read at all, so it can neither hold nor fail. A definition that does not name `design-questions` never asks the comment source anything.
- **The planner reads the definition once per pass** and judges every item, including one already in flight, against that reading. A definition replaced while a run is in flight takes effect at the next pass, and the definition is not recorded on a dispatch.
- **Every hold reason is a named constant with a waiting rule in the view,** so a test can find every reason and class it as general or landing-only.
- **Finished states come from the definition; frozen states take no receipt.** The states `abandoned`, `deleted` and `in-progress` stay readable and are never written under any definition.
- **A repeated move appends nothing.** A write of the state an item already has answers the item and adds no event, so it does not overwrite a stored receipt.

### 8.4 Completion through the definition's hook for items that never land

An item of a store that does not land is finished when it has been judged, not when a commit lands. A definition whose `completes` value is `hook` finishes its items through the `complete-item` hook: once a run ends, the hook records the verdicts and names the finished state, and the drain moves the item there with the reason, pointer and actor.

- **The verdict check belongs to the move, not to the drain.** `tillr item apply` can append a criterion between a read in the drain and the state move, and the item would then finish with a criterion nobody judged. So the check runs inside the move's own write transaction: `tillr item state --require-verdicts` refuses the move as `criteria-unjudged` unless every criterion the item holds at that moment has a recorded verdict naming the hash of its stored text. The drain passes the flag on every move a `complete-item` answer causes, turns the refusal into a hold, and keeps no second check of its own.
- **The flag is opt-in.** Without it a move behaves as before, so the loop's move to `landed` and the owner's hand moves are untouched. No definition key makes the rule mandatory (`D-CG10`): the drain already passes the flag on every move a `complete-item` answer causes, and a hand move that skips it stays possible and is an event with its actor.
- **Recovery after the move never enters Git.** Under a definition that completes on `hook`, recovery of an ended run reads the item. If the item is already in a finished state, the move happened: the drain writes the end-of-run records that are missing and does not call the hook again. Otherwise it runs the completion step again from the hook call.
- **Calling the hook twice is safe.** A verdict recorded twice is two events for the same answer, and a move to the state an item already has appends nothing.
- **The Git-only checks made when a run is claimed are skipped under `hook`,** and run as before under `git-landing`.

### 8.5 The unbuilt units

Five requests of the second use are not units of this document, because each is built, withdrawn
or owned by a sibling design:

- **Built: time and spend per step attempt**
  ([R2-13](./tillr-separable-parts.md#73-the-first-list)). Command runs record
  `spend_status: "not-applicable"` with nil `usage` in
  [spend.go](../../tools/tillr-run/run/spend.go), and
  [spend.go](../../tools/tillr-drain/internal/drainmodel/spend.go) and
  [usage.go](../../tools/tillr-view/usage.go) keep them out of unreported-spend counts.
- **Withdrawn: an owner acknowledgment point**
  ([R2-14](./tillr-separable-parts.md#73-the-first-list)). The consumer keeps its own cursor
  ([tillr-separable-parts.md §7.1](./tillr-separable-parts.md#71-the-use-the-requests-come-from)).
- **Built and pinned: run linkage on a plain lane.** Runs the drain starts on a store that does not
  land link cleanly across a multi-stage workflow in `TestRecordedVerdictExampleREADME`
  ([recorded_verdict_example_test.go](../../tools/tillr-drain/drain/recorded_verdict_example_test.go)).
  A run written by a second dispatcher on a store the drain plans is held `run-link-missing` on
  purpose, in `TestPlanHoldsAnItemThatMayBeARetry`
  ([main_cli_test.go](../../tools/tillr-drain/drain/main_cli_test.go); `P2` in
  [tillr-separable-parts.md §3](./tillr-separable-parts.md#3-proposed-work)).
- **Owned by a sibling design: an uncertain outward effect on run death**
  ([R2-12](./tillr-separable-parts.md#73-the-first-list), `C4`). It is the `release-run` hook and
  the `run-release-failed` hold of
  [tillr-non-landing-workflow-gaps.md](./tillr-non-landing-workflow-gaps.md), which waits on
  [OQ-NLW2](./tillr-non-landing-workflow-gaps.md#OQ-NLW2).
- **Built: a graph's size reported and never refused** (`R2-10`, `D-IG4`). The workflow check
  reports size and has no depth or node cap
  ([workflow_check.go](../../tools/tillr-drain/workflow/workflow_check.go));
  [design-from-scouts@v1.json](../../config/tillr/workflows/design-from-scouts@v1.json) bounds
  scout fan-out at `most: 8`. The budget a definition may set is part of Unit 3 below.

The five remaining units owned directly by this document rest on the owner's settled rulings and the
designer's engineering decisions `D-CG1` to `D-CG10` in [the ledger](#decision-ledger) (none holds
planning under [`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21)):

1. **Count outcomes per charter finished state and gate landing-only views**
   ([R2-8](./tillr-separable-parts.md#73-the-first-list), `D-CG6`). `rg -n 'finished_by_state' tools/tillr-view`
   returns nothing today. Add `completes: declared.Parsed.Completes` and
   `finished_by_state: map[string]int` — seeded with `0` for every state in
   `declared.Parsed.Finished` so zero-count outcomes remain countable — to `view.status`
   ([main.go](../../tools/tillr-view/main.go)) and `/api/v1/snapshot`
   ([serve.go](../../tools/tillr-view/serve.go), [serveread.go](../../tools/tillr-view/serveread.go)),
   aligned with the base Observatory layout kept in [`OQ-PR5`](../tillr/decision-provenance-review.md#OQ-PR5).
   In the Observatory web UI, hide the Landings and Trunk navigation tabs and landing-only header
   bands when `snapshot.completes != "landing"`, while keeping `GET /api/v1/landings` answering
   `200 []` on non-landing stores so API clients do not fault.
2. **Add an Item-page detail panel from the charter's `detail` hook**
   ([R2-7](./tillr-separable-parts.md#73-the-first-list), `D-CG7`). The `<argv> detail --body-ref <ref> --root <root>`
   contract (stdout `record.Envelope` of kind `item.detail`; exit `0` record, `1` none, `>= 2`
   fault) is documented in [the definition README](../../tools/tillr/definition/README.md) and
   validated at startup in [hooks.go](../../tools/tillr-drain/drain/hooks.go) and
   [lint_charter.go](../../tools/tillr/watch/lint_charter.go), while `rg -n 'item\.detail' tools/tillr-view`
   returns nothing. Add `GET /api/v1/items/{id}/detail` in [serve.go](../../tools/tillr-view/serve.go):
   read `body_ref` and `store.definition` in a short `ReadTx` and close the reader before spawning
   the hook; return `{"status":"absent"}` when `body_ref == ""` or `hooks.names` omits `"detail"`,
   otherwise run `<argv> detail --body-ref <ref> --root <root>` under a 5-second timeout, whose expiry is a fault (with
   `GIT_CEILING_DIRECTORIES` when `Completes != "landing"`) and map exit `0` to
   `{"status":"ok","payload":...}`, exit `1` to `{"status":"none"}`, and exit `>= 2` or malformed
   JSON to `{"status":"fault","detail":...}` without failing the rest of the Item page in
   [ItemDetailPane.tsx](../../tools/tillr-view/web/src/components/ItemDetailPane.tsx).
3. **Add charter-declared `graph_policy` and optional `max_expanded_nodes` budget**
   ([R2-9 and R2-10](./tillr-separable-parts.md#73-the-first-list), `D-CG8`). `rg -n 'graph_policy|allow_downstream_insert|allow_append_after_finished|max_expanded_nodes' tools/`
   returns nothing today. Add an optional `graph_policy` struct on `definition.Definition` in
   [definition.go](../../tools/tillr/definition/definition.go) (checked by
   [lint_charter.go](../../tools/tillr/watch/lint_charter.go)) with three fields:
   `allow_downstream_insert` (bool, default `false`), allowing a running stage's `graph.proposal`
   in [graph_proposal.go](../../tools/tillr-drain/drain/graph_proposal.go) to insert one tracked
   stage or sub-workflow immediately downstream of itself with matching output shape while leaving
   its own started node untouched; `allow_append_after_finished` (bool, default `false`, valid only
   when the charter declares at least one transition from a `finished` state back to `ready`),
   allowing stages to be appended on reopen while keeping prior `node_runs` and `node_artifacts`
   immutable; and `max_expanded_nodes` (int, `0` = unlimited), which causes the planner in
   [plan.go](../../tools/tillr-drain/drain/plan.go) to hold an item exceeding the charter's budget
   as `workflow-size-budget` naming `owner` as mover — never a silent refusal. The four core
   protections (single-writer store transaction with `graph_revisions` and `drain.graph-changed`,
   no started node altered, no self-replacement, and downstream input/output shape check) stay
   unconditional.
4. **Pin `blocked`-item waiting parity across CLI and HTTP.** `tillr view waiting` and
   `GET /api/v1/waiting` share `assembleWaitingAt` in [waiting.go](../../tools/tillr-view/waiting.go)
   and the reason table in [holdrules.go](../../tools/tillr-drain/holdrules/holdrules.go), and
   `TestWaitingCLIAndAPI` in [waiting_internal_test.go](../../tools/tillr-view/waiting_internal_test.go)
   checks parity on an unplanned `blocked` seed (`item-seed-06`), leaving planned `blocked` items
   carrying `waiting_on` metadata (`waiter: "owner"`, `waiter: "design"`, and
   `waiter: "item:<id>"`) with recorded `hold` dispatches to be asserted across both faces.
5. **Call the `after-completion` hook after item completion** (`D-CG9`). The
   `<argv> after-completion --item <id> --store <path> --root <root>` contract is specified in
   [the definition README](../../tools/tillr/definition/README.md) and validated at startup in
   [hooks.go](../../tools/tillr-drain/drain/hooks.go) and
   [lint_charter.go](../../tools/tillr/watch/lint_charter.go), while
   `rg -n 'AfterCompletion|after-completion-fault' tools/tillr-drain tools/tillr-loop` returns
   nothing. Add the call to the drain's step set ([steps.go](../../tools/tillr-drain/drain/steps.go))
   and invoke it outside SQLite write locks once an item enters a `finished` state (both after
   landing under `completes: "landing"` and after `complete-item` under `completes: "hook"`) when
   `hooks.names` includes `"after-completion"`, recording an idempotent `drain.after-completion`
   event keyed by `(item_id, completion_event_seq)` so crash recovery runs an unrecorded follow-up
   at most once and surfaces exit `>= 2` as an `after-completion-fault` alarm without undoing the
   completed item. Exit `1`, the hook's negative answer, is recorded on the same event and is
   neither retried nor alarmed. Expose a `tillr-loop after-completion` subcommand in
   [tools/tillr-loop](../../tools/tillr-loop/) wrapping [settings.go](../../tools/tillr/settings/settings.go)
   roll and push so charters naming `"after-completion"` run roll/push through the hook seam while
   embedded [landing.json](../../tools/tillr/definition/landing.json) (`hooks: null`) keeps
   existing behavior unchanged under `D-CG3`. Store `retention` remains `"keep"` until a concrete
   owner or consumer request asks to prune store events or run records.

**The safety rule every change to the core carries** (`D-CG3`). Under `git-landing` the queue
behaves identically, the existing suites pass unchanged, and a store created before the change
opens and drains. The drain runs on this code while it is rebuilt, so a change that breaks the
Git-landing queue stops the building of the rest.

The embedded `git-landing` definition leaves `hooks` unset: the drain's step flags name the six
step programs. Naming `tillr-loop` in the embedded definition makes the drain run the hook
program's `describe` at every loop start, including in fixtures that have no such program, so it
waits on a change to the drain's start check or its fixtures.

---

## Decision Ledger

No question is open in this document. [`OQ-CG2`](#OQ-CG2), [`OQ-CG3`](#OQ-CG3) and
[`OQ-CG5`](#OQ-CG5) record the owner's rulings (with [`OQ-VO4`](./tillr-vocabulary.md#OQ-VO4) and
[`OQ-VO5`](./tillr-vocabulary.md#OQ-VO5) quoted in [the vocabulary](./tillr-vocabulary.md));
`D-CG1` to `D-CG10` are the designer's engineering decisions, which the owner may overturn and
which do not hold planning under [`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21).

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-CG1 | The shape of the one file: closed by `D-CG1`. | 2026-11-17, designer | [Section 4](#4-what-one-file-could-mean) | ✅ |
| OQ-CG2 | Item states belong to the definition: the core runs a generic lifecycle and each definition names its own states and moves. | 2026-11-16, owner | [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices) | ✅ |
| OQ-CG3 | Separation is not held behind a proof of the drain on the existing queue; it is built at once. | 2026-11-17, owner | [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices) | ✅ |
| OQ-CG4 | Closed without a decision: it asked how to brief another project's agent, not about this design. | 2026-11-17 | — | — |
| OQ-CG5 | The kind of layer is a **charter** and the Git instance is **`git-landing`**, ruled in [`OQ-VO4`](./tillr-vocabulary.md#OQ-VO4) and [`OQ-VO5`](./tillr-vocabulary.md#OQ-VO5). | 2026-11-18, owner | [The words](#the-words-this-document-uses) | ✅ |
| `D-CG1` | **Designer's decision; the owner may overturn this.** The one file is a data file plus one hook program, and item graphs stay the inside of an item. It is the seam tillr already enforces, processes and records. **Rejected:** the data file alone; the hook program alone; a workflow graph as a rival shape. | 2026-11-17, designer | [Section 4](#4-what-one-file-could-mean) | ✅ |
| `D-CG2` | **Designer's decision; the owner may overturn this.** The store records the definition it runs; every utility opens the store, so none can disagree. **Rejected:** a flag on each utility. | 2026-11-17, designer | [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices) | ✅ |
| `D-CG3` | **Designer's decision; the owner may overturn this.** The safety rule for separating code the drain runs on. **Rejected:** a freeze on the drain while it is rebuilt. | 2026-11-17, designer | [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices) | ✅ |
| `D-CG4` | **Designer's decision; the owner may overturn this.** Whether an item may leave a finished state is each definition's policy, stated in its valid moves. **Rejected:** reopening refused core-wide. | 2026-11-17, designer | [Section 1.1](#11-positions-a-second-use-raised) | ✅ |
| `D-CG5` | **Designer's decision; the owner may overturn this.** The Judgment column of Section 6: which declined requests are tensions a definition settles and which are protections every definition keeps. | 2026-11-17, designer | [Section 6](#6-what-this-changes-in-what-was-said-before) | — |
| `D-CG6` | **Designer's decision; the owner may overturn this.** Expose `completes` and zero-seeded `finished_by_state` on `view.status` and `/api/v1/snapshot`, hide Landings/Trunk Observatory navigation when `completes != "landing"`, and keep `GET /api/v1/landings` returning `200 []` for API compatibility. **Rejected:** returning `404` on `/api/v1/landings` or omitting zero-count finished states. | 2026-11-27, designer | [Section 8.5](#85-the-unbuilt-units) | — |
| `D-CG7` | **Designer's decision; the owner may overturn this.** Serve `GET /api/v1/items/{id}/detail` by reading `body_ref` and `store.definition` in a short `ReadTx` and closing the SQLite reader before running `<argv> detail`, isolating non-landing stores with `GIT_CEILING_DIRECTORIES` and rendering hook faults inline without failing the Item page. **Rejected:** calling the hook inside `GET /api/v1/items/{id}` on every poll or holding a SQLite read lock across subprocess execution. | 2026-11-27, designer | [Section 8.5](#85-the-unbuilt-units) | — |
| `D-CG8` | **Designer's decision; the owner may overturn this.** Represent charter graph-reshaping and size policy as an optional `graph_policy` struct (`allow_downstream_insert`, `allow_append_after_finished`, `max_expanded_nodes`) enforced inside the core's four invariants, holding oversized expansions as `workflow-size-budget` with `owner` as mover. **Rejected:** arbitrary runtime rewrites of started nodes or silent workflow-expansion refusals. | 2026-11-27, designer | [Section 8.5](#85-the-unbuilt-units) | — |
| `D-CG9` | **Designer's decision; the owner may overturn this.** Run `after-completion` outside SQLite write locks once per `(item_id, completion_event_seq)` via a `drain.after-completion` event, wrap post-landing roll/push in `tillr-loop after-completion` while keeping embedded `landing.json` `hooks: null`, and keep `retention: "keep"` as the sole store-retention value until a consumer or owner asks to prune history. **Rejected:** rolling back a completed item on `after-completion` fault or inventing unasked store-event deletion rules. | 2026-11-27, designer | [Section 8.5](#85-the-unbuilt-units) | — |
| `D-CG10` | **Designer's decision; the owner may overturn this.** The verdict check on a move into a finished state stays a flag the caller passes (`--require-verdicts`), which the drain passes on every `complete-item` move; no definition key makes it mandatory. **Rejected:** a definition key that refuses every unjudged move, which would also refuse the owner's hand moves and which no use has asked for. | 2026-11-27, designer | [Section 8.4](#84-completion-through-the-definitions-hook-for-items-that-never-land) | ✅ |
