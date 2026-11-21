---
title: "A trunk fixer is gated on what its repair reaches and on the checks it was filed to repair"
status: in-review
stage: DESIGN
updated: 2026-11-21
tags: [tillr, gate, landing, drain, forks, design]
summary: "The trunk fixer's gate stops being the untiered gate over every landing since the last green commit. It becomes the gate any candidate gets, plus the checks the fixer names, run on the fixer's tree. A fork check that is red only because an upstream published a release is reported as one rebase request per fork and release and refuses nobody whose change does not touch that fork; a fork that follows a branch head gets the same acquittal and no request. One question is open for the owner: whether he is told, beyond the queue, when an upstream moves."
next: "Amend D-FG3 to say who rebases a fork onto a new release and whether the pin update is its own delivery (a design correction is queued), then build the drift rule. No owner-facing row or hold is built for drift."
depends-on:
  - landing-principles.md
  - gate-rework.md
vantage:
  status-chip: true
---

# A trunk fixer is gated on what its repair reaches and on the checks it was filed to repair

**Status:** The fixer's gate is built (`D-FG1` and `D-FG2`). The drift and notification work is not built.
The original tree description was read at `6432ac37e`.

> **In short.** The seat that restores a red trunk is the only seat whose gate reads every
> landing since the last green commit, so it is the only seat an outside release can stop. Its
> gate becomes the ordinary gate plus the checks it names, and upstream movement becomes a filed
> rebase request instead of a refusal.

**Why it matters.** On 2026-11-20 a correct two-file repair was refused by fork checks it did not
touch, and the trunk stayed red for a cause outside the repository.

