package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// On 2026-11-19 a loop claimed lane-4 for item-doc-triage's four parallel readers
// (run-…-a2-n9, -n37, -n38, -n39), then refused to stage them: the lane held a candidate
// nobody had landed. cancelStaging released lane-4, but the four read_only_runs
// memberships the claim had written stayed unsettled. No reader process ever started,
// yet every later lease on lane-4 — the trunk fixer's own run — was read back with those
// four readers as live runs beside it ("lease not recorded"), so lane-4 looked occupied
// by work that would never end. A cancelled claim must take its readers' memberships
// with it, the way run.freeLane settles a released group's members.
func TestCancelledStagingLeavesNoUnsettledParallelReaders(t *testing.T) {
	f, l, _ := parallelFixture(t)
	plan := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, work, err := l.applyPlan(context.Background(), plan, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v/%v", ok, err)
	}
	d := work.Decision
	if len(d.Parallel) == 0 {
		t.Fatalf("fixture: the first scout claimed no parallel siblings: %+v", d)
	}
	count := func() (members, unsettled int) {
		fixtureRead(t, f, func(r *store.Reader) error {
			return r.QueryRow(`SELECT count(*), COALESCE(SUM(settled=0),0) FROM read_only_runs WHERE lane_id=?`, d.LaneID).Scan(&members, &unsettled)
		})
		return members, unsettled
	}
	if members, _ := count(); members != len(d.Parallel)+1 {
		t.Fatalf("fixture: the claim recorded %d memberships, want %d", members, len(d.Parallel)+1)
	}
	if err := l.cancelStaging(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if state, runID, _ := laneRow(t, f, d.LaneID); state != "idle" || runID != "" {
		t.Fatalf("the cancelled claim left %s %s/%s, want idle", d.LaneID, state, runID)
	}
	if _, unsettled := count(); unsettled != 0 {
		t.Fatalf("the cancelled claim left %d never-started readers unsettled on %s", unsettled, d.LaneID)
	}
	// The same lane must then be claimable by the next plan as an ordinary lane.
	next := mustPlan(t, l)[0]
	if next.decision.LaneID != d.LaneID {
		t.Fatalf("the re-plan chose %s, want the released %s", next.decision.LaneID, d.LaneID)
	}
}
