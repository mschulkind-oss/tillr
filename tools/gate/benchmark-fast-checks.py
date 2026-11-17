#!/usr/bin/env python3
"""Manual five-run timing audit, not a test and never an elapsed-time assertion.

Run alone: python3 tools/gate/benchmark-fast-checks.py --baseline COMMIT
Copies only the two checks' source inputs to a private fixture repository under
YOLO_DURABLE_DIR. It never writes the real repository's result store or refs, and
leaves Go's standard shared build cache (or an owner's override) untouched.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import statistics
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]


def command(args, cwd, check=True):
    result = subprocess.run(args, cwd=cwd, capture_output=True, text=True)
    if check and result.returncode:
        raise RuntimeError(f"{args}: exit {result.returncode}\n{result.stdout}{result.stderr}")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline", required=True)
    args = parser.parse_args()
    destination = Path(os.environ["YOLO_DURABLE_DIR"]) / ("fast-check-audit-" + str(time.time_ns()))
    fixture = destination / "fixture"
    fixture.mkdir(parents=True)
    checks = ROOT / "tools/gate/checks"
    sys.path.insert(0, str(checks))
    import change_common as common
    spec = importlib.util.spec_from_file_location("go_build_audit", checks / "go-build.py")
    build = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(build)
    entries = common.tree_entries(ROOT)
    roadmap = "docs/tillr/roadmap.md"
    text = (ROOT / roadmap).read_text()
    named, _ = common.named_inputs(text, roadmap, entries)
    modules = build.GO_TOOL_DIRS
    # The real roadmap's named link targets and each built module's tracked source,
    # not a repository snapshot. Directory targets need no contents to resolve.
    selected = {roadmap, ".vantage.toml"}
    selected.update(path.relative_to(ROOT).as_posix() for path in checks.iterdir()
                    if path.suffix in (".py", ".json"))
    selected.update(p for p in named if entries[p][1] == "blob")
    for module in modules:
        selected.update(path.relative_to(ROOT).as_posix() for path in (ROOT / module).rglob("*.go"))
    selected.update(p for p in entries if entries[p][1] == "blob" and
                    (p.startswith("tools/gate/checks/") or any(p.startswith(m + "/") for m in modules)))
    for path in sorted(selected):
        source = ROOT / path
        if source.is_file():
            target = fixture / path
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, target)
    for path in named:
        if entries[path][1] == "tree":
            (fixture / path).mkdir(parents=True, exist_ok=True)
            # Git does not track an empty directory. A marker binds its existence,
            # so it is not mistaken for an untracked link destination by memory.
            (fixture / path / ".audit-directory").write_text("directory link target\n")
    baseline = destination / "baseline"
    baseline.mkdir()
    for name in ("documents.py", "documents.json", "change_common.py", "go-build.py"):
        result = command(["git", "show", f"{args.baseline}:tools/gate/checks/{name}"], ROOT)
        (baseline / name).write_text(result.stdout)
    # The audit compares code at the baseline to the candidate; baseline imports must
    # not resolve into the candidate's Python directory.
    command(["git", "init", "-q"], fixture)
    def commit():
        command(["git", "add", "."], fixture)
        command(["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                 "commit", "-qm", "fixture"], fixture)
        return command(["git", "rev-parse", "HEAD"], fixture).stdout.strip()
    base = commit()
    go_path = fixture / "tools/pi-profiler/cmd/pi-profiler/main.go"
    go_text = go_path.read_text()
    results = {}
    for stage in ("before", "after"):
        directory = baseline if stage == "before" else fixture / "tools/gate/checks"
        documents = [sys.executable, str(directory / "documents.py"), base, "HEAD"]
        go = [sys.executable, str(directory / "go-build.py")]
        # Prime remembered section/module passes at the unmodified inputs. Every
        # measured run has new bytes; this is not timing an exact-repeat tree hit.
        if stage == "after":
            (fixture / roadmap).write_text(text)
            go_path.write_text(go_text)
            commit()
            command(documents, fixture)
            command(go, fixture)
        for label, argv in (("documents", documents), ("go-build", go)):
            values = []
            for number in range(5):
                if label == "documents":
                    (fixture / roadmap).write_text(text + f"\nBudget audit {stage} {number}.\n")
                else:
                    (fixture / roadmap).write_text(text)
                    go_path.write_text(go_text + f"\nvar gateBudgetAudit = {number + (10 if stage == 'after' else 0)}\n")
                commit()
                started = time.monotonic()
                result = command(argv, fixture)
                elapsed = round((time.monotonic() - started) * 1000)
                values.append(elapsed)
                (destination / f"{stage}-{label}-{number}.log").write_text(result.stdout + result.stderr)
                print(stage, label, number, elapsed, "ms", flush=True)
            results[f"{stage}-{label}"] = {"runs_ms": values, "median_ms": statistics.median(values)}
            print(stage, label, "median", statistics.median(values), "ms", flush=True)
    (destination / "results.json").write_text(json.dumps(results, indent=2) + "\n")
    print(json.dumps(results, indent=2))
    print("Retained audit:", destination)


if __name__ == "__main__":
    main()
