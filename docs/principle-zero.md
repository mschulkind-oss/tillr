# Principle Zero

> **All fixes are systematic and architectural. We do not tell agents
> to do things the right way and expect change. Promises are not
> change. If a mistake was made, fix something structurally. The
> first priority is a tool change, the last is agent instructions.
> Agents are unreliable. Tools are not. Design accordingly.**

## Why

Agents drift. Their context changes session to session. Instructions
in prompts decay — the new instance you spawn next week may have a
different model version, a recompacted system prompt, or a different
operator who didn't read the same docs. Humans drift too: future-you
won't reread "remember to run the linter before committing." A
mitigation that depends on someone (agent or human) remembering to do
the right thing is not a fix.

Tools, by contrast, do what they are built to do. A pre-commit hook
that runs the linter doesn't forget. A state machine that won't
advance past a missing-test transition cannot be talked into it. A
data model that lacks the field cannot store it. A lifecycle that
loads context, runs work, and persists the result *as a process*
doesn't depend on the agent inside it remembering to call append.

This is the difference between a system that works because of
discipline and a system that works because of design.

## The fix priority

When a problem surfaces — a bad commit lands, an agent goes
off-script, a spec is misinterpreted, a check is skipped — work the
fix down this list:

### 1. Tool change *(first, always)*

Make the wrong path impossible, or at least unrewarding, at the tool
or protocol level. Examples:

- A check that fails the build / commit / cycle step.
- A CLI flag default that flips the wrong direction to off.
- A type that won't compile if the constraint is violated.
- A hook that fires automatically and does the right thing.
- An orchestrator process that handles the lifecycle so the agent
  can't skip a step.

### 2. Structural change

If a tool change isn't directly possible, reshape the data model, the
cycle, the queue, the dispatcher, the file layout — until the wrong
thing is no longer the natural option.

- Add a column / state / artifact that makes the constraint
  representable.
- Split a step that conflates two concerns.
- Move ownership: who creates a thing isn't who consumes it; the
  consumer's contract is the constraint.

### 3. Agent instructions *(last resort, stopgap only)*

Only when 1 and 2 are genuinely impossible *and* the cost of a
tool/structural fix is wildly disproportionate to the bug.

When you reach for an instruction-style fix, treat it as a TODO for a
real fix. File it as a feature in tillr; revisit until the structural
fix exists.

## How to recognize a non-fix

You are about to apply Principle Zero incorrectly when you find
yourself writing or saying:

- "Going forward, the agent should…"
- "We'll make sure to…"
- "Let me add a note in the prompt that says…"
- "Just remember to…"
- "From now on…"

Stop. Replace with: *what tool, hook, schema, or state machine would
make this impossible?*

## Concrete consequences for tillr

Tillr is the orchestrator layer for tillr-managed projects. That
makes Principle Zero load-bearing for the whole product:

- **Persona context lifecycle.** Loading a persona's context, running
  the work, persisting the result is the **orchestrator process's
  job**, not a sequence the persona prompt asks the agent to follow.
  The agent may forget to call `tillr persona append`; the orchestrator
  will not.
- **Compaction.** Auto-trigger when thresholds are crossed, at the
  orchestrator boundary. Don't tell anyone to remember to compact.
- **Quality gates.** gofmt, lint, tests live in pre-commit and CI,
  not in agent prompts saying "run the linter before committing."
- **Conductor state.** Hooks write to `swarf/conductor.md` after
  significant actions, not the conductor remembering to write.
- **Retros.** Run automatically post-session, not when someone runs
  `tillr retro`.
- **Commits.** Pre-commit enforces; agents cannot bypass without
  explicit human authorization.
- **Spec quality.** A feature with no spec cannot be claimed by a
  persona — the queue refuses, not the persona's prompt warning.

When designing any new feature, the test is:

> *Could an unreliable agent following this protocol produce a bad
> outcome?*

If yes, the protocol needs more tool/structural enforcement before it
ships.

## Scope

- **Tillr itself.** How we build tillr. CI, agent prompts in
  `.claude/agents/`, persona configs, retros, fix-it loops.
- **Every repo tillr manages.** Tillr's job is to be the tool layer
  that enforces correctness for the projects under it. When tillr
  initializes a managed project, Principle Zero is part of what gets
  set up.

A tillr-managed project inherits Principle Zero by virtue of using
tillr's orchestrator. The orchestrator is the tool; the project's
use of it is the structural enforcement.

---

Established 2026-04-28. Linked from `AGENTS.md`,
`docs/consulting-firm/README.md`, and embedded in `tillr init` for
every managed project.
