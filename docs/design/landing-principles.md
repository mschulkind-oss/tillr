---
title: "Landing principles — the durable rulings, and what enforces each"
status: accepted
stage: CURRENT
tags: [tillr, landing, principles, rulings, library]
summary: "Seventeen landing principles, each with the measurement it rests on, what enforces it, what does not, and the program it binds. A principle whose enforcement line says 'nothing' is a stated intent, not a rule."
---

# Landing principles

This is the library, not another ruling. Each entry is one durable principle, the measurement it
rests on, **what enforces it**, what is not enforced, and a scope line naming the program it binds.
A principle with nothing on the enforcement line is a stated intent; saying so keeps this file from
reading as a description of the machinery.

Read this to check a proposed change against what is settled.

## Scope: which lander a principle binds

The tree has one lander: [`tools/tillr-land`](../../tools/tillr-land/). It is the only program that
moves the trunk ref for a candidate. A scope line takes one of three shapes:

- **`the landing transaction`** — the single publish path, `tools/tillr-land`.
- **`not lander-scoped`** — the principle binds the dispatch side, `tillr-drain` and `tillr-loop`,
  and says nothing about the lander.
- **a named program** — the entry binds that program and no other.

---

## 1. A refusal must be a verdict

Anything that records a candidate as failed must be able to name what **the candidate** did wrong. If
it cannot, it is a statement about the machinery, and it must not consume the candidate's standing,
park it, or hold it out of the queue.

*Basis:* in one day's landing records, 53 of 53 refusals carried a cause meaning "the gate has no
opinion", and none was a gate verdict: each was a patch-id mismatch, a git error or a regeneration
failure.

*Enforced by:* [`tools/tillr-land`](../../tools/tillr-land/). No refusal reason means "unverified":
`Reasons()` in [`landing.go`](../../tools/tillr-land/land/landing.go) is a closed set
(`verdict-mismatch`, `verdict-failed`, `verdict-error`, `trunk-worktree-uncommitted`,
`patch-rewind`, `patch-at-head`, `item-not-landable`, `trunk-rewritten`, `already-on-trunk`),
pinned by `TestTheRefusalReasonsAreTheRegisteredSet` in
[`conformance_test.go`](../../tools/tillr-land/land/conformance_test.go). `squash-conflict`
(`ResultSquashConflict`) is its own outcome, not a refusal. Refusals that are not about the
candidate (`verdict-error`, `trunk-worktree-uncommitted`, `trunk-rewritten`, `already-on-trunk`)
leave the verdict standing and pin `refs/tillr/runs/<run_id>` at the head. On the dispatch side,
`trunk-rewritten` and `red-cap` are holds that retain the already-gated candidate rather than
failing it.

*Scope:* **the landing transaction.**

## 2. Re-application preserves the change, not the patch

When a candidate is re-applied onto a newer base, the resulting **diff** is expected to differ. A
check that demands the patch survive measures the base, not the author, and its failure rate rises
with how busy the trunk is.

*Basis:* a patch-identity check refused one candidate for a day because seven commits landed in the
directory it changed after its branch point, and recorded two different replayed patch-ids for its
one authored diff in a single pass.

*Enforced by:* `Trunk.Squash` in [`trunk.go`](../../tools/tillr-land/land/trunk.go) three-way merges
the candidate's `base..head` change onto the current tip
(`git merge-tree --write-tree --merge-base=<base> <trunk> <head>` plus `git commit-tree`) and never
compares patch identity. A `landed_tree` that differs from `gated_tree` on `landings` is the normal
outcome of a concurrent landing.

