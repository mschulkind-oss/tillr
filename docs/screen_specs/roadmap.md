# Roadmap — Screen Specification

## Overview

The Roadmap page is a strategic planning dashboard that visualizes the project's prioritized backlog. It provides four distinct views — Priority, Category, Timeline, and Dependencies — enabling product owners and agents to understand what work matters most, how items relate to each other, and what progress looks like at a glance.

The page is rendered client-side by `renderRoadmap()` in `app.js` (line 614), with supporting functions in `app3.js` for the hero banner, timeline view, progress calculations, and drag-and-drop reordering. Data is served by the Go backend via `/api/roadmap` endpoints defined in `server.go`.

**Route:** `#roadmap` (SPA hash navigation)

---

## User Roles & Personas

| Persona | Description | Primary Use |
|---------|-------------|-------------|
| **Product Owner** | Human stakeholder who defines priorities and approves work. | Review strategic priorities, reorder items, change statuses, approve/defer work. |
| **Agent Operator** | Human managing AI agents working on the project. | Understand what the agents should work on next, verify linked feature progress. |
| **AI Agent** | Automated system consuming `tillr next --json`. | Indirectly affected — roadmap priority determines what `tillr next` returns. |
| **Stakeholder** | Non-technical viewer needing a project overview. | View the timeline, print/export the roadmap for presentations. |

---

## User Stories

### US-1: View Roadmap Overview

**As a** product owner  
**I want** to see a high-level dashboard of all roadmap items  
**So that** I can quickly assess project health and progress.

**Acceptance Criteria:**

```gherkin
Given the project has roadmap items in various statuses
When I navigate to the Roadmap page
Then I see a hero banner containing:
  - An SVG progress ring showing percentage of items completed (green ring for roadmap items, purple ring for linked features)
  - Total roadmap item count
  - Linked feature completion ratio (e.g., "5/12 linked features done (42%)")
  - Status breakdown pills (Proposed, Accepted, In Progress, Completed, Deferred) with counts
  - Category chips with counts
  - Priority breakdown horizontal bar chart (one row per priority level)
And I see a Priority Distribution stacked bar chart below the hero
And I see a Category Distribution stacked bar chart with legend
And I see a compact summary line (e.g., "15 items · 3 critical · 5 high · 4 medium · 2 low · 1 nice to have")

Given the project has no roadmap items
When I navigate to the Roadmap page
Then I see an empty state with:
  - A 🗺️ icon
  - The message "Your roadmap is wide open"
  - The hint "Chart the course for your project by adding your first roadmap item."
  - A CLI prompt: `$ tillr roadmap add <title>`
```

### US-2: View Items Grouped by Priority

**As a** product owner  
**I want** to see roadmap items grouped by priority level  
**So that** I can focus on the most impactful work first.

```gherkin
Given roadmap items exist with various priorities
When I am on the Roadmap page with the "Priority" view active (default)
Then items are grouped under collapsible priority headers
And the priority groups appear in order: Critical, High, Medium, Low, Nice-to-Have
And each group header shows:
  - A color-coded priority icon (🔴 Critical, 🟠 High, 🟡 Medium, 🟢 Low, 🔵 Nice-to-Have)
  - The priority label in uppercase
  - A count badge (e.g., "3 items")
And each group has a vertical timeline line on the left, tinted to match the priority color
And each item card shows:
  - A left heat strip colored by priority (gradient bar on the card's left edge)
  - A sequential rank number badge
  - The item title (bold, 0.95rem)
  - A category pill (if the item has a category)
  - A truncated description (120 characters, with "show more" button if longer)
  - A feature progress bar (if linked features exist, showing done/total)
  - Inline linked feature badges (status badge + feature name + chain icon if feature has dependencies)
  - A priority badge with icon
  - An effort badge (XS/S/M/L/XL) if effort is set
  - A status badge
And items animate in with a staggered slide-in effect (0.06s delay per item)
```

### US-3: View Items Grouped by Category

**As a** product owner  
**I want** to group roadmap items by category  
**So that** I can see work distribution across different areas.

