---
title: "Count distinct conflict IDs in drain-repeated-item-conflicts so re-raising a conflict keeps the store truthful"
author: "Agent"
date: 2026-11-26
status: in-review
stage: DECIDED
stage-since: 2026-11-26
tags: [tillr, watch, drain, conflict, retry, alert, design]
summary: "Amend D-TW51 backward-only so drain-repeated-item-conflicts adds distinct_by = \"conflict_id\" on its count_in_window rule, keeping the append-only item.conflict transition log truthful when tillr drain retry re-raises an existing conflict with a revised instruction while counting each conflict_id once in 24 hours."
next: "Build the single follow-on slice adding distinct_by = \"conflict_id\" to config/tillr/watch/drain-repeated-item-conflicts.toml and covering the tillr drain retry re-raise sequence in tools/tillr-watch/repeated_item_conflicts_rule_test.go (with just pack-sync and commit-body explanation); the owner may review D-TW-RIC1."
depends-on:
  - ../tillr-watch.md
  - ../tillr-watch.md#D-TW41
  - ../tillr-watch.md#D-TW42
  - ../tillr-watch.md#D-TW51
  - ../tillr-watch.md#D-TW53
  - ../tillr-watch.md#D-TW73
  - ../tillr-watch.md#D-TW76
  - ../tillr-watch.md#D-TW83
  - ../tillr-watch.md#D-TW85
  - ./decision-record-files.md#R-TW-DR1
  - ./decision-record-files.md#R-TW-DR2
  - ./decision-record-files.md#D-TW-DR1
  - ../landing-principles.md#15-land-the-smallest-functional-change
related:
  - ../escalation-ladder.md#13-decision-ledger
  - ../tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9
  - ./windowed-ack-reopen.md#D-TW-WAR1
vantage:
  status-chip: true
---

# Count distinct `conflict_id`s in `drain-repeated-item-conflicts` so re-raising a conflict keeps the store truthful

