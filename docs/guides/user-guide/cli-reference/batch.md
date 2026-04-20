# Batch Operations

## `tillr feature batch`

Update multiple features at once.

```bash
# Set status for multiple features
tillr feature batch --ids feat-1,feat-2,feat-3 --status implementing

# Set milestone for multiple features
tillr feature batch --ids feat-1,feat-2 --milestone v1.0

# Set priority for multiple features
tillr feature batch --ids feat-1,feat-2,feat-3 --priority 8
```

| Flag | Description |
|------|-------------|
| `--ids IDs` | Comma-separated feature IDs |
| `--status S` | Set status for all listed features |
| `--milestone M` | Set milestone for all listed features |
| `--priority P` | Set priority for all listed features |

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
