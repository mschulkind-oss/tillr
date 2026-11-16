package main_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
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

	loop := f.newLoop(t, 1, steps)
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("the loop stopped %q, want %q: %s", result.Stop, drain.StopQueueDrained, result.Detail)
	}

	runID := theRunID(t, f.store)
	index := filepath.Join(traceRoot, "index.db")
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
