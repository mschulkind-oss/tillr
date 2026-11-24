package drain

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
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

type gateCountingHeavySteps struct {
	recordingSteps
	gateCalls atomic.Int32
	judge     func(context.Context, string, string) ([]HeavyCheck, error)
}

func (s *gateCountingHeavySteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.gateCalls.Add(1)
	return s.recordingSteps.Gate(ctx, c)
}

func (s *gateCountingHeavySteps) JudgeHeavy(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
	return s.judge(ctx, dir, only)
}

func TestPendingRestartDeferredByTrunkProofStopsNewGatesAndSecondProof(t *testing.T) {
	h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil }, "item-a")
	h.l.gatePolicy.LandingStrategy = "after-merge"
	h.l.gatePolicy.LaneBase = "green"

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	base := heavyGit(t, h.f.root, "rev-parse", TrunkRef)
	heavyGit(t, h.f.root, "update-ref", GreenRef, base)
	tip1 := heavyCommit(t, h.f.root, "tip-1")

	c := prepareReadyCandidate(t, h.f, h.l, "item-a")
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

	var (
		provedMu   sync.Mutex
		provedTips []string
	)
	enteredProof1 := make(chan string, 1)
	releaseProof1 := make(chan struct{})
	proof1Released := false
	defer func() {
		if !proof1Released {
			close(releaseProof1)
			h.l.heavyWG.Wait()
		}
	}()

	steps := &gateCountingHeavySteps{
		judge: func(jctx context.Context, dir, only string) ([]HeavyCheck, error) {
			sha := heavyGit(t, dir, "rev-parse", "HEAD")
			provedMu.Lock()
			provedTips = append(provedTips, sha)
			first := len(provedTips) == 1
			provedMu.Unlock()
			if first {
				enteredProof1 <- sha
				select {
				case <-releaseProof1:
				case <-jctx.Done():
					return nil, jctx.Err()
				}
			}
			return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
		},
	}
	h.l.cfg.Steps = steps

	// Start the first trunk proof and wait until it is active.
	if err := h.l.startHeavy(ctx, tip1); err != nil {
		t.Fatalf("startHeavy(tip1): %v", err)
	}
	select {
	case got := <-enteredProof1:
		if got != tip1 {
			t.Fatalf("first proof started on %s, want %s", shortSHA(got), shortSHA(tip1))
		}
	case <-time.After(hangGuard):
		t.Fatal("first trunk proof did not start")
	}

	// File a pending restart while the trunk proof is active.
	req := h.request(t, 1)[0]
	if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
		t.Fatalf("gateRestart while trunk proof active = %v, %q, %v; want deferred", handled, stop, err)
	}

	// Workers keep starting and running while the restart is deferred by the trunk proof.
	if pending, err := h.l.buildRestartPending(ctx); err != nil || pending {
		t.Fatalf("buildRestartPending while trunk proof active = %v, %v; want false, nil", pending, err)
	}
	if _, unlockMech, err := h.l.lockRestartMechanical(ctx); err != nil {
		t.Fatalf("lockRestartMechanical while trunk proof active = %v; want nil", err)
	} else {
		unlockMech()
	}
	procSteps := &ProcessSteps{Store: h.f.storePath}
	cmd := exec.Command("true")
	handoffCtx := context.WithValue(ctx, runStartAdmissionKey{}, h.l)
	if err := procSteps.startRunFaceContext(handoffCtx, cmd, run.Request{RunID: "worker-while-proof"}); err != nil {
		t.Fatalf("worker handoff while trunk proof active = %v; want nil", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}

	// No gate starts for the finished candidate, either directly from settlement or via ActionJudge.
	d := Decision{
		Action:     ActionJudge,
		ItemID:     c.ItemID,
		LaneID:     c.LaneID,
		RunRequest: &run.Request{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID},
	}
	if claimed, ok, err := h.l.claimSettledCandidateJudge(ctx, d, &PassTiming{}); err != nil || ok || claimed != nil {
		t.Fatalf("claimSettledCandidateJudge while proof active and restart pending = (%v, %v, %v); want (nil, false, nil)", claimed != nil, ok, err)
	}

	waiting := make(chan struct{}, 1)
	h.l.restartWaitObs = func() {
		select {
		case waiting <- struct{}{}:
		default:
		}
	}
	h.l.completions = make(chan runCompletion, 1)
	work := &pendingWork{Decision: d, Candidate: &c, BaseSHA: c.BaseSHA}
	h.l.launchExamination(ctx, d, work, h.l.cfg.Now(), &PassTiming{}, nil)
	awaitRestartTest(t, waiting, "finished candidate waiting on pending restart")
	if got := steps.gateCalls.Load(); got != 0 {
		t.Fatalf("gateCalls while trunk proof active = %d, want 0", got)
	}

	// Advance trunk and trigger heavy while proof 1 is active; no second proof may start.
	tip2 := heavyCommit(t, h.f.root, "tip-2")
	if err := h.l.startHeavy(ctx, tip2); err != nil {
		t.Fatalf("startHeavy(tip2): %v", err)
	}

	// Let the first trunk proof finish.
	close(releaseProof1)
	proof1Released = true
	h.l.heavyWG.Wait()

	if got := heavyGit(t, h.f.root, "rev-parse", GreenRef); got != tip1 {
		t.Fatalf("refs/tillr/green = %s, want %s", shortSHA(got), shortSHA(tip1))
	}
	// A subsequent rolling trigger before gateRestart also starts no second proof.
	if err := h.l.maybeRolling(ctx, tip2, false, &LoopResult{}); err != nil {
		t.Fatalf("maybeRolling(tip2) after proof 1 ended: %v", err)
	}
	h.l.heavyWG.Wait()

	provedMu.Lock()
	gotProved := append([]string(nil), provedTips...)
	provedMu.Unlock()
	if len(gotProved) != 1 || gotProved[0] != tip1 {
		t.Fatalf("proved tips = %v, want only [%s]", gotProved, shortSHA(tip1))
	}
	if got := steps.gateCalls.Load(); got != 0 {
		t.Fatalf("gateCalls after proof 1 ended = %d, want 0", got)
	}

	// When the first proof ends, the restart replaces the loop and is answered "restarted".
	if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); !handled || stop != StopRestarted || err != nil {
		t.Fatalf("gateRestart after first proof ended = %v, %q, %v; want replaced", handled, stop, err)
	}
	if len(h.execed) == 0 {
		t.Fatal("replacement did not exec")
	}

	var completion runCompletion
	select {
	case completion = <-h.l.completions:
	case <-time.After(hangGuard):
		t.Fatal("waiting candidate examination did not unblock after replacement")
	}
	h.l.examinations.Wait()
	if !errors.Is(completion.outcome.err, errRestartPending) {
		t.Fatalf("candidate examination err = %v, want errRestartPending", completion.outcome.err)
	}
	if got := steps.gateCalls.Load(); got != 0 {
		t.Fatalf("gateCalls after replacement = %d, want 0", got)
	}

	t.Setenv(restart.RequestEnv, req.ID)
	t.Setenv(restart.FromEnv, "old-build")
	if err := h.l.adoptRestart(ctx); err != nil {
		t.Fatalf("adoptRestart: %v", err)
	}
	if ans := h.answer(t, req.ID); ans.Answer != restart.AnswerRestarted {
		t.Fatalf("restart answer = %+v, want %q", ans, restart.AnswerRestarted)
	}
}

