# Human Workstreams

> A lightweight journal for tracking parallel threads of work across agent sessions.

## Problem

When working with AI agents, a human often has multiple parallel threads running:
- "Refactor the auth layer" (spans 3 features, 2 sessions)
- "Ship the mobile dashboard" (one big feature, ongoing)
- "Investigate that weird perf regression" (no feature yet, just exploring)

These get intertwined in a single agent session. The human context-switches between them, but there's no place to:
1. See all active threads at a glance
2. Link related features, docs, and notes together
3. Track open questions waiting for human input
4. Resume context after stepping away

Features track *what the system builds*. Workstreams track *what the human is thinking about*.

## Core Concept

A **workstream** is:
- Created by a human (never auto-generated)
- A named thread with notes, linked features, and linked docs
- Active or archived (simple two-state tillr)
- Cross-project portable (works on any tillr-managed repo)

## Data Model

```sql
CREATE TABLE workstreams (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'archived')),
  tags TEXT DEFAULT '',  -- comma-separated
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE workstream_notes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  workstream_id TEXT NOT NULL REFERENCES workstreams(id),
  content TEXT NOT NULL,
  note_type TEXT NOT NULL DEFAULT 'note' CHECK(note_type IN ('note', 'question', 'decision', 'idea')),
  resolved INTEGER NOT NULL DEFAULT 0,  -- for questions: 0=open, 1=answered
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE workstream_links (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  workstream_id TEXT NOT NULL REFERENCES workstreams(id),
  link_type TEXT NOT NULL CHECK(link_type IN ('feature', 'doc', 'url', 'discussion')),
  target_id TEXT NOT NULL DEFAULT '',    -- feature ID, discussion ID, etc.
  target_url TEXT NOT NULL DEFAULT '',   -- URL or file path
  label TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

## CLI Interface

```bash
# Create a workstream
tillr workstream create "Auth Refactor" --description "Rethinking JWT + session handling"

# List active workstreams
tillr workstream list

# Add a note (default type: note)
tillr workstream note auth-refactor "Decided to keep refresh tokens in httpOnly cookies"

# Add an open question
tillr workstream note auth-refactor "Should we support OIDC providers?" --type question

# Answer/resolve a question
tillr workstream resolve auth-refactor <note-id>

# Link a feature
tillr workstream link auth-refactor --feature api-authentication

# Link a doc (opens in Vantage if available)
tillr workstream link auth-refactor --doc docs/design/auth-flow.md

# Link an external URL
tillr workstream link auth-refactor --url "https://example.com/rfc"

# Show full workstream detail
tillr workstream show auth-refactor

# Archive when done
tillr workstream close auth-refactor
```

## Web UI

### /workstreams — List page
- Active workstreams as cards
- Each card: name, description snippet, note count, open question count, linked feature count
- Last activity timestamp
- Quick "add note" inline

### /workstreams/:id — Detail page
- Header: name, description (editable), status, tags
- Timeline of notes (newest first), color-coded by type:
  - Note (neutral) — general thinking
  - Question (yellow) — open questions needing human input, with resolved toggle
  - Decision (green) — decisions made
  - Idea (purple) — ideas to explore
- Linked features section with status badges
- Linked docs section — if Vantage is running, shows "Open in Vantage" button
- Linked URLs

## Vantage Integration

- Frontend fetches `/api/config` to get `vantage_url`
- If set, doc links render as `{vantage_url}/tillr/{doc_path}`
- If not set, doc links render as plain file paths
- Detection: frontend checks if Vantage is reachable on first load

## Cycle Type: `collaborative-design`

A new cycle type for human-in-the-loop design work. Unlike `feature-implementation`, this cycle has human-owned steps.

```
Steps:
1. intake (agent) — capture requirements, create workstream, link docs
2. research (agent) — investigate approaches, write research doc, surface open questions
3. human-review (HUMAN) — human reviews research, answers questions, provides direction
4. design (agent) — write design doc based on human input
5. human-approve (HUMAN) — human reviews design, approves or requests changes
6. [next steps defined just-in-time by human]
```

Key difference: steps marked `HUMAN` pause the cycle and surface in the web UI as "waiting for human input" rather than going into the agent work queue.

---

## Open Questions

1. **Should workstream notes support markdown?** Probably yes for longer notes, but short notes should just be plain text entry.

2. **Should agents be able to read workstream context?** Yes — `tillr next --json` should include workstream context if the feature being worked on is linked to a workstream. This gives agents the human's thinking.

3. **How does the `collaborative-design` cycle interact with the existing cycle engine?** The engine needs a concept of "human-owned" steps that don't queue agent work items but instead surface as notifications/tasks for the human.

4. **Should workstreams span projects?** For now, no — keep them per-project. Cross-project workstreams are a v2 idea.

5. **Vantage URL format**: Is `http://localhost:8000/tillr/docs/design/foo.md` the right pattern? Need to confirm with the actual Vantage daemon config.
