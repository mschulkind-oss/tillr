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

[What tillr will change for AutoQA](../design/tillr-separable-parts.md#open-questions) carries
eight more, [`OQ-SP1`](../design/tillr-separable-parts.md#OQ-SP1) to
[`OQ-SP8`](../design/tillr-separable-parts.md#OQ-SP8), from 2026-11-16: whether tillr answers
AutoQA's requests with that proposal, and which of its small changes are filed before the drain
proof.

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
  the pages show usage only (section 6.6 of the observatory redesign). Picking it up
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

<!-- tillr: item id="item-drain-spend-ceiling" paths="tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" -->

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

<!-- tillr: item id="item-drain-express-lanes" paths="tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" -->

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

### Hold an item that has no document block

An item can reach the queue without a block in any tracked document, because the item tool's
apply face writes the store directly. The loop then treats it like any other item, and its worker
is sent to read a section that does not exist. On 2026-11-16 that happened to
`item-agent-default-bmad-superpowers-skills`: it had been in the store alone since 2026-11-15, the
loop dispatched it, and its worker reported blocked after 34 seconds. The item tool's sync face
already counts these items as `unshared_open`, and on that day `tillr-item sync --dry-run` printed
`unshared_open: 3`, but nothing acts on the count. The loop should hold such an item with a reason
a person can read, and the count should appear where the operator already looks.

<!-- tillr: item id="item-sync-hold-unshared-items" paths="tools/tillr-drain/,tools/tillr-item/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** The planner does not dispatch an open item that no document block declares. It holds the item with the reason `no-document-block`, which names the document the item's body reference points to. A test stores an item with no block and a ready item with one, plans both, and asserts the first is held with that reason and the second is dispatched.
- **c2:** The hold lifts by itself in the pass after a sync imports a block for the item, with no command from the operator. A test adds the block, syncs, plans again, and asserts the item is dispatched.
- **c3:** The waiting face shows the held item as waiting on a person, with the reason `no-document-block` and the command that fixes it (add the block, or close the item). The loop's start line and the status face print the `unshared_open` count, so it is no longer visible only in a sync report.
- **c4:** An item that is closed or landed is never counted as unshared and never held for this reason. A test asserts both.

**Agent details:**

- **Ref:** `docs/design/queue-persistence-ruling.md` names the counter and its intent; the loop's start-up warning is the existing surface.
- **Where:** `tools/tillr-item/item/sync.go` (the `unshared_open` count), the planner in `tools/tillr-drain/drain/plan.go`, the waiting face in `tools/tillr-view/waiting.go`.
- **Evidence:** 2026-11-16, `tillr-item sync --dry-run --store .tillr/state.db` printed `unshared_open: 3`; run `run-item-agent-default-bmad-superpowers-skills-a1` ended `RESULT: blocked` after 34 s.

### Add the spend of killed runs to the record

When a loop exits on a fault, the workers it launched are killed, and a killed worker never writes
the report that carries its cost. The cost of that run is then recorded as unknown, so the total
the queue shows is lower than what was spent. The error grows with the number of faults: on
2026-11-16 the drain recorded `item-drain-recover-commands` at $10.70 over three runs and
`item-sync-design-without-item` at $0.67 over seven runs, and the killed runs in both recorded
nothing. A second effect follows. A new loop that settles a run an earlier loop started counts
that run's spend in its own pass record, so a pass looks more expensive than the work it did. The
record should have a number for every run however it ended, and should say which spend a loop
inherited.

<!-- tillr: item id="item-drain-record-killed-run-spend" paths="tools/tillr-drain/,tools/tillr-run/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** A run that ends because its loop stopped records the spend its backend can report for the work done so far, or records that the backend could not report it, and never leaves the field empty. A test kills a fixture worker that has produced usage output and asserts the recorded spend equals that output.
- **c2:** When no spend can be read for a killed run, the record says `unreported` for that run, and every total the views print states how many runs were unreported. A test asserts a total over one reported and one unreported run reads as a lower bound.
- **c3:** A pass record separates `spend started in this loop` from `spend carried over from an earlier loop's runs settled here`. A test settles a run started by a previous loop and asserts its cost appears only on the carried-over line.
- **c4:** The spend ceiling counts a killed run's recorded spend, so a loop that keeps being killed cannot spend past the ceiling unseen.

**Agent details:**

- **Ref:** `item-drain-spend-ceiling` in this document is the consumer of these totals.
- **Where:** the usage capture and run-finish records in `tools/tillr-drain/drain/` and `tools/tillr-run/`; the pass record and the spend lines in `tools/tillr-view/`.
- **Evidence:** 2026-11-16, `tillr view events --since 0`: `item-drain-recover-commands` showed $10.16 and $0.54 over three runs, the third killed at 08:49:05 UTC by loop 459 and recording nothing; `item-sync-design-without-item` showed $0.14 and $0.53 over seven runs, five of them killed.

### Hold an item whose runs keep being orphaned

A run that a loop stop orphans spends no retry and no re-admission, by design: the worker did not
fail, the loop did. The cost of that rule is that an item whose runs are killed again and again is
sent out again every time, without limit. On 2026-11-16 `item-sync-design-without-item` had five
orphaned attempts, each one a worker started and killed by a loop that was exiting on a fault. The
item is not the cause, and re-dispatching it fixes nothing until the fault is fixed. A person needs
to see the count, and after a stated number of orphaned attempts the item should wait for a person,
with the faults that killed its runs named, instead of being sent out again.

<!-- tillr: item id="item-drain-hold-repeatedly-orphaned" paths="tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** The waiting face and the item's record show the number of orphaned attempts per item, and the loop's settings file takes `orphan.limit`, defaulting to three. A test asserts the count is shown for an item with two orphaned runs and for an item with none.
- **c2:** When an item's orphaned attempts reach the limit, the planner holds it with the reason `orphan-limit`, which names each stopping loop's recorded fault, for example `fault: store: database is locked` or `gate-harness-error`. A test with three orphaned runs asserts the hold, the reason text and that nothing is dispatched.
- **c3:** `tillr drain retry` for that item clears the hold and starts a new count, and a run that ends on its own merits (a verdict, a refusal or a report) also starts a new count. A test asserts both.
- **c4:** A held item is not hidden from the operator: the loop's start line lists items held for `orphan-limit` with their counts.

**Agent details:**

- **Ref:** the interrupted-run rule in `tools/tillr-drain/README.md`; the loop stop records in `.tillr/loop/` carry each stop's fault text.
- **Where:** `AttemptInterrupted` handling in `tools/tillr-drain/drain/plan.go`, the `drain.orphaned` events, and the waiting face in `tools/tillr-view/waiting.go`.
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

<!-- tillr: item id="item-drain-design-seat-quota-failover" paths="tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** When the planner dispatches a conflict's design run, it skips every design seat whose quota window is recorded as shut and sends the run to the first one that is open. A test with two design seats, the first shut, asserts the run goes to the second.
- **c2:** When every design seat's window is shut, the item is held as `backend-window`, the reason a build item gets when its seat is shut, naming the seat and the reset time, and no design run starts. A test asserts the hold and that nothing is dispatched.
- **c3:** A design run that ends because its backend refused it for a usage limit records the seat's window as shut and returns its conflict to `raised`, so the next pass can send it to another seat. A test feeds a fixture run that exits on a weekly-limit message and asserts the conflict is `raised` and the window is shut.
- **c4:** A conflict whose design run ended any other way without an answer keeps today's behavior. A test asserts that a design run that ends normally with no block change still moves the conflict to the owner.

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

<!-- tillr: item id="item-drain-answered-conflict-beats-stale-retry" paths="tools/tillr-drain/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** An item whose latest conflict is `answered` is dispatched to its build even when an operator's retry for one of its earlier runs was used by a run before the one that raised the conflict. A test stores a retry used by run a7, a conflict raised by a7's conflict-fix run and answered by a design run, and asserts the plan dispatches rather than holding `retry-stale`.
- **c2:** A retry that names a run older than the latest, with no answered conflict after it, is still refused as `retry-stale`. A test asserts it.
- **c3:** `tillr drain why` names the answered conflict as the reason the item moves, so an operator can see why the retry no longer matters.

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

<!-- tillr: item id="item-run-commit-before-ending" paths="tools/tillr-run/,tools/tillr-drain/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** A build run whose worker exits while its lane has uncommitted changes inside the fence is not settled as a no-commit refusal until the worker has been given a chance to commit them. A test with a fixture worker that edits a fenced file and exits without committing asserts the run does not end as `no-op` on that exit.
- **c2:** When the worker then commits, the run is settled from that commit like any other candidate. A test asserts the candidate is the commit made after the first exit.
- **c3:** When the worker still does not commit, the run ends as a refusal whose record names the uncommitted paths, so an operator can tell lost work from a run that did nothing. A test asserts the paths appear in the record.
- **c4:** The design note for this item records which of the options in this section was chosen and why.

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

<!-- tillr: item id="item-drain-rolling-watermark-proves-what-ran" paths="tools/tillr-drain/,tools/fork-check.py,tests/fork-check.test.py,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** A rolling run with no watermark runs every check in the rolling tier against the trunk tip, whatever baseline the repo gate last verified. A test seeds a gate baseline equal to the tip, runs the first rolling run, and asserts no rolling check is skipped as unchanged.
- **c2:** A green rolling run that skipped checks advances the watermark only over what those checks cover, or records the skipped checks with the watermark, so a reader can tell a full audit from a partial one. A test asserts the record names the skipped checks.
- **c3:** The loop's rolling line states how many checks ran, how many were answered from cache and how many were skipped. A test asserts the three counts on a fixture run.
- **c4:** The drain's README states what a rolling run with no watermark audits, in one sentence a reader can check against the record.

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

<!-- tillr: item id="item-conflict-transition-not-duplicate" paths="tools/tillr-item/,tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** Recording a conflict transition whose status, design run and answer equal the conflict's last recorded transition writes no event. A test records the same transition twice and asserts one event.
- **c2:** The event list and the waiting face show a conflict's later records as transitions of the first, naming the status change and the design run, not as a second conflict. A test renders a raised conflict and its move to `awaiting` and asserts one conflict with two transitions.
- **c3:** The item tool's documentation states that a conflict is recorded once per transition.

**Agent details:**

- **Ref:** `ConflictKind` and `RecordConflict` in `tools/tillr-item/item/conflict.go`, whose comment already says each routing change is recorded.
- **Where:** `tools/tillr-item/item/conflict.go`, the drain's conflict routing in `tools/tillr-drain/drain/blocked.go`, and the events and waiting faces in `tools/tillr-view/`.
- **Evidence:** 2026-11-17, `tillr view events --since 4243`: events 4269 (02:06:39.497 UTC) and 4275 (02:06:45.327 UTC) are both `item.conflict` with id `conflict-run-item-agent-default-bmad-superpowers-skills-a1`; the first has status `raised`, the second `awaiting` with design run `run-item-agent-default-bmad-superpowers-skills-a2`, and every other field is equal.

### Add the live loop's session and leases to the running view

The running view is where an operator looks to see whether a loop is alive and what it is doing.
While a loop was running on 2026-11-17, the view showed the stop record of the loop before it, an
operator stop from 00:57 UTC marked as stale, and printed "lease not recorded" for the run that
loop had just started. The operator watching had to read the process list to learn the loop was
alive. A view that shows an old stop while a loop runs is worse than no view, because it tells
the person to start a loop that is already there. The running view should show the live loop's
session and the leases of its runs.

<!-- tillr: item id="item-view-running-shows-live-loop" paths="tools/tillr-view/,tools/tillr-drain/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** While a loop's session is live, the running view shows that session (its start and its last heartbeat) and not an older stop record. A test stores a stop record and a newer live session and asserts the view shows the session.
- **c2:** A run the live loop dispatched shows its lane lease and the time left on it. A test asserts a leased run never shows `lease not recorded`.
- **c3:** The stale stop note appears only when no live session exists. A test asserts both cases, and the web view agrees with the text view.

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

<!-- tillr: item id="item-drain-check-step-program-flags" paths="tools/tillr-drain/,tools/tillr-loop/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** `tillr drain loop --check`, and the loop's own start-up, hand each configured gate, fixer gate, land and verdict program the argument list the loop would send, with every optional flag present, in a mode that judges nothing. A program that refuses it stops the start with a usage fault naming the program and the flag.
- **c2:** The mode that judges nothing is defined once in the drain's README as part of the step-program contract, and the tillr-loop adapter implements it. A test starts a loop with an adapter that lacks one flag and asserts the start is refused before any run is dispatched.
- **c3:** A step program that does not implement the mode is reported once at start-up as unchecked, and the loop still starts. A test asserts the warning.
- **c4:** A step program's usage refusal at run time is reported as a configuration fault naming the program, not as an external failure. A test asserts the stop's class.

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

<!-- tillr: item id="item-land-across-owner-edits" paths="tools/tillr-land/,tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** Paths the landing does not touch: a landing never delays on them, never stashes, and leaves the file completely alone, with the same bytes, the same inode and the same modification time, so an editor never reports that it changed underneath it. This holds whether the file is tracked and modified, staged, untracked, or unmodified but open. Each path's index entry (staged versus unstaged) is unchanged, the landing completes, and the checkout update changes only the paths the landing changed. A test records bytes, inode and modification time before a landing over such files and asserts equality after, which is not an elapsed-time assertion. The existing test covers only the content of an untracked file.
- **c2:** Paths the landing touches, when c4 predicts a clean restore: only the owner's uncommitted content on those paths is set aside, the trunk moves, and the content is restored, without waiting for the owner. Afterwards the file holds what `git pull --autostash` would have left, the landed change plus the owner's edits, with staged and unstaged changes keeping their split. Each such file is written once, as a single replace of its final content; if the sequence unavoidably passes through an intermediate on-disk state, the lander README says so, the state stays inside the lock, and the path count stays minimal. A path whose final content equals what was on disk is not rewritten and keeps its inode and modification time. A test lands over a staged and an unstaged edit and asserts all of this, and that an unrelated dirty path was not set aside.
- **c3:** The window in which the owner's edits are absent is structurally minimal. Set-aside, trunk move and restore run back to back inside the landing's existing lock, after the gate verdict, the squash, the observed-trunk check and the prediction are done, with no gate, network call, store write or wait between them, and no Git work is added inside a SQLite write transaction. The set-aside lives in a ref under `refs/tillr/`, never `refs/stash`, so the drain's run-boundary check, which already excuses the lander's `refs/tillr/` markers, does not convict it and the rule that a `refs/stash` in the target stops a tick is not tripped. A crash between set-aside and restore leaves the owner's content recoverable from that ref, and the next landing restores it or reports it. Tests record the order of Git calls, run the boundary check across a landing, and kill the lander between the two steps.
- **c4:** Prediction before touching anything. Before setting content aside, the lander works out whether restoring the owner's content on top of the landed result would conflict. The computation changes nothing the owner can see: no working-tree, index or ref write, so a scratch index, `git merge-tree` or `git merge-file -p` on the blobs are acceptable and the builder chooses. The owner's content is read and the trunk moved under the same lock, so the answer cannot go stale. If the restore fails despite a clean prediction, from an I/O error or a race the lock should make impossible, the lander puts the owner's content back exactly as it was, records a fault, and writes no conflict markers. A test forces that path and asserts byte-identical content and the recorded fault.
- **c5:** A predicted conflict is the only state in which a landing pauses because of the owner's working tree, and in that state nothing on disk changes at all, which is the least surprising outcome for it: no conflict, nothing set aside, no markers. The gated candidate is retained and the item waits with a reason naming the conflicting paths and the advice "commit, restore, or change your edit on <paths>; the landing proceeds as soon as it no longer conflicts". The wait re-evaluates when the owner's content or the trunk changes, and other items that do not conflict keep landing meanwhile. A main checkout in the middle of a merge, rebase or cherry-pick also pauses, with a reason naming that state. The hold keeps the name `trunk-path-dirty` unless the builder renames it because the name now misleads; a rename updates every enumerator of drain reasons, including the waiting rules in the view and `TestWaitingEveryDrainReasonHasARule`, the `tillr-operating` skill and the READMEs. Tests assert an unchanged tree (content, inode, modification time), the reason and advice, a re-evaluation after the owner edits, and a second item landing meanwhile.
- **c6:** The prediction cases are decided and stated in the lander README, with a test for each: staged and unstaged overlapping changes keep their split; deleted or renamed paths on either side; mode changes; a binary file with any overlap is a conflict unless identical; an owner's untracked file on a path the landing adds is a conflict unless identical. The drain README, the lander README, the view's waiting rule and the `tillr-operating` skill say what is true, and an rg of those four for the old statement that any dirty landed path holds the landing returns nothing.
- **c7:** A landing that touches a file the owner has open with nothing saved but uncommitted updates it on disk as any `git pull` would; the lander does nothing clever and adds no hold, and the editor's own "file changed on disk" notice is the familiar, least surprising signal. Editor side files are never touched and never count as the owner's content on a landed path: Vim swap and backup files (`.<name>.swp`, `.swo`, `<name>~`, `4913` probes), Emacs lock and autosave files (`.#<name>`, `#<name>#`), and anything the repository's ignore rules exclude. They are not set aside and cause no predicted conflict unless the landing itself adds that exact path. Tests land a change on a clean tracked file and beside each kind of side file, and assert the landed content, the side files' bytes, inodes and modification times, and no pause.
- **c8:** The owner pulling, committing or rebasing forward on `main` while candidates are gated or in flight needs no drain intervention: a forward move is replayed onto, and a rewritten history is refused with `trunk-rewritten`. The lander README states both. A test with an owner commit between gate and landing asserts the landing and the unchanged verdict; the rewritten case is already pinned.

**Agent details:**

- **Ref:** `docs/design/board-state-machine.md` section 9 (D33, "the comparison-and-swap autostashes"); `docs/design/board-state-machine-plan.md` P10 (fences: never drop the stash on a failed apply, never stash during the gate); `docs/design/landing-principles.md`; the `refs/stash` rule in `docs/design/tillr-day2.md` and the boundary table in `tools/tillr-drain/README.md`.
- **Where:** `Trunk.Move` and `Trunk.UncommittedPaths` in `tools/tillr-land/land/trunk.go`; the dirty refusal in `tools/tillr-land/`; `tools/tillr-drain/drain/dirty_hold.go`; `trunk-path-dirty` in `tools/tillr-view/waiting.go`; the drain README section "Owner edits and waiting landings"; `yolo-packs/tillr/skills/tillr-operating/`.
- **Question for the builder, answered in its report and not a criterion:** whether the presence of an editor swap or lock file for a landed path, a sign the file is open and may hold unsaved changes, should appear in the landing's log line as information for the owner. It must not pause the landing, because the owner ruled that a predicted conflict is the only pause.
- **Evidence:** 2026-11-17, `Trunk.Move` runs `git -c merge.autoStash=false merge --ff-only` in the main checkout, so git itself refuses when the move would overwrite uncommitted content and otherwise advances only the changed paths; git's fast-forward keeps staged entries on untouched paths, but no test pins it. `TestALandingLeavesUncommittedContentOnAnUntouchedPathAlone` (untracked file only) and `TestALandingIsRefusedWhenTheTrunkWorktreeHoldsUncommittedContentOnALandedPath` in `tools/tillr-land/main_test.go`, and `TestUnrelatedDirtyPathDoesNotDelayLanding` in `tools/tillr-drain/drain/dirty_hold_test.go`, pin the two cases. `rg -n -i 'MERGE_HEAD|rebase-merge' tools/tillr-land tools/tillr-drain --glob '*.go'` returns nothing, so no code names an in-progress merge or rebase. `TestLockedLandingAcceptsFastForwardBeyondObservedTip` in `tools/tillr-land/trunk_history_test.go` pins the forward move, and `TestTheDrainLandsThroughTheRealAdapterAndTheRealLander` in `tools/tillr-drain/drain/step_argv_contract_test.go` pins `trunk-rewritten`. `rg -n -i dirty yolo-packs/tillr/skills/tillr-operating/` returns nothing (2026-11-17).
