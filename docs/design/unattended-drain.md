---
title: "Why the drain could not run one night alone, and the eight rules that let it"
status: in-review
stage: DESIGN
updated: 2026-11-21
tags: [tillr, drain, gate, landing, lanes, incident, design]
summary: "The night of 2026-11-21 produced 41 separate incidents and no proved-green trunk for nine hours. They reduce to eight structural gaps. Each gets one rule, a mechanism that enforces it, and a permanent scenario that fails if the incident returns."
next: "The owner rules OQ-UD1 to OQ-UD4. Rules 2, 3, 6 and 7 do not wait on them and can be planned now."
depends-on:
  - landing-principles.md
  - trunk-fixer-gate.md
  - tillr-watch.md
vantage:
  status-chip: true
---

# Why the drain could not run one night alone, and the eight rules that let it

**Status:** 2026-11-21. Nothing in this document is built. The incident record was read from the
loop's log, the store's faces, the sampler and `git log` on the morning of 2026-11-21, with the
trunk at `687b1a2c97`.

> **In short.** The night did not fail forty-one times. It failed eight ways. The drain lets a
> change to its own code land before that code's tests run, and then depends on a repair path,
> an operator and a subscription that were each able to stop. Close those eight gaps by
> mechanism and every incident below loses the condition it needs.

**Why it matters.** The owner asked for ten lanes by morning. The drain ended the night on two,
with a red trunk, having spent 49 of its 53 landings repairing itself. It is the second night in
three that a red trunk went unrepaired
([the 2026-11-19 record](../tillr/unproved-trunk-2026-11-19.md)).

**The shape.** Eight rules, each with one enforcing mechanism, and one scenario suite that holds
a named scenario for every incident. A drain build runs the live queue only after that suite
passes.

**Cost.** A change to the drain's own code waits for its own test suite before landing, which is
minutes, not the ten seconds every other change gets. Lanes stop being a fixed count.

