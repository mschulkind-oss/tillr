package drain

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// retryLaneFixture has an operator-authorized retry whose old run exited normally.
// An unfinished dispatch holds lane-1 without a lease. A separate lease holds
// express-1 when busy is true; otherwise express-1 is free.
func retryLaneFixture(t *testing.T, busy bool, otherItems ...string) (refusalLoopFixture, *Loop) {
	t.Helper()
	f, l := passFixture(t, append([]string{"retry-item", "work-holder"}, otherItems...)...)
	l.cfg.Lanes = []string{"lane-1", "express-1"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		at := l.cfg.Now()
		stamp := at.Format(time.RFC3339)
		if _, err := tx.Exec(`UPDATE items SET express=1 WHERE item_id='retry-item'; INSERT INTO lanes(lane_id,state) VALUES ('lane-1','idle'),('express-1','idle')`); err != nil {
			return err
		}
		if err := applyTerminal(tx, run.Record{RunID: "retry-old-run", ItemID: "retry-item", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: stamp, FinishedAt: stamp, ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit}); err != nil {
			return err
		}
		if _, err := item.SetState(context.Background(), tx, "retry-item", StateBlocked, at, item.WaitingOn{Waiter: "owner", Reason: "obstacle"}); err != nil {
			return err
		}
		if _, err := AuthorizeRetry(context.Background(), tx, RetryRequest{ItemID: "retry-item", RunID: "retry-old-run", By: "operator", Why: "fence widened", At: at.Add(time.Second)}); err != nil {
			return err
		}
		d := Decision{ItemID: "work-holder", LaneID: "lane-1", Action: ActionDispatch, Reason: ReasonReady, Attempt: 1, From: "queued", To: "running", RunRequest: &run.Request{ItemID: "work-holder", RunID: "work-holder-run", LaneID: "lane-1", Backend: "fixture"}}
		identifyDecision(&d)
		raw, err := json.Marshal(pendingWork{Decision: d})
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (0,?,'run','work-holder-run',?)`, dispatchStartedKind, string(raw)); err != nil {
			return err
		}
		if busy {
			_, err = run.ClaimLease(context.Background(), tx, run.Request{RunID: "lease-holder-run", ItemID: "lease-holder", Backend: "fixture"}, "express-1", at)
		}
		return err
	})
	return f, l
}

func retryWhyRow(t *testing.T, f refusalLoopFixture, l *Loop) WhyRow {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	var found *WhyRow
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{AvailableLanes: &l.cfg.Lanes, Now: l.cfg.Now().Format(time.RFC3339)}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID == "retry-item" {
				copy := row
				found = &copy
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("retry item missing from why")
	}
	return *found
}

func assertRetryNoRefusal(t *testing.T, row WhyRow) {
	t.Helper()
	for name, value := range map[string]string{"reason": row.Reason, "why": row.Why, "mover": row.Mover} {
		if strings.Contains(value, "harness-refused") || value == "the next pass" {
			t.Errorf("%s = %q for operator retry: must not report a harness refusal or the next pass alone", name, value)
		}
	}
}

func TestWhyOperatorRetryWaitingForLanesNamesHolders(t *testing.T) {
	f, l := retryLaneFixture(t, true)
	row := retryWhyRow(t, f, l)
	if row.Decision.Action != ActionHold || row.Reason != ReasonNoLane {
		t.Fatalf("fixture: expected no-lane hold, got %+v", row.Decision)
	}
	assertRetryNoRefusal(t, row)
	why := strings.ToLower(row.Why)
	for _, want := range []string{"operator retry", "waiting", "lane"} {
		if !strings.Contains(why, want) {
			t.Errorf("why %q does not say %q", row.Why, want)
		}
	}
	for _, want := range []string{"lane-1", "work-holder", "work-holder-run", "express-1", "lease-holder-run"} {
		if !strings.Contains(row.Mover, want) {
			t.Errorf("mover %q does not name %q", row.Mover, want)
		}
	}
}

func TestWhyOrdinaryOperatorRetryNamesEarlierDispatchAndExamination(t *testing.T) {
	f, l := retryLaneFixture(t, false, "earlier-item")
	l.cfg.Lanes = append(l.cfg.Lanes, "lane-2", "lane-3")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET express=0, rank='z' WHERE item_id='retry-item';
			UPDATE items SET rank='a' WHERE item_id='earlier-item';
			INSERT INTO lanes(lane_id,state,run_id) VALUES ('lane-3','leased','candidate-run')`)
		return err
	})
	row := retryWhyRow(t, f, l)
	if row.Decision.Action != ActionHold || row.Reason != ReasonNoLane {
		t.Fatalf("expected no-lane hold, got %+v", row.Decision)
	}
	for _, field := range []string{row.Why, row.Mover} {
		for _, want := range []string{"lane-2 held by earlier planned item earlier-item", "lane-3 held by active lease for candidate-run"} {
			if !strings.Contains(field, want) {
				t.Errorf("%q does not name %q", field, want)
			}
		}
		if strings.Contains(field, "express-1") {
			t.Errorf("ordinary retry reports an express lane: %q", field)
		}
	}
}

func TestWhyOperatorRetryPlannedOnFreeLane(t *testing.T) {
	f, l := retryLaneFixture(t, false)
	row := retryWhyRow(t, f, l)
	if row.Decision.Action != ActionRetry || row.Decision.LaneID != "express-1" {
		t.Fatalf("fixture: expected retry on express-1, got %+v", row.Decision)
	}
	assertRetryNoRefusal(t, row)
	why := strings.ToLower(row.Why)
	for _, want := range []string{"operator retry", "planned", "express-1", "free"} {
		if !strings.Contains(why, want) {
			t.Errorf("why %q does not say %q", row.Why, want)
		}
	}
	if strings.Contains(why, "waiting for a lane") {
		t.Errorf("free planned lane described as a wait: %q", row.Why)
	}
}

func TestOperatorRetryReasonAndHistoricalHoldRule(t *testing.T) {
	f, l := retryLaneFixture(t, false)
	_ = f
	d := planFor(t, l, "retry-item")
	if d.Action != ActionRetry || d.LaneID != "express-1" {
		t.Fatalf("fixture: expected retry on express-1, got %+v", d)
	}
	if d.Reason != "operator-retry" {
		t.Errorf("retry reason = %q, want operator-retry", d.Reason)
	}
	if got := holdrules.For("harness-refused"); got != (holdrules.Rule{Class: "ready", Where: "queue", Mover: "the next pass"}) {
		t.Errorf("historical harness-refused rule = %+v", got)
	}
}
