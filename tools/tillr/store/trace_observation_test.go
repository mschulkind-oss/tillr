package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

var traceFixtureSequence atomic.Uint64

func captureSQLiteTrace(t *testing.T, enabled bool) (string, func() string) {
	t.Helper()
	restore, err := SetSQLiteTraceSlowSpan(time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	value := ""
	if enabled {
		value = "1"
	}
	t.Setenv("TILLR_SQLITE_TRACE", value)
	path := filepath.Join(t.TempDir(), "trace.log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = file
	// A handle the test closes by defer traces its close after read() returns, so the
	// capture delivers what is queued before it closes the file; a record delivered to a
	// closed file is a sink error in the process-wide observer, and a later test's
	// capture would report it as an incomplete trace.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
		defer cancel()
		_, _ = FlushSQLiteTrace(ctx)
		os.Stderr = original
		file.Close()
	})
	return path, func() string {
		ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
		defer cancel()
		_, _ = FlushSQLiteTrace(ctx)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
}

func awaitSQLiteTrace(t *testing.T, read func() string, contains func(string) bool) {
	t.Helper()
	deadline := time.NewTimer(hangGuard)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if contains(read()) {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("expected trace did not arrive: %s", read())
		}
	}
}

func TestOpenTraceRecordsWALAndMigrationPhases(t *testing.T) {
	_, read := captureSQLiteTrace(t, true)
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "store.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	log := read()
	for operation, phases := range map[string][]string{
		"open":    {"begin", "ping.begin", "ping.end", "wal.begin", "wal.end", "end"},
		"migrate": {"begin", "acquired", "schema.begin", "schema.end", "commit.begin", "commit.end", "end"},
	} {
		for _, phase := range phases {
			if !strings.Contains(log, fmt.Sprintf("operation=%q phase=%s ", operation, phase)) {
				t.Errorf("missing %s/%s: %s", operation, phase, log)
			}
		}
	}
}

func TestSlowMigrationTraceCapturesHeldSchemaWithoutSQLValues(t *testing.T) {
	_, read := captureSQLiteTrace(t, true)
	t.Setenv("TILLR_TRACE_SECRET_CONTROL", "environment-secret-must-not-appear")
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	name := fmt.Sprintf("trace_migration_barrier_%d", traceFixtureSequence.Add(1))
	if err := sqlite.RegisterScalarFunction(name, 1, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		close(entered)
		<-release
		return int64(1), nil
	}); err != nil {
		t.Fatal(err)
	}
	registryMu.Lock()
	previous := append([]ownerDDL(nil), registeredDDL...)
	registeredDDL = append(registeredDDL, ownerDDL{owner: name, ddl: "SELECT " + name + "('sql-secret-must-not-appear');"})
	registryMu.Unlock()
	defer func() { registryMu.Lock(); registeredDDL = previous; registryMu.Unlock() }()
	finished := make(chan error, 1)
	go func() {
		s, err := Open(context.Background(), filepath.Join(filepath.Dir(os.Stderr.Name()), "migration.db"), true)
		if s != nil {
			s.Close()
		}
		finished <- err
	}()
	var finishOnce sync.Once
	var finishErr error
	finish := func() error {
		releaseOnce.Do(func() { close(release) })
		finishOnce.Do(func() { finishErr = <-finished })
		return finishErr
	}
	defer finish()
	<-entered
	awaitSQLiteTrace(t, read, func(log string) bool {
		return strings.Contains(log, "tillr-sqlite-stack ") && strings.Contains(log, `operation="migrate" elapsed=`)
	})
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	log := read()
	if !strings.Contains(log, ".(*Store).migrate") || !strings.Contains(log, "phase=schema.begin") || !strings.Contains(log, "phase=schema.end") {
		t.Fatalf("missing actual migration stage: %s", log)
	}
	for _, secret := range []string{"sql-secret-must-not-appear", "environment-secret-must-not-appear"} {
		if strings.Contains(log, secret) {
			t.Errorf("trace leaked %s", secret)
		}
	}
}

func TestSlowWriterTraceCapturesHolderAndWaitingBegin(t *testing.T) {
	_, read := captureSQLiteTrace(t, true)
	path := filepath.Join(t.TempDir(), "writer.db")
	s, err := Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := Open(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	holder, waiter := make(chan error, 1), make(chan error, 1)
	go func() {
		holder <- s.WriteTx(context.Background(), func(tx *Tx) error {
			close(entered)
			<-release
			_, err := tx.Exec("INSERT INTO meta VALUES('private-key','sql-secret-must-not-appear')")
			return err
		})
	}()
	<-entered
	go func() { waiter <- other.WriteTx(context.Background(), func(*Tx) error { return nil }) }()
	var finishOnce sync.Once
	var holderErr, waiterErr error
	finish := func() {
		releaseOnce.Do(func() { close(release) })
		finishOnce.Do(func() { holderErr = <-holder; waiterErr = <-waiter })
	}
	defer finish()
	// Both the acquired holder and the BEGIN waiter must have independent snapshots.
	awaitSQLiteTrace(t, read, func(log string) bool {
		holders, waiters := map[string]bool{}, map[string]bool{}
		for _, line := range strings.Split(log, "\n") {
			// Match the operation field, not a frame: every dump includes all goroutines.
			if !strings.HasPrefix(line, "tillr-sqlite-stack ") || !strings.Contains(line, `operation="github.com/mschulkind-oss/tillr/store.TestSlowWriterTraceCapturesHolderAndWaitingBegin.`) {
				continue
			}
			for _, part := range strings.Fields(line) {
				if strings.HasPrefix(part, "tx=") {
					if strings.Contains(line, ` stage="acquired" `) {
						holders[part] = true
					}
					if strings.Contains(line, ` stage="begin" `) {
						waiters[part] = true
					}
				}
			}
		}
		for holder := range holders {
			for waiter := range waiters {
				if holder != waiter {
					return true
				}
			}
		}
		return false
	})
	before := read()
	// A writer in this process waits for its turn in the process's write queue
	// (writequeue.go) before it begins; one in another process waits in BEGIN (newTx).
	if !(strings.Contains(before, "newTx") || strings.Contains(before, ".(*writeQueue).enter")) || !strings.Contains(before, ".(*Store).WriteTx") {
		t.Fatalf("missing blocked BEGIN/holder stacks: %s", before)
	}
	finish()
	if holderErr != nil {
		t.Fatal(holderErr)
	}
	if waiterErr != nil {
		t.Fatal(waiterErr)
	}
	if strings.Contains(read(), "sql-secret-must-not-appear") {
		t.Fatal("trace leaked SQL values")
	}
}

func TestSlowTraceDisabledAndErrorSecretsRemainPrivate(t *testing.T) {
	_, read := captureSQLiteTrace(t, false)
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "disabled.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(*Tx) error { time.Sleep(150 * time.Millisecond); return nil }); err != nil {
		t.Fatal(err)
	}
	if log := read(); log != "" {
		t.Fatalf("trace-off produced output: %s", log)
	}
	t.Setenv("TILLR_SQLITE_TRACE", "1")
	secret := errors.New("sql-and-environment-secret-must-not-appear")
	err = s.WriteTx(context.Background(), func(*Tx) error { return secret })
	if !errors.Is(err, secret) {
		t.Fatalf("trace changed callback error: %v", err)
	}
	if log := read(); strings.Contains(log, secret.Error()) || !strings.Contains(log, "error_type=") {
		t.Fatalf("missing private error classification: %s", log)
	}
}
