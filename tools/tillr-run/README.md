# tillr-run — one request to a terminal run record

> [!WARNING]
> **Known Gaps (verified 2026-11-14 at `04957a8a83`).** `tillr` has landed a live
> candidate on an unattended tick, but three parts are unfinished:
> 1. **The AutoQA handoff skill is not shipped** — `yolo-packs/tillr/skills/` holds
>    `tillr-operating` only, so the design's `tillr-autoqa` handoff has no skill to dispatch.
> 2. **The observatory front end is not built** — `tillr view serve` answers 404 for the UI
>    until the front end under `tools/tillr-view/web` is built and embedded, which is a
>    separate Node step and not part of a Go build.
> 3. **The `tillr` on `PATH` is the pack's rendered tree, not this working tree** — an edit
>    under `tools/tillr*` reaches the launcher only after the pack is mirrored and rendered,
>    so a session that must run its own change builds the working tree first.
>
> **Do not wire `tillr` into another repository's gate while these stand.** The toolset
> ships early so it can be reviewed, not so it can be depended on.

## What this module owns

`tillr-run` executes one run request to a terminal run record under a caller-named lane
root, and reads the lane and run state back. `exec` is the run itself; `request` builds the
request; `pause` requests a restart interruption; `lanes`, `status` and `release` inspect
and clean up what a tick left behind. `copied-usage` detects repeated usage pairs;
`mark-copied-usage` corrects one run whose usage came from an earlier run.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](../../docs/design/tillr-distribution.md);
the shared adapter, brief, report, and policy contract is
[`docs/reference/tillr-muse-backend.md`](../../docs/reference/tillr-muse-backend.md).

## State invariants

- **One candidate, one lane, one record of what the run cost.** The record is written
  whatever the run's outcome, because a run with no record is a run nobody can judge.
- **Run provenance is recorded from the request.** `run.record`, `run.allocate`, and
  `runs` carry `seat`, `workflow`, and `node` from `run.request` alongside `backend` and
  `route`, and remain nullable/omitted on pre-change records.
- **A lane released is not a candidate retried.** Release reclaims the directory; the
  candidate's outcome is the store's.
