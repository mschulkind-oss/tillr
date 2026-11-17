package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestInterruptedHeldReleaseFinishesFromIntentAfterHoldCleared(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	d := mustPlan(t, l)[0].decision
	if err := l.holdItemFault(context.Background(), c, StopGateHarnessError, "gate broke"); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER fail_release BEFORE INSERT ON events WHEN NEW.kind='drain.candidate-lane' BEGIN SELECT RAISE(ABORT,'release interrupted'); END`)
		return err
	})
	_, err := l.releaseHeldCandidate(context.Background(), &pendingWork{Decision: d, Candidate: &c}, StopGateHarnessError)
	if err == nil {
		t.Fatal("release was not interrupted after reset")
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.BaseSHA {
		t.Fatalf("fixture did not interrupt reset: %s", got)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`DROP TRIGGER fail_release`); err != nil {
			return err
		}
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		return ClearItemFault(tx, c.ItemID, faults[c.ItemID].Seq, l.cfg.Now())
	})
	steps := &heldHeadSteps{}
	replacement := f.loop(t, steps, &itemContentStager{})
	replacement.cfg.Lanes = []string{"lane-1", "lane-2"}
	examineHeldCandidate(t, replacement)
	if steps.candidate.RunID != c.RunID || steps.candidate.HeadSHA != c.HeadSHA {
		t.Fatalf("interrupted release stranded candidate: %+v", steps.candidate)
	}
}

func TestOperatorExamineReclaimsReleasedCandidateWithoutOldLane(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	examineHeldCandidate(t, l)
	sibling := mustPlan(t, l)[1]
	ok, _, err := l.applyPlan(context.Background(), sibling, c.BaseSHA, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("take original lane: %v/%v", ok, err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		return ClearItemFault(tx, c.ItemID, faults[c.ItemID].Seq, l.cfg.Now())
	})
	steps := &heldHeadSteps{}
	l.cfg.Steps, l.cfg.Lanes = steps, []string{"lane-1", "lane-2"}
	result, err := l.Examine(context.Background(), c.RunID)
	if err != nil || result.Landed != 1 {
		t.Fatalf("operator examination failed to reclaim pin: %+v/%v", result, err)
	}
	if steps.candidate.LaneID != "lane-2" || steps.candidate.HeadSHA != c.HeadSHA {
		t.Fatalf("operator did not stage same head elsewhere: %+v", steps.candidate)
	}
	if got := runGit(t, l.lanePath(c.LaneID), "rev-parse", "HEAD"); got != c.BaseSHA {
		t.Fatalf("operator wrote sibling lane: %s", got)
	}
}

func TestSetAsideReleasedCandidateLeavesSiblingLeaseAndCheckoutAlone(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	examineHeldCandidate(t, l)
	p := mustPlan(t, l)[1]
	ok, _, err := l.applyPlan(context.Background(), p, c.BaseSHA, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim sibling: %v/%v", ok, err)
	}
	ref, err := l.SetAside(context.Background(), c.ItemID, "operator requests rework")
	if err != nil || ref != "refs/tillr/runs/"+c.RunID {
		t.Fatalf("set aside released pin: %s/%v", ref, err)
	}
	if got := runGit(t, l.lanePath(c.LaneID), "rev-parse", "HEAD"); got != c.BaseSHA {
		t.Fatalf("set aside changed sibling checkout: %s", got)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var id string
		if err := r.QueryRow(`SELECT run_id FROM lanes WHERE lane_id=?`, c.LaneID).Scan(&id); err != nil {
			return err
		}
		if id != p.decision.RunRequest.RunID {
			t.Errorf("set aside cleared sibling's lease: %s", id)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOperatorExamineIgnoresUnexecutedHigherRankSibling(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	examineHeldCandidate(t, l)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET rank=CASE item_id WHEN 'item-b' THEN 'a' ELSE 'b' END`); err != nil {
			return err
		}
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		return ClearItemFault(tx, c.ItemID, faults[c.ItemID].Seq, l.cfg.Now())
	})
	steps := &heldHeadSteps{}
	l.cfg.Steps, l.cfg.Lanes = steps, []string{"lane-2"}
	result, err := l.Examine(context.Background(), c.RunID)
	if err != nil || result.Landed != 1 {
		t.Fatalf("unexecuted sibling consumed recovery capacity: %+v/%v", result, err)
	}
	if steps.candidate.HeadSHA != c.HeadSHA || steps.candidate.LaneID != "lane-2" {
		t.Fatalf("wrong recovery candidate: %+v", steps.candidate)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var starts int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='drain.started' AND json_extract(payload,'$.decision.item_id')='item-b'`).Scan(&starts); err != nil {
			return err
		}
		if starts != 0 {
			t.Errorf("operator dispatched sibling %d time(s)", starts)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHeldRepairKeepsUnexpectedCommittedHead(t *testing.T) {
	_, l, c := heldCandidateFixture(t)
	d := mustPlan(t, l)[0].decision
	file := filepath.Join(c.Worktree, "inspection.txt")
	writeForkFile(t, file, "operator inspection commit\n")
	runGit(t, c.Worktree, "add", "inspection.txt")
	runGit(t, c.Worktree, "commit", "-qm", "inspection")
	unexpected := runGit(t, c.Worktree, "rev-parse", "HEAD")
	w := &pendingWork{Decision: d, Candidate: &c, RepairHold: &repairHold{TrunkSHA: c.BaseSHA, Candidate: c}}
	released, err := l.releaseHeldCandidate(context.Background(), w, ReasonRepairStagingFailed)
	if err != nil || released {
		t.Fatalf("repair discarded unexpected commit: %v/%v", released, err)
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != unexpected {
		t.Fatalf("repair reset operator's commit: %s", got)
	}
}

func TestImmediateRepairHoldReleaseReadsDurableResetBasis(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	d := mustPlan(t, l)[0].decision
	writeForkFile(t, filepath.Join(f.root, "trunk.txt"), "repair trunk\n")
	runGit(t, f.root, "add", "trunk.txt")
	runGit(t, f.root, "commit", "-qm", "repair trunk")
	trunk := runGit(t, f.root, "rev-parse", "HEAD")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, repairHeldKind, c.RunID, repairHold{Candidate: c, TrunkSHA: trunk}, l.cfg.Now())
	})
	runGit(t, c.Worktree, "reset", "--hard", trunk)
	// The examination's in-memory snapshot predates the repair hold event.
	w := &pendingWork{Decision: d, Candidate: &c}
	released, err := l.releaseHeldCandidate(context.Background(), w, ReasonRepairStagingFailed)
	if err != nil || !released {
		t.Fatalf("recorded repair reset not admitted: %v/%v", released, err)
	}
	if got := runGit(t, f.root, "rev-parse", "refs/tillr/runs/"+c.RunID); got != c.HeadSHA {
		t.Fatalf("repair lost original candidate: %s", got)
	}
}

func TestRepairResetUsesCapturedTrunkNotMovingMain(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	captured := runGit(t, f.root, "rev-parse", TrunkRef)
	writeForkFile(t, filepath.Join(f.root, "newer.txt"), "later trunk\n")
	runGit(t, f.root, "add", "newer.txt")
	runGit(t, f.root, "commit", "-qm", "later trunk")
	later := runGit(t, f.root, "rev-parse", TrunkRef)
	if err := l.resetLaneTo(context.Background(), c.LaneID, captured); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != captured {
		t.Fatalf("reset chased moving trunk: %s", got)
	}
	if got := runGit(t, f.root, "rev-parse", TrunkRef); got != later {
		t.Fatal("reset moved main")
	}
}

func TestPinnedStagingRefusesDirtyAlreadyMaterializedHead(t *testing.T) {
	_, l, c := heldCandidateFixture(t)
	if err := l.pin(context.Background(), c.RunID, c.HeadSHA); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(c.Worktree, "candidate.txt")
	writeForkFile(t, file, "owner's inspection edits\n")
	w := &pendingWork{Candidate: &c, HeldFence: "candidate.txt\n", NeedsStage: true}
	if err := l.stagePinnedCandidate(context.Background(), w); err == nil {
		t.Fatal("dirty materialized candidate accepted")
	}
	if raw, err := os.ReadFile(file); err != nil || string(raw) != "owner's inspection edits\n" {
		t.Fatalf("inspection edits lost: %q/%v", raw, err)
	}
}
