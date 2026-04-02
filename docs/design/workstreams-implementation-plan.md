# Workstreams — Implementation Design (Design Step)

> Comprehensive design for the remaining workstreams implementation work.
> Review this, mark up what you want changed, then approve to start building.

## What's Already Built

- DB migration 32 (3 tables: workstreams, workstream_notes, workstream_links)
- Full CRUD API endpoints (`/api/workstreams/...`)
- CLI commands (`tillr workstream create/list/show/note/resolve/link/close`, alias `ws`)
- Web UI: list page (tree view) + detail page (timeline, links, notes)
- Cycle engine: `CycleStep{Name, Human}` struct, human steps skip work items
- `tillr cycle advance --feature <id> --approve/--reject` CLI
- Vantage doc link integration
- Cycle progress bar on workstream detail page

## What Needs Building

### 1. Markdown Rendering for Notes

**Current state**: Notes use a simple regex-based renderer (`simpleMarkdown()`) that handles bold, italic, code, and links. Tables render as raw text.

**Proposed change**: Replace `simpleMarkdown()` with a proper lightweight markdown parser. Options:

- **Option A: `marked` library** — well-established, 12KB gzipped. Already familiar to this codebase (was in the old static assets as `marked.min.js`). Supports full GFM (tables, task lists, strikethrough).
- **Option B: Custom CSS + regex** — extend the current approach with table support. Lighter but won't handle edge cases.
- **Option C: `markdown-it`** — more extensible than marked, similar size.

**Recommendation**: Option A (`marked`). We already had it in the project, it's battle-tested, and the `.prose` CSS we built handles the styling.

**Scope**:
- Install `marked` as a dependency
- Create a `<MarkdownContent content={text} />` component that sanitizes + renders
- Replace `dangerouslySetInnerHTML` with the component in WorkstreamDetail (notes + description), FeatureDetail (spec), and anywhere else we render user markdown
- The `.prose` CSS already handles all the visual styling

### 2. Cycle Human-Step UI (Approve/Reject in Browser)

**Current state**: Human steps can only be advanced via CLI (`tillr cycle advance`). The web UI shows "Waiting for human input" but has no action buttons.

**Proposed change**: Add approve/reject buttons directly to the cycle section on both the workstream detail page and the feature detail page.

**API needed**: `POST /api/cycles/{id}/advance` with body `{"action": "approve"|"reject", "notes": "..."}`

**UI design**:
```
┌─────────────────────────────────────────────────┐
│ Active Cycle: collaborative-design              │
│ ████████████░░░░░░░░  Step 4/5: human-approve   │
│                                                 │
│ ⚠ Waiting for human input: human-approve        │
│                                                 │
│ Notes: [________________________________]       │
│                                                 │
│ [✓ Approve & Advance]  [✗ Request Changes]      │
│                                                 │
│ Related docs:                                   │
│  📄 Original design doc  →  (Vantage link)      │
│  📄 Research notes       →  (Vantage link)      │
└─────────────────────────────────────────────────┘
```

When "Approve & Advance" is clicked:
1. POST to `/api/cycles/{id}/advance`
2. Invalidate queries to refresh the page
3. If the next step is agent-owned, show "Agent work queued"
4. If the cycle completes, show "Cycle complete!" toast

When "Request Changes" is clicked:
1. POST with `action: "reject"`
2. Note is saved as context
3. Step stays where it is

**Feature detail page**: Same buttons appear in the cycle section when the current step is human-owned.

### 3. Agent Workstream Context

**Current state**: `GetWorkContext()` in `engine.go` builds context for agent work items (feature spec, cycle info, prior results, roadmap item). No workstream data.

**Proposed change**: When a feature is linked to a workstream (via `workstream_links`), include the workstream context in the work item prompt.

**Data flow**:
1. In `GetWorkContext()`, after fetching feature data, check for workstream links where `link_type='feature'` and `target_id=featureID`
2. If found, fetch the workstream detail (notes, open questions)
3. Add to the context:

```
## Human Context (Workstream: "Auth Refactor")

Recent notes:
- [decision] Decided to keep refresh tokens in httpOnly cookies
- [question] OPEN: Should we support OIDC providers?
- [idea] Could use the new crypto API for key rotation

Open questions waiting for human input: 1
```

**DB query needed**: `GetWorkstreamByLinkedFeature(db, featureID)` — reverse lookup from feature ID to workstream.

