---
title: "Landing principles — the durable rulings, and what enforces each"
status: accepted
stage: CURRENT
verified: 2026-11-15
covers:
  - docs/design/escalation-ladder.md
  - docs/pi/candidate-triage-2026-10-28.md
  - docs/pi/landing-replay-ruling-2026-11-02.md
  - docs/pi/stranded-candidates-2026-11-02.md
tags: [board, landing, principles, rulings, library]
summary: "Fourteen principles distilled from the landing rulings of 2026-10-28 to 2026-11-17, each with the measurement that produced it, the thing that enforces it today, and the one lander it binds. A principle whose enforcement column says 'nothing' is a claim, not a rule, and is listed as such rather than assumed; a principle that is not lander-scoped says so and names the dispatch program it binds instead."
---

# Landing principles

This is the library, not another ruling. Each entry is one durable principle, the measurement that
produced it, the ruling that owns the detail, the column that matters — **what enforces it today** —
and a scope line naming the one lander it binds, or saying it is not lander-scoped. A principle with
nothing in the enforcement column is a stated intent; saying so is what keeps this file honest
instead of reading as a description of the machinery.

The rulings it distils are [the candidate triage](../pi/candidate-triage-2026-10-28.md) (the budget
and the escalation ladder), [the replay ruling](../pi/landing-replay-ruling-2026-11-02.md)
(squash-on-merge, and the deletion of `unverified`), and [the re-test record](../pi/stranded-candidates-2026-11-02.md)
(the measurement behind P5). Read those for argument; read this to check a proposed change against
what has already been settled.

## Scope: which lander a principle binds

