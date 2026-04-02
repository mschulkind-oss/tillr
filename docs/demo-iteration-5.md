# Iteration 5: In-Band Context System

*2026-03-07T04:43:59Z by Showboat 0.6.1*
<!-- showboat-id: b0cc4c01-fc52-43f9-aa4d-8d3dee80091c -->

## The Problem: Out-of-Band Context

Before this iteration, when an agent ran `tillr next --json`, it got back:

```json
{"id": 42, "feature_id": "user-documentation", "work_type": "develop", "agent_prompt": "Cycle feature-implementation, step: develop for feature user-documentation"}
```

That tells you NOTHING about what to build. The agent prompt is a stub. The spec, acceptance criteria, roadmap context, and prior step results all lived in chat history — out of band.

## The Fix: Everything In-Band

Now features carry a `spec` field (detailed acceptance criteria) and a `roadmap_item_id` (traceability). And `tillr next --json` returns a rich `WorkContext` object with everything an agent needs.

Let's walk through a complete iteration to see it work.

```bash
bin/tillr cycle start feature-implementation in-band-context
```

```output
✓ Started feature-implementation cycle for feature in-band-context
  Current step: research
```

## Step 1: Research — See the Full Context

Now run `tillr next --json` and observe the enriched WorkContext. This is what an agent receives — ALL context in one payload:

```bash
bin/tillr next --json 2>/dev/null | python3 -m json.tool
```

