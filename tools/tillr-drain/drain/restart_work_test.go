package drain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
)

type restartHeldSteps struct {
	recordingSteps
	entered  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func (s *restartHeldSteps) hold() { s.entered <- struct{}{}; <-s.release; close(s.finished) }
func (s *restartHeldSteps) Run(ctx context.Context, _ run.Request) (run.Record, string, error) {
	s.hold()
	return run.Record{}, "", ctx.Err()
}
func (s *restartHeldSteps) RollingConfigured() bool { return true }
func (s *restartHeldSteps) Rolling(ctx context.Context, _, _ string) (RollingOutcome, error) {
	s.hold()
	return RollingOutcome{Known: true, Green: true}, ctx.Err()
}
func (s *restartHeldSteps) JudgeHeavy(ctx context.Context, _, _ string) ([]HeavyCheck, error) {
	s.hold()
	return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, ctx.Err()
}

func awaitRestartTest(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(hangGuard):
		t.Fatalf("hung waiting for %s", what)
	}
}

func TestRestartAnswersWhileUnboundedWorkIsStillInflight(t *testing.T) {
	for _, kind := range []string{"worker", "rolling"} {
		t.Run(kind, func(t *testing.T) {
			h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil }, "")
			held := &restartHeldSteps{entered: make(chan struct{}, 1), release: make(chan struct{}), finished: make(chan struct{})}
			h.l.cfg.Steps = held
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			returned := make(chan error, 1)
			tip := runGit(t, h.f.root, "rev-parse", TrunkRef)
			switch kind {
			case "worker":
				go func() { _, _, err := h.l.runRestartable(ctx, run.Request{RunID: "worker"}); returned <- err }()
			case "rolling":
				go func() { returned <- h.l.maybeRolling(ctx, tip, true, &LoopResult{}) }()
			}
			awaitRestartTest(t, held.entered, "work entry")
			request := h.request(t, 1)[0]
			// Every entry refuses another session, even when invoked from inside an
			// examination rather than the planning pass's initial request check.
			if _, _, err := h.l.runRestartable(ctx, run.Request{}); !errors.Is(err, errRestartPending) {
				t.Fatalf("worker start=%v", err)
			}
			if err := h.l.maybeRolling(ctx, tip, true, &LoopResult{}); !errors.Is(err, errRestartPending) {
				t.Fatalf("rolling start=%v", err)
			}
			if _, _, err := h.l.judgeCandidate(ctx, Candidate{}, &LoopResult{}); !errors.Is(err, errRestartPending) {
				t.Fatalf("judge start=%v", err)
			}
			done, err := h.l.pass(ctx, &LoopResult{})
			if err != nil || done != StopRestarted {
				t.Fatalf("replacement=%s/%v", done, err)
			}
			t.Setenv(restart.RequestEnv, request.ID)
			t.Setenv(restart.FromEnv, "old-build")
			if err := h.l.adoptRestart(ctx); err != nil {
				t.Fatal(err)
			}
			if answer := h.answer(t, request.ID); answer.Answer != restart.AnswerRestarted {
				t.Fatalf("answer=%+v", answer)
			}
			select {
			case <-held.finished:
				t.Fatal("restart waited for unbounded work")
			default:
			}
			close(held.release)
			awaitRestartTest(t, held.finished, "old work cleanup")
			select {
			case err := <-returned:
				if err != nil && !errors.Is(err, errRestartPending) {
					t.Fatal(err)
				}
			case <-time.After(hangGuard):
				t.Fatal("work wrapper hung")
			}
		})
	}
}