```sql
SELECT w.* FROM workstreams w
JOIN workstream_links wl ON wl.workstream_id = w.id
WHERE wl.link_type = 'feature' AND wl.target_id = ?
LIMIT 1
```

**Scope**: ~30 lines in engine.go, 1 new query function, update `buildAgentGuidance()`.

### 4. Workstream Notes Improvements

**a. Cmd+Enter to submit** — Already implemented in the textarea onKeyDown handler.

**b. Note editing** — Currently notes are immutable after creation. Add:
- Click-to-edit on note content (inline editing)
- API already supports `PATCH /api/workstreams/{id}/notes/{nid}`

**c. Note deletion** — Add a delete button (small trash icon) on hover. Confirm before deleting.

**d. Note count badges on list page** — Show note count and open question count on each workstream card in the list view.

```
┌───────────────────────────────────────────────┐
│ Human Workstreams Feature          3/30/2026  │
│ Design and implement workstreams...           │
│ [feature] [v0.2]   📝 5 notes  ❓ 1 open     │
└───────────────────────────────────────────────┘
```

### 5. Workstream-Feature Bidirectional Display

**Current state**: The workstream detail shows linked features. But the feature detail page doesn't show linked workstreams.

**Proposed change**: On the feature detail page, if the feature is linked to a workstream, show a card:

```
┌─────────────────────────────────────────┐
│ 🧵 Workstream: Human Workstreams Feature │
│ 3 open questions · Last note 2h ago     │
│ [View workstream →]                     │
└─────────────────────────────────────────┘
```

**DB query**: Same reverse lookup as #3 above.

### 6. Just-in-Time Cycle Step Definition

**Current state**: Cycle types have fixed step sequences defined in Go code. The collaborative-design type has 5 steps. To add/modify steps, you need to edit Go source.

**Proposed approach**: Allow cycles to be extended at runtime:

- New API: `POST /api/cycles/{id}/steps` — append a step to the current cycle
- New API: `PATCH /api/cycles/{id}` — update the cycle's step list (stored as JSON in a new column)
- When a cycle has custom steps (stored in DB), those override the type's default steps
- CLI: `tillr cycle add-step <feature> <step-name> [--human]`

**DB change**: Add `custom_steps TEXT` column to `cycle_instances`. If non-null, it overrides the type's default steps.

This lets you do:
```bash
# At human-approve step, decide what comes next
tillr cycle add-step human-workstreams implement
tillr cycle add-step human-workstreams integration-test
tillr cycle add-step human-workstreams human-final-review --human
tillr cycle advance --feature human-workstreams --approve
# → advances to "implement" step
```

### 7. Dashboard Workstream Widget

**Proposed**: Add a "My Workstreams" section to the dashboard showing active workstreams with their latest note and open question count. Quick access to the workstream that matters right now.

---

## Implementation Order

| Phase | Work | Effort |
|-------|------|--------|
| 1 | Markdown rendering (`marked` + component) | Small |
| 2 | Cycle advance API + approve/reject UI buttons | Medium |
| 3 | Agent workstream context (reverse lookup + guidance) | Small |
| 4 | Note improvements (badges, edit, delete) | Small |
| 5 | Feature↔Workstream bidirectional display | Small |
| 6 | Just-in-time cycle step definition | Medium |
| 7 | Dashboard widget | Small |

Phases 1-5 are straightforward implementation. Phase 6 is the most architecturally interesting — it makes cycles truly dynamic. Phase 7 is polish.

---

## Open Design Questions

### D1: Should "Request Changes" on a human step create a workstream note automatically?
When you reject a cycle step, the rejection notes could auto-populate as a "question" note on the linked workstream so it's visible in the timeline.

we should capture all interactions for historical purposes and minig from agents AND humans at every step. fully auditable. we can later decide how to visualize all this since we have teh data.

### D2: Should cycle steps be reorderable?
With just-in-time step definition (#6), can you also drag-reorder remaining steps? Or is append-only sufficient?

for now, we're doing this to get this cycle developed. later we can worry about JIT cycles as part of the product. for now. it's only agent driven, not tillr driven, so no UI.

### D3: Should the dashboard widget show all workstreams or just ones with open questions?
Active-with-questions feels more actionable than a full list.

have a sort or badge for actionable, but we still want something about all open workstreams.

### D4: Note thread/reply support?
Should notes support threaded replies? Or is the flat timeline sufficient for v1?
flat for v1

---

*Viewable in Vantage: http://localhost:8000/tillr/docs/design/workstreams-implementation-plan.md*
