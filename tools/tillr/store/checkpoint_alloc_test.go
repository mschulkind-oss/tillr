package store

import (
	"context"
	"path/filepath"
	"testing"
)

// The checkpoint policy is decided before every write transaction, so every commit in every
// program pays for it. With the owner's lock file opened once per handle and the pragma run
// only when the decision changes, deciding it costs at most one allocation: a later change
// that reopens the file, resolves its path or formats the pragma per transaction shows here
// as a count, not as a slower benchmark nobody runs.
func TestDecidingTheCheckpointPolicyAddsAtMostOneAllocation(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "")
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "board.sqlite3"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	write := func() {
		if err := s.WriteTx(ctx, func(tx *Tx) error {
			_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('alloc', '1') ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	write()
	decided := testing.AllocsPerRun(200, write)
	s.writable = false // the same transaction without the policy step
	undecided := testing.AllocsPerRun(200, write)
	s.writable = true
	if added := decided - undecided; added > 1 {
		t.Errorf("deciding the checkpoint policy adds %.0f allocations to each write transaction (%.0f with it, %.0f without)", added, decided, undecided)
	}
}
