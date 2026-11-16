#!/usr/bin/env python3
"""Measure the gate's heavy-admission cap on a fixed representative batch.

Board item 404, the gate-latency design's D7 and F-9. `jobs` bounds the whole check
pool, but it counts CHECKS, not load: a check spawns a subprocess tree that
parallelizes internally (`go test -parallel 48` is the documented example), so twelve
checks on twelve cores is nested parallelism, not a load bound. The scheduler now
admits HEAVY checks (above `CHEAP_CHECK_SECONDS`) under a separate, smaller cap.
This harness selects that cap by measurement rather than by guess.

The batch is FIXED and representative of the heavy phase's shape: long checks with
nested parallelism and independent write sets. It is deliberately not the fork-quality
npm steps, because those write the shared `.forks/candidate` checkouts and a lane must
not corrupt a landing gate's candidate; the batch uses the real heavy checks that are
safe to run beside one another:

  * `pi-forks` -- the driver's own suite, 288 git-fixture cases (the check F-7
    indicted as contention-sensitive);
  * `go-tool` for `tools/tillr-store` -- a Go build and unit suite at `-parallel 48`;
  * `go-tool` for `tools/pi-profiler` -- the same shape at a smaller size.

Two copies of each make six heavy checks, so caps 1 through 6 are distinguishable.
Two cheap checks run first, under the `cheap_first` barrier, to show that the cheap
budget is the whole pool and does not move with the heavy cap.

Each cap is one real run of the scheduler (`run_checks`) with the real executor
(`execute_attempt`), so the durations are measured, not modelled. The box is shared,
so `/proc/loadavg` is recorded before and after every run and the numbers are read
with that in view.

    tools/gate-heavy-cap-sweep.py
    tools/gate-heavy-cap-sweep.py --caps 1,2,3,4,6,12 --jobs 12 --out sweep.json

The output is a JSON document (one entry per cap) and a table on stdout. The deployed
cap is the one the curve selects: the smallest cap whose wall time is within a small
tolerance of the best measured wall time, because a smaller cap is the safer admission
bound when the wall times are equal.
"""

import argparse
import importlib.util
import json
import os
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "tools" / "fork-check.py"


