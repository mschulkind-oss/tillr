#!/usr/bin/env python3
"""Build each Go package once; balance independent test processes on recorded durations."""
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import contextlib
from datetime import datetime, timezone
import fcntl
import hashlib
import itertools
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


LOCK_DIR_ENV = 'GO_TEST_SHARDS_LOCK_DIR'
SLOT_CLASS_ENV = 'GO_TEST_SHARDS_CLASS'
HOLDER_ENV = 'GO_TEST_SHARDS_HOLDER'
SLOT_CLASS_ORDINARY = 'ordinary'
SLOT_CLASS_PROOF = 'proof'
SLOT_CLASS_GATE = 'gate'
SLOT_CLASS_REPAIR = 'repair'
SLOT_CLASSES = (SLOT_CLASS_ORDINARY, SLOT_CLASS_PROOF, SLOT_CLASS_GATE, SLOT_CLASS_REPAIR)
_CLASS_PRIORITY = {
    SLOT_CLASS_REPAIR: 0,
    SLOT_CLASS_PROOF: 1,
    SLOT_CLASS_GATE: 2,
    SLOT_CLASS_ORDINARY: 3,
}
_WAIT_TICKET_SEQ = itertools.count()
DEFAULT_GATE_MAX_SLOTS = 8
DEFAULT_GATE_RESERVED_CAP = 4
# One lock pays for one concurrent Go test, not an entire parallel batch.
TESTS_PER_SLOT = 1
# Re-announce long waits periodically so neither logs nor output-based stall watchdogs go silent.
WAIT_REPORT_INTERVAL = 30.0


def default_lock_dir():
    """Return the slot pool every invocation shares unless one is named.

    GO_TEST_SHARDS_LOCK_DIR names another pool. It exists for this runner's own tests: a
    test that runs the runner must not wait on slots the rest of the machine holds.
    """
    named = os.environ.get(LOCK_DIR_ENV, '').strip()
    if named:
        return Path(named).resolve()
    return shared_cache_dir() / 'go-test-shards-locks'


