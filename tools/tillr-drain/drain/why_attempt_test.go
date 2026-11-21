package drain

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWhyShowsTheSameAttemptForAnInterruptedNodeRestart(t *testing.T) {
	ctx := context.Background()
	f, l, steps := restartGraphFixture(t)
	l.cfg.Steps = &dirtyWorkflowContinuationSteps{
		restartWorkflowSteps: steps,
		cases:                []dirtyContinuationCase{{endCause: run.EndSignal}},
	}
	show := func(lanes []string, action, reason, runID string, attempt int) {
		t.Helper()
		fixtureRead(t, f, func(r *store.Reader) error {
			answer, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339Nano), AvailableLanes: &lanes}, l.cfg.Policy, &VantageQuestions{Root: f.root})
			if err != nil {
				return err
			}
			for _, row := range answer.Rows {
				if row.ItemID != "item-a" {
					continue
				}
				// Named-item why serializes this row directly, not the planner envelope.
				body, err := json.Marshal(row)
				if err != nil {
					return err
				}
				var shown WhyRow
				if err := json.Unmarshal(body, &shown); err != nil {
					return err
				}
				d := shown.Decision
				gotID := ""
				if d.RunRequest != nil {
					gotID = d.RunRequest.RunID
				}
				if d.Action != action || shown.Reason != reason || gotID != runID || d.Attempt != attempt {
					t.Errorf("why: %s/%s run %q attempt %d, want %s/%s run %q attempt %d", d.Action, shown.Reason, gotID, d.Attempt, action, reason, runID, attempt)
				}
				return nil
			}
			t.Fatal("why omitted item-a")
			return nil
		})
	}

	const first = "run-item-a-a1-n1"
	const replacement = first + "-interrupted"
	show(l.cfg.Lanes, ActionDispatch, ReasonReady, first, 1)
	result := &LoopResult{}
	graphPass(t, l, result) // launch the original node; the run ends by signal
	graphPass(t, l, result) // settle it as a same-node restart
	show(l.cfg.Lanes, ActionDispatch, "workflow-next-node", replacement, 1)
	p := decisionFor(t, l, "item-a")
	applied, work, err := l.applyPlan(ctx, p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !applied {
		t.Fatalf("claim replacement: %v/%v", applied, err)
	}
	d := work.Decision
	fixtureWrite(t, f, func(tx *store.Tx) error {
		// The run face holds a worker lease until it publishes a terminal record.
		_, err := tx.Exec(`UPDATE lanes SET state='leased',run_id=?,leased_until=? WHERE lane_id=?`, replacement, l.cfg.Now().Add(time.Hour).UnixMilli(), d.LaneID)
		return err
	})
	show(l.cfg.Lanes, ActionHold, ReasonRunLive, replacement, 1)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='blocked', waiting_on='{"waiter":"owner","reason":"owner review"}' WHERE item_id='item-a'`)
		return err
	})
	show(l.cfg.Lanes, ActionHold, ReasonBlocked, "", 1)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='in-queue', waiting_on='' WHERE item_id='item-a'`); err != nil {
			return err
		}
		return applyTerminal(tx, run.Record{RunID: replacement, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend,
			State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339Nano), FinishedAt: l.cfg.Now().Format(time.RFC3339Nano), ArtifactRefs: []run.ArtifactRef{}})
	})
	if err := l.recordRefusal(ctx, Candidate{ItemID: d.ItemID, RunID: replacement, LaneID: d.LaneID}, RefusalFence); err != nil {
		t.Fatal(err)
	}
	if err := l.markExamined(ctx, d); err != nil {
		t.Fatal(err)
	}
	show([]string{}, ActionHold, ReasonNoLane, "", 1)
	const next = "run-item-a-a2-n1"
	show(l.cfg.Lanes, ActionDispatch, ReasonReturned, next, 2)
	graphPass(t, l, result)
	if got := steps.runs[len(steps.runs)-1].RunID; got != next {
		t.Fatalf("redispatch launched %q, want %q", got, next)
	}
}
