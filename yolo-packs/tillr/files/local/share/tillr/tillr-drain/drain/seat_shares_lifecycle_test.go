package drain

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestSeatSharesWorkflowRestartKeepsRecordedBackend(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	backend := l.cfg.Policy.Backends["fixture"]
	backend.Model = "other-model"
	l.cfg.Policy.Backends["other"] = backend
	l.cfg.Policy.SeatLists = map[string][]string{"build": {"fixture", "other"}}
	l.cfg.Policy.SeatShares = map[string][]float64{"build": {1, 1}}
	l.cfg.Policy.SeatPicks = map[string]string{"build": "random"}
	l.cfg.Policy.seatDraw = func() float64 { return .99 }
	p := mustPlan(t, l)[0]
	deadOwnerClaim(t, f, l, p)
	original := pendingForTest(t, f, "item-a").Decision.RunRequest
	if original.Backend != "other" {
		t.Fatalf("original draw %s", original.Backend)
	}
	// A restart keeps the recorded backend (never re-drawing from seat shares),
	// while building its command from the policy model now in force.
	backend.Model = "changed-model"
	l.cfg.Policy.Backends["other"] = backend
	l.cfg.Policy.seatDraw = func() float64 { return 0 }
	next := replaceGraphLoop(t, f, l, steps)
	restart := mustPlan(t, next)[0].decision
	if !restart.WorkflowRestart || restart.RunRequest.Backend != original.Backend || restart.RunRequest.Route() != "changed-model" {
		t.Fatalf("restart backend/route mismatch: %+v, original %+v", restart.RunRequest, original)
	}
}

func TestSeatSharesCancelledStagingRetractsAllUnstartedChoices(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := twoBackends()
	p.Seats = map[string]string{"build": "first"}
	p.SeatLists = map[string][]string{"build": {"first", "second"}}
	p.SeatShares = map[string][]float64{"build": {1, 1}}
	p.SeatPicks = map[string]string{"build": "random"}
	p.seatDraw = func() float64 { return .99 }
	l.cfg.Policy = &p
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE items SET seat='build'`); return err })
	plan := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, work, err := l.applyPlan(context.Background(), plan, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v/%v", ok, err)
	}
	d := work.Decision
	if d.RunRequest.Backend != "second" {
		t.Fatal("fixture must choose second")
	}
	sibling := d
	sibling.RunRequest = p.request(d.ItemID, d.LaneID, "parallel-unstarted", "second")
	d.Parallel = []Decision{sibling}
	fixtureWrite(t, f, func(tx *store.Tx) error { return recordSeatDispatch(tx, sibling) })
	if err := l.cancelStaging(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM seat_dispatches`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("cancel retained %d unstarted choices", n)
		}
		return nil
	})
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO backend_windows(backend,kind,fraction,status,reset_at,run_id) VALUES('second','quota',1,'rejected',?,'refused')`, l.cfg.Now().Add(time.Hour).UnixMilli())
		return err
	})
	p.seatDraw = func() float64 { return 0 } // The cancelled draw must not be reused.
	plan = mustPlan(t, l)[0]
	ok, work, err = l.applyPlan(context.Background(), plan, base, l.cfg.Now())
	if err != nil || !ok || work.Decision.RunRequest.Backend != "first" {
		t.Fatalf("reclaim reused closed backend: %v/%v/%+v", ok, err, work)
	}
}

func TestSeatSharesParallelRestartWaitsForClosedSibling(t *testing.T) {
	f, l, steps := parallelFixture(t)
	l.cfg.Policy.Backends["other"] = l.cfg.Policy.Backends["fixture"]
	l.cfg.Policy.SeatLists = map[string][]string{"scout": {"fixture", "other"}}
	l.cfg.Policy.SeatShares = map[string][]float64{"scout": {1, 1}}
	l.cfg.Policy.SeatPicks = map[string]string{"scout": "round-robin"}
	steps.interrupt = true
	steps.writerAfter = 6
	result := &LoopResult{}
	graphPass(t, l, result)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := appendEffect(tx, loopOrphanedKind, "run-item-a-a1-n1", struct{}{}, l.cfg.Now()); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO backend_windows(backend,kind,fraction,status,reset_at,run_id) VALUES('other','quota',1,'rejected',?,'refused')`, l.cfg.Now().Add(time.Minute).UnixMilli())
		return err
	})
	p := mustPlan(t, l)[0]
	if p.decision.Action != ActionHold || p.decision.Reason != ReasonBackendWindow {
		t.Fatalf("closed sibling not held: %+v", p.decision)
	}
	graphPass(t, l, result)
	if steps.started != 4 {
		t.Fatalf("closed sibling restarted: %d starts", steps.started)
	}
	at := l.cfg.Now()
	l.cfg.Now = func() time.Time { return at.Add(2 * time.Minute) }
	for i := 0; i < 3; i++ {
		graphPass(t, l, result)
	}
	if steps.started != 6 || steps.builders != 1 {
		t.Fatalf("reset lost finished readers: starts=%d builders=%d", steps.started, steps.builders)
	}
}

