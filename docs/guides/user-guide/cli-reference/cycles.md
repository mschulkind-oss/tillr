# Iteration Cycles

## `tillr cycle list`

List available iteration cycle types.

```bash
tillr cycle list
# Cycle         Description
# implement     Full implementation cycle (plan → code → test → review)
# ui-refine     UI polish with designer and reviewer agents
# bug-triage    Bug investigation and fix cycle
# roadmap-plan  Collaborative roadmap planning cycle
```

## `tillr cycle start <cycle-name> <feature-id>`

Start an iteration cycle for a feature.

```bash
tillr cycle start implement feat-1
# Started "implement" cycle for feat-1 (User authentication)
# Round 1 of 5 · work item created · run "tillr next" to begin
```

## `tillr cycle status`

Show active cycle progress.

```bash
tillr cycle status
# Feature  Cycle      Round  Score  Agent Role
# feat-1   implement  2/5    6/10   developer
# feat-7   ui-refine  1/3    —      designer
```

## `tillr cycle history <feature-id>`

Show cycle history for a feature — every round, score, and result.

```bash
tillr cycle history feat-1
# Cycle: implement
# Round 1  score: 6/10  "Implemented login only"
# Round 2  score: 8/10  "Added logout and refresh, tests passing"
# Round 3  (active)
```

## `tillr cycle score <score>`

Submit a judge score for the current cycle step. Scores are numeric (e.g. 0–10) and recorded against the active cycle step for the feature.

```bash
tillr cycle score 8.5 --feature feat-1 --notes "Good implementation but accessibility needs work"
# Scored feat-1 cycle step: 8.5
```

| Flag | Description |
|------|-------------|
| `--feature F` | Feature ID to score (required if ambiguous) |
| `--notes N` | Freeform notes explaining the score |

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
