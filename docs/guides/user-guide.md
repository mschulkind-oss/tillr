# Tillr User Guide

Tillr is a human-in-the-loop project management tool for agentic software development. It gives you a CLI to define features, assign work to AI agents through structured iteration cycles, gate quality with human QA, and visualize everything in a live-updating web dashboard.

---

## Table of Contents

1. [Quick Start](#quick-start)
2. [Installation](#installation)
3. [Onboarding an Existing Project](#onboarding-an-existing-project)
4. [Core Concepts](#core-concepts)
5. [CLI Reference](#cli-reference)
6. [Web Viewer Guide](#web-viewer-guide)
7. [Agent Integration Guide](#agent-integration-guide)
8. [Configuration Reference](#configuration-reference)
9. [Troubleshooting / FAQ](#troubleshooting--faq)

---

## Quick Start

Get a project running in two minutes:

```bash
# 1. Initialize a new project
tillr init my-app

# 2. Add a milestone and a feature
tillr milestone add "v1.0"
tillr feature add "User authentication" --milestone v1.0 --priority high

# 3. Start a cycle and hand work to an agent
tillr cycle start implement feat-1
tillr next          # Returns JSON with agentPrompt

# 4. Agent completes work; mark it done
tillr done --result "Implemented JWT auth with refresh tokens"

# 5. Review the result
tillr qa approve feat-1 --notes "Looks good, tests pass"

# 6. See everything in the web viewer
tillr serve
# Open http://localhost:3847
```

---

## Installation

Tillr is a single Go binary. Build from source:

```bash
cd /path/to/tillr
just build          # or: go build -o tillr ./cmd/tillr
```

Place the resulting `tillr` binary somewhere on your `$PATH`.

Verify the install:

```bash
tillr doctor
```

### Requirements

- **Go 1.24+** (build only)
- **SQLite** (bundled via `modernc.org/sqlite`; pure Go, no CGO or external install needed)
- A modern browser for the web viewer

---

## Onboarding an Existing Project

The Quick Start above covers brand-new projects. But most of the time you already have a codebase with history, milestones, and half-finished work. This section walks you through bringing an existing project under tillr management.

### When to Use Onboarding

Use this workflow when:

- You have a working codebase and want tillr to track its features going forward.
- You want to retroactively record completed work so the dashboard reflects reality.
- You're adopting tillr mid-project and need to capture in-progress and planned work.

You don't need to model everything — just the user-facing capabilities you want to track, iterate on, and QA.

### The Onboard Command

```bash
tillr onboard --name my-project --scan
# Scanning project...
# Detected: Go (go.mod), Git history (847 commits), CI config (.github/workflows/ci.yml), README.md
# Initialized project "my-project"
# Suggested milestones and features written to .tillr-onboard.json — review and apply.
```

The `--scan` flag inspects your repository for:

- **Languages and frameworks** — `go.mod`, `package.json`, `pyproject.toml`, etc.
- **Git history** — recent activity, contributors, branch structure.
- **CI configuration** — GitHub Actions, GitLab CI, or similar.
- **README and docs** — project description and existing documentation.

The scan produces suggestions, not changes. You review them and decide what to track.

For non-interactive use (CI or agent-driven onboarding), pass `--yes` to automatically create suggested milestones and features:

```bash
tillr onboard --name my-project --yes
```

You can also run the steps below manually for full control.

### Step-by-Step Walkthrough

#### 1. Initialize the Project

Start by creating the tillr database in your project root:

```bash
cd ~/projects/my-api
tillr init my-api
# Initializing project: my-api
# Created .tillr.json
# Created tillr.db
# Project "my-api" is ready.
```

#### 2. Create Milestones for Your Project Phases

Think about how your project is organized — shipped releases, the current sprint, and what's next. Create a milestone for each:

```bash
tillr milestone add "v1.0 — Launch"
tillr milestone add "v1.1 — Performance"
tillr milestone add "v2.0 — Multi-tenant"
```

#### 3. Record Completed Features

For work that's already shipped, add features with `--status done` so the dashboard reflects your actual progress:

```bash
tillr feature add "REST API endpoints" --milestone "v1.0 — Launch" --priority high
tillr feature edit feat-1 --status done

tillr feature add "Database migrations" --milestone "v1.0 — Launch" --priority high
tillr feature edit feat-2 --status done

tillr feature add "CI/CD pipeline" --milestone "v1.0 — Launch" --priority medium
tillr feature edit feat-3 --status done
```

This gives you an accurate history and makes milestone progress bars meaningful from day one.

#### 4. Add In-Progress Features

Capture what's actively being worked on:

```bash
tillr feature add "Rate limiting" --milestone "v1.1 — Performance" --priority high
tillr feature edit feat-4 --status implementing

tillr feature add "Response caching" --milestone "v1.1 — Performance" --priority medium
tillr feature edit feat-5 --status implementing
```

#### 5. Add Planned Features as Drafts

Future work stays in `draft` until you're ready to plan it:

```bash
tillr feature add "Tenant isolation" --milestone "v2.0 — Multi-tenant" --priority high
tillr feature add "Per-tenant billing" --milestone "v2.0 — Multi-tenant" --priority medium
tillr feature add "Admin dashboard" --milestone "v2.0 — Multi-tenant" --priority low
```

#### 6. Set Up Roadmap Items

The roadmap gives a higher-level, categorized view of where the project is headed. Add items for themes that span multiple features:

```bash
tillr roadmap add "API performance overhaul" --priority high --category Performance
tillr roadmap add "Multi-tenancy support" --priority high --category Architecture
tillr roadmap add "Developer portal" --priority medium --category DX
```

### Tips for Choosing What to Track

- **Track user-facing capabilities**, not implementation tasks. "User authentication" is a feature; "refactor the auth middleware" is not.
- **Don't over-model.** If a piece of work doesn't need QA gating or agent iteration, it probably doesn't need a feature entry.
- **Use milestones for sequencing**, not for every sprint. Milestones work best as meaningful delivery checkpoints (releases, betas, demos).
- **Start small.** You can always add more features later. Begin with what you're actively building and expand as you go.

### Verifying the Onboard

Once you've added your milestones and features, verify that everything looks right:

```bash
# Check project health
tillr doctor
# ✓ .tillr.json found
# ✓ tillr.db schema is current (v1)
# ✓ No orphaned work items
# All checks passed.

# Review the overall picture
tillr status
# Project: my-api
#
# Features:  3 draft · 2 implementing · 3 done
# Milestones: v1.0 — Launch (3/3 done) · v1.1 — Performance (0/2 done) · v2.0 — Multi-tenant (0/3 done)

# See it all in the web viewer
tillr serve
# Listening on http://localhost:3847
```

Open the web viewer and confirm the dashboard, feature board, and roadmap all reflect your project's current state. From here you can start iteration cycles on your in-progress features and hand work off to agents.

---

## Core Concepts

### Projects and Initialization

A tillr project is a directory containing a `.tillr.json` config file and a `tillr.db` SQLite database. Running `tillr init` creates both:

```
my-app/
├── .tillr.json    # Project configuration
├── tillr.db       # All project data (features, milestones, events, …)
└── …your source code…
```

Tillr finds your project by walking up from the current directory until it finds `.tillr.json`, so you can run commands from any subdirectory.

### Features and Tillr States

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
tillr feature add "OAuth provider" --depends-on feat-1
```

### Iteration Cycles

A **cycle** is a structured workflow that moves a feature through its states. Each cycle defines agent roles (designer, developer, QA, judge), iteration rounds, scoring criteria, and convergence rules. See [iteration-cycles.md](iteration-cycles.md) for full details.

### Human QA Workflow

When a feature reaches `human-qa`, it appears in the QA queue. You review the work, then either approve (moves to `done`) or reject (sends it back to `implementing` for another iteration). Every QA decision is recorded with notes.

### SQLite Storage

All data lives in a single `tillr.db` file — features, milestones, work items, events, QA results, and heartbeats. This makes projects portable (copy the file), inspectable (open it with any SQLite client), and version-controllable (back it up alongside your code).

---

## CLI Reference

### Project Management

#### `tillr init <name>`

Initialize a new tillr project in the current directory.

```bash
tillr init my-app
# Initializing project: my-app
# Created .tillr.json
# Created tillr.db
# Project "my-app" is ready.
```

This creates `.tillr.json` with default settings and initializes the SQLite database with the full schema.

#### `tillr status`

Show project status overview: features by state, milestone progress, and active agents.

```bash
tillr status
# Project: my-app
#
# Features:  2 draft · 1 implementing · 1 human-qa · 3 done
# Milestones: v1.0 (4/7 done) · v1.1 (0/3 done)
# Active:    1 agent working on feat-4 (implement cycle, round 2)
```

#### `tillr doctor`

Validate your environment and project setup. Checks for a valid config, database integrity, Go version, and common misconfigurations.

```bash
tillr doctor
# ✓ .tillr.json found
# ✓ tillr.db schema is current (v1)
# ✓ Go 1.24.2 detected
# ✓ No orphaned work items
# All checks passed.
```

---

### Feature Tillr

#### `tillr feature add <name>`

Add a new feature. Starts in `draft` state.

```bash
tillr feature add "User authentication" --milestone v1.0 --priority high
# Created feature feat-1: "User authentication" (draft, milestone: v1.0)

tillr feature add "OAuth provider" --depends-on feat-1
# Created feature feat-2: "OAuth provider" (draft, depends on: feat-1)
```

| Flag | Description |
|------|-------------|
| `--milestone M` | Assign to a milestone |
| `--priority P` | Set priority: `low`, `medium`, `high`, `critical` |
| `--depends-on F` | Declare a dependency on another feature ID |

#### `tillr feature list`

List features with optional filters.

```bash
tillr feature list
# ID      Status         Priority  Name
# feat-1  implementing   high      User authentication
# feat-2  draft          medium    OAuth provider
# feat-3  done           high      Database schema

tillr feature list --status human-qa --milestone v1.0
# ID      Status    Priority  Name
# feat-4  human-qa  high      Payment processing
```

| Flag | Description |
|------|-------------|
| `--status S` | Filter by tillr state |
| `--milestone M` | Filter by milestone |

#### `tillr feature show <id>`

Show full details and history for a feature.

```bash
tillr feature show feat-1
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

#### `tillr feature edit <id>`

Edit a feature's metadata.

```bash
tillr feature edit feat-1 --name "JWT Authentication" --priority critical
# Updated feat-1: name → "JWT Authentication", priority → critical
```

| Flag | Description |
|------|-------------|
| `--name N` | Rename the feature |
| `--priority P` | Change priority |
| `--status S` | Manually override status (use with care) |

#### `tillr feature remove <id>`

Remove a feature. Prompts for confirmation unless `--yes` is passed.

```bash
tillr feature remove feat-2
# Remove feature feat-2 "OAuth provider"? (y/N) y
# Removed feat-2.
```

---

### Agent Work Items

#### `tillr next [--cycle C]`

Get the next work item for an agent. Returns JSON to stdout for easy consumption by agent tooling.

```bash
tillr next
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

#### `tillr done [--result R]`

Mark the current work item as complete.

```bash
tillr done --result "Implemented all three endpoints with full test coverage"
# Marked work item 42 as done.
# Feature feat-1: cycle round 2 complete.
```

#### `tillr fail [--reason R]`

Mark the current work item as failed. The cycle will decide whether to retry or escalate.

```bash
tillr fail --reason "Cannot connect to external API for OAuth verification"
# Marked work item 42 as failed.
# Feature feat-1: work item failed, cycle will retry.
```

---

### Milestone Management

#### `tillr milestone add <name>`

Create a milestone.

```bash
tillr milestone add "v1.0" --description "Initial public release"
# Created milestone: v1.0
```

#### `tillr milestone list`

List milestones with progress.

```bash
tillr milestone list
# Milestone  Status  Progress
# v1.0       active  4/7 features done (57%)
# v1.1       active  0/3 features done (0%)
```

#### `tillr milestone show <id>`

Show milestone details including all assigned features.

```bash
tillr milestone show v1.0
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

#### `tillr cycle list`

List available iteration cycle types.

```bash
tillr cycle list
# Cycle         Description
# implement     Full implementation cycle (plan → code → test → review)
# ui-refine     UI polish with designer and reviewer agents
# bug-triage    Bug investigation and fix cycle
# roadmap-plan  Collaborative roadmap planning cycle
```

#### `tillr cycle start <cycle-name> <feature-id>`

Start an iteration cycle for a feature.

```bash
tillr cycle start implement feat-1
# Started "implement" cycle for feat-1 (User authentication)
# Round 1 of 5 · work item created · run "tillr next" to begin
```

#### `tillr cycle status`

Show active cycle progress.

```bash
tillr cycle status
# Feature  Cycle      Round  Score  Agent Role
# feat-1   implement  2/5    6/10   developer
# feat-7   ui-refine  1/3    —      designer
```

#### `tillr cycle history <feature-id>`

Show cycle history for a feature — every round, score, and result.

```bash
tillr cycle history feat-1
# Cycle: implement
# Round 1  score: 6/10  "Implemented login only"
# Round 2  score: 8/10  "Added logout and refresh, tests passing"
# Round 3  (active)
```

#### `tillr cycle score <score>`

Submit a judge score for the current cycle step. Scores are numeric (e.g. 0–10) and recorded against the active cycle step for the feature.

```bash
tillr cycle score 8.5 --feature feat-1 --notes "Good implementation but accessibility needs work"
# Scored feat-1 cycle step: 8.5
```

| Flag | Description |
|------|-------------|
| `--feature F` | Feature ID to score (required if ambiguous) |
| `--notes N` | Freeform notes explaining the score |

---

### Roadmap

#### `tillr roadmap show`

Display the current roadmap, grouped by category and sorted by priority.

```bash
tillr roadmap show
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

#### `tillr roadmap add <title>`

Add an item to the roadmap.

```bash
tillr roadmap add "WebSocket notifications" --priority high --category Core
# Added roadmap item: "WebSocket notifications" (Core, high priority)
```

#### `tillr roadmap prioritize`

Interactive prioritization session — presents items pairwise and asks you to choose.

#### `tillr roadmap export`

Export the roadmap as Markdown or JSON.

```bash
tillr roadmap export --format md > ROADMAP.md
tillr roadmap export --format json | jq .
```

---

### QA

#### `tillr qa pending`

Show features waiting for human QA review.

```bash
tillr qa pending
# ID      Priority  Name                    Waiting Since
# feat-4  high      Payment processing      2 hours ago
# feat-8  medium    Search functionality     15 minutes ago
```

#### `tillr qa approve <feature-id>`

Approve a feature — moves it from `human-qa` to `done`.

```bash
tillr qa approve feat-4 --notes "All tests pass, UI looks correct"
# Approved feat-4: "Payment processing" → done
```

#### `tillr qa reject <feature-id>`

Reject a feature — sends it back to `implementing` for another cycle iteration.

```bash
tillr qa reject feat-8 --notes "Search results not sorted by relevance"
# Rejected feat-8: "Search functionality" → implementing (back to cycle)
```

---

### History & Search

#### `tillr history`

Browse the event history log. Every state change, QA decision, and cycle event is recorded.

```bash
tillr history --feature feat-1 --since 2025-01-15
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

#### `tillr search <query>`

Full-text search across all project data — feature names, descriptions, QA notes, agent results, and event data.

```bash
tillr search "JWT"
# feat-1   "User authentication"    agent_prompt: "…JWT-based authentication…"
# feat-1   cycle result (round 2):  "…added JWT refresh token rotation…"
```

---

### Architecture Decision Records (ADRs)

#### `tillr decision add <title>`

Record an architecture decision.

```bash
tillr decision add "Use PostgreSQL for primary storage" \
  --context "Need a reliable RDBMS for transactional data" \
  --decision "PostgreSQL 16 with connection pooling" \
  --consequences "Team needs PostgreSQL expertise; adds ops complexity" \
  --feature feat-1
# Created decision: "Use PostgreSQL for primary storage" (proposed)
```

| Flag | Description |
|------|-------------|
| `--context C` | Why is this decision needed? |
| `--decision D` | What was decided? |
| `--consequences C` | What are the consequences? |
| `--feature F` | Link to a feature ID |
| `--status S` | Status: `proposed`, `accepted`, `rejected`, `superseded`, `deprecated` (default: `proposed`) |

#### `tillr decision list`

List all architecture decisions.

```bash
tillr decision list
# ID  Status    Title
# 1   accepted  Use PostgreSQL for primary storage
# 2   proposed  JWT vs session-based auth
```

#### `tillr decision show <id>`

Show full decision details, including context, decision text, consequences, and linked feature.

#### `tillr decision edit <id>`

Edit a decision's properties (status, context, decision text, consequences).

---

### Configuration Management

#### `tillr config init`

Create a `.tillr.yaml` configuration file with default values.

```bash
tillr config init
# Created .tillr.yaml with defaults
```

#### `tillr config show`

Show the current configuration (merged defaults + file overrides).

```bash
tillr config show
# default_milestone: ""
# default_priority: 5
# server_port: 3847
# theme: system
# agent_timeout_minutes: 30
# db_path: tillr.db
```

#### `tillr config set <key> <value>`

Set a configuration value in `.tillr.yaml`.

```bash
tillr config set server_port 8080
tillr config set default_priority 7
tillr config set theme dark
```

---

### Export

Export project data in multiple formats.

#### `tillr export features`

```bash
tillr export features --format md > FEATURES.md
tillr export features --format csv > features.csv
tillr export features --format json | jq .
```

#### `tillr export roadmap`

```bash
tillr export roadmap --format md > ROADMAP.md
```

#### `tillr export decisions`

```bash
tillr export decisions --format md > DECISIONS.md
```

#### `tillr export all`

Export all project data (features, roadmap, and decisions) at once.

```bash
tillr export all --format json > project-export.json
```

| Flag | Description |
|------|-------------|
| `--format F` | Output format: `json` (default), `md`, or `csv` |

---

### Queue Management

#### `tillr queue list`

List pending work items in priority order.

```bash
tillr queue list
# ID   Feature            Type        Priority  Claimed
# 12   feat-4             implement   high      agent-1
# 15   feat-7             research    medium    (unclaimed)
```

#### `tillr queue stats`

Show queue statistics — pending, claimed, and completed counts.

```bash
tillr queue stats
# Pending:   3
# Claimed:   1
# Completed: 12
```

#### `tillr queue reassign <work-item-id>`

Release a claimed work item back to the pending queue so another agent can pick it up.

```bash
tillr queue reassign 12
# Released work item 12 back to pending queue.
```

#### `tillr queue reclaim`

Reclaim stale work items that have had no heartbeat for 30+ minutes.

```bash
tillr queue reclaim
# Reclaimed 2 stale work item(s).
```

---

### Git / VCS Integration

Tillr auto-detects whether your project uses `git` or `jj` (Jujutsu).

#### `tillr git log`

Show recent commits.

```bash
tillr git log -n 10
```

| Flag | Description |
|------|-------------|
| `-n N` | Number of commits to show (default: 20) |

#### `tillr git branches`

Show branches and their linked features.

```bash
tillr git branches
```

#### `tillr git link <feature-id> <commit-hash>`

Link a commit to a feature for traceability.

```bash
tillr git link feat-1 abc123f
# Linked commit abc123f to feat-1.
```

---

### MCP Server

Start a Model Context Protocol (MCP) server for direct agent integration over stdio.

```bash
tillr mcp
```

The MCP server exposes tillr tools (`tillr_next`, `tillr_done`, `tillr_fail`, `tillr_status`, `tillr_features`, `tillr_feedback`) via JSON-RPC 2.0 over stdin/stdout. This allows AI agents to interact with tillr directly without subprocess CLI calls.

---

### Batch Operations

#### `tillr feature batch`

Update multiple features at once.

```bash
# Set status for multiple features
tillr feature batch --ids feat-1,feat-2,feat-3 --status implementing

# Set milestone for multiple features
tillr feature batch --ids feat-1,feat-2 --milestone v1.0

# Set priority for multiple features
tillr feature batch --ids feat-1,feat-2,feat-3 --priority 8
```

| Flag | Description |
|------|-------------|
| `--ids IDs` | Comma-separated feature IDs |
| `--status S` | Set status for all listed features |
| `--milestone M` | Set milestone for all listed features |
| `--priority P` | Set priority for all listed features |

---

## Web Viewer Guide

Start the web viewer:

```bash
tillr serve
# Tillr web viewer running at http://localhost:3847
# Watching tillr.db for changes…
```

| Flag | Description |
|------|-------------|
| `--port P` | Override the default port (3847) |

The web viewer is **read-only by design** — it renders the data that the CLI manages. All state changes happen through CLI commands (with the exception of QA approve/reject, which can be done from the web UI). The viewer updates in real time via WebSocket, so you can keep it open while agents work.

### Dashboard

The landing page shows project health at a glance. A kanban board groups features by tillr state — click any column header to filter the features list. Below the kanban you'll find milestone progress bars, a recent activity feed, roadmap highlights, a priority distribution chart, and a preview of active cycles.

### Feature Board

A tabular list of all features with status badges, priority indicators, and milestone assignments. Click any row to expand an inline detail panel showing the feature's description, milestone, priority, timestamps, and full history. Use the checkboxes to select multiple features, then use the floating action bar to batch-update status, milestone, or priority.

### Roadmap View

A presentation-quality roadmap grouped by priority (Critical → High → Medium → Low). Each item shows its status, category, and effort sizing badge. Click an item to expand its full description.

### Timeline View

A Gantt-style timeline page showing feature progress over time. Access it at `#timeline`. Features are displayed as horizontal bars spanning their active period, grouped by milestone. Useful for spotting bottlenecks and understanding parallel work.

### Cycle Progress

Displays both active and completed iteration cycles. Each cycle shows a step pipeline visualizing progress through the cycle's stages. Per-step scores are displayed alongside sparkline charts, with an average score summary. A cycle type reference grid at the bottom lists all available cycle definitions.

### Event History

A scrollable timeline of every project event, grouped by date. Category filter buttons (All / Cycle / Work / Feature / Roadmap / Milestone) and a feature dropdown let you narrow the view. Pagination uses a "load more" button to fetch older events.

### QA Review

A dedicated interface for reviewing features that have reached the `human-qa` stage. Features appear automatically when they enter `human-qa`, forming a review queue. Review the feature context and cycle results, then approve or reject with notes using the built-in textarea and action buttons.

### Decisions (ADRs)

Browse Architecture Decision Records at `#adrs`. Decisions are listed with their status (proposed, accepted, rejected, superseded, deprecated), linked features, and full context. Click any decision to view the complete record including context, decision text, and consequences.

### Keyboard Shortcuts

Press **`?`** on any page to see all available keyboard shortcuts. Shortcuts include navigation between pages, toggling dark mode, and jumping to specific features.

### Quick Feedback Button

A small **⊕** button floats in the bottom-right corner of every page. Click it to open a minimal text input — just type and press Enter to submit feedback, bug reports, or feature ideas. No forms, no dropdowns. Submissions appear in the idea queue (`tillr idea list`).

### Live Updates / WebSocket

The web viewer maintains a WebSocket connection to the server. When any data changes in the database, the server pushes an update and all open pages refresh automatically. If the connection drops, the viewer auto-reconnects with a 3-second backoff. No manual refresh needed — keep the dashboard open and watch agents work in real time.

---

## Agent Integration Guide

Tillr is designed so that AI agents interact with your project through the CLI. The typical agent loop:

```
┌─────────────────────────────────┐
│  tillr next                 │  ← Agent asks for work
│  → receives JSON agentPrompt   │
├─────────────────────────────────┤
│  Agent performs the work        │  ← Code, test, design, review
├─────────────────────────────────┤
│  tillr done --result "…"    │  ← Agent reports success
│  tillr fail --reason "…"    │  ← …or failure
└─────────────────────────────────┘
        ↓ (cycle continues or ends)
```

### Setting Up an Agent

1. **Point the agent at your project directory** — the agent must run tillr commands from within the project tree (any subdirectory works).

2. **Teach the agent the protocol:**
   - Call `tillr next` to get a work item. Parse the JSON response.
   - Read the `agentPrompt` field for instructions.
   - Do the work (write code, run tests, etc.).
   - Call `tillr done --result "description of what was done"` on success.
   - Call `tillr fail --reason "what went wrong"` on failure.

3. **The cycle handles the rest.** The iteration cycle manages rounds, scoring, and state transitions. The agent doesn't need to know about tillr states — it just picks up work and reports results.

### Example: Agent Script

```bash
#!/usr/bin/env bash
# Simple agent loop
while true; do
  WORK=$(tillr next)
  if [ "$WORK" = "{}" ]; then
    echo "No work available. Sleeping…"
    sleep 30
    continue
  fi

  PROMPT=$(echo "$WORK" | jq -r '.agentPrompt')
  # Send prompt to your AI agent, get result…

  tillr done --result "$AGENT_RESULT"
done
```

### Heartbeats

Long-running agents should send periodic heartbeats to signal they're still alive:

```bash
tillr heartbeat --message "Running integration tests"
# Heartbeat recorded.
```

The web viewer shows agent activity and heartbeat status in real time. Work items with no heartbeat for 30+ minutes are considered stale and can be reclaimed:

```bash
tillr queue reclaim
# Reclaimed 1 stale work item(s).
```

---

## Configuration Reference

Tillr uses two configuration files:

### Project File: `.tillr.json`

Created by `tillr init`, this file identifies the project root and stores core settings:

```json
{
  "project_dir": ".",
  "db_path": "tillr.db",
  "server_port": 3847
}
```

Tillr finds your project by walking up from the current directory until it finds `.tillr.json`, so you can run commands from any subdirectory.

### Defaults File: `.tillr.yaml`

Created by `tillr config init`, this optional file stores configuration defaults. It is merged with built-in defaults at runtime. Create it with:

```bash
tillr config init
```

Available fields:

| Field | Default | Description |
|-------|---------|-------------|
| `default_milestone` | `""` | Default milestone for new features |
| `default_priority` | `5` | Default priority for new features (integer) |
| `server_port` | `3847` | Port for the web viewer |
| `theme` | `system` | Web viewer theme: `light`, `dark`, or `system` |
| `agent_timeout_minutes` | `30` | Minutes before an agent is considered stale |
| `db_path` | `tillr.db` | Path to the SQLite database file |

View current configuration (merged defaults + file):

```bash
tillr config show
```

Set individual values:

```bash
tillr config set server_port 8080
tillr config set theme dark
```

### Project Discovery

Tillr walks up from the current working directory to find `.tillr.json`. This means you can run commands from any subdirectory:

```bash
cd my-app/src/auth
tillr status   # finds ../../.tillr.json
```

### Database

All data is stored in a single SQLite file (`tillr.db` by default). Key tables:

| Table | Purpose |
|-------|---------|
| `projects` | Project metadata |
| `milestones` | Milestone definitions and status |
| `features` | Features with tillr state, priority, and assignment |
| `feature_deps` | Dependency graph between features |
| `work_items` | Individual work items with agent prompts and results |
| `events` | Full audit log of every state change |
| `roadmap_items` | Roadmap entries with priority and category |
| `qa_results` | QA approval/rejection records with notes |
| `heartbeats` | Agent activity heartbeats |

You can inspect the database directly with any SQLite client:

```bash
sqlite3 tillr.db "SELECT id, name, status FROM features"
```

---

## Troubleshooting / FAQ

### "No .tillr.json found"

You're not inside a tillr project. Run `tillr init <name>` to create one, or `cd` into an existing project directory.

### "Database schema version mismatch"

Your `tillr.db` was created with a different version of tillr. Run `tillr doctor` to diagnose. Future versions will include automatic migrations.

### Can I use tillr with multiple agents?

Yes. Multiple agents can call `tillr next` concurrently. Each call returns a different work item — work items are assigned atomically to prevent double-assignment.

### Can I edit the database directly?

You can, but it's not recommended for state changes. Use the CLI to ensure events are logged, dependencies are checked, and cycles advance correctly. Direct reads (for debugging or reporting) are fine and encouraged.

### How do I back up my project?

Copy `.tillr.json` and `tillr.db`. That's everything. Both are regular files — commit them, sync them, or back them up however you like.

### Where are logs stored?

Events are stored in the `events` table inside `tillr.db`. Use `tillr history` or query the table directly. There are no external log files.

### Can the web viewer modify data?

No. The web viewer is strictly read-only. All state changes go through the CLI. This is a deliberate design choice — the CLI is the single source of truth.
