---
title: "Build plan: a tillr core with Git landing as its first definition"
status: accepted
stage: DECIDED
updated: 2026-11-24
tags:
  - tillr
  - plan
  - store
  - boundaries
  - product-qa
summary: "The build plan that turns tillr into a core that does not assume Git landing, with Git landing supplied by one definition and a second, runnable definition that lands nothing. The first tranche of eighteen items is built; this document states what each gives a second use, the rules the build settled, and the next tranche, which is unfiled."
next: "File the next tranche (Section 3) once the designer's decisions it rests on, D-CG1 to D-CG5 in the direction document's ledger, are reviewed by the owner."
depends-on:
  - tillr-core-and-git-landing.md
  - tillr-separable-parts.md
  - tillr-product-qa-requests.md
  - ../reference/tillr-item-graphs.md
  - tillr-judge-and-lifecycle.md
---

# Build plan: a tillr core with Git landing as its first definition

> **Delivered copy.** This is `docs/design/tillr-definitions-plan.md` from this repository at commit `0275c51d52` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-definitions-plan.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** the first tranche is built: the definition package, the store that records a definition, the item lifecycle and the planner reading it, the hook program contract, completion through a hook, and a worked example that runs with no Git repository. The next tranche ([Section 3](#3-the-next-tranche-in-prose)) is unfiled.

> **In short.** tillr's core runs a generic lifecycle and asks a definition for everything that belongs to one use. The acceptance test is a worked definition that lands nothing and runs from an empty directory: if it runs, the core does not assume Git landing.

**Why it matters.** The direction is that tillr is a general core and that Git landing is one definition on top of it ([the direction](tillr-core-and-git-landing.md#1-the-direction-in-the-owners-words)). A second use, a pipeline whose items are findings and not changes, is what tests that split, and its requests are the ones this plan answers ([the separable-parts table](tillr-separable-parts.md#7-the-second-round-tillrs-answer)).

**Reads with:** [the direction](tillr-core-and-git-landing.md) (what the core and a definition are, and the choices this build rests on), [the separable-parts proposal](tillr-separable-parts.md#7-the-second-round-tillrs-answer) (each request and what answers it), [the consuming pipeline's reply](tillr-product-qa-requests.md), and [the definition package's README](../../tillr/definition/README.md) (the contract a second use writes against).

This document holds no open question on purpose. The drain holds an item whose own document still asks something, so the questions stay in the direction document and this plan links them.

---

## The words this plan uses

- **Product QA:** a bug-finding pipeline that uses tillr as a consumer: its items are findings, and none of them lands a commit. Not [tillr-autoqa.md](tillr-autoqa.md), which is tillr's own exploratory-testing design.
- **Definition** *(coined in the direction document; the owner's word is **charter**, [OQ-VO4](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md#OQ-VO4))*: the layer that turns tillr's core into one use. It is a data file plus one hook program. **`git-landing`** is the definition for landing changes in Git ([OQ-VO5](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md#OQ-VO5)). This plan and the shipped code say "definition"; the code is not renamed, and the name is kept in as few places as possible so that a rename is mechanical.
- **Definition file** *(coined here)*: the JSON file that states a definition's vocabulary and rules. Not a workflow file, which states the steps of one item's graph.
- **Hook program** *(coined here)*: the one program a definition names, which the core calls for each judgment it cannot make itself. Not a step flag on the drain's command line, which names one program per step and wins over the hook when given.
- **Ready state, finished states:** the definition's names for the state the planner dispatches from and the states that end an item. Under `git-landing` they are `in-queue`, and `landed`, `resolved-elsewhere` and `closed`.
- **Prerequisite:** a named condition the planner checks before it dispatches an item, such as having a design document. A definition names which ones apply to it.
- **Hold:** the planner's named reason for not dispatching an item. **Lane:** a leased slot with its own directory where one run executes.
- **Store that does not land:** a store whose definition completes items some way other than a landing.

---

## 1. How to read this

**Precedence.** The direction document wins on behavior, and the tree wins on fact.

**The safety rule every change to the core carries.** Under `git-landing` the queue behaves identically, the existing suites pass unchanged, and a store created before the change opens and drains. The drain runs on this code while it is rebuilt, so a change that breaks the Git-landing queue stops the building of the rest.

**Order is enforced by edges, not by names.** The planner dispatches ready items in id order and does not serialize items that change the same package, so an item that rewrites the same package as another names it as a blocker.

**Reuse before writing.**

- Schema registration is per owner: `RegisterDDL` in [registry.go](../../tillr/store/registry.go). A table belongs to the utility that registers it.
- State moves record their reason and actor through one event shape: `recordLifecycle` in [lifecycle.go](../../tillr-item/item/lifecycle.go). Extend it; do not add a second.
- The drain checks its step programs' flags before dispatch ([step_check.go](../../tillr-drain/drain/step_check.go)), and the hook check extends that.
- The shape a hook program follows is [tillr-loop](https://github.com/mschulkind-oss/tillr/blob/main/tools/tillr-loop/tillr-loop): one file, one face per step, a record out.

**Traps.**

- `tillr item sync` reads only Git-tracked files under `docs/`, so a block in an uncommitted file is invisible to a dry run until it is staged.
- A read face opens a store read-only and never migrates it.
- The pack carries a copy of every `tools/tillr*` module, so a change under `tools/tillr*` also changes `yolo-packs/tillr/`.
- No Git, file or process work runs inside a SQLite write transaction anywhere in tillr, and a hook call is process work.

---

## 2. The first tranche

All eighteen items are built. Each row says what a second use can do with it.

| What it does | What a second use can try | Where it lives |
| :--- | :--- | :--- |
| Raw writes cannot reopen a landed or resolved item: the move is refused as `state-terminal` | Finish an item and trust that it stays finished | `tools/tillr-item/item/` |
| Every utility's tables exist once a store is created | `tillr store init`, then any read face, with nothing in between | `tools/tillr-store/` links each utility's schema package |
| The definition file and its reader, with `git-landing` embedded | Read the package README as the contract for a definition of their own | [`tools/tillr/definition/`](../../tillr/definition/README.md), [landing.json](../../tillr/definition/landing.json) |
| A store records the definition it runs; a store with none recorded runs `git-landing` | `tillr store init --definition <file>`, and `tillr store definition` to read it back | `tools/tillr-store/`, `tools/tillr/definition/` |
| A face that lists a store's items | `tillr item list --state <state>` to count items by state | `tools/tillr-item/` |
| `tillr item apply` refuses a record carrying a field it does not store | Apply their own records and see an unstored field named in the refusal | `tools/tillr-item/item/` |
| Item states and their moves come from the store's definition | A definition whose items end `confirmed`, `rejected` or `inconclusive`, and which allows a rejected one to be reopened with a reason | `tools/tillr-item/item/`, `IsFinished` in the definition package |
| A reason, a pointer and an actor are stored on every move into a finished state (`--reason`, `--resolves-via`, `--by`); a flag that would not be stored is refused as `flag-not-stored` | Finish an item as a repeat of another and read both back from `tillr item show` | `SetStateWithReceipt` in [state.go](../../tillr-item/item/state.go) |
| The planner's ready, finished and prerequisite tests read the definition, and the holds only landing can raise are in one table | `tillr drain why` on a store that does not land, with no landing-only hold | [definition.go](../../tillr-drain/drain/definition.go) in the drain |
| The hook program contract, checked when the loop starts | Write a hook program and see the loop refuse it at start, by name, when it is missing a hook | "Hook program contract" in the package README; `hookFlags` in [hooks.go](../../tillr-drain/drain/hooks.go) |
| `tillr-loop` answers the contract for the six Git-landing steps, with `fixer-gate` as a face of its own | Read one real hook program as the model for their own | [tillr-loop](https://github.com/mschulkind-oss/tillr/blob/main/tools/tillr-loop/tillr-loop) |
| Every run's environment carries the item and the node (`TILLR_ITEM_ID`, `TILLR_NODE_ID`) | Read the item id from the environment instead of parsing a run id | [exec.go](../../tillr-run/run/exec.go) |
| The item page shows every run, verdict, decision and state move | Open an item's page and audit what happened to it | [item_history.go](../../tillr-view/item_history.go) |
| The loop starts with no Git when the definition does not land | Start the loop on a plain directory | `tools/tillr-drain/drain/` |
| Staging gives a run a plain directory, and the brief comes from the definition's own template | An agent step briefed from their own template | `tools/tillr-drain/drain/`, `brief_template` in the definition file |
| [Completion through the definition's hook](#add-completion-through-the-definitions-hook-for-items-that-never-land) | An item that runs, is judged, and ends in a finished state the hook names | [hook_completion.go](../../tillr-drain/drain/hook_completion.go) |
| A command node runs in the lane directory and its program is given its node id | A two-step workflow, a command and then an agent, on one item | [command.go](../../tillr-drain/drain/command.go) |
| A worked definition that lands nothing, runnable end to end | Run the README's commands as written, from an empty directory | [`tools/tillr/examples/recorded-verdict/`](../../tillr/examples/recorded-verdict/README.md) |

The embedded `git-landing` definition leaves `hooks` unset: the drain's step flags name the six step programs. Naming `tillr-loop` in the embedded definition makes the drain run the hook program's `describe` at every loop start, including in fixtures that have no such program, so it waits on a change to the drain's start check or its fixtures.

### Rules the build settled

- **"Ready" governs an item's own work, not the answer to a conflict.** The item's own work is dispatched only from the definition's ready state; a conflict's answer run is dispatched from the definition's blocked state. Under a definition that does not complete on a landing, the conflict holds are not raised, and an item in the blocked state is held `blocked`.
- **One finished-state rule.** Every reader in the drain and the view asks the store's definition which states are finished; none keeps a list of its own.
- **Each prerequisite name gates its own holds, including the checks that come before them.** A prerequisite the definition does not name is not read at all, so it can neither hold nor fail. A definition that does not name `design-questions` never asks the comment source anything.
- **The planner reads the definition once per pass** and judges every item, including one already in flight, against that reading. A definition replaced while a run is in flight takes effect at the next pass, and the definition is not recorded on a dispatch.
- **Every hold reason is a named constant with a waiting rule in the view,** so a test can find every reason and class it as general or landing-only.
- **Finished states come from the definition; frozen states take no receipt.** The states `abandoned`, `deleted` and `in-progress` stay readable and are never written under any definition.
- **A repeated move appends nothing.** A write of the state an item already has answers the item and adds no event, so it does not overwrite a stored receipt.

### Add completion through the definition's hook for items that never land

An item of a store that does not land is finished when it has been judged, not when a commit lands. A definition whose `completes` value is `hook` finishes its items through the `complete-item` hook: once a run ends, the hook records the verdicts and names the finished state, and the drain moves the item there with the reason, pointer and actor.

- **The verdict check belongs to the move, not to the drain.** `tillr item apply` can append a criterion between a read in the drain and the state move, and the item would then finish with a criterion nobody judged. So the check runs inside the move's own write transaction: `tillr item state --require-verdicts` refuses the move as `criteria-unjudged` unless every criterion the item holds at that moment has a recorded verdict naming the hash of its stored text. The drain passes the flag on every move a `complete-item` answer causes, turns the refusal into a hold, and keeps no second check of its own.
- **The flag is opt-in.** Without it a move behaves as before, so the loop's move to `landed` and the owner's hand moves are untouched. Making the rule part of a definition, so that no move into a finished state can skip it, is not decided.
- **Recovery after the move never enters Git.** Under a definition that completes on `hook`, recovery of an ended run reads the item. If the item is already in a finished state, the move happened: the drain writes the end-of-run records that are missing and does not call the hook again. Otherwise it runs the completion step again from the hook call.
- **Calling the hook twice is safe.** A verdict recorded twice is two events for the same answer, and a move to the state an item already has appends nothing.
- **The Git-only checks made when a run is claimed are skipped under `hook`,** and run as before under `git-landing`.

---

## 3. The next tranche, in prose

None of these units is filed. Each builds on the definition file, the hook contract or the worked example, and each rests on choices recorded in [the direction document's ledger](tillr-core-and-git-landing.md#decision-ledger): where the line between core and definition falls is the owner's ruling ([`OQ-CG2`](tillr-core-and-git-landing.md#OQ-CG2)), and the shape of the one file and four other choices (`D-CG1` to `D-CG5` there) are the designer's. Unit 4 also rests on the designer's amendment to `D-IG4` in [item graphs](../reference/tillr-item-graphs.md#why-its-this-way). The request ids are those of [the separable-parts table](tillr-separable-parts.md#7-the-second-round-tillrs-answer).

1. **Count outcomes and draw the views from the definition** (R2-8). The status face and the observatory count items by the definition's finished states, and the landings and trunk pages appear only for a definition that lands.
2. **Add a detail panel from the `detail` hook** (R2-7). The item page asks the hook program for a record drawn from the item's `body_ref`. The hook's contract is written and checked at start; nothing calls it.
3. **Add graph change as a definition's policy, inside the four core rules** (R2-9). The core keeps one writer per change with its event, no node replacing itself, nothing started changing, and the downstream-shape check. A definition may allow a running node to insert one node directly downstream of itself with the same output shape, and may allow appending nodes to a finished graph, which the next scheduling pass starts.
4. **Replace the fixed graph caps with a size shown before dispatch** (R2-10). The concern behind `D-IG4` is runaway expansion and the spend that follows it. The guard is the refusal of cycles among workflow files, which makes expansion finite, plus the expanded size shown before the first node runs, plus a budget a definition may set that leads to a named next step for the owner, never a silent refusal. The workflow check reports graph size and refuses no graph for its depth or node count ([workflow_check.go](../../tillr-drain/workflow/workflow_check.go)); [design-from-scouts@v1.json](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/workflows/design-from-scouts@v1.json) bounds scout prompts at `most: 8`; a size budget supplied by a definition is not built.
5. **Add an uncertain outward effect as a state that blocks a retry until reconciled** (R2-12).
6. **Add time and tokens per step attempt, with their source** (R2-13). A command step's record says `none` for tokens, not zero.
7. **Add an owner acknowledgment point** (R2-14): a recorded "seen" per item.
8. **Verify the waiting class of a blocked item on both faces.** `tillr view waiting` and the served answer both assemble rows through `assembleWaiting` in [waiting.go](../../tillr-view/waiting.go) and the reason table in [holdrules.go](../../tillr-drain/holdrules/holdrules.go); a test that a blocked item reads the same on both is what remains.
9. **Test that a run started from the record sink's own request links to it,** and fix it if the next pass holds it `run-link-missing`.
10. **Add the `after-completion` hook and the retention rule.** Roll and push become the `git-landing` definition's `after-completion` hook, and `retention` gains its first rule beyond `keep`. The hook's contract is written and checked at start; nothing calls it.
