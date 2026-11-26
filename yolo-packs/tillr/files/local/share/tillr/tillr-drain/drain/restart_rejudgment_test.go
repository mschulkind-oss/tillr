package drain

import (
	"context"
	"encoding/json"
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

type rejudgeRestartSteps struct {
	*rebasedJudgeRouteSteps

	mu             sync.Mutex
	storePath      string
	now            func() time.Time
	leaseTimeoutMS int
	judgeCalls     int
	rejudgeRunID   string
	rejudgeStarted chan struct{}
	blockFirst     bool
}

func (s *rejudgeRestartSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	isJudge := req.Node == "judge" || strings.HasSuffix(req.RunID, "-judge") || req.NodeID == "evaluate" || strings.HasSuffix(req.NodeID, "/evaluate")
	if isJudge {
		s.mu.Lock()
		s.judgeCalls++
		call := s.judgeCalls
		block := s.blockFirst
		timeoutMS := s.leaseTimeoutMS
		s.mu.Unlock()
		if call == 2 && block {
			// Simulate tillr-run claiming the lane lease for the fresh re-judgment run
			// and still holding it when the loop restarts in place.
			leaseReq := req
			leaseReq.TimeoutMS = timeoutMS
			db, err := store.Open(context.Background(), s.storePath, true)
			if err != nil {
				return run.Record{}, "", err
			}
			err = db.WriteTx(context.Background(), func(tx *store.Tx) error {
				_, err := run.ClaimLease(context.Background(), tx, leaseReq, leaseReq.LaneID, s.now())
				return err
			})
			db.Close()
			if err != nil {
				return run.Record{}, "", err
			}
			s.mu.Lock()
			s.rejudgeRunID = req.RunID
			ch := s.rejudgeStarted
			s.rejudgeStarted = nil
			s.mu.Unlock()
			if ch != nil {
				close(ch)
			}
			<-ctx.Done()
			return run.Record{}, "", ctx.Err()
		}
	}
	return s.rebasedJudgeRouteSteps.Run(ctx, req)
}

func TestRestartDuringPreGateRebaseRejudgmentReachesGate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		timeoutMS int
	}{
		{name: "unbounded-lease", timeoutMS: 0},
		{name: "bounded-lease", timeoutMS: 600_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runRestartDuringPreGateRebaseRejudgmentTest(t, tc.timeoutMS)
		})
	}
}

func runRestartDuringPreGateRebaseRejudgmentTest(t *testing.T, leaseTimeoutMS int) {
	ctx := context.Background()
	f, l, baseSteps := judgeRouteFixture(t, "pass")
	base := &rebasedJudgeRouteSteps{judgeRouteSteps: baseSteps}
	report := &strings.Builder{}
	l.cfg.Report = report

	writeForkFile(t, f.root+"/config/tillr/workflows/scout-then-build@v1.json", evaluatorWorkflow)
	runGit(t, f.root, "add", "config/tillr/workflows")
	runGit(t, f.root, "commit", "-qm", "evaluator workflow")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1' WHERE item_id='item-a'`)
		return err
	})

	h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil })
	l.cfg.Restart = h.l.cfg.Restart
	h.f, h.l = f, l

	steps := &rejudgeRestartSteps{
		rebasedJudgeRouteSteps: base,
		storePath:              f.storePath,
		now:                    l.cfg.Now,
		leaseTimeoutMS:         leaseTimeoutMS,
		rejudgeStarted:         make(chan struct{}),
		blockFirst:             true,
	}
	l.cfg.Steps = steps

	var repaired string
	repairedReady := make(chan struct{})
	baseSteps.afterJudge = func() {
		steps.mu.Lock()
		calls := steps.judgeCalls
		steps.mu.Unlock()
		if calls != 1 {
			return
		}
		writeForkFile(t, f.root+"/repair.txt", "trunk repair\n")
		runGit(t, f.root, "add", "repair.txt")
		runGit(t, f.root, "commit", "-qm", "fix: repair trunk after judge")
		repaired = runGit(t, f.root, "rev-parse", TrunkRef)
		if err := recordTrunkRepair(ctx, f.root, repaired, "repair deferred to proof of "+shortSHA(repaired)+": focused check passed"); err != nil {
			t.Fatalf("recordTrunkRepair: %v", err)
		}
		close(repairedReady)
	}

	// Drive the workflow through scout -> build -> evaluate (which triggers the
	// trunk repair in afterJudge). Once repairedReady closes, the next pass starts
	// candidate examination -> pre-gate rebase -> re-judgment (-fresh) in the background.
	result := &LoopResult{}
	for i := 0; i < 12; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatalf("pass %d: %v\n%s", i, err, report.String())
		}
		drainOwned(t, l, result)
		select {
		case <-repairedReady:
			goto startExamination
		default:
		}
	}
	t.Fatalf("never finished initial evaluate node\n%s", report.String())

