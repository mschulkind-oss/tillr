# 8. Onboarding / DX (Developer Experience)

**Cycle ID**: `onboarding-dx`

**Purpose**: Improve the developer and user onboarding experience by systematically finding and eliminating friction. This cycle simulates a fresh user experience and iterates until onboarding is smooth.

**When to use**: New project setup, major version changes, DX audits, or whenever someone reports that getting started is painful.

### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Trier** | `dx-try` | Project README, getting-started docs | Attempt log: every step taken, every error hit, every moment of confusion | Fresh-perspective testing |
| 2 | **Friction Logger** | `dx-friction` | Attempt log | Prioritized friction point catalog with severity and category | Analysis and categorization |
| 3 | **Improver** | `dx-improve` | Friction points, codebase | Fixes: better error messages, docs, defaults, tooling | Code and doc changes |
| 4 | **Verifier** | `dx-verify` | Improvements, original friction points | Re-attempt log confirming friction points are resolved | Fresh-perspective re-testing |
| 5 | **Documenter** | `dx-document` | Verified improvements | Updated getting-started guides, README, troubleshooting docs | Documentation changes |

### Flow

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                                                                              │
│  Trier ──→ Friction Logger ──→ Improver ──→ Verifier ──→ Documenter          │
│                                                 │                            │
│                                      friction   │                            │
│                                      remains    │                            │
│    ▲                                             │                            │
│    └─────────────────────────────────────────────┘                            │
│                                                                              │
│                                   all clear ──→ DONE                         │
└──────────────────────────────────────────────────────────────────────────────┘
```

**Step-by-step**:

1. **Trier** starts from scratch — a clean environment with no prior context. Follows only the project's README and getting-started documentation. Documents every step taken, every command run, every error encountered, every moment of confusion or delay. Output is a detailed attempt log.

2. **Friction Logger** analyzes the attempt log and catalogs every friction point. Categorizes each by type (error message, missing doc, bad default, unclear step, missing dependency) and severity (blocker, painful, annoying, minor). Output is a prioritized friction catalog.

3. **Improver** addresses friction points starting with blockers, then painful, then annoying. Fixes might include: better error messages, clearer documentation, sensible defaults, automated setup steps, dependency checks. Output is code and documentation changes.

4. **Verifier** re-attempts the onboarding from scratch, specifically checking that each friction point has been resolved. If friction remains, loops back to Trier for a full fresh attempt (the improvements may have shifted the experience).

5. **Documenter** updates the getting-started guide, README, troubleshooting section, and any other onboarding documentation to reflect the improved experience.

### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| No blockers | Zero blocker-severity friction points | Loop back to Improver |
| No painful issues | Zero painful-severity friction points | Loop back to Improver |
| Clean onboarding | Verifier completes without new friction | Loop back to Trier for full re-attempt |

### Configuration

```json
{
  "cycle_id": "onboarding-dx",
  "max_iterations": 5,
  "quality_gate": {
    "type": "friction_free",
    "max_blocker_friction": 0,
    "max_painful_friction": 0,
    "max_annoying_friction": 3
  },
  "roles": ["dx-try", "dx-friction", "dx-improve", "dx-verify", "dx-document"],
  "typical_iterations": [2, 4],
  "on_max_iterations": "escalate_to_human",
  "loop_target_on_fail": "dx-try",
  "fresh_environment_required": true
}
```

### Database Representation

```sql
-- DX work items
INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-dx-1', 'dx-try', 'done',
        '{"steps_taken": 14, "errors_hit": 3, "confusion_points": 5, "total_time_minutes": 22}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-dx-1', 'dx-friction', 'done',
        '{"friction_points": [{"id": "f1", "type": "missing-dep", "severity": "blocker", "description": "go not found in PATH"}, {"id": "f2", "type": "unclear-step", "severity": "painful", "description": "No explanation of what tillr init does"}]}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-dx-1', 'dx-improve', 'done',
        '{"fixed": ["f1", "f2"], "changes": ["Added mise check to init", "Expanded README setup section"]}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-dx-1', 'dx-verify', 'done',
        '{"friction_remaining": 0, "steps_taken": 12, "total_time_minutes": 8}');

-- Verification as QA
INSERT INTO qa_results (feature_id, qa_type, passed, notes)
VALUES ('feat-dx-1', 'agent', 1, '{"blockers": 0, "painful": 0, "annoying": 1}');
```

### JSON Schema

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "OnboardingDXCycle",
  "type": "object",
  "properties": {
    "cycle_id": { "const": "onboarding-dx" },
    "feature_id": { "type": "string" },
    "iteration": { "type": "integer", "minimum": 1 },
    "state": {
      "type": "string",
      "enum": ["dx-try", "dx-friction", "dx-improve", "dx-verify", "dx-document", "done", "escalated"]
    },
    "config": {
      "type": "object",
      "properties": {
        "max_iterations": { "type": "integer", "default": 5 },
        "max_blocker_friction": { "type": "integer", "default": 0 },
        "max_painful_friction": { "type": "integer", "default": 0 },
        "max_annoying_friction": { "type": "integer", "default": 3 },
        "fresh_environment_required": { "type": "boolean", "default": true }
      }
    },
    "friction_catalog": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": { "type": "string" },
          "type": { "type": "string", "enum": ["error-message", "missing-doc", "bad-default", "unclear-step", "missing-dep", "tooling-gap"] },
          "severity": { "type": "string", "enum": ["blocker", "painful", "annoying", "minor"] },
          "description": { "type": "string" },
          "resolved": { "type": "boolean" }
        }
      }
    }
  },
  "required": ["cycle_id", "feature_id", "iteration", "state"]
}
```

« [All cycles](./README.md) · [Iteration cycles overview](../README.md)
