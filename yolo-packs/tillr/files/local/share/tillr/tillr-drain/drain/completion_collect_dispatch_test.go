package drain

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

type notifyDispatchStager struct {
	inner      Stager
	onDispatch func(itemID string)
}

func (s *notifyDispatchStager) Stage(ctx context.Context, spec StageSpec) error {
	if s.onDispatch != nil {
		s.onDispatch(spec.ItemID)
	}
	return s.inner.Stage(ctx, spec)
}

// TestReadyWorkDispatchesBeforeCollectionBacklogCompletes verifies that in actor
// mode, draining a completion and dispatching ready work do not block on an
// ended-item collection backlog, and that the backlog is still collected in full
// once the GC actor finishes.
func TestReadyWorkDispatchesBeforeCollectionBacklogCompletes(t *testing.T) {
	t.Setenv("TILLR_LOOP_STORE", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	endedIDs := []string{"item-ended-1", "item-ended-2", "item-ended-3"}
	f := newRefusalLoopFixture(t, "item-ready")
	holdWALKeeper(t, f.storePath)

	var dispatchOnce sync.Once
	dispatchedCh := make(chan string, 4)
	stager := &notifyDispatchStager{
		inner: &itemContentStager{},
		onDispatch: func(itemID string) {
			if itemID == "item-ready" {
				dispatchOnce.Do(func() { dispatchedCh <- itemID })
			}
		},
	}
	l := f.loop(t, &recordingSteps{}, stager)
	l.cfg.ItemWorktrees = true
	l.cfg.Report = io.Discard
	l.cfg.StorePollInterval = time.Hour

	endedDirs := make([]string, 0, len(endedIDs))
	for i, id := range endedIDs {
		runID := "run-" + id + "-a1-n1"
		itemDir, _, _, _, _ := stageFixtureItemDir(t, f, id, runID)
		endedDirs = append(endedDirs, itemDir)
		closedAt := l.cfg.Now().Add(time.Duration(i+1) * time.Second)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := item.Close(ctx, tx, id, "done elsewhere", "owner", closedAt)
			return err
		})
	}

	// Simulate an in-progress ended-item collection backlog holding endedLaneMu.
	l.endedLaneMu.Lock()
	locked := true
	defer func() {
		if locked {
			l.endedLaneMu.Unlock()
		}
	}()

	// Startup cache seeding must not block on the ended-item collection backlog.
	seedDone := make(chan error, 1)
	go func() {
		seedDone <- l.seedActorCaches(ctx, &LoopResult{})
	}()
	select {
	case err := <-seedDone:
		if err != nil {
			t.Fatalf("seedActorCaches: %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("seedActorCaches blocked on ended-item collection backlog")
	}

	l.actors.Active.Store(true)
	defer l.actors.Active.Store(false)
	stopGC := l.startGCActor(ctx, &LoopResult{})
	defer stopGC()

	// Queue a completion so drainCompletionsCount drains drained > 0 before dispatch.
	l.completions <- runCompletion{
		id:     "run-prior-completed",
		itemID: "",
	}

	passDone := make(chan error, 1)
	result := &LoopResult{}
	go func() {
		_, err := l.passMode(ctx, result)
		passDone <- err
	}()

	select {
	case got := <-dispatchedCh:
		if got != "item-ready" {
			t.Fatalf("dispatched %q, want item-ready", got)
		}
	case err := <-passDone:
		t.Fatalf("passMode exited before dispatching item-ready: err=%v", err)
	case <-time.After(hangGuard):
		t.Fatal("ready work did not dispatch while collection backlog was held")
	}

	select {
	case err := <-passDone:
		if err != nil {
			t.Fatalf("passMode: %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("passMode did not finish after dispatching ready work while collection backlog was held")
	}

	// Confirm the collection backlog is still pending at the moment dispatch completed.
	for _, dir := range endedDirs {
		if _, err := os.Stat(filepath.Join(dir, WorktreeDir)); err != nil {
			t.Fatalf("ended item worktree %s already gone before backlog released: %v", dir, err)
		}
	}

	// Release the backlog lock and verify the GC actor collects every ended
	// directory in full without any manual wake from the test.
	locked = false
	l.endedLaneMu.Unlock()

	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		remaining := 0
		for _, dir := range endedDirs {
			for _, sub := range []string{WorktreeDir, "scratch", "durable"} {
				if _, err := os.Stat(filepath.Join(dir, sub)); !os.IsNotExist(err) {
					remaining++
				}
			}
		}
		if remaining == 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	for _, dir := range endedDirs {
		for _, sub := range []string{WorktreeDir, "scratch", "durable"} {
			target := filepath.Join(dir, sub)
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("ended item artifact %s was not collected after backlog released (err=%v)", target, err)
			}
		}
	}
}

// TestInlinePassDispatchesReadyWorkBeforeCollectionBacklog verifies that in
// inline (non-actor) mode, passMode dispatches ready work before running
// ended-item collection or walking item directories, and still collects the
// full backlog afterward.
func TestInlinePassDispatchesReadyWorkBeforeCollectionBacklog(t *testing.T) {
	t.Setenv("TILLR_LOOP_STORE", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	endedIDs := []string{"item-ended-a", "item-ended-b"}
	f := newRefusalLoopFixture(t, "item-ready")
	holdWALKeeper(t, f.storePath)

	var dispatchOnce sync.Once
	dispatchedCh := make(chan string, 4)
	stager := &notifyDispatchStager{
		inner: &itemContentStager{},
		onDispatch: func(itemID string) {
			if itemID == "item-ready" {
				dispatchOnce.Do(func() { dispatchedCh <- itemID })
			}
		},
	}
	l := f.loop(t, &recordingSteps{}, stager)
	l.cfg.ItemWorktrees = true
	l.cfg.Report = io.Discard

	endedDirs := make([]string, 0, len(endedIDs))
	for i, id := range endedIDs {
		runID := "run-" + id + "-a1-n1"
		itemDir, _, _, _, _ := stageFixtureItemDir(t, f, id, runID)
		endedDirs = append(endedDirs, itemDir)
		closedAt := l.cfg.Now().Add(time.Duration(i+1) * time.Second)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := item.Close(ctx, tx, id, "done elsewhere", "owner", closedAt)
			return err
		})
	}

	// Hold endedLaneMu to represent a collection backlog in flight.
	l.endedLaneMu.Lock()
	locked := true
	defer func() {
		if locked {
			l.endedLaneMu.Unlock()
		}
	}()

	l.completions <- runCompletion{
		id:     "run-prior-inline",
		itemID: "",
	}

	passDone := make(chan error, 1)
	result := &LoopResult{}
	go func() {
		_, err := l.passMode(ctx, result)
		passDone <- err
	}()

	select {
	case got := <-dispatchedCh:
		if got != "item-ready" {
			t.Fatalf("dispatched %q, want item-ready", got)
		}
	case err := <-passDone:
		t.Fatalf("inline passMode exited before dispatching item-ready: err=%v", err)
	case <-time.After(hangGuard):
		t.Fatal("inline passMode did not dispatch ready work while collection backlog was held")
	}

	// Confirm ended directories were still pending when ready work dispatched.
	for _, dir := range endedDirs {
		if _, err := os.Stat(filepath.Join(dir, WorktreeDir)); err != nil {
			t.Fatalf("ended item worktree %s already gone before backlog released: %v", dir, err)
		}
	}

	// Release the backlog lock and verify the pass finishes and collects every ended directory.
	locked = false
	l.endedLaneMu.Unlock()

	select {
	case err := <-passDone:
		if err != nil {
			t.Fatalf("inline passMode: %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("inline passMode did not finish collecting backlog after lock release")
	}

	for _, dir := range endedDirs {
		for _, sub := range []string{WorktreeDir, "scratch", "durable"} {
			target := filepath.Join(dir, sub)
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("ended item artifact %s was not collected in inline mode (err=%v)", target, err)
			}
		}
	}
}

// TestDrainCompletionsSignalsGCAndRecordsSteps verifies that draining a
// completion with ItemWorktrees enabled signals the GC actor instead of running
// collectIdleWorktrees inline, and records meaningful completion_steps_ms keys
// on the loop pass event.
func TestDrainCompletionsSignalsGCAndRecordsSteps(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.ItemWorktrees = true
	l.cfg.LaneRoot = t.TempDir()
	l.sessionID = "session-drain-steps"
	l.sessionInitialized = true

	var ticks atomic.Int64
	base := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	l.cfg.Now = func() time.Time { return base.Add(time.Duration(ticks.Add(1)) * time.Second) }
	pt := newLoopPassTimer(l.cfg.Now)
	l.passTimer = pt
	defer func() { l.passTimer = nil }()

	// Drain any pre-existing GC wake.
	select {
	case <-l.actors.GCWakeChan():
	default:
	}

	// Hold endedLaneMu to prove drainCompletionsCount does not block on it.
	l.endedLaneMu.Lock()
	defer l.endedLaneMu.Unlock()

	l.completions <- runCompletion{
		id:     "run-step-timing",
		itemID: "",
	}

	endPhase := l.startPassPhase("drain_completions")
	drained, stop, err := l.drainCompletionsCount(context.Background(), &LoopResult{})
	endPhase()
	if err != nil || stop != "" || drained != 1 {
		t.Fatalf("drainCompletionsCount = (%d, %q, %v), want (1, \"\", nil)", drained, stop, err)
	}

	select {
	case <-l.actors.GCWakeChan():
	default:
		t.Fatal("drainCompletionsCount did not signal GC wake after draining a completion")
	}

	l.recordLoopPass(context.Background(), pt)
	events := readLoopPassEvents(t, f.storePath)
	if len(events) != 1 {
		t.Fatalf("pass events = %d, want 1", len(events))
	}
	var fact LoopPassFact
	if err := json.Unmarshal(events[0].Payload, &fact); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"finish_run_step", "take_completion", "signal_gc_collect"} {
		if fact.CompletionStepsMS[key] <= 0 {
			t.Fatalf("completion_steps_ms[%q] = %d, want > 0 (steps=%v)", key, fact.CompletionStepsMS[key], fact.CompletionStepsMS)
		}
	}
	if _, ok := fact.CompletionStepsMS["collect_idle_worktrees"]; ok {
		t.Fatalf("collect_idle_worktrees should not run inline in drain_completions: %v", fact.CompletionStepsMS)
	}
	assertLoopPassSum(t, fact)
}
