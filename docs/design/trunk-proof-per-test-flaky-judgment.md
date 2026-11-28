---
title: "The trunk proof judges flaky tests one test at a time and runs only the failing tests again"
status: in-review
stage: DESIGN
updated: 2026-11-28
tags:
  - tillr
  - gate
  - drain
  - proof
  - flake
  - testing
  - design
summary: "Moves pass/fail contradiction disabling from whole Go suite declarations (go-test-tillr-*) to individual Go test selectors (go-test:<module>:<pkg>:<TestName>), defines a single focused rerun of failed tests on the same tip where a pass means disabled + filed repair (never pass), specifies the targeted pre-merge gate and binomial/race/contention math for flaky-* repair items, defines what carries over across a moving tip alongside gate-test-caching.md and trunk-repair-on-moving-trunk.md, checks all seventeen landing principles, lists build slices, and puts OQ-TPF1 through OQ-TPF4 to the owner."
next: "The owner rules on OQ-TPF1, OQ-TPF2, OQ-TPF3, and OQ-TPF4; independent build slices that do not depend on an unanswered question may be filed and built under OQ-PR8."
depends-on:
  - ../reference/gate.md
  - ../reference/gate.md#D-GR2
  - ../reference/gate.md#D-GR6
  - ../reference/gate.md#D-GR8
  - ../reference/gate.md#D-GR15
  - ../reference/gate.md#D-GR17
  - ../reference/gate.md#D-GR21
  - ../reference/gate.md#D-GR22
  - ../reference/gate.md#D-GR23
  - ../reference/gate.md#D-GR31
  - ../reference/gate.md#D-GR32
  - ../reference/gate.md#D-GR49
  - ../reference/gate.md#D-GR53
  - ../reference/gate.md#D-GR54
  - ../reference/gate.md#D-GR56
  - ../reference/gate.md#D-FG1
  - ../reference/gate.md#D-FG2
  - ../reference/gate-check-rules.md
  - landing-principles.md
  - gate-test-caching.md
  - trunk-repair-on-moving-trunk.md
  - tillr-judge-and-lifecycle.md
  - tillr-item-split.md
  - ../tillr/decision-provenance-review.md
  - ../reference/authority-not-approval.md
vantage:
  status-chip: true
---

# The trunk proof judges flaky tests one test at a time and runs only the failing tests again

