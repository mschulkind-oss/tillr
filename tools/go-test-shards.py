#!/usr/bin/env python3
"""Build each Go package once; balance independent test processes on recorded durations."""
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import contextlib
import hashlib
import importlib.util
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
PROGRESS_OUT_ENV = 'TILLR_GO_SHARDS_PROGRESS_OUT'
DEFAULT_PROGRESS_INTERVAL = 60.0
SHARD_THRESHOLD = 100
MAX_SHARDS = 2 * _cpu_count if (_cpu_count := os.cpu_count()) and _cpu_count > 0 else 12
_GO_AFFECTED_MOD = None


def _load_go_affected():
    global _GO_AFFECTED_MOD
    if _GO_AFFECTED_MOD is not None:
        return _GO_AFFECTED_MOD
    path = Path(__file__).resolve().parent / 'gate' / 'go_affected.py'
    spec = importlib.util.spec_from_file_location('go_affected', path)
    if spec is None or spec.loader is None:
        raise ShardError(f'cannot load go_affected from {path}')
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    _GO_AFFECTED_MOD = mod
    return mod


def _locate_repo_root(start_path, *, fallback_to_script=False, stop_at_temp_roots=False):
    start = Path(start_path).resolve()
    temp_roots = set()
    if stop_at_temp_roots:
        for var in ('TMPDIR', 'YOLO_DURABLE_DIR'):
            val = os.environ.get(var, '').strip()
            if val:
                temp_roots.add(Path(val).resolve())
    for candidate in (start, *start.parents):
        if stop_at_temp_roots and candidate in temp_roots:
            break
        if (candidate / 'tools' / 'gate' / 'go-test-edges.json').is_file() or (candidate / '.git').exists():
            return candidate
    if fallback_to_script:
        return Path(__file__).resolve().parents[1]
    return None


def _check_cached_worker_refusal(module):
    run_id = os.environ.get('TILLR_RUN_ID', '').strip()
    if not run_id:
        return
    abs_module = Path(os.path.abspath(module))
    resolved_module = Path(module).resolve()
    if '.forks' in abs_module.parts or '.forks' in resolved_module.parts:
        raise ShardError(f'--cache is refused inside .forks/ when TILLR_RUN_ID is set: {module}')
    repo_roots = {Path(__file__).resolve().parents[1]}
    env_repo = os.environ.get('TILLR_REPO_ROOT', '').strip()
    if env_repo:
        repo_roots.add(Path(env_repo).resolve())
    try:
        top = subprocess.check_output(
            ['git', '-C', str(Path(__file__).resolve().parent), 'rev-parse', '--show-toplevel'],
            text=True, stderr=subprocess.DEVNULL,
        ).strip()
        if top:
            repo_roots.add(Path(top).resolve())
    except (OSError, subprocess.CalledProcessError):
        pass
    temp_roots = [
        Path(val).resolve()
        for val in (os.environ.get('TMPDIR', '').strip(), os.environ.get('YOLO_DURABLE_DIR', '').strip())
        if val
    ]
    matching_repos = [r for r in repo_roots if resolved_module.is_relative_to(r)]
    if not matching_repos:
        return
    deepest_repo = max(matching_repos, key=lambda p: len(p.parts))
    matching_temps = [t for t in temp_roots if resolved_module.is_relative_to(t)]
    if matching_temps:
        deepest_temp = max(matching_temps, key=lambda p: len(p.parts))
        if deepest_temp != deepest_repo and deepest_temp.is_relative_to(deepest_repo):
            return
    raise ShardError(f'--cache is refused on repository module {module} when TILLR_RUN_ID is set')


# A shard runs under a guard process: the guard is the session and group leader, starts the
# real command in its group, and kills the group when the runner's end of a pipe closes, so a
# runner that is killed, even by SIGKILL, takes every shard process with it.
GUARD_FLAG = '--shard-guard'
_live_groups = set()
_live_lock = threading.Lock()


def guard_main(descriptor, command):
    """Run command in this group and end the whole group when the runner's pipe closes."""
    def watch():
        try:
            while os.read(descriptor, 4096):
                pass
        except OSError:
            pass
        os.killpg(0, signal.SIGKILL)
    threading.Thread(target=watch, daemon=True).start()
    child = subprocess.Popen(command)
    code = child.wait()
    return 128 - code if code < 0 else code


def kill_group(group):
    try:
        os.killpg(group, signal.SIGKILL)
    except (ProcessLookupError, PermissionError):
        pass


def kill_live_groups():
    """Kill every shard process group this runner has started and not yet reaped."""
    with _live_lock:
        groups = list(_live_groups)
    for group in groups:
        kill_group(group)


def end_with_runner(number, _frame):
    """Signal handler: no shard outlives a runner that is told to stop."""
    kill_live_groups()
    raise SystemExit(128 + number)


def install_signal_handlers():
    for number in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
        signal.signal(number, end_with_runner)


