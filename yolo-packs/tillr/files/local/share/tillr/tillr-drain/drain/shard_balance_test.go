package drain

import (
	"os"
	"os/exec"
	"testing"
)

// Newly added tests have no recorded duration. A large fallback must not leave
// five processes idle while a single process executes all of those tests.
func TestShardRunnerBalancesUnrecordedTests(t *testing.T) {
	// The installed pack ships the module but not the repository-only runner.
	if _, err := os.Stat("../../go-test-shards.py"); os.IsNotExist(err) {
		t.Skip("repository-only shard runner is not installed")
	} else if err != nil {
		t.Fatal(err)
	}
	// The Python child is invisible to Go's result cache. Open the runner through
	// the persistent module-local alias used for sibling source inputs.
	declareSiblingInputs(t)
	if _, err := os.ReadFile("../.sibling-inputs/go-test-shards.py"); err != nil {
		t.Fatal(err)
	}
	program := `
import importlib.util
spec = importlib.util.spec_from_file_location('shards', '../../go-test-shards.py')
s = importlib.util.module_from_spec(spec)
spec.loader.exec_module(s)
for known in ([], ['TestKnown%d' % i for i in range(6)]):
    unknown = ['TestNew%d' % i for i in range(120)]
    names = known + unknown
    groups = s.assign(names, {name: 1 for name in known}, 6)
    assert sorted(sum(groups, [])) == sorted(names), groups
    assert all(18 <= len(group) <= 22 for group in groups), [len(group) for group in groups]
`
	if out, err := exec.Command("python3", "-c", program).CombinedOutput(); err != nil {
		t.Fatalf("shard assignment: %v\n%s", err, out)
	}
}

func TestShardRunnerFailureNamesFailingTestsAndOmitsPassingOutput(t *testing.T) {
	if _, err := os.Stat("../../go-test-shards.py"); os.IsNotExist(err) {
		t.Skip("repository-only shard runner is not installed")
	} else if err != nil {
		t.Fatal(err)
	}
	declareSiblingInputs(t)
	if _, err := os.ReadFile("../.sibling-inputs/go-test-shards.py"); err != nil {
		t.Fatal(err)
	}
	scratch := scratchUnderDurable(t)
	program := `
import importlib.util, os, pathlib, sys
spec = importlib.util.spec_from_file_location('shards', '../../go-test-shards.py')
s = importlib.util.module_from_spec(spec)
spec.loader.exec_module(s)

root = pathlib.Path(sys.argv[1])
binary = root / 'fake.test'
binary.write_text(
    "#!/usr/bin/env python3\n"
    "print('=== RUN   TestAlphaFail')\n"
    "print('    alpha_test.go:12: broken invariant')\n"
    "print('--- FAIL: TestAlphaFail (0.01s)')\n"
    "for i in range(30):\n"
    "    print(f'=== RUN   TestPass{i}')\n"
    "    print(f'--- PASS: TestPass{i} (0.01s)')\n"
    "print('FAIL')\n"
    "raise SystemExit(1)\n"
)
binary.chmod(0o755)
names = ['TestAlphaFail'] + [f'TestPass{i}' for i in range(30)]
try:
    s.run_shard('example.com/pkg', root, binary, names, 2, root / 'scratch', '0')
except s.ShardError as exc:
    text = str(exc)
    first_line = text.splitlines()[0]
    assert first_line == 'example.com/pkg shard 2 failed (exit 1): TestAlphaFail', f'bad first line: {first_line!r}'
    assert 'alpha_test.go:12: broken invariant' in text, text
    assert 'TestPass0' not in text and '--- PASS:' not in text, text
    passed = {e.get('Test') for e in exc.events if e.get('Action') == 'pass'}
    assert 'TestPass29' in passed, passed
else:
    raise AssertionError('expected ShardError')
`
	if out, err := exec.Command("python3", "-c", program, scratch).CombinedOutput(); err != nil {
		t.Fatalf("shard failure format: %v\n%s", err, out)
	}
}

