package drain

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRefusedAndCurrentRestartKeepTheInflightJudge(t *testing.T) {
	for _, answer := range []string{restart.AnswerAlreadyCurrent, restart.AnswerRefused} {
		t.Run(answer, func(t *testing.T) {
			var disk []byte
			if answer == restart.AnswerRefused {
				disk = []byte("broken replacement")
			}
			h := newRestartHarness(t, disk, func(context.Context, string) error { return errors.New("cannot check replacement") })
			f, l, s := judgeRouteFixture(t, "pass")
			l.cfg.Restart = h.l.cfg.Restart
			h.f, h.l = f, l
			held := &heldBuildJudge{judgeRouteSteps: s, entered: make(chan struct{}, 1), release: make(chan struct{}), cleaned: make(chan struct{})}
			l.cfg.Steps = held
			result := &LoopResult{}
			if _, err := l.pass(context.Background(), result); err != nil {
				t.Fatal(err)
			}
			// Worker completion starts the judge before the next pass.
			awaitRestartTest(t, held.entered, "judge")
			request := h.request(t, 1)[0]
			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			a, err := restart.AwaitAnswer(ctx, f.storePath, request.ID, time.Millisecond)
			cancel()
			if err != nil || a.Answer != answer {
				t.Fatalf("answer=%+v/%v", a, err)
			}
			select {
			case <-held.cleaned:
				t.Fatal("nonreplacement ended the judge")
			default:
			}
			close(held.release)
			drainOwned(t, l, result)
			for i := 0; i < 3; i++ {
				settleContinuation(t, l, result)
			}
			if strings.Join(s.order, ",") != "worker,judge,gate,landing" || result.Landed != 1 || result.InfraFailures != 0 {
				t.Fatalf("order=%v result=%+v", s.order, result)
			}
		})
	}
}

