package drain_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestClosingItemMidRunEndsLiveWorkerReleasesLaneAndLandsNothing(t *testing.T) {
	markers := scratch(t, "close-midrun-markers")
	t.Setenv("CLOSE_MARKERS", markers)
	// item_a blocks until a release file that is never created, so it can only exit
	// when the drain ends it. item_b writes its marker and waits for release so we
	// can prove lane-1 was released and handed to the waiting item.
	adapter := stub(t, "close-midrun-worker", counting+`echo started > "$CLOSE_MARKERS/$TILLR_RUN_ID.started"
if [ "$TILLR_RUN_ID" = "run-item_a-a1" ]; then
  while [ ! -f "$CLOSE_MARKERS/never" ]; do sleep 0.02; done
else
  while [ ! -f "$CLOSE_MARKERS/release-b" ]; do sleep 0.02; done
fi
printf 'RESULT: done\n'`)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store,
		itemRecordWithFence("item_a", "in-queue", "candidate.txt"),
		itemRecordWithFence("item_b", "in-queue", "candidate.txt"),
	)
	t.Cleanup(func() {
		writeFile(t, filepath.Join(markers, "never"), "cleanup")
		writeFile(t, filepath.Join(markers, "release-b"), "cleanup")
	})

	cmd, out := startStopFixture(t, f, 1)
	waitForFile(t, filepath.Join(markers, "run-item_a-a1.started"), hangGuard)

	// Close item_a mid-run while its worker holds the single lane.
	s, err := store.Open(context.Background(), f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := item.Close(context.Background(), tx, "item_a", "premise was wrong", "operator", time.Now().UTC())
		return err
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	// Closing item_a must end its live worker and release lane-1 so item_b starts.
	waitForFile(t, filepath.Join(markers, "run-item_b-a1.started"), hangGuard)

	writeFile(t, filepath.Join(markers, "release-b"), "finish")
	stopFixture(t, f, cmd, out, false)

	// Verify item_a's run ended, recorded drain.run-ended-item-closed, and landed nothing.
	closedEvents := stopEventsOf(t, f.store, "drain.run-ended-item-closed")
	if len(closedEvents) != 1 {
		t.Fatalf("drain.run-ended-item-closed count = %d, want 1; loop output:\n%s", len(closedEvents), out.String())
	}
	if closedEvents[0].ObjectID == nil || *closedEvents[0].ObjectID != "run-item_a-a1" {
		t.Fatalf("closed event object_id = %v, want run-item_a-a1", closedEvents[0].ObjectID)
	}
	var payload struct {
		ItemID    string `json:"item_id"`
		RunID     string `json:"run_id"`
		LaneID    string `json:"lane_id"`
		ItemState string `json:"item_state"`
		Reason    string `json:"reason"`
		Detail    string `json:"detail"`
	}
	if err := json.Unmarshal(closedEvents[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.ItemID != "item_a" || payload.RunID != "run-item_a-a1" || payload.ItemState != "closed" || payload.Reason != "item-closed" || payload.Detail == "" {
		t.Fatalf("unexpected drain.run-ended-item-closed payload: %+v", payload)
	}

	orphaned := stopEventsOf(t, f.store, "drain.orphaned")
	if len(orphaned) != 1 || orphaned[0].ObjectID == nil || *orphaned[0].ObjectID != "run-item_a-a1" {
		t.Fatalf("drain.orphaned events = %+v, want 1 for run-item_a-a1", orphaned)
	}

	rs, err := store.OpenRead(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if err := rs.ReadTx(context.Background(), func(r *store.Reader) error {
		var state string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id = 'item_a'`).Scan(&state); err != nil {
			return err
		}
		if state != "closed" {
			t.Errorf("item_a state = %q, want closed", state)
		}
		var landings int
		if err := r.QueryRow(`SELECT COUNT(*) FROM landings WHERE item_id = 'item_a'`).Scan(&landings); err != nil {
			return err
		}
		if landings != 0 {
			t.Errorf("item_a landings = %d, want 0", landings)
		}
		var runCount int
		if err := r.QueryRow(`SELECT COUNT(*) FROM runs WHERE run_id = 'run-item_a-a1'`).Scan(&runCount); err != nil {
			return err
		}
		if runCount != 1 {
			t.Errorf("item_a terminal run rows = %d, want 1", runCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
