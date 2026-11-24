---
title: "Restarting tillr in place, pausing sessions, and post-landing roll and push"
status: accepted
stage: CURRENT
verified: 2026-11-20
verified_commit: 9160c9c48f70286c203076abdc0c3a0d1d89ae48
covers:
  - .tillr.toml
  - tools/tillr/settings/settings.go
  - tools/tillr-drain/restart/restart.go
  - tools/tillr-drain/drain/restart.go
  - tools/tillr-drain/drain/stop.go
  - tools/tillr-drain/drain/schema_roll.go
  - tools/tillr-drain/alarms/store_roll.go
  - tools/tillr/store/open.go
  - tools/tillr-drain/drain/restart_work.go
  - tools/tillr-drain/drain/restart_examination.go
  - tools/tillr-drain/drain/restart_judge.go
  - tools/tillr-drain/drain/restart_rolling.go
  - tools/tillr-drain/drain/steps.go
  - tools/tillr-drain/drain/loop.go
  - tools/tillr-drain/drain/settle_judge.go
  - tools/tillr-drain/drain/build_restart.go
  - tools/tillr-drain/drain/build_readers.go
  - tools/tillr-drain/drain/parallel_restart.go
  - tools/tillr-drain/drain/post_landing.go
  - tools/tillr-drain/drain/extension_roll.go
  - tools/tillr-drain/drain/push_refused.go
  - tools/tillr-run/run/pause.go
  - tools/tillr-run/backends/pi/stop-at-gap
  - tools/tillr-view/restart.go
  - tools/tillr-view/build.go
  - tools/tillr-roll/tillr-roll.py
  - tools/tillr-roll/tillr-keep
related:
  - ./tillr-drain-successor.md
  - ./tillr-build-commit.md
  - ./observatory-redesign.md
  - ../design/tillr-selfhosted-build.md
  - ../design/tillr-refused-push.md
  - ../design/gate-rework.md
tags: [tillr, drain, view, run, restart, settings, reference]
summary: "How tillr replaces `tillr-drain loop` and `tillr-view serve` in place through durable restart requests, pauses and continues agent sessions without burning attempts, migrates active runs onto new builds, reads `.tillr.toml`, and rolls and pushes after landings."
---

# Restarting tillr in place, pausing sessions, and post-landing roll and push

> **Delivered copy.** This is `docs/reference/tillr-restart.md` from this repository at commit `4b6fdf35e8` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-restart.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Verified 2026-11-20 against `9160c9c48f70286c203076abdc0c3a0d1d89ae48`.
UNMEASURED: verification rests on the Go and Python process-fixture suites; no
live restart latency distribution on a ten-lane production drain is recorded
here.

