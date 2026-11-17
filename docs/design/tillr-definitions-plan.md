---
title: "Build plan: a tillr core with Git landing as its first definition"
status: accepted
stage: DECIDED
updated: 2026-11-17
tags:
  - tillr
  - plan
  - store
  - boundaries
  - product-qa
summary: "The build hand-off for the owner's 2026-11-17 go-ahead: eighteen queued items that turn tillr into a core that does not assume Git landing, with Git landing supplied by one definition and a second, runnable definition that lands nothing. Each item says what the Product QA pipeline can try once it lands. The next tranche is written in prose, to be filed when this one has landed."
next: "The drain builds the eighteen items in dependency order. When the worked example lands, Product QA's agent runs it and writes round three against the real thing; then the next tranche is filed."
depends-on:
  - tillr-core-and-git-landing.md
  - tillr-separable-parts.md
  - tillr-product-qa-requests.md
  - tillr-item-graphs.md
  - tillr-judge-and-lifecycle.md
---

# Build plan: a tillr core with Git landing as its first definition

**Status:** 2026-11-17. Written against `4df6e426f`. The owner said to build on 2026-11-17: "file
item(s) to build as much of the new tillr as possible so autoqa can play with the real thing and
comment for real the next round". By "autoqa" he means the Product QA pipeline (see
[the words](#the-words-this-plan-uses)). The eighteen items below are queued. They rest on
**working choices**, each to be confirmed or changed by the owner after the next round, which are
recorded in
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
- **Definition** *(a placeholder, coined in the direction document)*: the layer that turns tillr's
  core into one use. It is a data file plus one hook program. **`git-landing`** is the placeholder
  name of the definition for landing changes in Git, which is everything tillr does today. The owner
  has not chosen either word ([OQ-CG5](./tillr-core-and-git-landing.md#OQ-CG5)). Every item keeps
  the words in as few places as possible, so a rename is mechanical.
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
package yet, so nothing changes. The names `definition` and `git-landing` are placeholders the owner
has not chosen, and this item keeps them in one constant so that a rename is one line.

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

<!-- tillr: item id="item-def-store-definition" paths="tools/tillr-store/,tools/tillr/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** `tillr store init --definition <file>` validates the file with the definition package and records its name, version, content and SHA-256 in the store, with an event. An invalid file is refused with exit 2, and no store is created.
- **c2:** `tillr store definition --store <path>` prints the recorded definition as a record. A store with none recorded answers the embedded Git-landing definition, marked as the default. A test reads a store created before this change.
- **c3:** `tillr store definition --store <path> --set <file>` replaces the definition, with an event naming the old and the new SHA-256. It refuses a definition that drops a state some item in the store holds, and names those items.
- **c4:** The definition package offers one function that answers a store's definition, and a test asserts it returns the same definition the face prints.
- **c5:** No Git, file or process work runs inside a SQLite write transaction.
- **c6:** Every existing test of the store utility passes unchanged, and a store created before this change opens and drains as before.
- **c7:** The store utility's README documents the flag and the face.

### Add a face that lists a store's items

A consumer that wants to see its queue has no command to ask. `tillr item show` needs an item id,
and the only list of items is the waiting face, which lists open items and their holds. Product QA
reported this as a correction to tillr's stable surface, and it was confirmed on 2026-11-17. The
worked example at the end of this plan reads its outcomes through this face.

**Product QA can try:** `tillr item list --state <state>` to count findings by state.

**Agent details:** the face sits beside `show` in [main.go](../../tools/tillr-item/main.go) and
reads with the same query shape `show` uses in [show.go](../../tools/tillr-item/item/show.go).

<!-- tillr: item id="item-def-list-items" paths="tools/tillr-item/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** `tillr item list --store <path>` prints one record listing every item: its id, title, state, blockers and last update, ordered by id. An empty store prints an empty list with exit 0.
- **c2:** `--state <state>`, which may be given more than once, keeps only items in those states. A test builds a store with items in three states and asserts the list with and without the filter.
- **c3:** The item utility's README documents the face.
- **c4:** Every existing test of the item utility passes unchanged.

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

<!-- tillr: item id="item-def-apply-unknown-fields" paths="tools/tillr-item/,yolo-packs/tillr/,docs/" blocked_by="item-def-list-items" prose="2" -->

- **c1:** `tillr item apply` refuses a record whose payload carries a field the item utility does not store, as `unknown-field`, naming the field, and writes nothing. A test applies a record carrying `"kind": "finding"`.
- **c2:** Every kind of record tillr's own programs pass to `apply` is still accepted. A test builds one record from sync, one from the comment intake, one from seed and one in the shape the loop writes, and applies each.
- **c3:** The item utility's README states the producer rule the code enforces: `producer.system` must be `tillr`, and any other value is refused as a record from another system.
- **c4:** Every existing test of the item utility passes unchanged, and a store created before this change opens as before.

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

<!-- tillr: item id="item-def-item-lifecycle" paths="tools/tillr-item/,tools/tillr/,yolo-packs/tillr/,docs/" blocked_by="item-def-store-definition,item-def-apply-unknown-fields" prose="2" -->

- **c1:** `tillr item state`, `apply`, `close` and `reopen` take the states, the finished states, the ready, blocked and closed roles, and the allowed moves from the store's definition through the definition package, and no longer from constants in the item utility.
- **c2:** A move the definition does not list is refused as `transition-not-allowed`, naming the two states and the definition. A move out of a finished state is allowed only where the definition lists it, requires `--reason` and `--by`, and is recorded as an event that carries both.
- **c3:** A test with a fixture definition whose finished states are `confirmed` and `rejected`, and which lists a move from `rejected` back to its ready state, asserts that an item can finish `confirmed`, that leaving `confirmed` is refused, and that leaving `rejected` with a reason and an actor succeeds with both in the event.
- **c4:** Sync's set of finished states is the definition's finished states plus the legacy frozen states.
- **c5:** Under the Git-landing definition every face behaves exactly as before: the existing state, lifecycle, apply and sync tests pass unchanged.
- **c6:** A store created before this change opens and drains as before. A test opens such a store.
- **c7:** The item utility's README says the states come from the store's definition and names the face that prints it.

### Add a reason, pointer and actor to every finished-state move

A finished state is a claim, and a claim needs its evidence on the record. On 2026-11-17 `tillr item
state --state resolved-elsewhere --reason "repeat-of:f-0"` exited 0 and stored no reason, no
pointer and no actor: the flag was accepted and dropped. Product QA needs the reason a finding was
rejected and the finding it repeats. The owner's morning audit needs to see who decided. This
item stores all three on every move into a finished state, and refuses a flag instead of dropping
it.

**Product QA can try:** finishing a finding as a repeat of another, with the pointer to it, and reading
both back from `tillr item show`.

**Agent details:** the pointer flag is `--resolves-via`, the name the queued
[terminal-state block](./tillr-judge-and-lifecycle.md#prevent-landed-and-resolved-states-without-their-evidence)
uses in its c2, so that block's criterion is met by this one rather than contradicted.

<!-- tillr: item id="item-def-finished-state-receipts" paths="tools/tillr-item/,yolo-packs/tillr/,docs/" blocked_by="item-def-item-lifecycle" prose="2" -->

- **c1:** A move into any finished state through `tillr item state` takes `--reason`, `--by` and `--resolves-via`. The pointer is `item:<id>` for an item the store holds, `commit:<sha>` or `doc:<path>`. All three are stored on the item, carried by the update event, and printed by `tillr item show`.
- **c2:** A test moves an item to a finished state with all three and asserts what `show` prints and what the event carries.
- **c3:** A flag the face would not store on a given move is refused as `flag-not-stored`, naming the flag, instead of being accepted and dropped. A test passes `--reason` on a move to the ready state.
- **c4:** An `item:` pointer naming an item the store does not hold is refused as `pointer-unknown`.
- **c5:** The loop's own move to `landed` still succeeds unchanged, and every existing test of the item utility passes.
- **c6:** A store created before this change opens with its finished items readable as they were.
- **c7:** The item utility's README documents the three flags.

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

<!-- tillr: item id="item-def-planner-lifecycle" paths="tools/tillr-drain/,tools/tillr/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="item-def-item-lifecycle" prose="2" -->

- **c1:** The planner reads the store's definition once per pass. It dispatches only items in the definition's ready state, and a dependency is met when the blocker is in one of the definition's finished states.
- **c2:** The planner raises a prerequisite hold, such as `no-fence`, the open-question holds, `design-doc-missing` or `trunk-path-dirty`, only when the definition names that prerequisite. A test with a fixture definition that names none asserts that an item with no fence and no design document is dispatched.
- **c3:** The holds only landing can raise are listed in one table in the drain and raised only when the definition completes on `landing`. A test asserts that every hold reason the drain defines is either in that table or in the drain's list of general holds, so a new hold cannot ship unclassed.
- **c4:** Under the Git-landing definition every pass decides exactly as before. The existing planner and loop suites pass unchanged, and a test runs `tillr drain why` on one fixture store before and after the change and asserts identical rows.
- **c5:** `tillr drain why` and the waiting face print the definition's name in their header.
- **c6:** A store created before this change opens and drains as before.

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

<!-- tillr: item id="item-def-hook-contract" paths="tools/tillr-drain/,tools/tillr/,yolo-packs/tillr/,docs/" blocked_by="item-def-planner-lifecycle" prose="2" -->

- **c1:** The definition package's README states the hook contract: `describe`; the six steps `gate`, `land`, `verdict`, `replay`, `rolling` and `fixer-gate` with the flags the drain passes them today; `complete-item` with `--item`, `--run`, `--store` and `--root`; `after-completion` with `--item`, `--store` and `--root`; and `detail` with `--body-ref` and `--root`. For each it states the record printed and what each exit code means.
- **c2:** When the store's definition names a hook program and a step's flag is not given on the drain's command line, the drain runs that step as `<program> <hook> <flags>`. A step flag on the command line still wins.
- **c3:** At start the drain runs `<program> describe`, which prints the hooks it answers and the flags each accepts. The drain refuses to start with exit 2, naming the hook, when the definition lists a hook the program does not answer or when a hook does not accept a flag the drain will pass.
- **c4:** A hook the definition does not list is never called. A step whose hook is not listed and whose flag is not given behaves as an empty step flag does today.
- **c5:** A test runs a pass with a stub hook program that records its argv, and asserts each hook's flags and the refusal at start.
- **c6:** No Git, file or process work runs inside a SQLite write transaction.
- **c7:** Under the Git-landing definition, which names no hook program yet, the drain runs exactly as before, and the existing loop suites pass unchanged.

### Move tillr-loop into being the Git-landing definition's hook program

[tillr-loop](../../tools/tillr-loop/tillr-loop) already supplies the gate, land and verdict steps
for Git landing, in one file. Once the hook contract exists, it is the natural hook program for the
Git-landing definition, and the drain can find the six steps from the definition instead of from a
long command line. That makes the Git-landing definition a file plus a program, which is the shape
a second use copies.

**Product QA can try:** reading one real hook program, tillr-loop, as the model for their own.

**Agent details:** the replay and rolling programs the drain defaults to today are
`DefaultReplayProgram` and `DefaultRollingProgram` in the drain; the fixer gate is
`DefaultFixerGateProgram` in [trunk.go](../../tools/tillr-drain/drain/trunk.go). The flag-form cases
are [drain-argv.json](../../tools/tillr-loop/drain-argv.json).

<!-- tillr: item id="item-def-tillr-loop-hooks" paths="tools/tillr-loop/,tools/tillr/,yolo-packs/tillr/,docs/" blocked_by="item-def-hook-contract" prose="2" -->

- **c1:** `tillr-loop describe` prints the hooks it answers and their flags, in the contract's shape: `gate`, `land`, `verdict` and `fixer-gate` as today, and `replay` and `rolling` as faces that run the programs the drain defaults to today.
- **c2:** The embedded Git-landing definition names `tillr-loop` as its hook program and lists those six hooks.
- **c3:** A test asserts that, for every case in `drain-argv.json`, the step a drain started with no step flags would run through the hook program receives the same flags as the flag form does today.
- **c4:** The keeper's loop command, with its step flags, keeps working unchanged.
- **c5:** `tools/tillr-loop/test.py` and the drain's loop suites pass.
- **c6:** tillr-loop's README says it is the Git-landing definition's hook program and links the contract.

### Add the item and node to every run's environment

A program a run starts should not have to guess which item it is working on. Today `tillr run exec`
exports the run and the lane, and Product QA's adapter parses the item out of the run id, which is
internal and may change. Product QA asked for this (R2-5). The same face also overwrites a
scratch-directory variable from this repository's jail with `<lane dir>/durable/<run id>`,
undocumented; Product QA's tools read that variable for their own purposes and had to reset it. A
core should not impose one host's convention on every consumer.

**Product QA can try:** reading the item id from the environment in their adapter, and keeping their
own scratch variable.

**Agent details:** the environment list is in [exec.go](../../tools/tillr-run/run/exec.go), and
`DurableDir` names the directory. The request type is in
[request.go](../../tools/tillr-run/run/request.go); it already carries `item_id`.

<!-- tillr: item id="item-def-run-item-env" paths="tools/tillr-run/,yolo-packs/tillr/,docs/" prose="2" -->

- **c1:** `tillr run exec` exports `TILLR_ITEM_ID` from the request beside `TILLR_RUN_ID`, and `TILLR_NODE_ID` when the request carries a new optional `node_id`. A test runs a stub adapter that prints its environment and asserts both.
- **c2:** A request may set a new field, `scratch_env`, to `false`; then `run exec` leaves the scratch-directory variable it sets today as the caller passed it. A request that omits the field gets exactly today's environment, so the drain's runs are unchanged. A test covers both.
- **c3:** The run record names the run's scratch directory whether or not the variable was set.
- **c4:** The run utility's README lists every environment variable `run exec` sets and when.
- **c5:** Every existing test of the run utility passes unchanged.

### Add every run, verdict, decision and state move to the item page

The owner audits a finding by opening its page, and today the page shows little of what happened to
it. The served item answer builds its attempts only from landing candidates, so an item whose runs
made no candidate shows none. Its `decisions` are the drain's dispatch decisions, not the owner's
decision ledger, and it carries no state moves, close or reopen (`itemPayload` in
[serve.go](../../tools/tillr-view/serve.go)). Product QA asked for this (R2-6), and Git landing's
own items need the same history.

**Product QA can try:** opening a finding's page and reading its runs, verdicts, decisions and moves.

**Agent details:** the queued
[item-page block](./tillr-item-graphs.md#add-an-items-nodes-and-their-runs-to-the-item-page) adds
nodes on top of this; its c3 draws an item with no workflow as one node from its run, which this
item's list of runs feeds. That block needs no edit.

<!-- tillr: item id="item-def-item-page-history" paths="tools/tillr-view/,yolo-packs/tillr/,docs/" prose="2" -->

- **c1:** The served item answer lists every run of the item, with its end cause, start, finish, backend and spend, whether or not the run made a landing candidate.
- **c2:** It lists each verdict with its criterion, outcome, reason, evidence and actor, and each entry of the owner's decision ledger that names the item.
- **c3:** It lists each move of the item's state from the event log, with the reason and the actor where the event carries them, including close and reopen.
- **c4:** The keys the served answer has today keep their names and meaning; the new lists are added beside them.
- **c5:** The observatory's item page shows the runs, verdicts, decisions and moves in time order.
- **c6:** A test reads the served answer for an item with two runs that made no candidate, a verdict, a decision, a close and a reopen, and asserts every entry.

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

<!-- tillr: item id="item-def-loop-start-without-git" paths="tools/tillr-drain/,tools/tillr/settings/,yolo-packs/tillr/,docs/" blocked_by="item-def-hook-contract" prose="2" -->

- **c1:** When the store's definition does not complete on `landing`, the loop's start runs no Git command, no `tillr item sync`, no trunk history, no rolling tier and no roll or push.
- **c2:** A test starts `tillr drain loop --exit-when-drained` on a root outside any Git repository, with `GIT_CEILING_DIRECTORIES` set so no parent repository is found, and asserts exit 0. A second test starts it on a subdirectory of a repository and asserts exit 0.
- **c3:** The start line prints the definition's name and the steps it skipped.
- **c4:** Roll or push settings in that root's `.tillr.toml` are refused at start, naming the keys, rather than ignored.
- **c5:** No Git, file or process work runs inside a SQLite write transaction.
- **c6:** Under the Git-landing definition the loop starts exactly as before; the existing loop suites pass unchanged, and a store created before this change drains as before.

### Add plain-directory staging for a definition that does not land

A finding needs a directory to work in, not a Git worktree with a base commit and a fence. Today
staging always builds a worktree, `tillr run exec` accepts only that worktree as a run's working
directory (`WorktreeDir` in [request.go](../../tools/tillr-run/run/request.go)), and the brief an
agent receives has nine sections shaped for Git, filled with a base sha, a fence and a target
(`briefValuesFor` in [stage.go](../../tools/tillr-drain/drain/stage.go)). This item stages a plain
lane directory for a store that does not land, and renders the brief from the definition's own
template.

**Product QA can try:** an agent step on a finding, briefed from their own template, in a plain
directory.

**Agent details:** staging is [stage.go](../../tools/tillr-drain/drain/stage.go); the brief
template is the drain's `--brief-template` flag today, whose default is
[brief-template.md](../../tools/tillr-run/backends/brief-template.md).

<!-- tillr: item id="item-def-stage-plain-directory" paths="tools/tillr-drain/,tools/tillr-run/,yolo-packs/tillr/,docs/" blocked_by="item-def-loop-start-without-git,item-def-run-item-env" prose="2" -->

- **c1:** When the definition does not complete on `landing`, staging creates the lane's working directory as a plain directory, with no worktree, no base sha and no fence file, and the run request names it.
- **c2:** `tillr run exec` accepts that working directory for such a run, and still refuses any other directory for a run under Git landing.
- **c3:** The brief is rendered from the definition's `brief_template`, and no value staging fills in for it names a sha, a fence or a trunk. A test renders a brief from a fixture template and asserts its values.
- **c4:** A test stages one item of a fixture definition and asserts the directory, the request and the brief.
- **c5:** Under the Git-landing definition staging is unchanged; the existing stage and loop suites pass unchanged.
- **c6:** No Git, file or process work runs inside a SQLite write transaction.

### Add completion through the definition's hook for items that never land

A finding is finished when it has been judged, not when a commit lands. Today a run's end leads to
the candidate rule, the gate and the lander, and an item can only finish `landed`. This item lets a
definition finish its items through the `complete-item` hook: once a run ends, the hook records the
verdicts and names the finished state, and the drain moves the item there with the reason, pointer
and actor. The drain refuses that move unless every criterion has a recorded verdict, so an item
never finishes unjudged.

**Product QA can try:** a finding that runs, is judged, and ends `confirmed` or `rejected` on its own,
with the reason on the record.

**Agent details:** the end-of-run path is in [loop.go](../../tools/tillr-drain/drain/loop.go); the
move goes through the existing `ItemState` step in [steps.go](../../tools/tillr-drain/drain/steps.go)
with the flags the receipts item added.

<!-- tillr: item id="item-def-finish-on-hook" paths="tools/tillr-drain/,yolo-packs/tillr/,docs/" blocked_by="item-def-stage-plain-directory,item-def-finished-state-receipts" prose="2" -->

- **c1:** When the definition completes on `hook` and a run ends, no candidate rule, boundary bracket, gate or lander runs. The drain calls the `complete-item` hook with the item, the run, the store and the root.
- **c2:** When the hook exits 0 and prints a finished state, a reason and an optional pointer, the drain moves the item there with `--reason`, `--resolves-via` and `--by definition:<name>`.
- **c3:** When the hook exits 1 the item is not finished and stays where it is, under the rules a run that made no candidate gets today. Any other exit holds the item with a named fault that the waiting face shows.
- **c4:** The drain refuses the move, and holds the item with a named reason, unless every criterion of the item has a recorded verdict. A test with a stub hook that answers without recording verdicts asserts the hold.
- **c5:** A test applies an item, runs one stub run in a plain directory, has the hook record a verdict and answer `confirmed`, and asserts the end state, the reason, the event, and that no Git command ran: the test puts a `git` on `PATH` that fails it.
- **c6:** No Git, file or process work runs inside a SQLite write transaction.
- **c7:** Under the Git-landing definition the end of a run is unchanged; the existing loop suites pass unchanged.

### Route a command node into the lane directory and name its node

A findings workflow runs a command step, such as re-running the fuzzer's reproduction, and then an
agent step that investigates. The queued
[command-node block](./tillr-item-graphs.md#add-command-nodes-that-run-a-registered-program) adds
command nodes, and its c2 runs one "in the item's worktree", which is right for Git landing and does
not exist in a store that does not land. This item runs a command node in the plain lane directory
for such a store, and gives every node's program its node id. It is a new item rather than an edit
to that block, because that block is ready and may be dispatched at any time.

**Product QA can try:** a two-step workflow, a command and then an agent, on one finding.

**Agent details:** the run request's `node_id` field comes from the run-environment item; the drain
sets it here for every workflow node.

<!-- tillr: item id="item-def-command-node-plain-directory" paths="tools/tillr-drain/,yolo-packs/tillr/,docs/" blocked_by="item-ig-command-node,item-def-finish-on-hook" prose="2" -->

- **c1:** A command node of an item whose definition does not land runs through `tillr run exec` in the item's plain lane directory. Under Git landing it still runs in the item's worktree, as the command-node block's c2 says.
- **c2:** Every workflow node's run request carries `node_id`, so its program sees `TILLR_NODE_ID` beside `TILLR_ITEM_ID`.
- **c3:** A test runs a two-stage workflow, a command stub and then an agent stub, on an item of a fixture definition that does not land, and asserts each program's working directory and environment.
- **c4:** Under the Git-landing definition the existing workflow and loop suites pass unchanged.

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

<!-- tillr: item id="item-def-recorded-verdict-example" paths="tools/tillr/,tools/tillr-drain/,yolo-packs/tillr/,docs/" blocked_by="item-def-command-node-plain-directory,item-def-tillr-loop-hooks,item-def-item-page-history" prose="2" -->

- **c1:** `tools/tillr/examples/recorded-verdict/` holds a definition named `recorded-verdict` with the states `in-queue`, `blocked` and `closed` and the finished states `confirmed`, `rejected`, `inconclusive`, `repeat-of` and `tooling-fault`; no prerequisite; `completes: hook`; a workflow of one command stage and one agent stage; a hook program; a stub adapter; and a README.
- **c2:** The README gives one fenced command sequence that a Product QA agent runs from an empty directory with no Git repository: `tillr store init --definition`, `tillr item apply` of three findings, `tillr drain loop --exit-when-drained`, then `tillr item list` and `tillr item show` to read the outcomes.
- **c3:** A test copies the example into a fresh directory outside any repository, runs the README's fenced sequence as written, and asserts that the three findings end in three different finished states, each with a reason, a verdict on every criterion and an event, and that no Git command ran.
- **c4:** The test asserts the count of findings in each finished state from `tillr item list --state`.
- **c5:** The README links the definition package's README for every key, says which commands it uses are stable for a consumer, and lists what the example does not show yet.
- **c6:** `tests/pack-sync.test.py` passes with the example in the pack's mirror.

---

## 3. The next tranche, in prose

These are filed when the first tranche has landed, because each builds on the definition file, the
hook contract or the worked example, and because the queue already holds about eighty items. Each
names the request it answers.

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
   owner, never a silent refusal. The queued criteria that carry a cap need amending before they are
   dispatched (see [the separable-parts table](./tillr-separable-parts.md#7-the-second-round-tillrs-answer)).
5. **Add an uncertain outward effect as a state that blocks a retry until reconciled** (R2-12).
6. **Add time and tokens per step attempt, with their source** (R2-13). A command step's record says
   `none` for tokens rather than zero.
7. **Add an owner acknowledgment point** (R2-14): a recorded "seen" per item, which the morning audit
   advances.
8. **Route the waiting class through one classifier** (a stable-surface correction). For a blocked
   item, `tillr view waiting` prints "waits on work" and the served answer "needs you". This joins
   the queued item that gives every held item a class.
9. **Test that a run started from the record sink's own request links to it** (a stable-surface
   correction, read but not re-run). Fix it if the next pass holds it `run-link-missing`.
10. **Add the `after-completion` hook and the retention rule.** Roll and push become the Git-landing
    definition's `after-completion` hook, and `retention` gains its first rule beyond `keep`.

Clocks and the gate's "could not judge" are filed separately, by the owner's direct principle of
2026-11-17, and are not in this plan.

---

## 4. Reconciled with what is queued

- **The terminal-state block** (`item-jl-terminal-state-receipts`, queued and blocked): its c2's
  `--resolves-via` and its c3's refusal are built here by the receipts item and the first item. Its
  c1, a landing record before `landed`, is not, and stays its own.
- **The command-node block** (`item-ig-command-node`, queued and ready): unchanged. Its c2 stays
  right for Git landing; the delta is a new item that depends on it.
- **The item-page block** (`item-ig-view-nodes`, queued and blocked): unchanged. The item-page
  history item feeds its c3.
- **The exit-condition block** (`item-ig-exit-condition`, queued and blocked): unchanged. Completion
  through the hook is a definition's rule; a workflow's `no_commit` exit still decides when a graph
  is done.
- **The unshared-item block** (`item-sync-hold-unshared-items`): unchanged. Its c7 leaves alone a
  store no document declares, which covers a store fed by `apply`.
