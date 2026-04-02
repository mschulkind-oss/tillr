# Stats — Screen Specification

## Overview

The Stats page is the quantitative analytics hub of the Tillr app. It aggregates project-wide metrics—feature completion rates, cycle performance scores, roadmap distribution, milestone progress, burndown trajectories, and weekly velocity—into a single, live-updating dashboard. The page is designed for at-a-glance health checks by project leads and for trend analysis by agents and developers over time.

**Primary navigation entry:** Sidebar nav item "Stats" (renders via `App.renderStats()`).

**Data sources:** Two API endpoints supply all data:

| Endpoint | Payload | Purpose |
|---|---|---|
| `GET /api/stats` | `ProjectStats` | Feature counts, cycle scores, roadmap breakdown, milestone progress, activity totals |
| `GET /api/stats/burndown` | `BurndownData` | Daily burndown points and weekly velocity series |

---

## User Roles & Personas

| Persona | Description | Primary use of Stats page |
|---|---|---|
| **Project Lead** | Human decision-maker steering the product. | Monitor overall health, assess milestone risk, approve/reject based on score trends. |
| **Agent Operator** | Human or system dispatching AI agents to work items. | Track cycle throughput, spot stalled iterations, gauge velocity. |
| **AI Agent** | Automated worker consuming `tillr next --json`. | Read `/api/stats` (JSON) to self-assess performance and adapt strategy. |
| **Stakeholder** | Non-technical reviewer (PM, exec). | Glance at completion %, milestone bars, and burndown shape during check-ins. |

---

## User Stories

### US-1 — Project Health at a Glance

> **As a** project lead,
> **I want** to see completion rate, average score, total iterations, and recent activity in prominent summary cards,
> **so that** I can assess project health in under five seconds.

**Acceptance criteria (Given / When / Then):**

| # | Given | When | Then |
|---|---|---|---|
| 1.1 | The project has features in various statuses | I navigate to the Stats page | I see four overview cards: Completion, Avg Score, Iterations, Activity |
| 1.2 | 8 of 20 features are done | The page loads | The Completion card shows **40.0%** with subtitle "8 / 20 features" |
| 1.3 | No features exist yet | The page loads | The Completion card shows **0.0%** with subtitle "0 / 0 features" |
| 1.4 | The cycle_stats contain an avg_score of 7.8 from 12 scores | The page loads | The Avg Score card shows **7.8** with subtitle "from 12 scores" |
| 1.5 | No cycle scores exist | The page loads | The Avg Score card shows **0.0** with subtitle "from 0 scores" |
| 1.6 | 3 cycles with a total of 9 iterations exist | The page loads | The Iterations card shows **9** with subtitle "3.0 avg per cycle" |
| 1.7 | Activity stats report 142 total, 23 last-7-day, 64 last-30-day events | The page loads | The Activity card shows **142** with subtitle "23 last 7d · 64 last 30d" |

---

### US-2 — Feature Status Distribution

> **As a** project lead,
> **I want** to see how features are distributed across statuses in a donut chart,
> **so that** I can identify bottlenecks (e.g., too many features stuck in "human-qa").

**Acceptance criteria:**

| # | Given | When | Then |
|---|---|---|---|
| 2.1 | Features exist in statuses: draft (5), implementing (3), done (2) | The page loads | A CSS conic-gradient donut renders with three colored segments proportional to 5:3:2 |
| 2.2 | The donut renders | I look at the center hole | The total feature count is displayed inside the donut hole |
| 2.3 | The donut renders | I look beside the chart | A vertical legend lists each status with a colored dot, label, and count |
| 2.4 | No features exist | The page loads | The donut section shows an empty/neutral state (no gradient segments) |

**Status color mapping:**

| Status | Color |
|---|---|
| draft | `#6b7280` (gray) |
| planning | `#8b5cf6` (purple) |
| implementing | `#3b82f6` (blue) |
| agent-qa | `#f59e0b` (amber) |
| human-qa | `#ec4899` (pink) |
| done | `#10b981` (green) |
| blocked | `#ef4444` (red) |

---

### US-3 — Score Trend Over Time

> **As a** project lead,
> **I want** to see cycle scores plotted over time as a line chart with color-coded segments per cycle type,
> **so that** I can track quality trends and identify regressions.

**Acceptance criteria:**

