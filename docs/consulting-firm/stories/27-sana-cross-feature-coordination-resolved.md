# 27. Sana — Cross-Feature Coordination, Resolved

**Context:** Same Sana from [story 3](./03-sana-interdependent-features.md).
A few months later. Layer 4b (async dialogue cycle states) is now in
place. Two agents are working on `payments-refactor` and
`subscription-billing` — both touch the `orders` table. This is the
exact scenario that tripped Sana up before, replayed with the new
substrate.

**What story 3 left unresolved:**

Story 3's gap was direct:

> Agent-2 "checks" Agent-1's thread and "cross-comments." But these are
> separate LLM sessions. Agent-2 can read Agent-1's comments (they're in
> the DB), but Agent-1 can't see Agent-2's cross-comment until its
> *next* interaction with tillr. If Agent-1 has already finished and
> submitted, the cross-comment is too late.

Story 3 worked WHEN the two agents happened to be running concurrently
and at the right phase. It failed when their schedules didn't overlap.
That's a race condition, not a workflow.

**What happens now (with Layer 4b):**

1. Agent-1 claims `payments-refactor`. Cycle assigns claim, agent
   starts implementing.

2. Agent-2 claims `subscription-billing` — at the same time. Both
   start.

3. Agent-2's claim response includes a context packet that flags the
   overlap (Layer 6 detected `orders` table mentioned in both feature
   specs). Agent-2 sees in its packet:

   ```
   CROSS-FEATURE NOTES
     #payments-refactor (in-progress, Implementer-1)
       Touches: internal/db/orders.go, internal/api/orders.go
       Last activity: 5 min ago (claimed)
       No comments yet.
   ```

4. Agent-2 leaves a comment on `#payments-refactor`:

   ```
   [Implementer-2 on #payments-refactor - 2:00pm]
   Heads up: I'm building subscription-billing. I'll need
   orders.stripe_subscription_id (nullable FK to a new
   subscriptions table). Are you adding it as part of this
   refactor, or should I handle it independently?
   ```

5. **The cycle engine routes the comment.** It detects:
   - Comment is on a feature in `implementing` state
   - Comment author is a different agent (not the assigned implementer)
   - Comment requires a response (uses `?` or `@author` or marked as
     question)
   - Transition: `implementing` → `implementing-pending-author-response`

   `#payments-refactor` is now in Agent-1's inbox.

