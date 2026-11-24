---
title: "Eight rules that let the drain run unattended"
status: accepted
stage: DECIDED
tags: [tillr, drain, gate, landing, lanes, design]
summary: "Eight structural gaps stop the drain from running a night alone. Each gets one rule, a mechanism that enforces it, and a permanent scenario that fails if the gap returns."
next: "Write the implementation plan; first in the build order are the revert that cannot be outrun and fault injection."
depends-on:
  - landing-principles.md
  - ../reference/gate.md
  - tillr-watch.md
vantage:
  status-chip: true
---

# Eight rules that let the drain run unattended

**Status:** The owner ruled all four questions on 2026-11-21 ([the ledger](#14-decision-ledger)).
Of the decisions here, the rewritten-blocker mapping (`D-UD11`) is built; the loop is still run by
a lane count (`tillr-drain loop --lanes N`), so rule 4 is not. The other rows were not re-verified
against the code and are stated as design.

> **In short.** An unattended drain fails in eight ways, not in dozens. It lets a change to its own
> code land before that code's tests run, and then depends on a repair path, an operator and a
> subscription that can each stop. Close those eight gaps by mechanism and each incident loses the
> condition it needs.

**Why it matters.** Measured over one unattended night of about ten hours: 53 landings, of which 49
changed the drain itself; six landings named as the first red commit; five loop exits; more than
thirty holds released by hand; and no proved-green trunk for nine hours.

**The shape.** Eight rules, each with one enforcing mechanism, and one scenario suite that holds a
named scenario for every incident class. A drain build runs the live queue only after that suite
passes.

**Cost.** A change to the drain's own code waits for its own test suite before landing, which is
minutes, not the seconds every other change gets. Lanes stop being a fixed count.

**Start at [Section 5](#5-the-eight-gaps)**: the diagnosis. The rules fall out of it.

**Needs your ruling:** nothing. The designer's decisions in [the ledger](#14-decision-ledger) are
the owner's to overturn.

**Reads with:** [`landing-principles.md`](./landing-principles.md) (the settled rulings these rules
extend), [the gate reference](../reference/gate.md#the-trunk-fixers-gate) (the fixer's gate),
[`tillr-watch.md`](./tillr-watch.md) (the alerting design rule 8 needs),
[an oversized item lands in parts](./tillr-item-split.md) (items that do not finish).

---

## 1. Scope

This covers what the drain must do to run with no operator and no owner present. It does not cover:

- the design of the alerting utility, which [`tillr-watch.md`](./tillr-watch.md) owns;
- file-by-file build steps, which belong to an implementation plan.

## 2. Terms

Each term is used below in exactly this sense.

- **Drain.** The `tillr-drain` loop: one long-running process that takes items from the queue,
  starts workers on them, gates their results and lands them.
- **Item.** One piece of queued work, with words and pass conditions.
- **Fence.** A list of paths an item's worker may change. The owner retired it on 2026-11-19
  ([the vocabulary design](./tillr-vocabulary.md#51-the-work-and-its-statement)); one deny-list
  protecting shared state replaces it.
- **Lane.** A worktree the drain keeps for running one item at a time. An express lane takes only
  items filed as urgent.
- **Worker.** One agent process running one step of one item in a lane.
- **Trunk.** The `main` branch.
- **Lane gate.** The checks a candidate must pass before it lands.
- **Trunk proof.** The full check program, run on the trunk after landings. Also called the rolling
  tier.
- **Green.** The newest trunk commit the trunk proof has passed.
- **Fixer.** An item the drain files by itself to repair a red trunk.
- **Hold.** A state in which an item waits. Every hold names a **mover**: who or what ends it.
- **Operator.** The foreground agent session that watches the drain.
- **Designer.** The seat that writes designs, plans and reviews.
- **Fork.** A patched copy of an upstream tool, kept as a series of patches.
- **Roll.** The loop replacing itself with a newly built drain binary after a landing changes the
  drain's code.
- **Scenario** *(coined here)*. A scripted run of a drain build against a throwaway repository and
  store, with one fault arranged, that asserts what the drain does. It is not a unit test of one
  function and it is not a run of the live queue.

## 3. The measurements the rules are judged by

| Measure | Target |
| :--- | :--- |
| First-red landings whose failing check sits in a module the landing changed | 0 |
| Time from a first-red verdict to a landed revert or a started fixer | one revert's duration |
| Free worker slots while a ready item waits | 0 for longer than 2 minutes |
| Loop exits not asked for by the operator | 0 |
| Items held on the operator for longer than one check-in | 0 |

## 4. The incident classes

Each class is a way an unattended drain stops making progress.
[Section 7](#7-every-incident-class-and-what-stops-it) maps each to its rule.

### 4.1 A red trunk that stays red

A change to the drain's code lands through a lane gate that does not run the drain's suite. The
repair is then refused by a gate that reads checks the repair cannot affect, or abandoned because
the trunk moved, or never filed because a later landing discarded the finished red verdict.

### 4.2 Lanes empty while work waits

An item that cannot run — waiting on a closed backend, faulted, blocked, or behind a roll — keeps a
lane that a ready item needs.

### 4.3 The loop itself stops

An error that belongs to one item, such as a bad ref or an over-long argument list, ends the
process.

### 4.4 An upstream release refuses correct work

A fork rebase is refused by checks whose answers come from outside its tree: an upstream test, a
live credential, a wall-clock limit, or a comparison against the old fork tip.

### 4.5 Work that waited for a person

An item waits for the operator to do something the drain has every fact to do: add a path, release
an item whose blocker landed, read repeated refusals, or file the build of a landed design.

### 4.6 Nobody watching

The operator and the designers share one subscription, so its limit removes both, and an alarm that
is only a log line wakes nobody.

## 5. The eight gaps

1. **The drain's own code lands before its own tests run.** The lane gate is built to finish in
   seconds, so Go test suites run after landing. That is right for a configuration change and wrong
   for the drain's package.
2. **Repairing the trunk has conditions that a red trunk breaks.** A revert is abandoned when the
   trunk moves, and the trunk always moves. A fixer's gate reads checks the fixer cannot affect.
3. **Checks whose answer comes from outside the tree can refuse a candidate.**
4. **A lane is scarce and an item can keep one while nothing runs.**
5. **One item's fault can stop the loop or every other item.**
6. **The drain's fixes are tried for the first time on the live queue.** Nothing rehearses a new
   drain build before it runs the queue, and the roll that installs it costs lane time.
7. **Mechanical remedies wait for a person.**
8. **Noticing depends on someone being awake, and one account going quiet removes everyone.**

## 6. The eight rules

Each rule states the behavior, what happens when it fails, and the evidence an operator reads. The
first tier that can be reached is used: a structure that makes the incident impossible, else a
signal where it happens, else a written procedure.

### 6.1 Rule 1: the drain's code proves its own suites before it lands

A candidate that changes a file inside a Go module under `tools/tillr*` runs that module's whole
suite in its lane gate, and the gate's own self-tests when it changes `tools/gate/`. Changes outside
those paths keep the short gate.

- **Trigger.** The candidate's changed paths, read from the record the gate already writes.
- **Cost.** The drain's suite measured 7 min 35 s in one proof replay. The gate runs outside the
  landing lock ([principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once)), so one
  candidate's suite does not hold another candidate's landing.
- **Failure.** A red suite is a refusal like any other check and goes back to the worker.
- **What it does not cover.** Two candidates that each pass alone and fail together. The trunk
  proof stays for that, and rule 2 repairs it.
- **Evidence.** The gate's plan lists the suite in the landing tier for such a candidate.

Owner ruling [OQ-UD1](#OQ-UD1): this sets aside the short-gate target for this one class of change
and no other.

### 6.2 Rule 2: a red trunk is repaired by a step that cannot be outrun

1. **The revert is a landing.** When the proof names a first red landing, the loop builds its
   revert and replays the one failing check on it. If the trunk moves meanwhile, the revert is
   applied onto the new tip and landed without a second check, as
   [principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once) treats any candidate.
   A moved trunk is never a reason to abandon a revert.
2. **A revert that conflicts files a fixer**, because a later landing built on the red one. The
   fixer is gated as [the gate reference](../reference/gate.md#the-trunk-fixers-gate) states.
3. **Every finished red verdict produces a repair**, whatever lands after it.
4. **A log line that says a fixer was filed names the item**; when no item was created, the reason
   is written instead.
5. **Every first-red landing records which gate tier held the failing check.** That record is the
   measurement rule 1 is judged by.

- **Concurrency.** One revert or fixer per failing check at a time.
- **Evidence.** Time from a first-red verdict to a landed revert or a started fixer.

### 6.3 Rule 3: only the candidate's tree can refuse a candidate

A check whose result depends on something outside the tree it judges cannot fail a candidate. It
reports, and its report becomes work.

| Outside input | What the check does instead of refusing |
| :--- | :--- |
| An upstream release moved | Files one rebase item per fork and release, as `D-FG3` in [the gate reference](../reference/gate.md#upstream-drift) decides |
| A test needs a live credential | Runs without credentials; a test that then fails is recorded as needing one |
| An upstream test fails at upstream's own tag | Recorded against upstream, with the tag; it does not judge the patches |
| A wall-clock limit inside a suite the repository does not own | As the row above |

**The fork gate judges the patches.** For a fork candidate, the packages tested are those that
differ between the upstream base and the fork head. For a rebase the upstream base is the new tag,
so packages only upstream changed are not run.

- **Failure.** A check that cannot say whether its input is outside the tree says so and is treated
  as not judged ([principle 8](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)).
- **Evidence.** Each check in the registry declares its outside inputs; a check with one never
  appears as the failing check of a refusal.

### 6.4 Rule 4: workers are limited, worktrees are not

The number the owner sets is how many workers run at once. An item in flight has a worktree of its
own for as long as it needs one, and a worktree with no running worker does not count against that
number (owner ruling [OQ-UD2](#OQ-UD2)).

- An item that waits on a closed backend, a fault, a block, a verifier hold or its own gate keeps
  its worktree and frees its worker slot.
- A ready item starts when a worker slot is free. It never waits because another item is held.
- Worktrees are removed when their item lands, closes or is preserved to a ref.
- **Limit.** Disk. The drain reports the worktree count and refuses a new one only when the disk
  check fails, naming it.
- **Evidence.** "Free worker slots while a ready item waits" reads zero.

### 6.5 Rule 5: one item's fault holds that item and nothing else

- **The loop ends for two reasons only:** the operator stopped it, or the store cannot be read.
  Every other error belongs to an item, a lane or a check and becomes a hold with a named cause.
- **No flag set by one item's failure is read by another item's start.**
- **Enforced by fault injection.** For every step in a pass that touches git, the store or a
  process (stage, pin, preserve, record, start a gate, land, clean up), a test makes that step fail
  and asserts three things: the loop keeps running, the item is held with the cause, and a second
  ready item starts. A new step added without its row fails the test that lists the steps.
- **Evidence.** The count of loop exits not asked for by the operator is zero.

### 6.6 Rule 6: a drain build runs the live queue only after it passes the scenarios

Every incident class becomes a scenario. The scenario suite runs against the newly built binary, a
throwaway repository and a throwaway store.

1. A landing that changes the drain builds the new binary.
2. The scenario suite runs against that binary. The live loop keeps running the old binary and
   keeps starting workers.
3. On green, the loop swaps to the new binary between two passes. It holds no start while it waits
   for a gate; a running gate finishes under the binary that began it.
4. On red, the live loop stays on the old binary, and the red scenario is a trunk failure that
   rule 2 repairs.

- **Trigger.** Each new drain build; not a timer.
- **Rule for the future.** An incident is closed only when its scenario exists.
- **Forbidden.** A scenario does not sleep, read the clock or depend on machine load.
- **Evidence.** The loop's build line names the scenario run that admitted the build.

### 6.7 Rule 7: a hold names the operator only for a decision the rulebook reserves

| Hold | Who moves it under this rule |
| :--- | :--- |
| Worker needs a path outside its fence | Nobody: there is no fence ([OQ-UD4](#OQ-UD4)). One deny-list protects shared state from every worker, and the judge checks that the change does what the words ask and nothing more ([the vocabulary design](./tillr-vocabulary.md#51-the-work-and-its-statement)) |
| Repeated verifier refusals | The item's designer step reads them and rewrites the item or splits it |
| A verdict about the worker's report, not the candidate | The verifier corrects its verdict against the same candidate; the worker has nothing to amend |
| Blocker has landed | The drain |
| Blocker's landing was rewritten to a patch-identical trunk commit | The drain ([Rewritten blocker landings](#rewritten-blocker-landings)) |
| Blocker was closed and filed again | The new item records what it replaces, and waiters follow |
| Worker stopped for a red trunk | The drain, when the named check is green |
| A landed design with decisions ready to build | [`tillr-queue-mirrors-designs.md`](./tillr-queue-mirrors-designs.md) |
| An open question no roadmap links | The repository's finishing check fails while `vantage-check index` lists any |

- **Enforced by the table of hold reasons.** Each reason declares its mover. A reason whose mover
  is the operator must also declare which decision it reserves; the test that reads the table fails
  otherwise. This extends
  [principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act).
- **Evidence.** The count of items held on the operator for longer than one check-in.

#### Rewritten blocker landings

*(`D-UD11`, the designer's decision; the owner may overturn it. Built.)*

An item that waits on a landed blocker reads the blocker's recorded trunk commit. When trunk history
is rewritten, that commit is no longer an ancestor of `refs/heads/main`, and the waiter is held as
`blocker-landing-not-on-trunk`.

**When the patch matches, no judgment is owed.** At pass start the drain checks each trunk landing
whose recorded commit is not on `refs/heads/main`. If that commit's `git patch-id --stable` matches
exactly one commit between their merge base and `main`, the drain records the pair in
`trunk_rewrites`, moves the landing's marker ref, and appends a `drain.trunk-rewrite-absorbed`
event. Proof admission is unchanged: the waiter is ready if the replacement is in
`refs/tillr/green`, and otherwise waits for the trunk proof. Fork delivery branches and commits
sharing no history with `main` are skipped.

**When no unique match exists, the owner moves.** An empty commit, a rewrite that changed the diff,
a duplicated patch, or a dropped commit changes what counts as landed. The hold names the owner and
the command: `tillr drain accept-rewrite` maps an unmapped landing onto an ancestor of `main`. The
hold rule's mover is the quoted line "the owner; restore the named landing to main, or map its
replacement with tillr drain accept-rewrite" in
[`holdrules.go`](../../tools/tillr-drain/holdrules/holdrules.go).

### 6.8 Rule 8: noticing does not depend on anyone being awake

This rule needs [`tillr-watch.md`](./tillr-watch.md): alerts go to the foreground agent, and the
utility executes no remedy of its own. This design adds the first rule set and one requirement on
delivery.

| Rule | Fires when | Default |
| :--- | :--- | :--- |
| Trunk red, no repair in flight | The proof is red and no revert or fixer for that check is running | 10 minutes |
| Repair refused | A revert or fixer for the same check is refused a second time | 2 refusals |
| Slot idle with work ready | A worker slot is free while a ready item waits | 2 minutes |
| Held on a person | An item's mover is the operator or the owner | 15 minutes |
| Loop exit | The loop ends without an operator stop | immediately |
| Backend closed | A seat's backend refuses for quota or credentials | immediately |
| Same class twice | A second incident is filed under a class that already has one this run | 2 incidents |

**Delivery.** When a shared subscription reaches its limit the operator and the designers stop, and
for now that stop is accepted ([OQ-UD3](#OQ-UD3)): no route to the owner outside the session is
built. Rules 2 and 7 make the stop affordable, because a red trunk and a mechanical hold do not wait
for the operator. The ruling is revisited after the unattended night of
[Section 8](#8-what-done-looks-like).

**The operator's part, which is procedure and not mechanism:**

- A second incident of one class is reported to the owner as a statement about the class.
- An item's words separate what was observed from what the operator concludes.
- A record entry's time is read from the clock by the command that writes the entry.
- A report says only what has been done.

## 7. Every incident class and what stops it

| Class | Rule | Tier | Scenario or check that fails if it returns |
| :--- | :--- | :--- | :--- |
| A drain change lands red | 1 | structure | The gate plan for a drain change holds the drain suite in the landing tier |
| A repair refused for checks it cannot affect | 2, 3 | structure | A fixer lands its repair while a fork check is red |
| A red verdict discarded by a later landing | 2 | structure | A landing arrives after a red verdict; the repair is still filed |
| A revert abandoned because the trunk moved | 2 | structure | The trunk moves during a revert's check; the revert lands |
| A held item keeps a lane | 4 | structure | Four items wait on a closed backend; a fifth, needing another backend, starts |
| One failure stops every start | 5 | structure | One staging failure; a second ready item starts in the same pass |
| A roll holds starts | 6 | structure | A roll is pending; a ready item starts |
| An item's error ends the loop | 5 | structure | Fault injection at pin and at preserve; the loop keeps running |
| A landed blocker's waiter stays held | 7 | structure | A blocker lands; its waiter is ready in the next pass |
| A rebase refused by upstream's own tests | 3 | structure | A rebase's gate runs only the packages the patches touch, with no credentials |
| A worker stopped at a path list | 7 | structure | A worker changes a path its item never listed and is not stopped; a path on the deny-list is refused with the entry named |
| Repeated refusals wait for the operator | 7 | structure | Repeated refusals go to the designer step |
| A landed design's build is not filed | 7 | structure | A landed design's ready decisions are queued |
| The subscription runs out | 2, 7 | accepted for now | None for the stop itself ([OQ-UD3](#OQ-UD3)); the scenarios of rules 2 and 7 run with no operator present |
| An alarm wakes nobody | 8 | signal | A closed backend raises an alert to the foreground agent |
| Operator conduct | 8 | procedure | None; the "same class twice" alert is its backstop |

## 8. What done looks like

One night, checked the next morning from the store and the log:

- Eight hours with no operator action and no owner action.
- The trunk never red for longer than one revert takes, and green at the end.
- No free worker slot while a ready item waited for longer than 2 minutes.
- No item held on the operator.
- No loop exit.

Until that night has happened, the claim "the drain repairs itself" is not made in any report.

## 9. What this does not change

- The short gate for every change outside the drain's own code.
- The landing transaction and its principles.
- The ruling against attempt counts, time limits that fail work, and fallbacks to another model.
- Who designs: only an item's designer changes its definition.
- The alerting utility's design, which stays in [`tillr-watch.md`](./tillr-watch.md).

## 10. Alternatives

| Alternative | Verdict |
| :--- | :--- |
| Change the drain by hand until it is stable | Rejected. It removes the only repeated test the landing path gets |
| Run the whole check program before every landing | Rejected. It gives up the short gate for every change to cure a problem that drain changes cause |
| Keep landing first and make the revert faster | Rejected as the only measure. It is rule 2, and it is needed, but each red landing still costs a proof, a revert and every item that built on it |
| A second, always-on operator session | Rejected. It doubles the party doing by hand what rule 7 says the drain should do |
| Fix each incident as filed and add no rule | Rejected. It removes one cause at a time and leaves the class |

## 11. Risks

| Risk | Mitigation |
| :--- | :--- |
| Rule 1 slows drain changes so much that repairs queue up | Suites run outside the landing lock; a repair for a red trunk is a fixer and keeps the fixer's gate |
| The scenario suite becomes slow or flaky and blocks every roll | Scenarios may not sleep or read the clock; a red scenario is a trunk failure with a first-red search like any other |
| Unlimited worktrees fill the disk | A reported count, a disk check before each new worktree, removal on land, close or preserve |
| With no fence a worker wanders outside its item | The deny-list refuses a committed change to shared state before the gate ([`D-VO14`](./tillr-vocabulary.md#D-VO14)), and the judge names any changed path the item's words do not account for |
| Alert rules fire constantly during the build-out | Thresholds are data the operator tunes, as [`tillr-watch.md`](./tillr-watch.md) designs |

## 12. Order of work

1. **Rule 2, the revert that cannot be outrun**, and **rule 5, fault injection**: a trunk that stays
   red and a loop that stops are the two ways a night is lost.
2. **Rule 7's fence retirement.**
3. **Rule 1**, drain changes prove their own suites before landing.
4. **Rule 6, the scenario suite.**
5. **Rule 3, outside inputs.**
6. **The rest of rule 7.**
7. **Rule 8**, once the delivery designed in [`tillr-watch.md`](./tillr-watch.md) is built.
8. **Rule 4**, last because it changes the most and because rules 5 and 7 remove most of what makes
   lanes scarce.
9. **The unattended night** of [Section 8](#8-what-done-looks-like).

## 13. Open Questions

None. Each ruled question is a row in [the ledger](#14-decision-ledger).

## 14. Decision Ledger

The `OQ-UD` rows are the owner's rulings. The `D-UD` rows are the designer's decisions, each the
owner's to overturn. A dash in **Built** means not verified as built.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-UD1"></a>[OQ-UD1](#OQ-UD1) | Owner ruling: a change to the drain's own code waits for the drain's own suite before it lands | 2026-11-21 | [Rule 1](#61-rule-1-the-drains-code-proves-its-own-suites-before-it-lands) | — |
| <a id="OQ-UD2"></a>[OQ-UD2](#OQ-UD2) | Owner ruling: the number the owner sets limits running workers; worktrees are not counted | 2026-11-21 | [Rule 4](#64-rule-4-workers-are-limited-worktrees-are-not) | No: the loop takes `--lanes N` |
| <a id="OQ-UD3"></a>[OQ-UD3](#OQ-UD3) | Owner ruling: for now, accept that the operator and designers stop when the subscription runs out | 2026-11-21 | [Rule 8](#68-rule-8-noticing-does-not-depend-on-anyone-being-awake) | — |
| <a id="OQ-UD4"></a>[OQ-UD4](#OQ-UD4) | Withdrawn: the owner retired the fence on 2026-11-19, so nothing was left to rule. What is owed is that ruling's build | 2026-11-21 | [Rule 7](#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) | — |
| D-UD1 | A revert is a landing and is never abandoned because the trunk moved | 2026-11-21 | [Rule 2](#62-rule-2-a-red-trunk-is-repaired-by-a-step-that-cannot-be-outrun) | — |
| D-UD2 | A check with an input outside the tree reports and files work; it does not refuse a candidate | 2026-11-21 | [Rule 3](#63-rule-3-only-the-candidates-tree-can-refuse-a-candidate) | — |
| D-UD3 | The fork gate tests the packages that differ between the upstream base and the fork head | 2026-11-21 | [Rule 3](#63-rule-3-only-the-candidates-tree-can-refuse-a-candidate) | — |
| D-UD4 | The loop ends only on an operator stop or an unreadable store; fault injection at every effectful step enforces it | 2026-11-21 | [Rule 5](#65-rule-5-one-items-fault-holds-that-item-and-nothing-else) | — |
| D-UD5 | A drain build is admitted to the live queue by the scenario suite, and a roll holds no start | 2026-11-21 | [Rule 6](#66-rule-6-a-drain-build-runs-the-live-queue-only-after-it-passes-the-scenarios) | — |
| D-UD6 | An incident is closed only when its scenario exists | 2026-11-21 | [Rule 6](#66-rule-6-a-drain-build-runs-the-live-queue-only-after-it-passes-the-scenarios) | — |
| D-UD7 | A hold reason whose mover is the operator declares the decision it reserves, and a test reads the table | 2026-11-21 | [Rule 7](#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) | — |
| D-UD9 | A refusal about the report amends the report and does not rebuild the candidate | 2026-11-21 | [Rule 7](#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves) | — |
| D-UD10 | The first alert rules and their defaults | 2026-11-21 | [Rule 8](#68-rule-8-noticing-does-not-depend-on-anyone-being-awake) | — |
| D-UD11 | Map a rewritten trunk landing to its unique patch-identical commit on `main` at pass start; with no unique match, hold `blocker-landing-not-on-trunk` on the owner and let `tillr drain accept-rewrite` map it. Designer's decision; the owner may overturn it | 2026-11-21 | [Rewritten blocker landings](#rewritten-blocker-landings) | Yes: `drain.trunk-rewrite-absorbed` and the hold rule in `tools/tillr-drain` |
