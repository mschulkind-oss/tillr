# Architecture Decision Records (ADRs)

## `tillr decision add <title>`

Record an architecture decision.

```bash
tillr decision add "Use PostgreSQL for primary storage" \
  --context "Need a reliable RDBMS for transactional data" \
  --decision "PostgreSQL 16 with connection pooling" \
  --consequences "Team needs PostgreSQL expertise; adds ops complexity" \
  --feature feat-1
# Created decision: "Use PostgreSQL for primary storage" (proposed)
```

| Flag | Description |
|------|-------------|
| `--context C` | Why is this decision needed? |
| `--decision D` | What was decided? |
| `--consequences C` | What are the consequences? |
| `--feature F` | Link to a feature ID |
| `--status S` | Status: `proposed`, `accepted`, `rejected`, `superseded`, `deprecated` (default: `proposed`) |

## `tillr decision list`

List all architecture decisions.

```bash
tillr decision list
# ID  Status    Title
# 1   accepted  Use PostgreSQL for primary storage
# 2   proposed  JWT vs session-based auth
```

## `tillr decision show <id>`

Show full decision details, including context, decision text, consequences, and linked feature.

## `tillr decision edit <id>`

Edit a decision's properties (status, context, decision text, consequences).

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
