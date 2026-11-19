---
title: "Where a quick request's time goes in tillr, 2026-11-19"
status: accepted
stage: CURRENT
updated: 2026-11-19
tags: [tillr, latency, drain, gate, measurement, report]
summary: "A measurement of where end-to-end wall time goes for quick items in the live tillr loop on 2026-11-19, read from the live store through the read-only CLI and from the repository at d833451fe. Across three one-commit single-worker requests and one small planned-build request, active model tool work was 11.2 s to 39.7 s for the single-worker items, while end-to-end wall time was 634.0 s to 9,380.8 s. Five ranked bottlenecks account for the gap: two-pass post-run settlement and synchronous pass planning, broad and uncached gate checks, the default three-node workflow and its read-only commit guard, mid-pass plan invalidation on rank or trunk updates, and SQLite writer contention between tillr-drain and tillr-land."
---

# Where a quick request's time goes in tillr, 2026-11-19

**Status:** 2026-11-19. Measured on the live repository loop through the read-only `tillr` CLI faces (`tillr view passes`, `tillr view events`, `tillr view status`, `tillr view lanes`, `tillr item show`, `tillr drain policy`) and against the code in this tree at commit `d833451fe`. Nothing outside this report was changed.

> **In short.** On 2026-11-19, four small requests took **10m 34s**, **14m 36s**, **27m 40s**, and **2h 36m 21s** from filing to landing. On the three single-worker requests (`item-request-ad08eb1ca6`, `item-request-c0f1e4924c`, and `item-request-cfae4676fc` attempt 2), the agent's active tool loop took **31.6 s**, **39.7 s**, and **11.2 s** respectively. Even excluding a manual fault hold on `item-request-cfae4676fc`, the surrounding loop, settlement handoff, and gate spent **9x to 65x** longer than the worker spent editing and verifying the change.

Where code identifiers (`pass`, `lane`, `settle`, `judge`) differ from the user-facing words settled in [`docs/design/tillr-vocabulary.md`](../../design/tillr-vocabulary.md), this report names the code identifier beside the step it measures so each finding maps straight to `tools/tillr-drain`, `tools/tillr-gate`, `tools/tillr-run`, and `tools/tillr-land`. All timestamps below are UTC (`2026-11-19`), read from store events and per-run step records. Where existing records cannot establish a sub-interval, the text states `not established` and names the missing record.

---

## 1. The drain pass loop (`(*Loop).pass`)

One iteration of `(*Loop).pass` in [`tools/tillr-drain/drain/pass.go`](../../../tools/tillr-drain/drain/pass.go) executes five ordered phases. Phases 1 through 3 and the plan-application skeleton of Phase 4 run synchronously on the single loop goroutine; only child worker execution (`launchRun`), post-run settlement, and gate-plus-land judgment (`launchExamination`) run on background goroutines.

| Order | Phase in `(*Loop).pass` | Synchronous or async | What it runs | What it blocks |
| :--- | :--- | :--- | :--- | :--- |
| 1 | Pre-planning housekeeping | **Synchronous** | `gateRestart`, `migrateBuildRuns`, `prunePreservedWork`, `pruneRunRefs`, `intakeComments`, `confirmCommentReplies`, `flushCommentDeliveries`, `refreshPolicy`, draining `l.completions` (`takeCompletion` -> `recordPass`), `observeTrunk`, `refreshDocuments` (re-syncing tracked items when trunk moved), `reconcileLandingCalls`, `retireDesigners`, `maybeRolling`, `releaseUnlaunchedClaims`, seven hold-release sweeps (`releaseGeneratedTriage`, `resolveProvedFixers`, `releaseTrunkAcquittals`, `supersedeRetriedCandidates`, `releaseStaleBaseConflicts`, `settleUnansweredDesignRuns`, `settleMisreadDesignAnswers`), and `recordOrder` (which gates and lands `docs/tillr/queue-order.txt` inline when queue places changed). | Blocks planning (`passPlansGeneration`), every new dispatch, and every pending `settle` or `judge` claim for the duration of the housekeeping steps. |
| 2 | Snapshot planning (`passPlansGeneration`) | **Synchronous** | `refreshQuotas`, `withRunStartLock` (`gatherReapEvidence` process scan + `WriteTx` running `reapDeadLoopRuns` and `ReapExpiredLeases`), `releaseIdleHeldCandidates`, `recheckLanes` (scanning `/proc` for stray marked child processes), and one store `ReadTx` running `planDefinition`, `readPending`, and — **inside the per-decision loop** — `inputVersion(r, d)` for every planned item. | Blocks the entire pass. In `inputVersion` (`tools/tillr-drain/drain/pass.go`), `readPending(r)` and `ReadItemFaults(r)` are re-run from scratch for **each** decision in the queue (`O(items * events)`), making one `ReadTx` take **67.3 s to 99.0 s** at ~70–85 queued items before commit `a9a68ee9e` cached committed events in memory. |
| 3 | Comment readiness check | **Synchronous** | Loops over comment items calling `checkCommentDispatch`; if any comment batch closes, re-runs `passPlansGeneration` synchronously before proceeding. | Blocks Phase 4 plan application when comment items are present. |
| 4 | Sequential plan application (`for i := 0; i < len(plans); i++`) | **Sync claim + stage; async child** | In queue order, calls `applyPlanGeneration` -> `applyPlanRecorded` -> `writeGathered` (`tools/tillr-drain/drain/gathered.go`). Inside a SQLite `WriteTx`, re-verifies `currentDefinition`, `inputVersion(tx.Reader(), p.decision) == p.version`, `dispatchQuotaOpen`, `revParse(TrunkRef) == mainSHA`, and `currentRunInputs`. Each uncached git or file input aborts the `WriteTx` with `*drain.gatherMiss`, performs git/file I/O outside the lock, and restarts the `WriteTx` (including `inputVersion`). Then:<br>• **`ActionDispatch` / `ActionRetry`:** runs `stageDispatches` **synchronously** (`tillr-loop stage`: worktree reset/checkout, `recordStagedNode`, brief rendering; accumulated into `timing.StageMS`), then spawns **`launchRun` asynchronously**.<br>• **`ActionSettle` / `ActionJudge`:** spawns **`launchExamination` asynchronously**. | Each item's `writeGathered` retry loop and synchronous `stageDispatches` call blocks every lower-ranked item in `plans` — including fast `ActionSettle` and `ActionJudge` claims sitting behind dispatches. Worse, `launchExamination` for `ActionSettle` calls `l.settle(..., deferJudge=true)` (`tools/tillr-drain/drain/loop.go`), which **only** records `drain.candidate` and releases the examination without running the gate: every candidate must wait for a **second** full pass to plan and claim `ActionJudge` before `l.verify` (`gate` + `land`) can start. |
| 5 | Pass observation & wake wait | **Synchronous** | Deferred `observePass` writes `drain_passes` (`StageMS` recorded; `DispatchMS` initially `0` until completions arrive). If any plan applied or allocated a generation (`timing.Applied > 0 \|\| timing.TickID != ""`), returns immediately to start the next pass; otherwise blocks in `waitForTrigger` on `l.completions`, store/filesystem watches, or poll timer. | Nothing while active work applied; when waiting, wakes on the first completion or store change, which immediately enters Phase 1 of the next pass. |

---

## 2. End-to-end timelines of four recent quick items

The four items below were reconstructed on 2026-11-19 from `tillr item show --store /workspace/.tillr/state.db --item <id> --root /workspace`, `tillr view events --store /workspace/.tillr/state.db --since <seq>`, `tillr view passes --store /workspace/.tillr/state.db --last 60 --json` (and `--last 150 --json`), the per-run step timings (`steps.jsonl` and `gate.fast-verdict.json`), and the worker session JSONL transcripts recorded for each run.

