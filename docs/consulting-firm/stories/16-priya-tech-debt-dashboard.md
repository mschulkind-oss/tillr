# 16. Priya — Tech Debt Dashboard, 50 Features In

**Context:** Over 50 features, agents have flagged 12 tech debt items
in their comment threads. These aren't bugs — they're known compromises
that need future attention. Priya wants to see the full picture.

**What happens:**

1. Throughout normal work, agents have been flagging debt:

   ```
   #42 Implementer: "...Filed #58 for JWT migration (tech debt)"
   #55 Reviewer: "Event bus has no backpressure. Fine for now, will
   need it at scale. Tech debt."
   #78 Implementer: "Hardcoded notification types. Should be
   configurable. Tech debt for later."
   #88 Reviewer: "Stats queries are O(n) scans. Need indexes when
   data grows. Tagging as tech debt."
   ```

2. Tillr tracks all items tagged `tech-debt`. Priya opens the debt
   dashboard:

   ```
   tillr debt
   ```

   ```
   Tech Debt — 12 items
   ━━━━━━━━━━━━━━━━━━━━

   Critical (blocking features):
     #58  JWT migration         Source: #42 (reviewer flagged)
          Blocks: mobile client support
          Age: 6 weeks

   Growing (gets worse over time):
     #106 Event bus backpressure  Source: #55 (reviewer flagged)
          Affected: 4 features use event bus
          Risk: message loss under load

     #107 Stats query indexes     Source: #88 (reviewer flagged)
          Affected: dashboard load time
          Risk: O(n) degrades with data growth

   Cosmetic (annoying but stable):
     #108 Hardcoded notification types  Source: #78 (implementer)
     #109 Duplicate validation logic    Source: #62 (reviewer)
     ...7 more
   ```

3. The categorization (critical/growing/cosmetic) came from an agent
   that analyzed each debt item's characteristics: does it block other
   features? Does it degrade over time? Is it isolated?

4. Priya decides to address the top 3 this milestone. She bumps their
   priority and they enter the normal feature queue.

**What would trip her up:**
- The debt categorization is agent-generated. If an agent categorizes
  something as "cosmetic" that's actually "growing," Priya might ignore
  it until it causes a problem. She needs to be able to re-categorize.
- Some debt items are redundant — #107 (stats indexes) might be
  automatically resolved by #113 (data-export-v2) if that feature
  rewrites the queries. Cross-referencing debt items with planned
  features would show "this debt will be addressed by #113."

**What makes this work:**
- Debt isn't a separate tracking system. It's features with the
  `tech-debt` tag, discovered organically through normal work.
- Agents flag debt as they encounter it, not as a separate audit.
- Categorization helps the PM prioritize — not all debt is equal.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
