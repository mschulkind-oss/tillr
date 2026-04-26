# 21. Agent-1 — The Implementer's Perspective

**Context:** Agent-1 is an implementing agent. It's a Claude session
with the "implementer" role, working on feature #95 (`batch-import`)
in a project with 40 completed features. It has no memory of prior
sessions. Everything it knows comes from the context packet tillr
assembles at claim time.

**The first agent interaction:**

1. Agent-1 claims feature #95 via the tillr API. Tillr returns:

   ```json
   {
     "feature": {
       "id": 95,
       "title": "batch-import",
       "spec": "Allow users to import features from a CSV file...",
       "priority": 6,
       "tags": ["import", "data", "api"]
     },
     "context_packet": {
       "philosophies": [
         "Simplicity over comprehensiveness...",
         "No external dependencies for MVP (v2: embedded OK)...",
         "Every UI action should be reversible..."
       ],
       "constraints": [
         {"decision": "#22", "rule": "snake_case singular tables"},
         {"decision": "#19", "rule": "use event bus from #55"}
       ],
       "related_work": [
         {"feature": "#38", "summary": "user-model — established CRUD pattern",
          "reviewer_note": "Good pattern. Follow for new endpoints."},
         {"feature": "#83", "summary": "export-csv — similar scope",
          "time": "20 min", "files": 2}
       ],
       "project_knowledge": {
         "patterns": ["handler → validate → service → respond", "..."],
         "anti_patterns": ["no global state", "no over-engineering", "..."],
         "pm_preferences": ["minimal implementations preferred"]
       },
       "pm_comments": [],
       "cross_feature_notes": []
     },
     "worktree": "/home/agent/.tillr/worktrees/feature-95-batch-import"
   }
   ```

   **Gap:** The context packet is JSON. The agent needs to parse it
   and reason about it. If the packet is 3,000 tokens, that's 3,000
   tokens of context the agent must process before writing any code.
   For simple features, this overhead might be wasteful. For complex
   features, 3,000 tokens might not be enough. There's no way for the
   agent to request "more context about the event bus pattern" without
   making a separate API call.

2. Agent-1 reads the context, plans its approach, and starts
   implementing. It comments as it goes:

   ```
   POST /api/features/95/comments
   {
     "author_role": "implementer",
     "body": "Claiming batch-import. Per philosophy #1, keeping it
     simple: fixed CSV format, no configurable mapping. Per philosophy
     #3, adding batch undo via import_batch_id.",
     "metadata": {
       "philosophy_refs": [1, 3],
       "decision_type": "implementation_approach"
     }
   }
   ```

3. During implementation, Agent-1 discovers the spec is ambiguous:
   what happens if a CSV row has invalid data? Skip the row? Fail the
   whole import? It checks the context packet — no guidance. It comments:

   ```
   [Implementer - 9:08am]
   Spec gap: no guidance on invalid CSV rows. Options:
   1. Fail entire import on first error (strict)
   2. Skip invalid rows, report which were skipped (lenient)
   3. Import valid rows, quarantine invalid ones for review

   Going with option 2 (skip + report) per simplicity philosophy.
   The import response will include {"imported": 45, "skipped": 3,
   "errors": [{"row": 12, "reason": "missing title"}, ...]}.

   Filing this as a spec gap for PM awareness.
   ```

4. Agent-1 finishes, commits, and submits:

   ```
   POST /api/features/95/submit
   {
     "summary": "Implemented batch-import. Fixed CSV format, sync
     processing, batch undo. Skips invalid rows with error report.
     4 files changed, 180 lines added.",
     "spec_gaps": ["Invalid row handling not specified"]
   }
   ```

**What would trip the agent up:**
- The context packet includes "pm_preferences: minimal implementations
  preferred." But what if this specific feature needs a complex
  implementation? The agent has no way to know whether the PM would
  override the preference for this case. It defaults to simple and
  hopes.
- The agent commented "going with option 2 per simplicity philosophy."
  But the PM might have preferred option 3 (quarantine). By the time
  the PM sees the comment, the agent has already implemented option 2.
  The mid-flight correction problem from story #2 applies here too.
- The worktree path is assigned by tillr. If the agent tries to access
  files outside the worktree (e.g., checking another feature's code),
  it's working blind. The context packet is all it has.

**What makes this work:**
- The agent started with full project context despite having no memory.
  The context packet replaced institutional knowledge.
- Philosophy citations create traceability. The PM can see *why* the
  agent chose simple.
- Spec gaps are surfaced proactively. The agent didn't just guess — it
  flagged the ambiguity and documented its choice.
- Structured metadata in comments (philosophy_refs, decision_type)
  enables automated analysis later.

## Resolution (added later)

Two of the gaps flagged here are addressed by subsequent stages:

- The "agent went with option 2 per simplicity philosophy; PM might
  have preferred option 3" gap is resolved by
  [Story 24 (Meera questionnaires)](./24-questionnaires-as-checkpoints.md):
  high-stakes decisions get a hard-block checkpoint where the agent
  presents alternatives BEFORE deciding. The PM picks; the agent
  doesn't have to guess.
- The mid-flight correction problem is resolved by
  [Layer 4b (async dialogue)](../implementation-layers.md) — PM
  comments transition cycle state to `pending-author-response`, so the
  agent picks up the comment at its next cycle boundary.

The "context packet is JSON; agent must parse it before any code"
note is just a description of the protocol, not an unresolved gap.
See [Story 29 (Anders — platform adapter)](./29-anders-platform-adapter.md)
for how adapters convert tillr's canonical envelope to platform-
specific invocation.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
