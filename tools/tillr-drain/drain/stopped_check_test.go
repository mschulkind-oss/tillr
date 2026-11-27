package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/checkresult"
)

// A step with no lane root must refuse to write the run request: the path would be
// relative to the working directory, which for a test is the source tree. That stray
// file made the proof's pack-sync-test fail on 2026-11-27.
func TestProcessStepsRunRefusesEmptyLaneRoot(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	s := &ProcessSteps{Store: "unused", RunBin: "unused", Now: func() time.Time { return time.Unix(1, 0) }}
	_, _, err := s.Run(context.Background(), run.Request{RunID: "run-x-a1", ItemID: "item-x"})
	if err == nil {
		t.Fatal("Run with no lane root succeeded")
	}
	if _, statErr := os.Stat(".requests"); !os.IsNotExist(statErr) {
		t.Fatalf("Run wrote %s/.requests despite the empty lane root (stat err %v)", cwd, statErr)
	}
}

// A result the gate says does not belong to the judged tree (its worktree was changed
// by something other than the tip) is not a failure of the tree. It is recorded as an
// error naming the runner, so no trunk fixer is filed for it.
func TestDecodeHeavyVerdictRecordsUnboundFailureAsStoppedByRunner(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"kind": "gate.verdict",
		"payload": map[string]any{"checks": []map[string]any{
			{"check_id": "pack-sync-test", "result": "fail", "memory": "not remembered", "unbound_path": "tools/tillr-drain/drain/.requests/x.json"},
			{"check_id": "plain-fail", "result": "fail"},
			{"check_id": "unbound-pass", "result": "pass", "memory": "not remembered", "unbound_path": "a"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	checks, err := decodeHeavyVerdict(raw)
	if err != nil {
		t.Fatal(err)
	}
	word := func(c HeavyCheck) checkresult.Result {
		return classifyCheck(checkresult.Observation{Check: c.CheckID, Source: checkresult.HeavyCheckWord, Word: c.Result})
	}
	if got := word(checks[0]); got.Outcome == checkresult.Fail {
		t.Fatalf("unbound failure still recorded as a tree failure: %+v", checks[0])
	}
	if !strings.HasPrefix(checks[0].Detail, "stopped by the runner:") || !strings.Contains(checks[0].Detail, "tools/tillr-drain/drain/.requests/x.json") {
		t.Fatalf("detail does not name the runner and the path: %q", checks[0].Detail)
	}
	if got := word(checks[1]); got.Outcome != checkresult.Fail {
		t.Fatalf("a plain failure must stay a failure: %+v", checks[1])
	}
	if checks[2].Result != "pass" {
		t.Fatalf("a pass must stay a pass: %+v", checks[2])
	}
}

func TestHeavyRunnerRecordsUnboundFailureAsStoppedByRunnerAndFilesNoFixer(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t)
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	dir, err := heavyDirectory(ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(map[string]any{
		"kind": "gate.verdict",
		"payload": map[string]any{"checks": []map[string]any{
			{"check_id": "pack-sync-test", "result": "fail", "memory": "not remembered", "unbound_path": "tools/tillr-drain/drain/.requests/x.json"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	fixCalls := 0
	var reported HeavyRun
	runner := HeavyRunner{
		Root:                     f.root,
		Lock:                     loopHeld(t, f.root),
		Store:                    f.storePath,
		Work:                     filepath.Join(f.laneRoot, ".rolling", "worktree"),
		Trunk:                    TrunkRef,
		WithWorktreeRegistryLock: withWorktreeRegistryLock,
		UnlockRegisteredWorktree: unlockRegisteredWorktree,
		Judge: func(context.Context, string, string) ([]HeavyCheck, error) {
			return decodeHeavyVerdict(raw)
		},
		Report: func(r HeavyRun) { reported = r },
		Fix: func(ctx context.Context, sha string, checks []HeavyCheck) error {
			fixCalls++
			return l.fixHeavyChecks(ctx, sha, checks)
		},
	}

	attempt, err := runner.RunAttempt(ctx, tip)
	if err != nil {
		t.Fatalf("RunAttempt: %v", err)
	}
	if len(attempt.Runs) != 1 || len(attempt.Runs[0].Checks) != 1 {
		t.Fatalf("Runs = %+v, want 1 run with 1 check", attempt.Runs)
	}
	got := attempt.Runs[0].Checks[0]
	if got.CheckID != "pack-sync-test" || got.Result != "error" {
		t.Fatalf("recorded check = %+v, want pack-sync-test with Result=error", got)
	}
	if !strings.HasPrefix(got.Detail, "stopped by the runner:") || !strings.Contains(got.Detail, "tools/tillr-drain/drain/.requests/x.json") {
		t.Fatalf("detail does not name the runner and reason: %q", got.Detail)
	}
	if len(reported.Checks) != 1 || reported.Checks[0].Detail != got.Detail {
		t.Fatalf("reported checks = %+v, want detail %q", reported.Checks, got.Detail)
	}
	if fixCalls != 0 {
		t.Fatalf("Fix called %d times for a runner-stopped check, want 0", fixCalls)
	}
	if n := storeCount(t, f, `SELECT COUNT(*) FROM items`); n != 0 {
		t.Fatalf("items count = %d, want 0 (no trunk fixer filed)", n)
	}
}
