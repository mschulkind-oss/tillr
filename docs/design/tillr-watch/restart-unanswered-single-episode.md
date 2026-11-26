---
title: "One unanswered loop restart stretch raises one alert measured from its oldest pending request"
author: "Agent"
date: 2026-11-26
status: in-review
stage: DECIDED
stage-since: 2026-11-26
tags: [tillr, watch, drain, restart, alerting, design]
summary: "Record oldest_pending_id on drain.restart-request and drain.restart-answer in tools/tillr-drain/restart and group drain-restart-unanswered by role over the landed duration_without_event pattern so one unanswered loop restart opens one episode at 35 minutes from its oldest request without watch-engine changes."
next: "Run the follow-up build item specified in Section 4 across tools/tillr-drain/restart, config/tillr/watch/drain-restart-unanswered.toml, just pack-sync, docs/reference/tillr-restart.md, and scratch-store tests; the owner may review D-TW-RU1."
depends-on:
  - ../tillr-watch.md
  - ../tillr-watch.md#D-TW5
  - ../tillr-watch.md#D-TW16
  - ../tillr-watch.md#D-TW22
  - ../tillr-watch.md#D-TW29
  - ../tillr-watch.md#D-TW41
  - ../tillr-watch.md#D-TW42
  - ../tillr-watch.md#D-TW64
  - ../tillr-watch.md#D-TW73
  - ./decision-record-files.md#R-TW-DR1
  - ./decision-record-files.md#R-TW-DR2
  - ./decision-record-files.md#D-TW-DR1
  - ../landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it
  - ../landing-principles.md#15-land-the-smallest-functional-change
related:
  - ../../reference/tillr-restart.md
  - ../tillr-utility-contracts/tillr-watch.md
vantage:
  status-chip: true
---

# One unanswered loop restart stretch raises one alert measured from its oldest pending request

