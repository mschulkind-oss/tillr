#!/usr/bin/env python3
"""Build each Go package once; balance independent test processes on recorded durations."""
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import contextlib
import fcntl
import json
import math
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import threading
import time

DEFAULT_DURATIONS = Path(__file__).with_name('go-test-durations.json')
# Bound a stalled test or TestMain without timing out a shard that keeps completing tests.
DEFAULT_SHARD_TIMEOUT = 480.0


class ShardError(RuntimeError):
    """A named shard failed or did not prove completion of its assigned tests."""

    def __init__(self, message, events=()):
        super().__init__(message)
        self.events = list(events)


def assign(names, durations, count):
    groups = [[] for _ in range(count)]
    known = [name for name in names if name in durations]
    unknown = [name for name in names if name not in durations]
    loads = [0.0] * count
    for name in sorted(known, key=lambda n: (-durations[n], n)):
        slot = min(range(count), key=lambda i: (loads[i], i))
        groups[slot].append(name)
        loads[slot] += durations[name]
    # Each unmeasured test still occupies time. Distributing them with the mean
    # recorded cost prevents one growing fallback shard from holding the suite.
    estimate = sum(durations[name] for name in known) / len(known) if known else 1.0
    estimate = estimate or 1.0
    for name in unknown:
        slot = min(range(count), key=lambda i: (loads[i], i))
        groups[slot].append(name)
        loads[slot] += estimate
    return groups


def global_git_config():
    """Snapshot inherited global values, flattening includes so writes stay private."""
    # The all-scope reader treats a missing global file as empty, like Git normally
    # does, but still diagnoses malformed/unreadable files. Select only global values;
    # do not copy repository-local settings into unrelated fixture repositories.
    raw = subprocess.check_output(['git', 'config', '--list', '--includes',
                                   '--show-scope', '--null'], text=True)
    values = []
    fields = raw.rstrip('\0').split('\0') if raw else []
    for scope, entry in zip(fields[::2], fields[1::2]):
        if scope != 'global':
            continue
        key, separator, value = entry.partition('\n')
        if key.lower() == 'include.path' or key.lower().startswith('includeif.'):
            continue
        values.append((key, value if separator else 'true'))
    return values


def private_environment(label, scratch, git_values=None):
    """Isolate TestMain too, including discovery, while keeping owner git values."""
    config = scratch / 'gitconfig'
    try:
        scratch.mkdir(parents=True)
        config.write_text('')
        values = global_git_config() if git_values is None else git_values
        for key, value in values:
            subprocess.run(['git', 'config', '--file', str(config), '--add', key, value],
                           check=True, capture_output=True, text=True)
    except (OSError, subprocess.CalledProcessError) as exc:
        raise ShardError(f'{label} cannot prepare private git config: {exc}') from exc
    return dict(os.environ, TMPDIR=str(scratch), YOLO_DURABLE_DIR=str(scratch),
                GIT_CONFIG_GLOBAL=str(config))


def shared_cache_dir():
    """Return Go's shared build cache directory so separate invocations cooperate."""
    gocache = os.environ.get('GOCACHE', '').strip()
    if gocache and gocache != 'off':
        return Path(gocache).resolve()
    if sys.platform == 'darwin':
        return (Path.home() / 'Library' / 'Caches' / 'go-build').resolve()
    xdg = os.environ.get('XDG_CACHE_HOME', '').strip()
    base = Path(xdg) if xdg else Path.home() / '.cache'
    return (base / 'go-build').resolve()


