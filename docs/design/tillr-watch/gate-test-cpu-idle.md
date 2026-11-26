---
title: "Alert when a running gate's test binary stops using CPU"
author: "Agent"
date: 2026-11-25
status: in-review
stage: DECIDED
stage-since: 2026-11-25
tags: [tillr, watch, drain, gate, cpu, design]
summary: "Record loop-wide 1-minute per-binary subtree CPU deltas on drain.gate-test-cpu from tillr-drain's session sampler across live detached gates, and watch them with a consecutive_count for_each rule (gate-test-cpu-idle); split into two single-behavior follow-up build items (the event, then the rule)."
next: "Run the two ordered follow-up build items in Section 4 — first recording drain.gate-test-cpu in tools/tillr-drain/drain/, then adding config/tillr/watch/gate-test-cpu-idle.toml and its evaluation tests; the owner may review D-TW-GTCI1."
depends-on:
  - ../tillr-watch.md
  - ../tillr-watch.md#D-TW2
  - ../tillr-watch.md#D-TW13
  - ../tillr-watch.md#D-TW41
  - ../tillr-watch.md#D-TW42
  - ../tillr-watch.md#D-TW73
  - ../tillr-watch.md#D-TW81
  - ./decision-record-files.md#R-TW-DR1
  - ./decision-record-files.md#D-TW-DR1
  - ../landing-principles.md#15-land-the-smallest-functional-change
related:
  - ../tillr-utility-contracts/tillr-watch.md
vantage:
  status-chip: true
---

# Alert when a running gate's test binary stops using CPU

