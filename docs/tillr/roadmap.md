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

Twelve questions are left for the owner on this page: four from this round's designs, and eight
that the pi roadmap's reconciliation of 2026-11-16 found were really tillr's. Each has options and a recorded leaning.
They are the ones no recorded principle could answer. Twenty-three others that stood here were
settled on 2026-11-15 and 2026-11-16 by the session's designer, at the owner's request; each
says so in its design and is the owner's to overturn.

1. [`OQ-SH2`](../design/tillr-selfhosted-build.md#OQ-SH2): what becomes of the pack mirror that
   doubles every tillr change. First because it is a cost on every landing but stops nothing.
2. [`OQ-GM1`](../design/tillr-goal-monitor.md#OQ-GM1) and
   [`OQ-GM2`](../design/tillr-goal-monitor.md#OQ-GM2): which goal the first scheduled check-in
   carries, and whether a host timer watches it. Only the owner can name the goal.
3. [`OQ-AQ2`](../design/tillr-autoqa.md#OQ-AQ2): whether a bug AutoQA confirms is queued for
   building at once or held until its findings have proved accurate. Last because AutoQA is the
   last thing built.

4. [Criterion repair](../design/criterion-repair.md), five questions: who may supersede a frozen
   criterion, what the judge sees, and what happens to a stale premise or an unsatisfiable one.
   With the judge, which it amends; that a criterion is repaired by a supersession record and
   never by an edit is already settled.
5. [What a lane's agent is told](../design/agent-context-delivery.md), three questions about the
   context a worker receives. Last because the brief works today and these refine it.

[The judge and lifecycle](../design/tillr-judge-and-lifecycle.md) left this list: every question
it carried is a row of its ledger, and on 2026-11-16 it was cut into
[thirteen build items](../design/tillr-judge-and-lifecycle.md#8-build-order) that live in the
design. The first stops a gate pass from retiring a criterion only the owner can settle and waits
on nothing. Part of the design was already built under other items, and
[its seventh section](../design/tillr-judge-and-lifecycle.md#7-what-the-tree-already-holds)
measures which part. AutoQA still carries one design-seat item that turns the design into build
items.

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

<!-- tillr: item id="item-vantage-uvx-default" paths="tools/tillr-drain/,tools/tillr-view/,tests/,yolo-packs/tillr/,docs/tillr/roadmap.md" prose="2" -->

- **c1:** With no explicit override, planner and view index reads use the package runner; a regression uses a recording stub and no standalone checker and asserts the full argument list.
- **c2:** Explicit overrides still run exactly the chosen command, and missing or failing commands and malformed index output hold affected items rather than treating them as question-free; regression tests cover these cases.
- **c3:** Help text names the actual default, the committed delivery matches its sources, and the applicable tests and pack lint pass.

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
  tillr has no due date or interval on an item yet. It is the substrate of the goal monitor in
  [entry 1](#1-rulings-that-are-waiting-on-the-owner).

## Icebox

Wanted, not now, by the owner's call. Picking one up means giving it a place above.

- **Prices on the observatory**, iceboxed 2026-11-15. The owner wants to see what work cost in
  dollars and ruled that the pricing cannot be computed accurately enough yet, so
  [the pages show usage only](../design/observatory-redesign.md#66-what-a-run-used). Picking it up
  means settling how a price is derived per backend, including a subscription that is not billed
  per run, cached tokens, and models whose price changes.

- **The Muse backend** ([design](../design/tillr-muse-backend.md#1-what-a-backend-is)), iceboxed
  2026-11-15: the owner is not subscribing to a Meta plan. The adapter stays in the tree and
  [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json) keeps it out of
  every fail-over. Picking it up means subscribing, naming `muse` in a policy again, and cutting
  its second stage from the design.

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

<!-- tillr: item id="item-gate-pinned-fork-inputs" paths="tools/gate/,tools/fork-check.py,tests/fork-check.test.py,bin/pi-forks,tests/pi-forks.test.sh,yolo-packs/,docs/" blocked_by="item-gate-needs-unavailable" -->

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

<!-- tillr: item id="item-gate-runner-pays-first" paths="tools/gate/,tools/tillr-loop/,tools/tillr-drain/,tools/fork-check.py,tests/fork-check.test.py,yolo-packs/tillr/,docs/" blocked_by="" -->

- **c1:** A candidate that changes a path under the runner's own directory has the runner's suite added to its landing checks, whatever `landing_strategy` says, and does not land while that suite is red.
- **c2:** The same holds for `tools/fork-check.py` and its suite for as long as that file exists.
- **c3:** Such a landing is left out of the ten-second budget measurement and is counted separately, so it cannot make the budget test fail or hide a slow ordinary landing.
- **c4:** A test lands one candidate that changes the runner and one that does not, and asserts the first ran the suite before the merge and the second did not.

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

<!-- tillr: item id="item-obs2-stop-says-what-and-next" paths="tools/tillr-view/,tools/tillr-drain/,yolo-packs/tillr/,docs/" -->

- **c1:** When the loop is not running, the header shows four things: that it is stopped and since when, the item and step that stopped it, the reason in one plain sentence taken from the loop's own stop record, and who acts next (the loop on restart, a named item's rework, or the owner). The sentence "diagnose the recorded fault before restarting" is gone. A test renders the header from a fixture stop record for each stop reason the drain can write and asserts none falls back to a generic line.
- **c2:** The header says whether a loop process is alive right now, from the loop's heartbeat record, and says "no loop is running" when the last heartbeat is older than twice the heartbeat interval. A test covers a live, a stale and an absent heartbeat.
- **c3:** A page that asks the server for something the server does not serve shows one line: this page needs a newer server, the server's build and the page's build, and the command that restarts it. It never prints a route list or the word "face". A test serves the page against a server without the route and asserts the line.
- **c4:** Every term of art on a page has a one-sentence definition one hover or one click away, from one glossary file in the tree: pass, hold, hold-only pass, action, lane, run, candidate, gate, landing, heartbeat, session. A test fails when a page uses a glossary term with no definition attached, or when a term on the list has no entry.
- **c5:** The Passes page's filters are labeled by what they do: "Item" filters to passes that touched one item, and "Action" is a list of the actions that exist, not a free text box. The hold-only checkbox reads "show passes where nothing moved".

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

<!-- tillr: item id="item-prose-rewrite-before-build" paths="tools/tillr-item/,tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,.tillr.toml,docs/" blocked_by="item-settings-file" -->

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


### Leave runs alone when the loop stops, and pick them up when it starts

<!-- tillr: item id="item-drain-stop-keeps-runs" paths="tools/tillr-drain/,tools/tillr-run/,yolo-packs/tillr/,docs/" blocked_by="" -->

- **c1:** When the loop exits for any reason other than an operator's explicit "stop and end the runs", the runs in flight are not signaled. A test stops the loop on a fault with two fixture runs in flight and asserts both finish.
- **c2:** The next loop settles and judges those runs once each, including a run that finished while no loop was running. A test asserts one attempt and one landing per run.
- **c3:** `tillr drain stop --end-runs` is the only way to end runs with the loop. The command is a new `stop` face of `tillr-drain`; without the flag it stops the loop and signals no run. With the flag, every run in flight is ended through one function in the drain, and nothing else in the drain signals a run. That function is built against the tree the lane is staged from. Where `tillr run pause` exists there, the function asks each run to pause as "Pausing a session" in the [restart design](../design/tillr-restart.md#pausing-a-session) states, so the run resumes instead of starting over. Where it does not exist, the function sends the run face SIGTERM, which is what a loop stop does to every run today, and each run so ended is covered by c4: it is no attempt outcome and its item is dispatched again. This item builds no part of the pause protocol, and its absence is not a reason to report blocked. A test stops the loop with `--end-runs` and with two fixture runs in flight, and asserts both were ended through that function and that neither spent a retry.
- **c4:** A run that ended because the loop stopped or crashed is not an attempt outcome: it spends no retry, no fail-over and no re-admission, and the item is dispatched again on the next pass however many times that has happened. On 2026-11-15 five items were held as `run-infra-exhausted`, `readmission-exhausted` and `retry-stale` after loop stops used up budgets meant for a failing item. A test stops the loop three times under one item and asserts it is dispatched a fourth time with no authorization.

Amended 2026-11-16, answering the conflict the first run raised. c3 required runs to be paused "as the restart design states", and the tree the run was staged from had nothing to pause them with. The ruling is in c3 above, and these are its reasons:

- **The pause protocol is another item's work, and it has not landed.** At `1660b7b4`, `rg -n -i pause tools/tillr-run` returns nothing, and the `stop` face is absent too: `rg -n 'const faceList' tools/tillr-drain/main.go` prints `why, plan, once, retry, clear-fault, boundary, resume or loop`. The pause request, the `paused` outcome and each adapter's declared kind belong to *Pause a worker between tool calls and resume it where it stopped* in the restart design's [build order](../design/tillr-restart.md#build-order).
- **This item does not wait for it.** c1, c2 and c4 need no pause, and they are what stops a loop stop from spending an item's budgets, which is the damage measured on 2026-11-15. Holding them behind the pause item would leave that damage in place for as long as the pause takes to land.
- **The continuation the loop already has is not a pause.** `resumeAfterWall` in `tools/tillr-drain/drain/loop.go` continues a session once per run, and never when the loop was asked to stop. c3 does not reuse it and does not change its bound.
- **Without a pause, a run ended by `--end-runs` starts over.** That is the cost of this order of work. It is paid only on the explicit flag, and c4 keeps it from spending a retry.
- **Nothing is filed for the upgrade.** If this item lands before the pause item, the function in c3 still sends SIGTERM after the pause exists, and no criterion of any item changes it to a pause request. Filing that work is the owner's step, outside every criterion here.

### Add commands for the recoveries that were done by hand

<!-- tillr: item id="item-drain-recover-commands" paths="tools/tillr-drain/,tools/tillr-cli/,yolo-packs/tillr/,docs/" blocked_by="" -->

- **c1:** `tillr drain acquit --run <id> --why <text>` closes a conviction against a run that an operator says was not the run's doing, keeps the conviction's record, and makes the run's candidate eligible to be judged. It refuses a run that has no conviction.
- **c2:** `tillr drain examine --run <id>` gates, lands and records one candidate that is sitting in its lane, exactly as the loop would, and refuses when a loop holds the lane. A test lands a fixture candidate with it and asserts the records equal the loop's.
- **c3:** `tillr drain set-aside --item <id> --why <text>` moves an unlandable candidate out of its lane to a named ref, frees the lane, and holds the item for rework, in one transaction with the record that says so. A test asserts a following loop start does not stop on the missing candidate.
- **c4:** No recovery in the operating skill needs a script, a direct edit of the store or a `git reset` in a lane. A test fails when the skill's recovery section names any of the three.

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

- **c1:** No row of the waiting face is `unclassified`, has an empty reason, or has a dash for its mover. On 2026-11-15 six items whose runs had been killed and four items no pass had looked at yet showed all three. A test builds a store holding every hold the drain can record and asserts each maps to a class, a reason and a mover.
- **c2:** An item no pass has decided yet reads `waits on the loop · not yet planned`, and an item whose last run was ended by a loop stop reads `waits on the loop · run ended by a stop` with that run as its mover.
- **c3:** A reason the view has no rule for fails the view's test suite by name, so a new hold cannot ship without its line.

### Stop dispatching when the spend ceiling is reached

<!-- tillr: item id="item-drain-spend-ceiling" paths="tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="item-settings-file" -->

- **c1:** The settings file takes `spend.ceiling_usd` and `spend.window_hours`, both off by default. With them set, the loop dispatches nothing new once the recorded cost of runs started in the window reaches the ceiling, holds every ready item with the reason `spend-ceiling`, and resumes by itself when the window has moved on.
- **c2:** Runs in flight are not ended by the ceiling, and a run that reported no cost is counted in a separate line and never as zero.
- **c3:** The waiting face and the loop's start line show the ceiling, the amount spent in the window and when dispatch resumes.
- **c4:** A resumed or retried run that produced no new model output adds nothing to the total. A test asserts a retry of an interrupted run does not inherit the earlier attempt's cost.

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

<!-- tillr: item id="item-policy-move-run-on-quota" paths="tools/tillr-drain/,tools/tillr-run/,yolo-packs/tillr/,docs/" blocked_by="item-run-pause-then-resume,item-policy-quota-probe" -->

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

<!-- tillr: item id="item-drain-express-lanes" paths="tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="item-settings-file" -->

- **c1:** The settings file takes `lanes.express`, a count of lanes that are not part of the lane cap and are offered only to express work. The default is 0. Express work is an item on the design seat that answers review comments, an idea the owner saved ([the observatory's first two actions](../design/observatory-actions.md#53-the-item-that-is-created)), and any item whose block sets `express="true"`.
- **c2:** With every ordinary lane leased, an express item is dispatched on an express lane in the next pass. A test fills two ordinary lanes with runs that do not end, files an express item, and asserts it is dispatched while both are still running.
- **c3:** Ordinary work is never dispatched on an express lane, and an express item that finds no express lane free waits for one or for an ordinary lane, whichever frees first. No running work is stopped to make room.
- **c4:** The waiting face and the lane strip show express lanes apart from the cap: "7 of 10 running, 1 of 2 express".

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

