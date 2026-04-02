# Dashboard — Screen Specification

## Overview

The Dashboard is the default landing page of the Tillr web viewer. It provides a real-time, at-a-glance view of project health — summarizing feature progress, milestone completion, active iteration cycles, recent activity, roadmap highlights, and quality metrics. It is the first screen every user sees and serves as the primary navigation hub for drilling into specific areas of the project.

The page fetches six API endpoints in parallel on load (`/api/status`, `/api/features`, `/api/milestones`, `/api/roadmap`, `/api/cycles`, `/api/discussions`) and renders seven distinct sections. All data updates in real-time via WebSocket push.

## User Roles & Personas

- **Product Manager**: Uses the Dashboard to assess overall project health, check milestone progress, review QA queue depth, and decide what to prioritize next. Needs a quick answer to "are we on track?"
- **AI Agent Operator**: Monitors active cycles, checks whether agents are producing quality work (cycle scores), and identifies stalled or blocked features. Uses the Dashboard to decide whether to intervene.
- **Developer**: Glances at the feature board to see what's in progress and what's awaiting QA. Clicks through to feature details or the QA page for items needing attention.
- **Tech Lead**: Reviews priority distribution, spec coverage, and roadmap highlights to ensure the team is working on the right things at the right level of rigor.
- **Stakeholder**: Views the Dashboard in read-only mode during demos or check-ins. Needs the kanban board, milestone bars, and roadmap highlights to be self-explanatory without training.

## User Stories

### US-DASH-001: View project health summary

**As a** Product Manager, **I want** to see key project metrics at a glance, **so that** I can quickly assess whether the project is on track without drilling into individual features.

**Acceptance Criteria:**
- **Given** the Dashboard is loaded, **When** features exist in the project, **Then** I see a stats grid with five cards: Total Features, Completed, In Progress, Awaiting QA, and Active Cycles.
- **Given** the project has 12 features with 4 done, 3 implementing, 2 in agent-qa, 1 in human-qa, **When** I view the stats grid, **Then** Total Features shows "12", Completed shows "4", In Progress shows "3", Awaiting QA shows "3" (agent-qa + human-qa combined), and Active Cycles shows the count from `/api/status`.
- **Given** any stat card, **When** I look at it, **Then** it displays a large numeric value, a descriptive label, and a thematic emoji icon (📦, ✅, 🔨, 🔍, 🔄).

### US-DASH-002: Understand feature status distribution

**As a** Tech Lead, **I want** to see a visual breakdown of features by status, **so that** I can identify bottlenecks in the development pipeline.

**Acceptance Criteria:**
- **Given** the Dashboard is loaded and features exist, **When** I look below the stats grid, **Then** I see a horizontal stacked status bar showing the proportion of features in each status.
- **Given** the status bar is rendered, **When** I examine it, **Then** each segment is color-coded (Done=green, Human QA=orange, Agent QA=teal, Implementing=blue, Planning=purple, Draft=gray, Blocked=red) and only non-zero statuses appear.
- **Given** a status bar segment, **When** I hover over it, **Then** a tooltip shows the status label and exact count.
- **Given** the status bar, **When** I look below it, **Then** a legend with colored dots and labels identifies each segment.
- **Given** no features exist, **When** the Dashboard loads, **Then** the status bar is not rendered.

### US-DASH-003: Browse features on the kanban board

**As a** Developer, **I want** to see all features organized by status in a kanban-style board, **so that** I can quickly find features in a specific workflow stage.

**Acceptance Criteria:**
- **Given** the Dashboard is loaded, **When** features exist, **Then** a "Feature Board" card displays seven columns: Draft, Planning, Implementing, Agent QA, Human QA, Done, and Blocked.
- **Given** a kanban column, **When** it contains features, **Then** each feature appears as a card showing its name, a priority dot (color-coded: P1=red, P2=orange, P3=blue, P4=green, P5=gray), the priority number, and optionally its milestone name.
- **Given** a kanban column, **When** it contains no features, **Then** a dashed placeholder with "—" is shown.
- **Given** a kanban column header, **When** I look at it, **Then** it shows the status name (uppercase) and a count badge with the number of features.
- **Given** each column, **When** rendered, **Then** it has a 3px color-coded top border matching its status color.
- **Given** a column has many features, **When** the list exceeds 480px in height, **Then** the column becomes scrollable.

