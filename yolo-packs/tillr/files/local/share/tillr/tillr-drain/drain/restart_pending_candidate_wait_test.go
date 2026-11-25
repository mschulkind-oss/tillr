package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/store"
)

func TestPendingRestartHoldsWaitingCandidatesWithoutClaimLoop(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	if err := os.MkdirAll(filepath.Join(f.root, "config/tillr/workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/single-build@v1.json"),
		`{"name":"single-build","version":1,"stages":[{"id":"build","kind":"agent","seat":"build","access":"write"}],"edges":[]}`)
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "add workflow")

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='single-build@v1' WHERE item_id IN ('item-a','item-b')`)
		return err
	})

	gateProg := executable(t, `#!/bin/sh
echo "unexpected gate start" >&2
exit 1
`)
	steps := detachedGateTestSteps(t, f, time.Now, gateProg)
	var report lockedBuffer
	l := configureDetachedGateLoop(t, f, steps)
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
	l.gatePolicy.LandingStrategy = "after-merge"
	l.cfg.StorePollInterval = 25 * time.Millisecond
	l.cfg.Report = &report

	exe := filepath.Join(t.TempDir(), "tillr-drain-next")
	if err := writeForkSafe(exe, []byte("next build"), 0o755); err != nil {
		t.Fatal(err)
	}
	var execMu sync.Mutex
	var execed []string
	l.cfg.Restart = &RestartConfig{
		Executable: exe,
		Check:      func(context.Context, string) error { return nil },
		Exec: func(path string, argv, env []string) error {
			execMu.Lock()
			execed = append(execed, path)
			execMu.Unlock()
			return nil
		},
	}

	cA := prepareReadyCandidate(t, f, l, "item-a")
	cB := prepareReadyCandidate(t, f, l, "item-b")

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	// File a pending restart and hold restartMechanical.RLock to simulate an
	// active running gate or trunk proof keeping the restart pending.
	if _, err := restart.File(ctx, f.storePath, restart.RoleDrain, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	l.restartMechanical.RLock()
	heldLock := true
	defer func() {
		if heldLock {
			l.restartMechanical.RUnlock()
		}
	}()

	l.completions = make(chan runCompletion, 16)

	// Run several passes while the restart is held pending.
	for i := 0; i < 5; i++ {
		stop, err := l.pass(ctx, &LoopResult{})
		if err != nil {
			t.Fatalf("pass %d while restart held: %v", i+1, err)
		}
		if stop != "" {
			t.Fatalf("pass %d stopped early with %q while restart held", i+1, stop)
		}
		l.examinations.Wait()
	}

	countEvents := func(kind, runID string) int {
		t.Helper()
		var n int
		s := openFixture(t, f.storePath)
		defer s.Close()
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = ? AND object_id = ?`, kind, runID).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}

	for _, c := range []Candidate{cA, cB} {
		claimed := countEvents(examinationClaimedKind, c.RunID)
		examReleased := countEvents(examinationReleasedKind, c.RunID)
		released := countEvents(laneReleasedKind, c.RunID)
		if claimed > 1 {
			t.Errorf("%s (%s): drain.examination-claimed count = %d, want <= 1", c.ItemID, c.RunID, claimed)
		}
		if examReleased > 1 {
			t.Errorf("%s (%s): drain.examination-released count = %d, want <= 1", c.ItemID, c.RunID, examReleased)
		}
		if released > 1 {
			t.Errorf("%s (%s): drain.lane-released count = %d, want <= 1", c.ItemID, c.RunID, released)
		}
	}

	out := report.String()
	if !strings.Contains(out, "waiting for the restart") || !strings.Contains(out, "item-a") || !strings.Contains(out, "item-b") {
		t.Errorf("loop waiting line did not report both candidates waiting for the restart:\n%s", out)
	}
	if n := strings.Count(out, "candidate(s) waiting for the restart"); n != 1 {
		t.Errorf("waiting-for-restart line printed %d times across 5 passes, want 1:\n%s", n, out)
	}

	// Releasing the hold lets the next pass execute the pending restart.
	l.restartMechanical.RUnlock()
	heldLock = false
	stop, err := l.pass(ctx, &LoopResult{})
	if err != nil || stop != StopRestarted {
		t.Fatalf("pass after hold released = %q, %v; want %q, nil", stop, err, StopRestarted)
	}
	execMu.Lock()
	gotExeced := len(execed)
	execMu.Unlock()
	if gotExeced != 1 {
		t.Fatalf("exec count after hold released = %d, want 1", gotExeced)
	}
}

func TestWaitForTriggerWakesWhenRestartAnswered(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	l.cfg.StorePollInterval = 10 * time.Second
	l.pollOnly = true
	l.markRestartPending()

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = l.waitForTrigger(ctx, nil, l.cfg.Now(), &LoopResult{})
	}()

	time.Sleep(30 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("waitForTrigger returned before restart was answered")
	default:
	}

	l.clearRestartPending()
	select {
	case <-done:
	case <-time.After(hangGuard):
		t.Fatal("waitForTrigger did not wake when pending restart was answered")
	}
}
