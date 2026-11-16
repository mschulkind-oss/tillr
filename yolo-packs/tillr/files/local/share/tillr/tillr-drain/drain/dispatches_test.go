package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestTickDecisionIsImmutableAndReplayKeepsFirstTime(t *testing.T) {
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(scratchUnderDurable(t), "store.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	original := Decision{DecisionID: "decision-item-a1-queued-running", ItemID: "item", LaneID: "lane-1", Action: ActionDispatch, Reason: ReasonReady, RunRequest: &run.Request{RunID: "run-item-a1"}, DeferredBy: "holder", Seat: "worker"}
	at := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	write := func(d Decision, at time.Time) error {
		return s.WriteTx(ctx, func(tx *store.Tx) error { return recordPassDecision(tx, d, at) })
	}
	if err := write(original, at); err != nil {
		t.Fatal(err)
	}
	if err := write(original, at.Add(time.Hour)); err != nil {
		t.Fatalf("identical replay: %v", err)
	}
	for _, field := range []string{"item", "lane", "run", "action", "reason", "deferred", "seat"} {
		t.Run(field, func(t *testing.T) {
			changed := original
			switch field {
			case "item":
				changed.ItemID = "other"
			case "lane":
				changed.LaneID = "lane-2"
			case "run":
				changed.RunRequest = &run.Request{RunID: "run-other"}
			case "action":
				changed.Action = ActionRetry
			case "reason":
				changed.Reason = ReasonRetry
			case "deferred":
				changed.DeferredBy = "other"
			case "seat":
				changed.Seat = "designer"
			}
			err := write(changed, at.Add(time.Hour))
			if err == nil || !strings.Contains(err.Error(), original.DecisionID) {
				t.Fatalf("conflicting decision accepted or unnamed: %v", err)
			}
		})
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var count, millis int64
		var item, lane, runID, action, reason, deferred, seat string
		if err := r.QueryRow(`SELECT COUNT(*) FROM dispatches`).Scan(&count); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT item_id, lane_id, run_id, action, reason, dispatched_at, deferred_by, seat FROM dispatches WHERE decision_id = ?`, original.DecisionID).Scan(&item, &lane, &runID, &action, &reason, &millis, &deferred, &seat); err != nil {
			return err
		}
		if count != 1 || item != original.ItemID || lane != original.LaneID || runID != original.RunRequest.RunID || action != original.Action || reason != original.Reason || millis != at.UnixMilli() || deferred != original.DeferredBy || seat != original.Seat {
			t.Errorf("stored row changed: %d %s %s %s %s %s %d %s %s", count, item, lane, runID, action, reason, millis, deferred, seat)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