// Reproduces the 2026-11-23 20:46 sequence: a drain restart request is pending
// while a trunk proof runs with [push] what = "green" enabled, a newer unproved
// tip is on trunk, and finished candidates are waiting. When the proof ends
// green, the loop must restart before starting another trunk proof or any gate.
func TestPendingRestartAfterGreenProofRestartsBeforeNextProofAndGates(t *testing.T) {
	h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil }, "item-a", "item-b")
	h.l.gatePolicy.LandingStrategy = "after-merge"
	h.l.gatePolicy.LaneBase = "green"

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	// Configure a remote with [push] what = "green" and a gated pre-push hook
	// so any green push started while the restart is pending stays active across
	// the proof boundary and next pass, matching a live network push.
	remote := filepath.Join(t.TempDir(), "upstream.git")
	runGit(t, h.f.root, "init", "--bare", remote)
	runGit(t, h.f.root, "remote", "add", "origin", remote)
	runGit(t, h.f.root, "push", "-u", "origin", TrunkRef)
	releasePush := filepath.Join(t.TempDir(), "release-push")
	hook := filepath.Join(h.f.root, ".git", "hooks", "pre-push")
	script := "#!/bin/sh\nwhile [ ! -f " + releasePush + " ]; do sleep 0.01; done\n"
	if err := writeForkSafe(hook, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = writeForkSafe(releasePush, []byte("go\n"), 0o644)
		waitAction(t, &h.l.pushAction)
	}()
	h.l.cfg.PostLanding.Push.What = settings.PushGreen

	base := heavyGit(t, h.f.root, "rev-parse", TrunkRef)
	heavyGit(t, h.f.root, "update-ref", GreenRef, base)
	tip1 := heavyCommit(t, h.f.root, "tip-1")

	c1 := prepareReadyCandidate(t, h.f, h.l, "item-a")
	c2 := prepareReadyCandidate(t, h.f, h.l, "item-b")
	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, h.f, func(tx *store.Tx) error {
		for _, c := range []Candidate{c1, c2} {
			if _, err := tx.Exec(`UPDATE lanes SET state=?, run_id=?, leased_until=NULL WHERE lane_id=?`, run.LaneStateLeased, c.RunID, c.LaneID); err != nil {
				return err
			}
			if err := appendEffect(tx, examinationOwnerKind, c.RunID, here, h.l.cfg.Now()); err != nil {
				return err
			}
		}
		return nil
	})

	var (
		provedMu   sync.Mutex
		provedTips []string
	)
	enteredProof1 := make(chan string, 1)
	releaseProof1 := make(chan struct{})
	proof1Released := false
	defer func() {
		if !proof1Released {
			close(releaseProof1)
			h.l.heavyWG.Wait()
		}
	}()

	steps := &gateCountingHeavySteps{
		judge: func(jctx context.Context, dir, only string) ([]HeavyCheck, error) {
			sha := heavyGit(t, dir, "rev-parse", "HEAD")
			provedMu.Lock()
			provedTips = append(provedTips, sha)
			first := len(provedTips) == 1
			provedMu.Unlock()
			if first {
				enteredProof1 <- sha
				select {
				case <-releaseProof1:
				case <-jctx.Done():
					return nil, jctx.Err()
				}
			}
			return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
		},
	}
	h.l.cfg.Steps = steps

	// Start the first trunk proof and wait until it is active.
	if err := h.l.startHeavy(ctx, tip1); err != nil {
		t.Fatalf("startHeavy(tip1): %v", err)
	}
	select {
	case got := <-enteredProof1:
		if got != tip1 {
			t.Fatalf("first proof started on %s, want %s", shortSHA(got), shortSHA(tip1))
		}
	case <-time.After(hangGuard):
		t.Fatal("first trunk proof did not start")
	}

	// File a drain restart request while proof 1 is active, and arm the barrier.
	req := h.request(t, 1)[0]
	if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
		t.Fatalf("gateRestart while trunk proof active = %v, %q, %v; want deferred", handled, stop, err)
	}

	// One candidate is already waiting in launchExamination on the pending restart,
	// while the second waits in the store for the next pass.
	waiting := make(chan struct{}, 1)
	h.l.restartWaitObs = func() {
		select {
		case waiting <- struct{}{}:
		default:
		}
	}
	h.l.completions = make(chan runCompletion, 4)
	d1 := Decision{
		Action:     ActionJudge,
		ItemID:     c1.ItemID,
		LaneID:     c1.LaneID,
		RunRequest: &run.Request{RunID: c1.RunID, ItemID: c1.ItemID, LaneID: c1.LaneID},
	}
	h.l.launchExamination(ctx, d1, &pendingWork{Decision: d1, Candidate: &c1, BaseSHA: c1.BaseSHA}, h.l.cfg.Now(), &PassTiming{}, nil)
	awaitRestartTest(t, waiting, "finished candidate waiting on pending restart")

	// Advance trunk to a newer unproved tip while proof 1 is still running.
	tip2 := heavyCommit(t, h.f.root, "tip-2")

	// End proof 1 green.
	close(releaseProof1)
	proof1Released = true
	h.l.heavyWG.Wait()

	if got := heavyGit(t, h.f.root, "rev-parse", GreenRef); got != tip1 {
		t.Fatalf("refs/tillr/green = %s, want %s (newer unproved tip %s must not be proved before restart)", shortSHA(got), shortSHA(tip1), shortSHA(tip2))
	}

	// The next pass must restart instead of starting proof 2 or any candidate gate.
	stop, err := h.l.pass(ctx, &LoopResult{})
	if err != nil {
		t.Fatalf("pass after proof 1 ended: %v", err)
	}
	if stop != StopRestarted {
		t.Fatalf("pass stop after proof 1 ended = %q, want %q", stop, StopRestarted)
	}
	if len(h.execed) == 0 {
		t.Fatal("replacement did not exec")
	}

	h.l.heavyWG.Wait()
	h.l.examinations.Wait()

	provedMu.Lock()
	gotProved := append([]string(nil), provedTips...)
	provedMu.Unlock()
	if len(gotProved) != 1 || gotProved[0] != tip1 {
		t.Fatalf("proved tips before restart = %v, want only [%s]", gotProved, shortSHA(tip1))
	}
	if got := steps.gateCalls.Load(); got != 0 {
		t.Fatalf("gateCalls before restart = %d, want 0", got)
	}

	// The restarted loop answers the request "restarted".
	t.Setenv(restart.RequestEnv, req.ID)
	t.Setenv(restart.FromEnv, "old-build")
	if err := h.l.adoptRestart(ctx); err != nil {
		t.Fatalf("adoptRestart: %v", err)
	}
	if ans := h.answer(t, req.ID); ans.Answer != restart.AnswerRestarted {
		t.Fatalf("restart answer = %+v, want %q", ans, restart.AnswerRestarted)
	}
}
