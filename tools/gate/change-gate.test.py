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
                        # Even with a similar flag listed, an old runner must get the legacy call.
                        else:
                            help_text += b"  -scope-to-change-later\n"
                        return subprocess.CompletedProcess(args, 0, stdout=b"", stderr=help_text)
                    if args[0] == "runner":
                        self.assertEqual("--scope-to-change" in args, supported)
                        self.assertFalse(kwargs["env"]["TILLR_FAST_TREE_TIMING"])
                        return subprocess.CompletedProcess(args, 0, stdout=b"")
                    self.assertEqual(args[1:], ["tools/fork-check.py", "--since", "base", "--tier", "landing"])
                    return subprocess.CompletedProcess(args, 0)

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
        self.run_stall_case(False)

    def test_remaining_gate_stall_survives_the_real_adapter(self):
        self.run_stall_case(True)

    def test_direct_wrapper_keeps_only_the_stall_handoff(self):
        self.run_stall_case(True, measure=False)

    def run_stall_case(self, remaining, measure=True):
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
 raise AssertionError('remaining gate must not run after a stall: '+repr(args))
sys.argv=['change-gate','--base','base','--head','head','--runner','stub-checks','--logs','logs']
with patch.object(module,'warm_trimmed_go_cache'),patch.object(module.subprocess,'run',side_effect=invoke):
 code=module.main()
 assert commands[-1][0]=='stub-checks'
 sys.exit(code)
