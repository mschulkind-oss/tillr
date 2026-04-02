# History — Screen Specification

## Overview

The History page is a searchable, filterable event timeline that provides a complete audit trail of every action taken within a Tillr-managed project. It answers the question *"what happened, when, and to what?"* — surfacing feature tillr transitions, cycle events, QA decisions, agent heartbeats, and all other system events in reverse-chronological order.

**Route:** `#history`
**API endpoint:** `GET /api/history`
**Search endpoint:** `GET /api/search?q=<query>`

---

## User Roles & Personas

| Persona | Description | Primary goal on this page |
|---------|-------------|---------------------------|
| **Human product owner** | Oversees project progress, reviews agent work | Scan recent activity to stay informed; drill into specific feature timelines |
| **Agent operator** | Dispatches and monitors AI agents | Verify agent heartbeats, confirm cycle completions, investigate failures |
| **QA reviewer** | Approves or rejects features | Review QA-related events, trace approval/rejection history |
| **Debugger / Investigator** | Troubleshooting a problem | Search for specific events, filter by type or feature, inspect raw event data |

---

## User Stories

### US-1: View recent project activity

> **As a** product owner
> **I want** to see a chronological timeline of all project events
> **So that** I can stay informed about what has happened without reading logs.

**Given** the project has recorded events
**When** I navigate to the History page
**Then** I see events displayed in reverse-chronological order, grouped by date, with the most recent events first.

**Given** the project has no recorded events
**When** I navigate to the History page
**Then** I see an empty state with a 📜 icon, the message "No events recorded yet", and guidance on how to generate events.

---

### US-2: Identify event types at a glance

> **As a** product owner
> **I want** each event to display a colored icon badge indicating its type
> **So that** I can visually scan the timeline and quickly distinguish successes, failures, creations, and updates.

**Given** an event of type `feature.completed`
**When** it renders in the timeline
**Then** it displays a green (`.success`) dot with a ✔ icon.

**Given** an event of type `feature.failed`
**When** it renders in the timeline
**Then** it displays a red (`.danger`) dot with a ✘ icon.

**Given** an event of type `feature.created`
**When** it renders in the timeline
**Then** it displays a blue (`.info`) dot with a ⊕ icon.

**Given** an event of type `cycle.started`
**When** it renders in the timeline
**Then** it displays an orange (`.warning`) dot with a ▸ icon.

**Given** an event of type `feature.updated`
**When** it renders in the timeline
**Then** it displays a purple (`.purple`) dot with a ✎ icon.

#### Complete icon mapping

| Event type keyword | Icon | CSS class | Color |
|--------------------|------|-----------|-------|
| `approved`, `completed` | ✔ | `.success` | `#3fb950` (green) |
| `rejected`, `failed` | ✘ | `.danger` | `#f85149` (red) |
| `created` | ⊕ | `.info` | `#58a6ff` (blue) |
| `started`, `scored` | ▸ / ★ | `.warning` | `#d29922` (orange) |
| `updated`, `edit` | ✎ | `.purple` | `#bc8cff` (purple) |
| `removed`, `deleted` | ⊖ | — | default |
| `cycle` | ⟳ | `.info` | `#58a6ff` (blue) |
| `milestone` | ⚑ | `.info` | `#58a6ff` (blue) |
| `heartbeat` | ♥ | `.success` | `#3fb950` (green) |
| `qa`, `review` | ⊘ | `.warning` | `#d29922` (orange) |
| `moved`, `transition` | → | — | default |
| `assigned` | ⊙ | — | default |
| `comment`, `note` | ✦ | — | default |
| *(default)* | ● | — | `#58a6ff` (blue) |

---

### US-3: Filter events by category

> **As an** agent operator
> **I want** to filter events by category (e.g., feature, cycle, qa)
> **So that** I can focus on the event types relevant to my current task.

**Given** the History page is loaded with events of categories `feature`, `cycle`, and `qa`
**When** I look at the filter bar
**Then** I see an "All" button showing the total count plus one button per category, each showing its count, sorted by frequency (highest first).

