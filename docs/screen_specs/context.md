# Context Library — Screen Specification

## Overview

The Context Library is a searchable, filterable knowledge base embedded in the Lifecycle web viewer. Agents store context entries (source analysis, research findings, specs, notes, documentation) during their work cycles, and humans can browse, search, and contribute their own entries. Each entry is Markdown-rendered, optionally linked to a feature, and attributed to its author.

The page lives at the `#context` route and is rendered by `App.renderContext()` in `app4.js`.

### Key Capabilities

| Capability | Description |
|---|---|
| **Full-text search** | Substring search across title and content (`LIKE %q%`) |
| **Type filtering** | Filter pills for `all`, `source-analysis`, `doc`, `spec`, `research`, `note` |
| **Expandable cards** | Click-to-expand cards with Markdown-rendered content |
| **Feature association** | Entries optionally link to a feature via `feature_id` |
| **Agent authorship** | Every entry records its author (human, agent name, or system) |
| **Source tracking** | Tags field for free-form source/provenance metadata |

---

## User Roles & Personas

### Human Product Owner
Browse the knowledge base to review what agents have learned, verify research quality, and add clarifying notes or specs. Uses the web viewer exclusively.

### AI Agent (via API)
Creates context entries programmatically via `POST /api/context` during work cycles. Retrieves context via `GET /api/context` or `GET /api/context/search` to inform decisions. Never uses the web UI directly.

### Developer / Contributor
Searches context for prior art, specs, and research before starting work. May add documentation or notes through the API. Uses both web viewer and API.

---

## User Stories

### US-1: Browse All Context Entries

> **As a** product owner,
> **I want to** see all context entries in reverse-chronological order,
> **So that** I can review recent agent activity and knowledge.

**Acceptance Criteria (Given/When/Then):**

1. **Given** the project has context entries,
   **When** I navigate to `#context`,
   **Then** I see a list of cards ordered newest-first, each showing icon, title, type badge, relative timestamp, preview text (first 200 chars), author, and optional feature link and tags.

2. **Given** the project has zero context entries,
   **When** I navigate to `#context`,
   **Then** I see an empty state with the 📚 icon, message "No context entries", and hint "Context entries are added by agents during their work."

3. **Given** the page loads,
   **When** I look at the page header,
   **Then** I see "📚 Context Library" as the title and a subtitle showing the entry count (e.g., "14 entries" or "1 entry").

---

### US-2: Search Context Entries

> **As a** developer,
> **I want to** search context entries by keyword,
> **So that** I can quickly find relevant research, specs, or notes.

**Acceptance Criteria:**

1. **Given** I am on the Context Library page,
   **When** I type a query into the search bar,
   **Then** after a 400ms debounce the page re-renders showing only entries whose title or content_md contain the query (case-insensitive substring match via `GET /api/context/search?q=`).

2. **Given** I search for a term with no matches,
   **When** results return,
   **Then** I see the empty state with the message `No context entries matching "<query>"`.

3. **Given** I have a search active and a type filter applied,
   **When** results return,
   **Then** the API search runs first, then the client-side type filter is applied on top — both constraints are honored.

4. **Given** I clear the search input,
   **When** the debounce fires,
   **Then** the full unfiltered list is restored (API call to `GET /api/context`).

---

### US-3: Filter by Context Type

> **As a** product owner,
> **I want to** filter entries by type (source-analysis, doc, spec, research, note),
> **So that** I can focus on a specific category of knowledge.

**Acceptance Criteria:**

1. **Given** I am on the Context Library page,
   **When** I look below the search bar,
   **Then** I see six filter pills: `all`, `source-analysis`, `doc`, `spec`, `research`, `note`.

2. **Given** no filter is active,
   **When** I click a type pill (e.g., `research`),
   **Then** the pill receives the `.active` class (accent background, white text), the page re-renders showing only entries where `context_type === 'research'`, and the entry count subtitle updates.

3. **Given** I have a type filter active,
   **When** I click `all`,
   **Then** all entries are shown again.

4. **Given** I have a type filter active,
   **When** I click a different type pill,
   **Then** the previous pill deactivates and the new one activates — only one type filter is active at a time.

5. **Given** type filtering is client-side,
   **When** I apply a type filter,
   **Then** no additional API call is made — the filter runs on the already-fetched (or searched) entry list.