**The shape.** Three rules: the [fixer's gate](#the-fixers-gate), the
[drift rule](#upstream-drift-is-a-rebase-request-not-a-refusal) and
[who is told](#who-is-told-when-an-upstream-moves).

**Cost.** A fixer no longer re-judges other landings before it merges; the proof after the merge
is the only judge of them. Upstream drift is noticed later until the drift probe is built.

**Start at** [the fixer's gate](#the-fixers-gate).

**Needs your ruling:** nothing. The owner ruled [OQ-FG1](#OQ-FG1) on 2026-11-21. `D-FG1` to `D-FG5` in the
[Decision Ledger](#decision-ledger) are the designer's and his to overturn.

**Reads with:** [the gate rework](gate-rework.md) (its
[Section 6.9](gate-rework.md#69-upstream-drift-is-a-probe-not-a-check) already takes drift out of
the gate; this design applies it to the fixer), [landing principles](landing-principles.md), and
[the fork workflow](../pi/forks.md).

---

## Terms

- **Trunk fixer.** The generated item `fixer-main-<sha>` that the drain files when a trunk check
  is red. It names the failing check ids. It is not a revert and not an ordinary queue item.
- **Proof.** The run after each merge that judges the trunk tip and moves the green pointer
  (the heavy runner of [the gate rework](gate-rework.md#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer)).
- **Upstream reader** *(coined here)*. A check whose verdict depends on what an upstream
  repository currently publishes, not only on the commit under judgment. In
  `tools/fork-check.py` these are the checks the `reads_upstream` predicate matches, which is
  every fork check declared not reusable. That is the set the proof leaves out and the fixer's
  gate drops, as built. The set is wider than the checks that fetch: only `patch-branch-inv2`
  fetches the followed upstream, and only it can be drift. An upstream reader is not a check
  that merely needs the network to install dependencies.
- **Upstream drift** *(coined here)*. A `patch-branch-inv2` check that is red on a candidate, red
  on the candidate's base with the same answer, on a candidate that does not touch that fork.
  [Which checks can be drift](#which-checks-can-be-drift) defines "same answer" and "touches the
  fork". It is not a red the candidate caused, and it is not a check that could not run.
- **Rebase request** *(coined here)*. One generated fork item that names a fork, the upstream
  release its series does not merge, and the failing question. It is not a trunk fixer: the
  trunk commit is not wrong, and the repair is made in the fork's checkout.

## What happens today

Read in the source on 2026-11-21:

| Caller | Program | What it plans |
| :--- | :--- | :--- |
| Ordinary candidate | `tools/gate/change-gate.py`, then `fork-check.py --since <base> --tier landing` | The change and fast tree checks, then the landing-tier checks its own diff selects |
| Proof | `fork-check.py --tier rolling --all` | Every tree check, except upstream readers, which it names as not a property of the trunk commit (`apply_tier` in `tools/fork-check.py`) |
| Trunk fixer | The same `change-gate.py`, then `fork-check.py --since <last green>` or `--all`, with no tier (`DefaultFixerGateProgram` in `tools/tillr-drain/drain/trunk.go`, `fixerGate` in `steps.go`) | Every check any landing since the last green selects, upstream readers included |

The fixer is therefore the only caller that plans upstream readers over a range of other
changes. An ordinary candidate plans one only when its own diff touches a series or a pack
declaration. The proof never plans one.

A second rule compounds it. A candidate refused only by checks that are also red on its base and
on the tip is acquitted and lands with the failures recorded as inherited
(`trunkAlreadyRed` in `trunk.go`). A fixer is excluded from that rule in whole, so that it
cannot heal itself by doing nothing.

**The 2026-11-20 incident.** The trunk was red on one failing test in `tools/tillr-drain`. The
fixer committed a two-file test repair, `1d1d0d877` [unlanded]. Its gate refused on
`patch-branch-inv2` for the pi fork, because upstream pi had published `v1.6.0`, which conflicts
with `patch/pi`. Three more fork checks could not judge, because the `pi-manage-todo-list` remote
does not serve its pinned commit. The same repair filed as an ordinary item
(`item-request-f0ef2f6b68`) planned none of those checks.

## What the untiered range bought

The untiered range re-judges, before the fixer merges, three groups of checks:

| Group | Who else judges it | Lost by dropping it |
| :--- | :--- | :--- |
| Checks the fixer's own diff selects | The ordinary gate | Nothing; they stay |
| Tree checks that other landings since the last green select | The proof, on the tip, after the fixer lands | An earlier answer only. A second red is found by the proof and gets its own fixer, as any red does |
| Upstream readers that other landings select | Nobody | A verdict about the upstream, charged to a change that did not touch it |

The third group is the only coverage unique to the fixer's gate, and it is the coverage
[the gate rework](gate-rework.md#69-upstream-drift-is-a-probe-not-a-check) already ruled is not a
verdict about a commit. The second group costs more the longer the trunk has been red, which is
when the fixer is needed most. Both point the wrong way under the rule the
[landing principles](landing-principles.md) keep proving: a mechanism whose cost rises with
activity it does not control.

## The fixer's gate

A trunk fixer's gate runs two things, in this order, and passes only when both pass.

1. **The ordinary gate, unchanged.** Exactly what any candidate of the repository's landing
   strategy gets, over the fixer's own base and head: the change checks, the fast tree checks,
   and the landing tier its diff selects.
2. **The named checks.** Every check the fixer's criterion names, run on the fixer's tree by the
   selectors the proof recorded for them (the ones `fork-check.py --replay --only` takes). This
   includes a check of the rolling tier, which the ordinary gate would defer to the proof.

The second part exists because the fixer's claim is that it repairs those checks, and the
ordinary gate does not test that claim: the diff may not select the check, or the check may be
deferred. A fixer that lands without repairing leaves the trunk red and is never reverted, so
the claim is tested before the merge.

Specified cases:

- **No range since the last green is read.** The `{green}` value is no longer part of the
  fixer's gate. The fixer is still rebased onto the tip before it is gated, as today.
- **A fixer that names no check** runs the ordinary gate alone. It is resolved only by a whole
  green proof, which is today's rule.
- **A named check fails.** The gate refuses, and the refusal goes back to the fixer's own worker
  naming the test. This is a verdict.
- **A named check could not judge.** The gate answers "could not judge", never pass and never
  fail, and names the check and what it lacked. The fixer is held as a gate fault (the
  `gate-harness-error` stop), which is how it is built, and no defect item is filed for it
  ([what "could not judge" does](#the-checks-that-could-not-judge)). A fixer is not landed on a
  check nobody could run.
- **A named check that is an upstream reader** is not run as a named check. A red upstream
  reader is a change's own failure or, for `patch-branch-inv2` alone, drift, decided by the [drift rule](#upstream-drift-is-a-rebase-request-not-a-refusal),
  and no trunk fixer is filed for it from here on. A fixer already open that names one keeps
  its other checks, and that name is dropped when its gate is composed, with a line saying so.
- **The inherited-failure rule applies to checks the fixer does not name.** The fixer stays
  excluded from acquittal for the checks it names, which is the whole reason for the
  exclusion. For any other check it is judged as any candidate is, with the two exceptions in
  [a fixer never waits on itself](#a-fixer-never-waits-on-itself).
- **A refusal that mixes named and unnamed checks is a verdict.** When any check the fixer
  names is among the failing checks, nothing is acquitted and the whole refusal goes back to
  the fixer's worker. The inherited-failure rule is tried only when every failing check is one
  the fixer does not name.
- **Order and concurrency.** One gate run per candidate, as today. The named checks run after
  the ordinary gate passes; when the ordinary gate refuses, they do not run.
- **Forbidden.** The gate's own code stays outside a fixer's fence. Nothing here lets a fixer
  choose, narrow or rename the checks it is judged on: the list comes from the fixer's record.

### A fixer never waits on itself

Added on 2026-11-21 to answer a build worker's finding; the tree was read at `80d7ceb14a`. It is
the designer's decision, part of `D-FG2`, and the owner may overturn it.

The ordinary inherited-failure rule has two outcomes for a check that is red on the candidate's
base and on the tip (`settleTrunkAlreadyRed` in `tools/tillr-drain/drain/trunk.go`). When the
candidate's diff changes no path the check reads, the candidate lands with the failure recorded
as inherited. When it does change such a path, the red is attributed to a commit, the fixer for
that commit is filed or attached to, and the candidate is held behind that fixer.

Applied to a fixer without change, the second outcome can name the fixer itself. There is one
fixer per red commit (`fileTrunkFixerNoted` in the same file), so when the unnamed check is
attributed to the commit the fixer was filed for, the check attaches to that same fixer and the
fixer is then held with itself as its waiter. Nothing ever releases that hold. The build worker
reproduced it on 2026-11-21 with a test that passed by showing the fault:
`go test ./drain -run '^TestOrdinaryInheritedFailureRouteMakesFixerWaitOnItself$'` in
`tools/tillr-drain` recorded the fixer as blocked with its own item id as its waiter. That test
was the worker's scratch and is not in the tree.

The rule for a fixer whose gate fails only on checks it does not name, all red on its base and
on the tip:

| The fixer's diff and the check's read paths | The fixer the ordinary rule would hold it behind | Outcome |
| :--- | :--- | :--- |
| Do not intersect | Not asked | The fixer lands with the failure recorded as inherited, as any candidate does |
| Intersect | A different fixer, which does not wait on this one | The fixer is held behind that fixer, as any candidate is |
| Intersect | This fixer itself | The check is attached to this fixer and the refusal goes back to its worker. No hold is recorded |
| Intersect | A different fixer that already waits on this one, directly or through other fixers | The same as the row above: attached to this fixer, returned to its worker, no hold |

**Attached means what it means today.** The fixer's record gains the check's id in its list of
red checks and the check's watched paths in its fence, with the gate's own code removed, and the
attachment is recorded as an event. This is the existing attachment, not a new one.

**The check is then a named check.** From the fixer's next gate it runs on the fixer's tree, the
fixer is not acquitted of it, and the fixer is resolved only when it passes. This follows from
[the fixer's gate](#the-fixers-gate) and needs no further rule.

**The refusal the worker reads** names the check, says it was already red on the trunk at the
named commit, says it has been added to this fixer because the fixer's change touches paths the
check reads, and carries the check's recorded output. It is a refusal that returns work, so the
existing limits on returned refusals apply to it unchanged.

**No second fixer is filed for the same commit.** One fixer per red commit stays the rule.

**The fourth row leaves one check named by two open fixers.** That is allowed here and only
here. Whichever lands a repair, the proof resolves both when the check passes. Later
observations of that check attach to one of them by the existing order.

**Why the fixer takes the check instead of waiting.** Its change already touches what the check
reads, so its tree is where the check's answer is being changed, and it is the only item filed
for that commit. A hold would need a second mover, and for the same commit there is none.

Rejected, as the designer's decision:

- **A distinct second fixer for the same commit.** Two workers would repair one commit in
  overlapping paths, and the first would still wait on the second for a red its own diff reaches.
- **Landing the fixer with the failure inherited even though its diff intersects.** The
  intersection is the one case where nobody can say the fixer left the check as it found it.
- **Exempting a fixer from the inherited-failure rule in whole,** which is today's behavior and
  what `D-FG2` removes.

Done looks like this: with the trunk red on one test and an upstream release that conflicts with
a patch branch, a fixer whose diff touches only that test's files passes its gate and lands, and
its gate log lists the ordinary checks and the named check and no fork check.

## Upstream drift is a rebase request, not a refusal

[The gate rework](gate-rework.md#69-upstream-drift-is-a-probe-not-a-check) decides that `series`
and `patch-branch-inv2` leave both tiers and run as a drift probe, at loop start and after a
landing that changes a series or a pack declaration (`D-GR12`, that design's own decision). The
probe is not built: both kinds are still `TIER_LANDING` in `tools/fork-check.py`. This design
keeps that decision and says what happens until it is built, for every candidate and not only
a fixer.

When a gate plans a `patch-branch-inv2` check and it is red, the loop replays it on the
candidate's base, which the replay step already does for every red check.
[Which checks can be drift](#which-checks-can-be-drift) says why it is that kind alone. Then:

| The check on the base | The candidate's diff and the fork's own paths | Outcome |
| :--- | :--- | :--- |
| Passes | Any | The candidate's own failure. Ordinary refusal |
| Red, same answer | Do not intersect | Drift. The candidate lands with the failure recorded as inherited. One rebase request is filed |
| Red, same answer | Intersect | Drift, and the candidate changes the same fork. One rebase request is filed, and the candidate waits on it by name unless it is that request's own work |
| Could not judge | Any | Not drift and not a verdict. "Could not judge", named, as today |

No trunk fixer is filed for drift, because no trunk commit is at fault and the proof does not
judge these checks.

The table reads the same for every fork, whatever it follows. What is filed depends on what the
fork follows, and that is [the next two parts](#a-fork-that-follows-a-branch-head).

**The rebase request.**

- **One per fork and release.** A second observation of the same fork and release files nothing
  and attaches to the open request. A newer release for the same fork updates the open request
  in place.
- **It is fork work with a repository part.** It is filed the way fork work is filed, naming
  the fork, and its worker rebases the fork in the fork's lane checkout with plain `git rebase`.
  The lander checks and delivers that head; it does not rebase. A second run then moves the
  fork's recorded commit in the repository. Neither step is the operator's.
  [Who rebases, and how the recorded commit follows](#who-rebases-and-how-the-recorded-commit-follows)
  has the commands and the order.
- **It holds nothing but changes to that fork.** It never holds the green pointer, the proof,
  a fixer, or a candidate that does not touch the fork.
- **A fork pinned to a commit** has no release to follow. Its check answers for the pin, so a
  red there is never drift from a release.
- **It closes** when its repository part lands, which is the landing that moves the fork's
  recorded commit and whose gate passed `patch-branch-inv2` against that release, or when a
  newer request replaces it. Delivering the patch branch alone does not close it
  ([why](#who-rebases-and-how-the-recorded-commit-follows)).
- **It is filed only for a fork that follows releases.** That is a fork whose pack declaration
  carries no `follow` key and no pin
  ([the follow modes](../pi/forks.md#roll-forward-on-releases-never-on-a-branch-head)), which
  the target line reports as the kind `release`
  ([which checks can be drift](#which-checks-can-be-drift)).

### Which checks can be drift

Added on 2026-11-21 to answer a build worker's finding; the tree was read at `58d445ea7f`. It is
the designer's decision, part of `D-FG3`, and the owner may overturn it.

The finding: this design said "`series` and `patch-branch-inv2` are the two that fetch" and
asked for one request "per fork and release". The `series` check is planned per pack, as
`yolo pack series check yolo-packs/<pack>`, with no fork selector and no fetch, so it cannot name
a fork or a release. The `reads_upstream` predicate also matches `patch-branch-inv3`,
`forks-check`, `build` and `fork-quality`, which answer about the tree or a build, so using it
to classify drift would call an export mismatch or a compile failure drift. The predicate stays
what the proof and the fixer's gate use to leave checks out; that is built and unchanged. The earlier wording was the
designer's error.

**Only `patch-branch-inv2` can be drift.** The build selects it by its kind name, never by
`reads_upstream`.

| Check kind | Can it be drift | Why |
| :--- | :--- | :--- |
| `patch-branch-inv2` | Yes | It is planned once per fork, with `--fetch --fork <name>`, and asks whether that fork's patch branch merges the upstream a launch would build |
| `series` | No | It is planned per pack and reads a recorded answer without fetching. It is also not planned for a pack whose forks are all patch branches, which is every fork today: `rg -v '^#' bin/pi-forks.list` on 2026-11-21 lists eight forks, each delivered as `patch/<name>` |
| `patch-branch-inv3`, `forks-check`, `build`, `fork-quality` | No | Each answers about the series, a clean checkout of it, or its build. None reads what an upstream published since |

A red on a kind marked "No" that is also red on the base is handled as it is today, by the
existing acquittal for a trunk that is already red or by an ordinary refusal. This design adds
nothing for it. The ordinary gate's plan is unchanged: no check is added, removed or given a new
argument.

**The fork** is the one the check's own `--fork` argument names.

**The target** is what `bin/pi-forks patch-branch target --fork <name>` prints for that fork
after the check ran: one line holding the upstream repository, the kind, the ref and the commit.
The kind decides which rule applies:

| Target kind | Rule |
| :--- | :--- |
| `release` | The rebase request below. "Release" in "one per fork and release" is this line's ref |
| `head` | [A fork that follows a branch head](#a-fork-that-follows-a-branch-head). The record names this line's ref and commit |
| `pin` or `base` | Never drift. The check answered for a fixed commit, so the red is an ordinary refusal |
| No line, or a non-zero exit | Not drift. It is [a check that could not judge](#the-checks-that-could-not-judge) |

**"Red, same answer"** means all three of these hold:

1. The check's output on the candidate carries the driver's refusal phrase for this kind,
   `conflicts with` (`INVARIANT_PHRASES` in `tools/fork-check.py`).
2. The replay on the base carries the same phrase.
3. Both name the same target ref.

A red without that phrase, on either side, is the driver failing to answer. It is
[a check that could not judge](#the-checks-that-could-not-judge). A different ref on the two
sides means the upstream moved between the two runs; the candidate is gated again and nothing is
classified.

**"The fork's own paths"** are `yolo-packs/fork-pins/<name>` and, for a fork that still commits
a series directory, the paths under it. A candidate touches the fork when its diff includes one
of them. The check's `watches` are not the test: they name the whole pack directory and
`bin/pi-forks`, and the landing tier plans the check only when the diff matches one of them, so
by that test every planned check would "intersect" and the acquittal would never apply. A
candidate that edits another part of the same pack, or the pack's declaration without changing
this fork's target, does not touch the fork; a declaration edit that changes the target makes
the two sides name different refs or makes the base pass, and both are covered above.

Rejected, as the designer's decision:

- **Giving `series` a fork and a release.** It would need a fork selector or a fetch added to a
  check the ordinary gate plans, which changes the ordinary gate, and it would cover no fork
  that exists today.
- **Classifying by `reads_upstream`.** It names checks that read no upstream.
- **Reading the fork and release from the check's printed sentence alone.** The target command
  prints the same resolution as one machine-readable line, with the kind.

### A fork that follows a branch head

Added on 2026-11-21 to answer a build worker's finding; the tree was read at `746f87e2b3`. It is
the designer's decision, part of `D-FG3`, and the owner may overturn it.

The finding: "one per fork and release" has no answer for a fork that is neither pinned nor a
release follower. `yolo-packs/matt/pack.json` declares one, `pi-background-tasks`, with
`"follow": "head"`, because its upstream cuts no tags. The lander delivers a rewritten patch
branch only for a release: `releaseRebase` in `tools/tillr-land/land/fork_rebase.go` accepts
the rewrite only when the followed target's kind is `release`, and any other rewrite is refused
as `patch-rewind`. A rebase request for a head follower would therefore be an item no drain
landing can close, and a candidate held behind it would never be released.

The rule for a fork whose declaration says `follow: "head"`:

| The check on the base | The candidate's diff and the fork's own paths | Outcome |
| :--- | :--- | :--- |
| Passes | Any | The candidate's own failure. Ordinary refusal |
| Red, same answer | Do not intersect | Drift. The candidate lands with the failure recorded as inherited. **No item is filed** |
| Red, same answer | Intersect | Ordinary refusal, returned to the candidate's worker, as today. No item is filed and nothing is waited on |
| Could not judge | Any | Not drift and not a verdict, as for any fork |

- **The inherited record** names the check, the fork, the followed branch and the upstream
  commit the check read, in place of a release.
- **The loop prints one line** per fork and upstream commit, the first time a candidate lands
  with that drift inherited. That line and the candidate's verdict are the whole notice.
- **No rebase request, no trunk fixer and no hold** come from head-follower drift.
- **The rebase stays the operator's step**, `bin/pi-forks rebase`, by the existing rule in
  [the fork workflow](../pi/forks.md). The lander's release-only rule is unchanged.

**Why no request is filed.** A request is useful only when the queue can close it. For a head
follower nothing in the drain can, so filing one would add an item that never lands and a hold
that never releases. The acquittal in the second row is the part of `D-FG3` that keeps an
unrelated change landing, and it does not need the item.

**What this leaves undone.** A candidate that does touch a head follower's series or its pack
declaration while that fork has drifted is refused as it is today, and its worker cannot rebase
the fork. One fork is in this state. A delivery route for a head follower's rebase belongs to
the lander and is not designed here.

Rejected, as the designer's decision:

- **Filing the request keyed by fork and upstream commit.** The lander refuses its delivery, so
  the item and every candidate held behind it would wait with no mover.
- **Widening the lander to accept a head-follower rewrite inside this decision.** It changes
  what the lander will rewrite on a published branch, which is a separate design.
- **Leaving head-follower drift on the ordinary path in whole.** A change that does not touch
  the fork would still be refused for it, which is the incident this design answers.

### Who rebases, and how the recorded commit follows

Added on 2026-11-21 to answer a build worker's finding; the tree was read at `4eee9c11ad`. It is
the designer's decision, part of `D-FG3`, and the owner may overturn it.

The finding: this design said "the rebase itself is the lander's and the operator's step" and
that the request "closes when a landing makes the check pass against that release". Neither
matches the tree, and the earlier wording was the designer's error.

**The fork's recorded commit** is the file `yolo-packs/fork-pins/<fork>`: three lines holding the
delivered commit, the remote it is fetched from, and the upstream commit the patches sit on.
This part calls it "the record".

What the tree does, read on 2026-11-21:

| Question | Answer | Where |
| :--- | :--- | :--- |
| Does the lander rebase? | No. It accepts a rewritten head only when the followed release is already an ancestor of the candidate head, is not an ancestor of the base, and the range holds no merge. It then moves `refs/heads/patch/<fork>` | `releaseRebase` and `moveForkRebase` in `tools/tillr-land/land/fork_rebase.go` |
| What commit does `patch-branch-inv2` judge? | The first line of the record, not the patch branch. The gate runs the check with `--candidate`, and for a patch-branch fork that reads the recorded commit from the gate's own copy of the fork under `.forks/inputs/<fork>` | `load_series` in `bin/pi-forks`, which calls `tools/gate/checks/fork_input.py` |
| What does the gate do when that copy lacks the commit? | It fetches the commit from the remote on the record's second line. When the remote does not serve it, the check could not judge | `ensure` in `tools/gate/checks/fork_input.py` |
| Who writes the record? | Only `bin/pi-forks export`, which refuses to run in a drain run. `rg -n 'fork-pins' tools/tillr-land tools/tillr-drain -g '!*_test.go'` returns nothing, and the one test that matches writes the file by hand as a fixture | `export_branch` and `refuse_in_drain` in `bin/pi-forks` |
| Who puts a delivered commit into the gate's copy? | Only that same export, by a local fetch from the lane checkout. `rg -n 'forks/inputs' tools/tillr-land tools/tillr-drain` returns nothing | `export_branch` in `bin/pi-forks` |
| When does the remote serve a rewritten patch branch? | After the owner acknowledges the move and `bin/patch-branches-publish --force-rewritten` runs on the host | [Git movement and run convictions](../../tools/tillr-drain/README.md#git-movement-and-run-convictions) and [the roll-forward rule](../pi/forks.md#roll-forward-on-releases-never-on-a-branch-head) |

So a delivered rebase changes nothing the check reads. The record still names the old commit,
and the check stays red against the release until the record moves.

**Who rebases: the request's worker.** The worker on the request's fork part produces the
rebased head, in the lane's own fork checkout at `.forks/lanes/<lane>/<fork>`, on `main`:

```bash
git rebase --onto <release> <old base> main
```

- `<release>` is the ref the request names. The request's words carry both the ref and the
  commit of the target line ([which checks can be drift](#which-checks-can-be-drift)), and a
  newer release updates both in place.
- `<old base>` is the third line of the record.
- The worker resolves conflicts, keeps history linear, and drops a patch the release already
  contains, naming it in its report.
- The worker never runs `bin/pi-forks rebase` or `bin/pi-forks export`. Both stay refused in a
  drain run, and this design does not change that.

This is what the pi rebase onto `v1.6.0` did on 2026-11-21. Its worker's recorded command was
`git -c rerere.enabled=true rebase --reapply-cherry-picks --empty=stop --onto v1.6.0 b4afdddf44e059c67ccf5097e5317d913521b470 main`,
and `b4afdddf44e059c67ccf5097e5317d913521b470` is the third line of `yolo-packs/fork-pins/pi`.

**The lander's part** is unchanged in what it accepts: it checks the head as the first row of
the table says, and moves the patch branch. It gains one step, below.

**The request is a mixed item: a fork part, then a repository part.** The route exists. For an
item whose recorded delivery is `both`, the drain records the fork delivery and only then starts
a separate run for the repository part (`checkDeliveryOrder` and `settlePartialDelivery` in
`tools/tillr-drain/drain/delivery.go`). The generated request uses it:

1. **Fork part.** The worker rebases as above. The fork's gate builds that head. The lander
   delivers it to `refs/heads/patch/<fork>`.
2. **Repository part.** A second run, in a repository lane, rewrites the record. Its fence is
   the one path `yolo-packs/fork-pins/<fork>`. The three lines are:

   | Line | Value | How the worker reads it |
   | :--- | :--- | :--- |
   | 1 | The delivered commit | `git rev-parse refs/heads/patch/<fork>` |
   | 2 | The remote | Unchanged from the file |
   | 3 | The release's commit | Copied from the request's words, after `git merge-base --is-ancestor <commit> refs/heads/patch/<fork>` exits 0 |

   The release's tag is not a ref in this repository, so line 3 cannot be resolved by name
   there; the commit itself is present, because the delivered patch branch contains it. The
   ordinary gate plans `patch-branch-inv2` for this candidate, because the record is one of the
   check's watched paths. The check refuses a record whose third line is not an ancestor of its
   first (`load_series` in `bin/pi-forks`), and it now judges the rebased commit against the
   release.
3. **The request closes when the repository part lands.**

The order cannot be reversed. A record that names a commit no delivery has made is a record the
gate cannot fetch.

**What the lander gains: it puts the delivered commit into the gate's copy of the fork.** When
the lander delivers a fork head, it fetches that commit into `.forks/inputs/<fork>` from the
repository, as `export_branch` does from a lane checkout. Without this the repository part's
gate could not judge until the rewritten branch is published, and publication waits for the
owner's acknowledgment and a command on the host. The request would then wait on a person for
a step that needs no decision, which
[rule 7 of the unattended drain](unattended-drain.md#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves)
excludes. The fetch is from the local repository, so it adds no network read to a landing.
Publication stays what it is today: separate from the record, and the owner's to acknowledge.

**Between the two landings** the trunk's record still names the old commit, so the check is
still red with the same answer on every candidate that reaches it. The table at the top of this
rule keeps applying: a candidate that does not touch the fork lands with the failure inherited,
and one that does waits on the request. The request's own repository part is not held behind
itself, because its candidate is the one that makes the check pass.

**When the fork part cannot be finished**, the worker ends with an obstacle as any worker does.
A conflict that needs a product decision is the `owner` obstacle. Nothing else in this route
names a person.

Done looks like this: upstream publishes a release that conflicts with a fork's patches, the
drain files one request, a worker's rebased head is delivered to the patch branch, a second run
lands a one-file change to the record, and the request closes with no operator action. A
candidate that reaches `patch-branch-inv2` after that landing sees it pass.

Rejected, as the designer's decision:

- **The operator rebases.** The request that opened this part excludes it, citing
  [rule 7](unattended-drain.md#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves)
  as an owner ruling of 2026-11-21: a hold names the operator only for a decision the rulebook
  reserves, and a rebase onto a release reserves none.
- **The lander rebases.** A rebase can conflict, and settling a conflict is a worker's
  judgment that a gate must then see. The lander delivers what was gated and decides nothing.
- **Allowing `bin/pi-forks rebase` in a drain run.** That command also exports, which moves the
  patch branch and the record before any gate has run.
- **The lander writes the record when it delivers.** The record is a tracked file, so writing it
  is a trunk commit. The lander would be landing a change no gate judged.
- **Closing on the patch branch delivery.** The check would still be red, so every candidate
  that touches the fork would be released into a refusal.
- **Pointing the check at the patch branch instead of the record.** The record is what makes
  the check answer the same way in every worktree
  ([how a patch branch is resolved](../pi/forks.md#how-a-patch-branch-is-resolved)), and
  changing it would change the ordinary gate.

**What this leaves undone.** An ordinary fork delivery, one that only adds commits, also leaves
the record behind the patch branch. There the check stays green, because the older commit still
merges, so nothing waits on it. Keeping the record current after every fork delivery is not
designed here.

### The checks that could not judge

Rewritten on 2026-11-21 to answer a build worker's finding; the tree was read at `746f87e2b3`.
It is the designer's decision, `D-FG4`, and the owner may overturn it.

The finding: this part said such a check "takes the tooling-defect path", and that the path was
unchanged. No gate answer takes that path. A tooling-defect item is filed only when a worker's
report names a tooling obstacle (`fileDefectItem`, called from
`tools/tillr-drain/drain/obstacle_route.go`). The earlier wording was the designer's error.

A remote that does not serve a pinned commit is an unavailable resource, not drift. Such a check
is planned only when a candidate's diff reaches it or a fixer names it, so it no longer holds a
repair that does not touch the fork. Where it is planned, `D-FG4` keeps what the tree does today
and adds no route:

| Where the check could not judge | What happens, unchanged |
| :--- | :--- |
| An ordinary candidate's gate, with no verdict reached | The candidate is held as `gate-no-verdict` and the next pass gates the same candidate again (`holdGateNoVerdict` in `gate_no_verdict.go`). The mover is the loop |
| An ordinary candidate's gate, when the gate states an error of its own | The item is held as a gate fault, `gate-harness-error`, with the gate's words as the reason (`gateFault` in `item_fault.go`) |
| A fixer's named check | The fixer is held as a gate fault, `gate-harness-error`, naming the checks that could not judge (`fixer_gate.go`). Built with `D-FG1` |
| The replay of a red `patch-branch-inv2` check on the candidate's base | The red is not classified as drift. No request is filed and nothing is inherited; the candidate is handled as the gate's own answer says, by the rows above or as an ordinary refusal |

- **No gate answer files a tooling-defect item.** That stays the route for a worker's reported
  tooling obstacle and nothing else.
- **What `D-FG4` asks of the build** is the last row only: the drift classification treats
  "could not judge" on the base as a third answer, never as "red, same answer" and never as
  "passes".
- **The name of what was lacking** is the check's own output in the gate log. No new record is
  added for it.

Rejected, as the designer's decision:

- **Filing a defect item from a gate's "could not judge".** The cause is usually outside the
  repository (a remote, a network), so the item would have no fence a worker could repair in.
- **Changing the fixer's hold to match the ordinary candidate's re-gate.** It is built and
  works; the difference is recorded here, not removed.

> [!WARNING]
> Do not make the fixer's gate pass by skipping fork checks by name. The repair is the scope:
> a fixer whose diff does touch a series is still judged on that series, like any change.

## Who is told when an upstream moves

Settled here, as the designer's decision:

| Who | How |
| :--- | :--- |
| The queue | The rebase request is the notice. It exists from the first observation |
| A candidate that touches the same fork | Its hold names the rebase request as its waiter |
| A candidate that lands with the failure inherited | Its verdict records the check, the fork and the release |
| The operator | The loop prints one line when the request is filed, and the waiting face lists the request with its fork and release |
| A fixer's worker | Nothing. The drift is not in its brief unless its fence includes that fork |

For [a fork that follows a branch head](#a-fork-that-follows-a-branch-head) no request exists.
The inherited record on the candidate and the loop's one line are the notice.

The owner is told by the queue only. He ruled this on 2026-11-21 ([OQ-FG1](#OQ-FG1)): the rebase
request appears in the queue and on the waiting list like any item, and he is asked something only
if the rebase hits a conflict an agent cannot settle. No owner-facing row and no hold addressed to
him is built for drift.

## What this does not change

- What the lander accepts. Its release-only rule for a rewritten patch branch stays, which is
  why [a head follower](#a-fork-that-follows-a-branch-head) gets no rebase request. It gains one
  step after a fork delivery, the fetch into the gate's copy of the fork
  ([who rebases](#who-rebases-and-how-the-recorded-commit-follows)).
- The refusal of `bin/pi-forks rebase` and `bin/pi-forks export` in a drain run.
- The tooling-defect route. It is still entered only by a worker's reported tooling obstacle.
- The proof, the green pointer, automatic revert and the way a fixer is filed, attached and
  resolved. [A fixer never waits on itself](#a-fixer-never-waits-on-itself) uses the existing
  attachment and changes only what happens to the fixer after it: returned to its worker, not
  held.
- The ordinary gate, for any candidate.
- The drift probe's design. It remains the gate rework's unit and is still the right discoverer;
  until it is built, drift is seen only when a change touches a series or a pack declaration.
- `item-gate-pinned-fork-inputs`, which changes how fork checks read their pinned inputs.
- State that already exists. An open fixer is gated by the new rule the next time it is gated.
  No record is migrated.

## Alternatives

| Alternative | Verdict |
| :--- | :--- |
| Keep the untiered range and acquit the fixer of checks red on its base | Rejected. It still runs every check in the range before deciding, so the cost still grows with the red range, and "could not judge" still holds the fixer |
| Keep the untiered range and exclude upstream readers from it | Rejected. It fixes the incident and keeps the second group of [the table above](#what-the-untiered-range-bought), which the proof already judges |
| The ordinary gate alone, with no named checks | Rejected. A fixer that repairs nothing would land, and a fixer's landing is never reverted |
| Wait for the drift probe | Rejected as the only fix. The probe waits on other work, and the fixer's range is wrong for tree checks too |

## Risks

| Risk | Mitigation |
| :--- | :--- |
| A second red in the range is found one proof later than today | The proof names it and files its fixer, as for any red; the first fixer has already landed, so the range is shorter |
| Drift goes unseen while no change touches a fork | Stated above; the probe closes it. A launch on the host still holds the last good build |
| A named check's selector is missing from the fixer's record | The gate answers "could not judge" naming the check; it never passes by omission |
| A head follower drifts and no queue item says so | One fork is a head follower. Its drift is on the verdict of every candidate that inherits it and in one loop line; the rebase is the operator's step. A lander route for it is separate design |
| A candidate reaching a check whose remote stays unavailable is gated again every pass | This is today's behavior and `D-FG4` keeps it. `item-gate-pinned-fork-inputs` changes how those checks read their pinned inputs |
| A fixer's named list grows while it works, because unnamed reds its diff reaches attach to it | Only a check whose read paths the fixer's own diff changes can attach this way, and only when no other fixer can be waited on. The existing limits on returned refusals bound the loop |

## Open Questions

None. The owner ruled the one question on 2026-11-21; it is a row in the [Decision Ledger](#decision-ledger).

## Decision Ledger

The `D-FG` rows are the designer's decisions of 2026-11-21, made from the request that opened
this design; the owner may overturn each by comment. The [OQ-FG1](#OQ-FG1) row is his ruling.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-FG1"></a>[OQ-FG1](#OQ-FG1) | Owner ruling: when an upstream release breaks a patch series the owner is told by the queue only. No owner-facing row and no hold. | 2026-11-21 | [Who is told when an upstream moves](#who-is-told-when-an-upstream-moves) | — |
| `D-FG1` | A trunk fixer's gate is the ordinary gate over its own diff plus the checks it names, run on its tree. The untiered range since the last green is removed. Structural fix. (Designer's decision.) | 2026-11-21 | [The fixer's gate](#the-fixers-gate) | Built |
| `D-FG2` | A fixer stays excluded from acquittal only for the checks it names; for any other check it is judged as any candidate is, except that it is never held behind itself or behind a fixer that waits on it: such a check is attached to the fixer and the refusal returns to its worker. No second fixer is filed for the same commit. (Designer's decision; the exception was added on 2026-11-21 to answer a build worker's finding that the ordinary route made a fixer wait on itself.) | 2026-11-21 | [The fixer's gate](#the-fixers-gate), [A fixer never waits on itself](#a-fixer-never-waits-on-itself) | Built |
| `D-FG3` | A `patch-branch-inv2` check red on a candidate and on its base with the same refusal against the same target ref, on a candidate whose diff does not include that fork's own paths, is drift: the candidate lands with the failure inherited and one rebase request per fork and release is filed. No trunk fixer is filed for drift. The request is filed only for a fork that follows releases. For a fork that follows a branch head the candidate still lands with the failure inherited, no item is filed, one loop line is printed, and a candidate that touches that fork is refused as today, because the lander delivers a rewritten patch branch only for a release. Structural fix. (Designer's decision, carrying out `D-GR12` of the gate rework, itself a designer's decision; the head-follower rule was added on 2026-11-21 to answer a build worker's finding that `pi-background-tasks` follows `head`. Narrowed on 2026-11-21, as the designer's decision, to answer a build worker's finding that `series` is planned per pack without a fetch and that `reads_upstream` matches checks that read no upstream: only `patch-branch-inv2` can be drift, selected by kind name; the fork is the check's `--fork` argument and the release is the ref `bin/pi-forks patch-branch target` prints; `series`, `patch-branch-inv3`, `forks-check`, `build` and `fork-quality` are never drift; the ordinary gate's plan is unchanged. Amended on 2026-11-21, as the designer's decision, to answer a build worker's finding that the lander delivers a rebased head without rebasing and that the check reads `yolo-packs/fork-pins/<fork>`, not the patch branch: the request's worker rebases the fork in its lane checkout with `git rebase --onto <release> <old base> main`; the request is a mixed item whose fork part is delivered first and whose repository part then rewrites `yolo-packs/fork-pins/<fork>`; the lander fetches each delivered fork commit into the gate's copy of the fork; the request closes when the repository part lands. The operator takes no step: the request that opened the amendment cites rule 7 of the unattended drain as an owner ruling of 2026-11-21.) | 2026-11-21 | [Upstream drift](#upstream-drift-is-a-rebase-request-not-a-refusal), [Which checks can be drift](#which-checks-can-be-drift), [A fork that follows a branch head](#a-fork-that-follows-a-branch-head), [Who rebases, and how the recorded commit follows](#who-rebases-and-how-the-recorded-commit-follows) | — |
| `D-FG4` | A fork check that could not judge is neither drift nor a verdict. It holds only a candidate whose diff reaches it or a fixer that names it, by the holds the tree already has: an ordinary candidate is held as `gate-no-verdict` and gated again, or as a gate fault when the gate states its own error; a fixer's named check is a gate fault. No gate answer files a tooling-defect item. The one thing to build is that "could not judge" on the base is never classified as drift. (Designer's decision; corrected on 2026-11-21 to answer a build worker's finding that the "tooling-defect path" the earlier wording named is entered only by a worker's reported obstacle.) | 2026-11-21 | [The checks that could not judge](#the-checks-that-could-not-judge) | — |
| `D-FG5` | The queue, the affected candidates and the operator's surfaces are told of drift by the rebase request and one loop line; for a fork that follows a branch head, by the inherited record and one loop line. Nothing owner-facing is built until [OQ-FG1](#OQ-FG1) is ruled. (Designer's decision.) | 2026-11-21 | [Who is told](#who-is-told-when-an-upstream-moves) | — |
