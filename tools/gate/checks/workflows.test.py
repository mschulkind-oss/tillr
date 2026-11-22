#!/usr/bin/env python3
"""Tests for the workflows gate check (workflow check, tillr-watch lint, recorded-verdict)."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[3]
SCRATCH = Path(os.environ.get("YOLO_DURABLE_DIR", ROOT / "tools/gate/.scratch"))
SCRATCH.mkdir(parents=True, exist_ok=True)
CHECK_SCRIPT = ROOT / "tools/gate/checks/workflows.py"


def command(repo, *args):
    return subprocess.run(args, cwd=repo, text=True, capture_output=True)


def commit_all(repo, msg):
    assert command(repo, "git", "add", ".").returncode == 0
    res = command(repo, "git", "commit", "-qm", msg)
    assert res.returncode == 0, res.stdout + res.stderr
    return command(repo, "git", "rev-parse", "HEAD").stdout.strip()


def workflow_repo(repo):
    assert command(repo, "git", "init", "-q").returncode == 0
    (repo / "README.md").write_text("fixture\n")

    tools_dir = repo / "tools"
    tools_dir.mkdir(parents=True, exist_ok=True)
    for name in ("tillr-watch", "tillr-drain", "tillr-item", "tillr-run", "tillr-land"):
        (tools_dir / name).symlink_to(ROOT / "tools" / name)

    # Materialize tools/tillr with symlinked Go sources and a real copy of
    # examples/recorded-verdict so tests can mutate the example safely.
    tillr_dir = tools_dir / "tillr"
    tillr_dir.mkdir(parents=True, exist_ok=True)
    for entry in (ROOT / "tools/tillr").iterdir():
        if entry.name == "examples":
            examples_dir = tillr_dir / "examples"
            examples_dir.mkdir(parents=True, exist_ok=True)
            shutil.copytree(entry / "recorded-verdict", examples_dir / "recorded-verdict")
        else:
            (tillr_dir / entry.name).symlink_to(entry)

    config_dir = repo / "config/tillr"
    config_dir.mkdir(parents=True, exist_ok=True)
    shutil.copytree(ROOT / "config/tillr/workflows", config_dir / "workflows")
    shutil.copytree(ROOT / "config/tillr/watch", config_dir / "watch")
    shutil.copy2(ROOT / "config/tillr/backend-policy.json", config_dir / "backend-policy.json")
    shutil.copy2(ROOT / "config/tillr/commands.json", config_dir / "commands.json")

    return commit_all(repo, "chore: fixture")


def run_check(repo, base, head):
    return command(repo, "python3", str(CHECK_SCRIPT), base, head)


def test_registry():
    registry = json.loads((ROOT / "tools/gate/checks/workflows.json").read_text())
    assert registry["scope"] == "change"
    assert registry["timeout_ms"] == 20000
    for required in (
        "definition.json",
        "config/tillr/",
        "config/tillr/workflows/",
        "config/tillr/watch/",
        "tools/tillr-watch/",
    ):
        assert required in registry["reads"], f"missing {required} in workflows.json reads"


def test_unrelated_skips():
    with tempfile.TemporaryDirectory(dir=SCRATCH, prefix="workflow-gate-skip-") as tmp:
        repo = Path(tmp)
        base = workflow_repo(repo)
        (repo / "README.md").write_text("unrelated change\n")
        head = commit_all(repo, "docs: unrelated")
        res = run_check(repo, base, head)
        assert res.returncode == 0, res.stdout + res.stderr


def test_clean_change_passes_all_steps():
    with tempfile.TemporaryDirectory(dir=SCRATCH, prefix="workflow-gate-clean-") as tmp:
        repo = Path(tmp)
        base = workflow_repo(repo)
        rule_path = repo / "config/tillr/watch/test-clean.toml"
        rule_path.write_text(
            '[[rule]]\n'
            'id = "test-clean-rule"\n'
            'scope = "core"\n'
            'severity = "warning"\n'
            '\n'
            '[rule.reads]\n'
            'events = ["drain.refusal"]\n'
            '\n'
            '[rule.when]\n'
            'pattern = "count_in_window"\n'
            'match = "event.kind == \\"drain.refusal\\""\n'
            'group_by = "item_id"\n'
            'threshold = 5\n'
            'window = "10m"\n'
            '\n'
            '[rule.delivery]\n'
            'receiver = "foreground"\n'
            '\n'
            '[rule.guidance]\n'
            'summary = "Test clean rule for {{subject}}"\n'
            'check = ["Check {{rule_id}}"]\n'
            'do = ["Inspect {{subject}}"]\n'
        )
        head = commit_all(repo, "feat: valid watch rule")
        res = run_check(repo, base, head)
        assert res.returncode == 0, res.stdout + res.stderr


def test_invalid_workflow_fails_step1():
    with tempfile.TemporaryDirectory(dir=SCRATCH, prefix="workflow-gate-cycle-") as tmp:
        repo = Path(tmp)
        base = workflow_repo(repo)
        path = repo / "config/tillr/workflows/check@v1.json"
        path.write_text((ROOT / "tools/tillr-drain/drain/testdata/workflow-check/cycle/check@v1.json").read_text())
        head = commit_all(repo, "feat: cyclic workflow")
        res = run_check(repo, base, head)
        assert res.returncode == 1, res.stdout + res.stderr
        assert str(path) in res.stdout and "workflow-cycle" in res.stdout, res.stdout + res.stderr
        print("pass: added cycle fails workflow gate and names config/tillr/workflows/check@v1.json")


def test_invalid_watch_rule_fails_step2():
    with tempfile.TemporaryDirectory(dir=SCRATCH, prefix="workflow-gate-watch-") as tmp:
        repo = Path(tmp)
        base = workflow_repo(repo)
        bad_rule = repo / "config/tillr/watch/bad-rule.toml"
        bad_rule.write_text(
            '[[rule]]\n'
            'id = "bad-rule"\n'
            'scope = "core"\n'
            'severity = "warning"\n'
            '\n'
            '[rule.reads]\n'
            'events = ["nonexistent.event.kind"]\n'
            '\n'
            '[rule.when]\n'
            'pattern = "count_in_window"\n'
            'match = "event.kind == \\"nonexistent.event.kind\\""\n'
            'group_by = "item_id"\n'
            'threshold = 2\n'
            'window = "5m"\n'
            '\n'
            '[rule.delivery]\n'
            'receiver = "foreground"\n'
            '\n'
            '[rule.guidance]\n'
            'summary = "Bad rule {{subject}}"\n'
            'check = ["Check"]\n'
            'do = ["Do"]\n'
        )
        head = commit_all(repo, "feat: invalid watch rule")
        res = run_check(repo, base, head)
        assert res.returncode == 1, res.stdout + res.stderr
        assert "watch-lint-" in res.stdout, res.stdout + res.stderr


def test_invalid_recorded_verdict_fails_step3():
    with tempfile.TemporaryDirectory(dir=SCRATCH, prefix="workflow-gate-example-") as tmp:
        repo = Path(tmp)
        base = workflow_repo(repo)
        defn_path = repo / "tools/tillr/examples/recorded-verdict/definition.json"
        defn = json.loads(defn_path.read_text())
        defn["exit"] = {"requires_candidate": "commit_required"}
        defn_path.write_text(json.dumps(defn, indent=2) + "\n")
        head = commit_all(repo, "feat: invalid recorded-verdict charter")
        res = run_check(repo, base, head)
        assert res.returncode == 1, res.stdout + res.stderr
        assert "watch-lint-" in res.stdout, res.stdout + res.stderr


def test_new_backend_policy_variant_triggers_and_fails():
    with tempfile.TemporaryDirectory(dir=SCRATCH, prefix="workflow-gate-policy-") as tmp:
        repo = Path(tmp)
        base = workflow_repo(repo)
        variant = repo / "config/tillr/backend-policy.variant.json"
        variant.write_text('{\n  "default_backend": "pi",\n  "backends": {"pi": {"adapter": "pi", "model": "m"}},\n  "seats": {"scout": "pi"}\n}\n')
        head = commit_all(repo, "feat: invalid backend-policy variant")
        res = run_check(repo, base, head)
        assert res.returncode == 1, res.stdout + res.stderr
        assert "watch-lint-seat-unmapped" in res.stdout, res.stdout + res.stderr


def test_recorded_verdict_commit_required_workflow_fails_step3():
    with tempfile.TemporaryDirectory(dir=SCRATCH, prefix="workflow-gate-rv-wf-") as tmp:
        repo = Path(tmp)
        base = workflow_repo(repo)
        wf_path = repo / "tools/tillr/examples/recorded-verdict/config/tillr/workflows/recorded-verdict@v1.json"
        wf = json.loads(wf_path.read_text())
        wf["exit"] = {"requires_candidate": "commit_required", "requires_nodes": ["investigate"]}
        wf_path.write_text(json.dumps(wf, indent=2) + "\n")
        head = commit_all(repo, "feat: commit_required workflow in recorded-verdict")
        res = run_check(repo, base, head)
        assert res.returncode == 1, res.stdout + res.stderr
        assert "watch-lint-charter-candidate" in res.stdout, res.stdout + res.stderr


if __name__ == "__main__":
    test_registry()
    test_unrelated_skips()
    test_clean_change_passes_all_steps()
    test_invalid_workflow_fails_step1()
    test_invalid_watch_rule_fails_step2()
    test_invalid_recorded_verdict_fails_step3()
    test_new_backend_policy_variant_triggers_and_fails()
    test_recorded_verdict_commit_required_workflow_fails_step3()
    print("pass: all workflows gate check tests passed")
