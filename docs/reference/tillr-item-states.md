---
title: Tillr item states and frozen criteria
status: accepted
stage: CURRENT
verified: 2026-11-16
verified_commit: 087b4385b9208bf49b20d09fefe9564ea8618e96
covers:
  - tools/tillr-item/
  - tools/tillr-view/serveread.go
  - tools/tillr-view/blocked_waiter_test.go
  - tools/tillr-view/dispatchable_test.go
  - tools/tillr-view/serve_internal_test.go
tags: [tillr, item, state, reference]
---

# Tillr item states and frozen criteria

**Status:** Source-verified 2026-11-16 against `087b4385b9208bf49b20d09fefe9564ea8618e96`.
UNMEASURED: this reference does not claim observation of a live drain or board.

An item records the work requested, not the life of one attempt at that work.
Tillr separates its stored disposition from the view's reading of current activity.
A stopped attempt is not evidence that someone is still working. An acceptance
criterion's judgment is separate again: moving the item does not rewrite that judgment.

## Stored disposition and displayed activity

The writable vocabulary is owned by `writableStates` in
[`apply.go`](../../tools/tillr-item/item/apply.go). The following table explains its
meaning together with the derived view; it is not a second transition validator.

| Stored disposition | What the view reads without a live run or candidate |
| :--- | :--- |
| `not-started` | `not_started`: no work currently executing or queued by this reading |
| `in-queue` | `in_queue`: queued work, even without a current candidate |
| `blocked` | `blocked`: waiting on someone or something; metadata explains the wait |
| `landed` | `landed`: this item's work landed |
| `resolved-elsewhere` | `resolved_elsewhere`: the intent was satisfied by another path |
| `closed` | `closed`: work is no longer wanted, with its record retained |

`ProjectItemState` in [`serveread.go`](../../tools/tillr-view/serveread.go) emits
only seven display spellings: the readings above plus `working`. It also carries
`raw_state` for inspection; that field is not an eighth display state.

The projection proceeds in this order:

1. Read the stored disposition. Legacy `in-progress` reads as queued, not working.
   Legacy `completed` starts as not started and reads as landed only when the
   candidate argument is present at this step.
2. A supplied candidate changes the display to `in_queue`; a supplied run whose
   state is `running` then changes it to `working`.
3. Unresolved dependency edges change a nonterminal reading to `blocked`.
   A planner question hold does likewise unless an explicit recorded waiter
   already explains the block. Landed, resolved-elsewhere and closed readings
   are exempt from these final blocking overrides.

These are ordered overrides, not proof of a general terminal-state priority:
passing a candidate or running run can override even a terminal stored row.
In particular, the legacy `completed` intermediate reading is subsequently
replaced by `in_queue` when that same candidate argument is present. Do not infer
successful landing merely from that legacy word.

Unlike the original proposal, queue membership is **not solely derived from a live
candidate**. Stored `in-queue` remains queued after a run produces no judged
candidate. `SetState` in [`state.go`](../../tools/tillr-item/item/state.go) moves the
item's disposition alone; identical state and waiting metadata append no new event.

## Blocking and closure

`WaitingOn` in [`waiting.go`](../../tools/tillr-item/item/waiting.go) defines the
stored wait as a waiter and a reason. `SetState` requires both to be nonempty;
the accepted waiter names are `owner`, `design`, `plan`, or `item:<id>` naming
an existing item in the same store. This validator does not exclude the item itself.
Blocking is allowed from not-started, in-queue, or blocked. The writer preserves
an `underlying_state`; explicitly moving back to not-started or in-queue clears
the waiting metadata. There is no automatic unblock command implied by the view.

The view prefers the recorded waiter and reason over dependency-edge text.
For older blocked rows it honestly displays `waiter not recorded`. Dependency
edges and planner holds can also produce a blocked display without a stored
blocked disposition. The view's `underlying` text explains the wait; it is not
necessarily the stored resume disposition.

`Close` and `Reopen` in [`lifecycle.go`](../../tools/tillr-item/item/lifecycle.go)
require a reason and actor. Close accepts not-started, in-queue, or blocked;
it refuses an item with a successful landing record. It records the prior state,
reason, date and actor, leaving blocked metadata intact. Reopen restores that
saved disposition, including blocked. Ordinary state writes cannot enter or
leave closed: they must use these lifecycle operations. Definition updates may
retain closed without reopening it.

These item operations update the row and append an event in one transaction.
They do not delete branches, worktrees, runs or candidates, nor do they themselves
cancel a run or withdraw a candidate. Those broader effects in the historical
proposal must not be inferred from an item close. Likewise, this writer accepts
`resolved-elsewhere` without validating a resolving pointer; the state model's
required evidence is not enforced by `SetState` or `Apply`.

## What freezes

`checkState` distinguishes malformed vocabulary from frozen legacy values.
`abandoned`, `deleted`, and `in-progress` remain readable but no new state write
may produce them. Unknown names are usage errors; frozen names are state-rule
rejections. Legacy rows can move to a writable state. `completed` is a legacy
view input, not an accepted write spelling.

A criterion freezes independently of the item's lifecycle. In `carryCriteria`
in [`apply.go`](../../tools/tillr-item/item/apply.go), the signal is a **nonempty
stored criterion state**, not a presentation timestamp or a scan of verdict events.
For an existing criterion:

- An empty stored state leaves its text editable and permits its removal.
- A nonempty stored state forbids rewriting its text or omitting it from a
  replacement definition. Keep it and append a new criterion for changed requirements.
- Omitting the state in a definition update carries the stored state forward.
  Supplying a different state is refused, even when the stored state is empty.

`RecordVerdict` in [`verdict.go`](../../tools/tillr-item/item/verdict.go) is the
separate judgment writer. Changing an item state does not clear criterion states
or release frozen text. This keeps a recorded judgment attached to the requirement
it actually judged. It is not a freeze of the whole item definition: title, fence,
and other editable fields are not frozen by this comparison.

## Verification anchors

| Mechanism | Existing regression evidence |
| :--- | :--- |
| State moves and frozen legacy writes | [state tests](../../tools/tillr-item/state_test.go), `TestStateRefusesAStateNoTransitionMayWrite` |
| Criterion text, removal and state preservation | [criterion tests](../../tools/tillr-item/criterion_state_test.go), `TestAFrozenCriterionsTextCannotBeRewritten`, `TestAFrozenCriterionCannotBeDropped` |
| Close and reopen | [lifecycle tests](../../tools/tillr-item/lifecycle_test.go), `TestCloseAndReopenRestoreEveryStartingState` |
| Waiter metadata and clearing | [blocked-state tests](../../tools/tillr-item/blocked_state_test.go), `TestBlockedStateTransitionsAndClearing` |
| Working requires a live run | [dispatchable tests](../../tools/tillr-view/dispatchable_test.go), `TestProjectItemStateReadsWorkingFromTheLeaseNotTheStore` |
| Closed display vocabulary | [view tests](../../tools/tillr-view/serve_internal_test.go), `TestProjectItemStateNeverInventsAnEighthState` |

Re-verification starts with a diff from `verified_commit` over `covers`, then checks
these writers and the ordered projection. This document does not certify the
historical migration of abandoned or deleted rows, archive behavior, or a
compile-time prohibition on views reading raw state strings.