func TestPendingRestartPreventsSecondGateFromStarvingReplacement(t *testing.T) {
	for _, tc := range []struct {
		name             string
		gateRestartFirst bool
	}{
		{name: "settlement_before_gateRestart", gateRestartFirst: false},
		{name: "settlement_after_gateRestart", gateRestartFirst: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil })
			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			defer cancel()

			// Gate 1 takes its read hold before the restart is requested.
			_, releaseGate1, err := h.l.lockRestartSettlement(ctx)
			if err != nil {
				t.Fatalf("gate 1 lock: %v", err)
			}
			gate1Released := false
			defer func() {
				if !gate1Released {
					releaseGate1()
				}
			}()

			// File a pending restart request while Gate 1 holds restartMechanical.
			h.request(t, 1)
			if tc.gateRestartFirst {
				if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
					t.Fatalf("gateRestart while gate 1 held = %v, %q, %v; want deferred", handled, stop, err)
				}
			}

			waiting := make(chan struct{}, 1)
			h.l.restartWaitObs = func() {
				select {
				case waiting <- struct{}{}:
				default:
				}
			}

			gate2Acquired := make(chan struct{}, 1)
			gate2Done := make(chan error, 1)
			go func() {
				_, unlock, err := h.l.lockRestartSettlement(ctx)
				if err == nil {
					gate2Acquired <- struct{}{}
					unlock()
				}
				gate2Done <- err
			}()

			awaitRestartTest(t, waiting, "gate 2 waiting on pending restart")
			select {
			case <-gate2Acquired:
				t.Fatal("gate 2 took a read hold while restart was pending")
			case err := <-gate2Done:
				t.Fatalf("gate 2 returned before restart was answered: %v", err)
			default:
			}

			// Gate 1 still finishes normally while the restart waits.
			if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
				t.Fatalf("gateRestart before gate 1 release = %v, %q, %v; want deferred", handled, stop, err)
			}

			// As soon as Gate 1 releases, the restart replaces the loop before Gate 2 takes a hold.
			releaseGate1()
			gate1Released = true
			if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); !handled || stop != StopRestarted || err != nil {
				t.Fatalf("gateRestart after gate 1 release = %v, %q, %v; want replaced", handled, stop, err)
			}
			if len(h.execed) == 0 {
				t.Fatal("replacement did not exec")
			}

			select {
			case err := <-gate2Done:
				if !errors.Is(err, errRestartPending) {
					t.Fatalf("gate 2 err = %v, want errRestartPending", err)
				}
			case <-time.After(hangGuard):
				t.Fatal("gate 2 did not unblock after replacement")
			}
			select {
			case <-gate2Acquired:
				t.Fatal("gate 2 acquired a hold despite loop replacement")
			default:
			}
		})
	}
}

func TestPendingRestartAnsweredWithoutReplacingUnblocksWaitingGate(t *testing.T) {
	for _, answer := range []string{restart.AnswerAlreadyCurrent, restart.AnswerRefused} {
		t.Run(answer, func(t *testing.T) {
			var disk []byte
			check := func(context.Context, string) error { return errors.New("candidate rejected") }
			if answer == restart.AnswerRefused {
				disk = []byte("broken build")
			}
			h := newRestartHarness(t, disk, check)
			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			defer cancel()

			_, releaseGate1, err := h.l.lockRestartSettlement(ctx)
			if err != nil {
				t.Fatalf("gate 1 lock: %v", err)
			}
			gate1Released := false
			defer func() {
				if !gate1Released {
					releaseGate1()
				}
			}()

			req := h.request(t, 1)[0]

			waiting := make(chan struct{}, 1)
			h.l.restartWaitObs = func() {
				select {
				case waiting <- struct{}{}:
				default:
				}
			}

			gate2Held := make(chan struct{})
			releaseGate2 := make(chan struct{})
			gate2Done := make(chan error, 1)
			go func() {
				_, unlock, err := h.l.lockRestartSettlement(ctx)
				if err != nil {
					gate2Done <- err
					return
				}
				close(gate2Held)
				<-releaseGate2
				unlock()
				gate2Done <- nil
			}()

			awaitRestartTest(t, waiting, "gate 2 waiting on pending restart")
			select {
			case <-gate2Held:
				t.Fatal("gate 2 took a read hold before restart was answered")
			default:
			}

			releaseGate1()
			gate1Released = true
			if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); !handled || stop != "" || err != nil {
				t.Fatalf("gateRestart = %v, %q, %v; want handled without replacing", handled, stop, err)
			}
			if got := h.answer(t, req.ID).Answer; got != answer {
				t.Fatalf("answer = %q, want %q", got, answer)
			}
			if len(h.execed) != 0 {
				t.Fatalf("non-replacement request execed: %v", h.execed)
			}

			awaitRestartTest(t, gate2Held, "gate 2 acquiring hold after non-replacement answer")
			close(releaseGate2)
			select {
			case err := <-gate2Done:
				if err != nil {
					t.Fatalf("gate 2 err = %v, want nil", err)
				}
			case <-time.After(hangGuard):
				t.Fatal("gate 2 did not finish")
			}
		})
	}
}

