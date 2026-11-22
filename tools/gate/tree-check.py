#!/usr/bin/env python3
"""Run one fast tree declaration through the landing gate's declaration runner."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

if str(_GATE_DIR := Path(__file__).resolve().parent) not in sys.path:
    sys.path.insert(0, str(_GATE_DIR))
from classify import classify


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
            checks = json.loads(done.stdout)["payload"]["checks"]
            if (not isinstance(checks, list) or len(checks) != 1
                    or not isinstance(checks[0], dict) or checks[0].get("check_id") != args.only):
                raise ValueError("runner did not judge the requested check")
            result = checks[0]["result"]
            if not isinstance(result, str):
                raise ValueError("runner did not report a result word")
            context = {"check": args.only, "commit": args.head}
            exit_answer, word_answer = classify([
                dict(context, source="judging-program", exit_code=done.returncode),
                dict(context, source="result-word", word=result),
            ])
            exit_kind = (exit_answer["outcome"], exit_answer["detail"])
            word_kind = (word_answer["outcome"], word_answer["detail"])
            verdict = {("pass", "passed"): 0, ("fail", "failed"): 1, ("unknown", "stalled"): 6}.get(exit_kind)
            # The runner's wire uses these exact words, even though the general
            # result-word protocol also accepts synonyms from other producers.
            if result not in ("pass", "fail", "stalled") or exit_kind != word_kind or verdict is None:
                raise ValueError(f"runner exit {done.returncode} does not match {result} as a verdict")
            print(f"fast-tree: {args.only}: {result}")
            if checks[0].get("log_ref") and Path(checks[0]["log_ref"]).is_file():
                print(Path(checks[0]["log_ref"]).read_text())
            return verdict
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        print(f"fast-tree: {args.only}: could not judge: {error}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    sys.exit(main())
