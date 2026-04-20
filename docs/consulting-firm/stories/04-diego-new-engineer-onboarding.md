# 4. Diego — New Engineer, Joining a Project with 6 Months of History

**Context:** Diego is an engineer who just got access to a project that's
been running for 6 months with 120 completed features. He's looking at
the auth system and wondering why it uses session tokens instead of
JWTs. There's no architecture decision record, no wiki page, no README
section about auth.

**What happens today:**

Diego reads the code. Maybe runs `git blame`. Finds a commit message
that says "implement login page." No context. He asks the PM, who
vaguely remembers deciding this months ago but can't remember why.

**What happens with the consulting firm model:**

1. Diego searches tillr:

   ```
   tillr search "session tokens"
   ```

   Results:
   ```
   Feature #42  login-page  (done, 2026-04-15)
     Comment by Implementer: "Chose session tokens, not JWTs — simpler
     for v1."
     Comment by Reviewer: "Session tokens won't work for mobile. Filed
     #58 for JWT migration."
     Comment by PM: "Approved. JWT migration can wait."

   Feature #58  jwt-migration  (draft, unfiled)
     Description: "Migrate from session tokens to JWT for mobile
     client support. See #42 discussion."
   ```

2. Diego clicks into #42 and reads the full thread. In 2 minutes he
   knows:
   - Session tokens were chosen deliberately (simpler for v1)
   - The team knew JWTs would be needed for mobile
   - It was the PM's call to defer
   - There's a tracked feature (#58) for the migration
   - The reviewer flagged the trade-off at the time

3. He has full context. No archeology needed.

**What would trip him up:**
- `tillr search` needs to search comment bodies, not just feature titles
  and descriptions. If it only searches titles, Diego finds nothing for
  "session tokens."
- If the project has 120 features, the search results could be noisy.
  Relevance ranking matters — the feature where the decision was *made*
  should rank higher than features that merely reference it.

**What makes this work:**
- Comments are searchable. The decision lives where the work happened.
- Cross-references are automatic. #42 links to #58 because the
  reviewer mentioned it.
- The PM's reasoning is captured in their approval comment, not lost
  in a Slack thread that scrolled away 4 months ago.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
