package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type heldBuildJudge struct {
	*judgeRouteSteps
	mu      sync.Mutex
	held    bool
	entered chan struct{}
	release chan struct{}
	cleaned chan struct{}
}

func (s *heldBuildJudge) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if strings.HasSuffix(req.RunID, "-judge") {
		s.mu.Lock()
		already := s.held
		s.held = true
		s.mu.Unlock()
		if already {
			panic("heldBuildJudge: judge Run entered twice")
		}
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
	f, l, steps := judgeRouteFixture(t, "pass")
	l.cfg.Restart = h.l.cfg.Restart
	h.f, h.l = f, l
	held := &heldBuildJudge{judgeRouteSteps: steps, entered: make(chan struct{}, 1), release: make(chan struct{}), cleaned: make(chan struct{})}
	l.cfg.Steps = held
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	// The worker starts examination on completion; do not drain the owned
	// step until after the replacement has interrupted its judge.
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

type liveJudgeRestartSteps struct {
	*judgeRouteSteps
	mu           sync.Mutex
	proc         *ProcessSteps
	adapterPath  string
	judgeCalls   int
	judgeRunID   string
	judgeStarted chan struct{}
}

func (s *liveJudgeRestartSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if strings.HasSuffix(req.RunID, "-judge") || strings.Contains(req.RunID, "-judge") {
		s.mu.Lock()
		s.judgeCalls++
		call := s.judgeCalls
		if call == 1 {
			s.judgeRunID = req.RunID
		}
		ch := s.judgeStarted
		s.judgeStarted = nil
		s.mu.Unlock()
		if call > 1 {
			panic(fmt.Sprintf("live judge Run entered %d times (run=%s)", call, req.RunID))
		}
		if ch != nil {
			close(ch)
		}
		req.Command = []string{s.adapterPath}
		return s.proc.Run(ctx, req)
	}
	return s.judgeRouteSteps.Run(ctx, req)
}

func TestLiveJudgeRunSurvivesLoopRestartAndIsEnteredOnce(t *testing.T) {
	runBin := filepath.Join(scratchUnderDurable(t), "tillr-run")
	buildRun := exec.Command("go", "build", "-o", runBin, ".")
	buildRun.Dir = filepath.Join("..", "..", "tillr-run")
	if out, err := buildRun.CombinedOutput(); err != nil {
		t.Fatalf("build tillr-run: %v: %s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil })
	f, l, baseSteps := judgeRouteFixture(t, "pass")
	l.cfg.Restart = h.l.cfg.Restart
	h.f, h.l = f, l
	report := &strings.Builder{}
	l.cfg.Report = report

	work := scratchUnderDurable(t)
	startedFile := filepath.Join(work, "judge-started")
	releaseFile := filepath.Join(work, "judge-release")
	signalFile := filepath.Join(work, "judge-signals")
	verdictTemplate := filepath.Join(work, "verdict-template.json")

	adapterDir := filepath.Join(work, "adapter")
	if err := os.MkdirAll(adapterDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(adapterDir, "pause-kind"), "cannot-resume\n")
	adapterPath := filepath.Join(adapterDir, "run-verifier")
	writeForkFile(t, adapterPath, fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
trap 'echo SIGTERM >> %q; exit 143' TERM
trap 'echo SIGINT >> %q; exit 130' INT
touch %q
while [ ! -f %q ]; do
  sleep 0.05
done
cp %q "$YOLO_DURABLE_DIR/eval.output"
echo '{"type":"tillr_run_report","outcome":"ok"}'
`, signalFile, signalFile, startedFile, releaseFile, verdictTemplate))
	if err := os.Chmod(adapterPath, 0o755); err != nil {
		t.Fatal(err)
	}

	backendsDir, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends"))
	if err != nil {
		t.Fatal(err)
	}
	procSteps := &ProcessSteps{
		Root:        f.root,
		Store:       f.storePath,
		LaneRoot:    f.laneRoot,
		RunBin:      runBin,
		BackendsDir: backendsDir,
		Now:         l.cfg.Now,
	}
	steps := &liveJudgeRestartSteps{
		judgeRouteSteps: baseSteps,
		proc:            procSteps,
		adapterPath:     adapterPath,
		judgeStarted:    make(chan struct{}),
	}
	l.cfg.Steps = steps

	result := &LoopResult{}
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatalf("initial pass: %v\n%s", err, report.String())
	}
	awaitRestartTest(t, steps.judgeStarted, "live judge start")
	deadline := time.Now().Add(hangGuard)
	for {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s\nreport:\n%s", startedFile, report.String())
		}
		time.Sleep(15 * time.Millisecond)
	}

	head := runGit(t, l.lanePath("lane-1"), "rev-parse", "HEAD")
	headTree := runGit(t, l.lanePath("lane-1"), "rev-parse", head+"^{tree}")
	rawVerdict, err := json.Marshal(item.JudgeOutput{
		EvaluatedHead: head,
		EvaluatedTree: headTree,
		Outcome:       "pass",
		Verdicts: []item.JudgeEntry{{
			CriterionID:   "c1",
			CriterionHash: item.CriterionHash("the first criterion of item-a"),
			Verdict:       "done",
			ClaimClass:    "verification-only",
			Reason:        "verified across restart",
			Evidence:      []string{"commit:" + head},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, verdictTemplate, string(rawVerdict))

	request := h.request(t, 1)[0]
	stop, err := l.pass(ctx, result)
	if err != nil {
		t.Fatalf("restart pass: %v\n%s", err, report.String())
	}
	if stop != StopRestarted {
		t.Fatalf("restart pass stop = %q, want %q\n%s", stop, StopRestarted, report.String())
	}
	drainOwned(t, l, result)

	next := f.loop(t, steps, l.cfg.Stage)
	next.cfg.Policy = l.cfg.Policy
	next.cfg.Restart = l.cfg.Restart
	next.cfg.Report = report
	next.build.Revision = "replacement"
	h.l = next
	t.Setenv(restart.RequestEnv, request.ID)
	t.Setenv(restart.FromEnv, "old-build")
	if err := next.adoptRestart(ctx); err != nil {
		t.Fatalf("adoptRestart: %v", err)
	}

	result2 := &LoopResult{}
	passDone := make(chan error, 1)
	go func() {
		_, err := next.pass(ctx, result2)
		passDone <- err
	}()
	time.Sleep(250 * time.Millisecond)

	if sigRaw, err := os.ReadFile(signalFile); err == nil && len(sigRaw) > 0 {
		t.Fatalf("live judge run was signaled on restart: %s", sigRaw)
	}

	writeForkFile(t, releaseFile, "go\n")
	if err := <-passDone; err != nil {
		t.Fatalf("restarted pass 0: %v\n%s", err, report.String())
	}
	drainOwned(t, next, result2)

	for i := 0; i < 6 && result2.Landed == 0; i++ {
		settleContinuation(t, next, result2)
	}

	if sigRaw, err := os.ReadFile(signalFile); err == nil && len(sigRaw) > 0 {
		t.Fatalf("live judge run was signaled: %s", sigRaw)
	}
	steps.mu.Lock()
	calls := steps.judgeCalls
	steps.mu.Unlock()
	if calls != 1 {
		t.Fatalf("judgeCalls = %d, want 1\n%s", calls, report.String())
	}
	if result2.Landed != 1 || result2.InfraFailures != 0 {
		t.Fatalf("result2=%+v\n%s", result2, report.String())
	}
	if strings.Join(baseSteps.order, ",") != "worker,gate,landing" {
		t.Fatalf("order=%v, want [worker gate landing]", baseSteps.order)
	}
}
