package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"

	"github.com/mschulkind-oss/tillr/store"
)

func TestOwnerCriterionStopsPendingWorkflowNodes(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_criteria (item_id,criterion_id,text,state,claim_class) VALUES ('item-a','c-owner','Choose the direction.','not-done','verification-only')`)
		return err
	})
	result := &LoopResult{}
	for i := 0; i < 2; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 1 || !pendingForTest(t, f, "item-a").Ready {
		t.Fatal("fixture did not reach its next node")
	}
	// Hardening the class leaves the criterion id and text unchanged.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_criteria SET claim_class='owner-only' WHERE item_id='item-a' AND criterion_id='c-owner'`)
		return err
	})
	work := pendingForTest(t, f, "item-a")
	lane := work.Decision.LaneID
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := run.ClaimLease(context.Background(), tx, run.Request{RunID: "live-owner-workflow", ItemID: "item-a", Backend: "fixture"}, lane, l.cfg.Now())
		return err
	})
	live := findTriagePlan(t, l, "item-a").decision
	if live.Action != ActionHold || live.Reason != ReasonRunLive || live.WorkflowRelease {
		t.Fatalf("live workflow was released or advanced: %+v", live)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE lanes SET leased_until=1 WHERE lane_id=? AND run_id='live-owner-workflow'`, lane)
		return err
	})
	d := findTriagePlan(t, l, "item-a").decision
	if d.Action == ActionDispatch || !d.WorkflowRelease {
		t.Fatalf("pending workflow advanced with owner ruling owed: %+v", d)
	}
	graphPass(t, l, result)
	if len(steps.runs) != 1 || steps.gates != 0 || !pendingForTest(t, f, "item-a").Done {
		t.Fatalf("pending graph not released without a run: runs=%d gates=%d", len(steps.runs), steps.gates)
	}
	d = findTriagePlan(t, l, "item-a").decision
	if d.Action != ActionHold || d.Reason != ReasonOwnerOnlyOwed || d.RunRequest != nil || d.LaneID != "" {
		t.Fatalf("released item did not hold: %+v", d)
	}
}

func TestOwnerCriterionAddedAfterPlanPreventsDispatch(t *testing.T) {
	f, l := passFixture(t, "item-a")
	plan := mustPlan(t, l)[0]
	if plan.decision.Action != ActionDispatch {
		t.Fatalf("fixture must plan a dispatch: %+v", plan.decision)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_criteria (item_id,criterion_id,text,state,claim_class) VALUES ('item-a','c-owner','Choose the direction.','not-done','owner-only')`)
		return err
	})
	next := mustPlan(t, l)[0]
	if next.version == plan.version {
		t.Fatal("criterion state was not included in the plan's version")
	}
	before := eventCount(t, l.cfg.Store)
	ok, _, err := l.applyPlan(context.Background(), plan, runGit(t, l.cfg.Root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || ok || eventCount(t, l.cfg.Store) != before {
		t.Fatalf("stale plan dispatched after owner criterion added: applied=%v err=%v", ok, err)
	}
}

func TestOwnerCriterionPreservesTerminalWorkflowSettlement(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	graphPass(t, l, result)
	if len(steps.runs) != 1 || pendingForTest(t, f, "item-a").Ready {
		t.Fatal("fixture did not leave a terminal run awaiting settlement")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_criteria (item_id,criterion_id,text,state,claim_class) VALUES ('item-a','c-owner','Choose the direction.','not-done','owner-only')`)
		return err
	})
	d := findTriagePlan(t, l, "item-a").decision
	if d.Action != ActionSettle || d.WorkflowRelease {
		t.Fatalf("terminal run must settle before the next node is held: %+v", d)
	}
	graphPass(t, l, result)
	if len(steps.runs) != 1 || !pendingForTest(t, f, "item-a").Ready {
		t.Fatal("settlement did not preserve the completed node")
	}
}

func TestOwnerCriterionHoldsBeforeDocumentTriage(t *testing.T) {
	f, l := triageFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_criteria (item_id,criterion_id,text,state,claim_class) VALUES ('item-request','c-owner','The owner chooses the direction.','not-done','owner-only')`)
		return err
	})
	d := findTriagePlan(t, l, "item-request").decision
	if d.Action != ActionHold || d.Reason != ReasonOwnerOnlyOwed || d.LaneID != "" || d.RunRequest != nil {
		t.Fatalf("owner ruling spent a triage run: %+v", d)
	}
}
