---
title: "An acknowledged windowed alert counts as a new instance only when post-acknowledgement events satisfy the window condition"
author: "Agent"
date: 2026-11-25
status: in-review
stage: DECIDED
stage-since: 2026-11-25
tags: [tillr, watch, alerting, drain, gate, design]
summary: "Record the 2026-11-24 fast-gate-median-slow re-delivery measurement and settle when an acknowledged alert from a windowed rule (count_in_window and the rolling-median form) counts as a new instance: only once the condition holds after excluding every event at or before acked_seq, keeping the episode acknowledged in tillr watch status in the meantime."
next: "Build the single follow-on slice in Section 4 covering tools/tillr-drain/drain/gate_timing.go, tools/tillr/watch/, config/tillr/watch/fast-gate-slow.toml, just pack-sync, and scratch-store tests; the owner may review D-TW-WAR1 through D-TW-WAR3."
depends-on:
  - ../tillr-watch.md
  - ../tillr-watch.md#D-TW13
  - ../tillr-watch.md#D-TW16
  - ../tillr-watch.md#D-TW34
  - ../tillr-watch.md#D-TW43
  - ../tillr-watch.md#D-TW66
  - ../tillr-watch.md#D-TW72
  - ../tillr-watch-alert-workflow.md#R-AW4
  - ../tillr-watch-alert-workflow.md#D-AW11
  - ./decision-record-files.md#R-TW-DR1
  - ./decision-record-files.md#D-TW-DR1
  - ../landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it
  - ../landing-principles.md#15-land-the-smallest-functional-change
related:
  - ../tillr-utility-contracts/tillr-watch.md
vantage:
  status-chip: true
---

# An acknowledged windowed alert counts as a new instance only when post-acknowledgement events satisfy the window condition

