package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/quota"
	"github.com/mschulkind-oss/tillr/store"
)

func TestMergeQuotaWaitingPreservesPlaceAndPatchMove(t *testing.T) {
	rows := []waitingRow{
		{ItemID: "item-a", Place: 3, Document: "docs/design/example.md", Why: "run-live"},
		{ItemID: "patch-move", Observation: "patch-move", Why: "owner acknowledgment needed"},
	}
	paused := waitingRow{ItemID: "item-a", RunID: "run-a", Backend: "alpha", Why: "quota paused"}
	got := mergeQuotaWaiting(rows, []waitingRow{paused})
	if len(got) != 2 || got[0].RunID != "run-a" || got[0].Place != 3 || got[0].Document != rows[0].Document {
		t.Fatalf("quota pause lost item metadata: %+v", got)
	}
	if got[1].Observation != "patch-move" || got[1].Why != "owner acknowledgment needed" {
		t.Fatalf("quota merge lost patch move: %+v", got[1])
	}
}

func TestWaitingQuotaPauseReadsRunAndBackendReopening(t *testing.T) {
	s, err := store.Open(context.Background(), t.TempDir()+"/store.db", true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	reset := now.Add(time.Hour).Truncate(time.Second)
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for _, ddl := range []string{quota.DDL, `CREATE TABLE quota_pauses(run_id TEXT,item_id TEXT,lane_id TEXT,backend TEXT,state TEXT,paused_at INTEGER)`} {
			if _, err := tx.Exec(ddl); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO backend_quota VALUES('alpha','[]',?,0,'[{"kind":"five_hour","fraction":1,"reset_at":"`+reset.Format(time.RFC3339)+`"}]',''); INSERT INTO quota_pauses VALUES('run-a','item-a','lane-a','alpha','waiting',?),('run-b','item-b','lane-b','alpha','requested',?)`, now.UnixMilli(), now.UnixMilli(), now.UnixMilli())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{now, now.Add(quota.ProbeInterval + time.Second)} {
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			rows, err := waitingBackendQuotas(r, at)
			if err != nil {
				return err
			}
			if len(rows) != 2 {
				t.Fatalf("rows=%+v", rows)
			}
			row := rows[1]
			if row.ItemID != "item-a" || row.Class != "waits on work" || row.Where != "backend" {
				t.Fatalf("pause not visible: %+v", row)
			}
			for _, want := range []string{"run-a", "lane-a", "alpha"} {
				if !strings.Contains(row.Why, want) {
					t.Errorf("missing %s: %s", want, row.Why)
				}
			}
			want := reset.Format(time.RFC3339)
			if at != now {
				want = "reopening unknown"
			}
			if !strings.Contains(row.Why, want) {
				t.Errorf("missing %s: %s", want, row.Why)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}
