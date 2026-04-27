# Reset — Reference Document

Tillr is being reset to a minimal scaffold so it can be rebuilt
following the [consulting-firm roadmap](./consulting-firm/roadmap.md).
This doc indexes the pre-reset state. **The pre-reset code is preserved
in git** at the `archive/pre-reset` tag and branch (commit
`edae8899271449`); this doc tells you how to find specific things in
that history.

## Why we're resetting

The pre-reset codebase grew organically into 60+ CLI subcommands, 25+
web pages, 13 internal Go packages, and a sprawling feature set
spanning agents / cycles / decisions / discussions / ideas / sprints /
roadmap / queue / and more. Most of that surface is either obsolete
under the new model or gets reshaped by the consulting-firm proposal
(comments as the substrate, async reviewer dialogue, style guide
enforcement, context graph, hierarchy).

Rebuilding incrementally from a minimal scaffold per the roadmap is
faster and cleaner than retrofitting. The pre-reset code remains
fully accessible in git — we lose nothing, we just don't drag it with
us.

## Pre-reset git refs

Single source of truth for git archaeology:

- **Tag:** `archive/pre-reset`
- **Branch:** `archive/pre-reset`
- **Commit:** `edae88992714493652b73948a8a5583f1b662981`
- **Last consulting-firm commit on main before reset:** `399116e`
  (docs: stories as roadmap — 4 new stories, stage ordering, roadmap.md)

To find or resurrect anything documented below:

```bash
# View a deleted file's contents
git show archive/pre-reset:internal/cli/cycles.go

# See history of a deleted package
git log archive/pre-reset -- internal/engine/

# Diff a deleted file vs main
git diff main archive/pre-reset -- internal/cli/discussions.go

# Restore a deleted file to your working tree
git checkout archive/pre-reset -- internal/cli/specific-file.go

# Browse the pre-reset tree
git ls-tree -r archive/pre-reset internal/cli/
```

If you want to actually run the pre-reset code:

```bash
git checkout archive/pre-reset
just build
```

---

## Tech stack we keep

### Backend (Go 1.26)

| Library | Purpose |
|---------|---------|
| `github.com/spf13/cobra` | CLI scaffolding (root + subcommand pattern) |
| `modernc.org/sqlite` | SQLite, pure Go (no CGO) |
| `github.com/gorilla/websocket` | WebSocket for live updates |
| `github.com/fsnotify/fsnotify` | File watching (daemon config reload) |
| `gopkg.in/yaml.v3` | YAML config |
| `gofmt` / `goimports` / `golangci-lint` | Formatting and linting |

**Dropped:** `github.com/mattn/go-sqlite3` — duplicate driver; we
standardize on `modernc` (no CGO; works in jails).

### Frontend (React 19 + TypeScript 5)

| Library | Purpose |
|---------|---------|
| `vite` 7 | HMR-driven dev server, prod build |
| `react` 19 + `react-dom` | UI runtime |
| `react-router-dom` 7 | Client-side routing |
| `@tanstack/react-query` | Server state |
| `zustand` | Client state |
| `tailwindcss` 4 (+ `@tailwindcss/vite`) | Styling |
| `react-markdown` | Comment / spec rendering |
| `recharts` | Reserved for Layer 10 metrics; no UI uses it post-reset |
| `eslint`, `typescript-eslint`, `eslint-plugin-react-*` | Lint |

### Tooling / dev experience

| Tool | Purpose |
|------|---------|
| `mise` | Go / Node / golangci-lint version pinning |
| `just` | Build orchestration |
| `pnpm` | Frontend package manager |
| `overmind` + `Procfile.dev` | Dev orchestration (backend reload + Vite HMR in parallel) |

### File layout (preserved)

