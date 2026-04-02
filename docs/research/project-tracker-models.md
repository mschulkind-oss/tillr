# Project Tracker Models: Research and Synthesis for Tillr

> Research date: 2026-03-30
> Purpose: Inform tillr's entity model, particularly around state machines (cycles) as a universal primitive.

---

## Part 1: How Existing Trackers Model Work

### Jira

**Hierarchy**: Initiative > Epic > Story/Task/Bug > Sub-task

Jira's core abstraction is the *issue type* with a per-type *workflow* (a state machine of statuses with allowed transitions). Workflow schemes map issue types to workflows per-project, so epics can have different state flows than stories. This is Jira's deepest insight and its deepest problem: maximum configurability creates maximum complexity.

Key design choices:
- Workflows are per-issue-type, not per-instance. Every story in a project shares the same workflow.
- Custom fields extend any issue type, but field schemes are project-scoped.
- Boards (Kanban/Scrum) are *views* over workflow states, not first-class entities.
- Advanced Roadmaps add hierarchy levels (initiatives, themes) but they're a paid overlay, not core.

**What translates**: Per-type workflows. The idea that different *kinds* of work need different state machines.
**What doesn't**: The assumption that a human drags cards between columns. The ceremony of sprint planning and backlog grooming. The enormous configuration surface.

### Linear

**Hierarchy**: Workspace > Team > Project + Cycle > Issue > Sub-issue

Linear's model is deliberately simpler. Issues are the atomic unit. Projects group issues toward a deliverable (time-bound). Cycles are recurring time-boxes (sprints). Teams own workflows.

Key design choices:
- Workflows are per-team (not per-project or per-issue-type). All issues in a team share the same status flow: Backlog > Todo > In Progress > Done > Canceled.
- Projects have their own status (planned, in progress, completed, paused, canceled) separate from issue status.
- Cycles auto-roll: you set a cadence and they repeat. Unfinished issues roll forward.
- Views are saved filters, not structural entities.
- 2025-2026: AI features summarize threads, surface stalled issues, and integrate with external coding agents (Devin can deeplink Linear issues).

**What translates**: The distinction between project-level status and work-item-level status. The idea that the container (project) has its own tillr separate from its contents.
**What doesn't**: Fixed workflow per team. Cycles as time-boxes assume humans working in sprints. Agents don't need sprints.

### Shortcut (formerly Clubhouse)

**Hierarchy**: Objective > Milestone > Epic > Story > Task

Shortcut adds *Iterations* (time-boxed sprints that can span teams) and *Workflows* (configurable per-team status flows). Stories move through workflow states. Epics group stories. Milestones group epics toward a goal.

Key design choices:
- Workflows are customizable state machines with configurable states and categories (unstarted, started, done).
- Stories have a type (feature, bug, chore) that determines behavior (e.g., chores don't have estimates).
- Labels, custom fields, and groups provide flexible cross-cutting categorization.
- Milestones are the strategic layer connecting to OKRs or quarterly goals.

**What translates**: Story types with different behaviors. Workflow state categories (unstarted/started/done) as a meta-layer over custom states.
**What doesn't**: The assumption that humans manually move stories through states.

### Pivotal Tracker

**Hierarchy**: Project > Epic > Story (feature/bug/chore/release)

Pivotal Tracker's innovation was *automatic velocity-based planning*. You don't assign stories to iterations; Tracker fills iterations based on measured velocity. Stories have a fixed tillr: Unstarted > Started > Finished > Delivered > Accepted/Rejected.

Key design choices:
- Stories have a *fixed* workflow that cannot be customized. This is opinionated by design.
- Points measure complexity, not time. Velocity is points-per-iteration.
- The "Accepted/Rejected" terminal states encode a human approval gate directly into the workflow.
- Chores and bugs don't get point estimates (they're overhead, not deliverables).

**What translates**: The acceptance gate built into the workflow. Automatic scheduling based on capacity. The distinction between deliverables (features) and overhead (chores).
**What doesn't**: The rigid, non-customizable workflow. Points and velocity assume consistent human team throughput.

### Trello

**Hierarchy**: Board > List > Card > Checklist

Trello is a visual Kanban tool with almost no opinions about work modeling. Lists are columns. Cards move between lists. That's it. Structure is emergent, not enforced.

**What translates**: The radical simplicity. The idea that checklists (sub-tasks) live inside cards rather than being separate entities.
**What doesn't**: Everything about agentic work. Trello has no state machines, no workflows, no automation. It's pure manual.

