# 20. Wei — The Stakeholder Report That Writes Itself

**Context:** Wei needs to update stakeholders who aren't in tillr daily.
They want to know: what shipped, what's in flight, are we on track.
Wei currently spends 30 minutes per week writing these updates and
hates it.

**What happens:**

1. Wei runs:

   ```
   tillr report --since 1w --format stakeholder
   ```

2. A reporting agent traverses the last week's work and produces:

   ```
   Weekly Update — Tillr Project — Apr 4-11
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

   SHIPPED THIS WEEK (17 features)
     Notification system: Users now receive in-app notifications for
     feature status changes, review requests, and comments. Preferences
     are configurable per notification type.
     (Features #78, #80, #88 — completed, tested, merged)

     Export: CSV export of features and reporting data. One-click
     download from the dashboard.
     (Features #83, #85 — completed)

     Admin panel: Role management UI with permission enforcement.
     Admins can assign editor/viewer roles.
     (Features #67, #72 — completed)

     ...and 11 smaller improvements (error handling, settings,
     accessibility fixes)

   IN PROGRESS
     Async job processing (#102): Implementing embedded queue for
     background tasks. Philosophy amendment approved this week.

     File upload (#110): Spec in progress. PM review needed.

   MILESTONE: MVP Launch
     Progress: 38/50 features complete (76%)
     On track for target date (May 1)
     Remaining: 12 features, ~1.4 weeks at current velocity

   KEY DECISIONS THIS WEEK
     - Philosophy amended: embedded libraries OK for MVP
     - Cycle template updated: design review step added
     - JWT migration deferred to post-MVP (tracked in #58)
   ```

3. Wei reviews, makes one edit ("emphasize the notification system
   more, the CEO asked about it"), and sends it. 3 minutes instead
   of 30.

**What would trip her up:**
- The report groups features into themes ("Notification system") but
  the grouping is AI-generated. If it groups incorrectly (puts a
  backend fix under "Admin panel" because it touched the users table),
  the stakeholder gets confused. Wei needs to preview and edit before
  sending.
- Stakeholders might respond to the report with questions ("Why did
  JWT migration get deferred?"). Wei needs to be able to answer by
  searching tillr ("See the conversation on #42") — or better, the
  report should link to the relevant decision threads.

**What makes this work:**
- Everything the report needs already exists in tillr: features,
  comment threads, decisions, velocity data.
- Framed for stakeholders, not engineers. "Notification system" instead
  of "features #78, #80, #88."
- Generated on demand from the source of truth. Never stale.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
