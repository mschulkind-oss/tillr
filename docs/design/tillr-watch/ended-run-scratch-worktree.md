---
title: "Alert when a registered worktree remains under an ended run's scratch directory"
author: "Agent"
date: 2026-11-26
status: in-review
stage: DECIDED
stage-since: 2026-11-26
tags: [tillr, watch, drain, worktree, scratch, design]
summary: "Record transition-only drain.ended-run-scratch-worktree events from tillr-drain's Loop when git worktree list --porcelain reports a non-main worktree under an ended run's scratch directory, and watch them with an unwindowed predicate_match rule (drain-ended-run-scratch-worktree) grouped by worktree_path; split into two single-behavior follow-up build items (the event, then the rule)."
next: "Run the two ordered follow-up build items in Section 4 — first recording drain.ended-run-scratch-worktree in tools/tillr-drain/drain/, then adding drain-ended-run-scratch-worktree.toml and its evaluation tests; the owner may review D-TW-ERSW1."
depends-on:
  - ../tillr-watch.md
  - ../tillr-watch.md#D-TW2
  - ../tillr-watch.md#D-TW5
  - ../tillr-watch.md#D-TW41
  - ../tillr-watch.md#D-TW42
  - ../tillr-watch.md#D-TW48
  - ../tillr-watch.md#D-TW50
  - ../tillr-watch.md#D-TW63
  - ../tillr-watch.md#D-TW64
  - ../tillr-watch.md#D-TW73
  - ../tillr-watch.md#OQ-TW2
  - ./decision-record-files.md#R-TW-DR1
  - ./decision-record-files.md#R-TW-DR2
  - ./decision-record-files.md#D-TW-DR1
  - ../landing-principles.md#15-land-the-smallest-functional-change
related:
  - ../tillr-utility-contracts/tillr-watch.md
vantage:
  status-chip: true
---

# Alert when a registered worktree remains under an ended run's scratch directory