### GitHub Projects / Issues

**Hierarchy**: Organization > Repository > Issue/PR > Task list items

GitHub Projects v2 (2022+) added custom fields, configurable status columns, and workflow automations. Issues can have task lists (sub-issues). Projects are views over issues across repos.

Key design choices:
- Status is a custom field on the project, not intrinsic to the issue. The same issue can have different statuses in different projects.
- Built-in automations: auto-set status on add, on close, on PR merge.
- GitHub Actions enable custom workflow automation triggered by project events.
- Issues and PRs are tightly coupled (PRs can close issues, link to projects).

**What translates**: Status as a per-context property (an issue's status depends on which project you're viewing it in). The tight coupling of work items to code artifacts (PRs, commits).
**What doesn't**: The minimal workflow engine. No state machine formalism, no transition guards, no human approval gates.

### Plane (Open Source)

**Hierarchy**: Workspace > Project > Module + Cycle > Issue > Sub-issue

Plane is a modern open-source tracker (Linear/Jira alternative) with a clean entity model.

Key design choices:
- *Modules* group issues by feature/theme (like epics). *Cycles* are time-boxes (like sprints). Issues can belong to both.
- Workflow states are customizable per-project with state groups (backlog, unstarted, started, completed, cancelled).
- Properties system: custom fields on issues.
- Self-hostable with PostgreSQL backend.

**What translates**: The dual grouping mechanism (thematic modules + temporal cycles). State groups as a universal categorization over custom states.
**What doesn't**: Still assumes human-driven workflows.

### Huly (Open Source)

Huly is an all-in-one platform (tracker + docs + chat + virtual office). Its tracker supports customizable workflows, subtasks, labels, milestones, and bidirectional GitHub sync. It's architecturally interesting (reactive, event-sourced) but its work model is conventional.

---

## Part 2: Agentic Work Patterns

### How AI Coding Agents Track Work Today

**Devin**: Assigns tasks via Slack/Teams tags or Linear ticket tags. Devin operates in a sandboxed workspace, provides progress updates, and can dispatch sub-tasks to other agents. Integrates with Linear for task intake. Tracks confidence levels and asks for human clarification when uncertain.

**OpenHands / SWE-agent**: Operate on GitHub issues. The issue *is* the task specification. The agent reads the issue, works in a sandbox, and produces a PR. No internal task tracking -- the work state is implicit in the code/PR state.

**Claude Code / Copilot Agents**: Session-based. No persistent work tracking. The human maintains context across sessions. This is the gap tillr fills.

### Key Insight: Agents Don't Track Work, Harnesses Do

No current coding agent has its own project management model. They all rely on external systems (Linear, GitHub Issues, or nothing at all). The tracking is either:
1. **External**: Agent reads from and writes to a human-designed tracker (Devin + Linear)
2. **Implicit**: Work state lives in code artifacts (PRs, branches, test results)
3. **Absent**: The human keeps it all in their head

Tillr occupies a unique position: it *is* the harness that tracks work for agents while giving humans visibility and control.

### The APM Framework (Agentic Project Management)

The open-source APM framework (github.com/sdi2200262/agentic-project-management) addresses context window limitations through:
- A *Manager Agent* that coordinates specialized *Implementation Agents*
- A *Memory Bank* (shared project logbook) for context retention across sessions
- *Handover Protocols* for transferring context between agents
- Spec-driven task decomposition

This validates tillr's approach: structured workflows with explicit state, not ad-hoc agent sessions.

### AWS AI-DLC (AI-Driven Development Tillr)

AWS open-sourced an adaptive workflow methodology that:
- **Adapts depth to complexity**: simple bug fixes skip planning; complex features get full requirements/architecture/design
- **Embeds human oversight at decision gates**: approved plans before implementation, stakeholder review of artifacts
- **Records every human action and approval**: full audit trail
- **Structures into three phases**: Inception (planning), Construction (implementation), Operations (deployment)

This is essentially what tillr's cycle types already do, but AI-DLC formalizes the *adaptive* selection of which workflow to use based on task complexity.

### Martin Fowler's "Why Loop" vs "How Loop"

Fowler distinguishes:
- **The Why Loop**: Humans decide what software should do. This involves iteration, learning, changing requirements. Humans own this.
- **The How Loop**: Agents produce code, tests, infrastructure. Agents own this.
- **Human positioning**: Not "in the loop" (reviewing every output) or "out of the loop" (full autonomy), but **"on the loop"** -- building and managing the mechanisms that guide agent behavior.

