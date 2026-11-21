package drain

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func receiveHandoffCompletion(t *testing.T, l *Loop) runCompletion {
	t.Helper()
	select {
	case c := <-l.completions:
		return c
	case <-time.After(hangGuard):
		t.Fatal("loop did not publish the expected completion")
		return runCompletion{}
	}
}

func TestCompletionHandsOffWorkflowWithoutAnotherFullPass(t *testing.T) {
	f, l, _ := restartGraphFixture(t)
	l.cfg.StorePollInterval = time.Hour // The already queued completion is the only trigger.
	report := &lockedBuffer{}
	l.cfg.Report = report
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := &LoopResult{}
	if stop, err := l.pass(ctx, result); stop != "" || err != nil {
		t.Fatalf("initial pass: %q, %v", stop, err)
	}
	first := receiveHandoffCompletion(t, l)
	l.completions <- first
	if stop, err := l.waitForTrigger(ctx, nil, l.cfg.Now(), result); stop != "" || err != nil {
		t.Fatalf("worker handoff: %q, %v", stop, err)
	}
	if result.Dispatched != 2 {
		t.Fatalf("settled but did not dispatch the next step: %+v", result)
	}
	if !strings.Contains(report.String(), "worker run wall time") {
		t.Fatalf("tick report did not label worker runtime: %s", report.String())
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var finished, advanced, dispatched int64
		for _, check := range []struct {
			kind, id string
			seq      *int64
		}{
			{run.FinishKind, first.id, &finished},
			{nodeAdvancedKind, "run-item-a-a1-n2", &advanced},
			{dispatchStartedKind, "run-item-a-a1-n2", &dispatched},
		} {
			if err := tx.Reader().QueryRow(`SELECT COALESCE(MIN(seq),0) FROM events WHERE kind=? AND object_id=?`, check.kind, check.id).Scan(check.seq); err != nil {
				return err
			}
		}
		if !(0 < finished && finished < advanced && advanced < dispatched) {
			t.Errorf("event order: finish=%d settle=%d next dispatch=%d", finished, advanced, dispatched)
		}
		return nil
	})
	drainOwned(t, l, result)
}

type handoffSyncSteps struct {
	Steps
	onSync func()
}

func (s handoffSyncSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	s.onSync()
	return s.Steps.Sync(ctx)
}

func TestCompletedParentDoesNotRefuseNextDispatch(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	ctx := context.Background()
	result := &LoopResult{}
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatal(err)
	}
	completion := receiveHandoffCompletion(t, l)
	// Deliver during the pass, after its first completion drain. The store
	// already has the node advance that makes the parent's successor eligible.
	l.cfg.Steps = handoffSyncSteps{Steps: steps, onSync: func() { l.completions <- completion }}
	l.syncedSHA = ""
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatal(err)
	}
	if result.Dispatched < 2 {
		t.Fatalf("completion did not permit dispatch: %+v", result)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		err := tx.Reader().QueryRow(`SELECT count(*) FROM events WHERE CAST(payload AS TEXT) LIKE '%parent run%is still active%'`).Scan(&n)
		if err == nil && n != 0 {
			t.Errorf("recorded %d active-parent refusals", n)
		}
		return err
	})
	drainOwned(t, l, result)
}

func TestPendingRestartDoesNotBlockPassesDuringMechanicalWork(t *testing.T) {
	h := newRestartHarness(t, []byte("replacement"), func(context.Context, string) error { return nil })
	h.request(t, 1)
	h.l.restartMechanical.RLock()
	locked := true
	defer func() {
		if locked {
			h.l.restartMechanical.RUnlock()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	result := &LoopResult{}
	syncs := 0
	h.l.cfg.Steps = handoffSyncSteps{Steps: h.l.cfg.Steps, onSync: func() { syncs++ }}
	go func() {
		for i := 0; i < 2; i++ {
			h.l.syncedSHA = ""
			stop, err := h.l.pass(ctx, result)
			if err != nil || stop != "" {
				if err == nil {
					err = Internalf("pass stopped: %s", stop)
				}
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(hangGuard):
		t.Fatal("passes deadlocked behind the restart lock")
	}
	if syncs != 2 || len(h.execed) != 0 {
		t.Fatalf("want two passes and no replacement, got %d syncs, exec=%v", syncs, h.execed)
	}
	h.l.restartMechanical.RUnlock()
	locked = false
	if stop, err := h.l.pass(ctx, result); stop != StopRestarted || err != nil {
		t.Fatalf("deferred restart after mechanical work: %q, %v", stop, err)
	}
}
