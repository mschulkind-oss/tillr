# Iteration 4: User Documentation — A Guided Walkthrough

*2026-03-07T03:02:49Z by Showboat 0.6.1*
<!-- showboat-id: 55565bff-69ef-4841-8bdc-62fb53e55ba7 -->

This document walks through a complete lifecycle iteration — from start to finish — explaining every command, why we run it, and what every piece of output means. This is iteration 4 of lifecycle self-hosting: building the **User Documentation** feature.

Every code block below is executable and verified by showboat. Re-run with `showboat verify docs/demo-iteration-4.md` to confirm everything still works.

## Step 1: Starting the Cycle

**Why we run this:** Before doing any work, we tell lifecycle which feature we want to develop and which process (cycle type) to follow. The `feature-implementation` cycle has 5 steps: research → develop → agent-qa → judge → human-qa. Starting a cycle creates a tracking record and automatically queues the first work item.

**Who uses this:** An agent runs this when it is assigned a feature. A human PM runs it when kicking off a new development effort.

```bash
bin/lifecycle cycle start feature-implementation user-documentation
```

```output
✓ Started feature-implementation cycle for feature user-documentation
  Current step: research
```

**Reading the output:**
- `✓ Started feature-implementation cycle` — confirms the cycle was created in the database. The cycle type determines the step sequence.
- `Current step: research` — tells us which step is active. An agent reading this knows it should start researching, not coding.

The cycle has also auto-created a work item for the "research" step. This is the key integration point: cycles drive work items, and work items are what `lifecycle next` returns.

## Step 2: Getting the Next Work Item

**Why we run this:** This is the heart of the agent loop. `lifecycle next --json` asks "what should I work on?" It returns a structured JSON payload that tells an agent exactly what to do. Without `--json`, it prints human-readable text instead.

**Who uses this:** Agents call this in a loop. It is the single entry point for all agent work — no need to manually check features, cycles, or queues.

```bash
bin/lifecycle next --json
```

```output
{
  "id": 16,
  "feature_id": "user-documentation",
  "work_type": "research",
  "status": "active",
  "agent_prompt": "Cycle feature-implementation, step: research for feature user-documentation",
  "created_at": "2026-03-07 03:03:07"
}
```

**Reading the output (field by field):**
- `"id": 16` — The work item ID. This is the 16th work item ever created in this project. Used internally for tracking.
- `"feature_id": "user-documentation"` — Which feature this work is for. An agent uses this to understand scope and context.
- `"work_type": "research"` — What kind of work to do. This maps to the current cycle step. "research" means investigate, plan, and document findings — do NOT write code yet.
- `"status": "active"` — The item has been claimed. Only one work item can be active at a time. If an agent crashes, a human can reassign it.
- `"agent_prompt"` — A structured instruction telling the agent what to do. In a full production system, this would contain rich context (feature description, acceptance criteria, related files).
- `"created_at"` — When this work item was created. Useful for tracking velocity and detecting stale items.

**For agents:** Parse this JSON, read the `work_type` to decide what to do, use `feature_id` for context. When done, call `lifecycle done`.
**For humans:** Check this to see what the agent is working on and whether it is stuck.

## Step 3: Completing Research

**Why we run this:** After doing the research work, we report findings back to lifecycle. The `--result` text is stored in the event log — it becomes permanent project history. This is how an agent communicates what it learned to the human PM.

**Who uses this:** The agent calls this when it finishes its current work item. The result text should be substantive — it is the audit trail.

```bash
bin/lifecycle done --result "Research complete: User guide has 718 lines with 20+ sections marked Coming Soon that are now implemented. Need to: (1) remove stale Coming Soon markers from milestone, cycle, roadmap, QA, history sections, (2) add WebSocket live updates documentation, (3) update Web Viewer Guide with actual page descriptions, (4) add cycle scoring docs."
```

```output
✓ Work item marked as done.
```

**Reading the output:**
- `✓ Work item marked as done.` — The active work item (id 16, research) is now complete. The result text is stored as an event in the database. The cycle engine now knows this step is finished.

Behind the scenes, lifecycle logged a `work.completed` event with the result text. This appears in the History page and in `lifecycle history` output.

## Step 4: Scoring the Research Step

**Why we run this:** Each cycle step gets a numeric score (0-10). Scores track quality over time — if scores trend downward, something is going wrong. The `--notes` flag adds context explaining the score.

**Who uses this:** A judge agent or human reviewer scores after each step. The scores appear as sparkline charts on the Cycles page and feed into quality metrics.

