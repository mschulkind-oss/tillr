# Cycles — Screen Specification

## Overview

The Cycles page is the operational command center for iteration cycles — the structured, multi-step workflows that drive features from inception to completion. It surfaces active and completed cycles across all features, providing real-time visibility into which step each cycle is on, who (or what role) is currently active, how quality scores are trending, and how many iterations a cycle has gone through.

The page has two layers:

1. **Cycles List View** — a filterable list of cycle cards showing all active cycles first, then completed/failed cycles, each with an inline pipeline visualization, score sparkline, and key metadata.
2. **Cycle Detail View** — a drill-down view for a single cycle with a vertical step-by-step stepper, full score history with iteration tabs, a canvas-rendered score chart, and individual score cards.

Navigation: clicking a cycle card transitions to the detail view; a "← Back to Cycles" button returns to the list. Feature IDs are clickable cross-links that navigate to the Features page.

### Predefined Cycle Types

| Cycle Type | Icon | Steps | Quality Gate |
|---|---|---|---|
| UI Refinement | 🎨 | design → ux-review → develop → manual-qa → judge | Score ≥ 8.5 or human override |
| Feature Implementation | ⚙️ | research → develop → agent-qa → judge → human-qa | Score ≥ 8.0 AND human approval |
| Roadmap Planning | 📋 | research → plan → create-roadmap → prioritize → human-review | Human approval |
| Bug Triage | 🐛 | report → reproduce → root-cause → fix → verify | Reproduction test passes, no regressions |
| Documentation | 📖 | research → draft → review → edit → publish | Reviewer + editor approval |
| Architecture Review | 🏗️ | analyze → propose → discuss → decide → implement | Human decision + plan approved |
| Release | 🚀 | freeze → qa → fix → staging → verify → ship | Tests pass + staging verified |
| Onboarding/DX | 👋 | try → friction-log → improve → verify → document | Zero blockers, ≤ 3 annoying friction points |
| Spec Iteration | 📝 | research → draft-spec → review → judge → human-review | Human approval |

---

## User Roles & Personas

### Product Owner / Human Reviewer
Needs to see which cycles are blocked on human input (human-qa, human-review steps), approve or reject work, and monitor overall quality trends across features.

### Agent Orchestrator
An AI agent (or its supervisor) checking which step is active, what the current score trajectory looks like, and whether a cycle is looping excessively (high iteration count may signal a problem).

### Developer / Contributor
Wants to understand where a feature stands in its lifecycle, what steps remain, and what feedback judges or reviewers have given on prior steps.

### Project Manager
Monitors cycle throughput, identifies bottlenecks (cycles stuck on one step), and tracks quality score trends across the project.

---

## User Stories

### US-1: View All Active Cycles

**As a** product owner,
**I want** to see all currently active iteration cycles at a glance,
**so that** I know what work is in flight and which steps are being executed right now.

**Given** the project has one or more active cycles,
**When** I navigate to the Cycles page,
**Then** I see a list of cycle cards, with active cycles displayed first, each showing:
- The associated feature ID (clickable link to Features page)
- A status badge ("active")
- The cycle type badge in uppercase (e.g., "FEATURE IMPLEMENTATION")
- The iteration number badge (e.g., "⟳ Iteration 2")
- A horizontal pipeline visualization showing all steps as nodes, with completed steps marked ✓ in green, the current step highlighted in blue with a pulsing animation, and future steps grayed out
- A step count label (e.g., "2/5 steps")
- A progress bar filled proportionally to `current_step / total_steps`

### US-2: View Completed and Failed Cycles

**As a** project manager,
**I want** to see completed and failed cycles below the active ones,
**so that** I can review historical cycle outcomes.

**Given** the project has completed or failed cycles,
**When** I navigate to the Cycles page,
**Then** completed/failed cycles appear in a separate section below active cycles, each rendered as a cycle card with a "completed" or "failed" status badge. All pipeline nodes show as done (✓) for completed cycles.

