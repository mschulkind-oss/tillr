#!/usr/bin/env python3
"""Liveness regressions for the sharded Go runner."""
import importlib.util
import json
import os
from pathlib import Path
import select
import signal
import subprocess
import sys
import tempfile
import threading
import time
import types
import unittest
from unittest.mock import patch

RUNNER = Path(__file__).resolve().parents[1] / 'go-test-shards.py'
spec = importlib.util.spec_from_file_location('shards', RUNNER)
shards = importlib.util.module_from_spec(spec)
spec.loader.exec_module(shards)


class ShardLivenessTests(unittest.TestCase):
    def _enable_slot_limits(self):
        limit_patch = patch.object(shards, 'ENFORCE_SLOT_LIMITS', True)
        limit_patch.start()
        self.addCleanup(limit_patch.stop)

    @staticmethod
    def _runner_with_limits_cmd(*args):
        bootstrap = (
            'import importlib.util, sys; '
            f'spec = importlib.util.spec_from_file_location("go_test_shards", {str(RUNNER)!r}); '
            'mod = importlib.util.module_from_spec(spec); '
            'spec.loader.exec_module(mod); '
            'mod.ENFORCE_SLOT_LIMITS = True; '
            f'sys.argv = [{str(RUNNER)!r}, *sys.argv[1:]]; '
            'sys.exit(mod.main())'
        )
        return [sys.executable, '-c', bootstrap, *args]

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
            liveness_deadline = real_monotonic() + 60
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
                                     lock_dir=root / 'locks', progress=_ClockDriver())
            self.assertIn('fixture shard 2', str(caught.exception))
            self.assertIn('running: TestHung', str(caught.exception))
            self.assertIn('timed out', str(caught.exception))
            self.assertTrue(caught.exception.timed_out)
            self.assertTrue(any(e.get('Action') == 'run' and e.get('Test') == 'TestHung'
                                for e in caught.exception.events))
            # The ready child holds the FIFO open indefinitely. EOF proves the
            # process-group kill closed its descriptor and stopped it.
            readable, _, _ = select.select([child_fd], [], [], 3.2)
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
            liveness_deadline = real_monotonic() + 60.0
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
                                          lock_dir=root / 'locks',
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

    def test_proof_caller_gets_reserved_slot_while_ordinary_share_is_held(self):
        self._enable_slot_limits()
        parent = os.environ.get('TMPDIR') or os.environ['YOLO_DURABLE_DIR']
        with tempfile.TemporaryDirectory(dir=parent) as temporary:
            lock_dir = Path(temporary) / 'locks'
            ordinary_ready = threading.Barrier(3)
            release_ordinary = threading.Event()
            ordinary_waiting = threading.Event()
            ordinary_acquired = threading.Event()
            ordinary_notices = []
            proof_notices = []

            def hold_ordinary(run_name):
                try:
                    with shards.acquire_shard_slot(lock_dir, 2, proof_limit=1,
                                                   slot_class='ordinary', holder=run_name):
                        ordinary_ready.wait(timeout=10)
                        assert release_ordinary.wait(timeout=30)
                except BaseException:
                    ordinary_ready.abort()
                    raise

            holders = [
                threading.Thread(target=hold_ordinary, args=('run-worker-a',)),
                threading.Thread(target=hold_ordinary, args=('run-worker-b',)),
            ]
            for thread in holders:
                thread.start()
            ordinary_ready.wait(timeout=30)

            def waiter():
                def on_wait(message):
                    ordinary_notices.append(message)
                    ordinary_waiting.set()
                with shards.acquire_shard_slot(lock_dir, 2, on_wait, proof_limit=1,
                                               slot_class='ordinary', holder='run-worker-c'):
                    ordinary_acquired.set()

            waiter_thread = threading.Thread(target=waiter)
            waiter_thread.start()
            try:
                self.assertTrue(ordinary_waiting.wait(timeout=30))
                self.assertFalse(ordinary_acquired.is_set())
                self.assertEqual(len(ordinary_notices), 1)
                notice = ordinary_notices[0]
                self.assertIn('waiting for a test slot', notice)
                self.assertIn(str(lock_dir), notice)
                self.assertIn('2 holder(s) across 2 run(s)', notice)
                self.assertIn('run-worker-a (1)', notice)
                self.assertIn('run-worker-b (1)', notice)

                # With every ordinary slot still held, a proof-class caller gets its
                # reserved slot without waiting while the ordinary waiter stays blocked.
                with shards.acquire_shard_slot(lock_dir, 2, proof_notices.append,
                                               proof_limit=1, slot_class='proof',
                                               holder='trunk-proof'):
                    self.assertFalse(ordinary_acquired.is_set())
                self.assertEqual(proof_notices, [])
                self.assertFalse(ordinary_acquired.is_set())
            finally:
                release_ordinary.set()
                waiter_thread.join(timeout=30)
                for thread in holders:
                    thread.join(timeout=30)
            self.assertTrue(ordinary_acquired.is_set())

    def test_waiting_proof_caller_takes_freed_ordinary_slot_ahead_of_waiting_worker(self):
        self._enable_slot_limits()
        parent = os.environ.get('TMPDIR') or os.environ['YOLO_DURABLE_DIR']
        with tempfile.TemporaryDirectory(dir=parent) as temporary:
            lock_dir = Path(temporary) / 'locks'
            ordinary_held = threading.Event()
            proof_held = threading.Event()
            release_ordinary = threading.Event()
            release_proof = threading.Event()
            worker_waiting = threading.Event()
            proof_waiting = threading.Event()
            worker_acquired = threading.Event()
            proof_acquired = threading.Event()
            allow_proof_exit = threading.Event()

            def hold_slot(slot_class, holder_name, held_event, release_event, errors):
                try:
                    with shards.acquire_shard_slot(lock_dir, 1, proof_limit=1,
                                                   slot_class=slot_class, holder=holder_name):
                        held_event.set()
                        assert release_event.wait(timeout=30)
                except BaseException as exc:
                    errors.append(exc)
                    held_event.set()
                    raise

            holder_errors = []
            ord_holder = threading.Thread(
                target=hold_slot,
                args=('ordinary', 'run-worker-1', ordinary_held, release_ordinary, holder_errors))
            prf_holder = threading.Thread(
                target=hold_slot,
                args=('proof', 'trunk-proof', proof_held, release_proof, holder_errors))
            ord_holder.start()
            self.assertTrue(ordinary_held.wait(timeout=10))
            if holder_errors:
                raise holder_errors[0]
            prf_holder.start()
            self.assertTrue(proof_held.wait(timeout=10))
            if holder_errors:
                release_ordinary.set()
                ord_holder.join(timeout=10)
                raise holder_errors[0]

            def waiting_worker():
                with shards.acquire_shard_slot(lock_dir, 1, lambda _: worker_waiting.set(),
                                               proof_limit=1, slot_class='ordinary',
                                               holder='run-worker-2'):
                    worker_acquired.set()

            def waiting_proof():
                with shards.acquire_shard_slot(lock_dir, 1, lambda _: proof_waiting.set(),
                                               proof_limit=1, slot_class='proof',
                                               holder='gate:run-cand-1'):
                    proof_acquired.set()
                    assert allow_proof_exit.wait(timeout=30)

            worker_thread = threading.Thread(target=waiting_worker)
            proof_thread = threading.Thread(target=waiting_proof)
            worker_thread.start()
            self.assertTrue(worker_waiting.wait(timeout=30))
            proof_thread.start()
            try:
                self.assertTrue(proof_waiting.wait(timeout=30))
                # Free only the ordinary slot; the proof waiter must get it ahead of the worker.
                release_ordinary.set()
                ord_holder.join(timeout=30)
                self.assertTrue(proof_acquired.wait(timeout=30))
                self.assertFalse(worker_acquired.is_set())
                allow_proof_exit.set()
                proof_thread.join(timeout=30)
                self.assertTrue(worker_acquired.wait(timeout=30))
            finally:
                release_ordinary.set()
                allow_proof_exit.set()
                release_proof.set()
                for thread in (ord_holder, prf_holder, proof_thread, worker_thread):
                    thread.join(timeout=30)

    def test_cli_wait_names_holder_runs_and_proof_cli_runs_ahead_of_workers(self):
        self._enable_slot_limits()
        parent = os.environ.get('TMPDIR') or os.environ['YOLO_DURABLE_DIR']
        with tempfile.TemporaryDirectory(dir=parent) as temporary:
            root = Path(temporary)
            (root / 'go.mod').write_text('module fixture\n\ngo 1.23\n')
            (root / 'fixture_test.go').write_text(
                'package fixture\nimport "testing"\nfunc TestQuick(t *testing.T) {}\n'
            )
            durations = root / 'durations.json'
            durations.write_text('{}')
            lock_dir = root / 'locks'
            limit = shards.default_shard_limit()
            ready = threading.Barrier(limit + 1)
            release = threading.Event()

            def hold(idx):
                run_name = 'run-alpha' if idx == 0 else 'run-beta'
                try:
                    with shards.acquire_shard_slot(lock_dir, limit, slot_class='ordinary',
                                                   holder=run_name):
                        ready.wait(timeout=10)
                        assert release.wait(timeout=30)
                except BaseException:
                    ready.abort()
                    raise

            holders = [threading.Thread(target=hold, args=(i,)) for i in range(limit)]
            for thread in holders:
                thread.start()
            ready.wait(timeout=30)

            worker_env = dict(os.environ, TILLR_RUN_ID='run-waiter')
            worker_proc = subprocess.Popen(
                self._runner_with_limits_cmd(str(root), '-k', '1',
                                             '--durations', str(durations), '--lock-dir', str(lock_dir)),
                env=worker_env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
            )
            try:
                wait_line = worker_proc.stderr.readline()
                self.assertIn('waiting for a test slot', wait_line)
                self.assertIn('run-alpha (1)', wait_line)
                if limit >= 2:
                    self.assertIn(f'run-beta ({limit - 1})', wait_line)
                    self.assertIn(f'{limit} holder(s) across 2 run(s)', wait_line)
                self.assertIsNone(worker_proc.poll())

                proof_env = dict(
                    os.environ,
                    TILLR_GATE_SPANS_OUT=str(root / '.tillr/loop/run-cand-9/gate.spans.json'),
                )
                proof_env.pop('TILLR_RUN_ID', None)
                proof_res = subprocess.run(
                    self._runner_with_limits_cmd(str(root), '-k', '1', '--slot-class', 'proof',
                                                 '--durations', str(durations), '--lock-dir', str(lock_dir)),
                    env=proof_env, capture_output=True, text=True, timeout=60,
                )
                self.assertEqual(proof_res.returncode, 0, proof_res.stdout + proof_res.stderr)
                self.assertIn('1 tests passed', proof_res.stdout)
                self.assertNotIn('waiting for a test slot', proof_res.stderr)
                self.assertIsNone(worker_proc.poll())

                release.set()
                out, err = worker_proc.communicate(timeout=60)
                self.assertEqual(worker_proc.returncode, 0, out + err)
                self.assertIn('1 tests passed', out)
            finally:
                release.set()
                if worker_proc.poll() is None:
                    worker_proc.kill()
                    worker_proc.wait()
                for thread in holders:
                    thread.join(timeout=30)

    def test_sigkilled_holder_leaves_no_stale_slot_or_ghost_holder(self):
        self._enable_slot_limits()
        parent = os.environ.get('TMPDIR') or os.environ['YOLO_DURABLE_DIR']
        with tempfile.TemporaryDirectory(dir=parent) as temporary:
            lock_dir = Path(temporary) / 'locks'
            child = subprocess.Popen(
                [sys.executable, '-c',
                 'import importlib.util, pathlib, time\n'
                 f'spec = importlib.util.spec_from_file_location("shards", {str(RUNNER)!r})\n'
                 's = importlib.util.module_from_spec(spec); spec.loader.exec_module(s)\n'
                 's.ENFORCE_SLOT_LIMITS = True\n'
                 f'with s.acquire_shard_slot(pathlib.Path({str(lock_dir)!r}), 1, holder="run-crashed"):\n'
                 '    print("held", flush=True)\n'
                 '    time.sleep(60)\n'],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
            )
            try:
                self.assertEqual(child.stdout.readline().strip(), 'held')
                os.kill(child.pid, signal.SIGKILL)
                child.wait(timeout=30)
            finally:
                if child.poll() is None:
                    child.kill()
                    child.wait()
                child.stdout.close()
                child.stderr.close()

            self.assertEqual(shards.read_slot_holders(lock_dir), [])
            notices = []
            with shards.acquire_shard_slot(lock_dir, 1, notices.append, holder='run-next'):
                holders = shards.read_slot_holders(lock_dir)
                self.assertEqual([h.get('run') for h in holders], ['run-next'])
            self.assertEqual(notices, [])


if __name__ == '__main__':
    unittest.main()
