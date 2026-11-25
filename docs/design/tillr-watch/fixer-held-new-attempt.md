---
title: "Alert when an item held by a trunk fixer starts a new attempt instead of gating its kept candidate"
author: "Agent"
date: 2026-11-25
status: in-review
stage: DECIDED
stage-since: 2026-11-25
tags: [tillr, watch, drain, gate, fixer, design]
summary: "Answer conflict-run-item-request-8fdabc44ba-a1-n1 by specifying kept-candidate drain.fixer-attached attribution, post-hold gateJudgmentEpoch advancement, after and reset on predicate_match, and config/tillr/watch/drain-fixer-held-new-attempt.toml."
next: "Build item-request-8fdabc44ba in one delivery covering tools/tillr-drain/drain/, tools/tillr/watch/, just pack-sync, config/tillr/watch/drain-fixer-held-new-attempt.toml, and scratch-store tests; the owner may review D-TW-FHA1."
depends-on:
  - ../tillr-watch.md
  - ../tillr-watch.md#D-TW13
  - ../tillr-watch.md#D-TW16
  - ../tillr-watch.md#D-TW42
  - ../tillr-watch.md#D-TW77
  - ./decision-record-files.md#R-TW-DR1
  - ./decision-record-files.md#D-TW-DR1
  - ../landing-principles.md#15-land-the-smallest-functional-change
related:
  - ../tillr-utility-contracts/tillr-watch.md
vantage:
  status-chip: true
---

# Alert when an item held by a trunk fixer starts a new attempt instead of gating its kept candidate