### US-3: View Average Score on Cycle Card

**As an** agent orchestrator,
**I want** to see the average judge score on each cycle card,
**so that** I can quickly assess quality without drilling into details.

**Given** a cycle has at least one score recorded,
**When** the cycle card renders,
**Then** it displays a score badge showing "★ {avg} avg" where `avg` is the mean of all scores, formatted to one decimal place. The badge is color-coded:
- **Green** (`score-high`): average ≥ 7
- **Orange** (`score-mid`): average ≥ 4 and < 7
- **Red** (`score-low`): average < 4

**Given** a cycle has no scores,
**When** the cycle card renders,
**Then** no score badge is displayed.

### US-4: View Score Sparkline on Cycle Card

**As a** product owner,
**I want** to see a score trend sparkline on cycles with multiple scores,
**so that** I can see at a glance whether quality is improving or declining.

**Given** a cycle has 2 or more scores,
**When** the cycle card renders,
**Then** it displays an inline SVG sparkline (180×44px) below the pipeline, showing:
- A filled polygon area with gradient fill (accent color, low opacity)
- A polyline stroke tracing score values over time
- Circle dots at each data point
- A label to the right showing "{n} scores"
- Y-axis range fixed at 0–10

**Given** a cycle has fewer than 2 scores,
**When** the cycle card renders,
**Then** no sparkline is shown.

### US-5: View Step-Level Scores on Pipeline Nodes

**As a** developer,
**I want** to see individual step scores directly on the pipeline visualization,
**so that** I can identify which steps received which scores without opening the detail view.

**Given** a step in the pipeline has a corresponding score,
**When** the cycle card renders,
**Then** the pipeline node for that step shows a small score value below the step label, rendered with the `.cycle-node-score` class in accent color.

### US-6: Drill Into Cycle Detail

**As a** product owner,
**I want** to click on a cycle card to see its full detail,
**so that** I can review all scores, step-by-step progress, and judge notes.

**Given** I am on the Cycles list view,
**When** I click on a cycle card (but not on the feature ID link),
**Then** the list view is replaced by the Cycle Detail view, showing:
- A "← Back to Cycles" button
- The cycle type icon (emoji) and name
- The feature ID (clickable link), status badge, and iteration count
- The average score (if scores exist)
- A progress label (e.g., "60% complete (3/5 steps)")
- A thin progress bar
- A vertical stepper showing all steps
- A score history section
- Created/updated timestamps

### US-7: Navigate Back from Cycle Detail

**As a** user,
**I want** to return to the cycles list from the detail view,
**so that** I can review other cycles.

**Given** I am on the Cycle Detail view,
**When** I click the "← Back to Cycles" button,
**Then** the internal cycle detail state is cleared and the Cycles list view is rendered.

### US-8: View Vertical Step Stepper in Detail

**As a** developer,
**I want** to see each step of the cycle in a vertical stepper layout,
**so that** I can understand exactly where the cycle is in its progression.

**Given** I am on the Cycle Detail view,
**When** the stepper renders,
**Then** each step is displayed vertically with:
- A circular indicator: green ✓ for completed steps, blue with pulsing animation for the active step, gray for pending steps
- The step name in capitalized form
- A status badge: "DONE" (green), "ACTIVE" (blue), or "PENDING" (gray)
- Vertical connector lines between steps (green for completed transitions, gray for pending)

**Given** a step has one or more scores,
**When** the stepper renders that step,
**Then** each score is displayed inline as a color-coded badge with the score value, iteration label (e.g., "Iter 1"), optional notes text, and timestamp.

### US-9: View Score History with Iteration Tabs

**As a** product owner,
**I want** to filter scores by iteration,
**so that** I can compare quality across different iterations of the same cycle.

