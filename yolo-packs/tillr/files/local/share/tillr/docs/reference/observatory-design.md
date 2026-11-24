---
title: "Observatory truthfulness, provenance, and shared primitives"
status: accepted
stage: CURRENT
verified: 2026-11-20
verified_commit: 11dec7eb2e0e196e3a9588c354616bea6f5cb1b1
covers:
  - tools/tillr-view/apierror.go
  - tools/tillr-view/counts.go
  - tools/tillr-view/frontmatter.go
  - tools/tillr-view/questions.go
  - tools/tillr-view/serve.go
  - tools/tillr-view/serveobs.go
  - tools/tillr-view/serveread.go
  - tools/tillr-view/servews.go
  - tools/tillr-view/status.go
  - tools/tillr-view/vantagecache.go
  - tools/tillr-view/web/src/components/ClaimAuditBanner.tsx
  - tools/tillr-view/web/src/components/DocumentPane.tsx
  - tools/tillr-view/web/src/components/EpistemicBadge.tsx
  - tools/tillr-view/web/src/components/FinishedBand.tsx
  - tools/tillr-view/web/src/components/InspectPane.tsx
  - tools/tillr-view/web/src/components/InterToolExchangeView.tsx
  - tools/tillr-view/web/src/components/ItemDetailPane.tsx
  - tools/tillr-view/web/src/components/ItemWithoutRun.tsx
  - tools/tillr-view/web/src/components/NowBand.tsx
  - tools/tillr-view/web/src/components/ObservatoryHeaderBar.tsx
  - tools/tillr-view/web/src/components/PayloadInspectorDrawer.tsx
  - tools/tillr-view/web/src/components/QueueRail.tsx
  - tools/tillr-view/web/src/components/RefusalState.tsx
  - tools/tillr-view/web/src/components/StateGlyph.tsx
  - tools/tillr-view/web/src/models/board.ts
  - tools/tillr-view/web/src/models/document.ts
  - tools/tillr-view/web/src/models/now.ts
  - tools/tillr-view/web/src/models/queries.ts
  - tools/tillr-view/web/src/models/socket.ts
tags: [tillr, observatory, view, provenance, truthfulness, reference]
summary: "How `tillr-view` enforces explicit epistemic provenance, three-way state encoding, named-absence refusal and detail contracts, design-document metadata and Vantage links, CLI/HTTP count parity, and WebSocket cache invalidation across the observatory, plus what the linked-page redesign superseded."
---

# Observatory truthfulness, provenance, and shared primitives

> **Delivered copy.** This is `docs/reference/observatory-design.md` from this repository at commit `4b6fdf35e8` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/observatory-design.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

`tillr-view` serves the read-only truthfulness layer and shared UI primitives
that every observatory page and CLI summary builds on. Its job is to make sure a
human or agent inspecting the drain can tell an observed measurement from a
derived clock, an unrecorded seam, a corrupt artifact, or a named absence —
without conflating missing data with zero, encoding state by colour alone, or
drifting between CLI and HTTP counts.

**Status.** Verified on `2026-11-20` against `HEAD` at
`11dec7eb2e0e196e3a9588c354616bea6f5cb1b1`. Every truthfulness primitive,
shared component, count helper, Vantage index reader, and WebSocket invalidation
rule documented here is built and covered by automated Go and Vitest suites. The
original four-region viewport-locked board layout was superseded by the
linked-page architecture in [observatory redesign](observatory-redesign.md)
(confirmed by the owner in [`OQ-PR5`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR5)),
while its provenance contract and shared components remain live inside the
linked pages. `UNMEASURED`: no timed operator session on the observatory has
been recorded yet, so code and tests establish implementation behavior rather
than measured operator-triage times.

**Component map:**