### Summary comparison

| Item | Workflow | Files touched | End-to-end wall time | Active worker tool time (winning run) | Share of wall time spent in winning worker's tool loop |
| :--- | :--- | :--- | ---: | ---: | ---: |
| `item-request-cfae4676fc` | `single-worker@v1` | `.tillr.toml` (3 lines) | **9,380.8 s** (2h 36m 21s; **750.6 s** in `a2` before manual fault hold) | **11.2 s** (`a2`, 6 tool calls; `a1` spent 256.2 s across 30 calls) | **0.12%** (1.49% of `a2` pre-fault time) |
| `item-request-ad08eb1ca6` | `single-worker@v1` | `docs/design/tillr-diff-review.md` | **634.0 s** (10m 34s) | **31.6 s** (14 tool calls) | **4.98%** |
| `item-request-c0f1e4924c` | `single-worker@v1` | `docs/design/tillr-selfhosted-build.md` | **876.4 s** (14m 36s) | **39.7 s** (21 tool calls) | **4.53%** |
| `item-request-6c27d14b15` | `planned-build@v1` | 3 files in `yolo-packs/matt/` | **1,660.4 s** (27m 40s across `n1`, `n2`, `n3`) | **803.2 s** across 3 nodes (`n1` 374.1 s, `n2` 305.5 s, `n3` 123.6 s) | **48.38%** (`n2` edit+verify node alone: **18.40%**) |

---

### 2.1. `item-request-cfae4676fc` — "Reserve two lanes for interactively sourced items"

**Change:** set `express = 2` under `[lanes]` in `.tillr.toml` (`single-worker@v1`, `seat = build`). Total wall time from `item.add` (`04:06:21.000Z`) to `item.update` (`landed`, `06:42:41.811Z`): **9,380.81 s (2h 36m 20.8s)** across two worker runs (`a1` acquitted on a red trunk; `a2` hit `SQLITE_BUSY` inside `tillr-land` and sat fault-held for 86m 20s until manually cleared).

#### Timeline (`a1` and `a2`)

| Milestone | UTC timestamp (`2026-11-19`) | Delta (s) | Component | Evidence / notes |
| :--- | :--- | ---: | :--- | :--- |
| Filed (`item.add` seq `1294161`) | `04:06:21.000Z` | — | `tillr-item` | CLI request added to queue. |
| `a1` pass started (`gen-1959` `at`) | `04:06:24.148Z` | `+3.15` | `tillr-drain` | Wake after store write (`run.allocate` seq `1294174`). |
| `a1` claimed & stage started (`lane-3`) | `04:07:13.927Z` | `+49.78` | `tillr-drain` | Synchronous `passPlansGeneration` + `applyPlanRecorded` before staging. |
| `a1` staged (`tillr-loop stage` end) | `04:07:14.238Z` | `+0.31` | `tillr-loop` | Worktree reset + brief write (`steps.jsonl`). |
| `a1` dispatched (`tillr-run exec` start) | `04:07:14.583Z` | `+0.35` | `tillr-loop` / `tillr-run` | Subprocess spawn on `pi` (Carbon fallback). |
| `a1` worker session start | `04:07:15.873Z` | `+1.29` | `pi` | Session header timestamp. |
| `a1` first tool call (`read`) | `04:07:16.713Z` | `+0.84` | `pi` / model | First assistant tool call issued. |
| `a1` last tool result (30th call) | `04:11:32.893Z` | `+256.18` | `pi` / worker | 30 tool calls (see breakdown below). |
| `a1` run end (`run.finish` seq `1294221`) | `04:12:37.822Z` | `+64.93` | `pi` / `tillr-run` | Final assistant summary + usage extraction (`steps.jsonl` ends `04:12:37.905Z`). |
| `a1` settle pass `at` | `04:13:30.871Z` | `+53.05` | `tillr-drain` | Waiting for next pass to plan `ActionSettle`. |
| `a1` settled (`drain.lane-released` seq `1294226`) | `04:14:39.753Z` | `+68.88` | `tillr-drain` | Pass planning + `settle(..., deferJudge=true)` recording `drain.candidate` (`seq 1294225`). |
| `a1` judge pass `at` (`gen-1965`) | `04:15:37.798Z` | `+58.05` | `tillr-drain` | Second pass needed to plan `ActionJudge`. |
| `a1` gate start (`tillr-loop gate`) | `04:16:44.323Z` | `+66.53` | `tillr-drain` | Synchronous `passPlansGeneration` + `applyPlanRecorded` before `launchExamination`. |
| `a1` gate end (`rc=1`, `tillr-docs` red) | `04:17:58.240Z` | `+73.92` | `tillr-gate` / `fork-check.py` | `tests/tillr-roll.test.py` failed inside `tillr-docs` because trunk `f21f01caa` was already red. |
| `a1` trunk replay acquitted (`trunk-already-red`) | `04:21:49.419Z` | `+231.18` | `tillr-drain` | Trunk replay gate (`146.75 s`) + `order-record` (`83.22 s`) acquitted `a1` (`seq 1294290`) and blocked item on `fixer-main-f21f01caa68c`. |
| Unblocked (`item.update` seq `1294449`) | `04:34:13.278Z` | `+743.86` | `tillr-drain` | Fixer landed (`90f68aa7b`), releasing `trunk-already-red` acquittal hold. |
| `a2` pass started (`gen-1975` `at`) | `04:34:13.939Z` | `+0.66` | `tillr-drain` | Planned first in `04:34Z` pass (`run.allocate` seq `1294451`, `lane-1`, `pi-build` / Hydrogen). |
| `a2` stage started (`tillr-loop stage`) | `04:35:54.670Z` | `+100.73` | `tillr-drain` | **99.0 s** in synchronous `passPlansGeneration` + **1.7 s** in `applyPlanRecorded`. |
| `a2` staged | `04:35:55.050Z` | `+0.38` | `tillr-loop` | Worktree reset + brief write. |
| `a2` dispatched (`tillr-run exec` start) | `04:35:55.098Z` | `+0.05` | `tillr-loop` / `tillr-run` | Subprocess spawn on `pi-build`. |
| `a2` worker session start | `04:35:57.657Z` | `+2.56` | `pi` | `pi` process initialization. |
| `a2` first tool call (`read` + `bash`) | `04:35:58.555Z` | `+0.90` | `pi-build` / model | Parallel read of `.tillr.toml` + `git status`. |
| `a2` last tool result (6th call) | `04:36:09.714Z` | `+11.16` | `pi-build` / worker | **All reading, editing, TOML verification, and `git commit` completed in 11.16 s.** |
| `a2` run end (`run.finish` seq `1294526`) | `04:36:12.556Z` | `+2.84` | `pi` / `tillr-run` | Final report + exit (`steps.jsonl` ends `04:36:12.635Z`). |
| `a2` settle pass `at` (`gen-1977`) | `04:37:52.506Z` | `+99.95` | `tillr-drain` | Finished during `gen-1976` (`04:36:04Z`); waited for `gen-1976` to finish before `gen-1977` planned `ActionSettle`. |
| `a2` settled (`drain.candidate` seq `1294613`) | `04:39:33.917Z` | `+101.41` | `tillr-drain` | Blocked behind `gen-1977`'s synchronous planning and 3 foreground dispatches before `settle` committed. |
| `a2` judge pass `at` (`gen-1978`) | `04:40:08.870Z` | `+34.95` | `tillr-drain` | Next pass planning `ActionJudge`. |
| `a2` gate start (`tillr-loop gate`) | `04:42:12.583Z` | `+123.71` | `tillr-drain` | Blocked behind `gen-1978`'s synchronous `passPlansGeneration` and foreground dispatch loop. |
| `a2` gate end (`rc=0`) | `04:46:43.042Z` | `+270.46` | `tillr-gate` / `fork-check.py` | **41.8 s** in `tillr-gate checks --fast-tree` + **228.4 s** in `fork-check.py` (`tillr-docs` alone took **224.39 s** because `.tillr.toml` is in `TILLR_DOCS_READS`). |
| `a2` land attempt 1 (`rc=4`, `SQLITE_BUSY`) | `04:46:53.879Z` | `+10.84` | `tillr-land` / `tillr-drain` | `tillr-land` (`04:46:43.236Z`–`04:46:53.879Z`) timed out after `5.017 s` waiting for SQLite's write lock (`SQLITE_BUSY`) behind 13 back-to-back `tillr-drain` write transactions; `tillr-drain` faulted the item as `land-harness-error` at `04:46:55.114Z` (`seq 1294711`). |
| `land-harness-error` fault cleared | `06:13:15.392Z` | `+5,180.51` | operator / `tillr-drain` | `drain.item-fault-cleared` (`seq 1295479`) after 86m 20s idle in fault hold. |
| Candidate re-pinned to `lane-5` & staged | `06:42:39.603Z` | `+1,764.21` | `tillr-drain` / `tillr-loop` | Waited in queue behind higher-ranked work until `06:40:50.362Z` (`seq 1296127`), then staged candidate head onto `lane-5` (`0.22 s`). |
| `a2` land attempt 2 + verdict (`landed`) | `06:42:41.811Z` | `+2.21` | `tillr-land` / `tillr-item` | `tillr-land` ran `06:42:40.230Z`–`06:42:41.497Z` (**1.27 s**, `seq 1296142`), `verdict` + `item.update(landed)` at `06:42:41.811Z` (`seq 1296146`). |