This maps directly to tillr's model: cycles define the *how loop* structure, human steps in cycles are the *on-the-loop* control points, and the roadmap/workstream layer is the *why loop*.

### What Agents Need That Traditional Trackers Don't Provide

1. **Machine-readable work definitions**: Not "implement login" but structured specs with acceptance criteria, context, constraints, and references that an agent can parse and act on.

2. **State machines with transition guards**: Not just "move from In Progress to Done" but "can only transition to Done if tests pass AND agent-QA scores above threshold AND human has approved."

3. **Automatic state progression**: Agents shouldn't manually update status. The system should advance state based on outputs (PR merged = code complete, QA passed = ready for review).

4. **Human checkpoints as first-class workflow steps**: Not an afterthought notification, but a defined state where the workflow *blocks* until a human acts.

5. **Context packaging**: When an agent picks up work, it needs everything -- the spec, prior results, related decisions, current scores, constraints -- in one payload. Traditional trackers scatter this across fields, comments, and linked issues.

6. **Audit trails with intent**: Not just "field changed from A to B" but "agent X transitioned to state Y because QA score was 0.85 (threshold: 0.80), with these specific findings."

7. **Iteration as a primitive**: Traditional trackers model linear progress (todo > doing > done). Agents often need to *loop* -- implement, test, fail, re-implement. The state machine must support cycles, not just sequences.

8. **Capacity measured in compute, not hours**: Velocity isn't "points per sprint." It's "work items per hour" or "tokens per feature." Throughput is continuous, not time-boxed.

---

## Part 3: Synthesis -- The Tillr Entity Model

### Design Principles

1. **State machines all the way down.** Every entity that progresses through states gets a cycle (configurable DFA). Not just features -- roadmap items, milestones, ideas, decisions, even the project itself.

2. **Two-layer model.** The *planning layer* (what to build, why, in what order) is human-owned. The *execution layer* (how to build it, with what steps) is agent-owned with human checkpoints.

3. **Cycles are the universal progression primitive.** A cycle is not a "sprint" or a "time-box." It is a state machine instance attached to any entity, defining how that entity progresses from inception to completion.

4. **Context flows down, status flows up.** Parent entities provide context and constraints to children. Children report status upward. The system aggregates automatically.

5. **Human time is sacred.** Every human touchpoint must be high-signal. The system should batch, prioritize, and present only what needs human attention, with all context pre-assembled.

### The Entity Hierarchy

```
Project
  |
  +-- Workstream (human-owned strategic thread)
  |     |
  |     +-- notes, links, child workstreams
  |
  +-- Roadmap Item (what to build, prioritized)
  |     |
  |     +-- [cycle: roadmap-planning]
  |     +-- linked Features
  |
  +-- Milestone (grouping toward a goal)
  |     |
  |     +-- [cycle: milestone-tracking]
  |     +-- Features in this milestone
  |
  +-- Feature (the core unit of deliverable work)
  |     |
  |     +-- [cycle: feature-implementation, ui-refinement, etc.]
  |     +-- Work Items (atomic agent tasks)
  |     +-- QA Results
  |     +-- Discussions, Decisions, Context
  |     +-- Dependencies (feature <-> feature)
  |
  +-- Idea (intake queue, pre-feature)
  |     |
  |     +-- [cycle: idea-triage]
  |
  +-- Decision (ADR, architectural record)
        |
        +-- [cycle: architecture-review]
        +-- supersedes chain
```

### The Cycle Model (Universal State Machine)

A cycle is an instance of a cycle type (template) attached to any entity. This is tillr's core differentiator.

```
CycleTemplate
  - name: string                    # e.g., "feature-implementation"
  - description: string
  - steps: CycleStep[]              # ordered list of states
  - applicable_to: EntityType[]     # which entity types can use this template
  - max_iterations: int?            # optional cap on loops
  - auto_advance_rules: Rule[]?     # conditions for automatic state progression

CycleStep
  - name: string                    # e.g., "develop", "human-review"
  - owner: "agent" | "human"        # who is responsible for this step
  - entry_criteria: Condition[]?    # must be true to enter this step
  - exit_criteria: Condition[]?     # must be true to leave this step
  - timeout: duration?              # auto-escalate if stuck
  - on_timeout: Action?             # what happens on timeout

CycleInstance
  - id: int
  - template: string               # which CycleTemplate
  - entity_type: string            # "feature", "roadmap_item", "milestone", etc.
  - entity_id: string              # the entity this cycle is attached to
  - current_step: int              # index into template.steps
  - iteration: int                 # how many times we've looped
  - status: "active" | "completed" | "failed" | "paused"
  - created_at, updated_at

CycleScore
  - cycle_id: int
  - step: int
  - iteration: int
  - score: float
  - notes: string
  - scored_by: string              # agent ID or "human"
```