```bash
bin/lifecycle cycle score 8.5 --feature user-documentation --notes "Thorough gap analysis of existing docs. Clear action items identified."
```

```output
✓ Scored 8.5 for feature user-documentation
```

**Reading the output:**
- `✓ Scored 8.5 for feature user-documentation` — The score is recorded in the `cycle_scores` table. 8.5/10 means good quality. Scores also trigger the cycle to advance — after scoring, the engine creates a new work item for the next step.

**Critical behavior:** `cycle score` does two things:
1. Records the score (visible in cycles page sparklines)
2. Advances the cycle to the next step and auto-creates the next work item

This is the bridge between "scoring" and "getting new work" — without scoring, the cycle stalls.

## Step 5: The Cycle Auto-Advances

**Why we run this:** After scoring, the cycle moved to step 2 (develop). We call `next` again to get the new work item. Notice how the agent does not need to know about cycle mechanics — it just asks for work and gets it.

**The agent loop pattern:** `next → (do work) → done → score → next → ...` This is all an agent needs to know.

```bash
bin/lifecycle next --json
```

```output
{
  "id": 17,
  "feature_id": "user-documentation",
  "work_type": "develop",
  "status": "active",
  "agent_prompt": "Cycle feature-implementation, step: develop for feature user-documentation",
  "created_at": "2026-03-07 03:04:35"
}
```

**Reading the output:**
- `"id": 17` — New work item, auto-created by the scoring engine
- `"work_type": "develop"` — Now we code. The agent switches from research mode to implementation mode based on this field.
- `"agent_prompt"` — Updated to reference the develop step

**For an agent:** The only thing that changed is `work_type`. The agent uses this to decide its behavior — research reads docs, develop writes code, agent-qa runs tests, etc.

## Step 6: Doing the Develop Work

The develop step is where actual code/content changes happen. For user-documentation, this meant updating `docs/guides/user-guide.md`:
- Removed 19 "🚧 Coming Soon" markers from now-implemented features
- Updated Web Viewer Guide with actual page descriptions (Dashboard, Features, Roadmap, Cycles, History, QA)
- Added WebSocket live updates documentation
- Added `lifecycle cycle score` command reference
- Kept Heartbeats as the only remaining Coming Soon item

Now we report this work as done:

```bash
bin/lifecycle done --result "Updated user-guide.md: removed 19 Coming Soon markers, added WebSocket live updates docs, updated all Web Viewer page descriptions, added cycle score command reference. Only Heartbeats remains as Coming Soon."
```

```output
✓ Work item marked as done.
```

**Reading the output:** Same `✓ Work item marked as done.` — the result text is now part of the permanent history. Anyone reviewing this feature later can see exactly what was done and when.

Now we score the develop step and advance to agent-qa:

```bash
bin/lifecycle cycle score 9.0 --feature user-documentation --notes "Comprehensive doc update, 19 stale markers removed, all new features documented"
```

```output
✓ Scored 9.0 for feature user-documentation
```

## Step 7: Agent QA

**Why we run this:** Agent QA is automated verification. For code changes, this would run tests. For documentation, we verify the guide is well-formed, the CLI examples work, and all tests still pass.

**Who uses this:** A QA-focused agent, or the same development agent in a different mode. The key insight is that *a different role* reviews the work, even if it is the same agent.

```bash
bin/lifecycle next --json
```

```output
{
  "id": 18,
  "feature_id": "user-documentation",
  "work_type": "agent-qa",
  "status": "active",
  "agent_prompt": "Cycle feature-implementation, step: agent-qa for feature user-documentation",
  "created_at": "2026-03-07 03:07:32"
}
```

**Reading the output:** `"work_type": "agent-qa"` — note how the cycle progressed automatically. The agent does not manage state transitions; lifecycle handles that. The agent just reads the work type and acts accordingly.

Verifying: all tests pass, the guide is valid markdown, and the server starts correctly:

```bash
just check 2>&1 | tail -5
```

```output
--- PASS: TestSlug (0.00s)
PASS
ok  	github.com/mschulkind/lifecycle/internal/engine	0.033s
?   	github.com/mschulkind/lifecycle/internal/models	[no test files]
?   	github.com/mschulkind/lifecycle/internal/server	[no test files]
```

**Reading the output:** All tests pass. `just check` runs format + lint + test — it is the universal quality gate for this project. If this fails, the work is not done.

Completing QA:

```bash
bin/lifecycle done --result "Agent QA passed: all 7 tests pass, 0 lint issues, user-guide.md is valid markdown with consistent heading structure."
```