**Given** I click the "cycle" filter button
**When** the filter is applied
**Then** only events whose `event_type` starts with `cycle` are shown, the "cycle" button receives the `.active` class, and the timeline re-renders.

**Given** I click the "All" filter button
**When** the filter is applied
**Then** all events are shown regardless of category.

> **Implementation note:** Categories are derived by splitting `event_type` on `.` and taking the first segment (e.g., `feature.created` → `feature`). Filtering is performed client-side — no additional API call is made.

---

### US-4: Filter events by feature

> **As a** product owner
> **I want** to filter the timeline to show only events for a specific feature
> **So that** I can trace the full history of a single feature.

**Given** the project has events associated with more than one feature
**When** the History page renders
**Then** a feature dropdown (`<select>`) appears to the right of the filter buttons, listing "All features" plus each distinct `feature_id`.

**Given** I select a specific feature from the dropdown
**When** the selection changes
**Then** only events with a matching `feature_id` are displayed.

**Given** the project has events for only one feature (or none)
**When** the History page renders
**Then** the feature dropdown is not shown.

---

### US-5: Read relative timestamps

> **As a** user
> **I want** to see how long ago each event occurred in human-readable relative time
> **So that** I can quickly gauge recency without parsing ISO timestamps.

**Given** an event occurred 30 seconds ago
**When** it renders in the timeline
**Then** its timestamp reads "just now".

**Given** an event occurred 5 minutes ago
**When** it renders
**Then** its timestamp reads "5 minutes ago".

**Given** an event occurred yesterday
**When** it renders
**Then** its timestamp reads "yesterday at 2:30 PM" (with the actual time).

**Given** an event occurred 3 days ago
**When** it renders
**Then** its timestamp reads "3 days ago".

---

### US-6: View events grouped by date

> **As a** user
> **I want** events grouped under date headers
> **So that** I can orient myself temporally within the timeline.

**Given** events exist on multiple dates
**When** the timeline renders
**Then** events are grouped under date separators formatted as full dates (e.g., "Monday, January 15, 2024"), rendered as centered labels between horizontal gradient lines.

---

### US-7: Inspect event details

> **As a** debugger
> **I want** to click on a timeline event to expand its raw JSON data
> **So that** I can inspect the full payload for troubleshooting.

**Given** an event has associated data (non-empty `data` field)
**When** it renders in the timeline
**Then** key-value detail badges are displayed below the event title, and a hidden expandable JSON section (`.event-expand`) is attached.

**Given** I click on a timeline item
**When** the click target is not a clickable feature badge
**Then** the JSON expansion toggles visibility (hidden ↔ visible), showing the full `data` payload in a monospace `<pre>` block.

**Given** I click on a timeline item that has no `data`
**When** the click fires
**Then** nothing expands (no empty container is rendered).

---

### US-8: Navigate to a feature from an event

> **As a** product owner
> **I want** to click on a feature ID badge within an event
> **So that** I can jump directly to that feature's detail page.

**Given** an event has a non-empty `feature_id`
**When** it renders
**Then** a clickable badge (`.badge.badge-implementing.clickable-feature`) displays the feature ID.

**Given** I click the feature badge
**When** the click fires
**Then** the app navigates to the feature detail page (`#features/<feature_id>`) and the click does not toggle the JSON expansion.

---

### US-9: Paginate through large event lists

> **As a** user with a project containing hundreds of events
> **I want** the timeline to load incrementally
> **So that** the page renders quickly and I can load more events on demand.

**Given** there are more than 50 filtered events
**When** the History page renders
**Then** only the first 50 events are displayed, and a "Load more (N remaining)" button appears below the timeline.

**Given** I click "Load more"
**When** the next batch loads
**Then** 50 additional events are appended to the timeline (without re-rendering existing items), the button updates its remaining count, and newly appended items receive staggered fade-in animations.

**Given** all filtered events have been loaded
**When** the last batch is appended
**Then** the "Load more" button is removed from the DOM.

---

