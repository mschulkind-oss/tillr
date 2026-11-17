package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// The exit flush waits for records already queued: the sink is stalled while a
// write queues its records, then released, and the flush returns only once the
// delivery goroutine has handed every one of them to the sink.
func TestExitFlushWaitsForQueuedRecords(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "")
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "exit.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	privateSQLiteTraceObserver(t, sqliteTraceQueueLimit)
	block, release, _ := capturedStalledSQLiteStderr(t)
	block()
	t.Setenv("TILLR_SQLITE_TRACE", "1")
	failure := errors.New("exit flush fixture")
	if err := s.WriteTx(context.Background(), func(*Tx) error { return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	t.Setenv("TILLR_SQLITE_TRACE", "")
	if queued := SQLiteTraceDeliveryStatus(); queued.Pending == 0 {
		t.Fatalf("the stalled sink accepted every record, so nothing waited for the flush: %+v", queued)
	}
	release()
	status, err := FlushSQLiteTraceAtExit()
	if err != nil || status.Pending != 0 || status.Delivered != 3 {
		t.Fatalf("the exit flush returned before delivery: %v %+v", err, status)
	}
}

// A sink that never drains bounds the exit flush rather than holding the process.
func TestExitFlushIsBoundedByAStalledSink(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "")
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "stalled.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	privateSQLiteTraceObserver(t, sqliteTraceQueueLimit)
	block, _, _ := capturedStalledSQLiteStderr(t)
	block()
	t.Setenv("TILLR_SQLITE_TRACE", "1")
	if err := s.WriteTx(context.Background(), func(*Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_SQLITE_TRACE", "")
	status, err := FlushSQLiteTraceAtExit()
	if !errors.Is(err, context.DeadlineExceeded) || status.Pending == 0 {
		t.Fatalf("a stalled sink should end the exit flush at its bound with records pending: %v %+v", err, status)
	}
}

// Tracing off for the whole process, the exit flush starts nothing and waits for
// nothing: the observer never started.
func TestExitFlushWithTracingOffIsANoOp(t *testing.T) {
	observer := privateSQLiteTraceObserver(t, sqliteTraceQueueLimit)
	status, err := FlushSQLiteTraceAtExit()
	if err != nil || !status.Complete || observer.started.Load() {
		t.Fatalf("a never-traced process's exit flush did work: %v %+v", err, status)
	}
}
