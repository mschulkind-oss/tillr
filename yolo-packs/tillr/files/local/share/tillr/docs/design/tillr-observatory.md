---
title: "Tillr Observatory — the introspection contract behind the read server"
status: accepted
stage: BUILT
next: "Nothing. Fold what remains here into the two observatory references and retire this document."
tags:
  - tillr
  - observability
  - introspection
  - web
  - drain
summary: "What a drain must record so a read-only server can show every item, stage, tool call and hand-off without inventing missing evidence, and why the server is a pure reader with a normalized client cache."
depends-on:
  - session-recording.md
  - board-utilities.md
vantage:
  status-chip: true
---

# Tillr Observatory — the introspection contract behind the read server

> **Delivered copy.** This is `docs/design/tillr-observatory.md` from this repository at commit `0275c51d52` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-observatory.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Built as `tillr-view serve` ([`tools/tillr-view/`](../../tillr-view/README.md)),
a Go API with an embedded React front end. UNMEASURED: no timed operator session is recorded; the
evidence is the Go and front-end suites. The screens are specified in
[the observatory redesign](../reference/observatory-redesign.md) and the shared provenance
primitives in [observatory design](../reference/observatory-design.md); where this document and
those references differ, the references describe what runs.

> **In short.** Introspection is a contract over the store and the run artifacts, not a dashboard
> on top of logs. A read-only server assembles every stage, tool call and hand-off of a drained
> item, and marks a recording that does not exist as a gap instead of drawing a clean pass.

