#!/usr/bin/env python3
"""Permanent runner regression tests; parity lists and plans the real drain suite without running it."""
from contextlib import redirect_stderr
import importlib.util
import io
import json
import os
import re
from pathlib import Path
import subprocess
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
RUNNER = ROOT / 'tools/go-test-shards.py'
spec = importlib.util.spec_from_file_location('shards', RUNNER)
shards = importlib.util.module_from_spec(spec)
# Loaded in setUpModule so the first run reproduces the missing runner.


def setUpModule():
    spec.loader.exec_module(shards)


class RunnerTests(unittest.TestCase):
    def setUp(self):
        parent = os.environ.get('TMPDIR') or os.environ.get('YOLO_DURABLE_DIR')
        if not parent:
            raise RuntimeError('tests require TMPDIR or YOLO_DURABLE_DIR, never /tmp')
        self.tmp = tempfile.TemporaryDirectory(dir=parent)
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        # Every runner these tests start, in this process or a child, takes its slot from a
        # pool of its own. The machine-wide pool is held by whatever else is running, and
        # waiting on it would make the suite's duration a measure of the machine's load.
        pool = patch.dict(os.environ, {shards.LOCK_DIR_ENV: str(self.root / 'default-locks')})
        pool.start()
        self.addCleanup(pool.stop)

    def test_package_evidence_distinguishes_shards_from_go_result_cache(self):
        target = self.root / 'packages.json'
        packages = [{'ImportPath': 'demo/test', 'TestGoFiles': ['x_test.go']},
                    {'ImportPath': 'demo/library'}]
        with patch.object(shards.subprocess, 'check_output', return_value='\n'.join(map(json.dumps, packages))), \
             patch.object(shards, 'global_git_config', return_value={}), \
             patch.object(shards, 'default_lock_dir', return_value=self.root), \
             patch.object(shards.subprocess, 'run', side_effect=subprocess.CalledProcessError(1, 'go')):
            with self.assertRaises(subprocess.CalledProcessError):
                shards.run(self.root, 1, {}, self.root, '0', evidence_out=target)
        self.assertEqual(json.loads(target.read_text()), [
            {'import_path': 'demo/test', 'test_cache': 'bypassed'},
            {'import_path': 'demo/library', 'test_cache': 'not-applicable'},
        ])

    def test_failed_package_list_keeps_complete_records_without_building(self):
        target = self.root / 'packages.json'
        listed = [{'ImportPath': 'demo/good', 'TestGoFiles': ['x_test.go']},
                  {'ImportPath': 'demo/broken'}]
        error = subprocess.CalledProcessError(1, ['go', 'list', '-json', './...'],
                                             output='\n'.join(map(json.dumps, listed)) + '\n{')
        with patch.object(shards.subprocess, 'check_output', side_effect=error) as discover, \
             patch.object(shards.subprocess, 'run') as compile_package:
            with self.assertRaises(subprocess.CalledProcessError) as refusal:
                shards.run(self.root, 1, {}, self.root, '0', evidence_out=target)
        self.assertIs(refusal.exception, error)
        discover.assert_called_once_with(['go', 'list', '-json', './...'], cwd=self.root, text=True)
        compile_package.assert_not_called()
        self.assertEqual(json.loads(target.read_text()), {'discovery': 'partial', 'packages': [
            {'import_path': package['ImportPath'], 'test_cache': 'not-started'} for package in listed
        ]})

    def test_package_evidence_write_error_does_not_fail_run(self):
        stderr = io.StringIO()
        with patch.object(shards.subprocess, 'check_output', return_value='{"ImportPath":"demo/library"}'), \
             patch.object(shards, 'global_git_config', return_value={}), redirect_stderr(stderr):
            self.assertEqual(shards.run(self.root, 1, {}, self.root, '0', evidence_out=self.root), [])
        self.assertIn('cannot write package evidence', stderr.getvalue())

    def test_named_environment_pool_replaces_the_machine_wide_one(self):
        self.assertEqual(shards.default_lock_dir(), (self.root / 'default-locks').resolve())
        with patch.dict(os.environ, {shards.LOCK_DIR_ENV: ''}):
            self.assertEqual(shards.default_lock_dir(),
                             shards.shared_cache_dir() / 'go-test-shards-locks')

    def fake(self, body):
        script = self.root / 'fake'
        script.write_text('#!/usr/bin/env python3\n' + body)
        script.chmod(0o755)
        return script

    def assert_shard_exit(self, binary, names, message):
        # Exercise the CLI exit contract with a real shard and test2json process.
        def run(*args):
            return shards.run_shard('fixture', self.root, binary, names, 0,
                                    self.root / 'scratch', '3600s')
        record = self.root / 'record.json'
        record.write_text('{}')
        stderr = io.StringIO()
        with patch.object(shards, 'run', side_effect=run), patch.object(
                shards.sys, 'argv', ['runner', str(self.root), '--durations', str(record)]), redirect_stderr(stderr):
            self.assertEqual(shards.main(), 1)
        self.assertRegex(stderr.getvalue(), 'fixture shard 0.*' + message)

    def start_hung_runner(self):
        """Start a runner process whose one shard writes its pid and then never returns.

        Returns (runner, shard pid). The shard's test binary has a child of its own, so the
        proof covers a descendant of the test binary and not only test2json.
        """
        pids = self.root / 'pids'
        pids.mkdir()
        binary = self.fake(
            "import os, subprocess, time, pathlib\n"
            "grand = subprocess.Popen(['sleep', '1000'])\n"
            f"pathlib.Path({str(pids / 'shard')!r} + '.part').write_text(f'{{os.getpid()}} {{grand.pid}}')\n"
            f"os.replace({str(pids / 'shard')!r} + '.part', {str(pids / 'shard')!r})\n"
            "time.sleep(1000)\n")
        driver = self.root / 'driver.py'
        driver.write_text(
            'import importlib.util, signal, sys\n'
            f'spec = importlib.util.spec_from_file_location("shards", {str(RUNNER)!r})\n'
            'm = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)\n'
            'm.install_signal_handlers()\n'
            'from pathlib import Path\n'
            f'm.run_shard("fixture", Path({str(self.root)!r}), Path({str(binary)!r}), ["TestHung"], 0, '
            f'Path({str(self.root / "driver-scratch")!r}), "0", wall_timeout=3600)\n')
        runner = subprocess.Popen(['python3', str(driver)], stderr=subprocess.PIPE, text=True)
        self.addCleanup(lambda: (runner.poll() is None and runner.kill(), runner.wait(),
                                 runner.stderr.close()))
        marker = pids / 'shard'
        while not marker.exists():
            # The shard writes the marker as soon as it starts; ending first is the failure.
            if runner.poll() is not None:
                self.fail('the runner ended before its shard started: ' + runner.stderr.read())
            time.sleep(0.01)
        return runner, [int(p) for p in marker.read_text().split()]

    def assert_gone(self, pids):
        # The shard is killed by the runner's end, which is certain to happen; the deadline
        # only turns a runner that leaves it alive into a failure rather than a hang.
        deadline = time.monotonic() + 120
        for pid in pids:
            while True:
                try:
                    state = Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()[0]
                except (FileNotFoundError, ProcessLookupError):
                    break
                # A zombie has ended; only its parent's wait is missing, and no wait is ours.
                if state == 'Z':
                    break
                if time.monotonic() > deadline:
                    os.kill(pid, 9)
                    self.fail(f'shard process {pid} outlived the runner')
                time.sleep(0.01)

    def test_a_killed_runner_leaves_no_shard_process_behind(self):
        runner, pids = self.start_hung_runner()
        runner.kill()
        runner.wait()
        self.assert_gone(pids)

    def test_a_terminated_runner_leaves_no_shard_process_behind(self):
        runner, pids = self.start_hung_runner()
        runner.terminate()
        runner.wait()
        self.assert_gone(pids)

    def test_a_finished_shard_leaves_no_descendant_behind(self):
        marker = self.root / 'grand'
        binary = self.fake(
            "import subprocess, pathlib\n"
            f"grand = subprocess.Popen(['sleep', '1000'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)\n"
            f"pathlib.Path({str(marker)!r}).write_text(str(grand.pid))\n"
            "print('=== RUN   TestLeaks'); print('--- PASS: TestLeaks (0.00s)'); print('PASS')\n")
        shards.run_shard('fixture', self.root, binary, ['TestLeaks'], 0, self.root / 'scratch', '3600s')
        self.assert_gone([int(marker.read_text())])

    def test_absent_global_config_is_valid(self):
        binary = self.fake("print('=== RUN   TestWorks'); print('--- PASS: TestWorks (0.00s)'); print('PASS')\n")
        with patch.dict(os.environ, GIT_CONFIG_GLOBAL=str(self.root / 'absent')):
            events = shards.run_shard('fixture', self.root, binary, ['TestWorks'], 0,
                                      self.root / 'scratch', '3600s')
        self.assertTrue(any(e.get('Test') == 'TestWorks' and e.get('Action') == 'pass'
                            for e in events))

    def test_malformed_global_config_remains_an_error(self):
        config = self.root / 'invalid'
        config.write_text('[unclosed\n')
        with patch.dict(os.environ, GIT_CONFIG_GLOBAL=str(config)):
            with self.assertRaises(subprocess.CalledProcessError):
                shards.global_git_config()

    def test_scratch_preparation_failure_names_shard(self):
        binary = self.fake("print('PASS')\n")
        with patch.object(Path, 'mkdir', side_effect=OSError('scratch disk full')):
            with self.assertRaisesRegex(shards.ShardError, 'fixture shard 0.*scratch disk full'):
                shards.run_shard('fixture', self.root, binary, ['TestMissing'], 0,
                                 self.root / 'scratch', '3600s')

    def test_failure_names_shard(self):
        binary = self.fake("print('=== RUN   TestFail'); print('--- FAIL: TestFail (0.00s)'); print('FAIL'); raise SystemExit(1)\n")
        self.assert_shard_exit(binary, ['TestFail'], 'failed')

    def test_failure_retains_go_timeout_stack_after_an_ordinary_failure(self):
        binary = self.fake(
            "print('\\x16=== RUN   TestFail'); print('\\x16--- FAIL: TestFail (0.00s)')\n"
            "print('\\x16=== RUN   TestHung')\n"
            "print('panic: test timed out after 10m0s\\nrunning tests:\\n\\tTestHung (10m0s)\\n\\n'\n"
            "      'goroutine 1 [running]:')\n"
            "print('\\x16=== NAME  ')\n"  # Package exit, without a test-level FAIL for TestHung.
            "raise SystemExit(2)\n")
        with self.assertRaises(shards.ShardError) as caught:
            shards.run_shard('fixture', self.root, binary, ['TestFail', 'TestHung'], 0,
                             self.root / 'scratch', '0')
        self.assertIn('failed (exit 1): TestFail', str(caught.exception))
        self.assertIn('panic: test timed out after 10m0s\nrunning tests:\n\tTestHung (10m0s)\n\n'
                      'goroutine 1 [running]:', str(caught.exception))

    def test_cannot_start_names_shard(self):
        self.assert_shard_exit(self.root / 'missing', ['TestMissing'], 'cannot start')

    def test_missing_completion_names_shard(self):
        binary = self.fake("print('PASS')\n")
        self.assert_shard_exit(binary, ['TestMissing'], 'TestMissing')

    def test_packages_run_their_shards_side_by_side(self):
        packages = [{'ImportPath': name, 'Dir': str(self.root), 'TestGoFiles': ['x_test.go']}
                    for name in ['one', 'two']]
        barrier = threading.Barrier(4)
        def shard(*args):
            barrier.wait(timeout=30)  # Liveness guard; never an elapsed-time assertion.
            return [{'Package': args[0], 'Test': args[3][0], 'Action': 'pass'}]
        with patch.object(shards.subprocess, 'check_output', side_effect=[
                '\n'.join(map(json.dumps, packages)), 'TestA\nTestB\n', 'TestA\nTestB\n']), \
                patch.object(shards.subprocess, 'run') as compile_test, \
                patch.object(shards, 'global_git_config', return_value=[]), \
                patch.object(shards, 'run_shard', side_effect=shard):
            events = shards.run(self.root, 2,
                                {p: {'TestA': 1, 'TestB': 1} for p in ['one', 'two']},
                                self.root, '3600s')
        self.assertEqual(len(events), 4)
        self.assertEqual(compile_test.call_count, 2)  # One build per package, not per shard.
        for call in compile_test.call_args_list:
            self.assertEqual(call.args[0][:3], ['go', 'test', '-c'])

    def test_one_worker_keeps_known_and_unknown(self):
        self.assertEqual(shards.assign(['TestKnown', 'TestNew'], {'TestKnown': 1}, 1),
                         [['TestKnown', 'TestNew']])

    def test_balancing_reserves_fallback(self):
        groups = shards.assign(['TestSlow', 'TestFast', 'TestNew'],
                               {'TestSlow': 10, 'TestFast': 1}, 3)
        self.assertEqual(groups[-1], ['TestNew'])
        self.assertEqual(sorted(sum(groups, [])), ['TestFast', 'TestNew', 'TestSlow'])

    def test_unknown_test_and_isolation(self):
        module = self.root / 'module'
        module.mkdir()
        (module / 'go.mod').write_text('module fixture\n\ngo 1.23\n')
        (module / 'fixture_test.go').write_text('''package fixture
import ("testing"; "os"; "time"; "strings")
func check(t *testing.T) {
 if os.Getenv("YOLO_DURABLE_DIR") != os.Getenv("TMPDIR") { t.Fatal("durable scratch is not isolated") }
 if e := os.Chdir(os.Getenv("TMPDIR")); e != nil { t.Fatal(e) }
 p := "same-name"
 if _, e := os.Stat(p); !os.IsNotExist(e) { t.Fatal("another shard's file", e) }
 if e := os.WriteFile(p, []byte(t.Name()), 0600); e != nil { t.Fatal(e) }
 g := os.Getenv("GIT_CONFIG_GLOBAL")
 if b, e := os.ReadFile(g); e != nil || !strings.Contains(string(b), "untouched") || strings.Contains(string(b), "[include]") { t.Fatal("inherited config lost or shared", e) }
 if e := os.WriteFile(g, []byte(t.Name()), 0600); e != nil { t.Fatal(e) }
 time.Sleep(50*time.Millisecond)
}
func TestKnown(t *testing.T) { check(t) }
func TestNew(t *testing.T) { check(t) }
''')
        durations = self.root / 'durations.json'
        durations.write_text(json.dumps({'fixture': {'TestKnown': 1}}))
        report = self.root / 'events.jsonl'
        global_config = self.root / 'inherited.gitconfig'
        included = self.root / 'included.gitconfig'
        included.write_text('[fixture]\nseed = untouched\n')
        inherited = '[include]\npath = included.gitconfig\n'
        global_config.write_text(inherited)
        result = subprocess.run(['python3', str(RUNNER), str(module), '-k', '2',
                                 '--durations', str(durations), '--events', str(report)],
                                env=dict(os.environ, GIT_CONFIG_GLOBAL=str(global_config)),
                                capture_output=True, text=True)
        self.assertEqual(global_config.read_text(), inherited)
        self.assertEqual(included.read_text(), '[fixture]\nseed = untouched\n')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        passed = {e['Test'] for e in map(json.loads, report.read_text().splitlines())
                  if e.get('Action') == 'pass' and e.get('Test')}
        self.assertEqual(passed, {'TestKnown', 'TestNew'})

    def test_package_without_test_names_still_runs_testmain(self):
        module = self.root / 'module'
        module.mkdir()
        (module / 'go.mod').write_text('module fixture\n\ngo 1.23\n')
        (module / 'fixture_test.go').write_text('''package fixture
import ("testing"; "flag"; "os")
func BenchmarkOnly(b *testing.B) {}
func TestMain(m *testing.M) {
 flag.Parse()
 if flag.Lookup("test.list").Value.String() != "" { os.Exit(m.Run()) }
 os.Exit(1)
}
''')
        baseline = subprocess.run(['go', 'test', './...', '-count=1'], cwd=module,
                                  capture_output=True, text=True)
        self.assertNotEqual(baseline.returncode, 0, baseline.stdout + baseline.stderr)
        durations = self.root / 'durations.json'
        durations.write_text('{}')
        result = subprocess.run(['python3', str(RUNNER), str(module), '--durations', str(durations)],
                                capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertRegex(result.stderr, 'fixture shard 0.*failed')

    def test_discovery_uses_private_scratch_and_git_config(self):
        module = self.root / 'module'
        module.mkdir()
        (module / 'go.mod').write_text('module fixture\n\ngo 1.23\n')
        (module / 'fixture_test.go').write_text('''package fixture
import ("testing"; "os"; "strings")
func TestWorks(t *testing.T) {}
func TestMain(m *testing.M) {
 scratch := os.Getenv("TMPDIR")
 config := os.Getenv("GIT_CONFIG_GLOBAL")
 if !strings.Contains(scratch, "go-shards-") || !strings.HasPrefix(config, scratch+"/") {
  os.Stderr.WriteString("TestMain needs private discovery scratch and config\\n")
  os.Exit(1)
 }
 os.Exit(m.Run())
}
''')
        durations = self.root / 'durations.json'
        durations.write_text('{}')
        result = subprocess.run(['python3', str(RUNNER), str(module), '--durations', str(durations)],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_failed_run_still_writes_events(self):
        binary = self.fake("print('=== RUN   TestPass'); print('--- PASS: TestPass (0.00s)'); print('=== RUN   TestFail'); print('--- FAIL: TestFail (0.00s)'); print('FAIL'); raise SystemExit(1)\n")
        def run(*args):
            return shards.run_shard('fixture', self.root, binary, ['TestPass', 'TestFail'], 0,
                                    self.root / 'scratch', '0')
        record = self.root / 'durations.json'
        record.write_text('{}')
        events = self.root / 'events.jsonl'
        with patch.object(shards, 'run', side_effect=run), patch.object(
                shards.sys, 'argv', ['runner', str(self.root), '--durations', str(record),
                                    '--events', str(events)]), redirect_stderr(io.StringIO()):
            self.assertEqual(shards.main(), 1)
        self.assertTrue(events.exists(), 'failed run lost its passing and failing names')
        passed = {e.get('Test') for e in map(json.loads, events.read_text().splitlines())
                  if e.get('Action') == 'pass'}
        self.assertIn('TestPass', passed)

    def test_cli_has_no_nested_wall_deadline(self):
        record = self.root / 'durations.json'
        record.write_text('{}')
        with patch.object(shards, 'run', return_value=[]) as run, patch.object(
                shards.sys, 'argv', ['runner', str(self.root), '--durations', str(record)]):
            self.assertEqual(shards.main(), 0)
        self.assertEqual(run.call_args.args[-1], '0')

    def test_drain_name_parity(self):
        # Lists and plans the real drain suite and runs none of it: the rolling check
        # go-tool for tools/tillr-drain is the proof's one run of that suite.
        module = ROOT / 'tools/tillr-drain'
        native_command = ['go', 'test', '-json', '-list', '.', './...']
        planned = []
        real_popen = subprocess.Popen

        def refuse_a_test_run(command, *args, **kwargs):
            words = [str(word) for word in command]
            self.assertTrue(
                words == native_command
                or words == ['go', 'list', '-json', './...']
                or (words[:4] == ['go', 'test', '-c', '-o'] and len(words) == 6)
                or (words[0] == 'git' and words[1] == 'config')
                or words[1:] == ['-test.list=.', '-test.timeout=0'], words)
            return real_popen(command, *args, **kwargs)

        def record(package, cwd, binary, names, *rest):
            planned.extend((package, name) for name in names)
            return []

        with patch.object(shards, 'run_shard', record), \
                patch.object(subprocess, 'Popen', refuse_a_test_run):
            listing = subprocess.run(native_command, cwd=module, capture_output=True, text=True)
            self.assertEqual(listing.returncode, 0, listing.stdout + listing.stderr)
            shards.run(module, 6, shards.load_durations(shards.DEFAULT_DURATIONS),
                       self.root / 'parity', '0')
        native = set()
        for event in map(json.loads, listing.stdout.splitlines()):
            name = event.get('Output', '').strip()
            if event.get('Action') == 'output' and re.match(r'^(Test|Example|Fuzz)\w*$', name):
                native.add((event['Package'], name))
        self.assertTrue(native, 'Go listed no drain tests')
        self.assertEqual(len(planned), len(set(planned)))
        self.assertEqual(set(planned), native)

    def test_default_shard_limit_scales_with_cores(self):
        for cores, expected in [(32, 12), (16, 6), (8, 3), (4, 1), (2, 1), (1, 1), (None, 1)]:
            with patch.object(os, 'cpu_count', return_value=cores):
                self.assertEqual(shards.default_shard_limit(), expected)
                slots = (expected + shards.default_proof_shard_limit()
                         + shards.default_gate_shard_limit())
                self.assertLessEqual(slots * shards.TESTS_PER_SLOT, cores or 1)

    def test_default_pool_bounds_test_parallelism_on_32_cores(self):
        binary = self.fake(
            "import sys\n"
            "assert '-test.parallel=1' in sys.argv, sys.argv\n"
            "print('=== RUN   TestWorks')\n"
            "print('--- PASS: TestWorks (0.00s)')\n"
            "print('PASS')\n"
        )
        with patch.object(os, 'cpu_count', return_value=32):
            ordinary = shards.default_shard_limit()
            proof = shards.default_proof_shard_limit()
            gate = shards.default_gate_shard_limit()
            self.assertEqual(shards.SLOT_CLASS_GATE, 'gate')
            self.assertEqual(shards.SLOT_CLASSES, ('ordinary', 'proof', 'gate'))
            self.assertEqual((ordinary, proof, gate), (12, 4, 6))
            self.assertEqual((ordinary + proof + gate) * shards.TESTS_PER_SLOT, 22)
            shards.run_shard('fake', self.root, binary, ['TestWorks'], 0,
                             self.root / 'scratch', '0', [])

    def test_shared_cache_dir_prefers_gocache_then_xdg_then_home(self):
        custom = self.root / 'custom-gocache'
        xdg = self.root / 'xdg-cache'
        home = self.root / 'home'
        with patch.dict(os.environ, {'GOCACHE': str(custom), 'XDG_CACHE_HOME': str(xdg), 'HOME': str(home)}):
            self.assertEqual(shards.shared_cache_dir(), custom.resolve())
        with patch.dict(os.environ, {'GOCACHE': 'off', 'XDG_CACHE_HOME': str(xdg), 'HOME': str(home)}):
            self.assertEqual(shards.shared_cache_dir(), (xdg / 'go-build').resolve())
        clean_env = {k: v for k, v in os.environ.items() if k not in ('GOCACHE', 'XDG_CACHE_HOME')}
        clean_env['HOME'] = str(home)
        with patch.dict(os.environ, clean_env, clear=True):
            self.assertEqual(shards.shared_cache_dir(), (home / '.cache' / 'go-build').resolve())

    def test_waiting_runners_announce_and_never_exceed_limit(self):
        # Deterministic: no sleeps decide the outcome. Holders release only after every
        # surplus runner has announced that it is waiting, so an over-admission would
        # show as a peak above the limit.
        limit, runners = 3, 8
        lock_dir = self.root / 'pool'
        state = threading.Lock()
        active = peak = 0
        notices = []
        all_waiting = threading.Event()

        def notify(message):
            with state:
                notices.append(message)
                if len(notices) == runners - limit:
                    all_waiting.set()

        def runner():
            nonlocal active, peak
            with shards.acquire_shard_slot(lock_dir, limit, notify):
                with state:
                    active += 1
                    peak = max(peak, active)
                assert all_waiting.wait(60)
                with state:
                    active -= 1

        threads = [threading.Thread(target=runner) for _ in range(runners)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join(120)
            self.assertFalse(thread.is_alive())
        self.assertEqual(peak, limit)
        self.assertEqual(len(notices), runners - limit)
        self.assertTrue(all('waiting for a test slot' in n and str(lock_dir) in n for n in notices))

    def test_two_invocations_never_exceed_limit(self):
        limit = shards.default_shard_limit()
        first_count = max(1, limit - 1)
        second_count = 2
        lock_dir = self.root / 'locks'
        markers = self.root / 'markers'
        markers.mkdir()
        active_file = markers / 'active.txt'
        max_file = markers / 'max.txt'
        active_file.write_text('0')
        max_file.write_text('0')
        lock_file = markers / 'state.lock'
        release_file = markers / 'release'

        def write_module(name, prefix, count):
            mod = self.root / name
            mod.mkdir()
            (mod / 'go.mod').write_text(f'module {name}\n\ngo 1.23\n')
            tests_src = '\n'.join(f'func Test{prefix}{i}(t *testing.T) {{ hold(t) }}' for i in range(count))
            (mod / 'fixture_test.go').write_text(f'''package {name}
import (
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "syscall"
    "testing"
    "time"
)

func withLock(t *testing.T, fn func()) {{
    t.Helper()
    fd, err := syscall.Open({json.dumps(str(lock_file))}, syscall.O_CREAT|syscall.O_RDWR, 0666)
    if err != nil {{ t.Fatal(err) }}
    defer syscall.Close(fd)
    if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {{ t.Fatal(err) }}
    defer syscall.Flock(fd, syscall.LOCK_UN)
    fn()
}}

func hold(t *testing.T) {{
    withLock(t, func() {{
        raw, err := os.ReadFile({json.dumps(str(active_file))})
        if err != nil {{ t.Fatal(err) }}
        cur, err := strconv.Atoi(strings.TrimSpace(string(raw)))
        if err != nil {{ t.Fatal(err) }}
        cur++
        if err := os.WriteFile({json.dumps(str(active_file))}, []byte(strconv.Itoa(cur)), 0600); err != nil {{ t.Fatal(err) }}
        mraw, err := os.ReadFile({json.dumps(str(max_file))})
        if err != nil {{ t.Fatal(err) }}
        m, err := strconv.Atoi(strings.TrimSpace(string(mraw)))
        if err != nil {{ t.Fatal(err) }}
        if cur > m {{
            if err := os.WriteFile({json.dumps(str(max_file))}, []byte(strconv.Itoa(cur)), 0600); err != nil {{ t.Fatal(err) }}
        }}
    }})
    marker := filepath.Join({json.dumps(str(markers))}, "started_{prefix}_"+t.Name())
    if err := os.WriteFile(marker, []byte("1"), 0600); err != nil {{ t.Fatal(err) }}
    for {{
        if _, err := os.Stat({json.dumps(str(release_file))}); err == nil {{
            break
        }}
        time.Sleep(10 * time.Millisecond)
    }}
    withLock(t, func() {{
        raw, err := os.ReadFile({json.dumps(str(active_file))})
        if err != nil {{ t.Fatal(err) }}
        cur, err := strconv.Atoi(strings.TrimSpace(string(raw)))
        if err != nil {{ t.Fatal(err) }}
        cur--
        if err := os.WriteFile({json.dumps(str(active_file))}, []byte(strconv.Itoa(cur)), 0600); err != nil {{ t.Fatal(err) }}
    }})
}}

{tests_src}
''')
            return mod

        mod_a = write_module('moda', 'A', first_count)
        mod_b = write_module('modb', 'B', second_count)
        durations = self.root / 'durations.json'
        durations.write_text('{}')

        def wait_until(predicate, detail):
            deadline = time.monotonic() + 60
            while not predicate():
                if time.monotonic() > deadline:
                    self.fail(f'liveness guard expired waiting for {detail}')
                time.sleep(0.02)

        proc_a = subprocess.Popen(
            ['python3', str(RUNNER), str(mod_a), '-k', str(first_count), '--durations', str(durations),
             '--lock-dir', str(lock_dir)],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            wait_until(lambda: len(list(markers.glob('started_A_*'))) == first_count or proc_a.poll() is not None,
                       'first invocation to occupy its slots')
            if proc_a.poll() is not None:
                self.fail(f'first invocation exited early: {proc_a.stderr.read()}')

            proc_b = subprocess.Popen(
                ['python3', str(RUNNER), str(mod_b), '-k', str(second_count), '--durations', str(durations),
                 '--lock-dir', str(lock_dir)],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                if limit >= 2:
                    wait_until(lambda: len(list(markers.glob('started_B_*'))) >= 1 or proc_b.poll() is not None,
                               'second invocation to take the remaining slot')
                    if proc_b.poll() is not None:
                        self.fail(f'second invocation exited early: {proc_b.stderr.read()}')
                    self.assertEqual(len(list(markers.glob('started_B_*'))), 1,
                                     'second invocation oversubscribed past the machine-wide limit')
                    self.assertEqual(int(active_file.read_text().strip()), limit)
                release_file.write_text('go')
                out_a, err_a = proc_a.communicate(timeout=60)
                out_b, err_b = proc_b.communicate(timeout=60)
                self.assertEqual(proc_a.returncode, 0, out_a + err_a)
                self.assertEqual(proc_b.returncode, 0, out_b + err_b)
            finally:
                if proc_b.poll() is None:
                    proc_b.kill()
                    proc_b.wait()
        finally:
            if not release_file.exists():
                release_file.write_text('go')
            if proc_a.poll() is None:
                proc_a.kill()
                proc_a.wait()

        peak = int(max_file.read_text().strip())
        self.assertLessEqual(peak, limit)
        self.assertEqual(peak, limit)
        self.assertEqual(len(list(markers.glob('started_B_*'))), second_count)

    def test_slot_wait_spans_append_fail_open_to_trace_dir(self):
        lock_dir = self.root / 'trace-pool'
        trace_dir = self.root / 'trace-dir'
        trace_dir.mkdir()
        span_file = trace_dir / 'slot-waits.jsonl'

        # 1. Immediate acquisition writes waited_ms == 0, acquired == True, slot-0.
        with patch.dict(os.environ, {'TILLR_TRACE_DIR': str(trace_dir)}):
            with shards.acquire_shard_slot(lock_dir, 1, holder='run-imm', label='pkg shard 1'):
                pass

        # 2. Contended acquisition writes waited_ms > 0, acquired == True; proof fallback writes proof-0.
        entered_wait = threading.Event()
        release_holder = threading.Event()

        def hold_first():
            with shards.acquire_shard_slot(lock_dir, 1, holder='run-holder'):
                assert release_holder.wait(60)

        holder_thread = threading.Thread(target=hold_first)
        with patch.dict(os.environ, {'TILLR_TRACE_DIR': ''}):
            holder_thread.start()
        # Wait until slot-0.holder.json is live before starting contended waiters.
        deadline = time.monotonic() + 30
        while not shards.read_slot_holders(lock_dir):
            if time.monotonic() > deadline:
                self.fail('holder did not acquire slot-0')
            time.sleep(0.005)

        def contended_waiter():
            with patch.dict(os.environ, {'TILLR_TRACE_DIR': str(trace_dir)}):
                with shards.acquire_shard_slot(
                    lock_dir, 1,
                    notify=lambda _msg: entered_wait.set(),
                    holder='run-contended',
                    label='pkg shard 2',
                ):
                    pass

        waiter_thread = threading.Thread(target=contended_waiter)
        waiter_thread.start()
        self.assertTrue(entered_wait.wait(30))

        # Proof caller takes reserved proof-0 immediately while ordinary slot-0 is held.
        with patch.dict(os.environ, {'TILLR_TRACE_DIR': str(trace_dir)}):
            with shards.acquire_shard_slot(
                lock_dir, 1, slot_class='proof', proof_limit=1, holder='gate:run-1',
            ):
                pass

        # 3. Interrupted wait before acquisition writes acquired == False and slot is None.
        class _StopWait( BaseException ):
            pass

        with patch.dict(os.environ, {'TILLR_TRACE_DIR': str(trace_dir)}):
            with self.assertRaises(_StopWait):
                with shards.acquire_shard_slot(
                    lock_dir, 1,
                    notify=lambda _msg: (_ for _ in ()).throw(_StopWait()),
                    holder='run-interrupted',
                    label='pkg shard 3',
                ):
                    pass

        release_holder.set()
        waiter_thread.join(30)
        holder_thread.join(30)
        self.assertFalse(waiter_thread.is_alive())
        self.assertFalse(holder_thread.is_alive())

        rows = [json.loads(line) for line in span_file.read_text().splitlines() if line.strip()]
        by_run = {row['run']: row for row in rows}
        self.assertEqual(set(by_run), {'run-imm', 'run-contended', 'gate:run-1', 'run-interrupted'})

        imm = by_run['run-imm']
        self.assertEqual(imm['kind'], 'slot.wait')
        self.assertEqual(imm['waited_ms'], 0)
        self.assertEqual(imm['ts_start_ms'], imm['ts_end_ms'])
        self.assertEqual(imm['ts'], imm['ts_end'])
        self.assertEqual(imm['slot'], 'slot-0')
        self.assertEqual(imm['class'], 'ordinary')
        self.assertIs(imm['acquired'], True)
        self.assertEqual(imm['label'], 'pkg shard 1')

        contended = by_run['run-contended']
        self.assertGreater(contended['waited_ms'], 0)
        self.assertEqual(contended['ts_end_ms'] - contended['ts_start_ms'], contended['waited_ms'])
        self.assertEqual(contended['slot'], 'slot-0')
        self.assertIs(contended['acquired'], True)
        self.assertEqual(contended['label'], 'pkg shard 2')

        proof = by_run['gate:run-1']
        self.assertEqual(proof['slot'], 'proof-0')
        self.assertEqual(proof['class'], 'proof')
        self.assertIs(proof['acquired'], True)
        self.assertNotIn('label', proof)

        interrupted = by_run['run-interrupted']
        self.assertIsNone(interrupted['slot'])
        self.assertIs(interrupted['acquired'], False)
        self.assertGreaterEqual(interrupted['waited_ms'], 0)
        self.assertEqual(interrupted['label'], 'pkg shard 3')

        # 4. Missing directory or unwritable destination fails open without breaking slot acquisition.
        missing_dir = self.root / 'does-not-exist'
        with patch.dict(os.environ, {'TILLR_TRACE_DIR': str(missing_dir)}):
            with shards.acquire_shard_slot(lock_dir, 1, holder='run-missing'):
                pass
        self.assertFalse(missing_dir.exists())

        blocked_dir = self.root / 'blocked-trace'
        blocked_dir.mkdir()
        # Make slot-waits.jsonl a directory so opening it for append fails with EISDIR.
        (blocked_dir / 'slot-waits.jsonl').mkdir()
        with patch.dict(os.environ, {'TILLR_TRACE_DIR': str(blocked_dir)}):
            with shards.acquire_shard_slot(lock_dir, 1, holder='run-eisdir'):
                pass

    def test_sibling_gate_gets_free_reserved_slots_and_preempts_proof_and_ordinary(self):
        lock_dir = self.root / 'gate-pool'
        trace_dir = self.root / 'gate-trace'
        trace_dir.mkdir()
        errors = []
        threads = []

        def start_holder(slot_class, holder_name, acquired_evt, release_evt, *, label=None,
                         notify=None):
            def body():
                try:
                    with shards.acquire_shard_slot(
                        lock_dir, 12, notify=notify, slot_class=slot_class,
                        proof_limit=4, gate_limit=6, holder=holder_name, label=label,
                    ):
                        acquired_evt.set()
                        assert release_evt.wait(60)
                except BaseException as exc:
                    errors.append(exc)
                    acquired_evt.set()
                    raise

            thread = threading.Thread(target=body)
            thread.start()
            threads.append(thread)
            return thread

        worker_releases = ()
        proof_releases = ()
        gate1_release = None
        gate2_release = None
        ord_wait_release = None
        prf_wait_release = None
        env_ctx = patch.dict(os.environ, {'TILLR_TRACE_DIR': str(trace_dir)})
        cpu_ctx = patch.object(os, 'cpu_count', return_value=32)
        env_ctx.start()
        cpu_ctx.start()
        try:
            # 1. Occupy all 12 ordinary slots with workers, then all 4 proof slots with trunk-proof.
            worker_releases = [threading.Event() for _ in range(12)]
            worker_acquired = [threading.Event() for _ in range(12)]
            for idx in range(12):
                start_holder('ordinary', f'run-worker-{idx}', worker_acquired[idx], worker_releases[idx])
            for evt in worker_acquired:
                self.assertTrue(evt.wait(30))

            proof_releases = [threading.Event() for _ in range(4)]
            proof_acquired = [threading.Event() for _ in range(4)]
            for idx in range(4):
                start_holder('proof', 'trunk-proof', proof_acquired[idx], proof_releases[idx])
            for evt in proof_acquired:
                self.assertTrue(evt.wait(30))

            # Release 4 ordinary workers so gate-1 acquires 4 slot-* plus 4 gate-* (its 8-slot cap),
            # leaving all 12 ordinary and 4 proof slots held and 2 gate-* slots free.
            for idx in range(8, 12):
                worker_releases[idx].set()
            for idx in range(8, 12):
                threads[idx].join(30)

            gate1_release = threading.Event()
            gate1_acquired = [threading.Event() for _ in range(8)]
            for idx in range(8):
                start_holder('gate', 'gate:run-1', gate1_acquired[idx], gate1_release)
            for evt in gate1_acquired:
                self.assertTrue(evt.wait(30))

            # Verify a 9th simultaneous request from gate:run-1 hits its 8-slot cap without
            # consuming the remaining 2 gate-* slots or asserting gate-waiting.lock.
            class _CapReached(BaseException):
                pass

            cap_notices = []

            def on_cap_wait(msg):
                cap_notices.append(msg)
                self.assertFalse(shards.gate_caller_waiting(lock_dir))
                raise _CapReached()

            with self.assertRaises(_CapReached):
                with shards.acquire_shard_slot(
                    lock_dir, 12, notify=on_cap_wait, slot_class='gate',
                    proof_limit=4, gate_limit=6, holder='gate:run-1',
                ):
                    pass
            self.assertEqual(len(cap_notices), 1)

            holders = shards.read_slot_holders(lock_dir)
            gate1_slots = [h['slot'] for h in holders if h.get('run') == 'gate:run-1']
            self.assertEqual(len(gate1_slots), 8)
            self.assertEqual(sum(s.startswith('slot-') for s in gate1_slots), 4)
            self.assertEqual(sum(s.startswith('gate-') for s in gate1_slots), 4)
            self.assertEqual(sum(h['slot'].startswith('slot-') for h in holders), 12)
            self.assertEqual(sum(h['slot'].startswith('proof-') for h in holders), 4)
            self.assertEqual(sum(h['slot'].startswith('gate-') for h in holders), 4)

            # 2. Start waiting proof and ordinary callers before gate:run-2 arrives.
            ord_wait_started = threading.Event()
            ord_wait_acquired = threading.Event()
            ord_wait_release = threading.Event()
            start_holder(
                'ordinary', 'run-worker-waiter', ord_wait_acquired, ord_wait_release,
                notify=lambda _m: ord_wait_started.set(),
            )
            self.assertTrue(ord_wait_started.wait(30))

            prf_wait_started = threading.Event()
            prf_wait_acquired = threading.Event()
            prf_wait_release = threading.Event()
            start_holder(
                'proof', 'trunk-proof-waiter', prf_wait_acquired, prf_wait_release,
                notify=lambda _m: prf_wait_started.set(),
            )
            self.assertTrue(prf_wait_started.wait(30))
            self.assertTrue(shards.proof_caller_waiting(lock_dir))
            self.assertFalse(ord_wait_acquired.is_set())
            self.assertFalse(prf_wait_acquired.is_set())

            # 3. Second gate run immediately acquires the 2 free gate-* slots without waiting.
            gate2_imm_notices = []
            gate2_release = threading.Event()
            gate2_imm_acquired = [threading.Event() for _ in range(2)]
            for idx in range(2):
                start_holder(
                    'gate', 'gate:run-2', gate2_imm_acquired[idx], gate2_release,
                    label=f'gate2-imm-{idx}', notify=gate2_imm_notices.append,
                )
            for evt in gate2_imm_acquired:
                self.assertTrue(evt.wait(30))
            self.assertEqual(gate2_imm_notices, [])

            # 4. A 3rd shard of gate:run-2 waits on gate-waiting.lock and preempts the next freed
            #    slot-* ahead of both waiting proof and waiting ordinary callers.
            gate2_wait_started = threading.Event()
            gate2_wait_acquired = threading.Event()
            start_holder(
                'gate', 'gate:run-2', gate2_wait_acquired, gate2_release,
                label='gate2-preempt-slot', notify=lambda _m: gate2_wait_started.set(),
            )
            self.assertTrue(gate2_wait_started.wait(30))
            self.assertTrue(shards.gate_caller_waiting(lock_dir))
            self.assertFalse(gate2_wait_acquired.is_set())

            worker_releases[0].set()
            threads[0].join(30)
            self.assertTrue(gate2_wait_acquired.wait(30))
            self.assertFalse(prf_wait_acquired.is_set())
            self.assertFalse(ord_wait_acquired.is_set())

            # 4b. A 4th shard of gate:run-2 also preempts a freed proof-* slot ahead of waiting
            #     proof and ordinary callers.
            gate2_proof_wait_started = threading.Event()
            gate2_proof_wait_acquired = threading.Event()
            start_holder(
                'gate', 'gate:run-2', gate2_proof_wait_acquired, gate2_release,
                label='gate2-preempt-proof', notify=lambda _m: gate2_proof_wait_started.set(),
            )
            self.assertTrue(gate2_proof_wait_started.wait(30))
            self.assertTrue(shards.gate_caller_waiting(lock_dir))
            proof_releases[0].set()
            threads[12].join(30)
            self.assertTrue(gate2_proof_wait_acquired.wait(30))
            self.assertFalse(prf_wait_acquired.is_set())
            self.assertFalse(ord_wait_acquired.is_set())

            # 5. Once no gate caller is waiting, the next freed slot-* goes to proof ahead of ordinary,
            #    and the third freed slot-* goes to ordinary.
            self.assertFalse(shards.gate_caller_waiting(lock_dir))
            worker_releases[1].set()
            threads[1].join(30)
            self.assertTrue(prf_wait_acquired.wait(30))
            self.assertFalse(ord_wait_acquired.is_set())
            prf_wait_release.set()

            worker_releases[2].set()
            threads[2].join(30)
            self.assertTrue(ord_wait_acquired.wait(30))
            ord_wait_release.set()

            rows = [json.loads(line)
                    for line in (trace_dir / 'slot-waits.jsonl').read_text().splitlines()
                    if line.strip()]
            by_label = {row['label']: row for row in rows if 'label' in row}
            for idx in range(2):
                imm_row = by_label[f'gate2-imm-{idx}']
                self.assertEqual(imm_row['class'], 'gate')
                self.assertEqual(imm_row['waited_ms'], 0)
                self.assertTrue(imm_row['slot'].startswith('gate-'), imm_row)
            preempt_row = by_label['gate2-preempt-slot']
            self.assertEqual(preempt_row['class'], 'gate')
            self.assertGreater(preempt_row['waited_ms'], 0)
            self.assertTrue(preempt_row['slot'].startswith('slot-'), preempt_row)
            preempt_proof_row = by_label['gate2-preempt-proof']
            self.assertEqual(preempt_proof_row['class'], 'gate')
            self.assertGreater(preempt_proof_row['waited_ms'], 0)
            self.assertTrue(preempt_proof_row['slot'].startswith('proof-'), preempt_proof_row)
            if errors:
                raise errors[0]
        finally:
            for evt in (*worker_releases, *proof_releases, gate1_release, gate2_release,
                        ord_wait_release, prf_wait_release):
                if evt is not None:
                    evt.set()
            for thread in threads:
                thread.join(30)
            cpu_ctx.stop()
            env_ctx.stop()

    def test_gate_queue_wait_is_excluded_from_watchdog_and_cli_max_slots_caps_concurrency(self):
        # 1. Queue wait while all ordinary/proof/gate slots are held does not count toward
        #    run_shard's no-progress watchdog deadline.
        lock_dir = self.root / 'watchdog-locks'
        trace_dir = self.root / 'watchdog-trace'
        trace_dir.mkdir()
        binary = self.fake(
            "print('=== RUN   TestFast', flush=True)\n"
            "print('--- PASS: TestFast (0.00s)', flush=True)\n"
            "print('PASS', flush=True)\n"
        )
        ordinary_held = threading.Event()
        gate_held = threading.Event()
        release_blockers = threading.Event()

        blocker_errors = []

        def hold(slot_class, holder_name, held_evt):
            try:
                with shards.acquire_shard_slot(
                    lock_dir, 1, slot_class=slot_class, proof_limit=0, gate_limit=1,
                    gate_reserved_limit=1, holder=holder_name,
                ):
                    held_evt.set()
                    assert release_blockers.wait(30)
            except BaseException as exc:
                blocker_errors.append(exc)
                held_evt.set()
                raise

        trace_ctx = patch.dict(os.environ, {'TILLR_TRACE_DIR': str(trace_dir)})
        trace_ctx.start()
        blocker_threads = [
            threading.Thread(target=hold, args=('ordinary', 'run-worker', ordinary_held)),
            threading.Thread(target=hold, args=('gate', 'gate:blocker', gate_held)),
        ]
        for thread in blocker_threads:
            thread.start()
        try:
            self.assertTrue(ordinary_held.wait(30))
            self.assertTrue(gate_held.wait(30))
            if blocker_errors:
                raise blocker_errors[0]

            result_holder = []
            error_holder = []

            def run_waiting_gate_shard():
                try:
                    result_holder.append(shards.run_shard(
                        'fixture', self.root, binary, ['TestFast'], 0,
                        self.root / 'gate-scratch', '0', [], wall_timeout=0.35,
                        lock_dir=lock_dir, shard_limit=1, proof_limit=0, gate_limit=1,
                        gate_reserved_limit=1, slot_class='gate', holder='gate:waiter',
                    ))
                except BaseException as exc:
                    error_holder.append(exc)

            waiter = threading.Thread(target=run_waiting_gate_shard)
            waiter.start()
            while not shards.gate_caller_waiting(lock_dir):
                self.assertTrue(waiter.is_alive(), f'waiter exited early: {error_holder}')
                time.sleep(0.01)
            # Hold past wall_timeout (0.35s) while queued; watchdog must not fire during queue wait.
            time.sleep(0.6)
            self.assertTrue(waiter.is_alive(), f'watchdog fired during slot wait: {error_holder}')
            release_blockers.set()
            waiter.join(30)
            self.assertFalse(waiter.is_alive())
            self.assertEqual(error_holder, [])
            self.assertTrue(any(e.get('Test') == 'TestFast' and e.get('Action') == 'pass'
                                for e in result_holder[0]))
            rows = [json.loads(line)
                    for line in (trace_dir / 'slot-waits.jsonl').read_text().splitlines()
                    if line.strip()]
            span = next(row for row in rows if row['run'] == 'gate:waiter')
            self.assertEqual(span['class'], 'gate')
            self.assertGreater(span['waited_ms'], 0)
        finally:
            release_blockers.set()
            for thread in blocker_threads:
                thread.join(30)
            trace_ctx.stop()

        # 2. CLI --slot-class gate --max-slots 2 bounds simultaneous tests across shards.
        mod = self.root / 'maxslotsmod'
        mod.mkdir()
        (mod / 'go.mod').write_text('module maxslotsmod\n\ngo 1.23\n')
        active_file = self.root / 'ms-active.txt'
        max_file = self.root / 'ms-max.txt'
        lock_file = self.root / 'ms-state.lock'
        active_file.write_text('0')
        max_file.write_text('0')
        (mod / 'fixture_test.go').write_text(f'''package maxslotsmod
import (
    "os"
    "strconv"
    "strings"
    "syscall"
    "testing"
    "time"
)

func step(t *testing.T, delta int) {{
    t.Helper()
    fd, err := syscall.Open({json.dumps(str(lock_file))}, syscall.O_CREAT|syscall.O_RDWR, 0666)
    if err != nil {{ t.Fatal(err) }}
    defer syscall.Close(fd)
    if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {{ t.Fatal(err) }}
    defer syscall.Flock(fd, syscall.LOCK_UN)
    raw, err := os.ReadFile({json.dumps(str(active_file))})
    if err != nil {{ t.Fatal(err) }}
    cur, err := strconv.Atoi(strings.TrimSpace(string(raw)))
    if err != nil {{ t.Fatal(err) }}
    cur += delta
    if err := os.WriteFile({json.dumps(str(active_file))}, []byte(strconv.Itoa(cur)), 0600); err != nil {{ t.Fatal(err) }}
    mraw, err := os.ReadFile({json.dumps(str(max_file))})
    if err != nil {{ t.Fatal(err) }}
    m, err := strconv.Atoi(strings.TrimSpace(string(mraw)))
    if err != nil {{ t.Fatal(err) }}
    if cur > m {{
        if err := os.WriteFile({json.dumps(str(max_file))}, []byte(strconv.Itoa(cur)), 0600); err != nil {{ t.Fatal(err) }}
    }}
}}

func holdBrief(t *testing.T) {{
    step(t, 1)
    time.Sleep(80 * time.Millisecond)
    step(t, -1)
}}

func TestM0(t *testing.T) {{ holdBrief(t) }}
func TestM1(t *testing.T) {{ holdBrief(t) }}
func TestM2(t *testing.T) {{ holdBrief(t) }}
func TestM3(t *testing.T) {{ holdBrief(t) }}
''')
        durations = self.root / 'ms-durations.json'
        durations.write_text('{}')
        completed = subprocess.run(
            ['python3', str(RUNNER), str(mod), '-k', '4', '--slot-class', 'gate',
             '--max-slots', '2', '--durations', str(durations),
             '--lock-dir', str(self.root / 'ms-locks')],
            capture_output=True, text=True, timeout=60,
        )
        self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)
        self.assertLessEqual(int(max_file.read_text().strip()), 2)

        bad = subprocess.run(
            ['python3', str(RUNNER), str(mod), '--slot-class', 'gate', '--max-slots', '0'],
            capture_output=True, text=True, timeout=30,
        )
        self.assertNotEqual(bad.returncode, 0)

    def test_progress_interval_validation_rejects_non_positive_and_non_finite(self):
        record = self.root / 'durations.json'
        record.write_text('{}')
        for bad in ('0', '-1', '-0.5', 'nan', 'inf', '-inf'):
            with self.subTest(interval=bad):
                stderr = io.StringIO()
                with patch.object(shards.sys, 'argv', [
                        'runner', str(self.root), '--durations', str(record),
                        f'--progress-interval={bad}']), redirect_stderr(stderr):
                    with self.assertRaises(SystemExit) as caught:
                        shards.main()
                self.assertEqual(caught.exception.code, 2)
                self.assertIn('progress interval', stderr.getvalue())

    def test_progress_env_one_hop_and_flag_precedence(self):
        record = self.root / 'durations.json'
        record.write_text('{}')
        env_target = self.root / 'env-progress.json'
        flag_target = self.root / 'flag-progress.json'

        # 1. TILLR_GO_SHARDS_PROGRESS_OUT is popped before run() and used when --progress-file is absent.
        seen_env_during_run = []

        def fake_run(*args, **kwargs):
            seen_env_during_run.append(os.environ.get(shards.PROGRESS_OUT_ENV))
            return []

        with patch.dict(os.environ, {shards.PROGRESS_OUT_ENV: str(env_target)}), \
                patch.object(shards, 'run', side_effect=fake_run) as mocked_run, \
                patch.object(shards.sys, 'argv', ['runner', str(self.root), '--durations', str(record)]):
            self.assertEqual(shards.main(), 0)
        self.assertEqual(seen_env_during_run, [None])
        self.assertNotIn(shards.PROGRESS_OUT_ENV, os.environ)
        self.assertEqual(mocked_run.call_args.kwargs['progress_file'], env_target.resolve())
        self.assertEqual(mocked_run.call_args.kwargs['progress_interval'], shards.DEFAULT_PROGRESS_INTERVAL)

        # 2. --progress-file takes precedence over TILLR_GO_SHARDS_PROGRESS_OUT, which is still popped.
        seen_env_during_run.clear()
        with patch.dict(os.environ, {shards.PROGRESS_OUT_ENV: str(env_target)}), \
                patch.object(shards, 'run', side_effect=fake_run) as mocked_run, \
                patch.object(shards.sys, 'argv', [
                    'runner', str(self.root), '--durations', str(record),
                    '--progress-file', str(flag_target), '--progress-interval', '15']):
            self.assertEqual(shards.main(), 0)
        self.assertEqual(seen_env_during_run, [None])
        self.assertNotIn(shards.PROGRESS_OUT_ENV, os.environ)
        self.assertEqual(mocked_run.call_args.kwargs['progress_file'], flag_target.resolve())
        self.assertEqual(mocked_run.call_args.kwargs['progress_interval'], 15.0)

        # 3. Child test binaries never inherit TILLR_GO_SHARDS_PROGRESS_OUT.
        mod = self.root / 'envmod'
        mod.mkdir()
        (mod / 'go.mod').write_text('module envmod\n\ngo 1.23\n')
        (mod / 'env_test.go').write_text('''package envmod
import ("os"; "testing")
func TestEnvPopped(t *testing.T) {
    if v, ok := os.LookupEnv("TILLR_GO_SHARDS_PROGRESS_OUT"); ok {
        t.Fatalf("child inherited TILLR_GO_SHARDS_PROGRESS_OUT=%q", v)
    }
}
''')
        completed = subprocess.run(
            ['python3', str(RUNNER), str(mod), '--durations', str(record),
             '--lock-dir', str(self.root / 'env-locks')],
            env=dict(os.environ, TILLR_GO_SHARDS_PROGRESS_OUT=str(env_target)),
            capture_output=True, text=True, timeout=60,
        )
        self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)

    def test_progress_snapshots_advance_on_package_test_and_shard_completions(self):
        mod = self.root / 'progmod'
        mod.mkdir()
        (mod / 'go.mod').write_text('module progmod\n\ngo 1.23\n')
        (mod / 'prog_test.go').write_text('''package progmod
import ("testing"; "time")
func TestFirst(t *testing.T) {
    t.Run("sub1", func(t *testing.T) { time.Sleep(40 * time.Millisecond) })
    t.Run("sub2", func(t *testing.T) { time.Sleep(40 * time.Millisecond) })
}
func TestSecond(t *testing.T) {
    time.Sleep(80 * time.Millisecond)
}
''')
        progress_file = self.root / 'nested' / 'progress.json'
        snapshots = []
        real_replace = os.replace

        def capture_replace(src, dst):
            real_replace(src, dst)
            if Path(dst) == progress_file.resolve():
                snapshots.append(json.loads(Path(dst).read_text()))

        with patch.object(shards.os, 'replace', side_effect=capture_replace):
            events = shards.run(
                mod, 1, {}, self.root / 'scratch', '0',
                lock_dir=self.root / 'prog-locks',
                progress_file=progress_file.resolve(),
                progress_interval=0.05,
            )
        self.assertEqual(len([e for e in events if e.get('Action') == 'pass' and e.get('Test') in ('TestFirst', 'TestSecond')]), 2)
        self.assertGreaterEqual(len(snapshots), 2, snapshots)
        for index, snap in enumerate(snapshots, start=1):
            self.assertEqual(snap['seq'], index)
            self.assertEqual(
                snap['completed_units'],
                snap['completed_packages'] + snap['completed_tests'] + snap['completed_shards'],
            )
            self.assertIsInstance(snap['last_detail'], str)
            self.assertTrue(snap['last_detail'])
        for prev, curr in zip(snapshots, snapshots[1:]):
            self.assertGreater(curr['completed_units'], prev['completed_units'])
        # Subtests never count toward completed_tests (at most 2 top-level tests).
        self.assertLessEqual(snapshots[-1]['completed_tests'], 2)
        self.assertEqual(snapshots[-1]['completed_packages'], 1)

    def _fake_compile(self, binary):
        def compile_pkg(cmd, *args, **kwargs):
            out = Path(cmd[cmd.index('-o') + 1])
            out.parent.mkdir(parents=True, exist_ok=True)
            out.write_bytes(binary.read_bytes())
            out.chmod(0o755)
        return compile_pkg

    def test_progress_writes_zero_snapshots_when_faster_than_interval(self):
        packages = [{'ImportPath': 'fastpkg', 'Dir': str(self.root), 'TestGoFiles': ['x_test.go']}]
        binary = self.fake(
            "print('=== RUN   TestFast', flush=True)\n"
            "print('--- PASS: TestFast (0.00s)', flush=True)\n"
            "print('PASS', flush=True)\n"
        )
        progress_file = self.root / 'fast-progress.json'
        with patch.object(shards.subprocess, 'check_output', side_effect=[
                json.dumps(packages[0]), 'TestFast\n']), \
                patch.object(shards.subprocess, 'run', side_effect=self._fake_compile(binary)), \
                patch.object(shards, 'global_git_config', return_value=[]):
            events = shards.run(
                self.root, 1, {}, self.root / 'scratch', '0',
                lock_dir=self.root / 'fast-locks',
                progress_file=progress_file,
                progress_interval=60.0,
            )
        self.assertTrue(any(e.get('Test') == 'TestFast' and e.get('Action') == 'pass' for e in events))
        self.assertFalse(progress_file.exists())

    def test_progress_writes_zero_snapshots_when_stalled_in_slot_wait_or_running_test(self):
        # 1. Slot wait never increments completed_units or publishes a snapshot.
        lock_dir = self.root / 'stall-locks'
        slot_held = threading.Event()
        release_slot = threading.Event()

        def hold_slot():
            with shards.acquire_shard_slot(lock_dir, 1, holder='blocker'):
                slot_held.set()
                assert release_slot.wait(30)

        holder = threading.Thread(target=hold_slot)
        holder.start()
        self.assertTrue(slot_held.wait(30))

        binary_fast = self.fake(
            "print('=== RUN   TestAfterWait', flush=True)\n"
            "print('--- PASS: TestAfterWait (0.00s)', flush=True)\n"
            "print('PASS', flush=True)\n"
        )
        wait_progress = self.root / 'wait-progress.json'
        tracker = shards._ProgressTracker(wait_progress, 0.04)
        wait_errors = []

        def run_waiter():
            try:
                shards.run_shard(
                    'waitpkg', self.root, binary_fast, ['TestAfterWait'], 0,
                    self.root / 'wait-scratch', '0', [], lock_dir=lock_dir,
                    shard_limit=1, progress=tracker,
                )
            except BaseException as exc:
                wait_errors.append(exc)

        waiter = threading.Thread(target=run_waiter)
        waiter.start()
        try:
            time.sleep(0.15)
            self.assertTrue(waiter.is_alive())
            self.assertFalse(wait_progress.exists())
        finally:
            release_slot.set()
            waiter.join(30)
            holder.join(30)
        self.assertEqual(wait_errors, [])

        # 2. Action == 'run' hang writes zero snapshots; mid-run hang stops advancing seq.
        hang_progress = self.root / 'hang-progress.json'
        binary_hang = self.fake(
            "import time\n"
            "print('=== RUN   TestPassFirst', flush=True)\n"
            "print('--- PASS: TestPassFirst (0.00s)', flush=True)\n"
            "time.sleep(0.08)\n"
            "print('=== RUN   TestHangForever', flush=True)\n"
            "time.sleep(10)\n"
        )
        hang_tracker = shards._ProgressTracker(hang_progress, 0.04)
        with self.assertRaises(shards.ShardError) as caught:
            shards.run_shard(
                'hangpkg', self.root, binary_hang, ['TestPassFirst', 'TestHangForever'], 0,
                self.root / 'hang-scratch', '0', [], wall_timeout=0.25,
                lock_dir=self.root / 'hang-locks', shard_limit=1, progress=hang_tracker,
            )
        self.assertTrue(caught.exception.timed_out)
        self.assertTrue(hang_progress.exists())
        snap = json.loads(hang_progress.read_text())
        self.assertEqual(snap['seq'], 1)
        self.assertEqual(snap['completed_tests'], 1)

        # Pure Action == 'run' hang from start writes zero snapshots and does not count as shard completion.
        pure_hang_progress = self.root / 'pure-hang-progress.json'
        binary_pure_hang = self.fake(
            "import time\n"
            "print('=== RUN   TestHangOnly', flush=True)\n"
            "time.sleep(10)\n"
        )
        packages = [{'ImportPath': 'purehang', 'Dir': str(self.root), 'TestGoFiles': ['x_test.go']}]
        with patch.object(shards.subprocess, 'check_output', side_effect=[
                json.dumps(packages[0]), 'TestHangOnly\n']), \
                patch.object(shards.subprocess, 'run', side_effect=self._fake_compile(binary_pure_hang)), \
                patch.object(shards, 'global_git_config', return_value=[]):
            with self.assertRaises(shards.ShardError) as pure_caught:
                shards.run(
                    self.root, 1, {}, self.root / 'pure-scratch', '0',
                    shard_timeout=0.2,
                    lock_dir=self.root / 'pure-locks',
                    progress_file=pure_hang_progress,
                    progress_interval=0.04,
                )
        self.assertIn('timed out', str(pure_caught.exception))
        # Package discovery happened at t~0 before progress_interval=0.04s, then nothing completed.
        # Even if discovery happened after 0.04s, completed_tests and completed_shards must remain 0.
        if pure_hang_progress.exists():
            pure_snap = json.loads(pure_hang_progress.read_text())
            self.assertEqual(pure_snap['completed_tests'], 0)
            self.assertEqual(pure_snap['completed_shards'], 0)

    def test_progress_write_error_warns_without_failing_run(self):
        packages = [{'ImportPath': 'warnpkg', 'Dir': str(self.root), 'TestGoFiles': ['x_test.go']}]
        binary = self.fake(
            "import time\n"
            "print('=== RUN   TestSlow', flush=True)\n"
            "time.sleep(0.08)\n"
            "print('--- PASS: TestSlow (0.08s)', flush=True)\n"
            "print('PASS', flush=True)\n"
        )
        stderr = io.StringIO()
        # Passing self.root (an existing directory) as progress_file triggers OSError on os.replace.
        with patch.object(shards.subprocess, 'check_output', side_effect=[
                json.dumps(packages[0]), 'TestSlow\n']), \
                patch.object(shards.subprocess, 'run', side_effect=self._fake_compile(binary)), \
                patch.object(shards, 'global_git_config', return_value=[]), \
                redirect_stderr(stderr):
            events = shards.run(
                self.root, 1, {}, self.root / 'warn-scratch', '0',
                lock_dir=self.root / 'warn-locks',
                progress_file=self.root,
                progress_interval=0.02,
            )
        self.assertTrue(any(e.get('Test') == 'TestSlow' and e.get('Action') == 'pass' for e in events))
        self.assertIn('cannot write progress file', stderr.getvalue())
        self.assertEqual(list(self.root.parent.glob('.go-progress-*')), [])


if __name__ == '__main__':
    unittest.main()
