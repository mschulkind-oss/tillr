# 26. Olivia — Director Hierarchy and Recursive Org Structure

**Context:** Olivia is engineering director at a 30-person company. Three
product lines, each running as a tillr project:

- **Core platform** — 8 engineers, human PM Maya, 60+ features in flight
- **Mobile app** — 6 engineers, human PM Devraj, 40+ features in flight
- **Internal tools** — 3 engineers, an *agent-PM* with Olivia as
  escalation, 20+ features in flight

Each project uses the consulting-firm flow at its level. But Olivia
needs the cross-project view AND the ability to propagate strategic
decisions across projects. The flat "PM + agents" model that works
inside a single project doesn't compose across three.

This story is the redemption of the original "model a real corp
structure" framing — the consulting firm has a Director, not just
an Engagement Manager and engineers.

**What happens today (with the flat consulting-firm model):**

Three siloed tillr projects. Olivia opens each one separately. No
cross-project view. A philosophy added in Core ("prefer Postgres for
new datastores") doesn't propagate to Mobile or Internal Tools.
Capacity allocation is manual: emails to PMs. Strategic priorities
("Q1 launch is critical for Mobile") get communicated three times,
inconsistently.

Worse: when she WANTS the cross-project view ("which projects are
behind on the security audit?"), she has to ask each PM and assemble
the answer manually. The system has the data; the system can't
present it.

**What happens with the director hierarchy:**

1. Olivia sets up an ORG (above projects). Three projects nest under it:

   ```
   tillr org init "Acme Eng" \
     --project core-platform:/repos/core \
     --project mobile-app:/repos/mobile \
     --project internal-tools:/repos/tools
   ```

2. She defines org-level **philosophies** that propagate down to all
   projects' agents:

   ```
   tillr org philosophy add "Postgres for new datastores; existing
   stores stay where they are"
   tillr org philosophy add "Quality > speed for customer-facing
   features. Internal tools may favor speed."
   ```

   Each project's context packets now include the org philosophies in
   addition to the project's own.

3. Director dashboard:

   ```
   ━━━ Acme Eng — Director View ━━━━━━━━━━━━━━━━━━━━━━━━━━

   ACROSS PROJECTS
     120 features in flight (60 core / 40 mobile / 20 tools)
     12 awaiting human decision
     3 escalations from PMs (1 from each project)
     Org rejection rate: 8% this week (down from 11%)

   ESCALATIONS NEEDING DIRECTOR INPUT
     [tools]   feature-flag system choice — agent-PM proposes
                 LaunchDarkly, asks "approve and propagate to other
                 projects?" — 4h waiting
     [core]    Maya — "API rate limit conflict between billing team
                 and search team. Need org-level call." — 1d waiting
     [mobile]  Devraj — "iOS 18 release breaks our offline mode.
                 Need to deprioritize Q1 features." — 2h waiting

   CROSS-PROJECT DECISIONS THIS WEEK
     2026-04-22  Standardized on LaunchDarkly (all 3 projects)
     2026-04-19  Postgres for new datastores (philosophy propagated)
     2026-04-17  Auth library: keep custom for now, revisit Q3

   CAPACITY
     Core:   8 agents (avg utilization 87%)
     Mobile: 6 agents (avg utilization 92%)
     Tools:  4 agents (3 agent-PMs + 1 implementer; util 41%)

   PROJECT STATUS
     Core    ████████████░░░░  72% to milestone "v3.2"
     Mobile  ██████░░░░░░░░░░  35% to milestone "iOS launch"
     Tools   ████████████████  98% to milestone "admin v2"
   ```

4. She handles escalations one by one. The internal-tools agent-PM's
   feature-flag proposal:

   ```
   Escalation: feature-flag system choice
     From: internal-tools agent-PM
     Reason: Decision affects multiple projects; agent-PM not
       authorized to make org-wide tech choices.

     Context:
       - Tools needs feature flags for the new admin UI
       - Three options evaluated: LaunchDarkly, Unleash, build-our-own
       - Agent-PM recommends LaunchDarkly (already used by Core for
         A/B tests; mobile is evaluating Unleash but not committed)
       - Cost: ~$200/mo at our scale

     Proposed action: Approve LaunchDarkly + propagate as org philosophy
       so Mobile defaults to LaunchDarkly when they pick.

     [Approve + propagate] [Approve for tools only] [Reject] [Discuss]
   ```

   Olivia reads, agrees, clicks **[Approve + propagate]**. The decision
   becomes an org philosophy. Mobile's PM gets a notification: "Org
   decision: LaunchDarkly is the standard. Your Unleash evaluation can
   continue, but the default is now LaunchDarkly."

5. She handles Maya's rate-limit conflict similarly. She decides to
   prioritize billing's stricter limits because customer-facing.
   Decision rolls into the project's decision log.

6. She decides to pull 2 agents from Internal Tools (low utilization,
   close to milestone) to Core (high utilization, behind on milestone)
   for the next sprint:

   ```
   tillr org capacity reallocate \
     --from internal-tools --to core --count 2 --duration 1w
   ```

   The agent dispatcher routes new claims accordingly. Internal Tools
   queue lengthens; Core's shrinks.

7. Reports roll up automatically. Olivia subscribes to a weekly org
   stakeholder report (extension of [story 20](./20-wei-stakeholder-report.md)
   from per-project to org-level). It synthesizes from each project's
   own report.

**Recursive structure within a project:**

The hierarchy is recursive. Inside Core, Maya (the human PM) also
delegates:

- A **feature-owner agent** for each major feature, managing the
  implementer + style-enforcer + code-reviewer dialogue (story 25).
- Maya only weighs in when a feature owner escalates. Most features
  flow without her direct attention.

So the tree is:

```
Org (Olivia)
├── Project: Core (PM: Maya)
│   ├── Workstream: API
│   │   ├── Feature: rate-limiting
│   │   │   ├── Cycle role: implementer
│   │   │   ├── Cycle role: style-enforcer
│   │   │   └── Cycle role: code-reviewer
│   │   └── Feature: pagination
│   └── Workstream: Auth
├── Project: Mobile (PM: Devraj)
└── Project: Tools (Agent-PM, escalates to Olivia)
```

Each level:
- Sets context filtered for the level below
- Receives summarized state from the level below
- Makes decisions appropriate to its altitude

**Context flow (the original ask, redeemed):**

The user's original framing: *"engineers (agents) produce
docs/PRs/tickets/discussion, PMs read them all plan strategy, the
director talks to the PMs, etc. I think we can model context management
and discovery this way and have the right decisions with the right
context implicitly."*

The hierarchy IS the context management system:

- **DOWNWARD (filtering):** Each layer passes filtered context to the
  next. Org philosophies → all project context packets. Project
  philosophies → workstream context. Feature spec → cycle-step context
  envelopes. Each level adds; lower levels see the union of everything
  above.

- **UPWARD (summarization):** Each layer summarizes for the level above.
  Cycle-step comments → feature summary → workstream summary → project
  status → org rollup. Each level aggregates; upper levels see
  digests, not raw detail.

- **LATERAL (peer comments):** Same-level peers can comment on each
  other's work. Cross-feature within a project (story 27). Cross-project
  decisions at the org level (this story).

This solves the "context overload" problem the original framing
identified: *nobody reads everything*. Each role gets the slice
appropriate to its altitude, summarized appropriately, and the
information flow is automatic.

**Agent-PM mode:**

For Internal Tools, the PM role is itself an agent. It:

- Handles routine prioritization (apply org philosophies + project
  patterns; queue features by priority)
- Handles routine reviews (delegate to code-reviewer agent; approve if
  clean, escalate if not)
- ESCALATES to Olivia for: cross-project decisions, philosophy
  amendments, capacity changes, anything outside its training set or
  marked "escalate" by cycle template

The escalation criteria are themselves a cycle template (story 13).
Olivia tunes them based on what comes back: too many escalations means
loosen; too few means the agent-PM is making bad calls (visible at the
director dashboard's rejection-rate trend).

**Gaps:**

- **Defining the org structure is itself work.** Without good defaults,
  this is too much setup. The system should detect "you have multiple
  tillr projects on disk; want to nest them under an org?" and offer a
  one-command setup.

- **Recursive escalation rules need careful design.** When does a
  feature owner escalate to PM? When does PM escalate to director?
  Default thresholds (e.g., "escalate if cycle has looped >5 times,"
  "escalate if PR touches >2 packages PM hasn't reviewed before") +
  per-org overrides.

- **Cross-project decision propagation.** Which decisions propagate,
  which don't? Needs a `scope` field on decisions. Default could be
  "ask director on first decision in a category, then auto-propagate
  thereafter unless flagged."

- **Aggregate dashboard at scale.** 5 projects × 200 features = a lot
  to summarize. Summary quality is load-bearing AGAIN. The same
  brittleness from story 8 (synthesis) compounds at the org level.

- **Agent capacity arbitration.** Allocating across projects needs
  queue priority arbitration. If two projects both demand "+2 agents
  this week," who wins? Director-set priorities resolve, but the
  defaults need design.

- **Agent-PM trust calibration.** Olivia needs to know how much to
  trust the agent-PM's decisions. Track its escalation precision and
  its "I should have escalated this" hindsight rate. Surface in the
  dashboard.

- **Org-level philosophy conflicts.** Org says "prefer Postgres,"
  project says "this workstream needs MongoDB." Conflict resolution:
  org wins by default; project can request override (becomes a
  director escalation).

**What would trip Olivia up:**

- **Drowning in the dashboard.** Too much information surfaced at the
  org level breaks the "human's 15 minutes count" principle. The
  default org view should be "what needs your attention" — not "all
  metrics from all projects."

- **Agent-PM making bad calls because escalation thresholds were too
  high.** Olivia notices a few weeks in that the agent-PM approved
  three things she would have flagged. She adjusts escalation rules
  via cycle template PR.

- **Cross-project decisions creating lock-step that doesn't fit.** Her
  "always Postgres" philosophy might be wrong for a project that needs
  MongoDB for legitimate reasons. Per-project override mechanism is
  required from day one.

- **Recursive depth confusing operators.** Org → project → workstream
  → feature → cycle-step is FIVE LEVELS. Adding sub-PMs makes it 6.
  Operators need clear mental model of where they are in the tree.

**What makes this work:**

- **The recursive structure mirrors real engineering orgs.** Director
  → PMs → leads → engineers maps cleanly to tillr org → project →
  workstream → feature.

- **Context flows naturally.** Directors see strategy; PMs see
  projects; engineers see features. No role drowns in detail it
  doesn't need.

- **Decisions are made at the right altitude.** No escalation for
  routine; no flat decision-by-everyone for strategic.

- **Foundation reuse.** Each level uses the same comments + cycles +
  context packets primitives from earlier stages. The hierarchy adds
  routing and summarization, not a new substrate.

- **Agent-PM mode scales the model.** When the human PM is the
  bottleneck, an agent-PM can absorb routine load. The escalation
  protocol keeps the human in the loop for what matters.

**Position in roadmap:**

**Stage 8 — far future.** This requires Stages 1-7 to be mature:
comments, cycles, PR pipeline, context graph, knowledge synthesis, and
metrics all need to work well at the project level before composing
across projects. Highest risk because:

- Recursive org structure is a new data model
- Aggregate summarization at scale needs summary quality earlier
  stages have proven
- Cross-project context propagation is a research problem (which
  decisions propagate?)
- Agent-PM mode requires human-PM mode to be solid first
- Escalation tuning requires data to calibrate

For most teams, the flat single-project model from Stages 1-7 is
sufficient. Stage 8 is for organizations large enough to need
cross-project coordination. Don't build this until you have at least
3 mature tillr projects feeling the pain.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
