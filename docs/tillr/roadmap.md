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

Five questions are left for the owner on this page: one from this round's designs, and four
that the pi roadmap's reconciliation of 2026-11-16 found were really tillr's. Each has options and a recorded leaning.
They are the ones no recorded principle could answer. Twenty-three others that stood here were
settled on 2026-11-15 and 2026-11-16 by the session's designer, at the owner's request; each
says so in its design and is the owner's to overturn.

1. [`OQ-SH2`](../design/tillr-selfhosted-build.md#OQ-SH2): what becomes of the pack mirror that
   doubles every tillr change. First because it is a cost on every landing but stops nothing.

2. [Criterion repair](../design/criterion-repair.md), one question,
   [`OQ-CR4`](../design/criterion-repair.md#OQ-CR4): whether the judge step looks for a criterion
   whose premise has expired. With the judge, which it amends. The owner ruled the other four on
   2026-11-17: the whale supersedes with a required reason, the judge sees both texts, an
   unsatisfiable criterion is repaired by reopen then supersede, and a supersession refuses a
   verdict in flight. None of it is built.
3. [What a lane's agent is told](../design/agent-context-delivery.md), three questions about the
   context a worker receives. Last because the brief works today and these refine it.

[What tillr will change for Product QA](../design/tillr-separable-parts.md) carries
eight more, [`OQ-SP1`](../design/tillr-separable-parts.md#OQ-SP1) to
[`OQ-SP8`](../design/tillr-separable-parts.md#OQ-SP8), from 2026-11-16: whether tillr answers the
Product QA pipeline's requests with that proposal, and which of its small changes are filed. The
pipeline's second-round reply is recorded as received in
[Product QA's reply](../design/tillr-product-qa-requests.md), and on 2026-11-17 the owner said to build:
the answer is [the build plan](../design/tillr-definitions-plan.md), whose items hold no question.
None of the eight holds the build; [`OQ-SP3`](../design/tillr-separable-parts.md#OQ-SP3) still
decides the proposal's documentation items. (The first round called the pipeline "AutoQA", which is
[tillr's own exploratory-testing design](../design/tillr-autoqa.md), a different thing.)

[The core and the landing definition](../design/tillr-core-and-git-landing.md)
writes down the owner's direction of 2026-11-16 and carries five more,
[`OQ-CG1`](../design/tillr-core-and-git-landing.md#OQ-CG1) to
[`OQ-CG5`](../design/tillr-core-and-git-landing.md#OQ-CG5). Since the owner's go-ahead of
2026-11-17, [`OQ-CG1`](../design/tillr-core-and-git-landing.md#OQ-CG1),
[`OQ-CG2`](../design/tillr-core-and-git-landing.md#OQ-CG2),
[`OQ-CG3`](../design/tillr-core-and-git-landing.md#OQ-CG3) and
[`OQ-CG5`](../design/tillr-core-and-git-landing.md#OQ-CG5) each have a working
choice the build rests on, which he confirms or changes after the next round with the Product QA
pipeline's agent; [`OQ-CG4`](../design/tillr-core-and-git-landing.md#OQ-CG4) can wait.
That document also lists the owner's statements as the pipeline reported them, for him to confirm.

[Work reaches tillr through several doors](../design/tillr-intake-paths.md) writes down the owner's
idea of 2026-11-15 to 2026-11-17 that each way work enters (an agent files an item, the owner dumps
a ramble) runs its own small graph and ends in one item shape, and lists what is not yet settled.

[Planning and model tiering](../design/tillr-planning-node-model-tiering.md) is the proposal for the
owner's ask to spend less by giving each kind of work the cheapest model that can do it. A cheaper
model such as Terra runs only work that a command checks the moment it finishes, and climbs to Sol
when the check fails; a planning node first cuts a large build item into such checked slices. It
carries eight more questions,
[`OQ-PT1`](../design/tillr-planning-node-model-tiering.md#OQ-PT1) to
[`OQ-PT8`](../design/tillr-planning-node-model-tiering.md#OQ-PT8), from 2026-11-16, and the document
opens with [a box](../design/tillr-planning-node-model-tiering.md#what-i-am-asking-you-to-decide) that
puts each in one line with its recommendation and its cost if wrong. Answer the first two first.
[`OQ-PT1`](../design/tillr-planning-node-model-tiering.md#OQ-PT1) asks again the October ruling that
on Bedrock only Sol and Luna are worth setting up; a yes lets the two stage 2 items be filed, which put
test fixes on Terra behind a check.
[`OQ-PT2`](../design/tillr-planning-node-model-tiering.md#OQ-PT2) is the only part that can start in
hours; a yes is one policy edit that runs five named items on Terra and measures it. The other six
shape the planning items, and each item is written to its question's recommended answer, so on a yes
[the written-out items](../design/tillr-planning-node-model-tiering.md#15-ready-to-file-on-your-word)
are filed as they stand. Two faults in the run record that the proposal found wait on no question and
are [filed below](#filed-2026-11-17-the-run-records-spend-corrected-before-it-is-compared).

[The judge and lifecycle](../design/tillr-judge-and-lifecycle.md) left this list: every question
it carried is a row of its ledger, and on 2026-11-16 it was cut into
[thirteen build items](../design/tillr-judge-and-lifecycle.md#8-build-order) that live in the
design. The first stops a gate pass from retiring a criterion only the owner can settle and waits
on nothing. Part of the design was already built under other items, and
[its seventh section](../design/tillr-judge-and-lifecycle.md#7-what-the-tree-already-holds)
measures which part. AutoQA's one design-seat item stays held while that design is in the
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

- **Where:** the shared default in [`vantage.go`](../../tools/tillr-drain/vantage/vantage.go), its
  consumers in [`tillr-view`](../../tools/tillr-view/) and [`tillr-drain`](../../tools/tillr-drain/),
  and the [committed tillr delivery](../../yolo-packs/tillr/).
- **Verify:** put a recording package-runner stub on PATH with no standalone checker; default
  reads must invoke `uvx vantage-check index --format json`. Keep override, command-failure,
  malformed-index and open-question tests. Sync and lint the delivery.



## 3. Roll onto new code without ending runs

[Rolling tillr onto new code](../design/tillr-restart.md) is decided. It comes third because
every restart on 2026-11-15 was a kill, so a fix to tillr itself cost the runs in flight, and
because [entry 4](#4-choose-where-each-seat-runs) reuses its stop-and-continue step. Its first
slice (run output to a file) is built; the pause, the restart request, the roll after a landing,
the push after a landing and this repository's settings file are queued in its build order.

## 4. Choose where each seat runs

[The Claude backend](../design/tillr-claude-backend.md) is built and has had one hand run. A
mixed policy puts designers on the subscription and builders on pi. What is left is filed
[on this page](#filed-2026-11-15-choose-where-each-seat-runs-by-quota-and-move-work-when-a-backend-runs-out):
re-reading the policy without a restart, reading quota directly with a reserve, splitting a seat
by share or at random for bake-offs, and moving a run when its backend runs out. Fourth because
it saves money on every design run but stops nothing when it is missing.

## 5. The ten-second gate

[The gate rework](../design/gate-rework.md) is decided and partly built: check files, the policy
file and result memory have landed. Fifth because the owner's target is a gate measured in
seconds and landings that outpace ten lanes, and the remaining slices are a chain behind the
change-checks slice, which has failed its own gate twice and is being reworked.

## 6. The observatory answers the owner's questions

[The observatory redesign](../design/observatory-redesign.md) has its first screens built: the
waiting and passes faces, Home, Items, Passes, Performance, Trunk and trace export. Sixth, and
not higher, because [entry 2](#2-keep-the-loop-running) fixes what the screens would only report.
Queued behind it: [a stopped loop says what stopped it](#filed-2026-11-15-the-observatory-speaks-to-its-reader-not-to-its-builders),
and the lane strip, lane page, dependency graph and usage figures, which no longer wait on a
ruling.

## 7. The drain's own model

[Replace the drain tick with a converge function](../design/tillr-drain-successor.md) is decided
and four of its five steps are built: the transition value, attempt identities, the per-item pass
and the trigger wait. Seventh because what is left, retiring the tick's names, changes no
behavior. The design is still titled "successor function"; the owner's name for it is converge,
and the rename waits for that last step.

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
  yet." No AutoQA code exists. Its one open question,
  [`OQ-AQ2`](../design/tillr-autoqa.md#OQ-AQ2), is left unanswered and holds the design's one
  queue item, `item-design-autoqa-slices`, so the drain never dispatches it. Picking it up means
  ruling that question, which releases the item to cut the design into build items. This is not
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

On 2026-11-15 the gate's `structural` check failed on a fresh machine because seven local
`patch/*` branches did not exist, and the after-merge check could not judge because the shared
fork checkouts were absent. Both read a name on the machine (a branch, a directory) where the tree
should pin content. [The gate rework](../design/gate-rework.md) already makes a missing input a
named answer; this item removes the dependence.

### Fork checks resolve a commit the tree pins, through a cache that is filled on a miss

Criteria c2 and c3 were restored on 2026-11-17. Commit `bf87a76d8b`, which settled the landing
sequence around the owner's uncommitted edits, replaced them here with two criteria about the
lander and the drain's boundary check, which this item's paths do not include, and a build stopped
on them. Those two belong to
[routing a landing around the owner's uncommitted edits](#route-a-landing-around-the-owners-uncommitted-edits),
which carries its own c2 and c3. This item changes nothing under `tools/tillr-land/` or
`tools/tillr-drain/`.

<!-- tillr: item id="item-gate-pinned-fork-inputs" paths="tools/gate/,tools/fork-check.py,tests/fork-check.test.py,bin/pi-forks,tests/pi-forks.test.sh,yolo-packs/,docs/" blocked_by="" -->

- **c1:** Each fork a pack delivers from a patch branch records, in the tree, the commit that branch must be at, and exporting a series updates that record in the same change.
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

<!-- tillr: item id="item-gate-red-cap" paths="tools/tillr-drain/,tools/tillr-land/,tools/tillr-loop/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="item-gate-heavy-runner" -->

- **c1:** When the trunk is more landings, or more minutes, past the last green commit than `red_cap` allows, a landing is refused before the trunk moves, and the refusal names the red check and the open fixer item.
- **c2:** The refused item is not convicted: it keeps its candidate, is held with a reason of its own that names the cap, and lands with no new run once the green pointer moves. A test lands a fixer and asserts the held candidate lands on the next pass.
- **c3:** The fixer item itself is never refused by the cap.
- **c4:** A `red_cap` of zero disables the refusal, and the loop's status line still counts landings since green.
- **c5:** The `waiting` face shows items held by the cap as waiting on the fixer, by its item id.

### A new lane starts from the last green commit

<!-- tillr: item id="item-gate-lane-base" paths="tools/tillr-drain/,tools/tillr-run/,tools/tillr-loop/,yolo-packs/tillr/,docs/" blocked_by="item-gate-heavy-runner" -->

- **c1:** With `lane_base` set to `green`, a lane's worktree is cut from the green pointer, and its recorded base is that commit; with `tip` it is cut from the trunk tip as today.
- **c2:** An item whose blocker landed after the green pointer is not dispatched until the pointer contains the blocker's landing, and its waiting reason says it waits on the trunk being proved, not on the blocker.
- **c3:** The fixer item for a red trunk is always cut from the tip, whatever the setting, because the failure it fixes is not in the green commit.
- **c4:** On a store with no green pointer yet, lanes are cut from the tip and the loop says so once.

### A change to the gate's own runner lands only after the runner's suite passes

A change to the gate's own runner can break the gate for every lane, so the runner's suite proves such a change before it lands rather than after. Criterion c3 was aligned on 2026-11-17 with the owner's ruling on time limits ([`D-GR15`](../design/gate-rework.md#decision-ledger), [Section 6.5](../design/gate-rework.md#65-ten-seconds-is-a-failing-test)): a measured time is counted and reported and fails nothing.



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

<!-- tillr: item id="item-prose-rewrite-before-build" paths="tools/tillr-item/,tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,.tillr.toml,docs/" blocked_by="" -->

- **c1:** When sync records a `prose-unmarked` or `prose-shape` finding for an item that is not landed, it creates one rewrite item for it, `rewrite-<item id>`, on the cheap seat, fenced to the document that holds the block, ranked ahead of every item that is not itself a rewrite or a trunk fixer. A second sync creates no second rewrite.
- **c2:** The original item is held with the reason `awaits-rewrite` and its mover is the rewrite item, until the rewrite lands or the block passes the check on its own. The waiting face shows both.
- **c3:** The rewrite's brief carries the whole text of the prose guide and the findings, and its criteria are fixed: the title and the section's prose are rewritten to the guide; the directive's `id`, `paths`, `blocked_by` and `seat` are unchanged; no criterion's meaning changes, and a criterion whose wording changes keeps its number; the `prose` mark is added.
- **c4:** The loop refuses a rewrite candidate that changed an `id`, `paths`, `blocked_by` or `seat`, or that added or removed a criterion, before the gate, naming what changed.
- **c5:** Rewrite items are created only when the repository's settings file sets `prose.rewrite = true`. The default is false, as every default in that file is, and this repository's file sets it. A repository without it gets the findings and no rewrite items.

## Filed 2026-11-15: a design on the roadmap always has an item in the queue

Principle: the queue is the one place to see all the work, so anything the roadmap names has to
be there. On 2026-11-15 the owner asked why the item graphs design had no item. The sync creates
an item only from an item block, and a design that is still waiting on rulings has none, so four
designs were on the roadmap and invisible in the queue. Each now carries one design-seat item
that is held on its open questions. The check below keeps the next design from slipping through.

### Warn when a roadmap design has no item block

<!-- tillr: item id="item-sync-design-without-item" paths="tools/tillr-item/,tools/tillr-view/,yolo-packs/tillr/,docs/" -->

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

- **c1:** `tillr item sync` reports a `design-without-item` finding for a document under `docs/design/` that a roadmap links to, whose frontmatter stage is not built or done, and that holds no item block and is not named as the design document of any item that is not landed.
- **c2:** The finding names the document and the roadmap line that links it. It is a warning and changes no item. A test covers a design with a block, one without, and one whose only items are landed.
- **c3:** The waiting face lists such a document under "needs you" with the reason `design-without-item`, so it is visible until someone files its item.


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

<!-- tillr: item id="item-view-no-unclassified" paths="tools/tillr-view/,tools/tillr-drain/,yolo-packs/tillr/,docs/" -->

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

- **c1:** No row of either waiting face is `unclassified`, has an empty reason, or has a dash for its mover. On 2026-11-15 six items whose runs had been killed and four items no pass had looked at yet showed all three. A test builds a store holding every hold the drain can record and asserts that the history face maps each one to a class, a reason and a mover. The observatory no longer lists `unclassified` among its classes and lists `waits on the loop`.
- **c2:** On the history face, an item with no recorded decision reads `waits on the loop · not yet planned` with the next pass as its mover, and an item whose last run was ended by a loop stop reads `waits on the loop · run ended by a stop` with that run as its mover. The stop's label gives way to any decision recorded for the item at or after the stop's time, unless that decision is the one that dispatched the stopped run, and it gives way once the run is resumed. A test records a hold in the same millisecond as a stop and asserts the row shows the hold.
- **c3:** The default face is unchanged: `TestWhyAPIAndWaitingBeforeAnyPass` passes with its assertions unchanged, and a test asserts that an item whose last run was ended by a stop shows the planner's own class, reason and mover there.
- **c4:** A reason the history face has no rule for fails the view's test suite by name, so a new hold cannot ship without its line.

**Agent details:**

- **Where:** the history face is `assembleWaiting`, `waitingRuleFor` and `waitingDecision` in `tools/tillr-view/waiting.go`; the fallback row there is the one built with `Class: "unclassified"` and `Why: "no pass yet"`, and the fallback rule is the last line of `waitingRuleFor`. The default face is `plannerWhy` in `tools/tillr-view/why.go`, which runs `tillr-drain why`; its classes come from `whyPresentation` in `tools/tillr-drain/drain/why.go`, which this item does not change. The observatory's class lists are under `tools/tillr-view/web/src/components/`. The pack's copy under `yolo-packs/tillr/` is regenerated with `just pack-sync`.
- **The stop's evidence:** the drain records a `drain.orphaned` event whose object is the run, and a later `run.resume` event for the same run withdraws it. Read those two event kinds; do not infer a stop from a signal or an exit code. The decision that dispatched a run is the `dispatches` row whose `run_id` is that run.
- **Not this item:** changing what the planner answers, or adding the stop to the default face. The README section *Why each unlanded item is not moving* in `tools/tillr-view/README.md` already states the default face's contract; this item updates only its *Recorded-decision history* section.
- **Evidence:** at `77fe7c39`, `rg -n 'unclassified' tools/tillr-view/waiting.go tools/tillr-drain/drain/why.go` returns two lines, both in `waiting.go`: the fallback rule and the fallback row. `whyPresentation` has no such branch, and its default returns `needs a cause` with a named mover.

### Stop dispatching when the spend ceiling is reached

<!-- tillr: item id="item-drain-spend-ceiling" paths="tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" -->

**Ruling, 2026-11-17 (answers the first run's conflict):** the first run stopped because every
criterion here asked for a spend ceiling, and
[the momentum principle](../design/momentum-principle.md#what-this-forbids) forbids one: "A budget
that stops good work is a wall an operator has to debug at 3am." Two tests in `tools/tillr-drain`
hold that line. `TestTheSpendLineNamesTheMeasurement` fails a spend line that names a ceiling, and
`TestTheLoopCommandRefusesTheDeletedBudgets` refuses the deleted budget flags. The principle
stands and this item does not reopen it. The ceiling is withdrawn, and what remains is the
measurement an operator needs in order to decide for themselves whether to stop the loop or take
work out of the pile. Those two acts are the only throttles the principle recognizes.

The heading and the item id are kept so that links and the queue still find this item. They name
what was asked for, not what is built: this item builds no ceiling.

- **c1:** This item adds no ceiling. It adds no setting, flag or environment variable that names an amount of spend, no hold whose reason is spend, and no comparison of a spend total against a number. Dispatch does not depend on spend in any way. `TestTheSpendLineNamesTheMeasurement` and `TestTheLoopCommandRefusesTheDeletedBudgets` pass with their assertions unchanged, and a new test dispatches a ready fixture item in a store whose recorded spend is far above any plausible limit.
- **c2:** The loop's start line and the waiting face each show the recorded cost of runs started in the last 24 hours, labeled as a measurement with its window, such as `spend, last 24h: $41.20 over 37 runs`. The window is fixed at 24 hours and has no setting. A test builds a store with one run started inside the window and one started before it, and asserts that both places show only the first run's cost.
- **c3:** A run in the window that reported no cost is counted on that line as a number of runs without a cost, and never as zero dollars. A test asserts that a window holding one run with a cost and one without shows the first run's cost and `1 run reported no cost`.
- **c4:** A resumed or retried run that produced no new model output adds nothing to the windowed total. A test asserts a retry of an interrupted run does not inherit the earlier attempt's cost.

**Agent details:**

- **Ref:** the ruling is [the momentum principle](../design/momentum-principle.md), sections *What this forbids* and *The one legitimate stop*. The existing measurement is `spendLine` in `tools/tillr-drain/main.go`, which totals one loop's spend when the loop ends; this item adds a windowed total at the loop's start and in the view, and leaves that line as it is.
- **Where:** the loop's start line in `tools/tillr-drain/main.go`; the waiting face and its spend reads in `tools/tillr-view/` (`serve.go`, `serveread.go`); the pack's copy under `yolo-packs/tillr/`.
- **Not this item:** how an ended or orphaned run's spend is recorded belongs to *Add the spend of killed runs to the record* (`item-drain-record-killed-run-spend`). This item reads whatever cost a run's record carries and does not wait on that one.
- **Evidence:** at `2532d9b8`, `rg -n 'window_hours|ceiling_usd' tools/tillr-drain tools/tillr-view` returns nothing, so no part of the withdrawn ceiling is in the tree to remove.

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



### Read a backend's remaining quota directly and keep a reserve

<!-- tillr: item id="item-policy-quota-probe" paths="tools/tillr-run/,tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/design/tillr-claude-backend.md" -->

- **c1:** A backend row may name a `quota` command. The claude adapter ships one that answers the five-hour and seven-day windows as fractions used with their reset times. The commit states how it reads them and what one reading costs; a way that spends no model tokens is used if one exists, and if none does the design document says so with the measurement.
- **c2:** The loop reads a backend's quota before dispatching to it when its last reading is older than a named interval, and records each reading. A backend with no `quota` command is never probed and is treated as always open.
- **c3:** A backend row may set `reserve`, a fraction of the window the loop leaves unused. With `reserve = 0.2` the loop sends no new work to that backend once a window is 80 percent used, so the owner's own sessions keep room. The default is 0.
- **c4:** The waiting face shows each backend with a quota: the fraction used, the reserve, and when it reopens.

### Split a seat's work between backends by a simple share

<!-- tillr: item id="item-policy-seat-shares" paths="tools/tillr-drain/,yolo-packs/tillr/,docs/" blocked_by="" -->

- **c1:** A seat's list entry may be a backend name, as today, or a backend with a `share`. With `[{"backend": "claude", "share": 1}, {"backend": "pi", "share": 1}]` the seat's dispatches alternate between the two; shares of 3 and 1 send three of every four to the first. A list with no shares behaves exactly as today: first backend that is open, in order.
- **c2:** The split is by count of dispatches recorded in the store, so two loops on one store, or one loop restarted, keep the same proportion. A test dispatches 40 fixture items and asserts the counts.
- **c3:** A backend that is shut by quota or reserve takes no share while it is shut, and its share goes to the others in proportion. A fail-over after a refused run still walks the seat's list in order.
- **c4:** The policy check refuses a share that is not a positive number and a list that mixes a share on some entries with none on others, naming the seat.
- **c5:** A seat may set `pick` to `in-order` (today's behavior and the default), `round-robin` (each open backend in turn, which is equal shares) or `random` (one open backend drawn at random per dispatch, weighted by share when shares are given). The draw is recorded on the dispatch, so a run's backend is a fact and never re-derived. A test asserts round-robin over three backends visits each in turn and that 300 random draws over two equal backends land between 120 and 180 on each.

### Move a run to the next backend when its backend runs out, and keep its work

<!-- tillr: item id="item-policy-move-run-on-quota" paths="tools/tillr-drain/,tools/tillr-run/,yolo-packs/tillr/,docs/" blocked_by="item-policy-quota-probe" -->

- **c1:** When a backend's window shuts while runs are on it, each of those runs is stopped as [the restart design](../design/tillr-restart.md) stops a session, and continued on the next backend in its seat's list: a fresh session in the same lane and worktree, as the same attempt, whose first message says the earlier session ran out of quota and names the old transcript, the report if any, and the uncommitted work in the worktree.
- **c2:** Nothing the first session did is repeated by the loop: the worktree is not reset, no retry is spent, and the candidate is judged once. A test cuts a stub run after it has written two files and asserts the continuing session finds both.
- **c3:** A run whose seat has no other backend is paused, not ended, and resumes on the same backend when the window reopens. The waiting face shows it as waiting on that backend with the reopening time.
- **c4:** A run refused at its first message, before any work, is dispatched to the next backend as today, with no continuation message.

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

### Run owner-facing work on lanes outside the cap

<!-- tillr: item id="item-drain-express-lanes" paths="tools/tillr-drain/,tools/tillr-view/,tools/tillr/,tools/tillr-item/,yolo-packs/tillr/,docs/" blocked_by="" -->

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

- **c1:** The settings file takes `lanes.express`, a count of lanes that are not part of the lane cap and are offered only to express work. The default is 0. Express work is an idea the owner saved ([the observatory's first two actions](../design/observatory-actions.md#53-the-item-that-is-created)) and any item whose block sets `express="true"`. An item that answers review comments is not express work in this sense: it runs at its document's desk, as it does today. Whether an item is express is decided from the item's own definition, and a test asserts that a saved idea whose first workflow node names another seat is still dispatched on an express lane.
- **c2:** With every ordinary lane leased, an express item is dispatched on an express lane in the next pass. A test fills two ordinary lanes with runs that do not end, files an express item, and asserts it is dispatched while both are still running.
- **c3:** Ordinary work is never dispatched on an express lane, and an express item that finds no express lane free waits for one or for an ordinary lane, whichever frees first. No running work is stopped to make room.
- **c4:** The waiting face and the lane strip show express lanes apart from the cap: "7 of 10 running, 1 of 2 express". Desk runs are shown as they are today and are counted in neither number.

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
[`OQ-SP8`](../design/tillr-separable-parts.md#OQ-SP8) in the separable-parts design stays open on
one point only: whether AutoQA writes a block for each bug it files into tillr's own store, or
files it with the apply face and lets triage write the block.

<!-- tillr: item id="item-sync-hold-unshared-items" paths="tools/tillr-drain/,tools/tillr-item/,tools/tillr-view/,config/tillr/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** In a store that documents feed, the planner never dispatches an open item that no document block declares to the build seat, whatever seat or workflow the item names. A store is fed by documents when at least one item in it, in any state, is declared by a document block under the repository root; this is the same comparison of store items against blocks that produces `unshared_open`, because the store keeps no separate record that a sync ran. The planner dispatches one triage run for the undeclared item on the design seat, with the reason `triage-no-block`, fenced to the document the item's body reference names. A test stores an undeclared item that names the build seat and a ready declared item, plans both, and asserts the first goes to the design seat with that reason and the second to its own seat.
- **c2:** The triage run's brief carries the item's stored title, body reference and criteria as the request, and the prose guide, and asks for exactly one of three results. The first is a block for the item in that document in the ready-item shape listed above, with the same id, landed through the gate like any design change; one triage run may both tidy the request's wording and shape it into designable work. The second, for a request too thin to shape, is a block that declares the item together with a numbered question to the owner in the same document, saying exactly what is missing, so the item waits on the existing `question-open` hold. The third is a report that the item duplicates a named item or is already done. A test stages a triage run and asserts that its brief names the three results and that its fence admits only that document.
- **c3:** A triage block passes the same checks as a block from any other door: the loop refuses a triage candidate, before the gate and naming the rule, when the sync would report any finding or warning on its block, including `prose-unmarked` and every `prose-shape` warning. After a triage block lands, the next sync binds the stored item to it by id, and the planner sends the item to its own seat with no command from the operator. A test refuses a fixture triage block that lacks the guide mark, then lands a well-formed one, syncs, asserts no finding or warning on it, plans, and asserts the item is dispatched to its seat.
- **c4:** A triage run that reports a duplicate or finished item, or that ends blocked, is recorded as a conflict (`item.conflict`) waiting on the owner that carries the run's report, so the item is held `conflict-owner`. The owner accepts a duplicate or finished verdict by closing the item, or rejects it by answering the conflict. An undeclared item whose body reference names no document goes to the owner the same way, with no run. A test asserts both.
- **c5:** Each undeclared item gets at most one triage run for each version of its request. When the item is still undeclared after its triage run settles and its stored title, body reference and criteria have not changed, the planner raises the owner conflict of c4 instead of a second run; a change to any of the three allows one new triage run. A test settles a triage run that landed nothing, plans twice, and asserts that no second run is staged and the item is held `conflict-owner`, then changes the item's title and asserts one new triage run.
- **c6:** The waiting face shows each undeclared item with its next step and who takes it: triage on the design seat while that run is due or running, and the owner, with the question or the conflict text, once it reaches them. The loop's start line and the status face print the `unshared_open` count. A test asserts both lines of the waiting face and the count.
- **c7:** An item that is closed or landed is never counted as unshared and never triaged. A store with no item declared by any document block is left alone: a test stores open undeclared items with no declared item anywhere in the store, plans, and asserts every one goes to its own seat when otherwise ready and none is sent to triage.
- **c8:** The rule is one named prerequisite of the document-fed landing workflow, kept apart from the planner's general queue rules, and the drain's documentation lists it as such: in a store that documents feed, an item needs a document block before it is built.

**Agent details:**

- **Ref:** the owner's two statements of 2026-11-17, quoted above. The design-seat conflict path this reuses is the one a worker's blocked report takes today; the idea pipeline's stage 2 already rules that a request too vague to split becomes a one-question document that holds on the owner ([the idea pipeline](../design/tillr-idea-pipeline.md#2-the-pipeline-and-one-writer-rules)), and this item applies that disposition to an item already in the store. That pipeline's `intake` face and item kinds are not built, so nothing here waits on them. `docs/design/queue-persistence-ruling.md` names the `unshared_open` counter and its intent. The proposed `plan` seat in [planning and model tiering](../design/tillr-planning-node-model-tiering.md) is not in the seat vocabulary yet; if it lands first, the builder says whether triage belongs on it.
- **Where:** `decide` in `tools/tillr-drain/drain/plan.go`; `conflictDecision` in `tools/tillr-drain/drain/blocked.go` (the design-seat dispatch, and the owner routing for an empty body reference or a design run that ends blocked); the conflict brief and its one-document fence in `tools/tillr-drain/drain/stage.go`; the `unshared_open` count in `tools/tillr-item/item/sync.go`; the waiting face in `tools/tillr-view/waiting.go`; the workflow files under `config/tillr/workflows/` if the prerequisite is expressed there; the pack's copy under `yolo-packs/tillr/`.
- **Open point, one agent or two:** the owner has not said whether making a request well-formed and shaping rough text into designable work are one agent's job or two. This item lets one triage run on the design seat do both for this door and does not decide the question. The rewrite before build sends its rewrite to a cheap seat as a separate item instead, so whether the two doors share one rewriting seat belongs to the same open point. Both doors end in the same ready-item shape.
- **Naming:** the owner asked on 2026-11-17 why the intake face is called `apply` and not `add`. Do not rename it in this item; report what a rename would touch: the face, its callers and tests, the docs and pack skills that name it, and AutoQA's design, which files with it.
- **Evidence:** 2026-11-16, `tillr-item sync --dry-run` against this repository's store printed `unshared_open: 3`; run `run-item-agent-default-bmad-superpowers-skills-a1` ended `RESULT: blocked` after 34 s.

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

A run that a loop stop orphans spends no retry and no re-admission, by design: the worker did not
fail, the loop did. The cost of that rule is that an item whose runs are killed again and again is
sent out again every time, without limit. On 2026-11-16 `item-sync-design-without-item` had five
orphaned attempts, each one a worker started and killed by a loop that was exiting on a fault. The
item is not the cause, and re-dispatching it fixes nothing until the fault is fixed. A person needs
to see the count, and after a stated number of orphaned attempts the item should wait for a person,
with the faults that killed its runs named, instead of being sent out again.

<!-- tillr: item id="item-drain-hold-repeatedly-orphaned" paths="tools/tillr-drain/,tools/tillr-view/,tools/tillr/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

**Ruling, 2026-11-17 (answers the first run's fence conflict):** the first run stopped because c1
needs a reader the fence left out. The settings file is read by `tools/tillr/settings/`, which
refuses any table other than `roll` and `push`, so it refuses `[orphan]`. That directory is added
to this item's paths. The work extends that one reader to take `orphan.limit`, a whole number of
at least one that defaults to three, and adds no second reader of the settings file inside the
loop or the view. The criteria below are unchanged.

- **c1:** The waiting face and the item's record show the number of orphaned attempts per item, and the loop's settings file takes `orphan.limit`, defaulting to three. A test asserts the count is shown for an item with two orphaned runs and for an item with none.
- **c2:** When an item's orphaned attempts reach the limit, the planner holds it with the reason `orphan-limit`, which names each stopping loop's recorded fault, for example `fault: store: database is locked` or `gate-harness-error`. A test with three orphaned runs asserts the hold, the reason text and that nothing is dispatched.
- **c3:** `tillr drain retry` for that item clears the hold and starts a new count, and a run that ends on its own merits (a verdict, a refusal or a report) also starts a new count. A test asserts both.
- **c4:** A held item is not hidden from the operator: the loop's start line lists items held for `orphan-limit` with their counts.

**Agent details:**

- **Ref:** the interrupted-run rule in `tools/tillr-drain/README.md`; the loop stop records in `.tillr/loop/` carry each stop's fault text.
- **Where:** `AttemptInterrupted` handling in `tools/tillr-drain/drain/plan.go`, the `drain.orphaned` events, and the waiting face in `tools/tillr-view/waiting.go`; the settings reader in `tools/tillr/settings/settings.go` for `orphan.limit`; the pack's copy under `yolo-packs/tillr/`.
- **Evidence:** 2026-11-16, `tillr view events --since 0`: five `drain.orphaned` events for `item-sync-design-without-item`, the stops recorded as `fault` and `gate-harness-error`.

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

<!-- tillr: item id="item-land-across-owner-edits" paths="tools/tillr-land/,tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** Paths the landing does not touch: a landing never delays on them, never stashes, and leaves the file completely alone, with the same bytes, the same inode and the same modification time, so an editor never reports that it changed underneath it. This holds whether the file is tracked and modified, staged, untracked, or unmodified but open. Each path's index entry (staged versus unstaged) is unchanged, the landing completes, and the checkout update changes only the paths the landing changed. A test records bytes, inode and modification time before a landing over such files and asserts equality after, which is not an elapsed-time assertion. The existing test covers only the content of an untracked file.
- **c2:** Paths the landing touches, when c4 predicts a clean restore: only the owner's uncommitted content on those paths is copied to the recovery ref of c3, and the trunk moves with each of those files replaced by its final content, without waiting for the owner. Afterwards the file holds what `git pull --autostash` would have left, the landed change plus the owner's edits, with staged and unstaged changes keeping their split. Each such file is written once, as a single replace of its final content; if the sequence unavoidably passes through an intermediate on-disk state, the lander README says so, the state stays inside the lock, and the path count stays minimal. A path whose final content equals what was on disk is not rewritten and keeps its inode and modification time. A test lands over a staged and an unstaged edit and asserts all of this, and that an unrelated dirty path is not in the recovery copy and was not rewritten.
- **c3:** The owner's edits are never absent from disk, and the landing's own work around them is structurally minimal. The lander prepares everything inside the landing's existing lock and before the store's write transaction opens, after the gate verdict, the squash and the observed-trunk check: the prediction, the final content and index entry of every touched path, and the recovery copy of the owner's content on those paths. None of that preparation changes the working tree or the index. Writing the recovery copy removes nothing: each touched file stays on disk as the owner left it until it is replaced once by its final content. Inside the write transaction the lander does only c4's comparison of the touched paths and the final move it makes today, which now installs the prepared content together with the trunk move; no prediction, merge computation, gate, network call or wait happens there, and the exclusion of `tillr item close` that `TestFinalMoveExcludesCloseUntilReceipt` pins still holds. The recovery copy lives in a ref under `refs/tillr/`, never `refs/stash`, because the stash list belongs to the owner. Among the refs under `refs/tillr/`, the drain's run-boundary check ignores `refs/tillr/runs/`, `refs/tillr/landed/`, `refs/tillr/preserved/` and the two trunk-history refs `refs/tillr/trunk-history/observed` and `refs/tillr/trunk-history/lost` today, and reports any other as observed. Each of those names already has a meaning and a lifetime of its own, so the recovery copy goes under none of them: the builder gives it a prefix of its own under `refs/tillr/` and adds that prefix to the ignored refs in `tools/tillr-drain/drain/boundary.go` and to the boundary table in the drain README. The owner's own `git stash` in the main checkout stops nothing, before, during or after a landing. A landing that records no fault leaves no recovery ref behind. A crash between the recovery copy and the install leaves the owner's content on disk and in that ref, and the next landing removes the ref when the content on disk matches it, or reports the path and the ref when it does not. Tests record the order of Git calls and assert which of them run before the transaction opens, run the boundary check across a landing with an owner's stash present and assert it is clean, and kill the lander between the recovery copy and the install.
- **c4:** Prediction before touching anything. Before writing the recovery copy, the lander works out whether restoring the owner's content on top of the landed result would conflict. The computation changes nothing the owner can see: no working-tree, index or ref write, so a scratch index, `git merge-tree` or `git merge-file -p` on the blobs are acceptable and the builder chooses. The landing lock does not stop the owner's editor, so the answer can go stale: inside the write transaction, before the trunk moves or any file is written, the lander checks that every touched path still holds the content the prediction read. If one does not, it changes nothing, removes the recovery ref, and predicts again with the new content. The check also confirms that the main checkout still has the trunk branch checked out, as the prediction found it; if the owner has switched branches, the lander writes no file there and proceeds as `Trunk.Move` does today for a trunk that the checkout does not hold. A save is the bytes readable at a touched path, and no save made up to the lander's last read of the file it replaced there is lost without a report. The lander reads each replaced file again after the replacement and again as its last act: after the trunk move and the receipt, immediately before it removes that file and the recovery ref, with no Git command, store write or wait between that last read and the removal. A changed file found before the trunk moves makes the lander put the owner's files back, saved bytes included, and predict again. One found after the trunk moves undoes nothing: the landed checkout and index stay, the saved bytes are kept in a ref under `refs/tillr/`, and the landing records a fault that names the path and the ref. A save made through the path after the replacement is in the new file and stays on disk. Bytes written after the last read, through a descriptor opened before the replacement, are outside this criterion; the lander README states that limit and what bounds it, and the lander neither waits for a writer nor keeps the replaced file after the landing to narrow it. If the install fails despite a clean prediction, from an I/O error, the lander puts the owner's content back exactly as it was, records a fault, and writes no conflict markers. While undoing an install, the lander reads each file before it removes or replaces it, and bytes that differ from what it installed are an owner's save: they are put back at the path or kept in the ref with a fault. Tests force the failed install and assert byte-identical content and the recorded fault, save a file between the prediction and the check and assert that nothing changed and the landing then succeeds with the saved content, save a file during a landing of several paths and assert that the saved bytes survive on disk or in the named ref, save through a descriptor opened before the replacement once before the trunk moves and once after it and before the last read, and assert the retried landing in the first case and the fault, the ref and an unchanged landed checkout and index in the second, save at a path between an install and its undoing and assert the saved bytes survive, and switch the main checkout's branch after the prediction and assert that no file in it changed.
- **c5:** A predicted conflict is the only state in which a landing pauses because of the owner's working tree, and in that state nothing on disk changes at all, which is the least surprising outcome for it: no conflict, no recovery ref, no markers. The gated candidate is retained and the item waits with a reason naming the conflicting paths and the advice "commit, restore, or change your edit on <paths>; the landing proceeds as soon as it no longer conflicts". The wait re-evaluates when the owner's content or the trunk changes, and other items that do not conflict keep landing meanwhile. A main checkout in the middle of a merge, rebase or cherry-pick also pauses, with a reason naming that state. The hold keeps the name `trunk-path-dirty` unless the builder renames it because the name now misleads; a rename updates every enumerator of drain reasons, including the waiting rules in the view and `TestWaitingEveryDrainReasonHasARule`, the `tillr-operating` skill and the READMEs. Tests assert an unchanged tree (content, inode, modification time), the reason and advice, a re-evaluation after the owner edits, and a second item landing meanwhile.
- **c6:** The prediction cases are decided and stated in the lander README, with a test for each: staged and unstaged overlapping changes keep their split; deleted or renamed paths on either side; mode changes; a binary file with any overlap is a conflict unless identical, and a file is binary when the repository's attributes say so or when Git would detect it as binary; an owner's untracked file on a path the landing adds is a conflict unless identical; a path the landing turns from a file into a directory or back lands when the owner has no content there, including when the directory holds tracked files in subdirectories that the landing deletes, and is a conflict when the owner has; for a directory that becomes a file, the owner's content is every file the main checkout would still hold under that directory after the landing's own deletions, whether modified, untracked, ignored or an editor side file, because none of them can stay at its path, and the hold's reason names the landed path and each file in the way; a path that the repository's attributes convert on checkout (line endings, a clean or smudge filter) is compared in Git's stored form, so a clean checked-out file is never a false conflict, and is written in its checked-out form; a touched path whose parent directory in the main checkout is a symlink is never written through, and is a conflict unless the builder shows that Git's own checkout handles it safely. The drain README, the lander README, the view's waiting rule and the `tillr-operating` skill say what is true, and an rg of those four for the old statement that any dirty landed path holds the landing returns nothing.
- **c7:** A landing that touches a file the owner has open with nothing saved but uncommitted updates it on disk as any `git pull` would; the lander does nothing clever and adds no hold, and the editor's own "file changed on disk" notice is the familiar, least surprising signal. Editor side files are never touched and never count as the owner's content on a landed path: Vim swap and backup files (`.<name>.swp`, `.swo`, `<name>~`, `4913` probes), Emacs lock and autosave files (`.#<name>`, `#<name>#`), and anything the repository's ignore rules exclude. They are not copied to the recovery ref and cause no predicted conflict, with two exceptions: the landing itself adds that exact path, or the landing replaces the directory that holds the side file with a file, which c6 makes a conflict because the side file could not stay where it is. A side file beside a landed file, in a directory that remains a directory, never causes a pause. Tests land a change on a clean tracked file and beside each kind of side file, and assert the landed content, the side files' bytes, inodes and modification times, and no pause; a further test lands a directory-to-file change over a directory holding an ignored side file and asserts the pause, the reason naming both paths, and the side file's bytes, inode and modification time unchanged.
- **c8:** The owner pulling, committing or rebasing forward on `main` while candidates are gated or in flight needs no drain intervention: a forward move is replayed onto, and a rewritten history is refused with `trunk-rewritten`. The lander README states both. A test with an owner commit between gate and landing asserts the landing and the unchanged verdict; the rewritten case is already pinned.

**Agent details:**

- **Ref:** `docs/design/board-state-machine.md` section 9 (D33, "the comparison-and-swap autostashes"); `docs/design/board-state-machine-plan.md` P10 (fences: never drop the stash on a failed apply, never stash during the gate); `docs/design/landing-principles.md`; the boundary table in `tools/tillr-drain/README.md`.
- **Where:** `Trunk.Move` and `Trunk.UncommittedPaths` in `tools/tillr-land/land/trunk.go`; `finishLanding` in `tools/tillr-land/land/landing.go`, which calls `Trunk.Move` inside the write transaction; the ignored ref prefixes in `tools/tillr-drain/drain/boundary.go`; the dirty refusal in `tools/tillr-land/`; `tools/tillr-drain/drain/dirty_hold.go`; `trunk-path-dirty` in `tools/tillr-view/waiting.go`; the drain README section "Owner edits and waiting landings"; `yolo-packs/tillr/skills/tillr-operating/`.
- **Question for the builder, answered in its report and not a criterion:** whether the presence of an editor swap or lock file for a landed path, a sign the file is open and may hold unsaved changes, should appear in the landing's log line as information for the owner. It must not pause the landing, because the owner ruled that a predicted conflict is the only pause.
- **Evidence for the amendment:** 2026-11-17, `finishLanding` calls `t.Move` with the store transaction open, and `TestFinalMoveExcludesCloseUntilReceipt` and `TestGitPreparationAllowsHeartbeatAndClose` in `tools/tillr-land/contention_test.go` pin the exclusion; `tools/tillr-item/` is outside this item's paths and needs no change. The drain README lists `refs/stash` as ignored, and `TestOrdinaryGitUseDuringARunNeverConvictsIt` in `tools/tillr-drain/drain/boundary_ordinary_git_test.go` stashes in the main checkout without stopping anything. `rg -n 'func landerRef|func trunkHistoryRef' -A 2 tools/tillr-drain/drain/boundary.go` at `658465404` shows the ignored refs under `refs/tillr/` are the prefixes `refs/tillr/runs/`, `refs/tillr/landed/` and `preservedPrefix` (`refs/tillr/preserved/`, from `tools/tillr-drain/preserved/record.go`) and the two refs `observedTrunkRef` and `lostTrunkRef` (`refs/tillr/trunk-history/observed` and `refs/tillr/trunk-history/lost`, from `tools/tillr-drain/drain/trunk_history.go`); the README's boundary table gives "Observed" for any other ref inside tillr's namespaces. The first build's reviewer reproduced a write through a symlinked parent directory, an owner's save lost during a landing of several paths, a failed file-to-directory replacement, and a false conflict on a file with converted line endings; that build was not committed, so these are reports, not tests in the tree. The first build also stopped on `tests/tillr-docs.test.py` failing on the base over `tillr drain reset-lane`; `rg -n 'reset-lane' tools/tillr-drain/README.md` now returns nothing and `python3 tests/tillr-docs.test.py` passes. The third build reproduced the directory case in a scratch repository on 2026-11-17: with `notes/draft` tracked and `notes/.draft.swp` ignored, `git status` was empty, a fast-forward that replaced `notes/` with a file `notes` succeeded, and the swap file was gone afterwards. That build committed nothing, so this is a report, not a test in the tree. A later build's test `TestOwnerSaveThroughOpenDescriptorDuringRecoveryRemovalSurvivesLanding` failed on 2026-11-17 with `save lost from disk and recovery: Result:landed err=<nil> refs=""`: it opened a tracked file for writing before the landing, and wrote and closed it while the lander was deleting the recovery ref. The reviewer reproduced that, and also a staged revert left by undoing after the trunk had moved, an `ENOTEMPTY` failure replacing `notes/` with a file when it held the tracked `notes/sub/draft`, a text merge of a file marked `binary` in `.gitattributes` where `git pull --autostash` reports a conflict, and a landing written into a branch the owner created with `git checkout -b` during the landing. That build committed nothing either, so these are reports too.
- **Evidence:** 2026-11-17, `Trunk.Move` runs `git -c merge.autoStash=false merge --ff-only` in the main checkout, so git itself refuses when the move would overwrite uncommitted content and otherwise advances only the changed paths; git's fast-forward keeps staged entries on untouched paths, but no test pins it. `TestALandingLeavesUncommittedContentOnAnUntouchedPathAlone` (untracked file only) and `TestALandingIsRefusedWhenTheTrunkWorktreeHoldsUncommittedContentOnALandedPath` in `tools/tillr-land/main_test.go`, and `TestUnrelatedDirtyPathDoesNotDelayLanding` in `tools/tillr-drain/drain/dirty_hold_test.go`, pin the two cases. `rg -n -i 'MERGE_HEAD|rebase-merge' tools/tillr-land tools/tillr-drain --glob '*.go'` returns nothing, so no code names an in-progress merge or rebase. `TestLockedLandingAcceptsFastForwardBeyondObservedTip` in `tools/tillr-land/trunk_history_test.go` pins the forward move, and `TestTheDrainLandsThroughTheRealAdapterAndTheRealLander` in `tools/tillr-drain/drain/step_argv_contract_test.go` pins `trunk-rewritten`. `rg -n -i dirty yolo-packs/tillr/skills/tillr-operating/` returns nothing (2026-11-17).

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

### Make the cloud-usage mod pass strict plugin validation

The cloud-usage mod keeps a journal of Claude Code's model usage. Its README, and the criteria of
the item that adds channel and route intent to it, list `claude plugin validate --strict` among the
checks that must pass. On 2026-11-17 that command fails on the untouched mod at `main` under the
installed Claude Code. A worker building the channel-route item saw the same failure on an export of
the base, so it stopped as its brief says and the item was held. Until the check passes on the
base, no change to the mod can show that it did not break it.

Every message is a TypeScript warning from the mod's hooks, and `--strict` treats warnings as
errors. The mod's `tsconfig.json` extends `.claude-plugin/types/tsconfig.json`, which Claude Code
writes on first load and the repository ignores. That directory is absent here, so this is the
leading suspect, and the other two mods fail the same way. That is not yet established, and the
builder settles it.

<!-- tillr: item id="item-mod-passes-strict-plugin-validate" paths="yolo-packs/matt-mods/mods/cloud-usage/,yolo-packs/matt-mods/README.md,tests/" blocked_by="" prose="2" -->

- **c1:** `claude plugin validate --strict yolo-packs/matt-mods/mods/cloud-usage` exits 0 under the installed Claude Code. The report gives the command, the build from `claude --version`, and the date.
- **c2:** The commit message names the root cause of the TypeScript warnings, and says whether the generated `.claude-plugin/types/` declarations were missing, the mod's own `tsconfig.json` or sources were wrong, or both. The fix removes that cause. It does not drop `--strict`, add suppression comments, or exclude the hooks from checking.
- **c3:** A test in the repository's test directory runs the strict validation on the mod and asserts that it exits 0. It is `tests/cloud-usage-plugin-validate.test.py` unless the builder finds an existing test that already invokes the validator, which was not the case on 2026-11-17. When the `claude` binary is absent, the test skips and prints that reason. The report says whether the gate's environment has `claude`, and what runs there if it does not.
- **c4:** The mod's README and the matt-mods README give the one validation command that works from a clean checkout, including any step that creates the declarations the check needs.
- **c5:** `claude plugin test` on the mod and the existing cloud-usage Node and Python suites still pass, and `yolo pack lint yolo-packs/matt-mods` passes.

**Agent details:**

- **Where:** `yolo-packs/matt-mods/mods/cloud-usage/tsconfig.json`, `hooks/*.ts` and `tests/*.ts` in that mod; the "Develop and test" section of `yolo-packs/matt-mods/README.md`; the "Offline development" section of the mod's README.
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

<!-- tillr: item id="item-shard-the-tillr-go-suites" paths="tools/,.justfile,tests/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

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

- **c1:** A runner under the tools directory builds each package's test binary once, then runs K concurrent shards of it, balanced on a recorded per-test duration file. Tests missing from that record go to a fallback shard, so a new test is never silently skipped. A test seeds a record that omits one test and asserts the test runs.
- **c2:** The runner passes exactly the set of test names that `go test ./... -count=1` passes, per package. A test compares the two name lists on the drain module and asserts they are equal.
- **c3:** The runner exits non-zero when any test fails, when a shard cannot start, or when a shard exits without reporting every test assigned to it. A test per case asserts the exit code and a message that names the shard.
- **c4:** Shards do not interfere: each gets its own scratch subdirectory and its own git global configuration file. A test runs two shards that write to the same relative name and asserts neither sees the other's file.
- **c5:** The drain's rolling tier and `just done` run the drain suite through the runner, and the other tillr modules do too wherever the measured wall time drops. The report lists each module with its before and after wall time.
- **c6:** The suite cost estimate in the gate's check planner is refreshed from a measurement of the runner, with the command and date in the comment beside it.
- **c7:** The worker brief and the `tillr-operating` skill tell workers how to run the sharded runner and when to prefer a targeted `-run`.
- **c8:** The report records the drain suite's wall time before and after, as command, result and date. No test asserts elapsed time.
- **c9:** `python3 tools/gate/checks/scratch.py`, run from the repository root on the committed tree, prints nothing and exits 0. The report gives that command and its output. No tracked Markdown file the change adds or edits names the durable scratch directory, by variable or by path, outside a fenced block.

**Agent details:**

- **Where:** a new runner beside `tools/fork-check.py`; the drain module in `tools/tillr-drain/`; `GO_TOOL_COST_ESTIMATE` and its comment in `tools/fork-check.py`; the `.justfile` recipe that runs the gate; `yolo-packs/tillr/`.
- **Evidence:** 2026-11-16, `go test ./... -count=1` in the drain module: 64.0 s and 64.3 s wall at load 3 to 6 (package `drain` 215 tests in 64 s, root package 247 tests in 55 s, run side by side), 69.3 s at load 17 to 23, 118 s with `-p 1`. User 70 s plus system 37 s over 64 s wall is about 1.7 cores. Compile and link cost about 2 s. A run makes about 18,900 `git` execs (11,505 of them `git rev-parse`). Turning git's fsync off changed nothing (63 to 66 s); test directories on a memory filesystem saved about 5 s and made `TestFenceViolationScopesAForkCheckoutToItsRepoRelativePath` fail.
- **Evidence:** 2026-11-16, sharding with a test binary built once per package and balanced on recorded durations: K=6 took 13.7 to 14.5 s, K=8 took 14.5 s, K=4 took 17.9 s, at load 7 to 30. The only failures were three tests that need the `config/` directory, absent from the exported copy.
- **Evidence:** 2026-11-16, `t.Parallel()` trial on the 127 of 247 root tests that do not call `t.Setenv`: root wall unchanged at 55.0 s. `t.Setenv` appears at 43 call sites in the root package and 17 in `drain`, and the stub adapters read `GATE_ROOT`, `STUB_ROOT`, `RETIRE_*` and `PASS_*` from the environment.
- **Evidence:** the cost estimate was recorded at 30.9 s on 2026-11-15; `rg -n 'COST_ESTIMATE|30\.9' tools/fork-check.py` finds it (2026-11-17).

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

<!-- tillr: item id="item-whole-tree-checks-in-done-and-rolling" paths="tools/tillr-drain/,tools/tillr-view/,tools/gate/,tools/fork-check.py,.justfile,tests/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

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

- **c1:** `just done` runs every fast-tier declaration under the gate's checks directory that has tree scope, through the same program the gate uses, over the range from the last verified trunk to HEAD. A test makes a temporary repository with a failing tree check and asserts `just done` exits non-zero and names the check.
- **c2:** The set cannot drift. A test lists the fast tree declarations and fails when one is neither run by `just done` nor named in a short allowlist with a reason.
- **c3:** The rolling run runs the same checks over the range it covers. A red tree check there is a red rolling run: the drain files one deduplicated trunk fixer naming the failing check ids, exactly as it does for a red fork-check, and the watermark does not advance. A test with a stub rolling program asserts the fixer and the unmoved watermark.
- **c4:** A candidate refused only because a tree check is already red at its base is not charged, and its item comes back by itself. The loop records the existing `trunk-already-red` refusal naming the red commit and the checks, files the fixer, and sets the item to `blocked` with the fixer as its waiter, as a fork-check acquittal does today. While the fixer is not landed, the planner's reason for the item is the existing `trunk-broken` and its decision names the fixer. After the fixer is `landed`, the next pass returns the item to the queue with no command from anyone and dispatches a fresh run from the repaired trunk without an authorized retry. Two tests. The first asserts that the hold has the reason `trunk-broken`, names the fixer, and carries no failing verdict and no design conflict. The second marks the fixer landed, runs the loop again with a green gate, and asserts that the item was dispatched and landed; it also asserts that an item whose fixer was closed without landing stays `blocked`.
- **c5:** A candidate that is red on a tree check which passes on its base stays charged to the candidate. A test asserts the ordinary refusal.
- **c6:** The drain README, the `tillr-operating` skill (`yolo-packs/tillr/skills/tillr-operating/`) and the waiting view's `trunk-broken` rule in `tools/tillr-view/waiting.go` say which checks run where. The waiting rule's third field, which reads `cause not recorded` today, names the trunk fixer as what the item waits on, and a test in `tools/tillr-view/` asserts it. An rg of those three for the old statement that the rolling run replaces the gate's tree checks returns nothing.
- **c7:** The report names every fast tree check that the first run of the new `just done` found red on the trunk, as command, result and date, and says which were fixed.
- **c8:** `python3 tests/observatory-dev.test.py` passes in a checkout that has no `tools/tillr-view/web/node_modules`, such as a fresh lane worktree, and still passes in one that has it. The suite leaves that directory as it found it: absent when it was absent, and untouched when it was present, so a later real run of the recipe still installs its modules. The report gives the command, its result and the date for a run in the lane worktree.

**Agent details:**

- **Ref:** `docs/design/landing-principles.md`, the rule that a mechanism whose cost rises with trunk activity is pointed the wrong way: the tree checks are cheap (about 0.5 s each in the fast tier), so running them on every hand commit is the right direction. `tools/tillr-drain/README.md`, the section on broken-trunk detection.
- **Where:** `tools/gate/change-gate.py` runs `tillr-gate checks --fast-tree --base --head`; the declarations are under `tools/gate/checks/`. `DefaultReplayProgram` and `DefaultRollingProgram` in `tools/tillr-drain/drain/trunk.go`; `trunkAlreadyRed` there and `gateFailureContext` in `tools/tillr-drain/drain/steps.go`; the `done` recipe in `.justfile`; the rolling tier in `tools/fork-check.py`.
- **Evidence:** 2026-11-17, a hand commit on the trunk that put a documented build directory in a tracked README passed `just done`. `python3 tools/gate/checks/scratch.py` at the trunk then exited 1 with two findings, and the drain's gate refused the next candidate on the `scratch` check. A rolling run over the range holding that commit finished green and moved its watermark.
- **Evidence:** 2026-11-17, why the candidate was not acquitted. `DefaultReplayProgram` is `python3 tools/fork-check.py --replay --only {only}`, and `{only}` is the failing check ids from the gate's verdict. The replay matches fork-check kinds, and the gate's tree-check ids are a separate set. `python3 tools/fork-check.py --replay --only scratch` in a clone of the trunk printed `no planned check matches --only` and exited 2, which the loop reads as "could not replay", so it leaves the refusal standing and never files a fixer. The acquittal step therefore exists, but only for ids that fork-check also knows. A change-gate tree check has no replay path, so the gate's refusal stands even when the trunk is the one that is red.
- **Evidence:** 2026-11-17, `just done` after the repair ran five fork-check checks (`file-size`, `tillr-backends`, `tillr-docs` and two `go-tool` suites) and none of the gate's tree checks. `tillr-gate checks --fast-tree` over the same range ran all eleven declarations and passed.
- **Evidence:** 2026-11-17, `python3 tests/observatory-dev.test.py` in a lane worktree at trunk commit `1524cddc9`, with no change applied, exited 1 after 4 min 40 s with seven failed cases, all in the supervision half. Its output held `observatory-dev: npm ci (there are no node_modules)` and then `tools/observatory/run.sh: line 174: …/tools/tillr-view/web/node_modules/.tillr-platform: No such file or directory`. `ls tools/tillr-view/web` in that worktree shows no `node_modules`; the directory is ignored by `.gitignore` and exists in the main checkout. The fourth run's gate log shows the same seven cases and the same two lines, with the check selected `because: the dev recipe, Procfile, tool declaration or supervision suite changed`.
- **Where (c8):** `write_stubs` in `tests/observatory-dev.test.py` writes the stub `npm`, whose comment reads `` `ci` is a no-op ``; the supervision cases start the recipe with the repository as its working directory. `ensure_modules` in `tools/observatory/run.sh` runs `npm ci` when the directory is missing and then writes the stamp.
- **Evidence:** 2026-11-17, the sixth run added a loop test that acquits an item on a red tree check, marks its fixer `landed` and runs the loop again with a green gate. `go test ./... -run TestAcquittedTreeCandidateIsGatedAfterItsFixerLands` in `tools/tillr-drain` failed with `stop=queue-drained, dispatched=0, landed=0` and the item still `blocked`. Read at trunk commit `f721f4e12`: `rg -n 'StateBlocked' tools/tillr-drain/drain --glob '!*_test.go'` printed five lines: the constant and its comment, the one write in `acquitTrunkAlreadyRed`, and two reads in the planner. None of them moves an item out of that state.
- **Where (c4):** `acquitTrunkAlreadyRed` in `tools/tillr-drain/drain/trunk.go` writes the `blocked` state and the waiter. `isBlocked` and the `switch` in `decide`, in `tools/tillr-drain/drain/plan.go`, put the `blocked` rule before the `trunk-broken` rule. `mayBeARetry` in the same file is the rule that holds a second attempt for an owner. `ReadWaitingOn` in `tools/tillr-item/item/waiting.go` reads the waiter and the underlying state, and the drain already imports that package, so nothing outside the paths changes.

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

### Stop the rolling checks when the loop stops

When the loop is stopped during a rolling run, the loop kills the check program and expects its
children to go with it. They do not. The rolling program, `tools/fork-check.py`, starts every
check in a session of its own so that its own cancellation can reach it, and the loop's kill
reaches only the program's process group. Each `go test` therefore keeps running, and the loop's
cleanup then deletes the rolling worktree underneath it. That is what happened at 05:17 on
2026-11-17, when an orphaned `go test -parallel 48` kept running for about two minutes after a
stop. The fix that was meant to prevent it passes its test only because the test's child stays in
the program's own group. Rolling is off in the loop that runs today, so this bites only once it is
turned on.

<!-- tillr: item id="item-loop-stop-ends-rolling-checks" paths="tools/tillr-drain/,tools/fork-check.py,tests/fork-check.test.py,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** A loop stop during a rolling run ends every check the run started, including checks that `tools/fork-check.py` placed in their own session, before the loop removes the rolling worktree. A test starts a check whose child calls `setsid`, stops the run, and asserts that no process from that tree is alive afterward.
- **c2:** `tools/fork-check.py` ends its running checks when it receives SIGTERM, and the loop sends SIGTERM to the program and waits briefly for it before it sends SIGKILL. A test sends SIGTERM to the program and asserts that its children are gone.
- **c3:** The replay step that also runs checks in a scratch worktree ends its checks the same way. A test covers it with the same `setsid` child.
- **c4:** The regression test for c1 fails when the SIGTERM step is removed.

**Agent details:**

- **Ref:** the review's finding M1-1, and the fix this one completes in `runCheckGroup`.
- **Where:** `runCheckGroup` and `endProcessGroup` in `tools/tillr-drain/drain/steps.go`; `_run_cancellable`, `_kill_process_group` and `Cancellation.cancel` in `tools/fork-check.py`.
- **Evidence:** 2026-11-17, `rg -n 'start_new_session' tools/fork-check.py` finds `_run_cancellable` starting each check in its own session, and `rg -n 'cmd.Cancel' tools/tillr-drain/drain/steps.go` finds the loop's kill aimed at `-cmd.Process.Pid`, one group. A copy of the shipped test whose child calls `os.setsid()` failed with "the rolling check's child ... outlived the stopped rolling run".

### Show a moved patch branch to the owner before a push publishes it

The drain no longer stops a run because `refs/heads/patch/<fork>` moved while it ran. That is
deliberate: exporting a fork series is ordinary work, and stopping every other run for it was
a fault of its own. But the run is now only told about the move, in one loop log line and in the
run's `boundary.check` event, and nothing else reads either. A worker on an unrelated lane that
moves a patch branch passes its fence and its gate, and the owner's next push then publishes the
branch through the pre-push hook, after which the next launch builds pi from it. Nothing holds
that move and nothing shows it on the waiting view. The owner should be shown it, and the push
should wait for an answer.

<!-- tillr: item id="item-patch-branch-move-held-visible" paths="tools/tillr-drain/,tools/tillr-view/,bin/patch-branches-publish,tests/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** A move of `refs/heads/patch/<fork>` that the run boundary observes and that is not the run's own delivery or a landing appears on `tillr view waiting` as a row that needs the owner, naming the fork, the run and the old and new commits. A test records such an observation and asserts the row appears.
- **c2:** `bin/patch-branches-publish` refuses to publish a patch branch with an unacknowledged observed move, and its message names the fork and how to acknowledge. A test asserts the refusal and asserts a branch with no observation is still published.
- **c3:** Acknowledging the observation clears the row and lets the push through, and the acknowledgement is recorded with who gave it and when. A test asserts both.
- **c4:** A move that the lander marked, or that is the run's own delivery, raises no row and no refusal. A test asserts neither appears.

**Agent details:**

- **Ref:** the review's finding M2-3, and `item-drain-fork-lane-safety` c3 for who may move the branch.
- **Where:** `CompareRunObserved` in `tools/tillr-drain/drain/boundary.go`; the report at the `recorded, not attributed to the run` line in `tools/tillr-drain/drain/loop.go`; `tools/tillr-view/waiting.go`; `bin/patch-branches-publish`.
- **Evidence:** 2026-11-17, `rg -n 'observed = append' tools/tillr-drain/drain/boundary.go` shows `compareRunRefs` feeding `observed`, not `violations`, and `rg -n 'patch/' tools/tillr-view/waiting.go` finds no waiting rule for an observation. Until this lands, before a push, check the loop log for `recorded, not attributed to the run` naming `refs/heads/patch/`.

### Hold a lane out of rotation while its ended run's processes are alive

When a run ends, the loop frees its lane for the next item. If processes that carry the run's
markers are still alive, the loop prints a line and frees the lane anyway. That trades a lane
leased forever for a quieter fault. `tillr-run exec` ends the worker's process group only when the
run is cancelled, so after a normal exit the worker's background children survive: tool servers,
helper runners and jobs the worker started with `&`. If one of them is writing in the lane's
worktree, the next item is staged there and the stray's writes land in that item's candidate. The
next item's fence and gate judge them, so the harm is a misattributed refusal and not a bad
landing, but the owner gets no warning beyond one log line.

<!-- tillr: item id="item-lane-release-waits-for-run-children" paths="tools/tillr-drain/,tools/tillr-run/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** A lane whose ended run still has a live process carrying its markers, with its working directory under the lane's directory, is not staged with a new item. It is released in the store but stays out of rotation, with the same event and waiting row a lane refusal has, until no such process is left. A test leaves a marker-carrying process in a lane and asserts that the lane is not offered, and is offered once the process exits.
- **c2:** The waiting row names the process ids and how to end them. A test asserts the text.
- **c3:** A marker carrier whose working directory is outside the lane does not hold the lane. A test asserts the lane is offered.
- **c4:** A worker's background child is ended when its run exits normally, or the item records why that is not done. A test runs a worker that starts a `sleep` child, lets the run end, and asserts the child is gone or the lane is held.

**Agent details:**

- **Ref:** the review's finding M3-2, and the lane-rotation mechanism in `tools/tillr-drain/drain/lane_rotation.go`.
- **Where:** `releaseExamination` and `processIsRunFace` in `tools/tillr-drain/drain/pass.go`; `cmd.Cancel` in `tools/tillr-run/run/exec.go`; the lane rows in `tools/tillr-view/waiting.go`.
- **Evidence:** 2026-11-17, `rg -n 'released anyway' tools/tillr-drain/drain/pass.go` finds the loop releasing the lane with the strays left running, and `rg -n 'cmd.Cancel' tools/tillr-run/run/exec.go` finds the group kill only on cancellation.

### Take a lane out of rotation when its branch is held at loop start

A lane whose branch is checked out by a worktree elsewhere is taken out of rotation when an item is
staged on it, and the loop carries on with the other lanes. At loop start the same condition is
handled differently: the check that creates the lane worktrees returns the refusal as a fault, and
the loop does not start. The owner widens the loop from five lanes to ten, an old worktree that
still holds one of the new lanes' branches stops it, and the keeper restarts it into the same
fault. The lane that is blocked should wait and the rest should run.

<!-- tillr: item id="item-loop-start-holds-blocked-lane" paths="tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** At loop start, a lane whose branch another worktree holds is taken out of rotation with the existing `lane-branch-held` condition, and the loop starts with the other lanes. A test starts a loop with one such lane and asserts the loop runs and the lane is not offered.
- **c2:** The held lane shows on `tillr view waiting` with the path of the worktree that holds the branch. A test asserts the row.
- **c3:** When the holder is removed, the lane returns to rotation at the next re-check. A test asserts it.
- **c4:** A start where every lane is held still refuses to run, with a message that names each holder. A test asserts it.

**Agent details:**

- **Ref:** the review's precaution 2, and `ReasonLaneBranchHeld` in `tools/tillr-drain/drain/lane_rotation.go`.
- **Where:** `stageLanes` in `tools/tillr-drain/drain/loop.go`, and `refuseBranchHeld` and `branchHolder` it calls.
- **Evidence:** 2026-11-17, `rg -n 'refuseBranchHeld' tools/tillr-drain/drain/loop.go` finds the refusal returned from `stageLanes`, and `rg -n 'stageLanes' tools/tillr-drain/drain/loop.go` finds the call at loop start returning its error.

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

<!-- tillr: item id="item-trunk-fixer-fence-covers-failing-check" paths="tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** A trunk fixer's fence includes the failing check's own sources and tests, meaning the files its plan declares it reads, and not only the paths it recorded writing. A test files a fixer for a failing check whose test file no commit in the range touched, and asserts that file is in the fence.
- **c2:** The fence also includes the paths of every commit in the red range, not only the tip's. A test with a culprit commit two commits below the tip asserts the culprit's paths are in the fence.
- **c3:** The gate's own code stays outside the fence, as it is now. A test asserts that widening the fence does not add it.
- **c4:** A fixer that reports its fix lies outside its fence widens the fence once, to the failing check's whole watched tree, or escalates to the owner with the paths it needs named. A test asserts one of the two happens and the fixer is not left as an unanswered conflict.
- **c5:** A new trunk tip filed while a fixer for the same failing check is unresolved attaches to that fixer rather than filing another with the same fence. A test advances the tip three times and asserts one open fixer.
- **c6:** The `tillr view waiting` row for a held queue names the fixer, the check it was filed for and whether it is waiting on the owner. A test asserts the text.

**Agent details:**

- **Ref:** the fixer description at the top of `tools/tillr-drain/drain/trunk.go`, and the rolling-run branch that files it.
- **Where:** the rolling red branch in `tools/tillr-drain/drain/trunk.go` (`suspect`, `watch`, `fileTrunkFixer`, `trunkFixerPaths`); `readSpansWithStatus` in `tools/tillr-drain/drain/gate_spans.go`, which takes a failing check's watch set from the `writes` attribute of its span; `conflictDecision` in `tools/tillr-drain/drain/blocked.go`.
- **Evidence:** 2026-11-17, `go test -run TestSyncDryRunFreshStore ./...` in `tools/tillr-item` failed because change `b0c26afac` made store init create every item table, and landed fix `b87a2a97e` edited only `tools/tillr-item/sync_test.go` and its pack mirror.
- **Evidence:** 2026-11-17, the keeper log's six `filed fixer-main-` lines, one per trunk tip, each listed `tools/tillr-item/` sources changed in the range and `tools/tillr-store/main.go`, and none listed `tools/tillr-item/sync_test.go`.
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

<!-- tillr: item id="item-pt-cache-rewrites" paths="docs/,tools/tillr-run/,yolo-packs/tillr/" blocked_by="" prose="2" -->

- **c1:** A finding in `docs/research/` names the cause of each pattern, shown on at least three run logs per pattern, with the turn numbers and the token counts that show it.
- **c2:** The finding prices the repeated writes for the Sol build seat and for the Fable design seat separately, from the run logs, as command, result and date.
- **c3:** The finding reproduces the first pattern with the smallest pi session outside the loop that shows it, or says why it cannot be reproduced, and records that session's model, turns, tokens and cost.
- **c4:** When the fix lies in the pi adapter or in how tillr starts pi, the item makes it. A test asserts the changed arguments or environment, and one measured run after the fix shows the pattern gone, as command, result and date.
- **c5:** When the fix belongs in pi itself, in its patch series, on the provider's side or in a yolo pack's settings, the item does not make it. The finding states the change, where it belongs and its expected saving.
- **c6:** The finding says whether the one-hour cache retention that every pi session runs with is still worth it for runs that are never resumed, given that resumed designer sessions depend on it, and the item changes nothing about that retention.
- **c7:** Without a fix, nothing outside `docs/` changes. With one, `tools/tillr-run`'s tests pass, and a run that never reaches the pattern gets the same arguments and environment as before, which a test asserts.
- **c8:** The item writes nothing to the store, and no measurement in it is a test that asserts elapsed time.

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

<!-- tillr: item id="item-seed-rank-keeps-workflow" paths="tools/tillr-item/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** A rank-only seed of an item that has a workflow leaves the workflow as it was. A test stores an item with the workflow `scout-then-build@v1`, seeds a new rank for it, and asserts the stored workflow is unchanged.
- **c2:** The same test reads every stored column and every criterion and edge of the item before and after the seed, and asserts that only the rank, `updated_at` and `event_seq` differ.
- **c3:** The test fails on the code as it stands, because the stored-item read leaves the workflow out. The fix reads the workflow where the stored item is read, so that every later write of the item carries it.
- **c4:** An item with no workflow re-ranks exactly as before. A test asserts an unchanged result for it.
- **c5:** The README of `tillr-item` and the pack's `tillr-operating` skill no longer tell an operator to skip items that have a workflow when ranking, if they say so, and say that a rank-only seed changes the rank alone.

**Agent details:**

- **Ref:** `item-seed-retire` in [the convergence design](../design/tillr-convergence.md#retire-the-seed-file-and-its-command) and `item-oa-move-to-top` in [the observatory actions design](../design/observatory-actions.md) rework how a rank is written, and both are queued. This item is small and should land first, so the ranking method is safe for every item until they land; both wait for it.
- **Where:** `storedItem` in `tools/tillr-item/item/seed.go`, whose `SELECT` lists the columns it reads and omits `workflow`; `planSeed`, which builds the re-rank payload from that stored item (`rerank := held`) and so writes whatever `storedItem` left empty; `sameDefinition` in the same file, which compares only definition fields and is not the cause. The itemPayload type, and the apply path that writes it, carry the workflow field.
- **Evidence:** 2026-11-17, on a throwaway store: an item with `workflow` `scout-then-build@v1` came back with an empty workflow after a rank-only `tillr item seed`.
- **Evidence:** 2026-11-17, `rg -n 'workflow' tools/tillr-item/item/seed.go` finds nothing, and `storedItem` there reads `item_id, title, body_ref, state, design_doc, allowed_paths, rank, seat, prose` from `items`, so `planSeed` re-applies an item with no workflow. `Workflow` is a field of the payload in `tools/tillr-item/item/apply.go`, which the apply path writes.
- **Evidence:** 2026-11-17, the day's ranking run re-ranked 92 items one seed each and skipped none for carrying a workflow, so no live item has one yet and nothing was cleared.