| # | Given | When | Then |
|---|---|---|---|
| 3.1 | `scores_over_time` contains 15 data points across two cycle types | The page loads | A canvas line chart renders with segments colored by cycle type |
| 3.2 | I hover over a data point on the chart | The tooltip appears | It shows the score (bold, colored), cycle type (kebab-to-space), and date |
| 3.3 | I move the cursor away from all points | — | The tooltip hides |
| 3.4 | The chart renders | I inspect the Y axis | Grid lines appear at score values 2, 4, 6, 8, 10 |
| 3.5 | The chart renders | I inspect the X axis | Date labels are subsampled to avoid crowding (minimum ~6 labels shown) |
| 3.6 | The chart renders | I inspect the area under the line | A blue gradient fill (rgba 59,130,246) fades from 0.15 opacity at top to 0.01 at bottom |
| 3.7 | No scores exist | The page loads | The canvas area is empty (no crash, no misleading axes) |

**Cycle type color palette:**

| Cycle Type | Color |
|---|---|
| feature-implementation | `#3b82f6` (blue) |
| ui-refinement | `#8b5cf6` (purple) |
| bug-triage | `#ef4444` (red) |
| documentation | `#10b981` (green) |
| architecture-review | `#f59e0b` (amber) |
| release | `#ec4899` (pink) |
| roadmap-planning | `#14b8a6` (teal) |
| onboarding-dx | `#6366f1` (indigo) |

---

### US-4 — Roadmap Distribution

> **As a** stakeholder,
> **I want** to see roadmap items broken down by priority and by category in horizontal bar charts,
> **so that** I can understand where effort is allocated.

**Acceptance criteria:**

| # | Given | When | Then |
|---|---|---|---|
| 4.1 | Roadmap items exist with priorities: critical (2), high (5), medium (3), low (1) | The page loads | A "Roadmap by Priority" card shows horizontal bars with labels and counts |
| 4.2 | Roadmap items exist with categories: core (4), ux (2), infra (3) | The page loads | A "Roadmap by Category" card shows horizontal bars with labels and counts |
| 4.3 | Each bar row renders | I inspect it | It has a label (left-aligned, 90px), a filled track (proportional width with 0.6s ease transition), and a numeric value (right) |
| 4.4 | No roadmap items exist | The page loads | Both cards show with empty/zero bars |

**Priority color mapping:**

| Priority | Color |
|---|---|
| critical | `#ef4444` |
| high | `#f59e0b` |
| medium | `#3b82f6` |
| low | `#10b981` |
| nice-to-have | `#6b7280` |

**Category color mapping:**

| Category | Color |
|---|---|
| core | `#3b82f6` |
| ux | `#8b5cf6` |
| infrastructure | `#f59e0b` |
| dx | `#10b981` |
| documentation | `#6b7280` |

---

### US-5 — Cycle Type Distribution

> **As an** agent operator,
> **I want** to see the distribution of cycle types in a donut chart,
> **so that** I can understand what kinds of work the project is performing.

**Acceptance criteria:**

| # | Given | When | Then |
|---|---|---|---|
| 5.1 | Scores exist across 3 cycle types: feature-implementation (10), bug-triage (4), documentation (2) | The page loads | A canvas donut chart renders with three colored arcs proportional to 10:4:2 |
| 5.2 | The donut renders | I look at the center | The total score count is displayed as a large number with "SCORES" label beneath |
| 5.3 | The donut renders | I inspect the arcs | Inner radius is 55% of outer radius, creating a visible ring |
| 5.4 | No scores exist | The page loads | The chart area is empty or shows 0 in the center |

---

### US-6 — Feature Velocity Metrics

> **As an** agent operator,
> **I want** to see key velocity metrics (features completed, 7-day/30-day event rates, avg iterations, total cycles) as a text-based summary,
> **so that** I can gauge throughput without needing to interpret charts.

**Acceptance criteria:**

| # | Given | When | Then |
|---|---|---|---|
| 6.1 | Stats are loaded | The page renders | A "Feature Velocity" card shows rows for: Features Completed, Events (7 days), Events (30 days), Avg Iterations/Cycle, Total Cycles |
| 6.2 | Each velocity row | I inspect it | It has a gray label on the left and a bold value on the right, on a `--bg-tertiary` background with 6px border radius |
| 6.3 | No data exists | The page renders | All velocity values show 0 |

---

### US-7 — Milestone Progress

> **As a** project lead,
> **I want** to see every milestone with its name, done/total count, and a progress bar,
> **so that** I can track release readiness.

**Acceptance criteria:**

