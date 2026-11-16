#!/usr/bin/env python3
"""Regression for a recorded baseline older than the seed (2026-11-15)."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[3]
SCRATCH = Path(os.environ.get("YOLO_DURABLE_DIR", ROOT / "tools/gate/.scratch"))
SCRATCH.mkdir(parents=True, exist_ok=True)


def command(repo, *args, env=None):
    return subprocess.run(args, cwd=repo, env=env, text=True, capture_output=True)


with tempfile.TemporaryDirectory(dir=SCRATCH) as tmp:
    repo = Path(tmp)
    assert command(repo, "git", "init", "-q").returncode == 0
    (repo / "README.md").write_text("# Fixture\n")
    assert command(repo, "git", "add", ".").returncode == 0
    assert command(repo, "git", "commit", "-qm", "chore: before seed").returncode == 0
    old = command(repo, "git", "rev-parse", "HEAD").stdout.strip()
    (repo / "config/tillr").mkdir(parents=True)
    seed = {"items": [{"item_id": "item-one"}, {"item_id": "item-two"}]}
    (repo / "config/tillr/seed-items.json").write_text(json.dumps(seed))
    (repo / "tools").mkdir()
    shutil.copy(ROOT / "tools/fork-check.py", repo / "tools/fork-check.py")
    assert command(repo, "git", "add", ".").returncode == 0
    assert command(repo, "git", "commit", "-qm", "chore: introduce seed").returncode == 0
    base = command(repo, "git", "rev-parse", "HEAD").stdout.strip()
    (repo / "README.md").write_text("# Fixture\n\nEdited.\n")
    assert command(repo, "git", "add", ".").returncode == 0
    assert command(repo, "git", "commit", "-qm", "docs: edit one file").returncode == 0
    state = repo / ".git/yolo-fork-check/record.jsonl"
    state.parent.mkdir()
    state.write_text(json.dumps({"kind": "run", "baseline": {"commit": old,
        "tree": command(repo, "git", "rev-parse", old + "^{tree}").stdout.strip(),
        "at": "2026-11-02T00:00:00Z"}}) + "\n")
    explained = command(repo, "python3", "tools/fork-check.py", "--explain")
    assert explained.returncode == 0, explained.stdout + explained.stderr
    if "would run  seed-freeze" in explained.stdout:
        # Capture the fault before asserting the corrected behavior. The preflight
        # executes the very plan -- no record writes and no landing lock.
        ran = command(repo, "python3", "tools/fork-check.py", "--preflight", "--cells",
                      "baseline", "--report", str(repo / "report.json"))
        report = (repo / "report.json").read_text()
        assert "net-new" in report and "item-one" in report and "item-two" in report, report
        print("reproduced: --explain plans seed-freeze; preflight reports both entries net-new", flush=True)
    assert "would run  seed-freeze" not in explained.stdout, \
        "the legacy program still judges a change against a remembered baseline"
    print("pass: legacy planning cannot judge seed entries against a remembered baseline")
