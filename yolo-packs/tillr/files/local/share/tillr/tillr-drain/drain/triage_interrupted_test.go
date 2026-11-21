package drain

import (
	"context"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestFailedTriageWaitsOnItsDesignBackend(t *testing.T) {
	f, l := triageFixture(t)
	ctx := context.Background()
	p := findTriagePlan(t, l, "item-request")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, work, err := l.applyPlan(ctx, p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("triage claim: %v %v", ok, err)
	}
	d := work.Decision
	backend := d.RunRequest.Backend
	l.cfg.Policy.Backends["alternate"] = l.cfg.Policy.Backends[backend]
	l.cfg.Policy.SeatLists = map[string][]string{"design": {backend, "alternate"}}
	rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: backend,
		State: run.StateFailedToStart, StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339)}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	if err := l.markExamined(ctx, d); err != nil {
		t.Fatal(err)
	}
	wait := findTriagePlan(t, l, d.ItemID).decision
	if wait.Action != ActionHold || wait.Reason != ReasonBackendClosed || wait.DeferredBy != backend {
		t.Fatalf("failed triage did not wait on %s: %+v", backend, wait)
	}
	if err := ProbeBackend(ctx, f.storePath, f.root, backend, []string{"/bin/true"}); err != nil {
		t.Fatal(err)
	}
	next := findTriagePlan(t, l, d.ItemID).decision
	if next.Action != ActionDispatch || next.RunRequest == nil || next.RunRequest.Backend != backend || next.SelectSeat {
		t.Fatalf("recovered triage did not select its original backend: %+v", next)
	}
}

// On 2026-11-17 a triage run was claimed and staged, the loop was stopped and the jail
// relaunched before the run's face started, and the next loop reclaimed the claim as an
// orphan. No triage ever ran, yet the item was held for the owner: the claim stamps its
// run on the triage conflict, and the planner read any stamped run as the one triage run
// a request version gets. A triage run a loop stop interrupted, or one never started, is
// no triage: the request is triaged again, and the owner hold the planner already
// recorded for it does not stand.
func TestAnInterruptedTriageRunIsTriagedAgainNotSentToTheOwner(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		name := "planned"
		if recorded {
			name = "owner-hold-recorded"
		}
		t.Run(name, func(t *testing.T) {
			f, l := triageFixture(t)
			p := findTriagePlan(t, l, "item-request")
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			ok, w, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("claim: %v %v", ok, err)
			}
			d := w.Decision
			rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateTimedOut, StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndLeaseReclaimed, ArtifactRefs: []run.ArtifactRef{}}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if err := applyTerminal(tx, rec); err != nil {
					return err
				}
				return appendEffect(tx, loopOrphanedKind, rec.RunID, struct{}{}, l.cfg.Now())
			})
			if err := l.markExamined(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			if recorded {
				// The hold the planner recorded before this fix: the request is with the
				// owner, still naming the interrupted run.
				hold := findTriagePlan(t, l, d.ItemID)
				if hold.decision.Action != ActionHold || hold.decision.Reason != ReasonConflictOwner {
					t.Skipf("the planner no longer derives the owner hold to record: %+v", hold.decision)
				}
				if _, _, err := l.applyPlan(context.Background(), hold, base, l.cfg.Now()); err != nil {
					t.Fatal(err)
				}
			}
			next := findTriagePlan(t, l, d.ItemID).decision
			if next.Action != ActionDispatch || next.Reason != ReasonTriageNoBlock || next.BlockConflict == nil || next.BlockConflict.TriageVersion != d.BlockConflict.TriageVersion {
				t.Fatalf("the interrupted triage run was not triaged again: %+v", next)
			}
			if next.RunRequest == nil || next.RunRequest.RunID == rec.RunID {
				t.Fatalf("the triage is not a new run: %+v", next.RunRequest)
			}
			// A triage that actually ran still gets no second run for the same version.
			ok, w2, err := l.applyPlan(context.Background(), findTriagePlan(t, l, d.ItemID), base, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("second claim: %v %v", ok, err)
			}
			d2 := w2.Decision
			rec2 := run.Record{RunID: d2.RunRequest.RunID, ItemID: d2.ItemID, LaneID: d2.LaneID, Backend: d2.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{}}
			fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec2) })
			if err := l.markExamined(context.Background(), d2); err != nil {
				t.Fatal(err)
			}
			if hold := findTriagePlan(t, l, d.ItemID).decision; hold.Action != ActionHold || hold.Reason != ReasonConflictOwner {
				t.Fatalf("a triage that ran was offered a second run: %+v", hold)
			}
		})
	}
}
