# Spec Document — Screen Specification

## Overview

The **Spec Document** page (`📋 Spec Doc`) is a dynamically-generated, read-only living specification document that synthesizes all project data — features, milestones, discussions, and recent activity — into a single structured document. It is accessed from the sidebar navigation under the **Insights** section (`data-page="spec"`) and is rendered by `App.renderSpec()` in `app4.js`.

The page fetches its data from the `GET /api/spec-document` endpoint, which assembles the full document server-side by querying milestones, features (with dependency data), roadmap items, discussions, and events from the SQLite database. The result is a JSON payload containing a title, generation timestamp, ordered sections (each with optional embedded features), and aggregate statistics.

**Primary purpose:** Provide a printable, stakeholder-ready view of the entire project specification without requiring manual document maintenance.

**Route:** `#spec` (client-side hash routing)
**API:** `GET /api/spec-document`
**Renderer:** `App.renderSpec()` (app4.js, line 373)
**Event binding:** `App._bindSpecEvents()` (app4.js, line 446)

---

## User Roles & Personas

| Persona | Description | Primary use of this page |
|---------|-------------|--------------------------|
| **Product Owner** | Human stakeholder who defines features and reviews progress | Read the full specification, print it for meetings, verify feature coverage per milestone |
| **Engineering Lead** | Technical leader overseeing implementation | Review feature statuses, check dependency chains, assess milestone progress |
| **Agent (AI)** | Automated agent performing development work | Generally does not use this page directly (agents use `tillr next --json` via CLI), but the underlying API could be consumed programmatically |
| **QA Reviewer** | Human or agent performing quality assurance | Cross-reference the spec document against implemented features, verify completeness |
| **External Stakeholder** | Non-technical viewer (e.g., client, manager) | Read the printed version for a high-level understanding of project scope and progress |

---

## User Stories

### US-1: View the full specification document

**As a** product owner,
**I want to** see a complete, auto-generated specification document for my project,
**So that** I don't have to manually maintain a separate spec document.

**Acceptance Criteria:**

```gherkin
Given the project has milestones, features, discussions, and events
When I navigate to the Spec Doc page
Then I see a document titled "{ProjectName} — Software Specification"
And the subtitle shows "Generated {timestamp}" with the current date and time
And an executive summary section is displayed at the top
And features are grouped by milestone in subsequent sections
And each milestone section is labeled "Phase N: {milestone-id} — {milestone-name}"
```

---

### US-2: View executive summary statistics

**As a** product owner,
**I want to** see a quick statistical overview at the top of the document,
**So that** I can assess project health at a glance.

**Acceptance Criteria:**

```gherkin
Given the project has features in various statuses
When the Spec Document page loads
Then I see a stats grid with four cards:
  | Card Label    | Value Source                        |
  | Features      | Total number of features            |
  | Done          | Features with status "done"         |
  | In Progress   | Features with status "implementing" |
  | Milestones    | Total number of milestones          |
And the executive summary section contains a prose paragraph:
  "{ProjectName} is a project with N features across M milestones (Milestone1, Milestone2, ...)"
And the summary lists feature counts by status:
  "Feature Status: X done, Y in progress, Z planning, W blocked, V draft"
And the summary shows the total roadmap item count
```

---

### US-3: Navigate via table of contents

**As a** reader of the spec document,
**I want to** click on table of contents entries to jump to sections,
**So that** I can quickly navigate a long document.

**Acceptance Criteria:**

```gherkin
Given the spec document has multiple sections
When the page renders
Then a sticky sidebar appears on the left (220px wide)
And the sidebar contains a "Table of Contents" heading
And one link per section is listed below the heading
And each link displays the section title

When I click a TOC link
Then the page smoothly scrolls to the corresponding section
And the browser does NOT navigate away or change the URL hash
```

```gherkin
Given I scroll down the document
When the TOC sidebar reaches the top of the viewport
Then it remains fixed (sticky) at top:16px
And the TOC scrolls independently if its content exceeds 80vh
```

---

### US-4: View features grouped by milestone

**As an** engineering lead,
**I want to** see features organized under their respective milestones,
**So that** I can understand what work belongs to each project phase.

**Acceptance Criteria:**

