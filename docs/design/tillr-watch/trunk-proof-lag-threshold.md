---
title: "Set the trunk-proof-lag alert threshold from the measured wait of a landed commit for its green proof"
author: "Agent"
date: 2026-11-26
status: in-review
stage: DECIDED
stage-since: 2026-11-26
tags: [tillr, watch, drain, proof, alert, design]
summary: "Amend D-TW54 backward-only so trunk-proof-lag sets threshold_duration = \"95m\" from the measured wait of a landed commit to its covering green proof under a working heavy runner (up to 5442.2s / 90.7m), with the measurement command, result, and date recorded in the rule header comment."
next: "Build the single follow-on slice updating config/tillr/watch/trunk-proof-lag.toml and the threshold assertions in tools/tillr-watch/trunk_proof_rule_test.go and tools/tillr-watch/item_fault_rule_test.go (with just pack-sync); the owner may review D-TW-TPL1."
depends-on:
  - ../tillr-watch.md
  - ../tillr-watch.md#D-TW41
  - ../tillr-watch.md#D-TW42
  - ../tillr-watch.md#D-TW54
  - ../tillr-watch.md#D-TW73
  - ../tillr-watch.md#D-TW75
  - ../tillr-watch.md#D-TW83
  - ../tillr-watch.md#D-TW85
  - ./decision-record-files.md#R-TW-DR1
  - ./decision-record-files.md#D-TW-DR1
  - ../landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it
  - ../landing-principles.md#15-land-the-smallest-functional-change
related:
  - ../tillr-utility-contracts/tillr-watch.md
  - ../trunk-proof-survives-restart.md
vantage:
  status-chip: true
---

# Set the `trunk-proof-lag` alert threshold from the measured wait of a landed commit for its green proof

