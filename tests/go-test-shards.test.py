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


if __name__ == '__main__':
    unittest.main()
