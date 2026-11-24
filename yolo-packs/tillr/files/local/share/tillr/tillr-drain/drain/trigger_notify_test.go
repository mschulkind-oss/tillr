package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/notify"
	"github.com/mschulkind-oss/tillr/store"
)

func startInProcessRelay(t *testing.T, storePath string) (sockPath string, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- notify.Serve(ctx, notify.ServeConfig{
			StorePath: storePath,
			IdleExit:  hangGuard,
			Ready:     ready,
		})
	}()
	select {
	case sockPath = <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("relay exited early: %v", err)
	case <-time.After(hangGuard):
		cancel()
		t.Fatal("relay did not become ready")
	}
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case <-done:
		case <-time.After(hangGuard):
			t.Fatal("relay did not exit on cancel")
		}
		_ = os.Remove(notify.LockPath(sockPath))
	}
	t.Cleanup(stop)
	return sockPath, stop
}

func buildTillrCLIBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tillr")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join("..", "..", "tillr-cli")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build tillr cli: %v\n%s", err, out)
	}
	return bin
}

func TestWaitForTriggerWakesOnRelayedStoreChange(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", socketRuntimeDir(t))
	f, l := passFixture(t, "item-a")
	l.cfg.StorePollInterval = DefaultFallbackPollInterval
	startInProcessRelay(t, f.storePath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopNotify := l.startTriggerNotify(ctx)
	defer stopNotify()

	var result LoopResult
	woke := make(chan error, 1)
	go func() {
		stop, err := l.waitForTrigger(ctx, nil, l.cfg.Now(), &result)
		if err != nil {
			woke <- err
			return
		}
		if stop != "" {
			woke <- errors.New("unexpected stop: " + stop)
			return
		}
		woke <- nil
	}()

	// Commit a store change; Store.WriteTx publishes queue.changed post-commit.
	st, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seedRefusalItem(t, st, "item-wake")

	select {
	case err := <-woke:
		if err != nil {
			t.Fatalf("waitForTrigger: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitForTrigger did not wake on relayed store-change frame")
	}
}

func TestWaitForTriggerAutoStartsRelayWhenAbsent(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", socketRuntimeDir(t))
	tillrBin := buildTillrCLIBinary(t)

	f, l := passFixture(t, "item-a")
	l.cfg.StorePollInterval = DefaultFallbackPollInterval
	l.cfg.NotifyBin = tillrBin
	l.cfg.NotifyIdleExit = 250 * time.Millisecond

	if sock, ok := notify.ActiveSocketPath(f.storePath); ok {
		t.Fatalf("relay socket unexpectedly present before start: %s", sock)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopNotify := l.startTriggerNotify(ctx)
	defer stopNotify()

	if _, ok := notify.ActiveSocketPath(f.storePath); !ok {
		t.Fatal("startTriggerNotify did not auto-start relay when absent")
	}

	var result LoopResult
	woke := make(chan error, 1)
	go func() {
		stop, err := l.waitForTrigger(ctx, nil, l.cfg.Now(), &result)
		if err != nil {
			woke <- err
			return
		}
		if stop != "" {
			woke <- errors.New("unexpected stop: " + stop)
			return
		}
		woke <- nil
	}()

	st, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seedRefusalItem(t, st, "item-autostart")

	select {
	case err := <-woke:
		if err != nil {
			t.Fatalf("waitForTrigger: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitForTrigger did not wake via auto-started relay")
	}
}

func TestWaitForTriggerFallsBackToPollWhenNoRelay(t *testing.T) {
	cases := []struct {
		name      string
		noNotify  bool
		notifyBin string
	}{
		{name: "no-notify", noNotify: true},
		{name: "relay-unavailable", noNotify: false, notifyBin: filepath.Join(t.TempDir(), "missing-tillr")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_RUNTIME_DIR", socketRuntimeDir(t))
			f, l := passFixture(t, "item-a")
			l.cfg.NoNotify = tc.noNotify
			l.cfg.NotifyBin = tc.notifyBin

			var waited time.Duration
			tick := make(chan time.Time, 1)
			l.triggerTimer = func(d time.Duration) (<-chan time.Time, func()) {
				waited = d
				return tick, func() {}
			}

			var result LoopResult
			woke := make(chan error, 1)
			go func() {
				stop, err := l.waitForTrigger(context.Background(), nil, l.cfg.Now(), &result)
				if err != nil {
					woke <- err
					return
				}
				if stop != "" {
					woke <- errors.New("unexpected stop: " + stop)
					return
				}
				woke <- nil
			}()

			// Fire the fallback poll timer; waitForTrigger must wake cleanly.
			tick <- l.cfg.Now().Add(DefaultFallbackPollInterval)
			select {
			case err := <-woke:
				if err != nil {
					t.Fatalf("waitForTrigger: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("waitForTrigger did not wake on fallback poll timer")
			}
			if waited != DefaultFallbackPollInterval {
				t.Fatalf("fallback poll wait = %s, want %s", waited, DefaultFallbackPollInterval)
			}
			if _, ok := notify.ActiveSocketPath(f.storePath); ok {
				t.Fatal("relay socket unexpectedly created when relay is disabled/unavailable")
			}
		})
	}
}

func TestWaitForTriggerReconnectsAndResyncsAfterRelayRestart(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", socketRuntimeDir(t))
	f, l := passFixture(t, "item-a")
	l.cfg.StorePollInterval = DefaultFallbackPollInterval
	// Prevent auto-start from spawning a subprocess; this test drives the relay in-process.
	l.notifyStartRelay = func(context.Context, string, time.Duration) error {
		return errors.New("relay managed by test")
	}

	_, stopRelay1 := startInProcessRelay(t, f.storePath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopNotify := l.startTriggerNotify(ctx)
	defer stopNotify()

	// Kill relay 1 so the subscriber drops its connection, then start relay 2.
	stopRelay1()
	_, stopRelay2 := startInProcessRelay(t, f.storePath)
	defer stopRelay2()

	var result LoopResult
	woke := make(chan error, 1)
	go func() {
		stop, err := l.waitForTrigger(ctx, nil, l.cfg.Now(), &result)
		if err != nil {
			woke <- err
			return
		}
		if stop != "" {
			woke <- errors.New("unexpected stop: " + stop)
			return
		}
		woke <- nil
	}()

	// Reconnect must trigger an immediate resync wake without any new publish
	// and well before the 15 s fallback poll.
	select {
	case err := <-woke:
		if err != nil {
			t.Fatalf("waitForTrigger: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitForTrigger did not wake immediately on relay reconnect")
	}
}

func TestRefreshSessionHeartbeatDoesNotWakeTrigger(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", socketRuntimeDir(t))
	f, l := passFixture(t, "item-a")
	l.cfg.StorePollInterval = DefaultFallbackPollInterval
	startInProcessRelay(t, f.storePath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopNotify := l.startTriggerNotify(ctx)
	defer stopNotify()

	var result LoopResult
	woke := make(chan error, 1)
	go func() {
		stop, err := l.waitForTrigger(ctx, nil, l.cfg.Now(), &result)
		if err != nil {
			woke <- err
			return
		}
		if stop != "" {
			woke <- errors.New("unexpected stop: " + stop)
			return
		}
		woke <- nil
	}()

	if err := l.refreshSession(ctx, l.cfg.Now().Add(PassInterval)); err != nil {
		t.Fatalf("refreshSession: %v", err)
	}
	select {
	case err := <-woke:
		t.Fatalf("session heartbeat unexpectedly woke waitForTrigger (err=%v)", err)
	case <-time.After(150 * time.Millisecond):
	}

	// A real store mutation must still wake it immediately.
	st, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seedRefusalItem(t, st, "item-after-heartbeat")
	select {
	case err := <-woke:
		if err != nil {
			t.Fatalf("waitForTrigger: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("real store mutation did not wake waitForTrigger")
	}
}

// paritySteps drives a deterministic multi-step drain scenario that records real
// runs, events, and landings rows.
type paritySteps struct {
	recordingSteps
	t         *testing.T
	f         refusalLoopFixture
	now       func() time.Time
	mu        sync.Mutex
	landedIDs []string
	onLanded  func(itemID string)
}

func (s *paritySteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	// Commit a deterministic file inside the item's allowed_paths ("tools/<itemID>/")
	// so examination accepts it as a valid candidate.
	worktree := req.WorkingDir
	if !filepath.IsAbs(worktree) {
		worktree = filepath.Join(s.f.laneRoot, req.LaneID, worktree)
	}
	relFile := filepath.Join("tools", req.ItemID, "change.txt")
	absFile := filepath.Join(worktree, relFile)
	if err := os.MkdirAll(filepath.Dir(absFile), 0o755); err != nil {
		return run.Record{}, "", err
	}
	writeForkFile(s.t, absFile, "content for "+req.ItemID+"\n")
	commitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=Parity Test",
		"GIT_AUTHOR_EMAIL=parity@example.test",
		"GIT_AUTHOR_DATE=2026-11-24T00:00:00Z",
		"GIT_COMMITTER_NAME=Parity Test",
		"GIT_COMMITTER_EMAIL=parity@example.test",
		"GIT_COMMITTER_DATE=2026-11-24T00:00:00Z",
	)
	for _, args := range [][]string{
		{"add", relFile},
		{"commit", "-qm", "feat: " + req.ItemID},
	} {
		cmd := exec.Command("git", append([]string{"-C", worktree}, args...)...)
		cmd.Env = commitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return run.Record{}, "", errors.New("git commit: " + string(out))
		}
	}
	ts := "2026-11-24T00:00:00Z"
	rec := run.Record{
		RunID:        req.RunID,
		ItemID:       req.ItemID,
		LaneID:       req.LaneID,
		Backend:      req.Backend,
		State:        run.StateExited,
		ExitCode:     countPtr(0),
		StartedAt:    ts,
		FinishedAt:   ts,
		ArtifactRefs: []run.ArtifactRef{},
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, "", err
	}
	defer st.Close()
	if err := st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) }); err != nil {
		return rec, "", err
	}
	return rec, "RESULT: done\n", nil
}

func (s *paritySteps) Gate(_ context.Context, _ Candidate) (GateOutcome, error) {
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *paritySteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	commitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=Parity Test",
		"GIT_AUTHOR_EMAIL=parity@example.test",
		"GIT_AUTHOR_DATE=2026-11-24T00:00:00Z",
		"GIT_COMMITTER_NAME=Parity Test",
		"GIT_COMMITTER_EMAIL=parity@example.test",
		"GIT_COMMITTER_DATE=2026-11-24T00:00:00Z",
	)
	for _, args := range [][]string{
		{"merge", "--squash", c.HeadSHA},
		{"commit", "-qm", "land: " + c.ItemID},
	} {
		cmd := exec.Command("git", append([]string{"-C", s.f.root}, args...)...)
		cmd.Env = commitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return LandOutcome{}, errors.New("git land: " + string(out))
		}
	}
	headAfter := runGit(s.t, s.f.root, "rev-parse", "HEAD")
	headTree := runGit(s.t, s.f.root, "rev-parse", "HEAD^{tree}")
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return LandOutcome{}, err
	}
	defer st.Close()
	if err := st.WriteTx(ctx, func(tx *store.Tx) error {
		payload, _ := json.Marshal(map[string]any{
			"item_id":      c.ItemID,
			"run_id":       c.RunID,
			"result":       "landed",
			"base_sha":     c.BaseSHA,
			"head_sha":     c.HeadSHA,
			"trunk_before": c.BaseSHA,
			"trunk_after":  headAfter,
			"gated_tree":   headTree,
			"landed_tree":  headTree,
		})
		seq, err := tx.Append(event.Event{
			Kind:       "land.landing",
			ObjectType: "item",
			ObjectID:   &c.ItemID,
			Payload:    payload,
			At:         time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC),
		})
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO landings
			(item_id, run_id, base_sha, head_sha, trunk_before, trunk_after, gated_tree, landed_tree, verdict_id, result, event_seq)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'landed', ?)`,
			c.ItemID, c.RunID, c.BaseSHA, c.HeadSHA, c.BaseSHA, headAfter, headTree, headTree, "verdict-"+c.RunID, seq)
		return err
	}); err != nil {
		return LandOutcome{}, err
	}
	return LandOutcome{Result: LandLanded}, nil
}

func (s *paritySteps) ItemState(ctx context.Context, id, _ string, state string, _ ...item.WaitingOn) error {
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, state, id)
		return err
	}); err != nil {
		return err
	}
	if state == StateLanded {
		s.mu.Lock()
		s.landedIDs = append(s.landedIDs, id)
		cb := s.onLanded
		s.mu.Unlock()
		if cb != nil {
			cb(id)
		}
	}
	return nil
}

type paritySnapshot struct {
	Events   []parityEvent
	Runs     []parityRun
	Landings []parityLanding
}

type parityEvent struct {
	Kind       string
	ObjectType string
	ObjectID   string
	ItemID     string
	RunID      string
}

type parityRun struct {
	RunID    string
	ItemID   string
	LaneID   string
	Backend  string
	State    string
	ExitCode int
}

type parityLanding struct {
	ItemID     string
	RunID      string
	Result     string
	GatedTree  string
	LandedTree string
}

func snapshotParityStore(t *testing.T, storePath string) paritySnapshot {
	t.Helper()
	s := openFixture(t, storePath)
	defer s.Close()
	var snap paritySnapshot
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		// Events (excluding wall-time-throttled session observation events).
		rows, err := r.Query(`SELECT kind, COALESCE(object_type,''), COALESCE(object_id,''), COALESCE(payload,'{}')
			FROM events WHERE kind NOT IN (?, ?) ORDER BY seq`, LoopPassKind, ProcessCPUKind)
		if err != nil {
			return err
		}
		for rows.Next() {
			var kind, objType, objID, payload string
			if err := rows.Scan(&kind, &objType, &objID, &payload); err != nil {
				rows.Close()
				return err
			}
			var peek struct {
				ItemID string `json:"item_id"`
				RunID  string `json:"run_id"`
			}
			_ = json.Unmarshal([]byte(payload), &peek)
			snap.Events = append(snap.Events, parityEvent{
				Kind:       kind,
				ObjectType: objType,
				ObjectID:   objID,
				ItemID:     peek.ItemID,
				RunID:      peek.RunID,
			})
		}
		if err := rows.Close(); err != nil {
			return err
		}

		// Runs.
		rRows, err := r.Query(`SELECT run_id, item_id, lane_id, backend, state, COALESCE(exit_code, -1)
			FROM runs ORDER BY run_id`)
		if err != nil {
			return err
		}
		for rRows.Next() {
			var rr parityRun
			if err := rRows.Scan(&rr.RunID, &rr.ItemID, &rr.LaneID, &rr.Backend, &rr.State, &rr.ExitCode); err != nil {
				rRows.Close()
				return err
			}
			snap.Runs = append(snap.Runs, rr)
		}
		if err := rRows.Close(); err != nil {
			return err
		}

		// Landings.
		lRows, err := r.Query(`SELECT item_id, run_id, result, COALESCE(gated_tree,''), COALESCE(landed_tree,'')
			FROM landings ORDER BY item_id, run_id`)
		if err != nil {
			return err
		}
		for lRows.Next() {
			var lr parityLanding
			if err := lRows.Scan(&lr.ItemID, &lr.RunID, &lr.Result, &lr.GatedTree, &lr.LandedTree); err != nil {
				lRows.Close()
				return err
			}
			snap.Landings = append(snap.Landings, lr)
		}
		return lRows.Close()
	}); err != nil {
		t.Fatal(err)
	}
	return snap
}

func runParityScenario(t *testing.T, withNotify bool) paritySnapshot {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", socketRuntimeDir(t))
	f := newRefusalLoopFixture(t, "item-a")
	// Stage item-b's brief file on main up front so item-b can be filed dynamically
	// while the loop is waiting in waitForTrigger.
	if err := os.MkdirAll(filepath.Join(f.root, "briefs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "briefs", "item-b.md"), "# item-b\n\nSecond item.\n")
	runGit(t, f.root, "add", "briefs/item-b.md")
	runGit(t, f.root, "commit", "-qm", "docs: add item-b brief")
	// Normalize initial commit on main so gated_tree and landed_tree are identical
	// across the two independent repositories.
	var clockMu sync.Mutex
	now := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	getNow := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}

	itemALanded := make(chan struct{}, 1)
	itemBLanded := make(chan struct{}, 1)
	steps := &paritySteps{
		t:   t,
		f:   f,
		now: getNow,
		onLanded: func(id string) {
			switch id {
			case "item-a":
				select {
				case itemALanded <- struct{}{}:
				default:
				}
			case "item-b":
				select {
				case itemBLanded <- struct{}{}:
				default:
				}
			}
		},
	}
	stager := &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      getNow,
	}
	l := f.loop(t, steps, stager)
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.ExitWhenDrained = false
	l.cfg.StorePollInterval = DefaultFallbackPollInterval
	l.cfg.Now = getNow
	l.cfg.NoNotify = !withNotify

	// Suppress wall-clock heartbeat jitter in the parity test.
	l.heartbeatClock = func() heartbeatTicker {
		return heartbeatTicker{C: make(chan time.Time), Stop: func() {}}
	}

	stepFallback := make(chan struct{}, 8)
	clockStepped := make(chan time.Duration, 8)
	waitingForTrigger := make(chan struct{}, 8)
	if withNotify {
		startInProcessRelay(t, f.storePath)
		l.triggerTimer = func(d time.Duration) (<-chan time.Time, func()) {
			if d != DefaultFallbackPollInterval {
				t.Errorf("notify run fallback timer = %s, want %s", d, DefaultFallbackPollInterval)
			}
			select {
			case waitingForTrigger <- struct{}{}:
			default:
			}
			// Never fires: the notify run must wake strictly via the relay.
			return make(chan time.Time), func() {}
		}
	} else {
		l.triggerTimer = func(d time.Duration) (<-chan time.Time, func()) {
			ch := make(chan time.Time, 1)
			select {
			case waitingForTrigger <- struct{}{}:
			default:
			}
			go func() {
				<-stepFallback
				clockMu.Lock()
				now = now.Add(d)
				tNow := now
				clockMu.Unlock()
				clockStepped <- d
				ch <- tNow
			}()
			return ch, func() {}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		_, err := l.Run(ctx)
		runDone <- err
	}()

	// Step 1: wait for item-a to run, gate, and land.
	select {
	case <-itemALanded:
	case <-time.After(hangGuard):
		cancel()
		<-runDone
		t.Fatalf("withNotify=%v: item-a did not land", withNotify)
	}

	// Wait until the loop enters waitForTrigger idle after landing item-a.
	select {
	case <-waitingForTrigger:
	case <-time.After(hangGuard):
		cancel()
		<-runDone
		t.Fatalf("withNotify=%v: loop did not enter waitForTrigger after item-a", withNotify)
	}

	// Step 2: file item-b while the loop is waiting in waitForTrigger.
	st, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	seedRefusalItem(t, st, "item-b")
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if !withNotify {
		// Step the fake clock by 15 s to fire the fallback poll.
		stepFallback <- struct{}{}
		select {
		case stepped := <-clockStepped:
			if stepped != DefaultFallbackPollInterval {
				cancel()
				<-runDone
				t.Fatalf("stepped fallback interval = %s, want %s", stepped, DefaultFallbackPollInterval)
			}
		case <-time.After(hangGuard):
			cancel()
			<-runDone
			t.Fatal("stepped clock did not fire fallback timer")
		}
	}

	// Wait for item-b to run, gate, and land.
	select {
	case <-itemBLanded:
	case <-time.After(hangGuard):
		cancel()
		<-runDone
		t.Fatalf("withNotify=%v: item-b did not land", withNotify)
	}

	// Wait for the loop to settle back into waitForTrigger, then stop cleanly.
	select {
	case <-waitingForTrigger:
	case <-time.After(hangGuard):
		cancel()
		<-runDone
		t.Fatalf("withNotify=%v: loop did not become idle after item-b", withNotify)
	}
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("withNotify=%v: Loop.Run: %v", withNotify, err)
		}
	case <-time.After(hangGuard):
		t.Fatalf("withNotify=%v: Loop.Run did not stop", withNotify)
	}

	return snapshotParityStore(t, f.storePath)
}

func TestDrainNotifyAndNoNotifyParityUnderSteppedClock(t *testing.T) {
	withNotify := runParityScenario(t, true)
	withoutNotify := runParityScenario(t, false)

	if len(withNotify.Runs) != 2 {
		t.Fatalf("withNotify runs = %d, want 2: %+v", len(withNotify.Runs), withNotify.Runs)
	}
	if len(withNotify.Landings) != 2 {
		t.Fatalf("withNotify landings = %d, want 2: %+v", len(withNotify.Landings), withNotify.Landings)
	}
	if !reflect.DeepEqual(withNotify.Runs, withoutNotify.Runs) {
		t.Fatalf("runs differ:\nwith notify:    %+v\nwithout notify: %+v", withNotify.Runs, withoutNotify.Runs)
	}
	if !reflect.DeepEqual(withNotify.Landings, withoutNotify.Landings) {
		t.Fatalf("landings differ:\nwith notify:    %+v\nwithout notify: %+v", withNotify.Landings, withoutNotify.Landings)
	}
	if !reflect.DeepEqual(withNotify.Events, withoutNotify.Events) {
		t.Fatalf("events differ:\nwith notify:    %+v\nwithout notify: %+v", withNotify.Events, withoutNotify.Events)
	}
}

func TestItemRequestToLoopWakeLatency(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", socketRuntimeDir(t))
	tillrBin := buildTillrCLIBinary(t)

	f, l := passFixture(t, "item-a")
	l.cfg.StorePollInterval = DefaultFallbackPollInterval
	l.cfg.NotifyBin = tillrBin
	l.cfg.NotifyIdleExit = 250 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopNotify := l.startTriggerNotify(ctx)
	defer stopNotify()

	var result LoopResult
	entered := make(chan struct{})
	l.triggerTimer = func(d time.Duration) (<-chan time.Time, func()) {
		close(entered)
		timer := time.NewTimer(d)
		return timer.C, func() { timer.Stop() }
	}
	wokeAt := make(chan time.Time, 1)
	wakeErr := make(chan error, 1)
	go func() {
		stop, err := l.waitForTrigger(ctx, nil, l.cfg.Now(), &result)
		wokeAt <- time.Now()
		if err != nil {
			wakeErr <- err
			return
		}
		if stop != "" {
			wakeErr <- errors.New("unexpected stop: " + stop)
			return
		}
		wakeErr <- nil
	}()
	<-entered

	cmd := exec.Command(tillrBin, "item", "request", "--store", f.storePath, "--title", "Notify wake latency probe")
	cmd.Dir = filepath.Join("..", "..", "..")
	cmd.Stdin = strings.NewReader("Measure time from tillr item request to waitForTrigger wake.\n")
	reqStart := time.Now()
	out, err := cmd.CombinedOutput()
	reqDone := time.Now()
	if err != nil {
		t.Fatalf("tillr item request: %v\n%s", err, out)
	}

	var wakeTime time.Time
	select {
	case wakeTime = <-wokeAt:
		if err := <-wakeErr; err != nil {
			t.Fatalf("waitForTrigger: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitForTrigger did not wake after tillr item request")
	}

	fromRequestStart := wakeTime.Sub(reqStart)
	fromRequestReturn := wakeTime.Sub(reqDone)
	t.Logf("measured tillr item request -> waitForTrigger wake: from_start=%s from_commit_return=%s (fallback_poll=%s)",
		fromRequestStart, fromRequestReturn, DefaultFallbackPollInterval)
	if fromRequestStart >= DefaultFallbackPollInterval {
		t.Fatalf("wake took %s (>= fallback %s)", fromRequestStart, DefaultFallbackPollInterval)
	}
}
