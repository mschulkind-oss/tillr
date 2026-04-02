package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

const guideText = `# Tillr Agent Guide

You are managing a software project with tillr. This guide explains how to
think about and structure work — not how to use the CLI (run tillr --help
for that).

## Mental Model

Tillr tracks work as a hierarchy:

    Project
    └── Workstreams (parallel threads of human-driven work)
        ├── owned features (created for this workstream)
        └── dependency features (needed by this workstream, owned elsewhere)
    └── Milestones (release targets: v0.1, v1.0, etc.)
        └── Features (units of shippable work)
            └── Cycles (iteration loops: develop → qa → review)

**Workstreams** are the primary organizing unit. They represent goals like
"Add multi-tenant support" or "Harden API security." Features are the work
that gets done; workstreams explain WHY.

**Milestones** are delivery checkpoints. A feature belongs to one milestone
but may be linked to multiple workstreams.

## Entities and When to Create Them

### Features
A feature is a discrete, testable unit of work. Create one when:
- There is a clear deliverable (a new screen, API endpoint, behavior change)
- It can be verified independently
- It takes 1-5 days of agent work (split larger work)

**Required fields:**
- name: Short imperative phrase ("Add webhook retry logic")
- description: 1-2 sentences of context
- priority: 1-10 (10 = critical). Use 7+ sparingly.
- milestone_id: Which release this targets

**Optional but valuable:**
- spec: Markdown acceptance criteria. Write these if the feature is
  non-obvious. Include: what it does, edge cases, what "done" looks like.
- tags: Comma-separated labels for cross-cutting concerns

**How much detail?** Enough that an agent reading only the feature can
implement it without asking questions. If you'd need to explain something
verbally, put it in the spec.

### Workstreams
A workstream tracks a goal that spans multiple features. Create one when:
- You have 3+ features that serve a shared purpose
- You want to QA or track progress toward that goal as a unit
- Work has dependencies that need sequencing

**Required fields:**
- name: Goal-oriented ("API Security Hardening", not "Security stuff")
- description: What achieving this workstream means

**Link features to workstreams:**
- "owned" (--feature): Features created specifically for this workstream
- "dependency" (--depends): Features this workstream needs but doesn't own

A feature can be owned by one workstream and depended on by many.
Dependencies should be QA'd before owned features.

**Notes:** Use workstream notes to capture decisions, questions, and
direction changes. Tag them with types:
- decision: "We chose JWT over session tokens because..."
- question: "Do we need backwards compat for v1 tokens?"
- note: General context
- import: Brought in from external source (Slack, email, etc.)

### Milestones
A milestone is a release boundary. Create one when:
- You have a planned release (v1.0, beta, MVP)
- You need to track progress toward a deadline
- You want to scope what's "in" vs "out" for a release

Keep milestones coarse — 3-6 per project lifetime, not per sprint.

### Roadmap Items
Roadmap items are high-level strategic goals. They exist above features
and connect to them loosely. Use for:
- Quarterly planning ("Q2: Multi-region support")
- Strategic initiatives that span milestones

### Cycles
Cycles are iteration loops attached to features. The system has predefined
types (feature-implementation, bug-triage, etc.) — start the right one and
the state machine handles transitions. Human steps block until approved.

Don't create custom cycle types unless the predefined ones genuinely don't
fit. The built-in types encode proven workflows.

## Structuring a New Project

When onboarding a project that already has work in flight:

1. **Run tillr onboard --yes** to scan and bootstrap
2. **Create milestones** for your release targets
3. **Create features** for known work, assigning to milestones
4. **Create workstreams** to group features by goal
5. **Link features** to workstreams (owned + dependencies)
6. **Start cycles** on features that are ready for work

### Naming Conventions
- Feature IDs: kebab-case, descriptive ("add-webhook-retry", not "task-47")
- Workstream IDs: kebab-case, goal-oriented ("api-security", not "sprint-3")
- Milestone IDs: version-like ("v1.0", "mvp", "beta")

### Priority Guidelines
- 9-10: Blocking other work or a hard deadline. Use rarely.
- 7-8: Important for current milestone. Most active work lives here.
- 5-6: Should happen this milestone but can slip.
- 3-4: Nice to have. Do if there's capacity.
- 1-2: Backlog. Tracked but not scheduled.

## Keeping Things Current

As you work, keep tillr updated:

- **Complete features** when work is done (status → done)
- **Add notes to workstreams** when decisions are made
- **Link new features** to workstreams as they're discovered
- **Add dependency links** when you realize a workstream needs a feature
- **Close workstreams** when the goal is achieved

The planner agent should maintain workstream-feature links as part of
planning. When breaking down work, always ask: "Which workstream does
this serve? What does it depend on?"

## QA Workflow

Features flow through: draft → implementing → agent-qa → human-qa → done

The QA page groups features by workstream. Dependencies are flagged as
"prerequisite" — QA these first so downstream workstreams aren't blocked.

When approving/rejecting, add notes explaining why. These become part of
the feature's review history.

## JSON Output

Every command supports --json. Always use it when scripting or when an
agent needs structured data. The JSON output includes fields not shown
in the human-readable output.

Key commands for agent automation:
  tillr status --json          # Full project state
  tillr feature list --json    # All features with computed fields
  tillr workstream show <id> --json  # Workstream with notes, links, children
  tillr qa pending --json      # Features awaiting review
  tillr next --json            # Next work item with full context
`

var guideCmd = &cobra.Command{
	Use:   "guide",
	Short: "Prescriptive guide for agents and humans using tillr",
	Long: `Outputs a comprehensive guide explaining how to structure and manage
work with tillr. Covers entity types, naming conventions, priority levels,
workstream organization, and workflow patterns.

This is the recommended first read for any agent or human joining a
tillr-managed project.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if jsonOutput {
			return printJSON(map[string]string{
				"guide":  guideText,
				"format": "markdown",
			})
		}
		fmt.Print(guideText)
		return nil
	},
}
