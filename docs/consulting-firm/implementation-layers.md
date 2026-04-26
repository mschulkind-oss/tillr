# Implementation Layers

Build this incrementally. Each layer delivers value independently.
This doc decomposes by ARCHITECTURE; for the
**shipping order** (which layers ship together as stages, with risk
and validation criteria per stage), see [roadmap.md](./roadmap.md).

## Foundations (parallel concern, not a numbered layer)

### Platform Adapter

Tillr defines a universal protocol — cycle steps, context envelopes,
comment artifacts, status transitions, role files. The platform-
specific code is a thin invocation **adapter** per agent platform
(Claude SDK, Copilot cloud, etc.). The adapter:

- Receives a cycle-step dispatch `(role, envelope, model_class)`
- Translates to the platform's invocation
- Captures output as canonical tillr comments via the comments API
- Reports status / cost / errors back to the cycle engine

Adapters are platform-specific; the protocol is not. See
[story 29 (Anders)](./stories/29-anders-platform-adapter.md) for the
worked example, including the
[Claude vs Copilot capabilities matrix](./stories/29-anders-platform-adapter.md#platform-agnosticism-confirmed-by-research).

The adapter is needed from Stage 1 in its minimal form (~200 lines:
"call SDK, capture output as a single comment"). Sophistication grows
alongside subsequent stages.

## Layer 1: Comments (foundation)

- `comments` table (entity_type, entity_id, author_type, author_role, body)
- `tillr comment <id> "text"` CLI command
- `POST /api/features/:id/comments` API endpoint
- Comments visible on feature detail page in web UI
- Agent comments during implementation (injected via API by the agent
  harness or tillr CLI)

**This alone changes the game.** Even without cross-references, agent
identity, or structured metadata — just having a conversation thread on
every feature gives PMs the context they're missing. (See
[story 22](./stories/22-derek-progressive-disclosure.md) for this
working with nothing else.)

## Layer 2: Agent Comments in Cycles

- Cycle steps automatically create comments: "Claimed by implementer",
  "Submitted for review", "Reviewer approved"
- Agents are instructed to comment on decisions: library choices,
  architecture decisions, trade-offs encountered
- Comments attributed to agent role (implementer, reviewer, designer)

### Layer 2b: Cycle Questionnaires (enforced structure)

Layer 2 asks agents to comment on decisions — *soft* guidance. Layer 2b
hardens that into **mandatory structured output at cycle-step
boundaries**. A cycle template can attach a questionnaire to any step
(post-claim, pre-submit, post-review, etc.). The agent cannot advance
past a step without answering its questions.

- Questionnaires live on cycle templates (see Layer 9), so they're
  versioned and reviewable like any other config change.
- Each question has a format (short text, prose, yes/no + justification,
  list, structured options) and blocking semantics:
  - `hard_block` — agent waits for explicit PM approval.
  - `soft_block` — agent waits N minutes, then proceeds and flags
    "auto-proceeded" in the audit trail.
  - `log_only` — agent answers, no block.
- Conditional firing: a checkpoint can be declared conditional on
  feature attributes (`tag == billing`, `touches internal/auth/**`).
- Answers are rendered as structured comments on the feature, feeding
  decision extraction (Layer 5) and knowledge synthesis (Layer 7).

This is the PM's **dial for oversight depth** — routine features get a
one-question pre-submit checkpoint; high-stakes features get multiple
hard-blocking checkpoints with rich schemas. See
[story 24](./stories/24-questionnaires-as-checkpoints.md) for the
worked example.

## Layer 3: Cross-Feature Communication

- `cross_refs` table linking entities mentioned in comments
- Auto-detection: when a comment mentions `#42` or `feature-name`,
  create a cross_ref
- Cross-feature comments: agent working on #51 can comment on #42
- Linked features shown on feature detail page

## Layer 4: PM Interaction

- Mid-flight comments: PM comments while agent is implementing
- Agent checks for new PM comments before submitting
- "Comment" action alongside Approve/Reject in inbox
- Comment-triggered re-evaluation: PM says "change approach" → feature
  goes back to implementing with the comment as context

### Layer 4b: Async Reviewer ↔ Implementer Dialogue

Generalize the comment-triggered re-evaluation from "PM" to **any
commenter, including reviewer agents**. Reviewer leaves comments →
cycle state moves to `pending-author-response` → implementer agent's
inbox → implementer responds via comments → state moves to
`pending-reviewer-response` → reviewer's inbox → loop until reviewer
approves or PM intervenes.

This is the substrate for every multi-agent dialogue (style enforcer
↔ implementer, code reviewer ↔ implementer, designer ↔ reviewer).
No synchronous coordination primitive — agents process inboxes when
they next run, exactly like a real engineering org's async PR review.
See [story 25](./stories/25-style-enforcer-async-dialogue.md) for the
worked example.

- New cycle states: `<step>-pending-author-response`, `<step>-pending-reviewer-response`
- Comment metadata flags resolution: `resolved` / `accepted` / `rejected` per finding
- Stall detection: surface PRs in any `pending-*` state >24h
- Loop counter: track iterations per cycle step for the retro report