class ShardError(RuntimeError):
    """A named shard failed or did not prove completion of its assigned tests."""

    def __init__(self, message, events=(), timed_out=False):
        super().__init__(message)
        self.events = list(events)
        self.timed_out = timed_out


class _ProgressTracker:
    """Thread-safe monotonic completion counter with rate-limited atomic JSON snapshots."""

    def __init__(self, progress_file, progress_interval):
        self._path = Path(progress_file)
        self.interval = float(progress_interval)
        self._lock = threading.Lock()
        run_started = time.monotonic()
        self._next_publish_at = run_started + self.interval
        self._last_published_units = 0
        self._seq = 0
        self._completed_packages = 0
        self._completed_tests = 0
        self._completed_shards = 0
        self._completed_units = 0
        self._last_detail = ''

    def record_package(self, detail):
        with self._lock:
            self._completed_packages += 1
            self._completed_units = self._completed_packages + self._completed_tests + self._completed_shards
            self._last_detail = detail
            self._maybe_publish_locked()

    def record_test(self, detail):
        with self._lock:
            self._completed_tests += 1
            self._completed_units = self._completed_packages + self._completed_tests + self._completed_shards
            self._last_detail = detail
            self._maybe_publish_locked()

    def record_shard(self, detail):
        with self._lock:
            self._completed_shards += 1
            self._completed_units = self._completed_packages + self._completed_tests + self._completed_shards
            self._last_detail = detail
            self._maybe_publish_locked()

    def maybe_publish(self):
        with self._lock:
            self._maybe_publish_locked()

    def _maybe_publish_locked(self):
        now = time.monotonic()
        if now < self._next_publish_at or self._completed_units <= self._last_published_units:
            return
        self._seq += 1
        self._last_published_units = self._completed_units
        self._next_publish_at = now + self.interval
        payload = json.dumps({
            'seq': self._seq,
            'completed_units': self._completed_units,
            'completed_packages': self._completed_packages,
            'completed_tests': self._completed_tests,
            'completed_shards': self._completed_shards,
            'last_detail': self._last_detail,
        }) + '\n'
        tmp_path = None
        try:
            self._path.parent.mkdir(parents=True, exist_ok=True)
            fd, tmp_name = tempfile.mkstemp(prefix='.go-progress-', dir=str(self._path.parent))
            tmp_path = Path(tmp_name)
            try:
                os.write(fd, payload.encode('utf-8'))
            finally:
                os.close(fd)
            os.replace(tmp_path, self._path)
            tmp_path = None
        except OSError as error:
            if tmp_path is not None:
                with contextlib.suppress(OSError):
                    tmp_path.unlink()
            print(f'go-test-shards: cannot write progress file: {error}', file=sys.stderr)


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


def _is_git_shim(path):
    if path.parent.parts[-3:] == ('.yolo', 'bin', 'block'):
        return True
    try:
        with path.open('rb') as handle:
            head = handle.read(256)
    except OSError:
        return False
    return head.startswith(b'#!') and (
        b'_yg_tool=git' in head or b'# tillr-git-guard' in head
    )


def _is_tillr_git_guard(path):
    try:
        with path.open('rb') as handle:
            return handle.read(64).startswith(b'#!/bin/sh\n# tillr-git-guard')
    except OSError:
        return False


def resolve_real_git(path_value=None):
    """Find the real git executable behind block/git or tillr-git-guard shims."""
    raw_path = os.environ.get('PATH', '') if path_value is None else path_value
    saw_shim = False
    for entry in raw_path.split(os.pathsep):
        if not entry:
            continue
        candidate = Path(entry) / 'git'
        if not (candidate.is_file() and os.access(candidate, os.X_OK)):
            continue
        if _is_git_shim(candidate):
            saw_shim = True
            continue
        return candidate, saw_shim
    if saw_shim:
        for fallback in (Path('/bin/git'), Path('/usr/bin/git')):
            if fallback.is_file() and os.access(fallback, os.X_OK) and not _is_git_shim(fallback):
                return fallback, True
    return None, saw_shim


def _ensure_git_bin_dir(base_dir, real_git):
    digest = hashlib.sha256(str(real_git).encode('utf-8')).hexdigest()[:12]
    bin_dir = base_dir / 'go-test-git-bin' / digest
    link = bin_dir / 'git'
    try:
        if link.is_symlink() and os.readlink(link) == str(real_git):
            return bin_dir
    except OSError:
        pass
    bin_dir.mkdir(parents=True, exist_ok=True)
    tmp_link = bin_dir / f'.git.{os.getpid()}.{threading.get_ident()}'
    with contextlib.suppress(OSError):
        tmp_link.unlink()
    tmp_link.symlink_to(real_git)
    try:
        os.replace(tmp_link, link)
    except OSError:
        with contextlib.suppress(OSError):
            tmp_link.unlink()
        if not (link.is_symlink() and os.readlink(link) == str(real_git)):
            raise
    return bin_dir