**Status:** 2026-11-25, verified at `95d7043eb6`. Nothing specified in [`D-TW-FHA1`](#D-TW-FHA1) is built yet (`test -f config/tillr/watch/drain-fixer-held-new-attempt.toml` exits non-zero, `rg -n 'kept_candidate' tools/tillr-drain` returns nothing, and `PredicateMatch` in [`tools/tillr/watch/rules.go`](../../../tools/tillr/watch/rules.go) accepts neither `after` nor `reset`). Per owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) and [`D-TW-DR1`](./decision-record-files.md#D-TW-DR1), this standalone record expands and governs [`D-TW77`](../tillr-watch.md#D-TW77) without editing [`../tillr-watch.md`](../tillr-watch.md).

> **In short.** When a finished candidate is acquitted and held behind a `fixer-main-*` trunk fixer, the drain must gate that kept candidate once the fixer closes rather than burning a fresh worker attempt. Recording kept-candidate `drain.fixer-attached` after the pre-hold gate verdict, advancing `gateJudgmentEpoch` across `trunk-already-red` holds, and adding `after` and `reset` to `predicate_match` let one declarative rule in `config/tillr/watch/drain-fixer-held-new-attempt.toml` catch any bypassed re-gate without false-firing on pre-gate rebase work or post-gate retries.

**Why it matters.** On 2026-11-23, `item-request-b70a54f954` held a finished candidate behind a trunk fixer for `3h 58m` (`04:55` to `08:53 ET`) and then started `run-item-request-b70a54f954-a2-n1` instead of gating the kept candidate, discarding a finished attempt with zero alerts ([§1.1](#11-incident-filed-request-and-the-definition-conflict-this-record-answers)).

**The shape.** `Loop.verify` appends a run-scoped `drain.fixer-attached` (`kept_candidate: true`, carrying `item_id`) after `gate.finishJudgment()`, `predicate_match` gains optional `after` and `reset`, and `drain-fixer-held-new-attempt` arms on that hold, disarms on `gate.started` or `gate.verdict`, and fires if `run.allocate` starts a new attempt's worker run first ([§2](#2-producer-watch-engine-and-rule-contract-d-tw-fha1)).

**Cost.** Four touched areas in one build — `tools/tillr-drain/drain/`, `tools/tillr/watch/`, `just pack-sync` mirrors, and `config/tillr/watch/drain-fixer-held-new-attempt.toml` plus scratch-store tests — with zero schema migrations and zero changes to `tillr-view` or `fixer_resolve.go` ([§3](#3-how-change-nothing-else-governs-build-scope-and-verification-d-tw-fha1)).

**Start at [§2](#2-producer-watch-engine-and-rule-contract-d-tw-fha1)** for the exact event payloads, watch-engine rules, and TOML file, and [§6](#6-decision-ledger) for the provenance of [`D-TW-FHA1`](#D-TW-FHA1).

---

## 1. Problem and Provenance

### 1.1 Incident, filed request, and the definition conflict this record answers

On 2026-11-23 at `04:55 ET`, `item-request-b70a54f954` finished its candidate and was held `blocked` behind a trunk fixer (`trunk-already-red`). When that fixer closed `3h 58m` later (`08:53 ET`), `releaseIdleHeldCandidates` had retired the unexamined kept candidate during the hold, so the planner dispatched a new attempt (`run-item-request-b70a54f954-a2-n1`) instead of gating the kept candidate, with no `tillr-watch` alert opened. Commit `c7361cae` (`item-request-310fbd6b5d`) fixed the drain transition so kept candidates in `trunkWaits` survive `releaseIdleHeldCandidates` and return to `Gate` via `releaseTrunkAcquittals` in [`tools/tillr-drain/drain/trunk_release.go`](../../../tools/tillr-drain/drain/trunk_release.go).

Filed operator request `item-request-8fdabc44ba` asks to add one `tillr-watch` rule in its own new file under `config/tillr/watch`, reading `drain.fixer-attached` (registered as `FixerAttachedKind` in [`tools/tillr-drain/drain/events.go`](../../../tools/tillr-drain/drain/events.go)) and the registered event that starts a worker run for the item's next attempt (`run.allocate`, registered as `AllocateKind` in [`tools/tillr-run/run/schema.go`](../../../tools/tillr-run/run/schema.go)), changing nothing else, with the drain fix itself (`item-request-310fbd6b5d`) out of scope.

Build worker `run-item-request-8fdabc44ba-a1-n1` stopped with `OBSTACLE: definition` (`conflict-run-item-request-8fdabc44ba-a1-n1` on criteria `c1` and `c5`) after commit `c65c0004c4` condensed [`../tillr-watch.md#D-TW77`](../tillr-watch.md#D-TW77) to a five-line summary, surfacing three concrete incompatibilities against `95d7043eb6`:

1. **`drain.fixer-attached` carries no held-item `item_id` and is not emitted on new or exact-SHA trunk fixers.** In `routeTrunkFixer` ([`tools/tillr-drain/drain/trunk.go`](../../../tools/tillr-drain/drain/trunk.go)), `FixerAttachedKind` is appended only when attaching checks to an already-open older `fixer-main-*` item, with `ObjectType = "run"`, `ObjectID = fixerID`, and payload `{"fixer_item", "red_sha", "failing_checks", "allowed_paths"}`; creating a new `fixer-main-<sha>` (`item.add`) or reusing an exact `fixer-main-<sha>` emits no `drain.fixer-attached`, and `fileToolingDefect` ([`tools/tillr-drain/drain/defect.go`](../../../tools/tillr-drain/drain/defect.go)) emits `drain.fixer-attached` on `defect-*` items with no `item_id`. In addition, `acquitRedWithPaths` is called both from candidate gate failure in `Loop.verify` and from `routeObstacle` ([`tools/tillr-drain/drain/obstacle_route.go`](../../../tools/tillr-drain/drain/obstacle_route.go)) on worker `OBSTACLE: trunk`, where no kept candidate exists (`abandonFromBase`). Meanwhile, `heavyMissSubject` and `heavyMissCreationLog` in [`tools/tillr-view/heavy_misses.go`](../../../tools/tillr-view/heavy_misses.go) and `FixerSeen` in [`tools/tillr-drain/fixer/fixer_resolve.go`](../../../tools/tillr-drain/fixer/fixer_resolve.go) require fixer-scoped `drain.fixer-attached` events to retain `ObjectID = fixerID` and never appear in the `item.add` creation transaction.
2. **Distinguishing a bypassed re-gate from a post-gate retry requires an ordered arm-and-disarm pattern and post-hold verdict recording.** In [`tools/tillr/watch/rules.go`](../../../tools/tillr/watch/rules.go) and [`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go), `PredicateMatch` rejects `after` and `reset`: without arming on `drain.fixer-attached` and disarming when the kept candidate's gate runs (`gate.started`, added in `a7bac4c2`, or `gate.verdict`), a rule either alerts on every ordinary `run.allocate` or alerts falsely when a kept candidate *is* gated again, fails on its own defect, and legitimately starts its next attempt. In [`tools/tillr-drain/drain/events.go`](../../../tools/tillr-drain/drain/events.go), `gateJudgmentEpoch` advances only on `drain.gate-stall` and `drain.item-fault.cleared`, so re-gating the same `run_id` after a `trunk-already-red` hold hits `judgmentSeen` deduplication and skips appending the post-hold `gate.verdict` unless `gateJudgmentEpoch` also advances across kept-candidate `trunk-already-red` holds.
3. **Pre-gate rebase onto a repaired trunk allocates same-attempt `conflictFix` or re-judge runs before `gate.started`.** When a trunk fixer lands, `Loop.landCandidate` records `recordTrunkRepair` ([`tools/tillr-drain/drain/loop.go`](../../../tools/tillr-drain/drain/loop.go)). When the released kept candidate enters `Loop.verify`, `preRebaseRepairedTrunk` ([`tools/tillr-drain/drain/trunk_repair.go`](../../../tools/tillr-drain/drain/trunk_repair.go)) rebases it onto `main` *before* `gate.started`: a rebase conflict dispatches `conflictFixRunID(candidate.RunID)` (`-cfix` in [`tools/tillr-drain/drain/conflict.go`](../../../tools/tillr-drain/drain/conflict.go)), and a clean rebase sets `candidate.RebasedFromHead` so `judgeCandidate` ([`tools/tillr-drain/drain/judge.go`](../../../tools/tillr-drain/drain/judge.go)) dispatches `markRunID(req.RunID, "fresh")` (on `seat = "review"` or workflow evaluator `seat = "verify"`) before `gate.started`. Neither run starts a new attempt; matching bare `run.allocate` without excluding judge/evaluator seats and same-node continuation/repair suffixes would false-fire on the healthy repaired-trunk path.

### 1.2 Authority boundary: owner rulings versus designer decision

This record obeys four standing owner rulings without altering them:

- [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`2026-11-25`, `"ok, so save that"`): every new `tillr-watch` decision record lives in its own file under `docs/design/tillr-watch/`, with zero edits to [`../tillr-watch.md`](../tillr-watch.md).
- [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2) (`2026-11-16`, `"Explicitly, we NEVER want fallbacks anywhere in this project."`): no fallback path or silent degradation.
- [`OQ-TW2`](../tillr-watch.md#OQ-TW2) (`2026-11-21`, `"We don't cover issues. We use this to find the issues and fix them."`): `tillr-watch` executes no automatic remedy.
- [`D-TW73`](../tillr-watch.md#D-TW73) (`2026-11-22`, `"I never want alerts for expected conditions."`): `[rule.when]` excludes normal pre-gate rebase and post-gate retry flows, and `[rule.guidance]` never calls any firing expected.

Everything else in this document — [`D-TW-FHA1`](#D-TW-FHA1), answering `conflict-run-item-request-8fdabc44ba-a1-n1` for operator request `item-request-8fdabc44ba`, expanding [`D-TW77`](../tillr-watch.md#D-TW77), and amending [`D-TW13`](../tillr-watch.md#D-TW13), [`D-TW16`](../tillr-watch.md#D-TW16), and [`D-TW42`](../tillr-watch.md#D-TW42) backward-only — is an **operator-request and designer decision (`2026-11-25`), not an owner ruling, and the owner may overturn any part of it**.

---

## 2. Producer, Watch-Engine, and Rule Contract ([`D-TW-FHA1`](#D-TW-FHA1))

*Non-owner decision (operator request `item-request-8fdabc44ba` and designer ruling answering `conflict-run-item-request-8fdabc44ba-a1-n1`; the owner may overturn any part of this section).*

### 2.1 Kept-candidate `drain.fixer-attached` attribution and `gateJudgmentEpoch` in `tools/tillr-drain/drain/`

1. **Preserve fixer-scoped and defect-scoped `drain.fixer-attached` readers (`skipped = 0` under [`D-TW22`](../tillr-watch.md#D-TW22)):**
   - In `routeTrunkFixer` ([`tools/tillr-drain/drain/trunk.go`](../../../tools/tillr-drain/drain/trunk.go)), keep `ObjectType = "run"` and `ObjectID = fixerID` unchanged when attaching checks to an existing `fixer-main-*` item, and add `"item_id": fixerID` and `"kept_candidate": false` to the JSON payload.
   - In `fileToolingDefect` ([`tools/tillr-drain/drain/defect.go`](../../../tools/tillr-drain/drain/defect.go)), keep `ObjectType = "item"` and `ObjectID = id` unchanged when attaching a repeat tooling-defect report, and add `"item_id": id` and `"kept_candidate": false` to `defectAttachedPayload`.
   - Keeping `ObjectID` and emission timing unchanged preserves `heavyMissSubject` / `heavyMissCreationLog` in [`tools/tillr-view/heavy_misses.go`](../../../tools/tillr-view/heavy_misses.go) and `FixerSeen` in [`tools/tillr-drain/fixer/fixer_resolve.go`](../../../tools/tillr-drain/fixer/fixer_resolve.go), while `"item_id"` plus `"kept_candidate": false` ensures `group_by = "item_id"` skips zero events (`Skipped == 0`) and never arms `fixer-main-*` or `defect-*` items on their own attachments.
2. **Append kept-candidate `drain.fixer-attached` after `gate.finishJudgment()` in `Loop.verify`:**
   - Add unexported `trunkFixer *trunkFixerHold` (`fixerID string`, `redSHA string`, `failingChecks []string`) to `Candidate` in [`tools/tillr-drain/drain/loop.go`](../../../tools/tillr-drain/drain/loop.go). Populate `candidate.trunkFixer` inside `acquitRedWithPaths` ([`tools/tillr-drain/drain/trunk.go`](../../../tools/tillr-drain/drain/trunk.go)) right alongside `candidate.trunkAcquitted = true`, covering all three `routeTrunkFixer` branches: newly filed `fixer-main-<sha>`, reused exact `fixer-main-<sha>`, and check-overlap attachment onto an open `fixer-main-*`. Holds behind a non-fixer open test item (`holdForOpenTestFix` in [`tools/tillr-drain/drain/test_fix_hold.go`](../../../tools/tillr-drain/drain/test_fix_hold.go), which emits `drain.trunk-failure-linked`) leave `candidate.trunkFixer == nil`.
   - In `Loop.verify` ([`tools/tillr-drain/drain/loop.go`](../../../tools/tillr-drain/drain/loop.go)), inside the `GateFailed` branch's `if acquitted` block **after** `if err := gate.finishJudgment(); err != nil`, when `candidate.trunkFixer != nil && l.cfg.Store != "" && candidate.RunID != ""`, call `l.recordKeptCandidateFixerAttached(ctx, candidate, *candidate.trunkFixer)`:
     - Appending after `gate.finishJudgment()` guarantees `seq(drain.fixer-attached) > seq(pre-hold gate.started)` and `seq(drain.fixer-attached) > seq(pre-hold gate.verdict)` whether `recordGateJudgment` ran eagerly in `finishDetachedGate` or deferred through `outcome.finish`, so the pre-hold gate's own events never disarm the hold; and placing the call in `Loop.verify` excludes worker `OBSTACLE: trunk` holds in `obstacle_route.go` (where no kept candidate exists), fork-rebase waits, and `HeavyRunner`.
     - Inside one `WriteTx`, read `afterGateSeq = COALESCE(MAX(seq), 0)` from `events` where `kind = 'gate.verdict' AND object_type = 'run' AND object_id = candidate.RunID`; if zero `drain.fixer-attached` rows exist with `object_type = 'run' AND object_id = candidate.RunID AND seq > afterGateSeq`, call `appendEffect(tx, FixerAttachedKind, candidate.RunID, payload, l.cfg.Now().UTC())` (`ObjectType = "run"`, `ObjectID = &candidate.RunID`) with JSON payload:

```json
{
  "item_id": "<candidate.ItemID>",
  "run_id": "<candidate.RunID>",
  "lane_id": "<candidate.LaneID>",
  "fixer_item": "<hold.fixerID>",
  "red_sha": "<hold.redSHA>",
  "failing_checks": ["<check-id>"],
  "kept_candidate": true
}
```

3. **Advance `gateJudgmentEpoch` across `trunk-already-red` holds ([`tools/tillr-drain/drain/events.go`](../../../tools/tillr-drain/drain/events.go)):**
   - Keep top-level `"item_id": judgment.Run.ItemID` populated on `gateJudgmentPayload` (`gate.verdict`), matching `gateStartedPayload.ItemID` on `gate.started`.
   - Extend `gateJudgmentEpoch(r, runID)` so its `COALESCE(MAX(seq), 0)` query over `events` also includes `(kind = ? AND object_id = ? AND json_extract(payload, '$.kept_candidate') = 1)` (`FixerAttachedKind`, `runID`) and `(kind = ? AND object_id = ? AND json_extract(payload, '$.step') = ?)` (`RefusalKind`, `runID`, `RefusalTrunkAlreadyRed`). When the kept candidate re-enters `Gate` under the same `run_id`, `judgmentSeen` sees the advanced epoch and records the post-hold `gate.plan` and `gate.verdict` alongside `recordGateStarted`'s `gate.started`.

### 2.2 Ordered `predicate_match` (`after` and `reset`) in `tools/tillr/watch/`

Extending [`tools/tillr/watch/rules.go`](../../../tools/tillr/watch/rules.go), [`tools/tillr/watch/lint_rules.go`](../../../tools/tillr/watch/lint_rules.go), and [`tools/tillr/watch/pattern.go`](../../../tools/tillr/watch/pattern.go) (amending [`D-TW13`](../tillr-watch.md#D-TW13) and [`D-TW16`](../tillr-watch.md#D-TW16) while keeping `tools/tillr/watch` free of hardcoded event or item names):

1. **Schema and lint (`rules.go`, `lint_rules.go`):**
   - On `pattern == "predicate_match"`, allow optional `"after"` and `"reset"` in `[rule.when]` alongside `"group_by"`, `"pattern"`, `"match"`, `"clear_when"`, `"window"`, and `"fingerprint"` (`"after"` remains refused on `count_in_window`, `consecutive_count`, `alternating_pair`, and `lag_behind`).
   - When `after` is omitted (`w.After == nil`), `match` remains optional and `reset` is refused (`"when.reset: requires after on predicate_match"` in `LoadRules` and `CodeWatchLintRuleSchema` in `tillr watch lint`), preserving all existing `predicate_match` rules and refusal tests.
   - When `after` is present (`w.After != nil`), `match` is required (`"when.match: required when after is set on predicate_match"` in `LoadRules` and `CodeWatchLintRuleSchema` in `tillr watch lint`) and `reset` is optional. `Rule.expressions()`, `ruleEqual`, `ruleDefinitionDigestInput` (`definitionHash` under [`D-TW66`](../tillr-watch.md#D-TW66)), and `lintRuleExpr` (`event.kind` checks against `reads.events`) already include `When.After` and `When.Reset`.
2. **Per-subject evaluation (`pattern.go`):**
   - Add `armed bool` to `condition`, clearing `c.armed = false` whenever `clear_when` matches in `(*condition).observe` or a composite prefix `clear_when` matches in `observeCompositeRecord`.
   - In `(*condition).observe`, after applying `clear_when`, when `r.Pattern == PredicateMatch && r.After != nil` over post-baseline (`seq > first_loaded_seq`), in-window (`ts >= cutoff && ts <= eval_now`, or `ts <= eval_now` when `window` is omitted with `clear_when`) events in ascending `seq` order:
     1. If `r.Reset != nil && r.Reset.eval(values)`: set `c.armed = false` and return (disarming any pending `after` hold without clearing already-counted `c.events` or appending to `c.clears`).
     2. Else if `r.After.eval(values)`: set `c.armed = true` and return (arming the subject without counting the `after` event itself, so `seq_after < seq_match` strictly).
     3. Else if `c.armed && r.Match.eval(values)`: append `e` to `c.events`, set `c.armed = false` (recording one violation whose event fields populate `{{event.<name>}}` while allowing a later `after` event to re-arm), and return.
   - Because in-window post-baseline events stream in monotonic `seq` order with no wall-clock timer, `ruleCacheable` stays `true` ([`D-TW49`](../tillr-watch.md#D-TW49)). This `predicate_match` `after` / `reset` mechanism is identical to the ordered `predicate_match` contract shared with [`D-TW79`](../tillr-watch.md#D-TW79).

### 2.3 Shipped rule: `config/tillr/watch/drain-fixer-held-new-attempt.toml`

```toml
# 2026-11-23 incident: item-request-b70a54f954 was held behind a trunk fixer at
# 04:55 ET with a finished candidate, and at 08:53 ET after the fixer closed the
# drain started run-item-request-b70a54f954-a2-n1 instead of gating the kept
# candidate.
# Registered event kinds read: drain.fixer-attached, gate.started, gate.verdict, run.allocate.
[[rule]]
id = "drain-fixer-held-new-attempt"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.fixer-attached", "gate.started", "gate.verdict", "run.allocate"]

[rule.when]
group_by = "item_id"
pattern = "predicate_match"
after = 'event.kind == "drain.fixer-attached" and not (event.kept_candidate == false)'
reset = 'event.kind in ["gate.started", "gate.verdict"]'
match = 'event.kind == "run.allocate" and not (event.seat in ["design", "review", "verify"]) and not (event.run_id matches "-(judge|cfix|fresh|restart|interrupted|failover)$")'
window = "24h"

[rule.guidance]
summary = "Item {{subject}} started worker run {{event.run_id}} on {{event.lane_id}} after a trunk-fixer hold without gating its kept candidate again."
check = [
  "Inspect the event timeline for {{subject}} (drain.candidate, drain.fixer-attached, drain.examination-done, gate.started, gate.verdict, and run.allocate for {{event.run_id}}) and run tillr drain why {{subject}} to see why the kept candidate was retired or bypassed instead of gated.",
  "Check whether the kept candidate commit is still reachable in git and whether candidate retirement or release logic dropped it before examination."
]
do = [
  "Recover the kept candidate commit if still reachable, diagnose the drain transition that started {{event.run_id}} instead of gating the kept candidate, and file or track the fix."
]

[rule.delivery]
receiver = "foreground"
```

Why each clause in `[rule.when]` has this exact form:

- **`after = 'event.kind == "drain.fixer-attached" and not (event.kept_candidate == false)'`:** In `compare` ([`tools/tillr/watch/expression.go`](../../../tools/tillr/watch/expression.go)), `nil != false` evaluates to `false`. Writing `not (event.kept_candidate == false)` arms on production kept-candidate holds (`kept_candidate: true`) and on minimal synthetic test events omitting `kept_candidate`, while ignoring fixer-own and defect-own attachments (`kept_candidate: false`).
- **`reset = 'event.kind in ["gate.started", "gate.verdict"]'` (and not `clear_when`):** Both `gate.started` (`gateStartedPayload`) and `gate.verdict` (`gateJudgmentPayload`) carry top-level `item_id` (`Skipped == 0`). When the kept candidate *is* gated again after the hold, `gate.started` (in `ProcessSteps`) and `gate.verdict` (across both `ProcessSteps` and direct judgment tests) disarm `c.armed`, so even if that re-gate fails on the candidate's own defect and a legitimate next attempt (`run.allocate`) starts afterward, `0` alerts open. Conversely, if a new attempt starts *before* any post-hold gate (`drain.fixer-attached -> run.allocate`), `reset` does not clear `c.events`, so the opened alert **remains open** when that new attempt later reaches its own `gate.started` and `gate.verdict` (until resolved, item completion under [`D-TW63`](../tillr-watch.md#D-TW63), or `window = "24h"` expiry).
- **`match` on `run.allocate` excluding `["design", "review", "verify"]` seats and `-(judge|cfix|fresh|restart|interrupted|failover)$` run IDs:** `attemptRunID` (`run-<slug>-a<N>`) and workflow stage dispatches (`run-<slug>-a<N>-n<K>`) in [`tools/tillr-drain/drain/plan.go`](../../../tools/tillr-drain/drain/plan.go) and [`tools/tillr-drain/drain/run_id.go`](../../../tools/tillr-drain/drain/run_id.go) bound `<slug>` to `<= 56` characters (`<= 72` total, below `maxRunIDBaseLen = 88`), so every run starting an item's next attempt ends in `-a<N>` or `-a<N>-n<K>` on a non-judge/non-design seat (and `not (event.seat in [...])` stays `true` even when `seat` is omitted in a minimal test event). Meanwhile, pre-gate `preRebaseRepairedTrunk` conflict repairs (`conflictFixRunID`: `-cfix`), pre-gate re-judging of a rebased kept candidate (`judgeRunID` / `markRunID`: `-judge`, `-fresh`, `-restart`, `-interrupted`, `-failover` on `review` or `verify`), and `design` conflict runs are all excluded with zero false positives.

---

## 3. How `"Change nothing else"` Governs Build Scope and Verification ([`D-TW-FHA1`](#D-TW-FHA1))

Following [`D-TW42`](../tillr-watch.md#D-TW42) (as amended for rules requiring minimal producer attribution and watch-engine support) and [`landing-principles.md` §15](../landing-principles.md#15-land-the-smallest-functional-change), `"Change nothing else"` on `item-request-8fdabc44ba` means:

- **Untouched (out of scope):** Kept-candidate re-gating (`c7361cae`, `item-request-310fbd6b5d`), `tools/tillr-view/heavy_misses.go`, `tools/tillr-drain/fixer/fixer_resolve.go`, `tools/tillr-land`, every existing `.toml` rule file, `.tillr/state.db` schema (`0` DDL changes), and the unbuilt shipped-rule migration of [`../tillr-watch.md` §10](../tillr-watch.md#10-build-slices-for-shipped-and-repository-watch-rules) ([`D-TW83`](../tillr-watch.md#D-TW83)–[`D-TW88`](../tillr-watch.md#D-TW88); while `config/tillr/watch/` remains the active rules directory in the tree, the new file lives at `config/tillr/watch/drain-fixer-held-new-attempt.toml`, and Slice 2/3 of `D-TW88` will move shipped rules together when built).
- **Authorized single-build scope for `item-request-8fdabc44ba`:**
  1. `tools/tillr-drain/drain/` (`trunk.go`, `loop.go`, `defect.go`, `events.go`, plus unit tests in `tools/tillr-drain/drain/`) and `just pack-sync` mirror under `yolo-packs/tillr/`: verifying kept-candidate `drain.fixer-attached` emission after `gate.finishJudgment()` across new, reused, and check-attached `fixer-main-*` holds, absence on worker `OBSTACLE: trunk` and `holdForOpenTestFix`, `item_id` + `kept_candidate: false` on fixer/defect attachments without altering `ObjectID`, and post-hold `gate.verdict` emission via `gateJudgmentEpoch`.
  2. `tools/tillr/watch/` (`rules.go`, `lint_rules.go`, `pattern.go`, plus unit tests in `tools/tillr/watch/`) and `just pack-sync` mirror under `yolo-packs/tillr/`: verifying `predicate_match` `after` and `reset` parsing, lint refusals (`reset` without `after`; missing `match` with `after`), `evalCache` / `ObservationSource` parity, and `definitionHash`.
  3. `config/tillr/watch/drain-fixer-held-new-attempt.toml` and scratch-store test `tools/tillr-watch/fixer_held_new_attempt_rule_test.go` (`t.TempDir()` only, never writing `.tillr/state.db`; [`D-TW41`](../tillr-watch.md#D-TW41)): verifying `tillr watch lint` and `LoadRules` (`Diagnostic() == ""`, `Skipped == 0`), `1` open alert on `drain.fixer-attached -> run.allocate` (`run-...-a2` or `run-...-a2-n1`) that stays open across subsequent `gate.started` / `gate.verdict` of the new attempt, and `0` alerts on `drain.fixer-attached -> gate.started -> run.allocate`, `drain.fixer-attached -> gate.verdict -> run.allocate`, pre-gate rebase `-cfix` and `review`/`verify` `-fresh` runs, `design` runs, fixer/defect `kept_candidate: false` attachments, and ordinary `run.allocate` without a prior `drain.fixer-attached`.

---

## 4. Rejected Alternatives

Each alternative below was set aside by the designer and the operator request (the owner may overturn any of these dispositions except [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), and [`D-TW73`](../tillr-watch.md#D-TW73)):

| Alternative | Why it fails | Verdict |
| :--- | :--- | :--- |
| **Change `ObjectID` on `routeTrunkFixer`'s existing `drain.fixer-attached` to the held item** | Breaks `heavyMissSubject` / `heavyMissCreationLog` in [`tools/tillr-view/heavy_misses.go`](../../../tools/tillr-view/heavy_misses.go) and `FixerSeen` in [`tools/tillr-drain/fixer/fixer_resolve.go`](../../../tools/tillr-drain/fixer/fixer_resolve.go), and still misses newly created or reused `fixer-main-<sha>` holds while false-arming on `OBSTACLE: trunk`. | Rejected. |
| **Emit kept-candidate `drain.fixer-attached` inside `acquitRedWithPaths` before `gate.finishJudgment()`** | Places deferred `gate.verdict` from the *pre-hold* gate at a higher `events.seq` than `drain.fixer-attached`, immediately disarming `reset` before the hold even begins. | Rejected. |
| **Match every non-`design` `run.allocate` without excluding `review`/`verify` and `-(judge\|cfix\|fresh\|restart\|interrupted\|failover)$`** | False-alerts under [`D-TW73`](../tillr-watch.md#D-TW73) whenever `preRebaseRepairedTrunk` rebases a kept candidate onto a repaired trunk before `gate.started` and dispatches `-cfix` or re-judges via `-fresh`. | Rejected. |
| **Put `gate.started` / `gate.verdict` in `clear_when` instead of `reset`** | Auto-resolves the alert as soon as the wasted new attempt reaches its own gate, hiding the bypassed kept candidate before anyone inspects it. | Rejected. |
| **Use `window = "1h"` instead of `"24h"`** | Misses multi-hour trunk-fixer holds such as the `3h 58m` 2026-11-23 incident on `item-request-b70a54f954`. | Rejected. |

---

## 5. Open Questions

None.

---

## 6. Decision Ledger

[`D-TW-FHA1`](#D-TW-FHA1) records an operator-request (`item-request-8fdabc44ba`) and designer decision answering `conflict-run-item-request-8fdabc44ba-a1-n1`; it is **not** an owner ruling, and the owner may overturn any part of it. `Built` is checked against `95d7043eb6`.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-TW-FHA1"></a>**[D-TW-FHA1](#D-TW-FHA1)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** expand [`D-TW77`](../tillr-watch.md#D-TW77) and amend [`D-TW13`](../tillr-watch.md#D-TW13), [`D-TW16`](../tillr-watch.md#D-TW16), and [`D-TW42`](../tillr-watch.md#D-TW42) backward-only to answer `conflict-run-item-request-8fdabc44ba-a1-n1` in one build: (1) preserve `ObjectID` on fixer/defect `drain.fixer-attached` while adding `item_id` and `kept_candidate: false`, emit run-scoped kept-candidate `drain.fixer-attached` (`kept_candidate: true`, `item_id`) in `Loop.verify` after `gate.finishJudgment()`, and include kept-candidate `drain.fixer-attached` and `trunk-already-red` `drain.refusal` in `gateJudgmentEpoch`; (2) support optional `after` (requiring `match`) and `reset` (requiring `after`) on `predicate_match` in `tools/tillr/watch/`; and (3) ship `config/tillr/watch/drain-fixer-held-new-attempt.toml` arming on kept-candidate `drain.fixer-attached`, disarming on `gate.started` or `gate.verdict`, and firing on `run.allocate` starting a new attempt's worker run (excluding `design`/`review`/`verify` seats and `-(judge\|cfix\|fresh\|restart\|interrupted\|failover)$` continuations/repairs) over a `24h` window, with `just pack-sync` mirrors and scratch-store tests and no changes to `item-request-310fbd6b5d` (`c7361cae`), other rules, `tillr-view`, or `fixer_resolve.go`. | 2026-11-25 | [§1](#1-problem-and-provenance), [§2](#2-producer-watch-engine-and-rule-contract-d-tw-fha1), [§3](#3-how-change-nothing-else-governs-build-scope-and-verification-d-tw-fha1), [§4](#4-rejected-alternatives) | no |
