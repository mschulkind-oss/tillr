# Export

Export project data in multiple formats.

## `tillr export features`

```bash
tillr export features --format md > FEATURES.md
tillr export features --format csv > features.csv
tillr export features --format json | jq .
```

## `tillr export roadmap`

```bash
tillr export roadmap --format md > ROADMAP.md
```

## `tillr export decisions`

```bash
tillr export decisions --format md > DECISIONS.md
```

## `tillr export all`

Export all project data (features, roadmap, and decisions) at once.

```bash
tillr export all --format json > project-export.json
```

| Flag | Description |
|------|-------------|
| `--format F` | Output format: `json` (default), `md`, or `csv` |

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