| Responsibility | Primary files |
| :--- | :--- |
| CLI/HTTP queue counts and `closed`-blocker semantics | `tools/tillr-view/counts.go`, `status.go`, `serve.go` (`boardCountsOf`, `unmetBlocker`, `boardCounts`) |
| Structured API refusals and error rendering | `tools/tillr-view/apierror.go` (`apiCodes`), `web/src/components/RefusalState.tsx` |
| Epistemic provenance states and three-way state glyphs | `tools/tillr-view/serveread.go`, `serveobs.go`, `web/src/components/EpistemicBadge.tsx`, `StateGlyph.tsx`, `web/src/models/board.ts` |
| Item detail, worker claim markings, and run evidence inspection | `web/src/components/ItemDetailPane.tsx`, `ClaimAuditBanner.tsx`, `InterToolExchangeView.tsx`, `PayloadInspectorDrawer.tsx` |
| Design-document frontmatter, Vantage question index cache, and Vantage links | `tools/tillr-view/frontmatter.go`, `questions.go`, `vantagecache.go`, `web/src/components/DocumentPane.tsx`, `web/src/models/document.ts` |
| WebSocket invalidation stream, pause gate, and query-key hierarchy | `tools/tillr-view/servews.go`, `web/src/models/socket.ts`, `queries.ts`, `now.ts` |
| Retained components from the superseded four-region board | `web/src/components/ObservatoryHeaderBar.tsx`, `QueueRail.tsx`, `NowBand.tsx`, `FinishedBand.tsx`, `InspectPane.tsx`, `ItemWithoutRun.tsx` |

**Reads with:** [observatory redesign](observatory-redesign.md) (the active
linked-page observatory that replaced this design's four-region layout while
keeping its provenance rules), [observatory actions](observatory-actions.md)
(opt-in `--write` subprocess routes for queue placement and idea intake),
[designer sessions](tillr-designer-sessions.md) (Vantage review-comment intake
and warm designer sessions), [tillr item states](tillr-item-states.md) (the
projected item state machine and criterion rules),
[the observatory recording contract](../design/tillr-observatory.md) (the store
and trace envelopes these reads consume), and
[the decision provenance review](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md)
([`OQ-PR5`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR5) confirming the
observatory baseline).

---

## Terms and non-purposes

**Terms:**

- **The board:** Without qualification in observatory and tillr documents, the observatory's queue surface served by `tillr-view serve` (originally the four-region screen of header, queue rail, item pane, and evidence pane; now the linked Home and Items pages in [observatory redesign](observatory-redesign.md)). **Never the retired work board** (`tools/board-cli` and its session extensions), which was deleted in `3f02e3b916` and `afdc9a9571`; see [board documents retirement](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/board-docs-retirement.md).
- **The wire:** The JSON shapes returned by `/api/v1/*` and emitted over `/api/v1/ws`, defined by the Go structs in `tools/tillr-view/serve.go`, `serveobs.go`, `servews.go`, and `apierror.go` and mirrored in `tools/tillr-view/web/src/models/types.ts`.
- **Truthful absence:** A field the store or filesystem did not record, rendered as a named reason (`not recorded`, `absent`, `unobserved seam`, `no description recorded`) rather than hidden, collapsed into `"—"`, or turned into `0`.

**Non-purposes:**