### US-DASH-004: Navigate to feature details from the kanban board

**As a** Developer, **I want** to click a kanban card to see full feature details, **so that** I can quickly drill down from the overview.

**Acceptance Criteria:**
- **Given** I see a kanban card for feature "Search API", **When** I click on it, **Then** the app navigates to the feature detail page for that feature.
- **Given** a kanban card, **When** I hover over it, **Then** the cursor changes to a pointer and the card title is available as a tooltip.

### US-DASH-005: Track milestone progress

**As a** Product Manager, **I want** to see progress bars for each milestone, **so that** I can understand how close each milestone is to completion.

**Acceptance Criteria:**
- **Given** milestones exist, **When** the Dashboard loads, **Then** a "Milestones" card shows each milestone with its name, a status badge, a progress bar, a fraction (e.g., "3/5 features"), and a percentage.
- **Given** a milestone has 5 features and 3 are done, **When** I view its card, **Then** the progress bar is filled to 60% and the percentage reads "60%".
- **Given** a milestone is 100% complete, **When** I view its progress bar, **Then** the fill uses a success/green color and the card is styled as `milestone-complete`.
- **Given** a milestone is partially complete (0% < progress < 100%), **When** I view its card, **Then** the card is styled as `milestone-active`.
- **Given** no milestones exist, **When** the Dashboard loads, **Then** the card shows an empty state with a 🏔️ icon, "No milestones yet", and the hint `$ tillr milestone add <name>`.

### US-DASH-006: Navigate to features from a milestone card

**As a** Product Manager, **I want** to click a milestone card to see the features it tracks, **so that** I can investigate which features are holding up a milestone.

**Acceptance Criteria:**
- **Given** I see a milestone card, **When** I click on it, **Then** the app navigates to the Features page.
- **Given** a milestone card, **When** I hover over it, **Then** the cursor changes to a pointer indicating it is clickable.

### US-DASH-007: Review recent activity

**As a** Product Manager, **I want** to see a feed of recent project events, **so that** I can understand what happened since I last checked.

**Acceptance Criteria:**
- **Given** the project has recorded events, **When** the Dashboard loads, **Then** a "Recent Activity" card shows up to 8 of the most recent events.
- **Given** an activity item, **When** I view it, **Then** it shows a type-specific icon, a formatted event description, and a relative timestamp (e.g., "2 hours ago").
- **Given** an event related to a feature, **When** I view it, **Then** a feature ID badge appears next to the description.
- **Given** no events exist, **When** the Dashboard loads, **Then** the card shows an empty state with ⏳ icon, "No activity yet", and the hint "Events will appear here as you work on features."

**Event Icon Mapping:**
| Event Pattern | Icon |
|---|---|
| approved / completed | ✔ |
| rejected / failed | ✘ |
| created | ⊕ |
| started | ▸ |
| scored | ★ |
| updated / edit | ✎ |
| removed / deleted | ⊖ |
| cycle | ⟳ |
| milestone | ⚑ |
| heartbeat | ♥ |
| qa / review | ⊘ |
| moved / transition | → |
| assigned | ⊙ |
| comment / note | ✦ |
| (default) | ● |

### US-DASH-008: Navigate to a feature from the activity feed

**As a** Developer, **I want** to click an activity item tied to a feature to jump to that feature's detail page, **so that** I can quickly investigate what happened.

**Acceptance Criteria:**
- **Given** an activity item has a `feature_id`, **When** I click on it, **Then** the app navigates to that feature's detail page.
- **Given** an activity item has a `feature_id`, **When** I view it, **Then** the cursor is a pointer indicating it is clickable.
- **Given** an activity item has no `feature_id` (e.g., a milestone event), **When** I view it, **Then** it is not clickable and the cursor remains default.

### US-DASH-009: View roadmap highlights

**As a** Tech Lead, **I want** to see the top roadmap items on the Dashboard, **so that** I can keep strategic priorities visible without navigating to the full roadmap page.

