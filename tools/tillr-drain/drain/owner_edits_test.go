package drain_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

// The fixture adapter makes the edits while its run is in flight. The observer
// cannot attribute main-checkout content to an adapter rather than the owner.
func TestOwnerEditsDuringRunStillSettleJudgeAndLand(t *testing.T) {
	adapter := stub(t, "adapter", counting+committingAdapterBody+`echo owner > "$STUB_ROOT/base.txt"
echo owner > "$STUB_ROOT/owner-new.txt"
printf 'RESULT: done\n'
`)
	f := newLoopFixture(t, adapter)
	t.Setenv("STUB_ROOT", f.root)
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	result, err := loop.Run(context.Background())
	if err != nil || result.Stop != drain.StopQueueDrained || result.Landed != 1 || result.Rejected != 0 {
		t.Fatalf("owner edits stopped settlement: %+v / %v", result, err)
	}
	for _, step := range []string{"gate --item", "land --item", "verdict --item"} {
		if countMatching(lines(t, f.count), step) != 1 {
			t.Fatalf("missing %s: %v", step, lines(t, f.count))
		}
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "landed" {
		t.Fatalf("item state %s", got)
	}
	for _, e := range eventsOf(t, f.store) {
		if e.Kind == drain.BoundaryCheckKind && eventPayload(t, e)["clean"] != true {
			t.Fatalf("owner content convicted a run: %s", e.Payload)
		}
	}
}

func TestSiblingWriteConvictsOnlyItsRun(t *testing.T) {
	adapter := stub(t, "adapter", counting+committingAdapterBody+`echo escape > "$STUB_LANES/lane-2/worktree/escape.txt"
printf 'RESULT: done\n'
`)
	f := newLoopFixture(t, adapter)
	t.Setenv("STUB_LANES", f.laneRoot)
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	loop := f.newLoop(t, 2, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	result, err := loop.Run(context.Background())
	if err != nil || result.Stop != drain.StopQueueDrained || faultOf(t, f.store, "item_041").Reason != drain.StopHostBoundaryViolation {
		t.Fatalf("sibling escape: %+v / %v", result, err)
	}
	convicted := 0
	for _, e := range eventsOf(t, f.store) {
		if e.Kind != drain.BoundaryCheckKind {
			continue
		}
		p := eventPayload(t, e)
		if p["clean"] != false {
			continue
		}
		runs := p["runs"].([]any)
		if len(runs) != 1 || runs[0].(map[string]any)["run_id"] != "run-item_041-a1" {
			t.Fatalf("wrong conviction: %v", p)
		}
		first := p["first_violation"].(map[string]any)
		if first["target"] != filepath.Join(f.laneRoot, "lane-2", "worktree") {
			t.Fatalf("wrong worktree: %v", first)
		}
		convicted++
	}
	if convicted != 1 {
		t.Fatalf("convicted %d brackets, want one", convicted)
	}
}
