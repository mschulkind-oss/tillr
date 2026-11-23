package drain

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type fixerLandingHeavySteps struct {
	recordingSteps
	root   string
	t      *testing.T
	onLand func()
	judge  func(context.Context, string, string) ([]HeavyCheck, error)
}

func (s *fixerLandingHeavySteps) Land(_ context.Context, c Candidate) (LandOutcome, error) {
	writeForkFile(s.t, filepath.Join(s.root, "repaired.txt"), c.ItemID+"\n")
	runGit(s.t, s.root, "add", "repaired.txt")
	runGit(s.t, s.root, "commit", "-qm", "land "+c.ItemID)
	tip := runGit(s.t, s.root, "rev-parse", TrunkRef)
	if s.onLand != nil {
		s.onLand()
	}
	return LandOutcome{Result: LandLanded, TrunkAfter: tip}, nil
}

func (s *fixerLandingHeavySteps) JudgeHeavy(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
	return s.judge(ctx, dir, only)
}

// Reproduces the 2026-11-22 hold: while candidate gates hold restartMechanical and a
// loop restart is pending, a trunk fixer landing must still start and complete a proof
// of the new tip without waiting for the restart or for the running gates to finish,
// and without being cancelled when the fixer's examination finishes.
func TestHeavyProofStartsAfterFixerLandsWhileCandidateGatesRunAndRestartPending(t *testing.T) {
	h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil }, "fixer-main-deadbeef")
	h.l.gatePolicy.LandingStrategy = "after-merge"
	h.l.gatePolicy.LaneBase = "green"

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	// Seed a green base behind a newer red trunk tip, as on 2026-11-22.
	base := heavyGit(t, h.f.root, "rev-parse", TrunkRef)
	heavyGit(t, h.f.root, "update-ref", GreenRef, base)
	heavyCommit(t, h.f.root, "red-landing")

	c := prepareReadyCandidate(t, h.f, h.l, "fixer-main-deadbeef")
	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, h.f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE lanes SET state=?, run_id=?, leased_until=NULL WHERE lane_id=?`, run.LaneStateLeased, c.RunID, c.LaneID); err != nil {
			return err
		}
		return appendEffect(tx, examinationOwnerKind, c.RunID, here, h.l.cfg.Now())
	})

	entered := make(chan string, 1)
	releaseProof := make(chan struct{})
	var proofOnce sync.Once
	steps := &fixerLandingHeavySteps{
		root: h.f.root,
		t:    t,
		onLand: func() {
			// File a restart while the fixer landing and candidate gate both already
			// hold restartMechanical, before landCandidate triggers startHeavy.
			h.request(t, 1)
			if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
				t.Errorf("gateRestart while candidate gate active = %v, %q, %v; want deferred", handled, stop, err)
			}
		},
		judge: func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
			sha := heavyGit(t, dir, "rev-parse", "HEAD")
			proofOnce.Do(func() { entered <- sha })
			select {
			case <-releaseProof:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
		},
	}
	h.l.cfg.Steps = steps

	// Simulate an active candidate gate holding restartMechanical (and a settlement)
	// while a loop restart is pending, so gateRestart cannot exec.
	_, releaseGate, err := h.l.lockRestartSettlement(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if releaseGate != nil {
			releaseGate()
		}
	}()

	// Land the fixer through launchExamination (the real path whose per-examination
	// context cancels as soon as examineClaimed returns).
	h.l.completions = make(chan runCompletion, 1)
	d := Decision{
		Action:     ActionJudge,
		ItemID:     c.ItemID,
		LaneID:     c.LaneID,
		RunRequest: &run.Request{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID},
	}
	work := &pendingWork{Decision: d, Candidate: &c, BaseSHA: c.BaseSHA, Gated: true}
	h.l.launchExamination(ctx, d, work, h.l.cfg.Now(), &PassTiming{}, nil)

	var completion runCompletion
	select {
	case completion = <-h.l.completions:
	case <-time.After(hangGuard):
		t.Fatal("fixer examination did not complete")
	}
	h.l.examinations.Wait()
	if completion.outcome.err != nil || completion.result.Landed != 1 {
		t.Fatalf("fixer landing outcome=%+v result=%+v", completion.outcome, completion.result)
	}

	repairedTip := heavyGit(t, h.f.root, "rev-parse", TrunkRef)

	// While the candidate gate is still running and the restart is still pending,
	// the proof of the fixer's new tip must have started.
	var provedTip string
	select {
	case provedTip = <-entered:
	case <-time.After(5 * time.Second):
		close(releaseProof)
		h.l.heavyWG.Wait()
		t.Fatalf("trunk proof of repaired tip %s did not start while candidate gate ran and restart was pending", shortSHA(repairedTip))
	}
	if provedTip != repairedTip {
		close(releaseProof)
		h.l.heavyWG.Wait()
		t.Fatalf("proof started on %s, want repaired tip %s", shortSHA(provedTip), shortSHA(repairedTip))
	}

	// Complete the proof while the candidate gate is still running and the restart is still pending.
	close(releaseProof)
	h.l.heavyWG.Wait()

	if got := heavyGit(t, h.f.root, "rev-parse", GreenRef); got != repairedTip {
		t.Fatalf("refs/tillr/green = %s, want repaired tip %s (proof was cancelled or did not finish)", shortSHA(got), shortSHA(repairedTip))
	}

	// Even with a non-settling candidate examination holding restartMechanical (restartSettlements == 0)
	// and a restart still pending, maybeRolling starts a proof for the next tip.
	releaseGate()
	releaseGate = nil
	// Hold the read lock directly to represent an already-running non-settling examination.
	h.l.restartMechanical.RLock()
	releaseMech := func() { h.l.restartMechanical.RUnlock() }
	nextTip := heavyCommit(t, h.f.root, "after-fixer")
	if err := h.l.maybeRolling(ctx, nextTip, false, &LoopResult{}); err != nil {
		releaseMech()
		t.Fatalf("maybeRolling while restart pending and examination running: %v", err)
	}
	h.l.heavyWG.Wait()
	if got := heavyGit(t, h.f.root, "rev-parse", GreenRef); got != nextTip {
		releaseMech()
		t.Fatalf("refs/tillr/green = %s, want %s", shortSHA(got), shortSHA(nextTip))
	}

	// Once the running examination releases restartMechanical, the pending restart succeeds.
	releaseMech()
	if _, stop, err := h.l.gateRestart(ctx, &LoopResult{}); err != nil || stop != StopRestarted {
		t.Fatalf("gateRestart after proof and gate finished = %q, %v; want %q", stop, err, StopRestarted)
	}
}
