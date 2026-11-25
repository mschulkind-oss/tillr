package drain

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type settlingStartSteps struct {
	recordingSteps
	fixture           refusalLoopFixture
	now               func() time.Time
	settling, release chan struct{}
	started           chan string
}

func (s *settlingStartSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if req.ItemID == "item-a" {
		wt := filepath.Join(s.fixture.laneRoot, req.LaneID, req.WorkingDir)
		file := "tools/item-a/work.txt"
		if err := os.MkdirAll(filepath.Dir(filepath.Join(wt, file)), 0755); err != nil {
			return run.Record{}, "", err
		}
		if err := os.WriteFile(filepath.Join(wt, file), []byte("work\n"), 0644); err != nil {
			return run.Record{}, "", err
		}
		for _, args := range [][]string{{"add", file}, {"commit", "-m", "item-a work"}} {
			cmd := exec.CommandContext(ctx, "git", args...)
			cmd.Dir = wt
			if out, err := cmd.CombinedOutput(); err != nil {
				return run.Record{}, "", fmt.Errorf("git %v: %w: %s", args, err, out)
			}
		}
	}
	st, err := store.Open(ctx, s.fixture.storePath, true)
	if err != nil {
		return run.Record{}, "", err
	}
	defer st.Close()
	exit := 0
	stamp := s.now().UTC().Format(time.RFC3339Nano)
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
		State: run.StateExited, ExitCode: &exit, StartedAt: stamp, FinishedAt: stamp, ArtifactRefs: []run.ArtifactRef{}}
	if err := st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) }); err != nil {
		return rec, "", err
	}
	if req.ItemID == "item-b" {
		s.started <- req.LaneID
	}
	return rec, "", nil
}

func (s *settlingStartSteps) Gate(ctx context.Context, candidate Candidate) (GateOutcome, error) {
	if candidate.ItemID == "item-a" {
		close(s.settling)
		select {
		case <-s.release:
		case <-ctx.Done():
			return GateOutcome{}, ctx.Err()
		}
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

type settlingStartStager struct{ root string }

func (s settlingStartStager) Stage(_ context.Context, spec StageSpec) error {
	for _, name := range []string{FenceFile, ItemFenceFile} {
		if err := os.WriteFile(filepath.Join(s.root, spec.LaneID, name), []byte("tools/\n"), 0644); err != nil {
			return err
		}
	}
	return nil
}

func settlingStartFixture(t *testing.T, lanes int, now func() time.Time) (*Loop, *settlingStartSteps) {
	t.Helper()
	t.Setenv("TILLR_LOOP_STORE", "")
	f := newRefusalLoopFixtureWithLanes(t, lanes, "item-a")
	steps := &settlingStartSteps{fixture: f, now: now, settling: make(chan struct{}), release: make(chan struct{}), started: make(chan string, 2)}
	l := f.loop(t, steps, settlingStartStager{f.laneRoot})
	l.cfg.Lanes = l.cfg.Lanes[:lanes]
	l.cfg.Now = now
	l.cfg.Report = io.Discard
	l.cfg.QuestionsCmd = []string{"sh", "-c", `printf '%s' '{"index":{"documents":[]}}'`}
	holdWALKeeper(t, f.storePath)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var loopErr error
	go func() { defer close(done); _, loopErr = l.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if loopErr != nil {
				t.Error(loopErr)
			}
		case <-time.After(hangGuard):
			t.Error("loop did not stop")
		}
	})
	select {
	case <-steps.settling:
	case <-done:
		t.Fatalf("loop exited before settlement: %v", loopErr)
	case <-time.After(hangGuard):
		t.Fatal("item-a did not enter settlement")
	}
	return l, steps
}

func fileStartWhileSettling(t *testing.T, l *Loop, at time.Time) {
	t.Helper()
	s, err := store.Open(context.Background(), l.cfg.Store, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedRefusalItemAt(t, s, "item-b", at)
}

func waitSettlingStart(t *testing.T, steps *settlingStartSteps, want string) {
	t.Helper()
	select {
	case lane := <-steps.started:
		if lane != want {
			t.Fatalf("item-b started on %s, want %s", lane, want)
		}
	case <-time.After(hangGuard):
		t.Fatalf("item-b did not start on %s", want)
	}
}

func waitStartSnapshot(t *testing.T, l *Loop, ready func(startSnapshot) bool) startSnapshot {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		s := l.startView.Load().snapshot()
		if ready(s) {
			return s
		}
		select {
		case <-deadline.C:
			t.Fatal("watcher did not publish the expected snapshot")
		case <-tick.C:
		}
	}
}