```gherkin
Given roadmap items exist with various categories
When I click the "📁 Category" view toggle button
Then items are grouped under collapsible category headers
And each category header shows:
  - A 📁 icon
  - The category name (capitalized)
  - An item count badge
  - A collapse/expand chevron (▾ / ▸)
And categories are sorted alphabetically
And items without a category appear under "uncategorized"
And each category header is color-coded using a deterministic hash (6 color classes cycling through accent, purple, warning, success, danger, teal)

When I click a category header
Then the items under that category collapse (hide) or expand (show)
And the chevron toggles between ▾ (expanded) and ▸ (collapsed)
```

### US-4: View Timeline Visualization

**As a** stakeholder  
**I want** to see a timeline/Gantt-style view of roadmap items  
**So that** I can understand relative scope and status at a glance.

```gherkin
Given roadmap items exist
When I click the "🗓️ Timeline" view toggle button
Then I see a timeline view with:
  - A header: "Timeline View" with subtitle "Items sized by effort, colored by priority, filled by status"
  - A priority legend (colored dots: critical=red, high=amber, medium=blue, low=green, nice-to-have=purple)
  - A status legend (fill-level bars: Proposed=10%, Accepted=25%, In Progress=60%, Completed=100%, Deferred=5%)
  - Horizontal swim lanes grouped by category
And each lane has:
  - A category label on the left (color-coded)
  - A track containing blocks for each item in that category
And each block:
  - Is colored by priority (border color)
  - Is sized by effort (XS=1x, S=1.5x, M=2x, L=3x, XL=4x width)
  - Is filled proportionally by status (via CSS custom property --fill-pct)
  - Shows the item title as a label
  - Shows linked feature counts if any (e.g., "3/5")
  - Has a tooltip with title, status, priority, effort, and feature counts

When I click a timeline block
Then the view switches to Priority view
And the page scrolls to the corresponding item card
And the card briefly highlights (2-second rm-highlight animation)
```

### US-5: View Dependency Graph

**As a** product owner  
**I want** to see a visual dependency graph of features  
**So that** I can understand blocking relationships.

```gherkin
Given features exist with dependency relationships (depends_on fields)
When I click the "🔗 Dependencies" view toggle button
Then I see a dependency flow visualization with:
  - A legend showing node colors: Done (green), In Progress (blue), Draft (gray), Blocked (red)
  - A topological graph laid out in columns (layers)
  - Features with no dependencies appear in the leftmost column (layer 0)
  - Features depending on others appear in subsequent columns
  - Arrow indicators (→) between columns show flow direction
And each dependency node shows:
  - The feature name
  - The feature status (colored by status: dep-done, dep-implementing, dep-draft, dep-blocked, dep-planning)
  - A tooltip with feature name, status, and dependency list
And below the graph, a "Dependency Edges" list shows all edges as "Feature A → Feature B"

Given no features have dependency relationships
Then the "🔗 Dependencies" view toggle button is hidden
```

### US-6: Filter Roadmap Items

**As a** product owner  
**I want** to filter roadmap items by priority, category, and status  
**So that** I can focus on a specific subset of work.

```gherkin
Given I am on the Roadmap page in Priority or Category view
Then I see a filter bar with three filter groups:
  - Priority: pill buttons for All, Critical, High, Medium, Low, Nice to Have (only showing priorities that have items)
  - Category: pill buttons for All plus each unique category (sorted alphabetically)
  - Status: pill buttons for All, Proposed, Accepted, In Progress, Completed, Deferred (only showing statuses that have items)
And each pill shows a count badge (number of matching items)
And the "All" pill in each group is active by default

When I click a filter pill
Then it becomes active (highlighted with accent color)
And only items matching ALL active filters are shown
And empty priority/category sections are hidden entirely
And filter state persists across re-renders within the session

Given I switch to Timeline or Dependencies view
Then the filter bar is hidden

Given I switch back to Priority or Category view
Then the filter bar reappears with previously selected filters still active
```

