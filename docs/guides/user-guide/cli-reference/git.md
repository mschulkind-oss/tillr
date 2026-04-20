# Git / VCS Integration

Tillr auto-detects whether your project uses `git` or `jj` (Jujutsu).

## `tillr git log`

Show recent commits.

```bash
tillr git log -n 10
```

| Flag | Description |
|------|-------------|
| `-n N` | Number of commits to show (default: 20) |

## `tillr git branches`

Show branches and their linked features.

```bash
tillr git branches
```

## `tillr git link <feature-id> <commit-hash>`

Link a commit to a feature for traceability.

```bash
tillr git link feat-1 abc123f
# Linked commit abc123f to feat-1.
```

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
