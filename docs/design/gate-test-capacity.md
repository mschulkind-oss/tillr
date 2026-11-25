---
title: "Gate test concurrency, memory pressure, and internal process priority under kernel scheduling"
status: accepted
stage: DECIDED
stage-since: 2026-11-25
tags:
  - tillr
  - gate
  - capacity
  - concurrency
  - priority
  - testing
  - design
summary: "How many test processes tillr runs at once, and who decides. All four owner questions (OQ-GTC1 to OQ-GTC4) are ruled on 2026-11-25: no machine-wide limit on concurrent test processes (the unused slot pool in tools/go-test-shards.py is deleted), memory enforcement left to the kernel and container cgroup limits with no polling code (swap in use is not a signal), ready candidates gated one at a time without batching, and processor share weighted repair > gate > proof > worker via cgroup v2 cpu.weight subgroups when writable or nice otherwise. The per-gate shard ceiling (2 * os.cpu_count() in go_affected.py), shim-free git fixtures, and slot-pool deletion in go-test-shards.py are built; four-class priority, dead slot-observer cleanup, and GOMAXPROCS=1 shard hygiene remain."
next: "Build the four items of Section 7: startup priority detection and reporting, four-class process placement across tillr-drain and tillr-run, dead test-slot observer and watch-rule removal, and GOMAXPROCS=1 on single-test shards. No question is open; the owner may review D-GTC1 through D-GTC9."
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

