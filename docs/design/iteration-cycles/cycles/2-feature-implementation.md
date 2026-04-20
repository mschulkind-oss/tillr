# 2. Feature Implementation

**Cycle ID**: `feature-impl`

**Purpose**: Build new features from requirements through to human-approved quality. This is the workhorse cycle — the one most features will use.

**When to use**: New functionality, significant behavior changes, or any work that requires research, implementation, automated testing, and human sign-off.

### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Researcher** | `research` | Feature description, codebase context | Requirements analysis, approach options, relevant existing code | Read-only investigation |
| 2 | **Developer** | `implement` | Research findings, feature spec | Code changes with tests | Code changes |
| 3 | **Agent QA** | `agent-qa` | Implementation, test results | Automated test results, edge case analysis, code review | Testing and review; no code |
| 4 | **Judge** | `judge` | QA results, feature spec, implementation | Score (0–10) with completeness and quality breakdown | Score and feedback |
| 5 | **Human QA** | `human-qa` | Implementation, judge score, QA summary | Approval or rejection with feedback | Final authority |

### Flow

```
┌────────────────────────────────────────────────────────────────────┐
│                                                                    │
│  Researcher ──→ Developer ──→ Agent QA ──→ Judge                   │
│                     ▲                        │                     │
│                     │         score < 8.0    │                     │
│                     └──── feedback ◄─────────┘                     │
│                                              │                     │
│                              score ≥ 8.0     │                     │
│                                              ▼                     │
│                                          Human QA                  │
│                     ▲                        │                     │
│                     │         rejected       │                     │
│                     └──── feedback ◄─────────┘                     │
│                                              │                     │
│                              approved        ▼                     │
│                                            DONE                    │
└────────────────────────────────────────────────────────────────────┘
```

**Step-by-step**:

1. **Researcher** investigates the feature requirements. Reads existing code, identifies relevant modules, explores possible approaches, surfaces constraints. Output is a research document with recommended approach and open questions.

2. **Developer** implements the feature based on research findings. Writes code and tests. Output is a working implementation with passing tests.

3. **Agent QA** runs the full test suite, checks edge cases, reviews code for common issues (error handling, security, performance). Output is a structured QA report.

4. **Judge** evaluates the implementation against the feature spec. Scores completeness, code quality, test coverage, and adherence to requirements. If below threshold, provides specific feedback for the developer to address.

5. **Human QA** reviews the implementation with full context. Uses a checkbox-based approval flow stored in `qa_results.checklist`. Can approve (done) or reject with specific feedback that routes back to the developer.

### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| Judge score | ≥ 8.0 / 10 | Loop back to Developer with judge feedback |
| Human approval | Checkbox pass | Loop back to Developer with human feedback |
| Compound | Score ≥ 8.0 AND human pass | Both required for completion |

### Configuration

```json
{
  "cycle_id": "feature-impl",
  "max_iterations": 5,
  "quality_gate": {
    "type": "compound",
    "conditions": [
      { "type": "score", "threshold": 8.0 },
      { "type": "human_approval" }
    ]
  },
  "roles": ["research", "implement", "agent-qa", "judge", "human-qa"],
  "typical_iterations": [2, 5],
  "on_max_iterations": "escalate_to_human",
  "loop_target_on_judge_fail": "implement",
  "loop_target_on_human_fail": "implement"
}
```

### Database Representation

```sql
-- Feature flows through tillr statuses mapped to cycle roles
-- draft → planning (research) → implementing (develop) → agent-qa → human-qa → done

-- Research phase
INSERT INTO work_items (feature_id, work_type, status, agent_prompt)
VALUES ('feat-2', 'research', 'pending', '{"feature_spec": "...", "codebase_context": "..."}');

-- Implementation phase
INSERT INTO work_items (feature_id, work_type, status, agent_prompt)
VALUES ('feat-2', 'implement', 'pending', '{"research_findings": "...", "iteration": 1}');

-- Agent QA
INSERT INTO qa_results (feature_id, qa_type, passed, notes)
VALUES ('feat-2', 'agent', 1, '{"tests_passed": 47, "tests_failed": 0, "coverage": "89%"}');

-- Judge scoring
INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-2', 'judge', 'done', '{"score": 8.5, "breakdown": {"completeness": 9, "quality": 8, "tests": 9}}');

-- Human QA
INSERT INTO qa_results (feature_id, qa_type, passed, notes, checklist)
VALUES ('feat-2', 'human', 1, 'Looks good, clean implementation.',
        '{"items": [{"label": "Meets requirements", "checked": true}, {"label": "Code is readable", "checked": true}]}');
```

### JSON Schema

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "FeatureImplementationCycle",
  "type": "object",
  "properties": {
    "cycle_id": { "const": "feature-impl" },
    "feature_id": { "type": "string" },
    "iteration": { "type": "integer", "minimum": 1 },
    "state": {
      "type": "string",
      "enum": ["research", "implement", "agent-qa", "judge", "human-qa", "done", "escalated"]
    },
    "config": {
      "type": "object",
      "properties": {
        "score_threshold": { "type": "number", "default": 8.0 },
        "max_iterations": { "type": "integer", "default": 5 },
        "require_human_approval": { "type": "boolean", "default": true },
        "loop_target_on_judge_fail": { "type": "string", "default": "implement" },
        "loop_target_on_human_fail": { "type": "string", "default": "implement" }
      }
    },
    "history": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "iteration": { "type": "integer" },
          "score": { "type": ["number", "null"] },
          "human_approved": { "type": ["boolean", "null"] },
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
