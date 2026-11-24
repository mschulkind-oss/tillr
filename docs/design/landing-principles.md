---
title: "Landing principles — the durable rulings, and what enforces each"
status: accepted
stage: CURRENT
verified: 2026-11-23
tags: [tillr, landing, principles, rulings, library]
summary: "Seventeen landing principles, each with the measurement that produced it, the thing that enforces it today, and the one lander it binds. Principles 5 and 6 hold without a counted budget under owner ruling D-JL10 (worker endings are never counted; identical repeats are diagnosed at design). A principle whose enforcement column says 'nothing' is a claim, not a rule, and is listed as such rather than assumed; a principle that is not lander-scoped says so and names the dispatch program it binds instead."
---

# Landing principles

This is the library, not another ruling. Each entry is one durable principle, the measurement that
produced it, the ruling that owns the detail, the column that matters — **what enforces it today** —
and a scope line naming the one lander it binds, or saying it is not lander-scoped. A principle with
nothing in the enforcement column is a stated intent; saying so is what keeps this file honest
instead of reading as a description of the machinery.

Read this to check a proposed change against what has already been settled.

## Scope: which lander a principle binds

The tree has one lander: [`tools/tillr-land`](../../tools/tillr-land/). It is the only program that
can move the trunk ref for a candidate, and the only program a scope line may name when it binds a
lander.

A scope line therefore takes one of three shapes:

- **`the landing transaction`** — the single publish path, which is `tools/tillr-land` today.
- **`not lander-scoped`** — the principle binds the dispatch side, which is `tillr-drain` and
  `tillr-loop`, and says nothing about the lander.
- **a named program** — the entry binds that program and no other, and the line says which.

"Meets it" and "does not" are measurements of the tree, read in the source on 2026-11-19, not
intentions; where the lander does not meet an entry, the line names the gap that remains.

---

## 1. A refusal must be a verdict

Anything that records a candidate as failed must be able to name what **the candidate** did wrong. If
it cannot, it is a statement about the machinery, and it must not consume the candidate's budget,
park it, or hold it out of the queue.

*Measured 2026-11-02:* 53 of 53 refusals that day carried `cause=unverified`, and not one was a gate
verdict — every one was a patch-id mismatch, a git error, or a regeneration failure. A word meaning
"the gate has no opinion" was being recorded against fourteen candidates as though they had failed.

*Enforced by:* [`tools/tillr-land`](../../tools/tillr-land/) for the landing transaction. No
refusal reason means "unverified": in
[`landing.go`](../../tools/tillr-land/land/landing.go), `Reasons()` is a closed ten-value set
(`verdict-mismatch`, `verdict-failed`, `verdict-error`, `trunk-worktree-uncommitted`, `trunk-rewritten`, `patch-rewind`,
`patch-at-head`, `item-not-landable`, `already-on-trunk`, `red-cap`), pinned by `TestTheRefusalReasonsAreTheRegisteredSet` in
[`conformance_test.go`](../../tools/tillr-land/land/conformance_test.go), and `squash-conflict`
(`ResultSquashConflict`) is its own outcome rather than a refusal. `trunk-rewritten` is
an infrastructure hold: the drain retains the already-gated candidate, records both tips,
and waits for restored history without a failing candidate verdict or another gate.
`red-cap` is likewise a repository hold: the configured limit on landings or minutes
past the green pointer has been reached. Its record names the red checks and open
fixer, keeps the already-gated candidate, and reopens landing after proof advances.
The fixer itself is exempt; a pending proof with no known red check says so explicitly.
A candidate already present on the trunk settles before the cap is applied. Held
candidates are revisited when the trunk tip changes, so superseded work can exit
without waiting for the green pointer. If retained candidates occupy every offered
lane, one separate fixer worktree provides repair capacity without discarding them.

*Scope:* **the landing transaction.** [`tools/tillr-land`](../../tools/tillr-land/) meets it: a trunk
move between the gate and `pass` is squashed onto the moved tip by `Trunk.Squash`
([`trunk.go`](../../tools/tillr-land/land/trunk.go)) and recorded on `landings` with both
`gated_tree` and `landed_tree`, never refused on patch-id or base drift. Every refusal in `Reasons()`
names a specific fact about the verdict, the patch ref, uncommitted worktree paths, the
owner's closed/resolved item disposition, a trunk that already carries the change, or the configured unproved-range cap, and
non-candidate refusals (`verdict-error`, `trunk-worktree-uncommitted`, `already-on-trunk`, `red-cap`) leave the
verdict standing and pin `refs/tillr/runs/<run_id>` at the head.

## 2. Re-application preserves the change, not the patch

When a candidate is re-applied onto a newer base, the resulting **diff** is expected to differ. A
check that demands the patch survive is measuring the base, not the author, and its failure rate
rises with how busy the trunk is — it punishes the condition the mechanism exists to handle.

*Measured 2026-11-02:* one candidate was refused for a day because seven commits landed in
the directory it changed after its branch point. Two different replayed patch-ids were recorded
for its one authored diff in a single pass.

*Enforced by:* [`tools/tillr-land`](../../tools/tillr-land/):
[`Trunk.Squash`](../../tools/tillr-land/land/trunk.go) three-way merges the candidate's `base..head`
change onto the moved tip (`git merge-tree --write-tree --merge-base=<base> <trunk> <head>` plus
`git commit-tree`) and never compares patch identity, so a replayed diff that differs — and a
`landed_tree` that differs from `gated_tree` on `landings` — is the normal outcome of a concurrent
landing and not a defect.

