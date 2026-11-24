---
title: "Gate test concurrency, memory pressure, and internal process priority under kernel scheduling"
status: in-review
stage: DESIGN
updated: 2026-11-24
tags:
  - tillr
  - gate
  - capacity
  - concurrency
  - priority
  - testing
  - design
summary: "Rewrites the gate test capacity design after the owner's 2026-11-24 ruling rejected processor-load sampling, outside-load estimation, admission inequalities, and capacity leases. Records why six gates started together on 2026-11-24 (a pending loop restart waiting on a running trunk proof, already removed by Slice A of gate-rework.md and landing-during-proof), why gates are not batched, why memory pressure (0.00 PSI with 81 GB free) is the only dynamic resource signal that matters, the 2026-11-24 (02:07 ET, base 16f401f5a3) verifier measurement showing one gate capped at MAX_SHARDS = 12 uses ~22% of one processor per drain.test shard and leaves most of 32 processors unused, the designer's overturnable decision setting per-gate MAX_SHARDS = 2 × os.cpu_count() in Slice go-affected-max-shards, what jail settings cgroup subgroups would require versus nice today, and how kernel-scheduled concurrent gates compare against arrival-order serialization and batching on the night's numbers."
next: "The owner rules on OQ-GTC1 through OQ-GTC4 and reviews D-GTC1 through D-GTC4 in the Decision Ledger (Slice go-affected-max-shards in Section 3.3 / D-GTC4 is named so per-gate MAX_SHARDS = 2 × os.cpu_count() can be filed unless overturned)."
depends-on:
  - gate-rework.md
  - landing-principles.md
  - tillr-vocabulary.md
  - tillr-judge-and-lifecycle.md
  - tillr-scheduling-and-notifications.md
vantage:
  status-chip: true
---

# Gate test concurrency, memory pressure, and internal process priority under kernel scheduling

**Status:** 2026-11-24. Nothing here is built yet. Repository behavior is verified at `09670b916e` and `01ba61c8cb`, joined with the operator's 2026-11-24 night measurements (`23:48 ET` through `00:33 ET`), the verifier's 2026-11-24 (`02:07 ET`, base `16f401f5a3`) single-gate shard measurement, and the 2026-11-22 fork-rate profile. Standing owner rulings — including the owner's 2026-11-24 rejection of the earlier processor-headroom coordinator — are quoted verbatim in [Section 1.2](#12-standing-owner-rulings-that-govern-this-design) (`R-GTC1` through `R-GTC5`). Engineering boundaries and the per-gate shard ceiling derived from those rulings (`D-GTC1` through `D-GTC4`) are marked as the designer's decisions from the operator's 2026-11-24 requests and may be overturned by the owner; policy choices on cross-run test-process limits, memory-pressure enforcement, internal priority (`nice` vs cgroup `cpu.weight` subgroups), and gate scheduling order are left open to the owner in [`OQ-GTC1`](#OQ-GTC1) through [`OQ-GTC4`](#OQ-GTC4).

> **In short.** Scheduling runnable threads across processors is the Linux kernel's job, host isolation belongs to workspace and jail cgroups set by the owner, and a test that fails because the machine is busy is a broken test — not a reason to build a user-space CPU scheduler. The six-gate burst on 2026-11-24 (`18–21 min` per gate at `loadavg 130–180`, with `0.00` memory pressure and `81 GB` free) happened only because a pending loop restart held four finished candidates behind a running trunk proof and released them together at `00:02:18 ET`, a wait that [Slice A of Section 11 in `gate-rework.md`](./gate-rework.md#run-affected-go-tests-without-holding-sibling-landings) and landing-during-proof remove. What remains for the owner to settle is four clean questions: no cross-run process limit vs a fixed `2 × os.cpu_count()` cap, kernel/cgroup memory limits alone vs a `/sys/fs/cgroup/memory.pressure` shard guard, `nice` vs writable cgroup `cpu.weight` subgroups for internal priority among tillr's own classes, and keeping concurrent per-candidate gates vs arrival-order serialization or batching.

