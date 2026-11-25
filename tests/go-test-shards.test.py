#!/usr/bin/env python3
"""Permanent runner regression tests; parity lists and plans the real drain suite without running it."""
from contextlib import ExitStack, contextmanager, redirect_stderr
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


class RunnerTests(unittest.TestCase):
    def setUp(self):
        parent = os.environ.get('TMPDIR') or os.environ.get('YOLO_DURABLE_DIR')
        if not parent:
            raise RuntimeError('tests require TMPDIR or YOLO_DURABLE_DIR, never /tmp')
        self.tmp = tempfile.TemporaryDirectory(dir=parent)
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        env_patch = patch.dict(os.environ)
        env_patch.start()
        self.addCleanup(env_patch.stop)
        os.environ.pop(shards.PROGRESS_OUT_ENV, None)

    def test_package_evidence_distinguishes_shards_from_go_result_cache(self):
        target = self.root / 'packages.json'
        packages = [{'ImportPath': 'demo/test', 'TestGoFiles': ['x_test.go']},
                    {'ImportPath': 'demo/library'}]
        with patch.object(shards.subprocess, 'check_output', return_value='\n'.join(map(json.dumps, packages))), \
             patch.object(shards, 'global_git_config', return_value={}), \
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


    def fake(self, body):
        script = self.root / 'fake'
        script.write_text('#!/usr/bin/env python3\n' + body)
        script.chmod(0o755)
        return script

    def assert_shard_exit(self, binary, names, message, scratch=None):
        # Exercise the CLI exit contract with a real shard and test2json process.
        scratch_dir = scratch or (self.root / 'scratch')
        def run(*args, **kwargs):
            return shards.run_shard('fixture', self.root, binary, names, 0,
                                    scratch_dir, '3600s')
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

    def test_shard_exit_handles_progress_env_set_and_unset(self):
        binary = self.fake("print('=== RUN   TestFail'); print('--- FAIL: TestFail (0.00s)'); print('FAIL'); raise SystemExit(1)\n")
        with patch.dict(os.environ, {shards.PROGRESS_OUT_ENV: str(self.root / 'env-progress.json')}):
            self.assert_shard_exit(binary, ['TestFail'], 'failed', scratch=self.root / 'scratch-set')
            self.assertNotIn(shards.PROGRESS_OUT_ENV, os.environ)
        env_without = os.environ.copy()
        env_without.pop(shards.PROGRESS_OUT_ENV, None)
        with patch.dict(os.environ, env_without, clear=True):
            self.assert_shard_exit(binary, ['TestFail'], 'failed', scratch=self.root / 'scratch-unset')

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
        def run(*args, **kwargs):
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
            self.assertNotIn(shards.PROGRESS_OUT_ENV, os.environ)
        self.assertEqual(seen_env_during_run, [None])
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
            self.assertNotIn(shards.PROGRESS_OUT_ENV, os.environ)
        self.assertEqual(seen_env_during_run, [None])
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
            ['python3', str(RUNNER), str(mod), '--durations', str(record)],
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
                progress_file=progress_file,
                progress_interval=60.0,
            )
        self.assertTrue(any(e.get('Test') == 'TestFast' and e.get('Action') == 'pass' for e in events))
        self.assertFalse(progress_file.exists())

    def test_progress_writes_zero_snapshots_when_stalled_in_running_test(self):
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
            # Action == 'run' hang writes zero snapshots; mid-run hang stops advancing seq.
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
                progress_file=self.root,
                progress_interval=0.02,
            )
        self.assertTrue(any(e.get('Test') == 'TestSlow' and e.get('Action') == 'pass' for e in events))
        self.assertIn('cannot write progress file', stderr.getvalue())
        self.assertEqual(list(self.root.parent.glob('.go-progress-*')), [])





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
             '--durations', str(durations), '--events', str(events)],
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



    def _run_shards_cli(self, args, *, env=None, cwd=None):
        merged_env = os.environ.copy()
        if env is not None:
            merged_env.update(env)
        merged_env.setdefault('GOCACHE', str(self.root / 'gocache'))
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
        clean_env = {
            'GOCACHE': str(gocache),
            'YOLO_DURABLE_DIR': str(durable),
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

    def test_single_test_shard_sets_gomaxprocs_and_p1(self):
        captured = []

        class FakeProc:
            pid = 99999
            returncode = 0

            def poll(self):
                return 0

            def wait(self, timeout=None):
                return 0

        def fake_popen(args, **kwargs):
            captured.append((args, kwargs.get('env', {})))
            stdout = kwargs.get('stdout')
            if stdout is not None:
                stdout.write(b'{"Action":"pass","Package":"example.com/pkg","Test":"TestOne"}\n{"Action":"pass","Package":"example.com/pkg"}\n')
                stdout.flush()
            return FakeProc()

        with tempfile.TemporaryDirectory() as tmp:
            tmp_path = Path(tmp)
            fake_bin = tmp_path / 'pkg.test'
            fake_bin.write_text('#!/bin/sh\nexit 0\n')
            fake_bin.chmod(0o755)
            with patch.object(shards.subprocess, 'Popen', side_effect=fake_popen):
                shards.run_shard('example.com/pkg', tmp_path, fake_bin, ['TestOne'], 0, tmp_path / 'shard-scratch', '10s', git_values=[])
                self.assertEqual(len(captured), 1)
                args_shard, env_shard = captured[0]
                self.assertIn('-test.parallel=1', args_shard)
                self.assertEqual(env_shard.get('GOMAXPROCS'), '1')

                captured.clear()
                shards.run_cached_group(
                    'example.com/pkg',
                    tmp_path,
                    ['TestOne'],
                    0,
                    'k1',
                    tmp_path / 'durable',
                    '10s',
                    {'PATH': os.environ.get('PATH', '')},
                    [],
                    run_pattern='^TestOne$',
                )
                self.assertEqual(len(captured), 1)
                args_group, env_group = captured[0]
                cmd_part = args_group[args_group.index('--') + 1:]
                self.assertIn('-parallel', cmd_part)
                self.assertEqual(cmd_part[cmd_part.index('-parallel') + 1], '1')
                self.assertIn('-p', cmd_part)
                self.assertEqual(cmd_part[cmd_part.index('-p') + 1], '1')
                self.assertEqual(env_group.get('GOMAXPROCS'), '1')

    def test_max_shards_scales_with_cpu_count(self):
        """MAX_SHARDS matches go_affected (2 * os.cpu_count(), 12 when undetermined) and scales past 12."""
        go_affected_spec = importlib.util.spec_from_file_location(
            'go_affected_max_shards_test', ROOT / 'tools/gate/go_affected.py'
        )
        go_affected = importlib.util.module_from_spec(go_affected_spec)
        go_affected_spec.loader.exec_module(go_affected)

        host_cpus = os.cpu_count()
        expected_host = 2 * host_cpus if host_cpus and host_cpus > 0 else 12
        self.assertEqual(shards.MAX_SHARDS, expected_host)
        self.assertEqual(shards.MAX_SHARDS, go_affected.MAX_SHARDS)

        def load_shards(name):
            mod_spec = importlib.util.spec_from_file_location(name, RUNNER)
            mod = importlib.util.module_from_spec(mod_spec)
            mod_spec.loader.exec_module(mod)
            return mod

        loaded_by_cpus = {}
        for cpus in (1, 4, 8, 16, 32):
            with patch.object(os, 'cpu_count', return_value=cpus):
                mod = load_shards(f'shards_cpus_{cpus}')
            self.assertEqual(mod.MAX_SHARDS, 2 * cpus)
            loaded_by_cpus[cpus] = mod

        for missing in (None, 0, -1):
            with patch.object(os, 'cpu_count', return_value=missing):
                mod = load_shards(f'shards_missing_{missing}')
            self.assertEqual(mod.MAX_SHARDS, 12)

        pkg = 'example.com/tx/sharded'
        pkg_dir = self.root / 'sharded'
        pkg_dir.mkdir()

        def count_cached_shards(mod, total_tests):
            lines = ['package sharded', 'import "testing"']
            lines.extend(f'func TestCase{i:04d}(t *testing.T) {{}}' for i in range(total_tests))
            (pkg_dir / 'sharded_test.go').write_text('\n'.join(lines) + '\n')

            pkg_json = json.dumps({
                'ImportPath': pkg,
                'Dir': str(pkg_dir),
                'TestGoFiles': ['sharded_test.go'],
            })
            seen = []
            seen_lock = threading.Lock()

            def fake_run_cached_group(import_path, *_args, **_kwargs):
                with seen_lock:
                    seen.append(import_path)
                return ([{'Action': 'pass', 'Package': import_path}], 'miss')

            with (
                patch.object(mod.subprocess, 'check_output', return_value=pkg_json + '\n'),
                patch.object(mod, 'run_cached_group', side_effect=fake_run_cached_group),
                patch.object(mod, '_load_go_affected', return_value=go_affected),
                patch.object(go_affected, '_global_git_config', return_value=[]),
            ):
                mod.run(pkg_dir, 6, {}, self.root / 'scratch', '0', cache=True, durable_dir=self.root / 'durable')
            return len(seen)

        # 2,500 tests -> 2,500 // 125 = 20 uncapped shards.
        # On 8 processors (MAX_SHARDS = 16), scales past 12 and caps at 16;
        # on 16 processors (MAX_SHARDS = 32), runs all 20 shards and caps 5,000 tests (40 uncapped) at 32.
        self.assertEqual(count_cached_shards(loaded_by_cpus[8], 2500), 16)
        self.assertEqual(count_cached_shards(loaded_by_cpus[16], 2500), 20)
        self.assertEqual(count_cached_shards(loaded_by_cpus[16], 5000), 32)


if __name__ == '__main__':
    unittest.main()
