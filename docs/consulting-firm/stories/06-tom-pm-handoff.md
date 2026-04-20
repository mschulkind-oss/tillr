# 6. Tom — New PM, Taking Over a Running Project Mid-Stream

**Context:** Tom is taking over PM duties from Rachel on a project with
38 completed features, 12 in the queue, and 3 in flight. He's never
seen the codebase. He has one afternoon to get up to speed before Rachel
leaves for vacation.

**What happens today:**

Rachel gives him a brain dump over Zoom. He takes notes. He forgets
half of it. He spends a week re-discovering context by reading every
feature's spec and git history.

**What happens with the consulting firm model:**

1. Tom opens the tillr dashboard. He sees:

   ```
   Active features: 8
   In review: 3
   Blocked: 1 (waiting on external API key)
   Done this week: 5
   QA rejection rate: 15% (down from 22% last week)
   ```

2. He clicks into the 3 features in review. Each has a comment thread
   with full context. He reads the threads — not the code — and
   understands what each feature does, what decisions were made, and
   what the reviewer flagged.

3. He searches for recent decisions:

   ```
   tillr search --type decision --since 2w
   ```

   ```
   #42 login-page: session tokens over JWT (PM: "JWT can wait")
   #51 api-errors: simple format over RFC 7807 (PM: "not a public API")
   #67 permissions: three roles, middleware-based (team consensus)
   #72 admin-panel: 403 handling added after review (reviewer catch)
   ```

4. He checks the active philosophies:

   ```
   tillr philosophy list
   ```

   ```
   1. Simplicity over comprehensiveness (active since week 1)
   2. No external dependencies for MVP (amended week 8: embedded OK)
   3. Every UI action should be reversible (active since week 1)
   ```

5. In 30 minutes, Tom has the context that would take a week to
   reconstruct from code. He's ready to make decisions.

   **Gap:** Tom can see *what* decisions were made but not *why Rachel
   made them*. The PM approval comments are often just "Approved" — no
   reasoning. The system should prompt PMs to add a brief rationale
   when approving non-trivial decisions. Without it, Tom has the
   decisions but not the judgment behind them.

**What would trip him up:**
- 38 completed features is a lot of history. Tom needs a "project brief"
  command — a curated summary of the major decisions, active
  philosophies, and current state. Not a raw search, but a narrative.
- If Rachel's PM preferences are implicit (derived from correction
  patterns), Tom inherits them silently. He might disagree with "minimal
  implementations preferred" but not realize it's a synthesized
  preference driving agent behavior. Synthesized preferences need to be
  visible and overridable.

**What makes this work:**
- Comment threads are the institutional memory. Not a wiki that nobody
  updates, not Slack that scrolls away.
- Searchable decisions. Tom can find every decision made in the last
  two weeks without reading every ticket.
- Philosophies are explicit. Tom sees the strategic constraints in 3
  lines.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
