package drain

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type workerGateSteps struct {
	persistentSteps
	landLock    sync.Mutex // Stand in for the lander's land.lock, not the gate.
	landing     chan struct{}
	landRelease chan struct{}
	gateStarted chan Candidate
}

func (s *workerGateSteps) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	if c.ItemID == "item-b" {
		s.gateStarted <- c
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *workerGateSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.landLock.Lock()
	defer s.landLock.Unlock()
	if c.ItemID == "item-a" {
		close(s.landing)
		select {
		case <-s.landRelease:
		case <-ctx.Done():
			return LandOutcome{}, ctx.Err()
		}
	}
	return LandOutcome{Result: LandLanded}, nil
}

func TestWorkerEndStartsGateWithoutPassOrSiblingLanding(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	at := l.cfg.Now().UTC()
	l.cfg.Now = func() time.Time { return at }
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	steps := &workerGateSteps{
		persistentSteps: persistentSteps{store: f.storePath, started: make(chan struct{}), release: make(chan struct{})},
		landing:         make(chan struct{}), landRelease: make(chan struct{}), gateStarted: make(chan Candidate, 1),
	}
	l.cfg.Steps = steps
	stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt"]' WHERE item_id = 'item-b'`)
		return err
	})
	plans := mustPlan(t, l)
	var result LoopResult
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	for _, p := range plans {
		if p.decision.ItemID == "item-a" {
			timing := &PassTiming{AtMS: at.UnixMilli(), DecisionCounts: map[string]int{ActionSettle: 1}}
			if _, stale, err := l.applyReadyPlan(ctx, p, &base, at, timing, nil, &result); stale || err != nil {
				t.Fatalf("claim sibling settlement: stale=%t err=%v", stale, err)
			}
			if err := l.observePass(timing, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	select {
	case <-steps.landing:
	case <-time.After(hangGuard):
		t.Fatal("sibling did not enter landing")
	}
	var plan plannedApplication
	for _, p := range plans {
		if p.decision.ItemID == "item-b" {
			plan = p
		}
	}
	d := plan.decision
	if d.Action != ActionDispatch || d.LaneID != "lane-2" {
		t.Fatalf("expected worker dispatch on lane-2: %+v", d)
	}
	lane := l.lanePath(d.LaneID)
	d.RunRequest.WorkingDir = lane
	generation := ""
	ok, work, err := l.applyPlanGeneration(ctx, plan, base, at, &generation)
	if !ok || err != nil {
		t.Fatalf("claim worker: %t / %v", ok, err)
	}
	d = work.Decision
	if err := l.observePass(&PassTiming{TickID: generation, AtMS: at.UnixMilli(), DecisionCounts: map[string]int{ActionDispatch: 1}}, ""); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), FenceFile), "candidate.txt\n")
	writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(lane, "candidate.txt"), "candidate\n")
	runGit(t, lane, "add", "candidate.txt")
	runGit(t, lane, "commit", "-qm", "candidate B")
	l.launchRun(ctx, d, at, nil, &result)
	close(steps.release)
	// Do not run a pass or consume the completion that the old path needed
	// to start settlement. Keep the sibling in Land until our gate starts.
	select {
	case c := <-steps.gateStarted:
		if c.RunID != d.RunRequest.RunID {
			t.Fatalf("gated the wrong run: %+v", c)
		}
	case completion := <-l.completions:
		t.Fatalf("worker returned to the queue before starting its gate: step=%s outcome=%+v", completion.step, completion.outcome)
	case <-time.After(hangGuard):
		t.Fatal("gate did not start while the sibling was landing")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var finished, ready int64
		for kind, seq := range map[string]*int64{run.FinishKind: &finished, candidateReadyKind: &ready} {
			if err := tx.Reader().QueryRow(`SELECT seq FROM events WHERE kind=? AND object_id=?`, kind, d.RunRequest.RunID).Scan(seq); err != nil {
				return err
			}
		}
		if finished >= ready {
			t.Errorf("candidate ready (seq %d) before worker finished (seq %d)", ready, finished)
		}
		var candidateAt int64
		if err := tx.Reader().QueryRow(`SELECT ts FROM events WHERE seq=?`, ready).Scan(&candidateAt); err != nil {
			return err
		}
		if candidateAt != at.UnixMilli() {
			t.Errorf("candidate timestamp %d, want injected time %d", candidateAt, at.UnixMilli())
		}
		return nil
	})
	close(steps.landRelease)
	drainOwned(t, l, &result)
	if result.Landed != 2 {
		t.Fatalf("expected both gated candidates to land: %+v", result)
	}
	timings := readHandoffTimings(t, f.root, d.RunRequest.RunID)
	var exam *handoffTiming
	for i := range timings {
		if timings[i].Phase == "examination" {
			exam = &timings[i]
			break
		}
	}
	if exam == nil {
		t.Fatalf("missing examination timing for %s: %+v", d.RunRequest.RunID, timings)
	}
	assertExaminationAccounting(t, *exam, []string{
		"restart_lock_wait",
		"claim_worker_settlement",
		"settle",
		"claim_settled_judge",
		"verify_pre_gate",
		"candidate_gate",
		"post_gate",
		"pre_land",
		"land",
		"post_land",
		"lane_cleanup",
		"release_examination",
	})
}

