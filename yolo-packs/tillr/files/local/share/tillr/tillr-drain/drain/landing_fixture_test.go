package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// Lock reuse loads land's registered schema. Fixtures that only exercise proof
// lookup still supply a complete row instead of replacing that schema with a
// reduced table; these rows are test data, not actual landing transactions.
func recordFixtureLanding(tx *store.Tx, itemID, runID, tip string, seq int) error {
	_, err := tx.Exec(`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES(?,?,?,'fixture-verdict','landed','fixture-base',?,'fixture-before',?,?)`, "fixture-"+itemID+runID+tip, itemID, runID, tip, tip, seq)
	return err
}

func recordFixtureLandings(t *testing.T, storePath string, tips ...string) {
	t.Helper()
	if len(tips) == 0 {
		return
	}
	s, err := store.Open(context.Background(), storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for _, tip := range tips {
			if err := recordFixtureLanding(tx, "fixture-"+tip, "run-"+tip, tip, 0); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
