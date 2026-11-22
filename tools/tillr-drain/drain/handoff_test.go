package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type handoffSteps struct {
	*workflowSteps
	workerRelease chan struct{}
}

func (s *handoffSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	select {
	case <-s.workerRelease:
		return s.workflowSteps.Run(ctx, req)
	case <-ctx.Done():
		return run.Record{}, "", ctx.Err()
	}
}

func TestIntermediateHandoffDoesNotWaitForSiblingLanding(t *testing.T) {
	f, l, steps := workflowLoopFixture(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.cfg.Now = time.Now
	workerRelease := make(chan struct{})
	l.cfg.Steps = &handoffSteps{workflowSteps: steps, workerRelease: workerRelease}
	var result LoopResult
	if _, err := l.pass(ctx, &result); err != nil {
		t.Fatal(err)
	}

	// Hold the lock a sibling landing takes. The boundary and this
	// intermediate examination do not need to publish anything.
	unlock, err := l.lockTrunkHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	// Leave sweep work due before the next pass, while the same holder
	// still owns the landing lock.
	runGit(t, f.root, "update-ref", pinRef("run-sweep-fixture"), "HEAD")
	close(workerRelease)
	var worker runCompletion
	select {
	case worker = <-l.completions:
	case <-time.After(hangGuard):
		t.Fatal("worker could not close its boundary")
	}
	if worker.outcome.err != nil || worker.outcome.stop != "" {
		t.Fatalf("worker completion: %+v", worker.outcome)
	}
	if _, err := l.takeCompletion(worker, &result); err != nil {
		t.Fatal(err)
	}
	passCtx, stop := context.WithTimeout(ctx, hangGuard)
	defer stop()
	if _, err := l.pass(passCtx, &result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, &result)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=?`, nodeAdvancedKind, "run-item-a-a1-n2").Scan(&count); err != nil {
			return err
		}
		if count != 1 || steps.gates != 0 {
			t.Errorf("successors=%d, gates=%d; want one successor and no gate", count, steps.gates)
		}
		return nil
	})
	spans, err := os.Open(filepath.Join(RunDir(f.root, worker.id), handoffTimingFile))
	if err != nil {
		t.Fatal(err)
	}
	defer spans.Close()
	dec := json.NewDecoder(spans)
	var boundary, examination handoffTiming
	if err := dec.Decode(&boundary); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(&examination); err != nil {
		t.Fatal(err)
	}
	if boundary.Phase != "boundary" || examination.Phase != "examination" || examination.StartMS < boundary.EndMS {
		t.Fatalf("expected boundary before examination: %+v, %+v", boundary, examination)
	}
	if !slices.ContainsFunc(examination.Phases, func(span handoffSpan) bool {
		return span.Phase == "store_write" && span.StartMS >= examination.StartMS && span.EndMS <= examination.EndMS
	}) {
		t.Fatalf("no store write recorded inside examination: %+v", examination)
	}
	if _, err := l.pass(passCtx, &result); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND json_extract(payload,'$.run_id')=?`, run.AllocateKind, "run-item-a-a1-n2").Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("next node allocations=%d, want one before land.lock releases", count)
		}
		return nil
	})
	unlock()
	locked = false
	drainOwned(t, l, &result)
}