```gherkin
Given a project has milestones with assigned features
When the spec document renders
Then each milestone appears as a section titled "Phase {N}: {milestone-id} — {milestone-name}"
And the section content shows the milestone description (if any)
And the section shows "Features: {total} total, {done} done"
And each feature within the milestone is rendered as a card

Given a feature card is displayed
Then the card shows:
  - Feature name (bold, left-aligned, clickable → navigates to Features page)
  - Status badge (color-coded: done=green, implementing=blue, planning=yellow, blocked=red, draft=purple)
  - Priority label ("P{N}", right-aligned, dimmed)
  - Description text (if present, below the name)
  - Dependency list (if present, rendered as pill-shaped badges, each clickable)
  - Expandable "Specification" details section (if spec_md is present)
```

```gherkin
Given a project has features not assigned to any milestone
When the spec document renders
Then an "Unassigned Features" section appears after all milestone sections
And it shows "{N} features not yet assigned to a milestone."
And the unassigned features are rendered as cards identical to milestone features
```

---

### US-5: Expand and read feature specifications

**As a** QA reviewer,
**I want to** expand individual feature specifications inline,
**So that** I can read the detailed acceptance criteria without leaving the document.

**Acceptance Criteria:**

```gherkin
Given a feature has a spec_md field
When the feature card is displayed
Then a collapsed "<details>" element with summary "Specification" is shown

When I click the "Specification" summary
Then the details element expands
And the spec markdown is rendered as HTML (via marked.js with GFM and line breaks)
And the rendered content appears in a styled container with secondary background color
And the container has rounded corners and 12px padding

When I click the "Specification" summary again
Then the details element collapses
```

```gherkin
Given a feature does NOT have a spec_md field
When the feature card is displayed
Then no "Specification" expander is shown
```

---

### US-6: Click feature names to navigate

**As a** user reading the spec document,
**I want to** click on a feature name or dependency badge to navigate to that feature's detail view,
**So that** I can drill into the full feature record.

**Acceptance Criteria:**

```gherkin
Given a feature name is displayed with class "clickable-feature"
When I click on the feature name
Then the app navigates to the Features page with that feature selected/expanded
And the URL hash updates to reflect the navigation

Given a dependency badge is displayed
When I click on the dependency badge
Then the app navigates to the Features page for that dependency feature
```

---

### US-7: View active discussions appendix

**As a** product owner,
**I want to** see a list of all active discussions at the end of the spec document,
**So that** I know which decisions are still open.

**Acceptance Criteria:**

```gherkin
Given the project has one or more discussions
When the spec document renders
Then an "Active Discussions" section appears in the table of contents and document body
And each discussion is listed as a bullet point: "**{title}** ({status}) — {author}"

Given the project has no discussions
When the spec document renders
Then the "Active Discussions" section is omitted entirely
```

---

### US-8: View recent activity appendix

**As an** engineering lead,
**I want to** see a summary of recent project activity at the end of the spec document,
**So that** I can understand what has happened recently without switching pages.

**Acceptance Criteria:**

```gherkin
Given the project has recorded events
When the spec document renders
Then a "Recent Activity" section appears
And it shows up to 20 events (from the most recent 50 fetched)
And each event is formatted as: "[{event_type}] {data} — {timestamp}"

Given the project has more than 20 events
When the spec document renders
Then only the 20 most recent are shown

Given the project has no events
When the spec document renders
Then the "Recent Activity" section is omitted entirely
```

---

### US-9: Print the specification document

**As a** product owner,
**I want to** print the spec document with a clean layout,
**So that** I can share a physical or PDF copy with stakeholders.

**Acceptance Criteria:**

```gherkin
Given I am viewing the spec document
When I click the "🖨️ Print" button in the page header
Then the browser's native print dialog opens

When the print preview renders
Then the following elements are hidden:
  - Sidebar navigation
  - Hamburger menu button
  - Sidebar overlay
  - Chord indicator
  - Shortcut modal overlay
  - Table of contents sidebar (.spec-toc)
  - Page subtitle (generation timestamp)
  - All buttons (.btn)
  - Theme toggle
And the content area has no left margin and 20px padding
And the document font size is set to 11pt
And feature cards avoid page breaks (break-inside: avoid)
And the stats grid avoids page breaks (break-inside: avoid)
And card borders are forced to 1px solid #ddd for print visibility
```

---

### US-10: Live update on data changes