func TestRollCleanCheckoutIncludesFullTreeAndRecordsShardFailureDetails(t *testing.T) {
	if _, err := os.Stat("../../go-test-shards.py"); os.IsNotExist(err) {
		t.Skip("repository-only shard runner is not installed")
	} else if err != nil {
		t.Fatal(err)
	}
	declareSiblingInputs(t)
	for _, rel := range []string{"../.sibling-inputs/go-test-shards.py", "../.sibling-inputs/tillr-roll/tillr-roll.py"} {
		if _, err := os.ReadFile(rel); err != nil {
			t.Fatal(err)
		}
	}
	scratch := scratchUnderDurable(t)
	program := `
from contextlib import closing
import importlib.util, json, os, pathlib, sqlite3, subprocess, sys, textwrap

shards_path = pathlib.Path('../../go-test-shards.py').resolve()
roll_path = pathlib.Path('../../tillr-roll/tillr-roll.py').resolve()
spec = importlib.util.spec_from_file_location('tillr_roll', roll_path)
roll = importlib.util.module_from_spec(spec)
spec.loader.exec_module(roll)

work = pathlib.Path(sys.argv[1])
os.environ['GO_TEST_SHARDS_LOCK_DIR'] = str(work / 'locks')
os.environ['TILLR_SQLITE_TRACE'] = '1'

repo = work / 'repo'
repo.mkdir()
live = work / 'durable' / 'bin'
live.mkdir(parents=True)

def git(*args):
    return subprocess.check_output(
        ['git', '-C', str(repo), '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', *args],
        text=True,
    ).strip()

git('init', '-q')
(repo / '.gitignore').write_text('/.tillr/\n')
(repo / 'bin').mkdir()
(repo / 'bin/pi-forks').write_text('#!/bin/sh\nexit 0\n')
(repo / 'yolo-packs/tillr/files/local/share/tillr').mkdir(parents=True)
(repo / 'yolo-packs/tillr/files/local/share/tillr/marker.txt').write_text('present\n')
(repo / 'tools').mkdir()
(repo / 'tools/go-test-shards.py').write_bytes(shards_path.read_bytes())
(repo / 'tools/go-test-durations.json').write_text('{}\n')

for mod in roll.MODULES:
    mod_dir = repo / 'tools' / f'tillr-{mod}'
    mod_dir.mkdir(parents=True)
    (mod_dir / 'go.mod').write_text(f'module example.invalid/tillr-{mod}\n\ngo 1.23\n')
    (mod_dir / 'main.go').write_text('package main\nfunc main() {}\n')

# Check that clean_checkout materializes committed paths outside tools/ and config/.
(repo / 'tools/tillr-drain/checkout_test.go').write_text(textwrap.dedent('''\
    package main
    import (
        "os"
        "testing"
    )
    func TestCleanCheckoutMaterializesCommittedRepoPathsAndStripsTrace(t *testing.T) {
        if v, ok := os.LookupEnv("TILLR_SQLITE_TRACE"); ok && v != "" {
            t.Fatalf("unexpected TILLR_SQLITE_TRACE=%q", v)
        }
        for _, p := range []string{"../../bin/pi-forks", "../../yolo-packs/tillr/files/local/share/tillr/marker.txt"} {
            if _, err := os.Stat(p); err != nil {
                t.Fatalf("missing committed path %s in roll checkout: %v", p, err)
            }
        }
    }
    func TestFailingInRoll(t *testing.T) {
        if os.Getenv("FAIL_IN_ROLL") == "1" {
            t.Fatal("deliberate roll failure")
        }
    }
    func TestPassingAfterFailure(t *testing.T) {}
'''))

git('add', '.')
git('commit', '-qm', 'initial')
first = git('rev-parse', 'HEAD')

(repo / '.tillr').mkdir()
store = repo / '.tillr/state.db'
with closing(sqlite3.connect(store)) as db, db:
    db.execute(
        'CREATE TABLE events(seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, kind TEXT NOT NULL,'
        ' actor TEXT, seat TEXT, session_id TEXT, object_type TEXT, object_id TEXT, revision INTEGER,'
        " payload TEXT NOT NULL DEFAULT '{}')"
    )
    db.executemany(
        'INSERT INTO events(ts, kind, payload) VALUES (0, ?, ?)',
        (('drain.build-started', json.dumps({'role': r, 'revision': first})) for r in ('drain', 'view')),
    )

# Advance tools/tillr-drain so roll runs go-test-shards on tillr-drain.
(repo / 'tools/tillr-drain/main.go').write_text('package main\nfunc main() { _ = 1 }\n')
git('commit', '-qam', 'advance drain')
second = git('rev-parse', 'HEAD')

# 1. Failing roll records failing test name and shard runner failure line in drain.roll-failed.
os.environ['FAIL_IN_ROLL'] = '1'
try:
    roll.roll(repo, live, True, commit=second)
except roll.RollFailed as exc:
    msg = str(exc)
else:
    raise AssertionError('expected RollFailed')

with closing(sqlite3.connect(store)) as db:
    step, detail = json.loads(
        db.execute("SELECT payload FROM events WHERE kind = 'drain.roll-failed' ORDER BY seq DESC LIMIT 1").fetchone()[0]
    )['step'], json.loads(
        db.execute("SELECT payload FROM events WHERE kind = 'drain.roll-failed' ORDER BY seq DESC LIMIT 1").fetchone()[0]
    )['detail']

assert step == 'test tillr-drain', step
assert 'example.invalid/tillr-drain shard ' in detail and 'failed (exit 1): TestFailingInRoll' in detail, detail
assert 'TestFailingInRoll' in detail and 'deliberate roll failure' in detail, detail
assert 'returned non-zero exit status' not in detail, detail
assert 'TestPassingAfterFailure' not in detail, detail

# 2. With the failure cleared, clean_checkout has bin/ and yolo-packs/ and strips TILLR_SQLITE_TRACE, so the test step passes.
os.environ.pop('FAIL_IN_ROLL', None)
# Stub loop --check by making tillr-drain accept loop --check and exit 0.
(repo / 'tools/tillr-drain/main.go').write_text('package main\nfunc main() {}\n')
git('commit', '-qam', 'ready drain')
third = git('rev-parse', 'HEAD')
rc = roll.roll(repo, live, True, commit=third)
assert rc == 0, rc
`
	if out, err := exec.Command("python3", "-c", program, scratch).CombinedOutput(); err != nil {
		t.Fatalf("roll checkout and failure detail: %v\n%s", err, out)
	}
}