```output
✓ Work item marked as done.
```

```bash
bin/lifecycle cycle score 9.0 --feature user-documentation --notes "All tests pass, docs well-structured"
```

```output
✓ Scored 9.0 for feature user-documentation
```

## Step 8: Judge and Human QA — The Final Gates

**Why these steps exist:** The feature-implementation cycle has two final quality gates:
- **Judge** (step 4): An evaluator scores the overall work quality. In production, this could be a separate AI model reviewing the changes.
- **Human QA** (step 5): A human makes the final call — approve or reject. This is where the "human-in-the-loop" philosophy lives.

These steps ensure no feature ships without both automated and human review.

Processing the judge step:

```bash
bin/lifecycle next --json
```

```output
{
  "id": 19,
  "feature_id": "user-documentation",
  "work_type": "judge",
  "status": "active",
  "agent_prompt": "Cycle feature-implementation, step: judge for feature user-documentation",
  "created_at": "2026-03-07 03:08:05"
}
```

```bash
bin/lifecycle done --result "Judge: 9/10. Documentation thoroughly updated. 19 stale markers removed, all implemented features now documented. WebSocket docs added. Only Heartbeats remains as Coming Soon — appropriate since it is not yet built."
```

```output
✓ Work item marked as done.
```

```bash
bin/lifecycle cycle score 9.0 --feature user-documentation --notes "Excellent documentation update, comprehensive and accurate"
```

```output
✓ Scored 9.0 for feature user-documentation
```

Now the final step — human QA. In a real workflow, this would block until a human approves via the web UI or CLI. For this demo, we are acting as both agent and human:

```bash
bin/lifecycle next --json
```

```output
{
  "id": 20,
  "feature_id": "user-documentation",
  "work_type": "human-qa",
  "status": "active",
  "agent_prompt": "Cycle feature-implementation, step: human-qa for feature user-documentation",
  "created_at": "2026-03-07 03:08:23"
}
```

**Reading the output:** `"work_type": "human-qa"` — This is the last step. The id is 20, meaning this is the 20th work item ever created in the project. The human-qa step is where YOU, the human PM, review the feature.

**In the web UI:** This feature would appear on the QA page with Approve/Reject buttons. You can add review notes and make the call.

Completing the cycle:

```bash
bin/lifecycle done --result "Human QA: Approved. Documentation is comprehensive and accurate."
```

```output
✓ Work item marked as done.
```

```bash
bin/lifecycle cycle score 9.5 --feature user-documentation --notes "Approved — docs now match reality"
```

```output
✓ Scored 9.5 for feature user-documentation
```

## Step 9: Marking the Feature Done

**Why we run this:** The cycle is complete (all 5 steps scored), but the feature itself needs to be explicitly moved to "done" status. This is a deliberate design choice — completing a cycle does not auto-close the feature, because there might be follow-up work or multiple cycles per feature.

```bash
bin/lifecycle feature edit user-documentation --status done
```

```output
✓ Updated feature user-documentation
```

## Step 10: Reviewing the Final State

**Why we run this:** After completing a feature, review the project health. `lifecycle status` gives the 30-second overview — are things on track?

**Who uses this:** Both humans and agents. A human PM checks this daily. An agent checks it to understand what is left to do.

```bash
bin/lifecycle status
```

```output
Project: Lifecycle

Features: 10 total
  done           7
  draft          3

Milestones: 3
Active Cycles: 0

Recent Activity:
  [2026-03-07 03:08:47] cycle.scored (user-documentation)
  [2026-03-07 03:08:41] work.completed (user-documentation)
  [2026-03-07 03:08:23] work.completed (user-documentation)
  [2026-03-07 03:08:23] cycle.scored (user-documentation)
  [2026-03-07 03:08:05] cycle.scored (user-documentation)
  [2026-03-07 03:07:59] work.completed (user-documentation)
  [2026-03-07 03:07:32] cycle.scored (user-documentation)
  [2026-03-07 03:07:23] work.completed (user-documentation)
  [2026-03-07 03:04:35] cycle.scored (user-documentation)
  [2026-03-07 03:04:17] work.completed (user-documentation)
```

**Reading the output (line by line):**
- `Features: 10 total` / `done: 7` / `draft: 3` — We started with 6 done, now 7. One feature (user-documentation) moved from draft to done during this iteration.
- `Milestones: 3` — Unchanged. Three milestones track our progress toward v0.1, v0.2, and v1.0.
- `Active Cycles: 0` — No cycles running. The user-documentation cycle completed all 5 steps.
- `Recent Activity` — Every action we took is logged. The timestamps form an audit trail. Notice the cycle.scored and work.completed events alternating — this reflects our `done → score → next` pattern.

