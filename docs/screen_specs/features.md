# Features — Screen Specification

## Overview

The Features page is the central work-tracking surface of the Tillr app. It presents every feature in the managed project as an interactive table with status-colored rows, expandable detail panels, drag-and-drop priority reordering, dependency visualization, and inline status editing. Users can filter by status, search by text, and toggle between a **list view** and a **dependency graph view** rendered on an HTML canvas.

### Page URL

`#features` (SPA hash route). Deep-links to a specific feature via `#features/{feature-id}`.

### Entry Points

| Source | Mechanism |
|--------|-----------|
| Sidebar navigation | Click "Features" nav item |
| Dashboard feature card | Click a feature name |
| Dependency link | Click a `clickable-feature` anchor anywhere in the app |
| Direct URL | Navigate to `#features` or `#features/{id}` |

---

## User Roles & Personas

| Persona | Description | Primary Goals on This Page |
|---------|-------------|---------------------------|
| **Human Product Owner** | Steers priorities, approves/rejects QA, reviews progress | Filter by status, reorder priorities, approve/reject features in human-qa, read specs and history |
| **Agent Operator** | Dispatches AI agents and monitors their output | Check which features are in agent-qa, review work items and cycle scores, read blocking chains |
| **Developer (Human)** | Implements features, writes specs | Expand feature detail for full spec, check dependencies before starting work, update status |
| **Stakeholder / Observer** | Reviews project health without editing | View dependency graph, scan progress bars, filter to "done" for release notes |

---

## User Stories

### US-1 — View All Features

> **As a** product owner,
> **I want to** see every feature in a single table with status badges, priority indicators, and progress bars,
> **So that** I can assess project health at a glance.

**Acceptance Criteria**

```gherkin
Given the project has N features in the database
When I navigate to the Features page
Then I see a page header reading "Features" with subtitle "{N} features tracked"
  And a summary bar shows counts: "{done} done · {implementing} implementing · {blocked} blocked"
  And the table displays columns: Feature, Status, Priority, Milestone, Progress, Created
  And each row has a colored left border matching its status
  And done features show a strikethrough name at reduced opacity
```

### US-2 — Filter Features by Status

> **As a** product owner,
> **I want to** filter the feature list by status using pill buttons,
> **So that** I can focus on features in a specific tillr stage.

**Acceptance Criteria**

```gherkin
Given the Features page is loaded with features in multiple statuses
When I click the "implementing" filter pill
Then only features with status "implementing" are shown
  And the "implementing" pill has the active style (blue background, white text)
  And the pill displays a count badge showing how many features match
  And clicking "All" restores the full list

Given a filter is active and I type in the search box
When I enter a query
Then both the status filter AND the text search are applied together (AND logic)
```

### US-3 — Search Features by Text

> **As a** developer,
> **I want to** search features by name, ID, or description,
> **So that** I can quickly find a specific feature without scrolling.

**Acceptance Criteria**

```gherkin
Given the Features page is loaded
When I type "auth" into the search input (prefixed with a 🔍 icon)
Then the table live-filters to show only features whose name, ID, or description contains "auth" (case-insensitive)
  And the filtering happens on every keystroke (no submit required)

Given a search query is active
When I clear the search input
Then all features (respecting any active status filter) are shown again
```

### US-4 — Expand Feature Detail

> **As a** developer,
> **I want to** click a feature row to expand its full detail inline,
> **So that** I can read the spec, dependencies, work items, and history without leaving the page.

**Acceptance Criteria**