func TestRestartLockWaitDuringConcurrentSlowStageAndRestartCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	h := newRestartHarness(t, nil, func(context.Context, string) error { return nil }, "item-a", "item-b")
	h.l.cfg.Lanes = []string{"lane-1", "lane-2"}
	h.l.cfg.Now = time.Now

	stageEntered := make(chan struct{})
	releaseStage := make(chan struct{})
	stageExited := make(chan struct{})
	var releaseOnce sync.Once
	releaseSlowStage := func() {
		releaseOnce.Do(func() { close(releaseStage) })
	}
	defer releaseSlowStage()
	h.l.cfg.Stage = stagerFunc(func(stageCtx context.Context, spec StageSpec) error {
		if spec.ItemID == "item-a" {
			defer close(stageExited)
			close(stageEntered)
			select {
			case <-releaseStage:
			case <-stageCtx.Done():
				return stageCtx.Err()
			}
		}
		return nil
	})
	h.l.cfg.Steps = &recordingSteps{}
	holdWALKeeper(t, h.f.storePath)

	// Prepare item-b with a finished candidate on lane-2 ready for inline examination.
	fixtureWrite(t, h.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt"]' WHERE item_id = 'item-b'`)
		return err
	})
	base := runGit(t, h.f.root, "rev-parse", TrunkRef)
	plans := mustPlan(t, h.l)
	var planB plannedApplication
	for _, p := range plans {
		if p.decision.ItemID == "item-b" {
			planB = p
			break
		}
	}
	dB := planB.decision
	if dB.Action != ActionDispatch || dB.LaneID != "lane-2" {
		t.Fatalf("expected item-b dispatch on lane-2: %+v", dB)
	}
	laneB := h.l.lanePath(dB.LaneID)
	dB.RunRequest.WorkingDir = laneB
	genB := ""
	ok, workB, err := h.l.applyPlanGeneration(ctx, planB, base, h.l.cfg.Now().UTC(), &genB)
	if !ok || err != nil {
		t.Fatalf("claim item-b: ok=%v err=%v", ok, err)
	}
	dB = workB.Decision
	writeForkFile(t, filepath.Join(h.l.laneDirPath(dB.LaneID), FenceFile), "candidate.txt\n")
	writeForkFile(t, filepath.Join(h.l.laneDirPath(dB.LaneID), BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(laneB, "candidate.txt"), "candidate\n")
	runGit(t, laneB, "add", "candidate.txt")
	runGit(t, laneB, "commit", "-qm", "candidate B")
	recB := run.Record{
		RunID: dB.RunRequest.RunID, ItemID: dB.ItemID, LaneID: dB.LaneID, Backend: dB.RunRequest.Backend,
		State: run.StateExited, ExitCode: countPtr(0),
		StartedAt: "2026-11-23T00:00:00Z", FinishedAt: "2026-11-23T00:00:00Z",
	}
	fixtureWrite(t, h.f, func(tx *store.Tx) error { return applyTerminal(tx, recB) })

	// Claim item-a on lane-1 and start its slow stage, blocked on releaseStage.
	baseA := runGit(t, h.f.root, "rev-parse", TrunkRef)
	pA := decisionFor(t, h.l, "item-a")
	ok, workA, err := h.l.applyPlan(ctx, pA, baseA, h.l.cfg.Now().UTC())
	if !ok || err != nil || workA == nil || workA.Decision.ItemID != "item-a" {
		t.Fatalf("applyPlan item-a: ok=%v work=%+v err=%v", ok, workA, err)
	}
	dA := workA.Decision

	var stageResult LoopResult
	stageDone := make(chan error, 1)
	go func() {
		stageDone <- h.l.stageAndLaunchClaimed(ctx, dA, workA.BaseSHA, h.l.cfg.Now().UTC(), &stageResult)
	}()
	select {
	case <-stageEntered:
	case <-time.After(hangGuard):
		t.Fatal("slow stage on lane-1 did not start")
	}

	// File a non-replacing restart (rolling build check) while lane-1 is staging.
	req := h.request(t, 1)[0]
	type restartResult struct {
		handled bool
		stop    string
		err     error
	}
	restartChecked := make(chan restartResult, 1)
	go func() {
		handled, stop, err := h.l.gateRestart(ctx, &LoopResult{})
		restartChecked <- restartResult{handled: handled, stop: stop, err: err}
	}()
	var checked restartResult
	select {
	case checked = <-restartChecked:
	case <-time.After(hangGuard):
		t.Fatal("restart check blocked behind slow stage")
	}
	if checked.err != nil || !checked.handled || checked.stop != "" {
		t.Fatalf("gateRestart during slow stage = handled=%v stop=%q err=%v; want handled=true stop=\"\"", checked.handled, checked.stop, checked.err)
	}
	if got := h.answer(t, req.ID).Answer; got != restart.AnswerAlreadyCurrent {
		t.Fatalf("restart answer = %q, want %q", got, restart.AnswerAlreadyCurrent)
	}
	select {
	case <-stageExited:
		t.Fatal("slow stage exited before restart check was answered")
	default:
	}
	select {
	case err := <-stageDone:
		t.Fatalf("stageAndLaunchClaimed finished before release: err=%v", err)
	default:
	}

	// Examine finished item-b on lane-2 while item-a's slow stage is still running.
	completion := runCompletion{id: dB.RunRequest.RunID, itemID: dB.ItemID, step: "run"}
	h.l.examineWorkerEnd(ctx, dB, nil, &completion)
	if completion.outcome.err != nil {
		t.Fatalf("examineWorkerEnd err = %v", completion.outcome.err)
	}
	releaseSlowStage()
	select {
	case err := <-stageDone:
		if err != nil {
			t.Fatalf("stageAndLaunchClaimed err = %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("slow stage did not finish after release")
	}
	select {
	case c := <-h.l.completions:
		if c.outcome.err != nil {
			t.Fatalf("staged run completion err = %v", c.outcome.err)
		}
	case <-time.After(hangGuard):
		t.Fatal("staged run did not complete")
	}

	timings := readHandoffTimings(t, h.f.root, dB.RunRequest.RunID)
	var exam *handoffTiming
	for i := range timings {
		if timings[i].Phase == "examination" {
			exam = &timings[i]
			break
		}
	}
	if exam == nil {
		t.Fatalf("missing examination timing for %s: %+v", dB.RunRequest.RunID, timings)
	}
	var sawRestartLockWait bool
	for _, p := range exam.Phases {
		if p.Phase == "restart_lock_wait" {
			sawRestartLockWait = true
			break
		}
	}
	if !sawRestartLockWait {
		t.Fatalf("missing restart_lock_wait span in %+v", exam.Phases)
	}
}

func TestPendingRestartAnsweredDuringSlowExamination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	h := newRestartHarness(t, nil, func(context.Context, string) error { return nil }, "item-a")
	h.l.cfg.Lanes = []string{"lane-1"}
	h.l.cfg.Now = time.Now
	holdWALKeeper(t, h.f.storePath)

	dispatched := stageFinishedCandidateOnLane(t, h.f, h.l, "item-a", "lane-1")
	at := time.Now().UTC()
	claim, err := h.l.claimWorkerSettlement(ctx, dispatched.ItemID, dispatched.RunRequest.RunID, at)
	if err != nil || !claim.claimed {
		t.Fatalf("claimWorkerSettlement: claimed=%v err=%v", claim.claimed, err)
	}
	timing := &PassTiming{TickID: claim.tickID, AtMS: at.UnixMilli()}
	d := claim.decision
	work := claim.work

	examEntered := make(chan struct{})
	releaseExam := make(chan struct{})
	var nowCalls int
	var nowMu sync.Mutex
	h.l.cfg.Now = func() time.Time {
		nowMu.Lock()
		nowCalls++
		call := nowCalls
		nowMu.Unlock()
		// Call 1 is inside claimSettledCandidateJudge's ReadTx, mid-examination
		// with no WriteTx open, so gateRestart can answer a pending request.
		if call == 1 {
			close(examEntered)
			<-releaseExam
		}
		return time.Now()
	}

	examDone := make(chan runCompletion, 1)
	go func() {
		examDone <- h.l.examineClaimed(ctx, d, work, at, timing, nil)
	}()

	select {
	case <-examEntered:
	case <-time.After(hangGuard):
		t.Fatal("examination did not reach claimSettledCandidateJudge")
	}

	req := h.request(t, 1)[0]
	handled, stop, err := h.l.gateRestart(ctx, &LoopResult{})
	if err != nil || !handled || stop != "" {
		close(releaseExam)
		<-examDone
		t.Fatalf("gateRestart during slow examination = handled=%v stop=%q err=%v; want handled=true stop=\"\"", handled, stop, err)
	}
	if got := h.answer(t, req.ID).Answer; got != restart.AnswerAlreadyCurrent {
		close(releaseExam)
		<-examDone
		t.Fatalf("restart answer = %q, want %q", got, restart.AnswerAlreadyCurrent)
	}

	close(releaseExam)
	select {
	case got := <-examDone:
		if got.outcome.err != nil {
			t.Fatalf("examineClaimed err = %v", got.outcome.err)
		}
	case <-time.After(hangGuard):
		t.Fatal("examination did not finish after release")
	}
}
