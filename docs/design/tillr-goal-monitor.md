---
title: "A goal monitor: a scheduled check-in that inspects the board and files what it finds"
date: 2026-11-14
status: in-review
stage: DESIGN
next: "Nothing until it is picked up: the owner iceboxed this design on 2026-11-17 (\"let's icebox this goal stuff until we have a use for it again\"), on the tillr roadmap's Icebox. Picking it up means a use for a scheduled check-in, which answers OQ-GM1, then the owner's review of the designer's decisions in the ledger, then filing the two slices of Section 10, starting with scheduled tasks."
depends-on:
  - scheduled-tasks.md
  - ../reference/tillr-item-graphs.md
tags: [tillr, board, monitor, scheduling, graphs, design]
summary: "A goal is a recurring item whose criteria are a property of the system rather than a change to it. The monitor is that item, the clock that makes it due, and a pre-approved check-in graph whose collector nodes are mechanical, whose one agent node decides, and whose one command node files. The goal is the item's frozen criteria, so there is no new record kind, and a missed check-in is simply an overdue item. The monitor cannot repair the drain, the schedule, or itself, and the design says so: the outside authority is the owner reading the board, and it stops being needed when the monitor runs in an instance that is not the one being developed."
---

# A goal monitor: a scheduled check-in that inspects the board and files what it finds

