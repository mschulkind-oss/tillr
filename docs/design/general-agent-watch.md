---
title: "Any agent can wait on structured conditions and wake on deduplicated episodes without tillr"
status: in-review
stage: DESIGN
updated: 2026-11-22
tags: [watch, alerting, agents, tillr, contracts, design]
summary: "The owner asked on 2026-11-21, at lower priority than the drain's own work, to generalize tillr-watch so any agent can use it for any purpose with or without the rest of tillr. Today's rule engine, expression evaluator, six temporal patterns, 8 KiB truncating bundle, POSIX attachment lock, and foreground wait protocol are already domain-general; five couplings tie them to .tillr/state.db and tillr-drain. Separating an observation-source interface and a standalone SQLite watch state file (--state) lets any agent watch appended JSONL streams, plain-text logs, Git refs, or local processes (and, if approved, probe commands), write TOML rules or register one-command watches, and wake cleanly in Pi, Claude Code, or any shell harness, while existing tillr rules keep their exact meaning as one source adapter governed by the utility contracts design. Three choices are open for the owner (command/pack identity, active command probes, and sidecar vs in-store episodes for tillr). Nothing is built."
next: "At lower priority behind the drain's own rulings, rule OQ-GW1 (standalone agent-watch command and pack vs keeping tillr-watch), OQ-GW2 (passive sources only vs bounded probe commands), and OQ-GW3 (sidecar watch.db vs episodes inside .tillr/state.db). Slices 1 to 7 of Section 9 do not wait on those rulings."
depends-on:
  - tillr-watch.md
  - tillr-utility-contracts.md
  - tillr-utility-contracts/tillr-watch.md
vantage:
  status-chip: true
---

# Any agent can wait on structured conditions and wake on deduplicated episodes without tillr

