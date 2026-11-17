#!/usr/bin/env python3
"""Runner changes pay their own proof before a real fixture merge."""
import contextlib
import importlib.util
import io
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("runner_first_gate", ROOT / "tools/fork-check.py")
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)
wrapper_spec = importlib.util.spec_from_file_location("runner_first_wrapper", ROOT / "tools/gate/change-gate.py")
wrapper = importlib.util.module_from_spec(wrapper_spec)
wrapper_spec.loader.exec_module(wrapper)
SCRATCH = Path(os.environ.get("YOLO_DURABLE_DIR", ROOT / "tools/gate/.scratch"))
SCRATCH.mkdir(parents=True, exist_ok=True)


class RunnerFirst(unittest.TestCase):
    def plan(self, paths, tier):
        return gate.build_plan(ROOT, [], gate.Record(SCRATCH / "runner-first-state.json"),
                               paths, None, False, tier=tier)

    def test_runner_suite_is_never_deferred(self):
        for path in ("tools/tillr-gate/main.go", "tools/gate/change-gate.py",
                     "yolo-packs/tillr/files/local/share/tillr/tillr-gate/main.go"):
            with self.subTest(path=path):
                for tier in (None, gate.TIER_LANDING):
                    plan = self.plan([path], tier)
                    self.assertIn("gate-runner", [c.kind for c in plan.checks])
                    self.assertFalse(any(s.owed and s.kind == "gate-runner" for s in plan.skips))
        self.assertNotIn("gate-runner", [c.kind for c in self.plan(["docs/example.md"], gate.TIER_LANDING).checks])
        self.assertIn("self-suite", [c.kind for c in self.plan(["tools/fork-check.py"], gate.TIER_LANDING).checks])

    def test_missing_go_blocks_instead_of_deferring(self):
        original = gate.shutil.which
        with mock.patch.object(gate.shutil, "which", side_effect=lambda name: None if name == "go" else original(name)):
            plan = self.plan(["tools/tillr-gate/main.go"], gate.TIER_LANDING)
        self.assertTrue(any(s.kind == "gate-runner" and s.planned and not s.owed for s in plan.skips))

    def test_runner_and_ordinary_candidates_merge_only_after_selected_suites(self):
        with tempfile.TemporaryDirectory(dir=SCRATCH) as tmp:
            repo = Path(tmp)
            env = dict(os.environ, GIT_CONFIG_COUNT="2", GIT_CONFIG_KEY_0="user.name",
                       GIT_CONFIG_VALUE_0="Fixture", GIT_CONFIG_KEY_1="user.email",
                       GIT_CONFIG_VALUE_1="fixture@example.invalid")
            def git(*args):
                return subprocess.run(["git", *args], cwd=repo, env=env, check=True,
                                      capture_output=True, text=True).stdout.strip()
            git("init", "-q")
            (repo / "initial").write_text("base\n")
            git("add", ".")
            git("commit", "-qm", "chore: base")
            trunk = git("branch", "--show-current")
            events = []
            for name, path, red in (("red", "tools/tillr-gate/main.go", True),
                                    ("runner", "tools/tillr-gate/main.go", False),
                                    ("ordinary", "notes.txt", False)):
                git("checkout", "-qb", name)
                target = repo / path
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(name + "\n")
                git("add", ".")
                git("commit", "-qm", "chore: candidate")
                plan = self.plan([path], gate.TIER_LANDING)
                suites = [c for c in plan.checks if c.kind == "gate-runner"]
                # Exercise the production wrapper's verdict propagation. The fast
                # checks pass; the remaining program uses the real suite selection
                # with a recording process in place of the suite's assertions.
                real_run = subprocess.run
                def run(argv, **kwargs):
                    if argv[0] == "fixture-runner":
                        return subprocess.CompletedProcess(argv, 0, stdout=b"{}\\n")
                    if "tools/fork-check.py" in argv:
                        rc = 0
                        for check in suites:
                            events.append((name, "suite"))
                            rc |= real_run(["sh", "-c", "exit 1" if red else "exit 0"], cwd=repo).returncode
                        return subprocess.CompletedProcess(argv, rc)
                    return real_run(argv, **kwargs)
                previous = Path.cwd()
                try:
                    os.chdir(repo)
                    # A self-suite inherits the outer gate's artifact environment.
                    # Never replace its real fast verdict with the fixture bytes.
                    with mock.patch.dict(wrapper.os.environ, {"TILLR_FAST_VERDICT_OUT": ""}), mock.patch.object(wrapper, "warm_trimmed_go_cache"), mock.patch.object(wrapper.subprocess, "run", side_effect=run), mock.patch.object(wrapper.sys, "argv", ["change-gate", "--base", "HEAD~1", "--head", "HEAD", "--runner", "fixture-runner", "--logs", "logs"]):
                        passed = wrapper.main() == 0
                finally:
                    os.chdir(previous)
                git("checkout", "-q", trunk)
                before = git("rev-parse", "HEAD")
                if passed:
                    git("merge", "--ff-only", name)
                    events.append((name, "merge"))
                else:
                    self.assertEqual(before, git("rev-parse", "HEAD"))
            self.assertEqual(events, [("red", "suite"), ("runner", "suite"),
                                      ("runner", "merge"), ("ordinary", "merge")])

    def test_outer_fast_verdict_survives_self_suite(self):
        with tempfile.TemporaryDirectory(dir=SCRATCH) as tmp:
            verdict = Path(tmp) / "outer-verdict.json"
            verdict.write_text("real outer verdict\n")
            with mock.patch.dict(os.environ, {"TILLR_FAST_VERDICT_OUT": str(verdict)}):
                self.test_runner_and_ordinary_candidates_merge_only_after_selected_suites()
            self.assertEqual(verdict.read_text(), "real outer verdict\n")

    def test_exception_cost_is_reported_separately(self):
        plan = self.plan(["tools/tillr-gate/main.go"], gate.TIER_LANDING)
        for check in plan.checks:
            check.seconds = 120 if check.kind == "gate-runner" else 1
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            gate.print_plan(gate.Record(SCRATCH / "runner-first-state.json"), plan, "base", "head")
        fields = dict(word.split("=", 1) for word in out.getvalue().split()[2:])
        self.assertEqual(fields["landing_class"], "runner")
        self.assertEqual(float(fields["landing"]), 0)
        self.assertGreaterEqual(float(fields["runner_landing"]), 120)
        self.assertEqual(fields["runner_count"], "1")


if __name__ == "__main__":
    unittest.main()
