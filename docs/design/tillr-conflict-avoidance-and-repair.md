---
title: "Head off landing conflicts as early as possible, and repair each at the lowest cost"
author: "Agent"
date: 2026-11-25
updated: 2026-11-25
status: in-review
stage: DESIGN
stage-since: 2026-11-25
tags:
  - tillr
  - drain
  - gate
  - land
  - conflict
  - repair
  - design
summary: "On 2026-11-25 between 05:30 and 13:30 EST, 22 candidate landings ended in squash-conflict after 630.2 last-span gate-minutes (793.8 all-span gate-minutes), triggering 181.9 minutes of mostly cold-session -cfix repair runs ($11.51) and 413.3 minutes of full post-repair re-gates. Assuming built item eab5370855 (start lanes at trunk tip, 43455fc4dd) and filed item 347d54ef07 (abort unmergeable gates) and ordering Option 1 after 35c954f928 (tillr-green-trunk-worker-rebase.md, 7a09eeb863), this design weighs and ranks six conflict-avoidance and low-cost repair options by trigger fact, event or file carrier, cost per trunk move, and counterfactual savings on the 2026-11-25 record, checks each against the landing principles and OQ-GTC4, poses three owner questions (OQ-CAR1 through OQ-CAR3), and defines standalone build items."
next: "Build standalone items CAR-B1 (mechanical rebase and warm-session conflict repair), CAR-B2 (post-repair Go package verdict reuse), and CAR-B3 (pre-lane livePaths serialization); the owner rules OQ-CAR1 (splitting docs/design/tillr-utility-contracts.md), OQ-CAR2 (whether to add a gate-entry pairwise merge-tree hold), and OQ-CAR3 (advisory vs. mandatory worker rebase on path-overlap notices)."
depends-on:
  - landing-principles.md
  - landing-principles.md#2-re-application-preserves-the-change-not-the-patch
  - landing-principles.md#3-gate-what-will-be-the-tip-once
  - landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work
  - landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act
  - landing-principles.md#15-land-the-smallest-functional-change
  - gate-test-capacity.md
  - gate-test-capacity.md#OQ-GTC4
  - tillr-green-trunk-worker-rebase.md
  - tillr-shared-path-serialization.md
  - tillr-utility-contracts.md
related:
  - ../research/gate-batching.md
  - tillr-core-and-git-landing.md
  - tillr-change-scoped-checks.md
vantage:
  status-chip: true
---

# Head off landing conflicts as early as possible, and repair each at the lowest cost

