#!/usr/bin/env python3
"""A parked event is not the end of a concurrent tool batch."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

WATCHER = Path(__file__).resolve().parent.parent / "stop-at-gap"


class ParkedExitTest(unittest.TestCase):
    def run_child(self, program, bound="0.5"):
        with tempfile.TemporaryDirectory() as directory:
            env = dict(os.environ, TILLR_HARNESS_EXIT_BOUND_SECONDS=bound)
            return subprocess.run([sys.executable, str(WATCHER), str(Path(directory) / "pause"), sys.executable, "-u", "-c", program],
                                  capture_output=True, text=True, env=env, timeout=4)

    def test_sibling_finishes_before_normal_exit(self):
        result = self.run_child('''import json, time
print('{"type":"parked","toolCallId":"land"}', flush=True)
time.sleep(.2)
print('{"type":"message_end","message":{"role":"toolResult","toolCallId":"sibling"}}', flush=True)
print('{"type":"agent_end"}', flush=True)
''')
        self.assertEqual(result.returncode, 0, result.stderr)
        events = [json.loads(line) for line in result.stdout.splitlines()]
        self.assertEqual([event['type'] for event in events], ['parked', 'message_end', 'agent_end'])

    def test_incomplete_batch_has_distinct_failure(self):
        result = self.run_child('''import time
print('{"type":"parked"}', flush=True)
time.sleep(5)
''')
        self.assertNotEqual(result.returncode, 0)
        event = json.loads(result.stdout.splitlines()[-1])
        self.assertEqual(event['after'], 'parked-batch-incomplete')

    def test_disposal_hang_exits_at_the_same_bound(self):
        result = self.run_child('''import time
print('{"type":"parked"}', flush=True)
print('{"type":"agent_end"}', flush=True)
time.sleep(5)
''')
        self.assertEqual(result.returncode, 0, result.stderr)
        event = json.loads(result.stdout.splitlines()[-1])
        self.assertEqual(event['after'], 'parked')


if __name__ == "__main__":
    unittest.main()
