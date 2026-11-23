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
