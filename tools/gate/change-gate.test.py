#!/usr/bin/env python3
"""Check the real change adapter's stall handoff without clock waits."""
import json
import os
from pathlib import Path
import runpy
import subprocess
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


if __name__ == "__main__":
    unittest.main()
