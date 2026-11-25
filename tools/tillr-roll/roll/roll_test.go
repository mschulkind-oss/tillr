package roll

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const hangGuard = 5 * time.Minute

func TestFailureDetail(t *testing.T) {
	err := &ExitError{
		Cause:    errors.New("exit status 1"),
		ExitCode: 1,
		Stdout: strings.Join([]string{
			"tillr-sqlite open something",
			"go-test-shards: tools/tillr-drain waiting for a test slot",
			"tools/tillr-drain shard 2 failed (exit 1): TestAlpha, TestBeta",
			"--- FAIL: TestAlpha (0.01s)",
			"    alpha_test.go:10: expected 1, got 2",
			"    === RUN   TestIgnored",
			"    --- PASS: TestPass (0.00s)",
		}, "\n"),
	}
	got := FailureDetail(err)
	if !strings.Contains(got, "go-test-shards: tools/tillr-drain shard 2 failed (exit 1): TestAlpha, TestBeta") {
		t.Fatalf("missing normalized shard line in %q", got)
	}
	if !strings.Contains(got, "alpha_test.go:10: expected 1, got 2") {
		t.Fatalf("missing diagnostic line in %q", got)
	}
	if strings.Contains(got, "tillr-sqlite") || strings.Contains(got, "waiting for a test slot") || strings.Contains(got, "--- PASS:") {
		t.Fatalf("filtered line leaked into %q", got)
	}
}

func TestSteppedMapsExitIncompatible(t *testing.T) {
	err := Stepped("loop check", func() error {
		return &ExitError{Cause: errors.New("exit 3"), ExitCode: ExitIncompatible, Stderr: "incompatible"}
	})
	var rf *RollFailedError
	if !errors.As(err, &rf) {
		t.Fatalf("expected RollFailedError, got %T", err)
	}
	if rf.Step != "store check" {
		t.Fatalf("expected step 'store check', got %q", rf.Step)
	}
}