The tree has one lander: [`tools/tillr-land`](../../tools/tillr-land/). It is the only program that
can move the trunk ref for a candidate, and the only program a scope line may name when it binds a
lander. The two programs this section used to describe are gone: the board's landing pass was deleted
with the board CLI, and the merge queue was retired in the ruling recorded in
[`mq-retirement.md`](mq-retirement.md) [Section 1.1](mq-retirement.md#11-the-owners-ruling-delete-first-rebuild-in-pieces).

A scope line therefore takes one of three shapes:

- **`the landing transaction`** — the single publish path, which is `tools/tillr-land` today.
- **`not lander-scoped`** — the principle binds the dispatch side, which is `tillr-drain` and
  `tillr-loop`, and says nothing about the lander.
- **a named program** — the entry binds that program and no other, and the line says which.

"Meets it" and "does not" are measurements of the tree, read in the source on 2026-11-15, not
intentions; where the lander does not meet an entry, the line names the gap that remains.

---

## 1. A refusal must be a verdict

Anything that records a candidate as failed must be able to name what **the candidate** did wrong. If
it cannot, it is a statement about the machinery, and it must not consume the candidate's budget,
park it, or hold it out of the queue.

*Measured 2026-11-02:* 53 of 53 refusals that day carried `cause=unverified`, and not one was a gate
verdict — every one was a patch-id mismatch, a git error, or a regeneration failure. A word meaning
"the gate has no opinion" was being recorded against fourteen candidates as though they had failed.

*Enforced by:* [`tools/tillr-land`](../../tools/tillr-land/) for the landing transaction. The board
pass and merge queue that emitted `cause=unverified` (`reasonUnverified`) are retired; in
[`landing.go`](../../tools/tillr-land/land/landing.go), `Reasons()` is a closed nine-value set
(`verdict-mismatch`, `verdict-failed`, `verdict-error`, `trunk-worktree-uncommitted`, `trunk-rewritten`, `patch-rewind`,
`patch-at-head`, `item-not-landable`, `already-on-trunk`), pinned by `TestTheRefusalReasonsAreTheRegisteredSet` in
[`conformance_test.go`](../../tools/tillr-land/land/conformance_test.go), and `squash-conflict`
(`ResultSquashConflict`) is its own outcome rather than a refusal. `trunk-rewritten` is
an infrastructure hold: the drain retains the already-gated candidate, records both tips,
and waits for restored history without a failing candidate verdict or another gate.

*Scope:* **the landing transaction.** [`tools/tillr-land`](../../tools/tillr-land/) meets it: a trunk
move between the gate and `pass` is squashed onto the moved tip by `Trunk.Squash`
([`trunk.go`](../../tools/tillr-land/land/trunk.go)) and recorded on `landings` with both
`gated_tree` and `landed_tree`, never refused on patch-id or base drift. Every refusal in `Reasons()`
names a specific fact about the verdict, the patch ref, uncommitted worktree paths, the
owner's closed/resolved item disposition, or a trunk that already carries the change, and
non-candidate refusals (`verdict-error`, `trunk-worktree-uncommitted`, `already-on-trunk`) leave the
verdict standing and pin `refs/tillr/runs/<run_id>` at the head.

## 2. Re-application preserves the change, not the patch

When a candidate is re-applied onto a newer base, the resulting **diff** is expected to differ. A
check that demands the patch survive is measuring the base, not the author, and its failure rate
rises with how busy the trunk is — it punishes the condition the mechanism exists to handle.

*Measured 2026-11-02:* item 362 was refused for a day because seven commits landed in
`tools/board-cli/internal/board/` after its branch point. Its claim recorded two different replayed
patch-ids for one authored diff in a single pass.

*Enforced by:* [`tools/tillr-land`](../../tools/tillr-land/):
[`Trunk.Squash`](../../tools/tillr-land/land/trunk.go) three-way merges the candidate's `base..head`
change onto the moved tip (`git merge-tree --write-tree --merge-base=<base> <trunk> <head>` plus
`git commit-tree`) and never compares patch identity, so a replayed diff that differs — and a
`landed_tree` that differs from `gated_tree` on `landings` — is the normal outcome of a concurrent
landing and not a defect. The replay ruling's step 1 — the board pass's squash-merge — was the
earlier route to the same mechanism; it retired with the board.

*Scope:* **the landing transaction.** [`tools/tillr-land`](../../tools/tillr-land/) meets it: every
landing runs `Trunk.Squash` to three-way merge the candidate change onto the current tip and records
both trees on `landings` without inspecting `git patch-id`.
[Section 3](#3-gate-what-will-be-the-tip-once) is the same mechanism from the throughput side.

## 3. Gate what will be the tip, once

The gate runs on the tree the compare-and-swap then publishes — never on a tree that will be
rebuilt. If the trunk moves during a gate and the swap fails, the already-gated result is re-applied
onto the new tip and **landed without a second gate**, so throughput does not depend on the rate of
incoming change.

*Why:* re-gating on every trunk move makes landing a function of everyone else's velocity — the same
inverted polarity as principle 2, in a different place. This is the owner's stated model, modeled on
a merge queue with "require branches up to date" off.

*Enforced by:* [`tools/tillr-land`](../../tools/tillr-land/). Every candidate lands through one
stateless squash (`Land` in [`landing.go`](../../tools/tillr-land/land/landing.go) calling
`Trunk.Squash` in [`trunk.go`](../../tools/tillr-land/land/trunk.go)): the gate runs beforehand
outside `land.lock`, and under `land.lock` `Trunk.Squash` three-way merges `base..head` onto
`trunkBefore` through plumbing (`git merge-tree --write-tree --merge-base=<base>` plus
`git commit-tree` with the scope-checked `LandingIdentity` and the head's own message, which names no
item — the `Tillr-Item:` trailer was deleted by `D-RS3` in
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
(`TestSquashWritesOneCommitWithTheItemTrailerAndTheDeployedIdentity` and
`TestSquashOnAConflictNamesTheConflictAndNoCommit`).

[`board-utilities.md`](board-utilities.md) [§5.4](board-utilities.md#54-land-alone) states that
contract for `land`.

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

*Enforced by:* nothing yet; the parking rule that produced these repeats is deleted in step 2 of the
replay ruling, and the acceptance test is a drain cycle with zero repeated
`(candidate, tip, reason)` isolations.

*Scope:* **not lander-scoped.** It describes the dispatch side that owns a queue to return a candidate
to — `tillr-drain` and `tillr-loop` — and says nothing about the lander.

## 5. Only verdicts charge

A repair attempt is charged only when a tree the gate actually judged was refused, and only per
distinct cause. Parks, provider failures, no-candidate lanes and machinery faults charge nothing.

*Measured 2026-11-02:* ten candidates sat with "spent" budgets and **not one had a judged repair
tree**. Four of them had three no-candidate lanes each — their budget was spent entirely on provider
failures. The counter read 2/2 and the work had never once been seen by the gate.

*Enforced by:* nothing yet. Item 418 is where the charge event is redefined; until it lands, the
recomputation is a recorded hand operation.

*Scope:* **not lander-scoped.** The attempt ledger and the repair budget it charges live on the
dispatch side's runs, not the lander's; the lander writes `landings` and charges no repair
budget.

## 6. A budget escalates, it does not loop

K attempts is a budget per **distinct cause**, not per lifetime. A candidate that exhausts it flows
up the escalation ladder — the repair lane, then a contingency, then a recorded exception with an
expiry — and the last rung is an explicit "on hold", never a silent stall. Policy is the owner's;
the mechanism below the policy is not.

*Enforced by:* [the escalation ladder](escalation-ladder.md)'s four rungs and its `exceptions`
ledger. No standing authority below the owner may grant an exception.

*Scope:* **not lander-scoped.** The ladder and its ledger are dispatch-side. The lander ends a lost
compare-and-swap as an external failure with no rung; whether that becomes one is the dispatch side's
to decide.

## 7. A surface that hides parked work is a false report

The queue surface must show what is **not** moving, not only what is. A candidate held out of the
pass must be visible as held, with the reason — on the same surface used to judge the queue's health.

*Measured 2026-11-02:* `splitRejectedLandings` draws parked candidates under a "rejected" heading.
Twelve of fifteen queue entries were parked there, reading as work that had been refused, when in
fact nothing had judged most of them. One had been there four days.

*Enforced by:* nothing. The heading is the defect.

*Scope:* **not lander-scoped.** It binds the dispatch side's queue surface — `tillr view` and the
drain's dispatch table — not the lander, which keeps no candidate queue of its own.

## 8. A gate that cannot answer must say which

"Could not run" and "ran and said no" are different facts and must not share a class. A suite that
crashes, times out or produces no extractable verdict means the gate's own integrity is unknown and
the batch fails closed; a suite that names failing assertions is a verdict about the tree it ran
against, and must route like one.

"Times out" means stopped making progress, never ran longer than expected. The owner ruled on
2026-11-17 ([`D-GR15`](gate-rework.md#decision-ledger)): "target budgets, watch for stalls, but
don't let 'load is high' cause failures that would otherwise finish just fine." A time limit on a
check is a target that is measured and reported; a check still making progress is not stopped
because a clock passed and is never "could not judge"; what ends one early is a stall seen by a
watchdog, and a stall is named and leads to a named next step.

*Measured 2026-11-02:* a single stale assertion in `tests/fork-check.test.py` was reported as "not
verified" rather than as a verdict, so a whole batch refused and re-armed on a five-minute clock
instead of the offender being isolated.

*Enforced by:* `d78e05685` landed the split, with tests pinning both arms and the rule that a red
self-suite still never passes the batch. The 2026-11-17 half is **not enforced yet**: on that date
`tillr-gate` still stops a fast check at its ceiling and answers exit 4 (`fastCeilingMS` in
[`run.go`](../../tools/tillr-gate/run.go)), and the gate program and `tools/fork-check.py` still
stop checks on fixed timeouts.

*Scope:* **the landing transaction.** [`tools/tillr-land`](../../tools/tillr-land/) meets it: a gate
verdict whose result is `error` is refused with `ReasonVerdictError`, never read as a failure of the
candidate, and the gate's own exit codes keep the same split — a check that could not run is `exit 4`,
never a failed check.

## 9. Fix the machinery; never hand-carry the work

When the queue cannot land a candidate for a reason that is not about the candidate, the destination
is a deduped pile ticket and its execution is a mechanic lane. A candidate is never landed by hand
past the queue's own proof — a hand-carried landing leaves the machinery broken for the next one.

*Enforced by:* nothing yet; the ticket route is step 3 of the replay ruling. The owner has confirmed
no excepted landing is available while a machinery ticket is open.

*Scope:* **not lander-scoped.** It governs the operator and the drain whichever program is asked to
land; with one lander there is no second program to hand-carry past, which is the point.

## 10. Supersession is a verdict, not a stall

Work whose change is already on the trunk exits the queue on that finding. "Already present" is a
result, and the record may be corrected retroactively; it is not a state to wait in.

*Enforced by:* the lander records the finding and the drain exits the item on it. `Trunk.Squash` ([`trunk.go`](../../tools/tillr-land/land/trunk.go)) three-way merges
`base..head` onto `trunkBefore` via `git merge-tree --write-tree --merge-base=<base> <trunkBefore> <head>`.
When `trunkBefore` already holds the candidate's change, git's merge answers
`squash.Tree == trunkBefore^{tree}` without any patch comparison of ours. `Trunk.SquashWithMessage`
now checks exactly that and answers an `Empty` squash with no commit. `Land`
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
recorded refusal rather than a commit. `tillr-drain` exits the item on that refusal. What is not
enforced yet: the `resolved-elsewhere` move records no pointer to the commit that carries the
change, because `tillr-item state` takes none today. The lander's `patch-at-head` and `patch-rewind`
refusals are about `refs/heads/patch/*` delivery branches, not about an ordinary change the trunk
already holds.

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

*Measured 2026-11-03:* item 482's persisted 30-minute cooldown benched 55 items as `retry-exhausted`
behind a lane model string (`openrouter/deepseek/deepseek-v4.1-flash-1017:morph`) that could not
resolve and was never going to. When the cooldowns expired, item 487 was re-admitted at 17:53:37 and
dead at 17:53:45 — `No API key found for openrouter` — and was benched for another 30 minutes. 383 of
1,083 child logs carry that one error. The cooldown worked exactly as designed; converting a
permanent configuration error into a recurring wait was the design.

*Enforced by:* **nothing in the tree today.** The mechanism was built in the board CLI and deleted
with it on 2026-11-11 (`afdc9a9571`); `tillr-drain`'s answer to a failed run is a hold, not a
diagnosis. The surviving specification is
[the cooldown-removal design](./failure-diagnosed-not-waited-out.md), which carries the whole
mechanism: a fingerprint over the failing stage and the normalized error, an inputs hash recorded
with every failure, an admission rule keyed on a named change rather than a clock, a fleet trip at
the third distinct item under one inputs hash, and the drain's own tier-0 probes. That design is the
surviving record; the reference that described the built behavior was retired with its producer in
[the document retirement](./board-docs-retirement.md).

*Scope:* **not lander-scoped.** It binds the dispatch side — `tillr-drain` and `tillr-loop` — which
dispatches and re-dispatches work and holds retry, budget and bench state. The lander dispatches no
lanes and keeps no such state, so it cannot break this principle and is not bound by it.

## 12. A principle cites the measurement that produced it

Every entry here names the run, the number or the commit that made it non-negotiable. A principle
without one is a preference, and preferences lose to whatever is convenient on the day — which is how
all eleven of the above were broken at once.

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

*Measured 2026-11-04:* ten items (153, 192, 379, 432, 434, 442, 451, 453, 454, 455) sat in-progress
for 15-22 hours with terminal runs carrying `repair_state=needs-repair`. The only declared mover,
`LandingMoverRepair` in `skipMoverEntries`, named a repair lane deleted with P6, and the author's
session it stood for was gone; in-progress plus a terminal run is in neither the ready set nor the
dead-row sweep, so the only exit was a hand `board run readmit`.

*Enforced by:* the stranded-candidate transition (`repair_stranded.go`), which returns the item to
dispatch as rework when no live or resumable session remains, bounded per
`landingFailureSignature`; the corrected `skipMoverEntries` entry and `repairSkipDetail`; the view's
held-candidate mover; and `board land why`'s `moverLive` and `standing` fields, which report whether
the named mover can act rather than only its name. The board CLI that carried those mechanisms was
deleted; the surviving surface is `tillr view`, which reports a hold and its reason.

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
clean. The fence check still convicts a candidate whose committed change leaves its fence. The
gate still judges what lands. `TestOrdinaryGitUseDuringARunNeverConvictsIt` pins the ruling.
*Not enforced:* a worker whose only escape lies outside the lanes is no longer stopped. A rogue
ref or a stray worktree is named for an operator, and nothing acts on it.

*Scope:* **not lander-scoped.** The bracket is the dispatch side's, in `tillr-drain`. The lander
holds landings on trunk movement through its own checks, and it never convicts a run.

---

## Keeping this read

The library is only useful if it is consulted. Two mechanisms survive, in order of strength:

1. **[`AGENTS.md`](../../AGENTS.md)** links it from the repository guide, which every agent working
   in this repo has in context.
2. **The rulings themselves** link here, so a reader who arrives from a ruling finds the principle it
   established.

The third mechanism this section used to name — that the board constellation listed this file in a
reading order handed to every design seat — went with the board: the constellation was retired in
[the document retirement](./board-docs-retirement.md). Nothing replaces it yet, so the AGENTS.md
link is the only channel that reaches a reader who did not arrive from a ruling.

When a ruling is overturned, the principle is amended or retired here **in the same commit**. A
principle that stops being true and stays in this file is worse than no principle at all: it is a
rule a reader cannot tell is dead.
