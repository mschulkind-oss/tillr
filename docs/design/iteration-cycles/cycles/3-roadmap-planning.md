# 3. Roadmap Planning

**Cycle ID**: `roadmap-planning`

**Purpose**: Create and refine a prioritized development roadmap through research, synthesis, and human conversation. This cycle produces `roadmap_items`, not code.

**When to use**: Starting a new project phase, quarterly planning, strategic pivots, or whenever the team needs to decide *what to build next*.

### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Researcher** | `roadmap-research` | Project context, market/domain info, existing roadmap | Competitive analysis, trend report, user need assessment | Read-only investigation |
| 2 | **Planner** | `roadmap-plan` | Research findings, project goals | Concrete roadmap items with descriptions and rationale | Creates roadmap item proposals |
| 3 | **Prioritizer** | `roadmap-prioritize` | Proposed roadmap items, project constraints | Ranked list with impact/effort/dependency analysis | Reorders and annotates |
| 4 | **Human Reviewer** | `roadmap-review` | Prioritized roadmap | Adjustments, approvals, additions, removals | Final authority |

### Flow

```
┌────────────────────────────────────────────────────────────────┐
│                                                                │
│  Researcher ──→ Planner ──→ Prioritizer ──→ Human Reviewer     │
│       ▲                                         │              │
│       │             needs more research         │              │
│       └──────────── feedback ◄──────────────────┘              │
│                                                 │              │
│                          approved               ▼              │
│                                               DONE             │
└────────────────────────────────────────────────────────────────┘
```

**Step-by-step**:

1. **Researcher** searches the web for similar products, analyzes the competitive landscape, identifies technology trends, and reviews user feedback or support tickets. Output is a structured research report with findings organized by theme.

2. **Planner** synthesizes research into concrete roadmap items. Each item has a title, description, category, and rationale. Output is a set of proposed `roadmap_items` ready for prioritization.

3. **Prioritizer** ranks items by impact, effort, dependencies, and strategic alignment. Assigns priority levels (critical/high/medium/low/nice-to-have) and identifies dependency chains between items. Output is a ranked roadmap with justification.

4. **Human Reviewer** discusses the roadmap. Can adjust priorities, add new items, remove items, defer items, or request more research on specific topics. If the human requests more research, the cycle loops back to the researcher with specific questions.

### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| Human approval | Explicit approval | Loop back to Researcher or Planner with feedback |

### Configuration

```json
{
  "cycle_id": "roadmap-planning",
  "max_iterations": 4,
  "quality_gate": {
    "type": "human_approval"
  },
  "roles": ["roadmap-research", "roadmap-plan", "roadmap-prioritize", "roadmap-review"],
  "typical_iterations": [2, 4],
  "on_max_iterations": "present_best_version",
  "loop_target_on_fail": "roadmap-research"
}
```

### Database Representation

```sql
-- Roadmap items created during planning
INSERT INTO roadmap_items (id, project_id, title, description, category, priority, status, sort_order)
VALUES
  ('ri-1', 'proj-1', 'Plugin system', 'Allow third-party extensions...', 'extensibility', 'high', 'proposed', 1),
  ('ri-2', 'proj-1', 'Real-time dashboard', 'WebSocket-based live updates...', 'visibility', 'critical', 'proposed', 2);

-- Planning tracked as work items on a planning feature
INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-plan', 'roadmap-research', 'done', '{"findings": ["...", "..."]}');

-- Human review captured
INSERT INTO qa_results (feature_id, qa_type, passed, notes)
VALUES ('feat-plan', 'human', 1, 'Approved with modifications: deferred plugin system to Q3.');

-- Audit trail
INSERT INTO events (project_id, feature_id, event_type, data)
VALUES ('proj-1', 'feat-plan', 'roadmap-approved', '{"items_approved": 8, "items_deferred": 2}');
```

### JSON Schema

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "RoadmapPlanningCycle",
  "type": "object",
  "properties": {
    "cycle_id": { "const": "roadmap-planning" },
    "feature_id": { "type": "string" },
    "iteration": { "type": "integer", "minimum": 1 },
    "state": {
      "type": "string",
      "enum": ["roadmap-research", "roadmap-plan", "roadmap-prioritize", "roadmap-review", "done", "escalated"]
    },
    "config": {
      "type": "object",
      "properties": {
        "max_iterations": { "type": "integer", "default": 4 },
        "loop_target_on_fail": { "type": "string", "default": "roadmap-research" }
      }
    },
    "proposed_items": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": { "type": "string" },
          "title": { "type": "string" },
          "category": { "type": "string" },
          "priority": { "type": "string", "enum": ["critical", "high", "medium", "low", "nice-to-have"] },
          "status": { "type": "string", "enum": ["proposed", "accepted", "deferred", "rejected"] }
        }
      }
    }
  },
  "required": ["cycle_id", "feature_id", "iteration", "state"]
}
```

« [All cycles](./README.md) · [Iteration cycles overview](../README.md)
