# Project Management

## `tillr init <name>`

Initialize a new tillr project in the current directory.

```bash
tillr init my-app
# Initializing project: my-app
# Created .tillr.json
# Created tillr.db
# Project "my-app" is ready.
```

This creates `.tillr.json` with default settings and initializes the SQLite database with the full schema.

## `tillr status`

Show project status overview: features by state, milestone progress, and active agents.

```bash
tillr status
# Project: my-app
#
# Features:  2 draft · 1 implementing · 1 human-qa · 3 done
# Milestones: v1.0 (4/7 done) · v1.1 (0/3 done)
# Active:    1 agent working on feat-4 (implement cycle, round 2)
```

## `tillr doctor`

Validate your environment and project setup. Checks for a valid config, database integrity, Go version, and common misconfigurations.

```bash
tillr doctor
# ✓ .tillr.json found
# ✓ tillr.db schema is current (v1)
# ✓ Go 1.24.2 detected
# ✓ No orphaned work items
# All checks passed.
```

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
