#!/usr/bin/env python3
"""Judge the committed tree with every declared check: `just done`.

The checks are the declarations in tools/gate/checks/, one JSON file each, and the
judge is tillr-gate, built from this checkout. Two passes run in order:

  1. The change checks and the fast whole-tree checks, over BASE..HEAD.
  2. The heavy whole-tree checks (the Go suites and the script suites), on HEAD.

BASE is `--since`, else refs/tillr/green when HEAD descends from it, else the
upstream branch when HEAD descends from it, else HEAD's first parent. A heavy
check whose declared inputs are unchanged since it last passed is remembered and
not run again. This program writes no verdict record and moves no ref.

Exit codes: 0 every check passed, 1 a check failed, 4 the gate could not judge.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys


def git(*args, check=True):
    done = subprocess.run(["git", *args], capture_output=True, text=True)
    if check and done.returncode:
        raise RuntimeError(done.stderr.strip() or "git " + " ".join(args) + " failed")
    return done.stdout.strip()


def default_base():
    for candidate in ("refs/tillr/green", "@{upstream}"):
        sha = subprocess.run(["git", "rev-parse", "--verify", "--quiet", candidate + "^{commit}"],
                             capture_output=True, text=True)
        if sha.returncode == 0 and subprocess.run(
                ["git", "merge-base", "--is-ancestor", sha.stdout.strip(), "HEAD"]).returncode == 0:
            return sha.stdout.strip(), candidate
    parent = subprocess.run(["git", "rev-parse", "--verify", "--quiet", "HEAD^"],
                            capture_output=True, text=True)
    if parent.returncode == 0:
        return parent.stdout.strip(), "HEAD's first parent"
    return git("rev-parse", "HEAD"), "HEAD (a root commit)"


def build_runner(common):
    target = Path(common) / "tillr-gate" / "bin" / "tillr-gate"
    target.parent.mkdir(parents=True, exist_ok=True)
    built = subprocess.run(["go", "build", "-o", str(target), "."], cwd="tools/tillr-gate",
                           env=dict(os.environ, CGO_ENABLED="0"))
    if built.returncode:
        raise RuntimeError("tillr-gate did not build")
    return str(target)


def judge(runner, title, flags, base, head, logs):
    print(f"done: {title}", flush=True)
    ran = subprocess.run([runner, "checks", *flags, "--base", base, "--head", head, "--worktree", ".",
                          "--checks", "tools/gate/checks", "--logs", str(logs)],
                         stdout=subprocess.PIPE, text=True,
                         # Suites that call tillr-gate find the one this run judges with.
                         env=dict(os.environ, PATH=str(Path(runner).resolve().parent) + os.pathsep
                                  + os.environ.get("PATH", "")))
    try:
        checks = json.loads(ran.stdout)["payload"]["checks"]
    except (ValueError, KeyError, TypeError):
        print(f"done: {title}: could not judge: the gate printed no verdict (exit {ran.returncode})")
        return 4
    failed = 0
    for check in checks:
        result = str(check.get("result"))
        passed = result == "pass" or result.startswith("skipped") or result.startswith("remembered")
        note = ""
        if not passed:
            failed += 1
            note = "  log: " + str(check.get("log_ref") or check.get("log_path") or "none")
        print(f"  {'ok  ' if passed else 'FAIL'} {check.get('check_id')}: {result}{note}")
    if ran.returncode not in (0, 1):
        print(f"done: {title}: could not judge (exit {ran.returncode})")
        return 4
    return 1 if failed or ran.returncode else 0


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--since", metavar="BASE", help="judge the change checks over BASE..HEAD")
    parser.add_argument("--fast", action="store_true", help="run the first pass only")
    parser.add_argument("--only", metavar="ID", help="run exactly this heavy check")
    parser.add_argument("--runner", metavar="PATH", help="use this tillr-gate instead of building one")
    args = parser.parse_args()
    try:
        os.chdir(git("rev-parse", "--show-toplevel"))
        if git("status", "--porcelain"):
            print("done: could not judge: the working tree has uncommitted changes; "
                  "the checks judge HEAD, so commit first", file=sys.stderr)
            return 4
        head = git("rev-parse", "HEAD")
        if args.since:
            base, source = git("rev-parse", "--verify", args.since + "^{commit}"), "--since"
        else:
            base, source = default_base()
        common = git("rev-parse", "--path-format=absolute", "--git-common-dir")
        logs = Path(common) / "tillr-gate" / "done-logs" / head
        runner = args.runner or build_runner(common)
    except (OSError, RuntimeError) as error:
        print(f"done: could not judge: {error}", file=sys.stderr)
        return 4
    worst = 0
    if not args.only:
        worst = judge(runner, f"change and fast checks over {base[:12]}..{head[:12]} (base: {source})",
                      ["--fast-tree"], base, head, logs / "fast")
    if not args.fast and worst != 4:
        flags = ["--heavy-tree"] + (["--only", args.only] if args.only else [])
        worst = max(worst, judge(runner, f"heavy checks on {head[:12]}", flags, head, head, logs / "heavy"))
    print("done: " + {0: "PASSED", 1: "FAILED"}.get(worst, "COULD NOT JUDGE"))
    return worst


if __name__ == "__main__":
    sys.exit(main())
