package main_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
	_ "modernc.org/sqlite"
)

// TestOneTickIndexesTheRunItRecorded is the acceptance criterion's second half: a tick
// against a scratch store must leave the run it recorded in the trace index, with the real
// `tillr-trace` program building that index. A stub in its place would only prove the loop
// called something, and the defect the acceptance run found was a timeline that rendered
// correctly and truthfully said nothing because no step ever indexed the run.
func TestOneTickIndexesTheRunItRecorded(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	traceRoot := scratch(t, "trace-root")
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	steps.TraceBin = traceIndexer(t)
	steps.TraceRoot = traceRoot

	// The loop asks for the index and runs it in the background, and a stop cancels it, so
	// a loop that stopped at the drained queue could stop before the ingest finished. This
	// loop idles once the queue is drained and is stopped once an ingest has indexed a run.
	index := filepath.Join(traceRoot, "index.db")
	watch := &indexWatch{ProcessSteps: steps, index: index, indexed: make(chan struct{})}
	loop, err := drain.NewLoop(drain.LoopConfig{
		Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{laneName(1)},
		Policy: f.policy, Steps: watch, Stage: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}},
		Now: steps.Now, Report: os.Stderr,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result drain.LoopResult
		err    error
	}
	ended := make(chan outcome, 1)
	go func() {
		result, err := loop.Run(ctx)
		ended <- outcome{result, err}
	}()
	// A loop that never indexes the run leaves this receive waiting, which the suite's own
	// -timeout reports with every goroutine's stack.
	<-watch.indexed
	cancel()
	stopped := <-ended
	if stopped.err != nil {
		t.Fatalf("the loop failed: %v", stopped.err)
	}
	if stopped.result.Stop != drain.StopOperatorStop {
		t.Fatalf("the loop stopped %q, want %q: %s", stopped.result.Stop, drain.StopOperatorStop, stopped.result.Detail)
	}

	runID := theRunID(t, f.store)
	db, err := sql.Open("sqlite", index)
	if err != nil {
		t.Fatalf("opening the trace index %s failed: %v", index, err)
	}
	defer db.Close()
	var got string
	if err := db.QueryRow(`SELECT run_id FROM runs_index WHERE run_id = ?`, runID).Scan(&got); err != nil {
		t.Fatalf("the run %s is not in the trace index at %s: %v", runID, index, err)
	}
	if got != runID {
		t.Fatalf("the trace index names run %q, want %q", got, runID)
	}
}

// indexWatch is the real step set with its index step observed: it signals once an ingest
// has finished with at least one run in the index.
type indexWatch struct {
	*drain.ProcessSteps
	index   string
	indexed chan struct{}
	once    sync.Once
}

func (w *indexWatch) Index(ctx context.Context) error {
	if err := w.ProcessSteps.Index(ctx); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", "file:"+w.index+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var runs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM runs_index`).Scan(&runs); err == nil && runs > 0 {
		w.once.Do(func() { close(w.indexed) })
	}
	return nil
}

// traceIndexer is the real tillr-trace entry point beside this module, not a stub: the
// index has to be the one a later read (the observatory's) actually opens.
func traceIndexer(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "tillr-trace", "tillr-trace"))
	if err != nil {
		t.Fatalf("resolving the tillr-trace program failed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the tillr-trace program is not at %s: %v", path, err)
	}
	return path
}

// theRunID answers the run the tick recorded, read from the store the loop planned from.
func theRunID(t *testing.T, storePath string) string {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatalf("opening the store %s failed: %v", storePath, err)
	}
	defer s.Close()
	var runID string
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT run_id FROM runs ORDER BY started_at DESC LIMIT 1`).Scan(&runID)
	}); err != nil {
		t.Fatalf("reading the run the tick recorded failed: %v", err)
	}
	return runID
}
