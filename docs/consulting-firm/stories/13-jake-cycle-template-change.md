# 13. Jake — Process Improvement Driven by Data

**Context:** Jake has been running the default implement→review→human-qa
cycle for 30 features. He's not unhappy, but he wonders if the process
could be better. He doesn't know where the friction is.

**What happens:**

1. An agent running the weekly retrospective analysis produces a report:

   ```
   [Retro Agent - Monday 9:00am]
   Weekly process analysis — last 12 features:

   Rejection analysis:
     Code quality:   2/12 rejected (17%) — caught by reviewer agent
     Design/UX:      5/12 rejected (42%) — NOT caught by reviewer
     Spec gaps:      1/12 rejected (8%)  — spec was ambiguous

   Pattern: Reviewer agents catch code issues effectively but miss
   design issues. The human PM catches design problems at human-qa,
   causing a full rejection cycle.

   Proposal: Add a design review step between implement and code
   review. A design-focused agent checks layout, UX flow, and
   consistency with existing pages BEFORE the code reviewer runs.

   Filing as cycle template PR.
   ```

2. The cycle template PR:

   ```
   Cycle Template PR #1
   ━━━━━━━━━━━━━━━━━━━━

   CURRENT: implement → review → human-qa
   PROPOSED: implement → design-review → code-review → human-qa

   NEW STEP: design-review
     Agent role: Design Reviewer
     Checks: layout consistency, UX flow, responsive behavior,
             accessibility, visual match to existing pages
     Tools: Chrome DevTools MCP (screenshot comparison)
     Gate: pass/fail with specific feedback

   RATIONALE:
     42% of human-qa rejections are design issues. These cost a full
     rejection cycle (human time + agent rework). Catching them one
     step earlier saves PM time and reduces cycle count.

   ESTIMATED IMPACT:
     Expected rejection rate at human-qa: ~17% (from current 42%)
     Added cost per feature: ~2 min agent time for design review
     Net savings: ~60% fewer PM rejection interactions
   ```

3. Jake reviews:

   ```
   Inbox — 1 process change

     Cycle Template  Standard cycle: add design-review step
       Data: 42% of rejections are design issues
       Impact: 60% fewer PM rejections expected
       [Approve] [Reject] [Comment]
   ```

   He approves. The next feature queued gets the new cycle automatically.

   **Gap:** Jake approved based on the retro agent's analysis. But the
   analysis could be wrong — maybe the 5 "design rejections" were
   actually spec gaps that manifested as design issues. The retro agent
   categorized based on rejection comments, but PM rejection comments
   are often ambiguous ("this doesn't look right" — is that design or
   spec?). Tillr should let Jake drill into the raw data behind the
   recommendation.

**What would trip him up:**
- Cycle template changes affect all future features. If the design
  review step is bad (too slow, catches nothing), Jake needs to revert.
  There should be a "trial period" option: run the new template for 10
  features, then auto-generate a comparison report.
- The retro agent runs weekly. If a process problem is urgent (50%
  rejection rate spike), waiting a week is too long. Event-triggered
  retros (e.g., "3 rejections in a row") should supplement scheduled
  ones.

**What makes this work:**
- Process improvement driven by data, not intuition.
- The cycle template change goes through the same PR process as code.
  Jake approved it explicitly.
- The impact is quantified: "42% of rejections" and "60% fewer
  interactions."

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