startExamination:
	// This pass claims ActionJudge and launches examination in a goroutine; do not
	// call drainOwned yet because the re-judgment run blocks until restart.
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatalf("examination pass: %v\n%s", err, report.String())
	}
	awaitRestartTest(t, steps.rejudgeStarted, "pre-gate rebase re-judgment start")

	steps.mu.Lock()
	rejudgeRunID := steps.rejudgeRunID
	steps.mu.Unlock()
	if !strings.HasSuffix(rejudgeRunID, "-fresh") {
		t.Fatalf("rejudgeRunID = %q, want -fresh suffix\n%s", rejudgeRunID, report.String())
	}

	// Trigger an in-place restart while the re-judgment run holds lane-1's lease.
	restartReq := h.request(t, 1)[0]
	stop, err := l.pass(ctx, result)
	if err != nil {
		t.Fatalf("restart pass: %v\n%s", err, report.String())
	}
	if stop != StopRestarted {
		t.Fatalf("restart pass stop = %q, want %q\n%s", stop, StopRestarted, report.String())
	}
	drainOwned(t, l, result)

	// Confirm the store recorded examination release for the evaluator run and that
	// lane-1 is still leased to the re-judgment run right after exec.
	var releasedCount int
	var laneState, laneRunID string
	fixtureRead(t, f, func(r *store.Reader) error {
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id='run-item-a-a1-n3'`, examinationReleasedKind).Scan(&releasedCount); err != nil {
			return err
		}
		return r.QueryRow(`SELECT state, COALESCE(run_id, '') FROM lanes WHERE lane_id='lane-1'`).Scan(&laneState, &laneRunID)
	})
	if releasedCount == 0 {
		t.Fatalf("expected %s for run-item-a-a1-n3\n%s", examinationReleasedKind, report.String())
	}
	if laneState != run.LaneStateLeased || laneRunID != rejudgeRunID {
		t.Fatalf("lane-1 = (%q, %q), want (%q, %q)", laneState, laneRunID, run.LaneStateLeased, rejudgeRunID)
	}

	// While the candidate waits on its re-judgment run, `tillr drain why` must name
	// what it waits on rather than saying only that its lane is kept.
	var whyAns WhyAnswer
	lanes := []string{"lane-1"}
	fixtureRead(t, f, func(r *store.Reader) error {
		var err error
		whyAns, err = Why(r, Pass{AvailableLanes: &lanes, Now: l.cfg.Now().UTC().Format(time.RFC3339Nano)}, l.cfg.Policy, nil)
		return err
	})
	var row *WhyRow
	for i := range whyAns.Rows {
		if whyAns.Rows[i].ItemID == "item-a" {
			row = &whyAns.Rows[i]
			break
		}
	}
	if row == nil {
		t.Fatalf("Why omitted item-a: %+v", whyAns.Rows)
	}
	if !strings.Contains(row.Why, rejudgeRunID) {
		t.Fatalf("Why must name what the candidate waits on (%s), got position=%q reason=%q why=%q", rejudgeRunID, row.Position, row.Reason, row.Why)
	}
	if strings.Contains(row.Why, "candidate awaits pin verification and lane release") {
		t.Fatalf("Why must not say only that its lane awaits pin verification and lane release, got why=%q", row.Why)
	}

	// Now start the restarted loop on the new build with no operator action.
	steps.mu.Lock()
	steps.blockFirst = false
	steps.mu.Unlock()

	cfg2 := l.cfg
	cfg2.Steps = steps
	l2, err := NewLoop(cfg2)
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l2.build = BuildStamp{Digest: "next-build"}
	t.Setenv(restart.RequestEnv, restartReq.ID)
	t.Setenv(restart.FromEnv, "old-build")
	if _, err := l2.restarter().Adopt(ctx); err != nil {
		t.Fatalf("Adopt: %v", err)
	}

	result2 := &LoopResult{}
	for i := 0; i < 10 && result2.Landed == 0; i++ {
		if _, err := l2.pass(ctx, result2); err != nil {
			t.Fatalf("restarted pass %d: %v\n%s", i, err, report.String())
		}
		drainOwned(t, l2, result2)
	}
	if len(base.gated) == 0 {
		t.Fatalf("restarted loop never ran the gate for item-a\n%s", report.String())
	}
	if result2.Landed != 1 {
		t.Fatalf("restarted loop Landed = %d, want 1\n%s", result2.Landed, report.String())
	}
}

type liveRejudgeRestartSteps struct {
	*rebasedJudgeRouteSteps

	mu             sync.Mutex
	proc           *ProcessSteps
	adapterPath    string
	judgeCalls     int
	rejudgeRunID   string
	rejudgeStarted chan struct{}
}

func (s *liveRejudgeRestartSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	isJudge := req.Node == "judge" || strings.HasSuffix(req.RunID, "-judge") || req.NodeID == "evaluate" || strings.HasSuffix(req.NodeID, "/evaluate")
	if isJudge {
		s.mu.Lock()
		s.judgeCalls++
		call := s.judgeCalls
		s.mu.Unlock()
		if call == 2 {
			s.mu.Lock()
			s.rejudgeRunID = req.RunID
			ch := s.rejudgeStarted
			s.rejudgeStarted = nil
			s.mu.Unlock()
			if ch != nil {
				close(ch)
			}
			req.Command = []string{s.adapterPath}
			return s.proc.Run(ctx, req)
		}
	}
	return s.rebasedJudgeRouteSteps.Run(ctx, req)
}

func TestLiveRejudgmentRunSurvivesLoopRestartAndAdoptsVerdict(t *testing.T) {
	runBin := filepath.Join(scratchUnderDurable(t), "tillr-run")
	buildRun := exec.Command("go", "build", "-o", runBin, ".")
	buildRun.Dir = filepath.Join("..", "..", "tillr-run")
	if out, err := buildRun.CombinedOutput(); err != nil {
		t.Fatalf("build tillr-run: %v: %s", err, out)
	}
	for _, tc := range []struct {
		name    string
		inPlace bool
	}{
		{name: "in-place", inPlace: true},
		{name: "operator-stop", inPlace: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runLiveRejudgmentRunSurvivesRestart(t, runBin, tc.inPlace)
		})
	}
}

func runLiveRejudgmentRunSurvivesRestart(t *testing.T, runBin string, inPlace bool) {
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	f, l, baseSteps := judgeRouteFixture(t, "pass")
	base := &rebasedJudgeRouteSteps{judgeRouteSteps: baseSteps}
	report := &strings.Builder{}
	l.cfg.Report = report

	writeForkFile(t, f.root+"/config/tillr/workflows/scout-then-build@v1.json", evaluatorWorkflow)
	runGit(t, f.root, "add", "config/tillr/workflows")
	runGit(t, f.root, "commit", "-qm", "evaluator workflow")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1' WHERE item_id='item-a'`)
		return err
	})

	h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil })
	l.cfg.Restart = h.l.cfg.Restart
	h.f, h.l = f, l

	work := scratchUnderDurable(t)
	startedFile := filepath.Join(work, "rejudge-started")
	releaseFile := filepath.Join(work, "rejudge-release")
	signalFile := filepath.Join(work, "rejudge-signals")
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

	steps := &liveRejudgeRestartSteps{
		rebasedJudgeRouteSteps: base,
		proc:                   procSteps,
		adapterPath:            adapterPath,
		rejudgeStarted:         make(chan struct{}),
	}
	l.cfg.Steps = steps
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("report:\n%s", report.String())
			steps.mu.Lock()
			rid := steps.rejudgeRunID
			steps.mu.Unlock()
			if rid != "" {
				if raw, err := os.ReadFile(filepath.Join(RunDir(f.root, rid), RunStderrFile)); err == nil {
					t.Logf("run stderr:\n%s", raw)
				}
				if raw, err := os.ReadFile(filepath.Join(RunDir(f.root, rid), RunRecordFile)); err == nil {
					t.Logf("run record:\n%s", raw)
				}
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				rows, err := r.Query(`SELECT seq, kind, object_type, object_id, payload FROM events ORDER BY seq DESC LIMIT 15`)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var seq int64
					var kind, ot, oid, payload string
					if err := rows.Scan(&seq, &kind, &ot, &oid, &payload); err == nil {
						t.Logf("event %d: %s %s/%s %s", seq, kind, ot, oid, payload)
					}
				}
				return nil
			})
		}
	})

	var repaired string
	repairedReady := make(chan struct{})
	baseSteps.afterJudge = func() {
		steps.mu.Lock()
		calls := steps.judgeCalls
		steps.mu.Unlock()
		if calls != 1 {
			return
		}
		writeForkFile(t, f.root+"/repair.txt", "trunk repair\n")
		runGit(t, f.root, "add", "repair.txt")
		runGit(t, f.root, "commit", "-qm", "fix: repair trunk after judge")
		repaired = runGit(t, f.root, "rev-parse", TrunkRef)
		if err := recordTrunkRepair(ctx, f.root, repaired, "repair deferred to proof of "+shortSHA(repaired)+": focused check passed"); err != nil {
			t.Fatalf("recordTrunkRepair: %v", err)
		}
		close(repairedReady)
	}

	result := &LoopResult{}
	for i := 0; i < 12; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatalf("pass %d: %v\n%s", i, err, report.String())
		}
		drainOwned(t, l, result)
		select {
		case <-repairedReady:
			goto startExamination
		default:
		}
	}
	t.Fatalf("never finished initial evaluate node\n%s", report.String())