**Status:** 2026-11-22. Nothing in this generalization is built: read at `7243353ec6`, `rg -n 'jsonl|--state|agent_watches' tools/tillr/watch tools/tillr-watch --glob '!*_test.go'` returns nothing, seven of the eleven non-test Go files in `tools/tillr/watch` import `tillr/store`, and `tools/tillr-watch/main.go` blank-imports `tillr-drain/drain`. Lower priority than the drain's own work, per the owner's instruction in [§1](#1-what-the-owner-said). Only the owner's dated words quoted in [§1](#1-what-the-owner-said) are owner rulings; every other choice is the designer's and is marked so in the [Decision Ledger](#decision-ledger).

> **In short.** Separate the watch engine from `.tillr/state.db` along two seams — an **observation source** interface for reading inputs and a standalone SQLite **watch state file** (`--state`) for episodes, cursors, and ad-hoc watches — so any agent in any workspace can watch files, logs, Git refs, or processes and wake on deduplicated episodes without installing or running the rest of `tillr`, while `.tillr/state.db` becomes one source adapter whose existing rules do not change meaning.

**Why it matters.** Agents outside `tillr` still burn turns polling logs, build outputs, background servers, and Git refs by hand, or lose track of what they were waiting on across context compactions, because `tillr-watch` today refuses to start without a `.tillr/state.db` store and links `tillr-drain` at compile time ([§2](#2-todays-tillr-watch-what-is-already-general-and-what-is-tied-to-the-tillr-store)).

**The shape.** One pure Go watch engine with zero `tillr` imports, fed by pluggable **observation sources** (`jsonl`, `log`, `git`, `process`, optional `command`, and `tillr`), persisting episodes and per-owner delivery cursors in a self-initializing SQLite state file, and waking any harness through an ordinary foreground `wait` process with explicit `seen` confirmation ([§4](#4-what-a-rule-or-watch-may-observe-without-a-tillr-store)–[§8](#8-how-the-general-watcher-and-tillr-depend-on-each-other)).

**Cost.** Two new abstractions (the source interface and the standalone SQLite state store) and a contract-pinned `tillr` adapter ([§8](#8-how-the-general-watcher-and-tillr-depend-on-each-other)), sequenced behind the drain's own reliability work so active drain repairs are not delayed.

**Start at** [§2](#2-todays-tillr-watch-what-is-already-general-and-what-is-tied-to-the-tillr-store) for the exact split of today's code, then [§4](#4-what-a-rule-or-watch-may-observe-without-a-tillr-store)–[§7](#7-how-an-agent-in-any-harness-is-woken) for how an agent without `tillr` uses it.

**Needs your ruling:** [OQ-GW1](#OQ-GW1), [OQ-GW2](#OQ-GW2), [OQ-GW3](#OQ-GW3).

**Awaits your review:** the designer's decisions `D-GW1` to `D-GW12` in the [Decision Ledger](#decision-ledger).

**Reads with:** [the tillr-watch design](./tillr-watch.md) (the episode state machine, six temporal patterns, and foreground `wait` delivery this generalizes), [every tillr utility has a written contract](./tillr-utility-contracts.md) and [the tillr-watch contract](./tillr-utility-contracts/tillr-watch.md) (how the general tool and `tillr` depend on each other without reach-ins), and [the momentum principle](./momentum-principle.md) (fault detection rather than budgets or caps).

**Words.** **Episode**, **subject key**, **clearing event**, and **notice batch** keep their meanings from [the tillr-watch design](./tillr-watch.md#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5). **Contract**, **provides**, **expects**, and **reach-in** keep their meanings from [every tillr utility has a written contract](./tillr-utility-contracts.md#4-the-six-rules). Four terms are coined here:

- **Observation source** *(coined here)*: a named input adapter (`jsonl`, `log`, `git`, `process`, `command`, or `tillr`) that yields timestamped, monotonically numbered records and wakeup signals to the watch engine. It is not an action runner and never mutates the system it reads.
- **Observation** *(coined here)*: one normalized record `(seq, ts, kind, fields)` read from an observation source, evaluated by the same pattern and expression machinery regardless of where it came from.
- **Watch state file** *(coined here)*: the watcher's own SQLite database (`--state <path>`), holding episodes, notice events, per-owner delivery cursors, live rule overrides, and one-command agent watches when no `tillr` store is used (and alongside `.tillr/state.db` if [OQ-GW3](#OQ-GW3) chooses Option A). It is not a `tillr` store and carries no items, runs, or lanes.
- **Ad-hoc watch** *(coined here)*: a rule registered from the command line into the watch state file (`add` / `list` / `drop`) for a condition one agent is waiting on right now, evaluated by the same episode engine as a directory TOML rule.

---

## 1. What the owner said

The owner said on 2026-11-21, verbatim:

> "I want, you can file this as a lower priority, but I want this tiller watch command to be usable by any agent for any purpose that it wants, with or without the rest of tiller. This seems like such a useful thing to have some alerting system for agents. Let's generalize it somehow."

("tiller" is speech-to-text for `tillr`.)

What I take as ruled by those words:

1. **Lower priority than the drain's own work.** This design and its build slices sit behind active drain rulings and repairs on [the tillr roadmap](../tillr/roadmap.md).
2. **Usable by any agent for any purpose, with or without `tillr`.** Starting, evaluating, and waiting on watches must require no `.tillr/state.db`, no `tillr-drain` binary or Go package, and no `tillr` charter or workflow configuration.
3. **One alerting system, generalized rather than forked.** `tillr`'s own watch rules stay on the same engine, with `.tillr/state.db` as one input source among others and existing rules keeping their meaning.

Three standing owner rulings also bind every part of this design, quoted from their dated records:

- **No automatic remedy ([OQ-TW2](./tillr-watch.md#OQ-TW2), 2026-11-21):** *"Every single one of these sounds like a fallback to cover an issue. We don't cover issues. We use this to find the issues and fix them."* When a rule fires, the watcher records the episode and delivers the notice; it never runs a recovery command, kills a process, edits a file, or mutates external state.
- **No silent fallbacks ([the judge and lifecycle design](./tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27), 2026-11-20):** *"I hate fallbacks... We need deliberate choices, not fallbacks."* A missing source, bad schema, or unreadable ref is reported explicitly and never masked by switching to another source or dropping an error.
- **Defined utility contracts pinned by fast unit tests ([every tillr utility has a written contract](./tillr-utility-contracts.md#1-the-ruling), 2026-11-21):** *"So not just Vue should have a contract, all of the Tiller utilities should have a contract. They are separate utilities, so they need to have a defined API and what they expect from others."*

The filed request also carries forward the repository's standing rule against attempt counts and wall budgets that fail, bench, or cancel work ([the tillr-watch standing boundary](./tillr-watch.md#32-standing-ruling-an-alert-never-fails-benches-or-caps-work-d-tw2), [the momentum principle](./momentum-principle.md), and the owner's 2026-11-17 check-stall ruling quoted in [the decision provenance review](../tillr/decision-provenance-review.md#OQ-PR19): *"target budgets, watch for stalls, but don't let 'load is high' cause failures that would otherwise finish just fine"*). An alert informs an agent; it never terminates or caps work.

What the owner's words do not settle is either left open for him in [OQ-GW1](#OQ-GW1)–[OQ-GW3](#OQ-GW3) (the command and pack boundary, whether a rule may run a probe command, and whether `tillr` mode moves episodes to a sidecar file) or chosen here as a designer's decision (`D-GW1`–`D-GW12`) that he may overturn.

---

## 2. Today's tillr-watch: what is already general and what is tied to the tillr store

Everything in this section was read at `7243353ec6` on 2026-11-22 across the 6 non-test files of `tools/tillr-watch/`, the 11 non-test files of `tools/tillr/watch/`, and the 4 rule files (5 rules) in `config/tillr/watch/`.

### 2.1 What is already domain-general

More than half of `tillr-watch` already has nothing to do with `tillr`'s domain model:

- **Six temporal pattern evaluators (`tools/tillr/watch/pattern.go`, `eval.go`).** `predicate_match`, `count_in_window`, `consecutive_count`, `alternating_pair`, `lag_behind`, and `duration_without_event` — together with window-first filtering ([D-TW23](./tillr-watch.md#D-TW23)), consecutive repeat collapsing ([D-TW12](./tillr-watch.md#D-TW12)), clearing-event reconciliation ([D-TW17](./tillr-watch.md#D-TW17), [D-TW18](./tillr-watch.md#D-TW18)), string-only `group_by` subject extraction ([D-TW22](./tillr-watch.md#D-TW22)), and one-open-episode-per-`(rule_id, subject_key)` deduplication ([D-TW5](./tillr-watch.md#D-TW5)) — operate on timestamped records with string/number/boolean fields and need no knowledge of items, lanes, or runs.
- **Total expression language (`tools/tillr/watch/expression.go`).** The parser and evaluator (`==`, `!=`, `<`, `<=`, `>`, `>=`, `in`, `not in`, `contains`, RE2 `matches`, `empty()`, `age()`, boolean connectives, and dot-path field lookup) are deterministic, non-Turing-complete, and free of `tillr/store` imports, save for two hardcoded projection prefixes (`item.*` and `charter.*`).
- **Bounded 8 KiB notice bundle (`tools/tillr/watch/bundle.go`).** `buildBundle` renders structured guidance (`summary`, `check`, `do`), recent triggering events, and projections, then applies a deterministic five-step truncation ladder (`events` -> `do` -> `check` -> `summary` -> `subject`) to stay at or under `8,192` bytes. It imports nothing from `tillr/store`.
- **Git ref and commit-lag reader (`tools/tillr/watch/git.go`).** `readGit`, `gitCommit`, and `gitLag` resolve any two Git refs and compute commit distance and oldest-unmerged commit age in an isolated Git environment (`gitProjectionEnv`), touching `tillr/store` only for one fault-injection hook (`store.CheckIO("watch Git projection")`).
- **Process attachment lock and filesystem wake loop (`tools/tillr-watch/attachment.go`, `wake.go`, `wait.go`).** `acquireAttachment` and `attachmentActive` probe liveness with non-blocking POSIX `fcntl` read locks (`F_SETLK` / `F_GETLK`) that the kernel releases automatically on `SIGKILL`, and `wake` watches parent directories via `fsnotify` with a 60-second fallback ticker so missing files, atomic renames, and unconfirmed notice repeats never wedge a waiter.

### 2.2 The five couplings to the tillr store, and how each separates

Seven of the eleven non-test files in `tools/tillr/watch` (`delivery.go`, `eval.go`, `git.go`, `pattern.go`, `projection.go`, `rule_store.go`, `status.go`) import `github.com/mschulkind-oss/tillr/store`, and `tools/tillr-watch/main.go` blank-imports `github.com/mschulkind-oss/tillr-drain/drain`. Every coupling falls into one of five places (`D-GW1`):

| # | Coupling in today's code (`7243353ec6`) | Why it blocks non-tillr agents | How it separates |
| :--- | :--- | :--- | :--- |
| **C1** | **Compile-time event registry.** `tools/tillr-watch/main.go` blank-imports `tillr-drain/drain` ([RI-42](./tillr-utility-contracts.md#10-reach-ins-today)), and `loadRuleDocument` (`tools/tillr/watch/rules.go`) rejects any `reads.events` kind absent from `event.Kinds()` (which also cannot link `tillr-gate`'s `main`-package kinds, [RI-46](./tillr-utility-contracts.md#106-the-library-the-store-the-watch-and-the-dispatcher)). | No non-tillr event kind can load, and building the watcher compiles `tillr-drain`, `tillr-item`, `tillr-run`, and `tillr-land`. | Kind checking moves to each **observation source**. Non-tillr sources accept any non-empty kind string; the `tillr` source validates kinds and payload fields against provider `contract/` entries ([§8](#8-how-the-general-watcher-and-tillr-depend-on-each-other)), deleting the blank `tillr-drain` import. |
| **C2** | **SQL query over `events`.** `derive` (`tools/tillr/watch/pattern.go`) runs `SELECT seq,ts,kind,seat,object_type,object_id,revision,payload FROM events` through `*store.Reader` and resolves top-level `tillr` columns beside JSON `payload`. | Observations can come only from a `tillr` SQLite `events` table. | `derive` reads a source-neutral `ObservationSource` stream yielding `(seq, ts, kind, fields)` records ([§4.1](#41-the-observation-record-and-source-contract)). The `tillr` adapter maps `seat`, `object_type`, `object_id`, `revision`, and `payload` into `fields` identically to today's `lookupField`. |
| **C3** | **Store scopes and projections.** `parseScope` (`rules.go`) requires `core`, `charter:<name>`, or `workflow:<name>`; `projection.go` queries `items`, `workflow_selections`, and `definition.ForReader`; `expression.go` hardcodes `item.*` and `charter.*` operands. | A rule without a `tillr` charter or `items` table is rejected or unevaluated. | `scope` becomes optional and defaults to `"*"` (always active). `git_refs` stays in the general core (needing only a repo directory, with `store.CheckIO` replaced by an injected hook). `items`, `charter`, `workflow_selections`, `charter:<name>`, and `workflow:<name>` move into the `tillr` source adapter and return a clear `not evaluated` reason when no `tillr` source is configured. |
| **C4** | **Episodes, overrides, and `"foreground"`-only cursors inside `.tillr/state.db`.** `eval.go`, `delivery.go`, `rule_store.go`, and `status.go` register `alert_episodes`, `foreground_delivery`, and `watch_rule_overrides` on `.tillr/state.db`, append `watch.alert.*` into `events`, watch `<store>-wal`, and guard `Pending` (`delivery.go`) and `readOwners` (`status.go`) with `if owner != "foreground"`. | Without `.tillr/state.db` there is nowhere to store episodes or cursors — and even inside `tillr`, a caller passing `--owner operator-session` (as [the timed drain check-in skill](../../.agents/skills/drain-timed-checkin/SKILL.md) does today) receives zero standing-rule notices because of `owner != "foreground"`. | A standalone SQLite **watch state file** (`--state <path>`, [§6](#6-where-episodes-cursors-and-overrides-live)) owns episodes, `watch_events`, `delivery_cursors`, `watch_rule_overrides`, and `agent_watches`, and delivers to **every** `--owner` handle matching a rule's `receiver` (`"*"` by default). How `--store` in `tillr` mode stores episodes is [OQ-GW3](#OQ-GW3). |
| **C5** | **CLI flags, exit codes, and `tillr`-only packaging.** `main.go` requires `--store`, prints `"tillr watch:"` prefixes and `TILLR_WATCH_NOTICE`, exits `1` on any error and `2` on timeout ([RI-46](./tillr-utility-contracts.md#106-the-library-the-store-the-watch-and-the-dispatcher)), and has no standalone pack ([D-TW24](./tillr-watch.md#D-TW24)). | Another workspace has no binary on `PATH`, cannot omit `--store`, and cannot tell a timeout from a bad flag under standard `tillr` exit codes. | `--state` runs with zero `--store`; exit codes align with the standard contract (`0` notices/ok, `1` quiet timeout, `2` usage, `3` incompatible state/store, `4` external I/O, `5` internal); command/pack identity is [OQ-GW1](#OQ-GW1). |

---

## 3. Principles and standing boundaries

The designer's decisions below (`D-GW1`–`D-GW12`) hold these eight rules across both non-tillr and `tillr` uses:

1. **Observe and inform; never act ([OQ-TW2](./tillr-watch.md#OQ-TW2)).** Firing an episode writes watch state and wakes waiting receivers. No rule or ad-hoc watch may declare a `remedy`, run a recovery command, signal a process, or modify tracked files. `guidance.do` (or an ad-hoc watch's `--note`) is plain text for the woken agent, which decides what to do ([D-TW42](./tillr-watch.md#D-TW42)).
2. **Never fail, bench, or cap work ([D-TW2](./tillr-watch.md#D-TW2), [the momentum principle](./momentum-principle.md)).** Watch counts and windows exist solely to deduplicate noisy signals into one episode. An alert has no authority over any process, task, or queue item.
3. **No silent fallbacks.** If a configured source path is missing, a JSONL line is malformed, a Git ref does not resolve, or a schema version is unknown, the watcher records or prints that exact condition on stderr / `status --json` rather than substituting another input or pretending the condition passed.
4. **Total, side-effect-free evaluation ([D-TW4](./tillr-watch.md#D-TW4)).** Predicates, projections, and pattern state machines never execute shell expressions, allocate unbounded memory, or loop without bound. Every evaluation pass reads a bounded `[now - window, now]` slice and finishes in predictable time.
5. **One episode per `(rule_id, subject_key)` ([D-TW5](./tillr-watch.md#D-TW5)).** A condition that stays true across a thousand log lines or ticks wakes a receiver once when the episode opens, increments `observation_count` silently while it remains open, and wakes again only if a new episode opens after resolution (or its condition fingerprint changes).
6. **Printing is not confirmation ([D-TW11](./tillr-watch.md#D-TW11), [D-TW14](./tillr-watch.md#D-TW14)).** If an agent's background task is killed, compacted, or lost before the agent confirms `seen --through <seq>` (or re-arms with `--seen-through <seq>`), the notice stays pending (`shown_seq > seen_seq`) and wakes the next attached watcher immediately.
7. **Harness neutrality.** Waking an agent uses standard POSIX process exit and standard output lines, never a harness-proprietary plugin or turn-injection extension (ruled out for Pi by the owner on 2026-11-21 in [the tillr-watch design](./tillr-watch.md#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3)).
8. **Zero `tillr` reach-ins.** The general watch engine imports nothing from `tillr`. The `tillr` source adapter reads `tillr` solely through declared contract surfaces ([§8](#8-how-the-general-watcher-and-tillr-depend-on-each-other)).

---

## 4. What a rule or watch may observe without a tillr store

### 4.1 The observation record and source contract

Every source feeds the pattern engine the same normalized `Observation` (`D-GW2`):

| Field | Type | Meaning |
| :--- | :--- | :--- |
| `seq` | `int64` | Source-local monotonically increasing cursor (`>= 1`) within the source's current epoch |
| `ts` | `int64` | Observation timestamp in Unix milliseconds (from the record's timestamp field if declared and valid, else the file/probe observation time recorded by the watcher) |
| `kind` | `string` | Record kind (for example `"test.failed"`, `"line"`, `"ref.updated"`, `"process.exited"`, or a `tillr` event kind) |
| `fields` | `map[string]any` | Structured JSON-compatible map queried by `where`, `clear_where`, `group_by`, and template placeholders |

Each `ObservationSource` implementation provides three read-only operations:

1. `ReadWindow(ctx, sinceMillis, kinds) ([]Observation, SourceCursor, SkippedLines, error)` — returns matching observations in `[sinceMillis, now]` in ascending `seq` order, plus a count of malformed records skipped (reported explicitly like today's `group_by` skip count, never silently dropped).
2. `Project(ctx, names) (map[string]any, error)` — returns optional sidecar projections requested by a rule (`git_refs`, `process`, or, on the `tillr` adapter, `items` / `charter` / `workflow_selections`).
3. `WatchTargets() (fsPaths []string, pollInterval time.Duration)` — tells `wake` which files or parent directories to register with `fsnotify` (for sub-second wakeups on appended files and Git ref updates) and what fallback poll interval to use when a source has no filesystem event (such as kernel process state).

### 4.2 Non-tillr observation sources

Four passive sources ship in the core engine (`D-GW3`), and one active probe source is gated on [OQ-GW2](#OQ-GW2):

| Source `type` | What it observes | How `Observation` fields are populated | Wakeup mechanism |
| :--- | :--- | :--- | :--- |
| `jsonl` | An append-only file of newline-delimited JSON objects (test runners, build telemetry, structured service logs, agent event logs). | `seq` is the byte-offset / line ordinal tracked in `source_cursors`; `kind` comes from a configurable `kind_field` (default `"kind"`, falling back to `"event"` only when configured, otherwise `"record"`); `ts` comes from `ts_field` (Unix ms or RFC 3339, else observation time); `fields` holds the parsed JSON object. | `fsnotify` on the file's parent directory (surviving creation and atomic rename) + 60s fallback ticker. |
| `log` | An append-only plain-text log file (compiler output, dev server stdout/stderr, container log, system log). | Scans newly appended lines within a bounded tail window (`max_scan_bytes`, default `4 MiB`). A line matching the source's RE2 `match` pattern (or any non-empty line if `match` is omitted) produces `kind = "line"` (or a named `line_kind`) with `fields.line` and every named capture group `(?P<name>...)` extracted as a string field. | `fsnotify` on the file's parent directory + 60s fallback ticker. |
| `git` | Any Git repository on disk (`repo` path, defaulting to `--root` or `.`). | Emits virtual observations on ref movements and exposes `git.ref("<ref>")` plus `lag_behind` commit-distance and age calculations between any two refs (`HEAD`, `refs/heads/main`, `refs/remotes/origin/main`, tags, or custom refs) using `readGit` in an isolated Git environment (`GIT_OPTIONAL_LOCKS=0`). | `fsnotify` on `<gitdir>/refs`, `<gitdir>/HEAD`, and `<gitdir>/packed-refs` + 60s fallback ticker. |
| `process` | A local OS process identified by `pid`, `pid_file`, or a POSIX `fcntl` lockfile (`lock_file`). | Probes kernel liveness (`kill(pid, 0)` and `/proc/<pid>/stat` start-time identity on Linux so PID reuse is never mistaken for the original process, or `F_GETLK` on `lock_file`). Emits `process.alive` / `process.exited` transition records with `pid`, `alive` (`bool`), and `lock_held` (`bool`). | Poll ticker (`poll_every`, default `2s`, minimum `500ms`) + `fsnotify` on `pid_file` / `lock_file` parent directory. |
| `command` *(only if [OQ-GW2](#OQ-GW2) chooses B)* | Periodic execution of a read-only `argv` array (`["gh", "pr", "checks", "--json", "name,state"]`) with no shell (`sh -c` is not implicit), explicit `every` interval (`>= 5s`), explicit `timeout` (`<= 30s`), and `<= 256 KiB` stdout capture. | Parses exit status (`exit_code`), `duration_ms`, and either JSON stdout (`format = "json"`, yielding fields or array elements) or line stdout (`format = "lines"`), emitting one observation per probe or element. Never runs in response to an alert firing. | Poll ticker at `every`. |

With those sources, every non-tillr case named in the request has a direct representation:

- **Appended events file:** `type = "jsonl"`, `path = "logs/events.jsonl"`.
- **Plain-text log:** `type = "log"`, `path = ".next/server.log"`, `match = '(?P<level>ERROR|FATAL): (?P<msg>.*)'`.
- **Git ref or branch lag:** `type = "git"`, comparing `refs/remotes/origin/main` against `refs/heads/main` (or alerting when a ref moves).
- **Process liveness:** `type = "process"`, `pid = 41820` or `lock_file = "tmp/dev-server.lock"`.
- **Command output:** under [OQ-GW2](#OQ-GW2) Option B, `type = "command"` directly; under Option A, a tiny background command appending JSON lines to a file watched by `type = "jsonl"`.

### 4.3 Projections without a tillr store

In predicates (`where` / `clear_where`) and notice bundles (`reads.projections`), two projections belong to the general core (`D-GW3`):

- `git_refs` — enables `git.ref("<refname>")` in predicates and attaches resolved ref SHAs to the `8 KiB` bundle whenever a Git repository directory is available (`--root` or `[rule.source] repo = "..."`), with no store required.
- `process` — enables `process.alive` and `process.pid` in predicates and bundles when a rule attaches a `process` projection.

`items`, `charter`, and `workflow_selections` belong exclusively to the `tillr` source adapter. If a rule requests `reads.projections = ["items"]` when no `tillr` store is configured, the rule is reported cleanly as `not evaluated: projection "items" requires a tillr store source` (matching today's `Unsupported` reporting in `status` and `eval`).

### 4.4 Missing, unreadable, rotated, or truncated sources

File and process inputs behave differently from an append-only SQLite table, so four edge cases are defined explicitly (`D-GW4`):

1. **Source file does not exist yet.** When an agent starts a watcher before a build creates `build.log`, `jsonl` and `log` sources report `source not ready: missing <path>` in `status` without failing the watcher process, watch the parent directory via `fsnotify`, and begin reading at byte `0` the moment the file appears. (`duration_without_event` still measures silence from when the watcher attached or the rule's start anchor, so a build that never even creates its log alerts on schedule.)
2. **Log truncation or rotation (`inode` change or `size < byte_offset`).** For `jsonl` and `log`, `source_cursors` records `(dev, ino, byte_offset, epoch)`. When the file's `(dev, ino)` changes or its size shrinks below `byte_offset`, the source increments `epoch`, resets `byte_offset` to `0`, and logs one diagnostic notice (`source rotated or truncated; cursor reset to byte 0`) rather than reading past EOF or silently stalling at the old offset.
3. **Malformed JSONL line or oversized line.** A truncated trailing line without `\n` is held until the writer finishes the newline (so partial writes never false-fault). A completed line ending in `\n` that fails JSON decoding increments the rule's `skipped` counter and prints one stderr diagnostic naming the file and line number, matching today's `Skipped` accounting in `tools/tillr/watch/eval.go`.
4. **PID reuse on `process` sources.** When `pid` is given without a lockfile, the watcher records the process start time (`starttime` in `/proc/<pid>/stat` on Linux, `ps -o lstart=` elsewhere) on first observation. If the same numeric `pid` later shows a different start time, the original process is recorded as `process.exited`, never as still alive.

---

## 5. How rules and one-command agent watches are written

An agent without `tillr` has two ways to express what it wants to watch: **standing TOML rule files** (shared by a project or team in a directory) and **one-command ad-hoc watches** (registered on the fly by a session waiting on a specific condition) (`D-GW5`, `D-GW6`). Both compile to the same internal `Rule` struct and run through the same episode state machine.

### 5.1 Standing TOML rules without a tillr store

A TOML file in `--rules <dir>` uses the same `[[rules]]` schema as `config/tillr/watch/*.toml`, with two generalizations (`D-GW5`):

- `scope` is optional and defaults to `"*"` (always active). `core`, `charter:<name>`, and `workflow:<name>` remain valid for `tillr` store rules.
- `[rules.source]` names the rule's observation source (`type = "jsonl" | "log" | "git" | "process" | "command" | "tillr"` and its parameters, or `name = "<alias>"` referencing a shared `[sources.<alias>]` table in the file). Relative paths in `[rules.source]` resolve against `--root` (defaulting to the current working directory).

Three complete examples show how rules read non-tillr sources:

```toml
# 1. Alert once per test package when a test fails 3 times in 15 minutes in an NDJSON stream,
#    and auto-resolve when that package passes.
[[rules]]
id = "repeated-package-test-failure"
severity = "warn"
group_by = "package"

[rules.source]
type = "jsonl"
path = ".logs/test-events.jsonl"
kind_field = "event"
ts_field = "ts"

[rules.reads]
events = ["package.fail", "package.pass"]

[rules.when]
pattern = "count_in_window"
where = 'kind == "package.fail"'
threshold = 3
window = "15m"
clear_where = 'kind == "package.pass"'

[rules.guidance]
summary = "Package {{subject}} failed {{ threshold }} times in 15m without a pass."
check = ["Read the failing test output in .logs/test-events.jsonl for {{subject}}."]
do = ["Diagnose the root failure in {{subject}} before re-running the full suite."]
```

```toml
# 2. Alert when a dev server log prints PANIC or EADDRINUSE.
[[rules]]
id = "dev-server-fatal-line"
severity = "critical"

[rules.source]
type = "log"
path = "tmp/dev-server.log"
match = '(?P<level>PANIC|FATAL|EADDRINUSE)(?::\s*(?P<detail>.*))?'

[rules.reads]
events = ["line"]

[rules.when]
pattern = "predicate_match"
where = 'level != ""'

[rules.guidance]
summary = "Dev server logged {{level}}."
check = ["Inspect tmp/dev-server.log around the latest {{level}} line."]
do = ["Fix the server fault or port conflict and restart the dev server."]
```

```toml
# 3. Alert when local main lags origin/main by 5 commits for at least 10 minutes.
[[rules]]
id = "local-branch-behind-origin"
severity = "info"

[rules.source]
type = "git"

[rules.reads]
projections = ["git_refs"]

[rules.when]
pattern = "lag_behind"
ahead_ref = "refs/remotes/origin/main"
behind_ref = "refs/heads/main"
commits = 5
duration = "10m"

[rules.guidance]
summary = "Local main is at least 5 commits behind origin/main for 10m."
check = ["Run git log --oneline main..origin/main to inspect upstream changes."]
do = ["Rebase or merge origin/main when your working tree is clean."]
```

### 5.2 One-command ad-hoc watches for any agent

An agent waiting for a long build to log `"Listening on"`, a background PID to exit, or an NDJSON stream to emit `"deploy.finished"` should not have to create a directory and hand-author a 25-line TOML file (`D-GW6`, carrying forward [Mode B of the tillr-watch design](./tillr-watch.md#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3) without requiring `.tillr/state.db`):

```sh
# Watch a plain-text log for either readiness or a fatal error, fire once, and auto-drop:
<watch-cmd> add --state tmp/watch.db \
  --id api-ready \
  --log tmp/api.log \
  --matches '(?P<state>Listening on :8080|FATAL: .*)' \
  --once \
  --owner agent-main \
  --note "API server startup finished or faulted; inspect state field before running integration tests."

# Watch a background process by PID so the agent wakes when it exits:
<watch-cmd> add --state tmp/watch.db \
  --id cargo-bench \
  --pid 58219 \
  --where 'kind == "process.exited"' \
  --once \
  --owner agent-main \
  --note "cargo bench PID 58219 exited; read tmp/bench.out."

# Inspect or remove active ad-hoc watches:
<watch-cmd> list --state tmp/watch.db [--owner agent-main] [--json]
<watch-cmd> drop --state tmp/watch.db api-ready
```

(`<watch-cmd>` is `agent-watch` under [OQ-GW1](#OQ-GW1) Option A, or `tillr-watch` / `tillr watch` under Option B.)

Ad-hoc watches live in the `agent_watches` table of `--state` ([§6](#6-where-episodes-cursors-and-overrides-live)), so they survive session compactions, `wait` timeouts, and harness restarts. When `--once` is set, the watch automatically retires (`retired_at` stamped) as soon as the receiver confirms `seen --through <seq>` for the notice that opened its episode — never on mere printing, so a lost turn never drops a one-shot watch before the agent has read it.

### 5.3 How existing tillr rules keep their exact meaning

All four files (five rules) in `config/tillr/watch/` today (`drain-item-fault.toml`, `drain-repeated-verifier-refusals.toml`, `drain-revert.toml`, and `trunk-proof-lag.toml`) omit `[rules.source]` and write `scope = "core"` or `scope = "workflow:git-landing"` with `[rules.reads] events = [...]` or `projections = ["git_refs"]`.

Compatibility is exact and uses no heuristic (`D-GW5`):

- When `--store <path>` is passed on the CLI, the default rule source for any rule that omits `[rules.source]` is the `tillr` store at `<path>`. Every existing rule file in `config/tillr/watch/` loads and evaluates with zero edits and identical subject keys, window boundaries, clearing events, and `8 KiB` bundles.
- When `--store` is **not** passed (`--state` only), a rule that omits `[rules.source]` (unless it is a pure `lag_behind` rule with `reads.projections = ["git_refs"]` and `scope = "*"`) is refused at load time with an explicit message naming the missing `[rules.source]` table — never guessed.

---

## 6. Where episodes, cursors, and overrides live

When there is no `tillr` store, the watcher stores its state in a single self-initializing SQLite file passed via `--state <path>` (`D-GW7`).

- **Explicit path, never a hidden global database.** Following the house rule against silent defaults and cross-workspace bleed (the same reason [the tillr operating skill](../../yolo-packs/tillr/skills/tillr-operating/SKILL.md) forbids a home-directory default), `--state <path>` (or `--store <path>` in `tillr` mode) is required on every stateful command (`eval`, `wait`, `seen`, `status`, `daemon`, `add`, `list`, `drop`, `ack`, `silence`, `resolve`, `rule`, `subscribe`). Only `lint` is stateless. An agent in a scratch project can pass `--state .watch/state.db` or a session-local path; nothing is shared across workspaces unless two commands point at the same file.
- **SQLite in WAL mode with read-before-write planning.** `--state` opens in `journal_mode = WAL` with `busy_timeout = 5000` and runs `Evaluate` on a read snapshot before applying changes in one short `IMMEDIATE` write transaction (the same `Plan` / `ApplyPlan` split landed at `7243353ec6` in `tools/tillr/watch/eval.go`), so `status` is strictly read-only and multiple concurrent `wait --owner <id>` processes never starve each other.
- **Tables owned by `--state`:**
  1. `watch_meta` — `schema_version` (`1`) and creation metadata, so incompatible future versions fail cleanly with exit code `3`.
  2. `source_cursors` — `(source_key, epoch, seq, byte_offset, dev, ino, updated_at)` tracking monotonic per-source progress and file rotation.
  3. `alert_episodes` — `(episode_id, rule_id, subject_key, state, severity, condition_fingerprint, opened_seq, last_observed_seq, observation_count, opened_at, last_observed_at, acked_at, acked_by, silenced_until, resolved_at, resolved_reason, bundle_json)` — identical in semantics to today's `alert_episodes`.
  4. `watch_events` — `(seq INTEGER PRIMARY KEY AUTOINCREMENT, ts, kind, rule_id, subject_key, episode_id, payload_json)` recording `watch.episode.opened` and `watch.episode.resolved` with a single monotonic `seq` across all sources. Notice cursors (`through_seq`, `--seen-through`, `subscribe --after`) index `watch_events.seq`, decoupling delivery cursors from any one input source's sequence numbers.
  5. `delivery_cursors` — `(owner TEXT PRIMARY KEY, shown_seq, seen_seq, waiting_since, last_wait_at)` for **every** receiver handle (`--owner <id>`, defaulting to `"foreground"`). Replacing today's hardcoded `if owner != "foreground"` check (`tools/tillr/watch/delivery.go`, `status.go`) lets any named agent (`--owner operator-session`, `--owner builder-1`, `--owner foreground`) hold an independent delivery cursor over rules whose `receiver` is `"*"` (the default) or matches `<id>`.
  6. `agent_watches` — `(id TEXT PRIMARY KEY, owner, rule_toml, fire_once, created_at, retired_at)` for one-command watches ([§5.2](#52-one-command-ad-hoc-watches-for-any-agent)).
  7. `watch_rule_overrides` — `(rule_id TEXT PRIMARY KEY, action, rule_toml, updated_at, updated_by, reason)` for live `rule disable` / `enable` / `set` / `add` / `reset`.
- **Attachment lock directory.** Alongside `<state>`, `<state>.attached/<sha256(abs(state))+owner>.lock` holds the POSIX `fcntl` read lock so `status` distinguishes a live `wait` process (`attached: true`) from a `SIGKILL`ed one (`attached: false`, `waiting_since` still set -> `"unattached (previous wait ended without exit cleanup)"`) per owner handle.

When `--store .tillr/state.db` **is** passed, whether these tables live in a sidecar `.tillr/watch.db` (making `.tillr/state.db` 100% read-only to the watcher) or stay inside `.tillr/state.db` (preserving [OQ-TW5](./tillr-watch.md#OQ-TW5)'s single-DB layout) is [OQ-GW3](#OQ-GW3).

---

## 7. How an agent in any harness is woken

### 7.1 The harness-neutral wait contract

No agent harness needs a custom extension to run a command and wake when that command exits or prints a line. The watcher keeps and generalizes the foreground `wait` contract (`D-GW8`):

1. **One command evaluates, waits, and prints notices.** `wait --state <path> [--rules <dir>] [--owner <id>] [--timeout <dur> | --follow] [--seen-through <seq>]` optionally records `--seen-through` for the previous batch, acquires the POSIX attachment read lock, runs an immediate evaluation pass, and either:
   - returns immediately with exit `0` if unconfirmed notices (`seq > seen_seq`) are already open or just opened; or
   - sleeps on `fsnotify` + source poll intervals (`wake`), re-evaluating on each signal until an episode opens for `--owner` (exit `0`) or `--timeout` elapses with nothing to report (exit `1`, printing `timed out with no pending notices` and the `re-arm:` command).
2. **Self-contained notice batch.** On wake, stdout prints one header line (`WATCH_NOTICE through_seq=<N>`, plus `TILLR_WATCH_NOTICE through_seq=<N>` in `--store` mode so existing callers and skills match byte-for-byte), one JSON line per notice containing the bounded `<= 8 KiB` `Bundle`, and one final `re-arm: <exact shell-quoted command with --seen-through N>` line.
3. **Explicit confirmation.** `wait` advances `shown_seq` to `<N>` when it writes stdout, and advances `seen_seq` only when the woken agent either runs the printed `re-arm:` command (carrying `--seen-through <N>`) or runs `seen --state <path> --owner <id> --through <N>`.

### 7.2 Delivery in Pi, Claude Code, and other harnesses

Because `wait` uses only process exit and line-buffered stdout, every agent harness maps onto it directly (`D-GW8`):

| Harness | How the agent attaches | How wakeup reaches the model | How confirmation and re-arm happen |
| :--- | :--- | :--- | :--- |
| **Pi** (persistent session watch) | `background_task` (`action: "start"`, `completionPolicy: "notify"`) running `wait --state '<path>' ... --follow`, with atomic initial `watch: {condition: "output", pattern: "WATCH_NOTICE", wake: true}` (or `"TILLR_WATCH_NOTICE"` in `--store` mode). | When an episode opens, `wait --follow` prints the notice header; Pi's one-shot output watch fires and wakes the model into a turn with no 2-hour timeout and no process restart. Unconfirmed headers repeat every 60s so a race between `logs` and re-registering `watch` never drops a wake. | The agent pages `background_task` `logs` from `afterByte`, re-registers the `output` watch, and runs `seen --state '<path>' --through <N>`. |
| **Pi** (one-shot ad-hoc wait) | `background_task` (`action: "start"`, `completionPolicy: "wake"`) running `wait --state '<path>' --owner '<id>'` (no `--follow`). | While the agent works on other files or goes idle, `wait` blocks in the background; when the episode opens, `wait` exits `0` and `completionPolicy: "wake"` starts or steers the next model turn automatically. | The agent reads the task output (`logs` with `afterByte: 0`) and either runs `seen --through <N>` (if `--once` finished the watch) or starts the printed `re-arm:` command. |
| **Claude Code** | Background `Bash` tool call (`run_in_background: true`, `timeout: 7200000`) running `wait --state '<path>' ... --timeout 110m`. | Exit `0` (notice batch) or quiet exit `1` after 110 minutes (inside Claude Code's 2-hour ceiling) wakes the Claude Code session with the command's stdout. | The agent starts the next background `Bash` call using the printed `re-arm:` line, which already carries `--seen-through <N>`. |
| **Codex CLI, Gemini CLI, Agy, or shell supervisor** | Background job tool where available, or synchronous `wait --state '<path>' --timeout <dur>` when the agent has no concurrent foreground work to do; headless supervisors can also stream `subscribe --state '<path>' --after <seq>`. | Background exit notification or foreground command return hands the `<= 8 KiB` JSON notice bundle straight back to the caller. | Re-run the printed `re-arm:` command or `seen --through <N>`. |

### 7.3 Crashes, compactions, handoffs, and multiple watchers

| Event | What happens | Why no alert is lost or duplicated |
| :--- | :--- | :--- |
| **Context compaction in the same session** | In Pi (`--follow`) or Claude Code (background `Bash`), the child `wait` process stays alive; `status --state <path>` reports `attached: true` via the `fcntl` read lock, so the post-compaction agent reuses the live watcher instead of spawning a second one. | `delivery_cursors` and `alert_episodes` live in SQLite, not in model context. |
| **Watcher process killed (`SIGKILL`) or session crashes mid-turn** | The kernel drops the `fcntl` read lock immediately; `shown_seq` may exceed `seen_seq` if a notice printed right before the crash. | The next `wait` for that `--owner` sees `shown_seq > seen_seq` (unconfirmed) and immediately reprints every notice above `seen_seq`. |
| **Two agents watch the same `--state` file for different roles** | Agent A passes `--owner builder` and Agent B passes `--owner reviewer`. Each gets its own row in `delivery_cursors` and its own lock file in `<state>.attached/`. | Confirming `seen` for `builder` never marks a notice seen for `reviewer`. |
| **Two `wait` processes start accidentally with the same `--owner`** | Both acquire shared `F_RDLCK` locks on the owner's lockfile; if the second starts without `--follow`, `acquireAttachment` probes whether a lock is already held and prints a stderr warning (`another watcher is already attached for owner <id>`), while SQLite `IMMEDIATE` transactions keep cursor updates serialized. | Explicit diagnostics surface duplicate watchers without corrupting cursors. |

---

## 8. How the general watcher and tillr depend on each other

Applying the six rules (`P1`–`P6`) of [every tillr utility has a written contract](./tillr-utility-contracts.md#4-the-six-rules) and [the tillr-watch contract](./tillr-utility-contracts/tillr-watch.md) gives a strict one-way dependency boundary (`D-GW9`):

```text
  tools/<general-watch>/engine      (zero imports from tillr/* or tillr-*)
        ▲                 ▲
        │                 │
  standalone CLI     tillr source adapter (reads tillr's contracted surfaces only)
```

1. **The general watch engine imports nothing from `tillr`.** Pattern state machines, expression evaluation, `Bundle` truncation, `jsonl` / `log` / `git` / `process` sources, SQLite `--state` persistence, and `wait` / `seen` / `status` / `add` / `list` / `drop` / `lint` live in a pure Go package with zero `tillr` module dependencies. Any change to `tillr-drain`, `tillr-item`, `tillr-run`, `tillr-land`, or `tillr-gate` cannot fail the general engine's build.
2. **Blank-importing `tillr-drain` (`RI-42`) is deleted, and `tillr-gate` kinds (`RI-46`) become watchable.** Today `tools/tillr-watch/main.go` links `tillr-drain/drain` solely for `init()` side effects on `event.Kinds()`, which still misses `tillr-gate`'s `main`-package events (`gate.verdict`, `gate.repair.verdict`, `gate.suite.run`). Under [the utility contracts design](./tillr-utility-contracts.md#5-what-a-contract-holds-and-how-each-kind-is-pinned), each `tillr` utility declares its emitted event kinds and contract example payloads in its own `contract/` directory, pinned by that provider's fast unit tests. The `tillr` watch adapter validates `reads.events` kinds and payload dot-paths against those `contract/` manifests (or a compiled snapshot generated from them), so:
   - `tillr-watch` links **zero** `tillr-drain`, `tillr-item`, `tillr-run`, `tillr-land`, or `tillr-gate` Go packages;
   - `gate.*` events can be watched on equal footing with `drain.*`, `item.*`, `run.*`, and `land.*` events; and
   - if a provider renames a contracted payload field (`item_id`, `category`, `outcome`, `verdict`), the provider's own unit test fails before merge (`P3` / `P5`).
3. **Store reads stay inside `tillr`'s table contract.** The `tillr` source adapter opens `.tillr/state.db` read-only (`store.OpenReadOnly`) to query `events`, `items`, `workflow_selections`, and `meta` (`definition.ForReader`), mapping each `events` row's `seat`, `object_type`, `object_id`, `revision`, and JSON `payload` into `Observation.fields` with the exact precedence of today's `lookupField` (`payload` key first, then envelope column).
4. **Exit codes match `tillr/record` (`RI-46` resolved).** Both the general watcher and `tillr watch` use one exit-code table (`D-GW10`):
   - `0` — success (`wait` delivered at least one notice; `eval`, `status`, `seen`, `add`, `list`, `drop`, or `lint` succeeded);
   - `1` — negative answer (`wait --timeout` elapsed with no pending notices; `lint` found rule errors);
   - `2` — invalid CLI usage or malformed flag;
   - `3` — incompatible `--state` or `--store` schema version;
   - `4` — external I/O failure (unreadable directory, SQLite I/O error, Git execution failure);
   - `5` — internal invariant error.
   Because [the tillr operating skill](../../yolo-packs/tillr/skills/tillr-operating/SKILL.md) and [the timed drain check-in skill](../../.agents/skills/drain-timed-checkin/SKILL.md) currently expect exit `2` on quiet `wait --timeout`, the slice that switches timeout from `2` to `1` updates those skill instructions and `tools/tillr-watch` tests in the same commit.

---

## 9. The build, in slices that each land alone

Every slice below is lower priority than the drain's own work ([§1](#1-what-the-owner-said)). Slices 1 through 7 do **not** wait on [OQ-GW1](#OQ-GW1), [OQ-GW2](#OQ-GW2), or [OQ-GW3](#OQ-GW3): they separate the engine, add `--state` and passive sources, fix multi-owner cursors, and remove the `tillr-drain` reach-in behind the existing `tillr-watch` binary while keeping every existing `config/tillr/watch/*.toml` rule green (`D-GW12`). Slices 8 through 10 carry out the three owner rulings once answered.

| Slice | What it builds | Done when | Follows |
| :--- | :--- | :--- | :--- |
| **1** | **Source-neutral engine seam.** Extract `Observation` and `ObservationSource` inside `tools/tillr/watch`; move `.tillr/state.db` `events` reading, `items` / `charter` / `workflow_selections` projections, and `core | charter:* | workflow:*` scope checks behind a `TillrStoreSource` implementation; make `git_refs` independent of `tillr/store`. | Unit tests run all six pattern evaluators, clearing events, `group_by` skip counts, `git_refs`, and `8 KiB` truncation against an in-memory `ObservationSource` with zero SQLite `tillr` store open; all existing `tools/tillr/watch` and `tools/tillr-watch` tests pass unchanged. | — |
| **2** | **Standalone SQLite watch state (`--state`) and multi-owner cursors.** Add the self-initializing `--state <path>` SQLite backend (`watch_meta`, `source_cursors`, `alert_episodes`, `watch_events`, `delivery_cursors`, `watch_rule_overrides`) and remove the hardcoded `owner != "foreground"` filter in both `--state` and `--store` modes so any `--owner <id>` receives notices matching `receiver` (`"*"` default or `<id>`). | `eval`, `status`, `wait`, `seen`, and `daemon` run end-to-end with `--state <tmp>/watch.db` and no `--store`; a unit test with `--owner operator-session` and `--owner builder` proves independent `shown_seq` / `seen_seq` delivery and `fcntl` attachment reporting per owner. | 1 |
| **3** | **Appended file sources (`jsonl` and `log`).** Implement `jsonl` and `log` (RE2 named-capture) `ObservationSource` adapters with `fsnotify` parent-directory wakeups, `(dev, ino, byte_offset, epoch)` rotation/truncation handling, missing-file readiness reporting, and partial-line holding. | Fast unit tests cover live append wakeup, missing-at-start creation, log truncation cursor reset, malformed JSONL skip counting, and RE2 capture fields in predicates and guidance templates. | 2 |
| **4** | **Git and local process sources (`git` and `process`).** Promote `git` to a standalone `ObservationSource` (ref movement events + `lag_behind` with `--state` only) and add `process` (`pid` with start-time reuse guard, `pid_file`, and POSIX `lock_file` probe). | Unit tests verify branch-lag episodes without `.tillr/state.db`, child-process exit wakeup, PID-reuse rejection, and `fcntl` lockfile release detection. | 2 |
| **5** | **One-command ad-hoc watches (`add`, `list`, `drop`) and rule management (`ack`, `silence`, `resolve`, `lint`).** Add the `agent_watches` table and CLI faces so an agent can register a `--once` or standing watch in one command, manage open episodes, and pre-flight rule files with `lint`. | Unit tests verify `add ... --once` holds the watch across an unconfirmed `wait`, retires it on `seen --through`, survives process restart, and `lint` rejects any `remedy` field or malformed regex/expression. | 3, 4 |
| **6** | **Contract-driven `tillr` event validation (deletes `RI-42` and fixes `RI-46` event kinds).** Replace the blank `_ "github.com/mschulkind-oss/tillr-drain/drain"` import in `tools/tillr-watch/main.go` with contract-manifest validation over `tillr`'s provider `contract/` entries, including `tillr-gate` event kinds. | `go list -deps ./tools/tillr-watch` contains zero `tillr-drain`, `tillr-item`, `tillr-run`, or `tillr-land` packages; a rule naming `gate.verdict` loads and evaluates; existing `config/tillr/watch/*.toml` rule tests pass. | 1, and [utility contracts slice 2](./tillr-utility-contracts.md#11-what-i-would-build-in-order) |
| **7** | **Exit-code alignment (`RI-46`) and cross-harness wakeup guide.** Align CLI exit codes with `0..5` (`1` for quiet `wait --timeout`, `2` for usage), updating `tillr-operating` and `drain-timed-checkin` in lockstep when allowed paths include them, and ship harness recipes for Pi (`--follow` and `completionPolicy: "wake"`), Claude Code, and shell loops. | Conformance tests assert exit codes `0`, `1`, `2`, `3`, `4` on representative cases; no caller branches on exit `2` for timeout. | 2 |
| **8** | **Command name and yolo pack delivery ([OQ-GW1](#OQ-GW1)).** Package the standalone binary and launcher in the pack and command name chosen by [OQ-GW1](#OQ-GW1), updating `pack-sync` lists and tests together. | An isolated jail without `tools/` on `PATH` runs the watcher from its pack and passes `tests/pack-sync.test.py`. | 5, 7, and [OQ-GW1](#OQ-GW1) |
| **9** | **Bounded `command` probe source (if [OQ-GW2](#OQ-GW2) chooses Option B).** Add the shell-less `argv` `command` source with minimum interval, per-probe timeout, `256 KiB` cap, and `lint` checks. (Skipped if [OQ-GW2](#OQ-GW2) chooses Option A.) | Unit tests verify JSON and line output parsing, non-zero exit capture, timeout termination, and static refusal of shell-eval or post-alert execution. | 2, and [OQ-GW2](#OQ-GW2) |
| **10** | **`tillr` episode storage unification (if [OQ-GW3](#OQ-GW3) chooses Option A).** Move `--store` episode, cursor, and override persistence to sidecar `.tillr/watch.db`, keeping `.tillr/state.db` read-only from the watcher and updating `tillr-view` to read the sidecar contract. (Skipped if [OQ-GW3](#OQ-GW3) chooses Option B.) | Watcher runs against a read-only `.tillr/state.db` file (`0444` permissions in test) without error; `tillr-view` contract tests pass. | 2, 6, and [OQ-GW3](#OQ-GW3) |

Slices 3, 4, 6, and 7 depend only on Slice 1 or Slice 2 and can be built in parallel in separate lanes.

---

## 10. What this does not do

- **It does not execute remedies, hooks, webhooks, or recovery scripts when an alert fires.** Watching is strictly observational ([OQ-TW2](./tillr-watch.md#OQ-TW2)).
- **It does not fail, stop, bench, or cap any agent run, background task, or queue item.** Alerts wake a reasoning agent; they never enforce attempt ceilings or wall-clock budgets ([D-TW2](./tillr-watch.md#D-TW2), [the momentum principle](./momentum-principle.md)).
- **It does not add a Pi extension, Claude Code hook, or harness-specific turn injector.** Foreground/background command execution (`wait` + `seen`) works across every harness without custom runtime plugins.
- **It does not build a network daemon, central alert broker, or cross-machine pub/sub service.** Every source and state file is local to the workspace or jail that runs the watcher.
- **It does not change the syntax or meaning of any existing rule in `config/tillr/watch/`.** All five rules landed on trunk keep evaluating identically when `--store` is passed.
- **It does not pre-empt active `tillr-drain` work.** Nothing here runs ahead of the drain's own rulings and reliability fixes.

---

## 11. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Build a separate lightweight `agent-watch` tool from scratch and leave `tillr-watch` untouched | **Rejected:** duplicates six temporal pattern state machines, clearing-event logic, RE2/predicate evaluation, `8 KiB` bundle truncation, POSIX lock attachment, and `wait` / `seen` delivery, and directly contradicts the owner's request to generalize `tillr-watch` so it works *"with or without the rest of tiller"*. |
| Require non-tillr agents to initialize a full `.tillr/state.db` (`tillr store init`) and append events with `tillr store` | **Rejected:** forces every non-tillr project to carry `tillr`'s item/run/lane/charter schema and rewrite external logs or Git checks into `tillr` envelopes before watching anything. |
| Store standalone episodes in a plain JSON file instead of SQLite `--state` | **Rejected:** concurrent `wait --owner`, `seen`, `status`, and `add` processes need atomic read-snapshot planning and serialized cursor updates without torn reads or lockfile races; SQLite WAL is already linked and proven in `tools/tillr/watch`. |
| Use a hidden `~/.local/state/agent-watch/default.db` when `--state` is omitted | **Rejected:** silently mixes episodes and `seen_seq` cursors across unrelated workspaces and jails, violating the house no-fallback rule and repeating the cross-checkout hazard already barred in `tillr-operating`. |
| Deliver wakes through a Pi TypeScript extension (`sendMessage` / `triggerTurn`) and separate per-harness plugins | **Rejected:** the owner already ruled out a Pi turn-injection extension in [OQ-TW3](./tillr-watch.md#OQ-TW3) (2026-11-21) in favor of an ordinary foreground `wait` command, which works in Pi, Claude Code, Codex CLI, Gemini CLI, and plain shell scripts alike. |
| Keep blank-importing `tillr-drain` and add blank imports for `tillr-gate` to register event kinds | **Rejected:** `tillr-gate` is a `package main` module so it cannot be blank-imported ([RI-46](./tillr-utility-contracts.md#106-the-library-the-store-the-watch-and-the-dispatcher)), and linking every utility into the watcher violates [every tillr utility has a written contract](./tillr-utility-contracts.md). |

---

## 12. Risks

| Risk | Mitigation |
| :--- | :--- |
| A rule watching a multi-gigabyte log file scans too much data per wakeup | `log` and `jsonl` track `(dev, ino, byte_offset)` in `source_cursors` so steady-state wakeups read only newly appended bytes, and initial/rotated scans are capped by `max_scan_bytes` (default `4 MiB`) and `[now - window, now]` ([§4.2](#42-non-tillr-observation-sources), [§4.4](#44-missing-unreadable-rotated-or-truncated-sources)). |
| Log rotation or `truncate` leaves `byte_offset` past EOF and blinds the rule | `(dev, ino)` mismatch or `size < byte_offset` increments the source cursor's `epoch`, resets `byte_offset` to `0`, and emits an explicit stderr diagnostic ([§4.4](#44-missing-unreadable-rotated-or-truncated-sources)). |
| OS recycles a numeric PID after a watched process exits, masking the exit | The `process` source records process start time alongside `pid` on first read (or uses POSIX `fcntl` `lock_file` liveness) and treats a start-time mismatch as `process.exited` ([§4.4](#44-missing-unreadable-rotated-or-truncated-sources)). |
| Changing timeout exit code from `2` to `1` surprises an existing skill prompt | Slice 7 updates `tillr-operating`, `drain-timed-checkin`, and CLI conformance tests in the same commit, and stdout still prints the unmistakable `timed out with no pending notices` + `re-arm:` block ([§8](#8-how-the-general-watcher-and-tillr-depend-on-each-other)). |
| Work on generalizing watch distracts from urgent `tillr-drain` stabilization | Routed at the bottom of [the tillr roadmap](../tillr/roadmap.md) behind active drain rulings and work, per the owner's explicit priority instruction ([§1](#1-what-the-owner-said)). |

---

## Open Questions

1. 💬 **OQ-GW1: What command name and pack should ship the general watcher?**

   An agent in a repository with no `tillr` store wants to watch a build log and wake when an error pattern appears. Today `tillr-watch` is reached as `tillr watch` and is built only inside this repository ([D-TW24](./tillr-watch.md#D-TW24)); another jail does not have it on `PATH`. Giving the general watcher its own binary name and pack keeps non-tillr jails independent of `tillr`, while keeping `tillr-watch` preserves one command name across every skill and repository.

   - **A — Ship a standalone `agent-watch` binary and pack, with `tillr watch` delegating to it.** Any jail gets `agent-watch` on `PATH` without installing `tillr`. Existing `tillr watch --store ...` calls keep working unchanged through the `tillr` wrapper. Skills use `agent-watch` outside `tillr` and `tillr watch` (or `agent-watch`) inside it.

   - **B — Keep one binary name `tillr-watch` (`tillr watch`) and ship it in a small pack.** Agents with or without a `tillr` store run `tillr-watch --state ...` (or `tillr watch`). Every skill and notice uses one name everywhere, even in workspaces that never touch the rest of `tillr`.

   <!-- vantage: question id=OQ-GW1 leaning="A (Ship a standalone agent-watch binary and pack, with tillr watch delegating to it) — you asked for the command to be usable with or without the rest of tillr, and a standalone binary and pack lets non-tillr jails include it cleanly while tillr watch stays an unbroken alias." -->

   _Leaning:_ A (Ship a standalone `agent-watch` binary and pack, with `tillr watch` delegating to it) — you asked for the command to be usable with or without the rest of `tillr`, and a standalone binary and pack lets non-tillr jails include it cleanly while `tillr watch` stays an unbroken alias.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-GW2: May a watch rule run a configured probe command on an interval, or read passive sources only?**

   An agent wants an alert when a remote pull-request check fails or a local HTTP endpoint stops answering. Passive sources (`jsonl`, `log`, `git`, `process`) read local files, Git refs, and kernel process state without starting subprocesses. Allowing a `command` source lets a rule poll a read-only command such as `gh pr checks --json` directly, without a separate helper loop writing to a file first. Running a user-supplied command inside the watcher crosses from passive reading into active execution, so a careless probe command could mutate state even though alerts themselves run no remedy ([OQ-TW2](./tillr-watch.md#OQ-TW2)).

   - **A — Passive sources only (`jsonl`, `log`, `git`, `process`, and `tillr`); no `command` source.** The watcher never executes user commands. To watch an external API or check, the agent starts its own background loop that appends JSON lines to a file and watches that file.

   - **B — Allow a `command` source with shell-less `argv`, an explicit minimum interval, and bounded output.** A rule may poll a command directly with no wrapper script. Probes run only to read input (`argv` array, no shell, `>= 5s` interval, `<= 30s` timeout), and never in response to an alert firing.

   <!-- vantage: question id=OQ-GW2 leaning="B (Allow a command source with shell-less argv, an explicit minimum interval, and bounded output) — watching CI checks, ports, or external tools is one of the commonest reasons an agent polls in loops today, and forbidding shell eval plus banning post-alert commands keeps OQ-TW2 intact." -->

   _Leaning:_ B (Allow a `command` source with shell-less `argv`, an explicit minimum interval, and bounded output) — watching CI checks, ports, or external tools is one of the commonest reasons an agent polls in loops today, and forbidding shell eval plus banning post-alert commands keeps [OQ-TW2](./tillr-watch.md#OQ-TW2) intact.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-GW3: When watching a `tillr` store, should alert episodes and rule overrides move to a sidecar watch file or stay inside `.tillr/state.db`?**

   Without a `tillr` store, the watcher keeps its episodes, delivery cursors, and live rule overrides in its own SQLite file (`--state`). When `--store .tillr/state.db` is passed, today's `tillr-watch` writes those tables and `watch.alert.*` events into `.tillr/state.db` (where you placed live overrides in [OQ-TW5](./tillr-watch.md#OQ-TW5) on 2026-11-21). Now that `.tillr/state.db` becomes one input source among others, moving watch state to a sidecar `.tillr/watch.db` makes `.tillr/state.db` strictly read-only to the watcher and drops a second storage backend, while keeping them inside `.tillr/state.db` preserves a single database file for Observatory and `tillr store`.

   - **A — Use a sidecar `.tillr/watch.db` alongside `.tillr/state.db` (revisiting [OQ-TW5](./tillr-watch.md#OQ-TW5)).** `.tillr/state.db` is strictly read-only to the watcher, so a `tillr-drain` or `tillr-land` write transaction never blocks `wait` or `seen`, `tillr-store` owns zero watch tables, and one SQLite watch store serves every mode. `tillr-view` reads `.tillr/watch.db` for alerts.

   - **B — Keep episodes, overrides ([OQ-TW5](./tillr-watch.md#OQ-TW5)), and `watch.alert.*` events inside `.tillr/state.db` when `--store` is passed.** Observatory and `tillr store` read one database file as settled on 2026-11-21, and `--state` is used only when `--store` is omitted. The codebase keeps two episode-storage backends, and watcher writes share `.tillr/state.db`'s writer lock.

   <!-- vantage: question id=OQ-GW3 leaning="A (Use a sidecar .tillr/watch.db alongside .tillr/state.db) — treating .tillr/state.db as a strictly read-only source matches 'one source among others', eliminates writer-lock contention with the drain, and leaves one episode store to maintain." -->

   _Leaning:_ A (Use a sidecar `.tillr/watch.db` alongside `.tillr/state.db`) — treating `.tillr/state.db` as a strictly read-only source matches "one source among others", eliminates writer-lock contention with the drain, and leaves one episode store to maintain.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

**Who decided each row.** Rows `R-GW1` to `R-GW4` quote dated statements of the owner and are the only owner rulings. Every `D-GW*` row is the designer's decision of 2026-11-22, made from the filed request without him, and he may overturn any of them. Nothing in `D-GW1` to `D-GW12` is built.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| R-GW1 | **Owner ruling.** Generalize `tillr-watch` at lower priority so any agent can use it for any purpose, with or without the rest of `tillr` (*"I want, you can file this as a lower priority, but I want this tiller watch command to be usable by any agent for any purpose that it wants, with or without the rest of tiller. This seems like such a useful thing to have some alerting system for agents. Let's generalize it somehow."*). Quoted in full in [§1](#1-what-the-owner-said). | 2026-11-21, owner | [§1](#1-what-the-owner-said) | — |
| R-GW2 | **Owner ruling ([OQ-TW2](./tillr-watch.md#OQ-TW2)).** Watch rules execute no automatic remedy (*"Every single one of these sounds like a fallback to cover an issue. We don't cover issues. We use this to find the issues and fix them."*). | 2026-11-21, owner | [§1](#1-what-the-owner-said), [§3](#3-principles-and-standing-boundaries) | — |
| R-GW3 | **Owner ruling.** No silent fallbacks (*"I hate fallbacks... We need deliberate choices, not fallbacks"*, [the judge and lifecycle design](./tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27)); missing or malformed inputs are reported explicitly. | 2026-11-20, owner | [§1](#1-what-the-owner-said), [§3](#3-principles-and-standing-boundaries), [§4.4](#44-missing-unreadable-rotated-or-truncated-sources) | — |
| R-GW4 | **Owner ruling.** Separate `tillr` utilities have written contracts stating what each provides and expects, pinned by fast unit tests (*"all of the Tiller utilities should have a contract. They are separate utilities, so they need to have a defined API and what they expect from others"*, [every tillr utility has a written contract](./tillr-utility-contracts.md#1-the-ruling)). | 2026-11-21, owner | [§1](#1-what-the-owner-said), [§8](#8-how-the-general-watcher-and-tillr-depend-on-each-other) | — |
| D-GW1 | **Designer's decision; the owner may overturn it.** Split today's `tillr-watch` along five exact seams (`C1` event-kind validation, `C2` observation stream, `C3` scopes and projections, `C4` standalone SQLite state and multi-owner cursors, `C5` CLI/exit-code contract), keeping the six temporal pattern state machines, total expression evaluator, `8 KiB` truncating bundle, POSIX `fcntl` attachment lock, and `wait` / `seen` flow intact. Also preserves the filed request's standing no-count / no-wall boundary ([D-TW2](./tillr-watch.md#D-TW2), [the momentum principle](./momentum-principle.md)). **Rejected:** building a second independent alerting tool from scratch. | 2026-11-22, designer | [§2.2](#22-the-five-couplings-to-the-tillr-store-and-how-each-separates), [§3](#3-principles-and-standing-boundaries), [§11](#11-alternatives-considered) | — |
| D-GW2 | **Designer's decision; the owner may overturn it.** Normalize all inputs into `(seq, ts, kind, fields)` `Observation` records through a read-only `ObservationSource` interface (`ReadWindow`, `Project`, `WatchTargets`). | 2026-11-22, designer | [§4.1](#41-the-observation-record-and-source-contract) | — |
| D-GW3 | **Designer's decision; the owner may overturn it.** Ship four passive non-tillr sources in core (`jsonl`, `log` with RE2 named captures, `git`, and `process` with start-time / POSIX lockfile liveness) plus general `git_refs` and `process` projections; gate active `command` probes on [OQ-GW2](#OQ-GW2); isolate `items`, `charter`, and `workflow_selections` inside the `tillr` source adapter. | 2026-11-22, designer | [§4.2](#42-non-tillr-observation-sources), [§4.3](#43-projections-without-a-tillr-store) | — |
| D-GW4 | **Designer's decision; the owner may overturn it.** Handle non-existent files (`source not ready` + parent-dir `fsnotify`), file rotation/truncation (`(dev, ino, byte_offset, epoch)` cursor reset with stderr notice), partial/malformed JSONL lines (hold incomplete trailing line; count and report bad lines), and PID reuse (verify process start time) explicitly with zero silent fallbacks. | 2026-11-22, designer | [§4.4](#44-missing-unreadable-rotated-or-truncated-sources) | — |
| D-GW5 | **Designer's decision; the owner may overturn it.** Extend TOML rules with optional `scope` (default `"*"`) and `[rules.source]` (or `[sources.<alias>]`), while defaulting omitted `[rules.source]` to the `--store` `tillr` adapter when `--store` is passed and refusing omitted `[rules.source]` when `--store` is absent, so all existing `config/tillr/watch/*.toml` rules keep their exact meaning. | 2026-11-22, designer | [§5.1](#51-standing-toml-rules-without-a-tillr-store), [§5.3](#53-how-existing-tillr-rules-keep-their-exact-meaning) | — |
| D-GW6 | **Designer's decision; the owner may overturn it.** Provide one-command ad-hoc watches (`add`, `list`, `drop`) persisted in `agent_watches` with `--once` retiring only on confirmed `seen --through <seq>`. | 2026-11-22, designer | [§5.2](#52-one-command-ad-hoc-watches-for-any-agent) | — |
| D-GW7 | **Designer's decision; the owner may overturn it.** Persist standalone episodes, monotonic `watch_events`, per-owner `delivery_cursors`, `source_cursors`, `agent_watches`, and `watch_rule_overrides` in an explicit `--state <path>` SQLite WAL database (no hidden `~/.local` default), and remove the hardcoded `owner != "foreground"` filter so any `--owner <id>` receives notices matching `receiver` (`"*"` default or `<id>`). **Rejected:** plain JSON state files and implicit home-directory state files. | 2026-11-22, designer | [§6](#6-where-episodes-cursors-and-overrides-live), [§11](#11-alternatives-considered) | — |
| D-GW8 | **Designer's decision; the owner may overturn it.** Wake agents in any harness through the standard `wait` process (`--follow` + output watch or exit-wake `completionPolicy: "wake"` in Pi, `--timeout 110m` background `Bash` in Claude Code, background/foreground shell or `subscribe` elsewhere) with `WATCH_NOTICE` header (`TILLR_WATCH_NOTICE` preserved in `--store` mode), `<= 8 KiB` JSON bundles, `re-arm:` command, and explicit `seen` confirmation. **Rejected:** harness-specific turn-injection extensions. | 2026-11-22, designer | [§7](#7-how-an-agent-in-any-harness-is-woken), [§11](#11-alternatives-considered) | — |
| D-GW9 | **Designer's decision; the owner may overturn it.** Give the general watch engine zero `tillr` imports, delete the blank `tillr-drain` import (`RI-42`), and validate `tillr` event kinds and payload paths (including `tillr-gate`'s `gate.*` events, `RI-46`) against provider `contract/` entries under [every tillr utility has a written contract](./tillr-utility-contracts.md). | 2026-11-22, designer | [§8](#8-how-the-general-watcher-and-tillr-depend-on-each-other) | — |
| D-GW10 | **Designer's decision; the owner may overturn it.** Align exit codes to `0` (ok / notices), `1` (quiet `wait --timeout` / `lint` negative), `2` (usage), `3` (incompatible schema), `4` (external I/O), `5` (internal), updating skill prompts in the same slice (`RI-46`). | 2026-11-22, designer | [§8](#8-how-the-general-watcher-and-tillr-depend-on-each-other) | — |
| D-GW11 | **Designer's decision; the owner may overturn it.** Route this design and its open questions at the bottom of [the tillr roadmap](../tillr/roadmap.md) behind active drain rulings and work, honoring `R-GW1`. | 2026-11-22, designer | [§1](#1-what-the-owner-said), [§9](#9-the-build-in-slices-that-each-land-alone) | — |
| D-GW12 | **Designer's decision; the owner may overturn it.** Cut the build into the ten independently landable slices of [§9](#9-the-build-in-slices-that-each-land-alone), where Slices 1 to 7 land behind the existing binary without waiting on [OQ-GW1](#OQ-GW1)–[OQ-GW3](#OQ-GW3) and Slices 8 to 10 carry out those rulings. | 2026-11-22, designer | [§9](#9-the-build-in-slices-that-each-land-alone) | — |
