# Architecture and invariants: docs/reference/tillr-restart.md
import argparse
from concurrent.futures import ThreadPoolExecutor
from contextlib import closing
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import time


# The modules the landing gate runs through tools/go-test-shards.py (fork-check's GO_SHARD_DIRS).
SHARDED = ('tools/tillr-store', 'tools/tillr-item', 'tools/tillr-run', 'tools/tillr-gate',
           'tools/tillr-view', 'tools/tillr-land', 'tools/tillr-drain')
# The commit most recently installed, beside the bin directory.
ROLLED = 'tillr-roll.commit'
MODULES = ('cli', 'drain', 'gate', 'item', 'land', 'run', 'store', 'view')


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def git(*args, cwd=None):
    return run('git', *args, capture_output=True, cwd=cwd).stdout.strip()


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


class RollFailed(RuntimeError):
    """A roll step failed; the step's name is what the recorded event carries."""

    def __init__(self, step, cause):
        super().__init__(f'{step}: {cause}')
        self.step = step
        self.cause = cause


def stepped(step, call, *args, **kwargs):
    try:
        return call(*args, **kwargs)
    except (OSError, RuntimeError, subprocess.CalledProcessError) as exc:
        raise RollFailed(step, exc) from exc


def record_failure(store, commit, step, detail):
    """Append the failed roll to the store, best effort: the exit status already says it failed."""
    payload = json.dumps({'commit': commit, 'step': step, 'detail': ' '.join(str(detail).split())})
    try:
        with closing(sqlite3.connect(store, timeout=30)) as db, db:
            db.execute('INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, ?, ?, ?, ?)',
                       (int(time.time() * 1000), 'drain.roll-failed', 'commit', commit, payload))
    except sqlite3.Error as exc:
        print(f'tillr-roll: could not record the failed roll of {commit[:12]}: {exc}', file=sys.stderr)


def evidence(root, tip):
    """What already proved `tip`: the set of modules whose suites need no second run, or None
    for every module.

    The after-merge proof's green pointer proves every commit it contains, so every suite.
    A landing whose gated tree is the tree that landed was gated at exactly this tree, so the
    suites that landing's own change selected are proved; earlier landings in the range are not.
    """
    try:
        green = git('rev-parse', '--verify', '--quiet', 'refs/tillr/green^{commit}', cwd=root)
        if green:
            subprocess.run(['git', 'merge-base', '--is-ancestor', tip, green], cwd=root, check=True)
            return None
    except subprocess.CalledProcessError:
        pass
    return set()


def gated_modules(root, tip, store):
    """Modules the landing of `tip` changed and gated at exactly the landed tree."""
    try:
        with closing(sqlite3.connect(store.as_uri() + '?mode=ro', uri=True)) as db:
            row = db.execute("SELECT gated_tree, landed_tree FROM landings WHERE trunk_after = ? AND result = 'landed'",
                             (tip,)).fetchone()
    except sqlite3.Error:
        return set()
    if not row or not row[0] or row[0] != row[1] or row[1] != git('rev-parse', tip + '^{tree}', cwd=root):
        return set()
    try:
        changed = git('diff', '--name-only', '-z', '--no-renames', tip + '^', tip, '--', 'tools', cwd=root)
    except subprocess.CalledProcessError:
        return set()
    return {p.split('/')[1] for p in changed.split('\0') if p.startswith('tools/')}


def clean_checkout(root, tip, dest, shared):
    run('git', 'clone', '-q', '--shared', '--no-checkout', str(root), str(dest))
    run('git', 'sparse-checkout', 'set', '--cone', 'tools', 'config', cwd=dest)
    run('git', '-c', 'advice.detachedHead=false', 'checkout', '-q', '--detach', tip, cwd=dest)
    dist = shared / 'tools/tillr-view/web/dist'
    # The front end is untracked output, not part of the commit; the view embeds it when present.
    if dist.is_dir() and not (dest / 'tools/tillr-view/web/dist').exists():
        shutil.copytree(dist, dest / 'tools/tillr-view/web/dist')