#### What Changed from Current Model

The current model ties `CycleInstance.feature_id` directly to features. The new model replaces this with `entity_type` + `entity_id`, making cycles attachable to any entity. This is a single-column change in the database but a conceptual shift in the architecture.

Current:
```go
type CycleInstance struct {
    FeatureID string `json:"feature_id"`
    // ...
}
```

Proposed:
```go
type CycleInstance struct {
    EntityType string `json:"entity_type"` // "feature", "milestone", "roadmap_item", etc.
    EntityID   string `json:"entity_id"`
    // ...
}
```

### Entity Types and Their Cycles

| Entity | Example Cycle Templates | Human Steps |
|--------|------------------------|-------------|
| **Feature** | feature-implementation, ui-refinement, bug-triage | human-qa, human-review |
| **Roadmap Item** | roadmap-planning, collaborative-design | human-review, human-approve |
| **Milestone** | milestone-tracking | human-review (at completion) |
| **Idea** | idea-triage | human-approve (to become feature) |
| **Decision** | architecture-review | human-review, human-approve |
| **Discussion** | consensus-building | (all steps are human, but agents can draft) |

### Work Items: The Atomic Agent Task

Work items remain the lowest-level unit. They are what agents actually *do*. A work item is always scoped to a feature (the deliverable) and a cycle step (the context for why this work exists).

```
WorkItem
  - feature_id: string             # which feature
  - cycle_instance_id: int?        # which cycle step produced this
  - work_type: string              # "develop", "test", "review", "research"
  - status: "pending" | "claimed" | "active" | "done" | "failed"
  - agent_prompt: string           # structured instruction for the agent
  - result: string                 # agent's output
  - assigned_agent: string?
```

Work items don't need their own cycles. Their tillr is simple and fixed: pending > claimed > active > done/failed. This is intentional -- they're atomic. Complexity lives in the cycle that spawns them.

### How Work Flows Through the System

```
1. INTAKE
   Human adds idea via CLI/web/Slack
   -> Idea created (status: pending)
   -> [optional] idea-triage cycle starts automatically

2. PLANNING
   Idea approved -> Feature created (status: draft)
   Feature assigned to Milestone + Roadmap Item
   Human selects cycle template for the feature
   -> Cycle instance created (status: active, step: 0)

3. EXECUTION
   Cycle step is agent-owned:
     -> System creates WorkItem with agent_prompt
     -> Agent claims WorkItem via `tillr next`
     -> Agent receives WorkContext (feature + cycle + prior results + guidance)
     -> Agent completes work, submits result
     -> System scores the step (via judge agent or automated checks)
     -> If score meets threshold: advance to next step
     -> If score below threshold: loop (increment iteration, stay on step)

   Cycle step is human-owned:
     -> System notifies human (web dashboard, CLI, webhook)
     -> Human reviews, approves/rejects
     -> Approval: advance to next step
     -> Rejection: loop back to previous agent step with feedback

4. COMPLETION
   Final cycle step completed
   -> Feature status updated (e.g., "done")
   -> Milestone progress recalculated
   -> Roadmap item status may auto-update
   -> Event logged, audit trail updated

5. CONTINUOUS
   Agents report heartbeats, status updates
   Human monitors via dashboard
   Human can pause/redirect at any time by pausing the cycle
```

### What Traditional Concepts Map To

| Traditional Concept | Tillr Equivalent | Notes |
|-------------------|-----------------|-------|
| Sprint/Iteration | *Not needed* | Agents work continuously, not in time-boxes |
| Kanban Board | Dashboard status view | Read-only visualization of cycle states |
| Story Points | Estimate size (t-shirt) | Relative sizing, not velocity-based planning |
| Velocity | Agent throughput metrics | Items/hour, not points/sprint |
| Sprint Planning | Roadmap prioritization | Human sets priorities, agents pull work |
| Standup | Agent heartbeat dashboard | Real-time, not ceremonial |
| Retrospective | Cycle score history | Quantitative, not ceremonial |
| Epic | Milestone or Workstream | Grouping mechanism, not a work item type |
| User Story | Feature | The deliverable unit |
| Task/Sub-task | Work Item | Atomic agent task within a cycle step |
| Workflow | Cycle Template | Configurable state machine |
| Board Column | Cycle Step | But with entry/exit criteria, not just labels |
| Assignee | Agent Session | Agents claim work, not assigned by humans |
| Sprint Review | Human QA step in cycle | Built into the workflow, not a ceremony |