## Layer 5: Decision Extraction

- Structured decision metadata in comments (what, alternatives, rationale)
- `tillr search --type decision` finds all decisions across features
- Decision timeline on dashboard
- Auto-generated decision records from comment threads

## Layer 6: Context Graph Assembly

- On `tillr agent claim`, assemble a context packet from the graph:
  walk up to workstream/milestone, sideways to related features and
  decisions, back to prior work in the same domain
- `cross_refs` edges from comments, shared files, shared tags
- Dependency graph visualization (milestone view, feature map)
- Selective context: include only what's relevant to this feature's
  domain, not the entire project history
- Context packet as part of the claim response — agents receive it
  automatically, no prompt engineering needed
- **Per-cycle-step envelopes.** Context envelopes are scoped to the
  cycle step, not the feature. The implementer gets project history;
  the style enforcer gets the diff + style guide only; the code
  reviewer gets the diff + project knowledge. Each role loads exactly
  what its job needs and unloads when the step ends — no token waste,
  no context pollution between roles. (See
  [story 25](./stories/25-style-enforcer-async-dialogue.md).)

**This is the highest-leverage layer.** Comments (Layer 1) are
valuable on their own, but the context graph turns 40 features of
accumulated knowledge into something no ad-hoc prompting can match.

## Layer 7: Knowledge Synthesis and Agent Onboarding

- Synthesize project knowledge from review history: patterns (approved
  repeatedly), anti-patterns (rejected), PM preferences (derived from
  correction data)
- Include synthesized knowledge in context packets as "project brief"
- Implementation notes from comments become domain-specific onboarding
- Track knowledge freshness: patterns confirmed recently rank higher
- Knowledge is regenerated periodically — cached synthesis that updates
  when new reviews land

## Layer 8: Driving Philosophies

- `philosophies` table: text, version, status, created_at, superseded_at
- `philosophy_versions` table for history tracking
- `tillr philosophy add/list/show/history` CLI commands
- Philosophies included in every context packet
- Philosophy PRs: agents propose amendments, PM approves/rejects

## Layer 9: Universal PR Pipeline

- Unified review pipeline for all change types: code, philosophy,
  cycle template, knowledge, **style rule**, spec
- Validation per type: code → build/test, philosophy → conflict check,
  cycle → schema validation, knowledge → freshness check, style rule
  → example-pair required for blocking severity
- Inbox shows all pending proposals, not just code PRs

### Layer 9b: Style Guide as First-Class Artifact

- `style_rules` table: name, description, severity, invalid_example,
  valid_example, scope (file globs / tags), created_at, superseded_at
- Severity levels: `blocking` / `requires-justification` / `advisory`
- `tillr style add/list/show/history` CLI commands
- Style enforcer agent role with envelope = diff + applicable rules
- Style-rule PRs carve out exceptions (e.g., "rule X doesn't apply
  inside `internal/json/bootstrap.go`") and go through the universal
  PR pipeline
- Distinct from synthesized anti-patterns (Layer 7) — both coexist:
  curated rules enforce what the PM has thought to encode, synthesized
  brief catches the rest
- See [story 25](./stories/25-style-enforcer-async-dialogue.md) for
  the worked example

## Layer 10: Metrics, Estimation, and Reporting

- Velocity tracking, cycle time, PM time per review
- Estimation by analogy from completed features
- Tech debt dashboard with severity categorization
- Automated retrospectives with recommendations as PRs
- Stakeholder reports generated from graph data

## Layer 11: Hierarchical Org Structure (far future)

Layers 1-10 develop the flat consulting-firm model: one project, one
human PM, agents with roles. Layer 11 makes the structure **recursive**:
a tillr ORG sits above projects; each project may have a human PM or
an *agent-PM* that escalates to a director.

- `orgs` table: name, projects[], philosophies[], capacity allocations
- Org-level philosophies propagate to all projects' context packets
- Director dashboard: cross-project view, escalations from PMs,
  capacity allocation, aggregated stakeholder reports
- Cross-project decision propagation (with `scope` field on decisions)
- Recursive escalation: feature owner → PM → director, with default
  thresholds + per-org overrides
- Agent-PM mode: an agent acts as PM for routine decisions, escalates
  to a human director for non-routine

The hierarchy IS the context-management system: each level adds
filtered context downward and summarized rollups upward. Solves the
"context overload" problem the original framing identified — nobody
reads everything; each role gets the slice appropriate to its
altitude.

See [story 26 (Olivia)](./stories/26-olivia-director-hierarchy.md) for
the worked example.

This is **Stage 8 — far future.** Requires Stages 1-7 mature. Highest
risk because: new data model, aggregate summarization at scale (relies
on summarization quality earlier stages have proven), cross-project
context propagation (research problem: which decisions propagate?),
agent-PM trust calibration (only meaningful once human-PM is solid).

For most teams, the flat single-project model is sufficient. Don't
build this until you have at least 3 mature tillr projects feeling
the pain.

---

« [Consulting-firm overview](./README.md) · [Roadmap](./roadmap.md) · [Open questions](./open-questions.md) · [All stories](./stories/README.md)
