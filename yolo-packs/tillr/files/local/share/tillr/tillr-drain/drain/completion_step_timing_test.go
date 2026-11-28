package drain

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

// A pass whose collection step waits for the ended-lane lock and then walks the
// lane root records both steps, separately, on the pass event.
func TestCollectIdleWorktreesRecordsLockWaitAndWalkSteps(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.ItemWorktrees = true
	l.cfg.LaneRoot = t.TempDir()
	l.sessionID = "session-steps"
	l.sessionInitialized = true

	var ticks atomic.Int64
	base := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	l.cfg.Now = func() time.Time { return base.Add(time.Duration(ticks.Add(1)) * time.Second) }
	pt := newLoopPassTimer(l.cfg.Now)

	ctx := withPassStepTimer(context.Background(), pt)
	endStep := startPassStep(ctx, "collect_idle_worktrees")
	if _, err := l.collectIdleWorktrees(ctx); err != nil {
		t.Fatal(err)
	}
	endStep()

	for _, key := range []string{"collect_idle_worktrees", "collect_lock_wait", "collect_walk"} {
		if pt.Steps()[key] <= 0 {
			t.Fatalf("step %q not recorded: %v", key, pt.Steps())
		}
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
	if fact.CompletionStepsMS["collect_lock_wait"] <= 0 || fact.CompletionStepsMS["collect_walk"] <= 0 {
		t.Fatalf("completion_steps_ms = %v, want lock wait and walk recorded", fact.CompletionStepsMS)
	}
	assertLoopPassSum(t, fact)
}