**Status:** 2026-11-26, verified at `60f79791fd`. [`D-TW-TPL1`](#D-TW-TPL1) is not built yet (`rg -n 'threshold_duration|15m proofs' config/tillr/watch/trunk-proof-lag.toml` still matches `normal ~15m proofs` and `threshold_duration = "30m"`). Per owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`685934ce`) and [`D-TW-DR1`](./decision-record-files.md#D-TW-DR1), this standalone record amends [`D-TW54`](../tillr-watch.md#D-TW54) (and the `30m` / `30 minutes` `trunk-proof-lag` duration stated in [`§5.3`](../tillr-watch.md#53-which-rules-belong-to-tillr-and-which-stay-with-a-repository-d-tw83), [`§5.4`](../tillr-watch.md#54-the-ten-drain-conditions-d-tw39), and [`§5.9`](../tillr-watch.md#59-trunk-proof-lag-and-stalled-heavy-proof-progress-d-tw54) of [`../tillr-watch.md`](../tillr-watch.md)) backward-only with zero edits to [`../tillr-watch.md`](../tillr-watch.md).

> **In short.** Because post-merge heavy proofs run one at a time, a commit that lands right after a proof starts waits for both the in-flight proof and the next proof before `refs/tillr/green` covers it. Raising `trunk-proof-lag`'s `threshold_duration` from `"30m"` to `"95m"` — derived from recorded `land.landing` to covering green `drain.heavy-progress` history (`5442.2s` / `90.7m` maximum under an uninterrupted working heavy runner) — stops false alarms during healthy back-to-back proofs while leaving `trunk-proof-stalled` (`10m`) and red/cancelled backlog detection intact.

**Why it matters.** On 2026-11-24 `trunk-proof-lag` fired three times in 25 minutes with no fault behind it, and on 2026-11-25 it fired twice more in this repository (`33.9m` and `60.8m` waits) while every proof check was passing ([§1.1](#11-the-2026-11-24-and-2026-11-25-false-alert-incidents-and-the-definition-conflict)).

**The shape.** Keep `lag_behind` and every other rule key unchanged, set `threshold_duration = "95m"` on `trunk-proof-lag` in [`config/tillr/watch/trunk-proof-lag.toml`](../../../tools/tillr-watch/rules/trunk-proof-lag.toml), and record the read-only measurement command, result, and date in the header comment above `trunk-proof-lag` in place of the `"normal ~15m proofs"` assumption ([§2](#2-measured-wait-to-covering-green-proof-and-settled-threshold-d-tw-tpl1)).

**Cost.** One small build commit touching [`config/tillr/watch/trunk-proof-lag.toml`](../../../tools/tillr-watch/rules/trunk-proof-lag.toml) and the threshold assertions in [`tools/tillr-watch/trunk_proof_rule_test.go`](../../../tools/tillr-watch/trunk_proof_rule_test.go) and [`tools/tillr-watch/item_fault_rule_test.go`](../../../tools/tillr-watch/item_fault_rule_test.go) plus `just pack-sync`, with zero engine, schema, or heavy-runner changes ([§3](#3-build-scope-under-change-nothing-else-and-candidate-note-d-tw-tpl1)).

**Start at [§2](#2-measured-wait-to-covering-green-proof-and-settled-threshold-d-tw-tpl1)** for the recorded wait measurement and updated TOML header, and [§3](#3-build-scope-under-change-nothing-else-and-candidate-note-d-tw-tpl1) for the build boundary and out-of-scope candidate note.

**Needs your ruling:** None. Owner rulings [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), and [`D-TW73`](../tillr-watch.md#D-TW73) are already settled; [`D-TW-TPL1`](#D-TW-TPL1) comes from the filed operator request and the designer and may be overturned by the owner.

**Reads with:** [`../tillr-watch.md`](../tillr-watch.md) (baseline watch design and [`D-TW54`](../tillr-watch.md#D-TW54)), [`./decision-record-files.md`](./decision-record-files.md) (one-file-per-record rule), and [`../landing-principles.md`](../landing-principles.md) (measurement-backed thresholds and smallest functional change).

---

## 1. Incidents, Definition Conflict, and Authority Boundary

### 1.1 The 2026-11-24 and 2026-11-25 false-alert incidents and the definition conflict

In `gitLagWith` ([`tools/tillr/watch/git.go`](../../../tools/tillr/watch/git.go)), `lag_behind` computes `elapsed` from the oldest commit timestamp in `git rev-list --timestamp refs/tillr/green..refs/heads/main --`. Because `HeavyRunner` runs one post-merge trunk proof at a time against the `refs/heads/main` tip observed when the proof starts, a commit `C` that lands right after proof `P_1` starts is not in `P_1`'s `tip_sha`; `C` waits for `P_1` to finish and then waits for all of `P_2` before `refs/tillr/green` advances past `C`.

Under [`D-TW54`](../tillr-watch.md#D-TW54) (`threshold_duration = "30m"`, predicated on the comment `"normal ~15m proofs"` in [`config/tillr/watch/trunk-proof-lag.toml`](../../../tools/tillr-watch/rules/trunk-proof-lag.toml)), that two-proof wait repeatedly false-fired while the heavy runner was healthy:

1. **2026-11-24 incident (filed operator request):** Between `08:29` and `08:54 ET`, `trunk-proof-lag` fired three times (`alrt-4f97bc6122e5dc79287798f3e54cc223`, `alrt-e6ad3866280c4acb2230a19e7e6e1d06`, and an earlier instance) with zero fault behind it. Each time the heavy runner was actively progressing and the preceding proof had ended green minutes earlier (`08:16:42` and `08:41:52 ET`), with single proofs taking `9` to `25` minutes (`08:16:43` to `08:41:52 ET` = `25m 09s`). Two consecutive healthy proofs therefore span up to `~50` minutes, pushing the oldest unproved commit past `30m`.
2. **2026-11-25 recurrence in this repository (`.tillr/state.db`):** On 2026-11-25, `trunk-proof-lag` opened two more false episodes settled as `fix-rule` with no underlying fault:
   - `alrt-dc3e20f6de6b31072fcb994fb217bf75` (`14:59:37 ET` / `19:59:37Z`): `d550cd942c` landed at `19:29:37Z` during the green proof of `43455fc4dd` (`19:24:21Z`–`19:40:55Z`, `16.6m`) and was covered green by `d52831b6b9` (`19:40:58Z`–`20:03:32Z`, `22.6m`) after `2035.1s` (`33.9m`).
   - `alrt-843fa40ae1594fb6ca71f5342a4b4458` (`15:11:01 ET` / `20:11:01Z`): `7a09eeb863` landed at `19:41:01Z` (`3s` after `d52831b6b9`'s proof started at `19:40:58Z`), waited through both `d52831b6b9` (`22.6m`) and `273bd92ecf` (`20:03:39Z`–`20:41:47Z`, `38.1m`), and was covered green after `3646.0s` (`60.8m`).

The filed operator request asks to set `trunk-proof-lag`'s `threshold_duration` from a measurement of how long a landed commit waited to be proved green in the recorded history (`land.landing` to the `drain.heavy-progress` `"ended"`/`"green"` event whose proof covers it), put the measurement command, result, and date in the comment above `trunk-proof-lag` the way [`config/tillr/watch/drain-restart-unanswered.toml`](../../../tools/tillr-watch/rules/drain-restart-unanswered.toml) does, replace the `"~15m proofs"` sentence, use `lag_behind` as it is, and `"Change nothing else"`.

The first build run stopped with `OBSTACLE: definition` on criteria `c1` and `c5` because [`../tillr-watch.md`](../tillr-watch.md) settles [`D-TW54`](../tillr-watch.md#D-TW54) at `30 minutes` (`Built: yes`, repeated in [`§5.3`](../tillr-watch.md#53-which-rules-belong-to-tillr-and-which-stay-with-a-repository-d-tw83), [`§5.4`](../tillr-watch.md#54-the-ten-drain-conditions-d-tw39), and [`§5.9`](../tillr-watch.md#59-trunk-proof-lag-and-stalled-heavy-proof-progress-d-tw54)), and existing rule tests in [`tools/tillr-watch/`](../../../tools/tillr-watch/) pin the `30m` firing boundary.

### 1.2 Authority boundary: standing owner rulings versus designer decision

Four verbatim owner rulings bound this record without being altered by it:

- [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`2026-11-25`, `"ok, so save that"`): every new decision record about `tillr-watch` goes in its own small file under `docs/design/tillr-watch/`, with zero edits to [`../tillr-watch.md`](../tillr-watch.md).
- [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2) (`2026-11-16`, `"Explicitly, we NEVER want fallbacks anywhere in this project."`): no fallback path or silent degradation.
- [`OQ-TW2`](../tillr-watch.md#OQ-TW2) (`2026-11-21`, `"We don't cover issues. We use this to find the issues and fix them."`): `tillr-watch` executes no automatic remedy.
- [`D-TW73`](../tillr-watch.md#D-TW73) (`2026-11-22`, `"I never want alerts for expected conditions."`): `trunk-proof-lag` must not fire during healthy back-to-back proofs, and `[rule.guidance]` never calls any firing expected.

Everything settled below — [`D-TW-TPL1`](#D-TW-TPL1), including the covering-proof measurement query, the `"95m"` `threshold_duration` amending [`D-TW54`](../tillr-watch.md#D-TW54) backward-only, the header comment in [`config/tillr/watch/trunk-proof-lag.toml`](../../../tools/tillr-watch/rules/trunk-proof-lag.toml), the build scope under `"Change nothing else"`, and the rejected alternatives — is an **operator-request and designer decision (`2026-11-26`), not an owner ruling, and the owner may overturn any part of it**.

---

## 2. Measured Wait to Covering Green Proof and Settled Threshold ([`D-TW-TPL1`](#D-TW-TPL1))

*Non-owner decision from the filed operator request and the designer; the owner may overturn any part of this section.*

### 2.1 Covering-proof definition, read-only measurement command, and 2026-11-26 result

Two invariants govern the measurement from `events` and Git history:

1. **Exact commit coverage (`pos[trunk_after] <= pos[tip_sha]` on `refs/heads/main`, `green.seq > landing.seq`):** When commit `C` (`trunk_after` on a `land.landing` event with `result == "landed"`) lands while an older proof `P_1` (`tip_sha = T_1`) is already running, `P_1`'s `"ended"`/`"green"` event is appended at `seq > landing.seq` even though `T_1` is an ancestor of `C` rather than a descendant (`C` is not proved by `P_1`). Indexing `git rev-list --reverse refs/heads/main` maps each trunk commit to its linear position `pos` and selects the **first** subsequent `"ended"`/`"green"` `drain.heavy-progress` event with `green.seq > landing.seq` and `pos[trunk_after] <= pos[tip_sha]`.
2. **Separating a working heavy runner (`working`) from red-trunk and restart-cancelled proof waves (`all`):** Following [`config/tillr/watch/drain-restart-unanswered.toml`](../../../tools/tillr-watch/rules/drain-restart-unanswered.toml) (which reports both `all` requests and non-starved `acted` requests), `all` measures every covered landing, while `working` selects covered landings where **every** intervening `drain.heavy-progress` `ended` event (`landing.seq < ended.seq < covering_green.seq`) had `outcome == "green"` — zero `"red"`, `"cancelled"`/`"canceled"`, or `"fault"` proofs between the landing and its covering green proof. Opening `.tillr/state.db` with `mode=ro&readonly_shm=1` (matching `inventoryTables` in [`tools/tillr-store/inventory.go`](../../../tools/tillr-store/inventory.go)) reads committed WAL rows without altering database, WAL, or SHM bytes or mtimes.

Measured on `2026-11-26` UTC (`seq 1..51271` in `.tillr/state.db`, `151` `land.landing` `"landed"` events on `refs/heads/main`, `103` covered by `6` `"ended"`/`"green"` proofs through `273bd92ecf`) with:

```bash
python3 -c 'import json, sqlite3, subprocess; revs = subprocess.check_output(["git", "rev-list", "--reverse", "refs/heads/main"], text=True).splitlines(); pos = {s: i for i, s in enumerate(revs)}; c = sqlite3.connect("file:.tillr/state.db?mode=ro&readonly_shm=1", uri=True); lands = [(seq, ts, json.loads(p).get("trunk_after", "")) for seq, ts, p in c.execute("SELECT seq, ts, payload FROM events WHERE kind = \"land.landing\" AND json_extract(payload, \"$.result\") = \"landed\" ORDER BY seq")]; ends = [(seq, ts, json.loads(p).get("tip_sha", ""), json.loads(p).get("outcome", "")) for seq, ts, p in c.execute("SELECT seq, ts, payload FROM events WHERE kind = \"drain.heavy-progress\" AND json_extract(payload, \"$.phase\") = \"ended\" ORDER BY seq")]; greens = [(s, t, sha) for s, t, sha, o in ends if o == "green" and sha in pos]; cov = [((gt - lt) / 1000.0, [o for es, _, _, o in ends if ls < es < gs]) for ls, lt, sha in lands if sha in pos for gs, gt, _ in [next(((gs, gt, gsha) for gs, gt, gsha in greens if gs > ls and pos[sha] <= pos[gsha]), (0, 0, ""))] if gs]; all_s = sorted(w for w, _ in cov); wrk_s = sorted(w for w, inter in cov if all(o == "green" for o in inter)); print(f"all={len(all_s)} min={all_s[0]:.1f}s p50={all_s[len(all_s)//2]:.1f}s p90={all_s[int(len(all_s)*0.9)]:.1f}s max={all_s[-1]:.1f}s; working={len(wrk_s)} min={wrk_s[0]:.1f}s p50={wrk_s[len(wrk_s)//2]:.1f}s p90={wrk_s[int(len(wrk_s)*0.9)]:.1f}s max={wrk_s[-1]:.1f}s")'
```

Result (`2026-11-26`):

```text
all=103 min=1824.1s p50=15353.4s p90=21966.0s max=24007.0s; working=10 min=2022.1s p50=3646.0s p90=5442.2s max=5442.2s
```

### 2.2 Per-proof breakdown and why `threshold_duration = "95m"` is chosen ([`D-TW-TPL1`](#D-TW-TPL1))

| Covering green proof (`tip_sha`) | Proof run duration | Covered landings | Intervening `ended` outcomes | Wait range (`land.landing` to green `ended`) | Regime |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `95d7043eb6` (`12:43:04Z`) | `6104.8s` (`101.7m`) | `47` | `1`–`6` `"red"` | `112.6m`–`400.1m` | Red-trunk backlog (`6` failing proofs) |
| `c3d74d27e2` (`17:10:52Z`) | `6257.7s` (`104.3m`) | `31` | `1`–`4` `"cancelled"` (after `0`–`1` `"green"`) | `106.5m`–`351.8m` | Loop-restart proof-cancellation backlog |
| `79bdcbbf72` (`17:51:20Z`) | `2411.0s` (`40.2m`) | `4` (`bca4958e61`, `19f56bb872`, `d759517662`, `79bdcbbf72`) | `1` `"green"` (`c3d74d27e2`) | **`63.4m`–`90.7m`** (`3801.2s`–`5442.2s`) | **Uninterrupted working runner** |
| `43455fc4dd` (`19:40:55Z`) | `993.4s` (`16.6m`) | `15` | `1`–`3` `"red"` (after `0`–`1` `"green"`) | `30.4m`–`149.6m` | Red-trunk backlog (`3` failing proofs) |
| `d52831b6b9` (`20:03:32Z`) | `1353.8s` (`22.6m`) | `2` (`d550cd942c`, `d52831b6b9`) | `1` `"green"` (`43455fc4dd`) | **`33.7m`–`33.9m`** (`2022.1s`–`2035.1s`) | **Uninterrupted working runner** |
| `273bd92ecf` (`20:41:47Z`) | `2287.8s` (`38.1m`) | `4` (`7a09eeb863`, `6555e23cce`, `b0fc3f57fe`, `273bd92ecf`) | `1` `"green"` (`d52831b6b9`) | **`57.4m`–`60.8m`** (`3445.0s`–`3646.0s`) | **Uninterrupted working runner** |

Across all `10` uninterrupted working-runner landings (`working=10`), every wait exceeded `30m` (`33.7m` to `90.7m`), and even after `3ebc4b8e` (`MAX_SHARDS = 2 * os.cpu_count()`), single green proofs ran up to `38.1m` (`273bd92ecf`, matching `79bdcbbf72` at `40.2m`), so two consecutive healthy proofs span `~76`–`80` minutes and loaded healthy runs reached `5442.2s` (`90.7m`, when `bca4958e61` landed mid-proof during `c3d74d27e2` and was proved green by `79bdcbbf72`).

[`D-TW-TPL1`](#D-TW-TPL1) amends [`D-TW54`](../tillr-watch.md#D-TW54) backward-only by setting `trunk-proof-lag`'s `threshold_duration` to **`"95m"`** — the next 5-minute multiple strictly above the `5442.2s` (`90.7m`) maximum produced by a working heavy runner, matching the 5-minute rounding of [`config/tillr/watch/drain-restart-unanswered.toml`](../../../tools/tillr-watch/rules/drain-restart-unanswered.toml) (`1928.4s` / `32.1m` -> `"35m"`):

- **Quiet across every working-runner wait (`<= 90.7m`):** Covers the `<= 50m` two-proof waits of `2026-11-24`, the `33.7m`–`60.8m` and `63.4m`–`90.7m` working-runner waits of `2026-11-25`, and two back-to-back `~40m` proofs (`~80m`), obeying [`D-TW73`](../tillr-watch.md#D-TW73). (If the build worker re-runs the read-only measurement command on `.tillr/state.db` at build time and a newer uninterrupted `working` observation exceeds `5442.2s`, `threshold_duration` rounds up to the next 5-minute multiple strictly above that observed `working` maximum and updates the comment's result line accordingly; it is never set below `"95m"`.)
- **Fires on genuine proof lag while `trunk-proof-stalled` catches hangs in `10m`:** Every multi-proof red-trunk wave (`149.6m` and `400.1m`) and every restart-cancelled proof-starvation wave (`106.5m`–`351.8m`) still opens `trunk-proof-lag` at `95m` (with `reopen_when` re-delivering each red proof under [`D-TW75`](../tillr-watch.md#D-TW75)), while `trunk-proof-stalled` (`threshold_duration = "10m"`) continues to alert after 10 minutes without check progress (catching deadlocked tests like the `2026-11-21` incident in 10 minutes rather than waiting for `trunk-proof-lag`) and `drain-heavy-fault` alerts immediately when a proof cannot run.

### 2.3 Updated header comment and `trunk-proof-lag` rule in `config/tillr/watch/trunk-proof-lag.toml`

Only the header comment above `trunk-proof-lag` and `threshold_duration` inside `trunk-proof-lag`'s `[rule.when]` change (`trunk-proof-stalled` and every other key of `trunk-proof-lag` remain byte-identical):

```toml
# On 2026-11-21 the trunk went about an hour without proof three times
# while the heavy runner was stuck in a deadlocked test. Landings and reverts
# are recorded as land.landing and drain.revert; lag_behind reads Git refs, while
# trunk-proof-stalled catches a hung proof in 10m. Because proofs run one at a
# time, a commit landing just after a proof starts waits for both that proof and
# the next one before refs/tillr/green covers it.
# Measured on 2026-11-26 from recorded land.landing to covering drain.heavy-progress
# ended/green history in .tillr/state.db with:
#   python3 -c 'import json, sqlite3, subprocess; revs = subprocess.check_output(["git", "rev-list", "--reverse", "refs/heads/main"], text=True).splitlines(); pos = {s: i for i, s in enumerate(revs)}; c = sqlite3.connect("file:.tillr/state.db?mode=ro&readonly_shm=1", uri=True); lands = [(seq, ts, json.loads(p).get("trunk_after", "")) for seq, ts, p in c.execute("SELECT seq, ts, payload FROM events WHERE kind = \"land.landing\" AND json_extract(payload, \"$.result\") = \"landed\" ORDER BY seq")]; ends = [(seq, ts, json.loads(p).get("tip_sha", ""), json.loads(p).get("outcome", "")) for seq, ts, p in c.execute("SELECT seq, ts, payload FROM events WHERE kind = \"drain.heavy-progress\" AND json_extract(payload, \"$.phase\") = \"ended\" ORDER BY seq")]; greens = [(s, t, sha) for s, t, sha, o in ends if o == "green" and sha in pos]; cov = [((gt - lt) / 1000.0, [o for es, _, _, o in ends if ls < es < gs]) for ls, lt, sha in lands if sha in pos for gs, gt, _ in [next(((gs, gt, gsha) for gs, gt, gsha in greens if gs > ls and pos[sha] <= pos[gsha]), (0, 0, ""))] if gs]; all_s = sorted(w for w, _ in cov); wrk_s = sorted(w for w, inter in cov if all(o == "green" for o in inter)); print(f"all={len(all_s)} min={all_s[0]:.1f}s p50={all_s[len(all_s)//2]:.1f}s p90={all_s[int(len(all_s)*0.9)]:.1f}s max={all_s[-1]:.1f}s; working={len(wrk_s)} min={wrk_s[0]:.1f}s p50={wrk_s[len(wrk_s)//2]:.1f}s p90={wrk_s[int(len(wrk_s)*0.9)]:.1f}s max={wrk_s[-1]:.1f}s")'
# Result (2026-11-26): all=103 min=1824.1s p50=15353.4s p90=21966.0s max=24007.0s; working=10 min=2022.1s p50=3646.0s p90=5442.2s max=5442.2s
# Every commit proved by an uninterrupted working heavy runner (zero red,
# cancelled, or faulted proofs between landing and covering green proof) waited
# <= 5442.2s (90.7m); waits above 90.7m had intervening red or cancelled proofs.
# Set threshold_duration = "95m".
[[rule]]
id = "trunk-proof-lag"
scope = "core"
severity = "warning"

[rule.reads]
events = ["land.landing", "drain.revert", "drain.heavy-progress"]
projections = ["git_refs:refs/heads/main,refs/tillr/green"]

[rule.when]
group_by = "global"
pattern = "lag_behind"
leader = "refs/heads/main"
follower = "refs/tillr/green"
threshold_duration = "95m"
reopen_when = 'event.kind == "drain.heavy-progress" and event.phase == "ended" and event.outcome == "red"'
```

---

## 3. Build Scope Under `"Change nothing else"` and Candidate Note ([`D-TW-TPL1`](#D-TW-TPL1))

### 3.1 Single-commit build scope

Under [`D-TW42`](../tillr-watch.md#D-TW42) (every shipped rule matches its loader-and-evaluation tests in `tools/tillr-watch/`, which `go-affected` runs whenever `config/tillr/watch/` changes) and [`landing-principles.md` §15](../landing-principles.md#15-land-the-smallest-functional-change), `"Change nothing else"` authorizes exactly:

1. **[`config/tillr/watch/trunk-proof-lag.toml`](../../../tools/tillr-watch/rules/trunk-proof-lag.toml):** Update only the header comment above `trunk-proof-lag` and `trunk-proof-lag`'s `threshold_duration = "95m"` ([§2.3](#23-updated-header-comment-and-trunk-proof-lag-rule-in-configtillrwatchtrunk-proof-lagtoml)), leaving `[rule.reads]`, `pattern = "lag_behind"`, `leader`, `follower`, `reopen_when`, `[rule.guidance]`, `[rule.delivery]`, and the entire `trunk-proof-stalled` rule untouched.
2. **Existing rule evaluation tests in [`tools/tillr-watch/`](../../../tools/tillr-watch/) and `just pack-sync` mirror:** Because `tools/gate/go-test-edges.json` binds `config/tillr/watch/` to `github.com/mschulkind-oss/tillr-watch`, update the hardcoded `30m` threshold assertions on `trunk-proof-lag` in [`tools/tillr-watch/trunk_proof_rule_test.go`](../../../tools/tillr-watch/trunk_proof_rule_test.go) (`TestTrunkProofLagFiresAtThirtyMinutesUnprovedAge`, `TestTrunkProofRulesStayQuietDuringActiveFifteenMinuteProofWithNormalLandings`, and `TestTrunkProofLagReopensOnRedProofCLI` where `base` and `tip1` must predate `now` by `> 95m`, plus two-proof working-runner silence across `30m..94m`) and [`tools/tillr-watch/item_fault_rule_test.go`](../../../tools/tillr-watch/item_fault_rule_test.go) (`TestTrunkProofLagRule`, checking silence at `94m` and firing at `95m`) using `t.TempDir()` scratch stores only ([`D-TW41`](../tillr-watch.md#D-TW41)), and sync `yolo-packs/tillr/files/local/share/tillr/tillr-watch/` with `just pack-sync` in the same commit.
3. **Relation to unbuilt shipped-rule layering ([`D-TW83`](../tillr-watch.md#D-TW83), [`D-TW85`](../tillr-watch.md#D-TW85), [`D-TW88`](../tillr-watch.md#D-TW88)):** While `config/tillr/watch/` remains the active rules directory, `trunk-proof-lag.toml` stays at `config/tillr/watch/trunk-proof-lag.toml` with `scope = "core"`. When [`D-TW88`](../tillr-watch.md#D-TW88) Slice 2/3 embeds shipped rules into `tools/tillr-watch/rules/`, `trunk-proof-lag` carries this `"95m"` repository-calibrated default alongside the seven calibrated rules named in [`../tillr-watch.md` §5.3](../tillr-watch.md#53-which-rules-belong-to-tillr-and-which-stay-with-a-repository-d-tw83) so consumer repositories with faster or slower proof suites can tune `threshold_duration` via `[[override]]`.

### 3.2 Out-of-scope surfaces and candidate note

Per the filed operator request, `trunk-proof-stalled`, the rule language, proof speed, proof slot counts, and the heavy runner are out of scope and untouched. The gaps observed on those surfaces at `60f79791fd` are recorded here as candidates for separate items:

1. **Heavy runner proof cancellation on loop restarts (`HeavyRunner` / loop restart lifecycle):** In `.tillr/state.db` on 2026-11-25, `13` trunk proofs ended with `outcome == "cancelled"` (`4` consecutive before `c3d74d27e2`, pushing unproved wait to `351.8m`, and `9` consecutive after `273bd92ecf`), because restarting `tillr-drain` cancels the in-flight proof rather than detaching and reclaiming it (options laid out in [`../trunk-proof-survives-restart.md`](../trunk-proof-survives-restart.md)).
2. **Proof speed and test result caching (`HeavyRunner`, `tools/go-test-shards.py`, gate checks):** Single green proofs on 2026-11-25 ran `16.6m` (`43455fc4dd`), `22.6m` (`d52831b6b9`), `38.1m` (`273bd92ecf`), `40.2m` (`79bdcbbf72`), and `101.7m`–`104.3m` (`95d7043eb6` and `c3d74d27e2`, prior to `3ebc4b8e` raising `MAX_SHARDS` and `5e14fd0f` prioritizing trunk proofs ahead of candidate gates). Content-keyed gate and proof test caching ([`../gate-test-caching.md`](../gate-test-caching.md)) is not built yet (`rg -n 'gate-test-caching|test_cache' tools` returns nothing); once proof wall time drops reliably, `trunk-proof-lag`'s `threshold_duration` can be re-measured and lowered in a follow-up item.
3. **Single-slot proof serialization (proof slot counts):** `HeavyRunner` runs one proof at a time, so a commit landing seconds after a proof starts (`7a09eeb863` landed `3s` after `d52831b6b9` started) always waits for up to two full proofs (`<remainder of in-flight proof> + <next proof>`). Overlapping or speculative next-tip proofs are not built.
4. **`trunk-proof-stalled` and cached shard-runner progress (`threshold_duration = "10m"`, [`D-TW62`](../tillr-watch.md#D-TW62), [`D-TW70`](../tillr-watch.md#D-TW70)):** `trunk-proof-stalled` already catches hung checks in 10 minutes via `go-test-shards.py --progress-file`; [`D-TW70`](../tillr-watch.md#D-TW70) (verifying the progress contract in cached shard-runner mode) remains marked `not checked` in [`../tillr-watch.md`](../tillr-watch.md).
5. **Rule language (`lag_behind` in [`tools/tillr/watch/git.go`](../../../tools/tillr/watch/git.go)):** `lag_behind` evaluates `now - oldest_commit_timestamp` on Git refs alone without distinguishing an idle or broken proof loop from two active back-to-back green proofs; adding proof-cycle awareness to `tools/tillr/watch/` is out of scope because `"95m"` plus `trunk-proof-stalled` (`10m`), `reopen_when` ([`D-TW75`](../tillr-watch.md#D-TW75)), and `drain-heavy-fault` cover hung, red, and faulted proofs without new engine syntax.

---

## 4. Rejected Alternatives

Each alternative below was considered by the designer and set aside (the owner may overturn any of these dispositions except [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1), [`R-TW-DR2`](./decision-record-files.md#R-TW-DR2), [`OQ-TW2`](../tillr-watch.md#OQ-TW2), and [`D-TW73`](../tillr-watch.md#D-TW73)):

| Alternative | Why it fails | Verdict |
| :--- | :--- | :--- |
| **Keep `threshold_duration = "30m"` ([`D-TW54`](../tillr-watch.md#D-TW54) unamended)** | Fires on healthy back-to-back proofs (`3` false alerts on 2026-11-24; `10` of `10` working-runner landings on 2026-11-25 waited `33.7m`–`90.7m`), violating [`D-TW73`](../tillr-watch.md#D-TW73). | Rejected. |
| **Set `threshold_duration = "55m"` or `"65m"` from only the `2026-11-24` (`<= 25m`) or `d52831b6b9`/`273bd92ecf` (`<= 60.8m`) slice** | Even post-`3ebc4b8e` single green proofs took `38.1m` (`273bd92ecf`) and `40.2m` (`79bdcbbf72`), making two consecutive healthy proofs `~76`–`80m`, and recorded working-runner history already reached `5442.2s` (`90.7m`). | Rejected. |
| **Set `threshold_duration = "405m"` from `all = 103` covered landings (`max = 24007.0s / 400.1m`)** | Includes `6`-proof red-trunk waves (`400.1m`) and `4`-proof restart-cancellation waves (`351.8m`), muting `trunk-proof-lag` for nearly 7 hours during real proof failures and restart starvation. | Rejected. |
| **Replace `lag_behind` with a custom proof-cycle pattern in `tools/tillr/watch/`** | Out of scope per the filed request (`"Use the lag_behind pattern as it is. Change nothing else. Out of scope: ... the rule language"`). | Rejected. |
| **Edit `docs/design/tillr-watch.md` inline to amend `D-TW54`** | Violates owner ruling [`R-TW-DR1`](./decision-record-files.md#R-TW-DR1) (`"ok, so save that"`) and [`D-TW-DR1`](./decision-record-files.md#D-TW-DR1), recreating shared-file squash conflicts. | Rejected. |

---

## 5. Open Questions

None.

---

## 6. Decision Ledger

[`D-TW-TPL1`](#D-TW-TPL1) records a decision from the filed operator request and the designer; it is **not** an owner ruling, and the owner may overturn any part of it. `Built` is checked against `60f79791fd`.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-TW-TPL1"></a>**[D-TW-TPL1](#D-TW-TPL1)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** amend [`D-TW54`](../tillr-watch.md#D-TW54) (and the `30m` / `30 minutes` `trunk-proof-lag` duration in [`§5.3`](../tillr-watch.md#53-which-rules-belong-to-tillr-and-which-stay-with-a-repository-d-tw83), [`§5.4`](../tillr-watch.md#54-the-ten-drain-conditions-d-tw39), and [`§5.9`](../tillr-watch.md#59-trunk-proof-lag-and-stalled-heavy-proof-progress-d-tw54) of [`../tillr-watch.md`](../tillr-watch.md)) backward-only so `trunk-proof-lag` in [`config/tillr/watch/trunk-proof-lag.toml`](../../../tools/tillr-watch/rules/trunk-proof-lag.toml) keeps `lag_behind` and `trunk-proof-stalled` (`10m`) unchanged, replaces the `"normal ~15m proofs"` sentence with the dated read-only `land.landing`-to-covering-green-`drain.heavy-progress` measurement command and result (`2026-11-26`: `all=103` up to `24007.0s`, `working=10` up to `5442.2s` / `90.7m`), and sets `threshold_duration = "95m"` (or the next 5-minute multiple strictly above any higher `working` maximum observed when re-running the read-only measurement at build time, never below `"95m"`), delivered in one commit with updated `trunk-proof-lag` threshold assertions in [`tools/tillr-watch/trunk_proof_rule_test.go`](../../../tools/tillr-watch/trunk_proof_rule_test.go) and [`tools/tillr-watch/item_fault_rule_test.go`](../../../tools/tillr-watch/item_fault_rule_test.go) plus `just pack-sync`, and joining the calibrated defaults tunable via `[[override]]` under [`D-TW83`](../tillr-watch.md#D-TW83) / [`D-TW85`](../tillr-watch.md#D-TW85). | 2026-11-26 | [§2](#2-measured-wait-to-covering-green-proof-and-settled-threshold-d-tw-tpl1), [§3](#3-build-scope-under-change-nothing-else-and-candidate-note-d-tw-tpl1), [§4](#4-rejected-alternatives) | no |
