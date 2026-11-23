package drain

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type blockingReplayGateSteps struct {
	laneSteps
	gateOutcome GateOutcome
	mu          sync.Mutex
	gateCalls   int
	landCalls   int
	replayCalls int
	entered     chan string
	release     chan struct{}
	redByRev    map[string][]string
}

func (s *blockingReplayGateSteps) Gate(_ context.Context, _ Candidate) (GateOutcome, error) {
	s.mu.Lock()
	s.gateCalls++
	s.mu.Unlock()
	return s.gateOutcome, nil
}

func (s *blockingReplayGateSteps) Replay(ctx context.Context, _ Candidate, rev string, _ []string) (ReplayOutcome, error) {
	s.mu.Lock()
	s.replayCalls++
	red := append([]string(nil), s.redByRev[rev]...)
	s.mu.Unlock()
	if s.entered != nil {
		select {
		case s.entered <- rev:
		case <-ctx.Done():
			return ReplayOutcome{}, ctx.Err()
		}
	}
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return ReplayOutcome{}, ctx.Err()
		}
	}
	return ReplayOutcome{Rev: rev, Known: true, Red: red}, nil
}

func (s *blockingReplayGateSteps) Land(_ context.Context, _ Candidate) (LandOutcome, error) {
	s.mu.Lock()
	s.landCalls++
	s.mu.Unlock()
	return LandOutcome{Result: LandLanded}, nil
}

