# Milestone Management

## `tillr milestone add <name>`

Create a milestone.

```bash
tillr milestone add "v1.0" --description "Initial public release"
# Created milestone: v1.0
```

## `tillr milestone list`

List milestones with progress.

```bash
tillr milestone list
# Milestone  Status  Progress
# v1.0       active  4/7 features done (57%)
# v1.1       active  0/3 features done (0%)
```

## `tillr milestone show <id>`

Show milestone details including all assigned features.

```bash
tillr milestone show v1.0
# Milestone: v1.0
# Description: Initial public release
# Status: active
# Progress: 4/7 done
#
# Features:
#   ✓ feat-1  User authentication       done
#   ✓ feat-3  Database schema            done
#   ✓ feat-5  API endpoints              done
#   ✓ feat-6  Error handling             done
#   ◦ feat-4  Payment processing         human-qa
#   ◦ feat-7  Email notifications        implementing
#   ◦ feat-2  OAuth provider             draft
```

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
