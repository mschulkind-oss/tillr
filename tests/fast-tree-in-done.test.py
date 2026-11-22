#!/usr/bin/env python3
"""The gate's fast tree declarations also run in done, rolling, and base replay."""
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("fork_check_tree", ROOT / "tools/fork-check.py")
gate = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = gate
spec.loader.exec_module(gate)


class FastTreeTests(unittest.TestCase):
    def test_adapter_and_declaration_changes_select_self_suite(self):
        for path in ("tools/gate/tree-check.py", "tools/gate/checks/scratch.json"):
            plan = gate.build_plan(ROOT, [], gate.Record(Path(os.devnull), None), [path], None, False,
                                   audit_upstreams=False, audit_yolo=False, audit_node=False,
                                   base="HEAD")
            self.assertIn("self-suite", {c.kind for c in plan.checks}, path)

    def test_replay_accepts_exported_ids_with_dotted_labels(self):
        check = gate.Check("fixture-tree", f"checks.json in {ROOT}", [], "test", 0)
        self.assertTrue(gate.preflight_selected(check, [gate.declaration_id(check.kind, check.label, ROOT)]))

    def test_every_fast_tree_declaration_is_planned_in_done_and_rolling(self):
        expected = {json.loads(p.read_text())["id"] for p in (ROOT / "tools/gate/checks").glob("*.json")
                    if json.loads(p.read_text()).get("scope") == "tree"
                    and json.loads(p.read_text()).get("tier") == "fast"}
        self.assertTrue(expected)
        for tier in (None, "rolling"):
            plan = gate.build_plan(ROOT, [], gate.Record(Path(os.devnull), None), [], None, False,
                                   audit_upstreams=False, audit_yolo=False, audit_node=False,
                                   base="HEAD", tier=tier)
            actual = {c.kind for c in plan.checks if getattr(c, "fast_tree", False)}
            self.assertEqual(expected, actual)
            for check in plan.checks:
                if check.kind in expected:
                    declaration = next(json.loads(p.read_text()) for p in (ROOT / "tools/gate/checks").glob("*.json")
                                       if json.loads(p.read_text()).get("id") == check.kind)
                    if "." in declaration.get("reads", []):
                        self.assertTrue(gate.watched_path("arbitrary/dirty.md", set(check.watches)), check.kind)
                    self.assertIn("tools/gate/tree-check.py", check.argv)
                    self.assertFalse(check.reusable)
                    self.assertTrue(gate.preflight_selected(check, [check.kind]))
                    self.assertTrue(gate.preflight_selected(check, [gate.declaration_id(check.kind, check.label, ROOT)]))

    def test_just_done_and_rolling_refuse_a_red_tree_and_replay_names_it(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "tools/gate/checks").mkdir(parents=True)
            shutil.copy(ROOT / "tools/fork-check.py", root / "tools/fork-check.py")
            shutil.copy(ROOT / "tools/gate/tree-check.py", root / "tools/gate/tree-check.py")
            shutil.copy(ROOT / "tools/gate/classify.py", root / "tools/gate/classify.py")
            recipe = (ROOT / ".justfile").read_text().split("done *args:\n", 1)[1].split("\n\n", 1)[0]
            (root / ".justfile").write_text("done *args:\n" + recipe + "\n")
            decl = {"id": "fixture-tree", "label": "fixture tree", "scope": "tree", "tier": "fast",
                    "budget_ms": 100, "command": ["sh", "-c", "exit 1"], "reads": ["src/"]}
            (root / "tools/gate/checks/fixture.json").write_text(json.dumps(decl))
            runner = root / "tillr-gate"
            runner.write_text("#!/usr/bin/env python3\n" +
                "import json,os,shutil,sys\nfrom pathlib import Path\n" +
                "args=sys.argv\n" +
                "if args[1:2]==['classify']:\n" +
                "    installed=shutil.which('tillr-gate', path=os.environ['PATH'].partition(os.pathsep)[2])\n" +
                "    if installed: os.execv(installed, [installed, *args[1:]])\n" +
                "    sys.exit(127)\n" +
                "assert args[1:3]==['checks','--fast-tree']\n" +
                "checks=Path(args[args.index('--checks')+1]); ds=[json.loads(p.read_text()) for p in checks.glob('*.json')]\n" +
                "assert len(ds)==1 and ds[0]['id']=='fixture-tree'\n" +
                "rc=int(os.environ.get('TREE_RC','1')); result={0:'pass',1:'fail',6:'stalled'}.get(rc,'error')\n" +
                "Path(os.environ['TREE_RANGE']).write_text(args[args.index('--base')+1])\n" +
                "print(json.dumps({'payload':{'checks':[{'check_id':'fixture-tree','result':result}]}}))\n" +
                "sys.exit(rc)\n")
            runner.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ["PATH"],
                       YOLO_DURABLE_DIR=str(root), FORK_CHECK_PLAN_BASE="", TREE_RANGE=str(root / "range.calls"))
            for key in ("PI_FORKS_ROOT", "TILLR_GATE_SPANS_OUT", "FORK_CHECK_REUSE"):
                env.pop(key, None)
            for cmd in (["git", "init", "-q"], ["git", "add", "."],
                        ["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"]):
                subprocess.run(cmd, cwd=root, env=env, check=True)
            for args in ([], ["--tier", "rolling"], ["--replay", "--only", "fixture-tree"]):
                command = ["just", "done", *args] if not args else [sys.executable, "tools/fork-check.py", *args]
                result = subprocess.run(command, cwd=root, env=env, text=True, capture_output=True)
                self.assertEqual(1, result.returncode, result.stdout + result.stderr)
                self.assertIn("fixture-tree", result.stdout + result.stderr)
            # The approved baseline, not HEAD or an unrelated caller's range, is
            # handed to the declaration runner on the next done.
            green = subprocess.run(["just", "done"], cwd=root, env=dict(env, TREE_RC="0"), text=True, capture_output=True)
            self.assertEqual(0, green.returncode, green.stdout + green.stderr)
            verified = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
            (root / "src").mkdir()
            (root / "src/change").write_text("later commit\n")
            subprocess.run(["git", "add", "src"], cwd=root, env=env, check=True)
            subprocess.run(["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "later"], cwd=root, env=env, check=True)
            red = subprocess.run(["just", "done"], cwd=root, env=env, text=True, capture_output=True)
            self.assertEqual(1, red.returncode, red.stdout + red.stderr)
            self.assertEqual(verified, (root / "range.calls").read_text())
            for rc, expected in ((4, 2), (6, 2)):
                replay = subprocess.run([sys.executable, "tools/fork-check.py", "--replay", "--only", "fixture-tree"],
                                        cwd=root, env=dict(env, TREE_RC=str(rc)), text=True, capture_output=True)
                self.assertEqual(expected, replay.returncode, replay.stdout + replay.stderr)


if __name__ == "__main__":
    unittest.main()
