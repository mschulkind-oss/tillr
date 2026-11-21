package drain

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWorkflowExitMachineryFailureRetriesNodeNotRepair(t *testing.T) {
	raw := strings.Replace(exitRepair, `["check-again"]`, `["check"]`, 1)
	f, l, steps := exitFixture(t, raw, true, 0)
	steps.infraNode = 2
	l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "alternate"}
	l.cfg.Policy.SeatLists = map[string][]string{"scout": {"fixture", "alternate"}, "build": {"fixture", "alternate"}}
	result := &LoopResult{}
	for i := 0; i < 5; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 2 || result.Landed != 0 {
		t.Fatalf("failed node ran on an alternative before probe: runs %+v result %+v", steps.runs, result)
	}
	if held := planFor(t, l, "item-a"); held.Action != ActionHold || held.Reason != ReasonBackendClosed || held.DeferredBy != "fixture" {
		t.Fatalf("failed node did not hold on fixture: %+v", held)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		closed, err := readInfrastructureClosed(r)
		if !closed["fixture"] || closed["alternate"] {
			t.Errorf("expected fixture closed with alternate open: %v", closed)
		}
		return err
	})
	if err := ProbeBackend(context.Background(), f.storePath, f.root, "fixture", []string{"/bin/true"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 3 || steps.runs[2].RunID != "run-item-a-a1-n2-restart" || steps.runs[2].Backend != "fixture" || result.Landed != 1 {
		t.Fatalf("fault routing: runs %+v result %+v", steps.runs, result)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		err := tx.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id='item-a' AND node_id='repair' AND outcome!='skipped'`).Scan(&n)
		if n != 0 {
			t.Errorf("repair ran %d", n)
		}
		return err
	})
}

func TestWorkflowExitWaitingLegacyStore(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`DROP TABLE node_runs`); err != nil {
			return err
		}
		answer, err := Why(tx.Reader(), Pass{Build: &l.build, AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, &VantageQuestions{Root: f.root})
		if err == nil && len(answer.Rows) == 0 {
			t.Error("legacy waiting empty")
		}
		return err
	})
}
