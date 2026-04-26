# Tillr as a Consulting Firm

Exploring the model where the user is a client, tillr is the engagement
manager, and agents are the engineering team. The lens: what does it
feel like to manage a team that works in minutes instead of weeks — and
what breaks when you try?

## Thesis in one paragraph

Treat the feature ticket as the communal workspace, and let every
actor (agents of any role, the human PM, even future agents reading
history) speak to each other through threaded **comments** on tickets.
Everything else — context graph, knowledge synthesis, decision search,
stakeholder reports, hierarchical org structure — is a derived layer
over that comment substrate. Build it as a 10-layer onion (plus
foundation + far-future hierarchy): Layer 1 (just comments) is valuable
alone; each subsequent layer compounds on the ones below.

## Contents

- **[Stories](./stories/README.md)** — 29 narrative scenarios with
  named personas. The index is sorted by **stage** (the roadmap
  ordering), not file number, so reading top-to-bottom shows the
  evolution. Each story surfaces load-bearing mechanics and explicit
  gaps.
- **[Roadmap](./roadmap.md)** — 8 shipping stages (plus Stage 0
  foundational), each with stories unlocked, risk, effort, and
  validation criteria. The "build this in this order" doc.
- **[Implementation layers](./implementation-layers.md)** — the
  architectural decomposition. 10 numbered layers + sub-layers (2b,
  4b, 9b) + foundational adapter + Layer 11 (hierarchy).
- **[Open questions](./open-questions.md)** — 39 unresolved design
  questions with tentative leans.

## Suggested reading order

For someone new to the doc:

1. [Story 22 (Derek)](./stories/22-derek-progressive-disclosure.md) —
   why Layer 1 (just comments) is already valuable.
2. [Story 1 (Priya)](./stories/01-priya-solo-pm.md) — the decisions
   summary in the inbox. Most compressed demonstration of value.
3. [Story 24 (Meera)](./stories/24-questionnaires-as-checkpoints.md) —
   tunable oversight via cycle questionnaires.
4. [Story 25 (Henry)](./stories/25-style-enforcer-async-dialogue.md) —
   style guide + enforcer agent + async reviewer↔implementer dialogue
   (the substrate for multi-agent coordination).
5. [Story 7 (Kai)](./stories/07-kai-context-packet.md) — the context
   packet in full. The highest-leverage mechanic.
6. [Story 8 (Ava)](./stories/08-ava-knowledge-synthesis.md) — project
   knowledge synthesized from review history.
7. [Story 26 (Olivia)](./stories/26-olivia-director-hierarchy.md) —
   far-future: director / nested PM hierarchy. Redeems the original
   "consulting firm" framing.
8. [Story 23 (failure)](./stories/23-context-graph-failure.md) — the
   confessed blind spot. Read this before trusting the model.
9. [Story 28 (Sasha)](./stories/28-sasha-building-the-mvp.md) — how a
   real team adopts this stage by stage.
10. [Roadmap](./roadmap.md) — the build order with risk and
    validation per stage.
11. [Implementation layers](./implementation-layers.md) — the
    architecture.
12. [Open questions](./open-questions.md) — what's still unresolved.

For someone READY to build, jump straight to
[roadmap.md](./roadmap.md) → start with Stage 0 + Stage 1.

## Where this fits in tillr docs

This directory extends tillr's existing design vocabulary. See also:

- [VISION.md](../VISION.md) — the "cockpit, not autopilot" framing and
  iteration cycles that this builds on.
- [driving-motivation.md](../driving-motivation.md) — the implicit
  tracking principle that makes free comment-capture possible.
- [user-stories/](../user-stories/README.md) — foundational stories
  for the core tillr flow (claim/submit/human-qa, workstreams).
- [user-stories-agent-prs.md](../user-stories-agent-prs.md) — local
  PR records. This proposal generalizes PRs to philosophy/cycle/
  knowledge/style-rule changes (Layer 9).
- [user-stories-merge-queue.md](../user-stories-merge-queue.md) —
  isolated QA and sequential merge. Orthogonal: this proposal adds
  the conversation layer, not the isolation mechanics.
- [user-stories-agent-devenv.md](../user-stories-agent-devenv.md) —
  worktree + port allocation. Story 21 returns the worktree path in
  the claim response — this is the hand-off point.
- [user-stories-as-process.md](../user-stories-as-process.md) —
  four options for where user stories live in tillr. Open question
  16 leans "stories as specs" and inherits whatever that doc settles
  on.

## Status

This is a **mature brainstorm with a clear shipping plan** — not yet a
complete spec, but ready to start building. The stories are worked
through consistently, the 10-layer roadmap is sequenced into 8
shippable stages with risk and validation criteria per stage, and the
open questions are unusually honest about what's undecided.

The titular "consulting firm" metaphor was originally under-delivered
— the early doc developed "human PM + agents with roles" rather than
the full director/PM/engineer hierarchy the title promised. **Story 26
(Olivia) redeems that framing** by adding the recursive org structure
(director → nested PMs → leaf agents) as a far-future Stage 8. The
earlier stages remain the flat model; the hierarchy is layered on top
once foundations are stable.

Confidence gradient (highest to lowest):

1. Comments as foundation (story 22 makes this airtight)
2. Context packet shape (stories 7 and 21 show structure concretely)
3. Cycle questionnaires as oversight dial (story 24 — concrete
   mechanism that resolves the mid-flight race condition from stories
   2 and 21)
4. Async reviewer↔implementer dialogue via cycle-state + comments
   (story 25 — same loop as a real engineering org's PR review)
5. Cross-feature async coordination via the same substrate
   (story 27 — derivative of #4)
6. Universal PR pipeline for heterogeneous change types
7. Style guide enforcement as a cycle role with focused context
   envelope (story 25)
8. Platform adapter / agent-platform agnosticism (story 29 —
   confirmed via Copilot research)
9. Staged delivery / first-slice MVP (story 28 — project mgmt, not
   novel)
10. Estimation-by-analogy (story 17)
11. Knowledge synthesis (known brittle — acknowledged in story 8)
12. Cross-feature coordination at the level of code-level
    dependencies (story 23 blind spot remains)
13. Hierarchical org structure (story 26 — far future, complex,
    untested)