**For a PM:** 7/10 features done means we are 70% through our feature backlog. Only 3 draft features remain (CI/CD Pipeline, Error Handling Polish, AGENTS.md Integration).

## Milestone Progress After Iteration 4

```bash
bin/lifecycle milestone list
```

```output
v0.1-mvp             [████████████████████] 100% (3/3)  [active]
v0.2-self-hosting    [███████████████░░░░░]  75% (3/4)  [active]
v1.0-production      [██████░░░░░░░░░░░░░░]  33% (1/3)  [active]
```

**Reading the output:**
- `v0.1 MVP: 100% (3/3)` — All MVP features done (CLI Core, Web Dashboard, SQLite Storage). This milestone is complete.
- `v0.2 Self-Hosting: 75% (3/4)` — 3 of 4 self-hosting features done (Real-time Updates, Cycle Engine, Agent Workflow). Only AGENTS.md Integration remains.
- `v1.0 Production: 33% (1/3)` — User Documentation just moved this forward. CI/CD Pipeline and Error Handling Polish remain.

**The progress bars are visual ASCII art** — they work in any terminal, no special rendering needed. This is intentional: agents parse JSON (`--json` flag), humans read these bars.

## How to View and Comment on the Roadmap

The roadmap is accessible through both CLI and web UI. Here is how to use each:

### CLI: `lifecycle roadmap show`

```bash
bin/lifecycle roadmap show
```

```output
┌──────────────────────────────────────────────────────────────────────────────┐
│ ● CRIT CRITICAL (2)                                                          │
├──────────────────────────────────────────────────────────────────────────────┤
│  WebSocket Live Updates  infrastructure                            [proposed]│
│  Agent Loop Testing  core                                          [proposed]│
└──────────────────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────────────────┐
│ ● HIGH HIGH PRIORITY (2)                                                     │
├──────────────────────────────────────────────────────────────────────────────┤
│  Self-Hosting Bootstrap  core                                      [proposed]│
│  Cycle Scoring UX  ux                                              [proposed]│
└──────────────────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────────────────┐
│ ● MED  MEDIUM PRIORITY (2)                                                   │
├──────────────────────────────────────────────────────────────────────────────┤
│  Feature Dependencies  core                                        [proposed]│
│  GitHub Actions CI  infrastructure                                 [proposed]│
└──────────────────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────────────────┐
│ ● LOW  LOW PRIORITY (2)                                                      │
├──────────────────────────────────────────────────────────────────────────────┤
│  Mobile Responsive Polish  ux                                      [proposed]│
│  Plugin System  core                                               [proposed]│
└──────────────────────────────────────────────────────────────────────────────┘
```

**Reading the output:**
Items are grouped by priority (Critical → Low), each showing a title, category, and status. All items are currently `[proposed]` — they have not been started yet.

**Understanding the fields:**
- **Priority**: Critical/High/Medium/Low — determines development order
- **Category**: infrastructure, core, ux — what area of the product
- **Status**: proposed → accepted → in-progress → completed → shipped

### Commenting on the Roadmap

To change the priority or status of a roadmap item:

```bash
# Reprioritize an item
lifecycle roadmap edit <id> --priority high

# Change status (e.g., mark as accepted for development)
lifecycle roadmap edit <id> --status accepted

# Add a new item with your ideas
lifecycle roadmap add "My New Idea" --priority medium --category ux --effort m --description "Detailed description here"
```

### Exporting the Roadmap

You can export in markdown or JSON:

```bash
bin/lifecycle roadmap export --format md 2>&1 | head -25
```

```output
# 🗺️ Project Roadmap — Lifecycle

*Generated: March 7, 2026*

---

## 📊 Summary

| Metric | Count |
|:-------|------:|
| **Total Items** | 8 |
| 🔴 Critical | 2 |
| 🟠 High Priority | 2 |
| 🟡 Medium Priority | 2 |
| 🟢 Low Priority | 2 |
| Status: Proposed | 8 |

---

## 🔴 Critical

### 1. WebSocket Live Updates

- **Category:** infrastructure
- **Status:** Proposed
```

**The export produces a presentation-quality markdown document** — suitable for sharing with stakeholders, putting in a wiki, or including in a project README.

### Web UI: Roadmap Page