**As a** user with the spec document open,
**I want to** see the document update automatically when project data changes,
**So that** the specification always reflects the latest state.

**Acceptance Criteria:**

```gherkin
Given I have the Spec Doc page open
And the WebSocket connection is active
When another user or agent modifies a feature, milestone, or discussion
Then the server detects the SQLite database change
And pushes a notification via WebSocket
And the page re-renders with updated data from the API
```

---

### US-11: Handle empty project state

**As a** new user who just initialized a project,
**I want to** see a meaningful spec document even with no data,
**So that** I understand the page's purpose and what to do next.

**Acceptance Criteria:**

```gherkin
Given a project has been initialized but has no features, milestones, or discussions
When I navigate to the Spec Doc page
Then the document title still shows "{ProjectName} — Software Specification"
And the stats grid shows all zeros (0 Features, 0 Done, 0 In Progress, 0 Milestones)
And the executive summary states "0 features across 0 milestones"
And no milestone sections, discussions, or activity sections appear
And the TOC contains only "Executive Summary"
```

---

### US-12: Milestone ordering

**As a** product owner,
**I want to** see milestones displayed in their defined sort order,
**So that** the spec document reflects the intended project phases sequentially.

**Acceptance Criteria:**

```gherkin
Given milestones have been created with different sort_order values
When the spec document renders
Then milestone sections appear in ascending sort_order
And each is labeled "Phase 1:", "Phase 2:", etc., matching the sort order
And features within each milestone are sorted by priority (descending) then created_at (ascending)
```

---

## Screen Layout

### Overall Structure

```
┌──────────────────────────────────────────────────────────────────────┐
│ PAGE HEADER                                                          │
│ ┌──────────────────────────────────────────────────────────────────┐ │
│ │ 📋 {ProjectName} — Software Specification        [🖨️ Print]    │ │
│ │ Generated {date and time}                                       │ │
│ └──────────────────────────────────────────────────────────────────┘ │
│                                                                      │
│ STATS GRID                                                           │
│ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐                │
│ │  {N}     │ │  {N}     │ │  {N}     │ │  {N}     │                │
│ │ Features │ │ Done     │ │ In Prog. │ │ Mileston.│                │
│ └──────────┘ └──────────┘ └──────────┘ └──────────┘                │
│                                                                      │
│ ┌────────────────┐  ┌─────────────────────────────────────────────┐ │
│ │ TABLE OF       │  │ CONTENT AREA                                │ │
│ │ CONTENTS       │  │                                             │ │
│ │ (sticky, 220px)│  │ ┌─────────────────────────────────────────┐ │ │
│ │                │  │ │ Executive Summary                       │ │ │
│ │ Executive Sum. │  │ │ {project name} is a project with...     │ │ │
│ │ Phase 1: MVP   │  │ │ Feature Status: X done, Y in prog...   │ │ │
│ │ Phase 2: Polish│  │ │ Roadmap Items: N total                  │ │ │
│ │ Unassigned     │  │ └─────────────────────────────────────────┘ │ │
│ │ Discussions    │  │                                             │ │
│ │ Recent Activity│  │ ┌─────────────────────────────────────────┐ │ │
│ │                │  │ │ Phase 1: mvp — MVP                     │ │ │
│ │                │  │ │ Description text...                     │ │ │
│ │                │  │ │ Features: 5 total, 3 done               │ │ │
│ │                │  │ │                                         │ │ │
│ │                │  │ │ ┌─────────────────────────────────────┐ │ │ │
│ │                │  │ │ │ Feature Name    [done] P10          │ │ │ │
│ │                │  │ │ │ Description text                    │ │ │ │
│ │                │  │ │ │ Depends on: [feat-a] [feat-b]      │ │ │ │
│ │                │  │ │ │ ▶ Specification (expandable)       │ │ │ │
│ │                │  │ │ └─────────────────────────────────────┘ │ │ │
│ │                │  │ │                                         │ │ │
│ │                │  │ │ ┌─────────────────────────────────────┐ │ │ │
│ │                │  │ │ │ Another Feature  [implementing] P7 │ │ │ │
│ │                │  │ │ └─────────────────────────────────────┘ │ │ │
│ │                │  │ └─────────────────────────────────────────┘ │ │
│ │                │  │                                             │ │
│ │                │  │  ... more sections ...                      │ │
│ └────────────────┘  └─────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────┘
```

