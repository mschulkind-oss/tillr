# tillr-view — the board, read-only

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

`tillr-view` renders a board's status and event log from a store snapshot or from a stream
of records, answers what is running right now, and serves the same answers over HTTP. It
writes nothing anywhere, so it is safe to point at the live store while writers run.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-distribution.md).

## Earlier uncommitted work

The run detail API includes `preserved_work`, the loop's saved-edit settlement
records. The run page displays the saved Git ref, file and line-count summary,
and why the earlier run stopped. A pruned record says the ref was removed rather
than suggesting it can still be inspected. Both the current-planner and
recorded-history waiting views append this evidence to the item's reason, so it
also appears in terminal output and browser waiting tables. These are read-only
projections of events; the view neither creates nor deletes refs. The
[drain's preservation and retention rule](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md#earlier-uncommitted-work)
explains how the loop saves this work and when it removes the ref.

## Held lanes

The current-planner and recorded-history waiting views include `lane_status`
(`released` or `kept`) and `lane_reason` for pending held work. The same words
appear in the reason column of terminal and browser tables. A kept lane names
why its checkout is still needed or why safe release failed; a released lane
holds no candidate reservation even if another item now runs there. These are
read-only projections of drain events, not inferences from the item's old lane
number. See the [drain's held candidate capacity rule](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md#held-candidate-capacity)
for the kept classes and pinned-commit retry behavior.

## State invariants

- **It writes nothing, ever.** No store is opened read-write and no migration runs; a view
  that could write would need a lock, and a reader that takes a lock is a reader that stops
  a drain.
- **The store is the source of truth, and the absence of a record is the answer.** Where a
  record was never emitted, "there is none" is reported as such and never reconstructed from
  memory.
- **The record stream is the other source.** `status` and `events` read either a store or a
  records file, so a caller with artifacts and no database still gets the same shape.
- **`serve` needs a front end to have been built.** Without one it answers 404 for the UI
  and says so in the response, rather than serving a blank page.

## Standalone invocation

```sh
tillr-view status
tillr-view events
tillr-view waiting --store /path/to/.tillr/state.db
tillr-view passes --store /path/to/.tillr/state.db --last 2
tillr-view lanes --store /path/to/.tillr/state.db [--json]
tillr-view running --store /path/to/.tillr/state.db [--watch] [--json]
```

An external caller points these at a store snapshot; `status --json` reads the same JSON the loop reads;
`status` answers where the board stands now, `events` answers what happened in order,
For opted-in agent reviews, the waiting face recognizes `judge-grant-absent`,
`judge-unreadable`, and `eval-failed`. A failed or unreadable review includes its
recorded first reason, rather than presenting an unexplained hold. The
[drain's judge contract](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md#opted-in-judgment-before-the-gate)
defines those outcomes and when the judge's verdicts become records.

`waiting` answers why each unlanded item is not moving, `passes` lists what each planning
round decided, `lanes` lists every configured lane and its current or last run, and
`running` answers what is in flight. `status --text` prints a readable summary;
status without a format flag, or with `--json`, emits its `view.status` record. Store status reads the last whole writing sync's observation
directly from the item-owned table, without importing the item writer or reading documents
for these counts. It prints `unshared_open` (open store items no document declares),
with the oldest item's last-write age in whole minutes, and `stale_doc_blocks` (blocks
for landed items), alongside the sync's timestamp. JSON carries `synced_at`, both
counts, `unshared_open_ids`, `unshared_open_oldest`, and `stale_doc_block_ids`.
The age advances at read time, but the counts describe that saved sync, not current
documents. With no table or row, text says `no sync has been recorded` and JSON counts
are null, never zero. A record stream instead reports its latest supplied sync
observation, or null when it carries none. For an undeclared request, waiting output names
triage on the design seat while it is due or running, then the owner with the full
conflict report. See the [document-fed workflow prerequisite](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md#document-fed-workflow-prerequisite).

### Conflict transitions in the event list

`events` preserves its raw event list and cursor for consumers of the recorded stream.
Conflict events also carry `conflict_id`, `first_seq` (the first record's sequence),
and `transition` (what changed). Its `conflicts` list groups those records as one
conflict with named, dated transitions. A status or design-run change names that
change; new report evidence names the run that supplied it. Only a changed report
appears again in the grouped transitions, not the report repeated by a routing move.
With `--since`, the grouped list includes only later transitions but still names the
original sequence and compares against the previous record before the cursor.

## Starting the observatory

From any directory, with any `PATH` that holds the toolchain (`go` and `git`, plus `node` and
`npm` when the front end needs building), on the host or in a yolo jail:

```sh
just --justfile /path/to/checkout/.justfile observatory [port]      # one process, default 4318
just --justfile /path/to/checkout/.justfile observatory-dev [port]  # Vite on port, API on port+1
```

Inside the checkout, `just observatory` is the same command. Both recipes run
[`tools/observatory/run.sh`](https://github.com/mschulkind-oss/tillr/blob/main/tools/observatory/run.sh), which serves *this* checkout's board from
*this* checkout's build:

- **Both programs are built from this tree.** `tillr-view` and the `tillr-drain` it asks for
  Waiting are built static (`CGO_ENABLED=0`) into
  `${YOLO_DURABLE_DIR:-${XDG_CACHE_HOME:-~/.cache}}/tillr-observatory/<key>/`, where `<key>`
  is the first 16 hex digits of the sha256 of the checkout's absolute path. Two checkouts
  never share that directory. The server is handed the planner with `--tillr-drain`, and the
  store and root as absolute paths, so neither `PATH` nor the working directory decides which
  board is served.
- **The front end is rebuilt only when it is older than its sources.** Once `web/dist` is
  current, `just observatory` needs no Node. `node_modules` is stamped with the platform that
  installed it (`node_modules/.tillr-platform`) and is reinstalled with `npm ci` when another
  platform runs it, because a host and a container share it.
- **The URL** is `http://127.0.0.1:<port>`, printed at startup. For `observatory-dev`, that
  port is Vite's UI, and the API one above it answers JSON only.

### Reaching it from the host when it runs in a jail

A yolo jail with bridged networking has its own `localhost`, so a server in the jail is
reachable only from inside the container until the workspace publishes its port. Add this
to the **workspace's** `yolo-jail.toml` (`$YOLO_HOST_DIR/yolo-jail.toml` on the host,
`/workspace/yolo-jail.toml` in the jail), for each port you want to open:

```toml
[machine]
ports = [{ host = 4318, jail = 4318 }]
```

Then run `yolo config apply` and `yolo stop` on the host, and relaunch the jail. While a
port is listed there, the recipe binds `0.0.0.0` (the container's side of the publish), and
the host opens `http://localhost:<port>`. For a port that is not listed, it binds
`127.0.0.1` and prints the line to add. `observatory-dev` publishes only the UI port: the
API is reached through Vite's proxy. The jail cannot see whether it was relaunched after the
line was added, so a listed port that does not answer from the host means it has not been.

### The performance log

`serve` writes its own account of how long things took, on by default under `just observatory`
and `observatory-dev`, so "the board is slow" can be answered from a file rather than
reproduced. It is a different subject from the [Performance API](#performance-api), which
reads the drain's gate and item timings out of the store; this log times the requests the
board itself answers.

- **Where.** The recipe hands `serve` the directory
  `<checkout>/.yolo/durable/tillr-observatory/logs/<hostname>-<port>/` and prints it at
  startup. It is under the served checkout, not the jail's durable directory or `~/.cache`, so a
  log the host's server wrote is the same directory a jail of that checkout reads. Each day
  is one file, `server-YYYYMMDD.jsonl`, and the file `latest` holds the path of the one being
  written (text, not a symlink, because a bind mount may not carry one).
- **What.** One JSON line per record, each with `v` (the record shape, 1), `kind`, `ts` and
  `rev` (the build revision):
  - `start` — the root, store, build, planner, the recipe's phases (`npm_ci`, `web_build`,
    `go_build_view`, `go_build_drain`, in milliseconds) and `listening_ms`, the time from the
    recipe's start to the listening socket;
  - `request` — one per HTTP request: request id (also the `X-Request-Id` response header),
    route, status, body and wire bytes, total milliseconds and the handler's `phases`
    (`payload_ms`, `encode_ms`, and where they apply `vantage`, `planner` and `lane_logs`).
    Query values are never written, only their names;
  - `ws` — per live connection, one line per ten-second window that sent frames: polls,
    slowest poll, frames and which subjects changed;
  - `client` — a batch of the browser's own timings, posted to `POST /api/v1/client-timing`
    every ten seconds and with `sendBeacon` when the page is hidden: each page view's time to
    first data and to settled, every API fetch as the browser waited for it (joined to the
    server's line by request id), long main-thread tasks and renders over 50 ms. The route
    accepts only this board's own origin;
  - `summary` — every minute while requests arrive and at shutdown: per-route count, p50, p95
    and maximum;
  - `dropped` — how many lines were lost. A request never waits for the disk: one writer
    drains a bounded queue, and a full queue drops and counts rather than blocking.
- **Retention.** A day's file is capped at 32 MiB (it rolls to `server-YYYYMMDD.N.jsonl`),
  files older than seven days are deleted, and the directory is capped at 128 MiB, oldest
  first. Nothing but `server-*.jsonl` is ever deleted.
- **Switching it off or moving it.** `OBSERVATORY_PERF_LOG=off just observatory` runs with
  no log; `OBSERVATORY_PERF_LOG=<dir>` writes there instead. `tillr-view serve` on its own
  logs only when given `--perf-log <dir>` (or `TILLR_VIEW_PERF_LOG`).
- **Reading it.** `just observatory-perf` (that is, `tillr-view perf-log`) prints per-route
  and per-page percentiles, the slowest requests with their phases, the browser-minus-server
  gap, the live channel's traffic and each start's phases, for the last 24 hours of every
  server's log; `--since 1h`, `--top 20`, `--json`, or a file or one server's directory
  narrow it.

**Vantage's index is cached.** `/why`, `/questions` and an item's `/doc` all need Vantage's
index, and building it walks the whole tree: about 11 seconds on this checkout. The server
keeps the last index and reuses it while a fingerprint of the tree's Markdown (each
non-hidden `*.md` file's path, size and modification time, plus `.vantage.toml` and
`.vantage/`) is unchanged and the index is under ten minutes old. Taking the fingerprint
costs about 10 ms, so an answer written in Vantage still leaves the list on the next read.
Concurrent requests share one build, and a failed build is never kept.

**Nothing slow runs inside a read transaction.** The server's store handle has one
connection, so a route that holds its read transaction makes every other route wait. The
index lookup therefore happens before the transaction opens, and `/why` runs its planner after
its transaction has closed (the planner reads the store in its own process). Measured against
the live store on 2026-11-17, a snapshot requested during a cold `/why` waited 16.8 s before
this change and 23 ms after it.

### What each refusal means

Every refusal is one sentence on stderr, and it names the fix:

- **`go is not on PATH`** or **`git is not on PATH`**: the toolchain is missing, so nothing
  was built. Run from a shell that has `go` and `git`.
- **`the front end in … is missing or older than its sources, and node is not on PATH`**:
  the embedded UI would be stale. Run once with Node. After that, no Node is needed.
- **`node is not on PATH`** or **`hivemind is not on PATH`** (dev only): Vite or its
  supervisor is missing. Install it, or use `just observatory`.
- **`… is already this build … serving …: open http://…`** (exit 0): the same build already
  serves this store. Open the URL.
- **`… is held by an observatory built from <rev> …, not this build`**: an older server
  holds the port. Stop it with the `fuser` command the message prints, then run again.
- **`… is already in use by something that is not an observatory`**: another program holds
  the port. Free the port or pick another one.
- **`… is already answering, so this run would not be the face you see`** (dev only): the
  API port is held. This check runs before any build. Stop the holder and run again.

The header's build line gains **behind the checkout** when the checkout's tillr sources have
moved past the running server's revision, which a long-lived server otherwise hides. The
check runs `git diff` between the build's revision and `HEAD` over `tools/tillr*`, plus the
working tree for a clean build, and its result is cached for three seconds. Restart with
`just observatory`.

## Every lane's current work

`tillr-view lanes` and `GET /api/v1/lanes` read the same rows. Home's lane strip
uses this answer, links every lane, and computes its counts from those rows. The
[Home lane-strip design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-redesign.md#show-every-lane-and-its-current-work-on-home)
defines its three states: idle, running, and settling. A terminal run still held
by a lease is settling, not evidence that a gate or landing is in progress.

Rows carry the item, seat, backend, attempt number, run id, state-start clock,
and latest action with its clock. The seat is the one the dispatch decision recorded
(`dispatches.seat`), else the item's requested seat, and `seat_source` says which
(`dispatch`, `item` or `none`). `none` means the item asked for no seat and the
policy's default ran it, which Home shows as "no seat asked (policy default)". An idle row retains its last run and recorded
outcome. Attempt numbers count distinct runs for that item, including allocations
that have no terminal row yet. Missing clocks are null, never zero elapsed time.

A settling row carries only the last finished step and its finish clock. Home
draws `after gate ~40s`; `~` marks elapsed time calculated from a recorded clock,
not a recorded step in progress. No finished step means `step —`.

The live action reader recognizes pi and claude stream records, independent of
policy backend aliases. It reads at most the newest 256 KiB on each refresh and
ignores incomplete lines. Actions show a tool name and its first recorded argument,
or the first 80 characters of assistant text. A missing action clock uses the
log's modification time and is explicitly marked as derived. If no readable
action exists, log growth is labeled a liveness signal, not an action; an
unreadable or missing log carries an explicit note. Home marks only running
lanes quiet after five minutes without log growth. Settling lanes never receive
that mark. The strip refreshes on store/log notifications and polls every five
seconds for step files and quiet ages; Pause stops automatic refreshes.

`--loop-dir` and `--lane-root` default to the loop and lanes directories beside
the store, on every face including `serve`, so each file is found from the store
the server was given and never from a path recorded under another mount. Before
2026-11-16, `serve` had no lane-root default. Every lane then showed `open : no such
file or directory` and no action. A log that is truly absent is now a plain note:
`no stream log at <path>`. The JSON command prints the same array served as the API's
`data`.

## Trace downloads

`trace` exports the same spans returned by the run's timeline route, including the
store's gate-check measurements. It prints a standard JSON document directly, with
no tillr record envelope, and never writes to the store or rebuilds the trace index.

```sh
tillr-view trace --store /path/to/.tillr/state.db --run run-1 --format otlp-json
tillr-view trace --store /path/to/.tillr/state.db --item item-1 --format chrome-trace
tillr-view trace --store /path/to/.tillr/state.db --pass 20261115T031405Z
```

Choose exactly one of `--run`, `--item`, or `--pass`. A
[pass](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-redesign.md#4-entities-and-urls) is a planning
round identified by its UTC dispatch stamp; the second-resolution stamp selects
all dispatch decisions in that second. Only decisions naming a recorded run can
contribute a run. Each run keeps a separate trace id when an item or pass exports
multiple runs. No matching recorded runs is an explicit refusal, not an empty file.
`--loop-dir` and `--trace-index` have the same defaults as `serve`.

`GET /api/v1/runs/{id}/trace?format=otlp-json` and `format=chrome-trace` download
those same documents. The default format is `otlp-json`; unknown formats are refused.

- **OpenTelemetry JSON** follows the [OTLP JSON encoding](https://opentelemetry.io/docs/specs/otlp/#json-protobuf-encoding):
  resource spans, instrumentation scope spans, hexadecimal trace/span ids, typed
  attributes, and decimal-string nanosecond timestamps. One trace id names each run.
- **Chrome trace-event JSON** follows the [trace-event format](https://chromium.googlesource.com/catapult/+/HEAD/tracing/README.md):
  complete (`X`) events with microsecond start and duration, one process per run,
  and one thread track per span. Standard flow start/finish events link each parent
  to its children across tracks; trace, span and parent ids also remain in `args`.
  Separate tracks prevent overlapping siblings from being inferred as false children.
  Flow events are links, not additional duration spans.

Every span carries item, run, pass, lane, backend and check attributes; an id that
was not recorded is an empty string. Original bounds are retained as
`recorded_start_unix_nano` and `recorded_end_unix_nano`. Parent windows expand to
enclose recorded children, marked `timing_expanded`, because gate work can follow
the agent's finish. A span with incomplete timing carries `timing_missing: true`
and borrows its enclosing parent's window, labeled `timing_source` as
`parent_envelope`. An untimed root derives its bounds from recorded descendants,
labeled `children_envelope`. These are layout bounds, **not measured
durations**; filter marked spans out of duration analysis. Missing timing is never
exported as a zero-duration measurement. If no positive recorded interval exists
anywhere in the run, export refuses rather than inventing a clock. The timeline
route still keeps missing clocks null; only the downloads derive enclosing bounds.

## What each pass decided

`tillr-view passes` lists the newest [passes](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-redesign.md#4-entities-and-urls)
first. One pass contains every decision with the same `dispatched_at` millisecond.
Decisions appear in current queue rank order, with unranked items last and item id
breaking ties. Each decision names its item, action, and verbatim reason; actions
that launch work (`dispatch` and `retry`) also name the lane and run when recorded. Items removed from the queue retain their
decisions as unranked rows. The command's help owns its flags.

The JSON output is the same array served as `data` by `GET /api/v1/passes`.
Each row carries `dispatched_at_ms`, `decisions`, and `phases`. The API returns at
most 50 passes, with an exclusive `before` clock for backward paging and an inclusive
`since` lower bound. Both accept nonnegative Unix milliseconds or an RFC 3339 time.
The clock on the oldest returned row is the next page's `before` value. Invalid
bounds receive `bad-query`; no decisions is an empty array, not a missing route.

Hold-only passes remain visible with `phases: null`, even when a newer writer stored
zero phase values for them. For dispatched passes, recorded pass ids select their
measurements; older rows without that link use an exact timing-row start clock.
No matching measurement means absent phases, not zero. Individual unrecorded phase
values remain null; a recorded gate count of zero leaves `gate_ms` null, while a gate
actually measured at zero milliseconds retains that measured zero.

Only the newest pass in each loop session carries `stop` and its recorded `detail`.
Paging never moves that annotation to an older pass. Session rows bound decisions by
their start and end clocks. Result records carry no session id: their stop clocks
associate them with the ended session before the next session starts. A missing
result reads `stop not recorded`, including a session still running. Older stores
without session rows use successive result clocks as session boundaries, including
history before the first session row was recorded; they cannot identify a missing
historical session boundary. Result files that explicitly record zero passes, such as
startup faults, do not supply any pass's stop. Unreadable or malformed result files
refuse the read explicitly instead of inventing a stop.

## Why each unlanded item is not moving

For a [prose rewrite hold](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md#prose-rewrites-before-build),
the waiting face shows `awaits-rewrite` as the reason and `rewrite-<item id>` as
the mover, classified as waiting on work rather than needing the owner. A rewrite
held by `prose-criteria-count` instead names the owner and the incompatible criteria
limit, rather than pretending that another worker can resolve it.


`waiting` reads the current planner through `tillr-drain why`, as does
`GET /api/v1/why`. The observatory's Home and Items screens use that route for
class, reason, and mover, including items no loop pass has ever considered.
The route's `data` object carries `rows`, `summary`, and the planner's event
sequence; its response snapshot uses that same sequence. Each row includes the
full planner decision. No readiness rules are duplicated in the view utility.

The planner is found in this order: `--tillr-drain <path>`, then the `tillr-drain` in the
server's own directory (where `just observatory` and `just tillr-build` put both), then
the pack launcher's `tillr-drain-<digest>` beside a `tillr-view-<digest>`, and only then
`PATH`. Its answer must carry a build stamp that matches the server's: the same VCS
revision and dirty flag, or the same launcher digest. A planner that names no build, or
another one, is refused as `planner-mismatch`. A planner that cannot be found or run is
refused as `planner-unavailable`. Both answer 503 and name the fix. Neither is a store
failure, and neither says the drain is not moving: the snapshot and its loop heartbeat
still answer, and Home reads "the planner could not be asked". An invalid policy or
an incompatible store produces an explicit refusal, never a historical answer
presented as current. Each request starts one planner process and reads the
current document index; it does not migrate the store or write decisions.

Configure `waiting` and `serve` with the loop's `--policy` and `--lanes` inputs.
These inputs are not retained in the store: absent policy means `no-policy`,
and absent lanes means all store lanes are offered. `waiting --json` prints the
presentation rows; the API also carries the summary and full decisions.
`since` reads `current plan`, not an invented historical start time.
See [the drain's why guide](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md#ask-the-planner-why) for
summary accounting and the per-item command.

A conflict's dated transitions are appended to its item's reason line in both the
current-planner and recorded-history waiting faces. A later fault or retained repair
may supply the reason and recovery command, but does not hide the conflict's transitions.

### Recorded-decision history

`waiting --history` retains the recorded-decision view, as does
`GET /api/v1/waiting`. It prints one row per item except those stored as
`landed`, `closed`, or `resolved-elsewhere`. The columns are rank, item id,
class, where, why, mover, and since, ordered by queue rank with unranked items
last. Its JSON output and the historical API use the same row objects; the API
places the array in its usual `data` field beside the read transaction's snapshot.


The classes are `needs you`, `waits on work`, `needs a cause`, `ready`, `moving`, and
`waits on the loop`. A fixed reason table supplies the class (`class_source: "rule"`),
with a live run supplying `moving`; explicit stored waits carry `class_source: "store"`.
Unknown reasons remain verbatim and read `needs a cause`, with a mover that calls for
adding the missing history rule, not a guessed hold. A test enumerates every exported
drain reason, requires its rule by name, and builds a store holding each reason to
check that every history row has a class, reason, and mover.

**Why** is the newest decision's reason, not a fresh planner prediction. **Since** is
an absolute UTC time: the first decision of the unbroken trailing sequence with the
same action and reason. A changed action breaks that sequence even when the reason is
unchanged. The reader selects only the newest decision and the sequence's exact start
for each displayed item; neither a page size nor a time window truncates the sequence.
An item with no decision reads `waits on the loop` with **Why** `not yet planned`,
**Mover** `the next pass`, and **Since** `no pass yet`.

If the latest run has a recorded `drain.orphaned` event (an explicit loop stop), the
history row instead reads `waits on the loop` with **Why** `run ended by a stop`,
that run's id as **Mover**, and the stop time as **Since**. A later `run.resume`
event for that run withdraws the stop label, even if both events share a millisecond.
A decision recorded at or after the stop also replaces the label, unless it is the
dispatch that launched the stopped run. A hold tied with the stop therefore wins.
Signals, exit codes, and run state alone do not establish a stop. None of these
history labels changes the default face's fresh planner answer.

A landing held by owner content reads `waits on work` at `land`, with
`trunk-path-dirty: ` and the dirty paths in **Why**, and `commit or restore the path`
as **Mover**. Its JSON row adds **waited**, a duration rounded to the nearest
second from **Since** to the read time. Other rows carry an empty **waited** value.
The terminal prints the duration after the paths in **Why**; Home shows it beside
**Since**. A different dirty path set starts a different reason sequence and a
new clock. A landed item no longer appears in this view.

A replacement loop holds an item on `run-live` while a run from an earlier loop is still
alive, because stopping a loop leaves its runs alone. It reads `waits on work` at `lane`.
**Mover** says the run finishes and the loop then settles it; the operator's option is
`tillr drain stop --end-runs`, which ends the surviving runs.

A retained conflict repair that cannot be staged uses its recorded failure event
instead: **Why** includes `conflict-repair-staging-failed` and the exact staging
cause, **Mover** names the input correction that enables the next repair pass, and
**Since** is that failure's timestamp. It reads `needs you` at `land` even before
another pass records a hold decision. Completing examination clears this failure
from the waiting view without deleting its original evidence.

**Where** reports the recorded stage: queue, lane, fence, gate, land, or trunk, using
the current run's gate and refusal events and landing rows, with retired candidate rows
as history. A later hold does not erase a recorded stage.
**Mover** names the live run, deferred item, unfinished blockers, or the first unanswered
question in the current design-document index. The index uses the same root and question
command configuration as `status` and `serve`. An unreadable or missing document index is
reported explicitly; a question hold whose question is no longer present reads
`question not recorded`. Holds without a recorded cause read `cause not recorded`.
No question or cause is invented from the reason text.

Both faces open the store read-only and pin assembly to one read transaction. Tests
compare CLI and API rows, all table row counts, and the database bytes before and after
reading.

## What is running right now

`running` is the one command an operator runs before deciding whether to touch a live
drain. It prints three blocks:

1. **The loop and lane header** — `lanes leased: M/K` and the loop's last stop reason from
the newest `.tillr/loop/drain-*.json`. When an event was appended after that file, the
header says `stale stop record — newer run exists at seq <N>`: the shape that hid a loop
whose newest stop record was an older take.
2. **The active runs** — the lane, run, item, rank, title, elapsed (the viewer's clock from
the run's start), lease left, and the run's `.log` path. With no live run it says
`0 live runs; lanes idle: lane-1` rather than drawing an empty table.
3. **The last five finished runs** — the outcome (`landed <sha>`, `gate-failed`,
`exited <code>`, `timed-out`), duration and cost.

The face joins three sources because no two of them suffice: the lane rows and their
`run.allocate` events (a run that is going has no `runs` row until its terminal write), the
terminal `runs` rows (a lease is cleared the moment a run ends), and the loop's own
`drain.loop` result files (the store records no loop stop). `--watch` re-renders once a
second in a terminal and appends every render when its output is a pipe; `--json` emits a
`view.running` record instead of the table.

## Performance API

All three routes use the usual `{snapshot, data}` response envelope and read one
store transaction. They do not migrate or write the store.

`GET /api/v1/perf/checks` returns `data.checks`, ordered by `check_id`. Each row
has `count` (all executions), `sample_count` (executions with valid start and end
clocks), `median_ms`, `p95_ms`, `max_ms`, `waited_ms`, and `failures`. Fast checks
are included with `budget_ms`, `overruns` (runs beyond that target in the requested
range), and `stalls`. A `stalled` check failed and passed nothing; the waiting
reasons `gate-stall-rerun` and `gate-stalled` distinguish its automatic retained-head
rerun from a second-stall item fault. Durations
are in milliseconds and keep fractional precision. The median averages the two
middle samples when the count is even. The 95th percentile uses the standard
nearest-rank definition: sort the durations and select position `ceil(0.95 * n)`,
counting from one. It is `null` with fewer than five timed samples; median and
maximum are also `null` with no timed samples. Plan spans are not checks. Wait
time sums disjoint recorded causes (`pool`, `writes`, `cheap`, and `after`).
`heavy` and `exclusive` are subcategories already included in `pool`, not extra
waits. Failures count the gate's `FAIL` and `REFUSED` negative judgments, not
`ERR`, `STALE`, or unknown outcomes.

Both performance routes accept optional `from_ms` and `to_ms`: nonnegative Unix
milliseconds, inclusive lower bound and exclusive upper bound. Each may appear
once; malformed, empty, repeated, reversed, or equal bounds return HTTP 400
`bad-query`. Omitted bounds are unbounded. Check ranges select by the check's
start, item ranges by the run's start; an execution without a start clock is
included only when both bounds are omitted. An empty range returns an empty
array, not an error.

`GET /api/v1/checks/{id}` returns `data.check_id` and `data.executions`: every
stored execution of that check, ordered by start and span id, with its `run_id`,
`item_id`, `verdict_id`, `tick_id`, `duration_ms`, and the same span fields as the
run's gate route. Encode slashes in check ids, for example
`/api/v1/checks/gate%2Fpython3`. Missing clocks give a `null` duration; a missing
run or item association gives a `null` item id rather than dropping the execution.
An unknown check returns HTTP 404 `check-not-found`. A store without the
check-measurement table returns HTTP 503 `table-absent`, not an empty measurement.
Malformed recorded waits on `/api/v1/perf/checks` return HTTP 503
`store-unreadable`; the check-detail route preserves the raw wait record.

`GET /api/v1/perf/items` returns `data.items`, ordered by `item_id`. Each row
contains `run_count`, `usage_tokens`, `cost_usd`, and `lower_bound: true`.
A **lower bound** here means the sum of recorded usage only, not an estimate of
missing usage or a claim that the backend reported its full spend. A total is
`null` if no run recorded that quantity; recorded zero remains zero.
`unrecorded_runs` counts runs with neither tokens nor cost, while
`tokens_unrecorded_runs` and `cost_unrecorded_runs` also expose partially reported
runs. Runs with no item association are not assigned to an invented item. Ended
runs contribute their recorded usage just like finished runs; a face that left
no terminal record contributes an unreported run after its lease is reclaimed.
The snapshot header includes `unreported_runs` and `cost_unreported_runs`, and
prints the latter beside the summed price even when some runs reported a price.
Designer sums likewise disclose missing prices beside the reported sum.

Pass responses and the passes command separate `spend_started_in_loop` from
`spend_carried_over`: measurements from work this loop started, and from earlier
loops' workers settled by this pass. Both carry a count of wholly unreported runs
and field-specific missing counts. A legacy pass with no measurement remains
`null`, rather than acquiring an invented zero.

## Designers and review replies

The **Designs** page lists documents named by the store. Each document's page shows
its last designer's session, cache expiration time, measured context size, handled
batches and summed recorded cost. Missing measurements stay visibly unknown.
The item's design-document pane shows the same designer record.

The arrival counts distinguish comments changed within the selected session's cache
lifetime, after it, and comments with no session to resume. Unmeasured arrivals are
separate rather than silently counted as late. Use these counts when choosing
`comments.refresh_hours`. Retrying staging does not add another arrival record.

A comment item's page shows the session-choice reason, threads and replies. A reply
waits on the served checkout before its file is written and stays `written` while
in the inbox. After the file disappears, the loop reads Vantage's review interface:
`delivered` means an agent turn answers the reply's round, `dropped` means the comment
is absent, and `not confirmed` means the comment remains without that answer.
A failed read changes nothing; confirmation never holds the item open.

The loop receives its Vantage address and repository name at startup, not through
another settings read. With neither provided, written replies stay `written` and the
page says replies are not being confirmed. These pages only read loop-owned records:
`GET /api/v1/designs` supplies designers and arrival counts, while the existing item
and document routes carry their comment and designer details.

## What it refuses

- **A failed `serve` open is retried on the next API read.** After repairing or creating
  the store, **Read it again** reaches it without a server restart. An interrupted
  rollback-mode writer is diagnosed as `SQLITE_READONLY_ROLLBACK` (extended code 776)
  with `tillr-store recover --store <path>` as the remedy; this face never performs
  recovery or changes journal mode itself. A healthy read-only handle is retained.
- **A store it cannot read** — reported as the version or the path it found, never as an
  empty board.
- **A subject that is not there** — a negative answer with a record, not an error.
- **A database on a stream source** — the two sources are distinct and a caller names one.

## Test

`go test ./...` in this directory builds the binary and drives the faces against a scratch
store, a records file and a served instance; the front end's own suite lives under
`web/` and runs on Node.