def _bypass_git_shim_env(base_env, *candidate_roots):
    env = dict(base_env)
    path_val = env.get('PATH', '')
    real_git, saw_shim = resolve_real_git(path_val)
    if not saw_shim:
        return env
    kept = [
        entry for entry in path_val.split(os.pathsep)
        if not (entry and _is_tillr_git_guard(Path(entry) / 'git'))
    ]
    cleaned_path = os.pathsep.join(kept)
    real_after, still_shimmed = resolve_real_git(cleaned_path)
    if not still_shimmed and real_after is not None:
        env['PATH'] = cleaned_path
        return env
    if real_git is not None:
        for root_dir in candidate_roots:
            if root_dir is None:
                continue
            try:
                git_bin = _ensure_git_bin_dir(Path(root_dir), real_git)
                env['PATH'] = f'{git_bin}{os.pathsep}{cleaned_path}' if cleaned_path else str(git_bin)
                return env
            except OSError:
                continue
    env['PATH'] = cleaned_path
    env['YOLO_BYPASS_SHIMS'] = '1'
    return env


def global_git_config(env=None):
    """Snapshot inherited global values, flattening includes so writes stay private."""
    # The all-scope reader treats a missing global file as empty, like Git normally
    # does, but still diagnoses malformed/unreadable files. Select only global values;
    # do not copy repository-local settings into unrelated fixture repositories.
    base = os.environ if env is None else env
    fallback = base.get('TMPDIR') or base.get('YOLO_DURABLE_DIR')
    git_env = _bypass_git_shim_env(
        base,
        shared_cache_dir(),
        Path(fallback).resolve() if fallback else None,
    )
    raw = subprocess.check_output(['git', 'config', '--list', '--includes',
                                   '--show-scope', '--null'], text=True, env=git_env)
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
        env = _bypass_git_shim_env(
            dict(os.environ, TMPDIR=str(scratch), YOLO_DURABLE_DIR=str(scratch),
                 GIT_CONFIG_GLOBAL=str(config)),
            shared_cache_dir(),
            scratch,
        )
        config.write_text('', encoding='utf-8')
        values = global_git_config(env=os.environ) if git_values is None else git_values
        for key, value in values:
            if key.lower() in ('maintenance.auto', 'gc.auto'):
                continue
            subprocess.run(['git', 'config', '--file', str(config), '--add', key, value],
                           check=True, capture_output=True, text=True, env=env)
        with config.open('a', encoding='utf-8') as handle:
            handle.write('[maintenance]\n\tauto = false\n[gc]\n\tauto = 0\n')
    except (OSError, subprocess.CalledProcessError) as exc:
        raise ShardError(f'{label} cannot prepare private git config: {exc}') from exc
    return env


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


def run_shard(package, cwd, binary, names, index, scratch, timeout, git_values=None,
              wall_timeout=DEFAULT_SHARD_TIMEOUT, progress=None):
    label = f'{package} shard {index}'
    env = private_environment(label, scratch, git_values)
    env['GOMAXPROCS'] = '1'
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise ShardError(f'{label} cannot start: test binary is missing or not executable: {binary}')
    pattern = '^(' + '|'.join(re.escape(n) for n in names) + ')$'
    command = ['go', 'tool', 'test2json', '-t', '-p', package, str(binary),
               '-test.v=test2json', '-test.count=1', '-test.parallel=1',
               '-test.timeout=' + timeout, '-test.run=' + pattern]
    try:
        # Files rather than pipes let us recover even if an escaped descendant keeps
        # its inherited output descriptors open after the shard itself is killed.
        with tempfile.NamedTemporaryFile(dir=scratch) as out, tempfile.NamedTemporaryFile(dir=scratch) as err, \
                open(out.name, 'rb') as reader, open(err.name, 'rb') as error_reader, \
                contextlib.ExitStack() as cleanup:
            lifeline_read, lifeline_write = os.pipe()
            try:
                process = subprocess.Popen(
                    [sys.executable, str(Path(__file__).resolve()), GUARD_FLAG, str(lifeline_read), '--', *command],
                    cwd=cwd, env=env, stdout=out, stderr=err, start_new_session=True,
                    pass_fds=(lifeline_read,))
            except BaseException:
                os.close(lifeline_write)
                raise
            finally:
                os.close(lifeline_read)
            with _live_lock:
                _live_groups.add(process.pid)
            # Runs on every exit from this block, including an exception: the lifeline closes
            # and the group is killed before the scratch directory the binary lives in can go.
            def end_group():
                kill_group(process.pid)
                os.close(lifeline_write)
                with _live_lock:
                    _live_groups.discard(process.pid)
                process.wait()
            cleanup.callback(end_group)
            deadline = time.monotonic() + wall_timeout
            pending = b''
            seen_progress = set()
            seen_completed = set()
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
                    if (key[0] in assigned and '/' not in key[0]
                            and key[1] in ('pass', 'skip', 'fail')
                            and key[0] not in seen_completed):
                        seen_completed.add(key[0])
                        if progress is not None:
                            progress.record_test(f'{package} {key[0]} {key[1]}')
                if progress is not None:
                    progress.maybe_publish()
                if process.poll() is not None:
                    break
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    # Kill test2json, the test binary, and its ordinary descendants together.
                    kill_group(process.pid)
                    process.wait()
                    timed_out = True
                    break
                wait_timeout = min(1.0, remaining)
                if progress is not None:
                    wait_timeout = min(wait_timeout, progress.interval)
                try:
                    process.wait(timeout=max(0.0, wait_timeout))
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
                         f'not completed: {remaining}\n{stderr}', events, timed_out=True)
    if process.returncode or any(e.get('Action') == 'fail' for e in events):
        raw_failed = list(dict.fromkeys(
            e['Test'] for e in events if e.get('Action') == 'fail' and e.get('Test')
        ))
        failed_tests = [
            name for name in raw_failed
            if not any(other != name and other.startswith(name + '/') for other in raw_failed)
        ]
        output = ''.join(e.get('Output', '') for e in events)
        # A timed-out test may never emit a case-level FAIL. Keep its panic and
        # stacks even when an unrelated test in the shard did emit one.
        if raw_failed and not re.search(r'^panic: test timed out after ', output, re.MULTILINE):
            def belongs_to_failure(test_name):
                if not test_name:
                    return True
                return any(
                    test_name == failed or test_name.startswith(failed + '/') or failed.startswith(test_name + '/')
                    for failed in raw_failed
                )
            output = ''.join(e.get('Output', '') for e in events if belongs_to_failure(e.get('Test')))
        headline = f'{label} failed (exit {process.returncode})'
        if failed_tests:
            headline += f": {', '.join(failed_tests)}"
        detail = f'{output}{stderr}'.rstrip('\n')
        raise ShardError(f'{headline}\n{detail}' if detail else headline, events)
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


