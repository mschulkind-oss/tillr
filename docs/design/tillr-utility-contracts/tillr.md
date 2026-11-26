---
title: "Contract: tillr (the library)"
status: in-review
stage: DESIGN
tags: [tillr, contract, library]
summary: "What the tillr library provides to every other tillr utility and what it expects: the record envelope, the six exit codes, the event log, the store opener and its three registries, the board definition, settings, the Vantage index reader and the watch rules. It expects nothing from another utility by design, and reads four of their tables."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr (the library)

**Status:** The contract examples and the contract check are not built: `fd -t d '^contract$' tools` finds nothing. The lists below describe what the code provides and reads; the watch rows follow [the watch design](../tillr-watch.md). This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** A Go library at `tools/tillr` with no program of its own. Every Go utility links it. This contract covers seven packages others can import (`record`, `event`, `store`, `definition`, `settings`, `vantage`, `watch`) and one that holds only tests (`conformance`). The library also holds `checkresult` and `notify`, which this contract does not list yet.

## 1. What it provides

### 1.1 Faces

None. The faces that operate the store belong to [tillr-store](./tillr-store.md).

### 1.2 Go packages

| Package | What it answers | Linked by | Names they use |
| :--- | :--- | :--- | :--- |
| `record` | The envelope every record is wrapped in, and the exit codes | every Go utility except `tillr-watch` | `Envelope Producer Subject Encode Decode System SupportedVersion ErrUnsupportedVersion ErrForeignSystem`, and `ExitOK ExitNegative ExitUsage ExitIncompatible ExitExternal ExitInternal` |
| `event` | The registry of event kinds and the one way to append an event | drain, gate, item, land, run, store, view | `Register Lookup Kinds Event Append EventsDDL` |
| `store` | Opening, migrating, reading and writing the SQLite store | every Go utility except the gate and the dispatcher | Opening: `Open OpenRead OpenMigrated OpenOption WithBusyTimeout`. Transactions: `Store Reader Tx` with `ReadTx WriteTx Query QueryRow Exec Append Events EventsOfKind EventsSelected EventSelection`. Registries: `RegisterDDL RegisterColumn RegisterMaterializer`. Fixtures: `Seed InitFixture`. Upkeep: `Checkpoint CheckpointOwner CreateAndOwnCheckpoints Recover NeedsRecovery Fingerprint SchemaVersion IsBusy`. Guards: `HoldsWrite CheckIO ReportIO`. Tracing: `FlushSQLiteTraceAtExit` |
| `definition` | The item lifecycle: states, transitions, prerequisites, hooks | store, item, drain, view | `Definition ForStore ForReader Set Read Record GitLanding GitLandingName Hooks OccupiedStatesError`, and the four prerequisite names |
| `settings` | The repository's `.tillr.toml` | drain, gate (`D-UC23`), item, loop (`D-UC23`) | `Read ReadWithoutLanding Settings DefaultTrunkBranch DefaultChecksDir`, and `PushNone PushTip PushGreen` |
| `vantage` | The one decoder of the Vantage question index | drain, item, view | `ParseIndex RunIndexCommand DefaultIndexCommand IndexCommandName IndexError Question QuestionAnswered QuestionSource VantageQuestions Preloaded WithoutQuestions` |
| `watch` | Watch rules, their evaluation and their delivery | `tillr-watch` only | `LoadRules Rule Evaluator Notice Bundle` |
| `buildstamp` | The binary's build stamp and embedded `SupportRevision` (`D-UC25` of [the overview](../tillr-utility-contracts.md#810-the-loop-verifies-support-files-and-install-root-revision-at-startup), the designer's decision from an operator request, which the owner may overturn) | cli, drain (`D-UC25`), gate, item, land, loop, roll, run, store, trace, view, watch | `Read Stamp` (including `SupportRevision` under `D-UC25`) |
| `setupcheck` | Pre-start verification of adapters, workflows, and installed support-file / `support-revision` parity (`D-UC25`) | cli, drain (`D-UC25`) | `CheckAdapter CheckBriefTemplate ResolveWorkflowFile` (plus install-root support-file and `support-revision` verification under `D-UC25`) |

### 1.3 Records and event kinds

