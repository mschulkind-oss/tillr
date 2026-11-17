package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// With tracing on, every checkpoint reads as itself: the owner's cycle (whose own phases
// hold no SQLite writer) and its two writes (which do) are separate, plainly named spans,
// the cycle says whether the log restarted, the owner's closing cycle is its own span, a
// handle's open records its sidecar and its close is a span of its own, and a close that
// checkpoints a grown log, or the maintenance checkpoint, names itself. An operator
// reading a SQLITE_BUSY's trace can then tell an ordinary write from a restart, and a
// close from a write.
func TestTheTraceNamesTheOwnersCheckpointRestartAndClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	_, read := captureSQLiteTrace(t, true)
	ctx := context.Background()
	face, err := Open(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	write := func() {
		if err := face.WriteTx(ctx, func(tx *Tx) error {
			_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('traced', '1') ON CONFLICT(key) DO UPDATE SET value = meta.value || '1'`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	write()
	owner, err := OwnCheckpoints(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	write()
	if _, err := owner.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	face.Close()
	log := read()
	for operation, phases := range map[string][]string{
		"open":                           {"sidecar.begin", "sidecar.end"},
		"checkpoint-owner.checkpoint":    {"begin", "pin.begin", "pin.end", "checkpoint.begin", "checkpoint.complete", "log.restarted", "end"},
		"checkpoint-owner.pre-write":     {"begin", "acquired", "end"},
		"checkpoint-owner.restart-write": {"begin", "acquired", "end"},
		"checkpoint-owner.close":         {"begin", "pin.begin", "checkpoint.complete", "log.restarted", "owner-lock.released", "close.begin", "close.end", "end"},
		"close":                          {"begin", "log-checkpoint.skipped", "log-checkpoint.not-due", "writer.close.begin", "writer.close.end", "sidecar.close.begin", "sidecar.close.end", "end"},
	} {
		for _, phase := range phases {
			if !strings.Contains(log, fmt.Sprintf("operation=%q phase=%s ", operation, phase)) {
				t.Errorf("missing %s/%s", operation, phase)
			}
		}
	}
	acquired := 0
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, "phase=acquired ") && !strings.Contains(line, `operation="migrate"`) {
			acquired++
		}
	}
	if acquired != 6 {
		t.Errorf("expected 6 traced write transactions (two ordinary, two in each of the owner's cycles), saw %d", acquired)
	}
	// The cycle's outcome carries the log's length, so a log that stopped restarting shows
	// how far it has grown.
	if !strings.Contains(log, "phase=log.restarted ") || !strings.Contains(log, " log_frames=") || !strings.Contains(log, " log_bytes=") {
		t.Error("the cycle's outcome does not carry the log's frames and bytes")
	}
	if t.Failed() {
		t.Log(log)
	}
}

// A close that finds the log grown past CheckpointFrames, and the maintenance checkpoint,
// each name themselves: the close's decision phase, then its own "log-checkpoint" span
// and writes; the maintenance checkpoint's "store.checkpoint" span around the owner's.
func TestTheTraceNamesTheClosingAndMaintenanceCheckpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	_, read := captureSQLiteTrace(t, true)
	ctx := context.Background()
	value := strings.Repeat("x", 4000)
	if err := s.WriteTx(ctx, func(tx *Tx) error {
		for i := 0; i < CheckpointFrames+100; i++ {
			if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, fmt.Sprintf("grown-%d", i), value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Checkpoint(ctx, path); err != nil {
		t.Fatal(err)
	}
	log := read()
	for operation, phases := range map[string][]string{
		"close":                        {"log-checkpoint.due"},
		"log-checkpoint":               {"begin", "pin.begin", "checkpoint.complete", "log.restarted", "end"},
		"log-checkpoint.pre-write":     {"begin", "acquired", "end"},
		"log-checkpoint.restart-write": {"begin", "acquired", "end"},
		"store.checkpoint":             {"begin", "owner.absent", "end"},
		"checkpoint-owner.close":       {"log.restarted"},
	} {
		for _, phase := range phases {
			if !strings.Contains(log, fmt.Sprintf("operation=%q phase=%s ", operation, phase)) {
				t.Errorf("missing %s/%s", operation, phase)
			}
		}
	}
	if t.Failed() {
		t.Log(log)
	}
}

func field(line, key string) string {
	i := strings.Index(line, key)
	if i < 0 {
		return ""
	}
	rest := line[i+len(key):]
	if j := strings.IndexByte(rest, ' '); j >= 0 {
		return rest[:j]
	}
	return rest
}
