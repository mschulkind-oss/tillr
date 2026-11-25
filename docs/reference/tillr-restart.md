---
title: "Restarting tillr in place, pausing sessions, and post-landing roll and push"
status: accepted
stage: CURRENT
verified: 2026-11-24
covers:
  - .tillr.toml
  - tools/tillr/settings/settings.go
  - tools/tillr-drain/restart/restart.go
  - tools/tillr-drain/drain/restart.go
  - tools/tillr-drain/drain/stop.go
  - tools/tillr-drain/drain/schema_roll.go
  - tools/tillr-drain/alarms/store_roll.go
  - tools/tillr-drain/drain/restart_work.go
  - tools/tillr-drain/drain/restart_examination.go
  - tools/tillr-drain/drain/restart_judge.go
  - tools/tillr-drain/drain/restart_rolling.go
  - tools/tillr-drain/drain/steps.go
  - tools/tillr-drain/drain/parallel_restart.go
  - tools/tillr-drain/drain/quota_continue.go
  - tools/tillr-drain/drain/post_landing.go
  - tools/tillr-drain/drain/pi_roll.go
  - tools/tillr-drain/drain/extension_roll.go
  - tools/tillr-drain/drain/push_refused.go
  - tools/tillr-run/run/pause.go
  - tools/tillr-run/pihelper/stop_at_gap.go
  - tools/tillr-view/restart.go
  - tools/tillr-view/build.go
  - tools/tillr-roll/
  - tools/tillr-drain/keep/keep.go
  - tools/tillr-drain/keep/restart.go
related:
  - ./tillr-drain-successor.md
  - ./tillr-build-commit.md
  - ./observatory-redesign.md
  - ./tillr-refused-push.md
  - ../design/tillr-selfhosted-build.md
  - ./gate.md
tags: [tillr, drain, view, run, restart, settings, reference]
summary: "How tillr replaces `tillr-drain loop` and `tillr-view serve` in place through restart requests recorded in the store, pauses and continues agent sessions without spending an attempt, reads `.tillr.toml`, and rolls and pushes after landings."
---

# Restarting tillr in place, pausing sessions, and post-landing roll and push

**Status:** Verified 2026-11-24 by reading the code named in `covers`.
UNMEASURED: this reference records no observation of a live restart, pause or
push.

> **In short.** A restart is a request recorded in the store and addressed to a
> role. The running loop or observatory checks the binary a fresh launch would
> run, then replaces its own process image with it; workers already running keep
> their leases. A session pause is a second kind of request, answered by the
> runner according to what the harness can resume. `.tillr.toml` turns on the
> automatic roll and push that follow a landing; every such default is off.

| Package or program | Responsibility |
| :--- | :--- |
| `restart` in `tools/tillr-drain` | Restart requests, answers and build facts; candidate resolution and the in-place replacement |
| `drain` in `tools/tillr-drain` | When the loop may replace itself; what it releases first and reclaims after; post-landing roll and push |
| `run` in `tools/tillr-run` | Pause requests, adapter pause kinds, the paused ending and its continuation |
| `settings` in `tools/tillr` | Parses and validates `.tillr.toml` |
| `tools/tillr-view` | The observatory's own restart, and the build and restart facts it displays |
| `tools/tillr-roll` | This repository's test-build-install script and loop keeper |

---

## Restart requests and answers

