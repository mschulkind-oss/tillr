---
title: "Contract: tillr-watch"
status: in-review
stage: DESIGN
updated: 2026-11-22
tags: [tillr, contract, watch]
summary: "What tillr-watch provides and expects: evaluation, status, wait/seen, episode lifecycle faces (ack, track, silence, resolve), live rule overrides (rule), and pre-flight lint, with agent-readable notice and status formats and three exit codes that differ from every other utility's."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-watch

**Status:** The surface in sections 1 and 2 describes the code as it is, including the watch decisions `D-TW27`–`D-TW38`, `D-TW55`–`D-TW61`, `D-TW63` and `D-TW65`–`D-TW67` of [`../tillr-watch.md`](../tillr-watch.md). The pins listed under "To add" in section 4 are not built. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it. The watch decisions named here, other than the owner ruling `D-TW64`, are designer's decisions the owner may overturn.

**What it is.** One Go program at `tools/tillr-watch`. The rule engine is the `watch` package of [the library](./tillr.md); this program gives it faces. A **watch rule** is a TOML table that names event kinds and a condition over them; when the condition holds, the rule opens an alert.

## 1. What it provides

### 1.1 Faces

| Face | Flags | What it does |
| :--- | :--- | :--- |
| `eval` | `--once --store --rules DIR [--root DIR]` | Evaluates every rule once and records alerts |
| `daemon` | `--store --rules DIR [--root DIR]` | Evaluates at start and on wakeups with no foreground delivery |
| `status` | `--store --rules DIR [--root DIR] [--json]` | Prints rule lines (`skipped`, `unreadable`, `out-of-scope`), unresolved `alert` lines (including optional `by=`, `until=`, and `item=`; JSON `by`, `until`, `item_id` from `tracking_item_id`), and `owner` attachment lines; opens the store read-only and writes nothing ([`../tillr-watch.md` Section 6.7](../tillr-watch.md#67-the-second-build-the-remaining-patterns-projections-and-the-alert-commands-d-tw27), [Section 6.9](../tillr-watch.md#69-rule-load-baselines-judging-only-events-from-a-rules-first-load-onward-d-tw66), [Section 6.10](../tillr-watch.md#610-tracking-an-alert-cause-with-an-open-queue-item-d-tw67)) |
| `wait` | `--store --rules DIR [--root DIR]`, and `--owner`, `--timeout` or `--follow`, `--seen-through SEQ` | Holds the owner's attachment lock, waits until a notice is pending, prints it (with `ack:` and `track:` hints), and prints the command that waits again |
| `seen` | `--store --through SEQ`, and `--owner` | Records that notices up to a sequence number were seen |
| `ack` | `<alert-id> --by ACTOR [--note TEXT] [--item ITEM-ID] --store` | Claims an `open` episode (`acknowledged`), optionally attaching an open tracking item (`tracking_item_id`; [`../tillr-watch.md` Section 6.10](../tillr-watch.md#610-tracking-an-alert-cause-with-an-open-queue-item-d-tw67)) |
| `track` | `<alert-id> --item ITEM-ID [--by ACTOR] [--note TEXT] --store` | Attaches or updates an open tracking item (`tracking_item_id`) on an unresolved episode (`open`, `acknowledged`, or `silenced`), suppressing repeat wakeups while the item is open and waking once if the condition persists when the item lands or closes ([`../tillr-watch.md` Section 6.10](../tillr-watch.md#610-tracking-an-alert-cause-with-an-open-queue-item-d-tw67)) |
| `silence` | `<alert-id> (--until DUR\|TS \| --until-change) [--reason TEXT] --store` | Silences an untracked episode by time or condition fingerprint (refused when item-tracked; count-based silencing does not exist) |
| `resolve` | `<alert-id> --reason TEXT --store` | Manually resolves an unresolved episode |
| `rule` | `list \| show <id> \| add <file> --by ACTOR \| tune <id> --set K=V... --by ACTOR \| reset <id> --by ACTOR \| export` (`--store --rules DIR`) | Lists, inspects, overrides, resets, or exports live rules |
| `lint` | `[<repo-or-dir>] [--charter PATH] [--workflows PATH] [--policy PATH]... [--rules PATH]` | Static pre-flight validation of charter, workflows, backend policy, and watch rules ([`../tillr-watch.md` Section 7.2](../tillr-watch.md#72-tillr-watch-lint-checking-a-workflows-whole-configuration-before-it-runs-d-tw10)) |

| Exit | Meaning |
| :--- | :--- |
| 0 | Done; for `wait`, notices were printed |
| 1 | Every failure: usage, a refused rule, a store error |
| 2 | `wait` timed out with nothing pending |

These are not the library's exit codes, and no face prints a record envelope.

### 1.2 What `wait` prints

On standard output, in this order: the line `TILLR_WATCH_NOTICE <n> pending, through_seq <seq>`; per notice a line `[alert <alert> <rule> <subject>, <severity>] <summary>` (prefixed with `"tracked fix <item_id> landed|closed[ (<state>)] and the condition persists: "` when reopened after a tracked item lands or closes; [`../tillr-watch.md` Section 6.10](../tillr-watch.md#610-tracking-an-alert-cause-with-an-open-queue-item-d-tw67)) with indented `check:`, `do:`, optional `omitted:`, `ack:`, and `track:` lines; a separator line; one JSON line per notice with `rule_id subject severity guidance events omitted`; and a line starting `re-arm:` holding the command to run next.

### 1.3 Go packages, records, events, tables

None of its own. The alert and rule event kinds and the four watch tables (`alert_episodes`, `foreground_delivery`, `watch_rule_overrides`, `watch_rule_baselines`) are the library's ([`./tillr.md`](./tillr.md)).

### 1.4 Files

It reads the rule files under `--rules`, reads Git refs under `--root` when a rule declares `git_refs:` (and static tuple files in `lint`), holds a shared POSIX record lock on `<store>.watch-attached/<sha256(owner)>.lock` during `wait` (queried read-only by `status`), and watches the store's write-ahead log to know when to evaluate again.

### 1.5 Names others match on

`TILLR_WATCH_NOTICE`, `through_seq` and `re-arm:` are read by the agent that runs `wait`. No program in the repository parses them (`rg -n 'TILLR_WATCH_NOTICE|re-arm:' tools -g '*.go' -g '*.py' -g '!tools/tillr-watch/**' -g '!tools/tillr/watch/**'` returns nothing).

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| `OpenMigrated`, `OpenRead`, the transactions; `LoadRules`, `Evaluator`, `Notice`, `Rule` | [tillr](./tillr.md) | Go import | `main.go`, `wait.go` |
| That loading the `drain` package registers every event kind a rule may name, and with it the item, run and land kinds the drain links | [tillr-drain](./tillr-drain.md) | Go import, for no name | `main.go` |
| For each rule, the kinds it reads and the payload fields its text addresses. The two example rules read `drain.node-advanced`, `drain.refusal`, `drain.revert`, `drain.fixer-proved`, `boundary.check` and `land.landing` | [tillr-drain](./tillr-drain.md), [tillr-land](./tillr-land.md) | SQL on `events` | the rule files |
| The write-ahead log beside the store, changed by any writer | [tillr](./tillr.md) | files | `wait.go` |

Two things it cannot have. The gate's kinds are registered in a `main` package that nothing can link, so a rule naming `gate.verdict` is refused as unregistered. And a rule's payload path is checked against no list, so a renamed field makes the rule quietly match nothing.

## 3. Contract and private

**Contract:** the faces and their flags in [§1.1](#11-faces); the `status` `--json` keys (`rules`, `alerts` including optional `by`, `until` and `item_id`, and `owners`); the three marker words of [§1.5](#15-names-others-match-on); the JSON bundle line's six fields; that `status` and `lint` write nothing; and that refused episode mutations (`ack`, `track`, `silence`, `resolve`) leave the store untouched.

**Private:** the notice's wording, the separator line, and everything on standard error.

**The exit codes are a defect and not a contract.** They become the library's: 2 for usage, 3 for a store this build cannot open, and a named code for the timeout. Which code the timeout takes is small, and the designer's default is 1 (a negative answer: nothing fired); the owner may overturn it.

## 4. How the contract is pinned

In `tools/tillr-watch`, in its fast suite.

- **Exists.** `TestWatchWaitFromQuietToFiring`, `TestWatchStatusReadOnly`, `TestWatchWritableFacesRequireExistingStore`, `TestFollowDelivery`, `TestClaudeRearmDelivery`. They append a `drain.refusal` with a payload typed by hand.
- **To add.** One example of `wait`'s output per situation (one notice, several, cut guidance), compared for the marker lines and the JSON line. One example per exit code. A conformance test like every other Go utility's.
- **Rules against their providers.** Each event entry in a provider's contract lists its payload fields. The rule loader's test reads those lists and fails a rule that addresses a field no entry has. The tests' hand-typed payloads are replaced by the provider's event examples.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- RI-42: the drain package is imported only for what loading it registers.
- RI-46: no envelope, its own exit codes, no conformance test, and the gate's kinds cannot be linked.
