---
title: "Requests on tillr from a project that uses it without Git landing"
status: accepted
stage: CURRENT
next: "Answer the requests in Section 5 and the questions in Section 6 in tillr-separable-parts.md; Section 5's three high-priority requests come first."
tags: [tillr, design, requests, charter]
summary: "What a consuming project needs from tillr's core when its items finish on a recorded verdict and never make a commit: its preferred shapes, the owner's ruling that graph change and reopening are a charter's policy, and two lists of requests with what the tree does for each today."
---

# Requests on tillr from a project that uses it without Git landing

> **Delivered copy.** This is `docs/design/tillr-product-qa-requests.md` from this repository at commit `0275c51d52` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-product-qa-requests.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

> **In short.** A project can run a queue on tillr's core with a charter of its own, finish items
> on a hook's recorded verdict, and never touch Git. Two gaps stop such a project from keeping
> items of two kinds in one store: a charter has one lifecycle for every item, and a move between
> unfinished states stores no reason and no actor.

**Status.** Each request below carries what the tree does today, checked against the code named
in its row. tillr's answers, and the contract it offers a consumer, are in
[tillr-separable-parts.md](tillr-separable-parts.md). The requests in
[Section 5](#5-further-requests) are not answered there.

**Reads with:** [tillr-core-and-git-landing.md](tillr-core-and-git-landing.md) (the split
between the core and a charter, and its extension points),
[tillr-definitions-plan.md](tillr-definitions-plan.md) (the build plan for the first list),
[tillr-vocabulary.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md) (the word "charter").

## 1. The use these requests come from

The **consuming project** *(coined here)* is any project that calls tillr's programs to keep a
queue of its own. It is not tillr's own queue, which lands commits on a trunk. The one these
requests come from keeps two kinds of item:

- A **finding**: one failure reported by an automated test run. It ends with a verdict.
- A **bug**: one defect, which one or more findings point at. A person approves it and reports
  it.

Neither kind ever makes a commit. Its steps are commands (replay a failure, judge it again) and
read-only agent runs, in a plain directory.

A **charter** is the data file of item states and allowed moves, with one hook program beside it,
that turns the core into one use. The code and its flags call it a definition
(`tillr store init --definition`). A **finished state** is a state the charter lists as final.

## 2. Reactions

**The shapes the consuming project wants**, each of which is what the core has:

- Vocabulary as a data file plus one hook program.
- A generic lifecycle in the core, with each charter naming its finished states.
- Processes and records as the only interface. It does not ask for a Go library.

**The extension points, one case each:**

| Extension point | What the consuming project fills it with |
| :--- | :--- |
| Intake | Its own importer calls `tillr item apply`, one item per finding, with an opaque `body_ref` |
| What a node is | A command, a read-only agent run, or a person |
| Where a node runs | A plain directory: no worktree, no Git |
| What completes an item | A recorded verdict, answered by the `complete-item` hook |
| Lifecycle | Finished states of its own, and leaving one only by a recorded move |
| Prerequisites | An item with an open question for the owner is held |
| Outcome vocabulary | Its verdicts and closure reasons, each countable |
| The judge | An actor other than the one whose work is judged |
| The views' vocabulary | The item's evidence on its page, resolved from `body_ref` |

**What it withdrew:** a Go library, deadlines inside tillr's plan, a second loop in one root, a
running step that rewrites its graph freely, and an owner acknowledgment point (it keeps its own
cursor).

## 3. Graph change

**Owner ruling, 2026-11-17.** Whether an item may be reopened, and how a graph may change while
it runs, are each charter's policy. The core must not preclude them. A charter declares its valid
state moves, including whether, and from which finished states, an item may leave.

Two changes the core must allow a charter to permit:

- A running step inserts a node directly downstream of itself, with the same output shape and
  its data passed through. A step never replaces itself.
- Nodes are appended to a graph whose every node has finished. The next scheduling pass
  dispatches them.

What the core keeps whatever a charter says: one writer applies each change atomically with its
event, nothing that has started changes or becomes unstarted, and every change passes the check
that the outputs promised to the rest of the graph are still promised.

The consuming project needs tracked workflows only, which it can name in advance. It needs child
items from a step, branching on a typed outcome, and reopening as appended steps fed the earlier
result.

## 4. Requests for this round

The first list. "Today" is what the tree does.

| Request | Case | Today |
| :--- | :--- | :--- |
| R2-1. Every table created at `store init` | A reader runs before any drain has | Built |
| R2-2. A store declared not to land | A finding never makes a commit | Built: the store records its charter, and a store whose charter is not `git-landing` skips Git, item sync and landing. [The worked example](../../tillr/examples/recorded-verdict/) runs it |
| R2-3. Finished states keep a reason, an actor and a pointer, and leaving one is an event | A repeat points at its original; new evidence reopens it | Built: leaving a finished state without a reason is refused as `transition-reason-missing`, and a pointer to an unknown item as `pointer-unknown` |
| R2-4. Raw writes into and out of finished states closed | Nothing rewrites what already ran | Built for moves out (`state-terminal`, `transition-not-allowed`). Whether a move into a finished state with an open criterion is refused was not re-checked |
| R2-5. Command steps in workflows, with the item id passed | Replay and re-judge are commands | Built: a stage's kind is `agent`, `command` or `workflow`, and a run's environment carries `TILLR_ITEM_ID` |
| R2-6. Runs, verdicts, decisions and state moves on the item page | One person's audit of one item | Built, except that a move between unfinished states shows no actor or reason ([R3-2](#5-further-requests)) |
| R2-7. A detail panel the charter supplies, resolved from `body_ref` | The item carries one opaque reference; the detail lives in the consumer's store | Partly: the `detail` hook is in the contract, and no page calls it (`rg -n '"detail"' tools/tillr-view tools/tillr-drain -g '*.go'` finds only unrelated fields) |
| R2-8. Outcomes per kind, countable | How many findings a run left inconclusive | Partly: `tillr item list --state` counts each state; the item page shows a charter's finished state as not started ([R3-3](#5-further-requests)) |
| R2-9. A core that does not preclude a charter's graph changes | [Section 3](#3-graph-change) | Not built as a charter's policy |
| R2-10. No fixed size limit on a graph | A run whose every finding needs a chain of steps | Built: `tillr drain workflow check` reports a graph's size and does not refuse it. A list argument must still declare a bound (`workflow-argument-most`) |
| R2-11. No clock whose only consequence is a kill | An agent works until it is done; a stall detector decides liveness | Open: a lane's lease still expires at the run's timeout plus a fixed grace (`LeaseGraceMS` in `tools/tillr-run/run/lease.go`). A run with timeout 0 has no expiry |
| R2-12. An uncertain outward effect as a state that blocks a retry | A report whose send lost its session | Not built. An item `blocked` with waiter `owner` and a reason is the available substitute |
| R2-13. Time and tokens per step attempt, with their source | The audit of one item | Partly: a command run's record says `spend_status: not-applicable` ([R3-5](#5-further-requests)) |
| R2-14. An owner acknowledgment point | "New since you last looked" | Withdrawn |

## 5. Further requests

Raised after running a charter of the consuming project's own on the built core. Priority assumes
a person works the queue by hand, so a request that serves only unattended operation is low.

| Request | Priority | Case | Today |
| :--- | :--- | :--- | :--- |
| R3-1. Two item kinds in one charter: per-kind states and moves, or a kind field the moves key on | High | Findings and bugs in one store, joined by edges. With one lifecycle a finding can move into a bug's state; with two stores a bug cannot point at its finding | Not built: the charter reader refuses any key it does not know, and an item's `kind` field is refused as `unknown-field` |
| R3-2. A reason and an actor stored on a move between unfinished states that the charter marks as a decision | High | A person's decisions to report, approve or hold a bug are what an audit reads | Not built: `--reason` or `--by` on such a move is refused as `flag-not-stored` (`tools/tillr-item/item/finished_receipt.go`) |
| R3-3. A charter's finished states shown as finished | Medium | How many findings are confirmed | Not built: `ProjectItemState` in `tools/tillr-view/serveread.go` knows only the landing charter's states, so any other finished state reads as `not_started` |
| R3-4. `item show` drops the finished-state reason and pointer once the item leaves that state | Medium | A fixed bug that recurs | Not re-checked |
| R3-5. Not-applicable spend kept apart from unreported spend | Medium | A spend audit of a run made only of commands | Not built: `SpendTotals.add` in `tools/tillr-drain/drain/spend.go` counts every run with no usage as unreported |
| R3-6. `tillr drain workflow check <file>` finds the command registry when given one file | Medium | Checking workflow files before a run | Not re-checked |
| R3-7. `tillr view waiting` runs when the planner and the server come from the same launcher build | High | "What needs me" | Built: `checkPlannerBuild` in `tools/tillr-view/siblings.go` accepts two programs whose launcher digests agree, pinned by the case "launcher digests agree" |
| R3-8. A hook's refusal told apart from its crash | Medium | A hook is a program that can fail, and many runtimes exit 1 on an uncaught error | Open: `complete-item` exit 1 means "leave the item unfinished", and only other exits are a hook fault ([the hook table](../../tillr/definition/README.md)) |
| R3-9. No agent backend policy required for a graph of command nodes only | Low | A loop that runs no agent | Not re-checked |
| R3-10. An item's open questions shown by `item show` and on the item page | Low | A finding held on a question for the owner | Not re-checked |

One correction to a document stands: [the definition package's README](../../tillr/definition/README.md)
says the completion hooks are validated but not yet called, and its own hook table describes the
loop calling `complete-item`.

## 6. Questions the requests leave for tillr

None of these is ruled, and none is filed as an open question in a design document yet.

1. **Two kinds.** For R3-1, per-kind lifecycles in one charter, or two stores with a pointer that
   crosses them?
2. **A person's moves.** For R3-2, do the reason and actor go on the move, or does a decision
   record name the move it authorizes?
3. **A declared bound on a list argument.** Is `workflow-argument-most` a size cap of the kind
   R2-10 refuses? The consuming project accepts a declared bound only if exceeding it leads to a
   named next step and not a refusal.
4. **Hook exit 1.** Is a refusal meant to be an exit code at all (R3-8)?
5. **`producer.system`.** `tillr item apply` refuses a record whose producer system is not
   `tillr`, so a consumer's records claim tillr wrote them. Is `producer.utility` where a consumer
   names itself?
6. **A person as a step.** A stage of a person's own kind is refused (`workflow-kind`), so a
   person's decision is a `blocked` hold with waiter `owner` followed by a state move. Is a node
   whose result is that person's recorded answer wanted?
