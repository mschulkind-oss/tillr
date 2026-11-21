#!/usr/bin/env python3
"""A recorded fork object is independent of cache contents and local refs."""
from concurrent.futures import ThreadPoolExecutor
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[3]
CHECK = ROOT / 'tools/gate/checks/patch-ref.py'


def git(path, *args):
    return subprocess.check_output(['git', '-C', str(path), *args], text=True).strip()


class PinnedForkTest(unittest.TestCase):
    def test_only_exact_local_delivery_can_seed_a_repo_only_rebase(self):
        with tempfile.TemporaryDirectory() as t:
            root = Path(t)
            delivery = root / 'delivery'
            subprocess.run(['git', 'init', '-q', str(delivery)], check=True)
            git(delivery, '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                'commit', '--allow-empty', '-qm', 'already delivered rebase')
            oid = git(delivery, 'rev-parse', 'HEAD')
            tree = root / 'candidate'
            pins = tree / 'yolo-packs/fork-pins'
            pins.mkdir(parents=True)
            (pins / 'example').write_text(f'{oid}\n{root / "unpublished"}\n{oid}\n')
            env = {**os.environ, 'FORK_INPUT_ROOT': str(tree), 'PI_FORKS_ROOT': str(delivery)}
            env.pop('FORK_INPUT_CACHE', None)
            argv = ['python3', str(CHECK), 'patch/example']
            result = subprocess.run(argv, env=env, text=True, capture_output=True)
            self.assertEqual(result.returncode, 4, result.stderr)
            git(delivery, 'branch', 'patch/example', oid)
            result = subprocess.run(argv, env=env, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            cache = delivery / '.forks/inputs/example'
            self.assertEqual(git(cache, 'rev-parse', f'refs/fork-inputs/{oid}'), oid)

    def test_cached_pin_survives_gc_and_offline_source(self):
        with tempfile.TemporaryDirectory() as t:
            root = Path(t)
            source = root / 'source'
            subprocess.run(['git', 'init', '-q', str(source)], check=True)
            git(source, 'config', 'user.name', 'Fixture')
            git(source, 'config', 'user.email', 'fixture@example.invalid')
            (source / 'file').write_text('pin\n')
            git(source, 'add', 'file')
            git(source, 'commit', '-qm', 'pin')
            pin = git(source, 'rev-parse', 'HEAD')
            pins = root / 'tree/yolo-packs/fork-pins'
            pins.mkdir(parents=True)
            (pins / 'example').write_text(pin + '\n' + str(source) + '\n' + pin + '\n')
            env = {**os.environ, 'FORK_INPUT_ROOT': str(pins.parent.parent),
                   'FORK_INPUT_CACHE': str(root / 'cache')}
            def run():
                return subprocess.run(['python3', str(CHECK), 'patch/example'], env=env,
                                      text=True, capture_output=True)
            # Independent checks can first encounter the same empty cache together.
            with ThreadPoolExecutor(max_workers=8) as pool:
                results = list(pool.map(lambda _: run(), range(8)))
            self.assertTrue(all(r.returncode == 0 for r in results),
                            [(r.returncode, r.stderr) for r in results])
            cache = root / 'cache/example'
            # FETCH_HEAD is transient: a later fetch or maintenance can discard it.
            (cache / '.git/FETCH_HEAD').unlink()
            git(cache, 'gc', '--prune=now')
            source.rename(root / 'offline')
            result = run()
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_empty_current_stale_and_unavailable(self):
        with tempfile.TemporaryDirectory() as t:
            root = Path(t)
            source = root / 'source'
            source.mkdir()
            subprocess.run(['git', 'init', '-q', str(source)], check=True)
            subprocess.run(['git', '-C', str(source), 'config', 'user.name', 'Fixture'], check=True)
            subprocess.run(['git', '-C', str(source), 'config', 'user.email', 'fixture@example.invalid'], check=True)
            (source / 'file').write_text('stale\n')
            subprocess.run(['git', '-C', str(source), 'add', 'file'], check=True)
            subprocess.run(['git', '-C', str(source), 'commit', '-qm', 'stale'], check=True)
            stale = git(source, 'rev-parse', 'HEAD')
            (source / 'file').write_text('current\n')
            subprocess.run(['git', '-C', str(source), 'commit', '-qam', 'current'], check=True)
            pin = git(source, 'rev-parse', 'HEAD')
            pins = root / 'tree/yolo-packs/fork-pins'
            pins.mkdir(parents=True)
            (pins / 'example').write_text(pin + '\n' + str(source) + '\n' + stale + '\n')
            tree = pins.parent.parent
            cache = root / 'cache'
            env = {**os.environ, 'FORK_INPUT_ROOT': str(tree), 'FORK_INPUT_CACHE': str(cache)}
            def run():
                return subprocess.run(['python3', str(CHECK), 'patch/example'], env=env,
                                      text=True, capture_output=True)
            for state in ('empty', 'current', 'stale'):
                if state == 'stale':
                    subprocess.run(['rm', '-rf', str(cache)], check=True)
                    subprocess.run(['git', 'init', '-q', '--bare', str(cache / 'example')], check=True)
                    subprocess.run(['git', '-C', str(cache / 'example'), 'fetch', '-q', str(source), stale], check=True)
                result = run()
                self.assertEqual(result.returncode, 0, (state, result.stdout, result.stderr))
            # The driver and structural check share the root's input cache.
            shared_root = root / 'shared'
            shared_cache = shared_root / '.forks/inputs/example'
            shared_cache.parent.mkdir(parents=True)
            subprocess.run(['git', 'init', '-q', str(shared_cache)], check=True)
            subprocess.run(['git', '-C', str(shared_cache), 'fetch', '-q', str(source), pin], check=True)
            # Once present, the source may disappear; no refresh is allowed.
            source.rename(root / 'offline')
            shared_env = {**os.environ, 'FORK_INPUT_ROOT': str(tree), 'PI_FORKS_ROOT': str(shared_root)}
            shared_env.pop('FORK_INPUT_CACHE', None)
            result = subprocess.run(['python3', str(CHECK), 'patch/example'], env=shared_env,
                                    text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            result = run()
            self.assertEqual(result.returncode, 0, result.stderr)
            subprocess.run(['rm', '-rf', str(cache)], check=True)
            result = run()
            self.assertEqual(result.returncode, 4, (result.stdout, result.stderr))
            self.assertIn('could not judge', result.stderr)
            self.assertIn(pin, result.stderr)
            self.assertIn(str(source), result.stderr)


if __name__ == '__main__':
    unittest.main()
