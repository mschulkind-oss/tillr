# Agents Dashboard — Screen Specification

## Overview

The Agents Dashboard is the real-time monitoring hub for AI agent activity within a Lifecycle-managed project. It answers the question *"What are my agents doing right now?"* at a glance, and *"What happened?"* on drill-down.

The page is divided into four visual zones:

1. **Stats bar** — aggregate session metrics (total, active, completed, success rate).
2. **Active agents** — live cards showing each running agent's name, phase, progress, ETA, task description, linked feature, and a timeline of recent status updates.
3. **Empty state** — a friendly placeholder when no agents are active.
4. **Completed & failed sessions** — a collapsible history section for finished work.

All data refreshes automatically via WebSocket; no manual reload is needed.

### Navigation

- **Route**: `#agents` (SPA hash route)
- **Nav label**: 🤖 Agents (sidebar)
- **Renderer**: `App.renderAgents()` in `app4.js`

---

## User Roles & Personas

| Persona | Description | Primary goals on this screen |
|---------|-------------|------------------------------|
| **Human Supervisor** | Product owner or tech lead monitoring delegated agent work | See which agents are active, check progress, spot failures, review completed work |
| **DevOps / Infra Engineer** | Responsible for agent infrastructure and reliability | Monitor success rates, identify stuck agents, check ETA accuracy |
| **Agent (programmatic)** | AI agent posting status updates via the API | Not a viewer — produces the data this screen consumes |

> **Design principle**: This is an *agent-first monitoring view*. The primary user is a human watching AI agents work. The UI should communicate status quickly and clearly, the way a CI dashboard communicates build status.

---

## User Stories

### US-1: View aggregate agent metrics

> **As a** human supervisor,
> **I want to** see total, active, completed, and success-rate counts at the top of the page,
> **So that** I can assess overall agent health in under two seconds.

**Acceptance criteria (Given / When / Then):**

| # | Given | When | Then |
|---|-------|------|------|
| 1.1 | 5 agent sessions exist (2 active, 2 completed, 1 failed) | I open the Agents Dashboard | The stats bar shows: Total Sessions = 5, Active = 2, Completed = 2, Success Rate = 40% |
| 1.2 | No agent sessions exist | I open the Agents Dashboard | The stats bar shows: Total Sessions = 0, Active = 0, Completed = 0, Success Rate = 0% |
| 1.3 | All sessions are completed (none failed) | I view the stats bar | Success Rate = 100% |

---

### US-2: Monitor an active agent's progress

> **As a** human supervisor,
> **I want to** see a progress bar, current phase, and ETA for each active agent,
> **So that** I can estimate when work will be done and whether the agent is making forward progress.

**Acceptance criteria:**

| # | Given | When | Then |
|---|-------|------|------|
| 2.1 | An active agent has `progress_pct = 45`, `current_phase = "develop"`, and `eta = "2025-01-15T14:30:00Z"` | I view its card | I see a progress bar filled to 45%, a blue "develop" phase badge, and "ETA: 2025-01-15T14:30:00Z" |
| 2.2 | An active agent has no phase or ETA set | I view its card | The phase badge and ETA label are both absent (no empty placeholders) |
| 2.3 | An agent's progress changes from 45% to 60% | The WebSocket delivers a refresh event | The progress bar animates smoothly from 45% to 60% (0.8s cubic-bezier transition) |
| 2.4 | An active agent has `task_description` set | I view its card | The task description appears below the agent name |
| 2.5 | An active agent has no `task_description` | I view its card | No empty description row is rendered |

---

### US-3: Read an agent's status timeline

> **As a** human supervisor,
> **I want to** see the most recent status updates for each active agent,
> **So that** I can understand what the agent has accomplished and what it is doing now.

**Acceptance criteria:**

| # | Given | When | Then |
|---|-------|------|------|
| 3.1 | An active agent has 12 status updates | I view its card | Only the 5 most recent updates are shown (ordered newest first) |
| 3.2 | A status update has `phase = "research"` and `message_md = "## Findings\nFound 3 relevant patterns"` | I view the timeline entry | I see a "research" phase badge, a relative timestamp (e.g., "2 min ago"), and the markdown rendered as HTML (h2 heading + paragraph) |
| 3.3 | An active agent has 0 status updates | I view its card | The "Recent Updates" section is not rendered |
| 3.4 | A status update has no `phase` | I view the timeline entry | An empty span replaces the badge; the timestamp and message still render |

---

### US-4: Navigate from agent to linked feature

