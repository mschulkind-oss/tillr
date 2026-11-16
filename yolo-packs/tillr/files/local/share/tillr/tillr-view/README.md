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
tillr-view running --store /path/to/.tillr/state.db [--watch] [--json]
```

An external caller points these at a store snapshot and reads the same JSON the loop reads;
`status` answers where the board stands now, `events` answers what happened in order,
`waiting` answers why each unlanded item is not moving, `passes` lists what each planning
round decided, and `running` answers what is in flight.

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

`waiting` prints one row per item except those stored as `landed`, `closed`, or
`resolved-elsewhere`. The columns are rank, item id, class, where, why, mover, and since,
ordered by queue rank with unranked items last. Its JSON output and the served
`GET /api/v1/waiting` use the same row objects; the API places the array in its usual
`data` field beside the read transaction's snapshot.

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
