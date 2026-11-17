#!/usr/bin/env python3
"""Judge the caller's change, then the remaining repository checks."""
import argparse
from pathlib import Path
import subprocess
import sys

# What a fast tree check still running at its budget becomes. `keep` runs it on to its
# ceiling and keeps its verdict; `carry` stops it and defers it to the heavy tier. Carrying
# is suspended until a heavy runner exists to judge what is carried: with none, a carried
# check's judgment is dropped and the verdict stays pass (docs/design/gate-rework.md
# Section 6.5). `item-gate-heavy-runner` turns carrying back on here, and only here.
TREE_OVERRUN = "keep"


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
    warmed = subprocess.run([sys.executable, "tools/gate/checks/go-build.py"], stdout=sys.stderr)
    if warmed.returncode in (0, 1):
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
    # A trunk fixer's full gate: the remaining checks run untiered over the range since the
    # last green trunk, or over the whole tree, instead of the candidate's landing tier. The
    # change checks above still judge the candidate's own base..head.
    full = parser.add_mutually_exclusive_group()
    full.add_argument("--since", default=None, metavar="SHA")
    full.add_argument("--all", action="store_true")
    args = parser.parse_args()
    try:
        # The remaining program judges HEAD. Do not certify a different head with
        # a correct change verdict and an unrelated tree verdict.
        actual = subprocess.run(["git", "rev-parse", "HEAD"], check=True, capture_output=True, text=True)
        named = subprocess.run(["git", "rev-parse", args.head + "^{commit}"], check=True, capture_output=True, text=True)
        if actual.stdout != named.stdout:
            print("change-gate: could not judge: --head is not checked out", file=sys.stderr)
            return 4
        warm_trimmed_go_cache()
        checked = subprocess.run([args.runner, "checks", "--fast-tree", "--base", args.base, "--head", args.head,
                                  "--tree-overrun", TREE_OVERRUN,
                                  "--worktree", ".", "--checks", "tools/gate/checks", "--logs", args.logs])
        if checked.returncode:
            return checked.returncode
        scope = (["--all"] if args.all else ["--since", args.since] if args.since
                 else ["--since", args.base, "--tier", "landing"])
        remaining = subprocess.run([sys.executable, "tools/fork-check.py", *scope, "--lock-timeout", "30"])
        # The old program's usage/no-verdict exit is 2; RunGate reads 4 as
        # could-not-judge. A missing judgment must never become a rejected change.
        return remaining.returncode if remaining.returncode in (0, 1) else 4
    except (OSError, subprocess.CalledProcessError) as error:
        print(f"change-gate: could not judge: {error}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    sys.exit(main())
