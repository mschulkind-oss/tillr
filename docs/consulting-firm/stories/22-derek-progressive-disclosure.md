# 22. Derek — Just the Comments, Nothing Else

**Context:** Derek heard about tillr from a colleague. He doesn't want
the full consulting firm model — no philosophies, no knowledge
synthesis, no automated retros, no specialization. He just wants to see
what his agents are doing. He has 8 features and one agent running at a
time. He uses the CLI exclusively; he hasn't opened the web UI.

**The minimal workflow:**

1. Derek queues a feature:

   ```
   tillr add "Add search to feature list" --priority 5
   ```

2. An agent claims it. Derek doesn't set up any philosophies or domain
   tags. The context packet is minimal:

   ```
   Context for Feature #8: add-search
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

   SPEC
     "Add search to feature list"

   PROJECT KNOWLEDGE
     (5 completed features — 2 established patterns)

   No philosophies configured.
   No related features flagged.
   ```

3. The agent implements and comments:

   ```
   [Implementer - 4:15pm]
   Adding search to feature list page. Using the existing List
   endpoint with a new ?q= query parameter. Filtering by title
   and description, case-insensitive. SQLite LIKE query.
   ```

4. Derek checks the feature:

   ```
   tillr show 8 --comments
   ```

   ```
   Feature #8  add-search    human-qa
     1 comment by implementer

     [Implementer - 4:15pm]
     Adding search to feature list page. Using existing List
     endpoint with ?q= parameter. SQLite LIKE query.

     [Approve] [Reject]
   ```

5. Derek reads the one comment, tries the search, approves. Done.

   He didn't configure philosophies, didn't set up roles, didn't
   review knowledge PRs. Comments alone gave him the visibility
   he was missing.

**This is Layer 1.** Everything else builds on top:
- Layer 2: Agent comments in cycles (automatic, not manual)
- Layer 3: Cross-feature communication (`#42` detection)
- Layer 4: PM mid-flight comments
- Layer 5: Decision extraction from threads
- Layer 6: Context graph assembly
- Layer 7: Knowledge synthesis
- Layer 8: Driving philosophies
- Layer 9: Universal PR pipeline
- Layer 10: Metrics, estimation, reporting

Derek can add layers when he needs them. Comments alone — just seeing
what the agent did and why — is already a massive improvement over
"here's a diff, approve or reject."

**What would trip him up:**
- If the agent doesn't comment (because the cycle template doesn't
  require it), Derek gets nothing. Layer 1 requires that agent comments
  are on by default, not opt-in.
- Derek's features have no specs beyond the title. The agent is
  guessing at scope. Once he hits a rejection ("that's not what I
  meant"), he'll discover he needs better specs. The system should
  nudge: "This feature has no spec. Agent will interpret the title
  literally."

**The aha moment:**
Derek adds his 9th feature. The agent's comment mentions: "Following
the pattern from #3 — same CRUD structure." Derek realizes the agent
is already learning from his project's history, even without
philosophies or knowledge synthesis. The project knowledge is thin
(just patterns from reviewer comments), but it's there. The consulting
firm model isn't all-or-nothing — it grows with the project.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