---

### US-4: Expand a Context Card

> **As a** developer,
> **I want to** expand a context card to read the full Markdown content,
> **So that** I can review the complete details without leaving the page.

**Acceptance Criteria:**

1. **Given** a collapsed context card,
   **When** I click anywhere on the card (except the feature link),
   **Then** the `.ctx-expanded` section becomes visible, showing the full content rendered as Markdown (GFM with line breaks via `marked.parse` or regex fallback).

2. **Given** an expanded context card,
   **When** I click the card again,
   **Then** the expanded section hides (toggle behavior).

3. **Given** the card has a clickable feature link,
   **When** I click the feature ID,
   **Then** I navigate to the feature detail page — the card does NOT toggle.

---

### US-5: Create a Context Entry (API)

> **As an** AI agent,
> **I want to** create a context entry via the API,
> **So that** my research and findings are persisted for future reference.

**Acceptance Criteria:**

1. **Given** a valid JSON payload with at least `title`,
   **When** I `POST /api/context`,
   **Then** a new entry is created with status `201 Created` and the response contains the full entry including generated `id` and `created_at`.

2. **Given** a payload missing `title`,
   **When** I `POST /api/context`,
   **Then** I receive a `400 Bad Request` error.

3. **Given** a payload without `context_type`,
   **When** I `POST /api/context`,
   **Then** the entry is created with `context_type` defaulting to `"note"`.

4. **Given** a payload without `author`,
   **When** I `POST /api/context`,
   **Then** the entry is created with `author` defaulting to `"human"`.

5. **Given** a payload with `feature_id`,
   **When** I `POST /api/context`,
   **Then** the entry is associated with that feature and appears when filtering by `feature_id`.

---

### US-6: Retrieve Context for Agent Work

> **As an** AI agent,
> **I want to** search and retrieve context entries via the API,
> **So that** I can use prior research and specs when working on a feature.

**Acceptance Criteria:**

1. **Given** a project with context entries,
   **When** I `GET /api/context`,
   **Then** I receive all entries for the project ordered newest-first.

2. **Given** a feature ID,
   **When** I `GET /api/context?feature_id=<id>`,
   **Then** I receive only entries associated with that feature.

3. **Given** a search query,
   **When** I `GET /api/context/search?q=<query>`,
   **Then** I receive entries where title OR content_md contains the query (case-insensitive substring match).

4. **Given** a valid entry ID,
   **When** I `GET /api/context/<id>`,
   **Then** I receive the full entry object.

5. **Given** an invalid entry ID,
   **When** I `GET /api/context/<id>`,
   **Then** I receive a `404 Not Found` error.

---

### US-7: Navigate from Context to Feature

> **As a** product owner,
> **I want to** click a feature ID on a context card,
> **So that** I can jump to the feature detail view for full context.

**Acceptance Criteria:**

1. **Given** a context card with a `feature_id`,
   **When** I look at the card footer,
   **Then** I see `Feature: <feature_id>` as a clickable link.

2. **Given** a context card without a `feature_id`,
   **When** I look at the card footer,
   **Then** no feature link is shown.

3. **Given** I click the feature ID link,
   **When** the click event fires,
   **Then** the app navigates to the feature detail page and the card does NOT expand (event propagation is stopped).

---

## Screen Layout

### Wireframe (ASCII)

```
┌─────────────────────────────────────────────────────────────┐
│  📚 Context Library                                         │
│  14 entries                                                 │
├─────────────────────────────────────────────────────────────┤
│  ┌─────────────────────────────────────────┐                │
│  │ 🔍 Search context entries...            │                │
│  └─────────────────────────────────────────┘                │
│                                                             │
│  [all] [source-analysis] [doc] [spec] [research] [note]     │
│                                                             │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ 🔬 Authentication Research          research · 3h ago   ││
│  │ Analyzed OAuth2 flows and token refresh patterns...     ││
│  │ by agent-researcher · Feature: auth-layer · oauth jwt   ││
│  │─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─││
│  │ [Expanded: full Markdown content rendered here]         ││
│  └─────────────────────────────────────────────────────────┘│
│                                                             │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ 📋 API Endpoint Spec                    spec · 1d ago   ││
│  │ REST endpoints for user management: POST /users...      ││
│  │ by human · Feature: user-api                            ││
│  └─────────────────────────────────────────────────────────┘│
│                                                             │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ 📝 Performance Notes                    note · 2d ago   ││
│  │ Noticed p99 latency spikes during batch import...       ││
│  │ by agent-qa                                             ││
│  └─────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────┘
```

