# 6. Architecture Review

**Cycle ID**: `arch-review`

**Purpose**: Evaluate and evolve system architecture through structured analysis, proposal, adversarial challenge, and human decision-making. This cycle produces *decisions*, not code — though it may end with initial scaffolding.

**When to use**: Significant structural changes, technology evaluations, performance architecture, scaling decisions, or any work where the wrong choice is expensive to reverse.

### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Analyst** | `arch-analyze` | Current architecture, pain points, requirements | Architecture assessment with identified opportunities and risks | Read-only investigation |
| 2 | **Proposer** | `arch-propose` | Analysis, requirements, constraints | Architectural proposals with trade-off matrices | Creates proposals |
| 3 | **Discussant** | `arch-discuss` | Proposals, current architecture | Adversarial review: challenges assumptions, surfaces risks, explores alternatives | Advisory; challenges |
| 4 | **Decider** | `arch-decide` | Proposals, discussion, trade-offs | Final architectural decision with rationale | Human; final authority |
| 5 | **Implementer** | `arch-implement` | Decision, chosen proposal | Implementation plan, initial scaffolding, migration path | Code changes (scaffolding only) |

### Flow

```
┌────────────────────────────────────────────────────────────────────────┐
│                                                                        │
│  Analyst ──→ Proposer ──→ Discussant ──→ Decider (human)               │
│                  ▲                           │                         │
│                  │     needs more options    │                         │
│                  └───────────────────────────┘                         │
│                                              │                         │
│                               decided        ▼                         │
│                                         Implementer ──→ DONE           │
└────────────────────────────────────────────────────────────────────────┘
```

**Step-by-step**:

1. **Analyst** examines the current architecture. Identifies pain points, bottlenecks, coupling issues, scaling limits, and opportunities for improvement. Output is an architecture assessment document.

2. **Proposer** creates one or more architectural proposals. Each includes a description, diagrams (as text), trade-off analysis (pros/cons/risks), effort estimate, and migration path. Output is a structured set of proposals.

3. **Discussant** plays devil's advocate. Challenges each proposal's assumptions, identifies risks not covered, explores alternatives not considered, and pressure-tests the trade-off analysis. Output is an adversarial review.

4. **Decider** (human) reviews the proposals and discussion. Makes the final architectural decision with documented rationale. Can request additional proposals or deeper analysis, which loops back to the proposer.

5. **Implementer** creates a concrete implementation plan for the chosen architecture. May include initial scaffolding code, directory structure, interface definitions, and a phased migration plan.

### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| Human decision | Explicit architectural choice | Loop back to Proposer for more options |
| Implementation plan approved | Human signs off on plan | Loop back to Implementer with feedback |

### Configuration

```json
{
  "cycle_id": "arch-review",
  "max_iterations": 4,
  "quality_gate": {
    "type": "compound",
    "conditions": [
      { "type": "human_decision", "description": "Human must choose an architectural direction" },
      { "type": "human_approval", "description": "Implementation plan must be approved" }
    ]
  },
  "roles": ["arch-analyze", "arch-propose", "arch-discuss", "arch-decide", "arch-implement"],
  "typical_iterations": [2, 3],
  "on_max_iterations": "escalate_to_human",
  "min_proposals": 2,
  "loop_target_on_fail": "arch-propose"
}
```

### Database Representation

```sql
-- Architecture work items
INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-arch-1', 'arch-analyze', 'done',
        '{"pain_points": ["tight coupling in auth", "no caching layer"], "opportunities": ["event sourcing", "CQRS"]}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-arch-1', 'arch-propose', 'done',
        '{"proposals": [{"id": "A", "title": "Event sourcing", "effort": "high"}, {"id": "B", "title": "Simple refactor", "effort": "low"}]}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-arch-1', 'arch-discuss', 'done',
        '{"challenges": [{"proposal": "A", "risk": "Operational complexity"}, {"proposal": "B", "risk": "Wont scale past 10k users"}]}');

-- Human decision captured
INSERT INTO qa_results (feature_id, qa_type, passed, notes)
VALUES ('feat-arch-1', 'human', 1, 'Chose proposal A (event sourcing). Phased migration over 3 sprints.');

INSERT INTO events (project_id, feature_id, event_type, data)
VALUES ('proj-1', 'feat-arch-1', 'arch-decision', '{"chosen": "A", "rationale": "Long-term scalability outweighs short-term complexity"}');
```

### JSON Schema

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "ArchitectureReviewCycle",
  "type": "object",
  "properties": {
    "cycle_id": { "const": "arch-review" },
    "feature_id": { "type": "string" },
    "iteration": { "type": "integer", "minimum": 1 },
    "state": {
      "type": "string",
      "enum": ["arch-analyze", "arch-propose", "arch-discuss", "arch-decide", "arch-implement", "done", "escalated"]
    },
    "config": {
      "type": "object",
      "properties": {
        "max_iterations": { "type": "integer", "default": 4 },
        "min_proposals": { "type": "integer", "default": 2 }
      }
    },
    "proposals": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": { "type": "string" },
          "title": { "type": "string" },
          "description": { "type": "string" },
          "effort": { "type": "string", "enum": ["low", "medium", "high"] },
          "pros": { "type": "array", "items": { "type": "string" } },
          "cons": { "type": "array", "items": { "type": "string" } }
        }
      }
    },
    "decision": {
      "type": "object",
      "properties": {
        "chosen_proposal": { "type": "string" },
        "rationale": { "type": "string" },
        "decided_by": { "type": "string" }
      }
    }
  },
  "required": ["cycle_id", "feature_id", "iteration", "state"]
}
```

« [All cycles](./README.md) · [Iteration cycles overview](../README.md)