```gherkin
Given the feature list is displayed
When I click a feature row
Then a detail row expands below it spanning all 6 columns
  And the row gains the "expanded" class (highlighted background)
  And any previously expanded row collapses
  And the detail panel shows these sections:
    | Section       | Content                                                    |
    |---------------|------------------------------------------------------------|
    | ID            | Feature slug in monospace font (read-only)                 |
    | Status        | Dropdown `<select>` for inline status editing              |
    | Priority      | Priority label (Critical / High / Medium / Low / Nice to have) |
    | Milestone     | Milestone name (if assigned), otherwise omitted            |
    | Description   | Full description text (if present)                         |
    | Roadmap Item  | Clickable link to linked roadmap item (if present)         |
    | Depends On    | Comma-separated clickable feature links (if present)       |
    | Created       | Full ISO timestamp                                         |

Given the detail row is expanded
Then these sections load asynchronously (lazy-loaded on first expand):
  | Section            | Data Source                                 |
  |--------------------|---------------------------------------------|
  | Spec               | Feature spec rendered as numbered list or preformatted block |
  | Dependencies       | Depends-on list, required-by list, blocking chain, mini canvas graph |
  | Work Items & Cycles| Work items with status icons, cycle history with iteration badges, scores |
  | History            | Event timeline with icons, event types, results, and relative timestamps |
  | Discussions        | Linked discussions with status badges and comment counts |

Given I click the same expanded row again
When the detail row is visible
Then it collapses and the row loses the "expanded" class
```

### US-5 — Inline Status Editing

> **As a** product owner,
> **I want to** change a feature's status from a dropdown in the detail panel,
> **So that** I can advance features through the tillr without using the CLI.

**Acceptance Criteria**

```gherkin
Given a feature detail row is expanded
When I click the status dropdown (styled `<select>` with custom arrow ▾)
Then I see all valid statuses: draft, planning, implementing, agent-qa, human-qa, done, blocked
  And the current status is pre-selected

When I select a new status
Then a PATCH request is sent to /api/features/{id} with { "status": newStatus }
  And the row's left-border color updates to match the new status
  And the status badge in the table updates
  And a toast notification confirms the change
  And the click event does NOT propagate to the row (no collapse/expand toggle)
```

### US-6 — Drag-and-Drop Priority Reordering

> **As a** product owner,
> **I want to** drag features to reorder their priority,
> **So that** I can visually prioritize work without editing numeric values.

**Acceptance Criteria**

```gherkin
Given the feature list is displayed in list view
When I hover over a feature row
Then a drag handle (⠿) appears with grab cursor

When I start dragging a feature row
Then the row becomes semi-transparent (opacity 0.4, class "dragging")
  And features that DEPEND ON the dragged feature are highlighted with a red inset shadow (class "dep-blocker")
  And features that the dragged feature DEPENDS ON are highlighted with a green inset shadow (class "dep-dependency")

When I drag over a valid drop target row
Then that row shows a blue top-border indicator (class "drag-over", 3px solid accent)

When I drag over a row that is a dependency of the dragged feature
Then the drop cursor shows "not-allowed" (dropEffect = 'none')
  And the drop is prevented (dependency ordering constraint)

When I drop the feature on a valid target row
Then the row is visually repositioned in the DOM (with its detail row)
  And a POST request is sent to /api/features/reorder with the new priority order
  And the page re-renders to reflect the new ordering

When I release the drag (dragend)
Then all highlight classes are removed (dragging, drag-over, dep-blocker, dep-dependency)
```

### US-7 — Dependency Graph View

> **As a** stakeholder,
> **I want to** see a visual dependency graph of all features,
> **So that** I can understand blocking relationships and critical paths.

**Acceptance Criteria**

```gherkin
Given the Features page is loaded
When I click the "◈" (graph) toggle button in the toolbar
Then the table view is hidden and a full dependency graph is displayed
  And the "◈" button gains the active style (blue background)

Then above the canvas I see:
  | Element   | Content                                     |
  |-----------|---------------------------------------------|
  | Stats bar | "{X} features · {Y} dependencies · {Z} roots · {W} leaves" |
  | Legend    | Color-coded dots for each status: Draft (gray), Planning (yellow), Implementing (blue), Done (green), Blocked (red) |

Then the canvas renders:
  - Nodes as rounded rectangles with a left-edge status color bar
  - Feature name (truncated to 16 chars) and status label inside each node
  - Bezier curve edges with arrowheads connecting dependencies (left-to-right flow)
  - Topological layering: dependencies on the left, dependents on the right

When I hover over a node
Then connected edges and nodes are highlighted
  And unconnected elements are dimmed (alpha 0.1)
  And the hovered node gets a colored border glow

When I click a node
Then I navigate to that feature in list view (auto-expanded)

When I drag on the canvas background
Then the graph pans

When I scroll the mouse wheel on the canvas
Then the graph zooms in/out

When I click the "☰" (list) toggle button
Then the graph view is hidden and the table view is restored
```