### Element Hierarchy

```
.page-header
├── h2.page-title          "📚 Context Library"
└── .page-subtitle         "{n} entries" / "{n} entry"

search-container
└── input#contextSearchInput   type="text", placeholder="Search context entries..."

filter-bar
└── button.filter-btn.ctx-type-filter × 6   (all | source-analysis | doc | spec | research | note)

entry-list
├── .empty-state                              (when no entries)
│   ├── .empty-state-icon                     📚
│   ├── .empty-state-text                     "No context entries [matching '...']"
│   └── .empty-state-hint                     "Context entries are added by agents..."
│
└── .card.ctx-card × N                        (one per entry)
    ├── header-row
    │   ├── left: icon + strong(title)
    │   └── right: .status-badge(context_type) + timeAgo(created_at)
    ├── preview-text                           first 200 chars of content_md
    ├── metadata-row
    │   ├── "by {author}"
    │   ├── "Feature: {feature_id}"            (clickable, conditional)
    │   └── "{tags}"                           (conditional)
    └── .ctx-expanded                          (hidden by default)
        └── .md-content                        renderMD(content_md)
```

### Responsive Behavior

- Search input: `max-width: 400px`, `width: 100%` — shrinks on narrow viewports.
- Filter pills: `flex-wrap: wrap` — wrap to second line on small screens.
- Cards: full-width block elements, stack vertically.
- No grid or column layout — single-column design works at all breakpoints.

---

## Data Requirements

### ContextEntry Model

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `id` | int | Auto | — | Primary key, auto-incremented |
| `project_id` | string | Yes | — | Scoping: all queries filter by project |
| `feature_id` | string | No | `""` | Optional association with a feature |
| `context_type` | string | Yes | `"note"` | One of: `source-analysis`, `doc`, `spec`, `research`, `note` |
| `title` | string | Yes | — | Entry title (required for creation) |
| `content_md` | string | Yes | — | Markdown-formatted body content |
| `author` | string | Yes | `"human"` | Creator identity (e.g., `human`, `agent-researcher`, `system`) |
| `tags` | string | No | `""` | Free-form tags (space or comma separated) |
| `created_at` | string | Auto | NOW() | ISO 8601 timestamp, set by database |

### API Endpoints

| Method | Path | Purpose | Query Params |
|---|---|---|---|
| `GET` | `/api/context` | List all entries (newest first) | `feature_id` (optional) |
| `POST` | `/api/context` | Create a new entry | — (JSON body) |
| `GET` | `/api/context/search?q=` | Full-text search | `q` (required) |
| `GET` | `/api/context/{id}` | Get single entry by ID | — |

### POST /api/context Request Body

```json
{
  "feature_id": "auth-layer",
  "context_type": "research",
  "title": "OAuth2 Token Refresh Patterns",
  "content_md": "## Findings\n\n1. Refresh tokens should...",
  "author": "agent-researcher",
  "tags": "oauth jwt security"
}
```

### Type Icons Mapping

| `context_type` | Icon | Semantic |
|---|---|---|
| `source-analysis` | 🔍 | Code analysis, architecture review |
| `doc` | 📄 | Documentation artifacts |
| `spec` | 📋 | Feature or API specifications |
| `research` | 🔬 | Research findings, investigations |
| `note` | 📝 | Freeform notes, observations |
| *(fallback)* | 📎 | Unknown or missing type |

---

## Interactions

### Search Input (`#contextSearchInput`)

| Trigger | Action | Detail |
|---|---|---|
| User types | Debounced re-render (400ms) | Sets `App._contextSearch`, calls `App.navigate('context')` |
| Empty input | Clears search | Reverts to `GET /api/context` (full list) |
| Non-empty input | API search | Calls `GET /api/context/search?q=<encoded>` |

- The search query is preserved in `App._contextSearch` across re-renders.
- The input value is restored from `App._contextSearch` on each render (sticky search).

### Type Filter Pills (`.ctx-type-filter`)

