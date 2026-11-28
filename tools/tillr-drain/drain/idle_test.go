package drain

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/bgactors"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestDefaultWaitReportsFiveHoldsOnceAcrossOneHundredIdlePasses(t *testing.T) {
	f, l := passFixture(t, "held-0", "held-1", "held-2", "held-3", "held-4", "held-5", "held-6")
	l.cfg.ExitWhenDrained = false
	l.cfg.StorePollInterval = time.Millisecond
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})
	var report bytes.Buffer
	l.cfg.Report = &report
	var result LoopResult
	// Startup is not an idle pass: sync reports once before waiting begins.
	if _, err := l.refreshDocuments(context.Background(), &result); err != nil {
		t.Fatal(err)
	}
	report.Reset()
	pass := func() {
		t.Helper()
		stop, err := l.pass(context.Background(), &result)
		if err != nil || stop != "" {
			t.Fatalf("default idle pass stopped: %s / %v", stop, err)
		}
	}
	pass()
	first := report.String()
	if strings.Count(first, "\n") != 1 || !strings.Contains(first, "7 held item(s)") || !strings.Contains(first, "waiting for a trigger") {
		t.Fatalf("waiting summary: %q", first)
	}
	for i := 0; i < 7; i++ {
		present := strings.Contains(first, fmt.Sprintf("held-%d (%s)", i, ReasonBlocked))
		if present != (i < 5) {
			t.Fatalf("summary must name only the first five holds: %q", first)
		}
	}
	for i := 0; i < 100; i++ {
		pass()
	}
	if report.String() != first {
		t.Fatalf("100 idle passes added output: %q", report.String()[len(first):])
	}
	// A reason changing beyond the five printed examples is still a changed queue.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = 'held-6'`, StateLanded)
		return err
	})
	pass()
	if strings.Count(report.String(), "\n") != 2 {
		t.Fatalf("changed hold did not produce a fresh summary: %q", report.String())
	}
}