func TestGateFailedReplayReleasesLaneToWaitingWork(t *testing.T) {
	for _, tc := range []struct {
		name     string
		workflow bool
	}{
		{name: "ordinary", workflow: false},
		{name: "workflow", workflow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				f refusalLoopFixture
				l *Loop
				c Candidate
			)
			if tc.workflow {
				f, l, c = heldWorkflowCandidate(t)
			} else {
				f, l, c = heldCandidateFixture(t)
			}
			// Advance trunk so base != tip (repaired-at-tip path, matching 2026-11-22).
			writeForkFile(t, filepath.Join(f.root, "trunk.txt"), "repaired\n")
			runGit(t, f.root, "add", "trunk.txt")
			runGit(t, f.root, "commit", "-qm", "repair trunk")
			tip := runGit(t, f.root, "rev-parse", TrunkRef)

			steps := &blockingReplayGateSteps{
				gateOutcome: GateOutcome{
					Outcome:       GateFailed,
					FailingChecks: []string{"unit"},
				},
				entered: make(chan string, 2),
				release: make(chan struct{}),
				redByRev: map[string][]string{
					c.BaseSHA: {"unit"}, // red on base
					tip:       nil,      // green on tip -> repaired at tip
				},
			}
			l.cfg.Steps = steps

			// Claim examination for item-a on lane-1 and launch it in the background.
			plans := mustPlan(t, l)
			var p plannedApplication
			for _, cand := range plans {
				if cand.decision.ItemID == "item-a" {
					p = cand
					break
				}
			}
			if p.decision.Action != ActionJudge {
				t.Fatalf("initial item-a decision = %+v, want ActionJudge", p.decision)
			}
			ok, work, err := l.applyPlan(context.Background(), p, tip, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("claim examination: %v / %v", ok, err)
			}
			l.launchExamination(context.Background(), p.decision, work, l.cfg.Now(), &PassTiming{}, nil)

			// Wait for the base replay to enter.
			select {
			case rev := <-steps.entered:
				if rev != c.BaseSHA {
					t.Fatalf("first replay rev = %s, want base %s", rev, c.BaseSHA)
				}
			case <-time.After(hangGuard):
				t.Fatal("base replay did not start")
			}

			// While replay is running, lane-1 must already be idle and released in the store.
			w := pendingForTest(t, f, "item-a")
			if w == nil || !w.LaneReleased {
				t.Fatalf("item-a pending work during replay = %+v, want LaneReleased=true", w)
			}
			var laneState string
			var laneRun *string
			fixtureRead(t, f, func(r *store.Reader) error {
				return r.QueryRow(`SELECT state, run_id FROM lanes WHERE lane_id=?`, "lane-1").Scan(&laneState, &laneRun)
			})
			if laneState != run.LaneStateIdle || laneRun != nil {
				t.Fatalf("lane-1 during replay = (%q, %v), want idle/nil", laneState, laneRun)
			}

			// Planning now must hold item-a as gate-replay (not assign a duplicate lane)
			// and immediately offer lane-1 to waiting item-b.
			nextPlans := mustPlan(t, l)
			byItem := map[string]Decision{}
			for _, np := range nextPlans {
				byItem[np.decision.ItemID] = np.decision
			}
			if da := byItem["item-a"]; da.Action != ActionHold || da.Reason != ReasonGateReplay {
				t.Fatalf("item-a plan during replay = %+v, want hold/%s", da, ReasonGateReplay)
			}
			if db := byItem["item-b"]; db.Action != ActionDispatch || db.LaneID != "lane-1" {
				t.Fatalf("item-b plan during replay = %+v, want dispatch on lane-1", db)
			}

			// Why must show item-a in gate-replay with its lane released, never run-live,
			// never run-awaits-recovery, and never "candidate awaits pin verification and lane release".
			var why WhyAnswer
			lanes := l.lanes()
			fixtureRead(t, f, func(r *store.Reader) error {
				var err error
				why, err = Why(r, Pass{AvailableLanes: &lanes, StorePath: f.storePath}, l.cfg.Policy, nil)
				return err
			})
			for _, row := range why.Rows {
				if row.ItemID == "item-a" {
					if row.Reason != ReasonGateReplay || row.Position != "candidate" || row.Class != "moving" {
						t.Fatalf("item-a why during replay = (reason=%q position=%q class=%q why=%q), want (%q, candidate, moving)",
							row.Reason, row.Position, row.Class, row.Why, ReasonGateReplay)
					}
					if strings.Contains(row.Why, "candidate awaits pin verification and lane release") {
						t.Fatalf("item-a why still reports kept lane: %s", row.Why)
					}
					if row.LaneStatus != "released" {
						t.Fatalf("item-a lane status = %q (%s), want released", row.LaneStatus, row.Why)
					}
				}
			}

			// Let item-b claim lane-1 and commit its own work while item-a's replay is still running.
			var planB plannedApplication
			for _, np := range nextPlans {
				if np.decision.ItemID == "item-b" {
					planB = np
					break
				}
			}
			ok, _, err = l.applyPlan(context.Background(), planB, tip, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("item-b claim lane-1: %v / %v", ok, err)
			}
			lane1Worktree := l.lanePath("lane-1")
			writeForkFile(t, filepath.Join(lane1Worktree, "item-b.txt"), "work for item-b\n")
			runGit(t, lane1Worktree, "add", "item-b.txt")
			runGit(t, lane1Worktree, "commit", "-qm", "item-b work")
			itemBHead := runGit(t, lane1Worktree, "rev-parse", "HEAD")

			// Release both base and tip replays so item-a finishes its examination.
			close(steps.release)
			select {
			case done := <-l.completions:
				delete(l.active, done.id)
				if done.outcome.err != nil || done.outcome.stop != "" {
					t.Fatalf("item-a examination finished with error: %+v", done.outcome)
				}
			case <-time.After(hangGuard):
				t.Fatal("item-a examination did not finish")
			}

			// item-a's refusal must be recorded and its examination done.
			if got := pendingForTest(t, f, "item-a"); got == nil || !got.Done {
				t.Fatalf("item-a pending after replay = %+v, want Done=true", got)
			}
			var refusalDetail string
			fixtureRead(t, f, func(r *store.Reader) error {
				return r.QueryRow(`SELECT json_extract(payload, '$.detail') FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, RefusalKind, c.RunID).Scan(&refusalDetail)
			})
			if !strings.Contains(refusalDetail, shortSHA(tip)) || !strings.Contains(refusalDetail, "lane was released") {
				t.Fatalf("item-a refusal detail = %q, want repaired tip %s and lane-released note", refusalDetail, shortSHA(tip))
			}

			// lane-1 must still belong to item-b with its commit untouched.
			fixtureRead(t, f, func(r *store.Reader) error {
				return r.QueryRow(`SELECT state, run_id FROM lanes WHERE lane_id=?`, "lane-1").Scan(&laneState, &laneRun)
			})
			if laneState != run.LaneStateLeased || laneRun == nil || *laneRun != planB.decision.RunRequest.RunID {
				t.Fatalf("lane-1 lease after item-a finished = (%q, %v), want leased to %s", laneState, laneRun, planB.decision.RunRequest.RunID)
			}
			if head := runGit(t, lane1Worktree, "rev-parse", "HEAD"); head != itemBHead {
				t.Fatalf("lane-1 HEAD = %s, want item-b commit %s untouched", head, itemBHead)
			}
		})
	}
}

func TestGateFailedReplayPassInheritedRestagesAndLands(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	steps := &blockingReplayGateSteps{
		gateOutcome: GateOutcome{
			Outcome:               GateFailed,
			FailingChecks:         []string{"other"},
			CandidateChangedPaths: []string{"candidate.txt"},
			CheckReads:            map[string][]string{"other": {"other/"}},
		},
		redByRev: map[string][]string{
			c.BaseSHA: {"other"},
		},
	}
	l.cfg.Steps = steps

	// First pass: gate fails, replay runs (releasing lane-1), check is red on base/tip
	// and disjoint from candidate.txt -> promoted to GatePassedInherited, candidateGatedKind
	// recorded, and examination ends without touching the released lane.
	examineHeldCandidate(t, l)

	w := pendingForTest(t, f, "item-a")
	if w == nil || w.Done || !w.Gated || !w.LaneReleased || w.Examiner != nil {
		t.Fatalf("after GatePassedInherited with released lane: %+v", w)
	}
	if steps.landCalls != 0 {
		t.Fatalf("landCalls = %d before restage, want 0", steps.landCalls)
	}

	// Next pass: planner assigns item-a to lane-1, stages pinned commit, and lands directly without re-gating.
	plans := mustPlan(t, l)
	var p plannedApplication
	for _, cand := range plans {
		if cand.decision.ItemID == "item-a" {
			p = cand
			break
		}
	}
	if p.decision.Action != ActionJudge || p.decision.LaneID != "lane-1" {
		t.Fatalf("restage plan = %+v, want ActionJudge on lane-1", p.decision)
	}
	ok, work, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim restage: %v / %v", ok, err)
	}
	l.launchExamination(context.Background(), p.decision, work, l.cfg.Now(), &PassTiming{}, nil)
	select {
	case done := <-l.completions:
		delete(l.active, done.id)
		if done.outcome.err != nil || done.outcome.stop != "" {
			t.Fatalf("restage land failed: %+v", done.outcome)
		}
	case <-time.After(hangGuard):
		t.Fatal("restage land did not finish")
	}
	if steps.gateCalls != 1 || steps.landCalls != 1 {
		t.Fatalf("gateCalls=%d landCalls=%d, want 1/1 (no second gate)", steps.gateCalls, steps.landCalls)
	}
}

func TestGateFailedReplayDoesNotBlockInPlaceRestart(t *testing.T) {
	h := newRestartHarness(t, []byte("new-build"), nil, "item-a")
	steps := &blockingReplayGateSteps{
		gateOutcome: GateOutcome{
			Outcome:       GateFailed,
			FailingChecks: []string{"unit"},
		},
		entered: make(chan string, 1),
		release: make(chan struct{}),
	}
	h.l.cfg.Steps = steps

	// Acquire settlement lock (as verify does) and start judgeTrunkChecks in a goroutine.
	ctx, unlock, err := h.l.lockRestartSettlement(context.Background())
	if err != nil {
		t.Fatalf("lockRestartSettlement: %v", err)
	}
	defer unlock()

	base := runGit(t, h.f.root, "rev-parse", TrunkRef)
	done := make(chan error, 1)
	go func() {
		_, _, err := h.l.judgeTrunkChecks(ctx, steps, Candidate{ItemID: "item-a", BaseSHA: base}, base, []string{"unit"}, false)
		done <- err
	}()

	select {
	case <-steps.entered:
	case <-time.After(hangGuard):
		t.Fatal("replay did not start")
	}

	// File a restart request while Replay is blocked.
	req := h.request(t, 1)[0]
	select {
	case err := <-done:
		if err != errRestartPending {
			t.Fatalf("judgeTrunkChecks err = %v, want errRestartPending", err)
		}
	case <-time.After(hangGuard):
		close(steps.release)
		t.Fatal("in-place restart was blocked by gate-failure replay")
	}
	if !containsEnv(h.env, restart.RequestEnv+"="+req.ID) {
		t.Fatalf("restart did not exec for %s: env=%v", req.ID, h.env)
	}
	close(steps.release)
}

// TestGateFailedReplayRecordsRefusalAndRedispatchesInSamePass pins c6:
// a candidate whose gate fails after its lane was released before replay gets
// its drain.refusal recorded and goes back to a worker in the same pass,
// while leaving the sibling item on the released lane untouched and without
// any timer.
func TestGateFailedReplayRecordsRefusalAndRedispatchesInSamePass(t *testing.T) {
	for _, tc := range []struct {
		name          string
		repairedOnTip bool
	}{
		{name: "base-green", repairedOnTip: false},
		{name: "repaired-at-tip", repairedOnTip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, c := heldCandidateFixture(t)
			l.cfg.Lanes = []string{"lane-1", "lane-2"}
			if err := l.stageLanes(context.Background()); err != nil {
				t.Fatalf("stageLanes: %v", err)
			}
			if tc.repairedOnTip {
				writeForkFile(t, filepath.Join(f.root, "trunk.txt"), "repaired\n")
				runGit(t, f.root, "add", "trunk.txt")
				runGit(t, f.root, "commit", "-qm", "repair trunk")
			}
			tip := runGit(t, f.root, "rev-parse", TrunkRef)

			redByRev := map[string][]string{}
			if tc.repairedOnTip {
				redByRev[c.BaseSHA] = []string{"unit"}
				redByRev[tip] = nil
			}
			steps := &blockingReplayGateSteps{
				gateOutcome: GateOutcome{
					Outcome:       GateFailed,
					FailingChecks: []string{"unit"},
				},
				entered:  make(chan string, 2),
				release:  make(chan struct{}),
				redByRev: redByRev,
			}
			l.cfg.Steps = steps

			// Claim and launch examination for item-a on lane-1.
			plans := mustPlan(t, l)
			var planA plannedApplication
			for _, cand := range plans {
				if cand.decision.ItemID == "item-a" {
					planA = cand
					break
				}
			}
			ok, work, err := l.applyPlan(context.Background(), planA, tip, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("claim examination: %v / %v", ok, err)
			}
			l.launchExamination(context.Background(), planA.decision, work, l.cfg.Now(), &PassTiming{}, nil)

			// Wait until replay has released lane-1 and entered base replay.
			select {
			case <-steps.entered:
			case <-time.After(hangGuard):
				t.Fatal("replay did not start")
			}

			// Assign released lane-1 to sibling item-b and commit work there while
			// item-a's replay is still running.
			nextPlans := mustPlan(t, l)
			var planB plannedApplication
			for _, np := range nextPlans {
				if np.decision.ItemID == "item-b" {
					planB = np
					break
				}
			}
			if planB.decision.Action != ActionDispatch || planB.decision.LaneID != "lane-1" {
				t.Fatalf("item-b plan during replay = %+v, want dispatch on lane-1", planB.decision)
			}
			ok, _, err = l.applyPlan(context.Background(), planB, tip, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("item-b claim lane-1: %v / %v", ok, err)
			}
			lane1Worktree := l.lanePath("lane-1")
			writeForkFile(t, filepath.Join(lane1Worktree, "item-b.txt"), "sibling work\n")
			runGit(t, lane1Worktree, "add", "item-b.txt")
			runGit(t, lane1Worktree, "commit", "-qm", "item-b work")
			itemBHead := runGit(t, lane1Worktree, "rev-parse", "HEAD")

			// Let replay finish so item-a records drain.refusal and drain.run-examined.
			close(steps.release)
			select {
			case done := <-l.completions:
				if stop, err := l.takeCompletion(done, &LoopResult{}); err != nil || stop != "" {
					t.Fatalf("item-a examination finished with error: stop=%q err=%v", stop, err)
				}
			case <-time.After(hangGuard):
				t.Fatal("item-a examination did not finish")
			}

			// Verify drain.refusal (step=gate) and drain.run-examined were recorded.
			var refusalCount int
			var refusalStep, refusalDetail string
			var examinedCount int
			fixtureRead(t, f, func(r *store.Reader) error {
				if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=?`, RefusalKind, c.RunID).Scan(&refusalCount); err != nil {
					return err
				}
				if err := r.QueryRow(`SELECT json_extract(payload, '$.step'), json_extract(payload, '$.detail') FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, RefusalKind, c.RunID).Scan(&refusalStep, &refusalDetail); err != nil {
					return err
				}
				return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=?`, examinationDoneKind, c.RunID).Scan(&examinedCount)
			})
			if refusalCount != 1 || refusalStep != RefusalGate {
				t.Fatalf("refusal = (count=%d step=%q detail=%q), want count=1 step=%q",
					refusalCount, refusalStep, refusalDetail, RefusalGate)
			}
			if tc.repairedOnTip && !strings.Contains(refusalDetail, "lane was released") {
				t.Fatalf("repaired-at-tip refusal detail = %q, want lane-released note", refusalDetail)
			}
			if examinedCount != 1 {
				t.Fatalf("drain.run-examined count = %d, want 1", examinedCount)
			}

			// One l.pass with lane-2 free must dispatch item-a back to a worker in that
			// pass while leaving sibling item-b on lane-1 untouched.
			res := &LoopResult{}
			stop, err := l.pass(context.Background(), res)
			if err != nil {
				t.Fatalf("l.pass after refusal: %v", err)
			}
			if res.Dispatched != 1 {
				t.Fatalf("l.pass Dispatched = %d (stop=%q res=%+v), want 1 (plans=%+v)", res.Dispatched, stop, res, mustPlan(t, l))
			}
			select {
			case done := <-l.completions:
				if stop, err := l.takeCompletion(done, res); err != nil || stop != "" {
					t.Fatalf("dispatched retry worker finished with error: stop=%q err=%v", stop, err)
				}
			case <-time.After(hangGuard):
				t.Fatal("dispatched retry worker did not finish")
			}
			if got := steps.ranOn("item-a"); len(got) != 1 || got[0] != "lane-2" {
				t.Fatalf("item-a retry lanes = %v, want [lane-2]", got)
			}
			if w := pendingForTest(t, f, "item-a"); w == nil || w.Decision.Action != ActionDispatch || w.Decision.LaneID != "lane-2" || w.Decision.Attempt != 2 {
				t.Fatalf("item-a pending after retry pass = %+v, want ActionDispatch on lane-2 attempt 2", w)
			}

			var lane1State string
			var lane1Run *string
			fixtureRead(t, f, func(r *store.Reader) error {
				return r.QueryRow(`SELECT state, run_id FROM lanes WHERE lane_id=?`, "lane-1").Scan(&lane1State, &lane1Run)
			})
			if lane1State != run.LaneStateLeased || lane1Run == nil || *lane1Run != planB.decision.RunRequest.RunID {
				t.Fatalf("lane-1 lease after item-a redispatch = (%q, %v), want leased to %s", lane1State, lane1Run, planB.decision.RunRequest.RunID)
			}
			if head := runGit(t, lane1Worktree, "rev-parse", "HEAD"); head != itemBHead {
				t.Fatalf("lane-1 HEAD = %s, want sibling commit %s untouched", head, itemBHead)
			}
		})
	}
}

