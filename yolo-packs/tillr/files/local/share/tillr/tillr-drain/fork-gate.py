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
import shlex
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


# Mirrors tools/fork-check.py's FORK_QUALITY, extended with pi (tsc at root, vitest per touched package).
FORK_QUALITY = {
    'pi': {'root': 'npx tsc --noEmit', 'package': 'npx vitest --run'},
    'pi-session-search': ('check', 'test'),
    'pi-subagents': ('typecheck', 'test:unit'),
}

# Container/session variables that leak ambient cache retention or provider credentials into unit tests.
QUALITY_UNSET_ENV = (
    'PI_CACHE_RETENTION',
    'AWS_ACCESS_KEY_ID',
    'AWS_SECRET_ACCESS_KEY',
    'AWS_SESSION_TOKEN',
    'AWS_PROFILE',
    'AWS_BEARER_TOKEN_BEDROCK',
    'AWS_CONTAINER_CREDENTIALS_RELATIVE_URI',
    'AWS_CONTAINER_CREDENTIALS_FULL_URI',
    'AWS_WEB_IDENTITY_TOKEN_FILE',
    'OPENAI_API_KEY',
    'ANTHROPIC_API_KEY',
    'ANTHROPIC_OAUTH_TOKEN',
    'ANTHROPIC_AUTH_TOKEN',
    'AZURE_OPENAI_API_KEY',
    'GEMINI_API_KEY',
)


def touched_packages(candidate, base, head):
    result = subprocess.run(
        ['git', 'diff', '--name-only', '-z', base, head],
        cwd=candidate, capture_output=True, text=True, check=False,
    )
    if result.returncode:
        raise RuntimeError(f'git diff exited {result.returncode}')
    packages = set()
    for rel in result.stdout.split('\0'):
        if not rel:
            continue
        parts = Path(rel).parts
        if len(parts) >= 2 and parts[0] == 'packages' and (candidate / 'packages' / parts[1]).is_dir():
            packages.add(f'packages/{parts[1]}')
    return sorted(packages)


def quality_command(fork, candidate, base, head):
    entry = FORK_QUALITY.get(fork)
    if entry is None:
        return None
    if isinstance(entry, dict):
        commands = [entry['root']]
        packages = touched_packages(candidate, base, head)
        if not packages:
            print(f'fork-quality: {fork} touches no packages/ workspace; skipping vitest', flush=True)
        else:
            for pkg in packages:
                commands.append(f'(cd {shlex.quote(pkg)} && {entry["package"]})')
        return ' && '.join(commands)
    # Tuple entries (pi-subagents, pi-session-search) have devDependencies omitted or pruned by pack builds.
    scripts = ' && '.join(f'npm run {shlex.quote(script)}' for script in entry)
    return 'npm ci --include=dev --legacy-peer-deps --ignore-scripts --no-audit --no-fund && ' + scripts


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
    for stale in ('gate.verdict.json', 'fork-cache-source', 'fork-build-passed'):
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
    build_passed = directory / 'fork-build-passed'
    build_cmd = f'({command}) && : > {shlex.quote(str(build_passed))}'
    quality = quality_command(args.fork, candidate, args.base, args.head)
    print(f'fork-build: {args.head} with {pack.relative_to(root)}: {command}', flush=True)
    declaration_list = [{
        'id': 'fork-build', 'kind': 'fork-build', 'label': f'{args.fork}: pack build',
        'selects': [{'regex': '.'}], 'watches': [{'paths': True}],
        'command': ['sh', '-c', build_cmd], 'timeout_ms': 0,
    }]
    if quality:
        print(f'fork-quality: {args.head}: {quality}', flush=True)
        unset_cmd = 'unset ' + ' '.join(QUALITY_UNSET_ENV)
        guarded_quality = (
            f'if [ ! -f {shlex.quote(str(build_passed))} ]; then '
            f'echo "fork-quality: skipped because fork-build did not pass" >&2; exit 1; fi && '
            f'{unset_cmd} && '
            f'{quality}'
        )
        declaration_list.append({
            'id': 'fork-quality', 'kind': 'fork-quality', 'label': f'{args.fork}: quality',
            'selects': [{'regex': '.'}], 'watches': [{'paths': True}],
            'after': ['fork-build'],
            'command': ['sh', '-c', guarded_quality], 'timeout_ms': 0,
        })
    else:
        print(f'fork-quality: no quality entry for {args.fork}; keeping build-only verdict', flush=True)
    declarations = json.dumps(declaration_list, separators=(',', ':'))
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