### US-7: Expand Item Details

**As a** product owner  
**I want** to expand a roadmap item to see its full details  
**So that** I can review all metadata and take action.

```gherkin
Given I am viewing roadmap items in Priority or Category view
When I click on a roadmap item card
Then the item expands to show a details panel containing:
  - Full description text
  - ID (in monospace font)
  - Category (if set)
  - Effort badge with icon (🟢 XS, 🔵 S, 🟡 M, 🟠 L, 🔴 XL)
  - Created date (formatted)
  - Status action buttons (Proposed ○, Accepted ◉, In Progress ◔, Completed ✓, Deferred ⏸)
    - The current status button is dimmed and disabled (rs-active class)
  - Linked Features section showing enriched feature cards with:
    - Feature status badge
    - Feature name
    - Feature priority badge with icon
    - Dependency chain icon (⛓️) if the feature has dependencies
    - Collapsible spec section (▸ Spec toggle → reveals pre-formatted spec text)
And any previously expanded item collapses
And the breadcrumb updates to show the item title

When I click an expanded item again
Then it collapses
And the breadcrumb clears the detail segment

When I press Escape
Then all expanded items collapse
And the currently focused item loses focus
```

### US-8: Change Item Status

**As a** product owner  
**I want** to change a roadmap item's status inline  
**So that** I can update progress without leaving the page.

```gherkin
Given a roadmap item is expanded showing status action buttons
When I click a status button (e.g., "◔ In Progress")
Then a PATCH request is sent to /api/roadmap/{id}/status with { "status": "in-progress" }
And a success toast appears: "Status changed to in-progress"
And the page re-renders with updated data
And the same item remains expanded after re-render (via _expandedRoadmapId tracking)

Given the PATCH request fails
Then an error toast appears: "Failed to change status"
And the item remains in its previous state

Given the PATCH request returns a validation error
Then an error toast appears: "Error: {message}"
```

### US-9: Drag-and-Drop Reorder

**As a** product owner  
**I want** to reorder roadmap items by dragging them  
**So that** I can adjust priorities within a group.

```gherkin
Given I am viewing roadmap items in Priority or Category view
Then each item card has draggable="true" set

When I start dragging an item card
Then the card gets a "dragging" CSS class (visual feedback: reduced opacity, scaled down)
And related items are highlighted:
  - Items whose linked features DEPEND ON the dragged item's linked features get a red outline (dep-blocker class)
  - Items whose linked features ARE DEPENDED ON by the dragged item's features get a green outline (dep-dependency class)

When I drag over another item in the same priority/category group
Then the target item gets a "drag-over" CSS class (blue dashed border highlight)

When I drop an item onto another item in the same group
Then the dragged item is repositioned before or after the target (based on original position)
And a POST request is sent to /api/roadmap/reorder with the new sort order:
  { "items": [{ "id": "item-1", "sort_order": 1 }, { "id": "item-2", "sort_order": 2 }, ...] }
And the page re-renders with the new order

When I drop an item into a different priority group
Then the item is moved into that group at the drop position
And the reorder API is called for the target group

When I finish dragging (dragend)
Then the "dragging" class is removed from the source
And all "dep-blocker", "dep-dependency", and "drag-over" classes are cleared from all items

Given the reorder API call fails
Then the error is logged to console
And the page re-renders (restoring server state)
```

### US-10: Navigate Between Linked Features

**As a** product owner  
**I want** to click on a linked feature to navigate to it  
**So that** I can drill into feature details.

```gherkin
Given a roadmap item is expanded and shows linked features
When I click on a linked feature badge (inline or in the enriched section)
Then I navigate to the Features page with the clicked feature expanded

Given a roadmap item shows inline feature badges in its collapsed state
When I click an inline feature badge
Then the click is handled by the feature navigation handler (bindClickableFeatures)
And I navigate to the Features page with that feature selected
```

### US-11: Switch Between Views

**As a** product owner  
**I want** to toggle between different roadmap visualizations  
**So that** I can analyze my roadmap from different perspectives.

