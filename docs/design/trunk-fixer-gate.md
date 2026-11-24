---
title: "A trunk fixer is gated on what its repair reaches and on the checks it was filed to repair"
status: in-review
stage: DESIGN
updated: 2026-11-21
tags: [tillr, gate, landing, drain, forks, design]
summary: "The trunk fixer's gate stops being the untiered gate over every landing since the last green commit. It becomes the gate any candidate gets, plus the checks the fixer names, run on the fixer's tree. A fork check that is red only because an upstream published a release is reported as one rebase request per fork and release and refuses nobody whose change does not touch that fork; a fork that follows a branch head gets the same acquittal and no request. One question is open for the owner: whether he is told, beyond the queue, when an upstream moves."
next: "The separate drift probe is not built and has no design of its own. No owner-facing row or hold is built for drift."
depends-on:
  - landing-principles.md
  - ../reference/gate.md
vantage:
  status-chip: true
---

# A trunk fixer is gated on what its repair reaches and on the checks it was filed to repair

**Status:** The fixer's gate and upstream drift settlement are built (`D-FG1` through `D-FG4`). No owner-facing notification is built.

> **In short.** A gate over every landing since the last green commit would make the seat that
> restores a red trunk the only seat an outside release can stop. Its
> gate is the ordinary gate plus the checks it names, and upstream movement is a filed
> rebase request instead of a refusal.

**Why it matters.** Under a range gate, a correct repair is refused by fork checks it does not
touch, and the trunk stays red for a cause outside the repository.