| # | Given | When | Then |
|---|---|---|---|
| 7.1 | Two milestones exist: "v1.0" (8/10, 80%) and "v2.0" (2/15, 13%) | The page loads | A full-width card shows both milestones with progress bars |
| 7.2 | A milestone renders | I inspect it | The header row shows name on the left and "8 / 10 (80%)" on the right |
| 7.3 | A milestone renders | I inspect the bar | An 8px-tall progress bar fills to the correct percentage with `--accent` color and a 0.8s cubic-bezier animation |
| 7.4 | A milestone is 100% complete | The bar renders | The fill uses the `.success` variant (green) |
| 7.5 | No milestones exist | The page loads | The milestones section is empty or shows an informational message |

---

### US-8 — Feature Burndown Chart

> **As a** project lead,
> **I want** to see a burndown chart showing remaining features over time vs. an ideal line,
> **so that** I can predict whether we'll finish on schedule.

**Acceptance criteria:**

| # | Given | When | Then |
|---|---|---|---|
| 8.1 | Burndown data contains 30 daily points | The page loads and `/api/stats/burndown` returns | A canvas line chart renders with a red "Remaining" line and a dashed gray "Ideal" line |
| 8.2 | I hover over a data point | The tooltip appears | It shows the date (bold), remaining count (red), done count (green), and total (gray) |
| 8.3 | The chart renders | I inspect the legend | A legend shows: solid red line = "Remaining", dashed gray line = "Ideal" |
| 8.4 | The chart renders | I inspect the fill | A red gradient fill (rgba 239,68,68) fades under the remaining line from 0.12 to 0.01 opacity |
| 8.5 | No burndown data exists | The API returns empty points | The chart area is empty (no crash) |

---

### US-9 — Weekly Velocity Chart

> **As an** agent operator,
> **I want** to see a bar chart of features completed per week with an average velocity line,
> **so that** I can detect acceleration or deceleration in delivery pace.

**Acceptance criteria:**

| # | Given | When | Then |
|---|---|---|---|
| 9.1 | Velocity data contains 8 weeks of data | The page loads | A canvas bar chart renders with blue gradient bars for each week |
| 9.2 | I hover over a bar | The tooltip appears | It shows the week identifier (bold) and completed count (blue) |
| 9.3 | The average velocity is 4.5 across all weeks | The chart renders | A dashed orange line (`#f59e0b`) is drawn horizontally at the 4.5 mark with a label |
| 9.4 | The chart renders | I inspect the legend | A legend shows: blue rectangle = "Completed", dashed orange line = "Avg" |
| 9.5 | Bars render | I inspect their shape | Bars have rounded top corners (quadratic curve), max width 60px, minimum width 4px |
| 9.6 | No velocity data exists | The API returns empty array | The chart area is empty (no crash) |

---

### US-10 — Live Updates

> **As a** project lead,
> **I want** the Stats page to update automatically when project data changes,
> **so that** I always see current metrics without manual refresh.

**Acceptance criteria:**

| # | Given | When | Then |
|---|---|---|---|
| 10.1 | I am viewing the Stats page | A feature status changes (via CLI or another browser tab) | The WebSocket connection (`/ws`) receives an update event |
| 10.2 | An update event arrives while on the Stats page | The event is processed | `renderStats()` is re-invoked, re-fetching `/api/stats` and `/api/stats/burndown` |
| 10.3 | The page re-renders | Charts redraw | Canvas charts are redrawn via `App.drawStatsCharts()` after the HTML is injected |

---

## Screen Layout

### Grid Structure

The page uses a 4-column CSS grid (`.stats-grid`) with 16px gaps. Cards span 1, 2, or 4 columns depending on their size class.

```
┌──────────────────────────────────────────────────────────────────┐
│  STATS PAGE                                                      │
├───────────────┬───────────────┬───────────────┬──────────────────┤
│  Completion   │  Avg Score    │  Iterations   │  Activity        │
│  (sm, span 1) │  (sm, span 1) │  (sm, span 1) │  (sm, span 1)   │
├───────────────┴───────────────┼───────────────┴──────────────────┤
│  Feature Status Donut         │  Score Trend Line Chart          │
│  (md, span 2)                 │  (md, span 2)                    │
├───────────────────────────────┼──────────────────────────────────┤
│  Roadmap by Priority          │  Roadmap by Category             │
│  (md, span 2)                 │  (md, span 2)                    │
├───────────────────────────────┼──────────────────────────────────┤
│  Cycle Type Distribution      │  Feature Velocity                │
│  (md, span 2)                 │  (md, span 2)                    │
├───────────────────────────────┴──────────────────────────────────┤
│  Milestone Progress (full, span 4)                               │
├──────────────────────────────────────────────────────────────────┤
│  § Progress Over Time                                            │
├───────────────────────────────┬──────────────────────────────────┤
│  Feature Burndown Chart       │  Weekly Velocity Chart           │
│  (md, span 2)                 │  (md, span 2)                    │
└───────────────────────────────┴──────────────────────────────────┘
```

