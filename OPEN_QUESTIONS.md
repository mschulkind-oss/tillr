# Open Questions

> Tracking unresolved decisions and design questions for lifecycle.

---

## Active Questions

### OQ-001: Project Name
**Status:** Active
**Raised:** 2025-07-17
**Context:** The current working name "lifecycle" is generic and may conflict with other packages on PyPI, npm, or Go module registries. A more distinctive name would improve discoverability and reduce confusion.

**Question:** Should we rename from "lifecycle" to something more distinctive?

**Options:**
- Keep "lifecycle" — it's descriptive and clear
- Rename to something from the name candidates list (see docs/NAME_CANDIDATES.md)

**Dependencies:** Blocks section 1 of open-source checklist. Name must be decided before public repo setup.

---

### OQ-002: Web Viewer Technology
**Status:** Active
**Raised:** 2025-07-17
**Context:** The web frontend needs to display project state, iteration history, and agent activity. Vanilla JS is simpler with zero build step, but a framework (React, Svelte) is more maintainable for complex interactive UI with state management.

**Question:** Should the web frontend use a framework (React, Svelte) or vanilla JS?

**Options:**
- Vanilla JS — zero build step, fewer dependencies, simpler deployment
- Svelte — small bundle, good DX, compiles away
- React — largest ecosystem, most agent/LLM training data
- HTMX + server templates — minimal JS, server-driven

**Dependencies:** Affects `web/` directory structure, build pipeline, CI configuration.

---

### OQ-003: Agent Protocol
**Status:** Active
**Raised:** 2025-07-17
**Context:** Agents need to interact with lifecycle to report status, request human input, and receive instructions. CLI commands are universal and work with any agent, but a stdin/stdout JSON protocol would be faster for tight iteration loops and richer data exchange.

**Question:** Should agents interact via CLI commands only, or also support a stdin/stdout JSON protocol?

**Options:**
- CLI only — universal, simple, works with any shell-based agent
- JSON protocol over stdin/stdout — faster, structured, better for streaming
- Both — CLI for simple cases, JSON protocol for advanced integrations
- HTTP API — agents call a local server, most flexible but heavier

**Dependencies:** Affects core architecture, agent integration docs, and SDK design.

---

### OQ-004: Database Strategy
**Status:** Active
**Raised:** 2025-07-17
**Context:** lifecycle needs to persist project state, iteration history, and configuration. A single global database is simpler to query across projects but couples them. Per-project databases provide isolation but make cross-project views harder.

**Question:** One global DB or one DB per managed project?

**Options:**
- Global DB — single SQLite file, easy cross-project queries, simpler backup
- Per-project DB — SQLite file in each project root, full isolation, portable
- Hybrid — per-project for project data, global for user preferences and cross-project state

**Dependencies:** Affects data model, CLI commands, migration strategy, and backup/restore.

---

## Answered Questions

_No answered questions yet._
