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

// offRotationFixture starts item-a's run on lane-2, then shrinks the loop to lane-1 alone (the
// 2026-11-20 move from --lanes 30 to --lanes 20, with the run on lane-24). The run ends
// timed out and leaves its lane leased with no expiry, which is the store's state for it:
// the claim of a loop that is gone, which no run face and no examination holds.
func offRotationFixture(t *testing.T) (refusalLoopFixture, *Loop, run.Request) {
	t.Helper()
	ctx := context.Background()
	f := newRefusalLoopFixtureWithLanes(t, 2, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	holdWALKeeper(t, f.storePath)
	l.cfg.Lanes = []string{"lane-2"}
	plan := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	if ok, _, err := l.applyPlan(ctx, plan, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("dispatch %v / %v", ok, err)
	}
	dir := l.laneDirPath("lane-2")
	writeForkFile(t, filepath.Join(dir, FenceFile), "candidate.txt\n")
	writeForkFile(t, filepath.Join(dir, BaseSHAFile), base+"\n")
	l.cfg.Lanes = []string{"lane-1"}
	req := *plan.decision.RunRequest
	if req.LaneID != "lane-2" {
		t.Fatalf("the run is on %s, want lane-2", req.LaneID)
	}
	return f, l, req
}

func endOffRotationRun(t *testing.T, f refusalLoopFixture, l *Loop, req run.Request) {
	t.Helper()
	at := l.cfg.Now().Format(time.RFC3339)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, run.Record{RunID: req.RunID, ItemID: "item-a", LaneID: "lane-2", Backend: "fixture", State: run.StateTimedOut, EndCause: run.EndLeaseReclaimed, StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{}}); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state='leased', run_id=?, leased_until=NULL WHERE lane_id='lane-2'`, req.RunID)
		return err
	})
}

func offRotationLaneRow(t *testing.T, f refusalLoopFixture, lane string) (state, runID string) {
	t.Helper()
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT state, COALESCE(run_id,'') FROM lanes WHERE lane_id=?`, lane).Scan(&state, &runID)
	})
	return state, runID
}

// A run that ended on a lane the loop no longer runs held its item as no-lane for over ten
// hours: nothing examines a lane outside the rotation, and the loop's reap spared the
// no-expiry lease of a run it was not running. The next passes settle the run where it
// ended, release the lane, and dispatch the operator's authorized retry on a lane in rotation.
func TestARunEndedOnALaneOutsideTheRotationIsSettledAndItsRetryDispatchesInRotation(t *testing.T) {
	ctx := context.Background()
	f, l, req := offRotationFixture(t)
	endOffRotationRun(t, f, l, req)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(ctx, tx, RetryRequest{ItemID: "item-a", RunID: req.RunID, By: "operator", Why: "the run was cut short by a loop restart", At: l.cfg.Now(), Acknowledge: RetryAckUnjudged})
		return err
	})
	var retried *Decision
	for i := 0; i < 6 && retried == nil; i++ {
		var result LoopResult
		if _, err := l.pass(ctx, &result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, &result)
		for _, p := range mustPlan(t, l) {
			if p.decision.ItemID == "item-a" && p.decision.Action == ActionRetry {
				d := p.decision
				retried = &d
			}
		}
	}
	if retried == nil {
		t.Fatalf("the retry never dispatched; item-a still holds: %+v", planFor(t, l, "item-a"))
	}
	if retried.LaneID != "lane-1" || retried.RunRequest == nil || retried.RunRequest.LaneID != "lane-1" {
		t.Fatalf("the retry is planned on %s, want lane-1, the lane in rotation", retried.LaneID)
	}
	if state, holder := offRotationLaneRow(t, f, "lane-2"); state != "idle" || holder != "" {
		t.Fatalf("lane-2 is %s held by %q, want idle: the run settled where it ended", state, holder)
	}
}

// A claim that a live holder still owns is not stranded: the lane is held by a run that is not
// finished, and the reap leaves it.
func TestARunStillInFlightOnALaneOutsideTheRotationKeepsItsLane(t *testing.T) {
	ctx := context.Background()
	f, l, req := offRotationFixture(t)
	for i := 0; i < 2; i++ {
		var result LoopResult
		if _, err := l.pass(ctx, &result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, &result)
	}
	if state, holder := offRotationLaneRow(t, f, "lane-2"); state != "leased" || holder != req.RunID {
		t.Fatalf("lane-2 is %s held by %q, want it leased to the run that has no end", state, holder)
	}
}

// When an off-rotation run's lane cannot be released, the hold says the lane is outside the
// loop's rotation and names the operator as its mover, not the drain that cannot move it.
func TestAHoldOnALaneOutsideTheRotationNamesTheOperator(t *testing.T) {
	ctx := context.Background()
	f, l, req := offRotationFixture(t)
	endOffRotationRun(t, f, l, req)
	// A lease that expires later is a live holder's: the reap leaves it, and the item waits.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE lanes SET leased_until=? WHERE lane_id='lane-2'`, l.cfg.Now().Add(time.Hour).UnixMilli())
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	var answer WhyAnswer
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		lanes := []string{"lane-1"}
		var err error
		answer, err = Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339), AvailableLanes: &lanes, Build: &l.build}, l.cfg.Policy, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(answer.Rows) != 1 {
		t.Fatalf("%d rows, want 1", len(answer.Rows))
	}
	row := answer.Rows[0]
	for _, want := range []string{"operator; ", "lane-2", "--lanes"} {
		if !strings.Contains(row.Mover, want) {
			t.Fatalf("mover %q lacks %q", row.Mover, want)
		}
	}
	if !strings.Contains(row.Why, "outside") || !strings.Contains(row.Why, req.RunID) {
		t.Fatalf("why %q does not say the lane is outside the rotation and name the run", row.Why)
	}
	if row.Class != "needs you" {
		t.Fatalf("class %q, want needs you", row.Class)
	}
}
