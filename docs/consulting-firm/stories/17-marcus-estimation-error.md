# 17. Marcus — The 100x Estimation Error

**Context:** Marcus asks an agent to assess the backlog. The agent
reports that 5 of the remaining features are "complex, likely 2-4
weeks each." Marcus starts cutting scope and deferring features to a
future milestone. Then three of those features get claimed and
finished in under an hour each.

**The problem:**

Agents estimate in human-time because they were trained on human text.
"This refactor touches 15 files across 3 packages — in my experience,
that's a 2-3 week effort with testing." The agent is pattern-matching
to human engineering blog posts and project retrospectives. Its
estimate is off by **100x**.

This isn't just wrong — it **distorts decisions.** Marcus deprioritized
a feature ("too expensive for MVP"), simplified another ("just do the
basic version"), and sequenced three features serially ("too risky to
parallelize at this complexity"). All of those decisions were based on
estimates that bore no relationship to reality.

**What happens with historical estimation:**

1. Marcus asks for capacity planning:

   ```
   tillr milestone show "MVP Launch" --capacity
   ```

   ```
   MVP Launch — Capacity Planning
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

   Velocity (last 2 weeks): 8.5 features/week
   Average cycle time: 45 min (claim → done)
   Average PM time per feature: 3.2 min (review + approve)

   Remaining work:
     12 features queued (estimated: 1.4 weeks at current velocity)
     3 features in-progress
     2 features in review

   Risks:
     #102 (async-jobs) blocked on philosophy amendment
     #110 (file-upload) has no spec yet — PM action needed

   PM time budget: ~38 min for remaining 12 features
     (12 features × 3.2 min avg review time)
   ```

2. Each feature has an estimate based on similar *completed* features
   in *this* project:

   ```
   Feature #112: settings-page
     Agent self-estimate: "1-2 weeks including testing"  ← SUPPRESSED
     Tillr estimate (from history):
       Similar to: #61 (user-settings) — 35 min, 4 files, clean pass
       Similar to: #38 (user-model) — 28 min, 3 files, clean pass
       Estimate: 30-45 min agent-time
       Complexity: Standard (likely single iteration)

   Feature #113: data-export-v2
     Agent self-estimate: "3-4 weeks, significant scope"  ← SUPPRESSED
     Tillr estimate (from history):
       Similar to: #83 (export-csv) — 20 min, 2 files, clean pass
       New territory: PDF support (no prior features)
       Estimate: 45-90 min agent-time
       Complexity: Standard-Complex (may need 1 review iteration)
   ```

3. Marcus sees that the "2-4 week" features are actually 30-90 minute
   features. He un-defers the feature he cut. He restores the full
   scope on the one he simplified. He runs three "risky" features in
   parallel.

   The milestone finishes a week ahead of schedule instead of slipping
   two weeks.

**What would trip him up:**
- Historical estimation needs enough data to be useful. If the project
  has only 5 completed features, the analogies are weak. The system
  should show confidence: "Estimate based on 3 similar features
  (moderate confidence)" vs "No similar features found (low confidence,
  using project-wide average)."
- Similarity matching is doing real work here. "Similar to #61" — but
  what makes them similar? Tag overlap? File count? Description
  embedding? Marcus should be able to see *why* the system thinks these
  features are comparable.

**What makes this work:**
- Agent self-estimates are never surfaced to the PM. They're unreliable
  by design.
- Historical data from *this project* is the estimator.
- PM time is a separate line item — Marcus plans his review time, not
  the agents' work.
- Complexity buckets (Quick/Standard/Complex) are more useful than
  point estimates.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
