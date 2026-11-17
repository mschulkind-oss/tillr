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
skill is [`docs/design/tillr-distribution.md`](../../docs/design/tillr-distribution.md).

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

An external caller points these at a store snapshot and reads the same JSON the loop reads;
`status` answers where the board stands now, `events` answers what happened in order,
`waiting` answers why each unlanded item is not moving, `passes` lists what each planning
round decided, `lanes` lists every configured lane and its current or last run, and
`running` answers what is in flight.

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
[`tools/observatory/run.sh`](../../tools/observatory/run.sh), which serves *this* checkout's board from
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
[Home lane-strip design](../../docs/design/observatory-redesign.md#show-every-lane-and-its-current-work-on-home)
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
[pass](../../docs/design/observatory-redesign.md#4-entities-and-urls) is a planning
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

`tillr-view passes` lists the newest [passes](../../docs/design/observatory-redesign.md#4-entities-and-urls)
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
See [the drain's why guide](../tillr-drain/README.md#ask-the-planner-why) for
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
`unclassified`. The store does not record a class yet, so every row carries
`class_source: "rule"`: a fixed reason table in this binary supplies the class, with a
live run supplying `moving`. Unknown reasons remain verbatim and read `unclassified`,
not a guessed hold. A test enumerates every exported drain reason to require its rule.

**Why** is the newest decision's reason, not a fresh planner prediction. **Since** is
an absolute UTC time: the first decision of the unbroken trailing sequence with the
same action and reason. A changed action breaks that sequence even when the reason is
unchanged. The reader selects only the newest decision and the sequence's exact start
for each displayed item; neither a page size nor a time window truncates the sequence.
An item with no decision reads `no pass yet` in both fields.

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
clocks), `median_ms`, `p95_ms`, `max_ms`, `waited_ms`, and `failures`. Durations
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
runs. Runs with no item association are not assigned to an invented item.

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