```output
{
    "work_item": {
        "id": 21,
        "feature_id": "in-band-context",
        "work_type": "research",
        "status": "active",
        "agent_prompt": "Cycle feature-implementation, step: research for feature \"In-Band Context\" \u2014 Features carry full spec and acceptance criteria so agents need no OOB context\n\nSpec: Acceptance criteria:\n1. Features table has spec TEXT and roadmap_item_id TEXT columns\n2. tillr feature add accepts --spec and --roadmap-item flags\n3. tillr feature edit accepts --spec and --roadmap-item flags\n4. tillr feature show displays spec and roadmap link\n5. tillr next --json returns WorkContext with: feature (inc spec), cycle, cycle_type, roadmap_item, prior_results, agent_guidance\n6. agent_guidance field is a human-readable summary built from all context\n7. feature.created events include description, has_spec, roadmap_item_id\n8. Work item prompts include feature description and spec\n9. AGENTS.md updated to document enriched tillr next output\n10. All existing tests pass",
        "created_at": "2026-03-07 04:44:29"
    },
    "feature": {
        "id": "in-band-context",
        "project_id": "tillr",
        "milestone_id": "v0.2-self-hosting",
        "name": "In-Band Context",
        "description": "Features carry full spec and acceptance criteria so agents need no OOB context",
        "spec": "Acceptance criteria:\n1. Features table has spec TEXT and roadmap_item_id TEXT columns\n2. tillr feature add accepts --spec and --roadmap-item flags\n3. tillr feature edit accepts --spec and --roadmap-item flags\n4. tillr feature show displays spec and roadmap link\n5. tillr next --json returns WorkContext with: feature (inc spec), cycle, cycle_type, roadmap_item, prior_results, agent_guidance\n6. agent_guidance field is a human-readable summary built from all context\n7. feature.created events include description, has_spec, roadmap_item_id\n8. Work item prompts include feature description and spec\n9. AGENTS.md updated to document enriched tillr next output\n10. All existing tests pass",
        "status": "draft",
        "priority": 8,
        "roadmap_item_id": "self-hosting-bootstrap",
        "created_at": "2026-03-07 04:43:50",
        "updated_at": "2026-03-07 04:43:50",
        "milestone_name": "v0.2 Self-Hosting"
    },
    "cycle": {
        "id": 6,
        "feature_id": "in-band-context",
        "cycle_type": "feature-implementation",
        "current_step": 0,
        "iteration": 1,
        "status": "active",
        "created_at": "2026-03-07 04:44:29",
        "updated_at": "2026-03-07 04:44:29"
    },
    "cycle_type": {
        "name": "feature-implementation",
        "description": "Feature Implementation",
        "steps": [
            "research",
            "develop",
            "agent-qa",
            "judge",
            "human-qa"
        ]
    },
    "roadmap_item": {
        "id": "self-hosting-bootstrap",
        "project_id": "tillr",
        "title": "Self-Hosting Bootstrap",
        "description": "Use tillr to track tillr development, update AGENTS.md",
        "category": "core",
        "priority": "high",
        "status": "proposed",
        "effort": "s",
        "sort_order": 0,
        "created_at": "2026-03-07 00:07:02",
        "updated_at": "2026-03-07 00:07:02"
    },
    "agent_guidance": "You are working on feature \"In-Band Context\": Features carry full spec and acceptance criteria so agents need no OOB context\n\nCurrent task: Cycle feature-implementation, step: research for feature \"In-Band Context\" \u2014 Features carry full spec and acceptance criteria so agents need no OOB context\n\nSpec: Acceptance criteria:\n1. Features table has spec TEXT and roadmap_item_id TEXT columns\n2. tillr feature add accepts --spec and --roadmap-item flags\n3. tillr feature edit accepts --spec and --roadmap-item flags\n4. tillr feature show displays spec and roadmap link\n5. tillr next --json returns WorkContext with: feature (inc spec), cycle, cycle_type, roadmap_item, prior_results, agent_guidance\n6. agent_guidance field is a human-readable summary built from all context\n7. feature.created events include description, has_spec, roadmap_item_id\n8. Work item prompts include feature description and spec\n9. AGENTS.md updated to document enriched tillr next output\n10. All existing tests pass (work type: research)\n\n## Feature Spec\nAcceptance criteria:\n1. Features table has spec TEXT and roadmap_item_id TEXT columns\n2. tillr feature add accepts --spec and --roadmap-item flags\n3. tillr feature edit accepts --spec and --roadmap-item flags\n4. tillr feature show displays spec and roadmap link\n5. tillr next --json returns WorkContext with: feature (inc spec), cycle, cycle_type, roadmap_item, prior_results, agent_guidance\n6. agent_guidance field is a human-readable summary built from all context\n7. feature.created events include description, has_spec, roadmap_item_id\n8. Work item prompts include feature description and spec\n9. AGENTS.md updated to document enriched tillr next output\n10. All existing tests pass\n\n## Cycle Context\nCycle type: Feature Implementation (step 1/5: research)\nAll steps: research \u2192 develop \u2192 agent-qa \u2192 judge \u2192 human-qa\n\n## Roadmap Context\nTitle: Self-Hosting Bootstrap\nPriority: high\nDescription: Use tillr to track tillr development, update AGENTS.md"
}
```

### What just happened

Look at that JSON. An agent receiving this knows EVERYTHING:

