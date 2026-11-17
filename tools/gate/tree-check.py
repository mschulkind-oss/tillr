#!/usr/bin/env python3
"""Run one fast tree declaration through the landing gate's declaration runner."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--only", required=True)
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", default="HEAD")
    parser.add_argument("--runner", default="tillr-gate")
    args = parser.parse_args()
    try:
        declarations = [json.loads(p.read_text()) for p in Path("tools/gate/checks").glob("*.json")]
        matches = [d for d in declarations if d.get("id") == args.only
                   and d.get("scope") == "tree" and d.get("tier") == "fast"]
        if len(matches) != 1:
            raise ValueError(f"expected one fast tree declaration for {args.only}, found {len(matches)}")
        # The checks face admits all change declarations too. Give it just this tree
        # declaration, not a second implementation of its command or progress observer.
        scratch = os.environ.get("YOLO_DURABLE_DIR")
        if not scratch:
            common = subprocess.run(["git", "rev-parse", "--git-common-dir"],
                                    capture_output=True, text=True, check=True).stdout.strip()
            scratch = str(Path(common) / "fast-tree")
        scratch = Path(scratch).resolve()
        scratch.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix="fast-tree-", dir=scratch) as directory:
            directory = Path(directory)
            checks = directory / "checks"
            checks.mkdir()
            (checks / "check.json").write_text(json.dumps(matches[0]))
            done = subprocess.run([args.runner, "checks", "--fast-tree", "--base", args.base,
                                   "--head", args.head, "--tree-overrun", "keep", "--worktree", ".",
                                   "--checks", str(checks), "--logs", str(directory / "logs")],
                                  capture_output=True, text=True)
            sys.stdout.write(done.stdout)
            sys.stderr.write(done.stderr)
            if done.returncode not in (0, 1, 6):
                return 4
            checks = json.loads(done.stdout)["payload"]["checks"]
            if len(checks) != 1 or checks[0]["check_id"] != args.only:
                raise ValueError("runner did not judge the requested check")
            result = checks[0]["result"]
            if (done.returncode, result) not in ((0, "pass"), (1, "fail"), (6, "stalled")):
                raise ValueError(f"runner exit {done.returncode} does not match {result}")
            print(f"fast-tree: {args.only}: {result}")
            if checks[0].get("log_ref") and Path(checks[0]["log_ref"]).is_file():
                print(Path(checks[0]["log_ref"]).read_text())
            return done.returncode
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        print(f"fast-tree: {args.only}: could not judge: {error}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    sys.exit(main())
