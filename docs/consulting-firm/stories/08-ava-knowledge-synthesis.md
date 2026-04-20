# 8. Ava — PM for a Mature Project, 40 Features of History

**Context:** Ava's project has been running for three weeks with 40
completed features. She's noticed that early mistakes keep recurring —
agents hand-roll error handling, over-engineer config, use wrong naming
conventions. Each correction costs a rejection cycle and 3 minutes of
her time.

**What happens without institutional knowledge:**

Every new agent session starts cold. Agent #41 makes the same mistake
agent #5 made — hand-rolling error handling instead of using the
middleware. Agent #42 over-engineers a config system for something that
should be a constant. Ava has corrected these patterns 6 times. Each
correction is identical.

**What happens with agent onboarding:**

1. Agent claims feature #83: `export-csv`. Tillr assembles the context
   packet, which now includes a **project knowledge** section synthesized
   from 40 features of review history:

   ```
   PROJECT KNOWLEDGE (synthesized from 40 completed features)
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

   PATTERNS (do this)
     API endpoints: Use the handler → validate → service → respond
     pattern from #38. Error middleware wraps automatically.
     (Source: reviewer approved this pattern 12 times, corrected
     deviations 3 times)

     Data access: All DB queries go through service functions in
     internal/db/. No raw SQL in handlers.
     (Source: rejected on #14, #29 — corrected to service pattern)

     New tables: Follow the migration pattern in internal/db/migrate/.
     Naming: snake_case singular. Always add an index on created_at.
     (Source: established in #8, reinforced in reviewer comments on
     #22, #38, #61)

   ANTI-PATTERNS (don't do this)
     Global state: Rejected on #45. PM: "Use dependency injection.
     No package-level vars for stateful things."

     Over-engineering: Rejected on #33, #52, #71. PM pattern: "Start
     with the simplest thing. Constants over config files. Direct
     calls over abstractions. Add complexity when there's a concrete
     need, not before."

     Custom error handling: Rejected on #14. Error middleware exists
     (internal/server/middleware.go). Don't add try/catch or custom
     error formatting in handlers.

   PM PREFERENCES
     Minimal implementations preferred (data: 3 rejections for
     over-engineering vs 0 rejections for under-engineering)
     No email integration until provider is chosen
     Accessibility matters — reviewer checks for aria labels
   ```

2. The agent reads this and adjusts its approach before writing a line
   of code:

   ```
   [Implementer - 11:00am]
   Claiming export-csv. Reading project knowledge brief.

   Approach based on established patterns:
   - New endpoint: GET /api/features/export?format=csv
   - Handler → service function (no raw SQL in handler)
   - Service function in internal/db/features.go (existing file)
   - No config for export format — just CSV for now (keeping it
     simple per PM preference, not building a pluggable export system)
   - Error handling via existing middleware — no custom error logic
   ```

3. Clean implementation. Reviewer has minimal notes:

   ```
   [Reviewer - 11:20am]
   Reviewed export-csv. Follows all established patterns.
   Handler → service → response. No custom error handling.
   Simple implementation — just CSV, no format abstraction.

   One note: add Content-Disposition header so the browser downloads
   instead of displaying. Minor.

   Approved.
   ```

4. Ava sees a feature that went from claim to approval in 20 minutes
   with zero PM involvement beyond the final approve click.

**What would trip her up:**
- The synthesized knowledge says "3 rejections for over-engineering."
  But what if Ava *wants* a complex implementation for one specific
  feature? She'd need to override the synthesized preference in the
  spec: "For this feature, ignore the simplicity preference — I want
  a full plugin system." The spec needs to be able to override project
  knowledge.
- If the synthesis is wrong (misinterprets a rejection reason), it
  propagates to every future agent. Ava needs to review synthesized
  knowledge periodically — the knowledge PR flow (story #14) handles
  this, but she has to actually read them.

**What makes this work:**
- Project knowledge is synthesized from 40 features of actual review
  history — not a static doc someone wrote once.
- Anti-patterns come from real rejections. "Don't over-engineer" isn't
  a vague guideline — it's backed by 3 specific rejections with
  examples.
- The agent self-corrected before submitting. No rejection cycle, no
  PM time spent.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
