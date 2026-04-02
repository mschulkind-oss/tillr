# Tillr: Vision

## The Problem We're Solving

AI agents can write code, refactor systems, and ship features — but they can't decide *what matters*. They don't know when something looks right. They can't feel the difference between "technically correct" and "actually good." They need a human in the loop, and right now, that loop is broken.

Today's agentic development is ad-hoc. A developer kicks off an agent, waits, reviews a wall of changes, accepts or rejects, and repeats. There's no structured workflow. No visibility into what the agent is doing or why. No way to steer priorities mid-flight. No history to learn from. Every session starts from zero.

The result is predictable: wasted compute, inconsistent quality, developer fatigue, and a nagging sense that we're using a jet engine to power a bicycle. The agents are powerful. The workflow around them is not.

## The Vision

Tillr is the project manager that sits between humans and agents.

It doesn't replace agents — it gives them structure. It doesn't replace humans — it amplifies their judgment. It defines clear iteration cycles and ensures every piece of work flows through a structured pipeline with human checkpoints. It captures everything, forgets nothing, and makes the entire process visible and steerable.

Think of it as a cockpit, not autopilot. The agents are the engines. The human is the pilot. Tillr is the instrument panel, the flight plan, and the control surfaces.

Every feature, every refinement, every bug fix moves through a defined tillr: plan → implement → review → approve. Within each phase, specialized agents play distinct roles — designing, building, testing, judging — while humans retain the authority to steer, pause, or redirect at any point. The cycle converges toward quality through iteration, not through luck.

## Human Utility

Tillr is built around a simple belief: **the human's time is the scarcest resource.** Every design decision flows from this.

### See everything at a glance

A live-updating web dashboard shows what's in progress, what's waiting for review, and what's shipped. No digging through terminal sessions or chat logs. The state of your project is always one glance away.

### Steer without interrupting

Priorities shift. Requirements evolve. With tillr, humans adjust direction through structured inputs — reprioritizing the roadmap, refining requirements, adding context — without breaking the agents' flow. The tool mediates, so neither side blocks the other.

### QA as a first-class checkpoint

Nothing ships without human approval. QA isn't an afterthought bolted onto the end — it's a defined stage in every cycle. Agents prepare the work. Humans make the call. This isn't about distrust; it's about maintaining the standard.

### A searchable history of everything

Every decision, every iteration, every piece of feedback is captured in a local SQLite database. Humans can search it to understand why something was built a certain way. Agents can query it to avoid repeating past mistakes. Institutional knowledge accumulates instead of evaporating between sessions.

### Roadmap as a living document

The roadmap isn't a static list in a markdown file. It lives in the tool, is visible and prioritized, and evolves through structured conversation between humans and agents. Requirements flow through tillr, are captured as concrete artifacts, and can be visualized in context.

## Quality Output

Speed without quality is just mess-making at scale. Tillr encodes quality into the process itself.

**Iteration cycles with convergence.** Work progresses through defined rounds. A judge agent scores each iteration against criteria. Scores trend upward or the cycle surfaces the problem. This prevents both infinite loops and premature completion.

**Specialized agent roles.** A single agent doing everything produces single-perspective output. Tillr orchestrates multiple roles — designer, developer, QA, judge — each bringing a different lens to the same work. The designer cares about coherence. The developer cares about correctness. QA cares about edge cases. The judge cares about the whole.

**Human QA as the final gate.** Agents can get very good at satisfying automated criteria while missing the point entirely. The human checkpoint exists because some things — taste, priorities, user empathy — can't be scored by a function.

**Structured cycles prevent waste.** Without structure, agents will happily iterate forever, burning tokens and producing diminishing returns. Tillr's predefined cycles encode best practices: when to stop, when to escalate, when to ship.

## Design Principles

**CLI-first.** Agents and humans interact through the same powerful command-line interface. The CLI is the source of truth, the primary control surface, and the foundation everything else builds on. If it can't be done from the CLI, it can't be done.

**Data-rich.** SQLite stores everything — plans, iterations, scores, decisions, history. Nothing is lost. The database is the system's memory, and it's always queryable, always local, always yours.

**Visible.** The web viewer makes the invisible visible. It doesn't add functionality — it renders the data the CLI already manages into something a human can absorb at a glance. Read-only by design. The CLI acts, the viewer observes.

**Opinionated.** Tillr ships with predefined cycles that encode best practices for common workflows: UI refinement, feature implementation, roadmap planning, bug triage. You can start productive work immediately without designing your own process.

**Extensible.** When the built-in cycles don't fit, define your own. Custom cycles for custom workflows, with the same structured pipeline, the same quality gates, the same visibility.

**Open.** Designed for open source from day one. The architecture, the data formats, the extension points — all built with the assumption that others will read, use, and build on this.

## Who This Is For

Tillr is for developers who use AI agents for real work — not demos, not experiments, but production software development. People who have felt both the power and the friction of agentic tools. Who want the output quality to match the development speed. Who know that "just let the agent handle it" isn't a strategy.

If you're using Copilot, Gemini, Claude Code, or similar tools and you've ever thought *"this is powerful but chaotic"* — tillr is the structure you're missing.

## The Future We're Building Toward

We believe human-agent collaboration will become as natural and productive as human-human collaboration. But that future doesn't arrive by accident. It requires tools purpose-built for this new paradigm — tools that respect both the agent's capabilities and the human's judgment.

Tillr is a bet that the right abstraction isn't a better prompt or a smarter agent. It's a better *process*. One where humans and agents each do what they're best at, with clear interfaces between them, and where the work product gets better every cycle.

The age of ad-hoc agentic development is ending. What comes next should be structured, visible, and under human control. That's what we're building.
