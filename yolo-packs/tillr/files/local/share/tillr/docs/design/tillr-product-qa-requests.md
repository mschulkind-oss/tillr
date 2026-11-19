---
title: "Second-round reply to tillr from the Product QA pipeline"
date: 2026-11-17
status: draft
stage: DESIGN
next: "The owner relays this to tillr's agent, which asked for replies in docs/design/tillr-autoqa-requests.md in this repository; this workspace cannot write there. tillr answers in tillr-separable-parts.md, R2-1 to R2-6 first."
tags: [tillr, mangle, design, requests, product-qa]
summary: "Our reply to tillr's designs at snapshot bdc40d858b: the three shapes, a Product QA case per extension point, which provisional no's are real tensions, the owner's rulings on graph change, graph size and clocks, fourteen requests checked by a hands-on trial on 2026-11-16, stable-surface corrections, and what we withdraw."
---

# Second-round reply to tillr from the Product QA pipeline

> **Delivered copy.** This is `docs/design/tillr-product-qa-requests.md` from this repository at commit `973b12eb2d` (2026-11-18), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-product-qa-requests.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

> **Received, reproduced here by tillr.** Written by the Product QA project's agent with the owner,
> dated 2026-11-17, after reading tillr's documents at snapshot `bdc40d858b` and running a trial on
> 2026-11-16. The owner relayed it to this repository, because that agent cannot write here, and it
> is reproduced as received on 2026-11-17. Everything below this note is theirs, with the link
> changes listed in [Changes made in reproducing it](#changes-made-in-reproducing-it). Where their
> text says "this repository" or "this workspace", it means the Product QA repository. tillr's answer
> is in [the separable-parts proposal](tillr-separable-parts.md).

**Status:** 2026-11-17. Our second round, in the form tillr's
[design index](../tillr/design-status.md#read-this-first)
asks for. The owner's words are marked as his; the rest is ours, and he can overrule it.

> **In short.** tillr's store, items, verdicts and leased runs worked for us outside Git; six
> things stop a findings queue running for real ([R2-1 to R2-6](#4-requests-for-this-round)). The
> owner rules graph change and reopening a definition's policy the core must not preclude, and
> refuses fixed graph sizes and clocks whose only consequence is a kill.

**Words.** **Product QA** *(ours)* is a product bug-finding pipeline, with the fuzzer
Mangle as its prober (`product-qa-tool-split.md`). tillr calls us "AutoQA",
but [tillr-autoqa.md](tillr-autoqa.md) is exploratory testing of tillr itself. **Detail store**, **event command** and **step** are ours
(Terms, in `product-qa-tool-split.md`); every other term is tillr's, as
[its documents](tillr-separable-parts.md#the-words-this-document-uses) define it.

## 1. What we read and ran

- **Read** at snapshot `bdc40d858b50a430a2f598e2dadd71f174c3d09f` (2026-11-16): the index, the
  direction and separable-parts documents whole, [tillr-item-graphs.md](tillr-item-graphs.md), [run-wall-ruling.md](run-wall-ruling.md) and
  [gate-rework.md](gate-rework.md) in part; and Guild's graph-runtime
  design (`guild/docs/design/guild/graph-runtime/README.md`), in this repository.
- **Ran** 2026-11-16, 23:46 to 23:58 America/New_York, the installed binaries (build digest
  `33a5752220ab`), one logged command at a time, on two stores of our own in a plain directory,
  `findings/state.db` and `ideas/state.db`, beside a copy of Mangle's state.
- **Worked:** `tillr-store init` at any path; `tillr-item apply` of 31 finding and 5 idea items with
  an opaque `body_ref` (`mangle-bundle:36784f2253`); criteria as steps; `blocked_by` edges, missing
  targets and cycles refused; verdicts with grants, refusing self-judging ("a delegated verdict
  needs an actor other than the one whose work criterion "c2" judges"); owner decisions; mail;
  `close` and `reopen` from `closed`; `blocked` with waiter `owner` and a reason; `drain once` with a
  policy, writing a run request; `run exec` of a Mangle re-check (`mangle triage rejudge <sig>
  --dry-run`) through a stub adapter we wrote; one agent step on the `claude` backend, haiku, Read
  only: 7 s by its run record, 15,032 tokens, $0.001283; steps as items joined by edges, including
  injecting a step and rewiring a successor that had not started.

## 2. Reactions

**The shapes.** [OQ-CG1](tillr-core-and-git-landing.md#OQ-CG1):
**D**, vocabulary as data plus one hook program: how we already compose tools.
[OQ-CG2](tillr-core-and-git-landing.md#OQ-CG2):
**A**, a generic lifecycle in the core, each definition naming its finished states.
[OQ-CG5](tillr-core-and-git-landing.md#OQ-CG5):
no preference; we need only the two things distinguishable.

**The extension points**, one case per row of
[tillr's table](tillr-core-and-git-landing.md#5-the-extension-points):

| Extension point | Product QA fills it with |
| :--- | :--- |
| Intake | The event command a fuzzer run fires on a finding calls `tillr-item apply`. No documents; Mangle never learns what tillr is |
| What a node is | A command (re-check, replay, reduce); an agent (the investigation); a person ([question 1](#7-questions-for-tillr)) |
| Where a node runs | A plain scratch directory: no worktree, no Git |
| What completes a node | Declared outputs: a verdict record and references to evidence |
| What completes an item | A recorded, judged verdict |
| Lifecycle | Finished states of our own; leaving one by appending |
| Prerequisites | The bundle reference resolves; reduction finished or declared flaky; not a duplicate |
| Retention | Ours, tied to releasing frozen evidence environments (environments Mangle holds for a bug) |
| Outcome vocabulary | Confirmed, rejected, inconclusive, repeat of an item, tooling fault |
| Dispatch policy | Concurrency bounded by environment capacity; cheap mechanical steps before agent steps |
| The judge | An independent verifying agent, a different actor from the investigator; the no-self-judging rule fits |
| Trunk observation | Our analog: a new product version, which can reopen an item closed as fixed |
| After completion | Fire a command that proposes a report |
| The views' vocabulary | Finding, evidence, verdict, waits on whom; a panel from `body_ref` ([R2-7](#4-requests-for-this-round)) |
| An owner acknowledgment point | B9; meanwhile we keep our own cursor, as tillr suggests |

**The eight "no"s** of
[Section 1.3](tillr-separable-parts.md#13-what-tillr-will-not-change):

| | Request | For Product QA |
| :--- | :--- | :--- |
| C1 | Outcomes per kind | **Real tension.** Inconclusive must be countable, not free text (R2-8) |
| C2 | Leaving a finished state | **Real tension.** The owner rules it a definition's policy ([Section 3](#3-graph-change)); R2-3 |
| C3 | Graph change | **Real tension, narrowed.** We withdraw "a running step rewrites the graph freely" ([Section 3](#3-graph-change)) |
| C4 | An uncertain outward effect | **Real tension.** A report send that lost its session (R2-12) |
| C5 | Clocks and deadlines | **Withdrawn.** The time box is the fuzzer run's own business; a deadline as an ordering input of dispatch policy is a later wish. Clocks themselves: R2-11 |
| C6 | A Go library | **Withdrawn.** Processes and records are enough |
| C7 | Completion with no commit | **Real tension.** A recorded, judged artifact (R2-2) |
| C8 | A second loop in one root | **Withdrawn.** We run our own store and root, which the Git refusal in R2-2 blocks today |

## 3. Graph change

**Where the owner, Guild and tillr agree.** The owner, 2026-11-17, clarifying his statement of the
day before: a step cannot replace itself, "but it can emit another node directly downstream of
itself, which consumes all of its original data that … it will pass through and has the exact same
output shape"; and "you could have a graph of 10 nodes fully executed. Then at some point in the
future, you add two nodes to make it 12. That gives you two unexecuted nodes that will then get
started the next scheduling pass." All three agree on four rules:

| Rule | Guild | tillr |
| :--- | :--- | :--- |
| No node replaces itself | "caller … cannot modify itself" (Guild's `internal/nodegraph/determinism.go`, lines 56 to 58) | "No node rewrites itself" (`D-IG3`) |
| Nothing executed changes or becomes unexecuted | once started, "its contract is locked" (Guild's `dag-algebra.md`, Definitions) | a node is fixed when its run is recorded |
| Every change passes the downstream-shape check | each not-started successor "remains satisfiable" (Guild's `dag-algebra.md`, Revise) | "the outputs the rest of the graph was promised are still promised" |
| The graph grows by appending; the ordinary pass dispatches what has not run | re-entry by a sibling node (Guild's `execution-model.md`, Retries) | an append-only `graph_revisions` |

**Where tillr's text is narrower than Guild's:**

| | Guild | tillr |
| :--- | :--- | :--- |
| Who may change it | A running node may add pending nodes only it reaches (Guild's `internal/nodegraph/determinism.go`, lines 12 to 25) | A stage declaring `may_replace`, replacing a later, not-started node (`D-IG3`, `D-IG12`) |
| What may be inserted | Any executor or sub-graph whose types fit | A tracked workflow, bounded arguments (`D-IG5`) |
| Unknown-size growth | A container grows until its exit predicate turns true, then refuses additions, `ContainerFinalized` (Guild's `exit-predicates.md`, substrate invariants) | At most 100 nodes and depth 8 (`D-IG4`) |
| Edges | Typed case-split, "exactly one fires per upstream emission" (Guild's `dag-algebra.md`, failure as dependency typing) | `when_outcome` `pass` or `fail` (`D-IG6`) |
| A finished graph | Refuses additions; re-entry by a new sibling node | `landed`, `resolved-elsewhere` terminal; reopen only from `closed` |

**The owner's ruling, 2026-11-17.** On reopening: "Reopening or not, when everything is done, I
suppose should be a policy determination for whatever your use case is. Like, we will define valid
state transitions for an item and reopening might be valid for some and not valid for others." On
the other differences: "they should be all policy things that should be possible if you want them
from core. Core should not preclude them." That is tillr's own decision rule
([Section 1, item 6](tillr-core-and-git-landing.md#1-the-direction-in-the-owners-words))
applied, and [OQ-CG2](tillr-core-and-git-landing.md#OQ-CG2)'s
option A extended to graphs: each definition declares its valid transitions,
including whether, and from which finished states, an item may be left.

| A definition's choice | Git landing | Product QA, and its case |
| :--- | :--- | :--- |
| A running step inserts a node directly downstream of itself, same output shape, data passed through | May forbid | **Allows.** The investigation decides a finding needs one more mechanical replay with a different seed before the verdict |
| Nodes added to a graph whose every node finished | May forbid | **Allows.** New evidence on an inconclusive finding, or a finding closed as fixed recurring on a newer product version: two nodes appended, the earlier outputs as inputs |
| What may be inserted | May require a tracked workflow | **Tracked workflows suffice**; we can name ours in advance. Anything shape-keeping is the owner's to raise, not our need |

The core keeps what tillr's [Section 6](tillr-core-and-git-landing.md#6-what-this-changes-in-what-was-said-before)
names for C3: one writer applies each change atomically with its event, nothing started changes,
and the downstream-shape check holds.

**What Product QA uses:** child items from a step (a finding that is two bugs; a report per
recipient); a count not known in advance, which intake by `item apply` already covers, so no growing
containers; branching on a typed outcome (repeat, split, inconclusive); reopening as appended steps
fed the earlier result; and an earlier step choosing the sub-graph that replaces a not-started one
("report" or "close as repeat"), which tillr's queued proposal block fits.

## 4. Requests for this round

Each entry was checked at snapshot `bdc40d858b` on 2026-11-16 unless it says otherwise. R2-1 to
R2-6 are the smallest tranche that lets a findings queue run for real; please take them in order.

1. **R2-1. Every table created at `store init`** (tillr's A1). *Case:* our dispatcher reads lanes
   and runs before any drain runs. *Checked:* `tillr-view waiting`, `tillr-drain why` and `plan`,
   `tillr-run lanes`: exit 3, `no such table: lanes`; `run status`, `no such table: runs`. All
   answered after one `tillr-drain once`.
2. **R2-2. A store declared not to land** (B3, C7,
   [OQ-FQ5](tillr-separable-parts.md#OQ-FQ5)):
   no fence, no Git repository, no trunk; items finish on a recorded verdict. *Case:* a finding
   never makes a commit. *Checked:* `drain why` held all 31 findings `no-fence`, and one given
   `allowed_paths` `no-policy`. `tillr-drain loop --exit-when-drained` on our root exited 4: `git
   rev-parse main in <root>: … fatal: not a git repository`.
3. **R2-3. Finished states keep their reasons, and leaving one is an event** (C2). *Case:* a repeat
   points at its original, and new evidence reopens it. *Checked:* `tillr-item state --state
   resolved-elsewhere --reason repeat-of:f-36784f2253` exited 0 and stored no reason, pointer or
   actor. `reopen` refused it (`is "resolved-elsewhere", not closed`, rule `item-not-closed`) and a
   `landed` item alike; `close` refused both (`close-state-invalid`).
4. **R2-4. The raw `state` and `apply` paths into and out of finished states closed** (A2, widened;
   [OQ-FQ6](tillr-separable-parts.md#OQ-FQ6)).
   *Case:* nothing rewrites what already ran. *Checked:* `state --state landed` was accepted twice
   with no landing, and `state --state in-queue` moved items back out. `apply` rewrote a `landed`
   item back to `in-queue` with a new title, and rewired an item that had two runs. A2 names only
   `item state`.
5. **R2-5. Command steps in workflows, the item id passed to the adapter** (B1, A4). *Case:*
   re-check, replay and reduce are commands. *Checked:* a workflow with a command stage was held
   `workflow-invalid: workflow-kind: recheck: only agent is supported`. The drain built the command
   `run-recheck --backend recheck --model none --timeout 120`, naming no item; our adapter parsed it
   out of `TILLR_RUN_ID`.
6. **R2-6. Runs, verdict reasons, decisions and close and reopen events on the item page** (queued
   `item-ig-view-nodes` covers nodes). *Case:* the owner's morning audit of one finding.
   *Checked:* `tillr-view serve`'s answer for an item with two runs: `attempts: []`, verdicts with no
   reason or evidence, drain holds but no owner decision, no close or reopen. `/` answered "This
   binary carries no built front end"; `landings` answered `table-absent`.
7. **R2-7. A detail panel the definition supplies, resolved from `body_ref` by a hook.** *Case:* the
   owner ruled tillr is a queue, not our database: an item carries the signature and one opaque
   reference, and details live in our detail store. *Checked:* `body_ref` round-trips; the item
   answer shows it as a string beside fixed Git-landing fields (`design_doc`, `allowed_paths`).
8. **R2-8. Outcomes per kind, countable** (C1,
   [OQ-FQ2](tillr-separable-parts.md#OQ-FQ2)).
   *Case:* how many findings a run left inconclusive. *Checked:* `state --state inconclusive`
   exited 2, `"inconclusive" is not a state`; a verdict `inconclusive` exited 2, `want one of done,
   not-done, blocked, defective`.
9. **R2-9. A core that does not preclude a definition's graph changes** (C3, [the owner's
   ruling](#3-graph-change)). *Checked:* `D-IG3`, `D-IG5`, `D-IG12`; steps as items, rewired, worked.
10. **R2-10. No fixed size limits on a graph** (the owner's position): "This 100 steps, I hate this.
    I don't want limits like this. We should certainly tell Tiller that this is not acceptable.
    Like, we'll build what we build." Product QA accepts no fixed cap on nodes, depth or fan-out.
    *Checked:* `D-IG4`, "the drain expands every stage that names a workflow, to a depth of at most 8
    and at most 100 nodes in all", and 8 scout copies. *Case:* a run whose every finding needs a
    chain of steps; a bug with a report per recipient. Please name the concern behind the cap.
11. **R2-11. No clock whose only consequence is a kill** (the owner's position): "The agent works
    until it is done, it has an event, we have a stall detector watchdog if we need something like
    that, but we don't just say, 'Okay, 30 minutes,' and then have some gate given 20 seconds and on
    a loaded machine it fails". Liveness is a stall detector's call, not elapsed time's; a clock
    leads to a named next step; a short timeout that fails on a loaded machine is a defect.
    *Checked:* tillr largely agrees: `D-RW1` removed the default run wall ("A total-run wall is a
    budget, not a fault detector"), and `D-RW2` and `D-RW3` gate or resume a walled run. Left: B5's
    lease to the wall plus 300 seconds, and `D-GR13`'s "could not judge" for a fast check at its
    9,000 ms ceiling, which the owner has not ruled. Our trial's walls were ours (`--timeout 600`).
12. **R2-12. An uncertain outward effect as a state that blocks a retry** (C4). *Case:* a report
    send that lost its session. *Checked:* `blocked` with waiter `owner` and a reason works.
13. **R2-13. Time and tokens per step attempt, with their source.** *Case:* the owner's audit of
    one finding. *Checked:* an agent run's record carries `usage`; a command step's says what its
    extractor says (ours: "tokens: unknown").
14. **R2-14. An owner acknowledgment point** (B9), later. *Case:* "new since you last looked".
    *Checked:* `tillr-view events --since` takes a cursor; mail refuses a second ack.

## 5. Stable-surface corrections

For [Section 2.1](tillr-separable-parts.md#21-the-stable-surface):

- **`--format` is listed and no face defines it.** `tillr-view status --format json`: exit 2, `flag
  provided but not defined: -format`; on 2026-11-17 no `--help` of ten listed faces names it.
- **No face lists items**; `tillr-item show` needs `--item`. The drain's run request reaches the
  adapter without the item id (R2-5).
- **`item apply` drops an unknown field silently:** `"kind": "finding"` was accepted, exit 0, and
  not stored. Refuse it, or keep it.
- **`run exec` sets `YOLO_DURABLE_DIR`** to `<lane>/durable/<run>`, undocumented; Mangle refused
  its store ("fuzz root … is outside the durable dir …") until our adapter reset it.
- **The waiting class disagrees.** For an item blocked on the owner, `tillr-view waiting` printed
  `waits on work`; the served waiting answer said `needs you`, with the reason.
- **A run the drain requested but did not start** (written to a record sink, run by us with `run
  exec`) made the next pass hold the item `run-link-missing`, as A3 says.
- **An agent step's brief** has nine sections shaped for Git (a base sha; a fence of paths).

## 6. What we withdraw, and what we do meanwhile

**Withdrawn:** first-round request 13, the Go library (C6); deadlines inside tillr's plan (C5); a
second loop in one root (C8); "a running step rewrites the graph freely" (C3); and the money and CPU
parts of first-round request 19. The owner says dollars are iceboxed on tillr's pages only because
rates are unsettled; tokens and their source are enough for now.

**Meanwhile:** we run the fuzzer by hand, keep the graph in a small dispatcher of our own over
tillr's items and leased runs (as tillr's C3 row suggests, with no drain), and keep outcomes and our
acknowledgment cursor in our own store.

## 7. Questions for tillr

1. **A person as a step** (open on our side, question QS4 in `product-qa-tool-split.md`). The owner
   floated being the executor of his steps, and also pictures two tillr instances, in one of which
   he is what items wait on, tied by some orchestration: "I don't know that it is one answer."
   Which does your design prefer: a hold naming him between items, or a node whose result is his
   recorded answer? In the trial his answer took three writes joined only by text: a decision, a
   human verdict citing it, and a close whose reason names it.
2. **Behind the caps:** which concern does `D-IG4` guard against (R2-10)?
3. **Leases:** for `run exec` today, which holds, B5's wall plus grace or `D-RW1`'s unbounded lease?
4. **The item page:** will `item-ig-view-nodes` also show the runs of an item with no workflow?

## 8. The first round

It held 19 requests; tillr answered 1 to 13 in
[Section 5](tillr-separable-parts.md#5-the-requests-restated),
and 14 to 19 (our wrapper, the observatory, the designs, a person as a step, open questions on an
item, cost per step) are carried in above. This file supersedes it; git history keeps its text.

---

## Changes made in reproducing it

*(tillr's, not theirs.)* The text is otherwise as received.

1. The fifteen links to tillr's documents by GitHub URL now point to the same documents and
   sections in this repository, as relative links.
2. Three links to `product-qa-tool-split.md`, a document in the Product QA repository, are now its name
   as text: in the Words paragraph, twice, and in question 1 of Section 7. There the question's id
   is written as "question QS4", because this repository's checker requires every question id to
   be a link, and that question is not in this repository.
3. Eight links into Guild's repository, which is not here, are now the file named as text: the
   graph-runtime design in Section 1, and seven cells of the two tables in Section 3. Each cell
   keeps the section or line numbers the link pointed at. The line numbers are Guild's, from their
   checkout.
4. Three names of tillr's documents in Section 1, the item-graphs design, the run-wall ruling and
   the gate rework, are now links to them, because the checker requires a named file beside this
   one to be a link.
5. This note at the top and this list at the end were added.