**Start at** [§2](#2-the-introspection-contract) for the contract and
[§3.3](#33-live-drain-concurrency-sqlite-wal-and-point-in-time-request-snapshots) for how a
reader coexists with a writing drain.

**Reads with:** [session recording](session-recording.md) (the emitters),
[the item-states reference](../reference/tillr-item-states.md),
[board utilities](board-utilities.md) (which utility owns serving) and
[the `tillr-trace` README](../../tillr-trace/README.md).

**Terms.**

- **Introspection contract** *(coined here)*: the closed set of operator questions in
  [§2](#2-the-introspection-contract) and the records that answer each. Not a screen layout.
- **Inter-tool exchange** *(coined here)*: one hand-off between two programs in a drain pass,
  either supervisor to utility (the drain calling the runner, the gate or the lander) or harness
  to tool inside a run (a model turn calling a tool, a shell starting a subprocess).
- **Epistemic state** *(coined here)*: the provenance marker on a span, exchange or claim that
  says whether it was observed, derived, never recorded or unreadable.
- **Snapshot cursor** *(coined here)*: the schema version, event head and generation time
  returned on every response, naming the store transaction a view was derived from.

---

## 1. Principles

An operator observes a running system, compares it with a mental model, and corrects it. Four
disciplines from the `engineering-principles` skill bind the design:

1. **The three-step operator loop.** Every autonomous mechanism supports observing state without
   side effects, comparing it with the expected stages and invariants, and correcting through an
   explicit control face.
2. **Truthful, observable state.** The record says what happened. A failure is reported with its
   class and cause; a status reflects the mechanism, not an optimistic projection.
3. **Visibility travels with behavior.** A behavior whose execution cannot be inspected is
   unfinished.
4. **Human-operable automation.** Automation bounds its own authority and leaves a trail an
   operator can audit at rest or in flight.

Two more are enforced as structural laws of the API and the pages:

- **Runtime evidence is human-reviewable.** Traces are inspectable and causally linked; a
  narrative never invents a fact.
- **Missing measurements are not zero.** Absent data stays absent. A page that draws a plausible
  timeline from partial data is a false green with a stylesheet.

---

## 2. The introspection contract

Every page and payload answers one of six questions.

| # | Question | Answered from |
| :--- | :--- | :--- |
| Q1 | What is it doing right now? | Store tables for items, lanes, runs and candidates; the running run's log; the run's step spans. |
| Q2 | What did it do? | Dispatch decisions, runs, candidates, landing attempts and events in the store; the per-run gate, landing and verdict records. |
| Q3 | What tools did it touch, and what went between them? | The saved run request, the per-run records and step spans (supervisor to utility); the run log's model turns and tool calls and the shell's subprocess rows (harness to tool). |
| Q4 | Why did it stop, or why is this item held? | Dispatch actions and reasons, run exits, landing results, the gate verdict and the loop's stop record. |
| Q5 | What did it cost? | Token counts by kind and wall time per turn, run and item. The HTTP face shows no price. |
| Q6 | What changed, and is the agent's claim true? | The candidate's base, head and tree; `gated_tree` against `landed_tree`; the claim checks `tillr-trace` computes from the run's artifacts. |

### 2.1 What is recorded and what is not

| Seam | Recorded |
| :--- | :--- |
| Item state, criteria and verdicts | Yes, in the store. |
| Pass decisions (dispatch or hold, with reason) | Yes, as `dispatches` rows. |
| Staging inputs (brief, fence, base) | Yes, copied per run under `.tillr/loop/<run-id>/`, so a reused lane never overwrites an earlier run's inputs. |
| Supervisor steps (boundary, fence, gate, land, verdict) | Yes, one span per step in the run's `steps.jsonl`, with clocks, exit code and a stderr tail. |
| Model turns and token usage | Yes, from the run log. |
| Model tool calls | Arguments and results yes. The clock is derived when the harness stamps none. |
| Subprocesses inside a shell tool call | Yes when the run's adapter exports the `BASH_ENV` shim; the shim is fail-open, so a run without it has a gap, not a failure. |
| Git operations inside a run | No. Git trace2 recording is deliberately off: its variables are inherited by every test suite a run starts and record hundreds of thousands of calls per run. |

### 2.2 Rendering a gap as a gap

Every span, exchange and claim carries one epistemic state, computed by the server:

1. **Observed.** Existence, payload and clock come from an authoritative record.
2. **Derived clock.** The event was observed and its timestamp was inferred from the enclosing
   turn. The page marks it and draws no invented duration.
3. **Unobserved seam.** The seam is part of the pipeline and its emitter did not run. The page
   names the missing stream. It never renders "0 subprocesses failed".
4. **Unreadable.** A file existed and failed to parse. The page shows the path and the error.

The references split these further into
[six marks](../reference/observatory-redesign.md#six-distinct-marks-for-zero-missing-and-derived-facts).

---

## 3. State projection and the read model

### 3.1 Item state projection

A page never shows a raw machinery string as an item's status. One server-side projector,
`ProjectItemState`, maps every item to the states of
[the item-states reference](../reference/tillr-item-states.md). Run, candidate, gate and landing
state render inside the item's attempt history, never as a competing item state.

### 3.2 Three read sources, nothing cached

`tillr-view serve` is a pure reader. It combines:

1. **The store**, queried per request inside one read transaction. The tables are small, and a
   second materialized copy would drift for no latency gain.
2. **Per-run records** under `.tillr/loop/<run-id>/` and the saved run request. These are small
   and immutable once written.
3. **Run trace artifacts**, read on demand through `tillr-trace`. There is no persistent trace
   index (owner ruling, 2026-11-18: derived data is not cached until a performance problem is
   shown). Every answer is recomputed from the artifacts a question needs
   ([no index](../../tillr-trace/README.md#no-index-every-answer-is-read-from-the-artifacts)).

### 3.3 Live drain concurrency: SQLite WAL and point-in-time request snapshots

A drain writes the store while the operator reads it.

- **Read-only connection.** `tillr-view serve` opens the store through `store.OpenRead`
  ([`open.go`](../../tillr/store/open.go)), which carries `mode=ro` and a busy timeout. It
  never changes the journal mode or repairs the database. The
  `every_route_answers_a_read_only_store` case in
  [`serve_internal_test.go`](../../tillr-view/serve_internal_test.go) holds it to that. A
  WAL reader needs readable `-wal` and `-shm` files or permission to create them; a bare database
  in an unwritable directory is not promised to open.
- **Readers do not block writers.** Every writable open selects and verifies WAL (SQLite's
  write-ahead log); `OpenRead` keeps the mode it finds
  ([`crash_test.go`](../../tillr/store/crash_test.go)). A WAL read transaction does not
  block a commit, and an uncommitted transaction from a killed writer stays invisible.
- **A hot rollback journal is a refusal, not a repair.** A store in rollback mode that holds an
  interrupted write needs a writable connection to recover. The API answers 503
  `store-unreadable`, names the uncommitted transaction, and points the operator to
  `tillr-store recover --store <path>`. Never delete a journal by hand.
- **Retry after repair.** A failed open is retried on each GET, serialized across simultaneous
  requests. Once an open succeeds the server keeps that handle, so a repaired store is reached
  without a restart.
- **One snapshot per request.** Each handler runs its queries in a single read transaction whose
  first query reads the event head. Every later query in that request sees the store at that
  event head.

---

## 4. Why the client is shaped this way

- **A normalized cache keyed by entity.** Every component reads through query hooks keyed by
  entity id. Manual refresh, fallback polling and WebSocket invalidation all invalidate the same
  keys, so adding live updates changed no component.
- **WebSocket over server-sent events.** One connection carries low-frequency store invalidations
  and high-frequency log deltas for a run the reader has opened, without reopening a stream per
  subscription.
- **The URL is the navigation state.** Reloading or pasting a URL reopens the same entity, tab
  and selection.
- **One process to view a drain.** The built front end is embedded in the Go binary; Node is
  needed only for development (`just observatory-dev`, supervised by
  [`Procfile.dev`](../../tillr-view/Procfile.dev)).
- **Front-end checks are path-scoped.** A change that does not touch `tools/tillr-view/web/` pays
  nothing for them, and [`observatory-dev.test.py`](https://github.com/mschulkind-oss/tillr/blob/main/tests/observatory-dev.test.py) concerns
  only the launch recipe's own files. A gate step whose cost rises on every unrelated commit is
  pointed the wrong way ([landing principles](landing-principles.md)).

---

## 5. Slices

Code comments name these labels.

| Label | What it delivers | Built |
| :--- | :--- | :--- |
| `OBS-1` | Per-run copies of staging inputs, recorded pass decisions, and step spans with stderr tails. | Yes |
| `OBS-2` | The read-only HTTP API in `tillr-view serve`. | Yes |
| `OBS-3` | The React front end over a normalized query cache. | Yes |
| `OBS-4` | Subprocess recording inside a run's shell. | The `BASH_ENV` shim is wired in the `pi` adapter; git trace2 is deliberately not recorded. |
| `OBS-5` | WebSocket invalidation at `/api/v1/ws`. | Yes |

---

## 6. What makes this fail

| Failure | Prevention |
| :--- | :--- |
| Invented or smoothed data | Every span, exchange and claim carries an epistemic state; a missing stream leaves a claim unverified, never green. |
| A second writer to the store | The server opens the store read-only only, and a test holds every route to it. |
| A front end that needs a rewrite to go live | Components read only through the query cache; every refresh path invalidates the same keys. |
| A front-end build that slows every gate | Go API tests need no Node; front-end checks are path-scoped. |
| Answering "what is it doing" only after the run ends | A run is recorded as running before its adapter starts, and step spans are written as steps begin. |
| Contradicting the item-state model | One projector computes the item's state; machinery state is nested under attempts. |
| Dropped utility stderr on a failed step | The step span keeps a bounded stderr tail. |

---

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-OBS1 | The server and its web tree live inside `tools/tillr-view/` as `tillr-view serve`, not in a separate utility. | 2026-11-16 | [§4](#4-why-the-client-is-shaped-this-way) | Yes |
| OQ-OBS2 | Recording (`OBS-1`) lands before the pages that read it. | 2026-11-16 | [§5](#5-slices) | Yes |
| OQ-OBS3 | Owner ruling. No persistent trace index; `tillr-view` asks `tillr-trace` per request, which keeps one parser and one claim verifier. | 2026-11-18 | [§3.2](#32-three-read-sources-nothing-cached) | Yes |
| OQ-OBS4 | Designer's decision; the owner may overturn it. The two principles of [§1](#1-principles) each get a full card in the `engineering-principles` skill. | 2026-11-16 | [§1](#1-principles) | Not verified in this repository; the skill is not in the tree |