**Start at [Section 5](#5-the-eight-gaps)**: the diagnosis. The rules fall out of it.

**Needs your ruling:** [OQ-UD1](#OQ-UD1), [OQ-UD2](#OQ-UD2), [OQ-UD3](#OQ-UD3),
[OQ-UD4](#OQ-UD4).

**Reads with:** [`landing-principles.md`](./landing-principles.md) (the settled rulings these
rules extend), [`trunk-fixer-gate.md`](./trunk-fixer-gate.md) (the fixer's gate, built this
morning), [`tillr-watch.md`](./tillr-watch.md) (the alerting design rule 8 needs),
[`nonconverging-items.md`](../tillr/nonconverging-items.md) (items that do not finish). No
implementation plan is seeded yet.

---

## 1. Scope

This covers the drain's run in this repository from 12:09 AM ET on 2026-11-21, when the owner
left it to run unattended, to 10:30 AM ET the same morning. All times are Eastern.

It does not cover:

- the evening before, which the owner was present for;
- the design of the alerting utility, which [`tillr-watch.md`](./tillr-watch.md) owns;
- the refused-push repair's content, which [`refused-push-slices.md`](../tillr/refused-push-slices.md) owns;
- file-by-file build steps, which belong to an implementation plan.

## 2. Terms

Each term is used below in exactly this sense.

- **Drain.** The `tillr-drain` loop: one long-running process that takes items from the queue,
  starts workers on them, gates their results and lands them.
- **Item.** One piece of queued work, with words, pass conditions and a fence.
- **Fence.** The list of paths an item's worker may change.
- **Lane.** A worktree the drain keeps for running one item at a time. The drain ran two
  ordinary lanes and two express lanes all night. An express lane takes only items filed as
  urgent.
- **Worker.** One agent process running one step of one item in a lane.
- **Trunk.** The `main` branch.
- **Lane gate.** The checks a candidate must pass before it lands.
- **Trunk proof.** The full check program, run on the trunk after landings. The repository's
  documents also call it the rolling tier.
- **Green.** The newest trunk commit the trunk proof has passed.
- **Fixer.** An item the drain files by itself to repair a red trunk.
- **Hold.** A state in which an item waits. Every hold names a **mover**: who or what ends it.
- **Operator.** The foreground agent session that watches the drain. Last night it checked in
  every 15 minutes.
- **Designer.** The seat that writes designs, plans and reviews. Last night it ran on the
  owner's Claude subscription, and so did the operator.
- **Fork.** A patched copy of an upstream tool, kept as a series of patches. The one that
  mattered last night is the fork of the `pi` coding agent.
- **Roll.** The loop replacing itself with a newly built drain binary after a landing changes
  the drain's code.
- **Scenario** *(coined here)*. A scripted run of a drain build against a throwaway repository
  and store, with one fault arranged, that asserts what the drain does. It is not a unit test
  of one function and it is not a run of the live queue.

## 3. The night in numbers

| Measure | Value | How it was read |
| :--- | :--- | :--- |
| Lanes asked for by morning | 10 | Owner, 12:09 AM |
| Lanes at 10:30 AM | 2 | Loop's `--lanes` argument |
| Time with no proved-green trunk | 12:53 AM to about 9:57 AM, then red again | `loop: green=` lines |
| Landings named as the first red commit | 6 | `first red` lines, unique commits |
| Landings between 12:53 AM and 10:20 AM | 53, of which 49 changed the drain itself | `git log --since` |
| Loop exits | 5, all between 5:17 and 5:36 AM | Keeper's output |
| Time with no operator and no designer | 5 h 42 min (12:48 to 4:10 AM, 6:38 to 9:10 AM) | Backend alarm lines, check-in gaps |
| Releases the operator made by hand | more than 30 | Operator's record of each check-in |
| Windows of 30 minutes that met the lane rule | 0 | Sampler report at each check-in |
| Attempts at the `pi` rebase | 13, none delivered | Run names |
| Spend in the 24 hours to 10:01 AM | $843.78 over 846 runs | Loop's spend line |

## 4. Every incident

Each row is one thing that went wrong. **State** is what the tree holds at `687b1a2c97`: a
commit that fixed it, **queued** if a fix is filed and not landed, or **open** if nothing
addresses it. A fix in this column removes that one cause. The rule in
[Section 7](#7-every-incident-and-what-stops-it) is what removes the class.

### 4.1 A red trunk that stayed red

| # | When | What happened | Cause | State |
| :--- | :--- | :--- | :--- | :--- |
| N-01 | 12:53 AM | Trunk red on the drain's suite | `3537272f7a` and `67eb290316` landed through a lane gate that did not run that suite | open |
| N-02 | 1:58 AM | The fixer wrote the repair and its gate refused it | The fixer's gate ran every check since green, including a fork check an upstream release had turned red | `cc31e10d8b` |
| N-03 | 1:58 to 4:24 AM | The written repair sat unlanded | Nobody was awake to carry it (N-20) | open |
| N-04 | 4:47 AM | Two failing tests named by a finished proof, no repair filed | Each landing restarted the proof and threw away its finished red verdict | `1f8652f7c` |
| N-05 | 5:14 AM | The add-a-path command broke a view test | `a0b415fc3d` landed without that suite; reverted by the loop 27 minutes later | reverted, `9702855aee` |
| N-06 | 5:28 AM | The start coordinator broke a comment test | `c445066181` landed without the drain suite; never reverted | repaired by hand, `80d7ceb14a` |
| N-07 | 6:10 AM | Releasing blocked items broke a fold test | `9298e19241` landed without the drain suite; reverted 43 minutes later, then landed again | reverted, `1d83f5a28f` |
| N-08 | 6:16 AM | Two more drain tests red in two proofs, no repair filed | As N-04 | `1f8652f7c` |
| N-09 | 9:14 AM | Two fixers stopped to ask the owner a question | The question was the fork check of N-02 in other words | `cc31e10d8b` |
| N-10 | 9:41 AM | A third fixer refused by its own gate | As N-02 | `cc31e10d8b` |
| N-11 | 9:45 AM | The fix for N-02 turned the trunk red | `cc31e10d8b` left behind a gate self-test that pinned what it removed; the lane gate did not run that self-test | queued |
| N-12 | 9:51 AM | The no-failover change left three drain tests red | `207caae0ef` rewrote more than twenty files in the drain's package and passed its lane gate in 62 seconds; the plan for that gate lists the drain's suite as owed after landing | queued |
| N-13 | 10:10 AM | The loop could not revert N-11 | "trunk advanced while the revert was being checked" | open |
| N-14 | 10:10 AM | The loop said it was filing a fixer and no fixer appeared | Not established | open |

### 4.2 Lanes empty while work waited

| # | When | What happened | Cause | State |
| :--- | :--- | :--- | :--- | :--- |
| N-15 | all night | 22 to 42 seconds between a worker ending and its gate starting | The gate start waited for the loop's next pass | part landed with the instant-start slices |
| N-16 | 12:48 to 4:10 AM | 12 to 14 items that needed no designer did not start | Four items waiting for the closed designer backend each kept a lane | landed (lane-starvation fix) |
| N-17 | 6:24 AM | Lane empty 3 minutes | The planner offered the lane nine times to an item that could not take it, because another item held the `pi` fork | `9cfbb21d6` |
| N-18 | 9:14 to 9:33 AM | Lane empty 19 minutes with five items waiting | A design step refused for quota and run again collided with its own earlier decision record; the faulted item kept the lane | `05741800d5` |
| N-19 | 9:14 to 9:33 AM | Clearing the fault, blocking the item, setting it aside and closing it all failed to free the lane | A blocked item with no candidate keeps its lane; close refused any item with a landing | `746f87e2b3` for close; lane part open |
| N-20 | 9:38 to 9:49 AM | No new start on any lane | One failed staging attempt set a back-off flag that cleared only when no worker was running | `f9d0c9d14b` |
| N-21 | 9:59 to 10:02 AM | Both ordinary lanes empty, six items waiting | The roll held every start while it waited for one gate | queued |
| N-22 | 6:16 to 9:14 AM | Nothing startable in the queue | Every open item was held on the operator or on the `pi` rebase; 22 ready slices were not yet filed | open |

### 4.3 The loop itself stopped

| # | When | What happened | Cause | State |
| :--- | :--- | :--- | :--- | :--- |
| N-23 | 5:17 to 5:36 AM | Loop exited five times; lanes idle up to 20 minutes | Cleaning up a fork item wrote the fork's commit id as a ref in the main repository, where that commit does not exist, and treated the failure as fatal | `2a4becd0d` |
| N-24 | 5:15 AM | Preserving the rebase's work failed, and on one path ended the loop | A list of 1,399 files passed as one command argument | `768313b48` |
| N-25 | 12:08 AM | No dispatch for three and a half minutes | Operator restarted the loop for the wrong lane count; the stop left the process alive and idle | open |

### 4.4 One upstream release, thirteen attempts

Upstream released `pi` v1.6.0. The fix is one item: replay our patches onto it.

| # | When | What happened | Cause | State |
| :--- | :--- | :--- | :--- | :--- |
| N-26 | 10 PM to 4:16 AM | The rebase item sat blocked for six hours | Its blocker had landed at 9:56 PM; a blocked item was not released when its blocker landed, and no command released it | `5709b4fc2` |
| N-27 | 4:43 AM | Next item could not stage on its lane | The rebase worker ended with an empty last turn mid-rebase and left the lane's fork checkout dirty | landed (checkout recovery) |
| N-28 | 4:16 AM | The item was filed with a lane checkout path as its fence | Operator error; the repository guide forbids it | open |
| N-29 | 5:15 to 9:14 AM | Five verifier refusals on a correct candidate | Each asked for a change to the worker's report or to how tests were listed, not to the candidate | open |
| N-30 | 9:14 AM | Gate failed with HTTP 401 | The fork gate ran fork tests with the normal home directory, so tests reached live providers | `72e3fc731d` |
| N-31 | 10:09 AM | Gate failed on one upstream test with a 5-second limit | The fork gate picks packages against the old fork tip, so a rebase runs every package upstream changed, including ones our patches never touch | queued |
| N-32 | 9:38 AM | Staging refused the lane | The lane's fork checkout held the same item's earlier attempt | queued |

### 4.5 Work that waited for a person

| # | When | What happened | Cause | State |
| :--- | :--- | :--- | :--- | :--- |
| N-33 | all night | Items stopped at their fence at least nine times and waited for a check-in | A worker that needs one more path stops; only the operator widens a fence; the pack copy of a changed file is a second path nobody remembers | `1e87beb33` adds the command; the wait is open |
| N-34 | 4:58, 6:16 AM | Items held after three verifier refusals until the operator read them | The hold's mover is the operator | open |
| N-35 | 10:15 AM | A worker that stopped correctly for a red trunk was held as a fence escape | The loop judged its uncommitted work before reading the reason it stopped | queued |
| N-36 | 4:14 to 9:14 AM | The fixer-gate design landed and its build was not filed for five hours | Filing a build from a landed design is a hand step | open |
| N-37 | since 2026-11-20 | Fourteen open questions, five of them the alerting design's, reached no roadmap and no report | Nothing fails when a question is unrouted | open |
| N-38 | all night | The refused-push repair ran 16 attempts and the rebase 13 | One item too large to verify; see [`nonconverging-items.md`](../tillr/nonconverging-items.md) | split into 22 slices, `793e165b84` |

### 4.6 Nobody watching

| # | When | What happened | Cause | State |
| :--- | :--- | :--- | :--- | :--- |
| N-39 | 12:48 to 4:10 AM, 6:38 to 9:10 AM | No check-in ran and no design step ran | The operator and the designers share one subscription, and its five-hour limit was reached twice | open |
| N-40 | all night | The backend alarm named the outage and woke nobody | The alarm is a line in a log and a banner on a page | `a07ecb4e8f` builds the alarm; delivery open |
| N-41 | all night | Operator errors: two items filed with the cause backwards, four record entries with a clock time that was not read, a hold promised to the owner and not made, and no statement to the owner that the trunk could not go green while N-02 stood | The operator filed one defect per symptom and reported each as progress | open |

## 5. The eight gaps

Every incident above needed at least one of these to be true.

1. **The drain's own code lands before its own tests run.** The lane gate is built to finish in
   seconds, so Go test suites run after landing. That is right for a change to a configuration file. For
   the drain's package it produced six red landings in ten hours (N-01, N-05, N-06, N-07, N-11,
   N-12), each found only after the next items had built on it.
2. **Repairing the trunk had conditions that a red trunk breaks.** The revert is abandoned when
   the trunk moves, and the trunk always moves (N-13). The fixer's gate read checks the fixer
   could not affect (N-02, N-09, N-10). A finished red verdict was discarded by the next landing
   (N-04, N-08).
3. **Checks whose answer comes from outside the tree could refuse a candidate.** An upstream
   release (N-02), a live provider's credentials (N-30), a wall-clock limit in a test we do not
   own (N-31) and an old fork tip (N-31) each refused work that was correct.
4. **A lane is scarce and an item can keep one while nothing runs.** A closed backend (N-16), a
   fault (N-18), a block (N-19), a shared flag (N-20) and a roll (N-21) each emptied a lane that
   work was waiting for.
5. **One item's fault could stop the loop or every other item.** A bad ref and a long argument
   list ended the process (N-23, N-24). One staging failure stopped all starts (N-20).
6. **The drain's fixes are tried for the first time on the live queue.** Every row in
   [Section 4](#4-every-incident) was discovered by the queue stopping. Nothing rehearses a new
   drain build before it runs the queue, and the roll that installs it costs lane time (N-21).
7. **Mechanical remedies wait for a person.** More than thirty times the operator did something
   the drain had every fact to do itself: add a path the worker named, release an item whose
   blocker had landed, read three refusals and retry, repoint a dependency (N-26, N-33, N-34,
   N-35, N-36).
8. **Noticing depends on someone being awake, and one account going quiet removes everyone.**
   The operator polls every 15 minutes, on the same subscription as the designers (N-39, N-40).
   Nothing tells the owner that the trunk has been red for an hour.

## 6. The eight rules

Each rule states the behavior, what happens when it fails, and the evidence an operator reads.
The first tier that can be reached is used: a structure that makes the incident impossible,
else a signal where it happens, else a written procedure.

### 6.1 Rule 1: the drain's code proves its own suites before it lands

A candidate that changes a file inside a Go module under `tools/tillr*` runs that module's whole
suite, and the gate's own self-tests when it changes `tools/gate/`, in its lane gate. Every other
check keeps its place. Changes outside those paths keep the short gate.

- **Trigger.** The candidate's changed paths, read from the same record the gate already writes.
- **Cost, measured.** The drain's suite took 7 min 35 s in the proof's replay on 2026-11-21. The
  gate runs outside the landing lock ([principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once)),
  so one candidate's suite does not hold another candidate's landing.
- **Failure.** A red suite is a refusal like any other check and goes back to the worker.
- **What it does not cover.** Two candidates that each pass alone and fail together. The trunk
  proof stays for that, and rule 2 repairs it.
- **Evidence.** The gate's plan lists the suite in the landing tier for such a candidate. The
  count of first-red landings whose failing check sits in a module the landing changed is zero.

This is [OQ-UD1](#OQ-UD1), because it sets aside the ten-second target for this one class of
change.

### 6.2 Rule 2: a red trunk is repaired by a step that cannot be outrun

1. **The revert is a landing.** When the proof names a first red landing, the loop builds its
   revert and replays the one failing check on it. If the trunk moves meanwhile, the revert is
   applied onto the new tip and landed without a second check, exactly as
   [principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once) treats any candidate.
   A moved trunk is never a reason to abandon a revert.
2. **A revert that conflicts files a fixer**, because a later landing built on the red one. The
   fixer is gated as [`trunk-fixer-gate.md`](./trunk-fixer-gate.md) decides: its own repair and
   the check it was filed for.
3. **Every finished red verdict produces a repair** (built: `1f8652f7c`).
4. **A log line that says a fixer was filed names the item**, and the line is not written when
   no item was created; the reason is written instead.
5. **Every first-red landing records which gate tier held the failing check.** That record is
   the measurement rule 1 is judged by.

- **Concurrency.** One revert or fixer per failing check at a time; a second red on another
  check gets its own.
- **Evidence.** Time from a first-red verdict to a landed revert or a started fixer, per
  incident, on the observatory's trunk page.

### 6.3 Rule 3: only the candidate's tree can refuse a candidate

A check whose result depends on something outside the tree it judges cannot fail a candidate. It
reports, and its report becomes work.

| Outside input | What the check does instead of refusing |
| :--- | :--- |
| An upstream release moved | Files one rebase item per fork and release, as `D-FG3` in [`trunk-fixer-gate.md`](./trunk-fixer-gate.md#decision-ledger) decides |
| A test needs a live credential | Runs without credentials (built: `72e3fc731d`); a test that then fails is recorded as needing one |
| An upstream test fails at upstream's own tag | Recorded against upstream, with the tag; it does not judge our patches |
| A wall-clock limit inside a suite we do not own | As the row above |

**The fork gate judges the patches.** For a fork candidate, the packages tested are those that
differ between the upstream base and the fork head. For a rebase the upstream base is the new
tag, so packages only upstream changed are not run.

- **Failure.** A check that cannot say whether its input is outside the tree says so and is
  treated as not judged ([principle 8](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)).
- **Evidence.** Each check in the registry declares its outside inputs; a check with one never
  appears as the failing check of a candidate's refusal.

### 6.4 Rule 4: workers are limited, worktrees are not

The number the owner sets is how many workers run at once. An item that is in flight has a
worktree of its own for as long as it needs one, and a worktree with no running worker does not
count against that number.

- An item that waits on a closed backend, a fault, a block, a verifier hold or its own gate
  keeps its worktree and frees its worker slot.
- A ready item starts when a worker slot is free. It never waits because another item is held.
- Worktrees are removed when their item lands, closes or is preserved to a ref.

This follows the owner's words of 12:07 AM: "It's fine to hold a lane if there's a good reason.
Lanes are nearly free." It is [OQ-UD2](#OQ-UD2), because it replaces the lane count as the unit
the drain is run by.

- **Degenerate cases.** Zero free slots: items queue, as today. One item with many held
  attempts: one worktree, reused.
- **Limit.** Disk. Each worktree is a checkout; the drain reports the count and refuses a new
  one only when the disk check fails, naming it.
- **Evidence.** The sampler's "idle while work waits" is replaced by "free worker slots while a
  ready item waits", which should read zero.

### 6.5 Rule 5: one item's fault holds that item and nothing else

- **The loop ends for two reasons only:** the operator stopped it, or the store cannot be read.
  Every other error belongs to an item, a lane or a check and becomes a hold with a named cause.
- **No flag set by one item's failure is read by another item's start.**
- **Enforced by fault injection.** For every step in a pass that touches git, the store or a
  process (stage, pin, preserve, record, start a gate, land, clean up), a test makes that step
  fail and asserts three things: the loop keeps running, the item is held with the cause, and a
  second ready item starts. A new step added without its row fails the test that lists the
  steps.

- **Evidence.** The keeper's count of loop exits not asked for by the operator is zero.

### 6.6 Rule 6: a drain build runs the live queue only after it passes the scenarios

Every incident in [Section 4](#4-every-incident) becomes a scenario with its number in its name.
The scenario suite runs against the newly built binary, a throwaway repository and a throwaway
store.

1. A landing that changes the drain builds the new binary, as today.
2. The scenario suite runs against that binary. The live loop keeps running the old binary and
   keeps starting workers.
3. On green, the loop swaps to the new binary between two passes. It holds no start while it
   waits for a gate; a running gate finishes under the binary that began it.
4. On red, the live loop stays on the old binary, and the red scenario is a trunk failure that
   rule 2 repairs.

- **Trigger.** Each new drain build; not a timer.
- **Rule for the future.** An incident is closed only when its scenario exists. A repair item
  for the drain names the scenario it adds.
- **Forbidden.** A scenario does not sleep, read the clock or depend on machine load.
- **Evidence.** The loop's build line names the scenario run that admitted the build.

### 6.7 Rule 7: a hold names the operator only for a decision the rulebook reserves

| Hold last night | Who moved it | Who moves it under this rule |
| :--- | :--- | :--- |
| Worker needs a path outside its fence | Operator, after up to 15 minutes | The item's designer step rules: widen, split or refuse ([OQ-UD4](#OQ-UD4)) |
| The pack copy of a file in the fence | Operator | Nobody: a fence that names a source path includes its pack copy |
| Three verifier refusals | Operator | The item's designer step reads them and rewrites the item or splits it |
| A refusal about the worker's report, not the candidate | A whole new build attempt | The worker amends the report; the candidate is not rebuilt |
| Blocker has landed | Operator | The drain (built: `5709b4fc2`) |
| Blocker was closed and filed again | Operator repoints | The new item records what it replaces, and waiters follow |
| Worker stopped for a red trunk | Operator | The drain, when the named check is green |
| A landed design with decisions ready to build | Operator files items | [`tillr-queue-mirrors-designs.md`](./tillr-queue-mirrors-designs.md), ruled and not built |
| An open question no roadmap links | Nobody | The repository's finishing check fails while `vantage-check index` lists any |

- **Enforced by the table of hold reasons.** Each reason declares its mover. A reason whose mover
  is the operator must also declare which decision it reserves; the test that reads the table
  fails otherwise. This extends
  [principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act).
- **Evidence.** The count of items held on the operator for longer than one check-in.

### 6.8 Rule 8: noticing does not depend on anyone being awake

This rule needs [`tillr-watch.md`](./tillr-watch.md), which waits on five rulings of its own.
What this design adds is the first rule set and one requirement on delivery.

| Rule | Fires when | Default |
| :--- | :--- | :--- |
| Trunk red, no repair in flight | The proof is red and no revert or fixer for that check is running | 10 minutes |
| Repair refused | A revert or fixer for the same check is refused a second time | 2 refusals |
| Slot idle with work ready | A worker slot is free while a ready item waits | 2 minutes |
| Held on a person | An item's mover is the operator or the owner | 15 minutes |
| Loop exit | The loop ends without an operator stop | immediately |
| Backend closed | A seat's backend refuses for quota or credentials | immediately |
| Same class twice | A second incident is filed under a class that already has one this run | 2 incidents |

**Delivery requirement.** An alert reaches the owner by a route that does not pass through the
operator's session or the designers' account. [`tillr-watch.md`](./tillr-watch.md) designs
delivery to the foreground session and to a drain seat ([OQ-TW3](./tillr-watch.md#OQ-TW3)); it
does not design a route to the owner that survives both stopping. That route is
[OQ-UD3](#OQ-UD3).

**The operator's part, which is procedure and not mechanism:**

- A second incident of one class is reported to the owner as a statement about the class, in
  the next report, not as a second fixed symptom.
- An item's words separate what was observed from what the operator concludes, and a conclusion
  not yet proved says so.
- A record entry's time is read from the clock by the command that writes the entry.
- A report says only what has been done.

## 7. Every incident and what stops it

| Incident | Rule | Tier | Scenario or check that fails if it returns |
| :--- | :--- | :--- | :--- |
| N-01, N-05, N-06, N-07, N-11, N-12 | 1 | structure | The gate plan for a drain change holds the drain suite in the landing tier |
| N-02, N-09, N-10 | 2, 3 | structure | A fixer lands its repair while a fork check is red |
| N-03 | 2, 8 | structure, signal | A written repair lands with no operator present |
| N-04, N-08 | 2 | structure | A landing arrives after a red verdict; the repair is still filed |
| N-13 | 2 | structure | The trunk moves during a revert's check; the revert lands |
| N-14 | 2 | structure | The fixer-filed line and the item exist together or not at all |
| N-15 | 4 | structure | A gate starts in the pass in which its worker ends |
| N-16 | 4 | structure | Four items wait on a closed backend; a fifth, needing another backend, starts |
| N-17 | 4 | structure | An item that cannot take the fork is not offered a slot again until the fork is free |
| N-18, N-19 | 4, 5 | structure | A step re-run after a quota refusal records a new decision; a faulted item holds no slot |
| N-20 | 5 | structure | One staging failure; a second ready item starts in the same pass |
| N-21 | 6 | structure | A roll is pending; a ready item starts |
| N-22 | 7 | structure | Slices named by a landed plan are in the queue without a hand step |
| N-23, N-24 | 5 | structure | Fault injection at pin and at preserve; the loop keeps running |
| N-25 | 4 | structure | Changing the worker limit needs no restart, as [`tillr-live-drain-config.md`](./tillr-live-drain-config.md) decides |
| N-26 | 7 | structure | A blocker lands; its waiter is ready in the next pass |
| N-27, N-32 | 4, 5 | structure | A worker ends mid-rebase; the next fork item stages |
| N-28 | 7 | structure | Filing refuses a fence path under a lane checkout |
| N-29 | 7 | structure | A report-only refusal does not start a build step |
| N-30, N-31 | 3 | structure | A rebase's gate runs only the packages the patches touch, with no credentials |
| N-33 | 7 | structure | A fence stop is ruled by the designer step; the pack copy needs no ruling |
| N-34 | 7 | structure | Three refusals go to the designer step |
| N-35 | 7 | structure | A stop for a red trunk holds on that check and releases on green |
| N-36 | 7 | structure | A landed design's ready decisions are queued |
| N-37 | 7 | structure | The finishing check fails on an unrouted question |
| N-38 | 7 | signal | [`nonconverging-items.md`](../tillr/nonconverging-items.md); rule 8 raises the alert |
| N-39 | 8 | structure or signal | By the ruling on [OQ-UD3](#OQ-UD3) |
| N-40 | 8 | signal | A closed backend reaches the owner off the session |
| N-41 | 8 | procedure | None. This is the one row that rests on conduct, and the "same class twice" alert is its backstop |

## 8. What done looks like

One night, checked the next morning from the store and the log:

- Eight hours with no operator action and no owner action.
- The trunk never red for longer than one revert takes, and green at the end.
- No free worker slot while a ready item waited for longer than 2 minutes.
- No item held on the operator.
- No loop exit.
- The worker limit raised from 2 toward 10 by the owner's rule, with each step's evidence
  recorded.

Until that night has happened, the claim "the drain repairs itself" is not made in any report.

## 9. What this does not change

- The ten-second gate for every change outside the drain's own code.
- The landing transaction and its principles.
- The ruling against attempt counts, time limits that fail work, and fallbacks to another model.
- Who designs: only an item's designer changes its definition. Rule 7 moves fence rulings to
  that seat; it does not let a worker widen its own fence.
- The alerting utility's design, which stays in [`tillr-watch.md`](./tillr-watch.md).

## 10. Alternatives

| Alternative | Verdict |
| :--- | :--- |
| Stop changing the drain through the drain; change it by hand until it is stable | Rejected. It removes the only repeated test the landing path gets, and hand landings are how an identity error reached the trunk on 2026-10-28 |
| Run the whole check program before every landing | Rejected. It gives up the short gate for every change to cure a problem that six drain landings caused |
| Keep landing first and make the revert faster | Rejected as the only measure. It is rule 2, and it is needed, but each red landing still costs a proof, a revert and every item that built on it |
| A second, always-on operator session | Rejected. It doubles the party doing by hand what rule 7 says the drain should do |
| Fix each incident as filed and add no rule | Rejected. It is what was done all night; 31 fixes landed and the night ended red |

## 11. Risks

| Risk | Mitigation |
| :--- | :--- |
| Rule 1 slows drain changes so much that repairs queue up | Suites run outside the landing lock; a repair for a red trunk is a fixer and keeps the fixer's gate |
| The scenario suite becomes slow or flaky and blocks every roll | Scenarios may not sleep or read the clock; a red scenario is a trunk failure with a first-red search like any other |
| Unlimited worktrees fill the disk | A reported count, a disk check before each new worktree, removal on land, close or preserve |
| Designer rulings on fences spend the subscription faster | A fence ruling is one short step; the pack-copy case, the most common last night, needs none |
| Alert rules fire constantly during the build-out | Thresholds are data the operator tunes, as [`tillr-watch.md`](./tillr-watch.md) designs |

## 12. Order of work

1. **Rule 2, the revert that cannot be outrun**, and **rule 5, fault injection**. Neither waits
   on a ruling, and together they end the two ways the night was lost: a trunk that stayed red
   and a loop that stopped.
2. **Rule 6, the scenario suite**, seeded with the incidents whose fixes have landed, so those
   fixes cannot be undone.
3. **Rule 3, outside inputs**, with the `pi` rebase delivered through it.
4. **Rule 1**, on the ruling of [OQ-UD1](#OQ-UD1).
5. **Rule 7**, on the ruling of [OQ-UD4](#OQ-UD4).
6. **Rule 8**, on the rulings in [`tillr-watch.md`](./tillr-watch.md) and [OQ-UD3](#OQ-UD3).
7. **Rule 4**, on the ruling of [OQ-UD2](#OQ-UD2). It is last because it changes the most and
   because rules 5 and 7 remove most of what made lanes scarce.
8. **The unattended night** of [Section 8](#8-what-done-looks-like).

## 13. Open Questions

1. <a id="OQ-UD1"></a>💬 **OQ-UD1: Should a change to the drain's own code wait for the drain's own tests before it lands?**

   At 9:51 AM a worker finished the change that removes failover. It rewrote more than twenty
   files in the drain's main package and handed its commit to the lane gate. The gate passed it
   in 62 seconds and it landed. The drain's test suite, which takes about seven and a half
   minutes, ran afterwards on the trunk and failed three tests; by then other items had built on
   the change. This happened six times in ten hours. The short gate is your target for every
   landing, so making this class wait is yours to decide.

   - **A — Drain changes wait for their own suite.** A change under `tools/tillr*` lands seven
     or eight minutes later than today. Other lanes keep landing meanwhile. Everything else
     keeps the short gate.
   - **B — Nothing waits; the revert gets faster.** Every landing stays quick. A bad drain
     change reaches the trunk, and you see a revert some minutes later and a retry after it.
   - **C — Drain changes wait only for the tests of the packages they touch.** Shorter than A.
     A change that breaks a test in a package it did not touch still reaches the trunk.

   <!-- vantage: question id=OQ-UD1 leaning="A (Drain changes wait for their own suite) — six red landings in ten hours each cost far more than eight minutes, and 49 of 53 landings were drain changes." -->

   _Leaning:_ A (Drain changes wait for their own suite) — six red landings in ten hours each cost far more than eight minutes, and 49 of 53 landings were drain changes.

   **Answer:**

   > _(empty — fill in when decided)_

2. <a id="OQ-UD2"></a>💬 **OQ-UD2: Should the number you set limit running workers, with worktrees unlimited?**

   At 12:48 AM the designer backend closed for quota. Four items at the head of the queue were
   each waiting for a designer, and each sat in one of the four lanes. Twelve to fourteen other
   items needed no designer and did not start for three hours, because there was no lane to
   start them in. You said that night: "It's fine to hold a lane if there's a good reason. Lanes
   are nearly free." Today the drain is started with a lane count, and a held item uses one up.

   - **A — The number is workers.** You set "10" and ten workers run whenever ten items are
     ready. A held item keeps its checkout on disk and costs no slot. You would see more
     worktrees than workers.
   - **B — The number stays lanes, and held items give theirs up.** The count on disk matches
     the number you set. Every kind of hold needs its own rule for giving the lane back and
     getting the work back, which is where four of last night's incidents came from.

   <!-- vantage: question id=OQ-UD2 leaning="A (The number is workers) — it removes the whole class instead of writing a release rule per hold, and it is what your words describe." -->

   _Leaning:_ A (The number is workers) — it removes the whole class instead of writing a release rule per hold, and it is what your words describe.

   **Answer:**

   > _(empty — fill in when decided)_

3. <a id="OQ-UD3"></a>💬 **OQ-UD3: What keeps watching when the Claude subscription runs out?**

   The operator session and every designer step run on your one Claude subscription. At 12:48 AM
   it reached its five-hour limit. Designers stopped, which the drain handles, and the operator
   stopped too, so nobody checked in until 4:10 AM. It happened again from 6:38 to 9:10 AM. A
   finished trunk repair waited two and a half hours in the first gap for someone to carry it.
   You have ruled against falling back to another model, so this is not the operator's to
   settle.

   - **A — The watch moves off the subscription.** Alerts reach you by a route that needs no
     Claude session. Designers and the operator still stop at the limit; you are told within
     minutes and the drain keeps doing what needs no designer.
   - **B — The operator runs on metered access; designers stay on the subscription.** Check-ins
     never stop. It costs money per check-in, and the operator is no longer the model you chose
     for it unless the metered model is the same one.
   - **C — Accept the stop.** Nothing changes. Once rules 2 and 7 are built the drain needs the
     operator far less, and a limit costs designer work only.

   <!-- vantage: question id=OQ-UD3 leaning="A (The watch moves off the subscription) — it is the only option that costs nothing per hour and still tells you; C becomes reasonable only after an unattended night has passed." -->

   _Leaning:_ A (The watch moves off the subscription) — it is the only option that costs nothing per hour and still tells you; C becomes reasonable only after an unattended night has passed.

   **Answer:**

   > _(empty — fill in when decided)_

4. <a id="OQ-UD4"></a>💬 **OQ-UD4: When a worker needs a file outside its fence, who says yes?**

   A worker fixing how the drain stages a fork item found the fault in a file its item did not
   list. It stopped, as it must, and named the file. The item then waited for the operator's
   next check-in, up to 15 minutes, and the operator added the path. This happened at least
   nine times last night; the answer was yes every time. Today only the operator runs the
   command that widens a fence.

   - **A — The item's designer step rules.** The worker's request goes to the designer seat for
     that item, which answers widen, split into a new item, or refuse, with its reason. No one
     waits for a check-in. A designer step is spent each time.
   - **B — The operator keeps the ruling; only the pack copy is automatic.** Naming a source
     file covers its pack copy without a request. Every other request waits for the operator as
     today.
   - **C — The worker may take any file its change needs, and the verifier judges the fence
     afterwards.** No waiting and no designer step. A worker that wanders is caught only after
     it has done the work.

   <!-- vantage: question id=OQ-UD4 leaning="A (The item's designer step rules) — the designer already owns the item's definition, so this adds no new authority and removes the wait." -->

   _Leaning:_ A (The item's designer step rules) — the designer already owns the item's definition, so this adds no new authority and removes the wait.

   **Answer:**

   > _(empty — fill in when decided)_

## 14. Decision Ledger

These are the designer's decisions. Each is the owner's to overturn. None is built.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D-UD1 | A revert is a landing and is never abandoned because the trunk moved | 2026-11-21 | [Rule 2](#62-rule-2-a-red-trunk-is-repaired-by-a-step-that-cannot-be-outrun) | — |
| D-UD2 | A check with an input outside the tree reports and files work; it does not refuse a candidate | 2026-11-21 | [Rule 3](#63-rule-3-only-the-candidates-tree-can-refuse-a-candidate) | — |
| D-UD3 | The fork gate tests the packages that differ between the upstream base and the fork head | 2026-11-21 | [Rule 3](#63-rule-3-only-the-candidates-tree-can-refuse-a-candidate) | — |
| D-UD4 | The loop ends only on an operator stop or an unreadable store; fault injection at every effectful step enforces it | 2026-11-21 | [Rule 5](#65-rule-5-one-items-fault-holds-that-item-and-nothing-else) | — |
| D-UD5 | A drain build is admitted to the live queue by the scenario suite, and a roll holds no start | 2026-11-21 | [Rule 6](#66-rule-6-a-drain-build-runs-the-live-queue-only-after-it-passes-the-scenarios) | — |
| D-UD6 | An incident is closed only when its scenario exists | 2026-11-21 | [Rule 6](#66-rule-6-a-drain-build-runs-the-live-queue-only-after-it-passes-the-scenarios) | — |
| D-UD7 | A hold reason whose mover is the operator declares the decision it reserves, and a test reads the table | 2026-11-21 | [Rule 7](#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) | — |
| D-UD8 | A fence that names a source path includes its pack copy | 2026-11-21 | [Rule 7](#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) | — |
| D-UD9 | A refusal about the report amends the report and does not rebuild the candidate | 2026-11-21 | [Rule 7](#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) | — |
| D-UD10 | The first alert rules and their defaults | 2026-11-21 | [Rule 8](#68-rule-8-noticing-does-not-depend-on-anyone-being-awake) | — |
