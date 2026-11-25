#!/usr/bin/env python3
"""Judge the caller's change with the declared change checks and fast whole-tree checks."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

if str(_GATE_DIR := Path(__file__).resolve().parent) not in sys.path:
    sys.path.insert(0, str(_GATE_DIR))
from classify import ClassificationError, classify

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


def _go_build_command():
    go_build_decl = Path("tools/gate/checks/go-build.json")
    if not go_build_decl.is_file():
        return None
    go_build_script = Path("tools/gate/checks/go-build.py")
    if go_build_script.is_file():
        return [sys.executable, str(go_build_script)]
    repo_gate = Path("tools/gate/repo-gate")
    if repo_gate.is_file():
        return [str(repo_gate), "go-build"]
    return None


def go_build_needed(base, head):
    go_build_decl = Path("tools/gate/checks/go-build.json")
    if not _go_build_command():
        return False
    try:
        reads = json.loads(go_build_decl.read_text()).get("reads")
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

    The fast tier's go-build trims source paths (tools/gate/go_build.go), so a new
    checkout reuses the shared build cache. The first trimmed build on a machine, though,
    compiles the standard library and every module from cold -- 19.9 s measured on
    2026-11-16 -- past the check's 9 s ceiling, which would hold that one item as
    could-not-judge for a cold cache. So the cost is paid here, once per Go version per
    repository's git common dir, outside the fast tier's ceilings and before it runs.
    Its result is not a verdict -- the check itself judges the candidate next -- so a warm
    that could not run is tried again by the next gate, and one that ran (passing or
    failing) is recorded and not repeated. Its output goes to stderr, never stdout.
    """
    go_build_cmd = _go_build_command()
    if not go_build_cmd:
        return
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
    warmed = subprocess.run(go_build_cmd,
                            stdout=sys.stderr, env=check_environment())
    answer, = classify([{"check": "go-build", "source": "judging-program", "exit_code": warmed.returncode}])
    if answer["outcome"] in ("pass", "fail"):
        try:
            marker.parent.mkdir(parents=True, exist_ok=True)
            marker.write_text(version + "\n")
        except OSError as error:
            print(f"change-gate: could not record the warm cache ({error}); the next gate warms again",
                  file=sys.stderr)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    parser.add_argument("--runner", default="tillr-gate")
    parser.add_argument("--logs", required=True)
    args = parser.parse_args()
    try:
        # The tree checks judge the checkout. Do not certify a different head with
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
        return checked.returncode
    except (ClassificationError, OSError, subprocess.CalledProcessError) as error:
        print(f"change-gate: could not judge: {error}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    sys.exit(main())
