package land

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRecordReplayLandingBindsVerdictAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "board.sqlite3"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		v := Verdict{VerdictID: "v", BaseSHA: "base", TreeSHA: "other", Result: VerdictPass}
		record := func(tip string) (Landing, error) {
			return RecordReplayLanding(ctx, tx, "sync-remote-fixture", "run-sync", "before", tip, "worker-head", "repair", "base", "tree", v, time.Now())
		}
		if _, err := record("tip"); err == nil {
			t.Error("accepted an unbound verdict")
		}
		v.TreeSHA = "tree"
		first, err := record("tip")
		if err != nil {
			return err
		}
		again, err := record("tip")
		if err != nil || again.LandingID != first.LandingID || again.HeadSHA != "worker-head" || again.TrunkAfter != "repair" || again.ReplayTip != "tip" {
			t.Errorf("retry = %+v, %v; first = %+v", again, err, first)
		}
		if _, err := record("other-tip"); err == nil {
			t.Error("accepted a different publication under the same run")
		}
		var count int
		if err := tx.QueryRow(`SELECT count(*) FROM landings WHERE event_seq>0`).Scan(&count); err != nil {
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