```gherkin
Given I am on the Roadmap page
Then I see a view toggle bar with buttons:
  - "📊 Priority" (default, active)
  - "📁 Category"
  - "🗓️ Timeline"
  - "🔗 Dependencies" (only shown if features with dependencies exist)

When I click a view toggle button
Then the selected view becomes visible
And all other views are hidden
And the clicked button gets the "active" class (accent background, white text)
And the view preference persists for the session (stored in _roadmapView)

When switching to Timeline or Dependencies
Then the filter bar is hidden

When switching to Priority or Category
Then the filter bar is shown
```

### US-12: Keyboard Navigation

**As a** user who relies on keyboard navigation  
**I want** to navigate and interact with roadmap items using the keyboard  
**So that** I can use the page without a mouse.

```gherkin
Given I am on the Roadmap page with items visible
When I press the ↓ (ArrowDown) key
Then focus moves to the next roadmap item in the list (wraps to first from last)

When I press the ↑ (ArrowUp) key
Then focus moves to the previous roadmap item (wraps to last from first)

When I press Enter or Space on a focused item
Then the item expands or collapses (same as click)

When I press Escape
Then all items collapse
And the focused item loses focus

And a keyboard hint is shown at the bottom of the page:
  "Tip: Use ↑↓ to navigate, Enter to expand"
```

### US-13: Show More / Truncated Descriptions

**As a** product owner  
**I want** long descriptions to be truncated with a "show more" option  
**So that** the item list stays compact but I can read full details when needed.

```gherkin
Given a roadmap item has a description longer than 120 characters
Then the description is truncated at 120 characters with an ellipsis (…)
And a "show more" button appears inline

When I click "show more"
Then the full description replaces the truncated text
And the "show more" button disappears
And the click event does not propagate to the item card (no expand/collapse)

Given a roadmap item has a description of 120 characters or fewer
Then the full description is shown with no "show more" button
```

### US-14: Print / Export Roadmap

**As a** stakeholder  
**I want** to print or export the roadmap  
**So that** I can share it in meetings or documentation.

```gherkin
Given I am on the Roadmap page
Then I see a "🖨️ Print / Export" button in the page header

When I click the Print / Export button
Then the browser's native print dialog opens (window.print())
And the roadmap is rendered in a print-friendly format (via @media print styles)
```

### US-15: Auto-Expand on Navigation

**As a** user navigating from another page  
**I want** the relevant roadmap item to be automatically expanded  
**So that** I can see the item I was looking for without manual searching.

```gherkin
Given I navigate to the Roadmap page with a navigation context containing an item ID
  (e.g., from a timeline block click or a deep link)
When the page finishes rendering
Then the item with the matching ID is automatically expanded
And the page scrolls to center it in the viewport (smooth scroll)
And the navigation context is cleared after use

Given the page re-renders after a status change
And _expandedRoadmapId is set
Then the previously expanded item is re-expanded after render
And _expandedRoadmapId is cleared
```

### US-16: Real-Time Updates via WebSocket

**As a** product owner  
**I want** the roadmap to update in real time  
**So that** I see changes made by agents or other users immediately.

```gherkin
Given I am viewing the Roadmap page
And another user or agent changes a roadmap item (via CLI or API)
When a WebSocket message is received on /ws
Then the Roadmap page re-renders with fresh data from the API
And previously expanded items remain expanded (via _expandedRoadmapId)
And filter state is preserved (via roadmapFilters)
And the active view is preserved (via _roadmapView)
```

### US-17: View Spec for Linked Features

**As a** product owner  
**I want** to view the specification of linked features inline  
**So that** I can review acceptance criteria without leaving the roadmap.

```gherkin
Given a roadmap item is expanded and has linked features with specs
Then each linked feature card shows a "▸ Spec" toggle button

When I click the "▸ Spec" toggle
Then the spec content slides open (max-height animation from 0 to scrollHeight)
And the chevron changes to ▾
And the spec text is shown in a pre-formatted block (monospace)

When I click the toggle again
Then the spec content slides closed (max-height back to 0)
And the chevron changes back to ▸

When I click the toggle
Then the click event does not propagate to the parent item card
```