**Status:** 2026-11-26, verified at `11189ba68811`. Nothing specified in [`D-TW-ERSW1`](#D-TW-ERSW1) is built yet (`rg -n 'ended-run-scratch-worktree' tools config` returns nothing, and `test -f config/tillr/watch/drain-ended-run-scratch-worktree.toml` exits non-zero). Per owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`685934ce`) and [`D-TW-DR1`](./decision-record-files.md#D-TW-DR1), this decision record lives in its own standalone file under `docs/design/tillr-watch/` with zero edits to [`../tillr-watch.md`](../tillr-watch.md).

> **In short.** No event or watch projection records the repository's registered Git worktrees, so a worktree created inside a run's `<lane-dir>/durable/<run_id>` scratch directory stays silently registered in `.git/worktrees/` (and can hold a branch checked out) after the run ends. Having `*Loop` in `tools/tillr-drain/drain` record transition-only `drain.ended-run-scratch-worktree` events (`"present"` and `"cleared"`) from `listWorktrees` against ended runs' canonical scratch directories lets an unwindowed `predicate_match` rule grouped by `worktree_path` (`drain-ended-run-scratch-worktree`) open one alert per leftover registration and auto-resolve it once the responder saves any needed work and removes or prunes the registration.

**Why it matters.** On 2026-11-24, `item-request-4a7a6ff913` had to be closed unbuilt because none of the six landed rule patterns or store projections reads `git worktree list`, and eight sibling alert-rule requests worded `"one rule, one new file, change nothing else"` each cost a planning run and a design-seat answer ([§1.1](#11-what-happened-on-2026-11-24-and-why-no-existing-fact-suffices)).

**The shape.** `*Loop` in `tools/tillr-drain/drain` inspects non-main registered worktrees via `listWorktrees` (`git worktree list --porcelain` under `.git/worktree-registry.lock`), matches paths inside ended runs' canonical scratch directories, emits `drain.ended-run-scratch-worktree` only on `"present"` and `"cleared"` transitions, and feeds an unwindowed `predicate_match` rule (`drain-ended-run-scratch-worktree`) grouped by `worktree_path` ([§2](#2-recorded-fact-drainended-run-scratch-worktree-d-tw-ersw1), [§3](#3-landed-pattern-and-rule-contract-drain-ended-run-scratch-worktree-d-tw-ersw1)).

**Cost.** Two single-behavior follow-up build items — first the transition-only `drain.ended-run-scratch-worktree` producer in `tools/tillr-drain` (writing zero events on steady-state passes and making zero `git worktree list` calls on idle wakeups when no registration is tracked), then `drain-ended-run-scratch-worktree.toml` and its evaluation tests — with zero rule-language or database-schema changes ([§4](#4-follow-up-build-items-and-candidate-notes-d-tw-ersw1)).

**Start at [§2](#2-recorded-fact-drainended-run-scratch-worktree-d-tw-ersw1)** for the event contract and single writer, [§3](#3-landed-pattern-and-rule-contract-drain-ended-run-scratch-worktree-d-tw-ersw1) for the unwindowed `predicate_match` rule, and [§4](#4-follow-up-build-items-and-candidate-notes-d-tw-ersw1) for the two follow-up build items and out-of-scope candidate notes.

**Needs your ruling:** None. Standing owner rulings [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), [`D-TW48`](../tillr-watch.md#D-TW48), [`D-TW64`](../tillr-watch.md#D-TW64), and [`D-TW73`](../tillr-watch.md#D-TW73) govern; [`D-TW-ERSW1`](#D-TW-ERSW1) comes from the operator request and the designer and may be overturned by the owner.

**Reads with:** [`../tillr-watch.md`](../tillr-watch.md) (the baseline watch design), [`./decision-record-files.md`](./decision-record-files.md) (standalone watch decision records), and [`../landing-principles.md`](../landing-principles.md) (smallest functional change).

---

## 1. Problem, Existing Facts, and Authority Boundary

### 1.1 What happened on 2026-11-24 and why no existing fact suffices

On 2026-11-24, the operator filed `item-request-4a7a6ff913` worded `"one rule, one new file, change nothing else"` to alert when the repository has a registered Git worktree whose path is inside the scratch directory of a run that has ended (not a lane's own worktree, and not a live run's). That item was closed before a worker ran on it because inspecting [`tools/tillr/watch/rules.go`](../../../tools/tillr/watch/rules.go) and [`tools/tillr/watch/rule_schema.go`](../../../tools/tillr/watch/rule_schema.go) showed that none of the six landed patterns (`predicate_match`, `count_in_window`, `consecutive_count`, `alternating_pair`, `lag_behind`, `duration_without_event`) had a recorded fact to read (`rg -n 'worktree list' tools/tillr/watch tools/tillr-watch` returns nothing), and eight other alert-rule requests worded the same way each cost a planning run and a design-seat answer that day:

1. **`drain.trunk-core-worktree`** (`trunkCoreWorktreeKind` in [`tools/tillr-drain/drain/trunk_worktree.go`](../../../tools/tillr-drain/drain/trunk_worktree.go)) checks only whether shared repository config sets `core.worktree`, not `.git/worktrees/` registrations.
2. **`drain.ended-work-saved`, `drain.lane-kept`, and `drain.fork-leftover`** observe dirty or unmerged work inside a lane's own worktree (`<lane-dir>/worktree`) or `.forks/` checkouts. By contrast, `run.DurableDir(laneDir, runID)` in [`tools/tillr-run/run/exec.go`](../../../tools/tillr-run/run/exec.go) places each run's scratch directory at `<lane-dir>/durable/<run_id>`, a sibling of `<lane-dir>/worktree`, and `installGitGuard` in [`tools/tillr-run/run/git_guard.go`](../../../tools/tillr-run/run/git_guard.go) intercepts only `git init` and `git config` targeting the trunk common Git directory — so a worker or child subagent can run `git worktree add` into its own scratch directory and leave the registration in `<common-dir>/worktrees/` (holding a `.git/worktrees/` entry and any checked-out branch) after the run finishes.
3. **`drain.boundary-check`** (`BoundaryCheckKind` in [`tools/tillr-drain/drain/boundary.go`](../../../tools/tillr-drain/internal/boundary/boundary.go)) diffs pre-run and post-run snapshots for a single run's fence evaluation; it neither scans pre-existing scratch worktrees across ended runs nor records a clearing fact when an operator later removes or prunes a registration.
4. **Watch projections** (`items`, `charter`, `git_refs:`, `fixer_checks`, `green_proof:` in [`tools/tillr/watch/rule_schema.go`](../../../tools/tillr/watch/rule_schema.go)) read SQLite tables and Git refs rather than `git worktree list --porcelain`.

### 1.2 Placement and authority boundary

Although the 2026-11-24 request named `docs/design/tillr-watch.md` before [`./decision-record-files.md`](./decision-record-files.md) landed (`685934ce`), six standing owner rulings govern this document without modification:

- [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`2026-11-25`, `"ok, so save that"`): every new decision record about `tillr-watch` goes in its own small file under `docs/design/tillr-watch/`, and [`../tillr-watch.md`](../tillr-watch.md) is not edited when a record is added.
- [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2) (`2026-11-16`, `"Explicitly, we NEVER want fallbacks anywhere in this project."`): scratch-directory resolution uses one deterministic union rather than a fallback path, and no automatic remedy or fallback sweeper exists.
- [`OQ-TW2`](../tillr-watch.md#OQ-TW2) (`2026-11-21`, `"We don't cover issues. We use this to find the issues and fix them."`) and [`D-TW48`](../tillr-watch.md#D-TW48) (`2026-11-21`): leftover work is an alert, never automatically swept or deleted, because a scratch worktree can hold the only copy of unlanded commits or uncommitted edits.
- [`D-TW64`](../tillr-watch.md#D-TW64) (`2026-11-22`): every alert is investigated and ends in a recorded action, and instance variables scope silences to the specific condition instance (`worktree_path`).
- [`D-TW73`](../tillr-watch.md#D-TW73) (`2026-11-22`, `"I never want alerts for expected conditions."`): `[rule.when]` and the producer exclude live, paused, and resumed runs as well as lane and loop worktrees, and `[rule.guidance]` never calls a firing alarm `"expected"`.

Everything else in this document — [`D-TW-ERSW1`](#D-TW-ERSW1), choosing the `drain.ended-run-scratch-worktree` event name, single `*Loop` writer, payload schema, ended-run and scratch-directory matching, transition-only emission, unwindowed `predicate_match` rule, rejected alternatives, and the two single-behavior follow-up build items — is an **operator-request and designer decision (`2026-11-26`), not an owner ruling, and the owner may overturn any part of it**.

---

## 2. Recorded Fact: `drain.ended-run-scratch-worktree` ([`D-TW-ERSW1`](#D-TW-ERSW1))

*Operator-request and designer decision (not an owner ruling; the owner may overturn any part of this section).*

### 2.1 Event name, registration, and the one writer

- **Event kind:** `EndedRunScratchWorktreeKind = "drain.ended-run-scratch-worktree"`, registered in `init()` under `drain.Utility` (`"drain"`) in `tools/tillr-drain/drain` via `event.Register(EndedRunScratchWorktreeKind, Utility)`. Because [`tools/tillr-watch/main.go`](../../../tools/tillr-watch/main.go) already blank-imports `_ "github.com/mschulkind-oss/tillr-drain/drain"`, both `LoadRules` and `tillr watch lint` recognize `drain.ended-run-scratch-worktree` with no new cross-module dependency.
- **The one writer:** `*Loop` in `tools/tillr-drain/drain` (`observeEndedRunScratchWorktrees(ctx)` when `!l.nonLanding`), appending `event.Event{Kind: EndedRunScratchWorktreeKind, ObjectType: "run", ObjectID: &runID, Payload: body, At: l.cfg.Now().UTC()}`.
- **Active-pass placement, idle clearing, and non-scheduling classification:**
  1. **Active passes:** In `passMode` ([`tools/tillr-drain/drain/pass.go`](../../../tools/tillr-drain/drain/pass.go)), when `!l.nonLanding`, `*Loop` runs `observeEndedRunScratchWorktrees(ctx)` right beside `reclaimEndedItemLanes` ([`tools/tillr-drain/drain/ended_item_lane.go`](../../../tools/tillr-drain/drain/ended_item_lane.go)) after `drainCompletions`, logging store or Git read errors via `l.reportf` without faulting the loop. Any scratch worktree left behind by a just-completed run is recorded immediately before `l.idleState` is cached.
  2. **Prompt `"cleared"` emission while the queue is idle:** In `passMode`, when `l.idleState != nil` and `!l.nonLanding`, `*Loop` also calls `observeEndedRunScratchWorktrees(ctx)` before the `waitForTrigger` fast return **only while `len(l.activeEndedRunScratchWorktrees) > 0`** (at least one `"present"` scratch worktree is currently tracked). Steady-state idle loops (`len == 0`) spawn zero `git worktree list` subprocesses, whereas an operator running `git worktree remove` or `git worktree prune` while the queue is idle records `"state": "cleared"` on the next pass wakeup without waiting for new queue work.
  3. **Non-scheduling observation:** Classify `EndedRunScratchWorktreeKind` (`ObjectType == "run"`) as a non-scheduling observation in `isIdleTelemetryEvent` ([`tools/tillr-drain/drain/idle.go`](../../../tools/tillr-drain/drain/idle.go), and formerly `startStoreDelta.classify` in `tools/tillr-drain/drain/start_store_sync.go`, alongside `GateStartedKind` and `FastGateTimingKind`, and excluded beside `LoopPassKind` and `ProcessCPUKind` in `trigger_notify_test.go`), so `"present"` and `"cleared"` transitions never invalidate `l.idleState`.

### 2.2 Ended-run predicate, canonical scratch directories, and transition-only emission

1. **Ended-run predicate (per `run_id`, never per `lane_id`):** From a read snapshot of the store, a run `run_id` has **ended** if and only if all three conditions hold:
   - `runs` has a row for `run_id` whose `state` satisfies `isTerminalRunState(state)` in [`tools/tillr-drain/drain/end_unfinished_runs.go`](../../../tools/tillr-drain/drain/end_unfinished_runs.go) (`run.StateExited`, `run.StateFailedToStart`, or `run.StateTimedOut`, excluding `'paused'` runs and in-flight first-segment runs that have no `runs` row yet);
   - no live lease holds `run_id` (`NOT EXISTS (SELECT 1 FROM lanes WHERE state = 'leased' AND run_id = runs.run_id)` and `NOT EXISTS (SELECT 1 FROM read_only_runs WHERE run_id = runs.run_id AND settled = 0)`); and
   - `!resumeUnfinished(r, runID)` in [`tools/tillr-drain/drain/orphan.go`](../../../tools/tillr-drain/internal/procguard/orphan.go) (its newest `('run.resume', 'run.finish')` event is not `'run.resume'`, and no `quota_pauses` row holds `run_id`).

   Judging endedness per `run_id` catches an older ended run's leftover scratch worktree even after its lane leases a newer `run_id`, while never alerting on live, paused, or resumed runs ([`D-TW73`](../tillr-watch.md#D-TW73)).
2. **Deterministic scratch-directory union ([`R-TW-DR2`](./decision-record-files.md#R-TW-DR2)):** Each ended run `(run_id, lane_id, artifact_refs)` maps to the deterministic union of `run.DurableDir(l.laneDirPath(laneID), runID)` (`<lane-dir>/durable/<run_id>`, from `DurableDir` in [`tools/tillr-run/run/exec.go`](../../../tools/tillr-run/run/exec.go)) and every `run.ArtifactRef` ([`tools/tillr-run/run/apply.go`](../../../tools/tillr-run/run/apply.go)) with `Kind == "scratch"` and non-empty `Ref` in `runs.artifact_refs` (matching [`D-TW89`](../tillr-watch.md#D-TW89) and covering runs closed by `closeRun` in [`tools/tillr-run/run/lease.go`](../../../tools/tillr-run/run/lease.go) with `artifact_refs = '[]'` without any fallback branch). Paths are canonicalized via `canonicalDir` ([`tools/tillr-drain/drain/boundary.go`](../../../tools/tillr-drain/internal/boundary/boundary.go); when the directory itself has already been deleted from disk, canonicalizing the deepest existing ancestor via `filepath.EvalSymlinks` and joining the remaining suffix so symlinked ancestor directories still match `prunable` worktree entries).
3. **Registered worktree matching:** Read `listWorktrees(ctx, l.cfg.Root)` ([`tools/tillr-drain/drain/boundary.go`](../../../tools/tillr-drain/internal/boundary/boundary.go), under `withWorktreeSnapshotLock` on `.git/worktree-registry.lock`, never acquiring `land.lock`), skip `worktrees[0]` (the main checkout), canonicalize `wt.Path` the same way, and match `wtPath == scratchDir || strings.HasPrefix(wtPath, scratchDir + string(filepath.Separator))` (a strict path-component boundary so `run-1` never matches `run-10`). Because `<lane-dir>/durable/<run_id>` is disjoint from `<lane-dir>/worktree`, `.rolling/worktree`, `.replay/<sha>`, and `desk-<doc>/worktree`, lane and loop worktrees never match, while both on-disk and `prunable` (`wt.Prunable == true`) scratch registrations do.
4. **Transition-only emission:** Seed `l.activeEndedRunScratchWorktrees` (`worktree_path -> activeScratchWorktree`) once per `*Loop` lifetime by folding prior `drain.ended-run-scratch-worktree` rows from `events` in `seq` order (`"present"` inserts or updates `worktree_path`; `"cleared"` deletes `worktree_path`). On each observation:
   - Append `"state": "present"` (in sorted `worktree_path` order) **only** when a matched `worktree_path` is absent from `activeEndedRunScratchWorktrees` or its `(run_id, head_sha, branch, prunable)` tuple changed.
   - Append `"state": "cleared"` (in sorted `worktree_path` order, carrying `worktree_path`, `scratch_dir`, `run_id`, `lane_id`, `item_id`, and `repository` from the active entry) **once** when a tracked `worktree_path` is no longer matched (removed via `git worktree remove`, pruned via `git worktree prune`, or its `run_id` resumed), then delete `worktree_path` from `activeEndedRunScratchWorktrees`.
   - Steady-state passes write zero events; loop restarts do not re-emit `"present"`; a worktree removed while `tillr-drain` was stopped emits `"cleared"` on the startup pass.

### 2.3 Payload schema (`EndedRunScratchWorktreeFact`)

```json
{
  "state": "present",
  "worktree_path": "/workspace/.tillr/lanes/lane-2/durable/run-item-abc-a1/tmp-wt",
  "scratch_dir": "/workspace/.tillr/lanes/lane-2/durable/run-item-abc-a1",
  "run_id": "run-item-abc-a1",
  "lane_id": "lane-2",
  "item_id": "item-abc",
  "run_state": "exited",
  "head_sha": "f20627cf887d0000000000000000000000000000",
  "branch": "refs/heads/scratch-check",
  "prunable": false,
  "repository": "/workspace"
}
```

| Field | JSON type | Presence | Meaning |
| :--- | :--- | :--- | :--- |
| `state` | `string` | required | `"present"` when a non-main registered worktree lies at or under an ended run's scratch directory; `"cleared"` when a tracked `worktree_path` is no longer an ended-run scratch worktree. |
| `worktree_path` | `string` | required | Canonical filesystem path reported by `git worktree list --porcelain`; `group_by` subject key. |
| `scratch_dir` | `string` | required | Canonical scratch directory of the ended run that encloses `worktree_path`. |
| `run_id` | `string` | required | Ended run ID whose scratch directory encloses `worktree_path` (also `event.ObjectID` with `ObjectType = "run"`). |
| `lane_id` | `string` | required | `runs.lane_id` for `run_id`. |
| `item_id` | `string` | optional (`omitempty`) | `runs.item_id` when non-null and non-empty. |
| `run_state` | `string` | required on `"present"`, optional (`omitempty`) on `"cleared"` | Terminal `runs.state` (`"exited"`, `"failed-to-start"`, or `"timed-out"`). |
| `head_sha` | `string` | optional (`omitempty`) | `HEAD` SHA (`wt.Head`) from `git worktree list --porcelain` when present. |
| `branch` | `string` | optional (`omitempty`) | Checked-out ref (`wt.Branch`, for example `"refs/heads/..."`) from `git worktree list --porcelain` when not detached. |
| `prunable` | `boolean` | optional (`omitempty`) | `true` when `git worktree list --porcelain` marks the registration `prunable` (directory removed on disk while the `.git/worktrees/` entry remains). |
| `repository` | `string` | required | Canonical repository root (`l.cfg.Root`). |

---

## 3. Landed Pattern and Rule Contract: `drain-ended-run-scratch-worktree` ([`D-TW-ERSW1`](#D-TW-ERSW1))

*Operator-request and designer decision (not an owner ruling; the owner may overturn any part of this section).*

### 3.1 Landed pattern choice and evaluation mechanics

The rule uses the landed **unwindowed `predicate_match`** pattern (`PredicateMatch` in [`tools/tillr/watch/rules.go`](../../../tools/tillr/watch/rules.go), [`D-TW50`](../tillr-watch.md#D-TW50)): `loadWhen` sets `w.Window = 0` when `pattern = "predicate_match"`, `clear_when` is non-empty, and `window` is omitted. Zero changes to `tools/tillr/watch/` are needed.

How the landed engine evaluates each `worktree_path` subject:

1. **One episode per leftover registration (`group_by = "worktree_path"`, [`D-TW5`](../tillr-watch.md#D-TW5)):** Grouping by `worktree_path` opens one unresolved alert episode per leftover worktree path (even if one ended run created multiple scratch worktrees), automatically derives `worktree_path` as the rule's instance variable for scoped silences ([`D-TW64`](../tillr-watch.md#D-TW64), matching `TestShippedWatchRulesVariableDerivation` in [`tools/tillr/watch/variables_test.go`](../../../tools/tillr/watch/variables_test.go)), and avoids item-terminal auto-resolution ([`D-TW63`](../tillr-watch.md#D-TW63)) when `item_id` lands or closes while the worktree registration still exists.
2. **State-change reopening (`fingerprint = ["run_id", "head_sha", "branch"]`):** If an episode is silenced with `--until-change` and either its checked-out commit/branch moves or another run later leaves a worktree at the same path, the fingerprint changes and re-alerts the responder.
3. **Deterministic resolution (`clear_when = 'event.state == "cleared"'`):** Because `window` is omitted, an unaddressed registration never ages out silently; as soon as the responder removes or prunes the registration (or `run_id` resumes), the next `drain.ended-run-scratch-worktree` event with `state == "cleared"` resolves the episode (`condition-cleared`).

### 3.2 Rule specification (`config/tillr/watch/drain-ended-run-scratch-worktree.toml`)

```toml
# Reads registered kind drain.ended-run-scratch-worktree (transition-only
# observations of non-main Git worktrees registered under an ended run's
# scratch directory). Opens one unwindowed episode per worktree_path and clears
# when the registration is removed or pruned; executes no automatic sweeper.

[[rule]]
id = "drain-ended-run-scratch-worktree"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.ended-run-scratch-worktree"]

[rule.when]
group_by = "worktree_path"
fingerprint = ["run_id", "head_sha", "branch"]
pattern = "predicate_match"
match = 'event.state == "present"'
clear_when = 'event.state == "cleared"'

[rule.guidance]
summary = "Registered worktree {{subject}} remains inside ended run {{event.run_id}}'s scratch directory ({{event.lane_id}}, {{event.run_state}})."
check = [
  "Inspect git -C {{event.repository}} worktree list and, when {{subject}} still exists on disk, inspect git -C {{subject}} status and git -C {{subject}} log -n 5 before removing anything.",
  "Inspect {{event.run_id}} with tillr run status to see which run created {{subject}} and why it left the registration behind.",
]
do = [
  "Save any needed commits or files from {{subject}}, remove or prune the worktree registration by hand in {{event.repository}} so the next drain observation records cleared, and file a queue item if a harness or workflow defect caused the leak.",
]

[rule.delivery]
receiver = "foreground"
also_notify = ["subscribers"]
```

---

## 4. Follow-Up Build Items and Candidate Notes ([`D-TW-ERSW1`](#D-TW-ERSW1))

Per [`D-TW42`](../tillr-watch.md#D-TW42), [`landing-principles.md` §15](../landing-principles.md#15-land-the-smallest-functional-change), and the operator request (`"name the follow-up build items, each with one behavior (the event, then the rule)"`), implementation splits into two ordered single-behavior build items:

1. **Follow-up Build Item 1 — Record `drain.ended-run-scratch-worktree` transitions in `tools/tillr-drain` (the event):**
   - **Single behavior:** Register `EndedRunScratchWorktreeKind = "drain.ended-run-scratch-worktree"` under `drain.Utility` (`"drain"`), observe non-main worktrees from `listWorktrees` against ended runs' canonical scratch directories during `passMode` when `!l.nonLanding` (reconstructing active `"present"` entries from prior `events` rows once per `*Loop` lifetime, emitting only `"present"` and `"cleared"` transitions, and running before the idle `waitForTrigger` fast return while `len(activeEndedRunScratchWorktrees) > 0`), classify `EndedRunScratchWorktreeKind` (`ObjectType == "run"`) as a non-scheduling observation in `idle.go` and `start_store_sync.go` (and `trigger_notify_test.go`), and sync via `just pack-sync`.
   - **Verification:** Unit tests in `tools/tillr-drain/drain/` covering event registration under `"drain"`, live/paused/resumed runs excluded, older ended run caught even after its lane leases a newer run, runs with populated `artifact_refs` and runs closed with `artifact_refs = '[]'`, lane worktrees and main checkout excluded, on-disk and `prunable` scratch registrations, startup fold deduplication across `*Loop` restarts, prompt `"cleared"` emission while `l.idleState != nil`, and non-waking telemetry classification. Changes zero files under `config/tillr/watch/`, `tools/tillr/watch/`, or `tools/tillr-watch/`.
2. **Follow-up Build Item 2 — Add `drain-ended-run-scratch-worktree` rule and evaluation tests (the rule, ordered after Item 1):**
   - **Single behavior:** Add `config/tillr/watch/drain-ended-run-scratch-worktree.toml` ([§3.2](#32-rule-specification-configtillrwatchdrain-ended-run-scratch-worktreetoml)), register `"drain.ended-run-scratch-worktree"` in `registerShippedWatchKinds` in [`tools/tillr/watch/variables_test.go`](../../../tools/tillr/watch/variables_test.go), add scratch-store (`t.TempDir()`, [`D-TW41`](../tillr-watch.md#D-TW41)) evaluation tests in `tools/tillr-watch/`, and sync via `just pack-sync`, without changing `tools/tillr-drain/`.
   - **Verification:** Scratch-store unit tests in `tools/tillr-watch/` and `tools/tillr/watch/` verifying `LoadRules`, `TestShippedWatchRulesVariableDerivation` (`worktree_path` variable), `tillr watch lint` (`Skipped == 0`), `1` open episode per distinct `worktree_path` on `state == "present"`, episode persistence when `item_id` closes, `--until-change` reopening when `(run_id, head_sha, branch)` changes, and automatic `condition-cleared` resolution on `state == "cleared"`.

### Candidate notes on out-of-scope surfaces

- **Writing the event code and writing the rule file:** Deferred to Follow-up Build Items 1 and 2 per `"Change nothing else"`.
- **Rule language (`tools/tillr/watch/`):** No gap exists; unwindowed `predicate_match` (`clear_when` with `window` omitted, [`D-TW50`](../tillr-watch.md#D-TW50)), `group_by = "worktree_path"`, `fingerprint`, and every `{{event.<field>}}` placeholder in [§3.2](#32-rule-specification-configtillrwatchdrain-ended-run-scratch-worktreetoml) are already built.
- **Shipped-versus-repository watch rules ([`../tillr-watch.md` §10](../tillr-watch.md#10-build-slices-for-shipped-and-repository-watch-rules), [`D-TW83`](../tillr-watch.md#D-TW83)–[`D-TW88`](../tillr-watch.md#D-TW88)):** `config/tillr/watch/` is currently the active rules directory at `11189ba68811`; if [`D-TW84`](../tillr-watch.md#D-TW84) / [`D-TW88`](../tillr-watch.md#D-TW88) lands before Follow-up Build Item 2, `drain-ended-run-scratch-worktree.toml` (`scope = "core"`) goes in `tools/tillr-watch/rules/` and its `just pack-sync` mirror instead.
- **Other rules, automatic sweeping, and Git guard interception:** Other watch rules, automatic sweeping of leftover scratch worktrees (barred by [`D-TW48`](../tillr-watch.md#D-TW48) and [`OQ-TW2`](../tillr-watch.md#OQ-TW2)), and teaching `installGitGuard` ([`tools/tillr-run/run/git_guard.go`](../../../tools/tillr-run/run/git_guard.go)) to block `git worktree add` inside a run's scratch directory are out of scope per the 2026-11-24 request.

---

## 5. Rejected Alternatives

Each alternative below was considered by the designer and set aside (the owner may overturn any of these dispositions except [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), [`D-TW48`](../tillr-watch.md#D-TW48), [`D-TW64`](../tillr-watch.md#D-TW64), and [`D-TW73`](../tillr-watch.md#D-TW73)):

| Alternative | Why it fails | Verdict |
| :--- | :--- | :--- |
| **Add a `worktrees` projection to `tools/tillr/watch`** | Extends the rule language and evaluator (out of scope per the request), couples `tillr-watch` to `--lane-root`, `.git/worktree-registry.lock`, `runs`, `lanes`, `read_only_runs`, and `quota_pauses`, and leaves no durable audit trail in `events` once the worktree is removed. | Rejected. |
| **Emit `drain.ended-run-scratch-worktree` from `tillr-run` at `run.Exec` exit** | Misses runs killed by `SIGKILL`, OOM, or container exit that are closed later by `ReapExpiredLeases` or `end_unfinished_runs`, misses registrations predating startup, and cannot emit `"cleared"` when an operator removes or prunes the registration after `tillr-run` has exited. | Rejected. |
| **Append an un-deduplicated event every pass with a `1h` window (like `drain.trunk-core-worktree`)** | Unlike `core.worktree`, a leftover scratch worktree leaves `tillr-drain` running at `PassInterval = 1s`, writing up to 3,600 events per hour per registration, or letting an unaddressed registration age out silently if emission stops. | Rejected. |
| **Group by `run_id` or `item_id` instead of `worktree_path`** | Grouping by `item_id` auto-closes the episode under [`D-TW63`](../tillr-watch.md#D-TW63) when the item lands or closes even though the leftover worktree still exists; grouping by `run_id` collapses multiple scratch worktrees created in one run into a single episode whose `clear_when` on one path would prematurely resolve the other. | Rejected. |
| **Judge endedness per `lane_id` rather than per `run_id`** | Hides an ended run's leftover scratch worktree whenever its lane leases a subsequent `run_id`, delaying or toggling the alert across unrelated work on the same lane. | Rejected. |

---

## 6. Open Questions

None.

---

## 7. Decision Ledger

[`D-TW-ERSW1`](#D-TW-ERSW1) records a decision from the 2026-11-24 operator request and the designer; it is **not** an owner ruling, and the owner may overturn any part of it. `Built` is checked against `11189ba68811`.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-TW-ERSW1"></a>**[D-TW-ERSW1](#D-TW-ERSW1)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** (1) register `drain.ended-run-scratch-worktree` (`EndedRunScratchWorktreeKind`) under `drain.Utility` (`"drain"`) with `*Loop` in `tools/tillr-drain/drain` as the single writer (`observeEndedRunScratchWorktrees` when `!l.nonLanding`, including active-entry clearing while `l.idleState != nil` and non-scheduling telemetry classification in `idle.go` and `start_store_sync.go`), emitting transition-only `"state": "present"` and `"state": "cleared"` events (`worktree_path`, `scratch_dir`, `run_id`, `lane_id`, `item_id`, `run_state`, `head_sha`, `branch`, `prunable`, `repository`) when a non-main worktree in `listWorktrees` lies under an ended run's canonical scratch directory union (`run.DurableDir` plus `{Kind: "scratch"}` `runs.artifact_refs`); (2) watch `drain.ended-run-scratch-worktree` with landed unwindowed `predicate_match` (`group_by = "worktree_path"`, `fingerprint = ["run_id", "head_sha", "branch"]`, `match = 'event.state == "present"'`, `clear_when = 'event.state == "cleared"'`) in `drain-ended-run-scratch-worktree.toml`, guiding manual inspection and removal/pruning with zero automatic sweeper; and (3) build in two ordered single-behavior follow-up items — first the `drain.ended-run-scratch-worktree` event in `tools/tillr-drain`, then `drain-ended-run-scratch-worktree.toml` and scratch-store evaluation tests. | 2026-11-26 | [§2](#2-recorded-fact-drainended-run-scratch-worktree-d-tw-ersw1), [§3](#3-landed-pattern-and-rule-contract-drain-ended-run-scratch-worktree-d-tw-ersw1), [§4](#4-follow-up-build-items-and-candidate-notes-d-tw-ersw1), [§5](#5-rejected-alternatives) | no |
