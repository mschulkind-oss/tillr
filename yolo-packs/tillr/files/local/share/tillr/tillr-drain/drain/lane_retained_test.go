package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// A settled run whose uncommitted work cannot be preserved is one lane's condition. On
// 2026-11-17 the loop stopped on it ("lane lane-14 retained: could not preserve uncommitted
// work: ... database is locked"), taking every other lane down with it. The lane must still
// be retained — not reset, and not released for the next staging to clear — but by holding
// that run's item, so the rest of the loop goes on. The preservation is made to fail here
// deterministically: a preserved ref under the run's name already records another run.
func TestAPreservationFailureHoldsTheItemInsteadOfStoppingTheLoop(t *testing.T) {
	f, l, d, rec := savedFixture(t)
	ctx := context.Background()
	lane := d.LaneID
	tree := runGit(t, f.root, "rev-parse", TrunkRef+"^{tree}")
	foreign := runGit(t, f.root, "commit-tree", tree, "-m", `{"run_id":"run-someone-else","ref":"`+preservedPrefix+rec.RunID+`"}`)
	runGit(t, f.root, "update-ref", preservedPrefix+rec.RunID, foreign)

	p := mustPlan(t, l)[0]
	if p.decision.Action != ActionSettle {
		t.Fatalf("the fixture's run is not up for settlement: %+v", p.decision)
	}
	ok, work, err := l.applyPlan(ctx, p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim examination: %v/%v", ok, err)
	}
	l.launchExamination(ctx, p.decision, work, l.cfg.Now(), &PassTiming{}, nil)
	done := <-l.completions
	l.examinations.Wait()
	if done.outcome.err != nil {
		t.Fatalf("the settlement answered an error instead of retaining the lane: %v", done.outcome.err)
	}
	if done.outcome.stop == StopFault {
		t.Fatal("a preservation failure in one lane stopped the loop")
	}
	if done.outcome.stop == "" {
		t.Fatal("the settlement went on as if the work were preserved")
	}
	var result LoopResult
	if stop, err := l.takeCompletion(done, &result); stop != "" || err != nil {
		t.Fatalf("the loop stopped on one lane's retained work: %q/%v", stop, err)
	}
	if raw, err := os.ReadFile(filepath.Join(l.laneTarget(lane), "base.txt")); err != nil || string(raw) != "earlier work\n" {
		t.Fatalf("the retained lane's unpreserved work was touched: %q/%v", raw, err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		if w := works[d.ItemID]; w == nil || w.Done {
			t.Fatalf("the run was recorded examined, which releases its lane to be cleared: %+v", w)
		}
		var state string
		if err := r.QueryRow(`SELECT state FROM lanes WHERE lane_id = ?`, lane).Scan(&state); err != nil {
			return err
		}
		if state == "idle" {
			t.Fatal("the retained lane was released to the next staging")
		}
		return nil
	})
	if p := mustPlan(t, l)[0].decision; p.Action != ActionHold || p.Reason != ReasonPreserveFailed {
		t.Fatalf("the item is not held for its unpreserved work, so it would be re-examined every pass: %+v", p)
	}
}
