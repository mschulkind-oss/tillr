package drain

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-roll/roll"
	_ "modernc.org/sqlite"
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
	shardsBytes, err := os.ReadFile("../.sibling-inputs/go-test-shards.py")
	if err != nil {
		t.Fatal(err)
	}
	work := scratchUnderDurable(t)
	t.Setenv("TILLR_SQLITE_TRACE", "1")

	repo := filepath.Join(work, "repo")
	live := filepath.Join(work, "durable", "bin")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}

	writeFixture := func(rel, content string, mode os.FileMode) {
		t.Helper()
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}

	runGit(t, repo, "init", "-q")
	writeFixture(".gitignore", "/.tillr/\n", 0o644)
	writeFixture("bin/pi-forks", "#!/bin/sh\nexit 0\n", 0o755)
	writeFixture("yolo-packs/tillr/files/local/share/tillr/marker.txt", "present\n", 0o644)
	writeFixture("tools/go-test-shards.py", string(shardsBytes), 0o644)
	writeFixture("tools/go-test-durations.json", "{}\n", 0o644)

	for _, mod := range roll.Modules {
		writeFixture(filepath.Join("tools", "tillr-"+mod, "go.mod"), "module example.invalid/tillr-"+mod+"\n\ngo 1.23\n", 0o644)
		writeFixture(filepath.Join("tools", "tillr-"+mod, "main.go"), "package main\nfunc main() {}\n", 0o644)
	}

	// Check that clean_checkout materializes committed paths outside tools/ and config/.
	writeFixture("tools/tillr-drain/checkout_test.go", `package main

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
`, 0o644)

	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "initial")
	first := runGit(t, repo, "rev-parse", "HEAD")

	if err := os.MkdirAll(filepath.Join(repo, ".tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(repo, ".tillr", "state.db")
	db, err := sql.Open("sqlite", storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(
		"CREATE TABLE events(seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, kind TEXT NOT NULL," +
			" actor TEXT, seat TEXT, session_id TEXT, object_type TEXT, object_id TEXT, revision INTEGER," +
			" payload TEXT NOT NULL DEFAULT '{}')",
	); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"drain", "view"} {
		payload, err := json.Marshal(map[string]string{"role": role, "revision": first})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO events(ts, kind, payload) VALUES (0, ?, ?)", "drain.build-started", string(payload)); err != nil {
			t.Fatal(err)
		}
	}

	// Advance tools/tillr-drain so roll runs go-test-shards on tillr-drain.
	writeFixture("tools/tillr-drain/main.go", "package main\nfunc main() { _ = 1 }\n", 0o644)
	runGit(t, repo, "commit", "-qam", "advance drain")
	second := runGit(t, repo, "rev-parse", "HEAD")

	// 1. Failing roll records failing test name and shard runner failure line in drain.roll-failed.
	t.Setenv("FAIL_IN_ROLL", "1")
	_, rollErr := roll.Roll(repo, live, true, false, second, nil)
	var rf *roll.RollFailedError
	if !errors.As(rollErr, &rf) {
		t.Fatalf("expected *roll.RollFailedError, got %T: %v", rollErr, rollErr)
	}

	var payloadJSON string
	if err := db.QueryRow("SELECT payload FROM events WHERE kind = 'drain.roll-failed' ORDER BY seq DESC LIMIT 1").Scan(&payloadJSON); err != nil {
		t.Fatal(err)
	}
	var failedPayload struct {
		Step   string `json:"step"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &failedPayload); err != nil {
		t.Fatal(err)
	}
	if failedPayload.Step != "test tillr-drain" {
		t.Fatalf("step = %q, want %q", failedPayload.Step, "test tillr-drain")
	}
	detail := failedPayload.Detail
	if !strings.Contains(detail, "example.invalid/tillr-drain shard ") || !strings.Contains(detail, "failed (exit 1): TestFailingInRoll") {
		t.Fatalf("unexpected shard failure header in detail:\n%s", detail)
	}
	if !strings.Contains(detail, "TestFailingInRoll") || !strings.Contains(detail, "deliberate roll failure") {
		t.Fatalf("missing failing test detail:\n%s", detail)
	}
	if strings.Contains(detail, "returned non-zero exit status") {
		t.Fatalf("unexpected generic exit status in detail:\n%s", detail)
	}
	if strings.Contains(detail, "TestPassingAfterFailure") {
		t.Fatalf("passing test output leaked into detail:\n%s", detail)
	}

	// 2. With the failure cleared, clean_checkout has bin/ and yolo-packs/ and strips TILLR_SQLITE_TRACE, so the test step passes.
	if err := os.Unsetenv("FAIL_IN_ROLL"); err != nil {
		t.Fatal(err)
	}
	// Stub loop --check by making tillr-drain accept loop --check and exit 0.
	writeFixture("tools/tillr-drain/main.go", "package main\nfunc main() {}\n", 0o644)
	runGit(t, repo, "commit", "-qam", "ready drain")
	third := runGit(t, repo, "rev-parse", "HEAD")
	rc, err := roll.Roll(repo, live, true, false, third, nil)
	if err != nil || rc != 0 {
		t.Fatalf("Roll(third) = (%d, %v), want (0, nil)", rc, err)
	}
}
