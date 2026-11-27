---
title: "Catching same-tool documentation and contract regressions in the pre-merge gate without undoing drain proof deferral"
status: in-review
stage: DESIGN
updated: 2026-11-27
tags:
  - tillr
  - drain
  - gate
  - go-affected
  - testing
  - design
summary: "Reads the pre-merge gate verdicts and go-affected logs for the five 2026-11-27 landings that passed the fast gate and failed the trunk proof in the same tool they changed (showing all five failed checks/tests were outside the affected set rather than skipped or broken by merge), proposes moving four fast contract tests out of deferred tillr-drain/drain and wiring static tillrdocs checks into go-affected, states measured standalone and critical-path gate costs, checks the options against the landing principles and standing rulings, and puts the gate-policy choice to the owner in OQ-FGM1."
next: "The owner rules on OQ-FGM1 to choose Option A, B, C, D, or E."
depends-on:
  - landing-principles.md
  - landing-principles.md#1-a-refusal-must-be-a-verdict
  - landing-principles.md#2-re-application-preserves-the-change-not-the-patch
  - landing-principles.md#3-gate-what-will-be-the-tip-once
  - landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it
  - landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran
  - landing-principles.md#15-land-the-smallest-functional-change
  - fast-gate-tillr-drain-options.md
  - fast-gate-tillr-drain-options.md#OQ-DFG1
  - ../reference/gate.md
  - ../reference/gate.md#D-GR1
  - ../reference/gate.md#D-GR2
  - ../reference/gate.md#D-GR5
  - ../reference/gate.md#D-GR38
  - ../reference/gate.md#D-GR39
  - ../reference/gate.md#D-GR53
  - ../reference/gate.md#D-GR54
  - ../reference/gate.md#D-GR56
  - tillr-graph-is-the-path.md
  - ../tillr/decision-provenance-review.md
  - ../reference/authority-not-approval.md
related:
  - trunk-repair-on-moving-trunk.md
  - tillr-drain-package-decomposition.md
vantage:
  status-chip: true
---

# Catching same-tool documentation and contract regressions in the pre-merge gate without undoing drain proof deferral

