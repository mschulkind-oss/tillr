# Iteration Cycles

## Overview

An iteration cycle is a structured loop that moves work from "not done" to "done" through a sequence of specialized agent roles, with quality gates that determine whether to iterate again or ship. Each role in a cycle corresponds to one agent invocation — a single, focused unit of work with clear inputs and outputs.

Cycles encode best practices. They answer the questions that ad-hoc workflows leave implicit: *Who does what? In what order? How do we know it's good enough? When do we stop?*

Every cycle shares the same fundamental structure:

```
[Role 1] → [Role 2] → ... → [Role N] → [Quality Gate]
                                              │
                                    pass ←────┴────→ fail
                                      │                 │
                                    done          loop back with feedback
```

The `assigned_cycle` field on a feature determines which cycle governs its workflow. The cycle defines what `work_type` values get created in the `work_items` table, and what `event_type` entries accumulate in the `events` table as work progresses.

---

## Shared Concepts

### Roles

Each role in a cycle is a single agent invocation. A role has:

- **Name**: What this agent is called in the cycle (e.g., `developer`, `reviewer`)
- **Work type**: The `work_type` value stored in `work_items` (e.g., `ui-design`, `implement`)
- **Inputs**: What the agent receives (previous role's output, feature description, context)
- **Outputs**: What the agent produces (code changes, test results, scores, documentation)
- **Authority**: What the agent is allowed to do (read-only analysis, code changes, approval)

### Quality Gates

A quality gate is the decision point at the end of each iteration. It answers one question: *is this good enough to proceed, or do we loop back?*

Gates can be:

| Gate Type | Mechanism | Example |
|-----------|-----------|---------|
| **Score threshold** | Judge agent scores 0–10; must meet minimum | Score ≥ 8.0 |
| **Human approval** | Human reviews with pass/fail | Checkbox approval |
| **Automated check** | Tests pass, no regressions | All tests green |
| **Compound** | Multiple conditions combined | Score ≥ 8.0 AND human approval |

### Iteration Limits

Every cycle has a `max_iterations` configuration. If the cycle hasn't converged after this many iterations, it halts and escalates to a human with a summary of all attempts. This prevents infinite loops and runaway token burn.

### Data Flow

Each iteration produces a row in `work_items` for every role that executes, and an event in the `events` table for each state transition. The `qa_results` table captures quality gate outcomes.

```
Iteration 1:  work_item(design) → work_item(review) → work_item(implement) → qa_result
Iteration 2:  work_item(design) → work_item(review) → work_item(implement) → qa_result
...
Iteration N:  → qa_result(passed=true) → feature.status = 'done'
```

---

## Cycle Definitions

---

### 1. UI Refinement

**Cycle ID**: `ui-refinement`

**Purpose**: Iteratively improve the look, feel, and usability of UI components until they meet a high visual and interaction quality bar.

**When to use**: Styling changes, layout redesigns, component polish, accessibility improvements, or any work where the primary concern is *how it looks and feels* rather than *what it does*.

#### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **UI Designer** | `ui-design` | Feature description, current screenshots, previous feedback | Visual change proposal (layout, colors, typography, spacing, component structure) | Design decisions only; no code |
| 2 | **UX Reviewer** | `ux-review` | Design proposal, accessibility requirements | Usability assessment, accessibility audit, interaction pattern review | Advisory; flags issues |
| 3 | **Developer** | `implement` | Approved design proposal, UX feedback | Code changes implementing the design | Code changes |
| 4 | **Manual QA** | `manual-qa` | Implementation, original design spec | Screenshots, interaction test results, visual diff report | Testing only; no code |
| 5 | **Judge** | `judge` | QA results, design proposal, UX review | Score (0–10) with detailed rubric breakdown and feedback | Score and feedback |

#### Flow

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

#### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| Judge score | ≥ 8.5 / 10 | Loop back to UI Designer with judge feedback |
| Human override | Manual pass | Bypasses score threshold |

#### Configuration

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

#### Database Representation

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

#### JSON Schema

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

---

### 2. Feature Implementation

**Cycle ID**: `feature-impl`

**Purpose**: Build new features from requirements through to human-approved quality. This is the workhorse cycle — the one most features will use.

**When to use**: New functionality, significant behavior changes, or any work that requires research, implementation, automated testing, and human sign-off.

#### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Researcher** | `research` | Feature description, codebase context | Requirements analysis, approach options, relevant existing code | Read-only investigation |
| 2 | **Developer** | `implement` | Research findings, feature spec | Code changes with tests | Code changes |
| 3 | **Agent QA** | `agent-qa` | Implementation, test results | Automated test results, edge case analysis, code review | Testing and review; no code |
| 4 | **Judge** | `judge` | QA results, feature spec, implementation | Score (0–10) with completeness and quality breakdown | Score and feedback |
| 5 | **Human QA** | `human-qa` | Implementation, judge score, QA summary | Approval or rejection with feedback | Final authority |

#### Flow

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

#### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| Judge score | ≥ 8.0 / 10 | Loop back to Developer with judge feedback |
| Human approval | Checkbox pass | Loop back to Developer with human feedback |
| Compound | Score ≥ 8.0 AND human pass | Both required for completion |

#### Configuration

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

#### Database Representation

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

#### JSON Schema

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

---

### 3. Roadmap Planning

**Cycle ID**: `roadmap-planning`

**Purpose**: Create and refine a prioritized development roadmap through research, synthesis, and human conversation. This cycle produces `roadmap_items`, not code.

**When to use**: Starting a new project phase, quarterly planning, strategic pivots, or whenever the team needs to decide *what to build next*.

#### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Researcher** | `roadmap-research` | Project context, market/domain info, existing roadmap | Competitive analysis, trend report, user need assessment | Read-only investigation |
| 2 | **Planner** | `roadmap-plan` | Research findings, project goals | Concrete roadmap items with descriptions and rationale | Creates roadmap item proposals |
| 3 | **Prioritizer** | `roadmap-prioritize` | Proposed roadmap items, project constraints | Ranked list with impact/effort/dependency analysis | Reorders and annotates |
| 4 | **Human Reviewer** | `roadmap-review` | Prioritized roadmap | Adjustments, approvals, additions, removals | Final authority |

#### Flow

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

#### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| Human approval | Explicit approval | Loop back to Researcher or Planner with feedback |

#### Configuration

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

#### Database Representation

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

#### JSON Schema

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

---

### 4. Bug Triage

**Cycle ID**: `bug-triage`

**Purpose**: Systematically identify, reproduce, root-cause, fix, and verify bugs. This cycle emphasizes *proof* — a reproduction test must exist before a fix is attempted, and it must pass after.

**When to use**: Bug reports, regression discoveries, error log investigation, or any work where the primary goal is *fixing something that's broken*.

#### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Reporter** | `bug-report` | Bug description, error logs, user report | Structured bug report (expected vs actual, steps to reproduce, environment) | Documentation only |
| 2 | **Reproducer** | `bug-reproduce` | Bug report | Failing test case that demonstrates the bug | Test creation |
| 3 | **Root Cause Analyst** | `root-cause` | Failing test, bug report, codebase context | Root cause analysis with identified code paths | Read-only investigation |
| 4 | **Fixer** | `bug-fix` | Root cause analysis, failing test | Code fix (reproduction test must now pass) | Code changes |
| 5 | **Verifier** | `bug-verify` | Fix, full test suite | Verification report (fix works, no regressions) | Testing only |

#### Flow

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

#### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| Reproduction test exists | Required | Cycle blocks at Reproducer |
| Reproduction test passes | Required | Loop back to Fixer |
| No regressions | All tests pass | Loop back to Fixer |

#### Configuration

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

#### Database Representation

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

#### JSON Schema

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

---

### 5. Documentation

**Cycle ID**: `documentation`

**Purpose**: Create and refine documentation through iterative drafting, expert review, and editorial polish. Produces documentation that is accurate, complete, clear, and well-structured.

**When to use**: API documentation, user guides, architecture docs, onboarding materials, READMEs, or any work where the deliverable is *written prose about the system*.

#### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Researcher** | `doc-research` | Documentation target, codebase, existing docs | Information gathering report (code analysis, API surface, user needs) | Read-only investigation |
| 2 | **Drafter** | `doc-draft` | Research findings, documentation standards | Initial documentation draft | Content creation |
| 3 | **Reviewer** | `doc-review` | Draft, source code, accuracy requirements | Accuracy and completeness review with annotations | Advisory; flags issues |
| 4 | **Editor** | `doc-edit` | Reviewed draft, style guide | Refined documentation (language, structure, formatting) | Content modification |
| 5 | **Publisher** | `doc-publish` | Final draft | Integrated documentation (placed in correct location, linked, indexed) | File operations |

#### Flow

```
┌──────────────────────────────────────────────────────────────────────┐
│                                                                      │
│  Researcher ──→ Drafter ──→ Reviewer ──→ Editor ──→ Publisher        │
│                    ▲            │                                     │
│                    │   issues   │                                     │
│                    └────────────┘                                     │
│                                                         │            │
│                                              approved   ▼            │
│                                                       DONE           │
└──────────────────────────────────────────────────────────────────────┘
```

**Step-by-step**:

1. **Researcher** gathers information needed for the documentation. Analyzes code, reads existing docs, identifies the audience, catalogs what needs to be covered. Output is a research document with organized findings.

2. **Drafter** writes the initial documentation based on research findings. Follows project documentation standards. Output is a complete first draft.

3. **Reviewer** checks the draft for accuracy (does it match the code?), completeness (does it cover everything?), and clarity (will the audience understand it?). Annotates issues. If significant inaccuracies exist, loops back to the drafter.

4. **Editor** refines language, improves structure, fixes formatting, ensures consistency with the project's documentation style. Output is a polished document.

5. **Publisher** places the document in the correct location within the project, adds it to any indexes or navigation, creates cross-links, and verifies it renders correctly.

#### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| Reviewer approval | No critical accuracy issues | Loop back to Drafter |
| Editor approval | Meets style and clarity standards | Loop back to Editor (self-revise) |

#### Configuration

```json
{
  "cycle_id": "documentation",
  "max_iterations": 4,
  "quality_gate": {
    "type": "compound",
    "conditions": [
      { "type": "role_approval", "role": "doc-review" },
      { "type": "role_approval", "role": "doc-edit" }
    ]
  },
  "roles": ["doc-research", "doc-draft", "doc-review", "doc-edit", "doc-publish"],
  "typical_iterations": [2, 3],
  "on_max_iterations": "publish_with_disclaimer",
  "loop_target_on_fail": "doc-draft"
}
```

#### Database Representation

```sql
-- Documentation work items
INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-docs-1', 'doc-research', 'done',
        '{"topics": ["API endpoints", "authentication flow", "error codes"], "source_files": 12}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-docs-1', 'doc-draft', 'done',
        '{"file": "docs/api-reference.md", "word_count": 2400, "sections": 8}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-docs-1', 'doc-review', 'done',
        '{"accuracy_issues": 0, "completeness_gaps": 1, "clarity_issues": 3}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-docs-1', 'doc-edit', 'done',
        '{"changes": "Restructured auth section, added code examples, fixed formatting"}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-docs-1', 'doc-publish', 'done',
        '{"location": "docs/api-reference.md", "cross_links_added": 4}');
```

#### JSON Schema

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "DocumentationCycle",
  "type": "object",
  "properties": {
    "cycle_id": { "const": "documentation" },
    "feature_id": { "type": "string" },
    "iteration": { "type": "integer", "minimum": 1 },
    "state": {
      "type": "string",
      "enum": ["doc-research", "doc-draft", "doc-review", "doc-edit", "doc-publish", "done", "escalated"]
    },
    "config": {
      "type": "object",
      "properties": {
        "max_iterations": { "type": "integer", "default": 4 },
        "style_guide": { "type": "string", "description": "Path to project style guide" }
      }
    },
    "document": {
      "type": "object",
      "properties": {
        "target_path": { "type": "string" },
        "audience": { "type": "string" },
        "type": { "type": "string", "enum": ["api-reference", "guide", "tutorial", "architecture", "readme"] }
      }
    }
  },
  "required": ["cycle_id", "feature_id", "iteration", "state"]
}
```

---

### 6. Architecture Review

**Cycle ID**: `arch-review`

**Purpose**: Evaluate and evolve system architecture through structured analysis, proposal, adversarial challenge, and human decision-making. This cycle produces *decisions*, not code — though it may end with initial scaffolding.

**When to use**: Significant structural changes, technology evaluations, performance architecture, scaling decisions, or any work where the wrong choice is expensive to reverse.

#### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Analyst** | `arch-analyze` | Current architecture, pain points, requirements | Architecture assessment with identified opportunities and risks | Read-only investigation |
| 2 | **Proposer** | `arch-propose` | Analysis, requirements, constraints | Architectural proposals with trade-off matrices | Creates proposals |
| 3 | **Discussant** | `arch-discuss` | Proposals, current architecture | Adversarial review: challenges assumptions, surfaces risks, explores alternatives | Advisory; challenges |
| 4 | **Decider** | `arch-decide` | Proposals, discussion, trade-offs | Final architectural decision with rationale | Human; final authority |
| 5 | **Implementer** | `arch-implement` | Decision, chosen proposal | Implementation plan, initial scaffolding, migration path | Code changes (scaffolding only) |

#### Flow

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

#### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| Human decision | Explicit architectural choice | Loop back to Proposer for more options |
| Implementation plan approved | Human signs off on plan | Loop back to Implementer with feedback |

#### Configuration

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

#### Database Representation

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

#### JSON Schema

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

---

### 7. Release

**Cycle ID**: `release`

**Purpose**: Prepare and ship a release with quality confidence. This cycle is linear — it doesn't loop back to the beginning, but individual stages can retry on failure.

**When to use**: Cutting a release, shipping a version, publishing a package, or any workflow that takes existing work and packages it for delivery.

#### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Freezer** | `release-freeze` | Branch state, feature list | Release branch, changelog draft, inclusion list | Branch management |
| 2 | **QA** | `release-qa` | Release branch | Comprehensive test results (unit, integration, manual) | Testing only |
| 3 | **Fixer** | `release-fix` | Failed tests, QA report | Targeted fixes on the release branch | Code changes (release branch only) |
| 4 | **Stager** | `release-stage` | Tested release branch | Staging deployment, deployment verification | Deployment |
| 5 | **Verifier** | `release-verify` | Staging environment | Final validation report (smoke tests, sanity checks) | Testing only |
| 6 | **Shipper** | `release-ship` | Verified staging | Tagged release, published changelog, distribution | Release publication |

#### Flow

```
┌───────────────────────────────────────────────────────────────────────────────┐
│                                                                               │
│  Freezer ──→ QA ──→ Fixer ──→ Stager ──→ Verifier ──→ Shipper ──→ DONE       │
│                │       ▲         │           ▲                                │
│                │ fail  │         │   fail    │                                │
│                └───────┘         └───────────┘                                │
└───────────────────────────────────────────────────────────────────────────────┘
```

**Step-by-step**:

1. **Freezer** creates the release branch from the current stable state. Documents what's included (features, fixes, changes). Drafts the changelog. Output is a release branch and inclusion manifest.

2. **QA** runs comprehensive testing against the release branch: unit tests, integration tests, manual smoke tests. Output is a detailed test report with pass/fail for each category.

3. **Fixer** addresses any failures found by QA. Fixes are applied directly to the release branch and are minimal — only what's needed to fix the issue. After fixing, QA re-runs (loops between QA and Fixer until clean).

4. **Stager** deploys the release branch to a staging environment. Verifies the deployment succeeded. Output is a deployment confirmation with environment details.

5. **Verifier** performs final validation on staging. Smoke tests key user flows, checks for deployment-specific issues (environment variables, configurations, external service connectivity). If issues are found, loops back to Stager or Fixer.

6. **Shipper** tags the release in version control, finalizes the changelog, publishes to distribution channels (package registry, GitHub release, etc.), and announces the release.

#### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| All tests pass | 100% pass rate | Loop between QA and Fixer |
| Staging verification | Smoke tests pass | Loop between Stager and Verifier |
| Changelog updated | Required | Block Shipper until complete |

#### Configuration

```json
{
  "cycle_id": "release",
  "max_iterations": 5,
  "quality_gate": {
    "type": "compound",
    "conditions": [
      { "type": "all_tests_pass" },
      { "type": "staging_verified" },
      { "type": "changelog_updated" }
    ]
  },
  "roles": ["release-freeze", "release-qa", "release-fix", "release-stage", "release-verify", "release-ship"],
  "typical_iterations": [1, 3],
  "on_max_iterations": "abort_release",
  "allow_hotfix_loop": true
}
```

#### Database Representation

```sql
-- Release tracked as a feature with the release cycle
INSERT INTO features (id, project_id, name, status, assigned_cycle)
VALUES ('release-v0.2.0', 'proj-1', 'Release v0.2.0', 'implementing', 'release');

