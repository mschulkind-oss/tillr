---
title: "Alert when a process remains alive naming an ended run's scratch directory"
author: "Agent"
date: 2026-11-26
status: in-review
stage: DECIDED
stage-since: 2026-11-25
tags: [tillr, watch, drain, process, scratch, design]
summary: "Record state-change drain.run-scratch-process events from 1-minute /proc scans in tillr-drain's Loop for live non-zombie processes naming an ended run's scratch directory (excluding live runs), and watch them with an unwindowed predicate_match rule (drain-run-scratch-process-live) grouped by run_id; split into two single-behavior follow-up build items (the event, then the rule)."
next: "Run the two ordered follow-up build items in Section 4 — first recording drain.run-scratch-process in tools/tillr-drain/drain/, then adding tools/tillr-watch/rules/drain-run-scratch-process-live.toml and its evaluation tests — and run the separate Step 2 cleanup item of D-TW-DR4 to remove inline Section 5.20 and D-TW89 from docs/design/tillr-watch.md; the owner may review D-TW-ERSP1."
depends-on:
  - ../tillr-watch.md
  - ../tillr-watch.md#D-TW89
  - ../tillr-watch.md#D-TW2
  - ../tillr-watch.md#D-TW4
  - ../tillr-watch.md#D-TW5
  - ../tillr-watch.md#D-TW6
  - ../tillr-watch.md#D-TW32
  - ../tillr-watch.md#D-TW41
  - ../tillr-watch.md#D-TW42
  - ../tillr-watch.md#D-TW48
  - ../tillr-watch.md#D-TW50
  - ../tillr-watch.md#D-TW63
  - ../tillr-watch.md#D-TW64
  - ../tillr-watch.md#D-TW73
  - ../tillr-watch.md#D-TW84
  - ../tillr-watch.md#OQ-TW2
  - ./decision-record-files.md#R-TW-DR1
  - ./decision-record-files.md#R-TW-DR2
  - ./decision-record-files.md#D-TW-DR1
  - ./decision-record-files.md#D-TW-DR4
  - ../landing-principles.md#15-land-the-smallest-functional-change
related:
  - ../tillr-utility-contracts/tillr-watch.md
  - ./ended-run-scratch-worktree.md
vantage:
  status-chip: true
---

# Alert when a process remains alive naming an ended run's scratch directory