### US-8 — View Feature Dependencies in Detail

> **As a** developer,
> **I want to** see what a feature depends on and what depends on it,
> **So that** I can plan my work order and avoid blocking others.

**Acceptance Criteria**

```gherkin
Given a feature detail row is expanded
When the dependencies section loads (fetched from /api/features/{id}/deps)
Then I see:
  | Sub-section     | Content                                              |
  |-----------------|------------------------------------------------------|
  | Depends On      | List of dependency features as clickable links with status badges |
  | Required By     | List of features that depend on this one, same format |
  | ⚠️ Blocking Chain | (Conditional) Transitive unfinished dependencies shown with red left border and danger-colored text |
  | Mini Graph      | A 150px-tall canvas rendering the local dependency neighborhood |

When I click a dependency link (class "clickable-feature")
Then I navigate to that feature's detail view
```

### US-9 — View Work Items, Cycles, and Scores

> **As an** agent operator,
> **I want to** see the work history for a feature including cycle iterations and judge scores,
> **So that** I can evaluate agent performance and iteration progress.

**Acceptance Criteria**

```gherkin
Given a feature detail row is expanded
When the enriched data section loads (fetched from /api/features/{id})
Then I see up to three sub-sections:

  Work Items:
    - Each item shows: status icon, status badge, work type (bold), result text (if any), relative timestamp

  Cycle History:
    - Each item shows: status icon, status badge, cycle type (bold), current step name, iteration badge ("Iter N"), relative timestamp

  Scores:
    - Each item shows: score badge (color-coded by value), step name, iteration badge, notes (if any), relative timestamp
```

### US-10 — View Feature History

> **As a** product owner,
> **I want to** see a timeline of all events for a feature,
> **So that** I can audit what happened and when.

**Acceptance Criteria**

```gherkin
Given a feature detail row is expanded
When the history section loads (fetched from /api/history?feature={id})
Then I see a vertical timeline of events, each showing:
  - A circular icon (20px, centered emoji/symbol)
  - Event type label (bold)
  - Result or detail text (truncated with ellipsis)
  - Relative timestamp (right-aligned)
  And events are separated by a 1px border-bottom
```

### US-11 — View Linked Discussions

> **As a** product owner,
> **I want to** see discussions linked to a feature,
> **So that** I can review design decisions and open questions in context.

**Acceptance Criteria**

```gherkin
Given a feature has linked discussions
When the detail row is expanded and discussions load
Then I see a "Linked Discussions" header
  And each discussion shows: status badge, title (truncated with ellipsis), comment count with 💬 emoji
  And clicking a discussion navigates to the Discussions page for that item
```

### US-12 — View Feature Spec

> **As a** developer,
> **I want to** read the full acceptance criteria for a feature,
> **So that** I know exactly what to build.

**Acceptance Criteria**

```gherkin
Given a feature has a spec field
When the detail row is expanded
Then the spec section renders below a "Spec" header
  And if the spec contains numbered lines (e.g., "1. Must do X"), it renders as an ordered list (<ol>) with accent-colored markers
  And if the spec is freeform text, it renders in a preformatted code block (monospace, scrollable, max-height 300px)
  And HTML in the spec is escaped for security
```

### US-13 — Navigate to Feature via Deep Link

> **As a** user,
> **I want to** share a direct link to a specific feature,
> **So that** others can jump straight to its detail view.

**Acceptance Criteria**

```gherkin
Given I navigate to #features/my-feature-id
When the page loads
Then the feature with ID "my-feature-id" is auto-expanded
  And the page scrolls to that row
  And the breadcrumb updates to show the feature name
```

### US-14 — Empty States

> **As a** new user,
> **I want to** see helpful guidance when no features exist or my filters return nothing,
> **So that** I know what to do next.

**Acceptance Criteria**