### What's New (No Traditional Equivalent)

| Tillr Concept | Purpose |
|--------------|---------|
| **Cycle scoring** | Quantitative quality measurement at each step, enabling convergence |
| **Automatic looping** | Cycles can iterate (re-do steps) based on scores, not just progress linearly |
| **Context packaging** | `WorkContext` bundles everything an agent needs in one payload |
| **Human step blocking** | Workflow pauses at human steps until human acts -- not a notification, a gate |
| **Agent heartbeats** | Real-time progress visibility without interrupting the agent |
| **Idea intake queue** | Structured path from vague human input to actionable feature spec |
| **Decision records** | ADRs as first-class entities with their own tillr |
| **Multi-cycle entities** | A feature can go through feature-implementation, then ui-refinement, then bug-triage -- sequential cycles |

### Recommended Schema Change: Polymorphic Cycle Attachment

The key architectural change is making `CycleInstance` polymorphic on entity type. Two implementation options:

**Option A: Two-column polymorphic (recommended)**
```sql
ALTER TABLE cycle_instances
  ADD COLUMN entity_type TEXT NOT NULL DEFAULT 'feature';
ALTER TABLE cycle_instances
  RENAME COLUMN feature_id TO entity_id;
```

This is simple, queryable, and matches the existing pattern. Queries become:
```sql
SELECT * FROM cycle_instances
WHERE entity_type = 'roadmap_item' AND entity_id = 'ri-123';
```

**Option B: Separate junction tables per entity type**
```sql
CREATE TABLE cycle_feature (cycle_id INT, feature_id TEXT);
CREATE TABLE cycle_roadmap_item (cycle_id INT, roadmap_item_id TEXT);
```

This is more normalized but creates table sprawl and complicates queries. Not recommended for SQLite.

**Recommendation**: Option A. It's the pattern used by `events` (which already has `feature_id` but could generalize), `notifications` (which has `entity_type` + `entity_id`), and `context_entries`. The codebase already uses this pattern.

### Recommended Cycle Templates for New Entity Types

```go
// Idea triage: quick assessment of incoming ideas
{Name: "idea-triage", Description: "Idea Triage", Steps: []CycleStep{
    step("classify"),           // agent categorizes and enriches
    step("feasibility"),        // agent assesses feasibility
    humanStep("human-decide"),  // human approves, rejects, or defers
}}

// Milestone tracking: lightweight progress check
{Name: "milestone-review", Description: "Milestone Review", Steps: []CycleStep{
    step("gather-status"),      // agent aggregates feature statuses
    step("identify-risks"),     // agent flags blockers and risks
    humanStep("human-review"),  // human reviews and adjusts priorities
}}

// Decision tillr: from proposal to acceptance
{Name: "decision-review", Description: "Decision Review", Steps: []CycleStep{
    step("research"),           // agent researches context and alternatives
    step("draft-proposal"),     // agent writes the ADR
    humanStep("human-review"),  // human reviews proposal
    step("revise"),             // agent incorporates feedback
    humanStep("human-approve"), // human accepts or rejects
}}

// Roadmap refinement: turning a roadmap item into actionable features
{Name: "roadmap-refinement", Description: "Roadmap Refinement", Steps: []CycleStep{
    step("research"),            // agent researches the domain
    step("decompose"),           // agent breaks into features
    humanStep("human-review"),   // human reviews decomposition
    step("create-features"),     // agent creates feature records
    humanStep("human-approve"),  // human approves the plan
}}
```

### The Vantage Boundary

Tillr is the action side. Vantage is the thinking side. The boundary:

| Tillr (Action) | Vantage (Thinking) |
|----------------|-------------------|
| Feature status, cycle state | Agent reasoning logs, design docs |
| Work item prompts and results | Freeform research notes, brainstorming |
| QA scores, pass/fail | Detailed QA analysis prose |
| Decision records (structured) | Decision exploration (unstructured) |
| Event audit trail | Agent thought process trace |
| Dashboard metrics | Narrative project understanding |

