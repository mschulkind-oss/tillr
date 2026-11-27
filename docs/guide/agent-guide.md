---
title: "Agent operating guide: commands, exit codes, and rules"
status: accepted
stage: CURRENT
tags: [tillr, guide, agent, operations]
summary: "Self-contained operating reference for an AI agent running tillr in a repository: exact commands, exit codes, and hard boundaries."
---

# Agent operating guide: commands, exit codes, and rules

This page is the operating reference for an agent running `tillr` in a repository. For what `tillr` is, why to use it, and how Git landing works, see the [user guide](../guide.md). To install the `tillr` binaries and skills in a `yolo` jail from the pack, see [Setting up tillr in a yolo jail from the pack](../guide.md#5-setting-up-tillr-in-a-yolo-jail-from-the-pack). For charters (`--definition`), see [the charter guide](./charters.md).

---

## 1. Exit codes

Every `tillr` utility uses one shared exit-code contract (`tools/tillr/record/exit.go`). The distinction that matters most to an automated caller is between a **negative decision** (`1`) and **no decision** (`2`–`5`):

| Exit code | Name | Meaning |
| :--- | :--- | :--- |
| `0` | `ExitOK` | Completed; where the command makes a decision, the decision is positive. |
| `1` | `ExitNegative` | Completed with a negative decision; the JSON output record states why. |
| `2` | `ExitUsage` | Usage error, unknown flag, or malformed or unsupported input record. |
| `3` | `ExitIncompatible` | Store, schema, or record version is incompatible, or the store stayed locked past the caller's bound. |
| `4` | `ExitExternal` | An external dependency prevented a decision, distinct from a failed check. |
| `5` | `ExitInternal` | Internal bug; any open write transaction was rolled back. |

---

## 2. Commands for inspecting and operating tillr

Every `tillr <noun> ...` command forwards directly to `tillr-<noun> ...` on `PATH` and preserves argv, stdio, and exit codes. Run `tillr <noun> --help` or `tillr <noun> <face> --help` for full flag lists.

### Store (`tillr store`)

```console
$ tillr store init --store .tillr/state.db [--definition <charter.json>]
$ tillr store definition --store .tillr/state.db [--set <revised.json>]
$ tillr store check --store .tillr/state.db
$ tillr store inventory --root . [--json]
$ tillr store events --store .tillr/state.db --output <events.json> [--since <seq>]
$ tillr store checkpoint --store .tillr/state.db
```

### Items (`tillr item`)

```console
$ tillr item list --store .tillr/state.db [--state <state>]
$ tillr item show --store .tillr/state.db --item <item-id> [--root .]
$ printf '<request text>\n' | tillr item request --store .tillr/state.db --title "<title>" [--seat build|design] [--workflow <name@vN>]
$ tillr item apply --store .tillr/state.db --input <record.json> --now <rfc3339>
$ tillr item state --store .tillr/state.db --item <item-id> --state <state> --now <rfc3339> [--reason "<reason>"] [--by <actor>]
$ tillr item close --store .tillr/state.db --item <item-id> --reason "<reason>" --by <actor>
$ tillr item reopen --store .tillr/state.db --item <item-id> --reason "<reason>" --by <actor>
$ tillr item decision list --store .tillr/state.db
$ tillr item verdict record --store .tillr/state.db --input <verdict.json> --now <rfc3339>
```

### Drain loop and diagnostics (`tillr drain`)

```console
$ tillr drain why --store .tillr/state.db [--policy <policy.json>] [--lanes <N|list>] [--json]
$ tillr drain loop --root <repo> --store <store> --policy <policy.json> --lanes <N> [--check] [--exit-when-drained]
$ tillr drain probe-backend --store .tillr/state.db --backend <name> -- <smoke-command...>
$ tillr drain clear-fault --store .tillr/state.db --item <item-id> --fault <event-seq>
$ tillr drain retry --store .tillr/state.db --item <item-id> --run <run-id> --by <actor> --why "<reason>" --now <rfc3339> [--acknowledge gate-failed|unjudged]
$ tillr drain scale --root <repo> --lanes <N>
$ tillr drain stop --root <repo>
$ tillr drain workflow check <workflow-file-or-dir>
```

### Gate and landing (`tillr gate`, `tillr land`)

```console
$ tillr gate checks --base <sha> --head <sha> --logs <dir> [--checks tools/gate/checks] [--fast-tree] [--heavy-tree]
$ tillr gate change --base <sha> --head <sha> --run <run-id> --worktree <dir>
$ tillr gate plan --input <change.json> --registry <checks-dir>
$ tillr gate run --input <plan.json> --worktree <dir>
$ tillr land one --repo <repo> --store <store> --branch <branch> --gate <program>
$ tillr land pass --repo <repo> --store <store> --item <item-id> --run <run-id> --base <sha> --head <sha> --verdict <verdict.json>
```

### Views and watches (`tillr view`, `tillr watch`)

```console
$ tillr view status --store .tillr/state.db [--item <item-id>] [--text | --json]
$ tillr view waiting --store .tillr/state.db [--json]
$ tillr view alarms --store .tillr/state.db [--json]
$ tillr view running --store .tillr/state.db [--json]
$ tillr view lanes --store .tillr/state.db [--json]
$ tillr view passes --store .tillr/state.db [--last 3] [--json]
$ tillr view events --store .tillr/state.db [--since <seq>]
$ tillr watch status --store .tillr/state.db --rules <dir> [--json]
$ tillr watch eval --once --store .tillr/state.db --rules <dir>
$ tillr watch lint [<repo-or-dir>] [--charter <path>] [--workflows <path>] [--policy <path>] [--rules <path>]
```

---

## 3. What an agent must not do

1. **Never write to the SQLite store directly.** Always use `tillr store`, `tillr item`, `tillr drain`, `tillr land`, or `tillr watch` commands. Direct SQLite writes bypass append-only event triggers, validation, and WAL coordination.
2. **Never touch another lane or the main checkout from a lane worker.** A lane worker operates only inside its own assigned worktree and its own declared scratch directory. Do not modify `.tillr/`, other item/lane directories under `.tillr/items/` or `.tillr/lanes/`, git refs other than your own lane branch, or the main checkout.
3. **Never bypass hooks or gate checks.** Do not force-land commits onto the trunk outside `tillr-land`, skip `describe` or `complete-item` hook contracts, or alter commit authorship with invented names or emails (always read `git config user.name` and `git config user.email`).
4. **Never add, edit, or remove `tillr: item ...` directive blocks without authorization.** Only saved idea intake and document triage flows add or edit item directive blocks; ordinary build and docs runs must leave them untouched.
5. **Never run `just done` inside a lane.** `just done` acquires the landing lock and runs the full gate; the landing gate runs it at landing time. Inside a lane, run only targeted tests for the behavior you changed (or `tillr-gate shards` via `go run -C tools/tillr-gate . shards` when a broad selection is needed) and `uvx vantage-check <file>` on each changed Markdown file.
6. **Never kill processes by name or pattern.** Do not run `pkill`, `killall`, or `kill` fed from `pgrep` — other lanes, the landing gate, and the trunk proof run the same binaries as the same user. End only a process ID you started yourself.
7. **Never guess decisions that belong to the owner.** Under [Authority, not approval](../reference/authority-not-approval.md#terms), decisions involving irreversible loss, spending or external commitments, external publication, or changing policy and accepting risk belong to the owner. Stop and surface a clear question with concrete options rather than guessing.
