# 3. Sana — Design Lead, Managing Interdependent Features

**Context:** Sana runs a project with heavy feature interdependencies —
a user management system where permissions, admin UI, and audit logging
all touch the same models. She has 12 features in flight across 3
agents. Her biggest pain is features that break each other.

**What happens today:**

Both agents work in isolation. The `admin-panel` agent invents its own
permission model because it doesn't know what `user-permissions` is
building. When both submit, they're incompatible. Sana rejects one or
both and starts over.

**What happens with the consulting firm model:**

1. Agent-1 claims `user-permissions`, Agent-2 claims `admin-panel`. Both
   start working.

2. Agent-1 comments on `user-permissions`:

   ```
   [Implementer-1 - 2:00pm]
   Starting user-permissions. Defining three roles: admin, editor,
   viewer. Permission checks via middleware:
     func RequireRole(role string) gin.HandlerFunc

   API: GET /api/users/:id/role, PUT /api/users/:id/role
   ```

3. Agent-2, working on `admin-panel`, reads the related feature's
   thread (tillr links them via the `admin` tag and an explicit
   dependency). It comments on its own ticket:

   ```
   [Implementer-2 - 2:05pm]
   Building admin-panel. Checked #user-permissions thread — they're
   implementing RequireRole middleware with admin/editor/viewer roles.
   I'll build the admin UI to use these roles directly.

   API dependency: I need GET /api/users (list all) and
   PUT /api/users/:id/role (assign role). Confirming with
   Implementer-1 that these endpoints exist.
   ```

4. Agent-2 cross-comments on `user-permissions`:

   ```
   [Implementer-2 on #user-permissions - 2:06pm]
   I'm building admin-panel and need:
   1. GET /api/users — list all users with their current roles
   2. PUT /api/users/:id/role — must accept {"role": "admin|editor|viewer"}

   Are you exposing these? If not, can you add them? I'll block on the
   UI for role assignment until you confirm the API shape.
   ```

   **Gap:** Agent-2 "checks" Agent-1's thread and "cross-comments." But
   these are separate LLM sessions. Agent-2 can read Agent-1's comments
   (they're in the DB), but Agent-1 can't see Agent-2's cross-comment
   until its *next* interaction with tillr. If Agent-1 has already
   finished and submitted, the cross-comment is too late. The timing
   depends on both agents being in-flight simultaneously *and* having
   a mechanism to check for new comments mid-implementation.

5. Agent-1 responds:

   ```
   [Implementer-1 on #user-permissions - 2:08pm]
   Yes, both endpoints are in my plan. GET /api/users returns:
   [{"id": 1, "name": "...", "role": "admin"}, ...]

   PUT /api/users/:id/role accepts {"role": "admin|editor|viewer"}.
   Returns 403 if the caller isn't admin.

   I'll have these committed in ~5 minutes. You can build against
   this contract.
   ```

6. Both agents finish. The reviewer sees coordinated work:

   ```
   [Reviewer - 2:30pm]
   Reviewed both #user-permissions and #admin-panel together (tagged
   as related).

   The API contract matches: admin-panel calls exactly the endpoints
   that user-permissions exposes. Role types are consistent
   (admin/editor/viewer in both). Permission check middleware is
   applied to admin-panel routes correctly.

   One note: admin-panel doesn't handle the 403 case when a non-admin
   tries to change roles. It should show an error, not fail silently.

   Approving user-permissions. Returning admin-panel for the 403
   handling.
   ```

7. Sana sees both features with full coordination context:

   ```
   #user-permissions    human-qa     reviewed ✓
     "Three roles, middleware-based. API contract agreed with
      admin-panel team."

   #admin-panel         implementing (returned from review)
     "Role management UI built against agreed API. Reviewer caught
      missing 403 error handling."
   ```

**What would trip her up:**
- If the two agents claim at the same time and both finish before either
  checks the other's comments, the cross-collaboration doesn't happen.
  The story assumes overlapping windows. Need a mechanism: when two
  features share a dependency link, tillr staggers the claims or forces
  a "check related features" step before implementation begins.
- The reviewer reviewed both features together. How does it know to?
  The `related` tag needs to trigger paired review, not just surface
  in the UI.

**What makes this work:**
- Cross-feature comments. Agent-2 read Agent-1's thread and posted a
  question. Agent-1 answered. They agreed on an API contract.
- The reviewer checked both features as a pair.
- Sana didn't coordinate this. The agents did. She just sees the result.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