def default_shard_limit():
    """Machine-wide limit on concurrently running ordinary shards, sized from the core count."""
    return max(1, ((os.cpu_count() or 1) * 3) // 8)


def default_gate_shard_limit(ordinary_limit=None):
    """Reserved slots only gate and repair runs may take when ordinary slots are full."""
    limit = max(1, default_shard_limit() if ordinary_limit is None else ordinary_limit)
    spare = max(0, (os.cpu_count() or 1) - limit)
    return min(spare, 6, (limit + 1) // 2)


def default_proof_shard_limit(ordinary_limit=None, gate_limit=None):
    """Reserved slots only proof and repair runs may take when ordinary slots are full."""
    limit = max(1, default_shard_limit() if ordinary_limit is None else ordinary_limit)
    gate = (default_gate_shard_limit(limit) if gate_limit is None
            else max(0, gate_limit))
    spare = max(0, (os.cpu_count() or 1) - limit - gate)
    return min(spare, 4, (limit + 2) // 3)


def resolve_slot_class(slot_class=None):
    chosen = (slot_class or os.environ.get(SLOT_CLASS_ENV, '')).strip() or SLOT_CLASS_ORDINARY
    if chosen not in SLOT_CLASSES:
        raise ValueError(f'invalid slot class {chosen!r}; expected one of {", ".join(SLOT_CLASSES)}')
    return chosen


def resolve_holder_run(holder=None, slot_class=SLOT_CLASS_ORDINARY):
    """Name the run holding a slot so waiting callers see which runs own the pool."""
    if holder is not None and str(holder).strip():
        return str(holder).strip()
    env_holder = os.environ.get(HOLDER_ENV, '').strip()
    if env_holder:
        return env_holder
    run_id = os.environ.get('TILLR_RUN_ID', '').strip()
    if run_id:
        return run_id
    spans = os.environ.get('TILLR_GATE_SPANS_OUT', '').strip()
    if spans:
        parent = Path(spans).parent.name
        if parent == '.rolling':
            return 'trunk-proof'
        if parent:
            return f'gate:{parent}'
    if slot_class == SLOT_CLASS_PROOF:
        return 'trunk-proof'
    if slot_class in (SLOT_CLASS_GATE, SLOT_CLASS_REPAIR):
        return f'gate:pid-{os.getpid()}'
    return f'pid-{os.getpid()}'


def is_trunk_repair_holder(holder_name):
    """Return True when holder_name identifies a revert or trunk-fixer gate run."""
    name = (holder_name or '').strip()
    if name.startswith('gate:'):
        name = name[len('gate:'):]
    return (
        name.startswith('run-revert-')
        or name.startswith('revert-main-')
        or name.startswith('run-fixer-main-')
        or name.startswith('fixer-main-')
    )


def try_lock_slot(lock_dir, prefix, count, offset):
    for step in range(count):
        slot = (offset + step) % count
        slot_name = f'{prefix}-{slot}'
        path = lock_dir / f'{slot_name}.lock'
        candidate = os.open(path, os.O_CREAT | os.O_RDWR, 0o666)
        try:
            fcntl.flock(candidate, fcntl.LOCK_EX | fcntl.LOCK_NB)
            return candidate, slot_name
        except BlockingIOError:
            os.close(candidate)
        except BaseException:
            os.close(candidate)
            raise
    return None, None


def _caller_waiting(lock_dir, lock_name):
    path = lock_dir / lock_name
    try:
        fd = os.open(path, os.O_RDONLY)
    except OSError:
        return False
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        return True
    finally:
        os.close(fd)
    return False


def proof_caller_waiting(lock_dir):
    """Return True when at least one proof-class caller holds a shared lock on proof-waiting."""
    return _caller_waiting(lock_dir, 'proof-waiting.lock')


def gate_caller_waiting(lock_dir):
    """Return True when at least one gate-class caller holds a shared lock on gate-waiting."""
    return _caller_waiting(lock_dir, 'gate-waiting.lock')


def repair_caller_waiting(lock_dir):
    """Return True when at least one repair-class caller holds a shared lock on repair-waiting."""
    return _caller_waiting(lock_dir, 'repair-waiting.lock')


def _wait_queue_dir(lock_dir):
    return lock_dir / 'wait-queue'


def _wait_ticket_name(priority, arrival_ns, pid, tid, seq, pools):
    return f'{priority:02d}-{arrival_ns:020d}-{pid:010d}-{tid:020d}-{seq:08d}-{pools}.wait'


def _parse_wait_ticket(name):
    if not name.endswith('.wait') or name.startswith('.'):
        return None
    parts = name[:-5].split('-')
    if len(parts) != 6:
        return None
    try:
        priority = int(parts[0])
    except ValueError:
        return None
    return priority, parts[5]


def _register_wait_ticket(wait_queue_dir, priority, pools):
    wait_queue_dir.mkdir(parents=True, exist_ok=True)
    monotonic_ns = getattr(time, 'monotonic_ns', None)
    arrival_ns = monotonic_ns() if monotonic_ns is not None else int(time.monotonic() * 1_000_000_000)
    pid = os.getpid()
    tid = threading.get_ident()
    seq = next(_WAIT_TICKET_SEQ)
    name = _wait_ticket_name(priority, arrival_ns, pid, tid, seq, pools)
    ticket_path = wait_queue_dir / name
    tmp_path = wait_queue_dir / f'.{name}.tmp'
    ticket_fd = os.open(tmp_path, os.O_CREAT | os.O_TRUNC | os.O_RDWR, 0o666)
    try:
        fcntl.flock(ticket_fd, fcntl.LOCK_SH)
        os.replace(tmp_path, ticket_path)
        return ticket_fd, ticket_path, name, (priority, arrival_ns, pid, tid, seq)
    except BaseException:
        os.close(ticket_fd)
        with contextlib.suppress(OSError):
            tmp_path.unlink()
        raise


def _update_ticket_pools(wait_queue_dir, ticket_path, ticket_meta, new_pools):
    new_name = _wait_ticket_name(*ticket_meta, new_pools)
    new_path = wait_queue_dir / new_name
    os.replace(ticket_path, new_path)
    return new_path, new_name


def _release_wait_ticket(ticket_fd, ticket_path):
    if ticket_path is not None:
        with contextlib.suppress(OSError):
            ticket_path.unlink()
    _unlock_and_close(ticket_fd)


def _waiter_ahead(wait_queue_dir, pool_char, my_priority, my_ticket_name):
    """Return the priority of the first live waiter ahead of this caller for pool_char, or None."""
    try:
        names = sorted(os.listdir(wait_queue_dir))
    except OSError:
        return None
    for name in names:
        parsed = _parse_wait_ticket(name)
        if parsed is None:
            continue
        if my_ticket_name is not None and name >= my_ticket_name:
            break
        priority, pools = parsed
        if my_ticket_name is None and priority > my_priority:
            break
        if pool_char not in pools:
            continue
        path = wait_queue_dir / name
        try:
            fd = os.open(path, os.O_RDONLY)
        except OSError:
            continue
        try:
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                return priority
            with contextlib.suppress(OSError):
                path.unlink()
            fcntl.flock(fd, fcntl.LOCK_UN)
        except OSError:
            pass
        finally:
            os.close(fd)
    return None


def _holder_cap_dir(lock_dir, holder_name):
    slug = re.sub(r'[^A-Za-z0-9_.-]+', '_', holder_name).strip('_')[:48] or 'holder'
    digest = hashlib.sha256(holder_name.encode('utf-8')).hexdigest()[:12]
    path = lock_dir / 'holder-caps' / f'{slug}-{digest}'
    path.mkdir(parents=True, exist_ok=True)
    return path


def _unlock_and_close(fd):
    if fd is None:
        return
    try:
        fcntl.flock(fd, fcntl.LOCK_UN)
    finally:
        os.close(fd)


def write_slot_holder(lock_dir, slot_name, holder_name, slot_class):
    """Publish a live-flock-backed holder record under the caller's exclusive slot lock."""
    tmp_path = lock_dir / f'.{slot_name}.{os.getpid()}.{threading.get_ident()}.tmp'
    meta_path = lock_dir / f'{slot_name}.holder.json'
    payload = json.dumps({
        'slot': slot_name,
        'class': slot_class,
        'run': holder_name,
        'pid': os.getpid(),
    }).encode('utf-8')
    meta_fd = os.open(tmp_path, os.O_CREAT | os.O_TRUNC | os.O_RDWR, 0o666)
    try:
        os.write(meta_fd, payload)
        fcntl.flock(meta_fd, fcntl.LOCK_SH)
        os.replace(tmp_path, meta_path)
        return meta_fd, meta_path
    except BaseException:
        os.close(meta_fd)
        with contextlib.suppress(OSError):
            tmp_path.unlink()
        raise


def read_slot_holders(lock_dir):
    """Read metadata for every currently live slot holder without touching slot-*.lock."""
    holders = []
    try:
        paths = sorted(lock_dir.glob('*.holder.json'))
    except OSError:
        return holders
    for path in paths:
        try:
            fd = os.open(path, os.O_RDONLY)
        except OSError:
            continue
        try:
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raw = os.read(fd, 65536)
                info = json.loads(raw.decode('utf-8', errors='replace'))
                if isinstance(info, dict):
                    holders.append(info)
        except (OSError, ValueError):
            pass
        finally:
            os.close(fd)
    return holders


def describe_slot_holders(holders, fallback_count=0):
    counts = {}
    for info in holders:
        run_name = str(info.get('run') or 'unknown')
        counts[run_name] = counts.get(run_name, 0) + 1
    total = sum(counts.values())
    if total == 0:
        count = max(1, fallback_count)
        return f'held by {count} active runner(s)'
    ordered = sorted(counts.items(), key=lambda item: (-item[1], item[0]))
    runs = ', '.join(f'{name} ({count})' for name, count in ordered)
    return f'held by {total} holder(s) across {len(ordered)} run(s): {runs}'


def _iso_utc_ms(epoch_ms):
    return datetime.fromtimestamp(epoch_ms / 1000.0, tz=timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.%f')[:-3] + 'Z'


def record_slot_wait(*, start_wall_ms, waited_ms, slot, slot_class, holder_name, label, acquired):
    """Fail-open append of one `slot.wait` JSON line to `$TILLR_TRACE_DIR/slot-waits.jsonl`."""
    trace_dir = os.environ.get('TILLR_TRACE_DIR', '').strip()
    if not trace_dir:
        return
    try:
        if not os.path.isdir(trace_dir):
            return
        waited = max(0, int(waited_ms))
        ts_start_ms = int(start_wall_ms)
        ts_end_ms = ts_start_ms + waited
        row = {
            'kind': 'slot.wait',
            'ts': _iso_utc_ms(ts_start_ms),
            'ts_end': _iso_utc_ms(ts_end_ms),
            'ts_start_ms': ts_start_ms,
            'ts_end_ms': ts_end_ms,
            'waited_ms': waited,
            'slot': slot,
            'class': slot_class,
            'run': holder_name,
            'acquired': bool(acquired),
        }
        if label is not None:
            row['label'] = str(label)
        payload = (json.dumps(row, separators=(',', ':')) + '\n').encode('utf-8')
        out_path = os.path.join(trace_dir, 'slot-waits.jsonl')
        out_fd = os.open(out_path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o644)
        try:
            os.write(out_fd, payload)
        finally:
            os.close(out_fd)
    except OSError:
        pass


@contextlib.contextmanager
def acquire_shard_slot(lock_dir, limit, notify=None, *, slot_class=None,
                       proof_limit=None, gate_limit=None, max_slots=None,
                       gate_reserved_limit=None, holder=None, label=None):
    """Acquire one slot from a counted flock pool under lock_dir; wait without timeout.

    Priority order is `repair > proof > gate > ordinary`, and waiters within the same
    priority class are served in the order their wait began (FIFO):
    - `ordinary` callers take only `slot-0..limit-1` and yield while any higher-priority
      caller or earlier ordinary waiter is ahead of them.
    - `gate` callers cap their simultaneous slots per gate (`max_slots`, default 8) and their
      simultaneous reserved `gate-*` locks (`gate_reserved_limit`, default 4), try `slot-*`
      then `gate-*` (never `proof-*`), yield `slot-*` to `repair`, `proof`, and earlier `gate`
      waiters, and hold a shared lock on `gate-waiting.lock` while waiting.
    - `proof` callers try `slot-*` then `proof-0..`, yield to `repair` and earlier `proof`
      waiters, and hold a shared lock on `proof-waiting.lock` while waiting.
    - `repair` callers (revert and trunk-fixer gates) take `slot-*`, `proof-*`, or `gate-*`
      ahead of proof, gate, and ordinary callers, with FIFO order among simultaneous trunk
      repairs.
    The wait is outside every deadline the runner applies, so it cannot fail the work.
    """
    limit = max(1, limit)
    chosen_class = resolve_slot_class(slot_class)
    holder_name = resolve_holder_run(holder, chosen_class)
    if chosen_class != SLOT_CLASS_ORDINARY:
        env_class = os.environ.get(SLOT_CLASS_ENV, '').strip()
        if env_class == SLOT_CLASS_REPAIR or is_trunk_repair_holder(holder_name):
            chosen_class = SLOT_CLASS_REPAIR
    reserved_gate = (max(0, int(gate_limit)) if gate_limit is not None
                     else default_gate_shard_limit(limit))
    reserved_proof = (max(0, int(proof_limit)) if proof_limit is not None
                      else default_proof_shard_limit(limit, reserved_gate))
    effective_max_slots = (max(1, int(max_slots)) if max_slots is not None
                           else (DEFAULT_GATE_MAX_SLOTS if chosen_class == SLOT_CLASS_GATE else None))
    effective_gate_reserved = (
        (max(0, int(gate_reserved_limit)) if gate_reserved_limit is not None
         else DEFAULT_GATE_RESERVED_CAP)
        if chosen_class == SLOT_CLASS_GATE else 0
    )
    my_priority = _CLASS_PRIORITY[chosen_class]
    lock_dir.mkdir(parents=True, exist_ok=True)
    wait_queue_dir = _wait_queue_dir(lock_dir)
    cap_dir = (_holder_cap_dir(lock_dir, holder_name)
               if (effective_max_slots is not None or effective_gate_reserved > 0)
               else None)
    total_cap_fd = None
    gate_cap_fd = None
    fd = None
    slot_name = None
    wait_fd = None
    ticket_fd = None
    ticket_path = None
    ticket_name = None
    ticket_meta = None
    ticket_pools = None
    wait_started = None
    next_report_at = None
    start_wall_ms = int(time.time() * 1000)
    acquire_started = time.monotonic()
    span_recorded = False
    offset = os.getpid() + threading.get_ident()
    try:
        while total_cap_fd is None and effective_max_slots is not None:
            total_cap_fd, _ = try_lock_slot(cap_dir, 'total', effective_max_slots, offset)
            if total_cap_fd is not None:
                break
            now = time.monotonic()
            if wait_started is None:
                wait_started = now
                next_report_at = now
            if notify is not None and now >= next_report_at:
                elapsed = int(round(now - wait_started))
                after = f' after {elapsed}s' if elapsed > 0 else ''
                notify(f'waiting for a test slot{after}: per-run cap ({effective_max_slots} slots) '
                       f'reached for {holder_name} in {lock_dir} '
                       '(not a failure; no timeout applies while waiting)')
                next_report_at = time.monotonic() + WAIT_REPORT_INTERVAL
            time.sleep(0.02)

        while fd is None:
            repair_waiting = (chosen_class != SLOT_CLASS_REPAIR and repair_caller_waiting(lock_dir))
            proof_waiting = (chosen_class in (SLOT_CLASS_ORDINARY, SLOT_CLASS_GATE)
                             and proof_caller_waiting(lock_dir))
            gate_waiting = (chosen_class == SLOT_CLASS_ORDINARY and gate_caller_waiting(lock_dir))
            ahead_s = _waiter_ahead(wait_queue_dir, 's', my_priority, ticket_name)
            yielding_s = repair_waiting or proof_waiting or gate_waiting or ahead_s is not None
            if not yielding_s:
                fd, slot_name = try_lock_slot(lock_dir, 'slot', limit, offset)
                if fd is not None:
                    preempted_s = (
                        (chosen_class != SLOT_CLASS_REPAIR and repair_caller_waiting(lock_dir))
                        or (chosen_class in (SLOT_CLASS_ORDINARY, SLOT_CLASS_GATE)
                            and proof_caller_waiting(lock_dir))
                        or (chosen_class == SLOT_CLASS_ORDINARY and gate_caller_waiting(lock_dir))
                        or _waiter_ahead(wait_queue_dir, 's', my_priority, ticket_name) is not None
                    )
                    if preempted_s:
                        _unlock_and_close(fd)
                        fd = None
                        slot_name = None
                        repair_waiting = (chosen_class != SLOT_CLASS_REPAIR
                                          and repair_caller_waiting(lock_dir))
                        proof_waiting = (chosen_class in (SLOT_CLASS_ORDINARY, SLOT_CLASS_GATE)
                                         and proof_caller_waiting(lock_dir))
                        gate_waiting = (chosen_class == SLOT_CLASS_ORDINARY
                                        and gate_caller_waiting(lock_dir))
                        ahead_s = _waiter_ahead(wait_queue_dir, 's', my_priority, ticket_name)
                        yielding_s = repair_waiting or proof_waiting or gate_waiting or ahead_s is not None

            if (fd is None and chosen_class in (SLOT_CLASS_PROOF, SLOT_CLASS_REPAIR)
                    and reserved_proof > 0):
                ahead_p = _waiter_ahead(wait_queue_dir, 'p', my_priority, ticket_name)
                if not (repair_waiting or ahead_p is not None):
                    fd, slot_name = try_lock_slot(lock_dir, 'proof', reserved_proof, offset)
                    if fd is not None:
                        preempted_p = (
                            (chosen_class != SLOT_CLASS_REPAIR and repair_caller_waiting(lock_dir))
                            or _waiter_ahead(wait_queue_dir, 'p', my_priority, ticket_name) is not None
                        )
                        if preempted_p:
                            _unlock_and_close(fd)
                            fd = None
                            slot_name = None

            gate_reserved_capped = False
            if fd is None and reserved_gate > 0:
                if chosen_class == SLOT_CLASS_REPAIR:
                    if _waiter_ahead(wait_queue_dir, 'g', my_priority, ticket_name) is None:
                        fd, slot_name = try_lock_slot(lock_dir, 'gate', reserved_gate, offset)
                        if (fd is not None
                                and _waiter_ahead(wait_queue_dir, 'g', my_priority, ticket_name) is not None):
                            _unlock_and_close(fd)
                            fd = None
                            slot_name = None
                elif chosen_class == SLOT_CLASS_GATE and effective_gate_reserved > 0:
                    ahead_g = _waiter_ahead(wait_queue_dir, 'g', my_priority, ticket_name)
                    if not (repair_waiting or ahead_g is not None):
                        gate_cap_fd, _ = try_lock_slot(
                            cap_dir, 'gate-reserved', effective_gate_reserved, offset,
                        )
                        if gate_cap_fd is None:
                            gate_reserved_capped = True
                        else:
                            try:
                                fd, slot_name = try_lock_slot(lock_dir, 'gate', reserved_gate, offset)
                                if fd is not None and (
                                    repair_caller_waiting(lock_dir)
                                    or _waiter_ahead(wait_queue_dir, 'g', my_priority, ticket_name) is not None
                                ):
                                    _unlock_and_close(fd)
                                    fd = None
                                    slot_name = None
                            except BaseException:
                                _unlock_and_close(gate_cap_fd)
                                gate_cap_fd = None
                                raise
                            if fd is None:
                                _unlock_and_close(gate_cap_fd)
                                gate_cap_fd = None
            if fd is not None:
                break

            desired_pools = 's'
            if chosen_class in (SLOT_CLASS_PROOF, SLOT_CLASS_REPAIR) and reserved_proof > 0:
                desired_pools += 'p'
            if reserved_gate > 0 and (
                chosen_class == SLOT_CLASS_REPAIR
                or (chosen_class == SLOT_CLASS_GATE and effective_gate_reserved > 0 and not gate_reserved_capped)
            ):
                desired_pools += 'g'

            if ticket_fd is None:
                ticket_fd, ticket_path, ticket_name, ticket_meta = _register_wait_ticket(
                    wait_queue_dir, my_priority, desired_pools,
                )
                ticket_pools = desired_pools
            elif desired_pools != ticket_pools:
                ticket_path, ticket_name = _update_ticket_pools(
                    wait_queue_dir, ticket_path, ticket_meta, desired_pools,
                )
                ticket_pools = desired_pools

            if chosen_class in (SLOT_CLASS_REPAIR, SLOT_CLASS_GATE, SLOT_CLASS_PROOF) and wait_fd is None:
                wait_lock_name = (
                    'repair-waiting.lock' if chosen_class == SLOT_CLASS_REPAIR
                    else 'gate-waiting.lock' if chosen_class == SLOT_CLASS_GATE
                    else 'proof-waiting.lock'
                )
                wait_fd = os.open(lock_dir / wait_lock_name, os.O_CREAT | os.O_RDWR, 0o666)
                try:
                    fcntl.flock(wait_fd, fcntl.LOCK_SH)
                except BaseException:
                    os.close(wait_fd)
                    wait_fd = None
                    raise
            now = time.monotonic()
            if wait_started is None:
                wait_started = now
                next_report_at = now
            if notify is not None and now >= next_report_at:
                holders = read_slot_holders(lock_dir)
                expected = 1 if yielding_s else limit
                if len(holders) < expected:
                    time.sleep(0.005)
                    holders = read_slot_holders(lock_dir)
                elapsed = int(round(now - wait_started))
                after = f' after {elapsed}s' if elapsed > 0 else ''
                if chosen_class == SLOT_CLASS_REPAIR:
                    pool_desc = (
                        f'all {limit} ordinary, {reserved_proof} proof, and {reserved_gate} gate slots '
                        f'in {lock_dir}'
                    )
                elif chosen_class == SLOT_CLASS_GATE:
                    pool_desc = (
                        f'all {limit} ordinary and eligible gate slots '
                        f'({min(effective_gate_reserved, reserved_gate)}/{reserved_gate} per-gate cap) '
                        f'in {lock_dir}'
                    )
                elif chosen_class == SLOT_CLASS_PROOF:
                    pool_desc = f'all {limit} ordinary and {reserved_proof} proof slots in {lock_dir}'
                else:
                    pool_desc = f'ordinary share ({limit} slots) in {lock_dir}'
                if repair_waiting or (ahead_s == _CLASS_PRIORITY[SLOT_CLASS_REPAIR]
                                      and chosen_class != SLOT_CLASS_REPAIR):
                    why = 'yielding to a waiting trunk-repair run; '
                elif proof_waiting or (ahead_s == _CLASS_PRIORITY[SLOT_CLASS_PROOF]
                                       and chosen_class in (SLOT_CLASS_GATE, SLOT_CLASS_ORDINARY)):
                    why = 'yielding to a waiting proof run; '
                elif gate_waiting or (ahead_s == _CLASS_PRIORITY[SLOT_CLASS_GATE]
                                      and chosen_class == SLOT_CLASS_ORDINARY):
                    why = 'yielding to a waiting gate run; '
                elif ahead_s is not None:
                    why = 'waiting behind an earlier waiter; '
                else:
                    why = ''
                detail = describe_slot_holders(holders, fallback_count=limit)
                notify(f'waiting for a test slot{after}: {pool_desc} busy ({why}{detail}; '
                       'not a failure; no timeout applies while waiting)')
                next_report_at = time.monotonic() + WAIT_REPORT_INTERVAL
            time.sleep(0.02)
        if ticket_fd is not None:
            _release_wait_ticket(ticket_fd, ticket_path)
            ticket_fd = None
            ticket_path = None
        if wait_fd is not None:
            _unlock_and_close(wait_fd)
            wait_fd = None
        waited_ms = (0 if wait_started is None
                     else max(1, int(round((time.monotonic() - acquire_started) * 1000))))
        record_slot_wait(
            start_wall_ms=start_wall_ms, waited_ms=waited_ms, slot=slot_name,
            slot_class=chosen_class, holder_name=holder_name, label=label, acquired=True,
        )
        span_recorded = True
        meta_fd, meta_path = write_slot_holder(lock_dir, slot_name, holder_name, chosen_class)
    except BaseException:
        if not span_recorded:
            waited_ms = (0 if wait_started is None
                         else max(0, int(round((time.monotonic() - acquire_started) * 1000))))
            record_slot_wait(
                start_wall_ms=start_wall_ms, waited_ms=waited_ms, slot=None,
                slot_class=chosen_class, holder_name=holder_name, label=label, acquired=False,
            )
            span_recorded = True
        _release_wait_ticket(ticket_fd, ticket_path)
        _unlock_and_close(wait_fd)
        _unlock_and_close(fd)
        _unlock_and_close(gate_cap_fd)
        _unlock_and_close(total_cap_fd)
        raise
    try:
        yield
    finally:
        try:
            with contextlib.suppress(OSError):
                meta_path.unlink()
            os.close(meta_fd)
        finally:
            try:
                _unlock_and_close(fd)
            finally:
                try:
                    _unlock_and_close(gate_cap_fd)
                finally:
                    _unlock_and_close(total_cap_fd)


def run_shard(package, cwd, binary, names, index, scratch, timeout, git_values=None,
              wall_timeout=DEFAULT_SHARD_TIMEOUT, lock_dir=None, shard_limit=None,
              slot_class=None, proof_limit=None, gate_limit=None, max_slots=None,
              gate_reserved_limit=None, holder=None, progress=None):
    label = f'{package} shard {index}'
    env = private_environment(label, scratch, git_values)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise ShardError(f'{label} cannot start: test binary is missing or not executable: {binary}')
    # The lock is held for the whole binary lifetime. Neither the Go timeout nor
    # the progress watchdog starts until this shard has its own test capacity.
    effective_lock_dir = lock_dir if lock_dir is not None else default_lock_dir()
    effective_limit = shard_limit if shard_limit is not None else default_shard_limit()
    pattern = '^(' + '|'.join(re.escape(n) for n in names) + ')$'
    command = ['go', 'tool', 'test2json', '-t', '-p', package, str(binary),
               '-test.v=test2json', '-test.count=1', f'-test.parallel={TESTS_PER_SLOT}',
               '-test.timeout=' + timeout, '-test.run=' + pattern]
    try:
        # Files rather than pipes let us recover even if an escaped descendant keeps
        # its inherited output descriptors open after the shard itself is killed.
        with acquire_shard_slot(effective_lock_dir, effective_limit,
                                lambda message: print(f'go-test-shards: {label} {message}',
                                                      file=sys.stderr, flush=True),
                                slot_class=slot_class, proof_limit=proof_limit,
                                gate_limit=gate_limit, max_slots=max_slots,
                                gate_reserved_limit=gate_reserved_limit, holder=holder,
                                label=label), \
                tempfile.NamedTemporaryFile(dir=scratch) as out, tempfile.NamedTemporaryFile(dir=scratch) as err, \
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


def run(module, count, durations, scratch, timeout, *, shard_timeout=DEFAULT_SHARD_TIMEOUT,
        lock_dir=None, slot_class=None, proof_limit=None, gate_limit=None,
        max_slots=None, gate_reserved_limit=None, holder=None, evidence_out=None,
        progress_file=None, progress_interval=DEFAULT_PROGRESS_INTERVAL):
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
    lock_dir = lock_dir if lock_dir is not None else default_lock_dir()
    shard_limit = default_shard_limit()
    resolved_class = resolve_slot_class(slot_class)
    resolved_holder = resolve_holder_run(holder, resolved_class)
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
                             shard_timeout, lock_dir, shard_limit, resolved_class,
                             proof_limit, gate_limit, max_slots, gate_reserved_limit,
                             resolved_holder, progress))
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
    parser = argparse.ArgumentParser(
        description=__doc__,
        epilog='Each held slot permits one concurrent Go test. On 32 logical CPUs, '
               '12 ordinary, 4 proof, and 6 gate slots permit at most 22 concurrent tests. '
               'Waiting messages report live holders by run. Use the same shared pool '
               'for all runs; direct go test does not participate.',
    )
    parser.add_argument('module', type=Path)
    parser.add_argument('-k', '--shards', type=int, default=6)
    parser.add_argument('--durations', type=Path, default=DEFAULT_DURATIONS)
    parser.add_argument('--record', type=Path, help='write measured top-level durations after success')
    parser.add_argument('--events', type=Path, help='write Go test JSON events, including failed shards')
    parser.add_argument('--scratch', type=Path, help='parent of private scratch; defaults to TMPDIR or YOLO_DURABLE_DIR')
    parser.add_argument('--lock-dir', type=Path, help='shared lock directory; defaults to $GO_TEST_SHARDS_LOCK_DIR, then <GOCACHE>/go-test-shards-locks')
    parser.add_argument('--slot-class', choices=SLOT_CLASSES, default=None,
                        help='slot pool class: repair (revert and trunk-fixer gates; preempts all others), '
                             'proof (trunk proof; preempts gate and ordinary), '
                             'gate (landing gates; preempts ordinary), or ordinary (default, or $GO_TEST_SHARDS_CLASS)')
    parser.add_argument('--max-slots', type=int, default=None, metavar='N',
                        help=f'maximum simultaneous slots held by this run (default {DEFAULT_GATE_MAX_SLOTS} for --slot-class gate; unlimited otherwise)')
    parser.add_argument('--holder', default=None,
                        help='run name reported to waiting callers; defaults to $GO_TEST_SHARDS_HOLDER, $TILLR_RUN_ID, or $TILLR_GATE_SPANS_OUT')
    parser.add_argument('--timeout', default='0', help='explicit Go test timeout; default disables Go\'s own deadline')
    parser.add_argument('--shard-timeout', type=float, default=None, metavar='SECONDS',
                        help=f'maximum seconds without top-level test progress per shard (default {DEFAULT_SHARD_TIMEOUT:g})')
    parser.add_argument('--progress-file', type=Path, default=None,
                        help=f'write throttled completion progress snapshots as JSON (or ${PROGRESS_OUT_ENV})')
    parser.add_argument('--progress-interval', type=float, default=DEFAULT_PROGRESS_INTERVAL, metavar='SECONDS',
                        help=f'minimum seconds between completion progress snapshots (default {DEFAULT_PROGRESS_INTERVAL:g})')
    args = parser.parse_args()
    if args.shards < 1:
        parser.error('shards must be positive')
    if args.max_slots is not None and args.max_slots < 1:
        parser.error('max slots must be positive')
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
            if args.lock_dir is not None:
                options['lock_dir'] = args.lock_dir.resolve()
            if args.slot_class is not None:
                options['slot_class'] = args.slot_class
            if args.max_slots is not None:
                options['max_slots'] = args.max_slots
            if args.holder is not None:
                options['holder'] = args.holder
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