The envelope has nine fields: `record_version`, `kind`, `id`, `producer` (`system`, `utility`, `version`), `created_at`, `subject` (`kind`, `id`), `correlation_id`, `idempotency_key`, `payload`. `Decode` refuses a version other than 1 and a system other than `tillr`.

| Event kind | Owner | Payload fields |
| :--- | :--- | :--- |
| `fixture.seed` | `store` | `records` |
| `store.definition.set` | `store` | `old_sha256 new_sha256` |
| `store.judge-grants-archived` | `store` | the archived rows |
| `watch.alert.opened` | `watch` | `alert_id rule_id subject_key severity receiver opened_seq` |
| `watch.alert.acknowledged` | `watch` | `alert_id rule_id subject_key severity by note` (plus optional `item_id` when `--item` is given; [`../tillr-watch.md` Section 6.10](../tillr-watch.md#610-tracking-an-alert-cause-with-an-open-queue-item-d-tw67)) |
| `watch.alert.tracked` | `watch` | `alert_id rule_id subject_key severity item_id by note` ([`../tillr-watch.md` Section 6.10](../tillr-watch.md#610-tracking-an-alert-cause-with-an-open-queue-item-d-tw67)) |
| `watch.alert.silenced` | `watch` | `alert_id rule_id subject_key severity reason`, plus `until` or `until_change` |
| `watch.alert.reopened` | `watch` | `alert_id rule_id subject_key severity receiver cause` (plus `item_id item_state` on `tracked-item-landed` and `tracked-item-closed`) |
| `watch.alert.resolved` | `watch` | `alert_id rule_id subject_key severity resolved_seq reason` (including `reason: "item-<state>"` under [`D-TW63`](../tillr-watch.md#D-TW63), plus optional `receiver`, `note`, and `item_id` under [`D-TW67`](../tillr-watch.md#D-TW67)) |
| `watch.rule.set` | `watch` | `rule_id by` |
| `watch.rule.reset` | `watch` | `rule_id by` |

### 1.4 Tables

`events`, with its append-only triggers and two indexes; `meta`; `schema_migrations`; and for the watch rules `alert_episodes` (including `tracking_item_id` and `tracked_lag_pair` under [`D-TW67`](../tillr-watch.md#D-TW67)), `foreground_delivery`, `watch_rule_overrides` and `watch_rule_baselines` ([`D-TW66`](../tillr-watch.md#D-TW66)). `events` is read with raw SQL by the drain, the view, the item utility and land. The four watch tables have no reader outside the library and `tillr-watch` (`rg -n 'alert_episodes|foreground_delivery|watch_rule_overrides|watch_rule_baselines' tools -g '*.go' -g '*.py' -g '!tools/tillr/watch/**' -g '!tools/tillr-watch/**'` returns nothing).

### 1.5 Files and environment

| Place | Read by |
| :--- | :--- |
| The store file, and beside it `-wal`, `-shm` and `-checkpoint-owner` | every utility that opens the store; `tillr-store`'s inventory |
| The `meta` row `store.definition` | `definition` |
| `.tillr.toml` at the repository root, written by people | `settings` |
| `<install_root>/tillr/buildstamp/support-revision` (mirrored from `tools/tillr/buildstamp/support-revision` by `pack-sync` under `D-UC25`) | `buildstamp` (`//go:embed`), `setupcheck` |
| `TILLR_INSTALL_ROOT` | `setupcheck`, `tillr-run`, `tillr-loop`, `tillr-item` |
| `TILLR_SQLITE_TRACE` | the library, the gate, land, the roller |

### 1.6 Names others match on

- **Exit codes.** 0 done, 1 a negative decision, 2 usage, 3 incompatible, 4 an external failure, 5 an internal failure.
- **The system name** `tillr` and **the record version** 1; **the schema version** 6.
- **The default definition's name** `git-landing`, and the prerequisite names `fence`, `design-document`, `design-questions`, `owner-clean`.
- **A question's answered state** `answered`.

One sentence is compared by another utility, and the contract makes it private: the recovery remedy in `store/recover.go`, which the view repeats in `apierror.go` and tests for.

## 2. What it expects

The library is the bottom of the build: its `go.mod` requires no other tillr module. What it reads from others:

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| Rows of `items` (`item_id`, `state`, `seat`, `waiting_on`), to refuse a definition that drops a state items hold, evaluate the `items` watch projection, auto-close terminal item-subject episodes (`D-TW63`), and validate/reconcile item-tracked alerts (`D-TW67`) | [tillr-item](./tillr-item.md) | SQL | `definition/store.go`, `watch/tillr_source.go`, `watch/lifecycle.go`, `watch/eval.go` |
| Rows of `workflow_selections` (`item_id`, `workflow`), to evaluate `workflow:<name>` watch rule scopes (`D-TW31`) | [tillr-drain](./tillr-drain.md) | SQL | `watch/tillr_source.go` |
| `workflow_selections`, `graph_revisions`, `node_runs`, rewritten when a store moves to schema version 6 | [tillr-drain](./tillr-drain.md) | SQL | `store/judge_migration.go` |
| `item_grants`, a table of earlier schema versions, archived and dropped by the same migration | [tillr-item](./tillr-item.md) | SQL | `store/judge_migration.go` |
| The owners' registrations, made when their packages load: the library composes the schema from whatever the running program linked | every utility that owns a table or a kind | Go, at load | `store/registry.go` |
| The Vantage index command's JSON: `id state title line preference leaning blockedOn` | `vantage-check`, outside tillr | subprocess | `vantage/vantage.go` |
| For each watch rule, the kinds it names and the payload fields its text addresses | the kind's owner | SQL on `events` | `watch/pattern.go` |

## 3. Contract and private

**Contract:**

- The envelope's nine fields, the two refusals of `Decode`, and the six exit codes.
- The `event` and `store` names listed in [§1.2](#12-go-packages), and the rule that a second owner for a kind or a different DDL for an owner is refused.
- The `events` table's columns, and that it accepts no update and no delete.
- `definition`, `settings` and `vantage` by the names listed.
- The names of [§1.6](#16-names-others-match-on).
- The watch rule file format, which a rule author depends on.

**Private:**

- `meta` and `schema_migrations`, their keys, and the schema digest.
- Every error sentence, the recovery remedy included. A consumer branches on `NeedsRecovery`.
- The trace format, and the guard counters the drain's tests read (`WholeLogReads`, `EventLogReads`, `OpenWriteTransactions`). These stay exported for tests and are not listed.
- `CompatibilityJudgeGraph`, which rewrites a drain workflow from inside the library.
- `alert_episodes`, `foreground_delivery`, `watch_rule_overrides` and `watch_rule_baselines`.

`Reader.Query` and `Tx.Exec` stay, because every owner reads its own tables through them. What changes is that a query naming a table the caller does not own must match a table entry in the owner's contract.

## 4. How the contract is pinned

All pins live in `tools/tillr` and run in its fast suite.

- **Exists.** `TestTheFourContractPiecesArePresent`, `TestTheExitCodesAreTheContract`, `TestTheEnvelopeRefusesAnUnsupportedVersionAndAForeignSystem`, `TestTheKindRegistryRefusesASecondOwner`, `TestTheEventUtilitysDDLIsAppendOnly` and `TestTheStoreUtilityRegistersItsOwnContract`, and the import guard in `definition/definition_test.go`.
- **Packages.** One file per package uses every listed name with the caller's types, so a rename or a changed signature fails to compile in the library.
- **The envelope.** One example file per field arrangement, encoded and decoded by the test.
- **Tables.** The `events` columns are compared with the list; a test opens a store and reads each listed column.
- **Builders.** `store` publishes a builder that makes an empty store with every linked owner's schema, and one that appends an event through `Tx.Append`. Other utilities' builders are built on these two.
- **The contract check.** It lives in `conformance`, as [the overview](../tillr-utility-contracts.md#72-the-contract-check) describes, and reads every utility's contract directory.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- As provider: RI-40 (no typed read of any table), RI-45 (the recovery wording typed twice).
- As consumer: RI-41 (the library reads tables of utilities above it, and holds a drain workflow rewriter).
- The contract check itself answers RI-42 and RI-44, which are about lists the library should hold: which owners a program must link, and which programs exist.
