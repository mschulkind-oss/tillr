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
and clean up what a tick left behind.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-distribution.md).

## State invariants

- **One candidate, one lane, one record of what the run cost.** The record is written
  whatever the run's outcome, because a run with no record is a run nobody can judge.
- **A lane released is not a candidate retried.** Release reclaims the directory; the
  candidate's outcome is the store's.
- **Spend is read from the backend's own report**, not reconstructed from a transcript, so
  the number answers to the thing that spent it. After an operator ends the face,
  extraction still runs outside the canceled request, with its own existing time bound.
  Usage emitted before cancellation therefore remains measurable. A record with neither
  tokens nor cost says `spend_status: unreported` and carries a `spend_note` explaining why.
  Reclaiming a dead face's lease records that it left no terminal record; it does not
  extract its log or invent a price. Partial measurements retain their missing fields.
- **The backends are the boundary.** A pi, muse or other adapter's dialect never leaks into
  the record's shape; the adapters produce one report and this module owns the mapping.
- **A backend row's `harness_args` reach the harness unchanged.** A policy row may list
  arguments for the harness itself (tools, servers, standing text); the request's argv ends
  with `--` and then those arguments, in order, and a row with none gets no `--`. Only the
  claude adapter takes them; the others refuse the `--` before anything runs
  ([the claude backend design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-claude-backend.md#3-controlling-what-a-seat-may-use)).

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
it is ignored on fresh executions and also included when an adapter requires a fresh session
for the continuation. The drain uses it to ask an exited build worker to finish verification
and commit existing fenced work, without changing the staged brief.

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
[drain judges the answer](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md#settled-harnesses), rather than
retrying it as infrastructure failure.

The [Pi adapter](./backends/pi/run-pi) uses `exec pi --print --mode json`: print-mode
cleanup belongs in Pi's `runPrintMode` runtime disposal, not in a shell wrapper
that kills on the first settlement event. The cached Pi source inspected on
2026-11-17 already awaits runtime disposal and stdout flushing, but does not force
exit if handles remain. A source fix must be delivered in the Pi fork patch series,
which is outside this change's allowed paths; no adapter-only forced exit is added.
The run-face rule remains necessary for other harnesses as well.

## Pausing a live run

```sh
tillr-run pause --store STORE --run RUN_ID --pause-timeout-seconds 120
tillr-run exec --store STORE --lane-root LANES --input REQUEST --resume
```

`pause` appends a durable request. The executor gives the adapter a request-file
path in `TILLR_PAUSE_REQUEST`; adapters that cannot stop at a tool-call gap also
receive SIGTERM, with ten seconds before a process-group kill. A gap adapter is
allowed to finish until the requested straggler bound, then its process group is
killed. The first continuation message identifies an interrupted command and says
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
