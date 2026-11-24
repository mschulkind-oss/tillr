---
title: "tillr designs: what is built, what is designed, and what is still open"
status: accepted
stage: CURRENT
tags: [tillr, index, status, design]
summary: "The one page to read first about tillr: every design and reference document, what each covers, and whether it is built, partly built, designed, open or parked. It also declares which documents the tillr pack delivers to other workspaces."
---

# tillr designs: what is built, what is designed, and what is still open

> **Delivered copy.** This is `docs/tillr/design-status.md` from this repository at commit `4f503801bb` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/design-status.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

One row per document under `docs/design`, `docs/reference` and `docs/tillr`. Each status is the
one the document states for itself, in its frontmatter and its status paragraph, and the document
names the code or the command behind it. The state of a queued item is the store's to report:
`tillr drain status` and `tillr view` answer for the present moment.

## Read this first

**What tillr is.** tillr is work-queue tooling: command-line programs over one SQLite database,
the **store**. Work is tracked as **items**: an item is one piece of work under one id, with one
deliverable and one ending ([the vocabulary](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md#1-what-an-item-is)
defines it and every other word here). A loop, the **drain**, plans which item is ready, runs an
agent for it in a **lane** (a leased slot with its own git worktree), checks the result with the
**gate**, and puts one commit per item on the trunk with the **lander**. Each item has a
**fence**, the paths it may change, and **criteria**, the testable conditions its verdict is
judged against. An `OQ-` id names an open question only the owner rules on; a `D-` id names a
designer's decision the owner may overturn.

The code is machinery for landing changes in a Git repository. The direction is to separate it
into general machinery of queues and graphs,
[**the core**](../design/tillr-core-and-git-landing.md), and one **charter** per use on top of
it, the first being **the `git-landing` charter**
([OQ-VO4](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md#OQ-VO4), [OQ-VO5](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md#OQ-VO5)).
The code and its flags say "definition" for a charter.

**If you are reading this in another workspace,** you are reading the copy the tillr pack
delivered to `~/.local/share/tillr/docs/tillr/design-status.md`. It is a snapshot of one commit,
named in its banner and in `~/.local/share/tillr/docs/snapshot.json`. Every document below marked
`shipped` sits beside it at the same repository path, and the module READMEs sit at
`~/.local/share/tillr/<module>/README.md` beside the source they describe. A link to anything that
was not delivered points at the repository.

**How to read the status column.**

- **Built:** the tree does what the document says; a reference under `docs/reference` is kept true
  to the code it names.
- **Partly built:** part is in the tree. The document's status paragraph says which part, and
  names what is not.
- **Designed, not built:** no question waits on the owner, and the build has not started.
  Designer's decisions in its ledger may still be overturned.
- **Open questions:** the design waits on the owner's rulings, named by question id. Nothing in it
  is promised until those are ruled.
- **Parked:** on [the roadmap's Icebox](roadmap.md#icebox). Nothing is being built, and its
  questions are blocked until it is picked up.

**What is stable to build on.** tillr's seam for another program is processes and records: each
face (one subcommand of one program) reads and writes versioned JSON records, and its exit code is
the contract. Go packages and the store's tables are not a supported interface. Which faces and
records another program may rely on is
[Section 2, "The contract between tillr and a consumer"](../design/tillr-separable-parts.md#2-the-contract-between-tillr-and-a-consumer),
whose first part is [the stable surface](../design/tillr-separable-parts.md#21-the-stable-surface)
and whose second is
[what is internal](../design/tillr-separable-parts.md#22-what-is-internal-and-may-change-daily).

**Reading order for an agent from another project:**

1. This page, "Read this first".
2. [What tillr promises a consumer](../design/tillr-separable-parts.md):
   ["For a consumer's agent"](../design/tillr-separable-parts.md#for-a-consumers-agent), the
   contract in Section 2, and
   [Section 7](../design/tillr-separable-parts.md#7-a-consumers-requests-and-the-state-of-each),
   which maps each request a consumer made to what the tree does.
3. [The core and the `git-landing` charter](../design/tillr-core-and-git-landing.md):
   [Section 5](../design/tillr-core-and-git-landing.md#5-the-extension-points), the extension
   points, and
   [Section 8](../design/tillr-core-and-git-landing.md#8-building-it-the-owners-go-ahead-and-the-working-choices),
   what is built and the units that are not.
4. [The definition package's README](../../tillr/definition/README.md) and the worked
   example beside it, `tools/tillr/examples/recorded-verdict/`, which runs a store that lands
   nothing from an empty directory.

**How to ask for something.** Write the request in a document in your own repository, one
numbered request per entry with the case that needs it, and ask the owner to relay it
([how requests and answers travel](../design/tillr-separable-parts.md#24-how-requests-and-answers-travel)).
A request that cuts against a settled ruling gets a reason and the ruling's id.

## How the pieces fit

Fifteen directories under `tools/`. The umbrella `tillr` dispatches to the `tillr-<noun>`
programs, and each of those also runs alone with an explicit `--store`. The pack delivers eight
programs: `tillr` and the store, item, run, drain, gate, land and view programs. The
`tillr-operating` skill, delivered with the pack, says how they compose across the loop's steps.

| Piece | What it does | Status | Pack |
| :--- | :--- | :--- | :--- |
| [tillr-store](../../tillr-store/README.md) | Creates, migrates and checks the SQLite store every other program writes. | Built | shipped |
| [tillr-item](../../tillr-item/README.md) | Items, their states, criteria, verdicts and decisions; `sync` reads item blocks from tracked Markdown under `docs/`. | Built | shipped |
| [tillr-run](../../tillr-run/README.md) | Runs one agent or command in a lane under a lease and records its end cause and usage; one adapter per backend. | Built | shipped |
| [tillr-drain](../../tillr-drain/README.md) | The loop: plans, stages lanes, dispatches runs, judges candidates, lands, and says why each item waits. | Built | shipped |
| [tillr-gate](../../tillr-gate/README.md) | Runs the declared checks against a candidate, with budgets and remembered results. | Built | shipped |
| [tillr-land](../../tillr-land/README.md) | Squashes a gated candidate onto the trunk as one commit under the land lock. | Built | shipped |
| [tillr-view](../../tillr-view/README.md) | Read-only faces over the store, and `serve`, the observatory web page. | Built | shipped |
| [tillr (dispatcher)](../../tillr-cli/README.md) | `tillr <noun> <face>` forwards to the `tillr-<noun>` programs. | Built | shipped |
| [loop adapter](../../tillr-loop/README.md) | The `git-landing` charter's hook program: this repository's glue between the drain and its gate and lander. | Built | shipped |
| [tillr-trace](../../tillr-trace/README.md) | Records the commands a run starts, through a shell shim the run adapters set, and answers questions from those records. | Partly built ([session recording](../design/session-recording.md)) | shipped |
| [`tools/tillr`](../../tillr/) | The shared library: the store opener, records, exit codes, settings, the watch rules and the [definition package](../../tillr/definition/README.md). | Built | |
| [`tools/tillr-watch`](https://github.com/mschulkind-oss/tillr/blob/main/tools/tillr-watch/) | Standing alert rules over the store ([the watch design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-watch.md)). | Partly built | |
| [`tools/tillr-roll`](../../tillr-roll/) | `tillr-roll.py` builds and installs new binaries; `tillr-keep` restarts the drain loop when it exits. | Built | |
| [`tools/gate`](https://github.com/mschulkind-oss/tillr/blob/main/tools/gate/) | This repository's own check declarations and gate scripts ([the gate reference](../reference/gate.md)). | Built | |
| [`tools/observatory`](https://github.com/mschulkind-oss/tillr/blob/main/tools/observatory/) | `run.sh`, which builds and serves the observatory for this checkout. | Built | |

## References: how the built system works

Each reference describes code that is in the tree and names it in its `covers` list.

| Document | Covers | Status | Pack |
| :--- | :--- | :--- | :--- |
| [Authority, not approval](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/authority-not-approval.md) | Who may decide what: mechanical, delegated or the owner's; what the owner is asked; and that a judge is never the agent whose work is judged. | Built; each rule names its enforcement or says it has none | not shipped |
| [Gate](../reference/gate.md) | Check declarations, the fast tier before the merge and the heavy proof after it, remembered passes, the trunk fixer's gate and upstream drift. | Built | shipped |
| [Gate check rules](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/gate-check-rules.md) | A red is read once, a check asserts what was recorded, a set-aside check is named and counted, and a cited commit is reachable or marked. | Built | not shipped |
| [Item graphs](../reference/tillr-item-graphs.md) | Tracked JSON workflows expanded into finite graphs of agent and command nodes, bounded mid-run proposals, evaluator nodes and graph exits. | Built | shipped |
| [Item states](../reference/tillr-item-states.md) | The item states and frozen criteria. | Built | shipped |
| [Queue persistence](../reference/tillr-queue-persistence.md) | Item blocks in tracked Markdown and `tillr item sync`, roadmap placement, what stays private to the store, and landing receipts without commit trailers. | Built | shipped |
| [Drain successor](../reference/tillr-drain-successor.md) | The per-item successor function, derived positions, clock-free identities and the trigger-driven loop. | Built | shipped |
| [Build-commit continuation](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-build-commit.md) | How the drain continues a build run that exits with uncommitted fenced work until it commits or repeats. | Built | not shipped |
| [Run wall](../reference/run-wall-ruling.md) | No default wall-clock limit on a run; a committed candidate is judged whatever the wall says. | Built | shipped |
| [Restart](../reference/tillr-restart.md) | Restart requests, in-place replacement of the loop and the server, session pause and continuation, `.tillr.toml`, and roll and push after landings. | Built | shipped |
| [Refused push](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-refused-push.md) | What the drain does when an automatic push does not complete: the kinds of refusal, and replaying unpublished landings onto a moved remote. | Built | not shipped |
| [Store contention](../reference/tillr-store-contention.md) | The lander's write boundary and tracing who holds the store's writer. | Built | shipped |
| [Designer sessions](../reference/tillr-designer-sessions.md) | Review-comment intake, one design session per document, session selection and compaction, and reply delivery. | Built | shipped |
| [Claude backend](../reference/tillr-claude-backend.md) | The `run-claude` adapter: what a headless run loads, what a seat may use, and waiting when the subscription window is spent. | Built | shipped |
| [Backend adapters and the Muse backend](../reference/tillr-muse-backend.md) | The adapter contract, the staged brief, the report header, write-boundary snapshots and seat failover. | Built; no seat dispatches to the Muse adapter | shipped |
| [Distribution](../reference/tillr-distribution.md) | One pack, eight programs on `PATH` built from delivered source, and the documentation split. | Built | shipped |
| [Observatory truthfulness and primitives](../reference/observatory-design.md) | What a drain records for a read-only server, provenance marks, three-way states, named absences and count parity between commands and HTTP. | Built | shipped |
| [Observatory pages and read faces](../reference/observatory-redesign.md) | Linked entity pages, the read faces and routes, waiting classes, the run span waterfall, and usage without prices. | Built | shipped |
| [Observatory actions](../reference/observatory-actions.md) | Reordering the queue, saving an idea, and the HTTP write routes. | Built | shipped |

## Designs: direction, vocabulary and principles

| Document | Covers | Status | Pack |
| :--- | :--- | :--- | :--- |
| [The core and the `git-landing` charter](../design/tillr-core-and-git-landing.md) | tillr as a core of queues and graphs with one charter per use; where the Git-landing assumption is still wired in; the extension points; the unbuilt units. | Partly built: the charter file, the store that records it and a charter that lands nothing | shipped |
| [What tillr promises a consumer](../design/tillr-separable-parts.md) | The stable surface of commands and records, what is internal, and the state of each request an example consumer makes. | Partly built; [OQ-FQ7](../design/tillr-separable-parts.md#OQ-FQ7) and [OQ-FQ8](../design/tillr-separable-parts.md#OQ-FQ8) are blocked | shipped |
| [The vocabulary](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md) | One meaning for each word: item, node, run, session, worktree, and the words that are retired. | Partly built: the deny-list that replaces per-item fences and the `git-landing` name | not shipped |
| [Board utilities](../design/board-utilities.md) | The founding contract: seven programs over one store, each runnable alone, joined by processes and records. | Built | shipped |
| [Landing principles](../design/landing-principles.md) | The durable landing rulings, the measurement behind each, and what enforces it. | Built; each principle says what is not enforced | shipped |
| [The momentum principle](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/momentum-principle.md) | The pile is the only throttle: no tick budget, spend ceiling or artificial pause. | Built: a standing ruling | not shipped |
| [The graph is the path](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-graph-is-the-path.md) | Every act on an item is a node of its graph, and one pure function decides what comes next. | Partly built: the static workflow machinery | not shipped |
| [Convergence](../design/tillr-convergence.md) | Three structural causes of a queue that stops, and the target that removes them. | Partly built | shipped |
| [Unattended drain](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/unattended-drain.md) | Eight rules that let the drain run a night alone, each with a mechanism and a scenario. | Partly built | not shipped |
| [Who made the decisions](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md) | The owner's rulings on decisions agents made, the questions of that kind still open, and the rule for recording a decision. | Open questions: [OQ-PR6](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR6) to [OQ-PR11](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR11), [OQ-PR15](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR15), [OQ-PR16](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR16), [OQ-PR18](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR18), [OQ-PR19](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR19) | not shipped |

## Designs: items, intake and judging

| Document | Covers | Status | Pack |
| :--- | :--- | :--- | :--- |
| [Judge and lifecycle](../design/tillr-judge-and-lifecycle.md) | Who records a verdict, what an item's stored state means, what each ending of a run does to its item, and diagnosing a repeated failure. | Partly built; [OQ-JL5](../design/tillr-judge-and-lifecycle.md#OQ-JL5) is open | shipped |
| [Criterion repair](../design/criterion-repair.md) | How a frozen criterion that is wrong or stale is superseded. | Designed, not built | shipped |
| [Intake paths](../design/tillr-intake-paths.md) | Each way work enters runs through its own small graph that ends in one item shape. | Partly built: each door is built; the shared output contract is not | shipped |
| [Idea pipeline](../design/tillr-idea-pipeline.md) | How a sentence the owner types becomes ranked work through scouting, design and a fenced build. | Partly built: intake, the question hold and the design turn; item kinds and lineage are not | shipped |
| [Provisional design documents](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/provisional-design-docs.md) | An idea lands as a provisional design within minutes and is worked later. | Open questions: [OQ-PD1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/provisional-design-docs.md#OQ-PD1) to [OQ-PD4](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/provisional-design-docs.md#OQ-PD4) | not shipped |
| [The queue mirrors the designs](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md) | One queue item per unfinished design the roadmap routes, with build steps in a companion plan. | Designed, not built | not shipped |
| [Design to planning](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-design-to-planning-transition.md) | A design with no open question moves straight to planning, and review comments end in open questions or planning. | Open questions: [OQ-DT1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-design-to-planning-transition.md#OQ-DT1), [OQ-DT2](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-design-to-planning-transition.md#OQ-DT2) | not shipped |
| [Early design landing](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-early-design-landing.md) | A question and its design reach the trunk ahead of their item, with nothing rebased. | Designed, not built; [OQ-EL3](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-early-design-landing.md#OQ-EL3) is blocked | not shipped |
| [Request amendment](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-request-amendment.md) | A request that is hard to deliver as written is amended once by the design seat, and both texts are kept. | Open questions: [OQ-RA1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-request-amendment.md#OQ-RA1) | not shipped |
| [Item split](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-item-split.md) | An oversized item lands in parts: what is accepted lands, and what is objected to goes back alone. | Partly built: three of twelve slices | not shipped |
| [Scheduled items](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/scheduled-tasks.md) | A due time and a recurrence on the item; an item not yet due is not ready, with no daemon. | Designed, not built | not shipped |
| [The escalation ladder](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/escalation-ladder.md) | A lane that finds its plan or design wrong records the conflict and routes it to a seat with authority. | Partly built in a narrower shape; its seven questions await the owner | not shipped |
| [Operator decisions inside tillr](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/operator-decisions-inside-tillr.md) | Seven kinds of hand action by the operator, and who inside tillr takes each. | Open questions: [OQ-OD1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/operator-decisions-inside-tillr.md#OQ-OD1) to [OQ-OD3](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/operator-decisions-inside-tillr.md#OQ-OD3) | not shipped |

## Designs: review and compliance

| Document | Covers | Status | Pack |
| :--- | :--- | :--- | :--- |
| [A review before every code landing](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-diff-review.md) | An agent that did not write the change reads the diff before it lands and checks it against the item. | Designed, not built; [OQ-DR3](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-diff-review.md#OQ-DR3) is blocked | not shipped |
| [The compliance officer](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-design-document-check.md) | One cheap read-only agent checks the files an item is about to commit against the written policies registered for them. | Designed, not built | not shipped |
| [Review rules](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-review-rules.md) | Which rulebooks apply, which projects get landing review, and when plans and designs are checked. | Open questions: [OQ-RJ5](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-review-rules.md#OQ-RJ5) | not shipped |
| [Conflict answer check](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-conflict-answer-check.md) | A fresh review checks a designer's conflict answer against the tree before it lands. | Designed, not built | not shipped |
| [Owner review comments](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-review-comments.md) | A standing rule for review comments, a fast track for them, and the seam to the document viewer. | Designed, not built | not shipped |

## Designs: the drain, runs and backends

| Document | Covers | Status | Pack |
| :--- | :--- | :--- | :--- |
| [Planning node and model tiering](../design/tillr-planning-node-model-tiering.md) | A planning node cuts a build item into pieces, each piece names a seat, and a verify node checks the joined result. | Partly built; [OQ-PT7](../design/tillr-planning-node-model-tiering.md#OQ-PT7) and [OQ-PT8](../design/tillr-planning-node-model-tiering.md#OQ-PT8) are blocked | shipped |
| [Plan step and seat selection](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-plan-step-and-seat-selection.md) | Who chooses an item's seat, what the plan step is for, and how builders fan out. | Partly built; [OQ-PSS3](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-plan-step-and-seat-selection.md#OQ-PSS3) is open | not shipped |
| [Designer scouts](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-designer-scouts.md) | What a designer reads and checks itself, and what goes to cheaper read-only scouts. | Open questions: [OQ-RD1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-designer-scouts.md#OQ-RD1) to [OQ-RD3](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-designer-scouts.md#OQ-RD3) | not shipped |
| [Seat profiles](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-seat-profiles.md) | A named, complete backend policy per file, listed and switched with one command. | Open questions: [OQ-DSP1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-seat-profiles.md#OQ-DSP1) to [OQ-DSP3](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-seat-profiles.md#OQ-DSP3) | not shipped |
| [Live drain capacity](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-live-drain-config.md) | Changing lane counts, the backend policy and concurrency caps on a running drain. | Partly built: `tillr drain scale --lanes` | not shipped |
| [Scheduling and notifications](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-scheduling-and-notifications.md) | The scheduling pass only writes assignments; staging, launch, gate and landing run outside it. | Partly built | not shipped |
| [Shared path serialization](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-shared-path-serialization.md) | A second writer is held before lane allocation when its target files overlap live work. | Designed, not built | not shipped |
| [The landing call](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-landing-call.md) | A worker ends its run with one tool call, and a refusal comes back as that call's result. | Partly built; [OQ-LC1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-landing-call.md#OQ-LC1) is blocked | not shipped |
| [Lane shims](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-lane-shims.md) | Per-run command shims and run boundaries that refuse a forbidden command before it runs. | Open questions: [OQ-LS5](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-lane-shims.md#OQ-LS5) | not shipped |
| [Closed backend probe](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-closed-backend-probe.md) | The drain tests a closed backend itself, and only a failed probe escalates. | Open questions: [OQ-BP1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-closed-backend-probe.md#OQ-BP1) to [OQ-BP3](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-closed-backend-probe.md#OQ-BP3) | not shipped |
| [Fallback inventory](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-fallback-inventory.md) | Every path that runs because the intended one failed, with a ruling on each. | Open questions: [OQ-FB1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-fallback-inventory.md#OQ-FB1) to [OQ-FB8](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-fallback-inventory.md#OQ-FB8) | not shipped |
| [Gate test capacity](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/gate-test-capacity.md) | How many test processes run at once, memory pressure, and priority among tillr's own processes. | Open questions: [OQ-GTC1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/gate-test-capacity.md#OQ-GTC1) to [OQ-GTC4](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/gate-test-capacity.md#OQ-GTC4) | not shipped |
| [Evidence registry and suite guardrail](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-evidence-registry-and-suite-guardrail.md) | One registry of established results shared by the gate, the trunk proof and worker briefs, and a guardrail against whole-suite runs in a lane. | Open questions: [OQ-ER1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-evidence-registry-and-suite-guardrail.md#OQ-ER1) | not shipped |
| [Self-hosted build](../design/tillr-selfhosted-build.md) | Running tillr from the source its own trunk holds. | Partly built; [OQ-SH4](../design/tillr-selfhosted-build.md#OQ-SH4) is open | shipped |
| [Replaying recorded tasks](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/model-benchmarks-from-drain-records.md) | Replaying the drain's recorded tasks against several models and reading the results side by side. | Designed, not built; its three questions are blocked | not shipped |

## Designs: watching, recording and storage

| Document | Covers | Status | Pack |
| :--- | :--- | :--- | :--- |
| [tillr-watch](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-watch.md) | Standing rules, kept as data, that read the store and open one alert per condition. | Partly built: the engine, the command line, the linter and the rules | not shipped |
| [Alert workflow](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-watch-alert-workflow.md) | Every alert gets an investigation and one recorded outcome, and a silence binds a rule, an item and a bound. | Partly built: single-instance acknowledgment | not shipped |
| [Alert triage seat](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-alert-triage-seat.md) | Alerts go to a read-only triage seat inside tillr, and only what it cannot settle reaches the operating session. | Designed, not built | not shipped |
| [General agent watch](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/general-agent-watch.md) | The watch engine separated from the store, so any agent can wait on structured conditions. | Open questions: [OQ-GW1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/general-agent-watch.md#OQ-GW1) to [OQ-GW3](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/general-agent-watch.md#OQ-GW3) | not shipped |
| [Read-face timing](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-read-face-timing.md) | A sampler in the drain loop times the read faces so a slow one raises an alert. | Designed, not built | not shipped |
| [Session recording](../design/session-recording.md) | Record what a run was shown and what it executed, and query it later. | Partly built: the shim, the per-run record directory and the query commands | shipped |
| [Storage](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-storage.md) | A log per item written by one process, with SQL as a rebuildable index. | Designed, not built; [OQ-TS1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-storage.md#OQ-TS1) and [OQ-TS3](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-storage.md#OQ-TS3) are blocked | not shipped |
| [Declared state machines](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-state-machines.md) | One declared machine and one transition writer for each of the eight things that change state. | Open questions: [OQ-TSM1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-state-machines.md#OQ-TSM1) to [OQ-TSM4](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-state-machines.md#OQ-TSM4) | not shipped |

## Designs: utility contracts

[Utility contracts](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts.md) is the parent; each file under it
lists what one utility provides and expects, read from the code. The contract examples and their
checks are not built.

| Document | Covers | Status | Pack |
| :--- | :--- | :--- | :--- |
| [Utility contracts](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts.md) | Every utility has a contract of what it provides and expects, each promise pinned by the provider's own unit test. | Open questions: [OQ-UC1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts.md#OQ-UC1) | not shipped |
| [Contract: tillr (the library)](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts/tillr.md) | The record envelope, exit codes, event log, store opener, settings and watch rules. | Designed, not built | not shipped |
| [Contract: tillr-cli](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts/tillr-cli.md) | The one command `tillr <noun>` and its own exit codes. | Designed, not built | not shipped |
| [Contract: tillr-store](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts/tillr-store.md) | The faces that create, check, recover and inventory the store. | Designed, not built | not shipped |
| [Contract: tillr-item](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts/tillr-item.md) | Its faces, record and event kinds, tables, and the names of states, verdicts and rejection rules. | Designed, not built | not shipped |
| [Contract: tillr-run](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts/tillr-run.md) | The run record, lanes, run states and end causes, and the adapter scripts. | Designed, not built | not shipped |
| [Contract: tillr-drain](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts/tillr-drain.md) | Its faces, reader packages, the planner's answer, the hold reasons, tables and event kinds. | Designed, not built | not shipped |
| [Contract: tillr-gate](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts/tillr-gate.md) | Its faces, record kinds, exit codes and the remembered-results directory. | Designed, not built | not shipped |
| [Contract: tillr-land](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts/tillr-land.md) | Its faces, the landing record, results and refusal reasons, ref prefixes and the land lock. | Designed, not built | not shipped |
| [Contract: tillr-loop](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts/tillr-loop.md) | One script with eight faces and the `describe` answer the drain checks at start. | Designed, not built | not shipped |
| [Contract: tillr-view](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts/tillr-view.md) | Its faces, record kinds, and the HTTP API its web client reads. | Designed, not built | not shipped |
| [Contract: tillr-trace](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts/tillr-trace.md) | Its subcommands and the shell shim. | Designed, not built | not shipped |
| [Contract: tillr-roll](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts/tillr-roll.md) | The roller and the keeper, and the drain and view faces they expect. | Designed, not built | not shipped |
| [Contract: tillr-watch](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts/tillr-watch.md) | Evaluation, status, the episode faces and rule overrides, and its exit codes. | Designed, not built | not shipped |

## Parked designs and the roadmap

| Document | Covers | Status | Pack |
| :--- | :--- | :--- | :--- |
| [Goal monitor](../design/tillr-goal-monitor.md) | A goal as a recurring item whose criteria are a property of the system, checked on a schedule. | Parked; [OQ-GM1](../design/tillr-goal-monitor.md#OQ-GM1) and [OQ-GM2](../design/tillr-goal-monitor.md#OQ-GM2) are blocked | shipped |
| [AutoQA](../design/tillr-autoqa.md) | Exploratory testing of tillr with its own store, cheap probes and a judge that confirms bugs. | Parked; [OQ-AQ2](../design/tillr-autoqa.md#OQ-AQ2) is blocked | shipped |
| [Roadmap](roadmap.md) | The unfinished work, in order, with the reason for the order. | Kept current | shipped |

## How this page is kept

This page is edited by hand. It has one row for every document under `docs/design`,
`docs/reference` and `docs/tillr`, so a document added, moved or deleted changes this page in the
same commit, and a status changes here when the document's own status paragraph does.

Its tables are also the declaration of what the tillr pack delivers: every row whose last cell is
`shipped` is copied, and a row reading `not shipped` is not. To refresh the delivered copies
after this page or a shipped document changes, commit the change, then run `just pack-docs` and
commit what it writes. The copies are a snapshot of one commit, so the gate never fails because a
shipped document changed since; `just pack-docs` and `python3 tests/pack-sync.test.py --fresh`
say how far the snapshot lags.
