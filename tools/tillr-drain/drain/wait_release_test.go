package drain

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func landedWaitFixture(t *testing.T, earlierRun bool) (refusalLoopFixture, *Loop, string, string) {
	t.Helper()
	f, l := passFixture(t, "blocker")
	l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
	l.cfg.Policy.SeatLists = map[string][]string{"build": {"fixture"}}
	id := fileFixtureRequestWith(t, f, "Wait for the blocker to deliver its fix\n", "build", item.SingleWorkerWorkflow, requestFence, false)
	ctx, at := context.Background(), l.cfg.Now()
	var designRun string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if earlierRun {
			designRun = "ended-design-run"
			if err := applyTerminal(tx, run.Record{RunID: designRun, ItemID: id, LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: at.Format(time.RFC3339), FinishedAt: at.Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}}); err != nil {
				return err
			}
			c := item.Conflict{ConflictID: "conflict-waiter", ItemID: id, RunID: "ended-build-run", DesignRunID: designRun, Text: "needs design", Status: "awaiting", WaitingOn: "design"}
			if err := item.RecordConflict(tx, c, at); err != nil {
				return err
			}
			c.Status, c.DesignRunID = "raised", ""
			if err := item.RecordConflict(tx, c, at); err != nil {
				return err
			}
		}
		_, err := item.SetState(ctx, tx, id, StateBlocked, at, item.WaitingOn{Waiter: "item:blocker", Reason: "needs the delivered fix"})
		return err
	})
	return f, l, id, designRun
}

func landWaitBlocker(t *testing.T, f refusalLoopFixture) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='blocker'`)
		return err
	})
}

func assertLandedWaitReleased(t *testing.T, f refusalLoopFixture, l *Loop, id, runID string) {
	t.Helper()
	fixtureRead(t, f, func(r *store.Reader) error {
		var state, waiting string
		if err := r.QueryRow(`SELECT state, waiting_on FROM items WHERE item_id=?`, id).Scan(&state, &waiting); err != nil {
			return err
		}
		if state != StateInQueue || waiting != "" {
			t.Fatalf("wait not released: %s / %s", state, waiting)
		}
		events, err := r.EventsOfKind(0, "item.wait-released")
		if err != nil {
			return err
		}
		if len(events) != 1 || events[0].ObjectID == nil || *events[0].ObjectID != id {
			t.Fatalf("expected one release for %s: %+v", id, events)
		}
		var p struct {
			Blocker string `json:"blocker"`
			RunID   string `json:"run_id"`
			Reason  string `json:"reason"`
		}
		if err := json.Unmarshal(events[0].Payload, &p); err != nil {
			return err
		}
		if p.Blocker != "blocker" || p.RunID != runID || !strings.Contains(p.Reason, "landed") {
			t.Fatalf("release did not record its evidence: %+v", p)
		}
		return nil
	})
	if d := decisionFor(t, l, id).decision; d.Action != ActionDispatch || d.BlockConflict != nil {
		t.Fatalf("released item does not have an ordinary dispatch: %+v", d)
	}
}

func TestPassReleasesWaitForLandedItem(t *testing.T) {
	for _, tc := range []struct {
		name       string
		earlierRun bool
	}{{"without a run", false}, {"with an unanswered run", true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f, l, id, runID := landedWaitFixture(t, tc.earlierRun)
			if err := l.runTrunkProofStep(ctx, &LoopResult{}, false); err != nil {
				t.Fatal(err)
			}
			if d := decisionFor(t, l, id).decision; d.Action != ActionHold || d.Reason != ReasonBlocked {
				t.Fatalf("item did not wait for the unlanded blocker: %+v", d)
			}
			landWaitBlocker(t, f)
			// Trunk-proof actor steps release waits without dispatching the item.
			for range 2 {
				if err := l.runTrunkProofStep(ctx, &LoopResult{}, false); err != nil {
					t.Fatal(err)
				}
				assertLandedWaitReleased(t, f, l, id, runID)
			}
		})
	}
}

func TestRetryNamesWorkingStateCommandForLandedWait(t *testing.T) {
	ctx := context.Background()
	f, l, id, runID := landedWaitFixture(t, true)
	landWaitBlocker(t, f)
	s, err := store.Open(ctx, f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(ctx, tx, RetryRequest{ItemID: id, RunID: runID, By: "operator", Why: "blocker landed", At: l.cfg.Now()})
		return err
	})
	cmd := "tillr item state --item " + id + " --state in-queue"
	if err == nil || !strings.Contains(err.Error(), "No retry command is accepted") || !strings.Contains(err.Error(), cmd) {
		t.Errorf("retry refusal did not name the state command %q: %v", cmd, err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.SetState(ctx, tx, id, StateInQueue, l.cfg.Now())
		return err
	})
	if err != nil {
		t.Fatalf("state command was refused: %v", err)
	}
	assertLandedWaitReleased(t, f, l, id, runID)

	// The release answers only the run that preceded it, not an unanswered run
	// that ends after the item was admitted again.
	later := l.cfg.Now().Add(time.Minute).Format(time.RFC3339)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: "later-run", ItemID: id, LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: later, FinishedAt: later, ArtifactRefs: []run.ArtifactRef{}})
	})
	if d := decisionFor(t, l, id).decision; d.Action != ActionHold {
		t.Fatalf("an old release authorized a newer run: %+v", d)
	}
}
