# Core Concepts

## Projects and Initialization

A tillr project is a directory containing a `.tillr.json` config file and a `tillr.db` SQLite database. Running `tillr init` creates both:

```
my-app/
├── .tillr.json    # Project configuration
├── tillr.db       # All project data (features, milestones, events, …)
└── …your source code…
```

Tillr finds your project by walking up from the current directory until it finds `.tillr.json`, so you can run commands from any subdirectory.

## Features and Tillr States

A **feature** is the primary unit of work. Every feature moves through a linear pipeline of states:

```
draft → planning → implementing → agent-qa → human-qa → done
                                                 ↑
                                              blocked
```

| State | Meaning |
|-------|---------|
| `draft` | Captured idea, not yet planned |
| `planning` | Being broken down into work items |
| `implementing` | Under active development by an agent |
| `agent-qa` | Agent is self-reviewing the work |
| `human-qa` | Waiting for human approval (the quality gate) |
| `done` | Shipped |
| `blocked` | On hold — a dependency or external issue prevents progress |

The transition from `human-qa` to `done` is the critical human-in-the-loop gate. No feature ships without explicit human approval.

## Milestones and Milestone Gating

A **milestone** groups related features into a deliverable. Milestones track aggregate progress and can gate releases — all features in a milestone must reach `done` before the milestone is complete.

## Feature Dependencies

Features can depend on other features. A feature cannot enter `implementing` until all its dependencies are `done`. Declare dependencies at creation time:

```bash
tillr feature add "OAuth provider" --depends-on feat-1
```

## Iteration Cycles

A **cycle** is a structured workflow that moves a feature through its states. Each cycle defines agent roles (designer, developer, QA, judge), iteration rounds, scoring criteria, and convergence rules. See [iteration-cycles](../../design/iteration-cycles/README.md) for full details.

## Human QA Workflow

When a feature reaches `human-qa`, it appears in the QA queue. You review the work, then either approve (moves to `done`) or reject (sends it back to `implementing` for another iteration). Every QA decision is recorded with notes.

## SQLite Storage

All data lives in a single `tillr.db` file — features, milestones, work items, events, QA results, and heartbeats. This makes projects portable (copy the file), inspectable (open it with any SQLite client), and version-controllable (back it up alongside your code).

---

« [User Guide](./README.md)
