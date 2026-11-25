---
title: "Judging a candidate gate failure on a green-proved base when an open trunk fixer already names the failing case"
status: in-review
stage: DECIDED
stage-since: 2026-11-25
updated: 2026-11-25
tags:
  - tillr
  - drain
  - gate
  - fixer
  - proof
  - design
summary: "On 2026-11-24, a shard-order-dependent drain test (TestNonLandingLoopStartsWithoutGit) passed on the six-way trunk proof split and failed on candidate gates: after the first failing gate replayed red on its unproved base and tip and filed a trunk fixer, two subsequent candidates whose bases carried green proof records failed on the identical test case and assertion, skipped base replay in judgeTrunkChecks, and received RefusalGate back to their workers (~27 minutes per gate). This design settles the recorded fact on open trunk fixers (structured failing_cases in drain.fixer-logs plus go-test: selectors in trunk-green), acquits or promotes untouched candidates on that open fixer under OQ-GP7, and keeps ordinary RefusalGate when the candidate's diff touches the matched failing test."
next: "Build the single item in Section 7 covering tools/tillr-drain/drain/, targeted drain tests, and just pack-sync; the owner may review or overturn designer decisions D-FCM1, D-FCM2, and D-FCM3."
depends-on:
  - landing-principles.md
  - ../reference/gate.md
  - tillr-graph-is-the-path.md
  - tillr-judge-and-lifecycle.md
  - tillr-watch/decision-record-files.md
  - ../tillr/decision-provenance-review.md
vantage:
  status-chip: true
---

# Judging a candidate gate failure on a green-proved base when an open trunk fixer already names the failing case

**Status:** Verified at `972a5fbc887f` on 2026-11-25; nothing specified here is built yet (`rg -n 'failing_cases' tools/tillr-drain` returns nothing). In [`trunk.go`](../../tools/tillr-drain/drain/trunk.go), `judgeTrunkChecks` treats `green-<base>.json` whole-module coverage (`goAffectedCoveredByGreenProof`) as proving `go-affected/affected-tillr-go-test-packages-pass` green on the base without running a replay, while `routeTrunkFixer` records only check IDs in `trunk-green` and file paths in `drain.fixer-logs` (`FixerLogsKind`), so `settleTrunkAlreadyRed` cannot tell that an open `fixer-main-*` already owns the candidate's failing test case. Decisions [`D-FCM1`](#D-FCM1), [`D-FCM2`](#D-FCM2), and [`D-FCM3`](#D-FCM3) in the [Decision Ledger](#decision-ledger) are designer decisions made on a filed operator request; none is an owner ruling, and the owner may review or overturn any of them.

