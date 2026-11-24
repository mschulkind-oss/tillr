---
title: "Tillr observatory — linked entity pages, read faces, and truthfulness contract"
status: accepted
stage: CURRENT
verified: 2026-11-24
covers:
  - tools/observatory/run.sh
  - tools/tillr-view/
  - tools/tillr-drain/holdrules/holdrules.go
  - tools/tillr-drain/stop/explanation.go
  - tools/tillr-drain/drain/pass_observation.go
  - tools/tillr-drain/drain/pipeline_observation.go
  - tools/tillr-drain/drain/spans.go
  - tools/tillr-run/run/progress.go
tags: [tillr, observatory, view, ui, reference]
summary: "How the tillr observatory answers operator questions across linked entity pages, CLI faces, HTTP and WebSocket routes and trace exports: waiting classes, pass and lane projections, the single run span waterfall, usage without dollar prices, and explicit marks for missing or derived data."
---

# Tillr observatory — linked entity pages, read faces, and truthfulness contract

> **Delivered copy.** This is `docs/reference/observatory-redesign.md` from this repository at commit `06e63d0112` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/observatory-redesign.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Verified 2026-11-24 against the tree for the route table, the navigation links, the
CLI face list, the HTTP route list, the waiting classes and the
[Current values](#current-values) table. Per-component behavior below that level was not re-read
line by line. UNMEASURED: no timed operator session is recorded; the evidence is the Go and
front-end test suites.

The **observatory** is the operator's read surface of tillr: CLI faces on `tillr-view` and a
React application embedded in `tillr-view serve`, launched for a checkout by
[`tools/observatory/run.sh`](https://github.com/mschulkind-oss/tillr/blob/main/tools/observatory/run.sh) (`just observatory`,
`just observatory-dev`). It answers why work is waiting, what each lane is doing, what each
planning pass decided, where a run or gate check spent time, what reached the trunk, and what
tokens and latency each run consumed. It never opens the store for writing, never shows a dollar
price, and never presents a missing or derived clock as a recorded fact.

| Component | Lives in |
| :--- | :--- |
| Checkout build and launch | `tools/observatory/run.sh` |
| CLI dispatcher, read-only HTTP and WebSocket server, response cursor | `tools/tillr-view` (`serveFace`, `serverSnapshot`, `apiResponse`, `apiFailure`) |
| Waiting projections | `tools/tillr-view` (`plannerWhy`, `assembleWaiting`) and `tools/tillr-drain/holdrules` (`Rules`, `Classes`, `Unknown`) |
| Stop explanation | `tools/tillr-drain/stop` (`Explain`) |
| Price removal on read payloads | `tools/tillr-view` (`withoutPrices` in `priceless.go`) |
| Shell, router, live cache, entity links, provenance marks | `tools/tillr-view/web/src` (`App`, `ObservatoryShell`, `EntityLink`, `StateGlyph`, `EpistemicBadge`, `queryKeys`) |
| Pages | `tools/tillr-view/web/src/components` |

**Reads with:** [observatory actions](observatory-actions.md) (queue places, reordering, idea
intake and the `--write` routes), [tillr item states](tillr-item-states.md) (projected item
states), [observatory truthfulness and shared primitives](observatory-design.md) (the provenance
rules and shared components), and
[the recording contract](observatory-design.md#recording-contract-and-read-model) (what the store must record
for these reads).

---

## Principles and invariants

- **P1. Every screen answers an operator question, and the home screen answers "what needs me?"**
  A page is organized by who must act next, not by which table stores the row.
- **P2. Every entity has one URL, and every mention is a link.** An identifier rendered as plain
  text strands the reader.
- **P3. Provenance is explicit.** Zero, unrecorded, not applicable, unreadable, lower bound and
  derived never share a mark
  ([the six marks](#six-distinct-marks-for-zero-missing-and-derived-facts)).
- **P4. One run's timing lives on one time axis.** Stages, model turns, tool calls and gate checks
  share one span waterfall.
- **P5. Repeated identical observations fold; transitions stand out.** Consecutive passes with the
  same outcome collapse into one row naming the count and the earliest time. No row is discarded.
- **P6. The terminal is a primary reader.** A summary the browser leads with is answered by a CLI
  face over the same Go projection, so a terminal check never scrapes HTML
  ([CLI faces](#cli-faces-http-api-and-trace-export) lists the two that are HTTP-only).
- **P7. A table being read does not move under the cursor.** Once a reader scrolls or focuses a
  row, membership and order freeze; rows update in place and arrivals wait behind a banner.

Four invariants enforce them:

- **Read-only store handle.** `tillr-view` opens SQLite read-only and never writes to the store or
  the repository. With `--write`, queue placement and idea intake are delegated to `tillr-item` as
  a child process ([browser writes](observatory-actions.md#browser-writes)).
- **A cursor on every HTTP response.** Every `/api/v1/` answer, success or refusal, carries the
  store path, schema version, event head, generation time, build stamp and whether writes are
  enabled, so the browser detects a page newer than its server.
- **Server-clock windows and honest pause.** Bounded time windows anchor to the response's
  `generated_at`, never the browser clock. Pausing stops both WebSocket invalidation and fallback
  polling, so the displayed age never ticks over a silently refreshed query.
- **No prices in read payloads.** The HTTP face removes price fields and price-shaped text before
  serialization; stored records are unchanged.

> [!WARNING]
> Do not read `drain_ticks` to list planning passes. It records only passes that entered the
> landing gate and omits every pass where all items held. Pass faces read `drain_passes` and
> `dispatches`.

> [!WARNING]
> Do not add an exported `Reason` constant in `tools/tillr-drain/drain` without adding its row to
> `holdrules.Rules`. `TestEveryReasonConstantHasAHoldRule` fails when a reason has no class, stage
> and mover.

---

## Entities and URLs

A **pass** is one planning round of the drain loop, named by its UTC dispatch stamp. A **waiting
class** is the bucket that says who can move an item next. A lane's **work state** is `idle`,
`running` or `settling`; `settling` is a lane lease that still wraps a finished run.

Routes are real paths driven by the History API. Every link is an `<a href>`; a plain left click
is intercepted into a push, and middle-click and open-in-new-tab stay the browser's.
`tillr-view serve` answers every GET outside `/api` and the assets with `index.html`.

| Surface | Route |
| :--- | :--- |
| Home triage and lane strip | `/` |
| Queue and waiting table | `/items` (`?state=`, `?sort=`, `?class=`, `?reason=`) |
| Dependency graph | `/graph` (`?item=<id>`) |
| Pass ledger | `/passes` (`?range=`, `?item=`, `?action=`, `?session=`, `?holds=hide`) |
| Runs across items | `/runs` |
| Check and item performance | `/performance` (`?range=24h\|7d\|30d\|all`) |
| Trunk health and landings | `/trunk`, `/landing/<id>` |
| Open and blocked questions | `/questions` |
| Designer sessions | `/designs`, `/design/<path>` |
| Read-only store tables | `/admin` |
| One item | `/item/<id>` |
| One run and its span waterfall | `/run/<id>` (`?span=`, `?tab=`, `?artifact=`, `?exchange=`, `?q=`) |
| One lane | `/lane/<id>` |
| One gate check | `/check/<id>` (`?span=`, `?run=`) |

`EntityLink` maps a kind and an id (`item`, `run`, `pass`, `lane`, `check`, `landing`, `doc`,
`question`) to its URL. `EntityText` finds identifiers and `docs/…/*.md` paths inside free text and
links them.

---

## Shell, live updates, and navigation

`ObservatoryShell` wraps every page in two fixed bands. An active backend alarm appears above
both, before any item row or count: the backend or judge that cannot run, the recorded cause, the
items and lanes behind it, and what clears it. `tillr view status --text`, `tillr drain why` and
`tillr view waiting` lead with the same block. An alarm reports a condition; it does not stop the
loop or move work to another backend.

1. **Top bar.** The navigation links, a warning when the served checkout has moved past the
   running binary or the page build disagrees with the server build, the connection indicator
   (live, polling age, or paused), pause and refresh, the idea button when writes are enabled, and
   a glossary.
2. **Loop band.** Pinned under the top bar. It shows `Running` with the session, last pass and
   next pass due; `Stopped` with the recorded stop explanation and its recovery action; or
   `Not known` when no session is recorded. It never reports a running loop as stopped for lack of
   a record.

Live invalidation runs over `GET /api/v1/ws`, which accepts loopback origins only and emits
per-resource invalidation frames. A growing worker log invalidates the transcript key alone. The
lanes query keeps polling while the socket is live, because step files and quiet-age counters
advance without store events.

Keyboard controls belong to the surface that mounts them:

- **Items and Passes tables:** `j` / `k` move focus, `Enter` opens the focused row, `[` / `]` step
  and open, `u` returns Home, and `J` / `K` reorder the focused item when writes are enabled and
  the sort is by place.
- **Span waterfall, when its axis has focus:** `+` / `=` zoom in, `-` zooms out, the arrow keys
  pan, `f` resets to the recorded bounds.
- **Idea dialog:** `Ctrl+Enter` submits and `Escape` closes without discarding the draft.

---

## Home, waiting classes, and the lane strip

Every unlanded item is in exactly one of seven classes (`holdrules.Classes`):

| Class | Meaning |
| :--- | :--- |
| `alarm` | Held on a closed backend or unavailable quota; the operator acts on the alarm. |
| `needs you` | Blocked on an owner action: an open design question, an escalated conflict, a manual prerequisite. |
| `needs a cause` | Stopped on a fault, a red check or an unmapped hold reason that needs diagnosis. |
| `waits on work` | Held behind another item, an overlapping path fence, a document rewrite or a busy lane cap. |
| `waits on the loop` | Waiting for the drain to run a pass or settle a finished run. |
| `ready` | Eligible to dispatch on the next pass with a free lane. |
| `moving` | Executing a run, a gate or a landing step. |

Two routes produce waiting rows:

- **`GET /api/v1/why`** asks the sibling `tillr-drain why` binary, which must be the same build,
  what a planning pass decides now. The browser reads this one.
- **`GET /api/v1/waiting`** and `tillr-view waiting` project each item's latest recorded decision
  through `holdrules.Rules`, marked `class_source: "rule"`. A recorded reason with no rule maps to
  `holdrules.Unknown` (`needs a cause`), so an unmapped hold never disappears.

Home is two columns. The left groups waiting rows by class in the order above, keeping queue place
order inside a group, with a link to the full list per class. The right rail holds the lane strip
and the newest passes. The lane strip shows ordinary lanes, express lanes and document desks with
each lane's work state, item, seat and where the seat came from, backend, attempt count, run,
elapsed clock and latest action. A `settling` lane shows the last finished step and never claims a
gate is running.

---

## Items, Passes, and stable lists

Both tables hold their list stable (P7). Until the reader scrolls or focuses a row, the list
follows each snapshot. After that, visible ids and order freeze, each row's facts still update in
place, and arrivals, departures and reorderings are counted on a banner the reader clicks to
accept. Changing a filter resets the hold.

**Items** (`/items`) is the ungrouped queue ledger. `state` selects not-moving (the default),
unlanded or landed; `class` and `reason` filter; `sort=place` keeps true queue places and gaps.

**Passes** (`/passes`) is the newest-first planning ledger. Consecutive passes with identical
decisions, session, stop, phases and spend fold into one group. Folding runs over the whole loaded
list before any filter, so hiding a pass never manufactures a streak. Each group prints what
changed against the older group, its phase durations and its token spend, prefixed `≥` when a run
reported no usage.

---

## Item, Run, Lane, and the span waterfall

**Item page** (`/item/<id>`): projected state, criteria, allowed paths, the item's document with
its open question count and a Vantage link (inert with an explanation when no Vantage URL is
configured), token usage across attempts, the item's folded pass decisions, its attempts and its
landings.

**Run page** (`/run/<id>`) puts three reads on one time axis:

1. the coarse pipeline stages of the run;
2. model turns, tool calls and subprocess spans read on demand from the run's trace artifacts;
3. gate plan and check spans from `pipeline_spans`, with skipped checks as untimed rows carrying
   their skip reason.

The waterfall follows four rules:

- **Cycle-safe tree.** A malformed parent cycle is broken to roots. A span selected by URL expands
  its ancestors and clears a conflicting search, so a shared link always shows its target.
- **No invented clocks.** A span with no recorded start and finish draws no bar and says why. An
  open span's right edge advances on the viewer's clock, is marked as derived, and stops when
  paused.
- **End-time chain and uncovered time.** The chain starts at the latest-finishing root and
  descends only to children that end when their parent ends. Each span also reports its time not
  covered by its children.
- **Below the waterfall:** usage, the wall-time split (model time, active tool time, time waiting
  for a test slot, uncovered time), the drain's scheduling decisions about the run in time order,
  worker progress and child sessions, trace downloads, and tabs for transcript, exchanges, claims
  and artifacts.

**Lane page** (`/lane/<id>`): work state and lease, current or last run, item, seat, backend,
attempt, latest action with a quiet-age warning, run progress, recent steps and a live output
tail.

---

## Trunk, Performance, Check, Graph, Questions, and Designs

- **Trunk and landing.** A health banner (green or red, failing checks, automatic revert status)
  over a ledger of landings and post-landing trunk gate runs. A landing page shows the trunk
  before and after and both `gated_tree` and `landed_tree`, which legitimately differ when the
  squash replays onto a concurrent landing.
- **Performance and check.** Ranges anchor to the server's generation time. An empty bounded
  window probes all history, so "quiet in this window" differs from "nothing recorded". Tables
  cover gate checks by 95th percentile (withheld below the minimum sample count), gate wall per
  pass, phases per pass, and items by tokens. A check page lists one check's executions, each
  linked to its span.
- **Dependency graph.** Unlanded items laid out along `blocked_by` edges, colored by waiting
  class, with cycles, self-loops and satisfied or missing blockers marked.
- **Questions.** Vantage's question index, separating questions that hold queued items from other
  open or blocked questions.
- **Designs.** Designer session cache state, context size, handled comment batches and
  arrival-bucket counts.

---

## CLI faces, HTTP API, and trace export

- **CLI faces of `tillr-view`:** `status`, `events`, `heavy-misses`, `waiting`, `alarms`, `passes`,
  `lanes`, `running`, `run`, `trace`, `serve`, `restart` and `perf-log`. Each face's `--help` owns
  its flags ([`tools/tillr-view/README.md`](../../tillr-view/README.md)). The live planner
  answer is `tillr drain why`. Landings and check performance have HTTP routes and no CLI face.
- **HTTP read routes** under `/api/v1/`: `snapshot`, `why`, `waiting`, `lanes`, `passes`,
  `landings`, `landings/{id}`, `perf/checks`, `perf/items`, `checks/{id}`, `items/{id}`,
  `items/{id}/doc`, `runs/{id}` with `timeline`, `gate`, `transcript`, `exchanges`,
  `artifacts/{kind}` and `trace`, `questions`, `designs`, `admin/tables` and `ws`. The two POST
  routes exist only with `--write` ([observatory actions](observatory-actions.md)).
- **Closed namespace.** An unknown `/api/v1/` route answers 404 with `error.code` `unknown-route`
  and the server build, and the browser says the page needs a newer server.
- **Trace export.** `tillr-view trace` and `GET /api/v1/runs/{id}/trace` export OpenTelemetry JSON
  (`otlp-json`, the default) or Chrome trace-event JSON (`chrome-trace`). A span whose bounds were
  widened for layout keeps its recorded bounds and is tagged, so layout envelopes can be excluded
  from duration statistics.

---

## Provenance marks, usage, and visual tokens

### Six distinct marks for zero, missing, and derived facts

1. **Recorded zero** (`0`, `0 ms`): a measurement took place and was zero.
2. **Unrecorded** (`absent`, `not recorded`): no clock or field was written.
3. **Not applicable**: the stage or field does not apply to this item or run.
4. **Unreadable** (`corrupt`): a stored payload or artifact failed to parse or verify.
5. **Lower bound** (`≥ N`): a sum in which at least one run reported no count.
6. **Derived** (`~` prefix, `class_source: "rule"`): computed at read time from a rule table or
   the viewer's clock.

Every status badge encodes state three ways (color, symbol and word), so it stays legible
without color.

### Usage without dollar prices

Run, item and performance pages show tokens by kind (input, cache read, cache write, output,
reasoning, total), the model, the backend, time to first output and output tokens per second. An
unrecorded field is named as unrecorded, never shown as zero. No page shows or estimates a price
([OQ-OR1](#why-its-this-way)).

### Visual tokens

[`tokens.css`](../../tillr-view/web/src/tokens.css) is the stylesheet contract. The theme
follows the operating system's color-scheme preference, with no browser toggle. Semantic custom
properties cover surfaces, three ink levels, one accent, the state hues, the type scale and
spacing; data columns and clocks use tabular numerals.

---

## Scope boundaries and unbuilt routes

The observatory excludes:

- **Store or git writes from the server.** Only `--write` enables the two `tillr-item` child
  process actions.
- **Authentication.** `tillr-view serve` binds to loopback and checks loopback origins.
- **In-app Markdown rendering.** Documents stay in Vantage; the item page shows frontmatter,
  question counts and a link.
- **Dollar cost or burn-rate projections** ([Icebox](../tillr/roadmap.md#icebox)).

Not built:

- `/pass/<stamp>`, `/doc/<path>` and `/question/<id>` render the "Page unavailable" fallback in
  `App.tsx`.
- A graphical trunk timeline; `/trunk` is a table.
- Global navigation chords, a filter-focus key, a pause key and a key-sheet overlay.
- `QueueRail.tsx` and `InspectPane.tsx` are in the components directory and mounted by no page.

---

## Current values

The prose above says what each value is for; the literals live only here.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Default listen address | `127.0.0.1:4318` (`--addr`) | `serveFace` in `tools/tillr-view/serve.go` |
| Dev ports (`just observatory-dev`) | Vite on the port, Go API one above it | `tools/observatory/run.sh` |
| View grace past the next pass due | `SessionGraceMS = 5000` | `tools/tillr-view/heartbeat.go` |
| Drain heartbeat cadence | `PassInterval = 1s` | `tools/tillr-drain/drain/pass_observation.go` |
| Fallback poll interval | `FALLBACK_POLL_MS = 5000` | `tools/tillr-view/web/src/models/queries.ts` |
| WebSocket summary window | `wsWindowEvery = 10s` | `tools/tillr-view/servews.go` |
| Passes per HTTP page | `50` | `tools/tillr-view/passes.go` |
| Minimum timed samples for a 95th percentile | `5` | `tools/tillr-view/performance.go` |
| Home preview caps | `8` rows per class, `3` newest passes | `tools/tillr-view/web/src/components/Home.tsx` |
| Waterfall geometry | `HEIGHT = 420`, `ROW = 36` | `tools/tillr-view/web/src/components/SpanViewer.tsx` |
| Tertiary ink | `#626a79` light, `#949bad` dark | `tools/tillr-view/web/src/tokens.css` |

---

## Why it's this way

Each row is a decision a maintainer reading only the behavior above might undo.

| ID | Decision | Why it stays |
| :--- | :--- | :--- |
| D-OR1 | Every entity kind has its own URL and every mention renders as a link. | Hiding runs, passes, checks or landings behind query parameters on an item page leaves them without a shareable address. |
| D-OR2 | A pass is defined from `drain_passes` and `dispatches`, never from `drain_ticks`. | `drain_ticks` drops every pass where all items held, which are the passes an operator investigating a stalled queue needs. |
| D-OR3 | Recorded hold reasons are classified through one rule table, and an unmapped reason is `needs a cause`. | One leaf package shared by the drain's tests and the view keeps new hold reasons and triage classes from drifting apart. |
| D-OR4 | Six distinct marks for zero, unrecorded, not applicable, unreadable, lower bound and derived. | One dash for all of them hides instrumentation gaps and makes unrecorded spend look free. |
| D-OR5 | Terminal faces read the same Go projections the HTTP routes serve. | A browser-only projection cannot be pasted into a terminal diagnosis or tested without a DOM harness. |
| D-OR6 | Stages, trace spans and gate check spans share one waterfall on one axis. | Separate charts have incompatible scales and hide whether time went to model turns, subprocesses or gate waits. |
| D-OR7 | Designer's decision; the owner may overturn it. The run page splits wall time into model time, active tool time and test-slot wait, and lists the drain's scheduling decisions for the run in time order ([session recording](../design/session-recording.md)). | Without it a run waiting on a test slot looks identical to one executing a tool. |
| OQ-OR1 | Owner ruling, 2026-11-15. Show tokens by kind, model, backend and latency; show no dollar price anywhere. | Tokens and latency are recorded observations. A price estimate across subscriptions, cached tokens and changing rates is inaccurate. |
| OQ-OR2 | Each entity opens as its own full-width page, with row-stepping keys on list pages. Confirmed by the owner in [`OQ-PR5`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR5). | A link names one subject, and a full-width page gives the waterfall and tables room. |
| OQ-OR3 | Home groups by who must act next; `/items` stays one ungrouped table in place order. Confirmed by the owner in [`OQ-PR5`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR5). | Grouping both destroys the one visible queue order; a flat Home buries owner-blocked items. |