---

## Screen Layout

### Page Structure (top to bottom)

```
┌─────────────────────────────────────────────────────────┐
│ PAGE HEADER                                             │
│ ┌─────────────────────┐ ┌──────────┐ ┌───────────────┐  │
│ │ Roadmap (h2)        │ │ View     │ │ 🖨️ Print /   │  │
│ │                     │ │ Toggle   │ │ Export        │  │
│ └─────────────────────┘ └──────────┘ └───────────────┘  │
│ Strategic priorities and planned work — ranked by impact │
│ 15 items · 3 critical · 5 high · 4 medium · ...        │
├─────────────────────────────────────────────────────────┤
│ HERO BANNER (rm-hero-banner)                            │
│ ┌──────────┐ ┌──────────────────┐ ┌──────────────────┐  │
│ │ Progress │ │ Status    Cats   │ │ Priority         │  │
│ │ Ring     │ │ ┌──┐┌──┐┌──┐    │ │ Breakdown        │  │
│ │  [SVG]   │ │ │3 ││2 ││5 │    │ │ 🔴 critical ██ 3│  │
│ │  42%     │ │ └──┘└──┘└──┘    │ │ 🟠 high    ███ 5│  │
│ │ complete │ │ ┌─┐┌──┐┌───┐   │ │ 🟡 medium  ██ 4 │  │
│ │ 5/12 feat│ │ │A││UX││Inf│   │ │ 🟢 low     █ 2  │  │
│ └──────────┘ └──────────────────┘ └──────────────────┘  │
├─────────────────────────────────────────────────────────┤
│ DISTRIBUTION CHARTS                                     │
│ Priority: [██████████████████████████████████████]       │
│           critical  high  medium  low  nice-to-have     │
│ Category: [██████████████████████████████████████]       │
│           api  ux  infrastructure  ...                  │
├─────────────────────────────────────────────────────────┤
│ FILTER BAR (hidden in Timeline/Dependencies view)       │
│ Priority: [All|Critical|High|Medium|Low|Nice to Have]   │
│ Category: [All|api|infrastructure|ux|...]               │
│ Status:   [All|Proposed|Accepted|In Progress|...]       │
├─────────────────────────────────────────────────────────┤
│ VIEW CONTENT (one visible at a time)                    │
│                                                         │
│ ┌─ PRIORITY VIEW (default) ─────────────────────────┐   │
│ │ 🔴 CRITICAL — 3 items                             │   │
│ │ │  ┌─────────────────────────────────────────┐     │   │
│ │ ├──│ 1 │ Item Title    │ cat │ 🔴 crit │ M │ │   │   │
│ │ │  │   │ Description...│     │ accepted  │   │     │   │
│ │ │  │   │ ████░░ 2/3   │     │           │   │     │   │
│ │ │  │   │ [done] Feat A │ [impl] Feat B  │   │     │   │
│ │ │  └─────────────────────────────────────────┘     │   │
│ │ ├──[Item 2 card...]                                │   │
│ │ ├──[Item 3 card...]                                │   │
│ │                                                    │   │
│ │ 🟠 HIGH — 5 items                                  │   │
│ │ ├──[Item 4 card...]                                │   │
│ │ └──...                                             │   │
│ └────────────────────────────────────────────────────┘   │
│                                                         │
│ ┌─ CATEGORY VIEW (hidden until selected) ───────────┐   │
│ │ 📁 api — 4 items ▾                                 │   │
│ │   [Item cards...]                                  │   │
│ │ 📁 infrastructure — 3 items ▾                      │   │
│ │   [Item cards...]                                  │   │
│ └────────────────────────────────────────────────────┘   │
│                                                         │
│ ┌─ TIMELINE VIEW (hidden until selected) ───────────┐   │
│ │ Timeline View                                      │   │
│ │ Items sized by effort, colored by priority...      │   │
│ │ Legend: priority dots + status fill bars            │   │
│ │ ┌─────┬─────────────────────────────────────┐      │   │
│ │ │ api │ [████][██████████][████████]         │      │   │
│ │ │ ux  │ [██████][████]                      │      │   │
│ │ │ inf │ [████████████████]                  │      │   │
│ │ └─────┴─────────────────────────────────────┘      │   │
│ └────────────────────────────────────────────────────┘   │
│                                                         │
│ ┌─ DEPENDENCY VIEW (hidden until selected) ─────────┐   │
│ │ Legend: ● Done  ● In Progress  ● Draft  ● Blocked │   │
│ │ ┌──────┐    ┌──────┐    ┌──────┐                   │   │
│ │ │Feat A│ →  │Feat C│ →  │Feat E│                   │   │
│ │ │ done │    │ impl │    │draft │                   │   │
│ │ └──────┘    └──────┘    └──────┘                   │   │
│ │ ┌──────┐    ┌──────┐                               │   │
│ │ │Feat B│ →  │Feat D│                               │   │
│ │ │ done │    │draft │                               │   │
│ │ └──────┘    └──────┘                               │   │
│ │                                                    │   │
│ │ Dependency Edges:                                  │   │
│ │ Feat A → Feat C                                    │   │
│ │ Feat B → Feat D                                    │   │
│ │ Feat C → Feat E                                    │   │
│ └────────────────────────────────────────────────────┘   │
│                                                         │
│ Tip: Use ↑↓ to navigate, Enter to expand                │
└─────────────────────────────────────────────────────────┘
```