**Given** a cycle has scores across multiple iterations (max iteration > 1),
**When** the detail view renders,
**Then** iteration tabs appear: "All" (default, selected), "Iteration 1", "Iteration 2", etc. Each tab shows a count of scores for that iteration. Clicking a tab filters the score chart and score cards to only show scores from that iteration.

**Given** a cycle has only one iteration,
**When** the detail view renders,
**Then** no iteration tabs are shown.

### US-10: View Score History Chart (Canvas Sparkline)

**As an** agent orchestrator,
**I want** to see a visual chart of score history,
**so that** I can evaluate quality trends over time.

**Given** the detail view is open and the selected iteration filter yields 2+ scores,
**When** the score history section renders,
**Then** a canvas-based sparkline (600×80px) is drawn with:
- Y-axis gridlines at 0, 2.5, 5, 7.5, 10 with faint labels
- A gradient-filled area under the score line
- A solid accent-colored line connecting score points
- Color-coded dots at each point: green (≥ 8), orange (6–8), red (< 6)

**Given** fewer than 2 scores match the current iteration filter,
**When** the section renders,
**Then** no chart is displayed.

### US-11: View Individual Score Cards

**As a** product owner,
**I want** to read the judge's notes for each score,
**so that** I can understand the reasoning behind quality assessments.

**Given** the detail view is open,
**When** the score history section renders,
**Then** a responsive grid of score cards is displayed, each showing:
- A large score badge (color-coded: green ≥ 8, orange 6–8, red < 6)
- The step name (capitalized)
- The timestamp (formatted)
- The iteration label (if multiple iterations exist)
- The judge's notes (if present), separated by a border-top divider, with `white-space: pre-wrap` for formatting

### US-12: Navigate to Feature from Cycle

**As a** developer,
**I want** to click the feature ID on a cycle card or in the detail view,
**so that** I can jump directly to the feature's detail page.

**Given** a cycle card or detail view displays a feature ID,
**When** I click the feature ID link,
**Then** the app navigates to the Features page with that feature selected. The click does not trigger the cycle card's own click handler (event propagation is stopped).

### US-13: View Cycle Types Reference

**As a** new user,
**I want** to see all available cycle types and their steps,
**so that** I understand the workflow options available.

**Given** the Cycles page renders and the cycle types reference section is visible,
**When** I view the cycle types grid,
**Then** I see a responsive grid of cards, each showing:
- The cycle type name (capitalized, bold)
- The count of cycles using that type (if any)
- The ordered steps separated by arrow (→) indicators, each step capitalized

### US-14: View Empty State

**As a** user,
**I want** to see a helpful message when no cycles exist,
**so that** I know the page is working and understand how to create cycles.

**Given** the project has no cycles,
**When** I navigate to the Cycles page,
**Then** an empty state message is displayed indicating no cycles are running, with guidance on how to start one via the CLI (`lifecycle cycle start <type> <feature>`).

### US-15: Expanded Cycle Card with Judge Scores Table

**As a** developer,
**I want** to expand a cycle card inline to see a quick summary of scores,
**so that** I can review scores without navigating to the full detail view.

**Given** a cycle card is clicked and the detail view is shown (or expanded inline),
**When** the card has scores,
**Then** a collapsible detail section may appear showing a table with columns: Step, Score (as a color-coded badge), Notes, and Time.

### US-16: Real-Time Updates via WebSocket

**As a** product owner monitoring active work,
**I want** the Cycles page to update automatically when cycle state changes,
**so that** I always see the latest step, score, and status without refreshing.

**Given** I am viewing the Cycles page and a WebSocket connection is established,
**When** a cycle advances to a new step, receives a new score, or changes status,
**Then** the page re-renders with the updated data without requiring a manual refresh.

---

## Screen Layout

### Cycles List View

