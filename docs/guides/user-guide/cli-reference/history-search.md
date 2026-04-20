# History & Search

## `tillr history`

Browse the event history log. Every state change, QA decision, and cycle event is recorded.

```bash
tillr history --feature feat-1 --since 2025-01-15
# 2025-01-15 09:00  feat-1  created
# 2025-01-15 09:05  feat-1  status_change  draft → planning
# 2025-01-15 09:10  feat-1  status_change  planning → implementing
# 2025-01-15 10:30  feat-1  cycle_round    implement round 1 (score: 6/10)
# 2025-01-15 11:45  feat-1  cycle_round    implement round 2 (score: 8/10)
```

| Flag | Description |
|------|-------------|
| `--feature F` | Filter by feature ID |
| `--since S` | Show events after this date/time |
| `--type T` | Filter by event type (`status_change`, `cycle_round`, `qa_decision`, …) |

## `tillr search <query>`

Full-text search across all project data — feature names, descriptions, QA notes, agent results, and event data.

```bash
tillr search "JWT"
# feat-1   "User authentication"    agent_prompt: "…JWT-based authentication…"
# feat-1   cycle result (round 2):  "…added JWT refresh token rotation…"
```

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
