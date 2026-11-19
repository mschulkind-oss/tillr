---
title: "Roadmap — tillr"
status: accepted
tags: [roadmap, tillr, board, drain, observatory, gate]
summary: "What comes next for tillr and why in that order: the rulings only the owner can make, the defects that stop the running loop, and the builds behind them."
---

# Roadmap — tillr

**Last reconciled: 2026-11-15**, late, against the tree, the store and `vantage-check index`.
The repository roadmap's [Tillr section](../roadmap.md#tillr) points here. This page owns the order
and the reasons for it. Each linked design owns its own state, next step and questions, and the
queue (`tillr-view waiting`) owns which item is running.

## Ordering basis

The first rule that separates two entries decides.

1. **The owner's rulings come first**, because nobody else can make them. A design with an open
   question is a draft, however complete it reads.
2. **A defect that stops the running loop outranks any build.** The owner's standing instruction
   of 2026-11-15 is that keeping the loop running is the first job.
3. **A substrate outranks what sits on it.**
4. **Ties break toward the smaller sitting.**

## 1. Rulings that are waiting on the owner

The questions below are left for the owner on this page: 30 by `vantage-check index` on
2026-11-18, 22 of them the provenance review's. Each has options and a recorded leaning.
Twenty-three others that stood here were settled on 2026-11-15 and 2026-11-16 by the session's
designer, at the owner's request, and each is the owner's to overturn. Not every one of them says
so in its design: [the provenance review](./decision-provenance-review.md#3-corrections-that-need-no-ruling)
lists the rows that claimed a ruling nobody recorded.

[The queue mirrors the roadmap's outstanding designs](../design/tillr-queue-mirrors-designs.md),
written on 2026-11-18 to the owner's words of that evening, comes ahead of the list below: it
reopens how work written in a document reaches the queue, which the owner never ruled, and none
of it is built until he has.

[A review before every code landing](../design/tillr-diff-review.md), written on 2026-11-18 to
the owner's words of that evening, asks which model reviews an item's commits before they land
and who settles a repeated disagreement. No agent has ever read a change before tillr landed it,
and none of the design is built until he answers the first.

[A cheap agent checks every design document a designer changes](../design/tillr-design-document-check.md),
written on 2026-11-18 to the owner's words of that evening, is the documents' counterpart of that
review: it holds a designer's document to the writing skills, never to logic. It asks whether a
failing document lands before it is fixed, whether old faults in a touched document are the
designer's to fix, and what the check is called. It sits here because it shares the review's
return route, and none of it is built.

Five more left this list on 2026-11-18, none by the owner's ruling, and each is his to overturn.
Two are a designer's decisions: [`OQ-CR4`](../design/criterion-repair.md#OQ-CR4), that the judge
step gets no separate check of a criterion's premise, and
[`OQ-AC1`](../design/agent-context-delivery.md#OQ-AC1), that a lane-only extension is held to
that by a test and not by a manifest key. Two were closed as stale:
[`OQ-JL4`](../design/tillr-judge-and-lifecycle.md#OQ-JL4), which asked about two next actors
that no longer exist or cannot be named, and
[`OQ-AC5`](../design/agent-context-delivery.md#OQ-AC5), which asked how to measure caps the
owner has since removed. One is blocked: [`OQ-AQ2`](../design/tillr-autoqa.md#OQ-AQ2), until
AutoQA leaves the [Icebox](#icebox).

1. [Who made the decisions recorded in the tillr designs](./decision-provenance-review.md): the
   owner keeps, overturns or reopens the decisions he did not make. First because every other
   design on this page is built on some of them.

2. [`OQ-SH2`](../design/tillr-selfhosted-build.md#OQ-SH2): whether the copy of tillr that other
   workspaces run follows every landing or is refreshed only when it is published. Next because
   it is a cost on every landing but stops nothing.

3. [Criterion repair](../design/criterion-repair.md), one question,
   [`OQ-CR7`](../design/criterion-repair.md#OQ-CR7): who may replace a criterion's text once a
   verdict has frozen it. The owner's ruling of 2026-11-17 gave that act to a role that was
   deleted with the board. The rest of that day's four rulings stands: a replacement carries a
   required reason, the judge sees both texts, an unsatisfiable criterion is repaired by reopen
   then supersede, and a supersession refuses a verdict in flight. None of it is built.

[What a lane's agent is told](../design/agent-context-delivery.md#decision-ledger) left this list:
its two tillr questions are closed as above, and the one it keeps, a startup profile for pi, is
routed from [the pi roadmap](../pi/roadmap.md).

[The item graph is the whole path](../design/tillr-graph-is-the-path.md), written on 2026-11-17 to
the owner's rulings of that day, proposes that every act on an item is a node in its graph and that
one pure function decides what comes next. The graph model is not built. The owner ruled five of
its questions, and a sixth, which structure stores the graph, moved to
[the storage design](../design/tillr-storage.md). On 2026-11-18 he ruled the seventh,
[`OQ-GP7`](../design/tillr-graph-is-the-path.md#OQ-GP7): while a check is red, work lands only if
it does not touch what that check reads, with no landing cap; the red cap stays live until that
rule is built.
The storage design's [`OQ-TS1`](../design/tillr-storage.md#OQ-TS1) and
[`OQ-TS3`](../design/tillr-storage.md#OQ-TS3) are blocked until its trials are run again on a healthy file
system. [Early design landing](../design/tillr-early-design-landing.md) designs how a question and its design
land ahead of their item; the owner ruled [`OQ-EL1`](../design/tillr-early-design-landing.md#OQ-EL1) on
2026-11-18, that every design change lands that way; its [`OQ-EL3`](../design/tillr-early-design-landing.md#OQ-EL3) is blocked until
giving an item up is designed. [The vocabulary](../design/tillr-vocabulary.md), written on 2026-11-18, gives one meaning to item, node,
run, session and worktree. The owner ruled its three questions on 2026-11-18: the file that
turns tillr into one use is a charter ([`OQ-VO4`](../design/tillr-vocabulary.md#OQ-VO4)), the
first use is `git-landing` ([`OQ-VO5`](../design/tillr-vocabulary.md#OQ-VO5)), and an item is
finished once the full suite has passed on a trunk commit that contains it
([`OQ-VO6`](../design/tillr-vocabulary.md#OQ-VO6)). Its other decisions are the designer's and
await the owner's review in its ledger. [The landing call](../design/tillr-landing-call.md), written on
2026-11-18, proposes that a worker ends its run with one tool call and hears back only when something needs fixing;
five of its build steps are filed [below](#filed-2026-11-18-a-landing-is-a-suspended-tool-call), and
its [`OQ-LC1`](../design/tillr-landing-call.md#OQ-LC1), whether an item keeps its worktree until the full suite
has passed on it, is blocked until worktrees belong to items.

[What tillr will change for Product QA](../design/tillr-separable-parts.md) is tillr's answer to the
Product QA pipeline's requests. The pipeline's second-round reply is recorded as received in
[Product QA's reply](../design/tillr-product-qa-requests.md), and on 2026-11-17 the owner said to build:
the answer is [the build plan](../design/tillr-definitions-plan.md), whose eighteen items have all
landed. The document asks nothing of the owner now. Two of its questions are blocked until a
consumer needs them, [`OQ-FQ7`](../design/tillr-separable-parts.md#OQ-FQ7) and
[`OQ-FQ8`](../design/tillr-separable-parts.md#OQ-FQ8); the other six are closed in its ledger, one
on the owner's words and the rest by events or by a designer's decision that awaits his review. Its
four small pieces of work (P2, P3, P4, P6) need no ruling and are unfiled. (The owner sometimes
calls the pipeline "AutoQA", which is
[tillr's own exploratory-testing design](../design/tillr-autoqa.md), a different thing.)

[The core and the landing definition](../design/tillr-core-and-git-landing.md)
writes down the owner's direction of 2026-11-16 and asks nothing of him now. Its five questions
are closed in its ledger: two on his own words, one by a designer's decision that the build already
rests on and that awaits his review, one overtaken by events, and the naming question moved to the
vocabulary as [`OQ-VO4`](../design/tillr-vocabulary.md#OQ-VO4) and
[`OQ-VO5`](../design/tillr-vocabulary.md#OQ-VO5), which the owner ruled on 2026-11-18: the
`git-landing` charter. It also lists the owner's statements as the
pipeline reported them, and says which of them he has since made directly.

[Work reaches tillr through several doors](../design/tillr-intake-paths.md) writes down the owner's
idea of 2026-11-15 to 2026-11-17 that each way work enters (an agent files an item, the owner dumps
a ramble) runs its own small graph and ends in one item shape, and lists what is not yet settled.

[Planning and model tiering](../design/tillr-planning-node-model-tiering.md) is the design for the
owner's ask to spend less by giving each kind of work the cheapest model that is enough for it. The
owner ruled on 2026-11-18 that Terra implements and Luna scouts, and the planning stage
[filed below](#filed-2026-11-18-plan-first-then-spend-each-model-where-it-is-enough) landed that
day; no item runs through it yet. Nothing in it needs the owner today: its questions wait until
planned items have run and their cost can be read, and its ledger holds designer's decisions he has
not reviewed. Two faults in the run record that the design found are
[filed below](#filed-2026-11-17-the-run-records-spend-corrected-before-it-is-compared).

[Replaying the drain's recorded tasks against several models](../design/model-benchmarks-from-drain-records.md)
is the owner's request of 2026-11-17 to compare models on work this repository has really done,
read side by side before any rubric exists. It sits beside planning and model tiering because it
is the other way to learn which model is enough for which work. He said he is not ready to run
it, so its questions wait on him. Its first step, copying the shortlisted records before Git
deletes them, runs no model and is work to file.

[The judge and lifecycle](../design/tillr-judge-and-lifecycle.md) left this list: every question
it carried is a row of its ledger, and on 2026-11-16 it was cut into
[fourteen build items](../design/tillr-judge-and-lifecycle.md#8-build-order) that live in the
design. Eleven have landed and three are queued, as
[its seventh section](../design/tillr-judge-and-lifecycle.md#7-what-is-built-and-what-is-owed)
lists. AutoQA's one design-seat item is not admitted to the queue while that design is in the
[Icebox](#icebox).

[Queue persistence](../design/tillr-queue-persistence.md) is decided and waits on no ruling: every
question it carried is a row of its ledger, and on 2026-11-16 it was cut into
[eight build items](../design/tillr-queue-persistence.md#12-build-order) that live in the design.
The first gives each newly synced item its place in roadmap order, which is the owner's ruling of
2026-11-16, and waits on nothing. Much of the design was already built under other items, and
[its eleventh section](../design/tillr-queue-persistence.md#11-what-the-tree-already-holds)
measures which part. Nothing that retires or caps the run record is among the eight; an inventory
of that record is, because the owner ruled that the record is shaped only after it can be seen.

Two designs left this list on 2026-11-15. [Item graphs](../design/tillr-item-graphs.md) is
decided and waits on no ruling: one commit per item, all of refinement built, read-only nodes in
parallel, and lanes outside the cap for work the owner is waiting on. On 2026-11-16 it was cut
into [fifteen build items](../design/tillr-item-graphs.md#9-build-order) that live in the design,
in the owner's words: an item is slotted into a workflow, a node is one invocation of a harness
or one command, and a sub-agent a node starts is a child session and never a node. The first
block runs one item through a workflow of two nodes, a scout that only reads and then a builder,
to one landed commit, and waits on nothing. Nothing of the design was built before the cut
except the lander's squash, and
[its eighth section](../design/tillr-item-graphs.md#8-what-the-tree-already-holds) measures that.
[The observatory redesign](../design/observatory-redesign.md) is decided: the pages show
what a run used and never a price.

## 2. Keep the loop running

Second by rule 2. [Make Vantage's intended invocation the default](#route-vantage-index-reads-through-uvx-by-default)
comes first in recovery because an unavailable question reader holds otherwise actionable work.
[The overnight velocity evaluation](../research/tillr-drain-velocity.md)
puts identifying the store's live writer ahead of changing concurrency, because a lock-acquisition
fault stops progress regardless of lane admission. It is evidence and a recommended investigation,
not authorization to change the drain's policy.

On 2026-11-15 the loop stopped whole five times for the trouble of one item,
and each stop ended every run in flight. The causes are found and the repairs are filed as items
on this page, in this order:

- [Other people and other agents change the repository while the drain runs](#filed-2026-11-15-other-people-and-other-agents-change-the-repository-while-the-drain-runs).
  First because an operator's uncommitted edit is what caused the stops, and the owner's ruling of
  2026-10-23 already says such an edit is a wait and never a failure.
- [One item's trouble holds that item, and recovery is a command](#filed-2026-11-15-one-items-trouble-holds-that-item-and-recovery-is-a-command).
  Second because every recovery that night was done by hand and two of them caused the next stop.
- [Anyone can ask why an item is not running](#filed-2026-11-15-anyone-can-ask-why-an-item-is-not-running)
  and [the loop says why every item is where it is](#filed-2026-11-15-the-loop-says-why-every-item-is-where-it-is-and-what-it-has-spent).
  Third because half the lanes sat idle with no stated reason, and the second of these also
  carries the spend ceiling and turning the after-merge check back on.

### Route Vantage index reads through uvx by default

The owner confirmed on 2026-11-16 that running Vantage through its package runner is the
intended installation method. The shared tillr default instead assumes a standalone executable.
In this jail that executable is absent, so planner and view reads produce unknown-question holds;
the same reads succeed through the package runner. Fix this before other recovery work so a tool
invocation mistake does not hide the real reasons items are held.

**Outcomes**

- Read Vantage's question index through the intended installation without a per-command override.
- Keep explicit command overrides and honest failure reporting; never interpret a failed index as no questions.

**Agent Details**

- **Where:** the shared default in [`vantage.go`](../../tools/tillr/vantage/vantage.go), its
  consumers in [`tillr-view`](../../tools/tillr-view/) and [`tillr-drain`](../../tools/tillr-drain/),
  and the [committed tillr delivery](../../yolo-packs/tillr/).
- **Verify:** put a recording package-runner stub on PATH with no standalone checker; default
  reads must invoke `uvx vantage-check index --format json`. Keep override, command-failure,
  malformed-index and open-question tests. Sync and lint the delivery.



## 3. Roll onto new code without ending runs

[Rolling tillr onto new code](../design/tillr-restart.md) is built: all nine blocks of its build
order had landed by 2026-11-18, among them the pause, the restart request, the roll and push
after a landing, and this repository's settings file. It stays third as the record of why it
came first: every restart on 2026-11-15 was a kill, so a fix to tillr itself cost the runs in
flight, and [entry 4](#4-choose-where-each-seat-runs) reuses its stop-and-continue step.

## 4. Choose where each seat runs

[The Claude backend](../design/tillr-claude-backend.md) is built and has had one hand run. A
mixed policy puts designers on the subscription and builders on pi. What is left is filed
[on this page](#filed-2026-11-15-choose-where-each-seat-runs-by-quota-and-move-work-when-a-backend-runs-out):
re-reading the policy without a restart, reading quota directly with a reserve, splitting a seat
by share or at random for bake-offs, and moving a run when its backend runs out. Fourth because
it saves money on every design run but stops nothing when it is missing.

Also filed here: [a planning stage that fans implementation out by seat](#add-a-planning-stage-that-fans-implementation-out-by-seat),
which the owner asked to be high priority on 2026-11-18 and which sets the queue's order for model savings.

## 5. The ten-second gate

[The gate rework](../design/gate-rework.md) is decided and, as of 2026-11-18, built except one
decision: every slice of its build order has landed, and so have the red cap, the lane base and
runner-pays-first. The upstream drift probe of its `D-GR12` is not built. Fifth because the
owner's target is a gate measured in seconds and landings that outpace ten lanes.

Automatic revert landed on 2026-11-18 and is on here. First of the gate's remaining work is
[shortening the time from a red landing to its revert](#shorten-the-time-from-a-red-landing-to-its-revert),
because every lane waits on that revert.

## 6. The observatory answers the owner's questions

[The observatory redesign](../design/observatory-redesign.md) is built: all ten slices had landed
by 2026-11-18, including the lane strip, lane page, dependency graph and usage figures, and so
had [the observatory's first two writes](../design/observatory-actions.md). Sixth, and not
higher, because [entry 2](#2-keep-the-loop-running) fixes what the screens would only report.
Also filed here: [a stopped loop says what stopped it](#filed-2026-11-15-the-observatory-speaks-to-its-reader-not-to-its-builders).

## 7. The drain's own model

[Replace the drain tick with a converge function](../design/tillr-drain-successor.md) is built:
all five of its steps had landed by 2026-11-18, the last retiring the tick's names. The design is
still titled "successor function"; the owner's name for it is converge, and the document has not
been renamed.

## 8. Design work that keeps its context

Three pieces, filed on this page: [a design conversation keeps its designer](#filed-2026-11-15-a-design-conversation-keeps-its-designer),
[an item not written to the prose guide is rewritten before anyone builds it](#filed-2026-11-15-an-item-that-was-not-written-to-the-prose-guide-is-rewritten-before-anyone-builds-it),
and [a design on the roadmap always has an item in the queue](#filed-2026-11-15-a-design-on-the-roadmap-always-has-an-item-in-the-queue).
Eighth because each makes the queue cheaper to feed and easier to read, and none of them is
needed to keep it running.

## 9. Move the queue's items off this page

The sections below hold about forty item blocks that were filed here during 2026-11-15 because
the work had no other home that night. That makes this page four times its budget and mixes
priority with work detail. Each block belongs in the design it builds. Ninth, and deliberately
not done yet: moving a block changes what the queue reads, and it should be done one design at
a time as each is cut into build items.

## 10. Older designs

Each predates the round above. They are behind it because a ruling that is already open does not
get cheaper by being opened again.

- [Reshaping the board backlog](../design/tillr-board-reshape.md): both its questions were ruled
  on 2026-11-12. It owes the first triage list its sixth section describes. The earlier version
  of this page said a question was still open; that was stale.
- [Tillr today](../design/tillr-today.md) and [the observatory design](../design/observatory-design.md)
  are fully ruled. The first owes a reconciliation against the runs the loop has since made; the
  second is superseded screen by screen as [entry 6](#6-the-observatory-answers-the-owners-questions)
  lands.
- [The evidence tool](../../tools/pipeline-evidence/) still carries a reader for the deleted
  merge queue. Replacing it with a reader of the tillr store's landings is owed
  ([the retirement design](../design/mq-retirement.md#9-open-questions)), and so is deleting
  `.workboard.toml`, which nothing reads any more.
- [Scheduled tasks](../design/scheduled-tasks.md#12-open-questions) is fully settled and unbuilt:
  tillr has no due date or interval on an item yet. It is the substrate of the goal monitor,
  which is in the [Icebox](#icebox).

## Icebox

Wanted, not now, by the owner's call. Picking one up means giving it a place above.

- **Prices on the observatory**, iceboxed 2026-11-15. The owner wants to see what work cost in
  dollars and ruled that the pricing cannot be computed accurately enough yet, so
  the pages show usage only (section 6.6 of the observatory redesign). Picking it up
  means settling how a price is derived per backend, including a subscription that is not billed
  per run, cached tokens, and models whose price changes.

- **The Muse backend** ([design](../design/tillr-muse-backend.md#1-what-a-backend-is)), iceboxed
  2026-11-15: the owner is not subscribing to a Meta plan. The adapter stays in the tree and
  [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json) keeps it out of
  every fail-over. Picking it up means subscribing, naming `muse` in a policy again, and cutting
  its second stage from the design.

- **AutoQA, tillr's exploratory testing of itself** ([design](../design/tillr-autoqa.md)), iceboxed
  2026-11-17. The owner: "ice box this autoqa on the roadmap for now. I don't want to design this
  yet." No AutoQA code exists. Its one question,
  [`OQ-AQ2`](../design/tillr-autoqa.md#OQ-AQ2), is blocked until the design is picked up. The
  design's one queue item, `item-design-autoqa-slices`, is `not-started`: it was never admitted,
  so the drain never dispatches it. Picking it up means admitting that item and ruling the
  question, after which the item cuts the design into build items. This is not
  the Product QA pipeline, which [entry 1](#1-rulings-that-are-waiting-on-the-owner) covers.

- **The goal monitor** ([design](../design/tillr-goal-monitor.md)), iceboxed 2026-11-17. The owner:
  "let's icebox this goal stuff until we have a use for it again." Nothing is built and none of its
  three slices is in the queue. Its two open questions,
  [`OQ-GM1`](../design/tillr-goal-monitor.md#OQ-GM1) and
  [`OQ-GM2`](../design/tillr-goal-monitor.md#OQ-GM2), are left unanswered. Picking it up means a
  use for a scheduled check-in, ruling those two, and filing the slices, starting with
  [scheduled tasks](../design/scheduled-tasks.md#12-open-questions), its substrate.

## What this page does not cover

- **Pi**: its patches, forks, extensions and usage capture are in the [pi roadmap](../pi/roadmap.md).
- **The rest of the repository** is in the [repository roadmap](../roadmap.md).
- **Which item runs next** is the queue's answer, from `tillr-view waiting`, not this page's.

---

The sections from here down are the queue items filed on this page (see
[entry 9](#9-move-the-queues-items-off-this-page)). Their order is the order they were filed,
not priority.

## Filed 2026-11-18: the time from a red landing to its revert

<a id="shorten-the-time-from-a-red-landing-to-its-revert"></a>

### Add faster recovery from red landings

**Theme:** Heavy runner

**Description:** Tillr automatically reverts a landing when the full suite proves that it
broke trunk. Later landings wait for that revert, so a slow verdict holds up the whole drain.
Today the runner waits for every heavy check to finish before reporting a failure, even when an
earlier check has already found a problem.

On a loaded machine on 2026-11-18, the sharded Go tests took a median of fourteen minutes and
as long as fifty-three. Each proof also ran the drain's suite twice, while worker tests competed
with the proof for CPU time. Start recovery as soon as a check fails, give checks affected by
recent landings the first turn, and give the proof priority over worker tests. Still require the
full run to pass before advancing the green pointer, without imposing a time limit.

The work is four items, split by the design seat on 2026-11-18 after three builds stopped on it
as one. This block is the first and the smallest of them: a proof runs the drain's suite once.
The three blocks below it then change one thing each: a worker's processes get a lower CPU
priority, the checks that recent landings reach run first, and a failed check starts its
recovery while the proof continues. Each lands as its own commit and names the item it waits on.

**Outcomes**

- Run the drain suite once per proof.

**Agent Details**

- **Ref:** [gate rework Section 6.8](../design/gate-rework.md#68-a-red-heavy-check), the "Time to a revert" paragraph; the owner's ruling in [`OQ-GP1`](../design/tillr-graph-is-the-path.md#OQ-GP1).
- **Where:** the parity case `test_drain_name_parity` in `tests/go-test-shards.test.py`, the
  Go suite cases in `tests/fork-check.test.py`, and the "Time to a revert" paragraph of the gate
  design. This item changes nothing under `tools/`, though the directive's paths still list
  those modules from before the split.
- **Verify:** `python3 tests/go-test-shards.test.py` and `python3 tests/fork-check.test.py`, each
  with `TMPDIR` set to the run's scratch, and `uvx vantage-check docs/design/gate-rework.md`.

**Why the work is split, settled by the design seat on 2026-11-18.** Three builds stopped on this
block when it held six criteria across four modules. The first stopped on a path outside the
item's paths. The next two each reported that the task exceeds the seat, the last of them on the
last backend the build seat escalates to. That one left a change that ordered the declaration checks, deleted
the parity case and ran the recovery between two checks of the proof, and it named what it could
not settle: recovery for a second failure after an earlier revert had moved the trunk, legacy
verdicts that arrive together, and the CPU priority. The design seat then built all four parts in
a lane on 2026-11-18 at `332a18704`, to settle those points by measurement. With the changes of
the third and fourth items, `python3 tools/go-test-shards.py tools/tillr-drain -k 6` passed 2,086
tests. With the second item's change, the same command for `tools/tillr-run` passed 220. With this
item's change, `python3 tests/go-test-shards.test.py` ran 15 tests in 10.4 seconds and
`python3 tests/fork-check.test.py` exited 0. The changes were then discarded, because the design
seat's commit is this document. Each block below carries what that build found.

**The earlier builds' saved work is not applied by any of the four items.** The change at
`refs/tillr/preserved/run-item-heavy-revert-time-to-verdict-a3` deletes the parity case, which the
ruling below keeps, and calls the recovery from inside the proof's own loop, which pauses the
proof and shares its cleanup guard. Each item states its change in full.

**The criteria were renumbered by the split.** The ruling below was written before it. Its c4 is
this item's c1, and its c6 is this item's c2 for the part that covers c4. The old c5 is
[the priority item](#add-a-lower-cpu-priority-for-worker-processes), the old c3 is
[the order item](#move-the-checks-recent-landings-reach-to-the-front-of-the-proof), and the old
c1 and c2 are [the recovery item](#add-recovery-for-a-failed-check-while-the-proof-continues).

**Ruling, 2026-11-18 (answers the first run's fence conflict):** the first run stopped on c4
because the heavy check `go-test-shards-test` runs `python3 tests/go-test-shards.test.py`, and that
file was outside the item's paths. The run was right to stop. The paths now name
`tests/go-test-shards.test.py`, and nothing else about the item is reopened. These were read on
`2acf66dc4` on 2026-11-18:

- **A proof runs the drain suite three times today, not two.** `GO_SHARD_DIRS` in
  `tools/fork-check.py` already names `tools/tillr-drain`, so the legacy check
  `go-tool/tools-tillr-drain` runs the suite sharded. That run is the one c4 keeps. The other two
  are both in `test_drain_name_parity`, which runs `go test -json ./... -count=1` and then the
  sharded runner over the same module. `tools/fork-check.py` therefore needs no change for c4.
- **The parity case can compare names without running a test.** With `shards.run_shard` replaced
  by a function that records each shard's package and names and returns no events,
  `shards.run` over `tools/tillr-drain` planned 1,171 names with no name twice. They equalled the
  `Test`, `Example` and `Fuzz` names that `go test -json -list . ./...` printed for the module.
  The listing took 5.7 seconds and the planning 6.1. At `332a18704` the same comparison held over
  1,179 names.

Three points settle the change:

1. **The parity case lists and plans, and runs nothing.** `test_drain_name_parity` compares the
   pairs of package and name from the native listing with the pairs the runner hands to
   `run_shard`, as measured above, and asserts that no pair is planned twice. It starts no test:
   the only processes are the listing, `go list`, `go test -c` and each binary's own
   `-test.list`. The file's first line, which says parity exercises the real drain suite, changes
   to match.
2. **Nothing is lost by not running.** The case exists to prove the runner drops no name. That a
   planned name then completes is already enforced in every real run, where `run_shard` fails a
   shard with `missing completion`, and `test_missing_completion_names_shard` covers it.
3. **Only that case changes outside the original paths.** The declaration
   `tools/gate/checks/go-test-shards-test.json` stays as it is, reads included, because a change
   under `tools/tillr-drain/` can still change the names. `tools/go-test-shards.py` is not edited:
   the case replaces `run_shard` from the test. The rest of the file stays as it is, and it has no
   copy under `yolo-packs/`.

For c6, the regression test for c4 is in two parts. The parity case fails if it launches a command
that runs a test, which is any argument beginning `-test.run`, or a `go test` with neither `-list`
nor `-c`. A case in `tests/fork-check.test.py` asserts that a heavy plan holds exactly one command
naming `tools/tillr-drain` as a suite to run. The gate design's "Time to a revert" paragraph still
says the legacy check runs the suite whole; correct it under c6.

**Build order, each step a stop only if it fails.** Do the steps in order in one run. The end of
a step is not an ending, and the commit is made once, at step 4.

1. **The parity case.** In `tests/go-test-shards.test.py`, add `import re` beside the other
   imports, change the first line to say that parity lists the real drain suite and runs none of
   it, and replace the body of `test_drain_name_parity` with the one below. It passed as written
   on 2026-11-18.

```python
    def test_drain_name_parity(self):
        # Lists and plans the real drain suite and runs none of it: the rolling check
        # go-tool for tools/tillr-drain is the proof's one run of that suite.
        module = ROOT / 'tools/tillr-drain'
        listing = subprocess.run(['go', 'test', '-json', '-list', '.', './...'],
                                 cwd=module, capture_output=True, text=True)
        self.assertEqual(listing.returncode, 0, listing.stdout + listing.stderr)
        native = set()
        for event in map(json.loads, listing.stdout.splitlines()):
            name = event.get('Output', '').strip()
            if event.get('Action') == 'output' and re.match(r'^(Test|Example|Fuzz)\w*$', name):
                native.add((event['Package'], name))
        planned = []
        real_run = subprocess.run

        def refuse_a_test_run(command, *args, **kwargs):
            words = [str(word) for word in command]
            self.assertFalse([word for word in words if word.startswith('-test.run')], words)
            if words[:2] == ['go', 'test']:
                self.assertTrue('-list' in words or '-c' in words, words)
            return real_run(command, *args, **kwargs)

        def record(package, cwd, binary, names, *rest):
            planned.extend((package, name) for name in names)
            return []

        with patch.object(shards, 'run_shard', record), \
                patch.object(subprocess, 'run', refuse_a_test_run):
            shards.run(module, 6, shards.load_durations(shards.DEFAULT_DURATIONS),
                       self.root / 'parity', '0')
        self.assertEqual(len(planned), len(set(planned)))
        self.assertEqual(set(planned), native)
```

2. **The plan case.** In `tests/fork-check.test.py`, find the loop over
   `(module.TIER_LANDING, module.TIER_ROLLING)` that ends with
   `assert selected == expected, (directory, tier, selected)`. Directly after that assertion, add
   the lines below, at the same indentation as the assertion.

```python
                # A proof runs the drain's suite once: one rolling command names it, the
                # sharded one. The parity case in tests/go-test-shards.test.py starts no run of it.
                if directory == "tools/tillr-drain" and tier == module.TIER_ROLLING:
                    drain_runs = [check.argv[-1] for check in plan.checks
                                  if "tools/tillr-drain" in check.argv[-1]
                                  and ("go-test-shards.py" in check.argv[-1]
                                       or "go test" in check.argv[-1])]
                    assert len(drain_runs) == 1 and "go-test-shards.py" in drain_runs[0], drain_runs
```

3. **The gate design.** In the "Time to a revert" paragraph of `docs/design/gate-rework.md`,
   replace the statement that the legacy check runs the same suite whole with what is true: the
   legacy check `go-tool` for `tools/tillr-drain` runs the suite sharded, the parity case ran it
   twice more until this change, and it now lists and plans the suite and runs none of it. In
   point 2 of that paragraph, say the duplicate runs are gone. Change the paragraph's last line,
   which says the changes are filed as one roadmap item, to say they are four items that start at
   the same link. Leave points 1 and 3 and the words "proposed, not built" for the later items.
   If `docs/pi/tillr-go-shards-2026-11-17.md` describes the parity case as running the suite,
   correct that sentence too.
4. **Finish.** Run the two commands under **Verify** with `TMPDIR` set to the run's scratch, then
   `uvx vantage-check docs/design/gate-rework.md` and `python3 tests/tillr-docs.test.py`. Make one
   commit holding all of it. `tests/fork-check.test.py` takes about three minutes and prints many
   lines; its exit status is the result. No file under `tools/` changes, so `just pack-sync` has
   nothing to do.

**What does stop this build:** a failing test that needs a file outside the item's paths, or a
step whose named function or file is not in the tree. Report it with the step number, the command
and its output. A report that lists the later items' work as missing describes other items.



### Add a lower CPU priority for worker processes

**Theme:** Heavy runner

**Description:** Every lane's worker runs its own tests while the heavy proof runs the full
suite on the trunk, and both want every processor. The proof is the one every landing waits on,
so it should have the processors first. This item gives each worker's process group a lower CPU
priority at the moment the run starts. The proof is started by the drain and keeps the drain's
priority. Nothing is given a time limit and nothing is stopped: on a loaded machine the workers
slow down, and on an idle one the lower priority costs them nothing.

**Outcomes**

- Give the proof CPU priority over worker tests without stopping either for time.

**Agent Details**

- **Ref:** [the first item of this split](#add-faster-recovery-from-red-landings), which this
  item waits on so that the two do not edit the same paragraph of the gate design at once; point
  3 of the "Time to a revert" paragraph in
  [gate rework Section 6.8](../design/gate-rework.md#68-a-red-heavy-check).
- **Where:** `Exec` in `tools/tillr-run/run/exec.go`, directly after the block that handles a
  failed `cmd.Start()` and before the line `var note string`.
- **Verify:** `python3 tools/go-test-shards.py tools/tillr-run -k 6`, `just pack-sync`,
  `yolo pack lint yolo-packs/tillr`, and `uvx vantage-check` on each document changed.

**Rulings, settled by the design seat on 2026-11-18.** These were built and measured at
`332a18704`, where the test below passed and the `tools/tillr-run` suite passed 220 tests.

- **The priority is lowered for every run, not only while a proof is in progress.** A run outlives
  many proofs, and a process keeps the priority it was started with, so a rule that followed the
  proof would have to find and change processes that already exist. A lower priority only matters
  when processors are short, which is the case the item is about.
- **The mechanism is one call after the process starts.** The run's command already leads its own
  process group. `syscall.Setpriority(syscall.PRIO_PGRP, cmd.Process.Pid, WorkerNiceness)` lowers
  every process in that group, and every process they start later inherits the value. The
  constant `WorkerNiceness` is 10 and is exported from the `run` package for the test. There is no
  setting for it. The command is not wrapped in `nice`, because a wrapper changes what a missing
  adapter reports.
- **A failure to lower the priority does not fail the run.** The error `syscall.ESRCH` means the
  group has already ended and is ignored. Any other error is written as one line to the standard
  error of `tillr-run`, never to the run's log, which holds the adapter's stream.
- **The proof is not touched.** The drain starts the heavy checks, so they keep its priority. No
  file under `tools/tillr-drain/drain/` changes.

**Build order.** Do the steps in order in one run, and make the commit once, at step 4.

1. **The constant and the call.** In `tools/tillr-run/run/exec.go`, add the constant above the
   comment that begins `// Backend is the adapter program`, and add the call directly before
   `var note string`. The file does not import `fmt`, so the line is written with
   `os.Stderr.WriteString`.

```go
// WorkerNiceness is the niceness a run's process group is given, so that worker tests never
// take the processor from the heavy proof, which the drain runs at its own niceness.
const WorkerNiceness = 10
```

```go
	// A worker and everything it starts yield the processor to the trunk's proof. The
	// group is lowered once, here: a process it starts later inherits the value. A group
	// that has already ended has nothing to lower.
	if err := syscall.Setpriority(syscall.PRIO_PGRP, cmd.Process.Pid, WorkerNiceness); err != nil && err != syscall.ESRCH {
		os.Stderr.WriteString("tillr-run: the worker's CPU priority was not lowered: " + err.Error() + "\n")
	}
```

2. **The test.** Add `tools/tillr-run/run/priority_test.go` in package `run_test`, named
   `TestExecLowersTheWorkersCPUPriority`. It opens a fixture store the way
   `TestExecRequestEnvironment` in `environment_test.go` does. It reads its own niceness by
   running `nice` with no arguments, and expects that value plus `run.WorkerNiceness`, capped at
   19; when the two are equal it skips, because the process is already at the lowest priority. It
   runs `run.Exec` with `TimeoutMS: 0` and the command `/bin/sh -c` over the script below, where
   `OWN` is the test's own niceness and `OUT` is a file in the test's scratch, and asserts that
   the file holds the expected number. The inner `sh -c nice` is a process started after the
   group was lowered, so it reports what a worker's test inherits.

```sh
i=0; while [ "$(nice)" = "OWN" ] && [ $i -lt 100 ]; do sleep 0.05; i=$((i+1)); done; sh -c nice > OUT
```

3. **The documents.** In the run README, add a short section after "Worker child cleanup" that
   states the rule and the constant. In the drain README's section on after-merge heavy runs, add
   one sentence that worker processes run at a lower CPU priority than the proof and that nothing
   is stopped for time. In the gate design, rewrite point 3 of the "Time to a revert" paragraph to
   say what was built: a niceness of 10 on the worker's process group, for every run, with no
   control group weight and no time limit.
4. **Finish.** Run `just pack-sync`, then the commands under **Verify**, and make one commit
   holding all of it.



### Move the checks recent landings reach to the front of the proof

**Theme:** Heavy runner

**Description:** A proof runs its declaration checks one after another in the order of their
names and then starts the legacy program, whatever the landings since the last green commit
changed. A landing that breaks a check late in that order is found late. This item starts the
checks whose declared reads those landings changed before the others. The same checks run, and
only their order changes.

**Outcomes**

- Run affected checks first.

**Agent Details**

- **Ref:** [the priority item](#add-a-lower-cpu-priority-for-worker-processes), which this item
  waits on so that the documents change in one order; point 2 of the "Time to a revert" paragraph
  in [gate rework Section 6.8](../design/gate-rework.md#68-a-red-heavy-check).
- **Where:** the branch of `JudgeHeavy` in `tools/tillr-drain/drain/heavy_steps.go` that runs when
  `only` is empty. `legacyCheckWatches` in `legacy_watches.go` already answers each legacy check's
  watched paths without running a check.
- **Verify:** `go test ./drain -run 'TestHeavyProcess'` in `tools/tillr-drain`, then
  `python3 tools/go-test-shards.py tools/tillr-drain -k 6`, `just pack-sync`,
  `yolo pack lint yolo-packs/tillr`, and `uvx vantage-check` on each document changed.

**Rulings, settled by the design seat on 2026-11-18.** These were built at `332a18704`, where the
test below passed in its four cases.

- **The changed paths are the difference between the green pointer and the commit under proof.**
  They are read in the proof's checkout with
  `git diff --name-only --no-renames -z <green> HEAD --`. With no green pointer there are no
  changed paths and the order is today's.
- **A check is affected when a changed path equals one of its reads or lies under a read that
  ends in a slash.** That is the rule `legacyWatchFiles` already applies to a watch's path.
- **The order has three groups.** First the affected declaration checks, in name order. Then the
  legacy program, when a changed path matches a watch of any legacy check. Then the declaration
  checks that are not affected, in name order. When no legacy watch matches, the legacy program
  runs last, as it does today.
- **The order inside the legacy program does not change.** The program runs its checks side by
  side and starts the longest first, and `tools/fork-check.py` is not edited.
- **The watches are read only when they can change the order.** `legacyCheckWatches` runs
  `python3 tools/fork-check.py --registry-json`, which took 1.5 seconds on 2026-11-18. It is called
  only when there are changed paths and at least one declaration check is not affected. A tree
  with no `tools/fork-check.py` answers no watches, and the legacy program then runs last.
- **The returned list keeps the order the checks ran in.** With nothing affected, that is the
  declaration checks and then the legacy checks, as today.

**Build order.** Do the steps in order in one run, and make the commit once, at step 4.

1. **Two helpers.** Add them to `heavy_steps.go` above `runHeavyLegacy`.

```go
// heavyChangedPaths answers the paths the landings since the green pointer changed, or
// none when there is no pointer yet.
func heavyChangedPaths(ctx context.Context, dir string) ([]string, error) {
	green, err := heavyGreen(ctx, dir)
	if err != nil || green == "" {
		return nil, err
	}
	raw, err := exec.CommandContext(ctx, "git", "-C", dir, "diff", "--name-only", "--no-renames", "-z", green, "HEAD", "--").Output()
	if err != nil {
		return nil, Externalf("reading the paths changed since %s: %v", green, err)
	}
	return strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00"), nil
}

// heavyReadsChanged answers whether any changed path is one of reads or under one that
// names a directory.
func heavyReadsChanged(reads, paths []string) bool {
	for _, read := range reads {
		for _, path := range paths {
			if path != "" && (path == read || strings.HasSuffix(read, "/") && strings.HasPrefix(path, read)) {
				return true
			}
		}
	}
	return false
}
```

2. **The order.** In `JudgeHeavy`, the branch for an empty `only` sorts `ids`, runs a loop over
   them and then calls `runHeavyLegacy` and reports its failing checks. Keep `sort.Strings(ids)`
   and put the code below directly after it, in place of `result := []HeavyCheck{}` and the
   `for _, id := range ids {` line. The body of the loop stays as it is. After the loop, replace
   the call to `runHeavyLegacy`, the lines that report its failing checks and the `return` that
   follows them with the last five lines below, because `runLegacy` now holds that code.

```go
		paths, err := heavyChangedPaths(ctx, dir)
		if err != nil {
			return nil, err
		}
		// Checks the landings since the green pointer reach go first: declarations whose
		// reads they changed, then the legacy program when they changed one of its watches.
		var first, rest []string
		for _, id := range ids {
			if heavyReadsChanged(declared[id].Reads, paths) {
				first = append(first, id)
			} else {
				rest = append(rest, id)
			}
		}
		legacyFirst := false
		if len(paths) > 0 && len(rest) > 0 {
			watches, err := legacyCheckWatches(ctx, dir)
			if err != nil {
				return nil, err
			}
			for _, reads := range watches {
				if heavyReadsChanged(reads, paths) {
					legacyFirst = true
					break
				}
			}
		}
		result := []HeavyCheck{}
		var legacy []HeavyCheck
		runLegacy := func() error {
			var err error
			legacy, err = s.runHeavyLegacy(ctx, dir, "")
			if err == nil {
				failed := []string{}
				for _, check := range legacy {
					if check.Result == "fail" {
						failed = append(failed, check.CheckID)
					}
				}
				if len(failed) == 0 {
					failed = append(failed, "none")
				}
				heavyProgressReport(ctx, "loop: heavy legacy program judged %d checks; failing: %s\n", len(legacy), strings.Join(failed, ", "))
			}
			return err
		}
		for position, id := range append(first, rest...) {
			if legacyFirst && position == len(first) {
				if err := runLegacy(); err != nil {
					return nil, err
				}
			}
```

```go
		if !legacyFirst {
			if err := runLegacy(); err != nil {
				return append(result, legacy...), err
			}
		}
		return append(result, legacy...), nil
```

3. **The test.** Add `TestHeavyProcessRunsAffectedChecksFirst` in a new file
   `tools/tillr-drain/drain/heavy_order_test.go`. It builds the gate binary and the repository
   the way `TestHeavyProcessJudgeIncludesDeclarationsAndLegacyAndReplaysOneCheck` in
   `heavy_steps_test.go` does. The tree holds two heavy tree declarations with `"remember":false`:
   `a-unaffected` reads `unrelated/`, and `z-affected` reads `late-input`. Each one's command
   appends its own id to an order file outside the tree. The tree also holds a
   `tools/fork-check.py` of one line, which prints
   `{"declarations":[{"id":"legacy","watches":[{"path":"legacy-input"}]}]}`. The rolling program
   is a script from the `executable` helper that appends `legacy` to the order file and writes a
   span file with one check `legacy` of status `PASS`. The test commits that tree, points the green
   pointer at it with `heavyGit(t, root, "update-ref", GreenRef, base)`, commits one more change,
   runs the runner and asserts a green run and the order file's content. It has four cases:

   | The later commit adds | Order |
   | :--- | :--- |
   | `late-input` | `z-affected a-unaffected legacy` |
   | `legacy-input` | `legacy a-unaffected z-affected` |
   | both files | `z-affected legacy a-unaffected` |
   | a file neither reads | `a-unaffected z-affected legacy` |

4. **Finish.** In the drain README's section on after-merge heavy runs, state the three groups.
   In the gate design, rewrite the first sentence of point 2 of the "Time to a revert" paragraph to
   say what was built. Run `just pack-sync`, then the commands under **Verify**, and make one
   commit holding all of it.

**What does stop this build:** a failing test that needs a file outside the item's paths, or a
step whose named function is not in the tree. An existing test that asserts the old order is
rewritten to the three groups, and is not a reason to stop.



### Add recovery for a failed check while the proof continues

**Theme:** Heavy runner

**Description:** A revert can start only once the heavy run has named a failed check, and today
that is when the whole run ends. The landing that broke the trunk stays there, and every later
landing waits behind it, for as long as the slowest check takes. This item starts the culprit
search and the automatic revert for a failed check when that check ends, in a second checkout,
while the proof runs its remaining checks. The green pointer still moves only on a run in which
every check passed.

**Outcomes**

- Start the culprit search and revert as soon as a check fails, while the other checks continue.

**Agent Details**

- **Ref:** [the order item](#move-the-checks-recent-landings-reach-to-the-front-of-the-proof),
  which this item waits on because both change `JudgeHeavy`; point 1 of the "Time to a revert"
  paragraph in [gate rework Section 6.8](../design/gate-rework.md#68-a-red-heavy-check); the
  owner's ruling in [`OQ-GP1`](../design/tillr-graph-is-the-path.md#OQ-GP1).
- **Where:** `judgeTip`, `revert` and `Run` in `tools/tillr-drain/drain/heavy.go`, `JudgeHeavy`
  and `runHeavyLegacy` in `heavy_steps.go`, the guard check in `restart_rolling.go`, and one new
  file, `heavy_early.go`.
- **Verify:** `go test ./drain -race -run 'Heavy|Revert|Restart|Rolling'` in `tools/tillr-drain`,
  then `python3 tools/go-test-shards.py tools/tillr-drain -k 6`, `just pack-sync`,
  `yolo pack lint yolo-packs/tillr`, and `uvx vantage-check` on each document changed.

**Rulings, settled by the design seat on 2026-11-18.** The last build named three things it could
not settle. Each is ruled here from a trial build of this change, made by the design seat in a
lane whose trunk was at `332a18704`. That sha is the trunk commit the trial was built on. It does
not hold the trial: the trial's changes were never committed, and were discarded once this block
was written. No commit, branch or ref holds `heavy_early.go` or the three tests of step 5, and
`rg 'heavyCheckEnded|heavyPendingGuard' tools/tillr-drain` returns nothing at `743d8cfc7`. This
build writes them from the steps below. In the trial, the three tests passed, the command under
**Verify** with `-race` passed in 515 seconds, and the whole drain suite passed 2,086 tests.
Those figures record how the rulings were reached. They are not an input to this build and not
something to find before starting: the criteria are met by this build's own run of **Verify**.

- **Recovery runs beside the proof, one failed check at a time.** The proof's judge reports each
  check as it ends. A failed one joins a queue, and one goroutine takes the queue in order: the
  culprit search, then the offer to revert. The proof never waits for it. Two recoveries never
  run at once, so two reverts never race each other.
- **Recovery has its own checkout and its own cleanup guard.** Its checkout is the proof's path
  with `-replay` added. Its guard is `heavy.replay-cleanup-pending`, beside the proof's
  `heavy.cleanup-pending`. Both are needed and both were measured. With the proof's checkout, a
  replay removed the tree the proof was running in, and the proof's check ended with
  `getwd: no such file or directory`. With the proof's guard, the replay's supervisor could not
  create a guard that already existed, and the run ended after 31 seconds with
  `cleanup not acknowledged`. A guard of either name left by a killed runner holds the next run.
- **A second failure after an earlier revert has landed is offered on the run's tip, as today.**
  This is not a new case. The loop in `revert` already offers each landing in turn with
  `run.TipSHA` after the reverts before it have landed, and the lander replays the change onto
  the trunk as it then stands. Recovery passes the same tip.
- **A landing reverted earlier in the same run is not offered again.** When a later failed check
  is traced to it, the check records the revert that already landed and gets no fixer. The
  returned item's ending names the checks of the first offer only. The next proof judges the
  later check on the new tip.
- **A landing whose revert was refused can be offered again for a later check.** A refusal
  changed nothing on the trunk.
- **Legacy verdicts need no observer.** The last build reported that legacy verdicts arrive
  together. They do, at the program's first failed check: `run_checks` in `tools/fork-check.py`
  stops the pass on a red check, ends the checks in flight and names the rest not-run. So the
  judge reports the legacy checks when the program ends, which is before any declaration check
  that runs after it. `heavy_legacy_progress.py` and `tools/fork-check.py` are not edited.
- **Only a check that failed outright is recovered early.** A stalled check is retried at the end
  of the run, as today, and a second stall names the tip there.
- **Fixers are filed when the run ends, as today.** Recovery searches and offers the revert. A
  check whose revert was refused keeps its fixer, filed with the others after the last check.
- **The pointer rule does not change.** A failed check makes the run red whatever recovery did,
  and the pointer moves only when every check of one run passed. The next run proves the trunk
  as the revert left it.
- **A stop reaches both.** Recovery uses the run's context. `judgeTip` waits for the queue to
  empty before it answers, on every path, so the runner's lock is never released while a replay
  is running. A stop or a lost cleanup in recovery ends the run with that error.

**Build order.** Do the steps in order in one run, and make the commit once, at step 6. Every
function, comment and line that a step names was read in the tree at `743d8cfc7`, after
[the order item](#move-the-checks-recent-landings-reach-to-the-front-of-the-proof) landed there,
and each is present.

1. **The new file.** Create `tools/tillr-drain/drain/heavy_early.go` with the content below.

```go
package drain

import (
	"context"
	"os"
	"path/filepath"
	"sync"
)

const heavyReplayGuard = "heavy.replay-cleanup-pending"

// heavyPendingGuard answers the cleanup guard a killed runner left in dir, the proof's
// or the recovery's, or "" when there is none.
func heavyPendingGuard(dir string) string {
	for _, name := range []string{heavyCleanupGuard, heavyReplayGuard} {
		guard := filepath.Join(dir, name)
		if _, err := os.Stat(guard); err == nil || !os.IsNotExist(err) {
			return guard
		}
	}
	return ""
}

type heavyCheckEndedKey struct{}

// heavyCheckEnded reports one check of a whole-tier judge the moment it ends, so a red
// one starts its recovery while the judge runs the rest.
func heavyCheckEnded(ctx context.Context, check HeavyCheck) {
	if report, ok := ctx.Value(heavyCheckEndedKey{}).(func(HeavyCheck)); ok {
		report(check)
	}
}

// heavyAttributed is what recovery found for one failing check before the run ended.
type heavyAttributed struct {
	check             HeavyCheck
	against           string
	bisected, offered bool
	stillRed          bool
}

// heavyEarly recovers failing checks one at a time, in the order they ended, in a
// checkout of its own beside the proof's.
type heavyEarly struct {
	ctx      context.Context
	runner   HeavyRunner
	run      HeavyRun
	mu       sync.Mutex
	cond     *sync.Cond
	queue    []HeavyCheck
	closed   bool
	done     chan struct{}
	err      error
	handled  map[string]heavyAttributed
	reverted map[string]string
}

func (r HeavyRunner) startEarly(ctx context.Context, run HeavyRun) *heavyEarly {
	e := &heavyEarly{ctx: ctx, runner: r, run: run, done: make(chan struct{}), handled: map[string]heavyAttributed{}, reverted: map[string]string{}}
	e.cond = sync.NewCond(&e.mu)
	e.runner.Work = r.Work + "-replay"
	if own, ok := ctx.Value(heavyOwnershipKey{}).(heavyOwnership); ok {
		own.Guard = filepath.Join(filepath.Dir(own.Guard), heavyReplayGuard)
		e.ctx = context.WithValue(ctx, heavyOwnershipKey{}, own)
	}
	go e.loop()
	return e
}

func (e *heavyEarly) offer(check HeavyCheck) {
	if check.Result != "fail" || check.FirstRedLanding != "" {
		return
	}
	e.mu.Lock()
	if !e.closed {
		e.queue = append(e.queue, check)
		e.cond.Signal()
	}
	e.mu.Unlock()
}

func (e *heavyEarly) loop() {
	defer close(e.done)
	for {
		e.mu.Lock()
		for len(e.queue) == 0 && !e.closed {
			e.cond.Wait()
		}
		if len(e.queue) == 0 {
			e.mu.Unlock()
			return
		}
		check := e.queue[0]
		e.queue = e.queue[1:]
		e.mu.Unlock()
		if e.err == nil && e.ctx.Err() == nil {
			e.err = e.recover(check)
		}
	}
}

// wait ends the queue and answers once every check offered has been handled.
func (e *heavyEarly) wait() error {
	e.mu.Lock()
	e.closed = true
	e.cond.Broadcast()
	e.mu.Unlock()
	<-e.done
	return e.err
}

func (e *heavyEarly) recover(check HeavyCheck) error {
	against, bisected, err := e.runner.attribute(e.ctx, e.run, &check)
	if err != nil {
		return err
	}
	a := heavyAttributed{against: against, bisected: bisected}
	if bisected && e.runner.Revert != nil {
		a.offered = true
		if by := e.reverted[against]; by != "" {
			check.RevertedBy = by
		} else {
			out, err := e.runner.offerRevert(e.ctx, e.run.TipSHA, against, []HeavyCheck{check})
			if err != nil {
				return err
			}
			check.RevertedBy, check.RevertRefused, a.stillRed = out.Reverted, out.Refused, out.StillRed
			if out.Reverted != "" {
				e.reverted[against] = out.Reverted
			}
		}
	}
	a.check = check
	e.handled[check.CheckID] = a
	return nil
}
```

2. **`heavy.go`, four changes.** The file does not compile until all four are made. The code
   below is indented with spaces where it sits inside this list, so run `gofmt -w` on the file
   afterwards.
   - **`attribute`.** In `judgeTip`, the loop over `run.Checks` that follows the comment about
     `bisected` holds, for each failed check, the code from the comment
     `// The sha a fixer is filed against` down to the `if` that appends
     `"; attribution unavailable; red batch range "` to the detail. Move that code into a new
     method with the signature below. In the moved code, `against := check.FirstRedLanding`
     becomes `against = check.FirstRedLanding`, `return run, err` becomes
     `return "", false, err`, and `bisected[red] = true` becomes `found = true`. The method ends
     with `return against, found, nil`.

     ```go
     func (r HeavyRunner) attribute(ctx context.Context, run HeavyRun, check *HeavyCheck) (against string, found bool, err error)
     ```

   - **The loop.** In its place in the loop, directly after the `continue` for a check that did
     not fail, put the code below. The lines that add the check to `order` and `byLanding` stay
     after it.

     ```go
     against := ""
     if a, ok := early.handled[check.CheckID]; ok && check.FirstRedLanding == "" {
         // Recovery already ran for this check while the proof continued.
         check.FirstRedLanding, check.GateFault, check.Detail = a.check.FirstRedLanding, a.check.GateFault, a.check.Detail
         check.RevertedBy, check.RevertRefused = a.check.RevertedBy, a.check.RevertRefused
         against = a.against
         switch {
         case check.RevertedBy != "":
             continue
         case a.offered && a.stillRed && against != run.TipSHA:
             against = run.TipSHA
         case !a.offered && a.bisected:
             bisected[against] = true
         }
     } else {
         var found bool
         var err error
         against, found, err = r.attribute(ctx, run, check)
         if err != nil {
             return run, err
         }
         if found {
             bisected[against] = true
         }
     }
     ```

   - **`offerRevert` and `revert`.** Add the method below above `one`. In `revert`, add a last
     parameter `reverted map[string]string`, and replace the call to `r.Revert` together with the
     two `if` blocks after it, which turn an error into a refusal and fill an empty answer, with
     the second block below. The call in `judgeTip` becomes
     `r.revert(ctx, &run, order, byLanding, bisected, early.reverted)`.

     ```go
     // offerRevert asks Revert about one landing. A revert that failed for its own reasons is
     // a refusal; only a stop or a lost cleanup is an error.
     func (r HeavyRunner) offerRevert(ctx context.Context, tip, sha string, checks []HeavyCheck) (RevertOutcome, error) {
         out, err := r.Revert(ctx, tip, sha, checks, r.one)
         if err != nil {
             if ctx.Err() != nil || cleanupLost(err) {
                 return out, err
             }
             out = RevertOutcome{Refused: "the revert could not be attempted: " + err.Error()}
         }
         if out.Reverted == "" && out.Refused == "" {
             out.Refused = "the revert answered neither a landing nor a reason"
         }
         return out, nil
     }
     ```

     ```go
     out := RevertOutcome{Reverted: reverted[sha]}
     if out.Reverted == "" {
         var err error
         if out, err = r.offerRevert(ctx, run.TipSHA, sha, byLanding[sha]); err != nil {
             return nil, err
         }
     }
     ```

   - **The start, the wait and the guard.** At the top of `judgeTip`, add
     `early := r.startEarly(ctx, run)`. The whole-tier call becomes
     `r.Judge(context.WithValue(ctx, heavyCheckEndedKey{}, early.offer), dir, "")`; the stall
     retry's call keeps the plain `ctx`. Directly after the `inTree` call returns and before its
     error is tested, add the first block below. In `Run`, replace the two lines that build
     `guard` and test it with `os.Stat` by the one line in the second block; the body of that
     `if` is unchanged.

     ```go
     // Recovery owns a second checkout: it ends before the run does, whatever the proof answered.
     if earlyErr := early.wait(); err == nil {
         err = earlyErr
     }
     ```

     ```go
     if guard := heavyPendingGuard(dir); guard != "" {
     ```

3. **`restart_rolling.go`.** The function there that takes the heavy lock tests the proof's
   guard with `os.Stat` and refuses with "rolling cleanup not acknowledged". Test
   `heavyPendingGuard(dir)` instead and name the guard it answers in the refusal. Keep the
   variable `guard` as the proof's guard path, because the ownership value built below it still
   uses that path.
4. **`heavy_steps.go`, three lines.** In the loop over declaration checks, directly after the
   `heavyProgressReport` line that prints `loop: heavy %s %s in %s`, add
   `heavyCheckEnded(ctx, checks[0])`. In `runLegacy`, the closure inside `JudgeHeavy`, directly
   after the `heavyProgressReport`
   line that prints the failing checks, add a loop that calls `heavyCheckEnded(ctx, check)` for
   each check in `legacy`. In `runHeavyLegacy`, directly after the line that sets `spans`, give a
   replay a span file of its own, so that it can never remove the proof's:

```go
	if only != "" {
		// A replay can run beside the proof's own program, whose evidence this must not remove.
		spans = filepath.Join(s.LaneRoot, ".rolling", "heavy.replay.spans.json")
	}
```

5. **Three tests.** Each fails without the change.
   - `TestHeavyRedCheckIsRevertedWhileTheProofContinues`, in a new file `heavy_early_test.go`,
     uses `newRevertHarness` from `heavy_revert_test.go`. Its judge answers two checks. `quick`
     fails while the file `a` exists in the checkout. `slow` always passes. Asked for one check by
     name, the judge answers that check alone. Asked for the whole tier, it calls
     `heavyCheckEnded(ctx, quick)`, and on its first whole-tier call it then polls `h.reverts()`
     every 10 milliseconds, for at most a minute, until one revert with the result `reverted` is
     on record, and asserts that the green pointer is still the base. It then calls
     `heavyCheckEnded(ctx, slow)` and answers both checks. The test lands `item-broke` with the
     file `a` and `item-later` with the file `c`, calls `h.proof()`, and asserts that the revert
     was seen before `slow` ended, that there is one revert and it names the first landing, that
     the green pointer equals the trunk and the revert, and that no fixer was filed.
   - `TestHeavyProcessReplaysAFailedCheckWhileTheNextOneRuns`, in `heavy_early_process_test.go`,
     uses the real gate binary and `ProcessSteps`, as the order item's test does. The file `value`
     holds `good` at the base, where the green pointer is, and `bad` at the tip. The declaration
     `a-quick` passes when `value` is not `bad`. The declaration `b-slow` waits up to 30 seconds
     for a marker file outside the tree and passes when the marker exists. The runner's `Revert`
     is a function that records the landing and the check it was offered, calls its `replay`
     argument for `a-quick` on the base and expects a pass with no error, writes the marker, and
     answers a refusal. The test asserts one offer, of the tip for `a-quick`, a red run of two
     checks whose second passed, and a green pointer still on the base. With the proof's guard or
     the proof's checkout used for recovery, this test fails with the errors quoted in the
     rulings.
   - `TestHeavyLegacyFailureRecoversWhileALaterDeclarationRuns`, in
     `heavy_early_legacy_test.go`, has one declaration `slow`, which reads `unrelated/` and waits
     for the marker as above. Its `tools/fork-check.py` is one line that prints a registry with
     one declaration, `legacy`, of kind `fixture` and label `legacy`, watching `value`. The
     rolling program writes one span for `legacy` with that kind and label and the status `FAIL`,
     and exits 1. The replay program exits 0 when `value` holds `good`. The base holds `good` and
     the tip `bad`, so the legacy program runs first by the order item's rule. The `Revert`
     function records its offer, writes the marker and refuses. The test asserts one offer, of the
     tip for `legacy` with the read `value`, a red run, an unmoved pointer, and that `slow` passed.
6. **Finish.** In the drain README's section on after-merge heavy runs, say that a failed check
   is searched and offered for revert when it ends, in the checkout `worktree-replay` and under
   the guard `heavy.replay-cleanup-pending`, that the pointer rule is unchanged, and that either
   guard holds the next run; the sentence there that names `heavy.cleanup-pending` gains the
   second name. In the gate design's "Time to a revert" paragraph, rewrite the opening sentence,
   which says a revert starts "at the end of the whole run", and point 1, so that both say what
   this build made: a failed check is searched and offered for revert when it ends. Points 2 and
   3 stay as they are. Run `just pack-sync`, then the commands under **Verify**, and make one
   commit holding all of it.

**What does stop this build:** a failing test that needs a file outside the item's paths, a step
whose named function is not in the tree, or a test under **Verify** that fails with the change
made exactly as written. Report it with the step number, the command and its output. An existing
test that fails because it calls `revert` with the old parameters is updated, and is not a reason
to stop. Neither is the absence of the three tests or of `heavy_early.go` from the tree or from
any commit: steps 1 and 5 create them.



## Filed 2026-11-15: the observatory redesign's remaining slices

These are slices 3 to 10 of [the observatory redesign](../design/observatory-redesign.md#10-build-order),
filed here because that document still carries one open question. Each block's criteria are the
slice's own row in that table; the design is the reference for every screen, token and route a
criterion names.

### The observatory has a new shell, its visual tokens, one link component and Home



### The observatory has Items and Passes screens



### An item and a run each have a page, and the run page draws its spans on one time axis



### A run's spans export to standard trace formats

Built: this landed in commit `6fb8f2f0b`.

### The view serves landings, and the observatory has a Trunk screen

Built: this landed in commit `8d34f0d8a`.

### The view serves per-check and per-item performance

Built: this landed in commit `1de29062c`.

### The observatory has Performance and Check screens



### Every pass and the loop's heartbeat are recorded

Built: this landed in commit `4961df4206`.

### A finished run says how it ended

Built: this landed in commit `cd5ce63bd`.

## Filed 2026-11-15: a worker's refusal reaches a seat that can answer it

On 2026-11-15 a worker stopped in 23 seconds without editing because one criterion contradicted
itself. That was the right act, and the repair was then done by hand. The designs already say
where it should have gone: [the escalation ladder](../design/escalation-ladder.md#4-routing-as-a-recorded-transition)
(the worker records the conflict and it is routed to the cheapest seat with authority to answer)
and [the judge and lifecycle design](../design/tillr-judge-and-lifecycle.md#41-attempt-outcomes-are-not-criterion-verdicts-d-jl5)
(a `RESULT: blocked` run blocks the item on a named seat and writes no verdict).

### A run that reports blocked is routed to the design seat with its conflict as the brief

Built: this landed in commit `ed5cb51a2e`.

## Filed 2026-11-15: a check reads what the tree pins, never what a machine happens to hold

On 2026-11-15 the repo gate failed on a fresh machine because local delivery branches were
missing. The check after merging also could not judge because shared fork checkouts were absent.
Both checks depended on what that machine happened to hold rather than on the content recorded
in the repository.

### Isolate fork checks from local machine state

**Theme:** Repo Gate

Forks are upstream tools that we deliver with our own changes. A fork check should produce the
same answer on a fresh machine as on one that has been building those tools for months. To make
that possible, the repository must record the exact commit to check, and exporting a patch series
must update that record along with the delivered changes. Checks can reuse a local cache of
commits, but they must fetch the recorded commit when it is missing instead of choosing whatever
version is already available. If the fetch cannot succeed, the gate must explain which input is
missing rather than report that the change failed verification.

**Outcomes:**

- Record the exact fork commit with each delivery and use it on every machine.
- Reuse cached commits and fetch only when the required commit is missing.
- Report a missing input separately from a failed check, with enough detail to retrieve it.

**Agent Details:**

- **Ref:** [The gate rework](../design/gate-rework.md) defines the gate's response to missing
  inputs. The 2026-11-15 `structural` failure involved seven absent local `patch/*` branches;
  the after-merge check lacked shared fork checkouts.
- **Where:** [Gate checks](../../tools/gate/), [fork checker](../../tools/fork-check.py),
  [fork export command](../../bin/pi-forks), and [delivery packs](../../yolo-packs/).
  Criteria c2 and c3 were restored on 2026-11-17 after commit `bf87a76d8b` replaced them with
  landing and boundary-check requirements. Those requirements belong to
  [routing a landing around the owner's uncommitted edits](#route-a-landing-around-the-owners-uncommitted-edits),
  which carries its own c2 and c3. Do not change the lander or drain for this item.
- **Publishing (2026-11-18, answers the fourth run's report of blocked):** The recorded commit is
  what a check reads. It does not constrain publication, so do not change
  [the publisher](../../bin/patch-branches-publish) or [the push hook](../../hooks/pre-push);
  both stay outside this item's paths. In c1, "must be at" states what a check judges: exporting
  moves the local branch and the record in one change, so the two agree after every export. A
  branch moved any other way is not this item's concern. What the remote holds is still proved
  by the existing `bin/pi-forks patch-branch published` command, which this item leaves as it is.
  A publisher that refused a branch differing from the record would refuse every push made
  between a lane's export and that lane's landing, because the branch moves at export and the
  record reaches the trunk only when the lane lands.
- **Existing test cases (2026-11-18, answers the sixth run's report of blocked):** The fork export
  tests run the scenario files under [their cases directory](../../tests/pi-forks/), so those
  files, [their shared library](../../tests/pi-forks/lib.sh),
  [the fork list](../../bin/pi-forks.list) and
  [the static series test](../../tests/pi-patch-series.test.sh) are now inside this item's paths.
  A case that asserts the old contract is rewritten to assert c2, not kept and not deleted. The
  one known today is the last block of
  [the candidate-check case](../../tests/pi-forks/cases/25-candidate-check.sh), which expects
  `candidate --check` to turn stale when the cache's local `patch/chkconv` branch moves. Under
  c2 the candidate's identity for a fork delivered from a patch branch is the recorded commit, so
  that block becomes three assertions: moving the record makes the candidate stale and the
  message names both commits; moving the local branch while the record stays changes nothing;
  deleting the local branch changes nothing. Change any other case the same way when it fails
  for the same reason, and leave every case about publishing as it is.
- **Gate runner tests (2026-11-18, answers the eighth run's report of blocked):** Two tests in
  [the gate runner's needs test](../../tools/tillr-gate/needs_test.go) assert the old contract, so
  that file and [the runner's README](../../tools/tillr-gate/README.md) are now inside this item's
  paths. Change no other file under `tools/tillr-gate/`: the runner's handling of a `ref:` need
  stays as it is, and the other tests in that file, which use `patch/*` names only as fixtures for
  that handling, stay unchanged. Rewrite the two tests to assert c2 and c4; do not delete them.
  - `TestPatchRefProgramJudgesPiBranchContents` builds a fixture repository that records a commit
    and a fixture remote that holds it. The check judges the build script at the recorded commit.
    Moving the local `patch/pi` branch changes no answer, and neither does deleting it. A record
    whose commit the fixture remote lacks exits with the `could not judge` status and names the
    commit and the remote.
  - `TestFreshCloneStructuralWithoutPatchBranches` keeps its clone without patch branches and its
    run of the structural suite. In place of eight `unavailable` outcomes it asserts that none of
    the eight fork declarations names a `ref:refs/heads/patch/` need and that each one reads its
    fork's record. It must not contact the network, so it does not run the eight checks against
    the real remotes.
  - In the README, the section on optional local refs keeps its description of `ref:` needs. Its
    last paragraph, which says the eight declarations judge local branches, is rewritten to say
    they judge the recorded commit.
  - `tools/tillr-gate/` is also delivered as a copy under `yolo-packs/tillr/`. Run
    `just pack-sync` after editing and commit the regenerated copy in the same commit; do not
    edit the copy by hand.
- **Verify:** Use [fork-check tests](../../tests/fork-check.test.py) and
  [fork export tests](../../tests/pi-forks.test.sh) to check that exporting updates the recorded
  commit, deleting all local `patch/*` branches changes no result, and empty, current, and
  stale-only caches produce the same answer. Check that a cache hit triggers no refresh and
  that a fetch refusal names the missing commit and remote and returns `could not judge`.

<!-- tillr: item id="item-gate-pinned-fork-inputs" paths="tools/gate/,tools/fork-check.py,tests/fork-check.test.py,bin/pi-forks,bin/pi-forks.list,tests/pi-forks.test.sh,tests/pi-forks/,tests/pi-patch-series.test.sh,tools/tillr-gate/needs_test.go,tools/tillr-gate/README.md,yolo-packs/,docs/" blocked_by="" prose="2" -->

- **c1:** Each fork a pack delivers from a patch branch records, in the tree, the commit that branch must be at, and exporting a series updates that record in the same change. The record is the input a check reads; publishing a branch is not changed.
- **c2:** A check that needs a fork reads that commit and no local branch: with every local `patch/*` branch deleted, the check gives the same answer.
- **c3:** The commit is fetched into a cache directory when the cache lacks it and is never refreshed otherwise, so a cache holding older or newer commits changes no answer; a test runs the check against a cache that is empty, one that is current and one that holds only stale commits.
- **c4:** A machine that cannot fetch the commit is told which commit and which remote it lacks, and the check answers `could not judge`, not `failed`.

## Filed 2026-11-15: the documents are part of the world the loop converges on

[The drain's successor design](../design/tillr-drain-successor.md#21-the-function-and-its-inputs)
makes each pass a function of durable facts. Two facts are still read once, when the loop starts:
the item blocks in the documents, and the open questions. A block committed while the loop runs is
not seen until someone runs the sync by hand, which happened five times on 2026-11-15.

### The loop reads the documents again whenever the trunk moves

Built: this landed in commit `ce51abce35`.

## Filed 2026-11-15: the three gate rulings no slice builds

[The gate rework](../design/gate-rework.md#decision-ledger) settled a red cap, a lane base and an
exception for the gate's own runner on 2026-11-15, and its build order has no slice for any of
them. These three items are those slices. Each reads its setting from the policy file the
`item-gate-policy` item creates.

### Landing stops at the red cap and says which check is red



### A new lane starts from the last green commit



### A change to the gate's own runner lands only after the runner's suite passes

A change to the gate's own runner can break the gate for every lane, so the runner's suite proves such a change before it lands rather than after. Criterion c3 was aligned on 2026-11-17 with the owner's ruling on time limits ([`D-GR15`](../design/gate-rework.md#decision-ledger), [Section 6.5](../design/gate-rework.md#65-ten-seconds-is-a-failing-test)): a measured time is counted and reported and fails nothing.



## Filed 2026-11-17: stall detection where the kernel counter is refused

### Add stall detection without the kernel counter

**Start here (2026-11-18, answers the seventeenth run's report of blocked):** read this block to
its end before running a command; the rulings below are the instructions, the last one first. The
change was built, landed as `36e74e541` and was reverted by `e30659c1e`, because one test outside
the item's paths expected a silent check's log to be empty. The kept work the brief names is that
same change and holds no tracing supervisor: it is the change to commit, with one test added. Do
these in order:

1. Apply the kept work's `tools/`, `tests/` and `docs/` paths. Do not redesign or rewrite it.
   `rg -n -i 'ptrace' tools/tillr-gate tools/fork-check.py` must return no line.
2. Change the one assertion in `tools/gate/checks/change_checks_test.py` as the last ruling says,
   and run `python3 tools/gate/checks/change_checks_test.py` to its end.
3. Run `python3 tools/go-test-shards.py tools/tillr-gate -k 6`, then
   `python3 tests/fork-check.test.py`. Give each command a limit of 3600 seconds, on its own, not
   chained with another. A command that was cut off is no result: run it again, and do not report
   blocked for it.
4. Run `just pack-sync` last, to its end, then check `git status --short` and commit once.

**Theme:** Repo gate

**Description:** The repo gate must distinguish a check that has stopped making progress from
one that is simply taking longer on a busy machine. When the jail refuses access to the
kernel's CPU counter, the gate currently loses stall detection but can still pass its checks.
That leaves the drain's everyday runs without the protection the owner requested. Keep
watching for stalls through an unprivileged source of CPU accounting, and make the source
visible to the operator.

A check can do useful work in child processes that exit between samples. Their CPU time must
still count; otherwise, the gate could mistake active work for a stall. Continue allowing a
check to run while it uses CPU or produces output. If neither accounting source is readable,
report that detection is unavailable rather than implying that the check is being watched.

**Outcomes:**

- Detect stalled checks even when the jail refuses the kernel counter.
- Keep active checks running, including checks that work through short-lived children.
- Record the CPU accounting source, or explicitly report that detection is unavailable.

**Agent Details:**

- **Ref:** [Gate design](../design/gate-rework.md) (stall detection and unavailable CPU accounting).
- **Where:** `tools/tillr-gate/cpu_linux.go`, `tools/tillr-gate/watchdog.go`,
  `tools/tillr-gate/cpu_linux_test.go`, `tools/tillr-gate/watchdog_test.go`,
  the verdict record in `tools/tillr-gate/run.go` and `tools/tillr-gate/gate.go` with their
  tests,
  [`tools/fork-check.py`](../../tools/fork-check.py), `tests/fork-check.test.py`,
  [gate README](../../tools/tillr-gate/README.md), and the gate design's stall section.
- **Evidence:** On 2026-11-17, the drain's gate logs in a rootless Podman jail with
  `/proc/sys/kernel/perf_event_paranoid` set to 2 recorded
  `watchdog unavailable: kernel task-clock counter: permission denied`. This included
  `00-gate_python3.log` for `item-sync-hold-unshared-items-a4` and the gate program's own
  checks, even though gates passed.
- **Verify:** Force the kernel task-clock counter unavailable in both the Go gate and Python
  checker. Cover a silent, CPU-idle check for the full stall window; continued CPU use or
  output; short-lived children; and failure to read either accounting source. Check that logs
  and verdicts identify the source. Use the fallback the second ruling below settles, which
  counts work done in children that have already exited. Document the available sources and
  their precedence.

**Ruling, 2026-11-17 (answers the third run's fence conflict):** the third run stopped before
editing because c2 puts the CPU-accounting source in the verdict, and the verdict record is
written by `tools/tillr-gate/run.go` and `tools/tillr-gate/gate.go`, which the item's paths did
not name. The run was right to stop, and right not to carry the source in a field that means
something else. Commit `e62113977` has since widened the paths to the whole `tools/tillr-gate/`
directory, so both files and their tests are inside the fence, and the delivered copy under
`yolo-packs/tillr/` is synced in the same commit. Three points settle how c2 is met:

1. **The source is a field of its own.** The per-check verdict record (`VerdictCheck` in
   `run.go`) gains one string field, `cpu_source`, left out when it is empty. Both places that
   copy the watchdog's observations into that record set it: the single-check path in `run.go`
   and the gate's path in `gate.go`.
2. **One fixed name per source.** The field holds a short constant naming the source that
   supplied the last CPU sample: one name for the kernel counter, one for the fallback. The
   check log prints the same name, and `tools/fork-check.py` uses the same names in its log line
   and in the stall record that already carries `cpu_time_ms`. When neither source can be read
   the field is absent and `watchdog_unavailable` carries the reason, as it does today.
3. **No reader outside the gate changes.** The drain reads the verdict in
   `tools/tillr-drain/drain/gate_stall.go` and does not refuse a field it does not know, so the
   new field needs no change there, and that directory stays outside the paths.

The criteria below are unchanged.

**Ruling, 2026-11-17 (answers the sixth run's report of blocked):** the sixth run restored the
fifth run's change and stopped when `python3 tests/fork-check.test.py` passed a 300-second limit.
That change kept stall detection on by putting every check under a supervisor that
[traces](https://man7.org/linux/man-pages/man2/ptrace.2.html) it: the supervisor stops each
thread as it exits and saves its CPU time. The run could not tell a slow suite from a hung one.
Both were true, and the second one settles the mechanism. These were measured the same day in the
same jail, on base `123aeab1b`, with the preserved change from
`refs/tillr/preserved/run-item-gate-stall-watch-without-perf-counter-a6` applied and then removed:

- **The limit was shorter than the suite.** On the base alone the suite ran 4 minutes 2 seconds,
  with a load average near 80 on 32 processors, and then failed. So a 300-second cut-off says
  nothing about the change.
- **The suite is red on the base, before this item's tests.** It stops at the assertion
  `a timing declaration no longer matches a hit`, naming
  `tools/bedrock-spend/internal/cli/cli_test.go: if time.Since(started) > time.Second {`. Commit
  `e99cdf1a9` removed that line from the test and left its entry in
  `tools/gate-timing-declarations.json`. The Python regression tests this item adds sit after
  that assertion, so no run has yet reached them.
- **The tracing supervisor hangs a check.** With the stale entry removed in the working tree
  only, the suite reached its last step, `go test -C tools/tillr-gate ./... -run` over the new
  tests, which runs under the supervisor. It made no progress for 35 minutes, until the 40-minute
  limit on the whole suite ended it.
  The Go assembler it had started was a zombie whose second thread sat in a tracing stop that the
  supervisor never resumed, `go` waited on it in `do_wait`, and the supervisor slept. A watcher
  that can stop the check it watches turns its own defect into a stall charged to the candidate.
- **Control-group accounting is not available here.** `mkdir /sys/fs/cgroup/probe-a8` returns
  `Read-only file system`, so the gate cannot give a check a group of its own to read.
- **The rest held.** `python3 tools/go-test-shards.py tools/tillr-gate -k 6` passed 171 tests in
  22 seconds, and `just pack-sync` finished in 10 seconds and changed no path.

Four points settle the fallback:

1. **The fallback only reads; it never traces.** Drop the tracing supervisor: the file
   `tools/tillr-gate/process_accounting.py`, its copy inside `tools/fork-check.py`, the socket
   that nested watchers shared, and the code that starts a check under them. Nothing the fallback
   does may be able to delay, stop or signal the check.
2. **What a sample reads.** The members are the live processes in the check's process group,
   which is the group the runner already ends on a stall. Each is identified by its process id
   and its start time, both from `/proc/<pid>/stat`. For each member the sample sums the run time
   of its threads, the first number in `/proc/<pid>/task/<tid>/schedstat`, in nanoseconds. A
   sample shows CPU progress when that sum differs for any member, or when the set of members
   differs from the previous sample. A member that disappears while it is being read counts as
   a change.
3. **Why this needs no record of exited children to be complete.** Any process that ran between
   two samples was either alive at the first one, or was started after it by a process that ran.
   Following the starters back always reaches a member of the first sample that either ran, so
   its run time differs, or exited, so the set differs. Measured in this jail with a one-second
   sample: an idle leader showed progress in 0 of 8 samples; a leader that ignores its children's
   exit and starts one short child every 0.9 seconds showed 8 of 8; so did a leader that exits
   and leaves a busy orphan, and one that waits for short shell children.
4. **The reported total.** `cpu_time_ms` under the fallback is the highest value seen of the
   members' run time plus the time of their reaped children, which `/proc/<pid>/stat` carries as
   `cutime` and `cstime`. That is how c1's "still counts exited children" is judged. A child
   whose parent discards its exit status is missing from the total, so the documents call the
   total a lower bound; by point 3 such a child still counts as progress. A process that leaves
   the group is not a member, and the documents say so.

The names are unchanged: `kernel task-clock counter` wins when it attaches, and otherwise the
source is `process accounting`. When `/proc` or the run-time file cannot be read, the source is
absent and `watchdog unavailable` is printed, which is c5. The design's sentence that incomplete
`/proc` snapshots are never substituted is replaced by points 2 to 4 under c8. Keep the rest of
the preserved change: the `cpu_source` field, the source line, the tests' way of forcing the
counter unavailable, and the tests that do not depend on tracing.

For verification, give the Python suite no limit shorter than its run, and read a run that was cut
off as no result. The stale declaration is outside this item's paths. Remove it in the working
tree only to prove the tests behind it, restore it before committing, and if the assertion is
still red on the run's base, report the trunk broken and name that check.

The criteria below are unchanged.

**Ruling, 2026-11-18 (answers the ninth run's report of blocked):** the ninth run stopped before
editing, on two points. Neither is a conflict between its brief and the tree, and neither needs a
new decision.

1. **The rulings in this block are the brief's settled decisions.** The run's brief listed no
   settled decision, the tree held the two rulings above, and the run read that difference as a
   brief that was wrong about the tree. A brief that carries less than this block is short, not
   wrong: it names this block as the item, so the block is read to its end and the rulings are
   followed, the later one where two disagree. A stop is for a question the block does not
   answer. Build the read-only fallback the second ruling settles, on the parts of the preserved
   change it says to keep.
2. **How to prove the Python suite with the stale declaration outside the paths.** The second
   ruling's last paragraph already says it; these are its steps. Read on `2d70932eb` on
   2026-11-18: `rg -n -F 'if time.Since(started) > time.Second {'` over
   `tools/gate-timing-declarations.json` and `tools/bedrock-spend/internal/cli/cli_test.go`
   returns one line, in the declarations file, and none in the test, so the suite is still red
   there before it reaches this item's tests.
   - Run `python3 tests/fork-check.test.py` once on the unchanged declarations file, with no
     limit shorter than the run, and keep the assertion it stops at.
   - Remove the stale entry in the working tree only and run the suite again. This run is the
     proof of c6 and c7, and its output goes in the report.
   - Restore the file with `git checkout -- tools/gate-timing-declarations.json`, check that
     `git status --short` does not list it, and only then commit. The commit holds the item's
     paths and nothing else.
   - If the first run stopped at `a timing declaration no longer matches a hit`, end the report
     with the trunk broken, naming that assertion, the declarations file and the base sha. The
     commit comes first, so the candidate exists when the base is replayed. If the first run
     passed, because the declaration was repaired on the run's base, skip the removal and report
     done.

   Running a command over a file outside the paths is not a change to it; only the commit is
   held to the paths.

The criteria below are unchanged.

**Ruling, 2026-11-18 (answers the eleventh run's report of blocked):** the eleventh run restored
the preserved change, ran `just pack-sync`, saw it cut off part-way through the mirror of another
module, put that mirror back and stopped without running a test. Nothing in this block changes.
The run did its steps in the wrong order, on a change that was not yet the one to commit. These
were read on `2ca3cedff` on 2026-11-18, in the same jail:

- **The restored change is still the one the second ruling rejects.** `git ls-tree -r --name-only
  refs/tillr/preserved/run-item-gate-stall-watch-without-perf-counter-a11` lists
  `tools/tillr-gate/process_accounting.py`, the tracing supervisor, and its 336 lines are in that
  change. The eleventh run's 19 files are the sixth run's 19 files. Syncing and committing them
  would have delivered the supervisor that hangs a check.
- **The sync is short, and it was cut off by the caller's limit.** On the unchanged tree, with a
  load average of 42, `just pack-sync` finished in 20 seconds and `git status --short` then
  listed nothing. The recipe deletes each module's mirror and copies it again, eleven modules in
  turn, so a sync stopped part-way leaves one module's mirror partly deleted. That is what the
  run saw in `tillr-view`; the recipe was not rewriting that module with anything new.
- **The preserved change still applies.** `git diff` between that ref's first parent, which is
  `3318def2f`, and the ref passes `git apply --check` on `2ca3cedff`.
- **The stale declaration is still there.** The `rg` command in the ruling above returns the same
  one line, in `tools/gate-timing-declarations.json`.

The order of work, which the next run follows:

1. **Restore the sources only.** Apply the preserved change's `tools/`, `tests/` and `docs/`
   paths and leave out its copy under `yolo-packs/`. The mirror is made by the sync in step 4,
   never restored and never edited by hand.
2. **Build the fallback before anything else.** The restored change is the starting material, not
   the candidate. Do what the second ruling's four points say: delete
   `tools/tillr-gate/process_accounting.py`, its copy inside `tools/fork-check.py`, the shared
   socket and the code that starts a check under them, and write the fallback that only reads.
   The work is not ready to sync while `rg -n -i 'ptrace' tools/tillr-gate tools/fork-check.py`
   returns a line.
3. **Test.** Run `python3 tools/go-test-shards.py tools/tillr-gate -k 6`, then the Python suite by
   the steps in the ruling above.
4. **Sync once, last, and let it finish.** Run `just pack-sync` with a limit of at least ten
   minutes on the command; it is a file copy and needs no more than that on a busy machine. If it
   is cut off anyway, run it again to its end. A complete run rebuilds every module's mirror from
   its source, so it repairs a partial one, and no mirror is restored with `git checkout`.
5. **Check the paths, then commit.** `git status --short` lists only this item's paths and
   `yolo-packs/tillr/files/local/share/tillr/tillr-gate/`. A line for any other module's mirror
   means the sync did not finish: go back to step 4. Then commit once.

A cut-off command is no result, for the sync as for the suite. It is repeated with a longer
limit and is not a reason to stop.

The criteria below are unchanged.

**Ruling, 2026-11-18 (answers the thirteenth run's report of blocked):** the thirteenth run
repeated the eleventh run's order and stopped on two commands that its own limits cut off. Nothing
in the rulings above changes; what changes is where a run finds them. These were read on
`b39510945` on 2026-11-18, from that run's log and the tree:

- **The run never read the rulings.** Its one read of this block was
  `rg -A18 '^### Add stall detection' docs/tillr/roadmap.md`, which ends inside the description.
  The order of work is now the first paragraph under the heading, so a read of that length
  reaches it.
- **It tested the rejected change.** It applied the kept work with
  `tools/tillr-gate/process_accounting.py` in it, ran `just pack-sync` before any test, and
  edited no file. `git ls-tree -r --name-only
  refs/tillr/preserved/run-item-gate-stall-watch-without-perf-counter-a13` still lists the
  supervisor, and that ref's 19 files are the sixth run's.
- **Both stops were the caller's limit.** `just pack-sync; python3 tests/tillr-docs.test.py` ran
  under one 20-second limit, and the Go tests, `python3 tests/fork-check.test.py`, the sharded
  suite and the sync test ran chained under one 180-second limit. The second ruling measured the
  Python suite alone at 4 minutes 2 seconds on a busy machine, so 180 seconds could not finish it.
  `python3 tests/tillr-docs.test.py` is no part of this item's proof and is left to the gate.
- **The starting facts hold.** The kept work's `tools/`, `tests/` and `docs/` paths pass
  `git apply --check` on `b39510945`, and the `rg` command in the third ruling still returns one
  line, in `tools/gate-timing-declarations.json`.

The next run builds: it follows the paragraph under the heading, and reports blocked only for a
question this block does not answer.

The criteria below are unchanged.

**Ruling, 2026-11-18 (answers the seventeenth run's fence conflict):** the seventeenth run
restored the reverted change and stopped on a test outside the item's paths. The run was right to
stop. The paths now name that test, `tools/gate/checks/change_checks_test.py`, and nothing else
about the change is reopened. These were read on `e30659c1e` on 2026-11-18, in the same jail, with
the kept work of `refs/tillr/preserved/run-item-gate-stall-watch-without-perf-counter-a17` applied
and then removed:

- **The kept work is the read-only change.** Its ten files apply cleanly, none of them is
  `tools/tillr-gate/process_accounting.py`, and
  `rg -n -i 'ptrace' tools/tillr-gate tools/fork-check.py` returns no line. It is the change that
  landed as `36e74e541`.
- **Why it was reverted.** `e30659c1e` reverted it because the check `change-checks-test` failed
  on the trunk. `test_markdown_change_runs_all_declared_checks_against_caller_base` in
  `tools/gate/checks/change_checks_test.py` expected the log of a silent check to be empty when
  the watchdog is available, and failed with
  `'watchdog CPU source: process accounting\n' != ''`. That log line is what c2 asks for, so the
  test is what changes, not the log.
- **The amended test passes.** With the assertion changed as below,
  `python3 tools/gate/checks/change_checks_test.py` ran 39 tests in 46 seconds and passed.
- **The stale timing declaration is gone.** The `rg` command in the third ruling returns no line
  on this base, so `python3 tests/fork-check.test.py` runs on the unchanged tree and the third
  ruling's steps for removing and restoring the declaration no longer apply.

Three points settle the test:

1. **What the silent check's log holds.** Exactly one line. When the verdict's check carries
   `watchdog_unavailable`, the log is that text and a newline, as the test says today. Otherwise
   the check carries `cpu_source`, and the log is `watchdog CPU source: `, the value of
   `cpu_source` and a newline. The test reads the name from the verdict and does not fix which
   source it is, because the jail decides that.
2. **Exactly one of the two fields is set.** The test asserts that each check carries
   `watchdog_unavailable` or `cpu_source` and not both. A check that carries neither is being
   watched by nothing that the verdict names, which c2 and c5 together rule out.
3. **Only that assertion changes.** The rest of the file, the check's declaration
   `tools/gate/checks/change-checks-test.json` and the other tests under `tools/gate/` stay as
   they are. `tools/gate/` has no copy under `yolo-packs/`, so the sync in the last step adds no
   path for it.

The criteria below are unchanged.



## Filed 2026-11-15: a gate test that fails only under load

On 2026-11-15, with nine lanes running, `go test ./...` in `tools/tillr-gate` failed once in
`TestTwoShadowRecordsOverOneCandidateDiffMechanically` ("two shadow records over one candidate
differ in more than the measurement"). The same tree passed three times in a row once the lanes
were idle. The worker that saw it reported the trunk broken and stopped with its work uncommitted.

### The shadow record test passes under load

Built: this landed in commit `8e415d2b73`.

## Filed 2026-11-15: repeated conflict-repair failures interrupt unrelated work

### Prevent one repair failure from cycling the queue

Built: this landed in commit `263173aedf`.

**Description:** When conflict repair cannot start, unrelated workers should not repeatedly
start and get interrupted while the same broken repair is attempted again. Investigate why
one staging failure canceled sibling workers and why restarting the supervisor repeated
that failure without a changed input. Keep the candidate available for repair, and make the
next useful action visible to the operator.

**Outcomes:**

- Hold or recover the affected repair without repeatedly interrupting unrelated workers.
- Record the failure once per unchanged cause and expose what will let work continue.
- Attribute interrupted attempts and their usage to the correct run.

**Agent details:** On 2026-11-15, `tillr view running --store .tillr/state.db` reported
no live workers, while the supervisor log recorded 23 loop starts. The drain log repeatedly
reported a literal brace pair in generated conflict facts for S4. Two sibling items reached
attempt 23, exiting with code -1 in about one second. S4's passing candidate repeatedly
received `squash-conflict` without reaching its conflict-fix run. The immediate delivery
defects were repaired by storing exact code evidence in a run-local file, referencing it
from a short brief, and sending Pi's brief on standard input rather than in one
command-line argument. This item owns the
failure containment and restart behavior, not those delivery changes. Editing the main
checkout during the first recovery attempt also triggered the expected boundary stop;
that enforcement is not the defect, and recovery must leave the main checkout unchanged
while workers are active.

## Filed 2026-11-15: a spent subscription window is skipped, not retried per item

[The Claude backend design](../design/tillr-claude-backend.md#4-falling-back-when-the-window-is-spent)
falls back by letting each item's first start be refused. That costs one refused start per item
while the window is shut, and a run cut off mid-work is run again from the start. The harness's
stream already says when the window reopens: a `rate_limit_event` line carries the window's kind,
the fraction used and the reset time. On 2026-11-15 a live run through the adapter reported the
five-hour window at 97% used.

### A backend whose window is spent is skipped until the window reopens

Built: this landed in commit `d1bde2b171`.

**Ruling, 2026-11-16 (answers the first run's fence conflict):** the first run stopped because
`tests/tillr-muse-backend.test.py` pins the report header's exact key list and that suite was
outside the item's paths. The suite is now inside them, with
[`tillr-muse-backend.md`](../design/tillr-muse-backend.md#4-output-capture-and-verification),
which describes the header. The report keeps one shape for every backend: the four window facts
are four new header keys — `rate limit kind`, `fraction`, `status`, `reset` — added once to the
shared key list in `tools/tillr-run/backends/report.py`, after `cost usd` and before `complete`,
and to the suite's own list in the same order. Every extractor writes all four. Only the claude
extractor can fill them; the pi, muse and agy extractors always write `unknown`. The report body
stays the model's text verbatim: window facts go in the header and nowhere else, and the suite's
shared-body check is not loosened. The drain reads the four keys from the whole pinned report,
not from its tail.

## Filed 2026-11-15: the loop is a daemon unless told otherwise

`tillr drain loop` exits with `queue-drained` when nothing is dispatchable, unless it is started
with `--stay`. The owner's ruling on 2026-11-15 is the opposite default: the loop is a daemon that
waits, ready, and a new item, an answered question or a trunk move wakes it. A loop started without
the flag on that date exited while 45 items were still held.

### The loop waits when nothing is dispatchable, and exiting is the option

Built: this landed in commit `82e1ba7196`.

## Filed 2026-11-15: the observatory speaks to its reader, not to its builders

On 2026-11-15 the owner opened the observatory while the loop was stopped and could not tell what
had happened or what to do. The header said `STOPPED fault diagnose the recorded fault before
restarting` without saying what the fault was. The Passes page printed a refused request with the
server's whole route list, twice, because the page was newer than the server it was talking to. The
page used the words "face", "pass", "hold-only pass" and "action" without saying what any of them
means.

### A stopped loop says what stopped it, in plain words, and what happens next



## Filed 2026-11-15: an item that was not written to the prose guide is rewritten before anyone builds it

Principle: an item is read by a person deciding what matters before it is read by a worker, so
it has to be easy to scan. The `tillr-prose-style` skill says how: a short imperative title, a
description that leads with why, and the exact paths and checks kept apart in the criteria.

The skill is applied only when the agent writing an item happens to load it. Many items filed
on 2026-11-15 were written without it: their titles are statements instead of instructions and
their sections open with mechanism instead of motivation. Nothing notices, because the only
place every item passes through is the sync that reads the documents, and the sync checks
structure and not prose.

Two signals are cheap to check there. The first is a mark that only the skill tells an author
to add, so an item without it was written by someone who did not read the guide. The second is
the shape the guide asks for. Neither proves the prose is good, which only a reader can judge,
so an item that fails either one is not refused. It is rewritten: a small, high-priority item
goes to the cheap seat with the guide in its brief, and the original waits until that lands.

### Mark items written to the prose guide and check their shape at sync



### File a rewrite for an item that fails the prose check, ahead of the item itself


**Ruling, 2026-11-17 (answers the first run's fence conflict):** the first run stopped because c5
needs a reader the fence left out. The settings file is read by `tools/tillr/settings/`, which
refuses any table other than `roll`, `push` and `comments`, so it refuses `[prose]` and the loop
would not start with this repository's file in place. `tools/tillr/` is added to this item's
paths, with its tests. The work extends that one reader to take `prose.rewrite`, a boolean that
defaults to false, refuses any other key in the `prose` table as it refuses unknown keys in the
other tables, and adds no second reader of the settings file inside the sync, the loop or the
view. This repository has no settings file yet, so the work creates `.tillr.toml` at the
repository root holding only that table and key. The criteria below are unchanged.


## Filed 2026-11-15: a design on the roadmap always has an item in the queue

Principle: the queue is the one place to see all the work, so anything the roadmap names has to
be there. On 2026-11-15 the owner asked why the item graphs design had no item. The sync creates
an item only from an item block, and a design that is still waiting on rulings has none, so four
designs were on the roadmap and invisible in the queue. Each now carries one design-seat item
that is held on its open questions. The check below keeps the next design from slipping through.

### Warn when a roadmap design has no item block


**Ruling, 2026-11-16 (answers the second run's fence conflict):** the second run stopped because
c3 changes the waiting face, whose source is `tools/tillr-view/waiting.go`, and that directory
was outside the item's paths. It is now inside them, with its tests. The run also found the
deeper gap: the waiting face builds its rows from stored items only, and a sync finding is
printed in the `item.sync` record and stored nowhere, so the face has nothing to read. The
finding therefore goes into the store, and the face reads it there:

- A sync that writes replaces, in its own transaction, the whole set of `design-without-item`
  rows in one new store table that `tools/tillr-item/` owns. Each row holds the design
  document's path, the roadmap file and line that link it, and the time the finding was first
  seen, which a later sync keeps while the finding persists. A finding that no longer holds is
  gone after the next sync. A dry run writes nothing. This is not a change to an item, so c2's
  "changes no item" still holds, and the test for c2 asserts the items are identical.
- The waiting face reads that table directly, as it reads the item tables, and does not import
  `tools/tillr-item/` or re-read the documents: the block grammar stays in one place. A store
  without the table yields no such rows, as the face already treats its other optional tables.
- Each row of the table is one waiting row with class `needs you`, where `queue`, a why that
  starts with `design-without-item` and names the roadmap file and line, the owner as mover
  with the instruction to file the document's item block, and the first-seen time as since.
  The row has no item: `item_id` and `rank` are empty, the document's path is carried in one
  new `document` field that is empty on every item row, and these rows sort after every item
  row, by document path. The serve face's waiting list shows the document path where an item
  row shows its id. A test in `tools/tillr-view/` covers a store with one such row, one with
  none, and one without the table.



## Filed 2026-11-15: other people and other agents change the repository while the drain runs

Principle (owner's ruling of 2026-10-23, in [agent work control](../pi/agent-work-control.md#the-owners-direct-lane-owner-ruled-2026-10-23)): a
direct edit is the owner's hand, it takes no lock, collisions heal at the landing and not at the
edit, and a dirty tree is a wait and not a failure. The owner also asked, in
[the autonomous landing design](../design/autonomous-landing.md#5-outside-movement-is-routine), that other agents merging,
pulling or moving the remote have no large effect on the queue.

tillr does not honor that yet. On 2026-11-15 an operator's uncommitted edit in the main checkout
was read as a worker escaping its lane. The loop stopped, seven runs in flight were killed, and
four more stops followed from the records that stop left behind. Nothing about the edit was
dangerous: it touched documents no run was changing.

### Treat uncommitted changes in the main checkout as the owner's, never as a run's


**Ruling, 2026-11-16 (answers the first run's fence conflict):** the first run stopped before
editing because c4 changes the waiting face, whose source is `tools/tillr-view/waiting.go`, and
that directory was outside the item's paths. Editing only the delivered copy under
`yolo-packs/tillr/` would have broken the rule that the copy is derived from the source. The
directory is now inside the paths, with its tests, and all four criteria are built together. The
run also found two gaps in the face, and both are ruled here so the next run does not have to
choose:

- The drain records the wait as an ordinary decision for the item, as it records every other
  hold, with the reason `trunk-path-dirty: ` followed by the dirty paths the landing touches,
  sorted and separated by `, `. The exported constant in `tools/tillr-drain/` is the bare
  `trunk-path-dirty`. No new table is needed: the face already reads the item's latest decision.
- The face gives the bare reason a rule in its table (class `waits on work`, where `land`, mover
  `commit or restore the path`) and recognizes the `trunk-path-dirty: ` prefix the way it
  already recognizes `inputs-changed: `, so the row's why carries the paths and no row falls to
  `unclassified`. `TestWaitingEveryDrainReasonHasARule` then covers the new constant unchanged.
- "How long it has waited" is the time since the first decision of the current unbroken streak
  of that same reason, which is what the face already computes as the row's since. So the clock
  restarts when the set of dirty paths changes, and that is intended: it is a different wait.
  The row keeps its absolute since and gains one `waited` field, the elapsed time from since to
  now as a rounded duration. The field is empty on every other row. The terminal face prints it
  in the why cell after the paths, and the serve face shows it beside the since time.
- A test in `tools/tillr-view/` covers a store whose latest decision is the wait on one path,
  one on two paths, and one where the wait was followed by a landing, which shows no such row.


### Ignore fetches, pulls and other people's commits while runs are in flight


**Ruling, 2026-11-16 (answers the third run's conflict):** the third run built c1 to c4 and
stopped without a commit on two findings from its own review. Both are ruled here so the next run
does not have to choose.

- **The waiting face is inside the paths.** A new exported reason in `tools/tillr-drain/drain/`
  fails `TestWaitingEveryDrainReasonHasARule` in `tools/tillr-view/` until the face has a rule
  for it, and the face's source, `tools/tillr-view/waiting.go`, was outside the item's paths. The
  run was right to stop. The directory is now inside the paths, with its tests, and its delivered
  copy is synced in the same commit.
- **The reason has one spelling.** The exported constant is the bare `trunk-rewritten`. A
  candidate that is waiting to land while the rewrite stands gets an ordinary decision, as every
  other hold does, with the reason `trunk-rewritten: ` followed by the lost tip, ` -> ` and the
  new tip, both as full shas. The face gives the bare reason a rule (class `needs you`, where
  `trunk`, mover `restore the trunk's history`) and recognizes the `trunk-rewritten: ` prefix the
  way it already recognizes `trunk-path-dirty: `, so the row's why carries both tips and no row
  falls to `unclassified`.
- **The loop records a rewrite when it sees it, not when a candidate is held.** The run kept the
  lost tip only in the running loop until a candidate recorded a hold, so a loop replaced in
  between forgot the rewrite and the next loop would have landed onto the rewritten trunk. The
  drain's store keeps one row, the way `rolling_state` keeps the watermark: the trunk tip the
  loop last observed and, while a rewrite stands, the lost tip. Each pass reads the trunk, and
  compares it with that row, before it judges or lands anything. A tip that the stored tip is
  not an ancestor of is a rewrite, and the lost tip is written in that same pass, before any
  candidate is considered. A loop that starts reads the row first, so it holds landings from its
  first pass whether or not any decision names the rewrite. The row's name and columns are the
  run's to choose.
- **The rewrite is compared with the last observed tip, never with a candidate's base.** A reset
  to a candidate's base leaves that base an ancestor of the trunk while a later commit the loop
  had already seen is gone. The run's own test found this.
- **Restoring the history is the only release this item builds.** The row's lost tip is cleared
  in the pass that finds it an ancestor of the trunk again, and held candidates then land without
  a second gate. A way for the owner to accept a rewrite that was intended is not part of this
  item, and the run does not invent one.
- **The lander checks the same tip under its lock.** The trunk can be reset after the drain's
  check and before the lander takes `land.lock`. The drain passes the tip it observed to the
  lander, and the lander refuses with `trunk-rewritten` when the trunk it reads under the lock
  does not descend from that tip.
- **The third run's code was not kept.** The lane starts again from the trunk: on 2026-11-16
  `git status --short` in the lane's worktree at `60ea6e236` printed nothing. One more thing that
  run learned is carried: an existing test in `tools/tillr-drain/` asserts that a tag appearing
  mid-run convicts the run. It changes with c1 to assert the conviction for a ref inside tillr's
  own namespaces that the run was not given. It is not deleted, and not loosened to pass.


## Filed 2026-11-15: one item's trouble holds that item, and recovery is a command

On 2026-11-15 the loop stopped whole five times for the trouble of one item: a gate that could not
judge one candidate, a conviction against one run, a record of a dispatch that never started, a
lane whose candidate had been moved, and an item whose definition could not be updated. Each time
every other run was killed. Each recovery was done by hand, with a script or a direct edit of the
store, because no command exists for it, and two of those hand recoveries caused the next stop.

### Hold the one item and keep the loop running when a step cannot answer


**Ruling, 2026-11-16 (answers the third run's conflict):** the third run stopped, with its work
partial and uncommitted, because the waiting face's suite was already red: nine tests fail with
`no such table: drain_ticks`. That failure is in the trunk and not in this item's work, and the
run was right not to guess. It is ruled here so the next run does not stop on it again.

- **The failure is real and measured.** At `015af5a5`, with a clean worktree, `cd tools/tillr-view
  && go test ./...` fails `TestPassesDecisionsTimingsSessionsAndCLI` and the eight `TestPasses…`
  tests in `passes_edge_test.go`, each with that message. `cd tools/tillr-drain && go test ./...`
  passes there.
- **The cause is a rename the view only half followed.** Commit `becdab859` moved the loop's
  timing measurements from the table `drain_ticks` to `drain_passes` and kept the old table only
  as a source to copy from, as the drain's README states. `tools/tillr-view/serveread.go` reads
  the new table. `tools/tillr-view/passes.go` still sums phase times from the old one, and
  `passes_test.go` and `passes_edge_test.go` still seed it. `rg -n drain_ticks -g '*.go'
  tools/tillr-view` finds those three files and no other. On a real store this is more than a red test: the passes
  face reads a table that new measurements never reach.
- **This item repairs it, first.** The three files are inside this item's paths, the waiting
  face's suite is this item's own verification for c4, and a failing suite is not waived for being
  older than the change. The repair is replacing `drain_ticks` with `drain_passes` in those three
  files. Measured on 2026-11-16: with only that replacement, `go test ./... -count=1` in
  `tools/tillr-view` passes. It is criterion c5 below, and it goes in the same commit, with the
  pack copy synced.
- **A failing suite inside these paths is this item's to fix, not a stop.** The stop condition
  for a failure outside the task's scope applies to a failure whose repair needs a file outside
  the paths. If the next run meets one of those, it stops and names the file.
- **The third run's code was not kept.** The lane starts again from the trunk. Three things that
  run learned are carried so they are not learned twice:
  - The staging collision in c3 reproduces as `the decision decision-item-a-a1-queued-running is
    already recorded with different columns`: the dispatch record is written before staging, so
    a failed staging must take its record back or never write it.
  - The check that a lane's candidate is what the record says must run after the loop has
    recovered a candidate it retired itself. Run before it, the check holds a healthy item, and
    `TestRestartGatesAndLandsTheRetiredHead` fails.
  - Two existing tests assert the old whole-loop behavior and change with this item:
    `TestABoundaryViolationStopsTheLoopAndNamesThePath` in `tools/tillr-drain/loop_test.go`
    (the third run renamed it to say the item is held, and still failed it) and
    `TestAStepThatNeverStartedRecordsNoExitCode`. Each is updated to the item-local hold that c1
    states. Neither is deleted, and neither is loosened to pass.

### Add commands for the recoveries that were done by hand



## Filed 2026-11-15: a design conversation keeps its designer

A design gets better answers, faster and cheaper, from the session that wrote it than from a new
one that has to read everything again. Today a comment on a design reaches nobody until someone
pastes it into a session. The owner wants comments left in Vantage to go straight to the designer
session that last worked on that document's item, and wants that session kept ready for a few
minutes and then compacted, so it can still be resumed later.

[A comment on a design goes to the session that last worked on it](../design/tillr-designer-sessions.md)
is the design. It measured the turn on 2026-11-16: a session resumed inside its cache's lifetime
answers a comment for $0.42 where a new session costs $0.84, and one resumed after it costs more
than a new session. So a session is kept as a file and never as a process, is warm for the cache
lifetime the machine uses (an hour here, not five minutes), and is compacted only when a comment
next needs it. The six items that build it are in that document's
[build order](../design/tillr-designer-sessions.md#build-order); the first delivers one comment to
one resumed session end to end.

### Send review comments on a design to the session that last worked on it



## Filed 2026-11-15: the loop says why every item is where it is, and what it has spent

### Give every held item a class and a reason


**Ruling, 2026-11-17 (answers the second run's conflict):** the waiting face is two faces, and
the criteria below did not say which one they meant. The default face, which is what the
`waiting` command and the observatory's Home and Items screens show, asks the planner on every
read what the next pass would do with each item. The history face, which is `waiting --history`
and `GET /api/v1/waiting`, reads the decisions that passes have already recorded. The second run
built c2 on the history face, and its reviewer then found that the default face cannot show the
same words: `TestWhyAPIAndWaitingBeforeAnyPass` requires an item no pass has looked at to read
`no-policy` there, because that is what the planner answers.

The default face keeps its contract, and c2 belongs to the history face alone. An item is never
"not yet planned" on the default face, because the planner plans it at the moment of the read and
its answer is the real reason the item is not moving. Replacing `no-policy` with `not yet
planned` would hide the one thing the reader can act on. The same holds for a stopped run: the
default face shows what the next pass will do with that item, and the planner already gives a
re-dispatch the reason `earlier run orphaned by a loop stop`. The stop itself is a recorded fact,
so the history face is where it is shown. The three defects seen on 2026-11-15 — `unclassified`,
an empty reason and a dash for the mover — exist today only in the history face's fallback row and
fallback rule, and that is where this item removes them.

The second run also left one defect in its unfinished work, and c2 now settles it. A hold recorded
in the same millisecond as a stop was hidden behind the stop's label. Decisions carry a
millisecond time and no sequence number, so a tie cannot be ordered by time; the rule below orders
it by which run the decision belongs to.


**Agent details:**

- **Where:** the history face is `assembleWaiting`, `waitingRuleFor` and `waitingDecision` in `tools/tillr-view/waiting.go`; the fallback row there is the one built with `Class: "unclassified"` and `Why: "no pass yet"`, and the fallback rule is the last line of `waitingRuleFor`. The default face is `plannerWhy` in `tools/tillr-view/why.go`, which runs `tillr-drain why`; its classes come from `whyPresentation` in `tools/tillr-drain/drain/why.go`, which this item does not change. The observatory's class lists are under `tools/tillr-view/web/src/components/`. The pack's copy under `yolo-packs/tillr/` is regenerated with `just pack-sync`.
- **The stop's evidence:** the drain records a `drain.orphaned` event whose object is the run, and a later `run.resume` event for the same run withdraws it. Read those two event kinds; do not infer a stop from a signal or an exit code. The decision that dispatched a run is the `dispatches` row whose `run_id` is that run.
- **Not this item:** changing what the planner answers, or adding the stop to the default face. The README section *Why each unlanded item is not moving* in `tools/tillr-view/README.md` already states the default face's contract; this item updates only its *Recorded-decision history* section.
- **Evidence:** at `77fe7c39`, `rg -n 'unclassified' tools/tillr-view/waiting.go tools/tillr-drain/drain/why.go` returns two lines, both in `waiting.go`: the fallback rule and the fallback row. `whyPresentation` has no such branch, and its default returns `needs a cause` with a named mover.

### Add recent spend measurements without stopping dispatch

<a id="stop-dispatching-when-the-spend-ceiling-is-reached"></a>

Principle: [the momentum principle](../design/momentum-principle.md#what-this-forbids)
forbids a budget that stops useful work. Operators still need to see recent spending so they can
decide whether to stop the loop or remove work from the pile themselves. The drain loop and the
waiting view should show the same recorded costs for runs started in the last 24 hours, without
using those costs to decide whether work can start. Missing cost reports must remain visible,
and retries that produce no new model output must not count earlier spending again.

The November 17, 2026 ruling withdrew the originally requested ceiling after the first run
identified its conflict with that principle. This item adds measurement only; it does not
reopen that ruling or introduce another way to throttle work.

- **Theme:** Drain spending visibility
- **Outcomes:**
  - Show recent recorded spending at loop startup and in the waiting view.
  - Count runs without reported costs separately and avoid counting earlier costs again on retries.
  - Keep dispatch independent of spending and leave stopping decisions to the operator.



**Agent details:**

- **Ref:** [What this forbids](../design/momentum-principle.md#what-this-forbids) and [The one legitimate stop](../design/momentum-principle.md#the-one-legitimate-stop) govern the withdrawn ceiling. Keep the existing end-of-loop `spendLine` in `tools/tillr-drain/main.go` unchanged.
- **Where:** the loop's start line in `tools/tillr-drain/main.go`; the waiting face and its spend reads in `tools/tillr-view/` (`serve.go`, `serveread.go`); the pack's copy under `yolo-packs/tillr/`.
- **Verify:** Keep `TestTheSpendLineNamesTheMeasurement` and `TestTheLoopCommandRefusesTheDeletedBudgets` unchanged. Test dispatch with very high recorded spend; test both displays with runs inside and outside the fixed 24-hour window; test missing-cost counts and retries without new model output. Criteria c1–c4 specify the assertions.
- **Not this item:** how an ended or orphaned run's spend is recorded belongs to *Add the spend of killed runs to the record* (`item-drain-record-killed-run-spend`). This item reads whatever cost a run's record carries and does not wait on that one.
- **Evidence:** at `2532d9b8`, `rg -n 'window_hours|ceiling_usd' tools/tillr-drain tools/tillr-view` returns nothing, so no part of the withdrawn ceiling is in the tree to remove.

**Ruling, 2026-11-17 (answers the conflict raised after the fourth run):** no criterion is at fault,
so c1 to c4 stand as written. The measurement was built whole twice and passed the gate both times.
Both landings were refused because the trunk had moved under the candidate. The fix for the second
refusal carried the change onto a newer trunk and then stopped without a commit, on a test failure
that this item does not cause and that the trunk has since repaired. Five points settle it.

- [unlanded] **Start from the built candidate.** Commit `fb08eb32f`, the tip of `refs/tillr/preserved/run-item-drain-spend-ceiling-a4-cfix`, holds the implementation, its tests and the pack copy, already carried forward once onto a newer trunk, and it does not touch this file. Apply that one commit to the base and do not write the item again. Its message is a recovery record and not a commit message, so the new commit gets its own. On 2026-11-17 `git merge-tree --write-tree --name-only --merge-base fb08eb32f^ 8b22572cc fb08eb32f` printed a tree and no conflicted path. The same day, on that merged tree with nothing else running, `python3 tools/go-test-shards.py tools/tillr-drain -k 6` passed 1583 tests and `python3 tools/go-test-shards.py tools/tillr-view -k 6` passed 440. If the base has moved since and a conflict appears, keep what the trunk added and re-add the measurement beside it; a pack copy is regenerated with `just pack-sync` and never resolved by hand. A file that merges without a conflict can still be wrong against a newer trunk, so the full suites on the builder's own base decide and not the merge.
- **The builder does not edit this block or any other part of this file.** The second landing's
  refusal listed this file among its conflicts. The candidate named above does not change it, and
  the loop retires the block after landing. The `docs/` entry in this item's paths is for a tool
  README the change must keep true.
- **The test that stopped the fix is repaired on the trunk, so it gets no allowance.** The fix's
  full drain suite failed `TestCommentPathTwice` once with `designer <nil>`, while the view suite
  and a review ran beside it. The candidate changes no designer code and no comment test. The cause
  was a race in designer retirement, and commit `650f1f16e`, "fix(tillr-drain): keep a designer row
  while a landing rewrites its document", repaired it on the trunk after the fix ran. Measured on
  2026-11-17 on a 32-processor machine, on the merged tree named above: the compiled `drain` test
  binary run as `-test.run '^TestCommentPathTwice$' -test.count=6` in 24 processes at once passed
  144 of 144 runs. The builder leaves `tools/tillr-drain/drain/designer.go` and the comment tests
  as the trunk has them. A failure of this test, or of any other, is a failure like any other: the
  candidate's to fix when the change causes it, and `trunk-broken` only when the same check is red
  on the base with no change applied.
- **The waiting face is the text the `waiting` command prints.** The third run's review asked for
  the measurement in the browser too, and the fourth run's did not, so this is ruled here: c2 and c3
  are met by the loop's start line and by the line the `waiting` command prints above its table.
  The command's `--json` output, the HTTP routes and the browser view stay as they are. The
  candidate does exactly this. Showing the measurement in the browser is separate work for the
  owner to file if they want it, and its absence is not a finding against this item.
- **Where the two lines are printed.** The *Where* entry above names `main.go`, `serve.go` and
  `serveread.go`, and none of them prints either line. The loop's start line belongs in `Run` in
  `tools/tillr-drain/drain/loop.go`, after the `loop: build` line and before the definition is
  started. The waiting line belongs in `waitingFace` in `tools/tillr-view/waiting.go`, with the
  `definition:` line. One reader in `tools/tillr-drain/spend/`, a directory the candidate adds,
  computes the window for both, so the two places cannot disagree. A measurement that cannot be
  read is printed as unavailable at the loop's start and never stops the loop.

### Turn the after-merge check back on



Amended 2026-11-16, answering the conflict the first run raised. Four statements in the earlier block did not match the tree at `ee5ef39a`, and each is corrected above rather than left for the builder to resolve:

- **The keeper is not in the tree.** It is an untracked script on this machine until *Keep the loop's keeper in the repository* (below) lands, so no lane can edit it. This item does not wait on that one: c1's test covers the keeper from the commit that tracks it.
- **No tracked file passes a disabling flag.** `rg -n -e '--rolling' -g '!*_test.go' .` returned nothing at `ee5ef39a`, and the operating skill never names the flag. c1 is therefore a guard, not a removal. (One file has gained the flag since; the second amendment below answers it.)
- **The running loop on this machine is not a lane's to change.** A lane writes only its worktree; the process that is running, and the untracked keeper that starts it, are the owner's. The owner's step, outside every criterion here: restart the loop with no `--rolling` argument once this item lands. Until then a red trunk on this machine is still only logged.
- **The nine failures are not recorded in the tree.** `rg -n -i 'nine (failures|checks|red)|nine failing' docs` finds only this block. c2 now takes its list from the run it names, so the count is whatever that run reports, not nine.

Amended again 2026-11-16, answering the conflict the fifth run raised. c1 said the test passes on the staged tree, and at `bdd6b3c8` it could not: one tracked document passes an empty `--rolling`. The ruling is in c1 above, and these are its reasons:

- **The one hit is a record, not an instruction.** At `bdd6b3c8`, `rg -n -e '--rolling' -g '!*_test.go' .` finds the flag outside this block in one place: the loop command under [Scratch drain measurements](../design/tillr-claude-backend.md#scratch-drain-measurements) in `docs/design/tillr-claude-backend.md`. That command is what a lane ran on 2026-11-16 against a clone and a store in its scratch directory, and the text beside it says the rolling checks were off. Removing the flag would make the document say a different command was run, so the lane does not edit it.
- **The exception is a named list, not a rule about where a command sits.** Excusing every fenced command, or every file under `docs/design/`, would also excuse an instruction someone copies. A list with one reason per entry excuses only what a person has read and judged, and a new historical record costs one reviewed line.
- **Recipes, scripts and skills get no exception.** They are run or followed as written, which is the case the guard exists for. The keeper is a script, so it stays fully covered once it is tracked.
- **c2 and c3 are unchanged.**

### Remove the blocks of items that have already landed



### Keep the loop's keeper in the repository



### Run one item end to end on the Claude backend



Amended 2026-11-16, answering the conflict the first run raised. The earlier block asked a lane for things a lane cannot do, and each is corrected above:

- **A lane cannot run the loop on the real store or move the trunk.** The earlier c1 and c2 needed both: a dispatch writes the store and creates a lane, and a landing moves the trunk. A lane writes only its worktree and its scratch directory. The passes therefore run on a clone and a store inside the scratch directory, which exercises the same loop, policy file, adapter and lander. The first run declined to substitute this by itself, correctly; it is now what the criteria ask for.
- **What the clone does not show is named, not claimed.** It does not show the real queue being drained on the subscription. c4 makes the design say that.
- **Starting the real loop with the Claude-first policy is the owner's.** The owner kept the running loop on `backend-policy.bedrock.json` (the design's status line, read at `05b9168e`), and a lane does not change the running process. The owner's step, outside every criterion here: once this item lands, start a loop that names `backend-policy.claude.json`, and add the first real run to the design's status line.
- **A real window cannot be spent on request.** The earlier c2 already allowed a simulation; c2 now says what the simulation is, so the design cannot record it as a real refusal.
- **The item's paths hold no test file.** `tests/tillr-muse-backend.test.py`, which holds the claude adapter's tests, is outside them, so c3 says which findings are fixed here and which are filed.
- **If the subscription's window is spent when the lane runs,** c1 cannot be met that day. The lane records the real refusal under c2 and reports blocked on c1, naming the reset time the stream reported.

## Filed 2026-11-15: anyone can ask why an item is not running

On 2026-11-15 five of ten lanes were idle and neither the status page nor the operator could say
why. The loop knew: it computes a reason for every item on every pass. But it records a reason
only when it acts on it, so the 17 items it had never acted on showed as "no pass yet", and the
only way to learn that five of them were waiting on the owner's rulings was a hand-written script
that ran the planner against the live store.

### Answer why any item is not running, from the planner itself




## Filed 2026-11-15: choose where each seat runs, by quota, and move work when a backend runs out

Principle (owner, 2026-11-15): the rule for assigning work to backends is simple and stays
simple. Is there subscription quota left? Then run there; if not, run on the paid backend; or
split the work between them. All backends are in use at once, each seat on the one it suits:
designers on the subscription's strongest model, builders on a cheaper model through pi.

Part of this exists. A policy file already maps each seat to an ordered list of backends, so
"designers on Claude, builders on pi" is one file today, and its `overrides` table sends one
named item to one backend. A backend whose window is spent is skipped until it reopens. What is
missing is the rest of the owner's list: the loop reads the policy once at start, it learns the
quota only from a run that was refused, it cannot split a seat's work between two backends, and
a run cut off when the window shuts starts again from nothing on the next backend.

### Re-read the backend policy when it changes, without a restart



### Add direct quota checks with a reserve

The drain loop currently learns that a backend has exhausted its subscription allowance only
when it refuses a run. That wastes a start and gives the owner no way to leave room for their
own sessions. Read the remaining allowance before choosing a backend, and let the owner reserve
part of each usage window. The waiting view should show how much has been used, how much is
reserved, and when work can resume, so the owner can understand why a backend is unavailable.

**Outcomes:**

- Check remaining allowance before assigning work rather than waiting for a refused run.
- Hold new work when usage reaches the owner's reserve threshold.
- Show usage, reserves, and reopening times in the waiting view.


**Agent Details:**

- **Ref:** [Claude backend design](../design/tillr-claude-backend.md#4-falling-back-when-the-window-is-spent) and the ruling below.
- **Where:** `tools/tillr-run/` (quota command and Claude adapter), `tools/tillr-drain/` (readings and dispatch decisions), `tools/tillr-view/` (waiting view), and `yolo-packs/tillr/` (delivery). Record the reading method and cost in the commit and, if no token-free method exists, in the [Claude backend design](../design/tillr-claude-backend.md).
- **Verify:** Check the five-hour and seven-day usage fractions and reset times, refresh and record readings after the configured interval, leave backends without a quota command unprobed, enforce the reserve threshold with a default of zero, and display usage, reserves, and reopening times. Keep the dispatch and failure cases in the ruling below.

**Ruling, 2026-11-17 (answers the second failed attempt):** two runs stopped. The first was refused
at its first message because the subscription's seven-day window was spent, which is the gap this
item closes and needs no ruling. The second built most of c1 to c4 and stopped on three things:
one failing test it had not changed, and two faults in a step the criteria never asked for. Its
work is kept at `refs/tillr/preserved/run-item-policy-quota-probe-a6`, made on an older trunk, so
the next run re-applies what it needs onto the current one and does not check that tree out whole.
Five points settle it:

1. **The quota is checked once, when the loop chooses and claims a backend, and never after
   staging.** The second run added a second check after an item was staged, and cancelled the
   staged dispatch when the backend had shut in between. That cancel is what lost a workflow's
   next step, left a design conflict waiting on a run that never started, and recorded one
   session id for a designer while dispatching another. The second check and the cancel are
   removed, not repaired. The plan and the claim transaction are the only places a quota reading
   or a reserve can stop a dispatch. A dispatch that has been claimed always starts.
2. **The reserve is a threshold on the last reading, not a guarantee.** A reading may be as old as
   the interval c2 names, and work claimed just before a window crosses the line still starts, so
   the loop can overshoot the reserve by the runs it started inside one interval. That is
   accepted. A run that then meets a spent window is refused by the backend and handled as today,
   and moving such a run is a later item's work, not this one's.
3. **A designer's replacement session is held before it is staged.** When a designer's resume has
   failed and every backend its seat may use is shut, the loop leaves the failed run's record
   unsettled and holds, as the second run already built in the plan. It is settled and replaced on
   a later pass, once a backend is open. No session id is minted or recorded for a replacement
   until that pass, so the recorded id and the dispatched id are the same one.
4. **A refusal still shuts any backend, with or without a `quota` command.** The second run made a
   refused start shut a backend only when its row named a `quota` command, and edited fixtures to
   opt in. That is withdrawn: it would let a backend that has just refused a run be sent the next
   one. "Treated as always open" in c2 is about readings only: a backend with no `quota` command
   is never probed and never shut by a reading or a reserve. No existing fixture changes to keep
   its refusal hold. A `quota` command that fails, or answers something the loop cannot read,
   shuts that backend alone until a later reading succeeds, and the waiting face shows the error
   in place of the fractions.
5. **The failing test is a race in the test, and this item fixes it.** The test is
   `TestRunOutputSurvivesLoopProcessExit` in `tools/tillr-drain/run_output_test.go`, which is
   inside this item's paths. Its wrapper script writes each signal file with a shell redirect,
   which creates the file before it writes to it, and the test reads the file as soon as it
   exists. The failure the second run saw, an exit status of `""` where `1` was expected, is that
   empty file. On 2026-11-17, at trunk `768aa883a`, `go test . -run
   '^TestRunOutputSurvivesLoopProcessExit$' -count=30` in `tools/tillr-drain` passed, so the trunk
   is not broken and the race shows only under the load of the full suite. The wrapper writes both
   the `ready` and the `finished` file under another name and renames it into place, in the same
   commit as this item's work. If the full suite then fails in some other test this item did not
   touch, the run reports `trunk-broken` with the test's name, and does not stop as blocked.

**Ruling, 2026-11-17 (answers the conflict raised by `run-item-policy-quota-probe-a13`):** the work is built, and no criterion or
earlier ruling changes. Runs `run-item-policy-quota-probe-a12` and `run-item-policy-quota-probe-a13`
each reported done with one commit, and each was refused by the gate on the `documents` check
alone. That check named two dead section links in this roadmap, in the blocks of two other items,
and nothing in either candidate. No other check that ran failed for either. The links
were dead at the base both runs were staged from, `f1647cf10`, because two headings had been
renamed, this item's among them. Landing a candidate also removes its item block from this file,
so the check read the whole roadmap for a change that had touched no document. Measured on
2026-11-17: `uvx vantage-check docs/tillr/roadmap.md` at `f1647cf10` reported those two
`link/dead-section-anchor` errors and no other, and the same command on the trunk of that evening
reported nothing to fix, after the trunk commit "docs(tillr): repair renamed-heading links and
unblock held item blocks". Four points settle it:

1. **The next run is staged from a trunk that holds the link repair, and is not staged from
   `f1647cf10`.** A candidate on the older base fails the same check whatever it contains. The
   commit that carries this ruling repairs the same two links with the trunk's own two lines, so
   that it can pass that check from the older base; they merge with the trunk's as no change.
2. **Start from the second candidate and do not write the item again.** It is the commit below the
   tip of `refs/tillr/runs/run-item-policy-quota-probe-a13`, titled "feat(tillr): probe backend
   quotas with owner reserves". The tip above it only removes this block and is not carried. Apply
   that one commit to the new base, regenerate the pack copy with `just pack-sync` and do not
   resolve it by hand, and give the new commit its own message, which keeps the reading method
   and its cost as c1 asks. On 2026-11-17 `git merge-tree --write-tree --name-only` of that commit
   against the trunk of that evening reported no conflict. A file that merges without a conflict
   can still be wrong against the newer trunk, so the full suites of the three modules decide.
   The first candidate, below the tip of `refs/tillr/runs/run-item-policy-quota-probe-a12`, is
   evidence only.
3. **The candidate's change to designer retirement is dropped, and the trunk's is kept.** The
   second run met a failure in `TestDesignerCompactRunsOnlyAtColdDispatch` and made
   `retireDesigners` in `tools/tillr-drain/drain/designer.go` return early while the loop has
   active work, with a test in `designer_retire_test.go`. The trunk has since repaired that race
   itself, in "fix(tillr-drain): keep a designer row while a landing rewrites its document". The
   next run keeps the trunk's `retireDesigners` and its tests as they are and leaves out the
   candidate's early return and the test added for it. The candidate's other changes in
   `designer.go`, which hold a failed designer's replacement while its backends are shut, are
   point 3 of the ruling above and stay. If that test or `TestCommentPathTwice` still fails in the
   full suite on the new base, the run reports `trunk-broken` with the test's name.
4. **The two repairs the reviews asked for stay, and so does the fixture repair.** A parallel
   workflow's sibling readers pass the same quota and reserve check as the primary request, so a
   sibling is not claimed on a backend that a reading or a reserve has shut. A cached reading whose
   values have not changed writes nothing to the store. The large-brief test fixture in
   `tools/tillr-run/` no longer inherits the enclosing session's continuation prompt. None of
   these changes a criterion, and point 1 of the ruling above still holds: a claimed dispatch
   always starts.


### Add backend shares to distribute seat work

A seat is a role in the workflow, such as implementation or review, and it can use several
execution services, called backends. Always choosing the first available backend concentrates
that role's work on one service. The owner needs a way to spread work evenly or give one
service a larger share, while keeping the existing ordered choice for policies that do not
request a split. When a service runs out of quota, the others should take its share until it
reopens. Restarting the loop or running two loops against the same store must not erase the
history used to keep those proportions.

**Outcomes**

- Distribute a seat's work by chosen shares or selection mode without changing existing policies.
- Keep dispatch history across loop restarts and share work among available backends.
- Reject invalid shares and record each backend choice so the owner can inspect what ran.

**Agent Details**

- **Where:** [Drain planner and model policy](../../tools/tillr-drain/),
  [delivery pack](../../yolo-packs/tillr/), and [documentation](../).
- **Verify:** Preserve ordered selection for lists without shares; check equal and 3:1 shares,
  persisted dispatch counts across two loops and a restart, quota/reserve redistribution,
  ordered refusal fail-over, and seat-named validation errors. The count fixture dispatches
  40 items; selection fixtures check a three-backend round-robin and 300 equal-weight random
  draws with 120–180 dispatches per backend. Check that each dispatch records its draw.



**Ruling, 2026-11-17 (answers the conflict raised by `run-item-policy-seat-shares-a6`):** the work
is built, and no criterion changes. Run `run-item-policy-seat-shares-a5` reported done with one
commit, and the gate refused it on the `documents` check alone. That check named two dead section
links in this roadmap, in the blocks of two other items, and nothing in the candidate; no other
check that ran failed. The links were dead at the base the run was staged from, `f1647cf10`, and
the [quota item's ruling](#add-direct-quota-checks-with-a-reserve) records the same refusal of
its own two runs. Run `run-item-policy-seat-shares-a6` ended five seconds after it started, wrote
nothing and named no blocker, so it is evidence of nothing about the item. Measured on 2026-11-17
at trunk `123aeab1b`: `uvx vantage-check docs/tillr/roadmap.md` reported nothing to fix. Four
points settle it:

1. **Start from the built candidate and do not write the item again.** It is the commit below the
   tip of `refs/tillr/runs/run-item-policy-seat-shares-a5`, titled "feat(drain): distribute seat
   dispatches by backend shares". The tip above it only removes this block and is not carried.
   Apply that one commit to the base the run is staged from, which holds the link repair, and
   give the new commit its own message.
2. **The two README conflicts are resolved by keeping both sections.** On 2026-11-17 `git
   merge-tree --write-tree` of that commit against trunk `123aeab1b` reported a content conflict in
   `tools/tillr-drain/README.md` and in its pack copy, and merged every Go file without one. The
   conflict is two sections added at the same place: the trunk's "Backend quota admission" and
   the candidate's section on shares. Keep the trunk's section whole and place the candidate's
   after it. Resolve the source README only, then regenerate the pack copy with `just pack-sync`;
   do not resolve the pack copy by hand.
3. **"Shut" in c3 means the trunk's combined set, and the candidate predates it.** The candidate
   was built before the quota checks landed, so the dispatch claim it adds in `drain/pass.go`
   reads `readShutBackends`, which knows only a backend held by a refused run. The trunk now has
   `readQuotaShutBackends` in `drain/quota_probe.go`, which adds a backend shut by a quota reading
   or by a reserve. Every place the candidate decides whether a backend is open (the share
   selection at planning, the claim, the review-seat check and the restart checks for a workflow
   and its parallel siblings) uses the combined set, so a backend shut by a reserve takes no
   share. This merges without a conflict and is still wrong until changed, so it gets a test
   written first: with shares of 1 and 1 and the first backend shut by a reserve alone, every
   dispatch goes to the second, and the first takes its share again once its reading reopens it.
   The quota item's rule that a claimed dispatch always starts still holds: a share is chosen
   among the backends open at the claim, and is not re-drawn after it.
4. **The full suite of `tools/tillr-drain/` decides, and a command limit is not a verdict.** The
   first candidate's full run passed 1,552 tests only on a rerun, after the 600-second command
   limit cut the first one off. Run the sharded suite as a background command with no limit
   shorter than the suite. If it fails in a test this item did not touch, on a base where that
   test also fails, the run reports `trunk-broken` with the test's name and does not stop as
   blocked.

### Move unfinished runs when backend quota runs out

When a backend runs out of its usage allowance partway through a run, the work already
completed should not be lost or counted as a failed attempt. The drain loop needs to stop
the current session safely and let the next backend assigned to that role continue from
the same files, with enough context to understand where the earlier session left off.
If no other backend is available for that role, keep the run paused until its original
backend can accept work again. A refusal before any work begins still follows the existing
route to the next backend; it does not need a continuation handoff.

- **Theme:** Model Policy
- **Outcomes:**
  - Continue unfinished work on the next assigned backend without discarding progress.
  - Pause runs that have no alternative backend and show when they can resume.
  - Keep each continuation within the original attempt and judge its result once.


**Agent Details**

- **Ref:** [The restart design](../design/tillr-restart.md) for stopping a session;
  [the quota probe item](#add-direct-quota-checks-with-a-reserve) for backend windows;
  the numbered criteria below for the continuation contract.
- **Where:** `tools/tillr-drain/`, `tools/tillr-run/`, `tools/tillr-view/` (the waiting face),
  `yolo-packs/tillr/`, and `docs/`.
- **Verify:** Cut a stub run after it writes two files and check that its replacement session
  finds both in the same lane and worktree, within the same attempt. Check that the first
  message identifies the quota interruption, old transcript, any report, and uncommitted
  work; that the loop does not reset or repeat work, spend a retry, or judge the candidate
  twice; that a run with no alternative pauses and resumes on the original backend with its
  reopening time visible; and that an initial refusal routes onward without a continuation
  message.

**Ruling, 2026-11-17 (answers the first run's fence conflict):** the first run stopped before
editing because c3 changes the waiting face, and the waiting face is built in `tools/tillr-view/`,
which the item's paths did not name. The run was right to stop. `tools/tillr-view/` is added to
this item's paths, and the delivered copy under `yolo-packs/tillr/` is synced in the same commit.
Three points settle how c3 is met there:

1. **A paused run is a row of its own.** Today `waitingBackendQuotas` in
   `tools/tillr-view/waiting_quota.go` writes one row per backend and says nothing about which
   runs wait on it. c3 adds one row per paused run, naming the run's item, the backend it waits
   on, and the reopening time. The backend rows stay as they are.
2. **The view reads the pause; it does not infer it.** The drain records that a run is paused
   and on which backend, in the store. The view shows that record. It does not decide a run is
   paused by comparing a running run against a shut window.
3. **One reopening time.** The paused run's row takes its reopening time from the same quota
   reading the backend's row uses, so the two rows cannot disagree. When that reading gives no
   time, the row says the reopening time is unknown, as the backend's row does.

No criterion changes.

**Ruling, 2026-11-18 (answers the conflict two failed attempts raised):** neither attempt found a
fault in c1 to c4, so they stand as written. Runs `run-item-policy-move-run-on-quota-a3` and
`run-item-policy-move-run-on-quota-a4` each read the code, wrote nothing, ran no test and named no
blocker, so they are evidence of nothing about the item. What they lacked was a place to start:
the block said what must hold and not which existing parts already do most of it. The facts below
were read at `3318def2f`. Seven points settle the build, in the order to build it. Each step gets
its failing test first, and the whole item is still one commit.

1. **Most of the machinery exists; this item joins it.** Four parts are already built and are
   reused, not rewritten:
   - `refusedWindow` and `recordBackendWindow` in `tools/tillr-drain/drain/quota_window.go` read
     a run's report for a rejected rate limit and record the backend's window.
   - `quotaReplacementBackend` in `drain/quota_probe.go` walks a seat's list from a named backend
     and returns the first open one, using the combined shut set.
   - `resumeAfterWall` in `drain/loop.go` runs a second session under the same run id, in the
     same lane and worktree, and `Request.ResumePrompt` carries its first message.
   - `prepareContinuation` in `tools/tillr-run/run/pause.go` already builds the first message of
     a fresh session: the reason, the old transcript, the worktree, the report when one exists,
     and then the brief. Today only an adapter whose `pause-kind` is `cannot-resume` reaches it.
2. **What is wrong today is one branch in `settle`.** A run that ends in the infrastructure class
   is sent to `nextAvailableRequest` under the next attempt's run id, after `preserveWork` and
   `resetLane`. That is right for c4 and wrong for c1 and c2: it discards the worktree and starts
   a new attempt. The new branch sits before it and takes a run for which both of these hold:
   `refusedWindow` reports a refusal, and the run did work. Every other infrastructure failure
   keeps today's branch unchanged.
3. **"Did work" is read from the tree.** A run did work when its lane's head has moved from the
   base it was staged on, or when `uncommittedPaths` returns at least one path. A run that left
   the tree as it found it follows c4, whatever it read or said: nothing is lost by starting it
   again, and the loop needs no reading of the transcript to decide.
4. **A continuation on another backend is a fresh session, asked for by the request.** The next
   backend is a different harness, so the old session id means nothing to it. `run.Request` gains
   one field that asks for a fresh session, and `prepareContinuation` takes its fresh-session
   branch when that field is set, whatever the adapter's `pause-kind` says. The loop sends the
   same run id with `Resume` set, that field set, the next backend's command, and a
   `ResumePrompt` that says the earlier session ran out of quota on the named backend and lists
   the uncommitted paths. The transcript, the worktree and the report are added by
   `prepareContinuation` as they are today. The loop does not call `preserveWork` or `resetLane`,
   does not raise the attempt, and passes the continuation's record to `settle` once, as
   `resumeAfterWall` already does. This is the test c2 names: a stub adapter writes two files
   and exits refused, and the stub for the second backend asserts both files and the message.
5. **"The window shuts" means a recorded refusal, and a reserve stops no running run.** A window
   shuts for running work when a run's refusal writes a row to `backend_windows`. A backend held
   only by a reserve, a stale reading or a failed probe admits no new dispatch, as
   [the quota item](#add-direct-quota-checks-with-a-reserve) built it, and leaves running runs
   alone. When a refusal is recorded, the loop sends `tillr run pause` to every other live run on
   that backend, in the same pass. A run that comes back `paused` for that reason is continued by
   points 3 and 4, or held by point 6. A run that finishes before the pause reaches it is settled
   as any finished run. The loop records that it asked for the pause and why, so a pause for
   quota is not continued as a pause for a restart.
6. **A paused run is a row the loop writes and the loop removes.** When `quotaReplacementBackend`
   finds no open backend, the loop writes one row to a new table holding the run id, the item,
   the lane and the backend, and keeps the lane and the item's `running` position. This is the
   record the first ruling's point 2 says the view reads. Each pass asks
   `quotaReplacementBackend` again for every such row, starting from the row's backend, so the
   original is preferred when it is open. When the answer is the original backend, the run
   continues its own session with `Resume` set and no fresh-session field, and a message saying
   the backend's quota reopened. When the answer is another backend, point 4 applies. The row is
   deleted in the transaction that claims the continuation. [The restart
   design](../design/tillr-restart.md#pausing-a-session) calls a paused run that nobody resumes
   within one pass a fault; a run with such a row is the exception, and the view shows it as
   waiting with its reopening time, not as a fault. A loop with only such runs waits for the
   earliest reopening time or a new item, and does not exit.
7. **Scope: the single run of an item.** c1 to c4 are judged on a run that holds a lane by
   itself, on both paths that settle it: `settle` in `drain/loop.go` and the pending-work path in
   `drain/pass.go`. A workflow node and a read-only parallel node keep the failover they have, in
   `drain/workflow_failover.go` and `drain/parallel_failover.go`. A design run answering a
   conflict keeps its own release on refusal. Changing those is not this item.

The full suite follows the rule the shares item records: run the sharded suite for each module
this item changes as a background command with no limit shorter than the suite, and regenerate
the pack copy with `just pack-sync`. The builder does not edit this block or any other part of
this file; the loop retires the block after landing.

No criterion changes.


The owner runs bake-offs: one model served by several providers, such as the DeepSeek routes on
OpenRouter, with work spread across them. Each provider is already its own pi model id, so each
is one backend row, and the `round-robin` and `random` picks above spread the work. tillr only
fires the runs. The comparison is done by a separate project of the owner's, from the run records
and streams tillr already keeps, so no statistics are built here (owner's ruling, 2026-11-15).

## Filed 2026-11-15: work the owner is waiting on never queues behind long work

Principle (owner, 2026-11-15, [item graphs](../design/tillr-item-graphs.md#71-decision-ledger)): a
lane belongs to one item while it is worked, and the queue must not suffer priority inversion.
When every lane is busy with long low-priority runs and something the owner is waiting on
arrives, such as review comments on a design, it starts at once.

### Add dedicated lanes for owner-facing work

When long-running work occupies every ordinary lane, an idea the owner saves should not have
to wait for that work to finish. Give the drain planner a separately configured group of lanes
for saved ideas and other explicitly marked work. These extra lanes do not interrupt active
workers or replace the dedicated lanes where designers answer review comments. Show both groups
separately so the owner can see which capacity is busy. While verifying this change, also repair
the designer-retirement race: a document briefly missing during a landing must not end its
designer's session.

**Outcomes:**

- Start owner-facing work even when all ordinary lanes are busy, without stopping active work.
- Show the extra lanes separately and keep review comments at their document's dedicated lane.
- Keep designer sessions alive while a landing rewrites their documents.


**Agent Details:**

- **Ref:** [Item-graph decision ledger](../design/tillr-item-graphs.md#71-decision-ledger),
  [saved-idea actions](../design/observatory-actions.md#53-the-item-that-is-created), and the
  designer-session rules for [the desk](../design/tillr-designer-sessions.md#the-desk) and
  [keeping sessions warm](../design/tillr-designer-sessions.md#the-keep-warm-rule).
- **Where:** [Settings reader](../../tools/tillr/settings/),
  [item reader](../../tools/tillr-item/item/sync.go),
  [drain planner](../../tools/tillr-drain/drain/plan.go),
  [designer retirement](../../tools/tillr-drain/drain/designer.go), and
  [view](../../tools/tillr-view/); regenerate [the pack copy](../../yolo-packs/tillr/).
  The candidate to reuse and conflict-resolution instructions are preserved in the rulings below.
- **Verify:** Check the five criteria below: configuration and item classification, dispatch
  with ordinary lanes occupied, lane eligibility and waiting, separate display counts, and
  designer retirement before and after a committed deletion.

**Ruling, 2026-11-17 (answers the first run's fence conflict):** the first run stopped because c1
needs two readers the fence left out. The settings file is read by `tools/tillr/settings/`, which
refuses any table other than `roll` and `push`, so it refuses `[lanes]`. An item block is read by
`tools/tillr-item/item/sync.go`, which refuses `express` as an unknown attribute. Both directories
are added to this item's paths. The work extends those two readers and adds no second reader of
either file inside the loop or the view.

**Ruling, 2026-11-17 (answers the third run's conflict on designer sessions):** the third run moved
review-comment work off its document's desk and onto the express lanes, and then stopped because a
comment turn that changes lane cannot find its designer's session: the pi adapter opens a session
by id in the lane's own directory (`pi --help`: "Use the current directory's existing session with
the ID, creating it otherwise"), so the turn starts cold while the record says `resumed-warm`. That
move was never asked for, and it is withdrawn. Three points settle it:

1. **Review-comment work stays at its document's desk.** A desk, defined in
   [the designer sessions design](../design/tillr-designer-sessions.md#the-desk), is the lane that
   belongs to one design document. It is already outside the lane cap, a comment run already starts
   without waiting for a numbered lane, and at most `comments.max_running` of them run at once.
   That is the behavior this item's principle asks for, and the session is found again because the
   desk never moves. This item changes nothing about desks, comment dispatch or session choice, and
   a comment item never takes an express lane or counts against `lanes.express`.
2. **The pi adapter is not changed, and `tools/tillr-run/` stays outside this item's paths.** With
   comment work at its desk no item changes lane between turns, so no session lookup across lanes
   is needed.
3. **Whether an item is express is read from the item itself, before workflow routing.** The third
   run's failing test showed that a saved idea lost its express lane once the first workflow node
   replaced its seat with `scout`. The loop decides express from the item's own stored definition,
   and a later change of seat by a workflow node does not change the answer.

c1 and c4 are reworded below to say this. c2 and c3 are unchanged.

**Ruling, 2026-11-17 (answers the conflict two more failed attempts raised):** neither attempt found
a fault in c1 to c4, so they stand as written. The item was built whole both times. One landing was
refused because the worker edited this block, and the other stopped on a test failure that this
item does not cause. Four points settle it, and c5 below is added.

- **The builder does not edit this block or any other part of this file.** The fifth run's
  conflict fix was refused with `worker edited its own item block`, because the ref it re-applied
  carried a cleanup of this block. The loop retires the block after landing. The `docs/` entry in
  this item's paths is for the tool READMEs and design documents the change must keep true.
- [unlanded] **Start from the built candidate.** Commit `09ee50b77`, the tip of `refs/tillr/runs/run-item-drain-express-lanes-a7`, holds the implementation, its tests, the drain README section and the pack copy, and it does not touch this file. Apply that one commit to the base and do not write the item again. Do not take `2ae758849`, the fifth run's fix, which carries the refused edit to this block. On 2026-11-17 `git merge-tree --write-tree --merge-base 09ee50b77^ 0ebcebe25 09ee50b77` reported content conflicts in two source files, `tools/tillr-drain/README.md` and `tools/tillr-drain/drain/plan.go`, and in their pack copies, which `just pack-sync` regenerates and nobody resolves by hand. Resolve both by keeping what the trunk added since and re-adding the candidate's change beside it. A file that merges without a conflict can still be wrong against the newer trunk, so the full suites decide and not the merge.
- **The failing test is a race in designer retirement, and it is on the trunk.** The seventh run's
  full drain suite failed `TestCommentPathTwice` once with `designer <nil>`: the designer's row was
  gone after a comment revision landed. `retireDesigners` in `tools/tillr-drain/drain/designer.go`
  removes a row on two grounds: the document is absent, or its stage is done. It decides "absent"
  with one `os.Stat` of the file in the main checkout. A landing runs beside the loop's passes and
  rewrites that file, and git replaces a file by removing it and writing it again. Measured on
  2026-11-17 with git 2.56.0: while a repository ran 100 `git merge --squash` landings that each
  changed one tracked file, a second process that did nothing but stat that file found it absent on
  6,459 of 5,619,879 reads. A pass that reads inside that window removes the row of a document that
  was never deleted. The express change does not touch `retireDesigners` or the landing, so the
  fault is the trunk's; the seventh run did not reproduce it in ten focused runs on either side
  because the window is narrow.
- **This item repairs the race, as c5.** The file is inside this item's paths, the repair is a few
  lines, and the candidate cannot be verified honestly while the test can fail by chance. The
  [designer sessions design](../design/tillr-designer-sessions.md#the-keep-warm-rule) ends a session
  "when its document is deleted", and deleted means deleted from the trunk. So the loop treats a document
  as deleted only when it is absent from the trunk commit the pass has just read and also absent
  from the main checkout. A file that a landing is rewriting is in the trunk commit before and
  after, so it is never taken as deleted. A document the owner has written and not yet committed is
  in the checkout, so its row is kept as it is today. The stage-done ground is unchanged. This is
  the one change to designer handling this item makes; the second ruling's points on desks, comment
  dispatch and session choice all stand.

The candidate also registers every configured lane in the store when the loop starts, without
replacing an existing lease, so that the view can show "0 of 2 express" before the first express
dispatch. That is accepted as part of c4. `retireDesigners` reads no lane rows, so it is not a
suspect in the failure above.

**Ruling, 2026-11-17 (answers the eleventh run's conflict on c5):** the eleventh run stopped
because c5 asked for two things that cannot both hold. The `deleted` case of
`TestRetireDesignerLeavesTranscript` in `tools/tillr-drain/drain/designer_retire_test.go` removes a
committed document's file from the main checkout, does not commit the removal, and asserts the
designer's row is gone. c5 requires that row to be kept in exactly that state, and it also said the
test passes unchanged. The run's focused test showed the contradiction: with the test unchanged, the
uncommitted removal still ended the session. The behavior in c5 is the ruling and the test's fixture
was the mistake, so c5 no longer asks for that test to pass unchanged. Three points settle it:

- **The `deleted` case commits the removal before the retirement runs.** That is the only change
  this ruling permits to the existing test. Its `done` and `live` cases, the row assertion for each
  of the three cases, and the assertion that the transcript is left in place all stay as they are.
- **The uncommitted removal gets its own test.** The new test c5 describes covers the row that is
  kept before the removal is committed and gone after. It is added beside the existing test and
  does not replace it.
- **`TestCommentPathTwice` still passes unchanged**, and nothing else in c1 to c5 moves. The rule
  for which document counts as deleted is the one the ruling above states.


## Filed 2026-11-16: two decided designs are cut into build items

Item graphs and the judge are decided and nothing of either is built. Each needs its design
turned into a build order of small items before any builder can start. These two items lived
inside the designs they rewrite, and the loop rightly refused a worker that edited the section
holding its own item, so on 2026-11-16 two design-seat runs were spent and nothing landed. They
are filed here instead, outside the documents they change.

### Cut the item graphs design into build items



### Cut the judge and lifecycle design into build items



## Filed 2026-11-16: the observatory stops being read-only

The owner wants to act from the observatory and not only look. Two actions come first, in the
owner's words of 2026-11-16:

> I want to be able to change the priority of an item. Probably just by dragging it. I'm not sure
> I love the numbered and letter weird prioritization stuff. I'm not sure we need anything other
> than just a ranking.

> I want to be able to create an item myself, so basically I want to be able to hit a button, give
> some indication of a priority, and then just dump in a whole bunch of rambling thoughts and hit
> save, and then have that go through that designer loop. It'll add a real title, description, all
> that stuff from my words. It'll of course save my words, make a design doc if it needs to. If it
> needs me, it'll then be blocked on me.

Both touch things that are not settled. The queue's order lives only in the store today, so a
drag would be lost when the queue is rebuilt from the documents, and the owner ruled on
2026-11-16 that a sync sets a place only for an item it imports for the first time
([the queue persistence design](../design/tillr-queue-persistence.md#91-decision-ledger)), so a
drag survives every sync but not a rebuild from nothing. An idea typed into a page has to be
saved somewhere the tree keeps. Vantage is no model for that: the owner stated on 2026-11-16 that
it never writes to the repository, so the observatory would be the first page that does
([the idea pipeline's ledger](../design/tillr-idea-pipeline.md#61-settled-decisions)). [The idea pipeline](../design/tillr-idea-pipeline.md)
already describes the flow from a sentence to a landed change, and
[item graphs](../design/tillr-item-graphs.md) describes the designer that fleshes an idea out. So
the first step is a short design that joins them, not a build.

That design is [the observatory's first two actions](../design/observatory-actions.md), and it
leaves no question for the owner. The order becomes one list: the owner sees a place, the stored
key is hidden, and a drag writes one item. An idea's words are saved as a text file before
anything else happens, and the design seat's landing carries them into the tree with the titled
items that came from them. The server runs the item tool for both and never writes the store or
the tree itself. Its eight build blocks live in
[the design's build order](../design/observatory-actions.md#10-build-order); the first, a
move-to-top control that works from the Items page, waits on nothing.

### Design the observatory's first two actions: reorder by dragging, and add an idea



### Cut the queue persistence design into build items



## Filed 2026-11-16: three built designs are owed a reference

A design that is built should become a reference a maintainer can read without the history of how
it was argued. Three designs are built and have none, so they sit in the planning index as owed.

### Write the references for the item states, usage capture and the upstream-pin check


**Ruling, 2026-11-16 (answers the third run's conflict):** the third run stopped because c3 named
`tools/fork-check.py` as the home of the upstream-pin rule, and that file's step 22 now reads
"RETIRED here: commit citations are a change declaration under tools/gate/checks/". The
upstream-pin rule is the one that lets a document cite a commit outside this repository's history
when the commit belongs to an upstream project. It is implemented in
`tools/gate/checks/change_common.py` (`UPSTREAM_REF` and `unreachable_citations`) and run by
`tools/gate/checks/citations.py`; c3 now names those files. The fix record still names the older
location, so the reference says the rule moved and the record is left as written apart from its
pointer. Reading those files needs no wider paths: the item writes only under `docs/`.

## Filed 2026-11-16: two outside skills join the default agent skills

### Add BMAD elicitation and Superpowers brainstorming skills

The owner asked on 2026-11-16 for two skills from outside projects to be available to every
agent that starts from the default packs, without a separate installation in each project. One
helps an agent draw out what a person means before it builds, and the other runs a structured
brainstorm. Both come from public repositories, and copying their files into this repository
would leave two hand-kept copies to drift. So each is named by its Git address and the packs
fetch it. The house brainstorming skill already exists and must keep working as it does today.
Adding the outside one beside it must never replace it or leave two skills a person cannot tell
apart.

This item was filed into the queue on 2026-11-16 without a block in this document, so the loop
dispatched it and its worker stopped after 34 seconds, reporting that the section its body names
does not exist. The block below is that section, restored with the same id, paths and criteria
the queue already holds.

<!-- tillr: item id="item-agent-default-bmad-superpowers-skills" paths="yolo-packs/,config/yolo-jail/,tag-personal/config/yolo-jail/,tag-work/config/yolo-jail/,tests/,docs/" blocked_by="" prose="2" -->

**Ruling, 2026-11-17 (answers the first run's conflict):** the first run stopped because this
document held no section under the heading above and no text about either skill. That was true
of the commit it was staged from, `094c6c322d`, which predates the commit that restored this
block, `8814931dc`. The section now exists at the anchor the run's brief names, with the same
id, paths and criteria the queue holds, so the brief and the tree agree and nothing in the
criteria changes. The next run builds from this block as written. It needs no wider paths: the
packs, the three configuration directories, `tests/` and `docs/` are all inside the item's paths
already.

**Ruling, 2026-11-17 (answers the third run's conflict):** the third run stopped because c2
named a BMAD directory that the public repository no longer has. That was checked again on
2026-11-17: `git ls-remote https://github.com/bmad-code-org/BMAD-METHOD.git HEAD` returned
`e435a22574fe55722841086e5566420178b98df3`, and a clone at that commit has no `src/` directory.
The same skill, still named `bmad-advanced-elicitation` in its own `SKILL.md`, now lives at
`skills/core-tools/bmad-advanced-elicitation`. The owner asked for the skill, not for a
directory, so c2 below now names the directory the skill is in today and the run builds against
that. Do not reach back to an older commit to recover the old directory: that would freeze the
skill at a version its authors have moved on from. The Superpowers directory, `skills/brainstorming`
at `22f0b463e7a5e579904dd6211a520321035526f7`, is where c2 says it is and does not change. If
either project moves its skill again, that is the unavailable source c5 already covers: the
launch must fail visibly, and the repair is a one-line change to the directory named in the pack.
Whether to name a fixed commit for either source is the builder's choice, within what the pack
format offers; neither answer changes a criterion.

- **c1:** The default selected packs expose BMAD Advanced Elicitation and Superpowers Brainstorming to their supported agents without a separate per-project installation.
- **c2:** BMAD is sourced through the Git URL https://github.com/bmad-code-org/BMAD-METHOD.git, selecting skills/core-tools/bmad-advanced-elicitation; Superpowers is sourced through https://github.com/obra/superpowers.git, selecting skills/brainstorming. Neither skill is vendored as a manually maintained local copy.
- **c3:** Each selected skill keeps the supporting assets, scripts, and relative references it needs; unrelated upstream frameworks and startup hooks are not imported automatically.
- **c4:** The existing house brainstorming skill remains unchanged and discoverable. Superpowers receives a distinct discoverable name if the source names collide; no silent replacement or ambiguous duplicate selection is accepted.
- **c5:** Permanent pack-composition/render tests verify both default discoveries, supporting resources, and collision handling. Every edited pack passes yolo pack lint, and an unavailable Git source produces a visible failure rather than omitting a skill silently.

**Agent details:**

- **Ref:** Owner request, 2026-11-16: add these two skills to the default pack-loaded set, using Git URLs.
- **Sources:** [BMAD Advanced Elicitation](https://github.com/bmad-code-org/BMAD-METHOD/tree/main/skills/core-tools/bmad-advanced-elicitation), [Superpowers Brainstorming](https://github.com/obra/superpowers/tree/main/skills/brainstorming).
- **Where:** Inspect the default pack list and existing skill contributions in `config/yolo-jail/`, scoped configuration under `tag-personal/` and `tag-work/`, and `yolo-packs/`. Preserve host-config-synced prose and skills under `yolo-packs/matt/`; do not edit deployed home directories.
- **Verify:** Resolve the supported Git locator syntax, test the default composition for supported agent renders, validate all auxiliary references, and lint each changed pack. Name the actual render and binary tested; in-jail lint is not proof of host rendering.

## Filed 2026-11-16: defects the first observed drain pass turned up

The first drain pass the owner watched on 2026-11-16 ran the queue as it stands and found three
defects. None stops the drain from running, so they go through the queue like any other work.
Each is a place where the loop does something a person watching would not expect, and where
nothing tells the person it happened.

<a id="hold-an-item-that-has-no-document-block"></a>

### Route an item with no document block to triage

An item can reach the queue without a block in any tracked document, because the item tool's
apply face writes the store directly. The loop then treats it like any other item and sends a
build worker to read a section that does not exist. On 2026-11-16 that happened to
`item-agent-default-bmad-superpowers-skills`: it had been in the store alone since 2026-11-15, the
loop dispatched it, and its worker reported blocked after 34 seconds. The item tool's sync face
already counts these items as `unshared_open`, and on that day `tillr-item sync --dry-run` printed
`unshared_open: 3`, but nothing acts on the count.

This item first asked the loop to hold such an item until a person wrote its block. The owner
answered on 2026-11-17 that a hold nobody acts on is the wrong outcome: "if it ends up like this,
it's probably that's some half-baked request for me and it should be able to go to ... the next
step, which is a design agent or a triage agent or something like that. Like there should always
be a next step that's clear for these items and we should handle them somehow." So an item with
no block is read as a request that nobody has shaped yet, and its next step is a triage run on the
design seat. That seat already answers a worker's design conflict by amending the item's block and
landing the amendment through the gate, and on 2026-11-17 it did exactly that for the same BMAD
item. The triage run's product is the missing block, written with the item's own id so the next
sync binds the stored item to it. When the request is too thin to shape without the owner, the
run writes one question to the owner instead, so the item waits on a question a person can see
and answer rather than on nobody.

An item applied straight to the store is one of several ways work arrives, and the owner said on
2026-11-17 how they fit together: "You can imagine a triage subgraph which has the same output
shape as like a human half-baked triage has the same output shape as like an agent filed item,
even though they go through different paths to get there. The downstream stuff doesn't care
because it's ultimately aligned in the interface." An item an agent filed without reading the
prose guide is another such door, handled by the queued
[rewrite before build](#file-a-rewrite-for-an-item-that-fails-the-prose-check-ahead-of-the-item-itself),
and a rough idea the owner dumps in conversation is a third. Another session is recording these
intake doors in the design documents on 2026-11-17. This item builds only the door for an item
with no block, and it defines that door by what it must hand on: the same ready-item shape every
door ends in, which is what a build run needs today.

- A block in a tracked document, with the item's own id. Its heading starts with a verb from the
  prose guide's list and runs to at most twelve words, which the sync warns about otherwise.
- A prose paragraph before the directive that says why. The sync warns when there is none, but it
  checks only that a paragraph is there.
- A fence of allowed paths. The sync refuses a block that has none or names an invalid path, and
  the planner holds an item whose fence admits nothing.
- Dependencies in `blocked_by` that name items. The sync refuses one that is not an item id, and
  the planner holds the item until each one is finished.
- Between one and eight criteria. The sync refuses a block with none or with an empty one, and it
  warns about more than eight.
- The prose guide's current mark, which the sync reports as `prose-unmarked` when it is missing.
- No open question on the item's document, which the planner otherwise holds as `question-open`.

Nothing checks yet that a criterion can be checked, that the paragraph really says why, or that
the fence covers the files the work needs. Today a build run finds those out by stopping with a
conflict.

The owner said on the same day where a rule like this belongs: "I guess retention policy, item
lifecycle, item prerequisites, that sort of thing should be part of this custom, this layer that
describes your workflow use case, not the actual core." An item needs a document block before it
is built because this workflow lands changes in a Git repository from documents, so the rule is a
prerequisite of that workflow and not of the queue itself, as
[the core and the landing definition](../design/tillr-core-and-git-landing.md#2-the-workflow-the-landing-definition-wraps-as-it-runs-today)
records. That is why it applies only to a store
that documents feed, the narrowing made on 2026-11-17: Product QA files its items with the apply face
into a store of its own that nobody syncs, and that store is left alone. Question
[`OQ-FQ8`](../design/tillr-separable-parts.md#OQ-FQ8) in the separable-parts design stays, blocked until AutoQA is picked up, on
one point only: whether AutoQA writes a block for each bug it files into tillr's own store, or
files it with the apply face and lets triage write the block.



**Agent details:**

- **Ref:** the owner's two statements of 2026-11-17, quoted above. The design-seat conflict path this reuses is the one a worker's blocked report takes today; the idea pipeline's stage 2 already rules that a request too vague to split becomes a one-question document that holds on the owner ([the idea pipeline](../design/tillr-idea-pipeline.md#2-the-pipeline-and-one-writer-rules)), and this item applies that disposition to an item already in the store. That pipeline's `intake` face and item kinds are not built, so nothing here waits on them. `docs/design/queue-persistence-ruling.md` names the `unshared_open` counter and its intent. The proposed `plan` seat in [planning and model tiering](../design/tillr-planning-node-model-tiering.md) is not in the seat vocabulary yet; if it lands first, the builder says whether triage belongs on it.
- **Where:** `decide` in `tools/tillr-drain/drain/plan.go`; `conflictDecision` in `tools/tillr-drain/drain/blocked.go` (the design-seat dispatch, and the owner routing for an empty body reference or a design run that ends blocked); the conflict brief and its one-document fence in `tools/tillr-drain/drain/stage.go`; the `unshared_open` count in `tools/tillr-item/item/sync.go`; the waiting face in `tools/tillr-view/waiting.go`; the workflow files under `config/tillr/workflows/` if the prerequisite is expressed there; the pack's copy under `yolo-packs/tillr/`.
- **Open point, one agent or two:** the owner has not said whether making a request well-formed and shaping rough text into designable work are one agent's job or two. This item lets one triage run on the design seat do both for this door and does not decide the question. The rewrite before build sends its rewrite to a cheap seat as a separate item instead, so whether the two doors share one rewriting seat belongs to the same open point. Both doors end in the same ready-item shape.
- **Naming:** the owner asked on 2026-11-17 why the intake face is called `apply` and not `add`. Do not rename it in this item; report what a rename would touch: the face, its callers and tests, the docs and pack skills that name it, and AutoQA's design, which files with it.
- **Evidence:** 2026-11-16, `tillr-item sync --dry-run` against this repository's store printed `unshared_open: 3`; run `run-item-agent-default-bmad-superpowers-skills-a1` ended `RESULT: blocked` after 34 s.
- **Design answer, 2026-11-17, to the conflict two failed attempts raised:** neither attempt found a fault in the criteria, so c1 to c8 stand as written. The first attempt built the whole item and passed its gate, and its landing was refused only because the trunk had moved under one test file. The second attempt wrote the item again from nothing and stopped on a test that is red on the trunk. The builder therefore carries the built candidate forward, as the two points below say, and does not write the item a third time.
- [unlanded] **Start from the built candidate:** commit `18e8f50f9`, the parent of the tip of `refs/tillr/runs/run-item-sync-hold-unshared-items-a1-cfix`, holds the implementation, its tests, the drain README section and the pack copy. Apply that one commit to the base; do not take the ref's tip `bfe5f8934`, which only retires this block. On 2026-11-17 `git merge-tree --write-tree --merge-base 18e8f50f9^ eed8cffa8 18e8f50f9` reported content conflicts in five source files: `tests/tillr-docs.test.py`, `tools/tillr-drain/README.md`, `tools/tillr-drain/drain/loop.go`, `tools/tillr-item/item/sync.go` and `tools/tillr-view/status.go`. It reported the same conflicts in their pack copies, which `just pack-sync` regenerates and nobody resolves by hand. In `tests/tillr-docs.test.py` the conflict is two independent tests added at the same place, and both stay. Resolve the other four by keeping what the trunk added since and re-adding the candidate's change beside it. Run again on 2026-11-17 with `d0eb7b405` in place of `eed8cffa8`, the same command reported the same five source files and their pack copies and no others. A file that merges without a conflict can still be wrong against the newer trunk: `d0eb7b405` changed `tools/tillr-drain/drain/plan.go`, which the candidate also changes, so the suites below decide and not the merge. After resolving, run the candidate's own tests (`go test ./... -run 'Triage|DocumentFed|DocumentFree|Unshared'` in each of the three modules) and the three full suites.
- **Check the trunk before building:** the second attempt's stop was a red test in `tools/tillr-drain`, `TestTheSeedFileDispatchesTheRuledItems`, which failed at `eed8cffa8` and at `bb6e286b5` on a seed criterion that recorded an owner decision without a claim class. That test no longer exists under that name, and a later attempt stopped on 2026-11-17 because this block still told it to run the old name, which matches no test and exits 0 with `[no tests to run]`. The trunk repaired the fault and split the check in two: `TestTypedFrozenSeedDispatchesTheRuledItems` and `TestFrozenSeedRefusesUntypedOwnerCriteria`, both in `tools/tillr-drain/seed_test.go`. Run on 2026-11-17 at `d0eb7b405`, `go test . -run '^(TestTypedFrozenSeedDispatchesTheRuledItems|TestFrozenSeedRefusesUntypedOwnerCriteria)$' -v` in `tools/tillr-drain` printed `--- PASS` for both. Run that command on the staged base first, which takes about a second, and read the output for two `--- PASS` lines, because a pattern that matches nothing also exits 0. If either test fails, report the trunk as broken with that output before doing any other work. If both pass, build. If either name is gone again, that is not a reason to stop: find the test in `tools/tillr-drain/seed_test.go` that loads `config/tillr/seed-items.json`, run it, and name in the report the test that was run.
- **One trap the second attempt found:** `tillr-view` reads stores it must never migrate, and importing `tools/tillr-item/item` into it registers that package's writer schemas and breaks the view's legacy-store tests. The candidate avoids this by putting the comparison of store items against document blocks in the separate package `tools/tillr-item/docshare`, which the view, the item tool and the drain all import. Keep that split.

### Add the spend of killed runs to the record

A run that is ended before it finishes can leave no cost in the record. The loop's totals then
show less than was spent, and the error grows with the number of runs that are ended. On
2026-11-16 the drain recorded `item-drain-recover-commands` at $10.70 over three runs and
`item-sync-design-without-item` at $0.67 over seven runs, and the runs that were killed in both
recorded nothing. Since then a loop that stops or exits on a fault leaves its workers running:
each one finishes and writes its own record, cost included, and the next loop settles it. Two
gaps remain. An operator who stops the loop and asks it to end the running workers gets records
with no cost, because the cost is read with a request that the stop has already canceled. A
worker whose runner dies outright leaves no record at all. A second effect is now the ordinary
case: a new loop that settles a run an earlier loop started counts that run's spend in its own
pass record, so a pass looks more expensive than the work it did. The record should have a number
for every run however it ended, and should say which spend a loop inherited.


**Ruling, 2026-11-17 (answers the first run's conflict):** the first run stopped because c4 asked
a spend ceiling to count killed runs, and the tree has no ceiling.
[The momentum principle](../design/momentum-principle.md#what-this-forbids) forbids a spend or
token ceiling, and two tests in `tools/tillr-drain` hold that line:
`TestTheSpendLineNamesTheMeasurement` fails a spend line that names a ceiling, and
`TestTheLoopCommandRefusesTheDeletedBudgets` refuses the deleted budget flags. c4 is therefore
replaced by a criterion about measurement only. This item adds no ceiling, no hold, no setting and
no stop; it makes the totals complete, whoever reads them. c1 to c3 are unchanged.

**Ruling, 2026-11-17 (answers the third run's conflict):** the third run stopped because the
criteria assumed that a loop which stops or faults kills its workers. It does not. The loop
starts each run face outside its own context and process group, a stop or a fault ends only the
loop's wait, and the next loop settles the run from its terminal record; `launchRun` in
`tools/tillr-drain/drain/pass.go` and `ProcessSteps.Run` in `tools/tillr-drain/drain/steps.go`
both say so. That behavior stays, and this item must not make a loop stop or a fault signal a
worker. A run is ended early in exactly two ways, and the criteria below are rewritten to name
them. This ruling replaces the sentence above that left c1 to c3 unchanged.

- **An operator's end.** `tillr drain stop --end-runs` is the only drain command that signals a
  run face (`EndRuns` in `tools/tillr-drain/drain/stop.go`). The face writes its terminal record
  on a context detached from the canceled one, but reads the spend on the canceled one
  (`readSpend(ctx, ...)` in `tools/tillr-run/run/exec.go`), so the extractor is canceled with
  the run. The third run read this in the code and did not reproduce it; c1's test is the
  reproduction, written first.
- **A dead run face.** A face that is killed outright, or lost with its host, writes no terminal
  record. The next loop finds the claim's process gone and records the run as orphaned
  (`reapDeadLoopRuns` in `tools/tillr-drain/drain/orphan.go`). Such a run is `unreported`. This
  item does not make the loop run an extractor over a dead face's log.


**Agent details:**

- **Ref:** the consumers of these totals are the loop's spend line (`spendLine` in `tools/tillr-drain`) and the spend lines in `tools/tillr-view/`. `item-drain-spend-ceiling` in this document is not a consumer this item builds for: it asks for a ceiling the momentum principle forbids, and that contradiction is that item's to settle, not this one's.
- **Where:** the spend read in `tools/tillr-run/run/exec.go` (`readSpend` and its caller, beside the detached `writeCtx`); the run-finish and orphan records in `tools/tillr-drain/drain/` (`EndRuns` in `stop.go`, `reapDeadLoopRuns` in `orphan.go`); the pass record and the spend lines in `tools/tillr-view/`; the pack's copy under `yolo-packs/tillr/`.
- **Evidence:** 2026-11-16, `tillr view events --since 0`: `item-drain-recover-commands` showed $10.16 and $0.54 over three runs, the third killed at 08:49:05 UTC by loop 459 and recording nothing; `item-sync-design-without-item` showed $0.14 and $0.53 over seven runs, five of them killed. These figures were recorded before the loop stopped signaling its workers, so they are the history behind the item and not a description of the tree; a fixture must not reproduce a loop fault that kills a worker. The loop's current behavior is stated in `tools/tillr-drain/README.md`: "`tillr drain stop --end-runs` is the only drain command that ends a run."

### Hold an item whose runs keep being orphaned

**Withdrawn, 2026-11-18.** This item asked for an `orphan.limit` count, defaulting to three, after
which an item whose runs a loop stop had orphaned would wait for a person. The owner's ruling of
2026-11-17 removes the premise, and the item is not built:

- No attempt counts, retry budgets or "exhausted" states exist
  ([R1](../design/tillr-graph-is-the-path.md#1-the-direction-and-the-rules)).
- A killed or interrupted run resumes as the same node, nothing is counted, and the interruption is
  logged ([R5](../design/tillr-graph-is-the-path.md#1-the-direction-and-the-rules); the same shape
  for a design run is [Section 4.3, D-JL9](../design/tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9)).
- An item reaches the owner only through an explicit objection that names him, recorded as an open
  question in a design document
  ([R3](../design/tillr-graph-is-the-path.md#1-the-direction-and-the-rules)). A count reaching a limit
  is not such an objection.

An item that is orphaned again and again is a fault in the loop to fix, not a state of the item to
hold. The item is `resolved-elsewhere` in the store with this ruling as its evidence, its block is
removed from this document, and its unlanded candidate (the preserved ref
`refs/tillr/preserved/run-item-drain-hold-repeatedly-orphaned-a5-cfix`) is not to land.

## Filed 2026-11-16: work the queue triage found stranded

A read-only triage of the whole queue on 2026-11-16 found thirteen items held as `conflict-design`
with nothing able to move them, one held as `retry-stale` after its conflict was answered, and one
item whose worker built the change seven times and never committed it. The conflicts were answered
from their documents by hand that day. The three defects below are what let them strand, so the
next loop does not strand them again.

### Route design work past a design seat that is out of quota

The model policy lists the design seats in order, and the drain planner always sends a conflict's
design run to the first one. It does not check whether that seat's quota window is shut, and a
design run that ends on a usage limit is treated like a design run that answered nothing. On
2026-11-16 the first design seat was Claude, and Claude's weekly limit had been reached. Between
07:55 and 08:22 UTC the drain sent thirteen design runs to it, each one ended at once with exit 1,
and every one of the thirteen items then waited as `conflict-design` with its conflict marked as
awaiting an answer that could never come. A second design seat, on Bedrock, was in the same policy
and idle the whole time. A conflict should go to a design seat that can take it, and a design run
refused by a limit should leave the conflict free to be sent again rather than awaiting forever.



**Agent details:**

- **Ref:** the design dispatch in `conflictDecision`, `tools/tillr-drain/drain/blocked.go`, which calls `p.policy.seatList("design")[0]` and holds every status other than `raised`; the quota windows the build dispatch already consults in `tools/tillr-drain/drain/plan.go`.
- **Where:** `tools/tillr-drain/drain/blocked.go`, `tools/tillr-drain/drain/plan.go`, the design-run settle path in `tools/tillr-drain/drain/loop.go`; the waiting face in `tools/tillr-view/waiting.go` for the reason text.
- **Evidence:** 2026-11-16, `tillr view events --since 0`: thirteen `run.finish` records on `claude-designer`, started 07:55:43 to 08:22:52 UTC, each `exit_code` 1 with `end_cause` `normal-exit`; each run's log ends `You've hit your weekly limit · resets Nov 19, 8am (America/New_York)`. `tillr drain why --store .tillr/state.db` then held all thirteen items as `conflict-design` with their conflicts `awaiting`, while `config/tillr/backend-policy.mixed.json` lists `pi-designer` as the second design seat.

### Prevent an answered conflict from holding its item as retry-stale

An operator's retry names the run it replaces, and the planner refuses a retry as `retry-stale` once
a newer run exists, because the retry no longer describes the item's latest state. There is a guard
for the case where the newer run is a design run that answered a conflict: the answer should win.
The guard compares the retry against the run that raised the conflict, but the run that used up the
retry can be a different, earlier run. On 2026-11-16 that left `item-jl-criterion-claim-class`, the
first item of the judge-and-lifecycle chain, held as `retry-stale` with its conflict answered and
its answer landed, so five items behind it could not start. The planner should send an item whose
conflict is answered to its build, whatever retry its earlier runs used.



**Agent details:**

- **Ref:** `retryFor` in `tools/tillr-drain/drain/plan.go`, whose guard reads `p.inputDecisions[c.RunID]`, the conflict's raising run, instead of every run since the retry.
- **Where:** `tools/tillr-drain/drain/plan.go` and its tests.
- **Evidence:** 2026-11-16, `tillr view events --since 0`: retry `retry-item-jl-criterion-claim-class-run-item-jl-criterion-claim-class-a6` was used by run a7, which hit a squash conflict; conflict `conflict-run-item-jl-criterion-claim-class-a7-cfix` was raised by run a7-cfix and marked `answered` at seq 3414 after design run a9 landed (commit `60ea6e236`). `tillr drain why --store .tillr/state.db` then held the item as `retry-stale`, waiting on `run-item-jl-criterion-claim-class-a6`.

### Prevent a build run from ending with passing work uncommitted

A build worker is asked to commit one candidate in its lane, and the drain reads that commit as the
run's result. When the worker ends its turn before committing, the run is recorded as a refusal
with no candidate, and everything the worker built is thrown away when the lane is next staged. On
2026-11-16 this happened to `item-obs2-stop-says-what-and-next` seven times, at a cost of $9.89.
Its last run implemented the change, passed 300 browser tests and a production build, staged the
files, and then ended its turn with the words "the drain suite is still running; I'll commit only
after its result is known". The work was real and was lost. A run should not end as "no commit"
when the lane holds uncommitted work that would pass.

There are several ways to get there, and this item does not choose among them. The brief could
say more plainly that the session ends only after the commit. The run face could notice a dirty
lane when the worker exits and continue the same session with a message to finish and commit. Or
the run could end with a pause-kind continuation, the way a paused run is resumed on the same
attempt, so the next dispatch picks the session up instead of restaging the lane.



**Agent details:**

- **Ref:** the `pause` face and `pause-kind` in `tools/tillr-run/README.md`; the `candidate-no-op` reason in `tools/tillr-drain/drain/schema.go`.
- **Where:** the run face in `tools/tillr-run/` (exit handling and the pi adapter), the candidate settle in `tools/tillr-drain/drain/`, and the worker brief the drain writes.
- **Evidence:** 2026-11-16, `tillr view events --since 0`: seven runs of `item-obs2-stop-says-what-and-next` on `amazon-bedrock/global.openai.gpt-6.1-sol`, every one refused at step `no-op`, $9.89 in total, run a7 alone $7.61. The a7 run report recorded `complete: no` and ended with the worker's sentence quoted above.

## Filed 2026-11-16: defects the second observed drain pass turned up

The second drain pass the owner watched, early on 2026-11-17 UTC, stopped at its first landing on
an argument the land adapter did not accept; that fault is fixed in the same change that files
these items. The pass turned up four more defects. None of them stops the drain, so they go
through the queue. Each is a place where the record or the screen tells a person something other
than what happened.

### Prevent the rolling watermark from advancing past unaudited trunk

The rolling run is the drain's audit of the trunk itself: it runs the checks that are too slow
for each landing, and when it passes, the drain moves its watermark to the trunk tip, meaning
"everything up to here has been checked". The drain's own Go suite is one of those slow checks.
With no watermark yet, the drain asks for the whole tree, but the repo gate reads that request as
"everything since the gate's own last verified baseline". That baseline already held the trunk's
tree, so nothing counted as changed. The first rolling run ever to pass, on 2026-11-17, went green
in about 12 seconds having run 6 checks, 4 of them answered from cache, and skipped 54 as "no
change". The watermark still advanced to the tip. Nothing in the record says the audit was a near
no-op, so a person reading "green" believes the trunk was checked when it was not. A run with no
watermark should audit the whole tree, or the watermark should record only what was proven, and
the loop's line should say how much actually ran.



**Agent details:**

- **Ref:** the rolling tier and its watermark in `tools/tillr-drain/README.md`; `{since}` in `DefaultRollingProgram`, which becomes `--all` when there is no watermark.
- **Where:** `maybeRolling` in `tools/tillr-drain/drain/trunk.go` and `Rolling` in `tools/tillr-drain/drain/steps.go`; the `--all` planning in `tools/fork-check.py`.
- **Evidence:** 2026-11-17 02:06:27 to 02:06:39 UTC, `tillr drain loop` printed `rolling run green over ..01142982834a; watermark advances to 01142982834a`. The rolling span file it wrote, `.tillr/lanes/.rolling/gate.spans.json`, showed the plan `tier rolling, changed 0`, 6 checks executed (4 cached) and 54 skipped with "no ... changed" reasons, wall time 4.4 s.

### Prevent a conflict's routing change from reading as a duplicate

When a run reports that its item's definition is in conflict, the item tool records the conflict,
and it records it again each time the conflict moves: sent to a design seat, answered, closed.
Each of those records repeats the whole conflict, including the full run report. On 2026-11-17 the
conflict on the BMAD skills item was recorded at 02:06:39 as raised and again at 02:06:45 when the
loop sent it to a design run. The two records differ only in their status and the design run they
name, so anyone reading the event list sees the same conflict written twice and cannot tell a
routing change from a bug that wrote it twice. Nothing refuses a record that repeats the last one
exactly, so a real double write would look the same. A conflict should read as one conflict with
its transitions, and a repeat that changes nothing should not be written.

One record is neither a routing change nor a repeat. When an item fails a second time while its
conflict is still open, the drain writes the conflict again with the same status, design run and
answer but a longer report and the newer run's id. That record carries evidence the first one
lacks, so it is kept, and the views show it as evidence added to the same conflict. A repeat is
therefore a record equal to the last one in every field, not only in the three routing fields.



**Agent details:**

- **Ref:** `ConflictKind` and `RecordConflict` in `tools/tillr-item/item/conflict.go`, whose comment already says each routing change is recorded.
- **Where:** `tools/tillr-item/item/conflict.go`, the drain's conflict routing in `tools/tillr-drain/drain/blocked.go`, and the events and waiting faces in `tools/tillr-view/`.
- **Evidence:** 2026-11-17, `tillr view events --since 4243`: events 4269 (02:06:39.497 UTC) and 4275 (02:06:45.327 UTC) are both `item.conflict` with id `conflict-run-item-agent-default-bmad-superpowers-skills-a1`; the first has status `raised`, the second `awaiting` with design run `run-item-agent-default-bmad-superpowers-skills-a2`, and every other field is equal.
- **Settled by the first attempt's conflict (2026-11-17):** that attempt compared only status, design run and answer, which suppressed the evidence record `routeSecondFailure` writes in `tools/tillr-drain/drain/second_failure.go`, and it then reshaped the drain to compensate. Compare the whole `Conflict` record instead and leave `routeSecondFailure` writing as it does; `TestSecondFailureRoutesToDesign` should pass unchanged.
- **Verification scope:** run the Go suites from `tools/tillr-item`, `tools/tillr-drain` and `tools/tillr-view`. The pack copy is verified by `just pack-sync` and `python3 tests/pack-sync.test.py` only. Do not run `go test` inside the pack copy: at `6a9c634c`, before any change, `go test ./...` in `yolo-packs/tillr/files/local/share/tillr/tillr-item` already fails `TestSyncOverThisRepositorysDocuments`, because its `--root ../../..` resolves to the repository's top level only from `tools/tillr-item/item`. That failure is not this item's and is not a stop condition for it.

### Add the live loop's session and leases to the running view

The running view is where an operator looks to see whether a loop is alive and what it is doing.
While a loop was running on 2026-11-17, the view showed the stop record of the loop before it, an
operator stop from 00:57 UTC marked as stale, and printed "lease not recorded" for the run that
loop had just started. The operator watching had to read the process list to learn the loop was
alive. A view that shows an old stop while a loop runs is worse than no view, because it tells
the person to start a loop that is already there. The running view should show the live loop's
session and the leases of its runs.



**Agent details:**

- **Ref:** the loop session records the drain writes (`refreshSession`, `endSession`) and the stop records under `.tillr/loop/`.
- **Where:** `tools/tillr-view/running.go` (the `StaleNote` and `leaseLeftText` paths) and `tools/tillr-view/web/src/models/now.ts`.
- **Evidence:** 2026-11-17 02:07:59 UTC, during a live loop, `tillr view running` printed `loop: operator-stop (at 00:57:09Z)` with `stale stop record — newer run exists at seq 4222`, and `lease not recorded` for run `run-item-agent-default-bmad-superpowers-skills-a2`.

### Add a startup check that step programs accept the loop's flags

The loop hands each candidate to programs it is given on its command line: a gate, a lander and a
verdict program. It adds flags to their command lines as its features grow, and a program that
has not learned a new flag refuses the whole command. On 2026-11-17 the land adapter refused a
flag the loop had been sending for a day, and the loop found out only after a model had spent 91
seconds and $1.41 on a run and its candidate had passed the gate. Stopping the loop on that
refusal is right, because every landing would fail the same way. Finding out at the first landing
is late. The loop's existing start-up check reads its settings, policies and store, but never asks
the step programs whether they accept what it will send. It should, before any run starts.



**Agent details:**

- **Ref:** the step-program exit codes in `tools/tillr-loop/README.md`; the loop's `--check` face in `tools/tillr-drain/main.go`; the argument lists pinned in `tools/tillr-loop/drain-argv.json`.
- **Where:** `loopFace` in `tools/tillr-drain/main.go`, `ProcessSteps` in `tools/tillr-drain/drain/steps.go`, and the adapter's `self-check` face in `tools/tillr-loop/tillr-loop`.
- **Evidence:** 2026-11-17 02:08:40 UTC, `tillr drain loop` stopped with exit 4 and `the land program answered exit 2 for item-agent-default-bmad-superpowers-skills: ... unrecognized arguments: --observed-trunk 01142982834a72e786616b25458aac140be0bd6d`, after design run `run-item-agent-default-bmad-superpowers-skills-a2` (91 s, $1.413319) and a passing gate at 02:08:36 UTC.

### Route a landing around the owner's uncommitted edits

The owner expects to edit, pull and commit in the main checkout while the loop runs, without
warning, and expects a landing to cope. On 2026-11-17 the owner said: "we're supposed to be able
to ad hoc land stuff. and if the drain needs to land, didn't we decide autostash equiv was ok?
like you pulled some remote change when work was going on, which I will also do without warning
sometimes." They did decide it. The old board design ruled on 2026-10-31 that the landing
stashes the owner's uncommitted work, moves the trunk and re-applies the work, as `git pull
--autostash` does (decision D33 in `docs/design/board-state-machine.md`, section 9, built as
task P10 in `docs/design/board-state-machine-plan.md`). The move to tillr dropped it with the
old board tool (`docs/design/tillr-seed-triage.md`, row 592), and nothing replaced it.

**Build this item with the steps below. Read this section first; it is the ruling of
2026-11-18, amended the same day to answer the latest build's conflict.** Everything in this block after this
section is the record of how the change was designed, and a builder does not act on it: not on
the rulings, and not on the "Plan for the builder" under "Agent details", which this section
replaces. No criterion, path or rule changes here.

Two builds have now stopped after carrying out step 1 alone. Each restored the preserved tree of
an earlier build, wrote nothing else, and reported as beyond it the failures that step 2 repairs.
The first reported the lander suite's `index.lock: File exists` and the stall in
`TestFileAndDirectoryExchangesFollowTheOwnersContent`. Both are what the plan's steps 2 and 5
repair, and neither step had been carried out. The plan sat under "Agent details", after all of
the history, and it described each change in words for the builder to write. So the commands now
come first and the changes are given as a patch. The design seat made the changes of that plan,
and the README text it had left unwritten, in a clone at `55db82bc5`, ran the suites on the
result, and printed the difference at the end of this block: 8 files under `tools/tillr-land/`,
534 lines added and 5 removed. After step 2 no code and no prose remains to be written.

The second of those builds had this section in front of it, and its tree shows that it still
stopped after step 1. Its preserved copy differs from the result of steps 1 and 2 in exactly the 8
files of the patch, and in nothing else under `tools/tillr-land/`. It reported two things as
missing, and the patch holds both:

- **The stall in the directory test is the absence of step 2, not a fault in the runner.** The
  lander suite stopped in `TestFileAndDirectoryExchangesFollowTheOwnersContent`, in the subtest "a
  directory with tracked subdirectories becomes a file", after 480 seconds without progress. That
  is the repeating landing described further down in this block, which the whole directory
  exchange and the limit on predicting again end. Both are in the patch.
- **The test that kills a lander while it holds the index lock is in the patch.** Step 1's tree
  holds only `TestADeadLandersIndexLockIsReclaimedWithoutAHuman`, which writes a dead process's
  record into a lock file. The patch adds
  `TestALanderKilledWithTheIndexLockLeavesItNamedAndTheNextLandingRemovesIt` to
  `tools/tillr-land/idempotent_test.go`. It kills a real lander process just after the trunk move
  with `killAfterMove`, checks that the lock names that dead process, lands a second item and
  asserts that the lock is gone. That is the test c3 asks for, and the builder does not write
  another.

So steps 1 to 3 are now one command that stops at the first failure and ends by printing a line
that says the patch is in. A builder that has not seen that line has not built this item and has
no test result to report.

- **Build order, each step a stop only if it fails.** Do the steps in order in one run. The end
  of a step is not an ending, and the commit is made once, at step 5. Steps 1 to 3 are one
  command. Run it whole, from the lane worktree, as it is written:

  ```bash
  set -euo pipefail
  d="$(mktemp -d)"
  git diff bf86efa42 refs/tillr/preserved/run-item-land-across-owner-edits-a23 -- tools docs yolo-packs/tillr/skills | git apply --index
  git diff --cached --shortstat HEAD
  awk '/^# owner-edits-patch end$/{p=0} p; /^# owner-edits-patch begin$/{p=1}' docs/tillr/roadmap.md > "$d/owner-edits.diff"
  git apply --index "$d/owner-edits.diff"
  git diff --cached --shortstat HEAD
  (cd tools/tillr-land && gofmt -l . && go vet ./...)
  just pack-sync > "$d/pack-sync.log" 2>&1
  yolo pack lint yolo-packs/tillr | grep 'pack ok'
  grep -q 'func TestALanderKilledWithTheIndexLockLeavesItNamedAndTheNextLandingRemovesIt' tools/tillr-land/idempotent_test.go
  echo "patch applied: tools/tillr-land is $(git write-tree --prefix=tools/tillr-land/)"
  ```

  Its output is these four lines, with a `yolo-jail` version line before the third:
  `29 files changed, 4498 insertions(+), 108 deletions(-)`, then
  `29 files changed, 5027 insertions(+), 108 deletions(-)`, then a line containing `pack ok`, then
  `patch applied: tools/tillr-land is 3406c525f4ddf935571a93394aaa458b3c622a6b`. Do not go on to
  step 4 without the last line. A different hash on that line is a stop only when
  `git diff --stat 05c83dee8 HEAD -- tools/tillr-land` prints nothing, because then the trunk has
  not changed that directory and the tree should be this one. What the command does, by step:
  1. **Apply the preserved build.** The first `git apply`:

     ```bash
     git diff bf86efa42 refs/tillr/preserved/run-item-land-across-owner-edits-a23 -- tools docs yolo-packs/tillr/skills | git apply --index
     git diff --cached --shortstat HEAD
     ```

     The count after it is `29 files changed, 4498 insertions(+), 108 deletions(-)`. This is
     the only preserved ref to use. The others your brief lists under "Facts carried" hold the
     same build or an older one; do not apply, inspect or compare them. Do not take
     `yolo-packs/tillr/files/` from any ref: step 3 generates it.
  2. **Apply the patch.** The `awk` line writes the patch out of this block and the second
     `git apply` applies it:

     ```bash
     awk '/^# owner-edits-patch end$/{p=0} p; /^# owner-edits-patch begin$/{p=1}' docs/tillr/roadmap.md > "$d/owner-edits.diff"
     git apply --index "$d/owner-edits.diff"
     git diff --cached --shortstat HEAD
     (cd tools/tillr-land && gofmt -l . && go vet ./...)
     ```

     The count after it is `29 files changed, 5027 insertions(+), 108 deletions(-)`, and the
     fourth command prints nothing. Do not rewrite what the two applications produce.
  3. **Regenerate the mirror.** `just pack-sync`, then `yolo pack lint yolo-packs/tillr`,
     which prints a line containing `pack ok`.
  4. **Run the suites,** each with the two trace variables unset and its output in a file, and
     read only the last line of each file:

     ```bash
     env -u TILLR_SQLITE_TRACE -u TILLR_TRACE_DIR TMPDIR="$YOLO_DURABLE_DIR" python3 tools/go-test-shards.py tools/tillr-land -k 6 > "$YOLO_DURABLE_DIR/land.log" 2>&1; echo "land rc=$?"
     env -u TILLR_SQLITE_TRACE -u TILLR_TRACE_DIR TMPDIR="$YOLO_DURABLE_DIR" python3 tools/go-test-shards.py tools/tillr-view -k 6 > "$YOLO_DURABLE_DIR/view.log" 2>&1; echo "view rc=$?"
     env -u TILLR_SQLITE_TRACE -u TILLR_TRACE_DIR TMPDIR="$YOLO_DURABLE_DIR" python3 tools/go-test-shards.py tools/tillr-drain -k 6 > "$YOLO_DURABLE_DIR/drain.log" 2>&1; echo "drain rc=$?"
     python3 tests/tillr-docs.test.py > "$YOLO_DURABLE_DIR/docs.log" 2>&1; echo "docs rc=$?"
     python3 tests/pack-sync.test.py > "$YOLO_DURABLE_DIR/pack.log" 2>&1; echo "pack rc=$?"
     tail -n 1 "$YOLO_DURABLE_DIR/land.log" "$YOLO_DURABLE_DIR/view.log" "$YOLO_DURABLE_DIR/drain.log"
     ```

     Give each command a timeout of at least an hour, although the three suites took under
     fifteen minutes together here. Pass no `--shard-timeout` and no `--timeout`, and do not run
     a narrower `go test` in place of a suite.
  5. **Commit.** `git add -A tools docs/tillr/land-across-owner-edits yolo-packs/tillr`, then
     one commit. It holds 53 files: the 29 of steps 1 and 2 and the 24
     mirror copies. `docs/tillr/roadmap.md` is not part of it. Append `c1` to `c8` to the
     progress file before the commit.
- **What does stop this build:** an application that fails after the fallback below, output from
  the fourth command of step 2, or a failing test in step 4. Report it with the step number, the
  command and its output. A report that lists steps not yet done is not one of these, and neither
  is the size of the change.
- **If an application fails,** the trunk has moved under one of its files. Run the lines of the
  command one at a time from that `git apply` on, with `d="$(mktemp -d)"` set again and that
  `git apply` given `--index -3`, resolve each conflict towards the patch and keep what the trunk
  added, and continue to the last line. The counts in steps 1 and 2 and the hash may then differ;
  that alone is not a stop.
- **If the command is run a second time in one lane,** the first `git apply` fails because the
  build is already there. Run `git reset -q --hard HEAD` and
  `git clean -fdq -- tools docs/tillr/land-across-owner-edits yolo-packs/tillr` in the lane
  worktree, then the whole command again.
- **If a test fails in step 4,** run it alone once, with the trace variables unset and
  `-timeout 300s`. A test that passes alone is recorded in the report and does not stop the
  commit. For a test that fails alone, run it alone at the base with `git stash -u`, and restore
  the work with `git stash pop`. Red at the base is a `trunk` obstacle naming that test. Red only
  with the work applied is a `seat` obstacle that names the test and quotes its failure.
- **The failures the two builds reported are repaired by step 2.** After it,
  `TestAnUnrelatedCommitWithTheSameTreeIsNotTakenForTheLanding` passes because the test's owner
  step checks the killed lander's lock record and removes the lock, and
  `TestFileAndDirectoryExchangesFollowTheOwnersContent` passes because a directory that becomes a
  file is exchanged whole. Seeing either failure means step 2 was not applied: go back to the
  command above, and do not report it as an obstacle.
- **The builder's question about editor side files is answered.** The lander logs nothing about
  them, and the lander README in step 1's tree says so. The report need not take it up.
- **What this section measured,** in a clone at `55db82bc5` on 2026-11-18 with steps 1 to 3
  carried out. It is a report, not a test in the tree:
  - Step 1's `git apply --index` and step 2's both applied with no output, and the two counts
    were the ones the steps give. `gofmt -l .` and `go vet ./...` printed nothing.
  - `just pack-sync` changed 24 files under `yolo-packs/tillr/files/`, and `yolo pack lint
    yolo-packs/tillr` printed `pack ok`. The lint ran the jail's binary, so the host's render
    was not verified.
  - `python3 tools/go-test-shards.py tools/tillr-land -k 6` printed `191 tests passed, 6 shards
    per package` and exited 0. That run preceded the README text, which no test in the module
    reads.
  - `python3 tools/go-test-shards.py tools/tillr-view -k 6` printed `504 tests passed, 6 shards
    per package` and exited 0.
  - `python3 tools/go-test-shards.py tools/tillr-drain -k 6` printed `2114 tests passed, 6 shards
    per package` and exited 0, with the mirror regenerated.
  - `python3 tests/tillr-docs.test.py` and `python3 tests/pack-sync.test.py` exited 0.
  - `TestTheComposedPipelineRunsEndToEndOnAFixture` in the drain module failed once, on
    `pack-sync`, in a clone whose commit held step 1 without the mirror. It passed with the
    whole change uncommitted on `55db82bc5`, which is the state a lane is in at step 4, and
    passed again with the whole change committed.
  - The `rg` that c6 asks for, over the drain README, the lander README, `tools/tillr-view/waiting.go`
    and the `tillr-operating` skill, for `any dirty|dirty landed path|holds the landing|uncommitted
    content on a landed path`, returned nothing.
  - Measured again on 2026-11-18 in a clone at `05c83dee8`, with the command of steps 1 to 3 taken
    from this block and run as a script: it exited 0 and printed the four lines given above. The
    three suites then printed `191 tests passed` for `tools/tillr-land`, `504 tests passed` for
    `tools/tillr-view` and `2117 tests passed` for `tools/tillr-drain`, each exiting 0, and
    `python3 tests/tillr-docs.test.py` and `python3 tests/pack-sync.test.py` exited 0. The index
    then held 53 changed files.
  - The build that stopped after step 1, compared the same day: for the 29 files of steps 1 and
    2, the blob in `refs/tillr/preserved/run-item-land-across-owner-edits-a27` differs from the
    result of the two applications in 8, which are the 8 files of the patch.
    `git diff --stat refs/tillr/preserved/run-item-land-across-owner-edits-a23
    refs/tillr/preserved/run-item-land-across-owner-edits-a27 -- tools/tillr-land` printed
    nothing.

Tillr does something different today. A landing leaves unrelated uncommitted paths alone, so
those do not delay it. When the owner's uncommitted content sits on a path the landing changes,
the lander refuses and the loop holds the already-gated candidate with `trunk-path-dirty` until
the owner commits or restores that path. The owner waits for the loop, or the loop waits for the
owner, which is the blocking the autostash ruling was meant to remove.

The owner then ruled on 2026-11-17. First: "ok, for paths that don't touch, slip it in as
stealthily as possible, for paths that are touched, do an autostash type of thing as fast as
possible." Then, on the case where the owner's edits would conflict with what landed: "if
there's a conflict, ideally we can predict this before we leave the working directory
conflicted, I think this is the only state where we pause landing. not sure we have a choice
here."

The owner's guiding principle, also 2026-11-17: "My guiding principle here is that I'm just
imagining myself, a human author with a file open in Vim, trying to edit something, and I'm
trying to have it be as least surprising as possible what happens as landings happen that do or
do not touch the file I'm editing." What a person with a file open can observe is the file's
content, its identity on disk (inode), its modification time, and the files beside it, so the
criteria are written in those terms.

Two builds of this item stopped on 2026-11-17, and the criteria were amended the same day to
answer them. The first stopped on the transaction rule in c3: the lander already makes its final
trunk move inside the store's write transaction, which is what keeps `tillr item close` from
closing an item between the last check and the landing receipt, so "no Git work inside a write
transaction" read as impossible. The second stopped because c3 said a `refs/stash` in the main
checkout stops a tick, and the tree says the opposite. A review of the first build also found
four cases the criteria did not name. The rulings:

- **The Close exclusion stays, and nothing is computed inside it.** Everything that takes thought
  or time happens before the write transaction opens: the prediction, the final content and index
  entry of every path, and the recovery copy. The transaction keeps only the step it has today,
  the final move, and that step installs what was prepared. The one addition is c4's comparison
  of the touched paths against what the prediction read, which decides nothing new.
- **The recovery copy is a copy.** Writing the owner's content to a ref does not remove it from
  the working tree. The owner's files stay on disk as they were until each is replaced once by
  its final content, so there is no interval in which an edit is missing from disk.
- **The landing lock does not hold the owner's editor.** The owner can save at any moment, so a
  prediction can go stale, and the lander checks again before it writes.
- **`refs/stash` is ordinary Git and stops nothing.** The drain README's boundary table and
  `TestOrdinaryGitUseDuringARunNeverConvictsIt` are the truth; the sentence in
  `docs/design/tillr-day2.md` that says otherwise is out of date. The recovery copy still stays
  out of `refs/stash`, because that list belongs to the owner.

A third build stopped the same day on a case where c6 and c7 disagreed. When a landing turns a
directory into a file, every file left in that directory has to go, and Git removes an ignored one
without asking. c7 said an editor side file never causes a conflict, so a Vim swap file under
such a directory could neither be kept at its path nor hold the landing. The ruling, made by the
design seat from the owner's principle that a landing leaves the files beside an open file alone:

- **A landing never deletes a file it does not track.** When a landing has to put a file where
  the main checkout holds a directory, and that directory would still hold anything after the
  landing's own deletions, that is a predicted conflict. This covers untracked files, ignored
  files and editor side files alike. It is the same pause as any other predicted conflict, so
  the owner's ruling that a predicted conflict is the only pause still holds, and c7's rule for
  side files stays as it was everywhere else.

A fourth build stopped the same day, before it wrote anything, because the rulings above had
been added to this section while c3 still carried the two sentences they overruled: that a
`refs/stash` in the target stops a tick, and that the owner's content is set aside and later
restored. The criteria now say what the rulings say. c3 is rewritten around the recovery copy,
and the words "set aside" are gone from c2, c4, c5 and c7. c3 also names the two ref prefixes
the boundary check ignores, because the old text said the check excuses everything under
`refs/tillr/`, and it does not.

A later build stopped the same day on c4's sentence "an owner's save is never lost without a
report", and an independent reviewer sustained the stop. The build replaced each touched file by
swapping the prepared file into its path, which leaves the old file, now without a name, in the
lander's hands. A program that opened the old file for writing before the swap can still write
into it afterwards. The lander read the old file again after the swap and again after the trunk
move, and each read caught the saves made before it. A save written after the last read, and
before the lander deleted the old file, was gone from the disk and from every ref, and the
landing reported success. Another read only moves that gap, and a Git ref cannot hold bytes that
have not been written yet. Closing the gap completely means waiting for the writer or keeping
the old file after the landing, and both contradict the owner's rulings that a landing does not
wait for the owner and that a clean landing leaves nothing behind. The ruling, made by the design
seat from the owner's standard that a landing behaves as `git pull --autostash` does, and for
the owner to overrule if it is wrong:

- **A save is what a path holds.** The promise covers the bytes readable at a touched path, at
  every moment up to the lander's last read of the file it replaced there. A save made through
  the path after the replacement goes into the new file and stays on disk, so it needs no
  protection. Bytes written after that last read, through a descriptor opened before the
  replacement, are outside the promise, and the lander README says so in those words. `git pull`
  protects none of these saves, so this is narrower than Git's gap and is not a new surprise.
- **The lander reads each replaced file once more, as late as it can, and then deletes it.** The
  last read comes after the trunk move and after the landing's receipt is written, immediately
  before the old file and the recovery ref are removed. No Git command, store write or other
  slow step runs between that read and the removal. The lander does not wait, does not keep the
  old file past the landing, and takes no lock an editor would have to honor.
- **A change found before the trunk moves undoes the install; one found after it undoes
  nothing.** Before the trunk moves, the lander puts the owner's files back, saved bytes
  included, and predicts again. After the trunk moves, the landing stands, the checkout and
  index stay as landed, and the saved bytes go to the recovery ref with a fault naming the path
  and the ref. Undoing a published landing left a staged revert in the reviewer's reproduction.
- **Undoing an install is held to the same rule as the install.** Before the lander removes or
  replaces a file while undoing, it reads that file. If the file no longer holds what the lander
  installed, the owner saved there in the meantime, and those bytes are put back at the path or
  kept in the recovery ref with a fault.

The same review found three defects in that build which the criteria did not rule out in so
many words, so c4 and c6 now do. The lander wrote into whichever branch the main checkout held
after the owner switched branches during the landing. It merged a file that the repository's
attributes mark binary as if it were text. It failed to replace a directory with a file when
the directory held a tracked file in a subdirectory.

Another build stopped the same day because c3 said the drain's run-boundary check ignores only
`refs/tillr/runs/` and `refs/tillr/landed/`, and the tree ignores more. `landerRef` in
`tools/tillr-drain/drain/boundary.go` also passes `refs/tillr/preserved/`, and
`trunkHistoryRef` passes the two refs `refs/tillr/trunk-history/observed` and
`refs/tillr/trunk-history/lost`. The boundary table in the drain README lists all five. c3 now
names them, and no longer offers one of the existing prefixes as a home for the recovery copy,
because each already means something else. The ruling, made by the design seat:

- **The recovery copy gets a prefix of its own under `refs/tillr/`.** A ref under
  `refs/tillr/runs/` is a pin on a run's committed head, one under `refs/tillr/landed/` marks a
  squash commit and is how a retry finds a landing and how the boundary check excuses a moved
  patch branch, and one under `refs/tillr/preserved/` is a lane's saved edits, which the drain
  records with an event and prunes when the item closes. The owner's content from the main
  checkout is none of those. The builder chooses the name and adds it to the ignored refs in
  `boundary.go` and to the boundary table. A ref the check does not list is reported as
  observed, not as a violation, so the addition keeps a landing from printing a line about its
  own recovery copy; it does not rescue a run from a conviction.

One more build stopped the same day on the same sentence of c3. The trunk had moved since the
list was written: `landerRef` now also passes `refs/tillr/green`, the ref at which the heavy
runner records the last trunk tip it proved, and c3 said every ref outside its list is reported
as observed. An earlier build, made before that stop, had kept both exemptions when it replayed
onto the moved trunk, and that was right. The list had now gone stale twice, so the ruling, made
by the design seat, answers the list as well as the one ref:

- **The code is the list of ignored refs, and the criterion's list is a reading of it.** c3 now
  names `landerRef` and `trunkHistoryRef` as what decides, gives the refs they pass with the date
  they were read, and says that an ignored ref missing from that sentence does not contradict
  the criterion. A builder that finds another one keeps it and goes on.
- **`refs/tillr/green` stays ignored and stays the heavy runner's.** It holds one commit, the
  proved trunk tip, and the red cap reads it. The recovery copy does not go there, for the same
  reason it goes under none of the other existing names.
- **The boundary table gains the proof ref.** The drain README's table lists the other five and
  not `refs/tillr/green`, so the builder adds it in the same edit that adds the recovery prefix.
  The row that reports any other ref inside tillr's namespaces as observed stays as it is.

A build stopped on 2026-11-18 with the work written and uncommitted, because a landing that turns
a directory into a file never returned. The design seat ran that build's tests in a scratch copy
the same day and found three causes, none of which the criteria ruled out, and one stale pointer.

The landing did not hang inside one call; it repeated. The build removes a file by renaming it to
a temporary name in the same directory and keeps it there until the trunk has moved, so that an
undo can rename it back. When the landing deletes `g/s/t/y.txt` and `g/z.txt` and then puts a file
at `g`, those two temporaries are still inside `g`. The step that removes the emptied directory
found them, took them for the owner's files, and answered that the owner had changed a path. The
lander put everything back and predicted again, read the same content, and failed the same way,
with no limit on the number of attempts. The rulings, made by the design seat:

- **A directory that becomes a file is replaced whole, in one exchange.** The lander does not
  empty the directory file by file. It exchanges the directory with the prepared file in a single
  rename, which leaves the directory, with everything in it, under a temporary name beside the
  path. The deletions the landing makes under that directory are not carried out one by one on
  disk; they are covered by the exchange and appear only in the index. Undoing the install is the
  same exchange made again. This is also the single replace that c2 asks for.
- **The displaced directory is held to the rules for a displaced file.** After the exchange the
  lander reads the whole displaced directory. Before the trunk moves, a file there that the
  prediction did not read, or one whose content differs from what it read, is the owner's: the
  lander exchanges the directory back and predicts again, and that prediction is a conflict under
  c6 because the directory now holds the owner's content. The last read of c4 covers every file
  in the displaced directory. The lander then removes the files it read, by name, and the
  directories that are then empty. It never removes a directory tree recursively. If something it
  did not read is still there, the directory stays under its temporary name and the landing
  records a fault naming it, so the first ruling of the third build still holds: a landing never
  deletes a file it does not track.
- **Predicting again requires that something was read differently.** When the comparison or the
  install answers that the owner changed a path, the next prediction's readings of the touched
  paths, their index entries and the branch are compared with the previous prediction's. If they
  are all the same, the owner changed nothing and the lander's own install is at fault. That is
  the failed install of c4: the owner's content is put back, a fault is recorded, and the landing
  returns. A landing therefore cannot repeat without the owner having saved in between.

The second cause is the checkout's index lock. The build takes `.git/index.lock` in the main
checkout before its comparison and holds it until the trunk has moved, so that no Git command of
the owner's runs in the middle. Two tests that are on the trunk today,
`TestAnUnrelatedCommitWithTheSameTreeIsNotTakenForTheLanding` and
`TestTheSameRunLandingADifferentHeadIsNotTakenForItsEarlierLanding`, kill the lander just after
the trunk move and then run `git reset` and `git checkout` as the owner. Under the build both fail
with `Unable to create '.../.git/index.lock': File exists`, because the killed lander left its
lock. The ruling, made by the design seat from the owner's standard that a landing behaves as
`git pull --autostash` does, and for the owner to overrule if it is wrong:

- **A killed lander may leave the index lock, as a killed Git command does, and it says whose it
  is.** The lock file carries a record naming the lander, its host and its process. The lander
  releases the lock as soon as the trunk has moved, before the receipt and before the last read.
  The next lander that starts on that host removes a lock whose record names a dead lander, and
  touches no other lock. The lander README says that a lander killed during the final move leaves
  this lock, how to recognise it, and that removing it is safe once the named process is gone,
  which is what Git's own message tells the owner to do. The two tests keep every assertion they
  make. Their owner step first checks that the lock holds the lander's record and removes it.

The third cause is time. The worker's focused command had no `-timeout`, so the repeating landing
ran until the command was stopped after 600 seconds, and the suites after it never started. Each landing test in this
module runs a few dozen Git commands, and the build adds about sixty tests to it. The rule for
the builder is in the agent details below.

The stale pointer: commit `4f1ac49ab` moved the rule for each hold reason out of the view and into
one table in `tools/tillr-drain/holdrules/holdrules.go`, which
`TestEveryReasonConstantHasAHoldRule` checks. c5 and the agent details named only the view, and
now name both.

A build stopped later on 2026-11-18 and named the task beyond its seat. It had brought the last
build onto the moved trunk and changed nothing else: `git diff` between the two preserved builds
shows `land/owner.go`, `land/owner_fs.go` and `land/owner_install.go` identical, so none of the
four changes in the plan had been made. It then reported three failures as unexplained. The design
seat made the changes in a scratch copy the same day, on this block's base, and found that two of
the three were already ruled above and the third was not a failure.

- **The retained index lock is the ruling above, not a defect to design around.** The build
  already writes the record into the lock, releases the lock when the trunk has moved and reclaims
  a dead lander's lock. The two trunk tests kill the lander during the final move, so the lock is
  still there when they run Git as the owner. The only change is in those two tests: the owner
  step reads the lock, checks the record and removes it. No lander code changes for this.
- **The retry test was slow, and the worker's time limit was shorter than the test.**
  `TestOwnerSavesDoNotConsumeAnArbitraryLandingRetryBudget` has the owner save 201 times and
  passes in 67 seconds; the worker ran it with `-timeout 8s` and the suite with
  `--shard-timeout 15`. The ruling, made by the design seat: the number of saves is not a
  criterion, and 25 shows the same thing, that no fixed budget ends a landing while the owner keeps
  saving. The test then takes about nine seconds.
- **The directory tests stalled for the cause found above**, the repeating landing. The whole
  directory exchange and the limit on predicting again end it. With those made, the lander suite
  passes, so the plan below now gives each change at the level of the function it touches.
- **A file that appears in a displaced directory is found by listing, twice.** The lander lists
  the displaced directory when it reads it after the exchange, and once more after every path is
  installed, at the place where it already reads the displaced files again. A name that was not
  there the first time undoes the install before the trunk moves. A file created after that second
  listing is found when the directory is removed after the trunk move, and keeps the directory
  under its temporary name with a fault, as c6 says.
- **An owner's save at the path of a replaced directory keeps both.** If, while an install is
  being undone, the path where the directory stood no longer holds the file the lander put there,
  the lander leaves that file at the path, leaves the directory under its temporary name, and
  records a fault naming both. It does not exchange them, because that would move the owner's new
  file to a temporary name.



**Agent details:**

- **Ref:** `docs/design/board-state-machine.md` section 9 (D33, "the comparison-and-swap autostashes"); `docs/design/board-state-machine-plan.md` P10 (fences: never drop the stash on a failed apply, never stash during the gate); `docs/design/landing-principles.md`; the boundary table in `tools/tillr-drain/README.md`.
- **Where:** `Trunk.Move` and `Trunk.UncommittedPaths` in `tools/tillr-land/land/trunk.go`; `finishLanding` in `tools/tillr-land/land/landing.go`, which calls `Trunk.Move` inside the write transaction; the ignored ref prefixes in `tools/tillr-drain/drain/boundary.go`; the dirty refusal in `tools/tillr-land/`; `tools/tillr-drain/drain/dirty_hold.go`; `trunk-path-dirty` in `tools/tillr-drain/holdrules/holdrules.go` and in `tools/tillr-view/waiting.go`; the drain README section "Owner edits and waiting landings"; `yolo-packs/tillr/skills/tillr-operating/`.
- **Plan for the builder (2026-11-18, second version; superseded the same day by the build order at the head of this block, and kept as the description of what the patch at the end of this block holds; a builder does not carry it out):** every step below was made by the design seat in a scratch copy on `d4eecbea3` and the result is in the evidence line for this amendment, so a step that fails for the builder is a mistake in carrying it out, not an open design question. Do the steps in order and run the named tests after each. Run every `go test` with `-timeout 120s` or longer, never shorter; a landing test in this module takes about one second and the slowest takes nine. Run a whole module only as `python3 tools/go-test-shards.py tools/<module> -k 6` with no `--shard-timeout` and no `--timeout`, in the background if the harness limits a command's time: the three modules take several minutes together. `python3 tests/tillr-docs.test.py` takes 60 seconds, so give it at least 300.
  1. **Start from the last build.** In the lane worktree run `git diff bf86efa42 refs/tillr/preserved/run-item-land-across-owner-edits-a23 -- tools docs yolo-packs/tillr/skills | git apply`. The ref is in the main repository and is readable from the lane. The patch applies without offsets that matter on `d4eecbea3`; if the base has moved and it does not apply, use `git apply --3way` and resolve. Do not take `yolo-packs/tillr/files/` from the ref. This build already holds the hold rules change, the view test with the `operator;` prefix, the lock record and the reclaim of a dead lander's lock, so none of those is written again.
  2. **The two trunk tests, in `tools/tillr-land/idempotent_test.go`.** Add a helper that reads `.git/index.lock` in the fixture's checkout, decodes it as JSON, and fails unless `kind` is `tillr-land-index-lock`, `pid` is positive, `host` is not empty and `syscall.Kill(pid, 0)` answers `ESRCH`; and a second helper that calls the first and then removes the file. Call the second helper immediately after `killAfterMove` in `TestAnUnrelatedCommitWithTheSameTreeIsNotTakenForTheLanding` and in `TestTheSameRunLandingADifferentHeadIsNotTakenForItsEarlierLanding`, and change nothing else in them. Add the test c3 asks for: `killAfterMove`, the first helper, then land a second item made with `f.candidateFrom` through `passBoth`, and assert exit 0, result `landed`, and that `.git/index.lock` is gone.
  3. **The retry test, in `owner_retry_test.go`.** Change `saves` from 201 to 25 and the expected last line from `save-201` to `save-25`.
  4. **The limit on predicting again.** In `land/owner.go` add a method on `OwnerPlan` that answers a SHA-256 digest over the worktree, `HeadText`, `Busy`, `From`, `To`, and for each path its name, the five fields of `Disk` and the mode and object id of `IndexNow`, each written with its length so two fields cannot run together. Add a hook `FailComparison func() bool` to `OwnerHooks` and clear it in `ResetOwnerHooks`. In `MoveOwner` (`land/owner_install.go`), just before `checkPlan`, when the hook answers true: release the index lock, close the root, drop the recovery ref and return `ErrOwnerChanged`. In the loop in `Land` (`land/landing.go`) keep a string that starts empty. Where the loop handles `ErrOwnerChanged`, set it to the digest of the plan that was just tried. After `prepareLanding` returns and after the `prepared.stored` check, if the string is not empty and equals the new plan's digest, return `t.settleOwner(ctx, s, prepared, req, &publication{}, decided, failure)` where `failure` is an `*InstallFailure` whose `Err` says that the install reported a change and the next prediction read everything as before. `settleOwner` already records the fault and names the recovery copy for that error type. The test: an owner's edit on the last line of `a.txt`, `FailComparison` always true, `AfterPredict` counting; assert an error containing `fault recorded`, two predictions, the file's `statOf` unchanged, one `land.FaultKind` event, and the trunk still at the base.
  5. **The whole directory exchange.** In `land/owner_fs.go` add a function that, given the parent's descriptor, the path, its base name, the file to install and a map from checkout path to the content the prediction read for each file the landing deletes under that directory: makes the temporary with `makeTemp`, exchanges it with the directory by `Renameat2` with `RENAME_EXCHANGE`, then walks the displaced directory with `Openat` (`O_DIRECTORY|O_NOFOLLOW`) and `inspect`, collecting for each file its path, its path inside the directory, what it read and the open handle, and for each subdirectory its path, parents first. A file that is not in the map, or differs from it, or a count of files different from the map's, is the owner's change: close the handles and return `restoreDisplacement(dirfd, rel, base, tmp, true, want, ErrOwnerChanged)`, which exchanges the directory back and removes the lander's file. Add a second function that removes a displaced directory: each collected file by `Unlinkat` only when `Fstatat` on its name and `Fstat` on its handle give the same device and inode, then each collected subdirectory with `AT_REMOVEDIR`, deepest first, then the temporary name itself, answering whether that last removal succeeded. Add a third that lists the collected directories again and answers true when one holds a name that was not collected. In `land/owner_install.go`: give `installed` a flag for a displaced directory and the two collected lists. In `install`, before the loop, build the map for every action with `Install` set and `Disk.Kind == "dir"` from the `Remove` actions whose path begins with that path and a slash, using `planned(p)`; skip those `Remove` actions in the loop; and for the directory action call the exchange in place of `removeEmptyTree` and `swap`, set `swapped`, the flag, `tmp` and the lists, check `sameDir`, run the `AfterReplace` hook and continue. In `changedNodes` read each collected handle again and report a file whose bytes differ. In `MoveOwner`, where it tests `changedFiles()` after the `AfterInstall` hook, also call the listing function for each displaced directory and treat true as `ErrOwnerChanged`. In `dropTemps`, for a displaced directory call the removing function and, when it answers false, append to `in.leftover` a line naming the path and the temporary name. In `undoOne`, when the path still holds what was installed, exchange the temporary name back with `RENAME_EXCHANGE` and unlink the temporary name; when it does not, append a failure naming the path and the temporary name and leave both. Close the collected handles wherever `rec.handle` is closed, in `undo` and in `Finish`. Tests, in `owner_cases_test.go`, each on a landing that replaces `g` holding `g/z.txt` and `g/s/t/y.txt` with a file: one that in `AfterInstall` finds exactly one directory matching `.tillr-*.tmp` in the checkout root, asserts both files are inside it and that it holds no `.tillr-*` name at any depth, and after the landing asserts no `.tillr-*` name in the root and an empty `git status --porcelain`; one that writes `new.txt` into `g` in `BeforeSwap` for `g`, and in a second case into the displaced directory in `AfterInstall`, and asserts in both a hold on `g` and `g/new.txt`, all three files at their paths with their bytes, no `.tillr-*` name left and the trunk unmoved; one that writes `new.txt` into the displaced directory in `BeforeTrunkMove` and asserts the landing landed, one fault naming the temporary directory, one `land.FaultKind` event, the file's bytes still there, and `z.txt` and `s` gone from it. The fixture's landing also changes `a.txt`, so the root holds a second temporary that is a file; pick the directory with `os.Lstat`.
  6. **The READMEs.** In `tools/tillr-land/README.md` state the exchange of a whole directory and what is left under a temporary name with a fault; that a landing predicts again only when something was read differently and otherwise ends with a fault; and, in the index lock paragraph, that a lander killed during the final move leaves the lock, that its content names the lander's host and process, and that removing it is safe once that process is gone. Then run the rg of c6 over the four documents.
  7. **Finish.** `gofmt -l tools/tillr-land` prints nothing; `go vet ./...` in `tools/tillr-land`; `just pack-sync`; `yolo pack lint yolo-packs/tillr`; the three sharded suites; `python3 tests/tillr-docs.test.py`; `python3 tests/pack-sync.test.py`; one commit.
- **Question for the builder, not a criterion, and answered by the preserved build (the lander logs nothing about editor side files, and its README says so):** whether the presence of an editor swap or lock file for a landed path, a sign the file is open and may hold unsaved changes, should appear in the landing's log line as information for the owner. It must not pause the landing, because the owner ruled that a predicted conflict is the only pause.
- **Evidence for the amendment:** 2026-11-17, `finishLanding` calls `t.Move` with the store transaction open, and `TestFinalMoveExcludesCloseUntilReceipt` and `TestGitPreparationAllowsHeartbeatAndClose` in `tools/tillr-land/contention_test.go` pin the exclusion; `tools/tillr-item/` is outside this item's paths and needs no change. The drain README lists `refs/stash` as ignored, and `TestOrdinaryGitUseDuringARunNeverConvictsIt` in `tools/tillr-drain/drain/boundary_ordinary_git_test.go` stashes in the main checkout without stopping anything. `rg -n 'func landerRef|func trunkHistoryRef' -A 2 tools/tillr-drain/drain/boundary.go` at `658465404` shows the ignored refs under `refs/tillr/` are the prefixes `refs/tillr/runs/`, `refs/tillr/landed/` and `preservedPrefix` (`refs/tillr/preserved/`, from `tools/tillr-drain/preserved/record.go`) and the two refs `observedTrunkRef` and `lostTrunkRef` (`refs/tillr/trunk-history/observed` and `refs/tillr/trunk-history/lost`, from `tools/tillr-drain/drain/trunk_history.go`); the README's boundary table gives "Observed" for any other ref inside tillr's namespaces. The same command at `0ebcebe25` shows `landerRef` also returns true for `ref == GreenRef`; `rg -n 'GreenRef = ' tools/tillr-drain/heavy/status.go tools/tillr-drain/drain/heavy.go` shows that is `refs/tillr/green`, and `git log -S'ref == GreenRef ||' --oneline -- tools/tillr-drain/drain/boundary.go` names `89afe7e39` as the commit that added it. `rg -n 'refs/tillr/green' tools/tillr-drain/README.md` returns two lines, neither of them in the boundary table (2026-11-17). The first build's reviewer reproduced a write through a symlinked parent directory, an owner's save lost during a landing of several paths, a failed file-to-directory replacement, and a false conflict on a file with converted line endings; that build was not committed, so these are reports, not tests in the tree. The first build also stopped on `tests/tillr-docs.test.py` failing on the base over `tillr drain reset-lane`; `rg -n 'reset-lane' tools/tillr-drain/README.md` now returns nothing and `python3 tests/tillr-docs.test.py` passes. The third build reproduced the directory case in a scratch repository on 2026-11-17: with `notes/draft` tracked and `notes/.draft.swp` ignored, `git status` was empty, a fast-forward that replaced `notes/` with a file `notes` succeeded, and the swap file was gone afterwards. That build committed nothing, so this is a report, not a test in the tree. A later build's test `TestOwnerSaveThroughOpenDescriptorDuringRecoveryRemovalSurvivesLanding` failed on 2026-11-17 with `save lost from disk and recovery: Result:landed err=<nil> refs=""`: it opened a tracked file for writing before the landing, and wrote and closed it while the lander was deleting the recovery ref. The reviewer reproduced that, and also a staged revert left by undoing after the trunk had moved, an `ENOTEMPTY` failure replacing `notes/` with a file when it held the tracked `notes/sub/draft`, a text merge of a file marked `binary` in `.gitattributes` where `git pull --autostash` reports a conflict, and a landing written into a branch the owner created with `git checkout -b` during the landing. That build committed nothing either, so these are reports too.
- **Evidence for the 2026-11-18 amendment:** the design seat extracted `tools/` from `refs/tillr/preserved/run-item-land-across-owner-edits-a20` into a scratch directory and ran its tests there; that build was not committed, so these are reports, not tests in the tree. `go test . -timeout 30s -run 'TestFileAndDirectoryExchangesFollowTheOwnersContent' -v` timed out in the subtest "a directory with tracked subdirectories becomes a file", and the Git trace of that run held 380 `merge-tree` calls, one per prediction. With a line printed at each place the build answers that the owner changed a path, every attempt reported the same one, in `removeEmptyTree`, and the directory listing printed there was `g [s .tillr-96b1f939589cef00.tmp]`, `s [t]`, `t [.tillr-773a8252437c1b1b.tmp]`: the lander's own two temporaries. A scratch call of `renameat2` with `RENAME_EXCHANGE` between a regular file and a directory holding `s/t/y.txt` and `z.txt` returned 0, and the directory's files were then readable under the file's old name. `go test . -timeout 120s -run 'TestAnUnrelatedCommitWithTheSameTreeIsNotTakenForTheLanding|TestTheSameRunLandingADifferentHeadIsNotTakenForItsEarlierLanding'` failed in that build with the `index.lock` message quoted above, and the same command passes on `4f1ac49ab`. `rg -n 'trunk-path-dirty' tools/tillr-drain/holdrules/holdrules.go` returns two lines on `4f1ac49ab`.
- **Evidence for the second 2026-11-18 amendment:** all of it was measured by the design seat in scratch copies on 2026-11-18, and none of it is committed, so these are reports, not tests in the tree. `git diff --stat refs/tillr/preserved/run-item-land-across-owner-edits-a20 refs/tillr/preserved/run-item-land-across-owner-edits-a23 -- tools/tillr-land` lists the same eleven files as `git diff --stat 5ca0b0ff8 bf86efa42 -- tools/tillr-land`, and `git diff --quiet` between the two refs is clean for `land/owner.go`, `land/owner_fs.go` and `land/owner_install.go`. `git diff --stat bf86efa42 d4eecbea3 -- tools/tillr-land` is empty, and the patch of step 1 passes `git apply --check` on `d4eecbea3`. On that build, `go test . -timeout 120s -run 'TestAnUnrelatedCommitWithTheSameTreeIsNotTakenForTheLanding|TestTheSameRunLandingADifferentHeadIsNotTakenForItsEarlierLanding'` failed with the `index.lock` message and passed after step 2; `go test . -timeout 300s -run TestOwnerSavesDoNotConsumeAnArbitraryLandingRetryBudget -v` passed in 66.99 seconds, and in 9.24 seconds after step 3; `go test . -timeout 60s -run TestFileAndDirectoryExchangesFollowTheOwnersContent` timed out in the subtest "a directory with tracked subdirectories becomes a file" and passed after step 5. With steps 1 to 5 applied to a clone at `d4eecbea3`, `python3 tools/go-test-shards.py tools/tillr-land -k 6` printed `191 tests passed`, `tools/tillr-view` printed `501 tests passed`, `tools/tillr-drain` printed `2086 tests passed`, and `python3 tests/tillr-docs.test.py` exited 0 after 60 seconds. Steps 6 and 7 were not carried out, so the READMEs, the pack mirror and `yolo pack lint` are unverified.
- **Evidence:** 2026-11-17, `Trunk.Move` runs `git -c merge.autoStash=false merge --ff-only` in the main checkout, so git itself refuses when the move would overwrite uncommitted content and otherwise advances only the changed paths; git's fast-forward keeps staged entries on untouched paths, but no test pins it. `TestALandingLeavesUncommittedContentOnAnUntouchedPathAlone` (untracked file only) and `TestALandingIsRefusedWhenTheTrunkWorktreeHoldsUncommittedContentOnALandedPath` in `tools/tillr-land/main_test.go`, and `TestUnrelatedDirtyPathDoesNotDelayLanding` in `tools/tillr-drain/drain/dirty_hold_test.go`, pin the two cases. `rg -n -i 'MERGE_HEAD|rebase-merge' tools/tillr-land tools/tillr-drain --glob '*.go'` returns nothing, so no code names an in-progress merge or rebase. `TestLockedLandingAcceptsFastForwardBeyondObservedTip` in `tools/tillr-land/trunk_history_test.go` pins the forward move, and `TestTheDrainLandsThroughTheRealAdapterAndTheRealLander` in `tools/tillr-drain/drain/step_argv_contract_test.go` pins `trunk-rewritten`. `rg -n -i dirty yolo-packs/tillr/skills/tillr-operating/` returns nothing (2026-11-17).

The patch for step 2 of the build order at the head of this block. The two marker lines are not
part of it, and the command in step 2 leaves them out. It was made in a clone at `55db82bc5` on
2026-11-18 with `git diff --cached HEAD -- tools`, after step 1 had been applied and committed
there, and the suites above passed on the result. It holds steps 2 to 5 of the plan under "Agent
details" and the three README statements of that plan's step 6.

````diff
# owner-edits-patch begin
diff --git a/tools/tillr-land/README.md b/tools/tillr-land/README.md
index 7ac624354..87d49132e 100644
--- a/tools/tillr-land/README.md
+++ b/tools/tillr-land/README.md
@@ -281,12 +281,35 @@ rolled back, nothing is held, refused or recorded, and the landing tries again a
 (25 ms doubling to 1 s; the request's context ends it). The lock is created by linking a file that
 already carries the lander's ownership record (kind, host, process id, token), so record and lock
 appear together. A lock whose record names this host and a process that no longer exists is the
-lander's own leftover and the next landing removes it; any other lock is never touched. The index is
+lander's own leftover and the next landing removes it; any other lock is never touched. A lander
+killed during the final move leaves this lock, as a killed Git command leaves its own. Git then
+refuses the owner's commands with its usual `index.lock: File exists` message. The file is JSON
+whose `kind` is `tillr-land-index-lock` and whose `host` and `pid` name the lander, and removing it
+is safe once that process is gone. The index is
 replaced once, from a copy with only the touched entries changed, so untouched entries keep their
 stat data. Because a file is replaced by an exchange with a temporary, the displaced inode exists
 under a temporary name beside the path from the replacement until the trunk ref has moved; that is
 the one intermediate on-disk state, inside the lock, and the names are removed right after.
 
+**A directory that becomes a file is exchanged whole.** The lander does not empty the directory
+file by file. One exchange puts the prepared file at the path and leaves the directory, with
+everything in it, under a temporary name beside the path; the deletions the landing makes under it
+appear only in the index. The lander reads the whole displaced directory after the exchange and
+lists it again once every path is installed. A file the prediction did not read, or one whose
+content differs, is the owner's: before the trunk moves the directory is exchanged back and the
+landing predicts again, which is then a conflict. After the trunk has moved the lander unlinks the
+files it read, by name, and the directories that are then empty, never a tree recursively. Anything
+else found there stays, the directory keeps its temporary name (`.tillr-<random>.tmp` beside the
+path), and the landing records a `land.fault` event naming it. If the owner saves a file at the
+path while an install is being undone, that file stays at the path, the directory stays under its
+temporary name, and the fault names both.
+
+**A landing predicts again only when something was read differently.** When the comparison or the
+install answers that the owner changed a path, the next prediction's readings of the touched paths,
+their index entries and the branch are compared with the previous prediction's. If all are the
+same, the owner changed nothing: the landing ends as a failed install does, with the owner's
+content put back and a `land.fault` event, so a landing never repeats without a save in between.
+
 - A change found **before the trunk moves** puts the owner's files back (symlinks included), saved
   bytes included, and predicts again under the same lock. The comparison, the branch check before
   every mutation and a parent that changed do the same.
diff --git a/tools/tillr-land/idempotent_test.go b/tools/tillr-land/idempotent_test.go
index 1c0cd6d17..25c0b1fb8 100644
--- a/tools/tillr-land/idempotent_test.go
+++ b/tools/tillr-land/idempotent_test.go
@@ -3,6 +3,7 @@ package main_test
 import (
 	"bytes"
 	"context"
+	"encoding/json"
 	"os"
 	"os/exec"
 	"path/filepath"
@@ -110,6 +111,40 @@ func killAfterMove(t *testing.T, f fixture, args []string) string {
 	return tip
 }
 
+// ownerRemovesLandersIndexLock is the owner's step after a lander was killed during the final
+// move: the checkout's index.lock is still there, as a killed Git command leaves it. The owner
+// reads it, sees the lander's record naming a process that is gone, and removes it, which is
+// what Git's own message says to do.
+func ownerRemovesLandersIndexLock(t *testing.T, f fixture) {
+	t.Helper()
+	landersIndexLock(t, f)
+	if err := os.Remove(filepath.Join(f.dir, ".git", "index.lock")); err != nil {
+		t.Fatal(err)
+	}
+}
+
+// landersIndexLock asserts that the checkout's index lock carries the record of a lander that
+// is no longer running.
+func landersIndexLock(t *testing.T, f fixture) {
+	t.Helper()
+	path := filepath.Join(f.dir, ".git", "index.lock")
+	data, err := os.ReadFile(path)
+	if err != nil {
+		t.Fatalf("the killed lander left no index lock: %v", err)
+	}
+	var record struct {
+		Kind string `json:"kind"`
+		Pid  int    `json:"pid"`
+		Host string `json:"host"`
+	}
+	if err := json.Unmarshal(data, &record); err != nil || record.Kind != "tillr-land-index-lock" || record.Pid <= 0 || record.Host == "" {
+		t.Fatalf("the index lock does not name the lander, its host and its process: %q (%v)", data, err)
+	}
+	if err := syscall.Kill(record.Pid, 0); err != syscall.ESRCH {
+		t.Fatalf("the lander the lock names (pid %d) is not gone: %v", record.Pid, err)
+	}
+}
+
 // landingRows answers every landing row's run, result, reason and trunk_after, in
 // insertion order.
 func landingRows(t *testing.T, db string) [][4]string {
@@ -212,6 +247,24 @@ func TestARecoveredLandingIsRecordedBeneathAnotherItemsLaterLanding(t *testing.T
 	}
 }
 
+// TestALanderKilledWithTheIndexLockLeavesItNamedAndTheNextLandingRemovesIt: the kill lands
+// during the final move, while the lander holds the checkout's index lock. The lock stays, as
+// a killed Git command's does, and says whose it is; the next landing removes it and lands.
+func TestALanderKilledWithTheIndexLockLeavesItNamedAndTheNextLandingRemovesIt(t *testing.T) {
+	f := newFixture(t)
+	other := f.candidateFrom(t, "other-item", "second.txt", "second\n")
+	db := filepath.Join(scratch(t, "store"), "state.db")
+	killAfterMove(t, f, f.passArgs(t, db, "item_042", "run-1"))
+	landersIndexLock(t, f)
+	code, out, stderr := passBoth(t, other.passArgs(t, db, "item_043", "run-2"))
+	if code != 0 || decodeLanding(t, out).Payload.Result != "landed" {
+		t.Fatalf("the next landing exited %d: %s %s", code, out, stderr)
+	}
+	if _, err := os.Stat(filepath.Join(f.dir, ".git", "index.lock")); err == nil {
+		t.Fatal("the dead lander's index lock is still there")
+	}
+}
+
 // TestAnUnrelatedCommitWithTheSameTreeIsNotTakenForTheLanding: the trunk holds the
 // candidate's exact tree, but in a commit this landing did not write — the owner rewound
 // the killed landing and committed the same change by hand. Tree equality is not the
@@ -221,6 +274,7 @@ func TestAnUnrelatedCommitWithTheSameTreeIsNotTakenForTheLanding(t *testing.T) {
 	db := filepath.Join(scratch(t, "store"), "state.db")
 	args := f.passArgs(t, db, "item_042", "run-1")
 	killed := killAfterMove(t, f, args)
+	ownerRemovesLandersIndexLock(t, f)
 	git(t, f.dir, "reset", "-q", "--hard", f.baseSHA)
 	write(t, filepath.Join(f.dir, "candidate.txt"), "candidate\n")
 	git(t, f.dir, "add", "candidate.txt")
@@ -282,6 +336,7 @@ func TestTheSameRunLandingADifferentHeadIsNotTakenForItsEarlierLanding(t *testin
 	f := newFixture(t)
 	db := filepath.Join(scratch(t, "store"), "state.db")
 	earlier := killAfterMove(t, f, f.passArgs(t, db, "item_042", "run-1"))
+	ownerRemovesLandersIndexLock(t, f)
 	git(t, f.dir, "checkout", "-q", "-b", "candidate-2", f.headSHA)
 	write(t, filepath.Join(f.dir, "more.txt"), "more\n")
 	git(t, f.dir, "add", "more.txt")
diff --git a/tools/tillr-land/land/landing.go b/tools/tillr-land/land/landing.go
index a0865cdbf..c162e1b2f 100644
--- a/tools/tillr-land/land/landing.go
+++ b/tools/tillr-land/land/landing.go
@@ -330,6 +330,11 @@ func Land(ctx context.Context, s *store.Store, t *Trunk, req LandingRequest) (an
 	// again, under the same lock: the transaction changed nothing.
 	decided := landing
 	busyPause := 25 * time.Millisecond
+	// changedAt is the readings of the prediction whose install answered that the owner changed
+	// a path. The next prediction must read something differently; when it reads the same, the
+	// owner changed nothing and the install itself is at fault, so the landing ends as a failed
+	// install does instead of repeating.
+	changedAt := ""
 	for {
 		prepared, err := prepareLanding(ctx, s, t, decided, req)
 		if err != nil {
@@ -339,6 +344,10 @@ func Land(ctx context.Context, s *store.Store, t *Trunk, req LandingRequest) (an
 		if prepared.stored != nil {
 			return *prepared.stored, nil
 		}
+		if changedAt != "" && prepared.plan != nil && prepared.plan.readings() == changedAt {
+			failure := &InstallFailure{Err: errors.New("the install answered that the owner changed a touched path, and the next prediction read every touched path, its index entry and the branch as before")}
+			return t.settleOwner(ctx, s, prepared, req, &publication{}, decided, failure)
+		}
 		// Only the final check, Move and receipt need SQLite's single writer. Close
 		// either wins during preparation or waits here and sees the committed receipt.
 		pub := &publication{}
@@ -369,6 +378,10 @@ func Land(ctx context.Context, s *store.Store, t *Trunk, req LandingRequest) (an
 		}
 		if errors.Is(err, ErrOwnerChanged) {
 			landing = decided
+			changedAt = ""
+			if prepared.plan != nil {
+				changedAt = prepared.plan.readings()
+			}
 			if err := ctx.Err(); err != nil {
 				return Landing{}, err
 			}
diff --git a/tools/tillr-land/land/owner.go b/tools/tillr-land/land/owner.go
index 225ba043f..bf3370bc5 100644
--- a/tools/tillr-land/land/owner.go
+++ b/tools/tillr-land/land/owner.go
@@ -3,6 +3,7 @@ package land
 import (
 	"bytes"
 	"crypto/rand"
+	"crypto/sha256"
 	"encoding/hex"
 	"errors"
 	"fmt"
@@ -69,6 +70,8 @@ var OwnerHooks struct {
 	FailUndoInspect func(path string) error
 	// AfterParentCheck runs after the last identity check of a path's parent and before the swap.
 	AfterParentCheck func(path string)
+	// FailComparison answers true to make the final comparison report an owner's change.
+	FailComparison func() bool
 }
 
 // ResetOwnerHooks clears every hook.
@@ -78,7 +81,7 @@ func ResetOwnerHooks() {
 	OwnerHooks.BeforeTrunkMove = nil
 	OwnerHooks.BeforeSwap, OwnerHooks.BeforeRollbackSwap, OwnerHooks.FailDisplacedRead = nil, nil, nil
 	OwnerHooks.FailRollbackSwap, OwnerHooks.FailIndexRestore, OwnerHooks.FailFaultEvent = nil, nil, nil
-	OwnerHooks.BeforeIndexLock = nil
+	OwnerHooks.BeforeIndexLock, OwnerHooks.FailComparison = nil, nil
 	OwnerHooks.FailRestoreExchange, OwnerHooks.FailUndoInspect, OwnerHooks.AfterParentCheck = nil, nil, nil
 	OwnerHooks.BeforeLastRead, OwnerHooks.BeforeUndoRead, OwnerHooks.InstallFault = nil, nil, nil
 }
@@ -137,6 +140,23 @@ type OwnerPlan struct {
 	GitDir, HeadText string
 }
 
+// readings is a digest of everything the prediction read that the owner can change: the branch,
+// an operation in progress, and each touched path's content on disk and index entry. Two
+// predictions with the same readings saw the same checkout.
+func (p *OwnerPlan) readings() string {
+	h := sha256.New()
+	field := func(parts ...string) {
+		for _, part := range parts {
+			fmt.Fprintf(h, "%d:%s,", len(part), part)
+		}
+	}
+	field(p.Worktree, p.HeadText, p.Busy, p.From, p.To)
+	for _, op := range p.Paths {
+		field(op.Path, op.Disk.Kind, fmt.Sprint(op.Disk.Exec), string(op.Disk.Bytes), op.Disk.Listing, op.Disk.Parents, op.IndexNow.Mode, op.IndexNow.OID)
+	}
+	return hex.EncodeToString(h.Sum(nil))
+}
+
 // Held answers whether the plan pauses the landing, and the paths or state to name.
 func (p *OwnerPlan) Held() []string {
 	if p == nil {
diff --git a/tools/tillr-land/land/owner_fs.go b/tools/tillr-land/land/owner_fs.go
index d541b525e..07613d85b 100644
--- a/tools/tillr-land/land/owner_fs.go
+++ b/tools/tillr-land/land/owner_fs.go
@@ -6,6 +6,7 @@ import (
 	"io"
 	"os"
 	"path"
+	"sort"
 	"strings"
 
 	"golang.org/x/sys/unix"
@@ -292,6 +293,156 @@ func (r *rootDir) sameDir(dirfd int, rel string) bool {
 	return a.Dev == b.Dev && a.Ino == b.Ino
 }
 
+// dirFile is one file of a directory the install displaced whole. sub is its path inside the
+// directory; handle stays open until the last read, as a displaced file's does.
+type dirFile struct {
+	path, sub string
+	planned   node
+	handle    *os.File
+}
+
+// exchangeDir replaces the directory base with want in one exchange, so the directory and
+// everything in it stand under a temporary name beside the path. It then reads the whole displaced
+// directory: every file there must be one the landing deletes, holding what the prediction read
+// (expected, keyed by checkout path). Anything else is the owner's, and the directory is exchanged
+// back before ErrOwnerChanged is answered. dirs are the subdirectories, parents first.
+func exchangeDir(dirfd int, rel, base string, want node, expected map[string]node) (tmp string, files []*dirFile, dirs []string, err error) {
+	tmp, err = makeTemp(dirfd, want)
+	if err != nil {
+		return "", nil, nil, Externalf("creating a temporary beside %s: %v", rel, err)
+	}
+	if err := unix.Renameat2(dirfd, tmp, dirfd, base, unix.RENAME_EXCHANGE); err != nil {
+		unix.Unlinkat(dirfd, tmp, 0)
+		if err == unix.ENOENT {
+			return "", nil, nil, ErrOwnerChanged
+		}
+		return "", nil, nil, Externalf("replacing %s: %v", rel, err)
+	}
+	files, dirs, cause := readDisplacedDir(dirfd, tmp, "", rel, expected)
+	if cause == nil && len(files) != len(expected) {
+		cause = ErrOwnerChanged // a file the landing deletes is no longer there
+	}
+	if cause == nil {
+		return tmp, files, dirs, nil
+	}
+	closeDirFiles(files)
+	return "", nil, nil, restoreDisplacement(dirfd, rel, base, tmp, true, want, cause)
+}
+
+func closeDirFiles(files []*dirFile) {
+	for _, f := range files {
+		if f.handle != nil {
+			f.handle.Close()
+			f.handle = nil
+		}
+	}
+}
+
+// readDisplacedDir reads the directory at root/sub in dirfd, never following a link.
+func readDisplacedDir(dirfd int, root, sub, rel string, expected map[string]node) (files []*dirFile, dirs []string, err error) {
+	fd, err := unix.Openat(dirfd, path.Join(root, sub), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
+	if err != nil {
+		return nil, nil, ErrOwnerChanged
+	}
+	dir := os.NewFile(uintptr(fd), root)
+	names, err := dir.Readdirnames(-1)
+	if err != nil {
+		dir.Close()
+		return nil, nil, Externalf("reading %s: %v", path.Join(rel, sub), err)
+	}
+	defer dir.Close()
+	sort.Strings(names)
+	for _, name := range names {
+		n, f, isDir, err := inspect(fd, name)
+		child := path.Join(sub, name)
+		if err != nil {
+			return files, dirs, err
+		}
+		if isDir {
+			dirs = append(dirs, child)
+			more, deeper, err := readDisplacedDir(dirfd, root, child, rel, expected)
+			files, dirs = append(files, more...), append(dirs, deeper...)
+			if err != nil {
+				return files, dirs, err
+			}
+			continue
+		}
+		full := path.Join(rel, child)
+		files = append(files, &dirFile{path: full, sub: child, planned: n, handle: f})
+		if want, ok := expected[full]; !ok || !n.equal(want) {
+			return files, dirs, ErrOwnerChanged
+		}
+	}
+	return files, dirs, nil
+}
+
+// displacedDirGrew answers whether the displaced directory at tmp holds a name that was not read
+// there after the exchange: a file the owner created in it since.
+func displacedDirGrew(dirfd int, tmp string, files []*dirFile, dirs []string) bool {
+	known := map[string]bool{"": true}
+	for _, f := range files {
+		known[f.sub] = true
+	}
+	for _, d := range dirs {
+		known[d] = true
+	}
+	for sub := range known {
+		if sub != "" && !contains(dirs, sub) {
+			continue
+		}
+		fd, err := unix.Openat(dirfd, path.Join(tmp, sub), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
+		if err != nil {
+			return true
+		}
+		dir := os.NewFile(uintptr(fd), tmp)
+		names, err := dir.Readdirnames(-1)
+		dir.Close()
+		if err != nil {
+			return true
+		}
+		for _, name := range names {
+			if !known[path.Join(sub, name)] {
+				return true
+			}
+		}
+	}
+	return false
+}
+
+func contains(list []string, s string) bool {
+	for _, v := range list {
+		if v == s {
+			return true
+		}
+	}
+	return false
+}
+
+// removeDisplacedDir removes a displaced directory after the landing is published: the files that
+// were read, each only while its name still is the inode the lander holds, then the directories
+// left empty, deepest first. It never removes a tree recursively, so anything that appeared there
+// meanwhile keeps the directory, and false is answered.
+func removeDisplacedDir(dirfd int, tmp string, files []*dirFile, dirs []string) bool {
+	for _, f := range files {
+		name := path.Join(tmp, f.sub)
+		if f.handle == nil {
+			if f.planned.Kind == "symlink" {
+				unix.Unlinkat(dirfd, name, 0)
+			}
+			continue
+		}
+		var byName, byHandle unix.Stat_t
+		if unix.Fstatat(dirfd, name, &byName, unix.AT_SYMLINK_NOFOLLOW) == nil && unix.Fstat(int(f.handle.Fd()), &byHandle) == nil &&
+			byName.Dev == byHandle.Dev && byName.Ino == byHandle.Ino {
+			unix.Unlinkat(dirfd, name, 0)
+		}
+	}
+	for i := len(dirs) - 1; i >= 0; i-- {
+		unix.Unlinkat(dirfd, path.Join(tmp, dirs[i]), unix.AT_REMOVEDIR)
+	}
+	return unix.Unlinkat(dirfd, tmp, unix.AT_REMOVEDIR) == nil
+}
+
 // removeEmptyTree removes the directory name and the directories beneath it, all of which must
 // be empty of files: a file there is the owner's and is ErrOwnerChanged.
 func removeEmptyTree(dirfd int, name string) error {
diff --git a/tools/tillr-land/land/owner_install.go b/tools/tillr-land/land/owner_install.go
index bcc96ae7c..d81d58ac3 100644
--- a/tools/tillr-land/land/owner_install.go
+++ b/tools/tillr-land/land/owner_install.go
@@ -30,6 +30,11 @@ type installed struct {
 	base      string
 	tmp       string         // the name the displaced inode keeps until the install is settled
 	stuck     *MutationError // a replacement that failed after changing the directory
+	// isDir marks a directory that became a file: the whole directory was exchanged for the
+	// file and stands at tmp, with files and dirs naming what was read in it.
+	isDir bool
+	files []*dirFile
+	dirs  []string
 }
 
 func (rec *installed) closeDir() {
@@ -155,6 +160,12 @@ func (t *Trunk) MoveOwner(plan *OwnerPlan, ref, to, from string) (*OwnerInstall,
 		}
 		return nil, err
 	}
+	if OwnerHooks.FailComparison != nil && OwnerHooks.FailComparison() {
+		in.unlockIndex()
+		in.root.close()
+		t.dropRecovery(plan)
+		return nil, ErrOwnerChanged
+	}
 	if err := t.checkPlan(plan, from); err != nil {
 		in.unlockIndex()
 		in.root.close()
@@ -166,7 +177,7 @@ func (t *Trunk) MoveOwner(plan *OwnerPlan, ref, to, from string) (*OwnerInstall,
 	if OwnerHooks.AfterInstall != nil {
 		OwnerHooks.AfterInstall()
 	}
-	if len(in.changedFiles()) > 0 {
+	if len(in.changedFiles()) > 0 || in.displacedDirsGrew() {
 		return retry(ErrOwnerChanged)
 	}
 	if err := plan.guard(); err != nil {
@@ -201,6 +212,13 @@ func (in *OwnerInstall) dropTemps() {
 		if rec.tmp == "" || rec.dirfd < 0 {
 			continue
 		}
+		if rec.isDir {
+			if !removeDisplacedDir(rec.dirfd, rec.tmp, rec.files, rec.dirs) {
+				in.leftover = append(in.leftover, fmt.Sprintf("%s: the directory it replaced holds files the landing did not read and is left at %s beside the path", rec.path, rec.tmp))
+			}
+			rec.tmp = ""
+			continue
+		}
 		var byName, byHandle unix.Stat_t
 		if rec.handle != nil && unix.Fstatat(rec.dirfd, rec.tmp, &byName, unix.AT_SYMLINK_NOFOLLOW) == nil && unix.Fstat(int(rec.handle.Fd()), &byHandle) == nil &&
 			byName.Dev == byHandle.Dev && byName.Ino == byHandle.Ino {
@@ -381,7 +399,31 @@ func (in *OwnerInstall) install() error {
 		}
 		return actions[i].Path < actions[j].Path
 	})
+	// A directory that becomes a file is replaced whole, so the deletions beneath it are not
+	// carried out one by one: the exchange covers them.
+	covered := map[string]map[string]node{}
 	for _, p := range actions {
+		if p.Install && p.Disk.Kind == "dir" {
+			covered[p.Path] = map[string]node{}
+		}
+	}
+	under := func(file string) string {
+		for dir := range covered {
+			if strings.HasPrefix(file, dir+"/") {
+				return dir
+			}
+		}
+		return ""
+	}
+	for _, p := range actions {
+		if dir := under(p.Path); dir != "" && p.Remove {
+			covered[dir][p.Path] = planned(p)
+		}
+	}
+	for _, p := range actions {
+		if p.Remove && under(p.Path) != "" {
+			continue
+		}
 		rec := &installed{path: p.Path, planned: planned(p), existed: p.Disk.Kind == "file" || p.Disk.Kind == "symlink", dirfd: -1}
 		if p.Install {
 			rec.want = node{Kind: "file", Exec: p.FinalExec, Bytes: p.FinalContent}
@@ -411,6 +453,24 @@ func (in *OwnerInstall) install() error {
 		if !in.root.sameDir(dirfd, p.Path) {
 			return ErrOwnerChanged
 		}
+		if p.Disk.Kind == "dir" && p.Install {
+			tmp, files, dirs, err := exchangeDir(dirfd, p.Path, base, rec.want, covered[p.Path])
+			if err != nil {
+				var me *MutationError
+				if errors.As(err, &me) {
+					rec.stuck = me
+				}
+				return err
+			}
+			rec.swapped, rec.isDir, rec.tmp, rec.files, rec.dirs = true, true, tmp, files, dirs
+			if !in.root.sameDir(dirfd, p.Path) {
+				return ErrOwnerChanged
+			}
+			if OwnerHooks.AfterReplace != nil {
+				OwnerHooks.AfterReplace(p.Path)
+			}
+			continue
+		}
 		if p.Disk.Kind == "dir" {
 			if err := removeEmptyTree(dirfd, base); err != nil {
 				return err
@@ -450,6 +510,20 @@ func (in *OwnerInstall) install() error {
 func (in *OwnerInstall) changedNodes() map[string]node {
 	saved := map[string]node{}
 	for _, rec := range in.done {
+		for _, f := range rec.files {
+			if f.handle == nil {
+				continue
+			}
+			now := f.planned
+			if _, err := f.handle.Seek(0, io.SeekStart); err == nil {
+				if b, err := io.ReadAll(f.handle); err == nil {
+					now.Bytes = b
+				}
+			}
+			if !now.equal(f.planned) {
+				saved[f.path] = now
+			}
+		}
 		if !rec.swapped || !rec.existed {
 			continue
 		}
@@ -462,6 +536,17 @@ func (in *OwnerInstall) changedNodes() map[string]node {
 
 func (in *OwnerInstall) changedFiles() map[string]node { return in.changedNodes() }
 
+// displacedDirsGrew answers whether a directory the install displaced holds a file that was not
+// there when it was read: the owner's, so the install is undone before the trunk moves.
+func (in *OwnerInstall) displacedDirsGrew() bool {
+	for _, rec := range in.done {
+		if rec.isDir && rec.tmp != "" && displacedDirGrew(rec.dirfd, rec.tmp, rec.files, rec.dirs) {
+			return true
+		}
+	}
+	return false
+}
+
 // writeIndex replaces the index once: the touched entries are applied to a copy and the copy is
 // renamed over the index while the lander holds the index lock. Untouched entries keep their
 // stat data.
@@ -609,6 +694,7 @@ func (in *OwnerInstall) undo() error {
 			rec.handle.Close()
 			rec.handle = nil
 		}
+		closeDirFiles(rec.files)
 		rec.closeDir()
 	}
 	if len(rep.versions) == 0 && len(rep.failures) == 0 {
@@ -696,6 +782,11 @@ func (in *OwnerInstall) undoOne(rec *installed, rep *undoReport) {
 	if isDir || !cur.equal(rec.want) {
 		// The owner saved at the path after the replacement: that is the newer content, and the
 		// original it superseded is kept when it differs from what the prediction read.
+		if rec.isDir {
+			rep.failures = append(rep.failures, fmt.Sprintf("%s was saved by the owner after its directory was replaced; the directory is kept at %s beside it", rec.path, rec.tmp))
+			rec.tmp = ""
+			return
+		}
 		rep.keep(rec.path, restore, rec.planned, false)
 		in.unlinkTmp(rec)
 		return
@@ -710,6 +801,16 @@ func (in *OwnerInstall) undoOne(rec *installed, rep *undoReport) {
 	if OwnerHooks.BeforeRollbackSwap != nil {
 		OwnerHooks.BeforeRollbackSwap(rec.path, step)
 	}
+	if rec.isDir {
+		// The same exchange made again: the directory returns whole, saved bytes included.
+		if err := unix.Renameat2(rec.dirfd, rec.tmp, rec.dirfd, rec.base, unix.RENAME_EXCHANGE); err != nil {
+			fail("%s still holds the landing's content (%v)", rec.path, err)
+			return
+		}
+		unix.Unlinkat(rec.dirfd, rec.tmp, 0) // the lander's own content, read above
+		rec.tmp = ""
+		return
+	}
 	if !rec.existed {
 		in.removeAdded(rec, rep)
 		return
@@ -817,6 +918,7 @@ func (in *OwnerInstall) Finish() []string {
 			rec.handle.Close()
 			rec.handle = nil
 		}
+		closeDirFiles(rec.files)
 		rec.closeDir()
 	}
 	in.root.close()
diff --git a/tools/tillr-land/owner_cases_test.go b/tools/tillr-land/owner_cases_test.go
index cc75a30d6..325e950ac 100644
--- a/tools/tillr-land/owner_cases_test.go
+++ b/tools/tillr-land/owner_cases_test.go
@@ -205,3 +205,137 @@ func TestASecondChangeLandsWhileAnotherIsHeldByAConflict(t *testing.T) {
 		t.Fatalf("the held change did not land once the owner restored: %+v", l)
 	}
 }
+
+// dirToFile is a landing that replaces the directory g, holding tracked files at two depths,
+// with a file.
+func dirToFile(t *testing.T) contentionFixture {
+	t.Helper()
+	base := func(d string) {
+		os.MkdirAll(filepath.Join(d, "g", "s", "t"), 0o755)
+		write(t, filepath.Join(d, "g", "s", "t", "y.txt"), "y\n")
+		write(t, filepath.Join(d, "g", "z.txt"), "z\n")
+	}
+	cand := func(d string) { os.RemoveAll(filepath.Join(d, "g")); write(t, filepath.Join(d, "g"), "now a file\n") }
+	f := newOwnerFixtureWith(t, base, cand)
+	resetHooks(t)
+	return f
+}
+
+// displacedDir answers the temporary name the directory g stands under after the exchange.
+func displacedDir(t *testing.T, d string) string {
+	t.Helper()
+	all, _ := filepath.Glob(filepath.Join(d, ".tillr-*.tmp"))
+	var found []string
+	for _, p := range all {
+		if st, err := os.Lstat(p); err == nil && st.IsDir() {
+			found = append(found, p)
+		}
+	}
+	if len(found) != 1 {
+		t.Fatalf("displaced directories: %v, want one", found)
+	}
+	return found[0]
+}
+
+func TestADirectoryIsReplacedByOneExchangeAndLeavesNothingBehind(t *testing.T) {
+	f := dirToFile(t)
+	d := f.f.dir
+	var during []string
+	land.OwnerHooks.AfterInstall = func() {
+		tmp := displacedDir(t, d)
+		for _, p := range []string{"z.txt", "s/t/y.txt"} {
+			if _, err := os.Stat(filepath.Join(tmp, p)); err != nil {
+				t.Errorf("the displaced directory does not hold %s whole: %v", p, err)
+			}
+		}
+		inner, _ := filepath.Glob(filepath.Join(tmp, "*", "*", ".tillr-*"))
+		top, _ := filepath.Glob(filepath.Join(tmp, ".tillr-*"))
+		during = append(inner, top...)
+	}
+	if l := mustLand(t, f); !l.Landed() {
+		t.Fatalf("landing: %+v", l)
+	}
+	if len(during) != 0 {
+		t.Errorf("the directory was emptied file by file: %v", during)
+	}
+	if left, _ := filepath.Glob(filepath.Join(d, ".tillr-*")); len(left) != 0 {
+		t.Errorf("left behind: %v", left)
+	}
+	if statOf(t, filepath.Join(d, "g")).body != "now a file\n" || git(t, d, "status", "--porcelain") != "" {
+		t.Errorf("status %q", git(t, d, "status", "--porcelain"))
+	}
+}
+
+func TestAFileAppearingInADisplacedDirectoryBeforeTheTrunkMovesIsPutBack(t *testing.T) {
+	for name, when := range map[string]func(func()){
+		"before the exchange": func(save func()) {
+			land.OwnerHooks.BeforeSwap = func(p string) {
+				if p == "g" {
+					save()
+				}
+			}
+		},
+		"after the exchange": func(save func()) { land.OwnerHooks.AfterInstall = save },
+	} {
+		t.Run(name, func(t *testing.T) {
+			f := dirToFile(t)
+			d := f.f.dir
+			saved := false
+			when(func() {
+				if saved {
+					return
+				}
+				saved = true
+				dir := filepath.Join(d, "g")
+				if st, err := os.Stat(dir); err != nil || !st.IsDir() {
+					dir = displacedDir(t, d)
+				}
+				write(t, filepath.Join(dir, "new.txt"), "mine\n")
+			})
+			l := mustLand(t, f)
+			wantHeld(t, l, "g", "g/new.txt")
+			for p, want := range map[string]string{"g/new.txt": "mine\n", "g/z.txt": "z\n", "g/s/t/y.txt": "y\n"} {
+				if got := statOf(t, filepath.Join(d, p)).body; got != want {
+					t.Errorf("%s = %q, want %q", p, got, want)
+				}
+			}
+			if left, _ := filepath.Glob(filepath.Join(d, ".tillr-*")); len(left) != 0 {
+				t.Errorf("left behind: %v", left)
+			}
+			if tip := git(t, d, "rev-parse", "HEAD"); tip != f.f.baseSHA {
+				t.Errorf("the trunk moved to %s", tip)
+			}
+		})
+	}
+}
+
+func TestAFileAppearingInADisplacedDirectoryAfterItsLastListingIsKeptWithAFault(t *testing.T) {
+	f := dirToFile(t)
+	d := f.f.dir
+	var kept string
+	land.OwnerHooks.BeforeTrunkMove = func() {
+		kept = filepath.Join(displacedDir(t, d), "new.txt")
+		write(t, kept, "mine\n")
+	}
+	l, err := land.Land(context.Background(), f.s, f.trunk, f.req)
+	if err != nil || !l.Landed() {
+		t.Fatalf("landing: %+v, %v", l, err)
+	}
+	if got := statOf(t, kept).body; got != "mine\n" {
+		t.Fatalf("the owner's file is gone: %q", got)
+	}
+	if len(l.Faults) != 1 || !strings.Contains(l.Faults[0], filepath.Base(filepath.Dir(kept))) {
+		t.Fatalf("faults = %v, want one naming %s", l.Faults, filepath.Dir(kept))
+	}
+	if n := eventsOfKind(t, f.db, land.FaultKind); n != 1 {
+		t.Fatalf("fault events = %d, want 1", n)
+	}
+	if statOf(t, filepath.Join(d, "g")).body != "now a file\n" {
+		t.Error("the landed file is not at the path")
+	}
+	for _, p := range []string{"z.txt", "s"} {
+		if _, err := os.Stat(filepath.Join(filepath.Dir(kept), p)); err == nil {
+			t.Errorf("%s, which the landing deleted, is still in the displaced directory", p)
+		}
+	}
+}
diff --git a/tools/tillr-land/owner_retry_test.go b/tools/tillr-land/owner_retry_test.go
index b257f4aa6..9fe712ec9 100644
--- a/tools/tillr-land/owner_retry_test.go
+++ b/tools/tillr-land/owner_retry_test.go
@@ -14,7 +14,7 @@ func TestOwnerSavesDoNotConsumeAnArbitraryLandingRetryBudget(t *testing.T) {
 	resetHooks(t)
 	f := newOwnerFixture(t, nil)
 	path := filepath.Join(f.f.dir, "a.txt")
-	const saves = 201
+	const saves = 25
 	predictions := 0
 	land.OwnerHooks.AfterPredict = func() {
 		predictions++
@@ -29,7 +29,7 @@ func TestOwnerSavesDoNotConsumeAnArbitraryLandingRetryBudget(t *testing.T) {
 	if predictions != saves+1 {
 		t.Fatalf("predictions = %d, want %d", predictions, saves+1)
 	}
-	want := "L1\n" + strings.TrimSuffix(strings.TrimPrefix(lines, "l1\n"), "l9\n") + "save-201\n"
+	want := "L1\n" + strings.TrimSuffix(strings.TrimPrefix(lines, "l1\n"), "l9\n") + "save-25\n"
 	if got := statOf(t, path).body; got != want {
 		t.Fatalf("owner's last save was not carried: got %q, want %q", got, want)
 	}
@@ -37,3 +37,34 @@ func TestOwnerSavesDoNotConsumeAnArbitraryLandingRetryBudget(t *testing.T) {
 		t.Fatalf("clean landing left recovery refs: %s", refs)
 	}
 }
+
+// TestAChangeReportedWithNothingSavedEndsTheLandingAfterOneFurtherPrediction: the comparison
+// answers that the owner changed a path while the checkout holds what the prediction read. The
+// next prediction reads the same, so the lander is at fault, not the owner: the landing returns
+// with a recorded fault and the owner's file untouched, instead of predicting without end.
+func TestAChangeReportedWithNothingSavedEndsTheLandingAfterOneFurtherPrediction(t *testing.T) {
+	resetHooks(t)
+	f := newOwnerFixture(t, nil)
+	path := filepath.Join(f.f.dir, "a.txt")
+	write(t, path, strings.TrimSuffix(lines, "l9\n")+"mine\n")
+	before := statOf(t, path)
+	predictions := 0
+	land.OwnerHooks.AfterPredict = func() { predictions++ }
+	land.OwnerHooks.FailComparison = func() bool { return true }
+	l, err := land.Land(context.Background(), f.s, f.trunk, f.req)
+	if err == nil || l.Landed() || !strings.Contains(err.Error(), "fault recorded") {
+		t.Fatalf("landing = %+v, %v; want a recorded fault", l, err)
+	}
+	if predictions != 2 {
+		t.Fatalf("predictions = %d, want 2", predictions)
+	}
+	if got := statOf(t, path); got != before {
+		t.Fatalf("the owner's file changed: %+v, was %+v", got, before)
+	}
+	if n := eventsOfKind(t, f.db, land.FaultKind); n != 1 {
+		t.Fatalf("fault events = %d, want 1", n)
+	}
+	if tip := git(t, f.f.dir, "rev-parse", "HEAD"); tip != f.f.baseSHA {
+		t.Fatalf("the trunk moved to %s", tip)
+	}
+}
# owner-edits-patch end
````

## Filed 2026-11-17: tillr becomes a core with Git landing as its first definition

The owner said on 2026-11-17 to build as much of the new tillr as possible, so that the Product QA
pipeline can try the real thing and comment on it in the next round.
[The build plan](../design/tillr-definitions-plan.md) holds the eighteen items: the foundations
the pipeline asked for, the definition file and the store that records it, the item lifecycle and
the planner read from the definition, the hook program contract, a store that does not land, and a
worked example that lands nothing. The blocks live in that plan rather than in
[the direction](../design/tillr-core-and-git-landing.md), because the direction still carries open
questions and the drain would hold its items. The plan's next tranche is written in prose and is
filed when the first has landed.

## Filed 2026-11-17: two red bases that held queue items

Two queue items stopped on 2026-11-17 because a check that is red on `main`, and that the item
did not touch, failed under them. Each worker stopped as its brief says, and the drain held the
item. The two items below fix those checks, and each held item now waits on its fix.

### Fix the flaky full-trace-queue test in the store

The store has a test, `TestFullTraceQueueNeverBlocksAWriterAndReportsTheLoss`, that fails now and
then on `main` with nothing changed. On 2026-11-17 it was red at the base of the run that was
building the sharded-runner item, so that worker could not tell its own failures from this one,
stopped, and the item was held. A test that fails at random also makes every other item's gate
result doubtful, so it is worth fixing before more work is stacked on it.

The race is in the test, not in the trace queue. The queue's delivery goroutine writes the loss
report into the captured stderr pipe, and only then records the loss as reported, so once the
flush returns the line is in the pipe. The test then reads its capture buffer at once, but a
separate copier goroutine fills that buffer from the pipe, and under load it has not run yet. The
test sees an empty capture and reports that the trace never reported its loss. The product code
loses nothing, and the fix belongs in how the test waits for its own capture.



**Agent details:**

- **Where:** `capturedStalledSQLiteStderr` and the test body in `tools/tillr/store/trace_queue_test.go`; `reportLoss` and `FlushSQLiteTrace` in `tools/tillr/store/trace.go`, which this item reads and does not change.
- **Evidence:** 2026-11-17, with the load average near 26 on a 32-core machine, 24 copies of the compiled test binary each ran the test 100 times, and 14 of the 24 failed. The failure line was `trace_queue_test.go:190: the trace never reported its loss (want a line ending "dropped spans=0 records=20 sink_errors=0"): []`. On the same machine, `go test -count=50` and `go test -race -count=200` of that one test, run alone, both passed, so the failure needs load.
- **Evidence:** `reportLoss` in `tools/tillr/store/trace.go` flushes the report to the sink before it stores the reported count, and `FlushSQLiteTrace` waits for that count, so the product's order is correct. The test's `read` returns the buffer the copier goroutine fills, with no wait for that copier.

### Prevent strict validation failures in the cloud-usage mod

The cloud-usage mod keeps a journal of Claude Code's model usage. Strict plugin validation treats
warnings as errors, so it must pass before workers can use it to check their changes. On
2026-11-17 it failed on the unchanged mod with the installed Claude Code. A worker adding channel
and route intent reproduced the failure on a copy of its base revision and stopped, leaving that
work on hold. Fixing the underlying warnings will let workers distinguish problems in their own
changes from problems already present in the mod.

The warnings come from TypeScript checks on the mod's hooks. Missing type declarations generated
by Claude Code are the leading suspect, but that cause has not been established. The implementing
agent must determine whether the declarations, the mod's configuration, its sources, or a
combination need fixing. Validation must still check the hooks and reject warnings after the fix.

**Outcomes:**

- Pass strict validation from a clean checkout without weakening the checks.
- Catch future validation failures with an automated test and document any setup needed.
- Keep the mod's existing behavior and tests working.



**Agent details:**

- **Where:** `yolo-packs/matt-mods/mods/cloud-usage/tsconfig.json`, `hooks/*.ts` and `tests/*.ts` in that mod; the "Develop and test" section of `yolo-packs/matt-mods/README.md`; the "Offline development" section of the mod's README.
- **Investigate:** The mod's `tsconfig.json` extends `.claude-plugin/types/tsconfig.json`. Claude Code writes that configuration and its type declarations on first load; the repository ignores the generated directory. Establish whether its absence explains the warnings before choosing a fix.
- **Verify:** Run the strict validation command in c1 and record `claude --version` and the date. Add the regression test specified in c3, check the clean-checkout instructions in c4, and run every existing suite and pack lint listed in c5.
- **Evidence:** 2026-11-17, `claude plugin validate --strict yolo-packs/matt-mods/mods/cloud-usage` listed 22 warnings and ended `Validation failed (--strict treats warnings as errors)`: 12 TS5097 (an import path ending in `.ts` while `allowImportingTsExtensions` is off), 5 TS2345 and 4 TS2532 (possibly undefined values), and one TS2304 (`Cannot find name 'setTimeout'`). The same command also failed for `todo-cleanup` and `tok-stats`.
- **Evidence:** 2026-11-17, `.claude-plugin/types/` is absent from the mod's checkout and from `~/.claude/mods/cloud-usage` in this jail, and `.gitignore` lists `/yolo-packs/matt-mods/mods/*/.claude-plugin/types/`. A copy of the mod validated outside the repository failed the same way. No test or recipe invokes the validator: `rg -n 'plugin validate' tests .justfile tools/fork-check.py` finds nothing outside the READMEs and docs.

## Filed 2026-11-17: make the Go suites fast and trustworthy

A worker building one queue item on 2026-11-17 spent most of 35 minutes waiting on repeated full
runs of the drain's Go suite. The drain's rolling run executes the same suites inside the loop
after each trunk move, and has been seen taking from 12 seconds to about 3 minutes, which delays
dispatch while it runs. The measurements below were made on 2026-11-16 and 2026-11-17 on a
32-core machine with go 1.27.3 and git 2.56.0, on an exported copy of the tools directory, with
the load average between 3 and 30. These four items make the suites fast, keep their cached
results honest, and stop them leaving files behind. The gate forbids tests that assert on elapsed
wall-clock time, so every speed claim is evidence in a report, never a test assertion.

### Add a sharded runner for the tillr Go suites

A full `go test ./... -count=1` in the drain module takes about 64 seconds, yet it uses only
about 1.7 of the machine's 32 cores. None of the 462 tests calls `t.Parallel()`, and the heavy
ones set process environment variables that stub programs read, so marking them parallel does not
help: a trial on the 127 root tests that do not set environment left the wall time unchanged. The
shared state is process-wide, but separate processes do not share it. Building each package's
test binary once and running several of them side by side, each with its own slice of the tests,
took the same suite to 13.7 to 14.5 seconds with six shards. The drain's rolling run and the
repository gate both pay the 64 seconds today, and the cost estimate the gate plans with still
says 30.9 seconds.


**Ruling, 2026-11-17 (answers the second failed attempt):** two runs stopped, for two unrelated
reasons, and neither reason is in this design. The criteria c1 to c8 stand as written.

The first run stopped on the store's flaky test `TestFullTraceQueueNeverBlocksAWriterAndReportsTheLoss`,
which was red at its base. It was right to stop. That test is fixed on the trunk in commit
`f4bbfaf26`, so it is no longer a reason to stop, and this item does not touch store tracing.

The second run built the whole item, and the gate refused it on one check out of eleven. The
whole-tree `scratch` check failed on line 64 of the run's report,
`docs/pi/tillr-go-shards-2026-11-17.md`, where a sentence about shard isolation named the durable
scratch directory's environment variable in inline code. The other ten checks passed. The
repository forbids a tracked Markdown file from naming that directory, by its variable or by its
path, outside a fenced block; the two spellings are listed in the root `AGENTS.md` under
"Documents: state the evidence, never cite scratch". The report says "its own scratch directory"
in prose, and puts any command that needs the variable in a fenced block. c9 below makes the
builder run that check before committing. The check's exception list is not extended for this.

The second run's candidate is the starting point, not something to rebuild from nothing:
- [unlanded] commit `c5f377844`, reachable from `refs/tillr/runs/run-item-shard-the-tillr-go-suites-a2`, holds the runner, its duration record, its tests, the planner change and the report.

The trunk has moved since that commit's base, so the builder re-applies it rather than reusing
its tree. Three things differ on the trunk now, and each is settled here:

- **The skill conflicts.** `git merge-tree` on 2026-11-17 reported one content conflict, in
  `yolo-packs/tillr/skills/tillr-operating/SKILL.md`, because the trunk gained a paragraph on Go's
  test result cache at the same place. Both texts stay. The worker brief
  `tools/tillr-run/backends/brief-template.md` merged cleanly at the same paragraph and is read
  again by hand for the same reason.
- **The worker guidance has two tiers.** While editing, a worker uses a targeted `-run`, and in
  the drain module leaves off `-count=1`, because the drain's result cache is now trustworthy.
  For the one full run before committing, a worker uses the sharded runner on every module the
  gate runs through it. The runner always runs tests fresh, since it executes the built test
  binaries itself, and the guidance says so. c7 is read this way.
- **The native command keeps the cache.** The second run added `-count=1` to the planner's
  command for the modules that stay on `go test`. That is dropped: those commands stay as the
  trunk has them. Only the modules listed in the report as faster under the runner change.

The delivered copies under `yolo-packs/tillr/` are regenerated with `just pack-sync` after the
re-apply, not merged by hand. The before and after wall times in the report are measured again
on the new base, since the suites changed, and the planner's estimates follow those numbers.

**Ruling, 2026-11-17 (answers the fourth and fifth runs):** two more runs stopped, again for
reasons outside this design. c1 to c9 stand as written, and c10 and c11 are added below. Where
this ruling and the one above disagree, this one holds.

The fourth run built the whole item again and the gate refused it on one check,
`observatory-dev`, which is `python3 tests/observatory-dev.test.py`. Every candidate for this item
meets that suite, because the gate selects it when `.justfile` or the gate's own code changes.
The suite is red in any fresh lane worktree with no change applied: its supervision cases run the
real recipe with a stub `npm` whose `ci` does nothing, and the recipe then writes a platform stamp
into a `node_modules` directory that nothing made. The repair is also criterion c8 of "Add the
landing gate's whole-tree checks to just done and rolling", which has not landed. This item
cannot land without the suite passing, so it carries the same repair as c10, in the same words,
and whichever of the two items lands second finds the suite already green and leaves it alone.
The builder runs the suite on the staged base first. If it passes there, c10 is already met and
the change does not touch `tests/observatory-dev.test.py`. `tools/observatory/run.sh` is right
about a real `npm ci` and is not changed either way.

The fifth run stopped before building anything, because one test in the `tools/tillr` module is
red on the trunk: `TestEveryStoreProgramFlushesTheSQLiteTraceOnExit`, in the `store` package. The
test allows a store program exactly one `os.Exit`, the one in `main` that follows the trace
flush. Commit `89afe7e39` added a second in the drain module, in the `init` function of
`tools/tillr-drain/drain/heavy_supervisor.go`, which is the entry a drain binary re-executes
itself through to supervise a heavy judge. The run was right that this is not its change. It is
not this item's to repair either: the repair is a choice about the heavy runner's entry or about
the trace contract, and both belong to a trunk fixer. This item does not edit
`tools/tillr/store/` or `tools/tillr-drain/drain/heavy_supervisor.go`.

That red test does not stop this item, because no criterion needs it green. c2 compares names on
the drain module, which does not contain the test. In c5's table the `tools/tillr` module runs
with the test failing under both commands, and the comparison of passing names is still exact.
The builder reproduces the failure on the staged base before any edit. If the command below
fails with only that test, the builder records it under c11 and carries on to `RESULT: done`. If
it passes, the trunk was repaired and nothing is recorded. Any other failure on the staged base
is a new reason to stop. This is not a waiver for the gate: if the gate refuses the candidate on
that test, the refusal is the trunk's and goes to a fixer, and the candidate is kept.

```bash
cd tools/tillr && CGO_ENABLED=0 go test ./store -run '^TestEveryStoreProgramFlushesTheSQLiteTraceOnExit$' -count=1
```

The fourth run's candidate replaces the second run's as the starting point. It already holds what
the ruling above asked for: both skill paragraphs, the two-tier worker guidance, the native
commands without `-count=1`, a report that passes the `scratch` check, and a fix for a package
that holds only benchmarks, whose `TestMain` the runner skipped until then.
- [unlanded] commit `96b303849`, the first parent of the tip of `refs/tillr/runs/run-item-shard-the-tillr-go-suites-a4`, holds that candidate. The tip itself only retires this block and is not re-applied.

The builder re-applies that commit on the new base, regenerates the delivered copies with
`just pack-sync`, and measures again. One thing changed in the measurements between the fourth
run's two passes: `tillr-run` was faster under the runner in one and slower in the other. A
module whose two walls are within ten percent of each other stays on its native command, so the
planner's choice does not flip from run to run.


**Agent details:**

- **Where:** a new runner beside `tools/fork-check.py`; the drain module in `tools/tillr-drain/`; `GO_TOOL_COST_ESTIMATE` and its comment in `tools/fork-check.py`; the `.justfile` recipe that runs the gate; `yolo-packs/tillr/`.
- **Evidence:** 2026-11-16, `go test ./... -count=1` in the drain module: 64.0 s and 64.3 s wall at load 3 to 6 (package `drain` 215 tests in 64 s, root package 247 tests in 55 s, run side by side), 69.3 s at load 17 to 23, 118 s with `-p 1`. User 70 s plus system 37 s over 64 s wall is about 1.7 cores. Compile and link cost about 2 s. A run makes about 18,900 `git` execs (11,505 of them `git rev-parse`). Turning git's fsync off changed nothing (63 to 66 s); test directories on a memory filesystem saved about 5 s and made `TestFenceViolationScopesAForkCheckoutToItsRepoRelativePath` fail.
- **Evidence:** 2026-11-16, sharding with a test binary built once per package and balanced on recorded durations: K=6 took 13.7 to 14.5 s, K=8 took 14.5 s, K=4 took 17.9 s, at load 7 to 30. The only failures were three tests that need the `config/` directory, absent from the exported copy.
- **Evidence:** 2026-11-16, `t.Parallel()` trial on the 127 of 247 root tests that do not call `t.Setenv`: root wall unchanged at 55.0 s. `t.Setenv` appears at 43 call sites in the root package and 17 in `drain`, and the stub adapters read `GATE_ROOT`, `STUB_ROOT`, `RETIRE_*` and `PASS_*` from the environment.
- **Evidence:** the cost estimate was recorded at 30.9 s on 2026-11-15; `rg -n 'COST_ESTIMATE|30\.9' tools/fork-check.py` finds it (2026-11-17).
- **Evidence (c10):** 2026-11-17, run `run-item-shard-the-tillr-go-suites-a4`: the gate ran seven checks and failed one, `observatory-dev`, after 4 min 40 s, with seven failed cases and the line `observatory-dev: npm ci (there are no node_modules)`. `ls tools/tillr-view/web/node_modules` in a lane worktree at `fb90b1a96` on the same day reports no such directory.
- **Where (c10):** `write_stubs` in `tests/observatory-dev.test.py` writes the stub `npm`; `ensure_modules` in `tools/observatory/run.sh` runs `npm ci` when the directory is missing and then writes the stamp.
- **Evidence (c11):** 2026-11-17, in a lane worktree at `fb90b1a96` with no change applied, the command in the ruling exited 1 with `../../tillr-drain: the program calls os.Exit 2 times outside tests; only main's flushed exit may`. `rg -n 'os\.Exit' tools/tillr-drain --glob '*.go' --glob '!*_test.go'` finds the call in `main.go` and the one in `drain/heavy_supervisor.go`.
- **Evidence (c11):** 2026-11-17, `tillr-item list` on the repository's store showed six `fixer-main-*` items, all closed at 09:19 UTC, before `89afe7e39` was authored at 09:48 UTC. No open fixer names this test.

### Route the slowest drain and store tests through injected clocks

Several of the slowest tests spend their time waiting on real clocks that the code under test
could take from the test instead. Waiting is not what these tests check: they check what the loop
does after a hold, a missed restart or a busy store, not that a second really passed. Each wait
is small, but together they are about a fifth of the drain suite and most of the store suite, and
the owner pays them on every full run. The fix is to make each wait a setting that production
leaves alone and a test shortens, and to prove each shortened test still catches the bug it was
written for.



**Agent details:**

- **Where:** `StorePollInterval` is a `const` in `tools/tillr-drain/drain/trigger.go`; the store open and its busy timeout in `tools/tillr/store/`; `checkpointCycleLoop` and `heartbeatClock` in the drain module; the run module's terminal-cause test in `tools/tillr-run/`.
- **Evidence:** 2026-11-17, `TestDefaultWaitReportsFiveHoldsOnceAcrossOneHundredIdlePasses` takes 11.4 s, which is 101 idle passes at 100 ms. With the interval at 1 ms it took under 1.4 s and the `drain` package went from 59 s to 50 s.
- **Evidence:** 2026-11-17, `TestABusyCheckpointWriteDoesNotFaultTheLoop` 6.1 s and, in the store, `TestABusyOwnerWriteSkipsTheCycleWithoutAnError` 5.0 s wait out `busy_timeout(5000)`. `TestAMissedRestartIsReportedAndRetriedAtTheNextHeartbeat` 2.07 s waits two real 1 s heartbeats although a `heartbeatClock` seam exists that `checkpointCycleLoop` does not use. `TestALaneHeldByAFiniteLeaseIsWaitedOnNotStopped` 2.19 s seeds a real 2 s lease although the loop takes a clock. `TestBlockedTraceSinkDoesNotRetainRealSessionMutex` 2.24 s sleeps 2200 ms for an observer threshold. `TestStopDuringWallContinuation` 2.85 s uses a 1 s backend wall timeout with a stub `sleep 3`. In the run module `TestTerminalEndCauses/wall` takes 1.0 s.
- **Evidence:** 2026-11-17, the `tools/tillr` suite takes 13.5 s, all in `store`, about 10 s of it the busy timeout and three waits of about 2.1 s on the trace threshold. `tools/tillr-view` takes 9.2 s (145 tests, none parallel) and is not in this item.

### Prevent a cached test pass from hiding a broken sibling program

Go's test cache decides whether a package's tests need to run again by looking at the package's
own sources and inputs. The drain's root tests build the sibling programs `tillr-item`,
`tillr-run`, `tillr-land`, `tillr-gate` and `tillr-view` with a child `go build`, and the cache
cannot see those sources. A change to one of them can therefore leave the drain's tests reporting
`(cached)` and passing, although a fresh run would fail. That is a green result that is not true,
and it is the reason workers are told to pass `-count=1` every time, which makes the slow suite
slower still. Once the cache is trustworthy, workers can use it.



**Agent details:**

- **Where:** the child `go build` calls in the root test files of `tools/tillr-drain/`; `yolo-packs/tillr/skills/tillr-operating/` and the worker brief in `yolo-packs/tillr/`.
- **Evidence:** 2026-11-17, with `println(...); os.Exit(9)` inserted at the top of `tillr-item`'s `main()`, `go test ./...` in the drain module still printed `ok ... (cached)` in 1.0 s. Forcing a re-run made the root package fail. On an unchanged tree `go test ./...` without `-count=1` returned in 1.0 s.

### Prevent test runs from leaving built-binary directories behind

Each tillr module's `TestMain` builds the programs its tests need into a scratch directory, then
defers removing it and calls `os.Exit` with the test result. `os.Exit` skips deferred calls, so
the removal never runs and every run leaves its directory behind. The directories sit in the
durable scratch directory, outside the repository, and nobody sees them grow until the disk
fills. The drain's own are about 38 MB each. The fix is a `TestMain` that removes the directory
before it exits, on every path, and a check that keeps it true.



**Agent details:**

- **Where:** the `TestMain` in the root test file of each tillr module, for example `tools/tillr-drain/main_test.go`, `tools/tillr-item/main_test.go` and `tools/tillr-land/main_test.go`; the drain trace directory is in the drain tests; a new check under `tests/`.
- **Evidence:** 2026-11-17, `rg -n 'os.Exit|RemoveAll' tools/tillr*/main_test.go` shows `defer os.RemoveAll(dir)` followed by `os.Exit(...)` in the root test files of `tillr-drain`, `tillr-item`, `tillr-land`, `tillr-cli`, `tillr-view`, among others.
- **Evidence:** 2026-11-17 re-count in the durable scratch directory: 1,555 `tillr-drain-bin-*`, 138 `tillr-item-bin-*`, 103 `tillr-run-bin-*`, 97 `tillr-land-bin-*`, 96 `tillr-gate-bin-*` and 145 `tillr-drain-trace-*` directories, 2,134 in all. One `tillr-drain-bin-*` sample measured 38 MB with `du -sh`. Earlier the same day the count was 1,439 drain directories, about 50 GB by `du`.

## Filed 2026-11-17: keep a hand commit from leaving the trunk red unseen

### Add the landing gate's whole-tree checks to just done and rolling

A commit made on the trunk by hand, outside the drain, is verified by `just done`. The landing gate
judges a candidate with a different and larger set of checks: eleven fast checks, five of which read
the whole tree rather than the candidate's own change. `just done` does not run those five, and the
drain's rolling run does not run them either. A hand commit can therefore leave the trunk red for
every later candidate, with no check saying so. On 2026-11-17 that happened: a hand commit passed
`just done`, the rolling run went green over the range that held it, and the drain's gate then
refused the next candidate because a whole-tree check failed on the trunk itself. The candidate's
own change was sound, yet its item was held as a design conflict and a design seat was sent to
answer a failure that was never in its design. This item makes the two verifying tiers see what the
gate sees, files a fixer when the trunk is red, and stops a red trunk from being charged to the
candidate that happened to meet it.


**Ruling, 2026-11-17 (answers the first run's fence conflict):** the first run stopped before
editing because c6 changes the waiting view, whose source is `tools/tillr-view/waiting.go`, and
that directory was outside the item's paths. The run was right to stop. The directory is now
inside the paths, with its tests, and its delivered copy under `yolo-packs/tillr/` is synced in
the same commit. The run also looked for a waiting rule named `trunk-already-red` and found
none. None is needed: `trunk-already-red` is the step name on the refusal record, and the hold
that the waiting view shows is the existing reason `trunk-broken`. c4 and c6 are restated below
to say so, and no new exported hold reason is added. The other criteria are unchanged.

**Ruling, 2026-11-17 (answers the third and fourth runs' refusals):** neither refusal was about
the design in c1 to c7, which stands as written. Two things stopped the work.

The third run was refused at the fence because its commit edited this block. The item's paths
include `docs/`, and this file is under it, but a worker never edits `docs/tillr/roadmap.md`:
the block is the design seat's, and the lander retires it. No criterion asks for a change to this
file. The documents c6 names are the only ones the change must touch.

The fourth run's candidate was refused by the gate on one check, `observatory-dev`, which is
`python3 tests/observatory-dev.test.py`. The gate selects that suite whenever `.justfile`
changes, and c1 requires a `.justfile` change, so every candidate for this item meets it. The
suite is red in a lane worktree with no change at all: its supervision cases run the real recipe
in the checkout the suite is in, with a stub `npm` whose `ci` does nothing, and the recipe then
writes a platform stamp into a `node_modules` directory that the stub never made. The main
checkout has that directory, so the suite passes there and fails in any fresh worktree. The
failure is the suite's, not the candidate's, and the suite's own header says it needs no
`node_modules`. Because this item cannot land without passing that suite, the repair is part of
this item, as c8. It is made in `tests/observatory-dev.test.py`, which is inside the paths;
`tools/observatory/run.sh` is right about a real `npm ci` and is not changed.

**Ruling, 2026-11-17 (answers the sixth run's conflict):** the run was right to stop, and c4 as
it stood was wrong about the tree. It said the loop holds an acquitted item with the reason
`trunk-broken` and gates it again after the fixer lands, "the same way a fork-check acquittal
does". A fork-check acquittal does neither. It sets the item's state to `blocked`, with the fixer
recorded as the waiter, and nothing ever takes the item out of that state: the planner answers the
ordinary `blocked` reason for it, because that rule is read before the red-trunk rule, and it
still answers `blocked` after the fixer has landed. An acquitted item is therefore stranded until
a person moves it by hand. The run's reproduction is in the evidence below.

The return path does not exist, so this item builds it, for every acquittal: the tree-check
acquittal this item adds and the fork-check acquittal that is already there share one path. Four
things are settled.

- The acquittal is unchanged. It still records the `trunk-already-red` refusal, files the
  deduplicated fixer, and sets the item to `blocked` with the waiter `item:<fixer id>`. The existing
  test that asserts that state stays as it is.
- While that fixer is not landed, the planner's reason for the item is `trunk-broken`, and the
  decision names the fixer as what it waits on. A `blocked` item whose waiter is anything other
  than a trunk fixer keeps the ordinary `blocked` reason.
- Once that fixer is in the state `landed`, the next pass returns the item to the state it had
  before the acquittal, which the item store already keeps as the underlying state, and clears the
  waiter. Nobody runs a command. A fixer that ends in any other terminal state releases nothing:
  the item stays `blocked` for the owner, because the trunk was never shown to be repaired.
- The returned item is dispatched as a fresh run from the repaired trunk, and that run's candidate
  is what the gate judges. The old candidate is not kept and gated again. Keeping it would hold a
  lane for as long as the fixer takes, and with one lane the fixer could then never run. The cost is
  one more model run for each acquitted item, which is accepted. That dispatch is not an owner's
  retry: the rule that holds a second attempt until a person authorizes it does not apply to an
  item the loop itself released, and the acquitted attempt is not counted as a failure when the
  loop decides whether the item's attempts are exhausted.

c4 is restated below to say this. No other criterion changes, and no new hold reason is added.

**Ruling, 2026-11-17 (answers the eighth run's conflict):** the run was right to stop, and nothing
in c1 to c8 changes. The run reapplied the earlier candidate onto a moved trunk, and its targeted
tests passed. It stopped because the full `tools/tillr-drain` suite showed three failures that the
candidate did not cause. Two of them are red on the trunk with no change applied, and the third
depends on machine load. The evidence is below.

The two trunk failures both come from commit `bb6e286b5`, which made every criterion carry a claim
class: a stored label that says who may settle the criterion. A criterion with no label is read as
`verification-only`, meaning a passing check settles it. That commit refuses a criterion whose text
begins "Owner decision recorded" unless its label is something other than `verification-only`, and
it makes the loop's verdict step refuse a criterion that `tillr item show` prints with no label.
The commit did not update two things that the drain suite reads:

- `config/tillr/seed-items.json` has eleven criteria whose text begins "Owner decision recorded",
  and none states a claim class. Seeding stops at the first, in `item-pile-507-allowlist-hunt`.
- The stand-in `tillr-item` in `tools/tillr-drain/drain/step_argv_contract_test.go` prints a
  criterion with no `claim_class` field, so the real adapter refuses the drain's verdict call.

Every candidate for this item changes `tools/tillr-drain/`, so every candidate meets that suite,
and the item cannot land while the suite is red. An acquittal would not help: on today's trunk an
acquitted item is stranded, which is the fault c4 repairs. The two repairs are therefore part of
this item, as c9, in the same way as c8. `config/tillr/` is now inside the paths for the seed
file. Neither repair changes the claim-class rule itself: `tools/tillr-item/` and
`tools/tillr-loop/` are not changed.

The third failure, `TestHeavyKilledOwnerReapsJudgesBeforeCheckoutRecovery/legacy`, is not this
item's and is not repaired here. It waits 20 seconds for a child process to start, and it passed
three times out of three when run alone on the same commit. A run that sees it fail in the full
suite runs it alone once. If it passes alone, the run reports both results with the command and
date and carries on to its commit. If it fails alone, the run stops as before.

The eighth run's resolved tree is preserved under the ref
`refs/tillr/preserved/run-item-whole-tree-checks-in-done-and-rolling-a8-cfix`. The next run starts
from it and adds c9, and does not redo c1 to c8.

**Ruling, 2026-11-17 (answers the eleventh and twelfth runs' conflict):** the twelfth run was right
to stop, and c9 as it stood was wrong about the tree. Nothing in c1 to c8 changes. While this item
waited, trunk commit `d0eb7b405` made both repairs that c9 asked for, and made the first one a
different way. c9 then named a test that no longer exists and asked for an edit that the gate
refuses.

- The seed file is frozen. It is the historical queue and takes no more intake, and the gate's
  `seed-freeze` check refuses a candidate that changes the criteria of an entry still in it. The
  eleventh run followed the old c9, added the eleven labels to `config/tillr/seed-items.json`, and
  was refused by the gate on that check alone. Its other work was not judged red.
- `d0eb7b405` repaired the seed test without touching the seed. It replaced
  `TestTheSeedFileDispatchesTheRuledItems` with two tests in `tools/tillr-drain/seed_test.go`.
  `TestTypedFrozenSeedDispatchesTheRuledItems` adds the eleven labels to a copy of the seed inside
  the test. `TestFrozenSeedRefusesUntypedOwnerCriteria` asserts that the unchanged file is refused.
- The same commit gave the stand-in `tillr-item` in `step_argv_contract_test.go` its
  `"claim_class": "verification-only"`.

So the labels belong only in the test's copy, and the frozen-seed rule stands. This item now makes
neither repair. c9 is restated below as a guard: the candidate leaves the seed file and both test
files as the trunk has them, and the three tests pass. `config/tillr/` stays in the item's paths
only because the directive is not edited here; no criterion asks for a change under it, and a
change to the seed file there is refused by the gate.

The preserved ref named above predates `d0eb7b405`. Its 27 files do not include the seed file,
`seed_test.go` or `step_argv_contract_test.go`, so the next run applies its change onto today's
trunk and keeps the trunk's version of those three files and of their delivered copies under
`yolo-packs/tillr/`. The eleventh run also found and repaired one fault in the preserved work, and
that repair was lost with its candidate. The next run makes it again, under c4: the edit fence of a
trunk fixer filed for a red tree check must not include the scripts that judge the fixer, of which
the eleventh run named `tests/pack-sync.test.py` and the structural check's script. A test asserts
that no such script is in the fixer's fence.


**Agent details:**

- **Ref:** `docs/design/landing-principles.md`, the rule that a mechanism whose cost rises with trunk activity is pointed the wrong way: the tree checks are cheap (about 0.5 s each in the fast tier), so running them on every hand commit is the right direction. `tools/tillr-drain/README.md`, the section on broken-trunk detection.
- **Where:** `tools/gate/change-gate.py` runs `tillr-gate checks --fast-tree --base --head`; the declarations are under `tools/gate/checks/`. `DefaultReplayProgram` and `DefaultRollingProgram` in `tools/tillr-drain/drain/trunk.go`; `trunkAlreadyRed` there and `gateFailureContext` in `tools/tillr-drain/drain/steps.go`; the `done` recipe in `.justfile`; the rolling tier in `tools/fork-check.py`.
- **Evidence:** 2026-11-17, a hand commit on the trunk that put a documented build directory in a tracked README passed `just done`. `python3 tools/gate/checks/scratch.py` at the trunk then exited 1 with two findings, and the drain's gate refused the next candidate on the `scratch` check. A rolling run over the range holding that commit finished green and moved its watermark.
- **Evidence:** 2026-11-17, why the candidate was not acquitted. `DefaultReplayProgram` is `python3 tools/fork-check.py --replay --only {only}`, and `{only}` is the failing check ids from the gate's verdict. The replay matches fork-check kinds, and the gate's tree-check ids are a separate set. `python3 tools/fork-check.py --replay --only scratch` in a clone of the trunk printed `no planned check matches --only` and exited 2, which the loop reads as "could not replay", so it leaves the refusal standing and never files a fixer. The acquittal step therefore exists, but only for ids that fork-check also knows. A change-gate tree check has no replay path, so the gate's refusal stands even when the trunk is the one that is red.
- **Evidence:** 2026-11-17, `just done` after the repair ran five fork-check checks (`file-size`, `tillr-backends`, `tillr-docs` and two `go-tool` suites) and none of the gate's tree checks. `tillr-gate checks --fast-tree` over the same range ran all eleven declarations and passed.
- **Evidence:** 2026-11-17, `python3 tests/observatory-dev.test.py` in a lane worktree at trunk commit `1524cddc9`, with no change applied, exited 1 after 4 min 40 s with seven failed cases, all in the supervision half. Its output held `observatory-dev: npm ci (there are no node_modules)` and then `tools/observatory/run.sh: line 174: …/tools/tillr-view/web/node_modules/.tillr-platform: No such file or directory`. `ls tools/tillr-view/web` in that worktree shows no `node_modules`; the directory is ignored by `.gitignore` and exists in the main checkout. The fourth run's gate log shows the same seven cases and the same two lines, with the check selected `because: the dev recipe, Procfile, tool declaration or supervision suite changed`.
- **Where (c8):** `write_stubs` in `tests/observatory-dev.test.py` writes the stub `npm`, whose comment reads `` `ci` is a no-op ``; the supervision cases start the recipe with the repository as its working directory. `ensure_modules` in `tools/observatory/run.sh` runs `npm ci` when the directory is missing and then writes the stamp.
- **Evidence (c8, second run):** 2026-11-17, run `run-item-shard-the-tillr-go-suites-a4`: the gate refused the candidate on `observatory-dev` after 4 min 40 s, with seven failed cases and the line `npm ci (there are no node_modules)`. That candidate changed gate code and not the dev recipe, so the failure reaches any item that changes the gate, not only items that change `.justfile`; c8's repair of the suite covers it, and no separate item is filed.
- **Evidence:** 2026-11-17, the sixth run added a loop test that acquits an item on a red tree check, marks its fixer `landed` and runs the loop again with a green gate. `go test ./... -run TestAcquittedTreeCandidateIsGatedAfterItsFixerLands` in `tools/tillr-drain` failed with `stop=queue-drained, dispatched=0, landed=0` and the item still `blocked`. Read at trunk commit `f721f4e12`: `rg -n 'StateBlocked' tools/tillr-drain/drain --glob '!*_test.go'` printed five lines: the constant and its comment, the one write in `acquitTrunkAlreadyRed`, and two reads in the planner. None of them moves an item out of that state.
- **Where (c4):** `acquitTrunkAlreadyRed` in `tools/tillr-drain/drain/trunk.go` writes the `blocked` state and the waiter. `isBlocked` and the `switch` in `decide`, in `tools/tillr-drain/drain/plan.go`, put the `blocked` rule before the `trunk-broken` rule. `mayBeARetry` in the same file is the rule that holds a second attempt for an owner. `ReadWaitingOn` in `tools/tillr-item/item/waiting.go` reads the waiter and the underlying state, and the drain already imports that package, so nothing outside the paths changes.
- **Evidence:** 2026-11-17, in a lane worktree at trunk commit `eed8cffa8` with a clean `git status`, `go test ./... -run 'TestTheSeedFileDispatchesTheRuledItems|TestHeavyKilledOwnerReapsJudgesBeforeCheckoutRecovery|TestEveryArgvTheDrainSendsIsAcceptedByTheTillrLoopAdapter'` in `tools/tillr-drain` exited 1. The seed test printed `seed entry "item-pile-507-allowlist-hunt": criterion "c1" records an owner decision but is verification-only: declare its claim class`. The argv test printed `the verdict program answered exit 4 for item-a (pass): tillr-loop verdict: criterion c1 has no valid stored claim_class`. The heavy test passed in that run and in three more runs with `-count=1 -run TestHeavyKilledOwnerReapsJudgesBeforeCheckoutRecovery`. The eighth run saw it fail once, inside the full suite, with `judge child did not start`.
- **Where (c9):** `checkCriteria` in `tools/tillr-item/item/apply.go` holds the refusal, and `ownerCriterion` in `tools/tillr-item/item/claim.go` is the pattern it matches. `rg -n 'Owner decision recorded' config/tillr/seed-items.json` printed eleven lines at `eed8cffa8`, and `rg -n 'claim_class' config/tillr/seed-items.json` printed none. `adapterStubs` in `tools/tillr-drain/drain/step_argv_contract_test.go` writes the stand-in's `show` record. `compose_verdict` in `tools/tillr-loop/tillr-loop` is the reader that refuses a missing class. The loop's own suite, `tools/tillr-loop/test.py`, already gives its fixture criteria a `claim_class`.
- **Evidence (c9, restated):** 2026-11-17, in a lane worktree at trunk commit `f19e7396c` with a clean `git status`: `rg -n '^func Test' tools/tillr-drain/seed_test.go` printed `TestTypedFrozenSeedDispatchesTheRuledItems` and `TestFrozenSeedRefusesUntypedOwnerCriteria` and no other test. `rg -c 'Owner decision recorded' config/tillr/seed-items.json` printed 11, and `rg -c claim_class config/tillr/seed-items.json` printed nothing. `rg -n claim_class tools/tillr-drain/drain/step_argv_contract_test.go` printed the stand-in's `show` record with `"claim_class":"verification-only"`. The c9 command, with those three test names, exited 0. The eleventh run's gate log for `seed-freeze` held eleven lines of the form `config/tillr/seed-items.json: item-pile-507-allowlist-hunt is a surviving entry and its criteria changed; the file is intake-frozen under D-QP1`. `git diff --name-only` from the preserved ref's merge base with the trunk, `45dd60fa0`, to the preserved ref listed none of the seed file, `seed_test.go` or `step_argv_contract_test.go`.
- **Where (c9, restated):** `tools/gate/checks/seed-freeze.py` and its declaration `seed-freeze.json` beside it hold the freeze. The comment above `TestTypedFrozenSeedDispatchesTheRuledItems` in `tools/tillr-drain/seed_test.go` says why the frozen file is not rewritten.

## Filed 2026-11-17: what two lanes showed

A drain pass the owner watched on 2026-11-17, with two lanes running, showed two ways the loop
spends runs on work that is then lost or never needed. Each item below comes from one of them.

### Prevent a settled run from discarding its uncommitted work

When a worker ends a run and its lane still holds edits that were never committed, the loop
throws them away. It settles the run, pins the lane's head, and resets the lane to the trunk. If
the worker never committed, that head is the base commit, so the pin points at nothing the
worker did, and only the report and the logs survive. The next attempt for the item, the design
seat that answers its conflict, and the owner all start without the diff. A run that cost real
money can therefore leave nothing behind but a sentence saying the work was incomplete. The
fix is on the loop's side: when it settles a run, it saves what the lane holds before it resets
the lane, on a ref that is never landed, never gated and never treated as the item's candidate,
and it tells the next run and the owner where that ref is. The worker's side of the same problem
is the queued item `item-run-commit-before-ending`; this item covers the work the worker still
leaves behind when it ends, is killed, or is orphaned.



**Agent details:**

- **Ref:** `tools/tillr-drain/README.md`, the boundary table of refs a run may move and the section on an interrupted run's pin; [the model-tiering design](../design/tillr-planning-node-model-tiering.md), which measures that runs which edited files and then ended uncommitted or were killed are $98 of $228 (43%) of Sol spend; `item-run-commit-before-ending`, the worker side.
- **Where:** `abandon` and `pin` in `tools/tillr-drain/drain/loop.go`; the reported-blocked branch there; `resetLane`; the orphan release in `tools/tillr-drain/drain/pass.go`; `landerRef` in `tools/tillr-drain/drain/boundary.go`; the conflict-fix brief in `tools/tillr-drain/drain/stage.go`; the run view in `tools/tillr-view/`.
- **Evidence:** 2026-11-17, `rg -n 'reset|clean -|checkout --|stash' tools/tillr-drain/drain/*.go`: `resetLane` runs `git checkout --force -B <lane branch> <trunk>` then `git reset --hard <trunk>`, which reverts tracked edits and does not remove untracked files, and `rg -n 'clean -' tools/tillr-drain/drain` finds no `git clean`. `abandon` pins `l.headOf(lane)` before the reset, and a lane with uncommitted edits has the base as its head.
- **Evidence:** 2026-11-17, `rg -n 'refs/tillr/runs' tools/tillr-drain/drain/stage.go`: the only brief that names a pin is the conflict-fix brief, and it names the head of a committed candidate. No brief for a later attempt, a design-seat run or a continued session names uncommitted work, and nothing carries it.
- **Evidence:** 2026-11-17, the drain operator's notes: run `run-item-conflict-transition-not-duplicate-a1` (Pi build seat, 7 min 41 s, 5,280,378 tokens, $1.02) reported blocked with about 19 files modified and nothing committed. When the loop settled it, `refs/tillr/runs/run-item-conflict-transition-not-duplicate-a1` pointed at the base commit, the lane was reset (reflog `reset: moving to HEAD`), and `git status` was empty. The diff was lost; the report and test logs remained.
- **Evidence:** 2026-11-16, `tillr view events --since 0`: seven runs of `item-obs2-stop-says-what-and-next` ($9.89) implemented the change and ended without committing; see `item-run-commit-before-ending`.
- **Evidence:** 2026-11-17, `rg -n 'update-ref -d|prune' tools/tillr-drain/drain --glob '!*_test.go'` finds no ref deletion: nothing tillr keeps under `refs/tillr/` is ever dropped today, so this item states a retention rule instead of inheriting one.

## Filed 2026-11-17: fork work on a drain with several lanes

Three items filed on 2026-11-17 from [the usage ledger's request](../pi/usage-recording-request.md)
change the Pi and pi-subagents forks. Those are the first items to fence a fork checkout at all, and
reading how the drain would run them turned up two ways they would collide with the other lanes.

### Prevent a fork item from colliding with sibling lanes

The drain can already stage an item whose change lives in a fork rather than in this repository.
When the item's fence (the set of paths its worker may change) names a lane's fork checkout,
`.forks/lanes/<lane>/<fork>`, the worker edits that checkout and the lander moves the fork's patch
branch. Two things stop such items from being safe on a drain that runs several lanes. First,
staging reads only the checkout of the lane the item was dispatched to, while the planner gives the
item whichever lane is free. An item fenced to lane 1's checkout but given lane 3 is therefore staged
as an ordinary item, with nothing in its fence that it can edit. Second, the fork procedure ends with
`bin/pi-forks export`, which moves `refs/heads/patch/<fork>` in this repository while the worker is
still running. The run boundary then convicts every other run open at that moment, because the only
other-lane patch-branch move it excuses is one the lander has marked. A moved patch branch also
reaches every host at its next launch once it is pushed, so it should move only after the gate has
built it. The three usage-recording fork items wait on this one.



**Agent details:**

- **Ref:** AGENTS.md, "Landing a lane: shared forks, verified candidates" and "Patching an upstream tool: patch series first"; [the fork guide](../pi/forks.md); the run-boundary table in [the drain README](../../tools/tillr-drain/README.md).
- **Where:** `forkTarget` and `ensureForkCheckout` in `tools/tillr-drain/drain/stage.go`; `freeLane`, `forkOf` and `forkHeldBy` in `tools/tillr-drain/drain/plan.go`; `liveForks` in `tools/tillr-drain/drain/pass.go`; `landedPatchMove` in `tools/tillr-drain/drain/boundary.go`; `PatchRef` with the patch-branch refusals in `tools/tillr-land/land/`; and the `path-missing` warning in `tools/tillr-item/item/sync.go`.
- **Evidence (what already protects):** 2026-11-17, `rg -n 'ReasonForkHeld' tools/tillr-drain/drain/plan.go`: two items that deliver the same fork never run together. The hold counts this pass's dispatches, live leases and unfinished examinations (`liveForks`), whatever lane each is on. Commit `35a4d848d` added fork staging, the boundary's excuse for a run's own patch ref, and the lander's patch-branch move with its rewind refusal.
- **Evidence (c1):** `forkTarget` keeps only fence entries under `.forks/lanes/` followed by the dispatched lane's id, and `TestForkTargetIgnoresAnotherLanesCheckout` pins that. `freeLane` returns the first free lane in id order and never reads the fence.
- **Evidence (c3):** the drain README's boundary table excuses a sibling's move of `refs/heads/patch/<fork>` only when a `refs/tillr/landed/` marker written during the bracket names it, and, as of `eadcebd7a`, lists an unassigned move of `refs/heads/patch/<fork>`, a fork export included, as Observed: `CompareRunObserved` in `tools/tillr-drain/drain/boundary.go` records it and does not attribute it to the run, so no run is convicted for it. What remains open is that nothing shows or holds an observed move before a push, which is `item-patch-branch-move-held-visible`. AGENTS.md says that `export --lane` for a patch-branch fork moves this repository's `patch/<name>`.
- **Evidence (c7):** 2026-11-17, `tillr-item sync --dry-run` printed `path-missing` for each of the three usage-recording fork items, because no lane checkout exists until staging makes one.
- **Evidence:** 2026-11-17, before the usage-recording items were filed, `rg -n 'paths="[^"]*\.forks' docs` found no item that fences a fork checkout, and commit `81243bf84` records that fork-series items had been blocked since the beginning. The drain has never run one.

## Filed 2026-11-17: follow-ups from the review of six drain fixes

An independent review of six drain fixes on 2026-11-17 found that none of them blocked a restart,
and that each left one gap that can only be closed by a further change. The four items below are
those gaps. Each one says what happens today and what the review measured.

### Prevent rolling checks from outliving the loop

Stopping the drain loop should end its rolling checks before cleanup removes the temporary
checkout where they run. Today, the loop ends the check runner but leaves some of the checks
it started alive. Cleanup then removes their checkout while they are still using it. The
runner deliberately starts checks in separate groups so it can cancel them, but the loop
bypasses that cancellation when it stops the runner. The loop needs to give the runner time
to end its checks before forcing it to exit. The replay step needs the same cleanup order.
Rolling checks are currently disabled in the running loop; this gap matters when they are
turned back on.

**Ruling, 2026-11-17 (answers the conflict the second and third runs raised):** both runs built
c1 to c4 and were refused at the gate for a reason that is not in their work, so the criteria
below stand as written and nothing in the shutdown design is reopened. Three points settle it.

- **The refusal was the trunk's.** The one failing check in both verdicts was `documents`, and
  its two findings were `link/dead-section-anchor` errors at two links elsewhere in this roadmap,
  whose target headings had been renamed without them. The roadmap already failed on the base
  both runs were staged from: `uvx vantage-check` on the copy of this file at `f1647cf10`
  reports exactly those two errors. Commit `e62113977` has since repaired both links on the
  trunk, and `uvx vantage-check docs/tillr/roadmap.md` at `b1fb121d5` reports nothing to fix.
  This ruling therefore changes no link, and the builder changes none either.
- **Start from the built work.** The third run's candidate is kept at
  `refs/tillr/runs/run-item-loop-stop-ends-rolling-checks-a3`. It holds the implementation, its
  tests, the drain README section and the pack copy, and it reported the full drain and
  `fork-check` suites passing. Carry that change onto the current base and do not write the item
  again. Take this roadmap from the trunk and not from the candidate, then make only the edit
  the brief asks of this block. Run `just pack-sync` and do not resolve a pack copy by hand. A
  file that merges without a conflict can still be wrong against the newer trunk, so the full
  suites decide and not the merge.
- **A `documents` finding outside this item's change.** If the check again fails on a line this
  item did not write, the failure is the trunk's: report `trunk-broken`, naming the finding and
  the base, and do not report done.

**Ruling, 2026-11-17 (answers the conflict the sixth run raised):** the sixth run rebuilt c1 to
c4 and stopped because the leak survives on the path the loop really uses, and closing it needs
a file outside the item's fence. The finding is correct, so the fence gains `tools/tillr-loop/`
and the item gains c5. The shutdown design in the drain and in `tools/fork-check.py` is not
reopened.

- **What the run found.** The loop does not start `tools/fork-check.py` itself when the loop
  adapter is configured. It starts `tools/tillr-loop/tillr-loop rolling` or `replay`, and
  `revision_check` in that program runs `fork-check.py` as a child with `subprocess.run` and no
  signal handling. The built change sends SIGTERM to the program the loop started, which is the
  adapter. The adapter dies at once, and the group SIGKILL that follows ends `fork-check.py`
  before it can cancel the checks it placed in other sessions. Read on 2026-11-17 at
  `a1fdff636`: `rg -n 'subprocess.run\(\["python3", "tools/fork-check.py"' tools/tillr-loop/tillr-loop`
  finds that one call, and `rg -n 'signal' tools/tillr-loop/tillr-loop` finds nothing.
- **The fix is settled: the adapter becomes the check runner.** For the `rolling` and `replay`
  steps the adapter flushes its output and replaces itself with
  `python3 tools/fork-check.py <flags>` using `os.execvp`, so the process the loop signals is
  `fork-check.py`. The argv, the working directory, the environment, the output and the exit
  status stay what they are today, and an `OSError` from the call is still refused as "cannot
  run revision check". Do not build signal forwarding in the adapter: a parent that forwards and
  waits is a second shutdown sequence to keep correct, and replacing the process needs none. The
  adapter's other steps are not touched.
- **The adapter has a committed copy.** After editing `tools/tillr-loop/tillr-loop`, run
  `python3 tools/tillr-loop/sync-adapter.py`, which rewrites the copy the drain embeds at
  `tools/tillr-drain/drain/tillr-loop.py`, and then `just pack-sync`. Run
  `python3 tools/tillr-loop/test.py` beside the drain and `fork-check` suites.
- **Start from the sixth run's work.** Its uncommitted change is kept at
  `refs/tillr/preserved/run-item-loop-stop-ends-rolling-checks-a6`, made on `a1fdff636`: the
  runner, the drain shutdown, their tests, the drain README and the pack copies, eight files.
  Its targeted tests and its c4 check passed; its full suites were interrupted and decide
  nothing. Carry those eight files forward, add the adapter change and the c5 tests, and run the
  full suites. This replaces the starting point named in the ruling above; that ruling's other
  two points still hold.

**Ruling, 2026-11-18 (answers the conflict the eighth and ninth runs raised):** both runs built
c1 to c5, saw the shutdown tests pass, and stopped without a commit because a full suite was red
for a reason outside their change. Neither failure is a design question, so the criteria, the
paths and the shutdown design stand as written. What was missing is the rule for finishing
when the base is red, and these five points are that rule.

- **The `fork-check` suite is red on the base.** Measured on 2026-11-18 in a clean worktree at
  `c15a5440e`, with none of this item's work applied: `python3 tests/fork-check.test.py` exited 1
  at the assertion `a timing declaration no longer matches a hit`, naming
  `tools/bedrock-spend/internal/cli/cli_test.go: if time.Since(started) > time.Second {`. Commit
  `e99cdf1a9` removed that line from the test and left its entry in
  `tools/gate-timing-declarations.json`. That file is outside this item's paths and stays
  outside them; the entry is the trunk's to repair.
- **How to prove the Python suite anyway.** Run `python3 tests/fork-check.test.py` once on the
  unchanged declarations file, with no limit shorter than the run, and keep the assertion it
  stops at. Remove the stale entry in the working tree only and run the suite again; that run
  is the proof of c2 and its output goes in the report. Restore the file with
  `git checkout -- tools/gate-timing-declarations.json`, check that `git status --short` does
  not list it, and only then commit. If the first run passed, because the entry was repaired on
  the run's base, skip the removal.
- **The heavy test is not this item's.** The ninth run saw
  `TestHeavyKilledOwnerReapsJudgesBeforeCheckoutRecovery/legacy` fail once inside the full drain
  suite with the owner gone before its judge child started. That test waits on a child process
  starting and has failed the same way in another item's full run while passing alone. A run
  that sees it fail in the full suite runs it alone once, with
  `go test ./drain -count=1 -run TestHeavyKilledOwnerReapsJudgesBeforeCheckoutRecovery` in
  `tools/tillr-drain`. If it passes alone, the run reports both results with the command and
  date and carries on to its commit. If it fails alone, the run stops and reports the trunk
  broken, naming the test and the base. Any other drain failure is the candidate's until a run
  on the base shows otherwise.
- **Commit, then report.** A red suite on the base is not a reason to end without a commit: a
  run with no commit has no candidate, and the base cannot be replayed against nothing. Make
  the one commit, holding this item's paths and the pack copy and nothing else, once the
  shutdown tests, the adapter suite and the drain suite are accounted for as above. Then, if the
  first `fork-check` run stopped at the stale declaration, end the report with the trunk broken,
  naming that assertion, the declarations file and the base sha. Otherwise report done.
- **Start from the ninth run's work.** It is kept at
  `refs/tillr/preserved/run-item-loop-stop-ends-rolling-checks-a9`, uncommitted work saved on
  base `3318def2f`: eleven files, which are the runner, its test, the adapter and its embedded
  copy, the drain shutdown with its tests and README, and the pack copies. It reported the
  focused shutdown tests, the adapter suite, the c4 mutation, `just pack-sync` and
  `yolo pack lint yolo-packs/tillr` passing; those are that run's report, not a check made here.
  On 2026-11-18 `git merge-tree --write-tree --merge-base 3318def2f c15a5440e <that ref>`
  printed a tree and no conflict. Carry it onto the current base and do not write c1 to c5
  again; take this roadmap from the trunk. Run `python3 tools/tillr-loop/sync-adapter.py` and
  `just pack-sync` again and do not resolve a copy by hand. A clean merge is not a verdict, so
  the suites decide. This replaces the starting point named in the ruling above.

**Outcomes:**

- End all rolling checks before removing their temporary checkout.
- Give the check runner time to cancel its children before forcing it to exit.
- Apply the same shutdown behavior to replay checks.
- Do the same when the loop reaches the check runner through its step adapter.



**Agent Details:**

- **Ref:** the review's finding M1-1, and the earlier shutdown fix in `runCheckGroup` that
  this item completes.
- **Where:** `runCheckGroup` and `endProcessGroup` in
  [`steps.go`](../../tools/tillr-drain/drain/steps.go); `_run_cancellable`,
  `_kill_process_group` and `Cancellation.cancel` in
  [`fork-check.py`](../../tools/fork-check.py); `revision_check` in
  [`tillr-loop`](../../tools/tillr-loop/tillr-loop), with its embedded copy
  `tools/tillr-drain/drain/tillr-loop.py`.
- **Verify:** For c1 and c3, use a child that calls `setsid` to create a separate session
  and assert that every process in the check's tree exits before worktree removal. For c2,
  send SIGTERM to the runner and assert its children exit; check that the loop waits briefly
  before SIGKILL. For c4, remove the SIGTERM step and confirm the c1 regression test fails.
  For c5, run the c1 and c3 tests through the adapter's `rolling` and `replay` steps, and
  confirm each fails when `revision_check` is put back to `subprocess.run`.
- **Evidence:** At 05:17 on 2026-11-17, an orphaned `go test -parallel 48` continued for
  about two minutes after a stop. That day, `rg -n 'start_new_session' tools/fork-check.py`
  found `_run_cancellable` starting each check in its own session, while
  `rg -n 'cmd.Cancel' tools/tillr-drain/drain/steps.go` found the loop's kill aimed at
  `-cmd.Process.Pid`, only the runner's process group. The shipped test passed because its
  child stayed in that group. A copy whose child called `os.setsid()` failed with
  "the rolling check's child ... outlived the stopped rolling run".

### Hold unexpected patch branch moves for owner acknowledgment

Exporting a fork's changes can move the branch that supplies the next build. The drain
intentionally lets unrelated workers continue when that happens; stopping them for ordinary
fork maintenance would create needless failures. But when a worker observes a move that is
neither its own delivery nor a recorded landing, the drain only records it in the run's log
and event history. The owner gets no notice in the waiting view, and the next push can publish
that change before the owner has reviewed it. Show these unexpected moves to the owner and
hold publication until the owner acknowledges them, without interrupting legitimate deliveries
or landings.

A patch branch reaches the remote in more than one way. The publishing step sends the branches
the repository requires, the push guard sends a branch itself when a commit being pushed names
it, and a person can push the branch directly. The first build held only the first of these, so
the guard published the very branch the publishing step had just refused. A hold that one route
ignores is not a hold. Every route therefore asks the same question before it sends a patch
branch, and when the record of observed moves cannot be read, nothing is published until it can.

**Outcomes:**

- Show unexpected patch branch moves in the owner's waiting view.
- Hold publication on every route a push can take until the owner acknowledges the move, and
  record that acknowledgment.
- Let legitimate deliveries and recorded landings proceed without an extra hold.



**Agent details:**

- **Ref:** the review's finding M2-3, and `item-drain-fork-lane-safety` c3 for who may move the branch.
- **Where:** `CompareRunObserved` in `tools/tillr-drain/drain/boundary.go`; the report at the `recorded, not attributed to the run` line in `tools/tillr-drain/drain/loop.go`; `tools/tillr-view/waiting.go`; `bin/patch-branches-publish`; in `hooks/pre-push`, the `publish_failed` fall-through after the publisher call and the `git push` of `{oid}:refs/heads/{branch}` in the per-manifest loop of `main`.
- **Verify:** c1: record an unexpected observed move and assert that `tillr view waiting` names the fork, run, and old and new commits. c2: assert that publishing refuses an unacknowledged move with the fork and acknowledgment instructions, but publishes a branch with no observation. c3: acknowledge the move and assert that the row clears, publishing succeeds, and the record identifies who acknowledged it and when. c4: assert that a lander-marked move or the run's own delivery produces neither a waiting row nor a publishing refusal. c5: in `tests/pre-push-guard.test.sh` or a test beside it, run the hook itself, not the publisher alone, for the three pushes, and read the remote's ref after each. c6: remove read permission from the store, or replace it with a file that is not a store, and run both programs.
- **Evidence:** 2026-11-17, `rg -n 'observed = append' tools/tillr-drain/drain/boundary.go` shows `compareRunRefs` feeding `observed`, not `violations`, and `rg -n 'patch/' tools/tillr-view/waiting.go` finds no waiting rule for an observation. Until this lands, before a push, check the loop log for `recorded, not attributed to the run` naming `refs/heads/patch/`.
- **Design answer, 2026-11-17, to the conflict the first build raised:** the build was right to stop. Its reviewer reproduced `hooks/pre-push` publishing a branch the publisher had just refused, and the hook was outside the item's paths. The paths now include `hooks/pre-push`, and c5 and c6 state the two rules the first criteria left open: every route is held, and an unreadable record refuses. c1 to c4 keep their meaning; c1 gains the row's controls and the uncapped hold, both of which the first build found.
- **Why the hook refuses and does not explain:** the hook's comment at the fall-through says a failed publish is "NOT a refusal yet" so that the per-manifest check can print the exact remedy. That reasoning holds for a branch that failed to push. It does not hold for a held branch, whose remedy is the acknowledgment the publisher already printed. The builder gives the publisher a way to tell the hook the two apart, such as a dedicated exit status, and keeps the existing fall-through for every other failure.
- **A ref the push carries:** the hook passes each pushed ref to the publisher as `--skip-ref` so the publisher does not move it under the push. Skipping the publish must not skip the hold question; the hook asks it for every destination under `refs/heads/patch/` on its standard input.
- [unlanded] **The first build, kept as history; the build order below starts from a later commit:** `refs/tillr/preserved/run-item-patch-branch-move-held-visible-a3` in the main checkout holds it, uncommitted work saved as one commit on base `f1647cf10`: 23 files, the drain's recording and acknowledgment command, the shared `patchmoves` reader, the view's rows, the publisher's refusal, their tests and the pack copy. On 2026-11-17 `git merge-tree --write-tree --merge-base f1647cf10 a376bfdac <that ref>` printed a tree and no conflict. Carry it forward and add what c5 and c6 require; do not write c1 to c4 again. Its report gave these as passing: `python3 tools/go-test-shards.py tools/tillr-drain -k 6` with 1536 tests, the same for `tools/tillr-view` with 435, and `python3 tests/patch-move-publish.test.py` with 5. Those are that run's report, not a check made here, and a clean merge is not a verdict: run them again on the new base.
- **Known gaps in the first build, from its own review:** store discovery uses `Path.exists()`, which reads a permission error as absence (c6); the hold check covers only the branches `required()` returns (c5); the waiting row gets item-only controls and its hint omits `--store` (c1).
- **Design answer, 2026-11-17, to the second conflict:** no design question is open. The two builds that followed the first answer each read the saved first build, changed nothing, and reported that no repository blocker was found. Neither named a criterion it could not meet, and neither disputed c1 to c6, so the criteria, the paths and the rules above stand unchanged. Reading the saved build is the first step of this work, not the work, and finding it sound is not a reason to stop: a build that ends with no commit has not attempted the item.
- [unlanded] **The saved build still applies:** on 2026-11-17, in a worktree at `123aeab1b`, `git merge-tree --write-tree --merge-base f1647cf10 123aeab1b refs/tillr/preserved/run-item-patch-branch-move-held-visible-a3` printed a tree and exited 0, and `git diff f1647cf10 <that ref> | git apply --check -3 -` exited 0 for all 23 files. The ref is a merge commit whose first parent is `f1647cf10`, so the diff against that parent is the whole first build. The same day, `rg -n 'publish_failed' hooks/pre-push` still found the fall-through, `rg -n 'def required' bin/patch-branches-publish` still found the branch list the first build's check is limited to, and `rg -n 'patch/' tools/tillr-view/waiting.go` still found nothing.
- [unlanded] **Design answer, 2026-11-18, to the third conflict:** the work is built and one line of documentation refused it. The ninth run committed all of c1 to c6 as `cd1d3979a`, kept under `refs/tillr/runs/run-item-patch-branch-move-held-visible-a9` in the main checkout as that ref's first parent: 24 files, with the shared reader, the hook's refusal, the three-route hook test, the unreadable-store test and the pack copy. The gate ran four checks on it. The hook suite and the hook installer passed, and `python3 tests/tillr-docs.test.py` failed on exactly one line: `tools/tillr-drain/README.md:1033 is an option list`. That line is the second line of the README's acknowledgment example, a shell continuation that begins with `--observation`; the documentation suite refuses any indented line that begins with two dashes, inside a code fence or not. The tenth run then ended in three seconds with no change. No criterion is disputed and none changes.
- [unlanded] **The repair, checked here:** on 2026-11-18 at `2ca3cedff`, `git diff cd1d3979a^ cd1d3979a | git apply -3 -` applied all 24 files with no conflict. With the example then written on one line, `tillr drain acknowledge-patch-move --store .tillr/state.db --observation 123 --fork pi --by owner`, in the README and in its pack copy, `python3 tests/tillr-docs.test.py` exited 0 and printed `ok   no document carries a flag table (9 documents)`. The Go suites and the hook suite were not run again here; the ninth run's report gives them as passing on its base, which is that run's report and not a check made here.
- **Build order, each step a stop only if it fails.** This replaces the earlier order: start from the ninth run's commit, which already contains the first build and everything c5 and c6 added, and do not go back to the first build's saved ref.
  1. Apply the ninth run's commit to the working tree with the `git apply` command quoted under "The repair, checked here". If the base has moved and the apply conflicts, resolve the conflicts by hand; a conflict is not a blocker.
  2. In `tools/tillr-drain/README.md`, write the acknowledgment example as one line, so that no line of any tillr document begins with whitespace followed by two dashes. Do not weaken `tests/tillr-docs.test.py`.
  3. Run `just pack-sync`, then `python3 tests/tillr-docs.test.py`, `python3 tests/patch-move-publish.test.py`, `bash tests/pre-push-guard.test.sh`, and the sharded suites for `tools/tillr-drain` and `tools/tillr-view`. Run the hook suite with a canonical `TMPDIR`, one with no symbolic link in its path: the ninth run saw five hook identity assertions fail under a non-canonical one, and that is the test's environment, not a defect to chase. Repair anything the new base broke.
  4. Make one commit holding all of it. Applying the commit and reading it is not the work: a run that ends with no commit has not attempted the item.
- **The publisher's signal to the hook is settled:** `bin/patch-branches-publish` exits 3 when it refuses for a hold or for an unreadable record, and keeps exit 1 for every other failure. On 2026-11-17 `rg -n 'return 0 if ok else 1' bin/patch-branches-publish` showed 0 and 1 as its only statuses, and its argument parser uses 2. `hooks/pre-push` refuses the whole push on 3 and keeps the existing fall-through on 1.
- **What does stop this build:** a failing test that needs a file outside the item's paths, or a criterion that two of c1 to c6 make impossible together. Report either with the test's output or the two criteria named. A report without one of those is not a conflict for the design seat.

### Hold lanes until their runs' background processes exit

This work is already built and only needs carrying onto the current trunk. Before running
any command, read this whole block to its last line: the first point under "Agent details"
names the build to carry and the one command that carries it. The earlier work that a brief's
carried facts point to is an older, smaller build that no longer fits the trunk. Three runs
tested that older build, saw it rejected, and stopped without reading past the criteria.

When a worker finishes, its background helpers can keep running. The drain loop currently
makes the lane available to the next worker even when those helpers are still alive,
leaving only a log message for the owner. If a helper keeps writing in the lane's working copy,
its changes can become part of the next item's proposed change. The next item's checks catch
the unexpected writes, but the refusal is attributed to the wrong item.

The drain loop should keep that lane unavailable until the ended run's processes leave it,
without stopping work on other lanes. The owner also needs a visible explanation of which
processes are holding the lane and how to end them. Normal worker exit should clean up
background children; when it cannot, the lane must remain unavailable rather than pass the
problem to the next item.

**Outcomes:**

- Keep the next item out of a lane while an ended run's processes still occupy it.
- Show the owner which processes hold the lane and how to end them.
- Clean up background children on normal exit, or keep the lane unavailable.



**Agent details:**

- [unlanded] **Start here, before anything else in this block or in the brief:** the work is already built as commit `ebabfe518`. Run `git cherry-pick --no-commit ebabfe518` in the lane worktree as the first command. Do not read, diff or apply `refs/tillr/preserved/run-item-lane-release-waits-for-run-children-a1`, whatever the brief's carried facts say about it: it is an older and smaller build, and three runs have stopped on it. The cherry-pick reports six conflicted files; that is expected, and the resolutions are written out at the end of this block.
- **Ref:** the review's finding M3-2, and the lane-rotation mechanism in `tools/tillr-drain/drain/lane_rotation.go`.
- **Where:** `releaseExamination` and `processIsRunFace` in `tools/tillr-drain/drain/pass.go`; `cmd.Cancel` in `tools/tillr-run/run/exec.go`; the lane rows in `tools/tillr-view/waiting.go`.
- **Verify:** Leave a process carrying the ended run's markers with its working directory under the lane directory: assert the lane is not offered until the process exits, and the waiting row names its process id and how to end it. Repeat with the process's working directory outside the lane and assert the lane is offered. Run a worker that starts a `sleep` child and exits normally: assert the child is gone or the lane is held, and record why if normal-exit cleanup is not implemented.
- **Evidence:** 2026-11-17, `rg -n 'released anyway' tools/tillr-drain/drain/pass.go` finds the loop releasing the lane with the strays left running, and `rg -n 'cmd.Cancel' tools/tillr-run/run/exec.go` finds the group kill only on cancellation.
- **Design answer, 2026-11-17, to the conflict the second and third builds raised:** the work is built, and no criterion changes. Both builds reported done with one commit, and the gate refused each on the `documents` check alone; every other check that ran passed. That check's log named two dead section links in this roadmap, in the blocks of two other items (`link/dead-section-anchor` for `#read-a-backends-remaining-quota-directly-and-keep-a-reserve` and `#run-owner-facing-work-on-lanes-outside-the-cap`), and nothing in either candidate. The links were dead at the base both builds were staged from, `f1647cf10`, because two headings had been renamed. Landing a candidate removes its item block from this file, so the check read the whole roadmap for a change that had edited no roadmap text. The trunk commit `e62113977`, "docs(tillr): repair renamed-heading links and unblock held item blocks", repaired both links: on 2026-11-17 `uvx vantage-check docs/tillr/roadmap.md` on a tree that holds it printed `✓ 1 file checked, nothing to fix`.
- **Stage the next build from a trunk that holds the link repair:** a candidate on `f1647cf10` fails the same check whatever it contains. The commit that carries this answer is on such a trunk.
- [unlanded] **Start from the third build and do not write the item again:** it is `ebabfe518`, "fix(tillr): hold lanes until ended runs leave their processes", the commit below the tip of `refs/tillr/runs/run-item-lane-release-waits-for-run-children-a3` in the main checkout. The tip above it only removes this block and is not carried. It is 29 files on base `f1647cf10`: the lane hold and its tests in the drain, the normal-exit cleanup in the run face, the waiting row in the view, both READMEs, the operating skill and the pack copy. The second build, `5dcd89ac6` below the tip of `refs/tillr/runs/run-item-lane-release-waits-for-run-children-a2`, is evidence only: the third holds the same recovered work plus repairs the second lacks.
- [unlanded] **Three files conflict with the newer trunk, and each resolution keeps both sides:** on 2026-11-17 `git merge-tree --write-tree --name-only --merge-base f1647cf10 8b22572cc ebabfe518` reported conflicts in `tools/tillr-drain/drain/definition.go`, `tools/tillr-drain/drain/lane_rotation.go` and `tools/tillr-view/waiting.go`, and in their three pack copies. The trunk added the reason `lane-holds-uncommitted-work` and a `restaged:` waiting rule on the lines where the build added `lane-run-children-live`. Keep the trunk's constant, its classification entry and its waiting rules, and add the build's beside them. Regenerate the pack copy with `just pack-sync`; do not resolve it by hand. A file that merges without a conflict can still be wrong against the newer trunk, so `python3 tools/go-test-shards.py <module> -k 6` for `tools/tillr-drain`, `tools/tillr-run` and `tools/tillr-view` decides. The third build's report gave 1538, 183 and 436 tests passing on its own base; that is its report, not a check made here.
- **What the third build added beyond c1 to c4 stays:** its review found that a lane released by lease expiry, a release with no terminal run row, and a loop that does not land each skipped the process check, and each repair has a test that failed first. They are c1 applied to every route by which a lane returns to rotation. The build also made `groupCPU` in `tools/tillr-run/run/settled.go` skip a process whose `stat` read returns "no such process", as it already skips "file not found", with a test that a permission error is still fatal. The cleanup c4 asks for makes that exit race reachable in the run suite, so the repair is this item's.
- **c4 is met by both halves:** normal exit ends the children left in the worker's process group, and a child that left the group is held by c1 when it carries the markers and works inside the lane. The READMEs record that limit, which is the record c4 asks for.
- [unlanded] **Design answer, 2026-11-17, to the conflict the sixth run raised:** no criterion changes, and the stop was not a fault in the work. That run started from `refs/tillr/preserved/run-item-lane-release-waits-for-run-children-a1`, which its brief named as earlier work, and stopped when a read-only `git apply --check` of that ref's diff rejected hunks in `tools/tillr-drain/drain/lane_rotation.go` and `tools/tillr-view/waiting.go`. That ref is the wrong start. It is the first build, saved uncommitted as `f705ad782` on base `a16948318`: 23 files, +834 -20. The third build, `ebabfe518`, is 29 files, +1288 -36 against `f1647cf10`, and holds the same work plus the repairs listed below. Where a brief's carried facts name the first build and this block names the third, this block rules: the brief's line is generated from the oldest preserved ref and is recovery evidence only.
- [unlanded] **A conflict in the three named files is the work, not a blocker:** `git apply --check` has no three-way merge, so it rejects any hunk whose context the trunk moved. Carry the build with `git cherry-pick --no-commit ebabfe518` in the lane worktree and resolve what it reports. On 2026-11-17 `git merge-tree --write-tree --name-only --merge-base f1647cf10 66f98bad3 ebabfe518` reported the same three files and their three pack copies as on the earlier trunk, and nothing else. Stop only if a file outside those six conflicts, or if a suite stays red after the resolution below and the cause is outside the item's paths.
- **The three resolutions, read from that merge on 2026-11-17, are one line or one declaration each:**
  - `tools/tillr-drain/drain/definition.go`: the map line that ends the lane reasons carries both `ReasonLaneUncommittedWork: ""` and `ReasonLaneChildren: ""`.
  - `tools/tillr-drain/drain/lane_rotation.go`: the constant block keeps `ReasonLaneUncommittedWork = "lane-holds-uncommitted-work"` with its comment and gains `ReasonLaneChildren = "lane-run-children-live"` with its comment after it.
  - `tools/tillr-view/waiting.go`: take the trunk's side of the rule table whole, which holds the `lane-holds-uncommitted-work` and `restaged:` rows, and add the build's one row, `"lane-run-children-live": {"waits on work", "lane", "wait for the named processes to exit, or end them with the recorded kill command"}`, after the `lane-holds-uncommitted-work` row. Run `gofmt` on the file; the column alignment is its to decide.
- **Then finish as one commit:** run `just pack-sync` so the three pack copies are regenerated, not resolved; run `python3 tools/go-test-shards.py <module> -k 6` for `tools/tillr-drain`, `tools/tillr-run` and `tools/tillr-view`; commit once. A test that fails because the trunk changed a neighbor of the carried code is repaired inside the item's paths and reported, not treated as a stop.
- [unlanded] **Design answer, 2026-11-18, to the conflict the eighth run raised:** no criterion changes, and no design question is open. The eighth run repeated the sixth run's stop exactly: it took the first build from `refs/tillr/preserved/run-item-lane-release-waits-for-run-children-a1`, ran a read-only `git apply --check`, saw the same two hunks rejected in `tools/tillr-drain/drain/lane_rotation.go` and `tools/tillr-view/waiting.go`, and reported blocked with no file changed and no test run. The answer to the sixth run already ruled that ref out and named `ebabfe518` as the start; the eighth run did not act on it. The instruction now opens this block as "Start here", so it is read before the brief's carried fact is followed.
- [unlanded] **The start still holds on the current trunk:** on 2026-11-18, in a worktree at `3da00ddcf`, `git cat-file -t ebabfe518` printed `commit`, and `git merge-tree --write-tree --name-only --merge-base f1647cf10 3da00ddcf ebabfe518` reported conflicts in `tools/tillr-drain/drain/definition.go`, `tools/tillr-drain/drain/lane_rotation.go`, `tools/tillr-view/waiting.go` and their three pack copies, and in nothing else: the same six files as on 2026-11-17. The same day `rg -n 'ReasonLaneUncommittedWork' tools/tillr-drain/drain/definition.go tools/tillr-drain/drain/lane_rotation.go` and `rg -n 'lane-holds-uncommitted-work' tools/tillr-view/waiting.go` still found the trunk lines the three resolutions above are written against, so those resolutions stand as written.
- [unlanded] **Design answer, 2026-11-18, to the conflict the tenth run raised:** no criterion changes, and no design question is open. The tenth run made the sixth and eighth runs' stop a third time: a read-only `git apply --check` of `refs/tillr/preserved/run-item-lane-release-waits-for-run-children-a1`, the same two files rejected, no file changed and no test run. Its stream log shows why the "Start here" point did not stop it. The tenth run read this block with `rg -A20 '^### Hold lanes' docs/tillr/roadmap.md` and the eighth with `rg -A 17` of the same pattern, and both windows end at or before the directive, above every point under "Agent details". Neither run saw the instruction it was given. The block now opens with a paragraph, inside any such window, that sends the reader to the end of the block before a command is run.
- [unlanded] **The start still holds on the current trunk:** on 2026-11-18, in a worktree at `9eac60321`, `git cat-file -t ebabfe518` printed `commit`, and `git merge-tree --write-tree --name-only --merge-base f1647cf10 9eac60321 ebabfe518` reported conflicts in the same six files and in nothing else. The same day `rg -n 'ReasonLaneUncommittedWork' tools/tillr-drain/drain/definition.go tools/tillr-drain/drain/lane_rotation.go` and `rg -n 'lane-holds-uncommitted-work' tools/tillr-view/waiting.go` still found the trunk lines, and `rg -n 'lane-run-children-live|ReasonLaneChildren' tools/` found nothing, so the three resolutions stand as written and the work is still unlanded.
- **Read the block with the file reader, not a line window:** the block is longer than its criteria. A report whose commands show the block read only through a fixed number of lines after its heading has not read the item.
- **What is and is not a stop for this item:** a rejected hunk, a failed `git apply --check`, or a merge conflict in the six named files is not one, and a report that gives any of those as its blocker is not a conflict for the design seat. The stops are the two already named: a conflict in a file outside those six, or a suite that stays red after the three resolutions with its cause outside the item's paths. Report either with the command and its output. A build that ends with no commit and neither of those has not attempted the item.

### Hold blocked lanes without stopping loop startup

When the owner expands the drain loop to use more lanes, an old checkout may still be using
one of the added lanes' branches. Today, that one unavailable lane prevents the entire loop
from starting, and restarting the loop repeats the same failure. The loop already handles
this condition when assigning work: it holds the unavailable lane and continues with the
others. Startup should do the same so one occupied branch does not stop unrelated work.
If every lane is unavailable, the loop must refuse to start and identify the checkouts
that need to be removed.

**Outcomes:**

- Start the loop with the available lanes and hold only those whose branches are in use.
- Show which checkout holds each blocked lane, and return the lane to service when it is free.
- Refuse to start when no lane is available, and name every blocking checkout.



**Agent details:**

- **Start here, before any command:** this block is long and its last six points rule. Read it to its end with the file reader, not a line window after the heading. In short: never end with `RESULT: trunk-broken` or without a commit; start from `refs/tillr/preserved/run-item-loop-start-holds-blocked-lane-a9`; add the two desk rows to `tools/tillr-view/waiting.go` when the base lacks them; commit once.
- **Ref:** the review's precaution 2; existing `lane-branch-held` condition (`ReasonLaneBranchHeld`).
- **Where:** `stageLanes`, `refuseBranchHeld`, and `branchHolder` in `tools/tillr-drain/drain/loop.go`; lane rotation in `tools/tillr-drain/drain/lane_rotation.go`; held-lane rows in `tools/tillr-view/`.
- **Verify:** c1: start with one branch held by another worktree; assert the loop runs and does not offer that lane. c2: assert `tillr view waiting` includes the held lane and its holder's worktree path. c3: remove the holder and assert the next re-check returns the lane to rotation. c4: hold every lane and assert startup refuses with a message naming each holder.
- **Evidence:** 2026-11-17, `rg -n 'refuseBranchHeld' tools/tillr-drain/drain/loop.go` finds the refusal returned from `stageLanes`, and `rg -n 'stageLanes' tools/tillr-drain/drain/loop.go` finds the call at loop start returning its error.
- **Design answer, 2026-11-17, to the conflict the second build raised:** the report found no fault in the criteria, so c1 to c4 stand as written. The second build wrote the whole item and its tests, passed its focused tests, the full view suite and an independent review, and stopped without a commit because the full drain suite failed `TestCommentPathTwice` once with `designer <nil>`. That failure was the trunk's and is repaired there, as the third point says. The builder carries the built work forward and does not write the item again.
- [unlanded] **Start from the second build:** `refs/tillr/preserved/run-item-loop-start-holds-blocked-lane-a2` in the main checkout holds it, staged work saved as one commit on base `f1647cf10`: 12 files, which are `lane_rotation.go`, `loop.go`, `pass.go`, the new `startup_held_test.go` and the repaired fixture in `worktree_registry_writers_test.go` under `tools/tillr-drain/drain/`, the new `tools/tillr-view/startup_held_test.go`, and their pack copies. Its message is a recovery record and not a commit message, so the new commit gets its own. On 2026-11-17 `git merge-tree --write-tree --name-only --merge-base f1647cf10 123aeab1b <that ref>` printed a tree and no conflict. Apply that one commit to the base, run `just pack-sync` instead of trusting the carried pack copies, and commit once. The first build, at `refs/tillr/preserved/run-item-loop-start-holds-blocked-lane-a1`, is the older and smaller one and the second already recovered its tests; do not take it.
- **A clean merge is not a verdict:** the trunk changed `lane_rotation.go`, `loop.go` and `pass.go` after `f1647cf10`, in "fix(tillr-drain): never watch a run's own lane in its boundary bracket" and "fix(tillr-drain): clear a preserved lane and keep a dirty lane from stopping the loop" among others. The second build added its own guards against watching a lane whose worktree is missing, so the two may now do the same thing twice or disagree. Where they overlap, keep the trunk's mechanism and drop the build's duplicate, provided the build's regression tests in `startup_held_test.go` still pass. The suites decide: run `go test ./drain -run 'TestLoopStartsWithABranchHeldLaneOutOfRotation|TestStartup|TestLoopRefusesStartupWhenEveryLaneBranchIsHeld|TestRegistryLockCoversDetachedReplayAndLaneStaging'` in `tools/tillr-drain` and `go test . -run '^TestBranchHeldLaneWaitingRowNamesTheHolder$'` in `tools/tillr-view`, read the output for `ok` and not `[no tests to run]`, then run `python3 tools/go-test-shards.py tools/tillr-drain -k 6` and the same for `tools/tillr-view` once each. The pass counts in the second build's report are that run's report and not a check made here.
- **The test the second build stopped on is repaired on the trunk:** `TestCommentPathTwice` failed because `retireDesigners` read a document as deleted while a landing was rewriting its file. "fix(tillr-drain): keep a designer row while a landing rewrites its document", commit `650f1f16e`, repaired that; it is not in the second build's base `f1647cf10` and is in `123aeab1b`. Measured on 2026-11-17 at `123aeab1b` on a 32-processor machine with no change applied: the compiled `drain` test binary run as `-test.run '^TestCommentPathTwice$' -test.count=6` in 24 processes at once passed all 144 runs, where the same command before the repair failed 8 of 144. The second build does not touch `designer.go`, `comments.go` or `comments_test.go`, so it cannot bring the failure back.
- **A red test in the full suite is not a reason to report blocked, and not a reason to end without a commit:** a failure in a test this change touches, or in the startup, lane rotation or held-lane code, is this item's to repair. So is `TestWaitingEveryDrainReasonHasARule`, as the answer of 2026-11-18 to the tenth run says. Any other full-suite failure is replayed with the single test run repeatedly and in parallel, once on the change and once on the base with no change applied. A test that fails on the base too is named in the report with both outputs. A test that passes every replay on both is a load-sensitive test: name it in the report with the failing output and the replay counts, run the full suite once more by itself, and commit when that run exits 0. Every one of these endings has a commit. The gate replays a red check on the base itself and files the trunk's fixer; a report footer does neither.
- [unlanded] **Design answer, 2026-11-18, to the conflict the fifth run raised:** no criterion changes, and no design question is open. The fifth run built c1 to c4, passed its focused tests, and stopped without a commit because one shell command that ran the format check, the full drain suite and the full view suite together was killed at the worker's 600-second command limit. No test failed. Measured on 2026-11-18: the fifth run's work, `refs/tillr/preserved/run-item-loop-start-holds-blocked-lane-a5` in the main checkout, merged onto `9eac60321` with `git merge-tree --write-tree --name-only --merge-base 3da00ddcf 9eac60321 <that ref>` and no conflict, and in a copy of that merged tree `python3 tools/go-test-shards.py tools/tillr-drain -k 6` exited 0 with `1657 tests passed` and the same command for `tools/tillr-view` exited 0 with `448 tests passed`. The two focused commands named above printed `ok` in 7.9 and 2.2 seconds.
- **The suite is slow under load, not hung:** that drain run took 7 minutes 13 seconds and the view run 33 seconds, on a 32-processor machine whose load average was between 55 and 70 because other lanes were testing. The 40 seconds in `docs/pi/tillr-go-shards-2026-11-17.md` was measured on a quiet machine. A command limit of 600 seconds leaves the drain suite under three minutes of margin on a busy machine, and none when it shares the command with anything else.
- **Run each full suite as its own command, with its own limit:** start `python3 tools/go-test-shards.py tools/tillr-drain -k 6` alone, as a background task or with a command timeout of at least 1800 seconds, with its output redirected to a file in the run's scratch directory, and read the exit status and the `tests passed` line when it ends. Do the same for `tools/tillr-view`. Run `just format --check` as a third command, so its exit status is recorded. Set `TMPDIR` to the run's scratch directory first; the fifth run's first redirect went to the filesystem root because it was unset.
- **A command the worker's own limit killed is not a verdict and not a blocker:** it says nothing about the tests. Run it again by itself with a longer limit. Report blocked on a suite only when the runner exits nonzero, and then follow the red-test rule above.
- [unlanded] **Start from the fifth run's work, which replaces the second build as the start:** `refs/tillr/preserved/run-item-loop-start-holds-blocked-lane-a5` holds the same 12 files as the second build, +646 -30, already carried to base `3da00ddcf`, so it is nearer the trunk. It is a two-parent commit whose second parent is the saved index and holds nothing, so apply its first-parent diff: `git diff <that ref>^1 <that ref> | git apply --3way` in the lane worktree. On 2026-11-18 that command with `--check` added exited 0 at `9eac60321`. Its message is a recovery record, so the new commit gets its own. Run `just pack-sync` instead of trusting the carried pack copies. Since `3da00ddcf` the trunk changed `loop.go` and `pass.go` in "fix(tillr-drain): hold the item, not the loop, when a lane's work cannot be preserved" and "fix(tillr-drain): read the second-failure routing outside the write lock"; the merge was clean and both suites passed with them, as measured above. On a later trunk the suites decide again.
- [unlanded] **Design answer, 2026-11-18, to the conflict the seventh run raised:** no criterion changes, and no design question is open. The seventh run carried the 12 files forward, regenerated the pack copies, and its report gives `1,713 tests passed` for the full drain suite and a passing `yolo pack lint yolo-packs/tillr`. It stopped without a commit because the full view suite failed one test, `TestWaitingEveryDrainReasonHasARule`, and it did not replay that test on its base. The replay shows the failure is the trunk's and not this change's.
- [unlanded] **The view suite is red on the trunk with no change applied:** measured on 2026-11-18 in a clean worktree at `9f14ee05f`, `go test . -run '^TestWaitingEveryDrainReasonHasARule$' -count=1` in `tools/tillr-view` printed `--- FAIL` with `drain exports ReasonDeskCapacity = "waits on a desk" without a waiting rule` and the same line for `ReasonDocumentDeskBusy = "waits on the document's desk"`. The trunk commit `3a77eb692`, "feat(tillr-drain): route design items to document desks", added those two reasons in `tools/tillr-drain/drain/plan.go` and gave neither a row in the rule table of `tools/tillr-view/waiting.go`. The same day `git show c15a5440e:tools/tillr-view/waiting.go | rg 'waits on a desk|waits on the document'` printed nothing, so the trunk two commits later was still red.
- **Superseded on 2026-11-18 by the answer to the tenth run, below:** the answer to the seventh run ruled that this item does not add the two desk rows and that the trunk's own fixer would. No fixer was filed, so that ruling is withdrawn and this item adds them.
- **Superseded on 2026-11-18 by the answer to the tenth run, below:** the answer to the seventh run told the builder to run `TestWaitingEveryDrainReasonHasARule` on the staged base first and to end with `RESULT: trunk-broken` and no build when it failed. The ninth and tenth runs did exactly that, and the loop counted each as a failed attempt. That instruction is withdrawn. Never end this item with `RESULT: trunk-broken`.
- [unlanded] **Superseded on 2026-11-18 by the answer to the tenth run, below:** the answer to the seventh run named `refs/tillr/preserved/run-item-loop-start-holds-blocked-lane-a7` as the start. The ninth run's ref replaces it.
- [unlanded] **Design answer, 2026-11-18, to the conflict the tenth run raised:** c1 to c4 stand as written. The fault was in this block, not in the two runs. The answer to the seventh run told the builder to end with `RESULT: trunk-broken` and no commit while `TestWaitingEveryDrainReasonHasARule` is red on the base. The ninth and tenth runs obeyed, and the loop recorded each as a run that produced no commit, which is a failed attempt, and sent the second one here. The loop does not replay a check because a report footer names it. Read at `1887afe2c`: the comment on `designUnanswered` in `tools/tillr-drain/drain/design_unanswered.go` says the rule that acted on a run's trunk claim was removed on 2026-11-17, the branch of `tools/tillr-drain/drain/loop.go` that logs `produced no commit: recording a refusal` records the refusal whatever the footer says, and the one replay left is `acquitTrunkAlreadyRed` in `tools/tillr-drain/drain/trunk.go`, which runs only on a committed candidate whose gate failed. So a run of this item that commits nothing cannot succeed, and the earlier instruction could only repeat.
- [unlanded] **No fixer is repairing the test, so this item repairs it:** on 2026-11-18 at `1887afe2c`, `go test . -run '^TestWaitingEveryDrainReasonHasARule$' -count=1` in `tools/tillr-view` still printed `--- FAIL` for `ReasonDeskCapacity` and `ReasonDocumentDeskBusy`, a day and many landings after the desk commit `3a77eb692`. The same day `tillr-view waiting -store .tillr/state.db` in the main checkout showed every held landing with `heavy proof pending; no red check recorded; waiting on no open fixer recorded`, and its only `fixer-main-` rows were six finished ones dated 2026-11-17 before 09:11. `tools/tillr-view/` is in this item's paths, and the full view suite named in this item's verification cannot pass without the two rows. They are therefore part of this change.
- **The two rows, exactly:** in the rule table of `tools/tillr-view/waiting.go`, directly after the `"no-lane"` row, add `"waits on a desk": {"waits on work", "desk", "a free desk"},` and `"waits on the document's desk": {"waits on work", "desk", "the item holding the document's desk to finish"},`, then run `gofmt -w` on the file; the column alignment is its to decide. The class and place match what `tools/tillr-drain/drain/why.go` already answers for the same two reasons, `"waits on work", "desk"`. Add nothing else to `waiting.go`. If `rg -n 'waits on a desk' tools/tillr-view/waiting.go` already finds a row on the staged base, the trunk has been repaired: add no row and go on.
- [unlanded] **Start from the ninth run's work, which replaces the seventh run's as the start:** `refs/tillr/preserved/run-item-loop-start-holds-blocked-lane-a9` in the main checkout holds the same 12 files, +646 -30, carried to base `b39510945`. Its second parent is the empty saved index, so apply the first-parent diff of the sources only and let `just pack-sync` write the pack copies: with `R` set to that ref, run `git diff "$R^1" "$R" -- tools/ | git apply --3way` in the lane worktree. On 2026-11-18 at `1887afe2c` that command applied all six source files with no conflict. It stages what it applies, and the `waiting.go` rows are edited afterward, so stage everything with `git add -A` before the commit. Its message is a recovery record, so the new commit gets its own.
- [unlanded] **Measured with that start and the two rows, on 2026-11-18 at `1887afe2c`:** `go test . -run '^(TestWaitingEveryDrainReasonHasARule|TestBranchHeldLaneWaitingRowNamesTheHolder)$' -count=1` in `tools/tillr-view` printed `ok`; the focused drain command named above printed `ok` for `./drain` in 26 seconds; `python3 tools/go-test-shards.py tools/tillr-view -k 6` exited 0 with `453 tests passed`. `python3 tools/go-test-shards.py tools/tillr-drain -k 6` was run twice. The first run exited 1 with two failures, `TestASettledLaneTakesTheThirdItemBeforeItsSiblingEnds` in `tools/tillr-drain/pass_test.go`, which waits at most 10 seconds of wall-clock time, and `TestWritingSessionAnswersFirstComment` in `tools/tillr-drain/drain/design_desk_test.go`, while the machine's load average was between 85 and 97. The second run, by itself, exited 0 with `1720 tests passed` at a load average near 72. Each of the two tests was then replayed from a compiled test binary as `-test.count=4` in 12 processes at once, on the change and on the base with no change applied: all 48 runs passed on each side for each test. Neither failure was reproduced, so neither is shown to be this change's or the trunk's; both tests are load-sensitive and stay a defect to track outside this item. A builder who sees either follows the red-test rule above: replay, report, run the suite again by itself, and commit on exit 0.
- **Then finish as one commit:** run `just pack-sync`, the two focused commands, `just format --check`, and each full suite as its own command with a limit of at least 1800 seconds, then commit once. The commit holds the 12 files plus `tools/tillr-view/waiting.go` and its pack copy when the rows were added. The stops for this item are two: the apply reports a conflict in a file outside the six source files, or a test in the startup, lane rotation or held-lane code stays red after a repair attempt. Report either with the command and its output and `RESULT: blocked`. A red `TestWaitingEveryDrainReasonHasARule` is not a stop, and a run that ends with no commit and neither of those stops has not attempted the item.

### Add the failing check's own sources to a trunk fixer's fence

When the trunk goes red, the drain files one fixer item, fenced to the paths it may edit. The fence
is meant to cover the breakage. On 2026-11-17 it did not. The failing check was the whole test
suite of `tillr-item`, and the cause was a test in that package whose starting assumption an
earlier change had made false. The fixer's fence listed the files that the red range had changed,
but the stale test file had not been changed by any commit in the range, so it sat outside the
fence. Each fixer found the cause, reported that the fix lay outside its fence, and stopped. That
report left the item waiting on the owner, so the queue stayed held, and the next trunk tip filed a
new fixer with the same fence. Six fixers in a row, about $0.20 each, could not succeed, and 43
items waited behind them. A fixer must be able to touch the thing its check runs, and one that
finds it cannot must widen or escalate once rather than repeat.



**Agent details:**

- **Ref:** the fixer description at the top of `tools/tillr-drain/drain/trunk.go`, and the rolling-run branch that files it.
- **Where:** the rolling red branch in `tools/tillr-drain/drain/trunk.go` (`suspect`, `watch`, `fileTrunkFixer`, `trunkFixerPaths`); `readSpansWithStatus` in `tools/tillr-drain/drain/gate_spans.go`, which takes a failing check's watch set from the `writes` attribute of its span; `conflictDecision` in `tools/tillr-drain/drain/blocked.go`.
- **Evidence:** 2026-11-17, `go test -run TestSyncDryRunFreshStore ./...` in `tools/tillr-item` failed because change `b0c26afac` made store init create every item table, and landed fix `b87a2a97e` edited only `tools/tillr-item/sync_test.go` and its pack mirror.
- **Evidence:** 2026-11-17, the keeper log's six `filed fixer-main-` lines, one per trunk tip, each listed `tools/tillr-item/` sources changed in the range and `tools/tillr-store/main.go`, and none listed `tools/tillr-item/sync_test.go`.
- **Design answer, 2026-11-17, to the conflict two failed attempts raised:** no report found a fault in the criteria, so c1 to c6 stand as written. The item was built whole twice and passed its gate twice. Both landings were refused only because the trunk had moved under files the candidate also changes, and the other stops were tests that were red on the trunk at the time and are green now. The builder therefore carries the built candidate forward, as the points below say, and does not write the item again.
- [unlanded] **Start from the built candidate:** commit `98519384a`, the parent of the tip of `refs/tillr/runs/run-item-trunk-fixer-fence-covers-failing-check-a2-cfix`, holds the implementation, its tests, the drain README section and the pack copy: 52 files, all under `tools/tillr-drain/`, `tools/tillr-view/` and their pack copies. Apply that one commit to the base; do not take the ref's tip `f116702ed`, which only retires this block. On 2026-11-17 `git merge-tree --write-tree --merge-base 98519384a^ da13f0a15 98519384a` reported one content conflict, in `tools/tillr-drain/drain/why.go`, and the same conflict in its pack copy, which `just pack-sync` regenerates and nobody resolves by hand. The conflict is two independent blocks added at the same place in the loop that builds the rows of the why answer: the trunk's block that reports whether a held candidate's lane is kept or released, from `f8bc2770a`, and the candidate's block that adds the fixer's wait detail to a row held with `ReasonTrunkBroken`. Both stay, each a complete `if` statement of its own, the trunk's first. If the base has moved past `da13f0a15`, run the same command against it and resolve any further conflict the same way: keep what the trunk added and re-add the candidate's change beside it.
- **A clean merge is not a verdict:** `tools/tillr-drain/README.md` and `tools/tillr-view/waiting.go` changed on the trunk after the candidate's base `c3a3a35a9` and merged without a conflict, and `da13f0a15` changed how the green commit is read, which the candidate's view fixtures work around by selecting the `lane_base: tip` policy. The suites decide and not the merge. After resolving, run the candidate's own tests (`go test ./drain -run 'TestFixer|TestHeavyProcessDisappeared|Legacy.*Watches'` in `tools/tillr-drain` and `go test . -run 'TestWaitingHeldQueueNamesFixerCheckAndOwner|TestWaitingBrokenTrunkNamesTheFixer'` in `tools/tillr-view`), read the output for `ok` and not `[no tests to run]`, then run both full suites once, `just pack-sync`, and commit once.
- **Check the trunk before building:** three earlier stops were tests red on the trunk and outside this item. Run on 2026-11-17 at `da13f0a15`, all three are green: `go test . -run '^(TestTypedFrozenSeedDispatchesTheRuledItems|TestFrozenSeedRefusesUntypedOwnerCriteria)$' -v` in `tools/tillr-drain` printed `--- PASS` for both, which replace the seed test the first attempt stopped on; `go test . -run '^TestWaitingDefaultStoppedRunKeepsPlannerAnswer$' -v` in `tools/tillr-view` printed `--- PASS`; and `go test ./drain -run '^(TestHeavySuccessfulDeclarationEndsBackgroundChildren|TestHeavyCancellationEndsDeclarationChildrenBeforeRemovingTree)$' -count=5` printed `ok`. Run the first two on the staged base before any other work, and report the trunk as broken with the output if either fails. The third is a flake, and five green runs do not clear it: the cause the last attempt found is that `tools/tillr-drain/drain/heavy_tree_linux.go` compares an error from `/proc` to `unix.ESRCH` with `==`, so a wrapped "no such process" from a process that has just exited is treated as a failure. The candidate already carries that repair and its test, `TestHeavyProcessDisappearedHandlesWrappedProcErrors`, and both are inside this item's paths. Keep them in the commit; a heavy supervision failure during the full suite is this item's to repair and not a reason to stop.
- **Evidence:** 2026-11-17, `rg -n 'suspect = l.rangePaths' tools/tillr-drain/drain/trunk.go` finds the range paths used only when the failing check names no paths, so a check that names any path gets only those.

## Filed 2026-11-17: the run record's spend, corrected before it is compared

Principle: a routing decision is a comparison of what runs cost, so the record of what they cost
has to be right before anything is moved to a cheaper model. Reading the loop's history for
[the model-tiering design](../design/tillr-planning-node-model-tiering.md) on 2026-11-17 turned up
two faults in that record. Neither waits on any of that design's questions.

### Hold the copied usage of 49 old runs out of spend totals

Every cost figure the owner sees comes from the run records the store keeps, and 49 of those
records describe somebody else's run. Each of the 49 carries exactly the tokens and the cost of an
earlier run, $133.65 in all, out of $455.56 recorded. Most of them were attempts that were killed in
their first one or two seconds and never reached a model. The cause is already fixed: since
2026-11-15 each attempt reads its spend from a report it wrote itself, so a killed attempt can no
longer pick up the report of the attempt before it, and none of the runs started since then repeats
another run's usage. But the 49 old rows still say what they said. The observatory's performance
totals, each item's spend, and every rate anyone computes from the store therefore count that money
twice. This item adds a way to mark a run's usage as copied, with the old values kept in the event
log, a detector that finds such runs, and the operator's step that applies the correction to this
repository's store. The detector stays as a check, so a new leak of the same kind shows at once.



**Agent details:**

- **Ref:** [the model-tiering design](../design/tillr-planning-node-model-tiering.md), Section 1.1, "Where the money went"; the "Usage attribution" section of [the run program's README](../../tools/tillr-run/README.md), which records the fix.
- **Where:** `readSpend` in `tools/tillr-run/run/exec.go` (fixed, leave it); the run row's `usage_tokens` and `usage_cost_usd` in `tools/tillr-run/run/apply.go`; the event kinds in `tools/tillr-run/run/schema.go`, where `run.usage` already records a run whose spend could not be read; the sums in `tools/tillr-view/performance.go` and the run rows in `tools/tillr-view/serveread.go`.
- **Evidence:** 2026-11-17, `tillr view events --since 0` read at event 5494: 375 runs, of which 49 carry exactly the tokens and cost of an earlier run, $133.65 in all. All 49 started between 2026-11-15 22:19 UTC and 2026-11-16 00:58 UTC. Twenty-one of them are attempts a4 to a24 of `item-land-retires-block`, each recording 16,061,350 tokens and $3.134482, most of them ending after one or two seconds.
- **Evidence:** 2026-11-17, the same read: 285 runs started after 2026-11-16 01:18 UTC, the time of commit `263173aed`, which gave each spend extraction a fresh directory, and none of them repeats another run's usage. `git merge-base --is-ancestor 263173aed HEAD` succeeds.

### Isolate why long runs write their whole context to the cache again

Writing context into the model's prompt cache is the most expensive kind of token a Sol run pays
for. In the Sol run logs read on 2026-11-17 it was 47% of what those runs cost, against 39% for
reading from the cache and 14% for output. A run should write each part of its context to the cache
once and read it back on every later turn. Instead, those logs wrote 50.4 million tokens to the
cache while the largest contexts of the same runs add up to 22.8 million. About 27.6 million tokens,
an estimated $69 or 26% of Sol's spend, were written more than once. Reading the logs turn by turn
shows two patterns. In some long runs, once the context passes a certain size, every later turn
reads nothing from the cache and writes the whole context again. In other turns, the part read from
the cache stops growing at a fixed size while the part written grows on every turn. Neither is the
cache expiring while the run sits idle. The cause is not known: it may be where pi places its cache
markers, a limit on the provider's side, or something early in the context that changes and
invalidates everything after it. This item finds the cause, and fixes it when the fix is small and
lies in how tillr runs pi. It is the second-largest saving that the model-tiering design measured.



**Agent details:**

- **Ref:** [the model-tiering design](../design/tillr-planning-node-model-tiering.md), Section 1.2, "Where a Sol run's money goes", and Section 1.6, lever 2; [the usage ledger's request](../pi/usage-recording-request.md), whose section 4 is a different gap (cache writes that are billed but never recorded, filed as `item-pi-fork-billing-attempts`). This item explains writes that are recorded, so do not duplicate that one.
- **Where:** the run logs the loop keeps beside each lane, which each run record's artifacts name; each assistant message's `usage` carries `input`, `cacheRead` and `cacheWrite`. The pi adapter is `tools/tillr-run/backends/pi/run-pi`. `PI_CACHE_RETENTION=long` is set for every pi session by the matt pack (commit `c5f4a82f7`).
- **Evidence:** 2026-11-17, a per-turn read of the 227 Sol logs: cache writes cost $126.06 in all. Turns after the first that read nothing from the cache and wrote more than 50k tokens cost $30.18. They occur in 13 logs, starting at a context between 63k and 264k tokens, with a median of 132k. In `run-item-obs2-shell-home-a3`, turns 89 to 108 each read 0 and wrote between 132k and 149k tokens.
- **Evidence:** 2026-11-17, the same read: turns whose cache read equals the previous turn's while they write more than 15k tokens cost $20.53. In `run-item-drain-stop-keeps-runs-a3`, turns 34 to 53 each read exactly 92,262 tokens while their writes grew from 20,862 to 48,266, and from turn 54 every turn read nothing.
- **Evidence:** 2026-11-16, the design's own read: turns that started five or more minutes after the previous one carry under 2% of the cache writes, so idle expiry is not the cause. The largest Fable run, of `item-design-gate-rework`, wrote 1.695M tokens to the cache against a largest context of 256k.

## Filed 2026-11-17: what five lanes showed

A drain pass on five lanes, watched on 2026-11-17, showed two defects that cost lane time or put
the queue's own ranking method at risk. Neither stops the drain, so each goes through the queue.

### Prevent a settled worker that never exits from keeping its lane

A worker can finish its work, commit it and write its report, and still not end. The run face
waits only for the harness process to exit, so a harness that is finished but idle keeps its lane
for as long as it stays alive. On 2026-11-17 this held lane-4 for 54 minutes: the loop could not
examine the candidate, no other item could use the lane, and nothing in the run's record said
why. The stream already shows when a session has settled, and the report already ends in a
`RESULT:` footer, so the run face has what it needs to tell a finished harness that lingers from
one that is still working. It must end only the harness, never the lane's other processes, and it
must never end a harness that has not settled, because a stall in unsettled work is a different
problem that a stall watchdog owns. The Pi adapter in print mode is worth fixing at its own end
too, so that it exits once its session settles.



**Agent details:**

- **Ref:** `tools/tillr-run/README.md`, the run face's exit handling and its pause face; `item-run-commit-before-ending`, which changes how a run that ends without a candidate is settled in the same exit handling, so this item waits for it; `item-drain-hold-repeatedly-orphaned`, whose run is the evidence.
- **Where:** `Exec` and `waitForPause` in `tools/tillr-run/run/exec.go` and `tools/tillr-run/run/pause.go` (the wait that returns only when `cmd.Wait` does, and the process group the child leads); the end causes in `tools/tillr-run/run/schema.go`; `ClassifyAttempt` and `ReportFooter` in `tools/tillr-drain/drain/failover.go`; the stream's `agent_settled` event, which `tools/tillr-view/transcript_test.go` shows in a transcript; the Pi adapter `tools/tillr-run/backends/pi/run-pi`, which ends in `exec pi --print --mode json` so that the harness is the run's process, and the Pi fork's patch series under `yolo-packs/pi-fork/patches` if the exit belongs in print mode itself.
- **Evidence:** 2026-11-17, lane-4, run `run-item-drain-hold-repeatedly-orphaned-a3-cfix`: the stream wrote `agent_settled` and the report ended `RESULT: done` at 05:20:48Z, and the change was committed. The `pi` process did not exit, and for 54 minutes the run never finished, the loop could not examine the candidate, and the lane was occupied.
- **Evidence:** 2026-11-17, the same run: `pi` was idle, its CPU time did not advance, and its only children were two idle MCP helper servers. Ending the helpers did not make it exit. A SIGTERM to `pi` alone at 06:14:55Z ended it with exit 143, the run was recorded `normal-exit` with code 143, and the loop then judged the candidate and the gate passed.
- **Evidence:** 2026-11-17, `rg -n 'cmd.Wait' tools/tillr-run/run/pause.go`: `waitForPause` waits only on the harness's exit, so nothing in the run face ends a harness that has settled. `rg -n 'ReportFooter' tools/tillr-drain/drain/failover.go`: `ClassifyAttempt` judges an exited run whose log carries the `RESULT:` footer even when the exit code is not zero, except 124 and 2, which is why the candidate was judged once the process was ended by hand.

### Prevent a rank-only seed from clearing an item's workflow

`tillr item seed` is today the only command that writes a queue rank. When it re-ranks an item
that already exists, it reads the stored item, swaps in the new rank and writes the item back. The
read does not include the item's workflow, so the write sets the workflow to empty. No live item
has a workflow yet, so the 92 items ranked on 2026-11-17 came to no harm, but the method becomes
unsafe as soon as workflows are in use. A rank change must change the rank and the bookkeeping
fields and nothing else, for every item, and the ranking procedure must not need a rule that
says to skip items with a workflow.



**Agent details:**

- **Ref:** `item-seed-retire` in [the convergence design](../design/tillr-convergence.md#retire-the-seed-file-and-its-command) and `item-oa-move-to-top` in [the observatory actions design](../design/observatory-actions.md) rework how a rank is written, and both are queued. This item is small and should land first, so the ranking method is safe for every item until they land; both wait for it.
- **Where:** `storedItem` in `tools/tillr-item/item/seed.go`, whose `SELECT` lists the columns it reads and omits `workflow`; `planSeed`, which builds the re-rank payload from that stored item (`rerank := held`) and so writes whatever `storedItem` left empty; `sameDefinition` in the same file, which compares only definition fields and is not the cause. The itemPayload type, and the apply path that writes it, carry the workflow field.
- **Evidence:** 2026-11-17, on a throwaway store: an item with `workflow` `scout-then-build@v1` came back with an empty workflow after a rank-only `tillr item seed`.
- **Evidence:** 2026-11-17, `rg -n 'workflow' tools/tillr-item/item/seed.go` finds nothing, and `storedItem` there reads `item_id, title, body_ref, state, design_doc, allowed_paths, rank, seat, prose` from `items`, so `planSeed` re-applies an item with no workflow. `Workflow` is a field of the payload in `tools/tillr-item/item/apply.go`, which the apply path writes.
- **Evidence:** 2026-11-17, the day's ranking run re-ranked 92 items one seed each and skipped none for carrying a workflow, so no live item has one yet and nothing was cleared.
- **Verification scope:** run the Go suite from `tools/tillr-item` only. The pack copy is verified by `just pack-sync` and `python3 tests/pack-sync.test.py` only. Do not run `go test` inside the pack copy, and tell a reviewer not to. On 2026-11-17 at `37dd548ef`, before any change, `go test ./item/ -run '^TestSyncOverThisRepositorysDocuments$'` in `yolo-packs/tillr/files/local/share/tillr/tillr-item` already fails with "--root ../../.. is the subdirectory yolo-packs/tillr/files/local/share/ of a repository, not its top level", and the same command in `tools/tillr-item` passes: the test's `--root ../../..` resolves to the repository's top level only from `tools/tillr-item/item`. That failure is not this item's, is not a stop condition for it, and is not repaired here. A review finding that reports it does not hold the commit.
- [unlanded] **Start from the built candidate:** commit `ffd6655a2`, the tip of `refs/tillr/preserved/run-item-seed-rank-keeps-workflow-a1`, holds the fix, its test, both documents and the pack copy: 7 files on top of `064bf3e8a`. The first run stopped before committing only because its reviewer ran the pack copy's suite and met the failure above; the review found no defect in the change, and `python3 tools/go-test-shards.py tools/tillr-item -k 6` passed 471 tests. Apply that commit's change to the base and do not rebuild it from nothing. On 2026-11-17 `git merge-tree --write-tree --merge-base 064bf3e8a 37dd548ef ffd6655a2` reported no conflict. If the base has moved, run the same command against it; run `just pack-sync` after applying so that the pack copy is regenerated and not resolved by hand. Write a new commit message: the preserved commit's message is a recovery record that names the queue item, which a commit on the lane's branch must not. Then run c1 to c5's test, the full `tools/tillr-item` suite and the two pack checks above, and commit once.

## Filed 2026-11-17: a landing that changed its own settlement

A landing that changed what the verdict step reads from `tillr item show` could not be settled by
the loop that landed it, and the loop stopped on every start until its binaries were rebuilt by
hand. The rebuild was the fix for that night; this item removes the skew that made it necessary.

### Prevent the loop's step programs from running ahead of its binaries

The loop's gate, land and verdict steps run `tools/tillr-loop/tillr-loop` from the working tree,
so a landing changes them at once. The Go programs those steps call, such as `tillr-item`, stay at
the build the loop started from until someone rebuilds them. A landing that changes the contract
between the two therefore meets an old binary at its own settlement. On 2026-11-17 commit
`bb6e286b5` taught the verdict step to require each criterion's `claim_class` from
`tillr item show`, and the `tillr-item` beside the loop was built from an older commit that
prints no such field. The verdict step refused with exit 4, which the drain's contract treats as
a failed step that stops the loop, and each restart settled the same landing and stopped again.
The refusal also blamed the store ("has no valid stored claim_class") when the store was fine and
the binary was old. The step programs and the binaries should change together, and a field the
adapter needs but the binary does not print should be refused as a build mismatch.



**Agent details:**

- **Ref:** the verdict row of the exit-code table in `tools/tillr-loop/README.md`, which says a non-zero verdict stops the loop because a landed candidate whose item was not retired is landed work the board cannot see; that rule stands, and this item removes the skew that tripped it. `item-land-then-roll-and-push` in [the restart design](../design/tillr-restart.md) rebuilds binaries after a landing, but its roll runs after the landing's verdict, so it does not close this gap by itself.
- **Where:** `compose_verdict` in `tools/tillr-loop/tillr-loop`; the `tillr-build` recipe in `.justfile`, which builds the binaries the loop runs; `Verdict` in `tools/tillr-drain/drain/steps.go`.
- **Evidence:** 2026-11-17, the loop log: every start from about 09:52Z ended with `the verdict program answered exit 4 for item-jl-criterion-claim-class (pass): tillr-loop verdict: criterion c1 has no valid stored claim_class`, with the loop's build line naming `6221148e51e1`. `go version -m` on the installed `tillr-item` showed `vcs.revision=6221148e5`, and its `show` printed criteria with `criterion_id`, `text` and `state` only.
- **Evidence:** 2026-11-17, after `just tillr-build` at `bb6e286b5` and a relaunch at 09:59:35Z, the loop's first writable open added the column, `tillr item show` printed `claim_class` `verification-only` for every criterion of `item-gate-red-cap` and `item-def-item-lifecycle`, and the loop logged `landed item-jl-criterion-claim-class` with every criterion `done`.
- **Design ruling, 2026-11-17, after two attempts:** both attempts built the same mechanism and it stands. The drain carries a committed copy of `tools/tillr-loop/tillr-loop` inside its own module, compiled in with `go:embed`, and runs that copy for the gate, fixer gate, land and verdict steps. Go cannot embed a file outside a module, which is why it is a copy and not a reference. The four rulings below settle what the attempts left open; nothing else about the mechanism is to be redesigned.
  - **Which commands run the build's copy.** A configured step program whose script argument resolves to `tools/tillr-loop/tillr-loop` under the loop's `--root` is the standard adapter and runs from the build's copy; this covers the keeper's `python3 <root>/tools/tillr-loop/tillr-loop <face>` and the default fixer gate. Any other step program runs exactly as configured. Startup prints one line per step saying which of the two it is, so the substitution is never silent, and the drain passes its own build stamp to the step in the environment so the adapter can name its build for c2.
  - **`.justfile` is not edited.** The second attempt regenerated the copy inside the `tillr-build` recipe, and while it ran the trunk changed that recipe to build only the commit `refs/tillr/green` names, in a separate clone. The landing then stopped on `CONFLICT (content): Merge conflict in .justfile`. A recipe that builds a committed tree has nothing to regenerate, so the copy is committed with its source and checked instead: a script under `tools/tillr-loop/` writes it, and a drift test in the drain suite and one in `tools/tillr-loop/test.py` fail when the two differ. `just pack-sync` already mirrors `tools/tillr-drain/`, so the pack receives the copy with no recipe change. Because the green build compiles the copy that green commit holds, binaries and step programs come from one commit without further work.
  - **A later adapter-only change must be able to commit the copy.** The fence derivation in `tools/tillr-drain/drain/stage.go` adds the copy and its pack mirror whenever a fence reaches `tools/tillr-loop/`, with a test. This is the only staging change the item makes.
  - **The pack launchers stay as they are.** Both attempts changed all eight `yolo-packs/tillr/programs/*/run` launchers so a launcher could report the binary it selects. Each launcher's own header says `exec` is its whole contract and that it has no wrapper behavior, and c2 as amended does not need it.
- **Evidence:** 2026-11-17, the three gate and landing results behind the ruling, read from each run's gate log. The first attempt's gate failed only `observatory-dev`, selected because `.justfile` changed: `tools/observatory/run.sh` could not write `tools/tillr-view/web/node_modules/.tillr-platform` in that lane's worktree. The second attempt's gate passed and its landing hit the `.justfile` conflict above. The conflict repair's gate passed `observatory-dev` and the gate's own suite and failed only `tillr-docs`, on `FAIL: suite left tillr-drain-bin-3215689213`. `tests/tillr-scratch.test.py` counts new entries in the shared scratch directory around a `go test` of `tests/tillr-testmain_test.go`, which creates no such directory; `tools/tillr-drain/main_test.go` creates one with that prefix for every run of the drain suite, so a drain suite running elsewhere at that moment is the likely source. No gate red in the three was traced to the adapter change. With `.justfile` untouched the gate no longer selects `observatory-dev` or its own suite for this item.
- **Not this item:** the scratch check's count of other processes' directories is a defect in `tests/`, outside these paths; report it and do not work around it. The conflict repair's full drain suite also failed once in `TestHeavyProcessJudgeIncludesDeclarationsAndLegacyAndReplaysOneCheck` with `read /proc/<pid>/stat: no such process`. `rg -n 'err == unix.ESRCH' tools/tillr-drain/drain/heavy_tree_linux.go` finds four comparisons at the base this ruling was written on, and `os.ReadFile` returns that error wrapped, so a process that exits during the scan is reported as failed supervision. That is a separate defect. If the full suite hits it, this item may carry the narrow repair (`errors.Is` at those sites, with a regression test that fails first) and must say so in its report; it does not go looking for it.

## Filed 2026-11-17: a held candidate should not hold a lane

A held candidate is a finished piece of work that is waiting on a person or on another item. Today
it also keeps the lane it ran in, even though nothing is running there. With ten lanes that is a
real cost: the lane count caps how much new work can start, and idle reserved lanes count against
it.

### Hold the candidate's ref, not its lane

When a candidate is held for a gate harness fault, a gate stall, or a repair that cannot be staged,
the drain keeps its lane reserved with no worker on it. The candidate is already pinned under
`refs/tillr/runs/<run>`, so the lane is not what keeps the work safe. On 2026-11-17 at 09:52Z,
`tillr drain why` reported `lanes 10/10 busy; running 5; no-lane=28`. Lanes 3 and 8 were idle and
reserved for two items held by a gate harness error, while 28 ready items waited with no lane to
run in. The drain should release such a lane once the pinned ref is confirmed readable, and put
the candidate back on whatever lane is free when the hold is cleared. A hold that really needs the
worktree keeps its lane, and the waiting view should say so and why.


**Ruling, 2026-11-17 (answers the second failed attempt):** three runs stopped, and none of them
found a fault in this design. The criteria c1 to c6 stand as written. What stopped the runs was
the trunk: each time the builder ran the full suites, tests that were already red at its base
failed, and the brief tells a builder to stop when that happens. This ruling settles those
failures so that a fourth run does not stop on them, and adds c7.

The first run stopped on two drain tests, `TestTheSeedFileDispatchesTheRuledItems` and
`TestEveryArgvTheDrainSendsIsAcceptedByTheTillrLoopAdapter`. Both pass on the trunk now: on
2026-11-17 at commit `964802787`, `go test ./...` in `tools/tillr-drain` passed every package.
They are no longer a reason to stop.

The second run built the whole item and passed its gate, and its landing met a merge conflict in
`tools/tillr-drain/drain/why.go` and in the pack copy of that file. The repair run resolved both
by keeping the trunk's triage explanations and next actions beside the item's released-or-kept
lane notes. That resolution is right and is kept. The repair run then stopped because the view
suite was red at its base.

The view suite is still red on the trunk, and that is the one open problem. On 2026-11-17 at
commit `964802787`, `go test ./...` in `tools/tillr-view` failed five tests:
`TestLiveWaitingKeepsConflictTransitions`, `TestEveryNonLandingOutcomeRecoversAndAgreesWithView`,
`TestBuiltServerAsksItsOwnSiblingWhateverPATHHolds`, `TestWhyAPIAndWaitingBeforeAnyPass` and
`TestWaitingDefaultStoppedRunKeepsPlannerAnswer`. All five fail the same way: the planner answers
`reading refs/tillr/green: exit status 128`. Commit `47c82b1dc` made the planner read that ref,
which records the last trunk commit the heavy checks proved, and these fixtures use a temporary
directory that is not a Git repository as the repository root. A sixth failure the repair run
saw, a missing waiting rule for `trunk-unproved`, is already fixed on the trunk.

The fault is in the fixtures, not in the planner, and the repair belongs to this item, because c4
is tested in that suite and cannot be shown green while the suite is red. A root that is not a Git
repository is a real fault, so the planner keeps refusing it; it must not answer as though no
commit had been proved. Each failing fixture makes its root a Git repository instead. This was
tried on 2026-11-17: with `git init` run on the fixture's root, `TestWhyAPIAndWaitingBeforeAnyPass`
passed with no other change. No planner code changes for this, and no test is skipped or deleted.
If one of the five still fails once its root is a repository, and for a reason outside this
item's paths, that is a new fact: the builder stops and reports it.

The earlier work is the starting point, not something to rebuild from nothing:
- [unlanded] the commit at `refs/tillr/preserved/run-item-held-candidate-frees-its-lane-a2-cfix` holds the second run's candidate `f1d8d3a3e` with the `why.go` conflict already resolved: 40 files, the drain and view changes, their tests, both READMEs and the pack copies.

The trunk has moved since that commit's base, so the builder re-applies it rather than reusing
its tree. On 2026-11-17, `git merge-tree --write-tree 964802787` against that commit reported no
conflict. After re-applying, the builder runs `just pack-sync` again, so the pack copies are
derived from the merged sources and not carried over from the old tree.


**Agent details:**

- **Ref:** `docs/design/landing-principles.md`, the rule that a mechanism whose cost rises with trunk activity points the wrong way: a reserved lane costs more the longer a hold waits.
- **Where:** `retainLane`, a local in the examination goroutine of `tools/tillr-drain/drain/pass.go`, is true for `errGateStallHeld`, `errItemFaultHeld`, `errRepairHeld`, `errRepairPending`, `errDirtyHeld` and `errTrunkRewritten`, and skips `abandon` for all six. `dirtyHold` in `drain/dirty_hold.go` reads the owner's checkout and the candidate's pinned head, not the lane, which suggests it can release too; confirm before moving it. `pinQuietly` in `drain/loop.go` pins and does not report failure, so c1 needs a pin that is read back. The summary line is built in `drain/why.go` from `LanesBusy`. The waiting rules are in `tools/tillr-view/waiting.go`.
- **Evidence:** 2026-11-17 at 09:52Z, ten lanes: `tillr drain why` printed `lanes 10/10 busy; running 5; no-lane=28`; lanes 3 and 8 held no worker and were reserved for `item-def-apply-unknown-fields` and `item-gate-pinned-fork-inputs`, both held as `gate-harness-error`.

## Filed 2026-11-17: the trunk and its proof should survive what touches them

Two follow-ups from the night of 2026-11-17. A commit that no person or lander wrote reached the
trunk with nothing in the way, and the heavy proof loses minutes of work whenever the loop restarts.

### Hold landings when a trunk commit has a foreign author

On 2026-11-17 at 08:08 -0500, commit `6f2d9d938` [unlanded] reached `main` as `fork-check test
<test@example.invalid>` with the message `initial`. A test fixture had run its `git add -A` and
`git commit` against the real repository and swept an agent's uncommitted drain work onto the
trunk. Nothing stopped it, and nothing noticed it: `tests/git-identity.test.py` fails on the commit,
but only when someone runs the gate, and the loop kept landing on top of it. The loop should read
the trunk's new commits at every tick, tell the owner when one was written by anyone but the
configured identity, and stop landing until the owner decides. Running work is not the problem and
should continue. Only new landings wait, so that more commits do not pile onto a history the owner
may want to rewrite.

The loop's check alone leaves a gap, found on the first attempt at this work on 2026-11-17. The
loop reads the trunk at the start of a tick, and the lander takes its lock later. A foreign commit
that arrives in between is not seen, and the lander publishes on top of it. So the lander makes the
same check itself, under its lock, as the last thing before it publishes. This work therefore
changes the lander as well as the loop.

The fourth run stopped because the full loop suite never finished, and the cause is now known. It
is in the suite's fixtures, not in the new check and not in the runner. On 2026-11-18 the fourth
run's work was unpacked into a scratch directory, and `go test . -count=1 -timeout 12m` in
`tools/tillr-drain` stopped at the deadline inside `TestDesignAmendmentLandsThenDispatchesOriginalSeat`.
That test's stand-in lander cherry-picks a commit written as `stub <stub@example.invalid>` onto
the fixture's trunk. The new check correctly sees a foreign commit and holds landings, the loop
has nothing else to do, and it waits for an owner who never comes. The stack at the deadline was
`Loop.Run`, `pass`, `planNothing`, `waitForTrigger`. The sharded runner sets no deadline of its
own, so the same wait showed as ninety minutes of silence. The trunk itself is sound: on the same
day `python3 tools/go-test-shards.py tools/tillr-drain -k 6 --timeout 20m` at `3318def2f` passed
all 1,705 tests in 5 minutes 9 seconds, on a machine whose load average was near 60 on 32 cores.

So the builder repairs the fixtures and leaves the hold as it is. Every fixture commit that
reaches a fixture's trunk is written by the identity the suite's git config names, the way the
real lander writes it. A commit a stand-in worker makes in its lane may stay foreign, because it
is not on the trunk. The only trunk commits left foreign are the ones a test of this item puts
there on purpose. No test is skipped or deleted, and the hold is not given a deadline or an
escape to make a fixture pass.

The tenth run made those repairs and then stopped on two things. Both are answered here.

The first was a check that is red on the trunk. `python3 tests/tillr-docs.test.py` fails because
`tools/tillr-run/README.md` does not list `TILLR_READ_ONLY_GROUP`, which
`tools/tillr-run/run/exec.go` gives to every run. On 2026-11-18 it failed the same way at
`cf602180e`, with none of this work applied. This work does not touch either file, and the README
is outside the paths this item may change. So the failure belongs to the trunk and is not a reason
to stop. The builder leaves the README and the check alone, finishes and commits, and names the
failure in its report with the command that shows it on the base.

The second was a real fault in the work, against c2. The loop recorded a foreign commit only as
the reason a ready candidate was held. With no candidate ready, nothing was recorded and the
waiting view showed nothing, which is the case that matters most: the 2026-11-17 commit sat on the
trunk while nothing was waiting to land. The finding belongs to the trunk, not to an item. The
loop records it at the tick that finds it, and the view shows it for as long as it is unanswered,
whatever the queue holds. c2 now says so.

The twelfth run repaired the c2 fault and then stopped, and the thirteenth stopped the same way,
on a drain test it said was red on the trunk: `TestHookCompletionHoldsAndNegativeAnswer/negative`.
That stop is withdrawn, for two reasons measured on 2026-11-18 at `be7807a33`. The test is no
longer red: `go test . -count=1 -run '^TestHookCompletionHoldsAndNegativeAnswer$'` in
`tools/tillr-drain` passed three times on the clean tree and twice with the kept work applied,
and `1d806c3e8` changed that test's path on the trunk after the base those runs were given. And
the kept work is otherwise finished. Applied to `be7807a33`, it left exactly two failures in the
three full suites, each with a small repair inside this item's paths, and with both repairs made
all three suites passed. The details below name the two repairs and the measurements. So what is
left is short: apply the kept work, make the two repairs, run the suites, and commit. No
criterion changes.



**Agent details:**

- **Ref:** `tests/git-identity.test.py`, the check that reports after the fact; the authorship rule in `AGENTS.md`; `hooks/README.md`, how hooks are installed.
- **Where:** `Trunk.LandingIdentity` in `tools/tillr-land/land/trunk.go`, which already reads the configured identity from scopes a lane cannot write and is the reader to reuse; the per-tick trunk reads in `tools/tillr-drain/drain/trunk.go`; the waiting rules in `tools/tillr-view/waiting.go`; `hooks/` and `bin/hooks-install`.
- **Where, for c7:** `prepareHistory` in `tools/tillr-land/land/trunk_history.go`, called from the locked preparation in `tools/tillr-land/land/landing.go`. It already compares the loop's observed trunk with the tip under the lock, but only for ancestry, and refuses with `ReasonTrunkRewritten`. The identity check belongs beside it, over the same two commits, with a separate reason. The lost-history hold in c4 is the loop's `errTrunkRewritten`.
- **Pack copy:** a change under `tools/tillr-land/`, `tools/tillr-drain/` or `tools/tillr-view/` needs `just pack-sync`, which writes each module's copy under `yolo-packs/tillr/files/local/share/tillr/`. Commit the copies with the change.
- **Earlier attempts:** the first run, on 2026-11-17, built c1 to c6 and stopped when its review found the gap c7 closes, because the lander was outside the paths it was given. The third run's work covered the loop, the lander, the view and the hook, and a loop stop cut it off. The fourth re-applied that work and stopped on the suite described above. The tenth re-applied it again, repaired the fixtures, and stopped on the red trunk check and the c2 fault described above. The twelfth added the c2 record and its tests, and it and the thirteenth stopped on the drain test described above. None landed.
- [unlanded] **Refs that hold no work:** the sixth run and its follow-up left `refs/tillr/runs/run-item-trunk-refuses-foreign-identity-commit-a6` (`d79d92268`) and the same name ending `-a6-cfix` (`2461bb433`). Each is a one-file commit titled `chore: retire landed document block` that only deletes this item's criteria from this document. Neither holds any of the implementation, neither landed, and no `refs/tillr/preserved/` ref exists for either: on 2026-11-18 `git for-each-ref refs/tillr/preserved | rg foreign-identity` in the main repository listed only the third and fourth runs. The eighth run stopped because its brief pointed it at those refs as a candidate. They are not one. Do not apply them, do not look for their preserved work, and do not stop because it is missing. If a brief's carried facts name them, this paragraph is the answer: the commit named under **Start from** below is the only starting point.
- [unlanded] **Start from** the commit at `refs/tillr/preserved/run-item-trunk-refuses-foreign-identity-commit-a13` in the main repository, `5dc0bb30f`: 62 files, holding the loop, lander, view and hook changes, their tests, the fixture repairs, the c2 record that needs no item, and the pack copies. The twelfth run's ref holds the same tree. Do not rebuild it from nothing, and do not go back to the tenth run's commit, which lacks the c2 work. Its parent is `f213b9481` and the trunk has moved, so re-apply its change instead of reusing its tree: `git diff f213b9481 5dc0bb30f | git apply --3way` in the lane, or the equivalent. On 2026-11-18 `git merge-tree --write-tree be7807a33 5dc0bb30f` reported no conflict. If the trunk has moved again and `tools/tillr-drain/main.go` conflicts, the conflicts are lists that each side extended: the import block, the `dispatch` switch, and the `faceList` constant. Keep both sides in each. Do not resolve a pack copy by hand. Run `just pack-sync` afterwards so the pack copies come from the merged sources.
- **c2 is built in the kept work:** it adds `tools/tillr-view/waiting_identity.go`, which gives an unanswered foreign commit a waiting row that needs no item, and `TestWaitingForeignIdentityWithoutQueuedItems` and `TestWaitingForeignIdentityNamesCommitAuthorAndRemedy` in `waiting_identity_test.go`, the two tests c2 asks for. The item's own hold reason, `trunk-foreign-identity`, is kept, so a held candidate still says why it waits. Do not redesign it. Both tests passed in the full view suite measured below.
- **Repair one, a fixture the trunk added after the kept work was written:** `TestDirtyExitContinuesForAsLongAsEachContinuationChangesTheWork` in `tools/tillr-drain/drain/commit_continuation_test.go` points `GIT_CONFIG_GLOBAL` at a file that does not exist and writes `user.name` and `user.email` into the fixture repository's local config. With the kept work applied it fails with `sets user.name = "tillr" in its local config, so the identity a commit carries is a value a lane can write`, which is the identity reader refusing correctly. Repair the fixture the way `newRefusalLoopFixture` in `stage_refusal_test.go` already does it: write a config file holding `[user]`, `name = tillr` and `email = tillr@example.invalid` under `t.TempDir()`, point `GIT_CONFIG_GLOBAL` at it, and delete the two `runGit(t, f.root, "config", ...)` lines. The fixture's first return value is then unused, so name it `_`. Do not loosen the reader. Measured on 2026-11-18 with that change: `go test . -count=1 -run '^TestDirtyExitContinues'` in `tools/tillr-drain/drain` printed `ok`.
- **Repair two, two waiting rules the trunk is missing:** `go test . -count=1 -run '^TestWaitingEveryDrainReasonHasARule$'` in `tools/tillr-view` fails on the clean tree at `be7807a33` with `drain exports ReasonDecisionCollision = "decision-collision" without a waiting rule` and the same line for `ReasonSettleFailed = "settle-failed"`. Both reasons are declared in `tools/tillr-drain/drain/item_fault.go` and have no row in the view. The kept work already edits the `waitingRules` table in `tools/tillr-view/waiting.go`, so this item adds the two rows there. Use what `tools/tillr-drain/drain/why.go` says for the same two reasons: class `needs a cause`, place `loop`, remedy `fix the drain defect the fault names, then tillr drain clear-fault`. The existing test is the failing reproduction, so no new test is needed. Say in the commit message body that the change also gives the two reasons their waiting rules. If the trunk has added the rows by the time this is built, the test passes and there is nothing to add.
- **Measured with the kept work and both repairs, on 2026-11-18 at `be7807a33`:** `python3 tools/go-test-shards.py tools/tillr-drain -k 6 --timeout 20m` printed `1986 tests passed`, the same command for `tools/tillr-land` printed `107 tests passed`, and for `tools/tillr-view` `490 tests passed`. `python3 tests/pre-commit-identity.test.py` ran 4 tests and printed `OK`, and `bash tests/hooks-install.test.sh` printed `all hooks-install cases pass`. Before the repairs, the drain suite's only failure was the fixture in repair one and the view suite's only failure was the test in repair two. The drain suite took four and a half minutes at a load average near 28. These ran in an unpacked copy of the merged tree, which is not a git checkout, so `just pack-sync`, `python3 tests/pack-sync.test.py`, `just format` and `python3 tests/git-identity.test.py` were not run there: run them in the lane.
- **How this item ends:** with one commit and `RESULT: done`. A full-suite failure is this item's to repair when it is a test this change touches, a fixture that commits to a trunk, or the waiting-rule test above. Any other failure is run again alone, with `go test . -count=1 -run '^TestName$'`, on the clean base and on the work. If it passes alone on the work, it was load: run the suite again and carry on. If it fails the same way on the clean base, name it in the report and commit anyway. Do not end with a `trunk` obstacle for a `go test` name: that obstacle's `CHECKS` line takes gate check ids only, the loop replays those ids and nothing else, and it answered the thirteenth run that it found none of them red. `RESULT: failed` is not an ending at all. The twelfth run ended with it, with the work staged and verified, and the loop counted a run that produced nothing.
- **The check that was red on the trunk is repaired:** `python3 tests/tillr-docs.test.py` failed at `cf602180e` on `FAIL run README environment list`. On 2026-11-18 at `be7807a33` it exited 0. Run it, and expect it to pass. This work touches none of its inputs: `git diff --name-only HEAD -- tools/tillr-run tests/tillr-docs.test.py` prints nothing.
- **Fixtures to repair:** the root package of `tools/tillr-drain` runs its tests one after another, so the first fixture that waits hides the rest; in the fourth run five of its six shards were stuck. The kept work already points the suite's git config at `Fixture Landing <landing@example.invalid>` in `runTests` in `tools/tillr-drain/main_test.go`, and the `git` helper in `pipeline_test.go` commits as that identity. What still writes to a fixture trunk as someone else: stand-in landers that cherry-pick a worker's commit unchanged, as in `testDesignAmendment` in `blocked_answer_test.go`; scripts that commit straight to the fixture root, such as the `trunk move` commit in `conflict_test.go`; and the `Fixture` base commit in `TestPlanHoldsAnItemBlockedByAnUnlandedItem` in `main_test.go`. `rg -n 'user\.(name|email)|cherry-pick' tools/tillr-drain/*_test.go` lists the candidates; read each to see whether its commit reaches the trunk.
- **Verify the suite with a deadline:** run `python3 tools/go-test-shards.py tools/tillr-drain -k 6 --timeout 20m`. The runner's default sets no deadline, so a waiting loop prints nothing; with `--timeout` a stuck shard ends with the name of the test and its stack. A stack ending in `waitForTrigger` under `planNothing` is one more fixture to repair, not a reason to stop. Repair it and run again until the suite passes. A failure that is not this wait, lies outside the item's paths and also fails on the base with this work removed is the trunk's: report it and carry on, as with the red trunk check above. Stop and report only when the failure is caused by this work and its repair lies outside the item's paths.
- **Hook limits, read 2026-11-17:** `hooks/` holds only `pre-push`, and `bin/hooks-install` copies each executable there into the shared hooks directory, so one hook covers the main checkout and every worktree. A hook runs only for `git commit`, `merge` and `am`. It does not run for `commit-tree` or `update-ref`, and `--no-verify` and a `core.hooksPath` override skip it, so the hook is a convenience and the loop's check in c1 is the guard.
- **Evidence:** [unlanded] 2026-11-17, `git log -1 --format='%an <%ae> %s' 6f2d9d938` prints `fork-check test <test@example.invalid> initial`, and `git reflog main` records it as `commit: initial` at 08:08:12. The cause was a fixture inheriting `GIT_DIR` and `GIT_WORK_TREE`, reproduced in a scratch clone.

### Prevent losing heavy proof across loop restarts

The drain's heavy proof runs the expensive checks against the whole trunk, rather than checking
one worker's change in isolation. It takes three to eight minutes. On 2026-11-17, restarting the
loop discarded progress from a proof that was still running. The new loop repeated checks that
had already passed against the same commit. Preserve those results so a restart can resume useful
work instead of paying for it again.

The owner also needs to know how recent the last successful proof is. The loop log and waiting
view show the last commit that passed, but without its proof's age an hours-old result can look
as reassuring as a fresh one. When a proof fails, the drain already searches the landings since
that successful commit to find the first one that broke the checks. Keep that behavior. If the
starting commit already fails, or the last landing passes despite the current tip failing, file
repair work that identifies the affected commit range rather than ending the run with an error.

**Outcomes:**

- Reuse checks that passed against the same commit and resume proof promptly after a restart.
- Show the owner how long ago the last successful proof finished.
- File repair work for the affected commit range when no landing explains the failure.



**Agent Details:**

- **Ref:** [Landing principles](../design/landing-principles.md), including the rule that a mechanism whose cost rises with trunk activity points the wrong way.
- **Where:** `HeavyRunner.Run` and `firstRed` in [heavy.go](../../tools/tillr-drain/drain/heavy.go) implement the search over recorded landings. `startHeavy`, `heavyAttempted`, and `heavyStatusLine` are in [heavy_loop.go](../../tools/tillr-drain/drain/heavy_loop.go). `HeavyStatus` in [status.go](../../tools/tillr-drain/heavy/status.go) carries `GreenCommit` but no proof time. The waiting view lives in `tools/tillr-view/`.
- **Verify:** For c1, restart between two checks; assert the first runs once and the log identifies reused checks. For c2, assert proof starts on the first tick after restart without a new landing, and say in the run report whether the test passed before any code change. For c3, assert both the log and waiting view include the green commit and proof age. For c4, cover both an already-failing base and a passing last landing; assert the filed fixer's text names both ends of the range.
- **Existing coverage:** [heavy_attribution_test.go](../../tools/tillr-drain/drain/heavy_attribution_test.go) covers finding the first failing landing. Do not restore the first draft's removed criterion for that behavior; c4 addresses the cases where the search cannot attribute the failure.
- **Do not edit this item block.** The loop refuses a candidate whose worker changed its own item block, and it refused the second attempt on 2026-11-17 for exactly that: the earlier wording of c2 asked the item to record a finding, and the worker wrote it here. Findings go in the run report. Other documentation the change needs, such as the [drain README](../../tools/tillr-drain/README.md), is still in scope.
- **Already held:** `maybeRolling` in [trunk.go](../../tools/tillr-drain/drain/trunk.go) calls `startHeavy` with the trunk tip on every tick under the `after-merge` landing strategy, and `startHeavy` skips only a tip the loop has already attempted or a proof still running. A new loop has attempted nothing, so its first tick starts the proof. Read at `8b22572cc`; the second and third attempts each wrote a first-tick test that passed without a code change.
- **Earlier attempts:** Seven runs built or inspected this and none landed. Three on 2026-11-17 built most of it. The third attempt's work is kept in the main repository at `refs/tillr/preserved/run-item-heavy-restart-keeps-proof-and-names-red-landing-a3`. Its independent review found the defects that the criteria above now state: retry passes on the single gate program were saved but never read back, a cancelled proof overwrote the previous green commit's proof time, a range with several landings blamed an earlier landing without checking the last one, and the waiting view showed a fault row in a repository with no green commit. Two later runs, on 2026-11-17 and 2026-11-18, read that work, changed nothing and reported failure without naming a blocker. Two more on 2026-11-18 stopped at the first step of **Start procedure** as it then read, which **Design answer, 2026-11-18** below corrects. The earlier wording said to start from the kept work and gave no way to do it. **Start procedure** below is the ruling: it is the way, and a run that inspects the kept work and stops has not attempted the item.
- **Start procedure:** Follow these steps in order. Every command was run in a lane at `2ca3cedff` on 2026-11-18, with the results stated; steps 1 and 2 were run again at `1887afe2c` the same day.
  1. Repair the waiting view's two missing rules first, as the first part of this change. In `tools/tillr-view`, run `go test . -run '^TestWaitingEveryDrainReasonHasARule$'`. At `1887afe2c` it fails on a clean tree, because `ReasonDeskCapacity` and `ReasonDocumentDeskBusy` have no waiting rule. That failure is not a reason to stop: **Design answer, 2026-11-18** below rules that this item repairs it. Add the two rows given there to `waitingRules` in [waiting.go](../../tools/tillr-view/waiting.go), run the test again and read `ok`. If the test already passes on your base, the trunk has been repaired: add nothing and continue.
  2. Apply the kept work's source changes on top of step 1, and leave out its change to this file and its pack copies. Run this from the lane's root:

     ```bash
     R=refs/tillr/preserved/run-item-heavy-restart-keeps-proof-and-names-red-landing-a3
     git diff "$R^1" "$R" -- tools/tillr-drain tools/tillr-view | git apply --index --3way
     ```

     At `2ca3cedff` this applies with no conflict and adds or changes 20 files; at `1887afe2c` the same command with `--check` in place of `--index` exited 0. `--index` stages the new Go files, which the format check needs. If a later base conflicts, resolve the conflict in favor of the trunk's code and re-make the kept change on top of it.
  3. Confirm the starting state. In `tools/tillr-drain`, `go build ./...` succeeds and `go test ./drain -run Heavy` passes. In `tools/tillr-view`, `go test . -run 'Heavy|Waiting'` fails `TestWhyAPIAndWaitingBeforeAnyPass` and `TestWaitingDefaultStoppedRunKeepsPlannerAnswer`: both show a `trunk-proof` row reading `heavy proof unavailable` in a repository that has no `refs/heads/main`. That is the fourth defect in the list above, and those two tests are its failing reproduction.
  4. Repair the four defects, one at a time, writing the failing test first for the three that have none. The tests the criteria name are the list: c1's retry pass on the single gate program read back after a restart, c3's cancelled or refused proof leaving the proof time unchanged, c3's repository with no green commit, and c4's range of more than one landing with a green last landing.
  5. Check each criterion's test against **Verify** above, and add any test the kept work lacks.
  6. Run `just pack-sync`, then the full suites: `python3 tools/go-test-shards.py tools/tillr-drain -k 6`, and the same command with `tools/tillr-view`.
  7. Make one commit that holds the two waiting rules, the source, the tests, the README change and the pack copies, and does not touch this file.
- **Design answer, 2026-11-18, to the conflict the ninth and tenth runs raised:** no criterion changes. Both runs followed the earlier first step correctly: they ran the waiting view's test on a clean base, saw it fail, and ended by reporting the trunk broken. The earlier step was the wrong instruction, because it assumed a trunk fixer would repair the test and none exists. Measured on 2026-11-18 at `1887afe2c`, the trunk's tip: the test still fails with `drain exports ReasonDeskCapacity = "waits on a desk" without a waiting rule` and the same line for `ReasonDocumentDeskBusy = "waits on the document's desk"`, and `tillr view waiting --store .tillr/state.db` in the main checkout lists six trunk fixers, all closed, and reads `waiting on no open fixer recorded` on every item held at landing. So stopping repeats without end. This item repairs the test itself.
- **Why this item may repair it:** `tools/tillr-view/` is inside this item's paths, the kept work already changes `waiting.go`, and c3's tests live in the view suite, which cannot pass in full while that test is red. No open fixer exists for it, so no fixer collides. If another change adds the rows first, step 1 finds the test green and adds nothing; if both land, keep the trunk's rows and drop these.
- **The two rows, ruled so the run does not choose:** add these to `waitingRules`, after the `"no-lane"` row. The class and the `desk` place match what `tools/tillr-drain/drain/why.go` already returns for the two reasons.

  ```go
  "waits on a desk":              {"waits on work", "desk", "a free desk"},
  "waits on the document's desk": {"waits on work", "desk", "the run holding the document's desk"},
  ```

  Measured on 2026-11-18 at `1887afe2c` with only those two rows added: `go test . -run '^TestWaitingEveryDrainReasonHasARule$'` in `tools/tillr-view` printed `ok`, and so did `go test . -run 'Waiting|Why'`. The existing test is the failing reproduction, so the repair needs no new test. The commit message says in its body that it also gives the two desk reasons their waiting rules.
- **Reporting the trunk broken:** do not end with that result for the waiting view's test; it is this item's to repair. Any other full-suite failure in a test this change does not touch is replayed on the base with no change applied, and is reported as the trunk's only when it fails there too, with the test's name and both outputs.
- **Roadmap links:** Two attempts stopped on dead section links elsewhere in this file. Those are repaired on the trunk: `uvx vantage-check docs/tillr/roadmap.md` reported nothing to fix at `8b22572cc` on 2026-11-17. A failure there is no longer expected; if one appears outside this item's change, report it and stop.
- **Evidence recorded on 2026-11-17:** The following inspections found `heavyAttempted` only in memory on `Loop`, with no stored copy, and found the existing search plus two `Externalf` returns: one for a range with no landing and one for a failing tip whose last landing passes.

```bash
rg -n 'heavyAttempted' tools/tillr-drain/drain
rg -n 'func \(r HeavyRunner\) firstRed' -A40 tools/tillr-drain/drain/heavy.go
```

## Filed 2026-11-17: refs that outlive their run

The owner reads history in `tig`, and on 2026-11-17 he said: "shouldn't the refs disappear as the
commits land on main? that's what is cluttered. let's file an item for this."

### Prevent old run labels from cluttering history

Once a change reaches the main branch, the owner wants to read its history without sorting
through labels for every attempt that led to it. History viewers display named commit pointers
as labels, and the drain loop leaves those pointers behind after the work is complete. The repository
held 736 of these pointers on 2026-11-17: 564 run pins, 153 landing markers, 8 saved edits, 7 held
candidates, 2 recovery pointers, and the fixed pointers for the green commit and the trunk
history. The drain loop should remove the pointers that finished work no longer needs, and a
one-time cleanup should clear what has built up. That makes the history easier to read without
removing the changes that landed.

Some pointers still serve a purpose. A run pin keeps a worker's proposed change available until
it lands, and a landing marker lets the lander recognize a completed landing when a crash prevents
it from saving the receipt. Cleanup must wait until the landing record is safely stored and any
checks that need the marker have finished. It must also preserve commits needed by an active
worker, a candidate held for later, or a run that can still be retried.

Two kinds of pointer are not the drain loop's to remove. An operator writes the held candidates
under `refs/tillr/held/` and the recovery pointers under `refs/tillr/recovery/` by hand, and no
tillr program writes or deletes either. The loop and the one-time cleanup therefore never delete
them: they list each one with what the store says about its item, and the operator removes it
with `git update-ref -d` when it is no longer wanted. A worker staged for this item on 2026-11-17
stopped because the earlier wording asked for a listing with no such pointers left and named no
owner for them. This paragraph and the criteria below settle that.

An operator's pointer and a run pin can name the same commit, and a second worker stopped on
2026-11-18 because the criteria then disagreed about that pin: c4 kept it for as long as the
operator's pointer existed, and c6 allowed no run pin after a drained queue. The ruling is that
the pin gets no exemption. A run pin is deleted by the state of its item alone, whatever else
names its commit. The operator's pointer is itself a Git ref, so the commit stays in the
repository through that pointer, and the operator loses nothing when the pin goes. Keeping the
pin would also have needed a second mechanism to delete it once the operator removed the
pointer, and nothing would have run it. Criteria c4, c5 and c6 below now say the same thing.

A third worker stopped on 2026-11-18 because it could not find the code that writes a landing
marker at a fork's built head, and so could not tell whether the drain loop's reader of that
marker was obsolete. The reader is live. The lander has one call that writes a marker, and it
writes whatever commit the landing publishes. For a landing onto the main branch that is the
squash commit. For a fork delivery, which moves `refs/heads/patch/<fork>`, the lander publishes
the built head itself, so the same call writes the marker at that head. The ruling on order
follows from who reads it. A marker for a landing onto the main branch is deleted in the settle
step, once the landing record is stored. A marker for a fork delivery is kept until every run
that was in flight when the delivery happened has had its boundary checked, and is deleted then.
Criterion c2 and the details below carry both halves.

A fourth worker stopped on 2026-11-18 because this block said no tillr program reads the
operator's pointers, while the drain loop's boundary check does read them. The block was wrong
and the code is right. The boundary check compares every ref under `refs/tillr/` before and
after a run, so a held or recovery pointer that appears, moves or disappears during a run is
recorded as an observation, and a test asserts that. The ruling is that this stays exactly as it
is: the item does not remove, narrow or exempt that observation, and an observed change to an
operator's pointer still never counts against the run. What the item forbids is writing or
deleting those pointers. Reading them is allowed, and the one-time cleanup must read them in
order to list them. The wording above and the details below now say so.

**Outcomes:**

- Remove obsolete run labels after recording a landing or otherwise finishing an item.
- Clear accumulated labels while preserving work that still needs to land or be retried.
- Leave pointers an operator wrote by hand in place, and report them so the operator can remove them.
- Document how long the drain loop keeps each kind of commit pointer.



**Agent details:**

- **Ref:** `tools/tillr-drain/README.md`, the boundary table of refs a run may move and the saved-edit retention rule; `tools/tillr-land/README.md`, the landing marker and the section "Known cases the marker does not settle"; [the landing principles](../design/landing-principles.md).
- **Where:** `landerRef` and the landed-marker check in `tools/tillr-drain/drain/boundary.go`, which treat `refs/tillr/runs/` and `refs/tillr/landed/` as ignored and read a moved marker as a sibling's fork-series landing; `landedMarkerRef` in `tools/tillr-land/land/trunk.go` and the retry paths in `tools/tillr-land/land/landing.go`; `prunePreservedWork` in `tools/tillr-drain/drain/preserved.go`, the one deletion that exists and the shape to follow; the settle and landing steps in `tools/tillr-drain/drain/loop.go` and `pass.go`; `yolo-packs/tillr/` copies, regenerated with `just pack-sync`.
- **Landed markers have two live readers.** The lander's retry reads the marker to recognize a landing whose receipt it did not record (`tools/tillr-land/README.md`). The drain's boundary check reads a marker written during a bracket to tell a sibling's fork-series landing from a stray move of a patch branch. Deleting a marker only after the store holds the `landed` row keeps the first. The second reads only the markers of fork deliveries, and it reads them in a sibling's bracket: the pair of ref snapshots the loop takes before and after a run and then compares. `landedDuring` collects each marker that appeared or moved between the two snapshots, and `landedPatchMove` excuses a patch branch that moved to a commit one of them names. A marker deleted before a sibling's closing snapshot would turn that sibling's view of the delivery into an escape.
- **Order of deletion.** A marker whose landing's base ref is not a patch branch is deleted in the settle step that records the landing, after the `landed` row is durable: no sibling's check needs it, because a squash commit on the trunk is never the tip of a patch branch. A marker for a fork delivery is deleted only once every run whose bracket was open when the lander moved the patch branch has been compared. A run that starts after the delivery does not need the marker, because its opening snapshot already holds the moved patch branch. Deleting fork-delivery markers only at a settle step where no other run is in flight satisfies the rule and is an accepted implementation; fork deliveries are rare, and c6 is judged on a drained queue, where no run is in flight.
- **Evidence:** 2026-11-18, at commit `9eac60321`, `rg -n 'MarkLanded|PatchRef\(req.BaseRef\) != ""' tools/tillr-land/land/landing.go` shows the one production call, `t.MarkLanded(req.RunID, p.squash.Commit)`, and above it the branch that sets `p.squash = Squash{Commit: req.HeadSHA, Tree: p.landing.GatedTree}` when the base ref is a patch branch, under the comment "Publish exactly the built head". So for a fork delivery `p.squash.Commit` is the built fork head, and the marker names it. `tools/tillr-land/README.md`, section "Fork delivery", says the same: the marker "names that same head, so sibling run boundaries recognize the delivery". `rg -n 'landedPatchMove|landedDuring' tools/tillr-drain/drain/boundary.go` shows both functions and their one caller in the run comparison.
- **Evidence:** 2026-11-17, `git for-each-ref refs/tillr | wc -l` printed 736, and `git for-each-ref refs/tillr --format='%(refname)' | awk -F/ '{print $3}' | sort | uniq -c` printed runs 564, landed 153, preserved 8, held 7, recovery 2, green 1 and trunk-history 1. About 15:00Z the same commands had printed 727.
- **Operator-owned refs.** `refs/tillr/held/` and `refs/tillr/recovery/` are written by hand and are outside this item's deletions. When to delete one is the operator's decision, so the item adds no rule that proves one safe to delete. Neither kind protects a run pin: a pin lives and dies by its item's state, and the operator's ref alone keeps its commit. Tillr may read them: the boundary check already does, and the sweep's listing must. The boundary check keeps treating a change to either as observed and never as a conviction, and this item leaves `tillrRef`, `CompareRunObserved` and the test `boundary_ordinary_git_test.go` unchanged in that respect: the test's assertion that the observations name `refs/tillr/held/operator-pin` must still pass. `refs/tillr/trunk-history/` keeps the lifetime the drain README already gives it, and this item deletes nothing there.
- **Evidence:** 2026-11-17, `rg -n 'refs/tillr/(held|recovery)' tools/tillr-drain tools/tillr-land tools/tillr --glob '!*_test.go' --glob '!*.md'` printed one line, a comment in `tools/tillr-drain/drain/boundary.go` that calls a `refs/tillr/held/` ref an operator's hand-written pin. No code names either namespace in order to write or delete it. That search does not show that nothing reads them, and an earlier version of this line wrongly said so. 2026-11-18, at commit `2bf7dc780`, `rg -n '"refs/tillr/"' tools/tillr-drain tools/tillr-land tools/tillr --glob '!*_test.go'` printed one line, `tillrRef` in `tools/tillr-drain/drain/boundary.go`, whose prefix match on `refs/tillr/` puts both namespaces among the refs the run comparison reads, and `rg -n 'held' tools/tillr-drain/drain/boundary_ordinary_git_test.go` shows the test that writes `refs/tillr/held/operator-pin` by hand during a run and asserts the boundary check's observations name it while the run stays clean. The drain README's boundary table lists an operator's `refs/tillr/held/` pin among the refs whose changes are observed.
- **Evidence:** 2026-11-17, `git for-each-ref refs/tillr/held refs/tillr/recovery --format='%(refname) %(creatordate:short)'` printed seven held refs dated 2026-11-15, each named for an item, and two recovery refs dated 2026-11-16, `needs-merge-before-identity-optimization` and `resume-before-normal-hook-commit`, named for a situation and not for an item.
- **Evidence:** 2026-11-17, `rg -n 'update-ref -d|prune' tools/tillr-drain/drain --glob '!*_test.go'` finds one ref deletion, in `prunePreservedWork`, which deletes only `refs/tillr/preserved/*` once the item is finished or removed (commit `6221148e5`). The other matches prune worktree registrations. Nothing deletes a run pin, a landing marker, a held ref or a recovery ref, and `tools/tillr-land/README.md` says so for the first two.
- **Evidence:** 2026-11-17, a loop over `git for-each-ref refs/tillr/runs` with `git merge-base --is-ancestor <sha> main` found 291 of the 564 run pins are ancestors of `main`. Ancestry undercounts, because a squash landing leaves the run commit off the trunk. Matching each run to its item in the store, 536 of the 564 belong to an item with a `landed` row in `landings`, and 544 belong to an item that has a `landed` row or is in the state `landed` or `closed`. The store holds 248 `landed` rows, 88 `squash-conflict` and 4 `refused`.

## Filed 2026-11-17: questions for the owner belong in documents

On 2026-11-17 the owner was told that five items waited on "owner conflict" questions, and he asked: "why are there questions for me that aren't just OQs in docs that I can find in vantage? why do we have multiple categories?" His standing goals are visibility first and state in documents.

### Route an owner's question from a blocked run into its document

An open question (an OQ) is a numbered entry in a design document that carries a `vantage: question` directive, and Vantage's planning index lists every one of them for the owner. The loop already holds an item on `question-open` while such a question is unanswered, and releases it when the answer lands. A worker's blocked report takes a different path when the question is the owner's: the loop records an `item.conflict` in the store and holds the item as `conflict-owner`. Nothing writes that conflict into a document, the Vantage index never lists it, and no `tillr item` face answers it. The owner can read it only by scanning the waiting view's long "why" text. So the work that waits on him sits in the channel he does not read, and the channel he does read (documents) holds the smaller share. The fix is to make an owner's question a document question, so that waiting on the owner has one form and one place to answer.

The measured gap on 2026-11-17 is large. `tillr drain why` listed six items held `conflict-owner` and two held `question-open`. Reading the six conflicts also shows that the channel is mostly the wrong one. Five were not questions at all: the lane had been staged from a base older than the trunk, the design seat found the answer already on the trunk, and it reported "needs the owner" because restaging is a ref change no run may make. Only the BMAD and Superpowers skills item put a real choice to the owner, between three delivery routes. A document question suits that one, and a staging fault should never reach the owner, so this item writes a question only when the report offers the owner a decision, and the report is otherwise left to the existing repair path.

This item builds on one prerequisite, which has since landed, and on other landed work. The prerequisite is `item-qp-one-question-reader`, filed in [the queue persistence design](../design/tillr-queue-persistence.md), which makes the loop, the view and `tillr item show` read open questions through one reader in the shared library under `tools/tillr/`. That item was not landed when this block was filed: on 2026-11-17 a worker staged for this item found the reader still inside the loop's module and `tillr item show` still using its older hand-written parser, which skips numbered entries. A numbered question written by this item would then hold the item in the loop while `tillr item show` reported no open questions. So this item was dispatched only after the prerequisite landed, which it did in commit `c6614acf3`. This item relies on that reader and does not add a second or move the existing one. It does extend that reader in place, so its paths include `tools/tillr/`: the reader as this item found it keeps each document's questions and drops the index's `ledgerIds`, and the answer sync needs them to tell a ruled question from a deleted one. `item-view-no-unclassified` is landed. It gave every held item a class and a next mover, and this item scopes itself to what is left: the summary still counts holds by reason, and a `conflict-owner` row names "the owner" with no place to answer. `item-jl-owner-criterion-hold` holds an item that owes an owner-only criterion verdict, which is a separate case from a question, and it keeps its own reason. `item-comments-design-items-at-the-desk` is about where a design session runs and is unrelated beyond sharing the document. The design on queue persistence already rules that questions are document state and never item rows (`D-UT4`), and this item applies that ruling to the one path that still breaks it.

The work is four items, split by the design seat on 2026-11-18 after builds kept stopping on it as one. This block is the first of them. It lands the work those builds saved, which already publishes the question, holds the item, releases it on a landed answer, handles the item with no document, counts owner-waiting work and migrates the recorded conflicts, and it adds the rule that a published question holds only its own item. The three blocks below it then change one thing each: the answer sync reads the committed tree, a deleted question becomes an operator repair, and a test proves that the owner-waiting rows equal the planning index. Each lands as its own commit and names the item it waits on.



**Agent details:**

- **Ref:** `tools/tillr-drain/README.md`, the sections on conflicts and on triage, whose text says a report "becomes an owner `item.conflict`" and "remains visible under `conflict-owner`"; `docs/design/tillr-queue-persistence.md`, `D-UT4` and the sections on the `question-open` hold and the Vantage index.
- **Where:** `conflictDecision` and `ReasonConflictOwner` in `tools/tillr-drain/drain/blocked.go`; the settle step that answers a design-seat conflict by amending the item's block, in the same file; `whyPresentation` in `tools/tillr-drain/drain/why.go` and the `conflict-owner` row in `tools/tillr-view/waiting.go`; the question reader, which is in `tools/tillr-drain/vantage/` until `item-qp-one-question-reader` lands and is the package `vantage` in the shared library under `tools/tillr/` afterwards, where `parseVantageIndex` in `tools/tillr/vantage/vantage.go` is the one place that reads an index document and is where the ledger ids are added; the conflict record in `tools/tillr-item/item/conflict.go`; `yolo-packs/tillr/` copies, regenerated with `just pack-sync`.
- **The dated evidence below was written before the split** and numbers the criteria as this block then had them, one to seven. The old first, fourth, fifth and sixth are this item's first, third, fifth and fourth. The old second is this item's second together with `item-owner-answer-from-committed-tree` and `item-owner-question-removed-hold`, and the old third and seventh are `item-owner-waiting-equals-index`. Where that evidence says the item holds a deleted question `question-removed` or reads the ledger ids, the ruling stands and the later item builds it.
- **How a conflict reaches the owner today, read 2026-11-17:** a worker's blocked report raises a conflict waiting on the design seat; a design run reads it and, when it cannot amend the block, reports back, and the conflict moves to status `owner` with the design run's report appended as evidence. The design seat's own answer lands in the item's block. `tillr item --help` lists `decision`, `mail` and `comments`, and none of them answers a conflict. `rg -n '"answered"' tools --glob '!*_test.go'` finds the answered status set only by the design-seat settle, never by an owner action.
- **Evidence, 2026-11-17 about 16:00Z:** `tillr drain why --store .tillr/state.db --root . --policy config/tillr/backend-policy.bedrock.json --lanes lane-1,lane-2,lane-3,lane-4,lane-5,lane-6,lane-7,lane-8,lane-9,lane-10 | rg 'conflict-owner|question-open'` printed six items held `conflict-owner` (`item-run-refs-dropped-once-landed`, `item-oa-place-numbers`, `item-oa-from-mark`, `item-run-records-progress-and-helpers`, `item-agent-default-bmad-superpowers-skills`, `item-drain-express-lanes`) and two held `question-open` (`item-bedrock-claude-latest-only`, `item-design-autoqa-slices`). Its summary line read `held [blocked=20, ... conflict-design=2, conflict-owner=6, ... question-open=2]`.
- **Evidence, the stale-base cases:** the design runs for `item-oa-place-numbers`, `item-oa-from-mark`, `item-run-records-progress-and-helpers` and `item-drain-express-lanes` each reported that the trunk already held the answer and that the lane's `base_sha` was behind it. The fifth, `item-run-refs-dropped-once-landed`, stopped because its block was missing from the staged base. Those need a restage, which is outside this item.
- **Evidence, the index:** `uvx vantage-check index --format json` lists open questions per document, and none of the six conflicts appears in it.
- **The prerequisite is checked before any edit.** In the staged tree, `rg -n 'tillr-drain/vantage' tools -g '*.go'` and `rg -n 'func parseQuestions' tools/tillr-item` both return nothing once `item-qp-one-question-reader` has landed. If either prints a match, the lane was staged from a base without the prerequisite. That is a staging fault for the loop to repair by restaging, and it is not a question for the design seat or the owner. The worker reports it as such and does not move the reader itself. `tools/tillr/` is in this item's paths only so the landed reader can expose ledger ids.
- **Evidence, 2026-11-17 about 19:00Z:** a worker run staged for this item stopped before editing. `rg -n 'tillr-drain/vantage' tools/tillr-drain/drain/questions.go tools/tillr-view/waiting.go` printed one import in each file, `rg --files tools/tillr | rg '/vantage/'` printed nothing, and `rg -n 'func parseQuestions' tools/tillr-item/item/questions.go` printed the hand-written parser. The same three commands printed the same results when this block was amended later that day.
- **Evidence, 2026-11-18, the ledger ids:** a worker run built the question, the hold and the migration, then stopped on the second criterion. Its answer check treated a deleted question as answered, and it could read an index cached from an uncommitted edit. `uvx vantage-check index --format json` (version 0.12.0) on this date printed a `ledgerIds` list for each document, and 31 of 472 documents had a non-empty one: `docs/design/approver-agent.md` listed ten ids there and had no entry in `questions`. `rg -n 'ledger' tools/tillr/vantage` printed nothing, so the reader drops that list. The second criterion and the paths were amended to close both faults.
- **Any new hold reason is classified.** `TestAllDefinedHoldReasonsAreClassified` in `tools/tillr-drain` fails on a reason that has no class, and the same worker run failed it with a new `owner-answer-sync` reason. A reason this item adds gets a class and a next mover in the same change.
- **Evidence, 2026-11-18, two contradictions answered:** a worker run stopped before editing on two pairs of criteria. The second said a published question leaves the conflict open while the sixth told the migration to close it, and the earlier worker's migration had required the conflict to be `answered` or `closed` straight after publishing and printed "answered conflict". The sixth now follows the second: the migration publishes and leaves the conflict open. The third claimed every owner-waiting item is in the index while the fourth keeps a conflict that has no document. The fourth now gives that conflict to the operator, so it is not owner-waiting and the third's equality holds without it.
- **Evidence, 2026-11-18, the deleted question:** a later worker run stopped before editing on the second and third criteria. The second kept an item held with its conflict open after its question was deleted, and the third defined owner-waiting as held `question-open` and required equality with the index's open questions, which cannot list a deleted one. The block did not say which hold the deleted case takes. It is now ruled an operator repair: the item is held `question-removed`, a new reason with the operator as mover, so it is in neither of the third criterion's sets. `rg -n 'question-removed' tools -g '*.go'` printed nothing on this date, so the reason is new and the classification rule above applies to it.
- **Evidence, 2026-11-18, the uncommitted answer is this item's and is already in the saved work.** A worker run stopped before editing on the second criterion. It read "an answer written into the checkout and not committed changes nothing" as the committed-tree read, which the split gave to `item-owner-answer-from-committed-tree`, and so found the criterion and the split in contradiction. They are two cases. This item's is an answer still sitting in the checkout: `settleOwnerAnswers` in the saved work runs `git diff` of the document against the trunk and skips the conflict when the diff is not empty, and the saved work's `TestUncommittedOwnerAnswerDoesNotChangeTheConflict` in `tools/tillr-drain/drain/owner_question_test.go` changes the question's title to ✅ without a commit, runs the sync and asserts the status `question`, an empty `AnswerHead` and an unchanged event count. `git -C /workspace grep -n 'func TestUncommittedOwnerAnswer' refs/tillr/preserved/run-item-owner-conflict-is-a-document-question-a16 -- tools/tillr-drain` printed that test on this date. The second criterion's landed-answer test is the saved work's `TestOwnerReportQuestionGateHoldAnswerAndRelease`. So step 1 of the build order satisfies both of the criterion's tests, and no step writes a new one for it. The later item's case is an answer that was written, indexed and reverted, which the diff cannot see. The second criterion now names the guard it requires and names the other case as the later item's.
- **Why the work is split, settled by the design seat on 2026-11-18.** Builds kept stopping on this block when it held seven criteria across four modules, and three of them left the same change behind, which by the last was 1,440 added lines in 50 files outside the pack copy. The last ran on the last backend the build seat escalates to. It recovered the saved work, changed the migration to leave conflicts open, exposed the ledger ids, passed `go test -C tools/tillr-drain ./drain -run 'TestOwnerQuestion|TestUncommittedOwnerAnswer'`, and reported that the task exceeds it, naming the committed-tree read, the `question-removed` hold and the index equality as not done. The build order this block carried before the split was right and was too long for one run: eight steps, and every stopped build ended with a list of the steps it had not reached. So the saved work now lands first, in the form measured below, and each part that was never built is an item of its own with one to three criteria.
- **The saved work must land in this item, and no later item can recover it.** The loop removes an item's `refs/tillr/preserved/` refs when the item lands (the "Earlier uncommitted work" section of `tools/tillr-drain/README.md`, under Retention). The three later items therefore edit code that this item has landed, and none of them names a saved ref.
- **Measured 2026-11-18 at `660881e69`, the saved work is green after edits to eleven files.** The saved work of the last build is at `refs/tillr/preserved/run-item-owner-conflict-is-a-document-question-a16` in the main checkout, and its base is `660881e69`. Its tree was exported to a scratch copy, and steps 2 and 3 below were made there, in eleven files, without step 3's unit test of the filter and without step 4. The sharded suites for `tools/tillr`, `tools/tillr-item` and `tools/tillr-view` then passed, and `tools/tillr-drain` failed one test, `TestTheComposedPipelineRunsEndToEndOnAFixture`, with `watchdog unavailable: kernel task-clock counter: permission denied`. That test fails the same way in an exported copy of `660881e69` without the saved work and passes in a lane worktree, so it is a fact about the scratch copy and not about this change. `npm run typecheck && npm run test` in `tools/tillr-view/web` passed with 396 tests, and `python3 tests/tillr-docs.test.py` passed. The two tests that step 2 and step 3 add to `owner_question_test.go` were written after those suite runs and passed when run by name. Before the edits the saved work failed three tests: `TestAllDefinedHoldReasonsAreClassified` and `TestEveryReasonConstantHasAHoldRule` on the reason `owner-answer-sync`, and `TestWaitingForeignIdentityNamesCommitAuthorAndRemedy` in `tools/tillr-view`, which commit `86c0ddc76` added after the saved work was first written.
- **Settled by the design seat on 2026-11-18, and the owner's to overturn.** None of this is an open question for a worker.
  - **Statuses.** A conflict in status `question` is open. It has a published question, `QuestionRef` as `<document>#<id>`, and it waits on the owner. Both the settle and the migration leave a conflict in exactly that status. The field `QuestionMigrated` and every branch that reads it are deleted, so a migrated conflict cannot differ from a settled one.
  - **The planner reads the record and no index.** In `conflictDecision`, a conflict in status `question` is a hold with the reason `question-open` and `QuestionID` set to the id in `QuestionRef`. The reason `owner-answer-sync` is deleted: the sync records a landed answer earlier in the same pass, so the planner never sees the gap that reason named.
  - **A published question is its own item's hold.** `questionState` drops, before `firstUnanswered`, every question whose id is the `QuestionRef` of another item's conflict in status `question` for that document. The ids come from the conflicts the planner has already read, so no package parses a document for them. The filter is one exported function beside `FirstUnanswered` in `tools/tillr/vantage/vantage.go`.
  - **What this item leaves as the saved work has it.** The answer sync still reads the checkout's index, and skips a document that differs from the trunk. A question deleted without a ruling keeps its item held `question-open`, and the waiting view shows that row as `questions-unknown` with the operator as mover. Both are the next two items' work. Do not build them here.
- **Build order, each step a stop only if it fails.** Do the steps in order in one run. The end of a step is not an ending, and the commit is made once, at step 5.
  1. **Recover the saved work.** From the lane worktree run `git -C /workspace diff refs/tillr/preserved/run-item-owner-conflict-is-a-document-question-a16^ refs/tillr/preserved/run-item-owner-conflict-is-a-document-question-a16 -- ':!yolo-packs' | git apply -3 -`. The pack copy is left out because step 5 regenerates it. On `660881e69` this applies with no conflict. A conflict means the trunk has moved since, and it is resolved by keeping the trunk's structure and the saved work's intent. If a later run of this item has saved newer work, its brief names that ref: use the newest one and skip the edits below that it already holds. Then `go build ./...` passes in `tools/tillr`, `tools/tillr-item`, `tools/tillr-drain` and `tools/tillr-view`.
  2. **The record and the planner (c2, c4).** These are deletions. In `tools/tillr-drain/drain/blocked.go`, delete the constant `ReasonOwnerAnswerSync`; in the `question` case of `conflictDecision`, delete everything that reads `p.questions` and leave the case setting `d.QuestionID` from `QuestionRef` and `d.Reason = ReasonQuestionOpen`; and delete the line `c.QuestionMigrated = true`. In `tools/tillr-drain/drain/why.go`, delete the `ReasonOwnerAnswerSync` case of `whyPresentation`. In `tools/tillr-drain/drain/owner_answer.go`, the loop's skip condition becomes `c.QuestionRef == "" || c.Status != "question"`. Delete the field `QuestionMigrated` from `Conflict` in `tools/tillr-item/item/conflict.go` and from `waitingConflict` in `tools/tillr-view/waiting_conflict.go`, and run `gofmt -w` on both files. In `tools/tillr-view/waiting.go`, the condition before `decorateOwnerQuestionWaiting` becomes `c.Status == "question"`, and inside that function the branch on `vantage.QuestionAnswered` is deleted so that a listed question always gives the `needs you` row. In `tools/tillr-drain/drain/owner_question_test.go`, delete `|| !c.QuestionMigrated` from the migration test and the line `original.QuestionMigrated = backend == "command"` from the recovery test, and add to the migration test, after its first status check, an assertion that `decisionFor(t, l, "item-a").decision` is a hold with the reason `ReasonQuestionOpen`. In `tools/tillr-view/waiting_identity_test.go`, the expected mover gains the actor the saved work now prints: `operator; re-author the commit, or record its id with tillr-drain accept-identity`. Afterwards `rg -n 'QuestionMigrated|owner-answer-sync|OwnerAnswerSync' tools -g '!yolo-packs/**'` prints nothing, and `go test ./... -run 'Owner|HoldRule|HoldReasons|Classified|Why|Conflict|Question'` passes in `tools/tillr-drain`.
  3. **The shared document (c1).** Add the filter to `tools/tillr/vantage/vantage.go`, which then imports `slices`. The Go in this block is indented with spaces, so run `gofmt -w` on each file it is pasted into:

     ```go
     // WithoutQuestions answers the questions whose ids are not in ids, in the same order. A
     // question the loop published for one item is that item's hold alone, so a reader drops
     // the ids published for other items before asking FirstUnanswered.
     func WithoutQuestions(questions []Question, ids []string) []Question {
         if len(ids) == 0 {
             return questions
         }
         kept := make([]Question, 0, len(questions))
         for _, question := range questions {
             if !slices.Contains(ids, question.ID) {
                 kept = append(kept, question)
             }
         }
         return kept
     }
     ```

     Add a wrapper `withoutQuestions` beside `firstUnanswered` in `tools/tillr-drain/drain/questions.go`, and in `questionState` in `plan.go` call `firstUnanswered(withoutQuestions(answer.Questions, p.publishedForOthers(item)))`, with this method in `plan.go`:

     ```go
     // publishedForOthers answers the ids of the questions published in the item's design
     // document for other items' open conflicts. Each holds only the item it was published for.
     func (p *planner) publishedForOthers(row itemRow) []string {
         var ids []string
         for itemID, c := range p.conflicts {
             file, id, _ := strings.Cut(c.QuestionRef, "#")
             if itemID != row.ItemID && c.Status == "question" && file == row.DesignDoc && id != "" {
                 ids = append(ids, id)
             }
         }
         return ids
     }
     ```

     Add a test of `WithoutQuestions` to `tools/tillr/vantage/questions_test.go`: it drops only the ids it is given and keeps the order. Add this test to `owner_question_test.go`. In the scratch copy it passed, and it failed on the second item's decision when the call in `questionState` was put back to `firstUnanswered(answer.Questions)`:

     ```go
     func TestAPublishedQuestionHoldsOnlyItsOwnItem(t *testing.T) {
         f, l, d, base := designAnswerFixture(t)
         steps := &ownerQuestionSteps{t: t, f: f, l: l}
         l.cfg.Steps = steps
         c := settleDesignReport(t, f, l, d, base, ownerChoiceReport)
         file, id, _ := strings.Cut(c.QuestionRef, "#")
         s, err := store.Open(context.Background(), f.storePath, true)
         if err != nil {
             t.Fatal(err)
         }
         seedRefusalItem(t, s, "item-b")
         s.Close()
         fixtureWrite(t, f, func(tx *store.Tx) error {
             _, err := tx.Exec(`UPDATE items SET state='in-queue', design_doc=? WHERE item_id IN ('item-a','item-b')`, file)
             return err
         })
         l.questions = &VantageQuestions{Root: f.root, Command: ownerIndexCommand(file, id, "open")}
         if own := decisionFor(t, l, "item-a").decision; own.Action != ActionHold || own.Reason != ReasonQuestionOpen || own.QuestionID != id {
             t.Fatalf("the question's own item is not held on it: %+v", own)
         }
         if other := decisionFor(t, l, "item-b").decision; other.Action != ActionDispatch {
             t.Fatalf("a question published for item-a held item-b: %+v", other)
         }
         l.questions = &VantageQuestions{Root: f.root, Command: ownerIndexCommand(file, "OQ-1", "open")}
         if other := decisionFor(t, l, "item-b").decision; other.Action != ActionHold || other.Reason != ReasonQuestionOpen || other.QuestionID != "OQ-1" {
             t.Fatalf("the document's own question no longer holds item-b: %+v", other)
         }
     }
     ```

  4. **The README (c4).** In the section "Owner decisions in documents" of `tools/tillr-drain/README.md`, which the saved work adds, replace the two sentences that begin "Migration closes the legacy conflict only after publication" and "The document question still holds the item" with a statement that the migration leaves the conflict open in status `question`, exactly as a settled report does, and that the conflict is closed as answered only when the owner's ruling lands. Add one sentence saying that a published question holds only the item it was published for. `rg -n 'closes the legacy conflict' tools/tillr-drain/README.md` then prints nothing.
  5. **Finish.** Run `just pack-sync`, then `python3 tools/go-test-shards.py <module> -k 6` for each of `tools/tillr`, `tools/tillr-item`, `tools/tillr-drain` and `tools/tillr-view`, `npm run typecheck && npm run test` in `tools/tillr-view/web` (after `npm ci --no-audit --no-fund` when it has no `node_modules`), `yolo pack lint yolo-packs/tillr` and `python3 tests/tillr-docs.test.py`. Make one commit holding all of it.
- **What does stop this build:** a failing test that needs a file outside the item's paths, a step whose named function or file is not in the tree, or two criteria that cannot both hold. Report it with the step number, the command and its output. A report that lists the steps not yet done is not one of these, and neither is the work the three later items own: the committed-tree read, the `question-removed` hold and the index equality are not this item's to finish or to report as missing.
- **Verify:** the sharded suites of step 5, `go test ./... -run 'Owner|Published'` in `tools/tillr-drain`, `yolo pack lint yolo-packs/tillr`, `just pack-sync`, and `uvx vantage-check docs/tillr/roadmap.md`.

### Move the owner's answer read to the committed tree

The item above publishes an owner's question into a document and closes the conflict when the answer lands. Its answer sync reads the index of the checkout. That index is read once after a document sync and then kept, and the sync guards it only by skipping a document that differs from the trunk at the moment it looks. So an answer that is written into the checkout, indexed and then reverted without a commit can still close the conflict, because the kept index says "answered" and the document no longer differs. This item makes the sync read the index of the trunk commit itself, so that only a landed answer counts, and it proves the two forms a landed answer takes: a question marked answered, and a ruling compacted into the document's ledger.



**Agent details:**

- **Ref:** the section "Owner decisions in documents" in `tools/tillr-drain/README.md`; `docs/design/tillr-queue-persistence.md`, `D-UT4` and the sections on the `question-open` hold and the Vantage index.
- **Where:** `settleOwnerAnswers` in `tools/tillr-drain/drain/owner_answer.go`, which `refreshDocuments` in `loop.go` calls with the trunk commit; `VantageQuestions`, with its `Root`, `Command` and `Context`, and `LedgerIDs` on `DocumentQuestions`, in `tools/tillr/vantage/vantage.go`; the tests and the helper `ownerIndexCommand` in `tools/tillr-drain/drain/owner_question_test.go`; `yolo-packs/tillr/` copies, regenerated with `just pack-sync`.
- **The prerequisite is checked before any edit.** `rg -n 'func \(l \*Loop\) settleOwnerAnswers' tools/tillr-drain/drain/owner_answer.go` and `rg -n 'LedgerIDs' tools/tillr/vantage/vantage.go` each print a line once `item-owner-conflict-is-a-document-question` has landed. If either prints nothing, the lane was staged from a base without it. That is a staging fault for the loop to repair by restaging, and it is not a question for the design seat or the owner.
- **Measured 2026-11-18, how the index reports an answer (`vantage-check` 0.12.0).** In a copy of the tree, filling in the `**Answer:**` text of [`OQ-CL1`](../cost-levers/qa.md#OQ-CL1) left its state `open`. Changing its title marker from 💬 to ✅ made `uvx vantage-check index --format json` report the state `answered`. The tool's source reads the state from that marker only: ✅ is settled, 🔒 is blocked, and anything else is open. A document's `ledgerIds` are the first-cell ids of the table rows under a heading that reads `Decision Ledger`. On the same date 31 of 472 documents had a non-empty `ledgerIds`, and `docs/design/approver-agent.md` listed ten ids there with no entry in `questions`. So a test's answered question carries ✅ in its title, and its compacted ruling is a row `| <id> | ... |` under a `## Decision Ledger` heading with the numbered entry removed.
- **Measured 2026-11-18, the committed tree can be indexed on its own.** `git archive HEAD | tar -x -C <dir>` wrote the tree in 0.13 seconds, 59 MB, and `uvx vantage-check index --format json` run in that directory took 13.7 seconds and reported the same 472 documents and 171 questions as the checkout. So the sync can read the trunk commit's index without touching the checkout, and it must not do so on every pass.
- **Settled by the design seat on 2026-11-18, and the owner's to overturn.** None of this is an open question for a worker.
  - **The sync reads the committed tree.** For each conflict in status `question`, the sync compares `git rev-parse <commit>:<document>` with the blob it last examined for that conflict, kept in memory on the loop in a map from conflict id to blob, and does nothing more for that conflict when they are equal. When any differ, it exports the commit once with `git archive` into a temporary directory, runs the index command there through a new `VantageQuestions` whose `Root` is that directory and whose `Command` is `l.cfg.QuestionsCmd`, and removes the directory. It never uses `l.questions`, which reads the checkout, and it never reads the document itself. It deletes the `git diff` against the checkout, which the committed read replaces. An index that fails, or a document the commit does not hold, changes nothing, and the blob is not remembered, so the next pass tries again. A blob is remembered after its question has been examined, whatever the outcome.
  - **The outcomes, tried in this order.** The id is listed with the state `answered`: the conflict becomes `answered`, waiting on `build`, with `AnswerHead` the commit. The id is listed in any other state: the question is open and nothing is written. The id is not listed and is in the document's ledger ids: answered, as in the first outcome. The id is in neither place: nothing is written by this item. An outcome that changes nothing writes no event. The existing checks stay: the write is skipped when the trunk has moved since the commit was read or when the conflict changed in the store.
  - **The planner is not changed.** It holds a conflict in status `question` on `question-open` from the record, and it dispatches once the sync has recorded the answer.
- **The tests need an index command that answers from the tree it runs in.** `ownerIndexCommand` prints a fixed answer, so it cannot tell the export from the checkout. Keep it for the tests that only need a title, and add this beside it, running `gofmt -w` on the file afterwards. On 2026-11-18 the script printed an open question for a 💬 title, an answered one for ✅, the id under `ledgerIds` for a table row, an empty document for a file with none of them, and no document for a missing file:

  ```go
  const ownerTreeIndexScript = `f=$1; shift; q=; l=
  [ -f "$f" ] || { printf '{"index":{"documents":[]}}'; exit 0; }
  for id in "$@"; do
    if grep -q "✅ \*\*$id:" "$f"; then q="$q${q:+,}{\"id\":\"$id\",\"state\":\"answered\",\"title\":\"Which delivery should we use?\"}"
    elif grep -q "💬 \*\*$id:" "$f"; then q="$q${q:+,}{\"id\":\"$id\",\"state\":\"open\",\"title\":\"Which delivery should we use?\"}"; fi
    if grep -q "^| $id |" "$f"; then l="$l${l:+,}\"$id\""; fi
  done
  printf '{"index":{"documents":[{"path":"%s","questions":[%s],"ledgerIds":[%s]}]}}' "$f" "$q" "$l"`

  // ownerTreeIndexCommand answers from the document in the directory the command runs in.
  func ownerTreeIndexCommand(file string, ids ...string) []string {
      return append([]string{"sh", "-c", ownerTreeIndexScript, "questions", file}, ids...)
  }
  ```

- **Build order, each step a stop only if it fails.** Make the commit once, at step 4.
  1. **The reader's test (c2).** In `tools/tillr/vantage/questions_test.go`, add a test that parses an index document carrying `"ledgerIds":["ruled-1"]` for one document and finds that id in `Document(...).LedgerIDs`, and that an index document with no `ledgerIds` key gives an empty list. The reader already decodes the key, so this step changes no code outside the test. `rg -n 'ledgerIds' tools -g '*.go' -g '!*_test.go' -g '!yolo-packs/**'` prints lines in `tools/tillr/vantage/vantage.go` only, before and after this item.
  2. **The sync (c1, c2, c3).** Rewrite the loop body of `settleOwnerAnswers` to the settled design. Keep its reading of the conflicts, its trunk-moved check and its store write. Add the map of examined blobs to `Loop`.
  3. **The tests (c1, c2, c3).** Add `ownerTreeIndexCommand` to `owner_question_test.go`. Each test publishes a question with `settleDesignReport` as `TestOwnerReportQuestionGateHoldAnswerAndRelease` does, sets `l.cfg.QuestionsCmd = ownerTreeIndexCommand(file, id)`, and runs the sync through `refreshDocuments`. A committed ✅ title makes the conflict `answered`, and the next plan dispatches the item. A committed compacted ruling does the same. A ✅ title written to the checkout and not committed, and the same edit after it is reverted, both leave the conflict `question` with the same event count and the item held `question-open`. A committed document that lists the question as open and also holds a ledger row for its id leaves the conflict `question`. A committed document from which the entry was deleted with no ledger row leaves the conflict `question`. For the third criterion, give the script one more line that appends a line to a file outside the repository, run `refreshDocuments` twice with no new commit, and assert that the file holds one line. Change `TestUncommittedOwnerAnswerDoesNotChangeTheConflict` to set `l.cfg.QuestionsCmd` to the new command, because the rewritten sync no longer reads `l.questions`.
  4. **Finish.** In the section "Owner decisions in documents" of `tools/tillr-drain/README.md`, say that the sync reads the answer from the trunk commit and what counts as an answer. Run `just pack-sync`, `python3 tools/go-test-shards.py tools/tillr -k 6` and the same for `tools/tillr-drain`, `yolo pack lint yolo-packs/tillr` and `python3 tests/tillr-docs.test.py`. Make one commit holding all of it.
- **What does stop this build:** a failing test that needs a file outside the item's paths, a step whose named function or file is not in the tree, or two criteria that cannot both hold. Report it with the step number, the command and its output. A report that lists the steps not yet done is not one of these.
- **Verify:** `go test ./... -run 'Owner|Ledger'` in `tools/tillr-drain` and in `tools/tillr`, the sharded suites of step 4, `yolo pack lint yolo-packs/tillr`, `just pack-sync`, and `uvx vantage-check docs/tillr/roadmap.md`.

### Hold an item whose owner question was deleted

A question published for an item can be deleted from its document without a ruling, by a careless edit or a bad merge. After the two items above, the answer sync finds that question's id neither among the document's questions nor in its ledger and writes nothing, so the item stays held `question-open` on a question the planning index no longer lists. The owner then has nothing to answer, and nothing tells the operator to put the entry back. This item gives that case a hold of its own, `question-removed`, whose next mover is the operator, and returns the item to `question-open` when the entry is restored.



**Agent details:**

- **Ref:** the section "Owner decisions in documents" in `tools/tillr-drain/README.md`; `tools/tillr-drain/holdrules/holdrules.go`, whose `Rules` table is the one place a hold reason gets its class, place and mover.
- **Where:** the conflict record in `tools/tillr-item/item/conflict.go`; `settleOwnerAnswers` in `tools/tillr-drain/drain/owner_answer.go`; `conflictDecision` and the reason constants in `tools/tillr-drain/drain/blocked.go`; `landingHolds` in `tools/tillr-drain/drain/definition.go`; `whyPresentation` and `Why` in `tools/tillr-drain/drain/why.go`; the tests and `ownerTreeIndexCommand` in `tools/tillr-drain/drain/owner_question_test.go`; `yolo-packs/tillr/` copies, regenerated with `just pack-sync`.
- **The prerequisite is checked before any edit.** `rg -n 'ownerTreeIndexCommand' tools/tillr-drain/drain/owner_question_test.go` prints lines once `item-owner-answer-from-committed-tree` has landed. If it prints nothing, the lane was staged from a base without it. That is a staging fault for the loop to repair by restaging, and it is not a question for the design seat or the owner.
- **Evidence, 2026-11-18.** `rg -n 'question-removed' tools -g '*.go'` printed nothing, so the reason is new. An earlier build of the whole work failed `TestAllDefinedHoldReasonsAreClassified` with a reason it had added and not classified, which is why the third criterion exists.
- **Settled by the design seat on 2026-11-18, and the owner's to overturn.** None of this is an open question for a worker.
  - **One new field.** `QuestionRemovedHead` on `Conflict` in `tools/tillr-item/item/conflict.go`, JSON name `question_removed_head`, omitted when empty. It holds the trunk commit at which the sync found the question deleted. The status stays `question`, so the conflict is still open and no new status exists.
  - **The sync sets and clears it.** The sync already tries three outcomes in order and writes nothing for the fourth. They become four. The id is listed with the state `answered`, or is not listed and is in the ledger ids: the conflict becomes `answered` as it does today, and `QuestionRemovedHead` is cleared. The id is listed in any other state: the question is open, and a set `QuestionRemovedHead` is cleared, which is the restoration. The id is in neither place: `QuestionRemovedHead` is set to the commit. An outcome that changes nothing writes no event, so a conflict already marked removed at an earlier commit is not rewritten.
  - **The planner reads the record.** In `conflictDecision`, a conflict in status `question` gives the reason `question-removed` when `QuestionRemovedHead` is set and `question-open` otherwise. The planner reads no index for this.
  - **Nothing publishes twice.** The settle publishes only for a design run's report, and the migration selects only conflicts in status `owner`. A conflict in status `question` is neither, so restoring a deleted entry cannot lead to a second question for the same run id. The second criterion's test asserts it by counting the origin markers, the `<!-- tillr-owner-question ... -->` comments that name the run.
  - **Who moves.** The `Rules` row is `"question-removed": {"needs you", "queue", "restore the removed question under the item's block and land it"}`. In `Why`, the row's mover is `operator; restore <document>#<id> under the item's block and land it`, its why ends with `the question was removed without a ruling`, and it adds to the operator's count. The waiting view's own row for this case is the next item's work: until then the view shows the row it already shows for a question its index does not list.
- **Build order, each step a stop only if it fails.** Make the commit once, at step 4.
  1. **The record and the reason (c1, c3).** Add `QuestionRemovedHead` to `Conflict`. Add `ReasonQuestionRemoved = "question-removed"` beside `ReasonConflictOwner` in `blocked.go`, an entry beside `ReasonConflictOwner` in `landingHolds`, the `Rules` row, and a case in `whyPresentation` that returns `needs you`, the row's place and the rule's mover. Make the `conflictDecision` change. Then `TestAllDefinedHoldReasonsAreClassified` and `TestEveryReasonConstantHasAHoldRule` pass. If a test in `tools/tillr-view` lists every rule and fails on the new row, add the row there.
  2. **The sync (c1, c2).** Make the fourth outcome and the two clearings in `settleOwnerAnswers`.
  3. **The row and the tests (c1, c2).** In `Why`, give the `question-removed` row the mover and the why settled above. Tests in `owner_question_test.go`, each through `refreshDocuments` with `ownerTreeIndexCommand`: a commit that deletes the numbered entry leaves the status `question`, sets `QuestionRemovedHead` to that commit, plans the item as a hold with `ReasonQuestionRemoved`, and gives a `Why` row whose mover starts with `operator; restore` and names the document and the id, with `Summary.OwnerWaiting` zero and `Summary.OperatorNext` one. A commit that reverts the deletion clears the field and holds the item `question-open`, and the document holds one origin marker for the run id. A commit that restores the entry with a ✅ title makes the conflict `answered`. Change the earlier test that asserts a deleted entry leaves the conflict unchanged: it now asserts the field is set.
  4. **Finish.** In the section "Owner decisions in documents" of `tools/tillr-drain/README.md`, describe the `question-removed` hold and the operator's repair. Run `just pack-sync`, `python3 tools/go-test-shards.py <module> -k 6` for each of `tools/tillr-item`, `tools/tillr-drain` and `tools/tillr-view`, `yolo pack lint yolo-packs/tillr` and `python3 tests/tillr-docs.test.py`. Make one commit holding all of it.
- **What does stop this build:** a failing test that needs a file outside the item's paths, a step whose named function or file is not in the tree, or two criteria that cannot both hold. Report it with the step number, the command and its output. A report that lists the steps not yet done is not one of these.
- **Verify:** `go test ./... -run 'Owner|HoldRule|HoldReasons|Classified'` in `tools/tillr-drain`, the sharded suites of step 4, `yolo pack lint yolo-packs/tillr`, `just pack-sync`, and `uvx vantage-check docs/tillr/roadmap.md`.

### Test that owner-waiting work equals the planning index

The point of the three items above is that waiting on the owner has one form and one place: every item that waits on him is held on a question the planning index lists, and every question the loop published for him is some item's hold. Each of those items tests its own part. Nothing yet states the whole as one equality and tests it, and the waiting view still decides for itself, from its own read of the index, whether a published question is open or gone. This item writes the equality as a test against the index of the committed tree, makes the waiting view read the conflict record as the planner does, and adds the one test that walks a run through the whole path.



**Agent details:**

- **Ref:** the section "Owner decisions in documents" in `tools/tillr-drain/README.md` and the waiting section of `tools/tillr-view/README.md`; `docs/design/tillr-queue-persistence.md`, `D-UT4`, which rules that questions are document state and never item rows.
- **Where:** `Why` and its `Summary` in `tools/tillr-drain/drain/why.go`; `decorateOwnerQuestionWaiting`, `assembleWaiting` and `printWaiting` in `tools/tillr-view/waiting.go`; `waitingConflict` in `tools/tillr-view/waiting_conflict.go`; `WithoutQuestions` in `tools/tillr/vantage/vantage.go`; `TestWaitingSummarySeparatesDocumentAnswersFromOtherNextActors` in `tools/tillr-view/owner_waiting_test.go`; the tests and `ownerTreeIndexCommand` in `tools/tillr-drain/drain/owner_question_test.go`; `yolo-packs/tillr/` copies, regenerated with `just pack-sync`.
- **The prerequisite is checked before any edit.** `rg -n 'QuestionRemovedHead' tools/tillr-item/item/conflict.go` prints a line once `item-owner-question-removed-hold` has landed. If it prints nothing, the lane was staged from a base without it. That is a staging fault for the loop to repair by restaging, and it is not a question for the design seat or the owner.
- **Settled by the design seat on 2026-11-18, and the owner's to overturn.** None of this is an open question for a worker.
  - **The two sets of the equality.** The owner-waiting rows are the rows of `Why` whose reason is `question-open`. Each names its target in its mover, `owner; answer <document>#<id>`. The published questions are the `QuestionRef` values of the conflicts in status `question`. The index is read from an export of the trunk commit: the test runs `git archive` of the trunk into a temporary directory and gives `Why` a `VantageQuestions` whose `Root` is that directory and whose `Command` is `ownerTreeIndexCommand` with the published id.
  - **The fixture for the equality.** It is the fixture of `TestAPublishedQuestionHoldsOnlyItsOwnItem`: two items name one document, and a question is published for the first. The index then lists one unanswered question, and exactly one row is owner-waiting, the first item's, so the test also shows that the second item's row does not name the question. After the equality is asserted, the question's entry is deleted in a commit and the sync runs. The first item's row then has the reason `question-removed`, the index lists no question, no row is owner-waiting, and `Summary.OwnerWaiting` is zero.
  - **The view reads the record.** `waitingConflict` gains `QuestionRemovedHead`, JSON name `question_removed_head`. In `decorateOwnerQuestionWaiting`, the row is chosen by that field and not by whether the index lists the id. The owner's row has the class `needs you` and the mover `owner; answer <document>#<id>`. The operator's row has the class `needs you`, the mover `operator; restore <document>#<id> under the item's block and land it`, and a why that says the question was removed without a ruling. An index that cannot be read leaves the title out and changes neither row.
  - **The view's shared-document filter.** In `assembleWaiting`, where a `question-open` row takes the first open question of the item's design document, the view first drops the ids that are the `QuestionRef` of another item's conflict in status `question` for that document, by calling `vantage.WithoutQuestions`. No second filter is written.
- **Build order, each step a stop only if it fails.** Make the commit once, at step 4.
  1. **The view (c2).** Make the two view changes settled above. In `tools/tillr-view/owner_waiting_test.go`, add a test that calls `decorateOwnerQuestionWaiting` with a conflict in status `question`, once with the field empty and once with it set, and asserts the two rows. Extend `TestWaitingSummarySeparatesDocumentAnswersFromOtherNextActors` with a row whose mover is the operator's restore mover, and assert that the summary counts it under the operator and leaves the owner-waiting count unchanged.
  2. **The equality (c1).** Write the test in `tools/tillr-drain/drain/owner_question_test.go` as the first criterion states it and with the fixture settled above.
  3. **The whole path (c3).** `TestOwnerReportQuestionGateHoldAnswerAndRelease` already walks the path with an index command that prints a fixed answer. Change it to use `ownerTreeIndexCommand` throughout, so that the answer it releases on is the one committed on the trunk, and keep every assertion it makes.
  4. **Finish.** In `tools/tillr-drain/README.md`, name the three tests as the regression set for owner questions, and in the waiting section of `tools/tillr-view/README.md` describe the two rows. Run `just pack-sync`, `python3 tools/go-test-shards.py <module> -k 6` for each of `tools/tillr`, `tools/tillr-drain` and `tools/tillr-view`, `npm run typecheck && npm run test` in `tools/tillr-view/web` (after `npm ci --no-audit --no-fund` when it has no `node_modules`), `yolo pack lint yolo-packs/tillr` and `python3 tests/tillr-docs.test.py`. Make one commit holding all of it.
- **What does stop this build:** a failing test that needs a file outside the item's paths, a step whose named function or file is not in the tree, or two criteria that cannot both hold. Report it with the step number, the command and its output. A report that lists the steps not yet done is not one of these.
- **Verify:** `go test ./... -run 'Owner'` in `tools/tillr-drain` and in `tools/tillr-view`, the sharded suites of step 4, `yolo pack lint yolo-packs/tillr`, `just pack-sync`, and `uvx vantage-check docs/tillr/roadmap.md`.

## Filed 2026-11-17: a worker's failure goes back to the worker

The owner ruled on 2026-11-17 that a worker run's ending is never counted. A refused run now goes back to its worker with its evidence, every time, and only an identical repeat goes to the design seat. That is `D-JL10` in [the judge and lifecycle design](../design/tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10). Two parts of the ruling are not built, and each has an item below.

### Route a worker's refused ending to its own session

When the gate refuses a worker's candidate, or the worker ends without committing, the next run of the item now starts as a fresh session. Its brief carries the refusal, the failing checks with their logs, and the refs that keep the earlier run's work. That works, but the new session has to read the whole item again and rebuild the reasoning the earlier session already had, and it pays for that context a second time. The ruling asks for the refusal to go back to the session that produced it, where the harness can resume one. The drain already continues a worker's own session for uncommitted work and after a wall, so the mechanism exists on the lane. What is missing is keeping the lane and its session after a refusal, instead of resetting the lane, and handing the refusal to that session as its next turn. The design seat has the same gap, and its item is `item-design-refusal-resumes-session`. The two should share one mechanism.

- **Outcomes:**
  - Deliver a refused worker run's evidence to that run's own session when the harness can resume it.
  - Fall back to the fresh run with the evidence when it cannot, and record which one happened.
  - Keep no count: a refusal goes back every time, and an identical repeat still goes to the design seat.



**Agent details:**

- **Ref:** [Section 4.4 of the judge and lifecycle design](../design/tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10); the section "A refused run goes back to its worker" in `tools/tillr-drain/README.md`; [the board state machine, section 7](../design/board-state-machine.md#7-suspend-and-resume).
- **Where:** `returnedEnding` and the `returned-with-evidence` dispatch in `decide` in `tools/tillr-drain/drain/plan.go`; `returnedBrief` and `repeatedEnding` in `tools/tillr-drain/drain/returned.go`; the refusal paths of `settle` and `verify` and the continuation `resumeAfterWall` in `tools/tillr-drain/drain/loop.go`, where `abandon` resets the lane today; `yolo-packs/tillr/` copies, regenerated with `just pack-sync`.
- **Evidence:** on 2026-11-17, `tillr drain why` held `rewrite-item-design-refusal-resumes-session` as `gate-failed` because the fast check `documents` refused a dead anchor in its commit. Under the ruling that run's own session is the one that knows which anchor it wrote.

### Route an infrastructure failure to the next open backend

The owner ruled on 2026-11-17 that a machinery failure is not counted. Each failure should move the item to the next backend that is open, and when no backend is open the item should wait on the backend. Today the drain still allows one failover per item and then holds it as `run-infra-exhausted`, which names nobody who can move it. That hold was not removed with the other counts, because the drain has no fact that says a backend is open. It knows one only from a quota window, a refusal the provider stated with a reset time. An adapter that crashes, or a stream that drops, records no window. Without the budget, the planner would send the item straight back to a broken adapter on the next pass. A pass that dispatched a run never backs off, so the loop would spin on that adapter forever and never drain. So "open" has to become a recorded fact before the budget can go: a backend whose latest run failed in the infrastructure class is closed until something shows it is working again, such as a probe that succeeds or another run that ends normally.

- **Outcomes:**
  - Record when a backend is closed and what reopens it, without a clock or a count.
  - Move each machinery failure to the next open backend, and hold the item waiting on the backend when none is open.
  - Remove the failover budget and the `run-infra-exhausted` hold, for single runs and for workflow nodes.



**Build this item with the four steps below. Read this section first; it is the fifth ruling, of
2026-11-18, and it answers the ninth run's conflict.** Everything in this block after "Agent
details" is the record of how the change was designed. A builder does not act on it. No criterion,
path or rule changes here.

The ninth run applied the seventh run's preserved tree and nothing else. Its session log shows that
it read the head of this block and the first ruling, and never reached the patch the fourth ruling
gave. The tests it reported red are the ones that patch repairs: the plan that dispatched where it
should hold, quota continuation, the workflow restart, and the view's rule for `backend-closed`.
So the two patches are now one, and the commands come before the history. The patch at the end of
this block is the whole change to `config/`, `tests/` and `tools/`: 38 files, 1087 lines added and
240 removed. It needs no preserved ref. The refs your brief lists under "Facts carried" are the
earlier runs' trees; do not apply, inspect or recover any of them.

- **Build order, each step a stop only if it fails.** Do the steps in order in one run. The end of
  a step is not an ending, and the commit is made once, at step 4.
  1. **Apply the patch.** From the lane worktree:

     ```bash
     awk '/^# backend-open-patch end$/{p=0} p; /^# backend-open-patch begin$/{p=1}' docs/tillr/roadmap.md > "$YOLO_DURABLE_DIR/backend-open.diff"
     git apply --index "$YOLO_DURABLE_DIR/backend-open.diff"
     git diff --cached --shortstat HEAD
     (cd tools/tillr-drain && gofmt -l . && go build ./...)
     ```

     The third command prints `38 files changed, 1087 insertions(+), 240 deletions(-)`, and the
     fourth prints nothing. Do not rewrite what the patch produces: after this step no code
     remains to be written.
  2. **Regenerate the mirror.** Run `just pack-sync`, then `yolo pack lint yolo-packs/tillr`,
     which prints `pack ok`.
  3. **Run the suites,** each with the two trace variables unset and its output in a file, and
     read only the last line of each file:

     ```bash
     env -u TILLR_SQLITE_TRACE -u TILLR_TRACE_DIR TMPDIR="$YOLO_DURABLE_DIR" python3 tools/go-test-shards.py tools/tillr-view -k 6 > "$YOLO_DURABLE_DIR/view.log" 2>&1; echo "view rc=$?"
     env -u TILLR_SQLITE_TRACE -u TILLR_TRACE_DIR TMPDIR="$YOLO_DURABLE_DIR" python3 tools/go-test-shards.py tools/tillr-drain -k 6 > "$YOLO_DURABLE_DIR/drain.log" 2>&1; echo "drain rc=$?"
     python3 tests/tillr-muse-backend.test.py > "$YOLO_DURABLE_DIR/muse.log" 2>&1; echo "muse rc=$?"
     tail -n 1 "$YOLO_DURABLE_DIR/view.log" "$YOLO_DURABLE_DIR/drain.log" "$YOLO_DURABLE_DIR/muse.log"
     ```

     The drain suite ran for about 25 minutes here. Give the command a timeout of at least an
     hour, and do not run a narrower `go test` in its place.
  4. **Commit.** `git add -A config tests tools yolo-packs`, then one commit. It holds 71 files:
     the 38 of the patch and the 33 mirror copies. `docs/tillr/roadmap.md` is not part of it.
     Append `c1` to `c5` to the progress file before the commit.
- **What does stop this build:** the patch failing to apply after the fallback below, a build
  failure after step 1, or a failing test in step 3. Report it with the step number, the command
  and its output. A report that lists steps not yet done is not one of these, and neither is the
  size of the change.
- **If the patch does not apply,** the trunk has moved under one of its files. Run the second
  command of step 1 again as `git apply --index -3`, resolve each conflict towards the patch and
  keep what the trunk added, and continue. The third ruling below describes every hunk by file.
- **If a test fails in step 3,** run it alone once, with the trace variables unset. A test that
  passes alone is recorded in the report and does not stop the commit. For a test that fails
  alone, run it alone at the base with `git stash -u`, and restore the work with `git stash pop`.
  Red at the base is a `trunk` obstacle naming that test. Red only with the patch applied is this
  item's to fix, by the rule in the third ruling that covers the file.
- **What this section measured,** in the lane at `b045866f2` on 2026-11-18 with the patch applied
  and the mirror regenerated:
  - `python3 tools/go-test-shards.py tools/tillr-drain -k 6` printed `2093 tests passed, 6 shards
    per package` and exited 0. `TestRestartGatesAndLandsTheRetiredHead`, which the fourth ruling
    records as red on the trunk, passed: the trunk has repaired it.
  - `python3 tools/go-test-shards.py tools/tillr-view -k 6` printed `504 tests passed, 6 shards
    per package` and exited 0.
  - `python3 tests/tillr-muse-backend.test.py` and `python3 tests/tillr-docs.test.py` exited 0,
    and `yolo pack lint yolo-packs/tillr` printed `pack ok`. The lint ran the jail's binary, so
    the host's render was not verified.

**Agent details:**

- **Ref:** [Section 4.4 of the judge and lifecycle design](../design/tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10), ending 8; finding F-04 in [the graph design's audit table](../design/tillr-graph-is-the-path.md#124-audit-findings).
- **Where:** `attempt` in `tools/tillr-drain/drain/plan.go`, which counts every infrastructure-class run against `failovers()`; `nextBackend`, `failovers` and `NextRequest` in `tools/tillr-drain/drain/policy.go`; `availableBackend`, `recordBackendWindow` and `nextAvailableRequest` in `tools/tillr-drain/drain/quota_window.go`; the in-tick failover in `settle` in `tools/tillr-drain/drain/loop.go`; `retryWorkflowNode` in `tools/tillr-drain/drain/workflow_failover.go` and `parallelFailover` in `tools/tillr-drain/drain/parallel_failover.go`; the `fault` branch of `commandNodeHold` in `tools/tillr-drain/drain/workflow_order.go`.
- **Evidence:** on 2026-11-17 a version of this change that removed the budget and wrapped the walk was written and taken back before it landed, from reading the code rather than from a run. `recordBackendWindow` in `tools/tillr-drain/drain/quota_window.go` records a window only for a refusal that states a reset time, and `TestConsecutiveInfrastructureFailuresDoNotStopTheLoop` in `tools/tillr-drain/loop_test.go` drives a loop with one always-failing backend that must drain. With no budget, nothing closes that backend, so every pass would dispatch it again and the loop would not drain.

**Ruling, 2026-11-18 (answers the first run's fence conflict):** the first run built most of this
item and stopped without committing. Its reviewer found that a run's report can be edited or
deleted between the moment the run ends and the moment the drain first reads it, and the run
concluded that only a change under `tools/tillr-run/` could make the classification trustworthy.
That directory stays outside this item's paths, and the criteria above are unchanged. The item
asks for a recorded fact that a backend is open or closed. It does not ask for a report that
cannot be altered. Five points settle how the work is finished:

1. **The drain records a run's class once, when it first reads the ended run.** The class is what
   `ClassifyAttempt` in `tools/tillr-drain/drain/failover.go` answers from the run record and the
   log as they are at that moment. The drain saves that class as its own record, and the open or
   closed state of a backend is computed from the saved classes and the saved probe results only.
   Nothing reads the report a second time for this purpose, so a report that changes or
   disappears later neither closes nor reopens a backend.
2. **A report that changes before the drain's first read is out of scope.** The drain has always
   classified from the log it finds, and the failover budget this item removes had the same gap.
   Both wrong answers correct themselves through recorded events, so the loop cannot spin on
   either. A backend closed by mistake is reopened by a probe that succeeds. A backend left open
   by mistake is closed by its next run, which fails and is recorded. The README states this
   limit in one sentence beside the probe. Capturing the class when the run's ending is written
   is separate work under `tools/tillr-run/` and is not part of this item.
3. **The latest run is the latest by ending, not by row order.** The first run found that a spend
   correction rewrites a run row and moves its sequence number. "Latest run on a backend" is
   therefore ordered by the recorded ending of each run, and a later edit to a run's row does not
   reorder it.
4. **A hold names the backend that is closed.** When a group of parallel nodes waits because one
   node has no open backend, the hold names that node's backend and its reopening event, and not
   the backend of the group's first node. The test c4 asks for may cover this case or a single
   node; the hold's text must be right in both.
5. **The waiting view keeps the item's stage.** The view's `Where` value for an item held on a
   closed backend stays the item's queue stage, as the view returns it today. c5 is met when the
   classification names the backend and the mover that reopens it. The first run's new view test
   asserted `Where: backend`, which is wrong; correct the assertion and leave the view as it is.

Start from the first run's work and do not rewrite it. It is kept at
`refs/tillr/preserved/run-item-backend-open-is-a-fact-a1` in the main checkout: 61 files, of
which the source changes are under `tools/tillr-drain/`, `tools/tillr-view/`, `config/tillr/` and
`tests/tillr-muse-backend.test.py`, and the rest are the pack copies that `just pack-sync`
regenerates. Its `probe-backend` command, which runs a check the operator chooses and never runs
on its own, stands as the probe c1 names. What remains is point 1 where the saved class is not
yet the only input, point 4, the corrected view assertion, the full drain and view suites, and
the one commit.

**Second ruling, 2026-11-18 (answers the third run's conflict):** the third run finished points 1
to 5 above and stopped without committing, for two reasons. Neither changes the criteria or the
item's paths.

The first reason was a check that is red on the trunk. `python3 tests/tillr-docs.test.py` prints
`FAIL run README environment list` and exits 1 at `f65877b5a` with none of this work applied,
because `tools/tillr-run/README.md` does not list `TILLR_READ_ONLY_GROUP`, which
`tools/tillr-run/run/exec.go` gives to every run. This item touches neither file, and both are
outside its paths. The failure belongs to the trunk and is not a reason to stop. The builder
leaves the README and the check alone, confirms that `git diff --name-only HEAD -- tools/tillr-run
tests/tillr-docs.test.py` prints nothing, commits, and names the failure in its report with the
base sha. That script stops at its first failure, so the cases after it do not run. If the trunk
has repaired the README by the time this is built, the check passes.

The second reason was the candidate's own drain suite, and that is work to finish. The third
run's tree is kept at `refs/tillr/preserved/run-item-backend-open-is-a-fact-a3` in the main
checkout, and it was measured here on 2026-11-18. `git diff bc1f61db0 <that ref> -- .
':!yolo-packs' | git apply -3` onto `f65877b5a` applies 36 files with one conflict, in
`tools/tillr-drain/drain/plan.go`, where the trunk now calls the planner's own `p.firstBackend`.
With that conflict resolved towards the candidate and the trunk's method kept, `go test ./... -run`
over the ten tests the third run's report lists fails all ten. The eight of them that exist at
`f65877b5a` pass there. The causes were read from those runs, and they are four gaps in the rules
above. Each is settled here:

6. **An ended run with no saved class is classified by whoever reads it, and only the loop
   saves.** This refines point 1. The third run's `readInfrastructureClosed` skips a run whose
   class is not saved yet, while `readPriorRuns` computes the class for the same run. So a plan
   made before the loop has saved anything sees a failed run and no closed backend, and
   dispatches a failover where it should hold. Both readers use one rule: the saved class when
   there is one, and otherwise what `ClassifyAttempt` answers at that moment, without saving it.
   A face that only reads, such as `plan` or the view, never writes a class. With this change
   alone, `TestPlanHoldsWhenEveryBackendIsClosed` and `TestProbeBackendFaceRecordsSuccessAndFailure`
   passed here. The third run's `TestBackendAvailabilityUsesOnlySavedClass` asserts the opposite
   and is replaced by a test of this rule. Once a class is saved it still wins over the report,
   as point 1 says.
7. **A backend closes only on an ending that the trunk answers with a failover today.** The
   class that decides availability is the class the planner routes on, which `readPriorRuns`
   already corrects: a run the loop interrupted itself, by a stop, an orphaned run or a stale
   base, is `interrupted` and not `infrastructure`. The third run saved the uncorrected class, so
   an interrupted run closed its backend. The same holds for the two endings the trunk already
   answers by another rule. A run the provider refused with a stated reset time records a quota
   window, and the window alone closes that backend and reopens it. A run whose resumed session is gone is
   replaced by a fresh session on the same backend. None of these three is evidence that the
   backend is broken, so none closes it, and each is "any other class" for c1's reopening.
   These tests pass at `f65877b5a` and must pass unchanged:
   `TestWorkflowRestartRecoversSignalRecordWithoutStopMarker`,
   `TestQuotaContinuationWaitsAndReopensOriginalBackend`,
   `TestLoopSkipsSpentWindowAcrossRestartAndReopens`, `TestReturnedWorkerGoneHarnessSession` and
   `TestReturnedWorkflowResumesProducingNode`.
8. **A retry the owner authorized dispatches once, even to a closed backend.** The authorization
   is a recorded event, and its run retires it, so it is neither a clock nor a count and the loop
   cannot spin on it. The run starts on the item's own backend as it does today. If it ends in
   any other class it reopens the backend by c1's second event, and if it fails in the
   infrastructure class the backend stays closed and the item waits on it. This is the only
   dispatch to a closed backend, and the README says so beside the probe.
   `TestTheLoopDispatchesAnAuthorizedRetry` passes at `f65877b5a` and must pass unchanged.
9. **Saving a class does not make a pass write that would not have written.** The third run saves
   classes at the top of every quota refresh, and each save appends an event. A pass that defers
   because the store changed under it must write nothing, and
   `TestAPassDefersChangedItemRunAndLeaseWithoutWriting` failed with `stale application wrote 1
   event(s)`. Save the class where the loop already writes about an ended run, and when a loop
   starts for runs that ended while none was running. Where exactly is the builder's choice; that
   test passes at `f65877b5a` and must pass unchanged.

One base test does change. `TestPlanFailsAListedSeatOverAlongItsOwnList` in
`tools/tillr-drain/seat_test.go` expects the `run-infra-exhausted` hold this item removes. Its
last assertion becomes the hold on the closed backend, and the rest of it, which pins that an
item never leaves its seat's list, stays as it is.

Start from the third run's tree and not from the first run's; it contains the first run's work
and the repairs for points 1 to 5. Rebase it onto the current trunk, resolve the one conflict as
described, apply points 6 to 9, and run `just pack-sync`. Then run the sharded suites for
`tools/tillr-drain` and `tools/tillr-view` in full before the one commit. The third run's first
full drain run timed out at 1800 seconds because failing loop tests waited out their own
timeouts, so run the ten tests named in its report first and start the full suite only when they
pass.

**Third ruling, 2026-11-18 (answers the fifth run's conflict): the plan, step by step.** The fifth
run rebased the third run's tree, reproduced the stale-pass write, and stopped: it had the rules
above and no plan for applying them. This ruling is that plan. It changes no criterion and no
path. It was built and measured here on 2026-11-18 on top of `2acf66dc4`, outside the repository,
and the result is stated under "What the plan measured" below. Where a step below is more exact
than points 6 to 9, the step is the rule.

Two things about the tree changed after the second ruling was written, and both are corrected
here:

- **The view's rule table moved.** The trunk now keeps every hold reason's display rule in
  `Rules` in `tools/tillr-drain/holdrules/holdrules.go`, and `tools/tillr-view/waiting.go` only
  calls `holdrules.For`. The earlier runs added their rules to a `waitingRules` map in the view
  that no longer exists. c5 is met in `holdrules.go`.
- **Three base tests change, not one.** Besides `TestPlanFailsAListedSeatOverAlongItsOwnList`,
  the tests `TestLoopDoesNotSkipBackendWithoutWindowFacts` in
  `tools/tillr-drain/quota_window_test.go` and `TestPlanTransitionPayloadCountsTerminalRuns` in
  `tools/tillr-drain/transition_test.go` pin the behaviour this item removes: both expect a
  second item to start on a backend whose latest run just failed. Step 9 says what each becomes.

**Before any step: quiet the test output.** A run's environment sets `TILLR_SQLITE_TRACE` and
`TILLR_TRACE_DIR`, and with them every `go test` prints one line per store and git call. One
targeted run here printed more than 30,000 bytes of trace before its first result, and one full
run wrote a 78 MB log. Run every test command in this item with both variables unset and its
output in a file in the run's scratch directory, and then read only the result lines:

```bash
env -u TILLR_SQLITE_TRACE -u TILLR_TRACE_DIR go test ./... -run 'TestName' > "$YOLO_DURABLE_DIR/test.log" 2>&1
grep -a -A6 -e '^--- FAIL' -e '^    --- FAIL' -e '^panic:' -e '^FAIL' -e '^ok  ' "$YOLO_DURABLE_DIR/test.log" | grep -av '^loop: ' | cut -c1-600
```

**Step 1. Start from the fifth run's tree.** It is the third run's tree already rebased once, kept
at `refs/tillr/preserved/run-item-backend-open-is-a-fact-a5` in the main checkout. Its merge base
with the trunk is `e8ba23743`. In the lane worktree:

```bash
git diff e8ba23743 refs/tillr/preserved/run-item-backend-open-is-a-fact-a5 -- config tests tools | git apply -3
```

On `2acf66dc4` this applies 36 files and leaves four conflicts. Resolve each as follows, and then
`go build ./...` in `tools/tillr-drain` passes:

| File | Resolution |
|---|---|
| `tools/tillr-drain/drain/workflow_failover.go` | Take the candidate's side: the `if !ok` branch that keeps a ready continuation. Drop the trunk's branch that reads `l.cfg.Policy.failovers()`. |
| `tools/tillr-drain/loop_test.go` | Take the candidate's side: the second and third loops and the `ProbeBackend` call. |
| `tools/tillr-drain/main.go` | Keep the trunk's `faceList` and add `probe-backend, ` at its front. The trunk's list has `accept-identity` and `sweep-refs`, which the candidate's lacks. |
| `tools/tillr-view/waiting.go` | Delete the candidate's whole `waitingRules` map and its comment. Keep the trunk's `waitingRuleFor`, which calls `holdrules.For`. The candidate's other hunk in this file, which appends the rule's mover after `deferredBy` for `backend-closed`, applies cleanly and stays. |

**Step 2. Add the display rules (c5).** In `Rules` in `tools/tillr-drain/holdrules/holdrules.go`,
add two rows and change one, then run `gofmt -w` on the file:

```go
"backend-closed": {"waits on work", "backend", "a successful tillr-drain probe-backend or a non-infrastructure run"},
"command-fault":  {"needs a cause", "lane", "repair the command process and authorize a retry"},
// History only: an infrastructure failure now closes the backend, not the item.
"run-infra-exhausted": {"waits on work", "backend", "a successful tillr-drain probe-backend or a non-infrastructure run"},
```

`TestEveryReasonConstantHasAHoldRule` then passes. Point 5 is about the row the view returns, not
about this table: `TestWaitingBackendClosedNamesBackendAndReopeningMover` in `tools/tillr-view`
asserts `Where: queue` for a queued item and passes unchanged.

**Step 3. Save two facts for each ending, and classify an unsaved ending as it is read (points 6
and 7).** Rewrite `tools/tillr-drain/drain/backend_outcomes.go`:

- The table gains a column: `closes INTEGER NOT NULL DEFAULT 0`. `class` stays what
  `ClassifyAttempt` answers, because `readPriorRuns` routes on it and a quota refusal must still
  route as a failover. `closes` is the availability fact: 1 only for an infrastructure-class
  ending that no other rule answers. The `drain.backend-outcome` event carries both.
- `backendOutcome` gains `closes bool` and `saved bool`.
- `readBackendOutcomes` selects `COALESCE(o.class,'')` and `COALESCE(o.closes,0)`, sets `saved`
  when the class is not empty, and closes its rows. Then, only when some ending is unsaved, it
  reads the interrupted runs and `readPendingHistory`'s second result (pending work by run id)
  and fills each unsaved ending in memory:

  ```go
  rec := out[i].rec
  out[i].class = ClassifyAttempt(rec, logTail(rec.ArtifactRefs))
  out[i].closes = out[i].class == AttemptInfrastructure && !answeredByAnotherRule(rec, interrupted, byRun)
  ```

  It writes nothing. This is the one rule both readers share.
- `answeredByAnotherRule` is point 7 as code. It answers true for a run in the interrupted set,
  for a run where `refusedWindow(rec)` reports a stated reset time, and for a run whose pending
  decision makes `failedDesignerResume(decision, rec)` answer true, which is the resumed session
  that is gone. For the last, look the run up in the pending work by run id, and when the run is
  one of `parallelNodes(work.Decision)`, pass that node's decision. Treat an error from
  `failedDesignerResume` as false.
- Move the event loop at the top of `readPriorRuns` in `tools/tillr-drain/drain/failover.go`
  into `readInterruptedRuns(r) (interrupted, restaged map[string]bool, err error)` in this file,
  unchanged: `loopOrphanedKind` and `staleBaseKind` add a run and `run.ResumeKind` removes it.
  `readPriorRuns` calls it, and takes a class from the outcomes only when `o.saved` is true.
- `RecordBackendOutcomes` inserts each ending whose `saved` is false, with its `class` and
  `closes`, using `INSERT OR IGNORE` as it does now.

In `readInfrastructureClosed` in `tools/tillr-drain/drain/backend_availability.go`, the loop over
outcomes becomes: for the first ending seen for each backend, record its sequence and set
`closed[name] = o.closes`. Remove the `o.class == ""` skip. The probe query after it is unchanged.

**Step 4. Save only where the loop settles a run and when it starts (point 9).** Remove the
`RecordBackendOutcomes` call from `refreshQuotas` in `tools/tillr-drain/drain/quota_probe.go`.
Add the call in exactly three places in `tools/tillr-drain/drain/loop.go`, each returning the
error the way its neighbours do:

1. in `Run`, directly after the `l.startDefinition(ctx)` check;
2. in `settle`, before `lane := decision.LaneID`;
3. in `settle`'s failover loop, directly after `closeRunBracket` returns for the failover run and
   before `class = ClassifyAttempt(record, log)`.

`ProbeBackend` keeps its own call. A run that ends outside these moments stays unsaved and is
classified on each read by step 3 until the next settlement or loop start saves it.

**Step 5. Keep quota and infrastructure apart, so an authorized retry can pass (point 8).**

- `readShutBackends` in `tools/tillr-drain/drain/quota_window.go` goes back to the trunk's body:
  windows only, no call to `readInfrastructureClosed`.
- In `tools/tillr-drain/drain/quota_probe.go`, rename the existing `readQuotaShutBackends` to
  `readQuotaOnlyShut`. Add a new `readQuotaShutBackends` with the old signature that calls
  `readQuotaOnlyShut` and then marks every backend `readInfrastructureClosed` reports closed. Add
  `readDispatchShut(r, at, policy, d Decision)`, which answers `readQuotaOnlyShut` when
  `d.Action == ActionRetry` and `readQuotaShutBackends` otherwise.
- In `planDefinition` in `tools/tillr-drain/drain/plan.go`, read `readQuotaOnlyShut` beside the
  existing `shut` and give the planner a `quotaShut` field. In `decide`, where the backend is
  chosen:

  ```go
  shut := p.shut
  if retry != nil && retry.refusal == "" && !p.quotaShut[attempt.backend] {
      shut = p.quotaShut
  }
  next, degraded, ok := p.policy.availableBackend(attempt.backend, item.Seat, shut)
  ```

- In `tools/tillr-drain/drain/pass.go`, the claim re-reads the shut set in two places: before
  `dispatchQuotaOpen`, and before the `selectSeatDispatch` loop over `parallelNodes(d)`. Both
  call `readDispatchShut(..., d)` in place of `readQuotaShutBackends`. Without this the plan
  says retry, the claim refuses it, and the loop spins: `TestTheLoopDispatchesAnAuthorizedRetry`
  ran here until the suite's timeout.

**Step 6. A restart keeps its recorded request.** In `pendingDecision` in
`tools/tillr-drain/drain/pass.go`, the candidate rebuilds every restart's request from the
policy, which breaks `TestSeatSharesWorkflowRestartKeepsRecordedBackend`. Keep the retained
request and reroute only when the walk chose another backend:

```go
if d.WorkflowRestart {
    retained := *d.RunRequest
    request = &retained
    if name != retained.Backend {
        request = rerouteNodeRequest(policy, d, name)
    }
}
```

**Step 7. Tests of the new rules.** In `tools/tillr-drain/drain/backend_recorded_test.go`:

- Replace `TestBackendAvailabilityUsesOnlySavedClass` with a test of point 6: after one
  infrastructure ending and no save, `readInfrastructureClosed` reports the backend closed and the
  store's event count is unchanged; after `RecordBackendOutcomes` it is still closed.
- In `TestSavedInfrastructureClassKeepsItemWaitingAfterReportEdit`, a plan no longer saves. After
  the first plan, assert that `backend_outcomes` holds no row, call `RecordBackendOutcomes`, and
  only then edit the report.

Restore `tools/tillr-drain/drain/readmit_test.go` to the trunk's version with `git checkout HEAD
--`. The candidate's added block expects a signal-ended run the loop orphaned to close its
backend, which point 7 rules out.

**Step 8. The ten tests.** Run them with the pattern below in `tools/tillr-drain`. After steps 1
to 7, nine pass and `TestPlanFailsAListedSeatOverAlongItsOwnList` fails on its last assertion,
which step 9 changes.

```text
TestPlanHoldsWhenEveryBackendIsClosed|TestAPassDefersChangedItemRunAndLeaseWithoutWriting|TestProbeBackendFaceRecordsSuccessAndFailure|TestWorkflowRestartRecoversSignalRecordWithoutStopMarker|TestQuotaContinuationWaitsAndReopensOriginalBackend|TestLoopSkipsSpentWindowAcrossRestartAndReopens|TestReturnedWorkerGoneHarnessSession|TestReturnedWorkflowResumesProducingNode|TestTheLoopDispatchesAnAuthorizedRetry|TestPlanFailsAListedSeatOverAlongItsOwnList
```

**Step 9. The three base tests.**

- `TestPlanFailsAListedSeatOverAlongItsOwnList`: the last assertion becomes a hold with
  `drain.ReasonBackendClosed` and `DeferredBy == "designer-a, designer-b"`.
- `TestLoopDoesNotSkipBackendWithoutWindowFacts`: the failure still records no window, and it now
  closes `pi`. `item_051` has two runs, `pi` then `fallback`. `item_052` has one run, on
  `fallback`.
- `TestPlanTransitionPayloadCountsTerminalRuns`: its fixture fails both backends, so every item
  would hold. Add a last record, `runRecord("run-landed-second", "item_landed", "lane-7",
  "second", run.StateExited, exitCode(0), "")`, so that `second` is open. Plan with four lanes and
  a dispatch cap of four. `item_exhausted` becomes a dispatch from `queued` to `running` at
  attempt 3, and `item_landed` stays a hold, now at attempt 2. The other rows are unchanged.

**Step 10. The README.** `tools/tillr-drain/README.md` in the candidate has a section "Recorded
backend availability". Three of its statements are now wrong, and one is missing:

- Replace "The loop captures every newly observed terminal event's class before planning" with
  where the class is saved: when the loop settles a run and when a loop starts.
- Replace "Read-only views use only saved classes and probe results, never an unsaved
  classification" with point 6: a reader classifies an unsaved ending from the record and the log
  as they are, and does not save.
- State which infrastructure endings do not close a backend: a run the loop interrupted, a
  refusal with a stated reset time, and a resumed session that is gone.
- Beside the probe, say that a retry the owner authorized is the only dispatch to a closed
  backend.

**Step 11. Mirror, full suites, commit.** Run `just pack-sync`. Run the full suites with the quiet
form above: `go test ./... -count=1 -timeout 3000s` in `tools/tillr-drain` and in
`tools/tillr-view`, or the sharded runner on each. Run `python3 tests/tillr-muse-backend.test.py`.
Then make the one commit.

**What the plan measured.** Steps 1 to 9 and 11 were applied to a copy of the tree at `2acf66dc4`
on 2026-11-18; step 10's wording was not. Results there:

- The ten tests of step 8, the tests of step 7 and the three of step 9 passed.
- `go test ./... -count=1 -timeout 3000s -skip TestTheComposedPipelineRunsEndToEndOnAFixture` in
  `tools/tillr-drain` printed `ok` for every package with tests: the root package in 576 seconds
  and `drain` in 890 seconds.
- `go test ./... -count=1` in `tools/tillr-view` printed `ok` in 62 seconds.
- `python3 tests/tillr-muse-backend.test.py` exited 0.
- `TestTheComposedPipelineRunsEndToEndOnAFixture` failed in that copy with `gate:python3: watchdog
  unavailable: kernel task-clock counter: permission denied`, and passed in the lane worktree at
  `2acf66dc4` with none of this work applied. The message comes from `tools/tillr-gate`, which
  this item does not touch, and the copy was not a git worktree of this repository. The builder
  runs that one test in its lane with the work applied. If it fails there with the same message,
  run it at the base as well: red at the base is a `trunk` obstacle naming that test, and red only
  with the work applied is this item's to fix.
- `TestRestartReplacesTheLoopInPlaceWithoutSignalingARun` failed once here while four other test
  processes were running, with `replacement asked the worker to pause`, and passed twice alone
  with the work applied and twice alone at the base. Rerun it alone before treating it as a
  failure of this change.

**Fourth ruling, 2026-11-18 (answers the seventh run's conflict): the plan is carried out, and the
build applies it.** The seventh run did step 1 of the plan above correctly: it applied the fifth
run's tree and resolved the four conflicts as the table says. It then reported that the task
exceeds it and listed steps 3 to 11 as missing. The plan itself is sound. The design seat applied
steps 2 to 10 on top of the seventh run's tree in a lane at `6a75bf6dc` on 2026-11-18, exactly as
written, and the result is stated under "What this ruling measured" below. That work was then
discarded, because the design seat's commit is this document. What was kept was its difference
from the seventh run's tree, as a second patch. This ruling changes no criterion, no path and no
rule above.

- **The build order this ruling gave is replaced.** It applied the seventh run's preserved tree
  and then a second patch printed here. The ninth run did the first and not the second. The fifth
  ruling, at the head of this block, joins the two into the one patch at the end of the block,
  and its four steps are the build order. The second patch is no longer printed separately.
- **The drain test this ruling found red on the trunk is repaired.**
  `TestRestartGatesAndLandsTheRetiredHead` in `tools/tillr-drain/retire_test.go` failed at
  `6a75bf6dc` with none of this work applied. It passes at `b045866f2`, with and without the work.
- **What this ruling measured,** in the lane at `6a75bf6dc` on 2026-11-18 with steps 1 and 2
  applied and the mirror regenerated:
  - Before the three base tests of step 9 were changed, the ten tests of step 8 gave the result
    the third ruling predicts: nine passed and `TestPlanFailsAListedSeatOverAlongItsOwnList`
    failed on its last assertion. `TestLoopDoesNotSkipBackendWithoutWindowFacts` and
    `TestPlanTransitionPayloadCountsTerminalRuns` failed as well, as that ruling says they do.
    After step 9 all three passed.
  - `python3 tools/go-test-shards.py tools/tillr-drain -k 6` failed in one test,
    `TestRestartGatesAndLandsTheRetiredHead`, which is the trunk's failure described above. No
    other test failed.
  - `python3 tools/go-test-shards.py tools/tillr-view -k 6` printed `504 tests passed`.
  - `python3 tests/tillr-muse-backend.test.py` and `python3 tests/tillr-docs.test.py` exited 0.
  - `TestTheComposedPipelineRunsEndToEndOnAFixture` passed in the lane with the work applied, so
    the third ruling's caution about it is closed.

The patch for step 1 of the build order at the head of this block. The two marker lines are not
part of it, and the command in step 1 leaves them out. It was made in the lane at `b045866f2` on
2026-11-18 with `git diff --cached HEAD -- config tests tools`, after the seventh run's tree and
the fourth ruling's patch were applied there and the suites above had passed on the result.

````diff
# backend-open-patch begin
diff --git a/config/tillr/backend-policy.bedrock.json b/config/tillr/backend-policy.bedrock.json
index 9e68d151f..8dad9123c 100644
--- a/config/tillr/backend-policy.bedrock.json
+++ b/config/tillr/backend-policy.bedrock.json
@@ -4,7 +4,6 @@
   "preference": [
     "pi"
   ],
-  "max_failovers": 1,
   "seats": {
     "design": "pi-designer",
     "build": "pi-build",
diff --git a/config/tillr/backend-policy.claude.json b/config/tillr/backend-policy.claude.json
index 73083fc2b..76acf2241 100644
--- a/config/tillr/backend-policy.claude.json
+++ b/config/tillr/backend-policy.claude.json
@@ -5,7 +5,6 @@
     "claude",
     "pi"
   ],
-  "max_failovers": 1,
   "seats": {
     "design": ["claude-designer", "pi-designer"],
     "build": ["claude", "pi"],
diff --git a/config/tillr/backend-policy.json b/config/tillr/backend-policy.json
index 98fe6ba66..ce8589c5f 100644
--- a/config/tillr/backend-policy.json
+++ b/config/tillr/backend-policy.json
@@ -4,7 +4,6 @@
   "preference": [
     "pi"
   ],
-  "max_failovers": 1,
   "seats": {
     "design": "pi-designer",
     "build": "pi",
diff --git a/config/tillr/backend-policy.mixed.json b/config/tillr/backend-policy.mixed.json
index 8f09fed57..a55cbb70e 100644
--- a/config/tillr/backend-policy.mixed.json
+++ b/config/tillr/backend-policy.mixed.json
@@ -4,7 +4,6 @@
   "preference": [
     "pi"
   ],
-  "max_failovers": 1,
   "seats": {
     "design": [
       "claude-designer",
diff --git a/tests/tillr-muse-backend.test.py b/tests/tillr-muse-backend.test.py
index fb8246de9..86318ff33 100644
--- a/tests/tillr-muse-backend.test.py
+++ b/tests/tillr-muse-backend.test.py
@@ -473,8 +473,8 @@ def test_policy():
         "agy: an effort key, but its effort lives inside the model id and the "
         "adapter refuses --effort",
     )
-    # D-MM11: failover walks one ordered preference whose head is the default, bounded by
-    # max_failovers; a backend with an ordered model list starts at its own `model`.
+    # Failover walks an ordered preference, skipping backends closed by run facts.
+    # No policy carries the retired dispatch budget.
     preference = policy.get("preference", [])
     check(preference, "the policy names no failover preference")
     check(
@@ -485,10 +485,8 @@ def test_policy():
         all(name in backends for name in preference),
         f"the preference {preference} names a backend with no entry",
     )
-    check(
-        policy.get("max_failovers") == 1,
-        "max_failovers is not 1: an item could be dispatched past the one bounded failover",
-    )
+    for path in (ROOT / "config/tillr").glob("backend-policy*.json"):
+        check("max_failovers" not in json.loads(path.read_text()), f"{path.name}: retired max_failovers key")
     for name, entry in backends.items():
         if "models" in entry:
             check(
@@ -533,7 +531,7 @@ def test_claude_policy(policy):
     backends = policy["backends"]
     check(policy["default_backend"] == "claude", f"the Claude default is {policy['default_backend']!r}")
     check(policy.get("preference") == ["claude", "pi"], f"the Claude preference is {policy.get('preference')}")
-    check(policy.get("max_failovers") == 1, "the Claude policy's max_failovers is not 1")
+    check("max_failovers" not in policy, "the Claude policy names the retired max_failovers key")
     check(
         policy.get("seats") == {
             "design": ["claude-designer", "pi-designer"],
diff --git a/tools/tillr-drain/README.md b/tools/tillr-drain/README.md
index 91a40a70a..f4d5e9345 100644
--- a/tools/tillr-drain/README.md
+++ b/tools/tillr-drain/README.md
@@ -404,7 +404,8 @@ The command runs through `tillr run exec` in the item's existing worktree with
 backend `command`, no model route, no model spend, and no backend quota window.
 Exit 0 passes and exit 1 fails, irrespective of any report footer. Other exits,
 a program that cannot start, and an explicit `timeout_ms` wall hold the item as
-`run-infra-exhausted`, without backend failover. An omitted wall imposes none.
+`command-fault`, without backend failover. Repair the command process and
+explicitly retry the node. An omitted wall imposes none.
 A failed command follows a matching `when_outcome: fail` edge if one is ready;
 otherwise the item is held as `node-failed: <node>`, later nodes and the gate do
 not run, and the work is preserved for inspection.
@@ -1130,12 +1131,72 @@ counts them; the removed rule spent one fix run per candidate and then held the
 returned at the next loop start. A conflict's evidence key (`conflictEndingKey` in
 [`drain/returned.go`](./drain/returned.go)) is its sorted conflicting paths and the digest
 of its normalized hunks, so the same conflict against another trunk sha repeats and a trunk
-that moved again inside the hunk does not. A workflow node's refusal keeps its node rule. Machinery failures keep the one failover and `run-infra-exhausted`: the owner ruled
-that each should move to the next open backend and wait when none is open, but the drain
-has no fact that says a backend is open again except a quota window, so a walk with no
-budget would re-dispatch a broken adapter at once and forever. That is the queue item
-`item-backend-open-is-a-fact` in
-[the tillr roadmap](../../docs/tillr/roadmap.md#route-an-infrastructure-failure-to-the-next-open-backend).
+that moved again inside the hunk does not. A workflow node's refusal keeps its node rule.
+Machinery failures follow [recorded backend availability](#recorded-backend-availability),
+not a dispatch count.
+
+### Recorded backend availability
+
+An infrastructure failure means the harness could not produce a judgable answer:
+it failed to start, refused before running, or died without a report. The latest
+terminal run on a backend closes that backend only in this class. A later run
+ending in any other class reopens it. Three infrastructure endings are answered
+by another rule and do not close a backend: a run the loop interrupted itself
+(a stop, an orphaned run or a stale base), a refusal with a stated reset time
+(the quota window closes and reopens that backend), and a resumed session that
+is gone (a fresh session replaces it). The loop records the class once, from the
+terminal event and its pinned evidence, in `backend_outcomes` and a
+`drain.backend-outcome` event. It orders terminal events, not mutable run-row
+metadata: spend corrections do not count as new outcomes, and deleting or changing
+a report after classification cannot change availability. These facts include runs
+from other items and judge runs. A restart, policy edit, elapsed time, or successful
+quota reading is not evidence that a broken harness works again.
+
+The loop saves the class where it settles a run, and when a loop starts for
+endings that arrived while none was running. A reader that finds an ending with
+no saved class (`plan`, `why` or the view, before the next save) classifies it
+from the record and the log as they are, and does not save it. Once a class is
+saved it wins over the report.
+This migration does not require an operator to certify an outcome or run a probe.
+
+After a failure, dispatch walks the item's backends in order, wrapping at the end
+and skipping closed backends. A multi-backend seat stays within its own list;
+a single-backend seat keeps its existing fallback to the policy preference.
+When every allowed backend is unavailable, the item waits. An infrastructure hold
+is `backend-closed`, with the closed backend names in `deferred_by` and the mover
+shown by `why` and `waiting`: a successful explicit probe or a later run ending
+outside the infrastructure class. Quota holds remain independent; reopening the
+harness does not erase an unexpired quota refusal. No automatic probe or timer
+spends work on a closed backend. Legacy `max_failovers` policy keys are accepted
+but ignored; `run-infra-exhausted` is readable only as history.
+
+An operator can exercise the repaired backend with a smoke-check command and
+record the result:
+
+```bash
+tillr-drain probe-backend --store .tillr/store.sqlite3 --backend <name> -- <smoke-check-command> <args>
+```
+
+Use a command that actually exercises that named backend's harness and route,
+not a quota query or a command that merely exits zero. Exit zero records a
+successful `drain.backend-probe` event and reopens the backend; every other exit
+records a failed probe and leaves it closed. Output is discarded to keep credentials
+out of recorded state. The probe does not dispatch an item or consume its criteria.
+Its cost is the command's cost: a harness/model smoke check can spend provider tokens
+and quota, while the event write itself spends no model tokens. It is explicit,
+never automatic. A retry the owner authorized is the only dispatch to a closed
+backend: it starts once on the item's own backend, reopens that backend if it
+ends in any other class, and leaves it closed if it fails in the infrastructure
+class again. A probe completed before a later failed run cannot reopen it.
+A report changed or deleted before the drain first reads the ended run can affect
+that first classification; capturing the class at run completion is outside this
+mechanism, and a successful probe or a later recorded run corrects availability.
+
+A workflow failure retains the same node, attempt, base, and completed siblings.
+Its ready continuation waits on the same backend facts, then walks the same order
+when a backend reopens rather than faulting the graph or restarting earlier nodes.
+The implementation lives in `readInfrastructureClosed`, `ProbeBackend`, and the
+workflow continuation planner in the drain package.

 ### Owner decisions in documents

diff --git a/tools/tillr-drain/backend_probe.go b/tools/tillr-drain/backend_probe.go
new file mode 100644
index 000000000..51195dd29
--- /dev/null
+++ b/tools/tillr-drain/backend_probe.go
@@ -0,0 +1,44 @@
+package main
+
+import (
+	"context"
+	"errors"
+	"flag"
+	"fmt"
+	"os"
+	"os/signal"
+	"syscall"
+
+	"github.com/mschulkind-oss/tillr-drain/drain"
+	"github.com/mschulkind-oss/tillr/record"
+)
+
+func probeBackendFace(args []string) int {
+	fs := newFlagSet("probe-backend")
+	path := fs.String("store", "", "store holding backend run history (required)")
+	backend := fs.String("backend", "", "backend exercised by the smoke-check command (required)")
+	if err := fs.Parse(args); err != nil {
+		if errors.Is(err, flag.ErrHelp) {
+			printHelp(fs)
+			return record.ExitOK
+		}
+		return fail(drain.Usagef("%v", err))
+	}
+	if *path == "" || *backend == "" || fs.NArg() == 0 {
+		return fail(drain.Usagef("probe-backend needs --store <path>, --backend <name> -- <smoke-check command>"))
+	}
+	if _, err := os.Stat(*path); err != nil {
+		return fail(openFailure(err))
+	}
+	root, err := os.Getwd()
+	if err != nil {
+		return fail(err)
+	}
+	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
+	defer cancel()
+	if err := drain.ProbeBackend(ctx, *path, root, *backend, fs.Args()); err != nil {
+		return fail(err)
+	}
+	fmt.Printf("backend %s: successful probe recorded\n", *backend)
+	return record.ExitOK
+}
diff --git a/tools/tillr-drain/backend_probe_test.go b/tools/tillr-drain/backend_probe_test.go
new file mode 100644
index 000000000..54b6e569c
--- /dev/null
+++ b/tools/tillr-drain/backend_probe_test.go
@@ -0,0 +1,30 @@
+package main_test
+
+import (
+	"github.com/mschulkind-oss/tillr-drain/drain"
+	"github.com/mschulkind-oss/tillr-run/run"
+	"testing"
+)
+
+func TestProbeBackendFaceRecordsSuccessAndFailure(t *testing.T) {
+	path := fixture(t, itemRecord("item_042", "in-queue"), runRecord("broken", "item_042", "lane-9", "first", run.StateExited, exitCode(2), ""))
+	tickPath := tick(t, "2026-11-18T00:00:00Z", []string{"lane-1"}, 1, policyDoc(t, map[string]any{"default_backend": "first", "preference": []string{"first"}, "backends": map[string]any{"first": map[string]any{"adapter": "/bin/true", "model": "route"}}}))
+	if got := plan(t, path, tickPath)[0].Payload; got.Reason != drain.ReasonBackendClosed {
+		t.Fatalf("before probe: %+v", got)
+	}
+	if code, out := invoke(t, "probe-backend", "--store", path, "--backend", "first", "--", "/bin/false"); code == 0 {
+		t.Fatalf("failed probe passed: %s", out)
+	}
+	if got := plan(t, path, tickPath)[0].Payload; got.Action != drain.ActionHold {
+		t.Fatalf("failed probe reopened: %+v", got)
+	}
+	if code, out := invoke(t, "probe-backend", "--store", path, "--backend", "first", "--", "/bin/true"); code != 0 {
+		t.Fatalf("successful probe refused: %d %s", code, out)
+	}
+	if got := plan(t, path, tickPath)[0].Payload; got.Action != drain.ActionDispatch {
+		t.Fatalf("successful probe did not reopen: %+v", got)
+	}
+	if code, out := invoke(t, "probe-backend", "--help"); code != 0 {
+		t.Fatalf("probe help: %d %s", code, out)
+	}
+}
diff --git a/tools/tillr-drain/drain/backend_availability.go b/tools/tillr-drain/drain/backend_availability.go
new file mode 100644
index 000000000..9fc400da1
--- /dev/null
+++ b/tools/tillr-drain/drain/backend_availability.go
@@ -0,0 +1,150 @@
+package drain
+
+import (
+	"context"
+	"io"
+	"os/exec"
+	"strings"
+	"time"
+
+	"github.com/mschulkind-oss/tillr-run/run"
+	"github.com/mschulkind-oss/tillr/store"
+)
+
+const backendProbeKind = "drain.backend-probe"
+
+// readInfrastructureClosed projects availability from recorded facts. A run's
+// terminal event and an explicit successful probe share the store's event order;
+// a clock, policy edit, restart, or quota reading cannot reopen infrastructure.
+func readInfrastructureClosed(r *store.Reader) (map[string]bool, error) {
+	outcomes, err := readBackendOutcomes(r)
+	if err != nil {
+		return nil, err
+	}
+	closed := map[string]bool{}
+	sequences := map[string]int64{}
+	for _, o := range outcomes {
+		name := o.rec.Backend
+		if _, seen := sequences[name]; seen {
+			continue
+		}
+		// The latest ending decides. A saved ending keeps its saved fact; an
+		// unsaved one was classified by the read above, which saves nothing.
+		sequences[name] = o.seq
+		closed[name] = o.closes
+	}
+	probes, err := r.Query(`SELECT object_id,MAX(seq) FROM events WHERE kind=? AND json_extract(payload,'$.success')=1 GROUP BY object_id`, backendProbeKind)
+	if err != nil {
+		return nil, err
+	}
+	defer probes.Close()
+	for probes.Next() {
+		var backend string
+		var seq int64
+		if err := probes.Scan(&backend, &seq); err != nil {
+			return nil, err
+		}
+		if seq > sequences[backend] {
+			delete(closed, backend)
+		}
+	}
+	return closed, probes.Err()
+}
+
+// ProbeBackend runs an operator-chosen smoke check, not a quota reading or a
+// timer. Its exit is recorded even on failure; only exit zero reopens a backend.
+// Output is discarded so credentials cannot enter the event log.
+func ProbeBackend(ctx context.Context, path, root, backend string, argv []string) error {
+	if strings.TrimSpace(backend) == "" || backend == "command" || len(argv) == 0 || argv[0] == "" {
+		return Usagef("probe-backend needs --store, --backend and a smoke-check command after --")
+	}
+	if err := RecordBackendOutcomes(ctx, path); err != nil {
+		return err
+	}
+	s, err := store.Open(ctx, path, true)
+	if err != nil {
+		return err
+	}
+	defer s.Close()
+	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
+	cmd.Dir = root
+	cmd.Stdout = io.Discard
+	cmd.Stderr = io.Discard
+	probeErr := runCheckGroup(cmd)
+	err = s.WriteTx(ctx, func(tx *store.Tx) error {
+		return appendEffect(tx, backendProbeKind, backend, struct {
+			Success bool `json:"success"`
+		}{probeErr == nil}, time.Now().UTC())
+	})
+	if err != nil {
+		return err
+	}
+	if probeErr != nil {
+		return Externalf("backend %s probe failed: %v; backend remains closed", backend, probeErr)
+	}
+	return nil
+}
+
+// A mixed quota/infrastructure hold names the closed backends on this route and
+// their mover. The walk uses the same seat boundary as dispatch.
+func describeBackendHold(r *store.Reader, d *Decision, policy *Policy) error {
+	if d.Action != ActionHold || d.Reason != ReasonBackendWindow || policy == nil {
+		return nil
+	}
+	closed, err := readInfrastructureClosed(r)
+	if err != nil {
+		return err
+	}
+	start := policy.firstBackend(itemRow{ItemID: d.ItemID, Seat: d.Seat})
+	if d.RunRequest != nil {
+		start = d.RunRequest.Backend
+	}
+	if d.DeferredBy != "" {
+		start = d.DeferredBy
+	} else if len(d.Parallel) > 0 {
+		for _, node := range parallelNodes(*d) {
+			if node.RunRequest == nil || !closed[node.RunRequest.Backend] {
+				continue
+			}
+			rec, err := terminalRecord(r, node.RunRequest.RunID)
+			if err != nil {
+				return err
+			}
+			if rec == nil {
+				start, d.Seat = node.RunRequest.Backend, node.Seat
+				break
+			}
+		}
+	}
+	names := []string{}
+	seen := map[string]bool{}
+	for name := start; name != "" && !seen[name]; {
+		seen[name] = true
+		if closed[name] {
+			names = append(names, name)
+		}
+		next, _, ok := policy.nextBackend(name, d.Seat)
+		if !ok {
+			break
+		}
+		name = next
+	}
+	d.DeferredBy = ""
+	if len(names) > 0 {
+		d.Reason = ReasonBackendClosed
+		d.DeferredBy = strings.Join(names, ", ")
+		identifyDecision(d)
+	}
+	return nil
+}
+
+// Retain node identity, timeout, and parallel-group provenance while changing
+// only the backend's execution fields.
+func rerouteNodeRequest(policy *Policy, d Decision, name string) *run.Request {
+	request := *policy.request(d.ItemID, d.LaneID, d.RunRequest.RunID, name)
+	request.TimeoutMS = d.RunRequest.TimeoutMS
+	request.ReadOnlyGroup = d.RunRequest.ReadOnlyGroup
+	request.NodeID = d.RunRequest.NodeID
+	request.CorrelationID = d.RunRequest.CorrelationID
+	return &request
+}
diff --git a/tools/tillr-drain/drain/backend_availability_test.go b/tools/tillr-drain/drain/backend_availability_test.go
new file mode 100644
index 000000000..6a42c145b
--- /dev/null
+++ b/tools/tillr-drain/drain/backend_availability_test.go
@@ -0,0 +1,169 @@
+package drain
+
+import (
+	"context"
+	"os"
+	"path/filepath"
+	"testing"
+	"time"
+
+	"github.com/mschulkind-oss/tillr-run/run"
+	"github.com/mschulkind-oss/tillr/store"
+)
+
+func backendRun(t *testing.T, f refusalLoopFixture, id, backend string, exit int) {
+	t.Helper()
+	fixtureWrite(t, f, func(tx *store.Tx) error {
+		return applyTerminal(tx, run.Record{RunID: id, ItemID: "item-a", LaneID: "lane-1", Backend: backend, State: run.StateExited, ExitCode: code(exit), StartedAt: "2026-11-18T00:00:00Z", FinishedAt: "2026-11-18T00:00:01Z"})
+	})
+}
+
+func TestBackendInfrastructureClosureAndRecordedReopening(t *testing.T) {
+	for _, event := range []string{"run", "probe"} {
+		t.Run(event, func(t *testing.T) {
+			f, l := passFixture(t, "item-a")
+			backendRun(t, f, "broken", "fixture", 2)
+			d := mustPlan(t, l)[0].decision
+			if d.Action != ActionHold || d.Reason != ReasonBackendClosed || d.DeferredBy != "fixture" {
+				t.Fatalf("closed backend dispatched: %+v", d)
+			}
+			// Neither a later clock nor reconstructing a loop is a reopening event.
+			l.cfg.Now = func() time.Time { return time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC) }
+			if got := mustPlan(t, l)[0].decision; got.Action != ActionHold {
+				t.Fatalf("clock reopened backend: %+v", got)
+			}
+			if event == "run" {
+				backendRun(t, f, "answered", "fixture", 0)
+			} else {
+				if err := ProbeBackend(context.Background(), f.storePath, f.root, "fixture", []string{"/bin/false"}); err == nil {
+					t.Fatal("failed probe accepted")
+				}
+				if got := mustPlan(t, l)[0].decision; got.Reason != ReasonBackendClosed {
+					t.Fatalf("failed probe reopened: %+v", got)
+				}
+				if err := ProbeBackend(context.Background(), f.storePath, f.root, "fixture", []string{"/bin/true"}); err != nil {
+					t.Fatal(err)
+				}
+			}
+			// Inspect availability directly: an answered item may independently wait for a retry.
+			if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
+				t.Fatal(err)
+			}
+			fixtureWrite(t, f, func(tx *store.Tx) error {
+				closed, err := readInfrastructureClosed(tx.Reader())
+				if err == nil && closed["fixture"] {
+					t.Error("recorded event did not reopen backend")
+				}
+				return err
+			})
+			if event == "probe" && mustPlan(t, l)[0].decision.Action != ActionDispatch {
+				t.Fatal("probe did not release item")
+			}
+			backendRun(t, f, "broken-again", "fixture", 2)
+			if got := mustPlan(t, l)[0].decision; got.Reason != ReasonBackendClosed {
+				t.Fatalf("old reopening event won over new failure: %+v", got)
+			}
+		})
+	}
+}
+
+func TestBackendWalkWrapsAndKeepsSeat(t *testing.T) {
+	p := Policy{Preference: []string{"a", "b", "c"}, SeatLists: map[string][]string{"design": {"a", "b"}}}
+	name, left, ok := p.availableBackend("c", "", map[string]bool{"c": true})
+	if !ok || name != "a" || left != "" {
+		t.Fatalf("preference did not wrap: %s %s %v", name, left, ok)
+	}
+	name, left, ok = p.availableBackend("b", "design", map[string]bool{"b": true})
+	if !ok || name != "a" || left != "" {
+		t.Fatalf("seat did not wrap: %s %s %v", name, left, ok)
+	}
+	if _, _, ok := p.availableBackend("b", "design", map[string]bool{"a": true, "b": true}); ok {
+		t.Fatal("escaped closed seat")
+	}
+}
+
+func TestWorkflowBackendFailureWaitsAndReopensSameNode(t *testing.T) {
+	f, l, _ := workflowLoopFixture(t, "")
+	p := mustPlan(t, l)[0]
+	if ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now()); err != nil || !ok {
+		t.Fatalf("claim: %v %v", ok, err)
+	}
+	d := p.decision
+	backendRun(t, f, d.RunRequest.RunID, "fixture", 2)
+	if err := l.retryWorkflowNode(context.Background(), d, runGit(t, f.root, "rev-parse", TrunkRef), run.Record{RunID: d.RunRequest.RunID, Backend: "fixture", State: run.StateExited, ExitCode: code(2)}, &LoopResult{}); err != nil {
+		t.Fatal(err)
+	}
+	wait := mustPlan(t, l)[0].decision
+	if wait.Action != ActionHold || wait.Reason != ReasonBackendClosed || wait.NodeIndex != d.NodeIndex {
+		t.Fatalf("node did not wait: %+v", wait)
+	}
+	if err := ProbeBackend(context.Background(), f.storePath, f.root, "fixture", []string{"/bin/true"}); err != nil {
+		t.Fatal(err)
+	}
+	next := mustPlan(t, l)[0].decision
+	if next.Action != ActionDispatch || next.NodeIndex != d.NodeIndex || next.Attempt != d.Attempt {
+		t.Fatalf("node not resumed: %+v", next)
+	}
+}
+
+func TestBackendAvailabilityIgnoresMetadataAndReportEdits(t *testing.T) {
+	f, l := passFixture(t, "item-a")
+	report := filepath.Join(f.root, "answer.md")
+	writeForkFile(t, report, "RESULT: failed\n")
+	fixtureWrite(t, f, func(tx *store.Tx) error {
+		return applyTerminal(tx, run.Record{RunID: "answered", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: code(1), StartedAt: "2026-11-18T00:00:00Z", FinishedAt: "2026-11-18T00:00:01Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}}})
+	})
+	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
+		t.Fatal(err)
+	}
+	check := func(want bool) {
+		t.Helper()
+		fixtureWrite(t, f, func(tx *store.Tx) error {
+			closed, err := readInfrastructureClosed(tx.Reader())
+			if err == nil && closed["fixture"] != want {
+				t.Errorf("closed=%v, want %v", closed, want)
+			}
+			return err
+		})
+	}
+	check(false)
+	if err := os.Remove(report); err != nil {
+		t.Fatal(err)
+	}
+	check(false)
+	writeForkFile(t, report, "stream without a footer\n")
+	check(false)
+	backendRun(t, f, "broken", "fixture", 2)
+	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
+		t.Fatal(err)
+	}
+	check(true)
+	fixtureWrite(t, f, func(tx *store.Tx) error {
+		_, err := tx.Exec(`UPDATE runs SET event_seq=999999 WHERE run_id='answered'`)
+		return err
+	})
+	check(true)
+	if got := mustPlan(t, l)[0].decision; got.Reason != ReasonBackendClosed {
+		t.Fatalf("metadata reopened backend: %+v", got)
+	}
+}
+
+func TestBackendHoldNamesOnlyThePostFailureWalk(t *testing.T) {
+	f, l := passFixture(t, "item-a")
+	b := l.cfg.Policy.Backends["fixture"]
+	l.cfg.Policy.Backends["override"] = b
+	l.cfg.Policy.Backends["alternate"] = b
+	l.cfg.Policy.Preference = []string{"fixture", "alternate"}
+	l.cfg.Policy.Overrides = map[string]string{"item-a": "override"}
+	backendRun(t, f, "override-failed", "override", 2)
+	// Other items close the route without changing this item's failover start.
+	for _, backend := range []string{"fixture", "alternate"} {
+		fixtureWrite(t, f, func(tx *store.Tx) error {
+			return applyTerminal(tx, run.Record{RunID: backend + "-failed", ItemID: "item-b", LaneID: "lane-1", Backend: backend, State: run.StateExited, ExitCode: code(2), StartedAt: "2026-11-18T00:00:00Z", FinishedAt: "2026-11-18T00:00:01Z"})
+		})
+	}
+	d := mustPlan(t, l)[0].decision
+	if d.Reason != ReasonBackendClosed || d.DeferredBy != "fixture, alternate" {
+		t.Fatalf("hold named an unreachable reopening: %+v", d)
+	}
+}
diff --git a/tools/tillr-drain/drain/backend_outcomes.go b/tools/tillr-drain/drain/backend_outcomes.go
new file mode 100644
index 000000000..703f1325e
--- /dev/null
+++ b/tools/tillr-drain/drain/backend_outcomes.go
@@ -0,0 +1,195 @@
+package drain
+
+import (
+	"context"
+	"encoding/json"
+	"strings"
+	"time"
+
+	"github.com/mschulkind-oss/tillr-run/run"
+	"github.com/mschulkind-oss/tillr/store"
+)
+
+// Terminal classifications are captured once, keyed to the immutable run.finish
+// event, never the mutable runs.event_seq (usage corrections advance that field).
+// class is what ClassifyAttempt answered, which routing reads; closes is the
+// availability fact: an infrastructure ending that no other rule answers.
+const backendOutcomesDDL = `CREATE TABLE IF NOT EXISTS backend_outcomes (
+ terminal_seq INTEGER PRIMARY KEY REFERENCES events(seq),
+ class TEXT NOT NULL,
+ closes INTEGER NOT NULL DEFAULT 0
+);`
+
+const backendOutcomeKind = "drain.backend-outcome"
+
+type backendOutcome struct {
+	seq    int64
+	rec    run.Record
+	class  AttemptClass
+	closes bool
+	// saved says the class and closes were read from backend_outcomes. An unsaved
+	// ending is classified in memory by whoever reads it; only the loop saves.
+	saved bool
+}
+
+// readBackendOutcomes answers every agent run ending, newest first. A saved
+// ending keeps its saved facts; an unsaved one is classified from the record and
+// the log as they are now. It writes nothing, so a read-only face may call it.
+func readBackendOutcomes(r *store.Reader) ([]backendOutcome, error) {
+	rows, err := r.Query(`SELECT e.seq,e.payload,COALESCE(o.class,''),COALESCE(o.closes,0) FROM events e
+ LEFT JOIN backend_outcomes o ON o.terminal_seq=e.seq WHERE e.kind=? ORDER BY e.seq DESC`, run.FinishKind)
+	if err != nil && strings.Contains(err.Error(), "no such table: backend_outcomes") {
+		rows, err = r.Query(`SELECT seq,payload,'',0 FROM events WHERE kind=? ORDER BY seq DESC`, run.FinishKind)
+	}
+	if err != nil {
+		return nil, err
+	}
+	out := []backendOutcome{}
+	unsaved := false
+	for rows.Next() {
+		var o backendOutcome
+		var raw string
+		if err := rows.Scan(&o.seq, &raw, &o.class, &o.closes); err != nil {
+			rows.Close()
+			return nil, err
+		}
+		if err := json.Unmarshal([]byte(raw), &o.rec); err != nil {
+			rows.Close()
+			return nil, err
+		}
+		if o.rec.Backend == "command" {
+			continue
+		}
+		o.saved = o.class != ""
+		unsaved = unsaved || !o.saved
+		out = append(out, o)
+	}
+	err = rows.Err()
+	rows.Close()
+	if err != nil || !unsaved {
+		return out, err
+	}
+	interrupted, _, err := readInterruptedRuns(r)
+	if err != nil {
+		return nil, err
+	}
+	_, byRun, err := readPendingHistory(r)
+	if err != nil {
+		return nil, err
+	}
+	for i := range out {
+		if out[i].saved {
+			continue
+		}
+		rec := out[i].rec
+		out[i].class = ClassifyAttempt(rec, logTail(rec.ArtifactRefs))
+		out[i].closes = out[i].class == AttemptInfrastructure && !answeredByAnotherRule(rec, interrupted, byRun)
+	}
+	return out, nil
+}
+
+// answeredByAnotherRule says an infrastructure-class ending is not evidence that
+// its backend is broken: the loop interrupted the run itself, the provider
+// refused it with a stated reset time (the quota window closes and reopens that
+// backend), or the session it resumed is gone (a fresh session replaces it).
+func answeredByAnotherRule(rec run.Record, interrupted map[string]bool, byRun map[string]*pendingWork) bool {
+	if interrupted[rec.RunID] {
+		return true
+	}
+	if _, _, reset, ok := refusedWindow(rec); ok && !reset.IsZero() {
+		return true
+	}
+	work := byRun[rec.RunID]
+	if work == nil {
+		return false
+	}
+	decision := work.Decision
+	for _, node := range parallelNodes(work.Decision) {
+		if node.RunRequest != nil && node.RunRequest.RunID == rec.RunID {
+			decision = node
+			break
+		}
+	}
+	gone, err := failedDesignerResume(decision, rec)
+	return err == nil && gone
+}
+
+// readInterruptedRuns answers the runs whose latest segment the loop ended
+// itself. An interruption describes the segment it ended. A later continuation
+// of the same run (run.resume) is that run's new segment, and its outcome is judged.
+func readInterruptedRuns(r *store.Reader) (interrupted, restaged map[string]bool, err error) {
+	interrupted, restaged = map[string]bool{}, map[string]bool{}
+	events, err := r.Events(0)
+	if err != nil {
+		return nil, nil, err
+	}
+	for _, e := range events {
+		if e.ObjectID == nil {
+			continue
+		}
+		switch e.Kind {
+		case loopOrphanedKind:
+			interrupted[*e.ObjectID] = true
+		case staleBaseKind:
+			// A stale base is the staging's failure, not the item's: like a loop stop,
+			// it ends the attempt without an outcome to hold the item on.
+			interrupted[*e.ObjectID], restaged[*e.ObjectID] = true, true
+		case run.ResumeKind:
+			delete(interrupted, *e.ObjectID)
+		}
+	}
+	return interrupted, restaged, nil
+}
+
+// RecordBackendOutcomes saves the class and the availability fact of every
+// ending not saved yet, including standalone and judge runs. The loop calls it
+// where it settles a run and when it starts; a saved ending survives a later
+// edit or loss of its report.
+func RecordBackendOutcomes(ctx context.Context, path string) error {
+	reader, err := store.OpenRead(ctx, path)
+	if err != nil {
+		return err
+	}
+	var outcomes []backendOutcome
+	err = reader.ReadTx(ctx, func(r *store.Reader) error { var err error; outcomes, err = readBackendOutcomes(r); return err })
+	reader.Close()
+	if err != nil {
+		return err
+	}
+	pending := []backendOutcome{}
+	for _, o := range outcomes {
+		if !o.saved {
+			pending = append(pending, o)
+		}
+	}
+	if len(pending) == 0 {
+		return nil
+	}
+	s, err := store.Open(ctx, path, true)
+	if err != nil {
+		return err
+	}
+	defer s.Close()
+	return s.WriteTx(ctx, func(tx *store.Tx) error {
+		for _, o := range pending {
+			inserted, err := tx.Exec(`INSERT OR IGNORE INTO backend_outcomes(terminal_seq,class,closes) VALUES (?,?,?)`, o.seq, o.class, o.closes)
+			if err != nil {
+				return err
+			}
+			count, err := inserted.RowsAffected()
+			if err != nil {
+				return err
+			}
+			if count > 0 {
+				if err := appendEffect(tx, backendOutcomeKind, o.rec.Backend, struct {
+					TerminalSeq int64        `json:"terminal_seq"`
+					Class       AttemptClass `json:"class"`
+					Closes      bool         `json:"closes"`
+				}{o.seq, o.class, o.closes}, time.Now().UTC()); err != nil {
+					return err
+				}
+			}
+		}
+		return nil
+	})
+}
diff --git a/tools/tillr-drain/drain/backend_recorded_test.go b/tools/tillr-drain/drain/backend_recorded_test.go
new file mode 100644
index 000000000..2fd4750a7
--- /dev/null
+++ b/tools/tillr-drain/drain/backend_recorded_test.go
@@ -0,0 +1,140 @@
+package drain
+
+import (
+	"context"
+	"path/filepath"
+	"testing"
+
+	"github.com/mschulkind-oss/tillr-run/run"
+	"github.com/mschulkind-oss/tillr/store"
+)
+
+func TestSavedInfrastructureClassKeepsItemWaitingAfterReportEdit(t *testing.T) {
+	f, l := passFixture(t, "item-a")
+	report := filepath.Join(f.root, "report.md")
+	writeForkFile(t, report, "dropped stream without footer\n")
+	fixtureWrite(t, f, func(tx *store.Tx) error {
+		return applyTerminal(tx, run.Record{RunID: "stream-failed", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: code(1), StartedAt: "2026-11-18T00:00:00Z", FinishedAt: "2026-11-18T00:00:01Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}}})
+	})
+	if got := mustPlan(t, l)[0].decision; got.Reason != ReasonBackendClosed {
+		t.Fatalf("failed stream did not wait: %+v", got)
+	}
+	// A plan only reads: the class is saved where the loop settles or starts.
+	fixtureWrite(t, f, func(tx *store.Tx) error {
+		var count int
+		if err := tx.Reader().QueryRow(`SELECT COUNT(*) FROM backend_outcomes`).Scan(&count); err != nil {
+			return err
+		}
+		if count != 0 {
+			t.Errorf("a plan saved %d classes, want none", count)
+		}
+		return nil
+	})
+	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
+		t.Fatal(err)
+	}
+	writeForkFile(t, report, "RESULT: failed\n")
+	if got := mustPlan(t, l)[0].decision; got.Reason != ReasonBackendClosed {
+		t.Fatalf("edited report displaced saved infrastructure class: %+v", got)
+	}
+	if err := ProbeBackend(context.Background(), f.storePath, f.root, "fixture", []string{"/bin/true"}); err != nil {
+		t.Fatal(err)
+	}
+	if got := mustPlan(t, l)[0].decision; got.Action != ActionDispatch {
+		t.Fatalf("probe did not release failed item: %+v", got)
+	}
+}
+
+// An ending nobody has saved is classified by whoever reads it, and the read
+// saves nothing: a plan made before the loop's first save still sees the closure.
+func TestUnsavedEndingClosesItsBackendWithoutAWrite(t *testing.T) {
+	f, _ := passFixture(t, "item-a")
+	backendRun(t, f, "not-observed", "fixture", 2)
+	events := func() (count int) {
+		fixtureWrite(t, f, func(tx *store.Tx) error {
+			return tx.Reader().QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count)
+		})
+		return count
+	}
+	before := events()
+	fixtureWrite(t, f, func(tx *store.Tx) error {
+		closed, err := readInfrastructureClosed(tx.Reader())
+		if !closed["fixture"] {
+			t.Error("unsaved infrastructure ending did not close its backend")
+		}
+		return err
+	})
+	if after := events(); after != before {
+		t.Errorf("reading availability wrote %d event(s)", after-before)
+	}
+	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
+		t.Fatal(err)
+	}
+	fixtureWrite(t, f, func(tx *store.Tx) error {
+		closed, err := readInfrastructureClosed(tx.Reader())
+		if !closed["fixture"] {
+			t.Error("saved infrastructure ending did not keep its backend closed")
+		}
+		return err
+	})
+}
+
+func TestBackendOutcomesCaptureEveryEndedRunOnce(t *testing.T) {
+	f, _ := passFixture(t, "item-a")
+	backendRun(t, f, "older", "fixture", 2)
+	backendRun(t, f, "newer", "fixture", 0)
+	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
+		t.Fatal(err)
+	}
+	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
+		t.Fatal(err)
+	}
+	fixtureWrite(t, f, func(tx *store.Tx) error {
+		var count int
+		if err := tx.Reader().QueryRow(`SELECT COUNT(*) FROM backend_outcomes`).Scan(&count); err != nil {
+			return err
+		}
+		if count != 2 {
+			t.Errorf("saved %d classes, want both endings once", count)
+		}
+		return nil
+	})
+}
+
+func TestParallelBackendHoldNamesWaitingNodeNotFirstSibling(t *testing.T) {
+	f, l, _ := parallelFixture(t)
+	d := mustPlan(t, l)[0].decision
+	first := d
+	first.WorkflowRestart = true
+	first.RunRequest = rerouteNodeRequest(l.cfg.Policy, first, "fixture")
+	first.RunRequest.RunID = "first-ready"
+	second := first
+	second.RunRequest = rerouteNodeRequest(l.cfg.Policy, second, "fixture")
+	second.RunRequest.Backend = "broken"
+	second.RunRequest.RunID = "second-ready"
+	second.NodeIndex = 1
+	second.Seat = "review"
+	first.Parallel = []Decision{second}
+	l.cfg.Policy.Backends["broken"] = l.cfg.Policy.Backends["fixture"]
+	l.cfg.Policy.Seats["review"] = "broken"
+	backendRun(t, f, "broken-ending", "broken", 2)
+	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
+		t.Fatal(err)
+	}
+	for _, restart := range []bool{false, true} {
+		first.WorkflowRestart = restart
+		fixtureWrite(t, f, func(tx *store.Tx) error {
+			hold, err := pendingDecision(tx.Reader(), &pendingWork{Decision: first, Ready: true}, l.cfg.Now(), nil, nil, l.cfg.Policy, map[string]bool{"broken": true}, 0)
+			if err != nil {
+				return err
+			}
+			if err := describeBackendHold(tx.Reader(), &hold, l.cfg.Policy); err != nil {
+				return err
+			}
+			if hold.Reason != ReasonBackendClosed || hold.DeferredBy != "broken" {
+				t.Errorf("restart=%v hold names wrong backend: %+v", restart, hold)
+			}
+			return nil
+		})
+	}
+}
diff --git a/tools/tillr-drain/drain/command_loop_test.go b/tools/tillr-drain/drain/command_loop_test.go
index 52bbda189..7ac04a336 100644
--- a/tools/tillr-drain/drain/command_loop_test.go
+++ b/tools/tillr-drain/drain/command_loop_test.go
@@ -150,7 +150,7 @@ func TestCommandWorkflowOutcomesAndPreservation(t *testing.T) {
 				}
 				want := "node-failed: check"
 				if mode == "fault" || mode == "missing" || mode == "wall" {
-					want = ReasonInfraExhausted
+					want = ReasonCommandFault
 				}
 				if mode == "dirty" || mode == "escape-commit" {
 					want = ScopeEscape
diff --git a/tools/tillr-drain/drain/command_restart_test.go b/tools/tillr-drain/drain/command_restart_test.go
index 2b1687e64..c3aaf7916 100644
--- a/tools/tillr-drain/drain/command_restart_test.go
+++ b/tools/tillr-drain/drain/command_restart_test.go
@@ -25,7 +25,7 @@ func TestCommandHoldRestartKeepsOutcomeAndBuilderPin(t *testing.T) {
 			base := runGit(t, f.root, "rev-parse", "HEAD")
 			outcome, want := "failed", "node-failed: check"
 			if mode == "fault" {
-				outcome, want = "fault", ReasonInfraExhausted
+				outcome, want = "fault", ReasonCommandFault
 			}
 			// Reproduce the crash window after hold classification and reset, before
 			// markExamined. These are the same operations terminal settlement performs.
diff --git a/tools/tillr-drain/drain/definition.go b/tools/tillr-drain/drain/definition.go
index ac8d18c47..d53195e0e 100644
--- a/tools/tillr-drain/drain/definition.go
+++ b/tools/tillr-drain/drain/definition.go
@@ -52,9 +52,9 @@ var landingHolds = map[string]string{
 // checks. These are not landing-specific; the value names an optional prerequisite.
 var generalHolds = map[string]string{
 	ReasonCompleteItemFault: "", ReasonCriteriaUnjudged: "", ReasonCandidateNoOp: "",
-	ReasonBackendWindow: "", ReasonBlocked: "", ReasonNotReady: "",
+	ReasonBackendWindow: "", ReasonBackendClosed: "", ReasonBlocked: "", ReasonNotReady: "",
 	ReasonRunLinkMissing: "", ReasonRunUnjudged: "", ReasonNoPolicy: "",
-	ReasonNoLane: "", ReasonRunLive: "", ReasonInfraExhausted: "",
+	ReasonNoLane: "", ReasonRunLive: "", ReasonInfraExhausted: "", ReasonCommandFault: "",
 	ReasonDeskCapacity: "", ReasonDocumentDeskBusy: "",
 	ReasonRetryJudged: "", ReasonRetryUnjudged: "", ReasonToolingDefect: "",
 	ReasonDecisionCollision: "", ReasonSettleFailed: "",
diff --git a/tools/tillr-drain/drain/dispatches.go b/tools/tillr-drain/drain/dispatches.go
index 879cb62f4..26ac3a935 100644
--- a/tools/tillr-drain/drain/dispatches.go
+++ b/tools/tillr-drain/drain/dispatches.go
@@ -195,7 +195,7 @@ func init() {
 	// the same statement. A store created before drain_passes or retries existed gains each
 	// on its next write-open, which is what the migration's CREATE TABLE IF NOT EXISTS is
 	// for.
-	if err := store.RegisterDDL("drain", DispatchDDL+graphRevisionDDL+PassDDL+passSpendDDL+legacyPassMigrationDDL+SessionDDL+RetryDDL+GateSpanDDL+rollingDDL+trunkHistoryDDL+generationDDL+backendWindowDDL+backendQuotaDDL); err != nil {
+	if err := store.RegisterDDL("drain", DispatchDDL+graphRevisionDDL+PassDDL+passSpendDDL+legacyPassMigrationDDL+SessionDDL+RetryDDL+GateSpanDDL+rollingDDL+trunkHistoryDDL+generationDDL+backendWindowDDL+backendQuotaDDL+backendOutcomesDDL); err != nil {
 		panic(err)
 	}
 	for name, definition := range map[string]string{"loop_owner": "TEXT", "stop_requested": "INTEGER"} {
diff --git a/tools/tillr-drain/drain/failover.go b/tools/tillr-drain/drain/failover.go
index 409e24cbc..1334b684d 100644
--- a/tools/tillr-drain/drain/failover.go
+++ b/tools/tillr-drain/drain/failover.go
@@ -156,36 +156,31 @@ type priorRun struct {
 // store and what the store points at, and a log that is gone reads as "no report" rather
 // than as an error.
 func readPriorRuns(r *store.Reader) (map[string][]priorRun, error) {
-	interrupted, restaged := map[string]bool{}, map[string]bool{}
-	events, err := r.Events(0)
+	interrupted, restaged, err := readInterruptedRuns(r)
 	if err != nil {
 		return nil, err
 	}
-	// An interruption describes the segment it ended. A later continuation of the
-	// same run (run.resume) is that run's new segment, and its outcome is judged.
-	for _, e := range events {
-		if e.ObjectID == nil {
-			continue
-		}
-		switch e.Kind {
-		case loopOrphanedKind:
-			interrupted[*e.ObjectID] = true
-		case staleBaseKind:
-			// A stale base is the staging's failure, not the item's: like a loop stop,
-			// it ends the attempt without an outcome to hold the item on.
-			interrupted[*e.ObjectID], restaged[*e.ObjectID] = true, true
-		case run.ResumeKind:
-			delete(interrupted, *e.ObjectID)
+	// Once the loop observes an ending, routing and availability share that
+	// saved class; a report edit cannot move the item to a different retry rule.
+	outcomes, err := readBackendOutcomes(r)
+	if err != nil {
+		return nil, err
+	}
+	classes := map[string]AttemptClass{}
+	for _, o := range outcomes {
+		if _, seen := classes[o.rec.RunID]; !seen && o.saved {
+			classes[o.rec.RunID] = o.class
 		}
 	}
-	// Timestamp ties follow recording order, not lexical run-id order (a10 sorts
-	// before a9). event_seq is the store event's sequence; SQLite's insertion row
-	// number, rowid, breaks ties for legacy rows with no event sequence.
+	// Endings define run order, not mutable row metadata: a usage correction
+	// can advance runs.event_seq without ending a run again. Legacy rows with
+	// no ending event retain their recorded sequence, timestamp, and row order.
 	rows, err := r.Query(
 		`SELECT item_id, run_id, backend, state, exit_code, artifact_refs
 		   FROM runs WHERE item_id IS NOT NULL AND NOT EXISTS
               (SELECT 1 FROM events e WHERE e.kind='drain.judge.request' AND e.object_id=runs.run_id)
-           ORDER BY item_id, started_at, event_seq, rowid`)
+           ORDER BY item_id, COALESCE((SELECT MAX(e.seq) FROM events e
+             WHERE e.kind='run.finish' AND e.object_id=runs.run_id), event_seq), started_at, rowid`)
 	if err != nil {
 		return nil, readFailure(err)
 	}
@@ -209,7 +204,10 @@ func readPriorRuns(r *store.Reader) (map[string][]priorRun, error) {
 		if err := json.Unmarshal([]byte(artifactRefs), &refs); err != nil {
 			return nil, Internalf("reading the artifact refs of run %q: %v", rec.RunID, err)
 		}
-		class := ClassifyAttempt(rec, logTail(refs))
+		class := classes[rec.RunID]
+		if class == "" {
+			class = ClassifyAttempt(rec, logTail(refs))
+		}
 		if interrupted[rec.RunID] {
 			class = AttemptInterrupted
 		}
diff --git a/tools/tillr-drain/drain/loop.go b/tools/tillr-drain/drain/loop.go
index 254a6859d..85b13199f 100644
--- a/tools/tillr-drain/drain/loop.go
+++ b/tools/tillr-drain/drain/loop.go
@@ -772,6 +772,10 @@ func (l *Loop) Run(ctx context.Context) (result LoopResult, err error) {
 	if err := l.startDefinition(ctx); err != nil {
 		return faultResult(result, err)
 	}
+	// Runs that ended while no loop was running have no saved class yet.
+	if err := RecordBackendOutcomes(ctx, l.cfg.Store); err != nil {
+		return faultResult(result, err)
+	}
 	// The documents are synced before anything is planned: an item block committed since
 	// the last drain is work this drain should see, and the sync is the only way an item
 	// enters the store. A finding does not stop the loop — the documents without one were
@@ -1502,6 +1506,9 @@ func (l *Loop) settle(ctx context.Context, decision Decision, record run.Record,
 			outcome.err = l.markExamined(ctx, decision)
 		}
 	}()
+	if err := RecordBackendOutcomes(ctx, l.cfg.Store); err != nil {
+		return laneOutcome{err: err}
+	}
 	lane := decision.LaneID
 	worktree := l.lanePath(lane)
 	request := *decision.RunRequest
@@ -1642,7 +1649,7 @@ func (l *Loop) settle(ctx context.Context, decision Decision, record run.Record,
 		}
 	}
 	class := ClassifyAttempt(record, log)
-	if class == AttemptInfrastructure {
+	for class == AttemptInfrastructure {
 		if err := l.recordBackendWindow(ctx, record); err != nil {
 			return laneOutcome{err: err}
 		}
@@ -1652,8 +1659,8 @@ func (l *Loop) settle(ctx context.Context, decision Decision, record run.Record,
 		streak := l.infraStreak
 		l.mu.Unlock()
 		l.reportf("loop: %s failed in the infrastructure class (%d consecutive)\n", request.ItemID, streak)
-		// The one permitted failover: the machinery failed and the item learned nothing,
-		// so the same work goes to the next backend once, after the worktree is reset. It
+		// Machinery failed: close that backend and walk to the next open one after reset.
+		// A fully closed route ends this walk until recorded evidence reopens it. It
 		// runs in this lane's own goroutine, so its bracket has one target and a violation
 		// there names this run.
 		if deferJudge {
@@ -1665,7 +1672,11 @@ func (l *Loop) settle(ctx context.Context, decision Decision, record run.Record,
 		if err != nil {
 			return laneOutcome{err: err}
 		}
+		if !ok {
+			break
+		}
 		if ok {
+			decision.Attempt++
 			failover.CorrelationID = tick
 			if err := l.preserveWork(ctx, lane, request.RunID, "infrastructure failure before failover"); err != nil {
 				return laneOutcome{err: err}
@@ -1698,6 +1709,9 @@ func (l *Loop) settle(ctx context.Context, decision Decision, record run.Record,
 			if err != nil || stop != "" {
 				return laneOutcome{stop: stop, err: err}
 			}
+			if err := RecordBackendOutcomes(ctx, l.cfg.Store); err != nil {
+				return laneOutcome{err: err}
+			}
 			class = ClassifyAttempt(record, log)
 			if class == AttemptInfrastructure {
 				if err := l.recordBackendWindow(ctx, record); err != nil {
@@ -1707,8 +1721,8 @@ func (l *Loop) settle(ctx context.Context, decision Decision, record run.Record,
 		}
 	}
 	if class == AttemptInfrastructure {
-		// The failover failed too, or the policy has no hop left: the item holds and the
-		// next tick plans it as `run-infra-exhausted`. The criteria are untouched, the
+		// No backend is open: the next pass waits on recorded backend recovery.
+		// The criteria are untouched, the
 		// head is pinned, and the loop moves on.
 		return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane))}
 	}
diff --git a/tools/tillr-drain/drain/parallel_failover.go b/tools/tillr-drain/drain/parallel_failover.go
index 244e81275..e589f226a 100644
--- a/tools/tillr-drain/drain/parallel_failover.go
+++ b/tools/tillr-drain/drain/parallel_failover.go
@@ -21,8 +21,11 @@ func (l *Loop) parallelFailover(ctx context.Context, node Decision, rec run.Reco
 	if err != nil {
 		return node, false, err
 	}
-	if !ok || node.NodeInfraFailures >= l.cfg.Policy.failovers() {
-		return node, false, nil
+	if !ok {
+		request, _, _ = l.currentPolicy().NextRequest(node.ItemID, node.LaneID, node.RunRequest.RunID+"-failover", rec.Backend, node.Seat)
+		if request == nil {
+			return node, false, Usagef("workflow seat %s has no backend route", node.Seat)
+		}
 	}
 	request.ReadOnlyGroup = node.RunRequest.ReadOnlyGroup
 	request.NodeID = node.Workflow.Stages[node.NodeIndex].ID
diff --git a/tools/tillr-drain/drain/pass.go b/tools/tillr-drain/drain/pass.go
index 518019001..614cd0c8d 100644
--- a/tools/tillr-drain/drain/pass.go
+++ b/tools/tillr-drain/drain/pass.go
@@ -41,7 +41,7 @@ const (
 )

 func init() {
-	for _, kind := range []string{commentReplyStateKind, designerCompactedKind, examinationClaimedKind, examinationReleasedKind, dispatchStartedKind, candidateReadyKind, candidateRetirementKind, candidatePreparedKind, examinationDoneKind, examinationOwnerKind, loopOrphanedKind, laneReleasedKind, nodeAdvancedKind, graphChangedKind, candidateGatedKind} {
+	for _, kind := range []string{backendProbeKind, backendOutcomeKind, commentReplyStateKind, designerCompactedKind, examinationClaimedKind, examinationReleasedKind, dispatchStartedKind, candidateReadyKind, candidateRetirementKind, candidatePreparedKind, examinationDoneKind, examinationOwnerKind, loopOrphanedKind, laneReleasedKind, nodeAdvancedKind, graphChangedKind, candidateGatedKind} {
 		if err := event.Register(kind, Utility); err != nil {
 			panic(err)
 		}
@@ -432,17 +432,28 @@ func pendingDecision(r *store.Reader, work *pendingWork, at time.Time, inputs *R
 	}
 	if work.Ready {
 		if len(d.Parallel) > 0 && d.WorkflowRestart {
-			for _, node := range parallelNodes(d) {
+			nodes := parallelNodes(d)
+			for i, node := range nodes {
 				rec, err := terminalRecord(r, node.RunRequest.RunID)
 				if err != nil {
 					return Decision{}, err
 				}
-				if rec == nil && !commandStage(node) && shut[node.RunRequest.Backend] {
+				if rec == nil && !commandStage(node) && node.NodeInfraFailures > 0 {
+					name, _, ok := policy.availableBackend(node.RunRequest.Backend, node.Seat, shut)
+					if ok {
+						nodes[i].RunRequest = rerouteNodeRequest(policy, node, name)
+					}
+				}
+				if rec == nil && !commandStage(node) && shut[nodes[i].RunRequest.Backend] {
+					d.Seat = node.Seat
+					d.DeferredBy = nodes[i].RunRequest.Backend
 					d.Action, d.Reason, d.To = ActionHold, ReasonBackendWindow, ""
 					identifyDecision(&d)
 					return d, nil
 				}
 			}
+			d = nodes[0]
+			d.Parallel = nodes[1:]
 		}
 		if len(d.Parallel) > 0 {
 			open, err := dispatchQuotaOpen(r, d, shut, policy)
@@ -480,7 +491,7 @@ func pendingDecision(r *store.Reader, work *pendingWork, at time.Time, inputs *R
 			name = d.RunRequest.Backend
 		}
 		name, degraded, ok := policy.availableBackend(name, d.Seat, shut)
-		if d.WorkflowRestart {
+		if d.WorkflowRestart && d.NodeInfraFailures == 0 {
 			name, degraded = d.RunRequest.Backend, ""
 			ok = !shut[name]
 		}
@@ -491,6 +502,9 @@ func pendingDecision(r *store.Reader, work *pendingWork, at time.Time, inputs *R
 			if d.WorkflowRestart {
 				retained := *d.RunRequest
 				request = &retained
+				if name != retained.Backend {
+					request = rerouteNodeRequest(policy, d, name)
+				}
 			}
 			if request == nil {
 				return Decision{}, Usagef("workflow seat %s cannot build a request", d.Seat)
@@ -1055,7 +1069,7 @@ func (l *Loop) applyPlanRecorded(ctx context.Context, p plannedApplication, main
 		}
 		d := p.decision
 		if (d.Action == ActionDispatch || d.Action == ActionRetry) && d.RunRequest != nil {
-			shut, err := readQuotaShutBackends(tx.Reader(), l.cfg.Now(), l.currentPolicy())
+			shut, err := readDispatchShut(tx.Reader(), l.cfg.Now(), l.currentPolicy(), d)
 			if err != nil {
 				return err
 			}
@@ -1243,7 +1257,7 @@ func (l *Loop) applyPlanRecorded(ctx context.Context, p plannedApplication, main
 			if err := l.claimParallel(tx, &d); err != nil {
 				return err
 			}
-			shut, err := readQuotaShutBackends(tx.Reader(), at, l.currentPolicy())
+			shut, err := readDispatchShut(tx.Reader(), at, l.currentPolicy(), d)
 			if err != nil {
 				return err
 			}
diff --git a/tools/tillr-drain/drain/plan.go b/tools/tillr-drain/drain/plan.go
index e232c3fe7..f749ad047 100644
--- a/tools/tillr-drain/drain/plan.go
+++ b/tools/tillr-drain/drain/plan.go
@@ -219,6 +219,10 @@ func planDefinition(r *store.Reader, tick Pass, policy *Policy, questions Questi
 	if err != nil {
 		return nil, readFailure(err)
 	}
+	quotaShut, err := readQuotaOnlyShut(r, at, policy)
+	if err != nil {
+		return nil, readFailure(err)
+	}
 	items, err := readItems(r)
 	if err != nil {
 		return nil, err
@@ -426,6 +430,7 @@ func planDefinition(r *store.Reader, tick Pass, policy *Policy, questions Questi
 		commentHolds: commentHolds, documentRoot: root,
 		conflicts: conflicts,
 		shut:      shut,
+		quotaShut: quotaShut,
 		forks:     forks,
 		tick:      tick, policy: policy, held: lanes, deskOwners: deskOwners, itemStates: states, history: history,
 		priors: attemptPriors, retries: retries, classes: classes, latest: latest, questions: questions,
@@ -634,6 +639,9 @@ func planDefinition(r *store.Reader, tick Pass, policy *Policy, questions Questi
 			request.NodeID = decision.Workflow.Stages[decision.NodeIndex].ID
 			decision.RunRequest = &request
 		}
+		if err := describeBackendHold(r, &decision, policy); err != nil {
+			return nil, err
+		}
 		if def.Completes != "landing" && decision.RunRequest != nil && decision.RunRequest.WorkingDir == WorktreeDir {
 			request := *decision.RunRequest
 			request.WorkingDir = PlainDir
@@ -663,10 +671,13 @@ type planner struct {
 	definition  *definition.Definition
 	// An open review batch holds new build dispatches for its document; workers
 	// already running settle normally, without being interrupted by intake.
-	commentHolds   map[string]string
-	documentRoot   string
-	attempts       map[string]int
-	shut           map[string]bool
+	commentHolds map[string]string
+	documentRoot string
+	attempts     map[string]int
+	shut         map[string]bool
+	// quotaShut leaves out backends closed by an infrastructure ending: a retry
+	// the owner authorized is the one dispatch that may go to one.
+	quotaShut      map[string]bool
 	conflicts      map[string]item.Conflict
 	inputs         map[string]*RunInputs
 	inputDecisions map[string]Decision
@@ -756,7 +767,7 @@ func (p *planner) decide(item itemRow, blockedBy []string) Decision {
 		// A released acquittal starts fresh, including one whose dispatch had
 		// consumed an owner retry.
 		retry = nil
-		attempt.failover, attempt.exhausted, attempt.degraded = false, false, ""
+		attempt.failover, attempt.degraded = false, ""
 		if p.policy != nil {
 			attempt.backend = p.firstBackend(item)
 		}
@@ -783,20 +794,26 @@ func (p *planner) decide(item itemRow, blockedBy []string) Decision {
 		// A returned ending is about the candidate, not the machinery: the next run starts
 		// on the item's own backend.
 		attempt.backend = p.firstBackend(item)
-		attempt.failover, attempt.exhausted, attempt.degraded = false, false, ""
+		attempt.failover, attempt.degraded = false, ""
 	}
 	if hasConflict && conflict.Status == "answered" && p.policy != nil {
 		attempt.backend = p.firstBackend(item)
-		attempt.failover, attempt.exhausted, attempt.degraded = false, false, ""
+		attempt.failover, attempt.degraded = false, ""
 		decision.ConflictID = conflict.ConflictID
 	}
 	windowShut := false
+	backendWaitStart := ""
 	if p.policy != nil && item.Command == nil {
 		if retry != nil {
 			attempt.backend = p.firstBackend(item)
 		}
 		if attempt.backend != "" {
-			next, degraded, ok := p.policy.availableBackend(attempt.backend, item.Seat, p.shut)
+			backendWaitStart = attempt.backend
+			shut := p.shut
+			if retry != nil && retry.refusal == "" && !p.quotaShut[attempt.backend] {
+				shut = p.quotaShut
+			}
+			next, degraded, ok := p.policy.availableBackend(attempt.backend, item.Seat, shut)
 			windowShut = !ok
 			attempt.backend = next
 			if degraded != "" {
@@ -852,8 +869,6 @@ func (p *planner) decide(item itemRow, blockedBy []string) Decision {
 		decision.Action, decision.Reason = ActionHold, retry.refusal
 		decision.RetryID = retry.retryID
 		decision.SupersedesRunID = retry.supersedesRunID
-	case retry == nil && attempt.exhausted:
-		decision.Action, decision.Reason = ActionHold, ReasonInfraExhausted
 	case retry == nil && !interrupted && !attempt.failover && returned == nil && p.mayBeARetry(item.ItemID) && decision.ConflictID == "" && holdEnabled(p.rules(), p.retryHoldReason(item.ItemID)):
 		decision.Action, decision.Reason = ActionHold, p.retryHoldReason(item.ItemID)
 	case holdEnabled(p.rules(), ReasonNoFence) && fenceAdmitsNothing(fence):
@@ -865,6 +880,7 @@ func (p *planner) decide(item itemRow, blockedBy []string) Decision {
 		decision.Action, decision.Reason = ActionHold, ReasonNoPolicy
 	case windowShut:
 		decision.Action, decision.Reason = ActionHold, ReasonBackendWindow
+		decision.DeferredBy = backendWaitStart
 	case p.tick.MaxDispatches != nil && p.dispatched >= *p.tick.MaxDispatches:
 		decision.Action, decision.Reason = ActionHold, ReasonNoLane
 	default:
@@ -1376,26 +1392,7 @@ func attemptRunID(itemID string, attempt int) string {
 	return "run-" + itemID + "-a" + strconv.Itoa(attempt)
 }

-// attempt is what the item's own run history says its next dispatch is: which backend it
-// names, whether that dispatch is a failover re-dispatch, whether that walk leaves the
-// backend the item's seat names, and whether its failover budget is spent so the item must
-// hold instead.
-//
-// The chain is read from the item's terminal runs, oldest first. An item with no
-// infrastructure-class failure starts at its first backend — its override, else the
-// policy's default (design §2's precedence). An item whose runs include an
-// infrastructure-class failure is a failover in progress: it walks to the backend after
-// the last run's backend, and holds once the walk has no hop left or the policy's budget
-// is spent. Counting every infrastructure-class run rather than only the last one is the
-// conservative direction the budget wants: a chain is refused early rather than walked
-// again. A walk that leaves the backend the item's seat names is a model switch and is
-// recorded as one (nextBackend answers the seat), never read as an ordinary failover.
-//
-// A run that failed for a candidate reason leaves no chain here. Such an item holds with
-// ReasonRunLinkMissing unless a person authorized a retry, and the retry rule then checks
-// the run's class: nothing in this face spends a second backend on judged work. The
-// failover chain is the machinery's automatic answer; a retry is the operator's, and it
-// starts from the first backend rather than walking a chain the budget already spent.
+// An interruption retains the same backend; it is not a failover.
 func attemptForInterruption(priors []priorRun) attempt {
 	return attempt{backend: priors[len(priors)-1].Backend}
 }
@@ -1406,23 +1403,13 @@ func (p *planner) attempt(row itemRow) attempt {
 		return a
 	}
 	priors := p.priors[row.ItemID]
-	failures := 0
-	for _, prior := range priors {
-		if prior.Class == AttemptInfrastructure {
-			failures++
-		}
-	}
-	if failures == 0 {
+	if len(priors) == 0 || priors[len(priors)-1].Class != AttemptInfrastructure {
 		a.backend = p.firstBackend(row)
 		return a
 	}
-	if failures > p.policy.failovers() || len(priors) == 0 {
-		a.exhausted = true
-		return a
-	}
 	next, degraded, ok := p.policy.nextBackend(priors[len(priors)-1].Backend, row.Seat)
 	if !ok {
-		a.exhausted = true
+		a.backend = p.policy.firstBackend(row)
 		return a
 	}
 	a.backend = next
@@ -1438,8 +1425,6 @@ type attempt struct {
 	// failover says this dispatch re-stages an item whose last run failed in the
 	// infrastructure class, so the decision's reason says so and the spend is visible.
 	failover bool
-	// exhausted says the item's failover chain has no hop left: hold, do not walk.
-	exhausted bool
 	// degraded names the seat whose backend this walk leaves, or "": the failed backend
 	// was the one the item's seat names, so the re-dispatch reaches another model and the
 	// decision says so (seatDegradedReason).
diff --git a/tools/tillr-drain/drain/policy.go b/tools/tillr-drain/drain/policy.go
index ec24e6138..d902632de 100644
--- a/tools/tillr-drain/drain/policy.go
+++ b/tools/tillr-drain/drain/policy.go
@@ -39,7 +39,6 @@ type Policy struct {

 	DefaultBackend string   `json:"default_backend"`
 	Preference     []string `json:"preference"`
-	MaxFailovers   *int     `json:"max_failovers"`
 	// Seats maps each seat the item model defines (seat.go) to the backend that fills it. It is where a role reaches a model: an item names the seat and never a model
 	// id, so swapping which model designs, or moving the design seat to another backend, is
 	// an edit here and touches no item.
@@ -236,11 +235,6 @@ func (p Policy) seatNames() []string {
 	return names
 }

-// DefaultMaxFailovers is the failover budget a policy that names none carries: one
-// re-dispatch per item, which is the whole multiplication a tick can do to an item's cost
-// (design §2, "What bounds the spend").
-const DefaultMaxFailovers = 1
-
 // Check refuses a policy that cannot produce a complete request for every backend it
 // names, before the store is read: a dispatch built from such a policy would carry a
 // run.request a runner refuses, which is a record asserting a request that cannot run.
@@ -276,9 +270,6 @@ func (p Policy) Check() error {
 		}
 		seen[name] = true
 	}
-	if p.MaxFailovers != nil && *p.MaxFailovers < 0 {
-		return Usagef("the policy names max_failovers %d: a failover budget is zero or more re-dispatches", *p.MaxFailovers)
-	}
 	for _, name := range backendNames(p.Backends) {
 		if err := checkBackend(name, p.Backends[name]); err != nil {
 			return err
@@ -345,15 +336,6 @@ func checkBackend(name string, b Backend) error {
 	return nil
 }

-// failovers is the failover budget, with the policy's default filled in: a policy that
-// names none carries one re-dispatch per item.
-func (p Policy) failovers() int {
-	if p.MaxFailovers == nil {
-		return DefaultMaxFailovers
-	}
-	return *p.MaxFailovers
-}
-
 // firstBackend answers the backend an item's first dispatch names: its per-item override
 // when the policy carries one, else the backend its seat maps to, else the tick's default.
 //
@@ -378,31 +360,11 @@ func (p Policy) firstBackend(row itemRow) string {
 	return p.DefaultBackend
 }

-// nextBackend answers the backend a failover after `failed` walks to: the next one in the
-// preference order, never one before it (design §2, "never backward"). A backend that is
-// not in the preference at all — one reached by an override — starts the walk at the head,
-// because there is no position in the order to continue from.
-//
-// It also answers the seat whose backend the walk leaves, or "" when it leaves no seat. A
-// seat is a statement about the work — the design seat runs on the design backend — so a
-// walk that leaves that backend is a model switch, and it is named rather than performed
-// silently: the caller records the seat on the decision and spells it into the reason. The
-// seat is the item's, not a reverse lookup of the backend: several seats may share one
-// backend (build, review and scout all reach the default), and only the failed backend's
-// identity against the item's own seat says whether the walk is a degradation.
-//
-// It answers false when the walk has nowhere to go, which the caller reports as the
-// item's failover budget being spent rather than dispatching on the backend that just
-// failed.
-//
-// A seat that names a list of backends (docs/design/tillr-claude-backend.md §4) has its own
-// order, and that list replaces the preference for the item's walk: forward from the failed
-// backend, and no hop at the list's end, so a design item never falls onto the build
-// backend. Every backend on the list was named for that seat, so a step along it is not a
-// degradation and names no seat. A failed backend that is not on the list (one an
-// override reached) has no position to continue from, so the walk starts at the list's
-// head, the same rule the preference walk uses. A seat with one backend, and an item with
-// no seat, walk the preference exactly as before lists existed.
+// nextBackend walks after failed in the item's ordered seat list, wrapping at
+// the end. Multi-backend seats never leave their list. Single-backend seats
+// retain their fallback through the preference, naming any seat the walk leaves.
+// An override outside the order starts at its head. Availability is checked by
+// availableBackend, not by this order-only helper.
 func (p Policy) nextBackend(failed, seat string) (backend, degraded string, ok bool) {
 	if list := p.seatList(seat); seat != "" && len(list) > 1 {
 		for i, name := range list {
@@ -412,7 +374,7 @@ func (p Policy) nextBackend(failed, seat string) (backend, degraded string, ok b
 			if i+1 < len(list) {
 				return list[i+1], "", true
 			}
-			return "", "", false
+			return list[0], "", true
 		}
 		return list[0], "", true
 	}
@@ -434,25 +396,19 @@ func (p Policy) nextBackend(failed, seat string) (backend, degraded string, ok b
 			next := p.Preference[i+1]
 			return next, leavesSeat(next), true
 		}
-		return "", "", false
+		next := p.Preference[0]
+		return next, leavesSeat(next), true
 	}
 	if len(p.Preference) > 0 {
 		next := p.Preference[0]
 		return next, leavesSeat(next), true
 	}
-	return "", "", false
+	return failed, "", failed != ""
 }

-// NextRequest is the request a failover after failedBackend dispatches: the same item on
-// the same lane, on the next backend in the policy's preference. It also answers the seat
-// the walk left, or "" when the walk leaves no seat, so the caller can record the
-// degradation rather than let a model switch pass as an ordinary failover. It answers false
-// when the walk has no hop left, which the caller reports as an exhausted failover budget
-// rather than spending the backend that just failed.
-//
-// It is exported because the loop performs the one failover the design permits inside a
-// tick (design §4.1 step 5), and the walk is the policy's: a second spelling of "the next
-// backend" outside this file is a second answer to the same question.
+// NextRequest builds the next request in order without admitting it. The loop
+// uses nextAvailableRequest for dispatch; a waiting workflow uses this request
+// to retain its continuation until the planner finds an open backend.
 func (p Policy) NextRequest(itemID, laneID, runID, failedBackend, seat string) (*run.Request, string, bool) {
 	next, degraded, ok := p.nextBackend(failedBackend, seat)
 	if !ok {
diff --git a/tools/tillr-drain/drain/policy_test.go b/tools/tillr-drain/drain/policy_test.go
index 30191f8cc..cc1106ed7 100644
--- a/tools/tillr-drain/drain/policy_test.go
+++ b/tools/tillr-drain/drain/policy_test.go
@@ -103,10 +103,6 @@ func TestPolicyCheckRefusesADocumentThatCannotBuildARequest(t *testing.T) {
 			mutate: func(p *Policy) { p.Preference = []string{"first", "first"} },
 			names:  "twice",
 		},
-		"negative failover budget": {
-			mutate: func(p *Policy) { n := -1; p.MaxFailovers = &n },
-			names:  "max_failovers",
-		},
 		"backend with no adapter": {
 			mutate: func(p *Policy) {
 				b := p.Backends["second"]
@@ -253,8 +249,8 @@ func TestSelectionFollowsOverrideThenPreference(t *testing.T) {
 	if got, degraded, ok := policy.nextBackend("first", ""); !ok || got != "second" || degraded != "" {
 		t.Fatalf("the failover after first is %q / %q / %v, want second with no seat", got, degraded, ok)
 	}
-	if got, _, ok := policy.nextBackend("second", ""); ok {
-		t.Fatalf("the failover after the last backend is %q, want no hop", got)
+	if got, _, ok := policy.nextBackend("second", ""); !ok || got != "first" {
+		t.Fatalf("the failover after the last backend is %q, want wrapped first", got)
 	}
 	if got, degraded, ok := policy.nextBackend("unlisted", ""); !ok || got != "first" || degraded != "" {
 		t.Fatalf("the failover after an unlisted backend is %q / %q / %v, want the preference head", got, degraded, ok)
@@ -287,17 +283,28 @@ func TestAFailoverOffTheSeatsBackendNamesTheSeat(t *testing.T) {
 	}
 }

-// A policy that names no failover budget carries the default, and a policy that names zero
-// walks nowhere: the budget is the policy's, not the code's.
-func TestFailoverBudgetComesFromThePolicy(t *testing.T) {
-	policy := twoBackends()
-	if got := policy.failovers(); got != DefaultMaxFailovers {
-		t.Fatalf("a policy naming no budget has %d failovers, want the default %d", got, DefaultMaxFailovers)
+// Legacy budgets are accepted but ignored, even when their old value was invalid.
+func TestLegacyFailoverBudgetIsIgnored(t *testing.T) {
+	raw, err := json.Marshal(twoBackends())
+	if err != nil {
+		t.Fatal(err)
+	}
+	var doc map[string]any
+	if err := json.Unmarshal(raw, &doc); err != nil {
+		t.Fatal(err)
+	}
+	doc["max_failovers"] = -1
+	raw, _ = json.Marshal(doc)
+	var policy Policy
+	if err := json.Unmarshal(raw, &policy); err != nil {
+		t.Fatal(err)
+	}
+	if err := policy.Check(); err != nil {
+		t.Fatal(err)
 	}
-	none := 0
-	policy.MaxFailovers = &none
-	if got := policy.failovers(); got != 0 {
-		t.Fatalf("a policy naming zero failovers has %d", got)
+	next, _, ok := policy.nextBackend("second", "")
+	if !ok || next != "first" {
+		t.Fatalf("legacy budget changed walk: %s %v", next, ok)
 	}
 }

@@ -424,13 +431,13 @@ func TestAFailoverWalksTheItemsSeatBackends(t *testing.T) {
 		ok                 bool
 	}{
 		{"the design list's next backend", "designer-a", "design", "designer-b", "", true},
-		{"the end of the design list", "designer-b", "design", "", "", false},
+		{"the end of the design list wraps", "designer-b", "design", "designer-a", "", true},
 		{"the build list's next backend", "builder-a", "build", "builder-b", "", true},
-		{"the end of the build list", "builder-b", "build", "", "", false},
+		{"the end of the build list wraps", "builder-b", "build", "builder-a", "", true},
 		{"a backend off the list starts at its head", "builder-b", "design", "designer-a", "", true},
 		{"a one-element seat walks the preference and names the seat", "builder-a", "review", "builder-b", "review", true},
 		{"no seat walks the preference", "builder-a", "", "builder-b", "", true},
-		{"no seat stops at the preference's end", "builder-b", "", "", "", false},
+		{"no seat wraps at the preference's end", "builder-b", "", "builder-a", "", true},
 	}
 	for _, c := range cases {
 		t.Run(c.name, func(t *testing.T) {
@@ -448,8 +455,8 @@ func TestAFailoverWalksTheItemsSeatBackends(t *testing.T) {
 	if !ok || request.Backend != "designer-b" || degraded != "" {
 		t.Fatalf("the loop's failover off designer-a is %+v / %q / %v, want designer-b, not degraded", request, degraded, ok)
 	}
-	if _, _, ok := policy.NextRequest("item_042", "lane-1", "run-3", "designer-b", "design"); ok {
-		t.Fatalf("the loop's failover walked past the end of the design seat's list")
+	if req, _, ok := policy.NextRequest("item_042", "lane-1", "run-3", "designer-b", "design"); !ok || req.Backend != "designer-a" {
+		t.Fatalf("the loop's failover did not wrap within the design seat's list")
 	}
 }

diff --git a/tools/tillr-drain/drain/quota_probe.go b/tools/tillr-drain/drain/quota_probe.go
index 19a89d4da..0ecb4bfc1 100644
--- a/tools/tillr-drain/drain/quota_probe.go
+++ b/tools/tillr-drain/drain/quota_probe.go
@@ -217,8 +217,36 @@ func (l *Loop) quotaReplacementBackend(ctx context.Context, start, seat string)
 	return name, open, err
 }

-// No quota command means no reading-based hold. Refusal windows hold every backend.
+// readQuotaShutBackends is every backend a dispatch must skip: shut by quota or
+// closed by its latest infrastructure ending.
 func readQuotaShutBackends(r *store.Reader, at time.Time, policy *Policy) (map[string]bool, error) {
+	shut, err := readQuotaOnlyShut(r, at, policy)
+	if err != nil {
+		return nil, err
+	}
+	closed, err := readInfrastructureClosed(r)
+	if err != nil {
+		return nil, err
+	}
+	for name, isClosed := range closed {
+		if isClosed {
+			shut[name] = true
+		}
+	}
+	return shut, nil
+}
+
+// readDispatchShut is the shut set a claim re-reads. A retry the owner authorized
+// is the one dispatch to a closed backend, so it is refused by quota only.
+func readDispatchShut(r *store.Reader, at time.Time, policy *Policy, d Decision) (map[string]bool, error) {
+	if d.Action == ActionRetry {
+		return readQuotaOnlyShut(r, at, policy)
+	}
+	return readQuotaShutBackends(r, at, policy)
+}
+
+// No quota command means no reading-based hold. Refusal windows hold every backend.
+func readQuotaOnlyShut(r *store.Reader, at time.Time, policy *Policy) (map[string]bool, error) {
 	legacy, err := readShutBackends(r, at)
 	if err != nil {
 		return nil, err
diff --git a/tools/tillr-drain/drain/quota_window.go b/tools/tillr-drain/drain/quota_window.go
index e3c17d3d5..0d6b84fbd 100644
--- a/tools/tillr-drain/drain/quota_window.go
+++ b/tools/tillr-drain/drain/quota_window.go
@@ -153,7 +153,7 @@ func readBackendWindows(r *store.Reader, at time.Time) (map[string]time.Time, er
 }

 // Skipping a shut backend walks the same seat order as failover, without starting
-// a run or spending an item's failover budget. Cycles cannot make a shut backend
+// a run. Cycles cannot make a shut backend
 // eligible when an override falls back to the preference head.
 func (p Policy) availableBackend(name, seat string, shut map[string]bool) (string, string, bool) {
 	seen := map[string]bool{}
diff --git a/tools/tillr-drain/drain/schema.go b/tools/tillr-drain/drain/schema.go
index 56b5f6dd9..081c6aa81 100644
--- a/tools/tillr-drain/drain/schema.go
+++ b/tools/tillr-drain/drain/schema.go
@@ -78,6 +78,10 @@ const (
 	// ReasonBackendWindow holds an item when every backend on its route has a
 	// reported spent window; the reset time releases it without an operator retry.
 	ReasonBackendWindow = "backend-window"
+	// ReasonBackendClosed waits for recorded evidence that the backend works again.
+	ReasonBackendClosed = "backend-closed"
+	// ReasonCommandFault is a command process failure, not an agent backend route.
+	ReasonCommandFault = "command-fault"
 	// ReasonReady is a dispatch's reason: the item is queued, unblocked, and a lane and
 	// a policy were available.
 	ReasonReady = "ready"
@@ -145,8 +149,9 @@ const (
 	// ruling's degraded mode is an allowed, stamped run: the floor may produce the document,
 	// and this is the stamp that keeps it from being read as a full seat's work.
 	ReasonSeatDegraded = "seat-degraded"
-	// ReasonInfraExhausted is a hold's reason: the item's failover chain has no hop left.
-	// Either the policy's max_failovers budget is spent or the preference has no backend
+	// ReasonInfraExhausted is retained for historical records only. New infrastructure
+	// holds use ReasonBackendClosed and wait for evidence, not a budget.
+	// Previously the preference had no backend
 	// after the one that failed, and a chain is refused rather than walked on past what
 	// the policy allows.
 	ReasonInfraExhausted = "run-infra-exhausted"
diff --git a/tools/tillr-drain/drain/why.go b/tools/tillr-drain/drain/why.go
index 702bf15af..5ba746388 100644
--- a/tools/tillr-drain/drain/why.go
+++ b/tools/tillr-drain/drain/why.go
@@ -236,6 +236,8 @@ func Why(r *store.Reader, tick Pass, policy *Policy, questions QuestionSource) (
 			row.Mover = "restore " + ref + " under the item's block and land it"
 		case d.Reason == ReasonQuestionOpen && d.QuestionID != "":
 			row.WaitingOn = append(row.WaitingOn, d.QuestionID)
+		case d.Reason == ReasonBackendClosed && d.DeferredBy != "":
+			row.WaitingOn = append(row.WaitingOn, d.DeferredBy)
 		case (d.Reason == ReasonDeskCapacity || d.Reason == ReasonDocumentDeskBusy || d.Reason == ReasonForkHeld || d.Reason == ReasonTrunkBroken || d.Reason == "awaits-rewrite" || strings.HasPrefix(d.Reason, ReasonRedCap+": ")) && d.DeferredBy != "":
 			row.WaitingOn = append(row.WaitingOn, d.DeferredBy)
 		case d.Reason == ReasonBlocked:
@@ -477,6 +479,10 @@ func whyPresentation(d Decision) (class, where, mover string) {
 		return "needs you", "gate", "inspect the verdict; tillr drain retry --help"
 	case ReasonBoundaryScopeEscape, StopHostBoundaryViolation:
 		return "needs a cause", "fence", "inspect the boundary report"
+	case ReasonBackendClosed, ReasonInfraExhausted:
+		return "waits on work", "backend", "a successful tillr-drain probe-backend or a non-infrastructure run"
+	case ReasonCommandFault:
+		return "needs a cause", "lane", "repair the command process and authorize a retry"
 	case ReasonBackendWindow:
 		return "waits on work", "backend", "the backend reset time"
 	case ReasonQuestionRemoved:
diff --git a/tools/tillr-drain/drain/workflow_failover.go b/tools/tillr-drain/drain/workflow_failover.go
index dba18f5b1..211381e7d 100644
--- a/tools/tillr-drain/drain/workflow_failover.go
+++ b/tools/tillr-drain/drain/workflow_failover.go
@@ -23,18 +23,13 @@ func (l *Loop) retryWorkflowNode(ctx context.Context, d Decision, base string, r
 	if err != nil {
 		return err
 	}
-	if !ok || d.NodeInfraFailures >= l.cfg.Policy.failovers() {
-		if err := l.nodeOutcome(ctx, d, "fault"); err != nil {
-			return err
-		}
-		head := ""
-		if !l.nonLanding {
-			head = l.headOf(ctx, d.LaneID)
-		}
-		if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, "workflow node infrastructure exhausted"); stop != "" {
-			return retainedAfter(d.LaneID, stop)
+	if !ok {
+		// Persist a ready continuation even when the whole route is closed.
+		// Planning holds it until a recorded reopening, retaining this node/base.
+		request, _, _ = l.currentPolicy().NextRequest(d.ItemID, d.LaneID, d.RunRequest.RunID+"-failover", rec.Backend, d.Seat)
+		if request == nil {
+			return Usagef("workflow seat %s has no backend route", d.Seat)
 		}
-		return l.markExamined(ctx, d)
 	}
 	request.NodeID = d.Workflow.Stages[d.NodeIndex].ID
 	request.WorkingDir = d.RunRequest.WorkingDir
diff --git a/tools/tillr-drain/drain/workflow_order.go b/tools/tillr-drain/drain/workflow_order.go
index c9f431f23..c948e9a9f 100644
--- a/tools/tillr-drain/drain/workflow_order.go
+++ b/tools/tillr-drain/drain/workflow_order.go
@@ -88,7 +88,7 @@ func commandNodeHold(r *store.Reader, id string) (string, error) {
 		return ReasonBoundaryScopeEscape, nil
 	}
 	if outcome == "fault" {
-		return ReasonInfraExhausted, nil
+		return ReasonCommandFault, nil
 	}
 	// A successful harness with a missing declared file is not a failed task.
 	// Read the refusal for this producer, not the last sibling to finish.
diff --git a/tools/tillr-drain/holdrules/holdrules.go b/tools/tillr-drain/holdrules/holdrules.go
index d078fb631..06c4370d5 100644
--- a/tools/tillr-drain/holdrules/holdrules.go
+++ b/tools/tillr-drain/holdrules/holdrules.go
@@ -81,8 +81,11 @@ var Rules = map[string]Rule{
 	"fork-held":                    {"waits on work", "queue", "cause not recorded"},
 	"failover":                     {"ready", "queue", "the next pass"},
 	"seat-degraded":                {"ready", "queue", "the next pass"},
-	"run-infra-exhausted":          {"needs a cause", "lane", "cause not recorded"},
-	"harness-refused":              {"ready", "queue", "the next pass"},
+	"backend-closed":               {"waits on work", "backend", "a successful tillr-drain probe-backend or a non-infrastructure run"},
+	"command-fault":                {"needs a cause", "lane", "repair the command process and authorize a retry"},
+	// History only: an infrastructure failure now closes the backend, not the item.
+	"run-infra-exhausted": {"waits on work", "backend", "a successful tillr-drain probe-backend or a non-infrastructure run"},
+	"harness-refused":     {"ready", "queue", "the next pass"},
 	// A worker run's refused ending goes back to the item's worker with its evidence,
 	// uncounted (the owner's ruling of 2026-11-17).
 	"returned-with-evidence": {"ready", "queue", "the next pass"},
diff --git a/tools/tillr-drain/loop_test.go b/tools/tillr-drain/loop_test.go
index 83f9ccb9b..d25f13e68 100644
--- a/tools/tillr-drain/loop_test.go
+++ b/tools/tillr-drain/loop_test.go
@@ -560,15 +560,8 @@ func TestACandidateFailureDoesNotStopTheLoopAndDoesNotFailOver(t *testing.T) {
 	}
 }

-// Consecutive infrastructure failures do not stop the loop: it works every item it can and
-// ends where the pile ends. Nothing about an item was learned from a run that ran nothing, so
-// each item is held as `run-infra-exhausted` and each attempt is reported with its reason.
-//
-// This case is the removed circuit breaker's rewrite. It used to stop with
-// `infra-circuit-breaker` at the third failure, which is a stop with work still queued; it
-// now pins that three failures buy three attempts and no stop, and that no backoff was
-// applied — a tick that dispatched a run always has work to be getting on with, so the wait
-// (which belongs to a tick that dispatched nothing) never rations it.
+// An infrastructure failure closes the shared backend. Other queued items wait
+// without redispatching to it; a recorded successful probe permits one new attempt.
 func TestConsecutiveInfrastructureFailuresDoNotStopTheLoop(t *testing.T) {
 	f := newLoopFixture(t, stub(t, "adapter", failingAdapter))
 	fixtureAt(t, f.store,
@@ -591,24 +584,35 @@ func TestConsecutiveInfrastructureFailuresDoNotStopTheLoop(t *testing.T) {
 		t.Fatalf("the loop stopped %q (%s), want %q: a fault is not a budget",
 			result.Stop, result.Detail, drain.StopQueueDrained)
 	}
-	if result.InfraFailures != 3 || result.Ticks != 6 {
-		t.Fatalf("the loop saw %d infrastructure failure(s) over %d tick(s), want 3 over 3",
+	if result.InfraFailures != 1 {
+		t.Fatalf("the loop saw %d infrastructure failure(s) over %d tick(s), want one before closure",
 			result.InfraFailures, result.Ticks)
 	}
-	if result.Rejected != 0 || result.Dispatched != 3 {
-		t.Fatalf("the loop dispatched %d and rejected %d, want 3 and 0: a failed harness is not a verdict",
+	if result.Rejected != 0 || result.Dispatched != 1 {
+		t.Fatalf("the loop dispatched %d and rejected %d, want 1 and 0: a failed harness is not a verdict",
 			result.Dispatched, result.Rejected)
 	}
 	if gates := lines(t, f.count); countMatching(gates, "gate --item") != 0 {
 		t.Fatalf("the loop gated a run that failed in the infrastructure class: %v", gates)
 	}
-	// One report line per failure, each naming the attempt, and no wait: every tick here had
-	// an item to dispatch, so there was nothing for a backoff to make visible.
-	if countMatching(strings.Split(report.String(), "\n"), "failed in the infrastructure class") != 3 {
+	if countMatching(strings.Split(report.String(), "\n"), "failed in the infrastructure class") != 1 {
 		t.Fatalf("the loop did not report one line per infrastructure failure: %s", report.String())
 	}
-	if strings.Contains(report.String(), "nothing dispatched; waiting ") {
-		t.Fatalf("the loop backed off while it had items it could dispatch: %s", report.String())
+	// Starting another loop and moving its clock cannot reopen the backend.
+	closed := f.newLoopWith(t, 1, f.steps(t, gate, land, verdict),
+		passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, &report)
+	again, err := closed.Run(context.Background())
+	if err != nil || again.Dispatched != 0 {
+		t.Fatalf("closed backend retried: %+v / %v", again, err)
+	}
+	if err := drain.ProbeBackend(context.Background(), f.store, f.root, "fixture", []string{"/bin/true"}); err != nil {
+		t.Fatal(err)
+	}
+	reopened := f.newLoopWith(t, 1, f.steps(t, gate, land, verdict),
+		passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, &report)
+	afterProbe, err := reopened.Run(context.Background())
+	if err != nil || afterProbe.Dispatched != 1 {
+		t.Fatalf("reopened backend did not run once: %+v / %v", afterProbe, err)
 	}
 }

diff --git a/tools/tillr-drain/main.go b/tools/tillr-drain/main.go
index 1aa7a73a7..2cadef1b5 100644
--- a/tools/tillr-drain/main.go
+++ b/tools/tillr-drain/main.go
@@ -62,6 +62,8 @@ func dispatch(args []string) int {
 	}
 	face, rest := args[0], args[1:]
 	switch face {
+	case "probe-backend":
+		return probeBackendFace(rest)
 	case "workflow":
 		return workflowFace(rest)
 	case "why":
@@ -736,7 +738,7 @@ func failLoopStartup(root string, at time.Time, err error) int {

 // faceList is this binary's faces, in the order the usage lines print them. One home,
 // so the refusal, the unknown-face line and `--help` cannot disagree.
-const faceList = "workflow, why, policy, plan, once, retry, clear-fault, accept-rewrite, accept-identity, acknowledge-patch-move, sweep-refs, acquit, examine, set-aside, boundary, resume, stop, restart or loop"
+const faceList = "probe-backend, workflow, why, policy, plan, once, retry, clear-fault, accept-rewrite, accept-identity, acknowledge-patch-move, sweep-refs, acquit, examine, set-aside, boundary, resume, stop, restart or loop"

 func stopFace(args []string) int {
 	fs := newFlagSet("stop")
diff --git a/tools/tillr-drain/main_test.go b/tools/tillr-drain/main_test.go
index 41b2c6b3b..a37695573 100644
--- a/tools/tillr-drain/main_test.go
+++ b/tools/tillr-drain/main_test.go
@@ -1253,10 +1253,8 @@ func TestPlanDoesNotFailOverACandidateFailure(t *testing.T) {
 	}
 }

-// The budget: the policy says one failover, and a chain longer than that refuses rather
-// than walking. Two infrastructure failures in the item's history are one failover already
-// spent, so the item holds with the reason day-2's vocabulary names.
-func TestPlanHoldsWhenTheFailoverBudgetIsSpent(t *testing.T) {
+// Both backends have failed: no open backend exists until a recorded reopening.
+func TestPlanHoldsWhenEveryBackendIsClosed(t *testing.T) {
 	storePath := fixture(t,
 		itemRecord("item_042", "in-queue"),
 		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-8", "first", run.StateFailedToStart, nil, ""),
@@ -1265,17 +1263,16 @@ func TestPlanHoldsWhenTheFailoverBudgetIsSpent(t *testing.T) {
 	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, twoBackendPolicy(t, 1, nil))

 	got := plan(t, storePath, tickPath)[0].Payload
-	if got.Action != drain.ActionHold || got.Reason != drain.ReasonInfraExhausted {
-		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonInfraExhausted)
+	if got.Action != drain.ActionHold || got.Reason != drain.ReasonBackendClosed {
+		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonBackendClosed)
 	}
 	if got.RunRequest != nil {
-		t.Fatalf("a spent budget still dispatched to %q", got.RunRequest.Backend)
+		t.Fatalf("closed backends still dispatched to %q", got.RunRequest.Backend)
 	}
 }

-// The same chain with a budget of zero: the first infrastructure failure already exhausts
-// it, so the policy's number is what decides and not the code's.
-func TestPlanWalksNoFailoverWhenThePolicyBudgetIsZero(t *testing.T) {
+// A legacy zero budget cannot stop the walk to an open backend.
+func TestPlanIgnoresLegacyZeroFailoverBudget(t *testing.T) {
 	storePath := fixture(t,
 		itemRecord("item_042", "in-queue"),
 		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-9", "first", run.StateFailedToStart, nil, ""),
@@ -1283,8 +1280,8 @@ func TestPlanWalksNoFailoverWhenThePolicyBudgetIsZero(t *testing.T) {
 	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, twoBackendPolicy(t, 0, nil))

 	got := plan(t, storePath, tickPath)[0].Payload
-	if got.Action != drain.ActionHold || got.Reason != drain.ReasonInfraExhausted {
-		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonInfraExhausted)
+	if got.Action != drain.ActionDispatch || got.RunRequest == nil || got.RunRequest.Backend != "second" {
+		t.Fatalf("legacy zero budget changed routing: %+v", got)
 	}
 }

diff --git a/tools/tillr-drain/quota_window_test.go b/tools/tillr-drain/quota_window_test.go
index e66432435..5b46d67e7 100644
--- a/tools/tillr-drain/quota_window_test.go
+++ b/tools/tillr-drain/quota_window_test.go
@@ -97,11 +97,15 @@ func TestLoopDoesNotSkipBackendWithoutWindowFacts(t *testing.T) {
 			if _, err := f.newLoop(t, 1, steps).Run(context.Background()); err != nil {
 				t.Fatal(err)
 			}
-			for _, id := range []string{"item_051", "item_052"} {
-				runs := runsForItem(t, f.store, id)
-				if len(runs) != 2 || runs[0].Backend != "pi" {
-					t.Fatalf("legacy failure changed: %+v", runs)
-				}
+			// The failure records no window, and it closes pi: the first item fails
+			// over, and the second starts on the open backend.
+			first := runsForItem(t, f.store, "item_051")
+			if len(first) != 2 || first[0].Backend != "pi" || first[1].Backend != "fallback" {
+				t.Fatalf("first item did not fail over: %+v", first)
+			}
+			second := runsForItem(t, f.store, "item_052")
+			if len(second) != 1 || second[0].Backend != "fallback" {
+				t.Fatalf("second item was sent to the closed backend: %+v", second)
 			}
 		})
 	}
diff --git a/tools/tillr-drain/seat_test.go b/tools/tillr-drain/seat_test.go
index f2d1a19e5..e5894bf0b 100644
--- a/tools/tillr-drain/seat_test.go
+++ b/tools/tillr-drain/seat_test.go
@@ -299,11 +299,9 @@ func TestASeatDegradedFailoverIsNamedInTheLoopReport(t *testing.T) {
 	floor := stub(t, "floor-adapter", counting+committingAdapterBody+`printf 'RESULT: done\n'`)

 	f := newLoopFixture(t, floor)
-	one := 1
 	f.policy = &drain.Policy{
 		DefaultBackend: "pi",
 		Preference:     []string{"pi"},
-		MaxFailovers:   &one,
 		Seats:          map[string]string{"design": "pi-designer"},
 		Backends: map[string]drain.Backend{
 			"pi":          {Adapter: floor, Model: "floor-model", TimeoutSeconds: 60},
@@ -430,8 +428,8 @@ func TestPlanFailsAListedSeatOverAlongItsOwnList(t *testing.T) {
 		},
 	})
 	got = decisionFor(t, plan(t, spent, tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath)), "item_042")
-	if got.Action != drain.ActionHold || got.Reason != drain.ReasonInfraExhausted {
-		t.Fatalf("the item past its list's end was %q / %q, want a hold with %q", got.Action, got.Reason, drain.ReasonInfraExhausted)
+	if got.Action != drain.ActionHold || got.Reason != drain.ReasonBackendClosed || got.DeferredBy != "designer-a, designer-b" {
+		t.Fatalf("the item past its list's end was %q / %q waiting on %q, want a hold with %q on both designers", got.Action, got.Reason, got.DeferredBy, drain.ReasonBackendClosed)
 	}
 }

diff --git a/tools/tillr-drain/transition_test.go b/tools/tillr-drain/transition_test.go
index 0742f6ff7..56c4b7351 100644
--- a/tools/tillr-drain/transition_test.go
+++ b/tools/tillr-drain/transition_test.go
@@ -126,18 +126,20 @@ func TestPlanTransitionPayloadCountsTerminalRuns(t *testing.T) {
 		runRecord("run-exhausted-first-f1", "item_exhausted", "lane-9", "second", run.StateExited, exitCode(2), ""),
 		runRecord("run-failover-first", "item_failover", "lane-8", "first", run.StateFailedToStart, nil, ""),
 		runRecord(retryRun, "item_retry", "lane-9", "first", run.StateFailedToStart, nil, ""),
+		// A run that ended normally on the second backend leaves it open.
+		runRecord("run-landed-second", "item_landed", "lane-7", "second", run.StateExited, exitCode(0), ""),
 	)
 	seedRetry(t, storePath, "item_retry", retryRun, "the owner", "the harness could not start", "2026-11-09T01:00:00Z")
-	decisions, payloads := transitionPlan(t, storePath, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2", "lane-3"}, 3, transitionPolicy())
+	decisions, payloads := transitionPlan(t, storePath, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2", "lane-3", "lane-4"}, 4, transitionPolicy())
 	want := map[string]struct {
 		action, from, to string
 		attempt          int
 	}{
 		"item_dispatch":  {drain.ActionDispatch, "queued", "running", 1},
-		"item_exhausted": {drain.ActionHold, "held", "", 3},
+		"item_exhausted": {drain.ActionDispatch, "queued", "running", 3},
 		"item_failover":  {drain.ActionDispatch, "queued", "running", 2},
 		"item_hold":      {drain.ActionHold, "held", "", 1},
-		"item_landed":    {drain.ActionHold, "landed", "", 1},
+		"item_landed":    {drain.ActionHold, "landed", "", 2},
 		"item_retry":     {drain.ActionRetry, "queued", "running", 2},
 	}
 	if len(decisions) != len(want) {
diff --git a/tools/tillr-view/waiting.go b/tools/tillr-view/waiting.go
index a1e66ec33..939d90e9a 100644
--- a/tools/tillr-view/waiting.go
+++ b/tools/tillr-view/waiting.go
@@ -326,6 +326,9 @@ func assembleWaiting(r *store.Reader, questions vantage.QuestionSource, storePat
 			row.Since = time.UnixMilli(since).UTC().Format(time.RFC3339Nano)
 			if deferredBy != "" {
 				row.Mover = deferredBy
+				if last.Reason == "backend-closed" {
+					row.Mover += ": " + rule.mover
+				}
 			}
 			if last.Reason == "question-open" {
 				var excluded []string
diff --git a/tools/tillr-view/waiting_backend_closed_test.go b/tools/tillr-view/waiting_backend_closed_test.go
new file mode 100644
index 000000000..f4c252119
--- /dev/null
+++ b/tools/tillr-view/waiting_backend_closed_test.go
@@ -0,0 +1,40 @@
+package main
+
+import (
+	"context"
+	"database/sql"
+	"strings"
+	"testing"
+)
+
+func TestWaitingBackendClosedNamesBackendAndReopeningMover(t *testing.T) {
+	h := newServeHarness(t)
+	db, err := sql.Open("sqlite", "file:"+h.store)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer db.Close()
+	for _, q := range []string{
+		`ALTER TABLE dispatches ADD COLUMN deferred_by TEXT`,
+		`DELETE FROM dispatches`,
+		`INSERT INTO items(item_id,title,state,updated_at) VALUES ('item-backend','backend wait','in-queue',1000)`,
+		`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at,deferred_by) VALUES ('closed-backend','item-backend','hold','backend-closed',1000,'fixture')`,
+	} {
+		if _, err := db.Exec(q); err != nil {
+			t.Fatal(err)
+		}
+	}
+	rows, err := readWaiting(context.Background(), h.store, nil)
+	if err != nil {
+		t.Fatal(err)
+	}
+	for _, row := range rows {
+		if row.ItemID == "item-backend" {
+			if row.Class != "waits on work" || row.Where != "queue" || !strings.Contains(row.Mover, "fixture") || !strings.Contains(row.Mover, "probe-backend") || !strings.Contains(row.Mover, "non-infrastructure run") {
+				t.Fatalf("hold lacks mover: %+v", row)
+			}
+			return
+		}
+	}
+	t.Fatal("missing held item")
+}
# backend-open-patch end
````

## Filed 2026-11-18: plan first, then spend each model where it is enough

The owner asked on 2026-11-18 for a planning stage that fans an implementation out into many nodes and spends as little on models as it can: "split different types of work into different seats as we see fit", create "a few more seat names to slot into", fit Terra and Luna in wherever they can do the work, and encourage implementers who work alone to use cheaper sub-agents. This is the first slice of [the planning node and model tiering proposal](../design/tillr-planning-node-model-tiering.md), narrowed by the owner's later rulings, and it sits on [the item graphs design](../design/tillr-item-graphs.md) and the direction in [the graph-is-the-path design](../design/tillr-graph-is-the-path.md). It is entry 4's [choice of where each seat runs](#4-choose-where-each-seat-runs) applied to a finer grain of work.

### Add a planning stage that fans implementation out by seat

Today one worker on one model does a whole build item: it searches the tree, makes the edits, writes the tests, runs the checks and writes the docs, all at the price of the model that is on the build seat. Most of those steps do not need that model. Searching, listing, applying an edit that is already decided and running a named check are mechanical, and a cheap model does them as well. The owner's rule for model use is to put cheap models on well-specified implementation and mechanical work, to have a mid model verify it, and to keep the expensive model for root cause, design and risky core changes. The graph machinery that could express this is built and unused: workflow files, command nodes, per-node write scopes, parallel read-only nodes, and a first node that may propose replacing a later node that has not started. No item names a workflow, so no run has ever been a node run.

This work turns that machinery on for build items. A planning node reads the item and the tree first, then splits the work by kind into nodes, and each node names a seat. The seats become data in the backend policy instead of a fixed list of four, so a new kind of work is one policy entry and no code change. A join node on a stronger model then checks the pieces against the item's criteria before the gate. Implementers that still work alone are told, in their brief, which cheaper sub-agent seats exist and when to use them. Each node's record keeps which seat and model ran and what it cost, so the owner can compare what a landed item cost before and after.

Nothing here counts attempts. A node that cannot do its piece says so and names who acts next, and the drain routes it. There are no rungs, no re-plan caps and no time limits that fail work.

The work is seven items, split by the design seat on 2026-11-18 after four builds stopped on it. This block is the first and the smallest of them: the item store stops refusing a seat by a list and checks only the shape of its name. The two blocks below it have the planner and the workflow loader check a seat against the policy, and then declare the starting seats. The four after those add the piece list and the workflow, the verify node and the planner's guidance, the seat list in the implementer brief, and the cost figures. Each lands as its own commit and names the item it waits on.

- **Outcomes:**
  - Have sync and the item store accept any well-formed seat name and refuse a malformed one.



**Agent details:**

- **This item is small on purpose.** It changes five Go files and one README in `tools/tillr-item/`, and the pack mirror that `just pack-sync` writes. Four of the five Go files come from a saved patch that applies with one command. It changes nothing in `tools/tillr-drain/`, `config/tillr/` or any other module, though the directive's paths still list them from before the split. The policy keys, the six new seats and the planner's check are the next two items and are not this item's work. A report that lists them as missing describes other items.
- **Ref:** [the item graphs design](../design/tillr-item-graphs.md), decisions `D-IG3` and `D-IG12` and the block "Add a planning node's proposal to replace a later node"; [the graph-is-the-path design](../design/tillr-graph-is-the-path.md), `D-GP6` (seats are declared in the backend policy, with no rungs) and [`OQ-GP5`](../design/tillr-graph-is-the-path.md#OQ-GP5) (tillr watches every sub-agent and starts scouts and writers itself); [the planning and tiering proposal](../design/tillr-planning-node-model-tiering.md), sections 1 (what the run history says), 4 (kinds of work) and 5 (the planning node). The proposal's two rungs, one fix per rung and one re-plan are ruled out, and its items named `item-pt-*` are not dispatchable. Take its history and its kinds of work and none of its caps. These references serve all seven items, and this item needs none of them to be read.
- **Why the work is split, settled by the design seat on 2026-11-18.** Builds kept stopping on this block. It first held eight criteria across four modules, and three builds left the same four changed files behind. The design seat cut it to two criteria and named the tests the first step rewrites. The next build, on the last backend the build seat escalates to, applied the saved patch, ran `go test ./item -run 'Seat|Sync'` in `tools/tillr-item`, saw the one failure the block had told it to expect, and reported that the task exceeds it, listing the test rewrite, the planner, the workflow loader and the policies as missing. Its record shows four commands in 36 seconds. So the block is cut again, to the part that build had nearly finished. The design seat then made this item's whole change in a lane on 2026-11-18 at `81b180351`: the patch, the one test rewrite in step 2 and nothing else. `python3 tools/go-test-shards.py tools/tillr-item -k 6` passed 688 tests, and the same command for `tools/tillr-drain` passed 1996 with the drain untouched. The change was then discarded, because the design seat's commit is this document.
- **Where a seat is checked, settled by the design seat on 2026-11-18:** at plan time, against the policy in force, and not at sync. Sync and apply keep only the shape check in c1. Three facts in the tree decide it. Which policy is in force is the drain's `--policy` flag and it reloads while the loop runs, so sync cannot know it, and the four tracked policies do not have to agree. The policy type lives in `tools/tillr-drain`, which imports `tools/tillr-item`, so a check in sync would mean a second policy reader below the first. Apply replays recorded events, so a check there against today's policy file would refuse history when a seat is later renamed.
- **What holds between this item and the next.** Sync and apply accept a well-formed seat the code does not know, and the planner still refuses it with its existing message, through `item.KnownSeat` in `checkSeats` in `tools/tillr-drain/drain/plan.go`. That is a correct state: no item in the tree names a seat outside the four, and a refusal at plan time is where the next item leaves the check.
- **Out of scope for all seven items:** the full graph-is-the-path migration (every act a node, scheduling by node, dispatched children for every sub-agent); the storage design for graphs ([`OQ-GP2`](../design/tillr-graph-is-the-path.md#OQ-GP2)); any retry, rung, re-plan cap or timeout; and changing which model fills an existing seat.
- **The saved work.** `refs/tillr/preserved/run-item-plan-fans-out-by-seat-a14` in the main checkout holds four files of `tools/tillr-item/item/` as uncommitted work: a shape check in `seat.go`, its use in `sync.go` and `apply.go`, and `sync_test.go` changed to accept a well-formed unmapped name and refuse a malformed one. It is sound. On 2026-11-18 at `81b180351` the command in step 1 applied all four files cleanly. The refs of the same name ending `a7`, `a11` and `a12` hold the same four files. If none of them is there, make the four edits by hand as step 1 describes them.
- **Build order, each step a stop only if it fails.** Do the steps in order in one run. The end of a step is not an ending, and the commit is made once, at step 4.
  1. **Apply the saved patch.** From the lane worktree run `git -C /workspace diff 'refs/tillr/preserved/run-item-plan-fans-out-by-seat-a14^1' refs/tillr/preserved/run-item-plan-fans-out-by-seat-a14 -- tools/tillr-item | git apply -3 -`. It adds to `seat.go` a function value `validSeatName` that matches `^[a-z][a-z0-9-]*$` and a message constant `invalidSeatName` that says seats are declared in the backend policy file. It uses both in `checkSeat` in `apply.go` and in the `seat` attribute branch of `parseItemBlocks` in `sync.go`. In `sync_test.go` it changes the `invalid-seat` case to the malformed name `Architect` and has `TestSyncMapsTheSeat` store the seat `architect-2`. Leave `KnownSeat`, `SeatList`, the `seats` slice and the four constants in `seat.go` as they are.
  2. **Rewrite the one test that states the old rule.** `go test ./item` in `tools/tillr-item` now fails in exactly one test, `TestAnItemRefusesASeatThatDoesNotExist` in `seat_test.go`, with `an item asking for seat "qa" was accepted`. That failure is expected, and it is the whole of this step. Replace that test and the comment above it with one named `TestAnItemChecksOnlyTheShapeOfASeatName`. It opens the fixture store the same way. It applies `seatRecord(t, "item_042", "qa")` and fails if that returns an error or if `showSeat(t, s, "item_042")` is not `qa`. Then for each of `QA` and `2nd` it applies `seatRecord(t, "item_043", seat)` and fails unless the error is not nil, `item.ExitCode(err)` is 2, and the error text contains the seat in double quotes and the words `backend policy`. Every helper it needs is already used by the tests around it. Run `go test ./item` again; it passes.
  3. **Correct the README.** In `tools/tillr-item/README.md`, the bullet "A seat is a role, not a model" says an item names at most one of `design`, `build`, `review` or `scout`. Change it to say that an item names at most one seat, that the name is lowercase letters, digits and hyphens starting with a letter, and that the backend policy says which seats exist. The bullet "A document with a finding writes no definition" lists "an unknown attribute or seat"; change that to "an unknown attribute, a malformed seat name".
  4. **Finish.** Run `just pack-sync`, then `python3 tools/go-test-shards.py tools/tillr-item -k 6`, then `yolo pack lint yolo-packs/tillr` and `python3 tests/tillr-docs.test.py`. Make one commit holding the five Go files, the README and the mirror.
- **What does stop this build:** a failing test other than the one step 2 names, after step 2 is done; a step whose named function or file is not in the tree; or the patch failing to apply and the hand edits failing too. Report it with the step number, the command and its output. The failure step 2 names is not one of these, and neither is a report that lists steps not yet done or work that belongs to a later item.
- **If a decision is missing, report blocked** with one specific question. Where a seat is checked and what shape a name has are settled above and are not open questions.
- **Verify:** `go test ./...` in `tools/tillr-item/`, `yolo pack lint yolo-packs/tillr`, `just pack-sync`, and `uvx vantage-check docs/tillr/roadmap.md`.

### Move the seat check from a fixed list to the backend policy

After the item before this one, sync and the item store accept any well-formed seat name, and the drain still holds the fixed list of four: the planner, the policy loader and the workflow loader each ask `item.KnownSeat`. This item removes that list. The planner and the workflow loader refuse a seat the policy in force does not map, and each refusal names the policy file as the place a seat is declared, so adding a seat becomes one policy entry and no code change.

- **Outcomes:**
  - Make a seat any name the backend policy maps, with no seat list in code.
  - Name the policy file in every refusal of an unmapped seat.



**Agent details:**

- **Ref:** the references, the ruling on where a seat is checked and the out-of-scope list in [the first item of this split](#add-a-planning-stage-that-fans-implementation-out-by-seat), which this item waits on. That item leaves `validSeatName` and `invalidSeatName` in `tools/tillr-item/item/seat.go`, unexported, beside `KnownSeat` and `SeatList`.
- **Seats today:** on 2026-11-18 `rg -n 'KnownSeat|SeatList\(' tools` found `item.KnownSeat` called in three places in `tools/tillr-drain/drain/`: `checkSeats` in `plan.go`, `Policy.Check` in `policy.go` and `decodeWorkflow` in `workflow.go`. The two callers in `tools/tillr-item/item/` are gone once the first item has landed. The policy's `seats` map in `config/tillr/backend-policy*.json` already takes any name, but `Policy.Check` refuses one the code does not know.
- **The planner's check.** `checkSeats` in `plan.go` already refuses a plan that names a seat the policy does not map and already prints the seats it does map. Keep that refusal, drop the `KnownSeat` branch above it, and add the policy file's path to the message.
- **Where the workflow loader checks a seat.** The loader has two layers. `decodeWorkflow` in `workflow.go` reads one file and is given no policy; it holds the `KnownSeat` test. `expandWorkflow` in `workflow_check.go`, which `readWorkflow` calls with the policy, already refuses a stage whose seat the policy does not map, with the rule `workflow-unknown-seat` and the words "is not mapped by policy". So `decodeWorkflow` keeps only the shape check, under the same rule name, and `expandWorkflow` keeps its refusal and gains the policy file's path in the message. That check is made only when a policy is given.
- **How a refusal names the policy file.** `Policy` does not know which file it was read from: `decodePolicy` in `tools/tillr-drain/drain/why.go` receives the path as `ref` and does not keep it. Add a field `Ref string` to `Policy` with the tag `json:"-"`, set it in `decodePolicy`, and have each refusal say "seats are declared in" followed by that path. A policy built in a test with no path prints "the backend policy file" in its place.
- **Build order, each step a stop only if it fails.** Do the steps in order in one run. The end of a step is not an ending, and the commit is made once, at step 4.
  1. **The item module.** In `tools/tillr-item/item/seat.go`, turn `validSeatName` into an exported function `ValidSeatName(seat string) bool` so the drain can call it, and change its two callers in `apply.go` and `sync.go`. Delete `KnownSeat`, `SeatList` and the `seats` slice, keep the four constants, and rewrite the comment that says the vocabulary is closed: the item model checks a name's shape, and the backend policy says which seats exist. `go test ./item` in `tools/tillr-item` still passes. `tools/tillr-drain` does not compile until step 2 is done, and that is expected.
  2. **The three drain callers and the tests that state the old rule.** Each test below asserts the fixed list and is rewritten, not worked around.
     - Add `Ref` to `Policy` and set it in `decodePolicy`, as "How a refusal names the policy file" says.
     - In `plan.go`, drop the `KnownSeat` branch in `checkSeats` and add the policy path to the unmapped-seat message. `TestPlanRefusesASeatThatDoesNotExist` in `tools/tillr-drain/seat_test.go`, in the module's root package and not under `drain/`, writes the seat `qa` into the store and expects the words "does not exist" and the list "build, design, review, scout". Change its expectations to the words "which the policy does not map", the mapped seats it already expects, and the path of the policy file the test wrote. The comment on `setSeat` in that file says the item's own writer refuses a seat outside the vocabulary; correct it.
     - In `policy.go`, replace the `KnownSeat` refusal in `Policy.Check` with a refusal of a name that fails `item.ValidSeatName`, saying the name's shape and that seats are declared in the backend policy file. Two cases in `drain/policy_test.go` map the seat `desgn` and expect the words "which is not one of the item model's seats": the case "seat outside the vocabulary" and the case "an unknown seat". Change each to map the malformed name `Desgn` and to expect the new message.
     - In `workflow.go`, replace the `KnownSeat` test in `decodeWorkflow` with `item.ValidSeatName`, keeping the rule name. `TestWorkflowLoaderRules` in `drain/workflow_test.go` sets a stage's seat to `absent` and expects `workflow-unknown-seat` from `decodeWorkflow`; change the value to `Absent`. Add the policy path to the unmapped-seat finding in `expandWorkflow` in `workflow_check.go`.
     - `rg -n 'KnownSeat|SeatList\(' tools` must then find nothing. `rg -n "item model's closed" tools/tillr-drain/README.md` finds the one README sentence that states the old rule; rewrite it.
     - Run `go test . ./drain -run 'Seat|Policy|Workflow'` in `tools/tillr-drain`. A test that still fails because it expects the fixed list is rewritten to the rule in c1 in the same way.
  3. **The new test, in `drain/policy_test.go`.** A fixture policy file maps a seat named `architect-2`. The test asserts that `checkSeats` accepts an item naming it, that `readWorkflow` accepts a workflow node naming it, and that `checkSeats` and `readWorkflow` each refuse `architect-3` with the policy file's path in the message.
  4. **Finish.** Run `just pack-sync`, then `python3 tools/go-test-shards.py tools/tillr-item -k 6` and the same for `tools/tillr-drain`, then `yolo pack lint yolo-packs/tillr` and `python3 tests/tillr-docs.test.py`. Make one commit holding all of it.
- **What does stop this build:** a failing test that needs a file outside the item's paths, a step whose named function or file is not in the tree, or a contradiction in the criterion. Report it with the step number, the command and its output. A test that fails because it asserts the fixed list is not one of these, and neither is a report that lists the steps not yet done.
- **If a decision is missing, report blocked** with one specific question. Where a seat is checked and how a refusal names the policy file are settled above and are not open questions.
- **Verify:** `go test ./...` in each of `tools/tillr-item/` and `tools/tillr-drain/`, `yolo pack lint yolo-packs/tillr`, `just pack-sync`, and `uvx vantage-check docs/tillr/roadmap.md`.

### Add the starting seats to every backend policy

Once a seat is whatever the policy maps, the policies can name more kinds of work than the four they have. This item adds six seats to every tracked policy, with Terra and Luna on most of the work, and two policy keys: one that lists the seats that must not write and one that says in a sentence what each seat is for. A workflow node that writes on a read-only seat is refused when the workflow is loaded.

- **Outcomes:**
  - Declare six new seats, their purposes and the read-only ones in every tracked policy.
  - Refuse a writing workflow node on a seat the policy lists as read-only.



**Agent details:**

- **Ref:** the references and the out-of-scope list in [the first item of this split](#add-a-planning-stage-that-fans-implementation-out-by-seat), and the policy check as [the item before this one](#move-the-seat-check-from-a-fixed-list-to-the-backend-policy) leaves it: `Policy.Check` accepts any well-formed seat name, `expandWorkflow` in `workflow_check.go` refuses a stage on a seat the policy does not map, and `Policy` carries `Ref`, the path of the file it was read from.
- **The starting seats, settled by the design seat on 2026-11-18.** The four existing seats stay as they are: design on Fable 5.2, build on Terra, review on Sol, scout on Luna. Six seats are new. `plan` on Sol reads the item and the tree and writes the piece list. `edit` on Terra applies a settled edit named in the plan, such as a rename, a signature change at known call sites or a reformat. `test-write` on Terra writes tests from the criteria. `test-run` on Luna runs a named check command and reports its result without fixing it. `docs` on Terra writes the doc and README changes. `verify` on Sol is the join. In `config/tillr/backend-policy.bedrock.json` those are the backends `pi` for `plan` and `verify`, `pi-build` for `edit`, `test-write` and `docs`, and `pi-scout` for `test-run`. A piece that is real implementation goes to `build`, and read-only searching goes to `scout`.
- **Three questions the earlier text left open, now closed.** Luna does not write in a tillr node. [`OQ-SP6`](../design/seat-profiles.md#OQ-SP6) found it fit only for single-turn infilling and shallow search, and the owner's instruction of 2026-11-18 put it on the scout seat, which is read-only, so both stand: Luna fills `scout` and `test-run`, the policy lists those two under `read_only_seats`, and c1 makes that mechanical for a workflow file, as the piece list item does for a planner's proposal. That is why `edit` is on Terra. It stays its own seat, apart from `build`, so that moving it to a cheaper model later is one policy edit made on a measurement from the cost item below. `edit` and `test-run` stay two seats, because one writes and the other must not. `plan` has no second seat on the design model: a planner that meets a design decision reports blocked, and that is the route to the design model. None of the seven items edits `yolo-packs/seat-profiles/`, so Luna stays out of every pi sub-agent set.
- **Build order, each step a stop only if it fails.** Do the steps in order in one run. The end of a step is not an ending, and the commit is made once, at step 4.
  1. **The two policy keys.** Add `ReadOnlySeats`, a list under `read_only_seats`, and `SeatPurposes`, a map under `seat_purposes`, to `Policy` in `tools/tillr-drain/drain/policy.go`. `Policy.Check` refuses an entry of either that names a seat the policy does not map, and the refusal names the key and the seat. Test: one case for each key in `drain/policy_test.go`.
  2. **The read-only check.** Add it to `expandWorkflow` in `workflow_check.go` beside the unmapped-seat finding, under a new rule, `workflow-read-only-seat-writes`: a stage whose `access` is `write` on a seat the policy lists under `read_only_seats` is refused. The check is made only when a policy is given. Add the rule name to the list of graph rules in the `switch` in that file that holds `workflow-unknown-seat`, and to the list of rules in that file's opening comment. Test: `readWorkflow` refuses a `write` node on a read-only seat of a fixture policy.
  3. **The four policy files.** Add to each of the four `config/tillr/backend-policy*.json` files the six seats, `read_only_seats` holding `scout` and `test-run`, and a `seat_purposes` sentence for each of the ten seats, taken from the starting-seats paragraph. The bedrock file uses the backends that paragraph names. In each other file, `plan` and `verify` take the value its `review` seat has, `edit`, `test-write` and `docs` take the value of its `build` seat, and `test-run` takes the value of its `scout` seat. A value that is a list is copied as a list. Test: one loads every `backend-policy*.json` under `config/tillr/` through `LoadPolicy` and asserts the ten seats are mapped, and that in the bedrock file no seat outside `read_only_seats` has `pi-scout` in its list. Run `go test . ./drain -run 'Seat|Policy|Workflow'` in `tools/tillr-drain`.
     - **The Claude policy's Python test, settled by the design seat on 2026-11-18.** A worker reported that `test_claude_policy` in `tests/tillr-muse-backend.test.py` compares the whole `seats` map of `config/tillr/backend-policy.claude.json` to a literal holding the four existing seats, so it fails once the file maps ten, and that the test file was outside the item's paths. The paths now hold that one file. Rewrite the literal in `test_claude_policy` to the ten seats that step gives the Claude file: the four existing entries unchanged, `plan` and `verify` equal to `review`, `edit`, `test-write` and `docs` equal to `build`, and `test-run` equal to `scout`. Keep the comparison exact and change nothing else in that file. Run `python3 tests/tillr-muse-backend.test.py`.
  4. **Finish.** Update `tools/tillr-drain/README.md` for the two policy keys and the new rule. Run `just pack-sync`, then `python3 tools/go-test-shards.py tools/tillr-drain -k 6`, then `yolo pack lint yolo-packs/tillr`, `python3 tests/tillr-docs.test.py` and `python3 tests/tillr-muse-backend.test.py`. Make one commit holding all of it.
- **What does stop this build:** a failing test that needs a file outside the item's paths, a step whose named function or file is not in the tree, or a contradiction in the criterion. Report it with the step number, the command and its output. A report that lists the steps not yet done is not one of these.
- **If a decision is missing, report blocked** with one specific question. The seat set, the Luna conflict and the two policy keys are settled above and are not open questions, and no seat semantics are invented.
- **Verify:** `go test ./...` in `tools/tillr-drain/`, `python3 tests/tillr-muse-backend.test.py`, `yolo pack lint yolo-packs/tillr`, `just pack-sync`, and `uvx vantage-check docs/tillr/roadmap.md`.

### Add a piece list argument and the planned-build workflow

A planning node can already propose replacing a later node that has not started, but only with a list of scout prompts, a subset of the fence or a registered command. It cannot hand over a list of pieces that each name a seat and a write scope, so a planner has no way to cut a build into parts. This item adds that argument and the tracked workflow that uses it. It builds no scheduler, because the drain already runs writers one at a time and readers together.

- **Outcomes:**
  - Let a planner's proposal carry a list of pieces, each with a seat, an access, a write scope and outputs.
  - Add the `planned-build@v1` workflow and the template its plan node expands.
  - Refuse a piece on a seat the policy does not map, outside the fence, or writing on a read-only seat.



**Agent details:**

- **Ref:** the references and the out-of-scope list in [the first item of this split](#add-a-planning-stage-that-fans-implementation-out-by-seat), and the starting seats in [the item that declares them](#add-the-starting-seats-to-every-backend-policy), which this item waits on. By then a seat is a policy name, `Policy` carries `Ref`, the policy file's path, and the policy has `read_only_seats`.
- **What a planning node can already do:** per the item graphs design, a read-only node may propose replacing one later node that has not started with a tracked workflow and bounded arguments, applied in one transaction, with the old definition kept in `graph_revisions`. The bounded form is a list of scout prompts, a subset of the fence, or a registered command id. Check that built behavior in `tools/tillr-drain/drain/graph_proposal_test.go` and `workflow_expansion_test.go`. The gaps are an argument that is a list of pieces each carrying a seat, a scope and named outputs, and a template that expands to several writing nodes. `workflow="..."` in the directive is the existing way to slot an item into a workflow.
- **First use is deferred, settled by the design seat on 2026-11-18.** A worker reported that the roadmap and `config/tillr/` name no item to run through the workflow: `rg -n --hidden 'workflow="planned-build@v1"' docs config/tillr` returned nothing. That is right and it stays so. This item adds `config/tillr/workflows/planned-build@v1.json` and proves it with fixture tests, and it puts `workflow="planned-build@v1"` on no item's directive and changes no default. Which item runs through the workflow first is a later roadmap edit that the design seat or the owner makes after the split has landed, and a default for build items is a later settings change.
- **Writing pieces do not run in parallel, settled by the design seat on 2026-11-18.** An earlier criterion had the drain run writing pieces with disjoint scopes at the same time. That contradicted [`OQ-IG3`](../design/tillr-item-graphs.md#71-decision-ledger), which settles that read-only nodes of one item run together and writing nodes run one at a time on the item's single worktree, and the tiering proposal's own list of what it does not do says the same. The tree agrees: `claimParallel` in `tools/tillr-drain/drain/parallel.go` returns at once for a node whose access is not `read_only`, under the comment "A writer always keeps the lane to itself". `nextWorkflowNode` in `workflow_order.go` already picks the first ready node in stage order, so writing pieces expanded in plan order run in plan order with no added edges. Running writers together would overturn that decision, which the owner may overturn and this item does not.
- **How the pieces reach the graph, settled by the design seat on 2026-11-18.** The bounded-argument code takes three types today, `paths`, `strings` and `command`, and `bindWorkflow` in `workflow_arguments.go` refuses a `repeat` on anything except a `strings` argument and a read-only stage, with the message `workflow-repeat`. This item adds one type, `pieces`, and nothing else to the format. A `pieces` value is a list of at most `most` objects, and each object has exactly these keys: `id`, which follows the rule for an output name; `seat`; `access`, which is `read_only` or `write`; `write_scope`, a list of paths that is required for `write` and refused for `read_only`; `prompt`, a string; and `outputs`, an optional map from output name to type. An unknown key is refused. A stage whose `repeat` names a `pieces` argument becomes one copy per piece, in list order, through the code that already makes the copies for a `strings` argument, and each copy takes its seat, access, write scope, prompt and outputs from its piece. The tracked template is `config/tillr/workflows/build-pieces@v1.json`: one stage that repeats over `pieces` with `most` 12, and an exit that requires it with `commit_required`. `planned-build@v1` has three stages: `plan`, read-only on the `plan` seat, with a `graph.proposal` output and `may_replace` naming `work` and `build-pieces@v1`; `work`, a writing node on the `build` seat, which is what runs when the planner proposes nothing; and `verify`, read-only on the `verify` seat, named as the exit's `evaluator_slot`. The verify node is therefore the evaluator the drain already wires, and this item gives it no prompt of its own. The next item does.
- **Where each refusal is made.** `settleProposal` in `graph_proposal.go` records a refused proposal with a rule and a detail in `graph_revisions`, and already has `proposal-scope-outside-fence`. Add two rules beside it: `proposal-seat-unmapped` for a piece whose seat the policy in force does not map, with the policy file's path in the detail, and `proposal-read-only-seat-writes` for a `write` piece on a seat the policy lists under `read_only_seats`.
- **Build order, each step a stop only if it fails.** Do the steps in order in one run, and make the commit once, at step 3.
  1. **The `pieces` argument and the two workflow files (c1, c3).** Read `TestProposalExpandedInputFanout` in `graph_proposal_test.go` and `TestWorkflowExpansionArgumentsAndCopies` in `workflow_expansion_test.go` first; they show how a proposal with a `strings` argument becomes copies. Add the `pieces` type to `bindWorkflow`, to `workflowCheckArguments`, which needs a representative value for the file check, and to the copy expansion, which `rg -n 'CopiesOf' tools/tillr-drain/drain` finds. Add the two refusal rules to `settleProposal`. Add the two workflow files. Tests: a fixture planner writes a proposal with three pieces, and the test asserts the graph afterwards holds three copies with the seats and scopes given, and that each of the three bad proposals in c1 is recorded in `graph_revisions` with its rule and leaves the graph unchanged. The test for c3 reads the two workflow files and every `backend-policy*.json` under `config/tillr/`.
  2. **Order of the pieces (c2).** No production code is expected here. Write the test c2 states on the loop fixtures that `workflow_loop_test.go` and `parallel_semantics_test.go` use. If the writers do not start in plan order, the defect is in the copy order from step 1 and is fixed there.
  3. **Finish.** Update `tools/tillr-drain/README.md` for the `pieces` type, the two refusal rules and the two workflow files. Run `just pack-sync`, `python3 tools/go-test-shards.py tools/tillr-drain -k 6`, `yolo pack lint yolo-packs/tillr` and `python3 tests/tillr-docs.test.py`. Make one commit holding all of it.
- **What does stop this build:** a failing test that needs a file outside the item's paths, a step whose named function or file is not in the tree, or two criteria that cannot both hold. Report it with the step number, the command and its output. A report that lists the steps not yet done is not one of these.
- **If a decision is missing, report blocked** with one specific question. The deferred first use, the order writing pieces run in and the shape of the `pieces` argument are settled above and are not open questions.
- **Verify:** `go test ./...` in `tools/tillr-drain/`, `yolo pack lint yolo-packs/tillr`, `just pack-sync`, and `uvx vantage-check docs/tillr/roadmap.md`.

### Add the verify node's check and the planner's guidance

Once a plan can cut a build into pieces, nothing yet checks that the pieces together cover what the item asked for, and nothing tells the planner which model suits which kind of work. This item gives the verify node its question, sends an uncovered criterion back to the planner by name, and puts the owner's model-use rule in a file the planner's brief includes. The gate and the judge still decide landing.

- **Outcomes:**
  - Have the verify node name, for each criterion, the piece that satisfies it.
  - Send a criterion no piece covers back to the plan node by name.
  - Put the model-use rule in a tracked file the owner edits without a code change.



**Agent details:**

- **Ref:** the references and the out-of-scope list in [the first item of this split](#add-a-planning-stage-that-fans-implementation-out-by-seat), and the workflow as [the item before this one](#add-a-piece-list-argument-and-the-planned-build-workflow) leaves it: `verify` is already the stage the exit of `planned-build@v1` names as its `evaluator_slot`.
- **What the verify node is, settled by the design seat on 2026-11-18.** A worker reported that this block described machinery the tree does not have, and it was right. The block said the evaluator "is given the criteria, the diff and the outputs" and that a failing file takes a route back to the planner. Read at `d5696e5c5`: `planned-build@v1` wires no output to `verify`, and `nodeInputPaths` in `node_outputs.go` skips an edge with no `to_input`. The verify node's brief is written by `stageJudge` in `judge.go` and never passes through `workflowNodeBrief`, so a stage prompt does not reach it. A `not-done` finding is recorded by `settleJudgeFailure` as `eval-failed`, which `retryHoldReason` in `plan.go` answers as a hold and `returnedReasons` in `returned.go` does not list, so nothing dispatches a new plan. The criteria stand as written. The four paragraphs below say how each part is built, and none of them adds a second judge step, a second brief writer or a second reader of `eval.output`: [the evaluator block of the item graphs design](../design/tillr-item-graphs.md#route-a-workflows-evaluator-node-through-the-judges-intake) settles that there is one of each.
- **How the prompt and the pieces reach the verifier.** `evaluatorCandidate` in `workflow_evaluator.go` holds the decision, so it holds the graph as the planner left it. Add one string field to `Candidate`, `EvaluatorTask`, and the same field to `JudgeReceipt` under the JSON name `evaluator_task`, copied where `judgeCandidate` copies `EvaluatorInputs` into the receipt. `evaluatorCandidate` fills it with the evaluator stage's `prompt`, then one line for each node it appends to `Producers`, in that order: the node's id, seat and access, its prompt, and its output directory, which is `nodeOutputsDir` of the node's run. `stageJudge` appends the field to the brief it already writes, after the `Input` lines and only when the field is not empty, so the brief of an item judged without a workflow is unchanged. No edge, input or output is added to `planned-build@v1`. The joined diff is the `candidate.diff` the brief already names.
- **Where the verifier names the piece.** The verdict file keeps its format, and `tools/tillr-item/` is not changed. `CheckJudge` accepts any evidence entry that is one token with no space, so for a `done` entry the verifier puts `node:<id>` in `evidence`, with the id of the piece that satisfies the criterion. A criterion no piece covers gets `not-done` with a reason that starts `uncovered:`. The stage's prompt in `planned-build@v1.json` states both. The drain does not check that an entry carries a `node:` locator. The check is the verifier's own judgment, as the criterion says.
- **How a `not-done` finding goes back to the planner.** [`D-JL10`](../design/tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10) sends a refused candidate back with its evidence every time, and a verifier's `not-done` is a statement about the candidate of the same kind as a gate refusal. So when the judge step was entered through a workflow's evaluator node, which is when `Candidate.Evaluator` is set, `settleJudgeFailure` records a `not-done` outcome under a new refusal step, `verify-not-done`, in place of `eval-failed`. An item judged without a workflow keeps the `eval-failed` hold, and `blocked` and `defective` keep their routes. The new step is added to `returnedCauses` and `returnedReasons` in `returned.go` and gets its own sentence in `returnedBrief`. The refusal's detail is one line for each `not-done` entry of the verdict file, `<criterion id>: <reason>`, in criterion order. Its `evidence` field is `endingKey` of the step and the sorted ids of those criteria, so that the same criteria found uncovered a second time is an identical repeat and goes to the design seat as any repeat does. The refusal carries no `worker_session`. With none, the planner does not continue a retained node, and the next dispatch starts the item's workflow at its first stage on a fresh run. In `planned-build@v1` that stage is `plan`. `Stage` already appends `returnedBrief` to that run's brief, so the planner reads the named criteria under "Your previous attempt". The brief's "Facts carried" section is filled from the template in `tools/tillr-run/`, which is outside this item's paths and is not changed. `planned-build@v1` is the only tracked workflow with an `evaluator_slot`, and no item runs through it yet, so no queued item changes route.
- **What was read and what was not.** The paragraphs above were read from the functions they name. Two things were not run. The first is that the planner's `latest` run for the item is the verify node's run, which is the run the refusal event is keyed by and so the one `returnedEnding` reads the detail from. The second is that `markExamined`, which `finishEvaluatorHold` already calls, finds the repeat through `readRepeatedEnding`. The test in step 1 decides both. If the detail does not reach the plan brief or the repeat is not found, the fix is in `returnedEnding` or `readRepeatedEnding` in this module, and it is this item's work.
- **Model-use rule the planner's guidance carries:** cheap models for well-specified implementation and mechanical work (searching, listing, extracting, reformatting, applying a settled edit, running a named check and reporting), verified by a mid model; the expensive model only for root cause, design and risky core changes. Keep one effort level across all the nodes of one fan-out stage, because siblings share a prompt cache only when model, effort and tools match. Put the text in a tracked file the plan node's brief includes, so the owner edits it without a code change.
- **Build order, each step a stop only if it fails.** Do the steps in order in one run, and make the commit once, at step 3.
  1. **The verify node (c1).** Read `TestWorkflowEvaluatorJudgeRoute` and `TestWorkflowEvaluatorHoldCleanupReplay` in `workflow_evaluator_test.go` for the existing route. Add the `prompt` to the `verify` stage of `planned-build@v1.json`: for each criterion, write `node:<id>` of the piece that satisfies it in the entry's `evidence`, and give every criterion that no piece covers `not-done` with a reason that starts `uncovered:`. Add `EvaluatorTask` and fill it as settled above. Add the `verify-not-done` step and its return as settled above. A test that asserts `eval-failed` for a `not-done` finding of an evaluator node, such as the `not-done` case of `TestWorkflowEvaluatorJudgeRoute`, is changed to the new step, and a test of an item judged without a workflow is left as it is. Tests: the verify node's staged brief holds the stage's prompt and one line for each piece with its id and output directory. A fixture verifier's file marks `c2` `not-done`, and the test asserts the item's next planned decision is a dispatch of node `plan` with the reason `returned-with-evidence`, and that the brief staged for it names `c2`. A second identical finding raises the design seat's conflict and no third dispatch.
  2. **The planner's guidance (c2).** Put the model-use rule in a tracked file, `config/tillr/plan-guidance.md`, and have `workflowNodeBrief` in `node_brief.go` include it in the brief of a node on the `plan` seat. A missing file adds nothing to the brief and is not an error, so a fixture repository without one still stages. The `plan` stage's prompt tells the planner, when a piece cannot be cut without a design decision, to write no proposal and end its report with `OBSTACLE: owner`, a `QUESTION:` line holding the one specific question, and `RESULT: blocked`. A blocked report of a node that is not a command already takes the route `reportedBlock` in `blocked.go` gives it, which raises a conflict for the design seat; the test asserts the brief holds the guidance text and that a fixture planner's blocked report leaves the item waiting on design with no graph revision recorded.
  3. **Finish.** Update `tools/tillr-drain/README.md` for the verify prompt, the `verify-not-done` return and the guidance file, in its section on the workflow evaluator and its section [A refused run goes back to its worker](../../tools/tillr-drain/README.md#a-refused-run-goes-back-to-its-worker). Add one row to the table in [Section 4.4 of the judge design](../design/tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10), after row 1a, for a workflow evaluator's `not-done`, marked built, and run `uvx vantage-check` on that file. Run `just pack-sync`, `python3 tools/go-test-shards.py tools/tillr-drain -k 6`, `yolo pack lint yolo-packs/tillr` and `python3 tests/tillr-docs.test.py`. Make one commit holding all of it.
- **What does stop this build:** a failing test that needs a file outside the item's paths, a step whose named function or file is not in the tree, or two criteria that cannot both hold. Report it with the step number, the command and its output. A report that lists the steps not yet done is not one of these.
- **If a decision is missing, report blocked** with one specific question, and do not invent seat semantics. How the verifier gets its prompt and the pieces, where it names a piece, and how a `not-done` finding returns to the planner are settled above and are not open questions.
- **Verify:** `go test ./...` in `tools/tillr-drain/`, `yolo pack lint yolo-packs/tillr`, `just pack-sync`, and `uvx vantage-check docs/tillr/roadmap.md`.

### Add the policy's seats to the implementer brief

A worker that builds an item alone does every step on its own model, and its brief says nothing about the seats tillr has or about the cheaper sub-agents its own harness offers. This item adds one section to the brief that lists the policy's seats and tells the worker to hand mechanical work to those sub-agents. The two are different things, and the section says so: a seat is something tillr runs as a node, and a worker cannot start one.

- **Outcomes:**
  - List every seat the policy maps in the implementer brief, with the read-only ones marked.
  - Tell a worker to give mechanical work to its own harness's cheaper sub-agents.



**Agent details:**

- **Ref:** the references and the out-of-scope list in [the first item of this split](#add-a-planning-stage-that-fans-implementation-out-by-seat). This item waits on [the item that declares the starting seats](#add-the-starting-seats-to-every-backend-policy), which adds the policy keys this one reads: `read_only_seats`, and `seat_purposes`, a map from seat name to one sentence. A seat with no purpose prints its name alone.
- **A policy seat is not a sub-agent choice, settled by the design seat on 2026-11-18.** An earlier criterion had the pi adapter make the policy's seats available to a worker's own sub-agents, and a worker reported that this contradicts keeping Luna out of every pi sub-agent set. It does, and the criterion was rewritten. There are two vocabularies. A tillr seat is a name the backend policy maps to a backend, and tillr starts a run on it as a node. A pi worker's own sub-agents are pi's agents, such as its scout and its worker, and `yolo-packs/seat-profiles/files/seat-profiles.json` pins their models through the set in force. That file excludes Luna from every set and has no entry for a tillr seat name, so passing policy seats through would need a second model registry beside it, and this item adds none. The adapter therefore passes nothing, no seat is filtered into an "eligible" subset, and the brief lists all of the policy's seats with the read-only ones marked. Starting a worker's sub-agent on a tillr seat is the dispatched-children work under [`OQ-GP5`](../design/tillr-graph-is-the-path.md#OQ-GP5), which is out of scope.
- **Where to look for the implementer brief:** `tools/tillr-run/backends/brief-template.md` (the nine sections every adapter requires) and `tools/tillr-run/backends/pi/run-pi`, which sets `PI_SUBAGENTS_TEMP_ROOT` and the child-session marker for pi's own sub-agents. `yolo-packs/seat-profiles/` decides which models a worker's own sub-agents may use, and this item reads it and does not edit it or `run-pi`. The template is filled in `tools/tillr-drain/drain/stage.go`, in the module that already holds the policy, so the filler takes the seat names, the purposes and the read-only list from the policy in force. Add one section to the template for the seat lines and the fixed paragraph, and keep the paragraph free of any harness's agent names, since the template serves every adapter.
- **The template has a second filler, settled by the design seat on 2026-11-18.** A worker reported that `filled_brief` in `tests/tillr-muse-backend.test.py` reads `brief-template.md` and replaces only the placeholders in its fixed `values` map, so a new placeholder reaches `preflight.sh` unfilled and is refused. That is right: `tools/fork-check.py` runs that test for any backend change, and on 2026-11-18 `rg -ln 'brief-template' tests` found two more files, `tests/tillr-docs.test.py` and `tests/pack-sync.test.py`, neither of which fills a placeholder or counts the sections. The item's paths include that one test file, for the one map entry step 1 names, because the template and its fixture must change in the same commit for the gate to pass.
- **Build order, each step a stop only if it fails.** Make the commit once, at step 2.
  1. **The implementer brief (c1).** Add one section, `## Seats`, to `tools/tillr-run/backends/brief-template.md`, with a placeholder for the seat lines and the fixed paragraph, and fill the placeholder in `renderBrief` in `stage.go` from the policy in force. `tools/tillr-run/backends/preflight.sh` holds the list of the template's sections and refuses a brief that lacks one, so add the heading there and correct the count in its messages and in the READMEs that `rg -ln 'nine sections|all nine' tools` finds. Do not change `tools/tillr-run/backends/pi/run-pi`. The Muse acceptance test fills the same template from a fixed map: add the new placeholder's name and a fixture value to `values` in `filled_brief` in `tests/tillr-muse-backend.test.py`, change nothing else in that file, and run `python3 tests/tillr-muse-backend.test.py`. Tests as c1 states.
  2. **Finish.** Run `just pack-sync`, `python3 tools/go-test-shards.py tools/tillr-drain -k 6` and the same for `tools/tillr-run`, `yolo pack lint yolo-packs/tillr` and `python3 tests/tillr-docs.test.py`. Make one commit holding all of it.
- **What does stop this build:** a failing test that needs a file outside the item's paths, a step whose named function or file is not in the tree, or a contradiction in the criterion. Report it with the step number, the command and its output. A report that lists the steps not yet done is not one of these.
- **If a decision is missing, report blocked** with one specific question. What the brief and the adapter do with seats is settled above and is not an open question.
- **Verify:** `go test ./...` in each of `tools/tillr-drain/` and `tools/tillr-run/`, `python3 tests/tillr-muse-backend.test.py`, `yolo pack lint yolo-packs/tillr`, `just pack-sync`, and `uvx vantage-check docs/tillr/roadmap.md`.

### Add cost per landed item and per seat to drain why

The point of spreading work across seats is to spend less, and today nothing prints what a landed item cost or which seat the money went to. The runs already record each run's backend and cost. This item records the seat and the model on each node run and prints the two figures, with a baseline taken before any item has run through the planning workflow, so the owner can compare before and after.

- **Outcomes:**
  - Record each node run's seat, backend, model and cost.
  - Print cost per landed item and per seat, and the median of the last twenty landed build items.



**Agent details:**

- **Ref:** the references and the out-of-scope list in [the first item of this split](#add-a-planning-stage-that-fans-implementation-out-by-seat). This item waits on [the piece list item](#add-a-piece-list-argument-and-the-planned-build-workflow) because both change how `workflow.go` starts a node, and because that item adds the workflow whose name this one counts.
- **Where cost is recorded:** `tools/tillr-run/run/` (spend and usage), `tools/tillr-view/lane_nodes.go` and the `node_runs` rows in `tools/tillr-drain/drain/dispatches.go` and `workflow_order.go`. Start the measurement from what the runs already record before adding a field.
- **No real item on the workflow is needed.** First use of `planned-build@v1` is deferred, as the piece list item says. The test runs on fixture run records, this item's report states the baseline only, and the output prints the second figure once ten items have landed through the workflow.
- **Build order, each step a stop only if it fails.** Make the commit once, at step 2.
  1. **Cost (c1).** `runs` already holds `backend` and `usage_cost_usd` for every run, and `node_runs` ties a run to its node. Record the node's seat and the backend's model on the node run when it starts, in `startNode` in `workflow.go`, as new columns registered the way `tools/tillr-run/run/schema.go` registers `usage_cost_usd`. Add the two figures to `tillr drain why` in `tools/tillr-drain/why.go`: cost per landed item, and cost per seat, with a build item that ran no workflow counted on its item's seat or on `build` when the item names none. A sum that includes a run with no cost prints `unknown`. The median of the last twenty landed build items is one more line of the same output. Run it once against the repository's store, read-only, and put the number it prints in the run's report as the baseline.
  2. **Finish.** Update `tools/tillr-drain/README.md` for the new output. Run `just pack-sync`, the sharded suites for `tools/tillr-drain`, `tools/tillr-run` and `tools/tillr-view`, `yolo pack lint yolo-packs/tillr` and `python3 tests/tillr-docs.test.py`. Make one commit holding all of it.
- **What does stop this build:** a failing test that needs a file outside the item's paths, a step whose named function or file is not in the tree, or a contradiction in the criterion. Report it with the step number, the command and its output. A report that lists the steps not yet done is not one of these.
- **If a decision is missing, report blocked** with one specific question.
- **Verify:** `go test ./...` in each of `tools/tillr-drain/`, `tools/tillr-run/` and `tools/tillr-view/`, `yolo pack lint yolo-packs/tillr`, `just pack-sync`, and `uvx vantage-check docs/tillr/roadmap.md`.

## Filed 2026-11-18: a landing is a suspended tool call

[The landing-call design](../design/tillr-landing-call.md) proposes that a worker ends its run with
one tool call, `land`, and hears back only when something needs fixing: a refusal, a squash
conflict or a revert arrives as that call's result in the worker's own session. The owner asked for
it on 2026-11-18 and ruled that the gate's contents are a separate conversation. Its
[experiment](../design/tillr-landing-call.md#111-what-the-design-leans-on-that-nobody-ran) ran the
same day, and the fork's replay of an unanswered tool call holds. This section files five of the
design's build steps; step five is drafted in the design and not filed, and its worktree question,
[`OQ-LC1`](../design/tillr-landing-call.md#OQ-LC1), is blocked until worktrees belong to items. The pi build that sits
behind them is [the fork patch item](#add-park-open-without-prompt-and-a-features-flag-to-our-pi-fork),
and the host steps of the design's Section 6.6 gate only the switch, not these items: until the
installed pi prints the feature name, every adapter keeps the footer.

### Route a revert and a squash conflict to the worker's session

When the full suite proves that a landed change broke the trunk, or a change no longer applies to
the trunk, the worker that is told about it today has never seen the change: tillr starts a fresh
worker with the evidence in a new brief. A refused run already goes back to its own worker,
because the drain records the worker's session when it records the refusal. This item records the
session for these two endings in the same way, so the worker that wrote the change gets the
evidence when its session and lane are still there, and a fresh worker only when they are not.

- **Outcomes:**
  - Return a reverted landing to the session of the run that landed it.
  - Return a squash conflict to the session of the run whose candidate conflicted.
  - Say in the decision's recorded reason why a fresh worker was used instead.



**Agent Details:**

- **Ref:** [the landing-call design, Section 10](../design/tillr-landing-call.md#10-build-order), step 1 and its first drafted block; [the drain's guide](../../tools/tillr-drain/README.md#a-refused-run-goes-back-to-its-worker).
- **Where:** `recordRefusal` in `tools/tillr-drain/drain/events.go` skips `captureWorkerSession` for a conflict (`step != RefusalConflict`); the revert's payload in `tools/tillr-drain/drain/heavy_revert.go` carries no `WorkerSession`. `workerSessionReason` and `captureWorkerSession` are in `tools/tillr-drain/drain/worker_session.go`; the reasons `lane-unavailable` and `resume-failed` are set in `plan.go` and `designer.go`. A reverted landing's run is the one that landed, so its lane and session may already belong to another item; that is what c3 covers.
- **Build order, each step a stop only if it fails.** Make one commit, at step 3.
  1. Capture the worker session in the revert's payload and in the conflict's refusal, with the failing check's output or the conflicting paths as the evidence the returned prompt carries.
  2. Route both through the existing returned-run path so `workerSessionReason` decides resume against fresh. Add the tests of c1 to c4 beside the existing returned-run tests.
  3. Finish: update the README (c5), run `just pack-sync`, `python3 tools/go-test-shards.py tools/tillr-drain -k 6`, `yolo pack lint yolo-packs/tillr` and `python3 tests/tillr-docs.test.py`, then commit once.
- **What does stop this build:** a failing test that needs a file outside the item's paths, or a step whose named function is not in the tree. Report it with the step number, the command and its output.
- **If a decision is missing, report blocked** with one specific question.
- **Verify:** `go test ./...` in `tools/tillr-drain/`, `python3 tests/tillr-docs.test.py`, `just pack-sync`, and `uvx vantage-check docs/tillr/roadmap.md`.

### Add a landing call record and the answer a call may have

A worker's offer and what came of it are spread today across a refusal event, a landing row and a
revert event, and nothing says which of them the worker should hear about. This item adds one
record per call, the two small files a harness and the drain pass each other, and the pure
function that turns the landing steps' endings into the one answer a call may have. Nothing uses
it for delivery yet: it is proven against a stand-in harness, so the drain keeps working exactly
as it does today.

- **Outcomes:**
  - Record each offer as a call, with its answer, delivery and closing.
  - Choose a call's one answer by a pure function that a table test covers.
  - Declare each adapter's ending capability, `footer` for every adapter as shipped.



**Agent Details:**

- **Ref:** [the landing-call design](../design/tillr-landing-call.md): [Section 3.3](../design/tillr-landing-call.md#33-what-can-come-back) (the closed set of answers), [Section 5](../design/tillr-landing-call.md#5-the-record) (the record and its states), [Section 6.2](../design/tillr-landing-call.md#62-the-sequence) (the offer and answer files), [Section 7](../design/tillr-landing-call.md#7-the-other-harnesses) (capabilities).
- **Where:** `tools/tillr-drain/drain/` for the record, the chooser and the loop-start event; `tools/tillr-run/run/pause.go` for how `pause-kind` is read from an adapter directory, which `ending-capability` follows; `tools/tillr-run/backends/*/`. The `WorkerSession` record in `worker_session.go` stays: the design folds it into the call record only in a later step.
- **Where the offer file lives:** in the run's own durable directory, which is `run.DurableDir(laneDir, runID)` in `tools/tillr-run/run/exec.go`: `<lane dir>/durable/<run-id>/`, the scratch directory `run exec` creates for the worker and exports in its environment (the comment on `DurableDir` names the variable). It is written whole and renamed into place, and the drain reads it only after the run's process and its children are gone. The drain resolves the path the way `comments.go` does for `replies.jsonl` and `workflow_exit_record.go` does for `graph-exit.json`, and the stand-in harness writes to the directory that exported variable names.
- **Where the answer file lives:** in the same kind of directory of the run that opens the session again, so the drain writes it to `run.DurableDir` of that new run before starting it, whole and renamed into place.
- **Not the record directory:** `<root>/.tillr/loop/<run-id>/` (`RunDir` in `looprecord.go`) is where staging, the adapters and the gate keep a run's records. Neither file goes there, and this item adds nothing to it.
- **File names:** the implementer's. Declare each as one constant in `tools/tillr-drain/drain/`, beside the code that reads or writes it, and state both names in the README section of c6.
- **Build order, each step a stop only if it fails.** Make one commit, at step 3.
  1. The chooser and its table test (c2), with no store.
  2. The record, the store's refusal of a second answer, the offer-file intake and the capability files (c1, c3, c4).
  3. The stand-in harness test (c5), the README (c6), `just pack-sync`, the sharded suites for `tools/tillr-drain` and `tools/tillr-run`, `yolo pack lint yolo-packs/tillr`, `python3 tests/tillr-docs.test.py`; commit once.
- **What does stop this build:** a failing test that needs a file outside the item's paths, a step whose named function is not in the tree, or a contradiction between a criterion and the design section it cites. Report it with the step number, the command and its output.
- **If a decision is missing, report blocked** with one specific question. The wording of an answer's text is the implementer's, as the design says.
- **Verify:** `go test ./...` in `tools/tillr-drain/` and `tools/tillr-run/`, `python3 tests/tillr-muse-backend.test.py`, `yolo pack lint yolo-packs/tillr`, `just pack-sync`, and `uvx vantage-check docs/tillr/roadmap.md`.

### Add park, open-without-prompt and a features flag to our pi fork

pi can already run a tool call again when a session is opened and the call has no saved result.
That was checked on 2026-11-18 against the fork's `patch/pi` build: the same call id ran in a new
process and the saved result came before the new prompt. What pi cannot do yet is let a tool ask to
be left unanswered, open a session with no new prompt, or say what a build supports. Without those
three, a worker cannot end its run on a call and be answered later. This item adds them to the
fork, in the worker's own checkout. The delivery to the host and the adapter's use of them are
other items.

- **Outcomes:**
  - Let a tool leave its call unanswered, so the process saves the session and exits.
  - Open a saved session with no new prompt, answering the unanswered call first.
  - Print the build's feature names so the adapter can ask what it has.

<!-- tillr: item id="item-landing-call-pi-fork-park" paths=".forks/lanes/lane-1/pi/" blocked_by="" prose="2" -->

- **c1:** A tool whose `execute()` returns a park leaves its call with no saved result; the process makes no further model request, does not consult a settle hook, emits a top-level parked event naming the call id, flushes the session and exits 0. A real-process test asserts the transcript, the event and the exit code.
- **c2:** Opening that session with no prompt runs the tool again with the same call id and arguments, saves its result once, and continues to a model request. Opening it a second time repeats nothing. A test asserts both.
- **c3:** Opening a session that has no unanswered call and no trailing tool result, with no prompt, exits non-zero, says so in the stream and makes no model request. A test asserts it.
- **c4:** The features flag prints JSON that includes `parked-tool-calls`. A test asserts it.
- **c5:** When an extension adds a message as the session opens, the replayed result still directly follows its call in the request the model receives. A test with such an extension asserts the order of the messages sent.
- **c6:** The change is linear commits on `main` in the fork checkout this run is staged in. The worker runs neither `bin/pi-forks export` nor `bin/pi-forks check` and moves no ref of this repository. The touched packages' suites pass in the checkout, and the fork's `packages/coding-agent/docs/cli-integration.md` states the park result, the no-prompt open and the flag in the words a caller needs.

**Agent Details:**

- **Ref:** [the landing-call design](../design/tillr-landing-call.md): [Section 6.1](../design/tillr-landing-call.md#61-the-form-chosen-and-the-two-rejected) to [Section 6.5](../design/tillr-landing-call.md#65-the-fork-patches), and [Filing fork work](../../tools/tillr-drain/README.md#filing-fork-work). The same shape as [the safe-stop item](../design/tillr-restart.md).
- **Where:** `.forks/lanes/lane-1/pi/` is the fence spelling that staging reads. Staging rewrites it to the dispatched lane's checkout and starts that checkout at the delivered patch tip. Commit linearly on the fork's `main`. The replay already exists in the fork: `getPendingToolCalls` and `runAgentPendingToolCalls` in `packages/agent/src/agent-loop.ts`, `Agent.completePendingToolCalls` in `packages/agent/src/agent.ts`, from the fork commit titled "stop print workers safely on SIGUSR1". The park belongs where a tool result can already end a batch without a follow-up request; the no-prompt open is in `packages/coding-agent/src/modes/print-mode.ts` and its argument parsing; the flag is in the command line.
- **What the 2026-11-18 experiment saw, and c5 is for.** With the default extension set loaded, an extension appended a message to the session as it opened, between the unanswered call and the replayed result, and the Bedrock request failed with "The number of toolResult blocks ... exceeds the number of toolUse blocks of previous turn". A worker loads no discovery extensions, so it is not expected there; the order is still a precondition.
- **A resume still needs a prompt today:** print mode took a prompt and added it after the replayed result; that is what c2 and c3 replace.
- **Verify:** in the fork checkout, the touched packages' test suites (`packages/agent`, `packages/coding-agent`), and `uvx vantage-check docs/tillr/roadmap.md` for this document.

### Add the land tool and its brief ending to pi workers

With the call record and the fork's support in place, a pi worker can end its run with the `land`
tool instead of a report footer, and a refusal can reach it as that call's result instead of a new
message. The work is five items, split by the design seat on 2026-11-18 after three builds stopped
on it. This block is the first: it lands the tool, the brief ending and the watcher changes that a
saved patch already holds, with the tests the gate did not run. Nothing in it turns the new form
on. The loop records `footer` for the pi adapter whatever the installed pi prints, so every worker
keeps the footer exactly as today. The four blocks below carry the session file's path on the call,
have the adapter check the transcript, test delivery through a stand-in harness, and then switch
the form on.

- **Outcomes:**
  - Give a pi worker a `land` tool that records its offer and ends the run.
  - Stage the "Finishing" brief for a session pinned to the call form, and the footer brief for every other.
  - Have the gate run the adapter's landing-call tests.



**Agent Details:**

- **This item is one saved patch and six small edits.** The edits are listed below with the text to change. It does not write the transcript check, the message fallback, the stand-in delivery test or the switch; those are the four items below. A report that lists them as missing describes other items.
- **Ref:** [the landing-call design](../design/tillr-landing-call.md): [Section 3](../design/tillr-landing-call.md#3-the-contract-from-the-workers-seat), [Section 6.2](../design/tillr-landing-call.md#62-the-sequence) to [Section 6.4](../design/tillr-landing-call.md#64-the-keep-going-extension-and-the-settled-session-rule), and [Section 6.6](../design/tillr-landing-call.md#66-delivery-and-how-the-drain-knows). These references serve all five items, and this item needs none of them to be read.
- **Why the work is split, settled by the design seat on 2026-11-18.** Three builds stopped on this block when it held seven criteria and an eight-step plan. Each left the same saved patch behind: 22 files, 1,076 added lines. The last ran on the last backend the build seat escalates to. It applied the patch, ran the adapter's own tests, saw the one failure the plan had told it to expect, and reported that the task exceeds it. No build had run the two Go suites against the patch, and the design seat then did. Four faults showed, and none was in the plan:
  - The patch's watcher reads a `pi_session_id` column that the journal stand-in of `tools/tillr-run/pi_settled_exit_test.go` does not have. `TestPiSettledOwedWorkReadsTheBackgroundTaskJournal` failed and `TestPiSettledHarnessThatNeverExitsIsEndedByTheAdapter` never ended. The installed journal has the column (`journal.ts` in the `pi-background-tasks` package), so the stand-in is what is wrong.
  - The patch starts `pi` with the features flag at every loop start, whatever the policy names. In the drain's root package the `pi` on the path is often a stand-in harness, which took the flag for a session: two restart tests never ended, eleven more failed, and a stand-in committed a file named `candidate.txt` to the branch the suite was run from, twice.
  - `TestConflictReturnsToWorkerThreeTimesOnItsBranch`, which landed after the patch was written, records a call under `footer`; the patch refused to stage that session's next turn.
  - Nothing ran the adapter's four test programs, which the old plan already said.
- **What the design seat verified.** On 2026-11-18 at `ce91d5801` it applied the patch, made the six edits below and ran everything under **Verify**. `python3 tools/go-test-shards.py tools/tillr-run -k 6` passed 230 tests. The same command for `tools/tillr-drain` passed every test but one, `TestRestartGatesAndLandsTheRetiredHead`, which fails the same way at `ce91d5801` with no change applied and is not this item's. `just pack-sync`, `python3 tests/tillr-muse-backend.test.py`, `yolo pack lint yolo-packs/tillr` and `python3 tests/tillr-docs.test.py` passed. The change was then discarded, because the design seat's commit is this document. On 2026-11-19 at `bc6d787e0` two of those facts were read again: the patch still applies (`git apply --check -3` exit 0), and `go test . -run '^TestRestartGatesAndLandsTheRetiredHead$'` in `tools/tillr-drain` now passes with no change applied, so the trunk has repaired that test. The two full suites were not rerun at that commit.
- **The saved work.** `refs/tillr/preserved/run-item-landing-call-land-tool-a8` in the main checkout holds the patch as uncommitted work. The refs of the same name ending `a1` and `a4` hold the same patch (`git patch-id --stable` gave one id for all three on 2026-11-18).
- **Build order, each step a stop only if it fails.** Do the steps in order in one run. The end of a step is not an ending, and the commit is made once, at step 8.
  1. **Apply the saved patch.** From the lane worktree run `git -C /workspace diff 'refs/tillr/preserved/run-item-landing-call-land-tool-a8^1' refs/tillr/preserved/run-item-landing-call-land-tool-a8 -- tools | git apply -3 -`. It applied cleanly at `ce91d5801`, and `git apply --check -3` passed at `bc6d787e0`.
  2. **Take the probe out of the loop's start.** In `tools/tillr-drain/drain/landing_call.go`, function `recordEndingCapabilities`, remove three things: the line `piCapability, piPath := probePiEndingCapability(ctx)`; the three-line `if filepath.Base(filepath.Dir(path)) == "pi"` block that assigns `piCapability`; and, in the write transaction, everything after the append of `endingCapabilitiesKind`, so that the transaction body is again `_, err := tx.Append(...)` followed by `return err`. The function is then the one `git show HEAD:tools/tillr-drain/drain/landing_call.go` holds. Leave `probePiEndingCapability` and `landing_capability_test.go` as the patch has them. In `landing_call_test.go`, test `TestLandingCallLoopRecordsShippedCapabilities`, change the stand-in `pi` from `exit 2` to `printf '["parked-tool-calls"]\n'`; the test still wants `footer` for every adapter, which is the second half of c1.
  3. **Answer a footer call by the prompt.** In the same file, function `stageLandingAnswer`, the last `if` refuses a session turn unless the call's item, session, worktree and capability all match. Put a check before it: when `call.Capability != run.EndingCallSuspends`, return nil. Remove the capability term from the refusal that follows. `go test ./drain -run 'TestConflictReturnsToWorkerThreeTimesOnItsBranch|Landing|Ending|WorkerSession'` in `tools/tillr-drain` then passes.
  4. **Give the journal stand-in the column.** In `tools/tillr-run/pi_settled_exit_test.go`: in `fakeJournalSchema`, add `, pi_session_id TEXT` after `disabled_at INTEGER` in the `instances` table; in `fakeSettledPi`, change `VALUES ('inst', ?, ?, NULL)` to `VALUES ('inst', ?, ?, NULL, 'session')`; in `TestPiSettledOwedWorkReadsTheBackgroundTaskJournal`, change `VALUES ('i', ?, ?, ?)` to `VALUES ('i', ?, ?, ?, 'session')`.
  5. **Remove the one test that belongs to a later item.** In `tools/tillr-run/backends/pi/testdata/land.test.mjs`, delete the whole test named `a session with nothing to replay receives the answer as a message`. It fails because `run-pi` has no second start, and [the transcript check item](#add-a-transcript-check-before-a-pi-session-opens-with-no-prompt) writes it again with the code it needs.
  6. **Make the gate run the adapter's tests.** Add `tools/tillr-run/pi_landing_call_test.go`, package `main_test`, beside `pi_keep_going_test.go`. One test, `TestPiLandingCallAdapterTests`, with a subtest for each of three commands run in `backends/pi/testdata`: `node --test land.test.mjs keep-going.test.mjs`, `python3 extract-report-park.test.py` and `python3 parked-exit.test.py`. A subtest fails with the command's output on a non-zero exit, and fails with `t.Fatalf` when `exec.LookPath` does not find the program, as `pi_keep_going_test.go` does for `node`. Then `go test . -run 'TestPiSettled|TestPiLandingCallAdapterTests'` in `tools/tillr-run` passes.
  7. **Add the new files to the table** in `tools/tillr-run/backends/pi/README.md`: `land.ts`, and the four test programs of step 6.
  8. **Finish.** Run `just pack-sync`, then the commands under **Verify**, with the two Go suites through `python3 tools/go-test-shards.py <module> -k 6`. Make one commit holding the patch, the edits, the new test file and the mirror.
- **What does stop this build:** a test that still fails after its step is done; a step whose named function or text is not in the tree; or the patch failing to apply. Report it with the step number, the command and its output. Before naming a failing test that no step touches, run it on the base with the change stashed: a test that is red there is a `trunk` fact and not a `seat` one. `TestRestartGatesAndLandsTheRetiredHead` was such a test on 2026-11-18 and passed on the base on 2026-11-19. A report that lists steps not yet done, or work that belongs to a later item, is not a stop.
- **Run the suites from a clean branch and look at `git log` afterwards.** The stray `candidate.txt` commits came from the probe in the loop's start, which step 2 removes. If `git log` shows a commit titled `candidate` on the lane's branch, step 2 is not done.
- **Verify:** `go test ./...` in `tools/tillr-drain/` and `tools/tillr-run/`, `python3 tests/tillr-muse-backend.test.py`, `just pack-sync`, `yolo pack lint yolo-packs/tillr`, `python3 tests/tillr-docs.test.py`, and `uvx vantage-check docs/tillr/roadmap.md`.

### Add the session file path to the landing call

After the item before this one, the `land` tool writes an offer and the drain stages an answer file
for the session's next opening, but only the tool's own `session_start` hook knows where the
session's transcript is, and it writes that path after pi has started. The adapter needs the path
before it starts pi, to choose between opening with no prompt and opening with a message. This
item puts the path on the offer, has the drain keep it and write it beside the answer, and has the
drain record a delivery made as a message. The form stays off.

- **Outcomes:**
  - Record the session file's path with the offer.
  - Write that path beside the answer before pi starts.
  - Record a delivery the adapter made as a message.



**Agent Details:**

- **Ref:** [the first item of this split](#add-the-land-tool-and-its-brief-ending-to-pi-workers), which this item waits on, and [the design's Section 6.2](../design/tillr-landing-call.md#62-the-sequence). This item has not been built; the plan below is from reading the saved patch.
- **The tool.** In `tools/tillr-run/backends/pi/land.ts`, `executeLand` takes one more argument after `answered`, `transcript = ""`, and writes it as the `transcript` field of the offer. The registered tool's `execute()` passes `ctx.sessionManager.getSessionFile() ?? ""`. Delete the `session_start` hook that writes `landing-transcript`. In `testdata/land.test.mjs`, the expected offer gains `transcript: ''`, and one more assertion passes a path and reads it back.
- **The drain.** In `tools/tillr-drain/drain/landing_call.go`: `landingOffer` gains `Transcript string` with the tag `json:"transcript,omitempty"`, and `landingCall` embeds it, so the record keeps it with no other change. `stageLandingAnswer` writes `call.Transcript` and a newline to `landing-transcript` in `dir` when the path is not empty, before it writes the answer file. `reconcileLandingDelivery` reads `landing-delivery` in `dir` after its identity check. For `message` it checks that the run is no longer live, as the function already does lower down, and appends `call.delivered` with form `message` under the same `judgmentSeen` guard as the `tool-result` append. For anything else it continues as now.
- **Tests.** `TestReturnedLandingAnswerStaging` in `landing_call_test.go` already stages an answer; extend it to assert the `landing-transcript` file for a call recorded with a path and its absence for one without. Add `TestLandingDeliveryRecordsAMessage`, which stages the same turn, writes `message` to `landing-delivery`, calls `reconcileLandingDelivery` and asserts one `call.delivered` event whose form is `message`, and a second call adds none.
- **Verify:** as [the first item](#add-the-land-tool-and-its-brief-ending-to-pi-workers) states it.

### Add a transcript check before a pi session opens with no prompt

[The design's Section 6.3](../design/tillr-landing-call.md#63-failure-cases) left one case to pi: a
transcript that no longer holds the unanswered call. The fork does not behave as that row says for
every such transcript, so the adapter makes the check itself. This item adds the check as a program
beside `run-pi`, has `run-pi` choose between the two forms before it starts pi, and keeps pi's own
refusal as a second line. The form stays off.

- **Outcomes:**
  - Open a session with no prompt only when its transcript holds the landing call.
  - Deliver the answer as a message in every other case, with no model request made without it.
  - Say which form was used, in a file the drain reads.



**Agent Details:**

- **Ref:** [the first item of this split](#add-the-land-tool-and-its-brief-ending-to-pi-workers), and [the item before this one](#add-the-session-file-path-to-the-landing-call), which this item waits on: it leaves the path in `landing-transcript` beside the answer file, and the drain reading `landing-delivery`. This item has not been built; the plan below is from reading the saved patch and the fork.
- **The transcript check** (ruled 2026-11-18, in answer to a worker's report). Section 6.3 as first written says: "The harness reports nothing to replay and exits without a model request". `resumeWithoutPrompt` in `packages/coding-agent/src/core/agent-session.ts` on `patch/pi` refuses only when there is neither an unanswered call nor a trailing tool result, and it never compares a call id, so a transcript ending in any other call's result goes on to a model request that carries no answer. The fork stays as it is, and its item's criteria stand; the adapter makes the check, because it alone knows which call the answer is for. The transcript holds the landing call when all three are true of the session file: the last assistant message is a tool-use message that contains the call id; no compaction entry follows that message; and the call either has no saved result, or has one and the file's last message is a tool result. The second arm is the Section 6.3 case of a process that died after the result was saved, where pi continues from the saved result. A file the adapter cannot read or parse fails the check. Failing the check is always safe, because the message form delivers the same answer.
- **The program.** One Python file, `tools/tillr-run/backends/pi/landing-call-held`, executable. It walks the file's entries from the last one back through `parentId`, as `landingResultPersisted` in `tools/tillr-drain/drain/landing_call.go` does, and applies the three conditions. Take the entry shapes from the fixtures in `TestLandingResultPersisted`; a tool call in an assistant message's content has `type` `toolCall` and an `id`, and a compaction entry has `type` `compaction`. Its test is `testdata/landing-call-held.test.py`, which writes the six files of c1.
- **`run-pi`.** Where it sets `prompt=/dev/null` for an answer file, it first reads the call id and the text from the answer file and the path from the file `landing-transcript` beside the answer file, and runs the check. A missing path file fails the check. An answer file with no text is refused with `refuse`. On 0 it writes `tool-result` to the file `landing-delivery` in that directory, the turn's scratch, and keeps `prompt=/dev/null`. On 1 it writes `message`, writes the text to a file in the same directory and makes that file the prompt.
- **The second line.** Every other start keeps the last line of `run-pi`, the `exec`, byte for byte. Only the promptless start changes, because `run-pi` must outlive it. In `--mode json` pi reports the refusal on stdout, as the line `{"type":"error","message":"No unanswered tool call or trailing tool result to continue"}`, and exits 1 (`runPrintMode` in `packages/coding-agent/src/modes/print-mode.ts` on `patch/pi`). So the promptless start runs as `{ "$@" </dev/null 2>"$err"; echo $? >"$status"; } | tee "$copy"`, with the `err`, `status` and `copy` files all in the turn's scratch. When the status is not 0 and the copy holds that message, `run-pi` rewrites `landing-delivery` to `message` and ends with the ordinary `exec` and the text file as the prompt. Otherwise it exits with the status.
- **Tests.** In `testdata/land.test.mjs`: the existing test `run-pi keeps footer argv and opens an answer session without stdin` stages `{}` as its answer, which this item refuses, so give it a session file that holds the call, a `landing-transcript` file and an answer with a call id and text. Write again the test the first item removed, `a session with nothing to replay receives the answer as a message`, with its stand-in printing the JSON error line on stdout. Add one test for c2 whose stand-in `pi` appends its arguments and whether its stdin was empty to a log. Add `landing-call-held.test.py` to the commands in `tools/tillr-run/pi_landing_call_test.go`, and the two new files to the table in `tools/tillr-run/backends/pi/README.md`.
- **Verify:** `go test ./...` in `tools/tillr-run/`, `python3 tests/tillr-muse-backend.test.py`, `just pack-sync`, `yolo pack lint yolo-packs/tillr`, `python3 tests/tillr-docs.test.py`, `uvx vantage-check docs/tillr/roadmap.md` and `uvx vantage-check docs/design/tillr-landing-call.md`.

### Test landing-call delivery through a stand-in harness

The saved patch removed the one test that drove an answer through a settle and a reopened session,
`TestLandingCallStandInDelivery`, because the function it called was replaced. Nothing now asserts
that a refusal reaches the session as the call's result, or that each failure case of the design
ends as the design says. This item writes those tests. It changes production code only where a test
shows a fault.

- **Outcomes:**
  - Assert that a gate, a judge and a fence refusal are each delivered as the call's result.
  - Assert the stated outcome of every failure case in the design.

<!-- tillr: item id="item-landing-call-delivery-tests" paths="tools/tillr-drain/,tools/tillr-run/,docs/" blocked_by="" prose="2" -->

- **c1:** A gate refusal, a judge refusal and a fence refusal each reach the session as the result of the open call, with the evidence of [Section 3.3](../design/tillr-landing-call.md#33-what-can-come-back), and the session receives no user message. A test with a stand-in harness asserts for each that the delivery form recorded is `tool-result` and that the reopened run's prompt was empty.
- **c2:** Each failure case of [Section 6.3](../design/tillr-landing-call.md#63-failure-cases) has a test that asserts its stated outcome, and each test's name says which row it is.

**Agent Details:**

- **Ref:** [the first item of this split](#add-the-land-tool-and-its-brief-ending-to-pi-workers), and [the transcript check item](#add-a-transcript-check-before-a-pi-session-opens-with-no-prompt), which this item waits on. This item has not been built.
- **The delivery test.** In `tools/tillr-drain/drain/landing_call_test.go`, beside `TestReturnedLandingAnswerStaging`, which already settles a first run that offers, refuses it at the gate and plans the session's next turn. The new test goes one step further for each of the three refusals: its stand-in harness for the second turn records whether its stdin was empty, writes `tool-result` to `landing-delivery` and appends to the session file a `land` tool result that carries the staged answer, in the shape `TestLandingResultPersisted` accepts. The test then settles that run and asserts one `call.delivered` event with form `tool-result`. The removed test is in `git show HEAD:tools/tillr-drain/drain/landing_call_test.go` for the fixture calls it used.
- **The table.** Eleven rows. Rows that an existing test already asserts need only a test of the row's name that calls the same helper or a comment naming the row above the existing test: the replay with an answer file and the park with only an offer file (`land.test.mjs`), the exit bound (`parked-exit.test.py`), two answers for one call (the store's refusal, in `landing_call_test.go`) and the missing call (the transcript check item's tests). Write the others in the module that owns the behaviour: the tool's in `land.test.mjs`, the drain's in `landing_call_test.go`.
- **What does stop this build:** a row whose stated outcome the code does not produce and whose fix lies outside `tools/tillr-drain/` and `tools/tillr-run/`. Name the row and paste the test's output.
- **Verify:** as [the first item](#add-the-land-tool-and-its-brief-ending-to-pi-workers) states it.

### Move pi workers to the land tool when pi supports it

Everything the call form needs is in the tree after the four items above, and the loop still
records `footer` for the pi adapter. This item makes the loop ask the installed pi what it supports
and record `call-suspends` when it prints the feature. Until the host has the fork's delivery, the
installed pi does not print it, and every worker keeps the footer.

- **Outcomes:**
  - Choose the form by what the installed pi says it supports.
  - Record the answer and the binary's path at each start of the loop.

<!-- tillr: item id="item-landing-call-pi-switch-on" paths="tools/tillr-drain/,docs/" blocked_by="item-landing-call-delivery-tests" prose="2" -->

- **c1:** The pi adapter's effective ending capability is `call-suspends` only when the installed binary prints `parked-tool-calls` from the features flag, and `footer` otherwise. A test of the loop asserts both with stand-in binaries, including one that rejects the flag.
- **c2:** The loop starts `pi` with the features flag only when its policy names a backend whose adapter is the pi adapter, and records the answer with the binary's path as a `drain.pi-features` event. A loop whose policy names no such backend starts no `pi`. A test asserts each.
- **c3:** A probe whose process outlives its bound, or leaves a child holding its output, does not hold the loop's start. A test asserts it with a stand-in that sleeps.

**Agent Details:**

- **Ref:** [the first item of this split](#add-the-land-tool-and-its-brief-ending-to-pi-workers), and [the design's Section 6.6](../design/tillr-landing-call.md#66-delivery-and-how-the-drain-knows). This item waits on [the delivery tests](#test-landing-call-delivery-through-a-stand-in-harness). It has not been built.
- **What the first attempt showed.** The saved patch called `probePiEndingCapability` first thing in `recordEndingCapabilities`, for every loop. Measured 2026-11-18 at `ce91d5801` with `python3 tools/go-test-shards.py tools/tillr-drain -k 6`: `TestRestartReplacesTheLoopInPlaceWithoutSignalingARun` and `TestARestartThroughALauncherReachesTheLaunchersBuild` never ended, and eleven tests failed, among them `TestNonLandingLoopStartsWithoutGit`, the `TestHookCompletion` tests and the two wall tests in `wall_test.go`. Three causes: the stand-in `pi` programs of the root package take any start for a session, so they counted it, slept in it, or committed in the directory the test ran from; `exec.Cmd.Output` waits for a killed shell's child that still holds the pipe; and a loop with no pi backend started the real `pi`, which ran git where a test forbids it.
- **The change.** In `recordEndingCapabilities`, probe once, and only when the loop reaches a backend whose adapter directory is named `pi`; assign the answer to that adapter; append `drain.pi-features` only when a probe ran. The first item's step 2 names the three pieces to put back. In `probePiEndingCapability`, build the command as a variable and set `WaitDelay` to one second before `Output()`.
- **The stand-ins.** `rg -n 'stub\(t, "pi"' tools/tillr-drain` found four on 2026-11-18, in `restart_live_test.go`, `wall_test.go`, `resume_test.go` and `quota_window_test.go`. Each must answer the flag before it does anything else: put the line `[ "${1:-}" != --features ] || exit 2` first in each body. A test whose policy names the pi adapter and has no stand-in on the path gets one.
- **Run the suite from a clean branch and look at `git log` afterwards.** A commit titled `candidate` on the lane's branch means a stand-in ran as a probe and committed. Find the stand-in, fix it, and reset the branch to the commit before it; that commit must not be offered.
- **The host steps are not a condition for landing.** The fork delivered, built and applied on the host, and the jail relaunched, are what make the installed pi print the feature. The owner watches for that by the recorded `drain.pi-features` event.
- **Verify:** `go test ./...` in `tools/tillr-drain/`, `just pack-sync`, `yolo pack lint yolo-packs/tillr`, `python3 tests/tillr-docs.test.py`, and `uvx vantage-check docs/tillr/roadmap.md`.


### Add an obstacle tool to the pi worker

A worker that cannot finish writes its obstacle today as lines in a report, and a missing line is
found only after the run is over. This item makes the obstacle a tool call with typed arguments, so
the harness refuses a call that lacks a required field, and the reply (a design answer, the
owner's answer, or "the base replay did not reproduce your claim") returns to the same session as
that call's result.

- **Outcomes:**
  - Refuse an obstacle call that lacks a field its kind requires, without ending the run.
  - Route a tool-call obstacle exactly as the same obstacle in a footer.
  - Return the reply to the worker as the call's result.

<!-- tillr: item id="item-landing-call-obstacle-tool" paths="tools/tillr-run/backends/,tools/tillr-drain/,docs/" blocked_by="item-landing-call-pi-switch-on" prose="2" -->

- **c1:** `obstacle` accepts each of the six kinds (`definition`, `fence`, `trunk`, `tooling`, `seat`, `owner`) with that kind's required fields and rejects a call missing one, without ending the run. A test asserts each kind and one rejection.
- **c2:** An obstacle call is recorded and routed exactly as the same obstacle in a footer is today. A test runs one shared case file of obstacles through both forms and asserts equal routing.
- **c3:** The reply to an obstacle reaches the session as the call's result. A test asserts it for a design answer and for a `trunk` claim the base replay did not reproduce.

**Agent Details:**

- **Ref:** [the landing-call design, Section 3.5](../design/tillr-landing-call.md#35-a-named-obstacle-is-a-second-tool). The footer's obstacle table, which stays the form under `footer`, is in `tools/tillr-run/backends/brief-template.md`.
- **Where:** beside the `land` tool this item waits on; routing in `tools/tillr-drain/drain/`.
- **Verify:** `go test ./...` in `tools/tillr-drain/` and `tools/tillr-run/`, `python3 tests/tillr-muse-backend.test.py`, `just pack-sync`, `yolo pack lint yolo-packs/tillr`, `python3 tests/tillr-docs.test.py`, and `uvx vantage-check docs/tillr/roadmap.md`.
