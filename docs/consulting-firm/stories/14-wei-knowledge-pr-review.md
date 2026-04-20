# 14. Wei — Reviewing Knowledge Base Changes

**Context:** Wei manages a project where feature #38 established the
canonical data model pattern three weeks ago. Since then, the pattern
has evolved through 6 features. Wei notices agents are still following
the original pattern — the synthesized knowledge is stale.

**What happens:**

1. The knowledge synthesis agent runs (triggered by 5 new reviews since
   last synthesis):

   ```
   Knowledge PR #7
   ━━━━━━━━━━━━━━━

   CHANGES TO PROJECT KNOWLEDGE:

   UPDATED: "Data model pattern"
     OLD: "Follow the pattern from #38: model struct in internal/db/models.go,
           queries in internal/db/{entity}.go"
     NEW: "Follow the pattern from #38, refined through #61 and #78:
           - Model struct in internal/db/models.go
           - Queries in internal/db/{entity}.go
           - Always include CreatedAt, UpdatedAt timestamps
           - Always add a migration in internal/db/migrate/
           - Include a List function with pagination (limit/offset)
           The pagination pattern was established in #61 and is now
           mandatory — reviewer rejected non-paginated lists on #72."

   ADDED: "WebSocket patterns"
     Source: Feature #30 (implementer + reviewer)
     "Hub at internal/server/ws/hub.go. Use ws.Message struct.
      Broadcast via hub.Broadcast(), targeted via hub.SendTo().
      Don't close from write side (race). Test with wstest helper."

   REMOVED: "Avoid global state"
     Reason: Superseded by more specific guidance. The original
     rejection (#45) was about mutable package vars, not all globals.
     Replaced with: "No mutable package-level variables. Constants
     and init-time configuration are fine."

   FRESHNESS:
     Data model pattern: confirmed in 6 recent features ✓
     WebSocket patterns: confirmed in 2 features ✓
     Anti-pattern update: based on re-analysis of #45 rejection
   ```

2. Wei sees the knowledge PR in her inbox:

   ```
   Inbox — 1 knowledge update

     Knowledge  7 changes to project knowledge base
       3 updated · 2 added · 1 removed · 1 refined
       Sources: 8 recent features analyzed
       [Approve] [Modify] [Reject]
   ```

3. She scans the changes. The data model refinement looks right — she
   remembers the pagination rejection on #72. The WebSocket patterns
   are from an area she doesn't know well, but the sources check out.
   The "avoid global state" refinement is more precise — good.

   She clicks [Modify] on one item: "Add to the WebSocket patterns
   that we use JSON encoding, not protobuf. An agent tried protobuf
   on #92 and I rejected it."

   The knowledge updates with her modification. Future agents get the
   refined knowledge base.

**What would trip her up:**
- Wei modified one item. But did the agent apply her modification
  correctly? She'd want to see the final state after her edit, not just
  trust that it merged. A "preview after modification" step is needed.
- Knowledge PRs are less intuitive than code PRs. "REMOVED: Avoid
  global state" sounds alarming if you don't read the replacement.
  The diff format needs to make it clear this is a refinement, not a
  deletion of a safety rule.

**What makes this work:**
- Knowledge isn't just accumulated — it's curated through review.
- The PM can modify agent-generated knowledge. The synthesis is a
  starting point, not the final word.
- Stale patterns get updated or removed. The "avoid global state"
  anti-pattern was too broad — the refined version is actionable.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