def _write_evidence_safe(evidence_out, payload):
    if not evidence_out:
        return
    try:
        Path(evidence_out).write_text(json.dumps(payload))
    except OSError as error:
        print(f'go-test-shards: cannot write package evidence: {error}', file=sys.stderr)


def _classify_group_cache(events):
    pkg_outputs = [
        e.get('Output', '')
        for e in events
        if not e.get('Test') and e.get('Action') == 'output' and isinstance(e.get('Output'), str)
    ]
    pkg_terminal = [
        e.get('Action')
        for e in events
        if not e.get('Test') and e.get('Action') in ('pass', 'fail', 'skip')
    ]
    if any('(cached)' in chunk for chunk in pkg_outputs):
        return 'hit'
    if any(
        re.search(r'^(?:ok|FAIL|\?)\s', chunk, re.MULTILINE) or '[no tests to run]' in chunk
        for chunk in pkg_outputs
    ) or bool(pkg_terminal):
        return 'miss'
    return 'unknown'


def run_cached_group(package, cwd, names, index, scratch_key, durable, timeout,
                     base_env, git_values, *, explicit_scratch=False, edge_digest=None,
                     run_pattern=None, wall_timeout=DEFAULT_SHARD_TIMEOUT, progress=None):
    label = f'{package} shard {index}'
    go_affected = _load_go_affected()
    command = ['go', 'test', '-json', '-timeout', timeout, '-p', '1', '-parallel', '1']
    if run_pattern is not None:
        command.extend(['-run', run_pattern])
    command.append(package)

    try:
        with go_affected.acquire_invocation_scratch(durable, scratch_key) as (scratch_dir, _pool_dir):
            try:
                env = go_affected._bypass_git_shim_env(base_env, scratch_dir)
                env['GOMAXPROCS'] = '1'
                env['YOLO_DURABLE_DIR'] = str(scratch_dir)
                if explicit_scratch:
                    env['TMPDIR'] = str(scratch_dir)
                gitconfig = go_affected._write_private_gitconfig(scratch_dir, git_values, env=env)
                env['GIT_CONFIG_GLOBAL'] = str(gitconfig)
                if edge_digest is not None:
                    env['TILLR_GATE_EDGE_INPUTS'] = edge_digest
                else:
                    env.pop('TILLR_GATE_EDGE_INPUTS', None)
            except (OSError, subprocess.CalledProcessError) as exc:
                raise ShardError(f'{label} cannot prepare private git config: {exc}') from exc

            with tempfile.NamedTemporaryFile(dir=scratch_dir) as out, \
                    tempfile.NamedTemporaryFile(dir=scratch_dir) as err, \
                    open(out.name, 'rb') as reader, open(err.name, 'rb') as error_reader, \
                    contextlib.ExitStack() as cleanup:
                lifeline_read, lifeline_write = os.pipe()
                try:
                    process = subprocess.Popen(
                        [sys.executable, str(Path(__file__).resolve()), GUARD_FLAG,
                         str(lifeline_read), '--', *command],
                        cwd=cwd, env=env, stdout=out, stderr=err, start_new_session=True,
                        pass_fds=(lifeline_read,))
                except BaseException:
                    os.close(lifeline_write)
                    raise
                finally:
                    os.close(lifeline_read)
                with _live_lock:
                    _live_groups.add(process.pid)

                def end_group():
                    kill_group(process.pid)
                    os.close(lifeline_write)
                    with _live_lock:
                        _live_groups.discard(process.pid)
                    process.wait()

                cleanup.callback(end_group)
                deadline = time.monotonic() + wall_timeout
                pending = b''
                seen_progress = set()
                seen_completed = set()
                assigned = set(names)
                timed_out = False
                while True:
                    size = os.fstat(out.fileno()).st_size
                    lines = (pending + reader.read(max(0, size - reader.tell()))).split(b'\n')
                    pending = lines.pop()
                    for line in lines:
                        try:
                            event = json.loads(line)
                        except ValueError:
                            continue
                        key = (event.get('Test'), event.get('Action'))
                        if ((key[0] in assigned or (not assigned and key[0]))
                                and key[1] in ('run', 'pass', 'skip', 'fail')
                                and key not in seen_progress):
                            seen_progress.add(key)
                            deadline = time.monotonic() + wall_timeout
                        if (key[0] and '/' not in key[0]
                                and key[1] in ('pass', 'skip', 'fail')
                                and key[0] not in seen_completed):
                            seen_completed.add(key[0])
                            if progress is not None:
                                progress.record_test(f'{package} {key[0]} {key[1]}')
                    if progress is not None:
                        progress.maybe_publish()
                    if process.poll() is not None:
                        break
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        kill_group(process.pid)
                        process.wait()
                        timed_out = True
                        break
                    wait_timeout = min(1.0, remaining)
                    if progress is not None:
                        wait_timeout = min(wait_timeout, progress.interval)
                    try:
                        process.wait(timeout=max(0.0, wait_timeout))
                    except subprocess.TimeoutExpired:
                        pass
                reader.seek(0)
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
            break
    if timed_out:
        active = set()
        for event in events:
            name = event.get('Test')
            if name and event.get('Action') == 'run':
                active.add(name)
            elif name and event.get('Action') in ('pass', 'skip', 'fail'):
                active.discard(name)
        pending_names = set(names) - {
            e.get('Test') for e in events if e.get('Action') in ('pass', 'skip', 'fail')
        }
        detail = ', '.join(sorted(active)) or 'none reported'
        remaining = ', '.join(sorted(pending_names)) or 'none'
        raise ShardError(
            f'{label} timed out after {wall_timeout:g}s without top-level test progress; '
            f'running: {detail}; not completed: {remaining}\n{stderr}',
            events,
            timed_out=True,
        )
    if process.returncode or any(e.get('Action') == 'fail' for e in events):
        raw_failed = list(dict.fromkeys(
            e['Test'] for e in events if e.get('Action') == 'fail' and e.get('Test')
        ))
        failed_tests = [
            name for name in raw_failed
            if not any(other != name and other.startswith(name + '/') for other in raw_failed)
        ]
        output = ''.join(e.get('Output', '') for e in events)
        if raw_failed and not re.search(r'^panic: test timed out after ', output, re.MULTILINE):
            def belongs_to_failure(test_name):
                if not test_name:
                    return True
                return any(
                    test_name == failed or test_name.startswith(failed + '/') or failed.startswith(test_name + '/')
                    for failed in raw_failed
                )
            output = ''.join(e.get('Output', '') for e in events if belongs_to_failure(e.get('Test')))
        headline = f'{label} failed (exit {process.returncode})'
        if failed_tests:
            headline += f": {', '.join(failed_tests)}"
        detail = f'{output}{stderr}'.rstrip('\n')
        raise ShardError(f'{headline}\n{detail}' if detail else headline, events)

    pkg_done = any(
        not e.get('Test') and e.get('Action') in ('pass', 'skip')
        for e in events
    )
    if not pkg_done:
        completed = {e.get('Test') for e in events if e.get('Action') in ('pass', 'skip')}
        missing = set(names) - completed
        if missing:
            raise ShardError(f'{label} missing completion: {", ".join(sorted(missing))}', events)
    return events, _classify_group_cache(events)