> **As a** human supervisor,
> **I want to** click a feature ID on an agent card to navigate to that feature's detail page,
> **So that** I can see the full context of what the agent is working on.

**Acceptance criteria:**

| # | Given | When | Then |
|---|-------|------|------|
| 4.1 | An active agent has `feature_id = "feat-auth"` | I view its card | I see "Feature: feat-auth" rendered as a clickable link |
| 4.2 | I click the feature link | — | The app navigates to the feature detail view for `feat-auth` |
| 4.3 | An agent has no `feature_id` | I view its card | No feature row is rendered |

---

### US-5: See empty state when no agents are active

> **As a** human supervisor,
> **I want to** see a friendly empty state instead of a blank page when no agents are active,
> **So that** I know the dashboard is working and understand how agents will appear.

**Acceptance criteria:**

| # | Given | When | Then |
|---|-------|------|------|
| 5.1 | 0 active agents (but completed/failed sessions may exist) | I open the Agents Dashboard | I see the 🤖 emoji, "No active agents" heading, and hint text "Agents will appear here when they start working on tasks." |
| 5.2 | An agent transitions from active to completed | The WebSocket triggers a refresh | The active section transitions to the empty state; the agent moves to the collapsed history section |

---

### US-6: Review completed and failed sessions

> **As a** human supervisor,
> **I want to** review past agent sessions grouped in a collapsible section,
> **So that** I can audit what agents have done without cluttering the active view.

**Acceptance criteria:**

| # | Given | When | Then |
|---|-------|------|------|
| 6.1 | 3 completed and 1 failed session exist | I view the page | I see a collapsed `<details>` element labeled "Completed & Failed Sessions (4)" |
| 6.2 | I expand the section | — | Each past session shows: status icon (✅ completed / ❌ failed), name, status badge, relative timestamp, and task description (if present) |
| 6.3 | A past session's status is "completed" | I view its row | It uses the `status-done` badge (green) |
| 6.4 | A past session's status is "failed" | I view its row | It uses the `status-blocked` badge (red) |
| 6.5 | No completed or failed sessions exist | I view the page | The collapsible section is not rendered |

---

### US-7: Receive live updates without manual refresh

> **As a** human supervisor,
> **I want** the dashboard to update automatically when agent state changes,
> **So that** I always see current information without reloading the page.

**Acceptance criteria:**

| # | Given | When | Then |
|---|-------|------|------|
| 7.1 | I am viewing the Agents Dashboard | An agent posts a status update via `POST /api/agents/{id}/update` | The database change triggers `watchDBFile()`, which broadcasts `{ "type": "refresh" }` over WebSocket; the client re-renders the page |
| 7.2 | The WebSocket connection drops | 3 seconds elapse | The client automatically reconnects |
| 7.3 | I am viewing a different page | An agent update occurs | No re-render happens until I navigate back to the Agents page |

---

### US-8: View the dashboard on a mobile device

> **As a** human supervisor on the go,
> **I want** the dashboard to be usable on a phone screen,
> **So that** I can check on agent progress from anywhere.

**Acceptance criteria:**

| # | Given | When | Then |
|---|-------|------|------|
| 8.1 | Viewport ≤ 480px | I view the stats bar | Stats cards stack into a single column |
| 8.2 | Viewport 481–800px | I view the stats bar | Stats cards arrange in a 2-column grid |
| 8.3 | Viewport > 800px | I view the stats bar | Stats cards arrange in a 4-column grid (or auto-fit) |

---

### US-9: Use the dashboard in light and dark mode

> **As a** human supervisor,
> **I want** the Agents Dashboard to respect my theme preference,
> **So that** it is comfortable to view in any lighting condition.

**Acceptance criteria:**

| # | Given | When | Then |
|---|-------|------|------|
| 9.1 | Theme is dark (default) | I view the page | Card backgrounds are `#1c2128`, text is `#e6edf3`, accent is `#58a6ff` |
| 9.2 | Theme is light (`data-theme="light"`) | I view the page | Card backgrounds are `#ffffff`, text is `#1b1f24`, accent is `#0969da` |
| 9.3 | Theme is dark | I view a status badge | Badge uses dark-appropriate colors (e.g., implementing = dark blue bg `#0c2d6b` with `#58a6ff` text) |
| 9.4 | Theme is light | I view a status badge | Badge uses light-appropriate colors (e.g., implementing = light blue bg `#ddf4ff` with `#0969da` text) |

---

## Screen Layout

### Visual Structure

