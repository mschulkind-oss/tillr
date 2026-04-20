# 15. Carlos — Incident Response, Something Just Broke

**Context:** Carlos ships a feature and the dashboard breaks. The
deployed version has a regression — the stats endpoint returns 500.
He needs this fixed now, not through the normal queue.

**What happens today:**

Carlos notices the bug, manually files a feature, waits for an agent
to claim it, explains the urgency in the spec. By the time it's fixed,
he's done 30 minutes of context-switching and hand-holding.

**What happens with the consulting firm model:**

1. Carlos files an urgent feature:

   ```
   tillr add "Dashboard regression: chart data endpoint returns 500
   after merge of #88" --priority 10 --tag incident --tag regression
   ```

2. Tillr recognizes the incident pattern (priority 10 + incident tag)
   and triggers an accelerated cycle:

   ```
   Incident cycle: diagnose → fix → verify → human-qa
   (No design step. No agent review. Straight to fix.)
   ```

3. An agent claims immediately and starts diagnosis:

   ```
   [Diagnostician - 3:00pm]
   Incident: dashboard 500 after merge of #88.

   Diagnosis:
   - git bisect between main~3 and main: regression in commit abc123
   - Commit abc123 (from feature #88) changed the stats query
   - The query references a column that was renamed in #88's migration
     but the stats endpoint wasn't updated
   - Root cause: stats endpoint at internal/api/stats.go:142 uses
     old column name "created" instead of new name "created_at"

   Cross-referencing: Feature #88 (notification-feed) changed the
   migration but the implementer's comment thread doesn't mention
   the stats endpoint. The reviewer didn't catch the dependency.

   Fix: one-line change in internal/api/stats.go:142.
   Filing and implementing.
   ```

4. Fix is immediate. Verify step runs the test suite. Carlos sees:

   ```
   Incident #105  dashboard-500-regression    human-qa
     Diagnosed in 3 min. Fixed in 1 min. Tests pass.

     Root cause: Feature #88 renamed a column, stats endpoint
     wasn't updated.

     Post-mortem note: Reviewer on #88 should have caught this.
     Context graph should include "all queries that reference
     columns in modified tables" as related context.

     [Approve] [Reject]
   ```

5. Carlos approves. The post-mortem note becomes a process improvement
   signal — the context graph should surface column-level dependencies.

   **Gap:** The incident cycle skips agent review. For a one-line fix,
   that's fine. But what if the diagnosis is wrong and the "fix"
   introduces a second regression? Incident fixes that touch more than
   N files or N lines should still get a quick agent review. The
   accelerated cycle needs a complexity gate.

**What would trip him up:**
- Carlos filed the incident manually. In a real scenario, he might
  discover the regression from a user report, a failed health check, or
  a test suite. Automated incident creation (from monitoring or CI
  failure) would eliminate the manual step.
- The post-mortem note is a comment on the incident feature. But the
  process improvement (add column dependency checks to context graph)
  is a systemic change. It should generate a concrete PR, not just a
  note that might be forgotten.

**What makes this work:**
- Incident cycle is faster — no design or review step.
- Diagnosis is structured: bisect, root cause, cross-reference to the
  feature that introduced the regression.
- The post-mortem feeds back into the system. The reviewer's checklist
  improves for next time.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
