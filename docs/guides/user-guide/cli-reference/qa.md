# QA

## `tillr qa pending`

Show features waiting for human QA review.

```bash
tillr qa pending
# ID      Priority  Name                    Waiting Since
# feat-4  high      Payment processing      2 hours ago
# feat-8  medium    Search functionality     15 minutes ago
```

## `tillr qa approve <feature-id>`

Approve a feature — moves it from `human-qa` to `done`.

```bash
tillr qa approve feat-4 --notes "All tests pass, UI looks correct"
# Approved feat-4: "Payment processing" → done
```

## `tillr qa reject <feature-id>`

Reject a feature — sends it back to `implementing` for another cycle iteration.

```bash
tillr qa reject feat-8 --notes "Search results not sorted by relevance"
# Rejected feat-8: "Search functionality" → implementing (back to cycle)
```

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
