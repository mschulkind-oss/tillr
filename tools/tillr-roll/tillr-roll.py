import argparse
from concurrent.futures import ThreadPoolExecutor
from contextlib import closing
import hashlib
import json
import os
from pathlib import Path
import shlex
import sqlite3
import subprocess
import tempfile
import time


MODULES = ('cli', 'drain', 'gate', 'item', 'land', 'run', 'store', 'view')


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def git(*args):
    return run('git', *args, capture_output=True).stdout.strip()


def builds(store):
    facts = {}
    with closing(sqlite3.connect(store.as_uri() + '?mode=ro', uri=True)) as db:
        rows = db.execute("SELECT seq, payload FROM events WHERE kind = 'drain.build-started' ORDER BY seq DESC")
        for seq, payload in rows:
            fact = json.loads(payload)
            facts.setdefault(fact['role'], {**fact, 'seq': seq})
    return facts


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def report(old, new, answers, live):
    failed = False
    for role, answer in answers.items():
        before = old.get(role, {}).get('revision') or 'unknown'
        after = new.get(role, {}).get('revision') or 'unknown'
        mismatch = (answer not in ('refused', 'not-requested', 'error') and
                    new.get(role, {}).get('digest') != digest(live / f'tillr-{role}'))
        detail = '; part does not run from the live directory' if mismatch else ''
        print(f'{role}: old={before} new={after} answer={answer}{detail}')
        failed |= mismatch or answer in ('refused', 'error')
    return int(failed)


def roll(root, live, no_request, build_pi=False):
    if git('status', '--porcelain'):
        raise RuntimeError('refusing a dirty checkout')
    if build_pi:
        run(str(root / 'bin/pi-forks'), 'build-pi', cwd=root)
        return 0
    store = root / '.tillr/state.db'
    old = builds(store)
    tip = git('rev-parse', 'HEAD')
    # Either running part may lag. Unidentified builds cannot justify skipping tests.
    changed = set()
    for role in ('drain', 'view'):
        fact = old.get(role, {})
        revision = fact.get('revision')
        if not revision or fact.get('modified'):
            changed = None
            break
        try:
            changed.update(git('diff', '--name-only', '-z', '--no-renames', revision, tip, '--', 'tools').split('\0'))
        except subprocess.CalledProcessError:
            changed = None
            break
    build_env = {**os.environ, 'CGO_ENABLED': '0'}
    modules = sorted(p for p in (root / 'tools').glob('tillr*') if (p / 'go.mod').is_file())
    for module in modules:
        if changed is None or any(p.startswith(f'tools/{module.name}/') for p in changed):
            run('go', 'test', './...', cwd=module, env=build_env)
    with tempfile.TemporaryDirectory(prefix='tillr-roll-', dir=live.parent) as work:
        stage = Path(work)
        programs = []
        for module in MODULES:
            name = 'tillr' if module == 'cli' else f'tillr-{module}'
            target = stage / name
            source = root / 'tools' / f'tillr-{module}'
            tags = ['-tags', 'observatory'] if module == 'view' and (source / 'web/dist/index.html').is_file() else []
            run('go', 'build', '-buildvcs=true', *tags, '-o', target, '.', cwd=source,
                env=build_env)
            programs.append(target)
        for program in programs:
            run(program, '--help', stdout=subprocess.DEVNULL)
        adapter = root / 'tools/tillr-loop/tillr-loop'
        check = [stage / 'tillr-drain', 'loop', '--check', '--root', root]
        for face in ('gate', 'land', 'verdict'):
            check.extend([f'--{face}', shlex.join(['python3', str(adapter), face])])
        run(*check, env={**os.environ, 'PATH': f'{stage}:{os.environ["PATH"]}'})
        if git('status', '--porcelain') or git('rev-parse', 'HEAD') != tip:
            raise RuntimeError('checkout changed during the roll')
        # Smoke can take time; report the build that was running at the handoff.
        old = builds(store)
        live.mkdir(exist_ok=True)
        for program in programs:
            os.replace(program, live / program.name)
    roles = ('drain', 'view')
    if no_request:
        return report(old, old, dict.fromkeys(roles, 'not-requested'), live)

    def request(role):
        result = subprocess.run([live / f'tillr-{role}', 'restart', '--root', root, '--wait'],
                                text=True, stdout=subprocess.PIPE)
        lines = result.stdout.splitlines()
        if len(lines) == 2 and lines[1].startswith(lines[0] + ': '):
            answer = lines[1].split()[1]
            if not result.returncode or answer == 'refused':
                return answer
        return 'error'

    with ThreadPoolExecutor(max_workers=2) as workers:
        answers = dict(zip(roles, workers.map(request, roles)))
    # The view acknowledges adoption just before publishing its new build fact.
    deadline = time.monotonic() + 5
    while True:
        new = builds(store)
        if all(answer != 'restarted' or new.get(role, {}).get('seq', 0) > old.get(role, {}).get('seq', 0)
               for role, answer in answers.items()) or time.monotonic() >= deadline:
            return report(old, new, answers, live)
        time.sleep(0.05)


def main():
    parser = argparse.ArgumentParser(description='Test and install the committed tillr tools.')
    parser.add_argument('--no-request', action='store_true',
                        help='install without requesting restarts; the drain will request them')
    parser.add_argument('--pi', action='store_true',
                        help='build the keyed pi fork binary and update current.json')
    args = parser.parse_args()
    try:
        root = Path(git('rev-parse', '--show-toplevel'))
        live = Path(os.environ['YOLO_DURABLE_DIR']).resolve() / 'bin'
        return roll(root, live, args.no_request, build_pi=args.pi)
    except (OSError, RuntimeError, KeyError, sqlite3.Error, subprocess.CalledProcessError) as exc:
        parser.exit(1, f'tillr-roll: {exc}\n')


if __name__ == '__main__':
    raise SystemExit(main())
