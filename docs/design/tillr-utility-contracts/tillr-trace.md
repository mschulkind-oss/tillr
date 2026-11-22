---
title: "Contract: tillr-trace"
status: in-review
stage: DESIGN
updated: 2026-11-21
tags: [tillr, contract, trace]
summary: "What tillr-trace provides and expects, read from the code at dab5a2da0e: a Python script with five subcommands, of which `run` prints the one JSON document the view reads, and a shell shim that writes one line per command an agent runs. It expects two tables of the runner and the drain, the run directory, and the gate's records, and it turns every SQL error into an empty answer."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-trace

**Status:** 2026-11-21. Read at `dab5a2da0e`; nothing here is built. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** A Python script at `tools/tillr-trace` that reads one run's logs and records into a timeline and checks what the run claimed against what was observed, and a shell file, `bash-shim.sh`, that records each command an agent's shell runs. It writes no file of its own and keeps its rows in memory.

## 1. What it provides

### 1.1 Faces

Global flags `--store`, `--loop-dir`, `--trace-root`, then a subcommand:

| Subcommand | What it prints |
| :--- | :--- |
| `run <run>` | One JSON document. For an unknown run: `{"run": null, "events": [], "claims": []}` |
| `tick`, `audit <run>`, `cost`, `discrepancies` | Text |

Exit 0, or 2 for arguments it cannot parse.

### 1.2 The `run` document

| Part | Fields |
| :--- | :--- |
| `run` | `run_id log_path log_state proc_path proc_state git_path git_state brief_path fence_path base_sha detail`, and the rest of its run row |
| each of `events` | `obs_id kind ts_start ts_end duration_ms seam span_id summary payload` |
| each of `claims` | `claim_id claim_kind claim_text observed_status discrepancy_reason observed_at` |

The event `kind` here is the trace's own word and not a store event kind: `llm.turn`, `tool.call`, `run.lifecycle`, `proc.exec`, `gate.plan`, `gate.verdict`, `tick.decision`, `unreadable`.

The view runs `tillr-trace --store <store> --loop-dir <dir> run <id>` and reads this document.

### 1.3 The shim

A shell that has `BASH_ENV` pointing at the shim and `TILLR_TRACE_DIR` set appends one JSON line per command to `$TILLR_TRACE_DIR/proc-exec.jsonl`: `kind` (`proc.exec`), `seam` (`bash-shim`), `ts ts_end duration_ms run_id lane_id pid ppid cwd repo_rel_cwd exit_code span_id`, and the command.

### 1.4 Go packages, events, tables

None. Its three tables exist only in memory.

### 1.5 Names others match on

The file name `proc-exec.jsonl`; the shim's path, which the runner's adapters compute; the script's path, which the view looks for in three places.

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| `runs`: `run_id item_id lane_id backend state exit_code started_at finished_at artifact_refs`, and that an artifact reference is `kind` and `ref` with the kind `log` for the harness log | [tillr-run](./tillr-run.md) | SQL, with `sqlite3` | `Store.runs` |
| `dispatches`: `decision_id item_id lane_id run_id action reason dispatched_at` | [tillr-drain](./tillr-drain.md) | SQL, with `sqlite3` | `_tick_decisions` |
| The run directory `.tillr/loop/<run>/`: `base_sha`, `fence.txt`, `brief.md` | [tillr-drain](./tillr-drain.md) | files | `tillr_trace.py` |
| The harness log, at the path the run's artifact reference names | [tillr-run](./tillr-run.md) | files | `tillr_trace.py` |
| The log's line format: `session`, `request_start`, `tool_execution_start`, `tool_execution_end`, `turn_end` and their fields | the agent harness, outside tillr | files | `tillr_trace.py` |
| `gate.plan.json` and `gate.verdict.json`: `plan_id verdict_id base_sha tree_sha registry_digest result`, and per check `check_id result duration_ms` | [tillr-gate](./tillr-gate.md) | files | `tillr_trace.py` |
| `TILLR_RUN_ID` and `TILLR_LANE_ID` in the shell's environment | [tillr-run](./tillr-run.md) | environment | `bash-shim.sh` |
| `TILLR_TRACE_DIR`, `TILLR_REPO_ROOT` and `BASH_ENV`, set by the adapters | [tillr-run](./tillr-run.md) | environment | `bash-shim.sh` |
| `manifest.json` in a run directory: `tick_id base_sha head_sha tree_sha` | nobody. `rg -n 'manifest\.json' tools/tillr* bin -g '!tools/tillr-view/web/**' -g '!tools/tillr-trace/test.py'` finds only this reader | files | `tillr_trace.py` |

A SQL error of any kind is caught and read as no rows, so a renamed column shows as "no store row for this run".

## 3. Contract and private

**Contract:** `run <id>` with its three flags; the document's three parts with the fields listed; the absent-run document; the shim's line fields and file name; the environment names the shim reads.

**Private:** the four text subcommands; the in-memory tables; the claim-checking rules; `--trace-root`.

Two columns of the run row, `git_path` and `git_state`, are kept only because a comment says the view reads them by name. As listed fields they are held by a test and the comment can go.

## 4. How the contract is pinned

In `tools/tillr-trace/test.py`.

- **Exists today.** The suite runs the real script and the real shim, and asserts that no file is written.
- **Examples.** The `run` document for three situations, written as files by the suite: a full run from the checked-in canary log, a run with no log, an unknown run. The view's `trace_test.go` reads these and stops running the script.
- **The shim's line.** One example line made by running the shim, compared for its field names. The view's second parser of `proc-exec.jsonl` goes; it reads the document.
- **Its expectations.** The hand-typed DDL for six tables in `test.py` is replaced by a seed file the runner and the drain produce from their builders: a small store holding one finished run and one dispatch. The gate inputs are the gate's plan and verdict examples.
- **Errors.** One test renames a column in the seed store and requires a refusal that names the column, and not an empty answer.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- As consumer: RI-55 (raw SQL on `runs` and `dispatches`, with every error read as empty), RI-56 (reads a manifest nothing writes), RI-34 (its own reading of the gate's records), RI-58 (its suite types other utilities' DDL).
- As provider: RI-18 (the adapters find the shim by a relative path), RI-57 (the view decodes the document with an unnamed struct and parses the shim's file again).