**Acceptance Criteria:**
- **Given** roadmap items exist, **When** the Dashboard loads, **Then** a "📋 Roadmap Highlights" card shows up to 6 roadmap items.
- **Given** a roadmap item, **When** I view it, **Then** it shows a numbered index (colored by priority: critical=red, high=orange, medium=blue, low=green, nice-to-have=purple), the title, a status with icon (○ proposed, ◐ accepted, ◑ in-progress, ● completed, ◌ deferred), and optionally an effort badge (XS/S/M/L/XL).
- **Given** no roadmap items exist, **When** the Dashboard loads, **Then** the card shows "No roadmap items yet" in muted text.

### US-DASH-010: Navigate to roadmap details

**As a** Tech Lead, **I want** to click a roadmap item to see its full details, **so that** I can review the plan and its linked features.

**Acceptance Criteria:**
- **Given** I see a roadmap item on the Dashboard, **When** I click on it, **Then** the app navigates to the roadmap detail page for that item.
- **Given** I see the "📋 Roadmap Highlights" card header, **When** I click on it, **Then** the app navigates to the full Roadmap page.

### US-DASH-011: Assess priority distribution

**As a** Tech Lead, **I want** to see a chart of features by priority level, **so that** I can ensure the team is focused on the right mix of critical vs. low-priority work.

**Acceptance Criteria:**
- **Given** features exist with varying priorities, **When** the Dashboard loads, **Then** a "Priority Distribution" card shows a horizontal bar chart with one row per priority level (P1 Critical through P5 Nice-to-have).
- **Given** a priority level, **When** I view its bar, **Then** the bar width is proportional to the percentage of features at that priority, and the exact count is shown to the right.
- **Given** a priority level has zero features, **When** I view its row, **Then** the bar is empty (0% width) and the count shows "0".

### US-DASH-012: Monitor active iteration cycles

**As an** Agent Operator, **I want** to see active iteration cycles with their progress, **so that** I can monitor whether agents are making progress or are stalled.

**Acceptance Criteria:**
- **Given** active cycles exist, **When** the Dashboard loads, **Then** an "Active Cycles" section appears within the Priority Distribution card.
- **Given** an active cycle, **When** I view it, **Then** it shows the feature ID, the cycle type name, a progress bar, and a label with the current step name and step position (e.g., "develop (2/5)").
- **Given** a cycle is on step 3 of 5, **When** I view its progress bar, **Then** it is filled to 60% (calculated as `(3/5) × 100`).
- **Given** no active cycles exist, **When** the Dashboard loads, **Then** the "Active Cycles" sub-section shows "No active cycles" in muted text.

### US-DASH-013: Review cycle quality scores

**As an** Agent Operator, **I want** to see recent cycle scores at a glance, **so that** I can detect quality trends and intervene when scores drop.

**Acceptance Criteria:**
- **Given** cycles have recorded scores, **When** the Dashboard loads, **Then** a "🎯 Cycle Scores" card shows up to 24 score dots, ordered newest-first.
- **Given** a score dot, **When** I view it, **Then** it displays the numeric score to one decimal place, is color-coded (green ≥ 8, yellow ≥ 6, red < 6), and has a tooltip showing the score, feature ID, and any notes.
- **Given** no cycles have scores, **When** the Dashboard loads, **Then** the Cycle Scores card is not rendered at all.

### US-DASH-014: View aggregate project statistics

**As a** Tech Lead, **I want** to see summary statistics about the project, **so that** I can understand overall project maturity and coverage.

**Acceptance Criteria:**
- **Given** the Dashboard is loaded, **When** I view the "📊 Project Stats" card, **Then** I see four metrics in a 2×2 grid: Total Events, Discussions, Avg Cycle Score, and Spec Coverage (With Specs).
- **Given** features have recorded scores, **When** I view "Avg Cycle Score", **Then** it shows the mean of all scores rounded to one decimal place.
- **Given** no scores exist, **When** I view "Avg Cycle Score", **Then** it displays "—".
- **Given** 8 of 10 features have specs, **When** I view "With Specs", **Then** it shows "8/10 features" and a progress bar filled to 80%.
- **Given** all features have specs, **When** I view the spec coverage bar, **Then** the progress fill uses the success/green color.
- **Given** no features exist, **When** I view "With Specs", **Then** the progress bar is not rendered.

