# 9. Lin — Agent Onboarding a New Domain

**Context:** Lin's project is entering a new domain — WebSocket support.
No agent has touched this area before. The first agent in needs to
establish patterns that future agents will follow. Lin wants the domain
knowledge to build itself, not require separate documentation effort.

**What happens:**

1. Feature #30 is the first to touch the WebSocket layer. The
   implementer leaves detailed comments:

   ```
   [Implementer on #30 - last month]
   First time we're using WebSocket in this project. Documenting the
   pattern for future reference:

   - Hub lives at internal/server/ws/hub.go
   - Each client gets a goroutine pair (readPump/writePump)
   - Messages are typed: {"type": "feature.updated", "payload": {...}}
   - To broadcast: hub.Broadcast(msg) — don't write to individual
     connections
   - Testing: use the wstest helper at internal/server/ws/ws_test.go
     for integration tests. It handles the upgrade handshake.

   Lessons learned during implementation:
   - Don't close the connection from the write side — let readPump
     detect the close and clean up. Closing from both sides causes
     a race.
   - Message serialization: always use the ws.Message struct, don't
     marshal raw JSON strings. The struct handles the type field.
   ```

   The reviewer on #30 confirmed and added:

   ```
   [Reviewer on #30]
   Good documentation. Adding one note: if you need to send a message
   to a SPECIFIC client (not broadcast), use hub.SendTo(clientID, msg).
   The clientID is the user session ID from the auth middleware.
   ```

2. Three weeks later, feature #88 needs WebSocket for real-time
   notifications. The agent claims it and gets the context packet:

   ```
   DOMAIN: WebSocket
     Established by: Feature #30
     Implementation notes (from implementer + reviewer):
       - Hub: internal/server/ws/hub.go
       - Pattern: readPump/writePump goroutine pair per client
       - Broadcast: hub.Broadcast(msg)
       - Targeted: hub.SendTo(clientID, msg)
       - Messages: use ws.Message struct (typed)
       - Testing: wstest helper at internal/server/ws/ws_test.go
       - Gotcha: don't close from write side (race condition)
       - Gotcha: don't marshal raw JSON (use ws.Message struct)
   ```

3. The agent on #88 doesn't have to explore the WebSocket code from
   scratch:

   ```
   [Implementer on #88 - today]
   Building real-time notifications on existing WebSocket layer (see
   #30 implementation notes).

   Using hub.SendTo(clientID, msg) for targeted notifications — user
   should only see their own notifications, not broadcast.

   Adding new message type: {"type": "notification.new", "payload":
   {"title": "...", "body": "...", "feature_id": 78}}

   Using wstest helper for integration tests per #30 pattern.
   ```

4. No stumbling, no re-discovery, no "why did this race condition
   happen."

   **Gap:** The agent on #30 wrote detailed notes *because it was
   instructed to document decisions in comments*. But not every agent
   will be this thorough. If the cycle instructions just say "implement
   the feature," the first-in-domain agent might not document anything.
   There needs to be a "new domain" detection: when a feature touches
   files/packages that no prior feature has touched, add an instruction
   to the context packet: "You're the first to work in this area.
   Document the patterns you establish."

**What would trip Lin up:**
- How does tillr know this is a "new domain"? If the agent creates
  `internal/server/ws/`, that's a new package — detectable. But if the
  agent adds WebSocket code to an existing file, the domain boundary
  is fuzzy. Tag-based detection ("websocket" tag) requires Lin to tag
  features correctly.
- Domain notes accumulate. After 10 features touch WebSocket, the notes
  from #30 might be outdated. The knowledge synthesis (story #14) should
  detect when domain notes haven't been validated by recent features.

**What makes this work:**
- Implementation notes are a natural byproduct of "explain your
  decisions in comments." No separate documentation task.
- Tillr recognized the domain overlap and included #30's notes in #88's
  context packet.
- The reviewer's addendum on #30 (SendTo for targeted messages) turned
  out to be exactly what #88 needed. A review comment from a month ago
  became institutional knowledge.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
