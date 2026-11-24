from contextlib import closing, redirect_stdout
import hashlib
from importlib.machinery import SourceFileLoader
import importlib.util
import io
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import sqlite3
import re
import subprocess
import sys
import threading
import tempfile
import time
import tomllib
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location(
    'tillr_roll', ROOT / 'tools/tillr-roll/tillr-roll.py')
roll = importlib.util.module_from_spec(spec)
spec.loader.exec_module(roll)

keep_spec = importlib.util.spec_from_loader(
    'tillr_keep', SourceFileLoader('tillr_keep', str(ROOT / 'tools/tillr-roll/tillr-keep')))
keep = importlib.util.module_from_spec(keep_spec)
keep_spec.loader.exec_module(keep)


class RollTest(unittest.TestCase):
    def test_repository_enables_roll_and_push_and_explains_the_install(self):
        settings = tomllib.loads((ROOT / '.tillr.toml').read_text())
        self.assertEqual(settings['roll'], {
            'paths': ['tools/tillr*/', 'config/tillr/'],
            'command': 'just tillr-roll --no-request',
            'pause_timeout_seconds': 120,
        })
        self.assertEqual(settings['push'], {'what': 'green'})
        sections = {
            'tools/tillr-drain/README.md': 'Moving a running loop onto new code',
            'yolo-packs/tillr/skills/tillr-operating/SKILL.md': 'Update a running drain',
        }
        for path, heading in sections.items():
            with self.subTest(path=path):
                body = (ROOT / path).read_text().split(f'## {heading}\n', 1)[1].split('\n## ', 1)[0].strip()
                self.assertLessEqual(len(body.splitlines()), 5)
                for command in ('just tillr-build', 'just tillr-roll', 'just tillr-restart', 'yolo host apply --assert'):
                    self.assertIn(command, body)

    def test_late_build_failure_preserves_every_live_binary(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work) / 'repo'
            root.mkdir()
            live = Path(work) / 'bin'
            live.mkdir()

            def git(*args):
                return subprocess.check_output(
                    ['git', '-C', root, '-c', 'user.name=Fixture',
                     '-c', 'user.email=fixture@example.invalid', *args], text=True).strip()

            git('init', '-q')
            (root / '.gitignore').write_text('/.tillr/\n')
            (root / '.justfile').write_bytes((ROOT / '.justfile').read_bytes())
            script = root / 'tools/tillr-roll/tillr-roll.py'
            script.parent.mkdir(parents=True)
            script.write_bytes(Path(roll.__file__).read_bytes())
            for module in roll.MODULES:
                source = root / 'tools' / f'tillr-{module}'
                source.mkdir()
                (source / 'go.mod').write_text(f'module example.invalid/{module}\n\ngo 1.23\n')
                main = 'package main\nfunc main() {}\n' if module != 'view' else 'broken view source\n'
                (source / 'main.go').write_text(main)
                name = 'tillr' if module == 'cli' else f'tillr-{module}'
                (live / name).write_text(f'old {name}')
            git('add', '.')
            git('commit', '-qm', 'fixture source')
            revision = git('rev-parse', 'HEAD')
            (root / '.tillr').mkdir()
            with closing(sqlite3.connect(root / '.tillr/state.db')) as db, db:
                db.execute('CREATE TABLE events(seq INTEGER PRIMARY KEY, ts INTEGER NOT NULL DEFAULT 0, kind TEXT, object_type TEXT, object_id TEXT, payload TEXT)')
                db.executemany('INSERT INTO events(kind, payload) VALUES (?, ?)', (
                    ('drain.build-started', json.dumps({'role': role, 'revision': revision}))
                    for role in ('drain', 'view')))

            def checksums():
                return {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in live.iterdir()}

            before = checksums()
            result = subprocess.run(['just', 'tillr-roll', '--no-request'], cwd=root,
                                    env={**os.environ, 'YOLO_DURABLE_DIR': work},
                                    text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
            self.assertNotEqual(result.returncode, 0, result.stdout)
            self.assertIn("expected 'package', found broken", result.stdout)
            self.assertIn('tillr-view', result.stdout)
            self.assertEqual(checksums(), before)

    def fixture(self, work, view_source='package main\nfunc main() {}\n'):
        """A repository of eight no-op tillr modules, a store with the build facts, a live bin."""
        root = Path(work) / 'repo'
        root.mkdir()
        live = Path(work) / 'bin'
        live.mkdir()

        def git(*args):
            return subprocess.check_output(
                ['git', '-C', root, '-c', 'user.name=Fixture',
                 '-c', 'user.email=fixture@example.invalid', *args], text=True).strip()

        git('init', '-q')
        (root / '.gitignore').write_text('/.tillr/\n')
        for module in roll.MODULES:
            source = root / 'tools' / f'tillr-{module}'
            source.mkdir(parents=True)
            (source / 'go.mod').write_text(f'module example.invalid/{module}\n\ngo 1.23\n')
            (source / 'main.go').write_text(view_source if module == 'view' else 'package main\nfunc main() {}\n')
        (root / 'tools/go-test-shards.py').write_text('')
        git('add', '.')
        git('commit', '-qm', 'fixture source')
        revision = git('rev-parse', 'HEAD')
        (root / '.tillr').mkdir()
        with closing(sqlite3.connect(root / '.tillr/state.db')) as db, db:
            db.execute('CREATE TABLE events(seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, kind TEXT NOT NULL,'
                       ' actor TEXT, seat TEXT, session_id TEXT, object_type TEXT, object_id TEXT, revision INTEGER,'
                       ' payload TEXT NOT NULL DEFAULT \'{}\')')
            db.executemany('INSERT INTO events(ts, kind, payload) VALUES (0, ?, ?)', (
                ('drain.build-started', json.dumps({'role': role, 'revision': revision}))
                for role in ('drain', 'view')))
        return root, live, git

    def test_roll_of_a_commit_installs_that_commit_while_the_shared_checkout_advances(self):
        with tempfile.TemporaryDirectory() as work:
            root, live, git = self.fixture(work)
            (root / 'tools/tillr-drain/main.go').write_text('package main\nfunc main() { _ = 1 }\n')
            git('commit', '-qam', 'A')
            commit_a = git('rev-parse', 'HEAD')
            real_run = roll.run
            advanced = []

            def run(*args, **kwargs):
                result = real_run(*args, **kwargs)
                if args[:2] == ('go', 'build') and not advanced:
                    # A landing arrives, then a dirty edit: neither may reach this roll.
                    (root / 'tools/tillr-drain/main.go').write_text('package main\nfunc main() { _ = 2 }\n')
                    git('commit', '-qam', 'B')
                    (root / 'tools/tillr-cli/main.go').write_text('package main\nfunc main() { _ = 3 }\n')
                    advanced.append(git('rev-parse', 'HEAD'))
                return result

            sharded = []

            def shards(*args, **kwargs):
                if args[:1] == ('python3',):
                    sharded.append(args)
                    return
                return run(*args, **kwargs)

            with patch.object(roll, 'run', side_effect=shards), \
                    redirect_stdout(io.StringIO()):
                self.assertEqual(roll.roll(root, live, True, commit=commit_a), 0)
            self.assertNotEqual(advanced[0], commit_a)
            self.assertEqual(git('rev-parse', 'HEAD'), advanced[0])
            for name in ('tillr-drain', 'tillr', 'tillr-view'):
                stamp = subprocess.check_output(['go', 'version', '-m', live / name], text=True)
                self.assertIn(f'vcs.revision={commit_a}', stamp)
                self.assertIn('vcs.modified=false', stamp)
            self.assertEqual((live.parent / roll.ROLLED).read_text().strip(), commit_a)
            # tillr-drain changed since the running build, and the gate shards it: the runner,
            # without Go's own deadline, and not a bare `go test`.
            self.assertEqual(len(sharded), 1)
            self.assertTrue(sharded[0][1].endswith('tools/go-test-shards.py'))
            self.assertEqual(sharded[0][2], 'tools/tillr-drain')
            self.assertEqual(sharded[0][3:7], ('-k', '6', '--timeout', '0'))

    def test_sharded_modules_match_the_gate(self):
        # The gate's declared Go suites say which modules run through the shard runner.
        sharded = set()
        for path in (ROOT / 'tools/gate/checks').glob('go-test-*.json'):
            command = ' '.join(json.loads(path.read_text())['command'])
            module = re.search(r'go-test-shards\.py (tools/[\w-]+) -k', command)
            if module:
                sharded.add(module.group(1))
        self.assertEqual(sharded, set(roll.SHARDED))

    def test_requests_during_a_roll_yield_one_further_roll_of_the_newest_tip(self):
        with tempfile.TemporaryDirectory() as work:
            root, live, git = self.fixture(work)
            first = git('rev-parse', 'HEAD')
            rolled = []
            started = threading.Event()
            release = threading.Event()
            waiting = threading.Semaphore(0)

            def roll_commit(root, live, tip, no_request, store):
                rolled.append(tip)
                (live.parent / roll.ROLLED).write_text(tip + '\n')
                if len(rolled) == 1:
                    started.set()
                    self.assertTrue(release.wait(60))
                return 0

            def request():
                return roll.roll(root, live, True, on_wait=waiting.release)

            with patch.object(roll, 'roll_commit', side_effect=roll_commit), redirect_stdout(io.StringIO()):
                with ThreadPoolExecutor(max_workers=3) as pool:
                    one = pool.submit(request)
                    self.assertTrue(started.wait(60))
                    (root / 'second').write_text('b')
                    git('add', '.')
                    git('commit', '-qm', 'B')
                    two = pool.submit(request)
                    self.assertTrue(waiting.acquire(timeout=60))
                    (root / 'third').write_text('c')
                    git('add', '.')
                    git('commit', '-qm', 'C')
                    newest = git('rev-parse', 'HEAD')
                    three = pool.submit(request)
                    self.assertTrue(waiting.acquire(timeout=60))
                    release.set()
                    self.assertEqual([one.result(60), two.result(60), three.result(60)], [0, 0, 0])
            self.assertEqual(rolled, [first, newest])

    def test_failed_roll_records_the_commit_and_failing_step(self):
        with tempfile.TemporaryDirectory() as work:
            root, live, git = self.fixture(work, view_source='broken view source\n')
            commit = git('rev-parse', 'HEAD')
            with redirect_stdout(io.StringIO()), self.assertRaises(roll.RollFailed):
                roll.roll(root, live, True)
            with closing(sqlite3.connect(root / '.tillr/state.db')) as db:
                kind, object_id, payload = db.execute(
                    "SELECT kind, object_id, payload FROM events WHERE kind = 'drain.roll-failed'").fetchone()
            self.assertEqual((kind, object_id), ('drain.roll-failed', commit))
            self.assertEqual(json.loads(payload)['step'], 'build tillr-view')
            self.assertFalse((live.parent / roll.ROLLED).exists())

    def test_green_pointer_proves_every_suite_and_a_gated_landing_its_own(self):
        with tempfile.TemporaryDirectory() as work:
            root, live, git = self.fixture(work)
            first = git('rev-parse', 'HEAD')
            self.assertEqual(roll.evidence(root, first), set())
            git('update-ref', 'refs/tillr/green', first)
            self.assertIsNone(roll.evidence(root, first))
            (root / 'tools/tillr-drain/main.go').write_text('package main\nfunc main() { _ = 1 }\n')
            git('commit', '-qam', 'next')
            tip = git('rev-parse', 'HEAD')
            self.assertEqual(roll.evidence(root, tip), set())
            store = root / '.tillr/state.db'
            self.assertEqual(roll.gated_modules(root, tip, store), set())
            tree = git('rev-parse', tip + '^{tree}')
            with closing(sqlite3.connect(store)) as db, db:
                db.execute('CREATE TABLE landings(trunk_after TEXT, result TEXT, gated_tree TEXT, landed_tree TEXT)')
                db.execute("INSERT INTO landings VALUES (?, 'landed', ?, ?)", (tip, tree, tree))
            self.assertEqual(roll.gated_modules(root, tip, store), {'tillr-drain'})

    def test_report_refuses_mismatched_digest_and_refused_answer(self):
        with tempfile.TemporaryDirectory() as work:
            live = Path(work)
            path = live / 'tillr-view'
            path.write_text('live')
            old = {'view': {'revision': 'first'}}
            new = {'view': {'revision': 'second', 'digest': 'incorrect'}}
            with patch('builtins.print') as output:
                self.assertEqual(roll.report(old, new, {'view': 'already-current'}, live), 1)
                output.assert_called_once_with('view: old=first new=second answer=already-current; part does not run from the live directory')
            new['view']['digest'] = roll.digest(path)
            self.assertEqual(roll.report(old, new, {'view': 'restarted'}, live), 0)
            self.assertEqual(roll.report(old, new, {'view': 'refused'}, live), 1)


    def test_restart_from_running_loop_reads_policy_and_lanes_and_restarts(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work) / 'repo'
            root.mkdir()
            live = Path(work) / 'bin'
            live.mkdir()

            def git(*args):
                return subprocess.check_output(
                    ['git', '-C', root, '-c', 'user.name=Fixture',
                     '-c', 'user.email=fixture@example.invalid', *args], text=True).strip()

            git('init', '-q')
            (root / '.gitignore').write_text('/.tillr/\n')
            (root / '.justfile').write_bytes((ROOT / '.justfile').read_bytes())
            if (ROOT / 'mise.toml').is_file():
                (root / 'mise.toml').write_bytes((ROOT / 'mise.toml').read_bytes())
            for name in ('tillr-keep', 'tillr-roll.py'):
                dst = root / 'tools/tillr-roll' / name
                dst.parent.mkdir(parents=True, exist_ok=True)
                dst.write_bytes((ROOT / 'tools/tillr-roll' / name).read_bytes())
            adapter = root / 'tools/tillr-loop/tillr-loop'
            adapter.parent.mkdir(parents=True, exist_ok=True)
            adapter.write_text('#!/usr/bin/env python3\n')

            # Stub tillr-drain records every invocation, acts as loop when given `loop`,
            # and stops the running loop when given `stop`.
            calls_log = Path(work) / 'calls.log'
            drain_stub = live / 'tillr-drain'
            drain_stub.write_text(
                '#!/usr/bin/env python3\n'
                'import ctypes, json, os, signal, sys, time\n'
                'from pathlib import Path\n'
                f'log = Path({str(calls_log)!r})\n'
                'with log.open("a") as f:\n'
                '    f.write(json.dumps({"pid": os.getpid(), "argv": sys.argv[1:]}) + "\\n")\n'
                'if sys.argv[1:2] == ["loop"]:\n'
                '    ctypes.CDLL(None).prctl(15, b"tillr-drain", 0, 0, 0)\n'
                '    pid_file = Path(sys.argv[sys.argv.index("--root") + 1]) / ".tillr/stub.pid"\n'
                '    pid_file.parent.mkdir(parents=True, exist_ok=True)\n'
                '    pid_file.write_text(str(os.getpid()))\n'
                '    stop = [False]\n'
                '    signal.signal(signal.SIGTERM, lambda *_: stop.__setitem__(0, True))\n'
                '    deadline = time.monotonic() + 30\n'
                '    while not stop[0] and time.monotonic() < deadline:\n'
                '        time.sleep(0.02)\n'
                'elif sys.argv[1:2] == ["stop"]:\n'
                '    pid_file = Path(sys.argv[sys.argv.index("--root") + 1]) / ".tillr/stub.pid"\n'
                '    if pid_file.exists():\n'
                '        os.kill(int(pid_file.read_text()), signal.SIGTERM)\n'
                '        sys.exit(0)\n'
                '    sys.exit(2)\n'
            )
            drain_stub.chmod(0o755)

            # Minimal Go modules so `just tillr-build` builds from HEAD.
            for module in roll.MODULES:
                source = root / 'tools' / f'tillr-{module}'
                source.mkdir(parents=True, exist_ok=True)
                (source / 'go.mod').write_text(f'module example.invalid/{module}\n\ngo 1.23\n')
                (source / 'main.go').write_text('package main\nfunc main() {}\n')

            git('add', '.')
            git('commit', '-qm', 'fixture')
            head = git('rev-parse', 'HEAD')

            env = {**os.environ, 'YOLO_DURABLE_DIR': work,
                   'PATH': f'{live}:{os.environ["PATH"]}'}
            keeper = subprocess.Popen(
                [sys.executable, str(root / 'tools/tillr-roll/tillr-keep'),
                 '--root', str(root), '--', '--policy', 'config/custom-policy.json', '--lanes', '4'],
                cwd=root, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
            )
            try:
                deadline = time.monotonic() + 10
                while not (root / '.tillr/stub.pid').exists():
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(0.02)
                old_loop_pid = int((root / '.tillr/stub.pid').read_text())

                # Replace tillr-drain with a stub that won't overwrite the built binary,
                # wait — tillr-build overwrites live/tillr-drain with the Go no-op binary!
                # That's fine: after tillr-build replaces live/tillr-drain, the new keeper
                # starts the new binary (which exits immediately, so we just verify the
                # keeper was started with the exact --policy and --lanes args).
                result = subprocess.run(
                    ['just', '--justfile', str(root / '.justfile'), 'tillr-restart', '--root', str(root)],
                    cwd=root, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                )
                self.assertEqual(result.returncode, 0, result.stdout)
                self.assertIn('keeper: stop requested', result.stdout)
                self.assertIn(f'restart: stopping loop {old_loop_pid}', result.stdout)
                self.assertIn(f'restart: loop {old_loop_pid} exited', result.stdout)
                self.assertIn(f'restart: building from HEAD {head[:12]}', result.stdout)
                self.assertIn('policy=config/custom-policy.json', result.stdout)
                self.assertIn('lanes=4', result.stdout)
                self.assertEqual(keeper.wait(timeout=5), 0)

                # New keeper lock should be held and recorded launch saved.
                recorded = json.loads((root / '.tillr/keeper.launch.json').read_text())
                self.assertEqual(recorded, {'lanes': '4', 'policy': 'config/custom-policy.json'})
                deadline = time.monotonic() + 5
                while not (Path(work) / 'keeper.log').exists():
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(0.02)
            finally:
                if keeper.poll() is None:
                    keeper.terminate()
                    keeper.wait(timeout=5)
                keeper.stdout.close()
                subprocess.run(
                    [sys.executable, str(root / 'tools/tillr-roll/tillr-keep'),
                     '--root', str(root), '--stop'],
                    cwd=root, env=env, capture_output=True, text=True,
                )

    def test_restart_when_no_loop_is_running_uses_recorded_launch_and_succeeds(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work) / 'repo'
            root.mkdir()

            def git(*args):
                return subprocess.check_output(
                    ['git', '-C', root, '-c', 'user.name=Fixture',
                     '-c', 'user.email=fixture@example.invalid', *args], text=True).strip()

            git('init', '-q')
            (root / '.gitignore').write_text('/.tillr/\n')
            (root / '.justfile').write_bytes((ROOT / '.justfile').read_bytes())
            if (ROOT / 'mise.toml').is_file():
                (root / 'mise.toml').write_bytes((ROOT / 'mise.toml').read_bytes())
            for name in ('tillr-keep', 'tillr-roll.py'):
                dst = root / 'tools/tillr-roll' / name
                dst.parent.mkdir(parents=True, exist_ok=True)
                dst.write_bytes((ROOT / 'tools/tillr-roll' / name).read_bytes())
            for module in roll.MODULES:
                source = root / 'tools' / f'tillr-{module}'
                source.mkdir(parents=True, exist_ok=True)
                (source / 'go.mod').write_text(f'module example.invalid/{module}\n\ngo 1.23\n')
                (source / 'main.go').write_text('package main\nfunc main() {}\n')
            git('add', '.')
            git('commit', '-qm', 'fixture')
            head = git('rev-parse', 'HEAD')

            (root / '.tillr').mkdir()
            (root / '.tillr/keeper.launch.json').write_text(
                json.dumps({'policy': 'config/recorded.json', 'lanes': '6'}) + '\n'
            )

            env = {**os.environ, 'YOLO_DURABLE_DIR': work,
                   'PATH': f'{Path(work) / "bin"}:{os.environ["PATH"]}'}
            result = subprocess.run(
                ['just', '--justfile', str(root / '.justfile'), 'tillr-restart', '--root', str(root)],
                cwd=root, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            )
            try:
                self.assertEqual(result.returncode, 0, result.stdout)
                self.assertIn('keeper: not running', result.stdout)
                self.assertIn('restart: no loop running', result.stdout)
                self.assertIn(f'restart: building from HEAD {head[:12]}', result.stdout)
                self.assertIn('policy=config/recorded.json', result.stdout)
                self.assertIn('lanes=6', result.stdout)
                self.assertTrue((Path(work) / 'bin/tillr-drain').is_file())
            finally:
                subprocess.run(
                    [sys.executable, str(root / 'tools/tillr-roll/tillr-keep'),
                     '--root', str(root), '--stop'],
                    cwd=root, env=env, capture_output=True, text=True,
                )

    def test_pi_extensions_invokes_build_agent_dir_without_restarts(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work) / 'repo'
            root.mkdir()
            builder = root / 'tools/tillr-run/backends/pi/build-agent-dir'
            builder.parent.mkdir(parents=True)
            marker = Path(work) / 'built.txt'
            builder.write_text(
                '#!/usr/bin/env python3\n'
                f'from pathlib import Path\nPath({str(marker)!r}).write_text("built\\n")\n'
            )
            builder.chmod(0o755)
            with patch.object(roll, 'git', return_value=str(root)):
                rc = roll.roll_pi_extensions(root)
            self.assertEqual(rc, 0)
            self.assertEqual(marker.read_text(), 'built\n')

            with patch.object(roll, 'git', return_value=str(root)), \
                    patch.object(roll, 'roll_pi_extensions', return_value=0) as mock_ext, \
                    patch.object(roll, 'roll') as mock_roll, \
                    patch.object(sys, 'argv', ['tillr-roll.py', '--pi-extensions']):
                self.assertEqual(roll.main(), 0)
                mock_ext.assert_called_once_with(root)
                mock_roll.assert_not_called()


    def test_roll_pi_flag_invokes_pi_forks_build_pi(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            live = root / 'bin'
            live.mkdir()
            calls = []

            def command(*args, **kwargs):
                calls.append((args, kwargs.get('cwd')))

            with patch.object(roll, 'git', return_value=''), \
                    patch.object(roll, 'run', side_effect=command):
                self.assertEqual(roll.roll(root, live, True, build_pi=True), 0)
            self.assertEqual(calls, [((str(root / 'bin/pi-forks'), 'build-pi'), root)])

    def test_wait_for_exit_waits_past_60s_and_prints_what_it_waits_on(self):
        states = [
            ('tillr-drain', '12345', 'S'),
            ('tillr-drain', '12345', 'S'),
            ('tillr-drain', '12345', 'S'),
            ('tillr-drain', '12345', 'S'),
            ('tillr-drain', '12345', 'S'),
            None,
        ]
        now = [0.0]

        def fake_sleep(seconds):
            now[0] += 35.0

        output = io.StringIO()
        with patch.object(keep, 'process', side_effect=states), redirect_stdout(output):
            keep.wait_for_exit(4242, report_interval=30, poll_interval=0.05,
                               clock=lambda: now[0], sleep=fake_sleep)

        lines = output.getvalue().splitlines()
        self.assertGreater(now[0], 60.0)
        self.assertEqual(lines[0], 'restart: waiting for loop 4242 to exit')
        self.assertIn('restart: still waiting for loop 4242 to exit', lines[1:])
        self.assertEqual(lines.count('restart: still waiting for loop 4242 to exit'), 3)

    def test_wait_for_exit_returns_immediately_when_already_exited(self):
        sleeps = []
        output = io.StringIO()
        with patch.object(keep, 'process', return_value=None), redirect_stdout(output):
            keep.wait_for_exit(4242, sleep=sleeps.append)
        self.assertEqual(sleeps, [])
        self.assertEqual(output.getvalue(), '')

    def test_restart_drain_waits_for_slow_loop_exit_then_builds_and_relaunches(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            lock_path = root / '.tillr/keeper.lock'
            states = [
                ('tillr-drain', '999', 'S'),
                ('tillr-drain', '999', 'S'),
                ('tillr-drain', '999', 'S'),
                ('tillr-drain', '999', 'S'),
                None,
            ]
            now = [0.0]
            events = []

            def fake_run(cmd, **kwargs):
                if cmd[:2] == ['tillr-drain', 'stop']:
                    events.append('stop')
                    return subprocess.CompletedProcess(cmd, 0, '', '')
                events.append('build')
                return subprocess.CompletedProcess(cmd, 0, '', '')

            def fake_popen(cmd, **kwargs):
                events.append(('relaunch', cmd))
                return type('Proc', (), {'pid': 8888})()

            output = io.StringIO()
            with patch.object(keep, 'find_loop', return_value=4242), \
                    patch.object(keep, 'loop_argv', return_value=['loop', '--policy', 'p.json', '--lanes', '2']), \
                    patch.object(keep, 'stop_keeper'), \
                    patch.object(keep, 'wait_for_keeper_exit'), \
                    patch.object(keep, 'process', side_effect=states), \
                    patch.object(keep.time, 'monotonic', side_effect=lambda: now[0]), \
                    patch.object(keep.time, 'sleep', side_effect=lambda _: now.__setitem__(0, now[0] + 35.0)), \
                    patch.object(keep.subprocess, 'run', side_effect=fake_run), \
                    patch.object(keep.subprocess, 'check_output', return_value='deadbeef12345678\n'), \
                    patch.object(keep.subprocess, 'Popen', side_effect=fake_popen), \
                    patch.dict(os.environ, {'YOLO_DURABLE_DIR': work}), \
                    redirect_stdout(output):
                keep.restart_drain(root, [], lock_path)

        text = output.getvalue()
        self.assertGreater(now[0], 60.0)
        self.assertEqual(events[0], 'stop')
        self.assertEqual(events[1], 'build')
        self.assertEqual(events[2][0], 'relaunch')
        self.assertIn('--policy', events[2][1])
        self.assertIn('--lanes', events[2][1])
        self.assertIn('restart: stopping loop 4242\n', text)
        self.assertIn('restart: waiting for loop 4242 to exit\n', text)
        self.assertIn('restart: still waiting for loop 4242 to exit\n', text)
        self.assertIn('restart: loop 4242 exited\n', text)
        self.assertIn('restart: building from HEAD deadbeef1234\n', text)
        self.assertIn('restart: started keeper 8888 (policy=p.json, lanes=2)', text)


if __name__ == '__main__':
    unittest.main()