```
┌─────────────────────────────────────────────────────────────────┐
│  Cycles                                                         │
│                                                                 │
│  ┌─ Active Cycles ────────────────────────────────────────────┐ │
│  │                                                             │ │
│  │  ┌─ Cycle Card ──────────────────────────────────────────┐ │ │
│  │  │  feat-auth-redesign                    ● active        │ │ │
│  │  │                                                        │ │ │
│  │  │  FEATURE IMPLEMENTATION  ⟳ Iteration 2  ★ 7.3 avg    │ │ │
│  │  │                                          2/5 steps     │ │ │
│  │  │                                                        │ │ │
│  │  │  ●──────●──────◉──────○──────○                        │ │ │
│  │  │  research develop agent-qa judge human-qa              │ │ │
│  │  │   ✓       ✓      (3)                                   │ │ │
│  │  │          7.5                                           │ │ │
│  │  │                                                        │ │ │
│  │  │  ████████████████░░░░░░░░░░░░░  (40%)                 │ │ │
│  │  │                                                        │ │ │
│  │  │  ╱╲  ╱╲  ╱╲                                           │ │ │
│  │  │ ╱  ╲╱  ╲╱  ╲  5 scores                               │ │ │
│  │  └────────────────────────────────────────────────────────┘ │ │
│  │                                                             │ │
│  │  ┌─ Cycle Card ──────────────────────────────────────────┐ │ │
│  │  │  ...                                                   │ │ │
│  │  └────────────────────────────────────────────────────────┘ │ │
│  └─────────────────────────────────────────────────────────────┘ │
│                                                                 │
│  ┌─ Completed Cycles ────────────────────────────────────────┐  │
│  │  ┌─ Cycle Card ──────────────────────────────────────────┐│  │
│  │  │  feat-login-flow                     ● completed       ││  │
│  │  │  ...                                                   ││  │
│  │  └────────────────────────────────────────────────────────┘│  │
│  └────────────────────────────────────────────────────────────┘  │
│                                                                 │
│  ┌─ Cycle Types Reference ────────────────────────────────────┐ │
│  │  ┌──────────────────┐  ┌──────────────────┐               │ │
│  │  │ UI Refinement    │  │ Feature Impl.    │  ...          │ │
│  │  │ design → ux →    │  │ research →       │               │ │
│  │  │ develop → qa →   │  │ develop → qa →   │               │ │
│  │  │ judge            │  │ judge → human    │               │ │
│  │  └──────────────────┘  └──────────────────┘               │ │
│  └────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

### Cycle Detail View

```
┌─────────────────────────────────────────────────────────────────┐
│  ← Back to Cycles                                               │
│                                                                 │
│  ⚙️  Feature Implementation                                     │
│      feat-auth-redesign · ● active · Iteration 2 · ★ 7.3 avg  │
│                                                                 │
│      60% complete (3/5 steps)                                   │
│      ████████████████████░░░░░░░░░░░░                           │
│                                                                 │
│  ┌─ Steps ────────────────────────────────────────────────────┐ │
│  │                                                             │ │
│  │  ● Research ──────────────────────────── DONE               │ │
│  │  │   ┌─ 7.5  Iter 1  "Good coverage"  Jan 15 10:45 ┐      │ │
│  │  │   └────────────────────────────────────────────────┘      │ │
│  │  │                                                          │ │
│  │  ● Develop ───────────────────────────── DONE               │ │
│  │  │                                                          │ │
│  │  │                                                          │ │
│  │  ◉ Agent QA ──────────────────────────── ACTIVE             │ │
│  │  │  (pulsing blue)                                          │ │
│  │  │                                                          │ │
│  │  ○ Judge ─────────────────────────────── PENDING            │ │
│  │  │                                                          │ │
│  │  │                                                          │ │
│  │  ○ Human QA ──────────────────────────── PENDING            │ │
│  │                                                             │ │
│  └─────────────────────────────────────────────────────────────┘ │
│                                                                 │
│  ┌─ Score History ────────────────────────────────────────────┐ │
│  │                                                             │ │
│  │  [ All ]  [ Iteration 1 (3) ]  [ Iteration 2 (2) ]        │ │
│  │                                                             │ │
│  │  ┌─ Canvas Chart (600×80) ───────────────────────────────┐ │ │
│  │  │  10 ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─              │ │ │
│  │  │ 7.5 ─ ─ ─ ─ ─ ●─────────●─ ─ ─ ─ ─ ─               │ │ │
│  │  │   5 ─ ─ ─●───/─ ─ ─ ─ ─ ─\──●─ ─ ─ ─               │ │ │
│  │  │ 2.5 ─ ─/─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─               │ │ │
│  │  │   0 ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─               │ │ │
│  │  └────────────────────────────────────────────────────────┘ │ │
│  │                                                             │ │
│  │  ┌─ Score Card ────┐  ┌─ Score Card ────┐                 │ │
│  │  │  7.5  Research   │  │  8.2  Agent QA  │   ...          │ │
│  │  │  Jan 15 10:45    │  │  Jan 15 12:00   │                │ │
│  │  │  Iter 1          │  │  Iter 1         │                │ │
│  │  │ ──────────────── │  │ ──────────────  │                │ │
│  │  │  Good approach   │  │  Tests look     │                │ │
│  │  │  but needs more  │  │  comprehensive  │                │ │
│  │  └──────────────────┘  └─────────────────┘                │ │
│  └─────────────────────────────────────────────────────────────┘ │
│                                                                 │
│  Created: Jan 15, 2025 10:00    Updated: Jan 15, 2025 11:30    │
└─────────────────────────────────────────────────────────────────┘
```

---

## Data Requirements

### API Endpoints

| Endpoint | Method | Description | Response Shape |
|---|---|---|---|
| `/api/cycles` | GET | List all cycles (active + completed, deduplicated) | `CycleInstance[]` |
| `/api/cycles/{id}` | GET | Single cycle detail with scores and step names | `CycleDetail` |
| `/api/cycles/{id}/scores` | GET | All scores for a specific cycle | `CycleScore[]` |
| `/api/cycles/{featureId}/history` | GET | All cycle instances for a feature | `CycleInstance[]` |
| `/ws` | WebSocket | Real-time push on any DB change | Event messages |

### Data Models

#### CycleInstance
```json
{
  "id": 1,
  "feature_id": "feat-auth-redesign",
  "cycle_type": "feature-implementation",
  "current_step": 2,
  "iteration": 1,
  "status": "active",
  "created_at": "2025-01-15T10:00:00Z",
  "updated_at": "2025-01-15T11:30:00Z",
  "step_name": "agent-qa"
}
```

| Field | Type | Description |
|---|---|---|
| `id` | int | Primary key |
| `feature_id` | string | References the feature this cycle belongs to |
| `cycle_type` | string | One of the 9 predefined cycle type names |
| `current_step` | int | 0-based index into the cycle's steps array |
| `iteration` | int | 1-based iteration counter; increments on loop-back |
| `status` | string | `active`, `completed`, or `failed` |
| `step_name` | string | Computed: human-readable name of the current step |
| `created_at` | string | ISO 8601 timestamp |
| `updated_at` | string | ISO 8601 timestamp |

#### CycleDetail (GET `/api/cycles/{id}` response)
```json
{
  "cycle": { /* CycleInstance */ },
  "scores": [ /* CycleScore[] */ ],
  "steps": ["research", "develop", "agent-qa", "judge", "human-qa"]
}
```

#### CycleScore
```json
{
  "id": 5,
  "cycle_id": 1,
  "step": 0,
  "iteration": 1,
  "score": 7.5,
  "notes": "Good approach but needs more edge case coverage",
  "created_at": "2025-01-15T10:45:00Z"
}
```

| Field | Type | Description |
|---|---|---|
| `id` | int | Primary key |
| `cycle_id` | int | References the parent CycleInstance |
| `step` | int | 0-based index of the step that was scored |
| `iteration` | int | Which iteration this score belongs to |
| `score` | float64 | Numeric score, 0.0–10.0 scale |
| `notes` | string | Optional judge feedback/reasoning |
| `created_at` | string | ISO 8601 timestamp |

### Client-Side Cycle Type Registry

Step definitions are duplicated client-side for rendering the pipeline without an extra API call:

```javascript
const cycleTypeSteps = {
  'ui-refinement':        ['design','ux-review','develop','manual-qa','judge'],
  'feature-implementation':['research','develop','agent-qa','judge','human-qa'],
  'roadmap-planning':     ['research','plan','create-roadmap','prioritize','human-review'],
  'bug-triage':           ['report','reproduce','root-cause','fix','verify'],
  'documentation':        ['research','draft','review','edit','publish'],
  'architecture-review':  ['analyze','propose','discuss','decide','implement'],
  'release':              ['freeze','qa','fix','staging','verify','ship'],
  'onboarding-dx':        ['try','friction-log','improve','verify','document'],
  'spec-iteration':       ['research','draft-spec','review','judge','human-review'],
};
```

Icons are also mapped client-side:
```javascript
const cycleTypeIcons = {
  'ui-refinement': '🎨',       'feature-implementation': '⚙️',
  'roadmap-planning': '📋',    'bug-triage': '🐛',
  'documentation': '📖',       'architecture-review': '🏗️',
  'release': '🚀',             'onboarding-dx': '👋',
  'spec-iteration': '📝',
};
```

### Data Fetching Strategy

1. **List view**: Calls `GET /api/cycles` to get all cycles. Then, in parallel, calls `GET /api/cycles/{id}/scores` for each cycle to fetch scores. Failures default to empty score arrays.
2. **Detail view**: Calls `GET /api/cycles/{id}` which returns a `CycleDetail` with embedded scores and step names. If that fails, falls back to fetching `GET /api/cycles` (full list) + `GET /api/cycles/{id}/scores` separately and assembling the detail locally.

---

## Interactions

### Cycle Card Click → Detail View
- **Trigger**: Click on a `.cycle-card` element (anywhere except the feature ID link).
- **Behavior**: Stores the cycle ID in `App._activeCycleDetail`, calls `App.showCycleDetail(cycleId)`, which loads data and renders the detail view in place of the list.
- **Event handling**: `e.stopPropagation()` prevents bubbling. Clicks on `.clickable-feature` inside the card are excluded from this handler and instead navigate to the Features page.

### Feature ID Link Click
- **Trigger**: Click on a `.clickable-feature` element (on the cycle card or in the detail header).
- **Behavior**: Reads `data-feature-id` attribute, sets `App._navContext = { featureId: fid }`, and calls `App.navigate('features')`.

### Back Button (Detail → List)
- **Trigger**: Click on `#cdBack` button.
- **Behavior**: Clears `App._activeCycleDetail` and `App._cycleDetailIter`, then calls `App.navigate('cycles')` to re-render the list view.

