# 4. Bug Triage

**Cycle ID**: `bug-triage`

**Purpose**: Systematically identify, reproduce, root-cause, fix, and verify bugs. This cycle emphasizes *proof* — a reproduction test must exist before a fix is attempted, and it must pass after.

**When to use**: Bug reports, regression discoveries, error log investigation, or any work where the primary goal is *fixing something that's broken*.

### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Reporter** | `bug-report` | Bug description, error logs, user report | Structured bug report (expected vs actual, steps to reproduce, environment) | Documentation only |
| 2 | **Reproducer** | `bug-reproduce` | Bug report | Failing test case that demonstrates the bug | Test creation |
| 3 | **Root Cause Analyst** | `root-cause` | Failing test, bug report, codebase context | Root cause analysis with identified code paths | Read-only investigation |
| 4 | **Fixer** | `bug-fix` | Root cause analysis, failing test | Code fix (reproduction test must now pass) | Code changes |
| 5 | **Verifier** | `bug-verify` | Fix, full test suite | Verification report (fix works, no regressions) | Testing only |

### Flow

```
┌──────────────────────────────────────────────────────────────────────────┐
│                                                                          │
│  Reporter ──→ Reproducer ──→ Root Cause Analyst ──→ Fixer ──→ Verifier   │
│                                                       ▲          │       │
│                                                       │  failed  │       │
│                                                       └──────────┘       │
│                                                                  │       │
│                                                       passed     ▼       │
│                                                                DONE      │
└──────────────────────────────────────────────────────────────────────────┘
```

**Step-by-step**:

1. **Reporter** documents the bug in a structured format: expected behavior, actual behavior, steps to reproduce, environment details, severity assessment. Output is a complete bug report.

2. **Reproducer** creates a failing test case that demonstrates the bug. The test should fail reliably and fail for the *right reason*. If the bug cannot be reproduced, the cycle pauses for human investigation.

3. **Root Cause Analyst** investigates the codebase to identify why the bug occurs. Traces code paths, examines state transitions, identifies the root cause (not just the symptom). Output is a root cause analysis document.

4. **Fixer** implements the fix. The reproduction test must pass after the fix. No other tests should break. Output is code changes with the green reproduction test.

5. **Verifier** runs the full test suite and confirms: (a) the reproduction test passes, (b) no other tests regressed, (c) the fix addresses the root cause, not just the symptom. If verification fails, loops back to the fixer with details.

### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| Reproduction test exists | Required | Cycle blocks at Reproducer |
| Reproduction test passes | Required | Loop back to Fixer |
| No regressions | All tests pass | Loop back to Fixer |

### Configuration

```json
{
  "cycle_id": "bug-triage",
  "max_iterations": 5,
  "quality_gate": {
    "type": "automated",
    "conditions": [
      { "type": "test_exists", "description": "Reproduction test must exist" },
      { "type": "test_passes", "description": "Reproduction test must pass" },
      { "type": "no_regressions", "description": "Full test suite must pass" }
    ]
  },
  "roles": ["bug-report", "bug-reproduce", "root-cause", "bug-fix", "bug-verify"],
  "typical_iterations": [1, 3],
  "on_max_iterations": "escalate_to_human",
  "loop_target_on_fail": "bug-fix"
}
```

### Database Representation

```sql
-- Bug tracking through work items
INSERT INTO work_items (feature_id, work_type, status, agent_prompt)
VALUES ('feat-bug-1', 'bug-report', 'done',
        '{"expected": "Login returns 200", "actual": "Login returns 500", "steps": ["POST /login with valid creds"]}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-bug-1', 'bug-reproduce', 'done',
        '{"test_file": "tests/auth_test.go", "test_name": "TestLoginRegression_Issue42"}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-bug-1', 'root-cause', 'done',
        '{"cause": "Nil pointer in session middleware when cookie is expired", "file": "internal/auth/session.go", "line": 87}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-bug-1', 'bug-fix', 'done',
        '{"files_changed": ["internal/auth/session.go"], "reproduction_test_passes": true}');

-- Verification as QA result
INSERT INTO qa_results (feature_id, qa_type, passed, notes)
VALUES ('feat-bug-1', 'agent', 1, '{"tests_total": 142, "tests_passed": 142, "regressions": 0}');
```

### JSON Schema

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "BugTriageCycle",
  "type": "object",
  "properties": {
    "cycle_id": { "const": "bug-triage" },
    "feature_id": { "type": "string" },
    "iteration": { "type": "integer", "minimum": 1 },
    "state": {
      "type": "string",
      "enum": ["bug-report", "bug-reproduce", "root-cause", "bug-fix", "bug-verify", "done", "escalated"]
    },
    "config": {
      "type": "object",
      "properties": {
        "max_iterations": { "type": "integer", "default": 5 },
        "require_reproduction_test": { "type": "boolean", "default": true }
      }
    },
    "bug_report": {
      "type": "object",
      "properties": {
        "expected": { "type": "string" },
        "actual": { "type": "string" },
        "steps": { "type": "array", "items": { "type": "string" } },
        "severity": { "type": "string", "enum": ["critical", "high", "medium", "low"] }
      }
    },
    "root_cause": {
      "type": "object",
      "properties": {
        "cause": { "type": "string" },
        "file": { "type": "string" },
        "line": { "type": "integer" }
      }
    }
  },
  "required": ["cycle_id", "feature_id", "iteration", "state"]
}
```

« [All cycles](./README.md) · [Iteration cycles overview](../README.md)
