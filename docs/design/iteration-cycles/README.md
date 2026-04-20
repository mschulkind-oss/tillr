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

The eight predefined cycles are catalogued in [cycles/README.md](./cycles/README.md), with one file per cycle.

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

« [Design docs](../README.md)