-- Work items track each stage
INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('release-v0.2.0', 'release-freeze', 'done',
        '{"branch": "release/v0.2.0", "features_included": 5, "fixes_included": 3}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('release-v0.2.0', 'release-qa', 'done',
        '{"unit_tests": {"passed": 142, "failed": 0}, "integration_tests": {"passed": 38, "failed": 1}}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('release-v0.2.0', 'release-ship', 'done',
        '{"tag": "v0.2.0", "changelog": "CHANGELOG.md", "published_to": ["github-releases"]}');

-- Release event
INSERT INTO events (project_id, feature_id, event_type, data)
VALUES ('proj-1', 'release-v0.2.0', 'release-shipped', '{"version": "v0.2.0", "date": "2025-01-15"}');
```

#### JSON Schema

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "ReleaseCycle",
  "type": "object",
  "properties": {
    "cycle_id": { "const": "release" },
    "feature_id": { "type": "string" },
    "iteration": { "type": "integer", "minimum": 1 },
    "state": {
      "type": "string",
      "enum": ["release-freeze", "release-qa", "release-fix", "release-stage", "release-verify", "release-ship", "done", "aborted"]
    },
    "config": {
      "type": "object",
      "properties": {
        "max_iterations": { "type": "integer", "default": 5 },
        "allow_hotfix_loop": { "type": "boolean", "default": true },
        "staging_environment": { "type": "string" }
      }
    },
    "release": {
      "type": "object",
      "properties": {
        "version": { "type": "string" },
        "branch": { "type": "string" },
        "features_included": { "type": "array", "items": { "type": "string" } },
        "tag": { "type": "string" }
      }
    }
  },
  "required": ["cycle_id", "feature_id", "iteration", "state"]
}
```

