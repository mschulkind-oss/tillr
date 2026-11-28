---
title: "tillr-watch: Shared-Cause Keying and Landing Reset for drain-repeated-refusal"
author: "Agent"
date: 2026-11-28
status: in-review
stage: BUILT
stage-since: 2026-11-28
tags: [tillr, watch, alerting, drain, refusals, design]
summary: "Specify what two drain.refusal events across distinct items must share for drain-repeated-refusal to fire — case plus assertion text on named gate cases and shared file path on not-done verifier returns — record refusal_causes on drain.refusal, and broadcast land.landing resets across cause subjects."
next: "The owner may review D-TW-RRS1 through D-TW-RRS5; the drain-internal RepeatedRefusalThreshold count in section 5.3 is a separate item if wanted."
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

**Status:** 2026-11-28, verified at `56607eb607f5`. [`D-TW-RRS2`](#D-TW-RRS2), [`D-TW-RRS3`](#D-TW-RRS3), and [`D-TW-RRS4`](#D-TW-RRS4) are built on trunk (`50dd5e08`, with `607d2b6c` omitting `"gate:*"` adapter wrapper entries, `da8eedec` moving core rules to [`tools/tillr-watch/rules/`](../../../tools/tillr-watch/rules/), and `a9e77e15` moving `GateRefusalCauses` into [`tools/tillr-drain/internal/runhistory/refusal.go`](../../../tools/tillr-drain/internal/runhistory/refusal.go)). [`D-TW-RRS5`](#D-TW-RRS5) — added here on `2026-11-28` to answer `conflict-run-item-request-f511444f32-a1-n1` and amend [`D-TW-RRS1`](#D-TW-RRS1) and `28c34ad7` — is built as of `2026-11-28`, at the operator's request: `GateRefusalCauses` in [`refusal.go`](../../../tools/tillr-drain/internal/runhistory/refusal.go) keys named cases on check, case and first-line assertion text, and `computeRefusalCauses` in [`events.go`](../../../tools/tillr-drain/drain/events.go) keys `"verify-not-done"` and `"eval-failed"` on finding paths through `verifyReturnCauses`. Per owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) and [`D-TW-DR1`](./decision-record-files.md#D-TW-DR1), this standalone record amends [`D-TW76`](../tillr-watch.md#D-TW76) backward-only without editing [`../tillr-watch.md`](../tillr-watch.md).

> **In short.** `drain-repeated-refusal` fans out over a recorded `refusal_causes` array on `drain.refusal` (`for_each = "refusal_causes"`, `group_by = "cause"`) — keying named gate-case failures on `"gate:<check>:<case>:<assertion>"`, keying `not-done` verifier returns solely on shared finding file paths (`"verify-not-done:<path>"`, or `[]` when no finding path is recorded) while leaving verifier faults on their own refusal steps, and resetting every cause subject on `land.landing` (`result == "landed"`) via `broadcast_events = ["land.landing"]`.

**Why it matters.** On `2026-11-24` (`09:08 ET`), [`tools/tillr-watch/rules/drain-repeated-refusal.toml`](../../../tools/tillr-watch/rules/drain-repeated-refusal.toml) landed (`0f745305`) with `group_by = "global"` and paged 78 minutes later (`10:26 ET`) on `"trunk-already-red"` plus `"conflict"` ([§1.1](#11-the-2026-11-24-incident-and-why-this-is-a-design-record)). After `50dd5e08` and `28c34ad7` narrowed grouping to refusal step and `check:case`, two hours of normal multi-lane work on `2026-11-28` (`03:46`, `04:52`, and `05:34 ET`) still produced **four** false alerts — three on `verify-not-done` across unrelated items with different verifier findings, and one on `gate:go-affected:TestArchitecture` when two candidates broke two different rules in `TestArchitecture` in their own new code ([§1.2](#12-the-2026-11-28-false-alerts-on-verify-not-done-and-testarchitecture)) — proving that step-only verifier keys and test-name-only gate keys still rise with concurrent lane activity.

**The shape.** Four coupled decisions ([`D-TW-RRS1`](#D-TW-RRS1)–[`D-TW-RRS3`](#D-TW-RRS3) as amended by [`D-TW-RRS5`](#D-TW-RRS5), with [`D-TW-RRS4`](#D-TW-RRS4) recording the initial delivery and [§5.2](#52-the-d-tw-rrs5-amendment-slice-and-architecture-constraints) the amendment slice):

- Classify every `drain.refusal` step into cause keys that match only when the underlying fault is shared: `"gate:<check>:<case>:<assertion>"` for named gate cases with assertion text (`"gate:<check>"` only when a failing check names no cases, `"gate:no-verdict"` when `"gate"` names no checks, and no fallback to test name alone), `"verify-not-done:<path>"` per normalized `item.VerdictFinding.Paths` file on `not-done` verifier returns (`[]` when no finding path is present, while verifier machinery faults stay on `"verify-no-verdict"` and `threshold = 1` `"judge-unreadable"`), `"staging:<reason>"`, and step-level pipeline causes, versus `refusal_causes: []` for refusal classes owned by dedicated alerts, repair items, concurrency rules, or per-item dispositions ([`D-TW-RRS1`](#D-TW-RRS1), [`D-TW-RRS5`](#D-TW-RRS5)).
- Record `refusal_causes` as a non-null JSON array on every `drain.refusal` payload so `for_each = "refusal_causes"` and `group_by = "cause"` credit multi-cause gate and verifier refusals independently ([`D-TW-RRS2`](#D-TW-RRS2)).
- Declare explicit `broadcast_events` in `[rule.when]` so subject-less `land.landing` events reset and clear every active and in-window cause subject on `result == "landed"` while keeping `Skipped == 0` on `squash-conflict` and `refused` landings ([`D-TW-RRS3`](#D-TW-RRS3)).

**Cost.** Zero `.tillr/state.db` schema changes and zero watch-rule schema changes beyond `50dd5e08`: [`D-TW-RRS5`](#D-TW-RRS5) updates `GateRefusalCauses` in [`tools/tillr-drain/internal/runhistory/refusal.go`](../../../tools/tillr-drain/internal/runhistory/refusal.go), `computeRefusalCauses` in [`tools/tillr-drain/drain/events.go`](../../../tools/tillr-drain/drain/events.go), `holdJudge` in [`tools/tillr-drain/drain/judge.go`](../../../tools/tillr-drain/drain/judge.go), and their tests in [`tools/tillr-drain/drain/refusal_causes_test.go`](../../../tools/tillr-drain/drain/refusal_causes_test.go) and [`tools/tillr-watch/repeated_refusal_rule_test.go`](../../../tools/tillr-watch/repeated_refusal_rule_test.go) (`just pack-sync` mirrored).

**Start at [§2](#2-what-two-refusals-must-share-to-fire-d-tw-rrs1)** for the cause taxonomy and [`D-TW-RRS5`](#D-TW-RRS5) amendment, **[§3](#3-the-recorded-field-on-drainrefusal-d-tw-rrs2)** for `refusal_causes`, **[§4](#4-how-a-landing-resets-and-clears-cause-subjects-d-tw-rrs3)** for `broadcast_events`, or **[§5](#5-first-build-slice-d-tw-rrs5-amendment-slice-and-out-of-scope-gaps-d-tw-rrs4-d-tw-rrs5)** for the build slices and out-of-scope gaps. **Needs your ruling:** none blocking; [`D-TW-RRS1`](#D-TW-RRS1)–[`D-TW-RRS5`](#D-TW-RRS5) are operator-request and designer decisions that the owner may overturn. **Reads with:** [`../tillr-watch.md`](../tillr-watch.md) ([`D-TW76`](../tillr-watch.md#D-TW76), [`D-TW81`](../tillr-watch.md#D-TW81)), [`./decision-record-files.md`](./decision-record-files.md), and [`../landing-principles.md`](../landing-principles.md) ([§15](../landing-principles.md#15-land-the-smallest-functional-change)).

---

## 1. Problem, Incident, and Provenance

### 1.1 The 2026-11-24 incident and why this is a design record

Commit `0f745305` implemented [`D-TW76`](../tillr-watch.md#D-TW76) in [`config/tillr/watch/drain-repeated-refusal.toml`](../../../tools/tillr-watch/rules/drain-repeated-refusal.toml) (`2026-11-24 09:08 ET`), counting `drain.refusal` events across `distinct_by = "item_id"` under `group_by = "global"` with `pattern = "consecutive_count"`, `threshold = 2`, and `reset` / `clear_when` set to `'event.kind == "land.landing" and event.result == "landed"'`. At `10:26 ET` on `2026-11-24`, with ten lanes working concurrently, the rule opened an alert on two items whose refusals shared nothing:

1. At `10:20 ET`, one candidate was acquitted as `step = "trunk-already-red"` (a broken trunk test whose `fix-trunk-*` repair item was already filed).
2. At `10:25 ET`, a second item hit `step = "conflict"` (a squash conflict on a design file).

Because `group_by = "global"` collapses every `drain.refusal` into one counter, any two concurrent lanes refusing for unrelated reasons between two landings page the operator. Conversely, changing `group_by` in the TOML file alone cannot fix the rule: in `observeSingleRecord` and `observeForEachRecord` ([`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go)), a non-`global` rule extracts its subject from each event's own fields, and `land.landing` ([`tools/tillr-land/land/landing.go`](../../../tools/tillr-land/land/landing.go)) carries no refusal step or cause field — so a landing would miss every cause subject and increment `RuleResult.Skipped` instead of resetting the counter.

### 1.2 The 2026-11-28 false alerts on `verify-not-done` and `TestArchitecture`

After `50dd5e08` shipped [`D-TW-RRS1`](#D-TW-RRS1)–[`D-TW-RRS4`](#D-TW-RRS4) (`607d2b6c` then omitted `"gate:*"` adapter wrapper entries and `28c34ad7` keyed gate refusals with named cases as `"gate:<check>:<case>"`), `drain-repeated-refusal` opened **four** false alerts in two hours on `2026-11-28`:

1. **Three alerts on `cause = "verify-not-done"` (`03:46`, `04:52`, and `05:34 ET`):** `"verify-not-done"` is the ordinary return of a workflow verifier's `not-done` findings to the builder (`settleJudgeFailure` in [`tools/tillr-drain/drain/judge.go`](../../../tools/tillr-drain/drain/judge.go)). In all three episodes, the two items were unrelated and their verifier findings named different files and cases.
2. **One alert on `cause = "gate:go-affected:TestArchitecture"`:** `TestArchitecture` ([`tools/tillr-drain/architecture_test.go`](../../../tools/tillr-drain/architecture_test.go)) passed on trunk, which checks four distinct architectural invariants inside one top-level Go test function (`D-GR47` import isolation, `internal/*/doc.go` layer DAG, `drainAllowlist` + 10-line `*_bridge.go` / `*_aliases.go` AST limits, and `Loop` field/mutex ceilings). One candidate had added a new domain file in `drain/` (`new domain file "..." added to drain/`), while the second had written a bridge function exceeding the 10-line limit (`function ... is N lines (max 10 for bridge wrappers)`). Grouping on `"gate:go-affected:TestArchitecture"` collapsed two distinct candidate-only mistakes into a single cross-item alert.

Filed on `2026-11-28`, the operator's request directs `drain-repeated-refusal` to group gate refusals on the failing case together with its assertion text and not to count verifier returns at all unless their findings name the same path or the same verifier fault. Plan run `run-item-request-f511444f32-a1-n1` raised `conflict-run-item-request-f511444f32-a1-n1` because the request overturns [`D-TW-RRS1`](#D-TW-RRS1) on both points and requires settling what `"the same verifier fault"` means when `item.VerdictFinding` (`Kind` in `{"gap", "regression", "unasked-change"}`) carries no fault field. [`D-TW-RRS5`](#D-TW-RRS5) ([§2.2](#22-complete-drainrefusal-step-to-cause-mapping), [§2.3](#23-case-plus-assertion-gate-keys-and-path-only-verifier-return-keys-d-tw-rrs5)) answers both points.

### 1.3 Authority, provenance, and standalone file placement

Per [`./decision-record-files.md#R-TW-DR1`](./decision-record-files.md#R-TW-DR1) and [`./decision-record-files.md#D-TW-DR1`](./decision-record-files.md#D-TW-DR1), this record lives in its own unnumbered file under `docs/design/tillr-watch/`, uses topic-derived `D-TW-RRS*` anchors, amends [`../tillr-watch.md#D-TW76`](../tillr-watch.md#D-TW76) backward-only through prose and `depends-on`, and edits zero lines of [`../tillr-watch.md`](../tillr-watch.md) or any other existing document. Updating this existing record in place with zero added files under `docs/design/tillr-watch/` satisfies Clause 2 of [`./decision-record-files.md#D-TW-DR2`](./decision-record-files.md#D-TW-DR2).

Only quoted owner statements carry owner authority here:

- **Owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`2026-11-25`, `"ok, so save that"`):** each new `tillr-watch` decision record lives in its own file under `docs/design/tillr-watch/` without editing [`../tillr-watch.md`](../tillr-watch.md).
- **Owner ruling [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2) (`2026-11-16`, `"Explicitly, we NEVER want fallbacks anywhere in this project."`):** no implicit subject-lookup fallback, test-name-only gate fallback, or legacy-event fallback parser is introduced.
- **Owner rulings [`OQ-TW2`](../tillr-watch.md#OQ-TW2), [`D-TW64`](../tillr-watch.md#D-TW64), and [`D-TW73`](../tillr-watch.md#D-TW73):** watch rules execute no remedies, alerts end in recorded actions, and rule/alert prose never calls a firing alarm `"expected"` or `"normal"`.

Everything else in this document — [`D-TW-RRS1`](#D-TW-RRS1) through [`D-TW-RRS4`](#D-TW-RRS4) (`2026-11-26`, answering the `2026-11-24` operator request) and [`D-TW-RRS5`](#D-TW-RRS5) (`2026-11-28`, answering the `2026-11-28` operator request and `conflict-run-item-request-f511444f32-a1-n1`) — is an **operator-request and designer decision, not an owner ruling, and the owner may overturn any part of it**. No unresolved owner-only policy question blocks this design.

---

## 2. What Two Refusals Must Share to Fire ([`D-TW-RRS1`](#D-TW-RRS1))

*Operator-request and designer decision (not an owner ruling; the owner may overturn any part of this section).*

### 2.1 Why grouping by `step` or test name alone is both too coarse and too broad

Grouping `drain.refusal` by `event.step` or by check-plus-test-name alone fails in three directions:

1. **Too coarse on `"gate"` and `"staging"`:** Under `step == "gate"`, item A failing fast check `documents` on a broken link in its own file and item B failing `go-build` in an unrelated package share `step == "gate"` without sharing a cause. Even within one check and one Go test (`go-affected` running `TestArchitecture`), two candidates failing two different assertions inside `TestArchitecture` share `"gate:go-affected:TestArchitecture"` while failing for unrelated candidate-introduced reasons ([§1.2](#12-the-2026-11-28-false-alerts-on-verify-not-done-and-testarchitecture)). Likewise, under `step == "staging"` (`recordItemRefusal` in [`tools/tillr-drain/drain/events.go`](../../../tools/tillr-drain/drain/events.go)), a worktree git failure (`hold_reason = "staging-failed"`) and malformed item metadata (`hold_reason = "item-unstagleable"`) are distinct failure modes.
2. **Too broad on normal `not-done` verifier returns (`"verify-not-done"` and `"eval-failed"`):** A verifier returning `not-done` findings to a builder is normal per-candidate feedback, not a pipeline failure. Counting `"verify-not-done"` (or the compatibility pre-gate judge's `"eval-failed"` `not-done` return) as a whole step pages whenever any two concurrent items receive unrelated review findings before the next landing ([§1.2](#12-the-2026-11-28-false-alerts-on-verify-not-done-and-testarchitecture)).
3. **Too broad on already-owned, concurrency, and item-disposition refusals:** With ten lanes running against `lane_base = "tip"`, two lanes hitting `"trunk-already-red"` behind an already-filed `fix-trunk-*` item (already alerted by [`drain-trunk-fixer-filed.toml`](../../../tools/tillr-watch/rules/drain-trunk-fixer-filed.toml)), two lanes hitting squash `"conflict"` on hot paths (already covered by [`land-squash-conflict.toml`](../../../tools/tillr-watch/rules/land-squash-conflict.toml), [`land-repeated-squash-conflicts.toml`](../../../tools/tillr-watch/rules/land-repeated-squash-conflicts.toml) / [`D-TW68`](../tillr-watch.md#D-TW68), and [`drain-repeated-item-conflicts.toml`](../../../tools/tillr-watch/rules/drain-repeated-item-conflicts.toml) / [`D-TW51`](../tillr-watch.md#D-TW51)), or a single occurrence already alerted at `threshold = 1` ([`drain-revert.toml`](../../../tools/tillr-watch/rules/drain-revert.toml), [`drain-plan-output-unparseable.toml`](../../../tools/tillr-watch/rules/drain-plan-output-unparseable.toml), [`drain-judge-unreadable.toml`](../../../tools/tillr-watch/rules/drain-judge-unreadable.toml)) should not open a duplicate `drain-repeated-refusal` episode.

By contrast, [`drain-repeated-verifier-refusals.toml`](../../../tools/tillr-watch/rules/drain-repeated-verifier-refusals.toml) groups by `item_id` at `threshold = 5` (one item failing verification five times in 24 hours), so it does **not** detect two *distinct* items hitting `"verify-no-verdict"` or receiving `not-done` findings on the **same** repo-relative file path back-to-back before any landing; `drain-repeated-refusal` remains the cross-item detector for those shared causes.

### 2.2 Complete `drain.refusal` step-to-cause mapping

Under [`D-TW-RRS1`](#D-TW-RRS1) as amended by [`D-TW-RRS5`](#D-TW-RRS5), two `drain.refusal` events across distinct `item_id` values with no landed candidate (`land.landing` with `result == "landed"`) between them fire `drain-repeated-refusal` if and only if their `refusal_causes` arrays share at least one `cause` string under the exhaustive table below covering all ten `RefusalKind` emitters in [`tools/tillr-drain/drain/`](../../../tools/tillr-drain/drain/):

| `step` value(s) | Producer in `tools/tillr-drain/` | Emitted `cause` key(s) in `refusal_causes` | Reason |
|---|---|---|---|
| `"gate"` with named failing cases (`FailingCase.Name != ""`) | `recordRefusal` (`drain/events.go`) via `GateRefusalCauses` (`internal/runhistory/refusal.go`) | One `"gate:" + id + ":" + name + ":" + assertion` per failing case whose trimmed first-line `Assertion` is non-empty, sorted & deduplicated; `[]` for any named case with empty `Assertion` (never falling back to `"gate:" + id + ":" + name` or `"gate:" + id`) | Fires only when two distinct items fail the **same** check, test case, and assertion text; separates unrelated candidate rule violations inside multi-invariant tests such as `TestArchitecture` ([§2.3](#23-case-plus-assertion-gate-keys-and-path-only-verifier-return-keys-d-tw-rrs5)). |
| `"gate"` with failing check(s) naming `0` cases | `recordRefusal` (`drain/events.go`) via `GateRefusalCauses` (`internal/runhistory/refusal.go`) | One `"gate:" + id` per case-less failing check (`failing.ID`, or `check.CheckID` when `check.Failing` has no non-empty `ID` and `!strings.HasPrefix(check.CheckID, "gate:")`), sorted & deduplicated | Catches shared regressions on case-less checks (`documents`, `citations`, `go-build`, `elapsed-time`, `tillr-backends`, or unnamed package compile failures) while omitting gate adapter wrappers such as `"gate:python3"` (`607d2b6c`). |
| `"gate"` with `0` non-wrapper checks | `recordRefusal` (`drain/events.go`) via `GateRefusalCauses` (`internal/runhistory/refusal.go`) | `"gate:no-verdict"` | Missing or unreadable `gate.fast.verdict.json` / `gate.verdict.json` across two items (or only `"gate:*"` wrapper entries with no inner checks) is itself a shared gate-harness fault. |
| `"verify-not-done"`, `"eval-failed"` | `recordRefusal` (`drain/events.go`), `returnMissingCandidate` (`drain/missing_candidate.go`), `holdJudge` (`drain/judge.go`) | One `"verify-not-done:" + path` per unique repo-relative file path normalized via `normalizeFindingPath` across `VerifyFeedback.Findings[].Findings[].Paths` (sorted & deduplicated); `[]` when `VerifyFeedback` is `nil` or names zero `file:` paths | Counts a `not-done` verifier or compatibility-judge return **only** when findings on two distinct items name the same repo-relative path; verifier faults live on `"verify-no-verdict"` and `"judge-unreadable"` ([§2.3](#23-case-plus-assertion-gate-keys-and-path-only-verifier-return-keys-d-tw-rrs5)). |
| `"staging"` | `recordItemRefusal` (`drain/events.go`) | `"staging:" + hold_reason` (`"staging:staging-failed"` or `"staging:item-unstagleable"`, or `"staging"` if `hold_reason` is empty) | Separates worktree/git staging failures from unstageable item definitions while catching cross-item stager faults. |
| `"fence"`, `"land"`, `"no-op"`, `"node-output-missing"`, `"verify-no-verdict"`, `"plan-wrong"`, `"delivery-order"`, `"words-missing"`, `"words-altered"`, `"idea-no-outcome"` | `recordRefusal` (`drain/events.go`), `recordMissingVerify` (`drain/verify_return.go`) | `"<step>"` (single element) | Step-specific pipeline, runner, stager, workflow-verifier (`"verify-no-verdict"`), or lander failures that no cross-item `threshold = 1` rule already alerts on. |
| `"trunk-already-red"` | `recordTrunkRefusal` (`drain/trunk.go`) | `[]` (none) | Candidate is acquitted; `fixer_item` is already filed/attached and alerted by `drain-trunk-fixer-filed`. |
| `"tooling-defect"` | `holdBehindDefect` (`drain/defect.go`) | `[]` (none) | Candidate is acquitted behind a filed `fix-defect-*` item (`defect_item`). |
| `"conflict"` (hold or `restart: true`) | `recordConflict` (`drain/conflict.go`), `recordConflictRestart` (`drain/conflict_restart.go`) | `[]` (none) | Concurrent trunk path overlap, owned by `land-squash-conflict`, `land-repeated-squash-conflicts`, and `drain-repeated-item-conflicts`. |
| `"reverted"` | `appendRevert` (`drain/heavy_revert.go`) | `[]` (none) | Post-merge heavy-proof revert, already alerted immediately (`threshold = 1`, `critical`) by `drain-revert`. |
| `"plan-output-unparseable"`, `"judge-unreadable"` | `recordRefusal` (`drain/events.go`), `holdJudge` (`drain/judge.go`) | `[]` (none) | Already alerted immediately on the **first** occurrence (`threshold = 1`) by `drain-plan-output-unparseable` and `drain-judge-unreadable`. |
| `"judge-blocked"`, `"judge-defective"`, `"judge-defective-evidence-mismatch"`, `"judge-defective-close-refused"` | `holdJudge` (`drain/judge.go`) | `[]` (none) | Item-specific review dispositions on individual idea/review items rather than cross-item candidate/pipeline refusals. |

When an item hits a non-countable refusal (`refusal_causes: []`, such as `"conflict"`, `"trunk-already-red"`, or a `"verify-not-done"` return whose findings share no path with another item), `observeForEachRecord` iterates zero elements (or records per-path keys that do not match other items' paths): it neither increments an unrelated cause subject nor resets one that already has one refused item waiting, because `drain-repeated-refusal` defines an explicit `reset` expression ([§4.2](#42-watch-engine-evaluation-and-active-episode-seeding)).

### 2.3 Case-plus-assertion gate keys and path-only verifier-return keys ([`D-TW-RRS5`](#D-TW-RRS5))

*Operator-request and designer decision (`2026-11-28`, answering `conflict-run-item-request-f511444f32-a1-n1`; not an owner ruling, and the owner may overturn any part of this subsection).*

[`D-TW-RRS5`](#D-TW-RRS5) amends [`D-TW-RRS1`](#D-TW-RRS1) (and `GateRefusalCauses` from `28c34ad7`) on the two points raised by the `2026-11-28` operator request and `conflict-run-item-request-f511444f32-a1-n1`:

1. **Gate refusals (`GateRefusalCauses` in [`tools/tillr-drain/internal/runhistory/refusal.go`](../../../tools/tillr-drain/internal/runhistory/refusal.go)):**
   - For each `check` in `checks`, inspect `check.Failing` entries with non-empty `id := strings.TrimSpace(failing.ID)` (recording `sawCheck = true`).
   - Within each such `failing`, inspect `failing.Cases` entries with non-empty `name := strings.TrimSpace(c.Name)`. Whenever at least one named case exists (`caseFound = true`), normalize `c.Assertion` by taking the substring before the first `'\n'` (if any) and calling `strings.TrimSpace` (matching the single-line `assertion:` field written by `FormatTriageBlock` in [`tools/tillr-gate/goaffected/runner.go`](../../../tools/tillr-gate/goaffected/runner.go) and read by `ReadTriageBlock` in [`tools/tillr-drain/internal/runhistory/refusal_cases.go`](../../../tools/tillr-drain/internal/runhistory/refusal_cases.go)):
     - When `assertion != ""`, emit `"gate:" + id + ":" + name + ":" + assertion`.
     - When `assertion == ""`, emit **nothing** for that case. Per the operator's request (*"Grouping by the refusal step or by the test name alone does not show that"*) and owner ruling [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2) (*"Explicitly, we NEVER want fallbacks anywhere in this project."*), `GateRefusalCauses` **never** falls back to `"gate:" + id + ":" + name` (test name alone) or `"gate:" + id` (check ID alone) when a named `FailingCase` lacks assertion text (`ExtractSigquitCases` in [`runner.go`](../../../tools/tillr-gate/goaffected/runner.go) already populates `SigquitAssertion` on timed-out tests, and `ExtractFailCases` populates `Assertion` on every `t.Error`/`t.Errorf`/`t.Fatal`/`t.Fatalf` line).
   - Only when `!caseFound` (zero named cases in `failing.Cases`) does `failing` emit `"gate:" + id`; when `check.Failing` has no non-empty `ID` and `check.CheckID != ""` without `"gate:"` prefix, `check` marks `sawCheck = true` and emits `"gate:" + check.CheckID`.
   - When `!sawCheck` (`len(checks) == 0` or only `"gate:*"` wrapper entries with no inner checks), `GateRefusalCauses` returns `[{Cause: "gate:no-verdict"}]`; when `sawCheck && len(seen) == 0` (for example, named `FailingCase`s whose `Assertion` strings were all empty), it returns the non-nil empty slice `[]storelog.RefusalCause{}`.

2. **What `"the same verifier fault"` means, and path-only keying on `"verify-not-done"` and `"eval-failed"`:**
   - **Verifier faults are already distinct refusal steps, never `VerdictFinding` values:** `item.VerdictFinding` (`UnmarshalJSON` in [`tools/tillr-item/item/verdict.go`](../../../tools/tillr-item/item/verdict.go)) admits only candidate-finding kinds `"gap"`, `"regression"`, and `"unasked-change"` alongside `Case` and `Paths` (`file:<repo-relative-path>[:line]`), and exists only after `item.CheckJudge` ([`tools/tillr-item/item/judge.go`](../../../tools/tillr-item/item/judge.go)) and `checkJudgeScope` ([`tools/tillr-drain/drain/judge.go`](../../../tools/tillr-drain/drain/judge.go)) accept a valid verdict file. Actual verifier faults never reach `verify-not-done`:
     - When a workflow evaluator exits without writing `eval.output`, `recordMissingVerify` ([`tools/tillr-drain/drain/verify_return.go`](../../../tools/tillr-drain/drain/verify_return.go)) emits `step = "verify-no-verdict"` (`RefusalVerifyNoVerdict` in [`refusal.go`](../../../tools/tillr-drain/internal/runhistory/refusal.go): *"a machinery ending, not a finding about the candidate"*), which remains a step-level cause `[{"cause": "verify-no-verdict"}]` so two distinct items hitting the same silent/broken verifier open `drain-repeated-refusal` on `"verify-no-verdict"`.
     - When a judge or workflow evaluator fails execution, produces unreadable or schema-invalid JSON, fails `checkJudgeScope`, or binds the wrong candidate head/tree, `holdJudge` ([`judge.go`](../../../tools/tillr-drain/drain/judge.go)) emits `step = "judge-unreadable"`, which already opens [`drain-judge-unreadable.toml`](../../../tools/tillr-watch/rules/drain-judge-unreadable.toml) immediately at `threshold = 1` (`refusal_causes: []`).
   - **Correcting `"eval-failed"` (`ReasonEvalFailed`) alongside `"verify-not-done"`:** Every `holdJudge` call for an execution or validation error in [`judge.go`](../../../tools/tillr-drain/drain/judge.go) passes `ReasonJudgeUnreadable`; `settleJudgeFailure` (`judge.go`) emits `ReasonEvalFailed` (`"eval-failed"`) **solely** when `c.Evaluator == nil` (compatibility pre-gate judge) and `entry.Verdict == item.VerdictNotDone`, attaching `c.verifyFeedback = judgeRefusalFindings(findings, output)` and returning the candidate to the builder (`returnedBrief` in [`tools/tillr-drain/drain/returned.go`](../../../tools/tillr-drain/drain/returned.go)). `"eval-failed"` is therefore a `not-done` verifier return (not an evaluator execution fault, correcting the `2026-11-26` table gloss) and follows the exact same path-only rule as `"verify-not-done"`. In `holdJudge` (`judge.go`), `payload.VerifyFeedback = c.verifyFeedback` (for `ReasonEvalFailed` and `ReasonJudgeBlocked`) is assigned **before** `payload.RefusalCauses = computeRefusalCauses(payload.refusalPayload)` so compatibility-judge finding paths are visible to `computeRefusalCauses`.
   - **Path extraction for `"verify-not-done"` and `"eval-failed"` in `computeRefusalCauses` ([`tools/tillr-drain/drain/events.go`](../../../tools/tillr-drain/drain/events.go)):** Iterate `p.VerifyFeedback.Findings` (`[]verifyFindingRecord`), then each record's `Findings` (`item.VerdictFindings`), then each `VerdictFinding.Paths` locator, normalizing each locator via `normalizeFindingPath` ([`tools/tillr-drain/drain/verify_triage.go`](../../../tools/tillr-drain/drain/verify_triage.go): strip `"file:"`, strip any `:line` suffix at `":"` so findings at different lines of the same broken file match on the path, and strip leading `"./"`). Emit one sorted, deduplicated `RefusalCause{Cause: "verify-not-done:" + path}` per non-empty normalized path; when `p.VerifyFeedback == nil` or zero normalized `file:` paths exist (including `returnMissingCandidate` in [`tools/tillr-drain/drain/missing_candidate.go`](../../../tools/tillr-drain/drain/missing_candidate.go) and `not-done` entries with no `findings`), return non-nil `[]RefusalCause{}`.

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

### 4.3 Shipped `tools/tillr-watch/rules/drain-repeated-refusal.toml` specification

```toml
[[rule]]
id = "drain-repeated-refusal"
scope = "core"
severity = "warning"

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

[rule.guidance]
summary = "Repeated drain refusals share cause {{subject}}: {{count}} distinct items ({{distinct_values}}) were refused for {{subject}} with no landed commit between them (latest item {{event.item_id}}, step {{event.step}}, run {{event.object_id}})"
check = [
  "Run `tillr status --json` and inspect recent drain.refusal events for items {{distinct_values}} (latest detail: {{event.detail}}) to check whether a shared check regression, verifier fault, or pipeline failure is blocking unrelated items.",
  "Inspect the candidate runs and gate verdicts for {{distinct_values}} (`tillr run show {{event.object_id}}`) before retrying queued items against {{subject}}.",
]
do = [
  "Repair the shared failure behind {{subject}} blocking {{distinct_values}}, or file and track a remediation item (`tillr watch track {{alert_id}} --item <item-id> --by <who>`).",
  "Once a landing succeeds (`land.landing` with `result == \"landed\"`), verify this alert clears automatically.",
]

[rule.delivery]
receiver = "foreground"
```

Per [`D-TW73`](../tillr-watch.md#D-TW73), the guidance names the shared `{{subject}}`, `{{distinct_values}}`, and inspection commands without calling any refusal `"expected"` or `"normal"`, and never renders `"unknown"`. Because `[rule.when]` already fans out over `for_each = "refusal_causes"` and groups on `group_by = "cause"` (`decodeSubjectKey(key, 1)` in [`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go) preserves single-field keys containing `:`, `/`, and spaces verbatim), [`D-TW-RRS5`](#D-TW-RRS5) requires no `[rule.when]` change in [`tools/tillr-watch/rules/drain-repeated-refusal.toml`](../../../tools/tillr-watch/rules/drain-repeated-refusal.toml).

---

## 5. First Build Slice, `D-TW-RRS5` Amendment Slice, and Out-of-Scope Gaps ([`D-TW-RRS4`](#D-TW-RRS4), [`D-TW-RRS5`](#D-TW-RRS5))

*Operator-request and designer decisions (not owner rulings; the owner may overturn any part of this section).*

### 5.1 Initial build slice (`D-TW-RRS4`, shipped in `50dd5e08`)

Per [`../landing-principles.md` §15](../landing-principles.md#15-land-the-smallest-functional-change), shipping `refusal_causes` without the rule change or shipping `broadcast_events` without updating `drain-repeated-refusal.toml` would have left the false global alert firing in production. Commit `50dd5e08` delivered [`D-TW-RRS1`](#D-TW-RRS1)–[`D-TW-RRS3`](#D-TW-RRS3) together as one slice across [`tools/tillr-drain/drain/`](../../../tools/tillr-drain/drain/), [`tools/tillr/watch/`](../../../tools/tillr/watch/), `just pack-sync`, [`tools/tillr-watch/rules/drain-repeated-refusal.toml`](../../../tools/tillr-watch/rules/drain-repeated-refusal.toml) (moved from `config/tillr/watch/` in `da8eedec`), and [`tools/tillr-watch/repeated_refusal_rule_test.go`](../../../tools/tillr-watch/repeated_refusal_rule_test.go).

### 5.2 The `D-TW-RRS5` amendment slice and architecture constraints

[`D-TW-RRS5`](#D-TW-RRS5) ships as **one commit** updating cause derivation and its unit and watch-rule tests:

1. **[`tools/tillr-drain/internal/runhistory/refusal.go`](../../../tools/tillr-drain/internal/runhistory/refusal.go) (`GateRefusalCauses`):** Key named `FailingCase`s with non-empty trimmed first-line `Assertion` as `"gate:" + id + ":" + name + ":" + assertion`, emit no cause for named `FailingCase`s with empty `Assertion` (returning non-nil `[]storelog.RefusalCause{}` when `sawCheck && len(seen) == 0`, and `"gate:no-verdict"` only when `!sawCheck`), and keep `"gate:" + id` solely for failing checks with zero named cases ([§2.3](#23-case-plus-assertion-gate-keys-and-path-only-verifier-return-keys-d-tw-rrs5)). Keep the `GateRefusalCauses([]EndingCheck)` signature unchanged so its 3-line wrapper in [`tools/tillr-drain/drain/runhistory_bridge.go`](../../../tools/tillr-drain/drain/runhistory_bridge.go) needs no edit.
2. **[`tools/tillr-drain/drain/events.go`](../../../tools/tillr-drain/drain/events.go) (`computeRefusalCauses`) and [`tools/tillr-drain/drain/judge.go`](../../../tools/tillr-drain/drain/judge.go) (`holdJudge`):** Key `RefusalVerifyNotDone` and `ReasonEvalFailed` on sorted, deduplicated `"verify-not-done:" + path` entries normalized via `normalizeFindingPath` ([`tools/tillr-drain/drain/verify_triage.go`](../../../tools/tillr-drain/drain/verify_triage.go)) from `p.VerifyFeedback.Findings[].Findings[].Paths`, returning non-nil `[]RefusalCause{}` when `p.VerifyFeedback` carries zero normalized `file:` paths; in `holdJudge`, assign `payload.VerifyFeedback = c.verifyFeedback` before calling `computeRefusalCauses(payload.refusalPayload)`. **Architecture guardrail (`TestArchitecture` in [`tools/tillr-drain/architecture_test.go`](../../../tools/tillr-drain/architecture_test.go)):** add **zero** new `.go` domain files under `tools/tillr-drain/drain/` (`drainAllowlist`) and keep every function in `tools/tillr-drain/drain/*_bridge.go` and `*_aliases.go` at $\le 10$ lines.
3. **[`tools/tillr-drain/drain/refusal_causes_test.go`](../../../tools/tillr-drain/drain/refusal_causes_test.go) and [`tools/tillr-watch/repeated_refusal_rule_test.go`](../../../tools/tillr-watch/repeated_refusal_rule_test.go) (+ `just format` and `just pack-sync`):** Cover the `2026-11-28` regressions directly:
   - two `TestArchitecture` (or other `go-affected`) gate refusals across distinct items with **different** `FailingCase.Assertion` texts -> distinct `"gate:go-affected:TestArchitecture:<assertion>"` causes -> `0` alerts;
   - two gate refusals across distinct items sharing check, `FailingCase.Name`, and non-empty `FailingCase.Assertion` -> `1` alert on `"gate:<check>:<case>:<assertion>"`;
   - named `FailingCase` with empty `Assertion` -> `[]` (never `"gate:<check>:<case>"`, `"gate:<check>"`, or `"gate:no-verdict"`);
   - two `verify-not-done` (and `eval-failed`) refusals across distinct items with empty `VerifyFeedback` or findings on **different** paths -> `0` alerts;
   - two `verify-not-done` refusals across distinct items whose findings name the **same** repo-relative path (including at different `:line` numbers) -> `1` alert on `"verify-not-done:<path>"`;
   - two `verify-no-verdict` refusals across distinct items -> `1` alert on `"verify-no-verdict"`.

### 5.3 Candidate note: out-of-scope gaps (non-blocking)

Per the operator requests, three adjacent areas remain out of scope and none is a reason to refuse the build item:

1. **Drain-internal `RepeatedRefusalThreshold` and `LoopResult.RepeatedRefusal` ([`tools/tillr-drain/drain/refusal.go`](../../../tools/tillr-drain/drain/refusal.go), [`tools/tillr-drain/internal/runhistory/refusal.go`](../../../tools/tillr-drain/internal/runhistory/refusal.go), [`../tillr-convergence.md`](../tillr-convergence.md)):** Inside `tillr-drain`, `Loop.noteRefusedItem` (`l.refusedItems`) still counts any `RepeatedRefusalThreshold` (`2`) distinct items passing through `Loop.refuse` globally before `resetRefusedItems` on a landing. Aligning `l.refusedItems` with `computeRefusalCauses` is a separate `tillr-drain` item if desired.
2. **Squash-conflict alerts ([`tools/tillr-watch/rules/land-squash-conflict.toml`](../../../tools/tillr-watch/rules/land-squash-conflict.toml), [`tools/tillr-watch/rules/land-repeated-squash-conflicts.toml`](../../../tools/tillr-watch/rules/land-repeated-squash-conflicts.toml), [`tools/tillr-watch/rules/drain-repeated-item-conflicts.toml`](../../../tools/tillr-watch/rules/drain-repeated-item-conflicts.toml)):** `land-squash-conflict` alerts on every squash conflict (`threshold = 1`), and `land-repeated-squash-conflicts` / `drain-repeated-item-conflicts` are per-item rules. Any tuning of squash-conflict alert volume under high lane concurrency belongs to dedicated conflict-rule work.
3. **Operator response playbook when `drain-repeated-refusal` fires ([`../tillr-watch-alert-workflow.md`](../tillr-watch-alert-workflow.md)):** Beyond the diagnostic guidance in [§4.3](#43-shipped-toolstillr-watchrulesdrain-repeated-refusaltoml-specification), what action the whale or human operator takes (filing a fixer, pausing dispatch, or settling the episode) is governed by [`../tillr-watch-alert-workflow.md`](../tillr-watch-alert-workflow.md) and is unchanged here.

---

## 6. Rejected Alternatives

| Alternative | Why it fails | Disposition |
|---|---|---|
| **Group solely by `event.step` (`group_by = "step"`)** | Fires when two items fail completely different `"gate"` checks (e.g. `documents` vs `go-build`), and fires on two `"trunk-already-red"` or `"conflict"` holds that already have dedicated repair items and watch rules. | Rejected ([§2.1](#21-why-grouping-by-step-or-test-name-alone-is-both-too-coarse-and-too-broad)). |
| **Group named gate cases by `check + case` without `Assertion` (`"gate:go-affected:TestArchitecture"`), or fall back to `check + case` when `Assertion` is empty** | Opened a false alert on `2026-11-28` when two candidates broke two different architecture rules inside `TestArchitecture` while trunk was green; falling back to test-name-only when `Assertion` is empty repeats the same false grouping and violates [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2). | Rejected ([§1.2](#12-the-2026-11-28-false-alerts-on-verify-not-done-and-testarchitecture), [§2.3](#23-case-plus-assertion-gate-keys-and-path-only-verifier-return-keys-d-tw-rrs5)). |
| **Count `"verify-not-done"` or `"eval-failed"` as a whole step, or keep `:line` numbers on `"verify-not-done:<path>"`** | Whole-step `"verify-not-done"` opened three false alerts in two hours on `2026-11-28` across unrelated items with unrelated verifier findings; keeping `:line` misses two verifiers flagging different lines of the same broken file. | Rejected ([§1.2](#12-the-2026-11-28-false-alerts-on-verify-not-done-and-testarchitecture), [§2.3](#23-case-plus-assertion-gate-keys-and-path-only-verifier-return-keys-d-tw-rrs5)). |
| **Record a single scalar `cause` string on `drain.refusal`** | A multi-check `"gate"` failure (`["citations", "documents"]`) or multi-path verifier return encoded as one combined string misses a second item sharing only one check or path, whereas `for_each = "refusal_causes"` credits each cause independently. | Rejected ([§3](#3-the-recorded-field-on-drainrefusal-d-tw-rrs2)). |
| **Implicitly broadcast `reset`/`clear_when` whenever `group_by` or `for_each` is missing** | On `land.landing` with `result == "squash-conflict"` or `"refused"`, `reset` and `clear_when` are `false` and `refusal_causes` is absent — either incrementing `Skipped` on every conflict landing or silently masking malformed `drain.refusal` rows against [`D-TW22`](../tillr-watch.md#D-TW22) and [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2). | Rejected ([§4.1](#41-why-implicit-missing-field-broadcast-fails-r-tw-dr2-and-skipped-accounting)). |
| **Drop `reset` / `land.landing` and switch `drain-repeated-refusal` to `count_in_window`** | Changes the rule's core question from *"are distinct items failing on the same cause with no landing getting through?"* to a 24-hour cumulative counter that stays open even after trunk advances cleanly past the fault. | Rejected. |

---

## 7. Open Questions

None.

---

## 8. Decision Ledger

[`D-TW-RRS1`](#D-TW-RRS1)–[`D-TW-RRS5`](#D-TW-RRS5) record operator-request and designer decisions (`2026-11-26` and `2026-11-28`); none is an owner ruling, and the owner may overturn any part of them. `Built` is checked against `56607eb607f5`, except [`D-TW-RRS5`](#D-TW-RRS5), built on `2026-11-28` in the change that records it here.

| ID | Decision | Date | Where | Built |
|---|---|---|---|---|
| <a id="D-TW-RRS1"></a>**[D-TW-RRS1](#D-TW-RRS1)** | **Operator request and designer decision (not an owner ruling; owner may overturn; amended on gate-case and verifier-return keys by [`D-TW-RRS5`](#D-TW-RRS5)):** amend [`D-TW76`](../tillr-watch.md#D-TW76) backward-only so `drain-repeated-refusal` fires at `threshold = 2` distinct `item_id`s with no landed commit between them only when both refusals share a `refusal_causes` key — `"gate:<check>:<case>:<assertion>"` per named failing case with assertion text, `"gate:<check>"` per case-less non-wrapper failing check, `"gate:no-verdict"` when `"gate"` names zero non-wrapper checks, `"verify-not-done:<path>"` per normalized repo-relative `item.VerdictFinding.Paths` file on `"verify-not-done"` and `"eval-failed"` (`[]` when no finding path is present), `"staging:<hold_reason>"`, or step-level `"fence"`, `"land"`, `"no-op"`, `"node-output-missing"`, `"verify-no-verdict"`, `"plan-wrong"`, `"delivery-order"`, `"words-missing"`, `"words-altered"`, `"idea-no-outcome"` — while emitting `[]` for `"trunk-already-red"`, `"tooling-defect"`, `"conflict"`, `"reverted"`, `"plan-output-unparseable"`, `"judge-unreadable"`, and per-item judge review dispositions (`"judge-blocked"`, `"judge-defective"`, `"judge-defective-evidence-mismatch"`, `"judge-defective-close-refused"`). | 2026-11-26 | [§2](#2-what-two-refusals-must-share-to-fire-d-tw-rrs1) | yes (`50dd5e08`, `607d2b6c`, `28c34ad7`; amended by [`D-TW-RRS5`](#D-TW-RRS5), built `2026-11-28`) |
| <a id="D-TW-RRS2"></a>**[D-TW-RRS2](#D-TW-RRS2)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** record `refusal_causes` (`[]RefusalCause`, serialized as `"refusal_causes":[{"cause":"..."}]` or `"refusal_causes":[]`, never `null`) across all ten `drain.refusal` write sites in [`tools/tillr-drain/drain/`](../../../tools/tillr-drain/drain/) via an explicit helper (not embedded `MarshalJSON`), read in `drain-repeated-refusal` with `for_each = "refusal_causes"` and `group_by = "cause"`. | 2026-11-26 | [§3](#3-the-recorded-field-on-drainrefusal-d-tw-rrs2) | yes (`50dd5e08`) |
| <a id="D-TW-RRS3"></a>**[D-TW-RRS3](#D-TW-RRS3)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** add explicit `broadcast_events` in `[rule.when]` (`tools/tillr/watch/`), validated by `LoadRules` and `tillr watch lint`, included in `definitionHash`, pre-seeding unresolved episode subjects across `deriveCached`, `deriveFromSource`, and `TillrStoreSource.deriveRule`, and evaluating only `clear_when` and `reset` across all tracked subjects with `Skipped == 0` on non-matching broadcast events (`squash-conflict` and `refused` landings). | 2026-11-26 | [§4](#4-how-a-landing-resets-and-clears-cause-subjects-d-tw-rrs3) | yes (`50dd5e08`) |
| <a id="D-TW-RRS4"></a>**[D-TW-RRS4](#D-TW-RRS4)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** deliver `D-TW-RRS1`–`D-TW-RRS3` together as one initial slice (`tools/tillr-drain/drain/`, `tools/tillr/watch/`, `just pack-sync`, [`tools/tillr-watch/rules/drain-repeated-refusal.toml`](../../../tools/tillr-watch/rules/drain-repeated-refusal.toml), and [`tools/tillr-watch/repeated_refusal_rule_test.go`](../../../tools/tillr-watch/repeated_refusal_rule_test.go)), leaving `drain.RepeatedRefusalThreshold`, squash-conflict rules, and operator response playbooks out of scope as recorded in [§5.3](#53-candidate-note-out-of-scope-gaps-non-blocking). | 2026-11-26 | [§5.1](#51-initial-build-slice-d-tw-rrs4-shipped-in-50dd5e08), [§5.3](#53-candidate-note-out-of-scope-gaps-non-blocking) | yes (`50dd5e08`, `da8eedec`, `a9e77e15`) |
| <a id="D-TW-RRS5"></a>**[D-TW-RRS5](#D-TW-RRS5)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** answering `conflict-run-item-request-f511444f32-a1-n1` on the `2026-11-28` operator request, amend [`D-TW-RRS1`](#D-TW-RRS1) in one commit (`just pack-sync` mirrored, zero new `.go` domain files in `drain/` and $\le 10$-line wrappers in `*_bridge.go`): (1) in `GateRefusalCauses` ([`tools/tillr-drain/internal/runhistory/refusal.go`](../../../tools/tillr-drain/internal/runhistory/refusal.go)), key every named `FailingCase` (`Name != ""`) whose trimmed first-line `Assertion` is non-empty as `"gate:" + id + ":" + name + ":" + assertion`, emit nothing for named `FailingCase`s with empty `Assertion` (never falling back to `"gate:" + id + ":" + name` or `"gate:" + id`, per [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), and returning `[]` rather than `"gate:no-verdict"` whenever `sawCheck` holds), and emit `"gate:" + id` only when a failing check has zero named cases; (2) because `item.VerdictFinding` classifies candidate defects only (`gap`, `regression`, `unasked-change`) while verifier faults already emit dedicated refusal steps (`"verify-no-verdict"` counted at `threshold = 2`, `"judge-unreadable"` alerted at `threshold = 1` by `drain-judge-unreadable`), key both `not-done` verifier-return steps (`"verify-not-done"` and compatibility-judge `"eval-failed"`, setting `payload.VerifyFeedback` before `computeRefusalCauses` in `holdJudge` in [`tools/tillr-drain/drain/judge.go`](../../../tools/tillr-drain/drain/judge.go)) in `computeRefusalCauses` ([`tools/tillr-drain/drain/events.go`](../../../tools/tillr-drain/drain/events.go)) solely on sorted, deduplicated `"verify-not-done:" + path` entries normalized via `normalizeFindingPath` (stripping `"file:"`, `:line`, and `"./"`), returning `[]` whenever `VerifyFeedback` names zero `file:` paths. | 2026-11-28 | [§2.2](#22-complete-drainrefusal-step-to-cause-mapping), [§2.3](#23-case-plus-assertion-gate-keys-and-path-only-verifier-return-keys-d-tw-rrs5), [§5.2](#52-the-d-tw-rrs5-amendment-slice-and-architecture-constraints) | yes (`2026-11-28`, at the operator's request: `GateRefusalCauses` and `verifyReturnCauses`, with `TestComputeRefusalCauses` and `TestDrainRepeatedRefusalRule`) |
