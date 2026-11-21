# Architecture and invariants: docs/reference/tillr-restart.md
import argparse
from concurrent.futures import ThreadPoolExecutor
from contextlib import closing
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
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
SCHEMA_CHECK = 'tillr-drain: schema migration required'
SCHEMA_STAGE = '.tillr-schema-roll-'
EXIT_INCOMPATIBLE = 3  # tillr/record.ExitIncompatible
MODULES = ('cli', 'drain', 'gate', 'item', 'land', 'run', 'store', 'view')
TRACE_PREFIXES = ('tillr-sqlite ', 'tillr-sqlite-stack ', 'tillr-git ')
SHARD_FAILURE_RE = re.compile(r'^(?:go-test-shards:\s+)?\S+ shard \d+ (?:failed|timed out|cannot start|missing completion)\b')
GO_FAIL_RE = re.compile(r'^---\s*FAIL:\s*(\S+)')


def run(*args, **kwargs):
    if 'capture_output' in kwargs or ('stdout' in kwargs and 'stderr' in kwargs):
        return subprocess.run(args, check=True, text=True, **kwargs)
    call_kwargs = dict(kwargs)
    capture_stdout = 'stdout' not in call_kwargs
    capture_stderr = 'stderr' not in call_kwargs
    if capture_stdout:
        call_kwargs['stdout'] = subprocess.PIPE
    if capture_stderr:
        call_kwargs['stderr'] = subprocess.PIPE
    completed = subprocess.run(args, check=False, text=True, **call_kwargs)
    if capture_stdout and completed.stdout:
        sys.stdout.write(completed.stdout)
        sys.stdout.flush()
    if capture_stderr and completed.stderr:
        sys.stderr.write(completed.stderr)
        sys.stderr.flush()
    completed.check_returncode()
    return completed


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


def is_slot_notice(line):
    return line.startswith('go-test-shards: ') and (
        ' waiting for a test slot' in line or ' yielding to a waiting ' in line
    )


def failure_detail(cause):
    if not isinstance(cause, subprocess.CalledProcessError):
        return str(cause)
    raw = '\n'.join(part for part in (cause.stdout, cause.stderr) if part)
    lines = [
        line for line in raw.splitlines()
        if line.strip() and not line.startswith(TRACE_PREFIXES) and not is_slot_notice(line)
    ]
    if not lines:
        return str(cause)
    shard_lines = []
    fail_tests = []
    diagnostics = []
    for line in lines:
        stripped = line.strip()
        if SHARD_FAILURE_RE.match(stripped) or (
            stripped.startswith('go-test-shards: ') and ' tests passed,' not in stripped
        ):
            normalized = stripped if stripped.startswith('go-test-shards: ') else f'go-test-shards: {stripped}'
            shard_lines.append(normalized)
            match = re.search(r'failed \(exit -?\d+\):\s*(.+)$', normalized)
            if match:
                fail_tests.extend(name.strip() for name in match.group(1).split(',') if name.strip())
            continue
        fail_match = GO_FAIL_RE.match(stripped)
        if fail_match:
            fail_tests.append(fail_match.group(1))
            diagnostics.append(stripped)
            continue
        if line.startswith((' ', '\t')) and not stripped.startswith(('=== ', '--- PASS:')):
            diagnostics.append(stripped)
    if shard_lines or fail_tests:
        summary = list(dict.fromkeys(shard_lines))
        ordered_tests = list(dict.fromkeys(fail_tests))
        joined = '\n'.join(summary)
        missing_tests = [name for name in ordered_tests if name not in joined]
        if missing_tests:
            summary.append(f"failing tests: {', '.join(missing_tests)}")
        summary.extend(list(dict.fromkeys(diagnostics))[:12])
        return '\n'.join(summary)
    return '\n'.join(lines[-12:])


class RollFailed(RuntimeError):
    """A roll step failed; the step's name is what the recorded event carries."""

    def __init__(self, step, cause):
        self.step = step
        self.cause = cause
        self.detail = failure_detail(cause)
        super().__init__(f'{step}: {self.detail}')


def stepped(step, call, *args, **kwargs):
    try:
        return call(*args, **kwargs)
    except (OSError, RuntimeError, sqlite3.Error, subprocess.CalledProcessError) as exc:
        if (step == 'loop check' and isinstance(exc, subprocess.CalledProcessError)
                and exc.returncode == EXIT_INCOMPATIBLE):
            step = 'store check'
        raise RollFailed(step, exc) from exc


