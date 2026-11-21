package drain

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestForkHeldContinuationLeavesLaneForNextItem(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b", "item-c")
	ctx := context.Background()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return recordFixtureFork(tx, "item-b")
	})
	// Start the holder before the earlier item is ready to claim the same fork.
	holder := mustPlan(t, l)[1]
	if holder.decision.ItemID != "item-b" || holder.decision.LaneID != "lane-2" {
		t.Fatalf("unexpected holder plan: %+v", holder.decision)
	}
	if ok, _, err := l.applyPlan(ctx, holder, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("holder dispatch = %v, %v", ok, err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := recordFixtureFork(tx, "item-a"); err != nil {
			return err
		}
		// The classifier finished and released its lane. The successor has
		// not claimed a fork or staged a checkout yet.
		d := f.decisions()[0]
		d.Workflow = &Workflow{Stages: []WorkflowStage{
			{ID: "classify", Seat: "plan", Access: "read_only", Outputs: map[string]string{deliveryOutput: "json"}},
			{ID: "build", Seat: "build", Access: "write"},
		}}
		d.Attempt, d.NodeIndex, d.Seat = 1, 1, "build"
		d.Reason, d.NodeBaseSHA = "workflow-next-node", base
		d.RunRequest.RunID = "run-item-a-a1-n2"
		return appendEffect(tx, nodeAdvancedKind, d.RunRequest.RunID, pendingWork{
			Decision: d, BaseSHA: base, Ready: true, LaneReleased: true, ParentRunID: "run-item-a-a1-n1",
		}, l.cfg.Now())
	})

	plans := mustPlan(t, l)
	wait := plans[0].decision
	if wait.ItemID != "item-a" || wait.Action != ActionHold || wait.Reason != ReasonForkHeld || wait.DeferredBy != "item-b" || wait.LaneID != "" || wait.RunRequest != nil {
		t.Fatalf("earlier item should wait on item-b without a lane or launch: %+v", wait)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		answer, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339Nano), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		if row := answer.Rows[0]; row.ItemID != "item-a" || row.Class != "waits on work" || row.Mover != "item-b" || !slices.Equal(row.WaitingOn, []string{"item-b"}) {
			t.Fatalf("why should name item-b as the mover: %+v", row)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	plain := plans[2]
	if d := plain.decision; d.ItemID != "item-c" || !isDispatch(d) || d.LaneID != "lane-1" {
		t.Fatalf("plain item should get the sole free lane in the same pass: %+v", d)
	}
	if ok, _, err := l.applyPlan(ctx, plain, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("plain item dispatch = %v, %v", ok, err)
	}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		d := holder.decision
		at := l.cfg.Now().Format(time.RFC3339Nano)
		return applyTerminal(tx, run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{}})
	})
	if err := l.markExamined(ctx, holder.decision); err != nil {
		t.Fatal(err)
	}
	resumed := mustPlan(t, l)[0]
	if d := resumed.decision; d.ItemID != "item-a" || !isDispatch(d) || d.LaneID != "lane-2" {
		t.Fatalf("waiter should resume as soon as the fork is released: %+v", d)
	}
}