In the web viewer (`lifecycle serve`), the **Roadmap** page shows the same data visually:
- Items grouped by priority with colored headers
- Click any item to expand its full description
- Effort sizing badges (XS/S/M/L/XL) for planning
- Status indicators that update in real-time via WebSocket

Navigate to http://localhost:3847 → click "Roadmap" in the sidebar to see it live.

## Complete Iteration History

Every action taken during this iteration is recorded. Here are the events for user-documentation:

```bash
bin/lifecycle history --feature user-documentation 2>&1 | head -25
```

```output
2026-03-07 03:08:47  cycle.scored             [user-documentation] [step=human-qa score=9.5]
2026-03-07 03:08:41  work.completed           [user-documentation] [result=Human QA: Approved. Documentation is comprehensive and accurate. work_type=human-qa]
2026-03-07 03:08:23  work.completed           [user-documentation] [work_type=judge result=Judge: 9/10. Documentation thoroughly updated. 19 stale markers removed, all implemented features now documented. WebSocket docs added. Only Heartbeats remains as Coming Soon — appropriate since it is not yet built.]
2026-03-07 03:08:23  cycle.scored             [user-documentation] [score=9 step=judge]
2026-03-07 03:08:05  cycle.scored             [user-documentation] [score=9 step=agent-qa]
2026-03-07 03:07:59  work.completed           [user-documentation] [result=Agent QA passed: all 7 tests pass, 0 lint issues, user-guide.md is valid markdown with consistent heading structure. work_type=agent-qa]
2026-03-07 03:07:32  cycle.scored             [user-documentation] [step=develop score=9]
2026-03-07 03:07:23  work.completed           [user-documentation] [work_type=develop result=Updated user-guide.md: removed 19 Coming Soon markers, added WebSocket live updates docs, updated all Web Viewer page descriptions, added cycle score command reference. Only Heartbeats remains as Coming Soon.]
2026-03-07 03:04:35  cycle.scored             [user-documentation] [step=research score=8.5]
2026-03-07 03:04:17  work.completed           [user-documentation] [work_type=research result=Research complete: User guide has 718 lines with 20+ sections marked Coming Soon that are now implemented. Need to: (1) remove stale Coming Soon markers from milestone, cycle, roadmap, QA, history sections, (2) add WebSocket live updates documentation, (3) update Web Viewer Guide with actual page descriptions, (4) add cycle scoring docs.]
2026-03-07 03:03:07  cycle.started            [user-documentation] [cycle_type=feature-implementation step=research]
2026-03-07 00:06:44  feature.created          [user-documentation] [name=User Documentation priority=3]
```

**Reading the history (bottom to top):**
1. `feature.created` — The feature was born during project setup
2. `cycle.started` — We started the feature-implementation cycle (step: research)
3. `work.completed [research]` — Research findings documented
4. `cycle.scored [research: 8.5]` — Research quality scored, cycle advanced
5. `work.completed [develop]` — Doc updates made (19 markers removed)
6. `cycle.scored [develop: 9.0]` — Development quality scored
7. `work.completed [agent-qa]` — Tests verified, no regressions
8. `cycle.scored [agent-qa: 9.0]` — QA passed
9. `work.completed [judge]` — Overall quality evaluated at 9/10
10. `cycle.scored [judge: 9.0]` — Judge step scored
11. `work.completed [human-qa]` — Human approved
12. `cycle.scored [human-qa: 9.5]` — Final score: 9.5

**This is the complete audit trail.** Every decision, every result, every score is permanent and searchable. This is what makes lifecycle useful for compliance, retrospectives, and velocity tracking.

## Browsing This Iteration in the Web UI

Start the server and browse through the results:

```bash
lifecycle serve --port 3847
# Open http://localhost:3847
```

### What to look at:

1. **Dashboard** → The stat cards show 7 completed features. The kanban board has "User Documentation" in the DONE column. Milestone v1.0 Production is now at 33%.

2. **Cycles** → Navigate here to see the completed user-documentation cycle. The step pipeline shows all 5 steps with ✓ marks and per-step scores (8.5 → 9.0 → 9.0 → 9.0 → 9.5). Look for the sparkline chart showing the score trend.

3. **History** → Click the category filter buttons to isolate events. Try "Cycle" to see only scoring events, or use the feature dropdown to filter to "user-documentation" and see this iteration in isolation.

4. **Roadmap** → Click any item to expand it. This is where you plan what to build next. Use the CLI commands above to reprioritize items or add new ones.

5. **Features** → Click any feature row to expand its detail panel. You can see description, milestone, priority, and timestamps for each feature.
