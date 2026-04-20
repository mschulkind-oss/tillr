# 5. Rachel — Non-Technical PM, Delegates Everything Technical

**Context:** Rachel manages a project but doesn't write code and doesn't
read diffs. She has a product background — she knows *what* users need
but not *how* to build it. She wants agents to propose, discuss, and
bring her recommendations. She approves outcomes, not implementations.

**What happens:**

1. Rachel creates a feature with a deliberately open spec:

   ```
   tillr add "Reporting data model" --spec "Design the data model for
   project-level reporting. We need: feature velocity (features
   completed per week), cycle time (claim to done), agent utilization
   (% time in active work), and QA rejection rate. Propose the schema
   and any new queries. Two agents: one designs, one reviews the
   design. Bring me a recommendation."
   ```

2. The design agent claims it and proposes:

   ```
   [Designer - 3:00pm]
   Proposed schema for reporting:

   Option A: Materialized views
   - Create report_velocity, report_cycle_time views
   - Refresh on feature status change
   - Pro: simple queries, fast reads
   - Con: stale data between refreshes, extra storage

   Option B: Compute on read
   - Query features/work_items/events tables directly
   - Aggregate in SQL with window functions
   - Pro: always fresh, no extra tables
   - Con: slow on large datasets (>10k features)

   Option C: Hybrid
   - Compute on read for recent data (last 30 days)
   - Materialized summary for historical (older than 30 days)
   - Pro: fresh recent data, fast historical queries
   - Con: more complex, need a refresh job for historical

   Recommending Option B for now. We have <500 features. The queries
   are fast. We can add materialization when we hit performance issues.
   YAGNI.
   ```

3. The reviewer agent responds:

   ```
   [Reviewer - 3:08pm]
   Agree with Option B. Couple of additions:

   1. The cycle time query needs to handle features that were rejected
      and re-implemented. Use the FIRST claim time and LAST done time,
      not just the latest cycle.

   2. Agent utilization: define "active work" as time between claim
      and submit (not done — that includes human QA wait time). Use
      work_items timestamps.

   3. QA rejection rate: count features with status that went from
      human-qa → implementing (rejection) vs human-qa → done (approval).
      This is in the events table.

   Proposed query sketches:
   [SQL queries follow]

   Recommending Option B with these refinements. Ready for PM review.
   ```

   **Gap:** Rachel doesn't know SQL. The "proposed query sketches" are
   meaningless to her. She needs the recommendation framed in outcomes:
   "reports will be real-time, no extra setup, and we can optimize later
   if it gets slow." The agent comments need audience awareness — PM
   comments should be non-technical, reviewer/implementer comments can
   be technical. There's no mechanism for this today.

4. Rachel sees:

   ```
   Feature #85  reporting-data-model    human-qa
     5 comments · 1 design proposal · 1 review

     Recommendation: Compute on read (Option B)
     "Simple queries against existing tables. No new schema needed.
      Three options explored, team agreed on simplest."

     Reviewer additions:
     - Cycle time: first claim → last done (handles rejections)
     - Utilization: claim → submit (excludes QA wait)
     - Rejection rate: from events table transitions

     [Approve] [Reject] [Comment]
   ```

5. Rachel reads the summary, sees the team agreed, approves. She didn't
   design anything — she delegated the entire discussion and received a
   recommendation with rationale.

**What would trip her up:**
- If the designer and reviewer *disagree*, Rachel gets an unresolved
  conflict she's not equipped to resolve technically. She needs the
  agents to resolve it or present the trade-off in non-technical terms.
- Rachel can't evaluate "Option B is fast enough." She has to trust the
  team. The system should surface when trust is warranted ("these two
  agents agreed, reviewer has 89% catch rate") and when it's not ("this
  is new territory, no prior work to validate against").

**What makes this work:**
- Rachel asked the team to "figure it out." They did.
- The designer proposed options with trade-offs. The reviewer refined.
  Rachel received a recommendation, not a question.
- The full discussion is preserved. If someone later asks "why not
  materialized views?" the answer is in the thread.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
