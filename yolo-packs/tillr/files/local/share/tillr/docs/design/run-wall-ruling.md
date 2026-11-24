---
title: "Design ruling: no default run wall, and a committed candidate is judged before wall status"
status: accepted
stage: CURRENT
tags:
  - tillr
  - drain
  - run
  - timeout
  - state-machine
  - design-ruling
summary: "No backend sets a run wall by default. A lease may be unbounded (timeout_ms == 0 -> leased_until NULL), settle sends any run that produced a commit to the gate regardless of wall status, and an uncommitted walled run is resumed in settle before the lane is reset."
---

# Design ruling: remove the run wall and judge committed candidates before wall status

> **Delivered copy.** This is `docs/design/run-wall-ruling.md` from this repository at commit `0275c51d52` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/run-wall-ruling.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

> **Built.** No adapter sets a run wall by default.
>
> **Whose ruling.** "Ruling" here is a design agent's word: `D-RW1` to `D-RW4` are the designer's decisions of 2026-11-15, and the owner may overturn any of them.

A **run wall** (this document's word) is a limit on the total wall-clock duration of one
agent run, after which the run is killed. It is not a limit on one network read, and not
the lane lease, which only says how long a lane is reserved.

**Ruling.** `tillr` has no default run wall:

- [`config/tillr/backend-policy.json`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/backend-policy.json) carries no
  `timeout_seconds`, and the backend adapters (`run-pi`, `run-claude`, `run-muse`,
  `run-agy` under `tools/tillr-run/backends/`) wrap the harness in `timeout(1)` only when
  `--timeout` is passed.
- `timeout_seconds: 0` or `timeout_ms: 0` is an unbounded run whose lane lease carries
  `leased_until = NULL`.
- When an explicit wall does fire, `settle` in
  [`tools/tillr-drain/drain/loop.go`](../../tillr-drain/drain/loop.go) (stated in the
  drain reference's [transition table](../reference/tillr-drain-successor.md#transition-table))
  judges a run that committed and resumes one that did not.

---

## 1. Should the wall exist at all?

**Decision (`D-RW1`):** **No.** There is no default wall anywhere. `timeout_seconds` and
`timeout_ms` remain as an optional per-backend or per-request bound; `0` or omitted means
no wall, and a negative value is refused.

A run that an explicit wall did cut (`StateTimedOut`, or the adapter's exit `124`) is
classified `AttemptUnjudged` by `ClassifyAttempt` in `tools/tillr-drain/drain/failover.go`,
never as an infrastructure failure. Classifying it as infrastructure would re-run long
work from the start on a second backend and count against that backend.

**Why:**

- **A total-run wall is a budget, not a fault detector.** The
  [momentum principle](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/momentum-principle.md) forbids budgets that stop valid work.
  Measured over one batch of runs under an 1800-second wall: the wall caught no hang, cut
  two runs that had already committed, and killed a third 18 seconds after all of its
  checks had passed.
- **Every failure a wall was meant to cover is handled elsewhere:**
  1. *Normal exit or harness crash:* the terminal `run.record` and the lane release are
     written in one store transaction.
  2. *Operator stop:* the runner catches `SIGINT` and `SIGTERM`, kills the process group
     and writes the terminal record.
  3. *A runner killed outright:* a lease with a deadline is reaped by `ReapExpiredLeases`
     in `tools/tillr-run/run/lease.go`. A `NULL`-deadline lease is skipped by that reaper,
     the loop stops with `lanes-leased` naming `tillr run release <lane>`, and
     `ReleaseLane` frees it (`D-UT17` in the
     [utilities design's decision ledger](board-utilities.md#14-decision-ledger)).
  4. *A stalled network stream inside a harness:* a wall on total duration cannot tell a
     stalled socket from a long stretch of thinking. An idle timeout belongs on one read
     inside the harness, never on the run.

---

## 2. When the wall fires on a run that produced a commit

**Decision (`D-RW2`):** **A commit dominates the wall.** When an explicit wall fires on a
run whose lane `HEAD` moved off its base, and whose report does not end `RESULT: blocked`,
`settle` treats the run as a candidate and runs `Verify`, exactly as for a run that
finished. Once the gate records a `gate.verdict`, that verdict governs planning and retry
authorization, not the unjudged class.

**Why:**

- **The state model requires it.** The drain reference defines a
  [candidate](../reference/tillr-drain-successor.md#the-successor-function-and-derived-positions)
  as a run whose terminal record is stored, that produced a commit, and that the gate has
  not judged. A walled run with a commit meets that definition.
- **The gate catches every hazard of a mid-run kill.** `Loop.fenceViolation` refuses a
  dirty worktree or an out-of-fence path before the gate runs, and the gate runs the
  planned checks against the committed tree, so an incomplete commit fails as
  `gate-failed` with its head pinned at `refs/tillr/runs/<run_id>`.
- **The retry acknowledgment follows the gate.** A walled candidate that then fails the
  gate is both judged and unjudged-by-class. Retry authorization checks the gate's result
  first, so the hold reads `gate-failed` and accepts `--acknowledge gate-failed`; the
  `unjudged` acknowledgment is required only when no verdict exists.

---

## 3. When the wall fires on a run that produced no commit

**Decision (`D-RW3`):** **Resume in place, in `settle`, before the lane is reset.** The
continuation runs on the same lane and `run_id`, with the same harness session, and spends
neither a retry nor a failover hop. If the continuation commits, `D-RW2` sends it to the
gate. A run is never resumed when its lease is reaped, because that runner vanished
unobserved.

`resumeAfterWall` in `tools/tillr-drain/drain/loop.go` implements it. Two bounds apply:

| Case | Bound |
| :--- | :--- |
| The run left uncommitted work inside its fence | Continued until a continuation repeats the previous one identically |
| The run left no uncommitted work | One continuation per `run_id` (`Record.Resumed`) |

A lane with no `brief.md` or no session id has nothing to continue, so the item is held as
`run-unjudged`.

**Why:** an uncommitted walled run has no commit to pin, so its only artifacts are files
in the lane worktree, which a reset destroys. Continuing the warm session while those
edits are on disk turns them into a commit the gate can judge.

---

## 4. What is the smallest diff?

**Decision (`D-RW4`):** `D-RW1` to `D-RW3` are one change. Removing the wall without the
`settle` ordering leaves a walled, committed, gate-failed run that no acknowledgment can
retry. The change needs no schema migration and no new item state.

---

## Decision ledger

Every row is a **designer's decision; the owner may overturn it**.

| ID | Ruling | Rejects |
| :--- | :--- | :--- |
| `D-RW1` | No default `timeout_seconds` in `config/tillr/backend-policy.json` and no default wall in any adapter; `timeout_ms == 0` is an unbounded run with `leased_until = NULL`. A walled run stays `AttemptUnjudged`, outside the infrastructure class. | Keeping or tuning a default wall (violates the [momentum principle](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/momentum-principle.md)); classifying a wall as an infrastructure failure. |
| `D-RW2` | `settle` checks for a commit before the unjudged class, and a `gate.verdict` supersedes the unjudged class in planning and retry authorization. | Holding committed runs as `run-unjudged`; an acknowledgment no value can satisfy. |
| `D-RW3` | An uncommitted walled run is resumed in `settle` before the lane is reset, uncounted; never resumed when a lease is reaped. Built in part: a continuation of uncommitted fenced work is bounded only by the identical-repeat rule, while a wall-cut run with no uncommitted work keeps one continuation (`resumeAfterWall`). | Resetting the lane at once (destroys uncommitted edits); resuming with no bound at all. |
| `D-RW4` | `D-RW1` to `D-RW3` ship as one change. | Separating wall removal from the `settle` and retry ordering; a schema migration or a new item state. |

---

## Open questions

None.
