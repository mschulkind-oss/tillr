# General Development Instructions

Welcome to **Lifecycle** — a human-in-the-loop project management tool for agentic software development.

## Project Overview

Lifecycle is a CLI + web viewer that sits between human product owners and AI agents. It tracks, visualizes, and steers work through structured iteration cycles. Think: project manager for agentic development.

**Tech Stack**: Go 1.24, SQLite (modernc.org/sqlite), Cobra (CLI), net/http + gorilla/websocket (web viewer)

## Project Structure

```
lifecycle/
├── cmd/lifecycle/          # Binary entry point (main.go)
├── internal/               # Private Go packages
│   ├── cli/                # Cobra command definitions
│   ├── server/             # HTTP + WebSocket server
│   ├── db/                 # SQLite database layer (schema, migrations, queries)
│   ├── engine/             # Core lifecycle state machine logic
│   ├── models/             # Go structs for all entities
│   └── config/             # Configuration management
├── web/                    # Static web assets (HTML, CSS, JS) — embedded in binary
│   ├── static/             # CSS, JS, images
│   └── templates/          # HTML templates
├── tests/                  # Integration tests
├── docs/                   # Documentation
│   ├── VISION.md           # Guiding light — read this first
│   ├── design/             # Architecture and design docs
│   │   └── iteration-cycles.md  # All cycle definitions
│   ├── guides/             # How-to docs
│   │   └── user-guide.md   # Complete user guide (keep up to date!)
│   └── plans/              # Future roadmap
├── scratch/                # Git-tracked working notes
├── trash/                  # Safety net — move files here, NEVER rm
├── context/                # Gitignored data/logs
├── AGENTS.md               # You are here (PRIVATE — never public)
├── OPEN_QUESTIONS.md        # Decision tracking (PRIVATE)
├── Justfile                # Task runner
├── go.mod / go.sum         # Go dependencies
├── mise.toml               # Tool version management
└── yolo-jail.jsonc         # YOLO Jail config (PRIVATE)
```

## Core Tools

- **mise**: Tool version manager (`mise install` to set up)
- **go**: Build, test, run (`go build`, `go test`)
- **just**: Command runner (see `just --list`)
- **golangci-lint**: Go linter
- **jj**: Jujutsu VCS (colocated with git)
- **Chrome DevTools MCP**: Browser automation for web viewer testing

## Common Commands

```bash
just check          # Format + lint + test (THE quality gate)
just build          # Build binary to bin/lifecycle
just run <args>     # Run CLI with args
just dev            # Start web viewer dev server
just test           # Run all tests
just format         # Format Go code
just lint           # Lint with golangci-lint
just push           # Push jj bookmarks to remotes
just promote        # Promote staging → main
```

## Best Practices

1. **Verification**: Always run `just check` after changes. This is the universal quality gate.
2. **Safety**: **NEVER** use `rm`. Move files to `trash/` instead.
3. **Regression Testing**: Always keep tests used to fix bugs. Integrate them into the permanent suite.
4. **Clean Code**: Follow Go idioms and conventions. Use `gofmt` and `golangci-lint`.
5. **Error Handling**: Return errors, don't panic. Use `fmt.Errorf("context: %w", err)` for wrapping.
6. **Embed Assets**: Use `//go:embed` for web assets so the binary is self-contained.

## TDD Workflow

1. **Red**: Write a failing test for the new functionality.
2. **Green**: Write the minimum code to pass.
3. **Refactor**: Clean up while keeping tests green.

**Bug Fixing:**
1. **Reproduce**: Write a failing test case.
2. **Fix**: Modify code until it passes.
3. **Persist**: NEVER delete the reproduction test.

## Work Tracking

- **Roadmap**: `docs/plans/roadmap.md` — prioritized feature list
- **Open Questions**: `OPEN_QUESTIONS.md` — decisions needing human input
- **User Guide**: `docs/guides/user-guide.md` — keep this up to date with every feature change

---

# Product Specification

## What We're Building

A complete project management tool for agentic development with three interfaces:

### 1. CLI Tool (`lifecycle`)

The CLI is the primary interface for both agents and humans. All commands output structured JSON when `--json` flag is used (critical for agent integration), and human-readable text by default.

#### Project Commands
```
lifecycle init <name>                    Create a new lifecycle-managed project
lifecycle status                         Project overview dashboard
lifecycle doctor                         Validate environment and setup
```