### Expanded Item Card Layout

```
┌──────────────────────────────────────────────────────────┐
│ [heat] [#] │ Title                      │ [cat pill]     │
│            │ Description (truncated)... show more        │
│            │ ████░░ 2/3 features                         │
│            │ [done] Feature A  [impl] Feature B ⛓️       │
│            │                     [🔴 crit] [M] [accepted]│
├──────────────────────────────────────────────────────────┤
│ DETAILS PANEL (expanded)                                 │
│ ┌────────────────────────────────────────────────────┐   │
│ │ Full description text                              │   │
│ │                                                    │   │
│ │ ID          my-roadmap-item-id                     │   │
│ │ Category    infrastructure                         │   │
│ │ Effort      🟡 M                                   │   │
│ │ Created     Jan 15, 2025                           │   │
│ │                                                    │   │
│ │ Status  [○ Proposed][◉ Accepted][◔ In Progress]    │   │
│ │         [✓ Completed][⏸ Deferred]                  │   │
│ │                                                    │   │
│ │ 🔗 Linked Features (3)                             │   │
│ │ ┌──────────────────────────────────────────────┐   │   │
│ │ │ [done] Feature A  [🔴 critical]              │   │   │
│ │ │ ▸ Spec                                       │   │   │
│ │ ├──────────────────────────────────────────────┤   │   │
│ │ │ [impl] Feature B  [🟠 high] ⛓️               │   │   │
│ │ │ ▸ Spec                                       │   │   │
│ │ └──────────────────────────────────────────────┘   │   │
│ └────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────┘
```

---

## Data Requirements

### API Endpoints

| Method | Endpoint | Description | Query Parameters |
|--------|----------|-------------|------------------|
| `GET` | `/api/roadmap` | List all roadmap items | `category`, `priority`, `status`, `sort` |
| `PATCH` | `/api/roadmap/{id}/status` | Update item status | — |
| `POST` | `/api/roadmap/reorder` | Reorder items | — |
| `GET` | `/api/features` | List all features (for linking) | — |

### GET /api/roadmap Response

Returns `RoadmapItem[]`:

```json
[
  {
    "id": "api-caching",
    "project_id": "my-project",
    "title": "API Caching Layer",
    "description": "Implement Redis-based caching for hot API paths",
    "category": "infrastructure",
    "priority": "critical",
    "status": "in-progress",
    "effort": "l",
    "sort_order": 1,
    "created_at": "2025-01-15T10:30:00Z",
    "updated_at": "2025-01-20T14:00:00Z"
  }
]
```

