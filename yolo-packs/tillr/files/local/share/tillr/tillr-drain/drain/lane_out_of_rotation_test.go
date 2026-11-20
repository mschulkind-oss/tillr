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

// Lowering --lanes while work is in flight strands nothing. When a lane above the count holds a
// run whose lease a live holder keeps, `why` lists the lane as draining with the item it holds,
// names the drain as the mover (it serves that lane), and no longer tells the operator to relaunch
// with more lanes.
func TestAHoldOnALaneAboveTheCountIsDrainingAndNamesTheDrain(t *testing.T) {
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
	if row.Mover != "drain; lane-2" {
		t.Fatalf("mover %q, want the drain on lane-2", row.Mover)
	}
	if strings.Contains(row.Mover, "--lanes") || !strings.Contains(row.Why, "lane-2 is draining") {
		t.Fatalf("why %q / mover %q: want a draining lane-2, no relaunch advice", row.Why, row.Mover)
	}
	if row.Class == "needs you" {
		t.Fatalf("class %q: a draining lane waits on the drain, not the operator", row.Class)
	}
	if len(answer.Draining) != 1 || answer.Draining[0].LaneID != "lane-2" || answer.Draining[0].ItemID != "item-a" || answer.Draining[0].RunID != req.RunID {
		t.Fatalf("draining = %+v, want lane-2 holding item-a under %s", answer.Draining, req.RunID)
	}
}

// A lane above the count that holds a live run is observed, so the loop sees its lease: it is
// not idle or drained while that run is unfinished, and no new item is planned onto the lane.
func TestALaneAboveTheCountIsObservedWhileItHoldsWork(t *testing.T) {
	ctx := context.Background()
	f, l, _ := offRotationFixture(t)
	_, leases, err := l.passPlansGeneration(ctx, l.cfg.Now(), new(string))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, lease := range leases {
		found = found || lease.LaneID == "lane-2"
	}
	if !found {
		t.Fatalf("leases = %+v, want lane-2's lease observed: its run is live", leases)
	}
	if state, _ := offRotationLaneRow(t, f, "lane-2"); state != "leased" {
		t.Fatalf("lane-2 is %s, want leased", state)
	}
}

// After scaling from lane-8 down to five lanes, the held candidate finishes
// where it is, and no new item is planned onto lane-8.
func TestACandidateAboveTheCountFinishesAndTheLaneThenGoesUnused(t *testing.T) {
	ctx := context.Background()
	f, l, c := heldCandidateFixtureInLane(t, "lane-8")
	l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3", "lane-4", "lane-5"}
	if err := l.stageLanes(ctx); err != nil {
		t.Fatal(err)
	}
	judged := false
	for i := 0; i < 3; i++ {
		for _, p := range mustPlan(t, l) {
			d := p.decision
			switch {
			case d.ItemID == "item-a" && i == 0:
				judged = true
				if d.Action != ActionJudge || d.LaneID != c.LaneID {
					t.Fatalf("the candidate planned %s/%s on %s, want judged on %s", d.Action, d.Reason, d.LaneID, c.LaneID)
				}
			case d.ItemID != "item-a" && d.Action != ActionHold && d.LaneID == c.LaneID:
				t.Fatalf("pass %d offered %s a lane above the count: %+v", i, d.ItemID, d)
			}
		}
		var result LoopResult
		if _, err := l.pass(ctx, &result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, &result)
	}
	if !judged {
		t.Fatal("held candidate was not planned for judging")
	}
	if state, holder := offRotationLaneRow(t, f, c.LaneID); state != "idle" || holder != "" {
		t.Fatalf("%s is %s held by %q, want idle: the work finished and the lane goes unused", c.LaneID, state, holder)
	}
}
