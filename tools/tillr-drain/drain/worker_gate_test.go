package drain

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
		"plan_worker_settlement",
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