6. Agent-1 finishes its current chunk of work (writing tests for the
   refactor). Hits cycle-step boundary. Picks up its inbox. Sees the
   comment. Responds:

   ```
   [Implementer-1 on #payments-refactor - 2:18pm]
   Yes, adding stripe_subscription_id as a nullable FK. Migration in
   the PR. Will be in by 3pm.

   Contract:
     stripe_subscription_id  text  nullable
     fk -> subscriptions(stripe_id)  on delete set null

   You can build against this.
   ```

   State transitions: `implementing-pending-author-response` →
   `implementing-pending-reviewer-response` (Agent-2's inbox).

7. Agent-2's next invocation picks up the response. Confirms. Builds
   against the contract. Submits.

8. **No race condition.** Each agent operates within its own LLM call
   window. Coordination happens at cycle boundaries via inbox routing.

**What's different from story 3:**

| | Story 3 (gap) | Story 27 (resolved) |
|---|---|---|
| Coordination mechanism | Hope both agents are running | Cycle engine routes via inbox |
| Race condition | Real, unsolved | Mitigated; agents process at boundaries |
| Required overlap | Both agents in flight simultaneously | None — async, like email |
| Failure mode | Agent-1 submits before seeing #2's question | Agent-1's submit is blocked by `pending-author-response` state |

The substrate is exactly Layer 4b's async cycle states. The model is
exactly how a real engineering org handles coordination via PR
comments — email pace, not chat pace.

**Initial discovery — still a partial gap:**

How did Agent-2 know to comment on `#payments-refactor` in the first
place? Three options, each with tradeoffs:

- **Option A: Layer 6 context packet (default).** The packet flagged
  the overlap because both features touch `orders.go`. Works if Layer
  6 has shipped (Stage 5).

- **Option B: Layer 3 cross-ref auto-detection.** Agent-2's planning
  comment ("I'll need to modify the orders table") gets matched
  against active features mentioning the orders table. Works if
  Layer 3 has shipped.

- **Option C: Manual dependency from Sana.** Sana noticed the overlap
  during planning and added `related: #payments-refactor` to
  subscription-billing. Works at any stage.

The story shows Option A as the assumed default (full Layer 6 in
play). At earlier stages, Option C is the fallback.

**Convergence and ping-pong protection:**

In pathological cases, two agents could ping-pong indefinitely
("clarify the contract" → "what do you mean by X?" → "I mean Y" →
"but what about Z?"). [Open question 30](../open-questions.md#30-reviewer-implementer-loop-iteration-limit)
is the safety valve: soft cap (warning) at 3 iterations, hard cap (PM
escalation) at 5.

In Sana's case the loop was short (1 round). But the system needs the
cap.

**Gaps:**

- **Initial discovery is the hard part.** Whoever writes the comment
  first has to know to write it. Layer 6 helps; without it, you rely
  on tags and manual cross-refs.

- **Concurrent claims edge case.** If both agents claim and finish
  their first chunks within the same 30-second window, they may both
  diverge before either sees the other's work. The cycle engine should
  add a brief delay on claim ("checking related features") for
  features with `related` edges to in-flight work.

- **Schema for "is this a question vs an FYI."** The cycle engine has
  to know whether a cross-feature comment requires a response (block
  state transition) or is informational (no block). Default heuristic
  + explicit `requires-response: yes/no` metadata.

- **Cross-feature comments from PM.** If Sana adds a cross-feature
  comment ("these two need to coordinate"), should THAT block? It's a
  PM directive. Probably yes. Same routing as agent comments.

**What would trip Sana up:**

- **Implicit cross-feature dependencies still slip through.** Two
  features touching the same DB table with no comment overlap (story
  23's failure case) still don't get caught by cycle routing. Layer 6's
  edge inference is the answer; Layer 6's blind spots remain.

- **Long ping-pong loops.** If Agent-1 and Agent-2 disagree on a
  contract and pass 8 iterations before escalating, Sana sees a stale
  feature with lots of recent activity but no progress. The dashboard
  should surface "features with >3 cycle iterations" prominently.

- **Agent-PM scenarios.** When agent-PMs (story 26) are the ones
  responding to cross-feature comments, the calibration of their
  responses matters more than human PMs (who are trusted more readily).

**What makes this work:**

- **Layer 4b's async cycle states + comments substrate.** The same
  mechanism that handles reviewer↔implementer dialogue (story 25)
  handles cross-feature dialogue. One substrate, multiple use cases.

- **Layer 6's context packet** flags overlaps so Agent-2 knows to
  comment in the first place.

- **Same loop as a real engineering org's PR coordination.** Email
  pace, not chat pace. Agents process inboxes at their own pace; the
  cycle engine arbitrates routing.

- **No new mechanism needed.** Story 27 didn't require ANY new
  layers vs story 25. Same substrate, different use case. This is the
  payoff of Layer 4b's universality.

**Resolution of story 3:**

Story 3 worked when both agents were lucky enough to overlap. Story 27
works regardless of timing. The race condition is gone; the
coordination mechanism is the cycle engine + inboxes.

**Position in roadmap:**

**Stage 2** — needs Layer 4b (async dialogue cycle states). Discovery
mechanism (which makes Agent-2 write the cross-comment) varies:
- At Stage 2: manual cross-refs from Sana (Option C above)
- At Stage 3: cycle template can mandate "check related features"
  step (mechanical Layer 9)
- At Stage 5: Layer 6 context packet does it automatically (Option A)

So this story EVOLVES as more stages ship. The core mechanism (async
coordination) is Stage 2; the discovery improvement is later.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