#### Feature Lifecycle Commands
```
lifecycle feature add <name> [flags]     Add a feature (--milestone, --priority, --depends-on)
lifecycle feature list [flags]           List features (--status, --milestone, --json)
lifecycle feature show <id>              Feature details + full history
lifecycle feature edit <id> [flags]      Edit feature properties
lifecycle feature remove <id>            Remove feature (with confirmation)
```

#### Agent Workflow Commands
```
lifecycle next [--cycle C]               Get next work item (returns JSON with agentPrompt)
lifecycle done [--result R]              Mark current work item complete
lifecycle fail [--reason R]              Mark current work item as failed
lifecycle heartbeat [--message M]        Agent heartbeat (prevents stale detection)
```

#### Milestone Commands
```
lifecycle milestone add <name> [flags]   Create a milestone
lifecycle milestone list                 List milestones with progress bars
lifecycle milestone show <id>            Milestone details
```

#### Iteration Cycle Commands
```
lifecycle cycle list                     List available cycle types
lifecycle cycle start <type> <feature>   Start a cycle for a feature
lifecycle cycle status                   Active cycle progress
lifecycle cycle history <feature>        Cycle history for a feature
lifecycle cycle score <score> [--notes]  Submit a judge score for current cycle step
```

#### Roadmap Commands
```
lifecycle roadmap show [--format F]      Display roadmap (table, json, or markdown)
lifecycle roadmap add <title> [flags]    Add roadmap item (--priority, --category)
lifecycle roadmap edit <id> [flags]      Edit roadmap item
lifecycle roadmap prioritize             Interactive prioritization
lifecycle roadmap export [--format F]    Export roadmap (md or json)
```

#### QA Commands
```
lifecycle qa pending                     Features awaiting QA
lifecycle qa approve <feature> [--notes] Approve a feature
lifecycle qa reject <feature> [--notes]  Reject → back to development
lifecycle qa checklist <feature>         Generate/show QA checklist
```

#### History & Search Commands
```
lifecycle history [flags]                Event history (--feature, --since, --type)
lifecycle search <query>                 Full-text search across all project data
lifecycle log                            Compact activity log
```

### 2. Web Viewer (`lifecycle serve`)

A live-reloaded web dashboard served from the binary itself (embedded assets via `//go:embed`).

**Pages/Views:**
- **Dashboard**: Project health at a glance — features by status (kanban-style), milestone progress bars, active agents, recent activity feed
- **Features**: Detailed feature list with status badges, filtering, sorting. Click to expand full history.
- **Roadmap**: Visual roadmap — prioritized, categorized, with status indicators. Should be presentation-quality (suitable for showing stakeholders).
- **Cycles**: Active iteration cycles — current step, scores over time (chart), iteration count, role currently active
- **History**: Searchable event timeline with filters
- **QA**: Review interface — see pending items, approve/reject with notes, view QA checklists

**Technical Requirements:**
- Single HTTP server on configurable port (default 3847)
- WebSocket for live updates (server pushes on any DB change)
- File watcher on the SQLite DB for change detection
- Responsive design (works on mobile for quick checks)
- No build step — vanilla HTML/CSS/JS served statically, or use a lightweight framework (Alpine.js, htmx) if it improves the code
- Dark mode support
- All data loaded via JSON API endpoints

**API Endpoints** (JSON, used by both web viewer and potentially external tools):
```
GET  /api/status                  Project overview
GET  /api/features                Feature list (query params for filtering)
GET  /api/features/:id            Feature detail
GET  /api/milestones              Milestone list
GET  /api/roadmap                 Roadmap items
GET  /api/cycles                  Active cycles
GET  /api/cycles/:id/history      Cycle iteration history
GET  /api/history                 Event history (with pagination)
GET  /api/search?q=<query>        Full-text search
POST /api/qa/:feature/approve     Approve feature
POST /api/qa/:feature/reject      Reject feature
WS   /ws                          WebSocket for live updates
```

### 3. SQLite Storage

One SQLite database per managed project (stored at `.lifecycle.db` or configurable path). The schema is in `internal/db/db.go` — use migrations for all schema changes.

**Key Design Decisions:**
- WAL mode for concurrent reads during web serving
- Full-text search via FTS5 virtual tables
- Events table captures EVERYTHING (audit trail)
- JSON columns for flexible structured data (cycle configs, QA checklists, agent prompts)
- Timestamps in ISO 8601 format

