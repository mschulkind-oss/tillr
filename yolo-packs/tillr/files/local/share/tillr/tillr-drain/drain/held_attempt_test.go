package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestHeldPassesDoNotSpendAttemptsOnUnlaunchedClaims(t *testing.T) {
	ctx := context.Background()
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	for i := 0; i < 3; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 2 || steps.runs[1].RunID != "run-item-a-a1-n2" {
		t.Fatalf("first attempt's two nodes: %+v", steps.runs)
	}
	// Both superseded nodes have genuine runs. Dropping all superseded rows
	// would fall back to counting these two runs as two attempts.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE node_runs SET outcome='superseded' WHERE item_id='item-a' AND attempt=1`)
		return err
	})
	first := pendingForTest(t, f, "item-a").Decision
	if err := l.recordRefusal(ctx, Candidate{ItemID: first.ItemID, RunID: first.RunRequest.RunID, LaneID: first.LaneID}, RefusalFence); err != nil {
		t.Fatal(err)
	}
	if err := l.markExamined(ctx, first); err != nil {
		t.Fatal(err)
	}

	for pass := 0; pass < 3; pass++ {
		p := decisionFor(t, l, "item-a")
		if d := p.decision; d.Action != ActionDispatch || d.Reason != ReasonReturned || d.Attempt != 2 {
			t.Fatalf("pass %d: want dispatch at attempt 2, got %s attempt %d (%s)", pass, d.Action, d.Attempt, d.Reason)
		}
		ok, work, err := l.applyPlan(ctx, p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
		if err != nil || !ok {
			t.Fatalf("pass %d: claim: %v/%v", pass, ok, err)
		}
		d := work.Decision
		// Reproduce the historical abandoned claim: its node was superseded,
		// the dispatch was examined, and the worker never got a runs row.
		if err := l.nodeOutcome(ctx, d, "superseded"); err != nil {
			t.Fatal(err)
		}
		if err := l.cancelStaging(ctx, d); err != nil {
			t.Fatal(err)
		}
		lanes := l.cfg.Lanes
		l.cfg.Lanes = []string{}
		held := decisionFor(t, l, "item-a").decision
		l.cfg.Lanes = lanes
		if held.Action != ActionHold || held.Reason != ReasonNoLane || held.Attempt != 2 {
			t.Fatalf("pass %d: want no-lane hold with next attempt 2, got %s attempt %d (%s)", pass, held.Action, held.Attempt, held.Reason)
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			var runs int
			if err := r.QueryRow(`SELECT COUNT(*) FROM runs WHERE item_id=?`, d.ItemID).Scan(&runs); err != nil {
				return err
			}
			if runs != 2 || len(steps.runs) != 2 {
				t.Fatalf("pass %d: abandonment started a worker: %d rows, %d launches", pass, runs, len(steps.runs))
			}
			return nil
		})
	}

	// An already-inflated store can have abandoned claims above the slot
	// about to be reused. They must not shadow the new attempt's outcomes.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome)
 VALUES('item-a',147,'one','run-item-a-a147-n1','superseded')`)
		return err
	})
	for i := 0; i < 3; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 4 || steps.runs[2].RunID != "run-item-a-a2-n1" || steps.runs[3].RunID != "run-item-a-a2-n2" {
		t.Fatalf("released hold did not dispatch and progress within attempt 2: %+v", steps.runs)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var actual, abandoned int
		if err := r.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id='item-a' AND attempt=1 AND outcome='superseded'`).Scan(&actual); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id='item-a' AND attempt=147`).Scan(&abandoned); err != nil {
			return err
		}
		if actual != 2 || abandoned != 0 {
			t.Fatalf("superseded nodes after dispatch: %d actual, %d abandoned", actual, abandoned)
		}
		return nil
	})
}