#### Worker tool calls (`item-request-cfae4676fc`)

- **Attempt 2 (`run-item-request-cfae4676fc-a2`, `pi-build`, 6 tool calls, 11.16 s active):**
  - *Reading / exploring:* **2 calls** in 1 parallel turn (**2.35 s** turn+tool) — `read .tillr.toml` and `bash: git status`.
  - *Editing + commit:* **2 calls** (**4.83 s**) — `edit .tillr.toml` and `bash: git add .tillr.toml && git commit ...`.
  - *Verification the brief asked for:* **2 calls** (**3.95 s**) — `python3 -c 'import tomllib...'` to verify `.tillr.toml` parses with `lanes.express == 2`, plus recording `c1`, `c2` to `$TILLR_PROGRESS_FILE` and `git status --short`.
  - *Verification the brief did **not** ask for:* **0 calls (0.0 s)**.
- **Attempt 1 (`run-item-request-cfae4676fc-a1`, `pi`, 30 tool calls, 256.18 s active):**
  - *Reading / exploring:* **19 calls** (~**131.6 s**) — despite the brief stating *"No new test is needed for a repository settings file; check that `.tillr.toml` still parses"*, the worker explored `tools/tillr/settings/settings.go`, `tools/tillr-drain/drain/express_test.go`, `tools/fork-check.py`, and `tests/tillr-docs.test.py`.
  - *Editing + commit + progress:* **4 calls** (~**25.3 s**).
  - *Verification the brief asked for:* **2 calls** (~**27.9 s**) — TOML parse check and `git diff`/`git status`.
  - *Verification the brief did **not** ask for:* **5 calls** (~**83.0 s**) — `python3 tests/tillr-docs.test.py` (`50.1 s`), `python3 tests/tillr-roll.test.py`, `go test ./settings/...`, `tillr-drain --check`, and `python3 tools/fork-check.py --plan`.

---

### 2.2. `item-request-ad08eb1ca6` — "Correct tillr-diff-review against the tree"

**Change:** factual status update to `docs/design/tillr-diff-review.md` (`single-worker@v1`, `seat = design`, degraded to `pi-build` — see Section 4). Total wall time from `item.add` (`04:32:40.000Z`) to `item.update` (`landed`, `04:43:14.218Z`): **634.22 s (10m 34.2s)** in one run (`run-item-request-ad08eb1ca6-a1`).

| Milestone | UTC timestamp (`2026-11-19`) | Delta (s) | Component | Evidence / notes |
| :--- | :--- | ---: | :--- | :--- |
| Filed (`item.add` seq `1294368`) | `04:32:40.000Z` | — | `tillr-item` | Added in batch of doc-correction items. |
| Planned (`gen-1975` `at`, `lane-7`) | `04:34:13.939Z` | `+93.94` | `tillr-drain` | Waited while `gen-1974` (`04:32:44Z`) finished settling `6c27d14b15`. |
| Stage started (`tillr-loop stage`) | `04:36:00.074Z` | `+106.14` | `tillr-drain` | **99.0 s** `passPlansGeneration` + 3 preceding sequential dispatches in `gen-1975` (`run.allocate` seq `1294463`). |
| Staged | `04:36:00.463Z` | `+0.39` | `tillr-loop` | Worktree reset + brief write. |
| Dispatched (`tillr-run exec` start) | `04:36:00.518Z` | `+0.06` | `tillr-loop` / `tillr-run` | Spawned `pi-build` on `lane-7`. |
| Worker session start | `04:36:04.481Z` | `+3.96` | `pi` | `pi` startup under concurrent 4-lane launch. |
| First tool call (`read` + `bash` batch) | `04:36:05.510Z` | `+1.03` | `pi-build` / model | Parallel read of target doc, `git status`, `uvx vantage-check`. |
| Last tool result (14th call) | `04:36:37.097Z` | `+31.59` | `pi-build` / worker | **All reading, verification against code, edit, `vantage-check`, and `git commit` in 31.59 s.** |
| Run end (`run.finish` seq `1294532`) | `04:36:43.162Z` | `+6.07` | `pi` / `tillr-run` | Final summary + exit (`steps.jsonl` ends `04:36:43.235Z`). |
| Settle pass `at` (`gen-1977`) | `04:37:52.506Z` | `+69.34` | `tillr-drain` | Waited for `gen-1976` (`04:36:04Z`) to finish. |
| Settled (`drain.candidate` seq `1294614`) | `04:39:34.091Z` | `+101.59` | `tillr-drain` | Blocked behind `gen-1977` synchronous planning + dispatches; `deferJudge=true`. |
| Judge pass `at` (`gen-1978`) | `04:40:08.870Z` | `+34.78` | `tillr-drain` | Second pass required to claim `ActionJudge`. |
| Gate start (`tillr-loop gate`) | `04:42:12.507Z` | `+123.64` | `tillr-drain` | Blocked behind `gen-1978` synchronous planning + dispatches. |
| Gate end (`rc=0`) | `04:42:59.002Z` | `+46.50` | `tillr-gate` / `fork-check.py` | **41.5 s** in `tillr-gate checks --fast-tree` (`goBuildIdentity` 12.3 s, `structural` 6.21 s, `pack-sync` 4.49 s, `documents` 3.24 s, `format` 1.19 s) + **~5.0 s** in `fork-check.py` (`design-citations`). |
| Landed (`verdict` + `item.update` seq `1294684`) | `04:43:14.218Z` | `+15.22` | `tillr-land` / `tillr-item` | `tillr-land` ran `04:42:59.275Z`–`04:43:13.815Z` (**14.54 s**, delayed by SQLite lock contention with `tillr-drain`); `verdict` completed `04:43:14.218Z`. |

