package drain

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// A rolling run judges a detached worktree of the tip, never the trunk's own checkout: a
// landing may move the trunk while the run is in flight, and a run that read s.Root would
// judge a tree something else rewrote. The worktree is removed when the run ends, so the
// next run starts from a clean checkout of the tip it was asked about.
func TestARollingRunJudgesADetachedWorktreeOfTheTip(t *testing.T) {
	root := scratchUnderDurable(t)
	runGit(t, root, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(root, "README.md"), "base\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "base")
	tip := runGit(t, root, "rev-parse", "HEAD")

	laneRoot := filepath.Join(root, ".tillr", "lanes")
	record := filepath.Join(scratchUnderDurable(t), "cwd")
	steps := &ProcessSteps{
		Root: root, LaneRoot: laneRoot,
		RollingProgram: executable(t, "pwd > "+record),
	}
	out, err := steps.Rolling(context.Background(), "", tip)
	if err != nil {
		t.Fatalf("the rolling run failed: %v", err)
	}
	if !out.Known || !out.Green {
		t.Fatalf("the rolling run answered %+v, want a known green run", out)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("reading the run's working directory failed: %v", err)
	}
	got := strings.TrimSpace(string(raw))
	want := filepath.Join(laneRoot, ".rolling", "worktree")
	if got != want {
		t.Fatalf("the rolling run ran in %q, want the detached worktree %q", got, want)
	}
	if got == root {
		t.Fatal("the rolling run ran in the trunk's own checkout")
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("the rolling worktree was not removed: %v", err)
	}
}

// A loop stop can kill `git worktree add` mid-checkout, leaving `.rolling/worktree`
// registered with an `initializing` lock (with or without its directory on disk). The next
// rolling run unlocks that path's stale registration under the registry lock and checks out
// the requested tip cleanly.
func TestRollingRecoversAnInitializingLockedDetachedWorktree(t *testing.T) {
	for _, tc := range []struct {
		name              string
		missing, relative bool
	}{
		{name: "directory-present"},
		{name: "directory-missing", missing: true},
		{name: "directory-missing-relative", missing: true, relative: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := scratchUnderDurable(t)
			runGit(t, root, "init", "-q", "-b", "main")
			writeForkFile(t, filepath.Join(root, "README.md"), "base\n")
			runGit(t, root, "add", ".")
			runGit(t, root, "commit", "-qm", "base")
			base := runGit(t, root, "rev-parse", "HEAD")
			writeForkFile(t, filepath.Join(root, "README.md"), "tip\n")
			runGit(t, root, "add", ".")
			runGit(t, root, "commit", "-qm", "tip")
			tip := runGit(t, root, "rev-parse", "HEAD")

			laneRoot := filepath.Join(root, ".tillr", "lanes")
			rolling := filepath.Join(laneRoot, ".rolling", "worktree")
			if err := os.MkdirAll(filepath.Dir(rolling), 0o755); err != nil {
				t.Fatal(err)
			}
			add := []string{"worktree", "add", "--detach"}
			if tc.relative {
				add = append(add, "--relative-paths")
			}
			runGit(t, root, append(add, rolling, base)...)
			runGit(t, root, "worktree", "lock", "--reason", "initializing", rolling)
			if tc.missing {
				if err := os.RemoveAll(rolling); err != nil {
					t.Fatal(err)
				}
			}

			record := filepath.Join(scratchUnderDurable(t), "head")
			steps := &ProcessSteps{
				Root: root, LaneRoot: laneRoot,
				RollingProgram: executable(t, "git rev-parse HEAD > "+record),
			}
			out, err := steps.Rolling(context.Background(), "", tip)
			if err != nil {
				t.Fatalf("recover initializing lock: %v", err)
			}
			if !out.Known || !out.Green {
				t.Fatalf("rolling answered %+v, want known green", out)
			}
			raw, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(raw)); got != tip {
				t.Fatalf("rolling HEAD=%s, want %s", got, tip)
			}
		})
	}
}

// The gate record is append-only: callers must not pass an ignored lock flag. A
// rolling run uses the rolling tier without pretending to wait for a record lock.
func TestTheDefaultRollingProgramOmitsIgnoredLockTimeout(t *testing.T) {
	if !strings.Contains(DefaultRollingProgram, "--tier rolling") {
		t.Fatalf("the default rolling program is %q, want the rolling tier", DefaultRollingProgram)
	}
	if strings.Contains(DefaultRollingProgram, "--lock-timeout") {
		t.Fatalf("the default rolling program passes an ignored flag: %q", DefaultRollingProgram)
	}
}