**Status:** 2026-11-26, verified at `3ab5bd80d9`. Nothing specified in [`D-TW-RU1`](#D-TW-RU1) is built yet (`rg -n 'oldest_pending_id' tools config` returns nothing, and [`config/tillr/watch/drain-restart-unanswered.toml`](../../../tools/tillr-watch/rules/drain-restart-unanswered.toml) from `cba36242` still groups by `object_id`). Per owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) and [`D-TW-DR1`](./decision-record-files.md#D-TW-DR1), this decision record lives in its own standalone file under `docs/design/tillr-watch/` and edits zero lines of [`../tillr-watch.md`](../tillr-watch.md).

> **In short.** Landed `tillr-watch` rule patterns cannot deduplicate multiple queued `drain.restart-request` events into one episode measured from the oldest unanswered request from current event payloads alone: grouping by `object_id` opens one alert per request, grouping `duration_without_event` by `role` resets the 35-minute clock on every subsequent landing's roll, and grouping `predicate_match` by `role` reopens an acknowledged episode as each later request crosses 35 minutes. Recording `oldest_pending_id` atomically on `drain.restart-request` and `drain.restart-answer` in `tools/tillr-drain/restart` lets `drain-restart-unanswered` use the already-landed `duration_without_event` pattern grouped by `role` with zero watch-engine changes.

**Why it matters.** On 2026-11-24, one missed loop restart produced five `drain.restart-request` events between `07:52:32` and `08:30:37 ET` and opened separate alerts at `08:27 ET` and `08:31 ET` (with three more queued behind them), forcing duplicate operator investigation and acknowledgement for one fault ([§1.1](#11-the-2026-11-23-and-2026-11-24-measurements)).

**The shape.** `restart.File` stamps `oldest_pending_id` on `drain.restart-request` and `Restarter.record` stamps `oldest_pending_id` on `drain.restart-answer` when unanswered requests remain; `drain-restart-unanswered` groups `duration_without_event` by `role`, starts its 35-minute timer only when `id == oldest_pending_id` (or on an `exec`-window handoff answer), ignores subsequent requests and `"superseded"` answers, and clears only when `empty(event.oldest_pending_id)` holds ([§3](#3-recorded-fields-and-rule-contract-d-tw-ru1)).

**Cost.** One follow-up build across `tools/tillr-drain/restart`, `config/tillr/watch/drain-restart-unanswered.toml`, `just pack-sync`, [`../../reference/tillr-restart.md`](../../reference/tillr-restart.md), and targeted tests — with zero DDL migrations and zero edits to `tools/tillr/watch` ([§4](#4-the-follow-up-build-items-one-behavior-and-out-of-scope-boundaries-d-tw-ru1)).

**Start at [§2](#2-why-a-rule-only-change-cannot-express-this-on-current-events)** for the answer on rule-only expressibility, [§3](#3-recorded-fields-and-rule-contract-d-tw-ru1) for the exact payload and rule specification, and [§4](#4-the-follow-up-build-items-one-behavior-and-out-of-scope-boundaries-d-tw-ru1) for the follow-up build item's one behavior.

**Needs your ruling:** None. Standing owner rulings [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), [`D-TW64`](../tillr-watch.md#D-TW64), and [`D-TW73`](../tillr-watch.md#D-TW73) govern; [`D-TW-RU1`](#D-TW-RU1) comes from the operator request and the designer and may be overturned by the owner.

**Reads with:** [`../tillr-watch.md`](../tillr-watch.md) (baseline watch architecture and episode state machine), [`./decision-record-files.md`](./decision-record-files.md) (standalone watch decision-record rule), and [`../../reference/tillr-restart.md`](../../reference/tillr-restart.md) (the `restart` protocol and safe replacement boundaries).

---

## 1. Incidents, Constraints, and Provenance

### 1.1 The 2026-11-23 and 2026-11-24 measurements

Two incidents bound `drain-restart-unanswered` ([`landing-principles.md` §12](../landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it)):

1. **2026-11-23 lock-starvation incident (`01:02` to `02:09 ET`).** The drain loop could not restart onto a build installed at `01:02 ET` for `67` minutes (`4314.0s` before settlement) while new gates kept taking the restart lock ahead of the pending restart, and no `tillr-watch` alert fired. Commit `cba36242` added [`config/tillr/watch/drain-restart-unanswered.toml`](../../../tools/tillr-watch/rules/drain-restart-unanswered.toml) with `threshold_duration = "35m"` (above the `1928.4s` / `32.1m` maximum of all `197` non-starved acted-on `drain` restarts recorded in `.tillr/state.db` on 2026-11-24). Because landings and post-landing rolls continue arriving during lock starvation, **a newer `drain.restart-request` must never reset the 35-minute clock measured from the oldest unanswered request**, or active trunk traffic hides the exact starvation `drain-restart-unanswered` exists to catch.
2. **2026-11-24 per-request alert fan-out (`07:52` to `08:31 ET`).** On 2026-11-24, the drain loop missed its restart from `07:52 ET` onward (from a defect whose fix took effect only after the next loop restart). Each subsequent landing's post-landing roll called `restart.File` ([`tools/tillr-drain/restart/restart.go`](../../../tools/tillr-drain/restart/restart.go)) and appended a new `drain.restart-request` with `role = "drain"` at `07:52:32`, `07:56:22`, `08:17:45`, `08:26:33`, and `08:30:37 ET`, all remaining unanswered until a single loop restart settled them together. Because [`config/tillr/watch/drain-restart-unanswered.toml`](../../../tools/tillr-watch/rules/drain-restart-unanswered.toml) groups by `object_id` (the individual request ID), that single unanswered loop restart opened `alrt-3c9be38a441315a3bef38e67339ea` at `08:27 ET` and `alrt-5963f203fc5534bd9e2f18f9e519316d` at `08:31 ET`, with three more alerts due at `08:52`, `09:01`, and `09:05 ET` as each later request reached 35 minutes. The operator investigated and acknowledged the same underlying cause twice.

### 1.2 Authority boundary: owner rulings versus designer decision

Five standing owner rulings govern this record without modification:

- **One file per new watch decision record ([`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), 2026-11-25, `"ok, so save that"`):** every new `tillr-watch` decision record goes in its own standalone file under `docs/design/tillr-watch/`, with zero edits to [`../tillr-watch.md`](../tillr-watch.md).
- **No fallbacks ([`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), 2026-11-16, `"Explicitly, we NEVER want fallbacks anywhere in this project."`):** no fallback path or legacy-event dual behavior is introduced.
- **No automatic remedies ([`OQ-TW2`](../tillr-watch.md#OQ-TW2), 2026-11-21, `"We don't cover issues. We use this to find the issues and fix them"`):** the rule executes nothing.
- **Parameterized instance silences and mandatory investigation ([`D-TW64`](../tillr-watch.md#D-TW64), 2026-11-22):** rules expose instance variables (`[rule.variables]`) so a silence binds the specific unanswered stretch rather than blanketing the entire rule.
- **No `"expected"` alarm text ([`D-TW73`](../tillr-watch.md#D-TW73), 2026-11-22, `"why is there an "expected" alarm? that is never good for "boy who cried wolf""`):** `[rule.guidance]` never calls a firing alert expected.

Everything else in this document — [`D-TW-RU1`](#D-TW-RU1), including the expressibility determination, the `oldest_pending_id` payload field on `Request` and `Answer`, the `drain-restart-unanswered` rule update, the rejected alternatives, and the follow-up build item's scope — is an **operator-request and designer decision (`2026-11-26`), not an owner ruling, and the owner may overturn any part of it**.

---

## 2. Why a Rule-Only Change Cannot Express This on Current Events

**Direct answer:** Raising one alert per unanswered loop restart measured at 35 minutes from the *oldest* unanswered request is **not** a rule-only change that the landed `tillr-watch` patterns can express over current `drain.restart-request` and `drain.restart-answer` payloads. It requires **a small change to what `tools/tillr-drain/restart` records**, paired with a rule update on the already-landed `duration_without_event` pattern (and **zero** changes to `tools/tillr/watch`).

### 2.1 Current recorded facts

In [`tools/tillr-drain/restart/restart.go`](../../../tools/tillr-drain/restart/restart.go) at `3ab5bd80d9`:

- `File` appends `drain.restart-request` with `ObjectType = "restart"`, `ObjectID = &request.ID`, and JSON payload `Request{ID, Role, At}` (`{"id", "role", "at"}`). Neither the event columns nor the JSON payload distinguish the first request of an unanswered stretch from a second, third, or fifth request filed while the first is still pending.
- `Restarter.Settle` loops over `state.Pending[:len(state.Pending)-1]` and calls `rs.record` to append `drain.restart-answer` with `Answer = "superseded"` *before* checking or executing the newest candidate binary — and in `Loop.gateRestartWith` ([`tools/tillr-drain/drain/restart.go`](../../../tools/tillr-drain/drain/restart.go)), if `l.restartDeferred()` holds after `rs.Settle` returns `replace == true`, the loop returns without executing while the older requests already have `drain.restart-answer` (`"superseded"`) rows in `events` and `newest` remains unanswered.
- `Restarter.record` appends `drain.restart-answer` with `ObjectType = "restart"`, `ObjectID = &answer.RequestID`, and JSON payload `Answer{RequestID, Role, Answer, FromBuild, ToBuild, ToDigest, Detail, At}`. Nothing on `drain.restart-answer` states whether another unanswered request for `role` is still pending in `events`.

### 2.2 Why every landed pattern fails without recorded pending-stretch provenance

[`tools/tillr/watch/rules.go`](../../../tools/tillr/watch/rules.go) and [`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go) provide six patterns (`duration_without_event`, `predicate_match`, `count_in_window`, `consecutive_count`, `alternating_pair`, and `lag_behind`). Against unmodified `drain.restart-request` and `drain.restart-answer` events, every rule-only formulation fails at least one invariant:

1. **`duration_without_event` with `group_by = "object_id"` (current [`config/tillr/watch/drain-restart-unanswered.toml`](../../../tools/tillr-watch/rules/drain-restart-unanswered.toml)):** `alert_episodes` keys active episodes by `(rule_id, subject_key)` ([`D-TW5`](../tillr-watch.md#D-TW5)). Because each `drain.restart-request` mints a distinct `object_id`, five unanswered requests during one stalled restart create five distinct subjects and five independent episodes.
2. **`duration_without_event` with `group_by = "role"` or `group_by = "global"` over unmodified events:**
   - **Clock reset on `after`:** In `(*condition).observe` ([`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go)), every event matching `after` executes `c.events = append(c.events[:0], e)` and recomputes `c.waitAge = time.UnixMilli(values.nowMillis).Sub(time.UnixMilli(e.TS))`, while appending `e.Seq` to `c.clears`. Any landing whose post-landing roll files a new `drain.restart-request` within 35 minutes of the previous one overwrites `c.events` and resets `c.waitAge` to zero (and resolves an already-open episode via `c.clears`), hiding the 2026-11-23 lock-starvation failure mode whenever trunk lands at least once every 34 minutes.
   - **Premature clear or stranded episode on `match`:** Matching every `drain.restart-answer` clears the episode as soon as `Restarter.Settle` writes `answer == "superseded"` for older requests, even if `l.restartDeferred()` then defers replacement and leaves `newest` unanswered. Filtering `match` to `event.answer != "superseded"` still clears prematurely in the `exec` handoff window if a new request arrives between `Restarter.Replace` and `Restarter.Adopt` (`Adopt` writes `answer == "restarted"` for the adopted request while the newly filed request remains unanswered).
3. **`predicate_match` with `group_by = "role"`, `match = 'event.kind == "drain.restart-request" and age(event.ts) >= 2100'`, and `clear_when`:**
   - **Post-acknowledgement reopen on every later request:** As each subsequent request (`req2` at `07:56:22`, `req3` at `08:17:45`, …) reaches `age(event.ts) >= 2100`, `(*condition).observe` appends it to `c.events`, advancing `newest.Seq` past the episode's `acked_seq` (or `settled_seq`). In `episode.reopenCause` ([`tools/tillr/watch/eval.go`](../../../tools/tillr/watch/eval.go)), `newest.Seq > ackedSeq` triggers `"new-instance"` and reopens the acknowledged episode on every request at `t + 35m` — reproducing the exact multi-alert interruption on 2026-11-24.
   - **Wrong `{{elapsed}}` duration:** In `encodeConditionBundle` ([`tools/tillr/watch/bundle.go`](../../../tools/tillr/watch/bundle.go)), non-`duration_without_event` event patterns compute `elapsed = newest.TS - oldest.TS` (`0s` when one request has crossed 35 minutes) rather than `now - oldest.TS`.
4. **`count_in_window`, `consecutive_count`, `alternating_pair`, and `lag_behind`:** None of the four measures wall-clock silence elapsed since a single unanswered event (`threshold >= 1` on count/pair patterns fires on arrival or count without waiting 35 minutes, and `lag_behind` compares Git ref tips rather than event pairs).

---

## 3. Recorded Fields and Rule Contract ([`D-TW-RU1`](#D-TW-RU1))

*Non-owner decision (from the operator request and the designer; the owner may overturn any part of this section).*

### 3.1 Atomic `oldest_pending_id` on `drain.restart-request` and `drain.restart-answer`

In [`tools/tillr-drain/restart/restart.go`](../../../tools/tillr-drain/restart/restart.go), extend `Request` and `Answer` with `OldestPendingID` and populate both inside the existing store write transactions so serialized writers see exact `events.seq` ordering with zero new tables, columns, or event kinds:

```go
// Request is one request to replace a role's process.
type Request struct {
	ID              string    `json:"id"`
	Role            string    `json:"role"`
	At              time.Time `json:"at"`
	OldestPendingID string    `json:"oldest_pending_id"`
}

// Answer is the one event that settles a request.
type Answer struct {
	RequestID       string    `json:"request_id"`
	Role            string    `json:"role"`
	Answer          string    `json:"answer"`
	FromBuild       string    `json:"from_build"`
	ToBuild         string    `json:"to_build"`
	ToDigest        string    `json:"to_digest,omitempty"`
	Detail          string    `json:"detail,omitempty"`
	At              time.Time `json:"at"`
	OldestPendingID string    `json:"oldest_pending_id,omitempty"`
}
```

1. **In `File(ctx, storePath, role, at)`:** Move `json.Marshal(request)` inside the existing `s.WriteTx` closure. Before marshaling and calling `tx.Append`, call `ReadState(tx.Reader(), role)`:
   - When `len(state.Pending) == 0`, set `request.OldestPendingID = request.ID`.
   - When `len(state.Pending) > 0`, set `request.OldestPendingID = state.Pending[0].ID`.
   - Return the populated `request` to the caller.
2. **In `(*Restarter).record(ctx, answer)`:** Move `json.Marshal(answer)` inside the existing `s.WriteTx` closure. Before marshaling and calling `tx.Append`, call `ReadState(tx.Reader(), rs.Role)` and find the first entry `p` in `state.Pending` (which `ReadState` orders oldest-first by `events.seq`) with `p.ID != answer.RequestID`:
   - When no such pending request exists, set `answer.OldestPendingID = ""` (omitted in JSON by `omitempty`).
   - When at least one other pending request remains for `rs.Role`, set `answer.OldestPendingID = p.ID`.

### 3.2 How every restart transition behaves

| Transition in `tools/tillr-drain` | `oldest_pending_id` written | Effect on `drain-restart-unanswered` (`subject = "drain"`) |
| :--- | :--- | :--- |
| **First unanswered request `req1` (`File` when `Pending` is empty)** | `req1.oldest_pending_id = req1.ID` (`id == oldest_pending_id`) | Matches `after`: sets `c.events = [req1]` and measures `c.waitAge = now - req1.TS`. Opens one episode once `c.waitAge >= 35m`. |
| **Later requests `req2..reqN` while `req1` (or any newer unsettled request) is still pending (`File` when `Pending` is non-empty)** | `reqK.oldest_pending_id = state.Pending[0].ID` (`id != oldest_pending_id`) | Matches neither `after` nor `match`: `c.events` stays `[req1]`, `c.waitAge` keeps counting from `req1.TS`, and `newest.Seq` stays `req1.Seq` so an acknowledged or settled episode never reopens. |
| **`Restarter.Settle` marks `req1..req(N-1)` `"superseded"`, including when `Loop.gateRestartWith` then defers on `l.restartDeferred()`** | `answer.answer = "superseded"`, `answer.oldest_pending_id = reqN.ID` (non-empty because `reqN` is still unanswered; any subsequent `File` still sees `Pending = [reqN, ...]` so `id != oldest_pending_id`) | Matches neither `after` (`answer == "superseded"`) nor `match` (`not empty(oldest_pending_id)`): `c.events` stays `[req1]` and `c.waitAge` stays `now - req1.TS` with no clock reset and no premature resolution. |
| **Final pending request answered by `Adopt` (`"restarted"`), `Settle` (`"already-current"` or `"refused"`), `Refuse` (`"refused"`), or `activateSchemaRoll` (`"refused"`) with no newer request pending** | `answer.oldest_pending_id = ""` (omitted) | Matches `match` (`empty(event.oldest_pending_id)`): empties `c.events`, appends `answer.Seq` to `c.clears`, and resolves the episode with `resolved_reason = "condition-cleared"`. |
| **In-flight `exec` handoff: `req_next` is filed after `Replace`'s final `rs.newest` check and before `Adopt` records `"restarted"` (or just before a non-`"superseded"` `record` commits)** | `req_next` saw `req1` pending (`req_next.id != req_next.oldest_pending_id`); the non-`"superseded"` answer sees `req_next` remaining (`answer.oldest_pending_id = req_next.ID`) | Matches `after` (`event.kind == "drain.restart-answer" and event.answer != "superseded" and not empty(event.oldest_pending_id)`): appends `answer.Seq` to `c.clears` (resolving `req1`'s episode in `EvaluateRules`) and re-arms `c.events = [answer]` with `var.request_id = req_next.ID` so `req_next` is not orphaned if the new process also stalls for 35 minutes. |

### 3.3 Updated rule: `config/tillr/watch/drain-restart-unanswered.toml`

Using only keys and expressions already supported by `LoadRules` and `tillr watch lint` in `tools/tillr/watch` ([`D-TW16`](../tillr-watch.md#D-TW16), [`D-TW22`](../tillr-watch.md#D-TW22), [`D-TW29`](../tillr-watch.md#D-TW29), [`D-TW64`](../tillr-watch.md#D-TW64), [`D-TW73`](../tillr-watch.md#D-TW73)):

```toml
[[rule]]
id = "drain-restart-unanswered"
scope = "core"
severity = "warning"

[rule.variables]
request_id = "oldest_pending_id"

[rule.reads]
events = ["drain.restart-request", "drain.restart-answer"]

[rule.when]
group_by = "role"
pattern = "duration_without_event"
after = 'event.role == "drain" and ((event.kind == "drain.restart-request" and event.id == event.oldest_pending_id) or (event.kind == "drain.restart-answer" and event.answer != "superseded" and not empty(event.oldest_pending_id)))'
match = 'event.kind == "drain.restart-answer" and event.role == "drain" and empty(event.oldest_pending_id)'
threshold_duration = "35m"
window = "24h"

[rule.guidance]
summary = "Loop restart ({{subject}}) has gone unanswered for {{elapsed}} since {{var.request_id}}."
check = [
  "Run tillr drain why to read stale-loop-build and loop_restart_line and see which gate, proof, or stopped loop is holding the restart since {{var.request_id}}.",
  "Check that the drain loop process is alive and inspect the drain loop log and newest drain.restart-request and drain.restart-answer events.",
]
do = [
  "Establish why the drain loop has not answered pending restart requests since {{var.request_id}} — whether gates or proofs are starving the restart lock or the loop has stalled or exited — and fix the blocker or restart the loop.",
]

[rule.delivery]
receiver = "foreground"
```

Why each key in this rule has this exact form:

- **`group_by = "role"` (`Skipped == 0` under [`D-TW22`](../tillr-watch.md#D-TW22)):** Both `drain.restart-request` and `drain.restart-answer` already carry top-level string `"role"` (`"drain"` or `"view"`), so zero events are skipped (`Skipped == 0`) and at most one unresolved episode exists for `"drain"` across any number of queued requests ([`D-TW5`](../tillr-watch.md#D-TW5)).
- **`[rule.variables] request_id = "oldest_pending_id"` ([`D-TW64`](../tillr-watch.md#D-TW64)):** On an initial `drain.restart-request` (`id == oldest_pending_id`), `oldest_pending_id` is that request's ID; on an `exec`-window handoff `drain.restart-answer` (which has `request_id` rather than `id`), `oldest_pending_id` is the remaining unanswered request's ID. Extracting `request_id` from `"oldest_pending_id"` ensures `{{var.request_id}}` never renders `"unknown"` on either `after` branch and scopes variable-bound silences to `(role = "drain", request_id = <stretch start id>)` rather than silencing future stalled restarts.
- **Rule baseline behavior on upgrade ([`D-TW66`](../tillr-watch.md#D-TW66)):** Changing `group_by`, `after`, and `match` changes `definition_hash` in `watch_rule_baselines`, advancing `loaded_seq` so pre-upgrade events lacking `oldest_pending_id` neither false-clear nor reopen historical episodes (`empty(event.oldest_pending_id)` only matters for post-upgrade `drain.restart-answer` events after a post-upgrade `after` event with `seq > loaded_seq`).

---

## 4. The Follow-Up Build Item's One Behavior and Out-of-Scope Boundaries ([`D-TW-RU1`](#D-TW-RU1))

### 4.1 The follow-up build item's one behavior

**One behavior for the follow-up build item:** Record `oldest_pending_id` atomically on `drain.restart-request` (`restart.File`) and `drain.restart-answer` (`Restarter.record`) in `tools/tillr-drain/restart`, and update `config/tillr/watch/drain-restart-unanswered.toml` to group `duration_without_event` by `role` starting on `event.id == event.oldest_pending_id` (and on non-`"superseded"` answers with non-empty `oldest_pending_id`) and clearing on `empty(event.oldest_pending_id)` at the existing `35m` threshold, so one unanswered `drain` loop restart stretch raises at most one alert measured from its oldest unanswered request.

Per repository delivery rules ([`D-TW41`](../tillr-watch.md#D-TW41), [`D-TW42`](../tillr-watch.md#D-TW42), [`landing-principles.md` §15](../landing-principles.md#15-land-the-smallest-functional-change)), that single build commit includes:

1. [`tools/tillr-drain/restart/restart.go`](../../../tools/tillr-drain/restart/restart.go) and unit tests in `tools/tillr-drain/restart/` covering `OldestPendingID` on single and multi-request stretches, `Settle` `"superseded"` answers leaving `newest` pending, final settlement omitting `oldest_pending_id`, and `exec`-window handoff answers;
2. [`config/tillr/watch/drain-restart-unanswered.toml`](../../../tools/tillr-watch/rules/drain-restart-unanswered.toml) (preserving the 2026-11-23 measurement comment, adding the 2026-11-24 multi-request incident comment, and keeping `threshold_duration = "35m"`) and scratch-store rule tests in [`tools/tillr-watch/restart_unanswered_rule_test.go`](../../../tools/tillr-watch/restart_unanswered_rule_test.go) verifying:
   - five unanswered `drain` requests (matching the 2026-11-24 `07:52`–`08:30 ET` cadence) open **one** alert at `t0 + 35m` on subject `"drain"` naming `req1.ID`, do **not** reset the clock when `req2..req5` arrive before `35m`, and do **not** open a second episode or reopen an acknowledged episode when `req2..req5` each pass `35m` of age;
   - `Restarter.Settle` answering `req1..req4` `"superseded"` while `req5` remains unanswered (simulating `l.restartDeferred()` after `Settle`) neither clears the episode nor resets its elapsed timer;
   - answering `req5` with `"restarted"` (or `"already-current"` / `"refused"`) resolves the single episode;
   - a new request `req6` filed before `Adopt` answers `req5` resolves `req1`'s episode and starts a new 35-minute stretch for `req6`;
3. `just pack-sync` mirror updates under `yolo-packs/tillr/` for `tools/tillr-drain`, plus updating [`docs/reference/tillr-restart.md`](../../reference/tillr-restart.md) to document `oldest_pending_id` on `drain.restart-request` and `drain.restart-answer`.

*(If [`D-TW84`](../tillr-watch.md#D-TW84)–[`D-TW88`](../tillr-watch.md#D-TW88) have moved shipped rules from `config/tillr/watch/` to `tools/tillr-watch/rules/` before the follow-up build runs, the build edits `drain-restart-unanswered.toml` at its active shipped path and runs `just pack-sync`.)*

### 4.2 Out of scope for this design item (candidate note on untouched surfaces)

Per the operator request's `"Change nothing else"` boundary, this design commit adds only `docs/design/tillr-watch/restart-unanswered-single-episode.md` and leaves the following untouched:

- **Live rule file ([`config/tillr/watch/drain-restart-unanswered.toml`](../../../tools/tillr-watch/rules/drain-restart-unanswered.toml)) and restart package ([`tools/tillr-drain/restart/restart.go`](../../../tools/tillr-drain/restart/restart.go)):** unchanged in this commit; until the follow-up build in [§4.1](#41-the-follow-up-build-items-one-behavior) lands, multiple unanswered `drain.restart-request` events during a single stalled restart continue to open one episode per `object_id`.
- **The 35-minute threshold (`threshold_duration = "35m"`):** unchanged and backed by the 2026-11-24 `.tillr/state.db` distribution (`p99 = 1928.4s` / `32.1m` across non-starved acted-on restarts versus `4314.0s` / `71.9m` on the 2026-11-23 starvation incident).
- **Other watch rules and roles:** `role == "view"` unanswered restarts (still excluded by `event.role == "drain"`) and immediate alerting on `answer == "refused"` (which clears `drain-restart-unanswered` because the request is no longer unanswered, rather than firing a separate refused-restart rule) remain outside `drain-restart-unanswered` and untouched here.

---

## 5. Rejected Alternatives

Each alternative below was evaluated by the designer against `tools/tillr/watch` and `tools/tillr-drain/restart` and set aside (the owner may overturn any of these dispositions except [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), [`D-TW64`](../tillr-watch.md#D-TW64), and [`D-TW73`](../tillr-watch.md#D-TW73)):

| Alternative | Why it fails | Verdict |
| :--- | :--- | :--- |
| **Rule-only `duration_without_event` grouped by `role` without `oldest_pending_id`** | Every subsequent `drain.restart-request` overwrites `c.events` and resets `c.waitAge` to zero in `(*condition).observe`, masking the 2026-11-23 lock-starvation incident whenever landings arrive less than 35 minutes apart, while `"superseded"` answers in `Restarter.Settle` clear prematurely if `l.restartDeferred()` defers `exec`. | Rejected. |
| **Rule-only `predicate_match` grouped by `role` with `age(event.ts) >= 2100` and `clear_when`** | Each later request crossing 35 minutes appends a higher `Seq` to `c.events`, causing `episode.reopenCause` (`newest.Seq > ackedSeq`) to reopen an acknowledged episode on every request anyway, and `encodeConditionBundle` reports `{{elapsed}}` as `newest.TS - oldest.TS` instead of `now - oldest.TS`. | Rejected. |
| **Group `drain-restart-unanswered` by `oldest_pending_id` instead of `role`** | If `Restarter.Settle` answers `req1` `"superseded"` while `req2` stays pending (for example under `l.restartDeferred()`) and `Adopt` later answers `req2` `"restarted"`, `ReadState` at `Adopt` time sees `req1` already answered and `Pending == [req2]`; `Adopt`'s `oldest_pending_id` would not match `req1.ID` unless `ReadState` reconstructed multi-hop supersession chains, stranding `req1`'s open episode unresolved and incrementing `Skipped` on events that omit `oldest_pending_id`. | Rejected. |
| **Add a new `"first-start-wins"` flag or stateful pattern to `tools/tillr/watch` instead of recording `oldest_pending_id`** | Even with a `"keep oldest start"` mode in `duration_without_event`, the watch engine still cannot tell from an unmodified `drain.restart-answer` whether `Settle` or `Adopt` left a newer `drain.restart-request` pending in the store without domain-specific coupling between requests and answers. Recording `oldest_pending_id` in `restart` keeps `tools/tillr/watch` general and untouched. | Rejected. |
| **Suppress filing new `drain.restart-request` rows in `restart.File` when one is already pending** | Breaks `AwaitAnswer(ctx, storePath, id, poll)` (`tillr-drain restart --wait`) and `Restarter.Replace`'s `ErrSuperseded` invariant (`latest.ID != request.ID`), which relies on a newer `File` call recording its own request ID when the binary on disk changes again while `Settle` is running ([`../../reference/tillr-restart.md`](../../reference/tillr-restart.md)). | Rejected. |

---

## 6. Open Questions

None.

---

## 7. Decision Ledger

[`D-TW-RU1`](#D-TW-RU1) records an operator-request and designer decision; it is **not** an owner ruling, and the owner may overturn any part of it. `Built` is checked against `3ab5bd80d9`.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-TW-RU1"></a>**[D-TW-RU1](#D-TW-RU1)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** landed `tillr-watch` rule patterns cannot deduplicate multiple unanswered `drain.restart-request` events into one episode measured at 35 minutes from the oldest request without changing what `tools/tillr-drain/restart` records. A single follow-up build item implements one behavior with zero `tools/tillr/watch` engine changes: (1) atomically record `oldest_pending_id` on `drain.restart-request` inside `restart.File`'s `WriteTx` (`request.ID` when no `role` request is pending, else `state.Pending[0].ID`) and optional `oldest_pending_id` on `drain.restart-answer` inside `Restarter.record`'s `WriteTx` (omitted when no other `role` request remains pending, else the oldest remaining pending request's `ID`); and (2) update `config/tillr/watch/drain-restart-unanswered.toml` (`variables = { request_id = "oldest_pending_id" }`, `group_by = "role"`, `pattern = "duration_without_event"`, `threshold_duration = "35m"`, `window = "24h"`) so `after` arms only on `drain.restart-request` with `event.id == event.oldest_pending_id` or a non-`"superseded"` `drain.restart-answer` with non-empty `oldest_pending_id`, and `match` clears only on `drain.restart-answer` with `empty(event.oldest_pending_id)`, delivered with `just pack-sync`, `docs/reference/tillr-restart.md` field documentation, and unit plus scratch-store tests. | 2026-11-26 | [§2](#2-why-a-rule-only-change-cannot-express-this-on-current-events), [§3](#3-recorded-fields-and-rule-contract-d-tw-ru1), [§4](#4-the-follow-up-build-items-one-behavior-and-out-of-scope-boundaries-d-tw-ru1), [§5](#5-rejected-alternatives) | no |
