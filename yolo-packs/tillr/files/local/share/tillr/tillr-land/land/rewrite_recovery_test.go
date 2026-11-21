package land_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRecoveredLandingAcrossTrunkRewrite(t *testing.T) {
	r := newRepo(t)
	trunk := r.trunk(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	old, err := trunk.Squash(r.base, r.base, r.head, at)
	if err != nil {
		t.Fatal(err)
	}
	git(t, r.dir, "reset", "--hard", old.Commit)
	const runID = "run-recover"
	git(t, r.dir, "update-ref", land.LandedRefPrefix+runID, old.Commit)

	// The move happened but the receipt did not. Replay the squash onto a new
	// parent, and leave the marker at the old SHA to exercise crash recovery.
	git(t, r.dir, "reset", "--hard", r.base)
	write(t, filepath.Join(r.dir, "remote.txt"), "remote\n")
	git(t, r.dir, "add", "remote.txt")
	git(t, r.dir, "commit", "-m", "remote")
	parent := git(t, r.dir, "rev-parse", "HEAD")
	replacement, err := trunk.Squash(r.base, parent, r.head, at)
	if err != nil {
		t.Fatal(err)
	}
	git(t, r.dir, "reset", "--hard", replacement.Commit)

	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "board.sqlite3"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE trunk_rewrites(old_sha TEXT PRIMARY KEY,new_sha TEXT NOT NULL)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO trunk_rewrites VALUES(?,?)`, old.Commit, replacement.Commit)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	req := land.LandingRequest{
		ItemID: "item-recover", RunID: runID, BaseRef: "refs/heads/main", BaseSHA: r.base, HeadSHA: r.head, At: at,
		Verdict: land.Verdict{VerdictID: "verdict-recover", BaseSHA: r.base, TreeSHA: r.headTre, Result: land.VerdictPass},
	}
	first, err := land.Land(ctx, s, trunk, req)
	if err != nil || !first.Landed() || first.TrunkBefore != parent || first.TrunkAfter != replacement.Commit || first.LandedTree != replacement.Tree {
		t.Fatalf("recovery = %+v, %v; want recovered %s onto %s", first, err, replacement.Commit, parent)
	}
	retry, err := land.Land(ctx, s, trunk, req)
	if err != nil || retry.LandingID != first.LandingID || retry.TrunkAfter != first.TrunkAfter {
		t.Fatalf("retry = %+v, %v; want the same receipt as %+v", retry, err, first)
	}
	if tip := git(t, r.dir, "rev-parse", "HEAD"); tip != replacement.Commit {
		t.Fatalf("recovery moved trunk to %s, want %s", tip, replacement.Commit)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var count int
		if err := r.QueryRow(`SELECT count(*) FROM landings`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("recorded %d landings, want one", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
