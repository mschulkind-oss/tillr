---
title: "tillr-watch: Shared-Cause Keying and Landing Reset for drain-repeated-refusal"
author: "Agent"
date: 2026-11-26
status: in-review
stage: DECIDED
stage-since: 2026-11-26
tags: [tillr, watch, alerting, drain, refusals, design]
summary: "Specify what two drain.refusal events across distinct items must share for drain-repeated-refusal to fire, record refusal_causes on drain.refusal, broadcast land.landing resets across cause subjects, and define one coherent build slice."
next: "Build D-TW-RRS1 through D-TW-RRS4 in one coherent delivery covering tools/tillr-drain/drain/, tools/tillr/watch/, just pack-sync, config/tillr/watch/drain-repeated-refusal.toml, and tools/tillr-watch/repeated_refusal_rule_test.go; the owner may review D-TW-RRS1 through D-TW-RRS4."
depends-on:
  - ../tillr-watch.md
  - ../tillr-watch.md#D-TW22
  - ../tillr-watch.md#D-TW50
  - ../tillr-watch.md#D-TW66
  - ../tillr-watch.md#D-TW73
  - ../tillr-watch.md#D-TW76
  - ../tillr-watch.md#D-TW81
  - ./decision-record-files.md#R-TW-DR1
  - ./decision-record-files.md#R-TW-DR2
  - ./decision-record-files.md#D-TW-DR1
  - ../landing-principles.md#15-land-the-smallest-functional-change
related:
  - ../tillr-convergence.md
  - ../tillr-watch-alert-workflow.md
  - ./fixer-held-new-attempt.md
  - ./windowed-ack-reopen.md
vantage:
  status-chip: true
---

# Shared-cause keying and landing reset for `drain-repeated-refusal`

