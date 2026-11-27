---
title: "Scheduled items: an item not due yet is simply not ready"
status: accepted
stage: DECIDED
next: "Plan and build V1 (one-shot due_at) and V2 (recur_interval_ms and in-place re-arm) from [§9](#9-staging-v1-v2-and-the-criterion) and [§14](#14-where-the-code-lives-by-symbol). The owner may overturn any of the 13 designer's decisions in [§11](#11-decision-ledger) and [§12](#12-open-questions)."
tags: [tillr, drain, scheduling, cron, design]
summary: "Accepted, unbuilt substrate for scheduled and recurring queue items: due_at and recur_interval_ms are nullable integer columns on items, due-ness is a derived planner check (not-due) that keeps rank and needs no daemon, and recurrence re-arms the same item in place at completion so missed periods collapse to one run."
---

# Scheduled items: an item not due yet is simply not ready

**Status:** 2026-11-27. Accepted, nothing built. tillr has no `due_at` or `recur_interval_ms`
column on `items`, no `not-due` planner reason, and no `schedule.rearm` event
(`rg -n 'due_at|recur_interval_ms|not-due|schedule\.rearm' tools -g '*.go'` returns only the
unrelated `log-checkpoint.not-due` store reason).
[The roadmap](../tillr/roadmap.md) routes this design as the settled, unbuilt substrate of
[the goal monitor](tillr-goal-monitor.md) (parked in the Icebox until [`OQ-GM1`](tillr-goal-monitor.md#OQ-GM1) names a first check-in).

> **In short.** A due time and a recurrence are two nullable millisecond columns on `items` (`due_at` and `recur_interval_ms`). An `in-queue` item whose `due_at` is still ahead of the planner's clock is held as `not-due` — waiting on the clock rather than blocked on a person — keeps its rank, wakes the idle loop when `due_at` arrives with no daemon, and re-arms in place (`landed → in-queue`) at completion so three missed weeks produce one run rather than three.

**Why it matters.** Periodic convergence work and [goal check-ins](tillr-goal-monitor.md) need a way to say "do not start before T, and never forget me if the loop was off" without running a second scheduler daemon, inventing a `scheduled` state that rots without a writer, or flooding the queue after downtime.

**The shape.** Two additive columns on `items` ([§2](#2-the-item-fields)), one `not-due` check at the front of planner admission plus `idlePlanState.nextExpiry` wake clamping ([§3](#3-readiness-the-not-due-gate), [§6](#6-no-daemon)), and in-place re-arm inside `item.SetStateWithReceipt` (and verified `no_change` completion) scoping retry history to the new cycle ([§5.3](#53-re-arming-in-place-and-the-transition-the-machine-is-missing)).

**Start at:** [§2](#2-the-item-fields) for the schema and CLI faces, [§5.3](#53-re-arming-in-place-and-the-transition-the-machine-is-missing) for in-place re-arm, and [§14](#14-where-the-code-lives-by-symbol) for the grounded symbol map.

**Needs your ruling:** None: no question is open in this document. It holds 13 designer's decisions — [`D-1`](#D-1) to [`D-12`](#D-12) in [§11](#11-decision-ledger) and [`OQ-ST2`](#12-open-questions) — none of which is an owner ruling, and the owner may overturn any of them ([decision-provenance review](../tillr/decision-provenance-review.md#6-how-this-stops-happening)).

**Reads with:** [the goal monitor](tillr-goal-monitor.md) (the downstream consumer in the Icebox, including [`D-GM11`](tillr-goal-monitor.md#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained) on `--exit-when-drained`), [scheduling and notifications](tillr-scheduling-and-notifications.md) (`DefaultFallbackPollInterval` and pure scheduling passes), [the judge and lifecycle design](tillr-judge-and-lifecycle.md) (`D-JL10` on uncounted worker endings), and [the queue mirrors the designs](tillr-queue-mirrors-designs.md) (its [Section 8](tillr-queue-mirrors-designs.md#8-retiring-the-hidden-comments-and-what-is-deleted) retires in-document item blocks, which is why a schedule is written on the store faces only).

**Terms.** *Due time* is the instant before which an item must not be dispatched. *Recurrence* is a
fixed period after which a completed item becomes due again. *Drain* is the foreground loop process
(`tillr-drain`) that dispatches lanes; it is the only execution loop that ever reads a due time.
*Admission* is the per-item decision the drain's planner makes each pass (`decide` in
[`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go)): dispatch, or a named
reason not to. *Rank* is the item's lexicographic priority key (`items.rank`), the queue's ordering
term.

## 1. What this settles

Two rules frame everything below.

> **The queue may remember a time; it may not promise a time.** A scheduled item says "do not
> admit me before T". It never says "run me at T".

The test for whether work belongs here: *if lateness by days destroys the value, this is the wrong
primitive.* A certificate renewal with a hard external deadline needs an external timer on a
machine that is actually up, whose whole job is to file an ordinary, due-now item or page a
person. "Eventually, next time we are working, and never let me forget" is exactly, and only, what
this design provides.

The design settles four things:

1. **The stored shape** — two nullable columns on the item ([§2](#2-the-item-fields)).
2. **Readiness** — due-ness is a derived admission check, not a stored state
   ([§3](#3-readiness-the-not-due-gate)), and a not-due item is waiting rather than blocked
   ([§3.4](#34-what-a-not-due-item-does-to-ordering-and-to-counts)).
3. **Recurrence** — the same item re-arms in place at completion, which needs a way for a landed
   item to become dispatchable again
   ([§5.3](#53-re-arming-in-place-and-the-transition-the-machine-is-missing)).
4. **No daemon** — the drain's poll is the only clock ([§6](#6-no-daemon)).

No part of this design depends on a time limit that stops a run: the owner ruled against clocks
that stop work (2026-11-20).

## 2. The item fields

### 2.1 The two columns

| Field | Type | Meaning | Null means |
| :--- | :--- | :--- | :--- |
| `items.due_at` | `INTEGER NULL`, unix milliseconds | The next instant this item becomes admissible | No schedule: the item is due now |
| `items.recur_interval_ms` | `INTEGER NULL` | The period after which a completed item becomes due again | One-shot: schedule once, complete, stay completed |

Both are nullable and additive, so the migration is two registered columns and a schema version
bump ([§8](#8-migration-and-compatibility)).

`recur_interval_ms` is meaningless without `due_at` — an interval with no first due time has no
anchor — and a zero or negative interval would re-arm into the past on every pass. So the item
writer refuses both `due_at IS NULL AND recur_interval_ms IS NOT NULL` (`recur-without-due`) and
`recur_interval_ms <= 0` when non-null (`invalid-recur-interval`). Both are write-time checks in
[`tools/tillr-item/item/`](../../tools/tillr-item/item/), not table constraints: a `CHECK` would
need a table rebuild, and every write enters through the item package.

### 2.2 What an item without them means

Unscheduled behavior, unchanged. `due_at IS NULL` is the due predicate's first disjunct, so
every existing item is due at every instant, and `recur_interval_ms IS NULL` means the completion
write does nothing new. This is the whole compatibility story: the feature is inert for every
item that does not opt in.

### 2.3 The CLI surface

A schedule is written on the store faces only (`D-12`, the designer's decision). The hidden
`<!-- tillr: item ... -->` comment that `tillr item sync` reads today is being retired by
[the queue mirrors the designs](tillr-queue-mirrors-designs.md#8-retiring-the-hidden-comments-and-what-is-deleted)
(not built yet), so this design adds no schedule attributes to a form that is on its way out. The
schedule lives on the store faces in [`tools/tillr-item/`](../../tools/tillr-item/):

| Surface | Form | Notes |
| :--- | :--- | :--- |
| `tillr item request` | `--due <when>`, `--every <duration>` | Sets `due_at` (RFC 3339 / ISO-8601 instant or relative `+7d` against `--now` / wall clock) and `recur_interval_ms` (`7d`, `36h`) at filing |
| `tillr item schedule` | `--store <path> --item <id> [--due <when>\|""] [--every <duration>\|""] [--by owner\|whale]` | Targeted mutator (beside `rank` and `dep`) to set, push out, or clear (`""` → `NULL`) `due_at` and `recur_interval_ms` on an existing item without round-tripping its full record |
| `tillr item apply` | `due_at`, `recur_interval_ms` in the `item.record` JSON payload | Nullable integer milliseconds (`snake_case`, matching every other field of `itemPayload` in [`tools/tillr-item/item/apply.go`](../../tools/tillr-item/item/apply.go)) |
| `tillr item show`, `tillr item list` | `due_at`, `recur_interval_ms` in the JSON payload | Omitted or null when unset; rendered as a short schedule hint in `tillr item list --table` |

The CLI faces parse human durations into milliseconds at write time; the store and `item.record`
only ever carry integer milliseconds, keeping the store free of a duration parser. None of these
flags, fields, or the `schedule` face exists yet.

**Who may write them (`D-10`).** Only `owner` and `whale` (the coordinating agent) — the two
deciders `decisions.decided_by` admits
([`tools/tillr-item/item/schema.go`](../../tools/tillr-item/item/schema.go)). A due time is a
promise about when work happens, and a lane worker is the one party that must not be able to move
it. Adding, changing, or clearing `due_at` or `recur_interval_ms` via `request`, `schedule`, or
`apply` requires `--by owner` or `--by whale` (refusing other actors with `schedule-authority`),
while an `apply` round-trip that leaves both stored values unchanged needs no extra authority.

## 3. Readiness: the not-due gate

### 3.1 The rule

> An item in the ready state (`in-queue`) is due when `due_at IS NULL OR now >= due_at`. An `in-queue` item that is not due is held with `ReasonNotDue` (`"not-due"`).

That is the whole readiness rule. It is evaluated in the planner for items with no in-flight work
(`work == nil`), and for an `in-queue` item it runs ahead of every dependency, question, retry,
and resource check.

### 3.2 Where it sits in the planner

`itemSelect` in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) loads
items in every state (`landed`, `closed`, `resolved-elsewhere`, `not-started`, `blocked`, and
`in-queue`). Scoping the `not-due` check to `item.State == p.rules().Ready` (`in-queue`) with no
in-flight `pendingWork` (`work == nil`), and running it **before** workflow selection, Vantage
question-index reads (`p.questionState`), `blocked_by`/`after` edges, obstacle/attempt holds, and
backend/lane/agent limits, gives every state the right answer:

- **Finished (`landed`, `closed`, `resolved-elsewhere`), `not-started`, and explicitly `blocked` items** keep their stored state's disposition (`ReasonNotReady`, `ReasonBlocked`, `ReasonIcebox`). Closing a scheduled item (`tillr item close`) immediately excludes it rather than leaving it `not-due` until its future timestamp passes, and pausing an item in `blocked` reports the pause rather than the clock.
- **In-flight runs (`work != nil`)** finish normally ([§4](#4-priority-due-ness-never-promotes), no preemption).
- **Not-due `in-queue` items** return `ActionHold`, `ReasonNotDue` (`Decision.DueAt` set to the stored `due_at` instant) before touching Vantage's index, workflow files, dependencies, or lanes.

Two consequences of putting `not-due` ahead of dependencies, questions, and holds on `in-queue`
items, both wanted: a not-due item never reports a hold or dependency it also has because the clock
is what stops it now, and a not-due item stays out of every count that means *waiting on something
a human or another item must move*
([§3.4](#34-what-a-not-due-item-does-to-ordering-and-to-counts)).

**The check is in the planner, not in `itemSelect`'s `WHERE` clause.** Filtering in SQL
would be cheaper and would be wrong: an item excluded from the candidate set is invisible to
`tillr drain why` and to the view, so a scheduled item would vanish from the queue until it came
due — the opposite of "never forgotten". `itemSelect` and `scanItemRow` gain `due_at` (and
`recur_interval_ms` for display) in their column list and nothing else; their filter and ordering
are untouched.

### 3.3 Why a readiness rule and not a status

A `scheduled` state would be a stored fact that time invalidates with no writer — the row would say
`scheduled` forever until some process rewrote it, which is the daemon this design exists to avoid.
The item's declared transitions
([`tools/tillr/definition/landing.json`](../../tools/tillr/definition/landing.json)) would need a
`scheduled` state with edges to and from every other state, and the definition would then claim a
lifecycle that no writer drives.

The planner already returns reasons that are neither a state nor a transition: a hold and a
dependency wait are both derived at admission. Due-ness is one more of that kind.

### 3.4 What a not-due item does to ordering and to counts

**Ordering: nothing.** The not-due check is not an ordering term. A not-due item keeps its rank
position in the candidate list and is refused when the planner reaches it; the planner continues to
the next candidate, so a not-due item at rank `a` does not delay rank `b`. It consumes no lane and
blocks nothing.

**Counts: it is a waiting state, not a blocked state.** A blocked or held report is a claim that
something must move and that a human has work to do. For a not-due item both are false. So:

| Surface | A not-due item |
| :--- | :--- |
| Store writes on a pass (`holdWriter` in [`pass.go`](../../tools/tillr-drain/drain/pass.go)) | **None.** `ReasonNotDue` is a report-only hold (not one `persistentHold` names), so a pass writes neither a `dispatches` row nor an `events` row |
| Shared hold presentation (`Rules["not-due"]` in [`holdrules.go`](../../tools/tillr-drain/holdrules/holdrules.go)) | `Class: "waits on the loop"`, `Where: "queue"`, `Mover: "the due time to arrive"` (covers `TestEveryReasonConstantHasAHoldRule` and `TestWaitingEveryDrainReasonHasARule`) |
| `WhySummary` ([`why.go`](../../tools/tillr-drain/drain/why.go)) | Increments a dedicated `NotDue` (`json:"not_due"`) field only — **not** `OwnerWaiting`, `OperatorNext`, `DesignNext`, `DrainNext`, `CouldRun`, or `Held` |
| Loop idle summary (`heldDetail` in [`loop.go`](../../tools/tillr-drain/drain/loop.go)) | Formatted in its own `not-due` clause rather than the `held` or lane-`deferred` counts |
| The pass's per-item decision | **Returned** (`ActionHold`, `ReasonNotDue`, `DueAt`), so the item is decided once per pass and `--exit-when-drained` / `idlePlanState` read `DueAt` directly without parsing prose |

The summary line gains one field (`not-due N`). That is the whole cost of the decision, and it is
the honest one: an operator reading `owner-waiting`, `operator-next`, or `held` must not find a
scheduled item in any of them.

**`tillr drain why` and `tillr-view` get it from the same planner.** `Why` runs `planWithDetails`,
so a not-due item reports `Reason: "not-due"`, `Class: "waits on the loop"`, and a short `Why`
naming the due instant (and interval when recurring, e.g. `not-due: due 2026-11-09T00:00:00Z · every 7d`).

## 4. Priority: due-ness never promotes

> A due item competes for a lane exactly as any other ready item does, in rank order. Due-ness
> changes *whether* an item competes, never *how* it ranks.

Three consequences, each a refusal:

- **No overdue boost.** An item three weeks overdue ranks where its rank puts it. A boost would be a
  second, invisible priority system contradicting the one the owner can see and edit, and it could
  not be configured away per item.
- **No preemption.** A due item never interrupts a running lane. Coming due mid-drain is admission
  at the next poll, and a due item that loses the competition waits, still due, indefinitely.
- **No promotion of an unranked item.** An unranked recurring item competes as any unranked item
  does, so a recurring item is filed with an explicit rank.

Reversal condition: observed starvation of a due recurring item behind a continuous stream of
higher-ranked work. Even then the first fix is to re-rank the item, not to mechanize the exception.

## 5. Recurrence and the missed-period rule

### 5.1 The arithmetic

On the completion of an item whose `recur_interval_ms` is set:

```text
next_due_at = completed_at + recur_interval_ms
```

`completed_at` is the instant of the completion write, not the due time and not the run's start.
The anchor is the completion, so a "weekly" task runs every seven days from whenever it last
completed.

**Accepted property — cadence drift.** A completion-anchored weekly task's weekday wanders. What
matters is minimum spacing and bounded staleness, both of which hold. The alternative — slot
arithmetic, the smallest `previous_due_at + k·interval` still after completion — is deferred, not
refused ([§9](#9-staging-v1-v2-and-the-criterion)).

### 5.2 Why three missed weeks are one run

The no-queue property is a property of the **storage**, not of the arithmetic. The schedule has
exactly one slot of memory: the single `due_at` value. There is nowhere for three missed weeks to
accumulate. An item whose due time passed three weeks ago is simply overdue, once; the drain admits
it once; and when it completes, the next due time is computed from *that* completion, so the missed
slots were never represented and cannot be replayed.

Slot arithmetic would also not queue — it computes the smallest future slot, so it too collapses
missed periods. What distinguishes the two is cadence drift, not queueing. The failure mode both
avoid is a **catch-up storm**: a schedule that remembers every missed period and fires them back to
back, which for state-convergence work is pure waste — checking a property three times in a row
converges to the same place as checking it once.

Catch-up is therefore a named per-item opt-in (`recur_catch_up`, default off) that is **not built**.
It is only correct when each missed instance has distinct value, and work of that shape is a strong
hint it does not belong in this queue.

### 5.3 Re-arming in place, and the transition the machine is missing

The same item re-arms in place. At the completion write for an item with `recur_interval_ms` set,
in one SQLite write transaction: record the cycle's completion as for any item (the run, verdict,
and landing or finding records stand — that history *is* the audit trail), clear criterion states
for the next cycle, move the item back to `in-queue` with `due_at = completed_at + recur_interval_ms`,
and append a `schedule.rearm` event carrying `{item_id, previous_due_at, next_due_at, recur_interval_ms, completed_at}`.
The item never rests in `landed`; its runs, verdicts, and events accumulate under one id.

**The transition (`landed → in-queue`).** The stored item states in
[`tools/tillr-item/item/schema.go`](../../tools/tillr-item/item/schema.go) are `not-started`,
`blocked`, `in-queue`, `landed`, `resolved-elsewhere`, and `closed`.
[`tools/tillr/definition/landing.json`](../../tools/tillr/definition/landing.json) already declares
`{"from": "landed", "to": "in-queue"}` (and any non-landing charter definition that uses recurrence
declares `<finished> → <ready>`). Re-arm uses `landed → in-queue` — never `landed → not-started`,
because `not-started` is held permanently by `ReasonNotReady` with no automatic writer back to
`in-queue`.

**One store hook (`item.SetStateWithReceipt`), plus verified `no_change`.** The drain's writes of
`landed` all reach `item.SetStateWithReceipt` in
[`tools/tillr-item/item/state.go`](../../tools/tillr-item/item/state.go): `completeItem` in
[`workflow_exit.go`](../../tools/tillr-drain/drain/workflow_exit.go) and
`completeGraphWithoutCommit` in
[`workflow_exit_record.go`](../../tools/tillr-drain/drain/workflow_exit_record.go) through the
`tillr-item state` step (`ProcessSteps.ItemState`), the judged and recovery paths through
`item.SetState`, which is a thin wrapper over it, and an operator's `tillr item state --state landed`
through the same face. When `to` is `landed` (the
definition's completed state) and `recur_interval_ms IS NOT NULL`, `SetStateWithReceipt` validates
terminal evidence and writes the `in-queue → landed` event first, then inside the **same write
transaction** calls `WithdrawVerdicts` with the reason `schedule-rearm`
([`tools/tillr-item/item/withdraw.go`](../../tools/tillr-item/item/withdraw.go)) to clear
`item_criteria.state`, updates `items` to `state = "in-queue"` and `due_at = next_due_at`, writes
`item.state-changed` (`landed → in-queue`, `reason = "schedule-rearm"`), and appends
`schedule.rearm`. Likewise, in `completeNoChangeExit`
([`tools/tillr-drain/drain/workflow_exit_record.go`](../../tools/tillr-drain/drain/workflow_exit_record.go)),
a verified `RESULT: no_change` completion on an item with `recur_interval_ms IS NOT NULL` re-arms
the item instead of calling `item.Close`, so a quiet cycle on a landing workflow advances the
schedule rather than terminating recurrence.

**Cycle-scoped planner history and terminal evidence.** `currentTerminalEvidence` in
[`tools/tillr-item/item/terminal_evidence.go`](../../tools/tillr-item/item/terminal_evidence.go)
already scopes terminal evidence to events after the latest `landed → in-queue` transition, so
cycle 1's landing cannot satisfy cycle 2. On the planner side, `mayBeARetry`, `returnedEnding`, and
prior-run failure/interruption classification in
[`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) scope to runs started
**after** the item's latest `schedule.rearm` event so cycle 2 dispatches fresh once `due_at`
arrives instead of holding as an unauthorized retry of cycle 1, while `nextAttempt`
(`readAttemptInputs`) keeps incrementing monotonically across cycles so `run-<item>-a2` never
collides with `run-<item>-a1` in `runs` or `node_runs`.

**A recurring item's document block is never retired.** A schedule is written on the store faces
([§2.3](#23-the-cli-surface)), but while item blocks still exist an interval can be set on an item
that a document declares. The drain retires a declared block before gating (`retireDocumentBlock`
in [`tools/tillr-drain/drain/retire.go`](../../tools/tillr-drain/drain/retire.go)); it skips that
step for an item whose `recur_interval_ms` is set, or the declaration would disappear after the
first cycle.

**The no-op trap.** A recurring item whose correct outcome is "nothing to do" produces no commit.
Tying re-arm to the judged completion (`requires_candidate: "no_commit"` or verified `no_change`)
closes that, *provided the item's criterion is written so a no-op is judgeable as done*. That is a
briefing obligation on each recurring item, not a mechanism
([§7](#7-the-first-user-a-recurring-check-in)).

**Stopping recurrence** is already expressible: explicit `tillr item close` moves `state` to
`closed` and excludes the item from dispatch; pausing composes with `blocked` or with pushing
`due_at` out via `tillr item schedule`; clearing `--every ""` makes the current cycle the last one.

### 5.4 Failure does not advance the schedule

A failed or interrupted lane leaves the item uncompleted, so the item's `due_at` does not advance
and the item remains due under the drain's ordinary failure handling; this design adds no
schedule-specific attempt cap or backoff ([OQ-ST2](#12-open-questions)). That is the right default —
"not forgotten" includes not silently skipping a period because the attempt broke.

The ordinary failure handling is `D-JL10` (2026-11-17) in
[the judge and lifecycle design](tillr-judge-and-lifecycle.md#decision-ledger): worker endings are
never counted, nothing re-runs because an input hash changed, a failed or refused worker run returns
to its own session with its evidence, and only an identical repeat escalates to the design seat. The
explicit operator return is `tillr drain retry`.

## 6. No daemon

### 6.1 The invariant

> Nothing polls a due time. Nothing is always-on. A drain run is the only clock, and a due time in
> the past is simply ready.

The queue is a **pull** system: nothing needs to push at the due moment, because the moment is only
ever consulted by the thing that was going to do the work anyway. This design adds no systemd unit,
timer, cron entry or launchd job. A drain exists only as a foreground process a human or agent
starts.

The invariant that makes it work, recorded so a future optimization cannot quietly break it:

> **The drain must retain a periodic re-evaluation tick, or the wait between passes must take the
> earliest `due_at` as a wake deadline.**

The drain re-runs candidate selection and admission on every pass rather than snapshotting
candidates at startup, so a due check flips on its own as time passes. Two places in the idle loop
keep that true now that `idlePlanState`
([`tools/tillr-drain/drain/idle.go`](../../tools/tillr-drain/drain/idle.go)) caches unchanged idle
passes:

1. **Idle cache expiry (`captureIdleState` in [`idle.go`](../../tools/tillr-drain/drain/idle.go)):**
   folds the earliest future `Decision.DueAt` across `ReasonNotDue` holds into
   `idlePlanState.nextExpiry` (beside backend quota windows and rolling-gate deadlines) so
   `idleUnchanged` invalidates once `now >= nextExpiry` even when no store event arrived.
2. **Trigger timer and `--exit-when-drained` ([`trigger.go`](../../tools/tillr-drain/drain/trigger.go), [`loop.go`](../../tools/tillr-drain/drain/loop.go)):**
   `waitForTrigger` already waits no longer than `LoopConfig.StorePollInterval` (which defaults to
   `DefaultFallbackPollInterval`, 15 seconds) or the earliest lane lease; it also clamps the wait to
   `nextExpiry.Sub(at)`, so a longer configured poll interval cannot sleep past a due time, and under
   `--exit-when-drained` ([goal monitor §8](tillr-goal-monitor.md#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained),
   `D-GM11`) a `ReasonNotDue` hold waits rather than returning `StopQueueDrained`.

**Clock behavior.** The comparison uses wall-clock unix milliseconds. Machine sleep is harmless: the
first pass after wake sees the item due. A backwards clock step delays a due item; a forwards step
fires it early. Both are accepted — this is a not-forgotten reminder, not a timed actuator.

### 6.2 A month with no drain

The item sits overdue for a month and nothing happens, which is the feature working. Nothing in
tillr expires, TTLs or auto-closes old items, so a due item cannot rot away unnoticed. The first
pass of the next drain admits it, and the view shows it ready meanwhile. "A best-effort cron that is
never forgotten" reduces to: a stored timestamp with no expiry, read by a poll loop that already
exists.

## 7. The first user: a recurring check-in

The consumer this design is written for is [the goal monitor](tillr-goal-monitor.md): a recurring
item whose criteria state a property the system should hold, and whose correct outcome on most
cycles is a no-op the judge can still call done.

| Field | Value |
| :--- | :--- |
| `rank` | An explicit rank, filed with the item; not a default ([§4](#4-priority-due-ness-never-promotes)) |
| `due_at` | The filing instant plus the interval |
| `recur_interval_ms` | The interval |
| criteria | Written so that a correct no-op is judgeable as done |

The last row is the one that keeps the schedule alive. A recurring item whose criteria can only be
met by a commit would, on a cycle with nothing to do, either spin or never advance the schedule
([§5.3](#53-re-arming-in-place-and-the-transition-the-machine-is-missing)). So a no-op cycle
records the fact it checked — the evidence naming what was compared — and that record is what the
judge reads.

Nothing about the work itself changes between cycles: the brief is read fresh at each dispatch, so
editing it updates every future cycle.

## 8. Migration and compatibility

| Question | Answer |
| :--- | :--- |
| What happens to existing items? | Nothing. `due_at IS NULL` and `recur_interval_ms IS NULL` are the defaults, and both mean an unscheduled item |
| Does the on-disk format need a version bump? | **Yes**: `store.SchemaVersion` in [`tools/tillr/store/schema.go`](../../tools/tillr/store/schema.go) rises by one, so a binary that reads `due_at` never opens a store without it |
| What is the migration? | Two additive columns on `items`, registered with `store.RegisterColumn("item", "items", ...)` beside the existing ones in [`tools/tillr-item/item/schema.go`](../../tools/tillr-item/item/schema.go): no row rewritten, no constraint changed, no table rebuild |
| Does an older build refuse the newer store? | Yes: a build refuses to open a store whose schema version is newer than its own. That is the honest answer: the older build would not know what a due time means |
| Does the events table need a change? | No. An event kind is free text, so `schedule.rearm` needs no schema change |

## 9. Staging: V1, V2, and the criterion

| Stage | Contents | What it proves |
| :--- | :--- | :--- |
| **V1 — one-shot due** | `due_at` and `recur_interval_ms` columns + migration ([§8](#8-migration-and-compatibility)), `ReasonNotDue` in planner/holdrules/`Why`/`tillr-view`, `idlePlanState.nextExpiry` + `--exit-when-drained` wait, and `request`/`schedule`/`apply`/`show`/`list` support for `due_at` | Proves the check, the idle wake, `--exit-when-drained`, the view, and the migration with zero completion-path risk. Useful alone: "don't start this until the 3rd" |
| **V2 — recurrence** | `--every` / `recur_interval_ms` write validation, in-place `landed → in-queue` re-arm in `item.SetStateWithReceipt` and verified `completeNoChangeExit`, `schedule.rearm` event, and cycle-scoped planner retry history | The only stage touching completion and retry-history semantics; filing a live recurring check-in stays gated on [`OQ-GM1`](tillr-goal-monitor.md#OQ-GM1) in the parked goal monitor |
| Deferred, with a named trigger | Catch-up mode, slot-anchored cadence, a skipped-period count in the view | Each is a recorded decision, not a gap |
| Refused | Cron parser, timezone support, a daemon, a stored `scheduled` state, an overdue boost, preemption, expiry of overdue items, notifications on due transitions | Each smuggles in a promise the queue cannot keep |

**The criterion this design is held to**, and where each clause is met:

| Criterion clause | V1 | V2 |
| :--- | :--- | :--- |
| An item can carry a due time | yes | yes |
| An item can carry a recurrence | no | yes |
| It is not ready until due | yes | yes |
| The next drain run picks it up at its configured priority | yes | yes |
| Three missed weeks produce one run rather than three | no — there is no recurrence to miss | yes |
| No daemon or always-on machine is required | yes | yes |

So the criterion is **not met at V1** and **met at V2**. V1 is a real, shippable increment that
satisfies four of six clauses. Nothing in V1's schema or check changes for V2, which is the sense in
which the design stays cheap: the risky part (re-arm) is additive on top of the safe part (the
check).

## 10. Verification

Each clause of the criterion, and the check that would fail if it were broken. The item-column and
migration tests belong in [`tools/tillr-item/`](../../tools/tillr-item/) and
[`tools/tillr/store/`](../../tools/tillr/store/); the admission, priority and recurrence tests
belong in [`tools/tillr-drain/drain/`](../../tools/tillr-drain/drain/).

| Clause | Assertion shape |
| :--- | :--- |
| Carries a due time | Writing a due instant then showing the item reports the same instant; writing an empty value clears it to null |
| Carries a recurrence | `every 7d` stores `604800000`; an interval without a due time is refused |
| Not ready until due | An item with `due_at` one hour ahead is not dispatched, with reason `not-due`; the same item with `due_at` one hour behind is dispatched. **No sleep and no elapsed-time assertion**: the due time is written relative to the clock the planner is given, so the case cannot fail under load |
| Picked up at its configured priority | Two due items, ranks `a` and `b`, one free lane: the lane starts on `a`. Then a not-due item at rank `a` and a due item at rank `b`: the lane starts on `b`, proving a not-due item neither consumes a lane nor blocks lower-ranked work |
| Three missed weeks are one run | An item with `due_at` 21 days behind and a seven-day interval, drained once, has exactly one run row. After the completion, `due_at` equals the completion instant plus seven days — not 14 days behind, which is what a queueing schedule would leave |
| No daemon | The re-arm is observed by driving the completion write and then reading the row: no timer, no goroutine, no second process. The invariant itself is a review rule, not a test — a test cannot assert the absence of a future daemon |
| The re-arm transition | The definition's transition tests cover the transition re-arm uses |
| The migration | A store at the previous schema version opens, ends at the current one, and its existing items read as due with no interval |

Two of these are worth stating as rules rather than as cases. **No test asserts elapsed time.**
Due-ness is decided by a comparison and never by a wait; a test that slept for a due time to pass
would be a defect, not a flake. And **the not-due refusal writes no blocked event**: a case asserts
the events table holds no blocked or hold row for a not-due item, because that row is the claim
[§3.4](#34-what-a-not-due-item-does-to-ordering-and-to-counts) refuses.

## 11. Decision ledger

Every row below is a designer's decision; none is a quoted ruling from the owner, and the owner may
overturn any of them ([decision-provenance review](../tillr/decision-provenance-review.md#6-how-this-stops-happening)).
None is built.

| ID | Decision | Date | Reversal condition |
| :--- | :--- | :--- | :--- |
| <a id="D-1"></a>`D-1` | **Designer's decision; the owner may overturn this.** Due-ness is a derived admission check plus `ReasonNotDue` (`"not-due"`), on a stored nullable `items.due_at`. No new item state, no new edge kind. | 2026-11-02, designer | The rule "stored facts stay stored, readiness stays derived" is itself reversed |
| <a id="D-2"></a>`D-2` | **Designer's decision; the owner may overturn this.** `ReasonNotDue` runs on `in-queue` items with no in-flight work ahead of workflow, question, dependency, retry, and lane checks, in the planner rather than in `itemSelect`'s `WHERE` clause. | 2026-11-02, designer (grounded 2026-11-27) | A not-due item's other holds turn out to be what an operator needs to see first |
| <a id="D-3"></a>`D-3` | **Designer's decision; the owner may overturn this.** A not-due item is a clock-waiting state (`Class: "waits on the loop"` in `holdrules.go`): no `dispatches` or `events` write on a pass, and counted in `WhySummary.NotDue` rather than `Held`, `OwnerWaiting`, `OperatorNext`, `DesignNext`, or `DrainNext`. | 2026-11-02, designer (grounded 2026-11-27) | The summary's field count becomes a problem |
| <a id="D-4"></a>`D-4` | **Designer's decision; the owner may overturn this.** No scheduler or daemon; `captureIdleState` folds the earliest future `due_at` into `idlePlanState.nextExpiry`, `waitForTrigger` clamps its timer to `nextExpiry`, and `--exit-when-drained` waits while a `ReasonNotDue` hold remains (`D-GM11`). | 2026-11-02, designer (grounded 2026-11-27) | External hard-deadline work is brought in (which belongs in a host timer outside tillr) |
| <a id="D-5"></a>`D-5` | **Designer's decision; the owner may overturn this.** Recurrence is a stored positive `items.recur_interval_ms`; `next_due_at = completed_at + recur_interval_ms`. No queueing, cadence drift accepted, no cron parser. | 2026-11-02, designer | A consumer needs fixed wall-clock slots → slot-anchored arithmetic, still no cron parser without a second consumer |
| <a id="D-6"></a>`D-6` | **Designer's decision; the owner may overturn this.** Catch-up defaults off and is not built; named as a per-item opt-in (`recur_catch_up`) only if ever needed. | 2026-11-02, designer | A concrete item whose missed instances each have distinct value |
| <a id="D-7"></a>`D-7` | **Designer's decision; the owner may overturn this.** Recurring items re-arm **in place** inside `item.SetStateWithReceipt` (and verified `completeNoChangeExit`) using the existing `landed → in-queue` transition, withdrawing criterion verdicts (`WithdrawVerdicts`), appending `schedule.rearm`, and scoping planner retry/failure history to after `schedule.rearm` while keeping attempt numbers monotonic. | 2026-11-02, designer (grounded 2026-11-27) | A future workflow artifact cannot cleanly separate cycles under monotonic attempt numbers |
| <a id="D-8"></a>`D-8` | **Designer's decision; the owner may overturn this.** Failure never advances `due_at`; no schedule-specific backoff or cap is added. | 2026-11-02, designer | An observed hot-loop on a failing recurring item → fix drain-wide, not per-schedule |
| <a id="D-9"></a>`D-9` | **Designer's decision; the owner may overturn this.** Priority is the existing `items.rank`. No overdue boost, no preemption of running work. | 2026-11-02, designer | Observed starvation that re-ranking the item does not fix |
| <a id="D-10"></a>`D-10` | **Designer's decision; the owner may overturn this.** Only `owner` and `whale` (`decisions.decided_by` actors) may add, change, or clear `due_at` or `recur_interval_ms` across `request`, `schedule`, and `apply`. | 2026-11-02, designer | A worker seat legitimately needs to schedule its own follow-up work |
| <a id="D-11"></a>`D-11` | **Designer's decision; the owner may overturn this.** Build order is V1 (one-shot `due_at` + schema) → V2 (`recur_interval_ms` + in-place re-arm); live recurring items wait for [`OQ-GM1`](tillr-goal-monitor.md#OQ-GM1). | 2026-11-02, designer | A consumer needs V2 immediately → V1 and V2 may merge |
| <a id="D-12"></a>`D-12` | **Designer's decision; the owner may overturn this.** A schedule is written on the store faces only (`request`, `schedule`, `apply`); a document item block gains no `due` or `every` attribute, and the drain never retires the block of an item whose `recur_interval_ms` is set. | 2026-11-27, designer | The hidden item comments are kept after all → add the two attributes to the block |

## 12. Open Questions

No question is open. [`OQ-ST2`](#12-open-questions) carries no quoted ruling from the owner: it is the designer's decision
on recorded principle at the owner's request
([decision provenance review `OQ-PR22`](../tillr/decision-provenance-review.md#OQ-PR22)), so it stays
here rather than compacting into [§11](#11-decision-ledger) and remains open to the owner's overturn.

1. ✅ **OQ-ST2: How fast does a failing recurring item come back, and is that acceptable?**

   [§5.4](#54-failure-does-not-advance-the-schedule) leaves a failing recurring item due, with no
   schedule-specific attempt cap and no backoff.

   - **A — Leave it.** Fix a hot loop drain-wide, not per-schedule.
   - **B — A per-item attempt cap on recurring items only.** Bounded, but a capped recurring item
     silently stops being recurring.
   - **C — Measure first.**

   _Settled (designer's decision on recorded principle at the owner's request; the owner may overturn it):_ A. A failing recurring item is governed by the drain-wide rule like any other item, with no schedule-specific cap. Under the owner's ruling (2026-11-17, `D-JL10` in [the judge and lifecycle design](tillr-judge-and-lifecycle.md#decision-ledger)), a failed worker run goes back to its own session with its evidence, uncounted, and only an identical repeat goes to the design seat.

## 13. What this does not decide

- **Which live recurring check-in runs first in this repository.** Gated on [`OQ-GM1`](tillr-goal-monitor.md#OQ-GM1) in [the goal monitor](tillr-goal-monitor.md) (parked on the roadmap's Icebox).
- **The `recur_catch_up` column.** Named as a per-item opt-in and deliberately not added to the
  schema, because an unused column acquires stray semantics.
- **Anything about the check's cost.** A due-time comparison is one integer compare on a value
  already read; if that ever matters, the measurement belongs to the planner's own work.

## 14. Where the code lives, by symbol

Nothing below is built. Each row names the exact file and symbol in the current tree where V1 and
V2 attach.

| What this design touches | Where it lives |
| :--- | :--- |
| `due_at` and `recur_interval_ms` columns + registration | `ItemDDL` and `store.RegisterColumn("item", "items", ...)` in [`tools/tillr-item/item/schema.go`](../../tools/tillr-item/item/schema.go) |
| Schema version bump (`6 → 7` or `+1` at build) and additive migration | `store.SchemaVersion`, `store.Open`, and `store.OpenRead` in [`tools/tillr/store/schema.go`](../../tools/tillr/store/schema.go) |
| Item JSON payload, write validation (`recur-without-due`, `invalid-recur-interval`, `schedule-authority`), and read projection | `itemPayload` and `ApplyWithReceipt` in [`tools/tillr-item/item/apply.go`](../../tools/tillr-item/item/apply.go); `Show` / `List` / `FormatListTable` in [`tools/tillr-item/item/show.go`](../../tools/tillr-item/item/show.go) and [`tools/tillr-item/main.go`](../../tools/tillr-item/main.go) |
| `--due` and `--every` CLI flags and `schedule` mutator | `requestFace` in [`tools/tillr-item/request.go`](../../tools/tillr-item/request.go), `FileRequest` in [`tools/tillr-item/item/request.go`](../../tools/tillr-item/item/request.go), and `scheduleFace` / `item.SetSchedule` in [`tools/tillr-item/`](../../tools/tillr-item/) |
| Planner item loading, `ReasonNotDue`, `Decision.DueAt`, and `in-queue` admission check | `itemRow`, `itemSelect`, `scanItemRow`, `planWithDetails`, and `(*planner).decide` in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go); `ReasonNotDue` in [`tools/tillr-drain/internal/drainmodel/constants.go`](../../tools/tillr-drain/internal/drainmodel/constants.go) and [`tools/tillr-drain/drain/model_aliases.go`](../../tools/tillr-drain/drain/model_aliases.go) |
| Shared hold rule, `WhySummary.NotDue`, and Observatory waiting projection | `Rules["not-due"]` in [`tools/tillr-drain/holdrules/holdrules.go`](../../tools/tillr-drain/holdrules/holdrules.go); `Why` and `WhySummary` in [`tools/tillr-drain/drain/why.go`](../../tools/tillr-drain/drain/why.go); [`tools/tillr-view/waiting.go`](../../tools/tillr-view/waiting.go) |
| Idle fingerprint expiry, timer clamping, `heldDetail`, and `--exit-when-drained` wait | `captureIdleState` in [`tools/tillr-drain/drain/idle.go`](../../tools/tillr-drain/drain/idle.go); `waitForTrigger` in [`tools/tillr-drain/drain/trigger.go`](../../tools/tillr-drain/drain/trigger.go); `heldDetail` and the `StopQueueDrained` guard in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) |
| In-place re-arm (`landed → in-queue`, `WithdrawVerdicts`, `schedule.rearm`), verified `no_change` re-arm, and cycle-scoped planner retry history | [`tools/tillr/definition/landing.json`](../../tools/tillr/definition/landing.json) (`landed → in-queue`, already declared); `SetStateWithReceipt` in [`tools/tillr-item/item/state.go`](../../tools/tillr-item/item/state.go); `WithdrawVerdicts` in [`tools/tillr-item/item/withdraw.go`](../../tools/tillr-item/item/withdraw.go); `currentTerminalEvidence` in [`tools/tillr-item/item/terminal_evidence.go`](../../tools/tillr-item/item/terminal_evidence.go); `completeNoChangeExit` in [`tools/tillr-drain/drain/workflow_exit_record.go`](../../tools/tillr-drain/drain/workflow_exit_record.go); `mayBeARetry`, `returnedEnding`, and `readAttemptInputs` in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go); `event.Register("schedule.rearm", ...)` |
| The one clock a due time is compared against | The loop's injected clock (`LoopConfig.Now`) in [`tools/tillr-drain/drain/`](../../tools/tillr-drain/drain/) |

## 15. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| A stored `scheduled` state | Refused. A stored fact that time invalidates with no writer is the daemon this design exists to avoid |
| A blocker edge to a fake clock item | Refused. Due-ness is an item-to-clock fact; an edge needs a writer that runs on time, and cycle checks and edge rendering would need exemptions |
| Filter not-due items out in the planner's item query | Refused. The item would vanish from `tillr drain why` and the view until it came due — the opposite of never forgotten |
| A cron expression instead of an interval | Refused. A parser, timezone and daylight-saving semantics, and a next-fire computation far beyond "add the interval", for a wall-clock anchoring no current user needs |
| A successor item per cycle | Refused. No succession mechanism exists to reuse, and one recurring obligation would fragment across an ever-growing chain of ids |
| Catch-up by default | Refused. Recurring queue work is state convergence, not per-slot obligation; three check-ins converge where one does |
| An overdue priority boost | Refused. A second, invisible priority system contradicting the one the owner can see and edit |
| A systemd timer that files a due item | Refused for this feature. It is the right answer for work whose lateness destroys its value, and that work does not belong in this queue |