**Status:** Open for the owner's decisions in [`OQ-TPF1`](#OQ-TPF1), [`OQ-TPF2`](#OQ-TPF2), [`OQ-TPF3`](#OQ-TPF3), and [`OQ-TPF4`](#OQ-TPF4); **nothing in this proposal is built yet** (`rg -n 'prove-disabled' tools/tillr-drain` returns nothing, and `disabledPath` in [`tools/tillr-gate/enable.go`](../../tools/tillr-gate/enable.go) keys markers only on whole declaration IDs). Every proposed mechanism, option, slice breakdown, and leaning below is recorded from an operator request as **the designer's analysis and decision, not an owner ruling** ([decision provenance review](../tillr/decision-provenance-review.md#6-how-this-stops-happening), [`OQ-PR21` and `OQ-PR22`](../tillr/decision-provenance-review.md#decision-ledger)); the owner may overturn any part of it. No Go code or configuration is changed in this commit.

> **In short.** On 2026-11-27, `go-test-tillr-drain` ran twice on commit `50e065bc`: run one failed only `TestWhyAfterRestartOverLiveEvaluator/resumed_segment`, and run two failed only `TestAnOperatorStopInterruptsTheBackoff` and `TestIdleLoopRunDoesNotReplanUntilChange` (`16 / 249` = `6.43%` failure rate across `.rolling/logs`, even after an earlier fix in `929285ab` landed). Each of the three tests passed once and failed once on the same tree, yet whole-check contradiction disabling ([Landing Principle §17](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking), [`D-GR21`](../reference/gate.md#D-GR21)) saw only `(fail, fail)` on `go-test-tillr-drain`, disabled nothing, held `refs/tillr/green` back all day, and left an operator filing per-test items by hand. Recording pass/fail observations and disabled markers per Go test selector (`go-test:<module>:<pkg>:<TestName>`), running **only the failed test selectors** once on the same tip to separate deterministic regressions (`fail` + `fail`) from contradictions (`fail` + `pass` -> `disabled` + automatic `flaky-*` repair item, never `pass`), and requiring `flaky-*` repair gates to prove the named test alone removes the operator from the loop while letting the rest of the suite prove the trunk green.

**Why it matters.** A 2,144-test suite with three `2%–6%` load-sensitive flakes fails as a whole on most runs even when zero trunk commits broke anything, starving `refs/tillr/green` (`D-GR5`) and forcing manual triage.

**The shape.** [Section 1](#1-owner-direction-and-the-2026-11-27-incident) states the 2026-11-27 failure and the three code gaps behind it; [Section 2](#2-the-unit-of-contradiction-exclusion-and-automatic-repair-filing) defines per-test contradiction, exclusion in `tillr-gate shards`, and `flaky-*` filing; [Section 3](#3-running-only-what-failed-and-what-the-second-result-means) defines the targeted rerun and checks it against [Landing Principle §11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out), [Landing Principle §17](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking), and the no-retry rules; [Section 4](#4-the-repair-item-and-what-its-verify-step-runs) specifies `flaky-*` verification with measured probabilities and durations; [Section 5](#5-what-carries-over-when-the-trunk-moves) defines moving-tip carryover alongside [`gate-test-caching.md`](./gate-test-caching.md) and [`trunk-repair-on-moving-trunk.md`](./trunk-repair-on-moving-trunk.md); [Section 6](#6-check-against-landing-principles) audits all seventeen landing principles; [Section 7](#7-what-is-deliberately-not-done) lists non-goals; [Section 8](#8-build-slices) decomposes the build into five slices; and [Section 9](#9-open-questions) puts the owner's choices forward.

**Cost.** When a heavy Go suite is green, runtime cost is zero extra subprocesses (`go test -json` is already parsed in [`tools/tillr-gate/shards`](../../tools/tillr-gate/shards)). When `1–3` tests fail, a focused rerun of those tests alone costs `~1–40 s` instead of `6.6–38.9 min` for a second 6-shard suite run.

**Start at:** [Section 2](#2-the-unit-of-contradiction-exclusion-and-automatic-repair-filing), then jump to [Section 9](#9-open-questions) for the four questions.

**Needs your ruling:** [`OQ-TPF1`](#OQ-TPF1), [`OQ-TPF2`](#OQ-TPF2), [`OQ-TPF3`](#OQ-TPF3), and [`OQ-TPF4`](#OQ-TPF4) (plus five designer's decisions `D-TPF1` through `D-TPF5` in [the Decision Ledger](#decision-ledger), which the owner may overturn).

**Reads with:** [landing principles](./landing-principles.md), [the gate reference](../reference/gate.md), [gate check rules](../reference/gate-check-rules.md), [skip test work whose result is proved unchanged](./gate-test-caching.md), and [repairing a red trunk proof on a moving trunk](./trunk-repair-on-moving-trunk.md).

---

## 1. Owner direction and the 2026-11-27 incident

### 1.1 Owner direction and provenance

The 2026-11-27 filed request records the owner's prompt and direction (quoted verbatim from the operator-filed request text; because the request paraphrases the owner's spoken words rather than enclosing a direct transcript in quotation marks, it is recorded under `D-PR1` in [`docs/tillr/decision-provenance-review.md`](../tillr/decision-provenance-review.md#6-how-this-stops-happening) as an operator-recorded statement of the owner's direction):

> "The owner asked on 2026-11-27 why the trunk has stayed unproved all day, and ruled that this should be made to work: the proof should be able to run just the failing tests instead of the whole suite, and fixes should be targeted."

Three verbatim dated owner rulings in [`docs/reference/gate.md`](../reference/gate.md#settled-by-the-owner) and [`docs/design/gate-test-caching.md`](./gate-test-caching.md#1-owner-direction-and-framing) already establish the surrounding invariants:

- **[`D-GR21`](../reference/gate.md#D-GR21) (2026-11-21):** `"The correct move for a flaky test is disable it, file an item to fix it, and don't let it block anything. It is not considered green, but it also doesn't let you block other things."` and `"And the principle here is you make the trunk green by disabling the flaky test. The test is not green because it's flaky, but the trunk is green because the test is no longer included.And yes, the item that is filed should be to re-enable it correctly, the test that was flaky."`
- **[`D-GR17`](../reference/gate.md#D-GR17) (2026-11-19):** `"Rule of this repository: a test that fails or hangs under load is a broken test; do not add a timeout, a retry or a longer wait as the fix"`.
- **Test-skipping soundness bar (2026-11-25, in [`gate-test-caching.md`](./gate-test-caching.md#1-owner-direction-and-framing)):** `"if we know it's correct, we can be just as aggressive as possible."`

### 1.2 What happened on commit `50e065bc` (2026-11-27)

On commit `50e065bc` (`"fix(drain): query idle lane runs per lane to avoid SQLite OpenEphemeral"`), the trunk proof's `go-test-tillr-drain` check ([`tools/gate/checks/go-test-tillr-drain.json`](../../tools/gate/checks/go-test-tillr-drain.json)) ran twice on the identical tree:

| Test selector in `github.com/mschulkind-oss/tillr-drain/drain` | Proof run 1 on `50e065bc` | Proof run 2 on `50e065bc` | Same-commit verdict per test | Historical frequency / prior fix |
| :--- | :--- | :--- | :--- | :--- |
| `TestWhyAfterRestartOverLiveEvaluator/resumed_segment` ([`restart_evaluator_report_test.go`](../../tools/tillr-drain/drain/restart_evaluator_report_test.go)) | **FAIL** | **PASS** | Contradicted (`fail`, `pass`) | Recorded top-level duration `29.33 s` in [`tools/go-test-durations.json`](../../tools/go-test-durations.json) |
| `TestAnOperatorStopInterruptsTheBackoff` ([`loop_test.go`](../../tools/tillr-drain/drain/loop_test.go)) | **PASS** | **FAIL** | Contradicted (`pass`, `fail`) | Recorded duration `0.79 s` (`18.92 s` measured at host load `179.38` on 2026-11-28) |
| `TestIdleLoopRunDoesNotReplanUntilChange` ([`idle_test.go`](../../tools/tillr-drain/drain/idle_test.go)) | **PASS** | **FAIL** | Contradicted (`pass`, `fail`) | Failed in **`16` of `249`** (`6.43%`) drain-suite logs under `.tillr/lanes/.rolling/logs` (`rg -l` on 2026-11-27), despite an earlier fix landing in `929285ab` (2026-11-26) |
| **Whole check `go-test-tillr-drain` (`2,144` top-level tests)** | **FAIL** | **FAIL** | `(fail, fail)` — **no contradiction seen** | Trunk stayed unproved; operator had to file one item per test by hand |

### 1.3 Three code gaps that produced the stall and the escaped fix

1. **Contradiction memory is keyed on whole check declarations, not test selectors.** `treeFingerprint` in [`tools/tillr-gate/memory.go`](../../tools/tillr-gate/memory.go) and `disabledPath(checkID)` in [`tools/tillr-gate/enable.go`](../../tools/tillr-gate/enable.go) record `pass` and `fail` only for `"go-test-tillr-drain"` as a single unit. Whenever two or more flaky tests alternate across runs of a 2,144-test suite, the declaration records `(fail, fail)` and never triggers rule 3 of `Classify` in [`tools/tillr/checkresult/classify.go`](../../tools/tillr/checkresult/classify.go).
2. **Live check execution does not yet exclude disabled units or pass `--prove-disabled`.** As noted under *Not enforced* in [Landing Principle §17](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking), live `tillr-gate run` still annotates `flaky` in `annotateMemory` ([`tools/tillr-gate/memory.go`](../../tools/tillr-gate/memory.go)), `tillr-gate shards` has no flag or store lookup to skip disabled tests, and `tillr-drain` never passes `--prove-disabled` (`rg -n 'prove-disabled' tools/tillr-drain` returns nothing).
3. **`flaky-*` repair items bypass named-check verification before merge.** In [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) and [`tools/tillr-drain/drain/fixer_gate.go`](../../tools/tillr-drain/drain/fixer_gate.go), `settleFixerGate` runs only for `IsTrunkFixer` (`fixer-main-<sha>`), not for `IsFlakyRepairItem` (`flaky-<slug>`). Because [`D-GR53`](../reference/gate.md#D-GR53) and [`D-GR56`](../reference/gate.md#D-GR56) deferred `github.com/mschulkind-oss/tillr-drain/drain` out of pre-merge `go-affected` (`5e728e5a`), a candidate touching `tools/tillr-drain/drain/idle.go` and `idle_test.go` (such as `929285ab`) compiles `drain` in `go-build` (`go test -exec true ./...`), merges without running `TestIdleLoopRunDoesNotReplanUntilChange` even once, and immediately triggers `sweepDisabledMarkers` -> `tillr-gate enable` on land. Meanwhile, `TestIdleLoopRunDoesNotReplanUntilChange` in [`tools/tillr-drain/drain/idle_test.go`](../../tools/tillr-drain/drain/idle_test.go) still asserts no replan across a bare `time.Sleep(150 * time.Millisecond)` after `"waiting for a trigger"` is written to `l.cfg.Report`, which passes in quiet runs and trips `6.43%` of the time under heavy proof contention.

---

## 2. The unit of contradiction, exclusion, and automatic repair filing

### 2.1 The per-test check selector (`D-TPF1`, subject to [`OQ-TPF2`](#OQ-TPF2))

`tillr-drain` already defines a self-contained Go test selector grammar in `GoSuiteFailures` and `JudgeHeavyGo` ([`tools/tillr-drain/internal/gatespans/heavy_go.go`](../../tools/tillr-drain/internal/gatespans/heavy_go.go)):

```text
go-test:<module>:<package-import-path>:<TestName>
```

Examples from commit `50e065bc`:

```text
go-test:tools/tillr-drain:github.com/mschulkind-oss/tillr-drain/drain:TestIdleLoopRunDoesNotReplanUntilChange
go-test:tools/tillr-drain:github.com/mschulkind-oss/tillr-drain/drain:TestAnOperatorStopInterruptsTheBackoff
go-test:tools/tillr-drain:github.com/mschulkind-oss/tillr-drain/drain:TestWhyAfterRestartOverLiveEvaluator/resumed_segment
```

`tillr-gate shards` (`RunCachedGroup` in [`tools/tillr-gate/shards/cached.go`](../../tools/tillr-gate/shards/cached.go) and uncached shard execution in [`tools/tillr-gate/shards/runner.go`](../../tools/tillr-gate/shards/runner.go)) already executes `go test -json` and parses every `TestEvent` (`Package`, `Test`, `Action`, `Output`). Turning individual Go tests into first-class units of [`D-GR21`](../reference/gate.md#D-GR21) requires one attribution rule for subtests:

- **Leaf-failure attribution when subtests exist (`D-TPF1`, designer's leaning in [`OQ-TPF2`](#OQ-TPF2)):** In `go test -json`, when `TestWhyAfterRestartOverLiveEvaluator/resumed_segment` fails while sibling `TestWhyAfterRestartOverLiveEvaluator/first_segment` passes, Go emits `"Action": "fail"` for **both** `TestWhyAfterRestartOverLiveEvaluator/resumed_segment` and the enclosing `TestWhyAfterRestartOverLiveEvaluator`. If the runner recorded a failure on the parent whenever a child failed, disabling `resumed_segment` would leave a false failure or contradiction on the parent. Therefore:
  1. Every test or subtest that emits `"Action": "pass"` records a `pass` observation for its own selector.
  2. A test selector `T` that emits `"Action": "fail"` records a `fail` observation **if and only if no descendant subtest `T/<child>` also emitted `"Action": "fail"` in that run**.
  3. Consequently, a failure inside `t.Run("resumed segment", ...)` is recorded solely against `.../TestWhyAfterRestartOverLiveEvaluator/resumed_segment` (leaving `first_segment` active), whereas a `t.Fatal` in `TestWhyAfterRestartOverLiveEvaluator` outside `t.Run` (or after all subtests pass) has zero failing descendants and is recorded against `.../TestWhyAfterRestartOverLiveEvaluator`.
- **Top-level alternative ([`OQ-TPF2`](#OQ-TPF2) Option B):** Collapse all subtest events onto their top-level `Test*` parent selector (`.../TestWhyAfterRestartOverLiveEvaluator`). That avoids Go `-skip` subtest matching altogether, at the cost of temporarily disabling healthy sibling subtests (`first_segment`) until the repair item lands.

### 2.2 Per-test contradiction markers and per-test log slicing

When `tillr-gate` evaluates a Go suite (`tillr-gate shards` invoked by `go-test-*`, or `tillr-gate go-affected`) with `--store <results-dir>`:

1. **Per-test output slicing:** From each shard's `TestEvent` stream, the runner buffers lines by `ev.Test` (preserving parent preamble lines emitted before `t.Run` when slicing a subtest). When a test fails or contradicts a stored record, `copyDisabledLog` ([`tools/tillr-gate/enable.go`](../../tools/tillr-gate/enable.go)) writes **only that test's output slice** (`<episodeKey>.fail.log` and `<episodeKey>.pass.log`, typically `20–200` lines) rather than copying a multi-megabyte 6-shard suite log.
2. **Classification through the single classifier ([`D-GR23`](../reference/gate.md#D-GR23)):** Each observed test selector is classified via `checkresult.Classify` ([`tools/tillr/checkresult/classify.go`](../../tools/tillr/checkresult/classify.go)) against `<store>` using its `CheckID = "go-test:<module>:<pkg>:<TestName>"` and its test input fingerprint ([Section 5.2](#52-three-state-classes-across-a-moving-tip-t1---t2)).
3. **Contradiction -> atomic `DisabledMarker`:** When the store holds both a `pass` and a `fail` for the same `(CheckID, Fingerprint)` (and that episode is not in `resolved_episodes`), `writeRule3Marker` atomically publishes `<store>/disabled/<sha256(CheckID)>.json`. Because `disabledPath` hashes `CheckID` rather than `Fingerprint` ([`D-GR22`](../reference/gate.md#D-GR22)), later commits that edit `tools/tillr-drain/` **never** silently re-arm an unrepaired flaky test.

### 2.3 Excluding disabled tests in `tillr-gate shards` so the rest of the suite proves the trunk green

Before `tillr-gate shards` partitions a package's discovered `Test*` functions across shards (`Assign` in [`tools/tillr-gate/shards/runner.go`](../../tools/tillr-gate/shards/runner.go)):

1. **Read active disabled selectors for the package:** Scan `<store>/disabled/*.json` for markers whose `CheckID` has prefix `go-test:<module>:<pkg>:` (excluding any selector passed via `--prove-disabled`).
2. **Exclude top-level disabled tests before `Assign`:** Any disabled top-level `TestName` (no `/`) is removed from `names` before `Assign` builds shard `-run` patterns. The test never starts, consumes zero CPU, and emits one synthetic `disabled` check classification carrying the marker's `Episode` and `RepairItem`.
3. **Exclude disabled subtests via verified Go `-skip` patterns (when [`OQ-TPF2`](#OQ-TPF2) selects Option A):** When `TestParent/sub1` is disabled and `TestParent` is assigned to a shard so `TestParent/sub2` still runs, `shards` passes `-skip <pattern>` to `go test`. Source inspection and execution against Go 1.27.3's `$GOROOT/src/testing/match.go` (`splitRegexp`, `alternationMatch.matches`, and `fullName`) establish two exact construction rules that `shards` must enforce:
   - **Never wrap slash-separated subtest patterns in parentheses:** In `splitRegexp`, `/` splits subtest levels only when parenthesis depth `cp == 0` (`(^TestA$/^sub1$)|(^TestB$)` fails to split on `/` and skips `TestA` as a whole or misses `sub1`). Multiple skip selectors in one shard are joined by **unparenthesized** top-level `|`, with each `/` segment anchored (`^TestA$/^sub1$|^TestB$`).
   - **Prune child subtest patterns under already-disabled parents (or sort shorter segment lists first):** In `alternationMatch.matches`, Go returns the first matching alternation branch even if `partial == true`, and `fullName` skips only when `skip && !partialSkip`. Ordering `^TestA$` ahead of (or dropping) `^TestA$/^sub1$` guarantees `TestA` is skipped completely whenever the parent itself is disabled.
   - Any `TestEvent` with `"Action": "skip"` matching an active disabled subtest selector is classified as `Outcome: Disabled` (never `Pass` and never `Skipped`).
4. **Suite verdict aggregation and whole-check `memory.go` safety:**
   - If a `go-test-*` run finishes with `N_pass >= 0` passed tests, `N_disabled >= 1` disabled tests, and **zero** non-disabled failed tests, build errors, crashes, or stalls, `Aggregate` folds the suite to `pass` while recording `disabled` tests apart in the verdict and summary (`D-GR21`: *"the trunk is green because the test is no longer included"*). `refs/tillr/green` advances.
   - To prevent a suite-level remembered pass in [`tools/tillr-gate/memory.go`](../../tools/tillr-gate/memory.go) from outliving a `flaky-*` repair, `treeFingerprint` for `go-test-*` includes the sorted list of active disabled `go-test:<module>:...` selectors (or `tillr-gate enable` invalidates cached suite passes covering that module). Once `tillr-gate enable` clears the marker, the re-enabled test is immediately required on the next proof.

### 2.4 Automatic `flaky-*` repair filing by the loop

No new filing table or document directive is needed: `Loop.sweepDisabledMarkers` in [`tools/tillr-drain/drain/flaky_repair.go`](../../tools/tillr-drain/drain/flaky_repair.go) already scans `<store>/disabled/*.json` at pass start and after gate, replay, and heavy-proof steps, deduplicates against open `flaky-<slug>` items, files one `flaky-go-test-tools-tillr-drain-...` item on `TrunkFixerSeat` **with no document item block in any tracked Markdown file** ([`D-GR23`](../reference/gate.md#D-GR23)), stamps `marker.RepairItem`, and calls `tillr-gate enable` once that item reaches `StateLanded`. Passing `go-test:<module>:<pkg>:<TestName>` as `marker.CheckID` works with `FlakyRepairBaseID` (`flaky_repair.go`) out of the box, with one small guard (`D-TPF2`): cap slug length with a trailing 12-hex SHA-256 suffix when `len(slug) > 96` so deeply nested subtest names never exceed filesystem path limits under `<lane-root>/<item-id>/worktree`.

---

## 3. Running only what failed, and what the second result means

### 3.1 Focused contradiction probe on the same tip (`D-TPF3`, subject to [`OQ-TPF1`](#OQ-TPF1))

On commit `50e065bc`, run 1 of `go-test-tillr-drain` ran all `6` shards (`2,144` tests) and failed only `TestWhyAfterRestartOverLiveEvaluator/resumed_segment`. Re-running the entire `go-test-tillr-drain` check (`6.6 min` median at low contention, `11.4–38.9 min` under contention in [`docs/reference/gate.md`](../reference/gate.md#measured-proof-vs-gate-contention-2026-11-25)) wasted `99.95%` of its work re-testing `2,143` passing tests — and tripped two *other* flaky tests in run 2.

Under the proposed proof flow (`D-TPF3`, gated on the owner's ruling in [`OQ-TPF1`](#OQ-TPF1)):

1. **First observation (normal sharded suite run on tip `T`):** `go-test-tillr-*` runs via `tillr-gate shards --cache`. Every passing test records `pass` against its input fingerprint. Suppose `M >= 1` non-disabled tests fail: `F = {t_1, ..., t_M}`.
2. **Immediate check for an existing stored pass on the same inputs:** Before starting any second process, `checkresult.Classify` checks whether any `t_i` already has a stored `pass` for its exact current input fingerprint (for instance, from an earlier proof run on `T` or an earlier tip where `t_i`'s inputs were byte-identical). Any such `t_i` is **already contradicted** (`pass` + `fail`) and transitions straight to `disabled` + `DisabledMarker` with zero extra execution.
3. **Exclusions that are never rerun:**
   - **Timeouts and deadlocks ([`D-GR31`](../reference/gate.md#D-GR31)):** Any `t_i` whose output contains `panic: test timed out after ` or `fatal error: all goroutines are asleep - deadlock!` is marked `fail` without a second run.
   - **Compilation errors, binary crashes without named test failures, and watchdog stalls ([`D-GR16`](../reference/gate.md#D-GR16)):** Never probed as flaky tests; compilation/unnamed crashes stay `fail` at the suite level, and stalls stay `stalled` (`ExitStalled` = `6`).
4. **Single focused rerun of the remaining failed selectors on tip `T`:** For the remaining failed test selectors `F_probe` (typically `1–3` tests), the proof runs **only `F_probe`** once in `.rolling/worktree` at tip `T` with `-count=1`, `-p 1 -parallel 1`, and fresh private `TMPDIR` and `GIT_CONFIG_GLOBAL` (matching `JudgeHeavyGo` in [`tools/tillr-drain/internal/gatespans/heavy_go.go`](../../tools/tillr-drain/internal/gatespans/heavy_go.go)).

### 3.2 What the second result means for the test, the suite, and the trunk verdict

| Shard run on `T` | Focused rerun of `t_i` on `T` | Classification of `t_i` | Suite & `refs/tillr/green` outcome (if all other tests passed) | Loop action |
| :--- | :--- | :--- | :--- | :--- |
| `fail` | **`fail`** | **`fail` (deterministic red)** | Suite is **red (`fail`)**; `refs/tillr/green` does **not** advance | Enters focused culprit search (`FindFirstRed` via `JudgeHeavyGo` on `t_i`), automatic revert on `currentTip` ([`D-GR6`](../reference/gate.md#D-GR6)), or `fixer-main-<sha>` |
| `fail` | **`pass`** | **`disabled` (contradicted — NEVER `pass`)** | `t_i` is counted `disabled` (neither `pass` nor `fail`); if zero non-disabled failures remain, suite aggregates to **`pass` with `disabled` listed apart** and `refs/tillr/green` **advances to `T`** | Atomically writes `<store>/disabled/<sha256(t_i)>.json` with both per-test logs; `sweepDisabledMarkers` immediately files `flaky-<slug>` on `TrunkFixerSeat` |
| `fail` (`t_1`) and `fail` (`t_2`) | `t_1` -> **`pass`**, `t_2` -> **`fail`** | `t_1` = **`disabled`**; `t_2` = **`fail`** | Suite stays **red (`fail`)** solely because of `t_2` | `t_1` is disabled and gets `flaky-<slug_1>`; `t_2` enters culprit bisection / revert / `fixer-main-<sha>` alone |
| `fail` | **`stalled` / process `error`** | **`unknown` ("could not judge")** | Suite does **not** advance `refs/tillr/green`; `t_i` is **not** marked `disabled` | Handled as a proof check that could not judge ([Landing Principle §8](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)) |

**A pass on the second run is a contradiction, never a green.** The test itself is never credited `pass`; its failure output from the shard run is preserved on disk and handed to `flaky-<slug>`, and the test stays excluded and visibly counted under `disabled` on every report until its repair lands.

### 3.3 Check against Landing Principle 11, Landing Principle 17, and the no-retry ruling

1. **Agreement with [Landing Principle §17](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking) ([`D-GR21`](../reference/gate.md#D-GR21)):**
   [`D-GR21`](../reference/gate.md#D-GR21) states verbatim: `"The correct move for a flaky test is disable it, file an item to fix it, and don't let it block anything. It is not considered green, but it also doesn't let you block other things."` Applying `Classify` and `DisabledMarker` to `go-test:<module>:<pkg>:<TestName>` implements [`D-GR21`](../reference/gate.md#D-GR21) at the exact noun (`"a flaky test"`) the owner stated, rather than letting alternating flakes in a 2,144-test bundle mask each other as `(fail, fail)`.
2. **Agreement with [Landing Principle §11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out):**
   [Landing Principle §11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out) states: *"Determinism is established by observation: the same failure repeats under unchanged inputs, or strikes items that share nothing but the machinery. The test for any proposed failure mechanism is one question: what will be different on the next run, and how will the system know? A mechanism whose answer is 'time passed' is refused."*
   - A retry-to-green hopes time passed so a red disappears uninvestigated.
   - Here, **no outcome clears the failure or lets it go undiagnosed**: `(fail, fail)` diagnoses a deterministic trunk regression (`FindFirstRed` -> revert or `fixer-main-<sha>`), and `(fail, pass)` diagnoses non-determinism on identical inputs (disables `t_i` and files `flaky-<slug>` with both logs). What differs between the two outcomes is **which repair path owns the defect** — culprit revert / `fixer-main-<sha>` for a broken trunk commit versus `flaky-<slug>` for a non-deterministic test — never whether a defect exists.
3. **Substantive agreement vs. narrow textual amendment of [`D-GR22`](../reference/gate.md#D-GR22) and [`docs/reference/gate-check-rules.md`](../reference/gate-check-rules.md#a-red-is-read-once-never-re-run) (named for the owner in [`OQ-TPF1`](#OQ-TPF1)):**
   - **Substantive rule preserved:** [`docs/reference/gate-check-rules.md`](../reference/gate-check-rules.md#a-red-is-read-once-never-re-run) defines a retry as re-running a failed check to turn a red into a pass (*"A retry in the gate is an assertion that the first red meant nothing... A later pass never erases the red that came before it"*). Under [Section 3.2](#32-what-the-second-result-means-for-the-test-the-suite-and-the-trunk-verdict), a subsequent pass **never** erases the first red and **never** produces a `pass` verdict for that test; there is no retry budget (`N = 3` attempts to pass), and pre-merge candidate gates can remain strictly single-run.
   - **Exact written sentences that require owner amendment if live same-proof probing is enabled:**
     - Designer/operator decision [`D-GR22`](../reference/gate.md#D-GR22) (`2026-11-21`, in [The designer's decisions](../reference/gate.md#the-designers-decisions)) includes `"a live re-run"` in its rejected list: `"Rejected: a red verdict, a live re-run, disabling by fingerprint alone, or any retry, count or time window."` (Note also [`D-GR21`](../reference/gate.md#D-GR21)'s history note: `"Replaces the flaky pass of D-GR8, the live re-run in the evidence registry design, and a 2026-11-20 operator request for a red verdict"`, where the earlier evidence-registry proposal had re-run checks to classify them as a `flaky` *pass* rather than `disabled`.)
     - [`docs/reference/gate-check-rules.md`](../reference/gate-check-rules.md#a-red-is-read-once-never-re-run) (`status: accepted`, `stage: CURRENT`) states: `"A failed check costs exactly one run of that check."`
   - Because amending [`docs/reference/gate-check-rules.md`](../reference/gate-check-rules.md) and [`D-GR22`](../reference/gate.md#D-GR22) touches accepted reference documentation and how first-seen proof failures classify, this design **does not settle that amendment unilaterally**: [`OQ-TPF1`](#OQ-TPF1) asks the owner directly whether to authorize the single focused contradiction probe in the trunk proof (Option A, amending those two texts so a first-seen named Go test failure runs once more solely to distinguish `fail` from `disabled`), keep strictly passive cross-run per-test memory with zero live second run (Option B), or apply the probe in both trunk proofs and pre-merge `go-affected` (Option C).

---

## 4. The repair item and what its verify step runs

### 4.1 Why a single uncontended run lets flaky fixes escape (measured numbers)

From the 2026-11-27 measurement on `.tillr/lanes/.rolling/logs`, `TestIdleLoopRunDoesNotReplanUntilChange` failed in **`16` of `249` drain-suite runs**:

$$p_{\text{obs}} = \frac{16}{249} \approx 0.06426 \quad (6.43\%)$$

If an incomplete fix (such as `929285ab`) leaves a stationary per-run failure probability $p = 16 / 249$, the probability that $N$ independent runs **all pass by chance** (false-acceptance probability $\alpha = (1 - p)^N$) and the minimum $N = \lceil \ln(\alpha) / \ln(1 - p) \rceil$ required to bound false acceptance below $\alpha$ are:

| Repeat count $N$ (at stationary $p = 16/249 = 6.43\%$) | False-pass probability $(233/249)^N$ | Sequential wall time on `0.79 s` test (`TestAnOperatorStopInterruptsTheBackoff`) | Sequential wall time on `17.01 s` test (`TestIdleLoopRunDoesNotReplanUntilChange`, measured at load `179.38`) | Sequential wall time on `29.33 s` test (`TestWhyAfterRestartOverLiveEvaluator`) |
| :--- | :--- | :--- | :--- | :--- |
| **$N = 1$** (single run) | **`93.57%`** | `0.8 s` | `17.0 s` | `29.3 s` |
| **$N = 5$** | **`71.75%`** | `4.0 s` | `85.1 s` (`1.4 min`) | `146.7 s` (`2.4 min`) |
| **$N = 10$** | **`51.48%`** | `7.9 s` | `170.1 s` (`2.8 min`) | `293.3 s` (`4.9 min`) |
| **$N = 35$** ($\alpha \le 10\%$) | **`9.78%`** | `27.7 s` | `595.4 s` (`9.9 min`; `~1.7 min` across `6` workers) | `1,026.6 s` (`17.1 min`; `~2.9 min` across `6` workers) |
| **$N = 46$** ($\alpha \le 5\%$) | **`4.71%`** | `36.3 s` | `782.5 s` (`13.0 min`; `~2.2 min` across `6` workers) | `1,349.2 s` (`22.5 min`; `~3.8 min` across `6` workers) |
| **$N = 70$** ($\alpha \le 1\%$) | **`0.96%`** | `55.3 s` | `1,190.7 s` (`19.8 min`; `~3.3 min` across `6` workers) | `2,053.1 s` (`34.2 min`; `~5.7 min` across `6` workers) |

*How the single-test durations above were obtained:* `0.79 s` and `29.33 s` come from [`tools/go-test-durations.json`](../../tools/go-test-durations.json) on `HEAD` (`a52c6c14d27e`); `TestIdleLoopRunDoesNotReplanUntilChange` was added after the last durations snapshot and was measured on 2026-11-28 (`17.01 s`, alongside `18.92 s` for `TestAnOperatorStopInterruptsTheBackoff`) via `go test -C tools/tillr-drain -v -count=1 ./drain -run '^TestAnOperatorStopInterruptsTheBackoff$|^TestIdleLoopRunDoesNotReplanUntilChange$'` while the host load average stood at `179.38`.

**Why sequential `-count=N` alone still under-samples load-sensitive flakes ($p_{\text{solo}} \ll p_{\text{obs}}$):**
In [`tools/tillr-drain/drain/idle_test.go`](../../tools/tillr-drain/drain/idle_test.go) (`TestIdleLoopRunDoesNotReplanUntilChange`), the failure happens when goroutine scheduling delays cause an asynchronous pass notification to land inside `time.Sleep(150 * time.Millisecond)` after `"waiting for a trigger"` was logged. In `.rolling` proofs, `6` shards + concurrent gates + agent lanes contend for CPU (`load average 60–180`); in a solo sequential `go test -count=N` loop on an otherwise quiet machine, $p_{\text{solo}}$ can drop near `0%`, so even $N = 46$ quiet runs can pass without exercising the preemption window. Conversely, compiling with Go's race detector (`CGO_ENABLED=1 go test -race`, verified working on this Linux `go1.27.3` + `/bin/gcc` host in `1.06 s` because all tillr SQLite modules use pure-Go `modernc.org/sqlite`) catches unsynchronized memory accesses in `1` run, and running multiple instances of the compiled test binary concurrently with `GOMAXPROCS=1` reproduces scheduler preemption directly on that one test without touching the rest of the suite.

### 4.2 Proposed verify and pre-merge gate contract for `flaky-*` items (`D-TPF4`, repeat/load bar in [`OQ-TPF3`](#OQ-TPF3))

Every `flaky-<slug>` repair item carries the disabled selector `go-test:<module>:<pkg>:<TestName>` and both per-test logs (`pass_log_ref` and `fail_log_ref`) in its criterion ([`FlakyRepairCriterionText`](../../tools/tillr-drain/drain/flaky_repair.go)). Before a `flaky-*` candidate can merge and call `tillr-gate enable`, `Loop` runs a dedicated post-fast-gate settlement (`settleFlakyRepairGate`, extending [`tools/tillr-drain/drain/fixer_gate.go`](../../tools/tillr-drain/drain/fixer_gate.go)):

1. **Ordinary fast gate over the candidate's diff (`base..head`):** Runs `go-build`, `go-vet`, `elapsed-time` ([`tools/gate/checks/elapsed-time.json`](../../tools/gate/checks/elapsed-time.json), refusing new wall-clock timing bounds), `commit-identity`, and any non-deferred `go-affected` packages.
2. **Anti-evasion verification on the named test selector (`--prove-disabled`):**
   - Unlike `fixer-main-<sha>` (where `settleFixerGate` drops `goTestAbsent` when a feature and its test were intentionally removed), **a `flaky-*` candidate is refused if the named test reports `goTestAbsent` (`"test not present at this commit"`) or `goTestSkipped` (`"test or enclosing test skipped at this commit"`)**. Under [`D-GR21`](../reference/gate.md#D-GR21) (*"re-enable it correctly, the test that was flaky"*) and [`D-GR17`](../reference/gate.md#D-GR17) (*"do not add a timeout, a retry or a longer wait as the fix"*), deleting, renaming, or skipping the test requires an explicit `owner` obstacle, never a silent pass.
3. **Targeted execution of the named test alone — never the whole suite:**
   - Compile `./<pkg>` **once** (`go test -c -trimpath -o <scratch>/pkg.test`) rather than paying `go` link overhead on each run, and execute **only** `-test.run ^TestName$` (or `^Parent$/^Sub$`) in isolated per-run `TMPDIR` and `GIT_CONFIG_GLOBAL` directories at `repair` process priority (`nice 0` / `cpu.weight=400`, [`OQ-GR7`](../reference/gate.md#OQ-GR7)).
   - Run `1` pass under the race detector (`CGO_ENABLED=1 go test -race -count=1 -run <selector> ./<pkg>`) whenever a C compiler (`cc`/`gcc`) is present on the host.
   - Run the stress / repetition verification chosen by the owner in [`OQ-TPF3`](#OQ-TPF3). Because [`D-GR22`](../reference/gate.md#D-GR22) and [`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10) forbid inventing unmeasured arbitrary counts, **this document settles no magic repeat count $N$**: every numeric parameter ($\alpha$, $p_{\text{floor}}$, worker parallelism $W$, or choosing $N = 1$ + causal review) is left explicitly to the owner in [`OQ-TPF3`](#OQ-TPF3).

---

## 5. What carries over when the trunk moves

### 5.1 What [`gate-test-caching.md`](./gate-test-caching.md) and [`trunk-repair-on-moving-trunk.md`](./trunk-repair-on-moving-trunk.md) already settle vs. leave open

Both companion documents in `docs/design/` are currently **`status: in-review`, `stage: DESIGN`** with unanswered owner questions — neither is yet a graduated reference, so what they cite from earlier owner rulings must be separated cleanly from what their `OQ-*` questions still ask:

| Companion document | Already settled by dated owner rulings cited there | Diagnosed & proposed there, still awaiting owner ruling |
| :--- | :--- | :--- |
| **[`docs/design/gate-test-caching.md`](./gate-test-caching.md)** (`2026-11-25`, `in-review`) | **2026-11-25 owner bar:** `"if we know it's correct, we can be just as aggressive as possible"` — skip any test unit across gates, replays, restarts, and proofs when the verdict is proved unchanged. Also cites settled [`D-GR2`](../reference/gate.md#D-GR2) (no cross-lane lock), [`D-GR21`](../reference/gate.md#D-GR21) (contradictions disable), [`D-GR49`](../reference/gate.md#D-GR49) (`--cache` outside workers), and [`D-GR53`](../reference/gate.md#D-GR53)/[`D-GR56`](../reference/gate.md#D-GR56) (`tillr-drain/drain` after-merge). | **[`OQ-GC1`](./gate-test-caching.md#OQ-GC1)–[`OQ-GC5`](./gate-test-caching.md#OQ-GC5):** Audits Go 1.27.3's four cache gaps (`0 / 1,776` `drain` shards cached across lane worktrees; unverified cross-module and child `exec.Command` reads) and proposes four layers: (1) content-keyed `.git/tillr-gate/test-verdicts/`, (2) Linux Landlock ABI v11 read sandboxing, (3) single `go test -c` compile with `~20–30 s` checkpointed shards, and (4) leaf-package carving. |
| **[`docs/design/trunk-repair-on-moving-trunk.md`](./trunk-repair-on-moving-trunk.md)** (`2026-11-27`, `in-review`) | Cites settled [`D-GR2`](../reference/gate.md#D-GR2) (mechanisms whose delay rises with trunk traffic are pointed the wrong way), [`OQ-GP1`](./tillr-graph-is-the-path.md#decision-ledger)/[`D-GR6`](../reference/gate.md#D-GR6) (build culprit reverts **on the tip**, squash via `tillr-land`), [`OQ-GP7`](./tillr-graph-is-the-path.md#decision-ledger)/[`OQ-GR1`](../reference/gate.md#OQ-GR1) (no landing count/time cap on a red check), and [`D-GR53`](../reference/gate.md#D-GR53)/[`D-GR56`](../reference/gate.md#D-GR56). | **[`OQ-TRM1`](./trunk-repair-on-moving-trunk.md#OQ-TRM1):** Diagnoses the 2026-11-27 four-deferral loop (`offerRevert`'s `requireCurrentTip`, `RetainRedRepairs`'s unverified input-touch deferral across `go-test-tillr-drain`'s `22` `reads`, false `trunk-repair.json` writes, and `priorDeferredChecks` alternation) and leans toward **Option A**: revert on `currentTip` + extract `go-test:<module>:<pkg>:<TestName>` selectors from failed declaration suites + defer repair **only** on a demonstrated pass. |

### 5.2 Three state classes across a moving tip (`T1 -> T2`)

When `main` advances from `T1` to `T2` during or immediately after a trunk proof, per-test records split into three distinct carryover rules (`D-TPF5`, with input-key strictness before [`OQ-GC1`](./gate-test-caching.md#OQ-GC1) put to the owner in [`OQ-TPF4`](#OQ-TPF4)):

1. **Active `DisabledMarker`s (`disabled/<sha256(testSelector)>.json`) ALWAYS carry over (`T1 -> T2`).**
   Whether `T2` edited `docs/` or `tools/tillr-drain/drain/loop.go`, an already-contradicted test remains `disabled` and excluded from `tillr-gate shards` until its `flaky-*` repair item lands and runs `tillr-gate enable` ([`D-GR22`](../reference/gate.md#D-GR22)). A busy trunk never re-arms a known flake.
2. **Completed `pass` observations carry over whenever `T1..T2` did not change that test unit's inputs.**
   `go-test-tillr-drain.json` declares `22` repository-wide `reads` entries across `11` modules, so whole-check `memory.go` invalidates the entire suite on almost any code commit. At the package/shard/test level:
   - A landing on `docs/`, `.justfile`, `tools/tillr-watch/`, or an unrelated leaf package changes **zero** inputs of `github.com/mschulkind-oss/tillr-drain/drain`. Every `pass` observation recorded on `T1` (including shards completed before `T1`'s proof was cancelled when `T2` landed, matching [`gate-test-caching.md`](./gate-test-caching.md#43-option-3-finer-duration-balanced-shards-and-per-shard-result-checkpointing) Option 3) carries straight over to `T2` without re-running.
   - Even when `T2` edits a sibling module such as `tools/tillr-view/`, narrowing per-test edge inputs via `read_tests` ([`D-GR44`](../reference/gate.md#D-GR44)) and `build_tests` ([`D-GR51`](../reference/gate.md#D-GR51)) preserves the input key of every `drain` test that does not build or read `tillr-view`.
3. **Prior `fail` observations on `T1` either complete a cross-tip contradiction or run first (`fail-fast`) on `T2`:**
   - **Test inputs unchanged between `T1` and `T2` (`fp_T1(t) == fp_T2(t)`):** `t`'s `fail` record from `T1` stays bound to `fp_T1(t)`. If `T1`'s proof was cancelled before its focused rerun completed, `T2`'s proof runs `t` **first**: if `t` passes on `T2`, `Classify` immediately pairs `(fail@T1, pass@T2)` on identical `fp(t)`, marks `t` `disabled`, and files `flaky-<slug>`. If `t` fails again on `T2`, `(fail@T1, fail@T2)` confirms `t` is deterministically red without waiting `10–25 min` for `6` full shards.
   - **Test inputs changed between `T1` and `T2` (`fp_T1(t) != fp_T2(t)`):** Because `T2` changed `t`'s code or declared edges, a `pass` on `T2` is **not** a contradiction of `fail@T1` — `T2` may have genuinely fixed a regression introduced on `T1` (matching [`trunk-repair-on-moving-trunk.md`](./trunk-repair-on-moving-trunk.md#21-option-a--revert-on-currenttip-extract-failing-go-test-cases-for-focused-replay-and-never-defer-on-an-unverified-input-touch-designers-leaning) Option A, which supersedes a red check on a demonstrated pass). Even here, `T2`'s proof **runs `T1`'s failed selectors `{t}` first (`--prior-failed`)** ahead of full shards: if `t` still fails on `T2`, the proof catches it in seconds (and probes `t` on `T2` if [`OQ-TPF1`](#OQ-TPF1) Option A is chosen); if `t` passes on `T2`, the remaining shards verify the rest of `T2`.

---

## 6. Check against landing principles

Every principle in [`docs/design/landing-principles.md`](./landing-principles.md) is checked below, naming each principle this proposal touches and noting the one lander-only entry it leaves untouched:

| Principle in [`landing-principles.md`](./landing-principles.md) | Touched? | How this proposal satisfies it (or what it asks the owner to rule) |
| :--- | :--- | :--- |
| **[§1. A refusal must be a verdict](./landing-principles.md#1-a-refusal-must-be-a-verdict)** | **Yes** | Prevents an unrelated flaky test in `go-test-*` (or `go-affected`, if [`OQ-TPF1`](#OQ-TPF1) Option C is chosen) from convicting an innocent commit; on `flaky-*` items, refuses candidates that delete or `t.Skip` the named test (`goTestAbsent` / `goTestSkipped`) with an explicit candidate verdict. |
| **[§2. Re-application preserves the change, not the patch](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch)** | **Yes (via moving-tip carryover)** | Keying test observations on content blobs rather than commit SHAs or patch IDs means concurrent disjoint landings on `main` do not invalidate unchanged test results; lander squash (`Trunk.Squash`) is untouched. |
| **[§3. Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once)** ([`D-GR2`](../reference/gate.md#D-GR2)) | **Yes** | Pre-merge candidate gates keep zero cross-lane locks (per-test markers use atomic rename and per-selector lock files under `.git/tillr-gate/results/`). Moving-tip carryover and `--prior-failed` stop proof cost and repair latency from scaling up with trunk velocity. |
| **[§4. Every outcome is state-changing](./landing-principles.md#4-every-outcome-is-state-changing)** | **Yes** | Ends the `50e065bc` state-free loop where repeated proofs on the same tip alternated failing tests without advancing `refs/tillr/green`, reverting a culprit, or filing a `flaky-*` item. Every proof run now advances `refs/tillr/green`, disables newly contradicted tests + files `flaky-*`, or attributes deterministic `go-test:` failures. |
| **[§5. Only verdicts charge](./landing-principles.md#5-only-verdicts-charge)** ([`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)) | **Yes** | Watchdog stalls and process errors on a focused rerun classify as `unknown`, never charging or disabling a test; `flaky-*` worker refusals return uncounted without numeric attempt caps. |
| **[§6. A budget escalates, it does not loop](./landing-principles.md#6-a-budget-escalates-it-does-not-loop)** ([`D-JL9`](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9), [`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)) | **Yes** | A `flaky-*` worker whose candidate repeatedly fails `settleFlakyRepairGate` with identical normalized evidence escalates to `design` via `repeatedEnding` rather than looping forever. |
| **[§7. A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report)** | **Yes** | Disabled tests are never hidden as `pass`: `tillr-gate` run summaries list `disabled` counts and selectors apart, and each open `flaky-*` item appears directly in the queue and `tillr-view waiting`. |
| **[§8. A gate that cannot answer must say which](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)** ([`D-GR15`](../reference/gate.md#D-GR15)) | **Yes** | Stalls (`ExitStalled` = `6`), missing binaries, and crashes without test verdicts remain `unknown` / `stalled`, never misclassified as contradictions (`disabled`) and never killed by wall-clock budgets. |
| **[§9. Fix the machinery; never hand-carry the work](./landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work)** | **Yes** | Replaces manual operator filing and ad hoc workarounds with automatic `DisabledMarker` creation, deduplicated `flaky-*` repair items, and automatic `tillr-gate enable` on repair landing. |
| **[§10. Supersession is a verdict, not a stall](./landing-principles.md#10-supersession-is-a-verdict-not-a-stall)** | **No (untouched)** | `already-on-trunk` empty-squash settlement in `tillr-land` and `settleAlreadyOnTrunk` in `tillr-drain` are unchanged (`flaky-*` items also settle cleanly if an earlier commit already fixed the test and `settleFlakyRepairGate` proves it). |
| **[§11. A failure is diagnosed, not waited out](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out)** | **Yes** | Neither outcome of the focused rerun treats a failure as passing or waits on a clock: `(fail, fail)` diagnoses a deterministic trunk regression; `(fail, pass)` diagnoses non-determinism on identical inputs and files `flaky-*` with both logs ([Section 3.3](#33-check-against-landing-principle-11-landing-principle-17-and-the-no-retry-ruling); [`OQ-TPF1`](#OQ-TPF1)). |
| **[§12. A principle cites the measurement that produced it](./landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it)** | **Yes** | Rests directly on the 2026-11-27 commit `50e065bc` two-run proof measurement, the `16 / 249` (`6.43%`) `.rolling/logs` count on `TestIdleLoopRunDoesNotReplanUntilChange`, the `929285ab` escape, and measured single-test vs. 6-shard durations ([Section 1.2](#12-what-happened-on-commit-50e065bc-2026-11-27), [Section 4.1](#41-why-a-single-uncontended-run-lets-flaky-fixes-escape-measured-numbers)). |
| **[§13. A held state names a mover that can still act](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)** | **Yes** | Every `DisabledMarker` is stamped with its active `RepairItem` (`flaky-<slug>`), naming a live queue item dispatched on `TrunkFixerSeat` rather than waiting for an operator session. |
| **[§14. Convict a run on what it did, not on what happened while it ran](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)** | **Yes** | Focused reruns and `settleFlakyRepairGate` execute in private temp/git-config directories (`PrivateGoReplayGitConfig`) without touching sibling lanes or convicting concurrent runs. |
| **[§15. Land the smallest functional change](./landing-principles.md#15-land-the-smallest-functional-change)** ([`D-GR32`](../reference/gate.md#D-GR32)) | **Yes** | Decomposes into five small, independently functional slices ([Section 8](#8-build-slices)) where new `tillr-gate` capabilities land with tests and zero callers before `tillr-drain` switches onto them. |
| **[§16. What the operator can settle is settled inside tillr](./landing-principles.md#16-what-the-operator-can-settle-is-settled-inside-tillr)** | **Yes** | Directly eliminates the manual loop in which an operator had to read `.rolling/logs`, spot alternating test names across proof runs, and file one repair request per test by hand. |
| **[§17. Disable a flaky check, file its repair, and count it neither green nor blocking](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking)** ([`D-GR21`](../reference/gate.md#D-GR21)) | **Yes (core subject)** | Closes both gaps that kept [§17](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking) from firing on `go-test-tillr-drain`: narrows contradiction units from whole suites to individual Go test selectors, wires live shard exclusion, and wires `--prove-disabled` into `flaky-*` pre-merge verification. |

---

## 7. What is deliberately not done

1. **No suite-wide retry and no retry-to-green budget.** Re-running all `6` shards of `go-test-tillr-drain` on failure wastes `6.6–38.9 min` and trips unrelated flakes; crediting a `fail -> pass` flip as `pass` would violate [`D-GR21`](../reference/gate.md#D-GR21) and [gate check rules](../reference/gate-check-rules.md#a-red-is-read-once-never-re-run).
2. **No static quarantine list or blob-pinned skip file without a `DisabledMarker` and open `flaky-*` item.** [`docs/reference/gate-check-rules.md`](../reference/gate-check-rules.md#a-check-set-aside-from-the-verdict-is-narrow-ticketed-and-counted) notes that no static case-quarantine file exists; adding a hand-edited skip list in git would require commits to disable a flake and could outlive its repair. Store-backed `DisabledMarker`s + automatic `flaky-*` items + `tillr-gate enable` require zero hand-commits and cannot outlive the landed repair.
3. **No solo rerun of timed-out or deadlocked tests ([`D-GR31`](../reference/gate.md#D-GR31)) or stalled processes ([`D-GR16`](../reference/gate.md#D-GR16)).** A test that hit Go's timeout panic or `all goroutines are asleep - deadlock!` has already proved a hang/deadlock defect and would risk hanging the proof runner if re-executed.
4. **No whole-suite (`go-test-tillr-drain`) execution inside a `flaky-*` item's verify gate.** Running `2,144` unrelated tests to verify a fix to one named test wastes `10–25 min` and exposes the repair candidate to *other* tests; the ordinary fast gate covers compile/import-affected non-deferred packages while `settleFlakyRepairGate` hammers only the named test selector.
5. **No silent closing or re-arming of a `flaky-*` item when unrelated commits edit its package.** Under [`D-GR22`](../reference/gate.md#D-GR22), an unrepaired flaky test stays disabled across input changes until its `flaky-*` item lands and proves the test (`--prove-disabled`), rather than re-entering the trunk proof unfixed every time `tools/tillr-drain/drain/` is touched.
6. **No document item blocks in tracked Markdown.** Generated `flaky-*` items live solely in the queue store (`flaky_repair.go`), keeping tracked Markdown untouched ([`D-GR23`](../reference/gate.md#D-GR23)).

---

## 8. Build slices

Each slice below can be filed as a separate item obeying [Landing Principle §15](./landing-principles.md#15-land-the-smallest-functional-change), the installed-`tillr-gate` rollout rule ([`D-GR32`](../reference/gate.md#D-GR32)), the pre-merge gate fence ([`OQ-GR3`](../reference/gate.md#OQ-GR3), [`OQ-GR6`](../reference/gate.md#OQ-GR6)), and per-step question holds ([`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8)):

1. **Slice 1 — Per-test classification, `DisabledMarker` slicing, and active exclusion in `tillr-gate shards` (gate-fenced: `tools/tillr/checkresult/` + `tools/tillr-gate/` + `just pack-sync`; zero `tillr-drain` callers).**
   - Teach `tillr-gate shards` (when given `--store <dir>` and `--module-id <module>`) to attribute leaf test/subtest `pass` and `fail` events, slice per-test logs, classify each test via `checkresult.Classify` (`go-test:<module>:<pkg>:<TestName>`), write `DisabledMarker`s on `(pass, fail)` contradictions, exclude active disabled top-level tests from `Assign` and active disabled subtests via verified unparenthesized `-skip` patterns (unless named in `--prove-disabled`), report `disabled` test counts apart in verdict/summary output, and include active disabled test IDs in `go-test-*`'s `memory.go` fingerprint.
   - *Question dependencies:* [`OQ-TPF2`](#OQ-TPF2) (subtest vs. top-level unit) and [`OQ-TPF4`](#OQ-TPF4) (per-test input fingerprint scope).
2. **Slice 2 — Focused contradiction probe and `--prior-failed` ordering in `tillr-gate shards` (gate-fenced: `tools/tillr-gate/` + `just pack-sync`; zero `tillr-drain` callers).**
   - Add opt-in `--probe-contradictions` (running failed non-`D-GR31` test selectors once with `-count=1` on the same checkout and feeding both observations to `Classify` so `(fail, pass)` becomes `disabled` and `(fail, fail)` stays `fail`) and `--prior-failed <selectors>` (running prior-tip failed selectors before full shards).
   - *Question dependencies:* [`OQ-TPF1`](#OQ-TPF1) (whether `--probe-contradictions` is authorized in proof / gate). `--prior-failed` ordering has no open policy conflict once Slice 1 lands.
3. **Slice 3 — Wire `tillr-drain` trunk proof and `flaky_repair.go` onto per-test disabling (`tools/tillr-drain/` + `just pack-sync`).**
   - Landed only after `refs/tillr/green` contains Slices 1–2 ([`D-GR32`](../reference/gate.md#D-GR32)). Pass `--store` (and `--probe-contradictions` if ruled in [`OQ-TPF1`](#OQ-TPF1), plus `--prior-failed` from the previous proof's failed `go-test:` selectors) from `JudgeHeavy` ([`tools/tillr-drain/internal/gatespans/heavy_steps.go`](../../tools/tillr-drain/internal/gatespans/heavy_steps.go)), let suites with zero non-disabled failures advance `refs/tillr/green`, extract remaining deterministic `go-test:` failures for `FindFirstRed`, and bound `FlakyRepairBaseID` slug length in [`tools/tillr-drain/drain/flaky_repair.go`](../../tools/tillr-drain/drain/flaky_repair.go).
   - *Question dependencies:* Follows Slice 1 and [`OQ-TPF1`](#OQ-TPF1).
4. **Slice 4 — Pre-merge `settleFlakyRepairGate` for `flaky-*` items (`tools/tillr-drain/drain/fixer_gate.go` + `loop.go` + `just pack-sync`).**
   - Route `IsFlakyRepairItem` candidates through `settleFlakyRepairGate` after the ordinary fast gate: resolve the item's `go-test:<module>:<pkg>:<TestName>` (or declaration check ID), pass `--prove-disabled`, refuse `goTestAbsent` and `goTestSkipped`, compile `./<pkg>` once, run `CGO_ENABLED=1 go test -race -count=1` on the named selector, and run the repetition/contention bar chosen in [`OQ-TPF3`](#OQ-TPF3) before allowing merge and `tillr-gate enable`.
   - *Question dependencies:* [`OQ-TPF3`](#OQ-TPF3) only (independent of [`OQ-TPF1`](#OQ-TPF1) under [`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8), and already usable for existing `flaky-*` items).
5. **Slice 5 — Reference and landing-principles sync (`docs/reference/gate.md`, `docs/reference/gate-check-rules.md`, `docs/design/landing-principles.md`).**
   - Record the owner's [`OQ-TPF1`](#OQ-TPF1)–[`OQ-TPF4`](#OQ-TPF4) rulings verbatim and update *Enforced by* / *Not enforced* in [Landing Principle §17](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking) once Slices 1–4 are built.

---

## 9. Open questions

1. 💬 **OQ-TPF1: When a Go test fails for the first time in the trunk proof with no stored pass on those inputs yet, should the proof run that failed test once more on the same tip to check for a contradiction?**

   Today, the gate rules say a failed check runs once and is never re-run, because re-running to get a green pass hides flakes ([`docs/reference/gate-check-rules.md`](../reference/gate-check-rules.md#a-red-is-read-once-never-re-run); [`D-GR22`](../reference/gate.md#D-GR22)). In the trunk proof on 2026-11-27, you asked for the proof to run just the failing tests instead of the whole suite.

   Suppose a 2,144-test proof finishes and two tests failed. If the proof runs **only those two tests once more** on the exact same commit (`~2–40 s`), neither outcome treats either test as passing: a test that fails again is confirmed broken and triggers culprit bisection or automatic revert, while a test that passes on the identical commit has contradicted itself, so tillr disables that one test, files its `flaky-*` repair item with both logs, and lets the other 2,142 passing tests prove the trunk green. Without that focused probe, a flaky test whose inputs changed recently has no prior pass record yet, so the first proof treats it as a real trunk break and runs culprit bisection across recent landings until one of those replays happens to pass.

   - **A — Authorize one focused contradiction rerun of failed non-timeout Go tests in the trunk proof (amending [`D-GR22`](../reference/gate.md#D-GR22) and [`gate-check-rules.md`](../reference/gate-check-rules.md#a-red-is-read-once-never-re-run) for that proof probe only).** When a heavy Go suite fails on named tests (excluding timeouts, deadlocks, build errors, and stalls) that do not already have a stored pass on the same inputs, run only those failed test selectors once more on the same tip (`-count=1`). `fail + fail` stays red; `fail + pass` marks the test `disabled` (never `pass`) and files its `flaky-*` repair item. Pre-merge candidate gates remain strictly one-run.
   - **B — Passive cross-run per-test memory only (no live second run in the proof; [`D-GR22`](../reference/gate.md#D-GR22) and [`gate-check-rules.md`](../reference/gate-check-rules.md#a-red-is-read-once-never-re-run) stay unamended).** Record per-test `pass` and `fail` fingerprints on every run so a test that passed on an earlier run with identical inputs and fails now (or fails in Run 1 and passes during culprit bisection or Run 2) is disabled per test, without adding a second focused run inside the first failing proof.
   - **C — Authorize the single focused contradiction rerun in both the trunk proof and pre-merge `go-affected` candidate gates.** Apply Option A's one-shot probe (`fail + pass -> disabled + flaky-*`, never `pass`) to pre-merge `go-affected` failures as well, so a newly touched pre-merge flaky test disables itself on the first hit instead of triggering a base replay first.

   <!-- vantage: question id=OQ-TPF1 leaning="A — authorize the single focused contradiction probe on failed non-timeout Go tests in the trunk proof while keeping pre-merge candidate gates single-run: it carries out your 2026-11-27 request to run just the failing tests in the proof, never credits a pass, and avoids spending culprit bisection on a flake before disabling it." -->

   _Leaning (designer's leaning, not an owner ruling):_ **A** — it carries out the 2026-11-27 request that the proof run just the failing tests instead of the whole suite, keeps `fail + pass` strictly as `disabled` + filed repair (never `pass`), and avoids running multi-step culprit bisection before discovering a newly touched test is flaky, while keeping pre-merge candidate gates single-run (where base replay already acquits pre-existing trunk flakes).

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-TPF2: When a subtest (`t.Run`) contradicts itself, should tillr disable just that subtest or the enclosing top-level `Test*` function?**

   On commit `50e065bc`, `TestWhyAfterRestartOverLiveEvaluator` had two subtests: `first_segment` passed, and `resumed_segment` failed in Run 1 then passed in Run 2 ([Section 1.2](#12-what-happened-on-commit-50e065bc-2026-11-27)).

   Suppose a table-driven Go test has ten `t.Run` subtests and only one subtest is flaky. Disabling the failing leaf subtest (`TestParent/subtest_name`) via Go's `-skip` flag keeps the other nine subtests proving the trunk while the one broken subtest is repaired, though Go still runs `TestParent`'s shared setup code before `t.Run`. Disabling the whole top-level `TestParent` skips its setup code too, at the cost of losing coverage from all nine healthy subtests until the repair lands.

   - **A — Leaf subtest granularity (`go-test:<module>:<pkg>:<Parent>/<Sub>`), widening to `<Parent>` only when `<Parent>` fails outside any subtest.** Disable and file `flaky-*` for the specific contradicting subtest (`.../TestWhyAfterRestartOverLiveEvaluator/resumed_segment`), keeping sibling subtests (`first_segment`) active in the proof via Go's `-skip` flag.
   - **B — Top-level `Test*` function granularity (`go-test:<module>:<pkg>:<Parent>`).** Attribute every subtest pass or fail to its enclosing `Test*` function, omit the whole `Test*` function from shard assignment while disabled, and restore all of its subtests together when `flaky-*` lands.

   <!-- vantage: question id=OQ-TPF2 leaning="A — leaf subtest granularity when a subtest fails (widening to the parent Test* only when the parent fails outside subtests), preserving coverage of healthy sibling subtests in table-driven suites and matching JudgeHeavyGo's existing subtest selector support." -->

   _Leaning (designer's leaning, not an owner ruling):_ **A** — `JudgeHeavyGo` already supports `/` subtest selectors, Go 1.27.3's `-skip` flag skips individual subtests cleanly when joined with unparenthesized `|` ([Section 2.3](#23-excluding-disabled-tests-in-tillr-gate-shards-so-the-rest-of-the-suite-proves-the-trunk-green)), and table-driven Go tests otherwise lose coverage of healthy cases while one case is under repair.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-TPF3: What verification bar must a `flaky-*` repair item pass on the named test before landing and re-enabling it?**

   `TestIdleLoopRunDoesNotReplanUntilChange` failed in `16 / 249` (`6.43%`) suite logs even after an earlier fix (`929285ab`) landed ([Section 4.1](#41-why-a-single-uncontended-run-lets-flaky-fixes-escape-measured-numbers)). At a `6.43%` failure rate, `1` run passes an unfixed flake `93.6%` of the time, `10` runs pass it `51.5%` of the time, `46` runs bound false passes below `5%` (`70` runs below `1%`), and quiet sequential runs can still miss load-induced goroutine races if run without `-race` or CPU contention.

   Suppose an agent offers a fix for `flaky-...-TestIdleLoopRunDoesNotReplanUntilChange`. Whatever option you choose, the pre-merge gate will compile the package once, refuse any diff that deletes or `t.Skip`s the test or adds wall-clock timing bounds, and run **only the named test** (never the whole `drain` suite). The trade-off is how much proof of non-flakiness you want before merge versus gate wall time (`~1–4 min` across parallel workers for `46` stress runs of a `17–29 s` test, versus `~20–30 s` for a single `-race` run):

   - **A — Race detector + parallel contention stress sized from the measured failure rate (`alpha = 0.05`, `p_floor = 0.05`, capped at `59` runs across `W = 6` concurrent `GOMAXPROCS=1` workers) + review check for causal fix.** Compile `./<pkg>` once, run `1` `-race` pass plus `N = ceil(ln(0.05) / ln(1 - max(p_hat, 0.05)))` concurrent invocations of the named test binary (`N = 46` at `p_hat = 16/249`, `N = 59` when `p_hat < 5%` as occur when only `1` failure was seen before disabling; `~40 s` for sub-second tests, `~2–5 min` across `6` workers for `17–29 s` tests), while the code reviewer checks that the diff removes the race/polling cause rather than padding sleeps.
   - **B — Same `-race` + rate-derived concurrent stress (`alpha = 0.05`, `p_floor = 0.05`, `W = 6`), with no extra review rule beyond ordinary diff review.** Rely purely on the pre-merge gate's `elapsed-time` check, anti-skip check, `-race` run, and `N` concurrent stress runs.
   - **C — Single `-race` + `-count=1` `--prove-disabled` run (`N = 1` + `1` race run) before merge, relying on future trunk proofs to re-disable (`flaky-<slug>-2`) if an incomplete fix still flakes.** Zero statistical sample count (`~20–40 s` gate), accepting that load-timing flakes invisible to `-race` (like a `150 ms` sleep race) will land and re-trip on a later trunk proof if the worker's fix was incomplete.
   - **D — Custom confidence target or fixed count.** You specify your preferred false-pass ceiling `alpha` (for example `0.01` -> `N = 70` at `6.43%`), floor rate, or fixed parallel run count.

   <!-- vantage: question id=OQ-TPF3 leaning="A — compile the package once, run 1 -race pass plus rate-derived concurrent GOMAXPROCS=1 stress runs (alpha = 0.05, p_floor = 0.05 -> 46 to 59 runs across 6 workers, ~40 s to 5 min on the named test alone) paired with normal diff review checking for a causal fix, because N = 1 (Option C) already let 929285ab land without fixing TestIdleLoopRunDoesNotReplanUntilChange." -->

   _Leaning (designer's leaning, not an owner ruling):_ **A** (or **B** if you want the rule kept strictly mechanical without an extra review instruction) — Option C (`N = 1`) has a `93.57%` chance of passing an unfixed `6.43%` timing flake when `-race` sees no data race (as happened when `929285ab` left the `150 ms` sleep race in `idle_test.go`), whereas `46–59` concurrent `GOMAXPROCS=1` executions of the single compiled test binary take `~40 s` to `~5 min` without touching the rest of the suite.

   **Answer:**

   > _(empty — fill in when decided)_

4. 💬 **OQ-TPF4: Before kernel read sandboxing ([`OQ-GC1`](./gate-test-caching.md#OQ-GC1) in [`gate-test-caching.md`](./gate-test-caching.md)) lands, what input fingerprint should bind per-test pass and fail records across moving commits?**

   On a **single commit** (like Run 1 vs. Run 2 on `50e065bc`, or a shard run vs. its focused rerun on the same tip), every file in the repository is identical, so two answers for one test are `100%` guaranteed to share identical inputs. Across **two different commits** `T1` and `T2`, [`gate-test-caching.md`](./gate-test-caching.md#3-soundness-gaps-in-gos-test-cache-and-this-trees-exposure) showed that `4` packages outside `go-test-edges.json` read files or build sibling binaries in child processes without declaring them yet ([`OQ-GC1`](./gate-test-caching.md#OQ-GC1)).

   Suppose commit `T1` runs `go-test-tillr-drain` and commit `T2` lands a `docs/` edit or a `tools/tillr-view/` edit while the proof is running:

   - **A — Use narrowed per-package + `go-test-edges.json` (`read_tests` / `build_tests`) content keys immediately for both cross-commit pass carryover and contradiction detection (fixing the four known undeclared-edge packages from [`gate-test-caching.md`](./gate-test-caching.md#32-files-outside-the-packages-module) in Slice 1), then layer Landlock enforcement on top if ruled in [`OQ-GC1`](./gate-test-caching.md#OQ-GC1).** Lets `docs/` and unrelated-module landings carry over completed shard/test results immediately instead of restarting `go-test-tillr-drain` from scratch.
   - **B — Use exact commit tree (`HEAD^{tree}`) for contradiction disabling until [`OQ-GC1`](./gate-test-caching.md#OQ-GC1) Landlock enforcement lands, while still running `--prior-failed` tests first and keeping Go's existing `--cache` + `TILLR_GATE_EDGE_INPUTS` on a moving tip.** Guarantees zero false `DisabledMarker`s from an undeclared cross-module read before Landlock is live, while same-commit proof runs and same-commit focused probes ([`OQ-TPF1`](#OQ-TPF1) Option A) still catch and disable every flake immediately.

   <!-- vantage: question id=OQ-TPF4 leaning="A — key per-test/per-shard observations on the package import closure plus declared go-test-edges.json digests immediately (after closing the 4 missing edge declarations audited in gate-test-caching.md), and add Landlock enforcement when OQ-GC1 lands; or B if you chose OQ-GC1 Option A/C and want zero cross-commit contradiction/skip claims before Landlock is enforced." -->

   _Leaning (designer's leaning, not an owner ruling):_ **A** for `tools/tillr-drain` (whose cross-module `builds` and `reads` are already declared in [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json)), closing the `4` missing-edge packages noted in [`gate-test-caching.md`](./gate-test-caching.md#32-files-outside-the-packages-module) in the same slice; choose **B** if you want cross-commit carryover and cross-commit contradiction strictly gated behind [`OQ-GC1`](./gate-test-caching.md#OQ-GC1)'s Landlock sandbox while getting same-tip per-test disabling right away.

   **Answer:**

   > _(empty — fill in when decided)_

---

## Decision Ledger

Every row below is a designer's decision prepared from the 2026-11-27 filed request; none is an owner ruling, and the owner may overturn any of them ([`D-PR1`, `D-PR2`](../tillr/decision-provenance-review.md#6-how-this-stops-happening)). Owner choices in [`OQ-TPF1`](#OQ-TPF1)–[`OQ-TPF4`](#OQ-TPF4) will enter this ledger only when closed with the owner's dated words verbatim ([`OQ-PR22`](../tillr/decision-provenance-review.md#OQ-PR22)).

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-TPF1"></a>`D-TPF1` | **Designer's decision; the owner may overturn this (and [`OQ-TPF2`](#OQ-TPF2) governs subtest vs. top-level scope).** Represent individual Go test checks with the existing `JudgeHeavyGo` selector `go-test:<module>:<pkg>:<TestName>`; record `fail` on a parent test selector only when zero descendant subtests failed in that run; slice per-test `pass` and `fail` logs from `go test -json` `TestEvent` streams into `<store>/disabled/`; omit disabled top-level tests from `shards.Assign`; and when subtest disabling is active, build Go `-skip` flags using unparenthesized top-level `|` with disabled parents pruning descendant subtest patterns, including active disabled selectors in `go-test-*` tree-memory keys. | 2026-11-28 | [Section 2](#2-the-unit-of-contradiction-exclusion-and-automatic-repair-filing) | No |
| <a id="D-TPF2"></a>`D-TPF2` | **Designer's decision; the owner may overturn this.** Reuse `Loop.sweepDisabledMarkers` (`tools/tillr-drain/drain/flaky_repair.go`) without document blocks for `go-test:...` disabled markers, bounding `FlakyRepairBaseID` slugs over `96` bytes with a deterministic 12-hex SHA-256 suffix so deeply nested subtest names fit worktree and socket path limits. | 2026-11-28 | [Section 2.4](#24-automatic-flaky--repair-filing-by-the-loop) | No |
| <a id="D-TPF3"></a>`D-TPF3` | **Designer's decision; the owner may overturn this (and [`OQ-TPF1`](#OQ-TPF1) governs whether the live probe runs).** Exclude [`D-GR31`](../reference/gate.md#D-GR31) timeout/deadlock panics, compilation errors, unnamed crashes, and [`D-GR16`](../reference/gate.md#D-GR16) stalls from focused contradiction probing; classify `(fail, pass)` strictly as `Outcome: Disabled` (never `Pass`) and `(fail, stalled/error)` as `Outcome: Unknown` (never `Disabled` or `Pass`). | 2026-11-28 | [Section 3](#3-running-only-what-failed-and-what-the-second-result-means) | No |
| <a id="D-TPF4"></a>`D-TPF4` | **Designer's decision; the owner may overturn this (and [`OQ-TPF3`](#OQ-TPF3) governs the repetition/stress bar).** Route `IsFlakyRepairItem` (`flaky-*`) candidates through a post-fast-gate `settleFlakyRepairGate` step passing `--prove-disabled`, compiling `./<pkg>` once, running only the named test selector (never the whole suite), and refusing `goTestAbsent` and `goTestSkipped` as candidate failures rather than dropping them. | 2026-11-28 | [Section 4.2](#42-proposed-verify-and-pre-merge-gate-contract-for-flaky--items-d-tpf4-repeatload-bar-in-oq-tpf3) | No |
| <a id="D-TPF5"></a>`D-TPF5` | **Designer's decision; the owner may overturn this (and [`OQ-TPF4`](#OQ-TPF4) governs pre-Landlock key scope).** Across a moving trunk tip `T1 -> T2`, carry active `DisabledMarker`s unconditionally (`D-GR22`), run `T1`'s failed `go-test:` selectors first (`--prior-failed`) on `T2` ahead of full shards, and treat `pass@T2` after `fail@T1` as a contradiction when the test's input fingerprint is unchanged and as a potential trunk repair when `T1..T2` modified the test's inputs. | 2026-11-28 | [Section 5.2](#52-three-state-classes-across-a-moving-tip-t1---t2) | No |