### US-DASH-015: Navigate to QA page from stat card

**As a** Developer, **I want** to click the "Awaiting QA" stat card to jump directly to the QA page, **so that** I can quickly address items needing review.

**Acceptance Criteria:**
- **Given** I see the "Awaiting QA" stat card, **When** I click on it, **Then** the app navigates to the QA page.
- **Given** the "Awaiting QA" stat card, **When** I hover over it, **Then** the cursor changes to a pointer indicating it is clickable.
- **Given** the other stat cards (Total Features, Completed, In Progress, Active Cycles), **When** I click on them, **Then** nothing happens — they are not interactive.

### US-DASH-016: First-time empty project welcome

**As a** new user, **I want** to see a helpful welcome message when my project has no data, **so that** I know how to get started.

**Acceptance Criteria:**
- **Given** the project has zero features and zero milestones, **When** the Dashboard loads, **Then** I see a welcome screen with a 🚀 emoji, the heading "Welcome to your project!", a hint "Start building by adding your first feature and milestone.", and a CLI example `$ tillr feature add <name>`.
- **Given** the project has at least one feature or one milestone, **When** the Dashboard loads, **Then** the welcome screen is not shown — the full Dashboard renders instead.

### US-DASH-017: Real-time updates via WebSocket

**As a** Product Manager, **I want** the Dashboard to update automatically when changes happen, **so that** I always see current data without manually refreshing.

**Acceptance Criteria:**
- **Given** I have the Dashboard open, **When** another user or agent creates a feature via the CLI, **Then** the Dashboard re-fetches data and re-renders within seconds, reflecting the new feature on the kanban board and updated stat counts.
- **Given** the WebSocket connection is active, **When** a database change is detected by the server's file watcher, **Then** the server pushes an update message and the Dashboard refreshes.

### US-DASH-018: Responsive layout

**As a** Stakeholder viewing on a tablet, **I want** the Dashboard to adapt to smaller screens, **so that** I can check project status on any device.

**Acceptance Criteria:**
- **Given** a viewport wider than 1400px, **When** I view the Dashboard grid, **Then** it displays in a multi-column layout (up to 4 columns).
- **Given** a viewport between 768px and 1400px, **When** I view the Dashboard grid, **Then** it collapses to 2 columns.
- **Given** a viewport narrower than 768px, **When** I view the Dashboard grid, **Then** it collapses to a single column.
- **Given** the stats grid, **When** the viewport shrinks, **Then** the auto-fit grid wraps stat cards naturally (minimum card width 180px).
- **Given** the kanban board, **When** the viewport is narrow, **Then** the board scrolls horizontally.

### US-DASH-019: Keyboard navigation

**As a** power user, **I want** to navigate to the Dashboard using a keyboard shortcut, **so that** I can switch views quickly without using the mouse.

**Acceptance Criteria:**
- **Given** I am on any page, **When** I press `g` then `d`, **Then** the app navigates to the Dashboard.
- **Given** I am on the Dashboard, **When** I look at the sidebar, **Then** the Dashboard link is highlighted as active with `aria-current="page"`.

### US-DASH-020: Dark mode support

**As a** Developer working at night, **I want** the Dashboard to support dark mode, **so that** the interface is comfortable in low-light conditions.

**Acceptance Criteria:**
- **Given** the user's system or browser is set to dark mode, **When** the Dashboard loads, **Then** all cards, backgrounds, text, borders, and chart elements use dark-mode color variables (e.g., `var(--bg)`, `var(--text)`, `var(--border)`).
- **Given** dark mode is active, **When** I view kanban cards, stat cards, and progress bars, **Then** they remain visually distinct and readable with appropriate contrast.

## Screen Layout

The Dashboard is composed of the following visual sections, rendered top to bottom:

