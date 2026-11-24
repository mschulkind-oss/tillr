---
title: "Scheduled board tasks: an item not due yet is simply not ready"
date: 2026-11-02
updated: 2026-11-19
status: in-review
stage: DESIGN
next: "Re-ground the deleted board-cli symbols, schema migration, CLI flags, item-state edges and completion hook in tools/tillr, tools/tillr-item, tools/tillr-drain and tools/tillr-view before building V1 and V2 ([§9](#9-staging-v1-v2-and-the-criterion), [§14](#14-where-the-code-lives-by-symbol)); run the scratch dropped-patch check in [OQ-ST3](#12-open-questions). The owner may review and overturn the designer decisions in [§11](#11-decision-ledger) and [§12](#12-open-questions)."
board: "243"
tags: [tillr, board, drain, scheduling, cron, design]
summary: "A due time and a recurrence are two nullable integer columns on the item, and due-ness is a derived admission gate so a not-yet-due item is simply not ready, keeps its rank, and needs no daemon: the drain's existing poll is the only clock. Recurrence re-arms the same item in place at completion, and the single due_at slot is what makes three missed weeks one run. Nothing is built, and the 2026-11-02 symbol grounding below targets the deleted tools/board-cli rather than Tillr (tools/tillr, tools/tillr-item, tools/tillr-drain, tools/tillr-view), so the seams need re-grounding before V1 and V2 are built."
---

# Scheduled board tasks: an item not due yet is simply not ready

