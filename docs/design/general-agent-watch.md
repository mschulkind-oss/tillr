---
title: "Any agent can wait on structured conditions and wake on deduplicated episodes without tillr"
status: in-review
stage: DESIGN
tags: [watch, alerting, agents, tillr, contracts, design]
summary: "Separate the watch engine from the tillr store along two seams, an observation-source interface and a standalone watch state file, so any agent can watch files, logs, Git refs or processes and wake on deduplicated episodes, while existing tillr rules keep their meaning as one source adapter. Nothing is built; three choices are open for the owner."
next: "At lower priority than the drain's own work, rule OQ-GW1, OQ-GW2 and OQ-GW3. Slices 1 to 7 of Section 9 do not wait on those rulings."
depends-on:
  - tillr-watch.md
  - tillr-utility-contracts.md
  - tillr-utility-contracts/tillr-watch.md
vantage:
  status-chip: true
---

# Any agent can wait on structured conditions and wake on deduplicated episodes without tillr

**Status:** Nothing in this generalization is built.
`rg -n 'jsonl|--state|agent_watches' tools/tillr/watch tools/tillr-watch --glob '!*_test.go'`
returns nothing, and `tools/tillr-watch/main.go` blank-imports the drain's package. Only the rows
marked as owner rulings in the [Decision Ledger](#decision-ledger) are the owner's; every other
choice is the designer's and the owner may overturn it.

> **In short.** Separate the watch engine from the tillr store along two seams: an **observation
> source** interface for reading inputs and a standalone **watch state file** for episodes,
> cursors and ad-hoc watches. Any agent in any workspace can then watch files, logs, Git refs or
> processes without the rest of `tillr`, and the tillr store becomes one source adapter whose
> existing rules do not change meaning.

**Why it matters.** An agent outside `tillr` polls logs, build output, background servers and Git
refs by hand, or forgets what it was waiting on when its context is compacted. `tillr-watch`
refuses to start without a tillr store and links the drain at compile time
([§2](#2-what-is-already-general-and-what-is-tied-to-the-tillr-store)).

**The shape.** One Go watch engine with no `tillr` imports, fed by pluggable sources, persisting
episodes and per-owner cursors in its own SQLite file, and waking any harness through an ordinary
foreground `wait` process with explicit `seen` confirmation.

**Cost.** Two new abstractions (the source interface and the standalone state store) and a
contract-pinned `tillr` adapter.

**Start at** [§2](#2-what-is-already-general-and-what-is-tied-to-the-tillr-store), then
[§4](#4-what-a-rule-or-watch-may-observe-without-a-tillr-store) to
[§7](#7-how-an-agent-in-any-harness-is-woken).

**Needs your ruling:** [OQ-GW1](#OQ-GW1), [OQ-GW2](#OQ-GW2), [OQ-GW3](#OQ-GW3).

**Reads with:** [the tillr-watch design](./tillr-watch.md) (the episode state machine, the
temporal patterns and the foreground `wait` this generalizes),
[every tillr utility has a written contract](./tillr-utility-contracts.md) and
[the tillr-watch contract](./tillr-utility-contracts/tillr-watch.md) (how the general tool and
`tillr` depend on each other), and [the momentum principle](./momentum-principle.md).

**Words.** **Episode**, **subject key**, **clearing event** and **notice batch** keep their
meanings from [the tillr-watch design](./tillr-watch.md). **Contract** and **reach-in** keep
theirs from [the utility contracts design](./tillr-utility-contracts.md). Four terms are coined
here:

- **Observation source**: a named read-only input adapter that yields timestamped, numbered
  records to the watch engine. Not an action runner; it never changes what it reads.
- **Observation**: one normalized record `(seq, ts, kind, fields)` from a source, evaluated by the
  same pattern and expression machinery wherever it came from.
- **Watch state file**: the watcher's own SQLite database (`--state <path>`) holding episodes,
  notice events, per-owner cursors, rule overrides and ad-hoc watches. Not a tillr store; it
  carries no items, runs or lanes.
- **Ad-hoc watch**: a rule registered from the command line into the watch state file for a
  condition one agent is waiting on now, evaluated by the same engine as a rule file.

---

## 1. Requirements

Three requirements come from the owner's ruling (`R-GW1`, 2026-11-21):

1. **Lower priority than the drain's own work.**
2. **Usable by any agent for any purpose, with or without `tillr`.** Starting, evaluating and
   waiting need no tillr store, no drain binary or package, and no charter or workflow
   configuration.
3. **One alerting system, generalized, not forked.** The tillr rules stay on the same engine and
   keep their meaning.

Three standing owner rulings also bind the design: a rule runs no automatic remedy
([OQ-TW2](./tillr-watch.md#OQ-TW2)); nothing falls back silently
([`D-JL24`](./tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27));
and every utility has a written contract pinned by fast unit tests
([utility contracts](./tillr-utility-contracts.md)). An alert informs an agent; it never fails,
benches or caps work ([D-TW2](./tillr-watch.md#D-TW2)).

---

## 2. What is already general and what is tied to the tillr store

### 2.1 What is already domain-general

- **The temporal pattern evaluators** (`pattern.go`, `eval.go` in `tools/tillr/watch`), with
  window filtering, repeat collapsing, clearing events, subject extraction and one open episode
  per rule and subject ([D-TW5](./tillr-watch.md#D-TW5)). They operate on timestamped records with
  scalar fields and know nothing of items, lanes or runs.
- **The expression language** (`expression.go`): total, deterministic and not Turing-complete,
  apart from two hard-coded projection prefixes (`item.*`, `charter.*`).
- **The bounded notice bundle** (`bundle.go`): structured guidance and recent events, truncated
  deterministically to 8 KiB.
- **The Git ref and commit-lag reader** (`git.go`).
- **The attachment lock and wake loop** (`attachment.go`, `wake.go`, `wait.go` in
  `tools/tillr-watch`): POSIX read locks the kernel releases on kill, and directory watches with a
  fallback ticker.

### 2.2 The five couplings, and how each separates

| # | Coupling | Why it blocks other agents | How it separates |
| :--- | :--- | :--- | :--- |
| **C1** | **Compile-time event registry.** `main.go` blank-imports the drain so its event kinds register, and rule loading rejects any kind not registered (`registeredRuleKinds`). The gate's kinds cannot be linked this way. | No outside event kind loads, and building the watcher compiles other utilities. | Kind checking moves into each source. Outside sources accept any non-empty kind; the `tillr` source validates against the providers' contracts ([§8](#8-how-the-general-watcher-and-tillr-depend-on-each-other)). |
| **C2** | **SQL over the store's `events` table.** | Observations come only from a tillr store. | The engine reads a source-neutral stream of observations ([§4.1](#41-the-observation-record-and-source-contract)); the `tillr` adapter maps event columns and payload into fields with the same precedence. |
| **C3** | **Store scopes and projections.** A rule's scope must be `core`, a charter or a workflow, and projections query store tables. | A rule with no charter or items table is rejected or unevaluated. | `scope` becomes optional and defaults to always active. `git_refs` stays in the core. Store projections and scopes move into the `tillr` adapter and report "not evaluated" with a reason when no tillr source is configured. |
| **C4** | **Episodes, overrides and delivery cursors inside the store.** | With no store there is nowhere to keep them. | A standalone watch state file ([§6](#6-where-episodes-cursors-and-overrides-live)) owns them and delivers to every owner handle a rule's receiver matches. What `--store` mode does is [OQ-GW3](#OQ-GW3). |
| **C5** | **CLI, exit codes and packaging.** `--store` is required, every error exits 1, and no pack ships the binary alone ([D-TW24](./tillr-watch.md#D-TW24)). | Another workspace has no binary and cannot omit `--store`. | `--state` runs with no `--store`; exit codes follow the standard table ([§8](#8-how-the-general-watcher-and-tillr-depend-on-each-other)); the name and pack are [OQ-GW1](#OQ-GW1). |

---

## 3. Principles and standing boundaries

1. **Observe and inform; never act** ([OQ-TW2](./tillr-watch.md#OQ-TW2)). Firing writes watch
   state and wakes receivers. No rule declares a remedy, runs a command in response, signals a
   process or edits a file. Guidance is plain text for the woken agent
   ([D-TW42](./tillr-watch.md#D-TW42)).
2. **Never fail, bench or cap work** ([D-TW2](./tillr-watch.md#D-TW2)). Counts and windows exist
   only to fold a noisy signal into one episode.
3. **No silent fallbacks.** A missing path, a malformed line, an unresolved ref or an unknown
   schema is reported as exactly that.
4. **Total, side-effect-free evaluation** ([D-TW4](./tillr-watch.md#D-TW4)). Every pass reads a
   bounded window and finishes in predictable time.
5. **One episode per rule and subject** ([D-TW5](./tillr-watch.md#D-TW5)). A condition that stays
   true wakes a receiver once.
6. **Printing is not confirmation** ([D-TW11](./tillr-watch.md#D-TW11),
   [D-TW14](./tillr-watch.md#D-TW14)). A notice stays pending until the agent confirms it.
7. **Harness neutrality.** Waking uses process exit and standard output, never a harness plugin
   ([OQ-TW3](./tillr-watch.md#OQ-TW3)).
8. **No `tillr` reach-ins.** The engine imports nothing from `tillr`; the adapter reads only
   declared contract surfaces.

---

## 4. What a rule or watch may observe without a tillr store

### 4.1 The observation record and source contract

| Field | Meaning |
| :--- | :--- |
| `seq` | Source-local increasing cursor within the source's current epoch. |
| `ts` | Timestamp in Unix milliseconds: the record's own when declared and valid, otherwise the time the watcher observed it. |
| `kind` | Record kind. |
| `fields` | A JSON-compatible map read by predicates, grouping and templates. |

A source provides three read-only operations: read the observations in a time window (with a
count of malformed records skipped, reported and never dropped silently); answer named
projections; and name the files to watch and the fallback poll interval.

### 4.2 Non-tillr observation sources

| Source | Observes | Wakes on |
| :--- | :--- | :--- |
| `jsonl` | An append-only file of JSON lines. The kind and timestamp fields are configurable. | A watch on the parent directory, plus a fallback ticker. |
| `log` | An append-only text log. A line matching the source's RE2 pattern becomes an observation whose named capture groups are fields. Scans are bounded. | The same. |
| `git` | A repository on disk: ref movements, and commit distance and age between two refs. | Watches on the refs, plus a fallback ticker. |
| `process` | A local process named by pid, pid file or lock file, with start-time identity so a reused pid is never mistaken for the original. | A poll ticker. |
| `command` | Only if [OQ-GW2](#OQ-GW2) allows it: a read-only argument vector run on an interval with no shell, a timeout and bounded output. Never run in response to an alert. | A poll ticker. |

### 4.3 Projections without a tillr store

`git_refs` and `process` belong to the core. `items`, `charter` and `workflow_selections` belong
to the `tillr` adapter; a rule that asks for one with no tillr source is reported as not
evaluated, with the reason.

### 4.4 Missing, unreadable, rotated, or truncated sources

1. **The file does not exist yet.** The source reports "not ready", watches the parent directory,
   and reads from the start when the file appears. A rule waiting for silence still measures from
   when the watcher attached.
2. **Rotation or truncation.** The cursor records the file's identity and offset. A changed
   identity or a size below the offset starts a new epoch at offset zero and reports it once.
3. **Malformed or partial lines.** A trailing line with no newline is held until the writer
   finishes it. A complete line that fails to parse is counted as skipped and reported.
4. **Pid reuse.** A pid whose start time has changed is recorded as exited.

---

## 5. How rules and one-command agent watches are written

Both forms compile to the same rule and run through the same episode machine.

### 5.1 Standing rule files

A rule file uses the schema of `config/tillr/watch/*.toml` with two generalizations: `scope` is
optional, and a `[rules.source]` table names the rule's source. Relative paths resolve against
`--root`.

```toml
# Alert once per package when it fails 3 times in 15 minutes, and resolve when it passes.
[[rules]]
id = "repeated-package-test-failure"
severity = "warn"
group_by = "package"

[rules.source]
type = "jsonl"
path = ".logs/test-events.jsonl"
kind_field = "event"

[rules.reads]
events = ["package.fail", "package.pass"]

[rules.when]
pattern = "count_in_window"
where = 'kind == "package.fail"'
threshold = 3
window = "15m"
clear_where = 'kind == "package.pass"'

[rules.guidance]
summary = "Package {{subject}} failed 3 times in 15m without a pass."
```

### 5.2 One-command ad-hoc watches

An agent waiting for a log line or a process exit registers a watch in one command, extending
the agent watches of [D-TW8](./tillr-watch.md#D-TW8) to work with no tillr store:

```sh
<watch-cmd> add --state tmp/watch.db --id api-ready --log tmp/api.log \
  --matches '(?P<state>Listening on :8080|FATAL: .*)' --once --owner agent-main \
  --note "API startup finished or faulted; inspect the state field."
<watch-cmd> list --state tmp/watch.db
<watch-cmd> drop --state tmp/watch.db api-ready
```

Ad-hoc watches live in the watch state file, so they survive compaction, a `wait` timeout and a
harness restart. A `--once` watch retires only when the receiver confirms the notice, never on
printing.

### 5.3 How existing tillr rules keep their meaning

The rules in `config/tillr/watch/` name no source. With `--store`, a rule that names no source
reads the tillr store at that path, so every existing rule loads unchanged. Without `--store`,
such a rule is refused at load time with a message naming the missing table. Nothing is guessed.

---

## 6. Where episodes, cursors, and overrides live

With no tillr store, state lives in one self-initializing SQLite file named by `--state`.

- **An explicit path, never a hidden global file.** Every stateful command requires `--state`
  or `--store`; only `lint` is stateless. Nothing is shared across workspaces unless two
  commands name the same file.
- **Write-ahead logging with read-before-write planning.** Evaluation runs on a read snapshot and
  applies its changes in one short write transaction, so `status` is read-only and concurrent
  waiters do not starve each other.
- **What the file holds:** a schema version; per-source cursors with file identity and epoch;
  episodes; notice events with one increasing sequence across all sources, which delivery
  cursors index; one delivery cursor per owner handle; ad-hoc watches; and rule overrides.
- **Attachment locks** sit beside the file, one per owner, so `status` tells a live waiter from a
  killed one.

Whether `--store` mode keeps these tables in the tillr store or in a sidecar file is
[OQ-GW3](#OQ-GW3).

---

## 7. How an agent in any harness is woken

### 7.1 The harness-neutral wait contract

1. **One command evaluates, waits and prints.** `wait` records an optional confirmation of the
   previous batch, takes the attachment lock, evaluates once, and either returns at once with
   pending notices or sleeps on source signals until an episode opens or the timeout passes.
2. **A self-contained notice batch.** One header line carrying the batch's last sequence, one JSON
   line per notice holding the bounded bundle, and one `re-arm:` line with the exact command to
   run next.
3. **Explicit confirmation.** Printing advances the shown cursor. Only the re-arm command or
   `seen --through <N>` advances the seen cursor.

### 7.2 Delivery in Pi, Claude Code, and other harnesses

| Harness | Attaches by | Wakes by |
| :--- | :--- | :--- |
| Pi | A background task running `wait --follow` with an output watch on the notice header, or a one-shot `wait` whose exit wakes the session. | The output watch firing, or the task exiting. |
| Claude Code | A background shell call running `wait` with a timeout inside the harness ceiling. | The call exiting with the batch on standard output. |
| Any other shell | A background job where available, or a foreground `wait`. | The command returning. |

In each, the agent confirms and continues by running the printed `re-arm:` line.

### 7.3 Crashes, compactions, and multiple watchers

| Event | Outcome |
| :--- | :--- |
| Context compaction | The `wait` child stays alive and `status` reports it attached, so the agent reuses it. Cursors and episodes are in SQLite, not in model context. |
| Watcher killed | The kernel drops the lock. The next `wait` for that owner reprints every notice above the seen cursor. |
| Two agents, two roles | Each owner handle has its own cursor and lock; one confirming does not confirm for the other. |
| Two waiters, one owner | The second reports that a watcher is already attached; cursor updates stay serialized. |

---

## 8. How the general watcher and tillr depend on each other

```text
  general watch engine      (no imports from tillr)
        ▲             ▲
        │             │
  standalone CLI   tillr source adapter (reads tillr's contracted surfaces only)
```

1. **The engine imports nothing from `tillr`.** A change to any tillr utility cannot fail its
   build.
2. **The blank import goes, and gate events become watchable.** Each utility declares its event
   kinds and example payloads in its contract, pinned by its own tests. The adapter validates a
   rule's kinds and payload paths against those, so the watcher links no other utility, and a
   provider that renames a contracted field fails its own test first. This clears the reach-ins
   `RI-42` and `RI-46` listed in [the utility contracts design](./tillr-utility-contracts.md).
3. **Store reads stay inside the table contract.** The adapter opens the store read-only.
4. **One exit-code table** for both forms: `0` success or notices delivered; `1` a negative
   answer (a quiet timeout, lint errors); `2` usage; `3` incompatible schema; `4` external I/O;
   `5` internal. The slice that changes the quiet timeout's code updates
   [the operating skill](../../yolo-packs/tillr/skills/tillr-operating/SKILL.md) and
   the timed check-in skill (an operating skill kept outside this repository) in the same
   change.

---

## 9. The build, in slices that each land alone

Slices 1 to 7 wait on no ruling and keep every existing rule green.

| Slice | Builds | Done when | Follows |
| :--- | :--- | :--- | :--- |
| 1 | The source-neutral engine seam; store reads, projections and scopes behind a tillr source. | The pattern evaluators, clearing events, skip counts and bundle truncation pass against an in-memory source with no store open. | — |
| 2 | The standalone watch state file and per-owner cursors. | `eval`, `status`, `wait` and `seen` run with `--state` alone; two owners hold independent cursors. | 1 |
| 3 | The `jsonl` and `log` sources. | Tests cover live append, a file created late, truncation, malformed lines and capture fields. | 2 |
| 4 | The `git` and `process` sources. | Tests cover branch lag with no store, a process exit, pid reuse and lock release. | 2 |
| 5 | Ad-hoc watches (`add`, `list`, `drop`). | A `--once` watch survives an unconfirmed `wait` and a restart, and retires on `seen`. | 3, 4 |
| 6 | Contract-driven event validation in place of the blank import. | `go list -deps` of the watcher names no other utility, and a rule naming a gate event loads. | 1, and the contract entries |
| 7 | Exit-code alignment and harness recipes. | Conformance tests assert each code; no caller branches on the previous timeout code. | 2 |
| 8 | Command name and pack ([OQ-GW1](#OQ-GW1)). | A workspace with no `tools/` on `PATH` runs the watcher from its pack. | 5, 7 |
| 9 | The `command` source, if [OQ-GW2](#OQ-GW2) allows it. | Tests cover output parsing, a non-zero exit, a timeout and refusal of a shell. | 2 |
| 10 | Sidecar episode storage in store mode, if [OQ-GW3](#OQ-GW3) chooses it. | The watcher runs against a read-only tillr store. | 2, 6 |

---

## 10. What this does not do

- It runs no remedy, hook or recovery script when an alert fires.
- It does not fail, stop or cap any run, task or queue item.
- It adds no harness extension or turn injector.
- It builds no network daemon or cross-machine broker; every source and state file is local.
- It changes the syntax or meaning of no existing rule.

---

## 11. Alternatives considered

| Alternative | Why not |
| :--- | :--- |
| A second watch tool built from scratch | Duplicates the pattern machines, clearing logic, expression evaluator, bundle, lock and delivery, against the requirement of one system. |
| Require every agent to initialize a tillr store | Forces the item, run and lane schema on projects that only want to watch a log. |
| A JSON state file | Concurrent `wait`, `seen` and `status` need snapshot reads and serialized cursor updates; SQLite is already linked. |
| A default state file in the home directory | Mixes episodes and cursors across unrelated workspaces. |
| Per-harness wake plugins | Ruled out in [OQ-TW3](./tillr-watch.md#OQ-TW3); an ordinary `wait` works everywhere. |
| More blank imports to register gate kinds | The gate is a `main` package and cannot be imported, and linking every utility into the watcher is a reach-in. |

---

## Open Questions

1. 💬 **OQ-GW1: What command name and pack should ship the general watcher?**

   An agent in a repository with no tillr store wants to watch a build log and wake when an error pattern appears. `tillr-watch` is reached as `tillr watch` and is built only inside this repository ([D-TW24](./tillr-watch.md#D-TW24)); another workspace does not have it on `PATH`.

   - **A — Ship a standalone `agent-watch` binary and pack, with `tillr watch` delegating to it.** Any workspace gets `agent-watch` without installing `tillr`. Existing `tillr watch --store ...` calls keep working.

   - **B — Keep one binary name `tillr-watch` and ship it in a small pack.** Every skill and notice uses one name everywhere, even in workspaces that never touch the rest of `tillr`.

   <!-- vantage: question id=OQ-GW1 leaning="A (a standalone agent-watch binary and pack, with tillr watch delegating to it) — the requirement is use with or without the rest of tillr, and a standalone binary lets other workspaces include it cleanly while tillr watch stays an unbroken alias." -->

   _Leaning:_ A (a standalone `agent-watch` binary and pack, with `tillr watch` delegating to it) — the requirement is use with or without the rest of `tillr`, and a standalone binary lets other workspaces include it cleanly while `tillr watch` stays an unbroken alias.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-GW2: May a watch rule run a configured probe command on an interval, or read passive sources only?**

   An agent wants an alert when a remote pull-request check fails or a local HTTP endpoint stops answering. The passive sources read local files, Git refs and process state without starting anything. A `command` source lets a rule poll a read-only command directly. Running a user-supplied command crosses from reading into executing, so a careless probe could change state even though alerts run no remedy ([OQ-TW2](./tillr-watch.md#OQ-TW2)).

   - **A — Passive sources only.** The watcher never executes a user command. To watch an external check, the agent starts its own loop that appends JSON lines to a file and watches that file.

   - **B — Allow a `command` source with a shell-less argument vector, a minimum interval and bounded output.** A rule polls a command directly. Probes run only to read input, never in response to an alert.

   <!-- vantage: question id=OQ-GW2 leaning="B (a command source with a shell-less argument vector, a minimum interval and bounded output) — watching CI checks, ports or external tools is one of the commonest reasons an agent polls in a loop, and forbidding a shell and post-alert commands keeps OQ-TW2 intact." -->

   _Leaning:_ B (a `command` source with a shell-less argument vector, a minimum interval and bounded output) — watching CI checks, ports or external tools is one of the commonest reasons an agent polls in a loop, and forbidding a shell and post-alert commands keeps [OQ-TW2](./tillr-watch.md#OQ-TW2) intact.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-GW3: When watching a tillr store, should alert episodes and rule overrides move to a sidecar watch file or stay inside the store?**

   With no tillr store, the watcher keeps its state in its own file. With `--store`, `tillr-watch` writes those tables and its alert events into the store, where [OQ-TW5](./tillr-watch.md#OQ-TW5) placed live overrides. Once the store is one input source among others, a sidecar makes it read-only to the watcher.

   - **A — A sidecar watch file beside the store (revisiting [OQ-TW5](./tillr-watch.md#OQ-TW5)).** A drain or lander write never blocks `wait` or `seen`, the store owns no watch tables, and one watch store serves every mode. The observatory reads the sidecar for alerts.

   - **B — Keep episodes, overrides and alert events inside the store when `--store` is passed.** The observatory and the store utility read one file. The code keeps two episode backends, and watcher writes share the store's writer lock.

   <!-- vantage: question id=OQ-GW3 leaning="A (a sidecar watch file beside the store) — treating the store as a read-only source matches 'one source among others', removes writer-lock contention with the drain, and leaves one episode store to maintain." -->

   _Leaning:_ A (a sidecar watch file beside the store) — treating the store as a read-only source matches "one source among others", removes writer-lock contention with the drain, and leaves one episode store to maintain.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

Rows `R-GW1` to `R-GW4` are owner rulings. Every `D-GW` row is the designer's decision, which the
owner may overturn. Nothing in `D-GW1` to `D-GW12` is built.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| R-GW1 | Owner ruling. Generalize `tillr-watch`, at lower priority, so any agent can use it for any purpose with or without the rest of `tillr`. | 2026-11-21 | [§1](#1-requirements) | No |
| R-GW2 | Owner ruling ([OQ-TW2](./tillr-watch.md#OQ-TW2)). Watch rules run no automatic remedy. | 2026-11-21 | [§3](#3-principles-and-standing-boundaries) | — |
| R-GW3 | Owner ruling. No silent fallbacks; a missing or malformed input is reported. | 2026-11-20 | [§3](#3-principles-and-standing-boundaries), [§4.4](#44-missing-unreadable-rotated-or-truncated-sources) | — |
| R-GW4 | Owner ruling. Each tillr utility has a written contract of what it provides and expects, pinned by fast unit tests. | 2026-11-21 | [§8](#8-how-the-general-watcher-and-tillr-depend-on-each-other) | — |
| D-GW1 | Split `tillr-watch` along the five couplings, keeping the pattern machines, expression evaluator, bundle, attachment lock and `wait` / `seen` flow. Rejected: a second tool from scratch. | 2026-11-22 | [§2.2](#22-the-five-couplings-and-how-each-separates) | No |
| D-GW2 | Normalize every input into observations through a read-only source interface. | 2026-11-22 | [§4.1](#41-the-observation-record-and-source-contract) | No |
| D-GW3 | Four passive sources and the `git_refs` and `process` projections in the core; the `command` source waits on [OQ-GW2](#OQ-GW2); store projections live in the `tillr` adapter. | 2026-11-22 | [§4.2](#42-non-tillr-observation-sources), [§4.3](#43-projections-without-a-tillr-store) | No |
| D-GW4 | Missing files, rotation, malformed lines and pid reuse are each handled explicitly and reported. | 2026-11-22 | [§4.4](#44-missing-unreadable-rotated-or-truncated-sources) | No |
| D-GW5 | Rule files gain an optional scope and a source table; a rule naming no source reads the tillr store under `--store` and is refused without it. | 2026-11-22 | [§5.1](#51-standing-rule-files), [§5.3](#53-how-existing-tillr-rules-keep-their-meaning) | No |
| D-GW6 | One-command ad-hoc watches, with `--once` retiring only on a confirmed notice. | 2026-11-22 | [§5.2](#52-one-command-ad-hoc-watches) | No |
| D-GW7 | Standalone state in an explicit SQLite file with one delivery cursor per owner handle. Rejected: JSON state and a home-directory default. | 2026-11-22 | [§6](#6-where-episodes-cursors-and-overrides-live) | No |
| D-GW8 | Wake any harness through the ordinary `wait` process, a notice header, bounded bundles, a re-arm line and explicit `seen`. Rejected: harness-specific extensions. | 2026-11-22 | [§7](#7-how-an-agent-in-any-harness-is-woken) | No |
| D-GW9 | The engine imports nothing from `tillr`; the blank import is deleted; tillr event kinds, the gate's included, are validated against provider contracts. | 2026-11-22 | [§8](#8-how-the-general-watcher-and-tillr-depend-on-each-other) | No |
| D-GW10 | One exit-code table, `0` to `5`, with skill instructions updated in the same slice. | 2026-11-22 | [§8](#8-how-the-general-watcher-and-tillr-depend-on-each-other) | No |
| D-GW11 | This design sits behind the drain's own work on [the tillr roadmap](../tillr/roadmap.md). | 2026-11-22 | [§1](#1-requirements) | — |
| D-GW12 | Ten slices; 1 to 7 land behind the existing binary without waiting on a ruling. | 2026-11-22 | [§9](#9-the-build-in-slices-that-each-land-alone) | No |
