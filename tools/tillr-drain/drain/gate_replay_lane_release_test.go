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

			// Planning now must hold item-a as run-live (not assign a duplicate lane)
			// and immediately offer lane-1 to waiting item-b.
			nextPlans := mustPlan(t, l)
			byItem := map[string]Decision{}
			for _, np := range nextPlans {
				byItem[np.decision.ItemID] = np.decision
			}
			if da := byItem["item-a"]; da.Action != ActionHold || da.Reason != ReasonRunLive {
				t.Fatalf("item-a plan during replay = %+v, want hold/%s", da, ReasonRunLive)
			}
			if db := byItem["item-b"]; db.Action != ActionDispatch || db.LaneID != "lane-1" {
				t.Fatalf("item-b plan during replay = %+v, want dispatch on lane-1", db)
			}

			// Why must show item-a's lane released, never "candidate awaits pin verification and lane release".
			var why WhyAnswer
			lanes := l.lanes()
			fixtureRead(t, f, func(r *store.Reader) error {
				var err error
				why, err = Why(r, Pass{AvailableLanes: &lanes}, l.cfg.Policy, nil)
				return err
			})
			for _, row := range why.Rows {
				if row.ItemID == "item-a" {
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