startExamination:
	ctx1, cancel1 := context.WithCancel(ctx)
	defer cancel1()
	if _, err := l.pass(ctx1, result); err != nil {
		t.Fatalf("examination pass: %v\n%s", err, report.String())
	}
	awaitRestartTest(t, steps.rejudgeStarted, "pre-gate rebase re-judgment start")
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

	steps.mu.Lock()
	rejudgeRunID := steps.rejudgeRunID
	steps.mu.Unlock()
	if !strings.HasSuffix(rejudgeRunID, "-fresh") {
		t.Fatalf("rejudgeRunID = %q, want -fresh suffix\n%s", rejudgeRunID, report.String())
	}

	// Prepare the valid verdict JSON for the rebased candidate head.
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
			Reason:        "verified rebased",
			Evidence:      []string{"commit:" + head},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, verdictTemplate, string(rawVerdict))

	var restartReq restart.Request
	if inPlace {
		// Trigger an in-place restart while the live re-judgment run is still executing.
		restartReq = h.request(t, 1)[0]
		stop, err := l.pass(ctx1, result)
		if err != nil {
			t.Fatalf("restart pass: %v\n%s", err, report.String())
		}
		if stop != StopRestarted {
			t.Fatalf("restart pass stop = %q, want %q\n%s", stop, StopRestarted, report.String())
		}
		drainOwned(t, l, result)
	} else {
		// Simulate an operator stop (without --end-runs) while the re-judgment run is executing.
		cancel1()
		l.examinations.Wait()
	}

	// Start the restarted loop on a new build while the re-judgment run is still running.
	cfg2 := l.cfg
	cfg2.Steps = steps
	l2, err := NewLoop(cfg2)
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l2.build = BuildStamp{Digest: "next-build"}
	if inPlace {
		t.Setenv(restart.RequestEnv, restartReq.ID)
		t.Setenv(restart.FromEnv, "old-build")
		if _, err := l2.restarter().Adopt(ctx); err != nil {
			t.Fatalf("Adopt: %v", err)
		}
	}

	// Run the new loop while the re-judgment is still in progress, then release it.
	result2 := &LoopResult{}
	passDone := make(chan error, 1)
	go func() {
		_, err := l2.pass(ctx, result2)
		passDone <- err
	}()
	time.Sleep(250 * time.Millisecond)

	// Verify the re-judgment run was NOT signaled.
	if sigRaw, err := os.ReadFile(signalFile); err == nil && len(sigRaw) > 0 {
		t.Fatalf("re-judgment run was signaled on restart: %s", sigRaw)
	}

	// Release the running re-judgment process so it writes eval.output and exits 0.
	writeForkFile(t, releaseFile, "go\n")

	if err := <-passDone; err != nil {
		t.Fatalf("restarted pass 0: %v\n%s", err, report.String())
	}
	drainOwned(t, l2, result2)

	for i := 1; i < 12 && result2.Landed == 0; i++ {
		if _, err := l2.pass(ctx, result2); err != nil {
			t.Fatalf("restarted pass %d: %v\n%s", i, err, report.String())
		}
		drainOwned(t, l2, result2)
	}

	if sigRaw, err := os.ReadFile(signalFile); err == nil && len(sigRaw) > 0 {
		t.Fatalf("re-judgment run was signaled: %s", sigRaw)
	}
	steps.mu.Lock()
	calls := steps.judgeCalls
	steps.mu.Unlock()
	if calls != 2 {
		t.Fatalf("judgeCalls = %d, want 2 (no restart re-judgment)\n%s", calls, report.String())
	}
	if len(base.gated) == 0 {
		t.Fatalf("restarted loop never ran the gate for item-a\n%s", report.String())
	}
	if result2.Landed != 1 {
		t.Fatalf("restarted loop Landed = %d, want 1\n%s", result2.Landed, report.String())
	}
}