**The shape.** Three rules: the [fixer's gate](#the-fixers-gate), the
[drift rule](#upstream-drift-is-a-rebase-request-not-a-refusal) and
[who is told](#who-is-told-when-an-upstream-moves).

**Cost.** A fixer does not re-judge other landings before it merges; the proof after the merge
is the only judge of them. Upstream drift is noticed later until the drift probe is built.

**Start at** [the fixer's gate](#the-fixers-gate).

**Needs your ruling:** nothing. The owner ruled [OQ-FG1](#OQ-FG1) on 2026-11-21. `D-FG1` to `D-FG5` in the
[Decision Ledger](#decision-ledger) are the designer's and his to overturn.

**Reads with:** [the gate reference](../reference/gate.md) (the gate
whose fixer path this design changes) and [landing principles](landing-principles.md).

---

## Terms

- **Trunk fixer.** The generated item `fixer-main-<sha>` that the drain files when a trunk check
  is red. It names the failing check ids. It is not a revert and not an ordinary queue item.
- **Proof.** The run after each merge that judges the trunk tip and moves the green pointer
  (the heavy runner of [the gate reference](../reference/gate.md#after-merge-proof-and-the-green-pointer)).
- **Upstream reader** *(coined here)*. A check whose verdict depends on what an upstream
  repository currently publishes, not only on the commit under judgment: every fork check
  declared not reusable. That is the set the proof leaves out and the fixer's
  gate drops. The set is wider than the checks that fetch: only `patch-branch-inv2`
  fetches the followed upstream, and only it can be drift. An upstream reader is not a check
  that merely needs the network to install dependencies.
- **Upstream drift** *(coined here)*. A `patch-branch-inv2` check that is red on a candidate, red
  on the candidate's base with the same answer, on a candidate that does not touch that fork.
  [Which checks can be drift](#which-checks-can-be-drift) defines "same answer" and "touches the
  fork". It is not a red the candidate caused, and it is not a check that could not run.
- **Rebase request** *(coined here)*. One generated fork item that names a fork, the upstream
  release its series does not merge, and the failing question. It is not a trunk fixer: the
  trunk commit is not wrong, and the repair is made in the fork's checkout.
- **Delivery branch** *(coined here)*. `refs/heads/patch/<fork>`, the branch the lander moves
  when it delivers a fork head.
- **Pin file** *(the code's word)*. The tracked three-line file that records a fork's delivered
  commit, the remote it is fetched from, and the upstream commit its changes sit on (`Pin` in
  `tools/tillr-drain/remotesync/fork_drift.go`). This design also calls it "the record".
- **Fork driver** *(the code's word)*. The program the repository supplies that sets up a fork
  checkout, reports a fork's target and exports a fork. It is outside tillr.

## What the untiered range bought

An untiered range, meaning every check any landing since the last green commit selects, would
re-judge three groups of checks before the fixer merges:

| Group | Who else judges it | Lost by dropping it |
| :--- | :--- | :--- |
| Checks the fixer's own diff selects | The ordinary gate | Nothing; they stay |
| Tree checks that other landings since the last green select | The proof, on the tip, after the fixer lands | An earlier answer only. A second red is found by the proof and gets its own fixer, as any red does |
| Upstream readers that other landings select | Nobody | A verdict about the upstream, charged to a change that did not touch it |

The third group is the only coverage unique to the fixer's gate, and it is the coverage
[upstream drift](#upstream-drift-is-a-rebase-request-not-a-refusal) shows is not a
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
   selectors the proof recorded for them (the fixer-gate program receives each as
   `--only <proof selector>`). This
   includes a check of the rolling tier, which the ordinary gate would defer to the proof.

The second part exists because the fixer's claim is that it repairs those checks, and the
ordinary gate does not test that claim: the diff may not select the check, or the check may be
deferred. A fixer that lands without repairing leaves the trunk red and is never reverted, so
the claim is tested before the merge.

Specified cases:

- **No range since the last green is read.** The `{green}` value is not part of the
  fixer's gate. The fixer is rebased onto the tip before it is gated.
- **A fixer that names no check** runs the ordinary gate alone. It is resolved only by a whole
  green proof.
- **A named check fails.** The gate refuses, and the refusal goes back to the fixer's own worker
  naming the test. This is a verdict.
- **A named check could not judge.** The gate answers "could not judge", never pass and never
  fail, and names the check and what it lacked. The fixer is held as a gate fault (the
  `gate-harness-error` stop), and no defect item is filed for it
  ([what "could not judge" does](#the-checks-that-could-not-judge)). A fixer is not landed on a
  check nobody could run.
- **A named check that is an upstream reader** is not run as a named check. A red upstream
  reader is a change's own failure or, for `patch-branch-inv2` alone, drift, decided by the [drift rule](#upstream-drift-is-a-rebase-request-not-a-refusal),
  and no trunk fixer is filed for it. A fixer that names one keeps
  its other checks, and that name is dropped when its gate is composed, with a line saying so.
- **The inherited-failure rule applies to checks the fixer does not name.** The fixer stays
  excluded from acquittal for the checks it names, which is the whole reason for the
  exclusion. For any other check it is judged as any candidate is, with the two exceptions in
  [a fixer never waits on itself](#a-fixer-never-waits-on-itself).
- **A refusal that mixes named and unnamed checks is a verdict.** When any check the fixer
  names is among the failing checks, nothing is acquitted and the whole refusal goes back to
  the fixer's worker. The inherited-failure rule is tried only when every failing check is one
  the fixer does not name.
- **Order and concurrency.** One gate run per candidate. The named checks run after
  the ordinary gate passes; when the ordinary gate refuses, they do not run.
- **Forbidden.** The gate's own code stays outside a fixer's fence. Nothing here lets a fixer
  choose, narrow or rename the checks it is judged on: the list comes from the fixer's record.

### A fixer never waits on itself

This is the designer's decision, part of `D-FG2`, and the owner may overturn it.

The ordinary inherited-failure rule has two outcomes for a check that is red on the candidate's
base and on the tip (`settleTrunkAlreadyRed` in `tools/tillr-drain/drain/trunk.go`). When the
candidate's diff changes no path the check reads, the candidate lands with the failure recorded
as inherited. When it does change such a path, the red is attributed to a commit, the fixer for
that commit is filed or attached to, and the candidate is held behind that fixer.

Applied to a fixer without change, the second outcome can name the fixer itself. There is one
fixer per red commit (`fileTrunkFixerNoted` in the same file), so when the unnamed check is
attributed to the commit the fixer was filed for, the check attaches to that same fixer and the
fixer is then held with itself as its waiter. Nothing ever releases that hold.

The rule for a fixer whose gate fails only on checks it does not name, all red on its base and
on the tip:

| The fixer's diff and the check's read paths | The fixer the ordinary rule would hold it behind | Outcome |
| :--- | :--- | :--- |
| Do not intersect | Not asked | The fixer lands with the failure recorded as inherited, as any candidate does |
| Intersect | A different fixer, which does not wait on this one | The fixer is held behind that fixer, as any candidate is |
| Intersect | This fixer itself | The check is attached to this fixer and the refusal goes back to its worker. No hold is recorded |
| Intersect | A different fixer that already waits on this one, directly or through other fixers | The same as the row above: attached to this fixer, returned to its worker, no hold |

**Attached** means the fixer's record gains the check's id in its list of
red checks and the check's watched paths in its fence, with the gate's own code removed, and the
attachment is recorded as an event.

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
- **Exempting a fixer from the inherited-failure rule in whole,** which `D-FG2` rejects.

Done looks like this: with the trunk red on one test and an upstream release that conflicts with
a delivery branch, a fixer whose diff touches only that test's files passes its gate and lands, and
its gate log lists the ordinary checks and the named check and no fork check.

## Upstream drift is a rebase request, not a refusal

A question about a moving upstream is not a verdict about a commit. The standing decision is
that `series` and `patch-branch-inv2` leave both tiers and run as a drift probe, at loop start
and after a landing that changes a series or a pack declaration; the probe files an item and
never moves or holds the green pointer. The probe is not built. This design
keeps that decision and says what happens until it is built, for every candidate and not only
a fixer.

When a gate plans a `patch-branch-inv2` check and it is red, the loop replays it on the
candidate's base, which the replay step does for every red check.
[Which checks can be drift](#which-checks-can-be-drift) says why it is that kind alone. Then:

| The check on the base | The candidate's diff and the fork's own paths | Outcome |
| :--- | :--- | :--- |
| Passes | Any | The candidate's own failure. Ordinary refusal |
| Red, same answer | Do not intersect | Drift. The candidate lands with the failure recorded as inherited. One rebase request is filed |
| Red, same answer | Intersect | Drift, and the candidate changes the same fork. One rebase request is filed, and the candidate waits on it by name unless it is that request's own work |
| Could not judge | Any | Not drift and not a verdict. "Could not judge", named |

No trunk fixer is filed for drift, because no trunk commit is at fault and the proof does not
judge these checks.

The table's first, second and fourth rows read the same for every fork, whatever it follows and
however it is delivered. What is filed, and what the third row does, depend on both:

| The fork | What is filed | Third row (the candidate touches the fork) |
| :--- | :--- | :--- |
| Follows releases and is delivered as a delivery branch | One rebase request | The candidate waits on the request |
| [Follows a branch head](#a-fork-that-follows-a-branch-head) | Nothing | Ordinary refusal |
| [Still commits a series directory](#a-fork-that-still-commits-a-series-directory) | Nothing | Ordinary refusal |

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
  recorded commit and whose gate passed `patch-branch-inv2` against that release. A newer release updates the same open request and its delivery pieces.
  Delivering the delivery branch alone does not close it
  ([why](#who-rebases-and-how-the-recorded-commit-follows)).
- **It is filed only for a fork that follows releases and is delivered as a delivery branch**
  (`Requestable` in `tools/tillr-drain/remotesync/fork_drift.go`).
  Both tests are read from the tree, not from the wording of the declaration:
  1. *Follows releases* means the target line reports the kind `release`
     ([which checks can be drift](#which-checks-can-be-drift)). A declaration with no `follow`
     key, with `"follow": "release"` and with `"follow": "release:<prefix>"` all report that
     kind, and all three count. The absence of the key
     is not the test.
  2. *Delivered as a delivery branch* means the fork's entry in the repository's fork list names
     `patch/<name>`.
     [A fork that still commits a series directory](#a-fork-that-still-commits-a-series-directory)
     gets no request.

### Which checks can be drift

This is the designer's decision, part of `D-FG3`, and the owner may overturn it.

The `series` check is planned per pack, with no fork selector and no fetch, so it cannot name
a fork or a release. The upstream readers also include `patch-branch-inv3`,
`forks-check`, `build` and `fork-quality`, which answer about the tree or a build, so using that
set to classify drift would call an export mismatch or a compile failure drift. The set stays
what the proof and the fixer's gate use to leave checks out.

**Only `patch-branch-inv2` can be drift.** It is selected by its kind name (`ForkDriftCheck` in
`tools/tillr-drain/remotesync/fork_drift.go`), never by the upstream-reader set.

| Check kind | Can it be drift | Why |
| :--- | :--- | :--- |
| `patch-branch-inv2` | Yes | It is planned once per fork, with `--fetch --fork <name>`, and asks whether that fork's delivery branch merges the upstream it follows |
| `series` | No | It is planned per pack and reads a recorded answer without fetching. It is also not planned for a pack whose forks are all delivered as delivery branches |
| `patch-branch-inv3`, `forks-check`, `build`, `fork-quality` | No | Each answers about the series, a clean checkout of it, or its build. None reads what an upstream published since |

A red on a kind marked "No" that is also red on the base is handled by the
acquittal for a trunk that is already red or by an ordinary refusal. This design adds
nothing for it. The ordinary gate's plan is unchanged: no check is added, removed or given a new
argument.

**The fork** is the one the check's own `--fork` argument names.

**The target** is what the fork driver's target command prints for that fork
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
   `conflicts with`.
2. The replay on the base carries the same phrase.
3. Both name the same target ref.

A red without that phrase, on either side, is the driver failing to answer. It is
[a check that could not judge](#the-checks-that-could-not-judge). A different ref on the two
sides means the upstream moved between the two runs; the candidate is gated again and nothing is
classified.

**"The fork's own paths"** are the fork's pin file and, for a fork that still commits
a series directory, the paths under it (`Touches` in
`tools/tillr-drain/remotesync/fork_drift.go`). A candidate touches the fork when its diff includes one
of them. The check's `watches` are not the test: they name the whole pack directory and
the fork driver, and the landing tier plans the check only when the diff matches one of them, so
by that test every planned check would "intersect" and the acquittal would never apply. A
candidate that edits another part of the same pack, or the pack's declaration without changing
this fork's target, does not touch the fork; a declaration edit that changes the target makes
the two sides name different refs or makes the base pass, and both are covered above.

Rejected, as the designer's decision:

- **Giving `series` a fork and a release.** It would need a fork selector or a fetch added to a
  check the ordinary gate plans, which changes the ordinary gate.
- **Classifying by the upstream-reader set.** It names checks that read no upstream.
- **Reading the fork and release from the check's printed sentence alone.** The target command
  prints the same resolution as one machine-readable line, with the kind.

### A fork that follows a branch head

This is the designer's decision, part of `D-FG3`, and the owner may overturn it.

"One per fork and release" has no answer for a fork that is neither pinned nor a
release follower, such as one whose upstream cuts no tags and whose declaration says
`"follow": "head"`. The lander delivers a rewritten delivery
branch only for a release: `releaseRebase` in `tools/tillr-land/land/fork_rebase.go` accepts
the rewrite only when the followed target's kind is `release`, and any other rewrite is refused
as `patch-rewind`. A rebase request for a head follower would therefore be an item no drain
landing can close, and a candidate held behind it would never be released.

The rule for a fork whose declaration says `follow: "head"`:

| The check on the base | The candidate's diff and the fork's own paths | Outcome |
| :--- | :--- | :--- |
| Passes | Any | The candidate's own failure. Ordinary refusal |
| Red, same answer | Do not intersect | Drift. The candidate lands with the failure recorded as inherited. **No item is filed** |
| Red, same answer | Intersect | Ordinary refusal, returned to the candidate's worker. No item is filed and nothing is waited on |
| Could not judge | Any | Not drift and not a verdict, as for any fork |

- **The inherited record** names the check, the fork, the followed branch and the upstream
  commit the check read, in place of a release.
- **The loop prints one line** per fork and upstream commit, the first time a candidate lands
  with that drift inherited. That line and the candidate's verdict are the whole notice.
- **No rebase request, no trunk fixer and no hold** come from head-follower drift.
- **The rebase stays the operator's step.** The lander's release-only rule is unchanged.

**Why no request is filed.** A request is useful only when the queue can close it. For a head
follower nothing in the drain can, so filing one would add an item that never lands and a hold
that never releases. The acquittal in the second row is the part of `D-FG3` that keeps an
unrelated change landing, and it does not need the item.

**What this leaves undone.** A candidate that does touch a head follower's series or its pack
declaration while that fork has drifted is refused, and its worker cannot rebase
the fork. A delivery route for a head follower's rebase belongs to
the lander and is not designed here.

Rejected, as the designer's decision:

- **Filing the request keyed by fork and upstream commit.** The lander refuses its delivery, so
  the item and every candidate held behind it would wait with no mover.
- **Widening the lander to accept a head-follower rewrite inside this decision.** It changes
  what the lander will rewrite on a published branch, which is a separate design.
- **Leaving head-follower drift on the ordinary path in whole.** A change that does not touch
  the fork would still be refused for it, which is the refusal this design removes.

### A fork that still commits a series directory

This is the designer's decision, part of `D-FG3`, and the owner may overturn it.

For a fork whose list entry names a series directory, the check never reads the pin file, so a
change to the pin file cannot make `patch-branch-inv2` pass.

The rule for a fork whose entry in the repository's fork list does not name `patch/<name>`:

| The check on the base | The candidate's diff and the fork's own paths | Outcome |
| :--- | :--- | :--- |
| Passes | Any | The candidate's own failure. Ordinary refusal |
| Red, same answer | Do not intersect | Drift. The candidate lands with the failure recorded as inherited. **No item is filed** |
| Red, same answer | Intersect | Ordinary refusal, returned to the candidate's worker. No item is filed and nothing is waited on |
| Could not judge | Any | Not drift and not a verdict, as for any fork |

- **The inherited record** names the check, the fork and the release, as for any release
  follower.
- **The loop prints one line** per fork and release, the first time a candidate lands with that
  drift inherited. That line and the candidate's verdict are the whole notice.
- **No rebase request, no trunk fixer and no hold** come from it.
- **The rebase stays the operator's step**, which re-exports the series
  directory.
- **The delivery form is tested before a request is filed.** A request is filed only when
  the fork is delivered as a delivery branch; the target kind `release` alone is not enough.

**Why no request is filed.** The request's repository part is defined as a one-file change to
the record, and for this delivery form the check does not read the record. The item would
deliver a fork head and land a record change, and the check would still be red, so the request
could never close and every candidate held behind it would wait with no mover. This is the same
reasoning as for [a head follower](#a-fork-that-follows-a-branch-head).

**What this leaves undone.** A repository part that
rewrites a committed series directory inside a drain run is not designed here.

Rejected, as the designer's decision:

- **A repository part that re-exports the series directory.** The only writer of a series is
  the fork driver's export, which is refused in a drain run, and this design keeps that refusal.
  It would be a second delivery route.
- **Filing the request anyway, with the record as its fence.** Shown above not to close.
- **Leaving this drift on the ordinary path in whole.** A change that does not touch the fork
  would still be refused for it.

### Who rebases, and how the recorded commit follows

This is the designer's decision, part of `D-FG3`, and the owner may overturn it.

**The fork's recorded commit** is the fork's pin file: three lines holding the
delivered commit, the remote it is fetched from, and the upstream commit the fork's changes sit on.
This part calls it "the record".

| Question | Answer | Where |
| :--- | :--- | :--- |
| Does the lander rebase? | No. It accepts a rewritten head only when the followed release is already an ancestor of the candidate head, is not an ancestor of the base, and the range holds no merge. It then moves `refs/heads/patch/<fork>` | `releaseRebase` and `moveForkRebase` in `tools/tillr-land/land/fork_rebase.go` |
| What commit does `patch-branch-inv2` judge? | The first line of the record, not the delivery branch. The check reads the recorded commit from the gate's own copy of the fork | The fork driver |

So a delivered rebase changes nothing the check reads. The record still names the old commit,
and the check stays red against the release until the record moves.

**Who rebases: the request's worker.** The worker on the request's fork part produces the
rebased head, in the lane's own fork checkout, on `main`:

```bash
git rebase --onto <release> <old base> main
```

- `<release>` is the ref the request names. The request's words carry both the ref and the
  commit of the target line ([which checks can be drift](#which-checks-can-be-drift)), and a
  newer release updates both in place.
- `<old base>` is the third line of the record.
- The worker resolves conflicts, keeps history linear, and drops a patch the release already
  contains, naming it in its report.
- The worker never runs the fork driver's rebase or export. Both stay refused in a
  drain run, and this design does not change that.

**The lander's part** is unchanged in what it accepts: it checks the head as the first row of
the table says, and moves the delivery branch. It has one more step, below.

**The request is a mixed item: a fork part, then a repository part.** For an
item whose recorded delivery is `both`, the drain records the fork delivery and only then starts
a separate run for the repository part (`checkDeliveryOrder` and `settlePartialDelivery` in
`tools/tillr-drain/drain/delivery.go`). The generated request uses it:

1. **Fork part.** The worker rebases as above. The fork's gate builds that head. The lander
   delivers it to `refs/heads/patch/<fork>`.
2. **Repository part.** A second run, in a repository lane, rewrites the record. Its fence is
   the one path of the fork's pin file. The three lines are:

   | Line | Value | How the worker reads it |
   | :--- | :--- | :--- |
   | 1 | The delivered commit | `git rev-parse refs/heads/patch/<fork>` |
   | 2 | The remote | Unchanged from the file |
   | 3 | The release's commit | Copied from the request's words, after `git merge-base --is-ancestor <commit> refs/heads/patch/<fork>` exits 0 |

   The release's tag is not a ref in this repository, so line 3 cannot be resolved by name
   there; the commit itself is present, because the delivered delivery branch contains it. The
   ordinary gate plans `patch-branch-inv2` for this candidate, because the record is one of the
   check's watched paths. The check refuses a record whose third line is not an ancestor of its
   first, and it then judges the rebased commit against the
   release.
3. **The request closes when the repository part lands.**

**Operator's decision, 2026-11-21; the owner may overturn it.** If the delivered head already
contains the named release, the prescribed rebase leaves that head unchanged. In that case the
request has no fork part: it goes directly to the repository part and updates the pin from the
existing delivery. Only pin-backed forks get a generated request; a committed series directory
gets none. Both `follow: release` and `follow: release:<prefix>` are release followers, as is the
default release rule.

The order cannot be reversed. A record that names a commit no delivery has made is a record the
gate cannot fetch.

**The lander puts the delivered commit into the gate's copy of the fork.** When
the lander delivers a fork head, it fetches that commit into the gate's own copy of the fork from the
repository (`tools/tillr-land/land/fork_cache.go`). Without this the repository part's
gate could not judge until the rewritten branch is published, and publication waits for the
owner's acknowledgment and a command on the host. The request would then wait on a person for
a step that needs no decision, which
[rule 7 of the unattended drain](unattended-drain.md#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves)
excludes. The fetch is from the local repository, so it adds no network read to a landing.
Publication stays separate from the record, and the owner's to acknowledge.

**Between the two landings** the trunk's record still names the old commit, so the check is
still red with the same answer on every candidate that reaches it. The table at the top of this
rule keeps applying: a candidate that does not touch the fork lands with the failure inherited,
and one that does waits on the request. The request's own repository part is not held behind
itself, because its candidate is the one that makes the check pass.

**When the fork part cannot be finished**, the worker ends with an obstacle as any worker does.
A conflict that needs a product decision is the `owner` obstacle. Nothing else in this route
names a person.

Done looks like this: upstream publishes a release that conflicts with a fork's patches, the
drain files one request, a worker's rebased head is delivered to the delivery branch, a second run
lands a one-file change to the record, and the request closes with no operator action. A
candidate that reaches `patch-branch-inv2` after that landing sees it pass.

Rejected, as the designer's decision:

- **The operator rebases.** The request that opened this part excludes it, citing
  [rule 7](unattended-drain.md#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves)
  as an owner ruling of 2026-11-21: a hold names the operator only for a decision the rulebook
  reserves, and a rebase onto a release reserves none.
- **The lander rebases.** A rebase can conflict, and settling a conflict is a worker's
  judgment that a gate must then see. The lander delivers what was gated and decides nothing.
- **Allowing the fork driver's rebase in a drain run.** That command also exports, which moves the
  delivery branch and the record before any gate has run.
- **The lander writes the record when it delivers.** The record is a tracked file, so writing it
  is a trunk commit. The lander would be landing a change no gate judged.
- **Closing on the delivery of the delivery branch.** The check would still be red, so every candidate
  that touches the fork would be released into a refusal.
- **Pointing the check at the delivery branch instead of the record.** The record is what makes
  the check answer the same way in every worktree, and
  changing it would change the ordinary gate.

**What this leaves undone.** An ordinary fork delivery, one that only adds commits, also leaves
the record behind the delivery branch. There the check stays green, because the older commit still
merges, so nothing waits on it. Keeping the record current after every fork delivery is not
designed here.

### The checks that could not judge

This is the designer's decision, `D-FG4`, and the owner may overturn it.

No gate answer takes the tooling-defect path. A tooling-defect item is filed only when a worker's
report names a tooling obstacle (`fileDefectItem`, called from
`tools/tillr-drain/drain/obstacle_route.go`).

A remote that does not serve a pinned commit is an unavailable resource, not drift. Such a check
is planned only when a candidate's diff reaches it or a fixer names it, so it does not hold a
repair that does not touch the fork. Where it is planned, `D-FG4` adds no route:

| Where the check could not judge | What happens |
| :--- | :--- |
| An ordinary candidate's gate, with no verdict reached | The candidate is held as `gate-no-verdict` and the next pass gates the same candidate again (`holdGateNoVerdict` in `gate_no_verdict.go`). The mover is the loop |
| An ordinary candidate's gate, when the gate states an error of its own | The item is held as a gate fault, `gate-harness-error`, with the gate's words as the reason (`gateFault` in `item_fault.go`) |
| A fixer's named check | The fixer is held as a gate fault, `gate-harness-error`, naming the checks that could not judge (`fixer_gate.go`) |
| The replay of a red `patch-branch-inv2` check on the candidate's base | The red is not classified as drift. No request is filed and nothing is inherited; the candidate is handled as the gate's own answer says, by the rows above or as an ordinary refusal |

- **No gate answer files a tooling-defect item.** That stays the route for a worker's reported
  tooling obstacle and nothing else.
- **What `D-FG4` adds** is the last row only: the drift classification treats
  "could not judge" on the base as a third answer, never as "red, same answer" and never as
  "passes".
- **The name of what was lacking** is the check's own output in the gate log. No new record is
  added for it.

Rejected, as the designer's decision:

- **Filing a defect item from a gate's "could not judge".** The cause is usually outside the
  repository (a remote, a network), so the item would have no fence a worker could repair in.
- **Changing the fixer's hold to match the ordinary candidate's re-gate.** The difference is
  recorded here, not removed.

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

For [a fork that follows a branch head](#a-fork-that-follows-a-branch-head) and for
[a fork that still commits a series directory](#a-fork-that-still-commits-a-series-directory)
no request exists. The inherited record on the candidate and the loop's one line are the notice.

The owner is told by the queue only. He ruled this on 2026-11-21 ([OQ-FG1](#OQ-FG1)): the rebase
request appears in the queue and on the waiting list like any item, and he is asked something only
if the rebase hits a conflict an agent cannot settle. No owner-facing row and no hold addressed to
him is built for drift.

## What this does not change

- What the lander accepts. Its release-only rule for a rewritten delivery branch stays, which is
  why [a head follower](#a-fork-that-follows-a-branch-head) gets no rebase request. It has one
  step after a fork delivery, the fetch into the gate's copy of the fork
  ([who rebases](#who-rebases-and-how-the-recorded-commit-follows)).
- The refusal of the fork driver's rebase and export in a drain run.
- The tooling-defect route. It is still entered only by a worker's reported tooling obstacle.
- The proof, the green pointer, automatic revert and the way a fixer is filed, attached and
  resolved. [A fixer never waits on itself](#a-fixer-never-waits-on-itself) uses the existing
  attachment and changes only what happens to the fixer after it: returned to its worker, not
  held.
- The ordinary gate, for any candidate.
- The drift probe's design. It has no design of its own yet and is still the right discoverer;
  until it is built, drift is seen only when a change touches a series or a pack declaration.
- `item-gate-pinned-fork-inputs`, which changes how fork checks read their pinned inputs.
- State that already exists. An open fixer is gated by this rule the next time it is gated.
  No record is rewritten.

## Alternatives

| Alternative | Verdict |
| :--- | :--- |
| Keep the untiered range and acquit the fixer of checks red on its base | Rejected. It still runs every check in the range before deciding, so the cost still grows with the red range, and "could not judge" still holds the fixer |
| Keep the untiered range and exclude upstream readers from it | Rejected. It removes the fork refusal and keeps the second group of [the table above](#what-the-untiered-range-bought), which the proof already judges |
| The ordinary gate alone, with no named checks | Rejected. A fixer that repairs nothing would land, and a fixer's landing is never reverted |
| Wait for the drift probe | Rejected as the only fix. The probe waits on other work, and the fixer's range is wrong for tree checks too |

## Risks

| Risk | Mitigation |
| :--- | :--- |
| A second red in the range is found one proof later than a range gate would find it | The proof names it and files its fixer, as for any red; the first fixer has already landed, so the range is shorter |
| Drift goes unseen while no change touches a fork | Stated above; the probe closes it |
| A named check's selector is missing from the fixer's record | The gate answers "could not judge" naming the check; it never passes by omission |
| A head follower drifts and no queue item says so | A head follower's drift is on the verdict of every candidate that inherits it and in one loop line; the rebase is the operator's step. A lander route for it is separate design |
| A candidate reaching a check whose remote stays unavailable is gated again every pass | `D-FG4` keeps this. `item-gate-pinned-fork-inputs` changes how those checks read their pinned inputs |
| A fixer's named list grows while it works, because unnamed reds its diff reaches attach to it | Only a check whose read paths the fixer's own diff changes can attach this way, and only when no other fixer can be waited on. The existing limits on returned refusals bound the loop |

## Open Questions

None. The owner ruled the one question on 2026-11-21; it is a row in the [Decision Ledger](#decision-ledger).

## Decision Ledger

The `D-FG` rows are the designer's decisions of 2026-11-21; the owner may overturn each by comment. The [OQ-FG1](#OQ-FG1) row is his ruling.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-FG1"></a>[OQ-FG1](#OQ-FG1) | Owner ruling: when an upstream release breaks a patch series the owner is told by the queue only. No owner-facing row and no hold. | 2026-11-21 | [Who is told when an upstream moves](#who-is-told-when-an-upstream-moves) | — |
| `D-FG1` | A trunk fixer's gate is the ordinary gate over its own diff plus the checks it names, run on its tree. No range since the last green is read. Structural fix. (Designer's decision.) | 2026-11-21 | [The fixer's gate](#the-fixers-gate) | Built |
| `D-FG2` | A fixer stays excluded from acquittal only for the checks it names; for any other check it is judged as any candidate is, except that it is never held behind itself or behind a fixer that waits on it: such a check is attached to the fixer and the refusal returns to its worker. No second fixer is filed for the same commit. (Designer's decision.) | 2026-11-21 | [The fixer's gate](#the-fixers-gate), [A fixer never waits on itself](#a-fixer-never-waits-on-itself) | Built |
| `D-FG3` | A `patch-branch-inv2` check red on a candidate and on its base with the same refusal against the same target ref, on a candidate whose diff does not include that fork's own paths, is drift: the candidate lands with the failure inherited and one rebase request per fork and release is filed. No trunk fixer is filed for drift. Only `patch-branch-inv2` can be drift, selected by kind name; the fork is the check's `--fork` argument and the release is the ref the fork driver's target command prints. The request is filed only for a fork whose target kind is `release` and which is delivered as a delivery branch. It is a mixed item: its worker rebases the fork in its lane checkout with `GIT rebase --onto <release> <old base> main`, the fork part is delivered first, the repository part then rewrites the fork's pin file, the lander fetches each delivered fork commit into the gate's copy of the fork, and the request closes when the repository part lands. The operator takes no step. For a fork that follows a branch head, and for a fork that still commits a series directory, the candidate still lands with the failure inherited, no item is filed, one loop line is printed, and a candidate that touches that fork is refused. Structural fix. (Designer's decision, carrying out the drift-probe decision, itself a designer's decision.) | 2026-11-21 | [Upstream drift](#upstream-drift-is-a-rebase-request-not-a-refusal), [Which checks can be drift](#which-checks-can-be-drift), [A fork that follows a branch head](#a-fork-that-follows-a-branch-head), [A fork that still commits a series directory](#a-fork-that-still-commits-a-series-directory), [Who rebases, and how the recorded commit follows](#who-rebases-and-how-the-recorded-commit-follows) | Built |
| `D-FG4` | A fork check that could not judge is neither drift nor a verdict. It holds only a candidate whose diff reaches it or a fixer that names it, by the holds the tree already has: an ordinary candidate is held as `gate-no-verdict` and gated again, or as a gate fault when the gate states its own error; a fixer's named check is a gate fault. No gate answer files a tooling-defect item. "Could not judge" on the base is never classified as drift. (Designer's decision.) | 2026-11-21 | [The checks that could not judge](#the-checks-that-could-not-judge) | Built |
| `D-FG5` | The queue, the affected candidates and the operator's surfaces are told of drift by the rebase request and one loop line; for a fork that follows a branch head, by the inherited record and one loop line. Nothing owner-facing is built ([OQ-FG1](#OQ-FG1)). (Designer's decision.) | 2026-11-21 | [Who is told](#who-is-told-when-an-upstream-moves) | — |