func TestIdleLoopDoesNotReplanUnchangedHeldItems(t *testing.T) {
	const heldCount = 60
	ids := make([]string, heldCount)
	for i := range ids {
		ids[i] = fmt.Sprintf("held-%03d", i)
	}
	f, l := passFixture(t, ids...)
	l.cfg.ExitWhenDrained = false
	l.cfg.NoNotify = true
	l.cfg.StorePollInterval = time.Millisecond
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})
	var report bytes.Buffer
	l.cfg.Report = &report
	var result LoopResult
	ctx := context.Background()
	if _, err := l.refreshDocuments(ctx, &result); err != nil {
		t.Fatal(err)
	}
	report.Reset()

	var planCalls atomic.Int64
	planDefinitionCalls.Store(&planCalls)
	t.Cleanup(func() { planDefinitionCalls.Store(nil) })

	pass := func() {
		t.Helper()
		stop, err := l.pass(ctx, &result)
		if err != nil || stop != "" {
			t.Fatalf("idle pass stopped: %s / %v", stop, err)
		}
	}

	// First idle pass plans once and reports the held summary.
	pass()
	if got := planCalls.Load(); got != 1 {
		t.Fatalf("after first idle pass: planDefinition calls = %d, want 1", got)
	}
	if !strings.Contains(report.String(), fmt.Sprintf("%d held item(s)", heldCount)) {
		t.Fatalf("waiting summary = %q, want %d held item(s)", report.String(), heldCount)
	}

	// Repeated idle passes over an unchanged store do not re-plan.
	for i := 0; i < 20; i++ {
		pass()
	}
	if got := planCalls.Load(); got != 1 {
		t.Fatalf("after 20 unchanged idle passes: planDefinition calls = %d, want 1", got)
	}

	// Session telemetry events alone do not trigger a re-plan.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		sess := "sess-idle"
		_, err := tx.Append(event.Event{
			Kind:       LoopPassKind,
			ObjectType: "session",
			ObjectID:   &sess,
			Payload:    []byte(`{"session_id":"sess-idle","wall_ms":1,"phases_ms":{"other":1}}`),
			At:         l.cfg.Now().UTC(),
		})
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{
			Kind:       CheckpointNotRestartedKind,
			ObjectType: "session",
			ObjectID:   &sess,
			Payload:    []byte(`{"log_frames":10,"log_bytes":40960,"consecutive_cycles":1,"session_id":"sess-idle"}`),
			At:         l.cfg.Now().UTC(),
		})
		return err
	})
	for i := 0; i < 5; i++ {
		pass()
	}
	if got := planCalls.Load(); got != 1 {
		t.Fatalf("after telemetry events: planDefinition calls = %d, want 1", got)
	}

	// A changed hold wakes the loop to plan again, then returns to quiet idle.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, StateLanded, ids[heldCount-1])
		return err
	})
	pass()
	if got := planCalls.Load(); got != 2 {
		t.Fatalf("after changed hold: planDefinition calls = %d, want 2", got)
	}
	pass()
	if got := planCalls.Load(); got != 2 {
		t.Fatalf("after settling post-change: planDefinition calls = %d, want 2", got)
	}

	// A new item wakes the loop to plan again.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		now := l.cfg.Now().UTC().UnixMilli()
		_, err := tx.Exec(`INSERT INTO items(item_id, state, title, rank, updated_at) VALUES (?, ?, ?, ?, ?)`,
			"held-new", StateBlocked, "new held item", "zzzz", now)
		return err
	})
	pass()
	if got := planCalls.Load(); got != 3 {
		t.Fatalf("after new item: planDefinition calls = %d, want 3", got)
	}

	// A trunk move wakes the loop to plan again.
	writeForkFile(t, filepath.Join(f.root, "idle-move.txt"), "moved\n")
	runGit(t, f.root, "add", "idle-move.txt")
	runGit(t, f.root, "commit", "-qm", "move trunk")
	pass()
	if got := planCalls.Load(); got != 4 {
		t.Fatalf("after trunk move: planDefinition calls = %d, want 4", got)
	}

	// A finished run wakes the loop to plan again.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		now := l.cfg.Now().UTC().UnixMilli()
		_, err := tx.Exec(`INSERT OR IGNORE INTO lanes(lane_id, state) VALUES (?, ?)`, "lane-1", "free")
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO runs(run_id, item_id, lane_id, backend, state, started_at, finished_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"run-idle-finished", ids[0], "lane-1", "stub", "exited", now-1000, now)
		return err
	})
	pass()
	if got := planCalls.Load(); got != 5 {
		t.Fatalf("after finished run: planDefinition calls = %d, want 5", got)
	}
}

type idleWaitSignal struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	waiting chan struct{}
	once    sync.Once
}

func (s *idleWaitSignal) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.buf.Write(p)
	if strings.Contains(s.buf.String(), "waiting for a trigger") {
		s.once.Do(func() { close(s.waiting) })
	}
	return n, err
}

func TestIdleLoopRunDoesNotReplanUntilChange(t *testing.T) {
	const heldCount = 60
	ids := make([]string, heldCount)
	for i := range ids {
		ids[i] = fmt.Sprintf("held-%03d", i)
	}
	f := newRefusalLoopFixture(t, ids...)
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	l.cfg.Policy.Backends = map[string]Backend{"fixture": {Adapter: "fixture"}}
	l.cfg.ExitWhenDrained = false
	l.cfg.NoNotify = true
	l.cfg.StorePollInterval = 10 * time.Millisecond
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})

	sig := &idleWaitSignal{waiting: make(chan struct{})}
	l.cfg.Report = sig

	var planCalls atomic.Int64
	planDefinitionCalls.Store(&planCalls)
	t.Cleanup(func() { planDefinitionCalls.Store(nil) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := l.Run(ctx)
		done <- err
	}()

	select {
	case <-sig.waiting:
	case err := <-done:
		t.Fatalf("loop exited before reaching idle wait: %v", err)
	case <-time.After(hangGuard):
		t.Fatal("timed out waiting for loop to enter idle wait")
	}

	settledCalls := planCalls.Load()
	if settledCalls == 0 {
		t.Fatal("expected at least one planDefinition call before entering idle wait")
	}

	// Across many fallback poll intervals with no store change, planDefinition must not run again.
	time.Sleep(150 * time.Millisecond)
	if got := planCalls.Load(); got != settledCalls {
		t.Fatalf("planDefinition ran while idle with unchanged store: got %d, want %d", got, settledCalls)
	}

	// A changed hold wakes the loop to plan again.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, StateLanded, ids[0])
		return err
	})
	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		if planCalls.Load() > settledCalls {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := planCalls.Load(); got <= settledCalls {
		t.Fatalf("changed hold did not wake idle Loop.Run to re-plan: got %d, want > %d", got, settledCalls)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Loop.Run returned error on cancel: %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("timed out waiting for Loop.Run to stop")
	}
}

