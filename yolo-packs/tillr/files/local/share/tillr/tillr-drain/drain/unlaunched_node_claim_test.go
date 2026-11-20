package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// node1Claim brings item-a's workflow to its second node, Ready after the first node
// settled, and claims that node without staging it: the state the 2026-11-19 loop was in
// when staging refused lane-14. It answers the claimed decision.
func node1Claim(t *testing.T) (refusalLoopFixture, *Loop, *restartWorkflowSteps, Decision) {
	t.Helper()
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	graphPass(t, l, result)
	graphPass(t, l, result)
	before := pendingForTest(t, f, "item-a")
	if before.Decision.NodeIndex != 1 || !before.Ready || before.Done {
		t.Fatalf("fixture: item-a is not ready at its second node: %+v", before)
	}
	p := decisionFor(t, l, "item-a")
	ok, work, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v/%v", ok, err)
	}
	return f, l, steps, work.Decision
}

type stagerFunc func(context.Context, StageSpec) error

func (f stagerFunc) Stage(ctx context.Context, spec StageSpec) error { return f(ctx, spec) }

func nodeRunRows(t *testing.T, f refusalLoopFixture, runID string) (n int) {
	t.Helper()
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE run_id=?`, runID).Scan(&n)
	})
	return n
}

func assertClaimRetracted(t *testing.T, f refusalLoopFixture, d Decision) {
	t.Helper()
	runID := d.RunRequest.RunID
	if n := nodeRunRows(t, f, runID); n != 0 {
		t.Fatalf("the unlaunched node %s still has %d node_runs rows (outcome running)", runID, n)
	}
	var dispatches int
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE run_id=?`, runID).Scan(&dispatches)
	})
	if dispatches != 0 {
		t.Fatalf("the unlaunched claim left %d dispatch rows", dispatches)
	}
	if state, holder, _ := laneRow(t, f, d.LaneID); state != "idle" || holder != "" {
		t.Fatalf("the unlaunched claim left %s %s/%s, want idle", d.LaneID, state, holder)
	}
	w := pendingForTest(t, f, "item-a")
	if w.Done || !w.Ready || w.Decision.NodeIndex != d.NodeIndex || w.Decision.RunRequest.RunID != runID {
		t.Fatalf("the pending node was not restored: done=%v ready=%v node=%d run=%s", w.Done, w.Ready, w.Decision.NodeIndex, w.Decision.RunRequest.RunID)
	}
}

