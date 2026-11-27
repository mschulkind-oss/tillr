package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/event"
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

type conflictLandingSteps struct {
	*ProcessSteps
	t         *testing.T
	storePath string
	root      string
	now       func() time.Time
}

func (s *conflictLandingSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	trunk := strings.TrimSpace(runGit(s.t, s.root, "rev-parse", TrunkRef))
	payload, err := json.Marshal(map[string]any{
		"landing_id":     "land-" + c.RunID,
		"item_id":        c.ItemID,
		"run_id":         c.RunID,
		"result":         LandSquashConflict,
		"base_sha":       c.BaseSHA,
		"head_sha":       c.HeadSHA,
		"trunk_before":   trunk,
		"trunk_after":    trunk,
		"conflict_paths": []string{"CONFLICT (content): Merge conflict in conflict.txt"},
	})
	if err != nil {
		return LandOutcome{}, err
	}
	st, err := store.Open(ctx, s.storePath, false)
	if err != nil {
		return LandOutcome{}, err
	}
	defer st.Close()
	if err := st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       "land.landing",
			ObjectType: "item",
			ObjectID:   &c.ItemID,
			Payload:    payload,
			At:         s.now().UTC(),
		})
		return err
	}); err != nil {
		return LandOutcome{}, err
	}
	return LandOutcome{
		Result:        LandSquashConflict,
		TrunkAfter:    trunk,
		ConflictPaths: []string{"CONFLICT (content): Merge conflict in conflict.txt"},
	}, nil
}

