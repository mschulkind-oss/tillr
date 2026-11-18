package drain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type heldBuildJudge struct {
	*judgeRouteSteps
	entered chan struct{}
	release chan struct{}
	cleaned chan struct{}
}

func (s *heldBuildJudge) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if strings.HasSuffix(req.RunID, "-judge") {
		s.entered <- struct{}{}
		<-s.release
		close(s.cleaned)
		if ctx.Err() != nil {
			return run.Record{}, "", ctx.Err()
		}
		return s.judgeRouteSteps.Run(ctx, req)
	}
	if strings.Contains(req.RunID, "-judge-restart") {
		req.NodeID = "evaluate"
	}
	return s.judgeRouteSteps.Run(ctx, req)
}

func TestRestartAnswersDuringAgentJudgeAndReplacementJudgesCandidateOnce(t *testing.T) {
	h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil })
	f, l, steps := judgeRouteFixture(t, "pass", true)
	l.cfg.Restart = h.l.cfg.Restart
	h.f, h.l = f, l
	held := &heldBuildJudge{judgeRouteSteps: steps, entered: make(chan struct{}, 1), release: make(chan struct{}), cleaned: make(chan struct{})}
	l.cfg.Steps = held
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	awaitRestartTest(t, held.entered, "judge entry")
	request := h.request(t, 1)[0]
	if _, _, err := l.judgeCandidate(context.Background(), Candidate{}, &LoopResult{}); !errors.Is(err, errRestartPending) {
		t.Fatalf("judge started with request pending: %v", err)
	}
	if stop, err := l.pass(context.Background(), result); err != nil || stop != StopRestarted {
		t.Fatalf("restart=%s/%v", stop, err)
	}
	// The examiner's wrapper ends while its session is still in flight. Consume
	// its discarded result before adopting, without spending any item retry.
	l.examinations.Wait()
	drainOwned(t, l, result)
	next := f.loop(t, held, l.cfg.Stage)
	next.cfg.Policy = l.cfg.Policy
	next.cfg.Restart = l.cfg.Restart
	next.build.Revision = "replacement"
	h.l = next
	t.Setenv(restart.RequestEnv, request.ID)
	t.Setenv(restart.FromEnv, "old-build")
	if err := next.adoptRestart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if answer := h.answer(t, request.ID); answer.Answer != restart.AnswerRestarted {
		t.Fatalf("answer=%+v", answer)
	}
	select {
	case <-held.cleaned:
		t.Fatal("answer waited for judge session")
	default:
	}
	close(held.release)
	awaitRestartTest(t, held.cleaned, "interrupted judge cleanup")
	for i := 0; i < 4; i++ {
		settleContinuation(t, next, result)
	}
	if len(steps.order) != 4 || strings.Join(steps.order, ",") != "worker,judge,gate,landing" {
		t.Fatalf("candidate was judged more than once: %v", steps.order)
	}
	if result.Landed != 1 || result.InfraFailures != 0 {
		t.Fatalf("result=%+v", result)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind='item.verdict'`).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("candidate verdicts=%d", n)
		}
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, RetryAckKind).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("interrupted judge spent %d retries", n)
		}
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind='drain.refusal' AND json_extract(payload,'$.step') LIKE 'judge-%'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("interrupted judge recorded %d failures", n)
		}
		return nil
	})
}
