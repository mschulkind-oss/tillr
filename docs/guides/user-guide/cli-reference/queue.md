# Queue Management

## `tillr queue list`

List pending work items in priority order.

```bash
tillr queue list
# ID   Feature            Type        Priority  Claimed
# 12   feat-4             implement   high      agent-1
# 15   feat-7             research    medium    (unclaimed)
```

## `tillr queue stats`

Show queue statistics — pending, claimed, and completed counts.

```bash
tillr queue stats
# Pending:   3
# Claimed:   1
# Completed: 12
```

## `tillr queue reassign <work-item-id>`

Release a claimed work item back to the pending queue so another agent can pick it up.

```bash
tillr queue reassign 12
# Released work item 12 back to pending queue.
```

## `tillr queue reclaim`

Reclaim stale work items that have had no heartbeat for 30+ minutes.

```bash
tillr queue reclaim
# Reclaimed 2 stale work item(s).
```

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
