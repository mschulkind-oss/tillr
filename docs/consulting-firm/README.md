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

- **[MVP](./mvp.md)** — *the post-reset shipping plan*. 1-2 weeks of
  focused build to a dogfoodable conductor + persona system. Read
  this first if you're going to build.
- **[Stories](./stories/README.md)** — 31 narrative scenarios with
  named personas. The index is sorted by **stage** (the roadmap
  ordering), not file number, so reading top-to-bottom shows the
  evolution. Each story surfaces load-bearing mechanics and explicit
  gaps.
- **[Roadmap](./roadmap.md)** — long-term staged ordering. Stage 0 =
  the MVP. Subsequent stages build on MVP infrastructure.
- **[Implementation layers](./implementation-layers.md)** — the
  architectural decomposition. Conductor + persona foundation,
  numbered layers 1-10 + sub-layers (2b, 4b, 9b) + Layer 11
  (hierarchy).
- **[Open questions](./open-questions.md)** — 51 unresolved design
  questions with tentative leans.

## Suggested reading order

For someone new to the doc:

1. [Story 30 (Rui)](./stories/30-rui-conductor-pattern.md) — the
   post-reset foundation: conductor + persona pattern with swarf-
   stored context files. **Start here.**
2. [Story 22 (Derek)](./stories/22-derek-progressive-disclosure.md) —
   why Layer 1 (just comments) is already valuable.
3. [Story 1 (Priya)](./stories/01-priya-solo-pm.md) — the decisions
   summary in the inbox. Most compressed demonstration of value.
4. [Story 31 (Yael)](./stories/31-yael-tui-primary-interface.md) —
   the TUI as primary inspection surface.
5. [Story 24 (Meera)](./stories/24-questionnaires-as-checkpoints.md) —
   tunable oversight via cycle questionnaires.
6. [Story 25 (Henry)](./stories/25-style-enforcer-async-dialogue.md) —
   style guide + enforcer agent + async reviewer↔implementer dialogue
   (the substrate for multi-agent coordination).
7. [Story 7 (Kai)](./stories/07-kai-context-packet.md) — the context
   packet in full. The highest-leverage mechanic.
8. [Story 26 (Olivia)](./stories/26-olivia-director-hierarchy.md) —
   far-future: director / nested PM hierarchy.
9. [Story 23 (failure)](./stories/23-context-graph-failure.md) — the
   confessed blind spot.
10. [MVP](./mvp.md) — the concrete shipping plan.
11. [Roadmap](./roadmap.md) — long-term ordering after MVP.
12. [Implementation layers](./implementation-layers.md) — the
    architecture.
13. [Open questions](./open-questions.md) — what's still unresolved.

For someone READY to build, jump straight to **[mvp.md](./mvp.md)** —
that's where the post-reset shipping plan lives.

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

1. Comments as foundation (story 22 makes this airtight; already
   shipped in the post-reset skeleton)
2. **Conductor + persona pattern (story 30)** — proven on a previous
   project; high confidence in the shape, calibration uncertain
3. Persona context files in swarf (story 30) — load/append/die
   lifecycle is well-trodden
4. Async reviewer↔implementer dialogue via cycle-state + comments
   (story 25 — same loop as a real engineering org's PR review)
5. Cross-feature async coordination via the same substrate
   (story 27 — derivative)
6. Cycle questionnaires as oversight dial (story 24)
7. Universal PR pipeline for heterogeneous change types
8. TUI as primary inspection surface (story 31 — Bubble Tea is
   well-trodden, calibration of "is the web UI even needed" is open)
9. Style guide enforcement as a cycle role (story 25)
10. Auto-compaction at 20k words (mechanic clear; quality calibration
    only proves out in real usage)
11. Retro from session transcripts (story 30 — depends on transcript
    parsing quality)
12. Estimation-by-analogy (story 17)
13. Knowledge synthesis (brittle — story 8; partly subsumed by
    persona contexts post-reset)
14. Cross-feature coordination at the level of code-level
    dependencies (story 23 blind spot remains)
15. Multi-platform adapter (story 29 — *deferred* for MVP; Claude only)
16. Hierarchical org structure (story 26 — far future, complex,
    untested)