func TestIdleLoopWithHeldItemsRunsRollingCheckOnIntervalAndTrunkMove(t *testing.T) {
	f, l := passFixture(t, "held-a", "held-b")
	steps := &unknownDocumentRollingSteps{}
	l.cfg.Steps = steps
	l.cfg.ExitWhenDrained = false
	l.cfg.NoNotify = true
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})
	at := l.cfg.Now()
	l.cfg.Now = func() time.Time { return at }
	l.triggerTimer = func(time.Duration) (<-chan time.Time, func()) {
		ch := make(chan time.Time, 1)
		ch <- at
		return ch, func() {}
	}

	var planCalls atomic.Int64
	planDefinitionCalls.Store(&planCalls)
	t.Cleanup(func() { planDefinitionCalls.Store(nil) })

	var result LoopResult
	pass := func(wantRolling int, wantPlan int64) {
		t.Helper()
		if stop, err := l.pass(context.Background(), &result); err != nil || stop != "" {
			t.Fatalf("idle pass: %s / %v", stop, err)
		}
		if steps.rollingCalls != wantRolling {
			t.Fatalf("rolling checks = %d, want %d", steps.rollingCalls, wantRolling)
		}
		if got := planCalls.Load(); got != wantPlan {
			t.Fatalf("planDefinition calls = %d, want %d", got, wantPlan)
		}
	}

	// First pass runs the rolling check and plans once, then idle polls skip both.
	pass(1, 1)
	pass(1, 1)
	pass(1, 1)

	// Advancing by RollingEvery runs the rolling check once, then skips polls between.
	at = at.Add(RollingEvery)
	pass(2, 2)
	pass(2, 2)
	pass(2, 2)

	// Advancing again runs the rolling check once more, then skips polls between.
	at = at.Add(RollingEvery)
	pass(3, 3)
	pass(3, 3)

	// Moving the trunk tip runs the rolling check once, then skips polls between.
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "new tip while idle")
	pass(4, 4)
	pass(4, 4)
}

func TestIdleCaptureRejectsTrunkMoveSinceDocumentSync(t *testing.T) {
	f, l := passFixture(t, "held-a")
	l.cfg.ExitWhenDrained = false
	l.cfg.NoNotify = true
	l.cfg.StorePollInterval = time.Millisecond
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})
	var result LoopResult
	ctx := context.Background()
	synced, err := l.refreshDocuments(ctx, &result)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a commit landing on trunk after refreshDocuments and before
	// planNothing/captureIdleState finishes entering trigger wait.
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "commit during enter-wait")
	moved := runGit(t, f.root, "rev-parse", TrunkRef)
	if moved == synced {
		t.Fatalf("expected trunk to advance beyond %s", synced)
	}
	var triggerWaited bool
	l.triggerTimer = func(d time.Duration) (<-chan time.Time, func()) {
		triggerWaited = true
		ch := make(chan time.Time, 1)
		ch <- l.cfg.Now()
		return ch, func() {}
	}
	stop, err := l.planNothing(ctx, []Decision{{ItemID: "held-a", Action: ActionHold, Reason: ReasonBlocked}}, nil, l.cfg.Now(), &result)
	if err != nil || stop != "" {
		t.Fatalf("planNothing: stop=%q err=%v", stop, err)
	}
	if l.idleState != nil {
		t.Fatalf("captured idle state for unsynced trunk %s (synced %s)", moved, synced)
	}
	if triggerWaited {
		t.Fatal("planNothing entered trigger wait even though trunk moved since refreshDocuments")
	}
	if stop, err := l.pass(ctx, &result); err != nil || stop != "" {
		t.Fatalf("next pass: stop=%q err=%v", stop, err)
	}
	if result.SyncedSHA != moved {
		t.Fatalf("next pass SyncedSHA = %s, want %s", result.SyncedSHA, moved)
	}
}

