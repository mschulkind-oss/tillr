# Implementation Layers

Build this incrementally. Each layer delivers value independently.

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
  cycle template, knowledge, spec
- Validation per type: code → build/test, philosophy → conflict check,
  cycle → schema validation, knowledge → freshness check
- Inbox shows all pending proposals, not just code PRs

## Layer 10: Metrics, Estimation, and Reporting

- Velocity tracking, cycle time, PM time per review
- Estimation by analogy from completed features
- Tech debt dashboard with severity categorization
- Automated retrospectives with recommendations as PRs
- Stakeholder reports generated from graph data

---

« [Consulting-firm overview](./README.md) · [Open questions](./open-questions.md) · [All stories](./stories/README.md)
