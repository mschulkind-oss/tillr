#!/usr/bin/env python3
"""Judge the recorded delivery commit, independent of local branch refs."""
import os
from pathlib import Path
import subprocess
import sys

from fork_input import ensure, MissingInput


def main():
    branch = sys.argv[1]
    name = branch.removeprefix('patch/')
    root = Path(os.environ.get('FORK_INPUT_ROOT', Path.cwd()))
    cache_root = Path(os.environ.get('PI_FORKS_ROOT', root)) / '.forks' / 'inputs'
    cache = Path(os.environ.get('FORK_INPUT_CACHE', cache_root)) / name
    try:
        oid = ensure(root, name, cache)
    except (MissingInput, OSError) as error:
        print(f'patch-ref: could not judge: {error}', file=sys.stderr)
        return 4  # tillr-gate: external input prevented a judgment
    print(f'ok   recorded patch commit {oid} is available')
    if branch == 'patch/pi':
        script = subprocess.run(['git', '-C', str(cache), 'show', oid + ':scripts/fork-build.mjs'],
                                text=True, capture_output=True)
        if script.returncode:
            print('FAIL recorded pi commit lacks scripts/fork-build.mjs', file=sys.stderr)
            return 1
        print('ok   the pi patch commit adds scripts/fork-build.mjs')
        if '"build:offline"' not in script.stdout:
            print('FAIL fork-build.mjs runs upstream\'s build:offline, not the live build', file=sys.stderr)
            return 1
        print('ok   fork-build.mjs runs upstream\'s build:offline, not the live build')
    return 0


if __name__ == '__main__':
    sys.exit(main())
