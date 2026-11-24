---
title: "Contract: tillr-store"
status: in-review
stage: DESIGN
tags: [tillr, contract, store]
summary: "What tillr-store provides and expects: eight faces that create, check, recover and inventory the store, three record kinds, and five exit codes. It owns no table. It expects the item, run, land and drain packages to register their tables and kinds when loaded, and it types their table, ref and file names in its inventory."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-store

**Status:** The contract examples and their checks are not built: `fd -t d '^contract$' tools` finds nothing. The lists below describe what the code provides and reads. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** One Go program at `tools/tillr-store`. It is the operator's hands on the store file. The store's own code is in [the library](./tillr.md); this program wraps it in faces.

## 1. What it provides

### 1.1 Faces

| Face | Flags | What it prints |
| :--- | :--- | :--- |
| `init` | `--store`, `--fixture`, `--definition FILE` | `store.report` |
| `seed` | `--store`, `--input BUNDLE` | `store.report` |
| `check` | `--store` | `store.report` |
| `recover` | `--store` | `store.report` |
| `events` | `--store`, `--since N`, `--output FILE` | `store.report`; the file holds a JSON array of events |
| `checkpoint` | `--store`, `--truncate` | `store.report` |
| `definition` | `--store`, `--set FILE` | `store.definition` |
| `inventory` | `--root`, `--json` | `store.inventory` with `--json`; tab-separated rows without |

| Exit | Meaning |
| :--- | :--- |
| 0 | Done |
| 1 | A negative answer: `check` found damage, `recover` found nothing to do, `checkpoint` was refused because a loop owns it |
| 2 | Usage: a missing flag, an unknown face, a definition that drops a state items hold, seeding a store that is not a fixture |
| 3 | The store's schema version is not the one this build supports, or the store stayed locked |
| 5 | Internal |

A face with a decision prints its record even when it exits nonzero.

### 1.2 Go packages

None. It is a `main` package.

### 1.3 Records

| Record | Payload fields |
| :--- | :--- |
| `store.report` | `action schema_version supported_record_versions event_head_seq table_counts fixture integrity`, and by face `since event_count next_since journal_found log checkpoint` |
| `store.definition` | `definition version content sha256 default` |
| `store.inventory` | `rows` (`part name grain count bytes description`), `lane_totals`, `totals`, `largest_runs` |
| the `events` file | an array of `seq kind object_type object_id revision payload at` |

It registers no event kind. `store.report` is deliberately not an event.

### 1.4 Tables

None. `init` creates every table the linked owners registered.

### 1.5 Files

It writes the store file and its companions, and the file named by `events --output`.

### 1.6 Names others match on

The face names, which the library's recovery and checkpoint messages print as remedies (`tillr-store recover --store …`, `tillr-store checkpoint`). No other utility runs `tillr-store` in production code: `rg -n '"tillr-store"' tools -g '*.go' -g '!*_test.go' -g '!tools/tillr-store/**'` finds only two lists of program names in the drain.

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| The store opener, fixtures, checkpoint, recovery, the log reader; the definition; the envelope and exit codes | [tillr](./tillr.md) | Go import | throughout |
| That loading the `item` package registers the item tables, columns, event kinds and the `item.record` materializer | [tillr-item](./tillr-item.md) | Go import, for no name | `main.go` |
| The same for the `run` package and `run.record` | [tillr-run](./tillr-run.md) | Go import, for no name | `main.go` |
| The same for the `land` package | [tillr-land](./tillr-land.md) | Go import, for no name | `main.go` |
| The same for the `drain` package and the packages it links | [tillr-drain](./tillr-drain.md) | Go import, for no name | `main.go` |
| Table names, to describe them: typed names of the item, run and land utilities' tables | each owner | SQL (`count(*)` per table) | `inventory.go` |
| The ref groups `refs/tillr/runs` and `refs/tillr/preserved` | [tillr-drain](./tillr-drain.md) | `git for-each-ref` | `inventory.go` |
| The run directory's file names, typed, and the lane directory | [tillr-drain](./tillr-drain.md), [tillr-run](./tillr-run.md), [tillr-trace](./tillr-trace.md) | files | `inventory.go` |
| In its schema test: the drain's `loop` flags, and that the drain, view, run and item programs build | each of the four | subprocess | `init_schema_test.go` |

Without the four imports, `init` would make a store with three tables, and every other utility's first read would fail.

## 3. Contract and private

**Contract:** the eight faces and their flags; the five exit codes with the meanings above; the three record kinds with the payload fields listed; the `events` file's seven fields and that `next_since` is the cursor; that `init` makes every table of every owner.

**Private:** everything on standard error; the tab-separated inventory; the inventory's description sentences.

## 4. How the contract is pinned

In `tools/tillr-store`, in its fast suite, in process.

- **Exists.** `TestTheReportPayloadCarriesExactlyTheContractKeys`, `TestCheckReportsEveryContractField`, `TestEveryFaceRefusesAnIncompatibleSchema`, `TestSeedRefusesALiveStore`, `TestEventsWritesTheSeqSoTheCursorAdvances`, `TestHelpIsTheFaceSurface`.
- **To add.** One example per record kind and face, compared whole. The face and flag list as a file compared with `--help`. One example per exit code.
- **"Init makes every table".** `TestInitSchemaMatchesEveryOwnerAndTheLoop` pins this by building four other programs. The contract replaces it: each owner's contract lists its tables, and one in-process test compares `init`'s schema with the union of the lists. It needs no other program.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- RI-42: four packages imported only for what loading them registers.
- RI-43: the inventory types other utilities' table, ref and file names, and the schema test builds other utilities' programs and passes the drain's flags.
