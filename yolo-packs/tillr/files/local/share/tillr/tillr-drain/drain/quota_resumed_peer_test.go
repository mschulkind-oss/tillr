package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestQuotaRefusalAlsoPausesAnAlreadyResumedLiveRun(t *testing.T) {
	f, l, steps, d, rec := quotaContinuationFixture(t, false, true)
	// Like a real resume claim, a live segment has a lease and a run.resume event,
	// while the runs table still contains the previous segment's terminal record.
	req := *d.RunRequest
	req.Resume = true
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := run.ClaimLease(context.Background(), tx, req, d.LaneID, steps.at); err != nil {
			return err
		}
		return appendEffect(tx, run.ResumeKind, req.RunID, map[string]any{"run_id": req.RunID, "item_id": req.ItemID, "lane_id": req.LaneID, "backend": req.Backend, "session_id": "33b5f2d2-0cd4-4a49-b293-c58f5635561f", "resumed_at": steps.at.Format("2006-01-02T15:04:05Z07:00")}, steps.at)
	})
	rec.RunID = "another-refusing-run"
	if err := l.recordBackendWindow(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, run.PauseRequestKind, req.RunID).Scan(&n)
		if n != 1 {
			t.Errorf("resumed live run received %d pause requests", n)
		}
		return err
	})
}
