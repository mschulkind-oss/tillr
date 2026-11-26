package drain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// While candidate gates hold restartMechanical and a loop restart is pending,
// a new trunk proof (from a fixer landing or maybeRolling) must wait for the
// pending restart to be answered rather than starting and extending the wait.
func TestHeavyProofWaitsForPendingRestartWhileCandidateGatesRun(t *testing.T) {
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

	var proofStarts atomic.Int32
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
			proofStarts.Add(1)
			return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
		},
	}
	h.l.cfg.Steps = steps

	// Simulate an active candidate gate holding restartMechanical (and a settlement)
	// while a loop restart is pending, so gateRestart cannot exec yet.
	_, releaseGate, err := h.l.lockRestartSettlement(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if releaseGate != nil {
			releaseGate()
		}
	}()

	// Land the fixer through launchExamination.
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

	// The startHeavy triggered by the fixer landing must decline while the restart
	// is pending, even though the candidate gate still holds restartMechanical.
	h.l.heavyWG.Wait()
	if got := proofStarts.Load(); got != 0 {
		t.Fatalf("proofStarts while candidate gate ran and restart was pending = %d, want 0", got)
	}
	if got := heavyGit(t, h.f.root, "rev-parse", GreenRef); got != base {
		t.Fatalf("refs/tillr/green = %s, want base %s (proof should wait for restart)", shortSHA(got), shortSHA(base))
	}

	// Even with a non-settling candidate examination holding restartMechanical (restartSettlements == 0)
	// and a restart still pending, maybeRolling must not start a new proof.
	releaseGate()
	releaseGate = nil
	h.l.restartMechanical.RLock()
	releaseMech := func() { h.l.restartMechanical.RUnlock() }
	nextTip := heavyCommit(t, h.f.root, "after-fixer")
	if err := h.l.maybeRolling(ctx, nextTip, false, &LoopResult{}); err != nil {
		releaseMech()
		t.Fatalf("maybeRolling while restart pending and examination running: %v", err)
	}
	h.l.heavyWG.Wait()
	if got := proofStarts.Load(); got != 0 {
		releaseMech()
		t.Fatalf("proofStarts after maybeRolling while restart pending = %d, want 0", got)
	}

	// Once the running examination releases restartMechanical, the pending restart succeeds.
	releaseMech()
	if _, stop, err := h.l.gateRestart(ctx, &LoopResult{}); err != nil || stop != StopRestarted {
		t.Fatalf("gateRestart after running gate finished = %q, %v; want %q", stop, err, StopRestarted)
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
	recordFixtureLandings(t, h.f.storePath, tip1)

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

	// No new workers start while a loop restart is pending, even during an active trunk proof.
	if pending, err := h.l.buildRestartPending(ctx); err != nil || !pending {
		t.Fatalf("buildRestartPending while trunk proof active = %v, %v; want true, nil", pending, err)
	}
	if _, unlockMech, err := h.l.lockRestartMechanical(ctx); !errors.Is(err, errRestartPending) {
		if err == nil {
			unlockMech()
		}
		t.Fatalf("lockRestartMechanical while trunk proof active = %v; want errRestartPending", err)
	}
	procSteps := &ProcessSteps{Store: h.f.storePath}
	cmd := exec.Command("true")
	handoffCtx := context.WithValue(ctx, runStartAdmissionKey{}, h.l)
	if err := procSteps.startRunFaceContext(handoffCtx, cmd, run.Request{RunID: "worker-while-proof"}); !errors.Is(err, errRestartPending) {
		t.Fatalf("worker handoff while trunk proof active = %v; want errRestartPending", err)
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
	var completion runCompletion
	var completedEarly bool
	select {
	case <-waiting:
	case completion = <-h.l.completions:
		completedEarly = true
		if !errors.Is(completion.outcome.err, errRestartPending) {
			t.Fatalf("candidate examination err while proof active = %v, want errRestartPending", completion.outcome.err)
		}
	case <-time.After(hangGuard):
		t.Fatal("finished candidate did not defer or wait on pending restart")
	}
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

	if !completedEarly {
		select {
		case completion = <-h.l.completions:
		case <-time.After(hangGuard):
			t.Fatal("waiting candidate examination did not unblock after replacement")
		}
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
	recordFixtureLandings(t, h.f.storePath, tip1)

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
	select {
	case <-waiting:
	case comp := <-h.l.completions:
		if !errors.Is(comp.outcome.err, errRestartPending) {
			t.Fatalf("finished candidate examination err = %v, want errRestartPending", comp.outcome.err)
		}
	case <-time.After(hangGuard):
		t.Fatal("finished candidate did not defer or wait on pending restart")
	}

	// Advance trunk to a newer unproved tip while proof 1 is still running.
	tip2 := heavyCommit(t, h.f.root, "tip-2")
	recordFixtureLandings(t, h.f.storePath, tip2)

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

// TestPassedCandidateLandsWhileRestartWaitsOnTrunkProof reproduces the 2026-11-23
// 22:58 sequence: while a loop restart is pending and waiting on an active trunk
// proof, a candidate whose detached gate passes (and one already gated) finishes
// judging and lands immediately without waiting for the proof to end, a candidate
// needing a new gate waits on the pending restart without starting a gate, and
// once the proof ends the pending restart replaces the loop before any next
// proof or new gate starts.
func TestPassedCandidateLandsWhileRestartWaitsOnTrunkProof(t *testing.T) {
	t.Setenv("TILLR_LOOP_STORE", "")
	f := newRefusalLoopFixtureWithLanes(t, 3, "item-a", "item-b", "item-c")
	holdWALKeeper(t, f.storePath)

	// Commit a single declaration-file heavy check so ProcessSteps.JudgeHeavy
	// runs our controlled HeavyGateBin stub.
	if err := os.MkdirAll(filepath.Join(f.root, "tools", "gate", "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "tools", "gate", "checks", "unit.json"), `{"id":"unit","scope":"tree","tier":"heavy"}`+"\n")
	runGit(t, f.root, "add", "tools/gate/checks/unit.json")
	runGit(t, f.root, "commit", "-qm", "declare heavy unit check")
	base := heavyGit(t, f.root, "rev-parse", TrunkRef)
	heavyGit(t, f.root, "update-ref", GreenRef, base)
	tip1 := heavyCommit(t, f.root, "tip-1")
	recordFixtureLandings(t, f.storePath, tip1)

	work := scratchUnderDurable(t)
	enteredGateA := filepath.Join(work, "entered-gate-a")
	releaseGateA := filepath.Join(work, "release-gate-a")
	gateLog := filepath.Join(work, "gate-log")
	enteredProof1 := filepath.Join(work, "entered-proof-1")
	releaseProof1 := filepath.Join(work, "release-proof-1")
	provedLog := filepath.Join(work, "proved-log")

	gateProg := executable(t, fmt.Sprintf(`exec python3 - "$@" <<'PY'
import os, pathlib, sys, time
args = sys.argv[1:]
item = args[args.index("--item") + 1] if "--item" in args else ""
with open(%q, "a") as fh:
    fh.write(item + "\n")
if item == "item-a":
    pathlib.Path(%q).write_text(str(os.getpid()) + "\n")
    rel = pathlib.Path(%q)
    while not rel.exists():
        time.sleep(0.02)
PY
`, gateLog, enteredGateA, releaseGateA))

	heavyGateBin := executable(t, fmt.Sprintf(`exec python3 - "$@" <<'PY'
import json, os, pathlib, sys, time
args = sys.argv[1:]
head = args[args.index("--head") + 1] if "--head" in args else ""
with open(%q, "a") as fh:
    fh.write(head + "\n")
entered = pathlib.Path(%q)
if not entered.exists():
    entered.write_text(head + "\n")
    rel = pathlib.Path(%q)
    while not rel.exists():
        time.sleep(0.02)
print(json.dumps({"kind": "gate.verdict", "payload": {"checks": [{"check_id": "unit", "result": "pass"}]}}))
PY
`, provedLog, enteredProof1, releaseProof1))

	steps := detachedGateTestSteps(t, f, time.Now, gateProg)
	steps.HeavyGateBin = heavyGateBin
	l := configureDetachedGateLoop(t, f, steps)
	l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3"}
	l.gatePolicy.LandingStrategy = "after-merge"
	l.gatePolicy.LaneBase = "green"

	exe := filepath.Join(work, "tillr-drain-next")
	if err := writeForkSafe(exe, []byte("next build"), 0o755); err != nil {
		t.Fatal(err)
	}
	var execed []string
	l.cfg.Restart = &RestartConfig{
		Executable: exe,
		Check:      func(context.Context, string) error { return nil },
		Exec: func(path string, argv, env []string) error {
			execed = append(execed, path)
			return nil
		},
	}

	cA := prepareReadyCandidate(t, f, l, "item-a")
	cB := prepareReadyCandidate(t, f, l, "item-b")
	cC := prepareReadyCandidate(t, f, l, "item-c")

	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, c := range []Candidate{cA, cB, cC} {
			if _, err := tx.Exec(`UPDATE lanes SET state=?, run_id=?, leased_until=NULL WHERE lane_id=?`, run.LaneStateLeased, c.RunID, c.LaneID); err != nil {
				return err
			}
			if err := appendEffect(tx, examinationOwnerKind, c.RunID, here, l.cfg.Now()); err != nil {
				return err
			}
		}
		return appendEffect(tx, candidateGatedKind, cC.RunID, cC, l.cfg.Now())
	})

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer func() {
		cancel()
		_ = os.WriteFile(releaseGateA, []byte("cleanup\n"), 0o644)
		_ = os.WriteFile(releaseProof1, []byte("cleanup\n"), 0o644)
		l.heavyWG.Wait()
		l.examinations.Wait()
	}()

	l.completions = make(chan runCompletion, 4)

	// 1. Start item-a's detached gate before the restart is filed.
	dA := Decision{
		Action:     ActionJudge,
		ItemID:     cA.ItemID,
		LaneID:     cA.LaneID,
		RunRequest: &run.Request{RunID: cA.RunID, ItemID: cA.ItemID, LaneID: cA.LaneID},
	}
	l.launchExamination(ctx, dA, &pendingWork{Decision: dA, Candidate: &cA, BaseSHA: cA.BaseSHA}, l.cfg.Now(), &PassTiming{}, nil)
	waitForPath[error](t, enteredGateA, nil)

	// 2. Start trunk proof 1 on tip1 and wait until it is active.
	if err := l.startHeavy(ctx, tip1); err != nil {
		t.Fatalf("startHeavy(tip1): %v", err)
	}
	waitForPath[error](t, enteredProof1, nil)

	// 3. File a pending drain restart while proof 1 is active and arm the barrier.
	req, err := restart.File(ctx, f.storePath, restart.RoleDrain, l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	if handled, stop, err := l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
		t.Fatalf("gateRestart while trunk proof active = %v, %q, %v; want deferred", handled, stop, err)
	}

	// 4. item-b (needing a new detached gate) must not start a gate while proof 1 is active and restart is pending.
	dB := Decision{
		Action:     ActionJudge,
		ItemID:     cB.ItemID,
		LaneID:     cB.LaneID,
		RunRequest: &run.Request{RunID: cB.RunID, ItemID: cB.ItemID, LaneID: cB.LaneID},
	}
	l.launchExamination(ctx, dB, &pendingWork{Decision: dB, Candidate: &cB, BaseSHA: cB.BaseSHA}, l.cfg.Now(), &PassTiming{}, nil)
	var compB runCompletion
	select {
	case compB = <-l.completions:
	case <-time.After(hangGuard):
		t.Fatal("item-b examination did not defer while restart was pending")
	}
	if compB.itemID != cB.ItemID || !errors.Is(compB.outcome.err, errRestartPending) {
		t.Fatalf("item-b completion while restart pending = %+v, want errRestartPending", compB)
	}

	// 5. Release item-a's detached gate (exit 0 / pass) while proof 1 is still active.
	// It must finish judging and land without waiting for proof 1 to end.
	if err := os.WriteFile(releaseGateA, []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case compA := <-l.completions:
		if compA.itemID != cA.ItemID {
			t.Fatalf("unexpected completion for %s while waiting for %s: %+v", compA.itemID, cA.ItemID, compA)
		}
		if compA.outcome.err != nil || compA.result.Landed != 1 {
			t.Fatalf("item-a completion while proof active and restart pending: outcome=%+v result=%+v", compA.outcome, compA.result)
		}
	case <-time.After(hangGuard):
		t.Fatal("item-a whose gate passed did not land while trunk proof was active and restart was pending")
	}
	if got := itemStateOf(t, f.storePath, cA.ItemID); got != StateLanded {
		t.Fatalf("item-a state = %q, want %q", got, StateLanded)
	}
	var laneStateA string
	sCheck := openFixture(t, f.storePath)
	if err := sCheck.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT state FROM lanes WHERE lane_id=?`, cA.LaneID).Scan(&laneStateA)
	}); err != nil {
		sCheck.Close()
		t.Fatal(err)
	}
	sCheck.Close()
	if laneStateA != run.LaneStateIdle {
		t.Fatalf("item-a lane %s state = %q while proof active, want %q", cA.LaneID, laneStateA, run.LaneStateIdle)
	}

	// 6. item-c (already gated via candidateGatedKind) must also land without waiting for proof 1 to end.
	dC := Decision{
		Action:     ActionJudge,
		ItemID:     cC.ItemID,
		LaneID:     cC.LaneID,
		RunRequest: &run.Request{RunID: cC.RunID, ItemID: cC.ItemID, LaneID: cC.LaneID},
	}
	l.launchExamination(ctx, dC, &pendingWork{Decision: dC, Candidate: &cC, BaseSHA: cC.BaseSHA, Gated: true}, l.cfg.Now(), &PassTiming{}, nil)
	select {
	case compC := <-l.completions:
		if compC.itemID != cC.ItemID {
			t.Fatalf("unexpected completion for %s while waiting for %s: %+v", compC.itemID, cC.ItemID, compC)
		}
		if compC.outcome.err != nil || compC.result.Landed != 1 {
			t.Fatalf("item-c completion while proof active and restart pending: outcome=%+v result=%+v", compC.outcome, compC.result)
		}
	case <-time.After(hangGuard):
		t.Fatal("already-gated item-c did not land while trunk proof was active and restart was pending")
	}
	if got := itemStateOf(t, f.storePath, cC.ItemID); got != StateLanded {
		t.Fatalf("item-c state = %q, want %q", got, StateLanded)
	}

	// 7. End proof 1. The pending restart must replace the loop before any next proof or new gate starts.
	if err := os.WriteFile(releaseProof1, []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()

	if got := heavyGit(t, f.root, "rev-parse", GreenRef); got != tip1 {
		t.Fatalf("refs/tillr/green = %s, want %s", shortSHA(got), shortSHA(tip1))
	}
	if handled, stop, err := l.gateRestart(ctx, &LoopResult{}); !handled || stop != StopRestarted || err != nil {
		t.Fatalf("gateRestart after proof 1 ended = %v, %q, %v; want replaced", handled, stop, err)
	}
	if len(execed) == 0 {
		t.Fatal("replacement did not exec after proof 1 ended")
	}
	l.examinations.Wait()

	// Only item-a ran the gate; item-b never started a gate.
	gateRaw, err := os.ReadFile(gateLog)
	if err != nil {
		t.Fatalf("read gate log: %v", err)
	}
	if got := strings.Fields(string(gateRaw)); len(got) != 1 || got[0] != "item-a" {
		t.Fatalf("gate invocations = %v, want only [item-a]", got)
	}

	// Only tip1 was proved before the restart.
	provedRaw, err := os.ReadFile(provedLog)
	if err != nil {
		t.Fatalf("read proved log: %v", err)
	}
	if got := strings.Fields(string(provedRaw)); len(got) != 1 || got[0] != tip1 {
		t.Fatalf("proved tips = %v, want only [%s]", got, shortSHA(tip1))
	}

	// The restarted loop answers the request "restarted".
	t.Setenv(restart.RequestEnv, req.ID)
	t.Setenv(restart.FromEnv, "old-build")
	if err := l.adoptRestart(ctx); err != nil {
		t.Fatalf("adoptRestart: %v", err)
	}
	ans, err := restart.AwaitAnswer(ctx, f.storePath, req.ID, time.Millisecond)
	if err != nil {
		t.Fatalf("request %s unanswered: %v", req.ID, err)
	}
	if ans.Answer != restart.AnswerRestarted {
		t.Fatalf("restart answer = %+v, want %q", ans, restart.AnswerRestarted)
	}
}

// TestFailedGateSettlesWhileRestartWaitsOnTrunkProof reproduces the 2026-11-24
// 04:31 sequence: while a loop restart is pending and waiting on an active
// trunk proof, a detached gate that ends failed (or has already ended failed
// on disk) settles immediately without waiting for the proof to end: its
// timing, verdict, and refusal are recorded and its lane is released. Once the
// proof ends, the pending restart still replaces the loop.
func TestFailedGateSettlesWhileRestartWaitsOnTrunkProof(t *testing.T) {
	t.Setenv("TILLR_LOOP_STORE", "")
	f := newRefusalLoopFixtureWithLanes(t, 2, "item-a", "item-b")
	holdWALKeeper(t, f.storePath)

	if err := os.MkdirAll(filepath.Join(f.root, "tools", "gate", "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "tools", "gate", "checks", "unit.json"), `{"id":"unit","scope":"tree","tier":"heavy"}`+"\n")
	runGit(t, f.root, "add", "tools/gate/checks/unit.json")
	runGit(t, f.root, "commit", "-qm", "declare heavy unit check")
	base := heavyGit(t, f.root, "rev-parse", TrunkRef)
	heavyGit(t, f.root, "update-ref", GreenRef, base)
	tip1 := heavyCommit(t, f.root, "tip-1")

	work := scratchUnderDurable(t)
	enteredGateA := filepath.Join(work, "entered-gate-a")
	releaseGateA := filepath.Join(work, "release-gate-a")
	enteredProof1 := filepath.Join(work, "entered-proof-1")
	releaseProof1 := filepath.Join(work, "release-proof-1")

	gateProg := executable(t, fmt.Sprintf(`exec python3 - "$@" <<'PY'
import os, pathlib, sys, time
args = sys.argv[1:]
item = args[args.index("--item") + 1] if "--item" in args else ""
if item == "item-a":
    pathlib.Path(%q).write_text(str(os.getpid()) + "\n")
    rel = pathlib.Path(%q)
    while not rel.exists():
        time.sleep(0.02)
    sys.exit(1)
PY
`, enteredGateA, releaseGateA))

	heavyGateBin := executable(t, fmt.Sprintf(`exec python3 - "$@" <<'PY'
import json, pathlib, sys, time
args = sys.argv[1:]
head = args[args.index("--head") + 1] if "--head" in args else ""
entered = pathlib.Path(%q)
if not entered.exists():
    entered.write_text(head + "\n")
    rel = pathlib.Path(%q)
    while not rel.exists():
        time.sleep(0.02)
print(json.dumps({"kind": "gate.verdict", "payload": {"checks": [{"check_id": "unit", "result": "pass"}]}}))
PY
`, enteredProof1, releaseProof1))

	steps := detachedGateTestSteps(t, f, time.Now, gateProg)
	steps.HeavyGateBin = heavyGateBin
	l := configureDetachedGateLoop(t, f, steps)
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.gatePolicy.LandingStrategy = "after-merge"
	l.gatePolicy.LaneBase = "green"

	exe := filepath.Join(work, "tillr-drain-next")
	if err := writeForkSafe(exe, []byte("next build"), 0o755); err != nil {
		t.Fatal(err)
	}
	var execed []string
	l.cfg.Restart = &RestartConfig{
		Executable: exe,
		Check:      func(context.Context, string) error { return nil },
		Exec: func(path string, argv, env []string) error {
			execed = append(execed, path)
			return nil
		},
	}

	cA := prepareReadyCandidate(t, f, l, "item-a")
	cB := prepareReadyCandidate(t, f, l, "item-b")

	// Seed a finished failed detached gate on disk for item-b so we also cover
	// attaching to an already-failed detached gate while the proof is active.
	gateDirB := steps.gateRunDir(cB)
	if err := os.MkdirAll(gateDirB, 0o755); err != nil {
		t.Fatal(err)
	}
	nowMS := l.cfg.Now().UnixMilli()
	if err := writeJSONAtomic(filepath.Join(gateDirB, GateDoneFile), detachedGateDone{
		RunID:         cB.RunID,
		BaseSHA:       cB.BaseSHA,
		HeadSHA:       cB.HeadSHA,
		Argv:          []string{gateProg, "--item", cB.ItemID},
		Cwd:           cB.Worktree,
		StartedAtMS:   nowMS - 1000,
		FinishedAtMS:  nowMS,
		ExitCode:      1,
		MeasureTiming: true,
	}); err != nil {
		t.Fatal(err)
	}

	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, c := range []Candidate{cA, cB} {
			if _, err := tx.Exec(`UPDATE lanes SET state=?, run_id=?, leased_until=NULL WHERE lane_id=?`, run.LaneStateLeased, c.RunID, c.LaneID); err != nil {
				return err
			}
			if err := appendEffect(tx, examinationOwnerKind, c.RunID, here, l.cfg.Now()); err != nil {
				return err
			}
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer func() {
		cancel()
		_ = os.WriteFile(releaseGateA, []byte("cleanup\n"), 0o644)
		_ = os.WriteFile(releaseProof1, []byte("cleanup\n"), 0o644)
		l.heavyWG.Wait()
		l.examinations.Wait()
	}()

	l.completions = make(chan runCompletion, 4)

	// 1. Start item-a's detached gate before the restart is filed.
	dA := Decision{
		Action:     ActionJudge,
		ItemID:     cA.ItemID,
		LaneID:     cA.LaneID,
		RunRequest: &run.Request{RunID: cA.RunID, ItemID: cA.ItemID, LaneID: cA.LaneID},
	}
	l.launchExamination(ctx, dA, &pendingWork{Decision: dA, Candidate: &cA, BaseSHA: cA.BaseSHA}, l.cfg.Now(), &PassTiming{}, nil)
	waitForPath[error](t, enteredGateA, nil)

	// 2. Start trunk proof 1 on tip1 and wait until it is active.
	if err := l.startHeavy(ctx, tip1); err != nil {
		t.Fatalf("startHeavy(tip1): %v", err)
	}
	waitForPath[error](t, enteredProof1, nil)

	// 3. File a pending drain restart while proof 1 is active and arm the barrier.
	req, err := restart.File(ctx, f.storePath, restart.RoleDrain, l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	if handled, stop, err := l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
		t.Fatalf("gateRestart while trunk proof active = %v, %q, %v; want deferred", handled, stop, err)
	}

	assertFailedSettled := func(c Candidate) {
		t.Helper()
		var (
			laneState string
			timings   int
			verdicts  int
			refusals  int
		)
		sCheck := openFixture(t, f.storePath)
		defer sCheck.Close()
		if err := sCheck.ReadTx(ctx, func(r *store.Reader) error {
			if err := r.QueryRow(`SELECT state FROM lanes WHERE lane_id=?`, c.LaneID).Scan(&laneState); err != nil {
				return err
			}
			if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=?`, FastGateTimingKind, c.RunID).Scan(&timings); err != nil {
				return err
			}
			if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=?`, GateVerdictKind, c.RunID).Scan(&verdicts); err != nil {
				return err
			}
			return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=? AND json_extract(payload,'$.step')=?`, RefusalKind, c.RunID, RefusalGate).Scan(&refusals)
		}); err != nil {
			t.Fatal(err)
		}
		if laneState != run.LaneStateIdle {
			t.Fatalf("%s lane %s state = %q while proof active, want %q", c.ItemID, c.LaneID, laneState, run.LaneStateIdle)
		}
		if timings != 1 {
			t.Fatalf("%s %s count = %d while proof active, want 1", c.ItemID, FastGateTimingKind, timings)
		}
		if verdicts != 1 {
			t.Fatalf("%s %s count = %d while proof active, want 1", c.ItemID, GateVerdictKind, verdicts)
		}
		if refusals != 1 {
			t.Fatalf("%s %s (step=%s) count = %d while proof active, want 1", c.ItemID, RefusalKind, RefusalGate, refusals)
		}
	}

	// 4. Release item-a's detached gate (exit 1 / fail) while proof 1 is still active.
	// It must settle immediately without waiting for proof 1 to end.
	if err := os.WriteFile(releaseGateA, []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case compA := <-l.completions:
		if compA.itemID != cA.ItemID {
			t.Fatalf("unexpected completion for %s while waiting for %s: %+v", compA.itemID, cA.ItemID, compA)
		}
		if compA.outcome.err != nil || compA.result.Rejected != 1 || compA.result.Landed != 0 {
			t.Fatalf("item-a completion while proof active and restart pending: outcome=%+v result=%+v", compA.outcome, compA.result)
		}
	case <-time.After(hangGuard):
		t.Fatal("item-a whose gate failed did not settle while trunk proof was active and restart was pending")
	}
	assertFailedSettled(cA)

	// 5. item-b (already ended failed on disk) must also settle without waiting for proof 1 to end.
	dB := Decision{
		Action:     ActionJudge,
		ItemID:     cB.ItemID,
		LaneID:     cB.LaneID,
		RunRequest: &run.Request{RunID: cB.RunID, ItemID: cB.ItemID, LaneID: cB.LaneID},
	}
	l.launchExamination(ctx, dB, &pendingWork{Decision: dB, Candidate: &cB, BaseSHA: cB.BaseSHA}, l.cfg.Now(), &PassTiming{}, nil)
	select {
	case compB := <-l.completions:
		if compB.itemID != cB.ItemID {
			t.Fatalf("unexpected completion for %s while waiting for %s: %+v", compB.itemID, cB.ItemID, compB)
		}
		if compB.outcome.err != nil || compB.result.Rejected != 1 || compB.result.Landed != 0 {
			t.Fatalf("item-b completion while proof active and restart pending: outcome=%+v result=%+v", compB.outcome, compB.result)
		}
	case <-time.After(hangGuard):
		t.Fatal("item-b whose gate already failed on disk did not settle while trunk proof was active and restart was pending")
	}
	assertFailedSettled(cB)

	// 6. End proof 1. The pending restart must replace the loop and answer "restarted".
	if err := os.WriteFile(releaseProof1, []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()

	if handled, stop, err := l.gateRestart(ctx, &LoopResult{}); !handled || stop != StopRestarted || err != nil {
		t.Fatalf("gateRestart after proof 1 ended = %v, %q, %v; want replaced", handled, stop, err)
	}
	if len(execed) == 0 {
		t.Fatal("replacement did not exec after proof 1 ended")
	}
	l.examinations.Wait()

	t.Setenv(restart.RequestEnv, req.ID)
	t.Setenv(restart.FromEnv, "old-build")
	if err := l.adoptRestart(ctx); err != nil {
		t.Fatalf("adoptRestart: %v", err)
	}
	ans, err := restart.AwaitAnswer(ctx, f.storePath, req.ID, time.Millisecond)
	if err != nil {
		t.Fatalf("request %s unanswered: %v", req.ID, err)
	}
	if ans.Answer != restart.AnswerRestarted {
		t.Fatalf("restart answer = %+v, want %q", ans, restart.AnswerRestarted)
	}
}