func TestSeatSharesReviewClosureRetainsCandidateUntilReset(t *testing.T) {
	f, l, s, c := prepareJudgeCandidate(t, "pass")
	result := &LoopResult{}
	// This is the implicit-review path, not a workflow evaluator node.
	d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, RunRequest: c.Request, Action: ActionDispatch, Reason: ReasonReady, Attempt: 1, From: "queued", To: "running"}
	identifyDecision(&d)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := appendEffect(tx, dispatchStartedKind, c.RunID, &pendingWork{Decision: d, BaseSHA: c.BaseSHA}, l.cfg.Now()); err != nil {
			return err
		}
		return appendEffect(tx, candidateReadyKind, c.RunID, c, l.cfg.Now())
	})
	work := pendingForTest(t, f, "item-a")
	if work.Candidate == nil {
		t.Fatal("fixture needs a worker candidate")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO backend_windows(backend,kind,fraction,status,reset_at,run_id) VALUES('reviewer','reserve',0.8,'rejected',?,'reserve-reading')`, l.cfg.Now().Add(time.Minute).UnixMilli())
		return err
	})
	t.Logf("review policy=%v candidate evaluator=%+v judge=%+v", l.cfg.Policy.seatList("review"), work.Candidate.Evaluator, work.Candidate.Judge)
	// If closure arrives after planning, the judge must signal a temporary hold,
	// never write a terminal refusal that would retire the worker's candidate.
	_, accepted, err := l.judgeCandidate(context.Background(), *work.Candidate, result)
	held, keep := retainLane(err)
	if accepted || !held || !keep {
		t.Fatalf("closed review not retained: accepted=%v err=%v held=%v keep=%v", accepted, err, held, keep)
	}
	plan := mustPlan(t, l)[0].decision
	if plan.Action != ActionHold || plan.Reason != ReasonBackendWindow {
		t.Fatalf("window wait: %+v", plan)
	}
	graphPass(t, l, result)
	if pendingForTest(t, f, "item-a").Done || !reflect.DeepEqual(s.order, []string{"worker"}) {
		t.Fatalf("closed window discarded candidate or ran judge: %v", s.order)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, RefusalKind).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("quota wait wrote %d terminal refusals", n)
		}
		return nil
	})
	at := l.cfg.Now()
	l.cfg.Now = func() time.Time { return at.Add(2 * time.Minute) }
	for i := 0; i < 3 && result.Landed == 0; i++ {
		graphPass(t, l, result)
	}
	if result.Landed != 1 || !reflect.DeepEqual(s.order, []string{"worker", "judge", "gate", "landing"}) {
		t.Fatalf("reset rebuilt or lost candidate: %+v %v", result, s.order)
	}
}