**Status:** 2026-11-26, verified at `1ec65ee497ff`. [`D-TW-RIC1`](#D-TW-RIC1) is not built yet (`rg -n 'distinct_by' config/tillr/watch/drain-repeated-item-conflicts.toml` returns nothing). Per owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) and [`D-TW-DR1`](./decision-record-files.md#D-TW-DR1), this standalone record answers `conflict-run-item-request-a03ffe48bf-a1-n1` for `item-request-a03ffe48bf` and amends [`D-TW51`](../tillr-watch.md#D-TW51) (and [`§5.8`](../tillr-watch.md#58-repeated-itemconflict-events-on-one-item-d-tw51) of [`../tillr-watch.md`](../tillr-watch.md)) backward-only with zero edits to [`../tillr-watch.md`](../tillr-watch.md).

> **In short.** `item.conflict` is an append-only state-transition stream (`raised -> awaiting -> owner -> raised`), so delivering a revised instruction with `tillr drain retry` on an owner-held design conflict must append a second `status: "raised"` transition under the existing `conflict_id` to re-queue the design seat truthfully. Adding `distinct_by = "conflict_id"` to `drain-repeated-item-conflicts` makes the watch rule count distinct conflicts on an open item rather than raw `raised` transitions on the same conflict.

**Why it matters.** On 2026-11-25 a repository consuming `tillr` delivered a revised instruction with `tillr drain retry`, which appended a second `status == "raised"` `item.conflict` event carrying the same `conflict_id`, and `drain-repeated-item-conflicts` opened a warning alert counting that single conflict as two ([§1.1](#11-the-2026-11-25-retry-re-raise-sequence-and-the-definition-conflict)).

**The shape.** Leave `RecordConflict` and `authorizeDesignRetry` unchanged so the store records every state transition truthfully, add `distinct_by = "conflict_id"` to `[rule.when]` in [`config/tillr/watch/drain-repeated-item-conflicts.toml`](../../../tools/tillr-watch/rules/drain-repeated-item-conflicts.toml) using the existing `count_in_window` `distinct_by` support ([`D-TW76`](../tillr-watch.md#D-TW76)), and test the full `AuthorizeRetry` sequence against a scratch store ([§2](#2-truthful-transition-recording-and-distinct_by-on-drain-repeated-item-conflicts-d-tw-ric1), [§3](#3-answering-conflict-run-item-request-a03ffe48bf-a1-n1-and-build-scope-d-tw-ric1)).

**Cost.** One line in [`config/tillr/watch/drain-repeated-item-conflicts.toml`](../../../tools/tillr-watch/rules/drain-repeated-item-conflicts.toml) (plus header comment), one scratch-store regression test in [`tools/tillr-watch/repeated_item_conflicts_rule_test.go`](../../../tools/tillr-watch/repeated_item_conflicts_rule_test.go) mirrored with `just pack-sync`, and zero changes to `tillr-item`, `tillr-drain`, or the `tillr/watch` engine.

**Start at [§2](#2-truthful-transition-recording-and-distinct_by-on-drain-repeated-item-conflicts-d-tw-ric1)** for why keeping the store truthful selects `distinct_by = "conflict_id"` over suppressing `RecordConflict`, and [§3](#3-answering-conflict-run-item-request-a03ffe48bf-a1-n1-and-build-scope-d-tw-ric1) for the criterion reconciliation and `"Change nothing else"` build boundary.

**Needs your ruling:** None. Standing owner rulings [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), and [`D-TW73`](../tillr-watch.md#D-TW73) are preserved; [`D-TW-RIC1`](#D-TW-RIC1) comes from the 2026-11-25 filed request and the designer and may be overturned by the owner.

**Reads with:** [`../tillr-watch.md`](../tillr-watch.md) (baseline rule [`D-TW51`](../tillr-watch.md#D-TW51) and `distinct_by` [`D-TW76`](../tillr-watch.md#D-TW76)), [`./decision-record-files.md`](./decision-record-files.md) (one-file-per-record rule), and [`../tillr-judge-and-lifecycle.md`](../tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9) (design conflict transitions).

---

## 1. Sequence, Definition Conflict, and Authority Boundary

### 1.1 The 2026-11-25 retry re-raise sequence and the definition conflict

On 2026-11-25 a repository consuming `tillr` reported that delivering a revised instruction with `tillr drain retry` recorded the same `item.conflict` (`conflict_id`) a second time and caused `drain-repeated-item-conflicts` to count it as two. Tracing the sequence directly in the source at `1ec65ee497ff` reproduces every event:

1. **Initial raise (`status = "raised"`):** When worker run `R_1` ends blocked on an obstacle routed to `design`, `(*Loop).reportedBlock` in [`tools/tillr-drain/drain/blocked.go`](../../../tools/tillr-drain/drain/blocked.go) constructs `Conflict{ConflictID: "conflict-" + R_1, Status: "raised", WaitingOn: "design"}` and calls `RecordConflict` in [`tools/tillr-item/item/conflict.go`](../../../tools/tillr-item/item/conflict.go), appending the first `item.conflict` event for `"conflict-" + R_1`.
2. **Design claim and owner objection (`status = "awaiting"` then `status = "owner"`):** Claiming design run `D_1` appends `status: "awaiting", design_run_id: D_1` under `"conflict-" + R_1`. When `D_1` objects with `NEXT-ACTOR: owner`, `(*Loop).reportedBlock` appends `status: "owner", waiting_on: "owner", design_run_id: D_1` under `"conflict-" + R_1`.
3. **Delivering a revised instruction via `tillr drain retry` (`status = "raised"` on the same `conflict_id`):** When the operator or owner runs `tillr drain retry --item <id> --run <D_1> --by <who> --why <revised instruction>`, `AuthorizeRetry` in [`tools/tillr-drain/drain/retries.go`](../../../tools/tillr-drain/drain/retries.go) delegates to `authorizeDesignRetry` in [`tools/tillr-drain/drain/design_retry.go`](../../../tools/tillr-drain/drain/design_retry.go). `authorizeDesignRetry` appends the revised instruction to `c.Text`, sets `c.Status = "raised"`, `c.WaitingOn = "design"`, `c.DesignRunID = ""`, calls `item.RecordConflict(tx, c, at)` under the **same** `c.ConflictID` (`"conflict-" + R_1`), and appends `drain.retry.design` with `ReraisesConflict: c.ConflictID`.
4. **Other single-conflict re-raises (`status = "raised"` on the same `conflict_id`):** In addition to `authorizeDesignRetry`, `(*Loop).reportedBlock` (`designMissingActor` in [`tools/tillr-drain/drain/blocked.go`](../../../tools/tillr-drain/drain/blocked.go)), `designCauseBlockRemoved` in [`tools/tillr-drain/drain/loop.go`](../../../tools/tillr-drain/drain/loop.go), `(*Loop).settleUnansweredDesignRuns` in [`tools/tillr-drain/drain/design_unanswered.go`](../../../tools/tillr-drain/drain/design_unanswered.go), and unlaunched-claim rollback in [`tools/tillr-drain/drain/unlaunched_claim.go`](../../../tools/tillr-drain/drain/unlaunched_claim.go) also append `status = "raised"` transitions under an existing `conflict_id` when an unanswered design attempt returns to `design`.
5. **Double-counting in `drain-repeated-item-conflicts`:** Under [`D-TW51`](../tillr-watch.md#D-TW51), [`config/tillr/watch/drain-repeated-item-conflicts.toml`](../../../tools/tillr-watch/rules/drain-repeated-item-conflicts.toml) sets `pattern = "count_in_window"`, `group_by = "item_id"`, `match = 'event.status == "raised" and item.state != charter.closed and item.state not in charter.finished'`, `threshold = 2`, and `window = "24h"` with no `distinct_by`. `(*condition).observe` in [`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go) retains both `status == "raised"` transitions for `"conflict-" + R_1` (`len(c.events) == 2 >= 2`) and opens an alert on a single unresolved conflict.

The saved words of `item-request-a03ffe48bf` state an explicit choice between two options:

> one conflict id is recorded once, or the rule counts distinct conflict ids; choose the one that keeps the store truthful and say which in the commit body.

Run `run-item-request-a03ffe48bf-a1-n1` raised `OBSTACLE: definition` (`conflict-run-item-request-a03ffe48bf-a1-n1`) on criterion `c5` for two reasons:

- [`D-TW51`](../tillr-watch.md#D-TW51) in [`../tillr-watch.md`](../tillr-watch.md) specifies counting `item.conflict` events with `status == "raised"` without `distinct_by = "conflict_id"`; and
- filer-supplied criterion `c5` (`"tillr drain retry delivering a revised instruction does not record a second item.conflict with the same conflict id, and the repeated-conflicts watch rule counts one; a test covers it."`) pre-selected the first branch of the saved request's disjunction before checking how `ReadConflicts` and `authorizeDesignRetry` store conflict state.

### 1.2 Authority boundary: standing owner rulings versus designer decision

Four standing owner rulings govern this record without modification:

- [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`2026-11-25`, `"ok, so save that"`): every new decision record about `tillr-watch` lives in its own standalone file under `docs/design/tillr-watch/`, with zero edits to [`../tillr-watch.md`](../tillr-watch.md).
- [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2) (`2026-11-16`, `"Explicitly, we NEVER want fallbacks anywhere in this project."`): no fallback path or dual conflict schema is introduced.
- [`OQ-TW2`](../tillr-watch.md#OQ-TW2) (`2026-11-21`, `"We don't cover issues. We use this to find the issues and fix them."`): `drain-repeated-item-conflicts` executes no remedy.
- [`D-TW73`](../tillr-watch.md#D-TW73) (`2026-11-22`, `"I never want alerts for expected conditions."`): re-raising one existing conflict with `tillr drain retry` is how an owner hold hands revised guidance back to `design`, not two conflicts on the item, so the rule must not fire on it.

Everything settled below — [`D-TW-RIC1`](#D-TW-RIC1), including choosing `distinct_by = "conflict_id"` over suppressing `RecordConflict`, amending [`D-TW51`](../tillr-watch.md#D-TW51) backward-only, reconciling `c5` with the saved request words, and bounding the single-commit build — is an **operator-request and designer decision (`2026-11-26`), not an owner ruling, and the owner may overturn any part of it**.

---

## 2. Truthful Transition Recording and `distinct_by` on `drain-repeated-item-conflicts` ([`D-TW-RIC1`](#D-TW-RIC1))

*Non-owner decision from the 2026-11-25 filed request and the designer; the owner may overturn any part of this section.*

### 2.1 Why keeping the store truthful selects `distinct_by = "conflict_id"`

In [`tools/tillr-item/item/conflict.go`](../../../tools/tillr-item/item/conflict.go) (`D3` in [`../escalation-ladder.md`](../escalation-ladder.md#13-decision-ledger)), `item.conflict` is an append-only state-transition log, and `events` is the sole persistence for conflicts (`rg -n 'CREATE TABLE.*conflict' tools/` returns nothing):

- `ReadConflicts` scans `SELECT payload FROM events WHERE kind = 'item.conflict' ORDER BY seq` and overwrites `result[c.ItemID]` with each event's payload so callers read the latest transition of the item's conflict.
- `RecordConflict` validates `c.ConflictID != ""`, skips byte-identical no-op repeats (`reflect.DeepEqual(last, c)`), updates `items.state` and `waiting_on` (`Waiter: c.WaitingOn`, `Source: "conflict:" + c.ConflictID`) in the same transaction, and appends the `item.conflict` event.

Suppressing the second `item.conflict` event in `authorizeDesignRetry` ([`tools/tillr-drain/drain/design_retry.go`](../../../tools/tillr-drain/drain/design_retry.go)) would make the store lie and break dispatch:

1. Without appending `status: "raised", waiting_on: "design", design_run_id: ""`, `ReadConflicts` would continue returning `status: "owner", waiting_on: "owner", design_run_id: <D_1>`.
2. On the next planner pass, `(*planner).conflictDecision` in [`tools/tillr-drain/drain/blocked.go`](../../../tools/tillr-drain/drain/blocked.go) would hit `case c.WaitingOn == "owner":` and hold the item at `ReasonConflictOwner` instead of dispatching `design` (breaking `TestARetryNamingTheUnansweredDesignRunRaisesItsConflictOnceMore` in [`tools/tillr-drain/drain/design_retry_test.go`](../../../tools/tillr-drain/drain/design_retry_test.go)).
3. `c.Text` in `conflict.json` staged for the next design run would omit the operator's revised instruction (`req.Why`), and the append-only event log would hide a real `owner -> raised` transition.

Minting a fresh `conflict_id` in `authorizeDesignRetry` would be equally untruthful: the conflict still originates from worker run `c.RunID` and has never been answered (`drain.retry.design` records `reraises_conflict: c.ConflictID`), and a fabricated second `conflict_id` would still make `drain-repeated-item-conflicts` count two.

By contrast, counting distinct `conflict_id` values on `drain-repeated-item-conflicts` keeps every `item.conflict` transition in `events` intact while aligning the watch rule with Condition 3 of [`../tillr-watch.md` §5.4](../tillr-watch.md#54-the-ten-drain-conditions-d-tw39) (*"One item bouncing between a build obstacle and a design answer"*):

- **Zero engine or schema work:** `count_in_window` already accepts `distinct_by` in `loadDistinctBy` ([`tools/tillr/watch/rules.go`](../../../tools/tillr/watch/rules.go)), `lintDistinctBy` ([`tools/tillr/watch/lint_rules.go`](../../../tools/tillr/watch/lint_rules.go)), `definitionHash` and `(*condition).observe` ([`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go), [`D-TW76`](../tillr-watch.md#D-TW76)).
- **Guaranteed non-empty key (`Skipped == 0`):** `RecordConflict` refuses empty `ConflictID` at write time, so every `item.conflict` payload carries a non-empty string `conflict_id`.
- **Exact deduplication and 24-hour window semantics:** When `distinct_by = "conflict_id"` is set, `(*condition).observe` deletes any earlier retained observation with `prev.distinct == conflict_id` (`slices.DeleteFunc`) before appending the newer `status == "raised"` observation. Re-raising `conflict-1` via `tillr drain retry` (or after an unanswered design turn) leaves `len(c.events) == 1` (`< threshold = 2`, refreshing `conflict-1`'s 24-hour window timestamp to its latest `raised` event and preserving post-acknowledgement counting in [`./windowed-ack-reopen.md#D-TW-WAR1`](./windowed-ack-reopen.md#D-TW-WAR1)), whereas a second worker run raising a distinct `conflict-2` (`"conflict-" + R_2`) produces `len(c.events) == 2 >= 2` and opens the foreground alert.

### 2.2 Updated rule in `config/tillr/watch/drain-repeated-item-conflicts.toml`

[`D-TW-RIC1`](#D-TW-RIC1) amends [`D-TW51`](../tillr-watch.md#D-TW51) backward-only by adding `distinct_by = "conflict_id"` to `[rule.when]` in [`config/tillr/watch/drain-repeated-item-conflicts.toml`](../../../tools/tillr-watch/rules/drain-repeated-item-conflicts.toml) (and noting the 2026-11-25 retry re-raise case in the header comment), leaving `[rule.reads]`, `group_by`, `pattern`, `match`, `threshold`, `window`, `[rule.guidance]`, and `[rule.delivery]` unchanged:

```toml
# On 2026-11-21, several items each went to the design seat two or three times
# when the request's words conflicted with the code or with a design; on
# 2026-11-22, counting unfiltered item.conflict status transitions woke the
# operator on single conflicts going raised -> awaiting, and restarting the
# watcher emitted 44 alerts including already-closed and already-landed items;
# on 2026-11-25, delivering a revised instruction with tillr drain retry
# re-raised the same conflict_id (owner -> raised) and counted one conflict as
# two. Count distinct raised conflict_id values on open items (via the items and
# charter projections) and warn at two or more in 24 hours.
[[rule]]
id = "drain-repeated-item-conflicts"
scope = "core"
severity = "warning"

[rule.reads]
events = ["item.conflict"]
projections = ["items", "charter"]

[rule.when]
group_by = "item_id"
pattern = "count_in_window"
match = 'event.status == "raised" and item.state != charter.closed and item.state not in charter.finished'
distinct_by = "conflict_id"
threshold = 2
window = "24h"

[rule.guidance]
summary = "Item {{subject}} raised {{count}} item.conflict events in the last 24 hours."
check = ["Read the obstacle text for {{subject}} (for instance with tillr drain why {{subject}})."]
do = ["Check whether the request's own words are the cause (for example a request that tells a build worker to edit a design document, or to state a cause it cannot establish)."]

[rule.delivery]
receiver = "foreground"
```

Because `definitionHash` in [`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go) includes `DistinctBy`, adding `distinct_by = "conflict_id"` updates `drain-repeated-item-conflicts`'s rule definition digest cleanly on reload without touching stored episodes of other rules.

---

## 3. Answering `conflict-run-item-request-a03ffe48bf-a1-n1` and Build Scope ([`D-TW-RIC1`](#D-TW-RIC1))

### 3.1 Reconciling Criterion 5 (`c5`) with the saved request words

The saved words of `item-request-a03ffe48bf` ask the build to choose whichever of the two mutually exclusive remedies keeps the store truthful — *"one conflict id is recorded once, or the rule counts distinct conflict ids; choose the one that keeps the store truthful and say which in the commit body"* — while filer-supplied criterion `c5` (`"tillr drain retry delivering a revised instruction does not record a second item.conflict with the same conflict id, and the repeated-conflicts watch rule counts one; a test covers it."`) stated the first remedy before the source was checked.

Answering `conflict-run-item-request-a03ffe48bf-a1-n1`, [`D-TW-RIC1`](#D-TW-RIC1) settles that choice in favor of the saved request's second remedy (`distinct_by = "conflict_id"`). For the returned `plan`, `work`, and `verify` runs of `item-request-a03ffe48bf`:

1. `authorizeDesignRetry` ([`tools/tillr-drain/drain/design_retry.go`](../../../tools/tillr-drain/drain/design_retry.go)) and `RecordConflict` ([`tools/tillr-item/item/conflict.go`](../../../tools/tillr-item/item/conflict.go)) **keep** recording the `status: "raised"` transition under the existing `conflict_id` when `tillr drain retry` delivers a revised instruction; they are not modified.
2. Criterion `c5` is met under this design answer when:
   - [`config/tillr/watch/drain-repeated-item-conflicts.toml`](../../../tools/tillr-watch/rules/drain-repeated-item-conflicts.toml) sets `distinct_by = "conflict_id"` ([§2.2](#22-updated-rule-in-configtillrwatchdrain-repeated-item-conflictstoml)) so `drain-repeated-item-conflicts` counts **one** (`len(c.events) == 1`, zero alerts opened) when `tillr drain retry` re-raises the same `conflict_id` with a revised instruction, and opens at two when a second distinct `conflict_id` is raised on the same open item within 24 hours;
   - a test in [`tools/tillr-watch/repeated_item_conflicts_rule_test.go`](../../../tools/tillr-watch/repeated_item_conflicts_rule_test.go) reproduces that exact `drain.AuthorizeRetry` (`tillr drain retry`) sequence end-to-end on a `t.TempDir()` scratch store ([`D-TW41`](../tillr-watch.md#D-TW41)); and
   - the build commit body states that counting distinct `conflict_id`s in `drain-repeated-item-conflicts` was chosen over suppressing the second `item.conflict` event so the append-only conflict transition log stays truthful.

### 3.2 Single-commit build boundary under `"Change nothing else"`

Under [`D-TW42`](../tillr-watch.md#D-TW42) and [`landing-principles.md` §15](../landing-principles.md#15-land-the-smallest-functional-change), `"Change nothing else"` bounds the build commit to:

1. **[`config/tillr/watch/drain-repeated-item-conflicts.toml`](../../../tools/tillr-watch/rules/drain-repeated-item-conflicts.toml):** Add `distinct_by = "conflict_id"` under `[rule.when]` and update the header comment ([§2.2](#22-updated-rule-in-configtillrwatchdrain-repeated-item-conflictstoml)), leaving every other key and rule file untouched. (While `config/tillr/watch/` remains the active rules directory ahead of the unbuilt shipped-rule move in [`D-TW83`](../tillr-watch.md#D-TW83)–[`D-TW88`](../tillr-watch.md#D-TW88), the rule stays in `config/tillr/watch/drain-repeated-item-conflicts.toml` and will carry `distinct_by = "conflict_id"` when embedded under [`D-TW85`](../tillr-watch.md#D-TW85).)
2. **[`tools/tillr-watch/repeated_item_conflicts_rule_test.go`](../../../tools/tillr-watch/repeated_item_conflicts_rule_test.go) and `just pack-sync` mirror:** Keep all existing `TestDrainRepeatedItemConflictsRule` assertions passing (which already use distinct `conflict_id`s for two-conflict alert cases) and add an end-to-end test driving a real `store.Store` (`t.TempDir()`) through:
   - worker conflict `raised` (`item.RecordConflict`), design run `awaiting`, terminal design `run.Record`, and design objection `owner` (`DesignRunID` set);
   - `drain.AuthorizeRetry` delivering a revised instruction (`Why: "revised instruction"`), asserting that `events` holds two `status == "raised"` `item.conflict` rows with the identical `conflict_id` while `watch.Evaluator` on `drain-repeated-item-conflicts` stays quiet (`0` notices, `0` episodes) — and likewise stays quiet across non-retry same-`conflict_id` `raised` transitions; and
   - a subsequent distinct `conflict_id` (`status == "raised"`) on the same open item within 24 hours opening `1` foreground notice with `Summary` reporting `2`.
   Run `just pack-sync` in the same commit so `yolo-packs/tillr/files/local/share/tillr/tillr-watch/repeated_item_conflicts_rule_test.go` stays in sync.
3. **Out of scope (untouched):** `tools/tillr-item/`, `tools/tillr-drain/`, `tools/tillr/watch/` (including `lint_workflow.go`, where `watch-lint-unbounded-return` checks `drain.refusal` / run-allocation rules rather than `item.conflict`), other `.toml` rules in `config/tillr/watch/`, and `[rule.guidance]` wording.

---

## 4. Rejected Alternatives

Each alternative below was considered by the designer and set aside (the owner may overturn any of these dispositions except [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), and [`D-TW73`](../tillr-watch.md#D-TW73)):

| Alternative | Why it fails | Verdict |
| :--- | :--- | :--- |
| **Omit `item.RecordConflict` in `authorizeDesignRetry` so one `conflict_id` is recorded as `"raised"` only once** | Leaves `ReadConflicts` at `status: "owner", waiting_on: "owner"`, holding the item on `ReasonConflictOwner` instead of re-dispatching `design`, dropping the revised instruction from `conflict.json`, breaking `TestARetryNamingTheUnansweredDesignRunRaisesItsConflictOnceMore`, and hiding a real state transition from `events`. | Rejected. |
| **Mint a new `conflict_id` in `authorizeDesignRetry` on `tillr drain retry`** | Falsifies conflict identity in `events` (the conflict still comes from `c.RunID` and `drain.retry.design` records `reraises_conflict: c.ConflictID`) and still causes `drain-repeated-item-conflicts` to count two on a single conflict. | Rejected. |
| **Add a boolean `reraised` field to `item.Conflict` and filter `not (event.reraised == true)` in `match`** | Requires cross-module schema and producer edits across `tillr-item` and five re-raise call sites in `tillr-drain` when `distinct_by = "conflict_id"` already expresses conflict identity directly on existing events; also fails to refresh the conflict's 24h window timestamp on re-raise. | Rejected. |
| **Edit `docs/design/tillr-watch.md` inline to amend `D-TW51`** | Violates owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`"ok, so save that"`) and Clause 1 of the `documents` gate check in [`tools/gate/change_common.go`](../../../tools/gate/change_common.go). | Rejected. |

---

## 5. Open Questions

None.

---

## 6. Decision Ledger

[`D-TW-RIC1`](#D-TW-RIC1) records a decision from the 2026-11-25 filed request (`item-request-a03ffe48bf`) and the designer answering `conflict-run-item-request-a03ffe48bf-a1-n1`; it is **not** an owner ruling, and the owner may overturn any part of it. `Built` is checked against `1ec65ee497ff`.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-TW-RIC1"></a>**[D-TW-RIC1](#D-TW-RIC1)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** answering `conflict-run-item-request-a03ffe48bf-a1-n1` for `item-request-a03ffe48bf`, amend [`D-TW51`](../tillr-watch.md#D-TW51) (and [`§5.8`](../tillr-watch.md#58-repeated-itemconflict-events-on-one-item-d-tw51) of [`../tillr-watch.md`](../tillr-watch.md)) backward-only by choosing the saved request's second option — adding `distinct_by = "conflict_id"` under `[rule.when]` in [`config/tillr/watch/drain-repeated-item-conflicts.toml`](../../../tools/tillr-watch/rules/drain-repeated-item-conflicts.toml) (`count_in_window`, [`D-TW76`](../tillr-watch.md#D-TW76)) while leaving `RecordConflict` ([`tools/tillr-item/item/conflict.go`](../../../tools/tillr-item/item/conflict.go)) and `authorizeDesignRetry` ([`tools/tillr-drain/drain/design_retry.go`](../../../tools/tillr-drain/drain/design_retry.go)) untouched so `tillr drain retry` continues appending the truthful `status: "raised"` transition under the existing `conflict_id`. Criterion `c5` of `item-request-a03ffe48bf` is satisfied when `drain-repeated-item-conflicts.toml` sets `distinct_by = "conflict_id"`, [`tools/tillr-watch/repeated_item_conflicts_rule_test.go`](../../../tools/tillr-watch/repeated_item_conflicts_rule_test.go) (synced via `just pack-sync`) covers the `AuthorizeRetry` same-`conflict_id` re-raise counting `1` (`0` alerts) and a second distinct `conflict_id` opening at `2` on a `t.TempDir()` scratch store, and the build commit body states why `distinct_by = "conflict_id"` was chosen to keep the store truthful. | 2026-11-26 | [§2](#2-truthful-transition-recording-and-distinct_by-on-drain-repeated-item-conflicts-d-tw-ric1), [§3](#3-answering-conflict-run-item-request-a03ffe48bf-a1-n1-and-build-scope-d-tw-ric1), [§4](#4-rejected-alternatives) | no |
