# 2. Marcus — Backend Lead Turned PM, Opinionated About Architecture

**Context:** Marcus is a backend engineer who became PM for a developer
tools project. He has strong opinions about API design and doesn't want
agents making architectural calls without him. 5 features queued, 3 in
flight. He checks the dashboard twice a day.

**What happens today:**

Marcus sees `api-error-handling` in `implementing` status. He can't
steer it without rejecting it. His only intervention point is
approve/reject at human-qa. If the agent goes the wrong direction for
20 minutes, Marcus doesn't find out until it submits.

**What happens with the consulting firm model:**

1. Marcus opens the dashboard. Feature `api-error-handling` is in
   `implementing`. He clicks into it and sees the live comment thread:

   ```
   [Implementer - 11:30am]
   Claiming api-error-handling. The spec says "consistent error
   responses across all API endpoints." I'll create an error middleware
   that wraps all handlers.

   [Implementer - 11:35am]
   Design decision: using RFC 7807 Problem Details format for all
   error responses. Structure:
   {
     "type": "https://tillr.dev/errors/not-found",
     "title": "Feature not found",
     "status": 404,
     "detail": "No feature with ID 99"
   }
   ```

2. Marcus doesn't love RFC 7807 — it's heavy for a local tool. He
   comments:

   ```
   [PM (Marcus) - 11:38am]
   RFC 7807 is overkill for us. We don't need the "type" URI or
   "instance" field. Just use:
   { "error": "not_found", "message": "No feature with ID 99" }
   Keep it simple. We're not a public API.
   ```

3. The agent is still working. On its next iteration (or when it checks
   for comments before submitting), it sees the PM's comment:

   ```
   [Implementer - 11:40am]
   Got it. Switching to simple error format. Already had the middleware
   structure — just changing the response shape. Will keep the
   consistent wrapping but drop the RFC 7807 fields.
   ```

   **Gap:** How does the agent know Marcus commented? If the agent is
   mid-implementation in a single LLM call, it won't see the comment
   until the call completes. There needs to be a "check for PM comments"
   step — either a polling mechanism between implementation phases, or
   a natural breakpoint where the agent pauses and checks. The current
   cycle engine doesn't have mid-step interrupts.

4. The agent continues and submits. The reviewer sees the full thread
   including the mid-flight correction:

   ```
   [Reviewer - 11:52am]
   Reviewed. Error middleware is clean. Simple format as PM requested.
   Good coverage — all 12 endpoints wrapped.

   Note: two endpoints (POST /api/features and PUT /api/features/:id)
   return validation errors as a flat string. Suggest structured
   validation:
   { "error": "validation_failed", "message": "...",
     "fields": {"title": "required", "priority": "must be 1-10"} }

   This is additive, not a rewrite. Want me to include it?
   ```

5. Marcus sees the reviewer's suggestion. He comments:

   ```
   [PM - 11:55am]
   Yes, add field-level validation errors. Good catch.
   ```

6. Feature goes back to the implementer for the addition. The final
   submission has the change, and Marcus approves knowing exactly what
   happened and why.

**What would trip him up:**
- Marcus commented at 11:38. If the agent submitted at 11:39 (before
  seeing the comment), the work is wasted. The timing of PM comments
  vs agent submission is a race condition. Need a guard: "PM commented
  since you started — read before submitting."
- If Marcus is offline when the agent makes a big decision, the decision
  stands until review. Marcus wants a way to flag certain decision
  *types* as "always wait for PM" (e.g., "any new external dependency"
  or "any new API format").

**What makes this work:**
- Marcus intervened at 11:38 — before the agent finished. No rejection,
  no wasted work.
- The reviewer built on Marcus's direction, not against it.
- The ticket thread is the complete decision record. Six months later,
  anyone can see why simple format was chosen over RFC 7807.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