// TestWhyNeverReportsRunLiveAfterFinalVerdictWithoutProcess pins c7 and the
// asymmetric examiner observation/reaping bugs found in run-item-request-24525b7846-a1-n2
// and run-item-request-f0d2ae83ac-a2-n1-cfix.
func TestWhyNeverReportsRunLiveAfterFinalVerdictWithoutProcess(t *testing.T) {
	t.Run("ended worker session with live examiner reports gate-replay", func(t *testing.T) {
		f, l := passFixture(t, "item-a", "item-b")
		l.cfg.Lanes = []string{"lane-1"}
		l.sessionID = "ended-worker-session"
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`UPDATE items SET allowed_paths='["candidate.txt"]'`); err != nil {
				return err
			}
			_, err := tx.Exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, ended_at) VALUES (?, 10, 20, 30, ?)`,
				l.sessionID, l.cfg.Now().UnixMilli())
			return err
		})
		p0 := mustPlan(t, l)[0]
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		ok, _, err := l.applyPlan(context.Background(), p0, base, l.cfg.Now())
		if err != nil || !ok {
			t.Fatalf("dispatch: %v/%v", ok, err)
		}
		path := l.lanePath(p0.decision.LaneID)
		writeForkFile(t, filepath.Join(path, "candidate.txt"), "candidate\n")
		runGit(t, path, "add", "candidate.txt")
		runGit(t, path, "commit", "-qm", "candidate")
		writeForkFile(t, filepath.Join(l.laneDirPath(p0.decision.LaneID), FenceFile), "candidate.txt\n")
		c := Candidate{ItemID: "item-a", LaneID: p0.decision.LaneID, RunID: p0.decision.RunRequest.RunID, Worktree: path, BaseSHA: base, HeadSHA: runGit(t, path, "rev-parse", "HEAD")}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			rec := run.Record{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}}
			if err := applyTerminal(tx, rec); err != nil {
				return err
			}
			return appendEffect(tx, candidateReadyKind, c.RunID, c, l.cfg.Now())
		})

		// Switch to a new active drain session to examine the candidate from the ended session.
		l.sessionID = "active-examiner-session"
		steps := &blockingReplayGateSteps{
			gateOutcome: GateOutcome{
				Outcome:       GateFailed,
				FailingChecks: []string{"unit"},
			},
			entered: make(chan string, 2),
			release: make(chan struct{}),
		}
		l.cfg.Steps = steps

		plans := mustPlan(t, l)
		var p plannedApplication
		for _, cand := range plans {
			if cand.decision.ItemID == "item-a" {
				p = cand
				break
			}
		}
		ok, work, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
		if err != nil || !ok {
			t.Fatalf("claim examination: %v / %v", ok, err)
		}
		if work.LoopID != "ended-worker-session" {
			t.Fatalf("work.LoopID = %q, want ended-worker-session", work.LoopID)
		}

		l.launchExamination(context.Background(), p.decision, work, l.cfg.Now(), &PassTiming{}, nil)
		select {
		case <-steps.entered:
		case <-time.After(hangGuard):
			t.Fatal("replay did not start")
		}
		defer func() {
			close(steps.release)
			select {
			case done := <-l.completions:
				delete(l.active, done.id)
			case <-time.After(hangGuard):
			}
		}()

		lanes := l.lanes()
		var why WhyAnswer
		fixtureRead(t, f, func(r *store.Reader) error {
			var err error
			why, err = Why(r, Pass{AvailableLanes: &lanes, StorePath: f.storePath}, l.cfg.Policy, nil)
			return err
		})
		var rowA *WhyRow
		for i := range why.Rows {
			if why.Rows[i].ItemID == "item-a" {
				rowA = &why.Rows[i]
				break
			}
		}
		if rowA == nil {
			t.Fatalf("item-a missing from why rows: %+v", why.Rows)
		}
		if rowA.Reason != ReasonGateReplay || rowA.Position != "candidate" || rowA.Class != "moving" {
			t.Fatalf("why with ended worker session + live examiner = (reason=%q pos=%q class=%q why=%q), want (%q, candidate, moving)",
				rowA.Reason, rowA.Position, rowA.Class, rowA.Why, ReasonGateReplay)
		}
	})

	t.Run("dead examiner on judged run reports gate-replay recovery and reaps cleanly", func(t *testing.T) {
		f, l, c := heldCandidateFixture(t)
		here, err := currentLoopOwner()
		if err != nil {
			t.Fatalf("currentLoopOwner: %v", err)
		}
		deadOwner := &LoopOwner{Domain: here.Domain, UID: here.UID, PID: 99999999, Start: "1"}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if err := appendEffect(tx, "gate.verdict", c.RunID, map[string]any{"outcome": "fail", "failing_checks": []string{"unit"}}, l.cfg.Now()); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL WHERE lane_id = ?`, run.LaneStateIdle, c.LaneID); err != nil {
				return err
			}
			if err := appendEffect(tx, candidateLaneKind, c.RunID, candidateLane{Candidate: c, Released: true}, l.cfg.Now()); err != nil {
				return err
			}
			if err := appendEffect(tx, examinationOwnerKind, c.RunID, deadOwner, l.cfg.Now()); err != nil {
				return err
			}
			return appendEffect(tx, examinationClaimedKind, c.RunID, struct{}{}, l.cfg.Now())
		})

		lanes := l.lanes()
		var why WhyAnswer
		fixtureRead(t, f, func(r *store.Reader) error {
			var err error
			why, err = Why(r, Pass{AvailableLanes: &lanes, StorePath: f.storePath}, l.cfg.Policy, nil)
			return err
		})
		var rowA *WhyRow
		for i := range why.Rows {
			if why.Rows[i].ItemID == "item-a" {
				rowA = &why.Rows[i]
				break
			}
		}
		if rowA == nil {
			t.Fatalf("item-a missing from why rows after dead examiner")
		}
		if rowA.Reason == ReasonRunLive || rowA.Position != "held" || rowA.Reason != ReasonGateReplay {
			t.Fatalf("why with dead examiner = (reason=%q pos=%q class=%q why=%q), want held/%s and never %s",
				rowA.Reason, rowA.Position, rowA.Class, rowA.Why, ReasonGateReplay, ReasonRunLive)
		}

		// Reaping dead loop runs must clear BOTH Examiner and Examining so the candidate
		// returns to ActionJudge rather than staying stuck on examinationClaimedKind.
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return l.reapDeadLoopRuns(context.Background(), tx, l.cfg.Now(), reapEvidence{
				dead: map[LoopOwner]bool{*deadOwner: true},
				live: map[string]bool{},
			})
		})
		got := pendingForTest(t, f, "item-a")
		if got == nil || got.Examiner != nil || got.Examining {
			t.Fatalf("pending after reapDeadLoopRuns = %+v, want Examiner=nil and Examining=false", got)
		}
	})

	t.Run("final verdict without examiner or worker process never reports run-live", func(t *testing.T) {
		f, l, c := heldCandidateFixture(t)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return appendEffect(tx, "gate.verdict", c.RunID, map[string]any{"outcome": "fail", "failing_checks": []string{"unit"}}, l.cfg.Now())
		})
		lanes := l.lanes()
		var why WhyAnswer
		fixtureRead(t, f, func(r *store.Reader) error {
			var err error
			why, err = Why(r, Pass{AvailableLanes: &lanes, StorePath: f.storePath}, l.cfg.Policy, nil)
			return err
		})
		for _, row := range why.Rows {
			if row.ItemID == "item-a" && row.Reason == ReasonRunLive {
				t.Fatalf("item-a with final gate.verdict and no process reported run-live: %+v", row)
			}
		}
	})
}
