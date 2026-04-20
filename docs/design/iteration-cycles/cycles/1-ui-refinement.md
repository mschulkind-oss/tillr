# 1. UI Refinement

**Cycle ID**: `ui-refinement`

**Purpose**: Iteratively improve the look, feel, and usability of UI components until they meet a high visual and interaction quality bar.

**When to use**: Styling changes, layout redesigns, component polish, accessibility improvements, or any work where the primary concern is *how it looks and feels* rather than *what it does*.

### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **UI Designer** | `ui-design` | Feature description, current screenshots, previous feedback | Visual change proposal (layout, colors, typography, spacing, component structure) | Design decisions only; no code |
| 2 | **UX Reviewer** | `ux-review` | Design proposal, accessibility requirements | Usability assessment, accessibility audit, interaction pattern review | Advisory; flags issues |
| 3 | **Developer** | `implement` | Approved design proposal, UX feedback | Code changes implementing the design | Code changes |
| 4 | **Manual QA** | `manual-qa` | Implementation, original design spec | Screenshots, interaction test results, visual diff report | Testing only; no code |
| 5 | **Judge** | `judge` | QA results, design proposal, UX review | Score (0–10) with detailed rubric breakdown and feedback | Score and feedback |

### Flow

```
┌─────────────────────────────────────────────────────────────┐
│                                                             │
│  UI Designer ──→ UX Reviewer ──→ Developer ──→ Manual QA   │
│       ▲                                            │        │
│       │                                            ▼        │
│       │                                         Judge       │
│       │                                            │        │
│       │              score < 8.5                   │        │
│       └────────────── feedback ◄───────────────────┘        │
│                                                             │
│                       score ≥ 8.5 → DONE                    │
└─────────────────────────────────────────────────────────────┘
```

**Step-by-step**:

1. **UI Designer** receives the feature description and any prior feedback. Proposes visual changes with rationale. Output is a structured design document describing layout, colors, typography, spacing, and component hierarchy.

2. **UX Reviewer** evaluates the design proposal against usability heuristics, accessibility standards (WCAG), and interaction patterns. Flags issues, suggests improvements. Output is an annotated review with severity ratings.

3. **Developer** implements the approved design. Applies UX reviewer's feedback where applicable. Output is working code changes.

4. **Manual QA** tests the implementation against the design spec. Captures screenshots, tests interactions (hover states, focus management, responsive behavior), checks accessibility. Output is a test report with evidence.

5. **Judge** scores the result on a 0–10 rubric covering visual fidelity, usability, accessibility, and polish. If the score is below the threshold, provides specific feedback directing the next iteration. Feedback is routed back to the UI Designer as input for the next cycle.

### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| Judge score | ≥ 8.5 / 10 | Loop back to UI Designer with judge feedback |
| Human override | Manual pass | Bypasses score threshold |

### Configuration

```json
{
  "cycle_id": "ui-refinement",
  "max_iterations": 7,
  "quality_gate": {
    "type": "score_or_override",
    "score_threshold": 8.5,
    "allow_human_override": true
  },
  "roles": ["ui-design", "ux-review", "implement", "manual-qa", "judge"],
  "typical_iterations": [3, 7],
  "on_max_iterations": "escalate_to_human"
}
```

### Database Representation

When a feature is assigned this cycle, the following records are created per iteration:

```sql
-- Feature assignment
UPDATE features SET assigned_cycle = 'ui-refinement' WHERE id = ?;

-- Work items per iteration (one per role)
INSERT INTO work_items (feature_id, work_type, status, agent_prompt)
VALUES
  ('feat-1', 'ui-design',  'pending', '{"iteration": 1, "feedback": null}'),
  ('feat-1', 'ux-review',  'pending', NULL),
  ('feat-1', 'implement',  'pending', NULL),
  ('feat-1', 'manual-qa',  'pending', NULL),
  ('feat-1', 'judge',      'pending', NULL);

-- Judge result captured as QA
INSERT INTO qa_results (feature_id, qa_type, passed, notes)
VALUES ('feat-1', 'agent', 0, '{"score": 7.2, "feedback": "Spacing inconsistent..."}');

-- Events for audit trail
INSERT INTO events (project_id, feature_id, event_type, data)
VALUES ('proj-1', 'feat-1', 'cycle-iteration', '{"cycle": "ui-refinement", "iteration": 3, "score": 7.2}');
```

### JSON Schema

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "UIRefinementCycle",
  "type": "object",
  "properties": {
    "cycle_id": { "const": "ui-refinement" },
    "feature_id": { "type": "string" },
    "iteration": { "type": "integer", "minimum": 1 },
    "state": {
      "type": "string",
      "enum": ["ui-design", "ux-review", "implement", "manual-qa", "judge", "done", "escalated"]
    },
    "config": {
      "type": "object",
      "properties": {
        "score_threshold": { "type": "number", "default": 8.5 },
        "max_iterations": { "type": "integer", "default": 7 },
        "allow_human_override": { "type": "boolean", "default": true }
      }
    },
    "history": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "iteration": { "type": "integer" },
          "score": { "type": ["number", "null"] },
          "feedback": { "type": ["string", "null"] },
          "work_item_ids": {
            "type": "array",
            "items": { "type": "integer" }
          }
        }
      }
    }
  },
  "required": ["cycle_id", "feature_id", "iteration", "state"]
}
```

« [All cycles](./README.md) · [Iteration cycles overview](../README.md)