---

### 8. Onboarding / DX (Developer Experience)

**Cycle ID**: `onboarding-dx`

**Purpose**: Improve the developer and user onboarding experience by systematically finding and eliminating friction. This cycle simulates a fresh user experience and iterates until onboarding is smooth.

**When to use**: New project setup, major version changes, DX audits, or whenever someone reports that getting started is painful.

#### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Trier** | `dx-try` | Project README, getting-started docs | Attempt log: every step taken, every error hit, every moment of confusion | Fresh-perspective testing |
| 2 | **Friction Logger** | `dx-friction` | Attempt log | Prioritized friction point catalog with severity and category | Analysis and categorization |
| 3 | **Improver** | `dx-improve` | Friction points, codebase | Fixes: better error messages, docs, defaults, tooling | Code and doc changes |
| 4 | **Verifier** | `dx-verify` | Improvements, original friction points | Re-attempt log confirming friction points are resolved | Fresh-perspective re-testing |
| 5 | **Documenter** | `dx-document` | Verified improvements | Updated getting-started guides, README, troubleshooting docs | Documentation changes |

#### Flow

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

#### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| No blockers | Zero blocker-severity friction points | Loop back to Improver |
| No painful issues | Zero painful-severity friction points | Loop back to Improver |
| Clean onboarding | Verifier completes without new friction | Loop back to Trier for full re-attempt |

