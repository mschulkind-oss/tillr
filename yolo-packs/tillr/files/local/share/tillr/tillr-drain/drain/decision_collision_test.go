package drain

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// The design dispatch for a raised conflict names the attempt its run id names. Measured
// 2026-11-18: an item whose earlier run was judged (so its prior-run count skips that run)
// planned run a13 under attempt 12, whose decision id was the a12 dispatch's, and the store's
// refusal of the second row faulted the loop at every start.
func TestAConflictsDesignDispatchNamesTheAttemptOfItsRun(t *testing.T) {
	lanes := []string{"lane-1"}
	priors := make([]priorRun, 11)
	for i := range priors {
		priors[i] = priorRun{RunID: attemptRunID("item-a", i+1)}
	}
	p := planner{
		tick:     Pass{AvailableLanes: &lanes},
		policy:   &Policy{Seats: map[string]string{"build": "fixture", "design": "designer"}, Backends: map[string]Backend{"fixture": {Adapter: "fixture"}, "designer": {Adapter: "fixture"}}},
		priors:   map[string][]priorRun{"item-a": priors},
		attempts: map[string]int{"item-a": 13},
		held:     map[string]bool{}, assigned: map[string]bool{},
	}
	d := p.conflictDecision(itemRow{ItemID: "item-a"}, item.Conflict{ConflictID: "conflict-run-item-a-a12", ItemID: "item-a", RunID: "run-item-a-a12", BodyRef: "docs/x.md#a", Text: "# run report", WaitingOn: "design", Status: "raised"})
	if d.Action != ActionDispatch || d.RunRequest == nil {
		t.Fatalf("no design dispatch: %+v", d)
	}
	if d.RunRequest.RunID != "run-item-a-a13" || d.Attempt != 13 || !strings.Contains(d.DecisionID, "-a13-") {
		t.Fatalf("run %s, attempt %d, decision %s: the decision must name its run's attempt", d.RunRequest.RunID, d.Attempt, d.DecisionID)
	}
}

// A planned decision whose id the store already holds with other columns holds that one
// item with a named fault, and the loop goes on: it is never a loop fault.
func TestADecisionCollisionHoldsTheItemAndNotTheLoop(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	holdWALKeeper(t, f.storePath)
	base := commitBlock(t, f.root, "item-a", "- **c1:** the only version")
	runGit(t, f.root, "update-ref", GreenRef, base)
	p := decisionFor(t, l, "item-a")
	if p.decision.Action != ActionDispatch {
		t.Fatalf("no dispatch planned: %+v", p.decision)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO dispatches (decision_id, item_id, lane_id, run_id, action, reason, dispatched_at) VALUES (?, ?, 'lane-other', 'run-other', 'dispatch', 'earlier', 0)`, p.decision.DecisionID, p.decision.ItemID)
		return err
	})
	ok, work, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || ok || work != nil {
		t.Fatalf("a decision collision reached the loop: ok=%v work=%v err=%v", ok, work, err)
	}
	next := decisionFor(t, l, "item-a")
	if next.decision.Action != ActionHold || next.decision.Reason != ReasonDecisionCollision {
		t.Fatalf("the item is not held for the collision: %+v", next.decision)
	}
}

// A decision collision while settling one run holds that run's item with a named fault and
// the loop goes on; other settle errors keep their recorded rules.
func TestASettleErrorHoldsTheItemAndNotTheLoop(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	holdWALKeeper(t, f.storePath)
	base := commitBlock(t, f.root, "item-a", "- **c1:** the only version")
	runGit(t, f.root, "update-ref", GreenRef, base)
	result := &LoopResult{}
	stop, err := l.takeCompletion(runCompletion{id: "run-item-a-a1", itemID: "item-a", step: "settle", outcome: laneOutcome{err: &decisionCollisionError{ExitError: Internalf("the decision x is already recorded with different columns").(*ExitError), DecisionID: "x", ItemID: "item-a"}}}, result)
	if err != nil || stop != "" || result.StopItemID != "" {
		t.Fatalf("a settle error stopped the loop: stop=%q err=%v result=%+v", stop, err, result)
	}
	next := decisionFor(t, l, "item-a")
	if next.decision.Action != ActionHold || next.decision.Reason != ReasonSettleFailed {
		t.Fatalf("the item is not held for its settle error: %+v", next.decision)
	}
	// A settle error of another class keeps its recorded rule and ends the loop.
	if _, err := l.takeCompletion(runCompletion{id: "run-item-a-a2", itemID: "item-a", step: "run", outcome: laneOutcome{err: Internalf("reading the gate.plan payload failed")}}, &LoopResult{}); err == nil {
		t.Fatal("an unrecordable judgment was held on one item instead of failing the step")
	}
}
