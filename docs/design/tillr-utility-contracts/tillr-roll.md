---
title: "Contract: tillr-roll"
status: in-review
stage: DESIGN
tags: [tillr, contract, roll]
summary: "What tillr-roll provides and expects: two Python scripts, the roller that builds and installs new binaries and the keeper that keeps the loop running. Other utilities read almost nothing from them. They expect four faces of the drain and the view, two printed lines matched character for character, one event kind read and one written with raw SQL, and the `landings` table."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-roll

**Status:** The contract examples and their checks are not built: `fd -t d '^contract$' tools` finds nothing. The lists below describe what the code provides and reads. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** Two Python scripts at `tools/tillr-roll`:

- **The roller**, `tillr-roll.py`: builds every tillr program from a commit, tests what the landing did not prove, installs the programs, and asks the running drain and view to restart onto them.
- **The keeper**, `tillr-keep`: starts the drain's loop, restarts it when it exits, and stops or rebuilds it on request.

Their tests are outside the directory, in `tests/tillr-roll.test.py` and `tests/tillr-keep.test.py`.

## 1. What it provides

### 1.1 Faces

| Command | Flags | Exit |
| :--- | :--- | :--- |
| the roller | `--no-request`, `--commit <rev>`, `--pi`, `--pi-extensions` | 0 rolled; 1 a role refused, a program does not match, or any failure |
| the keeper | `--root`, `--stop`, `--restart`, and after `--` the loop's `--policy` and `--lanes` | 0; 2 on a handled error |

The drain runs the roller after a landing, through the command `.tillr.toml` names (`just tillr-roll --no-request`), and adds `--pi` when a landing touched the agent program.

### 1.2 Events

It inserts one event kind, `drain.roll-failed`, which the drain's `alarms` package registers and reads: `commit step detail`, and `mover` set to `operator` when the step is `store check`.

### 1.3 Go packages, tables

None.

### 1.4 Files

| Place | Also written by | Read by |
| :--- | :--- | :--- |
| The installed programs under the live directory | — | everything |
| `tillr-roll.commit` beside the live directory | drain | the roller |
| A staging directory named `.tillr-schema-roll-<digest>` holding the new programs and `roll.json` (`commit`, `store`) | — | drain |
| `<store>.roll-alarm.json`: `commit step detail mover ts`, written when the event cannot be inserted | — | drain |
| `tillr-watch.retire`, `tillr-roll.lock` | — | the roller |
| `.tillr/keeper.lock`, `.tillr/keeper.launch.json`, the keeper's log | — | the keeper |

### 1.5 Names others match on

- What the roller prints on failure, which the drain reads back to name a failed test: a line starting `tillr-roll: `.
- The step name `store check` and the mover `operator`.

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| `loop --check` with `--root --store --gate --land --verdict`: exit 3 for a store the new build cannot open, and the printed line `tillr-drain: schema migration required` | [tillr-drain](./tillr-drain.md) | subprocess | the roller |
| `restart --root --wait`: exactly two printed lines, the second starting with the first and holding `restarted` or `refused` | [tillr-drain](./tillr-drain.md), [tillr-view](./tillr-view.md) | subprocess | the roller |
| `loop` with `--root --gate --land --verdict --policy --lanes` | [tillr-drain](./tillr-drain.md) | subprocess | the keeper |
| `stop --root`: exit 0, or 2 when no loop runs | [tillr-drain](./tillr-drain.md) | subprocess | the keeper |
| The loop's process: a name `tillr-drain` or `tillr-drain-<hex>`, the argument `loop`, and `--root`, `--policy`, `--lanes` among its arguments | [tillr-drain](./tillr-drain.md) and the pack's launcher | `/proc` | the keeper |
| The event `drain.build-started`: `role revision digest modified` | [tillr-drain](./tillr-drain.md) | SQL, with `sqlite3` | the roller |
| The `events` table accepting an insert of `ts kind object_type object_id payload` | [tillr](./tillr.md) | SQL, with `sqlite3` | the roller |
| `landings`: `trunk_after result gated_tree landed_tree`, and the result `landed` | [tillr-land](./tillr-land.md) | SQL, with `sqlite3` | the roller |
| `refs/tillr/green`, the last proved trunk commit | [tillr-drain](./tillr-drain.md) | Git | the roller |
| `TILLR_ROLL_VERIFICATION` in its environment | [tillr-drain](./tillr-drain.md) | environment | the roller |
| The adapter's path, and its three faces as program words | [tillr-loop](./tillr-loop.md) | files | both |
| Nine modules that build: `cli drain gate item land run store view watch`; and the view's build tag | each utility | `go build` | the roller |
| Each program answering `--help` with exit 0 | each Go utility | subprocess | the roller |
| The exit code 3 meaning incompatible | [tillr](./tillr.md) | a copy | the roller |
| Lines to drop from a failed suite's output: three trace prefixes | [tillr-drain](./tillr-drain.md), [tillr](./tillr.md) | a copy | the roller |
| The shard runner's failure lines and the list of modules it shards | the repository's test scripts, outside tillr | subprocess, a copy | the roller |

## 3. Contract and private

**Contract:** the roller's four flags and two exit codes, because the drain runs it; the `drain.roll-failed` payload and the fallback file's fields; `roll.json`'s two fields and the staging directory's name form; `tillr-roll.commit`.

**Private:** everything either script prints; the keeper's files; the lock; how suites are chosen.

The one printed thing the drain reads, the failing test's name, becomes a field of the failure event's `detail` in place of a line it searches for.

## 4. How the contract is pinned

In `tests/tillr-roll.test.py` and `tests/tillr-keep.test.py`. Those two files move into `tools/tillr-roll` so that the utility's pins live in it; that is a move and no change of content.

- **Exists.** Both suites run without a real store, loop, gate or agent. They type an `events` table of six columns, a `landings` table of four, and a `drain.build-started` payload.
- **What it provides.** One example of the failure event and one of the fallback file, made by `record_failure`; one of `roll.json`, made by the staging function. The drain's `alarms` and schema-roll tests read them.
- **The write goes through the owner.** The roller stops inserting into `events`. It runs a face the drain provides that records a roll failure, so the kind's owner writes its own kind. The fallback file stays for a store that cannot be opened at all.
- **What it expects.** The typed tables are replaced by a seed store the library, the drain and land produce from their builders. The schema line, the two-line restart answer and `stop`'s exit codes are read from the drain's examples of those faces, and the stand-in `tillr-drain` in the keeper's suite prints from them.
- **Names in place of lines.** `loop --check` answers with exit 3 alone; the line beside it becomes private. `restart --wait` gains `--json`, and the roller reads the answer field.
- **The list of programs** is read from the dispatcher's noun file.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- As consumer: RI-51 (reads `drain.build-started` and `landings` with `sqlite3`, and inserts an event without the event log's write path), RI-52 (the roller and the drain match each other's printed lines), RI-53 (file names typed in both; exit code 3 copied), RI-54 (the keeper finds the loop under `/proc` and accepts two exits from `stop`), RI-44 (its own list of programs), RI-58 (its suite types other utilities' DDL).
