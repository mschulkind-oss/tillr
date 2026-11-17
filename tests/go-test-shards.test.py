#!/usr/bin/env python3
"""Permanent runner regression tests; parity also exercises the real drain suite."""
from contextlib import redirect_stderr
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
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
        module = ROOT / 'tools/tillr-drain'
        baseline = subprocess.run(['go', 'test', '-json', './...', '-count=1'],
                                  cwd=module, capture_output=True, text=True)
        self.assertEqual(baseline.returncode, 0, baseline.stdout + baseline.stderr)
        report = self.root / 'drain.jsonl'
        result = subprocess.run(['python3', str(RUNNER), str(module), '--events', str(report)],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        def passed(lines):
            return {(e['Package'], e['Test']) for e in map(json.loads, lines.splitlines())
                    if e.get('Action') == 'pass' and e.get('Test')}
        self.assertEqual(passed(baseline.stdout), passed(report.read_text()))


if __name__ == '__main__':
    unittest.main()