// Reproduces the 2026-11-24 12:15 collision: a drain restart is pending and a
// post-landing roll or push action is busy when a trunk proof ends green, with
// a newer unproved tip on trunk. The runner must wait for the busy action to
// finish and take the restart instead of starting the next tip's proof.
func TestPendingRestartWaitsForBusyPostLandingActionBeforeNextProof(t *testing.T) {
	cases := []struct {
		name   string
		action func(*Loop) *landingAction
	}{
		{name: "roll", action: func(l *Loop) *landingAction { return &l.rollAction }},
		{name: "push", action: func(l *Loop) *landingAction { return &l.pushAction }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil }, "item-a")
			h.l.gatePolicy.LandingStrategy = "after-merge"
			h.l.gatePolicy.LaneBase = "green"

			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			defer cancel()

			remote := filepath.Join(t.TempDir(), "upstream.git")
			runGit(t, h.f.root, "init", "--bare", remote)
			runGit(t, h.f.root, "remote", "add", "origin", remote)
			runGit(t, h.f.root, "push", "-u", "origin", TrunkRef)
			h.l.cfg.PostLanding.Push.What = settings.PushGreen

			base := heavyGit(t, h.f.root, "rev-parse", TrunkRef)
			heavyGit(t, h.f.root, "update-ref", GreenRef, base)
			tip1 := heavyCommit(t, h.f.root, "tip-1")
			recordFixtureLandings(t, h.f.storePath, tip1)

			greenReported := make(chan struct{})
			landingsReported := make(chan struct{})
			var greenOnce, landingsOnce sync.Once
			origOut := h.l.cfg.Report
			h.l.cfg.Report = writerFunc(func(p []byte) (int, error) {
				s := string(p)
				if strings.Contains(s, "loop: heavy run green at "+shortSHA(tip1)) {
					greenOnce.Do(func() { close(greenReported) })
				}
				if strings.Contains(s, "loop: heavy "+shortSHA(tip1)+" landings per heavy run=") {
					landingsOnce.Do(func() { close(landingsReported) })
				}
				if origOut != nil {
					return origOut.Write(p)
				}
				return len(p), nil
			})

			var (
				provedMu   sync.Mutex
				provedTips []string
			)
			enteredProof1 := make(chan string, 1)
			enteredProof2 := make(chan string, 1)
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
					n := len(provedTips)
					provedMu.Unlock()
					if n == 1 {
						enteredProof1 <- sha
						select {
						case <-releaseProof1:
						case <-jctx.Done():
							return nil, jctx.Err()
						}
					} else if n == 2 {
						select {
						case enteredProof2 <- sha:
						default:
						}
					}
					return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
				},
			}
			h.l.cfg.Steps = steps

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

			// File a pending drain restart while proof 1 is active.
			req := h.request(t, 1)[0]

			// Advance trunk to a newer unproved tip while proof 1 is still running.
			tip2 := heavyCommit(t, h.f.root, "tip-2")
			recordFixtureLandings(t, h.f.storePath, tip2)

			// Hold the post-landing action busy across the instant proof 1 ends green.
			action := tc.action(h.l)
			actionStarted := make(chan struct{})
			var actionStartedOnce sync.Once
			releaseAction := make(chan struct{})
			actionReleased := false
			defer func() {
				if !actionReleased {
					close(releaseAction)
					waitAction(t, action)
				}
			}()
			action.submit(tip2, func(string) {
				actionStartedOnce.Do(func() { close(actionStarted) })
				<-releaseAction
			})
			select {
			case <-actionStarted:
			case <-time.After(hangGuard):
				t.Fatalf("busy %s action did not start", tc.name)
			}

			// End proof 1 green while the post-landing action is still busy.
			close(releaseProof1)
			proof1Released = true
			select {
			case <-greenReported:
			case <-time.After(hangGuard):
				t.Fatal("proof 1 green report was not reached")
			}

			heavyDone := make(chan struct{})
			go func() {
				h.l.heavyWG.Wait()
				close(heavyDone)
			}()

			// While the post-landing action is still busy, the runner must wait on it
			// rather than advancing past the green report or starting proof 2 on tip2.
			select {
			case got := <-enteredProof2:
				close(releaseAction)
				actionReleased = true
				waitAction(t, action)
				<-heavyDone
				t.Fatalf("next proof started on %s while %s action was busy and restart was pending", shortSHA(got), tc.name)
			case <-landingsReported:
				// Report did not wait for the busy action; wait briefly for tip2 to confirm the collision.
				select {
				case got := <-enteredProof2:
					close(releaseAction)
					actionReleased = true
					waitAction(t, action)
					<-heavyDone
					t.Fatalf("next proof started on %s while %s action was busy and restart was pending", shortSHA(got), tc.name)
				case <-time.After(hangGuard):
					close(releaseAction)
					actionReleased = true
					waitAction(t, action)
					<-heavyDone
					t.Fatalf("green report did not wait for busy %s action while restart was pending", tc.name)
				}
			case <-time.After(200 * time.Millisecond):
			}

			// Finish the busy post-landing action; the runner should now stop before tip2.
			close(releaseAction)
			actionReleased = true
			waitAction(t, action)
			select {
			case <-heavyDone:
			case <-time.After(hangGuard):
				t.Fatal("heavy runner did not exit after busy action finished")
			}

			if got := heavyGit(t, h.f.root, "rev-parse", GreenRef); got != tip1 {
				t.Fatalf("refs/tillr/green = %s, want %s (tip2 %s must not be proved before restart)", shortSHA(got), shortSHA(tip1), shortSHA(tip2))
			}

			provedMu.Lock()
			gotProved := append([]string(nil), provedTips...)
			provedMu.Unlock()
			if len(gotProved) != 1 || gotProved[0] != tip1 {
				t.Fatalf("proved tips before restart = %v, want only [%s]", gotProved, shortSHA(tip1))
			}

			// The next pass must take the pending restart.
			stop, err := h.l.pass(ctx, &LoopResult{})
			if err != nil {
				t.Fatalf("pass after proof 1 and %s action ended: %v", tc.name, err)
			}
			if stop != StopRestarted {
				t.Fatalf("pass stop = %q, want %q", stop, StopRestarted)
			}
			if len(h.execed) == 0 {
				t.Fatal("replacement did not exec")
			}

			t.Setenv(restart.RequestEnv, req.ID)
			t.Setenv(restart.FromEnv, "old-build")
			if err := h.l.adoptRestart(ctx); err != nil {
				t.Fatalf("adoptRestart: %v", err)
			}
			if ans := h.answer(t, req.ID); ans.Answer != restart.AnswerRestarted {
				t.Fatalf("restart answer = %+v, want %q", ans, restart.AnswerRestarted)
			}
		})
	}
}