### Iteration Tab Selection
- **Trigger**: Click on a `.cd-iter-tab` button in the detail view.
- **Behavior**: Reads `data-iter` attribute (0 = "All", 1+ = specific iteration), stores in `App._cycleDetailIter`, and re-renders the detail view with filtered scores. The active tab receives the `.active` class.

### Cycle Card Expansion (Inline Detail)
- **Trigger**: Clicking a cycle card toggles a `.cycle-detail` section within the card.
- **Behavior**: The card receives an `.expanded` class (accent border, elevated shadow). The detail section (`display: none` by default) becomes visible, showing a judge scores table.

### WebSocket-Driven Re-render
- **Trigger**: WebSocket message indicating a DB change.
- **Behavior**: If the current page is "cycles", the entire view is re-fetched and re-rendered. If a detail view is open, it is re-rendered with fresh data while preserving the iteration tab selection.

---

## State Handling

### Application State Variables

| Variable | Type | Purpose |
|---|---|---|
| `App._activeCycleDetail` | `int \| null` | Cycle ID currently shown in detail view. `null` = list view. |
| `App._cycleDetailIter` | `int` | Iteration filter for the detail view. `0` = "All", `1+` = specific iteration. |
| `App._navContext` | `object \| null` | Cross-page navigation context (e.g., `{ featureId: "..." }`). |

