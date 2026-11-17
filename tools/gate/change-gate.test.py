#!/usr/bin/env python3
"""Check the real change adapter's stall handoff without clock waits."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

PROGRAM = Path(__file__).with_name("change-gate.py").resolve()


class ChangeGateStallTest(unittest.TestCase):
    def test_stall_record_and_exit_survive_the_real_adapter(self):
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
 if args[0]=='stub-checks':return subprocess.CompletedProcess(args,6,stdout=record)
 raise AssertionError('remaining gate must not run after a stall: '+repr(args))
sys.argv=['change-gate','--base','base','--head','head','--runner','stub-checks','--logs','logs']
with patch.object(module,'warm_trimmed_go_cache'),patch.object(module.subprocess,'run',side_effect=invoke):
 code=module.main()
 assert commands[-1][0]=='stub-checks'
 sys.exit(code)
'''
            result = subprocess.run(["python3", "-c", harness, str(PROGRAM)],
                                    env=dict(os.environ, TILLR_FAST_VERDICT_OUT=str(output)),
                                    capture_output=True)
            self.assertEqual(result.returncode, 6, result.stderr)
            self.assertEqual(result.stdout, output.read_bytes())
            check = json.loads(output.read_bytes())["payload"]["checks"][0]
            self.assertEqual(check["check_id"], "blocked")
            self.assertEqual(check["silent_samples"], 60)
            self.assertEqual(check["cpu_time_ms"], 12)


if __name__ == "__main__":
    unittest.main()
