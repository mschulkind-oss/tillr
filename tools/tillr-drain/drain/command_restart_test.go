package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestCommandHoldRestartKeepsOutcomeAndBuilderPin(t *testing.T) {
	for _, mode := range []string{"fail", "fault"} {
		t.Run(mode, func(t *testing.T) {
			f, l, steps := commandLoopFixture(t, mode, "read_only", false)
			result := &LoopResult{}
			// Run the builder, examine it, then run the command. Stop before examination.
			for i := 0; i < 3; i++ {
				graphPass(t, l, result)
			}
			if len(steps.runs) != 2 {
				t.Fatalf("runs %+v", steps.runs)
			}
			work := pendingForTest(t, f, "item-a")
			d := work.Decision
			head := runGit(t, l.laneTarget(d.LaneID), "rev-parse", "HEAD")
			base := runGit(t, f.root, "rev-parse", "HEAD")
			outcome, want := "failed", "node-failed: check"
			if mode == "fault" {
				outcome, want = "fault", ReasonCommandFault
			}
			// Reproduce the crash window after hold classification and reset, before
			// markExamined. These are the same operations terminal settlement performs.
			if err := l.nodeOutcome(context.Background(), d, outcome); err != nil {
				t.Fatal(err)
			}
			if stop := l.abandon(context.Background(), d.LaneID, d.RunRequest.RunID, head, "command node hold"); stop != "" {
				t.Fatal(stop)
			}
			next := replaceGraphLoop(t, f, l, steps)
			graphPass(t, next, result)
			if got := runGit(t, f.root, "rev-parse", "refs/tillr/runs/"+d.RunRequest.RunID); got != head {
				t.Fatalf("builder pin changed: %s -> %s", head, got)
			}
			if steps.gates != 0 || result.Landed != 0 || runGit(t, f.root, "rev-parse", "HEAD") != base || len(steps.runs) != 2 {
				t.Fatalf("replayed command or landed: %+v", result)
			}
			found := false
			for _, p := range mustPlan(t, next) {
				if p.decision.ItemID == "item-a" {
					found = true
					if p.decision.Action != ActionHold || p.decision.Reason != want {
						t.Fatalf("hold changed: %+v", p.decision)
					}
				}
			}
			if !found {
				t.Fatal("missing held item")
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var got string
				if err := tx.QueryRow(`SELECT outcome FROM node_runs WHERE run_id=?`, d.RunRequest.RunID).Scan(&got); err != nil {
					return err
				}
				if got != outcome {
					t.Errorf("outcome changed: %s -> %s", outcome, got)
				}
				return nil
			})
		})
	}
}
