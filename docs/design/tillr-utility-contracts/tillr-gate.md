---
title: "Contract: tillr-gate"
status: in-review
stage: DESIGN
updated: 2026-11-21
tags: [tillr, contract, gate]
summary: "What tillr-gate provides and expects, read from the code at dab5a2da0e: six faces, four record kinds, seven exit codes with the stall code 6, and the remembered-results directory. Its record types are in a `main` package nobody can import, so five utilities each hold a copy. It expects the check declarations and the registry from the repository's gate scripts."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-gate

**Status:** 2026-11-21. Read at `dab5a2da0e`; nothing here is built. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** One Go program at `tools/tillr-gate`. It derives what a candidate changed, plans which checks that change needs, runs them, and writes a verdict. It opens no store.

## 1. What it provides

### 1.1 Faces

| Face | Flags others pass | What it writes | Run by |
| :--- | :--- | :--- | :--- |
| `change` | `--item --lane --run --worktree --base --head --now --output` | `gate.change` | `tillr-loop` |
| `plan` | `--registry --input --output --root` | `gate.plan` | `tillr-loop` |
| `run` | `--input --worktree --output --logs --gate --gate-arg --gate-timeout --tree-overrun` | `gate.verdict`, written before it exits | `tillr-loop` |
| `checks` | `--base --head --worktree --checks --logs`, one of `--fast-tree` or `--heavy-tree`, and `--scope-to-change --only --tree-overrun` | `gate.verdict` on standard output | the drain (heavy tree), the repository's gate scripts (fast tree) |
| `shadow` | `--input --worktree --output --logs` | `gate.shadow` | nobody in the tree |
| `dualrun` | `--registry --report --work --candidate` | a comparison report | people |

| Exit | Meaning |
| :--- | :--- |
| 0 | The verdict is a pass |
| 1 | The verdict is a fail |
| 2 | Usage, or malformed input |
| 3 | Incompatible: the plan's registry digest does not match |
| 4 | It could not judge. Never a failed check |
| 5 | Internal |
| 6 | A check stalled |

### 1.2 Go packages

None. Every type below is in a `main` package and cannot be imported.

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

### 1.6 Names others match on

- **Verdict results.** `pass`, `fail`, `error`, `stalled`, `pass-inherited`.
- **Check results.** Those five, and `unavailable`, `inherited`, `over-budget`, and the one sentence `skipped: no changed path is read`.
- **The stall exit code** 6, and the rule for a gate program's exit: 0 pass, 1 fail, 6 stalled, anything else an error.
- **The check id of the gate program:** `gate:<program name>`.

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| The envelope, the exit codes, the kind registry | [tillr](./tillr.md) | Go import | throughout |
| The check declarations: each direct `*.json` under `tools/gate/checks`, with the keys `id label command scope tier budget_ms timeout_ms reads tools needs remember writes exclusive` and no others | the repository's gate scripts, outside tillr | files | `checkfiles.go` |
| The registry: `digest declarations in_process`, with the digest being the hash of the two arrays | `tools/fork-check.py --registry-json`, outside tillr | files | `registry.go` |
| The `--gate` program: its exit code, and the fast verdict it writes to the path in `TILLR_FAST_VERDICT_OUT` | the caller's program; in the drain's loop, `tools/gate/change-gate.py` | subprocess, files | `gate.go` |
| `TILLR_HEAVY_LOCK_FD`, an open lock handle | [tillr-drain](./tillr-drain.md) | environment | `checkgroup.go` |
| An inherited `TILLR_FAST_VERDICT_OUT` | [tillr-drain](./tillr-drain.md) | environment | `gate.go` |
| That the fast tier's limit, 9000 ms, equals the limit in `tools/gate/checks/gate-budget.py` | the repository's gate scripts | a test | `run.go` |

It reads no store, imports no other utility, and runs no other tillr program.

## 3. Contract and private

**Contract:**

- `change`, `plan`, `run` and `checks` with the flags above, and the seven exit codes.
- `gate.change` whole. Of `gate.plan`: `plan_id base_sha tree_sha registry_digest`, and per check `check_id reads kind label tier`. Of `gate.verdict`: all eight top-level fields; per check `check_id result duration_ms budget_ms over_budget_ms log_ref`; the inherited failure whole.
- The names of [§1.6](#16-names-others-match-on).
- The remembered-results directory and its file name form, because the gate scripts read it.
- The declaration keys and the registry shape, as what the gate accepts.

**Private:**

- The other check fields of the plan and the verdict; `gate.shadow`; `dualrun`'s report.
- Everything `run` prints on standard error: the summary line, the `unavailable` and `remembered pass` lines.
- The watchdog, the budgets' arithmetic, the Go environment memory.

**How the types reach consumers.** Under option A of [OQ-UC1](../tillr-utility-contracts.md#OQ-UC1) the record types move to a small package `tools/tillr-gate/gate` that holds only types, names and decoders, and land, the drain and the view import it. Under option B they stay where they are and consumers read the examples. Either way the copies go.

## 4. How the contract is pinned

In `tools/tillr-gate`, in its fast suite.

- **Exists today.** `TestThePlanFaceIsConformant`, `TestTheRunFaceIsConformant`, `TestTheGateProgramsExitIsTheVerdict`, `TestRunRecordsAFailedCheckAsAVerdict`, `TestRunExitsFourForACheckThatCannotStart`, `TestTheChangeFaceDerivesTheCandidatesRecord`.
- **Examples.** One `gate.change`, one `gate.plan`, and one `gate.verdict` per result: pass, fail, error, stalled, pass-inherited, and one with an unavailable check. Each is made by the face in process over a fixture registry whose checks are `true` and `false`, and compared whole with the file. Land's hand-built verdict and the trace's hand-typed plan are replaced by these.
- **Exit codes.** A name-set file with the seven codes, compared with the constants. The drain, the adapter and the two gate scripts compare against it.
- **What it accepts.** A test loads every real declaration under `tools/gate/checks` and the registry example. This one reads outside the module by design: the declarations are its input, and a declaration it refuses must fail here before a drain meets it.
- **Events.** The gate keeps the record kinds. The drain's events take names of their own ([the overview's fix for RI-37](../tillr-utility-contracts.md#10-reach-ins-today)), so a program can link both, and `tillr-watch` can know them.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- As provider: RI-34 (five copies of its record types), RI-35 (land recomputes the registry digest and reads its files), RI-36 (the stall code typed in five places), RI-37 (two registrations of two kinds), RI-39 (the view models retired land records beside the gate's), RI-46 (its kinds cannot be linked by `tillr-watch`), RI-47 (the adapter types its flags).
- As consumer: RI-38 (a directory named for the gate that holds the drain's files; two environment names with no owner stated).
