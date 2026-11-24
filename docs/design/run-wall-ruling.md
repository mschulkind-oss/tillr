---
title: "Design ruling: remove the run wall and judge committed candidates before wall status"
status: accepted
stage: CURRENT
updated: 2026-11-15
tags:
  - tillr
  - drain
  - run
  - timeout
  - state-machine
  - design-ruling
summary: "Remove the default 1800s run wall from backend-policy.json and the three backend adapters, allow optional unbounded leases (timeout_ms == 0 -> leased_until NULL), reorder settle so any run that produced a commit transitions to candidate and runs the gate regardless of wall status, and auto-resume an uncommitted walled run once per attempt in settle before resetting the lane."
depends-on:
  - ./momentum-principle.md
  - board-utilities.md
  - ../reference/tillr-drain-successor.md
---

# Design ruling: remove the run wall and judge committed candidates before wall status

> **Carried out** in commit `adc9b1269`: no adapter sets a run wall by default. This document stays as the record of the ruling.
>
> **Whose ruling.** "Ruling" here is a design agent's word: `D-RW1` to `D-RW4` are the designer's decisions of 2026-11-15 (the commit that added this document calls it "the designer's ruling"), and the owner may overturn any of them. No words of the owner's are recorded for them.

**Ruling.** Remove the default per-run wall across `tillr`: delete `timeout_seconds` from [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json), drop the hardcoded `wall=1800` `timeout(1)` wrapper in `run-pi`, `run-muse`, and `run-agy` unless `--timeout` is explicitly passed, and allow `timeout_seconds: 0` / `timeout_ms: 0` in `tillr-run` and `tillr-drain` as an unbounded run whose lane lease carries `leased_until = NULL` (superseding [`board-utilities.md`](./board-utilities.md) section 5.6 / `D-UT17`). When an explicit wall does fire (`StateTimedOut` or exit `124`), fix `settle` in both [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) and [`docs/reference/tillr-drain-successor.md`](../reference/tillr-drain-successor.md#transition-table) (formerly the drain successor design's section 2.3) to check `HEAD != base` **before** `AttemptUnjudged` so any committed candidate goes to `Verify` / `judge`, and when `HEAD == base`, auto-resume **once per attempt** (`!record.Resumed`) in `settle` while the lane still holds its worktree, `brief.md`, and `session-id` before resetting the lane. Keep `AttemptUnjudged` outside `AttemptInfrastructure`, add no item state, and bump no schema version.

---

## 1. Should the wall exist at all?

**Decision (`D-RW1`):** **No.** Remove the default wall everywhere; keep `timeout_seconds` / `timeout_ms` only as an optional per-backend or per-request bound (`0` or omitted means no wall). Keep `ClassifyAttempt` (`tools/tillr-drain/drain/failover.go`) mapping `StateTimedOut` and `adapterWallExit` (`124`) to `AttemptUnjudged` outside `AttemptInfrastructure` — reclassifying a wall as infrastructure would re-run long work from scratch on a second backend and increment `InfraFailures`, doubling waste instead of removing the cause.

**Evidence and survey of existing mechanisms:**

- **A total-run wall is a budget, not a fault detector.** [`docs/design/momentum-principle.md`](../design/momentum-principle.md) forbids budgets that stop valid work. In the measured wave, the 1800s wall caught zero hangs, stranded two runs that had already committed (`04b`, `04c`), and killed `04d` 18 seconds after all checks passed — wiping 28 minutes of reasoning and 5 verified edits.
- **Two independent 1800s walls exist today.** `run.Exec` (`tools/tillr-run/run/exec.go`) kills `-pgid` via `context.WithTimeout` from `backend-policy.json`'s `timeout_seconds: 1800`, **and** `run-pi`, `run-muse`, and `run-agy` (`tools/tillr-run/backends/*/run-*`) independently wrap the harness in `timeout "$wall"` with `wall=1800` (plus `--print-timeout "${wall}s"` in `run-agy`) while `Backend.command` (`tools/tillr-run/run/request.go`) never passes `--timeout`. Removing only the policy key would leave the adapter wall firing at 1800s.
- **Every legitimate failure claimed for the wall is already handled elsewhere — except one that a total-run wall cannot solve anyway:**
  1. *Normal exit or harness crash:* `run.Apply` (`tools/tillr-run/run/apply.go`) clears `lanes.state` and `leased_until` in the same SQLite transaction that writes the terminal `run.record`; `SpendExtractorTimeout` (`60s` in `exec.go`) bounds post-exit report extraction.
  2. *Operator stop or wedged child on shutdown:* `ProcessSteps.Run` (`tools/tillr-drain/drain/steps.go`) sends `SIGTERM` with `cmd.WaitDelay = 30 * time.Second`; `execFace` (`tools/tillr-run/main.go`) catches `SIGINT`/`SIGTERM`, kills the process group, and writes the terminal record on `context.WithoutCancel(ctx)`.
  3. *Orphaned lane lease after `tillr-run` itself is `SIGKILL`ed:* [`board-utilities.md`](./board-utilities.md) section 5.6 (`D-UT17`) required `timeout_ms > 0` solely to populate `lanes.leased_until` (`ClaimLease` in `tools/tillr-run/run/lease.go`). Survey of the tree shows `NULL`-expiry leases are **already implemented end to end**: `lanes.leased_until` (`tools/tillr-run/run/schema.go`) is nullable; `expiredLeases` (`lease.go`) skips `leased_until IS NULL`; `readLanes` and `activeLeases` (`tools/tillr-drain/drain/plan.go`) hold `NULL`-expiry lanes; `Loop.planNothing` (`loop.go`) stops cleanly with `StopLanesLeased` naming `tillr run release <lane>`; and `ReleaseLane` (`lease.go`) frees a `NULL`-expiry lane immediately (requiring only a one-line guard in `closeRun` so `LeasedUntil == 0` stamps `now` rather than Unix epoch `0`).
  4. *Silent TCP/stream stall inside a harness (`D-LR4`):* Nothing in `tillr-run` or `tillr-drain` probes mid-turn socket liveness, and a wall on total run duration cannot distinguish a stalled socket from 28 minutes of active thinking (`run-wall-evidence.md` section 5). Live run activity is already on screen (`tillr-view`, `tillr run lanes`, `git-trace2.jsonl`, `bash-shim.sh`; `run-wall-evidence.md` section 1). Any socket-idle timeout belongs per HTTP read inside the harness, never on total run duration in `tillr`.

---

## 2. When the wall fires on a run that produced a commit

**Decision (`D-RW2`):** **Reorder `settle` so commit-produced (`HEAD != base`) dominates `AttemptUnjudged`.** Whenever an explicit wall (`StateTimedOut` or exit `124`) fires on a run whose target `HEAD` moved off `base` (and whose log footer is not `RESULT: blocked`), `settle` transitions the item to **candidate** and runs `Verify` (`judge`), exactly as for `AttemptAnswered`. Once `Steps.Gate` records a `gate.verdict` (`judged == true`), that verdict governs `plan`, `AuthorizeRetry`, and `tillr-view` over `AttemptUnjudged`.

**Evidence:**

- **State-model contradiction.** [`tillr-drain-successor.md`](../reference/tillr-drain-successor.md#the-successor-function-and-derived-positions) defined **candidate** as *"the run's terminal record is in the store, the run produced a commit, and the gate has not judged it"*, yet its [transition table](../reference/tillr-drain-successor.md#transition-table) (`running -> settle`) and `Loop.settle` (`loop.go`) branched on `AttemptUnjudged` before reading `HEAD`, stranding committed runs `04b` and `04c` in `held` (`run-unjudged`).
- **Why the wall never needs to dominate a committed tree.** Every hazard of a mid-run kill after a commit is already caught mechanically inside `Loop.Verify` (`loop.go`):
  1. *Half-written second edit left uncommitted at kill time:* `Loop.fenceViolation` runs `git status --porcelain` before `Steps.Gate` and refuses any dirty or untracked file with `boundary-scope-escape` (`RefusalFence`), pinning `HEAD` and holding the item.
  2. *Out-of-scope commit:* `Loop.fenceViolation` checks `git diff --name-only -z base..head` against `fence.txt` before `Steps.Gate`.
  3. *Incomplete commit that breaks checks:* `Steps.Gate` runs the planned checks against `HEAD^{tree}`, records `RefusalGate` (`gate-failed`) without freezing `item_criteria`, and pins `HEAD` at `refs/tillr/runs/<run_id>`.
- **Companion retry-deadlock fix required by this reordering.** Today `plan.go` (`retryHoldReason`, `retryFor`), `retries.go` (`AuthorizeRetry`), and `tools/tillr-view/dispatchable.go` (`retryRefused`) assume `judged` and `AttemptUnjudged` are mutually exclusive. If a walled-but-committed candidate reaches `Gate` and fails, both `judged` and `AttemptUnjudged` become true on that `run_id`: `retryHoldReason` would misreport `run-unjudged` instead of `gate-failed`, and `AuthorizeRetry` / `retryFor` / `retryRefused` would reject **every** single-string `Acknowledge` value (`judged` requires `"gate-failed"` while `AttemptUnjudged` requires `"unjudged"`). Guarding the unjudged branch with `!judged` makes `gate.verdict` authoritative once the gate has spoken.

---

## 3. When the wall fires on a run that produced no commit

**Decision (`D-RW3`):** **Auto-resume once per attempt in `settle` (`!record.Resumed`), in place on the same lane and `run_id`, before `abandon` resets the worktree.** If the resumed segment commits, Decision 2 (`D-RW2`) sends it to `Verify` / `judge`; if it hits the wall a second time without a commit (or if the lane lacks a valid `session-id` or `brief.md`), `settle` resets the lane and holds the item as `run-unjudged`. Never auto-resume in `expire` (`ReapExpiredLeases`), where the runner vanished unobserved.

**Evidence and survey of existing mechanisms:**

- **Existing mechanism and why its current wiring is insufficient.** `tools/tillr-drain/drain/resume.go` (`Resume`) and `tools/tillr-run/run/exec.go` (`Request.Resume`, `ReadSessionID`, `openRunLog` with `O_APPEND`, `appendResume`, `Record.Resumed`) already continue a killed run on the same `run_id`, warm harness session (`--session-id`), and staged worktree without spending a retry or failover hop. Today `Resume` is wired only to the manual CLI face `tillr-drain resume`, while `Loop.settle` (`loop.go`) immediately calls `abandon` (`resetLane` -> `git reset --hard main`) — destroying uncommitted worktree edits (`04d`) and freeing the lane to be overwritten by the next dispatch (`LaneStager.Stage` replaces `brief.md` and `session-id`), making even manual `tillr-drain resume` impossible.
- **Why "wait for a person" fails the reconstruction constraint.** An uncommitted walled run has `HEAD == base`, so `pin` writes no commit ref beyond `base`; its only artifacts are untracked lane files that vanish on reset or store reconstruction from committed documents. Giving the warm session one continuation segment while its verified worktree edits (`04d`) are still on disk converts uncommitted work into a judged commit (landed on `main` or pinned at `refs/tillr/runs/<run_id>`).
- **Why once per attempt (`!record.Resumed`), not unbounded and not once per item.** `Record.Resumed` (`apply.go`) and the `run.resume` event (`exec.go`) are already scoped to `run_id` (one attempt), requiring zero schema or state changes. Bounding at one `run.resume` per `run_id` prevents an infinite wall loop on a genuinely stuck prompt, while allowing a fresh operator-authorized retry (`ActionRetry`, new `run_id`) its own clean attempt.

---

## 4. What is the smallest diff?

**Decision (`D-RW4`):** **Ship `D-RW1`–`D-RW3` as one queue item (~75 LOC of production changes across `tillr-run`, `tillr-drain`, `tillr-view`, and `backend-policy.json`, plus test updates and `just pack-sync`).** Splitting wall removal from the `settle` ordering fix leaves explicit-timeout tests and reaped runs with the `judged && AttemptUnjudged` deadlock; landing them together touches one cohesive seam with no schema migration and no new item state.

---

## The smallest diff

### Fence (`allowed_paths`)

```json
[
  "config/tillr/backend-policy.json",
  "tools/tillr-run/",
  "tools/tillr-drain/",
  "tools/tillr-view/",
  "tests/tillr-muse-backend.test.py",
  "docs/design/board-utilities.md",
  "docs/reference/tillr-drain-successor.md"
]
```

*(Note: `fencePaths` in `tools/tillr-drain/drain/stage.go` automatically derives `yolo-packs/tillr/files/local/share/tillr/{tillr-run,tillr-drain,tillr-view}/`, `yolo-packs/tillr/skills/tillr-operating/SKILL.md`, and `tests/tillr-docs.test.py` from `tools/tillr-*/`, but derives `tests/tillr-muse-backend.test.py` only when `tools/tillr-run/backends` is named directly — so `tests/tillr-muse-backend.test.py` and `config/tillr/backend-policy.json` must be listed explicitly.)*

### File-by-file changes

1. **`config/tillr/backend-policy.json`** — Remove `"timeout_seconds": 1800` from `agy`, `muse`, `pi`, and `pi-designer`.
2. **`tools/tillr-run/backends/pi/run-pi`, `tools/tillr-run/backends/muse/run-muse`, `tools/tillr-run/backends/agy/run-agy`** — Default `wall=` (empty) instead of `wall=1800`. Prepend `timeout "$wall"` (and pass `--print-timeout "${wall}s"` in `run-agy`) only when `--timeout` was explicitly passed with `wall > 0`.
3. **`tools/tillr-run/run/request.go` & `tools/tillr-drain/drain/policy.go`** — Mark `Backend.TimeoutSeconds` `omitempty`; in `Backend.Missing()`, refuse `b.TimeoutSeconds < 0` instead of `<= 0` (`0` = unbounded). When `b.TimeoutSeconds > 0`, `Backend.command()` passes `--timeout <seconds>` so adapter and runner bounds cannot silently diverge.
4. **`tools/tillr-run/run/exec.go` & `tools/tillr-run/run/lease.go`** — Mark `Request.TimeoutMS` `omitempty`; refuse `< 0` instead of `<= 0` in `checkRequest` and `ClaimLease`. When `req.TimeoutMS == 0`: `Exec` uses `context.WithCancel(ctx)` instead of `context.WithTimeout`, and `ClaimLease` writes `leased_until = NULL` (`claim.LeasedUntil` `0` / `omitempty`). In `closeRun`, when `claimed.LeasedUntil == 0`, set `FinishedAt` to `now.UTC()` instead of `time.UnixMilli(0)`.
5. **`tools/tillr-drain/drain/stage.go` & `tools/tillr-run/backends/brief-template.md`** — Render `TIMEOUT_SECONDS` (or `- **Timeout:**` line) as `"none"` when `spec.TimeoutMS == 0` and `"<N> seconds"` when `> 0`, so unbounded briefs never print `Timeout: 0 seconds`.
6. **`tools/tillr-drain/drain/loop.go` (`Loop.settle`)** — After `AttemptInfrastructure` handling, check `reportFooter(log) == "blocked"` and read `headSHA` vs `baseSHA` **before** `class == AttemptUnjudged`:
   - If `headSHA != baseSHA`, reset `l.infraStreak = 0` and call `l.Verify(...)` regardless of whether `class` is `AttemptAnswered` or `AttemptUnjudged`.
   - If `headSHA == baseSHA` and `class == AttemptUnjudged`: when `!record.Resumed`, `<lane-dir>/brief.md` exists, and `run.ReadSessionID(l.laneDirPath(lane)) != ""`, execute one resumed run (`resumedReq := request; resumedReq.Resume = true`) inside `snapshot` / `Steps.Run` / `closeRunBracket` (honoring `ctx.Err()`), then settle the resumed result once; otherwise call `l.abandon` (`run-unjudged`).
7. **`tools/tillr-drain/drain/plan.go`, `tools/tillr-drain/drain/retries.go`, `tools/tillr-view/dispatchable.go`** — Check `gateResults[latest] == GateFailed` before `AttemptUnjudged` in `retryHoldReason`, and guard the `AttemptUnjudged` acknowledgment check with `!judged` in `retryFor`, `AuthorizeRetry`, and `retryRefused`.
8. **Docs, tests, and pack mirror** — Amend [`docs/design/board-utilities.md`](./board-utilities.md) (section 5.6, `D-UT17`) and [`docs/reference/tillr-drain-successor.md`](../reference/tillr-drain-successor.md#transition-table) (`running -> settle` and `dispatch` lease prose; graduated from the former design's section 2.3); update the negative-timeout / wall assertions in `tools/tillr-run/run/{request_test.go,lease_test.go}`, `tools/tillr-run/{main_test.go,backends_test.go}`, `tools/tillr-drain/drain/policy_test.go`, `tools/tillr-drain/{main_test.go,loop_test.go}`, `tools/tillr-view/dispatchable_test.go`, and `tests/tillr-muse-backend.test.py`; add unit tests for three cases:
   - an unbounded `TimeoutMS == 0` execution, its `NULL` lease, and `ReleaseLane`;
   - a walled-but-committed run reaching `Gate`/`Land`, and remaining retryable via
     `--acknowledge gate-failed` if `Gate` fails;
   - an uncommitted walled run auto-resuming once, and holding on a second
     uncommitted wall.
   Then run `just pack-sync`.

### Queue item criteria

1. `config/tillr/backend-policy.json` carries no `timeout_seconds`, and `run-pi`, `run-muse`, and `run-agy` run without `timeout(1)` when `--timeout` is omitted while still honoring an explicit `--timeout N` (`python3 tests/tillr-muse-backend.test.py`).
2. `tillr-run` and `tillr-drain` accept `timeout_seconds: 0` / `timeout_ms: 0` (refusing `< 0`), lease the lane with `leased_until IS NULL`, release the lane atomically on terminal `run.record`, and stamp `finished_at = now` on manual `tillr run release` (`go test ./...` in `tools/tillr-run`).
3. In `Loop.settle`, a `StateTimedOut` or exit-`124` run with `HEAD != base` proceeds through `Verify` (`fenceViolation` -> `Gate` -> `Land`), and if `Gate` fails, `drain plan`, `AuthorizeRetry`, and `tillr-view` classify the hold as `gate-failed` and accept `--acknowledge gate-failed` (`go test ./...` in `tools/tillr-drain` and `tools/tillr-view`).
4. In `Loop.settle`, a `StateTimedOut` or exit-`124` run with `HEAD == base` and a valid lane `session-id` auto-resumes at most once (`!record.Resumed`) before resetting the lane, landing if the resumed segment commits and holding as `run-unjudged` if the resumed segment also produces no commit (`go test ./...` in `tools/tillr-drain`).
5. `just pack-sync` leaves zero diff under `yolo-packs/tillr/` (`python3 tests/pack-sync.test.py` and `python3 tests/tillr-docs.test.py`).

---

## Decision ledger

Every row is a **designer's decision; the owner may overturn it**.

| ID | Ruling | Rejects |
| :--- | :--- | :--- |
| `D-RW1` | Remove default `timeout_seconds` from `config/tillr/backend-policy.json` and default `wall=1800` from `run-pi`, `run-muse`, and `run-agy`; allow `timeout_ms == 0` (`leased_until = NULL`), superseding `D-UT17` and `D-LR4` on mandatory positive run walls while keeping `AttemptUnjudged` outside `AttemptInfrastructure`. | Keeping or tuning the 1800s wall (violates [`momentum-principle.md`](./momentum-principle.md); pure false-positive loss in the measured wave); reclassifying walls as `AttemptInfrastructure` (wastes failover budget on long work). |
| `D-RW2` | Check `HEAD != base` before `AttemptUnjudged` in `settle` (`loop.go` and [`tillr-drain-successor.md`](../reference/tillr-drain-successor.md#transition-table)), and treat `gate.verdict` (`judged`) as superseding `AttemptUnjudged` in `plan.go`, `retries.go`, and `tillr-view/dispatchable.go`. | Holding committed runs as `run-unjudged` (`04b`, `04c`); leaving `judged && AttemptUnjudged` unsatisfiable in `AuthorizeRetry`. |
| `D-RW3` | Auto-resume an uncommitted walled run (`HEAD == base`, `AttemptUnjudged`) once per attempt (`!record.Resumed`) in `settle` before `abandon` resets the lane; never auto-resume in `expire`. **Its "once" is superseded** by the owner's later rulings that a killed run resumes uncounted and that nothing carries an attempt count: an uncommitted walled run is resumed, uncounted. Built in part: a continuation of uncommitted fenced work is now bounded only by the identical-repeat rule, while a wall-cut run with no uncommitted work keeps its one continuation (`resumeAfterWall` in `tools/tillr-drain/drain/loop.go`). | Immediate `abandon` + `held` (destroys uncommitted edits like `04d` and leaves non-reconstructible state); unbounded resumes. |
| `D-RW4` | Land `D-RW1`–`D-RW3` as a single fenced queue item with `just pack-sync` and doc updates to [`board-utilities.md`](./board-utilities.md) and [`tillr-drain-successor.md`](../reference/tillr-drain-successor.md). | Splitting wall removal from the `settle`/`AuthorizeRetry` ordering fix, or introducing a schema migration / sixth item state. |

---

## Open questions

None.