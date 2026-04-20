# User Stories — Core Tillr Flow

Seven human users and three agent users working with tillr. Each story
follows them through real workflows, with exact technical steps and the
gaps we need to close.

This is the **foundation layer** of tillr's user-story work: the basic
install / claim / submit / QA flow. Later story sets explore extensions
to this flow — see [Where this fits](#where-this-fits).

## Contents

- **[Stories](./stories/README.md)** — 10 narratives, ~70 lines each,
  using the same 5-section template (context / today / proposed /
  tripping hazards / aha moment).
- **[Friction points](./friction-points.md)** — 23-row roadmap table
  mapping stories to gaps.
- **[Key insight](./key-insight.md)** — the one-paragraph distillation
  of what tillr is actually delivering.

## Shape of a story

Each file has the same structure:

1. **Context** — who the persona is, their project scale, their time budget
2. **What they see today** — the current flow, with its friction
3. **What they need** (or "What actually happens today") — the proposed
   improvements
4. **What would trip them up** — edge cases and failure modes
5. **The aha moment** — the payoff after they've used tillr for a while

## Where this fits

This set covers the **core flow**. Other story sets in `docs/` extend
specific aspects:

- [consulting-firm/](../consulting-firm/README.md) — context & conversation layer
  (comments, decisions, knowledge synthesis, philosophies)
- [user-stories-as-process.md](../user-stories-as-process.md) — four options
  for where user stories live as first-class entities in tillr
- [user-stories-agent-prs.md](../user-stories-agent-prs.md) — tillr PRs as
  local records, agent worktrees
- [user-stories-merge-queue.md](../user-stories-merge-queue.md) — isolated QA,
  sequential merge queue
- [user-stories-agent-devenv.md](../user-stories-agent-devenv.md) — worktree +
  port allocation for agents needing running services
- [user-stories-dev-environments.md](../user-stories-dev-environments.md) —
  dev environment management scope and boundaries
- [user-stories-cdp-proxy.md](../user-stories-cdp-proxy.md) — Chrome DevTools
  proxy design for multi-agent browser sharing
