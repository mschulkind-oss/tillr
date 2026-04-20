# Roadmap

## `tillr roadmap show`

Display the current roadmap, grouped by category and sorted by priority.

```bash
tillr roadmap show
# Roadmap: my-app
#
# [Core]
#   1. ★★★ User authentication          in-progress
#   2. ★★★ Payment processing            accepted
#   3. ★★  OAuth provider                proposed
#
# [Infrastructure]
#   4. ★★★ CI/CD pipeline                accepted
#   5. ★★  Monitoring & alerting         proposed
```

## `tillr roadmap add <title>`

Add an item to the roadmap.

```bash
tillr roadmap add "WebSocket notifications" --priority high --category Core
# Added roadmap item: "WebSocket notifications" (Core, high priority)
```

## `tillr roadmap prioritize`

Interactive prioritization session — presents items pairwise and asks you to choose.

## `tillr roadmap export`

Export the roadmap as Markdown or JSON.

```bash
tillr roadmap export --format md > ROADMAP.md
tillr roadmap export --format json | jq .
```

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
