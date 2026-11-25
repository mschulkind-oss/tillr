---
title: "Every tillr utility has a written contract: what it provides, what it expects, pinned by the provider's own unit tests"
status: in-review
stage: DESIGN
updated: 2026-11-25
tags: [tillr, contract, interface, tests, design]
summary: "Every tillr utility under tools/tillr* has a contract with two halves: what it provides and what it expects from the others. Each promise is pinned by a fast unit test in the utility that makes it, and consumers test against the provider's own examples. This design states the rules, the kinds of promise and their pins, the reads between utilities that have no stated interface, and the order of the build. One question waits on the owner."
next: "The owner rules OQ-UC1: whether a Go package may be the contract between two utilities. Slices 1 to 5 do not wait on it. Nothing is built."
depends-on:
  - board-utilities.md
  - landing-principles.md
vantage:
  status-chip: true
---

# Every tillr utility has a written contract

**Status:** Nothing here is built: `fd -t d '^contract$' tools` finds nothing, and no test in `tools/tillr*` reads another utility's examples. The statements about the tree in [§2](#2-what-exists-today-measured) were measured on 2026-11-24 with the commands shown there. The inventory in [§10](#10-reach-ins-today) was read from imports, subprocess calls, SQL and registrations, and each slice measures its pair again before it builds.

> **In short.** Each utility keeps, in its own tree, a list of what it promises and a set of **contract examples**: named situations with the exact output another utility would read. The utility's own unit tests rebuild every example, and every consumer's tests take their input from those same files. A change that leaves the contract files alone therefore cannot break a consumer, and a change that edits them is a contract change by definition, made in one commit with the consumers it names.

