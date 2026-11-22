#!/usr/bin/env python3
"""Judge the caller's change, then the remaining repository checks."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

# Budgets are targets. Working checks keep their own verdict; only a sampled stall
# ends one early. Carrying at a budget would discard its judgment.
TREE_OVERRUN = "keep"
GATE_HANDOFFS = ("TILLR_ORDINARY_GATE_TIMING", "TILLR_FAST_TREE_TIMING",
                 "TILLR_GATE_SPANS_OUT", "TILLR_FAST_VERDICT_OUT")


def check_environment():
    return {name: value for name, value in os.environ.items() if name not in GATE_HANDOFFS}


def supports_change_scope(runner):
    # The adapter is read from the candidate; the runner on PATH is from trunk.
    # Inspect its help, without trying the judge and retrying a possible real verdict.
    help_result = subprocess.run([runner, "checks", "--help"], capture_output=True, env=check_environment())
    return bool(re.search(rb"(?m)^[ \t]*--?scope-to-change(?:[ \t]|$)",
                          help_result.stdout + b"\n" + (help_result.stderr or b"")))


def go_build_needed(base, head):
    try:
        reads = json.loads(Path("tools/gate/checks/go-build.json").read_text()).get("reads")
        if not isinstance(reads, list) or not reads or not all(isinstance(p, str) and p for p in reads):
            return True
        reads = [os.path.normpath(p) for p in reads]
        paths = subprocess.run(["git", "diff", "--name-only", "--no-renames", "-z", base, head, "--"],
                               check=True, capture_output=True).stdout
    except (OSError, ValueError, AttributeError, subprocess.CalledProcessError):
        return True
    return any(read == "." or path == read or path.startswith(read + "/")
               for path in os.fsdecode(paths).split("\0") if path for read in reads)


def warm_trimmed_go_cache():
    """Run the go-build check once per Go version, unbounded, before the fast tier.

    The fast tier's go-build trims source paths (tools/gate/checks/go-build.py), so a new
    checkout reuses the shared build cache. The first trimmed build on a machine, though,
    compiles the standard library and every module from cold -- 19.9 s measured on
    2026-11-16 -- past the check's 9 s ceiling, which would hold that one item as
    could-not-judge for a cold cache. So the cost is paid here, once per Go version per
    repository's git common dir, outside the fast tier's ceilings and before it runs.
    Its result is not a verdict -- the check itself judges the candidate next -- so a warm
    that could not run is tried again by the next gate, and one that ran (passing or
    failing) is recorded and not repeated. Its output goes to stderr, never stdout.
    """
    try:
        version = subprocess.run(["go", "env", "GOVERSION"], capture_output=True, text=True).stdout.strip()
        common = subprocess.run(["git", "rev-parse", "--path-format=absolute", "--git-common-dir"],
                                check=True, capture_output=True, text=True).stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return
    if not version:
        return
    marker = Path(common) / "tillr-gate" / f"go-build-warm-{version}"
    if marker.exists():
        return
    print(f"change-gate: warming the trimmed Go build cache for {version}, once, outside the fast tier's ceilings",
          file=sys.stderr)
    warmed = subprocess.run([sys.executable, "tools/gate/checks/go-build.py"],
                            stdout=sys.stderr, env=check_environment())
    if warmed.returncode in (0, 1):
        try:
            marker.parent.mkdir(parents=True, exist_ok=True)
            marker.write_text(version + "\n")
        except OSError as error:
            print(f"change-gate: could not record the warm cache ({error}); the next gate warms again",
                  file=sys.stderr)


def preserve_fast_timing(path, first):
    # The remaining judge owns the stall evidence at this path. Preserve its
    # top-level checks, with the earlier declarations run as an observation.
    try:
        fast = json.loads(first)["payload"]
        remaining = json.loads(Path(path).read_bytes())
        if (remaining["payload"].get("result") != "stalled" or not isinstance(fast.get("wall_ms"), int)
                or not isinstance(fast.get("checks"), list)):
            return
        remaining["payload"]["fast_timing"] = {key: fast[key] for key in ("wall_ms", "checks")}
        # Never truncate a valid stall handoff if writing its timing fails.
        with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=Path(path).parent,
                                         prefix=".fast-timing-", delete=False) as temporary:
            target = Path(temporary.name)
            try:
                json.dump(remaining, temporary)
                temporary.write("\n")
                temporary.close()
                target.replace(path)
            finally:
                target.unlink(missing_ok=True)
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as error:
        # An observation failure must not replace the remaining judge's exit.
        print(f"change-gate: could not preserve fast timings: {error}", file=sys.stderr)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    parser.add_argument("--runner", default="tillr-gate")
    parser.add_argument("--logs", required=True)
    args = parser.parse_args()
    try:
        # The remaining program judges HEAD. Do not certify a different head with
        # a correct change verdict and an unrelated tree verdict.
        actual = subprocess.run(["git", "rev-parse", "HEAD"], check=True, capture_output=True, text=True)
        named = subprocess.run(["git", "rev-parse", args.head + "^{commit}"], check=True, capture_output=True, text=True)
        if actual.stdout != named.stdout:
            print("change-gate: could not judge: --head is not checked out", file=sys.stderr)
            return 4
        scoped = supports_change_scope(args.runner)
        if not scoped or go_build_needed(args.base, args.head):
            warm_trimmed_go_cache()
        checks_cmd = [args.runner, "checks", "--fast-tree"]
        if scoped:
            checks_cmd.append("--scope-to-change")
        path = os.environ.get("TILLR_FAST_VERDICT_OUT")
        # The stall and span handoffs are also used without timing. Only the
        # drain's explicit ordinary-invocation opt-in enables this observation.
        measure_landing = bool(os.environ.get("TILLR_ORDINARY_GATE_TIMING") == "1"
                               and path and os.environ.get("TILLR_GATE_SPANS_OUT"))
        checks_env = dict(check_environment(), TILLR_FAST_TREE_TIMING="1" if measure_landing else "")
        checked = subprocess.run([*checks_cmd, "--base", args.base, "--head", args.head,
                                  "--tree-overrun", TREE_OVERRUN,
                                  "--worktree", ".", "--checks", "tools/gate/checks", "--logs", args.logs],
                                 stdout=subprocess.PIPE, env=checks_env)
        # Preserve per-check timings and stall evidence beside the wrapper verdict.
        # The drain hands over this path; the outer gate supplies one for direct runs.
        if path:
            Path(path).write_bytes(checked.stdout)
        sys.stdout.buffer.write(checked.stdout)
        sys.stdout.buffer.flush()
        if checked.returncode:
            return checked.returncode
        remaining = subprocess.run([sys.executable, "tools/fork-check.py",
                                    "--since", args.base, "--tier", "landing"])
        if measure_landing and remaining.returncode == 6 and checked.stdout.strip():
            preserve_fast_timing(path, checked.stdout)
        # The old program's usage/no-verdict exit is 2; RunGate reads 4 as
        # could-not-judge. A missing judgment must never become a rejected change.
        return remaining.returncode if remaining.returncode in (0, 1, 6) else 4
    except (OSError, subprocess.CalledProcessError) as error:
        print(f"change-gate: could not judge: {error}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    sys.exit(main())
