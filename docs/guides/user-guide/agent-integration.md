# Agent Integration Guide

Tillr is designed so that AI agents interact with your project through the CLI. The typical agent loop:

```
┌─────────────────────────────────┐
│  tillr next                 │  ← Agent asks for work
│  → receives JSON agentPrompt   │
├─────────────────────────────────┤
│  Agent performs the work        │  ← Code, test, design, review
├─────────────────────────────────┤
│  tillr done --result "…"    │  ← Agent reports success
│  tillr fail --reason "…"    │  ← …or failure
└─────────────────────────────────┘
        ↓ (cycle continues or ends)
```

## Setting Up an Agent

1. **Point the agent at your project directory** — the agent must run tillr commands from within the project tree (any subdirectory works).

2. **Teach the agent the protocol:**
   - Call `tillr next` to get a work item. Parse the JSON response.
   - Read the `agentPrompt` field for instructions.
   - Do the work (write code, run tests, etc.).
   - Call `tillr done --result "description of what was done"` on success.
   - Call `tillr fail --reason "what went wrong"` on failure.

3. **The cycle handles the rest.** The iteration cycle manages rounds, scoring, and state transitions. The agent doesn't need to know about tillr states — it just picks up work and reports results.

## Example: Agent Script

```bash
#!/usr/bin/env bash
# Simple agent loop
while true; do
  WORK=$(tillr next)
  if [ "$WORK" = "{}" ]; then
    echo "No work available. Sleeping…"
    sleep 30
    continue
  fi

  PROMPT=$(echo "$WORK" | jq -r '.agentPrompt')
  # Send prompt to your AI agent, get result…

  tillr done --result "$AGENT_RESULT"
done
```

## Heartbeats

Long-running agents should send periodic heartbeats to signal they're still alive:

```bash
tillr heartbeat --message "Running integration tests"
# Heartbeat recorded.
```

The web viewer shows agent activity and heartbeat status in real time. Work items with no heartbeat for 30+ minutes are considered stale and can be reclaimed:

```bash
tillr queue reclaim
# Reclaimed 1 stale work item(s).
```

---

« [User Guide](./README.md)
