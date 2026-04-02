# Human Workstreams — Research Notes

> Research phase of the `collaborative-design` cycle for feature `human-workstreams`.

## Executive Summary

Human Workstreams adds a lightweight journaling/tracking layer for **human-driven threads of work** that span multiple agent sessions and features. Unlike features (which track *what the system builds*), workstreams track *what the human is thinking about*.

This document covers the implementation approach, integration points, scope decisions, and open questions for human review.

---

## 1. Implementation Scope (MVP)

### What's in scope
- **3 new DB tables**: `workstreams`, `workstream_notes`, `workstream_links` (Migration 32)
- **CLI commands**: `tillr workstream {create, list, show, note, resolve, link, close}`
- **API endpoints**: CRUD for workstreams, notes, and links (`/api/workstreams/...`)
- **Web pages**: `/workstreams` (list) and `/workstreams/:id` (detail with timeline)
- **Vantage integration**: doc links render as clickable Vantage URLs when configured
- **Sidebar entry**: "Workstreams" under the WORKSPACE section

### What's NOT in scope (v1)
- Cross-project workstreams (keep per-project for now)
- Agent auto-creation of workstreams (human-only creation)
- Workstream templates or automation
- Mobile/responsive optimizations

---

## 2. Data Layer — Migration 32

Three new tables, following existing codebase patterns (TEXT primary keys, datetime defaults, CHECK constraints):

```sql
-- Workstreams: human-tracked threads of work
CREATE TABLE IF NOT EXISTS workstreams (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'archived')),
  tags TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Notes: timestamped entries on a workstream
CREATE TABLE IF NOT EXISTS workstream_notes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  workstream_id TEXT NOT NULL REFERENCES workstreams(id),
  content TEXT NOT NULL,
  note_type TEXT NOT NULL DEFAULT 'note' CHECK(note_type IN ('note', 'question', 'decision', 'idea')),
  resolved INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Links: features, docs, URLs, discussions linked to a workstream
CREATE TABLE IF NOT EXISTS workstream_links (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  workstream_id TEXT NOT NULL REFERENCES workstreams(id),
  link_type TEXT NOT NULL CHECK(link_type IN ('feature', 'doc', 'url', 'discussion')),
  target_id TEXT NOT NULL DEFAULT '',
  target_url TEXT NOT NULL DEFAULT '',
  label TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

**ID generation**: Slugified from name (same pattern as features), e.g., "Auth Refactor" → `auth-refactor`.

**Why TEXT PKs?** Consistency with the rest of the codebase — features, milestones, discussions all use slugified TEXT IDs.

---

## 3. CLI Commands

Following existing patterns in `internal/cli/`:

| Command | Example | Notes |
|---------|---------|-------|
| `workstream create <name>` | `tillr workstream create "Auth Refactor" --description "..." --tags "security,backend"` | Generates slug ID |
| `workstream list` | `tillr workstream list` | Active only by default, `--all` includes archived |
| `workstream show <id>` | `tillr workstream show auth-refactor` | Full detail: notes, links, open questions |
| `workstream note <id> <text>` | `tillr workstream note auth-refactor "Decided on approach X" --type decision` | Types: note, question, decision, idea |
| `workstream resolve <id> <note-id>` | `tillr workstream resolve auth-refactor 42` | Marks a question as answered |
| `workstream link <id>` | `tillr workstream link auth-refactor --feature api-auth` | Also: `--doc`, `--url`, `--discussion` |
| `workstream close <id>` | `tillr workstream close auth-refactor` | Archives the workstream |

**JSON output**: All commands support `--json` via the global flag, matching existing CLI conventions.

**Shortcut**: Register `ws` as an alias (like `f` for feature, `d` for discuss).

---

## 4. API Endpoints

Following the existing `server.go` handler pattern:

```
GET    /api/workstreams              → list (active by default, ?status=all)
POST   /api/workstreams              → create
GET    /api/workstreams/{id}         → detail (includes notes + links)
PATCH  /api/workstreams/{id}         → update name/description/status/tags
DELETE /api/workstreams/{id}         → delete

GET    /api/workstreams/{id}/notes   → list notes
POST   /api/workstreams/{id}/notes   → add note
PATCH  /api/workstreams/{id}/notes/{nid} → update (resolve question)
DELETE /api/workstreams/{id}/notes/{nid} → delete note