**Status:** 2026-11-26, verified at `3097ac0e88a0`. Nothing specified in [`D-TW-RRS1`](#D-TW-RRS1) through [`D-TW-RRS4`](#D-TW-RRS4) is built yet (`rg -n 'refusal_causes|broadcast_events|BroadcastEvents' tools config` returns nothing, and [`config/tillr/watch/drain-repeated-refusal.toml`](../../../config/tillr/watch/drain-repeated-refusal.toml) still sets `group_by = "global"`). Per owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) and [`D-TW-DR1`](./decision-record-files.md#D-TW-DR1), this standalone record amends [`D-TW76`](../tillr-watch.md#D-TW76) backward-only without editing [`../tillr-watch.md`](../tillr-watch.md).

> **In short.** `drain-repeated-refusal` stops grouping all refusals into `global` and instead fans out over a recorded `refusal_causes` array on `drain.refusal` (`for_each = "refusal_causes"`, `group_by = "cause"`), while an explicit `broadcast_events = ["land.landing"]` declaration in `[rule.when]` lets a landed candidate (`result == "landed"`) reset every cause counter and resolve open cause episodes without counting non-`landed` landing rows in `Skipped`.

**Why it matters.** On 2026-11-24 (`09:08 ET`), [`config/tillr/watch/drain-repeated-refusal.toml`](../../../config/tillr/watch/drain-repeated-refusal.toml) landed (`0f745305`) with `group_by = "global"`. Seventy-eight minutes later (`10:26 ET`), with ten lanes active, it paged on two items with nothing in common — one held behind an already-filed trunk fixer (`"trunk-already-red"` at `10:20 ET`) and one hitting a squash `"conflict"` on a design file (`10:25 ET`) — violating the landing-principles rule that a mechanism whose false-alarm rate rises with concurrent lane activity is pointed the wrong way ([§1.1](#11-the-2026-11-24-incident-and-why-this-is-a-design-record)).

**The shape.** Three coupled parts, delivered together as one behavioral slice ([`D-TW-RRS4`](#D-TW-RRS4)):

- Classify every `drain.refusal` step into countable cross-item cause keys (`"gate:<check>"`, `"gate:no-verdict"`, `"staging:<reason>"`, or step-level pipeline causes) versus empty `refusal_causes: []` for refusal classes already owned by dedicated alerts, repair items, concurrency rules, or per-item judge dispositions ([`D-TW-RRS1`](#D-TW-RRS1)).
- Record `refusal_causes` as a non-null JSON array on every `drain.refusal` payload so `for_each = "refusal_causes"` and `group_by = "cause"` credit multi-check gate failures per check ([`D-TW-RRS2`](#D-TW-RRS2)).
- Add explicit `broadcast_events` in `[rule.when]` so subject-less `land.landing` events reset and clear every active and in-window cause subject on `result == "landed"` while keeping `Skipped == 0` on `squash-conflict` and `refused` landings ([`D-TW-RRS3`](#D-TW-RRS3)).

**Cost.** Zero `.tillr/state.db` schema changes, one pure cause-derivation helper across the ten `drain.refusal` write sites in [`tools/tillr-drain/drain/`](../../../tools/tillr-drain/drain/), one explicit `broadcast_events` rule field in [`tools/tillr/watch/`](../../../tools/tillr/watch/) (`just pack-sync` mirrored), and one updated TOML rule plus test in [`config/tillr/watch/drain-repeated-refusal.toml`](../../../config/tillr/watch/drain-repeated-refusal.toml) and [`tools/tillr-watch/repeated_refusal_rule_test.go`](../../../tools/tillr-watch/repeated_refusal_rule_test.go).

**Start at [§2](#2-what-two-refusals-must-share-to-fire-d-tw-rrs1)** for the cause taxonomy, **[§3](#3-the-recorded-field-on-drainrefusal-d-tw-rrs2)** for `refusal_causes`, **[§4](#4-how-a-landing-resets-and-clears-cause-subjects-d-tw-rrs3)** for `broadcast_events`, or **[§5](#5-first-build-slice-and-candidate-note-on-out-of-scope-gaps-d-tw-rrs4)** for the build slice and out-of-scope gaps. **Needs your ruling:** none blocking; [`D-TW-RRS1`](#D-TW-RRS1)–[`D-TW-RRS4`](#D-TW-RRS4) are operator-request and designer decisions that the owner may overturn. **Reads with:** [`../tillr-watch.md`](../tillr-watch.md) ([`D-TW76`](../tillr-watch.md#D-TW76), [`D-TW81`](../tillr-watch.md#D-TW81)), [`./decision-record-files.md`](./decision-record-files.md), and [`../landing-principles.md`](../landing-principles.md) ([§15](../landing-principles.md#15-land-the-smallest-functional-change)).

---

## 1. Problem, Incident, and Provenance

### 1.1 The 2026-11-24 incident and why this is a design record

Commit `0f745305` implemented [`D-TW76`](../tillr-watch.md#D-TW76) in [`config/tillr/watch/drain-repeated-refusal.toml`](../../../config/tillr/watch/drain-repeated-refusal.toml) (`2026-11-24 09:08 ET`), counting `drain.refusal` events across `distinct_by = "item_id"` under `group_by = "global"` with `pattern = "consecutive_count"`, `threshold = 2`, and `reset` / `clear_when` set to `'event.kind == "land.landing" and event.result == "landed"'`. At `10:26 ET` on `2026-11-24`, with ten lanes working concurrently, the rule opened an alert on two items whose refusals shared nothing:

1. At `10:20 ET`, one candidate was acquitted as `step = "trunk-already-red"` (a broken trunk test whose `fix-trunk-*` repair item was already filed).
2. At `10:25 ET`, a second item hit `step = "conflict"` (a squash conflict on a design file).

Because `group_by = "global"` collapses every `drain.refusal` into one counter, any two concurrent lanes refusing for unrelated reasons between two landings page the operator. Conversely, changing `group_by` in the TOML file alone cannot fix the rule: in `observeSingleRecord` and `observeForEachRecord` ([`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go)), a non-`global` rule extracts its subject from each event's own fields, and `land.landing` ([`tools/tillr-land/land/landing.go`](../../../tools/tillr-land/land/landing.go)) carries no refusal step or cause field — so a landing would miss every cause subject and increment `RuleResult.Skipped` instead of resetting the counter.

### 1.2 Authority, provenance, and standalone file placement

Per [`./decision-record-files.md#R-TW-DR1`](./decision-record-files.md#R-TW-DR1) and [`./decision-record-files.md#D-TW-DR1`](./decision-record-files.md#D-TW-DR1), this record lives in its own unnumbered file under `docs/design/tillr-watch/`, uses topic-derived `D-TW-RRS*` anchors, amends [`../tillr-watch.md#D-TW76`](../tillr-watch.md#D-TW76) backward-only through prose and `depends-on`, and edits zero lines of [`../tillr-watch.md`](../tillr-watch.md) or any other existing document.

Only quoted owner statements carry owner authority here:

- **Owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`2026-11-25`, `"ok, so save that"`):** each new `tillr-watch` decision record lives in its own file under `docs/design/tillr-watch/` without editing [`../tillr-watch.md`](../tillr-watch.md).
- **Owner ruling [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2) (`2026-11-16`, `"Explicitly, we NEVER want fallbacks anywhere in this project."`):** no implicit subject-lookup fallback or legacy-event fallback parser is introduced.
- **Owner rulings [`OQ-TW2`](../tillr-watch.md#OQ-TW2), [`D-TW64`](../tillr-watch.md#D-TW64), and [`D-TW73`](../tillr-watch.md#D-TW73):** watch rules execute no remedies, alerts end in recorded actions, and rule/alert prose never calls a firing alarm `"expected"` or `"normal"`.

Everything else in this document — [`D-TW-RRS1`](#D-TW-RRS1), [`D-TW-RRS2`](#D-TW-RRS2), [`D-TW-RRS3`](#D-TW-RRS3), and [`D-TW-RRS4`](#D-TW-RRS4), answering the `2026-11-24` operator request and amending [`D-TW76`](../tillr-watch.md#D-TW76) backward-only — is an **operator-request and designer decision (`2026-11-26`), not an owner ruling, and the owner may overturn any part of it**. No unresolved owner-only policy question blocks this design.

---

## 2. What Two Refusals Must Share to Fire ([`D-TW-RRS1`](#D-TW-RRS1))

*Operator-request and designer decision (not an owner ruling; the owner may overturn any part of this section).*

### 2.1 Why grouping by `step` alone is both too coarse and too broad

Grouping `drain.refusal` solely by `event.step` fails in two directions:

1. **Too coarse on `"gate"` and `"staging"`:** Under `step == "gate"`, item A failing fast check `documents` on a broken link in its own file and item B failing `go-build` in an unrelated package share `step == "gate"` without sharing a cause. Likewise, under `step == "staging"` (`recordItemRefusal` in [`tools/tillr-drain/drain/events.go`](../../../tools/tillr-drain/drain/events.go)), a worktree git failure (`hold_reason = "staging-failed"`) and malformed item metadata (`hold_reason = "item-unstagleable"`) are distinct failure modes.
2. **Too broad on already-owned, concurrency, and item-disposition refusals:** With ten lanes running against `lane_base = "tip"`, two lanes hitting `"trunk-already-red"` behind an already-filed `fix-trunk-*` item (already alerted by [`drain-trunk-fixer-filed.toml`](../../../config/tillr/watch/drain-trunk-fixer-filed.toml)), two lanes hitting squash `"conflict"` on hot paths (already covered by [`land-squash-conflict.toml`](../../../config/tillr/watch/land-squash-conflict.toml), [`land-repeated-squash-conflicts.toml`](../../../config/tillr/watch/land-repeated-squash-conflicts.toml) / [`D-TW68`](../tillr-watch.md#D-TW68), and [`drain-repeated-item-conflicts.toml`](../../../config/tillr/watch/drain-repeated-item-conflicts.toml) / [`D-TW51`](../tillr-watch.md#D-TW51)), or a single occurrence already alerted at `threshold = 1` ([`drain-revert.toml`](../../../config/tillr/watch/drain-revert.toml), [`drain-plan-output-unparseable.toml`](../../../config/tillr/watch/drain-plan-output-unparseable.toml), [`drain-judge-unreadable.toml`](../../../config/tillr/watch/drain-judge-unreadable.toml)) should not open a duplicate `drain-repeated-refusal` episode.

By contrast, [`drain-repeated-verifier-refusals.toml`](../../../config/tillr/watch/drain-repeated-verifier-refusals.toml) groups by `item_id` at `threshold = 5` (one item failing verification five times in 24 hours), so it does **not** detect two *distinct* items hitting `"verify-no-verdict"` or `"verify-not-done"` back-to-back before any landing; `drain-repeated-refusal` remains the cross-item detector for those steps.

### 2.2 Complete `drain.refusal` step-to-cause mapping

Two `drain.refusal` events across distinct `item_id` values with no landed candidate (`land.landing` with `result == "landed"`) between them fire `drain-repeated-refusal` if and only if their `refusal_causes` arrays share at least one `cause` string under the exhaustive table below covering all ten `RefusalKind` emitters in [`tools/tillr-drain/drain/`](../../../tools/tillr-drain/drain/):

| `step` value(s) | Producer in `tools/tillr-drain/drain/` | Emitted `cause` key(s) in `refusal_causes` | Reason |
|---|---|---|---|
| `"gate"` with $\ge 1$ extracted check ID | `recordRefusal` (`events.go`) | One `"gate:" + id` per failing check (`failing.ID` when non-empty in `check.Failing`, else `check.CheckID`, matching `gateRefusalDetail`), sorted & deduplicated | Fires only when two distinct items fail the **same** gate check; credits every failing check on a multi-check failure independently. |
| `"gate"` with `0` extracted check IDs | `recordRefusal` (`events.go`) | `"gate:no-verdict"` | Missing or unreadable `gate.fast.verdict.json` / `gate.verdict.json` across two items is itself a shared gate-harness fault. |
| `"staging"` | `recordItemRefusal` (`events.go`) | `"staging:" + hold_reason` (`"staging:staging-failed"` or `"staging:item-unstagleable"`, or `"staging"` if `hold_reason` is empty) | Separates worktree/git staging failures from unstageable item definitions while catching cross-item stager faults. |
| `"fence"`, `"land"`, `"no-op"`, `"node-output-missing"`, `"verify-no-verdict"`, `"verify-not-done"`, `"plan-wrong"`, `"delivery-order"`, `"words-missing"`, `"words-altered"`, `"idea-no-outcome"` | `recordRefusal` (`events.go`), `returnMissingCandidate` (`missing_candidate.go`), `recordMissingVerify` (`verify_return.go`) | `"<step>"` (single element) | Step-specific pipeline, runner, stager, verifier, or lander failures that no cross-item `threshold = 1` rule already alerts on. |
| `"eval-failed"` | `holdJudge` (`judge.go`) | `"eval-failed"` | Judge evaluator execution failure before producing a verdict; repeated across distinct items, it signals a broken evaluator setup. |
| `"trunk-already-red"` | `recordTrunkRefusal` (`trunk.go`) | `[]` (none) | Candidate is acquitted; `fixer_item` is already filed/attached and alerted by `drain-trunk-fixer-filed`. |
| `"tooling-defect"` | `holdBehindDefect` (`defect.go`) | `[]` (none) | Candidate is acquitted behind a filed `fix-defect-*` item (`defect_item`). |
| `"conflict"` (hold or `restart: true`) | `recordConflict` (`conflict.go`), `recordConflictRestart` (`conflict_restart.go`) | `[]` (none) | Concurrent trunk path overlap, owned by `land-squash-conflict`, `land-repeated-squash-conflicts`, and `drain-repeated-item-conflicts`. |
| `"reverted"` | `appendRevert` (`heavy_revert.go`) | `[]` (none) | Post-merge heavy-proof revert, already alerted immediately (`threshold = 1`, `critical`) by `drain-revert`. |
| `"plan-output-unparseable"`, `"judge-unreadable"` | `recordRefusal` (`events.go`), `holdJudge` (`judge.go`) | `[]` (none) | Already alerted immediately on the **first** occurrence (`threshold = 1`) by `drain-plan-output-unparseable` and `drain-judge-unreadable`. |
| `"judge-blocked"`, `"judge-defective"`, `"judge-defective-evidence-mismatch"`, `"judge-defective-close-refused"` | `holdJudge` (`judge.go`) | `[]` (none) | Item-specific review dispositions on individual idea/review items rather than cross-item candidate/pipeline refusals. |

When an item hits a non-countable refusal (`refusal_causes: []`, such as `"conflict"` or `"trunk-already-red"`), `observeForEachRecord` iterates zero elements: it neither increments any cause subject nor resets an unrelated cause subject that already has one refused item waiting, because `drain-repeated-refusal` defines an explicit `reset` expression ([§4.2](#42-watch-engine-evaluation-and-active-episode-seeding)).

---

## 3. The Recorded Field on `drain.refusal` ([`D-TW-RRS2`](#D-TW-RRS2))

*Operator-request and designer decision (not an owner ruling; the owner may overturn any part of this section).*

Every `drain.refusal` payload emitted by [`tools/tillr-drain/drain/`](../../../tools/tillr-drain/drain/) records a top-level non-null JSON array field `refusal_causes`:

```go
type RefusalCause struct {
	Cause string `json:"cause"`
}
```

On `refusalPayload` in [`tools/tillr-drain/drain/events.go`](../../../tools/tillr-drain/drain/events.go), `RefusalCauses []RefusalCause` carries struct tag `` `json:"refusal_causes"` `` (without `omitempty`), populated via one pure helper `computeRefusalCauses(p refusalPayload) []RefusalCause`:

1. **Always a JSON array (`[]`, never `null` or omitted):** When a step maps to zero causes under [§2.2](#22-complete-drainrefusal-step-to-cause-mapping), `computeRefusalCauses` returns a non-nil empty slice `[]RefusalCause{}` so JSON marshaling writes `"refusal_causes":[]`. In `observeForEachRecord` ([`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go)), an empty `[]any` loops zero times with `Skipped == 0`, whereas `null` or an omitted key increments `Skipped` under [`D-TW22`](../tillr-watch.md#D-TW22) and [`D-TW81`](../tillr-watch.md#D-TW81).
2. **Explicit helper call rather than `MarshalJSON` on `refusalPayload`:** `holdJudge` ([`tools/tillr-drain/drain/judge.go`](../../../tools/tillr-drain/drain/judge.go)) embeds `refusalPayload` inside an anonymous struct alongside `WriterRunID string`; defining `MarshalJSON` on `refusalPayload` would promote to the outer struct and silently drop `writer_run_id`. Calling `computeRefusalCauses` across all ten emitters (or a `newRefusalPayload` constructor used by all ten) preserves every outer and inner payload field.
3. **Why an array of objects (`refusal_causes`) instead of a scalar `cause` string:** A single `step == "gate"` refusal can fail multiple checks at once (for example `["citations", "documents"]`). A scalar string such as `"gate:citations,documents"` would fail to match a second item that failed only `"documents"`, whereas `for_each = "refusal_causes"` + `group_by = "cause"` ([`D-TW81`](../tillr-watch.md#D-TW81)) fans one `drain.refusal` out into independent observations for `"gate:citations"` and `"gate:documents"` while preserving parent fields (`item_id`, `step`, `detail`, `object_id`) on each child observation for `distinct_by = "item_id"` and guidance rendering.

---

## 4. How a Landing Resets and Clears Cause Subjects ([`D-TW-RRS3`](#D-TW-RRS3))

*Operator-request and designer decision (not an owner ruling; the owner may overturn any part of this section).*

### 4.1 Why implicit missing-field broadcast fails `R-TW-DR2` and `Skipped` accounting

In [`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go), `observeCompositeRecord` ([`D-TW50`](../tillr-watch.md#D-TW50)) broadcasts `clear_when` across matching subject prefixes when a `drain.lane-restored` event carries `lane_id` without `reason` and `clear_when` evaluates to `true`. That implicit prefix shortcut cannot be stretched to missing single-key / `for_each` subjects on `land.landing` for two reasons:

1. **Non-`landed` landing events fail `reset` and `clear_when`:** `reads.events = ["drain.refusal", "land.landing"]` delivers `land.landing` rows with `result == "squash-conflict"` and `result == "refused"` as well as `result == "landed"`. On a `squash-conflict` landing, both `reset` and `clear_when` (`event.kind == "land.landing" and event.result == "landed"`) evaluate to `false`, and `refusal_causes` is absent — indistinguishable from a malformed `drain.refusal` missing `refusal_causes` unless the rule declares which event kinds are subject-less broadcast streams. Falling through to `*skipped++` when `reset`/`clear_when` are `false` breaks `Skipped == 0` on every squash conflict (`TestDrainRepeatedRefusalRule` in [`tools/tillr-watch/repeated_refusal_rule_test.go`](../../../tools/tillr-watch/repeated_refusal_rule_test.go)); suppressing `Skipped` whenever `group_by` is missing violates [`D-TW22`](../tillr-watch.md#D-TW22) and owner ruling [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2) (`"Explicitly, we NEVER want fallbacks anywhere in this project."`).
2. **Explicit stream intent in `[rule.when]`:** Declaring `broadcast_events = ["land.landing"]` in `[rule.when]` states directly which `reads.events` kinds address all tracked subjects of the rule rather than carrying `for_each` / `group_by` fields.

### 4.2 Watch-engine evaluation and active-episode seeding

`[rule.when]` in [`tools/tillr/watch/rules.go`](../../../tools/tillr/watch/rules.go), [`tools/tillr/watch/lint_rules.go`](../../../tools/tillr/watch/lint_rules.go), [`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go), and [`tools/tillr/watch/tillr_source.go`](../../../tools/tillr/watch/tillr_source.go) gains optional `broadcast_events` (`When.BroadcastEvents []string`):

1. **Schema and `tillr watch lint` validation (`LoadRules` and `lintRuleFile`):**
   - `broadcast_events`, when present, must be a non-empty string array with no empty or duplicate entries, forbidden on `lag_behind`, projection-only `predicate_match` (`len(reads.events) == 0`), and `group_by == "global"`.
   - Requires at least one of `reset` or `clear_when` to be set.
   - Every kind in `broadcast_events` must appear in `reads.events`, and `reads.events` must retain at least one non-broadcast event kind so per-subject observations still exist.
   - `ruleDefinitionDigestInput` includes `BroadcastEvents []string` (`json:"broadcast_events,omitempty"`), keeping existing rule digests unchanged when omitted ([`D-TW66`](../tillr-watch.md#D-TW66)) while advancing `drain-repeated-refusal`'s digest on rollout so pre-upgrade history does not open retrospective alerts and any pre-upgrade `"global"` episode resolves cleanly at `startHead`.
2. **Pre-seeding open episodes across all three derivation paths:**
   - `deriveCached` (`pattern.go`), `deriveFromSource` (`pattern.go`, backed by `memoryState` in [`tools/tillr/watch/source.go`](../../../tools/tillr/watch/source.go)), and `TillrStoreSource.deriveRule` (`tillr_source.go`) pre-seed `subjects` from unresolved `alert_episodes` (`state != 'resolved'`) whenever `len(names) >= 2 || r.When.ForEach != "" || len(r.When.BroadcastEvents) > 0` (`decodeSubjectKey(key, 1)` already preserves arbitrary single-field keys verbatim, including `"gate:documents"`).
   - Pre-seeding guarantees that even if an open episode's triggering `drain.refusal` rows have aged past `window` or only tail events are scanned, a subsequent `land.landing` (`result == "landed"`) appends `landedSeq` to `c.clears` so `(episodeChanges).reconcile` ([`tools/tillr/watch/eval.go`](../../../tools/tillr/watch/eval.go)) resolves every open cause episode with `resolved_seq = landedSeq` and `resolved_reason = "condition-cleared"`.
3. **Runtime dispatch in `observeRecord` (`pattern.go`):**
   - Before `for_each` or `group_by` extraction, if `slices.Contains(r.When.BroadcastEvents, rec.Kind)`, `observeBroadcastRecord` iterates over every tracked `subject, c` in `subjects` (checking `lookupSubjectFacts` scope when projections are read), evaluates `clear_when` and `reset` against `rec.Lookup`, and **never** evaluates `match`/`after` or increments `*skipped`:
     - When `clear_when` holds: empties `c.events`, clears `c.hadPrevious` and `c.armed`, and appends `rec.Seq` to `c.clears`.
     - Otherwise, when `reset` holds: disarms `c.armed` on `predicate_match` with `after`, or empties `c.events` and appends `rec.Seq` to `c.clears` on `consecutive_count`.
     - When neither holds (such as `land.landing` with `result == "squash-conflict"` or `result == "refused"`): leaves every subject untouched with `Skipped == 0`.
   - Note also why `observeForEachRecord` preserves other causes when one item refuses: in `pattern.go`, `observeForEachRecord` clears absent subjects only when `r.When.Pattern == ConsecutiveCount && r.When.Reset == nil`. Because `drain-repeated-refusal` sets `reset = 'event.kind == "land.landing" and event.result == "landed"'`, item B failing `"gate:go-build"` leaves item A's pending `"gate:documents"` count intact until a candidate actually lands.

### 4.3 Target `config/tillr/watch/drain-repeated-refusal.toml` specification

```toml
[[rule]]
id = "drain-repeated-refusal"
stage = "drain"
severity = "high"

[rule.reads]
events = ["drain.refusal", "land.landing"]

[rule.when]
for_each = "refusal_causes"
group_by = "cause"
broadcast_events = ["land.landing"]
pattern = "consecutive_count"
match = 'event.kind == "drain.refusal"'
distinct_by = "item_id"
reset = 'event.kind == "land.landing" and event.result == "landed"'
clear_when = 'event.kind == "land.landing" and event.result == "landed"'
threshold = 2
window = "24h"

[rule.bundle]
include = ["recent_events", "run_ids"]
guidance = [
  "Repeated drain refusals share cause {{subject}}: {{count}} distinct items ({{distinct_values}}) were refused for {{subject}} with no landed commit between them (latest item {{event.item_id}}, step {{event.step}}, detail: {{event.detail}}).",
  "Run `tillr status --json` and inspect the recent `drain.refusal` events for {{distinct_values}} to check whether a shared check regression, verifier fault, or pipeline failure is blocking unrelated items.",
]
```

Per [`D-TW73`](../tillr-watch.md#D-TW73), the guidance names the shared `{{subject}}`, `{{distinct_values}}`, and inspection command without calling any refusal `"expected"` or `"normal"`, and never renders `"unknown"`.

---

## 5. First Build Slice and Candidate Note on Out-of-Scope Gaps ([`D-TW-RRS4`](#D-TW-RRS4))

*Operator-request and designer decision (not an owner ruling; the owner may overturn any part of this section).*

### 5.1 First buildable slice as one coherent behavior

Per [`../landing-principles.md` §15](../landing-principles.md#15-land-the-smallest-functional-change), shipping `refusal_causes` without the rule change or shipping `broadcast_events` without updating `drain-repeated-refusal.toml` would leave the false global alert firing in production. The smallest functional change is **one coherent slice — `drain-repeated-refusal` fires on two distinct items sharing a `refusal_causes` entry and resets on `land.landing` (`result == "landed"`)**:

1. **`tools/tillr-drain/drain/`:** Add `RefusalCause` and `computeRefusalCauses` ([§2.2](#22-complete-drainrefusal-step-to-cause-mapping), [§3](#3-the-recorded-field-on-drainrefusal-d-tw-rrs2)), populate non-null `refusal_causes` across all ten `RefusalKind` emitters, and test every step class (multi-check `"gate"`, verdict-less `"gate:no-verdict"`, `"staging:<reason>"`, step-level causes, `holdJudge` preserving `writer_run_id`, and `[]` exclusions).
2. **`tools/tillr/watch/` + `just pack-sync`:** Add `When.BroadcastEvents` parsing, `LoadRules` + `tillr watch lint` validation, `definitionHash` inclusion, active-episode subject seeding in `deriveCached` / `deriveFromSource` / `TillrStoreSource.deriveRule`, and `observeBroadcastRecord` ([§4.2](#42-watch-engine-evaluation-and-active-episode-seeding)), with unit tests covering `Store` and `ObservationSource` parity, warm `evalCache`, aged-out open episodes cleared at `landedSeq`, `Skipped == 0` on non-matching broadcast events, and `Skipped > 0` on malformed per-subject events.
3. **`config/tillr/watch/drain-repeated-refusal.toml` + `tools/tillr-watch/repeated_refusal_rule_test.go`:** Update the rule to [§4.3](#43-target-configtillrwatchdrain-repeated-refusaltoml-specification) and verify the `2026-11-24` regression directly (`"trunk-already-red"` + `"conflict"` -> `0` alerts; `"gate:documents"` + `"gate:go-build"` -> `0` alerts; two distinct items sharing `"gate:documents"` -> `1` alert on `"gate:documents"`, resolved at `landedSeq` on `land.landing` with `result == "landed"` and `Skipped == 0` across `squash-conflict` / `refused` landings).

Note on rollout window: for up to `24h` after deployment on an existing `.tillr/state.db`, pre-upgrade `drain.refusal` rows inside `[now - 24h, startHead]` lack `refusal_causes` and increment `RuleResult.Skipped` until they age past `24h`; per [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), no legacy fallback parser is added, and the changed `definitionHash` sets `loadedSeq = startHead` ([`D-TW66`](../tillr-watch.md#D-TW66)).

### 5.2 Candidate note: out-of-scope gaps (non-blocking)

Per the operator request, three adjacent areas remain out of scope for this design and its first build slice, and none is a reason to refuse this item:

1. **Drain-internal `RepeatedRefusalThreshold` and `LoopResult.RepeatedRefusal` ([`tools/tillr-drain/drain/refusal.go`](../../../tools/tillr-drain/drain/refusal.go), [`../tillr-convergence.md`](../tillr-convergence.md)):** Inside `tillr-drain`, `Loop.noteRefusedItem` (`l.refusedItems`) still counts any `RepeatedRefusalThreshold` (`2`) distinct items passing through `Loop.refuse` globally before `resetRefusedItems` on a landing (note that `recordTrunkRefusal`, `recordConflict`, and `holdBehindDefect` bypass `Loop.refuse`, whereas two unrelated `"gate"` failures on different checks still set `result.RepeatedRefusal = true` in `tillr-drain`'s summary report). Aligning `l.refusedItems` with `computeRefusalCauses` is a separate `tillr-drain` item if desired.
2. **Squash-conflict alerts ([`config/tillr/watch/land-squash-conflict.toml`](../../../config/tillr/watch/land-squash-conflict.toml), [`config/tillr/watch/land-repeated-squash-conflicts.toml`](../../../config/tillr/watch/land-repeated-squash-conflicts.toml), [`config/tillr/watch/drain-repeated-item-conflicts.toml`](../../../config/tillr/watch/drain-repeated-item-conflicts.toml)):** `land-squash-conflict` currently alerts on every squash conflict (`threshold = 1`), and `land-repeated-squash-conflicts` / `drain-repeated-item-conflicts` are per-item rules. Any tuning of squash-conflict alert volume under high lane concurrency belongs to dedicated conflict-rule work.
3. **Operator response playbook when `drain-repeated-refusal` fires ([`../tillr-watch-alert-workflow.md`](../tillr-watch-alert-workflow.md)):** Beyond the read-only diagnostic guidance in [§4.3](#43-target-configtillrwatchdrain-repeated-refusaltoml-specification), what action the whale or human operator takes (filing a fixer, pausing dispatch, or settling the episode) is governed by [`../tillr-watch-alert-workflow.md`](../tillr-watch-alert-workflow.md) and is unchanged here.

---

## 6. Rejected Alternatives

| Alternative | Why it fails | Disposition |
|---|---|---|
| **Group solely by `event.step` (`group_by = "step"`)** | Fires when two items fail completely different `"gate"` checks (e.g. `documents` vs `go-build`), and fires on two `"trunk-already-red"` or `"conflict"` holds that already have dedicated repair items and watch rules. | Rejected ([§2.1](#21-why-grouping-by-step-alone-is-both-too-coarse-and-too-broad)). |
| **Record a single scalar `cause` string on `drain.refusal`** | A multi-check `"gate"` failure (`["citations", "documents"]`) encoded as one combined string misses a second item failing `"documents"` alone, whereas `for_each = "refusal_causes"` credits each check independently. | Rejected ([§3](#3-the-recorded-field-on-drainrefusal-d-tw-rrs2)). |
| **Implicitly broadcast `reset`/`clear_when` whenever `group_by` or `for_each` is missing** | On `land.landing` with `result == "squash-conflict"` or `"refused"`, `reset` and `clear_when` are `false` and `refusal_causes` is absent — either incrementing `Skipped` on every conflict landing or silently masking malformed `drain.refusal` rows against [`D-TW22`](../tillr-watch.md#D-TW22) and [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2). | Rejected ([§4.1](#41-why-implicit-missing-field-broadcast-fails-r-tw-dr2-and-skipped-accounting)). |
| **Drop `reset` / `land.landing` and switch `drain-repeated-refusal` to `count_in_window`** | Changes the rule's core question from *"are distinct items failing on the same cause with no landing getting through?"* to a 24-hour cumulative counter that stays open even after trunk advances cleanly past the fault. | Rejected. |

---

## 7. Open Questions

No open questions block [`D-TW-RRS1`](#D-TW-RRS1)–[`D-TW-RRS4`](#D-TW-RRS4). If `go-affected` candidate-gate failures under `landing_strategy = "before-merge"` ever produce cross-item false sharing because `go_affected.py` emits `"go-affected"` as both `check_id` and `failing.ID` (while the repository's active `after-merge` policy runs `go-affected` only in the heavy tier), a follow-up item can refine `computeRefusalCauses` on `check_id == "go-affected"` to emit `"gate:go-affected:" + case.Name` from `failing.Cases` without changing the `refusal_causes` schema or watch rule.

---

## 8. Decision Ledger

[`D-TW-RRS1`](#D-TW-RRS1)–[`D-TW-RRS4`](#D-TW-RRS4) record operator-request and designer decisions (`2026-11-26`); none is an owner ruling, and the owner may overturn any part of them. `Built` is checked against `3097ac0e88a0`.

| ID | Decision | Date | Where | Built |
|---|---|---|---|---|
| <a id="D-TW-RRS1"></a>**[D-TW-RRS1](#D-TW-RRS1)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** amend [`D-TW76`](../tillr-watch.md#D-TW76) backward-only so `drain-repeated-refusal` fires at `threshold = 2` distinct `item_id`s with no landed commit between them only when both refusals share a cause key — `"gate:<id>"` per failing check (`"gate:no-verdict"` when `"gate"` has no check IDs), `"staging:<hold_reason>"`, or step-level `"fence"`, `"land"`, `"no-op"`, `"node-output-missing"`, `"verify-no-verdict"`, `"verify-not-done"`, `"plan-wrong"`, `"delivery-order"`, `"words-missing"`, `"words-altered"`, `"idea-no-outcome"`, `"eval-failed"` — while emitting `[]` for `"trunk-already-red"`, `"tooling-defect"`, `"conflict"`, `"reverted"`, `"plan-output-unparseable"`, `"judge-unreadable"`, and per-item judge review dispositions (`"judge-blocked"`, `"judge-defective"`, `"judge-defective-evidence-mismatch"`, `"judge-defective-close-refused"`). | 2026-11-26 | [§2](#2-what-two-refusals-must-share-to-fire-d-tw-rrs1) | no |
| <a id="D-TW-RRS2"></a>**[D-TW-RRS2](#D-TW-RRS2)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** record `refusal_causes` (`[]RefusalCause`, serialized as `"refusal_causes":[{"cause":"..."}]` or `"refusal_causes":[]`, never `null`) across all ten `drain.refusal` write sites in [`tools/tillr-drain/drain/`](../../../tools/tillr-drain/drain/) via an explicit helper (not embedded `MarshalJSON`), read in `drain-repeated-refusal` with `for_each = "refusal_causes"` and `group_by = "cause"`. | 2026-11-26 | [§3](#3-the-recorded-field-on-drainrefusal-d-tw-rrs2) | no |
| <a id="D-TW-RRS3"></a>**[D-TW-RRS3](#D-TW-RRS3)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** add explicit `broadcast_events` in `[rule.when]` (`tools/tillr/watch/`), validated by `LoadRules` and `tillr watch lint`, included in `definitionHash`, pre-seeding unresolved episode subjects across `deriveCached`, `deriveFromSource`, and `TillrStoreSource.deriveRule`, and evaluating only `clear_when` and `reset` across all tracked subjects with `Skipped == 0` on non-matching broadcast events (`squash-conflict` and `refused` landings). | 2026-11-26 | [§4](#4-how-a-landing-resets-and-clears-cause-subjects-d-tw-rrs3) | no |
| <a id="D-TW-RRS4"></a>**[D-TW-RRS4](#D-TW-RRS4)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** deliver `D-TW-RRS1`–`D-TW-RRS3` together as one first buildable slice (`tools/tillr-drain/drain/`, `tools/tillr/watch/`, `just pack-sync`, `config/tillr/watch/drain-repeated-refusal.toml`, and `tools/tillr-watch/repeated_refusal_rule_test.go`), leaving `drain.RepeatedRefusalThreshold`, squash-conflict rules, and operator response playbooks out of scope as recorded in [§5.2](#52-candidate-note-out-of-scope-gaps-non-blocking). | 2026-11-26 | [§5](#5-first-build-slice-and-candidate-note-on-out-of-scope-gaps-d-tw-rrs4) | no |
