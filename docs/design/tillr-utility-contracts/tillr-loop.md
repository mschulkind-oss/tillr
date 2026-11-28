---
title: "Contract: tillr-loop"
status: in-review
stage: DESIGN
updated: 2026-11-28
tags: [tillr, contract, loop, adapter]
summary: "What tillr-loop provides and expects: a Go program and `loop` package with eight faces, five exit codes, and the `describe` answer the drain checks at start (ported from Python under D-UC20 with its Python script, Python test suite, sync helper and synced drain copy removed, amended under D-UC23 for `.tillr.toml` `[gate] checks_dir`, and amended under D-UC24 to invoke compiled `tillr-gate change-gate` and `tillr-gate tree-check` faces rather than `python3` runner scripts). Its provided side is held by a generated file of real argument lists; its expected side types the flags of the gate, land and item programs and tests against stand-ins."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-loop

**Status:** Nothing here is built: `tools/tillr-loop` has no `contract/` directory. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it. The faces, flags, exit codes and test names below were checked against `tools/tillr-loop` on 2026-11-24.

**What it is.** A Go module at `tools/tillr-loop/` providing the `tillr-loop` binary and the `loop` package compiled into `tillr-drain`. The drain's loop has three steps it hands to a program: judge a candidate, land it, record its verdicts. This adapter is that program for this repository: it turns each step into calls of `tillr-gate`, `tillr-land` and `tillr-item`, and its [README](../../../tools/tillr-loop/README.md) documents the same faces and exit codes for an operator. Under `D-UC20` of [the overview](../tillr-utility-contracts.md#85-the-loop-adapter-moves-to-go) — the designer's decision from an operator request, which the owner may overturn — `tools/tillr-loop/tillr-loop` and `tools/tillr-loop/test.py` moved to Go with the same eight faces, arguments, records, exit codes and environment variables, `tools/tillr-loop/sync-adapter.py` and `tools/tillr-drain/drain/tillr-loop.py` were deleted, and the loop's `--gate`, `--land` and `--verdict` values in the keeper and documents updated to `tillr-loop`.

## 1. What it provides

### 1.1 Faces