// With no restart pending, a busy post-landing roll or push action does not delay
// starting the next tip's proof when the current proof ends green.
func TestNoPendingRestartStartsNextProofWhilePostLandingActionBusy(t *testing.T) {
	cases := []struct {
		name   string
		action func(*Loop) *landingAction
	}{
		{name: "roll", action: func(l *Loop) *landingAction { return &l.rollAction }},
		{name: "push", action: func(l *Loop) *landingAction { return &l.pushAction }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil }, "item-a")
			h.l.gatePolicy.LandingStrategy = "after-merge"
			h.l.gatePolicy.LaneBase = "green"

			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			defer cancel()

			base := heavyGit(t, h.f.root, "rev-parse", TrunkRef)
			heavyGit(t, h.f.root, "update-ref", GreenRef, base)
			tip1 := heavyCommit(t, h.f.root, "tip-1")
			recordFixtureLandings(t, h.f.storePath, tip1)

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
					n := len(provedTips)
					provedMu.Unlock()
					if n == 1 {
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

			// Advance trunk to tip2 and hold the post-landing action busy with NO restart pending.
			tip2 := heavyCommit(t, h.f.root, "tip-2")
			recordFixtureLandings(t, h.f.storePath, tip2)
			action := tc.action(h.l)
			actionStarted := make(chan struct{})
			releaseAction := make(chan struct{})
			defer func() {
				close(releaseAction)
				waitAction(t, action)
			}()
			action.submit(tip2, func(string) {
				close(actionStarted)
				<-releaseAction
			})
			select {
			case <-actionStarted:
			case <-time.After(hangGuard):
				t.Fatalf("busy %s action did not start", tc.name)
			}

			// End proof 1 green. Proof 2 on tip2 must start and complete without waiting
			// for the busy post-landing action.
			close(releaseProof1)
			proof1Released = true

			heavyDone := make(chan struct{})
			go func() {
				h.l.heavyWG.Wait()
				close(heavyDone)
			}()
			select {
			case <-heavyDone:
			case <-time.After(hangGuard):
				t.Fatalf("next proof blocked on busy %s action with no restart pending", tc.name)
			}

			if got := heavyGit(t, h.f.root, "rev-parse", GreenRef); got != tip2 {
				t.Fatalf("refs/tillr/green = %s, want %s", shortSHA(got), shortSHA(tip2))
			}
			provedMu.Lock()
			gotProved := append([]string(nil), provedTips...)
			provedMu.Unlock()
			if len(gotProved) != 2 || gotProved[0] != tip1 || gotProved[1] != tip2 {
				t.Fatalf("proved tips = %v, want [%s %s]", gotProved, shortSHA(tip1), shortSHA(tip2))
			}
		})
	}
}

