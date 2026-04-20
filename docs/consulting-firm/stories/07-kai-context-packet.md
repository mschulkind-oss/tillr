# 7. Kai — Power User, 3 Projects, Trusts the System

**Context:** Kai runs 3 tillr projects simultaneously — a web app, a
CLI tool, and an internal API. 150+ features across the three. He's
learned to trust the context graph and mostly auto-approves features
that pass agent review cleanly. His bottleneck is PM review time.

**What happens without the context graph:**

The agent reads the spec for `notification-preferences`: "Let users
configure which notifications they receive (email, in-app, none) per
event type." It builds a preferences table, a settings page, and a
notification service. Clean implementation. Submits.

The reviewer catches three problems:
1. The preferences table uses a different naming convention than the
   existing `user_settings` table from feature #61.
2. The notification service duplicates logic from the event system
   built in #55, which already has a pub/sub pattern.
3. The PM commented on the workstream two weeks ago: "Don't add email
   yet — we haven't decided on a provider. In-app only for MVP."

Three rejections. Three wasted agent sessions. Kai is frustrated:
"How did the agent not know any of this?"

**What happens with the context graph:**

1. The agent claims `notification-preferences`. Tillr assembles the
   context packet:

   ```
   Context for Feature #78: notification-preferences
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

   STRATEGIC CONTEXT
     Workstream: User Experience
     PM note on workstream (2026-03-28): "No email for MVP. We haven't
     picked a provider. In-app notifications only."

   CONSTRAINTS
     Decision #22: Table naming uses snake_case singular
       (established in #61: user_settings, not UserSettings)
     Decision #19: Event-driven patterns use the pub/sub system
       from #55 (internal/events/bus.go)

   RELATED WORK
     Feature #55 (done): event-system
       Built pub/sub event bus at internal/events/bus.go
       Pattern: bus.Subscribe("feature.created", handler)
       Reviewer note: "All new event producers should use this bus.
       Don't create separate notification channels."

     Feature #61 (done): user-settings
       Established user_settings table and settings API pattern
       Reference: internal/db/settings.go, internal/api/settings.go
       Reviewer note: "Good CRUD pattern. Follow this for new
       settings-like tables."

   CROSS-FEATURE NOTES
     Feature #80 (in-progress): activity-feed
       Implementer comment: "I'm building the in-app notification
       display. If someone is building notification preferences,
       I'll need to check their settings before showing items.
       Expected API: GET /api/users/:id/notification-preferences"
   ```

2. The agent starts with full awareness:

   ```
   [Implementer - 2:00pm]
   Claiming notification-preferences. Context packet received.
   Noting constraints:
   - In-app only (no email) per PM workstream guidance
   - Use event bus from #55 for notification triggers
   - Follow user_settings pattern from #61 for the prefs table
   - Feature #80 (activity-feed) expects
     GET /api/users/:id/notification-preferences

   Design:
   - Table: notification_preference (singular, snake_case per #22)
   - Subscribe to event bus topics, check user prefs before dispatching
   - API matches what #80 expects
   ```

3. First submission is clean. Reviewer confirms:

   ```
   [Reviewer - 2:25pm]
   Reviewed. Fully consistent with prior work:
   - Table follows #61 naming convention ✓
   - Uses event bus from #55 ✓
   - API matches #80's expected contract ✓
   - No email — in-app only per PM guidance ✓

   One suggestion: add a migration to seed default preferences
   (all notifications on) for existing users. Non-blocking.

   Approved.
   ```

4. Kai sees the feature in review with zero surprises:

   ```
   Feature #78  notification-preferences     human-qa
     4 comments · 0 issues · reviewed ✓

     Context used: 3 prior decisions, 2 related features,
     1 workstream constraint

     Reviewer: "Fully consistent with prior work. All constraints met."

     [Approve] [Reject] [Comment]
   ```

   Kai approves in 10 seconds. The agent got it right the first time.

**What would trip him up:**
- Kai auto-approves features that pass agent review cleanly. But what
  if the context graph missed a relevant constraint? He's trusting the
  graph to be complete. If the system shows "Context used: 3 decisions,
  2 related features" he assumes that's *all* the relevant context. A
  missing edge in the graph is invisible.
- With 150+ features across 3 projects, Kai needs per-project context
  isolation. A decision in project A should never leak into project B's
  context packets.

**What makes this work:**
- Three decisions and two related features were automatically surfaced.
  No one had to remember to tell the agent about them.
- The workstream-level PM comment ("no email for MVP") propagated down
  to a specific feature. The agent never would have found this without
  graph traversal.
- The in-flight feature #80's comment about the expected API contract
  prevented an incompatibility before it happened.
- Zero rejections. Zero wasted sessions.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
