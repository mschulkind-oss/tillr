#!/usr/bin/env python3
"""Permanent runner regression tests; parity lists and plans the real drain suite without running it."""
from contextlib import ExitStack, contextmanager, redirect_stderr
import fcntl
import importlib.util
import io
import json
import os
import re
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import threading
import time
import types
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
RUNNER = ROOT / 'tools/go-test-shards.py'
spec = importlib.util.spec_from_file_location('shards', RUNNER)
shards = importlib.util.module_from_spec(spec)
# Loaded in setUpModule so the first run reproduces the missing runner.


def setUpModule():
    spec.loader.exec_module(shards)


def _load_shards_from_commit(rev, module_name):
    source = subprocess.check_output(
        ['git', '-C', str(ROOT), 'show', f'{rev}:tools/go-test-shards.py'],
        text=True,
    )
    module = types.ModuleType(module_name)
    module.__file__ = f'{rev}:tools/go-test-shards.py'
    exec(compile(source, module.__file__, 'exec'), module.__dict__)
    return module


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
        os.environ.pop(shards.SLOT_CLASS_ENV, None)

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
            self.assertEqual(shards.SLOT_CLASS_REPAIR, 'repair')
            self.assertEqual(shards.SLOT_CLASSES, ('ordinary', 'proof', 'gate', 'repair'))
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

    def test_slot_limits_off_by_default_returns_immediately_without_locks_or_caps(self):
        self.assertFalse(shards.ENFORCE_SLOT_LIMITS)

        # Fresh lock_dir is never even created when limits are off.
        untouched_dir = self.root / 'never-created-locks'
        trace_dir = self.root / 'off-trace'
        trace_dir.mkdir()
        notices = []
        with patch.dict(os.environ, {'TILLR_TRACE_DIR': str(trace_dir)}):
            for slot_class in shards.SLOT_CLASSES:
                with shards.acquire_shard_slot(
                    untouched_dir, 1, notify=notices.append,
                    slot_class=slot_class, proof_limit=1, gate_limit=1,
                    max_slots=1, gate_reserved_limit=1,
                    holder=f'holder-{slot_class}', label=f'imm-{slot_class}',
                ):
                    pass
        self.assertEqual(notices, [])
        self.assertFalse(untouched_dir.exists())

        rows = [
            json.loads(line)
            for line in (trace_dir / 'slot-waits.jsonl').read_text().splitlines()
            if line.strip()
        ]
        self.assertEqual(len(rows), len(shards.SLOT_CLASSES))
        for slot_class, row in zip(shards.SLOT_CLASSES, rows):
            self.assertEqual(row['kind'], 'slot.wait')
            self.assertEqual(row['waited_ms'], 0)
            self.assertEqual(row['slot'], 'unlimited')
            self.assertEqual(row['class'], slot_class)
            self.assertIs(row['acquired'], True)
            self.assertEqual(row['label'], f'imm-{slot_class}')

        # Even when an older process holds every slot lock, waiting lock, wait ticket,
        # and per-gate cap lock, every class acquires immediately and concurrently
        # without blocking, writing wait tickets, holding waiting locks, or writing holders.
        lock_dir = self.root / 'older-version-locks'
        lock_dir.mkdir()
        wait_queue_dir = lock_dir / 'wait-queue'
        wait_queue_dir.mkdir()
        cap_dir = shards._holder_cap_dir(lock_dir, 'gate:run-capped')
        held_fds = []
        try:
            for name in ('slot-0.lock', 'proof-0.lock', 'gate-0.lock'):
                fd = os.open(lock_dir / name, os.O_CREAT | os.O_RDWR, 0o666)
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                held_fds.append(fd)
            for name in ('repair-waiting.lock', 'proof-waiting.lock', 'gate-waiting.lock'):
                fd = os.open(lock_dir / name, os.O_CREAT | os.O_RDWR, 0o666)
                fcntl.flock(fd, fcntl.LOCK_SH | fcntl.LOCK_NB)
                held_fds.append(fd)
            for name in ('total-0.lock', 'gate-reserved-0.lock'):
                fd = os.open(cap_dir / name, os.O_CREAT | os.O_RDWR, 0o666)
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                held_fds.append(fd)
            existing_ticket = wait_queue_dir / '00-00000000000000000001-0000000001-00000000000000000001-00000000-spg.wait'
            ticket_fd = os.open(existing_ticket, os.O_CREAT | os.O_RDWR, 0o666)
            fcntl.flock(ticket_fd, fcntl.LOCK_SH | fcntl.LOCK_NB)
            held_fds.append(ticket_fd)

            # 4 classes + 3 extra simultaneous gate calls for the same capped holder (7 total).
            requests = [
                ('ordinary', 'run-worker'),
                ('proof', 'trunk-proof'),
                ('repair', 'gate:run-revert-1'),
                ('gate', 'gate:run-capped'),
                ('gate', 'gate:run-capped'),
                ('gate', 'gate:run-capped'),
                ('gate', 'gate:run-capped'),
            ]
            barrier = threading.Barrier(len(requests))
            errors = []
            concurrent_notices = []

            def acquire_and_sync(slot_class, holder_name):
                try:
                    with shards.acquire_shard_slot(
                        lock_dir, 1, notify=concurrent_notices.append,
                        slot_class=slot_class, proof_limit=1, gate_limit=1,
                        max_slots=1, gate_reserved_limit=1, holder=holder_name,
                    ):
                        barrier.wait(timeout=10)
                except BaseException as exc:
                    errors.append(exc)
                    barrier.abort()

            threads = [
                threading.Thread(target=acquire_and_sync, args=req)
                for req in requests
            ]
            for thread in threads:
                thread.start()
            for thread in threads:
                thread.join(15)
                self.assertFalse(thread.is_alive())
            self.assertEqual(errors, [])
            self.assertEqual(concurrent_notices, [])
            self.assertEqual(os.listdir(wait_queue_dir), [existing_ticket.name])
            self.assertEqual(list(lock_dir.glob('*.holder.json')), [])
        finally:
            for fd in held_fds:
                os.close(fd)

    def test_waiting_runners_announce_and_never_exceed_limit(self):
        self._enable_slot_limits()
        # Deterministic: no sleeps decide the outcome. Holders release only after all
        # limit holders have entered and every surplus runner has announced that it is
        # waiting (or immediately on over-admission), so an over-admission would show as
        # a peak above the limit and a slow holder cannot miss the peak.
        limit, runners = 3, 8
        lock_dir = self.root / 'pool'
        state = threading.Lock()
        active = peak = 0
        notices = []
        release = threading.Event()

        def maybe_release_locked():
            if active > limit or (active == limit and len(notices) == runners - limit):
                release.set()

        def notify(message):
            with state:
                notices.append(message)
                maybe_release_locked()

        def runner():
            nonlocal active, peak
            with shards.acquire_shard_slot(lock_dir, limit, notify):
                with state:
                    active += 1
                    peak = max(peak, active)
                    maybe_release_locked()
                assert release.wait(60)
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

    def test_same_priority_waiter_after_lock_does_not_preempt_winner(self):
        self._enable_slot_limits()
        lock_dir = self.root / 'post-lock-pool'
        wait_dir = shards._wait_queue_dir(lock_dir)
        priority = shards._CLASS_PRIORITY[shards.SLOT_CLASS_ORDINARY]
        real_try_lock_slot = shards.try_lock_slot

        # 1. A same-priority waiter registering AFTER try_lock_slot started (e.g. blocked
        #    by this caller's own lock) must not cause the slot winner to drop its slot.
        after_tickets = []

        def lock_then_register_later_waiter(*args, **kwargs):
            fd, slot_name = real_try_lock_slot(*args, **kwargs)
            if fd is not None and not after_tickets:
                after_tickets.append(shards._register_wait_ticket(wait_dir, priority, 's'))
            return fd, slot_name

        notices = []
        try:
            with patch.object(shards, 'try_lock_slot', side_effect=lock_then_register_later_waiter):
                with shards.acquire_shard_slot(lock_dir, 1, notify=notices.append):
                    pass
        finally:
            for ticket_fd, ticket_path, *_ in after_tickets:
                shards._release_wait_ticket(ticket_fd, ticket_path)
        self.assertEqual(notices, [])
        self.assertEqual(len(after_tickets), 1)

        # 2. A same-priority waiter registered BEFORE try_lock_slot started still preempts
        #    a ticketless newcomer (simulated TOCTOU: missed on pre-lock check, caught post-lock).
        early_ticket = shards._register_wait_ticket(wait_dir, priority, 's')
        real_waiter_ahead = shards._waiter_ahead
        calls = [0]

        def miss_first_check(*args, **kwargs):
            calls[0] += 1
            if calls[0] == 1:
                return None
            return real_waiter_ahead(*args, **kwargs)

        class _Yielded(BaseException):
            pass

        try:
            with patch.object(shards, '_waiter_ahead', side_effect=miss_first_check):
                with self.assertRaises(_Yielded):
                    with shards.acquire_shard_slot(
                        lock_dir, 1, notify=lambda _m: (_ for _ in ()).throw(_Yielded()),
                    ):
                        pass
            early_arrival_ns = early_ticket[3][1]
            self.assertEqual(
                shards._waiter_ahead(wait_dir, 's', priority, None, before_ns=early_arrival_ns + 1),
                priority,
            )
            self.assertIsNone(
                shards._waiter_ahead(wait_dir, 's', priority, None, before_ns=early_arrival_ns),
            )
        finally:
            shards._release_wait_ticket(early_ticket[0], early_ticket[1])

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
            self._runner_with_limits_cmd(str(mod_a), '-k', str(first_count), '--durations', str(durations),
                                         '--lock-dir', str(lock_dir)),
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            wait_until(lambda: len(list(markers.glob('started_A_*'))) == first_count or proc_a.poll() is not None,
                       'first invocation to occupy its slots')
            if proc_a.poll() is not None:
                self.fail(f'first invocation exited early: {proc_a.stderr.read()}')

            proc_b = subprocess.Popen(
                self._runner_with_limits_cmd(str(mod_b), '-k', str(second_count), '--durations', str(durations),
                                             '--lock-dir', str(lock_dir)),
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
        self._enable_slot_limits()
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

    def test_sibling_gate_gets_free_reserved_slots_and_proof_preempts_gate_and_ordinary(self):
        self._enable_slot_limits()
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
        prf2_wait_release = None
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

            # 4. A 3rd shard of gate:run-2 waits on gate-waiting.lock; waiting proof callers
            #    still get freed proof-* and slot-* slots ahead of both gate and ordinary.
            gate2_wait_started = threading.Event()
            gate2_wait_acquired = threading.Event()
            start_holder(
                'gate', 'gate:run-2', gate2_wait_acquired, gate2_release,
                label='gate2-preempt-ordinary', notify=lambda _m: gate2_wait_started.set(),
            )
            self.assertTrue(gate2_wait_started.wait(30))
            self.assertTrue(shards.gate_caller_waiting(lock_dir))
            self.assertFalse(gate2_wait_acquired.is_set())

            proof_releases[0].set()
            threads[12].join(30)
            self.assertTrue(prf_wait_acquired.wait(30))
            self.assertFalse(gate2_wait_acquired.is_set())
            self.assertFalse(ord_wait_acquired.is_set())

            # 4b. A second proof waiter arriving after gate:run-2 is already waiting still
            #     preempts the next freed slot-* ahead of gate:run-2 and ordinary.
            prf2_wait_started = threading.Event()
            prf2_wait_acquired = threading.Event()
            prf2_wait_release = threading.Event()
            start_holder(
                'proof', 'trunk-proof-waiter-2', prf2_wait_acquired, prf2_wait_release,
                label='proof2-preempt-gate', notify=lambda _m: prf2_wait_started.set(),
            )
            self.assertTrue(prf2_wait_started.wait(30))
            self.assertTrue(shards.proof_caller_waiting(lock_dir))
            worker_releases[0].set()
            threads[0].join(30)
            self.assertTrue(prf2_wait_acquired.wait(30))
            self.assertFalse(gate2_wait_acquired.is_set())
            self.assertFalse(ord_wait_acquired.is_set())

            # 5. Once no proof caller is waiting, the next freed slot-* goes to gate ahead of
            #    ordinary, and the third freed slot-* goes to ordinary.
            self.assertFalse(shards.proof_caller_waiting(lock_dir))
            worker_releases[1].set()
            threads[1].join(30)
            self.assertTrue(gate2_wait_acquired.wait(30))
            self.assertFalse(ord_wait_acquired.is_set())

            worker_releases[2].set()
            threads[2].join(30)
            self.assertTrue(ord_wait_acquired.wait(30))
            ord_wait_release.set()
            prf2_wait_release.set()
            prf_wait_release.set()

            rows = [json.loads(line)
                    for line in (trace_dir / 'slot-waits.jsonl').read_text().splitlines()
                    if line.strip()]
            by_label = {row['label']: row for row in rows if 'label' in row}
            for idx in range(2):
                imm_row = by_label[f'gate2-imm-{idx}']
                self.assertEqual(imm_row['class'], 'gate')
                self.assertEqual(imm_row['waited_ms'], 0)
                self.assertTrue(imm_row['slot'].startswith('gate-'), imm_row)
            proof2_row = by_label['proof2-preempt-gate']
            self.assertEqual(proof2_row['class'], 'proof')
            self.assertGreater(proof2_row['waited_ms'], 0)
            self.assertTrue(proof2_row['slot'].startswith('slot-'), proof2_row)
            preempt_row = by_label['gate2-preempt-ordinary']
            self.assertEqual(preempt_row['class'], 'gate')
            self.assertGreater(preempt_row['waited_ms'], 0)
            self.assertTrue(preempt_row['slot'].startswith('slot-'), preempt_row)
            if errors:
                raise errors[0]
        finally:
            for evt in (*worker_releases, *proof_releases, gate1_release, gate2_release,
                        ord_wait_release, prf_wait_release, prf2_wait_release):
                if evt is not None:
                    evt.set()
            for thread in threads:
                thread.join(30)
            cpu_ctx.stop()
            env_ctx.stop()

    def test_gate_queue_wait_is_excluded_from_watchdog_and_cli_max_slots_caps_concurrency(self):
        self._enable_slot_limits()
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
        # Acquire ordinary before gate synchronously on this thread: gate prefers slot-* over
        # gate-*, so starting both concurrently lets gate claim slot-0 and deadlock ordinary.
        # Advance the runner's monotonic clock past wall_timeout while the waiter is queued
        # instead of racing a tight real-time deadline against subprocess startup under load.
        real_monotonic = time.monotonic
        clock_offset = [0.0]

        def shifted_monotonic():
            return real_monotonic() + clock_offset[0]

        with patch.dict(os.environ, {'TILLR_TRACE_DIR': str(trace_dir)}), \
                patch.object(shards.time, 'monotonic', side_effect=shifted_monotonic):
            blockers = ExitStack()
            try:
                blockers.enter_context(shards.acquire_shard_slot(
                    lock_dir, 1, slot_class='ordinary', proof_limit=0, gate_limit=1,
                    gate_reserved_limit=1, holder='run-worker',
                ))
                blockers.enter_context(shards.acquire_shard_slot(
                    lock_dir, 1, slot_class='gate', proof_limit=0, gate_limit=1,
                    gate_reserved_limit=1, holder='gate:blocker',
                ))

                result_holder = []
                error_holder = []

                def run_waiting_gate_shard():
                    try:
                        result_holder.append(shards.run_shard(
                            'fixture', self.root, binary, ['TestFast'], 0,
                            self.root / 'gate-scratch', '0', [], wall_timeout=60.0,
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
                # Advance past wall_timeout (60s) while queued; watchdog must not fire during wait.
                clock_offset[0] = 120.0
                self.assertTrue(waiter.is_alive(), f'watchdog fired during slot wait: {error_holder}')
                blockers.close()
                waiter.join()
                self.assertEqual(error_holder, [])
                self.assertTrue(any(e.get('Test') == 'TestFast' and e.get('Action') == 'pass'
                                    for e in result_holder[0]))
                rows = [json.loads(line)
                        for line in (trace_dir / 'slot-waits.jsonl').read_text().splitlines()
                        if line.strip()]
                span = next(row for row in rows if row['run'] == 'gate:waiter')
                self.assertEqual(span['class'], 'gate')
                self.assertGreaterEqual(span['waited_ms'], 120000)
            finally:
                blockers.close()

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
            self._runner_with_limits_cmd(str(mod), '-k', '4', '--slot-class', 'gate',
                                         '--max-slots', '2', '--durations', str(durations),
                                         '--lock-dir', str(self.root / 'ms-locks')),
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
        self._enable_slot_limits()
        fake_now = [1000.0]
        clock_lock = threading.Lock()

        def fake_monotonic():
            with clock_lock:
                return fake_now[0]

        def advance_clock(seconds):
            with clock_lock:
                fake_now[0] += seconds

        fake_time = types.SimpleNamespace(
            monotonic=fake_monotonic,
            time=time.time,
            sleep=time.sleep,
        )

        def sh_script(name, body):
            script = self.root / name
            script.write_text('#!/bin/sh\n' + body)
            script.chmod(0o755)
            return script

        def make_fifo(name):
            fifo = self.root / name
            os.mkfifo(fifo)
            fd = os.open(fifo, os.O_RDWR)
            self.addCleanup(os.close, fd)
            return fifo

        def scratch_has_event(scratch_dir, test_name, action):
            if not scratch_dir.is_dir():
                return False
            for path in scratch_dir.rglob('*'):
                if not path.is_file():
                    continue
                try:
                    raw = path.read_bytes()
                except OSError:
                    continue
                for line in raw.splitlines(keepends=True):
                    if not line.endswith(b'\n'):
                        continue
                    try:
                        obj = json.loads(line)
                    except ValueError:
                        continue
                    if obj.get('Test') == test_name and obj.get('Action') == action:
                        return True
            return False

        real_maybe_publish = shards._ProgressTracker.maybe_publish

        @contextmanager
        def drive_stalled_shard(scratch_dir, stalled_test, *, expected_units, interval, timeout):
            stage = [0]
            liveness_deadline = time.monotonic() + 60.0

            def stepped_maybe_publish(tracker):
                if time.monotonic() > liveness_deadline:
                    self.fail(f'liveness guard expired waiting for {stalled_test} in {scratch_dir}')
                if stage[0] == 0:
                    real_maybe_publish(tracker)
                    if (tracker._completed_units >= expected_units
                            and scratch_has_event(scratch_dir, stalled_test, 'run')):
                        self.assertFalse(tracker._path.exists())
                        advance_clock(interval * 2.0)
                        real_maybe_publish(tracker)
                        self.assertEqual(tracker._path.exists(), expected_units > 0)
                        if expected_units > 0:
                            self.assertEqual(json.loads(tracker._path.read_text())['seq'], 1)
                        stage[0] = 1
                elif stage[0] == 1:
                    advance_clock(interval * 3.0)
                    real_maybe_publish(tracker)
                    self.assertEqual(tracker._path.exists(), expected_units > 0)
                    if expected_units > 0:
                        self.assertEqual(json.loads(tracker._path.read_text())['seq'], 1)
                    advance_clock(timeout * 2.0)
                    stage[0] = 2
                else:
                    real_maybe_publish(tracker)

            with patch.object(shards._ProgressTracker, 'maybe_publish', stepped_maybe_publish):
                yield
            self.assertEqual(stage[0], 2)

        with patch.object(shards, 'time', fake_time):
            # 1. Slot wait never increments completed_units or publishes a snapshot.
            lock_dir = self.root / 'stall-locks'
            binary_fast = sh_script(
                'fast.sh',
                'printf "=== RUN   TestAfterWait\\n--- PASS: TestAfterWait (0.00s)\\nPASS\\n"\n',
            )
            wait_progress = self.root / 'wait-progress.json'
            tracker = shards._ProgressTracker(wait_progress, 60.0)
            entered_slot_wait = threading.Event()
            post_advance_poll = threading.Event()
            notice_count = [0]
            real_acquire = shards.acquire_shard_slot

            def observe_acquire(*args, **kwargs):
                orig_notify = args[2] if len(args) > 2 else kwargs.get('notify')

                def wrapped_notify(msg):
                    if orig_notify is not None:
                        orig_notify(msg)
                    notice_count[0] += 1
                    if notice_count[0] == 1:
                        entered_slot_wait.set()
                    else:
                        post_advance_poll.set()

                if len(args) > 2:
                    args = (*args[:2], wrapped_notify, *args[3:])
                else:
                    kwargs = dict(kwargs, notify=wrapped_notify)
                return real_acquire(*args, **kwargs)

            wait_errors = []
            with ExitStack() as blocker:
                blocker.enter_context(real_acquire(lock_dir, 1, holder='blocker'))
                with patch.object(shards, 'acquire_shard_slot', side_effect=observe_acquire):
                    def run_waiter():
                        try:
                            shards.run_shard(
                                'waitpkg', self.root, binary_fast, ['TestAfterWait'], 0,
                                self.root / 'wait-scratch', '0', [], wall_timeout=600.0,
                                lock_dir=lock_dir, shard_limit=1, progress=tracker,
                            )
                        except BaseException as exc:
                            wait_errors.append(exc)

                    waiter = threading.Thread(target=run_waiter)
                    waiter.start()
                    try:
                        self.assertTrue(entered_slot_wait.wait(30))
                        advance_clock(180.0)
                        self.assertTrue(post_advance_poll.wait(30))
                        tracker.maybe_publish()
                        self.assertTrue(waiter.is_alive())
                        self.assertEqual(tracker._completed_units, 0)
                        self.assertFalse(wait_progress.exists())
                    finally:
                        blocker.close()
                        waiter.join(30)
            self.assertFalse(waiter.is_alive())
            self.assertEqual(wait_errors, [])
            self.assertTrue(wait_progress.exists())
            wait_snap = json.loads(wait_progress.read_text())
            self.assertEqual(wait_snap['seq'], 1)
            self.assertEqual(wait_snap['completed_tests'], 1)

            # 2. Action == 'run' hang writes zero snapshots; mid-run hang stops advancing seq.
            poll_interval = 0.005
            watchdog_timeout = 0.25
            hang_fifo = make_fifo('hang.fifo')
            binary_hang = sh_script(
                'hang.sh',
                'printf "=== RUN   TestPassFirst\\n--- PASS: TestPassFirst (0.00s)\\n'
                '=== RUN   TestHangForever\\n"\n'
                f'read _ < {shlex.quote(str(hang_fifo))}\n',
            )
            hang_progress = self.root / 'hang-progress.json'
            hang_scratch = self.root / 'hang-scratch'
            hang_tracker = shards._ProgressTracker(hang_progress, poll_interval)
            with drive_stalled_shard(hang_scratch, 'TestHangForever', expected_units=1,
                                     interval=poll_interval, timeout=watchdog_timeout):
                with self.assertRaises(shards.ShardError) as caught:
                    shards.run_shard(
                        'hangpkg', self.root, binary_hang,
                        ['TestPassFirst', 'TestHangForever'], 0,
                        hang_scratch, '0', [], wall_timeout=watchdog_timeout,
                        lock_dir=self.root / 'hang-locks', shard_limit=1,
                        progress=hang_tracker,
                    )
            self.assertTrue(caught.exception.timed_out)
            self.assertIn('running: TestHangForever', str(caught.exception))
            self.assertTrue(hang_progress.exists())
            snap = json.loads(hang_progress.read_text())
            self.assertEqual(snap['seq'], 1)
            self.assertEqual(snap['completed_tests'], 1)

            # Pure Action == 'run' hang from start writes zero snapshots and does not count as shard completion.
            pure_fifo = make_fifo('pure.fifo')
            binary_pure_hang = sh_script(
                'pure-hang.sh',
                'printf "=== RUN   TestHangOnly\\n"\n'
                f'read _ < {shlex.quote(str(pure_fifo))}\n',
            )
            pure_shard_progress = self.root / 'pure-shard-progress.json'
            pure_shard_scratch = self.root / 'pure-shard-scratch'
            pure_shard_tracker = shards._ProgressTracker(pure_shard_progress, poll_interval)
            with drive_stalled_shard(pure_shard_scratch, 'TestHangOnly', expected_units=0,
                                     interval=poll_interval, timeout=watchdog_timeout):
                with self.assertRaises(shards.ShardError) as pure_shard_caught:
                    shards.run_shard(
                        'purehang', self.root, binary_pure_hang, ['TestHangOnly'], 0,
                        pure_shard_scratch, '0', [], wall_timeout=watchdog_timeout,
                        lock_dir=self.root / 'pure-shard-locks', shard_limit=1,
                        progress=pure_shard_tracker,
                    )
            self.assertTrue(pure_shard_caught.exception.timed_out)
            self.assertIn('running: TestHangOnly', str(pure_shard_caught.exception))
            self.assertFalse(pure_shard_progress.exists())

            pure_hang_progress = self.root / 'pure-hang-progress.json'
            pure_scratch = self.root / 'pure-scratch'
            packages = [{'ImportPath': 'purehang', 'Dir': str(self.root), 'TestGoFiles': ['x_test.go']}]
            with patch.object(shards.subprocess, 'check_output', side_effect=[
                    json.dumps(packages[0]), 'TestHangOnly\n']), \
                    patch.object(shards.subprocess, 'run', side_effect=self._fake_compile(binary_pure_hang)), \
                    patch.object(shards, 'global_git_config', return_value=[]), \
                    drive_stalled_shard(pure_scratch, 'TestHangOnly', expected_units=1,
                                        interval=poll_interval, timeout=watchdog_timeout):
                with self.assertRaises(shards.ShardError) as pure_caught:
                    shards.run(
                        self.root, 1, {}, pure_scratch, '0',
                        shard_timeout=watchdog_timeout,
                        lock_dir=self.root / 'pure-locks',
                        progress_file=pure_hang_progress,
                        progress_interval=poll_interval,
                    )
            self.assertIn('running: TestHangOnly', str(pure_caught.exception))
            self.assertTrue(pure_hang_progress.exists())
            pure_snap = json.loads(pure_hang_progress.read_text())
            self.assertEqual(pure_snap['seq'], 1)
            self.assertEqual(pure_snap['completed_packages'], 1)
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

    def test_waiting_gates_served_in_arrival_order_without_starvation(self):
        self._enable_slot_limits()
        lock_dir = self.root / 'fifo-gates'
        order = []
        order_lock = threading.Lock()
        errors = []
        threads = []
        releases = {}

        # Plant a stale (unlocked) wait ticket from a dead process to ensure it does not block anyone.
        wait_dir = shards.wait_queue_dir(lock_dir)
        wait_dir.mkdir(parents=True, exist_ok=True)
        stale_ticket = wait_dir / '00-00000000000000000001-0000000001-00000000000000000001-00000000-spg.wait'
        stale_ticket.write_text('')

        def start_waiter(name, *, slot_class='proof'):
            waiting = threading.Event()
            acquired = threading.Event()
            release = threading.Event()
            releases[name] = release

            def body():
                try:
                    with shards.acquire_shard_slot(
                        lock_dir, 1, notify=lambda _m: waiting.set(),
                        slot_class=slot_class, proof_limit=0, gate_limit=0,
                        holder=name,
                    ):
                        with order_lock:
                            order.append(name)
                        acquired.set()
                        assert release.wait(60)
                except BaseException as exc:
                    errors.append(exc)
                    waiting.set()
                    acquired.set()
                    raise

            thread = threading.Thread(target=body)
            thread.start()
            threads.append(thread)
            return waiting, acquired, release

        blocker = ExitStack()
        try:
            blocker.enter_context(shards.acquire_shard_slot(
                lock_dir, 1, slot_class='proof', proof_limit=0, gate_limit=0,
                holder='gate:run-initial',
            ))

            waiters = []
            for idx in range(1, 5):
                name = f'gate:run-item-{idx}'
                waiting, acquired, release = start_waiter(name)
                self.assertTrue(waiting.wait(30), f'{name} did not enter wait')
                self.assertFalse(acquired.is_set())
                waiters.append((name, acquired, release))

            # Release the initial holder; gate:run-item-1 must go first.
            blocker.close()
            self.assertTrue(waiters[0][1].wait(30))
            self.assertFalse(stale_ticket.exists())

            # While gate:run-item-1 holds the slot, a later gate arrives and tries to jump ahead.
            late_waiting, late_acquired, late_release = start_waiter('gate:run-item-5')
            self.assertTrue(late_waiting.wait(30))
            self.assertFalse(late_acquired.is_set())
            waiters.append(('gate:run-item-5', late_acquired, late_release))

            # Release each holder in turn; every waiting gate must acquire in arrival order.
            for idx, (name, acquired, release) in enumerate(waiters):
                self.assertTrue(acquired.wait(30), f'{name} did not acquire')
                with order_lock:
                    self.assertEqual(order, [w[0] for w in waiters[:idx + 1]])
                release.set()
                if idx + 1 < len(waiters):
                    self.assertTrue(waiters[idx + 1][1].wait(30), f'{waiters[idx + 1][0]} did not acquire next')
        finally:
            blocker.close()
            for evt in releases.values():
                evt.set()
            for thread in threads:
                thread.join(30)
        if errors:
            raise errors[0]
        self.assertEqual(order, [f'gate:run-item-{idx}' for idx in range(1, 6)])

    def test_revert_and_trunk_fixer_gates_served_before_waiting_ordinary_gates(self):
        self._enable_slot_limits()
        lock_dir = self.root / 'repair-priority'
        order = []
        order_lock = threading.Lock()
        errors = []
        threads = []
        releases = {}

        def start_waiter(name, *, slot_class='proof'):
            waiting = threading.Event()
            acquired = threading.Event()
            release = threading.Event()
            releases[name] = release

            def body():
                try:
                    with shards.acquire_shard_slot(
                        lock_dir, 1, notify=lambda _m: waiting.set(),
                        slot_class=slot_class, proof_limit=0, gate_limit=0,
                        holder=name,
                    ):
                        with order_lock:
                            order.append(name)
                        acquired.set()
                        assert release.wait(60)
                except BaseException as exc:
                    errors.append(exc)
                    waiting.set()
                    acquired.set()
                    raise

            thread = threading.Thread(target=body)
            thread.start()
            threads.append(thread)
            return waiting, acquired, release

        blocker = ExitStack()
        try:
            blocker.enter_context(shards.acquire_shard_slot(
                lock_dir, 1, slot_class='proof', proof_limit=0, gate_limit=0,
                holder='gate:run-initial',
            ))

            # Candidate gates begin waiting first, plus a trunk proof and an ordinary lane worker.
            cand1_w, cand1_a, cand1_r = start_waiter('gate:run-item-1', slot_class='gate')
            self.assertTrue(cand1_w.wait(30))
            cand2_w, cand2_a, cand2_r = start_waiter('gate:run-item-2', slot_class='gate')
            self.assertTrue(cand2_w.wait(30))
            proof_w, proof_a, proof_r = start_waiter('trunk-proof', slot_class='proof')
            self.assertTrue(proof_w.wait(30))
            worker_w, worker_a, worker_r = start_waiter('run-fixer-main-deadbeef1234-a1-n1', slot_class='ordinary')
            self.assertTrue(worker_w.wait(30))

            # Revert and trunk-fixer gates arrive AFTER the candidate gates and trunk proof are waiting.
            rev_w, rev_a, rev_r = start_waiter('gate:run-revert-dd17e3283fd1-d5f5f41a7e57', slot_class='gate')
            self.assertTrue(rev_w.wait(30))
            self.assertTrue(shards.repair_caller_waiting(lock_dir))
            fix_w, fix_a, fix_r = start_waiter('gate:run-fixer-main-dd17e3283fd1-a1-n1', slot_class='gate')
            self.assertTrue(fix_w.wait(30))

            # Release the initial slot; revert goes first, then trunk fixer, then trunk proof,
            # then candidate gates in arrival order, then the ordinary lane worker last.
            blocker.close()
            expected = [
                ('gate:run-revert-dd17e3283fd1-d5f5f41a7e57', rev_a, rev_r),
                ('gate:run-fixer-main-dd17e3283fd1-a1-n1', fix_a, fix_r),
                ('trunk-proof', proof_a, proof_r),
                ('gate:run-item-1', cand1_a, cand1_r),
                ('gate:run-item-2', cand2_a, cand2_r),
                ('run-fixer-main-deadbeef1234-a1-n1', worker_a, worker_r),
            ]
            for idx, (name, acquired, release) in enumerate(expected):
                self.assertTrue(acquired.wait(30), f'{name} did not acquire in turn {idx}')
                with order_lock:
                    self.assertEqual(order, [e[0] for e in expected[:idx + 1]])
                release.set()
        finally:
            blocker.close()
            for evt in releases.values():
                evt.set()
            for thread in threads:
                thread.join(30)
        if errors:
            raise errors[0]
        self.assertEqual(order, [
            'gate:run-revert-dd17e3283fd1-d5f5f41a7e57',
            'gate:run-fixer-main-dd17e3283fd1-a1-n1',
            'trunk-proof',
            'gate:run-item-1',
            'gate:run-item-2',
            'run-fixer-main-deadbeef1234-a1-n1',
        ])

    def test_proof_gets_reserved_slot_immediately_and_outranks_waiting_gates(self):
        self._enable_slot_limits()
        lock_dir = self.root / 'proof-over-gate'
        trace_dir = self.root / 'proof-over-gate-trace'
        trace_dir.mkdir()
        errors = []
        threads = []

        def start_holder(slot_class, holder_name, acquired_evt, release_evt, *, label=None,
                         notify=None):
            def body():
                try:
                    with shards.acquire_shard_slot(
                        lock_dir, 2, notify=notify, slot_class=slot_class,
                        proof_limit=1, gate_limit=1, holder=holder_name, label=label,
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

        gate_releases = [threading.Event() for _ in range(3)]
        gate_wait_release = threading.Event()
        proof1_release = threading.Event()
        proof2_release = threading.Event()
        with patch.dict(os.environ, {'TILLR_TRACE_DIR': str(trace_dir)}):
            try:
                # 1. Candidate gates hold every ordinary slot (slot-0, slot-1) and the gate slot (gate-0).
                gate_acquired = [threading.Event() for _ in range(3)]
                for idx in range(3):
                    start_holder('gate', f'gate:run-{idx + 1}', gate_acquired[idx], gate_releases[idx])
                for evt in gate_acquired:
                    self.assertTrue(evt.wait(30))

                holders = shards.read_slot_holders(lock_dir)
                self.assertEqual(len(holders), 3)
                self.assertTrue(all(h['class'] == 'gate' for h in holders), holders)
                self.assertFalse(any(h['slot'].startswith('proof-') for h in holders), holders)
                self.assertEqual({h['slot'] for h in holders}, {'slot-0', 'slot-1', 'gate-0'})

                # 2. A fourth candidate gate waits because ordinary and gate slots are full.
                gate_wait_started = threading.Event()
                gate_wait_acquired = threading.Event()
                start_holder(
                    'gate', 'gate:run-wait', gate_wait_acquired, gate_wait_release,
                    label='gate-waiter', notify=lambda _m: gate_wait_started.set(),
                )
                self.assertTrue(gate_wait_started.wait(30))
                self.assertTrue(shards.gate_caller_waiting(lock_dir))
                self.assertFalse(gate_wait_acquired.is_set())

                # 3. A proof suite arriving later immediately acquires proof-0 without waiting.
                proof1_notices = []
                proof1_acquired = threading.Event()
                start_holder(
                    'proof', 'trunk-proof', proof1_acquired, proof1_release,
                    label='proof-imm', notify=proof1_notices.append,
                )
                self.assertTrue(proof1_acquired.wait(30))
                self.assertEqual(proof1_notices, [])

                # 4. A second proof shard waits after gate:run-wait is already waiting; releasing an
                #    ordinary slot serves the waiting proof ahead of the waiting candidate gate.
                proof2_wait_started = threading.Event()
                proof2_acquired = threading.Event()
                start_holder(
                    'proof', 'trunk-proof', proof2_acquired, proof2_release,
                    label='proof-waiter', notify=lambda _m: proof2_wait_started.set(),
                )
                self.assertTrue(proof2_wait_started.wait(30))
                self.assertTrue(shards.proof_caller_waiting(lock_dir))

                slot0_holder = next(h['run'] for h in holders if h['slot'] == 'slot-0')
                slot0_idx = int(slot0_holder.rsplit('-', 1)[1]) - 1
                gate_releases[slot0_idx].set()
                threads[slot0_idx].join(30)

                self.assertTrue(proof2_acquired.wait(30))
                self.assertFalse(gate_wait_acquired.is_set())

                proof2_release.set()
                self.assertTrue(gate_wait_acquired.wait(30))
            finally:
                for evt in (*gate_releases, gate_wait_release, proof1_release, proof2_release):
                    evt.set()
                for thread in threads:
                    thread.join(30)
        if errors:
            raise errors[0]
        rows = [json.loads(line)
                for line in (trace_dir / 'slot-waits.jsonl').read_text().splitlines()
                if line.strip()]
        by_label = {row['label']: row for row in rows if 'label' in row}
        self.assertEqual(by_label['proof-imm']['slot'], 'proof-0')
        self.assertEqual(by_label['proof-imm']['waited_ms'], 0)
        self.assertEqual(by_label['proof-waiter']['slot'], 'slot-0')

    def test_caller_slot_class_is_cleared_before_each_test(self):
        self.assertNotIn(shards.SLOT_CLASS_ENV, os.environ)
        seen = []

        class Probe(RunnerTests):
            def runTest(self):
                seen.append(shards.SLOT_CLASS_ENV in os.environ)
                os.environ[shards.SLOT_CLASS_ENV] = shards.SLOT_CLASS_REPAIR

        with patch.dict(os.environ, {shards.SLOT_CLASS_ENV: shards.SLOT_CLASS_REPAIR}):
            for _ in range(2):
                result = unittest.TestResult()
                Probe('runTest').run(result)
                self.assertEqual(result.errors, [])
                self.assertEqual(result.failures, [])
            self.assertEqual(os.environ.get(shards.SLOT_CLASS_ENV), shards.SLOT_CLASS_REPAIR)
        self.assertEqual(seen, [False, False])

        completed = subprocess.run(
            [
                shards.sys.executable,
                str(Path(__file__).resolve()),
                'RunnerTests.test_gate_queue_wait_is_excluded_from_watchdog_and_cli_max_slots_caps_concurrency',
            ],
            env=dict(os.environ, **{shards.SLOT_CLASS_ENV: shards.SLOT_CLASS_REPAIR}),
            capture_output=True,
            text=True,
            timeout=60,
        )
        self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)

    def test_isolated_runs_bypass_block_git_shim_and_disable_auto_maintenance(self):
        real_git = None
        for entry in os.environ.get('PATH', '').split(os.pathsep):
            if not entry:
                continue
            cand = Path(entry) / 'git'
            if not (cand.is_file() and os.access(cand, os.X_OK)):
                continue
            if cand.parent.parts[-3:] == ('.yolo', 'bin', 'block'):
                continue
            try:
                head = cand.read_bytes()[:256]
            except OSError:
                continue
            if head.startswith(b'#!') and (b'_yg_tool=git' in head or b'# tillr-git-guard' in head):
                continue
            real_git = cand
            break
        self.assertIsNotNone(real_git)

        shim_hits = self.root / 'shim-hits.log'
        fake_block = self.root / '.yolo' / 'bin' / 'block'
        fake_block.mkdir(parents=True)
        fake_block_git = fake_block / 'git'
        fake_block_git.write_text(
            '#!/bin/sh\n'
            "# A guard: runs the real git unless an argument names this workspace's .yolo.\n"
            'if [ -z "$YOLO_BYPASS_SHIMS" ]; then\n'
            '  _yg_tool=git\n'
            f'  printf "block-hit\\n" >> "{shim_hits}"\n'
            '  exit 97\n'
            'fi\n'
            f'exec "{real_git}" "$@"\n'
        )
        fake_block_git.chmod(0o755)

        fake_guard_dir = self.root / 'git-guard'
        fake_guard_dir.mkdir()
        fake_guard_git = fake_guard_dir / 'git'
        fake_guard_git.write_text(
            '#!/bin/sh\n'
            '# tillr-git-guard\n'
            f'printf "guard-hit\\n" >> "{shim_hits}"\n'
            'exit 98\n'
        )
        fake_guard_git.chmod(0o755)

        fallback_env = shards._bypass_git_shim_env(
            {'PATH': os.pathsep.join([str(fake_guard_dir), str(fake_block)])},
            Path('/proc/unwritable-git-bin'),
        )
        self.assertEqual(fallback_env['YOLO_BYPASS_SHIMS'], '1')
        self.assertNotIn(str(fake_guard_dir), fallback_env['PATH'].split(os.pathsep))

        inherited_cfg = self.root / 'inherited.gitconfig'
        inherited_text = (
            '[user]\n'
            '\tname = Shard Owner\n'
            '\temail = shard@example.invalid\n'
            '[maintenance]\n'
            '\tauto = true\n'
            '[gc]\n'
            '\tauto = 6700\n'
        )
        inherited_cfg.write_text(inherited_text)

        module = self.root / 'gitshim-mod'
        module.mkdir()
        (module / 'go.mod').write_text('module fixture\n\ngo 1.23\n')
        (module / 'fixture_test.go').write_text(f'''package fixture
import (
\t"os"
\t"os/exec"
\t"strings"
\t"testing"
)
func verifyGitEnv() string {{
\tresolved, err := exec.LookPath("git")
\tif err != nil {{ return err.Error() }}
\tif resolved == "{fake_block_git}" || resolved == "{fake_guard_git}" {{
\t\treturn "git resolved to shell shim: " + resolved
\t}}
\tfor _, check := range [][2]string{{
\t\t{{"maintenance.auto", "false"}},
\t\t{{"gc.auto", "0"}},
\t\t{{"user.name", "Shard Owner"}},
\t}} {{
\t\tout, err := exec.Command("git", "config", "--global", "--get-all", check[0]).CombinedOutput()
\t\tif err != nil {{ return check[0] + ": " + err.Error() + ": " + string(out) }}
\t\tif got := strings.TrimSpace(string(out)); got != check[1] {{
\t\t\treturn check[0] + "=" + got + " want " + check[1]
\t\t}}
\t}}
\treturn ""
}}
func TestMain(m *testing.M) {{
\tif problem := verifyGitEnv(); problem != "" {{
\t\tos.Stderr.WriteString("TestMain: " + problem + "\\n")
\t\tos.Exit(1)
\t}}
\tos.Exit(m.Run())
}}
func checkRepo(t *testing.T) {{
\tif problem := verifyGitEnv(); problem != "" {{ t.Fatal(problem) }}
\tdir := t.TempDir()
\tif out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {{
\t\tt.Fatalf("git init: %v: %s", err, out)
\t}}
\tif out, err := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-qm", "ok").CombinedOutput(); err != nil {{
\t\tt.Fatalf("git commit: %v: %s", err, out)
\t}}
}}
func TestAlpha(t *testing.T) {{ checkRepo(t) }}
func TestBeta(t *testing.T) {{ checkRepo(t) }}
''')
        durations = self.root / 'durations.json'
        durations.write_text('{}')
        events = self.root / 'events.jsonl'
        shimmed_path = os.pathsep.join([str(fake_guard_dir), str(fake_block), os.environ.get('PATH', '')])
        env = dict(os.environ, PATH=shimmed_path, GIT_CONFIG_GLOBAL=str(inherited_cfg))
        env.pop('YOLO_BYPASS_SHIMS', None)
        result = subprocess.run(
            ['python3', str(RUNNER), str(module), '-k', '2',
             '--durations', str(durations), '--events', str(events),
             '--lock-dir', str(self.root / 'gitshim-locks')],
            env=env, capture_output=True, text=True, timeout=60,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(shim_hits.exists(), shim_hits.read_text() if shim_hits.exists() else '')
        self.assertEqual(inherited_cfg.read_text(), inherited_text)
        passed = {
            e['Test'] for e in map(json.loads, events.read_text().splitlines())
            if e.get('Action') == 'pass' and e.get('Test')
        }
        self.assertEqual(passed, {'TestAlpha', 'TestBeta'})

    def test_pre_a45355b197_and_unversioned_a45355b197_reproduce_mutual_yield_while_current_code_progresses(self):
        self._enable_slot_limits()
        pre_mod = _load_shards_from_commit('a45355b197~1', 'shards_pre_a45355b197')
        unversioned_mod = _load_shards_from_commit('a45355b197', 'shards_at_a45355b197')

        class _StopMutualYield(Exception):
            pass

        # 1. Reproduce the mutual yield: pre-a45355b197 proof waiter and unversioned a45355b197
        #    gate waiter share lock_dir, hold each other's wait locks, and both stay stuck even
        #    after slot-0 and proof-0 are released and free.
        repro_dir = self.root / 'mutual-yield-repro'
        released_evt = threading.Event()
        stop_evt = threading.Event()
        spin_lock = threading.Lock()
        spins = {'old_proof': 0, 'new_gate': 0}
        both_spun = threading.Event()
        old_waiting = threading.Event()
        old_acquired = threading.Event()
        new_waiting = threading.Event()
        new_acquired = threading.Event()
        repro_errors = []
        repro_threads = []

        def make_sleep_hook(role, real_sleep):
            def hooked_sleep(seconds):
                if stop_evt.is_set():
                    raise _StopMutualYield()
                if released_evt.is_set():
                    with spin_lock:
                        spins[role] += 1
                        if spins['old_proof'] >= 5 and spins['new_gate'] >= 5:
                            both_spun.set()
                real_sleep(min(seconds, 0.002))
            return hooked_sleep

        blockers = ExitStack()
        with patch.object(pre_mod.time, 'sleep', side_effect=make_sleep_hook('old_proof', pre_mod.time.sleep)), \
             patch.object(unversioned_mod.time, 'sleep', side_effect=make_sleep_hook('new_gate', unversioned_mod.time.sleep)):
            try:
                slot_blocker = ExitStack()
                slot_blocker.enter_context(pre_mod.acquire_shard_slot(
                    repro_dir, 1, slot_class='ordinary', proof_limit=1, gate_limit=1,
                    holder='worker-slot-blocker',
                ))
                slot_blocker.enter_context(pre_mod.acquire_shard_slot(
                    repro_dir, 1, slot_class='proof', proof_limit=1, gate_limit=1,
                    holder='proof-slot-blocker',
                ))
                blockers.enter_context(unversioned_mod.acquire_shard_slot(
                    repro_dir, 1, slot_class='gate', proof_limit=1, gate_limit=1,
                    gate_reserved_limit=1, holder='gate:lane-4',
                ))

                def run_old_proof():
                    try:
                        with pre_mod.acquire_shard_slot(
                            repro_dir, 1, notify=lambda _m: old_waiting.set(),
                            slot_class='proof', proof_limit=1, gate_limit=1,
                            holder='trunk-proof',
                        ):
                            old_acquired.set()
                    except _StopMutualYield:
                        pass
                    except BaseException as exc:
                        repro_errors.append(exc)
                        old_waiting.set()
                        raise

                def run_new_gate():
                    try:
                        with unversioned_mod.acquire_shard_slot(
                            repro_dir, 1, notify=lambda _m: new_waiting.set(),
                            slot_class='gate', proof_limit=1, gate_limit=1,
                            gate_reserved_limit=1, holder='gate:lane-4',
                        ):
                            new_acquired.set()
                    except _StopMutualYield:
                        pass
                    except BaseException as exc:
                        repro_errors.append(exc)
                        new_waiting.set()
                        raise

                t_old = threading.Thread(target=run_old_proof)
                t_new = threading.Thread(target=run_new_gate)
                repro_threads.extend((t_old, t_new))
                t_old.start()
                t_new.start()

                self.assertTrue(old_waiting.wait(30), 'pre-a45355b197 proof did not enter wait')
                self.assertTrue(new_waiting.wait(30), 'a45355b197 gate did not enter wait')
                self.assertTrue(pre_mod.gate_caller_waiting(repro_dir))
                self.assertTrue(unversioned_mod.proof_caller_waiting(repro_dir))

                # Free both slot-0 and proof-0; both waiters still yield to each other.
                slot_blocker.close()
                released_evt.set()
                self.assertTrue(both_spun.wait(30), 'waiters did not spin after slots were freed')
                self.assertFalse(old_acquired.is_set(), 'pre-a45355b197 proof should stay deadlocked on unversioned gate-waiting.lock')
                self.assertFalse(new_acquired.is_set(), 'a45355b197 gate should stay deadlocked on unversioned proof-waiting.lock')
                self.assertTrue(pre_mod.gate_caller_waiting(repro_dir))
                self.assertTrue(unversioned_mod.proof_caller_waiting(repro_dir))

                # Prove both physical slots are actually free while the two waiters deadlock.
                free_slot_fd, free_slot_name = shards.try_lock_slot(repro_dir, 'slot', 1, 0)
                try:
                    self.assertEqual(free_slot_name, 'slot-0')
                finally:
                    shards._unlock_and_close(free_slot_fd)
                free_proof_fd, free_proof_name = shards.try_lock_slot(repro_dir, 'proof', 1, 0)
                try:
                    self.assertEqual(free_proof_name, 'proof-0')
                finally:
                    shards._unlock_and_close(free_proof_fd)
            finally:
                stop_evt.set()
                slot_blocker.close()
                blockers.close()
                for thread in repro_threads:
                    thread.join(30)
        if repro_errors:
            raise repro_errors[0]

        # 2. With current rules-versioned wait queues, pre-a45355b197 proof and current gate
        #    share physical locks in lock_dir without ever yielding to each other's wait locks.
        fixed_dir = self.root / 'mutual-yield-fixed'
        fixed_errors = []
        fixed_threads = []
        old_fixed_waiting = threading.Event()
        old_fixed_acquired = threading.Event()
        old_fixed_release = threading.Event()
        new_fixed_waiting = threading.Event()
        new_fixed_acquired = threading.Event()
        new_fixed_release = threading.Event()
        ord_fixed_waiting = threading.Event()
        ord_fixed_acquired = threading.Event()
        ord_fixed_release = threading.Event()

        fixed_blockers = ExitStack()
        fixed_slot_blockers = ExitStack()
        try:
            # Fill both ordinary slots (slot-0, slot-1), proof-0, and gate-0.
            for idx in range(2):
                fixed_slot_blockers.enter_context(pre_mod.acquire_shard_slot(
                    fixed_dir, 2, slot_class='ordinary', proof_limit=1, gate_limit=1,
                    holder=f'worker-blocker-{idx}',
                ))
            fixed_slot_blockers.enter_context(pre_mod.acquire_shard_slot(
                fixed_dir, 2, slot_class='proof', proof_limit=1, gate_limit=1,
                holder='proof-blocker',
            ))
            fixed_blockers.enter_context(shards.acquire_shard_slot(
                fixed_dir, 2, slot_class='gate', proof_limit=1, gate_limit=1,
                gate_reserved_limit=1, holder='gate:lane-4',
            ))

            def run_old_proof_fixed():
                try:
                    with pre_mod.acquire_shard_slot(
                        fixed_dir, 2, notify=lambda _m: old_fixed_waiting.set(),
                        slot_class='proof', proof_limit=1, gate_limit=1,
                        holder='trunk-proof',
                    ):
                        old_fixed_acquired.set()
                        assert old_fixed_release.wait(60)
                except BaseException as exc:
                    fixed_errors.append(exc)
                    old_fixed_waiting.set()
                    old_fixed_acquired.set()
                    raise

            def run_new_gate_fixed():
                try:
                    with shards.acquire_shard_slot(
                        fixed_dir, 2, notify=lambda _m: new_fixed_waiting.set(),
                        slot_class='gate', proof_limit=1, gate_limit=1,
                        gate_reserved_limit=1, holder='gate:lane-4',
                    ):
                        new_fixed_acquired.set()
                        assert new_fixed_release.wait(60)
                except BaseException as exc:
                    fixed_errors.append(exc)
                    new_fixed_waiting.set()
                    new_fixed_acquired.set()
                    raise

            t_old = threading.Thread(target=run_old_proof_fixed)
            t_new = threading.Thread(target=run_new_gate_fixed)
            fixed_threads.extend((t_old, t_new))
            t_old.start()
            t_new.start()

            self.assertTrue(old_fixed_waiting.wait(30))
            self.assertTrue(new_fixed_waiting.wait(30))

            # Wait-signaling namespaces are completely disjoint across rules versions.
            self.assertTrue(pre_mod.proof_caller_waiting(fixed_dir))
            self.assertFalse(shards.proof_caller_waiting(fixed_dir))
            self.assertTrue(shards.gate_caller_waiting(fixed_dir))
            self.assertFalse(pre_mod.gate_caller_waiting(fixed_dir))
            self.assertNotEqual(shards.wait_rules_dir(fixed_dir), fixed_dir)

            # Release slot-0, slot-1, and proof-0; both waiters progress immediately.
            fixed_slot_blockers.close()
            self.assertTrue(old_fixed_acquired.wait(30), 'pre-a45355b197 proof did not progress')
            self.assertTrue(new_fixed_acquired.wait(30), 'current gate did not progress')

            # Physical slot locks remain shared at fixed_dir across versions: when ordinary
            # slots are full (held by pre_mod and shards), an ordinary caller must still wait.
            extra_old_acquired = threading.Event()
            extra_old_release = threading.Event()
            holders_now = shards.read_slot_holders(fixed_dir)
            held_ordinary = sum(1 for h in holders_now if h['slot'].startswith('slot-'))
            if held_ordinary < 2:
                def hold_remaining_ordinary():
                    try:
                        with pre_mod.acquire_shard_slot(
                            fixed_dir, 2, slot_class='ordinary', proof_limit=1, gate_limit=1,
                            holder='old-worker-fill',
                        ):
                            extra_old_acquired.set()
                            assert extra_old_release.wait(60)
                    except BaseException as exc:
                        fixed_errors.append(exc)
                        extra_old_acquired.set()
                        raise

                t_fill = threading.Thread(target=hold_remaining_ordinary)
                fixed_threads.append(t_fill)
                t_fill.start()
                self.assertTrue(extra_old_acquired.wait(30))
            else:
                extra_old_release.set()

            def run_ordinary_waiter():
                try:
                    with shards.acquire_shard_slot(
                        fixed_dir, 2, notify=lambda _m: ord_fixed_waiting.set(),
                        slot_class='ordinary', proof_limit=1, gate_limit=1,
                        holder='current-worker',
                    ):
                        ord_fixed_acquired.set()
                        assert ord_fixed_release.wait(60)
                except BaseException as exc:
                    fixed_errors.append(exc)
                    ord_fixed_waiting.set()
                    ord_fixed_acquired.set()
                    raise

            t_ord = threading.Thread(target=run_ordinary_waiter)
            fixed_threads.append(t_ord)
            t_ord.start()
            self.assertTrue(ord_fixed_waiting.wait(30))
            self.assertFalse(ord_fixed_acquired.is_set())

            new_fixed_release.set()
            old_fixed_release.set()
            extra_old_release.set()
            self.assertTrue(ord_fixed_acquired.wait(30))
            ord_fixed_release.set()
        finally:
            fixed_slot_blockers.close()
            fixed_blockers.close()
            old_fixed_release.set()
            new_fixed_release.set()
            ord_fixed_release.set()
            for thread in fixed_threads:
                thread.join(30)
        if fixed_errors:
            raise fixed_errors[0]

    def test_different_slot_rules_versions_never_yield_to_each_other(self):
        self._enable_slot_limits()
        other_spec = importlib.util.spec_from_file_location('shards_other_version', RUNNER)
        other_mod = importlib.util.module_from_spec(other_spec)
        other_spec.loader.exec_module(other_mod)
        other_mod.ENFORCE_SLOT_LIMITS = True
        other_mod.SLOT_RULES_VERSION = 'v999'

        lock_dir = self.root / 'cross-version-isolation'
        other_rules_dir = other_mod.wait_rules_dir(lock_dir)
        other_rules_dir.mkdir(parents=True, exist_ok=True)
        other_wait_lock = other_rules_dir / 'repair-waiting.lock'
        other_queue_dir = other_mod.wait_queue_dir(lock_dir)
        other_queue_dir.mkdir(parents=True, exist_ok=True)

        # Simulate a higher-priority repair waiter running under rules version v999.
        repair_wait_fd = os.open(other_wait_lock, os.O_CREAT | os.O_RDWR, 0o666)
        ticket_fd, ticket_path, _, _ = other_mod._register_wait_ticket(other_queue_dir, 0, 'spg')
        try:
            fcntl.flock(repair_wait_fd, fcntl.LOCK_SH)
            self.assertTrue(other_mod.repair_caller_waiting(lock_dir))
            self.assertFalse(shards.repair_caller_waiting(lock_dir))

            # Current-version ordinary caller must take a free slot immediately with zero yield.
            notices = []
            with patch.object(shards.time, 'sleep', side_effect=AssertionError('unexpected yield across rules versions')):
                with shards.acquire_shard_slot(
                    lock_dir, 1, notify=notices.append,
                    slot_class='ordinary', proof_limit=0, gate_limit=0,
                    holder='current-worker',
                ):
                    self.assertEqual(notices, [])
        finally:
            other_mod._release_wait_ticket(ticket_fd, ticket_path)
            shards._unlock_and_close(repair_wait_fd)

    def test_slot_rules_version_pins_yield_and_pool_rules(self):
        self._enable_slot_limits()
        # Immutable record of slot rules by version. Any change to priority order, pool
        # eligibility, wait-lock signaling, or yield behavior must bump SLOT_RULES_VERSION
        # rather than mutating an existing version's rules in place.
        expected_v2 = {
            'priority': (
                shards.SLOT_CLASS_REPAIR,
                shards.SLOT_CLASS_PROOF,
                shards.SLOT_CLASS_GATE,
                shards.SLOT_CLASS_ORDINARY,
            ),
            'pools': {
                shards.SLOT_CLASS_REPAIR: ('s', 'p', 'g'),
                shards.SLOT_CLASS_PROOF: ('s', 'p'),
                shards.SLOT_CLASS_GATE: ('s', 'g'),
                shards.SLOT_CLASS_ORDINARY: ('s',),
            },
            'wait_locks': {
                shards.SLOT_CLASS_REPAIR: 'repair-waiting.lock',
                shards.SLOT_CLASS_PROOF: 'proof-waiting.lock',
                shards.SLOT_CLASS_GATE: 'gate-waiting.lock',
                shards.SLOT_CLASS_ORDINARY: None,
            },
            'yields_to': {
                shards.SLOT_CLASS_REPAIR: {
                    's': (),
                    'p': (),
                    'g': (),
                },
                shards.SLOT_CLASS_PROOF: {
                    's': (shards.SLOT_CLASS_REPAIR,),
                    'p': (shards.SLOT_CLASS_REPAIR,),
                },
                shards.SLOT_CLASS_GATE: {
                    's': (shards.SLOT_CLASS_REPAIR, shards.SLOT_CLASS_PROOF),
                    'g': (shards.SLOT_CLASS_REPAIR,),
                },
                shards.SLOT_CLASS_ORDINARY: {
                    's': (
                        shards.SLOT_CLASS_REPAIR,
                        shards.SLOT_CLASS_PROOF,
                        shards.SLOT_CLASS_GATE,
                    ),
                },
            },
        }
        self.assertEqual(
            shards.SLOT_RULES_VERSION, 'v2',
            'bump SLOT_RULES_VERSION and add its pinned rule entry when changing slot yield rules',
        )
        self.assertEqual(
            shards.SLOT_RULES, expected_v2,
            'SLOT_RULES changed without bumping SLOT_RULES_VERSION',
        )
        self.assertEqual(
            shards._CLASS_PRIORITY,
            {cls: idx for idx, cls in enumerate(shards.SLOT_RULES['priority'])},
        )

        lock_dir = self.root / 'rules-pin'
        self.assertEqual(
            shards.wait_rules_dir(lock_dir),
            lock_dir / 'wait-rules' / shards.SLOT_RULES_VERSION,
        )
        self.assertEqual(
            shards.wait_queue_dir(lock_dir),
            lock_dir / 'wait-rules' / shards.SLOT_RULES_VERSION / 'wait-queue',
        )

        class _Yielded(Exception):
            pass

        def try_acquire_once(case_dir, caller_class, *, limit, proof_limit, gate_limit):
            with patch.dict(os.environ, {shards.SLOT_CLASS_ENV: ''}), \
                 patch.object(shards.time, 'sleep', side_effect=_Yielded):
                try:
                    with shards.acquire_shard_slot(
                        case_dir, limit, slot_class=caller_class,
                        proof_limit=proof_limit, gate_limit=gate_limit,
                        holder=f'pin-{caller_class}',
                    ):
                        return True
                except _Yielded:
                    return False

        # Verify live acquire_shard_slot pool eligibility matches SLOT_RULES['pools'].
        for caller_class in shards.SLOT_CLASSES:
            pools = shards.SLOT_RULES['pools'][caller_class]
            for pool_char, proof_limit, gate_limit in (('p', 1, 0), ('g', 0, 1)):
                case_dir = self.root / f'pin-pool-{caller_class}-{pool_char}'
                case_dir.mkdir(parents=True, exist_ok=True)
                slot0_fd, slot0_name = shards.try_lock_slot(case_dir, 'slot', 1, 0)
                self.assertEqual(slot0_name, 'slot-0')
                try:
                    acquired = try_acquire_once(
                        case_dir, caller_class,
                        limit=1, proof_limit=proof_limit, gate_limit=gate_limit,
                    )
                    self.assertEqual(
                        acquired, pool_char in pools,
                        f'pool eligibility mismatch for class={caller_class} pool={pool_char}',
                    )
                finally:
                    shards._unlock_and_close(slot0_fd)

        # Verify live acquire_shard_slot yielding matches SLOT_RULES['yields_to'] for every
        # (caller_class, waiting_class, pool_char) triple.
        for caller_class in shards.SLOT_CLASSES:
            for pool_char in shards.SLOT_RULES['pools'][caller_class]:
                for waiting_class, wait_lock_name in shards.SLOT_RULES['wait_locks'].items():
                    if not wait_lock_name:
                        continue
                    case_dir = self.root / f'pin-yield-{caller_class}-{pool_char}-{waiting_class}'
                    rules_dir = shards.wait_rules_dir(case_dir)
                    rules_dir.mkdir(parents=True, exist_ok=True)
                    wait_fd = os.open(rules_dir / wait_lock_name, os.O_CREAT | os.O_RDWR, 0o666)
                    slot0_fd = None
                    try:
                        fcntl.flock(wait_fd, fcntl.LOCK_SH)
                        if pool_char == 's':
                            proof_limit, gate_limit = 0, 0
                        elif pool_char == 'p':
                            slot0_fd, _ = shards.try_lock_slot(case_dir, 'slot', 1, 0)
                            proof_limit, gate_limit = 1, 0
                        else:
                            slot0_fd, _ = shards.try_lock_slot(case_dir, 'slot', 1, 0)
                            proof_limit, gate_limit = 0, 1
                        acquired = try_acquire_once(
                            case_dir, caller_class,
                            limit=1, proof_limit=proof_limit, gate_limit=gate_limit,
                        )
                        expected_yield = (
                            waiting_class in shards.SLOT_RULES['yields_to'][caller_class][pool_char]
                        )
                        self.assertEqual(
                            not acquired, expected_yield,
                            f'yield mismatch for caller={caller_class} pool={pool_char} waiter={waiting_class}',
                        )
                    finally:
                        shards._unlock_and_close(slot0_fd)
                        shards._unlock_and_close(wait_fd)

    def _run_shards_cli(self, args, *, env=None, cwd=None):
        merged_env = os.environ.copy()
        if env is not None:
            merged_env.update(env)
        merged_env.setdefault('GOCACHE', str(self.root / 'gocache'))
        merged_env.setdefault('GO_TEST_SHARDS_LOCK_DIR', str(self.root / 'default-locks'))
        return subprocess.run(
            [sys.executable, str(ROOT / 'tools' / 'go-test-shards.py'), *args],
            cwd=cwd or self.root,
            env=merged_env,
            capture_output=True,
            text=True,
        )

    def test_cached_sub_threshold_runs_single_group_and_hits_across_worktrees(self):
        repo = self.root / 'wt-repo'
        repo.mkdir()
        subprocess.run(['git', 'init', '-q'], cwd=repo, check=True)
        subprocess.run(['git', 'config', 'user.name', 'Test'], cwd=repo, check=True)
        subprocess.run(['git', 'config', 'user.email', 'test@example.com'], cwd=repo, check=True)
        mod = repo / 'mod'
        (mod / 'sub').mkdir(parents=True)
        (mod / 'lib').mkdir(parents=True)
        (mod / 'go.mod').write_text('module example.com/wtmod\n\ngo 1.22\n')
        (mod / 'lib' / 'lib.go').write_text('package lib\nfunc Val() int { return 1 }\n')
        (mod / 'sub' / 'sub_test.go').write_text(
            'package sub\n'
            'import (\n'
            '    "os"\n'
            '    "testing"\n'
            ')\n'
            'func TestAlpha(t *testing.T) {}\n'
            'func Example() {\n'
            '    // Output:\n'
            '}\n'
            'func TestMain(m *testing.M) { os.Exit(m.Run()) }\n'
        )
        subprocess.run(['git', 'add', '.'], cwd=repo, check=True)
        subprocess.run(['git', 'commit', '-q', '-m', 'init'], cwd=repo, check=True)
        wt2 = self.root / 'wt-two'
        subprocess.run(['git', 'worktree', 'add', '-q', str(wt2), 'HEAD'], cwd=repo, check=True)

        gocache = self.root / 'wt-gocache'
        durable = self.root / 'wt-durable'
        ev1 = self.root / 'ev1.json'
        ev2 = self.root / 'ev2.json'
        ev3 = self.root / 'ev3.json'
        events_file = self.root / 'events.jsonl'
        progress_file = self.root / 'progress.json'
        durations_file = self.root / 'durations.json'
        durations_file.write_text('{}\n')

        clean_env = {
            'GOCACHE': str(gocache),
            'YOLO_DURABLE_DIR': str(durable),
            'TILLR_RUN_ID': '',
        }

        # Run 1 in worktree 1: miss (1 group, no go test -c)
        res1 = self._run_shards_cli([
            str(mod), '-k', '6', '--cache', '--durations', str(durations_file),
            '--events', str(events_file), '--progress-file', str(progress_file),
            '--progress-interval', '0.01', '--record', str(durations_file),
        ], env={**clean_env, 'TILLR_GO_SHARDS_EVIDENCE_OUT': str(ev1)})
        self.assertEqual(res1.returncode, 0, res1.stdout + res1.stderr)
        data1 = json.loads(ev1.read_text())
        self.assertEqual(data1['mode'], 'cached-shards')
        by_ip1 = {p['import_path']: p['test_cache'] for p in data1['packages']}
        self.assertEqual(by_ip1, {
            'example.com/wtmod/lib': 'not-applicable',
            'example.com/wtmod/sub': 'miss',
        })
        recorded = json.loads(durations_file.read_text())
        self.assertIn('TestAlpha', recorded.get('example.com/wtmod/sub', {}))
        self.assertTrue(events_file.read_text().strip())
        if progress_file.exists():
            prog = json.loads(progress_file.read_text())
            self.assertGreaterEqual(prog['completed_packages'], 1)
            self.assertGreaterEqual(prog['completed_shards'], 1)

        # Run 2 in worktree 2: cross-worktree cache hit under -trimpath!
        # Overwrite durations with sentinel to verify --record does not overwrite from (cached) groups.
        durations_file.write_text('{"example.com/wtmod/sub": {"TestAlpha": 9.99}}\n')
        res2 = self._run_shards_cli([
            str(wt2 / 'mod'), '-k', '6', '--cache', '--durations', str(durations_file),
            '--record', str(durations_file),
        ], env={**clean_env, 'TILLR_GO_SHARDS_EVIDENCE_OUT': str(ev2)})
        self.assertEqual(res2.returncode, 0, res2.stdout + res2.stderr)
        data2 = json.loads(ev2.read_text())
        by_ip2 = {p['import_path']: p['test_cache'] for p in data2['packages']}
        self.assertEqual(by_ip2['example.com/wtmod/sub'], 'hit')
        self.assertEqual(json.loads(durations_file.read_text())['example.com/wtmod/sub']['TestAlpha'], 9.99)

        # Run 3 without --cache: default uncached mode still bypasses Go's test cache.
        res3 = self._run_shards_cli([
            str(mod), '-k', '2', '--durations', str(durations_file),
        ], env={**clean_env, 'TILLR_GO_SHARDS_EVIDENCE_OUT': str(ev3)})
        self.assertEqual(res3.returncode, 0, res3.stdout + res3.stderr)
        data3 = json.loads(ev3.read_text())
        self.assertIsInstance(data3, list)
        by_ip3 = {p['import_path']: p['test_cache'] for p in data3}
        self.assertEqual(by_ip3['example.com/wtmod/sub'], 'bypassed')

    def test_cached_large_package_shards_with_formula_and_hits_on_repeat(self):
        mod = self.root / 'largemod'
        mod.mkdir()
        (mod / 'go.mod').write_text('module example.com/largemod\n\ngo 1.22\n')
        lines = ['package largemod', 'import "testing"']
        for i in range(105):
            lines.append(f'func TestCase{i:03d}(t *testing.T) {{}}')
        (mod / 'large_test.go').write_text('\n'.join(lines) + '\n')

        gocache = self.root / 'large-gocache'
        durable = self.root / 'large-durable'
        ev1 = self.root / 'large-ev1.json'
        ev2 = self.root / 'large-ev2.json'
        clean_env = {
            'GOCACHE': str(gocache),
            'YOLO_DURABLE_DIR': str(durable),
            'TILLR_RUN_ID': '',
        }

        # Spy on shard count by checking scratch keys / invocations via run()
        seen_keys = []
        orig_run_group = shards.run_cached_group

        def spy_group(package, cwd, names, index, scratch_key, *args, **kwargs):
            seen_keys.append((index, scratch_key, len(names)))
            return orig_run_group(package, cwd, names, index, scratch_key, *args, **kwargs)

        with patch.object(shards, 'run_cached_group', side_effect=spy_group), \
             patch.dict(os.environ, clean_env):
            shards.run(mod, 3, {}, self.root / 's1', '0', cache=True, evidence_out=ev1)
            self.assertEqual(len(seen_keys), 3)
            self.assertEqual(json.loads(ev1.read_text())['packages'][0]['test_cache'], 'miss')

            seen_keys.clear()
            shards.run(mod, 3, {}, self.root / 's2', '0', cache=True, evidence_out=ev2)
            self.assertEqual(len(seen_keys), 3)
            self.assertEqual(json.loads(ev2.read_text())['packages'][0]['test_cache'], 'hit')

    def test_cached_edge_digest_invalidation_and_cli_edge_digest(self):
        repo = self.root / 'edge-repo'
        (repo / 'tools' / 'gate').mkdir(parents=True)
        (repo / 'tools' / 'tillr-helper').mkdir(parents=True)
        (repo / 'tools' / 'tillr-app').mkdir(parents=True)
        (repo / 'data').mkdir(parents=True)

        (repo / 'tools' / 'tillr-helper' / 'go.mod').write_text(
            'module github.com/mschulkind-oss/tillr-helper\n\ngo 1.22\n'
        )
        (repo / 'tools' / 'tillr-helper' / 'main.go').write_text(
            'package main\nfunc main() {}\n'
        )
        (repo / 'tools' / 'tillr-app' / 'go.mod').write_text(
            'module github.com/mschulkind-oss/tillr-app\n\ngo 1.22\n'
        )
        (repo / 'tools' / 'tillr-app' / 'app_test.go').write_text(
            'package app\n'
            'import (\n'
            '    "os"\n'
            '    "testing"\n'
            ')\n'
            'func TestEdgeInputs(t *testing.T) { _ = os.Getenv("TILLR_GATE_EDGE_INPUTS") }\n'
            'func TestApp(t *testing.T) { _ = os.Getenv("TILLR_GATE_EDGE_INPUTS") }\n'
        )
        (repo / 'data' / 'input.txt').write_text('v1\n')
        (repo / 'tools' / 'gate' / 'go-test-edges.json').write_text(json.dumps({
            'github.com/mschulkind-oss/tillr-app': {
                'builds': ['github.com/mschulkind-oss/tillr-helper'],
                'reads': ['data/input.txt'],
            },
        }))

        # CLI --edge-digest
        d1 = self._run_shards_cli(['--edge-digest', 'github.com/mschulkind-oss/tillr-app'], cwd=repo)
        self.assertEqual(d1.returncode, 0, d1.stderr)
        self.assertRegex(d1.stdout.strip(), r'^[0-9a-f]{64}$')
        d_missing = self._run_shards_cli(['--edge-digest', 'github.com/mschulkind-oss/unknown'], cwd=repo)
        self.assertEqual(d_missing.returncode, 0, d_missing.stderr)
        self.assertEqual(d_missing.stdout, '')

        gocache = self.root / 'edge-gocache'
        durable = self.root / 'edge-durable'
        clean_env = {'GOCACHE': str(gocache), 'YOLO_DURABLE_DIR': str(durable), 'TILLR_RUN_ID': ''}
        ev1 = self.root / 'e-ev1.json'
        ev2 = self.root / 'e-ev2.json'
        ev3 = self.root / 'e-ev3.json'
        ev4 = self.root / 'e-ev4.json'

        self.assertEqual(self._run_shards_cli(
            [str(repo / 'tools' / 'tillr-app'), '--cache'],
            env={**clean_env, 'TILLR_GO_SHARDS_EVIDENCE_OUT': str(ev1)},
        ).returncode, 0)
        self.assertEqual(json.loads(ev1.read_text())['packages'][0]['test_cache'], 'miss')

        self.assertEqual(self._run_shards_cli(
            [str(repo / 'tools' / 'tillr-app'), '--cache'],
            env={**clean_env, 'TILLR_GO_SHARDS_EVIDENCE_OUT': str(ev2)},
        ).returncode, 0)
        self.assertEqual(json.loads(ev2.read_text())['packages'][0]['test_cache'], 'hit')

        # Changing declared reads invalidates cache
        (repo / 'data' / 'input.txt').write_text('v2\n')
        self.assertEqual(self._run_shards_cli(
            [str(repo / 'tools' / 'tillr-app'), '--cache'],
            env={**clean_env, 'TILLR_GO_SHARDS_EVIDENCE_OUT': str(ev3)},
        ).returncode, 0)
        self.assertEqual(json.loads(ev3.read_text())['packages'][0]['test_cache'], 'miss')

        # Changing declared builds invalidates cache
        (repo / 'tools' / 'tillr-helper' / 'main.go').write_text('package main\nfunc main() { _ = 1 }\n')
        self.assertEqual(self._run_shards_cli(
            [str(repo / 'tools' / 'tillr-app'), '--cache'],
            env={**clean_env, 'TILLR_GO_SHARDS_EVIDENCE_OUT': str(ev4)},
        ).returncode, 0)
        self.assertEqual(json.loads(ev4.read_text())['packages'][0]['test_cache'], 'miss')

    def test_cached_failure_formatting_and_run_id_refusal(self):
        mod = self.root / 'failmod'
        mod.mkdir()
        (mod / 'go.mod').write_text('module example.com/failmod\n\ngo 1.22\n')
        (mod / 'fail_test.go').write_text(
            'package failmod\nimport "testing"\n'
            'func TestBoom(t *testing.T) { t.Fatal("kaboom") }\n'
        )
        res = self._run_shards_cli(
            [str(mod), '--cache'],
            env={'GOCACHE': str(self.root / 'fail-gocache'), 'YOLO_DURABLE_DIR': str(self.root / 'fail-durable'), 'TILLR_RUN_ID': ''},
        )
        self.assertNotEqual(res.returncode, 0)
        self.assertIn('example.com/failmod shard 0 failed (exit 1): TestBoom', res.stderr)
        self.assertIn('kaboom', res.stderr)

        # TILLR_RUN_ID refusal on repo module and .forks/, while allowing $TMPDIR fixture
        fake_repo = self.root / 'fake-repo'
        (fake_repo / 'pkg').mkdir(parents=True)
        (fake_repo / 'pkg' / 'go.mod').write_text('module example.com/repom\n\ngo 1.22\n')
        (fake_repo / 'pkg' / 'p_test.go').write_text('package pkg\nimport "testing"\nfunc TestOk(t *testing.T) {}\n')
        refused_repo = self._run_shards_cli(
            [str(fake_repo / 'pkg'), '--cache'],
            env={'TILLR_RUN_ID': 'run-worker-1', 'TILLR_REPO_ROOT': str(fake_repo)},
        )
        self.assertNotEqual(refused_repo.returncode, 0)
        self.assertIn('refused', refused_repo.stderr)

        forks_mod = self.root / '.forks' / 'lanes' / 'lane-1' / 'tillr'
        forks_mod.mkdir(parents=True)
        refused_forks = self._run_shards_cli(
            [str(forks_mod), '--cache'],
            env={'TILLR_RUN_ID': 'run-worker-1'},
        )
        self.assertNotEqual(refused_forks.returncode, 0)
        self.assertIn('.forks/', refused_forks.stderr)

        # Private fixture under $YOLO_DURABLE_DIR / $TMPDIR is allowed even with TILLR_RUN_ID set
        allowed = self._run_shards_cli(
            [str(fake_repo / 'pkg'), '--cache'],
            env={
                'TILLR_RUN_ID': 'run-worker-1',
                'TILLR_REPO_ROOT': str(ROOT),
                'YOLO_DURABLE_DIR': str(self.root),
                'GOCACHE': str(self.root / 'allow-gocache'),
            },
        )
        self.assertEqual(allowed.returncode, 0, allowed.stdout + allowed.stderr)

    def test_cached_cross_tool_go_affected_hits_go_test_shards(self):
        go_affected_spec = importlib.util.spec_from_file_location(
            'go_affected_cross_test', ROOT / 'tools/gate/go_affected.py'
        )
        go_affected = importlib.util.module_from_spec(go_affected_spec)
        go_affected_spec.loader.exec_module(go_affected)

        repo = self.root / 'cross-repo'
        (repo / 'tools' / 'gate').mkdir(parents=True)
        (repo / 'tools' / 'go-test-shards.py').write_text(RUNNER.read_text())
        (repo / 'data').mkdir(parents=True)
        (repo / 'data' / 'edge.txt').write_text('v1\n')

        small_mod = repo / 'tools' / 'tillr-small'
        small_mod.mkdir()
        (small_mod / 'go.mod').write_text('module github.com/mschulkind-oss/tillr-small\n\ngo 1.22\n')
        (small_mod / 'small_test.go').write_text(
            'package small\n'
            'import (\n'
            '    "os"\n'
            '    "testing"\n'
            ')\n'
            'func TestEdgeInputs(t *testing.T) { _ = os.Getenv("TILLR_GATE_EDGE_INPUTS") }\n'
            'func TestSmallA(t *testing.T) {}\n'
            'func TestSmallB(t *testing.T) {}\n'
        )

        large_mod = repo / 'tools' / 'tillr-large'
        large_mod.mkdir()
        (large_mod / 'go.mod').write_text('module github.com/mschulkind-oss/tillr-large\n\ngo 1.22\n')
        large_lines = [
            'package large',
            'import (',
            '    "os"',
            '    "testing"',
            ')',
            'func TestEdgeInputs(t *testing.T) { _ = os.Getenv("TILLR_GATE_EDGE_INPUTS") }',
        ]
        for i in range(105):
            large_lines.append(f'func TestLarge{i:03d}(t *testing.T) {{}}')
        (large_mod / 'large_test.go').write_text('\n'.join(large_lines) + '\n')

        (repo / 'tools' / 'gate' / 'go-test-edges.json').write_text(json.dumps({
            'github.com/mschulkind-oss/tillr-small': {
                'reads': ['data/edge.txt'],
            },
            'github.com/mschulkind-oss/tillr-large': {
                'reads': ['data/edge.txt'],
            },
        }))

        gocache = self.root / 'cross-gocache'
        durable = self.root / 'cross-durable'
        lock_dir = self.root / 'cross-locks'
        clean_env = {
            'GOCACHE': str(gocache),
            'YOLO_DURABLE_DIR': str(durable),
            'GO_TEST_SHARDS_LOCK_DIR': str(lock_dir),
            'TILLR_RUN_ID': '',
        }

        with patch.dict(os.environ, clean_env):
            sel = go_affected.select_affected_packages(
                repo,
                ['tools/tillr-small/small_test.go', 'tools/tillr-large/large_test.go'],
            )
            rc, out, ev_gate = go_affected.run_selected_tests(repo, sel, progress_stream=False)
            self.assertEqual(rc, 0, out)
            self.assertEqual(len(ev_gate['packages']), 2)

            ev_small = self.root / 'cross-ev-small.json'
            shards.run(small_mod, 6, {}, self.root / 'cs-small', '0', cache=True, evidence_out=ev_small)
            small_payload = json.loads(ev_small.read_text())
            self.assertEqual(small_payload['packages'][0]['test_cache'], 'hit')

            ev_large = self.root / 'cross-ev-large.json'
            shards.run(large_mod, 6, {}, self.root / 'cs-large', '0', cache=True, evidence_out=ev_large)
            large_payload = json.loads(ev_large.read_text())
            self.assertEqual(large_payload['packages'][0]['test_cache'], 'hit')


if __name__ == '__main__':
    unittest.main()