// On 2026-11-19 a finished build's workflow advanced to its next node and claimed the lane
// holding the build's own commit; staging refused, and cancelling the claim marked the
// pending node examined and left its node row running. The item then held as
// run-link-missing, "cause not recorded". A cancelled node claim leaves no record of the
// claim, and the next plan offers the same node again.
func TestACancelledNodeClaimIsRetractedAndTheNodeIsOfferedAgain(t *testing.T) {
	f, l, steps, d := node1Claim(t)
	if nodeRunRows(t, f, d.RunRequest.RunID) != 1 {
		t.Fatal("fixture: the claim did not mark its node running")
	}
	if err := l.cancelStaging(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	assertClaimRetracted(t, f, d)
	if got := decisionFor(t, l, "item-a").decision; got.Action != ActionDispatch || got.RunRequest.RunID != d.RunRequest.RunID {
		t.Fatalf("the node is not offered again: %s %s (%s)", got.Action, got.Reason, got.RunRequest.RunID)
	}
	graphPass(t, l, &LoopResult{})
	if n := len(steps.runs); n != 2 || steps.runs[1].RunID != d.RunRequest.RunID {
		t.Fatalf("the cancelled node did not run after the cancellation: %d runs", n)
	}
}

// The same pass offers the node again: a stager refusing the node's first staging with a
// lane-owned refusal sends the claim back, and the re-plan runs the node.
func TestARefusedNodeClaimIsOfferedAgainInTheSamePass(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	graphPass(t, l, result)
	graphPass(t, l, result)
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	if err := l.stageLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	inner := l.cfg.Stage
	refused := 0
	l.cfg.Stage = stagerFunc(func(ctx context.Context, spec StageSpec) error {
		if spec.RunID == "run-item-a-a1-n2" && spec.LaneID == "lane-1" {
			refused++
			return unlandedLaneRefusal(spec.LaneID, "9dd83a278", spec.BaseSHA)
		}
		return inner.Stage(ctx, spec)
	})
	before := len(steps.runs)
	graphPass(t, l, result)
	if refused == 0 {
		t.Fatal("fixture: staging never refused the node's lane")
	}
	ran := false
	for _, req := range steps.runs[before:] {
		if req.RunID == "run-item-a-a1-n2" {
			ran = true
			if req.LaneID != "lane-2" {
				t.Fatalf("the refused node ran on %s, want the free lane-2", req.LaneID)
			}
		}
	}
	if !ran {
		t.Fatalf("the refused node was not offered again in the same pass: ran %d runs", len(steps.runs)-before)
	}
	if w := pendingForTest(t, f, "item-a"); w.Decision.NodeIndex < 1 {
		t.Fatalf("the workflow lost its node: %+v", w.Decision)
	}
}

// A cancelled claim whose records an older cancelStaging left (examined, node still
// running, no dispatch row, lane idle) is recovered at the next pass entry, with no
// operator step: the node row is removed and the pending node is ready again.
func TestACrashLeftNodeClaimIsRetractedBeforeStaging(t *testing.T) {
	f, l, _, d := node1Claim(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := appendEffect(tx, examinationDoneKind, d.RunRequest.RunID, struct{}{}, l.cfg.Now()); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM dispatches WHERE decision_id=? AND item_id=? AND run_id=?`, d.DecisionID, d.ItemID, d.RunRequest.RunID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state='idle',run_id=NULL,leased_until=NULL WHERE lane_id=? AND run_id=?`, d.LaneID, d.RunRequest.RunID)
		return err
	})
	if w := pendingForTest(t, f, "item-a"); !w.Done {
		t.Fatal("fixture: the legacy cancel did not end the pending node")
	}
	if err := l.releaseUnlaunchedClaims(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertClaimRetracted(t, f, d)
	// A second recovery finds nothing to do.
	if err := l.releaseUnlaunchedClaims(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertClaimRetracted(t, f, d)
}

// A node that launched is not an unlaunched claim: its node row, dispatch row and lease
// are left alone by the pass-entry recovery.
func TestAnActiveNodeClaimIsNotRetractedByRecovery(t *testing.T) {
	f, l, _, d := node1Claim(t)
	if err := l.releaseUnlaunchedClaims(context.Background()); err != nil {
		t.Fatal(err)
	}
	if nodeRunRows(t, f, d.RunRequest.RunID) != 1 {
		t.Fatal("recovery retracted a live claim's node row")
	}
}

// Pinning an offered commit changes neither the lane's HEAD nor its status, so the
// fingerprint a re-check compares cannot see it. A lane out of rotation for holding an
// unlanded change returns once a run pin retains that commit: staging clears such a copy.
func TestALaneOutForAnUnlandedChangeReturnsOnceItsHeadIsPinned(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	worktree := laneWorktree(f, "lane-1")
	writeForkFile(t, worktree+"/unlanded.txt", "nobody landed this\n")
	runGit(t, worktree, "add", "unlanded.txt")
	runGit(t, worktree, "commit", "-qm", "an unlanded change")
	steps := &laneSteps{}
	l := f.loop(t, steps, &laneRefusingStager{t: t, f: f, lane: "lane-1"})
	onePass(t, l)
	if c := l.laneConditions["lane-1"]; c == nil || !c.out {
		t.Fatal("fixture: lane-1 did not leave rotation")
	}
	if err := l.recheckLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := l.laneConditions["lane-1"]; c == nil || !c.out {
		t.Fatal("an unchanged, unpinned lane returned to rotation")
	}
	head := runGit(t, worktree, "rev-parse", "HEAD")
	runGit(t, f.root, "update-ref", "refs/tillr/runs/run-item-a-a1", head)
	if err := l.recheckLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := l.laneConditions["lane-1"]; c == nil || c.out {
		t.Fatal("a lane whose head is now pinned stayed out of rotation")
	}
}