> **In short.** When a trunk fixer (`fixer-main-<sha>`) is created or attached in `routeTrunkFixer`, the drain extracts the trunk-confirmed concrete failing test cases from the check logs and `go-test:` selectors and persists them as structured `failing_cases` in the `drain.fixer-logs` event ([`D-FCM1`](#D-FCM1)). When a subsequent candidate gate fails and `trunkAlreadyRed` does not confirm a trunk red (for example because `green-<base>.json` covered the failing check on the trunk proof's shard split), the drain compares every failing case of the candidate gate against open `fixer-main-*` items' recorded failing cases: if every failing check resolves solely to matched cases on an open trunk fixer and the candidate's diff does **not** touch any matched failing test, the drain applies [`OQ-GP7`](./tillr-graph-is-the-path.md#decision-ledger) — promoting disjoint `reads` to `pass-inherited` and holding intersecting candidates behind that open `fixer-main-*` via `RefusalTrunkAlreadyRed` and kept-candidate `drain.fixer-attached` without re-running culprit bisection or returning the item to its worker ([`D-FCM2`](#D-FCM2)). If the candidate's diff **does** touch a matched failing test, the open fixer match does not acquit the candidate and the ordinary `RefusalGate` (`step: "gate"`) stands ([`D-FCM3`](#D-FCM3)).

- **Why it matters.** A test whose result depends on process or shard ordering can pass on the trunk proof's shard split (`green-<base>.json` green) while failing deterministically on the candidate gate's shard split. On 2026-11-24, two finished candidates hit `TestNonLandingLoopStartsWithoutGit` eleven minutes after the drain had already filed a trunk fixer for that exact failure; because their bases had green proof records, both were sent back to their workers on `RefusalGate`, burning two extra worker turns and two ~27-minute candidate gates ([Section 1](#1-the-2026-11-24-measurement-and-confirmed-source-reading)).
- **The shape.** Three cohesive rules in [`tools/tillr-drain/drain/`](../../tools/tillr-drain/drain/):
  1. **Recorded fact (`D-FCM1`, [Section 3](#3-the-recorded-fact-structured-failing-cases-on-open-trunk-fixers-d-fcm1)):** `routeTrunkFixer` persists trunk-confirmed `{check_id, package, case, where, assertion}` entries in `drain.fixer-logs` (`failing_cases`), read alongside `go-test:<module>:<package>:<TestName>` selectors in `trunk-green` with zero log re-parsing or prose heuristics at match time.
  2. **Untouched candidate disposition (`D-FCM2`, [Section 4](#4-judging-an-untouched-candidate-whose-failing-cases-match-an-open-trunk-fixer-d-fcm2)):** When every failing case of an unconfirmed gate failure matches an open `fixer-main-*` and the candidate touches none of those failing tests, `settleTrunkAlreadyRed` applies [`OQ-GP7`](./tillr-graph-is-the-path.md#decision-ledger) (`GatePassedInherited` when disjoint from `reads`; kept-candidate `RefusalTrunkAlreadyRed` + `drain.fixer-attached` hold on `fixerID` when intersecting).
  3. **Touched-test boundary (`D-FCM3`, [Section 5](#5-when-the-candidates-change-also-touches-the-matched-failing-test-d-fcm3)):** AST function-span overlap on `base_sha..head_sha` for Go tests (and test-file diff overlap for non-Go tests) detects edits to a matched failing test and leaves `RefusalGate` in force.
- **Cost.** One build item ([Section 7](#7-follow-on-build-item)) confined to `tools/tillr-drain/drain/`, targeted unit tests, and `just pack-sync`, with zero SQLite schema migrations, zero prose-matching heuristics, and zero fallbacks.
- **Provenance:** Governing owner rulings are cited in [Section 2](#2-governing-rulings-and-landing-principles). [`D-FCM1`](#D-FCM1), [`D-FCM2`](#D-FCM2), and [`D-FCM3`](#D-FCM3) are designer decisions from a filed operator request that the owner may overturn.

---

## 1. The 2026-11-24 measurement and confirmed source reading

### 1.1 Production timeline on 2026-11-24

Measured on 2026-11-24 (ET), before commit `463da393` repaired the shared test-binary build in `tools/tillr-drain/drain/cli_helpers_test.go`:

- **Underlying shard-split failure:** `TestNonLandingLoopStartsWithoutGit` in `tools/tillr-drain/drain/cli_helpers_test.go` stubbed `git` in `PATH` to fail; whenever it ran as the **first** test in its `go test` shard process to build the shared `tillr-drain` binary, `go build` failed with `"building tillr-drain failed ... error obtaining VCS status: exit status 99"`. The after-merge trunk proof split `tools/tillr-drain` six ways (`go-tool/tools-tillr-drain-s-whole-suite-passes`) so another test built the binary first and the proof passed; candidate gates (`go-affected/affected-tillr-go-test-packages-pass`) split the package differently and failed.
- **`09:46 ET` (first candidate):** The `gate-affected-tests-slow` alert item failed `go-affected` on `TestNonLandingLoopStartsWithoutGit`. Its base had no covering green proof record, so `judgeTrunkChecks` replayed the named case on its base and on the pre-rewrite trunk tip `3462584487` (`replay_go_affected.go`), reproduced red on both, and at **`10:00:48 ET`** filed `fixer-main-3462584487` and held the item behind it as `trunk-already-red` [unlanded].
- **`10:11:45 ET` and `10:12:01 ET` (second and third candidates):** While that trunk fixer was already open and running its own gate, two more candidates (`limit part 1` on pre-rewrite base `2d71a3b9c7`, and `the base-replay fix` on pre-rewrite base `d544ac91a9`) failed `go-affected` with the identical case (`TestNonLandingLoopStartsWithoutGit` in `cli_helpers_test.go`) and assertion (`"building tillr-drain failed ... error obtaining VCS status: exit status 99"`) [unlanded]. Because both bases had `green-<base>.json` proof records from the six-way trunk proof, the drain ran no base replay, recorded `drain.refusal` with `step: "gate"` (`RefusalGate`) for each, and sent both items back to their workers — costing two wasted worker attempts and two ~27-minute re-gates.

### 1.2 Confirmed source reading at `972a5fbc887f`

Every step of the 2026-11-24 sequence holds in [`tools/tillr-drain/drain/`](../../tools/tillr-drain/drain/) at `972a5fbc887f`:

1. **`judgeTrunkChecks` skips base replay when `green-<base>.json` covers the failing module (`trunk.go`, introduced in `881344b1`).**
   In `trunkAlreadyRed` ([`trunk.go`](../../tools/tillr-drain/drain/trunk.go)), an ordinary candidate gate failure sets `useBaseProof = true` (`gate.Outcome == GateFailed && candidate.BaseRef == ""`) and calls `judgeTrunkChecks(ctx, replay, candidate, base, gate.FailingChecks, useBaseProof)`. When `readGreenProofFile(dir, base)` finds a proved `green-<base>.json` whose `Passed` slice covers every Go module owning the failing packages (`goAffectedCoveredByGreenProof`), `passed[goAffectedCheckID]` becomes `true`, `rest` becomes empty, and `judgeTrunkChecks` returns `ReplayOutcome{Rev: base, Known: true}` (`Red: nil`, `source: "proof record"`). `trunkAlreadyRed` therefore returns `trunkReading{Confirmed: false, RepairedAt: "", Evidence: "base proof record"}`. (By the same code path in `trunkAlreadyRed`, if a candidate starts on an unproved base that replays red while a newer trunk tip has a six-way `green-<tip>.json`, `judgeTrunkChecks` on `tip` answers `"proof record"` with `tipOut.Red == nil` and sets `reading.RepairedAt = tip` with `reading.Evidence = "base replay; tip proof record"` even while the open `fixer-main-*` remains unresolved.)

2. **Neither `settleTrunkAlreadyRed` nor `holdForOpenTestFix` can match an open `fixer-main-*` filed from `go-affected` (`trunk.go`, `test_fix_hold.go`).**
   When `!reading.Confirmed && reading.RepairedAt == ""`, `settleTrunkAlreadyRed` calls `holdForOpenTestFix` ([`test_fix_hold.go`](../../tools/tillr-drain/drain/test_fix_hold.go), introduced in `a290da10`). That path misses `fixer-main-*` for two independent reasons:
   - **No recorded case name in the store for gate-replay fixers:** When `routeTrunkFixer` ([`trunk.go`](../../tools/tillr-drain/drain/trunk.go)) creates or attaches `fixer-main-<sha>` for `go-affected/affected-tillr-go-test-packages-pass`, `items.title` is `"fix the broken trunk at <sha>"`, `item_criteria.text` (`trunk-green`) records only `(red: go-affected/affected-tillr-go-test-packages-pass)` (unlike `HeavyRunner`'s `goSuiteFailures` in [`heavy_go.go`](../../tools/tillr-drain/drain/heavy_go.go), which writes `go-test:<module>:<package>:<TestName>`), `items.prose` and `item.RequestWords` are empty, and `drain.fixer-logs` (`FixerLogsKind`) stores only `check_logs` file paths. `findOpenTestFixer` -> `itemFixesTest` therefore sees no test identifier on `fixer-main-*`.
   - **Different hold contract from trunk fixers (`26afa050`, `c7361cae`):** `holdForOpenTestFix` is built for ordinary queue items filed to repair a test (`WaitingOn.Reason = "test-fix-open: ..."`, `candidate.trunkAcquitted = true`, `candidate.trunkFixer = nil`, no `RefusalTrunkAlreadyRed`, no `drain.fixer-attached`, releasing in `releasableItemWait` only when the blocker lands). Holding a kept candidate behind `fixer-main-*`, by contrast, requires [`OQ-GP7`](./tillr-graph-is-the-path.md#decision-ledger) `reads` evaluation, `RefusalTrunkAlreadyRed`, kept-candidate `drain.fixer-attached` (`kept_candidate: true`) after `gate.finishJudgment()` so `gateJudgmentEpoch` ([`events.go`](../../tools/tillr-drain/drain/events.go)) advances for re-gating and [`drain-fixer-held-new-attempt.toml`](../../config/tillr/watch/drain-fixer-held-new-attempt.toml) watches the kept candidate, and `repairedTrunkWaiter` ([`trunk_release.go`](../../tools/tillr-drain/drain/trunk_release.go)) so resolution by `resolveProvedFixers` or operator close also releases the waiter.

3. **Why same-check flake disabling (`D-GR21`) did not fire.**
   Under [Landing principles Section 17](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking), `tillr-gate` disables a check on an exact-fingerprint contradiction of the **same** check ID. Here, the trunk proof ran `go-tool/tools-tillr-drain-s-whole-suite-passes` (`scope: "tree"`) while the candidate gate ran `go-affected/affected-tillr-go-test-packages-pass` (`scope: "change"`, `remember: false` in [`go-affected.json`](../../tools/gate/checks/go-affected.json)), so no same-check fingerprint contradiction existed.

---

## 2. Governing rulings and landing principles

### 2.1 Settled owner rulings that constrain this design

Every choice in this document is engineering detail inside settled owner rulings:

- **Waiting vs `pass-inherited` on a red trunk ([`OQ-GP7`](./tillr-graph-is-the-path.md#decision-ledger), 2026-11-18; quoted in [`OQ-PR20`](../tillr/decision-provenance-review.md#decision-ledger)):** when a check is already red on the trunk, finished work that did not cause it lands as `pass-inherited` when its changed paths are disjoint from the red check's declared `reads`, and waits on the repair naming the intersecting paths when its changes touch those `reads`.
- **No fallbacks ([`R-TW-DR2`](./tillr-watch/decision-record-files.md#R-TW-DR2), 2026-11-16: `"Explicitly, we NEVER want fallbacks anywhere in this project."`):** matching uses one structured store record, never log-file scraping fallbacks or English-prose heuristics.
- **No count-based routing, and held items release their lanes (`D-JL10` and `D-JL13` in [`tillr-judge-and-lifecycle.md`](./tillr-judge-and-lifecycle.md#decision-ledger), 2026-11-17 and 2026-11-19):** acquittal pins the kept candidate commit, releases the lane in the same pass, and re-gates that kept candidate once the trunk fixer clears.
- **Provenance separation ([`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger), 2026-11-19):** engineering detail within settled rulings is marked explicitly as the designer's so the owner may review or overturn it.

### 2.2 Check against Landing Principles

| Principle in [`landing-principles.md`](./landing-principles.md) | How this design satisfies it |
| :--- | :--- |
| **Opening rule: cost must not rise with trunk activity** | Once one gate or proof files `fixer-main-<sha>`, N concurrent candidates hitting that same shard-dependent case are acquitted from SQLite state in milliseconds instead of paying N base replays or N wasted worker retries + 27-minute gates. |
| **[Section 3: Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once)** and **[Section 5: Only verdicts charge](./landing-principles.md#5-only-verdicts-charge)** | Untouched candidates keep their finished commit pinned and wait on `fixerID` (`RefusalTrunkAlreadyRed`) rather than burning new worker attempts on a trunk failure they did not cause. |
| **[Section 12: A principle cites the measurement that produced it](./landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it)** | Grounded in the 2026-11-24 `TestNonLandingLoopStartsWithoutGit` incident (`09:46`–`10:12:01 ET`, two 27-minute re-gates). |
| **[Section 13: A held state names a mover that can still act](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)** | Held candidates wait on `item:fixer-main-<sha>`, an active rank-`000` build item whose landing, green proof (`resolveProvedFixers`), or close releases every waiter via `repairedTrunkWaiter`. |
| **[Section 14: Convict a run on what it did, not on what happened while it ran](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)** | A candidate is convicted with `RefusalGate` on a fixer-matched case only when its own `base_sha..head_sha` diff touches that failing test (`D-FCM3`). |
| **[Section 15: Land the smallest functional change](./landing-principles.md#15-land-the-smallest-functional-change)** | Ships as one self-contained drain change ([Section 7](#7-follow-on-build-item)) while keeping test-binary, runner shard-splitting, and proof/gate shard alignment out of scope ([Section 6.1](#61-out-of-scope-gaps-and-candidate-note)). |

---

## 3. The recorded fact: structured failing cases on open trunk fixers (`D-FCM1`)

### 3.1 Persisting `failing_cases` in `drain.fixer-logs` (`D-FCM1`)

By the designer's decision ([`D-FCM1`](#D-FCM1), which the owner may overturn), the recorded fact naming an open trunk fixer's failing cases is a structured `failing_cases` array written into the `drain.fixer-logs` (`FixerLogsKind`, `object_type = "item"`, `object_id = fixerID`) event payload by `routeTrunkFixer` in [`trunk.go`](../../tools/tillr-drain/drain/trunk.go), paired with any `go-test:<module>:<package>:<TestName>` check selectors already present in `item_criteria` (`criterion_id = 'trunk-green'`).

Each element of `failing_cases` has type `FixerFailingCase`:

```go
type FixerFailingCase struct {
	CheckID   string `json:"check_id"`
	Package   string `json:"package,omitempty"`
	Case      string `json:"case"`
	Where     string `json:"where,omitempty"`
	Assertion string `json:"assertion,omitempty"`
}
```

Every trunk fixer in the drain — whether filed from a candidate gate replay (`settleTrunkAlreadyRed` -> `acquitRedWithPaths`), a worker `OBSTACLE: trunk` replay (`routeObstacle`), an after-merge trunk proof (`heavy_loop.go` -> `fileTrunkFixerNoted`), or a rolling run (`maybeRolling` -> `fileTrunkFixer`) — passes through `routeTrunkFixer`. When `routeTrunkFixer` creates a new `fixer-main-<sha>` or attaches failing checks to an existing open `fixer-main-*` (`attached != ""`), it extracts `failing_cases` across `failing` as follows:

1. **`go-test:<module>:<package>:<TestName>` selectors (`heavy_go.go`):** parse `<package>` and `<TestName>` directly from the check ID and emit `FixerFailingCase{CheckID: id, Package: pkg, Case: name}`, enriching `Where` and `Assertion` when `gate.CheckLogs[id]` carries a triage block.
2. **Trunk-confirmed check logs (`gate.CheckLogs[id]`):** parse `gate.CheckLogs[id]` with `readTriageBlock` and `readGoTestFailures` ([`refusal_cases.go`](../../tools/tillr-drain/drain/refusal_cases.go)). Keep only concrete named test cases (`Case != ""` and `isTestName(Case)`), extracting any Go import path from `FailingCase.Where` (`"<import_path> (<file>.go)"` -> `Package = "<import_path>"`) and omitting bare package or build failures (`Test == ""`).
3. **Preserving trunk soundness when enriching from `candidate.replayCheckLogs` (`1295435f`):** in `settleTrunkAlreadyRed`, `preferReplayCheckLogs` replaces `gate.CheckLogs[id]` with the base/tip replay log (`go test -json -count=1 -run ...` from [`replay_go_affected.go`](../../tools/tillr-drain/drain/replay_go_affected.go)) while `candidate.replayCheckLogs[id]` holds the candidate gate's `fork-check: triage --` log. Because a candidate could fail on both a trunk-broken test **and** a candidate-broken test that passed on base/tip replay, `gate.CheckLogs[id]` (when non-empty) is authoritative for **which** `(Package, Case)` pairs failed on the trunk; `candidate.replayCheckLogs[id]` is used only to populate missing `Where` and `Assertion` fields on those trunk-confirmed pairs (and is read for case names only when no replay log exists for `id`).
4. **Event persistence:** whenever `len(checkLogs) > 0 || len(failingCases) > 0`, `routeTrunkFixer` appends `drain.fixer-logs` (`FixerLogsKind`, `object_type = "item"`, `object_id = fixerID`) with `{"fixer_item": fixerID, "red_sha": redSHA, "check_logs": checkLogs, "failing_cases": failingCases}` inside the same `WriteTx` that creates or updates `fixerID`.

### 3.2 Reading open trunk fixers and matching a candidate gate failure (`D-FCM1`)

At candidate gate settlement time, reading open trunk fixers touches **only SQLite** — zero `.fixers/` filesystem log re-parsing and zero prose word matching (`fixIntentWords`):

1. **Open trunk fixer inventory:** query `items` joined with `item_criteria` (`criterion_id = 'trunk-green'`) for `item_id LIKE 'fixer-main-%'` in queue order (`ORDER BY i.rank, i.item_id`), keeping items that are neither finished nor closed (`!finishedState(declared.Parsed, state) && !closedState(declared.Parsed, state)`). For each open `fixerID`, load:
   - all `failing_cases` (and the latest `red_sha`, defaulting to `TrunkFixerSHA(fixerID)`) from `events WHERE kind = 'drain.fixer-logs' AND object_type = 'item' AND object_id = fixerID ORDER BY seq ASC`; plus
   - any `go-test:<module>:<package>:<TestName>` selectors in `fixerChecks(trunkGreenText)` not already present in `failing_cases`.
2. **Candidate failing-case extraction:** for **every** check `id` in `gate.FailingChecks`, read `gate.CheckLogs[id]` (supplemented by `gateFailingChecks(RunDir(root, candidate.RunID), candidate.RunID)` when `gate.CheckLogs[id]` is empty, matching `singleFailingTestNames` in [`test_fix_hold.go`](../../tools/tillr-drain/drain/test_fix_hold.go)) via `readTriageBlock` and `readGoTestFailures`:
   - If any check in `gate.FailingChecks` has no log, a truncated triage section (`"more failing case(s) not listed"`), a build or setup failure (`goBuildFailRe`), a package-only failure (`Test == ""`), or zero concrete named test cases, extraction returns `ok = false` and **no** case match holds.
3. **All-cases match predicate against a single open `fixerID`:** open trunk fixer `fixerID` matches `candidate`'s gate failure if and only if:
   - **No self-match, own-repair match, or wait cycle:** `candidate.ItemID != fixerID`, `!wouldCreateWaitCycle(r, candidate.ItemID, fixerID)` (and `!fixerWaitReaches(r, fixerID, candidate.ItemID)`), and if `IsTrunkFixer(candidate.ItemID)`, none of `gate.FailingChecks` or the candidate's failing cases overlaps `candidate.ItemID`'s own `fixerNamedChecks` or recorded `failing_cases` (a trunk fixer running its own gate never acquits itself on the failure it exists to fix); and
   - **Every candidate failing case `c` matches at least one recorded `FixerFailingCase` `f` on `fixerID`:**
     1. **Case identity:** `c.Case == f.Case`, or `testRootName(c.Case) == testRootName(f.Case)` when one is a subtest (`TestFoo/sub`) of the other (`TestFoo`);
     2. **Package / file scope:** when both `c.Package` and `f.Package` are non-empty, `c.Package == f.Package`; when either `Package` is empty and both `c.Where` and `f.Where` name a repository-relative file path, those normalized file paths match; and
     3. **Check scope:** either `fixerCheckOverlaps(f.CheckID, c.CheckID, candidate.Worktree)` holds, **or** both `c.CheckID` and `f.CheckID` are Go test checks (`go-affected/affected-tillr-go-test-packages-pass`, `go-tool/*`, `go-substrate/*`, `gate-runner/*`, or `go-test:*`) with `c.Package != "" && c.Package == f.Package` (so a proof-filed `go-test:` or `go-tool/` fixer and a gate-filed `go-affected` failure for the same Go package and test match each other).

---

## 4. Judging an untouched candidate whose failing cases match an open trunk fixer (`D-FCM2`)

### 4.1 Where in `settleTrunkAlreadyRed` the match runs (`D-FCM2`)

By the designer's decision ([`D-FCM2`](#D-FCM2), which the owner may overturn), in `settleTrunkAlreadyRed` ([`trunk.go`](../../tools/tillr-drain/drain/trunk.go)):

1. `reading, err := l.trunkAlreadyRed(ctx, *candidate, gate)` runs first as today:
   - When `reading.Confirmed` is `true`, both base and tip replayed red; the existing `settleTrunkAlreadyRed` path runs ([`OQ-GP7`](./tillr-graph-is-the-path.md#decision-ledger) `pass-inherited` or `acquitRedWithPaths`, which now writes `failing_cases` on `fixerID` via `D-FCM1`).
   - When `!reading.Confirmed && reading.RepairedAt != ""` **and** `!strings.HasSuffix(reading.Evidence, "tip proof record")`, the check failed base replay and genuinely passed a **replay** on `tip`; the trunk is repaired at `tip`, so `settleTrunkAlreadyRed` keeps its existing `repairedAtTipDetail` rebase-and-return behavior.
2. Whenever `!reading.Confirmed` and either `reading.RepairedAt == ""` (covering `"base proof record"` on 2026-11-24 as well as shard-order-sensitive single-case `"base replay"`) **or** `strings.HasSuffix(reading.Evidence, "tip proof record")` (where a six-way `green-<tip>.json` suppressed tip replay while `fixerID` is still open), `settleTrunkAlreadyRed` runs the open `fixer-main-*` failing-case match (`D-FCM1`) **before** `repairedAtTipDetail`, **before** `holdForOpenTestFix`, and **before** returning `("", false, nil)` (`RefusalGate`).
3. To keep the two hold mechanisms cleanly separated, `findOpenTestFixer` in [`test_fix_hold.go`](../../tools/tillr-drain/drain/test_fix_hold.go) skips `IsTrunkFixer(row.id)`: `fixer-main-*` items always settle through `D-FCM1`–`D-FCM3`, whereas `holdForOpenTestFix` remains strictly for ordinary queue items filed to fix a named test (`test-fix-open: ...`, `candidate.trunkFixer == nil`).

### 4.2 Applying the red-trunk `reads` rule and holding the kept candidate on `fixerID` (`D-FCM2`)

When open trunk fixer `fixerID` (with recorded `fixerRedSHA`) matches every failing case of `candidate`'s gate failure (`D-FCM1`) and `candidate`'s diff touches **none** of the matched failing tests (`D-FCM3`):

- **No redundant replay, bisection, revert, or fixer mutation:** the drain does not run `culpritLandingAttribution` or `revertFromGateReplay`, does not file a second fixer, and does not alter `fixerID`'s `trunk-green` selector or fence.
- **[`OQ-GP7`](./tillr-graph-is-the-path.md#decision-ledger) `reads` check (`intersectingRedPaths` in [`trunk.go`](../../tools/tillr-drain/drain/trunk.go)):** compute `changedPaths` (`gate.CandidateChangedPaths`, `gate.SuspectPaths`, or `ps.changedPaths(*candidate)`) and `intersecting := intersectingRedPaths(gate.FailingChecks, checkReads, changedPaths)`:
  1. **Disjoint paths (`len(intersecting) == 0 && len(changedPaths) > 0`):** call `promoteVerdictPassInherited(ctx, *candidate, fixerRedSHA, gate.FailingChecks)` and return `(GatePassedInherited, false, nil)` so the candidate lands immediately with `inherited_failures` recorded.
  2. **Intersecting paths (`len(intersecting) > 0 || len(changedPaths) == 0`, as on 2026-11-24 when both candidates modified `tools/tillr-drain/`):** acquit and hold the kept candidate behind `fixerID` identically to `acquitRedWithPaths`:
     - call `recordTrunkRefusal(ctx, *candidate, fixerRedSHA, gate.FailingChecks, fixerID)` to append `drain.refusal` (`step: "trunk-already-red"`, `fixer_item: fixerID`, `red_sha: fixerRedSHA`, `failing_checks`);
     - call `Steps.ItemState(ctx, candidate.ItemID, candidate.RunID, StateBlocked, item.WaitingOn{Waiter: "item:" + fixerID, Reason: reason})` where `reason` is `"trunk-already-red: <checks> (fixer <fixerID>; cases: <matched_cases>; paths: <intersecting>)"`;
     - increment `result.TrunkAcquitted`, set `candidate.trunkAcquitted = true`, and set `candidate.trunkFixer = &trunkFixerHold{fixerID: fixerID, redSHA: fixerRedSHA, failingChecks: append([]string(nil), gate.FailingChecks...)}`, returning `("", true, nil)`.
- **Post-verdict epoch, lane release, and automatic re-gate:** in `Loop.verify` ([`loop.go`](../../tools/tillr-drain/drain/loop.go)), `gate.finishJudgment()` records the initial `gate.verdict`, `recordKeptCandidateFixerAttached` ([`events.go`](../../tools/tillr-drain/drain/events.go)) appends run-scoped `drain.fixer-attached` (`kept_candidate: true`) after `gate.verdict` (advancing `gateJudgmentEpoch` and arming [`drain-fixer-held-new-attempt.toml`](../../config/tillr/watch/drain-fixer-held-new-attempt.toml)), and the loop pins the candidate commit and releases its lane (`D-JL13`). When `fixerID` lands and is proved past `refs/tillr/green`, is resolved by `resolveProvedFixers`, or is closed, `releaseTrunkAcquittals` -> `repairedTrunkWaiter` ([`trunk_release.go`](../../tools/tillr-drain/drain/trunk_release.go)) unblocks the item and `preRebaseRepairedTrunk` ([`trunk_repair.go`](../../tools/tillr-drain/drain/trunk_repair.go)) rebases and re-gates the kept candidate with zero extra worker runs.

---

## 5. When the candidate's change also touches the matched failing test (`D-FCM3`)

### 5.1 Mechanical predicate: when `base_sha..head_sha` touches a failing test (`D-FCM3`)

By the designer's decision ([`D-FCM3`](#D-FCM3), which the owner may overturn), whether `candidate` touches a matched failing test case `c` is decided mechanically from the `base_sha..head_sha` diff (`--no-renames`) in `candidate.Worktree` (or `l.cfg.Root` when `BaseSHA` and `HeadSHA` are in the repository object store), with zero heuristics:

1. **Go test case (`c.Package != ""` and `goTestName.MatchString(c.Case)`):**
   - Let `rootTest := testRootName(c.Case)` (stripping any `/subtest` suffix) and resolve `c.Package` to its repository-relative directory `<pkgDir>` via module `go.mod` paths (`indexReplayGoModules` / `resolveReplayGoPackage` in [`replay_go_affected.go`](../../tools/tillr-drain/drain/replay_go_affected.go)).
   - Inspect only changed `*_test.go` paths directly inside `<pkgDir>` (`path.Dir(p) == pkgDir && strings.HasSuffix(p, "_test.go")`). Editing production `.go` files, files in other packages, or `*_test.go` files in `<pkgDir>` that do not declare `func <rootTest>` in either `base_sha` or `head_sha` (unless unparseable) does **not** touch `c`.
   - For each changed `<pkgDir>/*_test.go` path, parse the `base_sha` blob (when present) and `head_sha` blob (when present) with Go's standard-library `go/parser` and `go/token` (`parser.ParseFile` with `token.NewFileSet`) and read zero-context diff hunks (`git diff --unified=0 --no-renames base_sha..head_sha -- <file>`):
     - If either existing blob fails Go syntax parsing, treat `<rootTest>` as touched (conservative on unparseable test code).
     - Locate top-level `ast.FuncDecl` (`Recv == nil`) with `Name.Name == rootTest` in `base_sha` (`[baseStart, baseEnd]`) and in `head_sha` (`[headStart, headEnd]`).
     - `<rootTest>` is **touched** if it exists in only one of `{base_sha, head_sha}` (added or deleted), if any deleted hunk range (`oldCount > 0`: `[oldStart, oldStart+oldCount-1]`; or insertion point `oldStart` when `oldCount == 0` falls strictly inside `[baseStart, baseEnd]`) overlaps `[baseStart, baseEnd]`, or if any added hunk range (`newCount > 0`: `[newStart, newStart+newCount-1]`; or deletion point `newStart` when `newCount == 0` falls strictly inside `[headStart, headEnd]`) overlaps `[headStart, headEnd]`.
     - Adding, deleting, or editing **other** `func Test*` functions or helper functions outside `func <rootTest>` in the same `<pkgDir>/*_test.go` file (as in `02160d92`, which added tests and helpers in `tools/tillr-drain/drain/replay_go_affected_test.go`) does **not** touch `func <rootTest>`.

2. **Non-Go test case:**
   - Extract the repository-relative test file path from `c.Where` (when recorded). If `c.Where` names a repository-relative file path, `candidate` touches `c` when `changedPaths` contains that path. If `c.Where` names no file path, `candidate` touches `c` when `changedPaths` contains any test file watched by `c.CheckID` (`*_test.*`, `*.test.*`, `*.spec.*`, or `test_*`).

### 5.2 Disposition when the candidate touches a matched failing test (`D-FCM3`)

When **any** failing case `c` of the candidate gate is touched by `candidate`'s `base_sha..head_sha` diff:

1. **No acquittal and no hold on `fixerID`:** `settleTrunkAlreadyRed` logs that `candidate.ItemID` failed on `<case>` (named by open `fixerID`) **and** modified `<case>` in `<file>`, skips both `D-FCM2` and `holdForOpenTestFix` (which likewise must not hold a candidate behind another item when the candidate itself edited the failing test), and returns `("", false, nil)`.
2. **Ordinary `RefusalGate` (`step: "gate"`):** `Loop.verify` records `drain.refusal` with `step: "gate"` (`RefusalGate`) and returns the candidate to its worker with the gate's failure summary (`failureSummary` in [`refusal_cases.go`](../../tools/tillr-drain/drain/refusal_cases.go)).
3. **Why `RefusalGate` is required here:**
   - `base_sha` contains only the **unedited** version of `func <rootTest>`, which already fails on the gate's shard split, so neither `green-<base>.json` nor a base replay can verify the candidate's new edits inside `func <rootTest>` or rule out a newly introduced bug in that test function.
   - Holding the candidate behind `fixerID` would park an unjudged modification to the exact function `fixerID` is repairing, guaranteeing a rebase conflict or semantic collision when `fixerID` lands.
   - Returning `RefusalGate` lets the worker drop an unintended edit to `func <rootTest>` or repair its own test change; if the item's scope genuinely requires modifying `func <rootTest>` and is blocked by the pre-existing trunk failure, the worker can report `OBSTACLE: trunk` (routed via [`obstacle_route.go`](../../tools/tillr-drain/drain/obstacle_route.go) or [`obstacle_hold.go`](../../tools/tillr-drain/drain/obstacle_hold.go)).

---

## 6. Negative space, out-of-scope candidate note, and edge cases

### 6.1 Out-of-scope gaps and candidate note

Per the request's scope boundary, three related gaps are **out of scope** for this design and its follow-on build item (listed here explicitly as the candidate note so none is mistaken for an omitted requirement):

1. **Fixing `TestNonLandingLoopStartsWithoutGit` itself:** that is the trunk fixer's job (and on trunk at `972a5fbc887f`, commit `463da393` already moved the shared `tillr-drain` test binary build into `cli_helpers_build_test.go` using the initial process environment).
2. **How the test runner splits packages into shards:** `tools/go-test-shards.py` and `tools/gate/go_affected.py` shard assignment and test ordering are unchanged here.
3. **Making the after-merge trunk proof and the candidate gate use the same shard split:** `go-tool/tools-tillr-drain-s-whole-suite-passes` and `go-affected/affected-tillr-go-test-packages-pass` remain distinct checks with their existing shard counts and scopes; any future alignment belongs in its own item.

In addition, this item builds no Go code or pack mirror changes ("Do not build it in this item") and adds, edits, or relies on no `tillr: item` directive comments.

### 6.2 Edge cases

| Case | Behavior under `D-FCM1`–`D-FCM3` |
| :--- | :--- |
| Candidate fails on two cases: `TestTrunkBroken` (named on open `fixerID`) **and** `TestCandidateBroken` (not named on `fixerID`) | Not all candidate failing cases match `fixerID` (`D-FCM1`); no acquittal holds, and `RefusalGate` returns both failures to the candidate's worker. |
| First candidate fails on `TestTrunkBroken` + `TestCandidateBroken`; `replay_go_affected.go` replays both on `base`/`tip`, where only `TestTrunkBroken` fails (`1295435f`) | `gate.CheckLogs[id]` holds the replay log in which only `TestTrunkBroken` failed; `routeTrunkFixer` records only `TestTrunkBroken` in `failing_cases` (enriching `Where`/`Assertion` from `candidate.replayCheckLogs[id]`) and never pollutes `fixerID` with `TestCandidateBroken` (`D-FCM1`). |
| Candidate adds `func TestNewFeature` and a local helper in `tools/tillr-drain/drain/cli_helpers_test.go` without touching `func TestNonLandingLoopStartsWithoutGit` | `go/parser` AST span check sees `func TestNonLandingLoopStartsWithoutGit` unchanged across `base_sha..head_sha` (`D-FCM3` = untouched), so `D-FCM2` acquits and holds the candidate behind `fixerID`. |
| Candidate edits `func TestNonLandingLoopStartsWithoutGit` (or introduces a syntax error in `cli_helpers_test.go`) and fails on `TestNonLandingLoopStartsWithoutGit` | `D-FCM3` marks the case touched (in both `D-FCM2` and `holdForOpenTestFix`) and records `RefusalGate` so the candidate returns to its worker. |
| Open `fixerID` itself fails its own candidate gate on `TestNonLandingLoopStartsWithoutGit` | `candidate.ItemID == fixerID` excludes self-match (`D-FCM1`); `fixerID` receives `RefusalGate` because its repair did not make the failing case pass. |
| Open `fixerID` was already open before `D-FCM1` shipped (legacy `drain.fixer-logs` event with `check_logs` only and no `failing_cases`, and no `go-test:` selector in `trunk-green`) | Under `R-TW-DR2` (no fallbacks), the matcher does not scrape `.fixers/` files on disk at match time; legacy fixers without `failing_cases` or `go-test:` selectors age out once closed or landed, and every newly created or attached fixer writes `failing_cases`. |

---

## 7. Follow-on build item

This design finishes with no `tillr: item` directive block and specifies **one** follow-on build item implementing [`D-FCM1`](#D-FCM1), [`D-FCM2`](#D-FCM2), and [`D-FCM3`](#D-FCM3):

1. **Build item: Acquit untouched candidates whose gate failure cases match an open trunk fixer on a green-proved base.**
   - **Where:** [`tools/tillr-drain/drain/trunk.go`](../../tools/tillr-drain/drain/trunk.go), [`tools/tillr-drain/drain/refusal_cases.go`](../../tools/tillr-drain/drain/refusal_cases.go), [`tools/tillr-drain/drain/test_fix_hold.go`](../../tools/tillr-drain/drain/test_fix_hold.go), a focused helper file in `tools/tillr-drain/drain/` (for example `fixer_cases.go` and `fixer_cases_test.go`), plus `just pack-sync` updating `yolo-packs/tillr/files/local/share/tillr/tillr-drain/`.
   - **Testable criteria:**
     1. **Recording `failing_cases` on trunk fixers (`D-FCM1`):** When `routeTrunkFixer` creates a new `fixer-main-<sha>` or attaches checks to an open `fixer-main-*`, it extracts trunk-confirmed concrete failing test cases (`{check_id, package, case, where, assertion}`) from `gate.CheckLogs` (enriching `where` and `assertion` from `candidate.replayCheckLogs` only for cases that failed in `gate.CheckLogs` when a replay log is present) and `go-test:<module>:<package>:<TestName>` selectors, and persists `failing_cases` in `drain.fixer-logs` (`FixerLogsKind`); candidate-only cases that passed in the base/tip replay log are excluded.
     2. **Acquitting untouched candidates on a green-proved base (`D-FCM1`, `D-FCM2`):** When a non-fixer candidate's gate fails on a base covered by `green-<base>.json` (or when `trunkAlreadyRed` leaves `!reading.Confirmed` under `"base proof record"`, single-case `"base replay"`, or `"tip proof record"`), every failing check resolves solely to concrete named cases matching an open `fixer-main-*` (from `drain.fixer-logs` `failing_cases` or `trunk-green` `go-test:` selectors), and the candidate touches none of those failing tests:
        - if `CandidateChangedPaths` is non-empty and disjoint from the failing checks' declared `reads`, `settleTrunkAlreadyRed` promotes `gate.verdict.json` to `pass-inherited` (`GatePassedInherited`) so the candidate lands;
        - if `CandidateChangedPaths` intersects `reads`, `settleTrunkAlreadyRed` records `drain.refusal` with `step: "trunk-already-red"` (`RefusalTrunkAlreadyRed`), blocks the item on `item:<fixerID>` naming `<fixerID>`, matched cases, and intersecting paths, sets `candidate.trunkAcquitted = true` and `candidate.trunkFixer`, emits run-scoped `drain.fixer-attached` (`kept_candidate: true`) after `gate.finishJudgment()` (advancing `gateJudgmentEpoch`), pins the candidate and releases its lane without starting a new worker attempt, and re-gates the kept candidate once `<fixerID>` clears.
     3. **Refusing candidates that touch a matched failing test (`D-FCM3`):** When `base_sha..head_sha` adds, deletes, or overlaps `func <rootTestName>` in `<pkgDir>/*_test.go` via `go/parser` AST spans (or fails Go parsing on a changed `<pkgDir>/*_test.go`, or modifies a non-Go case's test file), neither open `fixer-main-*` matching nor `holdForOpenTestFix` acquits the candidate; `Loop.verify` records `RefusalGate` (`step: "gate"`), whereas editing other `func Test*` functions, helpers outside `func <rootTestName>`, or production `.go` files in `<pkgDir>` still acquits under `D-FCM2`.
     4. **Clean boundary with `holdForOpenTestFix` and self-gating fixers:** `findOpenTestFixer` skips `fixer-main-*` items so trunk fixers always use the `trunk-already-red` / [`OQ-GP7`](./tillr-graph-is-the-path.md#decision-ledger) path; an open `fixer-main-*` failing its own gate never matches itself or its own named repair.
     5. **Verification and pack sync:** Targeted tests in `tools/tillr-drain/drain` pass, `just format` and `just pack-sync` leave `yolo-packs/tillr/` in sync with `tools/tillr-drain/`, and any touched Markdown passes `uvx vantage-check`.

---

## Open Questions

None open for the owner; [`D-FCM1`](#D-FCM1), [`D-FCM2`](#D-FCM2), and [`D-FCM3`](#D-FCM3) below are designer decisions on engineering detail under [`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger) that the owner may review or overturn.

---

## Decision Ledger

Governing owner rulings ([`OQ-GP7`](./tillr-graph-is-the-path.md#decision-ledger), [`R-TW-DR2`](./tillr-watch/decision-record-files.md#R-TW-DR2), [`D-JL10`](./tillr-judge-and-lifecycle.md#decision-ledger), [`D-JL13`](./tillr-judge-and-lifecycle.md#decision-ledger), and [`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger)) remain in their home ledgers and are cited in [Section 2.1](#21-settled-owner-rulings-that-constrain-this-design). Every row below is a designer's decision made on a filed operator request; none is an owner ruling, and the owner may overturn any of them.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-FCM1"></a>`D-FCM1` | **Designer's decision from a filed operator request; the owner may overturn this.** Record an open trunk fixer's failing test cases as structured `failing_cases` (`[]FixerFailingCase` with `{check_id, package, case, where, assertion}`) inside the `drain.fixer-logs` (`FixerLogsKind`, `object_type = "item"`, `object_id = fixerID`) event payload whenever `routeTrunkFixer` ([`trunk.go`](../../tools/tillr-drain/drain/trunk.go)) creates a `fixer-main-<sha>` or attaches to an open `fixer-main-*`, extracting trunk-confirmed cases from `gate.CheckLogs` via `readTriageBlock` and `readGoTestFailures` (using `candidate.replayCheckLogs` only to enrich `where` and `assertion` on cases that failed in `gate.CheckLogs`, or when no replay log exists) plus `go-test:<module>:<package>:<TestName>` selectors; at candidate gate settlement, read open `fixer-main-*` items' `drain.fixer-logs` `failing_cases` and `trunk-green` `go-test:` selectors from SQLite and match when every `gate.FailingChecks` entry resolves solely to concrete named test cases matching one open `fixer-main-*` by case (`c.Case == f.Case` or `testRootName` parent/subtest match), package/file scope, and check scope (`fixerCheckOverlaps` or Go test check on the same non-empty `package`), with no self-match, own-repair match, or wait cycle. **Rejected:** re-parsing `.fixers/` filesystem logs or matching English prose (`fixIntentWords` in `test_fix_hold.go`) at gate settlement time (fragile and violates `R-TW-DR2`); matching on bare check ID alone without concrete case identity (would acquit unrelated new test failures in `go-affected`). | 2026-11-25, designer | [Section 3.1](#31-persisting-failing_cases-in-drainfixer-logs-d-fcm1), [Section 3.2](#32-reading-open-trunk-fixers-and-matching-a-candidate-gate-failure-d-fcm1) | No ([Section 7](#7-follow-on-build-item)) |
| <a id="D-FCM2"></a>`D-FCM2` | **Designer's decision from a filed operator request; the owner may overturn this.** In `settleTrunkAlreadyRed` ([`trunk.go`](../../tools/tillr-drain/drain/trunk.go)), whenever `!reading.Confirmed` and no genuine tip replay proved repair (`reading.RepairedAt == ""` or `strings.HasSuffix(reading.Evidence, "tip proof record")`), evaluate `D-FCM1` against open `fixer-main-*` items ahead of `holdForOpenTestFix` (and skip `IsTrunkFixer` in `findOpenTestFixer`): when an open `fixerID` matches every failing case and the candidate touches none of those failing tests (`D-FCM3`), skip culprit bisection, auto-revert, and second-fixer filing, and apply [`OQ-GP7`](./tillr-graph-is-the-path.md#decision-ledger) against `fixerID` — promoting disjoint `CandidateChangedPaths` to `GatePassedInherited` (`promoteVerdictPassInherited`) and holding intersecting candidates via `recordTrunkRefusal` (`RefusalTrunkAlreadyRed`), `WaitingOn{Waiter: "item:" + fixerID}` naming `fixerID`, matched cases, and intersecting paths, `candidate.trunkAcquitted = true`, and `candidate.trunkFixer` so `Loop.verify` records kept-candidate `drain.fixer-attached` (`kept_candidate: true`) after `gate.finishJudgment()`, releases the lane, and re-gates the kept candidate via `repairedTrunkWaiter` / `preRebaseRepairedTrunk` once `fixerID` clears. **Rejected:** forcing a base replay whenever `green-<base>.json` exists (replays on a single test can still miss shard-split interactions and pay replay cost per candidate); routing `fixer-main-*` through `holdForOpenTestFix` (`test-fix-open:` skips [`OQ-GP7`](./tillr-graph-is-the-path.md#decision-ledger) `pass-inherited`, omits `RefusalTrunkAlreadyRed` and `drain.fixer-attached` so `gateJudgmentEpoch` and `drain-fixer-held-new-attempt` break, and ignores `resolveProvedFixers`). | 2026-11-25, designer | [Section 4.1](#41-where-in-settletrunkalreadyred-the-match-runs-d-fcm2), [Section 4.2](#42-applying-the-red-trunk-reads-rule-and-holding-the-kept-candidate-on-fixerid-d-fcm2) | No ([Section 7](#7-follow-on-build-item)) |
| <a id="D-FCM3"></a>`D-FCM3` | **Designer's decision from a filed operator request; the owner may overturn this.** Decide mechanically on `base_sha..head_sha` (`--no-renames`) whether a candidate touches a matched failing test: for Go test cases, parse `base_sha` and `head_sha` blobs of changed `<pkgDir>/*_test.go` files with `go/parser` and mark `func <testRootName(case)>` touched when added, deleted, overlapped by any `git diff --unified=0` hunk line range, or in a changed `<pkgDir>/*_test.go` file that fails Go syntax parsing (leaving edits to production `.go` files, other `*_test.go` files, other `func Test*` declarations, and helpers outside `func <testRootName(case)>` untouched); for non-Go test cases, mark touched when `CandidateChangedPaths` includes the test file named in `Where` (or test files watched by that check when `Where` names no file). When any matched failing test is touched, neither `D-FCM2` nor `holdForOpenTestFix` acquits the candidate; `Loop.verify` records ordinary `RefusalGate` (`step: "gate"`) and returns the candidate to its worker. **Rejected:** acquitting or holding a candidate that modified the failing test itself (clears or parks an unverified test edit and collides with `fixerID`); treating any edit anywhere in a Go `*_test.go` file or package as touching `<testRootName(case)>` (falsely convicts unrelated new tests in shared `*_test.go` files). | 2026-11-25, designer | [Section 5.1](#51-mechanical-predicate-when-base_shahead_sha-touches-a-failing-test-d-fcm3), [Section 5.2](#52-disposition-when-the-candidate-touches-a-matched-failing-test-d-fcm3) | No ([Section 7](#7-follow-on-build-item)) |
