#!/usr/bin/env python3
"""Check the real change adapter's stall handoff without clock waits."""
import io
import json
import os
from pathlib import Path
import runpy
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

PROGRAM = Path(__file__).with_name("change-gate.py").resolve()
GATE_DIR = PROGRAM.parents[1] / "gate"
CHECKS_DIR = GATE_DIR / "checks"


class ChangeGateCompatibilityTest(unittest.TestCase):
    def test_probes_installed_runner_before_using_optional_flag(self):
        for supported, changed in ((False, b"README.md\0"),
                                   (True, b"README.md\0"),
                                   (True, b"tools/tillr-gate/main.go\0")):
            with self.subTest(supported=supported, changed=changed):
                adapter = runpy.run_path(str(PROGRAM))
                commands = []

                def invoke(args, **kwargs):
                    commands.append(args)
                    if args[:2] == ["git", "diff"]:
                        self.assertEqual(args[2:], ["--name-only", "--no-renames", "-z", "base", "head", "--"])
                        return subprocess.CompletedProcess(args, 0, stdout=changed)
                    if args[0] == "git":
                        return subprocess.CompletedProcess(args, 0, stdout="head\n")
                    if args == ["runner", "checks", "--help"]:
                        help_text = b"Usage of checks:\n  -fast-tree\n"
                        if supported:
                            help_text += b"  -scope-to-change\n    skip unrelated checks\n"
                        # Even with a similar flag listed, an old runner must get the unscoped call.
                        else:
                            help_text += b"  -scope-to-change-later\n"
                        return subprocess.CompletedProcess(args, 0, stdout=b"", stderr=help_text)
                    if args[0] == "runner":
                        self.assertEqual("--scope-to-change" in args, supported)
                        self.assertFalse(kwargs["env"]["TILLR_FAST_TREE_TIMING"])
                        return subprocess.CompletedProcess(args, 0, stdout=b"")
                    raise AssertionError("no program runs after the declared checks: " + repr(args))

                argv = [str(PROGRAM), "--base", "base", "--head", "head", "--runner",
                        "runner", "--logs", "logs"]
                warming = []
                with patch.object(adapter["sys"], "argv", argv), \
                     patch.dict(os.environ, {"TILLR_FAST_VERDICT_OUT": ""}), \
                     patch.object(Path, "read_text", return_value='{"reads": ["tools/tillr-gate/"]}'), \
                     patch.dict(adapter["main"].__globals__, {"warm_trimmed_go_cache": lambda: warming.append(True)}), \
                     patch.object(subprocess, "run", side_effect=invoke):
                    self.assertEqual(adapter["main"](), 0)
                self.assertEqual(bool(warming), not supported or changed.startswith(b"tools/"))
                runner_commands = [c for c in commands if c[0] == "runner"]
                self.assertEqual(len(runner_commands), 2)
                self.assertEqual(runner_commands[0], ["runner", "checks", "--help"])


