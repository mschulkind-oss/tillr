#!/usr/bin/env python3
"""Parser, declaration, and fork-quality gate contracts; invoked by the drain's Go suite."""
import base64
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('fork_gate', Path(__file__).with_name('fork-gate.py'))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)
FORK_GATE = Path(__file__).with_name('fork-gate.py').resolve()
TILLR_GATE = os.environ.get('TILLR_GATE_BIN') or shutil.which('tillr-gate') or 'tillr-gate'


def scratch_root():
    env = os.environ.get('FORK_GATE_TEST_SCRATCH')
    if env:
        path = Path(env)
    elif os.environ.get('YOLO_DURABLE_DIR'):
        path = Path(os.environ['YOLO_DURABLE_DIR']) / 'fork-gate-test'
    else:
        path = Path(__file__).resolve().parents[2] / '.yolo' / 'durable' / 'tmp'
    path.mkdir(parents=True, exist_ok=True)
    return path


SCRATCH_ROOT = scratch_root()


class ForkBuildContracts(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(dir=SCRATCH_ROOT)
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)

    def pack(self, name, contribution):
        path = self.root / 'yolo-packs' / name / 'pack.json'
        path.parent.mkdir(parents=True)
        path.write_text(json.dumps({'contributes': [contribution]}))
        return path

    def test_jsonc_preserves_quoted_comments_and_removes_trailing_commas(self):
        path = self.root / 'pack.json'
        path.write_text('''{
          // comment
          "url": "https://example.invalid/", /* another comment */
          "literal": "/*not a comment*/,}",
          "array": [1, 2,],
        }''')
        self.assertEqual(gate.jsonc(path), {
            'url': 'https://example.invalid/', 'literal': '/*not a comment*/,}', 'array': [1, 2]})

    def test_declared_command_is_not_replaced_by_an_npm_guess(self):
        path = self.pack('fork', {'patch_branch': 'git+https://example.invalid/repo?ref=patch%2Fpi',
                                 'build': 'node scripts/fork-build.mjs'})
        self.assertEqual(gate.build_command(self.root, 'pi'), (path, 'node scripts/fork-build.mjs'))

    def test_source_only_delivery_has_an_explicit_check(self):
        path = self.pack('source', {'patch_branch': 'git+https://example.invalid/repo?ref=patch/pi'})
        found, command = gate.build_command(self.root, 'pi')
        self.assertEqual(found, path)
        self.assertEqual(command, 'git diff --exit-code HEAD && git diff --cached --exit-code')

    def test_no_delivery_is_an_error(self):
        with self.assertRaisesRegex(ValueError, 'found 0'):
            gate.build_command(self.root, 'pi')

    def test_ambiguous_delivery_is_an_error(self):
        for name in ('one', 'two'):
            self.pack(name, {'patch_branch': 'git+https://example.invalid/repo?ref=patch/pi'})
        with self.assertRaisesRegex(ValueError, 'found 2'):
            gate.build_command(self.root, 'pi')

    def test_existing_fork_quality_scripts_preserved(self):
        self.assertEqual(
            gate.quality_command('pi-subagents', self.root, 'HEAD', 'HEAD'),
            'npm ci --include=dev --legacy-peer-deps --ignore-scripts --no-audit --no-fund '
            '&& npm run typecheck && npm run test:unit',
        )
        self.assertEqual(
            gate.quality_command('pi-session-search', self.root, 'HEAD', 'HEAD'),
            'npm ci --include=dev --legacy-peer-deps --ignore-scripts --no-audit --no-fund '
            '&& npm run check && npm run test',
        )


class ForkQualityGateTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(dir=SCRATCH_ROOT)
        self.addCleanup(self.directory.cleanup)
        self.tmp = Path(self.directory.name)
        self.root = self.tmp / 'repo'
        self.root.mkdir()
        self.git(self.root, 'init', '-b', 'main')
        self.git(self.root, 'config', 'user.name', 'Fixture User')
        self.git(self.root, 'config', 'user.email', 'fixture@example.invalid')
        (self.root / '.gitignore').write_text('.tillr/\n')
        self.git(self.root, 'add', '.gitignore')
        self.git(self.root, 'commit', '-m', 'init')

        self.fork = self.tmp / 'fork'
        self.fork.mkdir()
        self.git(self.fork, 'init', '-b', 'main')
        self.git(self.fork, 'config', 'user.name', 'Fixture User')
        self.git(self.fork, 'config', 'user.email', 'fixture@example.invalid')
        (self.fork / 'README.md').write_text('base\n')
        (self.fork / 'packages' / 'pkg-a').mkdir(parents=True)
        (self.fork / 'packages' / 'pkg-a' / 'index.ts').write_text('export const a = 1;\n')
        (self.fork / 'packages' / 'pkg-b').mkdir(parents=True)
        (self.fork / 'packages' / 'pkg-b' / 'index.ts').write_text('export const b = 1;\n')
        for name, script in (('pkg-a', 'npx vitest --run --reporter=dot'),
                             ('pkg-b', 'npx vitest --run --untouched')):
            (self.fork / 'packages' / name / 'package.json').write_text(json.dumps({
                'name': name, 'version': '1.0.0', 'scripts': {'test': script},
            }))
        self.git(self.fork, 'add', '.')
        self.git(self.fork, 'commit', '-m', 'fork base')
        self.base = self.git(self.fork, 'rev-parse', 'HEAD')

        self.bin_dir = self.tmp / 'bin'
        self.bin_dir.mkdir()
        self.npx_log = self.tmp / 'npx.log'

    def git(self, cwd, *args):
        res = subprocess.run(['git', *args], cwd=cwd, capture_output=True, text=True, check=True)
        return res.stdout.strip()

    def write_pack(self, fork_name, build_cmd='true'):
        pack = self.root / 'yolo-packs' / fork_name / 'pack.json'
        pack.parent.mkdir(parents=True, exist_ok=True)
        pack.write_text(json.dumps({
            'contributes': [{
                'patch_branch': f'git+https://example.invalid/repo?ref=patch/{fork_name}',
                'build': build_cmd,
            }],
        }))

    def write_npx_stub(self, script):
        npx = self.bin_dir / 'npx'
        npx.write_text('#!/bin/sh\nset -eu\n' + script)
        npx.chmod(0o755)

    def run_fork_gate(self, fork_name, run_id, head):
        env = os.environ.copy()
        env['PATH'] = f'{self.bin_dir}:{env.get("PATH", "")}'
        env['NPX_LOG'] = str(self.npx_log)
        return subprocess.run(
            [
                'python3', str(FORK_GATE),
                '--item', 'item-1', '--lane', 'lane-1', '--run', run_id,
                '--worktree', str(self.fork), '--base', self.base, '--head', head,
                '--root', str(self.root), '--fork', fork_name,
                '--tillr-gate', TILLR_GATE,
            ],
            cwd=self.root, env=env, capture_output=True, text=True,
        )

    def commit_offline_devcheck_package(self, scripts):
        vendor = self.fork / 'vendor'
        vendor.mkdir(exist_ok=True)
        tgz_path = vendor / 'devcheck-1.0.0.tgz'
        buf = io.BytesIO()
        with tarfile.open(fileobj=buf, mode='w:gz') as tf:
            pkg_json = json.dumps({
                'name': 'devcheck',
                'version': '1.0.0',
                'bin': {'devcheck': 'bin.js'},
            }).encode()
            ti = tarfile.TarInfo('package/package.json')
            ti.size = len(pkg_json)
            ti.mode = 0o644
            tf.addfile(ti, io.BytesIO(pkg_json))

            bin_js = (
                b'#!/usr/bin/env node\n'
                b'console.log("devcheck:" + process.argv.slice(2).join(" "));\n'
                b'process.exit(process.argv.includes("--fail") ? 1 : 0);\n'
            )
            ti2 = tarfile.TarInfo('package/bin.js')
            ti2.size = len(bin_js)
            ti2.mode = 0o755
            tf.addfile(ti2, io.BytesIO(bin_js))

        tgz_bytes = buf.getvalue()
        tgz_path.write_bytes(tgz_bytes)
        integrity = 'sha512-' + base64.b64encode(hashlib.sha512(tgz_bytes).digest()).decode()
        (self.fork / 'package.json').write_text(json.dumps({
            'name': 'fixture-fork',
            'version': '1.0.0',
            'scripts': scripts,
            'devDependencies': {'devcheck': 'file:vendor/devcheck-1.0.0.tgz'},
        }, indent=2))
        (self.fork / 'package-lock.json').write_text(json.dumps({
            'name': 'fixture-fork',
            'version': '1.0.0',
            'lockfileVersion': 3,
            'requires': True,
            'packages': {
                '': {
                    'name': 'fixture-fork',
                    'version': '1.0.0',
                    'devDependencies': {'devcheck': 'file:vendor/devcheck-1.0.0.tgz'},
                },
                'node_modules/devcheck': {
                    'version': '1.0.0',
                    'resolved': 'vendor/devcheck-1.0.0.tgz',
                    'integrity': integrity,
                    'dev': True,
                    'bin': {'devcheck': 'bin.js'},
                },
            },
        }, indent=2))
        self.git(self.fork, 'add', 'vendor/devcheck-1.0.0.tgz', 'package.json', 'package-lock.json')
        self.git(self.fork, 'commit', '-m', 'add offline devcheck package')
        return self.git(self.fork, 'rev-parse', 'HEAD')

    def add_tui_test(self, assertion='assert.equal(process.cwd().endsWith("/packages/tui"), true);', *,
                     script='node --test test/*.test.ts'):
        tui = self.fork / 'packages' / 'tui'
        (tui / 'test').mkdir(parents=True, exist_ok=True)
        (tui / 'package.json').write_text(json.dumps({
            'name': 'tui', 'version': '1.0.0',
            'scripts': {} if script is None else {'test': script},
        }))
        (tui / 'test' / 'fixture.test.ts').write_text(
            'import { test } from "node:test";\n'
            'import assert from "node:assert/strict";\n'
            f'test("tui fixture ran in package", () => {{ {assertion} }});\n'
        )

    def quality_log(self, run_dir):
        logs = list((run_dir / 'gate.logs').glob('*-fork-quality.log'))
        self.assertEqual(len(logs), 1, str(logs))
        return logs[0].read_text()

    def test_passing_build_and_failing_quality_fails_without_cache_source(self):
        self.write_pack('pi', build_cmd='touch build.marker')
        self.write_npx_stub('echo "$PWD:$*" >> "$NPX_LOG"\nexit 1\n')
        (self.fork / 'packages' / 'pkg-a' / 'index.ts').write_text('export const a = 2;\n')
        self.git(self.fork, 'commit', '-qam', 'touch pkg-a')
        head = self.git(self.fork, 'rev-parse', 'HEAD')

        res = self.run_fork_gate('pi', 'run-quality-fail', head)
        self.assertEqual(res.returncode, 1, res.stdout + res.stderr)
        run_dir = self.root / '.tillr' / 'loop' / 'run-quality-fail'
        verdict = json.loads((run_dir / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(verdict['result'], 'fail')
        checks = {c['check_id']: c['result'] for c in verdict['checks']}
        self.assertEqual(checks, {'fork-build': 'pass', 'fork-quality': 'fail'})
        self.assertFalse((run_dir / 'fork-cache-source').exists())

    def test_passing_build_and_passing_quality_writes_cache_source(self):
        self.write_pack('pi', build_cmd='touch build.marker')
        self.write_npx_stub(
            'test -f "$(git rev-parse --show-toplevel)/build.marker"\n'
            'echo "$(pwd):$*" >> "$NPX_LOG"\n'
        )
        (self.fork / 'packages' / 'pkg-a' / 'index.ts').write_text('export const a = 3;\n')
        self.git(self.fork, 'commit', '-qam', 'touch pkg-a')
        head = self.git(self.fork, 'rev-parse', 'HEAD')

        res = self.run_fork_gate('pi', 'run-quality-pass', head)
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
        run_dir = self.root / '.tillr' / 'loop' / 'run-quality-pass'
        verdict = json.loads((run_dir / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(verdict['result'], 'pass')
        checks = {c['check_id']: c['result'] for c in verdict['checks']}
        self.assertEqual(checks, {'fork-build': 'pass', 'fork-quality': 'pass'})
        self.assertTrue((run_dir / 'fork-cache-source').exists())
        log_lines = self.npx_log.read_text().splitlines()
        self.assertEqual(len(log_lines), 2)
        self.assertTrue(log_lines[0].endswith(':tsc --noEmit'))
        self.assertTrue(log_lines[1].endswith('/packages/pkg-a:vitest --run --reporter=dot'))

    def test_failing_build_skips_quality_execution(self):
        self.write_pack('pi', build_cmd='exit 1')
        self.write_npx_stub('echo "$(pwd):$*" >> "$NPX_LOG"\n')
        (self.fork / 'packages' / 'pkg-a' / 'index.ts').write_text('export const a = 4;\n')
        self.git(self.fork, 'commit', '-qam', 'touch pkg-a')
        head = self.git(self.fork, 'rev-parse', 'HEAD')

        res = self.run_fork_gate('pi', 'run-build-fail', head)
        self.assertEqual(res.returncode, 1, res.stdout + res.stderr)
        run_dir = self.root / '.tillr' / 'loop' / 'run-build-fail'
        verdict = json.loads((run_dir / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(verdict['result'], 'fail')
        checks = {c['check_id']: c['result'] for c in verdict['checks']}
        self.assertEqual(checks, {'fork-build': 'fail', 'fork-quality': 'fail'})
        self.assertFalse((run_dir / 'fork-cache-source').exists())
        self.assertFalse(self.npx_log.exists(), 'fork-quality should not run npx when fork-build fails')

    def test_diff_touching_no_package_skips_package_tests(self):
        self.write_pack('pi', build_cmd='true')
        self.write_npx_stub('echo "$(pwd):$*" >> "$NPX_LOG"\n')
        (self.fork / 'README.md').write_text('updated\n')
        self.git(self.fork, 'commit', '-qam', 'touch readme only')
        head = self.git(self.fork, 'rev-parse', 'HEAD')

        res = self.run_fork_gate('pi', 'run-no-pkg', head)
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
        self.assertIn('skipping package tests', res.stdout)
        run_dir = self.root / '.tillr' / 'loop' / 'run-no-pkg'
        verdict = json.loads((run_dir / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(verdict['result'], 'pass')
        log_lines = self.npx_log.read_text().splitlines()
        self.assertEqual(len(log_lines), 1)
        self.assertTrue(log_lines[0].endswith(':tsc --noEmit'))

    def test_touched_packages_run_their_declared_tests_not_untouched_packages(self):
        self.write_pack('pi', build_cmd='touch build.marker')
        self.write_npx_stub(
            'test -f "$(git rev-parse --show-toplevel)/build.marker"\n'
            'echo "$PWD:$*" >> "$NPX_LOG"\n'
            'case "$*" in "tsc --noEmit"|"vitest --run --reporter=dot") ;; *) exit 1;; esac\n'
        )
        self.add_tui_test(script='node --test test/*.test.ts && echo tui-compound-step-ran')
        (self.fork / 'packages' / 'pkg-a' / 'index.ts').write_text('export const a = 5;\n')
        self.git(self.fork, 'add', '.')
        self.git(self.fork, 'commit', '-m', 'touch tui and pkg-a')
        head = self.git(self.fork, 'rev-parse', 'HEAD')

        res = self.run_fork_gate('pi', 'run-package-scripts', head)
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
        run_dir = self.root / '.tillr' / 'loop' / 'run-package-scripts'
        verdict = json.loads((run_dir / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(verdict['result'], 'pass')
        self.assertEqual({c['check_id']: c['result'] for c in verdict['checks']},
                         {'fork-build': 'pass', 'fork-quality': 'pass'})
        self.assertTrue((run_dir / 'fork-cache-source').exists())
        log = self.quality_log(run_dir)
        self.assertIn('tui fixture ran in package', log)
        self.assertIn('\ntui-compound-step-ran\n', log)
        self.assertIn('node --test test/*.test.ts', res.stdout + res.stderr + log)
        lines = self.npx_log.read_text().splitlines()
        self.assertEqual(len(lines), 2, lines)
        self.assertTrue(lines[0].endswith(':tsc --noEmit'))
        self.assertTrue(lines[1].endswith('/packages/pkg-a:vitest --run --reporter=dot'))
        self.assertNotIn('/packages/tui:vitest', '\n'.join(lines))
        self.assertNotIn('/packages/pkg-b:', '\n'.join(lines))

    def test_unavailable_package_runner_is_an_error_not_a_test_failure(self):
        self.write_pack('pi', build_cmd='touch build.marker')
        self.write_npx_stub('echo "$PWD:$*" >> "$NPX_LOG"\n')
        package = self.fork / 'packages' / 'pkg-a' / 'package.json'
        package.write_text(json.dumps({
            'name': 'pkg-a', 'version': '1.0.0',
            'scripts': {'test': 'tillr-nonexistent-test-runner --run'},
        }))
        self.git(self.fork, 'commit', '-qam', 'declare unavailable runner')
        head = self.git(self.fork, 'rev-parse', 'HEAD')

        res = self.run_fork_gate('pi', 'run-missing-runner', head)
        run_dir = self.root / '.tillr' / 'loop' / 'run-missing-runner'
        self.assertTrue((run_dir / 'gate.verdict.json').exists(), res.stdout + res.stderr)
        verdict = json.loads((run_dir / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(res.returncode, 4, res.stdout + res.stderr)
        self.assertEqual(verdict['result'], 'error')
        self.assertEqual({c['check_id']: c['result'] for c in verdict['checks']},
                         {'fork-build': 'pass', 'fork-quality': 'error'})
        diagnostic = res.stdout + res.stderr + self.quality_log(run_dir)
        self.assertIn('pkg-a', diagnostic)
        self.assertIn('tillr-nonexistent-test-runner', diagnostic)
        self.assertFalse((run_dir / 'fork-cache-source').exists())

    def test_offline_npm_exec_cannot_resolve_declared_runner(self):
        self.write_pack('pi', build_cmd='touch build.marker')
        self.write_npx_stub('echo "$PWD:$*" >> "$NPX_LOG"\n')
        runner = 'tillr-nonexistent-test-runner'
        script = f'npm exec --offline --yes --cache .empty-npm-cache -- {runner} --run'
        self.add_tui_test(script=script)
        self.git(self.fork, 'add', '.')
        self.git(self.fork, 'commit', '-m', 'declare unresolved npm exec runner')
        head = self.git(self.fork, 'rev-parse', 'HEAD')

        res = self.run_fork_gate('pi', 'run-unresolved-npm-exec', head)
        run_dir = self.root / '.tillr' / 'loop' / 'run-unresolved-npm-exec'
        log = self.quality_log(run_dir)
        self.assertIn('ENOTCACHED', log)
        self.assertEqual(res.returncode, 4, log)
        verdict = json.loads((run_dir / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(verdict['result'], 'error')
        self.assertEqual({c['check_id']: c['result'] for c in verdict['checks']},
                         {'fork-build': 'pass', 'fork-quality': 'error'})
        self.assertIn(f'packages/tui: unable to run scripts.test {script!r}', log)
        self.assertFalse((run_dir / 'fork-cache-source').exists())

    def test_missing_package_test_declaration_is_an_error(self):
        self.write_pack('pi', build_cmd='touch build.marker')
        self.write_npx_stub('echo "$PWD:$*" >> "$NPX_LOG"\n')
        self.add_tui_test(script=None)
        self.git(self.fork, 'add', '.')
        self.git(self.fork, 'commit', '-m', 'add tui without a test declaration')
        head = self.git(self.fork, 'rev-parse', 'HEAD')

        res = self.run_fork_gate('pi', 'run-missing-declaration', head)
        run_dir = self.root / '.tillr' / 'loop' / 'run-missing-declaration'
        self.assertEqual(res.returncode, 4, res.stdout + res.stderr)
        verdict = json.loads((run_dir / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(verdict['result'], 'error')
        self.assertEqual({c['check_id']: c['result'] for c in verdict['checks']},
                         {'fork-build': 'pass', 'fork-quality': 'error'})
        log = self.quality_log(run_dir)
        self.assertIn('packages/tui', log)
        self.assertIn('no scripts.test', log)
        self.assertFalse((run_dir / 'fork-cache-source').exists())

    def test_a_genuine_failing_declared_package_test_is_a_failure(self):
        self.write_pack('pi', build_cmd='touch build.marker')
        self.write_npx_stub('echo "$PWD:$*" >> "$NPX_LOG"\n')
        self.add_tui_test('assert.equal(1, 2);')
        self.git(self.fork, 'add', '.')
        self.git(self.fork, 'commit', '-m', 'add failing tui test')
        head = self.git(self.fork, 'rev-parse', 'HEAD')

        res = self.run_fork_gate('pi', 'run-failing-tui', head)
        run_dir = self.root / '.tillr' / 'loop' / 'run-failing-tui'
        self.assertEqual(res.returncode, 1, res.stdout + res.stderr)
        verdict = json.loads((run_dir / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(verdict['result'], 'fail')
        self.assertEqual({c['check_id']: c['result'] for c in verdict['checks']},
                         {'fork-build': 'pass', 'fork-quality': 'fail'})
        self.assertIn('tui fixture ran in package', self.quality_log(run_dir))
        self.assertFalse((run_dir / 'fork-cache-source').exists())

    def test_unlisted_fork_keeps_build_only_verdict_and_reports_it(self):
        self.write_pack('custom-fork', build_cmd='true')
        (self.fork / 'README.md').write_text('custom\n')
        self.git(self.fork, 'commit', '-qam', 'custom change')
        head = self.git(self.fork, 'rev-parse', 'HEAD')

        res = self.run_fork_gate('custom-fork', 'run-unlisted', head)
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
        self.assertIn('build-only verdict', res.stdout)
        run_dir = self.root / '.tillr' / 'loop' / 'run-unlisted'
        verdict = json.loads((run_dir / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(verdict['result'], 'pass')
        checks = [c['check_id'] for c in verdict['checks']]
        self.assertEqual(checks, ['fork-build'])

    def test_pi_subagents_offline_end_to_end(self):
        self.write_pack(
            'pi-subagents',
            build_cmd='npm ci --omit=dev --legacy-peer-deps --ignore-scripts --no-audit --no-fund',
        )
        head_pass = self.commit_offline_devcheck_package({
            'typecheck': 'devcheck typecheck',
            'test:unit': 'devcheck test:unit',
        })
        res = self.run_fork_gate('pi-subagents', 'run-subagents-pass', head_pass)
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
        run_dir = self.root / '.tillr' / 'loop' / 'run-subagents-pass'
        verdict = json.loads((run_dir / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(verdict['result'], 'pass')
        self.assertEqual(
            {c['check_id']: c['result'] for c in verdict['checks']},
            {'fork-build': 'pass', 'fork-quality': 'pass'},
        )
        self.assertTrue((run_dir / 'fork-cache-source').exists())

        head_fail = self.commit_offline_devcheck_package({
            'typecheck': 'devcheck typecheck',
            'test:unit': 'devcheck test:unit --fail',
        })
        res = self.run_fork_gate('pi-subagents', 'run-subagents-fail', head_fail)
        self.assertEqual(res.returncode, 1, res.stdout + res.stderr)
        run_dir_fail = self.root / '.tillr' / 'loop' / 'run-subagents-fail'
        verdict_fail = json.loads((run_dir_fail / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(verdict_fail['result'], 'fail')
        self.assertEqual(
            {c['check_id']: c['result'] for c in verdict_fail['checks']},
            {'fork-build': 'pass', 'fork-quality': 'fail'},
        )
        self.assertFalse((run_dir_fail / 'fork-cache-source').exists())

    def test_pi_session_search_offline_end_to_end(self):
        self.write_pack(
            'pi-session-search',
            build_cmd=(
                'npm ci --legacy-peer-deps --ignore-scripts --no-audit --no-fund '
                '&& npm run build '
                '&& npm prune --omit=dev --legacy-peer-deps --ignore-scripts --no-audit --no-fund'
            ),
        )
        head_pass = self.commit_offline_devcheck_package({
            'build': 'devcheck build',
            'check': 'devcheck check',
            'test': 'devcheck test',
        })
        res = self.run_fork_gate('pi-session-search', 'run-search-pass', head_pass)
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
        run_dir = self.root / '.tillr' / 'loop' / 'run-search-pass'
        verdict = json.loads((run_dir / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(verdict['result'], 'pass')
        self.assertEqual(
            {c['check_id']: c['result'] for c in verdict['checks']},
            {'fork-build': 'pass', 'fork-quality': 'pass'},
        )
        self.assertTrue((run_dir / 'fork-cache-source').exists())

        head_fail = self.commit_offline_devcheck_package({
            'build': 'devcheck build',
            'check': 'devcheck check',
            'test': 'devcheck test --fail',
        })
        res = self.run_fork_gate('pi-session-search', 'run-search-fail', head_fail)
        self.assertEqual(res.returncode, 1, res.stdout + res.stderr)
        run_dir_fail = self.root / '.tillr' / 'loop' / 'run-search-fail'
        verdict_fail = json.loads((run_dir_fail / 'gate.verdict.json').read_text())['payload']
        self.assertEqual(verdict_fail['result'], 'fail')
        self.assertEqual(
            {c['check_id']: c['result'] for c in verdict_fail['checks']},
            {'fork-build': 'pass', 'fork-quality': 'fail'},
        )
        self.assertFalse((run_dir_fail / 'fork-cache-source').exists())


if __name__ == '__main__':
    unittest.main()