#### Worker tool calls (`item-request-ad08eb1ca6`, 14 calls, 31.59 s active)

- *Reading / exploring:* **10 calls** across 6 turns (**~21.7 s**) — read `docs/design/tillr-diff-review.md`, `config/tillr/workflows/planned-build@v1.json`, `tools/tillr-drain/drain/judge.go`, `tools/tillr-drain/drain/loop.go`, ran scoped `rg` checks, checked commit ancestry (`git merge-base --is-ancestor`), and inspected `git diff`.
- *Editing + commit + progress:* **2 calls** (**4.64 s**) — `edit docs/design/tillr-diff-review.md` and `git add` + `git commit` + `$TILLR_PROGRESS_FILE`.
- *Verification the brief asked for:* **2 calls** (**3.53 s** dedicated + 1 parallel baseline call) — `uvx vantage-check docs/design/tillr-diff-review.md`.
- *Verification the brief did **not** ask for:* **0 calls (0.0 s)**.

---

### 2.3. `item-request-c0f1e4924c` — "Correct tillr-selfhosted-build against the tree"

**Change:** factual status update to `docs/design/tillr-selfhosted-build.md` (`single-worker@v1`, `seat = design`, degraded to `pi-build`). Total wall time from `item.add` (`04:32:40.000Z`) to `item.update` (`landed`, `04:47:16.426Z`): **876.43 s (14m 36.4s)** in one run (`run-item-request-c0f1e4924c-a1`).

| Milestone | UTC timestamp (`2026-11-19`) | Delta (s) | Component | Evidence / notes |
| :--- | :--- | ---: | :--- | :--- |
| Filed (`item.add` seq `1294374`) | `04:32:40.000Z` | — | `tillr-item` | Added in same doc-correction batch as `ad08eb1ca6`. |
| First planned (`gen-1975` `at`) | `04:34:13.939Z` | `+93.94` | `tillr-drain` | Planned in `gen-1975`, **then discarded unapplied around `04:36:01Z`** because `tillr item place` re-ranked the queue at `04:35:59Z` and invalidated `inputVersion`! |
| Re-planned (`gen-1976` `at`, `lane-12`) | `04:36:04.697Z` | `+110.76` | `tillr-drain` | Re-planned in next pass (`run.allocate` seq `1294544`). |
| Stage started (`tillr-loop stage`) | `04:37:24.329Z` | `+79.63` | `tillr-drain` | **67.3 s** `passPlansGeneration` + 2 preceding sequential stages in `gen-1976`. |
| Staged | `04:37:24.833Z` | `+0.50` | `tillr-loop` | Worktree reset + brief write. |
| Dispatched (`tillr-run exec` start) | `04:37:24.895Z` | `+0.06` | `tillr-loop` / `tillr-run` | Spawned `pi-build` on `lane-12`. |
| Worker session start | `04:37:30.490Z` | `+5.60` | `pi` | `pi` startup under concurrent multi-lane load. |
| First tool call (`read` + `bash` batch) | `04:37:31.768Z` | `+1.28` | `pi-build` / model | Parallel read of target doc, `git status`, `uvx vantage-check`. |
| Last tool result (21st call) | `04:38:11.461Z` | `+39.69` | `pi-build` / worker | **21 tool calls completed in 39.69 s.** |
| Run end (`run.finish` seq `1294592`) | `04:38:16.025Z` | `+4.56` | `pi` / `tillr-run` | Final summary + exit (`steps.jsonl` ends `04:38:16.135Z`). |
| Settle pass `at` (`gen-1978`) | `04:40:08.870Z` | `+112.85` | `tillr-drain` | Finished during `gen-1977` (`04:37:52Z`); waited for `gen-1977` to finish before `gen-1978` planned `ActionSettle`. |
| Settled (`drain.candidate` seq `1294663`) | `04:42:15.827Z` | `+126.96` | `tillr-drain` | Blocked behind `gen-1978` synchronous planning + dispatches; `deferJudge=true`. |
| Judge pass `at` (`gen-1980`) | `04:44:25.818Z` | `+129.99` | `tillr-drain` | Missed `gen-1979` (`04:42:15Z` snapshot already taken); planned in `gen-1980`. |
| Gate start (`tillr-loop gate`) | `04:46:51.871Z` | `+146.05` | `tillr-drain` | Blocked behind `gen-1980` synchronous planning + dispatches. |
| Gate end (`rc=0`) | `04:47:14.284Z` | `+22.41` | `tillr-gate` / `fork-check.py` | **19.5 s** in `tillr-gate checks --fast-tree` (`goBuildIdentity` 5.9 s, `documents` 3.21 s, `pack-sync` 1.75 s, `structural` 1.01 s) + **~2.9 s** in `fork-check.py`. |
| Landed (`verdict` + `item.update` seq `1294726`) | `04:47:16.426Z` | `+2.14` | `tillr-land` / `tillr-item` | `tillr-land` ran `04:47:14.446Z`–`04:47:15.662Z` (**1.22 s**); `verdict` completed `04:47:16.426Z`. |

#### Worker tool calls (`item-request-c0f1e4924c`, 21 calls, 39.69 s active)

- *Reading / exploring:* **16 calls** across 5 parallel turns + `git diff` (**~19.7 s**) — read `docs/design/tillr-selfhosted-build.md`, `tools/tillr-view/siblings.go`, `tools/tillr-view/siblings_test.go`, `.justfile`, `tests/pack-sync.test.py`, `docs/tillr/roadmap.md`, and verified directory/command claims.
- *Editing + commit + progress:* **3 calls** (**14.76 s**) — `edit`, `$TILLR_PROGRESS_FILE`, and `git commit`.
- *Verification the brief asked for:* **2 calls** (**5.15 s** post-edit + 1 parallel baseline call) — `uvx vantage-check docs/design/tillr-selfhosted-build.md`.
- *Verification the brief did **not** ask for:* **0 calls (0.0 s)**.

---

### 2.4. `item-request-6c27d14b15` — "Switch the Copilot menu's Astra row to Astra 6.2"

**Change:** update model IDs and labels in `yolo-packs/matt/files/pi/agent/extensions/bedrock-context.ts`, its unit test `bedrock-context.test.mjs`, and `yolo-packs/matt/README.md`. Because no `--workflow` override was passed and the fence touched code outside docs/settings, `tillr-drain` routed it through the default 3-node **`planned-build@v1`** workflow (`plan` -> `work` -> `judge` evaluator node) plus post-workflow `settle` and `judge` (gate + land). Total wall time from `item.add` (`04:09:55.000Z`) to `item.update` (`landed`, `04:37:35.390Z`): **1,660.39 s (27m 40.4s)**.