- **Spend is read from the backend's own report**, not reconstructed from a transcript, so
  the number answers to the thing that spent it. After an operator ends the face,
  extraction still runs outside the canceled request, with its own existing time bound.
  Usage emitted before cancellation therefore remains measurable. A record with neither
  tokens nor cost says `spend_status: unreported` and carries a `spend_note` explaining why,
  except a registered command: backend `command` has no model, skips report extraction
  and completed-session detection, and records `spend_status: not-applicable` with no usage,
  note, or model route. The drain owns the command's exit-code meaning; see
  [registered command nodes](../tillr-drain/README.md#registered-command-nodes).
  Reclaiming a dead face's lease records that it left no terminal record; it does not
  extract its log or invent a price. Partial measurements retain their missing fields.
- **The backends are the boundary.** A pi, muse or other adapter's dialect never leaks into
  the record's shape; the adapters produce one report and this module owns the mapping.
- **A backend row's `harness_args` reach the harness unchanged.** A policy row may list
  arguments for the harness itself (tools, servers, standing text); the request's argv ends
  with `--` and then those arguments, in order, and a row with none gets no `--`. Only the
  claude adapter takes them; the others refuse the `--` before anything runs
  ([the claude backend design](../../docs/design/tillr-claude-backend.md#3-controlling-what-a-seat-may-use)).
- **Only external `SIGTERM` and `SIGINT` record `end_cause: signal`.** When a run has not
  already settled, hit a repeating-call limit, timed out, or recorded an
  `ending_exception`, `exec` records `end_cause: signal` for a direct `SIGTERM` or `SIGINT`,
  a canceled run context (`tillr-run`'s own signal handler killing the process group), or
  an adapter-forwarded exit code `130` (`SIGINT`) or `143` (`SIGTERM`). Crash signals
  (`SIGSEGV`, `SIGABRT`, `SIGBUS`, `SIGILL`, or unrequested `SIGKILL` with an uncanceled
  context) and all other non-zero exits keep `end_cause: normal-exit` so broken backends
  still close. Whatever the cause, every signaled exit, whether the process died by the
  signal or an adapter forwarded exit 128 plus its number, records the signal's name in the
  record's `signal` field (`SIGKILL`, `SIGSEGV`), so a kill is distinguishable from a crash
  and from a plain exit. The drain still classifies on `end_cause`: a crash signal or an
  unrequested `SIGKILL` counts against the backend, and only `SIGTERM`/`SIGINT` is an
  interruption.

## Standalone invocation

```sh
tillr-run exec
tillr-run lanes
```

`exec` is what a caller drives when it runs its own candidate rather than a whole drain;
`lanes` is the read-only answer to what a tick left behind.

`exec --resume` continues a run instead of starting one. The run id is unchanged, so the
`runs` row is one row and the resume is a `run.resume` event on it; the harness session
staging pinned in the lane (`session-id`, which every adapter passes to its harness) is
reused, so the provider's prefix cache is warm; and the run's log is appended to rather than
truncated, because the killed segment's transcript is the evidence the resume continues.
A request may supply `resume_prompt` to replace the default interrupted-session instruction;
it is ignored on ordinary fresh executions and also included when an adapter requires a fresh session
for the continuation. The drain uses it to ask an exited build worker to finish verification
and commit existing fenced work, without changing the staged brief.

The drain can instead set `session_turn` after a worker refusal: a new run id and
normal allocation in the same retained harness session. Its first message includes
`resume_prompt`, the current brief and the requirement to make a new commit. Both
endings stay separate records for the drain's identical-repeat check. This is not
`exec --resume`, which continues the existing run record. The lane must carry a
session handle before a returned turn can be claimed.

A resumed request can also set `fresh_session` to force a new harness session even
when the adapter can resume. This is how quota continuations move to a different
backend without reusing an unrelated harness's session id. The new first message
includes the supplied reason, earlier transcript, worktree, any report for this
run, and the original brief. The log remains appended and the run id unchanged.
The resume event carries the request so a replacement loop reads the current
backend and command. Its claim atomically removes any recorded quota pause. The
new session id is published to the lane only after that claim commits; recovery
uses the claimed id if publication was interrupted. A peer's paused finish also
marks its quota row as waiting in the same transaction, so a restarted loop and
the waiting face see the pause without needing another settlement pass.

## Worker child cleanup

When the worker ends (normal exit, timeout, signal, settled-idle stop, or pause), `exec`
sends `SIGKILL` to the remaining members of the worker's process group and scans `/proc`
for any non-zombie processes owned by the caller's effective UID carrying this
execution's `TILLR_RUN_EXEC_ID` marker, ending them across detached sessions and process
groups before writing the terminal record and releasing the lane. Each matched PID's start
time is re-verified via `pidfd` before signaling, and the scan-and-kill pass repeats
briefly so a dying parent racing a fork cannot leak a child; if live marked processes
survive past the bounded wait, `exec` returns an external error. For non-`stops-at-a-gap`
pauses, `exec` sends `SIGTERM` to both the worker's process group and marked detached
groups before waiting for the straggler bound. The same cleanup also runs before the
worker starts, for processes of an earlier execution of the same run: a resume gets a new
execution id, so a runner killed before it cleaned up leaves processes the id never finds.
When `TILLR_LOOP_STORE` is set, those carry the run's `TILLR_RUN_ID` and that store, and
are ended (the runner and its ancestors never are); without a store the run id is not
unique and only the execution id is used. As a fallback if a process ever survives
cleanup, the drain keeps the lane out of rotation while children still carry the run and
store environment markers and have working directories inside the lane; see
[lane holds](../tillr-drain/README.md#lanes-that-staging-refuses).

## Worker CPU priority

At startup, `exec` sets the worker process group's niceness to 10. Children started
later inherit that niceness. The heavy proof keeps the drain's priority. This
adjustment adds no time limit and stops no process. If the adjustment fails, the
run continues; `tillr-run` reports the error on stderr unless the group already exited.

## Child environment

`exec` sets the following variables for every backend, including registered commands.
Request identities replace inherited values; an omitted `node_id` removes any inherited
node identity. A run with no item exports an empty item identity.

```text
TILLR_RUN_ID              Always: the request's run_id.
TILLR_RUN_EXEC_ID         Always: a unique per-execution token used to end detached child sessions when the run ends.
TILLR_ITEM_ID             Always: the request's item_id, empty when omitted.
TILLR_NODE_ID             Only when node_id is nonempty: the request's node_id; otherwise unset.
TILLR_LANE_ID             Always: the resolved lane id (allocated if omitted).
TILLR_SESSION_MODE        Always: new for fresh/non-resumable sessions, resume for resumable continuations.
TILLR_RESUME_PROMPT_FILE  Always: the continuation prompt path, empty for a fresh execution.
TILLR_PAUSE_REQUEST       Always: the request-file path in this run's scratch directory.
TILLR_PROGRESS_FILE       Always: the progress file in this run's scratch directory; workers append one criterion claim per line.
TILLR_CHILD_SESSIONS_ROOT Always: the pi-subagents directory in this run's scratch directory, where the pi adapter's child sessions are discovered.
TILLR_CHILD_SESSIONS_MARKER Always: the child-sessions-recorded marker path in this run's scratch directory; its presence reports child-session recording, and its absence means not recorded.
TILLR_HARNESS_RECORD_FILE Always: the harness-record file in this run's scratch directory; an adapter writes one line naming the chosen binary, recorded on runs.harness.
TILLR_PI_BUILDS_DIR       When set on the caller or when the caller's scratch directory is set: the shared pi-builds directory snapshotted before per-run scratch isolation; otherwise unset.
TILLR_READ_ONLY_GROUP     Only when read_only_group is nonempty: the group sharing a lane lease; otherwise unset.
TILLR_PI_AGENT_DIRS_DIR   When set in the caller's environment or derivable from the caller's durable directory: the shared directory holding content-keyed trunk Pi agent directories.
TILLR_EXTENSION_SET_RECORD_FILE Always: the file path in this run's scratch directory where the Pi adapter records the selected extension set.
YOLO_DURABLE_DIR          When scratch_env is omitted or true: <lane dir>/durable/<run_id>; false preserves the caller's value or absence.
```

The optional Boolean request field `scratch_env` defaults to true, preserving the drain's
existing scratch isolation. False opts out of setting the scratch-directory variable,
not out of creating the run's directory. The terminal record's `artifact_refs` always
includes a `scratch` entry naming that directory, also stored on the run row and returned
by `status`. Logs, continuation prompts, and pause requests retain their normal locations.

## Harness attribution and drain-only rebuilt pi

`tillr-run exec` reads the line written by the adapter to `TILLR_HARNESS_RECORD_FILE`
and persists it on `runs.harness` and `run.Record.Harness` (as well as the `run.finish`
event returned by `tillr-run status`), so every run record names the exact binary and
build identity that served it.

For the Pi backend, `exec` snapshots `TILLR_PI_BUILDS_DIR` from the caller's durable
`pi-builds` directory (or an inherited `TILLR_PI_BUILDS_DIR`) before per-run scratch
isolation. The [Pi adapter](./backends/pi/README.md#resolving-rebuilt-pi-privately)
resolves a verified series-keyed build from that directory privately for drain runs —
falling back to the previous verified build in `current.json` and then to `pi` on
`PATH` — and records `<path> (series <key>)` or `<real-path> (installed <version>)`.
Nothing is placed on shared `PATH`, so foreground `pi` sessions stay on the installed
binary until a host apply and relaunch.

When a landing on trunk touches `yolo-packs/pi-fork/` or `bin/pi-forks.list` (or lands on
`refs/heads/patch/pi`), the drain invokes its `[roll]` command with `--pi` (`tillr-roll --pi`,
which runs `bin/pi-forks build-pi`). Runs already in flight finish on the `pi` they
started with, later runs pick up the new verified build, and a failed build discards its
staging directory without removing any working build or `current.json`, recording a
post-landing `roll` (`pi`) failure without stopping the loop.

## Extension-set attribution

Before starting a backend, `exec` clears any prior `extension-set` file in the run's
scratch directory and exports `TILLR_EXTENSION_SET_RECORD_FILE` (along with the caller's
`TILLR_PI_AGENT_DIRS_DIR`, or a path derived from the caller's durable directory before
per-run scratch isolation). When the Pi adapter writes the selected extension set to that
file, `exec` records it on `runs.extension_set` and `run.Record.ExtensionSet` (returned
by `tillr-run status` and `run.finish`). See
[trunk extension loading](./backends/pi/README.md#trunk-extension-loading-drain-workers-only)
for how drain workers build, select, and hold extension sets.

## Ending a settled harness that lingers

A top-level `agent_settled` stream event means the session has finished its work;
`agent_end` alone does not. The executor requires a freshly extracted report from
this execution whose final nonempty line is a recognized `RESULT:` footer. It then
ends the harness's private process group after **five consecutive one-second
samples** with no output or CPU activity. The interval and count are constants in
[the run package](./run/settled.go), with injected sample instants in the tests.
New stdout, stderr (including the adapter's separate stream error file), CPU ticks,
or changed group membership restarts the count. A new `agent_start` removes the
settlement evidence. A resumed execution cannot reuse an earlier segment's footer.

If Pi's last assistant turn has neither visible text nor a tool call, the extractor
reports a missing result even if an earlier turn printed a footer or Pi exited zero.
For a workflow worker, the drain uses its missing-report path: it preserves the work
and holds the node on its original backend instead of marking it complete. The node
can run again there after recorded backend recovery.
A separately recorded landing offer remains independent evidence of a tool call.

The kill reaches the harness and helpers in its private process group, not sibling
processes in the lane. Linux process counters include helper CPU and the CPU of
children the harness has reaped. Missing or unreadable evidence does not authorize
termination. This is not a watchdog for unfinished work: an unsettled session or a
report without a final footer can remain silent indefinitely.

The run stays `exited`, retaining its observed signal exit code, with the new
`end_cause: settled-idle`. Its `run.record`, `run.finish` event and `run status`
include `settled_at` (when the executor observed settlement) and `harness_ended_at`
(the terminating sample's instant). Ordinary exits keep their existing cause and
omit these fields; an immediate exit never arms the injected sampling clock. The
[drain judges the answer](../tillr-drain/README.md#settled-harnesses), rather than
retrying it as infrastructure failure.

The five quiet seconds never come for a harness whose own timers keep running. On
2026-11-18, three Pi workers settled with `RESULT: done` and a commit, and then sat
for seven hours, because a leaked session hold kept every extension timer armed and
woke Pi's main thread every two to three seconds. So the
[Pi adapter's watcher](./backends/pi/README.md#ending-a-settled-session-pi-does-not-exit)
also ends a settled session on Pi's own statement that nothing more is owed. It
then appends a `tillr_harness_ended` event to the stream and exits 0. The executor
copies that event into the record as `ending_exception`, for example
`ended at agent_settled; pi did not exit after its session settled with no background
work owed; sent SIGTERM to pi 1234; harness exited 143`. The run stays `exited` with
`end_cause: normal-exit` and exit 0. That makes it an answered run, judged on its
report. It is neither a failure nor an interruption: the drain must not replay it
or treat it as signal-killed. `ending_exception` is accepted only on an `exited`
record, and `run status` returns it from the finish event. The settled-idle rule
stays as the ending for other harnesses.

## A session pi ended for a repeating call

Pi ends a session itself when the model keeps making one identical tool call. That is
pi's own loop detection, which runs in foreground sessions too. In `--mode json` it
first emits `{"type":"loop_detected","toolName":"read","args":{…},"streak":40}`, and
then the run ends and the process exits 0 like any ended run. On 2026-11-19 two pi
runs repeated one identical `read` 613 and 470 times in a row before anything stopped
them.

When the executor sees that event, it records the run as `exited`, with its real exit
code and `end_cause: repeating-call` rather than `normal-exit`. The record's
`repeating_call` names the tool, the arguments, an `args_digest` and the `streak`.
The arguments are re-encoded with sorted keys. `args_digest` is the SHA-256 of that
whole text, and `args` quotes at most its first 400 bytes. `run status` returns
`repeating_call` from the finish event. The record is refused if it has the cause but
no named call, or a named call under any other cause. The
[drain routes the ending](../tillr-drain/README.md#a-session-pi-ended-for-a-repeating-call):
it starts the work again in a new session, and never resumes the conversation that
looped.

When pi detects a loop and recovers in-run (`action: "recovered"`), it emits
`loop_detected` with `shape`, `tokensBefore`, and `tokensAfter` and resumes the
session without ending it. The executor records `loop_recovery` (`count`, `shape`,
`tokens_before`, `tokens_after`) on the run record without setting `repeating-call`;
if pi later emits `action: "ended"` (or omits `action`), both `loop_recovery` and
`repeating_call` are recorded.

`TestALoopDetectedStreamEndsAsRepeatingCall` runs two fixtures through `run.Exec`. They
are nine-call excerpts of those two runs, with the event appended, since those runs
predate it.

## Pausing a live run

```sh
tillr-run pause --store STORE --run RUN_ID --pause-timeout-seconds 120
tillr-run exec --store STORE --lane-root LANES --input REQUEST --resume
```

`pause` appends a durable request. The executor gives the adapter a request-file
path in `TILLR_PAUSE_REQUEST`; adapters that cannot stop at a tool-call gap also
receive SIGTERM, with ten seconds before a process-group kill. A gap adapter is
allowed to finish until the requested straggler bound, then its process group is
killed. A request with no `timeout_ms`, which is how the loop's build migration asks,
has no bound: a gap adapter stops at its next gap or ends on its own, and nothing is
killed (`TestAPauseWithNoBoundWaitsForTheGapOrTheRunsOwnEnd`). The first continuation message identifies an interrupted command and says
it may be run again. `paused` is an attempt outcome, never a verdict or a retry.

The adapter declares its kind beside its program in `pause-kind`, not in a backend
policy. A missing or invalid declaration is refused before claiming the lane.
The CLI always requires that declaration; the Go package's empty `BackendsDir`
option is only for tests that execute raw commands without any adapter.

For review-comment dispatches, the Pi adapter also ships a
[compact step](./backends/pi/README.md#compacting-before-an-expired-resume). The drain calls it
before an expired, large-session resume and records its token and cost receipt against the
item; expiry itself runs nothing.

A resumable adapter continues its session; a non-resumable one gets a fresh UUID
in the same lane and worktree, plus a first prompt pointing to the prior run log,
report when present, and uncommitted work. The staged brief is kept unchanged.
Resume appends to the log and records `run.resume`, without allocating a second
attempt. The drain retains the item's `running` position while paused, continues
it on the next settlement pass, and only judges the completed candidate. Multiple
pauses are permitted on that same attempt.

## Usage attribution

Each spend extraction writes into a fresh directory inside the lane. A failed
extractor that writes no new report therefore records unknown tokens and cost, never
the preceding attempt's report. This applies to an immediately interrupted retry and
to extraction after a resumed segment; resume still parses its appended run log.
The prior report remains available rather than being deleted to make a retry safe.

Measured 2026-11-16 by the
[interrupted-retry regression](./run/spend_retry_test.go): a first attempt reported
1,500 tokens and $0.03. Before fresh extraction destinations, a retry interrupted
before output inherited both values when its extractor failed to write a report.
The regression requires the retry's returned record and stored row to have unknown
usage while preserving the first attempt's measured usage. The real Pi extractor
was also probed with an empty stream and exit code -1: it returned its documented
incomplete-report exit 4 with `tokens: unknown` and `cost usd: unknown`. The leak
was the shared report destination, not an empty stream reporting prior model usage.

### Correcting old copied usage

The per-attempt report fix prevents new leaks; it does not repair old stored usage.
**This repository's store needs the correction once after this change lands.** The
2026-11-17 inventory found 49 old runs carrying an earlier run's exact tokens and
cost, totaling $133.65; this is the recorded inventory, not a count enforced by the
command. The operator, not a lane, applies the correction to the live store.

First inspect the detector's JSON list. Each entry names a run, the earliest
strictly earlier run with equal nonzero tokens and cost, and the matching values.
It excludes unknown or zero usage, equal start times, and the same run continued
under its unchanged run id. Detection is read-only and writes no events.

```sh
# From this repository's root, using the newly rendered launcher:
tillr run copied-usage --store .tillr/state.db
# After checking that the listed matches are copied reports:
tillr run copied-usage --store .tillr/state.db --apply --reason 'Old attempts reused the preceding report before per-attempt extraction'
# Check again: after correction the list should be [].
tillr run copied-usage --store .tillr/state.db
```

Use the actual store path if it differs. The standalone binary takes the same
arguments after its face name:

```sh
tillr-run mark-copied-usage --store /path/to/state.db --run copied-run --source-run earlier-run --reason 'Preceding report reused'
tillr-run copied-usage --store /path/to/state.db
```

Equal measurements alone do not prove copying: two independent runs can cost the
same. Review the matches before applying, and use `mark-copied-usage` for selected
runs when a whole-store correction is not justified. A single mark requires both
known values to equal the named source's values and the source to have started
strictly earlier. A continued run cannot name itself as its source. A missing run,
unequal or unknown measurement, invalid ordering, or blank reason is refused
without changing a run or appending an event.

Each mark clears both stored measurements to unknown and appends one
`run.usage.copied` event holding the old tokens and cost, the corrected run,
the source run and the operator's reason. The row's event sequence points at this
event. Batch detection and all its marks happen in one transaction; any failure
rolls back all marks and events. The transaction does no Git, artifact-file or
process work. An empty batch changes no rows and appends no events. Running it
again after correction is therefore harmless.

The observatory, item totals and historical pass spend still count corrected runs
as runs, but no longer count their copied measurements. Missing-usage counts rise,
and sums remain lower bounds, not a claim that the corrected runs spent zero.
Historical pass snapshots retain their stored values; their displayed totals
exclude pairs identified by the correction events.

## What it refuses

- **A request whose lane root is not there** — refused before anything runs.
- **A policy naming no backend** — refused, naming the backends the policy does have.
- **A release of a lane a live run holds** — refused, so a release cannot pull the directory
  out from under a process.
- **A resume with no session to continue** — refused before the lane is claimed: a cold start
  wearing a continuation's name would pay for the whole transcript again.

## Test

`go test ./...` in this directory builds the binary and drives the faces against scratch
lane roots and stub backends; the run package's own suite pins the schema and the mapping.