def load_gate():
    spec = importlib.util.spec_from_file_location("fork_check_sweep", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def load_average() -> str:
    try:
        return Path("/proc/loadavg").read_text().split()[0]
    except OSError:
        return "?"


def build_batch(gate):
    """The fixed representative batch: six heavy checks and two cheap ones."""
    # `-count=1` disables the Go test cache, so every cap pays for the suite rather than
    # reading a cached verdict. The gate's own `go-tool` step does not pass it, so a
    # repeat gate run may read the cache; the sweep measures the scheduler's admission on
    # real work, which is the question the cap answers.
    go_tool = ("cd {directory} && CGO_ENABLED=0 go build ./... && "
               "CGO_ENABLED=0 go test -count=1 -short -parallel 48 -timeout 3600s ./...")
    heavy = []
    for copy in range(2):
        heavy.append(gate.Check(
            "pi-forks", f"pi-forks copy {copy}", ["bash", "tests/pi-forks.test.sh"],
            "the driver's own cases", 600, cost=20.0))
        heavy.append(gate.Check(
            "go-tool", f"tools/tillr-store copy {copy}",
            ["sh", "-c", go_tool.format(directory="tools/tillr-store")],
            "a Go tool builds and its unit tests pass", 3600, cost=120.0))
        heavy.append(gate.Check(
            "go-tool", f"tools/pi-profiler copy {copy}",
            ["sh", "-c", go_tool.format(directory="tools/pi-profiler")],
            "a Go tool builds and its unit tests pass", 3600, cost=60.0))
    cheap = [
        gate.Check("structural", "cheap: the tracked tree lists", ["sh", "-c", "git ls-files >/dev/null"],
                   "a cheap integrity step", 60, cost=0.0),
        gate.Check("file-size", "cheap: the size budget", ["sh", "-c", "git ls-files >/dev/null"],
                   "a cheap integrity step", 60, cost=0.0),
    ]
    return cheap + heavy


def run_cap(gate, cap, jobs):
    checks = build_batch(gate)
    before = load_average()
    started = time.monotonic()
    wall, peak, halted = gate.run_checks(
        checks, jobs,
        lambda index: gate.execute_attempt(ROOT, checks[index], None),
        cheap_first=True, heavy_jobs=cap)
    elapsed = time.monotonic() - started
    after = load_average()
    heavy = [check for check in checks if check.cost > gate.CHEAP_CHECK_SECONDS]
    cheap = [check for check in checks if check.cost <= gate.CHEAP_CHECK_SECONDS]
    return {
        "cap": cap,
        "jobs": jobs,
        "wall_seconds": round(elapsed, 2),
        "check_wall_seconds": round(wall, 2),
        "peak_concurrency": peak,
        "halted": halted,
        "load_before": before,
        "load_after": after,
        "cheap_phase_seconds": round(max((check.seconds for check in cheap), default=0.0), 2),
        "heavy_seconds": round(sum(check.seconds for check in heavy), 2),
        "heavy_wait_seconds": round(sum(check.waited.get("heavy", 0.0) for check in checks), 2),
        "checks": [
            {"kind": check.kind, "label": check.label, "rc": check.rc,
             "seconds": round(check.seconds, 2),
             "heavy": check.cost > gate.CHEAP_CHECK_SECONDS,
             "waited": {cause: round(seconds, 2) for cause, seconds in check.waited.items()}}
            for check in checks
        ],
    }


def select_cap(results, tolerance=0.05):
    """The smallest cap whose wall time is within `tolerance` of the best measured one.

    A smaller cap is the safer admission bound when the wall times are equal: it bounds
    nested parallelism more tightly for the same measured cost. The tolerance is a
    fraction of the best wall time, so a cap that is 5% slower than the best is not
    selected merely because it is smaller.
    """
    best = min(result["wall_seconds"] for result in results)
    limit = best * (1.0 + tolerance)
    for result in sorted(results, key=lambda one: one["cap"]):
        if result["wall_seconds"] <= limit:
            return result["cap"]
    return min(results, key=lambda one: one["wall_seconds"])["cap"]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--caps", default="1,2,3,4,6,12",
                        help="comma-separated heavy-admission caps to measure")
    parser.add_argument("--jobs", type=int, default=12,
                        help="the whole-pool bound (default: 12, this box's core count)")
    parser.add_argument("--out", default="",
                        help="write the JSON results here (default: "
                             "$YOLO_DURABLE_DIR/gate-heavy-cap-sweep.json)")
    options = parser.parse_args(argv)
    caps = [int(part) for part in options.caps.split(",") if part.strip()]
    gate = load_gate()
    results = []
    for cap in caps:
        print(f"fork-check sweep: cap {cap} ...", file=sys.stderr, flush=True)
        results.append(run_cap(gate, cap, options.jobs))
    selected = select_cap(results)
    document = {"generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                "jobs": options.jobs, "caps": caps, "selected_cap": selected,
                "results": results}
    out = Path(options.out) if options.out else (
        Path(os.environ.get("YOLO_DURABLE_DIR", ROOT / ".yolo" / "durable"))
        / "gate-heavy-cap-sweep.json")
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(document, indent=2) + "\n")
    print(f"fork-check sweep: wrote {out}", file=sys.stderr)
    print(f"{'cap':>4} {'wall':>8} {'check wall':>11} {'peak':>5} {'cheap':>7} "
          f"{'heavy sum':>10} {'heavy wait':>11} {'load':>10}")
    for result in results:
        print(f"{result['cap']:>4} {result['wall_seconds']:>8.1f} "
              f"{result['check_wall_seconds']:>11.1f} {result['peak_concurrency']:>5} "
              f"{result['cheap_phase_seconds']:>7.1f} {result['heavy_seconds']:>10.1f} "
              f"{result['heavy_wait_seconds']:>11.1f} "
              f"{result['load_before'] + '->' + result['load_after']:>10}")
    print(f"fork-check sweep: selected cap {selected}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