### View States

#### List View (default)
- `_activeCycleDetail === null`
- Fetches all cycles + scores in parallel
- Partitions into active and completed/failed
- Renders cycle cards, pipeline, sparklines

#### Detail View
- `_activeCycleDetail === <cycleId>`
- Fetches single cycle detail (with fallback)
- Renders stepper, score chart, score cards
- Iteration tab controls filter which scores are displayed

### Score Color Classification

Used consistently across list and detail views:

| Score Range | CSS Class | Color | Usage |
|---|---|---|---|
| ≥ 8 | `score-green` | `var(--success)` / #3fb950 | Score badges, chart dots |
| ≥ 6 and < 8 | `score-yellow` | `var(--warning)` / #d29922 | Score badges, chart dots |
| < 6 | `score-red` | `var(--danger)` / #f85149 | Score badges, chart dots |

For the average score badge on cycle cards, a different threshold is used:

| Average Score | CSS Class | Meaning |
|---|---|---|
| ≥ 7 | `score-high` | Good quality |
| ≥ 4 and < 7 | `score-mid` | Needs attention |
| < 4 | `score-low` | Poor quality |

### Error / Edge-Case States

| Condition | Behavior |
|---|---|
| No cycles exist | Empty state message displayed |
| API fetch fails for scores | Defaults to empty array; card renders without score data |
| Cycle type not in client registry | Falls back to empty steps array; pipeline is not rendered |
| Detail API fails | Falls back to list+scores parallel fetch |
| WebSocket disconnects | Page still works via manual navigation; no live updates until reconnect |

