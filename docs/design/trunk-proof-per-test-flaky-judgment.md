---
title: "The trunk proof disables each failed test at once, lists it in the repository, and sends it to the mechanic"
status: accepted
stage: DECIDED
stage-since: 2026-11-28
updated: 2026-11-28
tags:
  - tillr
  - gate
  - drain
  - proof
  - flake
  - testing
  - design
summary: "Records the owner's 2026-11-28 rulings: leaf subtest granularity (OQ-TPF2, option A); a repair lands when the named test passes once, and a test that fails again goes round the same loop (OQ-TPF3); and the direction that settles OQ-TPF1 — any test that fails in the trunk proof is disabled at once with no rerun, the rest of the suite decides the proof, the failure goes to one general fixer (the mechanic), the commit counts as green everywhere, the disabled list is a tracked file that hosted CI also honors, tillr item sync queues a repair for every listed test that has none, and an optional setting (off by default) holds the push until no test is disabled. Settles the remaining points as designer's decisions D-TPF6 to D-TPF11, withdraws OQ-TPF4, restates the build slices, and names the landing principles these rulings change."
next: "No question is open for the owner. Slices 1 and 3 are on the trunk; Slices 2, 4 and 7 may each build on Slice 1 without waiting on refs/tillr/green or on Slice 5; Slice 5 hooks mechanic-job filing ahead of Slice 4's disabling commit, Slice 6 follows Slice 5, Slice 8 follows Slices 4–6, and Slice 9 amends the reference and the landing principles once Slices 1–8 are built."
depends-on:
  - ../reference/gate.md
  - ../reference/gate.md#D-GR5
  - ../reference/gate.md#D-GR6
  - ../reference/gate.md#D-GR15
  - ../reference/gate.md#D-GR16
  - ../reference/gate.md#D-GR17
  - ../reference/gate.md#D-GR21
  - ../reference/gate.md#D-GR22
  - ../reference/gate.md#D-GR23
  - ../reference/gate.md#D-GR31
  - ../reference/gate.md#D-GR32
  - ../reference/gate.md#D-GR53
  - ../reference/gate.md#D-GR56
  - ../reference/gate-check-rules.md
  - landing-principles.md
  - gate-test-caching.md
  - trunk-repair-on-moving-trunk.md
  - tillr-mechanic-escalation.md
  - test-runner-adapters.md
  - tillr-judge-and-lifecycle.md
  - ../tillr/decision-provenance-review.md
  - ../reference/authority-not-approval.md
vantage:
  status-chip: true
---

# The trunk proof disables each failed test at once, lists it in the repository, and sends it to the mechanic

