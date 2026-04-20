# 1. Priya — Solo PM, 50-Feature Project

**Context:** Priya is a senior engineer who built the initial prototype
of a SaaS tool solo. She's now using tillr to manage 50 features across
3 workstreams, acting as PM. She has 2-3 hours per day for PM work. She
doesn't want to read diffs — she wants to understand decisions.

**What happens today:**

The agent claims `dashboard-charts` (priority 8), implements it, submits
it. Priya comes back and sees it in human-qa. She opens the feature:

```
Feature #42  dashboard-charts     Priority 8    human-qa
  What to check:
  - Open dashboard
  - Verify charts render
  [Approve] [Reject]
```

400 lines across 6 files. She has no idea why the agent chose Chart.js
over D3, why data fetching is client-side instead of server-side, or
why there's a new `chartConfig.ts` file. She either approves blindly
or spends 20 minutes reverse-engineering decisions from the code.

**What happens with the consulting firm model:**

1. The agent claims `dashboard-charts` and starts working. As it
   implements, it comments on the ticket:

   ```
   [Implementer - 10:01am]
   Starting dashboard-charts. The spec says "charts for feature
   velocity and cycle time." I'll need a charting library — evaluating
   Chart.js vs D3 vs Recharts.

   [Implementer - 10:03am]
   Going with Recharts. Rationale: we're already using React, Recharts
   is React-native (no DOM manipulation), and the charts needed are
   standard (line, bar). D3 is overkill. Chart.js would need a React
   wrapper.

   [Implementer - 10:12am]
   Data fetching question: should I fetch chart data client-side from
   existing API endpoints, or add a dedicated /api/charts endpoint
   with pre-aggregated data?

   Client-side: simpler, no new endpoint, but N+1 queries on every
   dashboard load.
   Server-side: new endpoint, but single query, faster render.

   Going with server-side — the dashboard will be loaded frequently
   and the aggregation query is straightforward. Will add
   GET /api/stats/charts.
   ```

2. The agent submits. A review agent picks it up:

   ```
   [Reviewer - 10:18am]
   Reviewed dashboard-charts. Overall solid.

   One issue: the /api/stats/charts endpoint returns ALL chart data in
   one payload (velocity, cycle time, throughput, aging). This works
   now but will be slow when there's a year of data. Consider
   pagination or date-range params.

   Non-blocking for v1, but filing as tech debt. See new feature #73:
   "Chart endpoint date-range filtering."

   Approved with comment.
   ```

3. Priya opens her inbox. She sees:

   ```
   Feature #42  dashboard-charts     Priority 8    human-qa
     4 comments · 2 decisions · reviewed ✓

     Decisions:
       Recharts over D3/Chart.js (React-native, simpler for our needs)
       Server-side aggregation over client-side (perf at scale)

     Reviewer note:
       "Endpoint returns all data at once. Non-blocking, tracked in #73."

     What to check:
     - Open dashboard at http://localhost:5173/dashboard
     - Verify velocity and cycle time charts render with real data
     - Check empty state (new project, no data yet)
     [Approve] [Reject] [Comment]
   ```

   She knows *why* Recharts was chosen. She knows the data architecture
   decision. She knows there's a follow-up tracked. She reviews the
   charts visually and approves in 30 seconds.

   **Gap:** The inbox summary says "2 decisions" but doesn't show *which*
   alternatives were considered. Priya trusts the agent's judgment here,
   but on a more consequential decision (database choice, auth model)
   she'd want to see the rejected alternatives and why — without clicking
   into the full thread. The summary needs a "show rationale" expand.

**What would trip her up:**
- If there are 15 comments, the summary needs to be *very* good. Priya
  has 2-3 hours/day — she can't read 15-comment threads on 8 features.
  Summarization quality is load-bearing.
- If the reviewer and implementer disagree in comments, who wins? Priya
  needs to see unresolved disagreements surfaced in the inbox, not buried
  in the thread.

**What makes this work:**
- Comments accumulate during implementation, not after. The reviewer has
  context from the implementer's comments — it evaluates decisions, it
  doesn't re-derive them.
- Tech debt is automatically tracked: the reviewer's comment creates
  feature #73 without Priya lifting a finger.
- Priya reads a summary with linked decisions. The full thread is there
  if she wants it, but she doesn't need it.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