```
┌────────────────────────────────────────────────────────────────┐
│  PAGE HEADER                                                   │
│  🤖 Agent Dashboard                                           │
│  {N} active agent(s)                                           │
├────────────────────────────────────────────────────────────────┤
│  STATS BAR  (stats-grid — responsive 4-column grid)            │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐         │
│  │ Total    │ │ Active   │ │ Completed│ │ Success  │         │
│  │ Sessions │ │          │ │          │ │ Rate     │         │
│  │   12     │ │    3     │ │    8     │ │   89%    │         │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘         │
├────────────────────────────────────────────────────────────────┤
│  ACTIVE AGENTS  (or empty state)                               │
│  ┌────────────────────────────────────────────────────────┐    │
│  │  Agent Name      agent-1234567890   [develop]  ETA: … │    │
│  │  Task description text here                            │    │
│  │  Feature: feat-auth (clickable)                        │    │
│  │  ████████████░░░░░░░░░░░░░░░░░░  45%                  │    │
│  │  45% complete · Last active 2 min ago                  │    │
│  │  ─────────── Recent Updates ───────────                │    │
│  │  [research]  3 min ago                                 │    │
│  │  Markdown-rendered message content                     │    │
│  │  [planning]  10 min ago                                │    │
│  │  Another update message                                │    │
│  └────────────────────────────────────────────────────────┘    │
│  ┌────────────────────────────────────────────────────────┐    │
│  │  Another Agent    agent-9876543210   [research]        │    │
│  │  ...                                                   │    │
│  └────────────────────────────────────────────────────────┘    │
├────────────────────────────────────────────────────────────────┤
│  ▶ Completed & Failed Sessions (5)  [collapsed <details>]      │
│    ✅ Agent Name Three    [done]    1 hour ago                 │
│    ❌ Agent Name Four     [failed]  3 hours ago                │
└────────────────────────────────────────────────────────────────┘
```

### Empty State (when no active agents)

```
┌────────────────────────────────────────────────────────────────┐
│                                                                │
│                          🤖                                    │
│                   No active agents                             │
│    Agents will appear here when they start working on tasks.   │
│                                                                │
└────────────────────────────────────────────────────────────────┘
```

### CSS Class Map

| Visual zone | CSS class(es) | Layout |
|-------------|---------------|--------|
| Page header | `.page-header`, `.page-title`, `.page-subtitle` | Block |
| Stats bar | `.stats-grid` > `.stat-card` > `.stat-value` + `.stat-label` | CSS Grid (`repeat(auto-fit, minmax(180px, 1fr))`) |
| Agent card | `.card` | Block with flex rows inside |
| Card header row | Inline flex (`justify-content: space-between`) | Flex |
| Phase badge | `.status-badge.status-implementing` | Inline-block pill |
| Progress bar | `.progress-bar` > `.progress-fill` | Block, 8px height |
| Status timeline | Border-top section with inline flex rows | Block + Flex |
| Empty state | `.empty-state`, `.empty-state-icon`, `.empty-state-text`, `.empty-state-hint` | Centered block |
| History section | HTML `<details>` / `<summary>` > `.card` | Native disclosure |

---

## Data Requirements

### API Calls Made by the Renderer

| Order | Endpoint | Method | Purpose |
|-------|----------|--------|---------|
| 1 | `GET /api/agents` | GET | Fetch all agent sessions for the current project |
| 2 | `GET /api/agents/{id}` | GET | Fetch detail + status updates for each **active** agent (N calls for N active agents) |

### Models

#### AgentSession

```
Field              Type     JSON key              Required  Notes
─────────────────  ───────  ────────────────────   ────────  ─────────────────────────────
ID                 string   "id"                   yes       Format: "agent-{unix_millis}"
ProjectID          string   "project_id"           yes       FK → projects
FeatureID          string   "feature_id"           no        FK → features; omitempty
Name               string   "name"                 yes       User-provided session name
TaskDescription    string   "task_description"     no        Free-text; omitempty
Status             string   "status"               yes       active|paused|completed|failed|abandoned
ProgressPct        int      "progress_pct"         yes       0–100
CurrentPhase       string   "current_phase"        no        e.g., "research", "develop"; omitempty
ETA                string   "eta"                  no        ISO 8601 timestamp; omitempty
ContextSnapshot    string   "context_snapshot"     no        JSON blob; omitempty
CreatedAt          string   "created_at"           yes       ISO 8601
UpdatedAt          string   "updated_at"           yes       ISO 8601
```

#### StatusUpdate

