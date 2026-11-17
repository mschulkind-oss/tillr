package store

import (
	"context"
	"path/filepath"
	"testing"
)

// No write transaction does checkpoint work: the checkpoint is decided only at a handle's
// close and in the owner's cycle, so WriteTx reads no log, probes no lock and runs no cycle.
// The claim is measured by the checkpoint path's own count (checkpointWork), which nothing
// but a call into that path moves. An allocation count was tried first and was not
// deterministic: the garbage collector empties sync.Pool caches at times the test does not
// choose, and the allocations that refill them land in whichever loop is being measured.
func TestAWriteTransactionDoesNoCheckpointWork(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "")
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "board.sqlite3"), true)
	if err != nil {
		t.Fatal(err)
	}
	write := func(value int) {
		if err := s.WriteTx(ctx, func(tx *Tx) error {
			_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('work', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, value)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	before := checkpointWork.Load()
	// Past CheckpointFrames, so a commit that consulted the log would find it due.
	for i := range CheckpointFrames + 100 {
		write(i)
	}
	if got := checkpointWork.Load() - before; got != 0 {
		t.Errorf("%d write transactions made %d calls into the checkpoint path; a commit must make none", CheckpointFrames+100, got)
	}
	if log := ReadLog(s.path); log.Frames < CheckpointFrames {
		t.Fatalf("the log holds %d frames, short of the %d this test must pass", log.Frames, CheckpointFrames)
	}
	// The positive control: the close is where the checkpoint path runs, so the count moves.
	before = checkpointWork.Load()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if checkpointWork.Load() == before {
		t.Fatal("a close past CheckpointFrames made no call into the checkpoint path, so the count measures nothing")
	}
}