| Milestone | UTC timestamp (`2026-11-19`) | Delta (s) | Component | Evidence / notes |
| :--- | :--- | ---: | :--- | :--- |
| Filed (`item.add` seq `1294190`) | `04:09:55.000Z` | — | `tillr-item` | CLI request added to queue. |
| **`n1` (`plan` stage)** pass `at` (`gen-1963`, `lane-9`) | `04:10:36.425Z` | `+41.43` | `tillr-drain` | Planned node 1 (`run.allocate` seq `1294215`, `pi` / Carbon). |
| `n1` staged (`04:11:31.771Z`–`04:11:32.641Z`) | `04:11:32.641Z` | `+56.22` | `tillr-drain` / `tillr-loop` | **55.35 s** pass planning + **0.87 s** `stage`. |
| `n1` dispatched -> session -> first tool | `04:11:36.600Z` | `+3.96` | `tillr-run` / `pi` | Exec `04:11:33.314Z`, session `04:11:35.332Z`, first tool `04:11:36.600Z`. |
| `n1` last tool result (36th call) | `04:17:50.743Z` | `+374.14` | `pi` / `n1` planner | Read target files in **123 s** (`04:11:36Z`–`04:13:39Z`, 16 calls), tried to stop for single-piece execution, **was blocked by `keep-going.ts` (`"The run is not finished: there is no commit"`)**, and spent **251 s (`04:13:57Z`–`04:17:50Z`, 20 calls)** reading `keep-going.ts`, `run-pi`, `workflow.go`, and `obstacle.go` to escape! |
| `n1` run end (`run.finish` seq `1294254`) | `04:18:12.358Z` | `+21.62` | `pi` / `tillr-run` | Planner exited cleanly after second stop attempt. |
| `n1` -> `n2` node advance (`seq 1294270`) | `04:20:18.259Z` | `+125.90` | `tillr-drain` | Pass wait (`62.2 s` to `04:19:14.545Z`) + `settle` advancing workflow node (`63.7 s`). |
| **`n2` (`work` stage)** pass `at` (`gen-1967`) | `04:20:20.168Z` | `+1.91` | `tillr-drain` | Planned node 2 (`run.allocate` seq `1294272`, `pi` / Carbon). |
| `n2` staged (`04:21:36.318Z`–`04:21:36.354Z`) | `04:21:36.354Z` | `+76.19` | `tillr-drain` / `tillr-loop` | **76.15 s** pass planning + **0.04 s** same-lane node stage. |
| `n2` dispatched -> session -> first tool | `04:21:39.447Z` | `+3.09` | `tillr-run` / `pi` | Exec `04:21:36.803Z`, session `04:21:38.382Z`, first tool `04:21:39.447Z`. |
| `n2` last tool result (33rd call) | `04:26:44.928Z` | `+305.48` | `pi` / `n2` worker | Re-read all 3 files from scratch, inspected `pi-ai` catalog, edited 3 files, ran tests/lint, committed. |
| `n2` run end (`run.finish` seq `1294331`) | `04:27:06.164Z` | `+21.24` | `pi` / `tillr-run` | Worker summary + exit. |
| `n2` -> `n3` node advance (`seq 1294346`) | `04:29:05.415Z` | `+119.25` | `tillr-drain` | Pass wait (`60.7 s` to `04:28:06.855Z`) + `settle` advancing to `judge` node (`58.6 s`). |
| **`n3` (`judge` stage)** pass `at` (`gen-1973`) | `04:29:07.540Z` | `+2.13` | `tillr-drain` | Planned node 3 (`run.allocate` seq `1294361`, `pi` / Carbon). |
| `n3` dispatched -> session -> first tool | `04:30:18.872Z` | `+71.33` | `tillr-drain` / `pi` | **67.49 s** pass planning (`exec` `04:30:15.025Z`), session `04:30:17.356Z`, first tool `04:30:18.872Z`. |
| `n3` last tool result (13th call) | `04:32:22.494Z` | `+123.62` | `pi` / `n3` evaluator | Re-read diff and files, re-ran `node --test` and `yolo pack lint`, wrote `eval.output` (`pass`). |
| `n3` run end (`run.finish` seq `1294367`) | `04:32:35.370Z` | `+12.88` | `pi` / `tillr-run` | Evaluator exit. |
| Final settle (`drain.candidate` seq `1294447`) | `04:34:12.487Z` | `+97.12` | `tillr-drain` | Pass `at` `04:32:44.036Z` (`+8.67 s`) + `settle(..., deferJudge=true)` (`88.45 s`). |
| Mechanical judge pass `at` (`gen-1976`) | `04:36:04.697Z` | `+112.21` | `tillr-drain` | Missed `gen-1975` (`04:34:13.939Z` snapshot ran concurrently with `04:34:12Z` settle completion); planned in `gen-1976`. |
| Gate (`04:37:14.454Z`–`04:37:30.281Z`, `rc=0`) | `04:37:30.281Z` | `+85.58` | `tillr-drain` / `tillr-gate` | **69.76 s** `gen-1976` pass planning before `gate` started + **15.83 s** gate (`tillr-gate` `6.2 s` incl. `2.4 s` `goBuildIdentity` + `fork-check.py` `9.3 s`). |
| Landed (`verdict` + `item.update` seq `1294579`) | `04:37:35.390Z` | `+5.11` | `tillr-land` / `tillr-item` | `tillr-land` ran `04:37:31.914Z`–`04:37:35.323Z` (**3.41 s**); `verdict` completed `04:37:35.390Z`. |

#### Worker tool calls (`item-request-6c27d14b15`, 82 calls across 3 nodes, 803.24 s active)

- **Node 1 (`n1`, `plan`, 36 calls, 374.14 s):** 16 calls (**123.0 s**) reading target files and pack layout; **20 calls (251.1 s)** debugging `keep-going.ts` after `pi`'s stop hook demanded a git commit from a read-only `plan` node.
- **Node 2 (`n2`, `work`, 33 calls, 305.48 s):** 21 calls (**~190.4 s**) reading target files (`n2` starts a fresh session with no file cache from `n1`) and inspecting `@earendil-works/pi-ai` model definitions; 4 calls (**~50.4 s**) editing the 3 files, updating `$TILLR_PROGRESS_FILE`, and committing; 3 calls (**~47.4 s**) running **requested** verification (`node --test ... bedrock-context.test.mjs` and `yolo pack lint yolo-packs/matt`); 5 calls (**~17.4 s**) running **unrequested** checks (`effort-thinking.test.mjs`, `rcm-deployment.test.py`, git author checks).
- **Node 3 (`n3`, `judge`, 13 calls, 123.62 s):** 9 calls reading brief/diff/files, 2 calls re-running `bedrock-context.test.mjs` and `yolo pack lint`, 2 calls writing and checking `eval.output`.

---

## 3. Pass-level phase durations (`tillr view passes --last 30`)

Because `tillr view passes --last 30` is a sliding window, both 30-pass windows relevant on 2026-11-19 were measured via `tillr view passes --store /workspace/.tillr/state.db --last 30 --json` (and `--last 150 --json` for the filing-time window):

- **Window A (measurement time, `05:20:09.316Z` to `07:10:18.390Z`):** 30 passes.
- **Window B (when `item-request-458ffc9523` was filed at `04:39:11Z`, `03:19:13.248Z` to `04:37:52.506Z`):** 30 passes spanning the four items in Section 2 (`gen-1973` at `04:29:07Z` through `gen-1977` at `04:37:52Z` near its end).

### Phase distributions (`min`, `p50`, `p90`, `max` in milliseconds)

Using nearest-rank percentiles over non-null values reported by `tillr view passes`:

| Window | Metric | Non-null passes (`>0` count) | `min` (ms) | `p50` (ms) | `p90` (ms) | `max` (ms) |
| :--- | :--- | :--- | ---: | ---: | ---: | ---: |
| **Window A** (`05:20Z`–`07:10Z`, **22/30** `phases: absent`) | `stage_ms` | `8 / 30` (`7 > 0`) | `0` | `10,602` | `64,332` | `64,332` |
| Window A | `dispatch_ms` | `8 / 30` (`3 > 0`) | `0` | `0` | `3,503,693` | `3,503,693` |
| Window A | `gate_ms` | `3 / 30` (`3 > 0`) | `13,165` | `20,133` | `69,592` | `69,592` |
| Window A | `land_ms` | `8 / 30` (`3 > 0`) | `0` | `0` | `2,577` | `2,577` |
| **Window B** (`03:19Z`–`04:37Z`, **15/30** `phases: absent`) | `stage_ms` | `15 / 30` (`15 > 0`) | `161` | `622` | `14,747` | `20,313` |
| Window B | `dispatch_ms` | `15 / 30` (`15 > 0`) | `2,610` | `685,297` | `2,904,867` | `3,043,813` |
| Window B | `gate_ms` | `3 / 30` (`3 > 0`) | `5,680` | `15,827` | `67,106` | `67,106` |
| Window B | `land_ms` | `15 / 30` (`3 > 0`) | `0` | `0` | `396` | `3,468` |