| Trigger | Action | Detail |
|---|---|---|
| Click pill | Client-side filter | Sets `App._contextTypeFilter`, re-renders |
| Click `all` | Remove filter | Shows all entries from current API result |
| Click active pill | No-op | Same filter re-applied (idempotent) |

- Filter is client-side only — no additional API call.
- Active pill has accent background (`var(--accent)`) and white text.
- Filter state persists in `App._contextTypeFilter` across re-renders.

### Context Cards (`.ctx-card`)

| Trigger | Action | Detail |
|---|---|---|
| Click card body | Toggle expand/collapse | Shows/hides `.ctx-expanded` div |
| Click feature link | Navigate to feature | `e.target.closest('.clickable-feature')` check prevents card toggle |
| Hover card | Visual feedback | Border becomes accent color, shadow appears, card lifts 1px |

### Feature Links (`.clickable-feature`)

| Trigger | Action | Detail |
|---|---|---|
| Click | Navigate to feature detail | Uses `data-feature-id` attribute |
| Event propagation | Stopped | Card click handler returns early when feature link is clicked |

---

## State Handling

### Client-Side State

| State Variable | Type | Default | Persistence | Purpose |
|---|---|---|---|---|
| `App._contextSearch` | string | `''` | In-memory (session) | Current search query |
| `App._contextTypeFilter` | string | `'all'` | In-memory (session) | Active type filter |

Both values are lost on full page reload. They survive in-app navigation (e.g., visiting features then returning to context).

### Data Flow

```
User action (type/click)
    │
    ▼
Set App._contextSearch / App._contextTypeFilter
    │
    ▼
App.navigate('context')
    │
    ▼
App.renderContext()
    ├── If search query: GET /api/context/search?q=...
    │   Else:            GET /api/context
    │
    ▼
Client-side type filter (if not 'all')
    │
    ▼
Render HTML (cards, empty state)
    │
    ▼
App._bindContextEvents()  ← rebind search/filter/card handlers
```

### Card Expand/Collapse State

- **Not persisted.** Each re-render (from search or filter change) collapses all cards.
- Expand state is purely DOM-level (`style.display` toggle).

### Empty States

| Condition | Icon | Message | Hint |
|---|---|---|---|
| No entries in project | 📚 | "No context entries" | "Context entries are added by agents during their work." |
| Search returns no results | 📚 | `No context entries matching "<query>"` | "Context entries are added by agents during their work." |
| Type filter has no matches | 📚 | "No context entries" | "Context entries are added by agents during their work." |

### WebSocket Live Updates

The web viewer uses a WebSocket connection (`/ws`) for live updates. When a new context entry is created (e.g., by an agent via `POST /api/context`), the server pushes a change notification and the page re-renders automatically, keeping the knowledge base current without manual refresh.

---

## Accessibility Notes

### Current Implementation

- **Search input**: Has `placeholder` text but no explicit `<label>` or `aria-label`.
- **Filter pills**: Rendered as `<button>` elements (keyboard-accessible by default). Active state is visual only (color change) — no `aria-pressed` attribute.
- **Cards**: Use `cursor: pointer` to indicate interactivity but lack `role="button"`, `tabindex`, or `aria-expanded` attributes.
- **Expanded content**: Toggled via `style.display` — no `aria-expanded` state communicated to assistive technology.
- **Feature links**: Rendered as `<span>` elements with click handlers — not focusable or announced as links.

### Recommended Improvements

1. **Search input**: Add `aria-label="Search context entries"` or associate with a visible `<label>`.
2. **Filter pills**: Add `aria-pressed="true|false"` to reflect active state. Consider `role="radiogroup"` on the container with `role="radio"` on each pill since only one can be active.
3. **Cards**: Add `role="button"`, `tabindex="0"`, and `aria-expanded="true|false"`. Support `Enter`/`Space` key to toggle.
4. **Feature links**: Convert to `<a>` elements or add `role="link"` and `tabindex="0"`.
5. **Live region**: Wrap the entry count subtitle in an `aria-live="polite"` region so screen readers announce count changes after search/filter.
6. **Markdown content**: Ensure `renderMD()` output is sanitized (it uses `esc()` in the fallback path; `marked.parse` should use a sanitizer or trusted content only).
