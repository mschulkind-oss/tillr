# Agent Work Items

## `tillr next [--cycle C]`

Get the next work item for an agent. Returns JSON to stdout for easy consumption by agent tooling.

```bash
tillr next
```

```json
{
  "workItemId": 42,
  "featureId": "feat-1",
  "featureName": "User authentication",
  "workType": "implement",
  "agentPrompt": "Implement JWT-based authentication with login, logout, and token refresh endpoints. Use bcrypt for password hashing. Write tests for all endpoints.",
  "context": {
    "milestone": "v1.0",
    "priority": "high",
    "round": 2,
    "previousResult": "Round 1 implemented login only. Need logout and refresh."
  }
}
```

If no work is available, exits with code 0 and an empty JSON object.

| Flag | Description |
|------|-------------|
| `--cycle C` | Only return items from a specific cycle type |

## `tillr done [--result R]`

Mark the current work item as complete.

```bash
tillr done --result "Implemented all three endpoints with full test coverage"
# Marked work item 42 as done.
# Feature feat-1: cycle round 2 complete.
```

## `tillr fail [--reason R]`

Mark the current work item as failed. The cycle will decide whether to retry or escalate.

```bash
tillr fail --reason "Cannot connect to external API for OAuth verification"
# Marked work item 42 as failed.
# Feature feat-1: work item failed, cycle will retry.
```

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