```
cmd/tillr/             — main entry
main.go                — package main shim
internal/              — Go packages
  cli/                 — Cobra commands
  config/              — project + daemon config
  daemon/              — multi-project HTTP server
  db/                  — SQLite + queries
  models/              — data models
  server/              — HTTP server, auth, ratelimit
  version/             — version metadata
web/src/               — frontend
  components/          — reusable UI
  hooks/, lib/, store/, styles/
  api/                 — API client
  pages/               — route handlers
docs/                  — design docs (kept fully)
tests/                 — integration tests
.tillr.json            — project config
tillr.db               — local SQLite DB (gitignored)
```

---

## What was deleted

All paths below refer to the `archive/pre-reset` tree. Use
`git show archive/pre-reset:<path>` to see contents.

### CLI subcommands removed (`internal/cli/`)

The pre-reset CLI had 61 files in `internal/cli/`. After the reset,
only these remain:

- `root.go` — Cobra root command
- `serve.go` — start the web server
- `project.go` — project init
- `features.go` — minimal: list / show / add (rewritten)
- `comment.go` — Stage 1: tillr comment <feature> "text" (NEW)
- `errors.go` — shared error helpers (kept)

Removed:

```
agent.go                  agent_capabilities.go      agent_sessions.go
agent_workflow.go         api_docs.go                api_key.go
attach.go                 audit.go                   backup.go
batch.go                  bulk.go                    changelog.go
completion.go             config_cmd.go              context.go
cycle_templates.go        cycles.go                  daemon.go
dashboard.go              decisions.go               discussions.go
encrypt.go                export.go                  export_git.go
export_pdf_png.go         export_report.go           git.go
github_import.go          github_import_test.go      guide.go
hooks.go                  ideas.go                   interactive.go
jira_import.go            jira_import_test.go        mcp.go
milestones.go             notifications.go           onboard.go
perf.go                   plugins.go                 pr.go
qa.go                     queue.go                   release_notes.go
roadmap.go                sprints.go                 sync_agents.go
tags.go                   template_cmd.go            templates.go
time_tracking.go          undo.go                    webhooks.go
workstreams.go            worktrees.go
```

### Internal packages removed

```
internal/crypto/    — payload encryption (no current use case)
internal/engine/    — cycle engine (will be rebuilt minimal in Stage 2)
internal/export/    — feature/roadmap/decisions exporters
internal/mcp/       — MCP server (re-add when needed; not Stage 1)
internal/plugin/    — plugin discovery + execution
internal/vcs/       — git integration (re-add when needed)
```

### Frontend pages removed (`web/src/pages/`)

Pre-reset had 27 pages. Remaining: `Features.tsx` (rewritten minimal),
`FeatureDetail.tsx` (rewritten minimal), and a single empty
`Placeholder.tsx` if needed for routing stubs.

Removed:

```
AgentDetail.tsx        Agents.tsx             Context.tsx
CycleDetail.tsx        Cycles.tsx             Dashboard.tsx
DecisionDetail.tsx     Decisions.tsx          DiscussionDetail.tsx
Discussions.tsx        History.tsx            IdeaDetail.tsx
Ideas.tsx              MilestoneDetail.tsx    Placeholder.tsx
QA.tsx                 Queue.tsx              Roadmap.tsx
RoadmapDetail.tsx      Spec.tsx               Stats.tsx
Timeline.tsx           Workflow.tsx           WorkstreamDetail.tsx
Workstreams.tsx
```

### Frontend components removed (`web/src/components/`)

```
AttachmentPanel.tsx    EntityLink.tsx         HelpModal.tsx
KeyboardShortcuts.tsx  Lightbox.tsx           NotificationBell.tsx
```

Kept: `Layout.tsx`, `Sidebar.tsx` (slimmed to just Features),
`MarkdownContent.tsx`, `Skeleton.tsx`, `StatusBadge.tsx`, `Toast.tsx`.

### Server bits removed

```
internal/server/webhooks.go — webhook delivery
```

### Tests removed

Tests for deleted code:

```
internal/cli/github_import_test.go
internal/cli/jira_import_test.go
internal/crypto/*_test.go (folder removed)
internal/engine/*_test.go (folder removed)
internal/export/*_test.go (folder removed)
internal/mcp/*_test.go (folder removed)
internal/plugin/*_test.go (folder removed)
internal/vcs/vcs_test.go (folder removed)
```

