#!/usr/bin/env python3
"""Exercise Go's real result cache against broken sibling mains in an isolated clone.

Run from any directory. --run narrows the development reproduction; omit it for the
acceptance check (literal go test ./..., with no cache-disabling flags).
"""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--run', help='targeted Go test pattern for development only')
args = parser.parse_args()
source = Path(__file__).resolve().parents[2]
scratch = Path(os.environ['YOLO_DURABLE_DIR'])
scratch.mkdir(parents=True, exist_ok=True)
with tempfile.TemporaryDirectory(prefix='sibling-cache-', dir=scratch) as temp:
    repo = Path(temp) / 'repo'
    subprocess.run(['git', 'clone', '--quiet', '--local', str(source), str(repo)], check=True)
    # Overlay the candidate, including new committable files, without touching the source.
    names = subprocess.check_output(['git', 'ls-files', '-z', '--cached', '--others',
                                     '--exclude-standard'], cwd=source).split(b'\0')
    for raw in names:
        if not raw:
            continue
        rel = os.fsdecode(raw)
        src, dst = source / rel, repo / rel
        if src.is_file():
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dst)
    module = repo / 'tools/tillr-drain'
    env = dict(os.environ, YOLO_DURABLE_DIR=str(Path(temp) / 'scratch'))
    command = ['go', 'test', './...']
    if args.run:
        command += ['-run', args.run]

    def check(label, success, cached):
        result = subprocess.run(command, cwd=module, env=env, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        (scratch / ('cache-' + label + '.log')).write_text(result.stdout)
        root_lines = [line for line in result.stdout.splitlines()
                      if line.startswith(('ok ', 'ok\t', 'FAIL\t'))
                      and line.split()[1] == 'github.com/mschulkind-oss/tillr-drain']
        assert root_lines, result.stdout
        assert '[no tests to run]' not in root_lines[-1], result.stdout
        assert (result.returncode == 0) == success, result.stdout
        assert ('(cached)' in root_lines[-1]) == cached, result.stdout
        if not args.run:
            drain_lines = [line for line in result.stdout.splitlines()
                           if line.startswith(('ok ', 'ok\t', 'FAIL\t'))
                           and line.split()[1] == 'github.com/mschulkind-oss/tillr-drain/drain']
            assert drain_lines, result.stdout
            assert ('(cached)' in drain_lines[-1]) == cached, result.stdout
            print(f'{label}: {drain_lines[-1]}', flush=True)
        print(f'{label}: exit {result.returncode}; {root_lines[-1]}', flush=True)

    check('warm', True, False)
    check('unchanged', True, True)
    # Derive the exercised names from the actual sibling calls, not a second input list.
    import re
    siblings = sorted(set(re.findall(r'sibling\(t, "([a-z]+)"\)',
                                     (module / 'pipeline_test.go').read_text())))
    assert len(siblings) == 5, siblings
    for sibling in siblings:
        main = repo / ('tools/tillr-' + sibling) / 'main.go'
        original = main.read_text()
        assert 'func main() {' in original
        try:
            main.write_text(original.replace('func main() {',
                            'func main() { println("broken sibling cache probe"); os.Exit(9);', 1))
            check(sibling, False, False)
        finally:
            main.write_text(original)
    check('restored', True, False)
    check('restored-unchanged', True, True)
print('PASS: all five broken siblings invalidate the root result; unchanged results cache')
