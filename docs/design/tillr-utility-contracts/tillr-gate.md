---
title: "Contract: tillr-gate"
status: in-review
stage: DESIGN
updated: 2026-11-25
tags: [tillr, contract, gate]
summary: "What tillr-gate provides and expects: seven faces on HEAD (plus the `shards` face under `D-UC18` and the `go-affected` face under `D-UC19`), four record kinds, seven exit codes with the stall code 6, and the remembered-results directory. Its record types are in a `main` package nobody can import, so five utilities each hold a copy. It expects the check declarations and the registry from the operated repository. By `D-UC16` the default runner scripts are to become its own, by `D-UC18` `tools/go-test-shards.py` and its Python test suites move into `tillr-gate shards`, and by `D-UC19` `tools/gate/go_affected.py` and `tests/go-affected.test.py` move into `tillr-gate go-affected`."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-gate

**Status:** What it provides and what it expects describe the code as it is. The split into contract and private, the pins and the reach-in fixes are proposed and not built. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** One Go program at `tools/tillr-gate`. On `HEAD` today it has seven faces: it derives what a candidate changed, plans which checks that change needs, runs them, and writes a verdict. It opens no store. By `D-UC18` ([§8.3 of the overview](../tillr-utility-contracts.md#83-the-go-test-shard-runner-moves-to-go)) and `D-UC19` ([§8.4 of the overview](../tillr-utility-contracts.md#84-the-affected-packages-calculator-moves-to-go)) — the designer's decisions from operator requests, which the owner may overturn — `tools/go-test-shards.py` and its Python test suites move into `tools/tillr-gate/shards` as `tillr-gate shards`, and `tools/gate/go_affected.py` and `tests/go-affected.test.py` move into `tools/tillr-gate/goaffected` as `tillr-gate go-affected`.

## 1. What it provides

### 1.1 Faces

| Face | Flags others pass | What it writes | Run by |
| :--- | :--- | :--- | :--- |
| `change` | `--item --lane --run --worktree --base --head --now --output` | `gate.change` | `tillr-loop` |
| `plan` | `--registry --input --output --root` | `gate.plan` | `tillr-loop` |
| `run` | `--input --worktree --output --logs --gate --gate-arg --gate-timeout --tree-overrun` | `gate.verdict`, written before it exits | `tillr-loop` |
| `checks` | `--base --head --worktree --checks --logs`, one of `--fast-tree` or `--heavy-tree`, and `--scope-to-change --only --tree-overrun` | `gate.verdict` on standard output | the drain (heavy tree), the repository's gate scripts (fast tree) |
| `shadow` | `--input --worktree --output --logs` | `gate.shadow` | nobody in the tree |
| `classify` | `--aggregate --repo --store --prove-disabled`, with a JSON array of check observations on standard input | the classifications on standard output, in order | `tillr-loop`, the repository's gate scripts |
| `enable` | `--check --episode --worktree --store` | the result store's entry for the named check | people |

| Exit | Meaning |
| :--- | :--- |
| 0 | The verdict is a pass |
| 1 | The verdict is a fail |
| 2 | Usage, or malformed input |
| 3 | Incompatible: the plan's registry digest does not match |
| 4 | It could not judge. Never a failed check |
| 5 | Internal |
| 6 | A check stalled |

Under `D-UC18` of [the overview](../tillr-utility-contracts.md#83-the-go-test-shard-runner-moves-to-go) (the designer's decision from an operator request, not yet built), `tillr-gate` gains an eighth face, `shards`, porting `tools/go-test-shards.py` with identical flags (`<module>` in any order with flags, `--edge-digest`, `-k`/`--shards`, `--cache`, `--durations`, `--record`, `--events`, `--scratch`, `--timeout`, `--shard-timeout`, `--progress-file` and `--progress-interval`), exit codes (`0` on pass, `--edge-digest` or `--help`; `1` on shard failure, lane `--cache` refusal or runtime error; `2` on usage or missing scratch configuration; `128 + signum` on `SIGINT`/`SIGTERM`), `go-test-shards: ` summary and failure lines, and JSON output files, run by `.justfile`, `tools/gate/checks/go-test-tillr-*.json`, `tillr-roll` and developers.

Under `D-UC19` of [the overview](../tillr-utility-contracts.md#84-the-affected-packages-calculator-moves-to-go) (the designer's decision from an operator request, not yet built), `tillr-gate` gains a `go-affected` face, porting `tools/gate/go_affected.py` with identical flags (`--root`, `--base`, `--head`, `--changed`, `--drop-module`, `--select-only`, `--evidence-out`, `--verdicts-in`, `--verdicts-out` and `--invocation-guard`), exit codes (`0` on pass, `--select-only`, zero affected packages or `--help`; `1` on test failure or runtime error; `2` on usage error; `6` on binary stall; `128 + signum` on signals), `go-affected: ` progress, summary and failure lines, and JSON evidence and package-verdict files, run by `tools/gate/checks/go-affected.json`, `tillr-drain` base replay (`replay_go_affected.go`) and developers.

### 1.2 Go packages

None on `HEAD` today: every type below is in a `main` package and cannot be imported. Under `D-UC18` ([§8.3 of the overview](../tillr-utility-contracts.md#83-the-go-test-shard-runner-moves-to-go)) and `D-UC19` ([§8.4 of the overview](../tillr-utility-contracts.md#84-the-affected-packages-calculator-moves-to-go)) — the designer's decisions from operator requests, not yet built — `tools/tillr-gate/shards` (`github.com/mschulkind-oss/tillr-gate/shards`) holds the sharded Go test runner and its ported Go test suite, and `tools/tillr-gate/goaffected` (`github.com/mschulkind-oss/tillr-gate/goaffected`) holds the affected-packages calculator and runner plus its ported Go test suite, while the gate's record types remain governed by [Section 3](#3-contract-and-private).

### 1.3 Records

| Record | Payload fields |
| :--- | :--- |
| `gate.change`, with the id `change-<run>` | `head_sha base_sha tree_sha changed_paths subject` |
| `gate.plan` | `plan_id base_sha tree_sha registry_digest checks in_process`. A check has `check_id command timeout_ms reads writes depends_on kind label env drop needs tier exclusive exclusive_reason forced`, and optionally `declaration_digest scope budget_ms tools remember` |
| `gate.verdict` | `verdict_id plan_id registry_digest base_sha tree_sha result checks inherited_failures`. A check has `check_id result duration_ms cpu_time_ms log_ref`, and optionally `budget_ms over_budget_ms start_unix_nano end_unix_nano last_output_line missing_needs memory fingerprint pass_time fail_time unbound_path cpu_source silent_samples watchdog_unavailable`. An inherited failure has `check_id tip_sha log_ref output` |
| `gate.shadow` | its own |

The gate registers these four names as event kinds and appends none of them. The events named `gate.plan` and `gate.verdict` are appended by the drain, with nine fields of its own beside the gate's.

### 1.4 Tables

None.

### 1.5 Files and environment

| Place | Read by |
| :--- | :--- |
| The record files, written where `--output` says; by convention `.tillr/loop/<run>/gate.change.json`, `gate.plan.json`, `gate.verdict.json` | land, drain, view, `tillr-trace` |
| `<logs>/<check>.log` and `.triage.log`, named by a check's `log_ref` | drain |
| `<git common directory>/tillr-gate/results/<key>.<verdict>.json`, the remembered results | the repository's gate scripts |
| `TILLR_FAST_VERDICT_OUT`, which it sets for the `--gate` program and reads back | the gate scripts; the drain also sets it |
| Under `D-UC18` (not yet built), `TILLR_GO_SHARDS_PROGRESS_OUT` / `--progress-file`, `TILLR_GO_SHARDS_EVIDENCE_OUT`, `--events` and `--record` written by `shards` | drain (`JudgeHeavy` and `runHeavyLegacy`), `go_affected.py` (or `goaffected` under `D-UC19`) cross-tool cache tests, callers |
| Under `D-UC19` (not yet built), `TILLR_GO_AFFECTED_EVIDENCE_OUT` / `--evidence-out`, `TILLR_GO_VERDICTS_IN` / `--verdicts-in`, `TILLR_GO_VERDICTS_OUT` / `--verdicts-out` and `TILLR_GATE_EDGE_INPUTS` read and written by `go-affected` | `change-gate.py`, `fork-gate.py`, `goevidence.go`, drain base replay (`replay_go_affected.go`), `TestEdgeInputs` helpers |

### 1.6 Names others match on

- **Verdict results.** `pass`, `fail`, `error`, `stalled`, `pass-inherited`.
- **Check results.** Those five, and `unavailable`, `inherited`, `over-budget`, and the one sentence `skipped: no changed path is read`.
- **The stall exit code** 6, and the rule for a gate program's exit: 0 pass, 1 fail, 6 stalled, anything else an error.
- **The check id of the gate program:** `gate:<program name>`.

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| The envelope, the exit codes, the kind registry | [tillr](./tillr.md) | Go import | throughout |
| The check declarations: each direct `*.json` under `tools/gate/checks`, with the keys `id label command scope tier budget_ms timeout_ms reads tools needs remember writes exclusive` and no others | the operated repository, outside tillr | files | `checkfiles.go` |
| The registry: `digest declarations in_process`, with the digest being the hash of the two arrays | the operated repository, outside tillr | files | `registry.go` |
| The `--gate` program: its exit code, and the fast verdict it writes to the path in `TILLR_FAST_VERDICT_OUT` | the caller's program; in the drain's loop, the change gate, `change-gate.py`, which `D-UC16` makes this utility's own part | subprocess, files | `gate.go` |
| `TILLR_HEAVY_LOCK_FD`, an open lock handle | [tillr-drain](./tillr-drain.md) | environment | `checkgroup.go` |
| An inherited `TILLR_FAST_VERDICT_OUT` | [tillr-drain](./tillr-drain.md) | environment | `gate.go` |
| That the fast tier's limit, 9000 ms, equals the limit in `tools/gate/checks/gate-budget.py` | this repository's declared checks | a test | `run.go` |

It reads no store, imports no other utility, and runs no other tillr program.

**The runner scripts.** Wherever this file says "the repository's gate scripts" as a reader or a caller, it means `change-gate.py`, `tree-check.py` and the modules they import, which are under `tools/gate/` today. `D-UC16` of [the overview](../tillr-utility-contracts.md#81-defaults-come-from-tillrs-installation) moves them into this utility's tree, so they are delivered with it and a consumer repository keeps no copy. It is the designer's decision, made from an operator request, and the owner may overturn it. They then become a **part** of this utility, and what they read of the Go program (the `checks` and `classify` faces, the remembered results, `TILLR_FAST_VERDICT_OUT`) is a read between two parts. The check declarations stay the operated repository's own. Nothing has moved yet.

## 3. Contract and private

**Contract:**

- `change`, `plan`, `run` and `checks` with the flags above, and the seven exit codes.
- Under `D-UC18` (the designer's decision from an operator request, which the owner may overturn), `shards` with its flags and positional/flag ordering, exit codes (`0`, `1`, `2` and `128 + signum`), refusal to choose a scratch directory itself, refusal of `--cache` in a lane (`TILLR_RUN_ID`), child process-group lifetime guard, `go-test-shards: ` summary and shard-failure lines read by the drain and the roller, and JSON shapes for `--events`, `--record`, `--progress-file` / `TILLR_GO_SHARDS_PROGRESS_OUT` and `TILLR_GO_SHARDS_EVIDENCE_OUT`.
- Under `D-UC19` (the designer's decision from an operator request, which the owner may overturn), `go-affected` with its flags, exit codes (`0`, `1`, `2`, `6` and `128 + signum`), `MAX_SHARDS = 2 * runtime.NumCPU()` (`12` when undetermined) and single-test shard `GOMAXPROCS=1` / `-p 1`, explicit `git` lookup against the shim-bypassed `PATH` in `cmd.Env` with `WritePrivateGitconfig` error propagation, Linux `PR_SET_CHILD_SUBREAPER` lifeline guard, binary stall watchdog, `go-affected: ` progress/summary/failure lines read by the drain, and JSON shapes for `--evidence-out` / `TILLR_GO_AFFECTED_EVIDENCE_OUT` and `--verdicts-in` / `--verdicts-out` (`TILLR_GO_VERDICTS_IN` / `TILLR_GO_VERDICTS_OUT`).
- `gate.change` whole. Of `gate.plan`: `plan_id base_sha tree_sha registry_digest`, and per check `check_id reads kind label tier`. Of `gate.verdict`: all eight top-level fields; per check `check_id result duration_ms budget_ms over_budget_ms log_ref`; the inherited failure whole.
- The names of [§1.6](#16-names-others-match-on).
- The remembered-results directory and its file name form, because the gate scripts read it.
- The declaration keys and the registry shape, as what the gate accepts.

**Private:**

- The other check fields of the plan and the verdict; `gate.shadow`.
- Everything `run` prints on standard error: the summary line, the `unavailable` and `remembered pass` lines.
- The watchdog, the budgets' arithmetic, the Go environment memory.

**How the types reach consumers.** Under option A of [OQ-UC1](../tillr-utility-contracts.md#OQ-UC1) the record types move to a small package `tools/tillr-gate/gate` that holds only types, names and decoders, and land, the drain and the view import it. Under option B they stay where they are and consumers read the examples. Either way the copies go.

## 4. How the contract is pinned

In `tools/tillr-gate`, in its fast suite.

- **Existing tests.** `TestThePlanFaceIsConformant`, `TestTheRunFaceIsConformant`, `TestTheGateProgramsExitIsTheVerdict`, `TestRunRecordsAFailedCheckAsAVerdict`, `TestRunExitsFourForACheckThatCannotStart`, `TestTheChangeFaceDerivesTheCandidatesRecord`.
- **Examples.** One `gate.change`, one `gate.plan`, and one `gate.verdict` per result: pass, fail, error, stalled, pass-inherited, and one with an unavailable check. Each is made by the face in process over a fixture registry whose checks are `true` and `false`, and compared whole with the file. Land's hand-built verdict and the trace's hand-typed plan are replaced by these.
- **Exit codes.** A name-set file with the seven codes, compared with the constants. The drain, the adapter and the two gate scripts compare against it.
- **What it accepts.** A test loads every real declaration under `tools/gate/checks` and the registry example. This one reads outside the module by design: the declarations are its input, and a declaration it refuses must fail here before a drain meets it.
- **Events.** The gate keeps the record kinds. The drain's events take names of their own ([the overview's fix for RI-37](../tillr-utility-contracts.md#10-reach-ins-today)), so a program can link both, and `tillr-watch` can know them.
- **Shard runner (`D-UC18`).** Once built, Go unit tests in `tools/tillr-gate/shards` replace `tests/go-test-shards.test.py`, `tools/tillr-land/shards_test.py` and `tools/tillr-land/shards_liveness_test.go` in the same commit, keeping every case.
- **Affected-packages runner (`D-UC19`).** Once built, Go unit tests in `tools/tillr-gate/goaffected` replace `tests/go-affected.test.py` and `tools/gate/checks/go-affected-test.json` in the same commit, keeping every case (including `TestDiscoverModules`, `TestEdgeInputs` and the refusing-git-shim test).

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- As provider: RI-34 (five copies of its record types), RI-35 (land recomputes the registry digest and reads its files), RI-36 (the stall code typed in five places), RI-37 (two registrations of two kinds), RI-39 (the view models two land records nothing writes beside the gate's), RI-46 (its kinds cannot be linked by `tillr-watch`), RI-47 (the adapter types its flags).
- As consumer: RI-38 (a directory named for the gate that holds the drain's files; two environment names with no owner stated).