#### Configuration

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

#### Database Representation

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

#### JSON Schema

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

---

## Cycle Registry

All predefined cycles in one table for quick reference:

| Cycle ID | Purpose | Roles | Quality Gate | Typical Iterations | Max |
|----------|---------|-------|--------------|-------------------|-----|
| `ui-refinement` | Visual/UX polish | 5 (Designer → UX → Dev → QA → Judge) | Score ≥ 8.5 or human override | 3–7 | 7 |
| `feature-impl` | Build new features | 5 (Researcher → Dev → QA → Judge → Human) | Score ≥ 8.0 + human approval | 2–5 | 5 |
| `roadmap-planning` | Strategic planning | 4 (Researcher → Planner → Prioritizer → Human) | Human approval | 2–4 | 4 |
| `bug-triage` | Fix bugs with proof | 5 (Reporter → Reproducer → Analyst → Fixer → Verifier) | Tests pass, no regressions | 1–3 | 5 |
| `documentation` | Write/refine docs | 5 (Researcher → Drafter → Reviewer → Editor → Publisher) | Reviewer + editor approval | 2–3 | 4 |
| `arch-review` | Architecture decisions | 5 (Analyst → Proposer → Discussant → Decider → Implementer) | Human decision + plan approved | 2–3 | 4 |
| `release` | Ship a version | 6 (Freezer → QA → Fixer → Stager → Verifier → Shipper) | Tests pass + staging verified | 1–3 | 5 |
| `onboarding-dx` | Eliminate friction | 5 (Trier → Logger → Improver → Verifier → Documenter) | Zero blockers, zero painful | 2–4 | 5 |

