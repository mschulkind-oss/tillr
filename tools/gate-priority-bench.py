#!/usr/bin/env python3
"""Measure what seven lanes do to a landing gate, before and after the lane priority.

A landing gate is a parallel command (`just done` runs the plan's checks
concurrently), and so is a lane: the agent, its subagents, and the builds and test
suites they start. When both run at the same scheduling priority they split the
machine, and the gate's wall clock grows with the number of lanes beside it — the
2026-10-27 incident was nineteen minutes for a gate that costs about four on an
idle machine.

This harness measures that shape with the two workloads the drain and the landing
pass actually schedule:

- **the gate**: G CPU-bound workers started under `sh -c`, one per concurrent check
  a plan would run.
- **a lane**: seven process trees, each started exactly the way the drain starts
  one — `sh -c 'exec 3<>lease; nice -n N sh -c "<command>"'` — with W CPU-bound
  workers inside, standing in for an agent plus its subagents and build workers.

Each worker counts arithmetic iterations until a deadline, so every run lasts the
same fixed window, and the metric is the **share of the machine the gate held**
(CPU-seconds per wall-second, from `wait4`'s accounting of the whole tree). A
gate's work is fixed, so its share is its speed: a gate holding 0.10 of a 12-CPU
machine takes three times as long as one holding 0.30. Share is the metric because
it survives a machine somebody else is also using — an outside load lowers every
condition's share alike, and it averages over the whole window instead of
inheriting one moment of it. Each worker also reports its own counting window, so
a slow fork or interpreter start is not charged to work that never ran.

Three conditions per run: **idle** (the gate alone), **before** (lanes at niceness
0, the drain's own priority, which is what every lane got before the mechanism),
and **after** (lanes at niceness 19, the drain's default now). Every scenario is
measured idle-then-before-then-after, adjacent in time, and the whole set repeats:
the comparison the mechanism is about is read from adjacent runs, because this
machine's outside load moves faster than a minute.

Two sweeps are on by default, because the claim has two halves: how the gate fares
as lanes are ADDED (the defect was that seven lanes cost far more than one), and
how it fares as each lane's process tree gets busier (a lane mid-build is the shape
that hurt). `--lane-cpus` and `--lane-session` measure the two mechanisms this one
is weighed against: reserved capacity as a CPU set the lanes are confined to, and
the same niceness in a lane that has a session (and therefore an autogroup) of its
own.

The CPU-bound workload is the worst case on purpose: a gate that also waits on
disk gains from the priority exactly as much and loses nothing to it, so a gate
holding its idle throughput here holds it on real work. The numbers are comparable
between runs on one machine; they are not a claim about another machine.

    tools/gate-priority-bench.py                       # the default sweep
    tools/gate-priority-bench.py --lanes 7 --repeats 3 --json out.json

Scratch files (the worker script and the lane leases) go under
$YOLO_DURABLE_DIR/gate-priority-bench, or beside this checkout when that is unset,
and are removed afterwards.
"""

import argparse
import json
import os
import shlex
import shutil
import statistics
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

# One unit of counting work per loop pass; the deadline is checked between passes.
PASS = 10_000


def work_dir() -> Path:
    base = os.environ.get("YOLO_DURABLE_DIR")
    root = Path(base) if base else ROOT / ".yolo" / "durable"
    return root / "gate-priority-bench"


def write_worker(path: Path) -> Path:
    """The worker: count arithmetic passes until a deadline, then print the count."""
    path.write_text(
        "import sys, time\n"
        "deadline = float(sys.argv[1])\n"
        "x = 0\n"
        "count = 0\n"
        f"step = {PASS}\n"
        "started = time.monotonic()\n"
        "while time.monotonic() < deadline:\n"
        "    for _ in range(step):\n"
        "        x += 1\n"
        "    count += step\n"
        # The worker's OWN counting window, so a slow fork or interpreter start under
        # a busy machine is not charged to the workload it never ran.
        "print(count, time.monotonic() - started)\n"
    )
    return path


def tree_command(workers: int, deadline: float, worker: Path) -> str:
    """A process tree of `workers` workers, all waited for, all printing a count.

    This is the shape of both workloads: a gate's plan runs its checks concurrently
    and waits, and a lane's agent forks subagents and build workers and waits.
    """
    return (
        "i=0; "
        f"while [ \"$i\" -lt {workers} ]; do "
        f"{shlex.quote(sys.executable)} {shlex.quote(str(worker))} {deadline:.3f} & "
        "i=$((i+1)); done; wait"
    )