1. **No direct store or repository writes from `tillr-view` (`D-OBS2`, narrowed by `D-OA8`):** `tillr-view` opens SQLite strictly read-only (`store.OpenRead`). By default it registers only `GET` routes; passing `--write` adds the two `POST` routes documented in [observatory actions](observatory-actions.md), which delegate to `tillr-item` subprocesses rather than mutating SQLite from the HTTP server.
2. **Not a second Vantage ([`OQ-OBD2`](#why-its-this-way), [`OQ-OBD6`](#why-its-this-way)):** Design documents are authored, reviewed, and commented on in Vantage. `GET /api/v1/items/{id}/doc` and `DocumentPane` serve frontmatter metadata, indexed open-question counts, and an external Vantage link without shipping or rendering the document's Markdown body. (`react-markdown` remains in `web/package.json` solely for `TranscriptView.tsx` on the Run page, not for design documents.)
3. **No multi-machine aggregation or user accounts:** One `tillr-view serve` process reads one `.tillr/state.db` on loopback and relies on loopback `Origin` checks rather than login sessions.
4. **No invented timestamps or fabricated pass marks:** Untimed spans and unrecorded seams never receive interpolated wall clocks, and unverified or absent claims never render as passing checks.
5. **No dollar-cost display ([`OQ-OBD4`](#why-its-this-way), refined by [`OQ-OR1`](observatory-redesign.md#why-its-this-way)):** Token counts, model names, and streaming latency are displayed via `Usage.tsx`, while USD cost figures remain stripped (`priceless.go`) and iceboxed until pricing across subscriptions, cached tokens, and changing model rates is settled.

---

## Truthfulness and provenance contract

Every read route and component in `tillr-view` enforces seven truthfulness invariants (`D-OBS3`, `D-OBS5`, `D-OBS12`):

1. **Every derived or missing measurement carries an explicit provenance state.** `EpistemicBadge` (`web/src/components/EpistemicBadge.tsx`) renders seven distinct states mirrored from `tools/tillr-view/serveread.go` and `serveobs.go`:
   - `observed` — directly recorded timestamp, exit code, hash, or verdict (`tone: ok`).
   - `derived_clock` — duration or timestamp computed from an envelope clock, file `mtime`, or viewer wall clock (`tone: neutral`; durations and `file_mtime` timestamps also carry a leading `~` in `models/now.ts` and `InterToolExchangeView.tsx`).
   - `unobserved_seam` — a causal step or claim the emitter did not record as verified (`tone: warn`).
   - `unreadable` — a stored JSON column or on-disk artifact that failed to parse or verify (`tone: bad`; never HTTP `500` when the surrounding record is readable).
   - `absent` — a file, claim list, or optional field that does not exist on disk (`tone: neutral`).
   - `lane_copy` — an artifact read from the mutable lane worktree rather than the immutable loop archive (`tone: warn`), accompanied by `clobber_warning` in `PayloadInspectorDrawer.tsx`.
   - `no_clock` — a recorded event or exchange carrying no timestamp (`tone: neutral`).
2. **Absence is named at the point it occurs.** Empty lists and unset fields state what is missing and why (`No runs recorded for this item`, `no description recorded`, `No claim was checked for this run`) instead of disappearing or rendering blank cells.
3. **Worker progress reports are never presented as verdicts.** When a live or finished worker self-reports criterion progress before the judge runs, `ItemDetailPane.tsx` renders `~claimed` alongside `derived (worker report, not a verdict)` so self-reports cannot be mistaken for `met` verdicts.
4. **Unverified claims render as amber warnings, never quiet passes.** `ClaimAuditBanner.tsx` marks a claim `claim--verified` only when `status === "verified"` and `epistemic_state === "observed"`; any `unverified` claim or `unobserved_seam` state renders `claim--unverified`, `disproved` renders `claim--disproved`, and an empty `claim_checks` slice renders an explicit `absent` notice.
5. **Artifact misses show every path searched.** When `GET /api/v1/runs/{id}/artifacts/{kind}` (`serveobs.go`) finds no file for `brief`, `fence`, `verdict`, `run-log`, `agent-result`, `events`, or `worktree`, `PayloadInspectorDrawer.tsx` lists `searched_paths` verbatim rather than showing an empty `<pre>`.
6. **Live elapsed clocks tick only from recorded start times.** `liveElapsedText` and `leaseText` (`models/now.ts`) mark viewer-computed elapsed and lease countdown strings with `~` (or `expired` when `lease_expires_at <= nowMs`) and report `start not recorded` when `started_at` is null.
7. **Remaining stage-projection gap (`stages` in `serve.go`):** `tillr-drain` records per-run `boundary.check` events in the event log, and `SpanViewer` on the redesigned Run page reads `pipeline_spans`, yet the legacy `stages()` builder on `GET /api/v1/runs/{id}` still emits the `boundary` stage row unconditionally as `epistemic_state: "unobserved_seam"` (`detail: "boundary ran inside drain.tick; no per-run record"`).

---

## State encoding and shared refusal contract

### Three-way state encoding (`StateGlyph` and `board.ts`)

Status is never conveyed by colour alone (`D-OBS7`). `StateGlyph` (`web/src/components/StateGlyph.tsx`) renders a fixed-width symbol (`aria-hidden="true"`) paired with the visible state word and a semantic CSS modifier class across two vocabularies:

- **Projected item states (`STATE_GLYPHS` in `StateGlyph.tsx`, `STATE_LABELS` in `models/board.ts`):**
  - `working` (`●`, `working`)
  - `in_queue` (`◌`, `in queue`)
  - `blocked` (`◆`, `blocked`)
  - `not_started` (`·`, `not started`)
  - `landed` (`✓`, `landed`)
  - `resolved_elsewhere` (`—`, `resolved elsewhere`)
  - `closed` (`—`, `closed`)
  - unrecognized string fallback (`?`, raw string, `state-glyph--unknown`)
- **Waiting classes (`CLASS_GLYPHS` in `StateGlyph.tsx`, used by `EntityLink` and the linked pages):** `moving` (`●`), `ready` (`◌`), `needs-you` (`◆`), `needs-a-cause` (`▲`), `waits-on-work` (`◌`), `waits-on-the-loop` (`◌`), `done` (`✓`).

`TERMINAL_STATES` in `models/board.ts` and `terminalItemStates` in `serve.go` both classify `landed`, `resolved_elsewhere`, and `closed` as terminal queue dispositions ([`OQ-OBD5`](#why-its-this-way)). However, `unmetBlocker` in `counts.go` and `closed_blockers` on `GET /api/v1/items/{id}` treat a prerequisite item that is `closed` without landing as **still holding** its dependents: only `landed` and `resolved-elsewhere` satisfy a `blocked_by` edge, and `ItemDetailPane.tsx` surfaces `closed without landing: … (still holds this item; clear the dependency or reopen)` so the block is never silent.

### Structured API refusals (`apierror.go` and `RefusalState.tsx`)

Every non-2xx HTTP response from `tillr-view` writes `{error: {code, message}}` through `writeAPIError` (`tools/tillr-view/apierror.go`), mapping internal snake_case sentinel codes to kebab-case wire codes in `apiCodes` (`store-unavailable`, `invalid-argument`, `item-not-found`, `run-not-found`, `landing-not-found`, `doc-not-configured`, `doc-outside-repo`, `doc-not-found`, `unknown-kind`, `method-not-allowed`, `internal`, plus `unknown-route` on unmatched `/api/v1/*` paths).

`RefusalState` (`web/src/components/RefusalState.tsx`) is the single front-end renderer for any `Error` thrown by `api.ts`:

- Known codes (`DEFAULT_SENTENCES`) render a plain-language title, explanation, semantic notice tone (`notice--blocked` vs `notice--working`), and a `Try again` button only when retrying could succeed (`store-unavailable`, `internal`, network failure).
- Callers may supply `overrides` for route-specific wording (`ItemDetailPane` overrides `item-not-found`; `PayloadInspectorDrawer` overrides `unknown-kind`).
- `unknown-route` renders the build-mismatch message naming `serverBuild` and `PAGE_BUILD` and instructing the operator to restart with `just observatory`.
- Any unmapped wire `code` falls through to `The face refused: <code>` with the server's `message`, preserving the exact refusal code on screen rather than substituting a generic error banner.

---

## Item detail and design-document metadata

### `ItemDetailPane`

`ItemDetailPane` (`web/src/components/ItemDetailPane.tsx`, mounted inside `ItemPage` on `/item/<id>`) renders an item's stored definition and run list from `GET /api/v1/items/{id}`:

- **Header and metadata:** Item ID, `StateGlyph`, 1-based `place` (or `rank` fallback when terminal), `allowed_paths` fence (`none recorded` when empty), `waiting_on`, `resolves_via`, `close_reason` (`closed with no reason recorded` when `closed` has an empty reason), description (`no description recorded` when blank), and `body_ref` (`not recorded` when blank).
- **Criteria:** Lists frozen `item_criteria` in ordinal order (`met`, `unmet`, or `~claimed` when `claimed_criteria` contains an unmet criterion ID), or states `No criteria recorded — this item can land without a criterion gate.`
- **Dependencies:** Lists `blocked_by` and `blocking` item IDs as navigable buttons, plus any `closed_blockers` warning.
- **Runs:** Renders recorded runs newest-first with duration (`durationText` or ticking `liveElapsedText`), terminal reason, and gate exit code, or `No runs recorded for this item.`

### `DocumentPane` and `GET /api/v1/items/{id}/doc`

`DocumentPane` (`web/src/components/DocumentPane.tsx`, mounted inside `ItemPage`) and `documentData` (`serve.go`, `frontmatter.go`, `questions.go`, `vantagecache.go`) expose an item's design document as metadata and a hand-off link into Vantage ([`OQ-OBD2`](#why-its-this-way), [`OQ-OBD6`](#why-its-this-way)):

- **Path safety (`resolveDocument` in `questions.go`):** Resolves `items.design_doc` inside `repoRoot`, refusing empty fields (`doc-not-configured`), paths escaping `repoRoot` (`doc-outside-repo`), directories, and missing files (`doc-not-found`) with typed 4xx codes before reading bytes.
- **Metadata payload (`documentPayload` in `serve.go`):** Returns `path`, parsed YAML scalar `frontmatter` (`title`, `status`, `stage`, `next`, `updated` via `parseFrontmatter`, or `null` when absent), heading `anchor` extracted from `items.body_ref` when its path matches `design_doc` (`documentAnchor`), file size in `bytes`, `last_designer` session cache summary (`designers.go`), `open_questions`, and `questions_state`. Never returns document body prose.
- **Question counts from Vantage's index (`vantageCache` in `vantagecache.go`):** Runs `vantage.ReadIndex` (`uvx vantage-check index --format json`) behind a mutex with single-flight `prefetch` and a bounded cache window. When the index succeeds, `questions_state` is `"questions-ok"` and `open_questions` counts unanswered + blocked questions for that document; when `uvx` is unavailable or fails, `questions_state` is `"questions-unknown"` (`open_questions: null`) and `DocumentPane` renders an `unreadable` `EpistemicBadge` with `question state unknown (the Vantage index was unavailable)` rather than `0 open questions`.
- **Vantage link (`vantageLink` in `models/document.ts`):** Reads `snapshot.vantage_base_url` (populated from `--vantage-url` or `$TILLR_VANTAGE_BASE_URL`). When configured, renders `<a class="doc__link" href="<base>/#/<path>[#<anchor>]">Open in Vantage</a>` without polling or probing the Vantage server. When unconfigured (`""`), draws the control inert (`aria-disabled="true"`) with the explanation `no Vantage base URL is configured, so this link goes nowhere`.

---

## Count parity and live invalidation channel

### Shared queue counts (`counts.go`)

`boardCountsOf` (`tools/tillr-view/counts.go`) is the single count projection shared by `GET /api/v1/snapshot` (`serve.go`) and `tillr-view status --store` (`status.go`), guaranteeing CLI/HTTP count parity ([`OQ-OBD3`](#why-its-this-way)):

- Projects each item's effective state via `ProjectItemState` (`working` when a live run lease exists, `blocked` when `unmetBlocker` sees a non-`landed`, non-`resolved-elsewhere` `blocked_by` edge, otherwise stored `state`).
- Sums `working`, `in_queue`, `blocked`, `not_started`, `landed`, `resolved_elsewhere`, `closed`, and `total`.
- On `/api/v1/snapshot`, `boardCounts` embeds `statusCounts` and adds `question_holds` (non-terminal items whose `design_doc` currently has at least one unanswered question in the cached Vantage index). `models/board.ts` (`boardCounts`) reads `snapshot.counts` directly whenever present and falls back to client-side tallying only for older fixtures lacking `snapshot.counts`.

### WebSocket invalidation stream (`servews.go` and `socket.ts`)

`GET /api/v1/ws` pushes cache-key invalidation frames rather than full state payloads, keeping the HTTP `GET` routes as the single read model (`D-OBS4`):

- **Pre-upgrade baseline (`wsHandler` in `servews.go`):** Calls `detector.prime` *before* `wsUpgrader.Upgrade`. Because the browser refetches on `open`, priming first ensures no event landing between the client's open-refetch and the server's baseline is silently folded into "already seen." If priming fails, the server completes the upgrade and immediately sends a close frame carrying the truncated error reason so the browser surfaces the cause instead of an opaque handshake failure.
- **Loopback origin gate (`wsOriginAllowed`):** Accepts empty `Origin` headers (non-browser clients), `localhost`, and any loopback IP (`ip.IsLoopback()`) on any port (covering Vite's dev proxy), refusing non-loopback origins.
- **Resource prefixes (`changed` list in `invalidationFrame`):**
  - `"snapshot"` — invalidates `queryKeys.snapshot` (`["snapshot"]`) and `["why"]`, plus `["waiting"]`, `["passes"]`, `["perf"]`, `["questions"]`, and `["designs"]` (`invalidateSnapshotReaders` in `socket.ts`), and triggers throttled `["lanes"]` invalidations.
  - `"landings"` — invalidates `queryKeys.landings` (`["landings"]`).
  - `"items:<id>"` — invalidates the `["items", id]` subtree (both item detail and document metadata).
  - `"runs:<id>"` — invalidates the `["runs", id]` subtree (run summary, timeline, gate, transcript, exchanges, and artifacts), plus throttled `["lanes"]`.
  - `"runlog:<id>"` — invalidates `["runs", id]` with `exact: true`, refreshing the run's completeness and log state without refetching exchanges, timeline, or open artifacts four times a second while a log grows.
  - Unrecognized resource prefixes are ignored (`socket.ts`) so newer servers do not break older tabs.
- **Pause control (`paused` in `useObservatorySocket`):** Pausing keeps the WebSocket open (`pausedRef.current = true`), drops incoming invalidation frames, and suspends fallback polling (`queries.ts`). Resuming flips `paused` to `false` and invalidates the entire React Query cache (`client.invalidateQueries()`) to catch up in one pass ([`OQ-OBD1`](#why-its-this-way)).
- **Reconnect and fallback:** When disconnected and unpaused, `queries.ts` enables HTTP polling (`FALLBACK_POLL_MS`), while `socket.ts` reconnects with exponential backoff that resets only after a connection stays open past the stability threshold.

---

## Superseded layout and retained components

The linked-page architecture in [observatory redesign](observatory-redesign.md) replaced the original four-region viewport-locked layout (`ObservatoryHeaderBar` + `QueueRail` + `ItemPane` + `InspectPane`, plus `NowBand` and `FinishedBand`) with dedicated full-width pages routed by path (`/`, `/items`, `/item/<id>`, `/run/<id>`, `/lane/<id>`, `/passes`, `/trunk`, `/performance`, `/graph`, `/questions`, `/designs`).

At `11dec7eb2e0e196e3a9588c354616bea6f5cb1b1`, the components introduced for the original design split into two groups:

- **Mounted in the active `App.tsx` tree:** `ObservatoryShell.tsx` (top navigation, drain status, live/paused socket control), `ItemDetailPane.tsx` and `DocumentPane.tsx` (mounted inside `ItemPage`), `EpistemicBadge.tsx`, `StateGlyph.tsx`, and `RefusalState.tsx` (shared across pages), plus `ClaimAuditBanner.tsx`, `InterToolExchangeView.tsx`, and `PayloadInspectorDrawer.tsx` (mounted inside `RunPage` alongside `SpanViewer` and `TranscriptView`).
- **Retained unmounted in `tools/tillr-view/web/src/components/` with unit tests:** `ObservatoryHeaderBar.tsx`, `QueueRail.tsx`, `NowBand.tsx`, `FinishedBand.tsx`, `InspectPane.tsx`, and `ItemWithoutRun.tsx` remain in the source tree and are exercised by component unit tests under `tools/tillr-view/web/tests/` (`loop-band.test.tsx`, `observatory.test.tsx`), though `App.tsx` no longer mounts them.

---

## Current values

Verified on `2026-11-20` at `11dec7eb2e0e196e3a9588c354616bea6f5cb1b1`. Behavioral explanations live in the sections above; literal bounds and tuning constants live here.

| Setting | Value | Defined in |
| :--- | :--- | :--- |
| Vantage question-index cache TTL | `vantageMaxAge = 10 * time.Minute` | `tools/tillr-view/vantagecache.go` |
| Server WebSocket poll cadence | `wsPollInterval = 250 * time.Millisecond` | `tools/tillr-view/servews.go` |
| Server WebSocket frame write deadline | `wsWriteTimeout = 5 * time.Second` | `tools/tillr-view/servews.go` |
| Server WebSocket read/write buffer sizes | `ReadBufferSize = 1024`, `WriteBufferSize = 1024` | `wsUpgrader` in `tools/tillr-view/servews.go` |
| Server WebSocket close-reason byte cap | `120` bytes (under RFC 6455's 123-byte control-frame limit) | `closeWithReason` in `tools/tillr-view/servews.go` |
| Client WebSocket initial and max reconnect delay | `RECONNECT_FIRST_MS = 500`, `RECONNECT_MAX_MS = 10_000` | `tools/tillr-view/web/src/models/socket.ts` |
| Client WebSocket stability threshold to reset backoff | `RECONNECT_STABLE_MS = 1_000` | `tools/tillr-view/web/src/models/socket.ts` |
| Client minimum interval between WS-triggered `lanes` refetches | `LANES_REFRESH_MIN_MS = 2_000` | `tools/tillr-view/web/src/models/socket.ts` |
| Disconnected HTTP fallback poll interval | `FALLBACK_POLL_MS = 5_000` | `tools/tillr-view/web/src/models/queries.ts` |
| Live elapsed / lease ticker cadence | `1_000` ms (`useNowMs` default) | `tools/tillr-view/web/src/models/now.ts` |

---

## Why it's this way

Each row preserves a decision (`D-OBS1`–`D-OBS13`) or settled question ([`OQ-OBD1`](#why-its-this-way)–[`OQ-OBD6`](#why-its-this-way)) from the initial observatory design, recording what remains in force and what later designs amended. On 2026-11-20 the owner confirmed the observatory baseline in [`OQ-PR5`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR5).

| ID | Decision | Why it stays / how it evolved |
| :--- | :--- | :--- |
| D-OBS1 | Original four-region viewport-locked board layout (header, queue rail, item pane, evidence pane) *(superseded in layout by [observatory redesign](observatory-redesign.md))* | Superseded because squeezing queue lists, item metadata, and wide run timelines into fixed columns starved the waterfall and tables of horizontal space ([`OQ-OR2`](observatory-redesign.md#why-its-this-way)). |
| D-OBS2 | `tillr-view` opens SQLite read-only (`store.OpenRead`) and never mutates the store directly *(narrowed on HTTP surface by `D-OA8` in [observatory actions](observatory-actions.md#why-its-this-way))* | Keeps a broken or experimental UI incapable of corrupting drain state; `--write` opts in only to two `tillr-item` subprocess routes (`place` and `idea`) while `tillr-view`'s own DB handle stays read-only. |
| D-OBS3 | Attach explicit epistemic provenance (`EpistemicBadge`) to every stage, claim, exchange, and artifact | Prevents derived envelope clocks, unrecorded seams, lane-worktree copies, and unreadable JSON from masquerading as verified observations. |
| D-OBS4 | Push resource-prefix invalidations over `/api/v1/ws` (with leaf `runlog:<id>` scoping) and refetch via standard HTTP `GET` routes | Avoids maintaining a second state-serialization model on the socket and keeps growing run logs from refetching exchanges and artifacts four times a second. |
| D-OBS5 | Return HTTP `200` with `epistemic_state: "unreadable"` for corrupt field/artifact payloads and typed kebab-case `{error: {code, message}}` for route refusals | Lets healthy sibling fields on an item or run still render while giving `RefusalState` a stable machine code for true route failures. |
| D-OBS6 | Single-selection URL `#/item/<id>?run=<run_id>&tab=<tab>` *(superseded by per-entity hash URLs in `D-OR1` of [observatory redesign](observatory-redesign.md#why-its-this-way))* | Replaced by `#/item/<id>`, `#/run/<id>`, `#/lane/<id>`, `#/pass/<stamp>`, and `#/landing/<id>` so every entity has a first-class linkable URL. |
| D-OBS7 | Encode every item state and waiting class three ways — colour, word, and symbol (`StateGlyph`) | Preserves legibility under colour-vision deficiency, monochrome copies, and dense table scans. |
| D-OBS8 | Follow OS `prefers-color-scheme` with semantic CSS custom properties (`tokens.css`) and tabular numerals *(re-styled in [observatory redesign](observatory-redesign.md#visual-tokens))* | Avoids maintaining a bespoke theme-persistence toggle while keeping contrast and numeric column alignment consistent across light and dark palettes. |
| D-OBS9 | Minimal front-end dependency footprint (`react`, `react-dom`, `@tanstack/react-query`; `react-markdown` scoped to `TranscriptView.tsx`) | Avoids heavy UI/chart frameworks whose abstractions obscure missing-data states or bloat rebuilds during `just observatory`. |
| D-OBS10 | `j` / `k` keyboard row stepping (`[`, `]`, `j`, `k`, `u` on the redesigned list pages; [`OQ-OR2`](observatory-redesign.md#why-its-this-way)) | Keeps sequential queue and pass inspection fast without pointer travel. |
| D-OBS11 | Loopback-only `Origin` check on `/api/v1/ws` (`wsOriginAllowed`) | Blocks cross-origin browser pages from subscribing to local file paths, prompts, and run events while allowing `localhost` Vite dev proxying. |
| D-OBS12 | Render worker criterion claims as `~claimed · derived (worker report, not a verdict)` until the judge records a verdict | Stops a worker's self-reported completion from looking like a verified gate/judge pass while a run is live or blocked. |
| D-OBS13 | Cache `uvx vantage-check index --format json` in `vantageCache` with single-flight prefetch and explicit `questions-unknown` fallback | Keeps `/api/v1/snapshot` and `/api/v1/items/{id}/doc` fast without re-spawning Python on every request or fabricating `0 open questions` when `uvx` fails. |
| OQ-OBD1 | Client-side `Pause` keeps `/api/v1/ws` open, drops incoming invalidations while paused, and invalidates all queries on resume *(owner ruling, 2026-11-13)* | Lets an operator freeze a moving screen to read or copy evidence without touching the running drain or losing catch-up on resume. |
| OQ-OBD2 | Replace in-app design-document Markdown rendering with `DocumentPane` frontmatter metadata, question count, and an `Open in Vantage` link *(owner ruling, 2026-11-13)* | Vantage is already the review and comment tool; rendering a second un-commentable copy in the observatory duplicated Markdown parsing for no workflow gain. |
| OQ-OBD3 | Compute queue counts in one Go helper (`boardCountsOf` in `counts.go`) shared by `/api/v1/snapshot` and `tillr-view status --store` *(owner ruling, 2026-11-13)* | Eliminates off-by-one and `closed`-blocker discrepancies between terminal status checks and browser headers. |
| OQ-OBD4 | Icebox USD spend display (`2026-11-13`), later refined by [`OQ-OR1`](observatory-redesign.md#why-its-this-way) (`2026-11-15`) to show token, model, and latency usage while stripping dollar prices (`priceless.go`) | Operators need recorded token and throughput observations immediately, whereas dollar estimates remain misleading until subscription and cache pricing rules are designed. |
| OQ-OBD5 | Treat `landed`, `resolved_elsewhere`, and `closed` as terminal dispositions separated from active queue work *(owner ruling, 2026-11-13; carried into `#/items` filtering in [observatory redesign](observatory-redesign.md))* | Keeps finished tombstones from crowding active triage while keeping a `closed` (unlanded) prerequisite visible as an unmet `closed_blockers` hold on any dependent item. |
| OQ-OBD6 | Keep open questions and owner rulings canonical in Markdown design documents rather than a SQLite questions table *(designer's decision on principle, confirmed by owner in [`OQ-PR5`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR5))* | Prevents dual-source drift between git-tracked design rulings reviewed in Vantage and local `.tillr/state.db` state. |
