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
        for cores, expected in [(32, 16), (4, 2), (2, 1), (1, 1), (None, 1)]:
            with patch.object(os, 'cpu_count', return_value=cores):
                self.assertEqual(shards.default_shard_limit(), expected)

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


if __name__ == '__main__':
    unittest.main()
