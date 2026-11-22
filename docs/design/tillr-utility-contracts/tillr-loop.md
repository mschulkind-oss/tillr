---
title: "Contract: tillr-loop"
status: in-review
stage: DESIGN
updated: 2026-11-21
tags: [tillr, contract, loop, adapter]
summary: "What tillr-loop provides and expects, read from the code at dab5a2da0e: one Python script with eight faces, five exit codes, and the `describe` answer the drain checks at start. Its provided side is already held by a generated file of real argument lists. Its expected side is not: it types the flags of the gate, land and item programs and tests against stand-ins that accept anything."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-loop

**Status:** 2026-11-21. Read at `dab5a2da0e`; nothing here is built. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** One Python script at `tools/tillr-loop/tillr-loop`. The drain's loop has three steps it hands to a program: judge a candidate, land it, record its verdicts. This script is that program for this repository. It turns each step into calls of `tillr-gate`, `tillr-land` and `tillr-item`. The drain holds a byte-for-byte copy and runs the copy.

## 1. What it provides

### 1.1 Faces

| Face | What it does | Flags the drain passes |
| :--- | :--- | :--- |
| `gate` | `tillr-gate change`, then the registry, then `tillr-gate plan`, then `tillr-gate run` | the candidate flags `--item --lane --run --worktree --base --head --root` |
| `land` | one `tillr-land pass` | the candidate flags, and `--observed-trunk --base-ref --message-from` |
| `verdict` | `tillr-item show`, then one `tillr-item verdict record` per verification-only criterion | the candidate flags and `--verdict` |
| `fixer-gate` | replaces itself with the repository's check replay, for a fixer's candidate | the candidate flags and `--only` |
| `replay`, `rolling` | replace themselves with the repository's check script | `--only`; none |
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

None.

### 1.4 Files and environment

| Place | Read by |
| :--- | :--- |
| `<root>/.tillr/loop/<run>/`: `gate.change.json`, `gate.plan.json`, `gate.verdict.json`, `registry.json`, `logs/`, `change-logs/`. The gate writes the first three where this script tells it to | land, drain, view, `tillr-trace` |
| [`drain-argv.json`](../../../tools/tillr-loop/drain-argv.json): every argument list the drain sends | its own suite; written by a drain test |
| `tools/tillr-drain/drain/tillr-loop.py`, the copy, written by `sync-adapter.py` | drain |
| `PI_FORKS_ROOT`, set for the gate step | the repository's check script |

### 1.5 Names others match on

- The face names `gate`, `land`, `verdict`, `fixer-gate`, `replay`, `rolling`, which are the drain's hook names.
- The version word `tillr-step-argv-check-v1`.
- The script's path, typed in the drain, the roller and the keeper.

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| `change` with `--item --lane --run --worktree --base --head --now --output` | [tillr-gate](./tillr-gate.md) | subprocess | `compose_gate` |
| `plan` with `--registry --input --output --root` | [tillr-gate](./tillr-gate.md) | subprocess | `compose_gate` |
| `run` with `--input --worktree --output --logs --gate --gate-arg`; exits 0, 1 and 6 kept, and 2 to 5 meaning it refused or could not run the plan | [tillr-gate](./tillr-gate.md) | subprocess | `compose_gate`, `execute` |
| `pass` with `--store --repo --item --run --base --head --verdict --base-ref --now --run-dir`, and `--message-from --observed-trunk`; exits 0 and 1 kept | [tillr-land](./tillr-land.md) | subprocess | `compose_land` |
| `show` with `--store --item --root`, printing an `item.record` whose criteria carry `criterion_id text claim_class` | [tillr-item](./tillr-item.md) | subprocess | the `verdict` face |
| `verdict record` with `--store --input --now`, accepting the record of [§1.2](#12-records) | [tillr-item](./tillr-item.md) | subprocess | the `verdict` face |
| The four claim classes; the verdict `done`; the actor kind `mechanical`; that a criterion's hash is the SHA-256 of its text | [tillr-item](./tillr-item.md) | copies | the `verdict` face |
| The argument lists of `drain-argv.json`, and `TILLR_LOOP_ADAPTER_BUILD` | [tillr-drain](./tillr-drain.md) | subprocess, environment | every face |
| The registry export, the check replay and the change gate | the repository's gate scripts, outside tillr | subprocess | `gate`, `fixer-gate`, `replay`, `rolling` |

It reads no store.

## 3. Contract and private

**Contract:** the six hook faces with the flags `describe` prints; `--check-argv`; the five exit codes; the run directory's three record file names; the `item.verdict` record it writes.

**Private:** `self-check`'s text; every refusal sentence; the registry cache; how it finds the three programs.

**The copy is a generated file.** It has one writer, the sync script, and two tests already fail when it differs. It stays, because a Go program cannot embed a file from another module.

## 4. How the contract is pinned

In `tools/tillr-loop/test.py`, which runs in seconds.

- **Exists today, for what it provides.** `test_describe_answers_the_hook_contract`, `test_every_argv_the_drain_sends_is_accepted`, `test_check_argv_parses_without_reading_or_judging`, `test_gate_exit_mapping`, `test_land_exit_mapping`, `test_verdict_exit_mapping`, `test_adapter_copy_matches_source`. The first three are the model for this whole design: the drain writes a file of real argument lists, and the adapter's suite replays every one.
- **To add, for what it expects.** The same in the other direction. The gate, land and the item utility each publish their face and flag lists as files, and a test here checks every argument list this script composes against them. A flag renamed in `tillr-land` then fails land's own contract test first, and this one second, and never a drain run.
- **Stand-ins fed by examples.** The shell stand-ins stay, because the suite must not build Go programs. What they print comes from the providers' example files: the item utility's `show` example, the gate's verdict examples.
- **The verdict record.** One example of the record this script writes, checked against the item utility's `verdict record` input entry.
- **Names.** The claim classes and the stall code are read from the providers' name-set files and no longer typed.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- As consumer: RI-47 (it types three programs' flags and tests against stand-ins that accept anything), RI-48 (it parses `show`, writes a verdict record by hand, and copies the hash rule and the claim classes), RI-36 (the stall code typed again), RI-50 (its suite compiles a test into the drain's package).
- As provider: RI-49 (the copy in the drain).
