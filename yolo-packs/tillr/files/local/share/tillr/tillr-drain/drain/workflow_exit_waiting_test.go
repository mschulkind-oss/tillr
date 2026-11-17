package drain

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWorkflowExitHeldSecondNodeShowsProgress(t *testing.T) {
	raw := `{"name":"scout-then-build","version":1,"stages":[{"id":"one","kind":"agent","seat":"build","access":"write"},{"id":"two","kind":"agent","seat":"build","access":"read_only"},{"id":"three","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"one","to_node":"two"},{"from_node":"two","to_node":"three"}]}`
	f, l, steps := exitFixture(t, raw, true, 2)
	result := &LoopResult{}
	for i := 0; i < 6; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 2 {
		t.Fatalf("runs %v", steps.runs)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{Build: &l.build, AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, &VantageQuestions{Root: f.root})
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID == "item-a" {
				if row.Where != "lane / node two" || row.Why != "node-failed: two; 1/3 nodes passed" {
					t.Errorf("held progress: %+v", row)
				}
				if row.Decision.Workflow == nil || !strings.Contains(row.Mover, "retry") {
					t.Errorf("hold lacks graph or next action: %+v", row)
				}
				return nil
			}
		}
		t.Error("held item absent")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
