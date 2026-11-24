#!/usr/bin/env python3
"""Check `just done`'s program against a recording stand-in for tillr-gate."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

PROGRAM = Path(__file__).with_name("done.py").resolve()

RUNNER = r'''#!{python}
import json, os, sys
with open(os.environ["RUNNER_LOG"], "a") as log:
    log.write(json.dumps(sys.argv[1:]) + "\n")
kind = "fast" if "--fast-tree" in sys.argv else "heavy"
answer = json.loads(os.environ.get("RUNNER_" + kind.upper(), '{{"code": 0, "checks": [["one", "pass"]]}}'))
if answer.get("silent"):
    sys.exit(answer["code"])
print(json.dumps({{"payload": {{"checks": [
    {{"check_id": ident, "result": result, "log_ref": "logs/" + ident + ".log"}}
    for ident, result in answer["checks"]]}}}}))
sys.exit(answer["code"])
'''


class DoneTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory(dir=os.environ.get("YOLO_DURABLE_DIR"))
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.log = self.root / "runner.log"
        self.runner = self.root / "runner"
        self.runner.write_text(RUNNER.format(python=sys.executable))
        self.runner.chmod(0o755)
        self.env = dict(os.environ, RUNNER_LOG=str(self.log), GIT_CONFIG_GLOBAL=str(self.root / "gitconfig"),
                        GIT_CONFIG_NOSYSTEM="1")
        (self.root / "gitconfig").write_text(
            "[user]\n name = fixture\n email = fixture@example.invalid\n[init]\n defaultBranch = main\n")
        self.git("init", "-q")
        self.first = self.commit("first")
        self.second = self.commit("second")
        self.head = self.commit("third")

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.repo, env=self.env, check=True,
                              capture_output=True, text=True).stdout.strip()

    def commit(self, text):
        (self.repo / "file.txt").write_text(text + "\n")
        self.git("add", "file.txt")
        self.git("commit", "-q", "-m", text)
        return self.git("rev-parse", "HEAD")

    def done(self, *args, **answers):
        self.log.write_text("")
        env = dict(self.env, **{"RUNNER_" + kind.upper(): json.dumps(answer) for kind, answer in answers.items()})
        ran = subprocess.run([sys.executable, str(PROGRAM), "--runner", str(self.runner), *args],
                             cwd=self.repo, env=env, capture_output=True, text=True)
        calls = [json.loads(line) for line in self.log.read_text().splitlines()]
        return ran, calls

    def value(self, call, flag):
        return call[call.index(flag) + 1]

    def test_two_passes_run_in_order_and_a_clean_tree_passes(self):
        ran, calls = self.done()
        self.assertEqual(ran.returncode, 0, ran.stdout + ran.stderr)
        self.assertEqual([call[:2] for call in calls], [["checks", "--fast-tree"], ["checks", "--heavy-tree"]])
        # With no proved ref and no upstream, the change checks judge HEAD's own commit.
        self.assertEqual((self.value(calls[0], "--base"), self.value(calls[0], "--head")), (self.second, self.head))
        # The heavy checks judge one revision, not a range.
        self.assertEqual((self.value(calls[1], "--base"), self.value(calls[1], "--head")), (self.head, self.head))
        self.assertEqual(self.value(calls[0], "--checks"), "tools/gate/checks")
        self.assertTrue(ran.stdout.rstrip().endswith("done: PASSED"), ran.stdout)

    def test_uncommitted_changes_are_refused_before_any_check_runs(self):
        (self.repo / "file.txt").write_text("edited\n")
        ran, calls = self.done()
        self.assertEqual(ran.returncode, 4)
        self.assertEqual(calls, [])
        self.assertIn("uncommitted changes", ran.stderr)

    def test_a_failed_check_is_named_with_its_log_and_the_heavy_pass_still_runs(self):
        ran, calls = self.done(fast={"code": 1, "checks": [["one", "pass"], ["two", "fail"]]})
        self.assertEqual(ran.returncode, 1, ran.stdout + ran.stderr)
        self.assertEqual(len(calls), 2)
        self.assertIn("FAIL two: fail  log: logs/two.log", ran.stdout)
        self.assertIn("ok   one: pass", ran.stdout)
        self.assertTrue(ran.stdout.rstrip().endswith("done: FAILED"), ran.stdout)
        # A failing word is a failure even when the runner's exit code says otherwise.
        ran, _ = self.done(heavy={"code": 0, "checks": [["three", "stalled"]]})
        self.assertEqual(ran.returncode, 1, ran.stdout)

    def test_skipped_and_remembered_results_count_as_passed(self):
        ran, _ = self.done(heavy={"code": 0, "checks": [["a", "skipped: no changed path is read"],
                                                        ["b", "remembered pass"]]})
        self.assertEqual(ran.returncode, 0, ran.stdout)

    def test_no_verdict_is_could_not_judge_and_stops_before_the_heavy_pass(self):
        for answer in ({"code": 4, "silent": True}, {"code": 4, "checks": [["one", "pass"]]}):
            with self.subTest(answer=answer):
                ran, calls = self.done(fast=answer)
                self.assertEqual(ran.returncode, 4, ran.stdout + ran.stderr)
                self.assertEqual(len(calls), 1)
                self.assertTrue(ran.stdout.rstrip().endswith("done: COULD NOT JUDGE"), ran.stdout)

    def test_fast_and_only_each_run_one_pass(self):
        ran, calls = self.done("--fast")
        self.assertEqual(ran.returncode, 0, ran.stdout)
        self.assertEqual([call[1] for call in calls], ["--fast-tree"])
        ran, calls = self.done("--only", "go-test-example")
        self.assertEqual(ran.returncode, 0, ran.stdout)
        self.assertEqual([call[1] for call in calls], ["--heavy-tree"])
        self.assertEqual(self.value(calls[0], "--only"), "go-test-example")

    def test_base_is_since_then_the_proved_ref_when_head_descends_from_it(self):
        self.git("update-ref", "refs/tillr/green", self.first)
        _, calls = self.done("--fast")
        self.assertEqual(self.value(calls[0], "--base"), self.first)
        _, calls = self.done("--fast", "--since", self.second)
        self.assertEqual(self.value(calls[0], "--base"), self.second)
        # A proved ref on another line of history says nothing about this one.
        self.git("checkout", "-q", "--orphan", "other")
        other = self.commit("unrelated")
        self.git("checkout", "-q", "main")
        self.git("update-ref", "refs/tillr/green", other)
        _, calls = self.done("--fast")
        self.assertEqual(self.value(calls[0], "--base"), self.second)


if __name__ == "__main__":
    unittest.main()