```gherkin
Given the project has no features
When I navigate to the Features page
Then I see an empty state with:
  - Icon: ✨
  - Text: "No features yet"
  - Hint: "Features are the building blocks of your project…"
  - CTA: "$ tillr feature add <name>"

Given features exist but my filter/search matches none
When no rows pass the filter
Then I see a compact empty state with:
  - Icon: 🔍
  - Text: "No features match"
  - Hint: "Try adjusting your search or filters."

Given I switch to the graph view but there are no dependency edges
When the graph loads
Then I see:
  - Icon: 🔗
  - Text: "No features to graph"
```

---

## Screen Layout

### Wireframe (ASCII)

```
┌─────────────────────────────────────────────────────────────────────────┐
│  Features                                          {N} features tracked │
├─────────────────────────────────────────────────────────────────────────┤
│  ┌─────────────────────────────────────────────────────────────────────┐│
│  │ [All 12] [Draft 3] [Planning 2] [Implementing 4] ...   [☰][◈] 🔍…││
│  └─────────────────────────────────────────────────────────────────────┘│
│  5 features · 2 done · 2 implementing · 1 blocked                      │
├────────┬──────────┬──────────┬───────────┬──────────┬──────────────────┤
│Feature │ Status   │ Priority │ Milestone │ Progress │ Created          │
├────────┼──────────┼──────────┼───────────┼──────────┼──────────────────┤
│▐ Auth  │ ●done    │ ● Crit   │ v1.0 MVP  │ ████ 100%│ 2 days ago       │
│  auth-1│          │          │           │          │                  │
│  JWT…  │          │          │           │          │                  │
├────────┼──────────┼──────────┼───────────┼──────────┼──────────────────┤
│▐ Search│ ●impl    │ ● High   │ v1.0 MVP  │ ██░░  40%│ 1 day ago        │
│  srch-1│          │          │           │          │                  │
│  Full… │          │          │           │          │                  │
├────────┴──────────┴──────────┴───────────┴──────────┴──────────────────┤
│  ┌──────────────────────────────────────────────────────────────────┐  │
│  │ ID        srch-1                                                │  │
│  │ Status    [implementing ▾]                                      │  │
│  │ Priority  High                                                  │  │
│  │ Milestone v1.0 MVP                                              │  │
│  │ Depends   auth-1, db-layer                                      │  │
│  │ ─── Spec ──────────────────────────────────────────────────────  │  │
│  │ 1. Elasticsearch integration                                    │  │
│  │ 2. Pagination                                                   │  │
│  │ 3. Filters                                                      │  │
│  │ ─── Dependencies ─────────────────────────────────────────────  │  │
│  │ Depends On: [auth-1 ●done] [db-layer ●done]                    │  │
│  │ Required By: [dashboard ●draft]                                 │  │
│  │ ┌─mini-dep-graph-canvas──────────────────────────────────────┐  │  │
│  │ └────────────────────────────────────────────────────────────┘  │  │
│  │ ─── Work Items ───────────────────────────────────────────────  │  │
│  │ ✓ done   develop  "Implemented search endpoint"    2h ago       │  │
│  │ ─── History ──────────────────────────────────────────────────  │  │
│  │ 🔄 status_changed  draft → implementing             1d ago      │  │
│  │ ✨ feature_created                                   2d ago      │  │
│  └──────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
```

### Graph View Wireframe

```
┌─────────────────────────────────────────────────────────────────────────┐
│  Features                                          {N} features tracked │
├─────────────────────────────────────────────────────────────────────────┤
│  ┌─────────────────────────────────────────────────────────────────────┐│
│  │ [All 12] [Draft 3] [Planning 2] ...                [☰][◈] 🔍…     ││
│  └─────────────────────────────────────────────────────────────────────┘│
│  ┌─ Stats ────────────────────────────────────────────────────────────┐ │
│  │ 12 features · 8 dependencies · 3 roots · 4 leaves                 │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│  ┌─ Legend ───────────────────────────────────────────────────────────┐ │
│  │ ■ Draft  ■ Planning  ■ Implementing  ■ Done  ■ Blocked            │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│  ┌─ Canvas (pannable, zoomable) ──────────────────────────────────────┐ │
│  │                                                                    │ │
│  │   ┌──────────┐        ┌──────────┐        ┌──────────┐            │ │
│  │   │▎db-layer │───────▶│▎search   │───────▶│▎dashboard│            │ │
│  │   │  done    │        │  impl    │        │  draft   │            │ │
│  │   └──────────┘        └──────────┘        └──────────┘            │ │
│  │        │                                                           │ │
│  │        ▼                                                           │ │
│  │   ┌──────────┐                                                     │ │
│  │   │▎auth     │                                                     │ │
│  │   │  done    │                                                     │ │
│  │   └──────────┘                                                     │ │
│  │                                                                    │ │
│  └────────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────┘
```

