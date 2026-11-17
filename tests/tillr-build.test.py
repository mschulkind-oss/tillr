#!/usr/bin/env python3
"""Build all eight programs from a frozen green tree, never the newer checkout."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
MODULES = ('tillr-cli', 'tillr-drain', 'tillr-gate', 'tillr-item',
           'tillr-land', 'tillr-run', 'tillr-store', 'tillr-view')


class GreenBuild(unittest.TestCase):
    def setUp(self):
        parent = Path(os.environ['YOLO_DURABLE_DIR'])
        self.scratch = tempfile.TemporaryDirectory(prefix='green-build-', dir=parent)
        self.addCleanup(self.scratch.cleanup)
        self.root = Path(self.scratch.name) / 'repo'
        self.root.mkdir()
        self.out = Path(self.scratch.name) / 'output'
        self.env = dict(os.environ, YOLO_DURABLE_DIR=str(self.out))
        (self.root / '.justfile').write_text((ROOT / '.justfile').read_text())
        self.git('init', '-q', '-b', 'main')
        for module in MODULES:
            path = self.root / 'tools' / module
            path.mkdir(parents=True)
            (path / 'go.mod').write_text(f'module github.com/mschulkind-oss/{module}\n\ngo 1.23\n')
            extra = ''
            stamp = '""'
            if module == 'tillr-drain':
                source = path / 'drain'
                source.mkdir()
                (source / 'build.go').write_text((ROOT / 'tools/tillr-drain/drain/build.go').read_text())
                extra = '; "github.com/mschulkind-oss/tillr-drain/drain"'
                stamp = 'drain.ReadBuildStamp().String()'
            (path / 'main.go').write_text(
                f'package main\nimport ("fmt"; "os"{extra})\n'
                f'func main() {{ if len(os.Args)>1 && os.Args[1]=="loop" {{ '
                f'fmt.Println("loop: build", {stamp}); return }}; fmt.Println("older") }}\n')
        self.git('add', '.')
        self.git('commit', '-qm', 'older programs')
        self.green = self.git('rev-parse', 'HEAD')

    def git(self, *args):
        return subprocess.check_output(
            ['git', '-C', str(self.root), '-c', 'user.name=Fixture',
             '-c', 'user.email=fixture@example.invalid', *args], text=True).strip()

    def build(self):
        return subprocess.run(['just', 'tillr-build'], cwd=self.root, env=self.env,
                              text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)

    def test_build_uses_green_not_tip_or_dirty_sources(self):
        self.git('update-ref', 'refs/tillr/green', self.green)
        for module in MODULES:
            path = self.root / 'tools' / module / 'main.go'
            path.write_text(path.read_text().replace('older', 'newer'))
        self.git('add', '.')
        self.git('commit', '-qm', 'newer programs')
        for module in MODULES:
            path = self.root / 'tools' / module / 'main.go'
            path.write_text(path.read_text().replace('newer', 'dirty'))
        before = self.git('status', '--porcelain')
        tip = self.git('rev-parse', 'HEAD')
        result = self.build()
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertEqual(self.git('status', '--porcelain'), before)
        self.assertEqual(self.git('rev-parse', 'HEAD'), tip)
        self.assertEqual(self.git('rev-parse', 'refs/tillr/green'), self.green)
        for module in MODULES:
            name = 'tillr' if module == 'tillr-cli' else module
            got = subprocess.check_output([str(self.out / 'bin' / name)], text=True)
            self.assertEqual(got.strip(), 'older', name)
        first = subprocess.check_output([str(self.out / 'bin/tillr-drain'), 'loop'], text=True).splitlines()[0]
        self.assertTrue(first.startswith('loop: build '), first)
        self.assertIn('green ' + self.green, first)
        self.assertFalse(any(self.out.glob('tillr-build-*')), 'extracted tree was not cleaned up')

    def test_missing_pointer_builds_nothing_and_names_heavy_run(self):
        result = self.build()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('refs/tillr/green', result.stdout)
        self.assertIn('heavy run', result.stdout)
        self.assertIn('tillr drain loop', result.stdout)
        self.assertFalse(self.out.exists(), result.stdout)

    def test_failed_build_preserves_existing_programs(self):
        # Failure in the last module must not install the preceding seven builds.
        broken = self.root / 'tools/tillr-view/main.go'
        broken.write_text('not valid Go\n')
        self.git('add', '.')
        self.git('commit', '-qm', 'broken view')
        self.git('update-ref', 'refs/tillr/green', self.git('rev-parse', 'HEAD'))
        bin_dir = self.out / 'bin'
        bin_dir.mkdir(parents=True)
        for module in MODULES:
            name = 'tillr' if module == 'tillr-cli' else module
            (bin_dir / name).write_text('previous install')
        result = self.build()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("expected 'package'", result.stdout)
        for path in bin_dir.iterdir():
            self.assertEqual(path.read_text(), 'previous install', path.name)
        self.assertFalse(any(self.out.glob('tillr-build-*')), 'failed extraction was not cleaned up')

    def test_legacy_green_preserves_vcs_and_planner_guard(self):
        # Real pre-linker-stamp sources: the proof pointer may still precede this
        # recipe's landing. Go silently ignores -X targets missing from that tree.
        shutil.rmtree(self.root / 'tools')
        archive = subprocess.check_output(['git', '-C', str(ROOT), 'archive', 'HEAD', 'tools'])
        subprocess.run(['tar', '-x', '-C', str(self.root)], input=archive, check=True)
        legacy = self.root / 'tools/tillr-drain/drain/build.go'
        # Keep this regression permanent after the new stamp lands: emulate the
        # older stamp without requiring an ancestor object in a shallow clone.
        legacy.write_text(legacy.read_text().replace('var greenRevision string', '')
                          .replace('stamp := BuildStamp{Revision: greenRevision, GreenCommit: greenRevision}',
                                   'stamp := BuildStamp{}'))
        self.assertNotIn('var greenRevision', legacy.read_text())
        self.git('add', '.')
        self.git('commit', '-qm', 'legacy programs')
        green = self.git('rev-parse', 'HEAD')
        self.git('update-ref', 'refs/tillr/green', green)
        view_source = self.root / 'tools/tillr-view/main.go'
        view_source.write_text(view_source.read_text().replace('const version = "0.1.0"',
                                                              'const version = "999.0.0"'))
        self.git('add', '.')
        self.git('commit', '-qm', 'newer view version')
        result = self.build()
        self.assertEqual(result.returncode, 0, result.stdout)
        for module in MODULES:
            name = 'tillr' if module == 'tillr-cli' else module
            info = subprocess.check_output(['go', 'version', '-m', str(self.out / 'bin' / name)],
                                           text=True)
            self.assertIn('vcs.revision=' + green, info, name)
            self.assertIn('vcs.modified=false', info, name)
        store = Path(self.scratch.name) / 'legacy-store'
        subprocess.run([str(self.out / 'bin/tillr-store'), 'init', '--store', str(store)],
                       env=self.env, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        raw = subprocess.check_output([str(self.out / 'bin/tillr-drain'), 'why',
                                       '--store', str(store), '--root', str(self.root), '--json'],
                                      env=self.env)
        self.assertEqual(json.loads(raw)['build']['revision'], green)
        # The real green-built view must still reject another revision's planner.
        planner = Path(self.scratch.name) / 'wrong-planner'
        answer = json.dumps({'rows': [], 'summary': {},
                             'build': {'revision': self.git('rev-parse', 'HEAD'),
                                       'modified': False, 'digest': 'wrong'}})
        planner.write_text("#!/bin/sh\nprintf '%s\\n' '" + answer + "'\n")
        planner.chmod(0o755)
        refused = subprocess.run([str(self.out / 'bin/tillr-view'), 'waiting',
                                  '--store', str(store), '--root', str(self.root),
                                  '--tillr-drain', str(planner)], env=self.env,
                                 text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        self.assertNotEqual(refused.returncode, 0, refused.stdout)
        self.assertIn("another build's rules", refused.stdout)
        status = json.loads(subprocess.check_output([str(self.out / 'bin/tillr-view'),
                           'status', '--store', str(store), '--root', str(self.root)], env=self.env))
        self.assertEqual(status['producer']['version'], '0.1.0')
        self.assertFalse(status['payload']['heavy']['at_green'])

    def test_status_reports_whether_checkout_is_green(self):
        self.git('update-ref', 'refs/tillr/green', self.green)
        view = Path(self.scratch.name) / 'tillr-view'
        subprocess.run(['go', 'build', '-o', str(view), '.'],
                       cwd=ROOT / 'tools/tillr-view', env=self.env, check=True)
        # A store-backed whole-board status reads the shared heavy status contract.
        store = Path(self.scratch.name) / 'store'
        subprocess.run(['go', 'run', '.', 'init', '--store', str(store)],
                       cwd=ROOT / 'tools/tillr-store', env=self.env, check=True,
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        def status():
            raw = subprocess.check_output([str(view), 'status', '--store', str(store),
                                           '--root', str(self.root)], env=self.env)
            return json.loads(raw)['payload']['heavy']
        self.assertTrue(status()['at_green'])
        (self.root / 'new-tip').write_text('unproved')
        self.git('add', 'new-tip')
        self.git('commit', '-qm', 'unproved tip')
        proof = status()
        self.assertFalse(proof['at_green'])
        self.assertEqual(proof['green_commit'], self.green)


if __name__ == '__main__':
    unittest.main(verbosity=2)
