---
title: "Contract: tillr-gate"
status: in-review
stage: DESIGN
updated: 2026-11-28
tags: [tillr, contract, gate]
summary: "What tillr-gate provides and expects: nine faces on HEAD (`shards` under `D-UC18` and `go-affected` under `D-UC19` now landed, plus `change-gate` and `tree-check` under `D-UC24`), four record kinds, seven exit codes with the stall code 6, and the remembered-results directory. Its record types are in a `main` package nobody can import, so five utilities each hold a copy. It expects the check declarations (`[gate] checks_dir` in `.tillr.toml` under `D-UC23`) and the registry from the operated repository. By `D-UC16` and `D-UC24` the default gate runners move into compiled `tillr-gate change-gate` and `tillr-gate tree-check` faces with the Python runner scripts deleted, by `D-UC18` `tools/go-test-shards.py` moved into `tillr-gate shards`, and by `D-UC19` `tools/gate/go_affected.py` moved into `tillr-gate go-affected`."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-gate

**Status:** What it provides and what it expects describe the code as it is. The split into contract and private, the pins and the reach-in fixes are proposed and not built. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** One Go program at `tools/tillr-gate`. It derives what a candidate changed, plans which checks that change needs, runs them, and writes a verdict, opening no store. By `D-UC18` ([§8.3 of the overview](../tillr-utility-contracts.md#83-the-go-test-shard-runner-moves-to-go)), `D-UC19` ([§8.4 of the overview](../tillr-utility-contracts.md#84-the-affected-packages-calculator-moves-to-go)) and `D-UC24` ([§8.9 of the overview](../tillr-utility-contracts.md#89-the-remaining-python-files-move-to-go-and-a-gate-check-prevents-reintroduction)) — the designer's decisions from operator requests, which the owner may overturn — `tools/go-test-shards.py` moved into `tools/tillr-gate/shards` as `tillr-gate shards`, `tools/gate/go_affected.py` moved into `tools/tillr-gate/goaffected` as `tillr-gate go-affected`, and `tools/tillr-gate/{change-gate,tree-check,classify,change-gate.test}.py` move into compiled `tillr-gate change-gate` and `tillr-gate tree-check` faces with the Python files deleted.

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

Under `D-UC19` of [the overview](../tillr-utility-contracts.md#84-the-affected-packages-calculator-moves-to-go) (the designer's decision from an operator request), `tillr-gate` gained a `go-affected` face, porting `tools/gate/go_affected.py` with identical flags (`--root`, `--base`, `--head`, `--changed`, `--drop-module`, `--select-only`, `--evidence-out`, `--verdicts-in`, `--verdicts-out` and `--invocation-guard`), exit codes (`0` on pass, `--select-only`, zero affected packages or `--help`; `1` on test failure or runtime error; `2` on usage error; `6` on binary stall; `128 + signum` on signals), `go-affected: ` progress, summary and failure lines plus [`D-GR52`](../../reference/gate.md#D-GR52) `SIGQUIT` case extraction and bounded `fork-check: triage --` failure output, and JSON evidence and package-verdict files, run by `tools/gate/checks/go-affected.json`, `tillr-drain` base replay (`replay_go_affected.go`) and developers.

Under `D-UC24` of [the overview](../tillr-utility-contracts.md#89-the-remaining-python-files-move-to-go-and-a-gate-check-prevents-reintroduction) (the designer's decision from an operator request, which the owner may overturn, not yet built), `tillr-gate` gains two faces — `change-gate` (`--base`, `--head`, `--logs`, optional `--checks` and `--runner`, plus `-h`/`--help`) and `tree-check` (`--only`, optional `--base`, `--head`, `--checks`, `--logs` and `--runner`, plus `-h`/`--help`) — porting `tools/tillr-gate/{change-gate,tree-check,classify}.py` into Go (reading `[gate] checks_dir` from `.tillr.toml` under `D-UC23` when `--checks` is omitted), classifying via `checkresult.Classify` in-process, and exiting `0` (`pass` / `pass-inherited`), `1` (`fail`), `6` (`stalled`), or `4` (could not judge), run by `tillr-loop` (`gate`, `replay`, `fixer-gate`) and developers.

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
| Under `D-UC19` (not yet built), `TILLR_GO_AFFECTED_EVIDENCE_OUT` / `--evidence-out`, `TILLR_GO_VERDICTS_IN` / `--verdicts-in`, `TILLR_GO_VERDICTS_OUT` / `--verdicts-out` and `TILLR_GATE_EDGE_INPUTS` read and written by `go-affected` | `change-gate.py`, `tillr-drain fork-gate` (`forkgate.go`), `goevidence.go`, drain base replay (`replay_go_affected.go`), `TestEdgeInputs` helpers (`edge_inputs_test.go`) |

### 1.6 Names others match on

- **Verdict results.** `pass`, `fail`, `error`, `stalled`, `pass-inherited`.
- **Check results.** Those five, and `unavailable`, `inherited`, `over-budget`, and the one sentence `skipped: no changed path is read`.
- **The stall exit code** 6, and the rule for a gate program's exit: 0 pass, 1 fail, 6 stalled, anything else an error.
- **The check id of the gate program:** `gate:<program name>`.

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| The envelope, the exit codes, the kind registry, settings (`.tillr.toml` `[gate] checks_dir` via `ReadChecksDir` and `ParseChecksDir`; `D-UC23`, `D-UC26`) | [tillr](./tillr.md) | Go import | throughout |
| The check declarations: each direct `*.json` under `[gate] checks_dir` in `.tillr.toml` (default `tools/gate/checks`, or an explicit `--checks` directory, read via `settings.ReadChecksDir` / `settings.ParseChecksDir` ignoring unknown tables and unknown keys; `D-UC23` and `D-UC26` of [the overview](../tillr-utility-contracts.md#811-candidate-and-check-declaration-reads-tolerate-unknown-tillrtoml-tables-and-keys), the designer's decisions from operator requests, which the owner may overturn), with the keys `id label command scope tier budget_ms timeout_ms reads tools needs remember writes exclusive` and no others | the operated repository, outside tillr | files | `checkfiles.go`, `checks.go`, `classify.go`, `change_gate.go`, `tree_check.go` |
| The registry: `digest declarations in_process`, with the digest being the hash of the two arrays | the operated repository, outside tillr | files | `registry.go` |
| The `--gate` program: its exit code, and the fast verdict it writes to the path in `TILLR_FAST_VERDICT_OUT` | the caller's program; in the drain's loop, the change gate (`change-gate.py` under `D-UC16`, replaced by `tillr-gate change-gate` under `D-UC24`) | subprocess, files | `gate.go` |
| `TILLR_HEAVY_LOCK_FD`, an open lock handle | [tillr-drain](./tillr-drain.md) | environment | `checkgroup.go` |
| An inherited `TILLR_FAST_VERDICT_OUT` | [tillr-drain](./tillr-drain.md) | environment | `gate.go` |
| That the fast tier's limit, 9000 ms, equals the limit in `tools/gate/checks/gate-budget.py` | this repository's declared checks | a test | `run.go` |

It reads no store, imports no other utility, and runs no other tillr program.

**The runner scripts (`D-UC16` and `D-UC24`).** Wherever this file says "the repository's gate scripts" as a reader or a caller, it originally meant `change-gate.py`, `tree-check.py` and the modules they imported under `tools/gate/`. `D-UC16` of [the overview](../tillr-utility-contracts.md#81-defaults-come-from-tillrs-installation) moved them into `tools/tillr-gate/`, and `D-UC24` ([§8.9 of the overview](../tillr-utility-contracts.md#89-the-remaining-python-files-move-to-go-and-a-gate-check-prevents-reintroduction)) — both the designer's decisions from operator requests, which the owner may overturn — ports them into compiled `tillr-gate change-gate` and `tillr-gate tree-check` faces sharing `checkresult.Classify` in-process and deletes `tools/tillr-gate/{change-gate,tree-check,classify,change-gate.test}.py`. The check declarations stay the operated repository's own (`[gate] checks_dir` in `.tillr.toml` under `D-UC23`).

## 3. Contract and private

**Contract:**

- `change`, `plan`, `run` and `checks` with the flags above, and the seven exit codes.
- Under `D-UC18` (the designer's decision from an operator request, which the owner may overturn), `shards` with its flags and positional/flag ordering, exit codes (`0`, `1`, `2` and `128 + signum`), refusal to choose a scratch directory itself, refusal of `--cache` in a lane (`TILLR_RUN_ID`), child process-group lifetime guard, `go-test-shards: ` summary and shard-failure lines read by the drain and the roller, and JSON shapes for `--events`, `--record`, `--progress-file` / `TILLR_GO_SHARDS_PROGRESS_OUT` and `TILLR_GO_SHARDS_EVIDENCE_OUT`.
- Under `D-UC19` (the designer's decision from an operator request, which the owner may overturn), `go-affected` with its flags, exit codes (`0`, `1`, `2`, `6` and `128 + signum`), `MAX_SHARDS = 2 * runtime.NumCPU()` (`12` when undetermined) and single-test shard `GOMAXPROCS=1` / `-p 1`, explicit `git` lookup against the shim-bypassed `PATH` in `cmd.Env` with `WritePrivateGitconfig` error propagation, Linux `PR_SET_CHILD_SUBREAPER` lifeline guard, binary stall watchdog, `go-affected: ` progress/summary/failure lines read by the drain, and JSON shapes for `--evidence-out` / `TILLR_GO_AFFECTED_EVIDENCE_OUT` and `--verdicts-in` / `--verdicts-out` (`TILLR_GO_VERDICTS_IN` / `TILLR_GO_VERDICTS_OUT`).
- Under `D-UC24` (the designer's decision from an operator request, which the owner may overturn), `change-gate` and `tree-check` with their flags (`--base`, `--head`, `--logs`, `--only`, `--checks`, `--runner`), `[gate] checks_dir` default resolution under `D-UC23`, `HEAD` checkout verification, `go-build` cache warm-up (`TILLR_GO_BUILD_WARMED=1`), `TILLR_FAST_VERDICT_OUT` / `TILLR_FAST_TREE_TIMING` handling, `checkresult.Classify` outcome mapping, and exit codes (`0`, `1`, `4` and `6`).
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
- **What it accepts.** A test loads every real declaration under this repository's `tools/gate/checks` (plus a fixture exercising a non-default `[gate] checks_dir` in `.tillr.toml` under `D-UC23`) and the registry example. This one reads outside the module by design: the declarations are its input, and a declaration it refuses must fail here before a drain meets it.
- **Events.** The gate keeps the record kinds. The drain's events take names of their own ([the overview's fix for RI-37](../tillr-utility-contracts.md#10-reach-ins-today)), so a program can link both, and `tillr-watch` can know them.
- **Shard runner (`D-UC18`).** Once built, Go unit tests in `tools/tillr-gate/shards` replace `tests/go-test-shards.test.py`, `tools/tillr-land/shards_test.py` and `tools/tillr-land/shards_liveness_test.go` in the same commit, keeping every case.
- **Affected-packages runner (`D-UC19`).** Once built, Go unit tests in `tools/tillr-gate/goaffected` replace `tests/go-affected.test.py` and `tools/gate/checks/go-affected-test.json` in the same landed commit (squashed across per-piece lane commits when built via `build-pieces@v1`), keeping every case (including `TestDiscoverModules`, `TestEdgeInputs`, the refusing-git-shim test, and `TestCheckDeclarationsCoverSelectionInputs` pinning `go-affected.json`'s `exec go run -C tools/tillr-gate . go-affected --root "$PWD" --base "$1" --head "$2"` command and `go-test-tillr-gate.json` coverage over a synthetic repository staging `tools/tillr` and `tools/tillr-gate`).
- **Change-gate and tree-check faces (`D-UC24`).** Once built, Go unit tests in `tools/tillr-gate` replace `tools/tillr-gate/change-gate.test.py` (alongside the Go cases in `tools/gate/change_gate_test.go` and Go AST classification scans in `tools/tillr-gate/classification_scan_test.go`) in the same piece that deletes `tools/tillr-gate/{change-gate,tree-check,classify,change-gate.test}.py`.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- As provider: RI-34 (five copies of its record types), RI-35 (land recomputes the registry digest and reads its files), RI-36 (the stall code typed in five places), RI-37 (two registrations of two kinds), RI-39 (the view models two land records nothing writes beside the gate's), RI-46 (its kinds cannot be linked by `tillr-watch`), RI-47 (the adapter types its flags).
- As consumer: RI-38 (a directory named for the gate that holds the drain's files; two environment names with no owner stated).