**Status:** 2026-11-17: **iceboxed by the owner**, who said "let's icebox this goal stuff until we have a use for it again." It is parked on [the tillr roadmap's Icebox](../tillr/roadmap.md#icebox), not abandoned and not accepted. Nothing is built, neither slice in [§10](#10-the-downstream-slices) is in the queue, and both questions are blocked so that they stop asking. The [ledger](#11-decision-ledger)'s rows are the designer's decisions of 2026-11-14; the owner has ruled on none of them.

> **In short.** The owner asked for a monitor that inspects the board on a schedule and decides whether to act, built out of pieces this repository already has. It is **a composition, not a new mechanism**: a *goal* is a recurring item whose frozen criteria are a property of the system rather than a change to it, the clock is [`due_at`](scheduled-tasks.md#21-the-two-columns), the execution is a [pre-approved item graph](../reference/tillr-item-graphs.md#workflows-selection-and-expansion) whose leaves are read-only collectors, one deciding agent, and one filing command, and the decision is a stateless planner invoked once per check-in. The one new tracked file is `config/tillr/workflows/goal-checkin@v1.json`, and it is guarded by two keys and a fence because it is the file that says what the monitor may decide.

**Why it matters.** Every day the loop runs, someone reads the board to notice that the queue has not landed anything since Tuesday, or that an item has been held past its promise. That reading is *steering*, and it is the owner's attention spent as a polling loop. A goal monitor turns the repeating part of that reading into a due item: the schedule never forgets, the inspection is mechanical where it can be, and the only thing the agent does is decide what the mechanical collection means.

**The shape.** A recurring item carries a goal as its frozen criteria; on each due tick the item's pre-approved graph collects the evidence the goal names, an agent decides whether the evidence violates the goal, and a command files the resulting items under signature-derived ids so a repeated check-in cannot file the same work twice. The check-in writes no tracked file and can never produce a candidate commit.

**Cost.** One nullable-column schema change and one admission check that [scheduled-tasks already specifies](scheduled-tasks.md#21-the-two-columns); one rule for the loop's exit-once-drained mode ([§8](#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained)); one tracked graph template and an approval record to guard it, which is new and exists nowhere in the tree today; and four registered `command` leaves. No new binary, no daemon, no cron parser, and no second dispatcher.

**Start at** [§7](#7-the-bootstrap-problem-who-watches-the-monitor), what the monitor cannot do for itself; the rest is composition.

**Needs your ruling:** None. Blocked while the design is iceboxed: [OQ-GM1](#OQ-GM1) (what the first check-in watches) and [OQ-GM2](#OQ-GM2) (whether anything outside the loop pages the owner).

**Reads with:** [`scheduled-tasks.md`](scheduled-tasks.md) (the clock, unbuilt) and [`tillr-item-graphs.md`](../reference/tillr-item-graphs.md) (the graph engine, built).

**Terms.** *Goal* (coined here) — a recurring item whose frozen criteria state a property the system should hold, not a change to make, and whose correct outcome on a check-in is a no-op that the judge can still call done. *Check-in* (coined here) — one run of a goal's graph: the mechanical collection, the decision, and the actions it files. *Goal monitor* (coined here) — the composition of a goal, the clock that makes it due, and its check-in graph. *Outside authority* (coined here) — the actor that can see the monitor is not running, because it is not the monitor. *Not due*, *due time*, *recurrence*, *drain*, *admission* and *rank* are [scheduled-tasks §2](scheduled-tasks.md#2-the-item-fields)'s and are used here in exactly that sense. The **on-call** is the role that owns a breakage (a check that is red on the trunk) from first sight to repair. It is not a resident agent whose conversation holds the memory: it is a durable record of the breakage, a stateless decision step invoked once per event, and a short-lived fixing lane the drain creates. The monitor is the on-call's scheduled half. **Steering** is the manual act it replaces: a person reading the board on a schedule and directing work from what they see. **The board**, here and throughout, is the observatory's queue screen; see [observatory-design.md](../reference/observatory-design.md#terms-and-non-purposes).

## 1. The request, verbatim

The owner's request, copied without repair:

> I want to design Some sort of like monitor Technician repair guy. I think we maybe had a name for it somewhere If you dig through Doc's we decided on terminology here that basically is this process of the steering so there is like you can define a tiller goal or something like board parity or whatever it is and then uh it just on some schedule inspects everything and decides if some action needs to be taken so some this would be I guess a cron job in tiller and the job would be spawning some graph that's predefined and pre-approved that is like the check-in or something like that and it can have a spot for metadata that is like the goal or something like that and I feel like we could accomplish this exact same act of steering situation where you're just manually fixing drain bugs as they occur. I know it would be hard to do this on the drain itself because if the drains broken how does it fix itself? or if the cron job's broken, has it fixed itself? So we still, at this point, need this outside guidance, but this would be the idea of doing this for features that don't rise to that level. So... Or I guess you just can't do it on yourself because you could foreseeably, in the future when Tiller is more stable, have a Tiller instance that is your always functional instance and Tiller features could be developed on a sandbox of what you're developing, which is probably the model we will switch to in not too long once things start working.

Every clause of it, and where this document answers it:

| The request's clause | Answered in |
| :--- | :--- |
| "monitor technician repair guy … we maybe had a name for it … terminology here" | The name is settled: the **on-call** is the role and the **goal monitor** is its scheduled half. **Steering** is the manual act being replaced, and it is not renamed. See the terms paragraph above |
| "you can define a tiller goal or something like board parity or whatever it is" | A goal is a recurring item's frozen criteria. No new record kind. [§3](#3-the-goal-is-the-items-frozen-criteria) |
| "it just on some schedule" | `due_at` plus `recur_interval_ms`, decided by [scheduled-tasks](scheduled-tasks.md#21-the-two-columns). The schedule is the item's, not the monitor's. [§4](#4-the-clock-is-a-due-time-not-a-cron-job) |
| "inspects everything and decides if some action needs to be taken" | The check-in graph: mechanical collectors, one deciding agent, one filing command. [§5](#5-the-check-in-graph-and-its-two-keys) |
| "this would be I guess a cron job in tiller" | Refused as a cron job, adopted as a due item. There is no cron parser, no timer and no daemon. [§4](#4-the-clock-is-a-due-time-not-a-cron-job), [§9](#9-what-is-refused) |
| "spawning some graph that's predefined and pre-approved that is like the check-in" | `config/tillr/workflows/goal-checkin@v1.json`, versioned, digest-pinned, and approved by two independent keys. [§5](#5-the-check-in-graph-and-its-two-keys) |
| "it can have a spot for metadata that is like the goal" | The goal is the item's criteria; the graph's bounded `params` carry the *inspection plan*, not the goal. [§3](#3-the-goal-is-the-items-frozen-criteria) |
| "accomplish this exact same act of steering … manually fixing drain bugs as they occur" | It replaces the scheduled *look*, not the drain-bug repair. The boundary is stated, and it is the owner's own line: "features that don't rise to that level". [§2](#2-what-this-composes-and-what-it-refuses-to-re-implement) |
| "if the drains broken how does it fix itself? or if the cron job's broken, has it fixed itself?" | It cannot, and the design does not pretend otherwise. The four ways the monitor can be dead, who sees each, and what that actor may do. [§7](#7-the-bootstrap-problem-who-watches-the-monitor) |
| "we still, at this point, need this outside guidance" | Yes. The outside authority is named, with its standing authority bounded, and the condition that retires it is stated in the owner's terms. [§7](#7-the-bootstrap-problem-who-watches-the-monitor) |
| "have a Tiller instance that is your always functional instance and Tiller features could be developed on a sandbox" | That is the retirement condition, made checkable: the monitor's store, root and drain are disjoint from the watched system's. [§7.5](#75-when-the-outside-authority-is-no-longer-needed) |

## 2. What this composes, and what it refuses to re-implement

The monitor is **a composition of three pieces plus one tracked file**. It is not a
scheduled item *or* a graph *or* a new record kind; it is a scheduled item **whose** graph is a
check-in, and the goal is carried in a field the item already has.

| Piece | What it provides | What this design does not re-implement |
| :--- | :--- | :--- |
| The clock: `items.due_at`, `items.recur_interval_ms` ([scheduled-tasks §2.1](scheduled-tasks.md#21-the-two-columns)) | A nullable due instant, a nullable interval, due-ness as a derived admission gate, the single-slot no-queue property, and re-arm at the completion transition | Any parser, timer, scheduler or daemon. The monitor's cadence *is* the interval |
| The execution: pre-approved item graphs ([item graphs](../reference/tillr-item-graphs.md#workflows-selection-and-expansion)) | Versioned JSON templates under `config/tillr/workflows/`, three stage kinds over one argv boundary, static flattening, typed producer-bound artifacts, declarative `exit` | A per-goal workflow script, model-authored argv, a second sequencing runtime |
| The decision: one stateless planner per check-in | The durable record as memory, a stateless planner invoked per event, mechanical dedup by signature, and the rule that the planner writes records while the drain is the sole actor | A resident on-call, a second dispatcher, per-event bespoke scripts |
| `config/tillr/workflows/goal-checkin@v1.json` | — | This is the one thing that is new, and it is a data file, not code |

**The boundary the owner drew, kept.** The monitor is for goals whose failure is a *state*: board
parity, a queue that has stopped landing, an item that has been held past
its promise. A check that turns the trunk red is not among them: automatic revert answers it within
minutes, with no schedule. It is **not** for a goal whose failure is the
loop itself. If the drain is not running, or the schedule never fires, or the store will not open,
the monitor is not slow — it is absent, and an absent monitor cannot report its own absence.
[§7](#7-the-bootstrap-problem-who-watches-the-monitor) names what replaces it there, and that answer
is a person, by design rather than by omission.

**What it does not replace.** Steering is not only an inspection: it also directs an
expensive design seat with dated measurements, and it carries the owner's decisions back into work.
The monitor files items; it does not consult a designer, and it does not decide what work to do. A
goal whose remedy needs a ruling is a goal whose check-in files an escalation item naming the owner,
and the check-in then ends. **Deciding is the agent's; ruling is never the agent's.**

## 3. The goal is the item's frozen criteria

A goal is **not** a new record. An item already carries `title`, `body_ref`, a frozen `criteria[]`,
a `design_doc`, a fence, a rank and a state; a goal is the same item with two properties:

1. Its `criteria[]` state a property of the system — "no check on trunk has been red for more than seven days", "the queue's oldest in-queue
   item is younger than the trunk's last landing plus two days" — rather than a change to make.
2. Its correct outcome on a check-in may be **no change at all**, and that outcome must still be
   judgeable as done.

That second property is the load-bearing one, and it is not new here: [scheduled-tasks §5.3](scheduled-tasks.md#53-re-arming-in-place-and-the-transition-the-machine-is-missing) already records that a recurring
item whose correct outcome is "nothing to do" produces no candidate, so the criterion has to be
written so a no-op is judgeable as done, or the schedule never advances. A goal is the item kind
that obligation was written for.

**Why not a `goals` table.** Three reasons, in order of weight.

- **It would be a second readiness rule.** A goal record would need its own due time, its own
  interval and its own completion transition, because the item fields that already do that are not
  attachable to a new table. Two schedules that mean the same thing will disagree, and the one that
  disagrees will be discovered by a check-in that silently stops.
- **The board is the record the owner already reads.** A goal that is an item appears in the queue,
  in `tillr item list`, and in the view's waiting counts, with a due time and a last outcome. A goal
  in a side table appears nowhere until someone builds a second view for it.
- **Deletion test.** Remove the goal concept entirely and every field the monitor needs still
  exists; remove `items`, and nothing does. A concept whose removal changes no field is a name, not
  a schema — so it stays a name.

**Where the goal's metadata goes, then.** Three fields, each already existing:

| The owner's "spot for metadata" | Its home |
| :--- | :--- |
| What the goal asserts | `items.criteria[]`, frozen, judged like any other item's |
| What to inspect to check it | The graph's bounded `params` — `goal`, `check_ref`, `max_actions` ([§5.2](#52-parameters)) |
| How often, and when next | `items.recur_interval_ms`, `items.due_at` |

**Who may write it.** Only the owner and the coordinating agent, on exactly the ground
[scheduled-tasks §2.3](scheduled-tasks.md#23-the-cli-surface) already gives for a due time: the seat doing the work must not be able to move the promise
it is judged by. The monitor's own run seat writes neither the criteria nor the interval nor the
graph reference.

## 4. The clock is a due time, not a cron job

### 4.1 What the schedule is

A goal is an item with `recur_interval_ms` set and `due_at` set to its first check-in instant. On
completion the item re-arms in place — `next_due = completed_at + recur_interval_ms` — exactly as
[scheduled-tasks §5.1](scheduled-tasks.md#51-the-arithmetic) specifies. Nothing about the clock is new here; the monitor is that design's
consumer.

Three consequences the monitor depends on, all inherited rather than re-derived:

- **Cadence is completion-anchored and drifts.** A "daily" check-in runs every 24 hours from
  whenever it last completed. For a goal, which is convergence rather than an appointment, this is
  the right trade: a check-in delayed by six hours read the same board.
- **A missed check-in is an overdue item, not a skipped one.** The schedule has exactly one slot of
  memory, so three missed days are one check-in that is three days late — visible in the view as an
  item whose due time is in the past. There is no "silently skipped" state to detect, because there
  is nowhere for it to be represented.
- **Failure does not advance the schedule.** A check-in that fails leaves the item due, and the
  ordinary failure machinery retries it. A monitor that fails is a monitor whose next check-in is
  immediate, which is the property a monitor most needs.

### 4.2 The invariant, and what satisfies it

[scheduled-tasks §6.1](scheduled-tasks.md#61-the-invariant) records the invariant this whole design rests on:

> **The drain must retain a periodic re-evaluation tick, or the poll wait must take the earliest
> `due_at` as a wake deadline the way it already takes the run deadline.**

It holds. The loop is a daemon by default: when nothing is dispatchable it waits and re-plans, and with no store notification it re-plans on the fallback reconciliation poll (`DefaultFallbackPollInterval`, 15 seconds, in [`tools/tillr-drain/drain/trigger.go`](../../tools/tillr-drain/drain/trigger.go)). The owner ruled that default on 2026-11-15. A due time that passes while the loop is idle is therefore noticed on the next poll, with nothing written to wake it.

One mode does not satisfy it, and [§8](#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained) is the rule for it.

## 5. The check-in graph and its two keys

### 5.1 The template

`config/tillr/workflows/goal-checkin@v1.json`, tracked and gated like every template, is the check-in.
Its shape, in [item graphs](../reference/tillr-item-graphs.md#schema-stage-kinds-and-static-expansion)' stage kinds:

| Node | Kind | Access | Reads | Emits |
| :--- | :--- | :--- | :--- | :--- |
| `collect-board` | `command` | `read_only` | The store: queue depth, oldest in-queue age, held items and reasons, open breakages, overdue items | `board_snapshot.json` |
| `collect-gate` | `command` | `read_only` | The trunk's last gate verdict and the age of the newest red check | `gate_snapshot.json` |
| `collect-goal` | `command` | `read_only` | The goal's own mechanical check, named by `params.check_ref` | `goal_check.json` |
| `decide` | `agent` | `read_only` | The three snapshots | `actions.json` |
| `file` | `command` | `read_only` | `actions.json` | `filed.json` |

```text
exit:
  requires_nodes: [collect-board, collect-gate, collect-goal, decide, file]
  outputs: { filed: { from_node: file, from_output: filed } }
  requires_candidate: no_commit
```

Three properties of that table are decisions rather than formatting.

**Every collector is mechanical and every collector is `command`.** The inspection is the part that
must not be a model's opinion, because it is the part a later reader will use as evidence. A
collector that cannot produce its artifact fails the check-in, and the item stays due — a check-in
that ran on a missing snapshot would be a report about nothing.

**There is exactly one agent node and it decides nothing but meaning.** `decide` receives the three
snapshots and answers `actions.json`: for each goal clause, whether the snapshot violates it, and
what item — if any — the violation should file. It may write nothing, may run no command, and may
not file. The separation is what makes a wrong decision cheap: the blast radius is one
`actions.json`, which is retained as an artifact, so a bad check-in is auditable from its own record.

**There is no write node and no candidate.** `requires_candidate: no_commit`, and no node has
`access: "write"`, so the check-in cannot produce a worktree change and `Loop.Verify` never runs for
it. The `file` node's write is a **store** write, not a worktree write: `access` bounds the
worktree, and filing an item is the action the whole design exists to permit. This distinction is
the one an implementer is most likely to collapse, so it is stated as a rule: *a check-in may write
the board and may never write a file.*

### 5.2 Parameters

The template's `params` are typed data, bounded by the [item graphs](../reference/tillr-item-graphs.md#proposals-and-graph-revisions) rule that an argument never
interpolates a shell string and never alters an exit or evaluator invariant:

| Param | Type | Meaning | Default |
| :--- | :--- | :--- | :--- |
| `goal` | string, required | The goal item's id, echoed into every action's provenance | — |
| `check_ref` | string, required | The registered `command_ref` that performs the goal's mechanical check | — |
| `max_actions` | integer, 1–3 | The most items one check-in may file | `3` |

`max_actions` guards against one uniform decision filed as dozens of near-identical rows. A check-in that finds more than
`max_actions` violations
files **`max_actions` items plus one**, the extra naming the bound that was hit and the count. A
bound that silently truncates is worse than no bound, because the truncation is invisible.

### 5.3 What an action is, and why a second check-in cannot duplicate it

`actions.json` is a list. Each entry carries `title`, `body`, `criteria[]`, `allowed_paths[]`,
`design_doc` (optional), `signature`, and the snapshot excerpt it is drawn from. The `file` node
files each entry through a registered `tillr item` write command, and the item's **id is derived from the
signature** — `goal-<goal>-<sig8>` — so the store's primary key is the dedup mechanism.

The rule: dedup is a key lookup, not a
judgement. The signature is `(goal, violated clause, the narrowest stable subject)` — the check name
for a gate goal, the item id for a held-item goal. **What the
signature must not contain is anything that moves on its own** (a timestamp, a run id, the trunk
tip): a key that includes an observation mints a fresh
entity every time it is observed, and the duplication returns wearing a new key.

A second check-in that re-observes a violation finds the id taken and files nothing. The check-in
still completes as done, and `filed.json` records the entry as `already-open` with the existing
item's id — so "it is still broken and nobody has fixed it" is a recorded fact rather than a
silence.

### 5.4 The two keys

The template is the file that says what the monitor may decide. It is guarded by **two independent
keys**, and a change takes effect only when both have signed.

| Key | Who holds it | What it is | What it does alone |
| :--- | :--- | :--- | :--- |
| **Key 1 — the owner's approval** (new: nothing in the tree records an approval today) | The owner (or the coordinating agent), never a lane seat | A `graph.approved` record naming the template's `sha256`, written by `tillr graph approve goal-checkin@v1 --digest <sha>` | Loads nothing: with no gate-greened file behind the digest, the loader refuses an approved digest that matches no committed template |
| **Key 2 — the gate's validation** | The gate | The tracked-template check the gate runs ([`tools/gate/checks/workflows.json`](../../tools/gate/checks/workflows.json)): every template under `config/tillr/workflows/` parses, is acyclic, binds every port to a named producer, resolves every `command_ref`, and keeps every `write_scope` inside its item's fence — [item graphs](../reference/tillr-item-graphs.md#why-its-this-way)' `D-IG5` | Loads nothing: a committed template whose digest no `graph.approved` record names is refused at instantiation |

The loader's rule, in one sentence: *instantiate a template only when its committed bytes hash to a
digest the owner has approved, and refuse with the two digests named when they differ.* The owner's
approval is not a permission bit that a lane can flip, because the verb is seat-restricted on the
same ground as [scheduled-tasks §2.3](scheduled-tasks.md#23-the-cli-surface)'s write rule.

An in-flight item pins the digest at instantiation ([item graphs](../reference/tillr-item-graphs.md#selection-and-defaults)), so a concurrent edit cannot shift the
graph under a check-in that has already started. **A running check-in's template is the one it
began with, whatever the tree says now.**

### 5.5 What done looks like

The check-in's verdict is not its own. A `no_commit` item has no candidate, so `Loop.Verify` never
runs; the item completes on the judged-done path, and the judge's evidence is `filed.json` plus the
three snapshots, retained as artifacts under the run's directory. The observable outcomes that mean
the monitor was built as designed:

- A due goal item dispatches, runs five leaves, and re-arms with `due_at` exactly
  `completed_at + recur_interval_ms`.
- `filed.json` names, for every action, the snapshot excerpt it was drawn from; a check-in whose
  actions cite no evidence fails its criterion.
- A second check-in over an unchanged board files nothing, records each entry as `already-open`,
  and still completes as done.
- A check-in whose `collect-*` node fails produces a failed run, the item stays due, and the next
  tick retries it — with no partially-filed action set, because filing is the last leaf.
- The view shows the goal item as not-due with `due <instant> · every <n>`, and shows it as an
  ordinary ready item once due. There is no separate monitor surface to look at.

## 6. The anti-self-modification fence

Four layers, each mechanical, each refusing a different mistake. The first two exist because the
monitor is the one agent whose job is to decide what work the board should contain, which makes it
the one agent best placed to widen its own authority by accident.

1. **The check-in writes no file.** No node has `access: "write"` and the graph's exit is
   `no_commit` ([§5.1](#51-the-template)). A monitor that cannot write a file cannot edit its own template, its goal's
   criteria, or the code it is inspecting.
2. **The item's fence excludes the workflow directory.** A goal item's `allowed_paths` may not include
   `config/tillr/workflows/`, and the drain refuses one that does — in the planner's `decide`, beside
   the `fenceAdmitsNothing` refusal that already answers `no-fence` for an item whose fence names
   nothing, as a named hold rather than a review note. This is the layer that survives a future
   change that gives the check-in a write node for some unrelated reason.
3. **The approval record is seat-restricted.** `graph.approved` is writable by the owner and the
   coordinating agent only. The check-in's run seat cannot approve a template, so a compromised or confused check-in
   can, at worst, file items — which any lane can do, and which the gate and the judge then judge.
4. **The goal's own definition is seat-restricted.** Criteria, interval and `graph_ref` follow
   [scheduled-tasks §2.3](scheduled-tasks.md#23-the-cli-surface): the seat doing the work may not move the promise. A check-in cannot make
   itself due more often, cannot widen its own goal, and cannot point itself at a new graph.

**What the fence is not.** It is not a security boundary and it is not claimed as one. A lane seat
with the owner's own credentials can do anything the owner can; the fence's job is to make the
monitor's *ordinary* path incapable of self-modification, so that self-modification is a named act
with a record rather than a plausible edit nobody notices.

## 7. The bootstrap problem: who watches the monitor

The owner named it exactly: *"if the drains broken how does it fix itself? or if the cron job's
broken, has it fixed itself?"* The answer is **it does not, and this design does not try**. What it
does instead is make each way of being dead a different, visible fact with a named reader.

### 7.1 The four ways the monitor is dead

| # | What is broken | What the board looks like | Who sees it | What the monitor does |
| :--- | :--- | :--- | :--- | :--- |
| 1 | **The drain is not running**, so nothing dispatches | The goal item is due, and no run follows. `tillr view` derives the board from the store at read time, so due-ness is a fact it can render with no drain involved — once the clock slice adds due-ness to that projection ([§10](#10-the-downstream-slices)), the row shows ready with its overdue age | The owner, reading the board — or [OQ-GM2](#OQ-GM2)'s host timer, if the owner wants one | Nothing. It is not running |
| 2 | **The schedule never fires** — the item is held by a rule the plan refuses, or a re-arm was lost | The item is held, and `tillr drain plan` prints the hold's reason (a named rule, not prose) | The owner reading the plan or the hold | Nothing |
| 3 | **The check-in runs and wrongly says "fine"** — the false no-op | The `filed.json` and the three snapshots, retained with the run, beside a goal clause whose mechanical check says otherwise | The gate and the judge first, at the item's verdict; the *next* check-in after that; the owner if both miss | Nothing. It cannot grade itself: its verdict belongs to the judge |
| 4 | **The check-in files a wrong action** | One extra item in the queue, with the snapshot excerpt that justified it | The gate refuses it, or the owner closes it | Nothing. It does not hold, retire or edit its own actions |

The four together are the honest statement of the boundary in [§2](#2-what-this-composes-and-what-it-refuses-to-re-implement):
**the monitor is a client of the loop, not a substitute for it.** Every failure of the loop is
outside its reach, and the design's contribution is that each such failure is *visible* rather than
attributed to the monitor being slow.

### 7.2 The outside authority, named

The outside authority is **the owner, reading the board**. Not a second process, not a supervisor
agent, not the monitor watching itself. There is no plausible second candidate: anything else the
design could name would run on the same machine, under the same store, with the same credentials,
and would therefore be dead in failure mode 1 or 3 as surely as the monitor is. A supervisor that
shares a failure domain is not a supervisor.

**Its standing authority, without asking.** Three acts, all reversible, none of which the monitor may
perform:

- **Start and stop the drain.** The drain is a foreground process a person or an agent starts
  ([scheduled-tasks §6.1](scheduled-tasks.md#61-the-invariant)); re-launch is how failure mode 1 ends.
- **Hold and unhold an item.** Cheap, reversible, recorded — the throttle that already exists.
- **Rule on what the check-in filed.** An escalation item names the owner; the owner answers it, and
  the answer becomes the next item the drain dispatches.

What the outside authority may **not** do without a second, recorded act: change the graph template's
digest (that is key 1 plus key 2), or edit a goal's frozen criteria. That refusal is what keeps the
outside authority from *becoming* the monitor by accretion.

### 7.3 What the monitor does when it cannot act

Three refusals, each with a defined output rather than a silent degradation:

- **A collector cannot read its input.** The leaf fails, the check-in fails, and the item stays due.
  No partial action set is filed, because `file` is the last leaf and depends on all three
  collectors. A retry re-runs the whole check-in, which is safe because filing is idempotent on the
  signature.
- **The agent cannot decide** — an unreadable snapshot, an ambiguous clause. The check-in files **one**
  escalation item naming the input it could not read and the clause it could not evaluate, and
  completes as done. Guessing is the refusal; escalating is the action.
- **The evidence contradicts the goal but the remedy is a ruling.** The check-in files a `BLOCKED` item naming
  the owner and the exact question, and does not attempt the remedy.

### 7.4 How a missed check-in becomes visible rather than silent

Four mechanisms, in the order a reader would hit them:

1. **The due time does not move without a completion.** A missed check-in leaves `due_at` in the
   past; there is no "next slot" to skip to, because the schedule has one slot
   ([scheduled-tasks §5.2](scheduled-tasks.md#52-why-three-missed-weeks-are-one-run)). Overdue-ness is therefore the alarm, and it cannot be lost.
2. **The view derives the board at read time.** `tillr view` needs no drain and no monitor to say
   an item is ready, because the store is the only input; the expensive-looking failure is a
   rendering of a stored fact. (Due-ness has to join that projection — a change in the clock slice,
   [§10](#10-the-downstream-slices) — but the projection is where it belongs.)
3. **The last completed check-in is a run row.** "Last check-in 6 days ago" is derived from the
   latest terminal run, never stored twice, so it cannot disagree with the runs themselves.
4. **The verdict evidence is an artifact.** A check-in that ran and said "fine" left three
   snapshots and an `actions.json`; the next reader can falsify the claim without re-running
   anything. A `no-op` with no evidence fails the criterion that permits the no-op.

### 7.5 When the outside authority is no longer needed

The owner stated the condition and it is the right one: *"have a Tiller instance that is your always
functional instance and Tiller features could be developed on a sandbox of what you're developing."*

Made checkable, that is one predicate:

> The monitor's store, root and drain are disjoint from the watched system's.

When the monitor runs in an instance that is not the one being developed, failure mode 1 no longer
means "the thing being watched is also the thing that is broken": the watched system can be entirely
healthy while the monitor is dead, and the always-functional instance is the one whose own health is
the baseline. The bootstrap dependency is not *removed* — it is *moved one level out*, to an
instance whose whole purpose is not to be the system under change. The predicate is deliberately
mechanical: same `--store`, same `--root`, or same drain process, and the condition is not met.

**Recorded reversal condition.** If the always-functional instance is ever the one being developed —
because there is only one machine, or because the split was abandoned — the predicate is false again
and the outside authority is owed, whatever the design says.

## 8. A not-due item when the loop is told to exit once drained

By default the loop never stops for want of work ([§4.2](#42-the-invariant-and-what-satisfies-it)), so a goal that is not yet due simply waits in it. One mode is different. Started with `--exit-when-drained`, the loop stops and reports the queue drained when nothing is dispatchable and no run is in flight.

Once a due time exists, that report can be false. A scheduled item with nothing else in the queue is exactly that state — nothing dispatchable, no run — and it is also work that becomes ready with nothing written. A loop that exits there tells the operator the queue has nothing to run when it holds a check-in due tomorrow.

**The rule: a not-due item is a wait, not a stop.** In that mode "drained" means no dispatch, no run in flight, **and no item due later**. With a not-due item present the loop waits as it does by default and re-plans; it needs no timer of its own, because the store poll already re-plans it.

Four consequences an implementer would otherwise have to guess:

- **The decision carries the due instant.** The planner's decision for a not-due item states when it is due, as a field. A stop check that read it out of a formatted reason would be parsing prose.
- **A not-due item is waiting, not blocked.** It writes no blocked event and adds nothing to a blocked count. It holds no worktree and does not delay a lower-ranked item ([scheduled-tasks §3.4](scheduled-tasks.md#34-what-a-not-due-item-does-to-ordering-and-to-counts)).
- **The operator's line names the wait**, with the due instant: `the pile holds 1 item(s) not due until 2026-11-21T09:00:00Z; waiting`.
- **No test asserts elapsed time.** Every case drives the clock the loop is given, or writes a due time relative to the real one.

## 9. What is refused

Each refusal exists because the alternative smuggles in a promise this system cannot keep.

**The board as a scheduler** — refused in [scheduled-tasks §9](scheduled-tasks.md#9-staging-v1-v2-and-the-criterion) and repeated here because a
monitor is exactly the feature that tempts each one:

- A cron expression parser, timezones, daylight saving, or "09:00 sharp". The cadence is an
  interval, completion-anchored, and it drifts.
- A daemon, a systemd timer, a launchd job, or any wake-the-machine mechanism. *The board may
  remember a time; it may not promise a time.*
- Catch-up by default. Three missed check-ins are one check-in.
- Preemption of a running lane, or an overdue priority boost. A due goal competes by rank like
  anything else.
- Expiry of an overdue item, or notification on a due transition. The view showing it due **is**
  the notification system.

**The monitor as an actor** — new here, and the ones that keep it a client:

- **The monitor may not repair the drain, the schedule, or itself.** Failure modes 1–2 in
  [§7.1](#71-the-four-ways-the-monitor-is-dead) are outside its reach by construction, not by
  discipline.
- **The monitor may not land, gate, hold, unhold, retire or delete.** It files items. Every other
  verb stays with the drain and the owner.
- **The monitor may not be resident.** It is a stateless planner invoked per check-in, because context that lives in a conversation cannot be handed over, audited, or
  read after it is gone.
- **The monitor may not author argv.** Every command leaf is a tracked `command_ref`
  ([what a workflow node may never be](../reference/tillr-item-graphs.md#what-a-workflow-node-may-never-be)).
- **The check-in may not carry a clock, a sleep or a `now()`**, and it may not carry a timer of its
  own. The clock is the item's `due_at`, outside the graph; [what a workflow node may never be](../reference/tillr-item-graphs.md#what-a-workflow-node-may-never-be) refuses the inside.
- **A check-in may not file more than `max_actions` items** plus one bound-hit item. A horde of
  near-identical rows is the failure this bound exists to make impossible.
- **A check-in with no evidence may not claim a no-op.** A `no-op` verdict citing no snapshot fails
  the criterion that makes a no-op judgeable.

## 10. The downstream slices

Declared here and built later; none is in the queue, and this document touches no code.

| Slice | Builds | Depends on | What it must not do |
| :--- | :--- | :--- | :--- |
| `item-tillr-scheduled-items` | [scheduled-tasks](scheduled-tasks.md#9-staging-v1-v2-and-the-criterion)' V1 and V2: the two nullable columns and their schema change, the `not-due` check and its reason, the re-arm at completion and its event, the CLI flags, the view detail, and the rule of [§8](#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained) | Nothing. This is the substrate and it lands first | Add a scheduler, a daemon or a cron parser; assert elapsed time in a test |
| `item-tillr-goal-checkin-graph` | `config/tillr/workflows/goal-checkin@v1.json`; the `graph.approved` record and its seat-restricted `tillr graph approve`; the digest pin and its refusal; the four `command_ref` registrations; and the first goal item's definition | The clock slice above. The graph engine it runs on is built ([item graphs](../reference/tillr-item-graphs.md)) | Widen the item's fence to the workflow directory; give the check-in a write node; let the check-in approve a template |

**Order and why.** The clock is first because it is the only piece with no alternative. The check-in is second.

## 11. Decision Ledger

Every row is a **designer's decision** of 2026-11-14, made so that the design is complete. None is recorded as the owner's ruling, the owner may overturn any of them, and none is built.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-GM1` | The monitor is a **composition**: a recurring item (the clock), a pre-approved graph (the execution), and a stateless planner invoked per check-in (the decision). No new record kind, no scheduler, no second dispatcher | 2026-11-14 | [§2](#2-what-this-composes-and-what-it-refuses-to-re-implement) | — |
| `D-GM2` | A **goal is the item's frozen criteria**. The "metadata spot" is `criteria[]`, the graph's `params`, `recur_interval_ms` and `due_at` — all existing fields | 2026-11-14 | [§3](#3-the-goal-is-the-items-frozen-criteria) | — |
| `D-GM3` | The schedule is `due_at` + `recur_interval_ms`; a missed check-in is an overdue item; failure never advances the schedule | 2026-11-14 | [§4](#4-the-clock-is-a-due-time-not-a-cron-job) | — |
| `D-GM4` | The check-in is `config/tillr/workflows/goal-checkin@v1.json`: mechanical read-only collectors, one read-only agent that decides, one read-only-as-to-the-worktree command that files. No write node; `requires_candidate: no_commit` | 2026-11-14 | [§5.1](#51-the-template) | — |
| `D-GM5` | Actions dedup on a **signature-derived item id**; the signature excludes anything that moves on its own; a repeat files nothing and records `already-open` | 2026-11-14 | [§5.3](#53-what-an-action-is-and-why-a-second-check-in-cannot-duplicate-it) | — |
| `D-GM6` | The template is guarded by **two keys** — the owner's `graph.approved` digest and the gate's template validation of the committed file — and an in-flight item pins its digest | 2026-11-14 | [§5.4](#54-the-two-keys) | — |
| `D-GM7` | The **anti-self-modification fence** has four layers: no write node, the graph directory excluded from the item fence by a named refusal, the approval record seat-restricted, the goal's own definition seat-restricted | 2026-11-14 | [§6](#6-the-anti-self-modification-fence) | — |
| `D-GM8` | The monitor **cannot repair the drain, the schedule or itself**, and the design says so. The outside authority is the owner reading the board, with three bounded standing acts | 2026-11-14 | [§7](#7-the-bootstrap-problem-who-watches-the-monitor) | — |
| `D-GM9` | A missed check-in is visible through four mechanisms, the first of which is that `due_at` cannot advance without a completion; a `no-op` with no cited evidence fails its criterion | 2026-11-14 | [§7.4](#74-how-a-missed-check-in-becomes-visible-rather-than-silent) | — |
| `D-GM10` | The outside authority is **no longer needed** exactly when the monitor's store, root and drain are disjoint from the watched system's. Moved one level out, not removed | 2026-11-14 | [§7.5](#75-when-the-outside-authority-is-no-longer-needed) | — |
| `D-GM11` | A not-due item is a **wait, not a stop**. The loop waits by default, so the rule binds only the mode that exits when the queue is drained | 2026-11-14 | [§8](#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained) | — |
| `D-GM12` | `max_actions` bounds one check-in to 3 filings plus one bound-hit item; a silently truncating bound is refused | 2026-11-14 | [§5.2](#52-parameters) | — |
| `D-GM13` | The two slices land in the order clock, then check-in | 2026-11-14 | [§10](#10-the-downstream-slices) | — |

## 12. Open questions

Both questions are blocked: the owner iceboxed this design on 2026-11-17, and neither is asked until it is picked up.

1. 🔒 **OQ-GM1: What should the first scheduled check-in watch, and how often?**

   You iceboxed the goal monitor until there is a use for it. A use is a property you check today by looking: you open the queue screen, or a tracked fork, and see whether it has drifted. The monitor would take that look on a schedule and file an item when the property fails. Which property comes first is the part that is undecided.

   - **A — A tracked fork that has fallen behind its upstream's releases**, checked weekly.
   - **B — The queue screen agreeing with the store**, checked daily. Today you find out by reading the queue. Whether the two still disagree is not established.
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