### Card Anatomy

Each card (`.stats-card`) follows a consistent structure:

```
┌─────────────────────────────────┐
│  CARD TITLE            (.stats-card-title)
│                                 │
│  Main Content Area              │
│  (big number, chart, or bars)   │
│                                 │
│  Subtitle / Legend     (.stats-card-sub)
└─────────────────────────────────┘
```

- **Background:** `var(--bg-secondary)`
- **Border:** 1px solid `var(--border)`, 10px radius
- **Padding:** 20px

### Responsive Behavior

| Breakpoint | Behavior |
|---|---|
| ≥ 1600px | Grid auto-fits columns, `minmax(200px, 1fr)` |
| 769px – 1599px | Fixed 4-column grid |
| ≤ 768px | All cards collapse to single-column (`1fr`) |

---

## Data Requirements

### Primary Fetch: `GET /api/stats`

Returns `ProjectStats`:

```json
{
  "feature_stats": {
    "total": 20,
    "by_status": { "draft": 5, "planning": 3, "implementing": 4, "done": 8 },
    "completion_rate": 40.0
  },
  "cycle_stats": {
    "total_cycles": 6,
    "total_iterations": 18,
    "avg_score": 7.8,
    "scores_over_time": [
      { "date": "2025-01-15", "score": 7.5, "cycle": "feature-implementation" },
      { "date": "2025-01-22", "score": 8.2, "cycle": "ui-refinement" }
    ]
  },
  "roadmap_stats": {
    "total": 12,
    "by_priority": { "critical": 2, "high": 5, "medium": 3, "low": 2 },
    "by_category": { "core": 4, "ux": 3, "infrastructure": 3, "dx": 2 },
    "by_status": { "proposed": 4, "accepted": 6, "completed": 2 }
  },
  "milestone_stats": [
    { "name": "v1.0 MVP", "total": 10, "done": 8, "progress": 80.0 },
    { "name": "v2.0 Polish", "total": 15, "done": 2, "progress": 13.3 }
  ],
  "activity": {
    "total_events": 142,
    "events_last_7_days": 23,
    "events_last_30_days": 64
  }
}
```

### Secondary Fetch: `GET /api/stats/burndown`

Returns `BurndownData`:

```json
{
  "points": [
    { "date": "2025-01-01", "remaining": 15, "done": 5, "total": 20 },
    { "date": "2025-01-02", "remaining": 14, "done": 6, "total": 20 }
  ],
  "velocity": [
    { "week": "2025-W01", "completed": 5 },
    { "week": "2025-W02", "completed": 7 }
  ]
}
```

### Data Flow

```
Navigation click ("Stats")
  │
  ├──▶ App.renderStats()
  │      ├── await App.api('stats')  ─────▶ GET /api/stats ──▶ ProjectStats
  │      ├── Build HTML string from ProjectStats
  │      ├── Inject HTML into #content
  │      └── Store data in App._statsData
  │
  └──▶ App.drawStatsCharts()  (called after HTML injection)
         ├── Read App._statsData.cycle_stats.scores_over_time
         ├── App.drawScoreTrendChart('scoreTrendCanvas', scores)
         ├── App.drawCycleTypeChart('cycleTypeCanvas', scores)
         │
         └── await App.api('stats/burndown') ──▶ GET /api/stats/burndown ──▶ BurndownData
               ├── App.drawBurndownChart('burndownCanvas', ..., burndown.points)
               └── App.drawVelocityChart('velocityCanvas', ..., burndown.velocity)
```

---

## Interactions

### Hover Tooltips (Canvas Charts)

Three of the four canvas charts support mousemove tooltips:

| Chart | Trigger | Proximity | Tooltip Content |
|---|---|---|---|
| Score Trend | Hover within 30px of a data point | Closest point by Euclidean distance | Score (bold, cycle-colored), cycle type (space-separated), date (gray) |
| Burndown | Hover within 20px of a data point (X axis) | Closest point by X proximity | Date (bold), remaining (red), done (green), total (gray) |
| Weekly Velocity | Hover within bar bounds (X and Y) | Must be inside bar rectangle | Week (bold), completed count (blue) |