// A red run says what was red. The failing checks come from the spans the run recorded, so
// the rolling row names them instead of null, the fixer's brief names them, and the fixer's
// fence is the failing check's watched path rather than the whole trunk tree.
func TestARedRollingRunNamesTheChecksItRecorded(t *testing.T) {
	root := scratchUnderDurable(t)
	runGit(t, root, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(root, "README.md"), "base\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "base")
	tip := runGit(t, root, "rev-parse", "HEAD")

	laneRoot := filepath.Join(root, ".tillr", "lanes")
	storePath := filepath.Join(root, ".tillr", "state.db")
	s, err := store.InitFixture(context.Background(), storePath)
	if err != nil {
		t.Fatalf("creating the fixture store failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the fixture store failed: %v", err)
	}

	// The stub is the rolling program: it records one failing span, the way the gate's own
	// `--spans-out` does, and exits 1.
	program := executable(t, `cat > "$TILLR_GATE_SPANS_OUT" <<'JSON'
{"check_spans":[{"check_id":"unit","status":"FAIL","attributes_json":"{\"reads\":[\"tests/untouched_test.go\"],\"watches\":[\"lib/\"],\"writes\":[\"src/\",\"/durable/roster-storybook\",\"../outside\",\"src/../../escape\"]}"}]}
JSON
exit 1`)

	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	steps := &ProcessSteps{
		Root: root, LaneRoot: laneRoot, Store: storePath,
		RollingProgram: program, Now: func() time.Time { return now },
	}
	loop := &Loop{cfg: LoopConfig{
		Root: root, Store: storePath, LaneRoot: laneRoot,
		Steps: steps, Now: func() time.Time { return now }, Report: io.Discard,
	}}
	result := &LoopResult{}
	if err := loop.maybeRolling(context.Background(), tip, true, result); err != nil {
		t.Fatalf("the rolling run failed: %v", err)
	}
	if result.RollingRed != 1 || result.RollingGreen != 0 {
		t.Fatalf("the loop counted green=%d red=%d, want 0 and 1", result.RollingGreen, result.RollingRed)
	}
	fixer := TrunkFixerID(tip)
	paths := fixerPaths(t, storePath, fixer)
	if want := []string{"README.md", "lib/", "src/", "tests/untouched_test.go"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("the fixer's fence is %v, want range paths and declared reads/watches/writes %v", paths, want)
	}
	if got := rollingChecks(t, storePath); !reflect.DeepEqual(got, []string{"unit"}) {
		t.Fatalf("the rolling row names %v, want [unit]", got)
	}
	criteria := fixerCriteria(t, storePath, fixer)
	if len(criteria) != 1 || !strings.Contains(criteria[0], "unit") {
		t.Fatalf("the fixer's brief is %v, want it to name the failing check unit", criteria)
	}
	for _, dropped := range []string{"/durable/roster-storybook", "../outside", "src/../../escape"} {
		if !strings.Contains(criteria[0], dropped) {
			t.Errorf("the fixer's brief omits dropped path %q: %v", dropped, criteria)
		}
	}
}

// executable writes a shell program to run as a step, under the durable scratch root so a
// test never leaves work in /tmp.
func executable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(scratchUnderDurable(t), "rolling-program")
	if err := writeForkSafe(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("writing the rolling program failed: %v", err)
	}
	return path
}

// fixerPaths answers the fence the store holds for an item.
func fixerPaths(t *testing.T, storePath, itemID string) []string {
	t.Helper()
	read := openFixture(t, storePath)
	defer read.Close()
	var raw string
	if err := read.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT allowed_paths FROM items WHERE item_id = ?`, itemID).Scan(&raw)
	}); err != nil {
		t.Fatalf("reading the fixer's fence failed: %v", err)
	}
	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		t.Fatalf("decoding the fixer's fence %q failed: %v", raw, err)
	}
	return paths
}

// fixerCriteria answers the criteria texts the store holds for an item.
func fixerCriteria(t *testing.T, storePath, itemID string) []string {
	t.Helper()
	read := openFixture(t, storePath)
	defer read.Close()
	texts := []string{}
	if err := read.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT text FROM item_criteria WHERE item_id = ?`, itemID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var text string
			if err := rows.Scan(&text); err != nil {
				return err
			}
			texts = append(texts, text)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("reading the fixer's criteria failed: %v", err)
	}
	return texts
}

// rollingChecks answers the failing checks on the newest rolling row.
func rollingChecks(t *testing.T, storePath string) []string {
	t.Helper()
	read := openFixture(t, storePath)
	defer read.Close()
	var raw string
	if err := read.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT failing_checks FROM rolling_runs ORDER BY at DESC LIMIT 1`).Scan(&raw)
	}); err != nil {
		t.Fatalf("reading the rolling row failed: %v", err)
	}
	var checks []string
	if err := json.Unmarshal([]byte(raw), &checks); err != nil {
		t.Fatalf("decoding the rolling row's failing checks %q failed: %v", raw, err)
	}
	return checks
}

// openFixture opens a store read-only, failing the case when it cannot: a store the fixture
// wrote is not a state to report, it is the fixture failing.
func openFixture(t *testing.T, storePath string) *store.Store {
	t.Helper()
	read, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatalf("opening the store failed: %v", err)
	}
	return read
}