def roll_commit(root, live, tip, no_request, store):
    """Test, build, smoke and install `tip`, built from a clean checkout of that commit alone."""
    old = builds(store)
    # Either running part may lag. Unidentified builds cannot justify skipping tests.
    changed = set()
    for role in ('drain', 'view'):
        fact = old.get(role, {})
        revision = fact.get('revision')
        if not revision or fact.get('modified'):
            changed = None
            break
        try:
            changed.update(git('diff', '--name-only', '-z', '--no-renames', revision, tip, '--', 'tools',
                               cwd=root).split('\0'))
        except subprocess.CalledProcessError:
            changed = None
            break
    build_env = {**os.environ, 'CGO_ENABLED': '0'}
    live.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='tillr-roll-', dir=live.parent) as work:
        work = Path(work)
        source = work / 'source'
        stepped('checkout', clean_checkout, root, tip, source, root)
        proved = stepped('evidence', evidence, root, tip)
        accepted = None if proved is None else proved | stepped('evidence', gated_modules, root, tip, store)
        modules = sorted(p for p in (source / 'tools').glob('tillr*') if (p / 'go.mod').is_file())
        for module in modules:
            if proved is None:
                break
            if module.name in accepted:
                continue
            if changed is None or any(p.startswith(f'tools/{module.name}/') for p in changed):
                relative = f'tools/{module.name}'
                if relative in SHARDED:
                    # The shared slot pool bounds machine-wide test parallelism; Go's own deadline is off.
                    stepped(f'test {module.name}', run, 'python3', str(source / 'tools/go-test-shards.py'), relative,
                            '-k', '6', '--timeout', '0', '--scratch', str(work), cwd=source, env=build_env)
                else:
                    stepped(f'test {module.name}', run, 'go', 'test', '-timeout', '0', './...',
                            cwd=module, env=build_env)
        stage = work / 'stage'
        stage.mkdir()
        programs = []
        for module in MODULES:
            name = 'tillr' if module == 'cli' else f'tillr-{module}'
            target = stage / name
            module_dir = source / 'tools' / f'tillr-{module}'
            tags = ['-tags', 'observatory'] if module == 'view' and (module_dir / 'web/dist/index.html').is_file() else []
            stepped(f'build {name}', run, 'go', 'build', '-buildvcs=true', *tags, '-o', target, '.',
                    cwd=module_dir, env=build_env)
            programs.append(target)
        for program in programs:
            stepped(f'smoke {program.name}', run, program, '--help', stdout=subprocess.DEVNULL)
        adapter = source / 'tools/tillr-loop/tillr-loop'
        check = [stage / 'tillr-drain', 'loop', '--check', '--root', source, '--store', store]
        for face in ('gate', 'land', 'verdict'):
            check.extend([f'--{face}', shlex.join(['python3', str(adapter), face])])
        stepped('loop check', run, *check, cwd=source, env={**os.environ, 'PATH': f'{stage}:{os.environ["PATH"]}'})
        # Smoke can take time; report the build that was running at the handoff.
        old = builds(store)
        live.mkdir(exist_ok=True)
        for program in programs:
            stepped('install', os.replace, program, live / program.name)
        (live.parent / ROLLED).write_text(tip + '\n')
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


def roll(root, live, no_request, build_pi=False, commit=None, on_wait=None):
    """Roll one commit: the named one, else the newest tip at the moment this roll may start.

    Rolls are serialized by a lock. A request that had to wait for a running roll rolls the tip
    as of when the lock frees, and does nothing when a roll of that very tip already finished,
    so any number of landings during one roll cost exactly one further roll, of the newest tip.
    """
    if build_pi:
        if git('status', '--porcelain'):
            raise RuntimeError('refusing a dirty checkout')
        run(str(root / 'bin/pi-forks'), 'build-pi', cwd=root)
        return 0
    store = root / '.tillr/state.db'
    live.parent.mkdir(parents=True, exist_ok=True)
    with (live.parent / 'tillr-roll.lock').open('w') as lock:
        waited = False
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            waited = True
            if on_wait:
                on_wait()
            fcntl.flock(lock, fcntl.LOCK_EX)
        tip = git('rev-parse', '--verify', (commit or 'HEAD') + '^{commit}', cwd=root)
        rolled = live.parent / ROLLED
        if waited and commit is None and rolled.is_file() and rolled.read_text().strip() == tip:
            print(f'tillr-roll: {tip[:12]} was rolled while this request waited')
            return 0
        try:
            return roll_commit(root, live, tip, no_request, store)
        except RollFailed as failure:
            record_failure(store, tip, failure.step, failure.cause)
            raise


def roll_pi_extensions(root):
    builder = root / 'tools/tillr-run/backends/pi/build-agent-dir'
    run(str(builder), cwd=root)
    return 0


def main():
    parser = argparse.ArgumentParser(description='Test and install the committed tillr tools.')
    parser.add_argument('--no-request', action='store_true',
                        help='install without requesting restarts; the drain will request them')
    parser.add_argument('--commit', help='roll this commit instead of the newest tip')
    parser.add_argument('--pi', action='store_true',
                        help='build the keyed pi fork binary and update current.json')
    parser.add_argument('--pi-extensions', action='store_true',
                        help='rebuild the drain pi extensions agent directory without restarting drain/view')
    args = parser.parse_args()
    try:
        root = Path(git('rev-parse', '--show-toplevel'))
        if args.pi_extensions:
            return roll_pi_extensions(root)
        live = Path(os.environ['YOLO_DURABLE_DIR']).resolve() / 'bin'
        return roll(root, live, args.no_request, build_pi=args.pi, commit=args.commit)
    except (OSError, RuntimeError, KeyError, sqlite3.Error, subprocess.CalledProcessError) as exc:
        parser.exit(1, f'tillr-roll: {exc}\n')


if __name__ == '__main__':
    raise SystemExit(main())