**Status:** 2026-11-25, verified at `95376360db`. Nothing specified in [`D-TW-GTCI1`](#D-TW-GTCI1) is built yet (`rg -n 'gate-test-cpu' tools config` returns nothing, and `test -f config/tillr/watch/gate-test-cpu-idle.toml` exits non-zero). Per owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`685934ce`) and [`D-TW-DR1`](./decision-record-files.md#D-TW-DR1), this decision record lives in its own standalone file under `docs/design/tillr-watch/` with zero edits to [`../tillr-watch.md`](../tillr-watch.md).

> **In short.** No event recorded while a candidate gate runs carries the CPU usage of its test binaries, and check-group watchdogs stay silent whenever sibling shards keep burning CPU or writing output. Having `Loop` in `tools/tillr-drain/drain` emit one loop-wide 1-minute `drain.gate-test-cpu` snapshot across all live detached gates lets a `consecutive_count` rule with `for_each = "processes"` (`gate-test-cpu-idle`) open one alert per hung test binary so the responder can send `SIGQUIT` to that test binary alone, read its goroutine dump, and file the repair without ending, failing, or retrying the gate.

**Why it matters.** On 2026-11-24, `item-request-2a5d911104` had to be closed unbuilt because no live store event or projection records per-test-binary CPU during a running gate, and eight sibling alert-rule requests worded `"one rule, one new file, change nothing else"` each cost a planning run and a design-seat answer ([§1.1](#11-what-happened-on-2026-11-24-and-why-no-existing-fact-suffices)).

**The shape.** `Loop` samples live detached gates (`gate.face.json`) once per minute alongside `drain.process-cpu`, sums subtree CPU ticks (`utime + stime + cutime + cstime`) per live Go `*.test` binary into `drain.gate-test-cpu`, and `gate-test-cpu-idle` matches two consecutive full 1-minute zero-CPU windows via `consecutive_count` and `for_each = "processes"` ([§2](#2-recorded-fact-draingate-test-cpu-d-tw-gtci1), [§3](#3-landed-pattern-and-rule-contract-gate-test-cpu-idle-d-tw-gtci1)).

**Cost.** Two single-behavior follow-up build items — first the `drain.gate-test-cpu` producer in `tools/tillr-drain` (emitting zero events when no gate runs test binaries), then `config/tillr/watch/gate-test-cpu-idle.toml` and its tests — with zero rule-language or database-schema changes ([§4](#4-follow-up-build-items-and-candidate-notes-d-tw-gtci1)).

**Start at [§2](#2-recorded-fact-draingate-test-cpu-d-tw-gtci1)** for the event contract and single writer, [§3](#3-landed-pattern-and-rule-contract-gate-test-cpu-idle-d-tw-gtci1) for the `consecutive_count` rule, and [§4](#4-follow-up-build-items-and-candidate-notes-d-tw-gtci1) for the two follow-up build items.

**Needs your ruling:** None. Owner rulings [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), and [`D-TW73`](../tillr-watch.md#D-TW73) are already settled; [`D-TW-GTCI1`](#D-TW-GTCI1) comes from the operator request and the designer and may be overturned by the owner.

**Reads with:** [`../tillr-watch.md`](../tillr-watch.md) (the baseline watch design), [`./decision-record-files.md`](./decision-record-files.md) (standalone watch decision records), and [`../landing-principles.md`](../landing-principles.md) (smallest functional change).

---

## 1. Problem, Existing Facts, and Authority Boundary

### 1.1 What happened on 2026-11-24 and why no existing fact suffices

On 2026-11-24, the operator filed `item-request-2a5d911104` worded `"one rule, one new file, change nothing else"` to alert when a running gate's test processes have stopped using CPU, with guidance to send `SIGQUIT` to the test binary only, read the goroutine dump from the gate log, and file the test's repair without ending, failing, or retrying the gate. The item was closed before dispatch because inspecting [`tools/tillr/watch/rules.go`](../../../tools/tillr/watch/rules.go) and the registered event producers showed that none of the six landed patterns (`predicate_match`, `count_in_window`, `consecutive_count`, `alternating_pair`, `lag_behind`, `duration_without_event`) had a recorded fact to read while a gate is still running:

1. **`gate.started`** (`GateStartedKind` in [`tools/tillr-drain/drain/events.go`](../../../tools/tillr-drain/drain/events.go)) is appended once before checks run with `{item_id, run_id, lane_id, base_sha}` (`ObjectType = "run"`, `ObjectID = run_id`) and carries no process or CPU data.
2. **`gate.plan` and `gate.verdict`** (`GatePlanKind` and `GateVerdictKind` in [`tools/tillr-drain/drain/events.go`](../../../tools/tillr-drain/drain/events.go), appended in `finishDetachedGate` in [`tools/tillr-drain/drain/steps.go`](../../../tools/tillr-drain/drain/steps.go)) are recorded only after the gate exits, so `cpu_time_ms` is strictly post-mortem.
3. **`drain.process-cpu`** (`ProcessCPUKind` in [`tools/tillr-drain/drain/process_cpu.go`](../../../tools/tillr-drain/drain/process_cpu.go)) samples only `drain-loop`, `tillr-watch-wait`, and `tillr-view-serve`. Mixing short-lived gate test binaries into `drain.process-cpu` would skew its daemon summary fields (`max_cpu_percent`, `max_role`, `max_pid` — where a normal multi-threaded Go test binary routinely exceeds 100% CPU) and force the gate-test rule to evaluate every 1-minute daemon sample when zero gates are active.
4. **Projections** (`items`, `charter`, `git_refs`, `fixer_checks`, `green_proof` in [`tools/tillr/watch/rules.go`](../../../tools/tillr/watch/rules.go)) reflect queue, charter, and ref state rather than `/proc` process accounting.

Existing gate watchdogs also leave this state uncovered:

- **Check-group watchdog in `tillr-gate` (`watchCommand` in [`tools/tillr-gate/watchdog.go`](../../../tools/tillr-gate/watchdog.go)):** observes aggregate check-log byte growth and check-wide CPU progress across the entire check process group. In a sharded check (`tools/go-test-shards.py`, where `rg -n 'watchdog|SIGQUIT' tools/go-test-shards.py` returns nothing), one deadlocked `*.test` shard sits at zero CPU unseen for as long as sibling shards burn CPU or print output; once sibling shards finish, `watchCommand` waits 60 silent 1-second samples and terminates the entire check process group (`ExitStalled = 6`) rather than keeping the gate alive or isolating the hung test binary's goroutine dump.
- **Per-binary watchdog in `go_affected.py` (`_watch_go_test_binaries` in `tools/gate/go_affected.py`, now [`tools/tillr-gate/goaffected`](../../../tools/tillr-gate/goaffected/)):** covers only the repository-local `go-affected` check, not `go-test-shards.py` checks, direct `go test` checks, or consumer repositories' gate checks, and signals a given `(pid, start_time)` at most once.

### 1.2 Placement and authority boundary

Although the 2026-11-24 request named `docs/design/tillr-watch.md` before `docs/design/tillr-watch/decision-record-files.md` landed on 2026-11-25 (`685934ce`), four standing owner rulings govern this document without modification:

- [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`2026-11-25`, `"ok, so save that"`): every new decision record about `tillr-watch` goes in its own small file under `docs/design/tillr-watch/`, and [`../tillr-watch.md`](../tillr-watch.md) is not edited when a record is added.
- [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2) (`2026-11-16`, `"Explicitly, we NEVER want fallbacks anywhere in this project."`): no fallback writer, fallback `/proc` parser, or automatic remedy exists.
- [`OQ-TW2`](../tillr-watch.md#OQ-TW2) (`2026-11-21`, `"We don't cover issues. We use this to find the issues and fix them."`): `tillr-watch` executes no signal or gate action; guidance is read and acted on by the responder.
- [`D-TW73`](../tillr-watch.md#D-TW73) (`2026-11-22`, `"I never want alerts for expected conditions."`): `[rule.when]` excludes partial startup windows, active child subprocesses, and sub-minute `go_affected.py` watchdog reaps, and `[rule.guidance]` never calls any firing expected.

Everything else in this document — [`D-TW-GTCI1`](#D-TW-GTCI1), choosing the `drain.gate-test-cpu` event schema, single `Loop` writer, subtree CPU accounting, `consecutive_count` + `for_each` rule parameters, rejected alternatives, and the two follow-up build items — is an **operator-request and designer decision (`2026-11-25`), not an owner ruling, and the owner may overturn any part of it**.

---

## 2. Recorded Fact: `drain.gate-test-cpu` ([`D-TW-GTCI1`](#D-TW-GTCI1))

*Non-owner decision (from the 2026-11-24 operator request and the designer; the owner may overturn any part of this section).*

### 2.1 Event name, registration, and the one writer

- **Event kind:** `GateTestCPUKind = "drain.gate-test-cpu"`, registered in `init()` under `drain.Utility` (`"drain"`) in `tools/tillr-drain/drain` via `event.Register(GateTestCPUKind, Utility)`. Because [`tools/tillr-watch/main.go`](../../../tools/tillr-watch/main.go) already blank-imports `_ "github.com/mschulkind-oss/tillr-drain/drain"`, both `LoadRules` and `tillr watch lint` recognize `drain.gate-test-cpu` with no new cross-module dependency. (If the `procguard` internal package in [`../tillr-drain-package-decomposition.md`](../tillr-drain-package-decomposition.md) lands before the event build item, the `/proc` helper lives alongside `process_cpu.go` while `GateTestCPUKind` remains exported and registered by package `drain`.)
- **The one writer:** `Loop` in `tools/tillr-drain/drain`, called once per 1-minute `processCPUInterval` (`60 s`) on the `keepSessionAlive` heartbeat goroutine in [`tools/tillr-drain/drain/pass_observation.go`](../../../tools/tillr-drain/drain/pass_observation.go) right beside `recordProcessCPUIfDue` (primed at loop start alongside `primeProcessCPU`).
- **Why `Loop` must be the single loop-wide writer:**
  1. **`for_each` absent-subject clearing across concurrent gates:** In `observeForEachRecord` ([`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go)), a `consecutive_count` rule with `for_each = "processes"` and `reset` omitted clears every previously tracked subject absent from an incoming event's `processes` slice (`!seenInEvent[sub]` resets `c.events` and appends `rec.Seq` to `c.clears`). If each running gate emitted its own event containing only its own test binaries, gate B's event would omit gate A's live `process_key` and wipe gate A's idle streak and open alert episode. Emitting **one** loop-wide snapshot per minute from `Loop` gathers all live test binaries across every active detached gate into a single `processes` array.
  2. **Store isolation and check generality:** `tools/tillr-gate` is a standalone `package main` binary with no `tools/tillr-store` dependency, and check runners (`go_affected.py`, `go-test-shards.py`, direct `go test` checks, and consumer repositories' checks) are heterogeneous; `Loop` already owns the session store connection and detached gate supervision (`gate.face.json`).
- **Envelope and non-scheduling classification:**
  - `Kind = "drain.gate-test-cpu"`, `ObjectType = "session"`, `ObjectID = &sessionID` (matching `drain.process-cpu`).
  - Classified alongside `ProcessCPUKind` as a non-scheduling observation in [`tools/tillr-drain/drain/start_store_sync.go`](../../../tools/tillr-drain/drain/start_store_sync.go) and [`tools/tillr-drain/drain/idle.go`](../../../tools/tillr-drain/drain/idle.go) (and excluded beside `ProcessCPUKind` in `trigger_notify_test.go`), so 1-minute samples never wake idle drain planning passes.
- **Idle suppression and trailing clear snapshot:**
  - `Loop` appends `drain.gate-test-cpu` **only** when `len(processes) > 0` **or** the immediately preceding emitted `drain.gate-test-cpu` event in this session had `len(processes) > 0`.
  - When all previously sampled gate test binaries have exited, `Loop` appends **one** trailing event with `"processes": []` (`make([]GateTestCPUEntry, 0)` — a non-nil empty JSON array `[]`, never JSON `null`, because `observeForEachRecord` counts non-array `for_each` values in `Skipped` instead of clearing absent subjects) so `observeForEachRecord` clears streaks and resolves open episodes at that event's `seq`. Subsequent minutes with zero gate test binaries append zero events.

### 2.2 Discovering live gate test binaries and summing subtree CPU

On each 1-minute sample tick (`elapsed >= processCPUInterval` and `l.cfg.Store != ""`):

1. **Discover live detached gates:** Inspect candidate gate directories under `LoopDir(l.cfg.Root)` (`<root>/.tillr/loop/*`) when `l.cfg.Root != ""`, or `filepath.Join(filepath.Dir(l.cfg.Store), "gate-runs")` when `l.cfg.Root == ""` (matching `detachedGateDir` in [`tools/tillr-drain/drain/steps.go`](../../../tools/tillr-drain/drain/steps.go)). A gate directory `dir` is live when:
   - `readDetachedGateFace(dir)` returns a valid `face` (`face.PID > 0`, `face.StartTicks != ""`, `face.RunID != ""`);
   - `readDetachedGateDone(dir)` does **not** report a finished `done` with `done.RunID == face.RunID`; and
   - `heavySupervisorRunning(face.PID, face.StartTicks)` (evaluated against `l.procRootPath()`) confirms the supervisor process is still that exact `(PID, StartTicks)` incarnation.
2. **Walk `/proc` descendants of `face.PID`:** `superviseDetachedGate` in [`tools/tillr-drain/drain/steps.go`](../../../tools/tillr-drain/drain/steps.go) calls `prepareHeavyTree()`, making `face.PID` both a session leader and a Linux child subreaper (`PR_SET_CHILD_SUBREAPER`), so every process started inside that gate — including reparented grandchildren — remains in the `/proc` `ppid` tree rooted at `face.PID`. Reading `l.procRootPath()` (`/proc` in production, a temp directory in unit tests) once per sample:
   - Parse `/proc/<pid>/stat` (post-`)` fields: `state` at index `0`, `ppid` at `1`, `utime` at `11`, `stime` at `12`, `cutime` at `13`, `cstime` at `14`, `starttime` at `19`), skipping dead or zombie entries (`state == "Z" || state == "X"`).
   - Select every live descendant of `face.PID` whose NUL-separated `/proc/<pid>/cmdline` has `strings.HasSuffix(filepath.Base(argv[0]), ".test")` and at least one argument in `argv[1:]` with prefix `"-test."` (compiled Go test binaries).
3. **Compute subtree CPU ticks and `full_window`:**
   - For each live test binary `T` (`(pid, startTime)`), sum `utime + stime + cutime + cstime` across `T` and all live non-zombie descendants in `T`'s `ppid` subtree (`subtreeTicks`). Including live descendants plus `cutime + cstime` ensures that an integration test blocked in `cmd.Wait()` while a child `go build`, `git`, or helper binary uses CPU — or spawning and reaping short-lived subprocesses between ticks — records positive `cpu_ticks_delta` rather than false-reading as idle.
   - Look up `id = procIdentity{pid: T.pid, startTime: T.startTime}` in the sampler's previous tick map `prev`:
     - **First sighting (`!seen`):** `full_window = false`, `cpu_ticks_delta = subtreeTicks` (ticks accumulated since process start), and `prev[id] = subtreeTicks`. Marking `full_window = false` prevents false-counting a binary spawned milliseconds before the sample boundary ([`D-TW73`](../tillr-watch.md#D-TW73)).
     - **Consecutive sighting (`seen`):** `full_window = true`, `cpu_ticks_delta = subtreeTicks - prevTicks` when `subtreeTicks >= prevTicks` (else `0` if an unwaited child exited and was reparented outside `T`'s subtree), and `prev[id] = subtreeTicks`.
   - Compute `cpu_percent = int(math.Round(float64(cpu_ticks_delta) * 100.0 / (float64(procClockTicksPerSecond) * elapsed.Seconds())))` (`procClockTicksPerSecond = 100`, matching [`tools/tillr-drain/drain/process_cpu.go`](../../../tools/tillr-drain/drain/process_cpu.go)). Sort `processes` deterministically by `(run_id, pid)`.

### 2.3 Payload schema (`GateTestCPUFact` and `GateTestCPUEntry`)

```json
{
  "session_id": "sess-20261125-01",
  "window_ms": 60000,
  "processes": [
    {
      "process_key": "run-item-abc-a1:48210:918273645",
      "run_id": "run-item-abc-a1",
      "item_id": "item-abc",
      "lane_id": "lane-2",
      "pid": 48210,
      "start_ticks": "918273645",
      "binary": "drain.test",
      "cmdline": "/tmp/go-build123/b001/drain.test -test.paniconexit0 -test.timeout=10m0s",
      "log_dir": "/workspace/.tillr/loop/run-item-abc-a1",
      "full_window": true,
      "cpu_ticks_delta": 0,
      "cpu_percent": 0
    }
  ]
}
```

| Field | Location | JSON type | Meaning |
| :--- | :--- | :--- | :--- |
| `session_id` | top-level | `string` | Active `Loop` session ID (`"loop"` if unset in unit tests, matching `ProcessCPUFact`). |
| `window_ms` | top-level | `number` (`int64`) | Elapsed milliseconds since the preceding 1-minute sample. |
| `processes` | top-level | `array` of objects | Live Go test binaries across all active detached gates; serialized as `[]` (never `null`) on the single trailing clear event. |
| `process_key` | `processes[]` | `string` | `"<run_id>:<pid>:<start_ticks>"` — unique per gate run and OS process incarnation; `group_by` key ([`D-TW22`](../tillr-watch.md#D-TW22)). |
| `run_id` | `processes[]` | `string` | `face.RunID` of the detached gate owning this test binary. |
| `item_id` | `processes[]` | `string` | `face.ItemID` of the candidate under gate. |
| `lane_id` | `processes[]` | `string` | `face.LaneID` of the candidate under gate. |
| `pid` | `processes[]` | `number` (`int`) | OS PID of the `*.test` binary. |
| `start_ticks` | `processes[]` | `string` | `/proc/<pid>/stat` field 22 (`starttime`), checked before sending `SIGQUIT` so a recycled PID is never signalled. |
| `binary` | `processes[]` | `string` | `filepath.Base(argv[0])` (for example `"drain.test"`). |
| `cmdline` | `processes[]` | `string` | Space-joined `argv` from `/proc/<pid>/cmdline`, truncated to at most 512 UTF-8 bytes so alert timeline bundles remain well under 8,192 bytes. |
| `log_dir` | `processes[]` | `string` | Detached gate directory (`dir`), under which `tillr-gate` writes `logs/`, `change-logs/`, `gate.plan.json`, and `gate.stderr`. |
| `full_window` | `processes[]` | `boolean` | `false` on first sighting of `(pid, start_ticks)`; `true` on every subsequent consecutive 1-minute sample of that `(pid, start_ticks)`. |
| `cpu_ticks_delta` | `processes[]` | `number` (`uint64`) | Subtree `utime + stime + cutime + cstime` clock-tick delta over `window_ms` (`10 ms` resolution at `USER_HZ = 100`, catching sub-1% progress that integer `cpu_percent` rounds to `0`). |
| `cpu_percent` | `processes[]` | `number` (`int`) | Rounded integer percentage of one CPU core over `window_ms` for human-readable status and event inspection. |

---

## 3. Landed Pattern and Rule Contract: `gate-test-cpu-idle` ([`D-TW-GTCI1`](#D-TW-GTCI1))

*Non-owner decision (from the 2026-11-24 operator request and the designer; the owner may overturn any part of this section).*

### 3.1 Landed pattern choice and evaluation mechanics

The rule uses the landed **`consecutive_count`** pattern (`ConsecutiveCount` in [`tools/tillr/watch/rules.go`](../../../tools/tillr/watch/rules.go)) combined with landed **`for_each = "processes"`** and **`group_by = "process_key"`** in [`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go) ([`D-TW13`](../tillr-watch.md#D-TW13), [`D-TW81`](../tillr-watch.md#D-TW81)). Zero changes to `tools/tillr/watch/` are needed.

How landed `(*condition).observe` and `observeForEachRecord` evaluate each `(run_id, pid, start_ticks)` subject with `reset` omitted:

1. **Baseline sighting (`t0`):** `full_window == false` makes `match` evaluate to `false`; `c.events` stays empty (`0` count).
2. **Consecutive full idle windows (`t0 + 1m`, `t0 + 2m`):** Each sample with `full_window == true` and `cpu_ticks_delta == 0` appends to `c.events`. On the second consecutive full zero-CPU window (`threshold = 2`, representing `2` to `3` minutes of zero subtree CPU), `len(c.events) >= 2` opens one alert episode for `process_key`. Because `2` to `3` minutes exceeds `go_affected.py`'s 60-second internal per-binary watchdog (`WatchdogSilentSamples = 60` at `1 s` in [`tools/tillr-gate/watchdog.go`](../../../tools/tillr-gate/watchdog.go)), any binary already reaped by `go_affected.py` exits before reaching `threshold = 2`, avoiding duplicate or expected alerts ([`D-TW73`](../tillr-watch.md#D-TW73)) while alerting minutes ahead of 10-minute `go test` timeouts.
3. **Resumed CPU (`cpu_ticks_delta > 0`):** `match` evaluates to `false` with `r.Reset == nil`, which clears `c.events` and appends `rec.Seq` to `c.clears`, resetting any partial streak and auto-resolving any open episode.
4. **Test binary exit (after `SIGQUIT` or normal completion):** The next `drain.gate-test-cpu` snapshot (either carrying remaining live gate test binaries or the single trailing `"processes": []` snapshot) omits `process_key`. In `observeForEachRecord`, `!seenInEvent[sub]` clears `c.events` and appends `rec.Seq` to `c.clears`, resolving the episode automatically.

### 3.2 Rule specification (`config/tillr/watch/gate-test-cpu-idle.toml`)

```toml
# Reads registered kind drain.gate-test-cpu (1-minute per-binary subtree CPU
# snapshots across live detached gates). Opens one alert when a Go test binary
# and its descendant subtree burn zero CPU ticks across two consecutive full
# 1-minute windows; attaches no automatic remedy and never ends, fails, or
# retries the gate.

[[rule]]
id = "gate-test-cpu-idle"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.gate-test-cpu"]

[rule.when]
for_each = "processes"
group_by = "process_key"
pattern = "consecutive_count"
match = 'event.full_window == true and event.cpu_ticks_delta == 0'
threshold = 2
window = "10m"

[rule.guidance]
summary = "Gate test binary {{event.binary}} (PID {{event.pid}}, {{subject}}) in {{event.run_id}} ({{event.item_id}} on {{event.lane_id}}) used 0 CPU ticks for {{count}} consecutive 1-minute windows."
check = [
  "Inspect /proc/{{event.pid}}/stat and /proc/{{event.pid}}/cmdline to confirm PID {{event.pid}} is still live with starttime {{event.start_ticks}} and command {{event.cmdline}}.",
  "Inspect {{event.log_dir}}/ (gate.plan.json, gate.stderr, change-logs/, and logs/) to identify the check log receiving stderr from {{event.binary}}.",
]
do = [
  "If /proc/{{event.pid}}/stat still shows starttime {{event.start_ticks}} for {{event.binary}}, send SIGQUIT to that test binary PID only (kill -QUIT {{event.pid}} — never signal tillr-gate, go test, a shard runner, or a process group, and do not end, fail, or retry the gate), read the goroutine dump from the check log under {{event.log_dir}}/change-logs/ or {{event.log_dir}}/logs/, and file a queue item to repair the hung test.",
]

[rule.delivery]
receiver = "foreground"
also_notify = ["subscribers"]
```

---

## 4. Follow-Up Build Items and Candidate Notes ([`D-TW-GTCI1`](#D-TW-GTCI1))

Per [`D-TW42`](../tillr-watch.md#D-TW42), [`landing-principles.md` §15](../landing-principles.md#15-land-the-smallest-functional-change), and the operator request (`"name the follow-up build items, each with one behavior (the event, then the rule)"`), implementation splits into two ordered single-behavior build items:

1. **Follow-up Build Item 1 — Record `drain.gate-test-cpu` in `tools/tillr-drain` (the event):**
   - **Single behavior:** Register `GateTestCPUKind = "drain.gate-test-cpu"` under `drain.Utility` (`"drain"`) and emit loop-wide 1-minute `drain.gate-test-cpu` session snapshots from `Loop` (`keepSessionAlive` alongside `recordProcessCPUIfDue`) across all live detached gates (`gate.face.json` with live supervisor and no matching `gate.done.json`), recording per-test-binary subtree CPU tick deltas (`utime + stime + cutime + cstime`), `full_window` baseline tracking, and one trailing `"processes": []` clear snapshot on exit, wired alongside `ProcessCPUKind` as a non-scheduling observation in `start_store_sync.go` and `idle.go` and synced via `just pack-sync`.
   - **Verification:** Synthetic `/proc` and `gate.face.json` unit tests in `tools/tillr-drain/drain/` covering event registration under `"drain"`, zero events when no gate runs test binaries, `full_window == false` on first sighting and `full_window == true` on subsequent windows, positive `cpu_ticks_delta` when a child subprocess burns CPU or is reaped, concurrent detached gates combined into one `processes` array, single trailing `"processes": []` clear snapshot followed by silence, and non-waking store-sync classification. Changes zero files under `config/tillr/watch/` or `tools/tillr/watch/`.
2. **Follow-up Build Item 2 — Add `gate-test-cpu-idle` rule and evaluation tests (the rule, ordered after Item 1):**
   - **Single behavior:** Add `config/tillr/watch/gate-test-cpu-idle.toml` ([§3.2](#32-rule-specification-configtillrwatchgate-test-cpu-idletoml)) reading `drain.gate-test-cpu` with `consecutive_count` (`for_each = "processes"`, `group_by = "process_key"`, `threshold = 2`, `window = "10m"`), without changing `tools/tillr-drain` or `tools/tillr/watch/`.
   - **Verification:** Scratch-store (`t.TempDir()`, [`D-TW41`](../tillr-watch.md#D-TW41)) unit tests in `tools/tillr-watch/` (plus `just pack-sync` if shipped-rule embedding has landed) verifying `LoadRules` and `tillr watch lint` (`Skipped == 0`), `0` alerts on `full_window == false` or single idle windows, `0` alerts when `cpu_ticks_delta > 0` (including sub-1% tick progress where `cpu_percent == 0`), `1` open alert after two consecutive zero-tick full windows, independent streak tracking across concurrent gates in the same `processes` array, and automatic episode resolution both when `cpu_ticks_delta > 0` resumes and when the test binary disappears from the next snapshot (including `"processes": []`).

### Candidate notes on out-of-scope surfaces

- **Rule language (`tools/tillr/watch/`):** No gap exists; `for_each = "processes"` with absent-subject clearing on `consecutive_count` ([`D-TW81`](../tillr-watch.md#D-TW81)) and every placeholder used in [§3.2](#32-rule-specification-configtillrwatchgate-test-cpu-idletoml) are already built in `tools/tillr/watch/`.
- **Shipped-versus-repository watch rules ([`../tillr-watch.md` §10](../tillr-watch.md#10-build-slices-for-shipped-and-repository-watch-rules), [`D-TW83`](../tillr-watch.md#D-TW83)–[`D-TW88`](../tillr-watch.md#D-TW88)):** `config/tillr/watch/` is currently the active rules directory; if [`D-TW88`](../tillr-watch.md#D-TW88) Slice 2/3 lands before Follow-up Build Item 2, `gate-test-cpu-idle.toml` (`scope = "core"`) goes in the shipped core rules directory and `just pack-sync` mirror instead.
- **Post-merge trunk proofs and non-Go test runners:** Post-merge trunk proofs (`HeavyRunner` in `tools/tillr-drain/heavyrun/`) run supervised check groups directly rather than candidate `gate.face.json` detached gates, and Python or shell checks do not emit Go `SIGQUIT` goroutine dumps; watching those surfaces is outside this candidate-gate rule's scope.

---

## 5. Rejected Alternatives

Each alternative below was considered by the designer and set aside (the owner may overturn any of these dispositions except [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), and [`D-TW73`](../tillr-watch.md#D-TW73)):

| Alternative | Why it fails | Verdict |
| :--- | :--- | :--- |
| **Extend `drain.process-cpu` to include gate test binaries** | Corrupts `ProcessCPUFact`'s daemon summary fields (`max_cpu_percent`, `max_role`, `max_pid`) whenever a parallel Go test binary runs across multiple cores, and forces `gate-test-cpu-idle` to scan every 1-minute daemon sample when no gate is running. | Rejected. |
| **Emit `drain.gate-test-cpu` from `tillr-gate` or `tools/gate/go_affected.py`** | `tillr-gate` has no store dependency, `go_affected.py` misses `go-test-shards.py` and consumer checks, and per-gate events carry partial `processes` arrays that wipe sibling gates' streaks and open episodes in `observeForEachRecord`. | Rejected. |
| **Match `event.cpu_percent == 0` instead of `event.cpu_ticks_delta == 0`** | `cpu_percent` rounds `delta * 100 / 6000` to the nearest integer, so `1` to `29` clock ticks (`10` to `290 ms` of real CPU work in a minute) round to `0%` and false-fire on a slow I/O-bound test that is still making CPU progress ([`D-TW73`](../tillr-watch.md#D-TW73)). | Rejected. |
| **Sample only the `*.test` PID's own `utime + stime` without descendants and `cutime + cstime`** | Integration tests in `tillr-drain`, `tillr-land`, and `tillr-gate` spend tens of seconds waiting in `cmd.Wait()` on `go build` or `git` child processes; ignoring subtree and reaped-child ticks false-fires while child processes actively use CPU ([`D-TW73`](../tillr-watch.md#D-TW73)). | Rejected. |
| **Use `duration_without_event` between `gate.started` and `gate.verdict`** | Already covered by `gate-verdict-overdue.toml` (`3,650 s`); cannot identify which `*.test` PID hung or whether checks are actively burning CPU. | Rejected. |

---

## 6. Open Questions

None.

---

## 7. Decision Ledger

[`D-TW-GTCI1`](#D-TW-GTCI1) records a decision from the 2026-11-24 operator request and the designer; it is **not** an owner ruling, and the owner may overturn any part of it. `Built` is checked against `95376360db`.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-TW-GTCI1"></a>**[D-TW-GTCI1](#D-TW-GTCI1)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** (1) register `drain.gate-test-cpu` (`GateTestCPUKind`) under `drain.Utility` (`"drain"`) with `Loop` in `tools/tillr-drain/drain` as the single 1-minute writer across all live detached gates (`gate.face.json`), recording per-`*.test`-binary subtree `utime + stime + cutime + cstime` deltas (`cpu_ticks_delta`), `full_window`, `(pid, start_ticks, binary, cmdline, log_dir, run_id, item_id, lane_id, process_key)`, and one trailing `"processes": []` clear snapshot before going silent when idle; (2) watch `drain.gate-test-cpu` with landed `consecutive_count` (`for_each = "processes"`, `group_by = "process_key"`, `match = 'event.full_window == true and event.cpu_ticks_delta == 0'`, `threshold = 2`, `window = "10m"`) in `gate-test-cpu-idle.toml`, guiding the responder to verify `(pid, start_ticks)` and send `SIGQUIT` to that test binary PID alone without ending, failing, or retrying the gate; and (3) build in two ordered single-behavior follow-up items — first the `drain.gate-test-cpu` event in `tools/tillr-drain`, then the `gate-test-cpu-idle.toml` rule and scratch-store evaluation tests. | 2026-11-25 | [§2](#2-recorded-fact-draingate-test-cpu-d-tw-gtci1), [§3](#3-landed-pattern-and-rule-contract-gate-test-cpu-idle-d-tw-gtci1), [§4](#4-follow-up-build-items-and-candidate-notes-d-tw-gtci1), [§5](#5-rejected-alternatives) | no |
