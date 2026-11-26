package drain

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestSeatSharesPolicyRoundTripAndDescription(t *testing.T) {
	p := sharesPolicy(t, `{"backends":[{"backend":"builder-a","share":3},{"backend":"builder-b","share":1}],"pick":"random"}`)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var restored Policy
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Check(); err != nil {
		t.Fatal(err)
	}
	if restored.SeatShares["build"][0] != 3 || restored.SeatPicks["build"] != "random" || len(restored.SeatLists["build"]) != 2 {
		t.Fatalf("lost selection: %s", raw)
	}
	if !strings.Contains(restored.Description(), "build=builder-a:3,builder-b:1 pick=random") {
		t.Fatal(restored.Description())
	}
}

func TestSeatSharesLegacyReplayAndBackendImmutable(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := sharesPolicy(t, `["builder-a","builder-b"]`)
	d := Decision{DecisionID: "legacy", ItemID: "item-a", Seat: "build", Action: ActionDispatch, Reason: ReasonReady, RunRequest: p.Request("item-a", "lane-1", "legacy-run", "builder-a"), LaneID: "lane-1"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO dispatches(decision_id,item_id,lane_id,run_id,action,reason,seat,dispatched_at) VALUES(?,?,?,?,?,?,?,?)`, d.DecisionID, d.ItemID, d.LaneID, d.RunRequest.RunID, d.Action, d.Reason, d.Seat, l.cfg.Now().UnixMilli())
		return err
	})
	fixtureWrite(t, f, func(tx *store.Tx) error { return recordPassDecision(tx, d, l.cfg.Now()) })
	d.DecisionID = "new"
	fixtureWrite(t, f, func(tx *store.Tx) error { return recordPassDecision(tx, d, l.cfg.Now()) })
	d.RunRequest = p.Request("item-a", "lane-1", "legacy-run", "builder-b")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := recordPassDecision(tx, d, l.cfg.Now()); err == nil {
			t.Error("changed backend accepted")
		}
		return nil
	})
}

func TestSeatSharesImplicitReviewRecordsChoice(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := sharesPolicy(t, `{"backends":[{"backend":"builder-a","share":1},{"backend":"builder-b","share":1}],"pick":"round-robin"}`)
	p.Seats["review"] = p.Seats["build"]
	p.SeatLists["review"] = p.SeatLists["build"]
	p.SeatPicks["review"] = p.SeatPicks["build"]
	p.SeatShares["review"] = p.SeatShares["build"]
	l.cfg.Policy = &p
	for i, id := range []string{"judge-1", "judge-2"} {
		req, open, err := l.selectReviewDispatch(context.Background(), *p.Request("item-a", "lane-1", id, "builder-a"))
		if err != nil || !open || req.Backend != []string{"builder-a", "builder-b"}[i] {
			t.Fatalf("review: %+v/%v/%v", req, open, err)
		}
		// Same request restored before receipt writing must retain its recorded draw.
		again, open, err := l.selectReviewDispatch(context.Background(), *p.Request("item-a", "lane-1", id, "builder-a"))
		if err != nil || !open || again.Backend != req.Backend {
			t.Fatalf("review redrawn: %+v/%v", again, err)
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM dispatches WHERE seat='review' AND backend IS NOT NULL`).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			t.Errorf("recorded reviews %d", n)
		}
		return nil
	})
}
