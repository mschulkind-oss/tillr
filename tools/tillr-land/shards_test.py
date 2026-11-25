#!/usr/bin/env python3
"""Liveness regressions for the sharded Go runner."""
import importlib.util
import json
import os
from pathlib import Path
import select
import subprocess
import sys
import tempfile
import time
import types
import unittest
from unittest.mock import patch

RUNNER = Path(__file__).resolve().parents[1] / 'go-test-shards.py'
HANG_GUARD = 300.0
spec = importlib.util.spec_from_file_location('shards', RUNNER)
shards = importlib.util.module_from_spec(spec)
spec.loader.exec_module(shards)


class ShardLivenessTests(unittest.TestCase):
    def test_stalled_test_is_named_and_process_group_is_killed(self):
        parent = os.environ.get('TMPDIR') or os.environ['YOLO_DURABLE_DIR']
        with tempfile.TemporaryDirectory(dir=parent) as temporary:
            root = Path(temporary)
            binary = root / 'fake-test'
            child_fifo = root / 'child-fifo'
            os.mkfifo(child_fifo)
            child_fd = os.open(child_fifo, os.O_RDONLY | os.O_NONBLOCK)
            self.addCleanup(os.close, child_fd)
            child_code = '''import sys, time
with open(sys.argv[1], 'wb', buffering=0):
    print('ready', flush=True)
    while True:
        time.sleep(60)
'''
            binary.write_text('''#!/usr/bin/env python3
import subprocess, sys, time
# Reproduce startup slower than the shard's one-second stall deadline.
time.sleep(1.2)
child = subprocess.Popen([sys.executable, '-c', %r, %r], stdout=subprocess.PIPE)
assert child.stdout.readline() == b'ready\\n'
print('=== RUN   TestHung', flush=True)
while True:
 print('still spinning', flush=True)
 time.sleep(0.05)
''' % (child_code, str(child_fifo)))
            binary.chmod(0o755)

            real_monotonic = time.monotonic
            liveness_deadline = real_monotonic() + HANG_GUARD
            now = [0.0]
            started = [False]
            real_loads = json.loads

            def observe_event(*args, **kwargs):
                event = real_loads(*args, **kwargs)
                if event.get('Action') == 'run' and event.get('Test') == 'TestHung':
                    started[0] = True
                return event

            class _ClockDriver:
                interval = 0.02

                @staticmethod
                def maybe_publish():
                    if real_monotonic() > liveness_deadline:
                        self.fail('liveness guard expired waiting for TestHung to start')
                    if started[0]:
                        # run_shard has seen the run event and reset its deadline.
                        now[0] = 2.0

            fake_time = types.SimpleNamespace(monotonic=lambda: now[0],
                                              time=time.time, sleep=time.sleep)
            with patch.object(shards, 'time', fake_time), patch.object(shards.json, 'loads', observe_event):
                with self.assertRaises(shards.ShardError) as caught:
                    shards.run_shard('fixture', root, binary, ['TestHung'], 2,
                                     root / 'scratch', '0', [], wall_timeout=1,
                                     progress=_ClockDriver())
            self.assertIn('fixture shard 2', str(caught.exception))
            self.assertIn('running: TestHung', str(caught.exception))
            self.assertIn('timed out', str(caught.exception))
            self.assertTrue(caught.exception.timed_out)
            self.assertTrue(any(e.get('Action') == 'run' and e.get('Test') == 'TestHung'
                                for e in caught.exception.events))
            # The ready child holds the FIFO open indefinitely. EOF proves the
            # process-group kill closed its descriptor and stopped it.
            readable, _, _ = select.select([child_fd], [], [], HANG_GUARD)
            self.assertTrue(readable, 'child still holds the FIFO open')
            self.assertEqual(os.read(child_fd, 1), b'')

    def test_progressing_shard_can_outlive_the_stall_deadline(self):
        parent = os.environ.get('TMPDIR') or os.environ['YOLO_DURABLE_DIR']
        with tempfile.TemporaryDirectory(dir=parent) as temporary:
            root = Path(temporary)
            names = ['TestOne', 'TestTwo', 'TestThree']
            gates = root / 'gates'
            gates.mkdir()
            gate_fds = {}
            for gate_name in ('start', '0', '1'):
                fifo = gates / gate_name
                os.mkfifo(fifo)
                fd = os.open(fifo, os.O_RDWR)
                self.addCleanup(os.close, fd)
                gate_fds[gate_name] = fd

            binary = root / 'fake-test'
            binary.write_text('''#!/usr/bin/env python3
import pathlib, sys
gates = pathlib.Path(sys.argv[0]).parent / 'gates'
with (gates / 'start').open('rb') as gate:
    gate.read(1)
for index, name in enumerate(('TestOne', 'TestTwo', 'TestThree')):
    print('=== RUN   ' + name, flush=True)
    if index > 0:
        with (gates / str(index - 1)).open('rb') as gate:
            gate.read(1)
    print('--- PASS: ' + name + ' (0.80s)', flush=True)
print('PASS', flush=True)
''')
            binary.chmod(0o755)

            real_monotonic = time.monotonic
            liveness_deadline = real_monotonic() + HANG_GUARD
            now = [0.0]
            completed = []
            phase = [0]
            gate_order = ('start', '0', '1')

            class _ClockDriver:
                interval = 0.02

                @staticmethod
                def record_test(detail):
                    completed.append(detail)

                @staticmethod
                def maybe_publish():
                    if real_monotonic() > liveness_deadline:
                        self.fail('liveness guard expired waiting for fake-test progress')
                    step = phase[0] // 2
                    if step >= len(gate_order):
                        return
                    if phase[0] % 2 == 0:
                        if step == 0 or len(completed) >= step:
                            # Advance the stall clock while fake-test is still blocked on its
                            # current gate so run_shard checks `remaining` before releasing it.
                            now[0] += 1.5
                            phase[0] += 1
                    else:
                        os.write(gate_fds[gate_order[step]], b'\n')
                        phase[0] += 1

            fake_time = types.SimpleNamespace(
                monotonic=lambda: now[0],
                time=time.time,
                sleep=time.sleep,
            )
            with patch.object(shards, 'time', fake_time):
                events = shards.run_shard('fixture', root, binary, names, 0,
                                          root / 'scratch', '0', [], wall_timeout=2,
                                          progress=_ClockDriver())
            self.assertEqual(phase[0], 2 * len(gate_order))
            self.assertEqual(now[0], 4.5)
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
            driver = root / 'run-cli.py'
            driver.write_text('''import importlib.util, json, sys, time, types
spec = importlib.util.spec_from_file_location('shards', sys.argv[1])
shards = importlib.util.module_from_spec(spec)
spec.loader.exec_module(shards)
now = [0.0]
saw_hung = [False]
real_loads = json.loads

def observe_event(*args, **kwargs):
    event = real_loads(*args, **kwargs)
    if event.get('Action') == 'run' and event.get('Test') == 'TestHung':
        saw_hung[0] = True
    return event

def fake_monotonic():
    if saw_hung[0]:
        now[0] += 3.0
    return now[0]

shards.time = types.SimpleNamespace(monotonic=fake_monotonic, time=time.time, sleep=time.sleep)
shards.json.loads = observe_event
sys.argv = [sys.argv[1]] + sys.argv[2:]
sys.exit(shards.main())
''')
            result = subprocess.run(['python3', str(driver), str(RUNNER), str(root), '-k', '1',
                                     '--shard-timeout', '2', '--events', str(events)],
                                    capture_output=True, text=True, timeout=HANG_GUARD)
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
