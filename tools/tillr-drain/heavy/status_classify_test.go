package heavy

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/checkresult"
)

func TestReadStatusUsesClassifier(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	dir, err := Directory(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runPayload, err := json.Marshal(HeavyRun{
		Result: "red",
		Checks: []HeavyCheck{
			{CheckID: "chk-fail-override", Result: "pass"},
			{CheckID: "chk-unavail-override", Result: "pass"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "heavy.json"), runPayload, 0o644); err != nil {
		t.Fatal(err)
	}

	prev := classifyCheck
	t.Cleanup(func() { classifyCheck = prev })
	var seen []checkresult.Observation
	classifyCheck = func(o checkresult.Observation) checkresult.Result {
		seen = append(seen, o)
		switch o.Check {
		case "chk-fail-override":
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		case "chk-unavail-override":
			return checkresult.Result{Outcome: checkresult.Unavailable, Detail: checkresult.DetailUnavailable}
		}
		return checkresult.Classify(o)
	}

	status, err := ReadStatus(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.FailingChecks) != 1 || status.FailingChecks[0].CheckID != "chk-fail-override" {
		t.Fatalf("FailingChecks = %+v, want [chk-fail-override]", status.FailingChecks)
	}
	if len(status.UnavailableChecks) != 1 || status.UnavailableChecks[0].CheckID != "chk-unavail-override" {
		t.Fatalf("UnavailableChecks = %+v, want [chk-unavail-override]", status.UnavailableChecks)
	}
	if len(seen) != 2 || seen[0].Source != checkresult.HeavyCheckWord || seen[1].Source != checkresult.HeavyCheckWord {
		t.Fatalf("observations = %+v, want HeavyCheckWord for both checks", seen)
	}
}

func TestReadStatusReportsLiveRunningProofOnlyWhileLocked(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	tip, err := GitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := Directory(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	faultPayload, _ := json.Marshal(HeavyRun{
		Result: "unknown",
		Detail: "cleanup not acknowledged; checkout retained",
		Checks: []HeavyCheck{},
	})
	if err := os.WriteFile(filepath.Join(dir, "heavy.json"), faultPayload, 0o644); err != nil {
		t.Fatal(err)
	}
	runningPayload, _ := json.Marshal(HeavyRunning{
		TipSHA:    tip,
		StartedAt: time.Date(2026, 11, 22, 20, 32, 0, 0, time.UTC),
	})
	if err := os.WriteFile(filepath.Join(dir, RunningFile), runningPayload, 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, RunningLockFile), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	// Without an exclusive lock holder, leftover running files do not claim a live proof.
	status, err := ReadStatus(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if status.Result != "unknown" || status.Detail != "cleanup not acknowledged; checkout retained" {
		t.Fatalf("unlocked status = %+v, want prior unknown fault", status)
	}

	// Once the exclusive lock is held, ReadStatus reports the running proof instead.
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	status, err = ReadStatus(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if status.Result != "running" || status.Detail != "proving "+tip[:12] {
		t.Fatalf("locked status = %+v, want running proving %s", status, tip[:12])
	}
}
