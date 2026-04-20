# 19. Jake — The Automated Retrospective

**Context:** Every two weeks, Jake gets an automated retrospective. No
meeting, no ceremony — just data and recommendations filed as PRs.

**What happens:**

1. The retro agent runs on schedule:

   ```
   Retrospective — Sprint 4 (Mar 29 – Apr 11)
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

   VELOCITY
     Features completed: 17 (up from 14 last sprint)
     Avg cycle time: 42 min (down from 51 min)
     PM review time: 2.8 min/feature (down from 3.2 min)

   QUALITY
     First-pass approval rate: 71% (up from 58%)
     Reviewer catch rate: 89% (issues caught before PM sees them)
     Zero regressions introduced

   WHAT WENT WELL
     - Design review step (added sprint 3) reduced design rejections
       from 42% to 8%
     - Context graph assembly reduced "didn't know about X" rejections
       from 15% to 2%
     - Philosophy citations in comments — agents now explain WHY, not
       just WHAT, in 90% of decision comments

   WHAT NEEDS ATTENTION
     - Frontend features still 2.5x slower than backend (avg 68 min
       vs 27 min). Consider: pre-built component templates?
     - Three features required >3 iterations before approval.
       Common thread: ambiguous specs. Proposal: add a spec-review
       step for features with priority ≥ 8
     - Knowledge base has 4 stale entries (source features >30 days
       old, patterns may have evolved)

   RECOMMENDATIONS (filing as PRs)
     1. Cycle template PR: Add spec-review step for high-priority
        features
     2. Knowledge PR: Refresh 4 stale entries
     3. Philosophy PR: Add "Specs must include acceptance criteria
        for features priority ≥ 8"
   ```

2. Jake sees three PRs in his inbox. Each backed by data. He reviews
   and approves the ones he agrees with.

3. Sprint 5 starts with an improved cycle template, refreshed
   knowledge, and a new philosophy about spec quality. No retro
   meeting happened. The system improved itself.

**What would trip him up:**
- Three PRs every two weeks is manageable. But if the retro agent
  files 10 recommendations per sprint, it becomes noise. The agent
  needs a "significance threshold" — only file PRs for changes with
  meaningful expected impact (>10% improvement on some metric).
- The retro recommendations are correlations, not causations. "3
  features took >3 iterations" and "ambiguous specs" might be
  coincidence. Jake should be able to drill into each recommendation's
  evidence before approving.

**What makes this work:**
- Retrospectives are automated and data-driven. Actual metrics, not
  feelings.
- Recommendations are actionable PRs that go through the review
  pipeline — not action items that get forgotten.
- The flywheel: retro → PR → approval → better cycles → better
  features → better retro data.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
