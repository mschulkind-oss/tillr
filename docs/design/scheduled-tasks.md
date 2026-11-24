---
title: "Scheduled items: an item not due yet is simply not ready"
status: in-review
stage: DESIGN
next: "Ground the build points in [§14](#14-where-the-code-lives-by-symbol) against tools/tillr, tools/tillr-item, tools/tillr-drain and tools/tillr-view, then build V1 and V2 ([§9](#9-staging-v1-v2-and-the-criterion)). The owner may review and overturn the designer decisions in [§11](#11-decision-ledger) and [§12](#12-open-questions)."
tags: [tillr, drain, scheduling, cron, design]
summary: "A due time and a recurrence are two nullable integer columns on the item, and due-ness is a derived admission check so a not-yet-due item is simply not ready, keeps its rank, and needs no daemon: the drain's existing poll is the only clock. Recurrence re-arms the same item in place at completion, and the single due_at slot is what makes three missed weeks one run. Nothing is built."
---

# Scheduled items: an item not due yet is simply not ready

**Status:** Nothing built. tillr has no `due_at` or `recur_interval_ms` column on `items`, no
`not-due` planner reason, and no `schedule.rearm` event
(`rg -n 'due_at|recur_interval_ms|not-due|schedule\.rearm' tools -g '*.go'` returns only the
unrelated `log-checkpoint.not-due` store reason).
[The roadmap](../tillr/roadmap.md) lists this design as the unbuilt substrate of
[the goal monitor](tillr-goal-monitor.md). Rows `D-1`–`D-11` in [§11](#11-decision-ledger) and the
settlement of [`OQ-ST2`](#12-open-questions) are designer decisions without a quoted owner ruling
and remain open to the owner's overturn.

**Needs your ruling:** None (no live `💬` question).

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
anchor — so the store refuses the pair `due_at IS NULL AND recur_interval_ms IS NOT NULL`. That is a
write-time check in the item writer, not a table constraint: a `CHECK` would need a table rebuild,
and the pair is only ever written in two places.

### 2.2 What an item without them means

Unscheduled behavior, unchanged. `due_at IS NULL` is the due predicate's first disjunct, so
every existing item is due at every instant, and `recur_interval_ms IS NULL` means the completion
write does nothing new. This is the whole compatibility story: the feature is inert for every
item that does not opt in.

### 2.3 The CLI surface

Two values, `due` and `every`, on the faces that already write an item's definition in
[`tools/tillr-item/`](../../tools/tillr-item/), rather than a new verb:

| Surface | Form | Notes |
| :--- | :--- | :--- |
| A document-declared item block, read by `tillr item sync` | `due`, `every` attributes | So a recurring item is declared reproducibly in a tracked document |
| `tillr item request` and `tillr item apply` | the same two values | `due` takes an ISO-8601 instant or a relative duration (`+7d`); `every` takes a duration (`7d`, `36h`). An empty value clears the column to `NULL` |
| `tillr item show`, `tillr item list` | `dueAt`, `recurIntervalMs` in the JSON body | Rendered as a relative hint in the human view |

The command parses human durations into milliseconds at write time; the store only ever sees an
integer, which keeps the store free of a duration parser. None of these spellings exists yet.

**Who may write them.** Only the owner and the coordinating agent — the two deciders the store's
`decisions.decided_by` column admits. A due time is a promise about when work happens, and the seat
doing the work is the one party that must not be able to move it.

## 3. Readiness: the not-due gate

### 3.1 The rule

> An item is due when `due_at IS NULL OR now() >= due_at`. An item that is not due is not ready.

That is the whole readiness rule. It is evaluated in exactly one place, as a new check in the
planner's `decide`, and it is the first check there.

### 3.2 Where it sits in the planner

`decide` runs its checks in a fixed order and returns the first reason not to dispatch. The not-due
check goes **first**: it is a property of the item alone, read from a column already in hand, and
nothing else about the item is interesting until it is due.

Two consequences of putting it first, both wanted. A not-due item never reports a hold or a
dependency it also has, because those are not what is stopping it — the clock is. And a not-due item
stays out of every "not ready" count that means *waiting on something a human must move*
([§3.4](#34-what-a-not-due-item-does-to-ordering-and-to-counts)).

**The check is in `decide`, not in the query that loads the planner's items.** Filtering in SQL
would be cheaper and would be wrong: an item excluded from the candidate set is invisible to
`tillr drain why` and to the view, so a scheduled item would vanish from the queue until it came
due — the opposite of "never forgotten". The item load gains `due_at` in its column list and
nothing else; its filter and ordering are untouched, and the check reads the value off the loaded
row with no extra query.

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
| A blocked or hold event | **Not written.** Such an event is a claim that something must move; for a not-due item nothing does |
| The operator's list of blocked and held items | **Not listed.** That list is the operator's work queue |
| Dependency, criteria and hold counts | **Not counted** in any of them |
| The count of items waiting on a free lane | **Not counted.** Waiting there means "ready, with no free lane"; a not-due item is not ready |
| A new not-due count | **Counted here**, rendered in the status line as `not due N` |
| The pass's per-item decision | **Recorded**, so the item is decided once per pass |

The status line gains one field. That is the whole cost of the decision, and it is the honest one:
an operator reading a not-ready count split into dependency, criteria and hold must not find a
scheduled item in it.

**`tillr drain why` gets it for free.** The read-only preview runs the same planner, so a not-due
item reports that it is not dispatched, with the reason `not-due` and the due instant, with no new
planner code. The detail stays short: `due 2026-11-09 · every 7d`.

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

The same item re-arms in place. At the completion write for an item with an interval set, in one
store transaction: record the completion as for any item (the run, verdict and landing records
stand — that history *is* the audit trail), append a `schedule.rearm` event carrying the old and new
due time, return the item to a dispatchable state, and set the next due time. The item never rests
in `landed`; its runs, verdicts and events accumulate under one id.

**The gap.** The stored item states are `not-started`, `blocked`, `in-queue`, `landed`,
`resolved-elsewhere` and `closed`
([`tools/tillr-item/item/schema.go`](../../tools/tillr-item/item/schema.go)). The declared
transitions out of `landed` are one: `landed → in-queue`. Re-arm is the only writer that takes a
landed item back into the queue for a new cycle, and the build must state which declared transition
it uses — the existing `landed → in-queue`, or a new `landed → not-started` — in the definition,
reachable only from the re-arm write.

**The hook point.** An item is completed by the drain in `completeItem` and
`completeGraphWithoutCommit`
([`tools/tillr-drain/drain/workflow_exit.go`](../../tools/tillr-drain/drain/workflow_exit.go),
[`tools/tillr-drain/drain/workflow_exit_record.go`](../../tools/tillr-drain/drain/workflow_exit_record.go)),
which move `items.state` to `landed`; criterion verdicts are written separately by
`item.RecordVerdict` ([`tools/tillr-item/item/verdict.go`](../../tools/tillr-item/item/verdict.go))
into `item_criteria.state`. The re-arm attaches to the completion write, after the state write and
inside the same transaction. A recurring item's second cycle must be judged independently, so the
build also resets the item's criterion states for the new cycle.

**A document-declared recurring item must survive completion.** The drain retires a
document-declared item block before gating (`retireDocumentBlock` in
[`tools/tillr-drain/drain/retire.go`](../../tools/tillr-drain/drain/retire.go)). A recurring item
must not take that step, or its declaration disappears after the first cycle.

**The no-op trap.** A recurring item whose correct outcome is "nothing to do" produces no commit.
Tying re-arm to the judged completion closes that, *provided the item's criterion is written so a
no-op is judgeable as done*. That is a briefing obligation on each recurring item, not a mechanism
([§7](#7-the-first-user-a-recurring-check-in)).

**Stopping recurrence** is already expressible: `closed` is excluded from dispatch; pausing composes
with an existing hold or with pushing the due time out; clearing the interval makes the current
cycle the last one.

### 5.4 Failure does not advance the schedule

A failed or interrupted lane leaves the item uncompleted, so the item's `due_at` does not advance
and the item remains due under the drain's ordinary failure handling; this design adds no
schedule-specific attempt cap or backoff ([OQ-ST2](#12-open-questions)). That is the right default —
"not forgotten" includes not silently skipping a period because the attempt broke.

The ordinary failure handling is an owner ruling (2026-11-17), `D-JL10` in
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
candidates at startup, so a due check flips on its own as time passes. The subtlety: the drain wakes
early on store notifications, and the passage of time is not a store commit, so a due transition is
never *signaled* — only observed at the next pass. The fallback reconciliation poll
(`DefaultFallbackPollInterval`, 15 seconds, in
[`tools/tillr-drain/drain/trigger.go`](../../tools/tillr-drain/drain/trigger.go)) is that tick, far
inside any tolerance this feature promises, so V1 does nothing here. For `--exit-when-drained`, see
[goal monitor §8](tillr-goal-monitor.md#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained).

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
| **V1 — one-shot due** | The `due_at` column and its migration, the `not-due` check in `decide`, the not-due count and status-line field, the write faces' set and clear, the view detail | Proves the check, the view and the migration with zero lifecycle risk. Useful alone: "don't start this until the 3rd" |
| **V2 — recurrence** | The interval column, the re-arm transition, re-arm at the completion write, the `schedule.rearm` event, and the first recurring item | The only stage touching completion semantics |
| Deferred, with a named trigger | Catch-up mode, slot-anchored cadence, the earliest-due wake deadline, a skipped-period count in the view | Each is a recorded decision, not a gap |
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

Every row below is a designer decision; none carries a quoted ruling from the owner, and the owner
may overturn any of them ([decision provenance review](../tillr/decision-provenance-review.md)
[§5.2](../tillr/decision-provenance-review.md#52-medium-not-built)). None is built.

| ID | Decision | Reversal condition |
| :--- | :--- | :--- |
| D-1 | Due-ness is a derived admission check plus a new reason (`not-due`), on a stored nullable `due_at`. No new state, no new edge kind | The rule "stored facts stay stored, readiness stays derived" is itself reversed |
| D-2 | The check is the **first** check in the planner's `decide`, and it is there rather than in the query that loads the planner's items | A not-due item's other refusals turn out to be what an operator needs to see first |
| D-3 | A not-due item is a waiting state: no blocked event, no row in the blocked list, not counted as waiting on a lane, counted in a new not-due field | The status line's field count becomes a problem → fold the not-due count into the waiting count and accept the lie |
| D-4 | No scheduler or daemon; the drain's existing poll observes due transitions. Recorded invariant: the periodic tick must survive, or the earliest due time becomes a wake deadline | The drain becomes purely event-driven → adopt the wake-deadline variant |
| D-5 | Recurrence is a stored interval; next due = last completion + interval. No queueing, cadence drift accepted, no cron parser | A user needs fixed wall-clock slots → slot-anchored arithmetic, still no cron parser without a second user |
| D-6 | Catch-up defaults off and is not built; named as a per-item opt-in for if ever | A concrete item whose missed instances each have distinct value |
| D-7 | Recurring items re-arm **in place** at the completion write, with a `schedule.rearm` event and a declared transition that returns a completed item to the queue. No successor mechanism | The verdict model turns out to be once-only per item in a way re-arm cannot respect |
| D-8 | Failure never advances the schedule; no backoff added by this feature | An observed hot-loop on a failing recurring item → fix drain-wide, not per-schedule |
| D-9 | Priority is the existing rank, owner-set. No overdue boost, no preemption | Observed starvation that re-ranking the item does not fix |
| D-10 | Only the owner and the coordinating agent may write a due time or an interval | A lane legitimately needs to schedule its own follow-up work |
| D-11 | Build order V1 one-shot → V2 recurrence; the schema lands with V1 | A breakage makes V2 urgent → the stages may merge |

## 12. Open Questions

[`OQ-ST2`](#12-open-questions) carries no quoted ruling from the owner: it is the designer's decision
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

- **Any implementation.** No function body, no flag spelling beyond the table in
  [§2.3](#23-the-cli-surface), no migration code. The plan owns the build.
- **The `recur_catch_up` column.** Named as a per-item opt-in and deliberately not added to the
  schema, because an unused column acquires stray semantics.
- **Anything about the check's cost.** A due-time comparison is one integer compare on a value
  already read; if that ever matters, the measurement belongs to the planner's own work.

## 14. Where the code lives, by symbol

Nothing below is built. Each row names where the design attaches in the tree; the build
grounds each one before changing it.

| What this design touches | Where it lives |
| :--- | :--- |
| The two new columns | `ItemDDL` and `store.RegisterColumn("item", "items", ...)` in [`tools/tillr-item/item/schema.go`](../../tools/tillr-item/item/schema.go) |
| The version bump and the additive migration | `store.SchemaVersion` and `store.Open` in [`tools/tillr/store/schema.go`](../../tools/tillr/store/schema.go) |
| The planner's item row gains the due time; its filter and ordering are untouched | `itemRow` and the planner's item loading in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) |
| The new first check, and the `not-due` reason | `(*planner).decide` and the `Reason*` constants in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) |
| Keeping a not-due item out of the blocked, hold and dependency counts | The `WhyRow` classification in [`tools/tillr-drain/drain/why.go`](../../tools/tillr-drain/drain/why.go), and [`tools/tillr-view/`](../../tools/tillr-view/) |
| The pass that must not write a blocked event for a not-due item, the not-due count, and the `--exit-when-drained` check | [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) and [`tools/tillr-drain/drain/trigger.go`](../../tools/tillr-drain/drain/trigger.go) ([goal monitor §8](tillr-goal-monitor.md#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained)) |
| The re-arm transition | State and transition definitions in [`tools/tillr/definition/landing.json`](../../tools/tillr/definition/landing.json) and [`tools/tillr-item/item/`](../../tools/tillr-item/item/) |
| The completion write the re-arm attaches to | `completeItem` and `completeGraphWithoutCommit` in [`tools/tillr-drain/drain/workflow_exit.go`](../../tools/tillr-drain/drain/workflow_exit.go) and [`tools/tillr-drain/drain/workflow_exit_record.go`](../../tools/tillr-drain/drain/workflow_exit_record.go); `retireDocumentBlock` in [`tools/tillr-drain/drain/retire.go`](../../tools/tillr-drain/drain/retire.go) for a document-declared recurring item |
| The `due` and `every` values on the write faces | Item intake and updates in [`tools/tillr-item/`](../../tools/tillr-item/) (`sync`, `apply`, `request`) |
| The rule that only the owner and the coordinating agent write them | The write-face checks in [`tools/tillr-item/`](../../tools/tillr-item/) |
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