### Component Hierarchy

```
FeaturesPage
├── PageHeader  (.page-header)
│   ├── Title   (.page-title)  →  "Features"
│   └── Subtitle(.page-subtitle) →  "{N} features tracked"
├── Toolbar     (.features-toolbar)
│   ├── FilterPills (.filter-pills)
│   │   └── FilterPill[] (.filter-pill)  →  [All, Draft, Planning, …]
│   └── ToolbarRight (.features-toolbar-right)
│       ├── ViewToggle (.features-view-toggle)
│       │   ├── ListButton  (.view-toggle-btn[data-view="list"])
│       │   └── GraphButton (.view-toggle-btn[data-view="graph"])
│       └── SearchWrap (.features-search-wrap)
│           └── SearchInput (.features-search #featuresSearch)
├── TableView   (#featuresTableWrap)
│   ├── Summary (.features-summary)
│   └── Table   (.table)
│       ├── Header Row (th × 6)
│       └── FeatureRow[] (.ft-row[data-feature-id])
│           ├── NameCell      (.ft-name, .ft-id, .ft-desc)
│           ├── StatusCell    (.badge .badge-{status})
│           ├── PriorityCell  (.priority-dot .p-{1-5})
│           ├── MilestoneCell
│           ├── ProgressCell  (.ft-progress-wrap)
│           ├── CreatedCell
│           └── DetailRow (.ft-detail-row[data-detail-for])
│               ├── StaticFields (ID, Status dropdown, Priority, Milestone, Description, Roadmap link, Depends On, Created)
│               ├── SpecSection  (.feature-spec-section)  [lazy]
│               ├── DepsSection  ([data-deps-for])         [lazy]
│               │   ├── DependsOn   (.dep-detail-group)
│               │   ├── RequiredBy  (.dep-detail-group)
│               │   ├── BlockingChain (.dep-blocking)
│               │   └── MiniGraph   (.dep-mini-graph > canvas)
│               ├── EnrichedSection ([data-enriched-for])  [lazy]
│               │   ├── WorkItems   (.enriched-section)
│               │   ├── CycleHistory(.enriched-section)
│               │   └── Scores      (.enriched-section)
│               ├── HistorySection ([data-history-for])    [lazy]
│               └── DiscussionsSection ([data-discussions-for]) [lazy]
└── GraphView   (#featuresGraphWrap .features-graph-wrap)
    ├── StatsBar   (.depgraph-stats)
    ├── Legend     (.depgraph-legend)
    └── CanvasWrap (.depgraph-canvas-wrap)
        └── Canvas (#featuresDepCanvas)
```

---

## Data Requirements

### API Endpoints

| Method | Endpoint | Purpose | Query Params |
|--------|----------|---------|--------------|
| `GET` | `/api/features` | List all features | `?status={s}`, `?milestone={m}` |
| `GET` | `/api/features/{id}` | Feature detail + work items, cycles, scores | — |
| `PATCH` | `/api/features/{id}` | Update feature status or priority | — |
| `POST` | `/api/features/reorder` | Bulk priority reorder | — |
| `GET` | `/api/features/{id}/deps` | Dependency detail (depends-on, required-by, blocking chain) | — |
| `GET` | `/api/dependencies` | All features + edges for graph | — |
| `GET` | `/api/history?feature={id}` | Event history for feature | — |

### Request / Response Schemas

#### `GET /api/features` → `Feature[]`