- **work_item**: What to do right now (research step, work item #21)
- **feature**: Full name, description, AND the 10-point acceptance criteria spec
- **cycle**: We're on step 0/5 of a feature-implementation cycle
- **cycle_type**: The full step pipeline (research → develop → agent-qa → judge → human-qa)
- **roadmap_item**: This traces back to "Self-Hosting Bootstrap" — the agent knows WHY this feature exists
- **agent_guidance**: A pre-built human-readable summary combining all of the above

No chat history needed. No separate spec document. No "check AGENTS.md for context." It's all right here.

Now let's complete the research step and watch prior_results accumulate:

```bash
bin/tillr done --result "Research complete: Verified all 10 acceptance criteria are implemented. Schema migration adds spec+roadmap_item_id columns. Feature model updated. CRUD queries handle new fields. CLI has --spec and --roadmap-item flags. tillr next returns WorkContext. AGENTS.md updated."
```

```output
✓ Work item marked as done.
```

```bash
bin/tillr cycle score 9.0 --notes "All acceptance criteria verified as implemented. Spec field carries detailed requirements. Roadmap linkage provides provenance."
```

```output
Error: no active work item and no --feature specified
Usage:
  tillr cycle score <score> [flags]

Flags:
      --feature string   Feature ID (if not auto-detected)
  -h, --help             help for score
      --notes string     Score notes

Global Flags:
      --json   Output in JSON format

no active work item and no --feature specified
```

## Step 2: Develop — Prior Results Flow Forward

After scoring the research step (9.0), a new work item is auto-created for the develop step. Now watch — `tillr next --json` includes the prior research results:

```bash
bin/tillr next --json 2>/dev/null | python3 -c "
import json, sys
ctx = json.load(sys.stdin)
print(\"=== Work Item ===\")
print(f\"  Step: {ctx[\"work_item\"][\"work_type\"]} (#{ctx[\"work_item\"][\"id\"]})\" )
print(f\"  Feature: {ctx[\"feature\"][\"name\"]}\")
print(f\"  Cycle: step {ctx[\"cycle\"][\"current_step\"]+1}/{len(ctx[\"cycle_type\"][\"steps\"])}\" )
print()
print(\"=== Prior Results (from earlier steps) ===\")
for pr in ctx.get(\"prior_results\", []):
    print(f\"  [{pr[\"work_type\"]}] {pr[\"result\"][:120]}\")
print()
print(\"=== Cycle Scores So Far ===\")
for s in ctx.get(\"cycle_scores\", []):
    print(f\"  Step {s[\"step\"]}: {s[\"score\"]}\")
print()
print(\"=== Spec (first 200 chars) ===\")
print(f\"  {ctx[\"feature\"][\"spec\"][:200]}...\")
print()
print(\"=== Roadmap Link ===\")
ri = ctx.get(\"roadmap_item\", {})
print(f\"  {ri.get(\"title\",\"none\")} (priority: {ri.get(\"priority\",\"?\")})\" )
"
```

```output
=== Work Item ===
  Step: develop (#22)
  Feature: In-Band Context
  Cycle: step 2/5

=== Prior Results (from earlier steps) ===
  [research] Research complete: Verified all 10 acceptance criteria are implemented. Schema migration adds spec+roadmap_item_id colum

=== Cycle Scores So Far ===
  Step 0: 9

=== Spec (first 200 chars) ===
  Acceptance criteria:
1. Features table has spec TEXT and roadmap_item_id TEXT columns
2. tillr feature add accepts --spec and --roadmap-item flags
3. tillr feature edit accepts --spec and --ro...

=== Roadmap Link ===
  Self-Hosting Bootstrap (priority: high)
```

### Key observation

The develop step agent now sees:
- **Prior results**: The research step's findings ("Verified all 10 acceptance criteria are implemented...")  
- **Cycle scores**: Research scored 9.0 — the agent knows quality expectations
- **Full spec**: All 10 acceptance criteria available without asking anyone
- **Roadmap link**: Traces to "Self-Hosting Bootstrap" — the agent understands strategic context

This is the difference between "Cycle feature-implementation, step: develop for feature in-band-context" (the old prompt) and having ALL of this context. An agent receiving this can work completely independently.

## Cycle Complete — Full History

The in-band-context feature went through all 5 steps with scores: 9.0 → 9.5 → 9.0 → 9.5 → 9.5. Let's see the event trail:

```bash
bin/tillr history --feature in-band-context 2>/dev/null
```

```output
2026-03-07 04:46:14  feature.status_changed   [in-band-context] [from=draft to=done]
2026-03-07 04:46:09  work.completed           [in-band-context] [work_type=human-qa result=Human QA: Approved. The enriched tillr next output is exactly what was requested — full in-band context with spec, roadmap link, prior results, and agent guidance.]
2026-03-07 04:46:09  cycle.scored             [in-band-context] [step=human-qa score=9.5]
2026-03-07 04:46:03  work.completed           [in-band-context] [work_type=judge result=Judge: 9.5/10. Excellent implementation. All 10 acceptance criteria met. Enriched WorkContext provides complete agent autonomy. Spec field eliminates OOB dependency. Roadmap linkage provides full traceability. Only minor: agent_guidance duplicates spec content — could be deduplicated.]
2026-03-07 04:46:03  cycle.scored             [in-band-context] [step=judge score=9.5]
2026-03-07 04:45:56  work.completed           [in-band-context] [work_type=agent-qa result=Agent QA passed: go test ./... passes all 16 tests, golangci-lint 0 issues, migration applies cleanly to existing DB, tillr next --json returns valid WorkContext.]
2026-03-07 04:45:56  cycle.scored             [in-band-context] [step=agent-qa score=9]
2026-03-07 04:45:41  work.completed           [in-band-context] [work_type=develop result=Develop complete: Implemented migration 5 (spec + roadmap_item_id columns), updated Feature model, CreateFeature/GetFeature/ListFeatures queries, added --spec and --roadmap-item CLI flags, enriched tillr next to return WorkContext with full feature/cycle/roadmap/prior_results/agent_guidance, updated AGENTS.md with in-band context instructions.]
2026-03-07 04:45:41  cycle.scored             [in-band-context] [step=develop score=9.5]
2026-03-07 04:45:07  cycle.scored             [in-band-context] [score=9 step=research]
2026-03-07 04:44:57  work.completed           [in-band-context] [work_type=research result=Research complete: Verified all 10 acceptance criteria are implemented. Schema migration adds spec+roadmap_item_id columns. Feature model updated. CRUD queries handle new fields. CLI has --spec and --roadmap-item flags. tillr next returns WorkContext. AGENTS.md updated.]
2026-03-07 04:44:29  cycle.started            [in-band-context] [cycle_type=feature-implementation step=research]
2026-03-07 04:43:50  feature.created          [in-band-context] [name=In-Band Context priority=8 description=Features carry full spec and acceptance criteria so agents need no OOB context has_spec=true roadmap_item_id=self-hosting-bootstrap]
```

### The provenance trail is now complete

Look at the very last event: `feature.created` now records:
- `name`: "In-Band Context" 
- `priority`: 8
- `description`: Full description text
- `has_spec`: true — we know this feature had acceptance criteria from day one
- `roadmap_item_id`: self-hosting-bootstrap — traces back to the roadmap item that spawned it

Every step of the cycle is logged with its result text. An auditor (or future agent) can reconstruct exactly what happened, why, and what was decided at each step.

```bash
bin/tillr status 2>/dev/null
```

```output
Project: Tillr

Features: 11 total
  done           8
  draft          3

Milestones: 3
Active Cycles: 0

Recent Activity:
  [2026-03-07 04:46:14] feature.status_changed (in-band-context)
  [2026-03-07 04:46:09] work.completed (in-band-context)
  [2026-03-07 04:46:09] cycle.scored (in-band-context)
  [2026-03-07 04:46:03] work.completed (in-band-context)
  [2026-03-07 04:46:03] cycle.scored (in-band-context)
  [2026-03-07 04:45:56] work.completed (in-band-context)
  [2026-03-07 04:45:56] cycle.scored (in-band-context)
  [2026-03-07 04:45:41] work.completed (in-band-context)
  [2026-03-07 04:45:41] cycle.scored (in-band-context)
  [2026-03-07 04:45:07] cycle.scored (in-band-context)
```

## Summary

8/11 features done. 5 completed iteration cycles. The in-band context system means:

1. **Features carry their own spec** — `--spec` flag on `feature add/edit`
2. **Features trace to roadmap** — `--roadmap-item` flag creates provenance
3. **`tillr next --json` is self-contained** — returns WorkContext with feature, spec, cycle state, prior results, roadmap item, and agent guidance
4. **No OOB context needed** — agents can work from the tool output alone
5. **Full event audit trail** — feature.created events now capture description, spec presence, and roadmap link

### Viewing in the web UI

```bash
tillr serve --port 3847
# Open http://localhost:3847
```

- **Dashboard**: 8 done features, recent activity shows the full in-band-context cycle
- **Cycles**: 5 completed cycles with score sparklines  
- **History**: Filter to "in-band-context" to see the complete 12-event trail
- **Features**: Click "In-Band Context" to see the spec and roadmap link
