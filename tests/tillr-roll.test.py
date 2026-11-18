from contextlib import closing
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
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
        self.assertIs(settings['push']['after_landing'], True)
        sections = {
            'tools/tillr-drain/README.md': 'Moving a running loop onto new code',
            'yolo-packs/tillr/skills/tillr-operating/SKILL.md': 'Update a running drain',
        }
        for path, heading in sections.items():
            with self.subTest(path=path):
                body = (ROOT / path).read_text().split(f'## {heading}\n', 1)[1].split('\n## ', 1)[0].strip()
                self.assertLessEqual(len(body.splitlines()), 5)
                for command in ('just tillr-build', 'just tillr-roll', 'yolo host apply --assert'):
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
                db.execute('CREATE TABLE events(seq INTEGER PRIMARY KEY, kind TEXT, payload TEXT)')
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

    def test_smoke_finishes_before_rename(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            live = root / 'bin'
            live.mkdir()
            old = live / 'tillr'
            old.write_text('old installation')
            calls = []
            module = root / 'tools/tillr-drain'
            module.mkdir(parents=True)
            (module / 'go.mod').touch()

            def command(*args, **kwargs):
                self.assertEqual(old.read_text(), 'old installation')
                if args[:2] == ('go', 'test'):
                    calls.append('test')
                elif args[:2] == ('go', 'build'):
                    calls.append('build')
                    args[args.index('-o') + 1].write_text('new installation')
                else:
                    calls.append(args[1])
                    self.assertTrue(args[0].is_file())

            with old.open() as previous, patch.object(roll, 'git', return_value=''), \
                    patch.object(roll, 'builds', return_value={}), \
                    patch.object(roll, 'run', side_effect=command):
                self.assertEqual(roll.roll(root, live, True), 0)
                self.assertEqual(previous.read(), 'old installation')
            self.assertEqual(old.read_text(), 'new installation')
            self.assertEqual(calls, ['test'] + ['build'] * 8 + ['--help'] * 8 + ['loop'])

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


if __name__ == '__main__':
    unittest.main()