def lane_command(workers: int, deadline: float, worker: Path) -> str:
    return "sh -c " + shlex.quote(tree_command(workers, deadline, worker))


def run_tree(command: str, cwd: Path) -> dict:
    """Run one shell command tree and answer its throughput and total CPU time.

    `os.wait4` is what makes the CPU time the whole tree's: the kernel reports the
    terminated child's usage with its waited-for descendants folded in, so a gate
    that ran twelve checks in parallel answers with twelve checks' worth of CPU.
    """
    started = time.monotonic()
    process = subprocess.Popen(["sh", "-c", command], cwd=str(cwd),
                               stdout=subprocess.PIPE, text=True)
    output = process.stdout.read() if process.stdout else ""
    _, status, usage = os.wait4(process.pid, 0)
    process.returncode = os.waitstatus_to_exitcode(status)
    window = time.monotonic() - started
    counts = parse_worker_output(output)
    return {
        "window": window,
        "count": sum(one[0] for one in counts),
        "elapsed": sum(one[1] for one in counts) / len(counts) if counts else 0.0,
        "cpu": usage.ru_utime + usage.ru_stime,
        "status": process.returncode,
    }


def parse_worker_output(output: str) -> list:
    """The (count, elapsed) pairs a tree's workers printed, one pair per worker."""
    pairs = []
    for line in output.splitlines():
        parts = line.split()
        if len(parts) == 2 and parts[0].isdigit():
            try:
                pairs.append((int(parts[0]), float(parts[1])))
            except ValueError:
                continue
    return pairs


def start_lane(lease: Path, nice: int, command: str, cwd: Path, cpus=None,
               session: str = "group") -> subprocess.Popen:
    """Start one lane the way the drain does, and answer the process tree.

    The lease is opened by the shell in front of the lane and inherited by
    everything under it — the same descriptor discipline the drain uses — and the
    niceness goes on the inner shell so it is in force before the lane's first
    instruction.
    """
    lease.parent.mkdir(parents=True, exist_ok=True)
    if lease.exists():
        lease.unlink()
    os.mkfifo(lease, 0o600)
    shell = f"exec 3<>{shlex.quote(str(lease))}; nice -n {nice} {command}"

    def child_setup():
        # A lane leads its own PROCESS GROUP, exactly as the drain starts it, and stays
        # in the harness's SESSION. That distinction is load-bearing on a kernel with
        # sched_autogroup enabled: a new session is a new autogroup, and the scheduler
        # then shares the CPU between groups before it looks at any task's niceness.
        # A lane in its own session carries a weight of its own and the niceness
        # inside it buys the gate almost nothing — an artifact of the harness, not of
        # the drain, which sets a process group and never a session. `--lane-session`
        # exists to measure that difference, and its two values are the two orders a
        # shell could do it in.
        if session == "session":
            os.setsid()
        elif session == "session-niced":
            os.nice(nice)
            os.setsid()
        else:
            os.setpgrp()
        if cpus:
            # Reserved capacity, as a CPU set the lane may run on: the gate keeps
            # every CPU, so the CPUs the lanes are not on are the gate's alone.
            os.sched_setaffinity(0, set(cpus))

    return subprocess.Popen(["sh", "-c", shell], cwd=str(cwd),
                            stdout=subprocess.PIPE, text=True,
                            preexec_fn=child_setup)


def reap_lane(process: subprocess.Popen) -> dict:
    output = process.stdout.read() if process.stdout else ""
    _, status, usage = os.wait4(process.pid, 0)
    process.returncode = os.waitstatus_to_exitcode(status)
    counts = parse_worker_output(output)
    return {
        "count": sum(one[0] for one in counts),
        "elapsed": sum(one[1] for one in counts) / len(counts) if counts else 0.0,
        "cpu": usage.ru_utime + usage.ru_stime,
    }


def load_average() -> str:
    try:
        return ", ".join(f"{one:.2f}" for one in os.getloadavg())
    except OSError:
        return "unknown"