Tests kept:

```
internal/db/queries_test.go     — slimmed; only retained-table tests
internal/server/auth_test.go    — auth middleware
internal/server/ratelimit_test.go — rate limiter
tests/integration_test.go       — slimmed; only Stage 1 covers
```

### Database schema changes

Pre-reset schema had ~25 tables (features, work_items, events, cycles,
decisions, discussions, ideas, milestones, sprints, agent_sessions,
notifications, attachments, perf_metrics, etc.).

Post-reset schema is minimal:

- `projects` (kept, simplified)
- `features` (kept, simplified — no cycle / status state machine yet
  beyond `draft` / `done`)
- `comments` (NEW — Stage 1 substrate)

**Migration impact:** existing local `tillr.db` files are incompatible
with the new schema. Users must re-init: delete `tillr.db`, re-run
`tillr init`. (`tillr.db` is gitignored, so no commit-level impact.)

---

## What remains immediately after the reset

After all reset commits land, `just dev` produces:

- A backend serving on port 3847 with WebSocket plumbing intact.
- A frontend with one sidebar item (**Features**), an empty Features
  list page, and a feature-detail page with a comment thread.
- A CLI with: `tillr init`, `tillr serve`, `tillr feature add`, `tillr
  feature list`, `tillr feature show`, `tillr comment` (Stage 1
  preview).
- A SQLite database with three tables (`projects`, `features`,
  `comments`) and no cycle / agent state machine yet.

That's it. Anything beyond this is built per the
[roadmap](./consulting-firm/roadmap.md), starting with **Stage 0**
(platform adapter) and **Stage 1** (comments + cycle hooks).

---

## What was deliberately NOT removed

Future-us shouldn't be confused that these stayed:

- **Daemon multi-project model.** Useful when more than one project is
  active. Stays as scaffolding.
- **WebSocket scaffolding.** No producers immediately, but
  infrastructure is preserved for live comment updates in Stage 1.
- **Tailwind setup, React Query, Zustand, Router.** All foundational;
  adding them back later would just be busywork.
- **Auth + rate limiting (`internal/server/`).** Kept; minimal API
  still needs them.
- **`docs/`.** Untouched. Includes the consulting-firm proposal,
  roadmap, all user stories, vision, and the user guide.
- **mise / just / overmind / pnpm.** All dev infra preserved; only
  Justfile was minimally modified (`lint-ci` gofmt scope fix in commit
  `edae8899`).

---

## Forward plan

Next steps, after the reset commits land:

1. **Stage 0 — Foundational adapter** (~200 lines of Go).
   See [story 29 (Anders)](./consulting-firm/stories/29-anders-platform-adapter.md).
2. **Stage 1 — Comments + cycle hooks** (1-2 weeks).
   See [story 22 (Derek)](./consulting-firm/stories/22-derek-progressive-disclosure.md)
   and [story 1 (Priya)](./consulting-firm/stories/01-priya-solo-pm.md).
3. **Validate** for 4-6 weeks against Stage 1 criteria in
   [roadmap.md](./consulting-firm/roadmap.md).
4. **Stage 2** (Layer 4 + 4b — async dialogue), and onward per the
   roadmap.

---

## How to use this doc

When you need something we deleted:

1. Search this doc for the file or feature.
2. Run `git show archive/pre-reset:<path>` to view it.
3. Decide: do you want the old code as-is (`git checkout
   archive/pre-reset -- <path>`), or do you want to use it as
   reference for the new implementation?

**Default to reference, not restoration.** The reset exists because
the old code is shape-mismatched to the new model. Resurrecting it
verbatim is usually the wrong move; reading it for context and writing
fresh is usually the right one.

---

« [Docs index](./README.md) · [Roadmap](./consulting-firm/roadmap.md) · [Implementation layers](./consulting-firm/implementation-layers.md)