```
Field              Type     JSON key              Required  Notes
─────────────────  ───────  ────────────────────   ────────  ─────────────────────────────
ID                 int      "id"                   yes       Auto-increment PK
AgentSessionID     string   "agent_session_id"     yes       FK → agent_sessions
MessageMD          string   "message_md"           yes       Markdown-formatted message
ProgressPct        *int     "progress_pct"         no        Snapshot at time of update; pointer allows null
Phase              string   "phase"                no        Phase at time of update; omitempty
CreatedAt          string   "created_at"           yes       ISO 8601
```

### Derived / Computed Values (client-side)

| Value | Computation |
|-------|-------------|
| Active count | `agents.filter(a => a.status === 'active').length` |
| Completed count | `agents.filter(a => a.status === 'completed').length` |
| Failed count | `agents.filter(a => a.status === 'failed').length` |
| Success rate | `Math.round((completed.length / agents.length) * 100)` (0 if no sessions) |
| Past sessions | `agents.filter(a => a.status !== 'active')` (includes completed + failed) |
| Page subtitle | `"{active.length} active agent"` + plural `"s"` if count ≠ 1 |

### Database Tables

#### `agent_sessions`

```sql
CREATE TABLE IF NOT EXISTS agent_sessions (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    feature_id TEXT REFERENCES features(id),
    name TEXT NOT NULL,
    task_description TEXT,
    status TEXT NOT NULL DEFAULT 'active'
        CHECK(status IN ('active','paused','completed','failed','abandoned')),
    progress_pct INTEGER DEFAULT 0,
    current_phase TEXT,
    eta TEXT,
    context_snapshot TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_agent_sessions_project ON agent_sessions(project_id);
CREATE INDEX idx_agent_sessions_status ON agent_sessions(status);
```

#### `status_updates`

```sql
CREATE TABLE IF NOT EXISTS status_updates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_session_id TEXT NOT NULL REFERENCES agent_sessions(id),
    message_md TEXT NOT NULL,
    progress_pct INTEGER,
    phase TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_status_updates_session ON status_updates(agent_session_id);
```

---

## Interactions

### User Interactions

| # | Trigger | Behavior |
|---|---------|----------|
| I-1 | Click feature ID link (`.clickable-feature[data-feature-id]`) | SPA navigation to the feature detail view for that ID |
| I-2 | Click/toggle `<details>` summary ("Completed & Failed Sessions") | Native HTML disclosure expands or collapses the history section |
| I-3 | Hover on `.stat-card` | Border brightens to accent color, card lifts 2px (`translateY(-2px)`), box-shadow appears |
| I-4 | Hover on `.card` | Border brightens to accent color, card lifts 1px (`translateY(-1px)`), subtle box-shadow |

### System Interactions

| # | Trigger | Behavior |
|---|---------|----------|
| S-1 | WebSocket receives `{ "type": "refresh" }` | Client calls `App.navigate(currentPage)` → full re-render of `renderAgents()` |
| S-2 | WebSocket connection closes | Client waits 3 seconds, then automatically reconnects |
| S-3 | Database file changes (detected by `watchDBFile()`) | Server broadcasts refresh to all connected WebSocket clients |
| S-4 | `POST /api/agents/{id}/update` received | Creates `StatusUpdate` record; optionally updates `AgentSession.progress_pct` and `current_phase`; triggers S-3 |
| S-5 | `PATCH /api/agents/{id}` received | Updates `AgentSession` fields (status, progress, phase, ETA); triggers S-3 |
| S-6 | `POST /api/agents` received | Creates new `AgentSession` with status `"active"`; triggers S-3 |

### API Endpoints (Write Operations)

| Endpoint | Method | Body | Effect |
|----------|--------|------|--------|
| `/api/agents` | POST | `{ "name": "…", "task_description": "…", "feature_id": "…" }` | Create new active agent session (201 Created) |
| `/api/agents/{id}/update` | POST | `{ "message_md": "…", "progress_pct": N, "phase": "…" }` | Append status update + sync session fields (201 Created) |
| `/api/agents/{id}` | PATCH | `{ "progress_pct": N, "current_phase": "…", "eta": "…", "status": "…" }` | Partial update to session (200 OK) |

---

## State Handling

### Page States

| State | Condition | What renders |
|-------|-----------|--------------|
| **Loading** | API call in flight | Page is blank / previous content remains (no explicit loading spinner) |
| **Empty — no sessions** | `agents.length === 0` | Stats bar (all zeros), empty state with 🤖 emoji, no history section |
| **Empty — no active** | `active.length === 0` but `agents.length > 0` | Stats bar with totals, empty state with 🤖 emoji, collapsed history section with past sessions |
| **Active agents** | `active.length > 0` | Stats bar, agent cards with full detail, history section (if past sessions exist) |
| **WebSocket disconnected** | Connection lost | Auto-reconnect after 3s; stale data may display until reconnection |