---

## Accessibility Notes

### Keyboard Navigation
- Cycle cards should be focusable and activatable via Enter/Space (they are `<div>` elements with click handlers; adding `tabindex="0"` and `role="button"` is recommended).
- The "← Back to Cycles" button is a `<button>` element and is natively keyboard-accessible.
- Iteration tabs are `<button>` elements and are natively keyboard-accessible.
- Feature ID links should be focusable and activatable via Enter.

### Screen Reader Considerations
- Pipeline step states (done/active/pending) rely on visual indicators (color, checkmark, animation). Add `aria-label` attributes to each pipeline node describing its state (e.g., `aria-label="Step 1: Research — completed"`).
- The pulsing animation on active steps is purely decorative and does not convey information not available through other means (the "ACTIVE" text badge duplicates this).
- Score color coding conveys meaning (green = good, red = bad). The numeric score value is always present alongside the color, so the information is not color-dependent.
- Sparkline charts (both SVG and canvas) should have `aria-label` or `role="img"` with a text description of the trend (e.g., `aria-label="Score trend: 5 scores, average 7.3"`).

### Motion and Animation
- The `cyclePulse` animation (pulsing box-shadow on active steps) uses a subtle 2-second infinite loop. It should respect `prefers-reduced-motion` by being disabled when the user has requested reduced motion.

### Color and Contrast
- Score badges use both color and text (the numeric value) to convey information, meeting WCAG guidelines for not relying on color alone.
- Dark mode and light mode are both supported. The light mode overrides adjust background opacity for cycle type badges and iteration badges to maintain contrast.
- Status badges ("active", "completed", "failed") use the shared `.badge-{status}` pattern which includes text labels, not just color.

### Semantic HTML
- The page heading ("Cycles") provides landmark navigation.
- Score history tables use `<table>` with appropriate `<th>` headers.
- The detail view stepper would benefit from `<ol>` semantics with `aria-current="step"` on the active step to convey progress to assistive technology.