```jsonc
[
  {
    "id": "auth-module",           // string — slug ID
    "name": "Auth Module",         // string — display name
    "description": "JWT-based…",   // string | null
    "status": "implementing",      // enum — see Status Enum below
    "priority": 2,                 // int 1–5 (1 = Critical, 5 = Nice to have)
    "milestone_name": "v1.0 MVP",  // string | null
    "roadmap_item_id": "perf-123", // string | null
    "depends_on": ["db-layer"],    // string[] — feature IDs
    "spec": "1. Must do X\n2.…",   // string | null — acceptance criteria
    "created_at": "2025-01-15T…"   // ISO 8601 timestamp
  }
]
```

#### `GET /api/features/{id}` → `FeatureDetail`

```jsonc
{
  "feature": { /* Feature object */ },
  "work_items": [
    { "id": 1, "status": "done", "work_type": "develop", "result": "Implemented…", "created_at": "…" }
  ],
  "cycles": [
    { "id": 1, "cycle_type": "feature-implementation", "status": "active", "step_name": "develop", "iteration": 2, "created_at": "…" }
  ],
  "scores": [
    { "score": 8.5, "step": 3, "iteration": 1, "notes": "Good but…", "created_at": "…" }
  ]
}
```

#### `PATCH /api/features/{id}` — Request

```jsonc
{ "status": "done" }  // or { "priority": 3 }
```

#### `POST /api/features/reorder` — Request

```jsonc
{
  "items": [
    { "id": "auth-module", "priority": 1 },
    { "id": "search-api", "priority": 2 },
    { "id": "dashboard",  "priority": 3 }
  ]
}
```

#### `GET /api/features/{id}/deps` → `FeatureDeps`

```jsonc
{
  "feature": { /* Feature object */ },
  "depends_on": [
    { "id": "db-layer", "name": "Database Layer", "status": "done" }
  ],
  "depended_by": [
    { "id": "dashboard", "name": "Dashboard", "status": "draft" }
  ],
  "blocking_chain": [
    "cache-layer (planning)"   // transitive unfinished deps
  ]
}
```

#### `GET /api/dependencies` → `DependencyGraph`

```jsonc
{
  "nodes": [
    { "id": "auth-module", "name": "Auth Module", "status": "done" }
  ],
  "edges": [
    { "from": "auth-module", "to": "search-api" }
  ]
}
```

### Status Enum

| Value | Display | Border Color | Badge Background (Dark) | Badge Text Color |
|-------|---------|-------------|------------------------|-----------------|
| `draft` | Draft | `var(--text-muted)` gray | `#1f1d45` | purple |
| `planning` | Planning | `var(--purple)` | `#1f1d45` | purple |
| `implementing` | Implementing | `var(--accent)` blue | `#0c2d6b` | blue |
| `agent-qa` | Agent QA | `var(--info)` blue | `#1a2b32` | blue |
| `human-qa` | Human QA | `var(--warning)` orange | `#2a1f0c` | orange |
| `done` | Done | `var(--success)` green | `#0d2818` | green |
| `blocked` | Blocked | `var(--danger)` red | `#2d1215` | red |

### Priority Enum

| Value | Label | Dot Color | Glow |
|-------|-------|-----------|------|
| 1 | Critical | `var(--danger)` red | Yes — `rgba(248,81,73,0.5)` |
| 2 | High | `var(--warning)` orange | Yes — `rgba(210,153,34,0.4)` |
| 3 | Medium | `var(--accent)` blue | No |
| 4 | Low | `var(--success)` green | No |
| 5 | Nice to have | `var(--purple)` | Yes — `rgba(188,140,255,0.4)` |

### Progress Calculation

| Status | Percentage | Bar Color |
|--------|-----------|-----------|
| `draft` | 0% | gray |
| `planning` | 20% | `var(--accent)` blue |
| `implementing` | 40% | `var(--accent)` blue |
| `agent-qa` | 60% | `var(--accent)` blue |
| `human-qa` | 80% | `var(--accent)` blue |
| `done` | 100% | `var(--success)` green |
| `blocked` | 0% | `var(--danger)` red |

---

## Interactions

### Feature Row

