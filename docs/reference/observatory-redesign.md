---
title: "Tillr observatory — linked entity pages, read faces, and truthfulness contract"
status: accepted
stage: CURRENT
verified: 2026-11-20
verified_commit: bc8dda147c84de98a239194961e0fb4b187bea79
covers:
  - tools/observatory/run.sh
  - tools/tillr-view/
  - tools/tillr-drain/holdrules/holdrules.go
  - tools/tillr-drain/stop/explanation.go
  - tools/tillr-drain/drain/pass_observation.go
  - tools/tillr-drain/drain/pipeline_observation.go
  - tools/tillr-drain/drain/spans.go
  - tools/tillr-drain/drain/timing.go
  - tools/tillr-drain/drain/dispatches.go
  - tools/tillr-run/run/progress.go
tags: [tillr, observatory, view, ui, reference]
summary: "How the tillr observatory and `tillr-view` answer operator questions across linked entity pages, CLI faces, HTTP/WebSocket routes, and trace exports: hold-reason classes, pass and lane projections, the unified run span waterfall, token and latency usage without dollar prices, and explicit provenance marks for missing or derived data."
---

# Tillr observatory — linked entity pages, read faces, and truthfulness contract

**Status:** Verified 2026-11-20 against `bc8dda147c84de98a239194961e0fb4b187bea79`.
UNMEASURED: no timed operator session is recorded; verification rests on the Go
and front-end test suites.

The **observatory** is the operator-facing read surface of tillr: a set of
CLI faces on `tillr-view` and an embedded React application served by
`tillr-view serve` (launched for a checkout via `tools/observatory/run.sh`). It
answers why work is waiting, what each lane is doing, what each planning pass
decided, where a run or gate check spent time, what reached trunk, and what
tokens and latency each run consumed — without opening SQLite for writing,
without rendering dollar prices, and without disguising missing or derived
clocks as recorded facts.

| Component | Lives in |
| :--- | :--- |
| Checkout build and launch supervisor (`just observatory`, `just observatory-dev`) | `tools/observatory/run.sh` |
| CLI dispatcher, read-only HTTP/WebSocket server, and response cursor | `tools/tillr-view` (`serveFace`, `server`, `serverSnapshot`, `apiResponse`, `apiFailure`) |
| Live and historical waiting/hold projections (`why`, `waiting`) | `tools/tillr-view` (`plannerWhy`, `assembleWaiting`) and `tools/tillr-drain/holdrules` (`Rules`, `Classes`, `Unknown`) |
| Loop heartbeat, stop explanation, pass ledger, lanes, landings, performance, and trace export | `tools/tillr-view` (`readLoopStatus`, `readPasses`, `readLanes`, `readLandings`, `readPerfChecks`, `exportTrace`) and `tools/tillr-drain/stop` (`Explain`) |
| Pass, span, dispatch-seat, and worker/child-session producers | `tools/tillr-drain/drain` (`PassObservation`, `PipelineSpan`, `DispatchRecord`) and `tools/tillr-run/run` (`ProgressWriter`, `ChildSessionRecord`) |
| Dollar-stripping guard on read payloads | `tools/tillr-view` (`stripCost`) |
| Shell, hash router, live cache, entity links, and provenance marks | `tools/tillr-view/web/src` (`App`, `ObservatoryShell`, `EntityLink`, `StateGlyph`, `EpistemicBadge`, `queryKeys`) |
| Home, Items, Passes, Graph, Trunk, Performance, Check, Item, Run, Lane, Questions, and Designs pages | `tools/tillr-view/web/src/components` (`Home`, `ItemsView`, `PassesView`, `DependencyGraph`, `TrunkView`, `PerformanceView`, `CheckView`, `ItemPage`, `RunPage`, `SpanViewer`, `LanePage`, `QuestionsView`, `DesignsPage`, `Usage`) |