## Iteration Cycles

See `docs/design/iteration-cycles.md` for the full specification. The predefined cycles are:

1. **UI Refinement**: designer → ux-review → develop → manual-qa → judge
2. **Feature Implementation**: research → develop → agent-qa → judge → human-qa
3. **Roadmap Planning**: research → plan → create-roadmap → prioritize → human-review
4. **Bug Triage**: report → reproduce → root-cause → fix → verify
5. **Documentation**: research → draft → review → edit → publish
6. **Architecture Review**: analyze → propose → discuss → decide → implement
7. **Release**: freeze → qa → fix → staging → verify → ship
8. **Onboarding/DX**: try → friction-log → improve → verify → document

Each cycle step produces structured output stored in the DB. Judge steps produce numeric scores. Human steps block until human input via CLI or web viewer.

## Onboarding an Existing Project

Lifecycle can manage any software project. To onboard an existing project, use the guided onboarding command:

### Quick Onboard

```bash
# From the project root directory:
lifecycle onboard --name my-project --scan

# Or step by step:
lifecycle init my-project
lifecycle doctor
```

### Agent Onboarding Workflow

When an agent is tasked with onboarding a project into lifecycle, follow this process:

#### Step 1: Initialize
```bash
lifecycle onboard --name <project-name> --scan --json
```
This creates the project and scans the codebase. Read the output to understand the project structure.

#### Step 2: Create Milestones
Create 2–4 milestones representing development phases:
```bash
lifecycle milestone add "v1.0 MVP" --description "Core functionality complete"
lifecycle milestone add "v2.0 Polish" --description "UX refinements and documentation"
```

#### Step 3: Add Features (Use Judgement)

**For completed work** — Add features that are already built and working:
```bash
lifecycle feature add "Database Layer" \
  --status done \
  --description "PostgreSQL with migrations" \
  --spec "1. Schema migrations via goose\n2. Connection pooling\n3. Query builder" \
  --milestone v1.0-mvp \
  --priority 10
```

**For work in progress** — Add features that are partially built:
```bash
lifecycle feature add "Search API" \
  --status implementing \
  --description "Full-text search endpoint" \
  --spec "1. Elasticsearch integration\n2. Pagination\n3. Filters [NOT YET DONE]" \
  --milestone v1.0-mvp \
  --priority 7
```

**For planned work** — Add features on the roadmap but not started:
```bash
lifecycle feature add "Email Notifications" \
  --status draft \
  --description "Send emails on key events" \
  --spec "1. SMTP/SendGrid integration\n2. Template system\n3. Unsubscribe" \
  --milestone v2.0-polish \
  --priority 5
```

#### Step 4: Build the Roadmap
Add roadmap items for strategic planning:
```bash
lifecycle roadmap add "API Performance" \
  --description "Optimize query patterns, add caching layer, reduce p99 latency" \
  --priority high --category infrastructure --effort m
```

Link features to roadmap items:
```bash
lifecycle feature edit <feature-id> --roadmap-item <roadmap-id>
```

#### Step 5: Create Discussions for Open Questions
Use discussions for design decisions that need input:
```bash
lifecycle discuss new "RFC: Caching Strategy" \
  --feature cache-layer \
  --author onboarding-agent
lifecycle discuss comment 1 "Propose Redis for session cache, local LRU for hot paths" \
  --type proposal --author onboarding-agent
```

#### Step 6: Verify
```bash
lifecycle doctor          # Check everything is healthy
lifecycle status          # See project overview
lifecycle serve           # Launch web viewer to review
```

### How Far Back to Go

Use judgement based on project maturity:
- **New projects (<3 months)**: Add everything as features — the history is short enough to capture fully.
- **Established projects (3–12 months)**: Focus on current and planned work. Add major completed features as `--status done` with brief specs. Don't try to capture every past change.
- **Mature projects (>1 year)**: Only add actively maintained features and future work. Use `--status done` for major subsystems. Focus energy on roadmap and planned features.

### Writing Good Specs During Onboarding

Every feature should have a spec with numbered acceptance criteria. For completed features, document what IS built:
```
1. REST API with OpenAPI spec
2. JWT authentication with refresh tokens
3. Rate limiting at 100 req/s per client
4. PostgreSQL with connection pooling (max 50)
5. Automated migrations on startup
```