### US-10: See event count in the header

> **As a** product owner
> **I want** the page header to show the total number of events
> **So that** I have an at-a-glance measure of project activity.

**Given** the project has 245 events
**When** the History page renders
**Then** the page subtitle reads "245 events".

---

### US-11: Search events (via `/api/search`)

> **As a** debugger
> **I want** to search events by keyword
> **So that** I can find specific events without scrolling.

**Given** I perform a search via the `/api/search?q=<query>` endpoint
**When** results are returned
**Then** only events whose `data` field contains the query substring are returned, limited to 50 results, ordered newest first.

> **Implementation note:** Search uses SQL `LIKE '%query%'` on the `data` column. It does not search `event_type` or other fields. Search is case-insensitive per SQLite default behavior.

---

### US-12: Server-side filtering via API

> **As an** external tool or integration
> **I want** to query the history API with filter parameters
> **So that** I can retrieve targeted event data programmatically.

**Given** I call `GET /api/history?feature=auth-module`
**When** the server processes the request
**Then** only events with `feature_id = 'auth-module'` are returned.

**Given** I call `GET /api/history?type=feature.created`
**When** the server processes the request
**Then** only events with `event_type = 'feature.created'` are returned.

**Given** I call `GET /api/history?since=2024-06-01T00:00:00Z`
**When** the server processes the request
**Then** only events with `created_at >= '2024-06-01T00:00:00Z'` are returned.

**Given** I call `GET /api/history` with no parameters
**When** the server processes the request
**Then** the 100 most recent events are returned.

---

## Screen Layout

### Wireframe (ASCII)

```
┌─────────────────────────────────────────────────────────────────┐
│  History                                          245 events    │  ← page-header
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ [All 245] [feature 98] [cycle 72] [qa 45] [milestone 30]│   │  ← filter buttons
│  │                                    [▾ All features     ] │   │  ← feature dropdown
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │               ── Monday, January 15, 2024 ──             │   │  ← date separator
│  │                                                          │   │
│  │  ●  just now                                             │   │  ← timeline-dot + time
│  │     feature.completed    [auth-module]                   │   │  ← event type + feature badge
│  │     ┌─────────────────────────────────────┐              │   │
│  │     │ STATUS done │ REASON passed all QA  │              │   │  ← detail badges
│  │     └─────────────────────────────────────┘              │   │
│  │     ┌─────────────────────────────────────┐              │   │
│  │     │ { "status": "done", "reason": ... } │              │   │  ← expanded JSON (hidden)
│  │     └─────────────────────────────────────┘              │   │
│  │                                                          │   │
│  │  ▸  2 hours ago                                          │   │
│  │     cycle.started        [auth-module]                   │   │
│  │     ┌──────────────────────┐                             │   │
│  │     │ CYCLE feature-impl   │                             │   │
│  │     └──────────────────────┘                             │   │
│  │                                                          │   │
│  │               ── Sunday, January 14, 2024 ──             │   │  ← next date group
│  │                                                          │   │
│  │  ⊕  yesterday at 3:15 PM                                │   │
│  │     feature.created      [auth-module]                   │   │
│  │                                                          │   │
│  │          ┌──────────────────────────────┐                │   │
│  │          │  Load more (195 remaining)   │                │   │  ← pagination button
│  │          └──────────────────────────────┘                │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### Component hierarchy

```
page-header
  ├── title ("History")
  └── subtitle ("{N} events")

card
  ├── history-filters
  │     ├── filter-btn × N  ("All", per-category)
  │     │     └── filter-count (event count)
  │     └── filter-select (feature dropdown, conditional)
  │
  ├── timeline
  │     └── timeline-date-group × N
  │           ├── timeline-date-sep
  │           │     ├── timeline-date-line (left hr)
  │           │     ├── timeline-date-label ("Monday, January 15, 2024")
  │           │     └── timeline-date-line (right hr)
  │           └── timeline-item × N
  │                 ├── timeline-dot (icon)
  │                 ├── timeline-time (relative timestamp)
  │                 ├── timeline-event
  │                 │     ├── event type label (formatted)
  │                 │     └── badge.clickable-feature (feature ID, conditional)
  │                 ├── timeline-detail (conditional)
  │                 │     └── detail-badge × N
  │                 │           ├── detail-badge-key
  │                 │           └── detail-badge-val
  │                 └── event-expand (hidden, conditional)
  │                       └── event-json (<pre>)
  │
  └── timeline-load-more-wrap (conditional)
        └── timeline-load-more (button)