GET    /api/workstreams/{id}/links   → list links
POST   /api/workstreams/{id}/links   → add link
DELETE /api/workstreams/{id}/links/{lid} → delete link
```

**Authentication**: Same API key auth as all other endpoints.

---

## 5. Web UI Design

### `/workstreams` — List Page

Card layout similar to Features list:
- Each card: **name**, description snippet, note count, open question count, linked feature count
- Last activity timestamp (most recent note or link)
- Quick-action: "Add note" inline
- Filter: active (default) / archived / all

### `/workstreams/:id` — Detail Page

**Header section:**
- Name (editable inline), description, status badge, tags
- "Archive" / "Reactivate" action button

**Timeline section** (newest first):
- Notes displayed as timeline entries, color-coded by type:
  - **Note** (neutral/gray) — general thinking
  - **Question** (yellow/amber) — open questions, with resolve toggle
  - **Decision** (green) — decisions made
  - **Idea** (purple) — ideas to explore later
- Inline "Add note" form at top of timeline

**Links section:**
- **Features**: Status badge + link to feature detail page
- **Docs**: If Vantage URL configured → clickable link to `{vantage_url}/tillr/{path}`. Otherwise, plain file path.
- **URLs**: External links
- **Discussions**: Link to discussion detail page

**Add link form**: dropdown for type, text input for target

---

## 6. Vantage Integration

The Vantage URL is already configured:
- **Config**: `vantage_url: "http://localhost:8000"` in `.tillr.yaml`
- **Env var**: `LIFECYCLE_VANTAGE_URL`
- **API**: Exposed at `GET /api/config` → `{"vantage_url": "http://localhost:8000"}`

**Doc link rendering logic** (frontend):

```typescript
function docUrl(docPath: string, vantageUrl?: string): string | null {
  if (!vantageUrl) return null;
  // Strip leading slash/dots
  const clean = docPath.replace(/^\.?\//, '');
  return `${vantageUrl}/tillr/${clean}`;
}
```

If Vantage is configured, doc links show as "Open in Vantage" buttons. If not, they render as plain file paths with copy-to-clipboard.

---

## 7. Cross-Project Portability

The user wants to use workstreams on other projects ASAP. Key design decisions for portability:

1. **Self-contained in DB**: No file-system dependencies beyond the tillr DB. Works on any `tillr init`-ed project.
2. **No feature coupling required**: Workstreams can exist without linking to any features. They're useful standalone as a thinking journal.
3. **Config-independent**: Vantage integration is optional. Without it, everything still works — just no doc viewer links.
4. **Migration-safe**: Migration 32 uses `CREATE TABLE IF NOT EXISTS`, so it's safe to run on any existing tillr DB.

---

## 8. Collaborative-Design Cycle Integration

### How human-owned steps should work

The current cycle engine (`internal/engine/engine.go`) auto-creates work items for each step. For human-owned steps like `human-review` and `human-approve`, we need:

1. **Step metadata**: Mark steps as `human: true` in the cycle type definition
2. **No auto work-item**: When advancing to a human step, don't create an agent work item
3. **UI surfacing**: Show "Waiting for human input" on the feature detail page and dashboard
4. **Manual advance**: Human clicks "Approve" or "Request Changes" in the web UI to advance

**Proposed changes to models.go:**

```go
type CycleStep struct {
    Name  string
    Human bool // if true, step is human-owned (no agent work item)
}
```

This is a larger change that affects the cycle engine. For the MVP, we can:
- Keep the current string-based step definition
- Check step names for "human-" prefix convention (`human-review`, `human-approve`)
- Skip work item creation for these steps
- Add a manual advance API endpoint: `POST /api/cycles/{id}/advance`

---

## 9. Implementation Plan (Recommended Order)

1. **Migration 32** — Create the 3 tables
2. **DB queries** — CRUD functions in `internal/db/queries.go`
3. **CLI commands** — `tillr workstream` with all subcommands
4. **API endpoints** — REST handlers in `server.go`
5. **Frontend pages** — List and detail pages, sidebar entry
6. **Vantage integration** — Doc link rendering with config check
7. **Cycle engine update** — Human-step awareness (can be done incrementally)

Estimated: ~800 lines Go, ~400 lines TypeScript, 1 migration.

---

## Open Questions for Human Review

These need your input before we proceed to the design phase:

### Q1: Should workstream notes support markdown?
The design doc suggests yes for longer notes, plain text for short ones. But this adds rendering complexity on the frontend. **Recommendation**: Yes, reuse the existing `.prose` CSS we already built. Notes are entered as plain text but rendered as markdown.

absolutely support markdown for humans and agents. things should look nice and be easy to ready though good visual design.

### Q2: Should agents be able to read workstream context?
If a feature is linked to a workstream, should `tillr next --json` include the workstream's notes and open questions? This gives agents the human's thinking. **Recommendation**: Yes — add workstream context to `GetWorkContext()` in the engine when a feature has a workstream link.

yes? agents should have access to evertyhing and be able to organize their own info for humans and agents to see. i"d also like to have a hirerarchy of workstreams like a whole project vs a side task. I often want to dump in random notes of a project and have them structured. design docs or slack conversations.

### Q3: How should the collaborative-design cycle interact with the engine?
Option A: Add a `Human bool` field to cycle steps (structural change).
Option B: Use naming convention (`human-*` prefix) with no schema change.
Option C: Separate concept — "human tasks" that aren't work items at all.
**Recommendation**: Option B for MVP (least invasive), migrate to Option A later.

option A. do it right first.

### Q4: Should workstreams span projects?
For now, no — per-project keeps it simple and portable. Cross-project is a v2 idea. **Recommendation**: Per-project only. The human can create matching workstreams in different projects and link them via URLs.

no. nothing crosses the project boundary. up to the user to scope what a project is though. that said, one tillr server should be abel to hosue multiple projects. I don't want a port per project. one webserver, but separate scope in UI.

### Q5: Vantage URL format?
Is `http://localhost:8000/tillr/docs/design/foo.md` the right pattern? We need to confirm with actual Vantage routing. **Recommendation**: Make the path prefix configurable (default: project name from `.tillr.yaml`), so it works regardless of how Vantage is configured.

yes, configurable. pull out the entire prefix there which is http://localhost:8000/tillr/ and then it is repo relative.

### Q6: Workstream ID generation
Should IDs be auto-generated slugs (like features) or user-specified? **Recommendation**: Auto-slug from name, but allow override with `--id` flag for predictability.

yes, human readable but ultimately auto generated. I guess we can let people pick vanity slugs as well somehow.

---

*This research doc is viewable in Vantage at: http://localhost:8000/tillr/docs/research/human-workstreams-research.md*