*Scope:* **the landing transaction.** [Section 3](#3-gate-what-will-be-the-tip-once) is the same
mechanism from the throughput side.

## 3. Gate what will be the tip, once

The gate runs once. If the trunk moves during a gate, the already-gated change is re-applied onto
the new tip and **landed without a second gate**, so throughput does not depend on the rate of
incoming change.

*Basis:* re-gating on every trunk move makes landing a function of everyone else's velocity — the
inverted polarity of principle 2. The owner's stated model is a protected branch that does not
require candidates to be up to date before they merge.

*Enforced by:* [`tools/tillr-land`](../../tools/tillr-land/). The gate runs outside `land.lock`.
Under the lock, `Land` ([`landing.go`](../../tools/tillr-land/land/landing.go)) calls `Trunk.Squash`
to merge `base..head` onto `trunkBefore` through plumbing with no working tree, commits with the
scope-checked `LandingIdentity` and the head's own message, and `Trunk.Move` advances the trunk
guarded against `trunkBefore`. A landed commit carries no `Tillr-Item:` trailer
([`queue-persistence-ruling.md`](queue-persistence-ruling.md#3-landing-receipts-without-commit-trailers)).
`Land` records the outcome in the `landings` table with both trees: `gated_tree` is the head's tree
the verdict bound, `landed_tree` is the tree the trunk published.

- **A squash that conflicts** is recorded with `result = "squash-conflict"` and `conflict_paths`,
  and pins the head: never landed and never re-gated, because the conflict is about the candidate.
- **A change the trunk already holds** is refused as `already-on-trunk`
  ([Section 10](#10-supersession-is-a-verdict-not-a-stall)).
- **A retry after a crash** between the trunk move and the receipt does not publish again: the
  lander points `refs/tillr/landed/<run_id>` at the squash commit before the move, and a retry that
  finds it in the trunk's history records it. This is not a general once-only guarantee; the
  [lander README](../../tools/tillr-land/README.md#state-invariants) states the cases.
- **Fork delivery branches** are not the trunk: the lander publishes the exact built fork head
  ([fork delivery](../../tools/tillr-land/README.md#fork-delivery)).

Pinned by `TestAMovedTrunkSquashesTheChangeOntoTheNewTip` and
`TestASquashConflictIsRecordedAndPinsTheHead` in
[`main_test.go`](../../tools/tillr-land/main_test.go).

*Scope:* **the landing transaction.**

## 4. Every outcome is state-changing

A pass may not return a candidate to the queue in the same state, against the same tip, with the
same authored tip. Every outcome is landed, exited, judged, or blocked by an open item. A repeat is
a defect by definition.

*Basis:* one candidate was isolated four times in a day with the same reason and detail.

*Enforced by:* nothing as a whole. The acceptance test is a drain cycle with zero repeated
`(candidate, tip, reason)` isolations; the repeat detection of
[Section 6](#6-a-budget-escalates-it-does-not-loop) covers worker and design endings.

*Scope:* **not lander-scoped.**

## 5. Only verdicts charge

Parks, provider failures, no-candidate lanes and machinery faults never consume an item's standing.
The rule holds without a counter: a worker's ending is never counted, and numeric repair thresholds
are rejected (owner ruling `D-JL10`, 2026-11-17, in
[`tillr-judge-and-lifecycle.md`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)).

*Basis:* ten candidates sat with spent attempt budgets and none had a judged repair tree; four had
spent the whole budget on provider failures.

*Enforced by:* [`tillr-drain`](../../tools/tillr-drain/). A refused run, a run with no commit, a
fence escape, a refused landing and a squash conflict
[return to the worker](../../tools/tillr-drain/README.md#a-refused-run-goes-back-to-its-worker) with
their evidence, uncounted (`TestAFailedCandidateGoesBackToItsWorkerUntilItRepeats` in
[`loop_test.go`](../../tools/tillr-drain/drain/loop_test.go)). An infrastructure failure closes that
backend and walks to the next open one
([recorded backend availability](../../tools/tillr-drain/README.md#recorded-backend-availability)).

*Scope:* **not lander-scoped.** The lander writes `landings` and keeps no attempt counter.

## 6. A budget escalates, it does not loop

Work that keeps failing escalates; it never stalls silently and never loops. Escalation is triggered
by an **identical repeat** or an explicit **named obstacle**, not by a number of attempts: neither
worker runs nor design runs have numeric attempt thresholds (owner rulings `D-JL9` and `D-JL10`,
2026-11-17, in the
[judge and lifecycle decision ledger](./tillr-judge-and-lifecycle.md#decision-ledger)).

*Enforced by:* [`tillr-drain`](../../tools/tillr-drain/). A refused worker run returns to its worker
until an ending repeats an earlier ending of the same producer, cause and normalized evidence
(`repeatedEnding` in [`returned.go`](../../tools/tillr-drain/drain/returned.go)), or until the worker
reports a named obstacle. Either routes to the `design` seat. The owner is reached only by a design
run's `NEXT-ACTOR: owner` objection or a repeated non-gate design ending (`designRepeatOf` in
[`design_repeat.go`](../../tools/tillr-drain/drain/design_repeat.go);
`TestBlockedReportRoutesOnceToDesignThenOwner`). No authority below the owner grants an exception or
answers an owner question.

*Not enforced:* the four-rung dispatch and exceptions ledger of
[the escalation ladder](escalation-ladder.md).

*Scope:* **not lander-scoped.** The lander records `squash-conflict` or a refusal; routing is the
dispatch side's.

## 7. A surface that hides parked work is a false report

The queue surface must show what is **not** moving, not only what is. A candidate held out of the
pass is visible as held, with the reason, on the surface used to judge the queue's health.

*Basis:* a queue surface drew parked candidates under a "rejected" heading; twelve of fifteen
entries read as refused work when nothing had judged most of them.

*Enforced by:* [`tools/tillr-view`](../../tools/tillr-view/) and
[`holdrules.go`](../../tools/tillr-drain/holdrules/holdrules.go). `tillr-view waiting`
[reports why each unlanded item is not moving](../../tools/tillr-view/README.md#why-each-unlanded-item-is-not-moving),
with its next actor and required action. Every exported drain hold reason must have a display rule
(`TestEveryReasonConstantHasAHoldRule` in
[`hold_rules_test.go`](../../tools/tillr-drain/drain/hold_rules_test.go)), and every rule renders
with a class, reason and mover (`TestWaitingEveryDrainReasonHasARule` in
[`waiting_internal_test.go`](../../tools/tillr-view/waiting_internal_test.go)).

*Scope:* **not lander-scoped.** The lander keeps no candidate queue.

## 8. A gate that cannot answer must say which

"Could not run" and "ran and said no" are different facts and must not share a class. A suite that
crashes, stalls or produces no extractable verdict means the gate's own integrity is unknown; a
suite that names failing assertions is a verdict about the tree, and routes like one.

"Times out" means stopped making progress, never ran longer than expected (owner ruling
[`D-GR15`](../reference/gate.md#why-its-this-way), 2026-11-17). A time limit on a check is a target
that is measured and reported. A check still making progress is not stopped because a clock passed.

*Basis:* a single stale assertion in the gate's own test suite was reported as "not verified"
rather than as a verdict, so a whole batch was refused instead of the offender being isolated.

*Enforced by:* [`tools/tillr-gate`](../../tools/tillr-gate/). A check that could not run and a check
that failed are separate outcomes with separate exit codes. The progress observer in
[`watchdog.go`](../../tools/tillr-gate/watchdog.go) produces `stalled` when a check shows neither
output nor CPU progress ([budgets and the stall watchdog](../reference/gate.md#budgets-and-the-stall-watchdog)).
The drain retains a first stall as `gate-stall-rerun` and holds a second on the same head tree as
`gate-stalled`. The lander refuses a verdict whose result is `error` with `ReasonVerdictError`,
never as a failure of the candidate.

*Scope:* **the landing transaction** and the gate.

## 9. Fix the machinery; never hand-carry the work

When the queue cannot land a candidate for a reason that is not about the candidate, the answer is a
deduplicated repair item, not a landing by hand past the queue's own proof. A hand-carried landing
leaves the machinery broken for the next candidate.

*Enforced by:* nothing. The owner has ruled that no excepted landing is available while a machinery
item is open.

*Scope:* **not lander-scoped.** It governs the operator and the drain.

## 10. Supersession is a verdict, not a stall

Work whose change is already on the trunk exits the queue on that finding. "Already present" is a
result, not a state to wait in.

*Enforced by:* the lander and the drain. When the trunk already holds the candidate's change, the
three-way merge answers the tip's own tree; `Trunk.SquashWithMessage` answers an empty squash with
no commit, and `Land` records `result = "refused"`, `reason = "already-on-trunk"` and moves nothing
(`TestAnEmptySquashIsRefusedAsAlreadyOnTrunk` in
[`idempotent_test.go`](../../tools/tillr-land/idempotent_test.go)). `tillr-drain` gives that refusal
its own branch (`settleAlreadyOnTrunk` in
[`already_on_trunk.go`](../../tools/tillr-drain/drain/already_on_trunk.go)): it records the passing
verdict, moves the item to `resolved-elsewhere`, and releases the lane. The item is never held as
`land-refused`.

*Scope:* **the landing transaction and the drain.** The lander's `patch-at-head` and `patch-rewind`
refusals concern `refs/heads/patch/*` delivery branches, not this case.

## 11. A failure is diagnosed, not waited out

No mechanism may re-run work, or hold work for later re-running, on the ground that time has passed.
A retry, a retry budget, a cooldown and a bench-with-expiry are one mechanism in four spellings.
Work is re-admitted only against a **named change**: an input the failure implicated has changed and
the change is verified, or the failure itself stated a clearing condition (a `Retry-After`, a quota
reset) and that condition was checked. A failure that cannot name a clearing condition is diagnosed
before anything it blocks runs again. Determinism is established by observation: the same failure
repeats under unchanged inputs, or strikes items that share nothing but the machinery. The test for
any proposed failure mechanism is one question: *what will be different on the next run, and how
will the system know?* A mechanism whose answer is "time passed" is refused.

*Basis:* a persisted 30-minute cooldown benched 55 items behind a model identifier that could not
resolve; a re-admitted item failed again within eight seconds on the same configuration error, and
383 of 1,083 child logs carried that one error.

*Enforced by:* partly; no test asserts the principle as a whole. `tillr-drain` does not hold a
refused worker run: it returns the refusal to the worker with its evidence, and nothing in that path
waits on a clock. An ending whose producer, cause and normalized evidence equal an earlier ending of
the item goes to the design seat with both records (`repeatedEnding`); a squash conflict repeats
only when its paths and normalized hunks match (`conflictEndingKey`). A repeated design ending goes
to the owner (`designRepeatOf`). A named change is an event that names the item: an operator's
retry, a design answer, or the recovery of a killed run. Infrastructure failures are handled by
[recorded backend availability](../../tools/tillr-drain/README.md#recorded-backend-availability).

*Not enforced:* the failure fingerprint store, the fleet trip and the drain's own probes specified
in [the cooldown-removal design](./failure-diagnosed-not-waited-out.md).

*Scope:* **not lander-scoped.** The lander dispatches no lanes and keeps no retry state.

## 12. A principle cites the measurement that produced it

Every entry here states the measurement or ruling that made it non-negotiable. A principle without
one is a preference, and preferences lose to whatever is convenient on the day.

*Enforced by:* this file's shape. An entry with no basis or no enforcement line is visibly
incomplete.

*Scope:* **not lander-scoped.** The scope line is part of the shape: an entry that does not say
which program it binds is as incomplete as one with no measurement.

## 13. A held state names a mover that can still act

A state that holds work out of the queue must name the act that will next change it, and that act
must be one that can still happen. A mover that cannot act — a repair lane that does not exist, a
session that is gone, a person nobody asked — is a hold with no exit. When the named mover cannot
act, the state either installs one that can or hands the work to a state whose mover can, and the
surfaces say which.

*Basis:* ten items sat in progress for 15 to 22 hours behind a mover naming a repair lane that did
not exist; in a later measurement, five of seven design conflicts awaited a design run that had
already ended.

*Enforced by:* `tillr-drain`, hold by hold:

- **A claim whose run never launched** is released and its item dispatched again
  (`releaseUnlaunchedClaims` in
  [`unlaunched_claim.go`](../../tools/tillr-drain/drain/unlaunched_claim.go)).
- **A design conflict awaiting an ended design run** is raised again for the design seat by how the
  run ended, never by a count
  ([judge and lifecycle design, Section 4.3](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9);
  `settleUnansweredDesignRuns` in
  [`design_unanswered.go`](../../tools/tillr-drain/drain/design_unanswered.go)).
- **An interrupted triage run** is triaged again rather than sent to the owner
  (`TestAnInterruptedTriageRunIsTriagedAgainNotSentToTheOwner`).
- **A lane with uncommitted files** under a pinned candidate is refused for that lane alone as
  `lane-holds-uncommitted-work` (`ReasonLaneUncommittedWork` in
  [`lane_rotation.go`](../../tools/tillr-drain/drain/lane_rotation.go)).
- **A committed design answer** is read by its block's id and section; a hold that misread it is
  raised again (`settleMisreadDesignAnswers` in
  [`design_answer.go`](../../tools/tillr-drain/drain/design_answer.go)).
- **An owner hold over a design run's objection** has an owner's command,
  `tillr drain retry --item <id> --run <design run>`. Every retry refusal names the command that
  would be accepted, or says none exists and who moves (`authorizeDesignRetry` in
  [`design_retry.go`](../../tools/tillr-drain/drain/design_retry.go);
  `TestEveryRetryRefusalNamesTheAcceptedCommandOrTheMover`).

*Not enforced:* the gate's trunk-already-red replay cannot judge a fast-gate check id, so a design
run refused for the trunk's sake returns to the design seat until the trunk is fixed. The remaining
drain-side work is under
[`D-JL14`](./tillr-judge-and-lifecycle.md#47-offered-commits-own-candidate-lanes-unlaunched-claims-and-named-hold-movers-d-jl14).

*Scope:* **not lander-scoped.** This is the held-state half of
[Section 11](#11-a-failure-is-diagnosed-not-waited-out): that section forbids waiting on time, this
one forbids waiting on an actor that is not there.

## 14. Convict a run on what it did, not on what happened while it ran

A run is convicted only on evidence that the run itself made the change. A movement that merely
happened inside the run's window — a worktree added or removed, a branch, a tag, a fetch, a stash, a
hand commit, an operator's pin — can be the owner's or another agent's. It is recorded as
information and never holds an item or stops the loop. Owner ruling, 2026-11-17: ordinary git use
and ad hoc commits must not break the drain.

*Basis:* two runs that made no edits were held for a boundary violation whose cause was the loop's
own rolling checkout appearing during their window.

*Enforced by:* `CompareRunObserved` in
[`boundary.go`](../../tools/tillr-drain/drain/boundary.go). It convicts a write into a watched, idle
sibling lane worktree, and the run's own lane worktree going away. It only observes a rogue ref
inside tillr's namespaces, any other worktree's registration, and a main-checkout branch switch;
observations go on the run's `boundary.check` event and the check stays clean. The
[common deny-list](./tillr-vocabulary.md#51-the-work-and-its-statement) refuses a candidate whose
committed change touches protected paths or item declarations. Pinned by
`TestOrdinaryGitUseDuringARunNeverConvictsIt`.

*Not enforced:* a worker whose only escape lies outside the lanes is not stopped. A rogue ref or a
stray worktree is named for an operator, and nothing acts on it.

*Scope:* **not lander-scoped.** The lander never convicts a run.

## 15. Land the smallest functional change

A change lands as the smallest functional change possible, as a human engineer would land it. Part
of a design or a request may land alone, and that is the healthier outcome: a small change is easier
to review and test, and the work moves faster (owner ruling, 2026-11-21).

Two consequences follow. A change is not held back because a named case of its request is unmet: the
case follows as its own change. And "functional" limits "smallest": what lands works alone and
leaves the trunk no worse.

*Basis:* across 1,049 recorded landings the median changed 2 files and added 75 lines; of 95 landed
planned-build items, the 19 above the 95th percentile in size averaged 3.3 verifier refusals and the
other 76 averaged 1.1.

*Enforced by:* partly. The design seat can divide a request into parts that are filed as their own
items (`tillr drain split`, `FileSplit` in
[`split.go`](../../tools/tillr-item/item/split.go)).

*Not enforced:* nothing reads the size of a request, a plan or a candidate; a verifier still refuses
a whole candidate for one unmet case; and landing the accepted part of an oversized candidate, as
[the split design](./tillr-item-split.md) specifies, is not built. Beyond the split statement this
entry binds whoever files a request, writes a design or plans a build.

*Scope:* **not lander-scoped.** The lander lands one squashed commit per item whatever its size.

## 16. What the operator can settle is settled inside tillr

A problem the operating agent could solve without the owner does not go to the operating agent. It
goes to a similarly responsible agent, process or item inside tillr, which is given the evidence the
operating agent would have had to read (owner ruling, 2026-11-21). The operating agent is the
foreground session that watches the drain and acts as operator.

Two consequences follow. The operator is not a mover for any hold: a hold only the operator can end
waits for a check-in, which by
[Section 13](#13-a-held-state-names-a-mover-that-can-still-act) is a mover that cannot act when the
session is gone. And the test for reaching the owner is whether the decision is the owner's, never
whether a seat inside tillr failed or took long.

*Basis:* in one day the store recorded 34 items closed or set to landed by the operator; 30 of them
were settled without any word from the owner, and one of the hand finishes was wrong.

*Enforced by:* nothing as a whole. The drain releases a wait when its blocker lands and releases a
trunk obstacle when the named checks are green.

*Not enforced:* `tooling` and `seat` obstacles on a filed request name the operator as mover
(`rg -n 'Mover = "operator"' tools/tillr-drain/obstacles/read.go` finds three assignments), and an
item is closed or set to landed on a reason typed by hand.
[The design](./operator-decisions-inside-tillr.md) names the seat or process that takes each kind of
hand action.

*Scope:* **not lander-scoped.** The lander holds no item and asks no one.

## 17. Disable a flaky check, file its repair, and count it neither green nor blocking

A check that both passes and fails on the same declared inputs is flaky. The response is to disable
that check, file an item to re-enable that named check correctly, and keep it from blocking any
unrelated candidate or holding the trunk red (owner ruling `D-GR21`, 2026-11-21, in
[the gate reference](../reference/gate.md#a-contradicted-check-is-disabled)).

Two consequences follow. A disabled check is excluded from the pass, remembered and fail counts and
listed apart as `disabled`: the trunk is green because the check is not included, while the check
itself is not green until its repair item lands. And the filed item names the disabled check and is
done only when the check is back in the gate program and no longer contradicts itself.

*Basis:* teaching each consumer of check results about `disabled` separately produced a candidate of
111 files that its verifier refused eleven times, because separate sites still credited a
contradicted check green or turned `disabled` into `error`. One classifier is the remedy.

*Enforced by:* partly.

- **One classifier.** Classification sites route through
  [`classify.go`](../../tools/tillr/checkresult/classify.go) (`Classify`, `Aggregate`), guarded by
  `TestClassificationScanGoSites`, `TestClassificationScanDrainSites` and
  [`classification-scan.test.py`](../../tools/gate/classification-scan.test.py). `Aggregate` leaves
  `disabled` out of both failing and unknown outcomes.
- **Recorded episodes.** `tillr-gate classify` and `tillr-gate enable` persist a disabled marker on
  an exact-fingerprint contradiction and keep answering `disabled` until `enable` clears it. The run
  summary counts `disabled` apart
  (`TestDisabledCheckExcludedFromPassAndFailCountsAndListedAsDisabled`).
- **Filed repair.** `sweepDisabledMarkers` in
  [`flaky_repair.go`](../../tools/tillr-drain/drain/flaky_repair.go) files a `flaky-<check-id>` item
  to re-enable the check, deduplicated while open, and runs `tillr-gate enable` once it lands.

*Not enforced:* live `tillr-gate run` still marks a contradicted check `flaky` in `annotateMemory`
([`memory.go`](../../tools/tillr-gate/memory.go)) and executes it, and the drain does not pass
`--prove-disabled` when gating a repair candidate (`rg -n 'prove-disabled' tools/tillr-drain`
returns nothing). The remaining slices are in
[the classification inventory](../reference/gate.md#result-classification).

*Scope:* **not lander-scoped.** It binds `tillr-gate`, `tillr-drain` and `tillr-loop`. The lander
reads only the top-level `gate.verdict` result and keeps no check state.

---

## Keeping this read

[`AGENTS.md`](../../AGENTS.md) links this library from the repository guide, and the rulings link
here, so a reader who arrives from a ruling finds the principle it established.

When a ruling is overturned, the principle is amended or retired here **in the same commit**. A
principle that stops being true and stays in this file is a rule a reader cannot tell is dead.