**Why it matters.** A consumer's tests hold their own copies of what a provider produces: a table's columns, a stored value's fields, a sentence. A provider can change the original, pass its own tests, and break the consumer's suite, which takes minutes and runs after the landing ([§3](#3-the-failures-this-answers)).

**The shape.** A contract per utility (thirteen files beside this one), six rules, one table of pins by kind of promise, and one list of the reads that have no stated interface.

**Cost.** Every consumer test that seeds another utility's table by hand, or asserts another utility's sentence, is rewritten to read the provider's example. The wording of a hold stops being something a test may compare. A contract change costs one more edit, to a file a reviewer can see.

**Start at** [§4](#4-the-six-rules), the rules, then [§5](#5-what-a-contract-holds-and-how-each-kind-is-pinned).

**Needs your ruling:** [OQ-UC1](#OQ-UC1).

**Reads with:** [the common contract](./board-utilities.md#51-the-common-contract) (the four contract pieces the conformance suite holds), [landing principles](./landing-principles.md) (what the gate may and may not rest on), and the thirteen contract files in [§8](#8-the-thirteen-contracts).

**Words.** [The vocabulary](./tillr-vocabulary.md#5-the-words) defines item, run, lane, hold and verdict. These are coined in this document:

- **Utility**: one directory under `tools/` whose name starts with `tillr`. There are thirteen. It is not a Go package: the drain is one utility of many packages.
- **Provider** and **consumer**: for one read, the utility that produces a thing and the utility that reads it. A utility is both, for different things.
- **Surface**: everything of a utility that code outside it can observe. Not its tests.
- **Contract**: the part of a utility's surface it promises to keep. It changes only by an edit to that utility's contract files.
- **Private**: the rest of the surface. No other utility may read it, and its owner may change it freely.
- **Entry**: one promise in a contract, with a name, a kind ([§5](#5-what-a-contract-holds-and-how-each-kind-is-pinned)) and the consumers that rely on it.
- **Expectation**: one thing a consumer reads or calls, written in the consumer's contract and naming the provider's entry.
- **Contract example**: a named pair kept in the provider's tree. One half is a situation, built through the provider's own write path. The other half is the exact output a consumer reads in that situation. It is not a test fixture of the consumer: the consumer never writes one.
- **Contract test**: a unit test in the provider that rebuilds an example and compares, or that compares a list of names with the contract. It runs in the provider's process with no built binary, no network and no store but a fixture.
- **Reach-in**: a read of another utility's private surface, or of anything through a path no contract names. Each is a defect in [§10](#10-reach-ins-today).
- **Part**: a piece of one utility with a test suite of its own, such as the drain's `drain` package and its command package. A part is treated as a utility for every rule here.

---

## 1. The ruling

The owner ruled on 2026-11-21:

1. A change that breaks the view's suite has changed an interface, and that interface is written down.
2. There is a contract between the view and the other utilities that cannot be broken while the unit tests pass.
3. Unit tests exist so that an integration test is never the first to find a defect, and they stay fast.
4. Every tillr utility has a contract, not the view alone.
5. A contract has two halves: a defined API, and what the utility expects from the others.
6. The contracts are written for all of them.

The ruling does not settle these, and the design decides them:

- What form a contract takes, where it is kept and which test pins which kind of promise ([§4](#4-the-six-rules), [§5](#5-what-a-contract-holds-and-how-each-kind-is-pinned), [§6](#6-contract-examples)).
- What happens when a contract has to change ([§7](#7-how-a-contract-changes)).
- Which parts of each surface are contract and which are private (each utility's file).
- The order of the build ([§11](#11-what-i-would-build-in-order)).

"A defined API" between separate utilities can mean commands and records only, or can include a Go package that another utility links. That is the owner's to decide and is asked as [OQ-UC1](#OQ-UC1).

## 2. What exists today, measured

Paths are under `tools/`. Measured on 2026-11-24.

### 2.1 The thirteen utilities

`fd -t d -d 1 '^tillr' tools` lists thirteen directories.

| Utility | Form | What it is | Its contract |
| :--- | :--- | :--- | :--- |
| `tillr` | Go library | The record envelope, the exit codes, the event log, the store, the definition, settings, the Vantage index reader, the watch rules, and the conformance suite | [tillr](./tillr-utility-contracts/tillr.md) |
| `tillr-cli` | Go program | The `tillr <noun>` dispatcher: it finds `tillr-<noun>` and hands it the arguments | [tillr-cli](./tillr-utility-contracts/tillr-cli.md) |
| `tillr-store` | Go program | Creates, checks, recovers and inventories the store | [tillr-store](./tillr-utility-contracts/tillr-store.md) |
| `tillr-item` | Go program, three packages | Items, criteria, verdicts, conflicts, comments, ideas, decisions | [tillr-item](./tillr-utility-contracts/tillr-item.md) |
| `tillr-run` | Go program, one package, and adapter scripts | Runs one agent in a lane and records what it did | [tillr-run](./tillr-utility-contracts/tillr-run.md) |
| `tillr-gate` | Go program | Derives a change, plans checks, runs them and writes a verdict | [tillr-gate](./tillr-utility-contracts/tillr-gate.md) |
| `tillr-land` | Go program, one package | Puts a judged candidate on the trunk as one commit | [tillr-land](./tillr-utility-contracts/tillr-land.md) |
| `tillr-drain` | Go program, many packages | Plans the queue and drives the other utilities through it | [tillr-drain](./tillr-utility-contracts/tillr-drain.md) |
| `tillr-view` | Go program and a web client | Shows the store: status, waiting, lanes, runs, and the observatory server | [tillr-view](./tillr-utility-contracts/tillr-view.md) |
| `tillr-watch` | Go program | Evaluates watch rules over the store and waits on them | [tillr-watch](./tillr-utility-contracts/tillr-watch.md) |
| `tillr-loop` | Python script | The adapter the drain runs for its gate, land and verdict steps | [tillr-loop](./tillr-utility-contracts/tillr-loop.md) |
| `tillr-trace` | Python script and a shell shim | Reads one run's logs and records into a timeline | [tillr-trace](./tillr-utility-contracts/tillr-trace.md) |
| `tillr-roll` | Two Python scripts | Builds and installs new binaries, and keeps the loop running | [tillr-roll](./tillr-utility-contracts/tillr-roll.md) |

### 2.2 Four ways one utility reads another

A read crosses a boundary by one of four paths, and each is used without a list of what may be read through it.

| Path | How it is found | What is found |
| :--- | :--- | :--- |
| **Go import** | Every import of another utility's module in every `.go` file, split into production and test files | The drain imports the item, run and land packages. The view imports fourteen drain packages and two item packages. The land package imports one drain package, and the drain imports land, so the two modules require each other. The store program imports the item, run, land and drain packages, and the watch program the drain package, for no name at all: only so that loading them registers tables and event kinds |
| **Subprocess** | `rg 'exec\.Command'` in Go, `subprocess` and `execvp` in Python, in files that are not tests | The drain runs `tillr-run`, `tillr-item`, `tillr-gate` and the `tillr-loop` adapter. The view runs `tillr-drain why`, `tillr-item place`, `tillr-item idea` and `tillr-trace run`. The adapter runs `tillr-gate`, `tillr-land` and `tillr-item`. The roller runs `tillr-drain` and `tillr-view` |
| **SQL** | Every table name of [§2.3](#23-who-owns-which-table) searched in every other utility's production files | The view owns no table and names many in its own SQL. The drain reads and writes the item and run utilities' tables. The item utility reads the run, land and drain utilities' tables. The land package writes one drain table. Two Python scripts open the store with `sqlite3` |
| **Files, refs and environment** | Path constants, `refs/tillr/` prefixes and `TILLR_` names searched across utilities | The run directory `.tillr/loop/<run>/` is written and read by most of them, each with its own copy of the file names. Ref prefixes are typed as literals in the utilities that do not own them |

The same tree read by count, each with its command:

- `rg -c 'INSERT (OR \w+ )?INTO' tools/tillr-view -g '*_test.go'` reports 425 lines in 104 files. Every one writes a row into a table another utility owns, because the view owns none.
- `rg -c '\.(Query|QueryRow|Exec)(Context)?\(' -g '*.go' -g '!*_test.go'` reports 684 call sites in `tillr-drain`, 162 in `tillr-view` and 136 in `tillr-item`. The library offers no typed reader for any table; every read is a string of SQL.
- `rg -l 'tillr-drain/drain"' --max-depth 1 -g '!*_test.go' tools/tillr-drain` lists eight files of the drain's command package that link the `drain` package, and no list says which of its exported names are its surface.

### 2.3 Who owns which table

A table's owner is the utility whose code registers its DDL (`store.RegisterDDL`, `store.RegisterColumn`).

| Owner | Tables |
| :--- | :--- |
| `tillr` | `events`, `meta`, `schema_migrations`; and for the watch rules `alert_episodes`, `foreground_delivery`, `watch_rule_baselines`, `watch_rule_overrides` |
| `tillr-item` | `items`, `item_criteria`, `item_edges`, `item_splits`, `decisions`, `comment_batches`, `comment_intake`, `comment_intake_documents`, `comment_taken`, `design_without_item`, `idea_intake`, `item_requests`, `item_sync_residue`, `mail_deliveries`, `request_words` |
| `tillr-run` | `lanes`, `runs`, `read_only_runs`, `quota_pauses` |
| `tillr-land` | `landings`, `landing_claims`, and two that nothing writes: `candidates`, `landing_attempts` |
| `tillr-drain` | `dispatches`, `seat_dispatches`, `seat_waiting_choices`, `workflow_selections`, `node_runs`, `node_artifacts`, `node_reused`, `graph_revisions`, `retries`, `drain_passes`, `drain_pass_spend`, `drain_sessions`, `drain_generations`, `drain_runtime_config`, `gate_spans`, `backend_outcomes`, `backend_windows`, `backend_quota`, `backend_quota_readings`, `rolling_state`, `rolling_runs`, `rolling_coverage`, `trunk_history`, `trunk_rewrites`, `trunk_rewrite_unmatched`, `last_designers`, `comment_runs`, `comment_deliveries`, `comment_arrivals`, `comment_reply_states`, `comment_confirmation` |
| `tillr-gate`, `tillr-view`, `tillr-watch`, `tillr-cli`, `tillr-store`, the three scripts | none |

### 2.4 What is already held

Four things are a contract today, and this design keeps all four and builds on them.

1. **The four contract pieces.** [The common contract](./board-utilities.md#51-the-common-contract) names the record format, the schema, the event-kind registry and the exit codes, and `TestTheFourContractPiecesArePresent` and `TestTheExitCodesAreTheContract` in [the conformance suite](../../tools/tillr/conformance/conformance_test.go) hold them. Each Go utility also has a `conformance_test.go` that checks it registers its own kinds and tables.
2. **The argument lists the drain sends its adapter.** The drain's test `TestEveryArgvTheDrainSendsIsAcceptedByTheTillrLoopAdapter` generates [`drain-argv.json`](../../tools/tillr-loop/drain-argv.json) from its real code, and the adapter's suite replays every case. This is a contract example in all but name, and it is the model for [§6](#6-contract-examples): the provider writes the file from real code, and the consumer reads it.
3. **Every hold reason has a rule.** `TestEveryReasonConstantHasAHoldRule` in the drain fails when a reason constant has no row in the hold rules.
4. **Land's results and refusal reasons are closed lists**, held by `TestTheRefusalReasonsAreTheRegisteredSet` and `TestTheOutcomesAreTheRecordedSet`.

None of these holds what a consumer does with a provider's output. The four pieces say a record has an envelope and a kind is registered. They say nothing about which fields the view reads from the drain's answer, or which sentence a view test compares.

[Tillr as separable parts](./tillr-separable-parts.md) says where the line is for a reader outside this repository: record schemas and exit codes are designed to be a contract, and Go packages are not. Inside the repository several utilities link another utility's packages, which is [OQ-UC1](#OQ-UC1).

## 3. The failures this answers

A consumer's tests hold a copy of something the provider produces. The provider changes the original, its own tests pass because none of them knows about the copy, and the copy is found stale by a suite that runs after the landing. Three shapes of that failure occur between the drain or the item utility and the view:

| Shape | What changes in the provider | How the consumer's suite sees it |
| :--- | :--- | :--- |
| **A new read meets a hand-made table** | A drain reader package begins to read a table it did not read before | The view links that package and runs it over stores its tests build by hand, where the table has only the columns the view's tests typed |
| **New fields on a stored value** | The item utility's stored wait gains fields, and the drain's planner gives their absence a meaning | The view's tests write `waiting_on` rows by hand in the old shape, then build the drain from source and ask it to plan over them |
| **A changed sentence** | The sentence the drain writes for a hold's remedy is reworded | A view test compares the start of that sentence with a typed literal |

Two pairs inside one utility fail the same way:

- **The drain's `drain` package and its command package.** The command package's tests build the drain's own binary and compare text the `drain` package prints.
- **The runner's Go tests and its adapter scripts.** A Go test runs [`report.py`](../../tools/tillr-run/backends/report.py) and compares the report's exact bytes, so a change to the script alone fails a Go test first.

## 4. The six rules

Together the rules make the second point of [§1](#1-the-ruling) true: if a provider's unit tests pass and its contract files are untouched, no consumer has anything new to read.

**P1. One contract per utility, kept in that utility's tree.** It has two halves. *Provides* is a list of entries. *Expects* is a list of expectations, each naming one entry of one provider. Until a slice builds the files, the utility's file in [§8](#8-the-thirteen-contracts) is its contract.

**P2. A consumer reads only entries, and only by the path the entry names.** A Go symbol is called, a face is run, a record is decoded, a file is read. Everything else a provider has is private. A read of something private, or of an entry by another path (a table behind a reader function, a sentence beside a kind), is a reach-in.

**P3. Every entry is pinned by a unit test in its provider.** The test fails when the entry's observable form changes. It runs inside the provider's own fast suite, in one process: no tillr binary is built or started, no network is used, no clock is waited on, and the only store is a fixture. [§5](#5-what-a-contract-holds-and-how-each-kind-is-pinned) gives the pin for each kind.

**P4. A consumer's tests take a provider's output from the provider.** The input is a contract example read from the provider's tree, or state made by a builder the provider publishes. A consumer test never writes a row into another utility's table, never types another utility's record, and never types another utility's sentence. A consumer that holds its own copy of what a provider produces is the failure of [§3](#3-the-failures-this-answers): nothing in the provider can tell that the copy has gone stale.

**P5. Names are contract; sentences are not.** A state, a verdict, a hold kind, a rule id, an exit code and a field name are things code matches on, and each belongs to a closed list its provider pins. A sentence written for a person (a hold's reason, a remedy's explanation, an error's text) is private. A consumer shows it unchanged and compares nothing with it. Where a consumer must act on what a sentence says, the provider adds a name beside it and the consumer reads the name.

**P6. A contract changes in one commit, with every consumer it names.** The edit to the contract files is the change of interface. It is visible in the diff, it is what the gate selects consumers on, and nothing else can change what a consumer reads ([§7](#7-how-a-contract-changes)).

A part of a utility follows the same six rules toward the other parts of that utility. The drain's `drain` package has a contract its command package reads, and the runner's adapter scripts have one its Go code reads.

## 5. What a contract holds, and how each kind is pinned

An entry has one of eight kinds. The table gives, for each, what is promised, the unit test in the provider that pins it, and what a consumer may do with it.

| Kind | What the entry promises | The provider's pin | What a consumer may do |
| :--- | :--- | :--- | :--- |
| **Face** | A command exists under this name, takes the flags the entry lists, and answers each outcome with the exit code the entry lists | The face's entry function is called in-process with each example's arguments, and its exit code and standard output are compared. One names test compares the faces and listed flags with the binary's own dispatch | Build the arguments from the entry. Branch on the exit code and on record fields, never on standard error |
| **Record** | A JSON value printed or stored: its `kind`, its field names and types, and the value set of each field that holds a name | Each example's output is compared whole. A strict decode proves every field of the example is a field of the provider's declared type, and the reverse | Decode the listed fields. A field that is not listed does not exist for the consumer |
| **Event** | An event kind, the utility that owns it, its payload fields, and the change of state that appends it | One names test compares the kinds the utility registers with the entry list. Each kind has a payload example, pinned as a record | Read the kind by name and decode its listed payload fields |
| **Package** | The listed exported symbols of a Go package: each one's signature, and what it answers in each example | One test file in the provider uses every listed symbol with the types a consumer uses, so a changed signature does not compile. Readers are run against each example and compared | Import the package and use the listed symbols. Other exported symbols are private |
| **Table** | The listed columns of a table, their types, and the value set of each column that holds a name | One names test compares the listed columns with the DDL the owner registers. Each example's rows are read back and compared | `SELECT` the listed columns. Never write, never read an unlisted column |
| **Name set** | A closed list of names other code matches on: states, verdicts, hold kinds, rule ids, obstacle kinds, end reasons, exit codes | One test compares the whole list with the provider's constants, as [the exit-code test](../../tools/tillr/conformance/conformance_test.go) does today. The list is also kept as a plain file for consumers that are not Go | Compare with the provider's constant or the listed file. Never with a typed copy |
| **Place** | A file path pattern, a Git ref prefix or an environment variable, and the format of what it holds | The real writer writes into a scratch directory or repository, and the path and content are compared with the example | Read the place the entry names, in the format it names |
| **Builder** | A function or seed bundle that puts the provider into a named situation through its own write path | The provider's own example tests are made with it, so it cannot go stale unnoticed | Tests only: make state with it. Production code never calls a builder |

Five decisions sit in that table:

- **Any change to an example is a contract change, including an added field.** There is no judgment of "compatible" to make. An addition costs a regenerated file and a consumer run that passes.
- **A table is private unless it is listed.** Listing is for reading plain rows. A read that derives meaning, such as which items are waiting or why a lane is held, goes through a reader the owner provides, because the derivation is the owner's and a second copy of it lets the view and the drain disagree. For each pair, the first slice lists the columns that are read today, so the pin exists at once; the pair's file says which stay listed and which move behind a reader.
- **A Go package is an allowed kind of entry inside this repository.** The view links fourteen drain packages. [OQ-UC1](#OQ-UC1) asks the owner whether that stands. The pins are the same either way: a reader's answers are examples, and an example is a record.
- **Flags are contract only when an entry lists them.** [The distribution reference](../reference/tillr-distribution.md) bans a flag list in a document; the contract lists the flags another utility passes and no others.
- **Standard error is never contract.** No consumer parses it. A consumer that needs what it says gets a record field or an exit code.

## 6. Contract examples

A contract example is what turns a promise about behavior into something a unit test can hold. It carries the whole design.

**What one is.** A directory in the provider's tree with a name that says the situation, such as "an item blocked by a worker's obstacle, with a retry offered". It holds two things:

1. **The situation**, written as calls to the provider's builders with a fixed clock and fixed ids. It is never a dump of a database.
2. **The outputs**, one file for each entry a consumer reads in that situation: the record a face prints, the value a reader returns (encoded as JSON), the rows of a listed table, the event payload.

**Where they live.** Under `tools/<utility>/contract/`, beside the two lists of P1. The directory name is fixed here so the gate and the checks can name it. The file formats inside are the planner's choice, within three limits: plain files a Python script and the web client can read, byte-stable across runs, and one directory per example.

**How the provider uses one.** Its contract test builds the situation in a fixture store, produces each output through the real code, and compares with the file. A second mode of the same test rewrites the files. Rewriting is the only way an output file changes, and it is a contract change.

**How a consumer uses one.** Its test reads the output file as its input and asserts what the consumer does with it: the page it draws, the decision it makes, the line it prints. Where the consumer needs live state and not a file, it calls the same builder. It does not run the provider's logic to get there, and it does not start the provider's binary.

**What must have an example.** These are floors, not a full list:

- Each value of each name set that changes what a consumer does or shows.
- The empty case, the single case and the many case of every list a consumer draws.
- Each exit code of each face a consumer branches on.
- Every situation that a consumer's suite, a heavy check or an incident found first ([§7.3](#73-when-something-else-still-finds-it-first)).

**What an example is not.** It is not a recording of a real run, which would carry dates and ids nobody chose. It is not shared test scaffolding owned by nobody: every example has one owner, the provider. It is not a second definition of the output: the type in the provider's code is the definition, and the example is an instance of it.

## 7. How a contract changes

### 7.1 The steps

1. A change in a provider alters something an entry promises. A contract test in the provider's own suite fails. Its message names the entry, the example, and the consumers whose expectations name that entry.
2. The author either restores the behavior, or rewrites the example and changes each named consumer, in the same commit.
3. The gate sees a changed path under a `contract/` directory. It adds, for each utility with an expectation on the changed provider, that utility's contract tests: the tests that read examples, selected by name. It adds nothing else on this account.
4. A change that touches no `contract/` path selects no consumer on this account. The suites the gate already selects for other reasons are not reduced by this design.

The consumers of an entry are computed from the expectations that name it. A provider's entry does not carry a list of them, so there is one home for the fact and it cannot fall out of step.

### 7.2 The contract check

One check reads every utility's two lists together. It belongs to the conformance suite in [`tools/tillr/conformance`](../../tools/tillr/conformance/conformance_test.go), which is [the one suite every change runs](./board-utilities.md#8-the-shared-substrate). It fails on:

- an expectation that names an entry its provider does not have;
- an entry with no pin, or an example no test rebuilds;
- a Go import of another utility's package that no expectation names;
- SQL in one utility that names a table another utility owns, with no table expectation;
- a call to another utility's binary or script that no face expectation names.

The last three are a scan of source text, and they start from a list of allowed exceptions equal to [§10](#10-reach-ins-today). That list may only shrink. How the scan reads source is the planner's choice; what it must tell apart is production code from test code, because a test may also use builder entries and production code may not.

An entry that no expectation names is reported and does not fail. It is a candidate to make private.

### 7.3 When something else still finds it first

A contract pins what is written in it. A provider can still change something no example covers, and a consumer's suite or a heavy check can still be the first to see it. When that happens the cause is a missing entry or a missing example, and the repair has a fixed order: add the example to the provider so that the provider's suite fails on the offending change, then fix. A repair that only updates the consumer leaves the same hole open. The contract grows to cover its consumers one found case at a time, which is why the list in [§6](#6-contract-examples) ends with that floor.

### 7.4 Failures and edges

| Situation | What happens |
| :--- | :--- |
| A provider edits its contract and forgets a consumer | The consumer's contract tests fail in the lane gate, before the landing. The heavy suites are not what finds it |
| A consumer needs something no entry offers | The consumer's change adds the entry, its pin and an example to the provider, in the same commit. The contract check refuses the expectation until it does |
| Two lanes change one contract at once | An ordinary conflict in the contract files at the squash. The second lane rebuilds on the new tip. No rule here is special to it |
| A consumer's lane was gated before another lane changed the contract, and then lands | The three-way merge can be clean while the consumer reads an example that changed. The after-merge proof finds it, as it finds any such pair. This design does not close that gap ([§15](#15-risks)) |
| A utility has no consumers | It still has a contract: its faces and exit codes are read by people and by skills. Its expectations may be the larger half |
| Two utilities import each other | Both directions are written as expectations. The cycle itself is a reach-in to remove ([§10](#10-reach-ins-today)) |
| An example cannot be made byte-stable | The entry is not pinned, and the contract check fails on it. The provider injects the clock or the id source first |
| A script consumer cannot import a Go constant | It reads the name set's plain file and the example files. Both are in the provider's tree and in the pack copy |

### 7.5 Limits and defaults

- **Time.** One utility's contract tests finish in 5 seconds on one core, and the contract check in 1 second. Both numbers are first guesses, not measurements; the first slice measures them and the budget is set from that.
- **Trigger.** Contract tests run whenever their utility's fast suite runs, and in a consumer's lane gate when a provider's `contract/` path changed. The contract check runs on every change.
- **State that already exists.** Nothing in a store changes. No record, table or ref is migrated. The build adds files and tests, and moves reads from one path to another.
- **One writer.** A provider writes its own `contract/` directory. A consumer's change may add to it under the second row of [§7.4](#74-failures-and-edges), and the provider's suite judges the addition.
- **Never.** A contract test never starts a built tillr binary, never opens the live store, and never passes by being run again.

## 8. The thirteen contracts

Each file has the same five sections: what the utility provides, what it expects (each expectation naming its provider), which of its surface is contract and which is private, how the contract is pinned, and its reach-ins. Each is the utility's contract until a slice of [§11](#11-what-i-would-build-in-order) moves it into the utility's tree.

| Utility | Provides, in one line | Expects from | Contract |
| :--- | :--- | :--- | :--- |
| `tillr` | The envelope, exit codes, event log, store opener and registries, the definition, settings, Vantage index reader, watch rules | nothing in tillr | [tillr](./tillr-utility-contracts/tillr.md) |
| `tillr-cli` | `tillr <noun> …` | every program it dispatches to | [tillr-cli](./tillr-utility-contracts/tillr-cli.md) |
| `tillr-store` | Store creation, check, recovery, inventory | `tillr`; the item, run, land and drain packages for their registrations | [tillr-store](./tillr-utility-contracts/tillr-store.md) |
| `tillr-item` | Item records and faces, the `item` package, item tables, states, verdicts, rejection rules | `tillr`; the run, land and drain utilities' rows | [tillr-item](./tillr-utility-contracts/tillr-item.md) |
| `tillr-run` | `run.record`, lanes, run states and end causes, the adapter protocol | `tillr`; the drain's request; `tillr-trace`'s shim | [tillr-run](./tillr-utility-contracts/tillr-run.md) |
| `tillr-gate` | `gate.change`, `gate.plan`, `gate.verdict`, the stall exit code; by `D-UC16`, the default gate runner scripts | `tillr`; the check declarations and the registry the operated repository declares | [tillr-gate](./tillr-utility-contracts/tillr-gate.md) |
| `tillr-land` | `land.landing`, the `land` package, landing tables, results and refusal reasons, ref prefixes, the land lock | `tillr`, `tillr-gate`, `tillr-item`, `tillr-drain` | [tillr-land](./tillr-utility-contracts/tillr-land.md) |
| `tillr-drain` | The planner's answer, hold reasons and rules, fourteen reader packages, drain tables and events, the run directory | `tillr`, `tillr-item`, `tillr-run`, `tillr-land`, `tillr-gate`, `tillr-loop` | [tillr-drain](./tillr-utility-contracts/tillr-drain.md) |
| `tillr-view` | Read faces, the HTTP API its web client reads | every utility that owns a table | [tillr-view](./tillr-utility-contracts/tillr-view.md) |
| `tillr-watch` | Rule evaluation and waiting | `tillr`; the drain's registrations | [tillr-watch](./tillr-utility-contracts/tillr-watch.md) |
| `tillr-loop` | The gate, land and verdict steps as faces; `describe` | `tillr-gate`, `tillr-land`, `tillr-item`, `tillr-drain` | [tillr-loop](./tillr-utility-contracts/tillr-loop.md) |
| `tillr-trace` | The `run` document; the shell shim's line format | `tillr-run`, `tillr-drain`, `tillr-gate` | [tillr-trace](./tillr-utility-contracts/tillr-trace.md) |
| `tillr-roll` | The roller as a command (and by `D-UC17`, its Go port); the keeper moved to `tillr-drain keep` | `tillr-drain`, `tillr-view`, `tillr-land`, `tillr` | [tillr-roll](./tillr-utility-contracts/tillr-roll.md) |

### 8.1 Defaults come from tillr's installation

This subsection is from an operator request recorded on 2026-11-25, and its mechanism is the designer's decision (`D-UC16`). It is not an owner ruling, and the owner may overturn it. It amends `D-UC11` for the four defaults below and for nothing else.

**The fault.** A **consumer repository** has a tillr store and is worked by the tillr loop, but does not carry tillr's source. It gets tillr's programs from the pack under `yolo-packs/tillr`, rendered to `~/.local/share/tillr/<module>` with launchers on `PATH`. The request reports that on 2026-11-24 such a repository had to keep copies of tillr's own files under its `tools/` so the pack's programs would work, and that its copy of the gate runner scripts had already drifted from this repository's. The request's words for the fix: "Wanted: the defaults come from tillr's install; a consumer owns only its declared checks."

**What the tree does today**, read on 2026-11-25:

| Default | Where it resolves today | Read with |
| :--- | :--- | :--- |
| The brief template | `<root>/tools/tillr-run/backends/brief-template.md` | `rg -n 'brief-template.md' tools/tillr-drain/main.go tools/tillr-drain/recovery.go` |
| The adapters directory `tillr-run exec` reads, `--backends-dir` | `<repository root>/tools/tillr-run/backends`, derived from the git common directory | `defaultBackendsDir` in `tools/tillr-run/main.go` |
| The fork gate | `<root>/tools/tillr-drain/fork-gate.py` | `rg -n 'fork-gate.py' tools/tillr-drain/drain/steps.go` |
| The change gate and the fast tree check | `tools/gate/change-gate.py` and `tools/gate/tree-check.py`, relative to the candidate worktree | `DEFAULT_GATE_PROGRAM` and the `replay` face in `tools/tillr-loop/tillr-loop` |

The first three files are already delivered in the pack, inside `tillr-run` and `tillr-drain`; only the default path points at the operated repository. The two runner scripts are not delivered at all: `tools/gate/` is not a tillr utility and is not in `MODULES` of `tests/pack-sync.test.py`.

**The decision.**

1. **A default never resolves under the operated repository's `tools/`.** Each of the four defaults resolves inside tillr's **installation**: the module directory of the utility that owns the file. That is `tools/<module>` in this repository and `~/.local/share/tillr/<module>` in a rendered pack. How a program finds its own module directory is private to it.
2. **The owner of each default** is the utility whose tree holds it: `tillr-run` for the brief template and the adapters directory, `tillr-drain` for the fork gate, and `tillr-gate` for the two runner scripts.
3. **The runner scripts become engine code.** `change-gate.py`, `tree-check.py` and the modules they import move into `tillr-gate`'s tree, so they are delivered with it and exist once. They are no longer "the repository's gate scripts, outside tillr". The thirteen utilities stay thirteen (`D-UC1`).
4. **A consumer owns only its declared checks**: the files under its own `tools/gate/checks`, which are the declarations and the programs those declarations name. The installed runners read them from the candidate worktree, as they do today. That directory's path is unchanged.
5. **An explicit flag still wins.** `--brief-template`, `--backends-dir`, `--gate-program` and `--registry` keep their meaning. Only the value used when the flag is absent changes.
6. **A missing installed default is a refusal that names the installed path.** It never falls back to the operated repository's `tools/`: a fallback would let the drifted copy judge again without anyone seeing it.

**What this changes for the contracts.** The provider of the runner scripts in [tillr-loop](./tillr-utility-contracts/tillr-loop.md) and [tillr-gate](./tillr-utility-contracts/tillr-gate.md) is `tillr-gate`. The check declarations keep an outside provider, now named as the operated repository. [tillr-run](./tillr-utility-contracts/tillr-run.md) and [tillr-drain](./tillr-utility-contracts/tillr-drain.md) each state the default they own.

**Rejected** (the designer's, as the decision is):

| Alternative | Why not |
| :--- | :--- |
| Keep the defaults and have each consumer copy the files | It is the fault as reported: engine code in two repositories, already drifted |
| Try the installation first and fall back to `<root>/tools/` | Which file judged a candidate would depend on what the repository happens to hold |
| Ship `tools/gate/` whole in the pack, declarations included | The declarations are the consumer's own. This repository's would then be judged against another repository's tree |
| A new fourteenth utility for the runners | The runners are the judge `tillr-gate run --gate` starts. A new utility adds a contract and a pack module for two scripts |

**Not decided here, and listed for the build:**

- **A runner names this repository's checks.** `change-gate.py` reads `tools/gate/checks/go-build.json` and runs `tools/gate/checks/go-build.py` by name (`rg -n 'go-build' tools/gate/change-gate.py`, 2026-11-25). Engine code that names one repository's declaration needs that step to become a declaration's own business or to be skipped when the declaration is absent.
- **Which runner judges a candidate that changes the runner.** Today the candidate's copy does, because the path is relative to the candidate worktree. With an installed default, a change to the runners in this repository is judged by the installed copy unless this repository names the candidate's copy with `--gate-program`. [The self-hosted build](./tillr-selfhosted-build.md) owns that choice.
- **Other files under `tools/gate/`** (`done.py`, `go_affected.py`, `go-test-edges.json`, the tracked identity list) are not named by the request and stay where they are. The drain runs `tools/gate/go_affected.py` only when the file exists (`replay_go_affected.go`).
- **The adapter itself.** The drain runs its embedded copy, and the pack delivers a standalone one at `~/.local/share/tillr/tillr-loop/tillr-loop` (`case_loop_adapter` in `tests/pack-sync.test.py`), so a consumer already needs no copy of it.

### 8.2 The roller moves to Go

This subsection is from an operator request recorded on 2026-11-25, and its mechanism is the designer's decision (`D-UC17`). It is not an owner ruling, and the owner may overturn it. It amends `D-UC11` and [tillr-roll](./tillr-utility-contracts/tillr-roll.md) for how the roller and its tests are implemented, how deferred `tillr-watch` retirement handles unlink failures, and how callers in `tools/tillr-drain` connect to it.

**What the tree holds today**, read on 2026-11-25:

- **The keeper has already moved to Go.** `tillr-keep` and `tests/tillr-keep.test.py` were ported to `tillr-drain keep` in [`tools/tillr-drain/keep/keep.go`](../../tools/tillr-drain/keep/keep.go) and [`tools/tillr-drain/keep/restart.go`](../../tools/tillr-drain/keep/restart.go), invoked by `just tillr-keep` and `just tillr-restart` in [`.justfile`](../../.justfile).
- **The roller is still Python.** [`tools/tillr-roll/tillr-roll.py`](../../tools/tillr-roll/tillr-roll.py) and [`tests/tillr-roll.test.py`](../../tests/tillr-roll.test.py) remain on `HEAD` and are not yet ported.
- **Four `tillr-drain` tests load `tillr-roll.py` directly.** `TestNamedRollWatchInstallation` in [`watch_roll_test.go`](../../tools/tillr-drain/drain/watch_roll_test.go), `TestAfterMergeLandingRollInstallsWithoutRepeatingProof` in [`post_landing_roll_test.go`](../../tools/tillr-drain/drain/post_landing_roll_test.go), `TestSchemaUpgradeRollsWithoutStoppingRunningWorker` in [`schema_roll_test.go`](../../tools/tillr-drain/drain/schema_roll_test.go), and `TestRollCleanCheckoutIncludesFullTreeAndRecordsShardFailureDetails` in [`shard_balance_test.go`](../../tools/tillr-drain/drain/shard_balance_test.go) execute `tools/tillr-roll/tillr-roll.py` in embedded Python harnesses, with `tools/tillr-roll/tillr-roll.py` listed in [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json) and [`tools/gate/checks/go-affected.json`](../../tools/gate/checks/go-affected.json).

**The decision.**

1. **Port, not redesign.** The roller moves from `tools/tillr-roll/tillr-roll.py` to Go with identical behavior and command-line contract: `--no-request`, `--commit <rev>`, `--pi` and `--pi-extensions`; exit codes `0` and `1`; printed lines (`tillr-roll: ` failure prefix and summary lines); files (`tillr-roll.commit`, `tillr-roll.lock`, `tillr-watch.retire`, `.tillr-schema-roll-<digest>/roll.json` and `<store>.roll-alarm.json`); store reads and `drain.roll-failed` writes; and environment variables (`TILLR_ROLL_VERIFICATION`, `TILLR_BUILD_UNPROVED` and `TILLR_SQLITE_TRACE`). No third-party dependency is added beyond modules the repository already uses.
2. **Where the Go lives.** The plan step places the Go where it fits the existing modules best: either as a Go module at `tools/tillr-roll` (`module github.com/mschulkind-oss/tillr-roll`, with an importable `roll` package and a `main` package run by `just tillr-roll`), preserving the thirteen `tools/tillr*` directories of `D-UC1` and `SOURCE_ONLY_MODULES` in [`tests/pack-sync.test.py`](../../tests/pack-sync.test.py), or as a face of an existing Go utility when every pack-mirror and module list updates in the same commit.
3. **Go tests replace the Python suite.** Every roller test case in `tests/tillr-roll.test.py` is ported to Go tests in the same commit, and `tools/tillr-roll/tillr-roll.py` and `tests/tillr-roll.test.py` are deleted. This supersedes the earlier note in [tillr-roll](./tillr-utility-contracts/tillr-roll.md) that `tests/tillr-roll.test.py` moves into `tools/tillr-roll` unchanged.
4. **Deferred `tillr-watch` retirement propagates non-`os.ErrNotExist` unlink errors and restores recovery state.** In `settle_watch_retirement` of `tools/tillr-roll/tillr-roll.py` (pinned by `TestNamedRollWatchInstallation` in [`watch_roll_test.go`](../../tools/tillr-drain/drain/watch_roll_test.go)), when `tillr-watch.retire` names a completed schema roll whose staging directory is gone and whose commit matches `tillr-roll.commit`, the roller moves `tillr-watch` aside to a `.tillr-watch-*` backup, unlinks `tillr-watch.retire` with `missing_ok=True`, and then unlinks the backup (`retirement.commit()`). In Go:
   - Removing `tillr-watch.retire` ignores **only** `os.ErrNotExist`. Any other unlink error (such as `EACCES` / `EPERM`) must be returned before committing retirement so deferred cleanup restores `tillr-watch` from the `.tillr-watch-*` backup, leaves `tillr-watch.retire` in place for retry, and fails the roll rather than discarding the error.
   - If removing the `.tillr-watch-*` backup fails in `Commit`, the roller writes the saved marker bytes back to `tillr-watch.retire`, restores `tillr-watch` from the backup, and returns the error.
5. **Callers and plan boundaries.** The same change updates `.justfile`, [`tools/gate/checks/tillr-roll.json`](../../tools/gate/checks/tillr-roll.json), [`tools/gate/checks/go-build.py`](../../tools/gate/checks/go-build.py) (when `tools/tillr-roll` is a Go module), [`tools/gate/checks/go-affected.json`](../../tools/gate/checks/go-affected.json), [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json), [`tools/tillr-drain/internal/testinputs/siblings.txt`](../../tools/tillr-drain/internal/testinputs/siblings.txt) (when `tillr-drain` tests build or read `tools/tillr-roll` sources across modules), the four `tools/tillr-drain/drain/` roller tests, documents naming `tillr-roll.py`, and the pack mirror via `just pack-sync`. When a multi-node plan splits the Go roller package from `tools/tillr-drain/drain/` caller tests, either the roller node's own unit tests verify both marker-unlink and backup-unlink failure recovery before caller nodes run, or the caller node's write list includes the roller's directory so a cross-package discrepancy is not stranded across piece fences.

**Rejected** (the designer's, as the decision is):

| Alternative | Why not |
| :--- | :--- |
| Discard errors from removing `tillr-watch.retire` (`_ = removeIgnoringNotExist(...)`) | Violates `settle_watch_retirement` in `tillr-roll.py` and `TestNamedRollWatchInstallation`: a permission failure unlinking the marker would delete `tillr-watch` while leaving `tillr-watch.retire` behind |
| Drop the marker/backup unlink failure case from `TestNamedRollWatchInstallation` | Breaks the request's rule to keep behavior and every test case unchanged |
| Keep `tests/tillr-roll.test.py` as a Python test file inside `tools/tillr-roll/` | Contradicts the operator request to port the roller's tests to Go and delete the Python files |

## 9. Who expects what from whom

One row per pair with a read, in the order [§11](#11-what-i-would-build-in-order) builds them. "Paths" uses the four of [§2.2](#22-four-ways-one-utility-reads-another): I for a Go import, P for a subprocess, S for SQL, F for files, refs or environment.

| Consumer | Provider | Paths | What is read | Slice |
| :--- | :--- | :--- | :--- | :--- |
| `tillr-view` | `tillr-drain` | I P S F | The planner's answer from `why`; hold rules, obstacles, alarms, faults, quota, presence, restart, stop and other reader packages; drain tables; drain event payloads; the loop directory | 1 |
| `tillr-view` | `tillr-item` | I P S F | Item, criterion and edge rows; the stored wait; states; `place` and `idea` and their rejection rules; the idea words file | 2 |
| `tillr-drain` command package | `tillr-drain` `drain` package | I P | The package's exported names; text the engine prints | 3 |
| `tillr-run` Go code | `tillr-run` adapter scripts | P F | The report header and footer; the ending-capability and pause-kind answers; adapter exit codes | 4 |
| `tillr-drain` | `tillr-run` | I P S F | `run.record` and `exec`; the `run` package; lane, read-only-run and pause rows (read and written); the report footer; adapter exit codes | 6 |
| `tillr-drain` | `tillr-item` | I P S | The `item` package; `state` and `sync` and their exit codes; item rows (read and written); rejection rules | 7 |
| `tillr-item` | `tillr-drain` | S | `retries`, `workflow_selections`; three drain event kinds; the retry command's shape | 7 |
| `tillr-drain` | `tillr-land` | I P S F | The `land` package; `land.landing` from the land step; results and reasons; landing rows; ref prefixes; the land lock | 8 |
| `tillr-land` | `tillr-drain` | I S | The red-cap hold type; `trunk_history` (read and written), `trunk_rewrites` | 8 |
| `tillr-land`, `tillr-item` | each other | S | Item states; `landings`, `landing_claims` | 8 |
| `tillr-land`, `tillr-drain`, `tillr-view`, `tillr-trace`, `tillr-loop` | `tillr-gate` | P F | `gate.verdict`, `gate.plan`, `gate.change`; exit codes with the stall code; log paths | 9 |
| `tillr-gate` | `tillr-drain` | F | The fast-verdict path and the heavy lock handle in the environment | 9 |
| every Go utility | `tillr` | I S | The envelope and exit codes; the store opener; the registries; `events`; the definition | 10 |
| `tillr` | `tillr-item`, `tillr-drain` | S | `items`, `workflow_selections`, `node_runs`, `graph_revisions`, read by the library | 10 |
| `tillr-loop` | `tillr-gate`, `tillr-land`, `tillr-item` | P F | Each face's flags and exit codes; `item.record` from `show`; `verdict record` input | 11 |
| `tillr-drain` | `tillr-loop` | P F | `describe`; the three step faces; exit codes; the embedded copy | 11 |
| `tillr-roll` | `tillr-drain`, `tillr-view`, `tillr-land`, `tillr` | P S F | `loop --check` and its one line; `restart --wait` and its answer; `stop`; the build event; `landings`; a raw write to `events` | 12 |
| `tillr-trace` | `tillr-run`, `tillr-drain`, `tillr-gate` | S F | `runs`, `dispatches`; the run directory; gate records | 13 |
| `tillr-view`, `tillr-run` | `tillr-trace` | P F | The `run` document; the shim's path and line format | 13 |
| `tillr-view`, `tillr-item`, `tillr-drain` readers | `tillr-run` | S | `runs`, `lanes`, `read_only_runs`, `quota_pauses`; run states; run event payloads | 13 |
| `tillr-view`, `tillr-item`, `tillr-roll` | `tillr-land` | S | `landings` and the `land.landing` payload | 13 |
| `tillr-store`, `tillr-watch` | item, run, land, drain packages | I | Nothing by name: the load-time registrations | 14 |
| `tillr-cli`, `tillr-roll`, the drain's pack mirror | all | F | The list of programs | 14 |

## 10. Reach-ins today

Every row is a read with no stated interface. Each is a defect. The slice that fixes it is named; "listed" means the read becomes an expectation on an entry and stays, and "removed" means the read goes away. The ids are stable and the list may only shrink ([§7.2](#72-the-contract-check)).

### 10.1 The drain and the view

| ID | Reach-in | Fix | Slice |
| :--- | :--- | :--- | :--- |
| RI-01 | View tests build `tillr-drain` from source and run `why`, `plan` and `once`, then compare its sentences: a row's `why`, `mover`, `class` or `where` with a typed literal | Removed: the tests read the drain's examples; no sentence is compared | 1 |
| RI-02 | View tests write drain tables by hand over a copied schema: [`serve-fixture.sql`](../../tools/tillr-view/testdata/serve-fixture.sql) and inline `CREATE TABLE` statements | Removed: state comes from the owner's builder | 1, 13 |
| RI-03 | View production SQL names drain tables and reads drain event payloads by JSON path, such as `$.decision.run_request.run_id` | Listed for plain rows; derived reads move behind a drain reader | 1, 13 |
| RI-04 | The view holds a second copy of the planner's rule: `dispatchable.go`, and `waiting.go` in history mode, with hold reasons typed as literals | Removed: the drain provides the answer; the view shows it | 1 |
| RI-05 | The view's own copies of drain shapes and numbers: `waitingRow`, `stopExplanation`, the build stamp, the launcher digest, `SessionGraceMS`, event kind literals | Removed: the drain's types and name sets are read | 1 |
| RI-06 | The view writes the store through the drain's `restart` package, while its own header says it writes nothing | Listed as a package entry, and the header corrected | 1 |
| RI-07 | The view imports fourteen drain packages, and no list says which exported names it may use. `TestWaitingEveryDrainReasonHasARule` walks the drain's rule table inside the view's suite | Listed, name by name; the walk moves to the drain's contract test | 1 |
| RI-08 | The view's test helper builds over a workspace of every `tools/tillr*` module, so a compile error anywhere reaches the view's suite | Removed with RI-01 | 1 |

### 10.2 The item utility and the view

| ID | Reach-in | Fix | Slice |
| :--- | :--- | :--- | :--- |
| RI-09 | View SQL on item tables, and on the stored wait's JSON inside `items.waiting_on` | Listed for plain rows; "who is waiting on what" moves behind an item reader | 2 |
| RI-10 | View tests write item tables by hand and add columns to the copied schema with `ALTER TABLE` | Removed: the item utility's builder | 2 |
| RI-11 | Item names typed in the view: the states, with both `resolved-elsewhere` and `resolved_elsewhere`; a copy of the in-order rule in `order.go`; rejection rule names in `apierror.go`; a copy of the item record in `payloads.go` | Removed: the item utility's name sets and record entry | 2 |
| RI-12 | The idea words path is computed in both utilities | Listed as a place entry | 2 |

### 10.3 Inside the drain, and inside the runner

| ID | Reach-in | Fix | Slice |
| :--- | :--- | :--- | :--- |
| RI-13 | The drain's command-package tests build the binary and compare text the `drain` package prints | Removed: the engine's examples; names beside the sentences | 3 |
| RI-14 | The command package and its tests use exported names of `drain`, and nothing says which of the package's names are its surface | Listed | 3 |
| RI-15 | The report's header keys are typed in [`report.py`](../../tools/tillr-run/backends/report.py) and again in a test outside the runner; a Go test types the report's bytes | Removed: one example written by the script's own test, read by the Go test | 4 |
| RI-16 | Three parsers of the `RESULT:` footer with three grammars: `report.py`, the runner's `settled.go`, the drain's `failover.go` | Removed: one name set, one parser per language, both pinned to the same examples | 4, 6 |
| RI-17 | The obstacle field table is typed in two adapter scripts and in the drain | Removed: one name set in the runner's contract | 4, 6 |
| RI-18 | The adapters find `tillr-trace`'s shim by a relative path; the drain derives adapter file names from the adapter directory and types the adapter's exit codes 2 and 124 | Listed as place and name-set entries | 4, 13 |

### 10.4 The runner, the item utility and the drain

| ID | Reach-in | Fix | Slice |
| :--- | :--- | :--- | :--- |
| RI-19 | The drain writes the runner's tables with its own SQL: `lanes`, `read_only_runs`, `quota_pauses` | Removed: writes go through functions the runner provides | 6 |
| RI-20 | Run states typed as literals outside the runner. The item utility's list omits `paused`; the view queries `'running'`, which nothing writes | Removed: the runner's name set | 6, 13 |
| RI-21 | The landing-offer and ending-capability file names are typed in the drain and in the runner | Listed as place entries | 6 |
| RI-22 | The drain writes the item utility's tables with its own SQL, with no item event: `items.allowed_paths`, `items.title`, `item_criteria.text`, `comment_intake`, `comment_intake_documents` | Removed: writes go through the `item` package | 7 |
| RI-23 | The drain reads item tables with its own SQL in many files, with item states typed as constants of its own | Listed for plain rows; the rest through the `item` package it already links | 7 |
| RI-24 | The item utility reads `retries`, `workflow_selections` and three drain event kinds, and holds copies of the drain's retry command, its two acknowledgment names and exit code 124 | Listed; the copies become name-set reads | 7 |
| RI-25 | The drain matches six item event kinds the item utility never registers, and one record kind that is never an event | Removed | 7 |
| RI-26 | The drain reads `tillr-item state --help` and searches the text for flag names; what `sync`'s exit codes mean is written only in the drain | Listed as a face entry with its flags and codes | 7 |

### 10.5 Land, the gate and the drain

| ID | Reach-in | Fix | Slice |
| :--- | :--- | :--- | :--- |
| RI-27 | The land and drain modules require each other. Land's only use of the drain is one struct, the red-cap hold | Removed: the type moves or goes | 8 |
| RI-28 | Land reads and writes the drain's `trunk_history` and reads `trunk_rewrites`; the reason `trunk-rewritten` and two ref names are typed in both | Removed: one owner for the table, a function for the other | 8 |
| RI-29 | The drain implements the land lock's protocol a second time in `trunk_lock.go` | Removed: the `land` package's lock, which the drain already links | 8 |
| RI-30 | Land's results and reasons are typed again in the drain and the view; the drain decodes `land.landing` into unnamed structs and keeps its own copy of the lock observation | Removed: land's types and name sets | 8 |
| RI-31 | Ref prefixes typed as literals outside their owner: landed, owner-recovery, published, patch-history, runs, and `refs/tillr/green` in several utilities. The published prefix is declared in land and written only by the drain | Listed as place entries, each with one owner | 8 |
| RI-32 | `landings` is read with raw SQL from the drain, the view, the item utility and the roller, and consumer test files write landing rows by hand | Listed; tests use land's builder | 8, 13 |
| RI-33 | Land reads `items` to refuse a closed item; the item utility reads `landing_claims` and `landings` | Listed | 8 |
| RI-34 | The gate's record types cannot be imported, so each consumer has a copy: land's `verdict.go`, files of the drain, the view's `payloads.go` (which names land as the owner and lacks `inherited_failures`), and `tillr-trace` | Removed: one definition and one set of examples, read by all five | 9 |
| RI-35 | Land recomputes the gate's registry digest and reads the gate's files out of the run directory | Listed as record and place entries | 9 |
| RI-36 | The stall exit code 6 is typed in the gate, the drain, the adapter and two gate scripts | Removed: the gate's exit-code name set | 9 |
| RI-37 | `gate.plan` and `gate.verdict` are registered as event kinds by the gate and by the drain, with different payloads, so no program can link both | Removed: one owner for each kind, and a second name for the drain's wrapped event | 9 |
| RI-38 | The directory `tillr-gate/` under the Git common directory holds the gate's remembered results and the drain's proof files, with no owner stated. Two environment variables carry paths and a file handle between the drain and the gate. The drain parses the gate program's standard error | Listed as place entries; the parsed text becomes a record field | 9 |
| RI-39 | The view models `land.candidate` and `land.attempt`, which land does not write | Removed | 9 |

### 10.6 The library, the store, the watch and the dispatcher

| ID | Reach-in | Fix | Slice |
| :--- | :--- | :--- | :--- |
| RI-40 | The library provides no typed read of any table, so every reader is a string of SQL typed where it is used | Fixed pair by pair, as each owner gains readers | every slice |
| RI-41 | The library reads tables of utilities above it: `store/judge_migration.go` names `items`, `workflow_selections`, `node_runs` and `graph_revisions`, and `definition/store.go` reads `items` | Removed: the owner provides the migration step and the check | 10 |
| RI-42 | The store and watch programs, and one file of the drain, import other utilities' packages only so that loading them registers tables and kinds | Listed: each package's registration is an entry, and the import names it | 14 |
| RI-43 | `tillr-store`'s inventory types other utilities' table, ref and file names, and its schema test builds other utilities' binaries | Removed: the owners' place and table entries | 14 |
| RI-44 | The list of programs is typed in the dispatcher, the drain's schema roll, the roller and the pack mirror, and the lists differ | Removed: one name set in the library | 14 |
| RI-45 | The store-recovery wording is typed in the library and in the view | Removed: a name beside the sentence | 10 |
| RI-46 | `tillr-watch` prints no envelope, answers 1 for every error and 2 for a wait that timed out, and has no conformance test. The gate's event kinds cannot be linked into it, so a rule that names one is refused. Its tables are not made by `tillr-store init` | Brought under the four pieces, or the difference written as its contract | 14 |

### 10.7 The three scripts

| ID | Reach-in | Fix | Slice |
| :--- | :--- | :--- | :--- |
| RI-47 | The adapter types the flags of `tillr-gate`, `tillr-land` and `tillr-item`, and its suite runs shell stand-ins that accept anything, so a renamed flag passes | Removed: each provider's face examples | 11 |
| RI-48 | The adapter parses `tillr-item show`, writes an `item.verdict` record by hand, and holds its own copies of the criterion hash and the claim classes | Removed: the item utility's examples and name sets | 11 |
| RI-49 | The drain embeds a byte-for-byte copy of the adapter, kept equal by a script a person runs | Listed: the copy is a generated file with one writer | 11 |
| RI-50 | The adapter's suite compiles a Go test into the drain's package with an overlay and uses the drain's unexported helpers | Removed: the drain's contract test | 11 |
| RI-51 | The roller reads `drain.build-started` rows and `landings` with `sqlite3`, and inserts a `drain.roll-failed` event without the event log's write path | Removed: a drain face writes the event; reads are listed | 12 |
| RI-52 | The roller and the drain match each other's printed lines: `tillr-drain: schema migration required`, the two-line answer of `restart --wait`, the `tillr-roll: ` prefix | Removed: exit codes and record fields | 12 |
| RI-53 | Files shared by the roller and the drain with names typed in both: `roll.json`, `tillr-roll.commit`, the roll-alarm file. The roller copies exit code 3 and the list of sharded modules | Listed as place entries; name sets read from their owner | 12 |
| RI-54 | The keeper finds the loop by reading process names and arguments under `/proc`, and accepts exit 0 or 2 from `stop` | Listed: the process name and `stop`'s codes become entries | 12 |
| RI-55 | `tillr-trace` reads `runs` and `dispatches` with `sqlite3` and treats every SQL error as an empty result, so a renamed column shows as "no store row for this run" | Listed, and the error is reported | 13 |
| RI-56 | `tillr-trace` reads a `manifest.json` that nothing in the repository writes (`rg -n 'manifest\.json' tools/tillr-* -g '!tools/tillr-view/web/**' -g '!tools/tillr-trace/test.py'` finds only the reader) | Removed | 13 |
| RI-57 | The view decodes `tillr-trace run` with an unnamed struct and parses the shim's `proc-exec.jsonl` a second time | Removed: the trace's examples | 13 |
| RI-58 | The scripts' test suites type other utilities' DDL: `tillr-trace`'s `test.py` and the roller's test | Removed: seed files the owners provide | 12, 13 |

## 11. What I would build, in order

Each slice is one pair, or one small set of pairs with the same provider. Each lands alone and leaves everything working. Each ends with a **negative control**: a deliberate break of one of the shapes of [§3](#3-the-failures-this-answers), shown to fail in the provider's own suite.

Every slice begins by measuring its pair again at the tip, because the tree moves.

| Slice | Pair | What it builds | Done when |
| :--- | :--- | :--- | :--- |
| 1 | `tillr-drain` → `tillr-view`: holds and the planner's answer | The first `contract/` directory, in the drain. Name sets for hold reasons, classes, movers' owners and obstacle kinds. A record entry for the `why` answer. Examples for each hold class, each obstacle kind, the empty queue and a full one. The view's waiting tests read those examples: none builds the drain or compares a sentence. Clears RI-01, RI-04, RI-05, RI-07, RI-08 | A change of each of the three shapes of [§3](#3-the-failures-this-answers) either fails a contract test in the drain's suite or leaves the view's suite passing. The view's suite builds no drain binary for a waiting test |
| 2 | `tillr-item` → `tillr-view` | The item utility's `contract/`: states, the stored wait, rejection rules, the `place` and `idea` faces, a builder for item rows. Clears RI-09 to RI-12 | Adding a field to the stored wait fails an item contract test. No view test contains `INSERT INTO items` |
| 3 | The drain's `drain` package → its command package | The engine's entries for the names the command package uses, and a name beside each line a command test reads. Clears RI-13, RI-14 | A changed report sentence in `drain` fails no command-package test |
| 4 | The runner's adapter scripts ↔ its Go code | Report, footer and obstacle-field examples written by the scripts' own tests and read by the Go tests. Clears RI-15, and the runner's half of RI-16 to RI-18 | Changing a header key in `report.py` fails the script's test, not a Go test first |
| 5 | The contract check | The check of [§7.2](#72-the-contract-check) in the conformance suite, with its list of allowed exceptions equal to what is left of [§10](#10-reach-ins-today), and the gate's selection rule of [§7.1](#71-the-steps) | A new import of another utility's package with no expectation fails the check. A change under a `contract/` path selects its consumers' contract tests |
| 6 | `tillr-run` → `tillr-drain` | The runner's `contract/`: `run.record`, run states, end causes, pause kinds, lane functions the drain calls to write. Clears RI-16, RI-17, RI-19, RI-21 | No drain file writes a runner table |
| 7 | `tillr-item` ↔ `tillr-drain` | Entries for the `item` names the drain uses, for `state` and `sync` with their codes, and for writes. The drain's retry names as entries the item utility reads. Clears RI-22 to RI-26 | No drain file writes an item table |
| 8 | `tillr-land` ↔ `tillr-drain`, and land ↔ item | Land's `contract/`: the landing record, results, reasons, ref prefixes, the lock, a builder for landing rows. The module cycle removed. Clears RI-27 to RI-33 | `tillr-land`'s `go.mod` does not name `tillr-drain` |
| 9 | `tillr-gate` → its five readers | The gate's `contract/`: the three records with examples for pass, fail, error, stalled and pass-inherited; exit codes; the places it writes. One owner per event kind. Clears RI-34 to RI-39 | Each of the five readers decodes the gate's own example files in its tests |
| 10 | `tillr` → every utility | The library's entries: the names each utility uses of each package. The library stops reading tables above it. Clears RI-41, RI-45 | `rg 'FROM items' tools/tillr` finds nothing outside tests |
| 11 | `tillr-loop` ↔ the drain, gate, land, item | Face examples the adapter's suite replays in place of stand-ins that accept anything. Clears RI-47 to RI-50 | A renamed flag in `tillr-land pass` fails land's contract test and the adapter's |
| 12 | `tillr-roll` ↔ the drain and the view | Faces and exit codes in place of matched lines; one writer for the roll event. Clears RI-51 to RI-54 | The roller opens no store |
| 13 | The remaining reads of rows: view, trace and item on the runner's, land's and the drain's tables | Table entries and builders for what is still read as plain rows. Clears RI-02, RI-03, RI-20, RI-32, RI-55 to RI-58 | The view's `serve-fixture.sql` is deleted |
| 14 | The store, the watch and the dispatcher | The registrations as entries, one program list, the watch under the four pieces. Clears RI-42 to RI-44, RI-46 | The contract check's list of allowed exceptions is empty, and the check has no such list |

Slices 1 to 4 are the pairs of [§3](#3-the-failures-this-answers). Slice 5 comes next so that everything after it shrinks a checked list and cannot grow it. Slices 6 to 9 follow the pipeline in the order the drain drives it. The scripts come late because each needs its provider's examples first.

## 12. How the three failures would have gone

- **A changed sentence.** The mover's sentence is private (P5), so no view test compares it and the change breaks nothing. A view test that needs to know "the operator moves this, by adding a path" reads the mover's owner and the obstacle's kind, which are names, from the drain's example.
- **New fields on a stored wait.** The stored wait is a record entry of the item utility. Adding fields changes its example, so the item utility's contract test fails in the author's own lane. The message names the drain and the view as consumers. The author regenerates the example, and the consumers' contract tests run in that lane's gate against the new file.
- **A new read meets a hand-made table.** The reader's answers are entries with examples built by the table owner's builder, which makes a real table. The view's tests read the reader's examples and build no table by hand, so there is no half-made table for the new read to meet.

For the two pairs inside one utility: the report's bytes are an example written by `report.py`'s own test and read by the Go test, so the script's change fails beside the script. The command package reads the engine's names and not its sentences.

## 13. What this does not do

- It builds nothing. No file under `tools/` changes, and no store changes when it is built.
- It does not remove a heavy suite or reduce what the gate selects today. A suite that stops finding anything can be retired by a later decision with its own evidence.
- It does not write a reference of every flag. A flag is listed only where another utility passes it.
- It does not give tillr a versioned interface for readers outside this repository. [Tillr as separable parts](./tillr-separable-parts.md) owns that, and nothing here freezes a shape for them.
- It does not cover the operated repository's check declarations (`tools/gate/checks`), the rest of this repository's `tools/gate/` that [§8.1](#81-defaults-come-from-tillrs-installation) leaves in place, or the fork-target program a fork-series landing runs. Where a utility expects something from one of them, its file names it as the provider, and the pin is left to that script's own design. The two default runner scripts are covered: by `D-UC16` they are part of `tillr-gate`.
- It does not merge or divide a utility, and it does not decide whether the thirteen are the right thirteen.
- It does not rule on the pack copy under `yolo-packs/tillr/`. A `contract/` directory is part of a utility's tree and is copied with it.

## 14. Alternatives considered

| Alternative | Verdict | Why |
| :--- | :--- | :--- |
| Run every consumer's whole suite on every provider change | Rejected | The ruling excludes it: the integration suite stays the thing that finds the defect, and the view's suite takes minutes. It only moves the finding earlier, not into the provider |
| One schema language for every record, with generated types in each language | Rejected | It pins shapes and nothing else. Two of the three shapes of [§3](#3-the-failures-this-answers) are not a shape of record: one is a sentence, and one is a new read meeting a hand-made table. It also adds a generator to every build |
| Consumer-written contract tests that run in the provider's suite | Rejected in part | The provider's suite failing is kept. The consumer writing the example is not: a consumer's idea of the provider's output is the stale copy again. Here the consumer writes an expectation by name and the provider writes the example |
| Consumer tests regenerate their inputs by running the provider's binary | Rejected | It needs built binaries in a unit suite, which is slow and makes the view's suite build every module. It also fails in the consumer, not the provider |
| Put all thirteen in one module with one suite | Rejected | The ruling says they are separate utilities with defined APIs |
| Records and commands only, no Go package as contract, starting now | Left to the owner | It is option B of [OQ-UC1](#OQ-UC1). It is the cleaner line and the larger build |
| Write the contracts as prose only | Rejected | A document nothing checks goes stale, which is why [the distribution reference](../reference/tillr-distribution.md) bans it for flags |

## 15. Risks

- **The inventory is wrong somewhere.** It was read from the tree once, by hand. Every slice measures its pair again before it builds, and the contract check's scan is the real inventory from slice 5 on.
- **A contract covers only what is written in it.** Consumer suites will still find things first. [§7.3](#73-when-something-else-still-finds-it-first) makes each of those add an example, so the gap closes where it is found; it does not close in advance.
- **A lane gated before a contract change can land after it.** The squash merges cleanly, and the consumer reads an example that has moved. The after-merge proof finds this. Closing it would mean gating on the tip, which [landing principles](./landing-principles.md) already weighs and this design does not reopen.
- **Examples become a second thing to maintain.** They are regenerated by one command and never typed, and an example nobody reads is reported by the contract check. The risk that remains is their number: the floors of [§6](#6-contract-examples) could produce hundreds per utility, and the 5-second budget is unmeasured.
- **Private sentences lose a check someone wanted.** A view test that compares the remedy's text is also checking that the remedy makes sense. That check belongs to the drain, which writes the sentence, and slice 1 moves it there and does not delete it.
- **The list of allowed exceptions never shrinks.** A ratchet with 58 entries can sit for months. Each slice's "done when" is an id leaving the list.
- **A linked package still couples builds.** While the view compiles the drain's packages, a compile error in the drain stops the view's suite whatever the contract says. That is the cost of option A of [OQ-UC1](#OQ-UC1), and it is why the question is asked.

## Open Questions

1. 💬 **OQ-UC1: May one utility's contract include a Go package that another utility links, or only commands and records?**

   A worker changes how the drain decides that an item is held. Today the view shows that decision in two ways: it runs `tillr-drain why` and reads the JSON, and it also links fourteen of the drain's packages and calls them in its own process. With the contracts written, both paths are listed and pinned by the drain's own tests. The choice is whether the second path stays. [The common contract](./board-utilities.md#51-the-common-contract) says reads cross a boundary as records, and [tillr as separable parts](./tillr-separable-parts.md) says Go packages are not designed to be a contract; the tree links packages across utilities all the same.

   - **A — A listed package may be contract.** The fourteen packages stay linked, each with a list of the names the view may use. You see no change in how the view works. A compile error in the drain still stops the view's build, and a change to a listed function's meaning is caught by the drain's examples. The module cycle between land and the drain is still removed.
   - **B — Commands and records only.** Each of the fourteen packages becomes a face of `tillr-drain` that prints a record, as `why` already does, and the view links no other utility. You see the view start one process more for some pages. The view can then be built and tested with no other utility's source, which is what "separate utilities" means at its strictest. It is about fourteen more faces and their examples, built after slice 5.
   - **C — B for the view only.** The view, which is a reader, links nothing. The drain, which drives the others, keeps linking the item, run and land packages by listed names.

   <!-- vantage: question id=OQ-UC1 leaning="A (A listed package may be contract) — the pins are the same either way, so slices 1 to 5 build under A and lose nothing if you later choose B or C." -->

   _Leaning:_ A (A listed package may be contract) — the pins are the same either way, so slices 1 to 5 build under A and lose nothing if you later choose B or C.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

`R-UC1` is the owner's ruling. Every `D-UC` row is the designer's decision, which the owner may overturn. `D-UC16` and `D-UC17` are the designer's decisions made from operator requests, not owner rulings, and the owner may overturn them too. Nothing is built, so the last column is empty throughout.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| R-UC1 | **Owner ruling.** A change that breaks the view's suite has changed an interface that is written down; the contract cannot be broken while the unit tests pass; unit tests exist so that integration tests are never the ones that find something, and stay fast; every tillr utility has a defined API and a statement of what it expects from others | 2026-11-21, owner | [§1](#1-the-ruling) | — |
| D-UC1 | A utility is a directory under `tools/` whose name starts with `tillr`, so there are thirteen contracts, and a part with its own suite follows the same rules | 2026-11-21, designer | [§2.1](#21-the-thirteen-utilities), [§4](#4-the-six-rules) | — |
| D-UC2 | A contract has two lists, provides and expects, kept in the utility's own tree; an entry's consumers are computed from the expectations that name it | 2026-11-21, designer | [§4](#4-the-six-rules), [§7.1](#71-the-steps) | — |
| D-UC3 | Eight kinds of entry, each with one kind of pin in the provider | 2026-11-21, designer | [§5](#5-what-a-contract-holds-and-how-each-kind-is-pinned) | — |
| D-UC4 | Behavior is pinned by contract examples the provider writes from real code; a consumer's tests read them and never type another utility's row, record or sentence. **Rejected:** consumer-written examples, a schema language, and regenerating inputs from built binaries | 2026-11-21, designer | [§6](#6-contract-examples), [§14](#14-alternatives-considered) | — |
| D-UC5 | Names are contract and sentences are private; standard error is never contract | 2026-11-21, designer | [§4](#4-the-six-rules), [§5](#5-what-a-contract-holds-and-how-each-kind-is-pinned) | — |
| D-UC6 | Any change to an example is a contract change, including an added field; there is no judgment of compatibility | 2026-11-21, designer | [§5](#5-what-a-contract-holds-and-how-each-kind-is-pinned) | — |
| D-UC7 | A table is private unless listed; listed tables are read as plain rows; derived reads go through a reader the owner provides; nobody but the owner writes | 2026-11-21, designer | [§5](#5-what-a-contract-holds-and-how-each-kind-is-pinned) | — |
| D-UC8 | A contract changes in one commit with every consumer it names, and the gate selects consumers' contract tests on a changed `contract/` path and on nothing else for this reason | 2026-11-21, designer | [§7.1](#71-the-steps) | — |
| D-UC9 | One contract check in the conformance suite, with a list of today's reach-ins that may only shrink | 2026-11-21, designer | [§7.2](#72-the-contract-check) | — |
| D-UC10 | When a consumer suite or a heavy check still finds something first, the repair adds an example to the provider before anything else | 2026-11-21, designer | [§7.3](#73-when-something-else-still-finds-it-first) | — |
| D-UC11 | Which of each utility's surface is contract and which is private, as each of the thirteen files states. **Amended by `D-UC16`** for who provides the default gate runners and where four defaults resolve, and **by `D-UC17`** for porting the roller and its tests from Python to Go | 2026-11-21, designer; amended 2026-11-25 | [§8](#8-the-thirteen-contracts) | — |
| D-UC12 | The 58 reach-ins of [§10](#10-reach-ins-today) are defects, each with the fix named | 2026-11-21, designer | [§10](#10-reach-ins-today) | — |
| D-UC13 | The fourteen slices and their order: the four pairs of [§3](#3-the-failures-this-answers), then the check, then the pipeline, then the scripts | 2026-11-21, designer | [§11](#11-what-i-would-build-in-order) | — |
| D-UC14 | The time limits (5 seconds for a utility's contract tests, 1 second for the check) are first guesses to be replaced by slice 1's measurement | 2026-11-21, designer | [§7.5](#75-limits-and-defaults) | — |
| D-UC15 | Until [OQ-UC1](#OQ-UC1) is answered, a Go package may be an entry, and slices 1 to 5 build that way | 2026-11-21, designer | [§5](#5-what-a-contract-holds-and-how-each-kind-is-pinned) | — |
| D-UC16 | **From an operator request; the designer's decision, not an owner ruling.** The loop's defaults come from tillr's installation and never from the operated repository's `tools/`: the brief template and the adapters directory from `tillr-run`, the fork gate from `tillr-drain`, the change gate and the fast tree check from `tillr-gate`, whose tree the runner scripts move into. A consumer repository owns only its declared checks under `tools/gate/checks`. An explicit flag still wins, and a missing installed default is a refusal. **Rejected:** consumer copies, a fallback to `<root>/tools/`, shipping the declarations in the pack, a fourteenth utility | 2026-11-25, designer, from an operator request | [§8.1](#81-defaults-come-from-tillrs-installation) | — |
| D-UC17 | **From an operator request; the designer's decision, not an owner ruling.** `tools/tillr-roll/tillr-roll.py` and `tests/tillr-roll.test.py` are ported to Go with identical CLI, file, event and environment behavior, and the Python files are deleted (`tillr-keep` already moved to `tillr-drain keep`). Settling `tillr-watch.retire` ignores only `os.ErrNotExist` when unlinking the marker and restores both `tillr-watch` and `tillr-watch.retire` on any marker- or backup-unlink error, as pinned by `TestNamedRollWatchInstallation` in `tools/tillr-drain/drain/watch_roll_test.go`. **Rejected:** discarding non-`os.ErrNotExist` marker-unlink errors, dropping the unlink-failure test case, keeping Python tests | 2026-11-25, designer, from an operator request | [§8.2](#82-the-roller-moves-to-go) | — |