**Status:** 2026-11-25, verified at `7a09eeb863d7`. Of the three assumed siblings, `item-request-eab5370855` is now built (`43455fc4dd` switched [`config/tillr/gate-policy.json`](../../config/tillr/gate-policy.json) to `"lane_base": "tip"` under [`D-GR55`](../reference/gate.md#D-GR55)), `item-request-35c954f928` has landed its design in [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md) (`7a09eeb863`, [`D-WR1`](./tillr-green-trunk-worker-rebase.md#D-WR1)–[`D-WR8`](./tillr-green-trunk-worker-rebase.md#D-WR8) and [`OQ-WR1`](./tillr-green-trunk-worker-rebase.md#OQ-WR1)–[`OQ-WR3`](./tillr-green-trunk-worker-rebase.md#OQ-WR3), unbuilt), and `item-request-347d54ef07` remains unbuilt. Nothing specified here is built yet: `conflictFix` in [`tools/tillr-drain/drain/conflict.go`](../../tools/tillr-drain/drain/conflict.go) still sets `fix.SessionTurn = false` on every textual conflict (`reason = "trunk-conflict"`) and still dispatches an agent `fix` run even when `moveLaneOntoTip` rebases cleanly (`moved != ""`); `rg -n 'TILLR_GO_VERDICTS_IN' tools/tillr-loop tools/tillr-drain` returns nothing; `rg -n 'ReasonPathHeld' tools/tillr-drain` returns nothing; and `rg -n 'trunk-overlap' tools/tillr-drain tools/tillr-run` returns nothing. Decisions [`D-CAR1`](#D-CAR1) through [`D-CAR8`](#D-CAR8) in [§7](#7-decision-ledger) are designer decisions made from operator request `item-request-8972f1b9b7` and may be overturned by the owner.

> **In short.** Once new lanes start from trunk tip (`item-request-eab5370855`, `43455fc4dd`) and unmergeable gates stop immediately (`item-request-347d54ef07`), `8` of the `22` conflicts from 2026-11-25 disappear outright and `572.6` of `630.2` pre-conflict gate-minutes are saved, leaving **`14` conflicts, `~211.6` pre-abort gate-minutes, `~115.8` cold-session `-cfix` agent-minutes, and `~214.6` post-repair re-gate minutes**. Ranked by leverage and trunk-activity cost, the owner should take **Option 2** first (mechanical `merge-tree` rebase with zero LLM turns when clean, plus warm-session `-cfix` resume on real textual conflicts), **Option 5** second (wire `go_affected.py`'s existing per-package digest cache across gates and repairs), **Option 4** third (pre-lane `livePaths` serialization from [`tillr-shared-path-serialization.md`](./tillr-shared-path-serialization.md) plus splitting [`tillr-utility-contracts.md`](./tillr-utility-contracts.md) per [`OQ-CAR1`](#OQ-CAR1)), and **Option 1** fourth (ordered after [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md) Slices 1–2 to deliver path-filtered `<lane>/trunk-overlap.json` turn-gap notices per [`OQ-CAR3`](#OQ-CAR3)). **Option 3** (pairwise in-flight `merge-tree`) is omitted or restricted to gate-entry leapfrog holds ([`OQ-CAR2`](#OQ-CAR2)), and **Option 6** as a global gate-order lock is rejected under [`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4) (`606b43306d`), with its lawful halves folded into Options 2 and 4.

**Why it matters.** On 2026-11-25 (`05:30`–`13:30 EST`), `22` `squash-conflict` landings burned `630.2` last-span gate-minutes (`793.8` all-span gate-minutes), `181.9` `-cfix` agent-minutes (`$11.51`, `27.7M` tokens, with `15` of `21` repair runs throwing away warm sessions), and `413.3` post-repair re-gate minutes — `1,225.4` lane and gate minutes in eight hours ([§1](#1-the-2026-11-25-conflict-record-measured)).

**The shape.** Make every unavoidable conflict near-free first (`Option 2` + `Option 5`, both $O(0)$ per trunk move), prevent known hot-file collisions before lane allocation (`Option 4`, $O(|\text{released paths}|)$ per landing), order path-filtered turn-gap overlap notices (`Option 1`) directly onto [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md)'s `tillr-run lane-rebase` and hook channel, and never re-introduce a global gate queue ([§3](#3-checked-against-the-landing-principles-and-gate-batching-ruling), [§4](#4-weighing-the-six-options), [§5](#5-ranking-and-recommended-package)).

**Cost.** Zero SQLite DDL changes and zero new event kinds required (`Option 1` adds one monotonic lane-local notice file `<lane>/trunk-overlap.json` alongside [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md)'s `<lane>/trunk-green.json` and an optional additive `changed_paths` array on `land.landing`). Three build items (`CAR-B1`–`CAR-B3`) are ready immediately; three (`CAR-B4`–`CAR-B6`) wait on [`OQ-CAR1`](#OQ-CAR1)–[`OQ-CAR3`](#OQ-CAR3) ([§8](#8-standalone-build-items)).

**Start at** [§1](#1-the-2026-11-25-conflict-record-measured) for the 22-conflict audit, [§4](#4-weighing-the-six-options) for the per-option trigger/carrier/cost/savings analysis, and [§5](#5-ranking-and-recommended-package) for the ranked table.

**Needs your ruling:** [`OQ-CAR1`](#OQ-CAR1), [`OQ-CAR2`](#OQ-CAR2), [`OQ-CAR3`](#OQ-CAR3).

---

## 1. The 2026-11-25 Conflict Record, Measured

### 1.1 Query scope and accounting method

Querying `.tillr/state.db` (`events`, `runs`) and Git commit trees on `2026-11-25` across `05:30:00`–`13:30:00 EST` (`10:30:00`–`18:30:00 UTC`, `events.seq` `7632..14437`) for `kind = 'land.landing'` with `json_extract(payload, '$.result') = 'squash-conflict'` yields **22 conflict landings**:

- **Pre-conflict gate wall time:** Measuring each conflicted run from its latest preceding `gate.started` to `land.landing` totals **`630.2` gate-minutes**; measuring from the earliest `gate.started` of that `run_id` (capturing restarted gates on `#12`, `#13`, and `#19`) totals **`793.8` gate-minutes** (summarized in `item-request-347d54ef07` as `"about 650 gate-minutes in all"`). Unless noted, per-option gate savings below use the conservative **`630.2`-minute** last-span figure so restarted gate spans are never double-counted.
- **Follow-on `-cfix` repair runs:** Those 22 conflicts staged **`21` `-cfix` runs** (`#6`, `5fcba81a2a-a6`, stepped into a fresh attempt rather than `-cfix`). Inside the audit window, `18` completed (`3` conflicts at `13:29 EST` — `#20`, `#21`, `#22` — ran their repairs immediately after `13:30 EST`), consuming **`181.9` agent-minutes**, **`$11.51`**, and **`27,672,430` tokens**. Only **`1` of `21`** (`#12`, `run-item-request-b4a09d22e8-a2-cfix`, `2.80m`, `$0.69`) resumed its warm worker session (`resumed-worker`); **`15`** discarded warm sessions because `conflictFix` forced `reason = "trunk-conflict"` (`fix.SessionTurn = false`), **`4`** hit `session-missing`, and **`1`** hit `lane-unavailable`.
- **Post-repair re-gate cost:** `17` completed `-cfix` candidates entered a full post-repair gate, spending **`413.3` additional gate-minutes**, and **`3` of those `-cfix` runs ended in a second `squash-conflict`**: `#5` (`9664864271-a2-n1-cfix`, `33.9m`), `#14` (`56ef86bc43-a2-n1-cfix`, `50.2m`), and `eab5370855-a2-n3-cfix` (`4.0m` at `13:52 EST`).

### 1.2 When mergeability broke (`git merge-tree --write-tree`)

Replaying `git merge-tree --write-tree --merge-base=<base_sha> <trunk_commit> <head_sha>` against every `land.landing` (`result = "landed"`) in sequence partitions the 22 conflicts by the exact phase in which mergeability first failed:

| Phase where `merge-tree` first failed | Raw count (`lane_base = green`) | Pre-conflict gate (last-span) | `-cfix` agent time | Post-repair re-gate | Fate under `eab5370855` (`lane_base = tip`) |
| :--- | ---: | ---: | ---: | ---: | :--- |
| **Before attempt start** (stale `green` base `95d7043eb6` from `04:39 EST`) | `16` (`#2, #7–#13, #15–#22`) | `484.4m` | `124.0m` | `296.2m` | **`8` vanish completely** (`#2, #7, #8, #9, #10, #15, #16, #17`); **`1` (`#12`)** becomes a build-window collision; **`7` (`#11` vs `#9`, `#13, #18–#22`)** become gate-window collisions |
| **During worker build** (`started_at < winner_landed <= finished_at`) | `4` (`#1, #3, #5, #6`) | `65.8m` | `45.1m` | `58.6m` | Stay build-window collisions (`+ #12` = **`5` total**) |
| **During / waiting for gate** (`finished_at < winner_landed <= conflict_ts`) | `2` (`#4, #14`) | `79.9m` | `12.8m` | `58.5m` | Stay gate-window collisions (`+ #11, #13, #18–#22` = **`9` total**) |
| **Total** | **`22`** | **`630.2m`** | **`181.9m`** | **`413.3m`** | **`8` eliminated, `14` remaining** (`5` build-window, `9` gate-window) |

Two pathological artifacts of `lane_base = green` (`95d7043eb6`) stand out in the Git history of those 16 stale-base runs:

1. **False conflict after manual worker rebase (`#12`, `run-item-request-b4a09d22e8-a2`, `59.7m` gate):** The worker saw `main` ahead of `95d7043eb6` and manually rebased its candidate commit (`c8a00bf2` [unlanded]) onto `f17d0c36` (`HEAD^ == f17d0c36`), while `<lane>/base_sha` and `TILLR_GOAL_BASE` stayed `95d7043eb6`. At `11:26:40 EST`, `tillr-land` ran `git merge-tree --merge-base=95d7043eb6` instead of `f17d0c36`, replaying `20` intermediate `main` commits against themselves and failing on `15` paths — whereas `git merge-tree --write-tree --merge-base=f17d0c36` against the `11:26:40 EST` trunk tip (`19f56bb8`) **exits `0` with zero conflicts**.
2. **Manual `main` snapshot squashed onto stale base (`#13`, `run-item-request-7913681e82-a1`, `59.7m` last-span / `91.6m` all-span gate):** Starting on `95d7043eb6`, the worker merged `main`'s tree into its commit (`1753be75` [unlanded], `292` changed files relative to `95d7043eb6`) while `HEAD^` remained `95d7043eb6`. When two more items landed during its gate, `tillr-land` replayed all `292` files from `95d7043eb6` and collided across `38` paths.

### 1.3 Hot-file concentration and per-episode audit table

Collapsing 1-to-1 `yolo-packs/tillr/files/local/share/tillr/...` pack mirrors onto their canonical `tools/...` and `docs/...` sources, **two file clusters drove `16` of the `22` conflicts (`73%`)**:

- **[`docs/design/tillr-utility-contracts.md`](./tillr-utility-contracts.md) (`+ sub-docs`):** **`10` of `22` conflicts** (`9` on the parent file `#2, #3, #6, #8, #12, #13, #18, #19, #22`, plus `#16` on `docs/design/tillr-utility-contracts/tillr-run.md`). Independent utility-contract items repeatedly collided on the parent document's shared Decision Ledger and reach-in/slice tables.
- **`tools/tillr/watch/*.go` (`pattern.go`, `rules.go`, `lint_rules.go`, `eval.go`, `git.go`, `variables_test.go`):** **`8` of `22` conflicts** (`#7, #9, #10, #11, #12, #13, #14, #20`; `pattern.go` and `rules.go` in `6` each).
- **`tools/gate/checks/*` and `tools/gate/go-test-edges.json`:** **`5` of `22` conflicts** (`#8, #12, #13, #18, #19`). Individual checks already live in separate `tools/gate/checks/<id>.json` files; collisions hit `tools/gate/go-test-edges.json` (`#8, #12, #13`) and shared check files edited by two items at once (`go-affected.json`, `change-checks-test.json`).
- **[`docs/reference/gate.md`](../reference/gate.md):** **`3` of `22` conflicts** (`#18, #19, #21`).

| # | `seq` | EST | `run_id` | Broke (actual -> `tip`) | Gate (`m`) | `-cfix` (`m` / resume) | Re-gate (`m`) | Canonical conflict paths (abbreviated) |
| ---: | ---: | :--- | :--- | :--- | ---: | :--- | ---: | :--- |
| 1 | 7632 | 06:31 | `run-item-request-9664864271-a2-n1` | build -> build | 31.7 | 12.7 (cold) | 33.9 (conflict #5) | `tools/tillr-drain/why.go` |
| 2 | 7660 | 06:39 | `run-item-request-395fc64870-a5` | pre-start -> **none** | 0.2 | 11.3 (cold) | 0.2 | `docs/design/tillr-utility-contracts.md` |
| 3 | 7864 | 07:00 | `run-item-request-7a32402c34-a3` | build -> build | 0.2 | 13.7 (cold) | 0.1 | `docs/design/tillr-utility-contracts.md` |
| 4 | 7902 | 07:03 | `run-item-request-da2e75c68f-a1-n1` | gate -> gate | 29.7 | 0.5 (cold) | 18.8 | `tools/tillr-drain/drain/schema_roll_test.go` |
| 5 | 8109 | 07:19 | `run-item-request-9664864271-a2-n1-cfix` | build -> build | 33.9 | 18.7 (cold) | 24.6 | `tools/tillr-drain/drain/pass.go` |
| 6 | 8536 | 08:03 | `run-item-request-5fcba81a2a-a6` | build -> build | 0.1 | — (next `-a7`) | — | `docs/design/tillr-utility-contracts.md` |
| 7 | 9231 | 09:01 | `run-item-request-5f7f79fac4-a1-n7-...` | pre-start -> **none** | 19.1 | 5.5 (cold) | 34.4 | `tools/tillr/watch/{eval,lint_rules,lint_test,pattern,rules,source}.go` |
| 8 | 9321 | 09:07 | `run-item-request-4435f97b69-a1` | pre-start -> **none** | 11.4 | 12.4 (cold) | 65.4 | [`tillr-utility-contracts.md`](./tillr-utility-contracts.md), `go-affected.json`, `go-test-edges.json`, ... |
| 9 | 10687 | 10:22 | `run-item-request-947bbd0a3b-a2-n1` | pre-start -> **none** | 29.2 | 11.1 (cold) | 34.0 | `tools/tillr/watch/{lint_rules,pattern,rules}.go` |
| 10 | 10724 | 10:24 | `run-item-request-9da553ae27-a1` | pre-start -> **none** | 29.8 | 10.6 (cold) | 29.8 | `tools/tillr/watch/{git,git_eval_test}.go` |
| 11 | 10742 | 10:24 | `run-item-request-56ef86bc43-a2-n1` | pre-start -> gate (vs #9) | 31.5 | 12.4 (cold) | 50.2 (conflict #14) | `tools/tillr/watch/{lint_rules,pattern,rules,tillr_source}.go` |
| 12 | 11887 | 11:26 | `run-item-request-b4a09d22e8-a2` | pre-start -> build* | 59.7 | 2.8 (**warm**) | 29.8 | [`tillr-utility-contracts.md`](./tillr-utility-contracts.md), `watch/*`, `go-affected.json` (*clean on `HEAD^`!) |
| 13 | 11892 | 11:26 | `run-item-request-7913681e82-a1` | pre-start -> gate | 59.7 | 27.6 (missing) | — (next `-a2`) | [`tillr-utility-contracts.md`](./tillr-utility-contracts.md), `watch/*`, `go-test-edges.json` (`38` paths) |
| 14 | 11938 | 11:28 | `run-item-request-56ef86bc43-a2-n1-cfix` | gate -> gate | 50.2 | 12.3 (cold) | 39.7 | `tools/tillr/watch/{lint_rules,pattern,rules}.go` |
| 15 | 12095 | 11:38 | `run-item-request-6321447ce5-a1` | pre-start -> **none** | 10.2 | 3.5 (cold) | 27.8 | `tools/tillr-view/launcher_observatory_test.go` |
| 16 | 12554 | 12:02 | `run-item-request-4d1ca1e121-a3` | pre-start -> **none** | 33.2 | 4.9 (missing) | 25.0 | [`tillr-utility-contracts/tillr-run.md`](./tillr-utility-contracts/tillr-run.md), `tools/tillr-run/main.go` |
| 17 | 12611 | 12:04 | `run-item-request-d570a35de8-a2-n3` | pre-start -> **none** | 21.3 | 7.1 (cold) | 12.0 | `tools/tillr-drain/drain/heavy_loop.go` |
| 18 | 13264 | 12:39 | `run-item-request-8721e56757-a2` | pre-start -> gate | 26.6 | 9.5 (cold) | 38.2 | [`tillr-utility-contracts.md`](./tillr-utility-contracts.md), [`gate-test-capacity.md`](./gate-test-capacity.md), [`docs/reference/gate.md`](../reference/gate.md) |
| 19 | 14415 | 13:29 | `run-item-request-26f0d1b1d4-a2-n1` | pre-start -> gate | 37.3 | 5.2 (unavail) | — (post-13:30) | [`tillr-utility-contracts.md`](./tillr-utility-contracts.md), `pack-sync.test.py`, `tools/gate/checks/*` |
| 20 | 14424 | 13:29 | `run-item-request-8fdabc44ba-a4-n3` | pre-start -> gate | 38.4 | — (post-13:30) | — (post-13:30) | `tools/tillr/watch/{lint_rules,pattern,variables_test}.go` |
| 21 | 14434 | 13:29 | `run-item-request-eab5370855-a2-n3` | pre-start -> gate | 38.4 | — (post-13:30) | 4.0 (conflict) | [`docs/reference/gate.md`](../reference/gate.md) |
| 22 | 14437 | 13:29 | `run-item-request-7913681e82-a3` | pre-start -> gate | 38.4 | — (post-13:30) | — (post-13:30) | [`tillr-utility-contracts.md`](./tillr-utility-contracts.md), `tillr-docs.test.py` (ported in `874381de`) |

### 1.4 Authority boundary: standing owner rulings, designer decisions, and open questions

- **Standing owner rulings obeyed without change:**
  - [`landing-principles.md`](./landing-principles.md) ([§1](./landing-principles.md#1-a-refusal-must-be-a-verdict)–[§17](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking)) and its trunk-activity test (*"a mechanism whose cost rises with trunk activity is pointed the wrong way"*).
  - [`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4) (`2026-11-24`, implemented in `606b43306d`): gates are **never** batched or held in an arrival-order gate queue (`Schedule A`: start every ready gate immediately).
  - [`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger): repository/document layout and workflow policy choices belong to the owner ([`OQ-CAR1`](#OQ-CAR1)–[`OQ-CAR3`](#OQ-CAR3)), never guessed as settled rulings.
- **Designer recommendations (`2026-11-25`, not owner rulings; the owner may overturn any of them):** [`D-CAR1`](#D-CAR1)–[`D-CAR8`](#D-CAR8).

---

## 2. Assumed Baseline (`eab5370855`, `347d54ef07`) and Ordering After `35c954f928`

### 2.1 What `eab5370855` and `347d54ef07` already remove — and what they leave behind

1. **`item-request-eab5370855` (`lane_base: "tip"` + unproved-base worker briefing, built in `43455fc4dd` / [`D-GR55`](../reference/gate.md#D-GR55)):**
   - Eliminates **`8` of the `22` conflicts outright** (`#2, #7, #8, #9, #10, #15, #16, #17`, saving `154.3m` pre-conflict gate, `66.1m` `-cfix`, and `228.5m` post-repair re-gate). Note that `#9` (`947bbd0a3b`) and `#11` (`56ef86bc43`) built side-by-side at `09:48`–`09:53 EST` on `tools/tillr/watch/{pattern,rules}.go`: under `lane_base: "tip"`, `#9` lands cleanly at `10:22 EST` while `#11` (`31.5m` gate) becomes an in-flight collision against `#9`.
2. **`item-request-347d54ef07` (skip pre-gate or stop live gate via `git merge-tree --write-tree`):**
   - Standalone (even without `eab5370855`), skips `20` of `22` gates before `gate.started` and aborts `#4` and `#14` mid-gate when their colliding winner lands, saving **`572.6` of `630.2` pre-conflict gate-minutes** (leaving `57.6m` spent prior to abort: `15.5m` in `#4` and `42.1m` in `#14`), while saving **`0` `-cfix` runs** and **`0` post-repair re-gate minutes**.
3. **Combined baseline (`eab5370855` + `347d54ef07`) — the exact residual problem Options 1–6 compete to solve:**
   - **`14` conflicts still happen:** `5` during build (`#1, #3, #5, #6, #12` — skipped at `0m` gate by `347d54ef07`, saving `125.6m` pre-conflict gate) and `9` during/before gate (`#4, #11, #13, #14, #18, #19, #20, #21, #22` — where `347d54ef07` saves `138.8m` after the colliding landing arrives, but **`~211.6m` of gate work already ran before abort**).
   - **`~115.8m` (`$7.60`) of cold-session `-cfix` repair runs** and **`~214.6m` of post-repair Go re-gates** still execute across those `14` episodes (note: `5e728e5a58` / [`D-GR53`](../reference/gate.md#D-GR53) since deferred `tillr-drain/drain` integration tests to after-merge proof, while keeping all 38 other Go test packages — including `tools/tillr/watch`, `tools/gate`, `tools/tillr-gate`, `tools/tillr-run`, `tools/tillr-loop`, and `tools/tillr-view` — inside pre-merge `go-affected`).

### 2.2 Ordering `Option 1` after `item-request-35c954f928` ([`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md))

The request directs this design to *"absorb or order after"* `item-request-35c954f928` (*"tell a running worker when the trunk goes green and let it rebase"*). Because `35c954f928` has now landed its full specification in [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md) (`7a09eeb863`, [`D-WR1`](./tillr-green-trunk-worker-rebase.md#D-WR1)–[`D-WR8`](./tillr-green-trunk-worker-rebase.md#D-WR8) and [`OQ-WR1`](./tillr-green-trunk-worker-rebase.md#OQ-WR1)–[`OQ-WR3`](./tillr-green-trunk-worker-rebase.md#OQ-WR3)), **ordering Option 1 (`CAR-B5`) directly after [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md) Slices 1–2** avoids duplicating the worker-rebase machinery and reuses four pieces unchanged ([`D-CAR4`](#D-CAR4)):

1. **Green and check-repair notices stay governed by [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md) (`<lane>/trunk-green.json`, [`OQ-WR1`](./tillr-green-trunk-worker-rebase.md#OQ-WR1)–[`OQ-WR3`](./tillr-green-trunk-worker-rebase.md#OQ-WR3)):** Workers on an already-green base hear nothing when unrelated commits prove; workers on an unproved or red base receive `<lane>/trunk-green.json` (`green-advanced` or `check-repaired-on-tip`).
2. **Safe mid-run rebase and `<lane>/base_sha` advancement reuse `tillr-run lane-rebase` and `rebased-base.json` ([`D-WR4`](./tillr-green-trunk-worker-rebase.md#D-WR4), [`D-WR5`](./tillr-green-trunk-worker-rebase.md#D-WR5)):** [`tillr-green-trunk-worker-rebase.md` Section 4.1](./tillr-green-trunk-worker-rebase.md#41-why-raw-git-rebase-by-a-worker-is-unsafe-today) diagnoses the exact `TILLR_GOAL_BASE` vs. `HEAD` drift that caused false conflict `#12` (`b4a09d22e8-a2`) and solves it via `tillr-run lane-rebase` (`rebase_trunk` tool on `pi` and `claude`) writing an authorized `rebased-base.json` receipt read by [`land.ts`](../../tools/tillr-run/backends/pi/land.ts), [`keep-going.ts`](../../tools/tillr-run/backends/pi/keep-going.ts), [`claude/landing.go`](../../tools/tillr-run/claude/landing.go), and `settleBase`.
3. **`Option 1` (`CAR-B5`) adds only path-overlap detection (`<lane>/trunk-overlap.json`) on top of that channel ([§4.1](#41-option-1-ordered-after-35c954f928-turn-gap-notices-to-a-running-worker)):** When `land.landing` (`result == "landed"`) modifies paths intersecting an active writing lane's modified or untracked files, `tillr-drain` writes `<lane>/trunk-overlap.json` (`target_sha`, `overlapping_paths`) outside `<lane>/worktree`, authorizing `tillr-run lane-rebase` (`--reconcile` when overlapping hunks conflict per [`D-WR8`](./tillr-green-trunk-worker-rebase.md#D-WR8)) to advance `effective_base_sha` to `target_sha` even when the lane's prior base was already green.
4. **Advisory vs. mandatory rebase on `path-overlap` ([`OQ-CAR3`](#OQ-CAR3)):** While [`OQ-WR2`](./tillr-green-trunk-worker-rebase.md#OQ-WR2) settles opt-in vs. loop-only rebase for `trunk-green.json`, [`OQ-CAR3`](#OQ-CAR3) asks whether a `trunk-overlap.json` notice stays advisory with pre-gate mechanical fallback (`CAR-B1`) or blocks `land` until the worker rebases.

---

## 3. Checked Against the Landing Principles and Gate-Batching Ruling

Every option is tested against [`landing-principles.md`](./landing-principles.md) — specifically **the trunk-activity rule** (*"a mechanism whose cost rises with trunk activity is pointed the wrong way"*), [§2 (*Re-application preserves the change, not the patch*)](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch), [§3 (*Gate what will be the tip, once*)](./landing-principles.md#3-gate-what-will-be-the-tip-once), [§9 (*Fix the machinery; never hand-carry the work*)](./landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work), [§13 (*A held state names a mover that can still act*)](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act), and [§14 (*Convict a run on what it did, not on what happened while it ran*)](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran) — plus owner ruling [`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4) (`606b43306d`: no gate batching and no arrival-order gate lock):

| Option | Trunk-activity cost test | [`landing-principles.md`](./landing-principles.md) & [`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4) check | Ruling constraint |
| :--- | :--- | :--- | :--- |
| **Option 1** (ordered after `35c954f928`) — Turn-gap notice to running worker | **Fails if broadcast; passes when path-filtered.** Unfiltered notices wake $L$ agents on every landing ($O(L)$ LLM turns per trunk move). Path-filtering (`changed_paths ∩ lane_touched != ∅`) costs `< 5 ms` of local Git plumbing in `tillr-drain` and `0` LLM turns on disjoint landings. | Preserves [§14](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran) and touches no gate queue ([`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4)). | Must filter strictly by touched paths (`Option 1`) alongside [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md)'s unproved/red-base filter (`35c954f928`), and read at turn gaps only (`0` mid-tool interrupts). |
| **Option 2** — Mechanical repair first + warm-session `-cfix` | **Passes ($O(0)$ per trunk move).** Runs `git merge-tree` / `commit-tree` once when a candidate prepares to gate or conflicts — independent of how many unrelated commits landed meanwhile. | Enforces [§2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch) and [§9](./landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work); stops convicting clean rebases with paid LLM turns ([§14](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)). | Must verify the rebased tree (`Option 5` reuses unchanged check digests rather than skipping verification). |
| **Option 3** — Pairwise in-flight `merge-tree` | **Fails on building worktrees; marginal at gate entry.** Polling $B$ dirty worktrees costs $O(B(B+G))$ per edit/landing and holds finished candidates behind uncommitted workers. Gate-entry check against $G$ gating candidates costs $O(G)$ (`< 30 ms`). | Holding behind a gating candidate names an active mover ([§13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)), **but** trades guaranteed wait latency when the ahead gate fails to avoid bounded conflict waste ([`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4), [`gate-batching.md` §2](../research/gate-batching.md#2-why-batching-was-rejected)). | Never test uncommitted building worktrees; if enabled ([`OQ-CAR2`](#OQ-CAR2)), test only at gate entry against `l.verifying` and release immediately on ahead gate failure. |
| **Option 4** — Pre-lane hot-file serialization + file split | **Passes ($O(|\text{released paths}|)$ per landing).** Splitting hot docs reduces conflict surface permanently; `livePaths` holds colliding items before lane allocation (`0` wasted worker or gate CPU). | Directly implements [`tillr-shared-path-serialization.md`](./tillr-shared-path-serialization.md) (`D-SPS1`–`D-SPS7`), obeys [§13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act) (`DeferredBy` names active holder), and leaves disjoint gates parallel ([`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4)). | Exact file paths only (never directory/package locks); release immediately on `landed` or `parked`. |
| **Option 5** — Post-repair check / Go package verdict reuse | **Passes ($O(0)$ per trunk move).** Digest computation runs once per candidate gate over declared package inputs; trunk moves on untouched packages leave digests identical (`100%` hit). | Directly enforces [§3 (*Gate what will be the tip, once*)](./landing-principles.md#3-gate-what-will-be-the-tip-once) on the rebased candidate tree while skipping redundant package runs whose exact declared inputs already passed. | Must hash inputs at the **post-rebase candidate tree** (`head_sha^{tree}`), never carrying over a verdict whose package files or reverse-edge dependencies changed. |
| **Option 6** — Front-of-gate-order after repair | **Fails if built as a global gate lock; passes as per-path hold retention.** Commit `606b43306d` removed `test_slots.go` under [`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4); every ready gate starts at once. Blocking unrelated gates behind a repaired candidate violates [`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4). | Keeping `livePaths` held across `-cfix` (`D-SPS3`) + dispatching `-cfix` ahead of fresh ordinary starts prevents same-path leapfrogging without blocking a single disjoint gate. | Reject any cross-item gate lock; implement via continuous `livePaths` hold across `-cfix` (`CAR-B3`) + priority `-cfix` lane dispatch (`CAR-B1`). |

---

## 4. Weighing the Six Options

For each option below, **Standalone 2026-11-25 savings** measures the option alone against the raw `22`-conflict record (`630.2m` pre-conflict gate, `181.9m` `-cfix`, `413.3m` post-repair re-gate), and **Incremental savings net of `eab5370855` + `347d54ef07`** measures what it saves on the `14` remaining conflicts (`211.6m` pre-abort gate, `115.8m` `-cfix`, `214.6m` post-repair re-gate).

### 4.1 Option 1 (ordered after `35c954f928`): Turn-gap notices to a running worker

- **Recorded fact that triggers it:**
  - Existing `land.landing` event (`result == "landed"`, `trunk_before`, `trunk_after`). `tillr-drain` computes `changed_paths = git diff-tree -r --no-commit-id --name-only <trunk_before> <trunk_after>` (canonicalizing `yolo-packs/tillr/` mirrors) and intersects it against each active writing lane's touched paths (`git diff --name-only <effective_base>` union untracked files from `git status --porcelain`). A `path-overlap` notice fires iff the intersection is non-empty. (Green/repair notices on unproved bases are handled by [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md)'s `<lane>/trunk-green.json`, [§2.2](#22-ordering-option-1-after-item-request-35c954f928-tillr-green-trunk-worker-rebasemd).)
- **Message or event that carries it (`existing` SQLite events + `new` `<lane>/trunk-overlap.json` notice file reusing `35c954f928` hooks):**
  - **No new SQLite event kind is required** (optionally add `changed_paths []string` to `land.landing` in [`tools/tillr-land/land/landing.go`](../../tools/tillr-land/land/landing.go) so watch/view readers avoid shelling out to `git diff-tree`).
  - **New monotonic lane file `<lane>/trunk-overlap.json`:** `tillr-drain` writes `{"seq", "target_sha", "overlapping_paths", "updated_at"}` atomically outside `<lane>/worktree` alongside `<lane>/trunk-green.json` ([`D-WR2`](./tillr-green-trunk-worker-rebase.md#D-WR2)), with `trunk-overlap-ack.json` in `run.DurableDir` ensuring each `target_sha` surfaces at most once.
  - **Turn-gap reader & rebase helper (reused from [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md) Slices 1–2):** The Pi turn-gap extension (`run-pi`) and Claude `PostToolUse` `additionalContext` hook ([`tools/tillr-run/claude/landing.go`](../../tools/tillr-run/claude/landing.go)) read `<lane>/trunk-overlap.json` at the next tool/turn boundary and offer `rebase_trunk` (`tillr-run lane-rebase`, updating `rebased-base.json` per [`D-WR5`](./tillr-green-trunk-worker-rebase.md#D-WR5)); `muse` and `agy` (`cannot-resume`) catch up between nodes or via `CAR-B1` pre-gate rebase.
- **Cost per trunk move:**
  - `tillr-drain`: `1` `git diff-tree` (`~2 ms`) + `1` `git status`/`diff` per active writing lane (`~3 ms` x `2–4` lanes = `< 15 ms`).
  - Agent LLM cost: **`0` tokens on disjoint landings**; `1` warm turn-gap rebase (`~1–3m`) only when an active worker actually edited the landed file or benefits from a clean `green` advance.
- **What it would have saved on 2026-11-25:**
  - *Standalone:* Catches the **`4` build-window conflicts** (`#1, #3, #5, #6`), avoiding **`65.8m` pre-conflict gate**, **`3` `-cfix` runs (`45.1m` cold -> `~8m` warm in-session, saving `~37m`)**, and **`58.6m` post-repair re-gate** (including preventing repeat conflict `#5`). Cannot help `#4` or `#14` (worker already exited before winner landed) or the `16` pre-start conflicts.
  - *Net of `eab5370855` + `347d54ef07`:* Catches **`5` build-window conflicts** (`#1, #3, #5, #6, #12`), saving **`0m` gate before conflict** (already `0m` under `347d54ef07`), **`4` `-cfix` runs (`~35m` net agent time)**, and **`~88m` post-repair re-gate** (before Option 5). Net of Option 4 (`livePaths`), its incremental role narrows to undeclared-path build overlaps plus `35c954f928` green-advance notices.

### 4.2 Option 2: Mechanical repair first, and warm-session resume on real conflicts

- **Recorded fact that triggers it:**
  1. *Pre-gate / post-conflict mechanical rebase:* Existing candidate commit (`drain.candidate`: `base_sha`, `head_sha`) when `base_sha != refs/heads/main`, or existing `land.landing` (`result == "squash-conflict"`). First compute `effective_base`: if `head_sha^ != base_sha` and `is-ancestor(base_sha, head_sha^)` and `is-ancestor(head_sha^, main)` (as in `#12`), set `effective_base = head_sha^`; else `effective_base = base_sha`. Run `git merge-tree --write-tree --merge-base=<effective_base> refs/heads/main <head_sha>`.
  2. *Warm-session conflict repair (`SessionTurn = true`):* Triggered only when `git merge-tree --write-tree` exits `1` (real textual or delete/modify conflict).
- **Message or event that carries it (`existing` events only):**
  - **Zero new event kinds.**
  - When `merge-tree` exits `0` (`tree_oid`): `Loop` creates a single rebased commit `new_head = git commit-tree <tree_oid> -p <main_sha>` preserving author/message from `head_sha`, updates the lane branch and `<lane>/base_sha = <main_sha>`, appends **existing** `drain.candidate` (`base_sha: main_sha`, `head_sha: new_head`), and proceeds straight to `Loop.verify` with **zero LLM runs** (removing the unconditional `fix` agent dispatch on `moved != ""` in [`tools/tillr-drain/drain/conflict.go`](../../tools/tillr-drain/drain/conflict.go)).
  - When `merge-tree` exits `1`: Instead of calling `resetLane` + setting `reason = "trunk-conflict"`, `fix.SessionTurn = false` (which discarded `15` warm sessions on 2026-11-25!), `conflictFix` checks out `main_sha` in `<lane>/worktree`, runs `git cherry-pick --no-commit <effective_base>..<head_sha>` so Git leaves standard conflict markers only in the conflicted files while keeping `<lane>/worker-session.json` intact in `<lane>` (outside `<lane>/worktree`), calls `prepareWorkerResume`, and dispatches **existing** `run.allocate` (`-cfix`) with **`fix.SessionTurn = true`** whenever the candidate's session exists (`resumed-worker`). Ready `-cfix` runs dispatch at `Priority: 0` ahead of unstarted ordinary items.
- **Cost per trunk move:**
  - **`0` (`$O(0)$ per trunk move).** Nothing runs when `main` advances unless a candidate actually enters `verify` or hits `squash-conflict`; one `git merge-tree` + `commit-tree` takes `< 10 ms`.
- **What it would have saved on 2026-11-25:**
  - *Standalone:*
    1. Resolves **`#12` (`b4a09d22e8-a2`) mechanically with `0` LLM runs**, saving **`59.7m` pre-conflict gate**, **`2.8m` `-cfix`**, and **`29.8m` re-gate**.
    2. Converts all **`15` cold `trunk-conflict` `-cfix` runs** into warm `resumed-worker` turns. On 2026-11-25, the sole warm `-cfix` (`#12`) finished in **`2.80m` (`$0.69`)** versus a **`10.6m` (`$0.64`)** mean across cold `trunk-conflict` runs (`18.7m` on `#5`, `13.7m` on `#3`, `12.7m` on `#1`), saving **`~115m` of `-cfix` wall time** (`~63%` reduction).
  - *Net of `eab5370855` + `347d54ef07`:* Saves **`32.7m` pre-abort gate + `2.8m` `-cfix` + `29.8m` re-gate** on `#12` mechanically, and cuts `-cfix` agent wall time across the remaining `13` conflicts by **`~80m`** (`115.8m` -> `~35m`), with **zero false holds and zero trunk-activity overhead**.

### 4.3 Option 3: Pairwise `merge-tree` across in-flight candidates

- **Recorded fact that triggers it:**
  - When candidate $C_2$ commits (`drain.candidate`) and prepares to enter `Loop.verify`, `Loop` inspects `l.verifying` (candidates $C_1$ that already emitted `gate.started` and have not yet finished `land.landing` or failed `gate.verdict`).
  - Compute synthetic post-$C_1$ trunk tree $T_1 = \text{merge-tree}(\text{base}_1, \text{main}, \text{head}_1)$, then check $\text{merge-tree}(\text{base}_2, T_1, \text{head}_2)$ (approximated in one Git call via `git merge-tree --write-tree <merge_base> <head_1> <head_2>` when both share `<main>`). If non-zero, $C_2$ waits in `ReasonInFlightConflict` (`DeferredBy: C_1.ItemID`) until $C_1$ either lands (triggering Option 2 rebase of $C_2$ before $C_2$'s gate even starts!) or fails its gate (releasing $C_2$ immediately to gate).
  - *Rejected variant:* Running pairwise `merge-tree` against uncommitted *building* worktrees is rejected ([§3](#3-checked-against-the-landing-principles-and-gate-batching-ruling)).
- **Message or event that carries it (`existing` events only):**
  - **Zero new event kinds:** in-memory hold reason `ReasonInFlightConflict` surfaced by `tillr-drain why` (and optional `step = "in-flight-conflict"` on existing `drain.refusal` if logged).
- **Cost per trunk move:**
  - $O(0)$ per unrelated trunk move; $O(|l.\text{verifying}|)$ (`1`–`5` `git merge-tree` calls, `< 25 ms` total) once at gate entry, plus `1` re-check when a held candidate's blocker finishes.
  - **Hidden latency cost:** Whenever $C_1$ fails its gate (`~20–30%` of red gates during active development), holding $C_2$ added up to `35m` of idle wait latency for a conflict that never materialized (`Schedule B` penalty in [`gate-batching.md` §2](../research/gate-batching.md#2-why-batching-was-rejected)).
- **What it would have saved on 2026-11-25:**
  - *Standalone:* Holds later colliding gates in `#2, #3, #4, #8, #9/#11, #12, #13, #14, #18, #19, #20, #21, #22` (including preventing fast newcomers `39fbff3ebe-a1` [`3.0m` gate] and `5fcba81a2a-a10` [`7s` docs gate] from leapfrogging `#20` and `#22`'s `38.4m` Go gates!), saving **`~390m` of pre-conflict gate** (does **not** prevent `-cfix` repairs, because $C_2$ already finished building before gate entry).
  - *Net of `eab5370855` + `347d54ef07`:* Saves up to **`~179m` of pre-abort gate CPU** across the `9` gate-window conflicts (`#4, #11, #13, #14, #18–#22`), **and** prevents leapfrogs `#20` and `#22` (`0` `-cfix` saved unless preventing a leapfrog flips who repairs from a slow Go item to a fast docs item!).
  - *Net of Option 4 (`livePaths`) as well:* Because Option 4 already prevents `#4, #11, #13, #14, #18, #19, #22` before lane dispatch, Option 3's residual incremental gain shrinks to **`~20.2m` of pre-abort gate** (`#20` [`18.6m`] + `#21` [`1.6m`], where undeclared test/doc edits bypassed `allowed_paths`). Hence [`OQ-CAR2`](#OQ-CAR2) leans toward omitting Option 3 initially (`B`) or enabling it strictly at gate entry (`A`).

### 4.4 Option 4: Pre-lane hot-file serialization and splitting hot files

- **Recorded fact that triggers it:**
  1. *Queue half (`livePaths` in `PlanPass`, [`tillr-shared-path-serialization.md`](./tillr-shared-path-serialization.md) `D-SPS1`–`D-SPS7`):* Existing `items.allowed_paths`, `classify` `write_paths`, active `runs`, committed `drain.candidate` diff paths (`base_sha..head_sha`), and unresolved `conflict_paths` from `land.landing` (`squash-conflict`). Importantly, an item retains its `livePaths` claim continuously across build, gate, **and `-cfix` repair** until `landed` or `parked`/`blocked`.
  2. *Layout half (splitting hot files, [`OQ-CAR1`](#OQ-CAR1)):*
     - **[`docs/design/tillr-utility-contracts.md`](./tillr-utility-contracts.md) (`9` of `22` conflicts on parent file, `10` with `tillr-run.md`):** Per-utility detail files (`docs/design/tillr-utility-contracts/<utility>.md`) *already exist*, yet every utility build edited the parent file's shared Decision Ledger and reach-in/slice tables. Moving per-utility `D-UC*` ledger rows and slice tables into `docs/design/tillr-utility-contracts/<utility>.md` leaves the parent file untouched by individual utility builds.
     - **Gate check declarations (`5` of `22` conflicts):** `tools/gate/checks/<id>.json` is **already** one file per check; the `5` conflicts hit `tools/gate/go-test-edges.json` (`3` conflicts) and identical check files edited concurrently (`go-affected.json`, `change-checks-test.json`). Exact-file `livePaths` serializes edits to `go-test-edges.json` or a shared `<id>.json` without false holds on other checks ([`OQ-CAR1`](#OQ-CAR1) asks whether also to split `go-test-edges.json` per module).
     - **`tools/tillr/watch/*.go` (`8` of `22` conflicts):** `pattern.go`, `rules.go`, and `lint_rules.go` are cohesive Go package files (`6` conflicts each); `livePaths` serializes overlapping `watch` items cleanly at the front of the queue (`ReasonPathHeld`).
- **Message or event that carries it (`existing` events only):**
  - **Zero new SQLite event kinds:** `livePaths` is recomputed in memory on each `PlanPass` (`D-SPS7`) and reported via `ReasonPathHeld` (`DeferredBy`, contended paths) in `tillr-drain why` and `tillr view waiting`.
- **Cost per trunk move:**
  - $O(|\text{paths released by landed item}|)$ map deletions (`< 1 ms`), `0` subprocesses, `0` LLM turns. Splitting [`tillr-utility-contracts.md`](./tillr-utility-contracts.md) has **$O(0)$ runtime cost forever**.
- **What it would have saved on 2026-11-25:**
  - *Standalone:* Prevents **`18` of `22` conflicts** before worker dispatch (all except undeclared incidental edits like `#20` `variables_test.go` and stale-base `#7`/`#15`/`#17` if their prior holder had already landed before `95d7043eb6` stalled), saving **`~530m` pre-conflict gate**, **`~150m` `-cfix`**, and **`~340m` post-repair re-gate**.
  - *Net of `eab5370855` + `347d54ef07`:* Prevents **`11` to `12` of the `14` remaining conflicts** (`#1, #3, #4, #5, #6, #11, #12, #13, #14, #18, #19, #22` — including stopping `5fcba81a2a-a10` from even starting while `#19` and `#22` held [`tillr-utility-contracts.md`](./tillr-utility-contracts.md), or eliminating `#3, #6, #12, #13, #18, #19, #22`'s [`tillr-utility-contracts.md`](./tillr-utility-contracts.md) collisions outright via the file split!), saving **`~191m` of the `211.6m` pre-abort gate**, **`~98m` of `-cfix`**, and **`~185m` of post-repair re-gate**.

### 4.5 Option 5: Post-repair check and Go package verdict reuse

- **Recorded fact that triggers it:**
  - Fast checks in [`tools/tillr-gate/memory.go`](../../tools/tillr-gate/memory.go) already cache passing results by `treeFingerprint(declaration.Reads)` across candidate runs (`< 15s` total).
  - Heavy Go package tests in `tools/gate/go_affected.py` (`"remember": false` in [`tools/gate/checks/go-affected.json`](../../tools/gate/checks/go-affected.json)) **already implement cryptographic per-package input digesting** (`compute_package_verdict_digest`, hashing Go source files, `go.mod`/`go.sum`, transitive internal dependencies, `builds` binary edges, `env` edges, and `data` file edges at `head_sha`) and **`TILLR_GO_VERDICTS_IN` / `TILLR_GO_VERDICTS_OUT`**, verified by `tests/go-affected.test.py` — **but neither [`tools/tillr-loop/loop/loop.go`](../../tools/tillr-loop/loop/loop.go) nor [`tools/tillr-gate/change-gate.py`](../../tools/tillr-gate/change-gate.py) passes `TILLR_GO_VERDICTS_IN` / `TILLR_GO_VERDICTS_OUT` today**!
- **Message or event that carries it (`existing` env / artifact contract, zero new SQLite events):**
  - Maintain a store-scoped bounded JSON cache `<store_dir>/cache/go-package-verdicts.json` (`max = 2000` `(pkg, digest) -> passed_at` entries, atomic rename under file lock) wired into `tillr-loop` when invoking `change-gate.py` -> `go_affected.py` via **existing** `TILLR_GO_VERDICTS_IN` and `TILLR_GO_VERDICTS_OUT`.
  - **Crucial mid-gate abort enhancement (pairing with `347d54ef07`):** Update `go_affected.py` to flush passing `(pkg, digest)` entries incrementally as each package shard completes rather than only at process exit. When `347d54ef07` aborts a gate 25 minutes in because `main` moved on a Markdown file, every Go package that already passed in those 25 minutes is banked and skipped on the post-repair gate!
- **Cost per trunk move:**
  - **`0` (`$O(0)$ per trunk move).** Digesting runs once per `go_affected.py` invocation (`~120 ms` across packages) against the rebased candidate's own Git tree (`head_sha`). If `trunk_before..new_tip` and `-cfix` touched only Markdown or unrelated Go packages, `compute_package_verdict_digest` produces the exact same SHA-256 for every unaffected package (`100%` sound under [`landing-principles.md`](./landing-principles.md) [§3](./landing-principles.md#3-gate-what-will-be-the-tip-once)).
- **What it would have saved on 2026-11-25:**
  - *Standalone:* Across the `413.3m` of post-repair re-gates:
    - On docs/non-Go or disjoint-Go conflicts (`#8` [`65.4m`], `#18` [`38.2m`], plus post-13:30 re-gates on `#19`, `#21`, `#22`), **100% of Go package digests match**, collapsing `38–65m` Go re-gates to **`< 15s`** (`> 103m` saved on `#8` + `#18` alone).
    - On multi-package candidates where the conflict hit only one package (e.g., `#12` [`29.8m`], `#1` [`33.9m`], `#16` [`25.0m`]), unaffected reverse-dependency packages hit cache, saving another **`~65–90m`** — **`~170–195m` of post-repair gate time saved standalone**.
  - *Net of `eab5370855` + `347d54ef07`:* Saves **`~115–140m` of the `214.6m` post-repair re-gate time** (plus banking partial progress from `347d54ef07`'s `211.6m` aborted gates via incremental verdict flushing!).

### 4.6 Option 6: Keep front-of-gate-order after repair for a candidate whose gate passed before conflicting

- **Recorded fact that triggers it:**
  - Existing `gate.verdict` (`passed == true`) followed immediately by `land.landing` (`result == "squash-conflict"`), setting `Candidate.PriorGatePassed = true` onto the staged `-cfix` continuation.
- **Message or event that carries it (`existing` in-memory planner state, zero new SQLite events):**
  - Because commit `606b43306d` ([`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4)) removed `test_slots.go` so **every ready gate starts immediately with no gate queue**, there is no global gate-order queue to stand at the front of, and adding one is forbidden by [`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4).
  - Instead, the two lawful halves that prevent a repaired candidate from losing a second race (`#5`, `#14`, `eab5370855-a2-n3-cfix`) are:
    1. **Continuous `livePaths` hold across `-cfix` and re-gate (`Option 4`):** New items touching the conflicted candidate's files remain held in `ReasonPathHeld` until the repaired candidate lands or fails/parks.
    2. **Priority `-cfix` lane dispatch (`Option 2`) + cgroup `repair` / `gate` CPU share ([`OQ-GTC3`](./gate-test-capacity.md#OQ-GTC3)):** Ready `-cfix` continuations with `PriorGatePassed == true` claim the next free lane ahead of fresh unstarted items, and `Option 5` shrinks their re-gate from ~35 minutes to seconds when Go digests match.
- **Cost per trunk move:**
  - **`0` (`$O(0)$`)** under continuous per-path hold + `-cfix` dispatch priority; **unbounded cross-item throughput stall** (rejected) if implemented as a global gate lock.
- **What it would have saved on 2026-11-25:**
  - *Standalone (without Option 4 or Option 5):* **`0m` pre-conflict gate**, **`0m` first-round `-cfix`**, and at most **`1` of `3` repeat conflicts (`#14`, `50.2m`)** only if paired with per-path gate serialization between already-active `-cfix` siblings (`#9-cfix` vs `#11-cfix`). Ranked **#6 (last)** as a standalone mechanism; folded into `CAR-B1` and `CAR-B3`.

---

## 5. Ranking and Recommended Package

### 5.1 Side-by-side comparison and rank (`D-CAR1`)

| Rank | Option | Trigger (recorded fact) | Carrier (`existing` vs. `new`) | Cost per trunk move | Standalone 2026-11-25 saving | Net saving on top of `eab5370855` + `347d54ef07` | Recommendation |
| ---: | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **1** | **Option 2** — Mechanical repair first + warm-session `-cfix` | `drain.candidate` (`base_sha != main`, `head^`), `land.landing` (`squash-conflict`), `merge-tree` exit code | **Existing** `drain.candidate` & `run.allocate` (`SessionTurn = true`); `0` new events | **$O(0)$** (`< 10 ms` only on candidate gate/conflict) | `#12` eliminated (`59.7m` gate + `29.8m` re-gate); **`~115m` `-cfix` wall time** saved (`15` warm resumes) | `#12` (`32.7m` gate + `29.8m` re-gate) + **`~80m` `-cfix` wall time** (`115.8m -> ~35m`) | **Take now (`CAR-B1`)** — no owner question blocking |
| **2** | **Option 5** — Post-repair check & Go package verdict reuse | `gate.verdict` + `compute_package_verdict_digest` in `go_affected.py` | **Existing** `TILLR_GO_VERDICTS_IN`/`OUT` + store cache file; `0` new events | **$O(0)$** (`~120 ms` digesting per gate) | **`~170–195m` post-repair gate** (`38–65m` docs-conflict re-gates -> `< 15s`) | **`~115–140m` post-repair gate** + banks completed packages on `347d54ef07` mid-gate aborts | **Take now (`CAR-B2`)** — no owner question blocking |
| **3** | **Option 4** — Pre-lane hot-file serialization + hot-doc split | `items.allowed_paths`, `write_paths`, `runs`, `drain.candidate`, `conflict_paths` | **Existing** events + in-memory `livePaths` (`ReasonPathHeld`); doc split ([`OQ-CAR1`](#OQ-CAR1)) | **$O(\|\text{released paths}\|)$** (`< 1 ms`); `$O(0)$` for file split | Prevents up to **`18/22` conflicts** (`~530m` gate, `~150m` `-cfix`, `~340m` re-gate) | Prevents **`11–12/14` remaining conflicts** (`~191m` pre-abort gate, `~98m` `-cfix`, `~185m` re-gate) | **Take `livePaths` now (`CAR-B3`); split doc per [`OQ-CAR1`](#OQ-CAR1) (`CAR-B4`)** |
| **4** | **Option 1** (after `35c954f928`) — Filtered turn-gap overlap notices | `land.landing` (`trunk_before..after` $\cap$ lane edits) | **Existing** SQLite events + **new** `<lane>/trunk-overlap.json` on `35c954f928` hooks | **`< 15 ms`** Git check in drain; **`0` LLM turns** on disjoint moves | Catches **`4` build-window conflicts** (`65.8m` gate, `~37m` `-cfix`, `58.6m` re-gate) | Catches **`5` build-window conflicts** (`~35m` `-cfix`, `~88m` re-gate before Opt 4/5) | **Order after [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md) Slices 1–2 per [`OQ-CAR3`](#OQ-CAR3) (`CAR-B5`)** |
| **5** | **Option 3** — Gate-entry pairwise `merge-tree` | `drain.candidate` entering `verify` vs. `l.verifying` (`gate.started`) | **Existing** events + `ReasonInFlightConflict`; `0` new events | **$O(\|l.\text{verifying}\|)$** (`< 25 ms` at gate entry); adds wait latency on false alarms | Saves **`~390m` pre-conflict gate**; stops `#20`/`#22` leapfrogs (`0` `-cfix` saved) | **`~179m` pre-abort gate** before Opt 4, shrinking to **`~20.2m`** (`#20, #21`) after Opt 4 | **Defer (`B`) or restrict to gate-entry leapfrog hold (`A`) per [`OQ-CAR2`](#OQ-CAR2) (`CAR-B6`)** |
| **6** | **Option 6** — Front-of-gate-order after repair | `gate.verdict` (`passed`) + `land.landing` (`squash-conflict`) | Folded into `livePaths` retention (`CAR-B3`) + `-cfix` priority (`CAR-B1`) | **$O(0)$** (per-path); **forbidden** as global gate lock ([`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4)) | `0m` pre-conflict gate; at most `1` repeat conflict (`#14`, `50.2m`) | Absorbed by continuous `livePaths` hold (`CAR-B3`) + `CAR-B1` + `CAR-B2` | **Reject global gate lock; fold per-path hold & `-cfix` priority into `CAR-B1` / `CAR-B3`** |

### 5.2 Combined effect of the recommended package (`CAR-B1` through `CAR-B5`)

Layered onto `eab5370855` + `347d54ef07` across the 2026-11-25 workload:

1. **Prevention (`eab5370855` + `CAR-B3` + `CAR-B4`):** Eliminates `8` stale-base conflicts (`eab5370855`) and prevents `11`–`12` of the remaining `14` conflicts before a second worker ever allocates a lane (`CAR-B3` `livePaths` + `CAR-B4` [`tillr-utility-contracts.md`](./tillr-utility-contracts.md) split), leaving only **`2`–`3` undeclared incidental-path collisions** (`#20`, `#21`) out of `22`.
2. **Early detection (`347d54ef07` + `CAR-B5`):** Any undeclared collision during build gets a warm turn-gap notice (`CAR-B5`), and any collision during gate aborts immediately (`347d54ef07`) while flushing already-passed Go package digests (`CAR-B2`).
3. **Near-free repair (`CAR-B1` + `CAR-B2`):** Clean rebases (`#12`) finish in `< 10 ms` with `0` LLM turns; real textual conflicts resume the worker's warm session in **`~3m`** instead of `11m` cold (`CAR-B1`); and post-repair re-gates reuse unchanged Go package digests (`CAR-B2`), finishing in **`< 15s`** on docs/config repairs (`#21`) and testing only the single touched package on Go repairs (`#20`). Total conflict tax across the 8-hour window drops from **`1,225.4` gate+agent minutes (`22` conflicts) to `< 35` minutes (`2`–`3` minor repairs)**.

---

## 6. Open Questions

1. 💬 **OQ-CAR1: How should hot files (`docs/design/tillr-utility-contracts.md` and gate edge declarations) be split versus serialized in the queue (`Option 4`)?**

   On 2026-11-25, [`docs/design/tillr-utility-contracts.md`](./tillr-utility-contracts.md) appeared in `9` of the `22` conflicts (`10` including [`tillr-utility-contracts/tillr-run.md`](./tillr-utility-contracts/tillr-run.md)) because every utility's contract item edited the parent file's shared Decision Ledger and reach-in/slice tables, even though per-utility files already live under `docs/design/tillr-utility-contracts/<utility>.md`. Meanwhile, individual gate checks already live in separate `tools/gate/checks/<id>.json` files, while `tools/gate/go-test-edges.json` appeared in `3` conflicts. Exact-file queue serialization (`CAR-B3`, `D-SPS1`–`D-SPS7`) prevents conflicts on any shared file, but serializing [`tillr-utility-contracts.md`](./tillr-utility-contracts.md) forces otherwise independent Go utilities (`tillr-run`, `tillr-loop`, `tillr-roll`, `tillr-drain`) to wait on one another in the queue.

   - **A — Split `docs/design/tillr-utility-contracts.md`'s per-utility tables into `docs/design/tillr-utility-contracts/<utility>.md`, and serialize remaining shared files (`go-test-edges.json`, `watch/*.go`) via `livePaths`.** Each utility's `D-UC*` ledger rows, reach-ins, and build-slice status live in its own `docs/design/tillr-utility-contracts/<utility>.md` file; the parent document keeps only the cross-utility rules (`R-UC1`, `D-UC1`–`D-UC15`) and links to the thirteen utility files. Independent utility builds run in parallel with zero conflicts, while `go-test-edges.json` stays a single file serialized by `livePaths`.
   - **B — Queue serialization only (`livePaths`), with no document or file splits.** Leave `docs/design/tillr-utility-contracts.md` and `tools/gate/go-test-edges.json` as single files; `CAR-B3` holds a second writer in `ReasonPathHeld` until the first lands. Zero doc churn, at the cost of serializing concurrent utility-contract items.
   - **C — Split both `docs/design/tillr-utility-contracts.md` (as in A) and `tools/gate/go-test-edges.json` (into `tools/gate/edges/<module>.json` fragments merged by `go_affected.py`).** Removes cross-module queue holds on `go-test-edges.json` as well, at the cost of changing `go_affected.py` and edge-lint tests to load a directory of edge files.

   <!-- vantage: question id=OQ-CAR1 leaning="A — per-utility contract sub-documents already exist, and moving per-utility ledger/slice rows into them removes 9 of 22 conflicts without serializing unrelated utility builds or changing go_affected.py's edge loader." -->

   _Leaning:_ A — per-utility contract sub-documents already exist, and moving per-utility ledger/slice rows into them removes 9 of 22 conflicts without serializing unrelated utility builds or changing `go_affected.py`'s edge loader.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-CAR2: Should the drain also run a gate-entry pairwise `git merge-tree` check against already-gating candidates (`Option 3`), or omit it once `livePaths` (`Option 4`), `347d54ef07`, `Option 2`, and `Option 5` are in place?**

   When a fast newcomer edits an undeclared file already touched by a slow Go candidate currently in `Loop.verify` (as in `#20`, where a `3.0m` gate on `variables_test.go` leapfrogged `8fdabc44ba-a4-n3`'s `38.4m` gate), `livePaths` misses the undeclared path. Checking `git merge-tree --write-tree` at gate entry against `l.verifying` catches that leapfrog and holds the newcomer (`ReasonInFlightConflict`) until the running gate finishes — **but** if the running gate fails on its own bug, the newcomer waited up to ~35 minutes for a conflict that never landed (the throughput trade-off flagged in [`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4)).

   - **A — Omit `Option 3` initially; rely on `CAR-B1`–`CAR-B3` + `347d54ef07`, and revisit only if undeclared-path leapfrogs recur.** Keeps Schedule A pure (`every ready gate starts immediately`), avoids false-alarm gate waits when an ahead candidate fails its gate, and handles the rare undeclared collision (`2` of `22` on 2026-11-25) via `347d54ef07` abort + `CAR-B1` warm repair + `CAR-B2` incremental Go verdict reuse.
   - **B — Enable `Option 3` strictly at gate entry against `l.verifying` (`ReasonInFlightConflict`), never against uncommitted building worktrees.** Prevents a late fast gate from leapfrogging and aborting an already-running slow gate on undeclared overlapping files, releasing immediately if the ahead gate fails.
   - **C — Enable `Option 3` only when the ahead gating candidate has already run longer than a threshold (e.g., `5` minutes of heavy Go testing) and the newcomer's predicted gate is fast.** Protects sunk heavy-gate investment against fast-gate leapfrogs (`#20`, `#22`) while avoiding symmetric waits between peers.

   <!-- vantage: question id=OQ-CAR2 leaning="A — after pre-lane livePaths (CAR-B3), warm repair (CAR-B1), and incremental Go verdict reuse (CAR-B2), only ~20 gate-minutes across 2 undeclared overlaps remain on the 2026-11-25 record, which does not justify holding ready gates behind ahead gates that may fail." -->

   _Leaning:_ A — after pre-lane `livePaths` (`CAR-B3`), warm repair (`CAR-B1`), and incremental Go verdict reuse (`CAR-B2`), only `~20` gate-minutes across `2` undeclared overlaps remain on the 2026-11-25 record, which does not justify holding ready gates behind ahead gates that may fail.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-CAR3: On a `path-overlap` turn-gap notice (`Option 1`, ordered onto [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md)), should the running worker rebase opt-in while working, be required to rebase before `land`, or leave rebasing to the loop?**

   [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md) ([`OQ-WR2`](./tillr-green-trunk-worker-rebase.md#OQ-WR2)) asks whether `trunk-green.json` notices expose an opt-in `rebase_trunk` (`tillr-run lane-rebase`) tool backed by loop fallback; `Option 1` poses the twin policy choice when a sibling landing touches files the running worker has already edited (`<lane>/trunk-overlap.json`). Forcing an immediate rebase mid-node can collide with half-edited uncommitted files, whereas keeping `rebase_trunk` opt-in lets a warm worker reconcile (`--reconcile`) when convenient while `CAR-B1` catches anything left un-rebased at node exit.

   - **A — Opt-in turn-gap notice + `rebase_trunk` (`tillr-run lane-rebase`, including `--reconcile`) while working, backed by automatic mechanical loop rebase between nodes and before `gate.started` (`CAR-B1`).** Matches [`OQ-WR2`](./tillr-green-trunk-worker-rebase.md#OQ-WR2) Option A: the worker receives the `path-overlap` notice at its next turn gap and *may* rebase onto `target_sha` with warm context before calling `land`; if it finishes on its current base instead, `CAR-B1` mechanically merges onto `main` before gating (`0` LLM turns if hunks are disjoint, warm `SessionTurn = true` resume if hunks conflict).
   - **B — Mandatory worker rebase before `land` settles whenever `<lane>/trunk-overlap.json` is unapplied.** `land` (`land.ts`, `claude-landing`, and `keep-going.ts`) refuses to settle while `effective_base_sha` lags `<lane>/trunk-overlap.json`'s `target_sha`, forcing the running `pi` or `claude` worker to reconcile onto `target_sha` in-turn before offering a candidate (with `CAR-B1` still covering `muse` and `agy`).
   - **C — Loop-only rebase between nodes and before gate (`CAR-B1` only); omit `<lane>/trunk-overlap.json` mid-run notices.** Workers are never told about overlapping sibling landings mid-turn; `CAR-B1` rebases mechanically after the node finishes and resumes the warm session only if hunks actually conflict.

   <!-- vantage: question id=OQ-CAR3 leaning="A — many same-file edits touch disjoint hunks that CAR-B1 merges in <10 ms with 0 LLM turns, while opt-in rebase_trunk lets a warm worker adapt immediately when an upstream signature or table change affects its work." -->

   _Leaning:_ A — many same-file edits touch disjoint hunks that `CAR-B1` merges in `< 10 ms` with `0` LLM turns, while opt-in `rebase_trunk` lets a warm worker adapt immediately when an upstream signature or table change affects its work.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 7. Decision Ledger

Every row below (`D-CAR1`–`D-CAR8`) is a **designer decision (`2026-11-25`) made from operator request `item-request-8972f1b9b7`, not an owner ruling; the owner may overturn any of them**. Standing owner rulings ([`landing-principles.md`](./landing-principles.md), [`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4), [`D-GR55`](../reference/gate.md#D-GR55), [`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger)) and sibling designer proposals ([`D-WR1`](./tillr-green-trunk-worker-rebase.md#D-WR1)–[`D-WR8`](./tillr-green-trunk-worker-rebase.md#D-WR8)) remain in their home ledgers. `Built` is checked at `7a09eeb863d7`.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-CAR1"></a>**[D-CAR1](#D-CAR1)** | **Designer decision:** Rank the six options net of `eab5370855` and `347d54ef07` as: **1. Option 2** (mechanical rebase + warm-session `-cfix`), **2. Option 5** (Go package verdict digest reuse + incremental flush), **3. Option 4** (`livePaths` pre-lane serialization + [`tillr-utility-contracts.md`](./tillr-utility-contracts.md) split per [`OQ-CAR1`](#OQ-CAR1)), **4. Option 1** (ordered after [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md) Slices 1–2 for path-filtered `<lane>/trunk-overlap.json` notices per [`OQ-CAR3`](#OQ-CAR3)), **5. Option 3** (deferred or gate-entry-only per [`OQ-CAR2`](#OQ-CAR2)), and **6. Option 6** (rejected as a global gate lock under [`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4); folded into continuous `livePaths` hold across `-cfix` and priority `-cfix` lane dispatch). | 2026-11-25, designer | [§4](#4-weighing-the-six-options), [§5](#5-ranking-and-recommended-package) | — |
| <a id="D-CAR2"></a>**[D-CAR2](#D-CAR2)** | **Designer decision:** Before `gate.started` and on `squash-conflict`, compute `effective_base` (`head_sha^` when `is-ancestor(base_sha, head_sha^)` and `is-ancestor(head_sha^, main)`, else `base_sha`) and run `git merge-tree --write-tree --merge-base=<effective_base> main <head_sha>`. When exit `0`, create the rebased commit via `git commit-tree` with **zero LLM runs**; when exit `1`, preserve `<lane>/worker-session.json`, materialize conflict markers via `git cherry-pick --no-commit <effective_base>..<head_sha>` onto `main`, and resume `-cfix` with `fix.SessionTurn = true` at `Priority: 0` ahead of fresh ordinary starts. | 2026-11-25, designer | [§4.2](#42-option-2-mechanical-repair-first-and-warm-session-resume-on-real-conflicts), [§8 (`CAR-B1`)](#8-standalone-build-items) | — |
| <a id="D-CAR3"></a>**[D-CAR3](#D-CAR3)** | **Designer decision:** Wire `go_affected.py`'s existing `compute_package_verdict_digest` cache (`TILLR_GO_VERDICTS_IN` / `TILLR_GO_VERDICTS_OUT`) through `tillr-loop` (`<store_dir>/cache/go-package-verdicts.json`, bounded LRU) and flush completed `(pkg, digest)` passes incrementally as each package finishes so mid-gate `347d54ef07` aborts preserve completed package verdicts for the post-repair gate. | 2026-11-25, designer | [§4.5](#45-option-5-post-repair-check-and-go-package-verdict-reuse), [§8 (`CAR-B2`)](#8-standalone-build-items) | — |
| <a id="D-CAR4"></a>**[D-CAR4](#D-CAR4)** | **Designer decision (ordering `Option 1` after `35c954f928`):** Reuse [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md)'s `tillr-run lane-rebase`, `rebased-base.json` durable receipt, and Pi/Claude turn-gap hooks (`D-WR2`–`D-WR5`), adding `<lane>/trunk-overlap.json` written atomically by `tillr-drain` only when `land.landing`'s changed paths intersect an active writing lane's modified or untracked files (`0` new SQLite event kinds; optional additive `changed_paths` on `land.landing`). | 2026-11-25, designer | [§2.2](#22-ordering-option-1-after-item-request-35c954f928-tillr-green-trunk-worker-rebasemd), [§4.1](#41-option-1-ordered-after-35c954f928-turn-gap-notices-to-a-running-worker), [§8 (`CAR-B5`)](#8-standalone-build-items) | — |
| <a id="D-CAR5"></a>**[D-CAR5](#D-CAR5)** | **Designer decision:** Build exact-path pre-lane `livePaths` serialization (`D-SPS1`–`D-SPS7` in [`tillr-shared-path-serialization.md`](./tillr-shared-path-serialization.md)) with continuous path retention across `verify` and `-cfix` repair until `landed` or `parked`/`blocked`, satisfying the per-path non-overtaking goal of `Option 6` without introducing a global gate lock. | 2026-11-25, designer | [§4.4](#44-option-4-pre-lane-hot-file-serialization-and-splitting-hot-files), [§4.6](#46-option-6-keep-front-of-gate-order-after-repair-for-a-candidate-whose-gate-passed-before-conflicting), [§8 (`CAR-B3`)](#8-standalone-build-items) | — |
| <a id="D-CAR6"></a>**[D-CAR6](#D-CAR6)** | **Designer decision:** Reject pairwise `merge-tree` polling across uncommitted building worktrees (violates the trunk-activity scaling rule and holds finished candidates behind uncommitted workers); leave gate-entry-only pairwise hold to [`OQ-CAR2`](#OQ-CAR2). | 2026-11-25, designer | [§3](#3-checked-against-the-landing-principles-and-gate-batching-ruling), [§4.3](#43-option-3-pairwise-merge-tree-across-in-flight-candidates) | — |
| <a id="D-CAR7"></a>**[D-CAR7](#D-CAR7)** | **Designer decision:** Reject any global front-of-gate-order lock for repaired candidates (`Option 6` literal form) as incompatible with [`OQ-GTC4`](./gate-test-capacity.md#OQ-GTC4) (`606b43306d`). | 2026-11-25, designer | [§3](#3-checked-against-the-landing-principles-and-gate-batching-ruling), [§4.6](#46-option-6-keep-front-of-gate-order-after-repair-for-a-candidate-whose-gate-passed-before-conflicting) | — |
| <a id="D-CAR8"></a>**[D-CAR8](#D-CAR8)** | **Designer decision:** Ship the work as standalone single-commit build items (`CAR-B1`–`CAR-B3` unblocked; `CAR-B4`–`CAR-B6` gated on [`OQ-CAR1`](#OQ-CAR1)–[`OQ-CAR3`](#OQ-CAR3)) obeying [`landing-principles.md`](./landing-principles.md) [§15](./landing-principles.md#15-land-the-smallest-functional-change). | 2026-11-25, designer | [§8](#8-standalone-build-items) | — |

---

## 8. Standalone Build Items

Items `CAR-B1`, `CAR-B2`, and `CAR-B3` do **not** wait on [`OQ-CAR1`](#OQ-CAR1)–[`OQ-CAR3`](#OQ-CAR3) and can be filed and built immediately in parallel (touching disjoint packages except `CAR-B1`/`CAR-B3` in `tools/tillr-drain/drain/`, ordered `CAR-B1` then `CAR-B3`).

### 8.1 `CAR-B1`: Mechanical rebase onto `main` with effective-base detection, warm-session `-cfix` resume, and priority `-cfix` dispatch (`Option 2` + `Option 6` dispatch half)

- **Scope:** `tools/tillr-drain/drain/{conflict.go,loop.go,plan.go}` (+ `just pack-sync` mirrors) and unit tests in `tools/tillr-drain/drain/`.
- **Blocked on:** Nothing.
- **Testable acceptance criteria:**
  1. When a candidate enters `Loop.verify` or `conflictFix` with `base_sha != refs/heads/main`, `effectiveBase` selects `head_sha^` whenever `is-ancestor(base_sha, head_sha^)` and `is-ancestor(head_sha^, refs/heads/main)` (covering worker-rebased commits like `#12`), and `base_sha` otherwise.
  2. When `git merge-tree --write-tree --merge-base=<effectiveBase> refs/heads/main <head_sha>` exits `0`, `Loop` creates a single rebased commit (`git commit-tree` with parent `refs/heads/main`), updates `<lane>/base_sha` and appends `drain.candidate`, and enters `Loop.verify` **with `0` `run.allocate` (`-cfix`) dispatches** (removing the LLM run on `moved != ""` in `conflictFix`).
  3. When `merge-tree` exits `1`, `conflictFix` preserves `<lane>/worker-session.json` (never deleting or bypassing it via `reason = "trunk-conflict"`), checks out `refs/heads/main`, applies `git cherry-pick --no-commit <effectiveBase>..<head_sha>` so unmerged conflict markers are present in `<lane>/worktree`, and sets `fix.SessionTurn = true` (`resume_decision = "resumed-worker"`) whenever `prepareWorkerResume` validates the candidate's session.
  4. In `PlanPass`, a ready `-cfix` run whose candidate had `PriorGatePassed == true` claims an available ordinary lane ahead of unstarted ordinary items.
  5. Unit tests cover clean mechanical rebase (`0` agent runs), worker-rebased `head_sha^` (`0` conflicts, `0` agent runs), textual conflict preserving `SessionTurn = true` with conflict markers in the worktree, fallback-free refusal if session file is corrupt/missing (`session-missing` starts fresh on the conflicted tree), and `-cfix` lane priority.

### 8.2 `CAR-B2`: Store-scoped Go package verdict reuse across gates and conflict repairs, with incremental shard flushing (`Option 5`)

- **Scope:** `tools/tillr-loop/loop/loop.go`, `tools/tillr-gate/change-gate.py`, `tools/gate/change_gate.go`, `tools/gate/go_affected.py`, `tests/go-affected.test.py`, and unit tests in `tools/tillr-loop/loop/` (+ `just pack-sync` mirrors).
- **Blocked on:** Nothing.
- **Testable acceptance criteria:**
  1. `tillr-loop` passes `TILLR_GO_VERDICTS_IN` and `TILLR_GO_VERDICTS_OUT` pointing to `<store_dir>/cache/go-package-verdicts.json` (bounded to `2000` `(pkg, digest)` entries, written atomically under `flock` so concurrent `Schedule A` gates merge entries safely without corrupting or dropping sibling verdicts).
  2. `go_affected.py` flushes each passing `(pkg, digest)` entry into `TILLR_GO_VERDICTS_OUT` as soon as that package completes (before process exit), so if `347d54ef07` cancels a running gate via `SIGTERM`, already-passed packages remain cached.
  3. Re-gating a candidate after a Markdown-only conflict repair (or after `main` moved on unrelated packages) hits cache on `100%` of unchanged Go package digests and executes `0` `go test` subprocesses for those packages, while modifying any file in a package's declared source/dependency/`builds`/`data`/`env` closure invalidates its digest and re-runs `go test`.
  4. Automated tests in `tests/go-affected.test.py` and `tools/tillr-loop/loop/` verify cache hit after docs-only rebase, cache invalidation on Go/edge edit, concurrent two-gate atomic merge of `go-package-verdicts.json`, and partial-cache preservation across `SIGTERM` mid-run abort.

### 8.3 `CAR-B3`: Pre-lane `livePaths` serialization with continuous hold across gate and `-cfix` repair (`Option 4` queue half + `Option 6` path-hold half)

- **Scope:** Implementation of [`tillr-shared-path-serialization.md`](./tillr-shared-path-serialization.md) (`D-SPS1`–`D-SPS7`) in `tools/tillr-drain/drain/` and `tools/tillr-view/` (+ `just pack-sync` mirrors), explicitly retaining an item's `livePaths` across `verify` and `-cfix` until `landed` or `parked`/`blocked`.
- **Blocked on:** Nothing (builds `D-SPS1`–`D-SPS7` as already specified).
- **Testable acceptance criteria:**
  1. `PlanPass` computes `livePaths` (with 1-to-1 `yolo-packs/tillr/` mirror canonicalization) from active writing runs, committed candidates in `verify`/landing, and unresolved `-cfix` continuations (`conflict_paths` union committed diff paths).
  2. A second item whose resolved write paths intersect `livePaths` is held pre-lane (`ReasonPathHeld`, `DeferredBy` naming the holder, `0` lanes allocated, `0` `run.allocate` events), while disjoint items allocate lanes and start gates with zero delay.
  3. When a candidate hits `squash-conflict` and stages `-cfix`, its `livePaths` remain held uninterrupted until the repaired candidate lands or parks/blocks, preventing a late fast item (like `5fcba81a2a-a10` in `#22`) from starting on the contended file during repair.
  4. `tillr-drain why <item>` and `tillr view waiting` print `ReasonPathHeld`, the holding `item_id`, and the contended canonical paths.

### 8.4 `CAR-B4`: Split per-utility ledger and slice tables out of `docs/design/tillr-utility-contracts.md` (`Option 4` layout half)

- **Scope:** [`docs/design/tillr-utility-contracts.md`](./tillr-utility-contracts.md) and `docs/design/tillr-utility-contracts/<utility>.md` (plus proposed `tools/gate/edges/<module>.json` fragments and `tools/gate/go_affected.py` only if [`OQ-CAR1`](#OQ-CAR1) chooses `C`).
- **Blocked on:** Owner ruling on [`OQ-CAR1`](#OQ-CAR1) (`A` or `C`; skipped if `B`).
- **Testable acceptance criteria:**
  1. Per-utility `D-UC*` decision rows, reach-in inventories, and slice status tables live in `docs/design/tillr-utility-contracts/<utility>.md`, while [`docs/design/tillr-utility-contracts.md`](./tillr-utility-contracts.md) holds cross-utility rules and links to each utility sub-document.
  2. Every touched Markdown file passes `uvx vantage-check`, `tools/gate/repo-gate` (`documents`, `citations`, `design-citations`), and `tillr-docs`.

### 8.5 `CAR-B5`: Filtered `<lane>/trunk-overlap.json` turn-gap delivery (`Option 1`, ordered after `35c954f928`)

- **Scope:** `tools/tillr-drain/drain/`, `tools/tillr-land/land/landing.go` (optional additive `changed_paths`), `tools/tillr-run/` (`lane-rebase` authorization check, `backends/pi/` extension, `claude/landing.go`) (+ `just pack-sync` mirrors).
- **Blocked on:** [`tillr-green-trunk-worker-rebase.md`](./tillr-green-trunk-worker-rebase.md) Slices 1–2 (`tillr-run lane-rebase`, `rebased-base.json`, Pi/Claude turn-gap hooks) and owner ruling on [`OQ-CAR3`](#OQ-CAR3) (`A` or `B`; skipped if `C`).
- **Testable acceptance criteria:**
  1. On `land.landing` (`result == "landed"`), `tillr-drain` writes `<lane>/trunk-overlap.json` (`seq`, `target_sha`, `overlapping_paths`) atomically outside `<lane>/worktree` **only** for active writing lanes whose modified/untracked files intersect `changed_paths` (`0` files written or prompts injected on disjoint lanes).
  2. `tillr-run lane-rebase` (`rebase_trunk` tool on `pi` and `claude`, including `--reconcile`) accepts `<lane>/trunk-overlap.json`'s `target_sha` alongside `<lane>/trunk-green.json`'s `target_sha` when validating and recording `rebased-base.json`.
  3. The Pi turn-gap extension and Claude `PostToolUse` `additionalContext` hook surface unread `<lane>/trunk-overlap.json` targets once per `target_sha` (`trunk-overlap-ack.json`, `0` mid-tool kills), enforcing opt-in (`A`) or mandatory pre-`land` (`B`) rebase per [`OQ-CAR3`](#OQ-CAR3).

### 8.6 `CAR-B6` (Conditional): Gate-entry pairwise `merge-tree` hold against `l.verifying` (`Option 3`)

- **Scope:** `tools/tillr-drain/drain/{loop.go,why.go}` (+ `just pack-sync` mirrors).
- **Blocked on:** Built **only if** the owner chooses `B` or `C` on [`OQ-CAR2`](#OQ-CAR2) (omitted under leaning `A`).
- **Testable acceptance criteria:**
  1. Before `gate.started`, a newly committed candidate checks `git merge-tree --write-tree` against active candidates in `l.verifying` (and elapsed-time filter if [`OQ-CAR2`](#OQ-CAR2) = `C`), never polling uncommitted building worktrees.
  2. On non-zero `merge-tree`, the newcomer holds in `ReasonInFlightConflict` (`DeferredBy` naming the ahead gating item) and wakes immediately when the ahead gate either lands (rebasing via `CAR-B1` before gating) or fails its gate (starting its own gate immediately).
