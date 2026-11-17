package drain

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWorkflowExitMachineryFailureRetriesNodeNotRepair(t *testing.T) {
	raw := strings.Replace(exitRepair, `["check-again"]`, `["check"]`, 1)
	f, l, steps := exitFixture(t, raw, true, 0)
	steps.infraNode = 2
	l.cfg.Policy.Preference = []string{"fixture", "alternate"}
	l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "alternate"}
	l.cfg.Policy.SeatLists = map[string][]string{"scout": {"fixture", "alternate"}, "build": {"fixture", "alternate"}}
	result := &LoopResult{}
	for i := 0; i < 10; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 3 || !strings.Contains(steps.runs[2].RunID, "-n2") || steps.runs[2].Backend != "alternate" || result.Landed != 1 {
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
