package drain

import (
	"bytes"
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestFirstPassDispatchAcrossSiblingHistoryAfterRestart(t *testing.T) {
	f, old := passFixture(t, "item-a", "item-b")
	plans := mustPlan(t, old)
	a, b := plans[0], plans[1]
	if a.decision.ItemID != "item-a" || b.decision.ItemID != "item-b" || a.decision.LaneID == b.decision.LaneID {
		t.Fatalf("expected two offers on separate lanes: %+v %+v", a.decision, b.decision)
	}
	owner := relaunchedJailClaim(t, f, old, a)
	recordSession(t, f, owner, true)
	runStreams(t, f, a.decision.RunRequest.RunID)

	steps := &recordingSteps{}
	next := f.loop(t, streamSteps{steps, f.root}, &itemContentStager{})
	var report bytes.Buffer
	next.cfg.Report = &report
	ctx := context.Background()
	if err := next.stageLanes(ctx); err != nil {
		t.Fatal(err)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	// A sibling publishes a claim on its own lane while the recovered item
	// gathers Git/file facts with the writer unlocked. The pending snapshot
	// changes, but the recovered item's lane and claim inputs do not.
	changed := false
	writeGatheredHooks.Fetch = func(string) {
		if changed {
			return
		}
		changed = true
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return appendEffect(tx, dispatchStartedKind, b.decision.RunRequest.RunID, pendingWork{Decision: b.decision, BaseSHA: base}, next.cfg.Now())
		})
	}
	t.Cleanup(func() { writeGatheredHooks.Fetch = nil })
	var result LoopResult
	stop, err := next.pass(ctx, &result)
	writeGatheredHooks.Fetch = nil
	if err != nil || stop != "" {
		t.Fatalf("first pass stopped: %q, %v", stop, err)
	}
	drainOwned(t, next, &result)
	if !changed || !steps.ran("item-a") {
		t.Fatalf("first pass did not recover and start item-a (sibling claim=%v):\n%s", changed, report.String())
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var reaped bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_id=?)`, loopOrphanedKind, a.decision.RunRequest.RunID).Scan(&reaped); err != nil {
			return err
		}
		if !reaped {
			t.Fatal("the ended worker's claim was not reaped on the first pass")
		}
		return nil
	})
}