- **What [`R-GTC5`](#R-GTC5) removes:** The earlier draft's `/proc/stat` and `cpu.stat` sampler, "outside-busy" processor accounting, the admission inequality, processor-weight leases and holder files, `capacity-wait.json`, and any argument from broken wall-clock-assuming tests.
- **Why six gates ran together on 2026-11-24:** A drain build installed at `23:48:11 ET` waited on a running trunk proof until `00:01:52 ET`, holding four finished workers (`23:49–23:55 ET`) behind `markRestartPending()` in [`tools/tillr-drain/drain/restart.go`](../../tools/tillr-drain/drain/restart.go) and releasing four gates at `00:02:18 ET` plus two more by `00:08:12 ET` alongside a new proof (`18–21 min` per gate vs `5–8 min` earlier that night). Outside a pending restart, gates start staggered as workers finish.
- **Start at:** [Section 2](#2-why-six-gates-ran-together-on-2026-11-24-and-why-gates-are-not-batched) for the `23:48–00:23 ET` timeline and why gates are not batched, [Section 3](#3-concurrent-test-process-limits-and-memory-pressure) for fixed/zero process limits, memory pressure, and the [Section 3.3](#33-per-gate-shard-count-2026-11-24-measurement-and-processor-multiple-decision) per-gate shard measurement and `2 × os.cpu_count()` decision, [Section 4](#4-priority-among-tillrs-own-processes-and-the-jail-cgroup-boundary) for internal priority and the jail's read-only cgroup mount, and [Section 5](#5-arrival-order-queueing-and-candidate-batching-vs-running-ready-gates-at-once) for the `18–21 min` vs `5–8 min` turnaround arithmetic.
- **Needs your ruling:** [`OQ-GTC1`](#OQ-GTC1) (no cross-run test-process limit vs a fixed `2 × os.cpu_count()` cap), [`OQ-GTC2`](#OQ-GTC2) (kernel/cgroup memory enforcement alone vs a `/sys/fs/cgroup/memory.pressure` shard-spawn guard), [`OQ-GTC3`](#OQ-GTC3) (internal priority via `nice`, delegated cgroup `cpu.weight` subgroups after a jail mount change, or flat scheduling), and [`OQ-GTC4`](#OQ-GTC4) (running ready gates immediately vs FIFO gate serialization vs candidate batching).
- **Awaits your review (designer's decisions; you may overturn any of them):** [`D-GTC1`](#D-GTC1) through [`D-GTC4`](#D-GTC4) in the [Decision Ledger](#decision-ledger) (including Slice `go-affected-max-shards` in [Section 3.3](#33-per-gate-shard-count-2026-11-24-measurement-and-processor-multiple-decision)).
- **Reads with:** [`gate-rework.md`](./gate-rework.md) (owns `go-affected`, Slice A's detached gate across restarts, the stall watchdog of `D-GR15` / `D-GR16`, and the pre-existing 22-slot `D-GR20` / `D-GR43` / `D-GR48` pool left untouched here until ruled), [`landing-principles.md`](./landing-principles.md) (owns single-candidate `base_sha..head_sha` verdicts and three-way squash landing), [`tillr-vocabulary.md`](./tillr-vocabulary.md), [`tillr-judge-and-lifecycle.md`](./tillr-judge-and-lifecycle.md), and [`tillr-scheduling-and-notifications.md`](./tillr-scheduling-and-notifications.md).

---

## 1. Scope, candidate note, and standing owner rulings

### 1.1 Scope and candidate note on out-of-scope gaps

This document rewrites `docs/design/gate-test-capacity.md` to follow the owner's 2026-11-24 rulings ([`R-GTC5`](#R-GTC5)), answers the four cross-run design questions posed in the initial 2026-11-24 request, and records the follow-up 2026-11-24 measurement and designer decision on how many test shards a single gate runs:

1. whether any limit on concurrent test processes is needed at all given kernel thread scheduling, or a fixed multiple of `os.cpu_count()` with zero load inference ([Section 3.1](#31-question-1-whether-any-limit-on-concurrent-test-processes-is-needed-at-all), [`OQ-GTC1`](#OQ-GTC1));
2. whether memory pressure is the one dynamic signal worth acting on, and what acts on it ([Section 3.2](#32-question-2-whether-memory-pressure-is-the-one-signal-worth-acting-on-and-what-acts-on-it), [`OQ-GTC2`](#OQ-GTC2));
3. whether priority among tillr's own process classes (`repair`, candidate `gate`, `trunk-proof`, and lane workers' test runs) is worth having, with the measured reason for and against and the exact mechanism (`nice` today vs cgroup v2 `cpu.weight` subgroups if the jail delegates writable cgroups, [Section 4](#4-priority-among-tillrs-own-processes-and-the-jail-cgroup-boundary), [`OQ-GTC3`](#OQ-GTC3));
4. whether running gates in arrival order or batching candidates into one gate would land work sooner than running ready gates at once, evaluated on the 2026-11-24 numbers ([Section 5](#5-arrival-order-queueing-and-candidate-batching-vs-running-ready-gates-at-once), [`OQ-GTC4`](#OQ-GTC4)); and
5. how many test shards a single gate runs on its own, grounded in the 2026-11-24 (`02:07 ET`, base `16f401f5a3`) verifier measurement of `tools/tillr-drain/drain` and decided as a fixed multiple of processor count under [`R-GTC5`](#R-GTC5) ([Section 3.3](#33-per-gate-shard-count-2026-11-24-measurement-and-processor-multiple-decision), [`D-GTC4`](#D-GTC4) — the designer's decision, overturnable by the owner, naming Slice `go-affected-max-shards`).

**Candidate note on out-of-scope gaps.** Per the 2026-11-24 requests, seven adjacent items are out of scope here and are listed rather than changed:

- **Production and test code (out of scope here).** No code is edited in this commit. In particular, `ENFORCE_SLOT_LIMITS = False` (`f633b5fb81`) and the unused `slot-*` / `gate-*` / `proof-*` lock machinery in [`tools/go-test-shards.py`](../../tools/go-test-shards.py) remain as-is until the owner rules on [`OQ-GTC1`](#OQ-GTC1) through [`OQ-GTC4`](#OQ-GTC4), and `MAX_SHARDS = 12` in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py) stays unchanged until Slice `go-affected-max-shards` ([Section 3.3](#33-per-gate-shard-count-2026-11-24-measurement-and-processor-multiple-decision)) is filed and built.
- **The cross-run limit and scheduling questions already open in this document (out of scope for the per-gate shard addition).** [`OQ-GTC1`](#OQ-GTC1) through [`OQ-GTC4`](#OQ-GTC4) remain open for the owner exactly as written.
- **How individual slow tests are made faster (out of scope here).** On base commit `16f401f5a3`, `77` top-level tests of `10 s` or more (`31%` of `4,544 s`), `135` of `5–10 s` (`20%`), and `412` of `2–5 s` (`27%`) hold `78%` of `tools/tillr-drain/drain`'s uncached test duration; shortening individual slow tests is separate test-suite work rather than part of per-gate shard sizing.
- **Building the test binary once per package (out of scope here).** Compiling each affected package's test binary once (`go test -c`) and running shards directly against that binary instead of invoking `go test` once per shard is a separate optimization and is not bundled into Slice `go-affected-max-shards`.
- **The queued CPU-sampler implementation item (out of scope; closed by the operator).** The item queued from the rejected draft to build a `/proc/stat` and `cpu.stat` sampler was closed by the operator on 2026-11-24 and has no successor until the open questions here are answered.
- **[`gate-rework.md`](./gate-rework.md) (out of scope here).** `D-GR20`, `D-GR43`, and `D-GR48` in [`gate-rework.md`](./gate-rework.md) still describe the 22-slot lock pool (`12` `ordinary` + `6` `gate` + `4` `proof`); amending those ledger rows waits on the owner's answers here rather than editing [`gate-rework.md`](./gate-rework.md) in this commit.
- **Tests that fail on a busy machine because they assume fast wall-clock execution (out of scope here; tracked as broken tests).** Under [`R-GTC5`](#R-GTC5), a test that assumes a fast machine is defective in its own test code (enforced on changed test files by [`tools/gate/checks/elapsed-time.py`](../../tools/gate/checks/elapsed-time.py)) and is repaired in its own item, never treated as justification for capping concurrency.

### 1.2 Standing owner rulings that govern this design

Five dated owner rulings govern this design. Only these verbatim quoted statements are owner rulings (`R-GTC1` through `R-GTC5` in the [Decision Ledger](#decision-ledger)):

1. **No fallbacks (`R-GTC1`, owner, 2026-11-20, recorded in [`tillr-judge-and-lifecycle.md` Section 4.15](./tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27) and [`drain-timed-checkin`](../../.agents/skills/drain-timed-checkin/SKILL.md#model-policy-owner-2026-11-21)):**
   > "why does Terra having some error mean that we automatically flip to Sol? I don't like that. I hate fallbacks. Fallbacks are almost always wrong. We need deliberate choices, not fallbacks. We need to fix what's wrong. We don't need to just randomly try something else."

   And on the same day, confirming the rule applies across all machinery rather than models alone:
   > "That is everything. ... If something fails, you fix it. If it fails loudly, you fix it. You don't hide it with a fallback."

   *How it binds this design:* Whatever policy the owner selects in [`OQ-GTC1`](#OQ-GTC1)–[`OQ-GTC4`](#OQ-GTC4) is enforced directly with no silent fallback mode and no dead `ENFORCE_SLOT_LIMITS = False` toggle left behind. If cgroup subgroup priority ([`OQ-GTC3`](#OQ-GTC3) Option A) or PSI backpressure ([`OQ-GTC2`](#OQ-GTC2) Option B) is chosen and its required `/sys/fs/cgroup` interface is missing or unwritable, the runner fails loudly (`exit 4`, `result = "error"`) naming the path rather than silently dropping back to another mode.

2. **Lane count is judged by wasted work (`R-GTC2`, owner, 2026-11-21, recorded in [`drain-timed-checkin`](../../.agents/skills/drain-timed-checkin/SKILL.md#lane-steps)):**
   > "the reason why we have fewer lanes is because we're wasting a lot of work by having Lots of conflicts, rework, red trunks, all that stuff ... the amount of time waiting was sort of a proxy for that, but not the primary thing I care about."

   *How it binds this design:* Any priority or ordering rule inside tillr's own machinery is worthwhile only if it reduces wasted work (such as shrinking the pre-landing conflict window for already-finished candidates or clearing a trunk repair faster), not to chase an aesthetic CPU utilization target.

3. **Every pause is a bug with a cause (`R-GTC3`, owner, 2026-11-20, recorded in [`drain-timed-checkin`](../../.agents/skills/drain-timed-checkin/SKILL.md#the-bar-owner-2026-11-20)):**
   > "You should be able to operate this drain in a way such that changes land in nearly the same speed as if you were running yourself as sub-agents. There shouldn't be random pauses, and if there is, that's a bug that you fix."

   *How it binds this design:* Artificial barriers that hold finished candidates back from gating or landing — such as the pending-restart barrier that caused the 2026-11-24 six-gate pileup ([Section 2.1](#21-the-2026-11-24-pending-restart-release)), or holding a ready candidate idle to assemble a speculative gate batch ([Section 5.1](#51-question-4-comparing-all-three-gate-schedules-on-the-2026-11-24-numbers)) — work against smooth landing flow.

4. **Budgets are targets, stalls end checks, and high load must not fail valid work (`R-GTC4`, owner, 2026-11-17 and 2026-11-21, `D-GR15` in [`gate-rework.md` Section 6.5](./gate-rework.md#65-ten-seconds-is-a-failing-test) and `D-GR38` in [`gate-rework.md` Section 6.12](./gate-rework.md#612-a-change-to-a-tillr-go-module-runs-its-affected-test-packages-before-it-lands)):**
   > "target budgets, watch for stalls, but don't let 'load is high' cause failures that would otherwise finish just fine."

   And on pre-merge affected Go tests (2026-11-21, `D-GR38`):
   > "if you think drain suite selected tests are 40 seconds, let's do that, and monitor how it goes, just make sure we have perf monitoring all over it. 10 seconds was apsirational"

   *How it binds this design:* Kernel-scheduled runnable test processes making steady CPU progress must never be killed by wall-clock deadlines when concurrency stretches a 6-minute gate to 20 minutes; stall detection (`watchdog.go`) ends only genuinely stuck checks making zero CPU and output progress.

5. **The kernel schedules runnable threads; never infer a limit from processor load or external use, never justify a limit from broken wall-clock tests, and isolate the host via workspace/jail cgroups (`R-GTC5`, owner, 2026-11-24):**
   > "What does it mean for a core to be in use? I've never heard of any program scheduling cores and claiming that they're in use and allocating them. Like you run, you have your runnable threads, they will run as available and the whole point of a kernel is to schedule these things."

   > "20 minutes doesn't sound that terrible, but also how did all the gates start together? What was starting them? Why were they running together? Why aren't they batched? ... Also, if that test assumes a fast machine, that test is broken. So clearly that's not an argument for anything. And regardless of what we pick, I definitely don't want to infer some limit, like pick something responsible based on the number of processors, like two times the number of processors or something like that, and just do it. Unless we're hitting memory pressure, more of them is not an issue, and I can apply cgroups to workspaces and jails, so I can limit it from hitting the rest of the machine if I need to, and Otherwise, I don't want you guessing about external loads. That's ridiculous. But prioritizing within our own machinery, that's something that's potentially useful, if we have a reason to think that that is useful."

   > "I also believe that we have full access to cgroups in here and can make any subgroups that we want."

   *How it binds this design:*
   - Tillr never reads `/proc/stat`, `/sys/fs/cgroup/cpu.stat`, `/proc/<pid>/stat`, or `/proc/loadavg` to decide whether to start a test process, never defines "in-use" or "outside-busy" processors, and never estimates load outside tillr (`D-GTC1`).
   - If any test-process ceiling is kept at all, it is a fixed constant multiple of `os.cpu_count()` (such as `2 × os.cpu_count()`), or no ceiling at all ([`OQ-GTC1`](#OQ-GTC1)).
   - Protecting the rest of the machine from a workspace or jail belongs to host/workspace cgroups set by the owner outside tillr.
   - Because `/sys/fs/cgroup` inside the jail is mounted read-only today ([Section 4.2](#42-verified-cgroup-state-inside-the-jail-and-what-setting-subgroups-would-need)), this design states the exact jail configuration that cgroup subgroups would require and asks the owner in [`OQ-GTC3`](#OQ-GTC3) rather than assuming either `nice` or cgroup subgroups.

---

## 2. Why six gates ran together on 2026-11-24, and why gates are not batched

### 2.1 The 2026-11-24 pending-restart release

On the night of 2026-11-23 into 2026-11-24 (`32` logical processors), normal gate execution and the `00:02 ET` six-gate burst differed for one mechanical reason in [`tools/tillr-drain/drain/restart.go`](../../tools/tillr-drain/drain/restart.go):

| Time (`ET`, 2026-11-23/24) | Event | Measured state on the 32-processor host |
| :--- | :--- | :--- |
| **Earlier on 2026-11-23/24** | Steady-state drain operation (no pending restart) | Candidates enter their gates one by one as each worker finishes; each Go gate takes **`5 to 8 minutes`**. |
| **`23:48:11`** | New `tillr-drain` build installed while that landing's `trunk-proof` is running | In [`tools/tillr-drain/drain/restart.go`](../../tools/tillr-drain/drain/restart.go), `gateRestartWith` sees `pending == true` while `restartProofActive()` is true, calls `markRestartPending()`, and returns early. While `restartPending` is set, the loop starts **no** new candidate gate and performs **no** landing so `restartMechanical` can drain for `exec`. |
| **`23:49` – `23:55`** | Four lane workers finish their commits | All four finished candidates sit queued behind the pending restart (`7` to `13` minutes of idle hold before gating begins). |
| **`00:01:52`** | Running `trunk-proof` finishes; `tillr-drain loop` restarts in place (`exec`) | Pending-restart barrier clears. |
| **`00:02:18`** | Restarted loop dispatches all **four** queued candidate gates simultaneously | `4` Go gates start within the same second (`ENFORCE_SLOT_LIMITS = False` in [`tools/go-test-shards.py`](../../tools/go-test-shards.py)). |
| **`00:03:36` & `00:03:52`** | **Fifth** candidate gate starts (`00:03:36`) beside a new `trunk-proof` (`00:03:52`) | `5` candidate gates + `1` `trunk-proof` running concurrently. |
| **`00:08:12`** | **Sixth** candidate gate starts while the first five and `trunk-proof` are still running | **`6` candidate gates + `1` `trunk-proof`** active together; Linux load average reaches **`130 to 180` on `32` processors**; each gate takes **`18 to 21 minutes`** (vs `5 to 8 minutes` earlier that night). |
| **`00:33`** | Post-burst cgroup memory and delegation inspection inside the jail | `/sys/fs/cgroup/memory.pressure` reads **`0.00` at `avg10`, `avg60`, and `avg300`** with **`81 GB` available**; `/proc/self/cgroup` is `"0::/"` on a read-only `/sys/fs/cgroup` mount. |

Two facts follow directly from that trace:

1. **The six-gate simultaneous start was created by the pending-restart hold, not by normal worker completions.** Outside a pending loop restart, workers across lanes finish at staggered times and the loop starts one gate per finished candidate as soon as its worker exits.
2. **Two already-planned changes remove the hold that dammed those candidates up:** the item *"A candidate whose gate passed lands while a loop restart waits on the trunk proof"* and [Slice A of Section 11 in `gate-rework.md`](./gate-rework.md#run-affected-go-tests-without-holding-sibling-landings) (which decouples detached `ProcessSteps.Gate` execution and fast-gate settlement from `tillr-drain` restart quiescence, allowing the loop to `exec` across live detached gates without holding ready candidates back while a `trunk-proof` runs). Once those land, a binary rebuild during `trunk-proof` no longer accumulates a multi-gate wave behind `markRestartPending()`.

### 2.2 Why gates are not batched

Candidates are gated individually rather than combined into a multi-candidate batch because of the contract in [`landing-principles.md`](./landing-principles.md) [Section 2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch) and [Section 3](./landing-principles.md#3-gate-what-will-be-the-tip-once):

- **One candidate, one verdict bound to `(base_sha, head_sha^{tree})`:** Each candidate is gated once, alone, over its own diff `base_sha..head_sha`. When its gate passes, `Land` in [`tools/tillr-land/land/trunk.go`](../../tools/tillr-land/land/trunk.go) (`Trunk.Squash`) three-way merges `base_sha..head_sha` onto the current `main` tip without re-running the gate, recording both `gated_tree` and `landed_tree`.
- **Clean per-item attribution ([`landing-principles.md` Section 14](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)):** Because a gate runs only one candidate's tree against its base, a red check convicts that candidate alone (once any trunk baseline failure is ruled out) and returns actionable failure output straight to that item's lane. Combining `N` candidates into one speculative tree means a single broken candidate turns the whole batch red without naming which of the `N` items caused it, forcing bisection re-runs before *any* item in the batch can be convicted or cleared. [Section 5.1](#51-question-4-comparing-all-three-gate-schedules-on-the-2026-11-24-numbers) works out the exact wall-time cost on the 2026-11-24 numbers.

---

## 3. Concurrent test-process limits and memory pressure

### 3.1 Question 1: Whether any limit on concurrent test processes is needed at all

*`D-GTC1` (removing load-inferred admission) and `D-GTC3` (per-subprocess thread/shim hygiene) are the designer's decisions from the 2026-11-24 operator request and may be overturned by the owner; choosing between no cross-run process limit and a fixed `2 × os.cpu_count()` cap is left to the owner in [`OQ-GTC1`](#OQ-GTC1).*

Under [`R-GTC5`](#R-GTC5), processor-load inference is eliminated (`D-GTC1`): tillr neither reads `/proc/stat` or `cpu.stat` nor estimates external CPU load. Today at `09670b916e`, `ENFORCE_SLOT_LIMITS = False` (`f633b5fb81`) in [`tools/go-test-shards.py`](../../tools/go-test-shards.py) already runs with **no** cross-run slot limit, leaving only per-invocation parallelism bounds:

- [`tools/go-test-shards.py`](../../tools/go-test-shards.py): up to `MAX_SHARDS = 12` LPT-balanced shard processes per module suite (`-parallel 1` per shard);
- [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py): `ThreadPoolExecutor(max_workers=min(16, len(invocations)))` per `go-affected` gate check;
- [`tools/fork-check.py`](../../tools/fork-check.py): `ThreadPoolExecutor(max_workers=min(8, len(wanted)))` per non-Go landing check run.

Comparing the two clean options under [`R-GTC5`](#R-GTC5) — **no cross-run limit at all** vs **one fixed multiple of `os.cpu_count()` (`2 × N_cpu = 64` on 32 processors)**, with zero load inference in either:

| Policy | How it behaves at `1–2` gates (normal staggered arrivals) | How it behaves at `6` gates + `trunk-proof` (`2026-11-24 00:02 ET` burst) | Machinery & lock state |
| :--- | :--- | :--- | :--- |
| **Option A — No cross-run limit (retire the `go-test-shards.py` lock pool completely)** | `8–24` test processes run freely; Linux EEVDF/CFS schedules all runnable threads; gates finish in **`5–8 min`**. | `~70–100` runnable test processes share `32` cores (`loadavg 130–180`); all `6` gates finish in **`18–21 min`** with `0.00` memory pressure and `81 GB` RAM free. | **Zero** cross-process lock files, **zero** wait-ticket directories, and **zero** slot-wait watchdog exemptions; deletes ~450 lines of dead lock-pool code in [`tools/go-test-shards.py`](../../tools/go-test-shards.py). |
| **Option B — Fixed `2 × os.cpu_count()` semaphore (`64` slots on `32` processors, never reading load)** | Identical to Option A (`8–24 <= 64`, `0 ms` wait); `2×` core count keeps processors fed across `git` and disk I/O sleeps. | First `64` shards run concurrently (`2` per core) while shards `65..100` wait cleanly on `slot-00..63.lock`, bounding worst-case `fork`/`exec` churn (`2,601.9` forks/s and `392,360.6` ctxt/s measured at `21` `drain.test` binaries in [`drain-cpu-and-forks-2026-11-22.md`](../pi/drain-cpu-and-forks-2026-11-22.md)) and bounding peak RSS if `> 8–10` Go gates ever coincide. | One flat array of `2 × os.cpu_count()` `flock` files (`slot-00..63.lock`, replacing the `12 + 6 + 4` class-split pool); still needs slot-wait logging so `watchdog.go` does not count lock waits as stalls. |

**Per-subprocess hygiene independent of [`OQ-GTC1`](#OQ-GTC1) (`D-GTC3`):**
Whether the owner picks Option A or Option B in [`OQ-GTC1`](#OQ-GTC1), two local per-invocation cleanups reduce pointless thread and process creation without any cross-run limit or coordinator:

1. **Set `GOMAXPROCS=1` and `-p 1` on single-shard (`-parallel 1`) `go test` invocations:** Without `GOMAXPROCS=1`, every `go test` shard binary defaults `GOMAXPROCS` to `os.cpu_count() = 32`, spawning dozens of Go runtime and compiler OS threads inside a process designed to run one test sequentially.
2. **Match [`tools/go-test-shards.py`](../../tools/go-test-shards.py)'s shim-free `git` environment inside [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py):** On 2026-11-22 ([`drain-cpu-and-forks-2026-11-22.md`](../pi/drain-cpu-and-forks-2026-11-22.md)), `strace -f` showed every `git` call inside `go_affected.py`'s `drain.test` shards traversing `/home/agent/.yolo/bin/block/git` (`4` processes per `git` command instead of `1`, plus background `git maintenance run`). Resolving real `git` (`YOLO_BYPASS_SHIMS=1` / clean `git` symlink) and setting `maintenance.auto = false` and `gc.auto = 0` in `go_affected.py`'s hermetic `GIT_CONFIG_GLOBAL` cuts per-shard fork rate by ~4× with zero behavioral change.

### 3.2 Question 2: Whether memory pressure is the one signal worth acting on, and what acts on it

*`D-GTC2` (recognizing memory exhaustion as the sole dynamic resource failure boundary) is the designer's decision from the 2026-11-24 operator request and may be overturned by the owner; whether tillr relies solely on kernel/cgroup memory limits or also adds a PSI shard-spawn guard is left to the owner in [`OQ-GTC2`](#OQ-GTC2).*

1. **Why memory pressure is the *only* dynamic signal worth acting on (`D-GTC2`):**
   CPU oversubscription and memory exhaustion fail in fundamentally different ways on Linux:
   - **CPU saturation degrades throughput gracefully:** When `96` runnable test threads share `32` processors, the kernel scheduler time-slices them; nothing crashes, no verdict is corrupted, and total useful work stays high (`6` gates + `trunk-proof` completing in `18–21 min`).
   - **Memory exhaustion destroys running work:** When resident memory exceeds physical RAM + swap or a cgroup's `memory.max`, the kernel enters heavy synchronous page-reclaim thrashing and ultimately invokes the OOM killer (`SIGKILL`), terminating a `go test` or `tillr-gate` child mid-flight and turning a valid gate run into an incomplete/machinery-error run. Memory pressure is therefore the **one** dynamic resource signal where starting another test binary can cause real damage rather than fair time-sharing.

2. **What the 2026-11-24 measurement shows on this host:**
   At `00:33 ET` on 2026-11-24, right after the six-gate + `trunk-proof` wave on this 32-processor machine, `/sys/fs/cgroup/memory.pressure` read **`0.00` across `avg10`, `avg60`, and `avg300`**, with **`81 GB` of memory available** (container `/sys/fs/cgroup/memory.max` and `memory.high` both `"max"`). Even six simultaneous heavy Go gates plus `trunk-proof` plus active lane workers came nowhere near stressing memory on this machine.

3. **What can act on memory pressure ([`OQ-GTC2`](#OQ-GTC2)):**
   Two mechanisms exist, one purely in the kernel/jail cgroup and one inside tillr's shard runners:
   - **Kernel page reclaim + workspace/jail cgroup `memory.high` and `memory.max` ([`OQ-GTC2`](#OQ-GTC2) Option A):** In cgroup v2, setting `memory.high` on a workspace or jail cgroup (which the owner controls per workspace/jail, or via `yolo-cglimit --memory`) causes the Linux kernel itself to throttle memory-allocating tasks in direct reclaim as usage approaches `memory.high`, well before `memory.max` triggers an OOM kill. On an `81 GB`-free host with `0.00` PSI, zero code in tillr is needed.
   - **User-space PSI check on `/sys/fs/cgroup/memory.pressure` before spawning additional shards ([`OQ-GTC2`](#OQ-GTC2) Option B):** `/sys/fs/cgroup/memory.pressure` is readable inside the jail today without writable cgroups (`some avg10=0.00 avg60=0.00 avg300=0.00 ...` / `full avg10=0.00 ...`). If tillr ever runs inside a memory-constrained jail where `memory.max` is tight, `go-test-shards.py` and `go_affected.py` can check `memory.pressure` before launching shard `2..k` (always allowing `1` active shard per runner so forward progress never deadlocks) and sleep-poll in `1 s` steps while `some avg10` exceeds a fixed stall threshold (such as `10.0%`). Because that adds user-space wait code and watchdog wait reporting for a condition currently measured at `0.00`, [`OQ-GTC2`](#OQ-GTC2) asks the owner whether to build nothing in tillr (relying on kernel/jail cgroups) or add the PSI guard.

### 3.3 Per-gate shard count: 2026-11-24 measurement and processor-multiple decision

*[`D-GTC4`](#D-GTC4) is the designer's decision under the owner's 2026-11-24 rulings ([Section 1.2](#12-standing-owner-rulings-that-govern-this-design), [`R-GTC5`](#R-GTC5)); it is not an owner ruling, and the owner may overturn it.*

#### Measured finding (`2026-11-24`, `02:07 ET`, base `16f401f5a3`)

Measured by a verifier on **2026-11-24** (`02:07 ET` refusal of the item *"The drain package's tests take three minutes in the fast gate after the package split"*), on base commit `16f401f5a3` on this `32`-processor host:

- **Uncached `tools/tillr-drain/drain` test volume and duration distribution:** The `tools/tillr-drain/drain` package has **`1,921` top-level tests** summing **`4,544 s`** when run uncached:
  - **`77` tests of `10 s` or more** hold **`31%`** of total duration;
  - **`135` tests of `5–10 s`** hold **`20%`**;
  - **`412` tests of `2–5 s`** hold **`27%`**; and
  - the remaining **`1,297` tests under `2 s`** hold **`22%`**.
- **Per-gate shard and worker caps in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py):** `go_affected.py` caps one gate at **`MAX_SHARDS = 12`**, with each shard run as a separate `go test -parallel 1` invocation (`go test -json -timeout 0 -parallel 1`) inside a `ThreadPoolExecutor` of at most **`16`** workers (`max_workers=min(16, len(invocations))`). (In addition, at `01ba61c8cb` its unnarrowed large-package branch computes `min(MAX_SHARDS, max(MIN_SHARDS, len(non_edge) // 125))`, which would stop at `1,920 // 125 = 15` shards for `1,921` tests even if `MAX_SHARDS` alone were raised.)
- **Per-shard processor utilization and absence of cross-shard locking:** Processor profiling showed each `drain.test` process using **about `22%` of one processor**, because the tests spend most of their wall time waiting on child processes and SQLite. **Tests do not wait on a cross-shard fixture lock.**
- **Why shard count governs a solo gate's wall time:** With `12` shards each using `~22%` of one processor (`12 × 0.22 ≈ 2.6` processor-equivalents in the `drain.test` binaries themselves) and an uncached LPT test-sum floor of `4,544 s / 12 = 378.7 s` (`6.3 min` before per-shard `go test` startup), one gate running alone leaves most of the `32` processors unused by its own test binaries, and its wall time is set by the shard count.

#### Designer's decision (`D-GTC4`, overturnable): `MAX_SHARDS = 2 * os.cpu_count()`

Under the owner's 2026-11-24 rulings in [Section 1.2](#12-standing-owner-rulings-that-govern-this-design) ([`R-GTC5`](#R-GTC5)) — a limit is a plain multiple of the processor count and is never inferred from load — the designer decides (`D-GTC4`, **the designer's decision, which the owner may overturn**) to set the per-gate shard ceiling in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py) to **`2×` the processor count**:

```text
MAX_SHARDS = 2 * (os.cpu_count() or 1)   # 64 shards on 32 processors
```

Comparing candidate processor-count multiples on the `2026-11-24` (`16f401f5a3`) numbers (`4,544 s` uncached test sum, `~22%` of one processor per `drain.test` process, `32` logical processors):

| Per-gate `MAX_SHARDS` | Shards on `32` processors | Uncached `drain` LPT test-sum floor (`4,544 s / k`) | `drain.test` processor-equivalents (`k × 0.22`) | Trade-off for a solo gate |
| :--- | :--- | :--- | :--- | :--- |
| **Status quo (`12` fixed)** | `12` (`0.375×`) | **`378.7 s` (`6.3 min`)** | `~2.6` | Leaves most of `32` processors idle; cannot finish an uncached `drain` run near three minutes. |
| **`1 × os.cpu_count()`** | `32` (`1×`) | **`142.0 s` (`2.4 min`)** | `~7.0` | Still leaves processors underfed while shards block on child processes and SQLite (`~78%` non-CPU wait per `drain.test`), leaving little margin once `go test` startup and LPT tail skew are added. |
| **`2 × os.cpu_count()` (chosen in `D-GTC4`)** | **`64` (`2×`)** | **`71.0 s` (`1.2 min`)** | **`~14.1`** (+ child `git`/SQLite work) | Cuts the uncached shard-sum floor **`5.3×`** (`378.7 s -> 71.0 s`), keeps a solo gate's `drain.test` binaries and their child processes fed across `32` processors without inferring load, and stays well inside the `81 GB`-free / `0.00` PSI envelope measured on 2026-11-24 ([Section 3.2](#32-question-2-whether-memory-pressure-is-the-one-signal-worth-acting-on-and-what-acts-on-it)). |
| **`4 × os.cpu_count()`** | `128` (`4×`) | **`35.5 s` (`0.6 min`)** | `~28.2` (+ children) | Diminishing wall-time gain while `77` individual tests take `>= 10 s` (`31%` of total duration, out of scope here) and `go test` still links/starts per shard rather than building the test binary once per package (also out of scope here). |

When multiple gates run at the same time, per-gate `MAX_SHARDS = 2 * (os.cpu_count() or 1)` sets how many shards each gate may dispatch on its own, while cross-run concurrency remains governed by [`OQ-GTC1`](#OQ-GTC1) (kernel time-slicing across runnable threads under Option A, or the machine-wide counting semaphore under Option B).

#### Named slice to file: Slice `go-affected-max-shards`

So that [`D-GTC4`](#D-GTC4) can be filed and implemented as one self-contained repository change (unless overturned by the owner), **Slice `go-affected-max-shards`** is defined over [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py) and [`tests/go-affected.test.py`](../../tests/go-affected.test.py):

1. **Set `MAX_SHARDS = 2 * (os.cpu_count() or 1)`** in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py) (replacing `MAX_SHARDS = 12`), computed solely from processor count with zero processor-load or outside-load inspection ([`R-GTC5`](#R-GTC5)).
2. **Raise the shard `ThreadPoolExecutor` cap to match `MAX_SHARDS`:** change `ThreadPoolExecutor(max_workers=min(16, len(invocations)))` in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py) to `ThreadPoolExecutor(max_workers=min(MAX_SHARDS, len(invocations)))` so the `16`-worker pool cap does not serialize shards `17..MAX_SHARDS`.
3. **Use duration-based shard sizing up to `MAX_SHARDS` for unnarrowed large packages as well as narrowed subsets:** in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py)'s `len(tests) >= SHARD_THRESHOLD` branch, when recorded durations exist for the package, compute `shard_count = min(MAX_SHARDS, max(MIN_SHARDS, math.ceil(est_total / NARROWED_SHARD_DURATION_THRESHOLD)))` (keeping `min(MAX_SHARDS, max(MIN_SHARDS, len(non_edge) // 125))` only when duration data is absent) so a full unnarrowed `tools/tillr-drain/drain` run (`4,544 s` across `1,921` tests) scales to `MAX_SHARDS` (`64` on `32` processors) rather than stopping at `1,920 // 125 = 15` shards.
4. **Cover the slice in [`tests/go-affected.test.py`](../../tests/go-affected.test.py):** assert `MAX_SHARDS` equals `2 * (os.cpu_count() or 1)` across mocked processor counts, assert the execution pool allows up to `MAX_SHARDS` concurrent shard invocations rather than capping at `16`, and assert both narrowed and unnarrowed large packages reach `MAX_SHARDS` when estimated duration warrants it.

---

## 4. Priority among tillr's own processes and the jail cgroup boundary

### 4.1 Question 3: Measured reason for and against internal priority

Within tillr's own container, four classes of subprocesses compile and run tests:

1. **Trunk `repair` runs and gates** (reverting or fixing a red `main` tip);
2. **Candidate `gate`s** (verifying a finished lane commit so it can squash-land onto `main` and release its worktree);
3. **After-merge `trunk-proof`** (`heavyrun`, advancing the proved-green pointer asynchronously after commits have already landed); and
4. **Ordinary lane workers' own `go test` / `go build` commands** (targeted in-lane checks while a worker iterates).

**Measured reason FOR prioritizing `repair > gate > proof > worker`:**

- **Work-conserving under idle/moderate load, decisive during bursts:** Unlike a hard slot reservation (which leaves cores idle or blocks processes behind lock files), kernel CPU weights (`nice` or cgroup `cpu.weight`) are **100% work-conserving**. Whenever fewer than 32 threads are runnable, every class — including lane workers' `go test` commands and `trunk-proof` — receives `100%` of every available core with `0 ms` wait.
- **Shrinking the conflict window when cores saturate (`R-GTC2`):** During bursts like `00:02–00:23 ET` on 2026-11-24 (`loadavg 130–180`, when candidate gates, `trunk-proof`'s `12` shards, and active workers across `10` lanes all had runnable build/test threads at once), equal kernel weights divide CPU evenly per runnable thread. A candidate sitting in its gate has *already* spent all its LLM tokens and finished its code; every extra minute its gate stretches widens the window during which sibling commits move `main` and risk a `squash-conflict` rework (`R-GTC2`). Weighting `repair` highest, candidate `gate` second, `trunk-proof` third, and in-lane `worker` test subprocesses fourth shifts contested CPU cycles to trunk recovery and ready-to-land candidates while letting LLM-waiting or file-reading workers run unimpeded. *(If keeping the green pointer current is valued ahead of candidate landing latency, `proof` and `gate` swap to `repair > proof > gate > worker`.)*

**Measured reason AGAINST adding internal priority:**

- **Removing the pending-restart hold already eliminates the main cause of contention:** On 2026-11-24, spread-out gates earlier that night finished in **`5 to 8 minutes`** without CPU priority tiers; the `18 to 21 minute` stretch only occurred when `markRestartPending()` held four finished workers behind a `14`-minute `trunk-proof` and released six gates together. Once [Slice A of `gate-rework.md`](./gate-rework.md#run-affected-go-tests-without-holding-sibling-landings) and landing-during-proof remove that barrier, gates rarely pile up.
- **Worker suites are already narrow by rule:** Lane briefs forbid running full-module `go test ./...` suites inside lanes, so well-behaved worker test invocations run a single targeted `-run` pattern for a few seconds rather than twelve long-lived shards.

### 4.2 Verified cgroup state inside the jail and what setting subgroups would need

On 2026-11-24 at `00:33 ET` (and verified directly in this jail environment), inspecting `/proc/self/cgroup`, `/proc/self/mountinfo`, `/sys/fs/cgroup`, and `/bin/yolo-cglimit` establishes the exact cgroup state inside the jail today:

- `/proc/self/cgroup` is `0::/` (the container runs in its own cgroup namespace rooted at `/`).
- `/sys/fs/cgroup/cgroup.controllers` lists `cpu memory pids`.
- `/sys/fs/cgroup/cgroup.subtree_control` is **empty** (`""`).
- `/proc/self/mountinfo` mounts `/sys/fs/cgroup` **read-only**:
  `ro,nosuid,nodev,noexec,relatime - cgroup2 cgroup2 rw,nsdelegate,memory_recursiveprot,memory_hugetlb_accounting`.
- Consequently, `mkdir` of any child subgroup under `/sys/fs/cgroup` inside the jail fails immediately with **`Read-only file system`** (matching the earlier gate measurement recorded in [`docs/tillr/roadmap.md`](../tillr/roadmap.md)).
- Separately, `/bin/yolo-cglimit` (from yolo's `cgroup-delegate` loophole, which asks the host daemon to create cgroup scopes) is installed in the image, **but** (1) `cgroup-delegate` is not enabled in [`config/yolo-jail/config.toml`](../../config/yolo-jail/config.toml), and (2) `yolo-cglimit --help` supports only hard caps (`--cpu PCT`, `--memory LIMIT`, `--pids LIMIT`, `--name NAME`), **not** proportional `cpu.weight`.

**What jail change would be required for tillr to create `cpu.weight` subgroups from inside:**
To give tillr full in-jail cgroup v2 subgroup creation as the owner expected on 2026-11-24 (*"I also believe that we have full access to cgroups in here and can make any subgroups that we want"*), `yolo-jail`'s container setup needs **two** changes together:

1. **Mount `/sys/fs/cgroup` read-write (`rw`) inside the container** (replacing the container runtime's default `ro` mount on `/sys/fs/cgroup` over the already `nsdelegate`-mounted `cgroup2` superblock).
2. **Place the container's initial processes in a child leaf cgroup (such as `/init` or `/session`) at container startup and enable `+cpu` (or `+cpu +memory +pids`) in root `/sys/fs/cgroup/cgroup.subtree_control`.** Under Linux cgroup v2's **no-internal-processes rule**, a cgroup cannot enable domain controllers (`+cpu`, `+memory`) in `cgroup.subtree_control` while any live PID still resides directly in that cgroup (`0::/`); writing `+cpu` to `/sys/fs/cgroup/cgroup.subtree_control` fails with `EBUSY` unless PID 1 and the session's initial processes live in a child leaf like `/init`, leaving root `/` as pure interior routing above `/init` and `/tillr-{repair,gate,proof,worker}`.

*(Alternative host-daemon path: keep `/sys/fs/cgroup` read-only, enable `"cgroup-delegate"` in [`config/yolo-jail/config.toml`](../../config/yolo-jail/config.toml), and add `--cpu-weight <1..10000>` to `yolo-cglimit`. Direct writable cgroup delegation via (1)+(2) is cleaner because tillr can create persistent class subgroups once and spawn subprocesses directly into them via `UseCgroupFD` / `cgroup.procs` with zero host-daemon RPC per test binary.)*

### 4.3 Mechanism comparison: `nice` today vs cgroup v2 `cpu.weight` subgroups

In contrast to cgroup subgroups, **`nice` works inside the jail today with zero jail changes**: processes inside the jail start at `nice = 10` with `RLIMIT_NICE = (31, 31)`. On Linux (`sched_prio_to_weight`), raising niceness toward `19` never requires privileges (`nice 10` = weight `110`, `nice 12` = weight `71`, `nice 15` = weight `36`, `nice 19` = weight `15`; and under `RLIMIT_NICE = 31`, `nice` values down to `20 - 31 = -11` are also permitted if wider separation is wanted).

| Property | `nice` (`setpriority` / `os.nice`) | cgroup v2 `cpu.weight` subgroups (`/sys/fs/cgroup/tillr-*`) |
| :--- | :--- | :--- |
| **Works in the current jail with zero `yolo-jail` changes?** | **Yes** (verified live: base `nice = 10`, `RLIMIT_NICE = (31, 31)`). | **No** — requires mounting `/sys/fs/cgroup` `rw` + moving initial processes to `/init` + enabling `+cpu` in `cgroup.subtree_control` ([Section 4.2](#42-verified-cgroup-state-inside-the-jail-and-what-setting-subgroups-would-need)). |
| **Work-conserving (100% CPU use when higher classes are idle, zero lock waits)?** | **Yes.** | **Yes.** |
| **Inheritance by child `go`, `compile`, and `git` processes?** | **Automatic** across every `fork`/`exec`. | **Automatic** across every `fork`/`exec` once spawned in the subgroup (`CLONE_INTO_CGROUP` / `cgroup.procs`). |
| **Unit of weighting under contention** | **Per runnable thread:** if `6` concurrent gates run `72` threads at `nice 12` (`weight 71`) alongside `1` `trunk-proof` running `12` threads at `nice 15` (`weight 36`), gates get `(72×71) / (72×71 + 12×36) = 92%` of CPU; conversely, if `10` workers violate brief rules and spawn `120` threads at `nice 19` (`weight 15`) against `1` gate's `8` threads at `nice 10` (`weight 110`), `120×15 = 1800` still takes `67%` of CPU against `8×110 = 880`. | **Per class subgroup as a whole:** setting `tillr-repair = 400`, `tillr-gate = 200`, `tillr-proof = 100`, `tillr-worker = 50` divides contested CPU strictly `4 : 2 : 1 : 0.5` across active classes **regardless** of whether `tillr-worker` spawns `2` threads or `200` threads. |
| **Implementation surface in tillr** | ~15 lines setting niceness in `tillr-gate`, `heavyrun`, and `tillr-run` worker environment/launcher (`repair: +0 [10]`, `gate: +2 [12]`, `proof: +5 [15]`, `worker: +9 [19]`). | ~60 lines creating `/sys/fs/cgroup/tillr-{repair,gate,proof,worker}`, writing `cpu.weight`, and passing `SysProcAttr.UseCgroupFD` when spawning subprocesses (failing loudly with `exit 4` if `/sys/fs/cgroup` is not delegated, per [`R-GTC1`](#R-GTC1)). |

Because choosing between enabling writable delegated cgroups in `yolo-jail`, using `nice` immediately with zero jail change, or keeping flat scheduling is an owner decision, [`OQ-GTC3`](#OQ-GTC3) presents all three options rather than assuming the jail mount will or will not change.

---

## 5. Arrival-order queueing and candidate batching vs running ready gates at once

### 5.1 Question 4: Comparing all three gate schedules on the 2026-11-24 numbers

Using the operator's 2026-11-24 night measurements on this 32-processor host — **`5 to 8 minutes`** for a single Go gate earlier that night vs **`18 to 21 minutes`** per gate when **six** Go gates ran concurrently alongside `trunk-proof` from `00:02:18 ET` — we can compare all three gate-launching strategies directly on both **steady-state staggered arrivals** and **a 6-candidate simultaneous wave**:

| Strategy | Steady-state staggered arrivals (normal drain once Slice A removes restart holds) | 6-candidate burst: **1st** candidate gated at | 6-candidate burst: **6th** (all 6) gated at | 6-candidate burst: mean turnaround | Fast doc/config gate (`1–5 s`) during Go burst | Failure / conflict cost |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **A. Run every ready gate immediately (`all-at-once`, kernel-scheduled)** | **`5–8 min`** (`0` queue wait) | **`18–21 min`** | **`~20–26 min`** (`4` at `18–21 min`; `5th` started `+1.3 min`, `6th` `+5.9 min`) | **`18–21 min`** (`~3.3–3.5 min` per gate amortized!) | **`1–5 s`** (never waits behind Go gates) | Independent `(base_sha, head_sha^{tree})` verdict; `0` wasted re-gates when 1 of 6 is red. |
| **B1. Arrival-order FIFO (`1` Go gate at a time, solo `5–8 min` each)** | **`5–8 min`** when non-overlapping; adds `1–7 min` wait whenever 2 workers finish close together | **`5–8 min`** (`10–16 min` sooner for #1) | **`30–48 min`** (`6 × 5..8 min`, **`10–27 min` slower** to clear all 6!) | **`17.5–28.0 min`** (`(5+10+..+30)/6` to `(8+16+..+48)/6`) | **`1–5 s`** only if non-Go gates bypass the FIFO lock | Independent verdicts; requires cross-gate FIFO lock + watchdog wait exemption. |
| **B2. Arrival-order FIFO (`2` Go gates at a time, `~8–11 min` per pair)** | **`5–8 min`** (up to `~8–11 min` when 2 overlap) | **`~8–11 min`** (for #1 and #2) | **`~24–33 min`** (`3` waves of `8–11 min`, still **`4–12 min` slower** total makespan) | **`~16–22 min`** | **`1–5 s`** if non-Go gates bypass | Independent verdicts; still needs cross-gate coordinator lock. |
| **C. Speculative batching (`6` candidates merged into `1` combined tree and gated once)** | Either degrades to solo **`5–8 min`** (`batch = 1`) **or** adds deliberate idle hold waiting to collect a batch (violating [`R-GTC3`](#R-GTC3)) | **`~6–10 min`** *if all 6 are green* (`p^6`); **`20–32 min`** if 1 of 6 fails! | **`~6–10 min`** *if all 6 are green*; **`20–32 min`** under bisection (`1` batch + `⌈log2 6⌉ = 3` rounds) | **`~6–10 min`** best case; **`18–28+ min`** expected whenever per-item failure rate $\ge 15\%$ | Delayed to **`6–32 min`** unless split out of the batch | **Violates** [`landing-principles.md`](./landing-principles.md) [Sections 2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch), [3](./landing-principles.md#3-gate-what-will-be-the-tip-once), & [14](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran); `1` red item stalls `5` green items. |

Three concrete conclusions come straight out of those numbers:

1. **Why running all 6 gates at once (`18–21 min` total) cleared the wave *faster* than 1-at-a-time arrival order (`30–48 min` total):**
   A single gate does not keep all `32` processors busy from start to finish: `go-affected` selection, single-package compile warm-up (`go test -run '^$'`), `git` / SQLite I/O waits, and the LPT tail (when 10 short shards have finished and 1–2 long shards finish the last minute) leave substantial processor capacity unused. Running 6 solo gates end-to-end pays those underutilized tails six separate times (`6 × 5..8 = 30–48 min`), whereas starting all ready gates lets the Linux scheduler overlap one gate's compile/tail gaps with another gate's runnable shards, finishing all six in **`18–21 min`** (`~3.3–3.5 min` per gate of elapsed wall clock). Serial FIFO helps **only** Candidate #1 (`5–8 min` vs `18–21 min`) at the direct expense of Candidates #4–#6 (`20–48 min`) and slower total throughput. *(Note: if favoring an earlier-arriving gate's completion time during a rare overlap is ever desired without leaving cores idle during tails, stepping `nice` or `cpu.weight` slightly by gate arrival order would bias CPU toward Gate 1 while keeping 100% of cores utilized — without any FIFO lock.)*
2. **Why speculative batching loses on expected time as well as architecture:**
   Even if per-candidate gate failure probability is only $f = 15\%$, the probability that all $6$ candidates in a combined batch pass together is $(1 - 0.15)^6 = 37.7\%$ — meaning **`62.3%` of 6-candidate batches fail**. Whenever a batch fails, [`landing-principles.md` Section 14](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran) forbids convicting all 6 items; binary bisection adds $\lceil \log_2 6 \rceil = 3$ sequential `5–8 min` gate rounds (`20–32 min` total), or falling back to 6 parallel individual gates after the wasted `6–10 min` batch takes `24–31 min` — **slower** than running the 6 independent gates in parallel (`18–21 min`) in the first place, while holding green candidates hostage to a broken sibling and requiring a rewrite of [`tools/tillr-land`](../../tools/tillr-land/)'s `(base_sha, head_sha^{tree})` verdict invariant. And outside the pending-restart release (which Slice A removes), candidates arrive minutes apart, so batching would require holding finished candidates idle just to form a batch ([`R-GTC3`](#R-GTC3)).
3. **Why the real fix for the 2026-11-24 burst is already in flight:**
   With [Slice A of Section 11 in `gate-rework.md`](./gate-rework.md#run-affected-go-tests-without-holding-sibling-landings) and *"A candidate whose gate passed lands while a loop restart waits on the trunk proof"*, the four candidates from `23:49–23:55 ET` start gating immediately at `23:49–23:55 ET` instead of sitting frozen until `00:02:18 ET`, landing `10–15 minutes` earlier with no new gate queue or batcher at all. [`OQ-GTC4`](#OQ-GTC4) records the choice for the owner.

---

## 6. Checked against the Landing Principles

| Principle in [`landing-principles.md`](./landing-principles.md) | How this rewritten design satisfies it |
| :--- | :--- |
| **[Section 1: A refusal must be a verdict](./landing-principles.md#1-a-refusal-must-be-a-verdict)** & **[Section 8: A gate that cannot answer must say which](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)** | If an owner-selected mechanism ([`OQ-GTC2`](#OQ-GTC2) PSI file or [`OQ-GTC3`](#OQ-GTC3) delegated cgroup directory) cannot be read or written, the runner exits `4` (`result = "error"`, naming the path, zero fallback per [`R-GTC1`](#R-GTC1)) rather than recording a false candidate refusal. |
| **[Section 2: Re-application preserves the change](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch)** & **[Section 3: Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once)** | Preserved intact under [`OQ-GTC4`](#OQ-GTC4) Options A and B: every candidate is gated once over its own `base_sha..head_sha` and squash-landed via `Trunk.Squash` without multi-candidate tree batching. |
| **[Section 7: A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report)** | Under Option A of [`OQ-GTC1`](#OQ-GTC1), [`OQ-GTC2`](#OQ-GTC2), and [`OQ-GTC4`](#OQ-GTC4), zero user-space capacity holds exist (`nice` and `cpu.weight` never park processes); if any fixed semaphore ([`OQ-GTC1`](#OQ-GTC1) B), PSI guard ([`OQ-GTC2`](#OQ-GTC2) B), or gate FIFO ([`OQ-GTC4`](#OQ-GTC4) B) is chosen, its wait time is recorded explicitly apart from `exec_ms`. |
| **[Section 12: A principle cites the measurement that produced it](./landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it)** | Grounded in the 2026-11-24 `23:48–00:33 ET` timeline (`5–8 min` solo vs `18–21 min` 6-gate wave, `loadavg 130–180`, `memory.pressure = 0.00`, `81 GB` free, read-only `0::/` cgroup mount) and the 2026-11-22 fork profile (`2,601.9` forks/s, `4×` `block/git` shim overhead). |
| **[Section 14: Convict a run on what it did, not on what happened while it ran](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)** | Broken wall-clock tests are fixed as test defects rather than convicting slow runs (`R-GTC5`), and keeping unbatched per-candidate gates prevents one broken candidate in a 6-item batch from stalling or convicting five green siblings. |
| **[Section 15: Land the smallest functional change](./landing-principles.md#15-land-the-smallest-functional-change)** | Avoids building a ~300-line `/proc/stat` + `cpu.stat` lease coordinator; under the recommended leanings ([`OQ-GTC1`](#OQ-GTC1) A, [`OQ-GTC2`](#OQ-GTC2) A, [`OQ-GTC3`](#OQ-GTC3) B or A, [`OQ-GTC4`](#OQ-GTC4) A), implementation reduces to deleting dead lock-pool code in [`tools/go-test-shards.py`](../../tools/go-test-shards.py), applying `GOMAXPROCS=1` + `YOLO_BYPASS_SHIMS=1` in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py), and setting per-class `nice` (or `cpu.weight` subgroups). |

---

## Open Questions

1. 💬 **OQ-GTC1: Should tillr impose no machine-wide limit on concurrent test processes and let the Linux kernel schedule all runnable threads, or enforce one fixed ceiling at a constant multiple of `os.cpu_count()` (such as `2 × os.cpu_count() = 64` on 32 processors) with zero load inference?**

   On 2026-11-24 (`00:02–00:23 ET`), six candidate gates and `trunk-proof` ran together with `ENFORCE_SLOT_LIMITS = False` (`f633b5fb81`) on 32 processors: load average reached `130–180`, memory pressure stayed `0.00` with `81 GB` available, and all six gates completed in `18–21 minutes`. Each invocation already bounds its own fan-out (`MAX_SHARDS = 12` in [`tools/go-test-shards.py`](../../tools/go-test-shards.py), `min(16, len(invocations))` in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py), `min(8, len(wanted))` in [`tools/fork-check.py`](../../tools/fork-check.py)). Removing the machine-wide slot pool completely leaves all thread multiplexing to the kernel and deletes ~450 lines of lock-file code; keeping one fixed `2 × os.cpu_count()` counting semaphore (`64` slots on 32 cores, never reading CPU load) changes nothing below `64` concurrent shards while putting a hard ceiling on `fork`/`exec` churn and RSS if `10+` heavy Go gates ever run at once.

   - **A — Impose no cross-run limit on concurrent test processes, and delete the unused `slot-*` / `gate-*` / `proof-*` lock pool and `ENFORCE_SLOT_LIMITS` flag from `tools/go-test-shards.py`.** Each gate, proof, and worker test run keeps only its local per-invocation shard cap, the Linux scheduler multiplexes all runnable threads across processors, host isolation uses workspace/jail cgroups when needed, and cross-process slot locks and slot-wait watchdog workarounds disappear completely.
   - **B — Enforce one fixed machine-wide semaphore of `2 × os.cpu_count()` (`64` concurrent test processes on 32 processors), computed solely from processor count with zero load inference.** Normal 1–4 gate concurrency (`<= 64` shards) never waits, processors stay fed across `git` and I/O sleeps, and pathological `10+`-gate bursts cap at `64` simultaneous test binaries (`slot-00..63.lock`), at the cost of maintaining a flat `flock` semaphore and slot-wait telemetry.

   <!-- vantage: question id=OQ-GTC1 leaning="A — even the artificial 6-gate + trunk-proof burst on 2026-11-24 finished in 18–21 min with 0.00 memory pressure and 81 GB free, Slice A removes the pending-restart barrier that caused that burst, and deleting the cross-process lock pool removes an entire class of lock/wait state." -->

   _Leaning:_ A — even the artificial 6-gate + `trunk-proof` burst on 2026-11-24 finished in `18–21 min` with `0.00` memory pressure and `81 GB` free, Slice A removes the pending-restart barrier that caused that burst, and deleting the cross-process lock pool removes an entire class of lock/wait state.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-GTC2: Given that memory pressure is the only dynamic resource signal where overcommitment causes failure (`OOM`) rather than proportional kernel time-slicing, should enforcement stay strictly in the kernel and workspace/jail cgroups (`memory.high` / `memory.max`), or should tillr's shard runners also pause new shard spawns on `/sys/fs/cgroup/memory.pressure`?**

   At `00:33 ET` on 2026-11-24 following six concurrent Go gates plus `trunk-proof`, `/sys/fs/cgroup/memory.pressure` read `0.00` at `avg10`, `avg60`, and `avg300` with `81 GB` available. On Linux cgroup v2, if the owner sets `memory.high` on a workspace or jail cgroup, the kernel throttles allocating processes directly via page reclaim before `memory.max` invokes OOM. Alternatively, because `/sys/fs/cgroup/memory.pressure` is readable inside the jail today, `go-test-shards.py` and `go_affected.py` could pause starting shard `2..k` (while always letting `1` shard run) whenever `some avg10` exceeds a fixed stall threshold, protecting tighter-RAM hosts before OOM kills can abort a test binary.

   - **A — Leave memory enforcement entirely to the Linux kernel and any `memory.high` / `memory.max` cgroup limit configured on the workspace or jail; add zero memory-polling code to tillr.** Zero dead wait code on this `81 GB`-free (`0.00` PSI) machine, and kernel `memory.high` reclaim already throttles memory pressure cleanly without user-space polling whenever a jail memory cap is applied.
   - **B — Read `/sys/fs/cgroup/memory.pressure` in `go-test-shards.py` and `go_affected.py` before spawning shard `2..k` (always allowing `1` shard to run) and wait while `some avg10` exceeds a fixed threshold (such as `10.0%`).** Prevents OOM `SIGKILL`s from aborting running test binaries inside smaller-memory jails where `memory.max` is tight, at the cost of a PSI wait loop, loud-error handling if `/sys/fs/cgroup/memory.pressure` is absent, and watchdog wait reporting.

   <!-- vantage: question id=OQ-GTC2 leaning="A — measured memory pressure was 0.00 across avg10/avg60/avg300 with 81 GB free during six concurrent gates + proof, and cgroup v2 memory.high already throttles memory pressure in the kernel without user-space polling." -->

   _Leaning:_ A — measured memory pressure was `0.00` across `avg10`/`avg60`/`avg300` with `81 GB` free during six concurrent gates + proof, and cgroup v2 `memory.high` already throttles memory pressure in the kernel without user-space polling.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-GTC3: Should tillr prioritize CPU share among its own process classes (`repair > gate > proof > worker`), and if so, should we change `yolo-jail` so tillr can create cgroup v2 `cpu.weight` subgroups or use `nice` inside the existing jail?**

   Inside the jail today (`00:33 ET`, 2026-11-24), `/proc/self/cgroup` is `"0::/"`, `cgroup.controllers` is `"cpu memory pids"`, `cgroup.subtree_control` is empty, and `/sys/fs/cgroup` is mounted read-only (`mkdir` fails with `"Read-only file system"`), while `/bin/yolo-cglimit` exposes only hard `--cpu PCT` / `--memory` / `--pids` caps rather than `cpu.weight`. Creating `cpu.weight` subgroups inside the jail requires `yolo-jail` to mount `/sys/fs/cgroup` read-write, place initial container processes in a leaf child cgroup (such as `/init`) to satisfy cgroup v2's no-internal-processes rule, and enable `+cpu` in `/sys/fs/cgroup/cgroup.subtree_control` ([Section 4.2](#42-verified-cgroup-state-inside-the-jail-and-what-setting-subgroups-would-need)). Meanwhile, `nice` works immediately (`nice = 10`, `RLIMIT_NICE = (31, 31)`), weighting per runnable thread rather than per class subgroup. Both `cpu.weight` and `nice` are 100% work-conserving (zero idle cores and zero lock waits when uncontended) and shift CPU toward finishing candidate gates and trunk repairs when bursts saturate all 32 processors.

   - **A — Enable writable delegated cgroup v2 subgroups in `yolo-jail` (`rw` `/sys/fs/cgroup` + `/init` leaf cgroup + `+cpu` in `cgroup.subtree_control`) and place tillr's classes in `/sys/fs/cgroup/tillr-{repair,gate,proof,worker}` with `cpu.weight` (`400 / 200 / 100 / 50`).** Gives true class-level proportional CPU shares regardless of how many threads or `git` children each class spawns, and matches your expectation that jails can create arbitrary cgroup subgroups, requiring the `yolo-jail` container setup change first.
   - **B — Use `nice` inside tillr today with zero jail change (`repair` at inherited `+0` [`nice 10`], candidate `gate` at `+2` [`12`], `trunk-proof` at `+5` [`15`], ordinary worker test runs at `+9` [`19`]).** Works immediately inside the current read-only `/sys/fs/cgroup` jail in ~15 lines of code, is 100% work-conserving, and favors repairs and gates during bursts, with per-thread rather than per-subgroup weighting.
   - **C — Keep flat equal kernel priority across all tillr processes (`nice +0` inherited, no subgroups).** Zero code and zero jail changes, relying on Slice A + landing-during-proof to prevent multi-gate restart pileups and on brief rules keeping in-lane worker test runs short.

   <!-- vantage: question id=OQ-GTC3 leaning="B immediately (or A if you want writable delegated cgroups in yolo-jail across workspaces anyway) — nice gives work-conserving priority to repairs and candidate gates today with zero jail or lock complexity, while Option A gives cleaner group-level cpu.weight shares once yolo-jail mounts /sys/fs/cgroup rw with an /init leaf." -->

   _Leaning:_ B immediately (or A if you want writable delegated cgroups in `yolo-jail` across workspaces anyway) — `nice` gives work-conserving priority to repairs and candidate gates today with zero jail or lock complexity, while Option A gives cleaner group-level `cpu.weight` shares once `yolo-jail` mounts `/sys/fs/cgroup` `rw` with an `/init` leaf.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-GTC4: Should ready candidates keep starting their gates immediately in parallel, serialize Go gates in arrival order, or batch waiting candidates into one combined gate?**

   On the 2026-11-24 numbers (`5–8 min` per solo Go gate vs `18–21 min` for six concurrent Go gates + `trunk-proof`, [Section 5.1](#51-question-4-comparing-all-three-gate-schedules-on-the-2026-11-24-numbers)): running all six gates at once finished Candidate #1 in `18–21 min` and cleared **all six** within `~20–26 min` (`~3.3–3.5 min` per gate amortized, because the kernel overlaps one gate's compile/I/O/LPT-tail gaps with another gate's runnable shards). Serializing Go gates `1`-at-a-time in arrival order lands Candidate #1 sooner (`5–8 min`) while pushing Candidate #6 out to `30–48 min` (`10–27 min` slower total drain time). Batching six candidates into one combined tree finishes in `~6–10 min` *only* when all six pass; whenever `1` of `6` fails (`62%` of batches at a `15%` per-candidate failure rate), bisection takes `20–32 min`, stalls five green candidates behind one broken sibling, and requires replacing [`landing-principles.md`](./landing-principles.md) [Sections 2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch) and [3](./landing-principles.md#3-gate-what-will-be-the-tip-once)'s per-candidate `(base_sha, head_sha^{tree})` verdict contract. Meanwhile, [Slice A of `gate-rework.md`](./gate-rework.md#run-affected-go-tests-without-holding-sibling-landings) and landing-during-proof already eliminate the pending-restart hold that caused six gates to start together.

   - **A — Start every ready candidate's gate immediately and let the kernel schedule concurrent gates (no FIFO gate lock and no multi-candidate batching), relying on Slice A and landing-during-proof to remove pending-restart pileups.** Staggered steady-state arrivals land in `5–8 min`, rare bursts finish all six candidates `10–27 min` sooner than 1-at-a-time FIFO (`~20–26 min` vs `30–48 min`), fast doc/config gates land in `1–5 s`, and single-candidate verdicts stay intact.
   - **B — Serialize Go-test-bearing candidate gates in FIFO arrival order (`1` or `2` concurrent Go gates at a time; fast non-Go gates still run immediately).** Lands the first candidate of a simultaneous burst in `5–11 min` instead of `18–21 min`, at the cost of `24–48 min` total burst makespan and a cross-gate FIFO lock.
   - **C — Batch simultaneously waiting candidates onto one combined tree and gate the batch once, bisecting on failure (amending [`landing-principles.md`](./landing-principles.md) [Sections 2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch) and [3](./landing-principles.md#3-gate-what-will-be-the-tip-once)).** Cuts an all-green 6-candidate burst to `~6–10 min`, at the cost of `20–32 min` bisection stalls whenever 1 candidate in the batch is red and redesigning `tillr-land`'s single-candidate verdict binding.

   <!-- vantage: question id=OQ-GTC4 leaning="A — Slice A and landing-during-proof already remove the pending-restart hold that caused six gates to start together on 2026-11-24, and even in a 6-gate burst kernel-scheduled parallel gates clear all six in 18–21 min without serial tail waste or red-batch bisection." -->

   _Leaning:_ A — Slice A and landing-during-proof already remove the pending-restart hold that caused six gates to start together on 2026-11-24, and even in a 6-gate burst kernel-scheduled parallel gates clear all six in `18–21 min` without serial tail waste or red-batch bisection.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

Two sections, kept strictly separate so the owner's dated words are never presented as designer choices and every designer decision is clearly marked as subject to the owner's overturn.

### The owner's rulings

| ID | Ruling | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="R-GTC1"></a>`R-GTC1` | **No fallbacks — fail loudly and fix the root cause instead of masking errors behind a fallback.** Owner, 2026-11-20, verbatim ([`tillr-judge-and-lifecycle.md` Section 4.15](./tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27) and [`drain-timed-checkin`](../../.agents/skills/drain-timed-checkin/SKILL.md#model-policy-owner-2026-11-21)): *"why does Terra having some error mean that we automatically flip to Sol? I don't like that. I hate fallbacks. Fallbacks are almost always wrong. We need deliberate choices, not fallbacks. We need to fix what's wrong. We don't need to just randomly try something else."* And the same day on all non-model machinery: *"That is everything. ... If something fails, you fix it. If it fails loudly, you fix it. You don't hide it with a fallback."* | 2026-11-20 | [Section 1.2](#12-standing-owner-rulings-that-govern-this-design), [Section 4.3](#43-mechanism-comparison-nice-today-vs-cgroup-v2-cpuweight-subgroups) | — |
| <a id="R-GTC2"></a>`R-GTC2` | **Lane count is judged by wasted work (conflicts, rework, red trunks), with wait time only a secondary proxy.** Owner, 2026-11-21, verbatim ([`drain-timed-checkin`](../../.agents/skills/drain-timed-checkin/SKILL.md#lane-steps)): *"the reason why we have fewer lanes is because we're wasting a lot of work by having Lots of conflicts, rework, red trunks, all that stuff ... the amount of time waiting was sort of a proxy for that, but not the primary thing I care about."* | 2026-11-21 | [Section 1.2](#12-standing-owner-rulings-that-govern-this-design), [Section 4.1](#41-question-3-measured-reason-for-and-against-internal-priority) | — |
| <a id="R-GTC3"></a>`R-GTC3` | **Every unexplained pause is a bug to fix; landings should flow without random silent pauses.** Owner, 2026-11-20, verbatim ([`drain-timed-checkin`](../../.agents/skills/drain-timed-checkin/SKILL.md#the-bar-owner-2026-11-20)): *"You should be able to operate this drain in a way such that changes land in nearly the same speed as if you were running yourself as sub-agents. There shouldn't be random pauses, and if there is, that's a bug that you fix."* | 2026-11-20 | [Section 1.2](#12-standing-owner-rulings-that-govern-this-design), [Section 2.1](#21-the-2026-11-24-pending-restart-release), [Section 5.1](#51-question-4-comparing-all-three-gate-schedules-on-the-2026-11-24-numbers) | — |
| <a id="R-GTC4"></a>`R-GTC4` | **Budgets are targets with performance monitoring, stalls end checks, and high machine load must not fail valid work.** Owner, 2026-11-17 (`D-GR15` in [`gate-rework.md`](./gate-rework.md#decision-ledger)), verbatim: *"target budgets, watch for stalls, but don't let 'load is high' cause failures that would otherwise finish just fine."* And 2026-11-21 (`D-GR38` in [`gate-rework.md`](./gate-rework.md#decision-ledger)), verbatim: *"if you think drain suite selected tests are 40 seconds, let's do that, and monitor how it goes, just make sure we have perf monitoring all over it. 10 seconds was apsirational"*. | 2026-11-17; 2026-11-21 | [Section 1.2](#12-standing-owner-rulings-that-govern-this-design) | `watchdog.go` (`D-GR15`) |
| <a id="R-GTC5"></a>`R-GTC5` | **The kernel schedules runnable threads; never infer a limit from processor load or guess external loads, never justify a limit from broken fast-machine-assuming tests, isolate external host impact via workspace/jail cgroups, and weigh internal priority only if measurably useful.** Owner, 2026-11-24, verbatim: *"What does it mean for a core to be in use? I've never heard of any program scheduling cores and claiming that they're in use and allocating them. Like you run, you have your runnable threads, they will run as available and the whole point of a kernel is to schedule these things."* *"20 minutes doesn't sound that terrible, but also how did all the gates start together? What was starting them? Why were they running together? Why aren't they batched? ... Also, if that test assumes a fast machine, that test is broken. So clearly that's not an argument for anything. And regardless of what we pick, I definitely don't want to infer some limit, like pick something responsible based on the number of processors, like two times the number of processors or something like that, and just do it. Unless we're hitting memory pressure, more of them is not an issue, and I can apply cgroups to workspaces and jails, so I can limit it from hitting the rest of the machine if I need to, and Otherwise, I don't want you guessing about external loads. That's ridiculous. But prioritizing within our own machinery, that's something that's potentially useful, if we have a reason to think that that is useful."* *"I also believe that we have full access to cgroups in here and can make any subgroups that we want."* | 2026-11-24 | [Section 1.2](#12-standing-owner-rulings-that-govern-this-design), [Section 3](#3-concurrent-test-process-limits-and-memory-pressure), [Section 4](#4-priority-among-tillrs-own-processes-and-the-jail-cgroup-boundary), [Section 5](#5-arrival-order-queueing-and-candidate-batching-vs-running-ready-gates-at-once) | — |

### The designer's decisions

Each `D-GTC` row below is the design seat's engineering decision answering the operator's 2026-11-24 request within [`R-GTC1`](#R-GTC1)–[`R-GTC5`](#R-GTC5). None is an owner ruling, and the owner may overturn any of them.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-GTC1"></a>`D-GTC1` | **Retire processor-load admission, outside-load estimation, and capacity leases (`R-GTC5`):** drop the rejected 2026-11-24 draft's `/proc/stat` and `/sys/fs/cgroup/cpu.stat` sampler, "outside-busy" processor accounting, admission inequality, `holders/*.json` weight leases, and `capacity-wait.json` coordinator; leave host isolation to workspace/jail cgroups applied by the owner; treat any test that fails on a slow machine as a broken test to fix in its suite rather than a reason to cap concurrency; and settle whether any fixed `os.cpu_count()` multiple stays at all via [`OQ-GTC1`](#OQ-GTC1). **Rejected:** differential `/proc/stat` + `cpu.stat` headroom admission, `/proc/loadavg` throttling, and keeping `ENFORCE_SLOT_LIMITS = False` as a permanent dead flag once [`OQ-GTC1`](#OQ-GTC1) is ruled. The owner may overturn this. | 2026-11-24 | [Section 1.2](#12-standing-owner-rulings-that-govern-this-design), [Section 3.1](#31-question-1-whether-any-limit-on-concurrent-test-processes-is-needed-at-all) | — |
| <a id="D-GTC2"></a>`D-GTC2` | **Treat memory pressure as the sole dynamic resource boundary where excess concurrency causes process failure (`OOM`) rather than proportional kernel scheduling:** CPU saturation only time-slices runnable threads (`18–21 min` across 6 gates + `trunk-proof` on 2026-11-24 with `memory.pressure = 0.00` and `81 GB` available), whereas memory exhaustion triggers page-reclaim thrashing or OOM `SIGKILL`; leave the choice between relying strictly on kernel/cgroup `memory.high` + `memory.max` vs adding a `/sys/fs/cgroup/memory.pressure` shard-spawn guard to the owner in [`OQ-GTC2`](#OQ-GTC2). **Rejected:** acting on CPU runqueue depth, processor utilization, or shard wall-time stretch as dynamic admission signals. The owner may overturn this. | 2026-11-24 | [Section 3.2](#32-question-2-whether-memory-pressure-is-the-one-signal-worth-acting-on-and-what-acts-on-it) | — |
| <a id="D-GTC3"></a>`D-GTC3` | **Keep per-invocation subprocess hygiene (`GOMAXPROCS=1` on `-parallel 1` shards and shim-free `git` in `go_affected.py`) orthogonal to cross-run limits:** regardless of [`OQ-GTC1`](#OQ-GTC1)–[`OQ-GTC4`](#OQ-GTC4), single-threaded (`-parallel 1`) Go test shards set `GOMAXPROCS=1 -p 1` rather than spawning `32` Go runtime/compiler OS threads per shard, and [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py) matches [`tools/go-test-shards.py`](../../tools/go-test-shards.py)'s `YOLO_BYPASS_SHIMS=1` / clean `git` symlink plus `maintenance.auto=false` and `gc.auto=0` in `GIT_CONFIG_GLOBAL` to eliminate the `4×` `block/git` subshell fork multiplier measured on 2026-11-22. **Rejected:** letting `-parallel 1` shards default to `GOMAXPROCS=32` or routing test-fixture `git` calls through `block/git` subshells. The owner may overturn this. | 2026-11-24 | [Section 3.1](#31-question-1-whether-any-limit-on-concurrent-test-processes-is-needed-at-all) | — |
| <a id="D-GTC4"></a>`D-GTC4` | **Set per-gate `MAX_SHARDS = 2 * (os.cpu_count() or 1)` in `tools/gate/go_affected.py` (`64` on `32` processors, Slice `go-affected-max-shards`) under `R-GTC5`:** grounded in the 2026-11-24 (`02:07 ET`, base `16f401f5a3`) verifier measurement showing `tools/tillr-drain/drain`'s `1,921` top-level tests sum `4,544 s` uncached (`77` of `>= 10 s` = `31%`, `135` of `5–10 s` = `20%`, `412` of `2–5 s` = `27%`) while each `drain.test` shard uses `~22%` of one processor waiting on child processes and SQLite with zero cross-shard fixture lock, replace `MAX_SHARDS = 12` and the `16`-worker `ThreadPoolExecutor` cap (plus the `len(non_edge) // 125` unnarrowed cap when recorded durations exist) with `2 * (os.cpu_count() or 1)` and zero load inference, cutting a solo gate's uncached `drain` shard-sum floor from `378.7 s` to `71.0 s`. **Rejected:** keeping `MAX_SHARDS = 12` (`378.7 s` floor, `~2.6` `drain.test` processor-equivalents on `32` processors), `1 × os.cpu_count()` (`142.0 s` floor, `~7.0` `drain.test` processor-equivalents), `4 × os.cpu_count()` (`128` separate `go test` invocations per gate before once-per-package binary builds exist), or inferring shard count from machine load. This is the designer's decision; the owner may overturn it. | 2026-11-24 | [Section 3.3](#33-per-gate-shard-count-2026-11-24-measurement-and-processor-multiple-decision) | — |
