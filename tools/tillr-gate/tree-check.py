#!/usr/bin/env python3
"""Run one fast tree declaration through the landing gate's declaration runner."""
import argparse
import json
import os
from pathlib import Path
import posixpath
import subprocess
import sys
import tempfile
import tomllib

if str(_GATE_DIR := Path(__file__).resolve().parent) not in sys.path:
    sys.path.insert(0, str(_GATE_DIR))
from classify import classify

DEFAULT_CHECKS_DIR = "tools/gate/checks"


def _clean_checks_dir(raw):
    if not isinstance(raw, str) or any(c in raw for c in "*?\\") or "//" in raw:
        return None
    clean = raw[:-1] if raw.endswith("/") else raw
    if not clean or clean in (".", "..") or clean.endswith("/"):
        return None
    if clean == ".git" or clean.startswith(".git/") or clean.startswith("/"):
        return None
    parts = clean.split("/")
    if any(p in ("", ".", "..") for p in parts):
        return None
    if posixpath.normpath(clean) != clean:
        return None
    return clean


def resolve_checks_dir(explicit):
    if explicit is not None:
        return explicit
    path = Path(".tillr.toml")
    if not path.exists():
        return DEFAULT_CHECKS_DIR
    try:
        with path.open("rb") as f:
            data = tomllib.load(f)
    except (OSError, tomllib.TOMLDecodeError) as error:
        raise ValueError(f".tillr.toml: expected valid TOML: {error}") from error
    if not isinstance(data, dict) or "gate" not in data:
        return DEFAULT_CHECKS_DIR
    gate = data["gate"]
    if not isinstance(gate, dict):
        raise ValueError(".tillr.toml: gate: expected a table")
    unknown = sorted(k for k in gate if k != "checks_dir")
    if unknown:
        raise ValueError(f".tillr.toml: gate.{unknown[0]}: expected a known gate key (checks_dir)")
    if "checks_dir" not in gate:
        return DEFAULT_CHECKS_DIR
    clean = _clean_checks_dir(gate["checks_dir"])
    if clean is None:
        raise ValueError(".tillr.toml: gate.checks_dir: expected a clean repository-relative subdirectory path outside .git")
    return clean


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--only", required=True)
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", default="HEAD")
    parser.add_argument("--runner", default="tillr-gate")
    parser.add_argument("--checks", default=None)
    args = parser.parse_args()
    try:
        checks_dir = resolve_checks_dir(args.checks)
        declarations = [json.loads(p.read_text()) for p in Path(checks_dir).glob("*.json")]
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
