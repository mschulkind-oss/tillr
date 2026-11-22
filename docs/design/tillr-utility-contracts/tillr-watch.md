---
title: "Contract: tillr-watch"
status: in-review
stage: DESIGN
updated: 2026-11-21
tags: [tillr, contract, watch]
summary: "What tillr-watch provides and expects, read from the code at dab5a2da0e: four faces that evaluate watch rules over the event log and wait on them, a notice format read by agents, and three exit codes that differ from every other utility's. It expects the drain package to register every event kind a rule may name, and each rule expects payload fields of the kind's owner."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-watch

**Status:** 2026-11-21. Read at `dab5a2da0e`; nothing here is built. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** One Go program at `tools/tillr-watch`. The rule engine is the `watch` package of [the library](./tillr.md); this program gives it faces. A **watch rule** is a TOML table that names event kinds and a condition over them; when the condition holds, the rule opens an alert.

## 1. What it provides

### 1.1 Faces

| Face | Flags | What it does |
| :--- | :--- | :--- |
| `eval` | `--once --store --rules DIR` | Evaluates every rule once and records alerts |
| `status` | `--store --rules DIR` | Prints `rule <id> skipped=<n>` per rule; opens the store read-only |
| `wait` | `--store --rules DIR`, and `--owner`, `--timeout` or `--follow`, `--seen-through SEQ` | Waits until a notice is pending, prints it, and prints the command that waits again |
| `seen` | `--store --through SEQ`, and `--owner` | Records that notices up to a sequence number were seen |

| Exit | Meaning |
| :--- | :--- |
| 0 | Done; for `wait`, notices were printed |
| 1 | Every failure: usage, a refused rule, a store error |
| 2 | `wait` timed out with nothing pending |

These are not the library's exit codes, and no face prints a record envelope.

### 1.2 What `wait` prints

On standard output, in this order: the line `TILLR_WATCH_NOTICE <n> pending, through_seq <seq>`; per notice a line `[alert <alert> <rule> <subject>, <severity>] <summary>` with indented `check:` and `do:` lines; a separator line; one JSON line per notice with `rule_id subject severity guidance events omitted`; and a line starting `re-arm:` holding the command to run next.

### 1.3 Go packages, records, events, tables

None of its own. The alert kinds and the two alert tables are the library's.

### 1.4 Files

It reads the rule files under `--rules`, and watches the store's write-ahead log to know when to evaluate again.

### 1.5 Names others match on

`TILLR_WATCH_NOTICE`, `through_seq` and `re-arm:` are read by the agent that runs `wait`. No program in the repository parses them (`rg -n 'TILLR_WATCH_NOTICE|re-arm:' tools -g '*.go' -g '*.py' -g '!tools/tillr-watch/**' -g '!tools/tillr/watch/**'` returns nothing).

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| `OpenMigrated`, `OpenRead`, the transactions; `LoadRules`, `Evaluator`, `Notice`, `Rule` | [tillr](./tillr.md) | Go import | `main.go`, `wait.go` |
| That loading the `drain` package registers every event kind a rule may name, and with it the item, run and land kinds the drain links | [tillr-drain](./tillr-drain.md) | Go import, for no name | `main.go` |
| For each rule, the kinds it reads and the payload fields its text addresses. The two example rules read `drain.node-advanced`, `drain.refusal`, `drain.revert`, `drain.fixer-proved`, `boundary.check` and `land.landing` | [tillr-drain](./tillr-drain.md), [tillr-land](./tillr-land.md) | SQL on `events` | the rule files |
| The write-ahead log beside the store, changed by any writer | [tillr](./tillr.md) | files | `wait.go` |

Two things it cannot have today. The gate's kinds are registered in a `main` package that nothing can link, so a rule naming `gate.verdict` is refused as unregistered. And a rule's payload path is checked against no list, so a renamed field makes the rule quietly match nothing.

## 3. Contract and private

**Contract:** the four faces and their flags; the three marker words of [§1.5](#15-names-others-match-on); the JSON line's six fields; that `status` writes nothing.

**Private:** the notice's wording, the separator line, and everything on standard error.

**The exit codes are a defect and not a contract.** They become the library's: 2 for usage, 3 for a store this build cannot open, and a named code for the timeout. Which code the timeout takes is small, and my default is 1 (a negative answer: nothing fired); the owner may overturn it.

## 4. How the contract is pinned

In `tools/tillr-watch`, in its fast suite.

- **Exists today.** `TestWatchWaitFromQuietToFiring`, `TestWatchStatusReadOnly`, `TestWatchWritableFacesRequireExistingStore`, `TestFollowDelivery`, `TestClaudeRearmDelivery`. They append a `drain.refusal` with a payload typed by hand.
- **To add.** One example of `wait`'s output per situation (one notice, several, cut guidance), compared for the marker lines and the JSON line. One example per exit code. A conformance test like every other Go utility's.
- **Rules against their providers.** Each event entry in a provider's contract lists its payload fields. The rule loader's test reads those lists and fails a rule that addresses a field no entry has. The tests' hand-typed payloads are replaced by the provider's event examples.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- RI-42: the drain package is imported only for what loading it registers.
- RI-46: no envelope, its own exit codes, no conformance test, and the gate's kinds cannot be linked.