**Tooltip behavior:**
- Tooltip element: `.score-chart-tooltip` (absolutely positioned within `.score-chart-container`)
- Shown by setting `display: block` and positioning via `left`/`top` styles
- Hidden by setting `display: none` when cursor leaves proximity
- Non-interactive: `pointer-events: none`
- Shadow: `0 4px 12px rgba(0,0,0,0.3)`

### Navigation

- **Entry:** Click "Stats" in sidebar navigation
- **No outbound links:** The Stats page is read-only; it does not link to individual features, milestones, or cycles (users navigate to those via other sidebar items)

### High-DPI Rendering

All canvas charts use device pixel ratio scaling for crisp rendering on Retina/HiDPI displays:

```javascript
var dpr = window.devicePixelRatio || 1;
canvas.width = containerWidth * dpr;
canvas.height = containerHeight * dpr;
ctx.scale(dpr, dpr);
```

---

## State Handling

### Loading State

The Stats page fetches data asynchronously. During the fetch:

1. `App.renderStats()` is an `async` function — the page content area shows the previous content until the fetch completes and HTML is injected.
2. `App.drawStatsCharts()` fires after HTML injection; the burndown fetch is a secondary async call, so burndown/velocity charts may appear slightly after the main stats cards.

### Empty / Zero-Data States

| Component | Empty behavior |
|---|---|
| Overview cards | Show `0.0%`, `0.0`, `0`, `0` respectively with descriptive subtitles |
| Feature donut | Renders with no gradient segments (solid background) |
| Score trend chart | Empty canvas, no axes drawn without data |
| Roadmap bars | Bars render at zero width |
| Cycle type donut | Empty canvas, center shows `0` / `SCORES` |
| Velocity metrics | All values show `0` |
| Milestone progress | No milestone rows rendered |
| Burndown chart | Empty canvas (no crash on empty array) |
| Weekly velocity chart | Empty canvas (no crash on empty array) |

### Error State

If either API call fails:
- The `App.api()` wrapper handles errors via the standard error path
- Canvas charts that depend on the failed burndown endpoint show empty (the `.catch()` in `drawStatsCharts` silently absorbs the error)

### Data Caching

- `App._statsData` caches the last-fetched `ProjectStats` object
- This cache is used by `drawStatsCharts()` to avoid re-fetching when only canvas drawing is needed
- The cache is overwritten on every `renderStats()` call (no stale data risk on re-navigation)

### WebSocket Re-renders

When a WebSocket message arrives on `/ws`:
- If the current page is "stats", `renderStats()` is re-invoked
- The entire HTML is rebuilt and reinjected; all canvas charts are redrawn
- This is a full re-render, not a differential update

---

## Accessibility Notes

### Current State

| Aspect | Status | Notes |
|---|---|---|
| **Semantic HTML** | ⚠️ Partial | Cards use `<div>` elements; no `<section>`, `<article>`, or ARIA landmark roles |
| **Color contrast** | ✅ Good | Text uses `var(--text-primary)` / `var(--text-secondary)` on dark backgrounds; chart colors are distinct |
| **Keyboard navigation** | ❌ Missing | No focusable elements on the Stats page; canvas charts have no keyboard interaction |
| **Screen reader support** | ❌ Missing | Canvas charts are opaque to screen readers; no `aria-label` on canvases, no text alternatives for visual data |
| **Reduced motion** | ⚠️ Partial | Progress bar fills animate with CSS transitions (0.6s–0.8s); no `prefers-reduced-motion` media query to disable |
| **Dark mode** | ✅ Full | All colors use CSS custom properties that switch with the theme; chart colors are hardcoded but chosen for dark-background visibility |
| **Responsive** | ✅ Good | Grid collapses to single column at ≤768px; canvas charts resize to container width |
| **Touch** | ⚠️ Limited | Canvas tooltips rely on `mousemove`; no `touchmove` handler for mobile tooltip interaction |

### Recommendations for Future Improvement

1. **Add `role="img"` and `aria-label` to each `<canvas>`** — e.g., `aria-label="Score trend chart showing 15 scores ranging from 6.2 to 9.1"`.
2. **Provide a visually-hidden data table** as an alternative to each chart for screen reader users.
3. **Add `prefers-reduced-motion` handling** to disable CSS transitions on progress bars and bar chart fills.
4. **Use `<section>` elements** with heading hierarchy (`<h2>`, `<h3>`) for each card group (overview, charts, milestones, progress over time).
5. **Add `touchstart`/`touchmove` handlers** to canvas charts so mobile users can access tooltip data.