**Status:** 2026-11-19 (written 2026-11-02). Nothing built: Tillr has no `due_at` or
`recur_interval_ms` column, no `not-due` admission gate, and no `schedule.rearm` transition
(`rg -n --glob '!docs/**' --glob '!yolo-packs/**' 'due_at|recur_interval_ms|not-due|schedule\.rearm' tools config tests`
returns only unrelated `next_due_at` steering-timer and `log-checkpoint.not-due` store matches).
Deferred until picked up: [`docs/tillr/roadmap.md`](../tillr/roadmap.md) lists this design as the
unbuilt substrate of [the goal monitor](tillr-goal-monitor.md) (iceboxed on 2026-11-17), and
`docs/pi/roadmap.md` lists the weekly fork roll-forward ([§7](#7-the-first-user-the-weekly-evergreen-roll-forward))
as designed and unbuilt. This document originated on 2026-11-02 as a design for board item 243 from
section 22 of the retired agent runtime design,
grounded against `tools/board-cli/internal/board/` at `8ccf3b6f0` [unlanded]. `tools/board-cli` was
deleted on 2026-11-11 (`afdc9a9571`) and replaced by Tillr (`tools/tillr`, `tools/tillr-item`,
`tools/tillr-drain`, `tools/tillr-view`); the symbol and schema grounding below keeps its 2026-11-02
board-CLI names as historical context and must be re-grounded on Tillr before build (see
[§8](#8-migration-and-compatibility), [§10](#10-verification) and [§14](#14-where-the-code-lives-by-symbol)).
Rows `D-1`–`D-12` in [§11](#11-decision-ledger) and settlements [`OQ-ST1`](#12-open-questions)–[`OQ-ST4`](#12-open-questions) in
[§12](#12-open-questions) are designer decisions without a quoted owner ruling and remain open to
the owner's overturn; the dropped-patch demonstration requested in [`OQ-ST3`](#12-open-questions) has
no recorded result in the tree.

**Needs your ruling:** None (no live `💬` question; see [§11](#11-decision-ledger) and
[§12](#12-open-questions) for the designer decisions and settlements open to the owner's overturn,
and [`OQ-ST3`](#12-open-questions) for the unrun scratch experiment).

**Terms.** *Due time* is the instant before which an item must not be dispatched. *Recurrence* is a
fixed period after which a completed item becomes due again. *Drain* is the foreground loop process
(`tillr-drain` today; `board drain` in the 2026-11-02 board CLI) that dispatches lanes; it is the
only execution loop that ever reads a due time.
*Admission* is the per-item decision the drain's planner makes each pass (`decide` in
[`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) today; `admitItem` in the
deleted board CLI): dispatch, or a named reason not to. *Rank* is the item's lexicographic priority
key, the queue's ordering term.

## 1. What this settles, and what it inherits

retired section 22 is complete and
self-consistent, and none of it is built:
`rg -n --glob '!docs/**' --glob '!yolo-packs/**' 'due_at|recur_interval_ms|not-due|schedule\.rearm' tools config tests`
returns only unrelated `next_due_at` steering-timer and `log-checkpoint.not-due` store matches. So
the work here is not invention. Three things were added on 2026-11-02 when reconciling
retired section 22 against the then-live
board CLI (each seam has since moved to Tillr; see [§14](#14-where-the-code-lives-by-symbol)).

1. **retired section 22's open questions
   are answered where the code answers them.** Q-ST1 (can an item judged done be judged again?) and
   Q-ST2 (is there one completion write to hook?) are both settled by reading the store, and Q-ST5
   (how a hold is stored) is settled too.
   retired section 22 gated V2 on Q-ST1;
   that gate is now open.

2. **One gap
   retired section 22 does not name is
   closed.** Re-arming in place moves an item from `completed` back to `not-started`, and the item
   machine declares no such edge.
   retired section 22's "reset in place" is
   not expressible today; [§5.3](#53-re-arming-in-place-and-the-transition-the-machine-is-missing)
   adds the edge.

3. **The refusal path is decided.**
   retired section 22 says admission
   returns a `not-due` decision. It does not say what the drain does with a refusal, and the drain's
   existing answer — append a `drain.blocked` event and count the item as blocked — is wrong for a
   not-due item. [§3.4](#34-what-a-not-due-item-does-to-ordering-and-to-counts) decides that.

Everything else was retired section 22's,
and that section was deleted with its document on 2026-11-21. This document is now the only
statement of the design, so the two rules it used to cite without restating are restated here.

> **The board may remember a time; it may not promise a time.** A scheduled item says "do not
> admit me before T". It never says "run me at T".

The test for whether work belongs here: *if lateness by days destroys the value, this is the wrong
primitive.* A certificate renewal with a hard external deadline needs an external timer on a
machine that is actually up, whose whole job is to file an ordinary, due-now item or page a
person. "Eventually, next time we are working, and never let me forget" is exactly, and only, what
this design provides.

One thing the retired section carried is not inherited: a per-lane time limit on the roll-forward
run. The owner ruled on 2026-11-20 against clocks that stop work, so nothing below depends on one.

## 2. The item fields

### 2.1 The two columns

| Field | Type | Meaning | Null means |
| :--- | :--- | :--- | :--- |
| `items.due_at` | `INTEGER NULL`, unix milliseconds | The next instant this item becomes admissible | No schedule: the item is due now, which is every item that exists today |
| `items.recur_interval_ms` | `INTEGER NULL` | The period after which a completed item becomes due again | One-shot: schedule once, complete, stay completed |

Both are written through the store's `now()` helper, like every other timestamp in the store
(`created_at`, `updated_at`, `runs.started_at`). Both are nullable and additive, so the migration is
the rehearsed one: two `ALTER TABLE items ADD COLUMN` statements and a `SchemaVersion` bump
([§8](#8-migration-and-compatibility)).

`recur_interval_ms` is meaningless without `due_at` — an interval with no first due time has no
anchor — so the store refuses the pair `due_at IS NULL AND recur_interval_ms IS NOT NULL`. That is a
write-time check in the store, not a table constraint: a `CHECK` would need a table rebuild, and the
pair is only ever written by two functions.

### 2.2 What an item without them means

Exactly today's behavior, unchanged. `due_at IS NULL` is the due predicate's first disjunct, so
every existing item is due at every instant, and `recur_interval_ms IS NULL` means the completion
transition does nothing new. This is the whole compatibility story: the feature is inert for every
item that does not opt in.

### 2.3 The CLI surface

Two flags on the verbs that already write item columns, following `--time-bound`'s precedent rather
than adding a verb:

| Surface | Form | Notes |
| :--- | :--- | :--- |
| `board item add` | `--due <when>`, `--every <duration>` | `--due` takes an ISO-8601 instant or a relative duration (`+7d`); `--every` takes a duration (`7d`, `36h`) |
| `board item update` | the same two flags | An empty value clears the column to `NULL`, the way `--time-bound ""` clears the bound |
| `board plan apply` | `due`, `every` on the `add` and `update` ops | So a recurring item can be filed reproducibly in one transaction |
| `board item show`, `item list` | `dueAt`, `recurIntervalMs` in the JSON body | Rendered as a relative hint in the human view |

The CLI parses human durations into milliseconds at write time; the store only ever sees an integer.
That is retired section 22.3's rule and it is what
keeps the store free of a duration parser.

**Who may write them.** Only the `owner` and `whale` seats (in Tillr's `decided_by` vocabulary,
`owner` and `whale`; `whale` was the board era's lead coordinator seat). A due time is a promise about when work
happens, and the seat doing the work is the one party that must not be able to move it — the same
ground on which `assertNotAuthorityEscape` in the 2026-11-02 board CLI refused a lane seat recording
its own landing or retiring its own item.

> **Current tree state (2026-11-19):** `board item add`, `board item update` and `board plan apply`
> were deleted with `tools/board-cli` (`afdc9a9571`). In Tillr, item writes and reads live in
> [`tools/tillr-item/`](../../tools/tillr-item/) (`sync` over tracked-document item blocks, `apply`,
> `request`, `show`, `list`), with no `--due` or `--every` flag or item-block attribute yet.

## 3. Readiness: the not-due gate

### 3.1 The rule

> An item is due when `due_at IS NULL OR now() >= due_at`. An item that is not due is not ready.

That is the whole readiness rule. It is evaluated in exactly one place, as a new gate in
`admitItem`, and it is the first gate in the chain.

### 3.2 Where it sits in the gate chain

`admitItem` runs its gates in a fixed order and returns the first refusal. The not-due gate goes
**first**, ahead of the bundle parent:

| Position | Gate | Why the order is what it is |
| :--- | :--- | :--- |
| 1 (new) | `not-due` | A property of the item alone, read from a column already in hand. Nothing else about the item is interesting until it is due |
| 2 | bundle parent | |
| 3 | dependencies | |
| 4 | criterion present and frozen | |
| 5 | hold | |
| 6 | external block | |
| 7 | live run | |
| 8 | series in flight | |
| 9 | unlanded candidate | |
| 10 | stranded or landed | |

Two consequences of putting it first, both wanted. A not-due item never reports a hold or a
dependency it also has, because those are not what is stopping it — the clock is. And a not-due item
stays out of every "not ready" count that means *waiting on something a human must move*
([§3.4](#34-what-a-not-due-item-does-to-ordering-and-to-counts)).

**The gate is in `admitItem`, not in `readyItems`' `WHERE` clause.** This is the one place
retired section 22's phrasing ("candidate
selection does not change shape") needs sharpening. Filtering in SQL would be cheaper and would be
wrong: an item excluded from the candidate set is invisible to `drain why` and to the view, so a
scheduled item would vanish from the board until it came due — the opposite of "never forgotten".
`readyItems` gains `due_at` in its `SELECT` list and nothing else; its `WHERE` and `ORDER BY` are
untouched, and the gate reads the value off the `readyItem` struct with no extra query.

### 3.3 Why a readiness rule and not a status

retired section 22.2 rules this and the
ruling holds against the code. The stored status set is a closed `CHECK` (`not-started`,
`in-progress`, `blocked`, `completed`, `abandoned`, `needs-resize`), and a `scheduled` value would
be a stored fact that time invalidates with no writer — the row would say `scheduled` forever until
some process rewrote it, which is the daemon this design exists to avoid. The item machine's
declared edges would need a `scheduled` state with edges to and from every other state, and the
diagram would then claim a lifecycle that no writer drives.

The precedent is in the same function: the hold gate and the external-block gate are already
admission gates that are neither a status nor an edge. Due-ness is the third of that kind.

### 3.4 What a not-due item does to ordering and to counts

**Ordering: nothing.** `readyItems` orders by `(rank IS NULL), rank, id` and the not-due gate is not
an ordering term. A not-due item keeps its rank position in the candidate list and is refused when
the loop reaches it; the loop continues to the next candidate, so a not-due item at rank `a` does
not delay rank `b`. It consumes no lane and blocks nothing.

**Counts: it is a waiting state, not a blocked state.** This is the decision
retired section 22 leaves open, and the
drain's existing refusal path gets it wrong by default. Today a refusal at admission appends a
`drain.blocked` event, adds a row to `report.Blocked`, and increments a per-reason count. For a
not-due item all three are false claims: nothing is blocked, nothing must move, and the event would
say a human has work to do. So:

| Surface | A not-due item |
| :--- | :--- |
| `drain.blocked` event | **Not written.** A blocked event is a claim that something must move; for a not-due item nothing does |
| `report.Blocked` | **Not listed.** The blocked list is the operator's work queue |
| `notReadyBucket` | Maps `not-due` to no bucket, so it stays out of the dependency, criteria and hold counts |
| `report.Waiting` | **Not counted.** Waiting means "ready, with no free lane"; a not-due item is not ready |
| A new `notDue` poll count | **Counted here**, rendered in the status line as `not due N` beside `waiting N` |
| The poll's per-item decided-state map | **Recorded**, so the item is decided once per poll and the waiting loop skips it |

The status line gains one field. That is the whole cost of the decision, and it is the honest one:
an operator reading `not ready 3 (dependency 1, criteria 1, hold 1)` must not find a scheduled item
in that 3.

**`drain why` gets it for free.** The read-only preview calls the same `admitItem`, so a not-due
item reports `dispatch: false, state: "not-due", detail: "due <instant>"` with no new code. The
detail is clipped at 160 runes by the view, so it stays short: `due 2026-11-09 · every 7d`.

## 4. Priority: due-ness never promotes

The frozen criterion's clause is "the next drain run picks it up at its configured priority".
Precisely:

> A due item competes for a lane exactly as any other ready item does, ordered by
> `(rank IS NULL), rank, id`. Due-ness changes *whether* an item competes, never *how* it ranks.

Three consequences, each a refusal:

- **No overdue boost.** An item three weeks overdue ranks where its rank puts it. A boost would be a
  second, invisible priority system contradicting the one the owner can see and edit, and it could
  not be configured away per item.
- **No preemption.** A due item never interrupts a running lane. Coming due mid-drain is admission
  at the next poll, and a due item that loses the competition waits, still due, indefinitely.
- **No promotion out of the unranked tail.** An unranked recurring item loses to every ranked item,
  because `(rank IS NULL)` sorts first. So a recurring item must be filed with an explicit rank; the
  roll-forward's is set at filing
  ([§7](#7-the-first-user-the-weekly-evergreen-roll-forward)).

Reversal condition: observed starvation of a due recurring item behind a continuous stream of
higher-ranked work. Even then the first fix is to re-rank the item, not to mechanize the exception.

## 5. Recurrence and the missed-period rule

### 5.1 The arithmetic

On the completion of an item whose `recur_interval_ms` is set:

```text
next_due_at = completed_at + recur_interval_ms
```

`completed_at` is the store's `now()` at the completion write, not the due time and not the run's
start. The anchor is the completion, so a "weekly" task runs every seven days from whenever it last
completed.

**Accepted property — cadence drift.** A completion-anchored weekly task's weekday wanders. For the
roll-forward this is irrelevant: what matters is minimum spacing and bounded staleness, both of
which hold. The alternative — slot arithmetic, the smallest `previous_due_at + k·interval` still
after completion — is deferred, not refused ([§9](#9-staging-v1-v2-and-the-criterion)).

### 5.2 Why three missed weeks are one run

The no-queue property is a property of the **storage**, not of the arithmetic, and this is the one
place retired section 22's phrasing invites
a wrong reading. The schedule has exactly one slot of memory: the single `due_at` value. There is
nowhere for three missed weeks to accumulate. An item whose due time passed three weeks ago is
simply overdue, once; the drain admits it once; and when it completes, the next due time is computed
from *that* completion, so the missed slots were never represented and cannot be replayed.

Slot arithmetic would also not queue — it computes the smallest future slot, so it too collapses
missed periods. What distinguishes the two is cadence drift, not queueing. The failure mode both
avoid is a **catch-up storm**: a schedule that remembers every missed period and fires them back to
back, which for state-convergence work is pure waste — rolling the series forward three times
converges to the same place as rolling it once to the newest release.

Catch-up is therefore a named per-item opt-in (`recur_catch_up`, default off) that is **not built**.
It is only correct when each missed instance has distinct value, and work of that shape is a strong
hint it does not belong on this board.

### 5.3 Re-arming in place, and the transition the machine is missing

The same item re-arms in place. At the completion transition for an item with an interval set, in
one store transaction: record the completion exactly as today (the run, verdict and landing records
stand — that history *is* the audit trail), append a `schedule.rearm` event carrying the old and new
due time, set the status back to `not-started`, and set the next due time. The item never rests in
`completed`; its runs, verdicts and lineage events accumulate under one id.

**The gap.** `completed → not-started` is not a declared edge of the item machine, so the write is
refused today. The fix is one row in the machine's transition table and the regenerated diagram:

| From | Event | To | Why |
| :--- | :--- | :--- | :--- |
| `completed` | `schedule-rearm` | `not-started` | The only way a completed item re-enters the pile, and it is reachable only from the re-arm write |

The machine's table is the source and the committed `.mmd` is emitted from it, with a snapshot test
comparing the two, so the diagram cannot be forgotten. The event name is `schedule-rearm` in the
machine's hyphenated vocabulary (`verdict-done`, `time-bound-repeat`, `resize-split`) and
`schedule.rearm` in the events table's dotted vocabulary (`drain.admit`, `drain.blocked`); the two
vocabularies are separate and both spellings are needed.

**The hook point is one function.**
retired section 22's Q-ST2 asked whether
both completion paths funnel through one write. They do: the landing path (`retireRunAndItem` →
`RecordVerdict`) and the judged path (`board verdict record` → `RecordVerdict`) both reach the same
`UPDATE items SET verdict = ?, status = ?` in `RecordVerdict`. The re-arm attaches there, after the
status write, inside the same transaction. That answers Q-ST2 and it is why V2 is cheaper than
retired section 22 feared.

**Q-ST1 is answered: an item judged done can be judged again.** The verdicts table has a non-unique
index on `item_id` and no uniqueness constraint, so a second verdict row is structurally allowed;
`RecordVerdict`'s only identity check is the criterion hash, which re-arm does not change; and
`not-started → completed` is a declared edge. So a second cycle is judged independently, with fresh
verdict rows, and the frozen-criterion gate behaves as it does for any item.
retired section 22 gated V2 on this; the
gate is open.

**The trap retired section 22 names,
kept.** A recurring item whose correct outcome is "nothing to do" produces no candidate, and a
no-candidate lane success loops back to rework unless the judge records done. Tying re-arm to the
judged completion closes that, *provided the item's criterion is written so a no-op is judgeable as
done*. That is a briefing obligation on the first user, not a mechanism
([§7](#7-the-first-user-the-weekly-evergreen-roll-forward)).

**Stopping recurrence** is already expressible: `abandoned` (`closed` in Tillr's item states) is
terminal and excluded from candidate selection; pausing composes with the existing hold gate or by
pushing the due time out; clearing the interval makes the current cycle the last one.

> **Current tree state (2026-11-19):** The `ItemMachine` / `item_machine.mmd` table and the single
> `RecordVerdict` status write above describe the deleted `tools/board-cli`. In Tillr:
> - Stored item states in [`tools/tillr/definition/landing.json`](../../tools/tillr/definition/landing.json)
>   and [`tools/tillr-item/item/schema.go`](../../tools/tillr-item/item/schema.go) are `not-started`,
>   `blocked`, `in-queue`, `landed`, `resolved-elsewhere` and `closed` (there is no `completed` or
>   `abandoned` state; `landed → in-queue` is already a declared transition, while `landed → not-started`
>   is not).
> - `item.RecordVerdict` ([`tools/tillr-item/item/verdict.go`](../../tools/tillr-item/item/verdict.go))
>   updates `item_criteria.state` per criterion rather than `items.state`, while the drain loop
>   completes landed and no-commit graph runs in `completeItem` and `completeGraphWithoutCommit`
>   ([`tools/tillr-drain/drain/workflow_exit.go`](../../tools/tillr-drain/drain/workflow_exit.go),
>   [`tools/tillr-drain/drain/workflow_exit_record.go`](../../tools/tillr-drain/drain/workflow_exit_record.go))
>   and retires document-declared item blocks before gating in `retireDocumentBlock`
>   ([`tools/tillr-drain/drain/retire.go`](../../tools/tillr-drain/drain/retire.go) — a step a
>   recurring document-backed item must not take if it is to survive across cycles).

### 5.4 Failure does not advance the schedule

A failed or interrupted lane leaves the item uncompleted, so the item's `due_at` does not advance
and the item remains due under the drain's ordinary failure machinery; this design adds no
schedule-specific attempt cap or backoff ([OQ-ST2](#12-open-questions)). That is the right default —
"not forgotten" includes not silently skipping a period because the attempt broke.

*(Written on 2026-11-02 against the board CLI's un-capped re-admission. In Tillr today, failure
handling is governed by the owner's ruling of 2026-11-17 — `D-JL10` in
[the judge and lifecycle design](tillr-judge-and-lifecycle.md#decision-ledger), built in
[`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go): worker endings are never
counted, nothing re-runs because an input hash changed, a failed or refused worker run returns to
its own session with its evidence, and only an identical repeat escalates to the design seat, while
explicit operator return is `tillr drain retry` under `D-JL6`/`D-JL8`.)*

## 6. No daemon

### 6.1 The invariant

> Nothing polls a due time. Nothing is always-on. A drain run is the only clock, and a due time in
> the past is simply ready.

The board is a **pull** system: nothing needs to push at the due moment, because the moment is only
ever consulted by the thing that was going to do the work anyway. There is no systemd unit, timer,
cron entry or launchd job for the board, and this design adds none. A drain exists only as a
foreground process a human or agent starts.

The invariant that makes it work, recorded so a future optimization cannot quietly break it:

> **The drain must retain a periodic re-evaluation tick, or the poll wait must take the earliest
> `due_at` as a wake deadline the way it already takes the run deadline.**

The drain re-runs candidate selection and admission on every poll rather than snapshotting candidates at
startup, so a due gate flips on its own as time passes. The subtlety: the drain also wakes early on
completions, and the passage of time is not a completion or store commit, so a due transition is never
*signaled* — only observed at the next poll (in the 2026-11-02 board CLI, roughly the two-second
fallback tick; in Tillr today, `DefaultStorePollInterval = 100ms` in `waitForTrigger`,
[`tools/tillr-drain/drain/trigger.go`](../../tools/tillr-drain/drain/trigger.go), far inside any
tolerance this feature promises; for `--exit-when-drained`, see
[goal monitor §8](tillr-goal-monitor.md#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained)).
The existing tick satisfies the invariant today, so V1 does nothing here.

**Clock behavior.** The comparison uses wall-clock unix milliseconds, consistent with the rest of
the store. Machine sleep is harmless: the first poll after wake sees the item due. A backwards clock
step delays a due item; a forwards step fires it early. Both are accepted — this is a not-forgotten
reminder, not a timed actuator.

### 6.2 A month with no drain

The item sits overdue for a month and nothing happens, which is the feature working. Nothing in the
board expires, TTLs or auto-abandons old items — the store's only time machinery governs run
liveness and leases, never item age — so a due item cannot rot away unnoticed. The first poll of the
next drain admits it, and the view shows it ready meanwhile. "A best-effort cron that is never
forgotten" reduces to: a stored timestamp with no expiry, read by a poll loop that already exists.

## 7. The first user: the weekly evergreen roll-forward

The roll-forward is the work of advancing each fork's patch series onto the newest upstream release
(section 21 of the retired agent runtime design).
It is manual today — plain-git rebase, since the pack-level rebase command is gone — driven by a
workspace-scoped skill, per fork, by hand.

**The item.** One item, not one per fork:

| Field | Value |
| :--- | :--- |
| `title` | Roll the fork series forward onto the newest upstream release |
| `kind` | `task` |
| `rank` | An explicit middling rank — below breakage work, above background cleanup. Filed with the item; not a default |
| `due_at` | The filing instant plus seven days |
| `recur_interval_ms` | Seven days |
| `criterion` | The four-clause accounting criterion below |

**What changes for it.** Today the work happens when someone remembers. After V2 it happens on the
first drain run after each seven-day boundary, at its rank, with no one remembering. Nothing about
the work itself changes: the brief is read fresh at each dispatch, so editing it updates every
future cycle.

**Two triggers exist for this work, and only one of them is this design's.** section 21.5 of the
the retired agent runtime design files a
roll-forward item when a fork's base drifts past 50 commits or any new release appears, evaluated
wherever the resolver runs. This design's item comes due every seven days regardless. Both are
unbuilt (for hex-pinned forks, [`tools/fork-check.py`](../../tools/fork-check.py) `--pin-drift`
exists separately as a manual detector whose `--file` path still shells out to the retired `board`
CLI; `docs/pi/fork-pin-drift.md`). [OQ-ST1](#12-open-questions) settled on
keeping both triggers with the calendar item as the floor — drift files early, the weekly item
guarantees one is never forgotten, and the weekly item does not duplicate an open drift item (a
2026-11-16 designer decision, open to the owner's overturn); this document specifies the calendar
item because it is the one that needs a due time.

**The criterion, and the dropped-patch failure mode.** Evergreen trades conflict-debt for one silent
failure: a patch quietly disappearing during the rebase. The standing checks are real but pointed at
a different seam — the export replay check compares the replayed series against the checkout's
branch, and the patch-branch checks compare patch-ids of the base-to-tip range against the series.
Both compare the *post-rebase branch* against the *post-rebase series*, so a commit dropped **during
the rebase itself** vanishes from both sides and every check passes. So the criterion adds the
pre/post comparison the standing checks do not make, phrased as landed, checkable artifacts rather
than a lane's say-so:

1. Before rebasing, capture the patch inventory of the old range: patch-ids and subjects. Patch-ids
   are stable across a faithful rebase, which is what makes the comparison mechanical.

2. After rebase and export, **every** inventory entry is accounted for in a record landed with the
   change: present in the new range by patch-id match, or named as absorbed upstream with the
   upstream commit that subsumes it, or named as intentionally dropped with a reason. No fourth
   category; an unaccounted patch fails the criterion.

3. The export replay check and the patch-branch checks pass on the landed tree, named in the
   criterion so the judge has an artifact rather than a report.

4. **No-op case:** if no followed upstream has moved, the criterion is satisfied by that fact being
   recorded — the run's landed report or verdict evidence naming the compared refs — so a correct
   no-op is judgeable as done and the schedule re-arms.

Clause 4 is the one that keeps the schedule alive. Without it a correct no-op cycle would either
spin or never advance the schedule
([§5.3](#53-re-arming-in-place-and-the-transition-the-machine-is-missing)).

Per-fork granularity was considered and refused for V2: one weekly item rolling everything matches
how the owner phrased it, keeps one rank and one brief to maintain, and the accounting record is
naturally per-fork inside it. Split later if one item's roll-forward proves too large for one lane's run to finish.

## 8. Migration and compatibility

| Question | Answer |
| :--- | :--- |
| What happens to existing items? | Nothing. `due_at IS NULL` and `recur_interval_ms IS NULL` are the defaults, and both mean today's behavior |
| Does the on-disk format need a version bump? | **Yes**, `SchemaVersion` 11 → 12. A read verb returns early on a store that already reports the current version, so a v11 store read through a `due_at` it does not have would fail every item read |
| What is the migration? | Two `ALTER TABLE items ADD COLUMN` statements, purely additive: no row rewritten, no constraint changed, no table rebuild |
| Does an older build refuse a v12 store? | Yes, with the existing "store schema version is newer than this board" refusal. That is the honest answer: it would not know what a due time means |
| Does the events table need a change? | No. `events.kind` is free text with no `CHECK`, so `schedule.rearm` needs no schema change |

The migration pattern is the rehearsed one: a schema version probed at open, nullable columns added
in place, table rebuilds reserved for constraint widenings. Adding a nullable integer column is the
cheap kind, which is why retired section 22.8 could say "nothing needs to land now"
without the schema work growing in the meantime.

> **Current tree state (2026-11-19):** The `SchemaVersion` 11 → 12 numbers above were the deleted
> `tools/board-cli` store's. In Tillr, `store.SchemaVersion` is `5` in
> [`tools/tillr/store/schema.go`](../../tools/tillr/store/schema.go), `items` is declared in `ItemDDL`
> ([`tools/tillr-item/item/schema.go`](../../tools/tillr-item/item/schema.go)), additive columns are
> registered via `store.RegisterColumn("item", "items", ...)`, `store.OpenReadOnly` refuses a newer or
> unmigrated schema version, and `events.kind` remains free text with no `CHECK`.

## 9. Staging: V1, V2, and the criterion

| Stage | Contents | What it proves |
| :--- | :--- | :--- |
| **V1 — one-shot due** | The `due_at` column and its migration, the `not-due` gate in `admitItem`, the `notDue` count and status-line field, the CLI set/clear flags, the view detail | Proves the gate, the view and the migration with zero lifecycle risk. Useful alone: "don't start this until the 3rd" |
| **V2 — recurrence** | The interval column, the `schedule-rearm` machine edge, re-arm at the completion transition, the `schedule.rearm` event, and the roll-forward item itself | The only stage touching completion semantics |
| Deferred, with a named trigger | Catch-up mode, slot-anchored cadence, the earliest-due wake deadline, a skipped-period count in the view | Each is a recorded decision, not a gap |
| Refused | Cron parser, timezone support, a daemon, a stored `scheduled` status, an overdue boost, preemption, expiry of overdue items, notifications on due transitions | Each smuggles in a promise the board cannot keep |

**Does the frozen criterion hold at V1, at V2, or only across both?** Only across both, and the
split is exact:

| Criterion clause | V1 | V2 |
| :--- | :--- | :--- |
| An item can carry a due time | yes | yes |
| An item can carry a recurrence | no | yes |
| It is not ready until due | yes | yes |
| The next drain run picks it up at its configured priority | yes | yes |
| Three missed weeks produce one run rather than three | no — there is no recurrence to miss | yes |
| No daemon or always-on machine is required | yes | yes |

So the criterion is **not met at V1** and **met at V2**. V1 is a real, shippable increment that
satisfies four of six clauses; the item is not done until V2 lands. Nothing in V1's schema or gate
changes for V2, which is the sense in which the design stays cheap: the risky part (re-arm) is
additive on top of the safe part (the gate).

## 10. Verification

Each clause of the frozen criterion, and the check that would fail if it were broken. On 2026-11-02
these were specified as Go tests in `tools/board-cli/internal/board/` (`board_test`); `tools/board-cli`
is deleted, so in Tillr the item-column and migration tests belong in
[`tools/tillr-item/`](../../tools/tillr-item/) and [`tools/tillr/store/`](../../tools/tillr/store/)
and the admission, priority and recurrence tests belong in
[`tools/tillr-drain/drain/`](../../tools/tillr-drain/drain/) (`plan` / `why` / `loop`).

| Clause | Test file | Assertion shape |
| :--- | :--- | :--- |
| Carries a due time | `items_test.go` | `item add --due <instant>` then `item show` reports the same instant; `item update --due ""` clears it to null |
| Carries a recurrence | `items_test.go` | `--every 7d` stores `604800000`; `--every` without `--due` is refused |
| Not ready until due | `drain_why_test.go` | An item with `due_at = now() + 1h` reports `dispatch: false, state: "not-due"`; the same item with `due_at = now() - 1h` reports `dispatch: true`. **No sleep and no elapsed-time assertion**: the due time is written relative to the real clock, so the case cannot fail under load |
| Picked up at its configured priority | `drain_test.go` | Two due items, ranks `a` and `b`, one free lane: the lane starts on `a`. Then a not-due item at rank `a` and a due item at rank `b`: the lane starts on `b`, proving a not-due item neither consumes a lane nor blocks lower-ranked work |
| Three missed weeks are one run | `drain_recur_test.go` (new) | An item with `due_at = now() - 21d` and `recur_interval_ms = 7d`, drained once, has exactly one run row. After the completion, `due_at` equals the completion instant plus seven days — not `now() - 14d`, which is what a queueing schedule would leave |
| No daemon | `drain_recur_test.go` (new) | The re-arm is observed by driving the completion write and then reading the row: no timer, no goroutine, no second process. The invariant itself is a review rule, not a test — a test cannot assert the absence of a future daemon |
| The re-arm transition | `machines_test.go` | The exhaustive from-by-event walk covers the new edge, and the `.mmd` snapshot test fails if the diagram is not regenerated |
| The migration | `migration_test.go` | A v11 store opens, ends at `currentSchemaVersion`, and its existing items read as due with no interval |

Two of these are worth stating as rules rather than as cases. **No test asserts elapsed time.**
Due-ness is written relative to the real clock, so a case is decided by a comparison and never by a
wait; a test that slept for a due time to pass would be a defect, not a flake. And **the not-due
refusal writes no event**: a case asserts the events table holds no `drain.blocked` row for a
not-due item, because that row is the claim
[§3.4](#34-what-a-not-due-item-does-to-ordering-and-to-counts) refuses.

## 11. Decision ledger

Every row below (`D-1`–`D-12`) is a designer decision from 2026-11-02; none carries a quoted ruling
from the owner, and the owner may overturn any of them ([decision provenance review](../tillr/decision-provenance-review.md)
[§5.2](../tillr/decision-provenance-review.md#52-medium-not-built)). None of `D-1`–`D-12` is built
in Tillr today; where a row names a 2026-11-02 `board-cli` symbol (`admitItem`, `readyItems`,
`drain.blocked`, `completed → not-started`, `whale`), see [§14](#14-where-the-code-lives-by-symbol)
for the current Tillr seam.

| ID | Decision | Reversal condition |
| :--- | :--- | :--- |
| D-1 | Due-ness is a derived admission gate plus a new reason (`not-due`), on a stored nullable `due_at`. No new status, no new edge kind | The item machine's "stored facts stay stored, readiness stays derived" ruling is itself reversed |
| D-2 | The gate is the **first** gate in `admitItem`, and it is in `admitItem` rather than in `readyItems`' `WHERE` clause | A not-due item's other refusals turn out to be what an operator needs to see first |
| D-3 | A not-due item is a waiting state: no `drain.blocked` event, no `report.Blocked` row, not counted in `waiting`, counted in a new `notDue` field | The status line's field count becomes a problem → fold `notDue` into `waiting` and accept the lie |
| D-4 | No scheduler or daemon; the drain's existing poll observes due transitions. Recorded invariant: the periodic tick must survive, or the earliest due time becomes a wake deadline | The drain becomes purely event-driven → adopt the wake-deadline variant |
| D-5 | Recurrence is a stored interval; next due = last completion + interval. No queueing, cadence drift accepted, no cron parser | A user needs fixed wall-clock slots → slot-anchored arithmetic, still no cron parser without a second user |
| D-6 | Catch-up defaults off and is not built; named as a per-item opt-in for if ever | A concrete item whose missed instances each have distinct value |
| D-7 | Recurring items re-arm **in place** at the completion transition, with a `schedule.rearm` event and a new `completed → not-started` machine edge. No successor mechanism | The verdict model turns out to be once-only per item in a way re-arm cannot respect — **answered: it is not** ([§5.3](#53-re-arming-in-place-and-the-transition-the-machine-is-missing)) |
| D-8 | Failure never advances the schedule; no backoff added by this feature | An observed hot-loop on a failing recurring item → fix drain-wide, not per-schedule |
| D-9 | Priority is the existing rank, owner-set. No overdue boost, no preemption | Observed starvation that re-ranking the item does not fix |
| D-10 | Only the `owner` and `whale` seats may write a due time or an interval | A lane legitimately needs to schedule its own follow-up work |
| D-11 | Build order V1 one-shot → V2 recurrence; the schema lands with V1 | Drift causes a breakage that makes V2 urgent → the stages may merge |
| D-12 | The first user is one weekly item for all forks, with pre/post patch-inventory accounting as a landed artifact | One item's roll-forward proves too large for one lane's run to finish → split per fork |

## 12. Open Questions

None of [`OQ-ST1`](#12-open-questions)–[`OQ-ST4`](#12-open-questions) carries a quoted ruling from the owner: all four were marked settled on
2026-11-16 (`b5e5073cb`) by the session's designer on recorded principle at the owner's request
([decision provenance review `OQ-PR22`](../tillr/decision-provenance-review.md#OQ-PR22)), so they
stay here rather than compacting into [§11](#11-decision-ledger) and remain open to the owner's
overturn. For [`OQ-ST3`](#12-open-questions), the designer settlement chose Option A ("Demonstrate it"), and that scratch
experiment has not yet been performed or recorded in the repository.

1. ✅ **OQ-ST1: Does the roll-forward keep two triggers, or does the calendar item replace the drift
   trigger?**

   [§7](#7-the-first-user-the-weekly-evergreen-roll-forward) names both. If only the calendar item
   lands, a fork that drifts on day two waits for day seven; if it runs regardless, a quiet fork gets
   a no-op cycle every week.

   - **A — Calendar only.** One mechanism, one rank; a release landing the day after a cycle waits
     six days.
   - **B — Drift only.** No no-op cycles, but it needs the resolver to run, and a fork nobody
     resolves never rolls forward.
   - **C — Both, calendar as the floor.** Drift files early, the weekly item guarantees a bound; the
     weekly item must not duplicate an open drift item.

   _Original leaning (2026-11-02):_ Both, with the calendar item as the floor (C). The drift trigger is the fast path; the
   weekly item is the promise that nothing is forgotten, which is the whole point of the feature.

   _Settled (2026-11-16, designer's decision on recorded principle at the owner's request; not ruled by the owner, who may overturn it):_ C. Both triggers, with the calendar item as the floor: drift files a roll-forward early and the weekly item guarantees one is never forgotten, and the weekly item does not duplicate an open drift item.

2. ✅ **OQ-ST2: How fast does a failing recurring item come back, and is that acceptable?**

   On 2026-11-02, [§5.4](#54-failure-does-not-advance-the-schedule) left a failing recurring item due, with no
   attempt cap and no backoff; for an ordinary item a human noticed, while for a recurring one nothing
   in the board CLI bounded it.

   - **A — Leave it.** Fix a hot loop drain-wide, not per-schedule.
   - **B — A per-item attempt cap on recurring items only.** Bounded, but a capped recurring item
     silently stops being recurring.
   - **C — Measure first.** The re-dispatch interval is unmeasured today.

   _Original leaning (2026-11-02):_ Measure first (C). The interval was unmeasured, and both remedies were cheap to add
   later while a wrong cap was a silent stop.

   _Settled (2026-11-16, designer's decision on recorded principle at the owner's request, updated 2026-11-18 for `D-JL10`; not ruled by the owner on [`OQ-ST2`](#12-open-questions) itself, who may overturn it):_ A. A failing recurring item is governed by the drain-wide rule like any other item, with no schedule-specific cap. Under the owner's ruling of 2026-11-17 (`D-JL10` in [the judge and lifecycle design](tillr-judge-and-lifecycle.md#decision-ledger)), a failed worker run goes back to its own session with its evidence, uncounted, and only an identical repeat goes to the design seat; the one re-dispatch on a changed input that the 2026-11-16 settlement first cited ([`OQ-TC3`](tillr-convergence.md#decision-ledger)) was removed under that ruling.

3. ✅ **OQ-ST3: Is the dropped-in-rebase gap real, or is the criterion guarding a hole that does not
   exist?**

   [§7](#7-the-first-user-the-weekly-evergreen-roll-forward) claims the standing checks cannot catch
   a commit dropped during the rebase. That is an inference from what the checks compare; no one has
   demonstrated it. The criterion is correct either way, so this gates nothing.

   - **A — Demonstrate it.** Drop a commit in a scratch lane checkout, run the standing checks,
     record whether they pass.
   - **B — Leave it as an inference.** The accounting record is wanted either way.
   - **C — Withdraw the claim** and keep the record on its own merits.

   _Original leaning (2026-11-02):_ Demonstrate it (A). A claim inside a criterion that nobody has checked is the shape
   this repository keeps paying for, and the experiment is one scratch checkout.

   _Settled (2026-11-16, designer's decision on recorded principle at the owner's request; not ruled by the owner, who may overturn it — **experiment still unrun as of 2026-11-19**):_ A. Demonstrate it: drop a commit in a scratch lane checkout, run the standing checks, and record whether they pass. A claim inside a criterion that nobody has checked is not evidence, and the experiment is one checkout. *(Tree state on 2026-11-19: no record of this scratch experiment exists in `docs/`, `tools/` or `tests/`, so whether the gap is real remains undemonstrated.)*

4. ✅ **OQ-ST4: Is seven days the right staleness bound for the roll-forward?**

   The interval is the only thing bounding how stale a fork's base can get when the drift trigger has
   not fired, and it was chosen because "weekly" is how the owner phrased the work. A seven-day
   interval means a release can sit unrolled for six days without the drift trigger, and a quiet week
   produces a no-op cycle. Nothing measures whether either matters.

   - **A — Seven days.** Matches the phrasing, and a no-op cycle is cheap.
   - **B — A shorter interval**, so a release is picked up sooner, at the cost of more no-op cycles.
   - **C — Let the drift trigger set the pace** and make the calendar item a long-stop at, say,
     thirty days. Depends on [OQ-ST1](#12-open-questions).

   _Original leaning (2026-11-02):_ Seven days for now (A). The interval is a stored value on one item, so changing it
   later is an edit and not a design change.

   _Settled (2026-11-16, designer's decision on recorded principle at the owner's request; not ruled by the owner, who may overturn it):_ A. Seven days. The interval is one stored value on one item, so changing it later is an edit.

## 13. What this does not decide

- **Any implementation.** No function body, no flag spelling beyond the table in
  [§2.3](#23-the-cli-surface), no migration code. The plan owns the build.
- **The roll-forward item's rank letter.**
  [§7](#7-the-first-user-the-weekly-evergreen-roll-forward) states the rule (below breakage work,
  above background cleanup); the letter is a filing detail.
- **Whether the pile shows a due time.** The pile is Pi's `manage_todo_list`, joined to board rows
  by `external_ref`; whether a due time surfaces there is a pile question, not a board one.
- **The `recur_catch_up` column.** Named as a per-item opt-in and deliberately not added to the
  schema, because an unused column acquires stray semantics.
- **Anything about the gate's cost.** A due-time comparison is one integer compare on a value
  already read; if that ever matters, the measurement belongs to the gate's own work.

## 14. Where the code lives, by symbol

> **Historical symbol map (2026-11-02) and current Tillr seams (2026-11-19):** Every symbol in the
> left column below was read in `tools/board-cli/internal/board/` at `8ccf3b6f0` [unlanded], which was
> deleted on 2026-11-11 (`afdc9a9571`). As [goal monitor §8](tillr-goal-monitor.md#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained)
> notes, none of these symbols exists in the tree under its `board-cli` package; the third column
> names the current Tillr package or file where each seam lives today and must be re-grounded before
> V1 and V2 are built.

| 2026-11-02 `board-cli` symbol | What this design touches | Current Tillr seam (2026-11-19, unbuilt) |
| :--- | :--- | :--- |
| `itemsTableBody` | The one column list, used by both the fresh schema and the rebuild; the two new columns go here | `ItemDDL` and `store.RegisterColumn("item", "items", ...)` in [`tools/tillr-item/item/schema.go`](../../tools/tillr-item/item/schema.go) |
| `SchemaVersion`, `OpenStore` | The version bump and the additive migration | `store.SchemaVersion` (`5` today) and `store.Open` / `store.OpenReadOnly` in [`tools/tillr/store/schema.go`](../../tools/tillr/store/schema.go) |
| `readyItem`, `readyItems` | The struct gains `DueAt`; the `SELECT` gains `due_at`; the `WHERE` and `ORDER BY` are untouched | `itemRow` and planner item loading in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) |
| `admitItem` | The new first gate, and the `not-due` reason | `(*planner).decide` in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) |
| `stateNotDue` | The new refusal constant, beside `stateHeld` and `stateExternalBlocked` | `Reason*` hold/wait constants and `identifyDecision` in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) |
| `notReadyBucket` | Maps `not-due` to no bucket, so it stays out of the graph counts | `WhySummary` / `WhyRow` classification in [`tools/tillr-drain/drain/why.go`](../../tools/tillr-drain/drain/why.go) and [`tools/tillr-view/`](../../tools/tillr-view/) |
| `RunDrain`'s poll loop | The refusal branch that must not write a `drain.blocked` event for a not-due item, and the new `notDue` count | `Loop` pass/wait reporting and `--exit-when-drained` check in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) and [`tools/tillr-drain/drain/trigger.go`](../../tools/tillr-drain/drain/trigger.go) ([goal monitor §8](tillr-goal-monitor.md#8-a-not-due-item-when-the-loop-is-told-to-exit-once-drained)) |
| `ItemMachine` | The new `completed --schedule-rearm--> not-started` edge, and the regenerated `item_machine.mmd` | State and transition definitions in [`tools/tillr/definition/landing.json`](../../tools/tillr/definition/landing.json) and [`tools/tillr-item/item/`](../../tools/tillr-item/item/) (`landed` rather than `completed`; no `.mmd` generator) |
| `RecordVerdict` | The one completion write both paths reach; the re-arm attaches here | Split in Tillr: `item.RecordVerdict` ([`tools/tillr-item/item/verdict.go`](../../tools/tillr-item/item/verdict.go)) writes criterion verdicts, while `completeItem` / `completeGraphWithoutCommit` ([`tools/tillr-drain/drain/workflow_exit.go`](../../tools/tillr-drain/drain/workflow_exit.go), [`tools/tillr-drain/drain/workflow_exit_record.go`](../../tools/tillr-drain/drain/workflow_exit_record.go)) move `items.state` to `landed` (and `retireDocumentBlock` in [`tools/tillr-drain/drain/retire.go`](../../tools/tillr-drain/drain/retire.go) retires one-shot document blocks) |
| `AddItem`, `updatableColumns` | The two new flags on `item add` and `item update` | Item intake and updates in [`tools/tillr-item/`](../../tools/tillr-item/) (`sync`, `apply`, `request`) |
| `opKeys` | `due` and `every` on the `add` and `update` plan ops | Document item-block grammar (`sync` in [`tools/tillr-item/item/`](../../tools/tillr-item/item/)) and `item.record` payload fields |
| `assertNotAuthorityEscape` | The owner/whale-only rule for the two new writes | Write-face and fence checks across [`tools/tillr-item/`](../../tools/tillr-item/) and [`tools/tillr-drain/`](../../tools/tillr-drain/) |
| `drain_verify.go`'s `admitItem` call | Constructs a `readyItem` literal; the new field's zero value is "due", which is the right default there | Pure planner tests over `Pass` (`Pass.At()`) in [`tools/tillr-drain/drain/`](../../tools/tillr-drain/drain/) |
| `now()` | The store's one clock, and the only thing a due time is compared against | Injected `Pass.At()` (`Pass.Now`) and `LoopConfig.Now` in [`tools/tillr-drain/drain/`](../../tools/tillr-drain/drain/), plus `time.Time` timestamps in [`tools/tillr-item/`](../../tools/tillr-item/) |

## 15. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| A stored `scheduled` status | Refused. A stored fact that time invalidates with no writer is the daemon this design exists to avoid |
| A blocker edge to a fake clock item | Refused. Due-ness is an item-to-clock fact; an edge needs a writer that runs on time, and cycle checks and edge rendering would need exemptions |
| Filter not-due items out in `readyItems`' `WHERE` clause | Refused. The item would vanish from `drain why` and the view until it came due — the opposite of never forgotten |
| A cron expression instead of an interval | Refused. A parser, timezone and daylight-saving semantics, and a next-fire computation far beyond "add the interval", for a wall-clock anchoring no current user needs |
| A successor item per cycle | Refused. No succession mechanism exists to reuse, and one recurring obligation would fragment across an ever-growing chain of ids |
| Catch-up by default | Refused. Recurring board work is state convergence, not per-slot obligation; three roll-forwards converge where one does |
| An overdue priority boost | Refused. A second, invisible priority system contradicting the one the owner can see and edit |
| A systemd timer that files a due item | Refused for this feature. It is the right answer for work whose lateness destroys its value, and that work does not belong on this board |

