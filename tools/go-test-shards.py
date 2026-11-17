#!/usr/bin/env python3
"""Build each Go package once; balance independent test processes on recorded durations."""
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import json
import math
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

DEFAULT_DURATIONS = Path(__file__).with_name('go-test-durations.json')


class ShardError(RuntimeError):
    """A named shard failed or did not prove completion of its assigned tests."""

    def __init__(self, message, events=()):
        super().__init__(message)
        self.events = list(events)


def assign(names, durations, count):
    groups = [[] for _ in range(count)]
    unknown = [name for name in names if name not in durations]
    known_slots = max(1, count - bool(unknown))
    loads = [0.0] * known_slots
    for name in sorted((n for n in names if n in durations),
                       key=lambda n: (-durations[n], n)):
        slot = min(range(known_slots), key=lambda i: (loads[i], i))
        groups[slot].append(name)
        loads[slot] += durations[name]
    groups[-1].extend(unknown)
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


def run_shard(package, cwd, binary, names, index, scratch, timeout, git_values=None):
    label = f'{package} shard {index}'
    env = private_environment(label, scratch, git_values)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise ShardError(f'{label} cannot start: test binary is missing or not executable: {binary}')
    pattern = '^(' + '|'.join(re.escape(n) for n in names) + ')$'
    command = ['go', 'tool', 'test2json', '-t', '-p', package, str(binary),
               '-test.v=test2json', '-test.count=1', '-test.parallel=48',
               '-test.timeout=' + timeout, '-test.run=' + pattern]
    try:
        result = subprocess.run(command, cwd=cwd, env=env, capture_output=True, text=True)
    except OSError as exc:
        raise ShardError(f'{label} cannot start: {exc}') from exc
    try:
        events = [json.loads(line) for line in result.stdout.splitlines()]
    except ValueError as exc:
        raise ShardError(f'{label} invalid test report: {exc}') from exc
    if result.returncode or any(e.get('Action') == 'fail' for e in events):
        output = ''.join(e.get('Output', '') for e in events)
        raise ShardError(f'{label} failed (exit {result.returncode}):\n{output}{result.stderr}', events)
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


def run(module, count, durations, scratch, timeout):
    stream = subprocess.check_output(['go', 'list', '-json', './...'], cwd=module, text=True)
    packages = []
    decoder = json.JSONDecoder()
    while stream.strip():
        package, end = decoder.raw_decode(stream.lstrip())
        packages.append(package)
        stream = stream.lstrip()[end:]
    jobs = []
    git_values = global_git_config()
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
                             index, scratch / f'package-{number}-shard-{index}', timeout, git_values))
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
    parser.add_argument('--timeout', default='0', help='explicit per-shard Go timeout; default disables nested wall deadlines')
    args = parser.parse_args()
    if args.shards < 1:
        parser.error('shards must be positive')
    try:
        durations = load_durations(args.durations)
        parent = args.scratch or os.environ.get('TMPDIR') or os.environ.get('YOLO_DURABLE_DIR')
        if not parent:
            parser.error('set TMPDIR or YOLO_DURABLE_DIR, or pass --scratch (never defaults to /tmp)')
        parent = Path(parent).resolve()
        parent.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='go-shards-', dir=parent) as temporary:
            events = run(args.module.resolve(), args.shards, durations, Path(temporary), args.timeout)
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
