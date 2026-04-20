# 23. The Context Graph Fails — A Feature Slips Through

**Context:** Feature #115 (`audit-log-retention`) touches the events
table. Three prior features (#55, #78, #88) also touch the events
table, but #115 is tagged `infrastructure` while the others are tagged
`notifications` and `monitoring`. The context graph doesn't connect
them because they share no tags, no explicit dependencies, and no
cross-feature comments.

**What goes wrong:**

1. Agent claims #115. The context packet is thin:

   ```
   Context for Feature #115: audit-log-retention
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

   STRATEGIC CONTEXT
     Workstream: Infrastructure

   CONSTRAINTS
     (none specific to this feature)

   RELATED WORK
     (none found — no tag overlap, no cross-refs, no shared features)
   ```

2. The agent implements a retention policy that deletes events older
   than 90 days. It doesn't know that #78 (notification-preferences)
   subscribes to event bus topics that reference historical events,
   or that #88's stats queries aggregate across all events for
   velocity calculations.

   ```
   [Implementer - 10:00am]
   Implementing audit-log-retention. Adding a background job that
   runs daily, deletes events older than 90 days. Straightforward
   DELETE FROM events WHERE created_at < datetime('now', '-90 days').
   ```

3. The agent submits. Reviewer approves — it doesn't know about the
   downstream dependencies either.

4. Priya approves. The next morning, the velocity chart shows a cliff —
   historical data is gone. The stats queries return zero for months
   1-3. The notification system tries to reference events that no
   longer exist.

   ```
   Incident #120  Data loss: audit retention deleted live event data
     Root cause: Feature #115 deleted events used by features #78,
     #88. Context graph had no edge between infrastructure and
     notifications — they share the events table but have no tag
     or comment overlap.

     Post-mortem: The context graph missed a file-level dependency.
     #115, #78, and #88 all write to or read from the `events` table,
     but this relationship was invisible because it exists in code,
     not in comments or tags.
   ```

**What went wrong:**
- The context graph only knows about relationships that are *declared*
  (tags, dependencies) or *discussed* (cross-feature comments). It
  doesn't analyze code. The events table dependency exists only in SQL
  queries and Go function calls — invisible to the graph.
- The reviewer didn't catch it because the reviewer also lacks code-level
  awareness of cross-feature dependencies.
- Priya approved because the comment thread and reviewer both said it
  was clean.

**What this reveals:**
- The context graph has a blind spot: **shared resources at the code
  level.** Two features that touch the same database table, the same
  config file, or the same API endpoint are related even if no one
  says so in a comment.
- This is the argument for "shared files" edge detection in the graph
  (mentioned as a gap in story #10). Without it, the graph is only
  as good as the conversations — and conversations don't cover
  everything.
- It also reveals why automated tests matter alongside the consulting
  firm model. If there were integration tests that exercised the stats
  queries with realistic data, the retention job would have failed
  the test suite.

**Gap:** The fix is conceptually simple — add file-level and table-level
dependency edges to the graph, derived from `git log` analysis or static
analysis of SQL queries. But the implementation is non-trivial: which
files matter? Every feature touches `go.sum`. The signal-to-noise ratio
of file-level edges needs careful tuning.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