```

---

## Data Requirements

### API: `GET /api/history`

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `feature` | string | No | Filter by exact `feature_id` |
| `type` | string | No | Filter by exact `event_type` |
| `since` | string (ISO 8601) | No | Only events at or after this timestamp |

**Response:** `200 OK` — JSON array of `Event` objects. Hard limit of **100** events per response.

```jsonc
[
  {
    "id": 123,                          // INTEGER — auto-increment PK
    "project_id": "my-project",         // TEXT — owning project
    "feature_id": "auth-module",        // TEXT — associated feature (may be empty)
    "event_type": "feature.completed",  // TEXT — dot-separated category.action
    "data": "{\"status\":\"done\"}",    // TEXT — JSON payload (may be empty)
    "created_at": "2024-01-15T14:30:45Z" // TEXT — ISO 8601
  }
]
```

### API: `GET /api/search?q=<query>`

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `q` | string | Yes | Substring to search within event `data` |

**Response:** `200 OK` — JSON array of `Event` objects. Hard limit of **50** results. Returns empty array if `q` is empty.

### Database schema

```sql
CREATE TABLE events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  TEXT NOT NULL REFERENCES projects(id),
    feature_id  TEXT REFERENCES features(id),
    event_type  TEXT NOT NULL,
    data        TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_events_project ON events(project_id);
CREATE INDEX idx_events_created ON events(created_at);
```

### Client-side state

| Property | Type | Default | Purpose |
|----------|------|---------|---------|
| `_historyEvents` | `Event[]` | `[]` | Cached events from last API fetch |
| `_historyFilter` | `string` | `'all'` | Active filter — `'all'`, a category prefix (e.g., `'feature'`), or a `feature_id` |
| `_historyShown` | `number` | `50` | Number of events currently rendered |
| `_historyPageSize` | `number` | `50` | Batch size for pagination |

---

## Interactions

### Filter button click

1. User clicks a category filter button (e.g., "cycle").
2. `_historyFilter` is set to the button's `data-filter` value.
3. `navigate('history')` is called, re-rendering the entire page.
4. The active button receives the `.active` CSS class (blue background, white text).
5. The timeline shows only events whose `event_type` starts with the selected category.
6. **No API call is made** — filtering is client-side against `_historyEvents`.

### Feature dropdown change

1. User selects a feature from the `<select>` dropdown.
2. `_historyFilter` is set to the selected `feature_id` (or `'all'`).
3. `navigate('history')` is called, re-rendering the page.
4. Events are filtered to those matching the selected `feature_id`.

### Load more click

1. User clicks "Load more (N remaining)".
2. `_historyShown` is incremented by `_historyPageSize` (50), capped at total filtered count.
3. New timeline items are **appended** to the existing DOM (no full re-render).
4. Newly added items receive staggered fade-in animations (delay = `idx × 0.04s`, max 1.2s).
5. Clickable feature badges and expand handlers are re-bound on new items.
6. The button text updates to reflect the new remaining count, or the button is removed if all events are shown.

### Timeline item click (expand/collapse)

1. User clicks anywhere on a `.timeline-item`.
2. If the click target is inside a `.clickable-feature` badge, the click is ignored (feature navigation takes precedence).
3. Otherwise, the `.event-expand` container toggles between `display: none` and `display: block`.
4. The expanded section shows the full JSON `data` payload in a `<pre>` block.

### Feature badge click

1. User clicks a `.clickable-feature` badge on a timeline item.
2. The app navigates to `#features/<feature_id>`.
3. The event's expand/collapse handler does not fire (early return via `closest('.clickable-feature')` check).

