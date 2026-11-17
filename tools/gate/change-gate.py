#!/usr/bin/env python3
"""Judge the caller's change, then the remaining repository checks."""
import argparse
import subprocess
import sys


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
        checked = subprocess.run([args.runner, "checks", "--fast-tree", "--base", args.base, "--head", args.head,
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