**Status:** Decided on 2026-11-28 (`status: accepted`, `stage: DECIDED`). The owner ruled [`OQ-TPF2`](#OQ-TPF2) (option A: leaf subtest granularity) and [`OQ-TPF3`](#OQ-TPF3) (a repair lands when the named test passes once; a test that fails again goes round the same loop), and then gave the direction that settles [`OQ-TPF1`](#OQ-TPF1): the proof does not try to tell a flaky test from a broken one. Every quoted owner statement is in [Section 1.1](#11-owner-direction-and-the-2026-11-28-rulings) and [the Decision Ledger](#decision-ledger). **Slices 1 and 3 of [Section 8](#8-build-slices) are built; Slices 2 and 4 to 9 are not** (`rg -n 'prove-disabled' tools/tillr-drain` still returns nothing, and `disabledPath` in [`tools/tillr-gate/enable.go`](../../tools/tillr-gate/enable.go) still keys markers only on whole declaration IDs in the store). Slice 1 was measured on 2026-11-28 by `go test ./shards -run 'SkipPattern|PlanDrops|FailedTests|ShardsExclude|ProveDisabled|MissingDisabled|MalformedDisabled'` in `tools/tillr-gate` (it runs real fixture modules through the uncached and `--cache` runners, and shows listed tests and subtests never start, the summary counts them as `disabled`, and `--prove-disabled` runs one anyway), `go test ./disabledtests ./settings -run 'Gate|Parse|Load'` in `tools/tillr` (the schema and the `[gate] disabled_tests` key), and `go test . -run 'DisabledTests|EveryGoTest'` in `tools/gate` (the tree check `disabled-tests` and the declared reads of every `go-test-*` check). Two differences from this design's text are the tree's facts: the key, the schema and its validator live in the new `tools/tillr/disabledtests` package and `tools/tillr/settings`, not in `tools/tillr-gate/` or `tools/gate/`, because the `[gate]` settings are parsed there and both modules depend only on `tools/tillr`; and Go emits no `TestEvent` at all for a subtest that `-skip` excludes, so the `disabled` count in the summary is the number of listed selectors whose top-level test exists in the package, not a count of `skip` events. Leaf attribution and per-test output slicing are built as `FailedTests` in `tools/tillr-gate/shards`; nothing calls it for the mechanic job until Slices 4 and 5. Slice 3 was measured on 2026-11-28 by `go test . -run 'TestDisabledArgs'` in `tools/tillr-gate` (the `disabled-args` face against a temporary repository: `--packages` and `--package` output, `--prove-disabled`, exit 2 on a missing or doubled mode, a failure on a malformed list, and a real `go test` run showing the printed arguments skip the listed tests in one package while a same-named test in a sibling package still runs), `go test ./shards -run 'FullSkipPattern|SkipPattern'` and `go test ./goaffected -run 'TestLoadDisabledTests|TestRunSelectedTestsExcludes|TestVerdictDigestChangesWhen'` in the same module (the worktree and trunk-tip union including a broken trunk blob, unsharded, narrowed and sharded exclusion, and the verdict digest changing with the disabled set), `go test ./tillrdocs -run FacesHelp` in `tools/tillr` (the `faces` alignment fixture), and by running the `.justfile` `test` recipe's partition block by hand against a fixture module. Two differences from this design's text are the tree's facts: the `-skip` pattern builders live in the new `tools/tillr-gate/internal/skip` package, with `shards.SkipPattern` and the new `shards.FullSkipPattern` delegating to it, because the `shards` tests import `goaffected` and `goaffected` importing `shards` would be an import cycle; and `ComputePackageVerdictDigest` keeps its signature and reads the disabled set from a new `Disabled` field on `VerdictDigestCache`, so its callers did not change. `rg -n 'prove-disabled' tools/tillr-drain` still returns nothing. Every mechanism, file format, setting name and slice below (`D-TPF1` to `D-TPF11`) is **the designer's decision, not an owner ruling**, recorded from operator-filed requests ([decision provenance review](../tillr/decision-provenance-review.md#6-how-this-stops-happening)), and the owner may overturn any of them.

> **In short.** On 2026-11-27, `go-test-tillr-drain` ran twice on commit `50e065bc`: run one failed only `TestWhyAfterRestartOverLiveEvaluator/resumed_segment`, and run two failed only `TestAnOperatorStopInterruptsTheBackoff` and `TestIdleLoopRunDoesNotReplanUntilChange` (`16 / 249` = `6.43%` failure rate across `.rolling/logs`, even after an earlier fix in `929285ab` landed). Whole-check contradiction disabling ([Landing Principle §17](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking), [`D-GR21`](../reference/gate.md#D-GR21)) saw only `(fail, fail)`, disabled nothing, held `refs/tillr/green` back all day, and left an operator filing per-test items by hand. The owner's 2026-11-28 direction replaces detection with one rule: **a test that fails in the trunk proof is disabled at once, without a rerun; the rest of the suite decides the proof; the failure goes to the mechanic, who decides whether it is a flake or a break and what to do about it.** The commit counts as green. The disabled list is a tracked file in the repository, hosted CI skips the same tests, and `tillr item sync` queues a repair for every listed test that has none.

**Why it matters.** A 2,144-test suite with three `2%–6%` load-sensitive flakes fails as a whole on most runs even when no trunk commit broke anything. Today that starves `refs/tillr/green` ([`D-GR5`](../reference/gate.md#D-GR5)), fools culprit bisection into filing false `fixer-main-<tip>` items or reverting innocent commits, and forces manual triage. Under this direction one failed test costs one disabled entry and one mechanic job, and the proof still advances.

**The shape.** [Section 1](#1-owner-direction-the-2026-11-27-incident-and-the-2026-11-28-rulings) records the incident and the owner's words. [Section 2](#2-the-unit-the-tracked-disabled-list-and-exclusion) defines the per-test selector, the tracked disabled list, and how `tillr-gate shards`, `tillr-gate go-affected`, and `tillr-gate disabled-args` exclude listed tests. [Section 3](#3-the-proofs-rule-a-failed-test-is-disabled-at-once) states the proof's rule, what happens to a failure that cannot be pinned to one test, and what leaves the proof (the focused rerun and culprit bisection). [Section 4](#4-the-mechanic-job-sync-and-re-enabling) covers the mechanic job, `tillr item sync`, the disabling commit and re-enabling. [Section 5](#5-green-the-proved-pointer-and-publishing) covers green, the proved pointer and the optional push hold. [Section 6](#6-check-against-landing-principles) checks every landing principle and names the ones these rulings change. [Section 7](#7-what-is-deliberately-not-done) lists non-goals, [Section 8](#8-build-slices) restates the build slices, and [Section 9](#9-open-questions) records that no question remains open.

**Cost.** A green suite costs zero extra subprocesses (`go test -json` is already parsed in [`tools/tillr-gate/shards`](../../tools/tillr-gate/shards)). A failed test costs nothing extra in the proof: it is not run again. Verifying a repair runs only the named test once (`~1–30 s` instead of `6.6–38.9 min` for a 6-shard suite).

**Start at:** [Section 3](#3-the-proofs-rule-a-failed-test-is-disabled-at-once), then [Section 4](#4-the-mechanic-job-sync-and-re-enabling).

**Needs your ruling:** None. The designer's decisions [`D-TPF1`](#D-TPF1) to [`D-TPF11`](#D-TPF11) in [the Decision Ledger](#decision-ledger) are open to the owner's overturn, and [Section 6.1](#61-where-these-rulings-change-a-recorded-principle) names the recorded principles a later item must amend.

**Reads with:** [landing principles](./landing-principles.md), [the gate reference](../reference/gate.md), [gate check rules](../reference/gate-check-rules.md), [the mechanic seat](./tillr-mechanic-escalation.md), [test runner adapters](./test-runner-adapters.md), [skip test work whose result is proved unchanged](./gate-test-caching.md), and [repairing a red trunk proof on a moving trunk](./trunk-repair-on-moving-trunk.md).

**Terms.** *Trunk proof* (or *proof*): the after-merge run of the heavy checks on a trunk commit that decides whether `refs/tillr/green` (the *proved pointer*) moves to it ([the gate reference](../reference/gate.md#after-merge-proof-and-the-green-pointer)). *Mechanic*: the owner's name for one general fixer agent that takes a failure and decides what it is and what to do; its seat is specified in [the mechanic design](./tillr-mechanic-escalation.md). *Disabled list* (coined here): the tracked file of tests the gate does not run, defined in [Section 2.2](#22-the-disabled-list-is-a-tracked-file-d-tpf10). *Mechanic job* (coined here): the one queue item a proof run files for the mechanic, defined in [Section 4.1](#41-one-mechanic-job-per-proof-run-d-tpf6). *Publishing*: pushing the proved commit to the remote, configured by the `[push]` table in [`.tillr.toml`](../../.tillr.toml).

---

## 1. Owner direction, the 2026-11-27 incident, and the 2026-11-28 rulings

### 1.1 Owner direction and the 2026-11-28 rulings

The 2026-11-27 filed request records the owner's initial direction (an operator-recorded statement of the owner's direction under `D-PR1` in [`docs/tillr/decision-provenance-review.md`](../tillr/decision-provenance-review.md#6-how-this-stops-happening), because the request paraphrases the owner's spoken words):

> "The owner asked on 2026-11-27 why the trunk has stayed unproved all day, and ruled that this should be made to work: the proof should be able to run just the failing tests instead of the whole suite, and fixes should be targeted."

Three earlier dated owner rulings set the surrounding rules:

- **[`D-GR21`](../reference/gate.md#D-GR21) (2026-11-21):** `"The correct move for a flaky test is disable it, file an item to fix it, and don't let it block anything. It is not considered green, but it also doesn't let you block other things."` and `"And the principle here is you make the trunk green by disabling the flaky test. The test is not green because it's flaky, but the trunk is green because the test is no longer included.And yes, the item that is filed should be to re-enable it correctly, the test that was flaky."`
- **[`D-GR17`](../reference/gate.md#D-GR17) (2026-11-19):** `"Rule of this repository: a test that fails or hangs under load is a broken test; do not add a timeout, a retry or a longer wait as the fix"`.
- **Test-skipping soundness bar (2026-11-25, in [`gate-test-caching.md`](./gate-test-caching.md#1-owner-direction-and-framing)):** `"if we know it's correct, we can be just as aggressive as possible."`

On **2026-11-28** the owner reviewed this document and ruled in two steps.

**First, two questions** (both compacted into [the Decision Ledger](#decision-ledger) unchanged):

- **[`OQ-TPF2`](#OQ-TPF2) — option A:** leaf subtest granularity (`go-test:<module>:<pkg>:<Parent>/<Sub>`) when a subtest fails, widening to the parent only when the parent fails outside subtests ([Section 2.1](#21-the-per-test-selector-and-leaf-subtest-granularity-oq-tpf2-d-tpf1)).
- **[`OQ-TPF3`](#OQ-TPF3):**
  > "I think something like C. if we detect a flake, it goes for a fix and is disabled so it doesn't block hte current proof. after the fix, we run it again, if it passes, that's good neough. let the fixer decide what to do. if it fails again in a flakey way, hten we run it through the same loop again."

**Then the direction that settles [`OQ-TPF1`](#OQ-TPF1).** The owner had sent [`OQ-TPF1`](#OQ-TPF1) back (`"this seems liek a more generic issue for flakey tests and bisection and trunk proof, no? I'm not entierly sure any of these are teh ansewr."`). The owner's words, 2026-11-28, in order:

> "is it possible to mechanically find flakes? this seems impossible. even if we could dial into individual tests. a human would look at the failed test, see it was green last time, red now, and look at their code and argue it's not related or maybe rerun and pray."

> "teh thing I don't like here is that this means every singel failed test gets rerun always. a failure should go back to a fixer. it's either a flake or broken, but perhaps the issue is we don't know who the fixer is?"

> "say any failed test on the trunk gets disbaled always withot any rerun, and sent to a generic mechanic/fixer agent, who then figures out if it shoudl be fixed diretly, or sent back to an item, or reverted on trunk, or whatever."

> "does it really make "green" wekaer? we already keep developing and landing. this just makes it not annoying, no?"

The operator then put two points to the owner, who ruled both on 2026-11-28:

> "yes, absolutely the disalbed test lives in the repo. as little as possible should "only live in the store". I think this is all correct now. you can file it"

> "treat as green, yes"

And two follow-ups, 2026-11-28:

> "this also means that a tillr sync should find disabled tests and queue them for fixing I think"

> "we can have an optional config to disable pushing until 'real' green"

Read together (the filed request's reading; the operator's, not the owner's words):

1. **No detection and no rerun.** A test that fails in the trunk proof is disabled at once, and the rest of the suite decides the proof ([Section 3](#3-the-proofs-rule-a-failed-test-is-disabled-at-once)).
2. **One fixer.** The failure goes to the mechanic, who decides what it is and whether to fix it directly, send it back to the item that caused it, revert a landing on the trunk, or repair a flaky test. By [`OQ-TPF3`](#OQ-TPF3), the test is enabled again when it passes once after the fix, and goes round again if it fails again ([Section 4](#4-the-mechanic-job-sync-and-re-enabling)).
3. **Green everywhere.** A commit proved with disabled tests moves the proved pointer, new lanes start from it, and it is published; there is no second, stricter pointer ([Section 5](#5-green-the-proved-pointer-and-publishing)).
4. **The list lives in the repository.** The published commit states which tests are off, hosted CI skips the same ones, and as little as possible lives only in the store ([Section 2.2](#22-the-disabled-list-is-a-tracked-file-d-tpf10)).
5. **Sync queues repairs.** `tillr item sync` reads the tracked list and queues a repair for each listed test with no open repair, whoever listed it, in this repository or a consumer repository ([Section 4.2](#42-tillr-item-sync-queues-a-repair-for-every-uncovered-entry-d-tpf11)).
6. **Optional push hold.** A repository setting, off by default, holds the push until the proved commit has no disabled tests; the proved pointer and lane bases still advance ([Section 5.2](#52-the-optional-push-hold-d-tpf11)).

### 1.2 What happened on commit `50e065bc` (2026-11-27)

On commit `50e065bc` (`"fix(drain): query idle lane runs per lane to avoid SQLite OpenEphemeral"`), the proof's `go-test-tillr-drain` check ([`tools/gate/checks/go-test-tillr-drain.json`](../../tools/gate/checks/go-test-tillr-drain.json)) ran twice on the identical tree:

| Test selector in `github.com/mschulkind-oss/tillr-drain/drain` | Proof run 1 on `50e065bc` | Proof run 2 on `50e065bc` | Historical frequency / prior fix |
| :--- | :--- | :--- | :--- |
| `TestWhyAfterRestartOverLiveEvaluator/resumed_segment` ([`restart_evaluator_report_test.go`](../../tools/tillr-drain/drain/restart_evaluator_report_test.go)) | **FAIL** | **PASS** | Recorded top-level duration `29.33 s` in [`tools/go-test-durations.json`](../../tools/go-test-durations.json) |
| `TestAnOperatorStopInterruptsTheBackoff` ([`loop_test.go`](../../tools/tillr-drain/drain/loop_test.go)) | **PASS** | **FAIL** | Recorded duration `0.79 s` (`18.92 s` measured at host load `179.38` on 2026-11-28) |
| `TestIdleLoopRunDoesNotReplanUntilChange` ([`idle_test.go`](../../tools/tillr-drain/drain/idle_test.go)) | **PASS** | **FAIL** | Failed in **`16` of `249`** (`6.43%`) drain-suite logs under `.tillr/lanes/.rolling/logs` (`rg -l` on 2026-11-27), despite an earlier fix landing in `929285ab` (2026-11-26) |
| **Whole check `go-test-tillr-drain` (`2,144` top-level tests)** | **FAIL** | **FAIL** | Trunk stayed unproved; operator had to file one item per test by hand |

Under the rule in [Section 3](#3-the-proofs-rule-a-failed-test-is-disabled-at-once), run 1 would have disabled `resumed_segment`, passed the check on the other `2,143` tests and their siblings, advanced `refs/tillr/green`, and filed one mechanic job. Run 2 (on a later commit) would have done the same for the other two tests.

### 1.3 Three code gaps behind the stall and the escaped fix

1. **Disabled markers are keyed on whole check declarations, not test selectors.** `treeFingerprint` in [`tools/tillr-gate/memory.go`](../../tools/tillr-gate/memory.go) and `disabledPath(checkID)` in [`tools/tillr-gate/enable.go`](../../tools/tillr-gate/enable.go) record `pass` and `fail` only for `"go-test-tillr-drain"` as a single unit, so alternating failures record `(fail, fail)` and never trigger rule 3 of `Classify` in [`tools/tillr/checkresult/classify.go`](../../tools/tillr/checkresult/classify.go).
2. **Live check execution does not exclude disabled units or pass `--prove-disabled`.** As noted under *Not enforced* in [Landing Principle §17](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking), live `tillr-gate checks --heavy-tree` only annotates `outcome.Memory = "flaky"` in `annotateMemory`, `tillr-gate shards` skips nothing, and `tillr-drain` never passes `--prove-disabled`.
3. **`flaky-*` repair items bypass named-test verification before merge.** In [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) and [`tools/tillr-drain/drain/fixer_gate.go`](../../tools/tillr-drain/drain/fixer_gate.go), `settleFixerGate` runs only for `IsTrunkFixer` (`fixer-main-<sha>`), not for `IsFlakyRepairItem` (`flaky-<slug>`). Because [`D-GR53`](../reference/gate.md#D-GR53) and [`D-GR56`](../reference/gate.md#D-GR56) deferred `github.com/mschulkind-oss/tillr-drain/drain` out of pre-merge `go-affected` (`5e728e5a`), a candidate touching `idle.go` and `idle_test.go` (such as `929285ab`) merged without running `TestIdleLoopRunDoesNotReplanUntilChange` even once.

---

## 2. The unit, the tracked disabled list, and exclusion

<a id="21-the-per-test-check-selector-d-tpf1-subject-to-oq-tpf2"></a>

### 2.1 The per-test selector and leaf subtest granularity ([`OQ-TPF2`](#OQ-TPF2), [`D-TPF1`](#D-TPF1))

`tillr-drain` already defines a Go test selector grammar in `GoSuiteFailures` and `JudgeHeavyGo` ([`tools/tillr-drain/internal/gatespans/heavy_go.go`](../../tools/tillr-drain/internal/gatespans/heavy_go.go)), which [test runner adapters](./test-runner-adapters.md) keeps byte-for-byte as its `go-test` key:

```text
go-test:<module>:<package-import-path>:<TestName>
```

Examples from commit `50e065bc`:

```text
go-test:tools/tillr-drain:github.com/mschulkind-oss/tillr-drain/drain:TestIdleLoopRunDoesNotReplanUntilChange
go-test:tools/tillr-drain:github.com/mschulkind-oss/tillr-drain/drain:TestAnOperatorStopInterruptsTheBackoff
go-test:tools/tillr-drain:github.com/mschulkind-oss/tillr-drain/drain:TestWhyAfterRestartOverLiveEvaluator/resumed_segment
```

`tillr-gate shards` (`RunCachedGroup` in [`tools/tillr-gate/shards/cached.go`](../../tools/tillr-gate/shards/cached.go) and shard execution in [`tools/tillr-gate/shards/runner.go`](../../tools/tillr-gate/shards/runner.go)) already parses every `go test -json` `TestEvent`. Under the owner's [`OQ-TPF2`](#OQ-TPF2) ruling (option A), it attributes a failure as follows ([`D-TPF1`](#D-TPF1)):

1. **Leaf-only failure.** Go emits `"Action": "fail"` for both a failing subtest and its enclosing parent. A selector `T` that emits `fail` is a failed test **if and only if no descendant `T/<child>` also emitted `fail` in that run**. So a failure inside `t.Run("resumed segment", ...)` names only `.../TestWhyAfterRestartOverLiveEvaluator/resumed_segment`, and sibling `first_segment` stays in the proof.
2. **Widening to the parent.** A `t.Fatal` in the parent outside `t.Run` (or after every subtest passed) has no failing descendant, so it names the parent.
3. **Per-test output slice.** The runner buffers lines by `ev.Test` (keeping the parent's preamble before `t.Run`), so each failed test carries only its own output (typically `20–200` lines) rather than a multi-megabyte suite log. The slice goes to the mechanic job as evidence ([Section 4.1](#41-one-mechanic-job-per-proof-run-d-tpf6)), not into the repository.

### 2.2 The disabled list is a tracked file ([`D-TPF10`](#D-TPF10))

The owner ruled that the disabled test *"lives in the repo"* and that *"as little as possible should 'only live in the store'"*. The designer's decision ([`D-TPF10`](#D-TPF10), which the owner may overturn):

1. **Where it lives.** One JSON file per repository, its path set by a new `.tillr.toml` key `[gate] disabled_tests`. The default for a repository that sets nothing is `tillr-disabled-tests.json` at the repository root; this repository sets `tools/gate/disabled-tests.json`, beside [`allowed-commit-identities.json`](../../tools/gate/allowed-commit-identities.json). A missing file means no test is disabled.
2. **What an entry holds.** Only facts a reader of the published commit can check:

   ```json
   {
     "version": 1,
     "disabled": [
       {
         "selector": "go-test:tools/tillr-drain:github.com/mschulkind-oss/tillr-drain/drain:TestIdleLoopRunDoesNotReplanUntilChange",
         "source": "trunk-proof",
         "failed_on": "<trunk commit sha the proof ran>",
         "first_line": "idle_test.go:88: replanned without a trigger"
       }
     ]
   }
   ```

   `source` is `trunk-proof` for an entry the proof added and `hand` for one a person added. `failed_on` and `first_line` are omitted on a hand entry if the person has nothing to state. The file names **no queue item**: item ids are private queue state, and the link from a selector to its repair is the one thing kept in the store, where sync can rebuild it from the file ([Section 4.2](#42-tillr-item-sync-queues-a-repair-for-every-uncovered-entry-d-tpf11)).
3. **Whole checks too.** A whole check disabled today by a store marker under [`D-GR21`](../reference/gate.md#D-GR21) becomes an entry whose `selector` is the check id. The store's `disabled/*.json` markers and the `resolved/` episode archive retire; the file's git history is the episode record.
4. **Checked by the gate.** A tree check validates the file (schema, unique selectors, selector grammar) so a malformed hand edit fails the gate rather than disabling the wrong test.
5. **Hosted CI honors it.** Because the file is in the tree, any CI that runs a suite through `tillr-gate shards` reads it with no extra step (as [`.justfile`](../../.justfile)'s `test` recipe already does for `tools/tillr-drain` when called with no arguments). For a CI or build recipe that invokes `go test` directly, Go's `-skip` flag (`$GOROOT/src/testing/match.go`) matches only test and subtest names inside a test binary and has no package qualifier, and modules in this repository carry identically named top-level tests across sibling packages (for example `TestAReadOnlyOpenDoesNotMigrate` and `TestMainExitCodes` in `tools/tillr`, and `TestMaxShardsScalesWithCPUCount` in `tools/tillr-gate/shards` and `tools/tillr-gate/goaffected`), so a module-wide `go test ./... -skip <pattern>` would skip same-named tests in sibling packages. Under the designer's decision ([`D-TPF10`](#D-TPF10), revised 2026-11-28, which the owner may overturn), `tillr-gate disabled-args` scopes `-skip` per Go package ([Section 2.4](#24-excluding-listed-tests-in-lane-gates-and-direct-go-test-runs-d-tpf10)): `--module <module> --packages` prints the sorted package import paths in `<module>` that have active disabled `go-test:` entries, and `--module <module> --package <package-import-path>` prints `-skip` and `<pattern>` for that one package. In [`.justfile`](../../.justfile), `test` (run by `just check-ci` in [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml), which itself needs no edit) keeps a single `(cd "$dir" && go test ./... "$@")` call when `--packages` prints nothing and otherwise partitions `(cd "$dir" && go list ./...)` into unlisted packages (run together without `-skip` when non-empty) and listed packages (each run with its own `--package` skip arguments).

### 2.3 Excluding listed tests in `tillr-gate shards`

Before `tillr-gate shards` partitions a package's `Test*` functions across shards (`Assign` in [`tools/tillr-gate/shards/runner.go`](../../tools/tillr-gate/shards/runner.go)):

1. **Read the listed selectors for the package.** Read the disabled list from the gated tree and keep entries with prefix `go-test:<module>:<pkg>:`, except any selector passed via `--prove-disabled`. (`shards` runs in heavy tree checks under `checks --heavy-tree` and in `just test` for `tools/tillr-drain`; lane gates run `tillr-gate go-affected`, covered in [Section 2.4](#24-excluding-listed-tests-in-lane-gates-and-direct-go-test-runs-d-tpf10).)
2. **Drop listed top-level tests before `Assign`.** A listed `TestName` (no `/`) is removed from the names before shard `-run` patterns are built. It never starts and is reported as `disabled`.
3. **Skip listed subtests with Go `-skip` ([`OQ-TPF2`](#OQ-TPF2)).** When `TestParent/sub1` is listed and `TestParent` still runs, `shards` passes `-skip`. Execution against Go 1.27.3's `$GOROOT/src/testing/match.go` (`splitRegexp`, `alternationMatch.matches`, `fullName`) fixes two construction rules:
   - **Never wrap slash-separated patterns in parentheses.** `splitRegexp` splits on `/` only at parenthesis depth zero, so selectors are joined by unparenthesized top-level `|` with each segment anchored (`^TestA$/^sub1$|^TestB$`).
   - **Prune child patterns under a listed parent.** `alternationMatch.matches` returns the first matching branch even when partial, so `^TestA$/^sub1$` is dropped when `^TestA$` is listed.
   - A `TestEvent` with `"Action": "skip"` matching a listed selector is reported as `disabled`, never `pass` and never `skipped`.
4. **Remembered passes stay sound.** The disabled list is a declared read of every `go-test-*` check, so the whole-check remembered pass in [`tools/tillr-gate/memory.go`](../../tools/tillr-gate/memory.go) is keyed on it: removing an entry invalidates the remembered pass, and the test is required on the next proof.

### 2.4 Excluding listed tests in lane gates and direct `go test` runs ([`D-TPF10`](#D-TPF10))

A lane gate invokes `tillr-gate change-gate` ([`tools/tillr-gate/change_gate.go`](../../tools/tillr-gate/change_gate.go), `checksArgs := []string{"checks", "--fast-tree"}`), which in `checksFace` ([`tools/tillr-gate/checks.go`](../../tools/tillr-gate/checks.go)) admits only `check.Scope == "change"` declarations and fast tree declarations; every `go-test-*` declaration has `"scope": "tree"`, `"tier": "heavy"` and runs only in the trunk proof (`checks --heavy-tree` in [`tools/tillr-drain/internal/gatespans/heavy_steps.go`](../../tools/tillr-drain/internal/gatespans/heavy_steps.go)). The Go package tests a lane gate runs come from `tillr-gate go-affected` ([`tools/gate/checks/go-affected.json`](../../tools/gate/checks/go-affected.json), [`tools/tillr-gate/goaffected/`](../../tools/tillr-gate/goaffected)), where `rg -c 'disabled|Disabled' tools/tillr-gate/goaffected/*.go` returns nothing on `HEAD`. Under the designer's decision ([`D-TPF10`](#D-TPF10), revised 2026-11-28, which the owner may overturn):

1. **Per-package skip arguments in `tillr-gate disabled-args`.** Unlike `shards.SkipPattern` (which omits top-level selectors because `shards` removes them before `Assign`), a direct `go test` call has no `Assign` step and needs one `-skip` pattern covering both listed top-level tests (`^TestName$`) and listed subtests (`^TestParent$/^sub$`), joined by unparenthesized top-level `|` with each `/`-separated segment anchored and descendants under a listed ancestor pruned. `tillr-gate disabled-args` accepts `--module <module>` (required; slash-normalized relative to `--root`), optional `--root <dir>` (defaulting to the repository root located from `<module>` or `.`, or `.` outside a repository), repeatable `--prove-disabled <selector>`, and exactly one of two mode flags (omitting both or passing both exits `ExitUsage = 2`, because Go's `-skip` cannot scope across packages in a multi-package `go test ./...` call):
   - `--packages`: prints the sorted `<package-import-path>`s in `<module>` that have at least one active disabled `go-test:` entry (after subtracting `--prove-disabled`), one per line, or empty stdout when none;
   - `--package <package-import-path>`: prints `-skip` and `<pattern>` on two lines when `<package-import-path>` in `<module>` has active disabled `go-test:` entries, or empty stdout when none.
   In [`.justfile`](../../.justfile)'s `test` recipe, each module not already sent through `tillr-gate shards` calls `go run -C tools/tillr-gate . disabled-args --root "$PWD" --module "$dir" --packages`: when empty, `(cd "$dir" && go test ./... "$@")` runs unchanged; when non-empty, `(cd "$dir" && go list ./...)` is partitioned into packages not in `--packages` (tested together in one `go test` invocation with `"$@"` when non-empty, preserving `[no test files]` build checks and passing sibling packages with same-named tests) and packages in `--packages` (each tested in its own `go test` invocation with the arguments from `disabled-args --root "$PWD" --module "$dir" --package "$pkg"` and `"$@"`). (Adding `"disabled-args"` to `faces` in [`tools/tillr-gate/main.go`](../../tools/tillr-gate/main.go) widens `gofmt` alignment on `"shadow": ... shadowFace,`, so `FacesHelpFixtureError` in [`tools/tillr/tillrdocs/faces.go`](../../tools/tillr/tillrdocs/faces.go) is updated to match.)
2. **Union with the trunk tip's list in `tillr-gate go-affected`.** `go-affected` accepts repeatable `--prove-disabled <selector>` and builds its active disabled set as the union of two lists, minus any selector in `--prove-disabled`:
   - *The gated worktree's list:* read `.tillr.toml` at `--root` (`settings.Read`, using defaults when `.tillr.toml` is absent) and load `[gate] disabled_tests` (`disabledtests.Load`; a missing file is an empty list, and a malformed `.tillr.toml` or list fails the run).
   - *The trunk tip's list:* resolve `cfg.TrunkRef() + "^{commit}"` (`refs/heads/<trunk>^{commit}`, default `"main"`) in `--root`'s git repository, read `<tip>:.tillr.toml` when present to find that commit's `DisabledTestsPath()`, and parse `<tip>:<path>` with `disabledtests.Parse`. When `--root` is not a git repository, `refs/heads/<trunk>` does not exist, `<path>` is absent at `<tip>`, or `<tip>:.tillr.toml` / `<tip>:<path>` fails to parse (logged to stderr so a broken trunk blob never deadlocks the lane fixing it), the trunk tip contributes no entries.
   Because a lane worktree shares the repository's refs, a test the trunk proof disabled after the lane's base is skipped in that lane's `go-affected` run too; and on a repair candidate that removes an entry, `go-affected` still skips it via the trunk tip's list while `settleRepairGate` ([Section 4.4](#44-re-enabling-a-test-one-pass-after-the-fix-oq-tpf3-d-tpf4)) runs the removed selector once with `--prove-disabled`.
3. **Excluding listed tests in `goaffected.RunSelectedTests` and `-cfix` verdict reuse.** For each selected package `(item.Module, item.ImportPath)` in [`tools/tillr-gate/goaffected/runner.go`](../../tools/tillr-gate/goaffected/runner.go) (excluding a failed floor module `./...`, which runs only to report a module-load build error):
   - When `go-affected` builds an explicit `-run` pattern (narrowed `item.SelectedTests != nil` or sharded whole-package `shardCount >= 2`), drop listed top-level tests of `(item.Module, item.ImportPath)` before duration estimation and `Assign` (treating `TestEdgeInputs` as absent if listed, and skipping the `go test` invocation on a narrowed package when no `readerTests` remain), and pass `-skip <pattern>` with the listed subtests (`shards.SkipPattern`).
   - When `go-affected` runs an unsharded whole package without `-run` (`shardCount < 2`), keep the package invocation (preserving test-binary compilation checks even when every test is listed) and pass `-skip <pattern>` joining both listed top-level tests and listed subtests of `(item.Module, item.ImportPath)`.
   - In [`tools/tillr-gate/goaffected/verdicts.go`](../../tools/tillr-gate/goaffected/verdicts.go), `ComputePackageVerdictDigest` folds in the package's sorted active disabled selectors when non-empty, so a `-cfix` verdict recorded while a test or subtest was disabled is not reused after it is re-enabled.

---

## 3. The proof's rule: a failed test is disabled at once

### 3.1 How a failure is handled today, and what the direction removes

Asking `"how exactly do we detect flakes in the trunk proof?"` on `HEAD` shows four sites that run Go tests. Each one either reruns a failed test or bisects on it, and none tells a flake apart reliably:

1. **The proof itself.** `JudgeHeavy` ([`heavy_steps.go`](../../tools/tillr-drain/internal/gatespans/heavy_steps.go)) invokes `tillr-gate checks --heavy-tree`; `memory.go` keys only whole declarations, and `annotateMemory` marks `flaky` without disabling anything. Live proofs never disable a test today.
2. **The focused rerun.** `runFocusedFailingTests` (same file) reruns the previous proof's failed tests ahead of the next proof; a `6%` flake passes it `~94%` of the time and is logged `"focused rerun passed; running whole suite"`, as if cured.
3. **Culprit bisection and revert.** `SearchFirstRed`, `attribute` and `offerRevert` in [`tools/tillr-drain/heavyrun/heavy.go`](../../tools/tillr-drain/heavyrun/heavy.go) (with `FindFirstRed` in [`heavy_attribution.go`](../../tools/tillr-drain/heavyrun/heavy_attribution.go)) replay the failed test on earlier commits. For a `6%` flake the replay passes `~94%` of the time, so the proof stays red and files a false `fixer-main-<tip>`; in the rest, a flip on an innocent midpoint can revert an innocent landing.
4. **Repair checks and lane gates.** Deferred-package `flaky-*` items bypass `settleFixerGate` ([Section 1.3](#13-three-code-gaps-behind-the-stall-and-the-escaped-fix)), and `settleTrunkAlreadyRed` in [`tools/tillr-drain/drain/trunk.go`](../../tools/tillr-drain/drain/trunk.go) replays `base` once when a lane gate fails.

The owner's direction removes sites 2 and 3 from the proof: a failed test is not run again, and the proof does not bisect. Site 4 is handled in [Section 4.4](#44-re-enabling-a-test-one-pass-after-the-fix-oq-tpf3-d-tpf4) (repairs) and left as it is for lane gates ([Section 7](#7-what-is-deliberately-not-done)).

### 3.2 The rule ([`OQ-TPF1`](#OQ-TPF1), [`D-TPF3`](#D-TPF3))

**Owner ruling ([`OQ-TPF1`](#OQ-TPF1)):** `"say any failed test on the trunk gets disbaled always withot any rerun, and sent to a generic mechanic/fixer agent"`. In the proof:

1. **Every test reaches one verdict, read once.** A test that passes is `pass`; a test that fails is `disabled` at once, with no rerun, no stored-pass lookup and no wait for a contradiction. Whether it is a flake or a break is the mechanic's question, not the proof's.
2. **The rest of the suite decides the check.** A suite with zero non-disabled failures, and no failure that cannot be pinned to one test ([Section 3.3](#33-a-failure-that-cannot-be-pinned-to-one-test-d-tpf7)), aggregates to `pass`, listing its `disabled` tests apart in the verdict and the run summary. Tests already on the list before the proof started count the same way.
3. **Then three things happen, in order** ([`D-TPF9`](#D-TPF9)): the proof files (or extends) the one mechanic job for this run ([Section 4.1](#41-one-mechanic-job-per-proof-run-d-tpf6)); the drain lands one commit adding the newly failed tests to the disabled list ([Section 4.3](#43-how-the-disabling-commit-reaches-the-repository-d-tpf9)); and, if every check passed, the proved pointer moves to the proved commit ([Section 5](#5-green-the-proved-pointer-and-publishing)). Staging across slices (designer's decision from the operator's Slice 4 request, which the owner may overturn): Slice 4 lands steps 2 and 3 before Slice 5 builds step 1, leaving newly disabled entries in the tracked list for Slice 5 and Slice 6 (`tillr item sync`, [Section 4.2](#42-tillr-item-sync-queues-a-repair-for-every-uncovered-entry-d-tpf11)) to cover.

Where the proof runs (designer's decision, [`D-TPF3`](#D-TPF3)): only the trunk proof adds entries. A lane gate, a repair candidate's check, and a mechanic's own runs never add an entry from their own failures; a failure on an unmerged candidate goes back to that candidate's worker as it does today.

A test that fails again on a later proof before its entry has landed is disabled again in that proof and added to the existing job's evidence; it does not open a second job ([Section 4.1](#41-one-mechanic-job-per-proof-run-d-tpf6)).

### 3.3 A failure that cannot be pinned to one test ([`D-TPF7`](#D-TPF7))

Some failures have no single test to disable: a build error, a package binary that panics or exits before reporting every test, Go's `panic: test timed out after` (which ends the whole package binary, [`D-GR31`](../reference/gate.md#D-GR31)), a deadlock report, and a watchdog stall (`ExitStalled = 6`, [`D-GR16`](../reference/gate.md#D-GR16)). The designer's decision ([`D-TPF7`](#D-TPF7)):

- **The check fails and the proof is red.** Nothing is disabled for that package, because the tests that did not run have no verdict and disabling the whole package would hide every test in it. The proved pointer does not move.
- **It goes to the same mechanic job** as the run's disabled tests, with the package, the runner output and the landings since that check last passed. Tests in other packages that failed normally are still disabled in the same run. In Slice 4 alone, before Slice 5 builds the mechanic job, the red check keeps filing a tip `fixer-main-<tip>` through `HeavyRunner.Fix` without bisecting or auto-reverting (designer's decision from the operator's Slice 4 request; the owner may overturn this).
- **A stall or a missing tool stays `unknown` or `unavailable`** ([Landing Principle §8](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)); it is never classified as a failed test.

### 3.4 Culprit bisection and the focused rerun leave the proof

- **Culprit bisection.** The proof no longer calls `SearchFirstRed`, `FindFirstRed` or `offerRevert` on a failed test. Whether to bisect, revert a landing on the trunk, or neither is the mechanic's call, using its gated lander revert ability (`revert-landing` in [the mechanic design](./tillr-mechanic-escalation.md#41-summary-of-the-seven-abilities-their-limits-and-their-store-records-d-mc6)). This also ends the false `fixer-main-<tip>` filings and innocent reverts described in [Section 3.1](#31-how-a-failure-is-handled-today-and-what-the-direction-removes). The `auto_revert` setting of [`D-GR6`](../reference/gate.md#D-GR6) then applies only to what the mechanic decides, never to a proof failure directly.
- **The focused rerun.** `runFocusedFailingTests` and `SaveHeavyFailingTests` have nothing to rerun: every failed test is disabled. They are removed.

### 3.5 Check against "a red is read once" and Landing Principles 11 and 17

- **"A red is read once, never re-run"** ([`D-GR22`](../reference/gate.md#D-GR22), [gate check rules](../reference/gate-check-rules.md#a-red-is-read-once-never-re-run)) holds literally: `"A failed check costs exactly one run of that check."` A failed test is read once, and no later pass is sought or credited. No amendment is needed.
- **[Landing Principle §11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out)** holds: nothing waits on a clock, and a disabled test is re-admitted only against a named change (a repair whose one run passes, [Section 4.4](#44-re-enabling-a-test-one-pass-after-the-fix-oq-tpf3-d-tpf4)). The diagnosis moves from the proof to the mechanic.
- **[Landing Principle §17](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking)** changes in two places, named in [Section 6.1](#61-where-these-rulings-change-a-recorded-principle).

---

## 4. The mechanic job, sync, and re-enabling

### 4.1 One mechanic job per proof run ([`D-TPF6`](#D-TPF6))

The designer's decision ([`D-TPF6`](#D-TPF6)):

1. **One job per proof run, not one per test.** A proof run with any disabled test or unpinnable failure files one item on the mechanic seat ([`SeatMechanic`, `D-MC8`](./tillr-mechanic-escalation.md#51-seatmechanic-in-backend-policy-and-seat-profiles-d-mc8-oq-mc3)), carrying for each failure: the selector (or the package, for [Section 3.3](#33-a-failure-that-cannot-be-pinned-to-one-test-d-tpf7)), its output slice, the proved commit, and the landings since that test last passed. Until the mechanic seat is built, the job runs on the existing trunk fixer seat.
2. **"Landings since it last passed" needs no fingerprint.** Every proof runs every unlisted test, so a test's last pass is the most recent earlier proof that ran it and passed. The job lists the first-parent landings from that proved commit to this one. The store keeps each proof's per-test verdicts as evidence; nothing about the test's inputs is keyed.
3. **The job replaces two filings.** In Slice 5 (after Slice 4 disables failed tests in the proof), the mechanic job replaces both the per-check `flaky-<slug>` item of [`flaky_repair.go`](../../tools/tillr-drain/drain/flaky_repair.go) and the `fixer-main-<sha>` trunk fixer that Slice 4 retains only for unpinnable failures. The structured test selection of [test runner adapters](./test-runner-adapters.md) (`D-TRA6`) carries the selectors, so `holdForOpenTestFix` and `linkMatchingOpenFixers` match on selectors rather than prose.
4. **The mechanic decides the outcome per failure** (the owner's words: *"fixed diretly, or sent back to an item, or reverted on trunk, or whatever"*). It may fix the test or code in its own candidate, hand a selector to the item that caused it (which then owns that selector's repair), revert a landing through the lander, or repair a flaky test. Every outcome ends with the entry removed by a commit that passes the repair gate ([Section 4.4](#44-re-enabling-a-test-one-pass-after-the-fix-oq-tpf3-d-tpf4)).
5. **The mechanic only removes entries.** Adding an entry is the proof's or a person's act; the mechanic's prohibition on skipping checks ([`D-MC7`](./tillr-mechanic-escalation.md#43-what-the-mechanic-may-not-do-and-mechanical-enforcement-d-mc7)) still holds, and removing an entry only puts a test back.
6. **The job is done** when every selector it covers is off the list or handed to another open item.

### 4.2 `tillr item sync` queues a repair for every uncovered entry ([`D-TPF11`](#D-TPF11))

**Owner ruling ([`OQ-TPF1`](#OQ-TPF1)):** `"this also means that a tillr sync should find disabled tests and queue them for fixing I think"`. The tracked list is the source of repair work. The designer's decision on how ([`D-TPF11`](#D-TPF11)):

1. **What sync reads.** `tillr item sync` reads the disabled list at the repository root's `HEAD`, as it reads roadmaps today ([`.tillr.toml`](../../.tillr.toml) `[sync]`). It works the same in a consumer repository, because the list path comes from that repository's `[gate] disabled_tests`.
2. **"Covered" means an open item names the selector.** A selector is covered when an open item's structured test selection names it: the mechanic job that disabled it, or an item the mechanic handed it to.
3. **One job per sync, for what is uncovered.** Sync files one mechanic job holding every uncovered selector, whoever listed it: a hand entry, an entry whose job was closed without removing it, or an entry that arrived from upstream. A second sync with nothing new uncovered files nothing.
4. **No double queueing with the proof.** Once Slice 5 lands, the proof files its job **before** it lands the list commit ([Section 4.3](#43-how-the-disabling-commit-reaches-the-repository-d-tpf9)), so by the time sync can see the entry, the job already covers it. If the list commit fails to land, the job still covers its selectors and nothing is queued twice. While Slice 4 is on the trunk ahead of Slice 5, newly disabled entries are persisted in the tracked list without a job and are queued once Slices 5 and 6 land (designer's decision from the operator's Slice 4 request; the owner may overturn this).

### 4.3 How the disabling commit reaches the repository ([`D-TPF9`](#D-TPF9))

The designer's decision ([`D-TPF9`](#D-TPF9)):

1. **Who commits.** The drain builds the change on the current trunk tip: add each newly failed selector to the disabled list, sorted, skipping any already listed. It gates the change with the ordinary fast gate (a list-only change selects the list's tree check) and lands it through `tools/tillr-land` like a revert ([`D-GR6`](../reference/gate.md#D-GR6)), so it has a `landings` row binding the verdict to the tree.
2. **How it is told apart from a hand-made commit.** The author is the landing identity `Trunk.LandingIdentity` reads from git config (the history filter allows nothing else), so the commit is marked by content, not by author: subject `chore(gate): disable tests that failed the trunk proof of <short-sha>`, a body listing each selector with its first output line, and a trailer `Tillr-Disabled-By: trunk-proof <full-sha>`. Because `LandDDL` in [`tools/tillr-land/land/schema.go`](../../tools/tillr-land/land/schema.go) has no `kind` column (`rg -n '^\s+kind\b' tools/tillr-land/land/schema.go` returns nothing), the `disable` landing kind is recorded on `landings` the same way an automatic revert records `revert-main-<sha12>` (`RevertItemID` in [`heavy_revert.go`](../../tools/tillr-drain/drain/heavy_revert.go)): by its synthetic `item_id` prefix `disable-main-<sha12>` (`run_id` `run-disable-<sha12>-<tip12>`, lane `disable`), with no change to `tools/tillr-land`. A commit that edits the list with neither the trailer nor a `disable-main-` landing is a hand edit, and its entries carry `source: hand`.
3. **The proved pointer does not wait for it.** The pointer moves to the commit the proof ran ([Section 5.1](#51-green-everywhere-oq-tpf1)); the disabling commit is proved by a later proof like any landing.

### 4.4 Re-enabling a test: one pass after the fix ([`OQ-TPF3`](#OQ-TPF3), [`D-TPF4`](#D-TPF4))

Under the owner's [`OQ-TPF3`](#OQ-TPF3) ruling (`"after the fix, we run it again, if it passes, that's good neough. let the fixer decide what to do. if it fails again in a flakey way, hten we run it through the same loop again"`), the designer's decision ([`D-TPF4`](#D-TPF4)):

1. **The entry is removed in the fix's own commit.** Any candidate that removes entries from the disabled list — the mechanic's fix, its revert, or the item a selector was handed to — passes a repair gate after the ordinary fast gate (`settleRepairGate`, extending [`fixer_gate.go`](../../tools/tillr-drain/drain/fixer_gate.go)).
2. **Absent or skipped is refused.** The candidate is refused if a removed selector reports `goTestAbsent` or `goTestSkipped`. Deleting, renaming or skipping the test needs an explicit `owner` obstacle (*"re-enable it correctly, the test that was flaky"*, [`D-GR21`](../reference/gate.md#D-GR21); [`D-GR17`](../reference/gate.md#D-GR17)).
3. **One run of each removed selector.** Run only the removed selectors, once, with `-count=1` and `--prove-disabled`, at `repair` priority ([`OQ-GR7`](../reference/gate.md#OQ-GR7)). One pass accepts; one failure returns the candidate to its worker without touching the list. No repeat count, stress run or `-race` run is required; how the fixer convinces itself is the fixer's choice ([Section 4.5](#45-measured-failure-rates-and-reproduction-tools-as-background-for-the-mechanic)).
4. **Round again.** Once the entry is gone, the test runs in every proof. If it fails again, the proof disables it again and files a new job: the same loop.
5. **A person may remove an entry by hand** without a passing run. The test then simply runs in the next proof, and goes round again if it fails.

### 4.5 Measured failure rates and reproduction tools as background for the mechanic

Under [`OQ-TPF3`](#OQ-TPF3) (`"let the fixer decide what to do"`), none of the repeat counts, `-race` invocations or contention runs below is a gate requirement; they stay as measured background so the mechanic can see why one quiet run may miss a timing race.

From the 2026-11-27 measurement on `.tillr/lanes/.rolling/logs`, `TestIdleLoopRunDoesNotReplanUntilChange` failed in **`16` of `249`** drain-suite runs:

$$p_{\text{obs}} = \frac{16}{249} \approx 0.06426 \quad (6.43\%)$$

At a stationary per-run failure probability $p = 16 / 249$, the probability $(1 - p)^N$ that $N$ independent runs all pass, with single-test wall times:

| Repeat count $N$ | Chance all $N$ runs pass $(233/249)^N$ | Sequential wall time on `0.79 s` test (`TestAnOperatorStopInterruptsTheBackoff`) | Sequential wall time on `17.01 s` test (`TestIdleLoopRunDoesNotReplanUntilChange`, at load `179.38`) | Sequential wall time on `29.33 s` test (`TestWhyAfterRestartOverLiveEvaluator`) |
| :--- | :--- | :--- | :--- | :--- |
| **$N = 1$** | **`93.57%`** | `0.8 s` | `17.0 s` | `29.3 s` |
| **$N = 5$** | **`71.75%`** | `4.0 s` | `85.1 s` (`1.4 min`) | `146.7 s` (`2.4 min`) |
| **$N = 10$** | **`51.48%`** | `7.9 s` | `170.1 s` (`2.8 min`) | `293.3 s` (`4.9 min`) |
| **$N = 35$** | **`9.78%`** | `27.7 s` | `595.4 s` (`9.9 min`; `~1.7 min` across `6` workers) | `1,026.6 s` (`17.1 min`; `~2.9 min` across `6` workers) |
| **$N = 46$** | **`4.71%`** | `36.3 s` | `782.5 s` (`13.0 min`; `~2.2 min` across `6` workers) | `1,349.2 s` (`22.5 min`; `~3.8 min` across `6` workers) |
| **$N = 70$** | **`0.96%`** | `55.3 s` | `1,190.7 s` (`19.8 min`; `~3.3 min` across `6` workers) | `2,053.1 s` (`34.2 min`; `~5.7 min` across `6` workers) |

*How the durations were obtained:* `0.79 s` and `29.33 s` come from [`tools/go-test-durations.json`](../../tools/go-test-durations.json) on `a52c6c14d27e`; `TestIdleLoopRunDoesNotReplanUntilChange` was measured on 2026-11-28 (`17.01 s`, alongside `18.92 s` for `TestAnOperatorStopInterruptsTheBackoff`) via `go test -C tools/tillr-drain -v -count=1 ./drain -run '^TestAnOperatorStopInterruptsTheBackoff$|^TestIdleLoopRunDoesNotReplanUntilChange$'` at host load average `179.38`.

**Optional reproduction techniques.** In [`idle_test.go`](../../tools/tillr-drain/drain/idle_test.go), the failure happens when scheduling delay lands an asynchronous pass notification inside `time.Sleep(150 * time.Millisecond)` after `"waiting for a trigger"` was logged. Proofs run `6` shards beside gates and lanes (`load average 60–180`); a solo `go test -count=N` loop on a quiet machine rarely opens that window. Go's race detector (`CGO_ENABLED=1 go test -race`, verified on this `go1.27.3` host in `1.06 s` because the tillr SQLite modules use pure-Go `modernc.org/sqlite`) catches unsynchronized access directly, and several `GOMAXPROCS=1` instances of one `go test -c` binary against `-test.run ^TestName$` reproduce preemption on that one test.

### 4.6 Priority and status views ([`D-TPF8`](#D-TPF8))

A disabled test hides a later regression in the same test. The designer's decision ([`D-TPF8`](#D-TPF8)):

1. **The mechanic job runs ahead of other work.** It is dispatched before ordinary items and runs at `repair` process priority ([`OQ-GR7`](../reference/gate.md#OQ-GR7)), like a trunk fixer today.
2. **The status views show the disabled tests.** The drain status line shows the count of listed tests; `tillr-view` lists each entry with its source, the commit it failed on, how long it has been listed, and the open item that covers it, or *uncovered* until the next sync. The trunk-proof summary names the tests it disabled in that run.

---

## 5. Green, the proved pointer, and publishing

### 5.1 Green everywhere ([`OQ-TPF1`](#OQ-TPF1))

**Owner ruling ([`OQ-TPF1`](#OQ-TPF1)):** `"treat as green, yes"`. A commit proved with disabled tests is green everywhere: `refs/tillr/green` moves to it, new lanes start from it, and it is published (unless the setting in [Section 5.2](#52-the-optional-push-hold-d-tpf11) is on). There is no second, stricter pointer. The owner's reason: `"does it really make "green" wekaer? we already keep developing and landing. this just makes it not annoying, no?"`

What carries across a moving trunk tip: the disabled list is tracked, so it travels with every commit by ordinary merge, and a lane gate also honors the tip's list ([Section 2.4](#24-excluding-listed-tests-in-lane-gates-and-direct-go-test-runs-d-tpf10)). This design keeps no per-test results across commits; skipping a test whose result is proved unchanged belongs to [`gate-test-caching.md`](./gate-test-caching.md) ([`OQ-GC1`](./gate-test-caching.md#OQ-GC1) to [`OQ-GC5`](./gate-test-caching.md#OQ-GC5)). Repairing a red proof (now only an unpinnable failure, [Section 3.3](#33-a-failure-that-cannot-be-pinned-to-one-test-d-tpf7)) on a moving trunk stays with [`trunk-repair-on-moving-trunk.md`](./trunk-repair-on-moving-trunk.md).

### 5.2 The optional push hold ([`D-TPF11`](#D-TPF11))

**Owner ruling ([`OQ-TPF1`](#OQ-TPF1)):** `"we can have an optional config to disable pushing until 'real' green"`. The designer's decision on its form ([`D-TPF11`](#D-TPF11)): a key in the existing `.tillr.toml` `[push]` table,

```toml
[push]
what = "green"
# Holds the push while the proved commit lists any disabled test. Off by default.
hold_while_disabled = false
```

With the key off (the default), the owner's green-everywhere ruling stands as written. With it on, the drain pushes only a proved commit whose disabled list is empty; the proved pointer and lane bases still advance. The status line says the push is held and names the count of disabled tests holding it.

---

## 6. Check against landing principles

Every principle in [`docs/design/landing-principles.md`](./landing-principles.md) is checked below.

| Principle in [`landing-principles.md`](./landing-principles.md) | Touched? | How this design satisfies it, or where it changes it |
| :--- | :--- | :--- |
| **[§1. A refusal must be a verdict](./landing-principles.md#1-a-refusal-must-be-a-verdict)** | **Yes** | An unrelated failed test no longer convicts an innocent commit by culprit bisection; a repair candidate that deletes or skips its test is refused with an explicit verdict. |
| **[§2. Re-application preserves the change, not the patch](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch)** | **No** | The disabling commit is an ordinary squash landing; nothing compares patch identity. |
| **[§3. Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once)** ([`D-GR2`](../reference/gate.md#D-GR2)) | **Yes** | Lane gates honor the tip's list without a cross-lane lock; a repair runs only its removed selectors once. Proof cost no longer grows with bisection over trunk traffic. |
| **[§4. Every outcome is state-changing](./landing-principles.md#4-every-outcome-is-state-changing)** | **Yes** | Every proof either advances the proved pointer (with listed tests) or files a mechanic job for an unpinnable failure; the `50e065bc` loop of red proofs with no state change ends. |
| **[§5. Only verdicts charge](./landing-principles.md#5-only-verdicts-charge)** ([`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)) | **Yes** | Stalls and missing tools stay `unknown` or `unavailable` and disable nothing. |
| **[§6. A budget escalates, it does not loop](./landing-principles.md#6-a-budget-escalates-it-does-not-loop)** | **Yes** | A mechanic job that repeats identical endings escalates by `repeatedEnding` and the mechanic design's chain. |
| **[§7. A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report)** | **Yes** | Listed tests are never shown as `pass`; status views list each one and its covering item ([Section 4.6](#46-priority-and-status-views-d-tpf8)). |
| **[§8. A gate that cannot answer must say which](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)** ([`D-GR15`](../reference/gate.md#D-GR15)) | **Yes** | Stalls, crashes and missing binaries are never classified as failed tests ([Section 3.3](#33-a-failure-that-cannot-be-pinned-to-one-test-d-tpf7)). |
| **[§9. Fix the machinery; never hand-carry the work](./landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work)** | **Yes** | Disabling, filing and re-enabling are automatic; sync covers hand entries. |
| **[§10. Supersession is a verdict, not a stall](./landing-principles.md#10-supersession-is-a-verdict-not-a-stall)** | **No** | Unchanged. |
| **[§11. A failure is diagnosed, not waited out](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out)** | **Yes, kept** | No rerun and no clock; re-admission needs a named change (a passing repair). The diagnosis moves from the proof to the mechanic ([Section 3.5](#35-check-against-a-red-is-read-once-and-landing-principles-11-and-17)). |
| **[§12. A principle cites the measurement that produced it](./landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it)** | **Yes** | Rests on the `50e065bc` two-run measurement, the `16 / 249` count and the `929285ab` zero-run escape ([Section 1.2](#12-what-happened-on-commit-50e065bc-2026-11-27)). |
| **[§13. A held state names a mover that can still act](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)** | **Yes** | Once Slices 5 and 6 land, every listed test is covered by an open item and sync files one for any that is not ([Section 4.2](#42-tillr-item-sync-queues-a-repair-for-every-uncovered-entry-d-tpf11)); in Slice 4 alone, disabled entries persist in the tracked list awaiting Slices 5–6 while unpinnable failures keep filing `fixer-main-<tip>`. |
| **[§14. Convict a run on what it did, not on what happened while it ran](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)** | **Yes** | Only the trunk proof adds entries; a lane's own failure never disables a test. |
| **[§15. Land the smallest functional change](./landing-principles.md#15-land-the-smallest-functional-change)** ([`D-GR32`](../reference/gate.md#D-GR32)) | **Yes** | Nine slices ([Section 8](#8-build-slices)); `tillr-gate` flags land with zero callers first, and Slice 4 lands ahead of Slice 5 after Slice 1 is on the trunk so the proof stops stalling on flakes without waiting on `refs/tillr/green`. |
| **[§16. What the operator can settle is settled inside tillr](./landing-principles.md#16-what-the-operator-can-settle-is-settled-inside-tillr)** | **Yes** | Ends hand-filing one repair per test from `.rolling/logs`. |
| **[§17. Disable a flaky check, file its repair, and count it neither green nor blocking](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking)** ([`D-GR21`](../reference/gate.md#D-GR21)) | **Yes, changed** | See [Section 6.1](#61-where-these-rulings-change-a-recorded-principle). |

### 6.1 Where these rulings change a recorded principle

This design does not edit [`landing-principles.md`](./landing-principles.md), [`gate.md`](../reference/gate.md) or [`gate-check-rules.md`](../reference/gate-check-rules.md); Slice 9 ([Section 8](#8-build-slices)) amends them once the behavior is built. These are the recorded rules the 2026-11-28 rulings change:

1. **[Landing Principle §17](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking) and [`D-GR21`](../reference/gate.md#D-GR21) — the trigger.** Principle 17 disables a check that *"both passes and fails on the same declared inputs"*. Under [`OQ-TPF1`](#OQ-TPF1), a test is disabled when it fails once in the trunk proof; no contradiction is needed.
2. **Principle 17 and [`D-GR21`](../reference/gate.md#D-GR21) — "neither green".** Principle 17 says *"the check itself is not green until its repair item lands"*, and `D-GR21` says *"It is not considered green"*. The owner's later ruling (`"treat as green, yes"`) makes the commit green for the proved pointer, lane bases and publishing. The listed test itself is still reported `disabled`, never `pass`, so the part of Principle 17 that keeps it out of the pass count stands.
3. **Principle 17's *Enforced by* and [gate check rules](../reference/gate-check-rules.md#a-check-set-aside-from-the-verdict-is-narrow-ticketed-and-counted) — the record.** Both describe a store marker per contradicted check and one `flaky-*` item per check. Under these rulings the record is the tracked list, and the repair is one mechanic job per proof run. The gate-check-rules sentence that no case-quarantine file exists, and its rule that such a file binds to the test file's blob id, are replaced by the tracked list, which lapses only when an entry is removed.
4. **[The gate reference's "A contradicted check is disabled"](../reference/gate.md#a-contradicted-check-is-disabled)** — the same change as item 1, in the reference.
5. **Kept unchanged:** [Landing Principle §11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out) and "a red is read once, never re-run" ([`D-GR22`](../reference/gate.md#D-GR22), [gate check rules](../reference/gate-check-rules.md#a-red-is-read-once-never-re-run)). The direction keeps both literally: a failed test is read once and never rerun.
6. **Touched in [the gate reference](../reference/gate.md#a-red-proof):** [`D-GR6`](../reference/gate.md#D-GR6)'s automatic revert on a red proof no longer fires on a failed test, because the proof no longer bisects; reverts become the mechanic's decision ([Section 3.4](#34-culprit-bisection-and-the-focused-rerun-leave-the-proof)).

---

## 7. What is deliberately not done

1. **No rerun of a failed test, anywhere in the proof.** No suite retry, no focused rerun, no confirmation run and no diagnostic burst; the owner rejected that `"every singel failed test gets rerun always"`.
2. **No flake detection in the proof.** No per-test contradiction store and no input fingerprint for detection; whether a failure is a flake or a break is the mechanic's question.
3. **No store-only disabled state.** The list is tracked; the store keeps only the link from a selector to its covering item and each proof's per-test verdicts as evidence.
4. **No change to lane gates beyond honoring the list.** A lane gate failure on a test that is not listed still goes back to the lane's worker, with today's `settleTrunkAlreadyRed` acquittal. Only the trunk proof adds entries.
5. **No whole-suite run, repeat count or stress bar in a repair's gate** ([`OQ-TPF3`](#OQ-TPF3)).
6. **No document item blocks in tracked Markdown.** Mechanic jobs live in the queue store ([`D-GR23`](../reference/gate.md#D-GR23)).

---

## 8. Build slices

No question is open, so under [`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8) every slice may start; the order below follows dependencies, [Landing Principle §15](./landing-principles.md#15-land-the-smallest-functional-change) and the installed-`tillr-gate` rollout rule ([`D-GR32`](../reference/gate.md#D-GR32)). Designer's decision on rollout between Slice 1 and Slice 4 (revised 2026-11-28 from the operator's Slice 4 request; the owner may overturn this): [`D-GR32`](../reference/gate.md#D-GR32) binds candidate fast-gate scripts under `tools/gate/` that invoke the installed `tillr-gate` binary; here `go-test-*` declarations run `go run -C tools/tillr-gate . shards` from the checked-out tree and `tillr-roll` builds and installs `tillr-gate` and `tillr-drain` together from the trunk tip (`StageSchemaRoll` and the `install` loop in [`tools/tillr-roll/roll/roll.go`](../../tools/tillr-roll/roll/roll.go)), whereas holding Slice 4 on `refs/tillr/green` deadlocks when `refs/tillr/green` (`11d7df65`) is held back behind Slice 1 (`a0be587c`) by the flaky tests Slice 4 disables. Each `tools/tillr*` slice includes `just pack-sync`.

1. **Slice 1 — The tracked list and its exclusion in `tillr-gate shards`** (`tools/tillr-gate/`, `tools/gate/`; zero `tillr-drain` callers). The `[gate] disabled_tests` key, the list's schema and tree check, leaf subtest attribution ([`OQ-TPF2`](#OQ-TPF2)), per-test output slicing, exclusion before `Assign` and via `-skip`, `--prove-disabled`, `disabled` counts in the summary, and the list as a declared read of `go-test-*` checks.
2. **Slice 2 — The one-run repair gate** (`tools/tillr-drain/`). `settleRepairGate` for any candidate that removes entries: refuse absent or skipped tests, run each removed selector once ([`OQ-TPF3`](#OQ-TPF3)). Closes the `929285ab` zero-run escape. After Slice 1 is on the trunk.
3. **Slice 3 — Hosted CI and lane gates honor the list** (`tools/tillr-gate/`, `tools/tillr/tillrdocs/` if `faces` alignment in `main.go` shifts `FacesHelpFixtureError`, `.justfile`, plus `just pack-sync`; `.github/workflows/ci.yml` already calls `just check-ci` and needs no edit). Per-package `tillr-gate disabled-args` (`--package` and `--packages`) wired into `.justfile`'s `test` recipe ([Section 2.2](#22-the-disabled-list-is-a-tracked-file-d-tpf10)), and per-test exclusion plus the union with the trunk tip's list in `tillr-gate go-affected` ([Section 2.4](#24-excluding-listed-tests-in-lane-gates-and-direct-go-test-runs-d-tpf10)). After Slice 1 is on the trunk.
4. **Slice 4 — The proof disables failed tests** (`tools/tillr-gate/`, `tools/tillr-drain/`). In the trunk proof only ([`D-TPF3`](#D-TPF3)), a failed test aggregates as `disabled`; unpinnable failures fail the check ([Section 3.3](#33-a-failure-that-cannot-be-pinned-to-one-test-d-tpf7)) and keep filing `fixer-main-<tip>` through `HeavyRunner.Fix` until Slice 5 replaces it; the disabling commit lands through `tillr-land` with its trailer and `disable` landing kind (`item_id` prefix `disable-main-<sha12>`, with no `tools/tillr-land/` change; [Section 4.3](#43-how-the-disabling-commit-reaches-the-repository-d-tpf9)) and without filing a mechanic job yet; `runFocusedFailingTests`, `SaveHeavyFailingTests` and the proof's calls to `SearchFirstRed`, `FindFirstRed` and `offerRevert` are removed. After Slice 1 is on the trunk (does not wait on `refs/tillr/green` or Slice 5).
5. **Slice 5 — The mechanic job** (`tools/tillr-drain/`). One job per proof run with selectors, output slices and landings since each last pass; hooks into Slice 4's proof path **before** the disabling commit lands and replaces `fixer-main-<sha>` for unpinnable proof failures; extend the same job on a repeat; dispatch ahead of other work. Files on the trunk fixer seat until the mechanic seat of [the mechanic design](./tillr-mechanic-escalation.md) is built. After Slice 4.
6. **Slice 6 — Sync queues repairs** (`tools/tillr-item/`). Read the list at `HEAD`, find uncovered selectors, file one job per sync. After Slice 5.
7. **Slice 7 — The push hold** (`tools/tillr-drain/`). `[push] hold_while_disabled`, off by default, with its status line. After Slice 1 is on the trunk.
8. **Slice 8 — Status views and store retirement** (`tools/tillr-view/`, `tools/tillr-drain/`, `tools/tillr-gate/`). List entries and coverage in `tillr-view` and the status line; move any store `disabled/*.json` marker into the list once; retire `sweepDisabledMarkers`, `reconcileDisabledMarker` and the store path of `tillr-gate enable`.
9. **Slice 9 — Reference and principles sync** (`docs/reference/gate.md`, `docs/reference/gate-check-rules.md`, `docs/design/landing-principles.md`). Record the 2026-11-28 rulings verbatim in the gate reference and make each amendment named in [Section 6.1](#61-where-these-rulings-change-a-recorded-principle) once Slices 1–8 are built.

---

## 9. Open questions

None. [`OQ-TPF1`](#OQ-TPF1), [`OQ-TPF2`](#OQ-TPF2) and [`OQ-TPF3`](#OQ-TPF3) were ruled by the owner on 2026-11-28 and are compacted into [the Decision Ledger](#decision-ledger). [`OQ-TPF4`](#OQ-TPF4) was withdrawn by the designer: with no contradiction step, nothing in this design ties a test result to its inputs (see its ledger row).

---

## Decision Ledger

Per [`decision-provenance-review.md` §6](../tillr/decision-provenance-review.md#6-how-this-stops-happening) (`D-PR1`, `D-PR2`, [`OQ-PR22`](../tillr/decision-provenance-review.md#OQ-PR22)), [`OQ-TPF1`](#OQ-TPF1), [`OQ-TPF2`](#OQ-TPF2) and [`OQ-TPF3`](#OQ-TPF3) are **owner rulings** from 2026-11-28, quoting the owner's words verbatim. [`D-TPF1`](#D-TPF1) to [`D-TPF11`](#D-TPF11) are **designer's decisions** prepared from operator-filed requests of 2026-11-27 and 2026-11-28; none is an owner ruling, and the owner may overturn any of them. [`OQ-TPF4`](#OQ-TPF4) was withdrawn by the designer, which the owner may reverse.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-TPF1"></a>[`OQ-TPF1`](#OQ-TPF1) | **Owner ruling (direction).** The owner's words, in order: *"is it possible to mechanically find flakes? this seems impossible. even if we could dial into individual tests. a human would look at the failed test, see it was green last time, red now, and look at their code and argue it's not related or maybe rerun and pray."* — *"teh thing I don't like here is that this means every singel failed test gets rerun always. a failure should go back to a fixer. it's either a flake or broken, but perhaps the issue is we don't know who the fixer is?"* — *"say any failed test on the trunk gets disbaled always withot any rerun, and sent to a generic mechanic/fixer agent, who then figures out if it shoudl be fixed diretly, or sent back to an item, or reverted on trunk, or whatever."* — *"does it really make "green" wekaer? we already keep developing and landing. this just makes it not annoying, no?"* Then, on two points the operator put: *"yes, absolutely the disalbed test lives in the repo. as little as possible should "only live in the store". I think this is all correct now. you can file it"* — *"treat as green, yes"*. Then: *"this also means that a tillr sync should find disabled tests and queue them for fixing I think"* — *"we can have an optional config to disable pushing until 'real' green"*. Read as: a test that fails in the trunk proof is disabled at once with no rerun; the rest of the suite decides the proof; the failure goes to the mechanic; the commit is green everywhere with no stricter pointer; the disabled list is tracked in the repository and hosted CI honors it; `tillr item sync` queues a repair for each listed test with none; and an optional setting, off by default, holds the push until no test is disabled. Replaces the three detection options this question earlier put. | 2026-11-28 | [Section 1.1](#11-owner-direction-and-the-2026-11-28-rulings), [Section 3.2](#32-the-rule-oq-tpf1-d-tpf3), [Section 4.2](#42-tillr-item-sync-queues-a-repair-for-every-uncovered-entry-d-tpf11), [Section 5](#5-green-the-proved-pointer-and-publishing) | No |
| <a id="OQ-TPF2"></a>[`OQ-TPF2`](#OQ-TPF2) | **Owner ruling (option A):** Leaf subtest granularity (`go-test:<module>:<pkg>:<Parent>/<Sub>`) when a subtest fails, widening to the parent test only when the parent fails outside subtests, keeping sibling subtests active in the proof via Go's `-skip` flag. The owner chose option A in the 2026-11-28 review in the option's words: *"A — Leaf subtest granularity (`go-test:<module>:<pkg>:<Parent>/<Sub>`), widening to `<Parent>` only when `<Parent>` fails outside any subtest. Disable and file `flaky-*` for the specific contradicting subtest (`.../TestWhyAfterRestartOverLiveEvaluator/resumed_segment`), keeping sibling subtests (`first_segment`) active in the proof via Go's `-skip` flag."* | 2026-11-28 | [Section 1.1](#11-owner-direction-and-the-2026-11-28-rulings), [Section 2.1](#21-the-per-test-selector-and-leaf-subtest-granularity-oq-tpf2-d-tpf1) | No |
| <a id="OQ-TPF3"></a>[`OQ-TPF3`](#OQ-TPF3) | **Owner ruling:** *"I think something like C. if we detect a flake, it goes for a fix and is disabled so it doesn't block hte current proof. after the fix, we run it again, if it passes, that's good neough. let the fixer decide what to do. if it fails again in a flakey way, hten we run it through the same loop again."* Together with [`OQ-TPF1`](#OQ-TPF1) (which disables any failed proof test without flake detection and sends it to the mechanic under [`D-TPF6`](#D-TPF6)), a failed test is disabled and sent for repair so it does not block the proof in progress; the repair is accepted when the named test passes once after the fix (`--prove-disabled`, `-count=1`), with no fixed repeat count, load-sized stress run, or mandatory `-race` run in the gate; how the fixer convinces itself in its lane is the fixer's choice; and if the test fails again in a later proof after its repair lands, it goes round the same disable-and-repair loop again. | 2026-11-28 | [Section 1.1](#11-owner-direction-and-the-2026-11-28-rulings), [Section 4.4](#44-re-enabling-a-test-one-pass-after-the-fix-oq-tpf3-d-tpf4) | No |
| <a id="OQ-TPF4"></a>[`OQ-TPF4`](#OQ-TPF4) | **Withdrawn by the designer, 2026-11-28; the owner may reinstate it.** It asked what input fingerprint should bind per-test pass and fail records across commits before kernel read sandboxing, so that a pass and a fail could be paired as a contradiction. Under [`OQ-TPF1`](#OQ-TPF1) nothing is paired: "landings since the last pass" comes from proof history ([Section 4.1](#41-one-mechanic-job-per-proof-run-d-tpf6)), and carrying a completed pass across a moving tip is the question [`gate-test-caching.md`](./gate-test-caching.md) already puts in [`OQ-GC1`](./gate-test-caching.md#OQ-GC1). | 2026-11-28 | [Section 5.1](#51-green-everywhere-oq-tpf1) | — |
| <a id="D-TPF1"></a>`D-TPF1` | **Designer's decision; the owner may overturn this (implementing [`OQ-TPF2`](#OQ-TPF2) option A; revised 2026-11-28 for [`OQ-TPF1`](#OQ-TPF1)).** Use the `JudgeHeavyGo` selector `go-test:<module>:<pkg>:<TestName>`; name a parent as failed only when no descendant subtest failed in that run; slice per-test output from `go test -json` into the mechanic job's evidence; drop listed top-level tests from `shards.Assign`; build `-skip` for listed subtests with unparenthesized top-level `\|` and listed parents pruning their children; and make the disabled list a declared read of `go-test-*` checks so remembered passes are keyed on it. | 2026-11-28 | [Section 2.1](#21-the-per-test-selector-and-leaf-subtest-granularity-oq-tpf2-d-tpf1), [Section 2.3](#23-excluding-listed-tests-in-tillr-gate-shards) | No |
| <a id="D-TPF2"></a>`D-TPF2` | **Designer's decision; the owner may overturn this. Superseded 2026-11-28 by [`D-TPF6`](#D-TPF6) and [`D-TPF11`](#D-TPF11).** It reused `sweepDisabledMarkers` and `reconcileDisabledMarker` to file one `flaky-<slug>` item per store marker. Under [`OQ-TPF1`](#OQ-TPF1) the proof files one mechanic job per run and sync covers the rest; those functions retire in Slice 8. | 2026-11-28 | [Section 4.1](#41-one-mechanic-job-per-proof-run-d-tpf6) | No |
| <a id="D-TPF3"></a>`D-TPF3` | **Designer's decision; the owner may overturn this (revised 2026-11-28 for [`OQ-TPF1`](#OQ-TPF1); it earlier specified a shared contradiction store).** Only the trunk proof adds entries to the disabled list; a lane gate, a repair candidate's check and the mechanic's own runs never do; a failure on an unmerged candidate returns to its worker. | 2026-11-28 | [Section 3.2](#32-the-rule-oq-tpf1-d-tpf3) | No |
| <a id="D-TPF4"></a>`D-TPF4` | **Designer's decision; the owner may overturn this (implementing [`OQ-TPF3`](#OQ-TPF3); revised 2026-11-28 for the tracked list).** Any candidate that removes entries from the disabled list passes `settleRepairGate` after the fast gate: refuse `goTestAbsent` and `goTestSkipped` for a removed selector, run each removed selector once (`-count=1`, `--prove-disabled`, never the whole suite, no repeat, stress or `-race` bar), and land on one pass. | 2026-11-28 | [Section 4.4](#44-re-enabling-a-test-one-pass-after-the-fix-oq-tpf3-d-tpf4) | No |
| <a id="D-TPF5"></a>`D-TPF5` | **Designer's decision; the owner may overturn this. Superseded 2026-11-28 by [`OQ-TPF1`](#OQ-TPF1).** It carried store markers and per-test results across a moving tip and ran prior failures first (`--prior-failed`). The list now travels in the tree, failed tests are already disabled, and cross-commit pass carryover belongs to [`gate-test-caching.md`](./gate-test-caching.md). | 2026-11-28 | [Section 5.1](#51-green-everywhere-oq-tpf1) | No |
| <a id="D-TPF6"></a>`D-TPF6` | **Designer's decision; the owner may overturn this (revised 2026-11-28 from operator request).** Failures from one proof run go to the mechanic as one job, not one per test, carrying each failed test (or unpinnable package), its output slice, the proved commit and the first-parent landings since that test last passed in a proof. A repeat failure before the entry lands extends the open job. Built in Slice 5 after Slice 4 lands: once Slice 5 is in place, the job replaces `flaky-<slug>` and `fixer-main-<sha>` filing for proof failures; the mechanic decides each outcome and may only remove entries. | 2026-11-28 | [Section 4.1](#41-one-mechanic-job-per-proof-run-d-tpf6), [Section 8](#8-build-slices) | No |
| <a id="D-TPF7"></a>`D-TPF7` | **Designer's decision; the owner may overturn this (revised 2026-11-28 from operator request).** A failure that cannot be pinned to one test (a build error, a package panic or early exit, a timeout panic, a deadlock, a stall) disables nothing: the check fails, the proof is red, the proved pointer does not move, and the failure goes into the same mechanic job once Slice 5 lands (retaining `HeavyRunner.Fix`'s tip `fixer-main-<tip>` without bisection or auto-revert in Slice 4 alone). Stalls and missing tools stay `unknown` or `unavailable`. | 2026-11-28 | [Section 3.3](#33-a-failure-that-cannot-be-pinned-to-one-test-d-tpf7), [Section 8](#8-build-slices) | No |
| <a id="D-TPF8"></a>`D-TPF8` | **Designer's decision; the owner may overturn this.** The mechanic job is dispatched ahead of ordinary items at `repair` priority, and the status line and `tillr-view` show every listed test with its source, commit, age and covering item. | 2026-11-28 | [Section 4.6](#46-priority-and-status-views-d-tpf8) | No |
| <a id="D-TPF9"></a>`D-TPF9` | **Designer's decision; the owner may overturn this (revised 2026-11-28 from operator request).** Slice 4 lands after Slice 1 is on the trunk without waiting on `refs/tillr/green` or Slice 5: the drain lands one list-only commit on the trunk tip through `tillr-land` under the landing identity read from git config, with subject `chore(gate): disable tests that failed the trunk proof of <short-sha>`, a trailer `Tillr-Disabled-By: trunk-proof <full-sha>`, and the `disable` landing kind recorded via `item_id` prefix `disable-main-<sha12>` (`run-disable-<sha12>-<tip12>`, lane `disable`, matching `revert-main-<sha12>` with no `tools/tillr-land` schema change); a list edit with neither the trailer nor a `disable-main-` landing is a hand edit. Slice 5 then wires mechanic-job filing ahead of that list commit. The proved pointer moves to the proved commit without waiting for the list commit. A test is enabled again by removing its entry in a commit that passes [`D-TPF4`](#D-TPF4)'s one-run gate, or by hand. | 2026-11-28 | [Section 3.2](#32-the-rule-oq-tpf1-d-tpf3), [Section 4.3](#43-how-the-disabling-commit-reaches-the-repository-d-tpf9), [Section 4.4](#44-re-enabling-a-test-one-pass-after-the-fix-oq-tpf3-d-tpf4), [Section 8](#8-build-slices) | No |
| <a id="D-TPF10"></a>`D-TPF10` | **Designer's decision; the owner may overturn this (revised 2026-11-28 from operator request).** The disabled list is one JSON file at the path in `.tillr.toml` `[gate] disabled_tests` (default `tillr-disabled-tests.json`; here `tools/gate/disabled-tests.json`), holding per entry the selector, `source` (`trunk-proof` or `hand`), and optionally `failed_on` and `first_line`, and no queue item id. Whole-check store markers move into it and the store's `disabled/` and `resolved/` retire. A tree check validates it; `tillr-gate disabled-args` scopes `-skip` per Go package (`--module <module> --packages` and `--module <module> --package <package-import-path>`, joining listed top-level and subtest selectors) and `.justfile`'s `test` recipe partitions `go list ./...` with it so `just check-ci` never skips a same-named test in another package; and lane gates exclude listed tests and take the union of the gated worktree's list and `refs/heads/<trunk>`'s list in `tillr-gate go-affected` (including `ComputePackageVerdictDigest` for `-cfix` verdict reuse), rather than in `tillr-gate shards`. | 2026-11-28 | [Section 2.2](#22-the-disabled-list-is-a-tracked-file-d-tpf10), [Section 2.3](#23-excluding-listed-tests-in-tillr-gate-shards), [Section 2.4](#24-excluding-listed-tests-in-lane-gates-and-direct-go-test-runs-d-tpf10), [Section 8](#8-build-slices) | No |
| <a id="D-TPF11"></a>`D-TPF11` | **Designer's decision; the owner may overturn this (revised 2026-11-28 from operator request).** `tillr item sync` reads the list at `HEAD` and files one mechanic job per sync for every selector no open item's structured test selection names; once Slice 5 lands, the proof files its job before its list commit so sync never queues it twice, and any entries Slice 4 added ahead of Slice 5 are covered once Slices 5 and 6 land. The push hold is `[push] hold_while_disabled`, default `false`, holding only the push. | 2026-11-28 | [Section 4.2](#42-tillr-item-sync-queues-a-repair-for-every-uncovered-entry-d-tpf11), [Section 5.2](#52-the-optional-push-hold-d-tpf11), [Section 8](#8-build-slices) | No |