func TestBuildsAndRecordFailure(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "state.db")
	db, err := sql.Open("sqlite", "file:"+store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE events(seq INTEGER PRIMARY KEY, ts INTEGER NOT NULL DEFAULT 0, kind TEXT, object_type TEXT, object_id TEXT, payload TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO events(kind, payload) VALUES ('drain.build-started', '{"role":"drain","revision":"abc123","digest":"deadbeef"}')`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	facts, err := Builds(store)
	if err != nil {
		t.Fatal(err)
	}
	if facts["drain"].Revision != "abc123" || facts["drain"].Seq != 1 {
		t.Fatalf("unexpected facts: %+v", facts)
	}

	var errBuf bytes.Buffer
	RecordFailure(store, "abc123def4567890", "build tillr-drain", "compile\nfailed", &errBuf)
	if errBuf.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", errBuf.String())
	}

	// Missing store with store check writes roll-alarm.json.
	badStore := filepath.Join(dir, "missing.db")
	errBuf.Reset()
	RecordFailure(badStore, "abc123def4567890", "store check", "schema mismatch", &errBuf)
	if !strings.Contains(errBuf.String(), "could not record the failed roll of abc123def456") {
		t.Fatalf("expected stderr warning, got %q", errBuf.String())
	}
	alarmBytes, err := os.ReadFile(badStore + ".roll-alarm.json")
	if err != nil {
		t.Fatalf("expected alarm file: %v", err)
	}
	var alarm map[string]any
	if err := json.Unmarshal(alarmBytes, &alarm); err != nil {
		t.Fatal(err)
	}
	if alarm["mover"] != "operator" || alarm["step"] != "store check" {
		t.Fatalf("unexpected alarm payload: %+v", alarm)
	}
}

type failFlushWriter struct {
	bytes.Buffer
	flushErr error
}

func (w *failFlushWriter) Flush() error {
	return w.flushErr
}

func TestReportFlushErrorAndMismatch(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "bin")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "tillr-drain"), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := Digest(filepath.Join(live, "tillr-drain"))
	if err != nil {
		t.Fatal(err)
	}

	old := map[string]BuildFact{"drain": {Revision: "oldrev"}}
	newFacts := map[string]BuildFact{"drain": {Revision: "newrev", Digest: d}}
	answers := map[string]string{"drain": "restarted", "view": "not-requested"}

	var out bytes.Buffer
	status, err := Report(old, newFacts, answers, []string{"drain", "view"}, live, &out)
	if err != nil || status != 0 {
		t.Fatalf("status=%d err=%v out=%q", status, err, out.String())
	}
	if !strings.Contains(out.String(), "drain: old=oldrev new=newrev answer=restarted\n") {
		t.Fatalf("unexpected output: %q", out.String())
	}

	fw := &failFlushWriter{flushErr: errors.New("no space left")}
	status, err = Report(old, newFacts, answers, []string{"drain", "view"}, live, fw)
	if err == nil || status != 1 {
		t.Fatalf("expected flush error, got status=%d err=%v", status, err)
	}
}

func TestWatchRetirementAndSettle(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "bin")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	watch := filepath.Join(live, "tillr-watch")
	if err := os.WriteFile(watch, []byte("watch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, Rolled), []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stageName := SchemaStage + strings.Repeat("a", 64)
	marker := filepath.Join(dir, WatchRetire)
	if err := PublishWatchRetirement(marker, "deadbeef", filepath.Join(live, stageName)); err != nil {
		t.Fatal(err)
	}
	if err := SettleWatchRetirement(live, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(watch); !os.IsNotExist(err) {
		t.Fatalf("expected tillr-watch to be retired, err=%v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected marker to be removed, err=%v", err)
	}
}

func TestSettleWatchRetirementMarkerUnlinkErrorRecovery(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "bin")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	watch := filepath.Join(live, "tillr-watch")
	if err := os.WriteFile(watch, []byte("watch-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, Rolled), []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageName := SchemaStage + strings.Repeat("a", 64)
	marker := filepath.Join(dir, WatchRetire)
	if err := PublishWatchRetirement(marker, "deadbeef", filepath.Join(live, stageName)); err != nil {
		t.Fatal(err)
	}
	markerBytes, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}

	deps := &Deps{
		Remove: func(path string) error {
			if path == marker {
				return os.ErrPermission
			}
			return os.Remove(path)
		},
	}
	err = SettleWatchRetirement(live, deps)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected ErrPermission, got %v", err)
	}
	gotWatch, err := os.ReadFile(watch)
	if err != nil || string(gotWatch) != "watch-binary" {
		t.Fatalf("expected tillr-watch restored, got %q err=%v", string(gotWatch), err)
	}
	gotMarker, err := os.ReadFile(marker)
	if err != nil || !bytes.Equal(gotMarker, markerBytes) {
		t.Fatalf("expected marker intact, got %q err=%v", string(gotMarker), err)
	}
	backups, err := filepath.Glob(filepath.Join(live, ".tillr-watch-*"))
	if err != nil || len(backups) != 0 {
		t.Fatalf("expected no backup left behind, got %v err=%v", backups, err)
	}
}

func TestSettleWatchRetirementBackupUnlinkErrorRecovery(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "bin")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	watch := filepath.Join(live, "tillr-watch")
	if err := os.WriteFile(watch, []byte("watch-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, Rolled), []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageName := SchemaStage + strings.Repeat("b", 64)
	marker := filepath.Join(dir, WatchRetire)
	if err := PublishWatchRetirement(marker, "deadbeef", filepath.Join(live, stageName)); err != nil {
		t.Fatal(err)
	}
	markerBytes, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}

	deps := &Deps{
		Remove: func(path string) error {
			if strings.HasPrefix(filepath.Base(path), ".tillr-watch-") {
				return os.ErrPermission
			}
			return os.Remove(path)
		},
	}
	err = SettleWatchRetirement(live, deps)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected ErrPermission, got %v", err)
	}
	gotWatch, err := os.ReadFile(watch)
	if err != nil || string(gotWatch) != "watch-binary" {
		t.Fatalf("expected tillr-watch restored, got %q err=%v", string(gotWatch), err)
	}
	gotMarker, err := os.ReadFile(marker)
	if err != nil || !bytes.Equal(gotMarker, markerBytes) {
		t.Fatalf("expected marker rewritten with saved bytes, got %q err=%v", string(gotMarker), err)
	}
	backups, err := filepath.Glob(filepath.Join(live, ".tillr-watch-*"))
	if err != nil || len(backups) != 0 {
		t.Fatalf("expected no backup left behind, got %v err=%v", backups, err)
	}
}

func TestSettleWatchRetirementFallbackMarkerUnlinkError(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "bin")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, WatchRetire)
	if err := os.WriteFile(marker, []byte("not-json"), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := &Deps{
		Remove: func(path string) error {
			if path == marker {
				return os.ErrPermission
			}
			return os.Remove(path)
		},
	}
	err := SettleWatchRetirement(live, deps)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected ErrPermission, got %v", err)
	}
}

func TestEdgeInputs(t *testing.T) {
	_ = os.Getenv("TILLR_GATE_EDGE_INPUTS")
}

func repoRootForRollTest(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runGitFixture(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-C", root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid"}, args...)
	out, err := exec.Command("git", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func setupRollFixture(t *testing.T, work, viewSource string) (string, string, func(args ...string) string) {
	t.Helper()
	if viewSource == "" {
		viewSource = "package main\nfunc main() {}\n"
	}
	root := filepath.Join(work, "repo")
	live := filepath.Join(work, "bin")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}

	git := func(args ...string) string {
		return runGitFixture(t, root, args...)
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.tillr/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, module := range Modules {
		source := filepath.Join(root, "tools", "tillr-"+module)
		if err := os.MkdirAll(source, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte(fmt.Sprintf("module example.invalid/%s\n\ngo 1.23\n", module)), 0o644); err != nil {
			t.Fatal(err)
		}
		src := "package main\nfunc main() {}\n"
		if module == "view" {
			src = viewSource
		}
		if err := os.WriteFile(filepath.Join(source, "main.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "tools", "go-test-shards.py"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "fixture source")
	revision := git("rev-parse", "HEAD")

	if err := os.MkdirAll(filepath.Join(root, ".tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(root, ".tillr", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE events(
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		ts INTEGER NOT NULL,
		kind TEXT NOT NULL,
		actor TEXT,
		seat TEXT,
		session_id TEXT,
		object_type TEXT,
		object_id TEXT,
		revision INTEGER,
		payload TEXT NOT NULL DEFAULT '{}'
	)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"drain", "view"} {
		payload, _ := json.Marshal(map[string]string{"role": role, "revision": revision})
		if _, err := db.Exec(`INSERT INTO events(ts, kind, payload) VALUES (0, 'drain.build-started', ?)`, string(payload)); err != nil {
			t.Fatal(err)
		}
	}
	return root, live, git
}

func TestRepositoryEnablesRollAndPushAndExplainsTheInstall(t *testing.T) {
	root := repoRootForRollTest(t)
	tomlBytes, err := os.ReadFile(filepath.Join(root, ".tillr.toml"))
	if err != nil {
		t.Fatal(err)
	}

	sections := make(map[string]map[string]string)
	current := ""
	for _, rawLine := range strings.Split(string(tomlBytes), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = strings.TrimSpace(line[1 : len(line)-1])
			if sections[current] == nil {
				sections[current] = make(map[string]string)
			}
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && current != "" {
			sections[current][strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}

	wantRoll := map[string]string{
		"paths":                 `["tools/tillr*/", "config/tillr/"]`,
		"command":               `"just tillr-roll --no-request"`,
		"pause_timeout_seconds": "120",
	}
	if !reflect.DeepEqual(sections["roll"], wantRoll) {
		t.Fatalf("[roll] = %+v, want %+v", sections["roll"], wantRoll)
	}
	wantPush := map[string]string{
		"what": `"green"`,
	}
	if !reflect.DeepEqual(sections["push"], wantPush) {
		t.Fatalf("[push] = %+v, want %+v", sections["push"], wantPush)
	}

	docSections := map[string]string{
		"tools/tillr-drain/README.md":                      "Moving a running loop onto new code",
		"yolo-packs/tillr/skills/tillr-operating/SKILL.md": "Update a running drain",
	}
	for relPath, heading := range docSections {
		t.Run(relPath, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(root, relPath))
			if err != nil {
				t.Fatal(err)
			}
			marker := "## " + heading + "\n"
			_, after, ok := strings.Cut(string(content), marker)
			if !ok {
				t.Fatalf("heading %q not found in %s", heading, relPath)
			}
			body, _, _ := strings.Cut(after, "\n## ")
			body = strings.TrimSpace(body)
			lines := strings.Split(body, "\n")
			if len(lines) > 5 {
				t.Fatalf("section %q in %s has %d lines (> 5)", heading, relPath, len(lines))
			}
			for _, cmd := range []string{"just tillr-build", "just tillr-roll", "just tillr-restart", "yolo host apply --assert"} {
				if !strings.Contains(body, cmd) {
					t.Fatalf("section %q in %s missing %q", heading, relPath, cmd)
				}
			}
		})
	}
}

func TestLateBuildFailurePreservesEveryLiveBinary(t *testing.T) {
	repoRoot := repoRootForRollTest(t)
	work := t.TempDir()
	root := filepath.Join(work, "repo")
	live := filepath.Join(work, "bin")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}

	runGitFixture(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.tillr/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	justBytes, err := os.ReadFile(filepath.Join(repoRoot, ".justfile"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".justfile"), justBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Copy the tools/tillr-roll/ Go module into the fixture repo.
	rollSrc := filepath.Join(repoRoot, "tools", "tillr-roll")
	rollDst := filepath.Join(root, "tools", "tillr-roll")
	if err := os.MkdirAll(filepath.Join(rollDst, "roll"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"go.mod", "go.sum", "main.go", "roll/failure.go", "roll/roll.go", "roll/types.go"} {
		data, err := os.ReadFile(filepath.Join(rollSrc, rel))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rollDst, rel), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, module := range Modules {
		source := filepath.Join(root, "tools", "tillr-"+module)
		if err := os.MkdirAll(source, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte(fmt.Sprintf("module example.invalid/%s\n\ngo 1.23\n", module)), 0o644); err != nil {
			t.Fatal(err)
		}
		mainSrc := "package main\nfunc main() {}\n"
		if module == "view" {
			mainSrc = "broken view source\n"
		}
		if err := os.WriteFile(filepath.Join(source, "main.go"), []byte(mainSrc), 0o644); err != nil {
			t.Fatal(err)
		}
		name := "tillr-" + module
		if module == "cli" {
			name = "tillr"
		}
		if err := os.WriteFile(filepath.Join(live, name), []byte("old "+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	runGitFixture(t, root, "add", ".")
	runGitFixture(t, root, "commit", "-qm", "fixture source")
	revision := runGitFixture(t, root, "rev-parse", "HEAD")

	if err := os.MkdirAll(filepath.Join(root, ".tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(root, ".tillr", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE events(seq INTEGER PRIMARY KEY, ts INTEGER NOT NULL DEFAULT 0, kind TEXT, object_type TEXT, object_id TEXT, payload TEXT)`)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	for _, role := range []string{"drain", "view"} {
		payload, _ := json.Marshal(map[string]string{"role": role, "revision": revision})
		if _, err := db.Exec(`INSERT INTO events(kind, payload) VALUES ('drain.build-started', ?)`, string(payload)); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	_ = db.Close()

	checksums := func() map[string]string {
		out := make(map[string]string)
		entries, err := os.ReadDir(live)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(live, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(b)
			out[e.Name()] = hex.EncodeToString(sum[:])
		}
		return out
	}

	before := checksums()
	cmd := exec.Command("go", "run", ".", "--no-request")
	cmd.Dir = filepath.Join(root, "tools", "tillr-roll")
	cmd.Env = withEnvVar(os.Environ(), "YOLO_DURABLE_DIR", work)
	outBytes, err := cmd.CombinedOutput()
	out := string(outBytes)
	if err == nil {
		t.Fatalf("expected tillr-roll --no-request to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "expected 'package', found broken") {
		t.Fatalf("expected parse error in output:\n%s", out)
	}
	if !strings.Contains(out, "tillr-view") {
		t.Fatalf("expected tillr-view in output:\n%s", out)
	}
	after := checksums()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("live binaries changed after late build failure: before=%v after=%v", before, after)
	}
}

func TestRollOfACommitInstallsThatCommitWhileTheSharedCheckoutAdvances(t *testing.T) {
	work := t.TempDir()
	root, live, git := setupRollFixture(t, work, "")
	if err := os.WriteFile(filepath.Join(root, "tools", "tillr-drain", "main.go"), []byte("package main\nfunc main() { _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-qam", "A")
	commitA := git("rev-parse", "HEAD")

	var advanced []string
	var sharded [][]string
	deps := &Deps{
		Stdout: io.Discard,
		Stderr: io.Discard,
		Run: func(args []string, opts RunOpts) (CmdResult, error) {
			if len(args) > 0 && args[0] == "python3" {
				sharded = append(sharded, append([]string(nil), args...))
				return CmdResult{}, nil
			}
			res, err := Run(args, opts, nil)
			if len(args) >= 2 && args[0] == "go" && args[1] == "build" && len(advanced) == 0 {
				if wErr := os.WriteFile(filepath.Join(root, "tools", "tillr-drain", "main.go"), []byte("package main\nfunc main() { _ = 2 }\n"), 0o644); wErr != nil {
					t.Fatal(wErr)
				}
				git("commit", "-qam", "B")
				if wErr := os.WriteFile(filepath.Join(root, "tools", "tillr-cli", "main.go"), []byte("package main\nfunc main() { _ = 3 }\n"), 0o644); wErr != nil {
					t.Fatal(wErr)
				}
				advanced = append(advanced, git("rev-parse", "HEAD"))
			}
			return res, err
		},
	}

	status, err := Roll(root, live, true, false, commitA, deps)
	if err != nil || status != 0 {
		t.Fatalf("Roll failed: status=%d err=%v", status, err)
	}
	if len(advanced) == 0 || advanced[0] == commitA {
		t.Fatalf("expected shared checkout to advance beyond %s, got %v", commitA, advanced)
	}
	if gotHead := git("rev-parse", "HEAD"); gotHead != advanced[0] {
		t.Fatalf("HEAD = %s, want %s", gotHead, advanced[0])
	}
	for _, name := range []string{"tillr-drain", "tillr", "tillr-view"} {
		out, err := exec.Command("go", "version", "-m", filepath.Join(live, name)).CombinedOutput()
		if err != nil {
			t.Fatalf("go version -m %s failed: %v\n%s", name, err, out)
		}
		stamp := string(out)
		if !strings.Contains(stamp, "vcs.revision="+commitA) {
			t.Fatalf("%s missing vcs.revision=%s:\n%s", name, commitA, stamp)
		}
		if !strings.Contains(stamp, "vcs.modified=false") {
			t.Fatalf("%s missing vcs.modified=false:\n%s", name, stamp)
		}
	}
	rolledBytes, err := os.ReadFile(filepath.Join(filepath.Dir(live), Rolled))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(rolledBytes)) != commitA {
		t.Fatalf("rolled commit = %q, want %q", strings.TrimSpace(string(rolledBytes)), commitA)
	}
	if len(sharded) != 1 {
		t.Fatalf("expected 1 sharded test run, got %d: %v", len(sharded), sharded)
	}
	if !strings.HasSuffix(sharded[0][1], "tools/go-test-shards.py") || sharded[0][2] != "tools/tillr-drain" {
		t.Fatalf("unexpected sharded command: %v", sharded[0])
	}
	if !reflect.DeepEqual(sharded[0][3:7], []string{"-k", "6", "--timeout", "0"}) {
		t.Fatalf("unexpected sharded flags: %v", sharded[0][3:7])
	}
}

func TestShardedModulesMatchTheGate(t *testing.T) {
	root := repoRootForRollTest(t)
	paths, err := filepath.Glob(filepath.Join(root, "tools", "gate", "checks", "go-test-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`go-test-shards\.py (tools/[\w-]+) -k`)
	gateSharded := make(map[string]bool)
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var check struct {
			Command []string `json:"command"`
		}
		if err := json.Unmarshal(b, &check); err != nil {
			t.Fatal(err)
		}
		if m := re.FindStringSubmatch(strings.Join(check.Command, " ")); len(m) == 2 {
			gateSharded[m[1]] = true
		}
	}
	wantSharded := make(map[string]bool, len(Sharded))
	for _, s := range Sharded {
		wantSharded[s] = true
	}
	if !reflect.DeepEqual(gateSharded, wantSharded) {
		t.Fatalf("gate sharded=%v != Sharded=%v", gateSharded, wantSharded)
	}
}

func TestRequestsDuringARollYieldOneFurtherRollOfTheNewestTip(t *testing.T) {
	work := t.TempDir()
	root, live, git := setupRollFixture(t, work, "")
	first := git("rev-parse", "HEAD")

	var mu sync.Mutex
	var rolled []string
	started := make(chan struct{})
	release := make(chan struct{})
	waiting := make(chan struct{}, 4)

	deps := &Deps{
		Stdout: io.Discard,
		OnWait: func() {
			waiting <- struct{}{}
		},
		RollCommit: func(r, l, tip string, noRequest bool, store string) (int, error) {
			mu.Lock()
			rolled = append(rolled, tip)
			n := len(rolled)
			mu.Unlock()
			if err := os.WriteFile(filepath.Join(filepath.Dir(l), Rolled), []byte(tip+"\n"), 0o644); err != nil {
				return 1, err
			}
			if n == 1 {
				close(started)
				select {
				case <-release:
				case <-time.After(hangGuard):
					return 1, errors.New("timed out waiting for release")
				}
			}
			return 0, nil
		},
	}

	type result struct {
		status int
		err    error
	}
	ch1 := make(chan result, 1)
	ch2 := make(chan result, 1)
	ch3 := make(chan result, 1)

	go func() {
		s, e := Roll(root, live, true, false, "", deps)
		ch1 <- result{s, e}
	}()
	select {
	case <-started:
	case <-time.After(hangGuard):
		t.Fatal("timed out waiting for first roll to start")
	}

	if err := os.WriteFile(filepath.Join(root, "second"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "B")
	go func() {
		s, e := Roll(root, live, true, false, "", deps)
		ch2 <- result{s, e}
	}()
	select {
	case <-waiting:
	case <-time.After(hangGuard):
		t.Fatal("timed out waiting for second roll to block on lock")
	}

	if err := os.WriteFile(filepath.Join(root, "third"), []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "C")
	newest := git("rev-parse", "HEAD")
	go func() {
		s, e := Roll(root, live, true, false, "", deps)
		ch3 <- result{s, e}
	}()
	select {
	case <-waiting:
	case <-time.After(hangGuard):
		t.Fatal("timed out waiting for third roll to block on lock")
	}

	close(release)
	for i, ch := range []chan result{ch1, ch2, ch3} {
		select {
		case res := <-ch:
			if res.err != nil || res.status != 0 {
				t.Fatalf("goroutine %d failed: status=%d err=%v", i+1, res.status, res.err)
			}
		case <-time.After(hangGuard):
			t.Fatalf("goroutine %d timed out", i+1)
		}
	}
	if !reflect.DeepEqual(rolled, []string{first, newest}) {
		t.Fatalf("rolled = %v, want [%s %s]", rolled, first, newest)
	}
}

func TestFailedRollRecordsTheCommitAndFailingStep(t *testing.T) {
	work := t.TempDir()
	root, live, git := setupRollFixture(t, work, "broken view source\n")
	commit := git("rev-parse", "HEAD")

	_, err := Roll(root, live, true, false, "", &Deps{Stdout: io.Discard, Stderr: io.Discard})
	var rf *RollFailedError
	if !errors.As(err, &rf) {
		t.Fatalf("expected RollFailedError, got %v", err)
	}

	db, dbErr := sql.Open("sqlite", "file:"+filepath.Join(root, ".tillr", "state.db")+"?mode=ro")
	if dbErr != nil {
		t.Fatal(dbErr)
	}
	defer db.Close()
	var kind, objectID, payload string
	if qErr := db.QueryRow("SELECT kind, object_id, payload FROM events WHERE kind = 'drain.roll-failed'").Scan(&kind, &objectID, &payload); qErr != nil {
		t.Fatal(qErr)
	}
	if kind != "drain.roll-failed" || objectID != commit {
		t.Fatalf("got kind=%q object_id=%q, want drain.roll-failed %q", kind, objectID, commit)
	}
	var p struct {
		Step string `json:"step"`
	}
	if jErr := json.Unmarshal([]byte(payload), &p); jErr != nil {
		t.Fatal(jErr)
	}
	if p.Step != "build tillr-view" {
		t.Fatalf("step = %q, want 'build tillr-view'", p.Step)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(live), Rolled)); !os.IsNotExist(statErr) {
		t.Fatalf("expected rolled stamp not to exist, err=%v", statErr)
	}
}

func TestGreenPointerProvesEverySuiteAndAGatedLandingItsOwn(t *testing.T) {
	work := t.TempDir()
	root, _, git := setupRollFixture(t, work, "")
	first := git("rev-parse", "HEAD")

	if got := Evidence(root, first, nil); len(got) != 0 || got == nil {
		t.Fatalf("expected empty non-nil Evidence before green ref, got %v", got)
	}
	git("update-ref", "refs/tillr/green", first)
	if got := Evidence(root, first, nil); got != nil {
		t.Fatalf("expected nil Evidence when green ref points at commit, got %v", got)
	}

	if err := os.WriteFile(filepath.Join(root, "tools", "tillr-drain", "main.go"), []byte("package main\nfunc main() { _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-qam", "next")
	tip := git("rev-parse", "HEAD")
	if got := Evidence(root, tip, nil); len(got) != 0 || got == nil {
		t.Fatalf("expected empty non-nil Evidence for commit ahead of green, got %v", got)
	}

	store := filepath.Join(root, ".tillr", "state.db")
	if got := GatedModules(root, tip, store, nil); len(got) != 0 {
		t.Fatalf("expected empty GatedModules before landing row, got %v", got)
	}
	tree := git("rev-parse", tip+"^{tree}")
	db, err := sql.Open("sqlite", "file:"+store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE landings(trunk_after TEXT, result TEXT, gated_tree TEXT, landed_tree TEXT)")
	if err == nil {
		_, err = db.Exec("INSERT INTO landings VALUES (?, 'landed', ?, ?)", tip, tree, tree)
	}
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	got := GatedModules(root, tip, store, nil)
	if !reflect.DeepEqual(got, map[string]bool{"tillr-drain": true}) {
		t.Fatalf("GatedModules = %v, want map[tillr-drain:true]", got)
	}
}

func TestReportRefusesMismatchedDigestAndRefusedAnswer(t *testing.T) {
	live := t.TempDir()
	path := filepath.Join(live, "tillr-view")
	if err := os.WriteFile(path, []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := map[string]BuildFact{"view": {Revision: "first"}}
	newFacts := map[string]BuildFact{"view": {Revision: "second", Digest: "incorrect"}}

	var out bytes.Buffer
	status, err := Report(old, newFacts, map[string]string{"view": "already-current"}, []string{"view"}, live, &out)
	if err != nil || status != 1 {
		t.Fatalf("expected status=1 err=nil, got status=%d err=%v", status, err)
	}
	wantLine := "view: old=first new=second answer=already-current; part does not run from the live directory\n"
	if out.String() != wantLine {
		t.Fatalf("output = %q, want %q", out.String(), wantLine)
	}

	d, err := Digest(path)
	if err != nil {
		t.Fatal(err)
	}
	newFacts["view"] = BuildFact{Revision: "second", Digest: d}

	out.Reset()
	status, err = Report(old, newFacts, map[string]string{"view": "restarted"}, []string{"view"}, live, &out)
	if err != nil || status != 0 {
		t.Fatalf("expected status=0 err=nil, got status=%d err=%v", status, err)
	}

	out.Reset()
	status, err = Report(old, newFacts, map[string]string{"view": "refused"}, []string{"view"}, live, &out)
	if err != nil || status != 1 {
		t.Fatalf("expected status=1 err=nil for refused, got status=%d err=%v", status, err)
	}
}

func TestPiExtensionsInvokesBuildAgentDirWithoutRestarts(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "repo")
	builder := filepath.Join(root, "tools", "tillr-run", "backends", "pi", "build-agent-dir")
	if err := os.MkdirAll(filepath.Dir(builder), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(work, "built.txt")
	script := fmt.Sprintf("#!/bin/sh\necho built > %s\n", shellQuote(marker))
	if err := os.WriteFile(builder, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	status, err := RollPiExtensions(root, nil)
	if err != nil || status != 0 {
		t.Fatalf("RollPiExtensions failed: status=%d err=%v", status, err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "built\n" {
		t.Fatalf("marker = %q, want %q", string(got), "built\n")
	}
}

func TestRollPiFlagInvokesPiForksBuildPi(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "bin")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	type call struct {
		args []string
		dir  string
	}
	var calls []call
	deps := &Deps{
		Git: func(dir string, args ...string) (string, error) {
			return "", nil
		},
		Run: func(args []string, opts RunOpts) (CmdResult, error) {
			calls = append(calls, call{args: append([]string(nil), args...), dir: opts.Dir})
			return CmdResult{}, nil
		},
	}
	status, err := Roll(root, live, true, true, "", deps)
	if err != nil || status != 0 {
		t.Fatalf("Roll --pi failed: status=%d err=%v", status, err)
	}
	want := []call{{args: []string{filepath.Join(root, "bin", "pi-forks"), "build-pi"}, dir: root}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %+v, want %+v", calls, want)
	}
}

func TestTestCommitShardsChangedToolsGate(t *testing.T) {
	work := t.TempDir()
	root, _, git := setupRollFixture(t, work, "")
	base := git("rev-parse", "HEAD")

	gateDir := filepath.Join(root, "tools", "gate")
	if err := os.MkdirAll(gateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gateDir, "go.mod"), []byte("module example.invalid/gate\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gateDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "tools/gate")
	git("commit", "-qm", "add tools/gate")
	tip := git("rev-parse", "HEAD")

	var sharded [][]string
	deps := &Deps{
		Stdout: io.Discard,
		Stderr: io.Discard,
		Run: func(args []string, opts RunOpts) (CmdResult, error) {
			if len(args) > 0 && args[0] == "python3" {
				sharded = append(sharded, append([]string(nil), args...))
				return CmdResult{}, nil
			}
			return Run(args, opts, nil)
		},
	}
	old := map[string]BuildFact{
		"drain": {Role: "drain", Revision: base},
		"view":  {Role: "view", Revision: base},
	}
	if err := TestCommit(root, root, work, tip, filepath.Join(root, ".tillr", "state.db"), old, nil, deps); err != nil {
		t.Fatalf("TestCommit failed: %v", err)
	}
	if len(sharded) != 1 {
		t.Fatalf("expected 1 sharded test run, got %d: %v", len(sharded), sharded)
	}
	if !strings.HasSuffix(sharded[0][1], "tools/go-test-shards.py") || sharded[0][2] != "tools/gate" {
		t.Fatalf("unexpected sharded command: %v", sharded[0])
	}
}