### Component Hierarchy

```
div.spec-document
├── div.page-header (flex row, space-between)
│   ├── div
│   │   ├── h2.page-title          "📋 {title}"
│   │   └── div.page-subtitle      "Generated {timestamp}"
│   └── div
│       └── button.btn              "🖨️ Print" (onclick: window.print())
├── div.stats-grid
│   ├── div.stat-card  →  stat-value + stat-label ("Features")
│   ├── div.stat-card  →  stat-value + stat-label ("Done")
│   ├── div.stat-card  →  stat-value + stat-label ("In Progress")
│   └── div.stat-card  →  stat-value + stat-label ("Milestones")
└── div (flex row, gap:24px)
    ├── div.spec-toc (sticky sidebar, 220px)
    │   ├── div (heading: "Table of Contents")
    │   └── a.spec-toc-item × N (one per section)
    └── div (flex:1, main content)
        └── div.spec-section#spec-{id} × N
            ├── h2 (section title, bottom border)
            ├── div.md-content (rendered markdown)
            └── div (feature cards, if section has features)
                └── div.card × N
                    ├── div (flex row: name + status/priority)
                    │   ├── strong.clickable-feature
                    │   └── div (status-badge + priority)
                    ├── div (description, optional)
                    ├── div (dependencies, optional)
                    └── details (specification, optional)
                        ├── summary "Specification"
                        └── div.md-content (rendered spec markdown)
```

---

## Data Requirements

### API Endpoint

**`GET /api/spec-document`**

Returns a JSON object assembled server-side from multiple database queries.

#### Response Schema

```json
{
  "title": "string — '{ProjectName} — Software Specification'",
  "generated_at": "string — ISO 8601 timestamp",
  "sections": [
    {
      "id": "string — section anchor identifier (e.g., 'executive-summary', 'milestone-mvp')",
      "title": "string — section heading",
      "content_md": "string — markdown body for the section",
      "level": "number — heading level (always 1)",
      "features": [
        {
          "id": "string — feature ID",
          "name": "string — feature name",
          "status": "string — one of: draft, planning, implementing, agent-qa, human-qa, done, blocked",
          "priority": "number — integer priority (higher = more important)",
          "spec_md": "string | empty — markdown specification text",
          "description": "string | empty — brief feature description",
          "dependencies": ["string — feature ID"]
        }
      ]
    }
  ],
  "stats": {
    "total_features": "number",
    "done": "number",
    "in_progress": "number — count of 'implementing' status",
    "blocked": "number",
    "total_milestones": "number",
    "total_roadmap_items": "number"
  }
}
```

### Server-Side Data Assembly

The handler (`handleSpecDocument` in `server.go`) makes the following DB queries:

| Query Function | Source Table(s) | Purpose |
|---|---|---|
| `db.GetProject()` | `projects` | Get project name for title |
| `db.ListMilestones()` | `milestones` LEFT JOIN `features` | Milestones with feature counts (total, done) |
| `db.ListFeatures()` | `features` LEFT JOIN `milestones`, `feature_deps` | All features with deps, sorted by priority DESC then created_at |
| `db.ListRoadmapItems()` | `roadmap_items` | Total count for summary |
| `db.ListDiscussions()` | `discussions`, `discussion_comments` | Active discussions with comment counts |
| `db.ListEvents()` | `events` | Last 50 events for recent activity |

### Section Assembly Order

1. **Executive Summary** (`id: "executive-summary"`) — Always present
2. **Milestone sections** (`id: "milestone-{milestone_id}"`) — One per milestone, ordered by `sort_order`
3. **Unassigned Features** (`id: "unassigned"`) — Only if features exist without milestone assignment
4. **Active Discussions** (`id: "discussions"`) — Only if discussions exist
5. **Recent Activity** (`id: "recent-activity"`) — Only if events exist

### Feature Sorting

Within each milestone section, features are ordered by:
1. `priority` DESC (highest priority first)
2. `created_at` ASC (oldest first as tiebreaker)

---

## Interactions

### Table of Contents Navigation