*(Note: in Window A with `n = 8`, linear-interpolated `p50` for `stage_ms` is `10,602 ms` midway between `4,999 ms` and `16,205 ms`, and nearest-rank `p50` is `4,999 ms`; all other medians above have odd `n` or equal neighbors.)*

### Four measurement blind spots in `drain_passes` / `tillr view passes`

1. **`phases: absent` hides every settle-only and judge-only pass (`22/30` in Window A, `15/30` in Window B).** In [`tools/tillr-view/passes.go`](../../../tools/tillr-view/passes.go), `readPassPhases` returns `nil` (`phases: absent`) unless `passDispatched(decisions)` finds at least one `dispatch` or `retry` decision in that pass. Passes that only run `settle` or `judge` — which is **most** `gate` and `land` activity because `settle(..., deferJudge=true)` pushes `ActionJudge` into later passes — have their recorded `gate_ms` and `land_ms` suppressed in `tillr view passes` unless that later pass also happened to dispatch a new worker.
2. **Synchronous pass planning (`passPlansGeneration`) and `writeGathered` time are completely unrecorded (`not established` in `drain_passes`).** `drain_passes` stores `at_ms` (captured *before* `recordOrder` and `passPlansGeneration`), `stage_ms`, `dispatch_ms`, `gate_ms`, and `land_ms`, with no `plan_ms` or `apply_ms` column (`tools/tillr-drain/drain/pass_observation.go`). The **50 s to 100 s** synchronous planning stalls measured in Section 2 are invisible in `tillr view passes` and can only be reconstructed today by diffing `pass.at` against `tillr-loop stage` start in `steps.jsonl`; for passes with zero dispatches, exact synchronous duration is `not established` and would require recording `plan_ms` and `apply_ms` on `drain_passes`.
3. **`dispatch_ms` sums asynchronous child worker run durations as each child finishes — it is not the duration of the pass's dispatch step.** In `launchRun` (`tools/tillr-drain/drain/pass.go`), the background goroutine sets `completion.timing.DispatchMS = time.Since(start).Milliseconds()` around `snapshot` + `runRestartable` (`tillr-run exec`) + `closeAsyncBracket`; when that child finishes, `takeCompletion` -> `recordAccumulatedPassTiming` (`tools/tillr-drain/drain/timing.go`) executes `UPDATE drain_passes SET dispatch_ms = dispatch_ms + ? WHERE tick_id = ?`. Runs still live when `tillr view passes` is queried contribute `0 ms`, and each completion increments the originating pass's `dispatch_ms` cumulatively (`3,503,693 ms` = 58.4 minutes summed across parallel workers in the `05:20:09Z` pass).
4. **High `stage_ms` in Window A (`56.5 s` at `05:20:09Z`, `64.3 s` at `06:58:02Z`) reflects multi-lane cold worktree setup and concurrent git contention**, whereas single-lane warm `stage` on a clean branch took **0.31 s to 0.50 s** in Section 2.

### Why the `04:29:07Z` and `04:34:13Z` passes showed `147 s` and `60 s` of `dispatch` time — and why each pass actually took ~68 s and ~108 s

Examining `gen-1973` (`2026-11-19T04:29:07.540Z`) and `gen-1975` (`2026-11-19T04:34:13.939Z`) across `tillr view passes`, `tillr view events`, `steps.jsonl`, and the loop's `loop:` log lines separates two distinct phenomena — **how `dispatch_ms` is accumulated** versus **why the synchronous pass itself stalled before launching children**:

- **Pass `2026-11-19T04:29:07.540Z` (`gen-1973`):**
  - *Why the loop stalled for **67.5 s** before launching anything:* `at` was captured at `04:29:07.540Z`, yet the two dispatches (`item-request-e1016f3a23` `a1-n3` on `lane-4` and `item-request-6c27d14b15` `a1-n3` on `lane-9`) were not launched until **`04:30:12.490Z`** and **`04:30:14.289Z`** (`stage_ms = 355 ms`). Those **64.9 s** were spent synchronously inside `passPlansGeneration` (`ReadTx` calling `inputVersion` -> `readPending` + `ReadItemFaults` once per queued item) and `writeGathered` before `launchRun` was reached.
  - *Why `dispatch` read **147.6 s** around `04:39Z` (and later grew to **1,183.3 s**):* at `04:32:35.370Z`, the first of the two children (`run-item-request-6c27d14b15-a1-n3`, started `04:30:14.289Z`) finished and closed its boundary bracket (`~147.6 s` total goroutine time in `launchRun`), logging `tick gen-1973 ... (staging 0.4s, dispatch 147.6s, land 0.0s)` at `04:32:42.283Z`. At `04:39Z`, the second child (`run-item-request-e1016f3a23-a1-n3-g1-d9a376fef041b95c`) was **still running** — when it finally finished at `04:47:10.097Z` (`1,017.4 s` run + bracket), `recordAccumulatedPassTiming` added its duration to `gen-1973`, raising `dispatch_ms` from `~147,600 ms` to **`1,183,294 ms`**.
- **Pass `2026-11-19T04:34:13.939Z` (`gen-1975`):**
  - *Why the loop stalled for **100.7 s** before launching anything:* `at` was captured at `04:34:13.939Z`, right after `fixer-main-f21f01caa68c` unlocked all trunk-acquitted items. Synchronous `passPlansGeneration` took **~99.0 s** (`04:34:13.939Z` to `04:35:53Z`) before the 4 dispatches (`cfae4676fc` `a2` at `04:35:54.670Z`, `item-landing-call-delivery-tests` `a7` at `04:35:56.526Z`, `a410a35903` `a1-n1` at `04:35:58.739Z`, `ad08eb1ca6` `a1` at `04:36:00.074Z`; `stage_ms = 1,998 ms`) were staged and spawned.
  - *Why `dispatch` read **60.5 s** around `04:39Z` (and later grew to **922.8 s**):* at `04:36:12.556Z`, `cfae4676fc` `a2` finished after **17.6 s**, logging `tick gen-1975 ... (staging 2.0s, dispatch 17.6s, land 0.0s)` at `04:37:49.996Z`; half a second later at `04:37:50.372Z`, `ad08eb1ca6` `a1` (`42.9 s` goroutine time, finished `04:36:43.162Z`) was drained from `l.completions`, adding `17.6 s + 42.9 s = 60.5 s` (`tick gen-1975 ... dispatch 60.5s`)! Meanwhile `a410a35903` `a1-n1` was still running until `04:50:20.023Z` (`861.2 s`), which later pushed `gen-1975`'s cumulative `dispatch_ms` to **`922,788 ms`**.

---

## 4. Parallelism and backend routing during the `04:32Z`–`04:47Z` batch

### 4.1. Why `seat "design"` degraded to `pi-build`

`tillr view passes` and `keeper` logs around `04:34Z`–`04:38Z` reported `seat "design": preferred backend "pi-designer" is unavailable; running on "pi-build"`. Store events establish the exact chain:

