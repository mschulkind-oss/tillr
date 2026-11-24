---
title: Tillr item states and frozen criteria
status: accepted
stage: CURRENT
verified: 2026-11-24
covers:
  - tools/tillr/definition/
  - tools/tillr-item/
  - tools/tillr-view/serveread.go
  - tools/tillr-view/blocked_waiter_test.go
  - tools/tillr-view/dispatchable_test.go
  - tools/tillr-view/serve_internal_test.go
tags: [tillr, item, state, reference]
---

# Tillr item states and frozen criteria

> **Delivered copy.** This is `docs/reference/tillr-item-states.md` from this repository at commit `282386b687` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-item-states.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Verified 2026-11-24 by reading the code named in `covers`.
UNMEASURED: this reference does not claim observation of a live drain or view.

An item records the work requested, not the life of one attempt at that work.
Tillr keeps three things apart:

- the item's **stored state**, which says where the request stands;
- the view's **displayed state**, which adds what is happening to it right now;
- each acceptance criterion's **judgment**, which moving the item never rewrites.

A stopped attempt is not evidence that someone is still working, so "working" is
never a stored state.

## Stored state and displayed state

The stored vocabulary and its permitted moves belong to the store's lifecycle
definition (package `definition` in [`tools/tillr/definition`](../../tillr/definition/)).
The default definition, `git-landing`
([`landing.json`](../../tillr/definition/landing.json)), declares six
states. The table gives their meaning and what the view shows when no run or
candidate is live.

| Stored state | Displayed state | Meaning |
| :--- | :--- | :--- |
| `not-started` | `not_started` | Nothing is queued or running for this item |
| `in-queue` | `in_queue` | Queued for work, with or without a current candidate |
| `blocked` | `blocked` | Waiting on someone or something; stored metadata names the waiter |
| `landed` | `landed` | This item's work landed |
| `resolved-elsewhere` | `resolved_elsewhere` | The intent was satisfied by another path |
| `closed` | `closed` | The work is no longer wanted; its record is retained |

`landed`, `resolved-elsewhere` and `closed` are the definition's finished
states: a finished item satisfies the items that depend on it.

`ProjectItemState` in [`serveread.go`](../../tillr-view/serveread.go) is
the one place the view reads a stored state. It emits seven displayed states:
the six above plus `working`. It also carries `raw_state` for inspection; that
field is not an eighth displayed state. The projection applies these overrides
in order:

1. Start from the stored state.
2. A candidate in the queue changes the display to `in_queue`. A run whose
   state is `running` then changes it to `working`.
3. Unresolved dependency edges change a display that is not finished to
   `blocked`. A planner question hold does the same unless a recorded waiter
   already explains the block.

The overrides are ordered, not a general rule that finished states win: a
supplied candidate or running run changes the display of a finished row too.
Only the blocking overrides in step 3 exempt finished displays.

Queue membership is stored, not derived from a live candidate: `in-queue` stays
queued after a run produces no judged candidate, because a run that does not
land does not move the item. `SetState` in
[`state.go`](../../tillr-item/item/state.go) moves the item's state alone.
A write that changes neither the state nor the waiting metadata appends no
event.

## Finishing an item

Under `git-landing`, entering a finished state needs evidence
(`checkTerminalEvidence` and `checkStateReceipt` in
[`tools/tillr-item/item`](../../tillr-item/item/)):

- **`landed`** needs at least one criterion, every criterion judged `done`, and
  a current landing or graph-completion receipt for the item.
- **`resolved-elsewhere`** needs a `--resolves-via` pointer of the form
  `item:<id>`, `commit:<sha>` or `doc:<path>`, and is refused for an item that
  already has a current landing receipt. An item pointer must name another
  stored item; commit and document pointers are recorded as references and not
  probed.
- **Leaving a finished state** needs a reason and an actor.
- **A move while a landing is publishing the item** to `closed` or
  `resolved-elsewhere` is refused until that landing records its receipt.