For planned features, document what SHOULD be built:
```
1. Full-text search across all entities
2. Faceted filtering by category, date, status
3. Search suggestions with autocomplete
4. Results pagination with cursor-based navigation
5. Search analytics dashboard
```

### Valid Feature Statuses for Onboarding
- `draft` — Planned, not started (default)
- `planning` — Requirements being defined
- `implementing` — Currently being built
- `agent-qa` — In automated testing
- `human-qa` — Awaiting human review
- `done` — Complete and deployed
- `blocked` — Cannot proceed (document why in description)

## Agent Integration Pattern

**CRITICAL: All context comes from the tool.** When dispatching work to sub-agents, pass the JSON output of `lifecycle next --json` as the work specification. Do NOT summarize, paraphrase, or add OOB context. The tool output IS the spec.

Agents interact with lifecycle via CLI commands. The typical agent loop:

```bash
# 1. Get next work item — returns FULL context (feature spec, cycle state, prior results, roadmap link)
WORK=$(lifecycle next --json)
# Returns WorkContext: {
#   "work_item": {"id": 1, "feature_id": "f1", "work_type": "develop", "agent_prompt": "..."},
#   "feature": {"name": "...", "description": "...", "spec": "...", "roadmap_item_id": "..."},
#   "cycle": {"cycle_type": "feature-implementation", "current_step": 1, ...},
#   "cycle_type": {"steps": ["research", "develop", "agent-qa", "judge", "human-qa"]},
#   "roadmap_item": {"title": "...", "description": "...", "priority": "critical"},
#   "prior_results": [{"work_type": "research", "result": "Found X, Y, Z..."}],
#   "agent_guidance": "You are working on feature \"F1\": ...\n\n## Feature Spec\n..."
# }

# 2. Pass the ENTIRE JSON to the sub-agent — it contains everything needed
# The agent_guidance field is a human-readable summary of what to do

# 3. Report completion
lifecycle done --result "Implemented feature X with tests"

# 4. Repeat
WORK=$(lifecycle next --json)
```

### Creating features with full in-band context:
```bash
# Always provide --spec with acceptance criteria so agents can work independently
lifecycle feature add "My Feature" \
  --description "Brief summary" \
  --spec "Acceptance criteria:\n1. Must do X\n2. Must handle Y\n3. Tests required for Z" \
  --milestone v1.0-production \
  --priority 5 \
  --roadmap-item my-roadmap-item-id
```

For iteration cycles with scoring:
```bash
# Judge step
lifecycle cycle score 8.5 --notes "Good implementation but accessibility needs work"

# Human QA step (blocks until human acts via CLI or web)
lifecycle qa approve f1 --notes "Looks good, ship it"
```

## Coding Guidelines (Go-Specific)

### Package Organization
- `cmd/lifecycle/main.go` — Only calls `cli.Execute()`
- `internal/cli/` — One file per command group (features.go, milestones.go, cycles.go, etc.)
- `internal/db/` — All SQL lives here. No SQL in other packages.
- `internal/engine/` — Pure business logic, no I/O. Takes DB interface, returns results.
- `internal/server/` — HTTP handlers, WebSocket hub, static file serving.
- `internal/models/` — Shared structs used across packages.
- `internal/config/` — Configuration loading and validation.

### Error Handling
```go
// Always wrap errors with context
if err != nil {
    return fmt.Errorf("loading feature %s: %w", id, err)
}

// User-facing errors should be clear and actionable
fmt.Fprintf(os.Stderr, "Error: no lifecycle project found. Run 'lifecycle init <name>' first.\n")
```

### JSON Output
Every command that produces output must support `--json` flag:
```go
if jsonOutput {
    enc := json.NewEncoder(os.Stdout)
    enc.SetIndent("", "  ")
    return enc.Encode(result)
}
// Human-readable output
```

### Testing
- Unit tests next to the code: `db_test.go`, `engine_test.go`
- Integration tests in `tests/` for CLI end-to-end
- Use `testing.T` and table-driven tests
- Test the CLI by calling command functions directly (not subprocess)
- Test DB operations with in-memory SQLite (`:memory:`)

### Web Assets
```go
//go:embed web/static web/templates
var webAssets embed.FS
```
Embed all web assets into the binary. No external file dependencies at runtime.

## Quality Standards

- All code must pass `just check` (format + lint + test)
- All new features must have tests
- All CLI commands must have `--json` output
- Web viewer must update in real-time via WebSocket
- User guide must be updated when features change
- Error messages must be clear and suggest next steps