```
┌─────────────────────────────────────────────────────────────┐
│  Page Header                                                │
│  "{ProjectName} Dashboard"                                  │
│  "Project overview and health at a glance"                  │
├─────────────────────────────────────────────────────────────┤
│  Stats Grid (5 cards, auto-fit row)                         │
│  ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐              │
│  │ 📦   │ │ ✅   │ │ 🔨   │ │ 🔍   │ │ 🔄   │              │
│  │Total │ │Done  │ │In    │ │QA    │ │Active│              │
│  │Feat. │ │      │ │Prog. │ │Wait  │ │Cycle │              │
│  └──────┘ └──────┘ └──────┘ └──────┘ └──────┘              │
├─────────────────────────────────────────────────────────────┤
│  Status Bar (horizontal stacked bar + legend)               │
│  ████████████░░░░░░░░░░████░░░                              │
│  ● Done  ● Human QA  ● Implementing  ● Draft  ...          │
├─────────────────────────────────────────────────────────────┤
│  Feature Board (kanban, horizontal scroll)                  │
│  ┌──────┬──────┬──────┬──────┬──────┬──────┬──────┐         │
│  │Draft │Plan  │Build │AgQA  │HuQA  │Done  │Block │         │
│  │  2   │  1   │  3   │  1   │  1   │  4   │  0   │         │
│  │┌────┐│┌────┐│┌────┐│┌────┐│┌────┐│┌────┐│  —   │         │
│  ││card│││card│││card│││card│││card│││card││      │         │
│  │└────┘│└────┘│└────┘│└────┘│└────┘│└────┘│      │         │
│  │┌────┐│      │┌────┐│      │      │┌────┐│      │         │
│  ││card││      ││card││      │      ││card││      │         │
│  │└────┘│      │└────┘│      │      │└────┘│      │         │
│  └──────┴──────┴──────┴──────┴──────┴──────┴──────┘         │
├─────────────────────────────────────────────────────────────┤
│  Dashboard Grid (responsive: 4 → 2 → 1 columns)            │
│  ┌──────────────┬──────────────┬──────────────┬────────────┐│
│  │ Milestones   │ Recent       │ 📋 Roadmap   │ Priority   ││
│  │              │ Activity     │ Highlights   │ Distrib.   ││
│  │ ■■■■░░ 60%  │ ✔ feat done  │ 1. Search    │ ██░░ P1: 3 ││
│  │ ■■■■■■ 100% │ ⊕ feat added │ 2. Cache     │ ████ P2: 5 ││
│  │              │ ▸ cycle go   │ 3. Auth      │ ██░░ P3: 2 ││
│  │              │ ...          │ ...          │            ││
│  │              │              │              │ Active     ││
│  │              │              │              │ Cycles     ││
│  │              │              │              │ feat1 ██░  ││
│  ├──────────────┴──────────────┼──────────────┼────────────┤│
│  │                             │ 🎯 Cycle     │ 📊 Project ││
│  │                             │ Scores       │ Stats      ││
│  │                             │ 8.5 7.2 9.0  │ Events: 42 ││
│  │                             │ 6.1 8.8 ...  │ Discs: 3   ││
│  │                             │              │ Avg: 7.9   ││
│  │                             │              │ Specs: 8/10││
│  └─────────────────────────────┴──────────────┴────────────┘│
└─────────────────────────────────────────────────────────────┘
```

## Data Requirements

### API Endpoints

| Endpoint | Method | Purpose | Key Fields |
|---|---|---|---|
| `/api/status` | GET | Project overview metrics | `project`, `feature_counts` (map), `milestone_count`, `active_cycles`, `recent_events[]`, `active_work[]` |
| `/api/features` | GET | All features for kanban + stats | `id`, `name`, `status`, `priority`, `milestone_name`, `spec`, `description` |
| `/api/milestones` | GET | Milestone progress | `name`, `status`, `done_features`, `total_features` |
| `/api/roadmap` | GET | Strategic roadmap items | `id`, `title`, `priority`, `status`, `effort`, `category` |
| `/api/cycles` | GET | Cycle progress and scores | `id`, `feature_id`, `cycle_type`, `status`, `current_step`, `config.steps[]`, `scores[]` |
| `/api/discussions` | GET | Discussion count (error-safe) | `id`, `title`, `feature_id` |