### Timeline dot hover

1. User hovers over a `.timeline-item`.
2. The `.timeline-dot` scales up to 115% (`transform: scale(1.15)`).
3. The item background changes to `var(--bg-hover)`.

---

## State Handling

### Loading state

The page calls `GET /api/history` on render. While the fetch is in progress the page awaits the async response. No explicit loading spinner is rendered; the timeline area is empty until data arrives.

### Empty state

When no events exist (`events.length === 0`), the page renders:

```
┌───────────────────────────────┐
│            📜                 │
│   No events recorded yet     │
│                               │
│   (hint + call-to-action)    │
└───────────────────────────────┘
```

- Icon: 📜
- Primary message: "No events recorded yet"
- Guidance text on how to generate events via the CLI

### Populated state

Standard rendering with filters, timeline, and optional pagination.

### Filtered-empty state

When a filter is applied but no events match, the timeline renders empty within the card. The filter buttons remain visible so the user can change or clear the filter.

### WebSocket live updates

The History page receives live updates via the WebSocket connection (`/ws`). When a new event is inserted into the database, the server pushes a notification and the client re-fetches and re-renders the page, keeping the timeline current without manual refresh.

---

## Accessibility Notes

### Keyboard navigation

- Filter buttons and the feature dropdown are natively focusable and operable via keyboard (buttons and `<select>` elements).
- The "Load more" button is a `<button>` element, keyboard accessible by default.
- Timeline items use `cursor: pointer` but are `<div>` elements — they are **not** natively keyboard-focusable. Future improvement: add `tabindex="0"` and `role="button"` with `keydown` handlers for Enter/Space.

### Screen readers

- The page header conveys context ("History — 245 events").
- Filter buttons include text labels with counts (e.g., "feature 98") — readable by screen readers.
- Timeline date separators use `<hr>` elements with a visible label, providing structural separation.
- Feature badges contain text content (`feature_id`) that is screen-reader accessible.
- Detail badges use uppercase key labels and plain-text values.

### Color & contrast

- Event type colors (green, red, orange, blue, purple) are used **in addition to** distinct icons (✔, ✘, ⊕, ▸, ✎, etc.), ensuring color is not the sole differentiator.
- Both dark and light themes are supported. Light theme adjusts backgrounds, borders, and text colors for contrast (e.g., dark text on light backgrounds).
- Timeline dot icons are white-on-color, providing high contrast.

### Motion

- Staggered fade-in animations use `animation: timelineFadeIn 0.4s ease`. Users with `prefers-reduced-motion` are not currently accommodated. Future improvement: add a `@media (prefers-reduced-motion: reduce)` rule to disable animations.
- Dot scale-on-hover transition is 0.2s, a minimal motion effect.

### Responsive design

| Breakpoint | Timeline adjustments |
|------------|---------------------|
| Desktop (> 768px) | `padding-left: 40px`, dot at `left: -30px` (24×24px) |
| Tablet (≤ 768px) | `padding-left: 30px`, dot at `left: -24px` (20×20px), font reduced |
| Mobile (≤ 480px) | `padding-left: 24px`, dot at `left: -24px` (20×20px), event font `0.8rem`, detail badges `0.75rem` |

### Known gaps & recommendations

| Gap | Recommendation |
|-----|----------------|
| Timeline items are not keyboard-focusable | Add `tabindex="0"` and `role="button"` to `.timeline-item` elements; handle Enter/Space keydown |
| No `aria-live` region for load-more results | Wrap the timeline in an `aria-live="polite"` region or announce new items via an SR-only element |
| No `prefers-reduced-motion` support | Add `@media (prefers-reduced-motion: reduce) { .timeline-item { animation: none; opacity: 1; } }` |
| Filter buttons lack `aria-pressed` state | Add `aria-pressed="true"` to the active filter button |
| Feature dropdown lacks a visible `<label>` | Add a `<label>` element or `aria-label="Filter by feature"` to the `<select>` |
