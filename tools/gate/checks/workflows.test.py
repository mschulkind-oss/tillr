#!/usr/bin/env python3
"""A malformed added workflow fails the real workflow change check."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[3]
SCRATCH = Path(os.environ.get("YOLO_DURABLE_DIR", ROOT / "tools/gate/.scratch"))
SCRATCH.mkdir(parents=True, exist_ok=True)


def command(repo, *args):
    return subprocess.run(args, cwd=repo, text=True, capture_output=True)


with tempfile.TemporaryDirectory(dir=SCRATCH, prefix="workflow-gate-") as tmp:
    repo = Path(tmp)
    assert command(repo, "git", "init", "-q").returncode == 0
    (repo / "README").write_text("fixture\n")
    assert command(repo, "git", "add", ".").returncode == 0
    assert command(repo, "git", "commit", "-qm", "chore: fixture").returncode == 0
    base = command(repo, "git", "rev-parse", "HEAD").stdout.strip()
    path = repo / "config/tillr/workflows/check@v1.json"
    path.parent.mkdir(parents=True)
    path.write_text((ROOT / "tools/tillr-drain/drain/testdata/workflow-check/cycle/check@v1.json").read_text())
    assert command(repo, "git", "add", ".").returncode == 0
    assert command(repo, "git", "commit", "-qm", "feat: cyclic workflow").returncode == 0
    head = command(repo, "git", "rev-parse", "HEAD").stdout.strip()
    result = command(repo, "python3", str(ROOT / "tools/gate/checks/workflows.py"), base, head)
    assert result.returncode == 1, result.stdout + result.stderr
    assert str(path) in result.stdout and "workflow-cycle" in result.stdout, result.stdout
    registry = json.loads((ROOT / "tools/gate/checks/workflows.json").read_text())
    assert "config/tillr/workflows/" in registry["reads"] and registry["scope"] == "change"
    print("pass: added cycle fails workflow gate and names config/tillr/workflows/check@v1.json")
