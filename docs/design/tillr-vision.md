# Tillr: The Harness for Agentic Software Development

> Tillr puts humans "on the loop" — not reviewing every output, but engineering the system that produces better outputs.

## What Tillr Is

Tillr is a harness for agentic software development. It defines, governs, and improves the loops that AI agents work in, while giving humans visibility and control at the points that matter.

It is not a ticket tracker that agents happen to read. It is the control system itself.

## The Framework

Based on Martin Fowler's "Humans and Agents in Software Engineering Loops" (March 2026), there are three ways humans can relate to AI agents:

- **Out of the loop** ("vibe coding") — human says what, agent does everything. Works for throwaway code. Dangerous for anything maintained.
- **In the loop** — human reviews every agent output. Doesn't scale. Agents generate faster than humans can inspect.
- **On the loop** — human engineers *the harness*: the specs, quality checks, and workflow guidance that control agent behavior. Instead of fixing the output, you fix the system that produces the output.

Tillr is built for "on the loop."

## Two Loops

Fowler identifies two interconnected feedback systems in software development:

**The Why Loop** (human-owned): Iterating between ideas and working software. What to build, why, does it solve the problem. Humans own this because we're the ones who want what it produces.

**The How Loop** (agent-owned, human-governed): Nested implementation loops — feature specification, task decomposition, code generation, testing, review. Agents execute these loops. Humans design the harness that governs them.

### How This Maps to Tillr

| Concept | Tillr Implementation |
|---------|---------------------|
| Why Loop | Roadmap, workstreams, ideas, decisions |
| How Loop | Cycles (state machines), work items, agent sessions |
| The Harness | Cycle templates, human step placement, scoring thresholds |
| On-the-loop control | Human steps in cycles — approve, reject, redirect |
| Feedback signal | Cycle scores, QA results, audit trail |
| Agent self-evaluation | Judge steps, automated scoring |

## Tillr + Vantage

Tillr and Vantage are complementary:

| Tillr (Action) | Vantage (Thinking) |
|----------------|-------------------|
| What is the state of work? | What is the agent thinking? |
| Feature status, cycle state | Agent reasoning, design docs |
| Work item prompts and results | Freeform research, brainstorming |
| QA scores, pass/fail | Detailed QA analysis prose |
| Decision records (structured) | Decision exploration (unstructured) |
| Event audit trail | Agent thought process trace |
| Dashboard metrics | Narrative project understanding |

Together they give the human full situational awareness without requiring them to be in every loop.

## Core Abstractions

### Cycles: State Machines All the Way Down

A cycle is a configurable DFA (deterministic finite automaton) — an ordered sequence of steps, each owned by either an agent or a human. Cycles are the universal progression primitive in Tillr.

They attach to any entity: features, roadmap items, milestones, ideas, decisions, workstreams. Different entity types use different cycle templates, but the engine is the same.

```
CycleTemplate → defines the steps
CycleInstance → tracks progress through those steps for a specific entity
CycleStep    → { name, owner: agent|human }
```

Agent steps create work items. Human steps block until the human acts. Scores measure quality. Iterations allow looping when quality isn't met.

### The Entity Hierarchy

```
Project
  Workstream    — human-owned strategic thread (the why)
  Roadmap Item  — what to build, prioritized
  Milestone     — grouping toward a goal
  Feature       — the core unit of deliverable work
  Idea          — intake queue, pre-feature
  Decision      — architectural record (ADR)
```

Each can have cycles attached. Context flows down (parent provides constraints), status flows up (children report progress).

### Work Items: The Atomic Agent Task

Work items are what agents actually do. They're always scoped to a feature (the deliverable) and a cycle step (the context). Their lifecycle is fixed and simple: pending → claimed → active → done/failed. Complexity lives in the cycle that spawns them, not in the work item itself.

## The Agentic Flywheel

Fowler describes an evolutionary trajectory that Tillr enables:

1. **Agent self-evaluation** — agents analyze loop performance using scores, tests, metrics
2. **Recommendation generation** — agents propose improvements to the harness (better cycle templates, adjusted thresholds)
3. **Human review → automation** — humans initially evaluate proposals, then high-confidence improvements auto-approve
4. **Self-improving system** — the harness itself gets better over time

Tillr's idea intake pipeline already supports this: agents can submit ideas, which go through triage, become features, and get implemented through cycles.

## What Tillr Is Not

- **Not a Kanban board.** Agents don't need to see columns. Humans need status overviews, which the dashboard provides.
- **Not a sprint planner.** Agents work continuously. No time-boxing, no ceremonies.
- **Not a code review tool.** Agents evaluate their own work through judge steps and scoring. Humans intervene at human steps, not at every commit.
- **Not a micromanagement tool.** The whole point is that humans design the harness once, then the system runs. Human time is sacred — every touchpoint must be high-signal.

## The Story

A human has an idea. They drop it into Tillr. An agent triages it, assesses feasibility, and presents it for approval. The human approves — now it's a feature with a spec.

Tillr starts a cycle. The agent researches, designs, implements, tests. At each step, the cycle engine scores the output. If quality is met, it advances. If not, it loops. When the cycle reaches a human step, it stops and waits.

The human reviews — not the code, but the outcome. Does this solve the problem? Is the direction right? They approve or redirect. The cycle continues.

When it's done, the feature is done. The milestone updates. The roadmap reflects progress. The audit trail captures everything — every decision, every score, every human intervention.

The human never had to read a diff. They steered the ship by designing the route and checking the heading at waypoints. They were on the loop.

---

*References:*
- Martin Fowler, "Humans and Agents in Software Engineering Loops" (March 4, 2026) — https://martinfowler.com/articles/exploring-gen-ai/humans-and-agents.html
- Full tracker model research at docs/research/project-tracker-models.md