### Field Enumerations

| Field | Valid Values |
|-------|-------------|
| `priority` | `critical`, `high`, `medium`, `low`, `nice-to-have` |
| `status` | `proposed`, `accepted`, `in-progress`, `done`, `deferred` |
| `effort` | `xs`, `s`, `m`, `l`, `xl` |

### PATCH /api/roadmap/{id}/status Request/Response

Request:
```json
{ "status": "in-progress" }
```

Response (success):
```json
{ "ok": true }
```

Response (error — invalid status):
```json
{ "error": "invalid status: bogus" }
```

Response (error — not found):
```json
{ "error": "roadmap item not found" }
```

### POST /api/roadmap/reorder Request/Response

Request:
```json
{
  "items": [
    { "id": "item-a", "sort_order": 1 },
    { "id": "item-b", "sort_order": 2 },
    { "id": "item-c", "sort_order": 3 }
  ]
}
```

Response:
```json
{ "ok": true }
```

### Feature Linking

Features are linked to roadmap items via the `roadmap_item_id` field on the Feature model. The client builds a lookup map `featuresByRoadmap[roadmapItemId] → Feature[]` to resolve linked features per item.

Relevant Feature fields used on this page:
- `id`, `name`, `status`, `priority`, `spec`, `depends_on[]`, `roadmap_item_id`

### Derived/Computed Data

| Computation | Description |
|-------------|-------------|
| **Completion %** | `(items with status "done" / total items) × 100` |
| **Feature completion %** | `(linked features with status "done" / total linked features) × 100` |
| **Item progress** | Per item: `done linked features / total linked features` — shown as progress bar |
| **Priority distribution** | Count of items per priority level — stacked bar chart |
| **Category distribution** | Count of items per category — stacked bar chart |
| **Status breakdown** | Count of items per status — hero pills |
| **Dependency layers** | Topological sort of features by `depends_on` — used for dependency graph column layout |
| **Category hash** | Deterministic hash of category string → one of 6 color classes (roadmap-cat-0 through roadmap-cat-5) |

---

## Interactions

### Click Interactions

| Element | Action | Result |
|---------|--------|--------|
| Item card | Click | Toggle expand/collapse; update breadcrumb |
| Status button (in expanded details) | Click | PATCH status via API; toast; re-render; re-expand same item |
| Filter pill | Click | Set filter; toggle active class; show/hide items; hide empty sections |
| View toggle button | Click | Switch visible view; toggle button active state; show/hide filter bar |
| Category header (Category view) | Click | Collapse/expand items under that category; toggle chevron |
| "Show more" button | Click (stopPropagation) | Replace truncated description with full text |
| Inline feature badge | Click | Navigate to Features page with that feature expanded |
| Linked feature card | Click | Navigate to Features page with that feature expanded |
| Spec toggle (▸ Spec) | Click (stopPropagation) | Slide open/close spec content (max-height animation) |
| Timeline block | Click | Switch to Priority view; scroll to item; highlight for 2s |
| Dependency node | Click | Navigate to Features page with that feature expanded |
| Print / Export button | Click | Open browser print dialog |

### Drag-and-Drop Interactions

| Event | Handler | Visual Feedback |
|-------|---------|-----------------|
| `dragstart` | Set `dragging` class; compute dependency relationships; highlight related items | Card becomes semi-transparent; blockers get red outline; dependencies get green outline |
| `dragover` | Prevent default; set `drag-over` class on target | Target card gets blue dashed border |
| `dragleave` | Remove `drag-over` class | Border reverts |
| `drop` | Reposition DOM node; compute new sort order; POST to `/api/roadmap/reorder` | Page re-renders with new order |
| `dragend` | Remove `dragging`, `dep-blocker`, `dep-dependency`, `drag-over` classes | All visual feedback cleared |

### Keyboard Interactions

