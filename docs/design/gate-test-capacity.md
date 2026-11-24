---
title: "Gate test concurrency, memory pressure, and internal process priority under kernel scheduling"
status: in-review
stage: DESIGN
tags:
  - tillr
  - gate
  - capacity
  - concurrency
  - priority
  - testing
  - design
summary: "How many test processes tillr runs at once, and who decides. The kernel schedules runnable threads; tillr never samples processor load or estimates load outside itself. One gate's shard ceiling is twice the processor count (built). Four choices are open for the owner: a fixed cross-run ceiling or none, a memory-pressure guard or kernel limits alone, priority among tillr's own process classes, and whether ready gates run at once, in arrival order, or batched."
next: "The owner rules on OQ-GTC1 through OQ-GTC4 and reviews D-GTC1 through D-GTC4."
depends-on:
  - ../reference/gate.md
  - landing-principles.md
  - tillr-vocabulary.md
  - tillr-judge-and-lifecycle.md
  - tillr-scheduling-and-notifications.md
vantage:
  status-chip: true
---

# Gate test concurrency, memory pressure, and internal process priority under kernel scheduling

**Status:** The per-gate shard ceiling (`D-GTC4`) and the shim-free `git` environment of `D-GTC3` are in the tree. Nothing else here is built: no cross-run limit is enforced, no memory-pressure guard exists, and no priority is set among tillr's own processes. The owner's rulings are quoted in [Section 1](#1-the-rulings-that-govern-this-design); the designer's decisions (`D-GTC1` to `D-GTC4`) may be overturned by the owner; four policy choices are open in [`OQ-GTC1`](#OQ-GTC1) to [`OQ-GTC4`](#OQ-GTC4).

> **In short.** Scheduling runnable threads across processors is the kernel's job, protecting the rest of a machine belongs to the cgroup the workspace runs in, and a test that fails because the machine is busy is a broken test. So tillr builds no user-space processor scheduler. What it may still choose is a fixed ceiling on test processes, a guard on memory pressure, a priority order among its own process classes, and the order in which ready gates start.

- **Start at:** [Section 2](#2-when-gates-run-together) for when gates overlap and why they are not batched, [Section 3](#3-concurrent-test-process-limits-and-memory-pressure) for limits and memory, [Section 4](#4-priority-among-tillrs-own-processes) for priority, and [Section 5](#5-arrival-order-and-batching-against-running-ready-gates-at-once) for the schedule comparison.
- **Needs your ruling:** [`OQ-GTC1`](#OQ-GTC1), [`OQ-GTC2`](#OQ-GTC2), [`OQ-GTC3`](#OQ-GTC3) and [`OQ-GTC4`](#OQ-GTC4).
- **Reads with:** [the gate reference](../reference/gate.md), which owns `go-affected`, the detached landing gate and the stall watchdog; [the landing principles](./landing-principles.md), which own the single-candidate verdict and the three-way squash; and [the vocabulary](./tillr-vocabulary.md).

**The measurement the design rests on.** On one 32-processor machine, a Go gate running alone took 5 to 8 minutes. Six Go gates running together beside a trunk proof took 18 to 21 minutes each, at a load average of 130 to 180, with the container's memory pressure at 0.00 and 81 GB free. A later reading of host-wide memory pressure under five or six gates and a proof showed 11% to 15% of time stalled over ten seconds, with no process killed.

---

## 1. The rulings that govern this design

Five owner rulings bind it (`R-GTC1` to `R-GTC5` in [the owner's rulings](#the-owners-rulings)).

1. **No fallbacks (`R-GTC1`).** Whatever the owner picks in the open questions is enforced directly. If a chosen mechanism needs an interface that is missing or unwritable, the runner fails loudly, naming the path, and does not drop to another mode. No dead toggle is left behind.
2. **Lane count is judged by wasted work (`R-GTC2`).** A priority or ordering rule inside tillr is worth having only if it reduces wasted work, such as a shorter conflict window for a finished candidate or a faster trunk repair.
3. **Every pause is a bug with a cause (`R-GTC3`).** A barrier that holds a finished candidate back from its gate or its landing works against the flow, including holding a ready candidate idle to assemble a batch.
4. **Budgets are targets, stalls end checks, and high load must not fail valid work (`R-GTC4`).** A test process making steady progress is never killed by a wall-clock deadline because concurrency stretched its gate. The stall watchdog ends only a check that makes no progress ([budgets and the stall watchdog](../reference/gate.md#budgets-and-the-stall-watchdog)).
5. **The kernel schedules runnable threads (`R-GTC5`).** This binds four things:
   - tillr never reads `/proc/stat`, `cpu.stat`, `/proc/<pid>/stat` or `/proc/loadavg` to decide whether to start a test process, never defines "in-use" processors, and never estimates load outside itself (`D-GTC1`);
   - a ceiling, if one is kept, is a fixed multiple of `os.cpu_count()`;
   - protecting the rest of the machine belongs to the cgroup limits on the workspace or container, set by its owner outside tillr;
   - a test that assumes a fast machine is defective test code ([`tools/gate/checks/elapsed-time.py`](../../tools/gate/checks/elapsed-time.py) checks changed test files), and is never an argument for a limit.

## 2. When gates run together

### 2.1 What makes several gates start at once

The loop starts one gate per finished candidate as soon as its worker exits, so gates start staggered. Several start together only when something held finished candidates and released them at once. The one such hold measured was a pending loop restart waiting on a running trunk proof: while the restart was pending the loop started no gate and landed nothing, and released every waiting candidate when the proof ended.

Two mechanisms remove that hold. [The landing gate](../reference/gate.md#the-landing-gate) runs detached, so the loop can replace itself across live gates; and the restart wait is skipped while a trunk proof is active for a candidate whose gate passed (`waitForRestartAnswer` in [`tools/tillr-drain/drain/restart_work.go`](../../tools/tillr-drain/drain/restart_work.go)).

### 2.2 Why gates are not batched

Candidates are gated one at a time, not combined into one tree, because of two landing principles:

- **One candidate, one verdict.** Each candidate is gated once over its own diff, and its verdict binds `(base_sha, head_sha^{tree})`. `Trunk.Squash` in [`tools/tillr-land/land/trunk.go`](../../tools/tillr-land/land/trunk.go) then merges that diff onto the trunk tip without another gate run ([landing principles 2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch) and [3](./landing-principles.md#3-gate-what-will-be-the-tip-once)).
- **Clean attribution.** A red check convicts the one candidate it ran on ([landing principle 14](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)). A red batch of several candidates names none of them, and needs bisection before any can be convicted or cleared.

## 3. Concurrent test-process limits and memory pressure

### 3.1 Whether any cross-run limit is needed

No cross-run limit is enforced: `ENFORCE_SLOT_LIMITS = False` in [`tools/go-test-shards.py`](../../tools/go-test-shards.py) leaves its lock pool unused. Each invocation bounds only its own fan-out: `MAX_SHARDS = 12` shard processes per module suite in `go-test-shards.py`, and `ThreadPoolExecutor(max_workers=min(16, len(invocations)))` per `go-affected` check in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py).

| Policy | With one or two gates | With six gates and a trunk proof | Machinery |
| :--- | :--- | :--- | :--- |
| **A. No cross-run limit**, and the lock pool is deleted | The kernel schedules every runnable thread; gates take their solo time | Every gate slows in proportion and all finish; nothing waits on a lock | No lock files, no wait state, no watchdog exemption for lock waits |
| **B. One fixed semaphore of `2 × os.cpu_count()`**, never reading load | Identical to A, since the count stays under the ceiling | The first `2 × N` shards run and the rest wait, bounding process churn and peak memory | One flat set of lock files, and wait logging so the watchdog does not read a lock wait as a stall |

The choice is [`OQ-GTC1`](#OQ-GTC1).

**Per-process hygiene, whichever is chosen (`D-GTC3`).** Two local changes reduce pointless thread and process creation without any coordinator:

1. A shard that runs one test at a time (`-parallel 1`) sets `GOMAXPROCS=1` and `-p 1`, so it does not start one runtime thread per processor. Not built: `rg -n 'GOMAXPROCS' tools/gate/go_affected.py tools/go-test-shards.py` finds nothing.
2. Test fixtures call the real `git` with automatic maintenance off, so one `git` call is one process. Built: `go_affected.py` sets `YOLO_BYPASS_SHIMS` and disables `maintenance.auto` and `gc.auto` in its isolated Git configuration, as `go-test-shards.py` does.

### 3.2 Memory pressure is the one signal worth acting on

Processor saturation and memory exhaustion fail differently (`D-GTC2`). When more threads are runnable than there are processors, the kernel time-slices them: nothing crashes, no verdict is corrupted, and total work done stays high. When memory runs out, the kernel thrashes and then kills a process, which turns a valid gate run into an incomplete one. Memory pressure is therefore the only dynamic signal where starting another test binary can do damage.

Two things can act on it:

- **The kernel and the workspace's cgroup.** With `memory.high` set on the cgroup, the kernel throttles allocating tasks before `memory.max` kills one. tillr needs no code.
- **A check in the shard runners.** `memory.pressure` is readable without a writable cgroup. A runner could read it before starting each shard after its first, and wait while pressure is above a fixed threshold. One shard per runner always starts, so progress never deadlocks. This adds wait code and wait reporting.

The choice is [`OQ-GTC2`](#OQ-GTC2).

### 3.3 One gate's shard ceiling

**Measurement.** One full run of the drain package's tests, uncached, on a 32-processor machine: 1,921 top-level tests summing 4,544 seconds, with each test process using about 22% of one processor because the tests mostly wait on child processes and SQLite. No test waits on a lock shared between shards. At twelve shards, a gate running alone was limited by its shard count and not by the machine.

**Decision (`D-GTC4`, designer's; built).** `MAX_SHARDS` in `go_affected.py` is `2 * os.cpu_count()`, and 12 when the processor count cannot be read. `os.cpu_count()` returns `None` when the count is undetermined, so 12 is a stated value for that case and not a hidden fallback. `test_max_shards_scales_with_cpu_count` in [`tests/go-affected.test.py`](../../tests/go-affected.test.py) pins both.

| Ceiling | On 32 processors | Shortest possible run of the measured suite | Trade-off for a gate running alone |
| :--- | ---: | ---: | :--- |
| 12, fixed | 12 | 6.3 min | Caps every package regardless of the machine |
| `1 × os.cpu_count()` | 32 | 2.4 min | Leaves processors idle, since each test process waits about 78% of the time |
| **`2 × os.cpu_count()`** | **64** | **1.2 min** | A plain multiple with no load inference |
| `4 × os.cpu_count()` | 128 | 0.6 min | Little gain while a few long tests hold a third of the total |

What this leaves alone, each for its own change: the 16-worker pool and the `len(non_edge) // 125` sizing formula in `go_affected.py`; `MAX_SHARDS = 12` in `go-test-shards.py`, so the two tools plan different shard counts for the largest package; shortening the slowest tests; and compiling each package's test binary once.

## 4. Priority among tillr's own processes

### 4.1 The case for and against

Four classes of process compile and run tests: trunk **repair** runs, candidate **gates**, the after-merge **trunk proof**, and a lane **worker's** own test commands.

**For** ordering them `repair > gate > proof > worker`: a kernel weight is work-conserving, so when processors are free every class gets all it can use and nothing waits. When processors saturate, a candidate in its gate has already spent its tokens, and every minute its gate stretches widens the window in which sibling landings move the trunk and force rework (`R-GTC2`). Weighting shifts contested time to repairs and ready candidates.

**Against:** staggered gates do not contend much, the hold that made them start together is removed ([Section 2.1](#21-what-makes-several-gates-start-at-once)), and a lane worker's test runs are narrow by its brief.

### 4.2 Two mechanisms

| Property | `nice` | cgroup v2 `cpu.weight` subgroups |
| :--- | :--- | :--- |
| Works with a read-only `/sys/fs/cgroup` | Yes; raising niceness needs no privilege | No. It needs the mount writable, the container's first processes moved to a leaf cgroup, and `+cpu` enabled in `cgroup.subtree_control` (a cgroup cannot enable a controller for children while it holds processes itself) |
| Work-conserving | Yes | Yes |
| Inherited by child processes | Yes | Yes |
| Unit of weighting | Each runnable thread, so a class that starts many threads outweighs its niceness | Each class as a whole, however many threads it starts |
| Code in tillr | Setting niceness where the gate, the proof and a worker are launched | Creating the subgroups, writing weights and spawning into them, failing loudly when the mount is not writable (`R-GTC1`) |

The choice is [`OQ-GTC3`](#OQ-GTC3).

## 5. Arrival order and batching against running ready gates at once

Three schedules, compared on the measurement above (5 to 8 minutes alone, 18 to 21 minutes for six together):

| Schedule | Staggered arrivals | Six candidates at once: first done | All six done | A fast documentation gate during the burst | Cost of one red candidate |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **A. Start every ready gate** | 5–8 min, no wait | 18–21 min | 20–26 min | Seconds | None for the others |
| **B. Arrival order, one Go gate at a time** | 5–8 min, plus a wait when two finish close together | 5–8 min | 30–48 min | Seconds, if it bypasses the queue | None for the others; needs a cross-gate lock |
| **C. One combined gate for the batch** | Solo time, or a deliberate hold to collect a batch | 6–10 min if all pass | 6–10 min if all pass; 20–32 min with bisection | Delayed with the batch | One red candidate stalls the rest |

Two conclusions follow:

1. **Running all at once clears the batch sooner than arrival order.** A gate alone does not keep every processor busy: selection, compile warm-up, I/O waits and the last long shards leave capacity unused. Serial gates pay those gaps once each; concurrent gates overlap them. Arrival order helps only the first candidate, at the expense of the rest.
2. **Batching loses on expected time as well as on attribution.** At a 15% failure rate per candidate, a batch of six passes together 38% of the time. A failed batch needs about three more gate rounds to bisect, which is slower than six independent gates run together, and it requires replacing the single-candidate verdict of [Section 2.2](#22-why-gates-are-not-batched).

The choice is [`OQ-GTC4`](#OQ-GTC4).

## 6. Checked against the landing principles

| Principle | How the design meets it |
| :--- | :--- |
| [A refusal must be a verdict](./landing-principles.md#1-a-refusal-must-be-a-verdict); [a gate that cannot answer must say which](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which) | A chosen mechanism whose file cannot be read or written ends the run as an error naming the path, never as a refusal of the candidate |
| [Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once) | Kept under every option but batching |
| [A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report) | A weight never parks a process. A semaphore, a pressure guard or a gate queue, if chosen, records its wait time apart from execution time |
| [A principle cites the measurement that produced it](./landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it) | The measurements are stated at the top and in [Section 3.3](#33-one-gates-shard-ceiling) |
| [Convict a run on what it did](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran) | A slow run is not a failed run, and one candidate's red check never convicts a sibling |
| [Land the smallest functional change](./landing-principles.md#15-land-the-smallest-functional-change) | No load-sampling coordinator is built; under the leanings the work is deleting the unused lock pool and setting a weight per class |

---

## Open Questions

1. 💬 **OQ-GTC1: Should tillr impose no machine-wide limit on concurrent test processes, or one fixed ceiling at a constant multiple of `os.cpu_count()`?**

   Each invocation already bounds its own fan-out ([Section 3.1](#31-whether-any-cross-run-limit-is-needed)). Six gates and a trunk proof ran together without a limit and all finished, with no memory pressure.

   - **A — No cross-run limit; delete the unused lock pool and the `ENFORCE_SLOT_LIMITS` flag from `tools/go-test-shards.py`.** The kernel multiplexes every runnable thread, and cross-process locks and their wait handling disappear.
   - **B — One fixed semaphore of `2 × os.cpu_count()` test processes, computed from the processor count alone.** Ordinary concurrency never waits, and an unusual burst is capped, at the cost of maintaining the lock files and wait reporting.

   <!-- vantage: question id=OQ-GTC1 leaning="A — an unlimited six-gate burst finished with no memory pressure, the hold that caused the burst is removed, and deleting the lock pool removes a class of wait state." -->

   _Leaning:_ A — an unlimited six-gate burst finished with no memory pressure, the hold that caused the burst is removed, and deleting the lock pool removes a class of wait state.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-GTC2: Should memory enforcement stay in the kernel and the workspace's cgroup limits, or should the shard runners also pause new shards on memory pressure?**

   Memory is the only resource where overcommitment kills work ([Section 3.2](#32-memory-pressure-is-the-one-signal-worth-acting-on)).

   - **A — Leave it to the kernel and any `memory.high` or `memory.max` set on the workspace; add no polling code.** No wait code on a machine with memory to spare, and the kernel already throttles under `memory.high`.
   - **B — Read `memory.pressure` before starting each shard after the first, and wait while it is above a fixed threshold.** Protects a tightly limited container from a kill mid-test, at the cost of a wait loop, a loud error when the file is absent, and wait reporting.

   <!-- vantage: question id=OQ-GTC2 leaning="A — the measured burst showed no container memory pressure, and cgroup v2 memory.high already throttles in the kernel." -->

   _Leaning:_ A — the measured burst showed no container memory pressure, and cgroup v2 `memory.high` already throttles in the kernel.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-GTC3: Should tillr weight processor share among its own process classes (`repair > gate > proof > worker`), and by which mechanism?**

   Both mechanisms are work-conserving; they differ in what they need from the container and in the unit they weight ([Section 4.2](#42-two-mechanisms)).

   - **A — cgroup v2 `cpu.weight` subgroups, one per class.** True class-level shares however many threads a class starts; needs a writable cgroup mount with the container's first processes in a leaf cgroup.
   - **B — `nice`, set where each class is launched.** Works with a read-only cgroup mount and little code; weights each thread, not each class.
   - **C — No priority.** No code; relies on gates rarely overlapping and on worker test runs staying narrow.

   <!-- vantage: question id=OQ-GTC3 leaning="B — nice gives work-conserving priority to repairs and candidate gates with no change to the container; A gives cleaner class-level shares where the cgroup mount is writable." -->

   _Leaning:_ B — `nice` gives work-conserving priority to repairs and candidate gates with no change to the container; A gives cleaner class-level shares where the cgroup mount is writable.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-GTC4: Should ready candidates keep starting their gates at once, run their Go gates in arrival order, or be batched into one combined gate?**

   [Section 5](#5-arrival-order-and-batching-against-running-ready-gates-at-once) compares the three on measured times.

   - **A — Start every ready gate and let the kernel schedule them.** Clears a burst soonest in total, keeps fast gates fast and keeps the single-candidate verdict.
   - **B — Run Go gates in arrival order, one or two at a time; other gates start at once.** The first candidate of a burst finishes sooner and the last much later, and a cross-gate lock is needed.
   - **C — Gate waiting candidates as one combined tree, bisecting on failure.** Fastest when every candidate passes; one red candidate stalls the rest, and landing principles 2 and 3 must be amended.

   <!-- vantage: question id=OQ-GTC4 leaning="A — the hold that made gates start together is removed, and even in a burst concurrent gates finish sooner in total than a queue or a bisected batch." -->

   _Leaning:_ A — the hold that made gates start together is removed, and even in a burst concurrent gates finish sooner in total than a queue or a bisected batch.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

Two tables, so the owner's words are never presented as the designer's choices.

### The owner's rulings

| ID | Ruling | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="R-GTC1"></a>`R-GTC1` | **No fallbacks.** "If something fails, you fix it. If it fails loudly, you fix it. You don't hide it with a fallback." See also [no automatic move to another model](./tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27) | 2026-11-20 | [Section 1](#1-the-rulings-that-govern-this-design) | — |
| <a id="R-GTC2"></a>`R-GTC2` | **Lane count is judged by wasted work.** "the reason why we have fewer lanes is because we're wasting a lot of work by having Lots of conflicts, rework, red trunks, all that stuff ... the amount of time waiting was sort of a proxy for that, but not the primary thing I care about." | 2026-11-21 | [Section 1](#1-the-rulings-that-govern-this-design), [Section 4.1](#41-the-case-for-and-against) | — |
| <a id="R-GTC3"></a>`R-GTC3` | **Every unexplained pause is a bug.** "There shouldn't be random pauses, and if there is, that's a bug that you fix." | 2026-11-20 | [Section 1](#1-the-rulings-that-govern-this-design) | — |
| <a id="R-GTC4"></a>`R-GTC4` | **Budgets are targets, stalls end checks, and load must not fail valid work.** "target budgets, watch for stalls, but don't let 'load is high' cause failures that would otherwise finish just fine." | 2026-11-17 | [Section 1](#1-the-rulings-that-govern-this-design) | The stall watchdog, in [the gate reference](../reference/gate.md#budgets-and-the-stall-watchdog) |
| <a id="R-GTC5"></a>`R-GTC5` | **The kernel schedules runnable threads; no limit is inferred from load.** "What does it mean for a core to be in use? I've never heard of any program scheduling cores and claiming that they're in use and allocating them. […] the whole point of a kernel is to schedule these things." "if that test assumes a fast machine, that test is broken. […] pick something responsible based on the number of processors, like two times the number of processors or something like that, and just do it. Unless we're hitting memory pressure, more of them is not an issue, and I can apply cgroups to workspaces and jails […] I don't want you guessing about external loads. […] But prioritizing within our own machinery, that's something that's potentially useful, if we have a reason to think that that is useful." | 2026-11-24 | [Section 1](#1-the-rulings-that-govern-this-design) | — |

### The designer's decisions

None of these is an owner ruling, and the owner may overturn any of them.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-GTC1"></a>`D-GTC1` | tillr never samples processor load, estimates outside load, or leases capacity. **Rejected:** admission by `/proc/stat` and `cpu.stat` headroom; throttling on load average; keeping `ENFORCE_SLOT_LIMITS = False` as a permanent dead flag once [`OQ-GTC1`](#OQ-GTC1) is ruled | 2026-11-24 | [Section 1](#1-the-rulings-that-govern-this-design), [Section 3.1](#31-whether-any-cross-run-limit-is-needed) | No load sampler exists; the flag remains |
| <a id="D-GTC2"></a>`D-GTC2` | Memory pressure is the only dynamic signal on which starting another test process can destroy work. **Rejected:** acting on run-queue depth, processor use or stretched wall time | 2026-11-24 | [Section 3.2](#32-memory-pressure-is-the-one-signal-worth-acting-on) | — |
| <a id="D-GTC3"></a>`D-GTC3` | Per-process hygiene is independent of any cross-run limit: `GOMAXPROCS=1` on single-test shards, and real `git` with maintenance off in test fixtures | 2026-11-24 | [Section 3.1](#31-whether-any-cross-run-limit-is-needed) | `git` environment only |
| <a id="D-GTC4"></a>`D-GTC4` | `MAX_SHARDS` in `tools/gate/go_affected.py` is `2 * os.cpu_count()`, and 12 when the count cannot be read. Nothing else in that file changes with it. **Rejected:** a fixed 12; inferring the count from load; changing `tools/go-test-shards.py` in the same step | 2026-11-24 | [Section 3.3](#33-one-gates-shard-ceiling) | Yes |
