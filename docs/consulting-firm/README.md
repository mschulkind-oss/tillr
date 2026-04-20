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
stakeholder reports — is a derived layer over that comment substrate.
Build it as a 10-layer onion: Layer 1 (just comments) is valuable
alone; each subsequent layer compounds on the ones below.

## Contents

- **[Stories](./stories/README.md)** — 23 narrative scenarios with
  named personas. Each story surfaces load-bearing mechanics and
  explicit gaps. Start with stories 1, 7, 8, 22 to see the core
  thesis in action.
- **[Implementation layers](./implementation-layers.md)** — the
  10-layer roadmap. Each layer ships independently.
- **[Open questions](./open-questions.md)** — 16 unresolved design
  questions with tentative leans.

## Suggested reading order

1. [Story 22 (Derek)](./stories/22-derek-progressive-disclosure.md) —
   why Layer 1 (just comments) is already valuable.
2. [Story 1 (Priya)](./stories/01-priya-solo-pm.md) — the decisions
   summary in the inbox. The most compressed demonstration of value.
3. [Story 7 (Kai)](./stories/07-kai-context-packet.md) — the context
   packet in full. The highest-leverage mechanic.
4. [Story 8 (Ava)](./stories/08-ava-knowledge-synthesis.md) — project
   knowledge synthesized from review history.
5. [Story 21 (Agent-1)](./stories/21-agent-implementer-perspective.md) —
   the agent's perspective, including the claim-response JSON contract.
6. [Story 23 (failure)](./stories/23-context-graph-failure.md) — the
   confessed blind spot. Read this before trusting the model.
7. [Implementation layers](./implementation-layers.md) — the roadmap.
8. [Open questions](./open-questions.md) — what's still unresolved.

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
  knowledge changes (Layer 9).
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

This is a **mature brainstorm, not a polished spec.** The stories are
worked through consistently, the 10-layer roadmap is thought through,
and the open questions are unusually honest about what's undecided.
The titular "consulting firm" metaphor is under-delivered — the doc
develops "human PM + agents with roles" rather than a
director/PM/engineer hierarchy. If we want the richer hierarchy, that
would be an extension, not a synthesis.

Confidence gradient, high → low:

1. Comments as foundation (story 22 makes this airtight)
2. Context packet shape (stories 7 and 21 show the structure concretely)
3. Universal PR pipeline for heterogeneous change types
4. Estimation-by-analogy (story 17)
5. Knowledge synthesis (known brittle — acknowledged in story 8)
6. Cross-feature coordination (story 3 has a timing problem flagged but unsolved)
7. Context graph code-level edges (story 23 is the confessed blind spot)