def default_shard_limit():
    """Machine-wide limit on concurrently running shards, sized from the core count."""
    return max(1, (os.cpu_count() or 1) // 2)


@contextlib.contextmanager
def acquire_shard_slot(lock_dir, limit):
    """Acquire one slot from a counted flock pool under lock_dir; wait without timeout."""
    limit = max(1, limit)
    lock_dir.mkdir(parents=True, exist_ok=True)
    fd = None
    offset = os.getpid() + threading.get_ident()
    while fd is None:
        for step in range(limit):
            slot = (offset + step) % limit
            path = lock_dir / f'slot-{slot}.lock'
            candidate = os.open(path, os.O_CREAT | os.O_RDWR, 0o666)
            try:
                fcntl.flock(candidate, fcntl.LOCK_EX | fcntl.LOCK_NB)
                fd = candidate
                break
            except BlockingIOError:
                os.close(candidate)
            except BaseException:
                os.close(candidate)
                raise
        if fd is None:
            time.sleep(0.05)
    try:
        yield
    finally:
        try:
            fcntl.flock(fd, fcntl.LOCK_UN)
        finally:
            os.close(fd)


def run_shard(package, cwd, binary, names, index, scratch, timeout, git_values=None,
              wall_timeout=DEFAULT_SHARD_TIMEOUT, lock_dir=None, shard_limit=None):
    label = f'{package} shard {index}'
    env = private_environment(label, scratch, git_values)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise ShardError(f'{label} cannot start: test binary is missing or not executable: {binary}')
    effective_lock_dir = lock_dir if lock_dir is not None else shared_cache_dir() / 'go-test-shards-locks'
    effective_limit = shard_limit if shard_limit is not None else default_shard_limit()
    pattern = '^(' + '|'.join(re.escape(n) for n in names) + ')$'
    command = ['go', 'tool', 'test2json', '-t', '-p', package, str(binary),
               '-test.v=test2json', '-test.count=1', '-test.parallel=48',
               '-test.timeout=' + timeout, '-test.run=' + pattern]
    try:
        # Files rather than pipes let us recover even if an escaped descendant keeps
        # its inherited output descriptors open after the shard itself is killed.
        with acquire_shard_slot(effective_lock_dir, effective_limit), \
                tempfile.NamedTemporaryFile(dir=scratch) as out, tempfile.NamedTemporaryFile(dir=scratch) as err, \
                open(out.name, 'rb') as reader, open(err.name, 'rb') as error_reader:
            process = subprocess.Popen(command, cwd=cwd, env=env, stdout=out, stderr=err,
                                       start_new_session=True)
            deadline = time.monotonic() + wall_timeout
            pending = b''
            seen_progress = set()
            assigned = set(names)
            timed_out = False
            while True:
                # The reader has its own file offset; seeking a descriptor shared with
                # the writer would corrupt its output. Read a finite snapshot each time.
                size = os.fstat(out.fileno()).st_size
                lines = (pending + reader.read(max(0, size - reader.tell()))).split(b'\n')
                pending = lines.pop()
                for line in lines:
                    try:
                        event = json.loads(line)
                    except ValueError:
                        continue  # The complete report is validated below.
                    key = (event.get('Test'), event.get('Action'))
                    if key[0] in assigned and key[1] in ('run', 'pass', 'skip', 'fail') and key not in seen_progress:
                        seen_progress.add(key)
                        deadline = time.monotonic() + wall_timeout
                if process.poll() is not None:
                    break
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    # Kill test2json, the test binary, and its ordinary descendants together.
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    process.wait()
                    timed_out = True
                    break
                try:
                    process.wait(timeout=min(1.0, remaining))
                except subprocess.TimeoutExpired:
                    pass
            reader.seek(0)
            # Snapshot the sizes: even an escaped writer must not keep a read alive.
            stdout = reader.read(os.fstat(out.fileno()).st_size).decode('utf-8', errors='replace')
            stderr = error_reader.read(os.fstat(err.fileno()).st_size).decode('utf-8', errors='replace')
    except OSError as exc:
        raise ShardError(f'{label} cannot start: {exc}') from exc
    events = []
    for line in stdout.splitlines():
        try:
            events.append(json.loads(line))
        except ValueError as exc:
            if not timed_out:
                raise ShardError(f'{label} invalid test report: {exc}', events) from exc
            # A killed test2json can leave its last JSON record incomplete.
            break
    if timed_out:
        active = set()
        for event in events:
            name = event.get('Test')
            if name and event.get('Action') == 'run':
                active.add(name)
            elif name and event.get('Action') in ('pass', 'skip', 'fail'):
                active.discard(name)
        pending = set(names) - {e.get('Test') for e in events
                                if e.get('Action') in ('pass', 'skip', 'fail')}
        detail = ', '.join(sorted(active)) or 'none reported'
        remaining = ', '.join(sorted(pending)) or 'none'
        raise ShardError(f'{label} timed out after {wall_timeout:g}s without top-level test progress; running: {detail}; '
                         f'not completed: {remaining}\n{stderr}', events)
    if process.returncode or any(e.get('Action') == 'fail' for e in events):
        output = ''.join(e.get('Output', '') for e in events)
        raise ShardError(f'{label} failed (exit {process.returncode}):\n{output}{stderr}', events)
    completed = {e.get('Test') for e in events if e.get('Action') in ('pass', 'skip')}
    missing = set(names) - completed
    if missing:
        raise ShardError(f'{label} missing completion: {", ".join(sorted(missing))}', events)
    return events


def load_durations(path):
    # A missing default record is an error, not a silent degradation of balancing.
    record = json.loads(path.read_text())
    if not isinstance(record, dict):
        raise ValueError('durations must map package names to test durations')
    for package, tests in record.items():
        if not isinstance(tests, dict):
            raise ValueError(f'{package}: durations must be an object')
        for name, duration in tests.items():
            if not isinstance(duration, (int, float)) or not math.isfinite(duration) or duration < 0:
                raise ValueError(f'{package}/{name}: invalid duration {duration!r}')
    return record


def run(module, count, durations, scratch, timeout, *, shard_timeout=DEFAULT_SHARD_TIMEOUT, lock_dir=None):
    stream = subprocess.check_output(['go', 'list', '-json', './...'], cwd=module, text=True)
    packages = []
    decoder = json.JSONDecoder()
    while stream.strip():
        package, end = decoder.raw_decode(stream.lstrip())
        packages.append(package)
        stream = stream.lstrip()[end:]
    jobs = []
    git_values = global_git_config()
    lock_dir = lock_dir if lock_dir is not None else shared_cache_dir() / 'go-test-shards-locks'
    shard_limit = default_shard_limit()
    for number, package in enumerate(packages):
        if not package.get('TestGoFiles') and not package.get('XTestGoFiles'):
            continue
        binary = scratch / f'package-{number}.test'
        subprocess.run(['go', 'test', '-c', '-o', str(binary), package['ImportPath']],
                       cwd=module, check=True)
        discovery_env = private_environment(f"{package['ImportPath']} discovery",
                                            scratch / f'package-{number}-discovery', git_values)
        listed = subprocess.check_output([str(binary), '-test.list=.', '-test.timeout=0'],
                                         cwd=package['Dir'], env=discovery_env, text=True)
        names = [line for line in listed.splitlines()
                 if re.match(r'^(Test|Example|Fuzz)\w*$', line)]
        for index, group in enumerate(assign(names, durations.get(package['ImportPath'], {}), count)):
            # A package with only benchmarks can still fail in TestMain. Run it
            # once even with no runnable names, as the native Go driver does.
            if group or (not names and index == 0):
                jobs.append((package['ImportPath'], Path(package['Dir']), binary, group,
                             index, scratch / f'package-{number}-shard-{index}', timeout, git_values,
                             shard_timeout, lock_dir, shard_limit))
    events = []
    errors = []
    # Each package has at most K jobs. Run packages side by side, just as go test
    # does, rather than making a second large package wait behind the first one's K.
    with ThreadPoolExecutor(max_workers=max(1, len(jobs))) as pool:
        futures = [pool.submit(run_shard, *job) for job in jobs]
        for future in as_completed(futures):
            try:
                events.extend(future.result())
            except ShardError as exc:
                events.extend(exc.events)
                errors.append(str(exc))
    if errors:
        raise ShardError('\n'.join(errors), events)
    return events


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('module', type=Path)
    parser.add_argument('-k', '--shards', type=int, default=6)
    parser.add_argument('--durations', type=Path, default=DEFAULT_DURATIONS)
    parser.add_argument('--record', type=Path, help='write measured top-level durations after success')
    parser.add_argument('--events', type=Path, help='write Go test JSON events, including failed shards')
    parser.add_argument('--scratch', type=Path, help='parent of private scratch; defaults to TMPDIR or YOLO_DURABLE_DIR')
    parser.add_argument('--lock-dir', type=Path, help='shared lock directory; defaults to <GOCACHE>/go-test-shards-locks')
    parser.add_argument('--timeout', default='0', help='explicit Go test timeout; default disables Go\'s own deadline')
    parser.add_argument('--shard-timeout', type=float, default=None, metavar='SECONDS',
                        help=f'maximum seconds without top-level test progress per shard (default {DEFAULT_SHARD_TIMEOUT:g})')
    args = parser.parse_args()
    if args.shards < 1:
        parser.error('shards must be positive')
    if args.shard_timeout is not None and (not math.isfinite(args.shard_timeout) or args.shard_timeout <= 0):
        parser.error('shard timeout must be a positive finite number of seconds')
    try:
        durations = load_durations(args.durations)
        parent = args.scratch or os.environ.get('TMPDIR') or os.environ.get('YOLO_DURABLE_DIR')
        if not parent:
            parser.error('set TMPDIR or YOLO_DURABLE_DIR, or pass --scratch (never defaults to /tmp)')
        parent = Path(parent).resolve()
        parent.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='go-shards-', dir=parent) as temporary:
            options = {} if args.shard_timeout is None else {'shard_timeout': args.shard_timeout}
            if args.lock_dir is not None:
                options['lock_dir'] = args.lock_dir.resolve()
            events = run(args.module.resolve(), args.shards, durations, Path(temporary), args.timeout, **options)
        if args.events:
            args.events.write_text(''.join(json.dumps(e) + '\n' for e in events))
        if args.record:
            for event in events:
                name = event.get('Test', '')
                if name and '/' not in name and event.get('Action') in ('pass', 'skip'):
                    durations.setdefault(event['Package'], {})[name] = event.get('Elapsed', 0)
            args.record.write_text(json.dumps(durations, sort_keys=True, indent=2) + '\n')
        passed = sum(e.get('Action') == 'pass' and bool(e.get('Test')) for e in events)
        print(f'go-test-shards: {args.module}: {passed} tests passed, {args.shards} shards per package')
        return 0
    except ShardError as exc:
        if args.events:
            try:
                args.events.write_text(''.join(json.dumps(e) + '\n' for e in exc.events))
            except OSError as error:
                print(f'go-test-shards: cannot write events: {error}', file=sys.stderr)
        print(f'go-test-shards: {exc}', file=sys.stderr)
        return 1
    except (OSError, ValueError, subprocess.CalledProcessError) as exc:
        print(f'go-test-shards: {exc}', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
