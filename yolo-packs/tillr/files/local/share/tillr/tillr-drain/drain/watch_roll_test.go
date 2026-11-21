package drain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNamedRollWatchInstallation(t *testing.T) {
	declareSiblingInputs(t)
	if _, err := os.ReadFile("../.sibling-inputs/tillr-roll/tillr-roll.py"); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../tillr-roll/tillr-roll.py")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	writePackFixture(t, root, "revision", "pre-watch\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "pre-watch")
	old := runGit(t, root, "rev-parse", "HEAD")
	writePackFixture(t, root, "tools/tillr-watch/go.mod", "module fixture\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "with watch")
	current := runGit(t, root, "rev-parse", "HEAD")
	t.Setenv("TILLR_ROLL_VERIFICATION", "after-merge")
	cmd := exec.Command("python3", "-B", "-", script, old, current)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(`
import errno
import io
import json
from pathlib import Path
import runpy
import subprocess
import sys
from types import SimpleNamespace

script, old, current = sys.argv[1:]
roll = runpy.run_path(script)['roll']
ns = roll.__globals__
root = Path.cwd()
live = root / 'bin'
rolled = root / 'tillr-roll.commit'
marker = root / 'tillr-watch.retire'
watch = live / 'tillr-watch'
real_run = ns['run']
deferred = fail_build = refuse_restart = False
facts = {}
seq = 0

def run(*args, **kwargs):
    if args[:2] == ('go', 'build'):
        target = Path(args[args.index('-o') + 1])
        if fail_build:
            raise RuntimeError('fixture build failed')
        target.write_text(ns['git']('rev-parse', 'HEAD', cwd=kwargs['cwd'].parents[1]))
    elif args[1:3] == ('loop', '--check'):
        return SimpleNamespace(stdout=ns['SCHEMA_CHECK'] if deferred else '')
    elif len(args) == 2 and args[1] == '--help':
        assert args[0].is_file()
    else:
        return real_run(*args, **kwargs)

def activate(stage, commit):
    assert json.loads((stage / 'roll.json').read_text())['commit'] == commit
    for child in stage.iterdir():
        if child.name != 'roll.json':
            child.replace(live / child.name)
    rolled.write_text(commit + '\n')
    (stage / 'roll.json').unlink()
    stage.rmdir()

def request(args, **kwargs):
    global seq
    role = args[0].name.removeprefix('tillr-')
    if deferred and role == 'drain':
        stage = live / (ns['SCHEMA_STAGE'] + ns['digest'](args[0]))
        activate(stage, args[0].read_text())
    if refuse_restart and (not deferred or role == 'view'):
        return SimpleNamespace(stdout='fixture\nfixture: refused\n', returncode=1)
    seq += 1
    facts[role] = dict(seq=seq, revision=args[0].read_text(), digest=ns['digest'](args[0]))
    return SimpleNamespace(stdout='fixture\nfixture: restarted\n', returncode=0)

ns.update(run=run, builds=lambda store: facts.copy(), record_failure=lambda *args: None,
          test_commit=lambda *args: None)
# The imported subprocess module is shared with real_run; only intercept restarts.
real_subprocess = subprocess.run
subprocess.run = lambda args, **kwargs: request(args, **kwargs) if args[1] == 'restart' else real_subprocess(args, **kwargs)

def install(commit, no_request=True):
    assert roll(root, live, no_request, commit=commit) == 0

def expect_bad_revision():
    try:
        install('bad-requested-revision')
    except subprocess.CalledProcessError as exc:
        assert exc.cmd[:3] == ('git', 'rev-parse', '--verify'), exc
    else:
        raise AssertionError('roll unexpectedly resolved a bad revision')


def expect_failed_build():
    try:
        install(old)
    except ns['RollFailed'] as exc:
        assert 'fixture build failed' in str(exc)
    else:
        raise AssertionError('build unexpectedly succeeded')

install(current)
assert watch.read_text() == current
fail_build = True
expect_failed_build()
assert watch.read_text() == current
fail_build = False
rolled.unlink()
rolled.mkdir()  # Opening the installed commit stamp for writing must fail.
try:
    install(old)
except IsADirectoryError as exc:
    assert Path(exc.filename) == rolled
else:
    raise AssertionError('roll unexpectedly stamped a directory')
assert watch.read_text() == current
rolled.rmdir()
refuse_restart = True
assert roll(root, live, False, commit=old) == 1
assert watch.read_text() == current
refuse_restart = False
install(old)
assert not watch.exists() and (live / 'tillr').read_text() == old

# A successful requested handoff must retire watch itself, not settle a
# marker from an earlier --no-request roll.
install(current)
deferred = True
assert watch.read_text() == current and not marker.exists()
install(old, no_request=False)
assert rolled.read_text().strip() == old
assert not watch.exists() and not marker.exists()
install(current, no_request=False)
assert watch.read_text() == current

refuse_restart = True
assert roll(root, live, False, commit=old) == 1
expect_bad_revision()
assert watch.read_text() == current and not marker.exists()
refuse_restart = False

# Cleanup failure during settlement of an earlier successful --no-request
# roll must keep the binary and leave the marker eligible for a retry.
install(old)
activate(live / json.loads(marker.read_text())['stage'], old)
real_unlink = Path.unlink
def expect_failed_cleanup(target, error):
    def unlink(path, *args, **kwargs):
        if target(path):
            raise PermissionError(error)
        return real_unlink(path, *args, **kwargs)

    Path.unlink = unlink
    try:
        install(old, no_request=False)
    except PermissionError as exc:
        assert str(exc) == error
    else:
        raise AssertionError('roll unexpectedly completed retirement')
    finally:
        Path.unlink = real_unlink
    assert watch.read_text() == current and marker.exists()

expect_failed_cleanup(lambda path: path == marker, 'fixture marker cleanup failed')
# The next roll tries to settle the previous installation first. Failure to
# delete the recovery copy must restore both the binary and the retirement intent.
expect_failed_cleanup(lambda path: path.name.startswith('.tillr-watch-'), 'fixture backup cleanup failed')
install(old, no_request=False)
assert not watch.exists() and not marker.exists() and rolled.read_text().strip() == old

for schema_roll in (False, True):
    deferred = False
    install(current)
    deferred = schema_roll
    stdout = sys.stdout
    # Neither report line fills this buffer. Only flushing delivers the error.
    blocked = io.TextIOWrapper(io.BufferedWriter(io.FileIO('/dev/full', 'w')))
    sys.stdout = blocked
    try:
        install(old, no_request=not deferred)
    except OSError as exc:
        assert exc.errno == errno.ENOSPC, exc
    else:
        raise AssertionError(f'roll with unflushable stdout succeeded (deferred={deferred})')
    finally:
        sys.stdout = stdout
        try:
            blocked.close()
        except OSError as exc:
            assert exc.errno == errno.ENOSPC, exc
    assert rolled.read_text().strip() == old and watch.read_text() == current
    expect_bad_revision()
    assert watch.read_text() == current and not marker.exists()

deferred = False
install(current)
deferred = True
install(old)
assert watch.read_text() == current
pending = json.loads(marker.read_text())
assert pending['commit'] == old and pending['stage'].startswith(ns['SCHEMA_STAGE'])
stage = live / pending['stage']
fail_build = True
# Even a matching commit stamp is not enough while staging still exists.
rolled.write_text(old + '\n')
expect_failed_build()
assert watch.read_text() == current and marker.exists() and stage.is_dir()
activate(stage, old)
expect_failed_build()
assert not watch.exists() and not marker.exists()
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("named roll fixture: %v\n%s", err, out)
	}
}