'''
            if remaining:
                successful = {"payload": {"wall_ms": 2, "checks": [
                    {"check_id": "inner", "result": "pass", "execution": "reused", "wall_ms": 0}]}}
                successful_bytes = (json.dumps(successful) + "\n").encode()
                harness = harness.replace("if args[0]=='stub-checks':return subprocess.CompletedProcess(args,6,stdout=record)",
                    "if args[0]=='stub-checks':\n"
                    f"  assert kwargs['env']['TILLR_FAST_TREE_TIMING']=={('1' if measure else '')!r}\n"
                    "  assert not (set(kwargs['env']) & set(module.GATE_HANDOFFS) - {'TILLR_FAST_TREE_TIMING'})\n"
                    f"  return subprocess.CompletedProcess(args,0,stdout={successful_bytes!r})\n"
                    " if args[0]==sys.executable:\n  assert '--lock-timeout' not in args\n"
                    "  with open(os.environ['TILLR_FAST_VERDICT_OUT'],'rb') as first:\n"
                    f"   assert first.read()=={successful_bytes!r}\n"
                    "  open(os.environ['TILLR_FAST_VERDICT_OUT'],'wb').write(record)\n  return subprocess.CompletedProcess(args,6)")
                harness = harness.replace("assert commands[-1][0]=='stub-checks'", "assert commands[-1][0]==sys.executable")
            result = subprocess.run(["python3", "-c", harness, str(PROGRAM)],
                                    env=dict(os.environ, TILLR_FAST_VERDICT_OUT=str(output),
                                             TILLR_GATE_SPANS_OUT=str(output.with_name("spans.json")),
                                             TILLR_ORDINARY_GATE_TIMING="1" if measure else ""),
                                    capture_output=True)
            self.assertEqual(result.returncode, 6, result.stderr)
            payload = json.loads(output.read_bytes())["payload"]
            if remaining:
                self.assertEqual(result.stdout, successful_bytes)
                if measure:
                    self.assertEqual(payload["fast_timing"], successful["payload"])
                else:
                    self.assertNotIn("fast_timing", payload)
            else:
                self.assertEqual(result.stdout, output.read_bytes())
            self.assertEqual(payload["result"], "stalled")
            self.assertEqual(payload["checks"], [{
                "check_id": "blocked", "result": "stalled", "duration_ms": 60000,
                "silent_samples": 60, "cpu_time_ms": 12, "last_output_line": "last",
            }])

    def test_failed_timing_replacement_leaves_stall_bytes_intact(self):
        adapter = runpy.run_path(str(PROGRAM))
        with tempfile.TemporaryDirectory(dir=os.environ["YOLO_DURABLE_DIR"]) as directory:
            output = Path(directory) / "fast.json"
            stalled = b'{"payload":{"result":"stalled","checks":[]}}\n'
            output.write_bytes(stalled)
            with patch.object(Path, "replace", side_effect=OSError("fixture cannot replace")), \
                 patch("sys.stderr") as stderr:
                adapter["preserve_fast_timing"](output, b'{"payload":{"wall_ms":0,"checks":[]}}')
            stderr.write.assert_any_call("change-gate: could not preserve fast timings: fixture cannot replace")
            self.assertEqual(output.read_bytes(), stalled)
            self.assertEqual(list(Path(directory).iterdir()), [output])


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

    def test_remainder_classification_does_not_change_fast_verdict_bytes(self):
        adapter = runpy.run_path(str(PROGRAM))["main"]
        verdict = b' {"payload": {"checks": []}}\n\n'
        verdict_path = self.root / "verdict"
        runner = self.executable(self.root / "runner", "import sys\n"
                                 "if '--help' in sys.argv: print('  --scope-to-change')\n"
                                 f"else: sys.stdout.buffer.write({verdict!r})\n")
        # Run the real classifier, then substitute an answer distinct from the raw exit.
        # This is a test-boundary patch, not a tillr-gate stand-in or a copy of its rules.
        import classify
        def answer(observations):
            self.assertEqual(observations, [
                {"check": "legacy-remainder", "commit": "head", "source": "judging-program", "exit_code": 1}])
            self.assertEqual(classify.classify(observations)[0]["outcome"], "fail")
            return [{"outcome": "unknown", "detail": "error"}]

        run = subprocess.run
        def produce(cmd, **kwargs):
            if cmd[0] == "git":
                out = b"" if cmd[1] == "diff" else "head\n"
                return subprocess.CompletedProcess(cmd, 0, stdout=out)
            if cmd[0] == sys.executable:
                return subprocess.CompletedProcess(cmd, 1)
            return run(cmd, **kwargs)

        with tempfile.TemporaryFile() as out, patch.object(sys, "stdout", wraps=sys.stdout, buffer=out), \
             patch.object(sys, "argv", [str(PROGRAM), "--base", "base", "--head", "head",
                                        "--runner", str(runner), "--logs", "logs"]), \
             patch.dict(os.environ, TILLR_FAST_VERDICT_OUT=str(verdict_path)), \
             patch.dict(adapter.__globals__, {"classify": answer}), patch.object(subprocess, "run", side_effect=produce):
            self.assertEqual(adapter(), 4)
            out.seek(0)
            self.assertEqual(out.read(), verdict)
        self.assertEqual(verdict_path.read_bytes(), verdict)

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

    def test_cached_record_and_renderer_send_raw_observations_and_use_answer(self):
        import classify
        with patch.object(sys, "path", [str(PROGRAM.parent / "checks"), *sys.path]):
            import change_common as common

        (self.root / "key.pass.json").write_text('{"result": "pass", "time": "now"}')
        (self.root / "key.fail.json").write_text('{"result": "pass", "time": "now"}')
        self.assertIsNone(common.stored_result(self.root, "key", "fail"))
        installed_classify = classify.classify
        recorded = []
        def answer(observations):
            recorded.extend(observations)
            self.assertEqual(installed_classify(observations)[0]["outcome"], "pass")
            return [{"outcome": "unknown", "detail": "error"}]

        with patch.object(classify, "classify", side_effect=answer), \
             patch.object(common, "result_store", return_value=self.root), \
             patch.object(common, "git", return_value="tree"), \
             patch.object(common, "render", return_value=(0, {"version": "1", "findings": []}, 9)):
            self.assertIsNone(common.stored_result(self.root, "key", "pass"))
            with self.assertRaisesRegex(RuntimeError, "vantage-check exited"):
                common.judge_document_set(self.root, ["README.md"], {"README.md": "new-key"}, {}, {})
        self.assertEqual(recorded, [
            {"check": "documents", "source": "cached-record", "word": "pass"},
            {"check": "documents", "source": "document-render", "exit_code": 0,
             "report_read": True, "has_finding": False},
        ])
        self.assertFalse((self.root / "new-key.pass.json").exists())

    def test_unencodable_cached_word_is_a_miss_and_renders_again(self):
        import classify
        with patch.object(sys, "path", [str(PROGRAM.parent / "checks"), *sys.path]):
            import change_common as common

        record = self.root / "key.pass.json"
        record.write_text(r'{"result":"\ud800","time":"now","tool":"1"}')
        with patch.object(classify, "classify", wraps=classify.classify) as face, \
             patch.object(common, "result_store", return_value=self.root), \
             patch.object(common, "git", return_value="tree"), \
             patch.object(common, "render", return_value=(0, {"version": "1", "findings": []}, 9)) as render:
            self.assertEqual(common.judge_document_set(self.root, ["README.md"], {"README.md": "key"}, {}, {}), 0)
        render.assert_called_once_with(["README.md"], {})
        face.assert_called_once_with([{"check": "documents", "source": "document-render", "exit_code": 0,
                                      "report_read": True, "has_finding": False}])
        self.assertEqual(json.loads(record.read_text())["result"], "pass")
        with patch.object(classify, "classify", side_effect=classify.ClassificationError("offline")):
            with self.assertRaisesRegex(classify.ClassificationError, "offline"):
                common.stored_result(self.root, "key", "pass")

    def test_go_module_uses_classification_of_raw_nonzero_exit(self):
        import classify
        with patch.object(sys, "path", [str(PROGRAM.parent / "checks"), *sys.path]):
            go_build = runpy.run_path(str(PROGRAM.parent / "checks/go-build.py"))["main"]
        (self.root / "go.mod").touch()
        calls = []
        def answer(observations):
            calls.extend(observations)
            self.assertEqual(classify.classify(observations)[0]["outcome"], "fail")
            return [{"outcome": "pass", "detail": "passed"}]

        with patch.dict(go_build.__globals__, {"GO_TOOL_DIRS": [self.root], "classify": answer,
                                               "build_module": lambda *_: (4, "failed build")}), \
             patch.dict(os.environ, YOLO_DURABLE_DIR=str(self.root)):
            self.assertEqual(go_build(memory_store=self.root), 0)
        self.assertEqual(calls, [{"check": "go-build", "source": "go-module-build", "exit_code": 4}])

    def test_stored_result_sends_recorded_check_id_with_three_arg_call(self):
        import classify
        with patch.object(sys, "path", [str(PROGRAM.parent / "checks"), *sys.path]):
            import change_common as common

        common.store_result(self.root, "mod", "pass", 3, "tree", "go-build-module-1", check_id="go-build")
        actual = classify.classify
        seen = []
        with patch.object(classify, "classify", side_effect=lambda obs: (seen.extend(obs), actual(obs))[1]):
            self.assertIsNotNone(common.stored_result(self.root, "mod", "pass"))
        self.assertEqual(seen, [{"check": "go-build", "source": "cached-record", "word": "pass"}])

    def test_preserve_fast_timing_uses_classified_stall_outcome(self):
        import classify
        adapter = runpy.run_path(str(PROGRAM))
        preserve = adapter["preserve_fast_timing"]
        first = b'{"payload":{"wall_ms":4,"checks":[{"check_id":"inner","result":"pass"}]}}'
        output = self.root / "fast.json"
        initial = b'{"payload":{"result":"stalled","checks":[]}}\n'
        output.write_bytes(initial)
        seen = []
        def reject(observations):
            seen.extend(observations)
            self.assertEqual(classify.classify(observations)[0]["detail"], "stalled")
            return [{"outcome": "unknown", "detail": "error"}]

        with patch.dict(preserve.__globals__, {"classify": reject}):
            preserve(output, first)
        self.assertEqual(seen, [{"source": "result-word", "word": "stalled"}])
        self.assertEqual(output.read_bytes(), initial)
        preserve(output, first)
        self.assertEqual(json.loads(output.read_bytes())["payload"]["fast_timing"]["wall_ms"], 4)


def load_tests(loader, tests, pattern):
    # The gate's existing self-suite invokes this file. Keep the adapter and
    # source-scan regressions in that same selected check without widening its reads.
    for name, case in (("adapter-classify.test.py", "AdapterClassificationTest"),
                       ("classification-scan.test.py", "ClassificationScanTest")):
        module = runpy.run_path(str(PROGRAM.with_name(name)))
        tests.addTests(loader.loadTestsFromTestCase(module[case]))
    return tests


if __name__ == "__main__":
    unittest.main()
