# Human Workstreams — Design Spec v2

> Refined design incorporating human review feedback. This is the implementation-ready spec.

## Summary

Workstreams are human-tracked threads of work that live alongside the automated tillr. They support hierarchy (project-level → side tasks), markdown notes with type tagging, linked features/docs/URLs, and full agent access.

## Data Model

### workstreams

```sql
CREATE TABLE IF NOT EXISTS workstreams (
  id TEXT PRIMARY KEY,                    -- auto-slug from name, or vanity slug
  project_id TEXT NOT NULL DEFAULT '',    -- scoped to project
  parent_id TEXT DEFAULT NULL,            -- hierarchy: NULL = top-level, else child
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',   -- markdown
  status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'archived')),
  tags TEXT NOT NULL DEFAULT '',          -- comma-separated
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  FOREIGN KEY (parent_id) REFERENCES workstreams(id)
);
```

**Hierarchy**: `parent_id` enables nesting. A top-level workstream (parent_id=NULL) is a major thread like "Ship v0.2". Child workstreams are side tasks or sub-threads. UI shows these as indented or grouped.

### workstream_notes

```sql
CREATE TABLE IF NOT EXISTS workstream_notes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  workstream_id TEXT NOT NULL REFERENCES workstreams(id),
  content TEXT NOT NULL,                  -- markdown
  note_type TEXT NOT NULL DEFAULT 'note' CHECK(note_type IN ('note', 'question', 'decision', 'idea', 'import')),
  source TEXT NOT NULL DEFAULT '',        -- e.g. "slack", "design-doc", "manual"
  resolved INTEGER NOT NULL DEFAULT 0,   -- for questions: 0=open, 1=answered
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

**note_type = 'import'**: For dumped-in content (slack convos, design doc excerpts). The `source` field tracks where it came from.

### workstream_links

```sql
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

## Cycle Engine: Human-Owned Steps

### Model change

```go
type CycleStep struct {
    Name  string `json:"name"`
    Human bool   `json:"human"` // true = human-owned, no agent work item
}
```

`CycleType.Steps` changes from `[]string` to `[]CycleStep`.

**Impact**: All cycle type definitions, the engine's step advancement logic, work item creation, and the JSON API responses need updating.

### Behavior

When the engine advances to a step where `Human == true`:
1. **No work item created** — the step doesn't go into the agent queue
2. **Feature status unchanged** — stays in current state (no auto-transition)
3. **Event logged**: `cycle.human_step_reached` with step name
4. **Web UI**: Shows "Waiting for human input" badge on the feature/cycle
5. **Manual advance**: Human clicks "Approve" / "Request Changes" in web UI, or uses CLI: `tillr cycle advance --feature <id>`

### Updated cycle types

```go
{Name: "collaborative-design", Steps: []CycleStep{
    {Name: "intake", Human: false},
    {Name: "research", Human: false},
    {Name: "human-review", Human: true},
    {Name: "design", Human: false},
    {Name: "human-approve", Human: true},
}}
```

All existing cycle types keep `Human: false` for all steps — no behavior change.

## Vantage Integration

### Config

The Vantage **base prefix** is configurable. Default: `http://localhost:8000/{project_name}/`.

- YAML: `vantage_url: "http://localhost:8000"`
- Env: `LIFECYCLE_VANTAGE_URL`
- API: `GET /api/config` → `{"vantage_url": "http://localhost:8000", "project_id": "tillr"}`

### Doc link rendering

Frontend computes: `{vantage_url}/{project_id}/{relative_path}`

Example: `http://localhost:8000/tillr/docs/design/human-workstreams.md`

If `vantage_url` is empty, show plain file path with copy button.

## CLI Commands

```bash
# Workstream CRUD
tillr workstream create "Auth Refactor" [--description "..."] [--tags "..."] [--parent <id>] [--id <vanity-slug>]
tillr workstream list [--all] [--tree]
tillr workstream show <id>
tillr workstream close <id>

# Notes
tillr workstream note <id> "content" [--type note|question|decision|idea|import] [--source "slack"]
tillr workstream resolve <id> <note-id>

# Links
tillr workstream link <id> --feature <fid>
tillr workstream link <id> --doc <path>
tillr workstream link <id> --url <url> [--label "..."]
tillr workstream link <id> --discussion <did>

# Cycle advance (human steps)
tillr cycle advance --feature <id> [--approve|--reject] [--notes "..."]
```

Shortcut: `ws` → `workstream`

## API Endpoints

```
GET    /api/workstreams                    → list (active by default, ?status=all&tree=true)
POST   /api/workstreams                    → create
GET    /api/workstreams/{id}               → detail (includes children, notes, links)
PATCH  /api/workstreams/{id}               → update
DELETE /api/workstreams/{id}               → archive (soft delete → status=archived)

POST   /api/workstreams/{id}/notes         → add note
PATCH  /api/workstreams/{id}/notes/{nid}   → update/resolve
DELETE /api/workstreams/{id}/notes/{nid}   → delete

POST   /api/workstreams/{id}/links         → add link
DELETE /api/workstreams/{id}/links/{lid}    → delete

POST   /api/cycles/{id}/advance            → manual advance (human steps)
```

## Web UI

### Sidebar
Add "Workstreams" entry under WORKSPACE section (between Features and Roadmap).

### /workstreams — List page
- Tree view: top-level workstreams with expandable children
- Each card: name, description snippet, note count, open questions (yellow badge), linked features
- Last activity timestamp
- Quick "add note" inline form
- Filter: active (default) / archived / all

### /workstreams/:id — Detail page
- **Header**: name (editable), description (editable, markdown preview), status, tags, parent breadcrumb
- **Children**: if top-level, show child workstreams as cards
- **Timeline** (newest first, scrollable):
  - Note (gray) — general thinking
  - Question (amber) — with resolved/open toggle
  - Decision (green) — decisions made
  - Idea (purple) — ideas to explore
  - Import (blue) — dumped content with source tag
- **Links section**: features (with status badge), docs (with Vantage button), URLs, discussions
- **Add note form**: text area + type dropdown + submit
- **Add link form**: type dropdown + target input + submit

### Cycle detail — human step indicator
When a cycle is on a human-owned step, show:
- Yellow "Waiting for human input" banner
- "Approve" and "Request Changes" buttons
- Link to relevant docs/workstream for context

## Agent Access

When `tillr next --json` returns work context for a feature linked to a workstream:
- Include `workstream` object with: id, name, description, recent notes, open questions
- Agents can read the human's thinking and respond to open questions
- Agents can add notes to workstreams via CLI or API

## Implementation Order

1. **Migration 32**: Create 3 tables (workstreams, workstream_notes, workstream_links)
2. **Model change**: `CycleStep` struct with `Human` field, update all cycle type definitions
3. **Engine update**: Human step detection, skip work item creation, manual advance
4. **DB queries**: CRUD for workstreams, notes, links
5. **CLI**: `workstream` command with all subcommands, `cycle advance`
6. **API**: REST endpoints for workstreams + cycle advance
7. **Frontend**: Sidebar entry, list page, detail page, cycle human-step UI
8. **Agent context**: Enrich `GetWorkContext` with workstream data

---

*Viewable in Vantage: http://localhost:8000/tillr/docs/design/human-workstreams-v2.md*