def _run_cached(module, count, durations, scratch, timeout, *, shard_timeout=DEFAULT_SHARD_TIMEOUT,
                evidence_out=None, progress_file=None,
                progress_interval=DEFAULT_PROGRESS_INTERVAL,
                explicit_scratch=False, durable_dir=None, uncached_events=None):
    _check_cached_worker_refusal(module)
    if progress_file is not None and (not math.isfinite(progress_interval) or progress_interval <= 0):
        raise ValueError('progress interval must be a positive finite number of seconds')
    progress = (_ProgressTracker(progress_file, progress_interval)
                if progress_file is not None else None)
    go_affected = _load_go_affected()
    raw_env_snap = dict(os.environ)
    base_env = go_affected.build_env()
    discovery_error = None
    try:
        stream = subprocess.check_output(['go', 'list', '-json', './...'], cwd=module, env=base_env, text=True)
    except subprocess.CalledProcessError as error:
        if not evidence_out:
            raise
        discovery_error = error
        stream = error.output or ''
    packages = []
    decoder = json.JSONDecoder()
    while stream.strip():
        try:
            package, end = decoder.raw_decode(stream.lstrip())
        except ValueError:
            if discovery_error is None:
                raise
            break
        packages.append(package)
        stream = stream.lstrip()[end:]

    initial_inventory = [
        {'import_path': p['ImportPath'], 'test_cache': (
            'not-started' if (discovery_error or p.get('TestGoFiles') or p.get('XTestGoFiles'))
            else 'not-applicable')}
        for p in packages if isinstance(p, dict) and p.get('ImportPath')
    ]
    if evidence_out:
        if discovery_error:
            _write_evidence_safe(evidence_out, {'discovery': 'partial', 'packages': initial_inventory})
        else:
            _write_evidence_safe(evidence_out, {'mode': 'cached-shards', 'packages': initial_inventory})
    if discovery_error is not None:
        raise discovery_error

    git_values = go_affected._global_git_config(env=raw_env_snap)
    durable = (
        str(durable_dir) if durable_dir is not None
        else (raw_env_snap.get('YOLO_DURABLE_DIR') or raw_env_snap.get('TMPDIR') or str(scratch))
    )

    repo_root = _locate_repo_root(module, fallback_to_script=False, stop_at_temp_roots=True)
    edges = go_affected.load_edges(repo_root) if repo_root is not None else {}
    tested_ips = {
        p['ImportPath'] for p in packages
        if isinstance(p, dict) and p.get('ImportPath') and (p.get('TestGoFiles') or p.get('XTestGoFiles'))
    }
    needs_graph = any(edges.get(ip, {}).get('builds') for ip in tested_ips)
    pkg_info = go_affected.load_tillr_graph(repo_root)['pkg_info'] if (needs_graph and repo_root is not None) else {}
    resolved_builds_cache = {}

    def digest_for(import_path, active_tests):
        edge = edges.get(import_path)
        if edge is None or repo_root is None:
            return None
        if import_path not in resolved_builds_cache:
            resolved_builds_cache[import_path] = go_affected._resolve_edge_builds(repo_root, edge, pkg_info)
        return go_affected.compute_edge_digest(
            repo_root, edge, pkg_info,
            active_tests=active_tests,
            resolved_builds=resolved_builds_cache[import_path],
        )

    jobs = []
    planned_groups = {}
    group_results = {}
    for package in packages:
        if not package.get('TestGoFiles') and not package.get('XTestGoFiles'):
            continue
        ip = package['ImportPath']
        test_files = sorted(set((package.get('TestGoFiles') or []) + (package.get('XTestGoFiles') or [])))
        names = go_affected.scan_package_tests(
            Path(package['Dir']),
            {'import_path': ip, 'dir_rel': '.', 'test_files': test_files},
        )
        if progress is not None:
            progress.record_package(f'discovered {ip}')

        if len(names) < SHARD_THRESHOLD or count <= 1:
            planned_groups[ip] = 1
            run_pattern = '^$' if not names else None
            jobs.append((
                ip, module, names, 0, ip, durable, timeout, base_env, git_values,
                explicit_scratch, digest_for(ip, None), run_pattern,
                shard_timeout, progress,
            ))
        else:
            has_edge_test = 'TestEdgeInputs' in names
            non_edge = [t for t in names if t != 'TestEdgeInputs']
            shard_count = min(MAX_SHARDS, max(count, len(non_edge) // 125))
            buckets = assign(non_edge, durations.get(ip, {}), shard_count)
            pkg_jobs = []
            for shard_idx, bucket in enumerate(buckets):
                if not bucket:
                    continue
                shard_names = list(bucket)
                if has_edge_test:
                    shard_names.append('TestEdgeInputs')
                pattern = '^(' + '|'.join(re.escape(n) for n in shard_names) + ')$'
                pkg_jobs.append((
                    ip, module, shard_names, shard_idx, f'{ip}#shard-{shard_idx}',
                    durable, timeout, base_env, git_values,
                    explicit_scratch, digest_for(ip, bucket), pattern,
                    shard_timeout, progress,
                ))
            planned_groups[ip] = len(pkg_jobs)
            jobs.extend(pkg_jobs)

    events = []
    shard_errors = []
    try:
        with ThreadPoolExecutor(max_workers=max(1, len(jobs))) as pool:
            futures = {}
            for job_index, job in enumerate(jobs):
                (ip, mod_dir, names, shard_idx, scratch_key, dur, to, benv, gv,
                 exp_scr, edigest, rpat, sto, prog) = job
                fut = pool.submit(
                    run_cached_group,
                    ip, mod_dir, names, shard_idx, scratch_key, dur, to, benv, gv,
                    explicit_scratch=exp_scr, edge_digest=edigest, run_pattern=rpat,
                    wall_timeout=sto, progress=prog,
                )
                futures[fut] = (job_index, ip, shard_idx)
            for future in as_completed(futures):
                job_index, ip, shard_idx = futures[future]
                try:
                    group_events, group_cache = future.result()
                except ShardError as exc:
                    events.extend(exc.events)
                    group_results.setdefault(ip, []).append(_classify_group_cache(exc.events))
                    shard_errors.append((job_index, str(exc)))
                    if progress is not None and not exc.timed_out:
                        progress.record_shard(f'{ip} shard {shard_idx}')
                else:
                    events.extend(group_events)
                    if uncached_events is not None and group_cache != 'hit':
                        uncached_events.extend(group_events)
                    group_results.setdefault(ip, []).append(group_cache)
                    if progress is not None:
                        progress.record_shard(f'{ip} shard {shard_idx}')
    finally:
        if evidence_out:
            final_packages = []
            for entry in initial_inventory:
                ip = entry['import_path']
                if entry['test_cache'] == 'not-applicable':
                    final_packages.append(entry)
                    continue
                statuses = group_results.get(ip, [])
                expected = planned_groups.get(ip, 0)
                if expected == 0 or len(statuses) < expected:
                    status = 'not-started'
                elif any(s == 'unknown' for s in statuses):
                    status = 'unknown'
                elif all(s == 'hit' for s in statuses):
                    status = 'hit'
                else:
                    status = 'miss'
                final_packages.append({'import_path': ip, 'test_cache': status})
            _write_evidence_safe(evidence_out, {'mode': 'cached-shards', 'packages': final_packages})

    if shard_errors:
        shard_errors.sort(key=lambda item: item[0])
        raise ShardError('\ngo-test-shards: '.join(text for _, text in shard_errors), events)
    return events


def run(module, count, durations, scratch, timeout, *, shard_timeout=DEFAULT_SHARD_TIMEOUT,
        evidence_out=None, progress_file=None,
        progress_interval=DEFAULT_PROGRESS_INTERVAL, cache=False,
        explicit_scratch=False, durable_dir=None, uncached_events=None):
    if cache:
        return _run_cached(
            module, count, durations, scratch, timeout,
            shard_timeout=shard_timeout, evidence_out=evidence_out,
            progress_file=progress_file, progress_interval=progress_interval,
            explicit_scratch=explicit_scratch, durable_dir=durable_dir,
            uncached_events=uncached_events,
        )
    if progress_file is not None and (not math.isfinite(progress_interval) or progress_interval <= 0):
        raise ValueError('progress interval must be a positive finite number of seconds')
    progress = (_ProgressTracker(progress_file, progress_interval)
                if progress_file is not None else None)
    discovery_error = None
    try:
        stream = subprocess.check_output(['go', 'list', '-json', './...'], cwd=module, text=True)
    except subprocess.CalledProcessError as error:
        if not evidence_out:
            raise
        discovery_error = error
        stream = error.output or ''
    packages = []
    decoder = json.JSONDecoder()
    while stream.strip():
        try:
            package, end = decoder.raw_decode(stream.lstrip())
        except ValueError:
            if discovery_error is None:
                raise
            break  # Retain complete package records preceding a truncated tail.
        packages.append(package)
        stream = stream.lstrip()[end:]
    if evidence_out:
        # Publish the inventory before compilation. On a failed list, preserve
        # its observed prefix without ever submitting those packages as jobs.
        inventory = [
            {'import_path': p['ImportPath'], 'test_cache': (
                'not-started' if discovery_error else
                'bypassed' if p.get('TestGoFiles') or p.get('XTestGoFiles') else 'not-applicable')}
            for p in packages if isinstance(p, dict) and p.get('ImportPath')
        ]
        try:
            Path(evidence_out).write_text(json.dumps(
                {'discovery': 'partial', 'packages': inventory} if discovery_error else inventory))
        except OSError as error:
            print(f'go-test-shards: cannot write package evidence: {error}', file=sys.stderr)
    if discovery_error is not None:
        raise discovery_error
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
        if progress is not None:
            progress.record_package(f"discovered {package['ImportPath']}")
        for index, group in enumerate(assign(names, durations.get(package['ImportPath'], {}), count)):
            # A package with only benchmarks can still fail in TestMain. Run it
            # once even with no runnable names, as the native Go driver does.
            if group or (not names and index == 0):
                jobs.append((package['ImportPath'], Path(package['Dir']), binary, group,
                             index, scratch / f'package-{number}-shard-{index}', timeout, git_values,
                             shard_timeout, progress))
    events = []
    shard_errors = []
    # Each package has at most K jobs. Run packages side by side, just as go test
    # does, rather than making a second large package wait behind the first one's K.
    with ThreadPoolExecutor(max_workers=max(1, len(jobs))) as pool:
        futures = {pool.submit(run_shard, *job): job_index for job_index, job in enumerate(jobs)}
        for future in as_completed(futures):
            try:
                events.extend(future.result())
            except ShardError as exc:
                events.extend(exc.events)
                shard_errors.append((futures[future], str(exc)))
                if progress is not None and not exc.timed_out:
                    job = jobs[futures[future]]
                    progress.record_shard(f'{job[0]} shard {job[4]}')
            else:
                if progress is not None:
                    job = jobs[futures[future]]
                    progress.record_shard(f'{job[0]} shard {job[4]}')
    if shard_errors:
        shard_errors.sort(key=lambda item: item[0])
        raise ShardError('\ngo-test-shards: '.join(text for _, text in shard_errors), events)
    return events


def main():
    if sys.argv[1:2] == [GUARD_FLAG]:
        return guard_main(int(sys.argv[2]), sys.argv[4:])
    install_signal_handlers()
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('module', type=Path, nargs='?')
    parser.add_argument('-k', '--shards', type=int, default=6)
    parser.add_argument('--cache', action='store_true',
                        help='reuse Go test result cache with edge digests and skip sharding below 100 tests')
    parser.add_argument('--edge-digest', metavar='IMPORT_PATH', default=None,
                        help='print the current TILLR_GATE_EDGE_INPUTS digest for IMPORT_PATH and exit 0')
    parser.add_argument('--durations', type=Path, default=DEFAULT_DURATIONS)
    parser.add_argument('--record', type=Path, help='write measured top-level durations after success')
    parser.add_argument('--events', type=Path, help='write Go test JSON events, including failed shards')
    parser.add_argument('--scratch', type=Path, help='parent of private scratch; defaults to TMPDIR or YOLO_DURABLE_DIR')
    parser.add_argument('--timeout', default='0', help='explicit Go test timeout; default disables Go\'s own deadline')
    parser.add_argument('--shard-timeout', type=float, default=None, metavar='SECONDS',
                        help=f'maximum seconds without top-level test progress per shard (default {DEFAULT_SHARD_TIMEOUT:g})')
    parser.add_argument('--progress-file', type=Path, default=None,
                        help=f'write throttled completion progress snapshots as JSON (or ${PROGRESS_OUT_ENV})')
    parser.add_argument('--progress-interval', type=float, default=DEFAULT_PROGRESS_INTERVAL, metavar='SECONDS',
                        help=f'minimum seconds between completion progress snapshots (default {DEFAULT_PROGRESS_INTERVAL:g})')
    args = parser.parse_args()
    if args.edge_digest is not None:
        go_affected = _load_go_affected()
        repo_root = _locate_repo_root(Path.cwd(), fallback_to_script=True)
        edges = go_affected.load_edges(repo_root)
        edge = edges.get(args.edge_digest)
        if edge is not None:
            if edge.get('builds'):
                graph = go_affected.load_tillr_graph(repo_root)
                resolved = go_affected._resolve_edge_builds(repo_root, edge, graph['pkg_info'])
            else:
                resolved = ([], [])
            print(go_affected.compute_edge_digest(
                repo_root, edge, {}, active_tests=None, resolved_builds=resolved,
            ))
        return 0
    if args.module is None:
        parser.error('the following arguments are required: module')
    if args.shards < 1:
        parser.error('shards must be positive')
    if args.shard_timeout is not None and (not math.isfinite(args.shard_timeout) or args.shard_timeout <= 0):
        parser.error('shard timeout must be a positive finite number of seconds')
    if not math.isfinite(args.progress_interval) or args.progress_interval <= 0:
        parser.error('progress interval must be a positive finite number of seconds')
    try:
        durations = load_durations(args.durations)
        parent = args.scratch or os.environ.get('TMPDIR') or os.environ.get('YOLO_DURABLE_DIR')
        if not parent:
            parser.error('set TMPDIR or YOLO_DURABLE_DIR, or pass --scratch (never defaults to /tmp)')
        parent = Path(parent).resolve()
        parent.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='go-shards-', dir=parent) as temporary:
            options = {} if args.shard_timeout is None else {'shard_timeout': args.shard_timeout}
            # Consume this invocation's handoff, rather than exposing it to test binaries.
            if evidence_out := os.environ.pop('TILLR_GO_SHARDS_EVIDENCE_OUT', None):
                options['evidence_out'] = evidence_out
            env_progress_out = os.environ.pop(PROGRESS_OUT_ENV, None)
            progress_file = (
                args.progress_file.resolve() if args.progress_file is not None
                else (Path(env_progress_out.strip()).resolve()
                      if env_progress_out and env_progress_out.strip() else None)
            )
            if progress_file is not None:
                options['progress_file'] = progress_file
            if progress_file is not None or args.progress_interval != DEFAULT_PROGRESS_INTERVAL:
                options['progress_interval'] = args.progress_interval
            uncached_events = [] if args.cache else None
            if args.cache:
                options['cache'] = True
                options['uncached_events'] = uncached_events
                if args.scratch is not None:
                    options['explicit_scratch'] = True
                    options['durable_dir'] = args.scratch.resolve()
            events = run(args.module.resolve(), args.shards, durations, Path(temporary), args.timeout, **options)
        if args.events:
            args.events.write_text(''.join(json.dumps(e) + '\n' for e in events))
        if args.record:
            record_source = uncached_events if args.cache else events
            for event in record_source:
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