**Status:** Every owner question ([`OQ-GTC1`](#OQ-GTC1) to [`OQ-GTC4`](#OQ-GTC4)) is ruled as of 2026-11-25. Built in the tree: the per-gate shard ceiling (`D-GTC4`), the shim-free `git` fixture environment of `D-GTC3`, starting each ready gate one at a time without batching ([`OQ-GTC4`](#OQ-GTC4)), omitting any memory-pressure poller ([`OQ-GTC2`](#OQ-GTC2); `rg -n 'memory\.pressure' tools/` returns nothing), and deleting `ENFORCE_SLOT_LIMITS` and the cross-run file-lock slot pool from [`tools/go-test-shards.py`](../../tools/go-test-shards.py) (`7e896facad`, [`OQ-GTC1`](#OQ-GTC1); `rg -n 'ENFORCE_SLOT_LIMITS' tools/` returns nothing). Not built: four-class process priority ([`OQ-GTC3`](#OQ-GTC3); `rg -n 'UseCgroupFD|cpu\.weight' tools/` returns nothing, while `WorkerNiceness = 10` in [`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go) currently lowers every `tillr-run` process group including trunk repairs), removing the dead slot-waiter observer (`TestSlotsObserver` in [`tools/tillr-drain/drain/test_slots.go`](../../tools/tillr-drain/drain/test_slots.go)) and its watch rule, and setting `GOMAXPROCS=1` on single-test shards (`D-GTC3`; `rg -n 'GOMAXPROCS' tools/gate/go_affected.py tools/go-test-shards.py` returns nothing). The owner's rulings are quoted verbatim in [Section 1](#1-the-rulings-that-govern-this-design) and [the owner's rulings](#the-owners-rulings); the designer's decisions (`D-GTC1` to `D-GTC9`) are not owner rulings and may be overturned by the owner.

> **In short.** Scheduling runnable threads across processors is the kernel's job, protecting the rest of a machine belongs to the cgroup the workspace runs in, and a test that fails because the machine is busy is a broken test. Tillr therefore imposes no machine-wide ceiling on test processes ([`OQ-GTC1`](#OQ-GTC1)), leaves memory enforcement to the kernel and container limits without polling ([`OQ-GTC2`](#OQ-GTC2)), starts ready gates one at a time without batching ([`OQ-GTC4`](#OQ-GTC4)), and weights processor share among its own process classes (`repair > gate > proof > worker`) with cgroup v2 `cpu.weight` subgroups where it can create them and `nice` where it cannot ([`OQ-GTC3`](#OQ-GTC3)) — sharing a busy machine without ever holding work back.

- **Start at:** [Section 2](#2-when-gates-run-together) for when gates overlap and why they are not batched, [Section 3](#3-concurrent-test-process-limits-and-memory-pressure) for limits and memory, [Section 4](#4-priority-among-tillrs-own-processes) for the process-priority specification, [Section 5](#5-arrival-order-and-batching-against-running-ready-gates-at-once) for the schedule comparison, and [Section 7](#7-follow-on-build-items) for the build items that follow.
- **Needs your ruling:** None.
- **Reads with:** [the gate reference](../reference/gate.md), which owns `go-affected`, the detached landing gate and the stall watchdog; [the landing principles](./landing-principles.md), which own the single-candidate verdict and the three-way squash; [the gate batching research record](../research/gate-batching.md), which records the schedule comparison and the 2026-11-25 burst measurement; and [the vocabulary](./tillr-vocabulary.md).

**The measurements the design rests on.** On one 32-processor machine on 2026-11-24, a Go gate running alone took 5 to 8 minutes. Six Go gates running together beside a trunk proof took 18 to 21 minutes each, at a load average of 130 to 180, with the container's memory pressure at 0.00 and 81 GB free. A host-wide memory-pressure reading under five or six gates and a proof showed 11% to 15% of time stalled over ten seconds, with no process killed. On 2026-11-25, the operating session observed 7 GB in swap with almost no swap traffic and no process killed, and verified in the operating jail that `/sys/fs/cgroup` is mounted read-only (`mkdir` fails with `"Read-only file system"`) with controllers `cpuset cpu io memory pids` listed.

---

## 1. The rulings that govern this design

Nine owner rulings bind it (`R-GTC1` to `R-GTC5` and [`OQ-GTC1`](#OQ-GTC1) to [`OQ-GTC4`](#OQ-GTC4) in [the owner's rulings](#the-owners-rulings)).

1. **No fallbacks (`R-GTC1`).** Whatever the owner picks is enforced directly. If a chosen mechanism needs an interface that is missing or unwritable, the runner fails loudly, naming the path, and does not drop to another mode. No dead toggle is left behind. The single exception in this design is the startup choice between cgroup v2 subgroups and `nice` in [`OQ-GTC3`](#OQ-GTC3), which the owner named explicitly; once a loop selects `cgroup` at startup, any runtime failure to place a child into its subgroup still fails loudly naming the path.
2. **Lane count is judged by wasted work (`R-GTC2`).** A priority or ordering rule inside tillr is worth having only if it reduces wasted work, such as a shorter conflict window for a finished candidate or a faster trunk repair.
3. **Every pause is a bug with a cause (`R-GTC3`).** A barrier that holds a finished candidate back from its gate or its landing works against the flow, including holding a ready candidate idle to assemble a batch.
4. **Budgets are targets, stalls end checks, and high load must not fail valid work (`R-GTC4`).** A test process making steady progress is never killed by a wall-clock deadline because concurrency stretched its gate. The stall watchdog ends only a check that makes no progress ([budgets and the stall watchdog](../reference/gate.md#budgets-and-the-stall-watchdog)).
5. **The kernel schedules runnable threads (`R-GTC5`).** This binds four things:
   - tillr never reads `/proc/stat`, `cpu.stat`, `/proc/<pid>/stat` or `/proc/loadavg` to decide whether to start a test process, never defines "in-use" processors, and never estimates load outside itself (`D-GTC1`);
   - a per-invocation shard ceiling is a fixed multiple of `os.cpu_count()`;
   - protecting the rest of the machine belongs to the cgroup limits on the workspace or container, set by its owner outside tillr;
   - a test that assumes a fast machine is defective test code ([`tools/gate/checks/elapsed-time.py`](../../tools/gate/checks/elapsed-time.py) checks changed test files), and is never an argument for a limit.
6. **No machine-wide limit on concurrent test processes ([`OQ-GTC1`](#OQ-GTC1)).** Option **A** (`"1. A 2. A. swap means nothing. 3. we should use cgroups if available, can fallback to nice, yolo-cgroups gives you full access I think. do your best."`, 2026-11-25): tillr imposes no cross-run test-process limit, and the unused lock pool and `ENFORCE_SLOT_LIMITS` flag are deleted.
7. **Memory enforcement stays with the kernel and container limits, and swap in use is not a signal ([`OQ-GTC2`](#OQ-GTC2)).** Option **A** (`"2. A. swap means nothing."`, 2026-11-25): tillr adds no memory-pressure polling code and leaves memory enforcement to the kernel and any `memory.high` or `memory.max` limits on the workspace or container.
8. **Process-class priority uses cgroup v2 `cpu.weight` subgroups where writable, and `nice` where not ([`OQ-GTC3`](#OQ-GTC3)).** Ruled on 2026-11-25 (`"3. we should use cgroups if available, can fallback to nice, yolo-cgroups gives you full access I think. do your best."`): processor share among tillr's own process classes (`repair > gate > proof > worker`) uses cgroup v2 `cpu.weight` subgroups when tillr can create them and `nice` when it cannot. Using `nice` when cgroups are unavailable is the owner's own choice rather than a designer fallback under `R-GTC1`.
9. **Gates are not batched or queued in arrival order ([`OQ-GTC4`](#OQ-GTC4)).** Candidates keep being gated one at a time, each starting when it is ready; they are not combined into one tree and not held to collect a batch (`"ok, so let's not batch. close it with a resaerch report recorded for the future I guess?"`, 2026-11-25; recorded in [the gate batching research record](../research/gate-batching.md)).

## 2. When gates run together

### 2.1 What makes several gates start at once

The loop starts one gate per finished candidate as soon as its worker exits, so gates start staggered. Several start together only when something held finished candidates and released them at once. The one such hold measured was a pending loop restart waiting on a running trunk proof: while the restart was pending the loop started no gate and landed nothing, and released every waiting candidate when the proof ended.

Two mechanisms remove that hold. [The landing gate](../reference/gate.md#the-landing-gate) runs detached, so the loop can replace itself across live gates; and the restart wait is skipped while a trunk proof is active for a candidate whose gate passed (`waitForRestartAnswer` in [`tools/tillr-drain/drain/restart_work.go`](../../tools/tillr-drain/drain/restart_work.go)).

### 2.2 Why gates are not batched

Candidates are gated one at a time, not combined into one tree, because of two landing principles:

- **One candidate, one verdict.** Each candidate is gated once over its own diff, and its verdict binds `(base_sha, head_sha^{tree})`. `Trunk.Squash` in [`tools/tillr-land/land/trunk.go`](../../tools/tillr-land/land/trunk.go) then merges that diff onto the trunk tip without another gate run ([landing principles 2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch) and [3](./landing-principles.md#3-gate-what-will-be-the-tip-once)).
- **Clean attribution.** A red check convicts the one candidate it ran on ([landing principle 14](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)). A red batch of several candidates names none of them, and needs bisection before any can be convicted or cleared.

## 3. Concurrent test-process limits and memory pressure

### 3.1 No cross-run test-process limit

Each test-runner invocation bounds only its own fan-out: `MAX_SHARDS = 12` shard processes per module suite in [`tools/go-test-shards.py`](../../tools/go-test-shards.py), and `ThreadPoolExecutor(max_workers=min(16, len(invocations)))` with `MAX_SHARDS = 2 * os.cpu_count()` per `go-affected` check in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py).

| Policy | With one or two gates | With six gates and a trunk proof | Machinery |
| :--- | :--- | :--- | :--- |
| **A. No cross-run limit**, and the lock pool is deleted *(chosen)* | The kernel schedules every runnable thread; gates take their solo time | Every gate slows in proportion and all finish; nothing waits on a lock | No lock files, no wait state, no watchdog exemption for lock waits |
| **B. One fixed semaphore of `2 × os.cpu_count()`**, never reading load *(rejected)* | Identical to A, since the count stays under the ceiling | The first `2 × N` shards run and the rest wait, bounding process churn and peak memory | One flat set of lock files, and wait logging so the watchdog does not read a lock wait as a stall |

**Ruling ([`OQ-GTC1`](#OQ-GTC1), closed 2026-11-25).** Option **A** stands (`"1. A"`): tillr imposes no machine-wide limit on concurrent test processes, and the unused file-lock slot pool and `ENFORCE_SLOT_LIMITS` flag are deleted. Commit `7e896facad` removed `ENFORCE_SLOT_LIMITS` and the lock pool from [`tools/go-test-shards.py`](../../tools/go-test-shards.py) and [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py); removing the dead downstream slot-waiter observer (`TestSlotsObserver` in [`tools/tillr-drain/drain/test_slots.go`](../../tools/tillr-drain/drain/test_slots.go)), its watch rule, and stale reference notes is item 3 of [Section 7](#7-follow-on-build-items).

**Per-process hygiene (`D-GTC3`, designer's).** Two local settings reduce pointless thread and process creation without any cross-run coordinator:

1. A shard that runs one test at a time (`-parallel 1`) sets `GOMAXPROCS=1` and `-p 1`, so it does not start one Go runtime thread per processor. Not built: `rg -n 'GOMAXPROCS' tools/gate/go_affected.py tools/go-test-shards.py` returns nothing (item 4 of [Section 7](#7-follow-on-build-items)).
2. Test fixtures call the real `git` with automatic maintenance off, so one `git` call is one process. Built: `go_affected.py` and `go-test-shards.py` bypass shims and disable `maintenance.auto` and `gc.auto` in their isolated Git configurations.

### 3.2 Memory enforcement stays in the kernel, and swap is not a signal

Processor saturation and memory exhaustion fail differently (`D-GTC2`). When more threads are runnable than there are processors, the kernel time-slices them: nothing crashes, no verdict is corrupted, and total work done stays high. When memory runs out, the kernel thrashes and then kills a process, which turns a valid gate run into an incomplete one. Memory pressure is therefore the only dynamic resource where overcommitment can destroy work in progress.

**Ruling ([`OQ-GTC2`](#OQ-GTC2), closed 2026-11-25).** Option **A** stands (`"2. A. swap means nothing."`):

- **Kernel and container limits only.** Memory enforcement stays with the kernel and any `memory.high` or `memory.max` configured on the workspace or container cgroup. Under `memory.high`, the kernel throttles allocating tasks before `memory.max` invokes the OOM killer; tillr adds no `memory.pressure` polling loop, wait state, or wait reporting in the shard runners.
- **Swap occupancy is not a pressure signal.** Cold pages sitting in swap (including zram) do not indicate memory contention. On 2026-11-25, the operating session saw 7 GB in swap with almost no swap traffic and no process killed.

### 3.3 One gate's shard ceiling

**Measurement.** One full run of the drain package's tests, uncached, on a 32-processor machine: 1,921 top-level tests summing 4,544 seconds, with each test process using about 22% of one processor because the tests mostly wait on child processes and SQLite. No test waits on a lock shared between shards. At twelve shards, a gate running alone was limited by its shard count and not by the machine.

**Decision (`D-GTC4`, designer's; built).** `MAX_SHARDS` in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py) is `2 * os.cpu_count()`, and 12 when the processor count cannot be read (`os.cpu_count()` returns `None` when undetermined, so 12 is a stated value for that case and not a hidden fallback). `test_max_shards_scales_with_cpu_count` in [`tests/go-affected.test.py`](../../tests/go-affected.test.py) pins both.

| Ceiling | On 32 processors | Shortest possible run of the measured suite | Trade-off for a gate running alone |
| :--- | ---: | ---: | :--- |
| 12, fixed | 12 | 6.3 min | Caps every package regardless of the machine |
| `1 × os.cpu_count()` | 32 | 2.4 min | Leaves processors idle, since each test process waits about 78% of the time |
| **`2 × os.cpu_count()`** | **64** | **1.2 min** | A plain multiple with no load inference |
| `4 × os.cpu_count()` | 128 | 0.6 min | Little gain while a few long tests hold a third of the total |

What this leaves alone, each for its own change: the 16-worker pool and the `len(non_edge) // 125` sizing formula in `go_affected.py`; `MAX_SHARDS = 12` in `go-test-shards.py`, so the two tools plan different shard counts for the largest package; shortening the slowest tests; and compiling each package's test binary once.

## 4. Priority among tillr's own processes

### 4.1 The ruling, work conservation, and the four classes

Four classes of child process compile code and run tests inside a drain: trunk **repair**, candidate **gate**, after-merge trunk **proof**, and **worker** runs.

**Ruling ([`OQ-GTC3`](#OQ-GTC3), closed 2026-11-25).** The owner ruled: `"3. we should use cgroups if available, can fallback to nice, yolo-cgroups gives you full access I think. do your best."` Tillr weights processor share among its four classes in the order `repair > gate > proof > worker`, using cgroup v2 `cpu.weight` subgroups when tillr can create them at startup, and `nice` when it cannot. Using `nice` when cgroups are not writable is the owner's explicit choice, recorded as a narrow exception to the project's standing no-fallbacks rule (`R-GTC1`).

**Neither mechanism ever holds work back.** Both cgroup v2 `cpu.weight` and POSIX `nice` are purely work-conserving scheduler weights:

- Tillr never writes `cpu.max`, never parks a process in user space, and never delays starting a repair run, gate, proof, or worker.
- When the machine has idle processor capacity, any running class — including a `worker` run alone on a 32-processor host — receives 100% of the processors it can use.
- Weights take effect in the kernel scheduler only during instants when runnable threads compete for the same processors, shifting contested processor time toward trunk repairs and finished candidates whose tokens are already spent (`R-GTC2`, `R-GTC3`, [landing principle 7](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report)).

**Class weights and `nice` values (`D-GTC5`, designer's).** Every specific number and mapping below is the designer's decision and may be overturned by the owner:

| Order | Class | Processes in the class | cgroup v2 `cpu.weight` | `nice` target (`base` = loop's starting niceness; shown for `base = 0`) | Linux per-thread weight at `base = 0` |
| :--- | :--- | :--- | ---: | :--- | ---: |
| 1 (highest) | `repair` | Trunk fixer runs (`IsTrunkFixer`), trunk fixer and revert gates (`IsTrunkFixer` or `IsRevertItem`), and fixer named checks (`fixerCheck`) | `400` | `base + 0` (`0`) | `1024` |
| 2 | `gate` | Non-repair candidate landing gates (`--tillr-gate-supervisor`) and candidate base-check replays (`ProcessSteps.Replay`) | `200` | `min(19, base + 5)` (`5`) | `335` |
| 3 | `proof` | Asynchronous after-merge trunk proof (`HeavyRunner` / `JudgeHeavy`, `judgeHeavyGo`, `runHeavyLegacy`, and non-fixer `JudgeRequired`) | `100` | `min(19, base + 10)` (`10`) | `110` |
| 4 (lowest) | `worker` | Every non-fixer `tillr-run exec` run (lane `build`, `tricky`, `edit`, `test-write`, `test-run`, `plan`, `design`, `docs`, `classify`, `scout`, `review`, `verify`, and `triage`) and all child processes it starts | `50` | `min(19, base + 15)` (`15`) | `36` |

Why the two scales are shaped this way (`D-GTC5`):

- **cgroup v2 `cpu.weight` (`1..10000`, kernel default `100`):** Each step up doubles the whole class's processor share (`400 : 200 : 100 : 50`, or `8 : 4 : 2 : 1`), regardless of how many threads or test shards a class spawns. When `gate`, `proof`, and `worker` all contend without an active repair, `gate` receives `4/7` (57%) of contested capacity, `proof` receives `2/7` (29%), and all `worker` runs together share `1/7` (14%).
- **`nice` (`-20..19`):** On an unprivileged Linux host (`RLIMIT_NICE = 0` and no `CAP_SYS_NICE`), a process starting at niceness `base` can raise niceness toward `19` and cannot lower it. Non-negative steps of `+0, +5, +10, +15` clamped to `19` require no privilege from any starting niceness (`0, 5, 10, 15` when `tillr-drain` starts at `0`). Because `nice` weights each runnable thread rather than the whole class, a `+5` step (`1.25^5 ≈ 3.05×` per thread in Linux's `sched_prio_to_weight` table) gives steeper per-thread separation than the `2×` subgroup step so multi-lane worker threads do not drown a single repair or gate, while stopping at `15` rather than `19` or `SCHED_IDLE` keeps every class making steady progress.

### 4.2 Operating-jail cgroup facts and `yolo-cglimit`

Verified on 2026-11-25 in the operating jail:

- `/proc/self/cgroup` is `0::/`, and `/sys/fs/cgroup/cgroup.controllers` lists `cpuset cpu io memory pids`.
- `/sys/fs/cgroup` is mounted read-only: `mkdir /sys/fs/cgroup/<name>` fails with `EROFS` (`"Read-only file system"`).
- The jail installs `/bin/yolo-cglimit` (`yolo-cglimit --help`: `"yolo-cglimit — Run a command under cgroup v2 resource limits"`), which wraps one command (`yolo-cglimit [OPTIONS] -- COMMAND [ARGS...]`) through a host-side daemon and accepts four flags: `--cpu PCT` (`"CPU limit as percentage of ALL CPUs"`), `--memory LIMIT`, `--pids LIMIT`, and `--name NAME`.

**Why tillr does not use or depend on `yolo-cglimit` (`D-GTC6`, designer's).** `yolo-cglimit` can only cap an individual wrapped command; it cannot create per-class `cpu.weight` subgroups or delegate a writable cgroup v2 subtree to tillr. Its only processor flag, `--cpu PCT`, sets a hard `cpu.max` bandwidth ceiling (`PCT%` of all processors), which throttles a class even when the rest of the machine is idle — violating work conservation and `R-GTC3` — and it offers no `cpu.weight` share flag. Tillr never invokes `yolo-cglimit`: on this read-only cgroup mount tillr selects `nice` at startup, and on a plain Linux host (for example under `systemd-run --user --scope -p Delegate=cpu` or a delegated service unit) or in a container whose cgroup v2 subtree is mounted writable, tillr creates its four subgroups directly.

### 4.3 Startup detection and reporting in the loop log and `tillr drain why`

**How tillr detects a writable cgroup at startup (`D-GTC6`, designer's).** Once per loop invocation, during `Loop.Run` initialization in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) (with the cgroup mount root defaulting to `/sys/fs/cgroup` and overridable on `LoopConfig` for tests), `InitProcessPriority` probes cgroup v2 support as follows:

1. **Locate `own_cg` and check `cpu` controller availability.** Read `/proc/self/cgroup` for the unified hierarchy line (`0::<relpath>`), resolve `own_cg = filepath.Join(cgroupMount, relpath)`, and read `own_cg/cgroup.controllers` to confirm the `cpu` controller is listed.
2. **Compute a store-scoped subgroup root name.** Hash `processStoreIdentity(storePath)` to `tillr-<hash12>` so two loops serving different stores on one host never share or overwrite each other's subgroups, while an in-place `tillr drain restart` on the same store reuses `<cg_root>` across live detached gates.
3. **Create `<cg_root>` while satisfying cgroup v2's no-internal-processes rule.** In Linux cgroup v2, a non-root cgroup cannot enable `+cpu` in `cgroup.subtree_control` while any process sits directly in its `cgroup.procs` (`EBUSY`). Two delegation layouts occur in practice, and the probe handles both in order:
   - **Already-delegated parent (`own_cg != cgroupMount`):** When `parent_cg = filepath.Dir(own_cg)` already lists `cpu` in `parent_cg/cgroup.subtree_control` — true both when a container entrypoint moved initial processes to a leaf cgroup such as `/sys/fs/cgroup/init` and when an earlier `tillr-drain` incarnation before an in-place restart already moved itself into `.../supervisor` — `tillr-drain` ensures `<cg_root> = filepath.Join(parent_cg, "tillr-"+hash12)` (`0o755`). Because `<cg_root>` itself holds zero processes, writing `+cpu` to `<cg_root>/cgroup.subtree_control` succeeds without moving anything in `own_cg`.
   - **Self-delegated `own_cg` (such as `systemd-run --user --scope -p Delegate=cpu`):** Otherwise, `tillr-drain` creates `own_cg/supervisor` (`0o755`), moves every PID currently in `own_cg/cgroup.procs` into `own_cg/supervisor/cgroup.procs` so `own_cg` holds no internal processes, writes `+cpu` to `own_cg/cgroup.subtree_control`, creates `<cg_root> = filepath.Join(own_cg, "tillr-"+hash12)`, and writes `+cpu` to `<cg_root>/cgroup.subtree_control`.
4. **Create and weight the four class subgroups.** Ensure `<cg_root>/repair`, `<cg_root>/gate`, `<cg_root>/proof`, and `<cg_root>/worker` (`0o755`), write `400`, `200`, `100`, and `50` to their `cpu.weight` files, and open each subgroup directory with `O_RDONLY | O_DIRECTORY | O_CLOEXEC`, keeping the four file descriptors open for the loop's lifetime.
5. **Select `nice` when cgroups cannot be created.** If cgroup v2 is absent, `cpu` is missing from `cgroup.controllers`, or creating directories, vacating `own_cg/cgroup.procs`, or writing `+cpu` fails (`ENOENT`, `EROFS`, `EACCES`, `EPERM`, or `EBUSY` — for instance `EROFS` on the read-only `/sys/fs/cgroup` mount in the operating jail, or `EBUSY` when an unmovable root-owned PID 1 sits directly in `own_cg`), `tillr-drain` removes any empty directory the probe created, reads `base = unix.Getpriority(unix.PRIO_PROCESS, 0)`, and selects `nice` for the loop's lifetime, recording the failing path and kernel error.
6. **No runtime fallback once selected (`R-GTC1`).** The selection between `cgroup` and `nice` happens once at loop startup. If `cgroup` was selected at startup and spawning a child into its class subgroup descriptor later fails, the step fails loudly naming `<cg_root>/<class>` rather than dropping that process to `nice`.

**Reporting once in the loop's log and in `tillr drain why` (`D-GTC7`, designer's):**

- **Loop startup log (`l.cfg.Report` in `Loop.Run`):** Printed **once** at startup, never per pass or per child process:
  - When `cgroup` is in force: `loop: process priority: cgroup v2 (<cg_root>; cpu.weight repair=400 gate=200 proof=100 worker=50)`
  - When `nice` is in force: `loop: process priority: nice (repair=0 gate=5 proof=10 worker=15; cgroup unavailable: <path>: <error>)`
- **Session persistence and `tillr drain why` ([`tools/tillr-drain/drain/dispatches.go`](../../tools/tillr-drain/drain/dispatches.go), [`tools/tillr-drain/drain/pass_observation.go`](../../tools/tillr-drain/drain/pass_observation.go), [`tools/tillr-drain/drain/why.go`](../../tools/tillr-drain/drain/why.go), [`tools/tillr-drain/why.go`](../../tools/tillr-drain/why.go)):** `drain_sessions` registers a `process_priority TEXT` column written by `refreshSession` with the active JSON summary (`mechanism` `"cgroup"` or `"nice"`, `cgroup_root` when `"cgroup"`, `unavailable_reason` when `"nice"`, and `classes` mapping `repair`, `gate`, `proof`, and `worker` to their active `cpu.weight` or `nice` integers). `tillr drain why` reads `process_priority` from the live `drain_sessions` row inside its existing read-only store query — never probing `/sys/fs/cgroup` from the CLI caller and omitting the field when no loop is live — exposing `process_priority` on `WhyAnswer` (`--json`) and printing one `priority: ...` status line alongside `lanes:` in human-readable output.

### 4.4 Where each class's processes are placed, and what happens to worker test runs

**Placement at each launch site (`D-GTC8`, designer's):**

- **Loop supervisor and short control commands (`tillr-drain loop`, `Land`, `tillr-item`, `git` plumbing):** Remain in `own_cg` (or `own_cg/supervisor`) at `base` niceness, so scheduling passes and short lock-holding squash transactions are never delayed behind compilation or test suites.
- **Agent and command runs (`StartRun` in [`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go), invoking `tillr-run exec`):** Placed in `repair` when `IsTrunkFixer(req.ItemID)` is true, and in `worker` for every other run (`build`, `tricky`, `edit`, `test-write`, `test-run`, `plan`, `design`, `docs`, `classify`, `scout`, `review`, `verify`, `triage`). Under `cgroup`, `StartRun` sets `UseCgroupFD: true, CgroupFD: classFD` alongside `Setpgid: true` on `cmd.SysProcAttr` (`CLONE_INTO_CGROUP`, placing the process into `<cg_root>/<class>` inside `clone3` before `execve`). Under `nice`, `StartRun` launches `tillr-run` via `tillr-drain`'s own pre-exec niceness helper (`--tillr-exec-nice <target> -- <argv...>`, calling `unix.Setpriority(unix.PRIO_PROCESS, 0, target)` before `syscall.Exec`), avoiding any `/usr/bin/nice` `PATH` dependency and eliminating the post-`Start()` race window. The unconditional `WorkerNiceness = 10` `Setpriority` call in [`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go) is removed so `tillr-run` inherits its caller's priority class instead of demoting `repair` workers to `10` or colliding with `worker` niceness `15` when `RLIMIT_NICE` is `0`.
- **Detached candidate gates, repair gates, fixer named checks, and base replays (`startOrAttachDetachedGate`, `runGateSupervisor`, and `Replay` in [`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go); `fixerCheck` in [`tools/tillr-drain/drain/fixer_gate.go`](../../tools/tillr-drain/drain/fixer_gate.go)):** Placed in `repair` when `IsTrunkFixer(candidate.ItemID) || IsRevertItem(candidate.ItemID)` (and for `fixerCheck`, including `JudgeRequired` called from `fixerCheck`), and in `gate` for every other candidate gate and candidate base-check replay (`ProcessSteps.Replay`). Under `cgroup`, `UseCgroupFD` places `--tillr-gate-supervisor` (and `fixerCheck` or `Replay` commands) into `<cg_root>/repair` or `<cg_root>/gate` at `clone3`. Under `nice`, `runGateSupervisor` sets `unix.Setpriority(unix.PRIO_PROCESS, 0, target)` on itself before `child.Start()`, and `fixerCheck` and `Replay` use the same pre-exec niceness helper.
- **After-merge trunk proof (`JudgeHeavy` in [`tools/tillr-drain/drain/heavy_steps.go`](../../tools/tillr-drain/drain/heavy_steps.go), `judgeHeavyGo` in [`tools/tillr-drain/drain/heavy_go.go`](../../tools/tillr-drain/drain/heavy_go.go), and non-fixer `JudgeRequired`):** Placed in `proof`. `runHeavySupervisorGroup` in [`tools/tillr-drain/heavyrun/heavy_supervisor.go`](../../tools/tillr-drain/heavyrun/heavy_supervisor.go) preserves existing `UseCgroupFD` and `CgroupFD` fields on `cmd.SysProcAttr` when setting `Setpgid: true` rather than overwriting the struct literal, and `--tillr-heavy-supervisor` applies the `proof` (or `repair`, when called from `fixerCheck`) niceness before starting its supervised child when `nice` is in force.

**What happens to a worker's own test runs (`D-GTC8`).** On Linux, both cgroup v2 subgroup membership and process niceness are inherited automatically across every `fork`, `clone`, `setsid`, `setpgid`, and `execve`. Every subshell, sub-agent, Go compiler, and test command (`go test`, [`tools/go-test-shards.py`](../../tools/go-test-shards.py), `uvx vantage-check`) started by a lane worker — including shard guard processes that call `start_new_session=True` — remains inside `<cg_root>/worker` (or `<cg_root>/repair` for a trunk fixer worker) under `cgroup`, or at niceness `15` (or `0` for a trunk fixer worker) under `nice`. Neither `go-test-shards.py`, `go_affected.py`, nor lane command shims wrap individual test invocations, and the leftover `GO_TEST_SHARDS_CLASS` environment-variable plumbing in `tillr-drain` and `go_affected.py` is deleted.

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

**Ruling ([`OQ-GTC4`](#OQ-GTC4), closed 2026-11-25).** Schedule **A** stands (`"ok, so let's not batch. close it with a resaerch report recorded for the future I guess?"`): candidates keep being gated one at a time, each starting when it is ready, neither combined into one tree nor held to collect a batch. The schedule comparison and the 2026-11-25 operating-session measurement are recorded in [the gate batching research record](../research/gate-batching.md).

## 6. Checked against the landing principles

| Principle | How the design meets it |
| :--- | :--- |
| [A refusal must be a verdict](./landing-principles.md#1-a-refusal-must-be-a-verdict); [a gate that cannot answer must say which](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which) | Once `cgroup` is selected at startup, a subgroup placement failure ends the step as an infrastructure error naming `<cg_root>/<class>`, never as a refusal of the candidate |
| [Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once) | Kept: batching is ruled out ([`OQ-GTC4`](#OQ-GTC4)), and each candidate is gated once on its own diff |
| [A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report) | Cross-run slot locks and memory-pressure waits are ruled out ([`OQ-GTC1`](#OQ-GTC1), [`OQ-GTC2`](#OQ-GTC2)), and `cpu.weight` and `nice` never park a process ([`OQ-GTC3`](#OQ-GTC3)) |
| [A principle cites the measurement that produced it](./landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it) | The 2026-11-24 and 2026-11-25 burst, swap, shard, and cgroup-mount measurements are stated at the top and in [Section 3.3](#33-one-gates-shard-ceiling) and [Section 4.2](#42-operating-jail-cgroup-facts-and-yolo-cglimit) |
| [Convict a run on what it did](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran) | A slow run is not a failed run (`R-GTC4`), and one candidate's red check never convicts a sibling |
| [Land the smallest functional change](./landing-principles.md#15-land-the-smallest-functional-change) | No load-sampling coordinator or memory poller is built; the remaining work is four small, independent build items ([Section 7](#7-follow-on-build-items)) |

## 7. Follow-on build items

The per-gate shard ceiling (`D-GTC4`), shim-free `git` fixtures (`D-GTC3`), unbatched per-candidate gates ([`OQ-GTC4`](#OQ-GTC4)), kernel-only memory enforcement ([`OQ-GTC2`](#OQ-GTC2)), and the removal of `ENFORCE_SLOT_LIMITS` and the file-lock slot pool from [`tools/go-test-shards.py`](../../tools/go-test-shards.py) (`7e896facad`, [`OQ-GTC1`](#OQ-GTC1)) are already in the tree. Four build items finish the design (`D-GTC9`, designer's; item 2 depends on item 1, while items 3 and 4 are independent):

1. **Startup priority probe, session persistence, startup log line, and `tillr drain why` reporting ([`OQ-GTC3`](#OQ-GTC3), `D-GTC5` to `D-GTC7`).** Implement `InitProcessPriority` in `tools/tillr-drain/drain/` (two-step cgroup v2 probe over a configurable mount root, creating `<cg_root>/{repair,gate,proof,worker}` with `cpu.weight` `400 / 200 / 100 / 50` or selecting `nice` targets `min(19, base + {0,5,10,15})` with the failure reason, and zero calls to `yolo-cglimit`), log the chosen mechanism once in `Loop.Run`, store `process_priority` on `drain_sessions`, surface it in `tillr drain why` (text and `--json`, omitted when no loop is live), and run `just pack-sync`.
2. **Four-class process placement across `tillr-drain` and `tillr-run` ([`OQ-GTC3`](#OQ-GTC3), `D-GTC5`, `D-GTC8`; depends on item 1).** Place `StartRun`, detached gates (`--tillr-gate-supervisor`), `fixerCheck`, `Replay`, and trunk proof commands (`JudgeHeavy`, `judgeHeavyGo`, `runHeavyLegacy`, `JudgeRequired`) into `repair`, `gate`, `proof`, or `worker` via `UseCgroupFD` / `CgroupFD` under `cgroup` (failing loudly on runtime placement error, preserving `SysProcAttr` in `runHeavySupervisorGroup`) or pre-exec `setpriority` under `nice`; remove `WorkerNiceness = 10` from [`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go) and `GO_TEST_SHARDS_CLASS` from `tillr-drain` and [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py); update [`tools/tillr-run/README.md`](../../tools/tillr-run/README.md) and [`tools/tillr-drain/README.md`](../../tools/tillr-drain/README.md); verify child inheritance on worker test commands; and run `just pack-sync`.
3. **Remove dead test-slot observer, watch rule, and stale `ENFORCE_SLOT_LIMITS` documentation after `7e896facad` ([`OQ-GTC1`](#OQ-GTC1), `D-GTC1`).** Delete `TestSlotsObserver` (`tools/tillr-drain/drain/test_slots*.go` and its `observeTestSlots` call in `pass.go`), delete [`config/tillr/watch/drain-test-slots-free-with-waiters.toml`](../../config/tillr/watch/drain-test-slots-free-with-waiters.toml) and [`tools/tillr-watch/test_slots_rule_test.go`](../../tools/tillr-watch/test_slots_rule_test.go), remove unused `GO_TEST_SHARDS_LOCK_DIR` test setup, update [`docs/reference/gate.md`](../reference/gate.md) so it states that the cross-run slot pool is deleted rather than pending [`OQ-GTC1`](#OQ-GTC1), and run `just pack-sync`.
4. **Single-test Go shard thread hygiene (`D-GTC3`).** Set `GOMAXPROCS=1` (and `-p 1` on `go test` invocations) for `-parallel 1` shards in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py) and [`tools/go-test-shards.py`](../../tools/go-test-shards.py), covered by unit tests in [`tests/go-affected.test.py`](../../tests/go-affected.test.py) and [`tests/go-test-shards.test.py`](../../tests/go-test-shards.test.py).

---

## Open Questions

None.

---

## Decision Ledger

Two tables, so the owner's words are never presented as the designer's choices.

### The owner's rulings

| ID | Ruling | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="R-GTC1"></a>`R-GTC1` | **No fallbacks.** "If something fails, you fix it. If it fails loudly, you fix it. You don't hide it with a fallback." See also [no automatic move to another model](./tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27). [`OQ-GTC3`](#OQ-GTC3) records the owner's single explicit exception (`cgroup` at startup, `nice` when cgroups are not available). | 2026-11-20 | [Section 1](#1-the-rulings-that-govern-this-design), [Section 4.1](#41-the-ruling-work-conservation-and-the-four-classes) | — |
| <a id="R-GTC2"></a>`R-GTC2` | **Lane count is judged by wasted work.** "the reason why we have fewer lanes is because we're wasting a lot of work by having Lots of conflicts, rework, red trunks, all that stuff ... the amount of time waiting was sort of a proxy for that, but not the primary thing I care about." | 2026-11-21 | [Section 1](#1-the-rulings-that-govern-this-design), [Section 4.1](#41-the-ruling-work-conservation-and-the-four-classes) | — |
| <a id="R-GTC3"></a>`R-GTC3` | **Every unexplained pause is a bug.** "There shouldn't be random pauses, and if there is, that's a bug that you fix." | 2026-11-20 | [Section 1](#1-the-rulings-that-govern-this-design) | — |
| <a id="R-GTC4"></a>`R-GTC4` | **Budgets are targets, stalls end checks, and load must not fail valid work.** "target budgets, watch for stalls, but don't let 'load is high' cause failures that would otherwise finish just fine." | 2026-11-17 | [Section 1](#1-the-rulings-that-govern-this-design) | The stall watchdog, in [the gate reference](../reference/gate.md#budgets-and-the-stall-watchdog) |
| <a id="R-GTC5"></a>`R-GTC5` | **The kernel schedules runnable threads; no limit is inferred from load.** "What does it mean for a core to be in use? I've never heard of any program scheduling cores and claiming that they're in use and allocating them. […] the whole point of a kernel is to schedule these things." "if that test assumes a fast machine, that test is broken. […] pick something responsible based on the number of processors, like two times the number of processors or something like that, and just do it. Unless we're hitting memory pressure, more of them is not an issue, and I can apply cgroups to workspaces and jails […] I don't want you guessing about external loads. […] But prioritizing within our own machinery, that's something that's potentially useful, if we have a reason to think that that is useful." | 2026-11-24 | [Section 1](#1-the-rulings-that-govern-this-design) | — |
| <a id="OQ-GTC1"></a>[`OQ-GTC1`](#OQ-GTC1) | **No machine-wide limit on concurrent test processes; delete the unused lock pool and `ENFORCE_SLOT_LIMITS` (question closed, option A).** "1. A 2. A. swap means nothing. 3. we should use cgroups if available, can fallback to nice, yolo-cgroups gives you full access I think. do your best." Each invocation bounds only its own fan-out, and cross-process slot locks are removed. | 2026-11-25 | [Section 1](#1-the-rulings-that-govern-this-design), [Section 3.1](#31-no-cross-run-test-process-limit), [Section 7](#7-follow-on-build-items) | Runner lock pool and `ENFORCE_SLOT_LIMITS` deleted (`7e896facad`); dead observer/watch cleanup in [Section 7](#7-follow-on-build-items) |
| <a id="OQ-GTC2"></a>[`OQ-GTC2`](#OQ-GTC2) | **Memory enforcement stays in the kernel and container limits, and swap in use is not a signal (question closed, option A).** "1. A 2. A. swap means nothing. 3. we should use cgroups if available, can fallback to nice, yolo-cgroups gives you full access I think. do your best." No `memory.pressure` polling or shard-pause code is added; swap occupancy is ignored (on 2026-11-25, 7 GB sat in swap with almost no swap traffic and nothing killed). | 2026-11-25 | [Section 1](#1-the-rulings-that-govern-this-design), [Section 3.2](#32-memory-enforcement-stays-in-the-kernel-and-swap-is-not-a-signal) | Yes (no polling code exists) |
| <a id="OQ-GTC3"></a>[`OQ-GTC3`](#OQ-GTC3) | **Weight processor share `repair > gate > proof > worker` with cgroup v2 `cpu.weight` subgroups when writable, and `nice` when not (question closed).** "1. A 2. A. swap means nothing. 3. we should use cgroups if available, can fallback to nice, yolo-cgroups gives you full access I think. do your best." Selecting `nice` when cgroup subgroups cannot be created is the owner's own ruling rather than a designer fallback under `R-GTC1`. | 2026-11-25 | [Section 1](#1-the-rulings-that-govern-this-design), [Section 4](#4-priority-among-tillrs-own-processes), [Section 7](#7-follow-on-build-items) | No ([Section 7](#7-follow-on-build-items), items 1 and 2) |
| <a id="OQ-GTC4"></a>[`OQ-GTC4`](#OQ-GTC4) | **Gates are not batched or queued in arrival order (question closed).** "ok, so let's not batch. close it with a resaerch report recorded for the future I guess?" Candidates keep being gated one at a time, each starting when it is ready; they are not combined into one tree and not held to collect a batch. Recorded for future revisiting in [the gate batching research record](../research/gate-batching.md). | 2026-11-25 | [Section 1](#1-the-rulings-that-govern-this-design), [Section 2.2](#22-why-gates-are-not-batched), [Section 5](#5-arrival-order-and-batching-against-running-ready-gates-at-once) | Yes (current loop behavior; [research record](../research/gate-batching.md)) |

### The designer's decisions

None of these is an owner ruling, and the owner may overturn any of them.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-GTC1"></a>`D-GTC1` | tillr never samples processor load, estimates outside load, or leases capacity. **Rejected:** admission by `/proc/stat` and `cpu.stat` headroom; throttling on load average; keeping `ENFORCE_SLOT_LIMITS = False` or `TestSlotsObserver` once [`OQ-GTC1`](#OQ-GTC1) is ruled | 2026-11-24 | [Section 1](#1-the-rulings-that-govern-this-design), [Section 3.1](#31-no-cross-run-test-process-limit) | No load sampler exists; `ENFORCE_SLOT_LIMITS` deleted (`7e896facad`); `TestSlotsObserver` cleanup in [Section 7](#7-follow-on-build-items) |
| <a id="D-GTC2"></a>`D-GTC2` | Memory pressure is the only dynamic resource where overcommitment can kill work in progress; processor saturation only stretches wall time. **Rejected:** acting on run-queue depth, processor use, swap occupancy, or stretched wall time | 2026-11-24 | [Section 3.2](#32-memory-enforcement-stays-in-the-kernel-and-swap-is-not-a-signal) | — |
| <a id="D-GTC3"></a>`D-GTC3` | Per-process hygiene is independent of any cross-run limit: `GOMAXPROCS=1` and `-p 1` on single-test shards, and real `git` with maintenance off in test fixtures | 2026-11-24 | [Section 3.1](#31-no-cross-run-test-process-limit) | `git` environment built; `GOMAXPROCS=1` in [Section 7](#7-follow-on-build-items) |
| <a id="D-GTC4"></a>`D-GTC4` | `MAX_SHARDS` in `tools/gate/go_affected.py` is `2 * os.cpu_count()`, and 12 when the count cannot be read. Nothing else in that file changes with it. **Rejected:** a fixed 12; inferring the count from load; changing `tools/go-test-shards.py` in the same step | 2026-11-24 | [Section 3.3](#33-one-gates-shard-ceiling) | Yes |
| <a id="D-GTC5"></a>`D-GTC5` | Class weights under `cgroup` are `repair=400`, `gate=200`, `proof=100`, `worker=50` (`cpu.weight`); under `nice`, targets are `min(19, base + {0, 5, 10, 15})` (`0, 5, 10, 15` at `base = 0`). Both are strictly work-conserving (`cpu.max` is never written). **Rejected:** negative nice values requiring `CAP_SYS_NICE` or elevated `RLIMIT_NICE`; `SCHED_IDLE` or `cpu.max` hard ceilings that throttle idle processors | 2026-11-25 | [Section 4.1](#41-the-ruling-work-conservation-and-the-four-classes) | No |
| <a id="D-GTC6"></a>`D-GTC6` | Probe cgroup v2 once at `Loop.Run` startup under a store-scoped `<cg_root>` (`tillr-<hash12>`), handling both an already-delegated parent (`filepath.Dir(own_cg)`) and a self-delegated `own_cg` vacated into `own_cg/supervisor` before enabling `+cpu`, selecting `nice` on missing/read-only/undelegated cgroup v2 with no runtime fallback thereafter, and never calling `yolo-cglimit`. **Rejected:** depending on `yolo-cglimit` (`--cpu PCT` sets hard `cpu.max` caps, not `cpu.weight` shares); re-probing per spawn; silently falling back to `nice` after `cgroup` succeeded at startup | 2026-11-25 | [Section 4.2](#42-operating-jail-cgroup-facts-and-yolo-cglimit), [Section 4.3](#43-startup-detection-and-reporting-in-the-loop-log-and-tillr-drain-why) | No |
| <a id="D-GTC7"></a>`D-GTC7` | Log the active priority mechanism once in `Loop.Run`, persist it in `drain_sessions.process_priority`, and report it read-only in `tillr drain why` (text `priority:` line and `--json` `process_priority`, omitted when no loop is live). **Rejected:** logging per tick or per child; probing `/sys/fs/cgroup` inside `tillr drain why` | 2026-11-25 | [Section 4.3](#43-startup-detection-and-reporting-in-the-loop-log-and-tillr-drain-why) | No |
| <a id="D-GTC8"></a>`D-GTC8` | Place `StartRun` (`repair` for `IsTrunkFixer`, else `worker`), detached gates / `fixerCheck` / `Replay` (`repair` for fixer or revert, else `gate`), and `JudgeHeavy` / `judgeHeavyGo` / `runHeavyLegacy` / non-fixer `JudgeRequired` (`proof`) at spawn via `SysProcAttr` `UseCgroupFD` (`CLONE_INTO_CGROUP`, preserved in `runHeavySupervisorGroup`) or pre-exec `setpriority`, removing `WorkerNiceness = 10` in `tillr-run` and `GO_TEST_SHARDS_CLASS` in `tillr-drain` and `go_affected.py`, while worker child test runs inherit priority from the kernel without wrappers. **Rejected:** post-`Start()` `Setpriority` races; wrapping `go test` or `go-test-shards.py` directly; keeping `WorkerNiceness = 10` in `tillr-run` | 2026-11-25 | [Section 4.4](#44-where-each-classs-processes-are-placed-and-what-happens-to-worker-test-runs) | No |
| <a id="D-GTC9"></a>`D-GTC9` | Ship the remaining work in the four self-contained build items of [Section 7](#7-follow-on-build-items). **Rejected:** bundling priority, dead slot-observer deletion, and `GOMAXPROCS=1` into one commit | 2026-11-25 | [Section 7](#7-follow-on-build-items) | No |
