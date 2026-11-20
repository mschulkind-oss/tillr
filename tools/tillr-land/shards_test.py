#!/usr/bin/env python3
"""Liveness regressions for the sharded Go runner."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest

RUNNER = Path(__file__).resolve().parents[1] / 'go-test-shards.py'
spec = importlib.util.spec_from_file_location('shards', RUNNER)
shards = importlib.util.module_from_spec(spec)
spec.loader.exec_module(shards)


class ShardLivenessTests(unittest.TestCase):
    def test_stalled_test_is_named_and_process_group_is_killed(self):
        parent = os.environ.get('TMPDIR') or os.environ['YOLO_DURABLE_DIR']
        with tempfile.TemporaryDirectory(dir=parent) as temporary:
            root = Path(temporary)
            binary = root / 'fake-test'
            marker = root / 'child-survived'
            binary.write_text('''#!/usr/bin/env python3
import subprocess, sys, time
subprocess.Popen([sys.executable, '-c', "import time,pathlib; time.sleep(3); pathlib.Path(%r).touch()"])
print('=== RUN   TestHung', flush=True)
while True:
 print('still spinning', flush=True)
 time.sleep(0.05)
''' % str(marker))
            binary.chmod(0o755)
            with self.assertRaises(shards.ShardError) as caught:
                shards.run_shard('fixture', root, binary, ['TestHung'], 2,
                                 root / 'scratch', '0', [], wall_timeout=1,
                                 lock_dir=root / 'locks')
            self.assertIn('fixture shard 2', str(caught.exception))
            self.assertIn('TestHung', str(caught.exception))
            self.assertIn('timed out', str(caught.exception))
            self.assertTrue(any(e.get('Action') == 'run' and e.get('Test') == 'TestHung'
                                for e in caught.exception.events))
            # The child would create this marker if it survived the process-group kill.
            time.sleep(3.2)
            self.assertFalse(marker.exists())

    def test_progressing_shard_can_outlive_the_stall_deadline(self):
        parent = os.environ.get('TMPDIR') or os.environ['YOLO_DURABLE_DIR']
        with tempfile.TemporaryDirectory(dir=parent) as temporary:
            root = Path(temporary)
            binary = root / 'fake-test'
            binary.write_text('''#!/usr/bin/env python3
import time
for name in ('TestOne', 'TestTwo', 'TestThree'):
 print('=== RUN   ' + name, flush=True)
 time.sleep(0.8)
 print('--- PASS: ' + name + ' (0.80s)', flush=True)
print('PASS', flush=True)
''')
            binary.chmod(0o755)
            events = shards.run_shard('fixture', root, binary,
                                      ['TestOne', 'TestTwo', 'TestThree'], 0,
                                      root / 'scratch', '0', [], wall_timeout=2,
                                      lock_dir=root / 'locks')
            self.assertEqual({e.get('Test') for e in events if e.get('Action') == 'pass' and e.get('Test')},
                             {'TestOne', 'TestTwo', 'TestThree'})

    def test_cli_reports_stall_and_preserves_completed_events(self):
        parent = os.environ.get('TMPDIR') or os.environ['YOLO_DURABLE_DIR']
        with tempfile.TemporaryDirectory(dir=parent) as temporary:
            root = Path(temporary)
            (root / 'go.mod').write_text('module fixture\n\ngo 1.23\n')
            (root / 'fixture_test.go').write_text('''package fixture
import ("testing"; "time")
func TestPass(t *testing.T) {}
func TestHung(t *testing.T) { for { time.Sleep(time.Second) } }
''')
            events = root / 'events.jsonl'
            result = subprocess.run(['python3', str(RUNNER), str(root), '-k', '1',
                                     '--shard-timeout', '2', '--events', str(events),
                                     # A private slot pool: the machine-wide one is held by
                                     # every other shard run, and waiting on it is not the
                                     # stall this test measures.
                                     '--lock-dir', str(root / 'locks')],
                                    capture_output=True, text=True, timeout=60)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn('fixture shard 0 timed out', result.stderr)
            self.assertIn('running: TestHung', result.stderr)
            records = [json.loads(line) for line in events.read_text().splitlines()]
            self.assertTrue(any(e.get('Test') == 'TestPass' and e.get('Action') == 'pass'
                                for e in records))
            self.assertTrue(any(e.get('Test') == 'TestHung' and e.get('Action') == 'run'
                                for e in records))


if __name__ == '__main__':
    unittest.main()
