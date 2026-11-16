---
title: "Tillr — next steps: the next wave, in dispatch order"
status: proposed
stage: DESIGN
updated: 2026-11-10
tags: [board, tillr, lanes, schedule, design-ruling]
next: "Dispatch GATE-4 and ITEM-PKG today; LAND-1/2, RUN-2 and GATE-3 in parallel behind them"
---

# Tillr — next steps: the next wave, in dispatch order

**Status:** 2026-11-10. Read against the tree at `529398fa01` with an uncommitted
`tools/fork-check.py` change (the `tools/board-cli` removal) in the working copy — not the
`4b52350f1c` the brief named, so preconditions below name that dirty change where it matters.
No suite was run for this ruling; "landed" claims are the brief's, checked against `git log`.

> **In short.** The lanes document's Wave 3 is one wave stale: its dependencies landed, so
> DRAIN-1 moves into this wave, LAND-1 should absorb LAND-2, and ITEM-2 and ITEM-3 should
> merge. The fastest path to a composed pipeline is five parallel lanes behind two small
> ones that land first — and the one lane nobody scheduled, GATE-4, is now the most urgent,
> because no tillr module is gate-selected at all.

**Terms.** A *lane* is one dispatchable unit of work: one worker, one landing. A *fence* is
the exact set of paths a lane may touch; two lanes run in parallel only if their fences are
disjoint file by file, and lanes in one module directory are serial even then
([`board-utilities-lanes.md`](board-utilities-lanes.md) [§1](#1-the-next-wave-named), [§3](#3-the-design-decisions-still-open)). *Records mode* means a face
works from record files with no store; *store mode* means it reads domain rows from a store.
**ITEM-PKG** is coined here: a small lane defined in [Section 1](#1-the-next-wave-named).

## 1. The next wave, named

Dispatch in this order. Group A lands first (both are small); Group B starts the same day on
disjoint fences; Group C starts when its named precondition lands. Landing order follows
unblocking count, per [`board-utilities-lanes.md`](board-utilities-lanes.md) [[§3](#3-the-design-decisions-still-open).1](board-utilities-lanes.md#31-the-substrate-tools-tillr): GATE-4,
ITEM-PKG, then GATE-3 and LAND-1/2 in either order, RUN-2, ITEM-2+3, DRAIN-1, VIEW-2R.

| Lane | Fence (exact) | Precondition | Not dispatchable if |
| :--- | :--- | :--- | :--- |
| **A1 GATE-4** — add every tillr module to `GO_TOOL_DIRS` | [`tools/fork-check.py`](../../tools/fork-check.py), the `GO_TOOL_DIRS` tuple only | The uncommitted `tools/board-cli` removal in the same file lands first | That removal is still unlanded — one lane holds the file at a time ([[§3](#3-the-design-decisions-still-open).2](board-utilities-lanes.md#32-the-other-shared-files) of the lanes doc) |
| **A2 ITEM-PKG** — move item's DDL and record→row mapping from `package main` into an importable package, mirroring [`tools/tillr-run/run/schema.go`](../../tools/tillr-run/run/schema.go) (`RunDDL`, `init`) | `tools/tillr-item/**` | ITEM-1 landed (yes) | `tools/tillr-item/decision.go` exists — ITEM-2 is already in the fence (its brief's own precondition) |
| **B1 LAND-1/2 (merged)** — `land enqueue`, `check`, `pass` against a hand-written `gate.verdict` fixture ([`board-utilities.md`](board-utilities.md) [§5.4](board-utilities.md#54-land-alone)) | `tools/tillr-land/**` (new) | SUB-1 landed (yes); verdict shape taken from [§5.7](board-utilities.md#57-gate-alone)'s field list under Assumption A-VERDICT below | The owner rules that `pass` may only be built against a gate-produced verdict — that reinstates the LAND-2-after-GATE-3 edge this ruling removes |
| **B2 GATE-3** — `tillr-gate run`: a `gate.plan` plus a worktree into a tree-bound `gate.verdict`, no-store mode only ([§5.7](board-utilities.md#57-gate-alone)) | `tools/tillr-gate/**` | GATE-2 landed (yes); Assumption A-INPROCESS below | The owner requires Python-gate parity (in-process checks executed) in this lane — see [Section 3](#3-the-design-decisions-still-open) |
| **B3 RUN-2** — `run lanes`, `run status` read faces over the existing `lanes`/`runs` tables ([§5.6](board-utilities.md#56-run-alone)) | `tools/tillr-run/**` | RUN-1 landed (yes) | It is scoped to include lease-setting or release before the lease ruling in [Section 3](#3-the-design-decisions-still-open) |
| **C1 ITEM-2+3 (merged)** — decisions and mail records, then the `design_doc` read-through, per the two briefs already written | `tools/tillr-item/**` | ITEM-PKG landed | A second worker is put in `tools/tillr-item/**` — same-module lanes are serial |
| **C2 DRAIN-1** — `drain plan` only: `drain.tick` in, `drain.decision` records out, nothing written ([§5.5](board-utilities.md#55-drain-alone)), without the `question-open` reason in this slice | `tools/tillr-drain/**` (new) | ITEM-PKG landed (drain's store needs item's DDL and materializer registered in its own process — [`tools/tillr/store/fixture.go`](../../tools/tillr/store/fixture.go), `materializerFor`); ITEM-1 and RUN-1 landed (yes) | ITEM-PKG has not landed: [`tools/tillr-item/schema.go`](../../tools/tillr-item/item/schema.go) is `package main`, and a main package cannot be imported, so drain cannot register `ItemDDL` today |
| **C3 VIEW-2R** — VIEW-2's records-mode half: full `view.status` assembly from a records file ([§5.8](board-utilities.md#58-view-alone)) | `tools/tillr-view/**` | LAND-1/2's brief has fixed the `land.attempt` payload spelling | It is scoped to store mode, which needs per-owner domain readers that do not exist yet |

**Is the lanes doc's [[§2](#2-the-critical-path).2](board-utilities-lanes.md#22-the-waves-after) Wave 3 still right?** In membership, mostly; in order, no. Three
changes, each forced by what has landed:

- **DRAIN-1 belongs in this wave, not Wave 4.** Its table entry depends only on ITEM-1 and
  RUN-1 ("their tables, readers, and seed materializers"), and [§5.5](board-utilities.md#55-drain-alone)'s `plan` face reads
  items, lanes, and runs only. Both landed. Its real remaining precondition is one nobody
  listed: item never got RUN-1's importable-package treatment, and
  [`tools/tillr-view/status.go`](../../tools/tillr-view/status.go) (`namesSubject`)
  records the same hole from view's side. ITEM-PKG is that missing precondition, priced as
  its own lane because it must land alone in `tools/tillr-item/**` before either consumer.
- **ITEM-2 and ITEM-3 merge.** They share one fence, so they were always serial; the
  ITEM-3 brief already builds on ITEM-2's `decodeAs` test helper. Two landings buy nothing
  over one lane with two task blocks. Same logic, different direction: **VIEW-2 splits**
  into VIEW-2R (records mode, this wave) and a store-mode remainder that waits for domain
  readers — one label was covering two waves, the defect [§5.2](board-utilities.md#52-store-alone) of the lanes doc names.
- **LAND-1 absorbs LAND-2.** See [Section 2](#2-the-critical-path).

GATE-4 is absent from [[§2](#2-the-critical-path).2](board-utilities-lanes.md#22-the-waves-after)'s Wave 3 because [[§2](#2-the-critical-path).1](board-utilities-lanes.md#21-running-now) queued it "the moment GATE-1 lands." It
never landed. [`tools/fork-check.py`](../../tools/fork-check.py) today reads
`GO_TOOL_DIRS = ("tools/pi-profiler", "tools/mq")`, and `rg tillr tools/fork-check.py`
returns only comments — so as far as I could determine, no landed tillr module
(`tools/tillr`, `tillr-store`, `tillr-item`, `tillr-run`, `tillr-gate`, `tillr-view`) is
built or tested by the gate at all. Every lane below lands ungated until A1 does.

## 2. The critical path

The goal the path is measured against is the owner's first one: the composed pipeline of
[`board-utilities.md`](board-utilities.md) [§5.9](board-utilities.md#59-the-composed-pipeline) running end to end on a fixture, which is
what DRAIN-2 (the supervision loop) then automates. With SUB-1, ITEM-1, RUN-1, GATE-1 and
GATE-2 landed, the remaining chains to DRAIN-2 are:

- ITEM-PKG → DRAIN-1 → DRAIN-2
- RUN-2 → DRAIN-2
- GATE-3 → LAND-2 → DRAIN-2 ← **the critical path**: three lanes, and the only chain with
  two serial code lanes before the join

**The single change that shortens it most: delete the GATE-3 → LAND-2 edge by building
`land pass` against a fixture verdict now (B1 above).** The lanes doc already narrowed
LAND-1's dependency to the verdict *shape* because [§5.4](board-utilities.md#54-land-alone) makes the verdict "a file, not a
running gate." The same argument covers `pass`: its whole behavior — verify the verdict
binds the candidate's `tree_sha`/`base_sha`, refuse on a moved trunk, move the ref only on
a landed attempt — is testable against a fixture repository and a hand-written verdict.
The plan's WS5 "ready when … a verdict file can be produced for a real candidate" is an
*integration* condition, not an implementation dependency; pricing it as one is exactly the
assumed-dependency defect [§5.1](board-utilities.md#51-the-common-contract) of the lanes doc removed twice before. After this change
the path is GATE-3 → DRAIN-2 and ITEM-PKG → DRAIN-1 → DRAIN-2, two lanes each, in parallel.

**The measurement that proves it:** when GATE-3 lands, swap the fixture verdict in
LAND-1/2's composed-pipeline test for a gate-produced one and count the non-test files
that change under `tools/tillr-land/`. Zero proves the edge was assumed. Any change there
is a verdict-shape disagreement, and it is charged to whichever lane deviated from [§5.7](board-utilities.md#57-gate-alone)'s
field list — which is why Assumption A-VERDICT must be written into both briefs now.

## 3. The design decisions still open

No lane in Group A or B waits for an owner ruling if the assumptions below are accepted
as written into its brief. Each assumption names what invalidates it; an invalidated
assumption stops the lane, it does not get worked around inside the fence.

| # | Question needing a ruling | Blocks | Assumption that lets it dispatch now |
| :--- | :--- | :--- | :--- |
| Q1 | [`tools/tillr-gate/registry.go`](../../tools/tillr-gate/registry.go) `(*Registry).Plan` iterates `Declarations` only; `InProcess` checks (the Python gate's own-code checks) never enter a `gate.plan`. Who executes them in `gate run` — a Go port, a shell-out, or nobody? | GATE-3 as a Python-gate replacement; GATE-5 | **A-INPROCESS:** a plan that owes an in-process check produces verdict `error`, exit 4 — never `pass`. A verdict that silently omitted checks would assert a transition that did not happen. Invalidated if the owner wants any real candidate landed on a GATE-3 verdict before parity exists; that makes Q1 a hard pre-ruling. |
| Q2 | [§5.7](board-utilities.md#57-gate-alone) lists the `gate.verdict` fields in prose; no Go type for it exists anywhere in the tree (`rg Verdict tools/tillr-gate/` finds only comments). Who fixes the exact payload spellings? | LAND-1/2 and GATE-3 agreeing | **A-VERDICT:** [§5.7](board-utilities.md#57-gate-alone)'s list verbatim — `verdict_id`, `plan_id`, `registry_digest`, `base_sha`, `tree_sha`, `result`, per-check `{check_id, result, duration_ms, log_ref}` — nested under the envelope's `payload`. LAND-1/2's fixture is the specimen; GATE-3's conformance case must match it. Invalidated by any GATE-3 need for a field [§5.7](board-utilities.md#57-gate-alone) does not name; that is a design amendment, per the lanes doc's [`OQ-L5`](board-utilities-lanes.md#8-open-questions). |
| Q3 | [§5.5](board-utilities.md#55-drain-alone) derives `question-open` "through the same surface as `item show`" — but that surface (ITEM-3's document parser) does not exist and its home is unruled: item's importable package, the substrate, or a copy in drain? | DRAIN-1's full [§5.5](board-utilities.md#55-drain-alone) face | **A-NOQ:** DRAIN-1 v1 never emits `question-open`; the parser lands once, inside ITEM-PKG's importable item package, with ITEM-2+3, and drain imports it in a follow-up slice. Invalidated if dispatch-correctness requires question gating in the first drain proof — then ITEM-2+3 joins the critical path ahead of DRAIN-1. |
| Q4 | Lease semantics: [`tools/tillr-run/run/schema.go`](../../tools/tillr-run/run/schema.go) ships `leased_until` always `NULL` ("Leases are RUN-2's face"). What sets a lease, for how long, and what face releases a stale lane? | RUN-2's release slice; DRAIN-2 later | **Ruled** in [`board-utilities.md`](board-utilities.md) [§5.5](board-utilities.md#55-drain-alone) and [§5.6](board-utilities.md#56-run-alone) ([Section 14](board-utilities.md#14-decision-ledger), D-UT17): `run exec` sets the lease at claim for `timeout_ms` plus grace on the store host's clock, the terminal record releases it, `drain once` reaps the expired, and `run release` is the standalone release face. RUN-2 v1 stays read-only until the LEASE chunk of [`tillr-chunking-plan.md`](tillr-chunking-plan.md) [Section 3](tillr-chunking-plan.md#3-the-chunks) builds it. |
| Q5 | Table owners for `breakages*` and `flake_overrides` (the lanes doc's [`OQ-L1`](board-utilities-lanes.md#8-open-questions)), and whether gate ever gets a store-named mode at all ([§5.7](board-utilities.md#57-gate-alone)'s `gate_plans`/`gate_verdicts`) | SUB-3 only | No lane in this wave introduces successor tables for either, and B2/B1 are fenced to no-store mode. Q5 stays open without blocking anything dispatched here. |
| Q6 | The registry is a property of the machine (wave-3 finding in [`board-utilities-plan.md`](board-utilities-plan.md)): may a verdict produced on one machine be consumed by `land` on another? | Real (non-fixture) use of LAND-1/2 | Verdicts are consumed on the machine that produced them until ruled otherwise. Invalidated by any cross-machine landing workflow; fixture work is unaffected either way. |

Two things that look open and are not: live mail transport for ITEM-2 is already settled by
its brief (`delivery` is `intent` in every mode, no network import), and the `tools/mq`
route for LAND-3 was ruled the implementer's choice in the design's decision ledger
([`OQ-UT2`](board-utilities-lanes.md#8-open-questions)). Neither needs the owner again.

## 4. What to delete or stop

- **Stop MISC-2** (inventory decoupling inside `tools/board-cli/`). Its trigger was "the
  inventory first ships in a binary not built from a checkout," and its entire value was
  protecting gate runs. The monolith is out of `GO_TOOL_DIRS` and being replaced; nothing
  replaces MISC-2 because nothing needs it. For the same reason, **invest nothing further
  in `tools/board-cli/internal/board/inventory_internal_test.go`, since deleted with the board CLI**:
  CONF-1's second instrument now sits in a suite the gate never runs, so it can rot
  silently. The event-kind oracle's surviving home is
  [`tools/tillr/conformance/`](../../tools/tillr/conformance/conformance_test.go) —
  provided GATE-4 puts it under the gate.
- **Defer SUB-3 indefinitely** (gate's store-named mode). The composed pipeline already
  carries plans and verdicts as records between faces, and VIEW-2R assembles status from
  that stream; `gate_plans`/`gate_verdicts` tables would be a second copy of both. It
  returns only if a named consumer must query verdicts inside a store — none exists. This
  also parks half of Q5 at no cost.
- **Defer WRAP-1 off every path.** No brief, test, or caller invokes `tillr <noun>` today;
  everything calls `tillr-<noun>` directly. The wrapper earns its lane at the first
  retirement or the first external caller, whichever comes first, and [`OQ-L3`](board-utilities-lanes.md#8-open-questions) (its module
  home and shim mapping) can wait with it.
- **Stop scheduling RET-\* as a six-lane queue.** Retirement's per-landing safety was "the
  monolith still builds and tests" ([§4](#4-what-to-delete-or-stop) of the lanes doc), and the gate no longer checks
  that, so the queue's cost (six serial landings through `command_registry_entries.go`)
  now buys an unverified claim. Replacement: retirements leave the schedule entirely and
  return as one cutover decision per utility, gated as before on the [[§5.1](board-utilities.md#51-the-common-contract)1](board-utilities.md#511-where-the-burden-of-proof-sits) strong
  acceptance (the binary installed alone, standalone faces green on a fixture) — run by
  hand and recorded, since the gate cannot run it. "Writes never dual-run" ([[§1](#1-the-next-wave-named)2](board-utilities.md#12-the-migration-path)) is
  unaffected: tillr utilities write only fixture stores until that cutover.

Nothing already landed makes a remaining code lane redundant. In particular VIEW-2R is not
covered by VIEW-1: [`tools/tillr-view/status.go`](../../tools/tillr-view/status.go)
(`statusPayload`) assembles presence and a snapshot, not the subject.

## 5. The spec lanes

**SPEC-LAND and SPEC-DRAIN, as lanes, are not needed for LAND-1/2 or DRAIN-1, and should
not be dispatched.** The evidence is the two briefs that already exist: SPEC-ITEM's
products (the ITEM-2 and ITEM-3 briefs) were written straight from [§5.3](board-utilities.md#53-item-alone) plus the
inventory, at tests-first depth, and [§5.4](board-utilities.md#54-land-alone) and [§5.5](board-utilities.md#55-drain-alone) fix their records just as completely —
every payload field, every exit code, the registered `reason` strings. What those sections
do *not* fix is enumerated in [Section 3](#3-the-design-decisions-still-open) as rulings
and assumptions, which is design-seat work a spec lane cannot do in the owner's place.
So: each Group B/C lane's lead writes its brief in the ITEM-2 style as its first task,
during the window while Group A lands. That keeps the spec-during-code-waves rule ([[§2](#2-the-critical-path).1](board-utilities-lanes.md#21-running-now)
of the lanes doc) without spending a dispatch slot on it.

Spec work *is* still owed in exactly two places, and both are correctly deferred because
their shape is discovered by a predecessor — the one case where the plan's sketch logic
was right: **LAND-3** (absorbing `tools/mq`, roughly 13,000 lines with its own store and
schema) needs an inventory brief before any route can be priced, and **DRAIN-2**'s
supervision loop (tick trigger, runner protocol, how landed/isolated outcomes are
consumed) is not specified by [§5.5](board-utilities.md#55-drain-alone) or [§5.9](board-utilities.md#59-the-composed-pipeline) at build-now depth. Write those two briefs
when LAND-1/2 and DRAIN-1 respectively have landed, and not before.

## What I could not determine

Whether any step outside `GO_TOOL_DIRS` — a `just` recipe, a pack build — compiles the
tillr modules today; I searched [`tools/fork-check.py`](../../tools/fork-check.py)
only. GATE-4's brief must close this (the lanes doc's [`OQ-L4`](board-utilities-lanes.md#8-open-questions)) before claiming coverage.
I also did not verify that ITEM-PKG is behavior-preserving as a pure move; if item's
`main` and its schema are more entangled than run's were, A2 grows — but it stays inside
one fence either way, so no other lane's dispatch depends on the answer.