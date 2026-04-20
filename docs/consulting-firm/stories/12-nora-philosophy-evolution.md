# 12. Nora — When a Philosophy Needs to Evolve

**Context:** It's week 8. The "no external dependencies" philosophy is
causing friction. Three features are compromised by it. An agent
proposes an amendment.

**What happens:**

1. Agent claims `async-job-processing` and hits a wall:

   ```
   [Implementer on #102 - 2:00pm]
   Blocked on philosophy #2: "No external dependencies for MVP."

   This feature genuinely needs async processing. Users upload large
   files and can't wait for synchronous processing. Options within
   the constraint:
   - Goroutine pool (in-process): works but loses jobs on restart
   - SQLite-based job queue: durable but polling-based, janky

   Both options are worse than using an embedded queue. Proposing a
   philosophy amendment.
   ```

2. The agent files a philosophy PR:

   ```
   Philosophy PR #3
   ━━━━━━━━━━━━━━━━

   CURRENT:
     "No external service dependencies for MVP — everything runs
      in-process or uses SQLite. Zero ops overhead."

   PROPOSED:
     "No external service dependencies for MVP — everything runs
      in-process or uses SQLite. Exception: embedded libraries that
      run in-process (e.g., embedded NATS, embedded Redis) are OK
      when in-process alternatives are insufficient."

   RATIONALE:
     Features blocked or compromised by current constraint:
       #90  async-job-processing (this feature)
       #94  webhook-delivery (needs reliable retry)
       #102 file-processing-pipeline (needs durable queue)

     Embedded NATS runs as a Go library — no separate process, no
     ops overhead, no network dependency. It satisfies the spirit
     of the philosophy (zero ops) while relaxing the letter.

   IMPACT:
     Affects all future features. No impact on completed features.
     Three in-progress features would unblock immediately.
   ```

3. Nora sees the philosophy PR in her inbox alongside code PRs:

   ```
   Inbox — 4 items

     Code PR      #88: notification-feed           [Merge] [Reject]
     Code PR      #91: dashboard-filters           [Merge] [Reject]
     Philosophy   "No external deps" amendment      [Approve] [Reject]
     Code PR      #93: export-pdf                  [Merge] [Reject]
   ```

4. She reads the rationale. Three features blocked. Embedded NATS is
   in-process. The spirit of the philosophy (zero ops) is preserved.
   She approves with a comment:

   ```
   [PM - 2:20pm]
   Approved. Good call on embedded NATS — keeps the ops story clean.

   One constraint: document which embedded libraries we're using and
   why, so we don't end up with five different embedded systems.
   ```

5. The philosophy updates. All three blocked features get the updated
   context on their next agent interaction. The history shows:
   - v1 (week 1): No external dependencies
   - v2 (week 8): Exception for embedded libraries (PR #3, PM approved)

**What would trip her up:**
- If Nora rejects the philosophy PR, those 3 features stay blocked.
  The agents need a way to work within the constraint creatively, or
  the features need to be descoped. The rejection flow should prompt:
  "3 features are blocked by this philosophy. [Descope them] [Find
  workaround] [Approve amendment]."

**What makes this work:**
- The philosophy evolved through data, not gut feel. Three blocked
  features made a concrete case.
- The amendment went through review — Nora explicitly approved the
  change to the project's strategic direction.
- Version history means anyone can trace why the philosophy changed.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
