package drain

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-roll/roll"
	_ "modernc.org/sqlite"
)

func copyModuleTree(t *testing.T, srcRoot, dstRoot string) {
	t.Helper()
	absSrc, err := filepath.Abs(srcRoot)
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(absSrc, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(absSrc, path)
		if err != nil {
			return err
		}
		if rel != "." && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("copy module %s: %v", srcRoot, err)
	}
}

// Newly added tests have no recorded duration. A large fallback must not leave
// five processes idle while a single process executes all of those tests.
func TestShardRunnerBalancesUnrecordedTests(t *testing.T) {
	declareSiblingInputs(t)
	scratch := scratchUnderDurable(t)
	tillrAbs, err := filepath.Abs(filepath.Join("..", "..", "tillr"))
	if err != nil {
		t.Fatal(err)
	}
	gateAbs, err := filepath.Abs(filepath.Join("..", "..", "tillr-gate"))
	if err != nil {
		t.Fatal(err)
	}
	probeDir := filepath.Join(scratch, "assign-probe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := fmt.Sprintf(`module example.invalid/assign-probe

go 1.26.0

require github.com/mschulkind-oss/tillr-gate v0.0.0

replace github.com/mschulkind-oss/tillr-gate => %s

replace github.com/mschulkind-oss/tillr => %s
`, gateAbs, tillrAbs)
	if err := os.WriteFile(filepath.Join(probeDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	mainGo := `package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/mschulkind-oss/tillr-gate/shards"
)

func main() {
	for _, knownCount := range []int{0, 6} {
		var names []string
		durations := map[string]float64{}
		for i := 0; i < knownCount; i++ {
			n := fmt.Sprintf("TestKnown%d", i)
			names = append(names, n)
			durations[n] = 1.0
		}
		for i := 0; i < 120; i++ {
			names = append(names, fmt.Sprintf("TestNew%d", i))
		}
		groups := shards.Assign(names, durations, 6)
		var flat []string
		for _, g := range groups {
			if len(g) < 18 || len(g) > 22 {
				fmt.Fprintf(os.Stderr, "unbalanced group size %d\n", len(g))
				os.Exit(1)
			}
			flat = append(flat, g...)
		}
		want := append([]string(nil), names...)
		sort.Strings(flat)
		sort.Strings(want)
		if len(flat) != len(want) {
			fmt.Fprintf(os.Stderr, "count mismatch %d != %d\n", len(flat), len(want))
			os.Exit(1)
		}
		for i := range flat {
			if flat[i] != want[i] {
				fmt.Fprintf(os.Stderr, "mismatch at %d: %s != %s\n", i, flat[i], want[i])
				os.Exit(1)
			}
		}
	}
}
`
	if err := os.WriteFile(filepath.Join(probeDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", "-mod=mod", ".")
	cmd.Dir = probeDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shard assignment: %v\n%s", err, out)
	}
}

func TestShardRunnerFailureNamesFailingTestsAndOmitsPassingOutput(t *testing.T) {
	declareSiblingInputs(t)
	scratch := scratchUnderDurable(t)
	modDir := filepath.Join(scratch, "fail-mod")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "go.mod"), []byte("module example.invalid/shard-failure-test\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var testSrc strings.Builder
	testSrc.WriteString(`package failmod

import "testing"

func TestAlphaFail(t *testing.T) {
	t.Fatal("broken invariant")
}
`)
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&testSrc, "\nfunc TestPass%d(t *testing.T) {}\n", i)
	}
	if err := os.WriteFile(filepath.Join(modDir, "alpha_test.go"), []byte(testSrc.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	durPath := filepath.Join(scratch, "durations.json")
	if err := os.WriteFile(durPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(scratch, "events.jsonl")
	gateAbs, err := filepath.Abs(filepath.Join("..", "..", "tillr-gate"))
	if err != nil {
		t.Fatal(err)
	}
	shardScratch := filepath.Join(scratch, "runner")
	if err := os.MkdirAll(shardScratch, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", "-C", gateAbs, ".", "shards", modDir,
		"-k", "1",
		"--durations", durPath,
		"--events", eventsPath,
		"--scratch", shardScratch,
	)
	outBytes, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit from failing shard, output:\n%s", string(outBytes))
	}
	text := string(outBytes)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	headerRe := regexp.MustCompile(`^go-test-shards: example\.invalid/shard-failure-test shard \d+ failed \(exit 1\): TestAlphaFail$`)
	var matchedHeader bool
	for _, line := range lines {
		if headerRe.MatchString(line) {
			matchedHeader = true
			break
		}
	}
	if !matchedHeader {
		t.Fatalf("missing shard failure header in output:\n%s", text)
	}
	if !strings.Contains(text, "alpha_test.go:") || !strings.Contains(text, "broken invariant") {
		t.Fatalf("missing failing test detail:\n%s", text)
	}
	if strings.Contains(text, "TestPass0") || strings.Contains(text, "--- PASS:") {
		t.Fatalf("passing test output leaked:\n%s", text)
	}

	evFile, err := os.Open(eventsPath)
	if err != nil {
		t.Fatalf("open events %s: %v", eventsPath, err)
	}
	defer evFile.Close()
	var pass29 bool
	scanner := bufio.NewScanner(evFile)
	for scanner.Scan() {
		var ev struct {
			Test   string `json:"Test"`
			Action string `json:"Action"`
		}
		if json.Unmarshal(scanner.Bytes(), &ev) == nil && ev.Test == "TestPass29" && ev.Action == "pass" {
			pass29 = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !pass29 {
		t.Fatalf("expected TestPass29 pass event in %s", eventsPath)
	}
}

func TestRollCleanCheckoutIncludesFullTreeAndRecordsShardFailureDetails(t *testing.T) {
	declareSiblingInputs(t)
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
	writeFixture("tools/go-test-durations.json", "{}\n", 0o644)

	copyModuleTree(t, filepath.Join("..", "..", "tillr"), filepath.Join(repo, "tools", "tillr"))
	copyModuleTree(t, filepath.Join("..", "..", "tillr-gate"), filepath.Join(repo, "tools", "tillr-gate"))

	for _, mod := range roll.Modules {
		if mod == "gate" {
			continue
		}
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

	// Advance tools/tillr-drain so roll runs tillr-gate shards on tillr-drain.
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