**Status:** 2026-11-25, verified at `d6ddd74b4037`. Nothing specified in [`D-TW-WAR1`](#D-TW-WAR1) through [`D-TW-WAR3`](#D-TW-WAR3) is built yet (`rg -n 'window_oldest_seq|median20_oldest_seq' tools config` returns nothing, and `(episode).reopenCause` in [`tools/tillr/watch/eval.go`](../../../tools/tillr/watch/eval.go) returns `"new-instance"` on every acknowledged non-`lag_behind` episode whenever `newest.Seq > ackedSeq`). Per owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) and [`D-TW-DR1`](./decision-record-files.md#D-TW-DR1), this standalone record amends [`D-AW11`](../tillr-watch-alert-workflow.md#D-AW11), [`D-TW16`](../tillr-watch.md#D-TW16), [`D-TW43`](../tillr-watch.md#D-TW43), and [`D-TW66`](../tillr-watch.md#D-TW66) backward-only without editing [`../tillr-watch.md`](../tillr-watch.md) or [`../tillr-watch-alert-workflow.md`](../tillr-watch-alert-workflow.md).

> **In short.** On a rule that judges a window of events, comparing only `newest.Seq > acked_seq` treats each new arrival as a brand-new incident while pre-acknowledgement events still carry the window. Excluding every event at or before `acked_seq` when testing whether a new instance has formed keeps an acknowledged episode quiet in `tillr watch wait` and visible as `state=acknowledged` in `tillr watch status` until post-acknowledgement events alone satisfy the rule — `oldest_seq > acked_seq` with `match` holding on a fixed-$N$-event median window, and at least `threshold` post-acknowledgement counted observations on `count_in_window`.

**Why it matters.** On 2026-11-24 (`08:15:37` to `08:16:05 ET`), `fast-gate-median-slow` was acknowledged after its fix had landed at `07:58:59 ET`, then reopened 28 seconds later with `cause = "new-instance"` when one gate on an older base finished, having already been delivered and acknowledged twice within two minutes ([§1.1](#11-the-2026-11-24-measurement-and-why-reopencause-re-delivers-on-every-event)).

**The shape.** One post-acknowledgement window test in `(episode).reopenCause` ([§2.1](#21-weighing-the-candidate-across-fixed-count-and-time-bounded-windows-d-tw-war1)), paired with `median20_oldest_seq` on `drain.fast-gate-timing` and optional `window_oldest_seq` on event-reading `predicate_match` rules ([§2.2](#22-producer-boundary-and-rule-schema-for-the-median-form-d-tw-war2)).

**Cost.** One follow-on build slice across `tools/tillr-drain/drain/gate_timing.go`, `tools/tillr/watch/`, `config/tillr/watch/fast-gate-slow.toml`, `just pack-sync`, and scratch-store tests, with zero SQLite DDL migrations ([§4.1](#41-the-single-follow-on-build-slice-d-tw-war3)).

**Start at [§2](#2-when-an-acknowledged-windowed-alert-counts-as-a-new-instance-d-tw-war1-d-tw-war2)** for the reopening rule and schema, [§3](#3-what-the-operator-sees-in-the-meantime) for interim `status` and `wait` behavior, and [§4](#4-follow-on-build-slice-and-out-of-scope-candidate-note-d-tw-war3) for the build slice and out-of-scope candidate note.

**Needs your ruling:** None. Standing owner rulings [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), and [`R-AW4`](../tillr-watch-alert-workflow.md#R-AW4) are preserved; [`D-TW-WAR1`](#D-TW-WAR1) through [`D-TW-WAR3`](#D-TW-WAR3) are operator-request and designer decisions that the owner may overturn.

**Reads with:** [`../tillr-watch.md`](../tillr-watch.md) ([§5.1](../tillr-watch.md#51-declarative-rule-schema-d-tw4-oq-tw5), [§5.5](../tillr-watch.md#55-fast-gate-time-two-rules-and-the-event-they-read-d-tw43), [§6.1](../tillr-watch.md#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5)), [`../tillr-watch-alert-workflow.md`](../tillr-watch-alert-workflow.md) ([§5.1](../tillr-watch-alert-workflow.md#51-single-instance-watermarks-on-ack-and-settle-versus-active-silence-suppression)), [`./decision-record-files.md`](./decision-record-files.md), and [`../tillr-utility-contracts/tillr-watch.md`](../tillr-utility-contracts/tillr-watch.md).

---

## 1. Measurement, Root Cause, and Provenance

### 1.1 The 2026-11-24 measurement and why `reopenCause` re-delivers on every event

**Measurement (2026-11-24, ET):**

- **`07:58:59 ET`:** The fix that `fast-gate-median-slow` ([`config/tillr/watch/fast-gate-slow.toml`](../../../tools/tillr-watch/rules/fast-gate-slow.toml), `event.median20_wall_ms > 90000`) was tracked to landed on trunk, while the 20-run rolling window still held slow fast-gate runs recorded before the fix.
- **`08:15:37 ET`:** `fast-gate-median-slow` was acknowledged with a written cause stating that its tracked fix had landed at `07:58:59` and the 20-run window still held runs from before it. By that point the same alert had already been delivered and acknowledged twice within two minutes.
- **`08:16:04 ET`:** One more fast gate finished in `232 s` on a candidate whose base predated the `07:58:59` fix.
- **`08:16:05 ET`:** One second later, `tillr-watch` reopened the episode with `cause = "new-instance"` and delivered the alert again. With fast gates finishing every few minutes, each new gate completion re-delivered the alert until 20 post-acknowledgement gates had run, with nothing new to act on.

Two code shapes in `tillr` produce this behavior at `d6ddd74b4037`:

1. **Single-sequence comparison in `(episode).reopenCause` ([`tools/tillr/watch/eval.go`](../../../tools/tillr/watch/eval.go)):** For every `state == "acknowledged"` episode whose pattern is not `lag_behind` and whose `condition_fingerprint` is unchanged, `reopenCause` checks only `if newest.Seq > ackedSeq { return "new-instance" }`. Both `(changes).reconcile` in [`tools/tillr/watch/eval.go`](../../../tools/tillr/watch/eval.go) and `(*memoryState).reconcileRule` in [`tools/tillr/watch/source.go`](../../../tools/tillr/watch/source.go) pass only `newest = conditionNewest(...)` to `reopenCause`.
2. **Rolling-median encoding versus `count_in_window` state:**
   - **`fast-gate-median-slow` (the median form):** `medianFastGateTiming` in [`tools/tillr-drain/drain/gate_timing.go`](../../../tools/tillr-drain/drain/gate_timing.go) computes the 20-run median from the current run plus `SELECT json_extract(payload, '$.wall_ms') FROM events INDEXED BY events_by_kind WHERE kind = ? ORDER BY seq DESC LIMIT 19` and publishes only `median20_wall_ms` on `drain.fast-gate-timing`. In [`config/tillr/watch/fast-gate-slow.toml`](../../../tools/tillr-watch/rules/fast-gate-slow.toml), `fast-gate-median-slow` is a `predicate_match` rule over `event.median20_wall_ms > 90000`, so `tillr-watch` sees each new event's `seq > acked_seq` without knowing that up to 19 of the 20 runs inside `median20_wall_ms` came from `seq <= acked_seq`.
   - **`count_in_window` (six rules in [`config/tillr/watch/`](../../../tools/tillr-watch/rules/)):** `(*condition).observe` in [`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go) keeps every counted observation inside `[now - window, now]` in `c.events` (in strictly increasing `Seq` order, deduplicated by `distinct_by` when set). Yet because `reopenCause` inspects only `newest.Seq > ackedSeq`, the first post-acknowledgement event (`1` new observation) reopens a `threshold = K` rule while `K - 1` pre-acknowledgement observations still supply the threshold.

### 1.2 Authority boundary: standing owner rulings versus designer decisions

Four verbatim owner rulings bound this design without being altered by it:

- [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`2026-11-25`, `"ok, so save that"`): every new decision record about `tillr-watch` goes in its own file under `docs/design/tillr-watch/`, with zero edits to [`../tillr-watch.md`](../tillr-watch.md).
- [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2) (`2026-11-16`, `"Explicitly, we NEVER want fallbacks anywhere in this project."`): no silent fallback path.
- [`OQ-TW2`](../tillr-watch.md#OQ-TW2) (`2026-11-21`, `"We don't cover issues. We use this to find the issues and fix them."`): `tillr-watch` executes no automatic remedy.
- [`R-AW4`](../tillr-watch-alert-workflow.md#R-AW4) (`2026-11-23`, `"yes, you should be able to acknowledge it, but it shouldn't ignore it like you go and fix it. And it's only you should be able to acknowledge one instance of that event"`): acknowledging an alert covers one instance only and never ignores a later instance.

Everything settled below — [`D-TW-WAR1`](#D-TW-WAR1) through [`D-TW-WAR3`](#D-TW-WAR3), including weighing the candidate named in the 2026-11-25 operator request (which the request explicitly marks `"not a ruling"`), the post-acknowledgement window rule, `median20_oldest_seq`, `window_oldest_seq`, interim `status` / `wait` behavior, rejected alternatives, and the follow-on build slice — is an **operator-request and designer decision (`2026-11-25`), not an owner ruling, and the owner may overturn any part of it**.

---

## 2. When an Acknowledged Windowed Alert Counts as a New Instance ([`D-TW-WAR1`](#D-TW-WAR1), [`D-TW-WAR2`](#D-TW-WAR2))

*Non-owner decisions from the 2026-11-25 operator request and the designer; the owner may overturn any part of this section.*

### 2.1 Weighing the candidate across fixed-count and time-bounded windows ([`D-TW-WAR1`](#D-TW-WAR1))

The operator request names one candidate to weigh: *an acknowledged windowed alert counts as a new instance only once the window holds no event from before the acknowledgement and the condition still holds.* Testing that statement against both windowed shapes shows why it holds verbatim for fixed-$N$-event windows and must be stated in terms of post-acknowledgement observations for time-bounded `count_in_window` rules:

1. **Fixed-$N$-event rolling windows (`fast-gate-median-slow`):** A 20-event median requires $N = 20$ samples and reads the newest 20 events regardless of wall-clock spacing; each new `drain.fast-gate-timing` event evicts one older event from the 20-event sample. Excluding every event with `seq <= acked_seq` leaves a complete 20-event sample **if and only if** the 20-event window holds zero events with `seq <= acked_seq` (`oldest_seq > acked_seq`). While `oldest_seq <= acked_seq`, the median still aggregates runs from the already-acknowledged window; once `oldest_seq > acked_seq` and `event.median20_wall_ms > 90000` still holds, 20 post-acknowledgement runs alone sustain a median above 90 seconds — a genuine new instance. **The candidate is adopted directly for fixed-$N$-event windows.**
2. **Time-bounded `count_in_window` (`threshold = K`, duration `window = W`, optional `distinct_by`):** Unlike a fixed-$N$-event sample, `[now - W, now]` evicts pre-acknowledgement observations by **wall-clock age**, not as new observations arrive. Four of the six `count_in_window` rules in [`config/tillr/watch/`](../../../tools/tillr-watch/rules/) use multi-hour windows (`gate-drain-stalls`: `threshold = 3`, `window = "4h"`; `gate-repeated-stall`: `threshold = 3`, `window = "24h"`; `drain-repeated-verifier-refusals`: `threshold = 5`, `window = "24h"`; `drain-repeated-item-conflicts`: `threshold = 2`, `window = "24h"`). If `gate-drain-stalls` opens on 3 stalled gates at `10:00` (`seq = s_1, s_2, s_3`), is acknowledged (`acked_seq = s_3`), and a new stall burst produces 3 fresh stalled gates at `10:20` (`s_4, s_5, s_6 > s_3`), `s_1` remains inside `[now - 4h, now]` (`c.events[0].Seq = s_1 <= acked_seq`) until `14:00`. Requiring `c.events[0].Seq > acked_seq` would mute that complete 3-stall post-acknowledgement burst for `3h 40m` — or lose it completely on `gate-repeated-stall` if a passing gate matches `clear_when` before 24 hours elapse — violating [`R-AW4`](../tillr-watch-alert-workflow.md#R-AW4). Conversely, reopening on `s_4` alone (`1 < K`) treats `s_2` and `s_3` from the acknowledged instance as two-thirds of a new instance.

Both forms share **one underlying principle ([`D-TW-WAR1`](#D-TW-WAR1), amending [`D-AW11`](../tillr-watch-alert-workflow.md#D-AW11) backward-only):** when `condition_fingerprint` equals the acknowledged fingerprint, an `acknowledged` episode of a windowed rule counts as `"new-instance"` **if and only if the rule's window condition holds after excluding every event at or before `acked_seq` (`seq <= acked_seq`)**:

| Windowed rule form | Condition after excluding `seq <= acked_seq` | Counts as `"new-instance"` when |
| :--- | :--- | :--- |
| **Fixed-$N$-event aggregate** (`predicate_match` declaring `window_oldest_seq`, including `fast-gate-median-slow`) | A full $N$-event post-acknowledgement window exists iff all $N$ events in the window have `seq > acked_seq` | `newest.Seq > acked_seq`, `oldest_seq > acked_seq`, and `match` holds on `newest` *(adopts the candidate verbatim)* |
| **`count_in_window`** (`threshold = K`, `window = W`, optional `distinct_by`) | Counted observations inside `[now - W, now]` (after `distinct_by` deduplication) with `seq > acked_seq` | At least `K` counted observations in `c.events` have `Seq > acked_seq` (`post_ack_count >= threshold`) |

**Exact equivalence with `distinct_by` (`D-TW13`, `D-TW76`):** In `(*condition).observe` ([`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go)), `c.events` is kept in strictly ascending `Seq` order and, when `distinct_by` is set, retains only the newest observation `e` (`slices.DeleteFunc` followed by `append`) for each distinct string key inside `[now - W, now]`. A distinct key's retained observation in `c.events` has `Seq > acked_seq` if and only if at least one matching in-window event with `Seq > acked_seq` carried that key. Therefore `len(c.events) - sort.Search(len(c.events), func(i int) bool { return c.events[i].Seq > acked_seq })` equals the exact post-acknowledgement count both with and without `distinct_by`, with zero persisted counters ([`D-TW13`](../tillr-watch.md#D-TW13)). If `condition_fingerprint` changes (`newest.fingerprint != ackedFP`), `reopenCause` continues to return `"new-instance"` immediately regardless of window position ([`D-TW32`](../tillr-watch.md#D-TW32), [`D-AW11`](../tillr-watch-alert-workflow.md#D-AW11)).

### 2.2 Producer boundary and rule schema for the median form ([`D-TW-WAR2`](#D-TW-WAR2))

To let `tillr-watch` test `oldest_seq > acked_seq` on fixed-$N$-event aggregates while keeping `tools/tillr/watch` domain-neutral (importing neither `tillr-drain` nor `tillr-item`), [`D-TW-WAR2`](#D-TW-WAR2) amends [`D-TW16`](../tillr-watch.md#D-TW16), [`D-TW43`](../tillr-watch.md#D-TW43), and [`D-TW66`](../tillr-watch.md#D-TW66) backward-only across four seams:

1. **Producer (`medianFastGateTiming` in [`tools/tillr-drain/drain/gate_timing.go`](../../../tools/tillr-drain/drain/gate_timing.go)):**
   - Add `Median20OldestSeq *int64` (`json:"median20_oldest_seq,omitempty"`) to `fastGateTimingPayload`.
   - Change `medianFastGateTiming` to scan `seq` alongside `wall_ms` from the same indexed query inside `appendFastGateTiming`'s `WriteTx`:

     ```sql
     SELECT seq, json_extract(payload, '$.wall_ms')
     FROM events INDEXED BY events_by_kind
     WHERE kind = ? ORDER BY seq DESC LIMIT 19
     ```

   - Because `events_by_kind` is `(kind, seq)`, reading `seq` adds zero table lookups or extra queries. When fewer than 19 prior rows exist (`len(window) < 20`), both `Median20WallMS` and `Median20OldestSeq` are `nil` and omitted. When 19 prior rows exist (`len(window) == 20`), `Median20OldestSeq` is the `seq` of the 19th prior row — the minimum `seq` across the 20-run sample `{19 prior rows} U {current row}`.
2. **Rule schema, loader, and linter ([`tools/tillr/watch/rules.go`](../../../tools/tillr/watch/rules.go), [`tools/tillr/watch/lint_rules.go`](../../../tools/tillr/watch/lint_rules.go), [`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go)):**
   - Add `WindowOldestSeq string` (`window_oldest_seq`) to `When` on event-reading, non-`after` `predicate_match` rules only.
   - Both `LoadRules` and `tillr watch lint` (`CodeWatchLintRuleSchema`) refuse `window_oldest_seq`:
     - as an unknown key on `count_in_window`, `consecutive_count`, `alternating_pair`, `lag_behind`, and `duration_without_event`;
     - on projection-only `predicate_match` (`len(reads.events) == 0`: `"projection-only predicate_match must omit window_oldest_seq"`);
     - when `after` is set on `predicate_match` (`"cannot combine window_oldest_seq with after on predicate_match"`);
     - when empty, prefixed with `"event."`, equal to `"global"`, failing `validEventPath`, or naming a reserved envelope column (`"seq"`, `"ts"`, `"kind"`, `"seat"`, `"object_type"`, `"object_id"`, `"revision"`).
   - Add `WindowOldestSeq string` (`json:"window_oldest_seq,omitempty"`) to `ruleDefinitionDigestInput` so omitting `window_oldest_seq` leaves every existing rule's `definitionHash` byte-identical under [`D-TW66`](../tillr-watch.md#D-TW66) while setting or changing `window_oldest_seq` updates `definitionHash` and `ruleEqual`. `ruleCacheable` stays `true` because `window_oldest_seq` reads only the cached `Observation` payload.
3. **Evaluator (`(episode).reopenCause` in [`tools/tillr/watch/eval.go`](../../../tools/tillr/watch/eval.go), called from `(changes).reconcile` in `eval.go` and `(*memoryState).reconcileRule` in [`tools/tillr/watch/source.go`](../../../tools/tillr/watch/source.go)):**
   - Extend `(episode).reopenCause(r Rule, now time.Time, c *condition, newest observation, lagPair string) string` to receive `c`.
   - On `ep.state == "acknowledged"`, after confirming `newest.fingerprint == ackedFP` and handling `LagBehind`, compute `ackedSeq` (`ep.ackedSeq.Int64` when `ep.ackedSeq.Valid`, else `ep.ackedWatermark` when `> 0`, else `ep.last`):
     - If `newest.Seq <= ackedSeq`, return `""`.
     - If `r.When.Pattern == CountInWindow`: compute `postAck` as the count of entries in `c.events` with `Seq > ackedSeq`; return `"new-instance"` if `c != nil && postAck >= r.When.Threshold`, otherwise `""`.
     - If `r.When.Pattern == PredicateMatch && r.When.WindowOldestSeq != ""`: look up `newest.lookup(r.When.WindowOldestSeq)` (when `newest.lookup != nil`) and parse it with `integer(...)` (`int`, `int64`, `json.Number` in [`tools/tillr/watch/expression.go`](../../../tools/tillr/watch/expression.go)). If present as an integer satisfying `1 <= oldestSeq && oldestSeq <= newest.Seq` and `oldestSeq <= ackedSeq`, return `""`; otherwise (`oldestSeq > ackedSeq`, or absent / non-integer / out-of-range value) return `"new-instance"`.
   - **Why absent or invalid `window_oldest_seq` wakes without altering `c.observe` or `Skipped`:** Suppressing a reopen when `newest.Seq > ackedSeq` requires positive proof on `newest` that `1 <= oldest_seq <= acked_seq`. If a matching event lacks that proof (such as an event emitted before `tillr-drain` restarted onto the new binary or a malformed producer payload), `c.observe` continues evaluating `match` and `clear_when` normally (`RuleResult.Skipped` stays reserved for `group_by` and `for_each` routing skips reported by `daemon.go`), and `reopenCause` returns `"new-instance"` rather than muting an unproved window ([`R-AW4`](../tillr-watch-alert-workflow.md#R-AW4), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2)).
4. **Shipped rule update ([`config/tillr/watch/fast-gate-slow.toml`](../../../tools/tillr-watch/rules/fast-gate-slow.toml)):**
   - Add `window_oldest_seq = "median20_oldest_seq"` to `[rule.when]` of `fast-gate-median-slow`:

     ```toml
     [rule.when]
     group_by = "global"
     pattern = "predicate_match"
     match = 'event.median20_wall_ms > 90000'
     clear_when = 'event.median20_wall_ms <= 90000'
     window = "24h"
     window_oldest_seq = "median20_oldest_seq"
     ```

---

## 3. What the Operator Sees in the Meantime

Between `tillr watch ack <alert-id> --by <actor> [--note <text>]` (recording `acked_seq = s_ack`) and the end of the acknowledged window, every `tillr-watch` surface behaves as follows:

1. **Episode row and event log ([`tools/tillr/watch/eval.go`](../../../tools/tillr/watch/eval.go), [`tools/tillr/watch/source.go`](../../../tools/tillr/watch/source.go)):**
   - On each subsequent matching event with `seq > last_observed_seq` while `reopenCause` returns `""` (`1 <= oldest_seq <= acked_seq` on `fast-gate-median-slow`, or `1 <= post_ack_count < threshold` on `count_in_window`), reconciliation updates `last_observed_seq`, `last_observed_at`, `observation_count = observation_count + 1`, `condition_fingerprint`, and `variables_json` in place.
   - `state` remains `'acknowledged'`; `acked_by`, `acked_note`, `acked_at`, `acked_seq`, `acked_fingerprint`, and `settlement_state` (`'investigating'` after `ack`, or `'investigated'` after `tillr watch investigate`) are untouched.
   - Zero `watch.alert.reopened` events are appended to `events`.
2. **`tillr watch status` ([`tools/tillr/watch/status.go`](../../../tools/tillr/watch/status.go), [`tools/tillr-watch/status.go`](../../../tools/tillr-watch/status.go)):**
   - `ReadStatus` remains read-only ([`D-TW34`](../tillr-watch.md#D-TW34)) and queries `WHERE state <> 'resolved'`.
   - The alert **stays acknowledged and visible** in both text and `--json` output, showing `state=acknowledged`, `by="<actor>"`, and `observed=<count>` incrementing as in-window gates or events arrive while `owner ... pending=0` confirms nothing new is queued for wake-up:

     ```text
     alert <alert-id> rule=fast-gate-median-slow subject="global" state=acknowledged severity=warning opened=<rfc3339> observed=4 vars={} by="operator-session"
     owner foreground watcher=attached since=<rfc3339> last_wait=<rfc3339> pending=0
     ```

3. **`tillr watch wait` ([`tools/tillr/watch/delivery.go`](../../../tools/tillr/watch/delivery.go)):**
   - `Pending` selects `WHERE a.state = 'open'` with an `opened` or `reopened` event after `seen_seq`, so `tillr watch wait` stays asleep across all intermediate runs (`s_21` through `s_39` on a 20-run median acknowledged at `s_20`, or `1` through `K - 1` post-acknowledgement observations on `count_in_window`).
4. **How the interim `acknowledged` state ends:**
   - **Condition clears before a new instance forms ([`D-TW72`](../tillr-watch.md#D-TW72)):** As post-fix fast gates push `event.median20_wall_ms <= 90000` (matching `clear_when`), or when a `count_in_window` window ages below `threshold`, matches `clear_when`, or sees its item subject finish or close ([`D-TW63`](../tillr-watch.md#D-TW63)), the episode transitions directly from `acknowledged` to `resolved` (`resolved_reason = 'condition-cleared'` or `'item-<state>'`) without waking `wait` (keeping `settlement_state` unsettled until investigated and settled under [`OQ-AW2`](../tillr-watch-alert-workflow.md#OQ-AW2)).
   - **Post-acknowledgement events form a new instance ([`R-AW4`](../tillr-watch-alert-workflow.md#R-AW4), [`D-TW-WAR1`](#D-TW-WAR1)):** If run 40 finishes (`oldest_seq = s_21 > s_20 = acked_seq`) and `event.median20_wall_ms > 90000` still holds — or the `K`th post-acknowledgement observation arrives inside `[now - W, now]` on `count_in_window` — `reopenCause` returns `"new-instance"`. Reconciliation sets `state = 'open'`, clears `acked_*`, resets `settlement_state` from `'investigating'` to `'uninvestigated'`, rebuilds `bundle_json` from the current observations, appends `watch.alert.reopened` (`cause = "new-instance"`), and wakes `tillr watch wait`.

---

## 4. Follow-On Build Slice and Out-of-Scope Candidate Note ([`D-TW-WAR3`](#D-TW-WAR3))

### 4.1 The single follow-on build slice ([`D-TW-WAR3`](#D-TW-WAR3))

*Non-owner decision from the 2026-11-25 operator request and the designer; the owner may overturn it.*

Adding `window_oldest_seq = "median20_oldest_seq"` to [`config/tillr/watch/fast-gate-slow.toml`](../../../tools/tillr-watch/rules/fast-gate-slow.toml) requires the rule loader and `tillr watch lint` (`workflows` gate check) to accept `window_oldest_seq` and requires `medianFastGateTiming` to emit `median20_oldest_seq`. Per [`landing-principles.md` §15](../landing-principles.md#15-land-the-smallest-functional-change), all four parts land together in **one follow-on build slice**:

1. **Drain timing producer (`tools/tillr-drain/drain/gate_timing.go`, `tools/tillr-drain/drain/gate_timing_test.go`):** Populate `median20_oldest_seq` alongside `median20_wall_ms` in `medianFastGateTiming`; verify in unit tests that both fields are omitted for runs `1..19` and that run `20 + k` records the exact `seq` of run `1 + k` as `median20_oldest_seq`.
2. **Watch engine and linter (`tools/tillr/watch/rules.go`, `lint_rules.go`, `pattern.go`, `eval.go`, `source.go`, and unit tests in `tools/tillr/watch/`):** Add `When.WindowOldestSeq`, loader/linter validation, `definitionHash` (`omitempty` preserving existing digests when unset) and `ruleEqual` coverage, and `reopenCause` post-`acked_seq` checks for `CountInWindow` (with and without `distinct_by`) and `PredicateMatch` with `WindowOldestSeq` across both `Store` and `ObservationSource`. Update [`tools/tillr/watch/ack_single_instance_test.go`](../../../tools/tillr/watch/ack_single_instance_test.go) (`threshold = 2` `count_in_window` fixture) so `1` post-ack event keeps `state = "acknowledged"` with `observation_count == 2` and the `2`nd post-ack event reopens as `"new-instance"`.
3. **Rule file and CLI tests (`config/tillr/watch/fast-gate-slow.toml`, `tools/tillr-watch/fast_gate_slow_rule_test.go`, `tools/tillr-watch/ack_single_instance_test.go`):** Add `window_oldest_seq = "median20_oldest_seq"` to `fast-gate-median-slow`; update `ack_single_instance_test.go` for `threshold = 2` post-ack reopening; and verify on scratch stores (`t.TempDir()`, never touching `.tillr/state.db`) that after `ack` at run 20, runs `21..39` with `median20_oldest_seq <= acked_seq` keep `state=acknowledged` and `pending=0` in `tillr watch status` with rising `observed=<count>`, clearing via `median20_wall_ms <= 90000` resolves as `condition-cleared` without waking, run `40` (`median20_oldest_seq > acked_seq`, `median20_wall_ms > 90000`) reopens with `cause = "new-instance"` and wakes `wait`, and a post-ack matching event omitting `median20_oldest_seq` reopens with `Skipped == 0`.
4. **Pack mirror (`just pack-sync`):** Sync `yolo-packs/tillr/files/local/share/tillr/` in the same commit.

### 4.2 Out-of-scope items and candidate note

Per the filed request, the items below are out of scope for this design and its follow-on build slice; the gaps observed at `d6ddd74b4037` are recorded here as candidates for separate items:

1. **The 90-second (`fast-gate-median-slow`) and 60-second (`fast-gate-run-slow`) thresholds ([`config/tillr/watch/fast-gate-slow.toml`](../../../tools/tillr-watch/rules/fast-gate-slow.toml), [`D-TW43`](../tillr-watch.md#D-TW43), [`D-TW52`](../tillr-watch.md#D-TW52)):** Neither threshold is changed. Note also that `fast-gate-run-slow` (`group_by = "slowest_check"`, `match = 'event.wall_ms > 60000'`) judges a single run per event rather than a multi-event window, so each slow run after `acked_seq` is already an independent single-run observation; muting repeats while speeding up a check belongs to bounded item-linked `watch_silences` ([`D-AW4`](../tillr-watch-alert-workflow.md#D-AW4)).
2. **Tracked-item-landed reopening and `settled` single-instance watermarks ([`D-TW67`](../tillr-watch.md#D-TW67), [`D-AW2`](../tillr-watch-alert-workflow.md#D-AW2), [`D-AW5`](../tillr-watch-alert-workflow.md#D-AW5)):** Commit `d52831b6b9` retired `tillr watch track` in favor of `investigate` plus `settle --outcome fix-fault|fix-rule --item <id>` (`settlement_state = 'settled'`, `settled_seq`). Two rolling-window gaps remain on `settle`:
   - **Post-`settled_seq` window overlap in `(episode).reopenCause` ([`tools/tillr/watch/eval.go`](../../../tools/tillr/watch/eval.go)):** The `ep.settlementState == "settled"` branch still checks `newest.Seq > settledSeq` rather than applying the post-watermark window check (`post_settled_count >= threshold` / `oldest_seq > settled_seq`), so settling an active windowed alert against an open or newly landed fix item reopens (`reopenSettledOpenItem` or `reopenSettledTerminal` with `cause = "recurred-after-item-<state>"`) on the next matching event while pre-`settled_seq` events still populate the window.
   - **Settling against a fix that landed before a pre-fix-contaminated window observation ([`tools/tillr/watch/lifecycle.go`](../../../tools/tillr/watch/lifecycle.go), [`tools/tillr/watch/source.go`](../../../tools/tillr/watch/source.go)):** `Evaluator.Settle` and `validateSourceSettleItem` refuse a finished item when `itemSeq <= lastObservedSeq` (`"file a new item"`), even when `oldest_seq <= itemSeq` shows that the window's slow median still comes from runs before the fix landed (as at `08:15:37 ET` on 2026-11-24 after the `07:58:59 ET` fix).
3. **`lag_behind` rules (`trunk-proof-lag` in [`config/tillr/watch/trunk-proof-lag.toml`](../../../tools/tillr-watch/rules/trunk-proof-lag.toml), [`D-TW28`](../tillr-watch.md#D-TW28), [`D-TW75`](../tillr-watch.md#D-TW75), [`D-AW11`](../tillr-watch-alert-workflow.md#D-AW11)):** Untouched; `lag_behind` already suppresses repeat wakes via `acked_lag_pair` (`leaderOID:followerOID`) unless a ref moves or a post-watermark `reopen_when` event (`cause = "reopen-when"`) matches.
4. **Adjacent streak and alternation patterns (`consecutive_count` and `alternating_pair`):** Not `count_in_window` or rolling-median rules and untouched here. Note as a separate candidate that acknowledging an active `consecutive_count` (`threshold = K`, such as `drain-repeated-refusal`, `land-repeated-squash-conflicts`, or `drain-process-cpu-high`) or `alternating_pair` (`gate-fail-then-pass`) episode at length `K` currently reopens on observation `K + 1` (`newest.Seq > ackedSeq`) rather than after `K` post-acknowledgement streak or alternation observations.

---

## 5. Rejected Alternatives

Each alternative below was considered by the designer and set aside (the owner may overturn any of these dispositions except [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), and [`R-AW4`](../tillr-watch-alert-workflow.md#R-AW4)):

| Alternative | Why it fails | Verdict |
| :--- | :--- | :--- |
| **Require `c.events[0].Seq > acked_seq` (zero pre-ack events left in `[now - W, now]`) on `count_in_window`** | Time-bounded windows evict pre-ack events by wall-clock age (`4h`–`24h`), not arrival count, muting a complete fresh `>= K` burst for hours or losing it if `clear_when` intervenes, against [`R-AW4`](../tillr-watch-alert-workflow.md#R-AW4). | Rejected. |
| **Keep `newest.Seq > ackedSeq` on `fast-gate-median-slow` and rely on `watch_silences` instead of `ack`** | Treats one 20-run median exceedance as 20 separate instances on `ack`; moreover `watch_silences` ([`D-AW4`](../tillr-watch-alert-workflow.md#D-AW4)) is not built yet (`rg -n 'watch_silences' tools` returns nothing) and requires an open item ([`R-AW5`](../tillr-watch-alert-workflow.md#R-AW5)), which does not exist when the fix already landed. | Rejected. |
| **Add a dedicated `rolling_median` pattern to `tools/tillr/watch` instead of `median20_oldest_seq` + `window_oldest_seq`** | Duplicates `medianFastGateTiming`, forces `tillr-watch` to scan unbounded event counts outside time windows, and couples the engine to one aggregate when `medianFastGateTiming` already reads the exact 19 prior rows. | Rejected. |
| **Suppress reopening when `window_oldest_seq` is absent on a post-ack matching event** | Mutes real post-acknowledgement instances whenever a producer omits or misnames the boundary field, violating [`R-AW4`](../tillr-watch-alert-workflow.md#R-AW4) and [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2). | Rejected. |
| **Persist running post-ack counters on `alert_episodes`** | Violates [`D-TW13`](../tillr-watch.md#D-TW13) (every pass derives counts from stored events with no running counters) and adds an unnecessary schema migration. | Rejected. |

---

## 6. Open Questions

None.

---

## 7. Decision Ledger

[`D-TW-WAR1`](#D-TW-WAR1) through [`D-TW-WAR3`](#D-TW-WAR3) record decisions from the 2026-11-25 operator request and the designer; none of them is an owner ruling, and the owner may overturn any part of them. `Built` is checked against `d6ddd74b4037`.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-TW-WAR1"></a>**[D-TW-WAR1](#D-TW-WAR1)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** amend [`D-AW11`](../tillr-watch-alert-workflow.md#D-AW11) backward-only so an `acknowledged` episode with unchanged `condition_fingerprint` on a windowed rule reopens as `"new-instance"` if and only if the rule's condition holds after excluding every event with `seq <= acked_seq` — on fixed-$N$-event aggregates (`predicate_match` with `window_oldest_seq`), only when `newest.Seq > acked_seq`, `oldest_seq > acked_seq`, and `match` holds; on `count_in_window`, only when at least `threshold` counted in-window observations (after `distinct_by` deduplication) have `Seq > acked_seq` — while staying `state = "acknowledged"` and visible in `tillr watch status` with `pending = 0` in the meantime until either `condition-cleared` / terminal-item resolution or a full post-ack instance reopens it. | 2026-11-25 | [§2.1](#21-weighing-the-candidate-across-fixed-count-and-time-bounded-windows-d-tw-war1), [§3](#3-what-the-operator-sees-in-the-meantime), [§5](#5-rejected-alternatives) | no |
| <a id="D-TW-WAR2"></a>**[D-TW-WAR2](#D-TW-WAR2)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** amend [`D-TW16`](../tillr-watch.md#D-TW16), [`D-TW43`](../tillr-watch.md#D-TW43), and [`D-TW66`](../tillr-watch.md#D-TW66) backward-only by emitting `median20_oldest_seq` alongside `median20_wall_ms` in `medianFastGateTiming` (`tools/tillr-drain/drain/gate_timing.go`), adding optional validated `window_oldest_seq` to event-reading non-`after` `predicate_match` rules in `tools/tillr/watch/` (included with `omitempty` in `definitionHash` and failing open to `"new-instance"` with `Skipped == 0` if absent or invalid on a post-ack matching event), and setting `window_oldest_seq = "median20_oldest_seq"` on `fast-gate-median-slow` in `config/tillr/watch/fast-gate-slow.toml`. | 2026-11-25 | [§2.2](#22-producer-boundary-and-rule-schema-for-the-median-form-d-tw-war2), [§5](#5-rejected-alternatives) | no |
| <a id="D-TW-WAR3"></a>**[D-TW-WAR3](#D-TW-WAR3)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** deliver `D-TW-WAR1` and `D-TW-WAR2` in one follow-on build slice covering `tools/tillr-drain/drain/gate_timing.go`, `tools/tillr/watch/`, `config/tillr/watch/fast-gate-slow.toml`, `just pack-sync`, and scratch-store unit and CLI tests (`ack_single_instance_test.go` and `fast_gate_slow_rule_test.go`), leaving `90s`/`60s` thresholds, `settled` / tracked-item-landed reopening, `lag_behind`, and `consecutive_count` / `alternating_pair` out of scope as recorded in [§4.2](#42-out-of-scope-items-and-candidate-note). | 2026-11-25 | [§4.1](#41-the-single-follow-on-build-slice-d-tw-war3), [§4.2](#42-out-of-scope-items-and-candidate-note) | no |