func TestPlanNothingWithActiveActorsWakesSyncAndWaitsWhenTrunkMoved(t *testing.T) {
	f, l := passFixture(t, "held-a")
	l.cfg.ExitWhenDrained = false
	l.cfg.NoNotify = true
	l.cfg.StorePollInterval = DefaultFallbackPollInterval
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})
	var result LoopResult
	ctx := context.Background()
	synced, err := l.refreshDocuments(ctx, &result)
	if err != nil {
		t.Fatal(err)
	}
	l.actors.Ensure()
	l.actors.Active.Store(true)
	t.Cleanup(func() { l.actors.Active.Store(false) })

	runGit(t, f.root, "commit", "--allow-empty", "-qm", "commit after sync")
	moved := runGit(t, f.root, "rev-parse", TrunkRef)
	if moved == synced {
		t.Fatalf("expected trunk to advance beyond %s", synced)
	}
	l.actors.SetCachedTrunk(bgactors.TrunkCache{Tip: moved, Valid: true})
	var triggerWaited, syncWokenBeforeWait bool
	l.triggerTimer = func(d time.Duration) (<-chan time.Time, func()) {
		triggerWaited = true
		select {
		case <-l.actors.SyncWakeChan():
			syncWokenBeforeWait = true
		default:
		}
		ch := make(chan time.Time, 1)
		ch <- l.cfg.Now()
		return ch, func() {}
	}
	stop, err := l.planNothing(ctx, []Decision{{ItemID: "held-a", Action: ActionHold, Reason: ReasonBlocked}}, nil, l.cfg.Now(), &result)
	if err != nil || stop != "" {
		t.Fatalf("planNothing: stop=%q err=%v", stop, err)
	}
	if l.idleState != nil {
		t.Fatalf("captured idle state for unsynced trunk %s (synced %s)", moved, synced)
	}
	if !triggerWaited {
		t.Fatal("planNothing returned without waiting while actorsActive; passMode will spin without inline sync")
	}
	if !syncWokenBeforeWait {
		t.Fatal("planNothing did not wake sync actor before waiting when trunk moved since sync")
	}
}

func TestIdleUnchangedIgnoresTelemetryAndWakesOnTrunkOrDocsChange(t *testing.T) {
	f, l := passFixture(t, "held-a")
	l.cfg.ExitWhenDrained = false
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})
	l.actors.Active.Store(true)
	t.Cleanup(func() { l.actors.Active.Store(false) })
	l.actors.SetCachedTrunk(bgactors.TrunkCache{Tip: "abc", Valid: true})

	ctx := context.Background()
	l.idleState = l.captureIdleState(ctx, nil, l.cfg.Now().UTC())
	if l.idleState == nil {
		t.Fatal("expected non-nil idleState")
	}

	// Recording an idle telemetry event must keep idleUnchanged true.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		sid := "session-1"
		_, err := tx.Append(event.Event{
			Kind:       LoopPassKind,
			ObjectType: "session",
			ObjectID:   &sid,
			Payload:    []byte(`{}`),
			At:         l.cfg.Now().UTC(),
		})
		return err
	})
	if !l.idleUnchanged(ctx, l.cfg.Now().UTC()) {
		t.Fatal("idleUnchanged returned false when only telemetry event was appended")
	}

	// Any trunk tip change in trunkCache must wake the idle loop.
	l.actors.SetCachedTrunk(bgactors.TrunkCache{Tip: "def", Valid: true})
	if l.idleUnchanged(ctx, l.cfg.Now().UTC()) {
		t.Fatal("idleUnchanged returned true when cached trunk tip changed")
	}
}