func TestPendingRestartWhileDeferredAllowsNewGateHold(t *testing.T) {
	h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	releaseRoll, rollDone := make(chan struct{}), make(chan struct{})
	h.l.rollAction.submit("tip", func(string) { defer close(rollDone); <-releaseRoll })
	rollReleased := false
	defer func() {
		if !rollReleased {
			close(releaseRoll)
			<-rollDone
		}
	}()

	_, releaseGate1, err := h.l.lockRestartSettlement(ctx)
	if err != nil {
		t.Fatalf("gate 1 lock: %v", err)
	}
	defer releaseGate1()

	h.request(t, 1)
	if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
		t.Fatalf("gateRestart while deferred = %v, %q, %v; want deferred", handled, stop, err)
	}

	_, releaseGate2, err := h.l.lockRestartSettlement(ctx)
	if err != nil {
		t.Fatalf("gate 2 lock while restartDeferred() = %v; want admitted", err)
	}
	releaseGate2()

	close(releaseRoll)
	<-rollDone
	rollReleased = true

	// Restart is no longer deferred, so it still waits for Gate 1 and replaces once Gate 1 releases.
	if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
		t.Fatalf("gateRestart while gate 1 still active = %v, %q, %v; want deferred", handled, stop, err)
	}
	releaseGate1()
	if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); !handled || stop != StopRestarted || err != nil {
		t.Fatalf("gateRestart after roll and gate 1 finished = %v, %q, %v; want replaced", handled, stop, err)
	}
}

func TestPendingRestartNotStarvedByNewGateStarts(t *testing.T) {
	for _, tc := range []struct {
		name             string
		gateRestartFirst bool
	}{
		{name: "new_gate_before_gateRestart", gateRestartFirst: false},
		{name: "gateRestart_before_new_gate", gateRestartFirst: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil })
			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			defer cancel()

			// Gate 1 started before the new build / restart request.
			_, releaseGate1, err := h.l.lockRestartGateStart(ctx)
			if err != nil {
				t.Fatalf("gate 1 lock: %v", err)
			}
			gate1Released := false
			defer func() {
				if !gate1Released {
					releaseGate1()
				}
			}()

			// File a pending restart request while Gate 1 holds restartMechanical.
			h.request(t, 1)
			if tc.gateRestartFirst {
				if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
					t.Fatalf("gateRestart while gate 1 held = %v, %q, %v; want deferred", handled, stop, err)
				}
			}

			waiting := make(chan struct{}, 4)
			h.l.restartWaitObs = func() {
				select {
				case waiting <- struct{}{}:
				default:
				}
			}

			// New candidates keep becoming ready while Gate 1 is running and must not
			// acquire restartMechanical and postpone the restart past Gate 1.
			const arrivals = 2
			acquired := make(chan int, arrivals+1)
			done := make(chan error, arrivals+1)
			launchCandidate := func(id int) {
				go func() {
					_, unlock, err := h.l.lockRestartGateStart(ctx)
					if err == nil {
						acquired <- id
						unlock()
					}
					done <- err
				}()
			}
			for i := 0; i < arrivals; i++ {
				launchCandidate(i + 2)
			}
			for i := 0; i < arrivals; i++ {
				select {
				case <-waiting:
				case id := <-acquired:
					t.Fatalf("candidate %d acquired restartMechanical while restart was pending behind gate 1", id)
				case err := <-done:
					t.Fatalf("new gate start returned before restart was answered: %v", err)
				case <-time.After(hangGuard):
					t.Fatal("hung waiting for new gate start to block on pending restart")
				}
			}

			// Gate 1 finishes and releases its hold. Even if another candidate becomes
			// ready before gateRestart runs, it must also wait rather than taking a hold.
			releaseGate1()
			gate1Released = true
			launchCandidate(arrivals + 2)
			select {
			case <-waiting:
			case id := <-acquired:
				t.Fatalf("candidate %d acquired restartMechanical after gate 1 released and before restart", id)
			case err := <-done:
				t.Fatalf("post-release candidate returned before restart was answered: %v", err)
			case <-time.After(hangGuard):
				t.Fatal("hung waiting for post-release candidate to block on pending restart")
			}

			if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); !handled || stop != StopRestarted || err != nil {
				t.Fatalf("gateRestart after gate 1 release = %v, %q, %v; want replaced", handled, stop, err)
			}
			if len(h.execed) == 0 {
				t.Fatal("replacement did not exec")
			}

			for i := 0; i < arrivals+1; i++ {
				select {
				case err := <-done:
					if !errors.Is(err, errRestartPending) {
						t.Fatalf("waiting candidate err = %v, want errRestartPending", err)
					}
				case <-time.After(hangGuard):
					t.Fatal("waiting candidate did not unblock after replacement")
				}
			}
			select {
			case id := <-acquired:
				t.Fatalf("candidate %d acquired hold despite loop replacement", id)
			default:
			}
		})
	}

	t.Run("non_replacement_unblocks_waiting_gate_start", func(t *testing.T) {
		h := newRestartHarness(t, nil, func(context.Context, string) error { return nil })
		ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
		defer cancel()

		_, releaseGate1, err := h.l.lockRestartGateStart(ctx)
		if err != nil {
			t.Fatalf("gate 1 lock: %v", err)
		}
		gate1Released := false
		defer func() {
			if !gate1Released {
				releaseGate1()
			}
		}()

		req := h.request(t, 1)[0]
		if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
			t.Fatalf("gateRestart while gate 1 held = %v, %q, %v; want deferred", handled, stop, err)
		}

		waiting := make(chan struct{}, 1)
		h.l.restartWaitObs = func() {
			select {
			case waiting <- struct{}{}:
			default:
			}
		}

		gate2Held := make(chan struct{})
		releaseGate2 := make(chan struct{})
		gate2Done := make(chan error, 1)
		go func() {
			_, unlock, err := h.l.lockRestartGateStart(ctx)
			if err != nil {
				gate2Done <- err
				return
			}
			close(gate2Held)
			<-releaseGate2
			unlock()
			gate2Done <- nil
		}()

		awaitRestartTest(t, waiting, "gate 2 waiting on pending non-replacement restart")
		select {
		case <-gate2Held:
			t.Fatal("gate 2 acquired hold before non-replacement restart was answered")
		default:
		}

		releaseGate1()
		gate1Released = true
		if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); !handled || stop != "" || err != nil {
			t.Fatalf("gateRestart = %v, %q, %v; want handled without replacing", handled, stop, err)
		}
		if got := h.answer(t, req.ID).Answer; got != restart.AnswerAlreadyCurrent {
			t.Fatalf("answer = %q, want %q", got, restart.AnswerAlreadyCurrent)
		}

		awaitRestartTest(t, gate2Held, "gate 2 acquiring hold after non-replacement answer")
		close(releaseGate2)
		select {
		case err := <-gate2Done:
			if err != nil {
				t.Fatalf("gate 2 err = %v, want nil", err)
			}
		case <-time.After(hangGuard):
			t.Fatal("gate 2 did not finish")
		}
	})
}