**Status:** Open for the owner's decision in [`OQ-FGM1`](#OQ-FGM1); **this document settles nothing itself**. Every root-cause audit, option, timing measurement, ruling check, and leaning below is recorded from an operator request as **the designer's analysis and leaning, not an owner ruling** ([decision provenance review](../tillr/decision-provenance-review.md#6-how-this-stops-happening), [`OQ-PR21` and `OQ-PR22`](../tillr/decision-provenance-review.md#decision-ledger)); the owner may overturn any part of it. No Go code or gate configuration is changed in this commit.

> **In short.** On **2026-11-27** between `01:37` and `06:45` (`-05:00`), five landings passed the pre-merge gate (`repo-gate change-gate`) and were then named by the after-merge trunk proof as the first red commit on tests in the same tool they changed, holding `refs/tillr/green` back from publishing for more than half that window. Reading the recorded `gate.verdict` event, `gate.fast.verdict.json`, `go-affected` log, and `gated_tree` for each of the five shows the same answer in **all five cases**: the check or test that later failed was **outside the affected set** planned before the merge (`0` of `5` were selected and skipped; `0` of `5` passed on `gated_tree` and broke only on `landed_tree`). Two narrow gaps explain all five: (1) `CheckRunEnvironmentDocumented` in [`tools/tillr/tillrdocs/tillrdocs.go`](../../tools/tillr/tillrdocs/tillrdocs.go) runs only inside heavy tree check `tillr-docs` and has neither a unit test in `tillrdocs_test.go` nor a [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json) entry; and (2) four fast AST, CLI, and README contract tests (`0.06 s`–`6.04 s`) sit inside `github.com/mschulkind-oss/tillr-drain/drain`, which `DeferredProofPackages` drops unconditionally under [`OQ-DFG1`](./fast-gate-tillr-drain-options.md#OQ-DFG1) ([`D-GR56`](../reference/gate.md#D-GR56)) even though root `github.com/mschulkind-oss/tillr-drain` already ran in `go-affected` on all four `tillr-drain` candidates. Wiring static `tillrdocs` contract tests into `go-affected` and moving those four contract tests to root `tools/tillr-drain` catches all five before merge at **`~0 s` critical-path wall cost** on multi-package candidates (`+0.7 s` to `+7.0 s` on narrow single-target edits) while keeping `~2,076` slow `drain` integration tests after the merge.

- **Start at:** [Section 1](#1-gate-records-for-the-five-2026-11-27-landings) for the gate's own record on each of the five candidates, [Section 2](#2-two-structural-gaps-and-the-options-to-close-them) for the two gaps and the options, [Section 3](#3-measured-gate-time-cost-and-landing-principles-check) for measured gate-time costs and the check against [the landing principles](./landing-principles.md), and [`OQ-FGM1`](#OQ-FGM1) for the owner's gate-policy choice.
- **Needs your ruling:** [`OQ-FGM1`](#OQ-FGM1).
- **Reads with:** [landing principles](./landing-principles.md), [fast-gate options for `tillr-drain`](./fast-gate-tillr-drain-options.md) ([`OQ-DFG1`](./fast-gate-tillr-drain-options.md#OQ-DFG1)), [the gate reference](../reference/gate.md) ([Affected Go tests before the merge](../reference/gate.md#affected-go-tests-before-the-merge)), and [repairing a red trunk proof on a moving trunk](./trunk-repair-on-moving-trunk.md).

---

## 1. Gate records for the five 2026-11-27 landings

### 1.1 Summary of the three-way classification

Each landing's pre-merge gate record (`gate.verdict` event, `gate.fast.verdict.json`, and `go-affected` package execution log) and post-merge attribution can be reproduced with `tillr-view heavy-misses` and `tillr-gate go-affected --select-only`:

```sh
go run -C tools/tillr-view . heavy-misses \
  --store "$STORE_DB" \
  --root "$REPO_ROOT" \
  --from 2026-11-27T06:00:00Z \
  --to 2026-11-27T12:30:00Z

go run -C tools/tillr-gate . go-affected \
  --root "$REPO_ROOT" \
  --base <base_sha> \
  --head <landed_sha> \
  --select-only
```

Across all five landings, comparing the candidate's `gated_tree`, `landed_tree`, `gate.fast.verdict.json` check list, and `go-affected` selected-package list answers the three possibilities posed in the request:

| # | Landed commit (`ET` / `UTC`) | Failing proof check / test | Gate wall (`go-affected` pkgs, `slot_wait`) | Classification from gate record | Already broken in `gated_tree`? |
|---|---|---|---|---|---|
| 1 | `a973645a6742` (`01:39:24` / `06:39:24Z`) | `tillr-docs` (`CheckRunEnvironmentDocumented`) | `103.1 s` (`26` pkgs, `0 ms`) | **Outside the affected set** | Yes (`f8b773f02238`) |
| 2 | `f9f5129a6698` (`02:27:08` / `07:27:08Z`) | `TestAllDefinedHoldReasonsAreClassified`, `TestEveryReasonConstantHasAHoldRule` | `134.6 s` (`26` pkgs, `0 ms`) | **Outside the affected set** | Yes (`e310a9d27966`) |
| 3 | `66f173c094a8` (`04:48:50` / `09:48:50Z`) | `TestRecordedVerdictExampleREADME` | `96.0 s` (`12` pkgs, `0 ms`) | **Outside the affected set** | Yes (`gated_tree == landed_tree`) |
| 4 | `f084c4c9de6a` (`05:30:30` / `10:30:30Z`) | `TestRecordedVerdictExampleREADME` | `67.1 s` (`27` pkgs, `0 ms`) | **Outside the affected set** | Yes (`gated_tree == landed_tree`) |
| 5 | `615c50a4bced` (`06:29:07` / `11:29:07Z`) | `TestAllDefinedHoldReasonsAreClassified`, `TestWhyEmptyStoreAndMissingStore` | `128.7 s` (`39` pkgs, `0 ms`) | **Outside the affected set** | Yes (`c6c1b7d72c7b`) |

- **Outside the affected set (`5` of `5`):** Every failing check or test was excluded at selection time before execution began — Landing 1 because `tillr-docs` is a `tier: "heavy"` tree check with no `go-affected` test or edge on `github.com/mschulkind-oss/tillr/tillrdocs`, and Landings 2–5 because `DeferredProofPackages` in [`tools/tillr-gate/goaffected/select.go`](../../tools/tillr-gate/goaffected/select.go) drops `github.com/mschulkind-oss/tillr-drain/drain` unconditionally.
- **Selected but not run (`0` of `5`):** Every check planned in `gate.fast.verdict.json` (`20` checks per candidate) and every Go package selected by `go-affected` ran to completion and passed (`2` unchanged packages hit edge-keyed cache on Landing 5; `0` hit cache on Landings 1–4).
- **Passed at the gate and failed only on the merged tip (`0` of `5`):** Landings 3 and 4 landed with `base_sha == trunk_before` and `gated_tree == landed_tree`, so no concurrent merge occurred. On Landings 1, 2, and 5, `git show <gated_tree>:<path>` confirms the exact mismatch was already committed on the candidate's `gated_tree` before `Trunk.Squash` replayed it onto `trunk_before`.

### 1.2 Landing 1 — `a973645a6742` (`"add native backend slice 2 fenced file, search, and progress tools"`)

- **Gate and landing record:** Landed at `2026-11-27T06:39:24Z` (`01:39:24-05:00`), `event_seq: 100756`, `gate_event_seq: 100724`, `verdict_id: 57021af4b56978aa1516c63f3d099a7d07ac1771781cc1e3f34a2d26e9df7b79`, `base_sha: 75ba88aa636f`, `gated_tree: f8b773f02238`, `trunk_before: 78c28e3a07b4`, `landed_tree: 51879ff8438e`. Gate wall time was `103,140 ms` (`20` checks); `go-affected` ran `26` packages (`0` cached, `selection: 2,194 ms`, `slot_wait: 0 ms`, `102,951 ms` wall), led by `tillr-view` (`142.28 s` summed across shards), `tillr-run` (`78.67 s`), and `tillr-run/run` (`71.53 s`).
- **What broke and why it was already broken on `gated_tree`:** The candidate edited `18` paths across `tools/tillr-run` and `tools/tillr-drain` (plus pack mirrors), adding `TILLR_ACCESS`, `TILLR_SEAT`, and `TILLR_OUTPUTS_DIR` to the child environment in `tools/tillr-run/run/exec.go` without listing them in `tools/tillr-run/README.md` (already absent in `gated_tree` `f8b773f02238`; repaired in `2d65e5f7eede`). After merge, `tillr-docs` failed in `CheckRunEnvironmentDocumented` (`tools/tillr/tillrdocs/tillrdocs.go`).
- **Why the pre-merge gate did not run it (`outside the affected set`):**
  1. [`tools/gate/checks/tillr-docs.json`](../../tools/gate/checks/tillr-docs.json) declares `"scope": "tree", "tier": "heavy"`, so `cmdChangeGate` in [`tools/gate/change_gate.go`](../../tools/gate/change_gate.go) (`tillr-gate checks --fast-tree --scope-to-change`) excludes `tillr-docs` from the pre-merge plan (`FilterChecks` in [`tools/tillr-gate/checks.go`](../../tools/tillr-gate/checks.go) admits only `tier == "fast"` tree checks plus `scope == "change"` checks).
  2. `github.com/mschulkind-oss/tillr/tillrdocs` has no entry in [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json), so changes to `tools/tillr-run/run/exec.go` or `tools/tillr-run/README.md` do not select `tillr/tillrdocs` in `go-affected` — and [`tools/tillr/tillrdocs/tillrdocs_test.go`](../../tools/tillr/tillrdocs/tillrdocs_test.go) did not call `CheckRunEnvironmentDocumented` at all (only `tillrdocs.Main` via `cmd/tillr-docs-check` called it).

### 1.3 Landing 2 — `f9f5129a6698` (`"mirror roadmap-linked designs into item-design-<slug> rows"`)

- **Gate and landing record:** Landed at `2026-11-27T07:27:08Z` (`02:27:08-05:00`), `event_seq: 104105`, `gate_event_seq: 104094`, `verdict_id: 9d3692c790022e740b35d6adf0c7123ebc4d9074c2259077a1f8e1982bfed815`, `base_sha: ffa93fc35003`, `gated_tree: e310a9d27966`, `trunk_before: 22fb36d4cd71`, `landed_tree: 79017391befe`. Gate wall time was `134,596 ms` (`20` checks); `go-affected` ran `26` packages (`0` cached, `selection: 4,832 ms`, `slot_wait: 0 ms`, `134,044 ms` wall), led by `tillr-view` (`297.77 s` summed), `tillr-item` (`279.68 s`), and `tillr-land` (`220.89 s`), while also running `tillr-drain`, `tillr-drain/holdrules` (`0.16 s`), and `tillr-drain/internal/drainmodel` (`1.15 s`).
- **What broke and why it was already broken on `gated_tree`:** In `gated_tree` `e310a9d27966`, the candidate added `ReasonAwaitingOQQM2` (`"awaiting-oq-qm2"`) and `ReasonAwaitingOQQM3` (`"awaiting-oq-qm3"`) to `tools/tillr-drain/internal/drainmodel/constants.go`, `tools/tillr-drain/drain/model_aliases.go`, and `tools/tillr-drain/holdrules/holdrules.go`, omitting `generalHolds` in [`tools/tillr-drain/drain/definition.go`](../../tools/tillr-drain/drain/definition.go). While `go-test-tillr-drain` remained unproved across four deferred proofs ([analyzed separately](./trunk-repair-on-moving-trunk.md)), commit `a423ccb3ff2a` (`03:26:45-05:00`) also added `ReasonSplitCallDesign` in `tools/tillr-drain/drain/split.go` without updating `definition.go` or `holdrules.go`, so the kept proof log failed both `TestAllDefinedHoldReasonsAreClassified` (`0.72 s`, naming `awaiting-oq-qm2`, `awaiting-oq-qm3`, and `split-call-design`) and `TestEveryReasonConstantHasAHoldRule` (`0.17 s`, naming `ReasonSplitCallDesign`), with bisection naming `f9f5129a6698` as first red (repaired in `f23e52a000f6` and `47ac93bc5ce0`).
- **Why the pre-merge gate did not run them (`outside the affected set`):** `TestAllDefinedHoldReasonsAreClassified` (`tools/tillr-drain/drain/definition_test.go`) and `TestEveryReasonConstantHasAHoldRule` (`tools/tillr-drain/drain/hold_rules_test.go`) both reside in `github.com/mschulkind-oss/tillr-drain/drain`. `SelectAffectedPackagesWithDrop` in [`tools/tillr-gate/goaffected/select.go`](../../tools/tillr-gate/goaffected/select.go) seeds `dropPkgSet` from `DeferredProofPackages` (`{"github.com/mschulkind-oss/tillr-drain/drain"}`, [`D-GR54`](../reference/gate.md#D-GR54) / [`D-GR56`](../reference/gate.md#D-GR56)) and skips every package in `dropPkgSet` when emitting `selected`, even though `tillr-view` ran `TestWaitingEveryDrainReasonHasARule` (`0.40 s`) in the same `go-affected` pass via its `reads` edge on `tools/tillr-drain/`.

### 1.4 Landing 3 — `66f173c094a8` (`"enable item-owned worktrees in drain faces"`)

- **Gate and landing record:** Landed at `2026-11-27T09:48:50Z` (`04:48:50-05:00`), `event_seq: 108644`, `gate_event_seq: 108642`, `verdict_id: 932d01397b8bb0a60a68e7d9e99498c5b7e0c55ca0e8886b0e80c09df1158176`, `base_sha == trunk_before == 6519263769f8`, `gated_tree == landed_tree == 36415fb5b584`. Gate wall time was `96,033 ms` (`20` checks); `go-affected` ran `12` packages (`0` cached, `selection: 1,055 ms`, `slot_wait: 0 ms`, `95,859 ms` wall), led by `tillr-gate/goaffected` (`75.54 s`) and `tillr-view` (`72.67 s`), with root `github.com/mschulkind-oss/tillr-drain` finishing in `20.42 s`.
- **What broke and why it was already broken on `gated_tree`:** Enabling item-owned worktrees in `tools/tillr-drain/main.go`, `why.go`, `recovery.go`, and `drain/loop.go` broke `TestRecordedVerdictExampleREADME` (`6.04 s` in `tools/tillr-drain/drain/recorded_verdict_example_test.go`, `package drain_test`), which still expected pooled lane directories rather than `<lane-root>/<item-id>/work` (repaired in `e1fff6208059`). Because `gated_tree == landed_tree`, the merged tip was identical to the gated candidate tree.
- **Why the pre-merge gate did not run it (`outside the affected set`):** `TestRecordedVerdictExampleREADME` sits in `github.com/mschulkind-oss/tillr-drain/drain`. Although [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json) retains `build_tests` entries naming `TestRecordedVerdictExampleREADME` on `github.com/mschulkind-oss/tillr-drain/drain` so after-merge `go-test-tillr-drain` shard caching can key on its inputs ([`D-GR54`](../reference/gate.md#D-GR54)), `SelectAffectedPackagesWithDrop` skips `dropPkgSet["github.com/mschulkind-oss/tillr-drain/drain"]` during pre-merge selection.

### 1.5 Landing 4 — `f084c4c9de6a` (`"default lane root to .tillr/items and mark slice 2 built"`)

- **Gate and landing record:** Landed at `2026-11-27T10:30:30Z` (`05:30:30-05:00`), `event_seq: 109158`, `gate_event_seq: 109156`, `verdict_id: 60c145e8da516f6352264567cb6bc0b90e28d3cffb2d67d1d8ea00b0778230ac`, `base_sha == trunk_before == e1fff6208059`, `gated_tree == landed_tree == 41fde244324d`. Gate wall time was `67,145 ms` (`20` checks); `go-affected` ran `27` packages (`0` cached, `selection: 1,231 ms`, `slot_wait: 0 ms`, `66,886 ms` wall), led by `tillr-view` (`225.03 s` summed) and `tillr-gate/shards` (`58.67 s`), with root `github.com/mschulkind-oss/tillr-drain` finishing in `28.92 s` (including the new `lane_root_default_test.go` in root `tools/tillr-drain/`).
- **What broke and why it was already broken on `gated_tree`:** Changing the default `--lane-root` in `tools/tillr-drain/main.go` and `recovery.go` from `.tillr/lanes` to `.tillr/items` broke `TestRecordedVerdictExampleREADME` (`5.04 s`) a second time because `e1fff6208059` had hardcoded `.tillr/lanes/<item>/work` (repaired in `b8594c1d6e23`). Again `gated_tree == landed_tree`.
- **Why the pre-merge gate did not run it (`outside the affected set`):** Identical to Landing 3 — `TestRecordedVerdictExampleREADME` sat inside deferred `github.com/mschulkind-oss/tillr-drain/drain` while the new default-lane-root face tests ran next door in root `github.com/mschulkind-oss/tillr-drain`.

### 1.6 Landing 5 — `615c50a4bced` (`"bound idle item worktrees and scratch by count and disk"`)

- **Gate and landing record:** Landed at `2026-11-27T11:29:07Z` (`06:29:07-05:00`), `event_seq: 109810`, `gate_event_seq: 109808`, `verdict_id: 563f20dbcb66f0906090c28b49d8b4248293960a245cbbac8169548f0d142062`, `base_sha: dbfbbab7745e`, `gated_tree: c6c1b7d72c7b`, `trunk_before: ef174bc3972e`, `landed_tree: 275be0bfdde6`. Gate wall time was `128,682 ms` (`20` checks); `go-affected` ran `39` packages (`2` cached, `selection: 1,939 ms`, `slot_wait: 0 ms`, `128,463 ms` wall), led by `tillr-view` (`254.06 s` summed), `tillr-land` (`212.23 s`), and `tillr-gate/goaffected` (`95.81 s`), while also running root `github.com/mschulkind-oss/tillr-drain`, `tillr-drain/holdrules`, and `tillr-drain/internal/worktreebudget`.
- **What broke and why it was already broken on `gated_tree`:** In `gated_tree` `c6c1b7d72c7b`, the candidate added `ReasonWorktreeBound` to `internal/drainmodel/constants.go`, `drain/model_aliases.go`, and `holdrules/holdrules.go` without adding it to `generalHolds` in `drain/definition.go`, and added the `worktrees:` summary line to `tools/tillr-drain/why.go` and `drain/why.go` without updating `TestWhyEmptyStoreAndMissingStore` in `tools/tillr-drain/drain/why_cli_test.go` (`package drain_test`). The trunk proof failed `TestAllDefinedHoldReasonsAreClassified` (`0.75 s`) and `TestWhyEmptyStoreAndMissingStore` (`0.06 s`), and was later repaired on `main` in `895c6cb31b66` `[unlanded]`.
- **Why the pre-merge gate did not run them (`outside the affected set`):** Both failing tests live in `github.com/mschulkind-oss/tillr-drain/drain` and were dropped by `DeferredProofPackages`, even though root `github.com/mschulkind-oss/tillr-drain` ran `why_worktree_status_test.go` against the same `tillr-drain why` output in the pre-merge gate.

---

## 2. Two structural gaps and the options to close them

### 2.1 Why the misses happened without involving slow `drain` integration suites

On 2026-11-25, [`OQ-DFG1`](./fast-gate-tillr-drain-options.md#OQ-DFG1) ([`D-GR56`](../reference/gate.md#D-GR56)) deferred `github.com/mschulkind-oss/tillr-drain/drain` (`~2,080` tests, `~4,800 s` summed test wall time) from pre-merge `go-affected` to after-merge `go-test-tillr-drain`, with the owner noting: `"we'll collect more info and make another deciison in the future."` None of the five 2026-11-27 misses required those `~2,000` subprocess integration tests:

1. **Gap 1 — Zero-build `tillrdocs` contract checks are bundled with binary `--help` suites and unwired from `go-affected`.** In [`tools/tillr/tillrdocs/tillrdocs.go`](../../tools/tillr/tillrdocs/tillrdocs.go), `Main` runs `14` `Check*` functions alongside `RunFacesLane` (`8` binary builds + `121` `--help` runs). Thirteen of those `14` `Check*` functions (`CheckRunEnvironmentDocumented`, `CheckRefusedPushDocumented`, `CheckWorkflowJudgeDocumented`, `CheckRunTimingAndScheduleDocumented`, `CheckSeedRetirementDocumented`, `CheckWorkerRefusalSessionDocumented`, `CheckCacheSafeWorkingLoopDocumented`, `CheckDocumentFedPrerequisiteDocumented`, `CheckCommentIntakeDocumented`, `CheckRecovery`, `CheckRollingEnabled`, `CheckCallout`, and `CheckNoFlagTable`, plus the static file checks in `CheckDesignerSessionSelectionDocumented` before its `BuildModule("tillr-run", ...)` call) are pure file and text checks with zero subprocess builds, yet `tillrdocs_test.go` does not test them and `go-test-edges.json` has no `github.com/mschulkind-oss/tillr/tillrdocs` entry.
2. **Gap 2 — Four fast static-AST, CLI-smoke, and README-contract tests sit inside deferred `tillr-drain/drain` instead of root `tools/tillr-drain`.** Root `github.com/mschulkind-oss/tillr-drain` (`tools/tillr-drain/`) is **not** deferred: it already runs in `go-affected` whenever `tillr-drain` or its `drain`/`holdrules`/`internal/*` dependencies change, builds `tillr-drain` once in `TestMain` ([`tools/tillr-drain/main_test.go`](../../tools/tillr-drain/main_test.go)), and already runs `TestArchitecture` (`tools/tillr-drain/architecture_test.go`) on every edit under `tools/tillr-drain/` via `"read_tests": {"tools/tillr-drain/": ["TestArchitecture"]}` in [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json). Meanwhile:
   - `TestEveryReasonConstantHasAHoldRule` (`0.17 s`) imports only `tillr-drain/holdrules` and standard-library `go/ast`/`go/parser`, using zero `package drain` symbols;
   - `TestAllDefinedHoldReasonsAreClassified` (`0.75 s`) parses `*.go` ASTs and touches `package drain` only to check membership in `landingHolds` and `generalHolds` in `drain/definition.go`;
   - `TestWhyEmptyStoreAndMissingStore` (`0.06 s`) and `TestRecordedVerdictExampleREADME` (`5.0–6.0 s`) are black-box `package drain_test` CLI tests with zero unexported `drain` dependencies.

### 2.2 Designer options compared (not owner rulings)

1. **Option A — Wire static `tillrdocs` contract tests into `go-affected` and move the four fast `drain` contract tests to root `tools/tillr-drain` (smallest change within existing `go-affected` rules).**
   - **Static `tillrdocs` contracts:** Add `TestRunEnvironmentDocumented` (and targeted unit tests for the other `12` zero-build `Check*` functions in `tillrdocs.go`, keeping `CheckDesignerSessionSelectionDocumented`'s `tillr-run` binary build and `TestFacesHelpFixtureError`'s `8`-binary build out of those read-scoped tests) to [`tools/tillr/tillrdocs/tillrdocs_test.go`](../../tools/tillr/tillrdocs/tillrdocs_test.go). Register `github.com/mschulkind-oss/tillr/tillrdocs` in [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json) with narrow `reads` and `read_tests` (mapping `tools/tillr-run/README.md` and `tools/tillr-run/run/exec.go` to `TestRunEnvironmentDocumented`, and each remaining static check to the specific files it reads, updating [`tools/gate/checks/go-affected.json`](../../tools/gate/checks/go-affected.json) `reads` while respecting `TestCheckDeclarationsCoverSelectionInputs`'s exclusion of root `README.md` and `yolo-packs/tillr/files/local/share/tillr/`).
   - **Fast `drain` contracts:** Move `TestEveryReasonConstantHasAHoldRule`, `TestAllDefinedHoldReasonsAreClassified` (via a narrow read-only `IsClassifiedHoldReason(name string) bool` helper in `drain/definition.go` so `landingHolds` and `generalHolds` stay unexported), `TestWhyEmptyStoreAndMissingStore`, and `TestRecordedVerdictExampleREADME` from `tools/tillr-drain/drain/` into root `tools/tillr-drain/` (`CheckDrainTestParity` in [`tools/tillr-gate/goaffected/parity.go`](../../tools/tillr-gate/goaffected/parity.go) counts `Test*` declarations across all of `tools/tillr-drain/`, so moving `Test*` functions between `tools/tillr-drain/drain/` and `tools/tillr-drain/` preserves parity). On `github.com/mschulkind-oss/tillr-drain` in `go-test-edges.json`, add `TestAllDefinedHoldReasonsAreClassified` and `TestEveryReasonConstantHasAHoldRule` to `"read_tests": {"tools/tillr-drain/": [...]}` alongside `TestArchitecture`, and attach `TestRecordedVerdictExampleREADME`'s `reads`/`read_tests` (`tools/tillr/examples/`, `tools/tillr-run/backends/brief-template.md`) and `builds`/`build_tests` (`tillr-store`, `tillr-view`, `tillr-cli`).
2. **Option B — Add a pre-merge test allowlist for `DeferredProofPackages` in `tillr-gate/goaffected` + the same `tillrdocs` `go-affected` test and edge.** Keep all four `drain` tests inside `tools/tillr-drain/drain/` without moving files or exporting a helper on `drain`, and teach `SelectAffectedPackagesWithDrop` ([`tools/tillr-gate/goaffected/select.go`](../../tools/tillr-gate/goaffected/select.go)) to narrow `github.com/mschulkind-oss/tillr-drain/drain` to a declared `DeferredPackagePreMergeTests` allowlist (`TestAllDefinedHoldReasonsAreClassified`, `TestEveryReasonConstantHasAHoldRule`, `TestWhyEmptyStoreAndMissingStore`, `TestRecordedVerdictExampleREADME`) instead of dropping the package outright. Leaves `~2,076` `drain` tests after the merge, but pays `drain.test`'s `828`-file link plus `drain`'s `TestMain` builds of `tillr-run` and `tillr-gate` (`committed_events_main_test.go`) on every `drain`-touching gate and amends [`D-GR54`](../reference/gate.md#D-GR54)'s selection rules.
3. **Option C — Declare a separate `scope: "change", tier: "heavy"` static-docs check (`tillr-docs-static.json`) + Option A or B for the `drain` tests.** Note that adding a `tier: "fast"` check is blocked unless existing `budget_ms` allocations are trimmed: the `14` existing `tier: "fast"` declarations in `tools/gate/checks/*.json` already sum to `9,000 ms`, matching `LimitMS = 9000` in [`tools/gate/gate_budget.go`](../../tools/gate/gate_budget.go) ([`D-GR1`](../reference/gate.md#D-GR1), [`D-GR39`](../reference/gate.md#D-GR39)). A `scope: "change", tier: "heavy"` check avoids `LimitMS`, but duplicates path-filtering and edge-digest caching that `go-affected` already performs.
4. **Option D — Broaden pre-merge suites (move all of `why_cli_test.go` or un-defer `tillr-drain/drain` / `tillr-docs` wholesale).** Overturns [`OQ-DFG1`](./fast-gate-tillr-drain-options.md#OQ-DFG1) ([`D-GR53`](../reference/gate.md#D-GR53), [`D-GR56`](../reference/gate.md#D-GR56)) and pays multi-minute gate latency (`116.3 s` just for `^TestWhy` in `why_cli_test.go`; `233.6 s` across `6` shards for `tillr-drain/drain`) to catch defects that `6 s` of contract checks already catch.

---

## 3. Measured gate-time cost and landing-principles check

### 3.1 Measured gate-time cost on this host (2026-11-27)

Measured on **2026-11-27** on `HEAD` (`615c50a4bced`) with `-count=1` and a warm Go build cache:

| Command / slice | Measured `go test` / tool time | Measured process wall time | Notes |
|---|---|---|---|
| `go test -count=1 -C tools/tillr ./tillrdocs -run '^TestCheckRollingEnabled$'` | `0.48 s` | `739 ms` | Representative single static `tillrdocs` check (`0` binary builds) |
| `go test -count=1 -C tools/tillr ./tillrdocs` (`5` non-build unit tests) | `0.44 s` | `696 ms` | Whole static `tillrdocs` slice excluding `TestFacesHelpFixtureError` |
| `go run -C tools/tillr ./cmd/tillr-docs-check` | `3.04 s` | `3,038 ms` | Full `14` checks + `8` binary builds + `121` `--help` calls (warm cache) |
| `go test -count=1 -C tools/tillr-drain . -run '^TestArchitecture$'` | `1.48 s` | `2,511 ms` | Current `read_tests` baseline on root `tillr-drain` (includes `TestMain` build) |
| `go test -count=1 -C tools/tillr-drain .` (whole root package today) | `27.68 s` | `28,718 ms` | Already ran in `go-affected` on Landings 2, 3, 4, and 5 (`20.4 s`–`34.6 s`) |
| `TestEveryReasonConstantHasAHoldRule` + `TestAllDefinedHoldReasonsAreClassified` in `./drain` | `1.67 s` | `3,500 ms` | Test bodies take `0.17 s + 0.75 s = 0.92 s`; rest is `drain.test` link + `TestMain` |
| `TestWhyEmptyStoreAndMissingStore` alone in `./drain` | `3.09 s` | `5,034 ms` | Test body takes `0.06 s`; rest is `drain.test` link + `TestMain` + `drainBinOnce` |
| `TestRecordedVerdictExampleREADME` alone in `./drain` | `12.34 s` | `15,891 ms` | `5.04 s`–`6.04 s` inside a suite; builds `store`, `item`, `run`, `view`, `cli`, `drain` |
| All `4` missed `drain` tests together in `./drain` (**Option B** slice, `-count=1`) | `14.96 s` | `17,129 ms` | Standalone cost when `tillr-drain/drain` runs only the `4`-test allowlist |
| `go test -count=1 -C tools/tillr-drain ./drain -run '^TestWhy'` (**Option D** `why_cli_test.go`) | `114.71 s` | `116,346 ms` | Shows why moving all of `why_cli_test.go` instead of the smoke contract is costly |

What those measurements imply for candidate gate wall time:

- **Multi-package candidates like all five 2026-11-27 landings (`67.1 s`–`134.6 s` recorded wall, `slot_wait: 0 ms`):** **`~0 s` added on the critical path** under both **Option A** and **Option B**. On Landings 1–5, `go-affected` had zero worker-slot queueing (`slot_wait: 0 ms`) and its wall clock was set by `tillr-view` (`72.7 s`–`297.8 s` summed across shards) or `tillr-gate/goaffected` (`75.5 s`–`95.8 s`). Under Option A, root `tillr-drain` grows from `~28.7 s` to `~35 s` (`+0.92 s` for the two AST checks, `+0.06 s` for `TestWhyEmptyStoreAndMissingStore`, and `+5 s`–`6 s` for `TestRecordedVerdictExampleREADME` reusing `main_test.go`'s prebuilt `tillr-drain` binary), and `tillr/tillrdocs` runs in `0.74 s` on a parallel slot — both finishing `30 s`–`100 s` before the slowest package. Under Option B, the `17.1 s` `drain` allowlist run likewise finishes well inside the `67.1 s`–`134.6 s` critical path.
- **Narrow single-target candidates (where no slower consumer package is selected):**
  - Editing `tools/tillr-run/README.md` alone: **`+0.74 s` wall** (`tillr/tillrdocs -run ^TestRunEnvironmentDocumented$`), cached on subsequent runs when `TILLR_GO_TEST_EDGE_DIGEST` is unchanged.
  - Editing a `tools/tillr-drain` subpackage file that triggers root `tillr-drain` only via `"reads": ["tools/tillr-drain/"]` (`read_tests`): **`+0.9 s` wall** under Option A (`2.51 s` -> `~3.4 s` for `TestArchitecture` + the two AST reason checks), versus **`+17.1 s` wall** under Option B if all four allowlisted `drain` tests run together (or `+3.5 s` if split by edge).
  - Editing root `tools/tillr-drain` without triggering `tillr-view` or `tillr-land`: **`+6 s`–`7 s` wall** under Option A (`28.7 s` -> `~35 s`), remaining well below `fast-gate-median-slow` (`90 s`).

### 3.2 Check against [the landing principles](./landing-principles.md) and standing rulings

- **Trunk-activity polarity ([landing principles](./landing-principles.md) opening rule & [`D-GR2`](../reference/gate.md#D-GR2)):** Pre-merge `go-affected` runs inside each lane's isolated candidate gate with zero cross-lane lock ([`D-GR2`](../reference/gate.md#D-GR2)), so its cost does not grow with trunk landing rate. By contrast, letting same-tool contract regressions onto `main` triggers post-merge proof bisection, reverts, and fixer items whose delay compounds as `main` advances ([`OQ-TRM1`](./trunk-repair-on-moving-trunk.md#OQ-TRM1)), holding `refs/tillr/green` ([`D-GR5`](../reference/gate.md#D-GR5)) back for more than half of `01:37`–`06:45` on 2026-11-27.
- **[Landing Principles §1 (A refusal must be a verdict)](./landing-principles.md#1-a-refusal-must-be-a-verdict) and [§14 (Convict a run on what it did, not on what happened while it ran)](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran):** Running `TestRunEnvironmentDocumented`, `TestAllDefinedHoldReasonsAreClassified`, `TestEveryReasonConstantHasAHoldRule`, `TestWhyEmptyStoreAndMissingStore`, and `TestRecordedVerdictExampleREADME` on the candidate's own diff convicts the offending run in-lane with the exact failing test name so the worker fixes its own omission before merge.
- **[Landing Principles §2 (Re-application preserves the change, not the patch)](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch) and [§3 (Gate what will be the tip, once)](./landing-principles.md#3-gate-what-will-be-the-tip-once):** Leaves `tillr-land`'s `(base_sha, head_sha^{tree})` verdict binding and `Trunk.Squash` untouched; each candidate is still gated once on its lane tree.
- **[Landing Principles §12 (A principle cites the measurement that produced it)](./landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it) and [§15 (Land the smallest functional change)](./landing-principles.md#15-land-the-smallest-functional-change):** Promotes only the `5` fast contract checks (`0.06 s`–`6.04 s`) implicated by the 2026-11-27 proof failures, preserving [`OQ-DFG1`](./fast-gate-tillr-drain-options.md#OQ-DFG1) / [`D-GR56`](../reference/gate.md#D-GR56) for `tillr-drain/drain`'s `~2,076` subprocess integration tests and keeping `LimitMS = 9000` ([`D-GR1`](../reference/gate.md#D-GR1), [`D-GR38`](../reference/gate.md#D-GR38), [`D-GR39`](../reference/gate.md#D-GR39)) untouched. Because any change to which checks and `drain` tests run before the merge versus after the merge adjusts the gate boundary settled in [`OQ-DFG1`](./fast-gate-tillr-drain-options.md#OQ-DFG1) / [`D-GR56`](../reference/gate.md#D-GR56), [`OQ-FGM1`](#OQ-FGM1) puts that choice to the owner.

---

## Open Questions

1. 💬 **OQ-FGM1: Which pre-merge gate change, if any, should catch static `tillrdocs` and fast `tillr-drain` contract regressions before landing while keeping `tillr-drain/drain`'s slow integration suite after the merge?**

   On **2026-11-27** (`01:37`–`06:45`), five landings (`a973645a6742`, `f9f5129a6698`, `66f173c094a8`, `f084c4c9de6a`, and `615c50a4bced`) passed `repo-gate change-gate` and then failed the trunk proof on checks in the same tool they changed (`CheckRunEnvironmentDocumented` in `tillr-docs`, `TestAllDefinedHoldReasonsAreClassified`, `TestEveryReasonConstantHasAHoldRule`, `TestRecordedVerdictExampleREADME` twice, and `TestWhyEmptyStoreAndMissingStore`). In all five, the failing check or test was outside the pre-merge affected set. Every option and the leaning below are the designer's, from an operator request, not an owner ruling.

   - **A — Wire zero-build `tillrdocs` contract tests into `go-affected` (`go-test-edges.json`) and move the four fast `drain` contract tests to root `tools/tillr-drain` (`package main_test` / `package main`).** Adds unit tests for `CheckRunEnvironmentDocumented` (and the `12` other zero-build static `Check*` functions in `tillrdocs.go`) to `tools/tillr/tillrdocs/tillrdocs_test.go` with narrow `reads`/`read_tests` edges in `tools/gate/go-test-edges.json`, and moves `TestEveryReasonConstantHasAHoldRule`, `TestAllDefinedHoldReasonsAreClassified` (via a read-only `IsClassifiedHoldReason` helper on `drain`), `TestWhyEmptyStoreAndMissingStore`, and `TestRecordedVerdictExampleREADME` into non-deferred root `tools/tillr-drain` alongside `TestArchitecture` and `why_worktree_status_test.go`. Keeps `DeferredProofPackages` ([`D-GR54`](../reference/gate.md#D-GR54) / [`D-GR56`](../reference/gate.md#D-GR56)) and `LimitMS = 9000` ([`D-GR39`](../reference/gate.md#D-GR39)) unchanged; costs **`~0 s` on the critical path** of multi-package gates (`+0.74 s` on doc-only edits, `+0.9 s` on `read_tests`-only `tillr-drain` edits, `+6 s`–`7 s` on solo root `tillr-drain` edits).
   - **B — Keep the four `drain` tests inside `tools/tillr-drain/drain`, add a `DeferredPackagePreMergeTests` allowlist in `tillr-gate/goaffected`, and wire zero-build `tillrdocs` contract tests into `go-affected`.** Avoids moving test files or exporting `IsClassifiedHoldReason` on `drain`, at the cost of amending `SelectAffectedPackagesWithDrop` ([`D-GR54`](../reference/gate.md#D-GR54)) and paying `drain.test` link + `drain` `TestMain` binary builds (**`17.1 s` wall** standalone; **`~0 s` critical-path delta** when `tillr-view` or `tillr-land` also runs).
   - **C — Add a separate `scope: "change", tier: "heavy"` static-docs check (`tillr-docs-static.json`) paired with Option A or B for the four `drain` tests.** Runs static `tillrdocs` checks as a standalone change-scoped gate declaration outside `LimitMS = 9000` instead of through `go-affected` (`go-test-edges.json`).
   - **D — Move broader `drain` suites (`why_cli_test.go` at `116.3 s` wall, or full `tillr-drain/drain` at `233.6 s` across `6` shards) back into pre-merge `go-affected`.** Replaces [`OQ-DFG1`](./fast-gate-tillr-drain-options.md#OQ-DFG1) ([`D-GR56`](../reference/gate.md#D-GR56)) for broader pre-merge coverage at multi-minute gate latency.
   - **E — Keep current pre-merge selection unchanged ([`OQ-DFG1`](./fast-gate-tillr-drain-options.md#OQ-DFG1) Option C as-is) and rely on post-merge trunk repair ([`OQ-TRM1`](./trunk-repair-on-moving-trunk.md#OQ-TRM1)).** Adds zero pre-merge tests or edges, accepting brief red spells on `main` when same-tool contract checks fail after merge.

   <!-- vantage: question id=OQ-FGM1 leaning="A — catches all five 2026-11-27 failure modes in the candidate's own pre-merge gate using existing go-affected edge mechanics, leaves LimitMS = 9000 (D-GR39) and DeferredProofPackages (OQ-DFG1 / D-GR56) intact for the ~2,076 slow drain integration tests, adds ~0 s on multi-package critical paths (+0.74 s to +7 s on narrow single-target edits), and obeys Landing Principles §1, §2, §3, §12, §14, and §15." -->

   _Leaning (designer's leaning, not an owner ruling):_ **A** — catches all five 2026-11-27 failure modes in the candidate's own pre-merge gate using existing `go-affected` edge mechanics, leaves `LimitMS = 9000` ([`D-GR39`](../reference/gate.md#D-GR39)) and `DeferredProofPackages` ([`OQ-DFG1`](./fast-gate-tillr-drain-options.md#OQ-DFG1) / [`D-GR56`](../reference/gate.md#D-GR56)) intact for the `~2,076` slow `drain` integration tests, adds **`~0 s` on multi-package critical paths** (`+0.74 s` to `+7 s` on narrow single-target edits), and obeys [Landing Principles §1](./landing-principles.md#1-a-refusal-must-be-a-verdict), [§2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch), [§3](./landing-principles.md#3-gate-what-will-be-the-tip-once), [§12](./landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it), [§14](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran), and [§15](./landing-principles.md#15-land-the-smallest-functional-change). Choose **B** if you prefer keeping the four tests physically inside `tools/tillr-drain/drain/` via a `tillr-gate` allowlist despite the `17.1 s` standalone `./drain` startup cost.

   **Answer:**

   > _(empty — fill in when decided)_

---

## Decision Ledger

| ID | Decision | Provenance | Governing section | Built? |
|---|---|---|---|---|
| <a id="D-FGM1"></a>`D-FGM1` | **From an operator request; the designer's gate-record audit, timing measurements, and option framing, not an owner ruling (the owner may overturn any part of it, and no gate policy change is settled until the owner answers [`OQ-FGM1`](#OQ-FGM1)).** Record from each candidate's pre-merge gate verdict and `go-affected` log that all five 2026-11-27 proof misses (`a973645a6742`, `f9f5129a6698`, `66f173c094a8`, `f084c4c9de6a`, `615c50a4bced`) were outside the pre-merge affected set and already broken on `gated_tree`, measure the standalone and critical-path gate costs of Options **A**–**E** on `615c50a4bced`, and put the pre-merge policy choice to the owner in [`OQ-FGM1`](#OQ-FGM1) with no code or check-declaration edits in this commit. | 2026-11-27, designer, from an operator request | [Section 1](#1-gate-records-for-the-five-2026-11-27-landings), [Section 2](#2-two-structural-gaps-and-the-options-to-close-them), [Section 3](#3-measured-gate-time-cost-and-landing-principles-check), [`OQ-FGM1`](#OQ-FGM1) | Design only ([`OQ-FGM1`](#OQ-FGM1) open) |