| Key | Context | Action |
|-----|---------|--------|
| `↓` (ArrowDown) | Roadmap page | Focus next item (wraps around) |
| `↑` (ArrowUp) | Roadmap page | Focus previous item (wraps around) |
| `Enter` / `Space` | Focused item | Toggle expand/collapse |
| `Escape` | Roadmap page | Collapse all items; blur focused item |

---

## State Handling

### Client-Side State

| Variable | Type | Purpose | Tillr |
|----------|------|---------|-----------|
| `_roadmapData` | `RoadmapItem[]` | Cached API response for current render | Set on each render; used by drag-and-drop |
| `_roadmapFeatures` | `Feature[]` | Cached features list | Set on each render; used by dependency highlighting |
| `_roadmapView` | `string` | Active view (`priority`, `category`, `timeline`, `dependencies`) | Persists across renders within session; defaults to `priority` |
| `roadmapFilters` | `{ category, status, priority }` | Active filter selections | Persists across renders within session; defaults to all `'all'` |
| `_expandedRoadmapId` | `string \| null` | ID of item to auto-expand after re-render | Set before re-render (status change); cleared after use |
| `_navContext` | `{ id }` | Navigation context from external page | Set by router; cleared after use |
| `_breadcrumbDetail` | `string \| null` | Currently expanded item title for breadcrumb | Set on expand; cleared on collapse |

### Loading & Empty States

| State | Display |
|-------|---------|
| **Loading** | Spinner shown while `renderRoadmap()` awaits API responses (standard SPA loading pattern) |
| **Empty (no items)** | Full-page empty state: 🗺️ icon, "Your roadmap is wide open" message, CLI hint |
| **Filtered to empty** | Priority/category sections with no matching items are hidden via `display: none` |
| **No dependencies** | Dependencies view toggle button is hidden entirely |
| **Timeline empty** | "No items to display" message inside timeline container |

### Error States

| Error | Handling |
|-------|----------|
| API fetch failure | Caught by `api()` wrapper; may show generic error |
| Status change fails (network) | Toast: "Failed to change status" |
| Status change fails (validation) | Toast: "Error: {server error message}" |
| Reorder fails | Error logged to console; page re-renders to restore server state |

---

## Accessibility Notes

### ARIA & Semantic HTML

- Each roadmap item has `role="listitem"` and `tabindex="0"` for keyboard focus.
- Item containers have `role="list"`.
- The keyboard hint has `aria-hidden="true"` (decorative guidance).
- Priority icons have `aria-hidden="true"` on the `<span>` wrapper.
- Toast container has `role="status"` and `aria-live="polite"` for screen reader announcements.
- Focus is visually indicated via `focus-visible` outline (`:focus-visible { box-shadow: var(--focus-ring) }`).

### Color & Contrast

- Priority levels use both color AND icon (🔴🟠🟡🟢🔵) for non-color-dependent identification.
- Status is conveyed via both color and text label.
- Effort badges use both color and text (XS/S/M/L/XL).
- Dependency graph nodes use both color-coded borders AND text status labels.
- Dark mode and light mode have distinct theme-specific color values to maintain contrast.

### Keyboard

- Full arrow-key navigation (↑↓) for moving between items.
- Enter/Space to expand/collapse.
- Escape to collapse all and clear focus.
- All interactive elements (buttons, cards) are keyboard-reachable.

### Motion

- Staggered slide-in animations on item cards (`roadmapSlideIn`, 0.06s delay per item).
- Hover transitions on cards (`transform: translateY(-3px)`) respect no-preference for reduced motion (handled by CSS `prefers-reduced-motion` where supported).
- Progress ring uses `transition: stroke-dashoffset 1s ease` for animated fill.
- Smooth scrolling for auto-expand navigation (`behavior: 'smooth'`).

### Responsive Design

- At smaller viewports (≤768px): item cards stack vertically, meta wraps below content, summary grid reduces to 2 columns.
- At ≤480px: summary grid becomes single column, padding and font sizes reduce, timeline line positioning adjusts.
- Print media query applies separate layout optimizations for export.