// Reproduces the 2026-11-25 starvation sequence: a trunk proof is active, a
// loop restart request arrives, a pre-restart detached gate fails and frees its
// lane, and both the refused item (attempt 2, -a2-n1) and a queued item are
// ready to dispatch while another candidate is ready to gate. While the restart
// request is pending, no new worker run, detached gate, or second trunk proof
// may start until the restart is answered.
func TestPendingRestartWhileTrunkProofRunsStartsNoNewWorkersOrGates(t *testing.T) {
	t.Setenv("TILLR_LOOP_STORE", "")
	f := newRefusalLoopFixtureWithLanes(t, 3, "item-a", "item-b", "item-c")
	holdWALKeeper(t, f.storePath)

	if err := os.MkdirAll(filepath.Join(f.root, "tools", "gate", "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "tools", "gate", "checks", "unit.json"), `{"id":"unit","scope":"tree","tier":"heavy"}`+"\n")
	runGit(t, f.root, "add", "tools/gate/checks/unit.json")
	runGit(t, f.root, "commit", "-qm", "declare heavy unit check")
	base := heavyGit(t, f.root, "rev-parse", TrunkRef)
	heavyGit(t, f.root, "update-ref", GreenRef, base)
	tip1 := heavyCommit(t, f.root, "tip-1")

	work := scratchUnderDurable(t)
	enteredGateA := filepath.Join(work, "entered-gate-a")
	releaseGateA := filepath.Join(work, "release-gate-a")
	gateStartsFile := filepath.Join(work, "gate-starts.log")
	workerStartsFile := filepath.Join(work, "worker-starts.log")
	enteredProof1 := filepath.Join(work, "entered-proof-1")
	releaseProof1 := filepath.Join(work, "release-proof-1")

	gateProg := executable(t, fmt.Sprintf(`exec python3 - "$@" <<'PY'
import os, pathlib, sys, time
args = sys.argv[1:]
item = args[args.index("--item") + 1] if "--item" in args else ""
run_id = os.environ.get("TILLR_RUN_ID", item)
with open(%q, "a") as fh:
    fh.write(f"{item}\n")
if item == "item-a":
    entered = pathlib.Path(%q)
    if not entered.exists():
        tmp = entered.with_name(f".{entered.name}.{os.getpid()}.tmp")
        tmp.write_text(str(os.getpid()) + "\n")
        os.replace(tmp, entered)
        rel = pathlib.Path(%q)
        while not rel.exists():
            time.sleep(0.02)
        sys.exit(1)
PY
`, gateStartsFile, enteredGateA, releaseGateA))

	runBin := executable(t, fmt.Sprintf(`exec python3 - "$@" <<'PY'
import json, os, pathlib, subprocess, sys
args = sys.argv[1:]
run_id = os.environ.get("TILLR_RUN_ID", "")
with open(%q, "a") as fh:
    fh.write(f"{run_id}\n")
worktree = args[args.index("--worktree") + 1] if "--worktree" in args else ""
if worktree:
    pathlib.Path(worktree, f"work-{run_id}.txt").write_text(run_id + "\n")
    subprocess.run(["git", "-C", worktree, "add", "."], check=True)
    subprocess.run(["git", "-C", worktree, "commit", "-qm", f"work {run_id}"], check=True)
print(json.dumps({"run_id": run_id, "state": "finished", "end_cause": "completed"}))
PY
`, workerStartsFile))

	heavyGateBin := executable(t, fmt.Sprintf(`exec python3 - "$@" <<'PY'
import json, pathlib, sys, time
args = sys.argv[1:]
head = args[args.index("--head") + 1] if "--head" in args else ""
entered = pathlib.Path(%q)
if not entered.exists():
    entered.write_text(head + "\n")
    rel = pathlib.Path(%q)
    while not rel.exists():
        time.sleep(0.02)
print(json.dumps({"kind": "gate.verdict", "payload": {"checks": [{"check_id": "unit", "result": "pass"}]}}))
PY
`, enteredProof1, releaseProof1))

	steps := detachedGateTestSteps(t, f, time.Now, gateProg)
	steps.RunBin = runBin
	steps.HeavyGateBin = heavyGateBin
	l := configureDetachedGateLoop(t, f, steps)
	l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3"}
	l.gatePolicy.LandingStrategy = "after-merge"
	l.gatePolicy.LaneBase = "green"

	exe := filepath.Join(work, "tillr-drain-next")
	if err := writeForkSafe(exe, []byte("next build"), 0o755); err != nil {
		t.Fatal(err)
	}
	var execed []string
	l.cfg.Restart = &RestartConfig{
		Executable: exe,
		Check:      func(context.Context, string) error { return nil },
		Exec: func(path string, argv, env []string) error {
			execed = append(execed, path)
			return nil
		},
	}

	// item-a is on lane-1 with a pre-restart detached gate.
	// item-b is on lane-2 with a finished worker run ready to be gated, but NO gate started yet.
	// item-c is queued, ready to be dispatched as soon as a lane is free.
	cA := prepareReadyCandidate(t, f, l, "item-a")
	cB := prepareReadyCandidate(t, f, l, "item-b")
	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET allowed_paths='["a.txt"]' WHERE item_id='item-a'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET allowed_paths='["b.txt"]' WHERE item_id='item-b'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET allowed_paths='["c.txt"]' WHERE item_id='item-c'`); err != nil {
			return err
		}
		for _, c := range []Candidate{cA, cB} {
			if _, err := tx.Exec(`UPDATE lanes SET state=?, run_id=?, leased_until=NULL WHERE lane_id=?`, run.LaneStateLeased, c.RunID, c.LaneID); err != nil {
				return err
			}
			if err := appendEffect(tx, examinationOwnerKind, c.RunID, here, l.cfg.Now()); err != nil {
				return err
			}
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer func() {
		cancel()
		_ = os.WriteFile(releaseGateA, []byte("cleanup\n"), 0o644)
		_ = os.WriteFile(releaseProof1, []byte("cleanup\n"), 0o644)
		l.heavyWG.Wait()
		l.examinations.Wait()
	}()

	l.completions = make(chan runCompletion, 8)

	// 1. Start item-a's detached gate BEFORE the restart request is filed.
	dA := Decision{
		Action:     ActionJudge,
		ItemID:     cA.ItemID,
		LaneID:     cA.LaneID,
		RunRequest: &run.Request{RunID: cA.RunID, ItemID: cA.ItemID, LaneID: cA.LaneID},
	}
	l.launchExamination(ctx, dA, &pendingWork{Decision: dA, Candidate: &cA, BaseSHA: cA.BaseSHA}, l.cfg.Now(), &PassTiming{}, nil)
	waitForPath[error](t, enteredGateA, nil)

	// 2. Start trunk proof 1 on tip1 and wait until it is active.
	if err := l.startHeavy(ctx, tip1); err != nil {
		t.Fatalf("startHeavy(tip1): %v", err)
	}
	waitForPath[error](t, enteredProof1, nil)

	// 3. File a pending drain restart while proof 1 is active and arm the barrier.
	req, err := restart.File(ctx, f.storePath, restart.RoleDrain, l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	if handled, stop, err := l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
		t.Fatalf("gateRestart while trunk proof active = %v, %q, %v; want deferred", handled, stop, err)
	}

	// 4. Release item-a's pre-restart detached gate (exit 1 / refused) while proof 1 is active.
	// It must settle and free lane-1 so item-a (attempt 2) and queued item-b are both eligible.
	if err := os.WriteFile(releaseGateA, []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case compA := <-l.completions:
		if compA.itemID != cA.ItemID || compA.outcome.err != nil || compA.result.Rejected != 1 {
			t.Fatalf("item-a settlement while proof active = %+v (result=%+v), want 1 rejection", compA, compA.result)
		}
	case <-time.After(hangGuard):
		t.Fatal("item-a failed gate did not settle while trunk proof was active")
	}

	// 5. Confirm both item-a (retry after refusal) and item-c (queued) are planned,
	// then run a scheduling pass while proof 1 is still active and restart is pending.
	// Neither item-a (-a2-n1) nor queued item-c may be dispatched as new workers.
	var sawRetryA, sawDispatchC bool
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-a" && (p.decision.Action == ActionRetry || p.decision.Action == ActionDispatch) {
			sawRetryA = true
		}
		if p.decision.ItemID == "item-c" && p.decision.Action == ActionDispatch {
			sawDispatchC = true
		}
	}
	if !sawRetryA || !sawDispatchC {
		t.Fatalf("expected item-a retry (%v) and item-c dispatch (%v) to be planned after refusal", sawRetryA, sawDispatchC)
	}
	passResult := LoopResult{}
	if stop, err := l.pass(ctx, &passResult); stop != "" || err != nil {
		t.Fatalf("pass while trunk proof active = %q, %v; want empty, nil", stop, err)
	}
	if passResult.Dispatched != 0 {
		t.Fatalf("pass dispatched %d new worker runs while restart was pending and trunk proof active, want 0", passResult.Dispatched)
	}

	// Also verify a direct candidate gate attempt for item-b (no pre-restart gate)
	// waits for the pending restart rather than starting a new detached gate.
	dB := Decision{
		Action:     ActionJudge,
		ItemID:     cB.ItemID,
		LaneID:     cB.LaneID,
		RunRequest: &run.Request{RunID: cB.RunID, ItemID: cB.ItemID, LaneID: cB.LaneID},
	}
	waitingForRestart := make(chan struct{}, 2)
	l.restartWaitObs = func() {
		select {
		case waitingForRestart <- struct{}{}:
		default:
		}
	}
	l.launchExamination(ctx, dB, &pendingWork{Decision: dB, Candidate: &cB, BaseSHA: cB.BaseSHA}, l.cfg.Now(), &PassTiming{}, nil)
	select {
	case <-waitingForRestart:
	case compB := <-l.completions:
		if !errors.Is(compB.outcome.err, errRestartPending) {
			t.Fatalf("item-b completed examination while restart was pending: %+v", compB)
		}
	case <-time.After(hangGuard):
		t.Fatal("item-b did not defer or wait on pending restart before starting a new detached gate")
	}

	// Verify no new worker started and no new gate started after item-a's pre-restart gate.
	if raw, err := os.ReadFile(workerStartsFile); err == nil && strings.TrimSpace(string(raw)) != "" {
		t.Fatalf("worker runs started after restart request while trunk proof active:\n%s", string(raw))
	}
	rawGates, err := os.ReadFile(gateStartsFile)
	if err != nil {
		t.Fatalf("read gate starts: %v", err)
	}
	gateLines := strings.Fields(string(rawGates))
	if len(gateLines) != 1 || gateLines[0] != "item-a" {
		t.Fatalf("gate starts = %v, want only pre-restart [item-a]", gateLines)
	}

	// 6. End proof 1. The pending restart must replace the loop and answer "restarted".
	if err := os.WriteFile(releaseProof1, []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()

	if handled, stop, err := l.gateRestart(ctx, &LoopResult{}); !handled || stop != StopRestarted || err != nil {
		t.Fatalf("gateRestart after proof 1 ended = %v, %q, %v; want replaced", handled, stop, err)
	}
	if len(execed) == 0 {
		t.Fatal("replacement did not exec after proof 1 ended")
	}
	l.examinations.Wait()

	// Confirm still zero post-restart worker starts and only the 1 pre-restart gate.
	if raw, err := os.ReadFile(workerStartsFile); err == nil && strings.TrimSpace(string(raw)) != "" {
		t.Fatalf("worker runs started before restart completed:\n%s", string(raw))
	}
	rawGates, err = os.ReadFile(gateStartsFile)
	if err != nil {
		t.Fatalf("read gate starts after restart: %v", err)
	}
	gateLines = strings.Fields(string(rawGates))
	if len(gateLines) != 1 || gateLines[0] != "item-a" {
		t.Fatalf("gate starts after restart = %v, want only pre-restart [item-a]", gateLines)
	}

	t.Setenv(restart.RequestEnv, req.ID)
	t.Setenv(restart.FromEnv, "old-build")
	if err := l.adoptRestart(ctx); err != nil {
		t.Fatalf("adoptRestart: %v", err)
	}
	ans, err := restart.AwaitAnswer(ctx, f.storePath, req.ID, time.Millisecond)
	if err != nil {
		t.Fatalf("request %s unanswered: %v", req.ID, err)
	}
	if ans.Answer != restart.AnswerRestarted {
		t.Fatalf("restart answer = %+v, want %q", ans, restart.AnswerRestarted)
	}
}
