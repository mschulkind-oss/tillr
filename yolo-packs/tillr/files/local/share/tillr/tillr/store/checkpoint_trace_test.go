package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// With tracing on, the checkpoint owner's work reads as itself: the cycle (whose own phases
// hold no SQLite writer) and the owner's two writes (which do) are separate, plainly named
// spans, the cycle says whether the log restarted,
// the closing checkpoint is its own span, and every write transaction records the
// checkpoint policy it committed under before it records "acquired". An operator reading a
// SQLITE_BUSY's trace can then tell an ordinary write from the owner's restart.
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
	defer face.Close()
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
	log := read()
	for operation, phases := range map[string][]string{
		"checkpoint-owner.checkpoint":    {"begin", "pin.begin", "pin.end", "checkpoint.begin", "checkpoint.complete", "log.restarted", "end"},
		"checkpoint-owner.pre-write":     {"begin", "autocheckpoint.off", "acquired", "end"},
		"checkpoint-owner.restart-write": {"begin", "autocheckpoint.off", "acquired", "end"},
		"checkpoint-owner.close":         {"begin", "owner-lock.released", "checkpoint.begin", "checkpoint.complete", "close.begin", "close.end", "end"},
	} {
		for _, phase := range phases {
			if !strings.Contains(log, fmt.Sprintf("operation=%q phase=%s ", operation, phase)) {
				t.Errorf("missing %s/%s", operation, phase)
			}
		}
	}
	for _, phase := range []string{"autocheckpoint.default", "autocheckpoint.off"} {
		if !strings.Contains(log, `TheTraceNamesTheOwnersCheckpointRestartAndClose.func1" phase=`+phase+" ") {
			t.Errorf("an ordinary write did not record its policy %s", phase)
		}
	}
	// Each span's policy phase comes before its "acquired": the policy is never part of the
	// time a writer held SQLite's write lock.
	policy, acquired := map[string]bool{}, 0
	for _, line := range strings.Split(log, "\n") {
		tx := field(line, "tx=")
		switch {
		case strings.Contains(line, "phase=autocheckpoint."):
			policy[tx] = true
		case strings.Contains(line, "phase=acquired ") && !strings.Contains(line, `operation="migrate"`):
			acquired++
			if !policy[tx] {
				t.Errorf("a write transaction recorded acquired before its checkpoint policy: %s", line)
			}
		}
	}
	if acquired != 4 {
		t.Errorf("expected 4 traced write transactions (two ordinary, the owner's two), saw %d", acquired)
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
