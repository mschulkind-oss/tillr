package land

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func testRewriteAliasLookup(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "board.sqlite3"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO landings(landing_id,event_seq,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after)
			VALUES('l1',1,'item-1','run-1','v1','landed','base','head','before','old')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertLookup := func(t *testing.T, commit string, want bool) {
		t.Helper()
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			recorded, err := landingRecorded(r, commit)
			if err != nil || recorded != want {
				t.Errorf("landingRecorded(%s) = (%v, %v), want %v", commit, recorded, err, want)
			}
			got, err := storedLanding(r, LandingRequest{ItemID: "item-1", RunID: "run-1", HeadSHA: "head"}, commit, "")
			if err != nil || (got != nil) != want || (got != nil && got.TrunkAfter != "old") {
				t.Errorf("storedLanding(%s) = (%+v, %v), want receipt=%v with original SHA", commit, got, err, want)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("legacy store", func(t *testing.T) {
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			exists, err := hasTrunkRewrites(r)
			if err != nil || exists {
				t.Fatalf("expected no rewrite table, got %v, %v", exists, err)
			}
			if got, err := forwardRewrite(r, "old"); err != nil || got != "old" {
				t.Errorf("forwardRewrite(old) = %q, %v; want old", got, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		assertLookup(t, "old", true)
		assertLookup(t, "new", false)
	})
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE trunk_rewrites(old_sha TEXT PRIMARY KEY,new_sha TEXT NOT NULL)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO trunk_rewrites VALUES('old','middle'),('middle','new')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Run("aliases", func(t *testing.T) {
		assertLookup(t, "old", true)
		assertLookup(t, "new", true)
		assertLookup(t, "unrelated", false)
	})
}