func TestPendingRestartDoesNotWaitOnActiveTrunkProof(t *testing.T) {
	t.Run("exec_does_not_wait_for_active_proof", func(t *testing.T) {
		h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil })
		ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
		defer cancel()

		// Simulate an active trunk proof. Under OQ-TPS1 Option A, gateRestart must not
		// wait for the trunk proof to finish.
		h.l.heavyMu.Lock()
		h.l.heavyRunning = true
		h.l.heavyMu.Unlock()
		defer func() {
			h.l.heavyMu.Lock()
			h.l.heavyRunning = false
			h.l.heavyMu.Unlock()
		}()

		// Gate 1 is already running when the restart is requested.
		_, releaseGate1, err := h.l.lockRestartGateStart(ctx)
		if err != nil {
			t.Fatalf("gate 1 lock: %v", err)
		}
		gate1Released := false
		defer func() {
			if !gate1Released {
				releaseGate1()
			}
		}()

		h.request(t, 1)
		if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
			t.Fatalf("gateRestart while gate 1 active = %v, %q, %v; want deferred", handled, stop, err)
		}

		// Gate 1 finishes while the proof is still running; the restart replaces
		// immediately without waiting for the proof.
		releaseGate1()
		gate1Released = true
		if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); !handled || stop != StopRestarted || err != nil {
			t.Fatalf("gateRestart while proof active = %v, %q, %v; want replaced", handled, stop, err)
		}
		if len(h.execed) == 0 {
			t.Fatal("replacement did not exec")
		}
	})

	t.Run("new_trunk_proof_waits_for_pending_restart_while_gate_runs", func(t *testing.T) {
		h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil })
		ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
		defer cancel()

		_, releaseGate, err := h.l.lockRestartSettlement(ctx)
		if err != nil {
			t.Fatalf("gate lock: %v", err)
		}
		defer releaseGate()

		h.request(t, 1)
		if h.l.beginHeavyProof(ctx) {
			t.Fatal("beginHeavyProof started a new trunk proof while a restart was pending behind a running gate")
		}
	})
}
