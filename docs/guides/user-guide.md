# Lifecycle User Guide

Lifecycle is a human-in-the-loop project management tool for agentic software development. It gives you a CLI to define features, assign work to AI agents through structured iteration cycles, gate quality with human QA, and visualize everything in a live-updating web dashboard.

> **Note:** Features marked with 🚧 are coming soon. Unmarked features are implemented and available today.

---

## Table of Contents

1. [Quick Start](#quick-start)
2. [Installation](#installation)
3. [Core Concepts](#core-concepts)
4. [CLI Reference](#cli-reference)
5. [Web Viewer Guide](#web-viewer-guide)
6. [Agent Integration Guide](#agent-integration-guide)
7. [Configuration Reference](#configuration-reference)
8. [Troubleshooting / FAQ](#troubleshooting--faq)

---

## Quick Start

Get a project running in two minutes:

```bash
# 1. Initialize a new project
lifecycle init my-app

# 2. Add a milestone and a feature
lifecycle milestone add "v1.0"
lifecycle feature add "User authentication" --milestone v1.0 --priority high

# 3. Start a cycle and hand work to an agent
lifecycle cycle start implement feat-1
lifecycle next          # Returns JSON with agentPrompt

# 4. Agent completes work; mark it done
lifecycle done --result "Implemented JWT auth with refresh tokens"

# 5. Review the result
lifecycle qa approve feat-1 --notes "Looks good, tests pass"

# 6. See everything in the web viewer
lifecycle serve
# Open http://localhost:3847
```

---

## Installation

Lifecycle is a single Go binary. Build from source:

```bash
cd /path/to/lifecycle
just build          # or: go build -o lifecycle ./cmd/lifecycle
```

Place the resulting `lifecycle` binary somewhere on your `$PATH`.

Verify the install:

```bash
lifecycle doctor
```

### Requirements

- **Go 1.24+** (build only)
- **SQLite** (bundled via `go-sqlite3`; no external install needed)
- A modern browser for the web viewer

---

## Core Concepts

### Projects and Initialization

A lifecycle project is a directory containing a `.lifecycle.json` config file and a `lifecycle.db` SQLite database. Running `lifecycle init` creates both:

```
my-app/
├── .lifecycle.json    # Project configuration
├── lifecycle.db       # All project data (features, milestones, events, …)
└── …your source code…
```

Lifecycle finds your project by walking up from the current directory until it finds `.lifecycle.json`, so you can run commands from any subdirectory.

### Features and Lifecycle States

A **feature** is the primary unit of work. Every feature moves through a linear pipeline of states:

```
draft → planning → implementing → agent-qa → human-qa → done
                                                 ↑
                                              blocked
```

| State | Meaning |
|-------|---------|
| `draft` | Captured idea, not yet planned |
| `planning` | Being broken down into work items |
| `implementing` | Under active development by an agent |
| `agent-qa` | Agent is self-reviewing the work |
| `human-qa` | Waiting for human approval (the quality gate) |
| `done` | Shipped |
| `blocked` | On hold — a dependency or external issue prevents progress |

The transition from `human-qa` to `done` is the critical human-in-the-loop gate. No feature ships without explicit human approval.

### Milestones and Milestone Gating

A **milestone** groups related features into a deliverable. Milestones track aggregate progress and can gate releases — all features in a milestone must reach `done` before the milestone is complete.

### Feature Dependencies

Features can depend on other features. A feature cannot enter `implementing` until all its dependencies are `done`. Declare dependencies at creation time:

```bash
lifecycle feature add "OAuth provider" --depends-on feat-1
```

### Iteration Cycles

A **cycle** is a structured workflow that moves a feature through its states. Each cycle defines agent roles (designer, developer, QA, judge), iteration rounds, scoring criteria, and convergence rules. See [iteration-cycles.md](iteration-cycles.md) for full details.

### Human QA Workflow

When a feature reaches `human-qa`, it appears in the QA queue. You review the work, then either approve (moves to `done`) or reject (sends it back to `implementing` for another iteration). Every QA decision is recorded with notes.

### SQLite Storage

All data lives in a single `lifecycle.db` file — features, milestones, work items, events, QA results, and heartbeats. This makes projects portable (copy the file), inspectable (open it with any SQLite client), and version-controllable (back it up alongside your code).

---

## CLI Reference

### Project Management

#### `lifecycle init <name>`

Initialize a new lifecycle project in the current directory.

```bash
lifecycle init my-app
# Initializing project: my-app
# Created .lifecycle.json
# Created lifecycle.db
# Project "my-app" is ready.
```

This creates `.lifecycle.json` with default settings and initializes the SQLite database with the full schema.

#### `lifecycle status`

Show project status overview: features by state, milestone progress, and active agents.

```bash
lifecycle status
# Project: my-app
#
# Features:  2 draft · 1 implementing · 1 human-qa · 3 done
# Milestones: v1.0 (4/7 done) · v1.1 (0/3 done)
# Active:    1 agent working on feat-4 (implement cycle, round 2)
```

#### `lifecycle doctor`

Validate your environment and project setup. Checks for a valid config, database integrity, Go version, and common misconfigurations.

```bash
lifecycle doctor
# ✓ .lifecycle.json found
# ✓ lifecycle.db schema is current (v1)
# ✓ Go 1.24.2 detected
# ✓ No orphaned work items
# All checks passed.
```

---

### Feature Lifecycle

#### `lifecycle feature add <name>`

Add a new feature. Starts in `draft` state.

```bash
lifecycle feature add "User authentication" --milestone v1.0 --priority high
# Created feature feat-1: "User authentication" (draft, milestone: v1.0)

lifecycle feature add "OAuth provider" --depends-on feat-1
# Created feature feat-2: "OAuth provider" (draft, depends on: feat-1)
```

| Flag | Description |
|------|-------------|
| `--milestone M` | Assign to a milestone |
| `--priority P` | Set priority: `low`, `medium`, `high`, `critical` |
| `--depends-on F` | Declare a dependency on another feature ID |

#### `lifecycle feature list`

List features with optional filters.

```bash
lifecycle feature list
# ID      Status         Priority  Name
# feat-1  implementing   high      User authentication
# feat-2  draft          medium    OAuth provider
# feat-3  done           high      Database schema

lifecycle feature list --status human-qa --milestone v1.0
# ID      Status    Priority  Name
# feat-4  human-qa  high      Payment processing
```

| Flag | Description |
|------|-------------|
| `--status S` | Filter by lifecycle state |
| `--milestone M` | Filter by milestone |

#### `lifecycle feature show <id>`

Show full details and history for a feature.

```bash
lifecycle feature show feat-1
# Feature: feat-1
# Name:    User authentication
# Status:  implementing
# Priority: high
# Milestone: v1.0
# Dependencies: (none)
# Cycle: implement (round 2 of 5)
#
# History:
#   2025-01-15 09:00  created (draft)
#   2025-01-15 09:05  moved to planning
#   2025-01-15 09:10  moved to implementing
#   2025-01-15 10:30  cycle round 1 complete (score: 6/10)
```

#### `lifecycle feature edit <id>`

Edit a feature's metadata.

```bash
lifecycle feature edit feat-1 --name "JWT Authentication" --priority critical
# Updated feat-1: name → "JWT Authentication", priority → critical
```

| Flag | Description |
|------|-------------|
| `--name N` | Rename the feature |
| `--priority P` | Change priority |
| `--status S` | Manually override status (use with care) |

#### `lifecycle feature remove <id>`

Remove a feature. Prompts for confirmation unless `--yes` is passed.

```bash
lifecycle feature remove feat-2
# Remove feature feat-2 "OAuth provider"? (y/N) y
# Removed feat-2.
```

---

### Agent Work Items

#### `lifecycle next [--cycle C]`

Get the next work item for an agent. Returns JSON to stdout for easy consumption by agent tooling.

```bash
lifecycle next
```

```json
{
  "workItemId": 42,
  "featureId": "feat-1",
  "featureName": "User authentication",
  "workType": "implement",
  "agentPrompt": "Implement JWT-based authentication with login, logout, and token refresh endpoints. Use bcrypt for password hashing. Write tests for all endpoints.",
  "context": {
    "milestone": "v1.0",
    "priority": "high",
    "round": 2,
    "previousResult": "Round 1 implemented login only. Need logout and refresh."
  }
}
```

If no work is available, exits with code 0 and an empty JSON object.

| Flag | Description |
|------|-------------|
| `--cycle C` | Only return items from a specific cycle type |

#### `lifecycle done [--result R]`

Mark the current work item as complete.

```bash
lifecycle done --result "Implemented all three endpoints with full test coverage"
# Marked work item 42 as done.
# Feature feat-1: cycle round 2 complete.
```

#### `lifecycle fail [--reason R]`

Mark the current work item as failed. The cycle will decide whether to retry or escalate.

```bash
lifecycle fail --reason "Cannot connect to external API for OAuth verification"
# Marked work item 42 as failed.
# Feature feat-1: work item failed, cycle will retry.
```

---

### Milestone Management

#### `lifecycle milestone add <name>`

Create a milestone.

```bash
lifecycle milestone add "v1.0" --description "Initial public release"
# Created milestone: v1.0
```

#### `lifecycle milestone list`

List milestones with progress.

```bash
lifecycle milestone list
# Milestone  Status  Progress
# v1.0       active  4/7 features done (57%)
# v1.1       active  0/3 features done (0%)
```

#### `lifecycle milestone show <id>`

Show milestone details including all assigned features.

```bash
lifecycle milestone show v1.0
# Milestone: v1.0
# Description: Initial public release
# Status: active
# Progress: 4/7 done
#
# Features:
#   ✓ feat-1  User authentication       done
#   ✓ feat-3  Database schema            done
#   ✓ feat-5  API endpoints              done
#   ✓ feat-6  Error handling             done
#   ◦ feat-4  Payment processing         human-qa
#   ◦ feat-7  Email notifications        implementing
#   ◦ feat-2  OAuth provider             draft
```

---

### Iteration Cycles

#### `lifecycle cycle list`

List available iteration cycle types.

```bash
lifecycle cycle list
# Cycle         Description
# implement     Full implementation cycle (plan → code → test → review)
# ui-refine     UI polish with designer and reviewer agents
# bug-triage    Bug investigation and fix cycle
# roadmap-plan  Collaborative roadmap planning cycle
```

#### `lifecycle cycle start <cycle-name> <feature-id>`

Start an iteration cycle for a feature.

```bash
lifecycle cycle start implement feat-1
# Started "implement" cycle for feat-1 (User authentication)
# Round 1 of 5 · work item created · run "lifecycle next" to begin
```

#### `lifecycle cycle status`

Show active cycle progress.

```bash
lifecycle cycle status
# Feature  Cycle      Round  Score  Agent Role
# feat-1   implement  2/5    6/10   developer
# feat-7   ui-refine  1/3    —      designer
```

#### `lifecycle cycle history <feature-id>`

Show cycle history for a feature — every round, score, and result.

```bash
lifecycle cycle history feat-1
# Cycle: implement
# Round 1  score: 6/10  "Implemented login only"
# Round 2  score: 8/10  "Added logout and refresh, tests passing"
# Round 3  (active)
```

#### `lifecycle cycle score <score>`

Submit a judge score for the current cycle step. Scores are numeric (e.g. 0–10) and recorded against the active cycle step for the feature.

```bash
lifecycle cycle score 8.5 --feature feat-1 --notes "Good implementation but accessibility needs work"
# Scored feat-1 cycle step: 8.5
```

| Flag | Description |
|------|-------------|
| `--feature F` | Feature ID to score (required if ambiguous) |
| `--notes N` | Freeform notes explaining the score |

---

### Roadmap

#### `lifecycle roadmap show`

Display the current roadmap, grouped by category and sorted by priority.

```bash
lifecycle roadmap show
# Roadmap: my-app
#
# [Core]
#   1. ★★★ User authentication          in-progress
#   2. ★★★ Payment processing            accepted
#   3. ★★  OAuth provider                proposed
#
# [Infrastructure]
#   4. ★★★ CI/CD pipeline                accepted
#   5. ★★  Monitoring & alerting         proposed
```

#### `lifecycle roadmap add <title>`

Add an item to the roadmap.

```bash
lifecycle roadmap add "WebSocket notifications" --priority high --category Core
# Added roadmap item: "WebSocket notifications" (Core, high priority)
```

#### `lifecycle roadmap prioritize`

Interactive prioritization session — presents items pairwise and asks you to choose.

#### `lifecycle roadmap export`

Export the roadmap as Markdown or JSON.

```bash
lifecycle roadmap export --format md > ROADMAP.md
lifecycle roadmap export --format json | jq .
```

---

### QA

#### `lifecycle qa pending`

Show features waiting for human QA review.

```bash
lifecycle qa pending
# ID      Priority  Name                    Waiting Since
# feat-4  high      Payment processing      2 hours ago
# feat-8  medium    Search functionality     15 minutes ago
```

#### `lifecycle qa approve <feature-id>`

Approve a feature — moves it from `human-qa` to `done`.

```bash
lifecycle qa approve feat-4 --notes "All tests pass, UI looks correct"
# Approved feat-4: "Payment processing" → done
```

#### `lifecycle qa reject <feature-id>`

Reject a feature — sends it back to `implementing` for another cycle iteration.

```bash
lifecycle qa reject feat-8 --notes "Search results not sorted by relevance"
# Rejected feat-8: "Search functionality" → implementing (back to cycle)
```

---

### History & Search

#### `lifecycle history`

Browse the event history log. Every state change, QA decision, and cycle event is recorded.

```bash
lifecycle history --feature feat-1 --since 2025-01-15
# 2025-01-15 09:00  feat-1  created
# 2025-01-15 09:05  feat-1  status_change  draft → planning
# 2025-01-15 09:10  feat-1  status_change  planning → implementing
# 2025-01-15 10:30  feat-1  cycle_round    implement round 1 (score: 6/10)
# 2025-01-15 11:45  feat-1  cycle_round    implement round 2 (score: 8/10)
```

| Flag | Description |
|------|-------------|
| `--feature F` | Filter by feature ID |
| `--since S` | Show events after this date/time |
| `--type T` | Filter by event type (`status_change`, `cycle_round`, `qa_decision`, …) |

#### `lifecycle search <query>`

Full-text search across all project data — feature names, descriptions, QA notes, agent results, and event data.

```bash
lifecycle search "JWT"
# feat-1   "User authentication"    agent_prompt: "…JWT-based authentication…"
# feat-1   cycle result (round 2):  "…added JWT refresh token rotation…"
```

---

## Web Viewer Guide

Start the web viewer:

```bash
lifecycle serve
# Lifecycle web viewer running at http://localhost:3847
# Watching lifecycle.db for changes…
```

| Flag | Description |
|------|-------------|
| `--port P` | Override the default port (3847) |

The web viewer is **read-only by design** — it renders the data that the CLI manages. All state changes happen through CLI commands (with the exception of QA approve/reject, which can be done from the web UI). The viewer updates in real time via WebSocket, so you can keep it open while agents work.

### Dashboard

The landing page shows project health at a glance. A kanban board groups features by lifecycle state — click any column header to filter the features list. Below the kanban you'll find milestone progress bars, a recent activity feed, roadmap highlights, a priority distribution chart, and a preview of active cycles.

### Feature Board

A tabular list of all features with status badges, priority indicators, and milestone assignments. Click any row to expand an inline detail panel showing the feature's description, milestone, priority, timestamps, and full history.

### Roadmap View

A presentation-quality roadmap grouped by priority (Critical → High → Medium → Low). Each item shows its status, category, and effort sizing badge. Click an item to expand its full description.

### Cycle Progress

Displays both active and completed iteration cycles. Each cycle shows a step pipeline visualizing progress through the cycle's stages. Per-step scores are displayed alongside sparkline charts, with an average score summary. A cycle type reference grid at the bottom lists all available cycle definitions.

### Event History

A scrollable timeline of every project event, grouped by date. Category filter buttons (All / Cycle / Work / Feature / Roadmap / Milestone) and a feature dropdown let you narrow the view. Pagination uses a "load more" button to fetch older events.

### QA Review

A dedicated interface for reviewing features that have reached the `human-qa` stage. Features appear automatically when they enter `human-qa`. Review the feature context and cycle results, then approve or reject with notes using the built-in textarea and action buttons.

### Live Updates / WebSocket

The web viewer maintains a WebSocket connection to the server. When any data changes in the database, the server pushes an update and all open pages refresh automatically. If the connection drops, the viewer auto-reconnects with a 3-second backoff. No manual refresh needed — keep the dashboard open and watch agents work in real time.

---

## Agent Integration Guide

Lifecycle is designed so that AI agents interact with your project through the CLI. The typical agent loop:

```
┌─────────────────────────────────┐
│  lifecycle next                 │  ← Agent asks for work
│  → receives JSON agentPrompt   │
├─────────────────────────────────┤
│  Agent performs the work        │  ← Code, test, design, review
├─────────────────────────────────┤
│  lifecycle done --result "…"    │  ← Agent reports success
│  lifecycle fail --reason "…"    │  ← …or failure
└─────────────────────────────────┘
        ↓ (cycle continues or ends)
```

### Setting Up an Agent

1. **Point the agent at your project directory** — the agent must run lifecycle commands from within the project tree (any subdirectory works).

2. **Teach the agent the protocol:**
   - Call `lifecycle next` to get a work item. Parse the JSON response.
   - Read the `agentPrompt` field for instructions.
   - Do the work (write code, run tests, etc.).
   - Call `lifecycle done --result "description of what was done"` on success.
   - Call `lifecycle fail --reason "what went wrong"` on failure.

3. **The cycle handles the rest.** The iteration cycle manages rounds, scoring, and state transitions. The agent doesn't need to know about lifecycle states — it just picks up work and reports results.

### Example: Agent Script

```bash
#!/usr/bin/env bash
# Simple agent loop
while true; do
  WORK=$(lifecycle next)
  if [ "$WORK" = "{}" ]; then
    echo "No work available. Sleeping…"
    sleep 30
    continue
  fi

  PROMPT=$(echo "$WORK" | jq -r '.agentPrompt')
  # Send prompt to your AI agent, get result…

  lifecycle done --result "$AGENT_RESULT"
done
```

### Heartbeats 🚧 Coming Soon

Long-running agents can send heartbeats to signal they're still alive. The web viewer shows agent activity in real time.

---

## Configuration Reference

Lifecycle stores project configuration in `.lifecycle.json` at the project root.

```json
{
  "project_dir": ".",
  "db_path": "lifecycle.db",
  "server_port": 3847
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `project_dir` | `.` | Root directory of the project |
| `db_path` | `lifecycle.db` | Path to the SQLite database file |
| `server_port` | `3847` | Port for the web viewer |

### Project Discovery

Lifecycle walks up from the current working directory to find `.lifecycle.json`. This means you can run commands from any subdirectory:

```bash
cd my-app/src/auth
lifecycle status   # finds ../../.lifecycle.json
```

### Database

All data is stored in a single SQLite file (`lifecycle.db` by default). Key tables:

| Table | Purpose |
|-------|---------|
| `projects` | Project metadata |
| `milestones` | Milestone definitions and status |
| `features` | Features with lifecycle state, priority, and assignment |
| `feature_deps` | Dependency graph between features |
| `work_items` | Individual work items with agent prompts and results |
| `events` | Full audit log of every state change |
| `roadmap_items` | Roadmap entries with priority and category |
| `qa_results` | QA approval/rejection records with notes |
| `heartbeats` | Agent activity heartbeats |

You can inspect the database directly with any SQLite client:

```bash
sqlite3 lifecycle.db "SELECT id, name, status FROM features"
```

---

## Troubleshooting / FAQ

### "No .lifecycle.json found"

You're not inside a lifecycle project. Run `lifecycle init <name>` to create one, or `cd` into an existing project directory.

### "Database schema version mismatch"

Your `lifecycle.db` was created with a different version of lifecycle. Run `lifecycle doctor` to diagnose. Future versions will include automatic migrations.

### Can I use lifecycle with multiple agents?

Yes. Multiple agents can call `lifecycle next` concurrently. Each call returns a different work item — work items are assigned atomically to prevent double-assignment.

### Can I edit the database directly?

You can, but it's not recommended for state changes. Use the CLI to ensure events are logged, dependencies are checked, and cycles advance correctly. Direct reads (for debugging or reporting) are fine and encouraged.

### How do I back up my project?

Copy `.lifecycle.json` and `lifecycle.db`. That's everything. Both are regular files — commit them, sync them, or back them up however you like.

### Where are logs stored?

Events are stored in the `events` table inside `lifecycle.db`. Use `lifecycle history` or query the table directly. There are no external log files.

### Can the web viewer modify data?

No. The web viewer is strictly read-only. All state changes go through the CLI. This is a deliberate design choice — the CLI is the single source of truth.