func TestPendingRestartHoldsConflictedGatedCandidate(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	writeForkFile(t, filepath.Join(f.root, "conflict.txt"), "base\n")
	runGit(t, f.root, "add", "conflict.txt")
	runGit(t, f.root, "commit", "-qm", "add conflict base")

	gateProg := executable(t, "#!/bin/sh\necho \"unexpected gate start\" >&2\nexit 1\n")
	baseSteps := detachedGateTestSteps(t, f, time.Now, gateProg)
	steps := &conflictLandingSteps{
		ProcessSteps: baseSteps,
		t:            t,
		storePath:    f.storePath,
		root:         f.root,
		now:          time.Now,
	}
	var report lockedBuffer
	l := configureDetachedGateLoop(t, f, steps)
	l.cfg.Lanes = []string{"lane-1"}
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

	c := prepareReadyCandidate(t, f, l, "item-a")
	// Mark the candidate already gated so the loop takes the landing path.
	if err := appendJudgment(context.Background(), f.storePath, candidateGatedKind, "run", c.RunID, c, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	// Move trunk so conflictFix takes the moved-trunk conflict-repair path.
	writeForkFile(t, filepath.Join(f.root, "conflict.txt"), "trunk-1\n")
	runGit(t, f.root, "add", "conflict.txt")
	runGit(t, f.root, "commit", "-qm", "move trunk to conflict")

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	// File a pending restart and hold it behind an active rollAction (the
	// condition where already-gated candidates still attempt landing while
	// conflict-repair staging is held by the pending roll/restart).
	if _, err := restart.File(ctx, f.storePath, restart.RoleDrain, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	rollStarted := make(chan struct{})
	rollDone := make(chan struct{})
	rollHeld := true
	l.rollAction.submit("test-roll", func(string) {
		close(rollStarted)
		<-rollDone
	})
	<-rollStarted
	defer func() {
		if rollHeld {
			close(rollDone)
			if idle := l.rollAction.idle(); idle != nil {
				<-idle
			}
		}
	}()

	l.completions = make(chan runCompletion, 16)

	// Run several passes while the restart is held pending and trunk is unchanged.
	for i := 0; i < 5; i++ {
		stop, err := l.pass(ctx, &LoopResult{})
		if err != nil {
			t.Fatalf("pass %d while restart held: %v\nreport:\n%s", i+1, err, report.String())
		}
		if stop != "" {
			t.Fatalf("pass %d stopped early with %q while restart held\nreport:\n%s", i+1, stop, report.String())
		}
		l.examinations.Wait()
	}

	countSquashConflicts := func() int {
		t.Helper()
		var n int
		s := openFixture(t, f.storePath)
		defer s.Close()
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'land.landing' AND json_extract(payload, '$.result') = ? AND json_extract(payload, '$.run_id') = ?`, LandSquashConflict, c.RunID).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if got := countSquashConflicts(); got != 1 {
		t.Errorf("land.landing squash-conflict count while restart held = %d, want 1", got)
	}

	countEvents := func(kind string) int {
		t.Helper()
		var n int
		s := openFixture(t, f.storePath)
		defer s.Close()
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = ? AND object_id = ?`, kind, c.RunID).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := countEvents(examinationOwnerKind); got > 2 {
		t.Errorf("drain.examination.owner count while restart held = %d, want <= 2", got)
	}
	if got := countEvents(laneReleasedKind); got > 1 {
		t.Errorf("drain.lane-released count while restart held = %d, want <= 1", got)
	}

	out := report.String()
	if !strings.Contains(out, "waiting for the restart to stage conflict repair") || !strings.Contains(out, "item-a") {
		t.Errorf("loop waiting line did not name item-a as waiting for the restart to stage conflict repair:\n%s", out)
	}
	if n := strings.Count(out, "waiting for the restart to stage conflict repair"); n != 1 {
		t.Errorf("waiting-for-restart conflict repair line printed %d times across 5 passes, want 1:\n%s", n, out)
	}

	// Moving the trunk while the restart is still pending lets the loop try
	// landing once more against the new trunk, then hold again.
	writeForkFile(t, filepath.Join(f.root, "conflict.txt"), "trunk-2\n")
	runGit(t, f.root, "add", "conflict.txt")
	runGit(t, f.root, "commit", "-qm", "move trunk again")

	for i := 0; i < 3; i++ {
		stop, err := l.pass(ctx, &LoopResult{})
		if err != nil {
			t.Fatalf("pass %d after trunk move: %v", i+1, err)
		}
		if stop != "" {
			t.Fatalf("pass %d after trunk move stopped early with %q", i+1, stop)
		}
		l.examinations.Wait()
	}
	if got := countSquashConflicts(); got != 2 {
		t.Errorf("land.landing squash-conflict count after one trunk move = %d, want 2", got)
	}

	// Releasing rollAction lets the next pass execute the pending restart.
	close(rollDone)
	rollHeld = false
	if idle := l.rollAction.idle(); idle != nil {
		<-idle
	}
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

func TestConflictedCandidateNotRelandedWithoutRepairOrTrunkMove(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	writeForkFile(t, filepath.Join(f.root, "conflict.txt"), "base\n")
	runGit(t, f.root, "add", "conflict.txt")
	runGit(t, f.root, "commit", "-qm", "add conflict base")

	gateProg := executable(t, "#!/bin/sh\necho \"unexpected gate start\" >&2\nexit 1\n")
	baseSteps := detachedGateTestSteps(t, f, time.Now, gateProg)
	steps := &conflictLandingSteps{
		ProcessSteps: baseSteps,
		t:            t,
		storePath:    f.storePath,
		root:         f.root,
		now:          time.Now,
	}
	l := configureDetachedGateLoop(t, f, steps)
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
	l.gatePolicy.LandingStrategy = "after-merge"
	l.cfg.StorePollInterval = 25 * time.Millisecond

	c := prepareReadyCandidate(t, f, l, "item-a")
	if err := appendJudgment(context.Background(), f.storePath, candidateGatedKind, "run", c.RunID, c, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "conflict.txt"), "trunk-1\n")
	runGit(t, f.root, "add", "conflict.txt")
	runGit(t, f.root, "commit", "-qm", "move trunk to conflict")

	// Hold conflict-fix staging behind a binary roll (rollBlocksDispatch() == true)
	// without filing a restart in the store.
	l.rollChecking.Add(1)
	rollHeld := true
	defer func() {
		if rollHeld {
			l.rollChecking.Add(-1)
		}
	}()

	l.completions = make(chan runCompletion, 16)
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	for i := 0; i < 5; i++ {
		stop, err := l.pass(ctx, &LoopResult{})
		if err != nil {
			t.Fatalf("pass %d while roll blocks dispatch: %v", i+1, err)
		}
		if stop != "" {
			t.Fatalf("pass %d stopped early with %q", i+1, stop)
		}
		l.examinations.Wait()
	}

	countSquashConflicts := func() int {
		t.Helper()
		var n int
		s := openFixture(t, f.storePath)
		defer s.Close()
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'land.landing' AND json_extract(payload, '$.result') = ? AND json_extract(payload, '$.run_id') = ?`, LandSquashConflict, c.RunID).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if got := countSquashConflicts(); got != 1 {
		t.Fatalf("land.landing squash-conflict count while roll held repair = %d, want 1", got)
	}

	// Even if landCandidate is called directly while trunk and head are unchanged,
	// it routes to conflictFix without calling the lander again.
	planned := mustPlan(t, l)
	if len(planned) != 1 || planned[0].work == nil || planned[0].work.Candidate == nil {
		t.Fatalf("unexpected plan before direct landCandidate: %+v", planned)
	}
	if _, _, err := l.landCandidate(ctx, *planned[0].work.Candidate, &LoopResult{}, &PassTiming{}); !errors.Is(err, errRestartPending) {
		t.Fatalf("direct landCandidate while roll held repair = %v, want errRestartPending", err)
	}
	if got := countSquashConflicts(); got != 1 {
		t.Fatalf("land.landing squash-conflict count after direct landCandidate = %d, want 1", got)
	}

	// Releasing the roll lets the next pass stage conflict repair directly without
	// calling the lander a second time.
	l.rollChecking.Add(-1)
	rollHeld = false

	stop, err := l.pass(ctx, &LoopResult{})
	if err != nil {
		t.Fatalf("pass after roll released: %v", err)
	}
	if stop != "" {
		t.Fatalf("pass after roll released stopped with %q", stop)
	}
	l.examinations.Wait()
	if got := countSquashConflicts(); got != 1 {
		t.Fatalf("land.landing squash-conflict count after repair staged = %d, want 1", got)
	}
}
