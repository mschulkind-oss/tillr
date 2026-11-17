---
title: "tillr designs: what is built, what is queued, and what is only proposed"
status: accepted
stage: CURRENT
updated: 2026-11-16
tags: [tillr, index, status, design]
summary: "The one page to read first about tillr: every design and reference document, what each covers, its status as of 2026-11-16 measured against the code and the queue rather than the document's own status line, and what tillr will build from it next. It also declares which documents the tillr pack delivers to other workspaces."
---

# tillr designs: what is built, what is queued, and what is only proposed

**Status:** 2026-11-16. Every status below was checked against the tree at `424cc9db0` and the
queue on this checkout's store that evening, not copied from the documents' own status lines;
[a list of the lines that were wrong](#status-lines-that-are-stale) is at the end. The tree moves
several times an hour while the drain runs, so read a queued item's state as of this date.

## Read this first

**What tillr is.** tillr is the owner's work-queue tooling: eight command-line programs over one
SQLite database, the **store**. Work is declared as **items** in tracked Markdown (an HTML-comment
item block under a heading of a design document or a roadmap). A loop, the **drain**, plans which
item is ready, runs an agent for it in a **lane** (a leased slot with its own git worktree),
checks the result with the **gate**, and puts one commit per item on the trunk with the
**lander**. Each item has a **fence**, the paths it may change, and **criteria**, the testable
conditions its verdict is judged against. An `OQ-` id names an open question only the owner rules
on.

**If you are reading this in another workspace,** you are reading the copy the tillr pack
delivered to `~/.local/share/tillr/docs/tillr/design-status.md`. It is a snapshot of one commit,
named in its banner and in `~/.local/share/tillr/docs/snapshot.json`; the source lives in the
owner's repository at `docs/tillr/design-status.md`. Every document below marked
`shipped` sits beside it at the same repository path, and the module READMEs sit at
`~/.local/share/tillr/<module>/README.md` beside the source they describe. A link to anything that
was not delivered points at the repository on GitHub.

**How to read the status column.**

- **Built:** on `main` today; the row names the program or package.
- **Partly built:** some of the design is on `main`; the row says which part, and the rest is queued.
- **Designed, queued:** every ruling is in and the build is filed as queue items, named in the row.
  Queued is a promise in order, not a date: the drain works the queue in its own order.
- **Proposed, awaiting the owner:** the design waits on the owner's rulings, named by question id.
  Nothing in it is promised until those are ruled.
- **Superseded / historical:** kept as a record; listed in
  [the second table](#superseded-and-historical-records) and not delivered with the pack.

**Reading order for an agent from another project.** This page first; then
[the core and the Git-landing definition](../design/tillr-core-and-git-landing.md), the owner's
stated direction for tillr, which is the first row of [the status table](#status-of-every-design);
then [What tillr will change for AutoQA](../design/tillr-separable-parts.md), tillr's proposal to
the AutoQA pipeline and the contract between the two, which now sits under that direction.

**What is stable to build on.** tillr's seam for another program is processes and records: each
face (one subcommand of one program) reads and writes versioned JSON records, and its exit code is
the contract. Go packages are not a supported interface. Which faces and records another program
may rely on, and how it learns of a breaking change, is
[section 2, "The contract between tillr and AutoQA, for now"](../design/tillr-separable-parts.md#2-the-contract-between-tillr-and-autoqa-for-now),
whose first part is
[The stable surface](../design/tillr-separable-parts.md#21-the-stable-surface).

**How to ask for something.** Write the request in a design document in your own repository,
saying what you need and the case that needs it, and ask the owner to relay it. tillr answers in
[tillr-separable-parts.md](../design/tillr-separable-parts.md), which the pack delivers (its
[section 2.4](../design/tillr-separable-parts.md#24-how-requests-and-answers-travel) says how
requests and answers travel), and files what it accepts as items that appear in this page's next
column. A request that cuts against a settled ruling gets a reason and the ruling's id, not a
silent no.

## How the pieces fit

Ten modules under `tools/` and one program. `tillr` dispatches to the seven `tillr-<noun>`
programs, and each also runs alone with an explicit `--store`. The keeper, `tools/tillr-roll/tillr-keep`,
restarts the drain loop when it exits; it has no README. The `tillr-operating` skill, delivered
with the pack as a skill, says how the eight programs compose across the loop's seven steps.

| Piece | What it does | Status (2026-11-16) | Pack |
| :--- | :--- | :--- | :--- |
| [tillr-store](../../tools/tillr-store/README.md) | Creates, migrates and checks the SQLite store every other program writes. The shared library is `tools/tillr`. | Built | shipped |
| [tillr-item](../../tools/tillr-item/README.md) | Items, their states, criteria, verdicts and decisions; `sync` reads item blocks from tracked Markdown under `docs/`. | Built | shipped |
| [tillr-run](../../tools/tillr-run/README.md) | Runs one agent or command in a lane under a lease, records its end cause and usage; one adapter per backend. | Built | shipped |
| [tillr-drain](../../tools/tillr-drain/README.md) | The loop: plans, stages lanes, dispatches runs, judges candidates, lands, and says why each item waits. | Built | shipped |
| [tillr-gate](../../tools/tillr-gate/README.md) | Runs the declared checks of `tools/gate/checks/*.json` against a candidate, with budgets and remembered results. | Built | shipped |
| [tillr-land](../../tools/tillr-land/README.md) | Squashes a gated candidate onto the trunk as one commit under the land lock. | Built | shipped |
| [tillr-view](../../tools/tillr-view/README.md) | Read-only faces over the store, and `serve`, the observatory web page. | Built | shipped |
| [tillr (dispatcher)](../../tools/tillr-cli/README.md) | `tillr <noun> <face>` forwards to the seven programs. | Built | shipped |
| [loop adapter](../../tools/tillr-loop/README.md) | This repository's glue between the drain and its gate and lander; not part of the pack's source tree. | Built | shipped |
| [tillr-trace](../../tools/tillr-trace/README.md) | Records the commands and git calls a run makes, through a shell shim the pi adapter sources. | Partly built (slice 1 of [session recording](../design/session-recording.md)) | shipped |

## Status of every design

Ordered for a reader from another project: the owner's direction first, then the proposal
addressed to that reader, then the
roadmap, then from the foundation outward. "Next" lists queued item ids in the order their dependencies
allow, first the ones that wait on nothing.

| Document | Covers | Status (2026-11-16) | What tillr builds next | Pack |
| :--- | :--- | :--- | :--- | :--- |
| [The core and the Git-landing definition](../design/tillr-core-and-git-landing.md) | The owner's direction: tillr as general machinery of queues and graphs whose nodes are agents and commands, with landing changes in a Git repository as one small, separate definition on top; where the Git-landing assumption is wired in today, three shapes for that one definition, and the extension points a second use would fill. | The owner's stated direction, 2026-11-17; a proposal for both projects to react to; nothing built. Open: [OQ-CG1](../design/tillr-core-and-git-landing.md#OQ-CG1), [OQ-CG2](../design/tillr-core-and-git-landing.md#OQ-CG2), [OQ-CG3](../design/tillr-core-and-git-landing.md#OQ-CG3), [OQ-CG4](../design/tillr-core-and-git-landing.md#OQ-CG4), [OQ-CG5](../design/tillr-core-and-git-landing.md#OQ-CG5), none of which needs an answer before that conversation | Nothing is filed: AutoQA's agent reacts to its sections 4 to 6, then the owner and both sides decide what to build | shipped |
| [What tillr will change for AutoQA](../design/tillr-separable-parts.md) | tillr's proposal to the AutoQA pipeline: what changes now, what waits on the queued design, what tillr will not take, and the contract between the two. | Proposed, awaiting the owner: [OQ-SP1](../design/tillr-separable-parts.md#OQ-SP1), [OQ-SP2](../design/tillr-separable-parts.md#OQ-SP2), [OQ-SP3](../design/tillr-separable-parts.md#OQ-SP3), [OQ-SP4](../design/tillr-separable-parts.md#OQ-SP4), [OQ-SP5](../design/tillr-separable-parts.md#OQ-SP5), [OQ-SP6](../design/tillr-separable-parts.md#OQ-SP6), [OQ-SP7](../design/tillr-separable-parts.md#OQ-SP7), [OQ-SP8](../design/tillr-separable-parts.md#OQ-SP8) | The proposed small changes of its section 3, filed as items only once the owner rules | shipped |
| [Roadmap — tillr](roadmap.md) | The order of all remaining tillr work and the reason for it. | Current; reconciled 2026-11-15 | Its own blocks: eight drain items are ready (`item-drain-recover-commands`, `item-drain-express-lanes`, `item-drain-record-killed-run-spend`, `item-drain-hold-repeatedly-orphaned`, `item-drain-design-seat-quota-failover`, `item-drain-answered-conflict-beats-stale-retry`, `item-conflict-transition-not-duplicate`, `item-drain-check-step-program-flags`), then gate, policy, sync and view items | shipped |
| [Board utilities](../design/board-utilities.md) | The founding contract: seven programs over one store, each runnable alone, joined by processes and records. | Built: the seven `tillr-<noun>` programs and the shared `tools/tillr` library | Nothing; the contract is in force | shipped |
| [Item states](../reference/tillr-item-states.md) | Reference for the seven item states and frozen criteria. | Current reference | Nothing | shipped |
| [Queue persistence ruling](../design/queue-persistence-ruling.md) | Shared documents carry the work; private store state stays private; the item-block grammar. | Built: `tillr item sync` reads the grammar | Nothing of its own | shipped |
| [Queue persistence](../design/tillr-queue-persistence.md) | Documents define the queue and the store only projects it; ordering, parking, reconciliation. | Partly built: sync, block retirement after landing, the frozen legacy seed | `item-qp-sync-first-import-order`, `item-qp-one-question-reader`, `item-qp-store-inventory`; then `item-qp-sync-roadmap-scope`, `item-qp-sync-parks-stored-items`, `item-qp-loop-sync-scope`, `item-qp-reconcile-report`, `item-qp-status-sync-residue` | shipped |
| [Convergence](../design/tillr-convergence.md) | Three structural causes of the loop's early failures and the target that removes them. | Partly built: documents are the queue, holds name a mover, `tillr drain why` | `item-seed-retire` | shipped |
| [Item graphs](../design/tillr-item-graphs.md) | An item runs as a finite graph of nodes from a tracked workflow, on one lane, to one commit. | Partly built: blocks 1, 2 and 8 landed 2026-11-16 (a two-node workflow in `config/tillr/workflows/`, several commits landing as one, workflow progress kept across a restart) | `item-ig-workflow-check`, `item-ig-command-node`, `item-ig-node-write-scope`, `item-ig-node-outputs`, `item-ig-seat-default-workflow`; then `item-ig-exit-condition`, `item-ig-parallel-readers`, `item-ig-stage-names-workflow`, `item-ig-propose-replacement` (the planning node's proposal), `item-ig-scouted-design-workflow`, `item-ig-evaluator-node`, `item-ig-view-nodes` | shipped |
| [Planning node and model tiering](../design/tillr-planning-node-model-tiering.md) | A planning node splits a large item into checked slices, and each seat climbs a ladder of models from the cheapest. | Proposed, awaiting the owner: [OQ-PT1](../design/tillr-planning-node-model-tiering.md#OQ-PT1), [OQ-PT2](../design/tillr-planning-node-model-tiering.md#OQ-PT2), [OQ-PT3](../design/tillr-planning-node-model-tiering.md#OQ-PT3), [OQ-PT4](../design/tillr-planning-node-model-tiering.md#OQ-PT4), [OQ-PT5](../design/tillr-planning-node-model-tiering.md#OQ-PT5), [OQ-PT6](../design/tillr-planning-node-model-tiering.md#OQ-PT6), [OQ-PT7](../design/tillr-planning-node-model-tiering.md#OQ-PT7), [OQ-PT8](../design/tillr-planning-node-model-tiering.md#OQ-PT8); nothing built | Nothing until ruled | shipped |
| [Judge and lifecycle](../design/tillr-judge-and-lifecycle.md) | Who issues a verdict on a criterion, owner-only criteria, terminal states and their receipts. | Partly built (its section 7 measures which part); the rest queued | `item-jl-criterion-claim-class`; then `item-jl-owner-criterion-hold`, `item-jl-terminal-state-receipts`, `item-jl-judge-intake`, `item-jl-judge-before-gate`, `item-jl-judge-blocked-and-defective` | shipped |
| [Criterion repair](../design/criterion-repair.md) | How a frozen criterion that is wrong or stale gets superseded. | Proposed, awaiting the owner: [OQ-CR2](../design/criterion-repair.md#OQ-CR2), [OQ-CR3](../design/criterion-repair.md#OQ-CR3), [OQ-CR4](../design/criterion-repair.md#OQ-CR4), [OQ-CR5](../design/criterion-repair.md#OQ-CR5), [OQ-CR6](../design/criterion-repair.md#OQ-CR6) | Nothing until ruled | shipped |
| [Idea pipeline](../design/tillr-idea-pipeline.md) | How a sentence the owner types becomes a ranked item through scouting, design and a fenced build. | Partly built: the closed seven-state model, item kinds and edges; the child-item manifest is not built | The idea-saving items of observatory actions (`item-oa-idea-save`, `item-oa-idea-design-turn`) | shipped |
| [Designer sessions](../design/tillr-designer-sessions.md) | Review comments on a design reach the session that last worked on it, as small design items. | Partly built: hand-driven comment delivery landed 2026-11-16 | `item-comments-intake-in-the-loop` (running), `item-comments-design-items-at-the-desk`, `item-comments-hold-the-design`, `item-comments-show-the-designer` | shipped |
| [Run wall ruling](../design/run-wall-ruling.md) | No default wall-clock limit on a run; a committed candidate is judged whatever the wall says. | Built | Nothing | shipped |
| [Restart](../design/tillr-restart.md) | A restart is a request: the loop replaces itself and paused sessions resume on the new build. | Partly built: run output to a file, the repository settings file, pausing and resuming a worker | `item-drain-restart-on-request`, `item-pi-fork-stop-at-gap`; then `item-land-then-roll-and-push`, `item-dev-tillr-roll` | shipped |
| [Drain successor](../design/tillr-drain-successor.md) | Replace the clock-driven tick with one successor function over the item set. | Designed, not yet filed as items | Convergence orders it last, after hold classes and derived copies | shipped |
| [Gate rework](../design/gate-rework.md) | A ten-second landing gate that remembers only what it can recompute. | Partly built: six of nine slices (checks as files, remembered tree results, caller-supplied base, budgets) | `item-gate-heavy-runner`; then `item-gate-deploy-from-green`, `item-gate-scale-test`; from the roadmap `item-gate-pinned-fork-inputs`, `item-gate-runner-pays-first`, then `item-gate-red-cap`, `item-gate-lane-base` | shipped |
| [Landing principles](../design/landing-principles.md) | The durable landing rulings, the measurement behind each and what enforces it. | Current reference | Nothing | shipped |
| [SQLite contention recovery](../recovery/sqlite-contention/README.md) | Why the store's writer stalled the loop, and the fixes. | Built, pending independent review: shorter writer spans, no fsync under the writer, writer tracing, checkpoint ownership | The review | shipped |
| [Observatory contract](../design/tillr-observatory.md) | What the drain must record so a read-only web page can show every item, stage and call. | Built: `tillr-view serve` with its Go API and React front end | Superseded in its screens by the redesign below | shipped |
| [Observatory redesign](../design/observatory-redesign.md) | Linked pages for the owner's six questions: home, passes, runs, performance, trunk. | Partly built: shell, Home with lanes, Items and Passes, run pages, trace export, Trunk and Performance screens | `item-obs2-lane-page`, `item-obs2-run-usage`; then `item-obs2-dependency-graph`, `item-run-records-progress-and-helpers` | shipped |
| [Observatory actions](../design/observatory-actions.md) | The observatory's first two writes: reordering the queue and saving an idea. | Designed, queued | `item-oa-move-to-top`; then `item-oa-drag`, `item-oa-place-numbers`, `item-oa-idea-save`, `item-oa-from-mark`, `item-oa-idea-design-turn`, `item-oa-order-record-read`, `item-oa-order-record-land` | shipped |
| [Session recording](../design/session-recording.md) | Record agent work once, through a collector, a command shim and a usage hook, and query it later. | Partly built: slice 1 (`tools/tillr-trace`, the shim, writer tracing) | Slices 2 and 3 are not filed | shipped |
| [Distribution](../design/tillr-distribution.md) | One pack, eight programs on `PATH` built from shipped source, and the documentation split. | Built: `yolo-packs/tillr` | Nothing queued | shipped |
| [Self-hosted build](../design/tillr-selfhosted-build.md) | Running tillr from this tree's source across unattended drains. | Partly built: the pack launchers build once per source digest; `just tillr-build` builds this tree; the planner reads Vantage's index. Awaiting the owner on [OQ-SH2](../design/tillr-selfhosted-build.md#OQ-SH2) | Nothing until ruled | shipped |
| [Claude backend](../design/tillr-claude-backend.md) | A `run-claude` adapter so any seat can run on Claude Code headless. | Built: `tools/tillr-run/backends/claude` | Nothing | shipped |
| [Muse backend](../design/tillr-muse-backend.md) | The Meta Muse adapter. | Built and dormant: the subscription lapsed 2026-11-12 | Nothing | shipped |
| [Goal monitor](../design/tillr-goal-monitor.md) | A goal as a recurring item whose criteria are a property of the system, checked on a schedule. | Proposed, awaiting the owner: [OQ-GM1](../design/tillr-goal-monitor.md#OQ-GM1), [OQ-GM2](../design/tillr-goal-monitor.md#OQ-GM2) | After the rulings: scheduled items, the check-in graph, retiring the steering timer | shipped |
| [AutoQA](../design/tillr-autoqa.md) | Exploratory testing of tillr with its own store, cheap probes and a judge that confirms bugs. | Proposed, awaiting the owner: [OQ-AQ2](../design/tillr-autoqa.md#OQ-AQ2); no AutoQA code exists | `item-design-autoqa-slices`, a design-seat item that cuts it into build items, after [OQ-AQ2](../design/tillr-autoqa.md#OQ-AQ2) | shipped |
| [Agent context delivery](../design/agent-context-delivery.md) | Which channel each kind of instruction reaches an agent through. | Proposed, a sketch: [OQ-AC1](../design/agent-context-delivery.md#OQ-AC1), [OQ-AC3](../design/agent-context-delivery.md#OQ-AC3), [OQ-AC5](../design/agent-context-delivery.md#OQ-AC5) | Nothing until ruled | shipped |
| [Judge in tillr](../design/work-control-judge-in-tillr.md) | Ruling to move the session work-control judge into tillr and retire the shadow approver. | Ruled 2026-11-12; nothing built | Not filed; the judge's build lives in judge and lifecycle | shipped |

## Superseded and historical records

Kept for their evidence. None is delivered with the pack; each link goes to the repository.

| Document | What it was | Status | Pack |
| :--- | :--- | :--- | :--- |
| [tillr today](../design/tillr-today.md) | The plan of 2026-11-12 for getting the loop running. | Superseded | not shipped |
| [AutoQA seams](../design/autoqa-tillr-seams.md) | AutoQA's four seam questions against the binaries of 2026-11-13. | Superseded by AutoQA and separable parts | not shipped |
| [Next steps](../design/tillr-next-steps.md) | The dispatch wave of 2026-11-10. | Historical | not shipped |
| [Day 2](../design/tillr-day2.md) | The plan of 2026-11-12 for the first unattended loop. | Historical: the loop runs unattended | not shipped |
| [Loop readiness](../design/tillr-loop-readiness.md) | What blocked an overnight burn on 2026-11-12. | Historical | not shipped |
| [Chunking plan](../design/tillr-chunking-plan.md) | The 2026-11-12 cut of remaining work into lanes. | Historical: replaced by item blocks | not shipped |
| [Board reshape](../design/tillr-board-reshape.md) | Re-authoring the old board's backlog for tillr. | Historical: done | not shipped |
| [Seed triage](../design/tillr-seed-triage.md) | The first seed of 56 board items. | Historical: the seed file is frozen and the queue lives in documents | not shipped |
| [Coverage 2026-11-13](../design/tillr-coverage-2026-11-13.md) | A walk of fifteen designs for owed work. | Historical record | not shipped |
| [Drain acceptance 2026-11-13](../design/tillr-drain-acceptance-2026-11-13.md) | The first unattended two-lane run, measured. | Historical record | not shipped |
| [Observatory QA 2026-11-13](../design/observatory-qa-2026-11-13.md) | Findings against the first observatory. | Historical record | not shipped |
| [Observatory design](../design/observatory-design.md) | The three-pane board design. | Superseded by the observatory redesign | not shipped |
| [Observatory design plan](../design/observatory-design-plan.md) | Parking lot for that design. | Historical | not shipped |
| [Queue inventory 2026-11-13](../design/queue-inventory-2026-11-13.md) | Every candidate piece of work, surveyed. | Historical record | not shipped |
| [Board utilities plan](../design/board-utilities-plan.md) | The implementation plan for the seven programs. | Graduated: built | not shipped |
| [Board utilities lanes](../design/board-utilities-lanes.md) | That plan cut into lanes. | Graduated | not shipped |
| [Board documents retirement](../design/board-docs-retirement.md) | Retiring the old board's documents. | Done | not shipped |
| [Board names](../design/board-names.md) | Naming the machine; chose tillr. | Historical | not shipped |
| [LAND-3 inventory](../design/land-3i-inventory.md) | mq against tillr-land, before one lander was chosen. | Deprecated | not shipped |
| [mq retirement](../design/mq-retirement.md) | Deleting the merge queue and rebuilding inside tillr. | Historical: mq is gone | not shipped |
| [Run wall evidence](../design/run-wall-evidence.md) | The measurement behind the run wall ruling. | Historical record | not shipped |
| [Gate dual-run evidence](../design/gate-dual-run-evidence.md) | The Go gate against the Python gate on three candidates. | Historical record | not shipped |
| [Landing system](../reference/landing-system.md) | The two landers before tillr-land. | Superseded by landing principles | not shipped |
| [Landing runbook](../reference/landing-runbook.md) | Draining the old board by hand. | Superseded by the tillr-land README | not shipped |
| [Drain velocity research](../research/tillr-drain-velocity.md) | One overnight ten-lane drain, observed. | Research record | not shipped |

## What is changing right now

Landed on 2026-11-15 and 2026-11-16, read from `git log --oneline -60 -- tools docs/design`.
Documents written before these may describe the older behavior.

- **Store locking.** Shorter SQLite writer spans, no disk flush while holding the writer, writer
  tracing, and short-lived programs no longer checkpoint or delete the log (`31ba65672`,
  `64bee8457`, `f26fc6926`, `27f04df2d`, `91425b408`).
- **The gate.** Fast tree checks migrated to declaration files with enforced budgets, a slow check
  slows the gate instead of faulting the item, and `go-build` builds with `-trimpath`
  (`b1a2cd37e`, `71e782f64`, `f950d5381`, `511407810`).
- **Landing.** The lander no longer lands a change the trunk already carries, and the drain
  settles a run whose item already landed without judging it (`89b305539`, `98280ec09`).
- **Stopping and pausing.** Stopping the loop leaves runs alone unless `drain stop --end-runs`
  is given, and worker sessions pause and continue without a retry (`55c02cbd0`, `637458006`).
- **Workflows and settings.** Sequential agent workflows on one lane, workflow progress across
  restarts, repository settings read at startup, backend policy reloaded between passes
  (`b657ed406`, `6ee7e1d04`, `d327cf079`, `59211351a`).
- **Observatory.** Home shows every lane, and `just observatory` serves this checkout's own build
  (`a8841462f`, `4b13fd339`).
- **Design comments.** Hand-driven delivery of review comments to a design session (`895409b38`).

## Status lines that are stale

Found on 2026-11-16 by comparing each document's own status with the tree and the queue.

- [Gate rework](../design/gate-rework.md) said "Nothing built"; six of nine slices have landed.
  Fixed in its status line.
- [Restart](../design/tillr-restart.md) said "Nothing built"; three of seven blocks have landed.
  Fixed in its status line.
- [Distribution](../design/tillr-distribution.md) said "Pack unbuilt"; the pack is built. Fixed
  in its status line.
- [Item graphs](../design/tillr-item-graphs.md) said nothing of the runtime is built; three blocks
  have landed. Fixed in its status line.
- [Self-hosted build](../design/tillr-selfhosted-build.md) says "Nothing built"; the launcher and
  the Vantage-index step are built. Not fixed: the line is part of a measured paragraph.
- [The observatory contract](../design/tillr-observatory.md) says it is a draft whose next step is
  to rule four questions; no questions remain and the server is built. Not fixed: frontmatter.
- [Board utilities](../design/board-utilities.md) still names writing the implementation plan as
  its next step; the plan graduated and the programs are built. Not fixed: frontmatter.
- [AutoQA seams](../design/autoqa-tillr-seams.md) is superseded and its frontmatter says all four
  questions were ruled, but Vantage still counts them open. Not fixed: that is its Open Questions
  section.
- The `tillr-operating` skill's Known Gaps callout (verified 2026-11-12) says the event emitters
  are unbuilt; [separable parts](../design/tillr-separable-parts.md) measured them recording on
  2026-11-16. Not fixed: the callout is checked by `tests/tillr-docs.test.py` and spans lines.

## How this page is kept

This page is edited by hand and dated. Its tables are also the declaration of what the tillr pack
delivers: every row whose last cell is `shipped` is copied, and a row reading `not shipped` is
not. To refresh the delivered copies after this page or a shipped document changes, commit the
change, then run `just pack-docs` and commit what it writes. The copies are a snapshot of one
commit, so the gate never fails because a shipped document changed since; `just pack-docs` and
`python3 tests/pack-sync.test.py --fresh` say how far the snapshot lags.