Tillr answers: "What is the state of work?" Vantage answers: "What is the agent thinking?"

---

## Summary of Recommendations

1. **Generalize CycleInstance** from `feature_id` to `entity_type` + `entity_id`. This is the single most important change.

2. **Add `applicable_to` to CycleTemplate** so the UI can show only relevant cycle types when attaching a cycle to an entity.

3. **Add new cycle templates** for ideas, milestones, decisions, and roadmap items.

4. **Keep work items feature-scoped.** Work items are the atomic unit of agent work and they produce code artifacts tied to features. Don't generalize them -- features are the deliverable boundary.

5. **Don't add sprints or time-boxing.** Agents work continuously. The human steers via priorities and roadmap ordering, not calendar-based planning.

6. **Don't add Kanban boards.** The dashboard already shows status. Agents don't need to see a board. Humans need a status overview, which the existing dashboard provides.

7. **Add cycle-aware status rollup.** A milestone's effective status should be computed from its features' cycle states. A roadmap item's status should reflect its linked features. This is the "status flows up" principle.

8. **Consider adding transition guards to CycleStep** (entry/exit criteria) as a future enhancement. For now, the human/agent step distinction and scoring thresholds provide sufficient control. Don't over-engineer the state machine before the simpler model proves insufficient.

---

## Sources

- [Linear Conceptual Model](https://linear.app/docs/conceptual-model)
- [Linear Workflow Configuration](https://linear.app/docs/configuring-workflows)
- [Linear Projects](https://linear.app/docs/projects)
- [Jira Issue Hierarchy](https://www.atlassian.com/agile/project-management/epics-stories-themes)
- [Jira Workflow Schemes](https://community.atlassian.com/forums/Jira-questions/Can-different-workflows-be-defined-for-different-Epics-of-a-same/qaq-p/1964826)
- [Shortcut Hierarchy Best Practices](https://www.shortcut.com/blog/ultimate-setup-series-best-practices-for-the-shortcut-hierarchy)
- [Shortcut Milestones and Epics](https://www.shortcut.com/blog/how-we-use-milestones-epics-product-management-clubhouse)
- [Pivotal Tracker Terminology](https://www.pivotaltracker.com/help/articles/terminology/)
- [Pivotal Tracker Velocity](https://www.pivotaltracker.com/help/articles/analytics_velocity_chart/)
- [Plane Open Source](https://plane.so/open-source)
- [Plane vs Linear 2026](https://plane.so/blog/plane-versus-linear-which-should-you-choose-in-2026)
- [Huly Platform](https://github.com/hcengineering/platform)
- [GitHub Projects Automation](https://docs.github.com/en/issues/planning-and-tracking-with-projects/automating-your-project/using-the-built-in-automations)
- [Martin Fowler: Humans and Agents in Software Engineering Loops](https://martinfowler.com/articles/exploring-gen-ai/humans-and-agents.html)
- [AWS AI-DLC Open Source](https://aws.amazon.com/blogs/devops/open-sourcing-adaptive-workflows-for-ai-driven-development-life-cycle-ai-dlc/)
- [AWS AI-DLC Methodology](https://aws.amazon.com/blogs/devops/ai-driven-development-life-cycle/)
- [APM Framework](https://github.com/sdi2200262/agentic-project-management)
- [Agentic AI Orchestration 2026](https://onereach.ai/blog/agentic-ai-orchestration-enterprise-workflow-automation/)
- [Agentic Workflows Guide (Vellum)](https://vellum.ai/blog/agentic-workflows-emerging-architectures-and-design-patterns)
- [Human-in-the-Loop Patterns 2026](https://myengineeringpath.dev/genai-engineer/human-in-the-loop/)
- [Human-in-the-Loop Best Practices (Permit.io)](https://www.permit.io/blog/human-in-the-loop-for-ai-agents-best-practices-frameworks-use-cases-and-demo)
- [Configuration-Driven State Machines](https://medium.com/just-tech/configuration-driven-state-machines-db26b85d1a67)
- [Audit Trails for Agents](https://www.adopt.ai/glossary/audit-trails-for-agents)
- [AI Agent Audit Trail (Sweep)](https://www.sweep.io/blog/the-audit-trail-of-an-ai-agent)
- [Devin AI](https://devin.ai/)
- [OpenHands Platform](https://github.com/OpenHands/OpenHands)
