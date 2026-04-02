# GitHub Copilot CLI Integration Guide

This guide documents how to integrate **tillr** with GitHub Copilot CLI
(and similar AI-powered CLI assistants) so that agents can drive the full
project management workflow.

## Overview

Tillr exposes a CLI-first interface designed for agent consumption.
Every command supports `--json` output, making it trivially parseable by
Copilot CLI, Claude Code, Cursor, or any agent that can invoke shell commands.

## Quick Start

```bash
# 1. Agent picks up work
WORK=$(tillr next --json)

# 2. Agent reads the agent_guidance field and executes
# ... do the work ...

# 3. Agent reports completion
tillr done --result "Implemented feature X with tests"

# 4. Agent sends heartbeat during long tasks
tillr heartbeat --message "Running test suite..."
```

## Hook Points

Tillr provides several hook points where Copilot CLI can be integrated:

### 1. Work Intake Hook (`tillr next`)

The primary agent entry point. Returns the next work item with full context:

```bash
tillr next --json
```

Response includes:
- `work_item`: The task to perform
- `feature`: Full feature details with spec
- `cycle`: Current iteration cycle state
- `agent_guidance`: Human-readable instructions
- `notifications`: Any pending notifications

### 2. Completion Hook (`tillr done`)

Signal task completion with results:

```bash
tillr done --result "Description of what was accomplished"
```

### 3. Failure Hook (`tillr fail`)

Report task failure with reason:

```bash
tillr fail --reason "Build failed: missing dependency X"
```

### 4. Heartbeat Hook (`tillr heartbeat`)

Keep-alive signal during long-running tasks:

```bash
tillr heartbeat --message "Currently running integration tests (45% done)"
```

### 5. Idea Submission Hook (`tillr idea submit`)

Submit new ideas from any context:

```bash
tillr idea submit "Add dark mode" \
  --description "Users want dark mode support" \
  --type feature \
  --auto-implement
```

### 6. Auto-Intake Hook (`tillr idea process`)

Automatically process pending ideas without human approval:

```bash
tillr idea process          # Process all pending
tillr idea process 42       # Process specific idea
```

### 7. Status Hook (`tillr status`)

Get project overview:

```bash
tillr status --json
```

## Copilot CLI Configuration

### Setting Up Copilot Instructions

Create `.github/copilot-instructions.md` in your project:

```markdown
## Project Management

This project uses `tillr` for project management. Before starting work:

1. Run `tillr next --json` to get your assigned task
2. Read the `agent_guidance` field for instructions
3. When done, run `tillr done --result "..."` to report completion
4. If stuck, run `tillr fail --reason "..."` to report failure

Always send heartbeats during long tasks:
  `tillr heartbeat --message "status..."`

## Available Commands

- `tillr feature list --json` - List all features
- `tillr feature show <id> --json` - Feature details
- `tillr qa pending --json` - Features awaiting QA
- `tillr search <query> --json` - Full-text search
- `tillr discuss list --json` - Active discussions
```

### Environment Variables

Set these for consistent agent identification:

```bash
export TILLR_AGENT_ID="copilot-$(hostname)"
```

## Workflow Patterns

### Pattern 1: Continuous Work Loop

```bash
while true; do
  WORK=$(tillr next --json)
  STATUS=$(echo "$WORK" | jq -r '.status // empty')
  if [ "$STATUS" = "no_work" ]; then
    sleep 60
    continue
  fi

  # Process work item...
  FEATURE=$(echo "$WORK" | jq -r '.feature.id')
  GUIDANCE=$(echo "$WORK" | jq -r '.agent_guidance')

  # Do the work based on guidance...

  tillr done --result "Completed task for $FEATURE"
done
```

### Pattern 2: Feature-Focused Work

```bash
# Start a cycle for a specific feature
tillr cycle start feature-implementation my-feature

# Get the first work item
tillr next --json

# Complete each step, auto-advancing the cycle
tillr done --result "Research complete: found X approach"
tillr next --json
tillr done --result "Implementation complete with tests"
```

### Pattern 3: Bug Triage

```bash
# Report a bug
tillr bug report "Login page 500 error" \
  --description "Users see 500 error when clicking login"

# Process it automatically
tillr idea process

# Start bug triage cycle
tillr cycle start bug-triage login-page-500-error
```

### Pattern 4: Discussion-Driven Development

```bash
# Start an RFC discussion
tillr discuss new "RFC: New authentication system" \
  --body "Proposal to replace current auth..."

# Other agents can comment
tillr discuss comment 1 --content "I agree, but we should consider..."

# Resolve when consensus is reached
tillr discuss resolve 1
```

## Hook Registration

To see all available hook points:

```bash
tillr hooks
```

## Troubleshooting

### Agent not showing in dashboard

Ensure `TILLR_AGENT_ID` is set, or the agent will use a PID-based ID
that changes each session.

### Work items not appearing

Check the queue: `tillr queue list`
Check for stale items: `tillr queue reclaim`

### Feature stuck in wrong status

Use `tillr feature edit <id> --status <new-status>` to manually
transition (must follow valid state machine transitions).
