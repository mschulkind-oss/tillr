package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWriterTraceCorrelatesMigrationAndRollbackWithoutChangingOutcome(t *testing.T) {
	s := writeStore(t)
	logPath := filepath.Join(t.TempDir(), "writer.log")
	capture, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = capture
	defer func() { os.Stderr = original; capture.Close() }()
	// An open of a current store migrates nothing and takes no write lock, so the store
	// forgets its migration first: this open is one that migrates.
	if err := s.ForgetMigrations(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_SQLITE_TRACE", "1")
	migrated, err := store.Open(context.Background(), s.Path(), true)
	if err != nil {
		t.Fatal(err)
	}
	migrated.Close()
	failure := errors.New("trace rollback fixture")
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO meta VALUES('trace-probe','uncommitted')`)
		if err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("trace changed error: %v", err)
	}
	if value, err := s.Meta("trace-probe"); err != nil || value != nil {
		t.Fatalf("trace changed rollback: %v %v", value, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	flushStatus, flushErr := store.FlushSQLiteTrace(ctx)
	cancel()
	if flushErr != nil {
		t.Fatalf("%v: %+v", flushErr, flushStatus)
	}
	if err := capture.Sync(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.Contains(line, `operation="open"`) || strings.Contains(line, `operation="close"`) {
			continue
		}
		if strings.Contains(line, "phase=begin ") || strings.Contains(line, "phase=acquired ") || strings.Contains(line, "phase=end ") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 6 {
		t.Fatalf("want two complete writer spans, got %d: %s", len(lines), raw)
	}
	for i, phase := range []string{"begin", "acquired", "end", "begin", "acquired", "end"} {
		if !strings.Contains(lines[i], "phase="+phase) || !strings.Contains(lines[i], "pid=") || !strings.Contains(lines[i], "tx=") || !strings.Contains(lines[i], "store=") {
			t.Errorf("missing correlation fields: %s", lines[i])
		}
	}
	if !strings.Contains(lines[0], `operation="migrate"`) || !strings.Contains(lines[5], "error_type=") || strings.Contains(string(raw), failure.Error()) {
		t.Fatalf("missing operation/outcome: %s", raw)
	}
	t.Setenv("TILLR_SQLITE_TRACE", "")
	if err := s.WriteTx(context.Background(), func(*store.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := capture.Sync(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(raw) {
		t.Fatal("disabled trace wrote diagnostics")
	}
}
