#!/usr/bin/env python3
"""Build the exact fork head with its pack command, through tillr-gate's verdict face.

The ordinary repository gate reads repository paths, not a fork's paths. This gate
instead declares fork-build in a per-run registry, and never reads a moving patch
branch for its build input. Build output is retained under the run directory.
"""
import argparse
import hashlib
import json
import os
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


# Mirrors tools/fork-check.py's FORK_QUALITY, extended with pi (tsc at root, npm test per touched package).
FORK_QUALITY = {
    'pi': {'root': 'npx tsc --noEmit'},
    'pi-session-search': ('check', 'test'),
    'pi-subagents': ('typecheck', 'test:unit'),
}

# Keep tool lookup, locale, scratch, certificate trust and explicit npm cache
# settings. Do not inherit provider variables or config paths (including XDG
# and PI_CODING_AGENT_DIR); a list of known credential names goes stale.
QUALITY_TOOL_ENV = (
    'PATH', 'LANG', 'LC_ALL', 'LC_CTYPE', 'TMPDIR',
    'SSL_CERT_FILE', 'SSL_CERT_DIR', 'NODE_EXTRA_CA_CERTS',
    'NPM_CONFIG_CACHE', 'npm_config_cache', 'NPM_CONFIG_OFFLINE', 'npm_config_offline',
)


def run_quality(directory, command):
    with tempfile.TemporaryDirectory(prefix='fork-quality-home-', dir=directory) as home:
        env = {name: os.environ[name] for name in QUALITY_TOOL_ENV if name in os.environ}
        env.update(HOME=home, NPM_CONFIG_GLOBALCONFIG=os.devnull)
        result = subprocess.run(['sh', '-c', command], env=env, check=False)
        return result.returncode if result.returncode >= 0 else 4


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


def package_test(package):
    script = None
    try:
        manifest = json.loads((Path(package) / 'package.json').read_text())
        scripts = manifest.get('scripts') if isinstance(manifest, dict) else None
        script = scripts.get('test') if isinstance(scripts, dict) else None
        if not isinstance(script, str) or not script.strip():
            raise ValueError('no scripts.test declared')
    except FileNotFoundError:
        print(f'fork-quality: {package}: no scripts.test declared (missing package.json)', file=sys.stderr)
        return 4
    except (OSError, ValueError) as error:
        print(f'fork-quality: {package}: cannot read scripts.test: {error}', file=sys.stderr)
        return 4

    try:
        # npm exec/npx can fail to resolve an offline runner with status 1,
        # the same status as a failed test. Keep its diagnostics in the gate log.
        offline_cache_miss = False
        with subprocess.Popen(['npm', 'test'], cwd=package, stderr=subprocess.PIPE) as npm:
            for line in npm.stderr:
                sys.stderr.buffer.write(line)
                sys.stderr.buffer.flush()
                if line.strip() in (b'npm error code ENOTCACHED', b'npm ERR! code ENOTCACHED'):
                    offline_cache_miss = True
            status = npm.wait()
        if status and offline_cache_miss:
            raise RuntimeError('npm could not resolve the runner offline (ENOTCACHED)')
        if status in (126, 127) or status < 0:
            raise RuntimeError(f'npm test exited {status}')
    except (OSError, RuntimeError) as error:
        print(f'fork-quality: {package}: unable to run scripts.test {script!r}: {error}', file=sys.stderr)
        return 4
    return int(status != 0)


def quality_command(fork, candidate, base, head):
    entry = FORK_QUALITY.get(fork)
    if entry is None:
        return None
    if isinstance(entry, dict):
        commands = [entry['root']]
        packages = touched_packages(candidate, base, head)
        if not packages:
            print(f'fork-quality: {fork} touches no packages/ workspace; skipping package tests', flush=True)
        else:
            for pkg in packages:
                commands.append(shlex.join([
                    sys.executable, str(Path(__file__).resolve()), '--package-test', pkg,
                ]))
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
        isolated_quality = shlex.join([
            sys.executable, str(Path(__file__).resolve()), '--run-quality', str(directory), quality,
        ])
        guarded_quality = (
            f'if [ ! -f {shlex.quote(str(build_passed))} ]; then '
            f'echo "fork-quality: skipped because fork-build did not pass" >&2; exit 1; fi && '
            f'exec {isolated_quality}'
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
        if len(sys.argv) == 4 and sys.argv[1] == '--run-quality':
            sys.exit(run_quality(sys.argv[2], sys.argv[3]))
        if len(sys.argv) == 3 and sys.argv[1] == '--package-test':
            sys.exit(package_test(sys.argv[2]))
        sys.exit(main())
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print(f'fork-build: {error}', file=sys.stderr)
        sys.exit(4)
