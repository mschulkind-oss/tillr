#!/usr/bin/env python3
"""Build the exact fork head with its pack command, through tillr-gate's verdict face.

The ordinary repository gate reads repository paths, not a fork's paths. This gate
instead declares fork-build in a per-run registry, and never reads a moving patch
branch for its build input. Build output is retained under the run directory.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from urllib.parse import parse_qs, urlsplit


def jsonc(path):
    # Keep quoted strings intact while removing comments and trailing commas.
    tokens = r'"(?:\\.|[^"\\])*"|//[^\n]*|/\*.*?\*/'
    text = re.sub(tokens, lambda m: m[0] if m[0].startswith('"') else '', path.read_text(), flags=re.S)
    text = re.sub(r'"(?:\\.|[^"\\])*"|,\s*(?=[}\]])',
                  lambda m: m[0] if m[0].startswith('"') else '', text)
    return json.loads(text)


def build_command(root, fork):
    matches = []
    for path in sorted((root / 'yolo-packs').rglob('pack.json')):
        for contribution in jsonc(path).get('contributes', []):
            locator = contribution.get('patch_branch', '')
            if parse_qs(urlsplit(locator).query).get('ref') == ['patch/' + fork]:
                matches.append((path, contribution.get('build', '')))
    if len(matches) != 1:
        raise ValueError(f'fork {fork} needs exactly one pack delivery, found {len(matches)}')
    path, command = matches[0]
    if not command:
        # A pack with no build command delivers source only. Still run a real check.
        print(f'fork-build: {path} declares no build command; checking source-only delivery', flush=True)
        command = 'git diff --exit-code HEAD && git diff --cached --exit-code'
    return path, command


def call(argv, cwd):
    result = subprocess.run(argv, cwd=cwd, check=False)
    if result.returncode:
        raise RuntimeError(f'{argv[0]} {argv[1]} exited {result.returncode}')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    for flag in ('item', 'lane', 'run', 'worktree', 'base', 'head', 'root', 'fork'):
        p.add_argument('--' + flag, required=True)
    p.add_argument('--tillr-gate', default='tillr-gate')
    args = p.parse_args()
    root = Path(args.root).resolve()
    directory = root / '.tillr' / 'loop' / args.run
    directory.mkdir(parents=True, exist_ok=True)
    for stale in ('gate.verdict.json', 'fork-cache-source'):
        (directory / stale).unlink(missing_ok=True)
    pack, command = build_command(root, args.fork)
    # Builds can delete tracked tests or generate files. Never run them in the writer's tree.
    candidate = Path(tempfile.mkdtemp(prefix='fork-build-', dir=directory))
    call(['git', 'clone', '--quiet', '--shared', '--no-checkout', args.worktree, str(candidate)], root)
    call(['git', 'checkout', '--quiet', '--detach', args.head], candidate)
    # A fresh shared cache must retain the upstream refs that pi-forks check reads.
    call(['git', 'fetch', '--quiet', '--no-tags', args.worktree,
          '+refs/remotes/upstream/*:refs/remotes/upstream/*', '+refs/tags/*:refs/tags/*'], candidate)
    upstream = subprocess.run(['git', 'remote', 'get-url', 'upstream'], cwd=args.worktree,
                              capture_output=True, text=True)
    if upstream.returncode == 0:
        call(['git', 'remote', 'add', 'upstream', upstream.stdout.strip()], candidate)
    print(f'fork-build: {args.head} with {pack.relative_to(root)}: {command}', flush=True)
    declarations = json.dumps([{
        'id': 'fork-build', 'kind': 'fork-build', 'label': f'{args.fork}: pack build',
        'selects': [{'regex': '.'}], 'watches': [{'paths': True}],
        'command': ['sh', '-c', command], 'timeout_ms': 0,
    }], separators=(',', ':'))
    in_process = '[]'
    digest = hashlib.sha256((declarations + in_process).encode()).hexdigest()
    registry = directory / 'fork.registry.json'
    registry.write_text('{"digest":' + json.dumps(digest) + ',"declarations":' + declarations + ',"in_process":' + in_process + '}\n')
    change, plan, verdict = [directory / name for name in ('gate.change.json', 'gate.plan.json', 'gate.verdict.json')]
    call([args.tillr_gate, 'change', '--item', args.item, '--lane', args.lane, '--run', args.run,
          '--worktree', str(candidate), '--base', args.base, '--head', args.head, '--output', str(change)], candidate)
    call([args.tillr_gate, 'plan', '--registry', str(registry), '--input', str(change),
          '--root', str(candidate), '--output', str(plan)], candidate)
    result = subprocess.run([args.tillr_gate, 'run', '--input', str(plan), '--worktree', str(candidate),
                             '--output', str(verdict), '--logs', str(directory / 'gate.logs')], cwd=candidate)
    if not verdict.exists():
        raise RuntimeError(f'fork build gate produced no verdict (exit {result.returncode})')
    outcome = json.loads(verdict.read_text())['payload']['result']
    if outcome == 'pass':
        # Import the built objects, not a ref: only the lander moves patch/<fork>.
        call(['git', 'fetch', '--quiet', '--no-tags', str(candidate), args.head], root)
        (directory / 'fork-cache-source').write_text(str(candidate) + '\n')
    return {'pass': 0, 'fail': 1, 'error': 4}[outcome]


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print(f'fork-build: {error}', file=sys.stderr)
        sys.exit(4)