| Trigger | Action | Visual Feedback |
|---------|--------|-----------------|
| **Click** row | Toggle expand/collapse of detail row; collapse any other expanded row | Row gains/loses `.expanded` class (highlighted background) |
| **Hover** row | Highlight row; reveal drag handle | `td` background becomes `var(--bg-tertiary)`; border-left width increases to 4px; drag handle opacity goes from 0.5 → 1 |
| **Drag start** | Begin priority reorder | Row opacity 0.4 (`.dragging`); dependency rows highlighted red/green |
| **Drag over** valid target | Show drop indicator | Target row gets 3px blue top border on `td` (`.drag-over`) |
| **Drag over** dependency | Prevent drop | Cursor becomes `not-allowed`; `dropEffect = 'none'` |
| **Drop** | Reorder priorities | DOM reorder → POST `/api/features/reorder` → re-render |
| **Drag end** | Clean up | All highlight classes removed |

### Status Dropdown (in detail panel)

| Trigger | Action | Visual Feedback |
|---------|--------|-----------------|
| **Click** dropdown | Open native `<select>` menu | Focus ring (`var(--focus-ring)`), border turns accent |
| **Change** value | PATCH `/api/features/{id}` | Row border color updates; badge updates; toast notification; `animate-flash-status` animation on badge |
| **Click** (propagation) | `event.stopPropagation()` prevents row toggle | — |

### Filter Pills

| Trigger | Action | Visual Feedback |
|---------|--------|-----------------|
| **Click** pill | Set `_featuresFilter`; re-render table | Active pill: blue bg (`var(--accent)`), white text, shadow; inactive pills: border/bg secondary |
| **Hover** pill | — | Border becomes visible, translate -1px, box-shadow |
| **Active** pill | — | `transform: translateY(-1px); box-shadow: 0 2px 12px rgba(88,166,255,0.3)` |

### Search Input

| Trigger | Action | Visual Feedback |
|---------|--------|-----------------|
| **Input** (keystrokes) | Set `_featuresSearch`; re-render table | Table live-filters on every keystroke |
| **Focus** | — | Border color turns accent; `var(--focus-ring)` shadow |
| **Clear** | Reset filter | Full list restored (respecting active status filter) |

### View Toggle

| Trigger | Action | Visual Feedback |
|---------|--------|-----------------|
| **Click** "☰" (list) | Hide graph, show table | List button gains `.active` (blue bg) |
| **Click** "◈" (graph) | Hide table, show graph; fetch `/api/dependencies`; render canvas | Graph button gains `.active` (blue bg) |

### Dependency Graph Canvas

| Trigger | Action | Visual Feedback |
|---------|--------|-----------------|
| **Hover** node | Highlight connected edges/nodes; dim others | Hovered node: 2.5px status-colored border, glow shadow; unconnected elements: alpha 0.1 |
| **Click** node | Navigate to `#features/{id}` (auto-expand in list view) | — |
| **Mouse drag** (background) | Pan graph | Canvas origin translates |
| **Mouse wheel** | Zoom in/out | Canvas scale changes |

### Clickable Feature Links

| Trigger | Action | Visual Feedback |
|---------|--------|-----------------|
| **Click** `.clickable-feature` | `App.navigateTo('features', featureId)` | Navigates to list view with that feature expanded |

### Roadmap Item Link

| Trigger | Action | Visual Feedback |
|---------|--------|-----------------|
| **Click** `.feature-roadmap-link` | Navigate to roadmap page | Standard link hover: underline |

### Discussion Item

| Trigger | Action | Visual Feedback |
|---------|--------|-----------------|
| **Click** `.clickable-discussion` | Navigate to discussions page | Row background on hover |

---

## State Handling

### Client-Side State

| Variable | Type | Default | Purpose |
|----------|------|---------|---------|
| `_featuresData` | `Feature[]` | `[]` | Cached feature list from last API fetch |
| `_featuresFilter` | `string` | `'all'` | Active status filter pill |
| `_featuresSearch` | `string` | `''` | Current search query |
| `_expandedFeatureId` | `string \| null` | `null` | Currently expanded feature row ID |
| `_breadcrumbDetail` | `string \| null` | `null` | Feature name shown in breadcrumb when expanded |
| `_navContext.id` | `string \| null` | from URL hash | Feature ID from deep-link navigation |

### Live Updates (WebSocket)

The page subscribes to the WebSocket at `/ws`. On any database change:

1. Server detects SQLite file change via file watcher.
2. Server pushes update over WebSocket.
3. Client re-fetches `/api/features` and re-renders the table.
4. Changed rows receive a flash animation:
   - **Updated row**: `animate-flash-update` — brief amber background highlight (1.8s ease-out).
   - **New row**: `animate-flash-new` — brief green background + left border (2s ease-out).
   - **Status change**: `animate-flash-status` — pulsing blue box-shadow (1.5s ease-out).

### Lazy-Loading Pattern

Detail sections within an expanded row use a `[data-loaded="1"]` marker to avoid redundant API calls:

1. First expand: section container exists but `data-loaded` is unset.
2. `expandFeature(fid)` checks each section; if not loaded, fetches data and renders.
3. Sets `data-loaded="1"` on the section container.
4. Subsequent expands of the same feature skip the fetch (data is already rendered in the DOM).

### Error States

| Scenario | Behavior |
|----------|----------|
| API fetch fails | Toast notification with error message |
| Status PATCH fails | Toast with error; dropdown reverts to previous value |
| Reorder POST fails | Toast with error; page re-renders from server state |
| Empty response | Appropriate empty state displayed (see US-14) |
| WebSocket disconnect | Automatic reconnection attempts; stale data indicator (if applicable) |

---

## Accessibility Notes

### Keyboard Navigation

| Key | Context | Action |
|-----|---------|--------|
| `Tab` | Page | Moves focus through filter pills → view toggle → search input → table rows |
| `Enter` / `Space` | Filter pill | Activates filter |
| `Enter` / `Space` | Table row | Expands/collapses detail (via click handler on `cursor: pointer` row) |
| `Enter` / `Space` | View toggle button | Switches view |
| Arrow keys | Status dropdown | Navigate options in native `<select>` |
| `Escape` | Expanded detail | Should collapse detail row (future enhancement) |

### ARIA & Semantic Markup

- **Table**: Uses semantic `<table>`, `<thead>`, `<tbody>`, `<tr>`, `<th>`, `<td>` elements.
- **Sticky headers**: `<th>` elements use `position: sticky; top: 0` with `z-index: 10` for scroll persistence.
- **Status badges**: Use text labels (not color alone) — each badge includes the status name in uppercase text.
- **Priority dots**: Include a text label next to the colored dot (e.g., "Critical", "High") — not color-only.
- **Progress bars**: Include a percentage text label next to the visual bar (e.g., "40%").
- **Search input**: Has a `placeholder="Search features…"` attribute. The 🔍 icon is decorative (CSS `::before`, `pointer-events: none`).
- **Filter pills**: Interactive `<button>` elements (natively focusable and activatable).
- **Status dropdown**: Native `<select>` element — inherits browser accessibility behavior including keyboard navigation and screen reader announcements.
- **Focus indicators**: All interactive elements show `var(--focus-ring)` (box-shadow) on `:focus`.

### Color Contrast

- Status badges use high-contrast text-on-background pairs (e.g., green text on dark green background in dark mode, dark green text on light green background in light mode).
- Light mode overrides are provided for all status badges, priority badges, and dependency nodes via `[data-theme="light"]` selectors.
- Priority dots use a glow effect in addition to color, providing a secondary visual cue.

### Motion

- All animations use `ease-out` timing and complete within 2 seconds.
- Users who prefer reduced motion should see no animation (future enhancement: respect `prefers-reduced-motion` media query).

### Responsive Behavior

| Breakpoint | Adaptation |
|-----------|-----------|
| ≤ 768px | Toolbar stacks vertically; filter pills get larger touch targets (44px min-height); table gains horizontal scroll; description max-width reduced to 200px |
| ≤ 480px | Search input goes full width; toolbar fully columnar; same table scroll behavior |

### Drag-and-Drop Accessibility

- Drag handles use `cursor: grab` / `cursor: grabbing` to indicate draggability.
- Rows use `draggable="true"` HTML attribute.
- Dependency constraint violations are communicated visually via red/green shadows and `dropEffect = 'none'`.
- **Future enhancement**: Keyboard-accessible reordering (e.g., Alt+Arrow to move rows) is not currently implemented.