| Face | What it does | Flags the drain passes |
| :--- | :--- | :--- |
| `gate` | `tillr-gate change`, then `tillr-gate plan` over `--registry` (else `$TILLR_LOOP_REGISTRY`, else the candidate's `[gate] checks_dir` directory in `.tillr.toml`, defaulting to `tools/gate/checks`; `D-UC23`), then `tillr-gate run` | the candidate flags `--item --lane --run --worktree --base --head --root --store --work-dir --now`, and `--tillr-gate --tillr-land --tillr-item --registry --gate-program` |
| `land` | one `tillr-land pass` | the candidate flags, the three program flags, and `--observed-trunk --base-ref --message-from` |
| `verdict` | `tillr-item show`, then one `tillr-item verdict record` per verification-only criterion | the candidate flags, the three program flags and `--verdict` |
| `fixer-gate` | replaces itself with the judge of one named whole-tree declaration, in a fixer's candidate worktree | the candidate flags, the three program flags and `--only` |
| `replay`, `rolling` | replace themselves with `tillr-gate checks --heavy-tree` (or the fast tree check — `tree-check.py --only` on `HEAD` today, replaced by `tillr-gate tree-check --only` under `D-UC24` — for a fast declaration) on the checked-out revision | `--only`; `--all` or `--since` |
| `describe` | prints `{"hooks": {<face>: [<flags>]}}` | none |
| `self-check` | prints the commands it would run, and runs nothing | none |

Every face takes `--check-argv`: parse the arguments, do nothing, exit 0.

| Exit | Meaning |
| :--- | :--- |
| 0 | `gate`: pass. `land`: landed. `verdict`: recorded |
| 1 | `gate`: fail. `land`: not landed |
| 2 | Usage, or a value it could not derive |
| 4 | No judgment was made. Any exit of the judging step other than 0, 1 and (for the gate) 6 becomes this |
| 6 | `gate` only: a check stalled |

### 1.2 Records

It writes one `item.verdict` record per criterion to `<run directory>/item.verdict.<criterion>.json`, with `item_id criterion_id criterion_hash verdict claim_class evidence evaluated_head actor_kind`, and hands the file to `tillr-item verdict record`.

### 1.3 Go packages, events, tables

Under `D-UC20` of [the overview](../tillr-utility-contracts.md#85-the-loop-adapter-moves-to-go), `tools/tillr-loop/loop` is the Go package linked by `tillr-drain`, with no events or tables.

### 1.4 Files and environment

| Place | Read by |
| :--- | :--- |
| `<root>/.tillr/loop/<run>/`: `gate.change.json`, `gate.plan.json`, `gate.verdict.json`, `logs/`, `change-logs/`. The gate writes the first three where the adapter tells it to | land, drain, view, `tillr-trace` |
| [`drain-argv.json`](../../../tools/tillr-loop/drain-argv.json): every argument list the drain sends | its own suite; written by a drain test |

### 1.5 Names others match on

- The face names `gate`, `land`, `verdict`, `fixer-gate`, `replay`, `rolling`, which are the drain's hook names.
- The version word `tillr-step-argv-check-v1`.
- The adapter's command: `tillr-loop` (and `builtin:tillr-loop` or the legacy `tools/tillr-loop/tillr-loop` path for older configurations).

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| The envelope, exit codes, settings (`.tillr.toml` `[gate] checks_dir` via `ReadChecksDir`; `D-UC23`, `D-UC26`) | [tillr](./tillr.md) | Go import | `loop.go` |
| `change` with `--item --lane --run --worktree --base --head --now --output` | [tillr-gate](./tillr-gate.md) | subprocess | `compose_gate` |
| `plan` with `--registry --input --output --root` | [tillr-gate](./tillr-gate.md) | subprocess | `compose_gate` |
| `run` with `--input --worktree --output --logs --gate --gate-arg`; exits 0, 1 and 6 kept, and 2 to 5 meaning it refused or could not run the plan | [tillr-gate](./tillr-gate.md) | subprocess | `compose_gate`, `execute` |
| `pass` with `--store --repo --item --run --base --head --verdict --base-ref --now --run-dir`, and `--message-from --observed-trunk`; exits 0 and 1 kept | [tillr-land](./tillr-land.md) | subprocess | `compose_land` |
| `show` with `--store --item --root`, printing an `item.record` whose criteria carry `criterion_id text claim_class` | [tillr-item](./tillr-item.md) | subprocess | the `verdict` face |
| `verdict record` with `--store --input --now`, accepting the record of [§1.2](#12-records) | [tillr-item](./tillr-item.md) | subprocess | the `verdict` face |
| The four claim classes; the verdict `done`; the actor kind `mechanical`; that a criterion's hash is the SHA-256 of its text | [tillr-item](./tillr-item.md) | copies | the `verdict` face |
| The argument lists of `drain-argv.json`, and `TILLR_LOOP_ADAPTER_BUILD` | [tillr-drain](./tillr-drain.md) | subprocess, environment | every face |
| The change gate (`change-gate.py` on `HEAD` today, replaced by `tillr-gate change-gate` under `D-UC24`) and the fast tree check (`tree-check.py` on `HEAD` today, replaced by `tillr-gate tree-check` under `D-UC24`), as the default runners | [tillr-gate](./tillr-gate.md), from tillr's installation | subprocess | `gate`, `fixer-gate`, `replay`, `rolling` |
| The check declarations under the candidate's or checkout's configured `[gate] checks_dir` directory in `.tillr.toml` (default `tools/gate/checks`, read via `settings.ReadChecksDir` ignoring unknown tables and unknown keys; overridden on `gate` and `self-check` by `--registry`, then `TILLR_LOOP_REGISTRY`; `D-UC23`, [`D-UC26`](../tillr-utility-contracts.md#811-candidate-and-check-declaration-reads-tolerate-unknown-tillrtoml-tables-and-keys)) | the operated repository, outside tillr | files | `gate`, `self-check`, `fixer-gate`, `replay`, `rolling` |

It reads no store.

**Who provides the runners.** This table named the provider of the two runner scripts as "the repository's gate scripts, outside tillr" until 2026-11-25. `D-UC16` of [the overview](../tillr-utility-contracts.md#81-defaults-come-from-tillrs-installation) — the designer's decision from an operator request, which the owner may overturn — moved the default runners (`change-gate.py` and `tree-check.py`) to `tillr-gate`'s installation via `TILLR_INSTALL_ROOT`, leaving the operated repository owning only its check declarations (`--gate-program` and `--registry` still override the defaults), and `D-UC20` preserved that resolution in the initial Go port. `D-UC23` ([§8.8 of the overview](../tillr-utility-contracts.md#88-the-check-declarations-directory-is-configured-in-tillrtoml)) reads the operated repository's check-declarations directory from `[gate] checks_dir` in `.tillr.toml` (default `tools/gate/checks`, with `--registry` and `TILLR_LOOP_REGISTRY` keeping precedence on `gate` and `self-check`), and `D-UC24` ([§8.9 of the overview](../tillr-utility-contracts.md#89-the-remaining-python-files-move-to-go-and-a-gate-check-prevents-reintroduction)) — both the designer's decisions from operator requests, which the owner may overturn — compiles `change-gate.py` and `tree-check.py` into `tillr-gate change-gate` and `tillr-gate tree-check` faces so `tillr-loop` invokes the resolved `tillr-gate` binary directly with zero `.py` files and no `python3` or `resolveGateRunner` lookup.

## 3. Contract and private

**Contract:** the six hook faces with the flags `describe` prints; `--check-argv`; the five exit codes; the run directory's three record file names; the `item.verdict` record it writes.

**Private:** `self-check`'s text; every refusal sentence; how it finds the three programs and the installed default runners.

**No synced copy in Go (`D-UC20`).** While the adapter is a Python script on `HEAD` today, `tools/tillr-drain/drain/tillr-loop.py` is kept equal to `tools/tillr-loop/tillr-loop` by `tools/tillr-loop/sync-adapter.py` because `//go:embed` cannot reach a sibling module. By `D-UC20` of [the overview](../tillr-utility-contracts.md#85-the-loop-adapter-moves-to-go) (the designer's decision from an operator request, which the owner may overturn), porting the adapter to Go removes the need for a synced second copy: Go packages link directly into `tillr-drain`, and both `tools/tillr-drain/drain/tillr-loop.py` and `tools/tillr-loop/sync-adapter.py` are deleted (superseding the earlier note that kept the generated copy).

## 4. How the contract is pinned

In the Go test suite under `tools/tillr-loop/` (`go test ./...`), which runs in seconds (ported from the deleted Python suite `tools/tillr-loop/test.py` and `tools/tillr-loop/testdata/drain-hooks_test.go` under `D-UC20`).

- **Exists today, for what it provides.** The Go tests covering `describe` answering the hook contract, replaying every argument list in `drain-argv.json`, `--check-argv` parsing without reading or judging, and the `gate`, `land`, and `verdict` exit mappings. The first three are the model for this whole design: the drain writes a file of real argument lists, and the adapter's suite replays every one.
- **To add, for what it expects.** The same in the other direction. The gate, land and the item utility each publish their face and flag lists as files, and a test here checks every argument list the adapter composes against them. A flag renamed in `tillr-land` then fails land's own contract test first, and this one second, and never a drain run.
- **Stand-ins fed by examples.** The stand-ins stay, because the suite must not build external Go programs. What they print comes from the providers' example files: the item utility's `show` example, the gate's verdict examples.
- **The verdict record.** One example of the record the adapter writes, checked against the item utility's `verdict record` input entry.
- **Names.** The claim classes and the stall code are read from the providers' name-set files, not typed here.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- As consumer: RI-47 (it types three programs' flags and tests against stand-ins that accept anything), RI-48 (it parses `show`, writes a verdict record by hand, and copies the hash rule and the claim classes), RI-36 (the stall code typed again), RI-50 (its Python suite compiles a test into the drain's package with an overlay; removed when `D-UC20` ports the suite to Go).
- As provider: RI-49 (the synced copy in the drain; removed when `D-UC20` ports the adapter to Go).