*Scope:* **the landing transaction.** [`tools/tillr-land`](../../tools/tillr-land/) meets it: every trunk
landing runs `Trunk.Squash` to three-way merge the candidate change onto the current tip and records
both trees on `landings` without inspecting `git patch-id`.
[Section 3](#3-gate-what-will-be-the-tip-once) is the same mechanism from the throughput side.

## 3. Gate what will be the tip, once

The gate runs on the tree the compare-and-swap then publishes — never on a tree that will be
rebuilt. If the trunk moves during a gate and the swap fails, the already-gated result is re-applied
onto the new tip and **landed without a second gate**, so throughput does not depend on the rate of
incoming change.

*Why:* re-gating on every trunk move makes landing a function of everyone else's velocity — the same
inverted polarity as principle 2, in a different place. This is the owner's stated model: a
protected branch that does not require candidates to be up to date before they merge.

*Enforced by:* [`tools/tillr-land`](../../tools/tillr-land/). Every trunk candidate lands through one
stateless squash (`Land` in [`landing.go`](../../tools/tillr-land/land/landing.go) calling
`Trunk.Squash` in [`trunk.go`](../../tools/tillr-land/land/trunk.go)): the gate runs beforehand
outside `land.lock`, and under `land.lock` `Trunk.Squash` three-way merges `base..head` onto
`trunkBefore` through plumbing (`git merge-tree --write-tree --merge-base=<base>` plus
`git commit-tree` with the scope-checked `LandingIdentity` and the head's own message, which names no
item — a landed commit carries no `Tillr-Item:` trailer, per `D-RS3` in
[`queue-persistence-ruling.md`](queue-persistence-ruling.md#3-landing-receipts-without-commit-trailers) — and no working
tree in the merge) before `Trunk.Move` advances the trunk guarded against `trunkBefore`. `Land`
records the outcome in the queue-free `landings` table ([`schema.go`](../../tools/tillr-land/land/schema.go))
and emits a `land.landing` event with both trees: `gated_tree` is the candidate head's tree that the
verdict bound, and `landed_tree` is `squash.Tree`, the tree the trunk published. There is no separate
carry flag or candidate queue: when the trunk has not moved (`trunkBefore == base_sha`), `gated_tree`
equals `landed_tree`; when a concurrent landing moved the trunk, `gated_tree` differing from
`landed_tree` is the normal outcome of that concurrent landing and not a defect. When the moved trunk
already holds the candidate's entire change, `squash.Tree == trunkBefore^{tree}`, and the landing is
refused as `already-on-trunk` rather than published as an empty commit; see
[Section 10](#10-supersession-is-a-verdict-not-a-stall). Across a crash between `Trunk.Move` and
the receipt, a retry of the same run does not squash or publish again. Before the move, the lander
points its marker ref `refs/tillr/landed/<run_id>` at the squash commit. A retry that finds that
commit in the trunk's history, as exactly this head's squash onto its parent, records it as the
landing. A retry of a landing that was recorded answers the stored row and writes nothing. This is
not a general once-only guarantee. A marker an owner deleted, an unrecorded landing an owner
reverted, and a run id reused by a second store over the same repository are each answered
differently; the [lander README](../../tools/tillr-land/README.md) states each case. A squash that conflicts is recorded on
`landings` with `result = "squash-conflict"` (`ResultSquashConflict`) and `conflict_paths`, and pins
`refs/tillr/runs/<run_id>` at `head_sha` — never landed, never re-gated, because the conflict is
about the candidate rather than the machinery. Both outcomes are pinned in
[`main_test.go`](../../tools/tillr-land/main_test.go)
(`TestAMovedTrunkSquashesTheChangeOntoTheNewTip` and `TestASquashConflictIsRecordedAndPinsTheHead`)
and [`trunk_test.go`](../../tools/tillr-land/land/trunk_test.go)
(`TestSquashWritesOneCommitWithNoItemTrailerAndTheDeployedIdentity` and
`TestSquashOnAConflictNamesTheConflictAndNoCommit`), consistent with
[`tools/tillr-land/README.md`](../../tools/tillr-land/README.md#state-invariants) (landed commits carry no `Tillr-Item:` trailer).

[`board-utilities.md`](board-utilities.md) [§5.4](board-utilities.md#54-land-alone) states that
contract for `land`.

Fork delivery branches are not the trunk: the lander publishes the exact built fork
head rather than rewriting fork history. A passing `fork-build` check is required,
and the locked tip must be an ancestor of that head. Cache preparation and receipt
recovery retain the current delivered tip. This is the
[lander's fork-delivery contract](../../tools/tillr-land/README.md#fork-delivery),
covered by the `TestFork` and `TestAPatchBranch` tests in the lander's module.

*Scope:* **the landing transaction.** [`tools/tillr-land`](../../tools/tillr-land/) meets it, and its
behavior is test-pinned rather than intended: `Trunk.Squash` is the single path every candidate moves
through, and the cases in [`main_test.go`](../../tools/tillr-land/main_test.go) and
[`trunk_test.go`](../../tools/tillr-land/land/trunk_test.go) pin both the clean concurrent move
(`gated_tree != landed_tree`) and the conflicting move (`squash-conflict`).

## 4. Every outcome is state-changing

A pass may not return a candidate to the queue in the same state, against the same tip, with the
same authored tip. Every outcome is landed, exited, judged, or blocked-by an open ticket. A repeat
is a defect by definition.

*Measured 2026-11-02:* the same candidate was isolated with the same reason and detail at 11:54,
12:27, 12:31 and 17:07 — four times, learning nothing.

*Enforced by:* nothing yet; the acceptance test is a drain cycle with zero repeated
`(candidate, tip, reason)` isolations.

*Scope:* **not lander-scoped.** It describes the dispatch side that owns a queue to return a candidate
to — `tillr-drain` and `tillr-loop` — and says nothing about the lander.

## 5. Only verdicts charge

Parks, provider failures, no-candidate lanes and machinery faults never consume an item's
standing. The rule holds without a counter: the owner ruled on 2026-11-17 in
[`tillr-judge-and-lifecycle.md`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)
(`D-JL10` in its [decision ledger](./tillr-judge-and-lifecycle.md#decision-ledger)) that a worker's
ending is never counted, and rejected numeric repair thresholds.

*Measured 2026-11-02:* ten candidates sat with "spent" budgets and **not one had a judged repair
tree**. Four of them had three no-candidate lanes each — their budget was spent entirely on provider
failures. The counter read 2/2 and the work had never once been seen by the gate.

*Enforced by:* [`tillr-drain`](../../tools/tillr-drain/), under `D-JL10` rather than a charge
counter. A
refused run, a run with no commit, a fence escape, a refused landing, and a squash conflict return
to the worker with their evidence every time and uncounted
([`tools/tillr-drain/README.md`](../../tools/tillr-drain/README.md#a-refused-run-goes-back-to-its-worker);
`TestAFailedCandidateGoesBackToItsWorkerUntilItRepeats` in
[`tools/tillr-drain/loop_test.go`](../../tools/tillr-drain/drain/loop_test.go)), while an infrastructure
failure closes that backend and walks to the next open backend (`backend-closed` when none is open;
[`tools/tillr-drain/README.md`](../../tools/tillr-drain/README.md#recorded-backend-availability)).

*Scope:* **not lander-scoped.** Worker-run settlement lives on the dispatch side (`tillr-drain`),
not the lander's; the lander writes `landings` and keeps no attempt counter.

## 6. A budget escalates, it does not loop

Work that keeps failing escalates; it never stalls silently and never loops. Escalation is
triggered by an **identical repeat** or an explicit **named obstacle**, not by a number of
attempts: the owner ruled on 2026-11-17 in
[`tillr-judge-and-lifecycle.md`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)
(`D-JL9` and `D-JL10` in its [decision ledger](./tillr-judge-and-lifecycle.md#decision-ledger))
that neither worker runs nor design runs have numeric attempt thresholds. Policy is the owner's;
the mechanism below the policy is not.

*Enforced by:* [`tillr-drain`](../../tools/tillr-drain/) in the uncounted shape of `D-JL9`,
`D-JL10`, and `D-JL11` ([`tillr-judge-and-lifecycle.md`](./tillr-judge-and-lifecycle.md#4-a-failed-attempt-is-never-a-verdict-and-never-a-count)),
not by [the escalation ladder](escalation-ladder.md)'s four-rung `plan-owner` dispatch or
`exceptions` ledger, which are unbuilt. A refused worker run returns to its worker until an ending
repeats an earlier ending of the same producer, cause and normalized evidence (`repeatedEnding` in
[`tools/tillr-drain/drain/returned.go`](../../tools/tillr-drain/drain/returned.go)), or until the
worker reports a named obstacle; either routes to the `design` seat, and reaches the owner only on a
design run's `NEXT-ACTOR: owner` objection or a repeated non-gate design ending (`designRepeatOf` in
[`tools/tillr-drain/drain/design_repeat.go`](../../tools/tillr-drain/drain/design_repeat.go);
`TestBlockedReportRoutesOnceToDesignThenOwner` in
[`tools/tillr-drain/blocked_route_test.go`](../../tools/tillr-drain/drain/blocked_route_test.go)). No
standing authority below the owner may grant an exception or answer an owner question.

*Scope:* **not lander-scoped.** Conflict and repeat routing are dispatch-side (`tillr-drain`). The
lander records `squash-conflict` or a refusal with no rung; how that routes is the dispatch side's.

## 7. A surface that hides parked work is a false report

The queue surface must show what is **not** moving, not only what is. A candidate held out of the
pass must be visible as held, with the reason — on the same surface used to judge the queue's health.

*Measured 2026-11-02:* a queue surface drew parked candidates under a "rejected" heading.
Twelve of fifteen queue entries were parked there, reading as work that had been refused, when in
fact nothing had judged most of them. One had been there four days.

*Enforced by:* [`tools/tillr-view`](../../tools/tillr-view/) and
[`tools/tillr-drain/holdrules`](../../tools/tillr-drain/holdrules/holdrules.go). `tillr-view waiting` (and `GET /api/v1/why`
plus `waiting --history` / `GET /api/v1/waiting`; see
[`tools/tillr-view/README.md`](../../tools/tillr-view/README.md#why-each-unlanded-item-is-not-moving))
reports why each unlanded item is not moving, classifying every held row and naming its next actor
and required action. Every exported drain hold reason is required to have a display rule in
[`holdrules.go`](../../tools/tillr-drain/holdrules/holdrules.go) (`TestEveryReasonConstantHasAHoldRule`
in [`tools/tillr-drain/drain/hold_rules_test.go`](../../tools/tillr-drain/drain/hold_rules_test.go)),
`TestWaitingEveryDrainReasonHasARule` in
[`tools/tillr-view/waiting_internal_test.go`](../../tools/tillr-view/waiting_internal_test.go) verifies
every rule renders with a class, reason and mover, and
`TestWaitingSummarySeparatesDocumentAnswersFromOtherNextActors` in
[`tools/tillr-view/owner_waiting_test.go`](../../tools/tillr-view/owner_waiting_test.go) pins the
separation of owner document questions from drain, design-seat and operator actions.

*Scope:* **not lander-scoped.** It binds the dispatch side's queue surface — `tillr view` and the
drain's dispatch table — not the lander, which keeps no candidate queue of its own.

## 8. A gate that cannot answer must say which

"Could not run" and "ran and said no" are different facts and must not share a class. A suite that
crashes, times out or produces no extractable verdict means the gate's own integrity is unknown and
the batch fails closed; a suite that names failing assertions is a verdict about the tree it ran
against, and must route like one.

"Times out" means stopped making progress, never ran longer than expected. The owner ruled on
2026-11-17 ([`D-GR15`](../reference/gate.md#why-its-this-way)): "target budgets, watch for stalls, but
don't let 'load is high' cause failures that would otherwise finish just fine." A time limit on a
check is a target that is measured and reported; a check still making progress is not stopped
because a clock passed and is never "could not judge"; what ends one early is a stall seen by a
watchdog, and a stall is named and leads to a named next step.

*Measured 2026-11-02:* a single stale assertion in the gate's own test suite was reported as "not
verified" rather than as a verdict, so a whole batch refused and re-armed on a five-minute clock
instead of the offender being isolated.

*Enforced by:* [`tools/tillr-gate`](../../tools/tillr-gate/). A check that could not run and a
check that failed are separate outcomes with separate exit codes. The check runner uses the
progress observer in
[`watchdog.go`](../../tools/tillr-gate/watchdog.go): sixty one-second samples without output
or group CPU progress produce `stalled`, exit 6, with observations. The drain retains the
first stall as `gate-stall-rerun` and holds a second on the same head tree as `gate-stalled`.
The gate program wrapper applies the same progress rule; a time limit is a reporting target.
A rolling stall advances no watermark, and a second consecutive stall of the
same check on the same range files one deduplicated trunk fixer. The
[wrapper watchdog slice](../reference/gate.md#budgets-and-the-stall-watchdog)
is covered by injected-observation tests.

*Scope:* **the landing transaction.** [`tools/tillr-land`](../../tools/tillr-land/) meets it: a gate
verdict whose result is `error` is refused with `ReasonVerdictError`, never read as a failure of the
candidate, and the gate's own exit codes keep the same split — a check that could not run is `exit 4`,
never a failed check.

## 9. Fix the machinery; never hand-carry the work

When the queue cannot land a candidate for a reason that is not about the candidate, the destination
is a deduped pile ticket and its execution is a mechanic lane. A candidate is never landed by hand
past the queue's own proof — a hand-carried landing leaves the machinery broken for the next one.

*Enforced by:* nothing yet. The owner has confirmed no excepted landing is available while a
machinery ticket is open.

*Scope:* **not lander-scoped.** It governs the operator and the drain whichever program is asked to
land; with one lander there is no second program to hand-carry past, which is the point.

## 10. Supersession is a verdict, not a stall

Work whose change is already on the trunk exits the queue on that finding. "Already present" is a
result, and the record may be corrected retroactively; it is not a state to wait in.

*Enforced by:* the lander records the finding and the drain exits the item on it. `Trunk.Squash` ([`trunk.go`](../../tools/tillr-land/land/trunk.go)) three-way merges
`base..head` onto `trunkBefore` via `git merge-tree --write-tree --merge-base=<base> <trunkBefore> <head>`.
When `trunkBefore` already holds the candidate's change, git's merge answers
`squash.Tree == trunkBefore^{tree}` without any patch comparison of ours. `Trunk.SquashWithMessage`
checks exactly that and answers an `Empty` squash with no commit. `Land`
([`landing.go`](../../tools/tillr-land/land/landing.go)) then records `result = "refused"`,
`reason = "already-on-trunk"`, pins the head, and moves nothing, so no empty commit reaches the
trunk. The case is pinned by `TestAnEmptySquashIsRefusedAsAlreadyOnTrunk` in
[`idempotent_test.go`](../../tools/tillr-land/idempotent_test.go). The one exception is a retry
of this run's own unrecorded landing: that is recorded as the landing it was (see
[Section 3](#3-gate-what-will-be-the-tip-once)). On the dispatch side, `tillr-drain`'s
`landCandidate` ([`loop.go`](../../tools/tillr-drain/drain/loop.go)) gives `already-on-trunk`
its own branch (`settleAlreadyOnTrunk` in
[`already_on_trunk.go`](../../tools/tillr-drain/drain/already_on_trunk.go)). The branch records the
passing verdict and moves the item to `resolved-elsewhere`, the existing terminal state for an
intent that another path satisfied. The loop then marks the work examined and releases the lane, as
it does after a landing. The item is never held as `land-refused`. The case is pinned by the
`commit` subtest of `TestDirtyPathWaitRetainsCandidateAcrossReplacement` in
[`dirty_hold_test.go`](../../tools/tillr-drain/drain/dirty_hold_test.go).

*Scope:* **the landing transaction and the drain.** [`tools/tillr-land`](../../tools/tillr-land/)
does not stall a superseded candidate: there is no candidate queue to wait in, and the finding is a
recorded refusal rather than a commit. `tillr-drain` exits the item on that refusal, recording
`ResolvesVia: "commit:" + c.HeadSHA` (`resolveElsewhere` in
[`terminal_item.go`](../../tools/tillr-drain/drain/terminal_item.go)). The lander's `patch-at-head`
and `patch-rewind` refusals are about `refs/heads/patch/*` delivery branches, not about an ordinary
change the trunk already holds.

## 11. A failure is diagnosed, not waited out

No mechanism may re-run work, or hold work for later re-running, on the ground that time has passed.
A retry, a retry budget, a cooldown and a bench-with-expiry are one mechanism in four spellings —
each spends another run, or an item's evening, on the hope that the failure cleared while nobody
looked — and none of them may be built again. Work is re-admitted only against a **named change**:
an input the failure implicated has changed and the change is verified, or the failure itself stated
a clearing condition (a `Retry-After`, a quota reset) and that condition was checked. A failure that
cannot name a clearing condition is diagnosed — its cause found and recorded — before anything it
blocks runs again. Determinism is not classified in advance; it is established by observation, when
the same failure repeats under unchanged inputs or strikes items that share nothing but the
machinery. The test for any proposed failure mechanism is one question: *what will be different on
the next run, and how will the system know?* A mechanism whose answer is "time passed" is refused.

*Measured 2026-11-03:* a persisted 30-minute cooldown benched 55 items as `retry-exhausted`
behind a lane model string (`openrouter/deepseek/deepseek-v4.1-flash-1017:morph`) that could not
resolve and was never going to. When the cooldowns expired, one item was re-admitted at 17:53:37 and
dead at 17:53:45 — `No API key found for openrouter` — and was benched for another 30 minutes. 383 of
1,083 child logs carry that one error. The cooldown worked exactly as designed; converting a
permanent configuration error into a recurring wait was the design.

*Enforced by:* **partly, and no test asserts the principle as a whole.** Under the
owner's ruling of 2026-11-17 (`D-JL10` in
[the judge and lifecycle design](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)),
`tillr-drain` does not hold a refused worker run. It sends the refusal back to the worker with its
evidence, so the next run has the failure named in its brief, and nothing in that path waits on a
clock. A repeat is established by observation: an ending whose producer, cause and normalized
evidence equal an earlier ending of the item goes to the design seat to be diagnosed, with both
records (`repeatedEnding` in `tools/tillr-drain/drain/returned.go`, table-tested in
`returned_test.go`). A squash conflict is one of those endings: a conflict repeats only when its conflicting paths
and normalized hunks equal an earlier one's (`conflictEndingKey`). Except for gate refusals, which always return to the same designer session when available, the
design seat's own endings are compared the same way: a design ending that repeats an earlier one goes to the owner as an objection the loop raises, with a question written
from both records (`designRepeatOf` in `tools/tillr-drain/drain/design_repeat.go`). A "named
change" in the sense above is an event that names the item: an operator's retry, a design answer,
or the recovery of a killed run. An infrastructure failure is handled by recorded backend
availability and `backend-closed`
([`tools/tillr-drain/README.md`](../../tools/tillr-drain/README.md#recorded-backend-availability)).
What is not enforced: the fleet trip, the tier-0 probes and the
fingerprint store of the design below. The specification is
[the cooldown-removal design](./failure-diagnosed-not-waited-out.md), which carries the whole
mechanism: a fingerprint over the failing stage and the normalized error, an inputs hash recorded
with every failure, an admission rule keyed on a named change rather than a clock, a fleet trip at
the third distinct item under one inputs hash, and the drain's own tier-0 probes.

*Scope:* **not lander-scoped.** It binds the dispatch side — `tillr-drain` and `tillr-loop` — which
dispatches and re-dispatches work and holds retry, budget and bench state. The lander dispatches no
lanes and keeps no such state, so it cannot break this principle and is not bound by it.

## 12. A principle cites the measurement that produced it

Every entry here names the run, the number or the commit that made it non-negotiable. A principle
without one is a preference, and preferences lose to whatever is convenient on the day.

*Enforced by:* this file's shape. An entry with an empty measurement or an empty enforcement column
is visibly incomplete rather than quietly true.

*Scope:* **not lander-scoped.** This file's shape, which is why the scope line is part of the shape
rather than a new rule: an entry that does not say which program it binds is as incomplete as one
with no measurement.

## 13. A held state names a mover that can still act

A state that holds work out of the queue must name the act that will next change it, and that act
must be one that can still happen. A mover named by a mechanism that no longer exists — a deleted
repair lane, a session that is gone, a person nobody asked — is not a mover: it is a hold with no
exit, and the work behind it is invisible because every surface reports the name rather than the
fact. When the named mover cannot act, the state must either install one that can or hand the work
to a state whose mover can, and the surfaces must say which of the two is true.

*Measured 2026-11-04:* ten items sat in progress for 15-22 hours with terminal runs marked as
needing repair. The only declared mover named a repair lane that did not exist, and the author's
session it stood for was gone; an in-progress item with a terminal run was in neither the ready set
nor the dead-row sweep, so the only exit was a re-admission by hand.

*Measured 2026-11-17:* five of seven design conflicts sat `awaiting` a design run that had already
ended, and every view said "waits on work: design" with nobody working. Seven items held a claim
whose run never launched. Eight items then held `conflict-owner` with no command that could raise
the conflict again, three of them only because a hand commit had fixed the trunk-side cause of the
gate refusal, and two of them over a committed design answer the settlement had misread.

*Enforced by:* in `tillr-drain`, each of these holds names a mover that can act:

- **A claim whose run never launched** is released, and its item is dispatched again
  (`releaseUnlaunchedClaims` in
  [`unlaunched_claim.go`](../../tools/tillr-drain/drain/unlaunched_claim.go);
  pinned by `TestACancelledClaimsRecordsAreRecognizedAndTheItemIsDispatchedAgain` in
  [`unlaunched_claim_test.go`](../../tools/tillr-drain/drain/unlaunched_claim_test.go)).
- **A design conflict awaiting an ended design run** is raised again for the design seat, by how
  the run ended and never by a count or by what changed since its dispatch: the owner's ruling of
  2026-11-17, recorded in
  [the judge and lifecycle design, Section 4.3](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9).
  A gate refusal, a killed run and a run that committed nothing go back to the design seat; a
  gated design commit is the answer; a run that commits nothing objects by naming the next actor,
  and only an objection naming the owner reaches the owner (`settleUnansweredDesignRuns` in
  [`design_unanswered.go`](../../tools/tillr-drain/drain/design_unanswered.go); pinned in
  [`design_unanswered_test.go`](../../tools/tillr-drain/drain/design_unanswered_test.go) and
  [`design_objection_test.go`](../../tools/tillr-drain/drain/design_objection_test.go)).
- **An interrupted triage run** is triaged again rather than sent to the owner
  (`TestAnInterruptedTriageRunIsTriagedAgainNotSentToTheOwner` in
  [`triage_interrupted_test.go`](../../tools/tillr-drain/drain/triage_interrupted_test.go)).
- **A lane with uncommitted files** under a pinned candidate is refused for that lane alone as
  `lane-holds-uncommitted-work` instead of stopping the loop (`ReasonLaneUncommittedWork` in
  [`lane_rotation.go`](../../tools/tillr-drain/drain/lane_rotation.go);
  `TestPinnedCandidateOnALaneWithUncommittedFilesLeavesTheLoopRunning` in
  [`lane_dirty_test.go`](../../tools/tillr-drain/drain/lane_dirty_test.go)).
- **A committed design answer** is read by its block's id and section. A hold that misread
  the answer is raised again for the design seat, naming the commit (`designBlockAnswer` and
  `settleMisreadDesignAnswers` in
  [`design_answer.go`](../../tools/tillr-drain/drain/design_answer.go);
  pinned in [`design_answer_test.go`](../../tools/tillr-drain/drain/design_answer_test.go)).
- **An owner hold over a design run's objection** has an owner's command:
  `tillr drain retry --item <id> --run <design run>` raises the conflict for one more design run.
  Every retry refusal names the command the face would accept, or says that none exists and who
  moves (`authorizeDesignRetry` in
  [`design_retry.go`](../../tools/tillr-drain/drain/design_retry.go); pinned
  in [`design_retry_test.go`](../../tools/tillr-drain/drain/design_retry_test.go) and by
  `TestEveryRetryRefusalNamesTheAcceptedCommandOrTheMover` in
  [`main_cli_test.go`](../../tools/tillr-drain/drain/main_cli_test.go)).

What is not enforced yet: the gate's trunk-already-red replay cannot judge a fast-gate check id
such as `documents`, so it never acquits a refusal the trunk caused. A design run refused for the
trunk's sake goes back to the design seat each time until the trunk is fixed; the gap is the
gate's to close. On the drain side (measured
2026-11-19 and queued under
[`D-JL14`](./tillr-judge-and-lifecycle.md#47-offered-commits-own-candidate-lanes-unlaunched-claims-and-named-hold-movers-d-jl14)),
[`unlaunched_claim.go`](../../tools/tillr-drain/drain/unlaunched_claim.go) undoes only conflict
moves today while `cancelStaging` marks an unlaunched workflow-node claim examined, leaving the
item held as bare `run-link-missing`, and ten rules in
[`holdrules.go`](../../tools/tillr-drain/holdrules/holdrules.go) still carry `"cause not recorded"`
as their mover.

*Scope:* **not lander-scoped.** The dispatch side holds candidates in repair states and items out of
dispatch — `tillr-drain` and `tillr-loop` — and the lander keeps no held state of its own, so it
cannot break this principle and is not bound by it. This is the held-state half of
[Section 11](#11-a-failure-is-diagnosed-not-waited-out): where that section forbids waiting on time,
this one forbids waiting on an actor that is not there.

## 14. Convict a run on what it did, not on what happened while it ran

A run is convicted only on evidence that the run itself made the change. A movement that merely
happened inside the run's window can be the owner's or another agent's. Examples are a worktree
added or removed, a branch, a tag, a fetch, a stash, a hand commit, a fork export, or an
operator's pin. Such a movement is recorded as information, and it must never hold an item or
stop the loop. The owner ruled on 2026-11-17: "why do we convict anyone for removing worktrees?
that sounds stupid? we should be free to act with normal tools without breaking the drain, goes
along with ad hoc commits". That ruling followed the one earlier the same day: "we're supposed
to be able to ad hoc land stuff".

*Measured 2026-11-17:* at five lanes, two runs that made no edits were held
`host-boundary-violation`. The cause was "worktree `<lane root>/.rolling/worktree` appeared",
which was the loop's own rolling checkout. Twenty-six stale registered worktrees from finished
agents were also left unremoved, because removing them would have convicted every run in flight.

*Enforced by:* `CompareRunObserved` in
[`tools/tillr-drain/drain/boundary.go`](../../tools/tillr-drain/drain/boundary.go). It splits a
run's bracket into what it convicts and what it only observes. It convicts a write into a
watched, idle sibling lane worktree, and the run's own lane worktree going away. It observes a
rogue ref inside tillr's namespaces, any other worktree's registration, and a main-checkout
branch switch. The observations go on the run's `boundary.check` event, and the check stays
clean. The [common deny-list](./tillr-vocabulary.md#51-the-work-and-its-statement) refuses a candidate whose committed change touches protected paths or item declarations. The
gate still judges what lands. `TestOrdinaryGitUseDuringARunNeverConvictsIt` pins the ruling.
*Not enforced:* a worker whose only escape lies outside the lanes is not stopped. A rogue
ref or a stray worktree is named for an operator, and nothing acts on it.

*Scope:* **not lander-scoped.** The bracket is the dispatch side's, in `tillr-drain`. The lander
holds landings on trunk movement through its own checks, and it never convicts a run.

## 15. Land the smallest functional change

A change lands as the smallest functional change possible, as a human engineer would land it. Part
of a design or a request may land by itself, and that is the healthier outcome, not a compromise:
a small change is easier to review and to test, and the work moves faster. The owner ruled on
2026-11-21, verbatim, with his spelling kept: "part of desgins can absoutely land. this is
generally MORE healthy. Remember, we're trying to mirror humans here. Humans desire to land the
smallest functional change possible to make reviewing, testing, velocity, all this stuff easier,
and it will also make it easier for us."

Two consequences follow from his words. A change is not held back because a named case of its
request is still unmet: the case follows as its own change. And "functional" is the limit on
"smallest": what lands works alone and leaves the trunk no worse than it was.

*Measured 2026-11-21:* one item's verifier refused it twelve times in two and a half hours, each
time on a different case, while its candidate grew from 34 to 64 files, and nothing landed. Across
the 1,049 landings the store records, the median landing changes 2 files and adds 75 lines. Of 95
landed `planned-build@v1` items, the 19 above the 95th percentile in size averaged 3.3 verifier
refusals and the other 76 averaged 1.1. The figures and how they were read are in
[the split design](./tillr-item-split.md#2-the-case-measured).

*Enforced by:* nothing. [The split design](./tillr-item-split.md) is the only mechanism that
applies it: it lets the accepted part of an oversized item land while the rest follows, and its
[build order](./tillr-item-split.md#14-the-build-in-slices-that-each-land-alone) is itself written
as slices that each land alone. That design is decided and unbuilt:
`rg -n -i 'carve|donor|parent_item|child_item' tools/tillr-item tools/tillr-drain tools/tillr-land`
found nothing on 2026-11-21. *Not enforced:* nothing reads the size of a request, a plan or a
candidate, and the planner's guidance says nothing about it
(`rg -n -i 'split|size|independent|large|small' config/tillr/plan-guidance.md` found nothing on
2026-11-21). A verifier still refuses a whole candidate for one unmet case. Until the split design
is built, this entry is a stated intent that binds whoever files a request, writes a design or
plans a build.

*Scope:* **not lander-scoped.** It binds where the size of a change is chosen: filing, the design
seat, the planner and the verifier, which run under `tillr-drain` and `tillr-loop`. The lander
lands one squashed commit per item whatever its size, so it cannot break this principle and is not
bound by it.

## 16. What the operator can settle is settled inside tillr

A problem the operating agent could solve without the owner does not go to the operating agent. It
goes to a similarly responsible agent, process or item inside tillr, which is given the evidence
the operating agent would have had to go and read. The owner ruled on 2026-11-21, verbatim: "I
guess a working principle should be that if you ever feel that you as the [operating agent] could
solve some problem, and it wouldn't need to be escalated to me, then that problem should escalate
to... a similarly responsible agent, process, ticket, whatever, within Tiller, rather than popping
up to you." (The operating agent is the foreground session that watches the drain and acts as
operator.)

Two consequences follow from his words. The operator is not a mover for any hold: a hold that only
the operator can end waits for a check-in, and by [Section 13](#13-a-held-state-names-a-mover-that-can-still-act)
that is a mover that cannot act when the session is gone. And the test for reaching the owner is
whether the decision is his, never whether a seat inside tillr failed or took long.

*Measured 2026-11-21:* from 12:00 AM to 10:58 PM ET the store records 29 items closed and 5 set to
landed by the operator, 29 holds addressed to the operator on 21 items, 32 paths added to items by
the operator, and 2 design retries the operator requested. Four of the 34 carry words of the
owner's about that item; the other 30 were settled without him. One of the five hand finishes was wrong: an
item was set to landed as a duplicate of work that did not deliver it. The command, the event
kinds read and how the closes were sorted are in
[the design that applies this principle](./operator-decisions-inside-tillr.md#2-the-day-measured).

*Enforced by:* nothing. [The design](./operator-decisions-inside-tillr.md) names, for each of seven
kinds of hand action, the seat or process inside tillr that takes it, and none of it is built. Two
parts exist already: the drain releases a wait when its blocker lands, and it releases a trunk
obstacle when the named checks are green. *Not enforced:* `tooling` and `seat` obstacles on a filed
request still name the operator as mover (`rg -n 'Mover = "operator"' tools/tillr-drain/obstacles/read.go`
found three assignments on 2026-11-21), no fault reason declares what ends it, and an item is
closed or set to landed on a reason typed by hand. Until the design is built, this entry is a
stated intent that binds the operator: each hand action of this kind is a missing rule to file.

*Scope:* **not lander-scoped.** It binds the dispatch side, `tillr-drain` and `tillr-loop`, where
holds, faults and the queue's order live. The lander holds no item and asks no one, so it cannot
break this principle and is not bound by it.

## 17. Disable a flaky check, file its repair, and count it neither green nor blocking

A check that both passes and fails on the same declared inputs is flaky. The response is to disable
that check, file an item to re-enable that named check correctly, and keep it from blocking any
unrelated candidate or holding the trunk red. The owner ruled on 2026-11-21 in two messages,
verbatim, with his spelling kept: "The correct move for a flaky test is disable it, file an item to
fix it, and don't let it block anything. It is not considered green, but it also doesn't let you
block other things." and "And the principle here is you make the trunk green by disabling the flaky
test. The test is not green because it's flaky, but the trunk is green because the test is no longer
included.And yes, the item that is filed should be to re-enable it correctly, the test that was
flaky."

Two consequences follow from his words. First, a disabled check is excluded from the program's
pass, remembered, and fail counts and is listed apart as `disabled`: the candidate or trunk is green
because the contradicted check is no longer included, while the check itself remains not green until
its repair item lands. Second, the filed item is not a generic flake report: it names the disabled
check and asks to fix it at its cause and re-enable it correctly, with its work done only when the
check is back in the gate program and no longer both passes and fails on the same inputs.

*Measured 2026-11-21:* before `D-GR21` in
[the gate reference](../reference/gate.md#a-contradicted-check-is-disabled), three incompatible
behaviors competed across the gate: crediting a contradicted check as a `flaky` pass, re-running it
live on unrelated candidates so an intermittent failure still refused landings or filed trunk fixers,
and treating the contradiction itself as red. When `disabled` was first attempted by teaching check
consumers one at a time, the kept candidate grew to 111 files and 10,088 added lines and was refused
eleven times by its verifier because separate Go and Python sites still credited a contradicted
check green, turned `disabled` into `error`, or dropped its repair item on a cancelled or errored
step ([the classification inventory](../reference/gate.md#result-classification)).

*Enforced by:* **partly.** Every moved Go and Python classification site routes through one
five-outcome classifier in [`tools/tillr/checkresult/classify.go`](../../tools/tillr/checkresult/classify.go)
(`Classify`, `Aggregate`, `Disabled`, `DisabledMarker`), guarded by `TestClassificationScanGoSites` in
[`tools/tillr-gate/classification_scan_test.go`](../../tools/tillr-gate/classification_scan_test.go),
`TestClassificationScanDrainSites` in
[`tools/tillr-drain/drain/classification_scan_test.go`](../../tools/tillr-drain/drain/classification_scan_test.go),
and [`tools/gate/classification-scan.test.py`](../../tools/gate/classification-scan.test.py).
`Aggregate` leaves `disabled` out of both failing and unknown outcomes, so a run whose remaining
checks pass answers `pass`. Store-backed `tillr-gate classify` and `tillr-gate enable`
([`tools/tillr-gate/classify.go`](../../tools/tillr-gate/classify.go),
[`tools/tillr-gate/enable.go`](../../tools/tillr-gate/enable.go)) persist
`disabled/<sha256(check_id)>.json` with both timestamps and retained log copies on an
exact-fingerprint contradiction, keep returning `disabled` until `enable` records the episode
cleared, and honor `--prove-disabled` for the repair candidate's proof. In
[`tools/tillr-gate/main.go`](../../tools/tillr-gate/main.go), `writeRunSummary` leaves `disabled`
checks out of `passed`, `remembered`, and `failed`, counts `disabled` apart, and lists each disabled
check with its episode evidence and repair item
(`TestDisabledCheckExcludedFromPassAndFailCountsAndListedAsDisabled` in
[`disabled_summary_test.go`](../../tools/tillr-gate/disabled_summary_test.go)). On the dispatch
side, `sweepDisabledMarkers` in
[`tools/tillr-drain/drain/flaky_repair.go`](../../tools/tillr-drain/drain/flaky_repair.go) sweeps
`disabled/*.json` at pass start and when every gate, replay, rolling, and heavy step returns
(detaching cancellation so errored and cancelled steps still file), files `flaky-<check-id>` (and
`-2`, `-3`, … for later episodes) titled ``re-enable check `<id>` correctly`` as a generated repair
item needing no document block, and invokes `tillr-gate enable` once the repair item lands
(`TestDisabledMarkerSweepFilesReEnableItemTitleAndWords`,
`TestDisabledMarkerSweepDeduplicatesWhileOpenAndEnablesAfterLand`, and
`TestDisabledMarkerSweepFilesOnCancelledAndErroredSteps` in
[`flaky_repair_test.go`](../../tools/tillr-drain/drain/flaky_repair_test.go)). *Not enforced:* live
`tillr-gate run` and `tillr-gate checks` still set `outcome.Memory = "flaky"` in `annotateMemory`
([`tools/tillr-gate/memory.go`](../../tools/tillr-gate/memory.go)) and execute the contradicted
check instead of short-circuiting on `disabled/*.json`, the Python store-backed checks do not yet
call store-backed `tillr-gate classify`, and the drain does not yet pass `--prove-disabled` when
gating a `flaky-<check-id>` candidate (`rg -n 'prove-disabled' tools/tillr-drain` returned nothing
on 2026-11-23) or re-classify cached proof/replay records and historical `DISABLED` statuses (slices
`2c`, `2d`, `3a`, and `3b` in
[the classifier build order](../reference/gate.md#result-classification)).

*Scope:* **not lander-scoped.** It binds the gate and dispatch side — `tillr-gate`, `tillr-drain`,
and `tillr-loop` — which classify check outcomes, print verdict summaries, sweep disabled markers,
file `flaky-<check-id>` repair items, and run `tillr-gate enable`.
[`tools/tillr-land`](../../tools/tillr-land/) reads only the top-level `gate.verdict` result
(`pass`, `pass-inherited`, `fail`, `error` in
[`verdict.go`](../../tools/tillr-land/land/verdict.go)) and neither inspects per-check `disabled`
entries nor keeps check state.

---

## Keeping this read

The library is only useful if it is consulted. Two mechanisms carry it, in order of strength:

1. **[`AGENTS.md`](../../AGENTS.md)** links it from the repository guide, which every agent working
   in this repo has in context.
2. **The rulings themselves** link here, so a reader who arrives from a ruling finds the principle it
   established.

The AGENTS.md link is the only channel that reaches a reader who did not arrive from a ruling.

When a ruling is overturned, the principle is amended or retired here **in the same commit**. A
principle that stops being true and stays in this file is worse than no principle at all: it is a
rule a reader cannot tell is dead.