1. At `03:55:17.277Z`, designer run `run-item-doc-triage-2026-11-18-tillr-roadmap-designs-a3-n44-restart` exited with `failure_class: "infra"` (`run.finish` seq `1294138`).
2. At `03:55:17.463Z`, `tillr-drain` appended `drain.backend-outcome` (`seq 1294139`) with `"backend": "pi-designer"`, `"outcome": "infra"`, closing `pi-designer`'s availability window in `backendAvailability` (`tools/tillr-drain/drain/quota_window.go`).
3. At `04:34Z`, `config/tillr/backend-policy.copilot.json` had `"seats": {"design": {"backend": "pi-designer"}}` with **no** `"fallbacks"` array on the `design` seat, so `Policy.availableBackend` (`tools/tillr-drain/drain/policy.go`) fell straight through to policy `"default": "pi-build"` (Hydrogen) instead of `"pi"` (Carbon). Commit `518c27bfb` subsequently added `"fallbacks": ["pi"]` to `design` in `config/tillr/backend-policy.copilot.json`.
4. **Latency effect on quick doc requests:** for `ad08eb1ca6` and `c0f1e4924c`, running on `pi-build` (Hydrogen) **helped** rather than hurt — `pi-build` finished their 14-call and 21-call tool loops in **31.6 s** and **39.7 s** (and `cfae4676fc` `a2` in **11.2 s**), whereas `pi` (Carbon) took **256.2 s** on `cfae4676fc` `a1`.

### 4.2. Why 20+ ready items launched in batches of 4 and 6 despite free lanes

`.tillr.toml` configured `lanes.count = 20` (with 2 reserved for `express` once `cfae4676fc` landed; `lane-1` through `lane-18` observed active in `tillr view lanes`). Reconstructing active `run.allocate`..`drain.lane-released` intervals from `tillr view events` shows lanes were **not** saturated:

- **`gen-1975` (`04:34:13.939Z`):** **3–4 lanes busy** at pass start (`lane-4` running `e1016f3a23`, plus `item-seed-06` lanes; **15+ of 20 configured lanes free**) and **~40 items ready**. Yet `gen-1975` staged and launched only **4 items** (`cfae4676fc` on `lane-1` at `04:35:54Z`, `item-landing-call-delivery-tests` on `lane-3` at `04:35:56Z`, `a410a35903` on `lane-5` at `04:35:58Z`, `ad08eb1ca6` on `lane-7` at `04:36:00Z`) and discarded the remaining planned dispatches!
  - **Cause (`inputVersion` mismatch after queue re-ranking):** at `04:35:59.000Z` — while `gen-1975` was four items into its sequential `applyPlanGeneration` loop after spending **99.0 s** in `passPlansGeneration` — `tillr item place` (`item.update` seq `1294473`..`1294517`) re-ranked the queue. Because `inputVersion` (`tools/tillr-drain/drain/pass.go`) hashes `items.rank` and `items.updated_at`, `applyPlanRecorded` saw `version != p.version` for every remaining item in `plans` (`c0f1e4924c` onward) and skipped them as `stale`.
- **`gen-1976` (`04:36:04.697Z`):** **~7 lanes busy**, **13+ lanes free**, and **35+ items ready**. `gen-1976` spent **67.3 s** in `passPlansGeneration` (`04:36:04Z`–`04:37:12Z`), claimed 1 `judge` (`6c27d14b15`) and **6 dispatches** (`5199079f3d` on `lane-10` at `04:37:17Z`, `d1b55a09cc` on `lane-11` at `04:37:21Z`, `c0f1e4924c` on `lane-12` at `04:37:24Z`, `7f667ccffb` on `lane-13` at `04:37:28Z`, `22b3632ca2` on `lane-14` at `04:37:32Z`, and `de24789038` on `lane-15` whose `WriteTx` committed just before `04:37:35.3Z` and staged until `04:37:43Z`), and then aborted all remaining planned dispatches!
  - **Cause (`head != mainSHA` abort on concurrent landing):** `6c27d14b15`'s concurrent `judge` examination (launched first in `gen-1976`) finished its `15.8 s` gate and moved `main` at **`04:37:35.323Z`**. Inside `applyPlanRecorded` (`tools/tillr-drain/drain/pass.go`), every `ActionDispatch` checks `head, _ := l.revParse(ctx, l.cfg.Root, l.cfg.TrunkRef); if head != mainSHA { return false, nil, nil }`. The moment `6c27d14b15` landed on `main`, every remaining unapplied dispatch in `gen-1976` failed `head != mainSHA` and was dropped until `gen-1977` (`04:37:52.506Z`).

---

## 5. Ranked avoidable delays and one-piece fixes

Items marked **[Owner trade-off]** below alter a workflow, verification, or ordering guarantee that only the owner can decide; items without that marker preserve current semantics while removing pure mechanical overhead.

### Rank 1 — Two-pass post-run settlement (`deferJudge=true`) and synchronous pass-loop blocking

- **Measured cost:** **200 s to 405 s** after `run.finish` before `gate` even started (`ad08eb1ca6`: **329.3 s** from `run.finish` `04:36:43Z` to `gate` start `04:42:12Z`; `c0f1e4924c`: **515.8 s** from `04:38:16Z` to `04:46:51Z`; `cfae4676fc` `a2`: **360.0 s** from `04:36:12Z` to `04:42:12Z`), plus **50 s to 100 s** before initial dispatch.
- **Root causes in code:**
  1. `launchExamination` for `ActionSettle` (`tools/tillr-drain/drain/pass.go`, `loop.go`) hardcodes `l.settle(..., deferJudge=true)`, which writes `drain.candidate`, exits, and forces the candidate to wait for a **second** pass to plan and claim `ActionJudge` before `l.verify` runs.
  2. `inputVersion(r, d)` (`tools/tillr-drain/drain/pass.go`) calls `readPending(r)` and `ReadItemFaults(r)` from scratch **once per planned item** inside `passPlansGeneration` and again on every `writeGathered` retry. (Commit `a9a68ee9e` cached committed store events in memory so event scanning no longer hits SQLite per call, but `readPending` and `ReadItemFaults` still replay across the cached slice `N` times per pass.)
  3. `stageDispatches` runs synchronously inside the foreground `for _, p := range plans` loop, delaying `launchExamination` claims queued behind dispatches.
- **One-piece fixes:**
  - **Piece 1a (pure optimization — `tools/tillr-drain`):** compute `pendingMap` and `faultMap` once per `ReadTx` in `passPlansGeneration` and pass pre-read maps into `inputVersion` (with a unit/benchmark test in `tools/tillr-drain/drain/` asserting single-pass evaluation).
  - **Piece 1b (`tools/tillr-drain`):** when `settle` produces a landable candidate on its lane and no higher-priority waiter requires preempting that lane, transition directly from `settle` into `verify` (`gate` + `land`) inside the same examination instead of returning through `deferJudge=true` (pinned by an integration test in `tools/tillr-drain/drain/` asserting 1-pass settle+judge completion). **[Owner trade-off]:** today `deferJudge=true` lets the planner interpose a newly arrived higher-priority hold or express preemption between `settle` and `judge`; chaining `settle -> verify` trades that interposition point for saving an entire pass cycle (~100 s–250 s).

### Rank 2 — Broad and uncached gate work on narrow edits

