package drain

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-drain/heavyrun"
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

func newDetachedProofLoopFixture(t *testing.T, root, work, storePath, gateBin string, restartCfg *RestartConfig) *Loop {
	t.Helper()
	steps := &ProcessSteps{
		Root:         root,
		LaneRoot:     work,
		Store:        storePath,
		HeavyGateBin: gateBin,
	}
	return &Loop{
		gatePolicy: GatePolicy{LandingStrategy: "after-merge", LaneBase: "green"},
		cfg: LoopConfig{
			Root:     root,
			Store:    storePath,
			LaneRoot: work,
			Steps:    steps,
			Restart:  restartCfg,
			Now:      time.Now,
			Report:   io.Discard,
		},
	}
}

func TestPendingRestartDoesNotHoldDispatchWhileProofRuns(t *testing.T) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	checksDir := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(checksDir, "unit.json"), `{"id":"unit","scope":"tree","tier":"heavy"}`)
	base := heavyGit(t, root, "rev-parse", TrunkRef)
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip1 := heavyCommit(t, root, "tip-1")

	s, err := store.InitFixture(context.Background(), filepath.Join(work, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	recordFixtureLandings(t, s.Path(), tip1)

	entered := filepath.Join(work, "proof-entered")
	release := filepath.Join(work, "proof-release")
	defer os.WriteFile(release, []byte("ok"), 0o644)

	gate := executable(t, `touch `+entered+`
while [ ! -e `+release+` ]; do sleep 0.02; done
printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"unit","result":"pass"}]}}\n'`)

	binPath := filepath.Join(work, "tillr-drain.bin")
	if err := os.WriteFile(binPath, []byte("build-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	var execCount atomic.Int32
	restartCfg := &RestartConfig{
		Executable: binPath,
		Argv:       []string{"tillr-drain"},
		Check:      func(context.Context, string) error { return nil },
		Exec: func(string, []string, []string) error {
			execCount.Add(1)
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	l1 := newDetachedProofLoopFixture(t, root, work, s.Path(), gate, restartCfg)
	if err := l1.adoptRestart(ctx); err != nil {
		t.Fatalf("l1.adoptRestart: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("build-2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := l1.startHeavy(ctx, tip1); err != nil {
		t.Fatalf("startHeavy(tip1): %v", err)
	}

	// Wait until the detached --tillr-heavy-proof supervisor is actively running the check.
	deadline := time.After(hangGuard)
	for {
		if _, err := os.Stat(entered); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("detached heavy proof did not enter judge")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// File a pending restart while the detached proof runs. Under OQ-TPS1 Option A,
	// gateRestart must replace immediately without waiting for the proof.
	if _, err := restart.File(ctx, s.Path(), restart.RoleDrain, time.Now()); err != nil {
		t.Fatal(err)
	}
	handled, stop, err := l1.gateRestart(ctx, &LoopResult{})
	if !handled || stop != StopRestarted || err != nil {
		t.Fatalf("gateRestart while detached proof active = %v, %q, %v; want true, %q, nil", handled, stop, err, StopRestarted)
	}
	if execCount.Load() != 1 {
		t.Fatalf("exec count = %d, want 1", execCount.Load())
	}
	l1.heavyWG.Wait()

	// The detached --tillr-heavy-proof supervisor must still be running across the exec.
	gateDir, err := heavy.Directory(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	face, ok := heavyrun.ReadHeavyProofFace(gateDir)
	if !ok || !heavyrun.HeavyProofRunning(face) {
		t.Fatalf("detached heavy proof not running after exec (ok=%v, face=%+v)", ok, face)
	}

	// Simulate the post-exec replacement Loop re-attaching via gateRestart / reclaimDetachedHeavyProof.
	l2 := newDetachedProofLoopFixture(t, root, work, s.Path(), gate, nil)
	if handled, stop, err := l2.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
		t.Fatalf("post-exec gateRestart = %v, %q, %v; want false, \"\", nil", handled, stop, err)
	}
	if !l2.heavyInFlight() {
		t.Fatal("post-exec loop did not re-attach to detached heavy proof")
	}

	// Neither worker dispatch nor new gate starts are held while the detached proof runs.
	if pending, err := l2.buildRestartPending(ctx); err != nil || pending {
		t.Fatalf("buildRestartPending while re-attached proof runs = %v, %v; want false, nil", pending, err)
	}
	_, unlockGate, err := l2.lockRestartGateStart(ctx)
	if err != nil {
		t.Fatalf("lockRestartGateStart while re-attached proof runs = %v; want admitted", err)
	}
	unlockGate()

	// Let the detached proof finish and verify l2 settles refs/tillr/green to tip1.
	if err := os.WriteFile(release, []byte("done"), 0o644); err != nil {
		t.Fatal(err)
	}
	l2.heavyWG.Wait()

	green, err := heavy.Green(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if green != tip1 {
		t.Fatalf("refs/tillr/green after detached proof settled = %s, want %s", shortSHA(green), shortSHA(tip1))
	}
}

func TestDetachedHeavyProofSurvivesExecDuringSearchFirstRed(t *testing.T) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	checksDir := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(checksDir, "unit.json"), `{"id":"unit","scope":"tree","tier":"heavy","reads":["value"]}`)
	base := heavyCommit(t, root, "base-with-check")
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip1 := heavyCommit(t, root, "good-intermediate")
	tip2 := heavyCommit(t, root, "culprit")

	s, err := store.InitFixture(context.Background(), filepath.Join(work, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	recordFixtureLandings(t, s.Path(), tip1, tip2)

	callsLog := filepath.Join(work, "gate-calls.log")
	replayEntered := filepath.Join(work, "replay-entered")
	replayRelease := filepath.Join(work, "replay-release")
	defer os.WriteFile(replayRelease, []byte("ok"), 0o644)

	gate := executable(t, fmt.Sprintf(`echo "ARGS: $@" >> %s
only=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = --only ]; then shift; only=$1; fi
  shift
done
sha=$(git rev-parse HEAD)
echo "SHA=$sha ONLY=$only" >> %s
if [ -n "$only" ] && [ "$sha" = %s ]; then
  touch %s
  while [ ! -e %s ]; do sleep 0.02; done
fi
if [ "$sha" = %s ]; then
  printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"unit","result":"fail","reads":["value"],"detail":"broken"}]}}\n'
else
  printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"unit","result":"pass","reads":["value"]}]}}\n'
fi`, callsLog, callsLog, tip1, replayEntered, replayRelease, tip2))

	binPath := filepath.Join(work, "tillr-drain.bin")
	if err := os.WriteFile(binPath, []byte("build-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	restartCfg := &RestartConfig{
		Executable: binPath,
		Argv:       []string{"tillr-drain"},
		Check:      func(context.Context, string) error { return nil },
		Exec:       func(string, []string, []string) error { return nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	l1 := newDetachedProofLoopFixture(t, root, work, s.Path(), gate, restartCfg)
	if err := l1.adoptRestart(ctx); err != nil {
		t.Fatalf("l1.adoptRestart: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("build-2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := l1.startHeavy(ctx, tip2); err != nil {
		t.Fatalf("startHeavy(tip2): %v", err)
	}

	// Wait until SearchFirstRed enters the replay on tip1.
	for {
		if _, err := os.Stat(replayEntered); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			raw, _ := os.ReadFile(callsLog)
			t.Fatalf("detached heavy proof did not enter SearchFirstRed replay (%v); calls:\n%s", ctx.Err(), string(raw))
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Replace l1 mid-SearchFirstRed.
	if _, err := restart.File(ctx, s.Path(), restart.RoleDrain, time.Now()); err != nil {
		t.Fatal(err)
	}
	handled, stop, err := l1.gateRestart(ctx, &LoopResult{})
	if !handled || stop != StopRestarted || err != nil {
		t.Fatalf("gateRestart during SearchFirstRed = %v, %q, %v; want replaced", handled, stop, err)
	}
	l1.heavyWG.Wait()

	l2 := newDetachedProofLoopFixture(t, root, work, s.Path(), gate, nil)
	if handled, stop, err := l2.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
		t.Fatalf("post-exec gateRestart = %v, %q, %v; want false, \"\", nil", handled, stop, err)
	}
	if !l2.heavyInFlight() {
		t.Fatal("post-exec loop did not re-attach to detached SearchFirstRed")
	}

	if err := os.WriteFile(replayRelease, []byte("done"), 0o644); err != nil {
		t.Fatal(err)
	}
	l2.heavyWG.Wait()

	gateDir, err := heavy.Directory(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := heavy.ReadStatus(ctx, root, s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if status.Result != "red" {
		t.Fatalf("heavy status result = %q, want red (dir=%s)", status.Result, gateDir)
	}
	if len(status.FailingChecks) != 1 || status.FailingChecks[0].FirstRedLanding != tip2 {
		t.Fatalf("failing checks = %+v, want FirstRedLanding=%s", status.FailingChecks, tip2)
	}
}