**Reads with:** [observatory actions](./observatory-actions.md) (1-based queue
places, drag reordering, verbatim `idea` intake, and `--write` HTTP mutations),
[tillr item states](./tillr-item-states.md) (projected item states and frozen
criteria), [observatory truthfulness and shared primitives](./observatory-design.md)
(the provenance rules, shared glyph/refusal/document components, and earlier
four-region board whose pane layout this system replaced), [the observatory recording contract](../design/tillr-observatory.md)
(the store and artifact schemas behind these reads), and
[the decision provenance review](../tillr/decision-provenance-review.md) (the
2026-11-20 owner ruling [`OQ-PR5`](../tillr/decision-provenance-review.md#OQ-PR5)
confirming the linked-page layout and Home grouping as the baseline).

---

## Principles and invariants

The observatory's design principles (`P1.`–`P7.`) govern every CLI face, HTTP
route, and browser component:

- **P1. Every screen answers an operator question, and the home screen answers "what needs me?"** A page is organized around who must act next (`needs you`, `needs a cause`, `waits on work`, `waits on the loop`, `ready`, `moving`), not around which database table stores the row.
- **P2. Every entity has one URL, and every mention is a link.** An item, pass, run, lane, check, landing, document, question, or design has a canonical hash address; rendering an identifier as plain text strands the reader.
- **P3. Provenance is explicit: zero, unrecorded, not applicable, unreadable, lower bound, and derived never share a mark.** A missing clock or unreported token count is named as missing or bounded (`not recorded`, `absent`, `≥`), and a viewer-computed elapsed clock is marked as the viewer's clock rather than a stored measurement.
- **P4. One run's timing lives on one time axis.** Stages, model turns, tool calls, gate plans, and gate checks share a single span waterfall on the Run page rather than separate charts with separate horizontal scales.
- **P5. Repeated identical observations fold; transitions stand out.** Consecutive planning passes with the same canonical outcome and consecutive item decisions with the same action and reason collapse into one streak row naming the count and earliest timestamp, without discarding any underlying row.
- **P6. Both read faces are primary.** Any summary or table the browser leads with (`why`, `waiting`, `passes`, `lanes`, `landings`, `perf`, `trace`, `status`) is answered by a `tillr-view` CLI subcommand over the same Go projection, so terminal checks and automated tests never depend on scraping HTML.
- **P7. A table being read does not move under the cursor.** Scrolling or focusing a row pins visible list membership and order while existing rows update their facts in place; arrivals, removals, and external reorderings wait behind an explicit banner until accepted.

Four structural invariants enforce those rules in code:

- **Read-only store handle (`server.store` in `tools/tillr-view`).** `tillr-view` opens SQLite read-only once for the life of the server and never writes to the database or repository. Even when started with `--write`, the server delegates queue placement and idea intake to `tillr-item` as a serialized child process ([observatory actions](./observatory-actions.md#browser-writes)).
- **Cursor on every HTTP response (`serverSnapshot` on `apiResponse` and `apiFailure` in `tools/tillr-view`).** Every `/api/v1/` response — success or refusal — carries `store_path`, `schema_version`, `event_head_seq`, `generated_at`, `build` (`revision`, `digest`, `dirty`), `writes_enabled`, and optional `order_recording` and `vantage_base_url`, so the browser can detect build skew and attribute every panel to the cursor it read.
- **Server-clock anchoring and honest pause (`useRead` in `src/models/queries.ts`, `PerformanceView` in `src/components/PerformanceView.tsx`).** Bounded time windows on Performance anchor exclusively to `Date.parse(snapshot.generated_at)` so client clock skew never drops or invents rows. Pausing in the shell (`paused = true`) halts both WebSocket cache invalidations and fallback polling (`FALLBACK_POLL_MS`), so the age shown in the header never ticks over a silently refreshed query.
- **Dollar-free read payloads (`stripCost` in `tools/tillr-view`, `Usage.tsx` in `src/components`).** Read routes strip `usage_cost_usd` before serialization and browser components render tokens, models, backends, and latencies only; dollar pricing remains out of the UI until per-backend pricing is accurate.

> [!WARNING]
> Do not read `drain_ticks` to list planning passes. `drain_ticks` records only passes that entered the landing gate and omits every pass where all items held; pass faces read `drain_passes` joined with `dispatches` grouped by `dispatched_at` (`readPasses` in `tools/tillr-view`).

> [!WARNING]
> Do not add an exported `Reason` constant in `tools/tillr-drain/drain` without adding its display rule to `holdrules.Rules` in `tools/tillr-drain/holdrules`. `TestEveryReasonConstantHasAHoldRule` fails the build when a reason has no mapped class, stage, and mover.

---

## Entities and URLs

A **pass** is one planning round of the drain loop, identified by its UTC second-or-millisecond dispatch stamp (`YYYYMMDDTHHMMSSZ`, or `.mmmZ` when sub-second precision is present; `passStamp` in `src/components/Home.tsx` and `parsePassStamp` in `tools/tillr-view`). A **waiting class** is the operator-facing bucket that says who can move an item next (`holdrules.Classes`). A **work state** on a lane (`idle`, `running`, or `settling`) distinguishes active worker execution from a lane lease that still wraps a finished run during post-run settlement (`readLanes` in `tools/tillr-view`).

Navigation state lives entirely in `window.location.hash` (`src/models/url.ts`, `useScreenHash` in `src/components/ObservatoryShell.tsx`):

| Surface | Hash route | Component / status |
| :--- | :--- | :--- |
| Home triage and lane strip | `#/` | `Home` (`src/components/Home.tsx`) |
| Queue and waiting table | `#/items` (`?state=`, `?sort=`, `?class=`, `?reason=`) | `ItemsView` (`src/components/ItemsPasses.tsx`) |
| Queue dependency DAG | `#/graph` (`?item=<id>`) | `DependencyGraph` (`src/components/DependencyGraph.tsx`) |
| Pass ledger | `#/passes` (`?range=`, `?item=`, `?action=`, `?session=`, `?holds=hide`) | `PassesView` (`src/components/ItemsPasses.tsx`) |
| Check and item performance | `#/performance` (`?range=24h\|7d\|30d\|all`) | `PerformanceView` (`src/components/PerformanceView.tsx`) |
| Trunk health and landings | `#/trunk` (`?outcome=`, `?item=`, `?range=`, `?superseded=show`) | `TrunkView` (`src/components/TrunkView.tsx`) |
| Open and blocked questions | `#/questions` | `QuestionsView` (`src/components/QuestionsView.tsx`) |
| Designer session warmth | `#/designs`, `#/design/<path>` | `DesignsPage` (`src/components/Designers.tsx`) |
| Single item | `#/item/<id>` | `ItemPage` (`src/components/ItemRunPages.tsx`) |
| Single run and span waterfall | `#/run/<id>` (`?span=`, `?tab=`, `?artifact=`, `?exchange=`, `?q=`) | `RunPage` + `SpanViewer` (`src/components/ItemRunPages.tsx`, `SpanViewer.tsx`) |
| Single lane | `#/lane/<id>` | `LanePage` (`src/components/LanePage.tsx`) |
| Single gate check | `#/check/<id>` (`?span=`, `?run=`) | `CheckView` (`src/components/PerformanceView.tsx`) |
| Single landing | `#/landing/<id>` | `LandingPage` (`src/components/TrunkView.tsx`) |

Legacy query-only URLs redirect cleanly in `App.tsx`: `#/?item=<id>` redirects to `#/item/<id>`, and `#/item/<id>?run=<run>` replaces the history entry with `#/run/<run>` while preserving `tab`, `artifact`, and `exchange` parameters.

Two shared components turn entity names into links across every screen:

- `EntityLink` (`src/components/EntityLink.tsx`) maps `(kind, id)` (`item`, `run`, `lane`, `pass`, `check`, `landing`, `doc`, `question`) to its canonical hash URL and optional state glyph.
- `EntityText` (`src/components/Home.tsx`) tokenizes free-text reasons, movers, and diff summaries against known item IDs, run IDs, lane IDs, pass stamps, question IDs, check IDs, and `docs/…/*.md` paths so inline identifiers link directly to their pages.

---

## Shell, live updates, and navigation

`ObservatoryShell` (`src/components/ObservatoryShell.tsx`) wraps every page in a fixed two-band chrome. An active backend alarm appears above both bands, before item rows or counts. The snapshot names the backend or judge that cannot run, the time in Eastern, the recorded cause, the items and lanes behind it, and the evidence and operator action that clear it. The same alarm block leads `tillr view status --text`, `tillr drain why`, and `tillr view waiting`. Alarms report a condition; they do not stop the loop or move work to another backend.

The two bands are:

1. **Top bar:** Holds the nine `NAV` links (`Home`, `Items`, `Graph`, `Passes`, `Runs`, `Performance`, `Trunk`, `Questions`, `Designs`), build-freshness warnings when the served checkout's `HEAD` has moved past the running binary (`checkoutWatch` in `tools/tillr-view`) or when `PAGE_BUILD` disagrees with `snapshot.build`, the connection/freshness indicator (`Live` over `/api/v1/ws`, fallback polling age when disconnected, or `Paused`), `Pause` / `Resume` and manual `Refresh` buttons, the opt-in `New idea` button when `snapshot.writes_enabled` is true, and a collapsible `Glossary` (`Item`, `Idea`, `Lane`, `Express lane`, `Run`, `Attempt`, `Gate`, `Trunk`).
2. **Loop band (`LoopBand`):** Pinned directly beneath the top bar so it never scrolls out of view. Reads `drain_summary` and `loop_status` (`readLoopStatus` in `tools/tillr-view/heartbeat.go`):
   - **Running:** When `drain_sessions` has an unfinished session whose `next_pass_at` is within `SessionGraceMS`, displays `Running`, session ID, last pass timestamp, next pass due (or `Overdue` once past `next_pass_at + SessionGraceMS`), and separate running counts for ordinary and express lanes (`work_state === "running"`, excluding document desks and settling leases).
   - **Stopped:** Displays `Stopped` alongside the structured `stop.Explanation` (`Explain` in `tools/tillr-drain/stop/explanation.go`), which preserves the recorded stop reason, timestamp, item ID, step, plain-language `sentence`, and operator recovery `next` action rather than inventing advice from a bare code.
   - **Not known:** When `drain_sessions` has no rows or an older schema lacks session tracking, displays `Not known` rather than reporting a running loop as stopped.

Live invalidation runs over `GET /api/v1/ws` (`serveWS` in `tools/tillr-view/servews.go`, `src/models/socket.ts`), validating loopback `Origin` headers and emitting resource invalidation frames mapped to `queryKeys` (`src/models/queries.ts`). Run keys split `queryKeys.run` (`["runs", id, "run"]`) from `queryKeys.runTranscript` (`["runs", id, "run", "transcript"]`) under `queryKeys.runSubtree` (`["runs", id]`): a growing worker log emits `runlog:<id>`, invalidating only the transcript key rather than re-reading exchanges, timeline, gate, and artifacts on every log line. `useLanesQuery` continues polling on a timer even while the WebSocket is live (faster while `#/lane/<id>` is open) because step files in `.tillr/loop/runs/<run>/steps/` and quiet-age counters advance without SQLite store events.

Browser keyboard controls are scoped to the surfaces that mount them:

- **Items and Passes tables (`useRowKeys` in `src/components/ItemsPasses.tsx`):** `j` / `k` move row focus down or up, `Enter` opens the focused row's entity URL, `[` / `]` step to the previous or next row and open its entity URL, `u` navigates to `#/`, and `J` / `K` reorder the focused item one visible row down or up when `--write` is enabled and `sort=place`.
- **Run span waterfall (`SpanViewer` in `src/components/SpanViewer.tsx`, when `.span-axis` has focus):** `+` or `=` zooms in, `-` zooms out, `ArrowLeft` / `ArrowRight` pans across the window, and `f` resets to the recorded span bounds (alongside mouse-wheel zoom, `Shift`+wheel or horizontal-wheel pan, and click-drag pan).
- **Idea intake (`NewIdea` in `src/components/NewIdea.tsx`, when `writes_enabled` is true):** `n` outside form inputs opens the `New idea` dialog, `Ctrl+Enter` / `Cmd+Enter` submits, and `Escape` closes without discarding the `localStorage` draft.

---

## Home, waiting classes, and the lane strip

Every unlanded item is classified into one of seven action classes (`holdrules.Classes` in `tools/tillr-drain/holdrules`):

| Class | Meaning |
| :--- | :--- |
| `alarm` | Held on a closed backend or unavailable quota; the operator acts on the backend alarm. |
| `needs you` | Blocked on an owner action (an open design question, an owner-escalated conflict, a manual prerequisite). |
| `needs a cause` | Stopped on a fault, red check, unmapped hold reason, or missing explanation that requires diagnosis or repair. |
| `waits on work` | Held behind another item (`blocked_by`, overlapping path fence, document rewrite, active designer comment batch, or busy lane cap). |
| `waits on the loop` | Waiting for the drain loop to run a pass or settle a finished run. |
| `ready` | Eligible to dispatch on the next pass with an available lane. |
| `moving` | Currently executing a worker run, gate, or landing step. |

Two server routes produce `WaitingRow` records, and the browser's `fetchWaiting` (`src/models/api.ts`) reads `/api/v1/why`:

- **Live planner diagnosis (`GET /api/v1/why`, `tillr-view why` in `tools/tillr-view/why.go`):** Invokes the sibling `tillr-drain why` binary (`siblings.go`, requiring a matching build) against the read-only store, configured policy, offered lanes, and cached Vantage index (`vantageCache`), returning what a live planning pass decides right now (`class_source: "store"` or planner-supplied).
- **Recorded history projection (`GET /api/v1/waiting`, `tillr-view waiting` in `tools/tillr-view/waiting.go`):** Projects each active item's latest recorded dispatch/hold decision through `holdrules.Rules` (`class_source: "rule"`). Any recorded reason absent from `holdrules.Rules` maps to `holdrules.Unknown` (`Class: "needs a cause"`, `Where: "queue"`, `Mover: "add a history rule for this recorded reason"`) so an unmapped hold never vanishes silently.

`Home` (`src/components/Home.tsx`) renders a two-column triage workspace:

- **Left column (grouped triage):** Partitions rows from `fetchWaiting` into the seven classes in priority order (`alarm`, `needs you`, `needs a cause`, `waits on work`, `waits on the loop`, `ready`, `moving`), preserving 1-based queue `place` order inside each group, showing up to the per-group preview cap, and linking `see all N →` to `#/items?state=unlanded&class=<class>`.
- **Right rail (`LaneStrip` and `Last passes`):**
  - **Every lane's current work (`LaneStrip` in `src/components/LaneStrip.tsx`, backed by `GET /api/v1/lanes` and `tillr-view lanes` in `tools/tillr-view/lanes.go`):** Displays ordinary lanes, express lanes (`express`), and document desks (`desk`), computing header and strip counts from `work_state`: `running` (live run), `settling` (a held lane lease whose run row is already terminal, rendered as `after <step> ~<elapsed>` using the last finished step clock rather than claiming a gate is running), and `idle` (retaining the lane's last run and outcome). Each row shows its item link, resolved seat and `seat_source` (`dispatch` from `dispatches.seat`, `item` from the item definition, or `none` rendered as `"no seat asked (policy default)"`), backend, 1-based attempt count across distinct runs, run link, state-start elapsed clock, and latest recorded action.
  - **Last passes:** Summarizes the newest passes from `GET /api/v1/passes` with dispatch/hold counts and non-hold decision links. (Landings live on `#/trunk` rather than a separate Home card.)

---

## Items, Passes, and stable lists

Both `#/items` and `#/passes` wrap their rows in `useStableList` (`src/components/ItemsPasses.tsx`), keyed by the active filter tuple:

- Until the reader scrolls or focuses a row, list membership and order follow new server snapshots immediately.
- Once scrolled or focused (`held = true`), visible row IDs and their order freeze. Incoming snapshots still update each visible row's columns (`class`, `why`, `mover`, `since`, `latest_run`) in place, while newly arrived or departed IDs increment `N newer ↑` / `N updates ↑` and external reorderings surface `order changed ↑`. Clicking the banner accepts the latest list and scrolls back to the top; changing any filter resets the scope automatically.

### Items

`ItemsView` (`#/items`, `src/components/ItemsPasses.tsx`) renders the ungrouped queue ledger:

- **Presets and filters:** URL parameter `state` selects `not-moving` (default; hides `ready` and `moving`), `unlanded` (all rows from `/api/v1/why`), or `landed` (projected from `snapshot.items` where `projected_state.state === "landed"`). Additional URL parameters filter by `class` and verbatim `reason` (`why`), and sort by `sort=place` (default, preserving true 1-based places and gaps across the full queue) or `sort=item`.
- **Columns and actions:** Renders `#` (`place`), `Item` (with an `idea` chip and title for saved ideas, or observation/document subject), `Class` (`ClassState`), `Where`, `Why` (including any `graph_changes`), `Mover` (`EntityText`), `Since`, `Last run`, and — when `writes_enabled` is true — `Order` (`↕` drag handle and `Move to top`, documented in [observatory actions](./observatory-actions.md#browser-writes)).

### Passes

`PassesView` (`#/passes`, `src/components/ItemsPasses.tsx`, backed by `GET /api/v1/passes` and `fetchPassHistory`) renders the newest-first planning ledger:

- **Streak folding before filtering (`foldPasses`):** Consecutive passes whose sorted decision tuples `(item_id, place, action, reason, lane_id, run_id)`, `session_id`, `stop`, `detail`, `phases`, and spend totals are identical fold into a single group (`×N passes since <oldest>, same answer`). Folding runs over the complete loaded pass list *before* applying `item`, `action`, `session`, `range`, or `holds=hide` filters so hiding an intervening pass never manufactures a false streak.
- **Change line, phases, and loop spend:** Each group prints `changed(...)` against the immediately older group (listing per-item `action · reason` transitions), phase durations from `drain_passes` (`phases`), and `PassSpend` (`spend_started_in_loop` and `spend_carried_over`, prefixing `≥ ` whenever `tokens_unreported > 0` indicates runs with unrecorded usage).

---

## Item, Run, Lane, and the span waterfall

### Item page

`ItemPage` (`#/item/<id>`, `src/components/ItemRunPages.tsx`) combines `GET /api/v1/items/{id}`, `GET /api/v1/items/{id}/doc`, `GET /api/v1/passes`, and `GET /api/v1/landings?item=<id>`:

- Mounts `ItemDetailPane` (`src/components/ItemDetailPane.tsx`) for projected item state ([`tillr-item-states.md`](./tillr-item-states.md)), frozen/live acceptance criteria, allowed-path fence, `DocumentPane` (frontmatter, open question count, and configured `vantage_base_url` link — drawn inert with an explanation when unconfigured), and verbatim idea intake words plus `from` outcomes when `data.idea` is present.
- Renders `ItemUsage` (`src/components/Usage.tsx`) summing token kinds across recorded attempts, `FoldedDecisions` (`foldItemDecisions`, collapsing consecutive passes with the same `(action, reason)` for this item and linking to `#/passes?item=<id>`), the attempts table linking each `#/run/<run_id>`, and recorded landings linking to `#/landing/<landing_id>`.

### Run page and the span waterfall

`RunPage` (`#/run/<id>`, `src/components/ItemRunPages.tsx`) and `SpanViewer` (`src/components/SpanViewer.tsx`, `src/models/spans.ts`) unify three read routes onto one shared horizontal time axis:

1. **Coarse pipeline stages (`GET /api/v1/runs/{id}`):** Seven stage slots (`stage`, `run`, `boundary`, `fence`, `gate`, `land`, `verdict`), carrying `runs.finish_reason` (`run/exec.go`) and stage epistemic states.
2. **Trace observations (`GET /api/v1/runs/{id}/timeline`):** Model turns (`llm_turn`), tool calls (`tool_call`), and subprocess spans read on demand from run artifacts by `tillr-trace run <run-id>`.
3. **Gate plan and check spans (`GET /api/v1/runs/{id}/gate`, recorded in `pipeline_spans` by `drain/pipeline_observation.go` and `drain/spans.go`):** Replaces the coarse `${runID}/gate` root with the stored `gate.plan_span` (keeping `${runID}/gate` in `aliases` so deep links still resolve), attaches check execution spans under their plan or parent span, and appends `skipped_checks` as untimed `absent` rows carrying their skip `reason`.

`analyzeSpans` (`src/models/spans.ts`) and `SpanViewer` enforce four rules over that combined tree:

- **Cycle-safe hierarchy and virtualized tree:** Malformed parent cycles are broken deterministically to roots; rows render in a fixed-height virtualized tree viewport (`role="tree"`) with expand/collapse toggles, text search (`?q=`), and URL-bound selection (`?span=`). Selecting a span via URL automatically expands collapsed ancestors and clears a conflicting `?q=` filter so shared deep links always reveal their target.
- **No invented clocks:** A span lacking valid `started_at_ms` / `finished_at_ms` draws no bar and renders `.waterfall__gap-notice` stating its reason (`no clock recorded — not drawn`, or a skipped check's skip reason). An open running span (`open === true`) advances its right edge on a periodic browser timer (`data-clock="viewer"`, styled `derived_clock` and titled `"so far — viewer's clock; finish not recorded"`), stopping immediately when `paused` is true. Initial axis fitting waits until `initialComplete` settles and never overrides a user zoom or pan (`touched`).
- **End-time chain (`★`) and uncovered self-time:** `analyzeSpans` marks `critical` starting at the latest-finishing clocked root and descending only to children whose `end` equals their parent's `end` (earlier children had spare slack before that finish). It also computes each span's uncovered time (`duration` minus the union of direct children's intervals clipped to the parent) and lists the top uncovered-time spans beside the selected span's attributes and `EntityLink` pills. For `llm_turn` and `tool_call` spans, the detail panel links to `?span=<id>&tab=transcript` while noting that the timeline route records no per-span transcript entry ID.
- **Usage, worker records, trace downloads, and inspect tabs:** Below the waterfall, `RunPage` renders `RunUsage`, `WorkerRecords` (`worker_progress` milestones and nested `child_sessions` written by `tools/tillr-run/run/progress.go` and read by `tools/tillr-view/worker_records.go`), download links for `OTLP JSON` and `Chrome trace JSON`, and secondary tabs for bounded `Transcript` pages, model `Exchanges`, verification `Claims`, and run `Artifacts`.

### Lane page

`LanePage` (`#/lane/<id>`, `src/components/LanePage.tsx`) inspects one lane from `GET /api/v1/lanes`, `GET /api/v1/runs/{id}`, and `GET /api/v1/runs/{id}/transcript`: work state and lease state, current or last run, item, resolved seat (`seat_source`), backend, attempt number, latest action with quiet-age warning, `Run progress` and nested `Child sessions`, `Recent steps` read from `.tillr/loop/runs/<run>/steps/`, and a live output tail refreshed while active unless paused.

---

## Trunk, Performance, Check, Graph, Questions, and Designs

- **Trunk and Landing pages (`#/trunk`, `#/landing/<id>` in `src/components/TrunkView.tsx`, backed by `GET /api/v1/landings`, `GET /api/v1/landings/{id}`, and `tillr-view landings` in `tools/tillr-view/landings.go`):** Displays a `Trunk health` banner (`green` vs `red` from `trunk_status`, failing check links, and `auto_revert` status), followed by a chronological ledger merging `landings` rows (`landed`, `superseded`, `conflict`, `reverted`) with post-landing `trunk_gates` runs. Supports `outcome`, `item`, `range`, and `superseded=show` filters. `LandingPage` shows `trunk_before` → `landed_sha`, `gated_tree` vs `landed_tree` (which legitimately differ when `Trunk.Squash` replays onto a concurrent landing), run and item links, and any conflict paths.
- **Performance and Check pages (`#/performance`, `#/check/<id>` in `src/components/PerformanceView.tsx`, backed by `GET /api/v1/perf/checks`, `GET /api/v1/perf/items`, `GET /api/v1/checks/{id}`, and `tillr-view perf` in `tools/tillr-view/performance.go`):**
  - Anchors `range` (`24h` default, `7d`, `30d`, `all`) to `snapshot.generated_at` (`[since, end)`). When a bounded window is empty, `EmptyRange` probes `to_ms=<end>` across all history so the page distinguishes "quiet in this window, older records exist" from "nothing recorded yet."
  - Renders four tables: `Gate checks` sorted by `p95_ms` descending (`n` timed / total executions, `p50`, `p95` — suppressed as unavailable below the minimum timed-sample threshold rather than fabricating a tail percentile — `Max`, summed `Waited` from `waited_ms_json`, and `Failed`), `Gate wall per pass` from `drain_summary.gate_trend` (highlighting `▲ Over budget` and counting gated passes missing served budgets), `Phases per pass` (paging `/api/v1/passes` exhaustively via `fetchPassRange`), and `Items` sorted by `usage_tokens` descending.
  - `CheckView` (`#/check/<id>`, optional `?span=<span_id>&run=<run_id>`) lists recorded executions of one check, highlighting the selected span row (`aria-selected`) and linking each execution back to `#/run/<run_id>?span=<span_id>`.
- **Dependency graph (`#/graph` in `src/components/DependencyGraph.tsx`, `src/models/dependencyGraph.ts`):** Lays out unlanded items left-to-right across `blocked_by` edges from `snapshot.items`, coloring each node by its waiting class (`ClassState`), marking cycles and self-loops (`data-cycle`, `data-self`) and satisfied or missing external blockers (`✓` / `?`), and dimming unrelated nodes/edges when `?item=<id>` is selected to isolate everything the item waits on and everything it unblocks.
- **Questions (`#/questions` in `src/components/QuestionsView.tsx`, `tools/tillr-view/questions.go`):** Reads Vantage's question index through the shared `vantageCache` (`vantagecache.go`), separating questions currently holding queued items (`question-open`) from unreferenced open/blocked questions, with configured Vantage links per document.
- **Designs (`#/designs`, `#/design/<path>` in `src/components/Designers.tsx`, `tools/tillr-view/designers.go`):** Reports designer session cache state (`warm` with `warm_until` vs expired), recorded context token count, handled review-comment batches, and arrival-bucket counts (`within_lifetime`, `after_lifetime`, `no_session`, `unmeasured`) used to tune `comments.refresh_hours`.

---

## CLI faces, HTTP API, and trace export

`tillr-view` exposes matching CLI subcommands and read-only `/api/v1/` routes (`tools/tillr-view/main.go`, `serve.go`):

- **CLI subcommands:** `status`, `why`, `waiting`, `passes`, `lanes`, `landings`, `perf`, `trace`, `events`, `perf-log`, and `serve` (whose `--help` flags own exact invocation syntax; see [`tools/tillr-view/README.md`](../../tools/tillr-view/README.md)).
- **HTTP read routes:** `/api/v1/snapshot`, `/api/v1/why`, `/api/v1/waiting`, `/api/v1/lanes`, `/api/v1/passes`, `/api/v1/landings`, `/api/v1/landings/{id}`, `/api/v1/perf/checks`, `/api/v1/perf/items`, `/api/v1/checks/{id}`, `/api/v1/items/{id}`, `/api/v1/items/{id}/doc`, `/api/v1/runs/{id}`, `/api/v1/runs/{id}/timeline`, `/api/v1/runs/{id}/gate`, `/api/v1/runs/{id}/transcript`, `/api/v1/runs/{id}/exchanges`, `/api/v1/runs/{id}/artifacts/{kind}`, `/api/v1/runs/{id}/trace`, `/api/v1/questions`, `/api/v1/designs`, and `/api/v1/ws` (plus `POST /api/v1/items/{id}/place` and `POST /api/v1/ideas` when `--write` is passed; see [observatory actions](./observatory-actions.md)).
- **Closed API namespace and stale-server detection (`read` in `src/models/api.ts`):** Static web assets are served at `/` and never under `/api/v1/`. An unrecognized `/api/v1/` route answers HTTP `404` with `error.code = "unknown-route"` and `snapshot.build`; the browser surfaces `"This page needs a newer server; server build: …; page build: …; restart with just observatory"` rather than failing with a generic parse error, and `RefusalState` (`src/components/RefusalState.tsx`) renders any other `apiFailure` naming its `error.code`.
- **Standard trace export (`tillr-view trace` and `GET /api/v1/runs/{id}/trace` in `tools/tillr-view/trace.go`):** Exports one `--run`, `--item`, or `--pass` to OpenTelemetry JSON (`otlp-json`, default) or Chrome trace-event JSON (`chrome-trace`, one process per run and one thread track per span with flow arrows so overlapping siblings are never misread as nested). Retains original bounds in `recorded_start_unix_nano` and `recorded_end_unix_nano`; when parent windows expand around post-agent gate spans or untimed spans borrow layout bounds, tags them with `timing_expanded`, `timing_missing: true`, and `timing_source` (`parent_envelope` or `children_envelope`) so layout envelopes can be filtered out of duration statistics. Refuses export when a run has no positive recorded clock interval anywhere.

---

## Provenance marks, usage, and visual tokens

### Six distinct marks for zero, missing, and derived facts

`EpistemicBadge` (`src/components/EpistemicBadge.tsx`), `StateGlyph` (`src/components/StateGlyph.tsx`), and the data formatters keep six states visually and textually distinct (`D-OR4`):

1. **Recorded zero (`0`, `0 ms`, `0 tokens`):** A measurement took place and its value was zero.
2. **Unrecorded (`absent` badge, `not recorded`):** No clock or field was written for a stage, span, or token counter.
3. **Not applicable (`not_applicable` badge):** The stage or field does not apply to this item or run shape.
4. **Unreadable (`corrupt` badge):** A stored payload or artifact failed to parse or verify (`unreadable_json`, hash mismatch).
5. **Lower bound (`≥ N`):** Summed token spend across a pass or item where one or more constituent runs reported no token count (`tokens_unreported > 0`).
6. **Derived (`derived` / `derived_clock` badge, `~` elapsed prefix, `class_source: "rule"`):** Computed at read time from a rule table or the viewer's wall clock rather than read as a stored fact.

Every status badge and class cell encodes state three ways — colour, symbol (`●`, `◆`, `▲`, `◌`, `✓`, `—`, `?`), and word (`StateGlyph.tsx`, `ClassState` in `EntityLink.tsx`) — so status remains legible without colour.

### Usage without dollar prices

`Usage.tsx` (`RunUsage`, `ItemUsage`, `UsageSummary` in `src/components/Usage.tsx`) renders recorded resource consumption on `#/run/<id>`, `#/item/<id>`, and `#/performance`:

- **Tokens by kind:** Input, cache-read, cache-write, output, reasoning, and total tokens (`runs.usage_tokens` and per-turn trace usage), naming unrecorded fields explicitly rather than substituting `0`.
- **Model, backend, and streaming latency:** Model identifier, backend adapter, time to first output (`first_output_ms`), and generation throughput (`output_tokens_per_second`, surfaced both in `RunUsage` and inline on `llm_turn` rows in `SpanViewer`).
- **No dollar prices ([`OQ-OR1`](#why-its-this-way), `priceless.go`):** Neither run pages, item pages, pass summaries, nor performance tables display or estimate USD cost; `stripCost` removes `usage_cost_usd` on the server.

### Visual tokens

`tokens.css` (`tools/tillr-view/web/src/tokens.css`) and `theme.ts` (`tools/tillr-view/web/src/models/theme.ts`) define the stylesheet contract:

- Theme follows the operating system's `prefers-color-scheme: dark` media query via `document.documentElement.dataset.theme` (`light` or `dark`), with no separate browser toggle to drift from platform state.
- Semantic custom properties cover surfaces (`--bg`, `--surface`, `--surface-2`, `--line`), ink hierarchy (`--ink-1`, `--ink-2`, `--ink-3`, tuned to maintain readable text contrast on every surface), accent (`--accent`), the six state hues (`--state-moving`, `--state-ready`, `--state-owner`, `--state-work`, `--state-cause`, `--state-pass`), type scale (`--t-label`, `--t-data`, `--t-body`, `--t-title`, `--t-page`), and spacing (`--s1`–`--s5`), with `font-variant-numeric: tabular-nums` on data columns and clocks.

---

## Scope boundaries and unbuilt routes

The observatory deliberately excludes:

- **Direct database or git writes from the server:** `tillr-view` never opens a writable SQLite handle or runs `git commit`; only `--write` enables the two `tillr-item` subprocess actions (`place` and `idea`) documented in [observatory actions](./observatory-actions.md).
- **Authentication or multi-user accounts:** `tillr-view serve` binds to loopback by default and relies on loopback `Origin` checks rather than login sessions.
- **In-app Markdown rendering or embedded Vantage:** Design documents stay in Vantage (or the repository); `DocumentPane` renders frontmatter metadata, question counts, and an external Vantage link ([`OQ-OBD2`](./observatory-design.md#why-its-this-way), [`OQ-OBD6`](./observatory-design.md#why-its-this-way)).
- **Dollar cost or burn-rate projections:** Excluded under [`OQ-OR1`](#why-its-this-way) until pricing is accurate across subscriptions, cached tokens, and changing model rates ([Icebox](../tillr/roadmap.md#icebox)).

Routes and controls present in link helpers or earlier sketches that are **not built** at `bc8dda147c84de98a239194961e0fb4b187bea79`:

- **Four hash routes render `App.tsx`'s `"Page unavailable"` fallback (`This build does not serve <hash> yet.`):** `#/runs` (cross-item runs list linked from `NAV`), `#/pass/<stamp>` (single-pass page linked from `EntityLink`), `#/doc/<path>` (standalone document route), and `#/question/<id>` (standalone question route; `#/questions` lists all open/blocked questions).
- **Graphical five-lane Trunk timeline:** Not built; `#/trunk` serves the chronological landings and post-landing trunk-gate table.
- **Unmounted first-design components:** `QueueRail.tsx` and `InspectPane.tsx` remain in `tools/tillr-view/web/src/components/` from the superseded three-pane layout and are not mounted by `App.tsx` (`ItemDetailPane.tsx` remains mounted inside `ItemPage`).
- **Unimplemented draft shortcuts:** Global two-key navigation chords (`g h`, `g i`, …), `/` filter focus, keyboard `p` pause, `t` timezone toggle (timestamps render as ISO-8601 UTC or relative strings), `y` / `c` copy shortcuts, and `?` key-sheet overlay are not wired in the front end.
- **Queued `#/run/<id>` wall-time split and chronological scheduling-decision ledger ([`D-OR7`](#why-its-this-way), specified in [`D-SR1`](../design/session-recording.md#44-worker-test-slot-wait-recording-run-wall-time-split-and-chronological-scheduling-decisions-d-sr1)):** Not yet built on the trunk at `ba0fe8f64` (while `9f468ca23` Claude transcript entries/closing notice and `57fb8867f` per-run `seat` / `workflow` / `node` display are already built and kept intact). When built on top of the trunk across `tools/go-test-shards.py`, `tests/go-test-shards.test.py`, `tools/tillr-view/`, and its `just pack-sync` mirror, `go-test-shards.py` appends fail-open `slot.wait` spans to `$TILLR_TRACE_DIR/slot-waits.jsonl` and `RunPage` / `GET /api/v1/runs/{id}` render the per-run split of wall time into model time, active tool time, and unioned test-slot wait time alongside the drain's time-ordered scheduling decisions for that run (allocation, gate start and end, refusal, retry).

---

## Current values

Verified at `bc8dda147c84de98a239194961e0fb4b187bea79`. The prose above explains what each setting and bound does; literal values live only here.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Default `tillr-view serve` listen address | `127.0.0.1:4318` (`--addr`) | `serveFace` in `tools/tillr-view/serve.go`; `Justfile` `observatory` |
| Dev supervisor ports (`just observatory-dev`) | Vite on `<port>` (`4318`), Go API on `<port + 1>` (`TILLR_VIEW_API`) | `tools/observatory/run.sh`, `tools/tillr-view/web/vite.config.ts` |
| Default loop and lane paths | `--loop-dir <store-dir>/../loop`, `--lane-root <store-dir>/lanes` | `serveFace` in `tools/tillr-view/serve.go` |
| Vantage base URL and perf-log env vars | `$TILLR_VANTAGE_BASE_URL` (`--vantage-url`), `$TILLR_VIEW_PERF_LOG` (`--perf-log`), `$TILLR_VIEW_PERF_STARTUP` (`--perf-startup`) | `serveFace` in `tools/tillr-view/serve.go` |
| Drain heartbeat cadence and view grace window | `PassInterval = 1s`, `SessionGraceMS = 5000` (`5s` past `next_pass_at`) | `tools/tillr-drain/drain/pass_observation.go`; `tools/tillr-view/heartbeat.go` |
| Disconnected HTTP fallback poll interval | `FALLBACK_POLL_MS = 5000` (`5s`); `1000` (`1s`) for `useLanesQuery` when a lane page is open | `tools/tillr-view/web/src/models/queries.ts` |
| WebSocket summary window cadence | `wsWindowEvery = 10s` | `tools/tillr-view/servews.go` |
| `GET /api/v1/passes` page size | `50` passes per response (`before` exclusive, `since` inclusive) | `passBounds` in `tools/tillr-view/passes.go`; `fetchPassHistory` in `src/models/api.ts` |
| Minimum timed samples for check `p95_ms` | `5` timed executions (`null` below `5`) | `tools/tillr-view/performance.go`; `PerformanceView.tsx` |
| Home preview caps | `8` rows per waiting class; `3` newest passes in `Last passes` | `Home` in `tools/tillr-view/web/src/components/Home.tsx` |
| Span waterfall viewport geometry and summary cap | `HEIGHT = 420` px, `ROW = 36` px, top `10` uncovered-time spans, `1000` ms open-span clock tick | `SpanViewer` in `tools/tillr-view/web/src/components/SpanViewer.tsx` |
| Front-end runtime dependencies (`package.json`) | `react`, `react-dom`, `@tanstack/react-query`, `react-markdown` | `tools/tillr-view/web/package.json` |
| Adjusted tertiary ink tokens (`--ink-3`) | `#626a79` (light), `#949bad` (dark) for ≥ 4.5:1 surface contrast | `tools/tillr-view/web/src/tokens.css` |

---

## Why it's this way

Each row preserves a decision or settled question (`D-OR1`–`D-OR7`, [`OQ-OR1`](#why-its-this-way)–[`OQ-OR3`](#why-its-this-way)) that a maintainer reading only the behavior above might otherwise undo. On 2026-11-20 the owner confirmed the linked-page layout, Home grouping, and fixed queue order as the accepted baseline in [`OQ-PR5`](../tillr/decision-provenance-review.md#OQ-PR5).

| ID | Decision | Why it stays |
| :--- | :--- | :--- |
| D-OR1 | Give every entity kind its own canonical hash URL and render every mention through `EntityLink` / `EntityText` | Hiding runs, passes, checks, or landings behind query parameters on `#/item/<id>` leaves cross-item entities without shareable addresses and forces the reader back through tables. |
| D-OR2 | Define a pass from `drain_passes` and `dispatches` grouped by `dispatched_at`, never from `drain_ticks` | `drain_ticks` records only passes that ran the gate, silently dropping every pass where all items held — the exact passes an operator investigating a stalled queue needs to read. |
| D-OR3 | Classify recorded hold reasons through one leaf rule table (`holdrules.Rules`) marked `class_source: "rule"`, routing unmapped reasons to `holdrules.Unknown` (`needs a cause`) | Keeping one leaf package shared by `tillr-drain` tests and `tillr-view` prevents silent drift between new drain hold reasons and operator-facing triage classes. |
| D-OR4 | Keep six distinct marks for recorded zero, unrecorded, not applicable, unreadable, lower bound (`≥`), and derived (`~` / viewer clock) | Collapsing nulls, skips, corrupt artifacts, and true zeros into one `"—"` hides instrumentation gaps and makes unrecorded spend or timing look free. |
| D-OR5 | Ship `why`, `waiting`, `passes`, `lanes`, `landings`, `perf`, and `trace` as CLI faces over the same Go projections that `/api/v1/` serves | Browser-only projections cannot be pasted into terminal diagnostics or verified without a DOM harness; shared Go projections keep CLI and web counts identical by construction. |
| D-OR6 | Combine run stages, `tillr-trace` timeline spans, and `pipeline_spans` gate checks into one `SpanViewer` waterfall on one time axis | Separate stage strips, gate waterfalls, and turn timelines use incompatible horizontal scales and hide whether wall time went to model turns, subprocesses, or gate waits. |
| D-OR7 | **Designer's decision; the owner may overturn this.** Split a run's wall time on `#/run/<id>` into model time, active tool time, and unioned worker test-slot wait time from fail-open `$TILLR_TRACE_DIR/slot-waits.jsonl` spans in `tools/go-test-shards.py` (with `≥` lower-bound fallback on legacy wait notices and `not recorded` when unmeasured), and list the drain's scheduling decisions for that run in chronological order *(operator-filed request, mechanism written by the design seat on 2026-11-20; not an owner's ruling; queued under [`D-SR1`](../design/session-recording.md#44-worker-test-slot-wait-recording-run-wall-time-split-and-chronological-scheduling-decisions-d-sr1))* | Without structured worker slot-wait intervals and run-scoped scheduling events on `#/run/<id>`, a long run waiting on test-slot contention looks identical to active tool execution and forces the operator to cross-read passes, dispatches, refusals, and retries by hand. |
| OQ-OR1 | Show token counts by kind, model, backend, time to first output, and output tokens/sec on runs, items, and performance, and strip dollar prices everywhere *(owner ruling, 2026-11-15)* | Token and latency facts are recorded observations needed for post-run analysis, whereas USD estimates across subscriptions, cached tokens, and shifting rates are inaccurate and stay iceboxed ([roadmap](../tillr/roadmap.md#icebox)). |
| OQ-OR2 | Open each entity (including an item) as its own full-width page rather than a three-pane split or quick-look modal, with row-stepping keys (`[` / `]`, `j` / `k`, `u`) on list pages *(settled on principle 2026-11-15; confirmed by owner in [`OQ-PR5`](../tillr/decision-provenance-review.md#OQ-PR5))* | A link can name only one subject; full-width entity pages give the span waterfall, transcript, and tables horizontal room without squeezing three columns onto one viewport. |
| OQ-OR3 | Group Home by who must act next (`needs you` first) in place order while keeping `#/items` as one ungrouped queue table in curated `place` order *(settled on principle 2026-11-15; confirmed by owner in [`OQ-PR5`](../tillr/decision-provenance-review.md#OQ-PR5))* | Grouping both screens destroys a single visible queue order to drag and audit, while flat rank order on Home buries owner-blocked items beneath higher-placed items waiting on work. |
