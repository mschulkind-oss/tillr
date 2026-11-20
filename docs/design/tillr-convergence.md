---
title: "Why tillr keeps hitting walls: three causes, one target"
status: accepted
stage: DECIDED
updated: 2026-11-19
tags:
  - tillr
  - drain
  - queue
  - observability
  - design
summary: "Reads three days of tillr history as three structural causes rather than many defects — derived copies that are checked instead of derived, failures that become holds only a person can clear, and decisions that are recorded but not shown — and sets one target: a cold machine rebuilds the queue from documents, drains it, and shows every stopped item with the act that moves it. Document sync, seed retirement, waiting/why visibility and hold classes are built; input-change re-admission was superseded by D-JL10; post-run cause statements, the two-unrelated-items stop, removed-block retirement, derived-copy regeneration and profile-owned model ids remain unbuilt."
next: "Build the two D-TC3 units at the end of Section 4 (written without hidden item comments; on 2026-11-18 the owner ruled OQ-QM1 in tillr-queue-mirrors-designs.md to put tasks in companion <topic>-plan.md files, unbuilt): the cause statement, then the stop on two unrelated items. Reconcile the unruled removed-block retirement rule in D-TC2 against triage-no-block and the companion-plan rollout; D-TC5 waits on OQ-SH2, and OQ-TC2 waits on harness profiles and OQ-PT7/OQ-PT8."
depends-on:
  - queue-persistence-ruling.md
  - landing-principles.md
  - tillr-drain-successor.md
  - momentum-principle.md
vantage:
  status-chip: true
---

# Why tillr keeps hitting walls: three causes, one target