---

## Custom Cycles

The predefined cycles cover the most common workflows, but projects can define custom cycles. A custom cycle follows the same structure:

```json
{
  "cycle_id": "custom-cycle-name",
  "max_iterations": 5,
  "quality_gate": {
    "type": "score|human_approval|automated|compound|friction_free",
    "...gate-specific config..."
  },
  "roles": ["role-1", "role-2", "role-3"],
  "typical_iterations": [2, 4],
  "on_max_iterations": "escalate_to_human|abort|present_best_version",
  "loop_target_on_fail": "role-name"
}
```

Custom cycles must define:

1. **A unique `cycle_id`** that doesn't conflict with predefined cycles.
2. **At least two roles** — a cycle with one role is just a task.
3. **A quality gate** — without one, the cycle has no convergence mechanism.
4. **A `max_iterations` limit** — to prevent runaway loops.
5. **A `loop_target_on_fail`** — where to restart when the quality gate fails.

---

## Engine Integration

The cycle engine (in `internal/engine/`) is responsible for:

1. **Advancing state**: Moving a feature through its cycle's roles in order.
2. **Evaluating gates**: Checking quality gate conditions after each full iteration.
3. **Managing loops**: Routing back to the correct role when a gate fails.
4. **Enforcing limits**: Halting and escalating when `max_iterations` is reached.
5. **Recording history**: Creating `work_items`, `qa_results`, and `events` for every transition.

The `tillr next` CLI command queries the engine for the next pending work item across all active features, respecting cycle order and feature priority. The `tillr done` command marks work complete and advances the cycle state.

```
tillr next    →  "Feature 'auth-redesign' needs 'implement' (feature-impl, iteration 2)"
tillr done    →  advances to 'agent-qa' role
tillr next    →  "Feature 'auth-redesign' needs 'agent-qa' (feature-impl, iteration 2)"
```

This ensures agents always know exactly what to do next, and the cycle's structure is enforced regardless of which agent picks up the work.