def condition(args, work: Path, cwd: Path, nice, lane_count: int, lane_workers: int) -> dict:
    """One measured run: the lanes (if any) start first, then the gate."""
    lanes = []
    if nice is not None:
        # The lanes outlive the gate: a lane that stopped mid-window would measure a
        # machine the gate had to itself for the rest of it.
        lane_deadline = time.monotonic() + args.window + 1.0
        for index in range(lane_count):
            lanes.append(start_lane(cwd / "leases" / f"lane-{index}.lease", nice,
                                    lane_command(lane_workers, lane_deadline, work), cwd,
                                    cpus=list(range(args.lane_cpus)) if args.lane_cpus else None,
                                    session=args.lane_session))
        # Let the lanes be running before the gate starts: the question is what they
        # do to a gate beside them, not what they do to one that starts first.
        time.sleep(0.25)

    gate = run_tree(tree_command(args.gate_workers, time.monotonic() + args.window, work), cwd)
    lane_runs = [reap_lane(process) for process in lanes]
    return {
        "gate": gate,
        "lane_count": sum(one["count"] for one in lane_runs),
        "lane_cpu": sum(one["cpu"] for one in lane_runs),
        "load": load_average(),
    }


def median(values):
    values = [one for one in values if one == one]
    return statistics.median(values) if values else float("nan")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--lanes", type=int, nargs="+", default=[1, 2, 4, 7],
                        help="lane process trees, swept (default: 1 2 4 7)")
    parser.add_argument("--gate-workers", type=int, default=os.cpu_count() or 4,
                        help="parallel gate checks (default: the machine's CPUs)")
    parser.add_argument("--lane-workers", type=int, nargs="+", default=[1, 4, 8],
                        help="workers inside each lane tree, swept (default: 1 4 8)")
    parser.add_argument("--lane-cpus", type=int, default=0,
                        help="confine every lane tree to the lowest N CPUs (default: 0, none); the "
                             "gate keeps every CPU, so the rest is reserved capacity. This is the "
                             "alternative mechanism, measured here rather than shipped")
    parser.add_argument("--lane-session", choices=["group", "session", "session-niced"],
                        default="group",
                        help="how a lane is separated from the harness: its own process group in "
                             "the harness's session (group, what the drain does), a session of its "
                             "own (session), or a session created AFTER the niceness is applied "
                             "(session-niced). The last two measure what autogrouping does to a "
                             "lowered lane")
    parser.add_argument("--fixed-lanes", type=int, default=7,
                        help="lane count the worker sweep uses (default: 7)")
    parser.add_argument("--fixed-lane-workers", type=int, default=4,
                        help="workers the lane-count sweep uses (default: 4)")
    parser.add_argument("--repeats", type=int, default=3, help="runs per condition (default: 3)")
    parser.add_argument("--window", type=float, default=4.0,
                        help="seconds each measured run lasts (default: 4)")
    parser.add_argument("--json", metavar="PATH", help="also write every run's numbers here")
    args = parser.parse_args()

    cwd = work_dir()
    shutil.rmtree(cwd, ignore_errors=True)
    cwd.mkdir(parents=True, exist_ok=True)
    worker = write_worker(cwd / "worker.py")
    cpus = os.cpu_count() or 1
    print(f"gate-priority-bench: {cpus} CPUs, load average {load_average()}")
    print(f"  gate: {args.gate_workers} checks; {args.window:g}s window; "
          f"lanes at niceness 0 (before) and 19 (after)"
          + (f", confined to {args.lane_cpus} CPUs" if args.lane_cpus else ""))

    # Two sweeps, because the claim has two halves: how the gate fares as lanes are
    # ADDED (the defect was that seven lanes cost seven times what one did), and how
    # it fares as each lane's process tree gets busier (a lane mid-build is the shape
    # that hurt). The idle run is measured first in every repeat, so a machine whose
    # outside load drifts between repeats is read against the idle beside it.
    pairs = []
    for lane_count in args.lanes:
        pairs.append((lane_count, args.fixed_lane_workers))
    for lane_workers in args.lane_workers:
        pairs.append((args.fixed_lanes, lane_workers))
    scenarios = [("idle", 0, 0)]
    for lane_count, lane_workers in dict.fromkeys(pairs):
        scenarios.append((f"{lane_count} lanes, {lane_workers} workers each",
                          lane_count, lane_workers))

    # Each scenario's three conditions are measured ADJACENT in time (idle, then
    # lanes at the gate's priority, then lanes lowered), and the whole set is
    # repeated. That is the design the machine forces: this one carries other
    # people's work at a load that swings by 3x in a minute, so a comparison across
    # a minute is a comparison of two different machines. Adjacent runs share their
    # neighbours, and the medians over the repeats are read from that.
    rows = []
    try:
        for repeat in range(args.repeats):
            for name, lane_count, lane_workers in scenarios:
                for condition_name, nice in (("idle", None), ("before", 0), ("after", 19)):
                    measured = condition(args, worker, cwd, nice, lane_count, lane_workers)
                    rows.append({"repeat": repeat, "condition": condition_name,
                                 "scenario": name, "lanes": lane_count,
                                 "lane_workers": lane_workers, **measured})
    finally:
        shutil.rmtree(cwd, ignore_errors=True)

    # The gate's own speed, in passes per second per check, and the share of the
    # machine it held. The share is the scheduling fact; the speed is what the
    # landing pass waits on. Both are reported because the second also carries each
    # run's process startup, which the share does not.
    def rate(one):
        """Passes per second per worker, measured inside each worker's own window."""
        elapsed = one["gate"]["elapsed"]
        return (one["gate"]["count"] / elapsed / args.gate_workers) if elapsed else float("nan")

    def share(one, key="gate"):
        return one[key]["cpu"] / (one["gate"]["window"] * cpus)

    idle_rate = median([rate(one) for one in rows if one["condition"] == "idle"])
    idle_share = median([share(one) for one in rows if one["condition"] == "idle"])
    print()
    print(f"{'run':<4} {'condition':<7} {'scenario':<22} {'gate checks/s':>14} "
          f"{'vs idle':>8} {'gate share':>10} {'lane share':>10}  load")
    for index, one in enumerate(rows):
        lane_share = (one["lane_cpu"] / (one["gate"]["window"] * cpus)) if one["lane_cpu"] else 0.0
        print(f"{index:<4} {one['condition']:<7} {one['scenario']:<22} "
              f"{rate(one):>14.0f} {rate(one) / idle_rate:>7.2f}x "
              f"{share(one):>10.3f} {lane_share:>10.3f}  {one['load']}")

    print()
    print(f"summary (median of {args.repeats} repeats; the idle gate held {idle_share:.3f} of "
          f"the machine and ran at {idle_rate:.0f} checks/s):")
    print(f"  {'scenario':<26} {'before':>18} {'after':>18} {'after/before':>13}")
    for name, _, _ in scenarios:
        if name == "idle":
            continue
        cells = []
        for condition_name in ("before", "after"):
            selected = [one for one in rows
                        if one["condition"] == condition_name and one["scenario"] == name]
            shares = [share(one) for one in selected]
            cell = median(shares) if shares else float("nan")
            cells.append((cell, cell / idle_share if idle_share else float("nan")))
        pairs = []
        for repeat in range(args.repeats):
            before = [one for one in rows if one["repeat"] == repeat
                      and one["condition"] == "before" and one["scenario"] == name]
            after = [one for one in rows if one["repeat"] == repeat
                     and one["condition"] == "after" and one["scenario"] == name]
            if before and after:
                pairs.append(share(after[0]) / share(before[0]))
        pair_text = f"{median(pairs):.2f}x" if pairs else "-"
        print(f"  {name:<26} {cells[0][0]:>10.3f} {cells[0][1]:>5.2f}x "
              f"{cells[1][0]:>10.3f} {cells[1][1]:>5.2f}x {pair_text:>13}")
    print()
    print("  each cell is the share of the machine the gate held and that share as a fraction")
    print("  of the idle gate's: a gate's work is fixed, so its share IS its speed. after/before")
    print("  is the median of the per-repeat pairs at that scenario")
    print()
    print("  spread (min..max of the per-repeat shares, so a reader can see the machine's noise):")
    for name, _, _ in scenarios:
        for condition_name in ("before", "after"):
            shares = [share(one) for one in rows
                      if one["condition"] == condition_name and one["scenario"] == name]
            if not shares:
                continue
            print(f"    {name:<26} {condition_name:<7} {min(shares):>9.3f}..{max(shares):<9.3f}")
        idle_shares = [share(one) for one in rows if one["condition"] == "idle"]
        if name == "idle" and idle_shares:
            print(f"    {'idle':<26} {'-':<7} {min(idle_shares):>9.3f}..{max(idle_shares):<9.3f}")
    if args.json:
        Path(args.json).write_text(json.dumps(
            {"cpus": cpus, "args": vars(args), "idle_checks_per_second": idle_rate, "runs": rows},
            indent=2, default=str))
        print(f"\nwrote {args.json}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
