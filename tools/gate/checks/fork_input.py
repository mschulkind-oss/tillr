#!/usr/bin/env python3
"""Read a tree-pinned fork commit, fetching it only when absent from its cache."""
import fcntl
from pathlib import Path
import re
import subprocess
import sys


class MissingInput(Exception):
    pass


def pinned(root: Path, name: str) -> tuple[str, str]:
    if not re.fullmatch(r'[A-Za-z0-9._-]+', name):
        raise MissingInput(f'invalid fork name {name!r}')
    path = root / 'yolo-packs' / 'fork-pins' / name
    try:
        lines = path.read_text().splitlines()
    except OSError as error:
        raise MissingInput(f'cannot read fork pin {path}: {error}') from error
    if (len(lines) != 3 or not re.fullmatch(r'[0-9a-f]{40}', lines[0])
            or not lines[1] or not re.fullmatch(r'[0-9a-f]{40}', lines[2])):
        raise MissingInput(f'invalid fork pin {path}: expected commit, remote and base')
    return lines[0], lines[1]


def ensure(root: Path, name: str, cache: Path) -> str:
    oid, remote = pinned(root, name)
    cache.parent.mkdir(parents=True, exist_ok=True)
    # Multiple checks may fill the same initially empty cache at once. Keep the
    # lock outside the repository so it also protects its first initialization.
    with (cache.parent / ('.' + cache.name + '.lock')).open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if not (cache / 'HEAD').exists() and not (cache / '.git').exists():
            cache.mkdir(parents=True, exist_ok=True)
            init = subprocess.run(['git', 'init', '-q', str(cache)], capture_output=True, text=True)
            if init.returncode:
                raise MissingInput(f'cannot initialize cache {cache}: {init.stderr.strip()}')
        def has_commit():
            return subprocess.run(['git', '-C', str(cache), 'cat-file', '-e', oid + '^{commit}'],
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
        if not has_commit():
            result = subprocess.run(['git', '-C', str(cache), 'fetch', '--no-tags', remote, oid],
                                    capture_output=True, text=True)
            if result.returncode or not has_commit():
                raise MissingInput(f'cannot fetch commit {oid} from {remote} into {cache}: {result.stderr.strip()}')
        # FETCH_HEAD is overwritten by the next fetch; retain each immutable input
        # across Git maintenance and across worktrees using different pins.
        result = subprocess.run(['git', '-C', str(cache), 'update-ref', f'refs/fork-inputs/{oid}', oid],
                                capture_output=True, text=True)
        if result.returncode:
            raise MissingInput(f'cannot retain commit {oid} in {cache}: {result.stderr.strip()}')
        return oid


def main():
    try:
        root, name, cache = sys.argv[1:]
        print(ensure(Path(root), name, Path(cache)))
        return 0
    except (MissingInput, OSError, ValueError) as error:
        print(f'fork input: could not judge: {error}', file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.exit(main())