### Agent Status State Machine

```
               ┌───────────┐
               │  (created) │
               └─────┬─────┘
                     │ POST /api/agents
                     ▼
               ┌───────────┐
          ┌───▶│   active   │◀───┐
          │    └──┬──┬──┬───┘    │
          │       │  │  │        │
          │  PATCH│  │  │PATCH   │ PATCH (resume)
          │       │  │  │        │
          │       ▼  │  ▼        │
          │  ┌──────┐│┌───────┐  │
          │  │paused│││failed │  │
          │  └──┬───┘│└───────┘  │
          │     │    │           │
          └─────┘    │           │
                     ▼           │
              ┌───────────┐     │
              │ completed  │     │
              └───────────┘     │
                                │
              ┌───────────┐     │
              │ abandoned  │─────┘ (theoretically resumable)
              └───────────┘
```

Valid `status` values (enforced by CHECK constraint):
- `active` — agent is currently working
- `paused` — agent is temporarily paused
- `completed` — agent finished successfully
- `failed` — agent encountered an unrecoverable error
- `abandoned` — session was abandoned

### Progress Bar States

| Condition | Visual |
|-----------|--------|
| `progress_pct = 0` | Empty bar (background only, no fill visible) |
| `0 < progress_pct < 100` | Partial fill with accent color; animated transition on update |
| `progress_pct = 100` | Full bar; may use `.progress-fill.success` (green) variant |

### Conditional Rendering Rules

| Element | Shown when | Hidden when |
|---------|------------|-------------|
| Phase badge | `current_phase` is non-empty | `current_phase` is empty/null |
| ETA label | `eta` is non-empty | `eta` is empty/null |
| Task description | `task_description` is non-empty | `task_description` is empty/null |
| Feature link | `feature_id` is non-empty | `feature_id` is empty/null |
| Recent Updates section | Agent has ≥ 1 status update | Agent has 0 status updates |
| History `<details>` | ≥ 1 non-active session exists | All sessions are active (or no sessions) |
| Empty state | 0 active agents | ≥ 1 active agent |

---

## Accessibility Notes

### Semantic Structure

- **Page heading**: `<h2>` for "🤖 Agent Dashboard" (assumes `<h1>` is the app name in the shell)
- **Stats bar**: Grid of `<div>` elements with `.stat-card` — consider adding `role="status"` or `aria-label` for screen readers
- **Agent cards**: `<div class="card">` — names use `<strong>` for emphasis
- **History section**: Native `<details>` / `<summary>` — fully accessible keyboard toggle out of the box
- **Feature links**: Elements with `.clickable-feature` — should be `<a>` or `<button>` with appropriate role for keyboard navigation

### Color & Contrast

- All badge color pairs (background + text) are designed for WCAG AA contrast in both dark and light themes
- Progress bar uses `var(--accent)` fill against `var(--bg-tertiary)` — sufficient contrast for a decorative/supplementary indicator
- Muted text uses `var(--text-muted)` / `var(--text-secondary)` — intended for supplementary information, not critical content

### Keyboard Navigation

- `<details>` toggle: focusable and operable via Enter/Space natively
- Clickable feature links: should be focusable (`tabindex="0"` or use `<a>`) and operable via Enter key
- Stat cards: hover effects are cosmetic only — no keyboard interaction required

### Motion & Animation

- Progress bar fill: 0.8s cubic-bezier transition — respects `prefers-reduced-motion` (should be considered for future enhancement)
- Badge scale-in: 0.3s animation — cosmetic, not motion-critical
- Card hover: 0.2s translateY — subtle, non-disorienting

### Screen Reader Considerations

- Progress percentage is conveyed as text below the bar ("45% complete · Last active 2 min ago") — not reliant on visual bar alone
- Status is conveyed as text in badges — not color-only
- Emoji in page title (🤖) and status icons (✅ ❌) carry meaning — should have `aria-label` or be supplemented with text (currently text is adjacent)
- Relative timestamps ("2 min ago") are plain text — accessible without additional markup

### Known Gaps

1. Agent cards are `<div>` not `<article>` — could benefit from landmark roles for card-by-card navigation
2. Feature links use `<span class="clickable-feature">` — should ideally be `<a>` or `<button>` for native keyboard support
3. No `aria-live` region for real-time updates — screen reader users won't be notified of WebSocket-driven content changes
4. No `prefers-reduced-motion` media query to disable animations
