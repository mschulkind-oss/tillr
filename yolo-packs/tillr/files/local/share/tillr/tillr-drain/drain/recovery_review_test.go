package drain

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestExaminePreparesThenJudgesUnrecordedTerminalCandidate(t *testing.T) {
	p := newPendingTerminalFixture(t, false, "")
	steps := &terminalSteps{}
	l := p.replacement(t, steps)
	result, err := l.Examine(context.Background(), p.runID)
	if err != nil || result.Landed != 1 || steps.gates != 1 || steps.lands != 1 || steps.verdicts != 1 {
		t.Fatalf("stopped before judgment: %+v / %+v / %v", result, steps, err)
	}
}

func TestExamineReleasesInvalidatedWorkflowWithoutJudgment(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	for i := 0; i < 5; i++ {
		graphPass(t, l, result)
	}
	w := pendingForTest(t, f, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='blocked' WHERE item_id='item-a'`)
		return err
	})
	if d := mustPlan(t, l)[0].decision; !d.WorkflowRelease {
		t.Fatalf("fixture not invalidated: %+v", d)
	}
	got, err := l.Examine(context.Background(), w.Decision.RunRequest.RunID)
	if err != nil || got.Landed != 0 || steps.gates != 0 || !pendingForTest(t, f, "item-a").Done {
		t.Fatalf("invalid workflow judged: %+v / %+v / %v", got, steps, err)
	}
}

func TestExamineUsesCorrectedRepairInputs(t *testing.T) {
	p := newPendingTerminalFixture(t, true, "")
	l := p.replacement(t, &terminalSteps{})
	c := *pendingForTest(t, p.f, "item-a").Candidate
	hold, err := l.repairBasis(context.Background(), c, LandOutcome{Result: LandSquashConflict}, l.cfg.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.holdRepair(context.Background(), hold, Externalf("fixture broken adapter")); err != errRepairHeld {
		t.Fatal(err)
	}
	l.cfg.Policy.Backends["fixture"] = Backend{Adapter: "corrected-fixture"}
	if d := mustPlan(t, l)[0].decision; d.Action != ActionJudge {
		t.Fatalf("fixture repair not reopened: %+v", d)
	}
	before := snapshotStore(t, p.f.storePath)
	_, err = l.Examine(context.Background(), p.runID)
	if err != nil && strings.Contains(err.Error(), ReasonRepairStagingFailed) {
		t.Fatalf("corrected inputs ignored: %v", err)
	}
	if after := snapshotStore(t, p.f.storePath); after.seq == before.seq {
		t.Fatal("corrected repair was never claimed")
	}
}

func TestSetAsideInterruptionSurvivesAdvancingTrunk(t *testing.T) {
	p := newPendingTerminalFixture(t, true, "")
	l := p.replacement(t, &terminalSteps{})
	// Force an ordinary rollback after the pin and reset destination were persisted.
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER refuse_set_aside BEFORE INSERT ON events WHEN NEW.kind='drain.set-aside' BEGIN SELECT RAISE(ABORT,'fixture interruption'); END`)
		return err
	})
	if _, err := l.SetAside(context.Background(), "item-a", "needs rework"); err == nil {
		t.Fatal("fixture did not fail")
	}
	fixtureWrite(t, p.f, func(tx *store.Tx) error { _, err := tx.Exec(`DROP TRIGGER refuse_set_aside`); return err })
	// Model process death rather than the normal error path's restored head.
	runGit(t, l.lanePath("lane-1"), "reset", "--hard", p.base)
	runGit(t, p.f.root, "commit", "--allow-empty", "-qm", "unrelated trunk movement")
	if _, err := l.SetAside(context.Background(), "item-a", "finish after interruption"); err != nil {
		t.Fatalf("trunk movement stranded recovery: %v", err)
	}
}