func TestRestartDuringOlderJudgeTerminationDoesNotWaitForTheProcess(t *testing.T) {
	h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil })
	f, l, s := judgeRouteFixture(t, "pass")
	persistFixtureJudgeSelection(t, f)
	l.cfg.Restart = h.l.cfg.Restart
	h.f, h.l = f, l
	lane := l.laneDirPath("lane-1")
	session, err := NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane, BriefFile), "worker brief\n")
	writeForkFile(t, filepath.Join(lane, SessionIDFile), session+"\n")
	base := runGit(t, l.lanePath("lane-1"), "rev-parse", "HEAD")
	worker := run.Request{RunID: "run-item-a-a1", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture"}
	if _, _, err := s.Run(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	c := Candidate{ItemID: worker.ItemID, LaneID: worker.LaneID, RunID: worker.RunID, Worktree: l.lanePath(worker.LaneID), BaseSHA: base, HeadSHA: s.workerHead}
	req := l.cfg.Policy.request(worker.ItemID, worker.LaneID, worker.RunID+"-judge", "reviewer")
	receipt := &JudgeReceipt{Build: "older-build", Request: *req, WorkerBrief: []byte("worker brief\n"), WorkerSession: []byte(session + "\n")}
	if err := writeJudgeReceipt(filepath.Join(RunDir(f.root, worker.RunID), judgeReceiptFile), receipt); err != nil {
		t.Fatal(err)
	}
	identity, err := processStoreIdentity(f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "ignore-term.py")
	writeForkFile(t, script, "import signal,time\nsignal.signal(signal.SIGTERM, lambda *_: print('term', flush=True))\nprint('ready', flush=True)\nwhile True: time.sleep(100)\n")
	cmd := exec.Command("python3", script)
	cmd.Env = append(withoutProcessEnv(os.Environ(), "TILLR_RUN_ID", "TILLR_LOOP_STORE"), "TILLR_RUN_ID="+req.RunID, "TILLR_LOOP_STORE="+identity)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	reader := bufio.NewReader(stdout)
	if line, err := reader.ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("process readiness=%q/%v", line, err)
	}
	done := make(chan error, 1)
	go func() {
		ctx, unlock, err := l.lockRestartMechanical(context.Background())
		if err == nil {
			defer unlock()
			_, _, err = l.judgeCandidate(ctx, c, &LoopResult{})
		}
		done <- err
	}()
	if line, err := reader.ReadString('\n'); err != nil || line != "term\n" {
		t.Fatalf("termination request=%q/%v", line, err)
	}
	request := h.request(t, 1)[0]
	select {
	case err := <-done:
		if !errors.Is(err, errRestartPending) {
			t.Fatalf("older-judge recovery=%v", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("older judge termination held the next restart")
	}
	t.Setenv(restart.RequestEnv, request.ID)
	t.Setenv(restart.FromEnv, "old-build")
	// This fake exec did not inherit the parent's exec.Cmd waiter. Exercise the
	// protocol adoption without treating that test-owned process as inherited.
	if _, err := l.restarter().Adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if answer := h.answer(t, request.ID); answer.Answer != restart.AnswerRestarted {
		t.Fatalf("answer=%+v", answer)
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("answer waited for the interrupted judge: %v", err)
	}
}

func TestRetainedJudgeWaitAnswersAnotherRestart(t *testing.T) {
	h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil })
	fixtureWrite(t, h.f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, run.Record{RunID: "saved-judge", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: h.l.cfg.Now().Format(time.RFC3339), FinishedAt: h.l.cfg.Now().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}}); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE runs SET state='running' WHERE run_id='saved-judge'`)
		return err
	})
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := restartWork(context.Background(), h.l, func(child context.Context) (run.Record, error) {
			close(entered)
			return h.l.awaitJudgeRecord(child, "saved-judge")
		})
		done <- err
	}()
	awaitRestartTest(t, entered, "retained judge wait")
	request := h.request(t, 1)[0]
	select {
	case err := <-done:
		if !errors.Is(err, errRestartPending) {
			t.Fatalf("retained wait=%v", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("retained judge wait held the replacement")
	}
	t.Setenv(restart.RequestEnv, request.ID)
	t.Setenv(restart.FromEnv, "old-build")
	if err := h.l.adoptRestart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if answer := h.answer(t, request.ID); answer.Answer != restart.AnswerRestarted {
		t.Fatalf("answer=%+v", answer)
	}
}

func TestStagedJudgeReceiptWithoutAHandoffCanStartOnTheSameBuild(t *testing.T) {
	f, l, s := judgeRouteFixture(t, "pass")
	persistFixtureJudgeSelection(t, f)
	l.cfg.Restart = &RestartConfig{}
	lane := l.laneDirPath("lane-1")
	session, err := NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane, BriefFile), "worker brief\n")
	writeForkFile(t, filepath.Join(lane, SessionIDFile), session+"\n")
	base := runGit(t, l.lanePath("lane-1"), "rev-parse", "HEAD")
	worker := run.Request{RunID: "run-item-a-a1", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture"}
	if _, _, err := s.Run(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{ItemID: worker.ItemID, LaneID: worker.LaneID, RunID: worker.RunID, Worktree: l.lanePath(worker.LaneID), BaseSHA: base, HeadSHA: s.workerHead}
	req := l.cfg.Policy.request(worker.ItemID, worker.LaneID, worker.RunID+"-judge", "reviewer")
	build, _ := l.build.Hash()
	receipt := &JudgeReceipt{Build: build, Request: *req, WorkerBrief: []byte("worker brief\n"), WorkerSession: []byte(session + "\n")}
	receipt.Options.Head = candidate.HeadSHA
	receipt.Options.Tree = runGit(t, candidate.Worktree, "rev-parse", candidate.HeadSHA+"^{tree}")
	receipt.Options.ItemID = candidate.ItemID
	judgeSession, err := NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJudgeReceipt(filepath.Join(RunDir(f.root, worker.RunID), judgeReceiptFile), receipt); err != nil {
		t.Fatal(err)
	}
	if err := l.stageJudge(context.Background(), candidate, receipt, judgeSession); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := l.judgeCandidate(context.Background(), candidate, &LoopResult{}); err != nil || !ok {
		t.Fatalf("staged receipt refused a never-started judge: %v/%v", ok, err)
	}
	if strings.Join(s.order, ",") != "worker,judge" {
		t.Fatalf("order=%v", s.order)
	}
	raw, err := os.ReadFile(filepath.Join(lane, SessionIDFile))
	if err != nil || strings.TrimSpace(string(raw)) != session {
		t.Fatalf("worker session=%q/%v", raw, err)
	}
}
