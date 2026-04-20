# 11. Nora — First-Time User, Setting Up Philosophies Day One

**Context:** Nora is starting a new project from scratch. She's read
about tillr's consulting firm model and wants to set it up right. She
has 3 core principles she wants every agent to follow, and a backlog
of 15 features she's ready to queue. She's done zero features so far.

**First 10 minutes:**

1. Nora creates her project and adds her philosophies:

   ```
   tillr philosophy add "Simplicity over comprehensiveness — start with
   the narrowest useful implementation. Add features when there's a
   concrete need, not a hypothetical one."

   tillr philosophy add "No external service dependencies for MVP —
   everything runs in-process or uses SQLite. Zero ops overhead."

   tillr philosophy add "Every UI action should be reversible — users
   must be able to undo. If an action is destructive, confirm first
   and keep a soft-delete for 30 days."
   ```

2. She queues her first feature: `batch-import`. An agent claims it.
   The context packet includes:

   ```
   DRIVING PHILOSOPHIES (apply to ALL work)
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   1. Simplicity over comprehensiveness
      Start with the narrowest useful implementation.
   2. No external dependencies for MVP
      Everything runs in-process or uses SQLite.
   3. Every UI action should be reversible
      Undo, confirm destructive actions, soft-delete 30 days.

   PROJECT KNOWLEDGE
     (No patterns established yet — this is the first feature.)
   ```

3. The agent's approach shifts immediately:

   ```
   [Implementer - 9:00am]
   Claiming batch-import. Reviewing driving philosophies.

   Per philosophy #1 (simplicity): No configurable column mapping.
   Import expects a fixed CSV format matching our schema. We can
   add mapping later if users need it.

   Per philosophy #2 (no external deps): No Redis queue. Process
   imports synchronously for now. For large files (>1000 rows),
   process in chunks within a single HTTP request with progress
   streamed via SSE.

   Per philosophy #3 (reversibility): Import creates records with
   an import_batch_id. User can undo an entire import (soft-delete
   all records in the batch) from the UI.
   ```

4. Nora reviews a feature that aligns with her vision on the first try:

   ```
   Feature #1  batch-import     human-qa
     3 comments · reviewed ✓

     Philosophy alignment:
       #1 simplicity: fixed CSV format, no configurability ✓
       #2 no external deps: synchronous, in-process ✓
       #3 reversibility: batch undo via import_batch_id ✓

     [Approve] [Reject] [Comment]
   ```

**What would trip her up:**
- Nora's philosophies are prose. "Simplicity over comprehensiveness" is
  clear to her, but agents might interpret it differently. Agent A reads
  it as "no tests" (simpler!), Agent B reads it as "no abstractions."
  Philosophies need examples or anti-examples: "Simplicity means: prefer
  constants over config files, prefer direct calls over abstractions.
  It does NOT mean: skip tests, skip error handling, or skip validation."
- On the first feature, there's no project knowledge. The context packet
  is thin. Nora should expect the first 3-5 features to require more
  hands-on review as patterns are established. The system should tell
  her: "Project knowledge builds after ~5 completed features. Expect
  higher review effort initially."

**What makes this work:**
- Philosophies prevented three wrong decisions before they happened.
  No Redis. No configurable mapping. Undo built in from the start.
- The agent *cited* the philosophies in its reasoning. Nora can see
  exactly which principle drove which decision.
- This scales: every agent, every feature, same strategic alignment
  without Nora repeating herself.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