func settlingClaimMS(t *testing.T, l *Loop) int64 {
	t.Helper()
	var ms sql.NullInt64
	s, err := store.OpenRead(context.Background(), l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT p.claim_ms FROM drain_passes p JOIN dispatches d ON d.pass_id = p.tick_id WHERE d.item_id='item-b' ORDER BY d.rowid DESC LIMIT 1`).Scan(&ms)
	}); err != nil {
		t.Fatal(err)
	}
	if !ms.Valid || ms.Int64 < 0 {
		t.Fatalf("invalid claim_ms: %+v", ms)
	}
	return ms.Int64
}

func TestStartItemFiledWhileSettlingWithoutWaiting(t *testing.T) {
	l, steps := settlingStartFixture(t, 2, time.Now)
	fileStartWhileSettling(t, l, time.Now())
	waitSettlingStart(t, steps, "lane-2") // Settlement remains blocked until cleanup.
	settlingClaimMS(t, l)
}

func TestStartOneLaneHoldsUntilSettleReleasesLane(t *testing.T) {
	l, steps := settlingStartFixture(t, 1, time.Now)
	fileStartWhileSettling(t, l, time.Now())
	snap := waitStartSnapshot(t, l, func(s startSnapshot) bool { return s.itemsByID["item-b"].ItemID != "" })
	if !snap.occupied["lane-1"] {
		t.Fatal("lane-1 was offered while item-a was settling")
	}
	s, err := store.OpenRead(context.Background(), l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var claims int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE item_id='item-b'`).Scan(&claims)
	}); err != nil {
		t.Fatal(err)
	}
	if claims != 0 {
		t.Fatalf("item-b claimed %d dispatches before lane-1 release", claims)
	}
	close(steps.release)
	waitSettlingStart(t, steps, "lane-1")
}

func TestStartRerankPreservesReadyAtAndClaimMS(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	now := start
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	l, steps := settlingStartFixture(t, 1, clock)
	fileStartWhileSettling(t, l, start)
	snap := waitStartSnapshot(t, l, func(s startSnapshot) bool { return !s.readyAt["item-b"].IsZero() })
	mu.Lock()
	now = start.Add(5 * time.Second)
	mu.Unlock()
	ctx := context.Background()
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.Place(ctx, tx, "item-b", item.Destination{To: "first"}, clock(), "owner", "test")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	reranked := waitStartSnapshot(t, l, func(s startSnapshot) bool {
		return s.lastEventSeq > snap.lastEventSeq && len(s.items) > 0 && s.items[0].id == "item-b"
	})
	if before, after := snap.readyAt["item-b"], reranked.readyAt["item-b"]; !after.Equal(before) {
		t.Fatalf("rerank changed readyAt: %v -> %v", before, after)
	}
	close(steps.release)
	waitSettlingStart(t, steps, "lane-1")
	if ms := settlingClaimMS(t, l); ms != 0 {
		t.Fatalf("claim_ms = %d; the later lane release and claim shared the same clock instant", ms)
	}
}

func TestStartCoordinatorPollsWithoutFilesystemWatcher(t *testing.T) {
	view := newStartView()
	view.storePoll = time.Millisecond
	refreshed := make(chan struct{}, 1)
	defer view.watch(context.Background(), time.Hour, nil, func(_ context.Context, change startWatchChange) (startSnapshot, error) {
		if change == startWatchStore {
			select {
			case refreshed <- struct{}{}:
			default:
			}
		}
		return startSnapshot{}, nil
	})()
	select {
	case <-refreshed:
	case <-time.After(hangGuard):
		t.Fatal("store poll did not wake the fallback watcher")
	}
}

func TestStartPendingPassKeepsFreshOwnerHold(t *testing.T) {
	f, l := triageFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET body_ref='' WHERE item_id='item-request'`)
		return err
	})
	ctx := context.Background()
	at := l.cfg.Now()
	snap, err := readProposedStarts(ctx, f.storePath, at, l.cfg.Policy, l.startViewPass(), l.questions.(*VantageQuestions), startSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	view := newStartView()
	view.current.Store(&snap)
	writes, err := startHoldWrites(view, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.writeHolds(ctx, writes); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		conflicts, err := item.ReadConflicts(tx.Reader())
		if err == nil && conflicts["item-request"].WaitingOn != "owner" {
			t.Errorf("fresh owner hold not recorded: %+v", conflicts["item-request"])
		}
		return err
	})
}

type joiningStartStager struct{ entered, canceled, finish chan struct{} }

func (s joiningStartStager) Stage(ctx context.Context, _ StageSpec) error {
	close(s.entered)
	<-ctx.Done()
	close(s.canceled)
	<-s.finish
	return ctx.Err()
}

func TestStartShutdownJoinsStaging(t *testing.T) {
	t.Setenv("TILLR_LOOP_STORE", "")
	f := newRefusalLoopFixture(t, "item-a")
	stage := joiningStartStager{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	l := f.loop(t, &recordingSteps{}, stage)
	l.cfg.Lanes = l.cfg.Lanes[:1]
	l.cfg.Report = io.Discard
	holdWALKeeper(t, f.storePath)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := l.Run(ctx); done <- err }()
	var finish sync.Once
	defer finish.Do(func() { close(stage.finish) })
	select {
	case <-stage.entered:
	case err := <-done:
		t.Fatalf("loop exited before staging: %v", err)
	case <-ctx.Done():
		t.Fatal("staging did not start")
	}
	cancel()
	deadline := time.After(hangGuard)
	select {
	case <-stage.canceled:
	case <-deadline:
		t.Fatal("staging was not canceled")
	}
	select {
	case err := <-done:
		t.Fatalf("loop returned before staging cleanup: %v", err)
	default:
	}
	finish.Do(func() { close(stage.finish) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-deadline:
		t.Fatal("loop did not stop after staging cleanup")
	}
}
