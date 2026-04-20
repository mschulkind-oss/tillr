# 9. Val — Developer, Losing Track of What Needs Attention

**Context:** Val has been using tillr for a month. There are 4 workstreams,
60 features, and agents running periodically. Things are moving, but Val has
lost the thread. She opens tillr and doesn't know where to start.

**What she sees today:**

- Dashboard: numbers and charts. "47 features, 12 done, 8 in progress."
  Okay, but what does she need to DO?
- Workstream pages: each one has features in various states. She has to
  click into each workstream, scan the features, look for human-qa items,
  check for blocked features, read discussion threads.
- Queue page: shows what agents can work on. Not really for her.
- No inbox. No notifications. No "start here."

**What she needs — the Global Inbox:**

Val opens tillr and the first page she sees is her inbox. It shows everything
across all workstreams that needs a human decision, ordered by priority:

```
Your Inbox (7 items)

QA Review (3)
  login-page               Authentication    Priority 8    2 hours ago
    "Login form with email/password. Check: layout looks right, error
     messages are clear, forgot password link works."
    [Approve] [Reject]

  signup-validation         Authentication    Priority 7    5 hours ago
    "Email validation and password strength. Check: try weak passwords,
     check the error messages make sense."
    [Approve] [Reject]

  api-rate-limiting         API Gateway       Priority 9    1 day ago
    "Rate limiting on public endpoints. Check: hit the endpoint rapidly,
     verify you get a 429 after the limit."
    [Approve] [Reject]

Blocked (2)
  oauth-integration         Authentication    Priority 9    3 days ago
    Blocked on: "Need Google OAuth client ID. Who creates this?"
    [Provide Answer] [Defer]

  database-migration        Data Layer        Priority 8    1 day ago
    Blocked on: depends on schema-redesign (in progress)
    [No action needed — auto-resolves when dependency completes]

Previously Rejected (1)
  mobile-layout             UI Polish         Priority 6    12 hours ago
    Rejected 2 days ago: "Header overlaps on iPhone SE"
    Agent resubmitted with fix. Re-review.
    [Approve] [Reject]

Needs Spec (1)
  search-api                API Gateway       Priority 8
    "Full-text search endpoint" — no spec, can't be claimed by agents.
    [Write Spec] [Defer]
```

**Gap:** This inbox doesn't exist today. The workstream detail page has a
"Human Inbox" section that lists categories, but:
- It's per-workstream, not global
- Items show feature names but not what to do about them
- There's no inline approve/reject — you have to navigate to the feature
  detail page
- There's no "what to check" summary — you see the raw spec, not a
  human-oriented test plan
- Features without specs aren't flagged as a problem

**What Val does with the inbox:**

1. She starts at the top (highest priority). `api-rate-limiting` — she
   opens a terminal, hits the endpoint 100 times with curl, sees the 429
   response. Looks right. She clicks [Approve] and types "Verified rate
   limiting works. 429 after 50 requests."

2. `login-page` — she opens the app, tries logging in with wrong password,
   sees the error message. Tries with right password, gets in. Clicks
   [Approve].

3. `oauth-integration` is blocked. She creates a Google OAuth app, gets
   the client ID, clicks [Provide Answer] and pastes it. The feature
   unblocks and goes back to the agent queue.

4. `search-api` needs a spec. She clicks [Write Spec] and types:
   ```
   GET /api/search?q=<query>
   - Searches features, discussions, and roadmap items
   - Returns top 20 results ranked by relevance
   - Each result has: type, title, snippet, link
   - Empty query returns 400
   ```
   The feature is now claimable by agents.

5. She's done in 15 minutes. She closes tillr and goes back to other work.
   When agents finish more features, she'll have new inbox items tomorrow.

**The aha moment:** Val realizes she's spending 15 minutes a day on tillr
and getting more done than when she spent 2 hours a day manually reviewing
PRs and writing Claude prompts. The structure made her faster, not slower.

---

« [All stories](./README.md) · [User-stories overview](../README.md)