A repeated write of `landed` or `resolved-elsewhere` to an item already in that
state answers with the stored item and writes nothing.

## Blocking and closure

`WaitingOn` in [`waiting.go`](../../tillr-item/item/waiting.go) is the
stored wait: a waiter and a reason. A move to `blocked` requires both. The
accepted waiters are `owner`, `design`, `plan`, or `item:<id>` naming an item in
the same store; the check does not exclude the item itself. Blocking saves the
state the item was in as `underlying_state`. Moving back to `not-started` or
`in-queue` clears the waiting metadata. The view never unblocks an item.

The view prefers the recorded waiter and reason over dependency-edge text. A
blocked row with no recorded waiter displays `waiter not recorded`. Dependency
edges and planner holds can also produce a blocked display without a stored
`blocked` state, so the view's `underlying` text explains the wait and is not
necessarily the state the item resumes in.

`Close` and `Reopen` in [`lifecycle.go`](../../tillr-item/item/lifecycle.go)
each require a reason and an actor. Close accepts `not-started`, `in-queue` or
`blocked`. It records the prior state, reason, date and actor, and leaves
blocked metadata intact. Reopen restores the saved state, including `blocked`.
Ordinary state writes cannot enter or leave `closed`; they are refused with the
command to use. A definition update may carry `closed` through without
reopening the item.

Each of these operations updates the row and appends an event in one
transaction. None deletes a branch, worktree, run or candidate, cancels a run,
or withdraws a candidate.

## What freezes

Three state names — `abandoned`, `deleted` and `in-progress` — are frozen:
a stored row holding one stays readable and can move to a declared state, and
no write may produce one (`checkState` in
[`lifecycle_policy.go`](../../tillr-item/item/lifecycle_policy.go)). A
frozen name is a state-rule rejection; a name the definition does not declare is
a usage error. The view reads `in-progress` as queued. It never reads the
undeclared name `completed` as landed: such a row displays as not started, or as
queued when a candidate accompanies it.

A criterion freezes independently of the item's state. In `carryCriteria` in
[`apply.go`](../../tillr-item/item/apply.go), the signal is a **nonempty
stored criterion state**, not a timestamp or a scan of verdict events. For an
existing criterion:

- An empty stored state leaves its text editable and permits its removal.
- A nonempty stored state forbids rewriting its text or omitting it from a
  replacement definition. Keep it and append a new criterion for a changed
  requirement.
- Omitting the state in a definition update carries the stored state forward.
  Supplying a different state is refused, even when the stored state is empty.

`RecordVerdict` in [`verdict.go`](../../tillr-item/item/verdict.go) is the
only writer of a criterion's state. Changing an item's state does not clear
criterion states or release frozen text, so a recorded judgment stays attached
to the requirement it judged. The freeze covers criteria only: title, fence and
other editable fields are not frozen by it.

## Verification anchors

| Mechanism | Regression test |
| :--- | :--- |
| State moves and frozen names | [state tests](../../tillr-item/state_test.go), `TestStateRefusesAStateNoTransitionMayWrite` |
| Criterion text, removal and state preservation | [criterion tests](../../tillr-item/criterion_state_test.go), `TestAFrozenCriterionsTextCannotBeRewritten`, `TestAFrozenCriterionCannotBeDropped` |
| Close and reopen | [lifecycle tests](../../tillr-item/lifecycle_test.go), `TestCloseAndReopenRestoreEveryStartingState` |
| Waiter metadata and clearing | [blocked-state tests](../../tillr-item/blocked_state_test.go), `TestBlockedStateTransitionsAndClearing` |
| Working requires a live run | [dispatchable tests](../../tillr-view/dispatchable_test.go), `TestProjectItemStateReadsWorkingFromTheLeaseNotTheStore` |
| Seven displayed states | [view tests](../../tillr-view/serve_internal_test.go), `TestProjectItemStateNeverInventsAnEighthState` |

To re-verify, read the lifecycle definition, the writers named above and the
ordered projection. This document does not describe a store that declares a
lifecycle definition other than `git-landing`.
