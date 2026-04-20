# 3. Lisa — Product Owner, Only Touches the UI

**Context:** Lisa is non-technical. She works with a dev team that uses tillr.
She doesn't use the CLI. She doesn't read code. She opens the tillr web UI
to see what's happening and make decisions.

**What she needs from the UI:**

1. **"What needs my attention?"** — She opens tillr and the first thing she
   sees should be her inbox. Items grouped by urgency:
   - Features waiting for QA (approve/reject decisions)
   - Blocked features (need her input to unblock)
   - Features that were rejected and resubmitted (re-review)
   - Open questions from agents or devs

   **Gap:** Today the workstream page has a "Human Inbox" section, but:
   - It's per-workstream, not global. Lisa has to click into each workstream
     to see what needs attention. She needs a global inbox across all
     workstreams.
   - Items don't explain what she should do. "Feature X is in human-qa" tells
     her nothing. She needs "Feature X: Login page is ready for review. Check
     that the layout matches the mockup. [Approve] [Reject]"
   - There's no priority ordering within the inbox. High-priority QA items
     should be at the top.

2. **"What's the state of the project?"** — The dashboard should give her
   a one-glance answer: how many features are done, how many are in
   progress, how many are waiting for her. A burndown or progress bar per
   workstream.

   **Gap:** The dashboard exists but it's developer-focused — cycle counts,
   agent sessions, technical metrics. Lisa wants: "Auth workstream: 8/12
   features done. 2 waiting for your review."

3. **"I want to change priorities."** — She should be able to drag-and-drop
   or re-order the roadmap. Today she'd have to ask a dev to run CLI
   commands.

   **Gap:** The roadmap page is read-only in the UI. All mutations go through
   the CLI. Lisa can't reprioritize from the browser.

4. **QA review flow.** When Lisa clicks on a feature in her inbox, she needs:

   a. **Summary**: "This feature adds email/password login to the app." One
      paragraph, written for a non-technical reader.

   b. **What to check**: A human-readable checklist. Not "verify the bcrypt
      rounds are >=12" but "try logging in with a wrong password — you should
      see an error message." These items are things she can actually verify
      by using the app.

   c. **Visual diff** (for UI features): Before/after screenshots, or a link
      to a preview deployment.

   d. **Approve/Reject** with a required notes field on rejection. The notes
      go back to the agent as context for the next iteration.

   **Gap:** Today QA is a status badge and a checklist that's mostly
   technical. The checklist items come from the agent's automated QA, not
   from a human-oriented test plan. The approve/reject buttons exist on the
   workstream detail page but they're small and the flow isn't guided.

**What would trip her up:**
- She can't find the approve/reject buttons. They're on the workstream
  detail page, nested under a feature, under a QA section. She needs them
  front and center in the inbox.
- She doesn't understand status labels like "human-qa" or "agent-qa." She
  wants "Ready for your review" and "Agent is checking."
- She can't add a feature or create a roadmap item from the UI. She has
  to ask a developer. Basic CRUD in the UI would help.
- No notifications. She has to remember to check tillr. Email or Slack
  notifications when something needs her attention would close the loop.

---

« [All stories](./README.md) · [User-stories overview](../README.md)
