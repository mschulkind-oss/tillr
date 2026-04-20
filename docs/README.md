# Tillr Documentation

Tillr is a human-in-the-loop project management tool for agentic software
development. This directory holds the vision, design, user-facing guides,
and narrative design work (user stories).

## Start here

- **[VISION.md](./VISION.md)** — the big-picture framing: "cockpit, not
  autopilot"
- **[driving-motivation.md](./driving-motivation.md)** — the core problem
  and the Implicit Tracking Principle
- **[guides/user-guide/README.md](./guides/user-guide/README.md)** — how to actually
  use tillr (CLI + web UI)
- **[guides/walkthrough.md](./guides/walkthrough.md)** — visual tour of
  every page

## Directory map

```
docs/
├── README.md                      ← you are here
├── VISION.md                      big-picture framing
├── driving-motivation.md          the core problem
├── design/                        design decisions
│   └── iteration-cycles/          8 predefined cycles, overview, registry
├── guides/                        how to use tillr
│   ├── user-guide/
│   ├── walkthrough.md
│   └── copilot-integration.md
├── screenshots/                   UI screenshots (PNG)
├── user-stories/                  core user-story set (10 personas)
│   ├── stories/                   one file per story
│   ├── friction-points.md         derived roadmap table
│   └── key-insight.md             one-paragraph distillation
├── consulting-firm/               extended proposal: context & conversation layer
│   ├── stories/                   23 stories, one per file
│   ├── implementation-layers.md   10-layer build roadmap
│   └── open-questions.md          16 unresolved design questions
├── user-stories-as-process.md     where stories live as first-class entities
├── user-stories-agent-prs.md      local PR records, agent worktrees
├── user-stories-merge-queue.md    isolated QA, sequential merge
├── user-stories-agent-devenv.md   running services for UI QA
├── user-stories-dev-environments.md  worktree dev-env scope
└── user-stories-cdp-proxy.md      multi-agent browser isolation
```

## Reading paths

### "I want to understand tillr's vision"

1. [VISION.md](./VISION.md) — the big picture
2. [driving-motivation.md](./driving-motivation.md) — why this, now
3. [user-stories/](./user-stories/README.md) — the core flow in narrative form
4. [design/iteration-cycles/](./design/iteration-cycles/README.md) — how cycles
   move work from "not done" to "done"

### "I want to use tillr"

1. [guides/user-guide/README.md](./guides/user-guide/README.md) — the reference
2. [guides/walkthrough.md](./guides/walkthrough.md) — screenshots
3. [guides/copilot-integration.md](./guides/copilot-integration.md) — if
   you're wiring tillr into an agent CLI

### "I want to design tillr's next phase"

1. [user-stories/](./user-stories/README.md) — the core flow (what works
   today, what's missing)
2. [consulting-firm/](./consulting-firm/README.md) — the context &
   conversation proposal (comments, decisions, knowledge synthesis)
3. [user-stories-as-process.md](./user-stories-as-process.md) — stories as
   first-class entities in tillr
4. The "pillar" docs for isolation mechanics:
   - [user-stories-agent-prs.md](./user-stories-agent-prs.md)
   - [user-stories-merge-queue.md](./user-stories-merge-queue.md)
   - [user-stories-agent-devenv.md](./user-stories-agent-devenv.md)
   - [user-stories-dev-environments.md](./user-stories-dev-environments.md)
   - [user-stories-cdp-proxy.md](./user-stories-cdp-proxy.md)

## Cross-references between design docs

- **[consulting-firm/](./consulting-firm/README.md)** extends the core
  [user-stories/](./user-stories/README.md) flow with a conversation
  layer. Stories 7 and 21 in consulting-firm show the context packet
  that supersedes the thin claim response called out as a gap in
  [user-stories/stories/06](./user-stories/stories/06-agent-claiming-from-queue.md).
- **[user-stories-agent-prs.md](./user-stories-agent-prs.md)** introduces
  tillr PRs as local records. Consulting-firm Layer 9 generalizes "PR"
  to philosophy / cycle template / knowledge changes — see
  [consulting-firm/implementation-layers.md](./consulting-firm/implementation-layers.md).
- **[user-stories-merge-queue.md](./user-stories-merge-queue.md)**
  introduces isolated QA; consulting-firm layers conversation on top
  without changing isolation or merging.
- **[user-stories-agent-devenv.md](./user-stories-agent-devenv.md)**
  describes the worktree + port allocation consulting-firm story 21
  returns in the claim response.
- The **CDP proxy design** in
  [user-stories-cdp-proxy.md](./user-stories-cdp-proxy.md) concluded
  that existing MCP gateways solve most of the multi-agent browser
  isolation problem — see the most recent commits for the current
  position.

## Adding new docs

- Keep individual files under ~800 lines / ~30KB. If a doc grows
  beyond that, split it into a subdirectory with a `README.md` index
  (see `user-stories/` or `consulting-firm/` for examples).
- Story collections should use one-file-per-story with a shared
  `README.md` index listing persona + theme.
- Link laterally between related docs instead of duplicating content.
