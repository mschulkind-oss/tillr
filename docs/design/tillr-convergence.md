---
title: "Why tillr keeps hitting walls: three causes, one target"
status: accepted
stage: DECIDED
updated: 2026-11-15
tags:
  - tillr
  - drain
  - queue
  - observability
  - design
summary: "Reads three days of tillr history as three structural causes rather than many defects — derived copies that are checked instead of derived, failures that become holds only a person can clear, and decisions that are recorded but not shown — and sets one target: a cold machine rebuilds the queue from documents, drains it, and shows every stopped item with the act that moves it."
next: "Build removed-block retirement, item-seed-retire and item-drain-readmit-on-changed-input; then rebuild this machine's queue from the documents with tillr item sync."
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
directory. Since then the block reader and the trailer deletion are built (`D-TC1`, and `D-TC2`
in part — see the [Decision Ledger](#decision-ledger)); nothing else here is. The three open
questions were settled on 2026-11-15 by the session's designer on recorded principle, at the
owner's request; the owner may overturn any of them.

> **In short.** The walls are not complexity. Almost every fix of the last three days repairs one of
> three structural causes, and each cause has a principle already written down in this tree that
> nothing enforces. Enforce those three, in order, and the loop stops needing repair.

**Why it matters.** `git log --since=2026-11-13` on `main` answers 169 commits in three days, and
most of them touch tillr. Three
areas were fixed, then reworked inside a day: the fence, the boundary bracket, and who owns the
queue's definition (four ownership models in about 38 hours).

**The shape.** One invariant to build toward — *delete `.tillr/`, sync, drain* — and three rules
that make it hold: derive every copy, give every hold a mover, show every decision.

**Cost.** The seed file, its count pin, the commit trailer and the hand-run mirror go away. A held
item may run again without a person asking for it, which is a change in who authorizes spend.

**Start at [Section 2](#2-the-three-causes)** — the causes. The target and the order fall out of it.

**Needs your ruling:** None.

**Reads with:** [`queue-persistence-ruling.md`](queue-persistence-ruling.md) (the ruling whose last
two commits this design executes first), [`landing-principles.md`](landing-principles.md) (the
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
| What the only working bootstrap produces | `tillr-item seed` writes 62 rows from [`seed-items.json`](../../config/tillr/seed-items.json), 48 of them `in-queue`. |
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
| The seed row count in [`seed_test.go`](../../tools/tillr-drain/seed_test.go) | the seed file | 23 commits touched the file since 2026-11-13; the count is re-based by hand |
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
  ([OQ-TC1](#decision-ledger).)

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
  ([OQ-TC3](#decision-ledger).)

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
- **The per-machine-group policy files are the stopgap.** `backend-policy.json` and
  `backend-policy.bedrock.json` stay until a harness profile can answer for the design seat, then
  the model ids leave them. ([OQ-TC2](#decision-ledger).)

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

Two attempts at this work built the retirement and neither produced a candidate the loop could
gate, for reasons this section did not state. Both replaced this section with a "Built" note.
The loop refuses that before the gate, as a worker editing its own item block, and it writes the
retirement itself once the change has landed. Removing the block also orphaned the dependency
that `item-oa-move-to-top` in [`observatory-actions.md`](observatory-actions.md) declares on
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

<!-- tillr: item
     id="item-seed-retire"
     blocked_by=""
     paths="config/tillr/,tools/tillr-item/,tools/tillr-drain/,tools/tillr-cli/,tools/tillr-view/,tools/gate/,tools/fork-check.py,tests/fork-check.test.py,docs/,yolo-packs/tillr/"
-->

- **c1:** The seed file `config/tillr/seed-items.json` and the `seed` face of `tillr-item` are gone, and `tillr item seed` answers a usage refusal that names `tillr item sync`.
- **c2:** The seven seed rows that were neither landed nor a restated open question appear as prose entries on `docs/pi/roadmap.md`.
- **c3:** The gate's legacy-seed intake freeze is gone with the file, where it lives today. The check was moved out of `tools/fork-check.py` into the gate's change declarations (the per-check files under `tools/gate/checks/`), so that directory is where it is removed: `tools/gate/checks/seed-freeze.json` and `tools/gate/checks/seed-freeze.py` are deleted; `tools/gate/checks/change_common.py` no longer defines `LEGACY_SEED` or `legacy_seed_findings` and has no `seed-freeze` branch; `tools/gate/checks/change_checks_test.py` and `tools/gate/checks/change-checks.test.py` no longer write a seed fixture or name the check, and a regression of theirs that exists only to judge the seed is deleted with it. `tools/fork-check.py` no longer describes `config/tillr/seed-items.json` (its step 8b), `tests/fork-check.test.py` no longer writes a seed fixture or names `seed-freeze`, and `tools/tillr-drain/seed_test.go` and its row-count pin are removed. `rg 'seed-freeze|LEGACY_SEED|legacy_seed_findings' tools/gate tools/fork-check.py tests/fork-check.test.py` returns nothing, and the suites under `tools/gate/` and `tests/fork-check.test.py` pass. Deleting the declaration leaves one fewer fast check, so the number of fast checks that `tools/gate/checks/tree_checks_test.py` pins drops by one in the same change; that pin counts the gate's declarations and is not a seed fixture.
- **c4:** The store keeps a way to set an item's rank after the seed is gone: `tillr item rank` sets the rank of one item the store already holds and changes nothing else about that item (its state, criteria, allowed paths and runs are as they were), refuses an item the store does not hold, and a test pins both. Run without `--rank`, the command is a usage refusal that leaves the item's rank and its events as they were, and a test pins that too; an explicit empty `--rank` is a different request and is not refused by this rule.
- **c8:** The pack's dated documentation snapshot is not part of this change. `yolo-packs/tillr/files/local/share/tillr/docs/snapshot.json` and every delivered file it names are byte for byte as they were. Each is the copy of a blob that file names, `tests/pack-sync.test.py` recomputes each copy from its blob, and only `just pack-docs` rewrites them, from a committed tree. So the rule that a Markdown link to a deleted file becomes inline code covers source documents and stops at those copies: correcting a copy by hand fails that suite, and refreshing the snapshot needs a commit this change does not yet have. Under `yolo-packs/tillr/files/` the change is what `just pack-sync` writes and nothing else, and `python3 tests/pack-sync.test.py` exits 0. The copies keep their links to the deleted files until the snapshot is next refreshed on the trunk, and one of them, the link to `tillr-drain/seed_test.go` in the copy of this document, stops resolving when the mirror drops that file. Measured on 2026-11-17 at `f0d04f12e`: with that mirror file moved aside, `uvx vantage-check` on the copy reports `link/missing-target` for it, and the gate's document check judges only the Markdown files a change touches, so it does not read the copy. That is the snapshot's lag behind its source, which the suite's `--fresh` mode reports and a later `just pack-docs` on the trunk closes. It is not a finding against this change and not a reason to stop, and this change neither runs `just pack-docs` nor edits `tools/pack-docs.py`.
- **c5:** The board's unranked heading in `tools/tillr-view/web/src/components/QueueRail.tsx`, its tests and `tools/tillr-view/web/README.md` name `tillr item rank` as the command that records a priority, and no file under `tools/tillr-view/` outside test fixture item ids names `config/tillr/seed-items.json` or `tillr-item seed`.
- **c6:** The change leaves this section of `docs/design/tillr-convergence.md` byte for byte as it was, from its heading to the next heading, and leaves every other item block's directive as it was, including the `blocked_by` of `item-oa-move-to-top` in `docs/design/observatory-actions.md`. The change does not mark this item built: the loop retires the block after landing. Outside this section the document may be brought up to date, which covers its `next` line, the two tables in Sections 1 and 2 and the rows for this ruling and for sync in the [Decision Ledger](#decision-ledger). A Markdown link in any tracked document to a file this change deletes becomes inline code naming the former path, and `uvx vantage-check` passes on each changed Markdown file, named one by one.
- **c7:** No test fixture depends on the deleted seed. `commit()` in `tools/gate/checks/change_checks_test.py` stages only paths its fixture still writes, so it no longer names `config`, and a fixture that needs an unrelated tracked file puts it under a directory the fixture already tracks. `python3 tools/gate/checks/change_checks_test.py`, `python3 tools/gate/checks/tree_checks_test.py` and `python3 tests/tillr-docs.test.py` each exit 0, and so does `go test ./item -run '^TestSyncOverThisRepositorysDocuments$'` in `tools/tillr-item`.

### Re-admit a held item when an input it failed on changes

Built: this landed in commit `72dc84e4a4`.

## 5. What this does not propose

- No new item state, and no schema change for steps 1 to 4.
- No change to the gate's checks, the lander's squash, or the run wall's removal. Those three are
  root-cause fixes and they hold.
- No ruling on the twelve questions in the four designs under review. Steps 1 to 6 depend on none
  of them.
- No retry budget or cooldown. The one count is the single re-admission of
  [Section 3.2](#32-every-hold-names-a-mover-that-can-act), after which a person decides.
- No committed queue snapshot and no rank in a document.

## 6. Risks

| Risk | Mitigation |
| :--- | :--- |
| The 45 seed rows include one that never landed | Before a row is deleted, its criteria are checked against `main`; a row that does not pass becomes an item block instead |
| A re-admitted item loops on a change that does not matter | The cause statement names which input changed, and an item that fails again after one re-admission stays held until the owner runs `tillr drain retry` |
| The first drain on new models fails for reasons that are not the items' | It runs supervised on one lane, and a machinery failure stops it under the two-item rule |

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-TC1` | A directive counts only when its opening `<!--` starts a line outside a fenced block. **Rejected:** skipping fences alone, which reads about forty inline mentions as malformed blocks. | 2026-11-15 | [Section 1](#1-what-a-cold-machine-finds-today) | ✅ (`tools/tillr-item/item/sync.go`; the suite syncs this repository's tracked documents and asserts zero findings) |
| `D-TC2` | Sync is the only intake, is idempotent, never rewrites rank, runs or `landed`, and treats a changed block as a changed item. A block may name a `seat` (amended 2026-11-15, [ruling Section 1](queue-persistence-ruling.md#1-shared-item-block-shape)), which is part of the definition a changed block changes. **Rejected:** a `-v2` clone as the way to revise a judged item. | 2026-11-15 | [Section 3.1](#31-the-queue-is-read-from-documents-and-from-nothing-else) | Partial (`tillr-item sync`, run at `tillr-drain loop` start: idempotent, never moves state, rank or runs, and updates a changed definition through an `item.update` event while no criterion carries a state — once one does, the row is left alone and reported `definition-frozen`. Not built: a removed block retiring its item; and `tillr-item seed` still exists until `item-seed-retire` lands under [OQ-TC1](#decision-ledger)) |
| `D-TC3` | Every hold carries one of three classes and a mover; a post-run hold records a cause statement with its inputs. **Rejected:** a retry door per hold reason. | 2026-11-15 | [Section 3.2](#32-every-hold-names-a-mover-that-can-act) | — |
| `D-TC4` | One read face answers why each unlanded item is not moving, text first; a new hold or stop ships its row in the same commit. **Rejected:** fixing the board one field at a time. | 2026-11-15 | [Section 3.3](#33-every-decision-is-shown-where-the-owner-looks) | — |
| `D-TC5` | The pipeline regenerates derived copies between commit and gate; tests assert structure and never a count. **Rejected:** asking the worker to regenerate, and a pre-commit hook, which fixes the human path only. | 2026-11-15 | [Section 3.4](#34-a-derived-copy-is-produced-by-the-pipeline) | — |
| OQ-TC1 | Delete `config/tillr/seed-items.json` and the `seed` face of `tillr-item`; sync is the only intake. The seven rows that are neither landed nor a restated open question become prose entries on [`docs/pi/roadmap.md`](../pi/roadmap.md), and the row-count pin goes. Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ("fill them in through principles"); the principle is the owner's standing goal that state lives in documents, and [`queue-persistence-ruling.md`](queue-persistence-ruling.md)'s ruling that documents are the only intake. The owner may overturn it. **Rejected:** pruning the file and keeping it; teaching sync to ignore the seed's state. | 2026-11-15 | [Section 3.1](#31-the-queue-is-read-from-documents-and-from-nothing-else) | — (`item-seed-retire`) |
| OQ-TC2 | Tillr's tracked policy names seats and, for each seat, the harness that fills it; the model id comes from that harness's own profile on the machine, so tillr's tree names no model. The per-machine-group policy files stay as the stopgap until a harness profile can answer for the design seat. Unlike a pure "seat only" policy, which harness fills a seat stays a tillr choice, because the owner asked on 2026-11-15 to choose which seats run on the Claude subscription. Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ("fill them in through principles"); the principles are AGENTS.md's "Pi's model and provider are DERIVED from the selected profile" and the backend policy's rule that no model id is copied into `tools/tillr*`. The owner may overturn it. **Rejected:** per-group policy files as the target; an untracked per-machine file. | 2026-11-15 | [Section 3.5](#35-a-seat-names-its-harness-and-the-harness-names-the-model) | Partial (the stopgap: `config/tillr/backend-policy.json` and `config/tillr/backend-policy.bedrock.json`, each still naming model ids) |
| OQ-TC3 | A held item is dispatched again by itself when an input it failed on has changed (its item block, its allowed paths, the backend policy, or the tillr build); unchanged inputs never re-run; an item that fails again after one such re-admission stays held until the owner runs `tillr drain retry`. Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ("fill them in through principles"); the principles are [landing principle 6](landing-principles.md#6-a-budget-escalates-it-does-not-loop) (a budget escalates, it does not loop) and `D-JL6` in [`tillr-judge-and-lifecycle.md`](tillr-judge-and-lifecycle.md) (re-queue only against a recorded basis). The owner may overturn it. **Rejected:** always waiting for the owner; re-admitting only on an edit to the item's own block. | 2026-11-15 | [Section 3.2](#32-every-hold-names-a-mover-that-can-act) | — (`item-drain-readmit-on-changed-input`) |