class ChangeGateStallTest(unittest.TestCase):
    def test_stall_record_and_exit_survive_the_real_adapter(self):
        self.run_stall_case()

    def run_stall_case(self, measure=True):
        with tempfile.TemporaryDirectory(dir=os.environ["YOLO_DURABLE_DIR"]) as directory:
            output = Path(directory) / "fast.json"
            harness = r'''
import importlib.util, json, os, subprocess, sys
from unittest.mock import patch
spec=importlib.util.spec_from_file_location('change_gate', sys.argv[1])
module=importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
record=json.dumps({'payload':{'result':'stalled','checks':[{'check_id':'blocked','result':'stalled','duration_ms':60000,'silent_samples':60,'cpu_time_ms':12,'last_output_line':'last'}]}}).encode()+b'\n'
commands=[]
def invoke(args, **kwargs):
 commands.append(args)
 if args[0]=='git':return subprocess.CompletedProcess(args,0,stdout='head\n')
 if args==['stub-checks','checks','--help']:return subprocess.CompletedProcess(args,0,stdout=b'')
 if args[0]=='stub-checks':return subprocess.CompletedProcess(args,6,stdout=record)
 raise AssertionError('nothing runs after the declared checks: '+repr(args))
sys.argv=['change-gate','--base','base','--head','head','--runner','stub-checks','--logs','logs']
with patch.object(module,'warm_trimmed_go_cache'),patch.object(module.subprocess,'run',side_effect=invoke):
 code=module.main()
 assert commands[-1][0]=='stub-checks'
 sys.exit(code)
'''
            result = subprocess.run(["python3", "-c", harness, str(PROGRAM)],
                                    env=dict(os.environ, TILLR_FAST_VERDICT_OUT=str(output),
                                             TILLR_GATE_SPANS_OUT=str(output.with_name("spans.json")),
                                             TILLR_ORDINARY_GATE_TIMING="1" if measure else ""),
                                    capture_output=True)
            self.assertEqual(result.returncode, 6, result.stderr)
            payload = json.loads(output.read_bytes())["payload"]
            self.assertEqual(result.stdout, output.read_bytes())
            self.assertEqual(payload["result"], "stalled")
            self.assertEqual(payload["checks"], [{
                "check_id": "blocked", "result": "stalled", "duration_ms": 60000,
                "silent_samples": 60, "cpu_time_ms": 12, "last_output_line": "last",
            }])

class DeclarationClassificationTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.installed = shutil.which("tillr-gate")
        self.assertIsNotNone(self.installed, "the installed tillr-gate must be on PATH")

    def executable(self, path, body):
        path.write_text(f"#!{sys.executable}\n" + body)
        path.chmod(0o755)
        return path

    def test_tree_sends_code_and_word_to_path_and_checks_pair_and_identity(self):
        checks = self.root / "tools/gate/checks"
        checks.mkdir(parents=True)
        (checks / "check.json").write_text(json.dumps({"scope": "tree", "tier": "fast", "id": "one"}))
        log = self.root / "observations.json"
        bin_dir = self.root / "bin"
        bin_dir.mkdir()
        self.executable(bin_dir / "tillr-gate", "import os, sys\n"
                        "assert sys.argv[1:] == ['classify']\n"
                        "data = sys.stdin.buffer.read()\n"
                        f"open({str(log)!r}, 'wb').write(data)\n"
                        # Forward the exact bytes and arguments to the next face on PATH.
                        "import subprocess\n"
                        f"sys.exit(subprocess.run([{self.installed!r}, *sys.argv[1:]], input=data).returncode)\n")
        decoy = self.root / "decoy-called"
        self.executable(self.root / "tillr-gate", f"open({str(decoy)!r}, 'w').close()\nraise SystemExit(99)\n")
        runner = self.executable(self.root / "runner", "import json, os, sys\n"
                                 "assert sys.argv[1:3] == ['checks', '--fast-tree']\n"
                                 "print(json.dumps({'payload': {'checks': [{'check_id': os.environ['ID'], "
                                 "'result': os.environ['WORD']}]}}))\nraise SystemExit(int(os.environ['CODE']))\n")
        command = [sys.executable, str(PROGRAM.with_name("tree-check.py")),
                   "--only", "one", "--base", "base", "--runner", str(runner)]
        for code, word, identity, expected in (
            (0, "pass", "one", 0), (1, "fail", "one", 1), (6, "stalled", "one", 6),
            (0, "fail", "one", 4), (1, "pass", "one", 4), (6, "error", "one", 4),
            (4, "error", "one", 4), (0, "PASS", "one", 4), (0, "ok", "one", 4),
            (1, "FAIL", "one", 4), (6, "STALLED", "one", 4), (0, "pass", "other", 4),
        ):
            with self.subTest(code=code, word=word, identity=identity):
                log.unlink(missing_ok=True)
                env = dict(os.environ, PATH=str(bin_dir) + os.pathsep + os.environ["PATH"],
                           YOLO_DURABLE_DIR=str(self.root), CODE=str(code), WORD=word, ID=identity)
                done = subprocess.run(command, cwd=self.root, env=env, capture_output=True, text=True)
                self.assertEqual(done.returncode, expected, done.stderr)
                if identity == "one":
                    self.assertEqual(json.loads(log.read_text()), [
                        {"check": "one", "commit": "HEAD", "source": "judging-program", "exit_code": code},
                        {"check": "one", "commit": "HEAD", "source": "result-word", "word": word},
                    ])
                else:
                    self.assertFalse(log.exists())
                self.assertFalse(decoy.exists())
        done = subprocess.run(command, cwd=self.root, capture_output=True, text=True,
                              env=dict(env, PATH=str(self.root / "no-tools"), CODE="0", WORD="pass", ID="one"))
        self.assertEqual(done.returncode, 4, done.stderr)
        self.assertIn("could not judge: tillr-gate classify", done.stderr)
        self.assertFalse(decoy.exists())

    def test_tree_returns_the_agreed_classification(self):
        tree_main = runpy.run_path(str(PROGRAM.with_name("tree-check.py")))["main"]
        declaration = self.root / "check.json"
        declaration.write_text('{"scope":"tree","tier":"fast","id":"one"}')
        runner = subprocess.CompletedProcess([], 0, stderr="", stdout=
            '{"payload":{"checks":[{"check_id":"one","result":"pass"}]}}')
        passed = {"outcome": "pass", "detail": "passed"}
        failed = {"outcome": "fail", "detail": "failed"}
        with patch.object(Path, "glob", return_value=[declaration]), \
             patch.object(sys, "argv", ["tree-check", "--only", "one", "--base", "base"]), \
             patch.dict(os.environ, YOLO_DURABLE_DIR=str(self.root)), \
             patch.object(subprocess, "run", return_value=runner):
            # Change only the returned classifications, not the raw pair from the runner.
            for answers, expected in (([failed, failed], 1), ([passed, failed], 4)):
                with self.subTest(answers=answers), \
                     patch.dict(tree_main.__globals__, {"classify": lambda _: answers}), \
                     patch.object(sys, "stdout", new_callable=io.StringIO), \
                     patch.object(sys, "stderr", new_callable=io.StringIO):
                    self.assertEqual(tree_main(), expected)

    def test_transport_rejects_missing_failed_malformed_and_reordered_answers(self):
        import classify

        observations = [{"source": "result-word", "word": "pass"},
                        {"source": "result-word", "word": "fail"}]
        self.assertEqual([r["outcome"] for r in classify.classify(observations)], ["pass", "fail"])
        with patch.dict(os.environ, PATH=str(self.root)):
            with self.assertRaisesRegex(classify.ClassificationError, "tillr-gate classify"):
                classify.classify(observations)
        for code, stdout in ((2, "[]"), (0, "not json"), (0, "null"), (0, "[]"),
                             (0, '[{}, {}]'), (0, '[{"outcome": "pass", "detail": 4}, {}]'),
                             (0, '[{"outcome": "fail", "detail": "failed", "raw_word": "fail"},'
                                 ' {"outcome": "pass", "detail": "passed", "raw_word": "pass"}]')):
            with self.subTest(code=code, stdout=stdout), patch.object(classify, "run_command", return_value=
                    subprocess.CompletedProcess([], code, stdout=stdout, stderr="")):
                with self.assertRaisesRegex(classify.ClassificationError, "tillr-gate classify"):
                    classify.classify(observations)

    def test_warm_stamp_uses_classified_outcome(self):
        import classify
        warm = runpy.run_path(str(PROGRAM))["warm_trimmed_go_cache"]
        mark = self.root / "tillr-gate/go-build-warm-go1.test"
        def produce(cmd, **kwargs):
            out = {"go": "go1.test\n", "git": str(self.root) + "\n"}.get(cmd[0])
            return subprocess.CompletedProcess(cmd, 0, stdout=out)

        def answer(observations):
            self.assertEqual(observations, [{"check": "go-build", "source": "judging-program", "exit_code": 0}])
            self.assertEqual(classify.classify(observations)[0]["outcome"], "pass")
            return [{"outcome": "unknown", "detail": "error"}]

        with patch.object(subprocess, "run", side_effect=produce):
            with patch.dict(warm.__globals__, {"classify": answer}):
                warm()
            self.assertFalse(mark.exists())
            warm()
        self.assertEqual(mark.read_text(), "go1.test\n")

    def test_change_gate_succeeds_in_consumer_repo_without_go_build(self):
        checks = self.root / "tools/gate/checks"
        checks.mkdir(parents=True)
        (checks / "ok.json").write_text(json.dumps({
            "id": "ok",
            "label": "Consumer check",
            "scope": "change",
            "tier": "fast",
            "budget_ms": 1000,
            "command": ["true"],
        }))
        env = dict(
            os.environ,
            GIT_CONFIG_COUNT="2",
            GIT_CONFIG_KEY_0="user.name",
            GIT_CONFIG_VALUE_0="Fixture",
            GIT_CONFIG_KEY_1="user.email",
            GIT_CONFIG_VALUE_1="fixture@example.invalid",
        )
        subprocess.run(["git", "init", "-q"], cwd=self.root, env=env, check=True)
        (self.root / "README.md").write_text("# Consumer\n")
        subprocess.run(["git", "add", "."], cwd=self.root, env=env, check=True)
        subprocess.run(["git", "commit", "-q", "-m", "base"], cwd=self.root, env=env, check=True)
        base = subprocess.run(["git", "rev-parse", "HEAD"], cwd=self.root, env=env, check=True,
                              capture_output=True, text=True).stdout.strip()
        (self.root / "README.md").write_text("# Consumer\n\nUpdated.\n")
        subprocess.run(["git", "add", "README.md"], cwd=self.root, env=env, check=True)
        subprocess.run(["git", "commit", "-q", "-m", "head"], cwd=self.root, env=env, check=True)
        head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=self.root, env=env, check=True,
                              capture_output=True, text=True).stdout.strip()
        done = subprocess.run(
            [sys.executable, str(PROGRAM), "--base", base, "--head", head,
             "--runner", self.installed, "--logs", str(self.root / "logs")],
            cwd=self.root, env=env, capture_output=True, text=True,
        )
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertNotIn("warming the trimmed Go build cache", done.stderr)
        self.assertNotIn("go-build.py", done.stderr)
        verdict = json.loads(done.stdout.splitlines()[0])["payload"]
        self.assertEqual([(c["check_id"], c["result"]) for c in verdict["checks"]], [("ok", "pass")])
        self.assertFalse(list((self.root / ".git").glob("tillr-gate/go-build-warm-*")))

    def test_custom_checks_dir_override_and_invalid_settings(self):
        env = dict(
            os.environ,
            GIT_CONFIG_COUNT="2",
            GIT_CONFIG_KEY_0="user.name",
            GIT_CONFIG_VALUE_0="Fixture",
            GIT_CONFIG_KEY_1="user.email",
            GIT_CONFIG_VALUE_1="fixture@example.invalid",
        )
        subprocess.run(["git", "init", "-q"], cwd=self.root, env=env, check=True)
        (self.root / ".tillr.toml").write_text('[gate]\nchecks_dir = "custom/checks"\n')
        custom = self.root / "custom/checks"
        custom.mkdir(parents=True)
        (custom / "ok.json").write_text(json.dumps({
            "id": "ok",
            "label": "Custom change check",
            "scope": "change",
            "tier": "fast",
            "budget_ms": 1000,
            "command": ["true"],
        }))
        (custom / "tree.json").write_text(json.dumps({
            "id": "tree-ok",
            "label": "Custom tree check",
            "scope": "tree",
            "tier": "fast",
            "budget_ms": 1000,
            "command": ["true"],
        }))
        (self.root / "README.md").write_text("# Base\n")
        subprocess.run(["git", "add", "."], cwd=self.root, env=env, check=True)
        subprocess.run(["git", "commit", "-q", "-m", "base"], cwd=self.root, env=env, check=True)
        base = subprocess.run(["git", "rev-parse", "HEAD"], cwd=self.root, env=env, check=True,
                              capture_output=True, text=True).stdout.strip()
        (self.root / "README.md").write_text("# Head\n")
        subprocess.run(["git", "add", "README.md"], cwd=self.root, env=env, check=True)
        subprocess.run(["git", "commit", "-q", "-m", "head"], cwd=self.root, env=env, check=True)
        head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=self.root, env=env, check=True,
                              capture_output=True, text=True).stdout.strip()

        # change-gate.py reads [gate] checks_dir from .tillr.toml
        done = subprocess.run(
            [sys.executable, str(PROGRAM), "--base", base, "--head", head,
             "--runner", self.installed, "--logs", str(self.root / "logs")],
            cwd=self.root, env=env, capture_output=True, text=True,
        )
        self.assertEqual(done.returncode, 0, done.stderr)
        verdict = json.loads(done.stdout.splitlines()[0])["payload"]
        self.assertEqual({(c["check_id"], c["result"]) for c in verdict["checks"]},
                         {("ok", "pass"), ("tree-ok", "pass")})

        # tree-check.py reads [gate] checks_dir from .tillr.toml
        tree_prog = PROGRAM.with_name("tree-check.py")
        tree_done = subprocess.run(
            [sys.executable, str(tree_prog), "--only", "tree-ok", "--base", base, "--head", head,
             "--runner", self.installed],
            cwd=self.root, env=env, capture_output=True, text=True,
        )
        self.assertEqual(tree_done.returncode, 0, tree_done.stderr)

        # --checks overrides .tillr.toml
        override = self.root / "override/checks"
        override.mkdir(parents=True)
        (override / "over.json").write_text(json.dumps({
            "id": "over",
            "label": "Override check",
            "scope": "tree",
            "tier": "fast",
            "budget_ms": 1000,
            "command": ["true"],
        }))
        over_done = subprocess.run(
            [sys.executable, str(PROGRAM), "--base", base, "--head", head,
             "--checks", "override/checks", "--runner", self.installed, "--logs", str(self.root / "logs-over")],
            cwd=self.root, env=env, capture_output=True, text=True,
        )
        self.assertEqual(over_done.returncode, 0, over_done.stderr)
        over_verdict = json.loads(over_done.stdout.splitlines()[0])["payload"]
        self.assertEqual([(c["check_id"], c["result"]) for c in over_verdict["checks"]], [("over", "pass")])
        tree_over = subprocess.run(
            [sys.executable, str(tree_prog), "--only", "over", "--base", base, "--head", head,
             "--checks", "override/checks", "--runner", self.installed],
            cwd=self.root, env=env, capture_output=True, text=True,
        )
        self.assertEqual(tree_over.returncode, 0, tree_over.stderr)

        # Invalid .tillr.toml exits 4 on both scripts when --checks is omitted
        (self.root / ".tillr.toml").write_text('[gate]\nchecks_dir = "../escape"\n')
        bad_cg = subprocess.run(
            [sys.executable, str(PROGRAM), "--base", base, "--head", head,
             "--runner", self.installed, "--logs", str(self.root / "logs-bad")],
            cwd=self.root, env=env, capture_output=True, text=True,
        )
        self.assertEqual(bad_cg.returncode, 4, bad_cg.stderr)
        bad_tc = subprocess.run(
            [sys.executable, str(tree_prog), "--only", "tree-ok", "--base", base, "--head", head,
             "--runner", self.installed],
            cwd=self.root, env=env, capture_output=True, text=True,
        )
        self.assertEqual(bad_tc.returncode, 4, bad_tc.stderr)


if __name__ == "__main__":
    unittest.main()
