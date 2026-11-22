---
title: "Why the drain could not run one night alone, and the eight rules that let it"
status: accepted
stage: DECIDED
updated: 2026-11-21
tags: [tillr, drain, gate, landing, lanes, incident, design]
summary: "The night of 2026-11-21 produced 42 separate incidents and no proved-green trunk for nine hours. They reduce to eight structural gaps. Each gets one rule, a mechanism that enforces it, and a permanent scenario that fails if the incident returns."
next: "Write the implementation plan. The owner ruled all four questions on 2026-11-21; first in the build order are the revert that cannot be outrun, fault injection, and putting the fence retirement of 2026-11-19 into effect."
depends-on:
  - landing-principles.md
  - trunk-fixer-gate.md
  - tillr-watch.md
vantage:
  status-chip: true
---

# Why the drain could not run one night alone, and the eight rules that let it

**Status:** 2026-11-21. The owner ruled all four questions the same day ([the ledger](#14-decision-ledger)). Nothing in this document is built. The incident record was read from the
loop's log, the store's faces, the sampler and `git log` on the morning of 2026-11-21, with the
trunk at `687b1a2c97`.

> **In short.** The night did not fail forty-two times. It failed eight ways. The drain lets a
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

**Needs your ruling:** nothing. The ten designer's decisions in [the ledger](#14-decision-ledger)
are yours to overturn.

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
- **Fence.** The list of paths an item's worker may change. The owner retired it on 2026-11-19
  ([the vocabulary design](./tillr-vocabulary.md#51-the-work-and-its-statement)); the drain still
  enforced it all night (N-42).
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
| N-33 | all night | Items stopped at their fence at least nine times and waited for a check-in | The fence is still enforced (N-42); a worker that needs one more path stops and only the operator widens a fence | `1e87beb33` adds a command to widen; the cause is open |
| N-42 | since 2026-11-19 | The owner's ruling that retires the fence is not in effect | The item that replaces path lists with one deny-list reads as landed in the queue after 69 attempts, yet `rg -l -i deny tools/tillr-drain/drain config/tillr` finds nothing on 2026-11-21. How it came to read as landed, read from the store's events on 2026-11-21: its 62nd attempt met a merge conflict at landing; its 67th landed only documents (`352e1c71d4`, six Markdown files); on 2026-11-20 at 7:31 PM ET it was set to wait on the item that classifies fork work at planning, as "Duplicate work"; when that item landed on 2026-11-21 the wait was released at 6:21 AM ET, and at 6:27 AM ET an operator action (`finish-existing-landing`, "Duplicate of work that landed") set it to landed. The item it was called a duplicate of delivers fork classification, not the deny-list, and the one candidate that added `config/tillr/deny-list.json` never reached the trunk. A build worker then stopped on the retirement because the deny-list as requested banned the item declarations a saved idea's turn must add; the designer's [`D-VO13`](./tillr-vocabulary.md#D-VO13) settles that. A second build worker stopped the same day because the request listed refs among what the deny-list protects, and a moved ref does not show who moved it; the designer's [`D-VO14`](./tillr-vocabulary.md#D-VO14) settles that the deny-list reads only the candidate's committed change | open |
| N-34 | 4:58, 6:16 AM | Items held after three verifier refusals until the operator read them | The hold's mover is the operator | open |
| N-35 | 10:15 AM | A worker that stopped correctly for a red trunk was held as a fence escape | The loop judged its uncommitted work before reading the reason it stopped | queued |
| N-36 | 4:14 to 9:14 AM | The fixer-gate design landed and its build was not filed for five hours | Filing a build from a landed design is a hand step | open |
| N-37 | since 2026-11-20 | Fourteen open questions, five of them the alerting design's, reached no roadmap and no report | Nothing fails when a question is unrouted | open |
| N-38 | all night | The refused-push repair ran 16 attempts and the rebase 13 | One item too large to verify; see [`nonconverging-items.md`](../tillr/nonconverging-items.md) | split into 22 slices, `793e165b84` |

#### Evidence for N-42

On 2026-11-21 the read-only queries below showed that attempt 62 conflicted and attempt 67
landed only the six Markdown files in `352e1c71d45`. The wait then cleared when the fork
classification work landed. The operator's `finish-existing-landing` event at 11:27:14 UTC
set the item to `landed` with `resolves_via` pointing to that work, not to a deny-list
implementation. The two linked landings delivered fork classification and routing. The
base had no tracked deny-list; the path's history on that base was empty.

```bash
python3 - <<'PY'
import sqlite3
c = sqlite3.connect('file:/workspace/.tillr/state.db?mode=ro', uri=True)
c.execute('PRAGMA query_only=ON')
i = 'item-request-3ee8dd349e'
for row in c.execute('select state,lifecycle_by,lifecycle_reason,resolves_via from items where item_id=?', (i,)):
    print(row)
for row in c.execute('select run_id,result,trunk_after from landings where item_id=?', (i,)):
    print(row)
for row in c.execute('select seq,kind,payload from events where seq in (1319976,1326764,1327583,1339466,1339645) order by seq'):
    print(row)
PY
git show --format='%h %s' --name-only 352e1c71d45 7b2fac8e8a 3537272f7a
git ls-tree f60cfc0801ec1119120588567a786fd4684a0419 config/tillr/deny-list.json
git log --oneline f60cfc0801ec1119120588567a786fd4684a0419 -- config/tillr/deny-list.json
```

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
   N-35, N-36). Nine or more of those were fence stops, under a fence the owner retired two days
   earlier (N-42).
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

The owner ruled this on 2026-11-21 ([OQ-UD1](#OQ-UD1)): it sets aside the ten-second target for
this one class of change and no other.

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
Lanes are nearly free." The owner ruled it on 2026-11-21 ([OQ-UD2](#OQ-UD2)): the worker count
replaces the lane count as the unit the drain is run by.

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
| Worker needs a path outside its fence | Operator, after up to 15 minutes | Nobody: there is no fence. The owner retired it on 2026-11-19 and repeated that on 2026-11-21 ([OQ-UD4](#OQ-UD4)). One deny-list protects shared state from every worker, and the judge checks that the change does what the words ask and nothing more ([the vocabulary design](./tillr-vocabulary.md#51-the-work-and-its-statement)) |
| Three verifier refusals | Operator | The item's designer step reads them and rewrites the item or splits it |
| A verdict about the worker's report, not the candidate | A whole new build attempt | The verifier corrects its verdict against the same candidate; the worker has nothing to amend |
| Blocker has landed | Operator | The drain (built: `5709b4fc2`) |
| Blocker's landing was rewritten to a patch-identical trunk commit | Operator, with no command named | The drain, by matching `git patch-id --stable` into `trunk_rewrites` ([Rewritten blocker landings](#rewritten-blocker-landings)) |
| Blocker was closed and filed again | Operator repoints | The new item records what it replaces, and waiters follow |
| Worker stopped for a red trunk | Operator | The drain, when the named check is green |
| A landed design with decisions ready to build | Operator files items | [`tillr-queue-mirrors-designs.md`](./tillr-queue-mirrors-designs.md), ruled and not built |
| An open question no roadmap links | Nobody | The repository's finishing check fails while `vantage-check index` lists any |

- **Enforced by the table of hold reasons.** Each reason declares its mover. A reason whose mover
  is the operator must also declare which decision it reserves; the test that reads the table
  fails otherwise. This extends
  [principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act).
- **Evidence.** The count of items held on the operator for longer than one check-in.

#### Rewritten blocker landings

*(`D-UD11`, the designer's decision, following an operator request; the owner may overturn it.)*

Commit `4652d0a6fb` [unlanded] replaced a misleading `trunk-unproved` hold with `blocker-landing-not-on-trunk: <blocker> at <sha>` when a landed trunk blocker's recorded `landings.trunk_after` (resolved through `trunk_rewrites`) is an ancestor of neither `refs/tillr/green` nor `refs/heads/main`, and gave it the mover `"the operator; restore the named landing's trunk ancestry or repair its recorded rewrite mapping"`. Before `2323ec3153` taught `tillr drain accept-rewrite` to populate `trunk_rewrites`, three trunk rewrites had already been accepted under `refs/tillr/trunk-history/accepted/` (`b15d48ce6aa4` [unlanded] on 2026-11-17, `d6927d19ab3a` [unlanded] on 2026-11-20 after commit-identity normalization, and `9843836c0974` [unlanded] on 2026-11-20), leaving `trunk_rewrites` empty (`0` rows).

**Count of affected items and landings (measured 2026-11-21 against `.tillr/state.db` and `refs/heads/main`).** Zero other open items are held the same way: the pi totals-reader item, waiting on `9a3b977aff` [unlanded] (rewritten in `d6927d19ab3a` [unlanded] to `b14b8c9dea` with the same subject, diffstat, and `git patch-id --stable`), is the only open item held by `blocker-landing-not-on-trunk`. Two closed items still carry `blocker-landing-not-on-trunk` in planner history from the same `d6927d19ab3a` [unlanded] rewrite (both pointing to `eb7cb5d9c648` [unlanded], with `mover: "no further work: closed"`). Across the store as a whole, `149` trunk `landings.trunk_after` rows from those three pre-`2323ec3153` rewrites remain unmapped (`35` in `d6927d19ab3a` [unlanded], `42` in `b15d48ce6aa4` [unlanded], and `72` in `9843836c0974` [unlanded], excluding `refs/heads/patch/*` fork landings and four older fork landings that share no merge base with `refs/heads/main`), and all `149` match a unique commit on `refs/heads/main` by `git patch-id --stable` (`0` ambiguous, `0` unmatched).

**Operator-free mapping when the patch matches (`D-UD11`).** When both commits carry the same patch, no human judgment is owed. At pass start in `tillr-drain` (alongside trunk observation and acquittal release, opening the store writer only when at least one pair is matched and caching checked `(old_sha, main_tip)` pairs in memory so an unmatchable commit does not re-scan `git log -p` on unchanged tips), the drain checks every trunk `landings` row (`result = 'landed'`, `$.base_ref` not under `refs/heads/patch/*`) whose resolved `trunk_after` (`old_sha`) is not an ancestor of `refs/heads/main`:

1. If `old_sha` exists in Git and `git merge-base <old_sha> refs/heads/main` exits `0` at merge base $B$ (exit `1` skips unrelated fork histories without error), compute `git patch-id --stable` for `old_sha` and for $B..\text{refs/heads/main}$ (batching `git log --no-merges -p` from the oldest merge base across the pass's candidates).
2. When `old_sha` has a non-empty patch ID that matches a unique commit `new_sha` in $B..\text{refs/heads/main}$ one-to-one, write `(old_sha, new_sha, recorded_at, evidence)` into `trunk_rewrites`, update any `refs/tillr/landed/<run_id>` marker pointing at `old_sha` to `new_sha`, and append a `drain.trunk-rewrite-absorbed` event naming the `git patch-id --stable` evidence.
3. Reconciling all unmapped trunk `landings` rows repairs all `149` pre-`2323ec3153` rows on the first pass so future dependents and read-only `tillr drain why` and `tillr drain plan` calls see the mapping immediately. Proof admission stays unchanged: if `new_sha` is in `refs/tillr/green`, the dependent item is ready; if `new_sha` is on `refs/heads/main` ahead of `refs/tillr/green`, it waits on `trunk-unproved` (`the heavy runner; prove the trunk`).

**Named command and owner mover when no unique patch match exists (`D-UD11`).** `blocker-landing-not-on-trunk` remains reachable only when `old_sha` has no unique patch-identical replacement on `refs/heads/main` (an empty commit, a rewrite that altered the diff, an ambiguous duplicate patch, or a commit dropped from `main`). Substituting a non-patch-identical commit for a landed blocker, or restoring dropped trunk history, changes what counts as landed rather than acting on a mechanical fact, so the hold names the owner and the exact command ([principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)):

- When no unaccepted trunk-tip rewrite is active (`trunk_history.lost_tip == ""` and `refs/heads/main` contains `observed_tip`), `tillr drain accept-rewrite` (`AcceptRewrite` in `tools/tillr-drain/drain/trunk_rewrite_accept.go`) also accepts an unmapped `landings.trunk_after` commit (`result = 'landed'`, resolved SHA not on `refs/heads/main`) replaced by an ancestor commit on `refs/heads/main`, pinning `refs/tillr/trunk-history/accepted/<lost>`, writing `(lost, current)` into `trunk_rewrites` (`evidence = "accept-rewrite:landing"`), and appending `drain.trunk-rewrite-accepted` — while continuing to refuse with `"no rewrite is recorded"` once mapped or when the lost commit is neither an active lost trunk tip nor an unmapped landing, and keeping existing `accept-rewrite` flags and `--help` text intact.
- The hold rule for `blocker-landing-not-on-trunk` in `tools/tillr-drain/holdrules/holdrules.go` (and `tools/tillr-drain/README.md`) replaces `"the operator; restore the named landing's trunk ancestry or repair its recorded rewrite mapping"` with `"the owner; restore the named landing to main, or map its replacement with tillr drain accept-rewrite"`.

*(Conflict note for the returned build: the first build attempt ended on `OBSTACLE: definition` because its lane base `7243353ec6` equaled `refs/tillr/green` and lacked `4652d0a6fb` [unlanded] on `refs/heads/main`. Landing this design update squashes onto `refs/heads/main` past `4652d0a6fb` [unlanded] and advances the returned request's staged base through `designAnswerLanded` in `tools/tillr-drain/drain/lane_base.go`, so `ReasonBlockerLandingNotOnTrunk` and its `holdrules.go` entry are present on the returned worker's base tree. Authorized build scope spans `tools/tillr-drain/` and `yolo-packs/tillr/files/local/share/tillr/tillr-drain/`, committing `just pack-sync` inside the writing commit rather than as a separate trailing piece.)*

### 6.8 Rule 8: noticing does not depend on anyone being awake

This rule needs [`tillr-watch.md`](./tillr-watch.md). The owner ruled its five questions on
2026-11-21: alerts go to the foreground agent, and the utility executes no remedy of its own.
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

**Delivery, as ruled on 2026-11-21.** Alerts go to the foreground agent
([`tillr-watch.md`](./tillr-watch.md#OQ-TW4)). When the shared subscription reaches its limit the
operator and the designers stop, and for now that stop is accepted ([OQ-UD3](#OQ-UD3)): no route
to the owner outside the session is built. What makes the stop affordable is rules 2 and 7, under
which a red trunk and a mechanical hold do not wait for the operator. The owner's "for now" is
revisited after the unattended night of [Section 8](#8-what-done-looks-like).

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
| N-29 | 7 | structure | A finding about the worker's report returns to the verifier, not the builder |
| N-30, N-31 | 3 | structure | A rebase's gate runs only the packages the patches touch, with no credentials |
| N-33, N-42 | 7 | structure | A worker changes a path its item never listed and is not stopped; a path on the deny-list is refused with the entry named |
| N-34 | 7 | structure | Three refusals go to the designer step |
| N-35 | 7 | structure | A stop for a red trunk holds on that check and releases on green |
| N-36 | 7 | structure | A landed design's ready decisions are queued |
| N-37 | 7 | structure | The finishing check fails on an unrouted question |
| N-38 | 7 | signal | [`nonconverging-items.md`](../tillr/nonconverging-items.md); rule 8 raises the alert |
| N-39 | 2, 7 | accepted for now | None for the stop itself ([OQ-UD3](#OQ-UD3)); the scenarios of rules 2 and 7 run with no operator present |
| N-40 | 8 | signal | A closed backend raises an alert to the foreground agent |
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
- Who designs: only an item's designer changes its definition.
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
| With no fence a worker wanders outside its item | The deny-list refuses a committed change to shared state before the gate (the designer's [`D-VO14`](./tillr-vocabulary.md#D-VO14), which the owner may overturn), and the judge names any changed path the item's words do not account for |
| Alert rules fire constantly during the build-out | Thresholds are data the operator tunes, as [`tillr-watch.md`](./tillr-watch.md) designs |

## 12. Order of work

1. **Rule 2, the revert that cannot be outrun**, and **rule 5, fault injection**. Together they
   end the two ways the night was lost: a trunk that stayed red and a loop that stopped.
2. **The fence retirement, put into effect** (rule 7, N-42). It was ruled on 2026-11-19 and
   accounts for the largest single group of hand releases.
3. **Rule 1**, drain changes prove their own suites before landing.
4. **Rule 6, the scenario suite**, seeded with the incidents whose fixes have landed, so those
   fixes cannot be undone.
5. **Rule 3, outside inputs**, with the `pi` rebase delivered through it.
6. **The rest of rule 7.**
7. **Rule 8**, once the delivery designed in [`tillr-watch.md`](./tillr-watch.md#D-TW11) is built.
8. **Rule 4**, workers limited and worktrees not. It is last because it changes the most and
   because rules 5 and 7 remove most of what made lanes scarce.
9. **The unattended night** of [Section 8](#8-what-done-looks-like).

## 13. Open Questions

None. The owner ruled all four on 2026-11-21; each is a row in [the ledger](#14-decision-ledger).

## 14. Decision Ledger

The `OQ-UD` rows are the owner's rulings of 2026-11-21, given as Vantage review comments. The
`D-UD` rows are the designer's decisions, each the owner's to overturn. None of either is built.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-UD1"></a>[OQ-UD1](#OQ-UD1) | Owner ruling: a change to the drain's own code waits for the drain's own suite before it lands | 2026-11-21 | [Rule 1](#61-rule-1-the-drains-code-proves-its-own-suites-before-it-lands) | — |
| <a id="OQ-UD2"></a>[OQ-UD2](#OQ-UD2) | Owner ruling: the number the owner sets limits running workers; worktrees are not counted | 2026-11-21 | [Rule 4](#64-rule-4-workers-are-limited-worktrees-are-not) | — |
| <a id="OQ-UD3"></a>[OQ-UD3](#OQ-UD3) | Owner ruling: for now, accept that the operator and designers stop when the subscription runs out | 2026-11-21 | [Rule 8](#68-rule-8-noticing-does-not-depend-on-anyone-being-awake) | — |
| <a id="OQ-UD4"></a>[OQ-UD4](#OQ-UD4) | Withdrawn. The owner had already retired the fence on 2026-11-19; the question should not have been asked. What is owed is that ruling's build | 2026-11-21 | [Rule 7](#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) | — |
| D-UD1 | A revert is a landing and is never abandoned because the trunk moved | 2026-11-21 | [Rule 2](#62-rule-2-a-red-trunk-is-repaired-by-a-step-that-cannot-be-outrun) | — |
| D-UD2 | A check with an input outside the tree reports and files work; it does not refuse a candidate | 2026-11-21 | [Rule 3](#63-rule-3-only-the-candidates-tree-can-refuse-a-candidate) | — |
| D-UD3 | The fork gate tests the packages that differ between the upstream base and the fork head | 2026-11-21 | [Rule 3](#63-rule-3-only-the-candidates-tree-can-refuse-a-candidate) | — |
| D-UD4 | The loop ends only on an operator stop or an unreadable store; fault injection at every effectful step enforces it | 2026-11-21 | [Rule 5](#65-rule-5-one-items-fault-holds-that-item-and-nothing-else) | — |
| D-UD5 | A drain build is admitted to the live queue by the scenario suite, and a roll holds no start | 2026-11-21 | [Rule 6](#66-rule-6-a-drain-build-runs-the-live-queue-only-after-it-passes-the-scenarios) | — |
| D-UD6 | An incident is closed only when its scenario exists | 2026-11-21 | [Rule 6](#66-rule-6-a-drain-build-runs-the-live-queue-only-after-it-passes-the-scenarios) | — |
| D-UD7 | A hold reason whose mover is the operator declares the decision it reserves, and a test reads the table | 2026-11-21 | [Rule 7](#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) | — |
| D-UD9 | A refusal about the report amends the report and does not rebuild the candidate | 2026-11-21 | [Rule 7](#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) | — |
| D-UD10 | The first alert rules and their defaults | 2026-11-21 | [Rule 8](#68-rule-8-noticing-does-not-depend-on-anyone-being-awake) | — |
| D-UD11 | **Designer's decision, following an operator request; the owner may overturn this.** Reconcile unmapped trunk `landings.trunk_after` commits at pass start by unique `git patch-id --stable` on `merge-base(old_sha, main)..main` into `trunk_rewrites` (skipping `refs/heads/patch/*` and unrelated-root commits with no merge base on `main`, and keeping `refs/tillr/green` proof admission intact); when no unique patch match exists, hold `blocker-landing-not-on-trunk` on the owner (`"the owner; restore the named landing to main, or map its replacement with tillr drain accept-rewrite"`) and let `tillr drain accept-rewrite` map an individual unmapped `landings.trunk_after` commit onto an ancestor of `refs/heads/main` when no trunk-tip rewrite is pending | 2026-11-21 | [Rule 7](#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves), [Rewritten blocker landings](#rewritten-blocker-landings) | — |