Tillr upgrades its long-lived processes — `tillr-drain loop` and `tillr-view serve`
— without killing active agent runs or dropping HTTP requests. Callers file a
durable restart request in SQLite rather than sending a signal; each process
validates the replacement binary on disk, replaces itself via `syscall.Exec` at
a safe boundary while preserving its PID and supervisor locks, and lets new work
start on the new build immediately while older gap-capable sessions pause
between tool calls and resume in place on the same attempt. Per-repository
settings in [`.tillr.toml`](https://github.com/mschulkind-oss/tillr/blob/main/.tillr.toml) configure automatic post-landing
binary rolls and green-pointer or trunk-tip pushes.

| Component | Lives in |
| :--- | :--- |
| Durable restart request, answer, build-started events, digest comparison, and launcher resolution | [`tools/tillr-drain/restart/restart.go`](../../tillr-drain/restart/restart.go) (`File`, `ReadState`, `ReadAnswers`, `AwaitAnswer`, `Restarter`, `PublishBuild`, `PublishBuildTx`, `ReadBuild`, `LauncherCandidate`, `FileDigest`) |
| Drain loop restart gate, mechanical lock, examination handoff, `syscall.Exec`, and inherited-child reaping | [`tools/tillr-drain/drain/restart.go`](../../tillr-drain/drain/restart.go) (`adoptRestart`, `gateRestart`, `inheritedChildren`, `reapInherited`), [`restart_work.go`](../../tillr-drain/drain/restart_work.go) (`lockRestartMechanical`, `lockRestartSettlement`, `restartDefersToSettlement`, `restartWork`), [`restart_examination.go`](../../tillr-drain/drain/restart_examination.go) (`releaseRestartExaminations`, `reclaimDetachedGateExaminations`), [`restart_judge.go`](../../tillr-drain/drain/restart_judge.go), [`restart_rolling.go`](../../tillr-drain/drain/restart_rolling.go), [`loop.go`](../../tillr-drain/drain/loop.go) (`verify`), [`settle_judge.go`](../../tillr-drain/drain/settle_judge.go) (`claimSettledCandidateJudge`, `runCandidateExamination`), [`tools/tillr-drain/main.go`](../../tillr-drain/main.go) (`restartFace`, `loopFace --check`) |
| Detached run and landing-gate sessions, file-backed streams, session pause, `SIGUSR1` gap stop, and continuation | [`tools/tillr-drain/drain/steps.go`](../../tillr-drain/drain/steps.go) (`ProcessSteps.Run`, `ProcessSteps.Gate`), [`tools/tillr-run/run/pause.go`](../../tillr-run/run/pause.go) (`ReadPauseKind`, `RequestPause`, `prepareContinuationSession`), [`tools/tillr-run/run/exec.go`](../../tillr-run/run/exec.go), [`tools/tillr-run/backends/pi/stop-at-gap`](../../tillr-run/backends/pi/stop-at-gap) |
| Post-restart build migration for workers and parallel read-only groups | [`tools/tillr-drain/drain/build_restart.go`](../../tillr-drain/drain/build_restart.go) (`migrateBuildRuns`), [`build_readers.go`](../../tillr-drain/drain/build_readers.go) (`claimBuildReaders`), [`parallel_restart.go`](../../tillr-drain/drain/parallel_restart.go) (`restartParallel`) |
| Observatory server graceful HTTP drain, `syscall.Exec`, listener recovery, and build/restart header projection | [`tools/tillr-view/restart.go`](../../tillr-view/restart.go) (`restartFace`, `serving`), [`build.go`](../../tillr-view/build.go) (`buildInfo`, `freshness`), [`serve.go`](../../tillr-view/serve.go), [`ObservatoryHeaderBar.tsx`](../../tillr-view/web/src/components/ObservatoryHeaderBar.tsx) |
| Per-repository settings loader (`.tillr.toml`) | [`tools/tillr/settings/settings.go`](../../tillr/settings/settings.go) (`Read`, `ReadWithoutLanding`, `Settings`) |
| Post-landing roll, pi/extension fork rolls, autopush, and linear replay on refused pushes | [`tools/tillr-drain/drain/post_landing.go`](../../tillr-drain/drain/post_landing.go) (`landingAction`, `afterLandingCandidate`, `afterGreen`, `pushTrunk`, `pushGreen`), [`extension_roll.go`](../../tillr-drain/drain/extension_roll.go), [`push_refused.go`](../../tillr-drain/drain/push_refused.go) (`healRefusedPush`) |
| Repository roll builder/installer and loop keeper | [`tools/tillr-roll/tillr-roll.py`](../../tillr-roll/tillr-roll.py), [`tools/tillr-roll/tillr-keep`](../../tillr-roll/tillr-keep), [`.justfile`](https://github.com/mschulkind-oss/tillr/blob/main/.justfile) (`tillr-roll`, `tillr-build`, `tillr-keep`, `tillr-restart`) |

---

## Restart requests and answers

Neither `tillr-drain loop` nor `tillr-view serve` watches the filesystem for
rebuilt binaries ([`D-RT1`](#why-its-this-way), [`D-RT2`](#why-its-this-way)).
Instead, [`tools/tillr-drain/restart/restart.go`](../../tillr-drain/restart/restart.go)
coordinates restarts through three append-only store event kinds without adding
dedicated SQLite tables:

- **`drain.restart-request`** (`RequestKind`, appended by `File`): records a
  request addressed to role `"drain"` or `"view"` with generated ID
  `restart-<role>-<unix_ms>-<4B_hex>` and `at`.
- **`drain.restart-answer`** (`AnswerKind`, appended by `Restarter.Record`):
  records the terminal disposition of one request ID — `"restarted"`,
  `"already-current"`, `"refused"`, or `"superseded"` — along with `from_build`,
  `to_build`, `to_digest`, `detail`, and `at`. When multiple unanswered requests
  exist for the same role, `ReadState` identifies the newest in event sequence
  order alongside the older ones; `Restarter.Settle` answers every older request
  with `"superseded"` (`detail` naming the winning request ID) before acting on
  the newest.
- **`drain.build-started`** (`BuildKind`, written by `PublishBuild` and
  `PublishBuildTx` and read by `ReadBuild`): records `BuildFact` (`role`,
  `revision`, `time`, `modified`, `digest`, `executable`, `pid`, `at`) whenever
  a loop or observatory server starts or completes a restart. For a drain that
  replaces its running program to fulfill a restart request, `restart_request`
  holds the request ID; for a drain starting after `tillr-drain stop` (which
  appends `drain.stop-requested` when flagging live local sessions),
  `stop_request` holds that stop ID (or `"drain_sessions"` for a pre-event
  session stop); for the first drain start in a store with no prior drain build
  or session, `initial` is `true`. Other starts omit those fields.

### Resolving and comparing the candidate binary

Before replacing itself, `Restarter` resolves the executable path a fresh launch
would run (`LauncherCandidate` in `restart/restart.go`):

1. **Pack launcher resolution:** When `TILLR_LAUNCHER` (`LauncherEnv`) is set in
   the environment (exported by
   [`yolo-packs/tillr/programs/tillr-drain/run`](https://github.com/mschulkind-oss/tillr/blob/main/yolo-packs/tillr/programs/tillr-drain/run)
   and its seven sibling launchers), `LauncherCandidate` runs `$TILLR_LAUNCHER`
   with `TILLR_LAUNCHER_RESOLVE=1` (`LauncherResolveEnv`). The launcher compiles
   any updated pack source if needed, prints the selected binary path without
   `exec`ing it, and returns.
2. **Direct binary resolution:** Otherwise, `LauncherCandidate` resolves its
   fallback executable (stripping any Linux `" (deleted)"` suffix when an
   installer atomically replaced the file) and resolves symlinks with
   `filepath.EvalSymlinks`.

`FileDigest` hashes both the running image and the resolved candidate path with
SHA-256. If the two digests are equal, `Restarter.Settle` records
`"already-current"` and returns `replace = false` without `exec`ing.

When the digests differ and preflight validation (`Restarter.Check`) succeeds,
`Restarter.Replace` injects two environment variables and calls `syscall.Exec`:

- `TILLR_RESTART_REQUEST=<request_id>` (`RequestEnv`)
- `TILLR_RESTART_FROM=<old_build_line>` (`FromEnv`)

On startup, the new image calls `Restarter.Adopt` (via `adoptRestart` in
`tillr-drain` and `serving.watch` in `tillr-view`), which reads those variables,
unsets them so child processes never inherit them, and records the `"restarted"`
answer naming both `from_build` and `to_build` alongside `PublishBuild`. If a
request was filed while no process for that role was running, the next process
to start sees it as pending on its first poll and `Settle` answers
`"already-current"` because its running digest already matches disk.

---

## Replacing the loop and observatory in place

### `tillr-drain restart` and `tillr-drain loop --check`

`tillr-drain restart` (`--root <repo>` or `--store <path>`, optional `--wait`)
files a `"drain"` restart request and prints its ID; with `--wait`, it polls
`AwaitAnswer` every `100ms` until answered (or interrupted by `SIGINT`/`SIGTERM`),
prints the formatted answer, and exits `1` if the answer is `"refused"`
(`restartFace` in [`tools/tillr-drain/main.go`](../../tillr-drain/main.go)).

Before giving up its process image, the running loop runs the candidate binary
with `--check` appended to its original CLI flags (`RestartConfig.Check`,
[`D-RT5`](#why-its-this-way)). `tillr-drain loop --check` validates CLI flags,
[`.tillr.toml`](https://github.com/mschulkind-oss/tillr/blob/main/.tillr.toml), the backend policy, the store's schema
version, store hooks, and configured step-program executables (`steps.CheckPrograms`),
then exits `0` without starting a pass. The store check is read-only at the
current version. For a lower version it tries the migration in a transaction
and rolls it back; a newer or unreadable schema is refused. Any non-zero exit
records `"refused"` on the restart request with the candidate's diagnostic
output and leaves the current loop running.

### Safe replacement boundaries inside `tillr-drain loop`

The loop replaces itself immediately rather than waiting for running agent
sessions to finish ([`D-RT3`](#why-its-this-way)), while protecting fast
in-process state transitions from mid-step interruption:

1. **Background roll and push guard:** `gateRestart` returns without acting
   while any post-landing `landingAction` (`rollAction`, `piRollAction`,
   `extRollAction`, or `pushAction`) is busy, both before `Settle` and again
   after acquiring `restartMechanical`, so `syscall.Exec` never discards a
   queued follow-up roll or push.
2. **Mechanical vs. unbounded work (`restartMechanical`):** Short mechanical
   steps (pre-gate claim/preparation, post-gate store judgment, `Steps.Land`, and
   non-detached in-process test `Steps`) hold read locks via
   `lockRestartSettlement` or `lockRestartMechanical`
   ([`restart_work.go`](../../tillr-drain/drain/restart_work.go)).
   Long-running model sessions, whole-tree checks, and detached landing-gate
   waits wrapped in `restartWork` release their mechanical read lock while
   running in a goroutine and poll `gateRestart` every `50ms`. Under
   [`D-GR43`](../design/gate-rework.md#decision-ledger) and
   [`D-GR17`](../design/gate-rework.md#decision-ledger), `ProcessSteps.Gate`
   ([`steps.go`](../../tillr-drain/drain/steps.go)) runs the gate program
   in a detached, guarded child session (`--tillr-gate-supervisor`) that writes
   durable live-face (`gate.face.json`) and completion (`gate.done.json`)
   metadata alongside `gate.verdict.json`, `gate.spans.jsonl`, and
   `gate.timing.jsonl` in `RunDir(s.Root, candidate.RunID)`. `verify`
   ([`loop.go`](../../tillr-drain/drain/loop.go)) releases
   `restartMechanical` via `restartWork` while awaiting the detached gate, and
   `claimSettledCandidateJudge`
   ([`settle_judge.go`](../../tillr-drain/drain/settle_judge.go)) and
   `lockRestartSettlement` (`buildRestartPending`) still admit sibling
   fast-gate candidates when `restartDefersToSettlement()` already holds. Once
   `gateRestart` passes `Restarter.Settle` (including `--check`), it acquires
   the exclusive write lock on `restartMechanical`, waiting for in-flight
   mechanical steps to finish while blocking new ones.
3. **Recheck on supersession or binary change:** Inside `Restarter.Replace`
   (called under `restartMechanical`), the restarter checks `Owing` (returning
   `ErrSuperseded` if a newer request arrived) and re-checks the candidate's
   digest and `Check` if the binary on disk changed since `Settle`.
4. **Explicit examination release:** Because `syscall.Exec` preserves the loop's
   PID and `/proc/<pid>/stat` start time, the successor image cannot detect from
   `/proc` that the prior examiner vanished. `releaseRestartExaminations`
   ([`restart_examination.go`](../../tillr-drain/drain/restart_examination.go))
   explicitly appends `examinationReleasedKind`, clears `examinationOwnerKind`,
   and idles lanes whose examined run has no live `tillr-run` face before
   `syscall.Exec` — except when a candidate's detached gate is live (or already
   wrote `gate.done.json`), in which case it keeps the lane leased
   (`leased_until = NULL`) so the successor loop can reclaim the examination in
   `reclaimDetachedGateExaminations` to await or consume the detached gate
   result and land without re-running the gate.
5. **Close-on-exec supervision for rolling and heavy checks:** Both after-merge
   heavy proofs (`superviseHeavy`) and rolling checks (`rollingOwnership` in
   [`restart_rolling.go`](../../tillr-drain/drain/restart_rolling.go))
   hold an `os.Pipe` whose write end is close-on-exec alongside `heavy.lock`. If
   `syscall.Exec` fails, the pipe stays open and the current loop keeps
   ownership; when `syscall.Exec` succeeds, the kernel closes the pipe, waking
   the child supervisor cleanly without relying on a dead image's goroutines.
6. **Reaping inherited child processes:** Detached `tillr-run exec` and
   `--tillr-gate-supervisor` processes from the pre-`exec` image remain direct
   OS children of the loop PID. On startup after an adopted handoff,
   `adoptRestart` snapshots existing child PIDs from `/proc`
   (`inheritedChildren`) before starting any new subprocess and spawns
   `reapInherited`, which calls `syscall.Wait4(pid, nil, WNOHANG, nil)` every
   `200ms` so finished pre-handoff `tillr-run` processes and pre-`exec` detached
   gate supervisor children (once they write `gate.done.json` and exit) never
   linger as zombies.

### `tillr-view restart` and `tillr-view serve`

`tillr-view restart` (`--store <path>` or `--root <repo>`, optional `--wait`)
files a `"view"` restart request and, with `--wait`, polls `AwaitAnswer` every
`250ms` (`restartPoll` in [`tools/tillr-view/restart.go`](../../tillr-view/restart.go)).
Inside `tillr-view serve`, `serving.watch` polls `Restarter.Pending` every
`250ms`:

- Active finite HTTP requests are counted (`serving.track`), excluding
  long-lived WebSocket upgrades.
- `Restarter.Settle` validates the candidate binary by running
  `<candidate> <args...> --check` (`serveFace --check`).
- When `replace` is true, `serving.replace` closes open WebSocket connections
  (`closeSockets`), calls `http.Server.Shutdown` and `serving.waitIdle` with a
  `30s` deadline (`restartShutdownBound`) so in-flight HTTP responses finish
  cleanly, and calls `Restarter.Replace` (`syscall.Exec`).
- If `Shutdown`/`waitIdle` exceeds `30s` or `syscall.Exec` fails,
  `serving.recover` reopens the TCP listener (`reopen`) and WebSocket upgrades
  on the original address, records `"refused"` on the restart request, and keeps
  serving.

`tillr-view` surfaces both the running build and recent restart events on
`drain_summary` ([`tools/tillr-view/build.go`](../../tillr-view/build.go)),
rendered in both `tillr view status` and
[`ObservatoryHeaderBar.tsx`](../../tillr-view/web/src/components/ObservatoryHeaderBar.tsx).

---

## Pausing a session

### Process isolation across `syscall.Exec`

For `tillr-drain loop` to `exec` while runs continue undisturbed, `ProcessSteps.Run`
([`tools/tillr-drain/drain/steps.go`](../../tillr-drain/drain/steps.go))
launches each `tillr-run exec` subprocess in its own process group
(`Setpgid: true`) with `Stdin` attached to `/dev/null` and `Stdout`/`Stderr`
redirected to regular files under `<laneRoot>/<lane>/durable/<run>/`
(`RunRecordFile` = `exec.record.json`, `RunStderrFile` = `exec.stderr`), closing
both file descriptors in `tillr-drain` immediately after `cmd.Start()`. The loop
holds no open pipe to any worker, and `tillr-drain` signals (`SIGINT`,
`SIGTERM`) never propagate to detached `tillr-run` process groups.

### `tillr-run pause` and adapter `pause-kind` declarations

`tillr-run pause --store <path> --run <id> [--pause-timeout-seconds 120]` calls
`run.RequestPause` ([`tools/tillr-run/run/pause.go`](../../tillr-run/run/pause.go)),
which verifies that the run currently holds a leased row in `lanes` and appends
a `run.pause-request` (`PauseRequestKind`) event carrying `{"timeout_ms": ...}`
without modifying item state, retry counts, or verdicts.

Every backend adapter directory declares a single-line `pause-kind` file read by
`run.ReadPauseKind`, governing how `tillr-run exec` stops the harness when it
observes `run.pause-request` ([`D-RT4`](#why-its-this-way),
[`D-RT9`](#why-its-this-way)):

| `pause-kind` | Shipped adapters | How `tillr-run exec` stops the run | How the next segment continues |
| :--- | :--- | :--- | :--- |
| `stops-at-a-gap` | [`pi`](../../tillr-run/backends/pi/pause-kind) | Writes `$TILLR_PAUSE_REQUEST` in the run's scratch directory and starts the straggler timer (`timeout_ms`, default `120s` on bounded pauses; unbounded when `timeout_ms <= 0`). [`stop-at-gap`](../../tillr-run/backends/pi/stop-at-gap) waits until `pi` emits its `{"type":"session"}` JSON header (confirming its `SIGUSR1` handler is installed), then sends `SIGUSR1` **only to the `pi` PID**, never to its process group or tool children. Patched `pi` finishes any in-flight tool batch and streaming turn, saves results, starts no new model or tool call, flushes the session, and exits `0`. Only if `timeout_ms` expires first does `tillr-run` kill the process group. | Resumes the saved `pi` session in place in the same lane and worktree. |
| `stops-where-it-stands` | [`claude`](../../tillr-run/backends/claude/pause-kind) | Sends `SIGTERM` to the harness process group immediately, waits `10s`, and kills any survivor. | Resumes the saved Claude session via `--resume <session_id>` (grounded in the 2026-11-16 owner measurement where `claude -p` interrupted by `SIGTERM` during `sleep 60` resumed with its prior context intact). |
| `cannot-resume` | [`agy`](../../tillr-run/backends/agy/pause-kind), [`muse`](../../tillr-run/backends/muse/pause-kind) | Sends `SIGTERM` immediately, waits `10s`, and kills any survivor. | Starts a fresh session (`prepareContinuation` in `pause.go`) whose opening message supplies the interruption reason, old transcript path, worktree state, any partial report, uncommitted paths, and original brief. |

### Terminal state and same-attempt continuation

When a run stops because of a pause request (and did not fail on an unrelated
harness error before being signaled), `tillr-run exec` records terminal state
`StatePaused` (`"paused"`) with `EndCause = "restart"` (`EndRestart`) and writes
`<lane>/durable/pause-notice` stating whether the stop happened cleanly at a
tool-call gap or interrupted a running command.

When `tillr-drain` settles or reaps a `"paused"` run, it spends no retry, raises
no attempt counter, and runs no gate or verdict. Unless the pause is held by an
explicit quota-wait record, the loop resumes the run on the same attempt and
lane (`resumeAfterWall` / `runContinuation` in `drain/loop.go` and
`prepareContinuationSession` in `run/pause.go`), passing the notice from
`durable/pause-notice` as the continuation prompt. A `"paused"` run that no pass
resumes and no quota-wait row accounts for is classified as `AttemptUnjudged`
and surfaced as a fault rather than silently left idle.

---

## Migrating in-flight work across builds

Once `tillr-drain loop` starts on a new binary, new dispatches immediately use
the new build ([`D-RT3`](#why-its-this-way)). Existing runs started under an
older loop image are handled according to their role:

- **Gap-capable worker sessions (`migrateBuildRuns` in [`build_restart.go`](../../tillr-drain/drain/build_restart.go)):**
  Each dispatch records the loop's `build.Hash()` in `Inputs.TillrBuild` on its
  durable dispatch record. On subsequent passes, `migrateBuildRuns` checks every
  leased lane whose recorded `TillrBuild` is non-empty and differs from the
  running loop's `build.Hash()`. If the run's adapter declares `stops-at-a-gap`
  (`adapterPauseKind`) and no `run.pause-request` exists yet for that run, the
  loop appends an **unbounded** `run.pause-request` (`{"tillr_build": "<hash>"}`,
  omitting `timeout_ms` so `TimeoutMS == 0`). Under [`D-RT4`](#why-its-this-way)
  and [`D-RT9`](#why-its-this-way), build migration never imposes a straggler
  kill timer on a long tool command, and adapters declaring
  `stops-where-it-stands` or `cannot-resume` (as well as runs without a recorded
  dispatch build) are not signaled by `migrateBuildRuns` — they finish on the
  build they started under.
- **Parallel read-only groups (`claimBuildReaders` in [`build_readers.go`](../../tillr-drain/drain/build_readers.go) and `restartParallel` in [`parallel_restart.go`](../../tillr-drain/drain/parallel_restart.go)):**
  Read-only scout/triage fan-out nodes write nothing to the worktree. When a
  parallel group's dispatch build differs from the current loop build, finished
  readers keep their recorded results; still-running readers have replacement
  `<run_id>-restart` rows claimed in `lanes` and `node_runs`, the old group is
  marked orphaned, live old workers receive `SIGTERM` (`interruptBuildRun`), and
  replacement readers launch under the new build.
- **In-flight judge runs (`awaitJudgeRecord` in [`restart_judge.go`](../../tillr-drain/drain/restart_judge.go)):**
  Detached judge runs continue across `syscall.Exec`; the successor loop polls
  for the judge run's terminal record in SQLite (`100ms` interval), verifying
  that a live `tillr-run` face still exists until the record lands.

---

## The settings file

Per-repository configuration lives in [`.tillr.toml`](https://github.com/mschulkind-oss/tillr/blob/main/.tillr.toml) at the
repository root, loaded by `settings.Read`
([`tools/tillr/settings/settings.go`](../../tillr/settings/settings.go),
[`D-RT7`](#why-its-this-way)). When `.tillr.toml` is absent, `settings.Read`
returns defaults in which every automatic publication or spending feature is off.
When present, `read` validates every table and key against a closed allowlist,
so any unknown table, unknown key, wrong type, or out-of-range value refuses
loop startup (and `tillr-drain loop --check`).

| Table and keys | Default when omitted | Meaning and validation |
| :--- | :--- | :--- |
| `[roll] command`, `paths`, `pause_timeout_seconds` | `command = ""`, `paths = []` (disabled), `pause_timeout_seconds = 120` | Non-empty `command` (run via `sh -c <command>` from `--root`) requires non-empty `paths` (matched via `path.Match` against changed path prefixes/components). `pause_timeout_seconds` must be a positive integer. |
| `[push] what`, `remote`, `ref` (`after_landing`) | `what = "none"`, `remote = ""`, `ref = ""` | `what` is `"none"`, `"green"`, or `"tip"` (legacy `after_landing = true` maps to `"tip"` and is refused if `what` is also set). Empty `remote`/`ref` resolve from `refs/heads/main`'s git upstream ([`D-RT8`](#why-its-this-way), [`D-RT10`](#why-its-this-way)). |
| `[lanes] express` | `0` | Non-negative integer count of reserved `express-1..N` lanes for owner-facing work. |
| `[sync] roadmaps` | `[]` (unrestricted) | Array of tracked roadmap paths scoping loop document sync. |
| `[order] record`, `settle_minutes` | `record = ""`, `settle_minutes = 10` | Clean repo-relative path for the queue order record and non-negative settle delay in minutes. |
| `[intake] dir`, `paths` | `dir = ""`, `paths = []` | Clean repo-relative directory and allowed-path prefixes for saved owner ideas. |
| `[comments] vantage_url`, `repo`, `paths`, `quiet_seconds`, `poll_seconds`, `max_running`, `compact_over_tokens`, `refresh_hours` | `nil` (disabled; when `[comments]` is present: `60s` quiet, `15s` poll, `3` max running, `40000` tokens, `0` refresh hours) | Enables Vantage review-comment intake; requires a valid HTTP(S) `vantage_url` and non-empty `paths` (see [`tillr-designer-sessions.md`](tillr-designer-sessions.md)). |
| `[prose] rewrite` | `false` | Boolean enabling automatic prose-rewrite items; `false` leaves findings advisory. |
| `[workflows] default` | `{}` | Table mapping seat names (`build`, `design`, ...) to `<name>@v<N>` workflow strings (see [`tillr-item-graphs.md`](tillr-item-graphs.md)). |

For non-landing store definitions, `settings.ReadWithoutLanding` refuses any
configured `[roll]` or `[push]` keys (even empty or false ones) because
non-landing stores do not complete on Git landings. At startup and during
`--check`, `StartupLine()` prints one summary line to `stderr` reporting every
active and default setting.

---

## Post-landing roll and push

After each landing (and when the after-merge proof advances `refs/tillr/green`),
`afterLandingCandidate` and `afterGreen`
([`tools/tillr-drain/drain/post_landing.go`](../../tillr-drain/drain/post_landing.go))
submit work to per-action background runners (`landingAction`). Each
`landingAction` runs at most one worker goroutine with a single replaceable
`pending` tip slot: if another landing arrives while an action is running, the
newest tip replaces `pending` and runs once the current execution finishes.
Outcomes are recorded as `drain.post-landing-action` (`postLandingKind`) events
carrying `{"action": ..., "what": ..., "detail": ...}`, where an empty `detail`
clears any prior warning in `tillr view waiting`.

### Post-landing roll, pi fork roll, and extension roll

When `[roll] command` is non-empty, `afterLandingCandidate` checks the candidate's
`BaseRef` (for fork patch-branch items) or the files changed in `tip^..tip` (via
`git diff-tree --no-commit-id --name-only --no-renames -r -z`):

1. **Tillr binary roll (`rollAction`):** When a trunk landing touches any path
   matching `roll.paths` (in this repository, `tools/tillr` and `tools/tillr-*`,
   [`D-RT6`](#why-its-this-way)), `rollAction` runs `sh -c <roll.command>` in
   `--root`. In this repository `.tillr.toml` sets
   `command = "python3 tools/tillr-roll/tillr-roll.py --no-request"`: the roll
   script tests, builds, smoke-checks, and swaps the binaries without waiting on
   itself, and `rollAction` then files the restart requests in the store
   (`restart.File`). It normally requests both roles; for a
   [schema upgrade](#store-schema-upgrades) it requests only drain, and the new
   drain requests view after migration. When preflight refuses a roll, the
   loop keeps running the old build and records the failure. Store failures
   raise the `store-roll-blocked` alarm.
2. **Pi fork roll (`piRollAction`):** When `candidate.BaseRef` is
   `refs/heads/patch/pi` (`piForkPatchRef`) or a trunk landing touches
   `yolo-packs/pi-fork/` or `bin/pi-forks.list` (`piSeriesPaths`),
   `submitPiRoll` runs `sh -c "<roll.command> --pi"`.
3. **Pi extension roll (`extRollAction`):** When `candidate.BaseRef` is a
   non-`pi` `refs/heads/patch/<fork>` branch (`isExtensionPatchBranch`) or a
   trunk landing touches extension inputs under `yolo-packs/matt/` or
   `bin/pi-forks.list` (`matchesExtensionRollPaths` in
   [`extension_roll.go`](../../tillr-drain/drain/extension_roll.go)),
   `submitExtensionRoll` runs `sh -c "<roll.command> --pi-extensions"`.

### Pushing what the proof has passed

When `[push]` is enabled (`what = "green"` or `"tip"`), `pushDestination`
resolves `push.remote` and `push.ref` (defaulting to `refs/heads/main`'s
configured upstream via `git for-each-ref`) and runs `git push --porcelain --
<remote> <sha>:<ref>` with a `5m` timeout (`pushTimeout`), never passing `+` or
`--force` ([`D-RT8`](#why-its-this-way)):

- **`what = "green"` (this repository's setting, [`D-RT10`](#why-its-this-way)):**
  Landings push nothing. Instead, `afterGreen` submits `pushGreen` whenever the
  after-merge proof advances `refs/tillr/green`, and once at loop startup so a
  remote left behind while no loop ran catches up. `pushGreen` skips the network
  call (`errAlreadyPublished`) when `refs/remotes/<remote>/<branch>` already
  contains `<green>`. The remote branch therefore never receives an unproven
  trunk commit.
- **`what = "tip"`:** Every trunk landing (`candidate.BaseRef == ""`) submits
  `pushTrunk` (`refs/heads/main:<ref>`).
- **Pre-push hook and patch branches:** `git push` runs the repository's normal
  hooks (`bin/patch-branches-publish`), which publish local `patch/*` fork
  branches at their current tips even when `main` pushes an older `green` SHA.
- **Failure visibility:** A failed or timed-out push logs
  `loop: push of <what> <sha> failed (...; the loop goes on)`, records a
  non-empty `detail` on `drain.post-landing-action` (`action = "push"`),
  surfaces in `tillr view waiting`, and never blocks the loop.

### Linear replay on a refused push

When `git push --porcelain` fails specifically because the remote branch moved
ahead (`refusedPushError`: porcelain `[rejected]` with `fetch first`,
`non-fast-forward`, or `stale info`), `healRefusedPush`
([`tools/tillr-drain/drain/push_refused.go`](../../tillr-drain/drain/push_refused.go))
recovers linearly without force-pushing:

1. Fetches the remote branch (`git fetch`) and acquires the landing history lock
   (`lockTrunkHistory`).
2. Verifies the main checkout is on `refs/heads/main` and that every unpublished
   local commit ahead of the fetched tip is a non-merge commit.
3. Replays each unpublished commit onto the fetched tip in order via
   `git merge-tree --write-tree` and `git commit-tree`, keeping original authors
   and messages while stamping the landing committer identity.
4. Pins `refs/tillr/published/<remote>/<branch>` to the fetched tip as the
   last remote tip this clone accepted, moves the main
   checkout with `git reset --keep` (aborting cleanly without changing files if
   any modified path holds dirty owner edits), and records rewrite absorption
   mappings so `refs/tillr/green` carries across without raising a
   `trunk-rewritten` hold. In `"tip"` mode `afterLandingCandidate` pushes once
   more immediately; in `"green"` mode the rebased tip is proved first and
   pushed at the next green advance.

Under [OQ-RP4](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-refused-push.md#OQ-RP4), tillr stamps the configured Git
identity on its own trunk landings; no other commit's identity can hold a landing. The
owner ruled: *"we need to make sure that the Tiller machinery and sub-agents are producing
commits that follow the identity that the author wants, which is what's configured in the Git
config, or if we have settings to change this in our Tiller config, whatever that is. Apart
from that ... The Tiller itself should not be enforcing anything about identities."* The
published pointer guards against remote rewrites, not against other authors or committers.

Any merge conflict, merge commit in the unpublished range, checkout off `main`,
or dirty overlapping path leaves the repository untouched and fails the push
with `mover: operator` and manual recovery commands.

---

## The developer's command in this repository

Shipping tillr (`yolo-packs/tillr`) owns the restart protocol, while this
repository owns how its source tree is tested, compiled, installed, and kept
alive ([`D-RT1`](#why-its-this-way)):

### `tools/tillr-roll/tillr-roll.py` (`just tillr-roll` and `just tillr-build`)

[`tools/tillr-roll/tillr-roll.py`](../../tillr-roll/tillr-roll.py) turns a
committed revision into installed binaries under the jail's durable `bin/`
directory:

- **Concurrency serialization:** Acquires an exclusive `flock` on
  `<git-common-dir>/tillr-roll.lock`. Before blocking on that lock, a caller
  targeting `HEAD` writes its target SHA to `tillr-roll.commit`; if an earlier
  holder rolled an older commit while the newcomer waited, `pending_newer_head`
  causes the newcomer to roll the newer commit rather than falsely reporting
  `already at <old_sha>`.
- **Clean sparse clone and front-end build:** Clones `tip` (`HEAD` by default,
  `refs/tillr/green` under `just tillr-build` unless `TILLR_BUILD_UNPROVED` is
  set, or `--commit <sha>`) into a temporary sparse checkout of `tools/` and
  `config/`, so uncommitted working-tree edits never enter a build. When
  `tools/tillr-view/web` exists, builds or copies `tools/tillr-view/web/dist`
  and passes `-tags observatory` when compiling `tillr-view`
  ([`OQ-SH3`](../design/tillr-selfhosted-build.md#OQ-SH3)).
- **Proof-aware test skipping:** Compares changed Go modules since the currently
  installed binary's `vcs.revision`. Tests are skipped when `refs/tillr/green`
  is already at or ahead of `tip`, or when `tip` is a recorded landing in
  `.tillr/state.db` with `gated_tree == landed_tree` (zero concurrent merge
  drift since its pre-merge gate). Otherwise, runs `tools/gate/go-test-shards.py`
  (`-k 6`) or `go test ./...` for each affected module.
- **Smoke check and atomic swap:** Compiles `tillr`, `tillr-drain`,
  `tillr-gate`, `tillr-item`, `tillr-land`, `tillr-run`, `tillr-store`, and
  `tillr-view` into a staging directory. It also compiles `tillr-watch` if
  the target revision has `tools/tillr-watch`. It runs `--help` on each and
  `tillr-drain loop --check` against `.tillr/state.db`, then installs the
  binaries into the jail's durable `bin/` directory with `os.replace`. On an
  immediate install of a pre-watch revision, the roller moves `tillr-watch`
  aside before writing `tillr-roll.commit`. It deletes the backup only after
  successful reporting and restores it if the roll fails. On a schema upgrade,
  only the replacement drain is exposed before the handoff. A failed preflight
  leaves the installed binaries untouched and appends `drain.roll-failed`.
- **Restart handoff and verification:** Unless `--no-request` is passed, runs
  `tillr-drain restart --root <root> --wait` and `tillr-view restart --root
  <root> --wait` concurrently for same-schema rolls. On an upgrade it waits
  for drain to migrate and restart before waiting on view. It waits up to `5s`
  for `drain.build-started` sequence numbers to advance on any `"restarted"`
  role. It also supports `--pi` and `--pi-extensions` to rebuild the durable pi
  fork and extension directories without touching Go binaries.

### Store schema upgrades

A landing that raises `store.SchemaVersion` follows the normal automatic roll.
The staged drain's `loop --check` tests the migration without committing it.
The roller then exposes only the replacement drain; the other programs stay
staged so they cannot migrate the store while the old loop is still running.
The old loop checks the candidate and replaces itself at its normal safe
boundary. The new loop commits the migration on its first writable startup,
promotes the staged programs, then publishes its build and acknowledges the
restart. It requests the view upgrade only after migration. The keeper and
worker processes are not stopped: a worker already running keeps its lease
and store connection, and the new loop takes up its recorded result. Migrations
must preserve the writes those already-running workers use to record their results.

The new loop promotes `tillr-watch` if it was staged. For a deferred install
of a pre-watch revision, after a successful requested handoff and report the
roller checks the staging directory and `tillr-roll.commit` directly. If
staging is gone and the stamp matches the target commit, it retires the
installed `tillr-watch` without using a marker.

If staging is still present after a successful report, the roller records
pending retirement in `tillr-watch.retire` beside `tillr-roll.commit`. A
successful deferred roll with `--no-request` also writes this marker rather
than retiring watch directly. The marker records the target commit and the
staging directory name. A refused restart or failed report neither removes
the binary as part of that install nor publishes a new eligible marker.

At the start of each binary roll, under the roll lock, the roller checks for
a marker from an earlier successful roll. If the recorded staging directory
still exists, it keeps both the marker and the binary. Once the directory is
gone, it removes both if the stamp matches the recorded commit; otherwise it
discards only the marker. This lets an older activator finish its install
without having to know about watch ([D-TW25](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-watch.md#D-TW25)).

If the store check fails, the old loop stays running. A store failure during
preflight or migration raises `store-roll-blocked` in the loop's alarms (also
read by `tillr view alarms` when the store is readable). The mover is the
operator: resolve the named store problem, then retry with `just tillr-roll`.
The alarm clears on a later drain build-started event. If SQLite cannot append
the event, the loop reads the alarm from `<store>.roll-alarm.json` instead; a
later drain startup that publishes its build clears that file.

### `tools/tillr-roll/tillr-keep` (`just tillr-keep` and `just tillr-restart`)

[`tools/tillr-roll/tillr-keep`](../../tillr-roll/tillr-keep) is the
lightweight supervisor holding `<root>/.tillr/keeper.lock`:

- Launches `tillr-drain loop` with the standard `tools/tillr-loop/tillr-loop`
  step flags and records `--policy` and `--lanes` in `.tillr/keeper.launch.json`.
- Because an in-place restart uses `syscall.Exec`, the loop's PID does not
  change and `tillr-keep` stays blocked in `proc.wait()`.
- If `tillr-drain loop` exits `0` or `2` (`operator-stop` via `tillr-drain stop`),
  `tillr-keep` exits `0` without relaunching. On non-zero fault exits, it sleeps
  with exponential backoff (`60s` doubling up to `1800s`, resetting after `300s`
  of healthy uptime) before relaunching.
- `just tillr-keep-stop` (`tillr-keep --stop`) opens a `pidfd` on the lock's PID,
  verifies `/proc/<pid>/stat` start time to prevent PID-reuse signaling, and
  sends `SIGTERM` to the keeper while leaving the loop running.
- `just tillr-restart` (`tillr-keep --restart`) captures the active `--policy`
  and `--lanes`, stops the keeper, stops `tillr-drain` and waits for exit,
  builds `HEAD` via `TILLR_BUILD_UNPROVED=<HEAD> just tillr-build`, and spawns a
  fresh background keeper attached to `.tillr/keeper.log`.

---

## Invariants and warnings

- **`syscall.Exec` preserves PID and process start time.** Never infer across a
  restart that an examination or lease owner died by checking `/proc/<pid>/stat`
  alone; any in-memory ownership held by the loop must either live on a
  close-on-exec file descriptor (`rollingOwnership`, `superviseHeavy`) or be
  explicitly released in SQLite before `syscall.Exec`
  (`releaseRestartExaminations`).
- **No parent-held pipes to surviving subprocesses.** Any child intended to
  outlive `tillr-drain loop`'s `syscall.Exec` must run with `Setpgid: true`
  (`ProcessSteps.Run`) or in a detached session (`Setsid: true` for
  `ProcessSteps.Gate`) and file-backed `Stdout`/`Stderr` closed immediately in
  the parent (`RunDir(s.Root, candidate.RunID)` for detached gate supervisors).
  Passing an `os.Pipe` to a long-lived child holds the pipe open across `exec`
  or delivers `SIGPIPE` when closed.
- **Detached gate supervisors use `PR_SET_CHILD_SUBREAPER` and `(ownerPID, ownerStartTicks)` liveness supervision.**
  `ProcessSteps.Gate` (`--tillr-gate-supervisor`) installs
  `PR_SET_CHILD_SUBREAPER` and monitors `(ownerPID, ownerStartTicks)` so a loop
  crash or explicit stop reaps the entire gate and separate-session shard tree
  ([`D-GR17`](../design/gate-rework.md#decision-ledger)), while `syscall.Exec` leaves
  `(ownerPID, ownerStartTicks)` intact so the supervisor keeps running across an
  in-place loop upgrade.
- **`stop-at-gap` signals only the `pi` PID, only after the session header.**
  Signaling before `{"type":"session"}` hits Node's built-in `SIGUSR1` debugger
  listener before `pi` installs its handler, and signaling the process group
  would deliver `SIGUSR1` to child shells or tools that terminate on `SIGUSR1`.

---

## Negative space

- **No filesystem watcher or automatic hot-reload of Go code:** Neither
  `tillr-drain` nor `tillr-view` polls `tools/` or watches inotify events;
  restarts happen strictly when a `drain.restart-request` event is recorded.
- **No straggler kill bound on build migration, and no mid-run migration for
  non-gap harnesses:** `migrateBuildRuns` omits `timeout_ms` so a long build or
  test tool inside a `pi` worker is never killed by a tillr upgrade, and
  `claude`, `agy`, and `muse` workers finish their current segment on the build
  they started under.
- **No forced pushes on refused autopushes:** `pushTrunk` and `pushGreen` never
  pass `--force`. Recovery uses a linear replay, as described in
  [`docs/design/tillr-refused-push.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-refused-push.md).
- **Patch-branch publication is not gated on `refs/tillr/green`:** When
  `push.what = "green"` pushes `main`, the `pre-push` hook still runs
  `bin/patch-branches-publish` at local `patch/*` branch tips; narrowing patch
  publication to proven commits remains an unmade owner choice.

---

## Current values

Verified at `a1cdcebb22562dd1a98ac530be5add442dcb59d3`.

| Value | Setting / literal | Defined in |
| :--- | :--- | :--- |
| Restart event kinds | `drain.restart-request`, `drain.restart-answer`, `drain.build-started` | `RequestKind`, `AnswerKind`, `BuildKind` in [`tools/tillr-drain/restart/restart.go`](../../tillr-drain/restart/restart.go) |
| Restart roles and answers | Roles: `"drain"`, `"view"`; answers: `"restarted"`, `"already-current"`, `"refused"`, `"superseded"` | `RoleDrain`, `RoleView`, `Answer*` in [`restart.go`](../../tillr-drain/restart/restart.go) |
| `exec` handoff and launcher env vars | `TILLR_RESTART_REQUEST`, `TILLR_RESTART_FROM`, `TILLR_LAUNCHER`, `TILLR_LAUNCHER_RESOLVE=1` | `RequestEnv`, `FromEnv`, `LauncherEnv`, `LauncherResolveEnv` in [`restart.go`](../../tillr-drain/restart/restart.go) |
| Drain unbounded-step restart poll & zombie reap intervals | `50ms` (`restartWork`); `200ms` (`reapInherited`) | [`restart_work.go`](../../tillr-drain/drain/restart_work.go), [`restart.go`](../../tillr-drain/drain/restart.go) |
| `tillr-drain restart --wait` poll interval | `100ms` | `restartFace` in [`tools/tillr-drain/main.go`](../../tillr-drain/main.go) |
| Observatory restart poll and HTTP shutdown bound | `250ms` (`restartPoll`); `30s` (`restartShutdownBound`) | [`tools/tillr-view/restart.go`](../../tillr-view/restart.go) |
| Default bounded pause timeout & non-gap `SIGTERM` grace | `120s` (`Roll.PauseTimeoutSeconds` / `--pause-timeout-seconds`); `10s` (`SIGTERM` before kill) | [`tools/tillr/settings/settings.go`](../../tillr/settings/settings.go), [`tools/tillr-run/main.go`](../../tillr-run/main.go), [`tools/tillr-run/run/exec.go`](../../tillr-run/run/exec.go) |
| Build-migration pause request payload | `{"tillr_build": "<hash>"}` (`TimeoutMS = 0`, unbounded; `stops-at-a-gap` only) | `requestBuildPause` in [`tools/tillr-drain/drain/build_restart.go`](../../tillr-drain/drain/build_restart.go) |
| Post-landing action event kinds & push timeout | `drain.post-landing-action` (`postLandingKind`), `drain.roll-failed`; push timeout `5m` (`pushTimeout`) | [`tools/tillr-drain/drain/post_landing.go`](../../tillr-drain/drain/post_landing.go), [`tools/tillr-roll/tillr-roll.py`](../../tillr-roll/tillr-roll.py) |
| `tillr-keep` fault backoff base, max, and reset uptime | `60s` base, doubling to `1800s` (`30m`), resets after `300s` (`5m`) uptime | `BACKOFF_BASE`, `BACKOFF_MAX`, `HEALTHY_RUN` in [`tools/tillr-roll/tillr-keep`](../../tillr-roll/tillr-keep) |

---

<a id="decision-ledger"></a>

## Why it's this way

Renamed from `D-RS1`–`D-RS10` on 2026-11-18 so the `D-RS` prefix belongs solely
to [`queue-persistence-ruling.md`](../design/queue-persistence-ruling.md)
([`decision-provenance-review.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md)).

| ID | Choice | Why |
| :--- | :--- | :--- |
| D-RT1 | Ship the restart request, session pause, and `.tillr.toml` settings in tillr itself while keeping the test-build-swap script (`tillr-roll.py`) and keeper (`tillr-keep`) in this repository *(owner ruling, 2026-11-15)* | Every repository running tillr needs graceful restarts and settings, whereas building from `tools/` into the jail's durable `bin/` directory is specific to developing tillr in this monorepo. |
| D-RT2 | Record restart requests as durable store events (`drain.restart-request`) addressed to a role rather than OS signals *(designer decision, 2026-11-15)* | Signals require PID discovery, race with process startup or exit, carry no answer payload, and are lost when the target process is temporarily down. |
| D-RT3 | Replace `tillr-drain loop` in place immediately via `syscall.Exec` so newly dispatched lanes run on the new build while older gap-capable sessions pause and resume one by one *(owner ruling, 2026-11-15)* | Waiting for all active runs to pause before `exec` would make every binary upgrade as slow as the slowest in-flight tool command across all lanes. |
| D-RT4 | Pause gap-capable sessions between tool calls (`SIGUSR1`), applying a `120s` straggler bound only on bounded pauses (operator or scale-down) and **no** straggler bound on build migration *(owner ruling, 2026-11-15; amended 2026-11-18 so no time limit fails work, `d6c6cc238`)* | Resuming between tool calls wastes zero tokens or command work; operator pauses need a deterministic upper bound, while an automatic tillr upgrade must never kill a legitimate multi-minute test suite. |
| D-RT5 | Validate the replacement binary on disk with `tillr-drain loop --check` (and `serve ... --check` for `tillr-view`) before relinquishing the running process *(designer decision, 2026-11-15)* | Catches broken flags, bad `.tillr.toml` edits, missing step programs, unsupported schemas, and failed trial migrations while the healthy loop is still serving and can report `"refused"`. |
| D-RT6 | Roll onto any landing that touches configured `roll.paths` automatically with no per-commit opt-out flag *(owner ruling, 2026-11-15)* | Every landed change must leave the system working; changes that must take effect together belong in a single commit rather than behind a deferred roll state. |
| D-RT7 | Read repository-scoped automation from `.tillr.toml` with every default off and unknown keys refused *(owner ruling, 2026-11-15)* | Prevents other repositories from silently inheriting this repository's self-roll or autopush behavior and catches typos in configuration keys at startup. |
| D-RT8 | Keep automatic `git push` optional, off by default, never forced, and non-blocking to the drain loop *(owner ruling, 2026-11-15)* | Remote network outages or diverged upstream branches must surface in `tillr view waiting` rather than halting local lane execution or overwriting remote history. |
| D-RT9 | Stop non-gap harnesses (`stops-where-it-stands`, `cannot-resume`) with `SIGTERM` + `10s` grace + kill on bounded pauses (resuming saved sessions or starting fresh sessions from transcript + worktree), patch the `pi` fork to stop at a tool-call gap on `SIGUSR1`, and restrict automatic build-migration pauses to `stops-at-a-gap` adapters *(owner rulings, 2026-11-15 and 2026-11-18)* | Avoids throwing away in-flight non-gap runs on routine tillr rolls while still allowing explicit operator/quota pauses to stop every adapter promptly without losing worktree progress. |
| D-RT10 | Configure `push.what = "green"` in this repository so automatic pushes publish `refs/tillr/green` rather than each unproved landing (`push.what = "tip"` remains available) *(owner decision, 2026-11-18)* | Under after-merge heavy verification, `main` can briefly hold an unproved or red commit; pushing `green` guarantees the remote branch only advances to proven commits. |