All six endpoints are fetched in parallel via `Promise.all()`. The `/api/discussions` call uses `.catch(() => [])` for graceful degradation if the endpoint is unavailable.

### Derived Calculations

| Metric | Formula |
|---|---|
| Total Features | Sum of all values in `feature_counts` |
| Awaiting QA | `feature_counts['agent-qa'] + feature_counts['human-qa']` |
| Milestone % | `Math.round((done_features / total_features) * 100)` or 0 if no features |
| Cycle Progress % | `Math.round(((currentStepIndex + 1) / totalSteps) * 100)` |
| Avg Cycle Score | Mean of all `scores[].score` values across all cycles |
| Spec Coverage | `features.filter(f => f.spec && f.spec.trim()).length` / total |

## Interactions

| Element | Trigger | Action |
|---|---|---|
| Kanban card (with feature ID) | Click | Navigate to feature detail page |
| Kanban card (status-only fallback) | Click | Navigate to Features page filtered by that status |
| Milestone card | Click | Navigate to Features page |
| Activity item (with `feature_id`) | Click | Navigate to feature detail page |
| "Awaiting QA" stat card | Click | Navigate to QA page |
| Roadmap item row | Click | Navigate to roadmap item detail |
| "📋 Roadmap Highlights" card header | Click | Navigate to Roadmap page |
| Status bar segment | Hover | Tooltip with status label and count |
| Kanban card | Hover | Title tooltip with full feature name |
| Score dot | Hover | Tooltip with score, feature ID, and notes |
| Sidebar "📊 Dashboard" link | Click | Navigate to Dashboard (reload) |
| Keyboard shortcut `g` then `d` | Keypress | Navigate to Dashboard |

## State Handling

### Loading State

The Dashboard uses `async/await` with `Promise.all()` for data fetching. There is no explicit loading spinner — the page transitions handled by the app framework manage the visual transition. Data appears once all six parallel requests resolve.

### Empty States

| Condition | Presentation |
|---|---|
| **No features AND no milestones** | Full-page welcome: 🚀 "Welcome to your project!" with hint and CLI example `$ tillr feature add <name>`. No other sections render. |
| **No milestones (features exist)** | Milestones card shows: 🏔️ "No milestones yet" with hint `$ tillr milestone add <name>`. |
| **No recent events** | Activity card shows: ⏳ "No activity yet" with hint "Events will appear here as you work on features." |
| **No roadmap items** | Roadmap Highlights card shows: "No roadmap items yet" in muted text. |
| **No active cycles** | Active Cycles sub-section shows: "No active cycles" in muted text. |
| **No cycle scores** | Cycle Scores card is not rendered at all — the entire card is omitted from the grid. |
| **No features (status bar)** | Status bar section is not rendered. |
| **No features (spec coverage)** | Spec coverage progress bar within Project Stats is not rendered. |
| **Empty kanban column** | Column renders with a dashed placeholder showing "—". |

### Error States

- The `/api/discussions` endpoint is wrapped in `.catch(() => [])`, so discussion failures degrade gracefully to a count of zero without breaking the Dashboard.
- Other endpoint failures propagate to the global API error handler. The Dashboard does not render partial data if a primary endpoint fails.

## Accessibility Notes

- **Progress bars** use `role="progressbar"` and `aria-valuenow` attributes for screen readers.
- **Navigation icons** (emoji) use `aria-hidden="true"` to prevent screen readers from announcing decorative icons.
- **Active page** is marked with `aria-current="page"` on the sidebar link.
- **Color + text**: All status-dependent information is conveyed through both color and text/icons, not color alone (e.g., kanban columns have colored borders AND uppercase status labels; score dots show numeric values AND use color).
- **Keyboard navigation**: The `g` + `d` shortcut provides keyboard access. Interactive elements (kanban cards, activity items, milestone cards, roadmap items) are clickable `<div>` elements — future improvement could add `role="button"` and `tabindex="0"` for full keyboard accessibility.
- **Contrast**: Dark mode uses CSS custom properties (`var(--bg)`, `var(--text)`, `var(--border)`) to maintain readable contrast in both themes.
