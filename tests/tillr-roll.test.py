from contextlib import closing, redirect_stdout
import hashlib
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


if __name__ == '__main__':
    unittest.main()