**Status:** 2026-11-26, verified at `3d346476295b`. Nothing specified in [`D-TW-ERSP1`](#D-TW-ERSP1) is built yet (`rg -n 'run-scratch-process|ended-run-scratch-process' tools config yolo-packs` returns nothing, and both `test -f tools/tillr-watch/rules/drain-run-scratch-process-live.toml` and `test -f config/tillr/watch/drain-run-scratch-process-live.toml` exit non-zero). Per owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`685934ce`), [`D-TW-DR1`](./decision-record-files.md#D-TW-DR1), and Step 1 of [`D-TW-DR4`](./decision-record-files.md#D-TW-DR4) (`b1156e65`), this standalone decision record moves the pre-enforcement inline record added in `8016445f2b54` ([`../tillr-watch.md` §5.20](../tillr-watch.md#520-process-left-alive-under-an-ended-runs-scratch-directory-d-tw89), [`D-TW89`](../tillr-watch.md#D-TW89)) into its own file under `docs/design/tillr-watch/` with zero edits to [`../tillr-watch.md`](../tillr-watch.md) and zero decision changes (`git log 8016445f2b54..3d346476295b --oneline -- docs/design/tillr-watch.md` returns only `da8eedec` and `202d0f22`, showing zero subsequent inline decision records added to [`../tillr-watch.md`](../tillr-watch.md)).

> **In short.** No existing store event or watch projection tracks live processes whose `/proc/<pid>/cmdline` names a path under an ended run's scratch directory (`<lane-root>/<lane-id>/durable/<run-id>`). Having `Loop` in `tools/tillr-drain/drain` append state-change `drain.run-scratch-process` events from 1-minute `/proc` scans (excluding processes belonging to live runs) lets an unwindowed `predicate_match` rule grouped by `run_id` (`drain-run-scratch-process-live`) open one alert episode per ended run with surviving scratch processes and resolve it automatically once `live_count` drops to `0`.

**Why it matters.** On 2026-11-24, `item-request-ccd99e5fbb` (`"one rule, one new file, change nothing else"`) had to be closed before a worker ran because none of the landed watch patterns or projections read live process command lines by ended-run scratch path, leaving detached worker or test subprocesses holding scratch paths after run settlement unseen ([§1.1](#11-what-happened-on-2026-11-24-and-why-no-existing-fact-suffices)).

**The shape.** `Loop` in `tools/tillr-drain/drain` scans `l.procRootPath()` once per minute alongside `recordProcessCPUIfDue` and `keepSessionAlive`, matches live non-zombie same-UID `/proc/<pid>/cmdline` arguments on a path-component boundary against ended runs' scratch prefixes (after a `5s` post-`finished_at` grace and live-run exclusion), emits `drain.run-scratch-process` only on state changes (`live_count > 0` or clearing `live_count = 0`), and feeds an unwindowed `predicate_match` rule (`drain-run-scratch-process-live`) grouped by `run_id` with `fingerprint = ["process_keys"]` ([§2](#2-recorded-fact-drainrun-scratch-process-d-tw-ersp1), [§3](#3-landed-pattern-and-rule-contract-drain-run-scratch-process-live-d-tw-ersp1)).

**Cost.** Two single-behavior follow-up build items — first the state-change `drain.run-scratch-process` producer in `tools/tillr-drain` (writing zero events while idle), then `drain-run-scratch-process-live.toml` and its evaluation tests — with zero rule-language or database-schema changes ([§4](#4-follow-up-build-items-and-candidate-notes-d-tw-ersp1)).

**Start at [§2](#2-recorded-fact-drainrun-scratch-process-d-tw-ersp1)** for the event contract and single writer, [§3](#3-landed-pattern-and-rule-contract-drain-run-scratch-process-live-d-tw-ersp1) for the unwindowed `predicate_match` rule, and [§4](#4-follow-up-build-items-and-candidate-notes-d-tw-ersp1) for the two follow-up build items and Step 2 cleanup note.

**Needs your ruling:** None. Owner rulings [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), [`D-TW48`](../tillr-watch.md#D-TW48), [`D-TW64`](../tillr-watch.md#D-TW64), and [`D-TW73`](../tillr-watch.md#D-TW73) are already settled; [`D-TW-ERSP1`](#D-TW-ERSP1) comes from the operator request and the designer (`8016445f2b54`) and may be overturned by the owner.

**Reads with:** [`../tillr-watch.md`](../tillr-watch.md) (the baseline watch design), [`./decision-record-files.md`](./decision-record-files.md) (standalone watch decision records and the two-step migration rule), [`./ended-run-scratch-worktree.md`](./ended-run-scratch-worktree.md) (sibling ended-run scratch worktree alert), and [`../landing-principles.md`](../landing-principles.md) (smallest functional change).

---

## 1. Problem, Existing Facts, and Authority Boundary

### 1.1 What happened on 2026-11-24 and why no existing fact suffices

On 2026-11-24, the operator filed alert-rule request `item-request-ccd99e5fbb` worded `"one rule, one new file, change nothing else"` for a process whose command line names a path under an ended run's scratch directory while still alive (and not belonging to a live run), then closed it before a worker ran because no recorded event or projection tracked live processes by ended-run scratch path:

1. **`drain.process-cpu` monitors only three daemon roles.** `ProcessCPUKind` in [`tools/tillr-drain/drain/process_cpu.go`](../../../tools/tillr-drain/drain/process_cpu.go) samples 1-minute CPU tick deltas for `drain-loop`, `tillr-watch-wait`, and `tillr-view-serve`. Mixing arbitrary leaked worker subprocesses into `drain.process-cpu` would conflate daemon CPU profiling with ended-run scratch-path liveness and rewrite unrelated monitoring.
2. **`survivingRunProcesses` checks environment markers at settlement only.** `survivingRunProcesses` in [`tools/tillr-drain/drain/surviving_processes.go`](../../../tools/tillr-drain/drain/surviving_processes.go) scans `/proc/<pid>/environ` for `TILLR_RUN_ID=<run_id>` and `TILLR_LOOP_STORE=<resolved_store>` during run settlement (`checkSurvivingRunProcesses` and landing-offer intake), holding the item via `drain.item-fault` (`reason = "run-processes-live"`). It misses detached or environment-scrubbed processes whose `/proc/<pid>/cmdline` names `<lane-dir>/durable/<run-id>`, misses runs started without `TILLR_LOOP_STORE` or processes inspected after settlement, and records no clearing event when the process exits.
3. **Projections do not read `/proc`.** The watch projections in [`tools/tillr/watch/rule_schema.go`](../../../tools/tillr/watch/rule_schema.go) (`items`, `charter`, `git_refs:`, `fixer_checks`, `green_proof:`) read store tables and Git refs rather than `/proc`, and `tillr-watch` rules remain pure declarative store readers ([`D-TW4`](../tillr-watch.md#D-TW4)).

### 1.2 Placement, backward-only migration, and authority boundary

Commit `8016445f2b54` (`2026-11-25`, `"docs(watch): design ended-run scratch-process event and alert rule"`) answered a follow-up operator request asking which recorded fact that alert reads, which landed rule pattern it uses, and what follow-up build items implement it, landing inline as [`../tillr-watch.md` §5.20](../tillr-watch.md#520-process-left-alive-under-an-ended-runs-scratch-directory-d-tw89) and [`D-TW89`](../tillr-watch.md#D-TW89) before commit `738bb57bea91` enforced [`decision-record-files.md` §3](./decision-record-files.md#3-the-one-enforcement-the-gates-documents-check-d-tw-dr2) in [`tools/gate/change_common.go`](../../../tools/gate/change_common.go).

Six standing owner rulings govern this document without modification:

- [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`2026-11-25`, `"ok, so save that"`): every new decision record about `tillr-watch` goes in its own small file under `docs/design/tillr-watch/`, and [`../tillr-watch.md`](../tillr-watch.md) is not edited when a record is added.
- [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2) (`2026-11-16`, `"Explicitly, we NEVER want fallbacks anywhere in this project."`): no fallback writer, fallback `/proc` reader, automatic reaper, or gate carve-out exists.
- [`OQ-TW2`](../tillr-watch.md#OQ-TW2) (`2026-11-21`, `"We don't cover issues. We use this to find the issues and fix them."`) and [`D-TW48`](../tillr-watch.md#D-TW48) (`2026-11-21`): `tillr-watch` executes no signal or process reaper; the responder inspects each verified PID and ends it individually by exact PID.
- [`D-TW64`](../tillr-watch.md#D-TW64) (`2026-11-22`): every alert is investigated and ends in a recorded action, with `[rule.variables]` exposing instance values for scoped silences.
- [`D-TW73`](../tillr-watch.md#D-TW73) (`2026-11-22`, `"I never want alerts for expected conditions."`): the `5s` post-`finished_at` teardown grace and live-run exclusion suppress teardown races and active worker inspections, and `[rule.guidance]` never calls a firing alarm `"expected"`.

Under [`D-TW-DR1`](./decision-record-files.md#D-TW-DR1) and Step 1 of [`D-TW-DR4`](./decision-record-files.md#D-TW-DR4) (`b1156e65`), [`D-TW-ERSP1`](#D-TW-ERSP1) restates and replaces inline [`../tillr-watch.md` §5.20](../tillr-watch.md#520-process-left-alive-under-an-ended-runs-scratch-directory-d-tw89) and [`D-TW89`](../tillr-watch.md#D-TW89) backward-only without editing [`../tillr-watch.md`](../tillr-watch.md) or [`./decision-record-files.md`](./decision-record-files.md). Amending [`D-TW-DR4`](./decision-record-files.md#D-TW-DR4)'s colon-clause summary backward-only where that summary paraphrased `8016445f2b54` with different identifiers, [`D-TW-ERSP1`](#D-TW-ERSP1) preserves every mechanism, identifier, schema field, writer rule, TOML rule configuration, rejected alternative, and build split of `8016445f2b54` unchanged. Everything in [`D-TW-ERSP1`](#D-TW-ERSP1) is the **designer's decision answering the operator request (`2026-11-25` in `8016445f2b54`, moved standalone on `2026-11-26`) — not an owner ruling — and the owner may overturn any part of it**.

---

## 2. Recorded Fact: `drain.run-scratch-process` ([`D-TW-ERSP1`](#D-TW-ERSP1))

*Non-owner decision (from the operator request and the designer in `8016445f2b54`; the owner may overturn any part of this section).*

### 2.1 Event name, registration, and the one writer

- **Event kind and registration:** Register `drain.run-scratch-process` in `tools/tillr-drain/drain/` via `event.Register("drain.run-scratch-process", Utility)`, which links into `tillr-watch` automatically through `_ "github.com/mschulkind-oss/tillr-drain/drain"` in [`tools/tillr-watch/main.go`](../../../tools/tillr-watch/main.go). Append each event with `object_type = "run"` and `object_id = &runID` so episodes grouped by `run_id` receive the run's timeline bundle ([`D-TW6`](../tillr-watch.md#D-TW6)).
- **The one writer (`tillr-drain` `Loop`):** Sample `/proc` on the existing 1-minute cadence alongside `recordProcessCPUIfDue` in [`tools/tillr-drain/drain/loop.go`](../../../tools/tillr-drain/drain/loop.go) and `keepSessionAlive` in [`tools/tillr-drain/drain/pass_observation.go`](../../../tools/tillr-drain/drain/pass_observation.go), reading `l.procRootPath()` so unit tests supply a synthetic `/proc` tree and logging store write errors without faulting the loop.

### 2.2 Distinguishing ended runs from live runs, scratch matching, and state-change emission

1. **Live runs:** From a read snapshot of the store plus in-memory `Loop` state, collect every `run_id` leased on `lanes` (`state = 'leased' AND run_id IS NOT NULL AND run_id != ''` in `RunDDL`, [`tools/tillr-run/run/schema.go`](../../../tools/tillr-run/run/schema.go)), every active sibling in `read_only_runs` (`settled = 0` joined to a leased `lanes` row), and every run currently in flight in `Loop`. Because `Exec` in [`tools/tillr-run/run/exec.go`](../../../tools/tillr-run/run/exec.go) claims `lanes` before `cmd.Start()` (including on `Resume`) and holds that lease across `cmd.Wait()`, `endRunProcesses` ([`tools/tillr-run/run/children.go`](../../../tools/tillr-run/run/children.go)), and `readSpend` (`extract-report`) until `writeTerminal` commits `runs` and clears `lanes` atomically, neither a live worker nor `extract-report` runs outside a live lease.
2. **Ended runs and their scratch prefixes:** Select rows in `runs` whose `run_id` is not in the live-run set and whose `finished_at` is at least `5s` before the scan timestamp (the `5s` post-`finished_at` grace avoids sub-second pipe-close teardown races such as `--shard-guard` in [`tillr-gate shards`](../../../tools/tillr-gate/shards), [`D-TW73`](../tillr-watch.md#D-TW73)). Map each ended run to `run.DurableDir(filepath.Join(laneRoot, lane_id), run_id)` (`<lane-root>/<lane-id>/durable/<run-id>`, from `DurableDir` in [`tools/tillr-run/run/exec.go`](../../../tools/tillr-run/run/exec.go)) plus any `{Kind: "scratch"}` path recorded in `runs.artifact_refs`, matching both raw and symlink-resolved paths on a path-component boundary (next byte is `/`, end-of-argument, whitespace, quote, `:`, or `;` — never `[A-Za-z0-9._-]`, so `run-1` never matches `run-10`).
3. **Excluding processes of a live run:** Scan live non-zombie (`state != 'Z' && state != 'X'`) same-UID `/proc/<pid>` entries (`pid != selfPID`). When `/proc/<pid>/cmdline` names an ended run's scratch directory, read `/proc/<pid>/environ` and skip the process if `TILLR_RUN_ID` or `TILLR_RUN_EXEC_ID` belongs to a run in the live-run set (for example, a live worker or triage run inspecting an earlier run's scratch files).
4. **State-change emission (zero idle store writes):** `Loop` maintains `activeScratchRuns map[string]string` (`run_id -> process_keys`), seeded once per loop session from the store's latest `drain.run-scratch-process` event per `run_id` whose `live_count > 0`. On each 1-minute scan:
   - Append nothing when no ended run has matching processes and `activeScratchRuns` is empty.
   - Append `drain.run-scratch-process` (`live_count > 0`) for an ended `run_id` only when `run_id` newly enters `activeScratchRuns` or its `process_keys` value changes.
   - Append `drain.run-scratch-process` (`live_count = 0`, `process_keys = ""`, `process_summary = ""`, `processes = []`) once when a `run_id` in `activeScratchRuns` has zero matching processes on a scan — including on the first scan after a loop restart when a leaked process exited while the loop was down, or when `run_id` resumed into the live-run set — then delete `run_id` from `activeScratchRuns`.

### 2.3 Payload schema (`RunScratchProcessEntry` and `drain.run-scratch-process`)

```json
{
  "session_id": "sess-20261125-01",
  "run_id": "run-item-abc-a1",
  "item_id": "item-abc",
  "lane_id": "lane-2",
  "scratch_dir": "/workspace/.tillr/lanes/lane-2/durable/run-item-abc-a1",
  "live_count": 1,
  "process_keys": "48210:918273645",
  "process_summary": "48210 (/tmp/go-build123/b001/drain.test -test.run=^TestLeak$)",
  "processes": [
    {
      "pid": 48210,
      "start_time": "918273645",
      "process_key": "48210:918273645",
      "cmdline": "/tmp/go-build123/b001/drain.test -test.run=^TestLeak$ --scratch /workspace/.tillr/lanes/lane-2/durable/run-item-abc-a1/tmp",
      "matched_path": "/workspace/.tillr/lanes/lane-2/durable/run-item-abc-a1/tmp"
    }
  ]
}
```

| Field | JSON type | Meaning |
| :--- | :--- | :--- |
| `session_id` | `string` | `tillr-drain` loop session ID recording the observation. |
| `run_id` | `string` | Ended run whose scratch directory appears on `/proc/<pid>/cmdline`. |
| `item_id` | `string` (`omitempty`) | `runs.item_id` when the run belonged to an item. |
| `lane_id` | `string` | `runs.lane_id`. |
| `scratch_dir` | `string` | Canonical scratch directory (`<lane-root>/<lane-id>/durable/<run-id>`). |
| `live_count` | `int` | Count of matching live processes (`> 0` while active, `0` when cleared). |
| `process_keys` | `string` | Comma-joined `<pid>:<start_time>` entries sorted by `pid` (string scalar so `fingerprint` in [`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go) hashes it directly). |
| `process_summary` | `string` | `"<pid> (<cmdline>), ..."` summary for guidance placeholders (`""` when cleared). |
| `processes` | `[]RunScratchProcessEntry` | `{pid, start_time, process_key, cmdline, matched_path}` sorted by `pid` (`[]` when cleared). |

---

## 3. Landed Pattern and Rule Contract: `drain-run-scratch-process-live` ([`D-TW-ERSP1`](#D-TW-ERSP1))

*Non-owner decision (from the operator request and the designer in `8016445f2b54`; the owner may overturn any part of this section).*

### 3.1 Landed pattern choice and evaluation mechanics

`drain-run-scratch-process-live` uses the landed unwindowed **`predicate_match`** pattern in [`tools/tillr/watch/rules.go`](../../../tools/tillr/watch/rules.go) ([`D-TW50`](../tillr-watch.md#D-TW50)) with `match`, `clear_when`, and no `window` — holding until cleared like [`drain-lane-refused.toml`](../../../tools/tillr-watch/rules/drain-lane-refused.toml) and [`run-model-timeout.toml`](../../../tools/tillr-watch/rules/run-model-timeout.toml), with zero changes to the rule language or evaluator:

1. **One episode per ended run (`group_by = "run_id"`, [`D-TW5`](../tillr-watch.md#D-TW5)):** Grouping by `run_id` opens one episode per ended run rather than `N` episodes when one leaked test binary holds child workers, and `resolveVariables` in [`tools/tillr/watch/variables.go`](../../../tools/tillr/watch/variables.go) exposes `run_id` alongside `[rule.variables]`.
2. **Survives item landing or closure ([`D-TW63`](../tillr-watch.md#D-TW63)):** Because `run_id` is not a key in `items`, `itemTerminalChecker` in [`tools/tillr/watch/source.go`](../../../tools/tillr/watch/source.go) keeps the episode open even when `item_id` has already `landed` or `closed`.
3. **Reopens on process-set change (`fingerprint = ["process_keys"]`, [`D-TW32`](../tillr-watch.md#D-TW32)):** Fingerprinting `process_keys` reopens a `silence --until-change` episode if the surviving `(pid, start_time)` set changes, and `clear_when = 'event.live_count == 0'` resolves the episode (`condition-cleared`) once all matching processes exit. Guidance obeys [`D-TW2`](../tillr-watch.md#D-TW2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), and [`D-TW73`](../tillr-watch.md#D-TW73).

### 3.2 Rule specification (`tools/tillr-watch/rules/drain-run-scratch-process-live.toml`)

```toml
[[rule]]
id = "drain-run-scratch-process-live"
scope = "core"
severity = "warning"

[rule.variables]
item_id = "item_id"
lane_id = "lane_id"
scratch_dir = "scratch_dir"
process_keys = "process_keys"

[rule.reads]
events = ["drain.run-scratch-process"]

[rule.when]
group_by = "run_id"
pattern = "predicate_match"
match = 'event.live_count > 0'
clear_when = 'event.live_count == 0'
fingerprint = ["process_keys"]

[rule.guidance]
summary = "Ended run {{subject}} on {{event.lane_id}} still has {{event.live_count}} live process(es) naming {{event.scratch_dir}}: {{event.process_summary}}."
check = [
  "Verify /proc/<pid>/stat and /proc/<pid>/cmdline against process_keys ({{event.process_keys}}) to confirm each PID still belongs to the leaked process from {{subject}} (item {{event.item_id}}).",
  "Inspect the run log and scratch directory {{event.scratch_dir}} to see which test, tool or background command detached without exiting when {{subject}} ended."
]
do = [
  "End only verified leaked PIDs individually by exact PID (never pkill, killall or pgrep), and file or track the queue item fixing the detached-process leak."
]

[rule.delivery]
receiver = "foreground"
also_notify = ["subscribers"]
```

---

## 4. Follow-Up Build Items and Candidate Notes ([`D-TW-ERSP1`](#D-TW-ERSP1))

Per [`D-TW42`](../tillr-watch.md#D-TW42) and [`landing-principles.md` §15](../landing-principles.md#15-land-the-smallest-functional-change), implementation splits into two ordered single-behavior follow-up build items unchanged from `8016445f2b54`:

1. **Follow-up Build Item 1 — Record `drain.run-scratch-process` in `tillr-drain` (the event):**
   - **Single behavior:** Register `drain.run-scratch-process` in `tools/tillr-drain/drain/`, scan `l.procRootPath()` once per minute from `Loop` and `keepSessionAlive` with startup seeding from un-cleared `drain.run-scratch-process` events, a `5s` post-`finished_at` grace, path-component-boundary scratch matching (`DurableDir` plus `{Kind: "scratch"}` `artifact_refs`, raw and symlink-resolved), and live-run exclusion across `lanes`, `read_only_runs`, in-flight `Loop` runs, and `/proc/<pid>/environ` (`TILLR_RUN_ID`, `TILLR_RUN_EXEC_ID`); add unit tests in `tools/tillr-drain/drain/run_scratch_process_test.go`; and run `just pack-sync`.
2. **Follow-up Build Item 2 — Add watch rule `drain-run-scratch-process-live` (the rule, ordered after Build Item 1):**
   - **Single behavior:** Add `tools/tillr-watch/rules/drain-run-scratch-process-live.toml` ([§3.2](#32-rule-specification-toolstillr-watchrulesdrain-run-scratch-process-livetoml); `8016445f2b54` wrote `"in config/tillr/watch/ (or tools/tillr-watch/rules/ once D-TW84 lands)"`, and `da8eedec` has since landed [`D-TW84`](../tillr-watch.md#D-TW84)) and scratch-store (`t.TempDir()`, [`D-TW41`](../tillr-watch.md#D-TW41)) evaluation tests in `tools/tillr-watch/run_scratch_process_rule_test.go` covering `tillr watch lint`, opening on `live_count > 0`, staying open when `item_id` is `landed` or `closed`, reopening `silence --until-change` when `process_keys` changes, and resolving with `condition-cleared` on `live_count == 0`, mirrored via `just pack-sync`.

### Candidate notes on out-of-scope surfaces

- **Rule language (`tools/tillr/watch/`):** No gap exists; unwindowed `predicate_match`, `group_by = "run_id"`, `fingerprint = ["process_keys"]`, `[rule.variables]`, and every guidance placeholder in [§3.2](#32-rule-specification-toolstillr-watchrulesdrain-run-scratch-process-livetoml) are already built.
- **Step 2 inline cleanup on [`../tillr-watch.md`](../tillr-watch.md) ([`D-TW-DR4`](./decision-record-files.md#D-TW-DR4)):** Per [`decision-record-files.md` §4.3](./decision-record-files.md#43-moving-d-tw89-520-on-item-request-2cd1fca1c2-d-tw-dr4), deleting inline [`§5.20`](../tillr-watch.md#520-process-left-alive-under-an-ended-runs-scratch-directory-d-tw89) and [`D-TW89`](../tillr-watch.md#D-TW89) from [`../tillr-watch.md`](../tillr-watch.md) (updating its frontmatter `next:` and opening `**Status:**` paragraph) and repointing `[D-TW89](../tillr-watch.md#D-TW89)` in [`./ended-run-scratch-worktree.md`](./ended-run-scratch-worktree.md) (`a8d1c06d2e5b`) and any later inbound references to `./ended-run-scratch-process.md#D-TW-ERSP1` runs in a separate follow-up cleanup item that adds zero files under `docs/design/tillr-watch/`, keeping every commit compliant with Clause 2 of [`decision-record-files.md` §3.1](./decision-record-files.md#31-pure-range-predicate-basehead).

---

## 5. Rejected Alternatives

Each alternative below was considered by the designer in `8016445f2b54` and set aside (the owner may overturn any of these dispositions except [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), [`D-TW48`](../tillr-watch.md#D-TW48), [`D-TW64`](../tillr-watch.md#D-TW64), and [`D-TW73`](../tillr-watch.md#D-TW73)):

| Alternative | Why it fails | Verdict |
| :--- | :--- | :--- |
| **Extend `drain.process-cpu` to include leaked run subprocesses** | `drain.process-cpu` monitors CPU tick deltas for `drain-loop`, `tillr-watch-wait`, and `tillr-view-serve`; mixing arbitrary leaked worker subprocesses conflates daemon CPU profiling with ended-run scratch-path liveness and rewrites unrelated monitoring. | Rejected. |
| **Rely on `survivingRunProcesses` alone** | Runs only at settlement via `/proc/<pid>/environ` (`TILLR_RUN_ID` and `TILLR_LOOP_STORE`), missing environment-scrubbed processes whose `/proc/<pid>/cmdline` names `<lane-dir>/durable/<run-id>`, processes inspected after settlement, and exit clearing events. | Rejected. |
| **Use `tillr-run exec` as the writer** | `Exec` in [`tools/tillr-run/run/exec.go`](../../../tools/tillr-run/run/exec.go) exits once `writeTerminal` commits and is no longer running minutes later to detect a surviving detached grandchild or append a clearing event when that process eventually exits. | Rejected. |
| **Read `/proc` directly inside `tillr-watch`** | Polling OS process state inside the watcher violates [`D-TW4`](../tillr-watch.md#D-TW4) and leaves no durable audit trail in `events` once the process exits. | Rejected. |
| **Use `consecutive_count` with `for_each = "processes"` and a rolling `window`** | As in [`drain-process-cpu-high.toml`](../../../tools/tillr-watch/rules/drain-process-cpu-high.toml), a rolling window requires rewriting store events every minute for the lifetime of a leak, resolves falsely as `condition-cleared` if the drain stops longer than `window` while the leaked process stays alive, and opens multiple episodes for one leaked process tree. | Rejected. |

---

## 6. Open Questions

None.

---

## 7. Decision Ledger

[`D-TW-ERSP1`](#D-TW-ERSP1) records a decision from the 2026-11-24 operator request and the designer (`8016445f2b54`, moved standalone on `2026-11-26`); it is **not** an owner ruling, and the owner may overturn any part of it. `Built` is checked against `3d346476295b`.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-TW-ERSP1"></a>**[D-TW-ERSP1](#D-TW-ERSP1)** | **Operator request and designer decision (not an owner ruling; owner may overturn; restates and replaces inline [`D-TW89`](../tillr-watch.md#D-TW89) from `8016445f2b54` unchanged):** (1) `tillr-drain` (`Loop`) records state-change `drain.run-scratch-process` events (`session_id`, `run_id`, `item_id`, `lane_id`, `scratch_dir`, `live_count`, `process_keys`, `process_summary`, `processes`, with `object_type = "run"` and `object_id = &runID`) from 1-minute `l.procRootPath()` scans for live non-zombie same-UID processes whose `/proc/<pid>/cmdline` names an ended run's scratch directory (`DurableDir` plus `{Kind: "scratch"}` `runs.artifact_refs`, raw and symlink-resolved on a path-component boundary after a `5s` post-`finished_at` grace, excluding live runs on `lanes`, `read_only_runs`, in-flight `Loop` runs, and `/proc/<pid>/environ` `TILLR_RUN_ID` / `TILLR_RUN_EXEC_ID`, seeded at startup from un-cleared events); (2) `drain-run-scratch-process-live` uses landed unwindowed `predicate_match` grouped by `run_id` (`match = 'event.live_count > 0'`, `clear_when = 'event.live_count == 0'`, `fingerprint = ["process_keys"]`); and (3) implementation splits into two ordered single-behavior follow-up build items — first the `drain.run-scratch-process` event in `tools/tillr-drain`, then `tools/tillr-watch/rules/drain-run-scratch-process-live.toml` and evaluation tests. | 2026-11-25 | [§2](#2-recorded-fact-drainrun-scratch-process-d-tw-ersp1), [§3](#3-landed-pattern-and-rule-contract-drain-run-scratch-process-live-d-tw-ersp1), [§4](#4-follow-up-build-items-and-candidate-notes-d-tw-ersp1), [§5](#5-rejected-alternatives) | no |
