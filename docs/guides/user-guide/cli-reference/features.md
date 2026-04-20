# Feature Tillr

## `tillr feature add <name>`

Add a new feature. Starts in `draft` state.

```bash
tillr feature add "User authentication" --milestone v1.0 --priority high
# Created feature feat-1: "User authentication" (draft, milestone: v1.0)

tillr feature add "OAuth provider" --depends-on feat-1
# Created feature feat-2: "OAuth provider" (draft, depends on: feat-1)
```

| Flag | Description |
|------|-------------|
| `--milestone M` | Assign to a milestone |
| `--priority P` | Set priority: `low`, `medium`, `high`, `critical` |
| `--depends-on F` | Declare a dependency on another feature ID |

## `tillr feature list`

List features with optional filters.

```bash
tillr feature list
# ID      Status         Priority  Name
# feat-1  implementing   high      User authentication
# feat-2  draft          medium    OAuth provider
# feat-3  done           high      Database schema

tillr feature list --status human-qa --milestone v1.0
# ID      Status    Priority  Name
# feat-4  human-qa  high      Payment processing
```

| Flag | Description |
|------|-------------|
| `--status S` | Filter by tillr state |
| `--milestone M` | Filter by milestone |

## `tillr feature show <id>`

Show full details and history for a feature.

```bash
tillr feature show feat-1
# Feature: feat-1
# Name:    User authentication
# Status:  implementing
# Priority: high
# Milestone: v1.0
# Dependencies: (none)
# Cycle: implement (round 2 of 5)
#
# History:
#   2025-01-15 09:00  created (draft)
#   2025-01-15 09:05  moved to planning
#   2025-01-15 09:10  moved to implementing
#   2025-01-15 10:30  cycle round 1 complete (score: 6/10)
```

## `tillr feature edit <id>`

Edit a feature's metadata.

```bash
tillr feature edit feat-1 --name "JWT Authentication" --priority critical
# Updated feat-1: name → "JWT Authentication", priority → critical
```

| Flag | Description |
|------|-------------|
| `--name N` | Rename the feature |
| `--priority P` | Change priority |
| `--status S` | Manually override status (use with care) |

## `tillr feature remove <id>`

Remove a feature. Prompts for confirmation unless `--yes` is passed.

```bash
tillr feature remove feat-2
# Remove feature feat-2 "OAuth provider"? (y/N) y
# Removed feat-2.
```

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