| Trigger | Action | Result |
|---------|--------|--------|
| Click TOC link (`.spec-toc-item`) | `e.preventDefault()` + `scrollIntoView({ behavior: 'smooth', block: 'start' })` | Smooth scroll to the target section; no URL change |
| Scroll page | CSS `position: sticky; top: 16px` | TOC sidebar stays fixed in viewport |
| TOC overflows | CSS `max-height: 80vh; overflow-y: auto` | TOC becomes independently scrollable |

### Feature Cards

| Trigger | Action | Result |
|---------|--------|--------|
| Click feature name (`.clickable-feature`) | `App.navigateTo('features', featureId)` | Navigate to Features page with that feature selected |
| Click dependency badge | `App.navigateTo('features', depId)` | Navigate to Features page for the dependency |
| Click "Specification" summary | Native `<details>` toggle | Expand/collapse the rendered spec markdown |

### Print

| Trigger | Action | Result |
|---------|--------|--------|
| Click "🖨️ Print" button | `window.print()` | Opens browser print dialog with print-optimized layout |

### Live Updates

| Trigger | Action | Result |
|---------|--------|--------|
| WebSocket `refresh` message | Page re-renders via `App.renderSpec()` | All sections, stats, and features update to reflect current DB state |

---

## State Handling

### Loading State

The page relies on `App.renderSpec()` being an `async` function that `await`s the API call. The content area is replaced with the rendered HTML once the API responds. There is no explicit loading spinner specific to this page — it uses the app-wide page rendering mechanism.

### Error State

If the `App.api('spec-document')` call fails (network error, server error), the page falls through to the app-wide error handling. The API handler returns errors for:
- Missing project (`db.GetProject()` fails) → 500 error
- Missing milestones or features → 500 error
- Discussions/events query failures are swallowed (default to empty arrays)

### Empty States

| Condition | Behavior |
|-----------|----------|
| No features | Stats show 0; executive summary says "0 features across 0 milestones"; no milestone sections |
| No milestones | No milestone sections rendered; features (if any) appear in "Unassigned Features" |
| No discussions | "Active Discussions" section omitted from TOC and content |
| No events | "Recent Activity" section omitted from TOC and content |
| No spec_md on a feature | The `<details>` expander is not rendered for that feature card |
| No description on a feature | The description div is not rendered |
| No dependencies on a feature | The dependencies div is not rendered |
| Feature has no milestone_id | Feature appears in "Unassigned Features" section |

### Data Freshness

- Document is regenerated on every page load (no caching)
- `generated_at` timestamp reflects the moment the API was called (`timeNowISO()`)
- WebSocket-triggered re-renders fetch fresh data from the API

---

## Accessibility Notes

### Current Implementation

| Aspect | Status | Details |
|--------|--------|---------|
| **Semantic headings** | ✅ Present | Each section uses `<h2>` with bottom border |
| **Link text** | ✅ Descriptive | TOC links use section titles as text |
| **Status communication** | ⚠️ Visual only | Status badges use color alone (background + text color); no ARIA labels |
| **Interactive elements** | ⚠️ Partial | Feature names use `<strong>` with click handlers, not `<a>` or `<button>` |
| **Print button** | ✅ Standard | Uses native `<button>` element |
| **Expandable sections** | ✅ Native | Uses `<details>/<summary>` which has built-in keyboard and screen reader support |
| **Keyboard navigation** | ⚠️ Limited | TOC links are `<a>` tags (focusable); feature names are `<strong>` (not focusable by default) |
| **Color contrast** | ✅ Theme-aware | Uses CSS custom properties that adapt to light/dark theme |
| **Sticky TOC** | ℹ️ Neutral | Sticky positioning does not affect accessibility; content order in DOM is logical |
| **Landmark regions** | ⚠️ Missing | No `<nav>`, `<main>`, or `<article>` landmarks; all content is in generic `<div>` elements |

### Recommendations for Improvement

1. **Add `role="navigation"` and `aria-label="Table of Contents"`** to the TOC sidebar div
2. **Add `aria-label` to status badges** (e.g., `aria-label="Status: done"`) for screen readers
3. **Use `<button>` or `<a role="button">`** for clickable feature names instead of `<strong>` with click handlers
4. **Add `tabindex="0"` and `keydown` handlers** to clickable feature names and dependency badges
5. **Add `role="article"` or use `<article>`** for each feature card
6. **Include a skip link** to jump from TOC to content for keyboard users