**Status:** 2026-11-15. Measured against `main` at `35a3dc9f1`, on a machine with no `.tillr/`
directory. Reconciled against the tree at `dcb76fe41` on 2026-11-18 and at `85ca09644` on
2026-11-19. These are built: the block
reader and the trailer deletion (`D-TC1`), sync as document intake (`D-TC2`, apart from
removed-block retirement; store-declared CLI filings `idea`, `doc-triage` and `request` also exist,
and on 2026-11-18 the owner ruled [`OQ-QM1`](tillr-queue-mirrors-designs.md#OQ-QM1) in
[`tillr-queue-mirrors-designs.md`](tillr-queue-mirrors-designs.md) to replace hidden item comments
with mirrored roadmaps and companion `<topic>-plan.md` files once built), the seed's retirement
([`OQ-TC1`](#OQ-TC1), `item-seed-retire`), the "why is it not moving" face (`D-TC4`), and hold
classes and movers (`D-TC3`, in part; `tools/tillr-drain/drain/returned.go` also returns a worker
run's refusal with normalized check-log digests and escalates an identical repeat under `D-JL10`,
and `tools/tillr-drain/drain/defect.go` files tooling-defect items from worker `tooling` obstacles).
Re-admission on a changed input
([`OQ-TC3`](#OQ-TC3)) was built and then removed: the owner's ruling of 2026-11-17
(`D-JL10` in [the judge and lifecycle design](tillr-judge-and-lifecycle.md#decision-ledger))
supersedes it, and `f8774bd63` took the code out (`TestAChangedInputReDispatchesNothing` in
`tools/tillr-drain/drain/readmit_test.go`). Four remainders are not built. Two are drafted at the
end of [Section 4](#4-what-i-would-build-in-order) (post-run hold cause statements and the
two-unrelated-items stop). Two wait on something else: removed-block retirement (`D-TC2`) waits on
an unruled design decision between retiring an item whose block was removed and routing unshared
open items to `triage-no-block` (and on the unbuilt companion-plan rollout of
[`tillr-queue-mirrors-designs.md`](tillr-queue-mirrors-designs.md)), while regenerating derived
copies (`D-TC5`) and moving model ids out of the policy files ([`OQ-TC2`](#OQ-TC2)) wait on other
documents' open questions. The [Decision Ledger](#decision-ledger) names what each one waits on. Its
three former open questions ([`OQ-TC1`](#OQ-TC1) to [`OQ-TC3`](#OQ-TC3)) were initially settled on
2026-11-15 by the session's designer on recorded principle, at the owner's request:
[`OQ-TC1`](#OQ-TC1) is built, [`OQ-TC2`](#OQ-TC2) remains a designer's decision in the stopgap
state, and [`OQ-TC3`](#OQ-TC3) was superseded by the owner in `D-JL10`; `D-TC1` to `D-TC5` are also
designer's decisions that the owner may overturn.

> **In short.** The walls are not complexity. Almost every fix of the last three days repairs one of
> three structural causes, and each cause has a principle already written down in this tree that
> nothing enforces. Enforce those three, in order, and the loop stops needing repair.

**Why it matters.** `git log --since=2026-11-13` on `main` answers 169 commits in three days, and
most of them touch tillr. Three
areas were fixed, then reworked inside a day: the fence, the boundary bracket, and who owns the
queue's definition (four ownership models in about 38 hours).

**The shape.** One invariant to build toward — *delete `.tillr/`, sync, drain* — and three rules
that make it hold: derive every copy, give every hold a mover, show every decision.

**Cost.** The seed file, its count pin, the commit trailer and the hand-run mirror go away. Under
the initial [`OQ-TC3`](#OQ-TC3) proposal, a held item could run again on a changed input without a
person asking for it (superseded on 2026-11-17 by `D-JL10` in
[`tillr-judge-and-lifecycle.md`](tillr-judge-and-lifecycle.md#decision-ledger), which returns a
worker refusal to the worker rather than re-dispatching on input-hash changes).

**Start at [Section 2](#2-the-three-causes)** — the causes. The target and the order fall out of it.

**Needs your ruling:** No open `OQ-*` directive in this document. Removed-block retirement vs.
`triage-no-block` (`D-TC2`) remains an unruled design decision owed here (alongside the unbuilt
companion-plan rollout of [`tillr-queue-mirrors-designs.md`](tillr-queue-mirrors-designs.md)),
`D-TC5` waits on [`OQ-SH2`](tillr-selfhosted-build.md#OQ-SH2), and the agent-authored decisions in
the [Decision Ledger](#decision-ledger) remain open to the owner's review and overturn (tracked in
[`decision-provenance-review.md`](../tillr/decision-provenance-review.md):
[`OQ-PR6`](../tillr/decision-provenance-review.md#OQ-PR6),
[`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8),
[`OQ-PR10`](../tillr/decision-provenance-review.md#OQ-PR10)).

**Reads with:** [`queue-persistence-ruling.md`](queue-persistence-ruling.md) (the ruling whose last
two commits this design executes first), [`tillr-queue-mirrors-designs.md`](tillr-queue-mirrors-designs.md)
(the 2026-11-18 ruling on [`OQ-QM1`](tillr-queue-mirrors-designs.md#OQ-QM1) retiring hidden item
comments in favor of companion `<topic>-plan.md` files once built), [`landing-principles.md`](landing-principles.md) (the
principles named here as unenforced), [`tillr-drain-successor.md`](tillr-drain-successor.md) (the
loop model the hold classes project onto), [`tillr-claude-backend.md`](tillr-claude-backend.md)
(the harness a seat may name in [Section 3.5](#35-a-seat-names-its-harness-and-the-harness-names-the-model)), and
[`failure-diagnosed-not-waited-out.md`](failure-diagnosed-not-waited-out.md) (the mechanism
[Section 3.2](#32-every-hold-names-a-mover-that-can-act) reuses).

---

## 1. What a cold machine finds today

The tenet is that state lives in documents. This machine was started with no `.tillr/` on purpose,
so it measures the tenet directly.

| Fact | Measured 2026-11-15 |
| :--- | :--- |
| A command that builds the queue from documents | None. `tillr-item` has eight faces and `sync` is not one; the ruling's third commit was never made. |
| Item blocks in tracked Markdown | 6, in four documents. All six parse; one (`item-bar-ironbar-config-v2`) names two paths not on `main` and is iceboxed. |
| What the only working bootstrap produces | `tillr-item seed` writes 62 rows from `config/tillr/seed-items.json`, 48 of them `in-queue`. |
| How many of those 62 are open work | 17. The ruling counted 29 open rows in the old store and named each; 17 are in the seed, 6 are the blocks above, 1 has since landed, 5 were left private on purpose. The other 45 seed rows are landed work the file still calls `in-queue`. |
| The landing receipt | [`trunk.go`](../../tools/tillr-land/land/trunk.go) still stamps the `Tillr-Item:` trailer the ruling deleted. |
| The models a run would use | [`backend-policy.json`](../../config/tillr/backend-policy.json) names two OpenRouter models. This machine has Bedrock only. |

So the honest reconstruction of this machine's queue is **six items, four dispatchable now**: the
board timeline read API, the killed-writer store recovery, the observatory dev check in the gate,
and the substrate Go suites at the landing tier. The seed bootstrap would instead re-dispatch 45
finished items on models this machine cannot reach.

> [!WARNING]
> A scanner that only skips fenced code is not enough. The two queue-persistence documents write the
> directive inside inline code about forty times, and a dry parse of the tree read each as a
> malformed block. A directive counts only when its opening `<!--` starts a line outside a fence.

## 2. The three causes

### 2.1 A derived copy is checked instead of derived

A **derived copy** *(coined here)* is a tracked file whose whole content follows from another
tracked file. It is not a cache: a cache is untracked and can be deleted. Tillr keeps five, and each
has a check that refuses correct work until someone repairs the copy by hand.

| Derived copy | Source | What the check cost |
| :--- | :--- | :--- |
| The pack mirror under `yolo-packs/tillr/` | `tools/tillr*` | 12 commits whose subject is a re-mirror (`git log --since=2026-11-13 --oneline`, subjects matching `mirror`); `0d95fe589` records the fifth lag in one day; one correct candidate refused on it alone (`483e3a44d`) |
| The seed row count in `tools/tillr-drain/seed_test.go` | the seed file | 23 commits touched the file since 2026-11-13; the count is re-based by hand |
| The seed's item definitions | the store, then the documents | re-seeding refused with `seed-conflict` until written back by hand (`ba8f10bd0`) |
| A lane's fence additions | the item's paths | fixed three times: brief, mirror, companions (`483e3a44d`, `979501265`, `5ae7896e9`) |
| The module list | the `tools/` directory | kept separately in the gate and in the mirror test |

The fence is the one that was eventually solved, and it shows the fix: `5ae7896e9` made the
additions a function the stager computes. Nothing asks a worker to remember them now.

### 2.2 A failure becomes a hold only a person can clear

After a run, the planner holds an item for one of six reasons: `boundary-scope-escape`,
`candidate-no-op`, `conflict-exhausted`, `gate-failed`, `run-unjudged`, `run-link-missing`. Every
one clears the same way: a person runs `tillr-drain retry`. None records what would have to be
different for a second run to succeed.

That is why failures read as "stuck". `4a79a8e7f` found seven items held permanently on one
reason. `ca91d764d` measured a drain that refused every candidate it produced across three ticks.
The fixes added retry doors (`4a79a8e7f`, `f8ed9d09b`) rather than a cause.

[`landing-principles.md`](landing-principles.md) already rules this, and says so about itself:
"every outcome is state-changing", "only verdicts charge", "fix the machinery; never hand-carry the
work" and "a failure is diagnosed, not waited out" each carry *enforced by: nothing*.

### 2.3 A decision is recorded and not shown

The store holds what the loop decided: `dispatches` has every dispatch and hold with its reason, and
the loop's result file carries a sentence naming each held item. No surface reads either. The owner
sees counts and a stop word.

Measured from the operator's seat: the board showed 0 running over a leased lane (`57300100a`) and
8 working over none (`ab5f67fb5`); a timed-out run shows "0 rejected"; a held item with no recorded
blocker reads one fixed sentence. Each was fixed one field at a time. The question the owner
actually asks — *why is this item not moving, and what moves it* — has no surface.

## 3. The target

One invariant, checkable on any machine:

> **Delete `.tillr/`, run sync, run the drain. The queue is the same queue, the drain proceeds, and
> one command lists every item that is not moving with the act that will move it.**

Three rules make it hold. Each is stated as behavior; mechanism is the implementer's unless named.

### 3.1 The queue is read from documents, and from nothing else

- **One reader.** `tillr item sync` reads item blocks from tracked Markdown and writes missing items
  to the store. It is the only way an item enters a store. It runs at drain start and on demand.
- **Idempotent.** A second run with unchanged documents writes nothing and appends no event.
- **Never destroys local facts.** Sync does not change the rank, the runs or the `landed` state of
  an item the store already holds.
- **A changed block is a changed item.** When a block's paths or criteria differ from the store's,
  sync updates the definition and records that it changed. This replaces the `-v2` clone: editing
  the block is the door back.
- **A removed block retires the item** in the store unless it has a live lease, in which case sync
  reports it and changes nothing.
- **Malformed input stops sync, not the drain.** A block with no `id`, no path, no criterion, a
  duplicate id, or a `blocked_by` naming no block is reported with file and heading, and no item is
  written from that file. Items from other files are unaffected.
- **Zero blocks is a valid queue.** Sync exits 0 and the drain idles.
- **The trailer goes.** The lander stops stamping `Tillr-Item:`; the receipt is the block's absence.
- **The seed goes.** `config/tillr/seed-items.json` and the `seed` face of `tillr-item` are
  deleted, and with them the row-count pin in `seed_test.go`. The ten seed rows that restate an
  open question need nothing: the question is already in its document. The seven rows that are
  neither landed nor such a restatement (four pi-fork items that cannot be staged yet, three whose
  criteria the ruling says are wrong) become prose entries on
  [`docs/pi/roadmap.md`](../pi/roadmap.md), which keeps their text without a second intake.
  ([OQ-TC1](#OQ-TC1).)

The first proof is this machine: sync on a store that does not exist yields the six items of
[Section 1](#1-what-a-cold-machine-finds-today), four dispatchable.

### 3.2 Every hold names a mover that can act

A **mover** is the act that will next change a held item, as
[`landing-principles.md`](landing-principles.md) uses the word. Every hold belongs to one of three
classes, and the class is part of the hold's record:

| Class | Holds | Mover |
| :--- | :--- | :--- |
| **Waits on the owner** | an open question in the design document; a block the owner iceboxed | the owner's ruling, read from the document |
| **Waits on work** | an unfinished blocker; a red trunk; a fork delivery in flight; no free lane | another item's transition, which the loop already watches |
| **Needs a cause** | the six post-run holds of [Section 2.2](#22-a-failure-becomes-a-hold-only-a-person-can-clear) | a named change to an input, or a diagnosis |

The third class is the change. A post-run hold records a **cause statement**: the stage that
failed, the normalized error, and the **inputs** the run had — the item's definition, the tillr
build, the backend policy, and the base commit.

- **Unchanged inputs never re-run.** This is the existing rule, kept.
- **The same failure on two unrelated items is the machinery's.** When one stage and error holds
  items that share no path, the loop stops dispatching, says so, and files one defect. It does not
  hold the items one by one. Two is the count because `RepeatedRefusalThreshold` is already 2.
- **A changed input re-admits the item once, by itself.** When an input the held run had — the
  item block, its allowed paths, the backend policy, or the tillr build — differs from what the
  hold recorded, the next pass dispatches the item with no retry command, and that pass's recorded
  decision names the input that changed. **Re-admission** *(coined here)* is that automatic
  dispatch of a held item. An item that fails again after one re-admission stays held until the
  owner runs `tillr drain retry`: a budget escalates to a person, it does not loop
  ([landing principle 6](landing-principles.md#6-a-budget-escalates-it-does-not-loop)), and a re-queue happens only against a recorded
  basis (`D-JL6` in [`tillr-judge-and-lifecycle.md`](tillr-judge-and-lifecycle.md)).
  ([OQ-TC3](#OQ-TC3).) **Superseded** by the owner's ruling of 2026-11-17 (`D-JL10` in
  [the judge and lifecycle design](tillr-judge-and-lifecycle.md#decision-ledger)): nothing re-runs
  because an input changed, and a worker run's ending is never counted. The re-admission was
  removed at `f8774bd63`.

### 3.3 Every decision is shown where the owner looks

- **One answer to "why is it not moving".** A read face lists every item that is not landed with
  its position, its hold, the hold's class, the mover, and how long it has waited. It reads only
  the store. It is text first, so it works in a terminal and can be pasted into a conversation; the
  board draws the same rows.
- **One answer to "what did the loop just do".** Each pass's decisions are listed in order: what was
  dispatched, what was held and why. The data is in `dispatches` today.
- **A stop says its sentence.** The loop's stop detail is shown wherever the stop reason is.
- **Visibility ships with the behavior.** A change that adds a hold or a stop adds its row to these
  faces in the same commit.

The board timeline already has two item blocks in
[`observatory-design.md`](observatory-design.md); they are the drawn form of the second bullet and
stay as written.

### 3.4 A derived copy is produced by the pipeline

- **The pipeline regenerates, the gate compares.** Between a run's commit and its gate, the loop
  regenerates every derived copy in the lane and commits the result into the candidate. A worker is
  never asked to; a brief never mentions it.
- **A count is asserted structurally or not at all.** A test may assert that every item has a fence
  and a criterion. It may not assert how many items exist.
- **A list has one home.** The module list is read from the directory by both of its readers.

This is the direction [`tillr-selfhosted-build.md`](tillr-selfhosted-build.md) leans for the
mirror; that document's three questions stay there and are not reopened here.

### 3.5 A seat names its harness, and the harness names the model

A **seat** is a role an item may ask for (design, build, review, scout). A **harness** is the
backend adapter that runs a seat's work — pi, or the `claude -p` backend of
[`tillr-claude-backend.md`](tillr-claude-backend.md).

- **Tillr's tracked policy names seats and, for each, the harness that fills it.** Which harness
  fills a seat stays a tillr choice, because the owner asked on 2026-11-15 to choose which seats
  run on the Claude subscription.
- **The model comes from the harness's own profile on the machine.** Tillr's tree names no model
  id; a machine changes models by changing its profile, as pi's model and provider are already
  derived from the selected profile.
- **The per-machine-group policy files are the stopgap.** The `config/tillr/backend-policy*.json`
  files (`backend-policy.json`, `backend-policy.bedrock.json`, `backend-policy.claude.json`,
  `backend-policy.mixed.json` and `backend-policy.copilot.json`) stay until a harness profile can
  answer for the design seat, then the model ids leave them. ([OQ-TC2](#OQ-TC2).)

## 4. What I would build, in order

Each step leaves the tree shippable, and each later step is run through the drain the earlier ones
repaired.

1. **The block reader and the trailer deletion, by hand.** The drain cannot build its own intake.
   These are the ruling's second and third commits, plus the line-start rule of
   [Section 1](#1-what-a-cold-machine-finds-today).
2. **This machine's models**, through the per-machine-group policy file of
   [Section 3.5](#35-a-seat-names-its-harness-and-the-harness-names-the-model).
3. **Cold reconstruction and a supervised first drain** of the four dispatchable items. Two of the
   four are visibility and durability fixes, so the drain's first output improves the next drain.
4. **The "why is it not moving" face** ([Section 3.3](#33-every-decision-is-shown-where-the-owner-looks)),
   as an item block the drain builds.
5. **Hold classes and cause statements** ([Section 3.2](#32-every-hold-names-a-mover-that-can-act)).
6. **Derived copies produced by the pipeline** ([Section 3.4](#34-a-derived-copy-is-produced-by-the-pipeline)).
7. **The successor function**, already accepted, whose positions the hold classes project onto.

The seed's retirement and re-admission on a changed input are item blocks the drain builds.

### Retire the seed file and its command

Landed as `item-seed-retire`: `config/tillr/seed-items.json`, its pin and the working `seed` face
are gone, and `tillr-item seed` now refuses and names `tillr item sync`. What follows is the
record of the attempts before it landed.

Two attempts at this work built the retirement and neither produced a candidate the loop could
gate, for reasons this section did not state. Both replaced this section with a "Built" note.
The loop refuses that before the gate, as a worker editing its own item block, and it writes the
retirement itself once the change has landed. Removing the block also orphaned the dependency
that `item-oa-move-to-top` in [`observatory-actions.md`](../reference/observatory-actions.md#moving-an-item) declares on
this item, which is why document sync then failed. Criterion c6 states the boundary.

The second attempt also stopped on test fallout from deleting the seed, and criterion c7 names
it. The gate's change-check fixtures stage a `config` directory that exists only because they
write a seed file into it, so the fixture's staging has to change with the fixture.

One failure a worker meets in a lane is not this item's and is not a reason to stop. Measured on
2026-11-17 at `919bb62ae`, in a lane's worktree with no change applied:
`go test . -run '^TestPlanHoldsAnItemBlockedByAnUnlandedItem$'` in `tools/tillr-drain` fails its
`blocker landed` case with `hold / trunk-unproved`. The test plans against the checkout it runs
in, a lane shares the main checkout's proved-trunk pointer, and the gate's clean checkout has
none, so the gate does not see it. Its repair is criterion c8 of `item-ig-parallel-readers` in
[`tillr-item-graphs.md`](tillr-item-graphs.md). A worker that sees exactly that one case fail
reports it and carries on; any other failure in the drain suite is a real finding. The same
measurement ran `python3 tests/fork-check.test.py` to exit 0 on that commit, so a failure there
belongs to the candidate. That test keeps its gate records under the shared git directory by
design, which is not an edit outside the fence.

A third attempt stopped for a different reason, and criterion c9 names it. In `build_repo()` of
`tests/fork-check.test.py` the seed fixture is written directly above the line that makes the
fixture a repository, and a patch recovered from an earlier attempt removed that line with the
fixture. The fixture's `git add -A` and `git commit` then ran in a directory that was not a
repository. The test's scratch was under the main checkout, so git walked up and committed the
main checkout's staged index to the trunk: the commit of 2026-11-17 at 08:45 -0500 with the
subject `initial`, authored `fork-check test <test@example.invalid>`. Measured on 2026-11-17 in
a directory under the main checkout that holds no repository of its own:
`git rev-parse --show-toplevel` prints the main checkout, and with `GIT_CEILING_DIRECTORIES`
set to that directory's parent it fails with `not a git repository`.

That commit is the owner's to keep or rewrite, and no worker on this item repairs, reverts or
re-authors it. What it carried was a guard against this fault: helpers named `git_init` and
`require_own_repo`, and a `GIT_CEILING_DIRECTORIES` setting on every fixture git command, in the
same test file. A worker staged from a base that has those helpers keeps them, and one staged
from a base without them does not add them, because the guard is not this item's work. The
earlier attempts' patches to `tests/fork-check.test.py` were cut from a tree without the
helpers, so that file's edit is made by hand against the file as the lane has it and is not
applied from a recovered patch.

A fourth attempt built the retirement, passed every other suite and an independent review, and
stopped without committing because the full drain suite failed once: `TestCommentPathTwice` in
`tools/tillr-drain/drain/comments_test.go` reported `designer <nil>`. Criterion c10 rules on it.
The failure is not the retirement's. Under `tools/tillr-drain` that attempt's change deletes
`seed_test.go`, which is a test of the module's root package, and nothing in the `drain` package;
`git diff --name-only 02147626 e62b16ce8 -- tools/tillr-drain/drain/comments.go
tools/tillr-drain/drain/comments_test.go tools/tillr-drain/drain/designer.go` prints nothing, so
the code that attempt ran is the code the trunk has now. Measured on 2026-11-17 at `e62b16ce8`
in a lane's worktree with no change applied:
`python3 tools/go-test-shards.py tools/tillr-drain -k 6` passed all 1393 tests in each of two
runs, and the compiled `drain` test binary run as
`-test.run '^TestCommentPathTwice$' -test.count=12` in sixteen processes at once passed all 192
times. The failure was not reproduced, and its cause is not established.

What the same measurement did establish is that the test is not hermetic. Its fixture sets no
question index command, so the loop runs the real `uvx vantage-check index --format json` in
the fixture repository, and the loop drops a document's designer record when that index reports
the document's stage as done. With a `uvx` first on `PATH` that answers such an index for
`docs/design/example.md`, the test fails at the same line with the same `designer <nil>`; with
one that exits 3 it fails differently, as a loop fault that names the command. That is one way
to produce the message and not a finding that it is what happened. Making the test hermetic and
finding the cause are repairs to the comment path's tests. They are not this item's work, and
no item tracks them yet: that is the owner's to file, and this section does not stand in for it.

The fourth attempt's tree is also older than the trunk. It was cut at `02147626`, and ten of
its 59 paths have changed on the trunk since, among them `tools/tillr-item/main.go`,
`tools/tillr-item/item/apply.go`, `tools/fork-check.py` and `tests/fork-check.test.py`. A
worker that recovers it replays its change onto the lane's base path by path and does not copy
those files from the preserved tree, which would undo the trunk's edits to them.



### Re-admit a held item when an input it failed on changes

Built: this landed in commit `72dc84e4a4`, and was removed at `f8774bd63` under the owner's ruling
of 2026-11-17 (`D-JL10` in [the judge and lifecycle design](tillr-judge-and-lifecycle.md#decision-ledger)).

### Record a cause statement with each post-run hold

When a run fails after it starts, the loop holds its item for one of six reasons. The hold
says which reason, but not what went wrong or what would have to change for a second run to
succeed. So a person who reads a held item must open the run's log to find the stage that
failed and the error it printed. The loop already records the run's inputs when it dispatches
the run: the item's block, its allowed paths, the backend policy and the tillr build
(`RunInputs` in `tools/tillr-drain/drain/readmit.go`; input-change re-admission itself was
removed under `D-JL10`, while `tools/tillr-drain/drain/returned.go` returns a worker refusal
with normalized check-log digests). The hold row itself still does not point at those inputs or
carry a normalized cause statement. [Section 3.2](#32-every-hold-names-a-mover-that-can-act)
asks that each post-run hold carry a **cause statement**, made of three parts: the stage that
failed, the error in a normalized form that two runs can share, and the run's recorded inputs.
The waiting face then shows that statement beside the mover, so the reader sees what to change.

**Outcomes:**

- Record what failed, and with which inputs, whenever a run's item is held.
- Show the cause beside the hold wherever the owner reads why an item is not moving.
- Give the next unit a normalized error it can compare across items.

**Not yet filed.** On the owner's ruling of 2026-11-18, no document gains a new item
directive; on the same day the owner ruled [`OQ-QM1`](tillr-queue-mirrors-designs.md#OQ-QM1) in
[`tillr-queue-mirrors-designs.md`](tillr-queue-mirrors-designs.md) to put build tasks in companion
`<topic>-plan.md` files (not yet built). This unit stays written without a directive to become a
task once that reader lands (or via `tillr item request`). It waits on nothing.

- **c1:** Each of the six post-run holds named in
  [Section 2.2](#22-a-failure-becomes-a-hold-only-a-person-can-clear) records a cause statement
  with the hold. The statement names the stage, the normalized error and the inputs the held run
  had.
- **c2:** The normalized error has no run id, lane path, worktree path, timestamp or temporary
  directory in it. Two runs that fail the same check with the same message, in different lanes
  and at different times, produce the same normalized error.
- **c3:** The statement's inputs are the ones the loop recorded when it dispatched that run, not
  a second reading taken at hold time.
- **c4:** `tillr drain why` and the waiting face show the stage and the normalized error on the
  held item's row, and a hold recorded before this change shows that it has no cause statement.
- **c5:** The drain README states the cause statement's fields, and the convergence Decision
  Ledger's `D-TC3` cell names where it is built.

**Agent Details:**

- **Proposed id:** `item-drain-hold-cause-statement`.
- **Paths:** `tools/tillr-drain/`, `tools/tillr-view/`,
  `yolo-packs/tillr/files/local/share/tillr/tillr-drain/`,
  `yolo-packs/tillr/files/local/share/tillr/tillr-view/`, and this document.
- **Ref:** [Section 3.2](#32-every-hold-names-a-mover-that-can-act) and `D-TC3` in the
  [Decision Ledger](#decision-ledger).
- **Where:** the six reasons are in `tools/tillr-drain/drain/schema.go`. The recorded inputs are
  the `drain.inputs` event, whose shape is `RunInputs` in `tools/tillr-drain/drain/readmit.go`.
  The waiting rows are built in `tools/tillr-view/waiting.go` and `tools/tillr-view/why.go`.
  Run `just pack-sync` after the last source edit and commit the regenerated mirror.
- **Verify:** Add drain tests that drive each of the six holds with a stub run and check the
  recorded statement. Add a test that two failures in different lanes normalize to the same
  error. Add a view test for the row text and for a hold that has no statement. Run
  `go test ./...` in `tools/tillr-drain` and `tools/tillr-view`.

### Stop dispatch when one failure holds two unrelated items

A failure that holds two items which share no file is unlikely to be either item's fault. It is
far more often the machinery's: a broken gate, a missing tool, a backend that refuses every
request. Today the loop holds each such item on its own and keeps dispatching more work into
the same failure, so the queue fills with held items while the real defect goes unnamed. The
loop already counts refused items, but that count only reports and holds nothing.
[Section 3.2](#32-every-hold-names-a-mover-that-can-act) sets the rule: when one stage and one
normalized error hold two items whose allowed paths share nothing, the loop stops dispatching,
says why, and files one defect item. It does not hold the items one by one.

**Outcomes:**

- Stop new dispatch when one failure reaches two unrelated items.
- File one defect item that names the stage, the error and both items.
- Resume dispatch once that defect item lands, with no command to run.

**Not yet filed.** On the owner's ruling of 2026-11-18, no document gains a new item
directive; on the same day the owner ruled [`OQ-QM1`](tillr-queue-mirrors-designs.md#OQ-QM1) in
[`tillr-queue-mirrors-designs.md`](tillr-queue-mirrors-designs.md) to put build tasks in companion
`<topic>-plan.md` files (not yet built). This unit stays written without a directive to become a
task once that reader lands (or via `tillr item request`). It waits on the cause statement unit
above, whose normalized error it compares (note: `c5` below was drafted before `D-JL10` removed
input-change re-admission in favor of acquitted repair holds such as `ReasonToolingDefect` in
`tools/tillr-drain/drain/defect.go`).

- **c1:** When two post-run holds carry the same stage and normalized error, and their items'
  allowed paths share no path, the loop dispatches no new run. Runs already in flight finish.
- **c2:** The loop files exactly one defect item for that stage and error, through its existing
  defect path. The item's text names the stage, the normalized error and both held items. A
  third item held by the same failure files nothing new.
- **c3:** Two items whose allowed paths overlap, held by the same failure, are held one by one as
  they are today, and dispatch continues.
- **c4:** The loop's status and `tillr drain why` say in one line that dispatch is stopped, why,
  and which defect item will release it.
- **c5:** When the defect item lands, the next pass dispatches again, and the two held items are
  re-admitted under the existing rule for a changed input.
- **c6:** The repeated-refusal report keeps its meaning, as a report that holds nothing. Its
  comment no longer cites the closed stop set of the day-two design as the reason no failure
  stops the loop.

**Agent Details:**

- **Proposed id:** `item-drain-unrelated-failure-stop`.
- **Paths:** `tools/tillr-drain/`, `tools/tillr-view/`,
  `yolo-packs/tillr/files/local/share/tillr/tillr-drain/`,
  `yolo-packs/tillr/files/local/share/tillr/tillr-view/`, and this document.
- **Waits on:** `item-drain-hold-cause-statement`, the unit above.
- **Ref:** [Section 3.2](#32-every-hold-names-a-mover-that-can-act) and
  [the risks table](#6-risks), whose third row relies on this rule.
- **Where:** the defect path is `fileDefectItem`, with `DefectItemPrefix` and
  `ReasonToolingDefect`, in `tools/tillr-drain/drain/defect.go`. The report is
  `RepeatedRefusalThreshold` in `tools/tillr-drain/drain/refusal.go`. A new stop reason is
  entered in `tools/tillr-drain/drain/definition.go`, and its row goes in
  `tools/tillr-view/waiting.go` in the same commit. Run `just pack-sync` after the last source
  edit and commit the regenerated mirror.
- **Verify:** Add drain tests: two unrelated items held by one failure stop dispatch and file
  one defect; two overlapping items do not stop it; a third item files nothing new; and landing
  the defect resumes dispatch. Add a view test for the stopped line. Run `go test ./...` in
  `tools/tillr-drain` and `tools/tillr-view`.

## 5. What this does not propose

- No new item state, and no schema change for steps 1 to 4.
- No change to the gate's checks, the lander's squash, or the run wall's removal. Those three are
  root-cause fixes and they hold.
- No ruling on the twelve questions in the four designs under review. Steps 1 to 6 depend on none
  of them.
- No retry budget or cooldown. The one count proposed here was the single re-admission of
  [Section 3.2](#32-every-hold-names-a-mover-that-can-act), after which a person decides
  (superseded by `D-JL10`, which counts no endings and removes input-change re-admission).
- No committed queue snapshot and no rank in a document.

## 6. Risks

| Risk | Mitigation |
| :--- | :--- |
| The 45 seed rows include one that never landed | Before a row is deleted, its criteria are checked against `main`; a row that does not pass becomes an item block instead |
| A re-admitted item loops on a change that does not matter | The cause statement names which input changed, and an item that fails again after one re-admission stays held until the owner runs `tillr drain retry` (superseded when `D-JL10` removed input-change re-admission at `f8774bd63`) |
| The first drain on new models fails for reasons that are not the items' | It runs supervised on one lane, and a machinery failure stops it under the two-item rule |

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-TC1` | A directive counts only when its opening `<!--` starts a line outside a fenced block. **Rejected:** skipping fences alone, which reads about forty inline mentions as malformed blocks. | 2026-11-15 | [Section 1](#1-what-a-cold-machine-finds-today) | ✅ (`tools/tillr-item/item/sync.go`, `TestSyncIgnoresInlineMentionsAndFencedDirectives`; slated for retirement once the companion-plan reader of [`OQ-QM1`](tillr-queue-mirrors-designs.md#OQ-QM1) in [`tillr-queue-mirrors-designs.md`](tillr-queue-mirrors-designs.md) is built) |
| `D-TC2` | Sync is the only intake, is idempotent, never rewrites rank, runs or `landed`, and treats a changed block as a changed item. A block may name a `seat` (amended 2026-11-15, [ruling Section 1](queue-persistence-ruling.md#1-shared-item-block-shape)), which is part of the definition a changed block changes. **Rejected:** a `-v2` clone as the way to revise a judged item. | 2026-11-15 | [Section 3.1](#31-the-queue-is-read-from-documents-and-from-nothing-else) | Partial. Built: `tillr-item sync`, which the loop runs at start and after each trunk change, is the only document-block intake now that `item-seed-retire` has landed (`tillr-item` also accepts store-declared CLI filings `idea`, `doc-triage` and `request`, and [`OQ-QM1`](tillr-queue-mirrors-designs.md#OQ-QM1) in [`tillr-queue-mirrors-designs.md`](tillr-queue-mirrors-designs.md) replaces hidden item comments with mirrored roadmaps and companion `<topic>-plan.md` files once built). Sync is idempotent, never moves state, rank or runs, and updates a changed definition through an `item.update` event; once a criterion carries a state, the row is left alone and reported `definition-frozen`. **Not built, and not filed:** a removed block retiring its item. The landed `item-sync-hold-unshared-items` handles that same case another way: an open item with no block (`UnsharedOpenIDs` in `tools/tillr-item/item/sync.go`) goes to triage on the design seat (`triageDecision` in `tools/tillr-drain/drain/document_prerequisite.go`, called from `tools/tillr-drain/drain/plan.go`) and is not retired. Which of the two rules stands is a design decision this document owes, so no item is filed for it |
| `D-TC3` | Every hold carries one of three classes and a mover; a post-run hold records a cause statement with its inputs. **Rejected:** a retry door per hold reason. | 2026-11-15 | [Section 3.2](#32-every-hold-names-a-mover-that-can-act) | Partial. Built: the classes and movers, in the definitions every hold reason is entered in (`tools/tillr-drain/drain/definition.go`, `TestAllDefinedHoldReasonsAreClassified`) and the waiting face that shows them (`tools/tillr-view/waiting.go`, `TestWaitingEveryDrainReasonHasARule`); worker refusals also return with normalized check-log digests (`tools/tillr-drain/drain/returned.go`) and worker `tooling` obstacles file defect items (`tools/tillr-drain/drain/defect.go`). Not built: the post-run hold cause statement and the two-unrelated-items rule, written as the last two units of [Section 4](#4-what-i-would-build-in-order) |
| `D-TC4` | One read face answers why each unlanded item is not moving, text first; a new hold or stop ships its row in the same commit. **Rejected:** fixing the board one field at a time. | 2026-11-15 | [Section 3.3](#33-every-decision-is-shown-where-the-owner-looks) | ✅ (`tools/tillr-view/waiting.go` and `tools/tillr-view/why.go`) |
| `D-TC5` | The pipeline regenerates derived copies between commit and gate; tests assert structure and never a count. **Rejected:** asking the worker to regenerate, and a pre-commit hook, which fixes the human path only. | 2026-11-15 | [Section 3.4](#34-a-derived-copy-is-produced-by-the-pipeline) | — Not filed. It waits on [OQ-SH2](tillr-selfhosted-build.md#OQ-SH2) in [the self-hosted build design](tillr-selfhosted-build.md), which [Section 3.4](#34-a-derived-copy-is-produced-by-the-pipeline) leaves there. That question decides what becomes of the pack mirror and its check. Until it is ruled, the module list keeps multiple homes (`MODULES` in `tests/pack-sync.test.py`, `MIRRORED_MODULES` in `tools/pack-docs.py`, `MODULES` in `tests/tillr-docs.test.py` and `tests/tillr-build.test.py`, and the watch list in `tools/fork-check.py`) |
| OQ-TC1 | Delete `config/tillr/seed-items.json` and the `seed` face of `tillr-item`; sync is the only intake. The seven rows that are neither landed nor a restated open question become prose entries on [`docs/pi/roadmap.md`](../pi/roadmap.md), and the row-count pin goes. Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ("fill them in through principles"); the principle is the owner's standing goal that state lives in documents, and [`queue-persistence-ruling.md`](queue-persistence-ruling.md)'s ruling that documents are the only intake. The owner may overturn it. **Rejected:** pruning the file and keeping it; teaching sync to ignore the seed's state. | 2026-11-15 | [Section 3.1](#31-the-queue-is-read-from-documents-and-from-nothing-else) | ✅ (`item-seed-retire`; `config/tillr/seed-items.json` is deleted and `tillr-item seed` refuses in `tools/tillr-item/main.go`) |
| OQ-TC2 | Tillr's tracked policy names seats and, for each seat, the harness that fills it; the model id comes from that harness's own profile on the machine, so tillr's tree names no model. The per-machine-group policy files stay as the stopgap until a harness profile can answer for the design seat. Unlike a pure "seat only" policy, which harness fills a seat stays a tillr choice, because the owner asked on 2026-11-15 to choose which seats run on the Claude subscription. Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ("fill them in through principles"); the principles are AGENTS.md's "Pi's model and provider are DERIVED from the selected profile" and the backend policy's rule that no model id is copied into `tools/tillr*`. The owner may overturn it. **Rejected:** per-group policy files as the target; an untracked per-machine file. | 2026-11-15 | [Section 3.5](#35-a-seat-names-its-harness-and-the-harness-names-the-model) | Partial (the stopgap: the five `config/tillr/backend-policy*.json` files — `backend-policy.json`, `backend-policy.bedrock.json`, `backend-policy.claude.json`, `backend-policy.mixed.json` and `backend-policy.copilot.json` — each still naming model ids). Not filed: the model ids stay until a harness profile can answer for the design seat, and [the planning-node model tiering design](tillr-planning-node-model-tiering.md), where [`OQ-PT1`](tillr-planning-node-model-tiering.md#OQ-PT1) to [`OQ-PT6`](tillr-planning-node-model-tiering.md#OQ-PT6) are ruled or closed in its ledger and only [`OQ-PT7`](tillr-planning-node-model-tiering.md#OQ-PT7) and [`OQ-PT8`](tillr-planning-node-model-tiering.md#OQ-PT8) remain open (blocked on landed planned items), maps seats to backend rows that still carry model ids |
| OQ-TC3 | A held item is dispatched again by itself when an input it failed on has changed (its item block, its allowed paths, the backend policy, or the tillr build); unchanged inputs never re-run; an item that fails again after one such re-admission stays held until the owner runs `tillr drain retry`. Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ("fill them in through principles"); the principles are [landing principle 6](landing-principles.md#6-a-budget-escalates-it-does-not-loop) (a budget escalates, it does not loop) and `D-JL6` in [`tillr-judge-and-lifecycle.md`](tillr-judge-and-lifecycle.md) (re-queue only against a recorded basis). The owner may overturn it. **Rejected:** always waiting for the owner; re-admitting only on an edit to the item's own block. **Superseded by the owner's ruling of 2026-11-17** (`D-JL10` in [the judge and lifecycle design](tillr-judge-and-lifecycle.md#decision-ledger)): nothing re-runs because an input's hash changed, and no ending is counted. | 2026-11-15 | [Section 3.2](#32-every-hold-names-a-mover-that-can-act) | Built at `72dc84e4a4`, removed at `f8774bd63`; `tools/tillr-drain/drain/readmit.go` now only records a run's inputs (`TestAChangedInputReDispatchesNothing` in `tools/tillr-drain/drain/readmit_test.go`) |
