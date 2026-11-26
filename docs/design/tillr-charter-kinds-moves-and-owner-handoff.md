---
title: "Two item kinds in one charter, recorded decision moves, and hook handoffs to the owner"
author: "Agent"
date: 2026-11-26
status: in-review
stage: DESIGN
next: "Rule OQ-CK1, OQ-CK2, and OQ-CK3 before queueing the four build items in Section 9"
depends-on:
  - tillr-core-and-git-landing.md
  - tillr-idea-pipeline.md
  - tillr-separable-parts.md
tags: [tillr, charters, definitions, items, lifecycle, hooks, drain]
summary: "Answers a consuming repository's two open questions — per-kind lifecycles in one charter versus cross-store links, and actor/reason on unfinished-state moves versus move-linked decision records — specifies how complete-item hands an item and proposal to the owner without finishing it, states what existing version 1 charter files change, and breaks the build into four ordered items."
vantage:
  status-chip: true
---

# Two item kinds in one charter, recorded decision moves, and hook handoffs to the owner

On 2026-11-25, a consumer repository running [`tillr`](./tillr-core-and-git-landing.md) against the 2026-11-24 pack reported its two highest-priority charter gaps after real queue use, together with the completion-hook handoff those gaps sit beside. Re-reading the tree at `555c0414` on 2026-11-25 (and unchanged on this branch) confirms all three facts in code, while [What tillr changes for a consuming pipeline](./tillr-separable-parts.md#74-further-requests) lists the first two as `R3-1` and `R3-2` and leaves them unruled in [§7.7 of that document](./tillr-separable-parts.md#77-questions-the-requests-leave-open).

This document evaluates the options and states a recommendation for the owner on each open question ([`OQ-CK1`](#OQ-CK1), [`OQ-CK2`](#OQ-CK2), and [`OQ-CK3`](#OQ-CK3)), specifies the recommended mechanics across [`tools/tillr/definition/`](../../tools/tillr/definition/), [`tools/tillr-item/`](../../tools/tillr-item/), [`tools/tillr-drain/`](../../tools/tillr-drain/), [`tools/tillr-view/`](../../tools/tillr-view/), and [`tools/tillr/watch/`](../../tools/tillr/watch/), states what a `version: 1` charter file written today changes ([§6](#6-what-a-charter-file-written-today-must-change)), and ends in four ordered build items ([§9](#9-build-items)). Every design choice below (`D-CK1` through `D-CK6`) is a **designer decision from an operator request** that the owner may overturn; per [`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21), nothing in [§9](#9-build-items) is queued to build until the owner rules [`OQ-CK1`](#OQ-CK1) through [`OQ-CK3`](#OQ-CK3).

---

## 1. What the tree does today

Re-reading the Go packages at `555c0414` establishes the exact failure modes the consumer hit:

1. **One flat lifecycle per charter, and no `kind` on an item.** `Definition` in [`tools/tillr/definition/definition.go`](../../tools/tillr/definition/definition.go) has one top-level `states` slice, single `ready`, `blocked`, and `closed` strings, one `finished` slice, and one `transitions` list. `checkJSONKeys` refuses any unknown key (such as `"kinds"` or `"default_kind"`). On the store side, `itemPayload` in [`tools/tillr-item/item/apply.go`](../../tools/tillr-item/item/apply.go) has no `kind` field (strict decoding refuses `"kind"` as `unknown-field`), and `items` in [`tools/tillr-item/item/schema.go`](../../tools/tillr-item/item/schema.go) has no `kind` column. When findings and bugs share one store so `blocked_by` edges and `item:<id>` pointers can join them, nothing stops a finding from moving into `reported` or a bug from moving into `inconclusive`.
2. **`--reason` and `--by` are refused on unfinished-to-unfinished moves.** `checkStateReceipt` in [`tools/tillr-item/item/finished_receipt.go`](../../tools/tillr-item/item/finished_receipt.go) computes `entering := !wasFinished && nowFinished`, `leaving := wasFinished && !nowFinished`, and `releasing := currentState == d.Blocked && to == d.Ready`. It refuses `--reason` with `flag-not-stored` unless `entering || leaving || to == d.Blocked || releasing`, and refuses `--by` with `flag-not-stored` unless `entering || leaving || releasing` (even `to == d.Blocked` refuses `--by`). `SetStateWithReceipt` in [`tools/tillr-item/item/state.go`](../../tools/tillr-item/item/state.go) calls `recordLifecycle` only on those branches, so `readStateMoves` in [`tools/tillr-view/item_history.go`](../../tools/tillr-view/item_history.go) records blank `Actor` and `Reason` for every owner decision between unfinished states (such as `awaiting-owner -> approved` or `approved -> in-queue`).
3. **`complete-item` can only finish an item or trigger a refusal.** `CompleteItem` in [`tools/tillr-drain/drain/hook_completion.go`](../../tools/tillr-drain/drain/hook_completion.go) treats hook exit `1` as `Completed: false`, which `settleHook` passes to `l.refuse(..., RefusalNoOp)` — incrementing the refusal counter and re-dispatching or faulting the item rather than parking it for the owner. On exit `0`, `CompleteItem` requires `d.IsFinished(answer.State)` and faults with `"hook must name a finished state and a nonblank reason"` (`complete-item-fault`) if the hook names any unfinished state, and unconditionally passes `--require-verdicts` to `tillr-item state`. A hook therefore has no non-faulting way to say *"the agent finished drafting this proposal; park the item waiting on the owner without finishing it."*

---

## 2. Governing rulings and invariants

Three settled rulings in the repository bound any design here:

- **The owner's 2026-11-16 charter direction** ([The core and the `git-landing` charter §1](./tillr-core-and-git-landing.md#1-the-direction-in-the-owners-words)) establishes five core invariants:
  - states and allowed transitions are declared in the charter, not in Go code;
  - role states (`ready`, `blocked`, `closed`, `finished`) tell the core how to dispatch and hold without hardcoding state names;
  - *"No item reaches a finished state without a recorded verdict against its criteria"*;
  - *"No item waits without a named next step and an actor who takes it."*
- **Item kinds and single-store edges** ([`D-IP1` and `D-IP2` in The idea-to-code pipeline §6](./tillr-idea-pipeline.md#6-decisions-and-open-questions)) already chose a `kind` column on `items` and an `item_edges` table in one SQLite store for tillr's own unbuilt `idea`, `design`, and `build` kinds, rather than separate stores per kind.
- **Provenance discipline** ([`D-PR2` and `OQ-PR21` in Decision provenance review](../tillr/decision-provenance-review.md#OQ-PR21)) requires that choices affecting what the owner writes, sees, or operates be put to the owner as open questions before implementation, and that designer-originated mechanisms be labeled as designer decisions the owner may overturn.

---

## 3. Two item kinds: one charter versus links across two stores

The consumer's queue holds two item kinds — `finding` (an automated or exploratory observation to verify) and `bug` (a confirmed defect to draft, review, and report) — that point at one another (`blocked_by` edges, `--resolves-via item:<id>`, `--waiter item:<id>`) while following distinct state graphs.

### 3.1 The two options

| Dimension | Option A: Per-kind lifecycles in one charter (one store) | Option B: Separate charters across two stores with cross-store links |
| :--- | :--- | :--- |
| **Lifecycle isolation** | `Definition` declares `kinds.<kind>` lifecycles; `items.kind` binds each item to its kind's `states`, roles, `finished` set, and `transitions`. A `finding` cannot enter a `bug`-only state. | Each store has its own single-lifecycle `store.definition`. Isolation comes from file separation. |
| **Edges and pointers** | Existing `item_edges (item_id, blocked_by)` foreign keys, `checkWaitingOn` (`item:<id>`), and `checkStateReceipt` (`--resolves-via item:<id>`) work in one SQLite transaction with `PRAGMA foreign_keys = ON`. | Foreign keys cannot cross SQLite files. Every `item:<id>` pointer and `blocked_by` edge needs a new `store:<alias>:<id>` scheme, a store-registry file, and cross-file existence checks with no transactional integrity. |
| **Drain planning and lanes** | One `tillr drain loop` plans ready findings and ready bugs in one pass against one lane pool and one backend policy; blocker satisfaction (`blockerSatisfied` in [`why.go`](../../tools/tillr-drain/drain/why.go)) reads `d.LifecycleFor(kind).IsFinished(state)` in memory. | Requires running two `tillr drain loop` processes (splitting lane capacity and agent caps across processes) or teaching `tillr-drain` to attach multiple stores at once. |
| **Observatory, `why`, and `watch`** | One `events` sequence, one `tillr view waiting` list, and one `tillr-watch` source cover both kinds together. | Every CLI and web view (`waiting`, `why`, `status`, Item page links, `tillr-watch`) must federate across stores or force the owner to check two dashboards. |
| **Charter independence** | Kinds in one charter share store-wide `completes`, `retention`, `prerequisites`, and `hooks` program (receiving `--item`, from which `item show` gives `kind`). | Two kinds can use different `completes` modes (`"landing"` vs `"hook"`) without sharing a hook binary. |

### 3.2 Recommendation and designer specification (`D-CK1`, `D-CK2`)

**Recommendation ([`OQ-CK1`](#OQ-CK1)):** Choose **Option A** (per-kind lifecycles in one charter and one store). Findings and bugs in one product pipeline are one queue operated by one owner: splitting them across SQLite files breaks foreign-key integrity on `item_edges`, forces cross-DB pointer syntax into every `item:<id>` consumer, and splits `tillr view waiting` and lane capacity in half, all to avoid a `kinds` map in `Definition` and a `kind` column that [`D-IP1`](./tillr-idea-pipeline.md#6-decisions-and-open-questions) already calls for.

Under Option A, the designer specifies the charter and item mechanics as follows (**`D-CK1`** and **`D-CK2`**, designer decisions from an operator request, overturnable by the owner):

- **`Lifecycle` and `Definition.Kinds` (`D-CK1`).** Extract the per-lifecycle fields (`states`, `ready`, `blocked`, `closed`, `finished`, `transitions`) into a `Lifecycle` struct in [`tools/tillr/definition/definition.go`](../../tools/tillr/definition/definition.go), embedded on `Definition` for single-lifecycle charters and mapped by kind name under optional `kinds: map[string]Lifecycle` plus optional `default_kind: string`:
  - **Kind names:** lowercase ASCII slug (`^[a-z][a-z0-9-]*$`).
  - **Two authoring forms when `kinds` is present:**
    - *Explicit per-kind lifecycles* (the `finding` + `bug` case): top-level `states`, `ready`, `blocked`, `closed`, `finished`, and `transitions` are omitted, and every entry in `kinds` declares a complete valid `Lifecycle` (verified by the existing state, role, finished, and transition rules).
    - *Shared lifecycle with named kinds* (the `git-landing` `idea`/`design`/`build` case): top-level `Lifecycle` is present, and a `kinds.<k>` entry with empty `states` inherits the top-level `Lifecycle`; a `kinds.<k>` entry with non-empty `states` must declare its own complete `Lifecycle` (no partial field-by-field inheritance that hides which transitions apply).
  - **Cross-kind role consistency:** if two kinds in one charter share a state name `S`, `S` must carry the same role classification in both (`IsFinished(S)`, `S == Blocked`, and `S == Closed` agree). Distinct state names (such as `confirmed` on `finding` and `reported` on `bug`) stay disjoint, while shared states (`in-queue`, `blocked`, `closed`) keep one unambiguous meaning for store-wide projections and watch expressions (`item.state != charter.closed and item.state not in charter.finished` in [`tools/tillr/watch/tillr_source.go`](../../tools/tillr/watch/tillr_source.go)).
  - **Lookup helper:** `d.LifecycleFor(kind string) (Lifecycle, bool)` returns the top-level `Lifecycle` when `len(d.Kinds) == 0` (requiring `kind == ""`), resolves `kind == ""` via `d.DefaultKind` when set, and otherwise looks up `d.Kinds[kind]`.
- **`items.kind` and immutability (`D-CK2`).** Register `kind TEXT NOT NULL DEFAULT ''` on `items` via `store.RegisterColumn("item", "items", "kind", "TEXT NOT NULL DEFAULT ''")` in [`tools/tillr-item/item/schema.go`](../../tools/tillr-item/item/schema.go) (alongside `ItemDDL`, with `store.SchemaVersion` in [`tools/tillr/store/schema.go`](../../tools/tillr/store/schema.go) bumped if required by the migration contract):
  - `item.record.payload` accepts optional `"kind": "<name>"`.
  - When `len(d.Kinds) == 0`: `"kind"` must be omitted or `""` (refused as `kind-not-declared` otherwise).
  - When `len(d.Kinds) > 0`: if `"kind"` is omitted or `""`, `apply` resolves `d.DefaultKind` if non-empty and otherwise refuses with `kind-required`; an unknown kind refuses with `kind-unknown`.
  - Once an item exists, `apply` refuses changing a non-empty `items.kind` (`kind-immutable`), because changing kind mid-life could strand an item in a state or transition history invalid for the new kind; only assigning `d.DefaultKind` (or an explicit kind whose `Lifecycle` contains the item's current state) when upgrading a previously untyped row (`items.kind == ""`) is allowed.
  - `tillr item show`, `tillr item list` (adding optional `--kind <name>` filter), `tillr drain why`, `tillr view waiting`, the Item page, and `tillr-watch`'s `items` projection (`item.kind`) expose `kind`.
  - Every state check in `tillr-item`, `tillr-drain` (`plan.go`, `why.go`, `terminal_item.go`, `hook_completion.go`), `tillr-view`, and `tillr-watch` (`eval.go`, `silences.go`, `lifecycle.go`) resolves `lc, ok := d.LifecycleFor(item.Kind)` instead of reading flat `d.Ready`, `d.Blocked`, `d.Closed`, `d.Finished`, or `d.Allows`.
  - `tillr store definition --set` checks every existing row `(item_id, kind, state)` in `items`: each row's resolved `kind` (falling back to the new charter's `default_kind` when `items.kind == ""`) must exist and include `state` in its `states`, refusing the replacement before any write if any existing item would become orphaned.

---

## 4. The owner's moves: reason and actor on the move versus a decision record

When a person triages an unfinished item — approving a bug draft (`awaiting-owner -> approved`), holding a bug (`awaiting-owner -> on-hold`), sending a draft back for another pass (`awaiting-owner -> in-queue`), or dismissing a bug — an audit needs **who** made the move and **why**.

### 4.1 Evaluating the options

Today `decisions` in [`tools/tillr-item/item/decision.go`](../../tools/tillr-item/item/decision.go) stores standing policy rulings (`scope` in `global | subsystem | items`, `decided_by` in `owner | whale`, `status` in `active | superseded | revoked`), while `item.update` lifecycle events (`lifecycle_action`, `lifecycle_reason`, `lifecycle_by` in [`tools/tillr-item/item/lifecycle.go`](../../tools/tillr-item/item/lifecycle.go)) store per-item state-move provenance surfaced on the Item page by `readStateMoves` in [`tools/tillr-view/item_history.go`](../../tools/tillr-view/item_history.go).

| Dimension | Option A: `reason` and `by` directly on `tillr item state` | Option B: `decision.record` names the move; `state` requires `--decision <id>` | Option C: Option A on every move + optional `--decision <id>` link |
| :--- | :--- | :--- | :--- |
| **Concept fit** | Keeps one-time per-item triage moves on `item.update` (where `close`, `reopen`, finished entry/exit, and `blocked -> ready` already live) and keeps `decisions` pure as standing policy. | Mixes hundreds of routine per-bug triage moves into the standing `decisions` ledger, polluting policy queries (`active | superseded | revoked`) with one-shot state changes. | Separates routine move receipts (`--reason`, `--by`) from standing policy while letting a move cite a standing `decision_id` when one applies. |
| **Operator ergonomics and atomicity** | One atomic command and one transaction: `tillr item state --item X --state approved --by owner --reason "..."`. | Two commands (or a wrapper record) for every owner move; `decided_by` in `decision.go` is closed to `owner \| whale`, blocking named operators or external systems (`user:alice`, `operator`). | Same single command as Option A; `--decision` is optional. |
| **Audit read path** | `readStateMoves` in [`item_history.go`](../../tools/tillr-view/item_history.go) already reads `lifecycle_by` and `lifecycle_reason` from `item.update` payloads — it shows the actor and reason automatically once `SetStateWithReceipt` writes them. | Requires joining `events` against `decisions` and inventing supersession semantics when an item later moves back. | Same as Option A, plus optional link to `decisions.decision_id`. |

### 4.2 Recommendation and designer specification (`D-CK3`)

**Recommendation ([`OQ-CK2`](#OQ-CK2)):** Choose **Option A** now (store `reason` and `by` directly on the state move, accepted on every state change and required when the charter marks the transition `"decision": true`). Standing policy rulings remain in `tillr item decision`; if a consumer later wants a foreign-key link from a move to a standing policy ruling (Option C), optional `--decision <id>` can be added additively without changing charter syntax or existing move events.

Under Option A, the designer specifies the transition and CLI rules as follows (**`D-CK3`**, designer decision from an operator request, overturnable by the owner):

1. **Charter transition flag (`"decision": true`).** `Transition` in [`tools/tillr/definition/definition.go`](../../tools/tillr/definition/definition.go) gains `Decision bool` (`json:"decision,omitempty"`):
   ```json
   {"from": "awaiting-owner", "to": "approved", "decision": true}
   ```
   - Omitting `"decision"` defaults to `false`, preserving every existing charter file.
   - `lc.TransitionFor(from, to) (Transition, bool)` returns the matching declared transition.
   - Validation rule: a transition to `lc.Closed` stays governed by `tillr item close` (`checkClosedTransition` refuses `tillr item state --state <closed>` with `closed-use-close`); setting `"decision": true` on a transition into `lc.Closed` is refused at charter validation (`decision-on-closed-transition`) so charter authors do not write a dead requirement that `item close` bypasses (`item close` already requires `--reason` and records `--by`).
2. **Accept `--reason` and `--by` on every state change (`from != to`).** Remove the `flag-not-stored` refusal in `checkStateReceipt` ([`tools/tillr-item/item/finished_receipt.go`](../../tools/tillr-item/item/finished_receipt.go)) for `--reason` and `--by` on unfinished moves (including `to == lc.Blocked`, which previously accepted `--reason` while refusing `--by`):
   - Whenever `currentState != to` and either `receipt.Reason != ""`, `receipt.By != ""`, `tr.Decision`, `entering`, `leaving`, `releasing`, or `receipt.Waiter != ""`, `SetStateWithReceipt` records the move via `recordLifecycle` (`lifecycle_action`, `lifecycle_reason`, `lifecycle_by`) in the same `item.update` event, rather than plain `updateItemRow`.
   - On an idempotent no-op (`currentState == to` with no waiting-metadata update), no `item.update` event is written; passing `--reason` or `--by` on a same-state no-op where nothing is stored continues to refuse as `flag-not-stored` so provenance is never silently dropped.
   - `--resolves-via` remains restricted to entering a `finished` state (`flag-not-stored` on unfinished moves).
3. **Require `--reason` and `--by` when `"decision": true`.** When `lc.TransitionFor(currentState, to)` has `Decision: true`:
   - Blank `--reason` is refused (`item.UsageError`, exit `2`, rule `transition-reason-missing`) naming `currentState -> to`.
   - Blank `--by` is refused (`item.UsageError`, exit `2`, rule `transition-actor-missing`) naming `currentState -> to`. Unlike `tillr item close` (which defaults blank `--by` to `"unknown"`), a decision transition never substitutes `"unknown"`, because recording who decided is the point of marking the transition `"decision": true`.
   - Actor format: any non-blank trimmed string with no control characters (matching `lifecycle_by` today: `"owner"`, `"whale"`, `"operator"`, `"definition:<name>"`, or a consumer actor string such as `"owner:matt"`).
4. **Surface in `item show` and `item history`.** `readStateMoves` in [`tools/tillr-view/item_history.go`](../../tools/tillr-view/item_history.go) already reads `lifecycle_by` and `lifecycle_reason` from every `item.update` event, so the Item page timeline displays the actor and reason on unfinished moves with no schema change to `events`. `tillr item show` additionally projects `last_decision` (`{from, to, by, reason, at}`) from the latest decision move on the item (or omits it if none) so CLI consumers can read the latest ruling without scanning the event log.

---

## 5. Handing an item to the owner from `complete-item` without finishing it

When an agent finishes drafting a bug report, preparing a triage recommendation, or investigating an ambiguous finding, the agent's run is done, **the item itself is not finished**, and the next move belongs to the owner reviewing a concrete proposal.

### 5.1 Why exit `1` and `finished` states both fail this case

- **Hook exit `1` means refusal, not handoff.** In [`hook_completion.go`](../../tools/tillr-drain/drain/hook_completion.go), `CompleteItem` returns `Completed: false` on exit `1`, and `settleHook` calls `l.refuse(ctx, d, RefusalNoOp, ...)`, keeping the item in `ready` so the drain retries it and then faults it on repeated no-op refusals.
- **Naming a `finished` state lies about queue and dependency state.** Marking an unapproved draft `"finished"` satisfies `blocked_by` dependents prematurely, requires all criteria (including any `owner-only` criterion) to carry verdicts already, and blocks subsequent ordinary `tillr item state` transitions (`finished-item-locked` unless reopened or explicitly allowed out of `finished`).

### 5.2 Which unfinished states `complete-item` may target

Every waiting item must obey Principle 5 of [the charter direction](./tillr-core-and-git-landing.md#1-the-direction-in-the-owners-words): *"No item waits without a named next step and an actor who takes it."* Two shapes satisfy that rule when `complete-item` exits `0` naming an unfinished state:

- **Option A — Allow either `lc.Blocked` or any charter-declared unfinished waiting state (`!= lc.Ready`, `!= lc.Closed`), requiring `waiter` and `reason` (plus optional `proposal`).** Simple charters with no extra states park the item in `"blocked"` with `"waiter": "owner"`; multi-stage charters (such as `bug` in Product QA) can declare a dedicated state such as `"awaiting-owner"` so `in-queue -> awaiting-owner` is the hook's handoff and `awaiting-owner -> approved` / `awaiting-owner -> dismissed` (`"decision": true`) are exclusively the owner's decision moves, kept distinct from generic dependency blocks.
- **Option B — Allow only `lc.Blocked` (`"state": "<blocked>"`) on non-finished hook answers.** Every charter reuses its existing `blocked` role state with `"waiter": "owner"`, `"reason"`, and `"proposal"`, and no other unfinished state is accepted from `complete-item`.

**Recommendation ([`OQ-CK3`](#OQ-CK3)):** Choose **Option A**. It is a strict superset of Option B: a charter that declares no extra state uses `"state": "blocked", "waiter": "owner"` with zero charter edits, while a charter with `finding` and `bug` kinds can separate `"awaiting-owner"` (outgoing `"decision": true` moves) from `"blocked"` (dependency or environment holds).

### 5.3 Hook output, store state, planner hold, and crash recovery (`D-CK4`, `D-CK5`)

Under Option A, the designer specifies the `complete-item` contract and settlement path as follows (**`D-CK4`** and **`D-CK5`**, designer decisions from an operator request, overturnable by the owner):

1. **`complete-item` stdout JSON contract (`D-CK4`).** On exit `0`, `complete-item` prints one JSON object (strict unknown-field rejection preserved):
   ```json
   {
     "state": "awaiting-owner",
     "waiter": "owner",
     "reason": "Draft report ready; confirm reproduction severity before filing upstream",
     "proposal": "doc:proposals/bug-42.md"
   }
   ```
   `CompleteItem` validates two mutually exclusive exit-`0` modes against the item's kind lifecycle `lc`:
   - **Mode 1 — Finished settlement (`lc.IsFinished(answer.State)` and `answer.State != lc.Closed`):**
     - `reason` must be non-blank; `resolves_via` is optional; `waiter` and `proposal` must be empty (refused as `complete-item-fault` if set).
     - `CompleteItem` runs `tillr-item state --store <store> --item <id> --state <state> --require-verdicts --reason <reason> --by definition:<name> [--resolves-via <ref>] --settled-run <run_id>`.
   - **Mode 2 — Waiter/owner handoff (`!lc.IsFinished(answer.State)`):**
     - `answer.State` must be in `lc.States`, must **not** equal `lc.Ready` (preventing an immediate re-dispatch loop; `"state": "<ready>"` still faults as `complete-item-fault`), must **not** equal `lc.Closed` (`closed` belongs to `tillr item close`), and `lc.Allows(currentState, answer.State)` must hold. (If the transition is marked `"decision": true` in the charter, `CompleteItem` faults as `complete-item-fault`: decision moves belong to an explicit actor, not an automated completion hook.)
     - `answer.Waiter` is **required** and validated by `checkWaitingOn` (`"owner"` for an owner handoff; `"design"`, `"plan"`, or `"item:<id>"` are also valid waiters).
     - `answer.Reason` is **required** (non-blank summary of what the agent produced and what decision the owner owes).
     - `answer.Proposal` is **optional** and validated by the same typed pointer checker as `--resolves-via` (`doc:<repo-relative-path>`, `item:<existing-item-id>`, or `commit:<reachable-sha>`); `answer.ResolvesVia` must be empty on an unfinished handoff (`complete-item-fault` if set).
     - **Criterion verdicts on handoff:** `CompleteItem` **omits `--require-verdicts`**. Before printing the handoff JSON, the hook records `item.verdict` entries for whichever `verification-only` criteria the agent run just satisfied; unjudged `owner-only` criteria (or final disposition criteria) remain open until the item later enters a `finished` state, where `--require-verdicts` still enforces the invariant that no item finishes with unjudged criteria.
     - `CompleteItem` runs `tillr-item state --store <store> --item <id> --state <state> --waiter <waiter> --reason <reason> [--proposal <ref>] --by definition:<name> --settled-run <run_id>`.

2. **Store representation of `WaitingOn` and `--proposal` (`D-CK5`).**
   - `WaitingOn` in [`tools/tillr-item/item/waiting.go`](../../tools/tillr-item/item/waiting.go) gains `Proposal string` (`json:"proposal,omitempty"`).
   - `tillr item state` accepts `--waiter <waiter>` and optional `--proposal <kind:ref>` whenever the target state `to` is `lc.Blocked` or any other unfinished state (`to != lc.Ready && to != lc.Closed && !lc.IsFinished(to)`). Moving into a non-`ready`, non-`closed`, unfinished state other than `lc.Blocked` without `--waiter` remains allowed when manually parking an item in a passive state (such as `not-started`), whereas supplying `--waiter` requires a non-blank `--reason` (`blocked-reason-missing`) and stores `WaitingOn{Waiter, Reason, Proposal, Source}` in `items.waiting_on` (`Source: "hook"` when `--by` has prefix `definition:`, else `"manual"`).
   - When an item subsequently moves to `lc.Ready`, to `lc.Closed`, to any `lc.Finished` state, or to another unfinished state without `--waiter`, `SetStateWithReceipt` clears `items.waiting_on` (`''`), while preserving the handoff receipt permanently in `events` (`lifecycle_action: "handoff"`, `lifecycle_by`, `lifecycle_reason`, `lifecycle_proposal`, `lifecycle_run_id`).

3. **Planner, `why`, `waiting`, Observatory, and crash-recovery behavior (`D-CK5`).**
   - **Next pass (`plan.go` and `why.go`):** An item in `lc.Blocked` or another unfinished state with `WaitingOn != nil` is held (never dispatched, zero refusals recorded). In [`tools/tillr-drain/drain/why.go`](../../tools/tillr-drain/drain/why.go), when `WaitingOn != nil && WaitingOn.Waiter == "owner"` (whether `d.Reason == ReasonBlocked` or `d.Reason == ReasonNotReady`), the row classifies as `class: "needs you"`, `mover: "owner"`, `why: WaitingOn.Reason` (with `Proposal` surfaced on `WhyRow`, `waitingRow` in [`tools/tillr-view/waiting.go`](../../tools/tillr-view/waiting.go), `tillr item show`, and the Item page).
   - **Hook capability preflight (`hooks.go`):** `checkHooks` in [`tools/tillr-drain/drain/hooks.go`](../../tools/tillr-drain/drain/hooks.go) checks `--waiter`, `--proposal`, and `--settled-run` alongside `--require-verdicts`, `--reason`, `--by`, and `--resolves-via` in `tillr-item state -h` at loop startup; hook `describe` flags (`--item`, `--run`, `--store`, `--root`) do **not** change.
   - **Crash-recovery idempotence (`settleHook` in `hook_completion.go`):** Today `settleHook` calls `checkItemTerminal` before running `complete-item` so a crash between `CompleteItem` and `markExamined` does not re-run the hook. Because an owner handoff leaves the item **unfinished** (and the owner might even move the item back to `in-queue` before the loop restarts), `checkItemTerminal` alone would miss the already-committed handoff. Recording `--settled-run <run_id>` as `lifecycle_run_id` in the `item.update` event lets `settleHook` check `hasSettledRun(r, d.ItemID, d.RunRequest.RunID)` before invoking `complete-item`: if an `item.update` event on `d.ItemID` already carries `lifecycle_run_id == d.RunRequest.RunID`, `settleHook` skips re-running the hook and returns `laneOutcome{}` so the loop writes `drain.examined` and releases the lane cleanly.

---

## 6. What a charter file written today must change

**`D-CK6` (designer decision from an operator request, overturnable by the owner):** Keep `"version": 1` in `Definition` and make `kinds`, `default_kind`, and `transitions[].decision` optional additive fields, matching how optional `observability` was added to [`tools/tillr/definition/definition.go`](../../tools/tillr/definition/definition.go) under `version: 1`. Bumping `version` to `2` would force every existing single-kind charter file ([`tools/tillr/definition/landing.json`](../../tools/tillr/definition/landing.json), [`tools/tillr/examples/recorded-verdict/definition.json`](../../tools/tillr/examples/recorded-verdict/definition.json), and the worked example in [`docs/guide/charters.md`](../guide/charters.md)) to edit its version number and would change the SHA-256 digest stored in `meta['store.definition']` for byte-identical semantics.

### 6.1 Summary by capability

| Capability | What an existing `version: 1` charter JSON changes | What the consumer's CLI / hook calls change |
| :--- | :--- | :--- |
| **Keep current single-kind behavior** | **Nothing.** Existing `version: 1` files validate unchanged with the same SHA-256 digest. | **Nothing.** |
| **Record `--reason` and `--by` optionally on unfinished moves** | **Nothing.** `tillr item state` accepts and stores `--reason` and `--by` on any `from != to` move automatically. | Pass `--by <actor> --reason "<why>"` on `tillr item state`. |
| **Require `--reason` and `--by` on specific moves (`R3-2`)** | Add `"decision": true` to each owner-decision object in `transitions` (or `kinds.<kind>.transitions`). | `tillr item state` without both `--by` and `--reason` on those moves exits `2` (`transition-actor-missing` / `transition-reason-missing`). |
| **Hand an item + proposal to the owner via `"blocked"`** | **Nothing** if `{"from": "<ready>", "to": "<blocked>"}` and outgoing `"blocked"` transitions are already declared. | In `complete-item`, exit `0` printing `{"state": "blocked", "waiter": "owner", "reason": "...", "proposal": "doc:..."}` without judging `owner-only` criteria first. |
| **Hand an item + proposal to the owner via a dedicated state (under [`OQ-CK3`](#OQ-CK3) Option A)** | Add the unfinished state (for example `"awaiting-owner"`) to `states` (not `finished`), add `{"from": "<ready>", "to": "awaiting-owner"}`, and add outgoing `"decision": true` transitions from `"awaiting-owner"`. | In `complete-item`, exit `0` printing `{"state": "awaiting-owner", "waiter": "owner", "reason": "...", "proposal": "doc:..."}`. `describe` flags (`--item`, `--run`, `--store`, `--root`) are unchanged. |
| **Separate two item kinds in one store (`R3-1`, under [`OQ-CK1`](#OQ-CK1) Option A)** | Move top-level `states`, `ready`, `blocked`, `closed`, `finished`, and `transitions` into `"kinds": {"finding": {...}, "bug": {...}}` (and optionally set `"default_kind"` when upgrading an existing store with untyped rows via `tillr store definition --set`). | Include `"kind": "finding"` or `"kind": "bug"` in `item.record.payload` passed to `tillr item apply`. |

### 6.2 Concrete before-and-after charter diff

A `version: 1` hook charter written today (modeled on [`tools/tillr/examples/recorded-verdict/definition.json`](../../tools/tillr/examples/recorded-verdict/definition.json)) that previously squeezed findings and bugs into one flat state list changes as follows:

```diff
 {
-  "definition": "recorded-verdict",
+  "definition": "product-qa",
   "version": 1,
-  "states": ["in-queue", "blocked", "confirmed", "rejected", "closed"],
-  "ready": "in-queue",
-  "blocked": "blocked",
-  "closed": "closed",
-  "finished": ["confirmed", "rejected", "closed"],
-  "transitions": [
-    {"from": "in-queue", "to": "blocked"},
-    {"from": "in-queue", "to": "confirmed"},
-    {"from": "in-queue", "to": "rejected"},
-    {"from": "in-queue", "to": "closed"},
-    {"from": "blocked", "to": "in-queue"},
-    {"from": "blocked", "to": "closed"},
-    {"from": "closed", "to": "in-queue"}
-  ],
+  "kinds": {
+    "finding": {
+      "states": ["in-queue", "blocked", "confirmed", "rejected", "inconclusive", "closed"],
+      "ready": "in-queue",
+      "blocked": "blocked",
+      "closed": "closed",
+      "finished": ["confirmed", "rejected", "inconclusive", "closed"],
+      "transitions": [
+        {"from": "in-queue", "to": "blocked"},
+        {"from": "in-queue", "to": "confirmed"},
+        {"from": "in-queue", "to": "rejected"},
+        {"from": "in-queue", "to": "inconclusive"},
+        {"from": "in-queue", "to": "closed"},
+        {"from": "blocked", "to": "in-queue", "decision": true},
+        {"from": "blocked", "to": "confirmed", "decision": true},
+        {"from": "blocked", "to": "rejected", "decision": true},
+        {"from": "blocked", "to": "closed"},
+        {"from": "closed", "to": "in-queue", "decision": true}
+      ]
+    },
+    "bug": {
+      "states": ["in-queue", "blocked", "awaiting-owner", "approved", "reported", "dismissed", "closed"],
+      "ready": "in-queue",
+      "blocked": "blocked",
+      "closed": "closed",
+      "finished": ["reported", "dismissed", "closed"],
+      "transitions": [
+        {"from": "in-queue", "to": "blocked"},
+        {"from": "in-queue", "to": "awaiting-owner"},
+        {"from": "in-queue", "to": "closed"},
+        {"from": "awaiting-owner", "to": "approved", "decision": true},
+        {"from": "awaiting-owner", "to": "in-queue", "decision": true},
+        {"from": "awaiting-owner", "to": "dismissed", "decision": true},
+        {"from": "awaiting-owner", "to": "closed"},
+        {"from": "approved", "to": "reported", "decision": true},
+        {"from": "approved", "to": "in-queue", "decision": true},
+        {"from": "blocked", "to": "in-queue", "decision": true},
+        {"from": "blocked", "to": "closed"},
+        {"from": "closed", "to": "in-queue", "decision": true}
+      ]
+    }
+  },
   "prerequisites": [],
   "completes": "hook",
   "retention": "keep",
   "brief_template": "brief.md",
   "hooks": {
     "argv": ["python3", "hook.py"],
     "names": ["complete-item"]
   }
 }
```

Upgrading an already-populated store in place runs `tillr store definition --store .tillr/state.db --set charter.json`: if existing rows in `items` were created before `kind` existed (`items.kind = ''`), setting `"default_kind": "finding"` during the migration (and backfilling each bug's kind before removing `"default_kind"`) lets `--set` verify every existing item's `(kind, state)` without rewriting the store from scratch.

---

## 7. Open Questions

1. 💬 **OQ-CK1: Should two item kinds that link to each other live as per-kind lifecycles in one charter and store, or in two stores joined by cross-store pointers?**

   A consuming pipeline tracks findings and bugs that reference one another (`blocked_by` edges, `--resolves-via item:<id>`, `--waiter item:<id>`) while moving through distinct state graphs ([§3](#3-two-item-kinds-one-charter-versus-links-across-two-stores)).

   - **A — Per-kind lifecycles in one charter (`kinds` in `Definition` and `kind` on `items` in one store).** Keeps single-store foreign-key integrity on `item_edges`, existing `item:<id>` pointers, single-pass `tillr drain` scheduling, one `tillr view waiting` surface, and alignment with [`D-IP1`](./tillr-idea-pipeline.md#6-decisions-and-open-questions).
   - **B — Separate stores (one charter per store) with cross-store `store:<alias>:<id>` pointers and edges.** Allows each kind to pick an independent `completes` mode and hook binary, at the cost of cross-file pointer syntax, no SQLite foreign keys across stores, and split drain loops and views.

   <!-- vantage: question id=OQ-CK1 leaning="A — per-kind lifecycles in one charter and one store, preserving foreign-key edges, item:<id> pointers, single-pass planning, and alignment with D-IP1." -->

   _Leaning:_ A — per-kind lifecycles in one charter and one store, preserving foreign-key edges, `item:<id>` pointers, single-pass planning, and alignment with [`D-IP1`](./tillr-idea-pipeline.md#6-decisions-and-open-questions).

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-CK2: Should a person's decision on a move between unfinished states be recorded as `reason` and `by` on the state move, or as a `decision.record` that names the move?**

   Today `checkStateReceipt` refuses `--reason` and `--by` on unfinished moves as `flag-not-stored`, while `tillr item decision` stores standing policy rulings ([§4](#4-the-owners-moves-reason-and-actor-on-the-move-versus-a-decision-record)).

   - **A — Record `reason` and `by` directly on the state move (`tillr item state --reason --by`), accepted on every state change and required when the charter transition sets `"decision": true`.** Keeps one-command atomic moves on `item.update` (where `readStateMoves` already reads them) and reserves `tillr item decision` for standing policy rulings.
   - **B — Extend `decision.record` to name the state move it authorizes, and require `--decision <id>` on `tillr item state` when `"decision": true`.** Puts per-item triage moves into the `decisions` table alongside standing rulings, requiring two writes (or a combined command) and widening `decided_by` beyond `owner | whale`.
   - **C — Option A on every move plus an optional `--decision <id>` link on `tillr item state` when a standing policy ruling also governs the move.** Keeps routine moves lightweight while allowing foreign-key linkage to `decisions.decision_id`.

   <!-- vantage: question id=OQ-CK2 leaning="A — record reason and by directly on the state move and require them when transitions[].decision is true, keeping tillr item decision strictly for standing policy rulings." -->

   _Leaning:_ A — record `reason` and `by` directly on the state move and require them when `transitions[].decision` is `true`, keeping `tillr item decision` strictly for standing policy rulings (Option C's `--decision` flag remains an additive extension if later needed).

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-CK3: When `complete-item` hands an item and proposal to the owner without finishing it, may it target any charter-declared unfinished waiting state, or only the charter's `blocked` role state?**

   Both choices require `"waiter"` (such as `"owner"`) and a non-blank `"reason"` (plus optional `"proposal"` pointer), omit `--require-verdicts`, and record `--settled-run <run_id>` for crash recovery ([§5.2](#52-which-unfinished-states-complete-item-may-target)).

   - **A — Either `lc.Blocked` or any charter-declared unfinished state (`!= lc.Ready`, `!= lc.Closed`) allowed from the item's current state.** Simple charters use `"blocked"` with zero state additions, while multi-stage charters can declare `"awaiting-owner"` so proposal review has its own outgoing `"decision": true` moves distinct from dependency holds.
   - **B — Only `lc.Blocked`.** Every non-finished `complete-item` handoff parks the item in the charter's `blocked` state with `"waiter": "owner"`.

   <!-- vantage: question id=OQ-CK3 leaning="A — allow either blocked or any charter-declared unfinished non-ready, non-closed state when waiter and reason are supplied, so simple charters need no new state and multi-stage charters can separate awaiting-owner from generic blocks." -->

   _Leaning:_ A — allow either `blocked` or any charter-declared unfinished non-`ready`, non-`closed` state when `waiter` and `reason` are supplied, so simple charters need no new state and multi-stage charters can separate `awaiting-owner` from generic blocks.

   **Answer:**

   > _(empty — fill in when decided)_

---

## 8. Decision Ledger

Every row below is a **designer decision (from an operator request) that the owner may overturn**, contingent on the owner's rulings on [`OQ-CK1`](#OQ-CK1), [`OQ-CK2`](#OQ-CK2), and [`OQ-CK3`](#OQ-CK3). None is an owner ruling.

| ID | Decision | Origin | Date | Governing section |
| :--- | :--- | :--- | :--- | :--- |
| `D-CK1` | Extract `Lifecycle` in `tools/tillr/definition/definition.go`, add optional `kinds` and `default_kind` with `LifecycleFor(kind)` lookup, and enforce cross-kind role consistency when two kinds share a state name | Designer decision (from an operator request; owner may overturn; contingent on [`OQ-CK1`](#OQ-CK1)) | 2026-11-26 | [§3.2](#32-recommendation-and-designer-specification-d-ck1-d-ck2) |
| `D-CK2` | Add `items.kind` (`TEXT NOT NULL DEFAULT ''`), validate `item.record.payload.kind` against `Definition.Kinds` on `apply` and `store definition --set`, forbid mutating a non-empty kind (`kind-immutable`), and thread `LifecycleFor(kind)` through item, drain, view, and watch readers | Designer decision (from an operator request; owner may overturn; contingent on [`OQ-CK1`](#OQ-CK1)) | 2026-11-26 | [§3.2](#32-recommendation-and-designer-specification-d-ck1-d-ck2) |
| `D-CK3` | Add `Decision bool` (`"decision": true`) on charter `Transition` (refused on transitions into `closed`), accept and record `--reason` and `--by` on every state change (`from != to`), and require both non-blank on `"decision": true` moves (`transition-reason-missing`, `transition-actor-missing`) | Designer decision (from an operator request; owner may overturn; contingent on [`OQ-CK2`](#OQ-CK2)) | 2026-11-26 | [§4.2](#42-recommendation-and-designer-specification-d-ck3) |
| `D-CK4` | Extend `complete-item` exit-`0` answers so an unfinished target state (`!= Ready`, `!= Closed`, non-decision transition) with required `waiter` and `reason` and optional `proposal` parks the item without `--require-verdicts` | Designer decision (from an operator request; owner may overturn; contingent on [`OQ-CK3`](#OQ-CK3)) | 2026-11-26 | [§5.3](#53-hook-output-store-state-planner-hold-and-crash-recovery-d-ck4-d-ck5) |
| `D-CK5` | Add `Proposal` to `WaitingOn` (`--proposal <kind:ref>`), record `--settled-run <run_id>` (`lifecycle_run_id`) so `settleHook` is crash-idempotent on non-terminal hook handoffs, and classify `waiter == "owner"` waiting states as `"needs you"` across `why`, `waiting`, `item show`, and the Item page | Designer decision (from an operator request; owner may overturn; contingent on [`OQ-CK3`](#OQ-CK3)) | 2026-11-26 | [§5.3](#53-hook-output-store-state-planner-hold-and-crash-recovery-d-ck4-d-ck5) |
| `D-CK6` | Keep charter `"version": 1` with optional additive `kinds`, `default_kind`, and `transitions[].decision` fields so existing `version: 1` charters (`landing.json`, `recorded-verdict`) keep byte-identical validity and digests | Designer decision (from an operator request; owner may overturn) | 2026-11-26 | [§6](#6-what-a-charter-file-written-today-must-change) |

---

## 9. Build items

Per [`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21), none of the four build items below is filed as a runnable item block until the owner rules the open question(s) it depends on. Each slice touches `tools/tillr*` and therefore runs `just pack-sync` in the same commit.

### Build item 1: Charter `Lifecycle`, `kinds`, `default_kind`, and `transitions[].decision` in `tools/tillr/definition`

- **Depends on:** [`OQ-CK1`](#OQ-CK1) and [`OQ-CK2`](#OQ-CK2).
- **Paths:** `tools/tillr/definition/`, `tools/tillr/watch/`, `tools/tillr/README.md`, `yolo-packs/tillr/`.
- **Deliverable:**
  - **`c1`:** `Definition` in [`tools/tillr/definition/definition.go`](../../tools/tillr/definition/definition.go) extracts `Lifecycle` (`States`, `Ready`, `Blocked`, `Closed`, `Finished`, `Transitions`), supports optional `kinds` (`^[a-z][a-z0-9-]*$`) and `default_kind` under `version: 1` (`D-CK1`, `D-CK6`), exposes `LifecycleFor(kind)` and `TransitionFor(from, to)`, enforces cross-kind role consistency on shared state names, and keeps `landing.json` and `examples/recorded-verdict/definition.json` valid unchanged.
  - **`c2`:** `Transition` supports optional `"decision": true` (`Decision bool`), preserved across JSON round-trips and refused when `to` equals the lifecycle's `closed` state (`decision-on-closed-transition`, `D-CK3`).
  - **`c3`:** `tillr watch lint` (`tools/tillr/watch/lint_charter.go`) validates single-kind and multi-kind charters and exposes unioned charter facts without breaking existing watch rules; unit tests in `tools/tillr/definition/definition_test.go` and `tools/tillr/watch/lint_test.go` pin valid and rejected cases, and `just pack-sync` is clean.

### Build item 2: `items.kind`, per-kind state validation, and `store definition --set` checks in `tools/tillr-store` and `tools/tillr-item`

- **Depends on:** Build item 1 and [`OQ-CK1`](#OQ-CK1).
- **Paths:** `tools/tillr-store/`, `tools/tillr-item/`, `tools/tillr/store/`, `tools/tillr/watch/`, `yolo-packs/tillr/`.
- **Deliverable:**
  - **`c1`:** Store schema adds `items.kind TEXT NOT NULL DEFAULT ''` via `store.RegisterColumn` in [`tools/tillr-item/item/schema.go`](../../tools/tillr-item/item/schema.go) (migrating existing stores cleanly); `tillr item apply` validates `payload.kind` against `Definition.Kinds` and `DefaultKind` (`kind-not-declared`, `kind-required`, `kind-unknown`) and refuses changing a non-empty kind (`kind-immutable`, `D-CK2`).
  - **`c2`:** `tillr item state`, `close`, `reopen`, `show`, and `list` (adding `--kind <name>`) resolve the item's lifecycle via `d.LifecycleFor(kind)` so an item of one kind is refused entry into another kind's states; `tillr store definition --set` refuses a replacement charter that orphans any existing `(kind, state)` row.
  - **`c3`:** `tillr-watch` item terminal checks and `items` projection (`tools/tillr/watch/eval.go`, `silences.go`, `lifecycle.go`, `tillr_source.go`) evaluate `d.LifecycleFor(kind)` and expose `item.kind`; targeted tests in `tools/tillr-item/` and `tools/tillr/watch/` cover two-kind stores (`finding` + `bug` joined by `blocked_by` and `item:<id>` pointers), and `just pack-sync` is clean.

### Build item 3: Actor and reason on unfinished moves, `"decision": true` enforcement, and `--waiter` / `--proposal` / `--settled-run` on `tillr-item state`

- **Depends on:** Build item 2, [`OQ-CK2`](#OQ-CK2), and [`OQ-CK3`](#OQ-CK3).
- **Paths:** `tools/tillr-item/`, `tools/tillr-view/`, `yolo-packs/tillr/`.
- **Deliverable:**
  - **`c1`:** `checkStateReceipt` and `SetStateWithReceipt` in [`tools/tillr-item/item/`](../../tools/tillr-item/item/) accept and record `--reason` and `--by` on every state change (`from != to`, including `to == Blocked`), still refusing them as `flag-not-stored` on same-state no-ops where nothing is stored, and require non-blank `--reason` (`transition-reason-missing`) and non-blank `--by` (`transition-actor-missing`) whenever `lc.TransitionFor(from, to).Decision` is `true` (`D-CK3`).
  - **`c2`:** `WaitingOn` gains `Proposal` (`--proposal <doc|item|commit:ref>`) and `--settled-run <run_id>` (`lifecycle_run_id`), allowed on `lc.Blocked` and (per [`OQ-CK3`](#OQ-CK3)) unfinished waiting states when `--waiter` and `--reason` are provided, clearing `items.waiting_on` when the item leaves the waiting state (`D-CK5`).
  - **`c3`:** `tillr item show` projects `last_decision` and `waiting_on.proposal`, and `readStateMoves` (`tools/tillr-view/item_history.go`) plus `waiting.go` surface unfinished-move actors/reasons and proposals; tests in `tools/tillr-item/` and `tools/tillr-view/` verify both, and `just pack-sync` is clean.

### Build item 4: `complete-item` owner-proposal handoffs and per-kind planning in `tools/tillr-drain`

- **Depends on:** Build item 2, Build item 3, and [`OQ-CK3`](#OQ-CK3).
- **Paths:** `tools/tillr-drain/`, `tools/tillr/examples/recorded-verdict/`, `docs/guide/charters.md`, `yolo-packs/tillr/`.
- **Deliverable:**
  - **`c1`:** `CompleteItem` and `settleHook` in [`tools/tillr-drain/drain/hook_completion.go`](../../tools/tillr-drain/drain/hook_completion.go) accept exit-`0` owner/waiter handoff answers (`state`, `waiter`, `reason`, optional `proposal`) without `--require-verdicts`, still fault on `lc.Ready`, `lc.Closed`, `"decision": true` transitions, missing `waiter`/`reason`, or `resolves_via` on unfinished states (`D-CK4`), and skip re-running `complete-item` on crash recovery when `lifecycle_run_id` already matches the run (`D-CK5`).
  - **`c2`:** `plan.go`, `why.go`, `terminal_item.go`, and `hooks.go` in [`tools/tillr-drain/drain/`](../../tools/tillr-drain/drain/) resolve each item's `LifecycleFor(kind)`, hold handoff items as `"needs you"` (`mover: "owner"`) with zero refusals until moved back to `lc.Ready`, and check `--waiter`, `--proposal`, and `--settled-run` in `checkHooks`.
  - **`c3`:** End-to-end tests in `tools/tillr-drain/drain/hook_completion_test.go` cover a two-kind charter (`finding` + `bug`), a `complete-item` owner-proposal handoff with an unjudged `owner-only` criterion, crash-recovery idempotence before `drain.examined`, and the owner's `"decision": true` follow-up move; [`docs/guide/charters.md`](../guide/charters.md) documents `kinds`, `"decision": true`, and handoff answers, and `just pack-sync` is clean.
