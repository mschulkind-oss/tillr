---
title: "A goal monitor: a scheduled check-in that inspects the queue and files what it finds"
status: in-review
stage: DESIGN
next: "Nothing until it is picked up: the design is on the tillr roadmap's Icebox. Picking it up means a use for a scheduled check-in, which answers OQ-GM1, then the owner's review of the designer's decisions in the ledger, then the two slices of Section 10, starting with scheduled tasks."
depends-on:
  - scheduled-tasks.md
  - ../reference/tillr-item-graphs.md
tags: [tillr, monitor, scheduling, graphs, design]
summary: "Parked. A goal is a recurring item whose criteria are a property of the system rather than a change to it. The monitor is that item, the clock that makes it due, and a pre-approved check-in graph whose collector nodes are mechanical, whose one agent node decides, and whose one command node files. Nothing is built."
---

# A goal monitor: a scheduled check-in that inspects the queue and files what it finds

> **Delivered copy.** This is `docs/design/tillr-goal-monitor.md` from this repository at commit `5fabdf36fe` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-goal-monitor.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Parked on [the tillr roadmap's Icebox](../tillr/roadmap.md#icebox) until there is a use for it. Nothing is built: `rg 'due_at|goal-checkin|graph.approved' tools config -g '!*.md'` returns nothing. Neither slice in [§10](#10-the-downstream-slices) is in the queue, and both questions are blocked. The [ledger](#11-decision-ledger)'s rows are the designer's decisions; the owner has ruled on none of them. This document states the decisions and the rules an implementer needs; it is short on purpose while the design is parked.

> **In short.** A monitor that inspects the queue on a schedule and decides whether to act is a composition, not a new mechanism: a goal is a recurring item, the clock is [`due_at`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/scheduled-tasks.md#21-the-two-columns), the execution is a [pre-approved item graph](../reference/tillr-item-graphs.md#workflows-selection-and-expansion), and the decision is one agent node per check-in. The one new tracked file is `config/tillr/workflows/goal-checkin@v1.json`.

**Needs your ruling:** None. Blocked while the design is parked: [OQ-GM1](#OQ-GM1) (what the first check-in watches) and [OQ-GM2](#OQ-GM2) (whether anything outside the loop pages the owner).

**Reads with:** [`scheduled-tasks.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/scheduled-tasks.md) (the clock, unbuilt) and [`tillr-item-graphs.md`](../reference/tillr-item-graphs.md) (the graph engine, built).

**Terms**, each coined here. A **goal** is a recurring item whose frozen criteria state a property the system should hold, and whose correct outcome on a check-in may be no change at all. A **check-in** is one run of a goal's graph: the mechanical collection, the decision, and the items it files. The **goal monitor** is the composition of a goal, the clock that makes it due, and its check-in graph. The **outside authority** is the actor that can see the monitor is not running, because it is not the monitor. *Not due*, *due time* and *recurrence* are defined in [scheduled-tasks §2](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/scheduled-tasks.md#2-the-item-fields).

## 2. What this composes

| Piece | What it provides |
| :--- | :--- |
| The clock: `items.due_at`, `items.recur_interval_ms` ([scheduled-tasks §2.1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/scheduled-tasks.md#21-the-two-columns)) | A due instant, an interval, due-ness as an admission check, and re-arm at completion. No parser, timer or daemon |
| The execution: [item graphs](../reference/tillr-item-graphs.md#workflows-selection-and-expansion) | Versioned templates under `config/tillr/workflows/`, typed artifacts, a declared exit. No per-goal script |
| The decision: one agent node per check-in | The store as memory, and deduplication by signature. No resident agent and no second dispatcher |

The monitor is for goals whose failure is a state: a queue that has stopped landing, an item held past its promise. It is not for a goal whose failure is the loop itself; [§7](#7-the-bootstrap-problem-who-watches-the-monitor) names who covers that. The monitor files items. It does not rule, and a goal whose remedy needs a ruling files an escalation item naming the owner.

## 3. The goal is the item's frozen criteria

A goal is not a new record (`D-GM2`). It is an item whose `criteria[]` state a property of the system, such as "no check on trunk has been red for more than seven days". What to inspect is in the graph's `params`, and how often is `recur_interval_ms` and `due_at`. Each criterion is written so that a no-op is judgeable as done, or the schedule never advances ([scheduled-tasks §5.3](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/scheduled-tasks.md#53-re-arming-in-place-and-the-transition-the-machine-is-missing)). Only the owner and the coordinating agent may write a goal's criteria, interval or graph reference; the seat doing the work may not move the promise it is judged by.

**Rejected:** a `goals` table. It would need its own due time and completion rule, which would disagree with the item's, and a goal in a side table appears in no view the owner already reads.

## 4. The clock is a due time, not a cron job

A goal has `recur_interval_ms` set and `due_at` set to its first check-in. On completion it re-arms in place, `next_due = completed_at + recur_interval_ms` ([scheduled-tasks §5.1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/scheduled-tasks.md#51-the-arithmetic)). Three consequences (`D-GM3`):

- **Cadence is anchored on completion and drifts.** A check-in delayed by six hours read the same queue.
- **A missed check-in is an overdue item, not a skipped one.** The schedule has one slot, so three missed days are one late check-in.
- **Failure does not advance the schedule.** A failed check-in leaves the item due.

The loop notices a due time that passes while it is idle, because it re-plans on its fallback poll (`DefaultFallbackPollInterval` in [`tools/tillr-drain/drain/trigger.go`](../../tillr-drain/drain/trigger.go)). One mode does not, and [§8](#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained) is its rule.

## 5. The check-in graph and its two keys

`config/tillr/workflows/goal-checkin@v1.json` is the check-in (`D-GM4`):

| Node | Kind | Access | Reads | Emits |
| :--- | :--- | :--- | :--- | :--- |
| `collect-queue` | `command` | `read_only` | The store: queue depth, oldest in-queue age, held items and reasons, overdue items | `queue_snapshot.json` |
| `collect-gate` | `command` | `read_only` | The trunk's last gate verdict and the age of the newest red check | `gate_snapshot.json` |
| `collect-goal` | `command` | `read_only` | The goal's own mechanical check, named by `params.check_ref` | `goal_check.json` |
| `decide` | `agent` | `read_only` | The three snapshots | `actions.json` |
| `file` | `command` | `read_only` | `actions.json` | `filed.json` |

- **Every collector is a command.** The inspection is evidence, so it is not a model's opinion. A collector that cannot produce its artifact fails the check-in, and the item stays due.
- **One agent node, which decides meaning only.** It writes nothing but `actions.json`, runs no command and files nothing.
- **No write node and no candidate.** The exit is `requires_candidate: no_commit`. A check-in may write the store and may never write a file.
- **Parameters:** `goal` (the goal item's id), `check_ref` (the registered command that performs the goal's check) and `max_actions` (1 to 3, default 3). A check-in that finds more violations files `max_actions` items plus one that names the bound and the count; a bound that truncates silently is refused (`D-GM12`).
- **An action's item id is derived from its signature** (`D-GM5`): `goal-<goal>-<sig8>`, where the signature is the goal, the violated clause and the narrowest stable subject. It contains nothing that moves on its own, such as a timestamp, a run id or the trunk tip. A second check-in that sees the same violation finds the id taken, files nothing and records the entry as `already-open`.
- **Two keys guard the template** (`D-GM6`). The owner's approval is a `graph.approved` record naming the template's sha256, written by a seat-restricted `tillr graph approve`. The gate's validation is the tracked-template check of [`tools/gate/checks/workflows.json`](https://github.com/mschulkind-oss/tillr/blob/main/tools/gate/checks/workflows.json). The loader instantiates a template only when its committed bytes hash to an approved digest, and an item in flight keeps the digest it began with.

**Done looks like:** a due goal item dispatches, runs five nodes and re-arms; `filed.json` names the snapshot excerpt behind every action; a second check-in over an unchanged queue files nothing and still completes; a failed collector leaves the item due with nothing filed, because filing is the last node.

## 6. The anti-self-modification fence

Four layers (`D-GM7`):

1. **The check-in writes no file,** so it cannot edit its template, its criteria or the code it inspects.
2. **The item's fence excludes the workflow directory.** A goal item whose `allowed_paths` include `config/tillr/workflows/` is refused by the planner as a named hold.
3. **The approval record is seat-restricted** to the owner and the coordinating agent.
4. **The goal's own definition is seat-restricted** in the same way.

The fence is not a security boundary. It makes the monitor's ordinary path incapable of changing itself, so that such a change is a named act with a record.

## 7. The bootstrap problem: who watches the monitor

The monitor cannot repair the drain, the schedule or itself, and this design does not try (`D-GM8`). Each way of being dead is a visible fact with a named reader:

| What is broken | What the queue shows | Who sees it |
| :--- | :--- | :--- |
| The drain is not running | The goal item is due and no run follows | The owner, reading the queue |
| The schedule never fires | The item is held, and `tillr drain plan` prints the hold's reason | The owner, reading the plan |
| The check-in wrongly says "fine" | `filed.json` and the snapshots, beside a clause whose mechanical check says otherwise | The judge at the item's verdict, then the next check-in |
| The check-in files a wrong action | One extra item, with the excerpt that justified it | The gate refuses it, or the owner closes it |

- **The outside authority is the owner, reading the queue.** Anything else would share the monitor's machine, store and credentials, and so its failures. The owner may start and stop the drain, hold and unhold an item, and rule on what a check-in filed. Changing the template's digest or a goal's criteria is a second, recorded act.
- **When the monitor cannot act:** a failed collector fails the check-in; an agent that cannot decide files one escalation item naming the input and the clause; evidence whose remedy is a ruling files a blocked item naming the owner and the question.
- **A missed check-in is visible** (`D-GM9`): the due time does not move without a completion, the view derives the queue at read time, the last check-in is a run row, and a `no-op` that cites no evidence fails its criterion.
- **The outside authority is not needed** when the monitor's store, root and drain are disjoint from the watched system's (`D-GM10`). The dependency moves one level out; it is not removed.

## 8. A not-due item when the loop is told to exit once drained

Started with `--exit-when-drained`, the loop stops and reports the queue drained when nothing is dispatchable and no run is in flight. A scheduled item with nothing else in the queue is that state, so the report would be false.

**The rule: a not-due item is a wait, not a stop** (`D-GM11`). In that mode "drained" means no dispatch, no run in flight, and no item due later. With a not-due item present the loop waits and re-plans on its store poll.

- **The decision carries the due instant** as a field, so the stop check parses no prose.
- **A not-due item is waiting, not blocked.** It writes no blocked event, holds no worktree and delays no lower-ranked item ([scheduled-tasks §3.4](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/scheduled-tasks.md#34-what-a-not-due-item-does-to-ordering-and-to-counts)).
- **The operator's line names the wait** with the due instant.
- **No test asserts elapsed time.** Every case drives the clock the loop is given.

## 9. What is refused

- **The queue as a scheduler** ([scheduled-tasks §9](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/scheduled-tasks.md#9-staging-v1-v2-and-the-criterion)): no cron expressions or time zones, no daemon or host timer, no catch-up of missed check-ins, no preemption or overdue boost, no expiry and no notification on a due transition.
- **The monitor as an actor:** it does not land, gate, hold, unhold, retire or delete; it is not resident; it writes no command line of its own, since every command node is a tracked `command_ref`; and its graph carries no clock or sleep.

## 10. The downstream slices

Neither is in the queue (`D-GM13`: the clock first, then the check-in).

| Slice | Builds | Depends on |
| :--- | :--- | :--- |
| Scheduled items | [scheduled-tasks](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/scheduled-tasks.md#9-staging-v1-v2-and-the-criterion)' two nullable columns, the `not-due` check, the re-arm at completion, the command flags, the view detail, and the rule of [§8](#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained) | Nothing |
| The check-in graph | The template, the `graph.approved` record and `tillr graph approve`, the digest pin and its refusal, the four command registrations, and the first goal item | The clock slice |

## 11. Decision Ledger

Every row is a designer's decision. None is the owner's ruling, the owner may overturn any of them, and none is built.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-GM1` | The monitor is a composition: a recurring item, a pre-approved graph, and one deciding agent node per check-in. No new record kind, no scheduler, no second dispatcher | 2026-11-14 | [§2](#2-what-this-composes) | — |
| `D-GM2` | A goal is the item's frozen criteria; its metadata lives in `criteria[]`, the graph's `params`, `recur_interval_ms` and `due_at` | 2026-11-14 | [§3](#3-the-goal-is-the-items-frozen-criteria) | — |
| `D-GM3` | The schedule is `due_at` plus `recur_interval_ms`; a missed check-in is an overdue item; failure never advances the schedule | 2026-11-14 | [§4](#4-the-clock-is-a-due-time-not-a-cron-job) | — |
| `D-GM4` | The check-in is `goal-checkin@v1.json`: read-only command collectors, one read-only agent that decides, one command that files. No write node; `requires_candidate: no_commit` | 2026-11-14 | [§5](#5-the-check-in-graph-and-its-two-keys) | — |
| `D-GM5` | Actions deduplicate on an item id derived from a signature that excludes anything that moves on its own; a repeat files nothing and records `already-open` | 2026-11-14 | [§5](#5-the-check-in-graph-and-its-two-keys) | — |
| `D-GM6` | The template is guarded by two keys, the owner's `graph.approved` digest and the gate's template validation, and an item in flight pins its digest | 2026-11-14 | [§5](#5-the-check-in-graph-and-its-two-keys) | — |
| `D-GM7` | The anti-self-modification fence has four layers | 2026-11-14 | [§6](#6-the-anti-self-modification-fence) | — |
| `D-GM8` | The monitor cannot repair the drain, the schedule or itself. The outside authority is the owner reading the queue, with three standing acts | 2026-11-14 | [§7](#7-the-bootstrap-problem-who-watches-the-monitor) | — |
| `D-GM9` | A missed check-in is visible through four mechanisms; a `no-op` with no cited evidence fails its criterion | 2026-11-14 | [§7](#7-the-bootstrap-problem-who-watches-the-monitor) | — |
| `D-GM10` | The outside authority is not needed when the monitor's store, root and drain are disjoint from the watched system's | 2026-11-14 | [§7](#7-the-bootstrap-problem-who-watches-the-monitor) | — |
| `D-GM11` | A not-due item is a wait, not a stop. The rule binds only the mode that exits when the queue is drained | 2026-11-14 | [§8](#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained) | — |
| `D-GM12` | `max_actions` bounds one check-in to 3 filings plus one bound-hit item; a silently truncating bound is refused | 2026-11-14 | [§5](#5-the-check-in-graph-and-its-two-keys) | — |
| `D-GM13` | The two slices land in the order clock, then check-in | 2026-11-14 | [§10](#10-the-downstream-slices) | — |

## 12. Open questions

Both questions are blocked while the design is parked; neither is asked until it is picked up.

1. 🔒 **OQ-GM1: What should the first scheduled check-in watch, and how often?**

   A use is a property you check today by looking: you open the queue screen and see whether something has drifted. The monitor would take that look on a schedule and file an item when the property fails. Which property comes first is undecided.

   - **A — A dependency that has fallen behind its upstream's releases**, checked weekly.
   - **B — The queue screen agreeing with the store**, checked daily.
   - **C — Something else**, named when the need appears.

   <!-- vantage: question id=OQ-GM1 blocked-on="a-use-for-a-scheduled-check-in" -->

   Blocked until you have a use for a scheduled check-in. That use is the answer, so no leaning is offered.

   **Answer:**
   > _(empty — fill in when decided)_

2. 🔒 **OQ-GM2: Should something outside the loop page you when a check-in is overdue?**

   The monitor cannot notice that it is not running. If the loop is down, a due check-in sits in the queue marked overdue, and you learn of it when you next open the queue screen. A timer on the host, outside the loop, could notice a check-in overdue by more than its interval and page you. It would be a change to the host, which only you can make.

   - **A — No timer.** You look at the queue, where an overdue check-in is visible.
   - **B — A host timer that only pages.** One more thing to install and keep alive on each machine. If it ever does more than page, it is a second scheduler.

   <!-- vantage: question id=OQ-GM2 blocked-on="a-check-in-has-run" leaning="No timer yet (A): a pager is worth building once a check-in has actually been missed, and building it first would make the monitor's liveness look solved before it has been observed once." -->

   _Leaning:_ No timer yet (A): a pager is worth building once a check-in has actually been missed, and building it first would make the monitor's liveness look solved before it has been observed once.

   Blocked until the design is picked up and a check-in has run.

   **Answer:**
   > _(empty — fill in when decided)_