- **Measured cost:** **270.5 s** on `.tillr.toml` (`cfae4676fc` `a2`), **46.5 s** on `tillr-diff-review.md` (`ad08eb1ca6`), and **22.4 s** on `tillr-selfhosted-build.md` (`c0f1e4924c`) — up to **24x** longer than the edit itself.
- **Root causes in code:**
  1. `.tillr.toml` is in `TILLR_DOCS_READS` (`tools/fork-check.py`), so editing `[lanes] express = 2` triggers `tests/tillr-docs.test.py`, which runs four Python regression suites (`tillr-build`, `tillr-scratch`, `tillr-keep`, `tillr-roll`) **and** compiles all eight Go binaries (`go build` across `MODULES`) and runs `--help` on every documented CLI face (**224.39 s** under load). `.tillr.toml` was added there on 2026-11-18 only because `tests/tillr-roll.test.py` reads `.tillr.toml`.
  2. `tools/gate/change-gate.py` invokes `tillr-gate checks --fast-tree` on **every** candidate before `fork-check.py`. Even on a 1-file Markdown edit where zero Go files changed, `RunWith` (`tools/tillr-gate/run.go`) calls `goBuildIdentity` (`tools/tillr-gate/goenvmemory.go`), spawning `go env` and `go mod edit -json` across every Go module in the tree (**5.9 s to 12.3 s**), and runs all 12 fast-tree checks (`structural`, `pack-sync`, `documents`, `format`, etc.) serially (**19.5 s to 41.5 s** total).
- **One-piece fixes:**
  - **Piece 2a (pure optimization — `tools/fork-check.py` & `tests/`):** give `tests/tillr-roll.test.py` (and `tillr-keep.test.py`) its own lightweight step watched by `.tillr.toml` / `.justfile` / `tools/tillr-roll/`, removing `.tillr.toml` from `TILLR_DOCS_READS` so `.tillr.toml` edits run the ~2 s roll suite instead of rebuilding 8 Go binaries in `tillr-docs.test.py` (verified in `tests/fork-check.test.py`).
  - **Piece 2b (`tools/tillr-gate`):** cache `goModuleIdentity` by `(go_mod_blob_sha, go_sum_blob_sha)` plus once-per-process `go env` instead of spawning `go mod edit -json` per module on every gate (`tools/tillr-gate/goenvmemory_test.go`), and parallelize independent fast-tree checks in `RunWith` (`tools/tillr-gate/run_test.go`). **[Owner trade-off]:** skipping `--fast-tree` checks whose declared `reads` do not intersect `base..head` on docs-only changes would cut gate time to <5 s, but changes the current whole-tree `--fast-tree` guarantee in `change-gate.py`.

### Rank 3 — Default 3-node `planned-build@v1` workflow + read-only `keep-going.ts` commit trap on small code requests

- **Measured cost:** **~1,100 s of the 1,660.4 s** on `item-request-6c27d14b15`: **251.1 s** wasted inside `n1` fighting `keep-going.ts`, plus **~340 s** in two extra pass/settle node handoffs (`n1 -> n2` `127.8 s`, `n2 -> n3` `121.4 s`, `n3` planning `71.3 s`), plus **~314 s** of duplicated file reading and test running across `n1`, `n2`, and `n3`.
- **Root causes in code:**
  1. `tools/tillr-run/backends/pi/keep-going.ts` refused `n1`'s stop with `"The run is not finished: there is no commit"` on a read-only `plan` stage that legitimately produces zero commits when choosing single-piece execution.
  2. Any CLI request touching code defaults to `planned-build@v1` (`plan` -> `work` -> `judge`) unless `--workflow single-worker@v1` is explicitly passed.
- **One-piece fixes:**
  - **Piece 3a (pure bug fix — `tools/tillr-run`):** exempt read-only runs (`TILLR_READ_ONLY_GROUP` / read-only workflow nodes) from the missing-commit refusal in `tools/tillr-run/backends/pi/keep-going.ts` (tested in `tools/tillr-run/run/pi_keep_going_test.go`).
  - **Piece 3b (`tools/tillr-item` / `tools/tillr-drain`):** workflow selection for small/explicit requests — e.g., allowing a `--quick` flag or fenced-file threshold to select `single-worker@v1` instead of `planned-build@v1`. **[Owner trade-off]:** skipping `plan` and the LLM `judge` node (`n3`) saves **10–15 minutes** per small code request, at the cost of omitting pre-gate LLM diff review on those items.

### Rank 4 — Mid-pass dispatch invalidation from `rank`/`updated_at` in `inputVersion` and `head != mainSHA`

- **Measured cost:** **80 s to 190 s** per invalidated batch (`c0f1e4924c` lost **190.4 s** across `gen-1975` -> `gen-1976`), throttling effective launch concurrency to 4–6 items per pass even with 13–15+ free lanes.
- **Root causes in code:**
  1. `inputVersion` (`tools/tillr-drain/drain/pass.go`) includes `items.rank` and `items.updated_at`, so `tillr item place` re-ranking the queue mid-pass invalidates every remaining planned dispatch even when the item's state, fence, workflow, and lane are unchanged.
  2. `applyPlanRecorded` aborts `ActionDispatch` whenever `revParse(TrunkRef) != mainSHA` — so a fast sibling landing on `main` mid-pass cancels every remaining dispatch in `plans`, forcing another full planning pass.
- **One-piece fixes:**
  - **Piece 4a (`tools/tillr-drain`):** remove `rank` (and rank-only `updated_at` bumps) from `inputVersion` in `tools/tillr-drain/drain/pass.go` so queue placement edits reorder the *next* pass without aborting non-conflicting dispatches already planned for free lanes (tested in `tools/tillr-drain/drain/`). **[Owner trade-off]:** if the owner places a newly urgent item at rank 1 while all lanes are nearly full, honouring in-flight pass plans lets the last free lane go to the old plan rather than immediately aborting to re-plan rank 1.
  - **Piece 4b (pure optimization — `tools/tillr-drain`):** refresh `mainSHA` in `applyPlanRecorded` when `TrunkRef` advances cleanly without touching the planned item's `currentRunInputs` or fence, Staging onto the new `head` instead of dropping the rest of the pass (tested in `tools/tillr-drain/drain/`).

### Rank 5 — SQLite writer starvation between `tillr-drain` and `tillr-land` (`SQLITE_BUSY` -> `land-harness-error`)

- **Measured cost:** **6,947 s (1h 55m 47s)** added to `item-request-cfae4676fc` (`10.6 s` blocked in `tillr-land` + `5,180.5 s` held as `land-harness-error` awaiting manual clearance + `1,764 s` queue/restage wait), plus **14.5 s** landing tail on `ad08eb1ca6`.
- **Root causes in code:**
  1. Store handles open with `DefaultBusyTimeout = 5 * time.Second` (`tools/tillr/store/options.go`). During `04:46:43Z`–`04:46:54Z`, `tillr-drain`'s in-process goroutines executed 13 consecutive `WriteTx` calls (`writeGathered`, `appendJudgment`, `releaseExamination`), starving external subprocess `tillr-land` past `5,000 ms` inside `claimLanding` (`tools/tillr-land/land/landing.go`).
  2. When `tillr-land` exited `rc=4` on `SQLITE_BUSY`, `tillr-drain` recorded `land-harness-error` — a permanent item fault (`drain.item-fault`) that halts the item until an operator clears it, violating [Principle 1](../../design/landing-principles.md#1-a-refusal-must-be-a-verdict) (machinery contention must not convict or permanently park a candidate).
- **One-piece fix (`tools/tillr/store`, `tools/tillr-land`, `tools/tillr-drain`):** raise `WithBusyTimeout` (or retry transient `SQLITE_BUSY` with jittered backoff before acquiring `land.lock` in `claimLanding`) **and** classify `SQLITE_BUSY` lock timeouts in `tillr-drain`'s landing error handler as a transient mechanical retry rather than a permanent `land-harness-error` item fault (tested in `tools/tillr-land/contention_test.go` and `tools/tillr-drain/drain/`).