Neither `tillr-drain loop` nor `tillr-view serve` watches the filesystem for a
rebuilt binary ([`D-RT2`](#why-its-this-way)). Package `restart` coordinates
through three append-only event kinds and no table of its own:

- **`drain.restart-request`** (`File`) addresses a request to role `drain` or
  `view`.
- **`drain.restart-answer`** (`Restarter.Record`) settles one request as
  `restarted`, `already-current`, `refused` or `superseded`, with the builds it
  moved between and a detail line. The newest unanswered request for a role is
  the only one acted on; `Restarter.Settle` answers every older one
  `superseded`.
- **`drain.build-started`** (`PublishBuild`, read by `ReadBuild`) records the
  build a loop or observatory is running each time one starts or completes a
  restart.

### Resolving and comparing the candidate binary

`LauncherCandidate` resolves the executable a fresh launch would run. When
`TILLR_LAUNCHER` is set — the pack's launchers, such as
[`yolo-packs/tillr/programs/tillr-drain/run`](../../yolo-packs/tillr/programs/tillr-drain/run),
export it — it runs that launcher with `TILLR_LAUNCHER_RESOLVE=1`, and the
launcher prints the binary it would run without running it. Otherwise it
resolves the process's own executable path through symlinks.

`FileDigest` hashes the running image and the candidate. Equal digests settle
the request as `already-current`. When they differ and the candidate's check
passes, `Restarter.Replace` sets `TILLR_RESTART_REQUEST` and
`TILLR_RESTART_FROM` and calls `syscall.Exec`. The new image's
`Restarter.Adopt` reads and unsets both, publishes its build, and records
`restarted`. A request filed while no process holds the role is answered
`already-current` by the next process to start.

---

## Replacing the loop and observatory in place

### `tillr-drain restart` and `tillr-drain loop --check`

`tillr-drain restart` files a `drain` request and prints its id; `--wait` blocks
until the answer and exits `1` on `refused`.

Before giving up its image, the running loop runs the candidate with its own
arguments plus `--check` ([`D-RT5`](#why-its-this-way)). `tillr-drain loop
--check` validates flags, settings, policies, step programs and any required
store migration, without committing the migration or starting a pass. A nonzero
exit answers `refused` with the candidate's output, and the old loop keeps
running.

### Safe replacement boundaries inside `tillr-drain loop`

The loop replaces itself without waiting for agent sessions to finish
([`D-RT3`](#why-its-this-way)). `gateRestartWith` (package `drain`) decides
when:

1. **Deferred while follow-up work is busy.** A request stays unanswered for a
   later pass while a post-landing roll or push is running or a trunk proof is
   active.
2. **Never queued behind a gate or landing.** Short mechanical steps — claiming,
   store judgment, the landing — hold a shared lock. The restart takes that
   lock only if it is free at that moment; otherwise the request waits for a
   later pass. Long waits (model sessions, whole-tree checks, a detached gate)
   release the shared lock and poll for a restart instead.
3. **Rechecked at the last moment.** `Restarter.Replace` returns
   `ErrSuperseded` when a newer request arrived, and checks the candidate again
   when the binary on disk changed after `Settle`.
4. **Examinations released explicitly.** `syscall.Exec` keeps the process id
   and start time, so the successor cannot infer that an examiner is gone.
   `releaseRestartExaminations` records the release in the store first. A
   candidate whose detached gate is live or already finished keeps its lane
   leased, and `reclaimDetachedGateExaminations` in the successor awaits or
   consumes that gate's result without running the gate again.
5. **Detached gates outlive the exec.** The gate program runs under a detached
   supervisor (`--tillr-gate-supervisor`, [`D-GR17`](./gate.md#why-its-this-way))
   that writes `gate.face.json` and `gate.done.json` in the run's directory.
   Rolling checks hold a close-on-exec pipe (`rollingOwnership`), so a
   successful exec releases ownership and a failed one keeps it.
6. **Inherited children are reaped.** `adoptRestart` publishes the live build
   and answers only the request carried across the exec; it does not pause,
   resume or record a build for any run. It lists the children the old image
   left (`inheritedChildren`), and `reapInherited` collects them as they exit.

### `tillr-view restart` and `tillr-view serve`

`tillr-view restart` files a `view` request, with the same `--wait`. The server
polls for a pending request, checks the candidate with `<args> --check`, closes
its WebSocket connections, shuts the HTTP server down within a bound
([Current values](#current-values)), and replaces itself. When shutdown
overruns the bound or the exec fails, it reopens its listener on the same
address, answers `refused`, and keeps serving. The observatory shows the
running build and recent restart events in its header and in
`tillr view status`.

---

## Pausing a session

The loop starts every `tillr-run exec` in its own process group, with standard
input closed and output redirected to files in the run's directory. It holds no
pipe to a worker, so a worker survives the loop's exec and the loop's signals.

`tillr-run pause --run <id>` (`RequestPause`, package `run`) appends a
`run.pause-request` event for a run that holds a lane lease. It changes no item
state, attempt count or verdict. Besides the operator, the loop files a pause
when a run must wait out a quota window, bounded by
`roll.pause_timeout_seconds`.

Each backend adapter declares one `pause-kind`, which decides how
`tillr-run exec` answers the request ([`D-RT4`](#why-its-this-way),
[`D-RT9`](#why-its-this-way)):

| `pause-kind` | Shipped adapters | How the run stops | How the next segment continues |
| :--- | :--- | :--- | :--- |
| `stops-at-a-gap` | [`pi`](../../tools/tillr-run/backends/pi/pause-kind) | `tillr-run pi-stop-at-gap` signals the harness process alone, once its session header shows the handler is installed. The harness finishes the tool calls in flight, starts none, saves the session and exits. The process group is killed only if the request's timeout expires first. | Resumes the saved session in the same lane and worktree. |
| `stops-where-it-stands` | [`claude`](../../tools/tillr-run/backends/claude/pause-kind) | Terminates the process group, waits a short grace, and kills any survivor. | Resumes the saved session. |
| `cannot-resume` | [`agy`](../../tools/tillr-run/backends/agy/pause-kind), [`muse`](../../tools/tillr-run/backends/muse/pause-kind) | The same termination. | Starts a new session whose opening message carries the interruption reason, the old transcript's path, the worktree's state and the original brief. |

A run stopped by a pause ends in state `paused` with end cause `restart`
(`StatePaused`, `EndRestart`), and the runner writes `pause-notice` in the
lane's durable directory saying whether it stopped at a gap or interrupted a
command. Settling a paused run spends no retry, raises no attempt number and
runs no gate. Unless a quota wait holds it, the loop continues the run on the
same attempt and lane with that notice as the prompt.

### Work in flight across a restart

A restart pauses nothing. Workers stay on their original leases, and the new
loop takes up their recorded results. `restartParallel` keeps every finished
result of a parallel read-only group and restarts only the executions the
operator interrupted or that were reclaimed after their owner died. A detached
judge run continues across the exec; the successor waits for its terminal
record (`awaitJudgeRecord`).

---

## The settings file

Per-repository configuration is [`.tillr.toml`](../../.tillr.toml) at the
repository root, read by package `settings`
([`D-RT7`](#why-its-this-way)). With no file, every automatic roll, push and
intake is off. An unknown table, an unknown key, a wrong type or an out-of-range
value refuses loop startup and `tillr-drain loop --check`.

| Table and keys | Default | Meaning |
| :--- | :--- | :--- |
| `[trunk] branch` | `"main"` | Short Git branch name where trunk landings go. Refuses `refs/` prefixes and invalid ref names. |
| `[roll] command`, `paths`, `pause_timeout_seconds` | no command, no paths, `120` | A command needs at least one path pattern. The command runs after a landing that touches a matching path. The timeout bounds a pause the loop files. |
| `[push] what`, `remote`, `ref` | `what = "none"` | `none`, `tip` or `green`. `after_landing = true` is an alternate spelling of `tip`, refused when `what` is also set. An empty `remote` or `ref` resolves from the trunk's configured upstream. |
| `[lanes] express` | `0` | Count of lanes reserved for owner-facing work. |
| `[candidate_size] file_limit`, `added_line_limit` | `27`, `1200` | Advisory size limits for a candidate. |
| `[sync] roadmaps` | none | Tracked roadmap paths the loop's document sync is scoped to. |
| `[intake] dir`, `paths` | none | Clean repository-relative directory and path prefixes for saved ideas. |
| `[comments]` | absent | Review-comment intake; see [`tillr-designer-sessions.md`](./tillr-designer-sessions.md#comment-intake-and-batch-items). |
| `[prose] rewrite` | `false` | Whether prose findings become rewrite items or stay advisory. |
| `[workflows] default` | none | Seat name to workflow, see [`tillr-item-graphs.md`](./tillr-item-graphs.md). |

A store whose lifecycle definition does not finish items by landing refuses
any `[trunk]`, `[roll]` or `[push]` key (`ReadWithoutLanding`). At startup and under
`--check`, `StartupLine` prints one line naming every setting, including
inactive defaults.

### Landing branch

`[trunk] branch` names the branch that serves as the repository's trunk
(default `"main"`). It governs where candidates are squashed, what a fresh lane
starts from when no proved commit exists, what the after-merge proof proves,
where `refs/tillr/green` advances, which branch's upstream `[push]` resolves and
publishes, what the trunk fixer and auto-revert act on, and what the read faces
and Observatory display. A fork's own checkout keeps its own `main` branch.

The main checkout may remain on another branch while the loop lands on the
configured landing branch. Only when the main checkout is on the landing branch
does a landing or remote-sync replay update the main checkout's working tree;
otherwise the ref advances without disturbing the checked-out branch.

A store records `trunk.branch` in `meta` on its first trunk landing (and legacy
stores with prior non-patch landings default to `"main"`). If `.tillr.toml`
later names a different `[trunk] branch`, `tillr-drain` refuses to start (and
refuses `--check`) with an error naming both branches.
To switch `[trunk] branch` on a repository that already has a store with
landings:

1. Stop the loop.
2. Archive or remove `.tillr/state.db` and the trunk-bound refs
   (`refs/tillr/green`, `refs/tillr/trunk-history/*`, and
   `refs/tillr/published/*`).
3. Update `[trunk] branch` in `.tillr.toml` and initialize a new store (or
   revert `.tillr.toml` to the recorded branch).

---

## Post-landing roll and push

After each landing, and when the after-merge proof advances `refs/tillr/green`,
`afterLandingCandidate` and `afterGreen` (package `drain`) hand work to one
background runner per action (`landingAction`). A runner executes one job at a
time and keeps one pending slot: a newer tip replaces the pending one. Each
outcome is a `drain.post-landing-action` event; a nonempty detail shows in
`tillr view waiting`, and an empty one clears it. No outcome blocks the loop.

### Post-landing roll, pi fork roll, and extension roll

With `roll.command` set, three actions can follow a landing
([`D-RT6`](#why-its-this-way)):

1. **Binary roll.** A trunk landing that touches a `roll.paths` match runs the
   command through the shell in the repository root, then files the restart
   requests: both roles, or only `drain` for a
   [schema upgrade](#store-schema-upgrades). A refused roll leaves the old
   build running and records the failure.
2. **Pi fork roll.** A landing on the pi patch branch (`piForkPatchRef`), or a
   trunk landing touching the pi series' inputs (`piSeriesPaths`), runs the
   command with `--pi` (`submitPiRoll`).
3. **Extension roll.** A landing on another fork's patch branch
   (`isExtensionPatchBranch`), or a trunk landing touching extension inputs
   (`matchesExtensionRollPaths`), runs the command with `--pi-extensions`
   (`submitExtensionRoll`).

### Pushing what the proof has passed

With `push.what` set to `green` or `tip`, `pushDestination` resolves the remote
and ref and the loop pushes with a timeout, never forced
([`D-RT8`](#why-its-this-way)):

- **`green`** ([`D-RT10`](#why-its-this-way)). Landings push nothing.
  `pushGreen` runs when the after-merge proof advances `refs/tillr/green`, and
  once at loop startup so a remote left behind catches up. It skips the network
  call when the remote-tracking branch already contains the green commit
  (`errAlreadyPublished`). The remote branch never receives an unproved trunk
  commit.
- **`tip`.** Every trunk landing pushes the trunk (`pushTrunk`).
- **Hooks.** The push runs the repository's hooks. A `pre-push` hook that
  publishes fork patch branches publishes them at their current tips, even when
  the trunk push carries an older green commit.

### Linear replay on a refused push

Only a rejection that says the remote branch moved (`fetch first`,
`non-fast-forward` or `stale info`) is healed. `healRefusedPush` fetches the
branch and, under the landing lock (`lockTrunkHistory`), replays the
unpublished trunk commits onto the fetched tip in order, keeping authors and
messages and adding no merge commit. It records the fetched tip under
`refs/tillr/published/` as the last remote tip this clone accepted, and carries
`refs/tillr/green` across the rewrite so no trunk-rewritten hold follows.

A conflict leaves the trunk, the green ref and the checkout unchanged and files
a fenced express repair item (`remote-sync-conflict`). A merge commit among the
unpublished commits, or a checkout that cannot be moved safely, needs the
operator.

Under [OQ-RP4](./tillr-refused-push.md#OQ-RP4), tillr stamps the
configured Git identity on its own trunk landings and enforces nothing about
any other commit's identity. The published pointer guards against a remote
rewrite, not against other authors.

---

## The developer's command in this repository

tillr owns the restart protocol. This repository owns how its own source tree
is tested, built, installed and kept running ([`D-RT1`](#why-its-this-way)).
Its settings name `just tillr-roll --no-request` as the roll command, for
landings under `tools/tillr*/` and `config/tillr/`.

### `tools/tillr-roll/` (`just tillr-roll` and `just tillr-build`)

[`tools/tillr-roll/`](../../tools/tillr-roll/) turns one commit into
installed binaries in the environment's durable `bin` directory:

- **One roll at a time.** Rolls serialize on a file lock. A roll that waited
  rolls the tip as of when the lock frees, and does nothing when that tip was
  rolled meanwhile, so any number of landings during one roll cost one further
  roll.
- **Committed source only.** It rolls the current tip, or `--commit <sha>`.
  `just tillr-build` builds `refs/tillr/green`, or the commit named by
  `TILLR_BUILD_UNPROVED`, from a clean clone. The observatory front end is
  compiled in when its built assets exist
  ([`OQ-SH3`](../design/tillr-selfhosted-build.md#OQ-SH3)).
- **Tests skipped when already proved.** Tests are skipped when
  `refs/tillr/green` contains the tip, or when the tip's landing records the
  gated tree equal to the landed tree. Otherwise it runs
  [`tools/go-test-shards.py`](../../tools/go-test-shards.py).
- **Smoke check, then swap.** Every program is built into a staging directory
  and run with `--help`; the staged `tillr-drain loop --check` runs against the
  store. Only then is each binary renamed into place. A failed step leaves the
  installed binaries untouched and appends `drain.roll-failed`.
- **Restart.** Without `--no-request` it requests both restarts itself and
  waits for the answers. `--pi` and `--pi-extensions` rebuild the pi fork and
  the extension directory without touching the Go binaries.

### Store schema upgrades

A landing that raises the store's schema version follows the ordinary roll:

1. The staged drain's `loop --check` tries the migration without committing it.
2. The roller exposes only the replacement drain. The other programs stay
   staged, so none can migrate the store while the old loop runs.
3. The old loop checks the candidate and replaces itself at its usual boundary.
4. The new loop commits the migration on its first writable startup, promotes
   the staged programs, publishes its build, answers the request, and only then
   requests the view's restart.

The keeper and the workers are not stopped. A running worker keeps its lease
and store connection, so a migration must preserve the writes a running worker
uses to record its result.

A program the target commit no longer builds is retired only after the staging
directory is gone and the roll stamp matches; until then a marker beside the
stamp keeps both ([D-TW25](../design/tillr-watch.md#D-TW25)).

When the store check fails, the old loop stays running. A store failure in
preflight or migration raises the `store-roll-blocked` alarm; the mover is the
operator, who resolves the named problem and runs `just tillr-roll`. A later
drain build start clears it. When the event cannot be appended, the alarm is
read from `<store>.roll-alarm.json` instead.

### `tillr-drain keep` (`just tillr-keep` and `just tillr-restart`)

[`tools/tillr-drain/keep/keep.go`](../../tools/tillr-drain/keep/keep.go) holds
`.tillr/keeper.lock` and keeps one loop running:

- It starts `tillr-drain loop` with the standard step programs and records its
  launch arguments in `.tillr/keeper.launch.json`.
- An in-place restart keeps the loop's process id, so the keeper keeps waiting
  on the same child.
- After any loop exit it waits and starts the loop again. The wait doubles on
  each quick exit up to a ceiling and resets after a long run
  ([Current values](#current-values)).
- `just tillr-keep --stop` stops the keeper alone and leaves the loop running.
  It checks the recorded process's start time before signaling.
- `just tillr-restart` stands the keeper down, stops the loop, builds the
  current tip with `TILLR_BUILD_UNPROVED`, and starts a new keeper.

---

## Invariants and warnings

- **The exec keeps the process id and start time.** Never infer across a
  restart that an examiner or lease owner died from the process table alone.
  Ownership the loop holds in memory must sit on a close-on-exec descriptor or
  be released in the store before the exec.
- **No parent-held pipe to a child that must survive.** Such a child runs in
  its own process group or session with file-backed output that the parent
  closes at once. A pipe either stays open across the exec or kills the child
  when it closes.
- **The detached gate supervisor watches its owner by process id and start
  time.** A crashed or stopped loop therefore takes the gate's whole process
  tree with it, while an in-place restart, which changes neither, leaves the
  gate running.
- **`tillr-run pi-stop-at-gap` signals only the harness process, only after the session
  header.** An earlier signal reaches the runtime's default handler, and a
  group-wide signal reaches tool children that die on it.

---

## Negative space

- **No filesystem watcher and no hot reload.** A restart happens only when a
  request is recorded.
- **A restart does not migrate a running session onto the new build.** A
  worker finishes the segment it started; only a pause request stops one early.
- **No forced push.** Recovery from a refused push is the linear replay; see
  [`tillr-refused-push.md`](./tillr-refused-push.md).
- **Patch-branch publication is not tied to `refs/tillr/green`.** Whether to
  narrow it to proved commits is undecided.

---

## Current values

| Value | Literal | Defined in |
| :--- | :--- | :--- |
| Restart poll during a long loop step | `50ms` | `restartPollInterval`, package `drain` |
| Inherited-child reap interval | `200ms` | `reapInherited`, package `drain` |
| Observatory restart poll; shutdown bound | `250ms`; `30s` | `restartPoll`, `restartShutdownBound` in `tools/tillr-view` |
| Pause timeout | `120` seconds | `roll.pause_timeout_seconds`; `tillr-run pause --pause-timeout-seconds` |
| Grace before killing a harness that cannot stop at a gap | `10s` | package `run` in `tools/tillr-run` |
| Push timeout | `5m` | `pushTimeout`, package `drain` |
| Keeper wait after a loop exit | `60` seconds, doubling to `1800`; reset after a `300`-second run | [`tools/tillr-drain/keep/keep.go`](../../tools/tillr-drain/keep/keep.go) |
| This repository's push setting | `push.what = "green"` | [`.tillr.toml`](../../.tillr.toml) |

---

<a id="decision-ledger"></a>

## Why it's this way

| ID | Decision | Why it stays |
| :--- | :--- | :--- |
| D-RT1 | The restart request, the session pause and `.tillr.toml` ship in tillr; the test-build-install script and the keeper stay in this repository *(owner ruling, 2026-11-15)* | Every repository running tillr needs restarts and settings; building from this source tree is specific to developing tillr here. |
| D-RT2 | A restart request is a store event addressed to a role, not an operating-system signal *(designer decision, 2026-11-15)* | A signal needs process discovery, races startup and exit, carries no answer, and is lost while the target is down. |
| D-RT3 | The loop replaces itself in place at once; running workers keep their leases and the new loop takes up their results *(owner ruling, 2026-11-15)* | Waiting for every run would make each upgrade as slow as the slowest tool command in flight. |
| D-RT4 | A gap-capable session pauses between tool calls, and a pause request carries a straggler bound; a tillr upgrade files no pause *(owner ruling, 2026-11-15, amended 2026-11-18)* | Stopping between tool calls wastes no tokens or command work, an explicit pause needs an upper bound, and an upgrade must never kill a long test run. |
| D-RT5 | The replacement is validated with `--check` before the running process gives up its image *(designer decision, 2026-11-15)* | Bad flags, settings, step programs or migrations are caught while the healthy process can still answer `refused`. |
| D-RT6 | Every landing that touches `roll.paths` rolls, with no per-commit opt-out *(owner ruling, 2026-11-15)* | Every landed change must leave the system working; changes that must take effect together belong in one commit. |
| D-RT7 | Repository automation is read from `.tillr.toml`, every default off and unknown keys refused *(owner ruling, 2026-11-15)* | Another repository must not inherit self-roll or push by accident, and a mistyped key must fail at startup. |
| D-RT8 | Automatic push is optional, off by default, never forced, and never blocks the loop *(owner ruling, 2026-11-15)* | A network outage or a diverged upstream must show as a waiting row, not halt lanes or overwrite remote history. |
| D-RT9 | A harness that cannot stop at a gap is terminated after a short grace on a pause, then resumed from its saved session or restarted from its transcript and worktree; the pi fork is patched to stop at a tool-call gap *(owner rulings, 2026-11-15 and 2026-11-18)* | An explicit pause stops every adapter promptly without losing worktree progress. |
| D-RT10 | This repository sets `push.what = "green"`; `tip` stays available *(owner decision, 2026-11-18)* | With after-merge verification the trunk can briefly hold an unproved commit; pushing green advances the remote only to proved ones. |