def record_failure(store, commit, step, detail):
    """Append the failed roll to the store, best effort: the exit status already says it failed."""
    fact = {'commit': commit, 'step': step, 'detail': ' '.join(str(detail).split())}
    at = int(time.time() * 1000)
    if step == 'store check':
        fact['mover'] = 'operator'
    payload = json.dumps(fact)
    try:
        with closing(sqlite3.connect(store.as_uri() + '?mode=rw', uri=True, timeout=30)) as db, db:
            db.execute('INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, ?, ?, ?, ?)',
                       (at, 'drain.roll-failed', 'commit', commit, payload))
    except sqlite3.Error as exc:
        print(f'tillr-roll: could not record the failed roll of {commit[:12]}: {exc}', file=sys.stderr)
        if step == 'store check':
            try:
                with tempfile.TemporaryDirectory(prefix='.roll-alarm-', dir=store.parent) as tmp:
                    alarm = Path(tmp) / 'alarm.json'
                    alarm.write_text(json.dumps({**fact, 'ts': at}))
                    os.replace(alarm, store.with_name(store.name + '.roll-alarm.json'))
            except OSError as err:
                print(f'tillr-roll: could not save the store roll alarm: {err}; mover: operator', file=sys.stderr)


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
    run('git', '-c', 'advice.detachedHead=false', 'checkout', '-q', '--detach', tip, cwd=dest)
    dist = shared / 'tools/tillr-view/web/dist'
    # The front end is untracked output, not part of the commit; the view embeds it when present.
    if dist.is_dir() and not (dest / 'tools/tillr-view/web/dist').exists():
        shutil.copytree(dist, dest / 'tools/tillr-view/web/dist')


def test_commit(root, source, work, tip, store, old, build_env):
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
    proved = stepped('evidence', evidence, root, tip)
    if proved is None:
        return
    accepted = proved | stepped('evidence', gated_modules, root, tip, store)
    modules = sorted(p for p in (source / 'tools').glob('tillr*') if (p / 'go.mod').is_file())
    for module in modules:
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


def stage_schema_roll(stage, live, tip, store):
    # Only the replacement loop is exposed before exec. A new sibling opening
    # the store for a write could otherwise migrate it out from under the old
    # loop (which still owns passes, work starts, and any in-flight proof).
    drain = stage / 'tillr-drain'
    pending = live / (SCHEMA_STAGE + digest(drain))
    (stage / 'roll.json').write_text(json.dumps({'commit': tip, 'store': str(store.resolve())}))
    if not pending.exists():
        os.rename(stage, pending)
        os.replace(pending / drain.name, live / drain.name)
    else:
        # A retry of the identical build can reuse the prepared siblings.
        os.replace(drain, live / drain.name)


def roll_commit(root, live, tip, no_request, store):
    """Build, smoke and install `tip` from a clean checkout of that commit alone."""
    old = stepped('store check', builds, store)
    # An automatic after-merge roll does not duplicate the repository's proof.
    # A manual roll, including a build without restart, still tests by default.
    after_merge = no_request and os.environ.get('TILLR_ROLL_VERIFICATION') == 'after-merge'
    base_env = {k: v for k, v in os.environ.items() if k not in ('TILLR_SQLITE_TRACE', 'TILLR_ROLL_VERIFICATION')}
    build_env = {**base_env, 'CGO_ENABLED': '0'}
    live.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='tillr-roll-', dir=live.parent) as work:
        work = Path(work)
        source = work / 'source'
        stepped('checkout', clean_checkout, root, tip, source, root)
        if not after_merge:
            test_commit(root, source, work, tip, store, old, build_env)
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
        checked = stepped('loop check', run, *check, cwd=source,
                          env={**base_env, 'PATH': f'{stage}:{os.environ["PATH"]}'})
        schema_roll = checked is not None and SCHEMA_CHECK in checked.stdout.splitlines()
        # Smoke can take time; report the build that was running at the handoff.
        old = stepped('store check', builds, store)
        live.mkdir(exist_ok=True)
        if schema_roll:
            stepped('stage schema roll', stage_schema_roll, stage, live, tip, store)
        else:
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

    if schema_roll:
        drain_answer = request('drain')
        answers = {'drain': drain_answer,
                   'view': request('view') if drain_answer == 'restarted' else 'not-requested'}
    else:
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
            record_failure(store, tip, failure.step, failure.detail)
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
