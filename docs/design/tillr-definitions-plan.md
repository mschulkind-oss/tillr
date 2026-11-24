---
title: "Build plan: a tillr core with Git landing as its first definition"
status: accepted
stage: DECIDED
updated: 2026-11-19
tags:
  - tillr
  - plan
  - store
  - boundaries
  - product-qa
summary: "The build hand-off for the owner's 2026-11-17 go-ahead: eighteen items, all landed, that turn tillr into a core that does not assume Git landing, with Git landing supplied by one definition and a second, runnable definition that lands nothing. Each item says what the Product QA pipeline can try once it lands. The next tranche is written in prose, still unfiled."
next: "The first tranche has landed. Product QA's agent runs the worked example and writes round three, and the owner reviews the designer's decisions the build rests on (D-CG1 to D-CG5 in the direction document's ledger); only then is the next tranche filed."
depends-on:
  - tillr-core-and-git-landing.md
  - tillr-separable-parts.md
  - tillr-product-qa-requests.md
  - ../reference/tillr-item-graphs.md
  - tillr-judge-and-lifecycle.md
---

# Build plan: a tillr core with Git landing as its first definition

**Status:** 2026-11-17. Written against `4df6e426f`. The owner said to build on 2026-11-17: "file
item(s) to build as much of the new tillr as possible so autoqa can play with the real thing and
comment for real the next round". By "autoqa" he means the Product QA pipeline (see
[the words](#the-words-this-plan-uses)). The eighteen items below were queued that day, and all eighteen had landed by 2026-11-18 (read from the store's status face against the tree at `dcb76fe41`). They rest on
six choices, two of them the owner's own words and the rest the designer's decisions, which await
the owner's review. All are recorded in
[the direction document, Section 8](./tillr-core-and-git-landing.md#8-building-it-the-owners-go-ahead-and-the-working-choices).

> **In short.** Five items have no dependency and can start at once. One of them fixes a defect in
> tillr's own use: a landed item can be put back in the queue by one stray write. The chain ends in
> a worked example: a definition that lands nothing, runnable from an empty directory with no Git
> repository. That example is what Product QA copies, and it is the acceptance test for the set.

**Why it matters.** Product QA's second round asked for six things before a findings queue could run
on tillr ([their requests R2-1 to R2-6](./tillr-product-qa-requests.md#4-requests-for-this-round)).
The owner's direction is that tillr is a general core and that Git landing is one definition on top
of it ([the direction](./tillr-core-and-git-landing.md#1-the-direction-in-the-owners-words)). This
plan builds both at once, because the second use is what tests the split.

**Reads with:** [the direction](./tillr-core-and-git-landing.md) (what the core and a definition
are, and the inventory these items are grounded in),
[the separable-parts proposal, Section 7](./tillr-separable-parts.md#7-the-second-round-tillrs-answer)
(each of Product QA's requests and the item that builds it), and
[Product QA's reply](./tillr-product-qa-requests.md).

This document holds no open question on purpose. The drain holds an item whose own document still
asks something, so the questions stay in the direction document and this plan links them.

---

## The words this plan uses

- **Product QA:** a product bug-finding pipeline, with the fuzzer Mangle as its prober. Its
  agent wrote [the second-round reply](./tillr-product-qa-requests.md). It is not
  [tillr-autoqa.md](./tillr-autoqa.md), which is tillr's own exploratory-testing design.
- **Definition** *(a placeholder, coined in the direction document; the owner's word is **charter**)*:
  the layer that turns tillr's core into one use. It is a data file plus one hook program.
  **`git-landing`** is the name of the definition for landing changes in Git, which is everything
  tillr does today. Both were placeholders when this plan was written; the owner ruled on 2026-11-18
  that the layer is a charter ([OQ-VO4](./tillr-vocabulary.md#OQ-VO4)) and that the first use is
  `git-landing` ([OQ-VO5](./tillr-vocabulary.md#OQ-VO5)). This plan and the shipped code still say
  "definition"; the code is not renamed. Every item keeps the words in as few places as possible, so
  a rename is mechanical.
- **Definition file:** the JSON file that states a definition's vocabulary and rules. Coined here.
- **Hook program:** the one program a definition names, which the core calls for each step it
  cannot decide itself. The six step programs the drain runs today (gate, land, verdict, replay,
  rolling, fixer gate) become its first hooks. Coined here.
- **Ready state, finished states:** the definition's names for the state the planner dispatches
  from and the states that end an item. Today they are `in-queue` and `landed`,
  `resolved-elsewhere` and `closed`.
- **Prerequisite:** a condition the planner checks before it dispatches an item, such as having a
  fence. Today every prerequisite is a hold reason written in Go; here a definition names which ones
  apply to it.
- **Fence:** the set of paths an item may change. **Hold:** the planner's named reason for not
  dispatching an item. **Lane:** a leased slot with its own directory where one run executes.
- **Store that does not land:** a store whose definition completes items some way other than a
  landing. Coined in [Product QA's R2-2](./tillr-product-qa-requests.md#4-requests-for-this-round).

---

## 1. How to read and build this

**Precedence.** The direction document wins on behavior, the tree wins on cosmetic fact, and this
plan is the first thing to be wrong. An item's criteria are its contract. When a criterion and the
tree disagree beyond cosmetics, the worker stops and reports instead of twisting the code.

**What the items share.** Every item that changes core behavior carries the same safety rule as a
criterion: today's Git-landing queue behaves identically, the existing suites pass unchanged, and a
store created before the change opens and drains. The drain runs on this code while it is rebuilt,
so a change that breaks today's queue stops the building of the rest.

**Order.** The planner dispatches ready items in id order and serializes nothing by fence: two items
whose fences overlap can run at once (only a shared fork is excluded, `forkHeldBy` in
[plan.go](../../tools/tillr-drain/drain/plan.go)). On 2026-11-17 a 22-minute run hit squash
conflicts with sibling landings in `pass.go` and `settings.go`. So every item that rewrites the same
package as another names it in `blocked_by`, and the order below is enforced by edges, not by
names. Three chains run beside each other:

| Chain | Items, in order |
| :--- | :--- |
| The item utility | [finished states final](#prevent-raw-writes-from-reopening-a-landed-or-resolved-item), [list items](#add-a-face-that-lists-a-stores-items), [unknown fields](#prevent-item-apply-from-dropping-a-field-it-does-not-store), [lifecycle](#move-item-states-and-their-moves-into-the-stores-definition), [receipts](#add-a-reason-pointer-and-actor-to-every-finished-state-move) |
| The core and the store | [tables at init](#add-every-utilitys-tables-when-a-store-is-created) and [the definition file](#add-the-definition-file-and-the-git-landing-definition), then [the store's definition](#add-the-definition-a-store-runs-to-the-store-itself) |
| The drain | [planner](#route-the-planners-ready-finished-and-prerequisite-tests-through-the-definition), [hook contract](#add-the-hook-program-contract-and-check-it-at-loop-start), [start without Git](#route-the-loops-start-around-git-when-the-definition-does-not-land), [plain directories](#add-plain-directory-staging-for-a-definition-that-does-not-land), [finish on the hook](#add-completion-through-the-definitions-hook-for-items-that-never-land), [command nodes](#route-a-command-node-into-the-lane-directory-and-name-its-node), [the worked example](#add-a-worked-definition-that-lands-nothing-runnable-end-to-end) |

Three items stand alone and can run at once with anything:
[the run's environment](#add-the-item-and-node-to-every-runs-environment),
[the item page](#add-every-run-verdict-decision-and-state-move-to-the-item-page), and, once the hook
contract lands, [tillr-loop as the hook program](#move-tillr-loop-into-being-the-git-landing-definitions-hook-program).

**Where the code is.** The inventory is
[the direction, Section 3](./tillr-core-and-git-landing.md#3-where-the-git-landing-assumption-is-wired-in-today).
The facts each item quotes were re-run on 2026-11-17 on binaries built from `2a35749f6`; the
commands are in [the separable-parts Evidence](./tillr-separable-parts.md#evidence).

**Reuse before writing.**

- Schema registration is per owner, in an `init()`: `RegisterDDL` in
  [registry.go](../../tools/tillr/store/registry.go). A table belongs to the utility that
  registers it.
- Moves into and out of `closed` already record their reason and actor in the event:
  `recordLifecycle` in [lifecycle.go](../../tools/tillr-item/item/lifecycle.go). Extend it rather
  than adding a second event shape.
- The drain already checks its step programs' flags before dispatch:
  [step_check.go](../../tools/tillr-drain/drain/step_check.go), landed in `1076e92aa`. The hook
  check extends it.
- The adapter-and-record shape the hook program follows is
  [tillr-loop](../../tools/tillr-loop/tillr-loop): one file, one face per step, a record out.

**Traps.**

- `tillr item sync` reads only Git-tracked files under `docs/`, so a block in an uncommitted file
  is invisible to a dry run until it is staged.
- A store's tables are created by whichever utility opens it first and migrates. A read face opens
  read-only and never migrates, which is why a store only `init` touched answers "no such table".
- The pack carries a byte-identical copy of every `tools/tillr*` module, so any change under
  `tools/tillr*` also changes `yolo-packs/tillr/`, which is why every fence names it.
- No Git, file or process work runs inside a SQLite write transaction anywhere in tillr; a hook
  call is process work.

---

## 2. The first tranche

### Prevent raw writes from reopening a landed or resolved item

A landed item is a claim that its work is on the trunk, and a resolved item is a claim that
something else satisfied it. Today either claim can be undone by one command, and the drain would
then dispatch the work a second time. Product QA found it in their trial on 2026-11-16, and it was
confirmed on the current build on 2026-11-17: `tillr item state --state in-queue` moves an item out
of `landed` and out of `resolved-elsewhere`, and `tillr item apply` of a record saying `in-queue`
with a new title rewrote a landed item. The item utility checks only moves into and out of
`closed` (`checkClosedTransition` in [lifecycle.go](../../tools/tillr-item/item/lifecycle.go)). This
is a defect in tillr's own Git-landing use, so it depends on nothing and goes first.

**Product QA can try:** finishing an item and trusting that it stays finished.

**Agent details:** the queued
[terminal-state block](./tillr-judge-and-lifecycle.md#prevent-landed-and-resolved-states-without-their-evidence)
asks for the same refusal in its c3; once this lands, that criterion is already true, and its
worker finds it so. The two criterion rules `tillr item apply` already has stay as they are and
are checked first, which answers the conflict a worker raised on 2026-11-17:
`TestAStateMoveDoesNotUnfreezeACriterion` in [state_test.go](../../tools/tillr-item/state_test.go)
changes a landed item's criterion state and requires `criterion-state`, and it passes unchanged.
`state-terminal` is the name for a change those two rules let through.



### Add every utility's tables when a store is created

The first command a new consumer runs on a fresh store fails. On 2026-11-17, on a store that only
`tillr store init` had touched, `tillr drain why` and `tillr view waiting` exited 3 on "no such
table: items", and `tillr run lanes` and `tillr run status` on `lanes` and `runs`. The tables exist
only after the drain's loop has opened the store once, because each utility registers its own
tables and `tillr-store` links none of them. Product QA asked for this first (R2-1): views and the
planner should work on a store the drain never opened.

**Product QA can try:** `tillr store init`, then any read face, with nothing in between.

**Agent details:** advice, with its reason: linking each utility's schema package into
`tillr-store` (a blank import of the package that calls `RegisterDDL`, through the modules' local
`replace` lines) makes `init` create the same schema the loop does, with no read face changed. No
module imports `tillr-store`, so this adds no import cycle. Another way that meets the criteria is
fine. `tillr run status` reports one run and has no empty answer: it needs `--run <id>`, and for a
run the store does not hold it prints its record and exits 1 (`statusFace` in
[main.go](../../tools/tillr-run/main.go)). That contract stays, which answers the conflict a worker
raised on 2026-11-17 against a c1 that asked that face for exit 0; no file of the run utility
changes.



### Add the definition file and the Git-landing definition

Everything tillr does today assumes one use, landing changes in Git, and that use is written into
constants across the item utility, the planner and the drain. Before any of it can move, the rules
need one place to live. This item adds the definition file, a JSON file that states a use's
vocabulary and rules, and a package in the core library that reads and checks it. It also states
today's behavior as the first definition, `git-landing`, embedded in that package. Nothing reads the
package yet, so nothing changes. The names `definition` and `git-landing` were placeholders the owner
had not chosen when this item was written (he has since named the layer a charter and kept
`git-landing`, in [OQ-VO4](./tillr-vocabulary.md#OQ-VO4) and [OQ-VO5](./tillr-vocabulary.md#OQ-VO5)), and this item keeps them in one constant so that a rename is one line.

**Product QA can try:** reading the definition package's README, which is the contract a second use
writes its definition against.

**Agent details:** the keys are the owner's working choice for shape D and lifecycle A
([the direction, Section 8](./tillr-core-and-git-landing.md#8-building-it-the-owners-go-ahead-and-the-working-choices)).
The Git-landing values come from `writableStates` and `frozenStates` in
[apply.go](../../tools/tillr-item/item/apply.go), [lifecycle.go](../../tools/tillr-item/item/lifecycle.go),
and the hold reasons in [schema.go](../../tools/tillr-drain/drain/schema.go).



### Add the definition a store runs to the store itself

Every utility that opens a store has to agree on which definition the store runs, or the item
utility would accept a state the planner does not know. The store is the one thing they all open,
so the definition is recorded there, once, when the store is made. A store with nothing recorded
runs `git-landing`, which is every store that exists today. This is the owner's working choice for
where the declaration lives; the direction document records it for him to confirm.

**Product QA can try:** `tillr store init --definition <file>` with a definition of their own, and
`tillr store definition` to read it back.

**Agent details:** the reader every utility uses lives in the definition package; no utility parses
the stored content itself.



### Add a face that lists a store's items

A consumer that wants to see its queue has no command to ask. `tillr item show` needs an item id,
and the only list of items is the waiting face, which lists open items and their holds. Product QA
reported this as a correction to tillr's stable surface, and it was confirmed on 2026-11-17. The
worked example at the end of this plan reads its outcomes through this face.

**Product QA can try:** `tillr item list --state <state>` to count findings by state.

**Agent details:** the face sits beside `show` in [main.go](../../tools/tillr-item/main.go) and
reads with the same query shape `show` uses in [show.go](../../tools/tillr-item/item/show.go).



### Prevent item apply from dropping a field it does not store

A consumer that sends a field tillr does not keep should be told, not ignored. On 2026-11-17 a
record carrying `"kind": "finding"` was applied with exit 0 and the field was gone. A consumer that
believes its field was stored will build on a value that is not there. The same check found an
undocumented rule: `apply` refuses any record whose `producer.system` is not `tillr`, which Product
QA hit and worked around. Both belong in the item utility's documented contract.

**Product QA can try:** applying their finding records and seeing any field tillr does not keep
named in the refusal.

**Agent details:** the payload type is `itemPayload` in
[apply.go](../../tools/tillr-item/item/apply.go). Every record tillr's own programs write must still
pass: sync's, the comment intake's, seed's and the loop's.



### Move item states and their moves into the store's definition

Which states an item can be in, which of them finish it, and whether a finished item may be
reopened are, by the owner's direction, choices for each use to make. Today they are constants in
the item utility. This item makes the item utility read them from the store's definition. With
`git-landing` nothing changes. With another definition, a use can name its own finished states, such
as `confirmed` and `rejected`, and allow leaving some of them, which the owner, as Product QA reports
him, wants to be the definition's policy ("reopening might be valid for some and not valid for
others"). Leaving a finished state is always a recorded event with a reason and an actor.

**Product QA can try:** a definition whose findings end `confirmed`, `rejected` or `inconclusive`, and
which allows a rejected finding to be reopened with a reason.

**Agent details:** the constants are `writableStates`, `frozenStates` and the closed rules in
[apply.go](../../tools/tillr-item/item/apply.go), [state.go](../../tools/tillr-item/item/state.go)
and [lifecycle.go](../../tools/tillr-item/item/lifecycle.go), and `terminalStates` in
[sync.go](../../tools/tillr-item/item/sync.go). The legacy frozen states (`abandoned`, `deleted`,
`in-progress`) stay readable and unwritable under every definition.



### Add a reason, pointer and actor to every finished-state move

A finished state is a claim, and a claim needs its evidence on the record. On 2026-11-17 `tillr item
state --state resolved-elsewhere --reason "repeat-of:f-0"` exited 0 and stored no reason, no
pointer and no actor: the flag was accepted and dropped. Product QA needs the reason a finding was
rejected and the finding it repeats. The owner's morning audit needs to see who decided. This
item stores all three on every move into a finished state, and refuses a flag instead of dropping
it.

**Product QA can try:** finishing a finding as a repeat of another, with the pointer to it, and reading
both back from `tillr item show`.

**What this item builds on:** the lifecycle item above, which is on the trunk. A first run of this
item on 2026-11-17 was staged from a base older than that landing, found the state face still on
hard-coded states, and stopped. It was right to stop, and the plan stands: this item is not built
on hard-coded states and does not absorb the lifecycle work. The check is one command,
`rg -n 'SetStateWithReceipt|MoveReceipt' tools/tillr-item/`. A tree where it prints nothing is
staged too early; the run reports that as a staging fault, and the answer is a restage on the
current trunk, not another change to this block.

What the lifecycle landing left for this item to extend, as read on the trunk on 2026-11-17:

- `tillr item state` already declares `--reason` and `--by`, and hands them to
  `SetStateWithReceipt` in [state.go](../../tools/tillr-item/item/state.go) as a `MoveReceipt`.
  That receipt is checked and recorded only when an item *leaves* a finished state. This item adds
  the pointer to the same receipt and uses it for a move *into* one. It adds no second receipt type
  and no second event shape.
- Which states are finished comes from the store's definition (`IsFinished`), never from a list in
  the item utility, so the three flags work under a definition whose finished states are
  `confirmed` and `rejected` exactly as under `git-landing`.
- The flags are accepted on a move into a finished state, not required by this item: the loop moves
  an item to `landed` with none of them, and c5 keeps that working. Requiring the pointer for
  `resolved-elsewhere` is the queued terminal-state block's c2, not this item's.
- `--reason` is stored on three moves once this item lands: into a finished state, out of one, and
  into the blocked state, where it is the reason the item waits. `--by` is stored on the first two.
  `--resolves-via` is stored only on the first. Every other use is the `flag-not-stored` refusal of
  c3, whose test therefore starts from a state that is neither finished nor blocked.
- A write of the state an item already has still answers the item and appends no event, so a
  repeated move does not overwrite a stored receipt.
- The legacy frozen states (`abandoned`, `deleted`, `in-progress`) stay readable and are never
  written, so no move into one exists and none takes a receipt.

**A second answer that this block replaces:** while the first run's stop was being answered, a
second amendment was drafted against the older base. It said this item could be built before the
lifecycle item, by reading the definition's finished list beside the hard-coded state tables, and
it kept the lifecycle item as a blocker. The lifecycle item landed before that draft did, and the
two did not merge. The draft is dropped, not merged: the tables it told a run to leave alone
(`writableStates`, `terminalState`, `terminalStates`) are no longer in the item utility
(`rg -n 'writableStates|terminalStates?\b' tools/tillr-item/` prints nothing on 2026-11-17), and
the blocker it kept is met. What it said that still holds is already above: finished states come
from the store's definition, and frozen states take no receipt. The marker below therefore names
no blocker.

**Agent details:** the pointer flag is `--resolves-via`, the name the queued
[terminal-state block](./tillr-judge-and-lifecycle.md#prevent-landed-and-resolved-states-without-their-evidence)
uses in its c2, so that block's criterion is met by this one rather than contradicted.



### Route the planner's ready, finished and prerequisite tests through the definition

The planner decides three things that belong to a use rather than to the core: which state an item
must be in to run, which states of a blocker free its dependents, and which conditions an item must
meet first. Today all three are Git landing's, written in Go. An item with no fence is held
`no-fence`, and an item with no design document is held for that, even in a store whose items have
neither on purpose. This item makes the planner read all three from the store's definition, and
puts the holds that only landing can raise in one table, so that a store that does not land never
sees them.

**Product QA can try:** `tillr drain why` on their findings store, with no item held `no-fence`.

**Agent details:** the landing-only holds are `candidate-no-op`, `boundary-scope-escape`,
`gate-failed`, `land-refused`, `no-fence`, `fork-held`, `trunk-broken`, `conflict-fix`,
`conflict-exhausted`, `conflict-design`, `conflict-owner`, `trunk-path-dirty`, `trunk-rewritten`
and `candidate-record-mismatch` (counted in
[the direction, Section 8.1](./tillr-core-and-git-landing.md#81-what-a-store-that-does-not-land-needs-from-the-core)).
The tests are `isBlocked`, `terminalStates` and the `StateInQueue` checks in
[plan.go](../../tools/tillr-drain/drain/plan.go).

**What two stopped runs settled:** a run of this item on 2026-11-17 built most of it, then stopped
on two readings of the criteria that the tree contradicts, and on three review findings. It was
right to stop. The rulings below answer each one, and c1, c3 and c4 are reworded to match. That
run's uncommitted work is kept at the ref `refs/tillr/preserved/run-item-def-planner-lifecycle-a2`
as recovery evidence; a new run reads it and does not land it as it stands.

- **"Ready" governs an item's own work, not the answer to a conflict.** Today a conflict's answer
  run (the design seat's amendment, or a conflict fix) is dispatched while the item sits in
  `blocked` (`decide` and `conflictDecision` in plan.go). That stays. c1 now says so: the item's own
  work is dispatched only from the definition's ready state, and a conflict's answer run is
  dispatched from the definition's blocked state. Both names come from the definition (`ready`,
  `blocked`), so no `StateInQueue` or `StateBlocked` comparison is left in the planner's decisions.
  Under a definition that does not complete on `landing`, the conflict holds are not raised (they
  are in the landing-only table), and an item in the blocked state is held `blocked`. Routing a
  conflict in such a store is not this item's work.
- **One finished-state rule, and one test changes its setup.** The package map `terminalStates`
  is deleted from the drain. Every reader of it, in the planner and outside it (`preserved.go`,
  `retries.go`, `trunk.go`, `terminal_item.go`, `why.go`), asks the store's definition instead, so
  the drain has one answer. `rg -n 'terminalStates\b' tools/tillr-drain/ -g '!*_test.go'` prints
  nothing when this is done. `TestWhyChangesWithPlannerHoldRule` in
  [why_test.go](../../tools/tillr-drain/drain/why_test.go) proves that `why` and the planner share
  one rule by adding `custom-done` to that map. It keeps its name and its two assertions (`blocked`
  first, `ready` after). Only its middle step changes: it records in the fixture store a definition
  whose finished list includes `custom-done`, in place of writing to the map. This is the one
  existing drain test whose body may change, and c4 names it.
- **The import guard gains two consumers.** `TestNameOnlyInOneConstantAndOnlyLifecycleConsumers` in
  [definition_test.go](../../tools/tillr/definition/definition_test.go) allows only the store and
  item utilities to import the definition package. This item adds the drain and the view to that
  allowed list, and nothing else in that test changes. Its count of the Git-landing name stays at
  one: the drain, the view and their tests print and compare the name the store's record carries
  (or the `GitLandingName` constant), and never spell it.
- **Every hold reason is a named constant.** c3's test can only cover reasons it can find. Two are
  not `Reason…` constants today: the literal `"Vantage not answering"` in
  [pass.go](../../tools/tillr-drain/drain/pass.go), and `node-output-missing`, which is a refusal
  kind that `plan.go` also returns as a hold reason. Both get a `Reason…` constant with the same
  text, and both are general holds. A reason built from a constant and a detail, such as
  `workflow-invalid: …` or `trunk-path-dirty: <paths>`, is classed by its constant prefix, and
  `workflow-invalid` gains the constant it lacks. A reason on a dispatch, such as `resume-failed`,
  is not a hold and is not in either list. The test parses the drain's non-test source, collects
  every `Reason…` constant, and fails on a bare string literal assigned as a hold's reason. No
  printed text changes.
- **A prerequisite the definition names is checked exactly as today.** That includes what today's
  check does when it has nothing to read: with no document root, `documentPrerequisites` holds
  nothing, and under Git landing that must stay so for c4. This item does not tighten it. A
  prerequisite the definition does not name is not read at all, so it can neither hold nor fail.
- **The definition is not recorded on a dispatch.** The pass reads the definition once and judges
  every item, including an attempt already in flight, against that one reading. An in-flight
  attempt is re-judged each pass, so a definition replaced mid-flight takes effect at the next
  pass. Adding the definition's hash to a dispatch's recorded inputs would change stored payloads
  under Git landing, against c4 and c6, so it is left out.

**What the next stopped run settled:** a later run on 2026-11-17 started before the rulings above
were on the trunk. It built most of the item and stopped, reporting that c1 could not be met inside
its paths: the planner reads the definition, then asks the item utility's sync which items'
documents are unshared, and sync reads the definition again for itself. The run concluded that it
needed to change sync, in `tools/tillr-item/`. It does not, and this item's paths stay as they are.
The rulings below say how the criteria read, and c4 is reworded to match. That run's uncommitted
work is kept at the ref `refs/tillr/preserved/run-item-def-planner-lifecycle-a4`. It was built on
an older base and against none of the rulings above, so a new run reads it for its approach and
does not replay it.

- **What "once per pass" in c1 means.** The planner's own decisions in a pass (ready, finished,
  which prerequisites apply, whether the definition completes on landing) all come from one
  definition value, read at the top of `Plan` and passed down. No drain code reads it a second time
  in the same pass. A read made by another package for its own use, through the reader the planner
  hands it, is not a second planner read. It cannot disagree with the first either: `Plan` takes a
  `store.Reader`, which is one read transaction, and `definition.ForReader` reads inside it, so
  both reads return the same declaration. Sync keeps reading its own definition, `SyncInput` gains
  no field, and the drain does not copy sync's logic or cache anything.
- **The document check is itself a prerequisite.** The sync dry run in `documentPrerequisites`, and
  the `triage-no-block` dispatch and the `design-doc-missing` hold it feeds, run only when the
  definition names `design-document`. Under a definition that does not name it, sync is not called in a pass at all.
- **Each prerequisite name gates its own holds, including the checks that come before them.**
  `fence` gates `no-fence`. `design-document` gates the document holds above. `design-questions`
  gates every open-question hold, and also the `Vantage not answering` hold that
  [pass.go](../../tools/tillr-drain/drain/pass.go) raises when the comment source cannot be
  reached: a definition that does not name `design-questions` never asks that source anything, so
  it never raises that hold. `owner-clean` gates `trunk-path-dirty`. Being gated by a prerequisite
  does not make a hold landing-only: `Vantage not answering` stays in the general list.
- **The new constants are plain strings, and each has a waiting rule.** The ruling above that every
  hold reason is a named constant stands, and it adds exactly three: one each for
  `Vantage not answering`, `node-output-missing` and `workflow-invalid`. The stopped run wrote one
  of its constants as an alias of the refusal kind, and gave neither a waiting rule, so
  `TestWaitingEveryDrainReasonHasARule` in
  [waiting_internal_test.go](../../tools/tillr-view/waiting_internal_test.go) failed twice. That
  test requires every exported `Reason…` constant in the drain to be a string literal and to have
  an entry in `waitingRules` in [waiting.go](../../tools/tillr-view/waiting.go). So each new
  constant is declared with its text as a literal, the `node-output-missing` one repeats the text
  of `RefusalNodeOutput` and does not refer to it, and `waitingRules` gains the entries that are
  missing (`workflow-invalid` has one already). That test is not edited. No constant is added
  beyond those three.
- **No check that the definition changed between planning and writing.** The stopped run added one
  when a dispatch is applied. No criterion asks for it, replacing a live store's definition is not
  something this plan has built yet, and the ruling above already says a replaced definition takes
  effect at the next pass. Leave it out.
- **What "as before" in c4 covers.** It covers what a pass decides: the rows, actions and reasons.
  c5 adds a header line to `tillr drain why` and to the waiting face, so a test that asserts the
  exact text or the line count of those two outputs is updated for that one line and nothing else.
  The stopped run met two: `TestWhyEmptyStoreAndMissingStore` in
  [why_cli_test.go](../../tools/tillr-drain/drain/why_cli_test.go) and `TestWaitingCLIAndAPI` in
  [waiting_internal_test.go](../../tools/tillr-view/waiting_internal_test.go). Do not hide the
  header to keep an old assertion. The before-and-after test in c4 compares the rows, not the
  header.



### Add the hook program contract and check it at loop start

The drain hands its judgments to programs: today six step programs named on its command line. A
second use needs a few more, such as one that decides when an item is finished, and it needs one
place to name them. This item makes the definition name one hook program and the hooks it answers,
writes down what the core passes to each hook and what each exit code means, and has the drain
check at start that the program answers what will be asked of it. A mismatch then stops the loop
at start with a message, instead of surfacing as a failed run later. The step flags on the drain's
command line still win, so the keeper's command keeps working.

**Product QA can try:** writing their own hook program against the contract and seeing the loop
refuse it at start, by name, when it is missing a hook.

**Agent details:** this extends the step-program check landed in `1076e92aa`
([step_check.go](../../tools/tillr-drain/drain/step_check.go)) rather than adding a second one.
The six steps and their flags are `ProcessSteps` in [steps.go](../../tools/tillr-drain/drain/steps.go)
and the flags in [main.go](../../tools/tillr-drain/main.go).



### Move tillr-loop into being the Git-landing definition's hook program

[tillr-loop](../../tools/tillr-loop/tillr-loop) already supplies the gate, land and verdict steps
for Git landing, in one file. Once the hook contract exists, it is the natural hook program for the
Git-landing definition, and the drain can find the six steps from the definition instead of from a
long command line. That makes the Git-landing definition a file plus a program, which is the shape
a second use copies.

**Product QA can try:** reading one real hook program, tillr-loop, as the model for their own.

**What the stopped run settled:** a run on 2026-11-17 stopped before editing and reported that the
hook contract was not in its tree. It was right to stop. That run was staged one commit before the
contract landed. The contract is on the trunk now, in the commit titled
`feat(tillr): negotiate definition hooks at loop startup`, and nothing in this item changes
because of that report. Reading the landed contract against this item did find one criterion that
cannot be met inside this item's paths. c2 is reworded for it, and the rulings below say how the
rest read.

- **Check the base first.** From the worktree's root, run:

  ```sh
  rg -n '^## Hook program contract' tools/tillr/definition/README.md
  ```

  One match means the contract is present. No match means the base is older than the contract:
  report blocked and name the base. Do not write the contract here, and do not change the drain.
- **The contract is settled, and the drain's side of it is built.** It is the "Hook program
  contract" section of the definition package's [README](../../tools/tillr/definition/README.md).
  The flags the drain requires of each hook are `hookFlags` in `tools/tillr-drain/drain/hooks.go`. This item writes the program that answers the contract. It
  changes no drain file, and it does not change the contract's text.
- **The embedded definition keeps `hooks` unset in this item (c2, reworded).** The drain runs
  `describe` at start whenever the store's definition names a hook program, including when every
  step flag is given, and it runs it at the loop's root. Many of the drain's loop tests start a
  loop at a fixture root that has no `tools/tillr-loop/tillr-loop`. Measured on 2026-11-17: with
  the trunk's `tools/` tree copied out at the contract's commit and `hooks` in
  [landing.json](../../tools/tillr/definition/landing.json) set to
  `{"argv": ["python3", "tools/tillr-loop/tillr-loop"], "names": [the six steps]}`, `go test .` in
  `tools/tillr-drain` failed 19 tests, and its log printed
  `configuration fault: hook describe [python3 tools/tillr-loop/tillr-loop] failed` twelve times.
  With the file as the trunk has it, the same command in the same copy failed 4 of those 19, which
  are therefore the copy's and not the change's. So naming the program in the embedded definition breaks 15
  drain tests, c5 asks for those suites to pass, and the drain is outside this item's paths. This
  item therefore ships the program and proves it against the contract, and the embedded definition
  is not edited. Naming `tillr-loop` there is left to an item whose paths include the drain,
  because the drain's fixture roots need a hook program first, or its start check needs to change.
  That item is not written yet, and filing it is the owner's call.
- **The program's argument vector.** Wherever this item writes the definition's `hooks` value (the
  c2 test and the README), `argv` is `["python3", "tools/tillr-loop/tillr-loop"]` and `names` is
  `gate`, `land`, `verdict`, `replay`, `rolling`, `fixer-gate`, in that order. The path is relative
  for the reason `DefaultFixerGateProgram` gives: a gate runs in the candidate's worktree.
- **`fixer-gate` becomes a face of its own (c1).** In the flag form the fixer gate is the `gate`
  face given `--all` or `--since`. A hook is called as `<argv> <name> <flags>`, so `tillr-loop`
  gains a `fixer-gate` face that does what `gate` does with one of those two flags. `gate` keeps
  accepting them, which is what keeps the keeper's command working for c4.
- **`replay` and `rolling` take the contract's flags, not the default programs' (c1).** The drain
  calls `replay --only <ids>` and `rolling --all` or `rolling --since <sha>`. The `replay` face
  runs the gate program's replay of those ids and the `rolling` face runs its rolling tier with
  the range flag it was given. Each adds the replay or rolling-tier flag itself, passes its
  environment through unchanged, and exits with the program's exit code.
- **What `describe` prints.** One JSON object whose only key is `hooks`; the drain refuses any
  other key. For each of the six names it lists every flag the contract lists, including the three
  optional `land` flags and both range flags. `describe` reads no store and needs no other flag.
- **Where c3's test lives and what it compares.** It is in
  [test.py](../../tools/tillr-loop/test.py), beside the test that already replays
  `drain-argv.json`. For each case the hook name is the case's face, except that the two cases
  named `fixer-gate-…` use `fixer-gate`. The test gives the case's arguments to that hook face and
  asserts it parses them to the same values as the flag form. The file has no `replay` or
  `rolling` case, and none is added: the drain's own test writes that file, so this item does not
  edit it. A second test covers those two faces with a stand-in gate program that records
  its arguments.
- **If a drain suite fails for c5, stop.** Run the drain's suites as c5 says. With the embedded
  definition unchanged they should pass as they do on the base. A failure there is outside this
  item's paths: report it with the test's name, and do not edit the drain.

**Agent details:** the replay and rolling programs the drain defaults to today are
`DefaultReplayProgram` and `DefaultRollingProgram` in the drain; the fixer gate is
`DefaultFixerGateProgram` in [trunk.go](../../tools/tillr-drain/drain/trunk.go). The flag-form cases
are [drain-argv.json](../../tools/tillr-loop/drain-argv.json).



### Add the item and node to every run's environment

A program a run starts should not have to guess which item it is working on. Today `tillr run exec`
exports the run and the lane, and Product QA's adapter parses the item out of the run id, which is
internal and may change. Product QA asked for this (R2-5). The same face also overwrites a
scratch-directory variable from this repository's jail with `<lane dir>/durable/<run id>`,
undocumented; Product QA's tools read that variable for their own purposes and had to reset it. A
core should not impose one host's convention on every consumer.

**Product QA can try:** reading the item id from the environment in their adapter, and keeping their
own scratch variable.

**Agent details:** the environment list and the `Request` type are both in
[exec.go](../../tools/tillr-run/run/exec.go), and `DurableDir` there names the directory. The
request already carries `item_id`; [request.go](../../tools/tillr-run/run/request.go) builds one
from the face's flags.

Two candidates met the criteria as first written and were refused at the gate on 2026-11-17. This
answers the conflict raised then; the design is unchanged, and only the form of the README's list
in c4 is now stated.

- The second candidate failed the gate's `scratch` check, which printed the README line that named
  the scratch-directory variable in a table row, and the same line in the pack's copy. That check
  (`scratch_citations` in [tree_common.py](../../tools/gate/checks/tree_common.py)) refuses any
  tracked Markdown line that names the variable, wherever it appears outside a fenced block, and
  its list of exceptions is a gate file outside this item's paths. A fenced block is skipped
  whole, as a specimen. So c4's list is one fenced block, and the README's prose around it says
  "the scratch-directory variable", as this block does. No gate file changes and no exception is
  added. The same holds for every other Markdown file the change touches.
- The first candidate failed `pack-sync` on an interpreter cache file in the pack's copy of the run
  utility. Commit `864c56815` made that check ignore such files on both sides, so nothing in this
  item answers it.

Before committing, run the check that refused the second candidate, from the worktree's root:

```sh
python3 tools/gate/checks/scratch.py
```



### Add every run, verdict, decision and state move to the item page

The owner audits a finding by opening its page, and today the page shows little of what happened to
it. The served item answer builds its attempts only from landing candidates, so an item whose runs
made no candidate shows none. Its `decisions` are the drain's dispatch decisions, not the owner's
decision ledger, and it carries no state moves, close or reopen (`itemPayload` in
[serve.go](../../tools/tillr-view/serve.go)). Product QA asked for this (R2-6), and Git landing's
own items need the same history.

**Product QA can try:** opening a finding's page and reading its runs, verdicts, decisions and moves.

**Agent details:** the
[item-page node graph](../reference/tillr-item-graphs.md#observatory) adds
nodes on top of this; it draws an item with no workflow as one node from its run, which this
item's list of runs feeds.



### Route the loop's start around Git when the definition does not land

A store of findings makes no commits, and today the loop refuses to start on its root. On
2026-11-17, a loop started on a root outside any repository exited 4 at `git rev-parse main`, and
one started inside a repository but not at its top level exited 2, because the start runs `tillr
item sync`, which refuses such a root. This item lets a store whose definition does not land start
its loop with no Git at all. Under Git landing nothing changes.

**Product QA can try:** starting tillr's own loop on their findings store's directory.

**Agent details:** the Git steps at start are the trunk reads in
[loop.go](../../tools/tillr-drain/drain/loop.go) and [trunk.go](../../tools/tillr-drain/drain/trunk.go),
trunk history in [trunk_history.go](../../tools/tillr-drain/drain/trunk_history.go), the rolling
tier, the startup sync, and the roll and push settings read by
[settings.go](../../tools/tillr/settings/settings.go).



### Add plain-directory staging for a definition that does not land

A finding needs a directory to work in, not a Git worktree with a base commit and a fence. Today
staging always builds a worktree and refuses a request whose working directory is anything else
(the `working_dir` check in `Stage`, [stage.go](../../tools/tillr-drain/drain/stage.go)), and the
brief an agent receives has nine sections shaped for Git, filled with a base sha, a fence and a target
(`briefValuesFor` in [stage.go](../../tools/tillr-drain/drain/stage.go)). This item stages a plain
lane directory for a store that does not land, and renders the brief from the definition's own
template.

**Product QA can try:** an agent step on a finding, briefed from their own template, in a plain
directory.

**Agent details:** staging is [stage.go](../../tools/tillr-drain/drain/stage.go); the brief
template is the drain's `--brief-template` flag today, whose default is
[brief-template.md](../../tools/tillr-run/backends/brief-template.md). The plain directory is
named relative to the lane directory, like every working directory a request carries.



### Add completion through the definition's hook for items that never land

A finding is finished when it has been judged, not when a commit lands. Today a run's end leads to
the candidate rule, the gate and the lander, and an item can only finish `landed`. This item lets a
definition finish its items through the `complete-item` hook: once a run ends, the hook records the
verdicts and names the finished state, and the drain moves the item there with the reason, pointer
and actor. The drain refuses that move unless every criterion has a recorded verdict, so an item
never finishes unjudged.

**Start here:** a run of this item runs the two commands below first, in the worktree, before it
reads anything else. They carry the earlier draft onto the tree as it stands. Do not test the draft
with `git apply --check`: that command refuses the draft at `pass.go`, and its refusal is the
expected result of the wrong command, not a blocker and not a reason to report `blocked`.

```bash
git diff a376bfdac refs/tillr/preserved/run-item-def-finish-on-hook-a3 -- \
  tools/tillr-drain tools/tillr-item | git apply --3way -
python3 - <<'RESOLVE'
import re
p = "tools/tillr-drain/drain/pass.go"
s = open(p).read()
s, n = re.subn(r"<<<<<<< ours\n(.*?)[^\n]*\n=======\n(.*?)>>>>>>> theirs\n", r"\1\2", s, flags=re.S)
assert n == 1, n
open(p, "w").write(s)
RESOLVE
```

The first command prints `Applied patch to 'tools/tillr-drain/drain/pass.go' with conflicts.`,
takes the other 16 files cleanly and exits 1. That exit is expected, so the two commands are not
joined with `&&`. The second resolves that one conflict: it keeps the trunk's lines
and replaces only the last of them with the draft's line, the one that begins `if !l.nonLanding &&`.
Measured on 2026-11-18 on the trunk at `c15a5440e`, then discarded: after these two commands
`gofmt -l` names no file, `go build ./...` and `go vet ./...` exit 0 in `tools/tillr-drain` and
`tools/tillr-item`, `go test . ./drain -run 'HookCompl|Hook|NonLanding'` passes in
`tools/tillr-drain`, and `go test ./... -run TestStateRequiresVerdictsForEveryStoredCriterion`
passes in `tools/tillr-item`. The run then does what is left: it removes the 10-second deadline
from `TestFailedDesignerResumeStartsNewSessionOnce`, runs the full item and drain suites, runs the
high-effort review, runs `just pack-sync`, and commits once. The rest of this block is the reasoning
behind those steps and the rulings a run needs when one of them fails.

**Product QA can try:** a finding that runs, is judged, and ends `confirmed` or `rejected` on its own,
with the reason on the record.

**Agent details:** the end-of-run path is in [loop.go](../../tools/tillr-drain/drain/loop.go); the
move goes through the existing `ItemState` step in [steps.go](../../tools/tillr-drain/drain/steps.go)
with the flags the receipts item added.

A first run of this item on 2026-11-17 built the hook path, passed its twelve targeted cases, and
stopped before committing. Its review found two defects, and the fix for the first was in a file
outside the item's paths. The run was right to stop. This answers the conflict it raised. c1 to c7
stand as written and one criterion, c8, is appended: a presented criterion's text does not move,
and a block holds at most eight criteria, so c8 carries both fixes and a test for each.

- **The verdict check belongs to the move, not to the drain.** The first run had the drain read the
  item's verdicts and then call the state step. `tillr item apply` can append a criterion between
  those two steps, and the item would then finish with a criterion nobody judged, which is what c4
  forbids. A check in the drain cannot close that gap, because the drain's read and the item
  utility's write are separate transactions. So the check moves into the state move's own write
  transaction: `tillr item state` gains a flag, `--require-verdicts`, and with it
  `SetStateWithReceipt` in [state.go](../../tools/tillr-item/item/state.go) refuses the move,
  with the named refusal `criteria-unjudged`, unless every criterion the item holds at that moment
  has a recorded verdict. A criterion has a recorded verdict when an `item.verdict` event names
  it with the hash of its stored text; `RecordVerdict` in
  [verdict.go](../../tools/tillr-item/item/verdict.go) is the one writer of those. The drain
  passes the flag on every move a `complete-item` answer causes and turns the refusal into c4's
  hold. It keeps no second check of its own, so there is one rule in one place. That is why the
  marker below now names `tools/tillr-item/`; the item's id and blocker are unchanged.
- **The flag is opt-in.** Without `--require-verdicts` a move behaves exactly as today, so the
  loop's move to `landed` and the owner's hand moves are untouched and the existing item suites
  pass unchanged. The drain's start check asks the item program for the flag, as it does for
  every step flag it will pass. Making the rule part of a definition, so that no move into a
  finished state can skip it, is a later decision and not this item's.
- **Restart after the move never enters Git.** The first run's recovery sent a hook-completed run
  into the Git cleanup that preserves and resets a worktree, and a plain directory has neither.
  Under a definition that completes on `hook`, recovery of an ended run takes no Git step at all.
  It reads the item: if the item is already in a finished state, the move happened, so the drain
  writes the end-of-run records that are missing and does not call the hook again. Otherwise it
  runs the completion step again from the hook call. Calling the hook twice is safe on the item
  utility's side: a verdict recorded twice is two events for the same answer, and a move to the
  state an item already has appends nothing.

A second run on 2026-11-17 built c1 to c8 against this text and also stopped before committing.
Its work is kept at the ref `refs/tillr/preserved/run-item-def-finish-on-hook-a3` [unlanded]: 34
files, 17 of them source and 17 their pack copies. It stopped because one test that hook
completion does not reach, `TestFailedDesignerResumeStartsNewSessionOnce` in
[designer_choice_test.go](../../tools/tillr-drain/drain/designer_choice_test.go), failed once in
the full sharded suite with `context deadline exceeded`. A failed suite cannot be waived by a
passing rerun, so that run was right not to commit. This answers the conflict it raised. No
criterion changes; the rulings below say what a run of this item does about that test and about
the fixtures it had to edit.

- **The failure is the test's own 10-second deadline, not the change.** The test puts one
  `context.WithTimeout` of 10 seconds around two whole loop runs and every store read after them.
  The failure was at the last store read, after every earlier assertion had passed, so the clock ran
  out and nothing was wrong with the result. Measured on 2026-11-17 on the second run's tree:
  `go test ./drain -run '^TestFailedDesignerResumeStartsNewSessionOnce$' -count=20` passed all 20
  in 75.5 seconds, about 3.8 seconds each, on a 32-core machine at a load average of 37. The
  recorded duration for that test is 2.03 seconds. It fails when six shards and a review share the
  machine and one run takes more than 10 seconds.
- **This item repairs that test, because its file is under this item's paths.** The repair removes
  the deadline: the test's context is cancelled when the test ends and carries no timeout, so a hang
  is ended by the test binary's own limit and not by a clock that depends on load. That is the
  repair the sharded-runner report records for the other
  nested test deadlines. The test's assertions do not change. Raising 10 to a larger number is not
  the repair, since any fixed number fails at some load.
- **The same rule covers any other test the full suite fails.** If the failing test's file is under
  this item's paths and the run can name the cause, the run repairs it in the same commit and
  reports the failure, the cause and the passing result. A rerun that passes with nothing changed
  repairs nothing and does not count. If the cause is outside this item's paths, or the run cannot
  name it, the run stops and reports as before.
- **Three test edits are expected, and c7 allows them.** c7 is about what the Git-landing
  definition does when a run ends: its assertions stay as they are. It does not freeze fixtures
  that the new start check reaches. The second run found that `TestNonLandingLoopStartsWithoutGit`
  and `TestNonLandingLoopRefusesLandingSettings` used an item stub that failed on any call, or an
  installed item program without `--require-verdicts`. Their fixtures may answer the start check's
  help call, and must still fail if document sync or Git runs. The third edit is the deadline
  above.
- **The Git-only checks when a run is claimed are skipped under `hook`.** The second run's failing
  `git` on `PATH` showed two Git checks in dispatch claiming, before any run starts. Under a
  definition that completes on `hook` they do not run. Under the Git-landing definition they run
  as today. This is what c5 already requires, stated so the next run does not rediscover it.

Neither run's work is on the trunk. A run of this item starts from the tree as it stands and reads
the preserved ref as a draft: it was built on an older base, its high-effort review was not
finished, and its item suite was never run in full. The run finishes all three before it commits.

A third run on 2026-11-17 changed no file and reported `failed`: it read the preserved ref's file
list and stopped without building anything. The loop then took the lane's head, `e597f5bc8`, as this
item's change, and on 2026-11-18 asked a conflict-fix run to re-apply that commit on the moved
trunk. That run stopped, and it was right to. This answers the conflict it raised. No criterion
changes.

- **`e597f5bc8` is not this item's change, and nothing is re-applied.** It is a roadmap commit from
  other work: it changes only [roadmap.md](../tillr/roadmap.md), and the trunk already holds it
  (`git merge-base --is-ancestor e597f5bc8 HEAD` exits 0, run on 2026-11-18). No run of this item
  has made a commit, so this item has no candidate to carry forward. The loop's commit that retired
  this block on top of it, `1fcb00b0e` [unlanded], is not on the trunk either, and this block and
  its eight criteria stand.
- **The next run is a build run from the tree as it stands.** It is not a conflict-fix run and it
  is given no commit to re-apply. It builds c1 to c8 and commits once.
- **The draft is carried as a patch of its 17 source files, by a three-way apply.** The preserved
  ref sits on `a376bfdac`, and the trunk has changed 45 files under `tools/tillr-drain/` and
  `tools/tillr-item/` since. So the draft is this diff and nothing wider:
  `git diff a376bfdac refs/tillr/preserved/run-item-def-finish-on-hook-a3 -- tools/tillr-drain
  tools/tillr-item`, which is 17 files, 604 lines added and 23 removed. Measured on 2026-11-18 on
  the trunk at `3da00ddcf`: `git apply --3way` takes 16 of the 17 files cleanly and leaves
  conflicts in one, [pass.go](../../tools/tillr-drain/drain/pass.go), which the run resolves by
  hand, keeping what the trunk added there. The run does not check out or cherry-pick the ref, and
  does not compare the ref with the trunk's head: that comparison shows 225 files, most of them the
  trunk's later work undone. It does not copy the 17 pack files. `just pack-sync` writes them.
- **Reading the draft is not the work.** A run of this item ends in one of two ways: a commit that
  meets c1 to c8, with the full item and drain suites run, or a report that names the one thing
  that stopped it. A report with no change and no named blocker, like the third run's, gives the
  next run nothing to act on.
- **Why the loop took another work's commit as this item's change is not this item's to repair.**
  The third run reported no paths changed, and the conflict-fix brief still named a head for its
  change. That is a fault in how the loop picks a candidate after a run that changed nothing. It is
  recorded here so the owner can file it; a run of this item does not look for it or fix it.

A build run on 2026-11-18 changed no file and reported `blocked`. It tested the draft with a
different command from the one above, `git show --format= <ref> -- tools | git apply --check -`,
which is a plain apply with no three-way merge. That command refused the patch at
[pass.go](../../tools/tillr-drain/drain/pass.go), and the run stopped there and asked for the patch
to be reconciled. This answers the conflict it raised. No criterion changes, and the rulings above
stand: the conflict in that one file was already measured and already assigned to the run.

- **A refused patch in `pass.go` is the expected result, and it is not a stop.** Reconciling the
  draft with the trunk is this item's work, and no other run does it first. A run stops on the
  draft only when it cannot say which side of a conflict is right, and then it reports the lines in
  question.
- **The command is the three-way one, run as written.** Measured again on 2026-11-18 on the trunk
  at `9eac60321`:
  `git diff a376bfdac refs/tillr/preserved/run-item-def-finish-on-hook-a3 -- tools/tillr-drain
  tools/tillr-item | git apply --3way -` takes 16 of the 17 files and leaves one conflict, in one
  place, in `pass.go`. `git apply --check` without `--3way` refuses the whole patch on that same
  place and says nothing about the other 16 files, so it is not a test of whether the draft can be
  used.
- **The one conflict has a known resolution.** It is in `applyPlanGeneration`, at the check that
  runs when the decision is a dispatch or a retry. Since the draft was written the trunk added a
  block before that check, which reads the backends that quota has shut and returns without
  claiming a run when the decision's backend is one of them. The draft's only change at that place
  is to put `!l.nonLanding &&` in front of the check that follows, the one that defers a dispatch
  when a landing has moved the trunk, so that it does not run under a definition that completes on
  `hook`. The resolution keeps the trunk's quota block exactly as it is, with no `nonLanding`
  condition on it, and adds `!l.nonLanding &&` to the condition of the check after it. Nothing else
  in the file needs a decision.
- **With that resolution the draft builds and its own tests pass.** Measured on 2026-11-18 on
  `9eac60321`, then discarded: `go build ./...` and `go vet ./...` exit 0 in both modules;
  `go test . ./drain -run 'HookCompl|Hook|NonLanding'` passes in `tools/tillr-drain`; and
  `go test ./... -run TestStateRequiresVerdictsForEveryStoredCriterion` passes in
  `tools/tillr-item`. That is the starting point and not the finish. What is left is what the
  second run's answer lists: the test deadline repair, the full item and drain suites, the
  high-effort review, `just pack-sync`, and one commit.

A second build run on 2026-11-18 also changed no file and reported `blocked`, for the same reason
in nearly the same words: it piped the draft to `git apply --check`, was refused at
[pass.go](../../tools/tillr-drain/drain/pass.go), and asked for the patch to be reconciled. The
answer above already ruled on that, so the ruling is not what was missing. What was missing is a
form a run can follow without reading this history first. This answers the conflict it raised. No
criterion changes and no ruling above changes. The steps below replace the prose above as the
place a run starts, and they were run again, in this order, on 2026-11-18 on the trunk at
`d9cb0ca2e`.

1. Apply the draft with the three-way command. A plain `git apply`, with or without `--check`, is
   the wrong command, and its refusal is not a result to report.

   ```bash
   git diff a376bfdac refs/tillr/preserved/run-item-def-finish-on-hook-a3 -- \
     tools/tillr-drain tools/tillr-item | git apply --3way -
   ```

   It prints `Applied patch to 'tools/tillr-drain/drain/pass.go' with conflicts.` and applies the
   other 16 files cleanly. That one line is the expected output. The run goes on to step 2.
2. Resolve the one conflict in `pass.go`, in `applyPlanGeneration`. Keep every line of the side
   Git marks `ours`, which is the trunk's quota block, and change only the condition on that
   side's last line, so that the section reads as follows with no conflict markers left:

   ```go
   		if (d.Action == ActionDispatch || d.Action == ActionRetry) && d.RunRequest != nil {
   			shut, err := readQuotaShutBackends(tx.Reader(), l.cfg.Now(), l.currentPolicy())
   			if err != nil {
   				return err
   			}
   			open, err := dispatchQuotaOpen(tx.Reader(), d, shut, l.currentPolicy())
   			if err != nil {
   				return err
   			}
   			if !open {
   				return nil
   			}
   		}
   		if !l.nonLanding && (d.Action == ActionDispatch || d.Action == ActionRetry) {
   ```

   If the trunk has changed the `ours` side again by the time a run reads this, the rule is the
   same: the trunk's lines stay, and the only line the draft contributes is the last one.
3. Check the starting point. With step 2 done, measured on `d9cb0ca2e` and then discarded:
   `go build ./...` and `go vet ./...` exit 0 in `tools/tillr-drain` and in `tools/tillr-item`;
   `go test . ./drain -run 'HookCompl|Hook|NonLanding'` passes in `tools/tillr-drain`; and
   `go test ./... -run TestStateRequiresVerdictsForEveryStoredCriterion` passes in
   `tools/tillr-item`.
4. Finish the work the second run's answer lists: remove the 10-second deadline from
   `TestFailedDesignerResumeStartsNewSessionOnce`, run the full item and drain suites, run the
   high-effort review, run `just pack-sync`, and commit once.

- **A conflict in a draft is work, never a blocker.** A run of this item may report `blocked` only
  for something outside its paths, or for a conflict whose right side it cannot name, and then it
  quotes the lines. "The patch does not apply" names neither, and a third report of it would give
  the next answer nothing new to rule on.
- **If the steps above fail as written, the run builds without the draft.** Should the ref be
  gone, or the three-way apply leave conflicts in more than `pass.go`, the run does not stop. It
  builds c1 to c8 from the tree as it stands, using this block's rulings as the design. The draft
  saves work; it is not a requirement of the item.

A third build run on 2026-11-18 changed no file and reported `blocked` in eight seconds, again on
`git apply --check` at [pass.go](../../tools/tillr-drain/drain/pass.go). Its log shows why the two
answers above did not reach it. It read the first 22 lines under this block's heading, searched the
document for the words "Start here" and found nothing, and every command it ran carried a
10-second limit. The numbered steps were more than 150 lines below the heading, so it never saw
them. This answers the conflict it raised. No criterion and no ruling changes.

- **The steps now sit at the top of the block, under "Start here".** They are the same steps as
  the numbered list above, and the conflict in `pass.go` is now resolved by a command and not by
  hand, so a run has nothing to decide there. Where the two differ, the text under "Start here"
  is the one a run follows. It was run again on 2026-11-18 on the trunk at `c15a5440e`, with the
  results it states.
- **A run reads the whole block before it reports anything but `done`.** The block runs from its
  heading to the criteria. A report of `blocked` or `failed` names the ruling in this block that
  does not cover its case. A report that does not is answered by this paragraph and nothing more.
- **The suites and the build do not fit in ten seconds.** The drain's targeted tests alone took 58
  seconds in the measurement above. A run gives its build and test commands no time limit, or one
  of at least ten minutes.
- **A fourth report of the same refusal goes to the owner, not to another answer.** The design has
  nothing more to say about this patch. If a run given this text still stops at `git apply
  --check`, the fault is in how the run is staged or which seat it is given, and the owner rules
  on that.



### Route a command node into the lane directory and name its node

A findings workflow runs a command step, such as re-running the fuzzer's reproduction, and then an
agent step that investigates. [Command nodes](../reference/tillr-item-graphs.md#shipped-workflows-and-command-registry) run a registered program
in the item's worktree, which is right for Git landing and does
not exist in a store that does not land. This item runs a command node in the plain lane directory
for such a store, and gives every node's program its node id. It is a new item rather than an edit
to that block, because that block is ready and may be dispatched at any time.

**Product QA can try:** a two-step workflow, a command and then an agent, on one finding.

**Agent details:** the run request's `node_id` field comes from the run-environment item; the drain
sets it here for every workflow node.



### Add a worked definition that lands nothing, runnable end to end

Product QA should be able to run the new tillr, not read about it. This item ships a second, minimal
definition that lands nothing: findings are filed with `tillr item apply`, run a command step and an
agent step in a plain directory, are judged, and finish in one of five outcome states that can be
counted. It comes with one documented command sequence that works from an empty directory with no
Git repository. It is what Product QA copies for their own store, and it is the acceptance test for
this whole plan: if it runs, the core no longer assumes Git landing.

**Product QA can try:** everything above, by running the README's commands as written.

**Agent details:** the example lives under the core library, so the pack's module mirror carries it
to `~/.local/share/tillr/tillr/examples/recorded-verdict/README.md` in any workspace with the tillr
pack. The test lives in the drain's suite because the drain module already links the item and run
utilities.

The first run built the example, its test and the pack copies, and then stopped without a commit on
two checks that are red on the trunk. Both are answered here.

The first is `python3 tests/tillr-docs.test.py`, which prints `FAIL run README environment list`
because `tools/tillr-run/README.md` does not list `TILLR_READ_ONLY_GROUP`, which
`tools/tillr-run/run/exec.go` gives to every run. The second is
`TestHookCompletionHoldsAndNegativeAnswer/negative` in `tools/tillr-drain`, which expects two
dispatches and two refusals and gets one of each. On 2026-11-18 both failed the same way at
`f213b9481`, with none of this work applied. This work changes none of their inputs. So both belong
to the trunk and neither is a reason to stop. The builder leaves them alone, finishes and commits,
and names both in its report with the commands that show them on the base. No criterion changes:
c1 to c6 were met by the first run's work and stand as written.



- [unlanded] **Start from** the commit at `refs/tillr/preserved/run-item-def-recorded-verdict-example-a1` in the main repository, `c48173c4b`: 32 files, holding the example under `tools/tillr/examples/recorded-verdict/`, `tools/tillr-drain/recorded_verdict_example_test.go`, two added lines in `tools/tillr-drain/internal/testinputs/siblings.txt`, and the pack copies of all three. Do not rebuild it from nothing. Its first parent is `73ed72839` and the trunk has moved, so re-apply its change instead of reusing its tree. On 2026-11-18 `git merge-tree --write-tree f213b9481 c48173c4b` reported no conflict. Its second parent holds what that run had staged, which is empty; ignore it. Do not resolve a pack copy by hand: run `just pack-sync` after re-applying, so the copies come from the sources.
- **What passed with that work on `f213b9481`, 2026-11-18:** `go test . -run '^(TestRecordedVerdictExampleREADME|TestMissingSiblingBuildInputIsRejected)$'` in `tools/tillr-drain`, and `python3 tests/pack-sync.test.py`. The first run's report also says `go test ./...` in `tools/tillr`, `yolo pack lint yolo-packs/tillr` and `uvx vantage-check` on the example's two Markdown files passed. Run them again after re-applying.
- **The red docs check:** its inputs are `tools/tillr-run/README.md` and `tools/tillr-run/run/exec.go`, and both are outside this item's paths. Do not edit either, do not edit the check, and do not stop for it. That script stops at its first failure, so the cases after this one do not run in it. If the trunk has repaired it by the time this is built, the check simply passes.
- **The red hook test:** `go test . -run '^TestHookCompletionHoldsAndNegativeAnswer$'` in `tools/tillr-drain` fails only in its `negative` case, at the assertion `result.Dispatched != 2 || result.Rejected != 2`, with `Dispatched:1 Rejected:1` and the item held `candidate-no-op`. It fails the same way with every `TILLR_*` variable removed from the environment, so it is not caused by running inside a lane. The test file is inside this item's paths, but which count is right is a question about how the loop returns a refused run, and that is not this item's to decide. Do not edit `tools/tillr-drain/hook_completion_test.go` and do not change the loop to make it pass. The example does not depend on it: a finding that ends `rejected` gets there by the hook answering with that state, not by the hook exiting 1.
- **Before committing,** confirm that `git diff --name-only HEAD -- tools/tillr-run tests/tillr-docs.test.py tools/tillr-drain/hook_completion_test.go tools/tillr-drain/drain` prints nothing. Then run `python3 tools/go-test-shards.py tools/tillr-drain -k 6 --timeout 20m`. A failure there other than the hook test's `negative` case is this work's to repair if this work causes it. A failure that lies outside the item's paths and also fails on the base with this work removed is the trunk's: report it and carry on. Stop and report only when the failure is caused by this work and its repair lies outside the item's paths.

---

## 3. The next tranche, in prose

These are filed when the first tranche has landed, because each builds on the definition file, the
hook contract or the worked example, and because the queue already holds about eighty items. Each
names the request it answers.

**What the tranche waits on, as of 2026-11-18.** The first tranche has landed, but the tranche is
still not filed, on purpose. Two things must happen first, and neither has:

- **Product QA's third round.** Their agent runs the worked example and writes round three against
  the real thing. [Their reply](./tillr-product-qa-requests.md) is still the second round.
- **The owner's review of the designer's decisions.** Every unit below rests on choices recorded
  in [the direction document's ledger](./tillr-core-and-git-landing.md#decision-ledger). Where the
  line between core and definition falls, which unit 1 depends on, is the owner's own ruling
  ([`OQ-CG2`](./tillr-core-and-git-landing.md#OQ-CG2)). The shape of the one file and four other
  choices (`D-CG1` to `D-CG5` there) are the designer's, and the owner has not reviewed them. Unit 4
  also waits on the owner confirming the designer's amendment to `D-IG4` in
  [item graphs](../reference/tillr-item-graphs.md#why-its-this-way).

Filing these units now would put work in the queue that rests on decisions the owner has not seen.

1. **Count outcomes and draw the views from the definition** (R2-8). The status face and the
   observatory count items by the definition's finished states, and the landings and trunk pages
   appear only for a definition that lands.
2. **Add a detail panel from the `detail` hook** (R2-7). The item page asks the hook program for a
   record drawn from the item's `body_ref`, so Product QA's finding details appear on tillr's page.
3. **Add graph change as a definition's policy, inside the four core rules** (R2-9). The core keeps
   one writer per change with its event, no node replacing itself, nothing started changing, and the
   downstream-shape check. A definition may allow a running node to insert one node directly
   downstream of itself with the same output shape, and may allow appending nodes to a finished
   graph, which the next scheduling pass starts.
4. **Replace the fixed graph caps with a size shown before dispatch** (R2-10). The concern behind
   `D-IG4` is runaway expansion and the spend that follows it. The guard is the refusal of cycles
   among workflow files, which already makes expansion finite, plus the expanded size shown before
   the first node runs, plus a budget a definition may set that leads to a named next step for the
   owner, never a silent refusal. Since this was drafted, the workflow check and stage expansion
   already report graph size and refuse no graph for its depth or node count
   ([workflow_check.go](../../tools/tillr-drain/workflow/workflow_check.go)), while
   [design-from-scouts@v1.json](../../config/tillr/workflows/design-from-scouts@v1.json) still bounds
   scout prompts at `most: 8` and no definition-supplied size budget is built yet (see
   [the separable-parts table](./tillr-separable-parts.md#7-the-second-round-tillrs-answer)).
5. **Add an uncertain outward effect as a state that blocks a retry until reconciled** (R2-12).
6. **Add time and tokens per step attempt, with their source** (R2-13). A command step's record says
   `none` for tokens rather than zero.
7. **Add an owner acknowledgment point** (R2-14): a recorded "seen" per item, which the morning audit
   advances.
8. **Route the waiting class through one classifier** (a stable-surface correction). For a blocked
   item, `tillr view waiting` printed "waits on work" and the served answer "needs you". Both faces
   now assemble rows through `assembleWaiting` in [waiting.go](../../tools/tillr-view/waiting.go) and
   the reason table in [holdrules.go](../../tools/tillr-drain/holdrules/holdrules.go), which landed
   the hold-classification piece; verifying the blocked-item case across both faces remains unfiled.
9. **Test that a run started from the record sink's own request links to it** (a stable-surface
   correction, read but not re-run). Fix it if the next pass holds it `run-link-missing`.
10. **Add the `after-completion` hook and the retention rule.** Roll and push become the Git-landing
    definition's `after-completion` hook, and `retention` gains its first rule beyond `keep`.

Clocks and the gate's "could not judge" are filed separately, by the owner's direct principle of
2026-11-17, and are not in this plan.

---

## 4. Reconciled with what is queued

When this plan was written on 2026-11-17, five items already in the queue overlapped with it; all
five have since landed:

- **The terminal-state block** (`item-jl-terminal-state-receipts`, queued and blocked on 2026-11-17;
  landed in [terminal_evidence.go](../../tools/tillr-item/item/terminal_evidence.go)): its c2's
  `--resolves-via` and its c3's refusal were built here by the receipts item and the first item, and
  its c1, a landing record before `landed`, landed in its own item.
- **The command-node block** (`item-ig-command-node`, queued and ready on 2026-11-17; landed in
  [command.go](../../tools/tillr-drain/drain/command.go)): unchanged. Its c2 stayed right for Git
  landing; the delta was built as a new item that depended on it.
- **The item-page block** (`item-ig-view-nodes`, queued and blocked on 2026-11-17; landed in
  [item_nodes.go](../../tools/tillr-view/item_nodes.go)): unchanged. The item-page history item fed
  its c3.
- **The exit-condition block** (`item-ig-exit-condition`, queued and blocked on 2026-11-17; landed in
  [workflow_exit.go](../../tools/tillr-drain/drain/workflow_exit.go)): unchanged. Completion through
  the hook is a definition's rule; a workflow's `no_commit` exit still decides when a graph is done.
- **The unshared-item block** (`item-sync-hold-unshared-items`, landed in
  [document_prerequisite.go](../../tools/tillr-drain/drain/document_prerequisite.go)): unchanged.
  Its c7 leaves alone a store no document declares, which covers a store fed by `apply`.
