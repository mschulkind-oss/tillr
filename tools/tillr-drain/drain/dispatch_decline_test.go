package drain

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestEachApplicationOfTheSameStaleDispatchPlanRecordsADecline(t *testing.T) {
	f, l := passFixture(t, "item-a")
	plan := mustPlan(t, l)[0]
	if !isDispatch(plan.decision) || plan.decision.RunRequest == nil {
		t.Fatalf("fixture did not plan a dispatch: %+v", plan.decision)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, StateBlocked, plan.decision.ItemID)
		return err
	})
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	for application := 1; application <= 2; application++ {
		before := eventCount(t, f.storePath)
		ok, work, err := l.applyPlan(context.Background(), plan, base, l.cfg.Now())
		if err != nil || ok || work != nil {
			t.Fatalf("stale application %d: applied=%v, work=%+v, error=%v", application, ok, work, err)
		}
		assertOnlyDispatchDecline(t, f.storePath, before, plan.decision, "item or lane state changed since planning")
	}
}

func TestDispatchApplicationErrorLogsAndRecordsADeclineAndReturnsTheError(t *testing.T) {
	f, l := passFixture(t, "item-a")
	var report bytes.Buffer
	l.cfg.Report = &report
	plan := mustPlan(t, l)[0]
	if !isDispatch(plan.decision) || plan.decision.RunRequest == nil {
		t.Fatalf("fixture did not plan a dispatch: %+v", plan.decision)
	}
	const failure = "fixture dispatch insert refused"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER refuse_fixture_dispatch BEFORE INSERT ON dispatches
			BEGIN SELECT RAISE(ABORT, 'fixture dispatch insert refused'); END`)
		return err
	})
	before := eventCount(t, f.storePath)
	ok, _, err := l.applyPlan(context.Background(), plan, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err == nil || !strings.Contains(err.Error(), failure) {
		t.Fatalf("application error = %v, want original failure %q", err, failure)
	}
	if ok {
		t.Fatal("failed dispatch applied")
	}
	log := report.String()
	if strings.Count(log, "declined dispatch of "+plan.decision.ItemID+" on "+plan.decision.LaneID) != 1 || !strings.Contains(log, failure) {
		t.Fatalf("report = %q, want one declined dispatch naming the item, lane, and failure", log)
	}
	assertOnlyDispatchDecline(t, f.storePath, before, plan.decision, err.Error())

	// A claim error is observable by watch but does not put the item in a wait in Why.
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		declines, err := readDispatchDeclines(r)
		if _, ok := declines[plan.decision.ItemID]; ok {
			t.Error("Why treated the application error as a claim wait")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBusyDispatchDeclineDoesNotFailThePass(t *testing.T) {
	f, l := passFixture(t, "item-a")
	var report bytes.Buffer
	l.cfg.Report = &report
	plan := mustPlan(t, l)[0]
	if !isDispatch(plan.decision) || plan.decision.RunRequest == nil {
		t.Fatalf("fixture did not plan a dispatch: %+v", plan.decision)
	}
	ctx := context.Background()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	at := l.cfg.Now()
	timing := &PassTiming{}
	result := &LoopResult{}
	before := eventCount(t, f.storePath)

	// Hold the writer on a separate connection across both the claim and its
	// decline write. Nested writes bypass the in-process queue, so both see
	// the same SQLite lock contention the loop sees from another process.
	fixtureWrite(t, f, func(*store.Tx) error {
		cancelled, stale, err := l.applyReadyPlan(ctx, plan, &base, at, timing, nil, result)
		if err != nil || cancelled || !stale {
			t.Fatalf("busy decline escaped the pass as cancelled=%v, stale=%v, error=%v; report:\n%s", cancelled, stale, err, &report)
		}
		return nil
	})
	log := report.String()
	if !strings.Contains(log, "declined dispatch of "+plan.decision.ItemID+" on "+plan.decision.LaneID+": \"store: beginning the write transaction:") ||
		!strings.Contains(log, "dispatch decline for "+plan.decision.ItemID+" on "+plan.decision.LaneID+" was not recorded, the store was busy:") {
		t.Fatalf("expected both the busy dispatch refusal and the lost observation in the report, got:\n%s", log)
	}
	if after := eventCount(t, f.storePath); after != before || timing.TickID != "" || timing.Applied != 0 || result.StopStep != "" {
		t.Fatalf("busy decline changed the store or the pass: events %d -> %d, timing %+v, result %+v", before, after, timing, result)
	}
	if ok, _, err := l.applyPlan(ctx, mustPlan(t, l)[0], base, at); !ok || err != nil {
		t.Fatalf("the next plan could not claim the dispatch after the writer released its lock: applied=%v, error=%v", ok, err)
	}
}

func TestWatchRuleDocumentAcceptsDispatchDeclined(t *testing.T) {
	const document = `[[rule]]
id = "fixture-dispatch-declined"
scope = "core"
severity = "warning"
[rule.reads]
events = ["drain.dispatch-declined"]
[rule.when]
group_by = "object_id"
pattern = "count_in_window"
threshold = 1
window = "90s"
[rule.guidance]
summary = "A dispatch was declined"
check = ["Read the decline reason"]
do = ["Check the item and lane state"]
[rule.delivery]
receiver = "foreground"
`
	rules, err := watch.LoadRuleDocument("fixture-dispatch-declined.toml", []byte(document))
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || len(rules[0].Reads.Events) != 1 || rules[0].Reads.Events[0] != "drain.dispatch-declined" {
		t.Fatalf("loaded rules = %+v, want one rule reading drain.dispatch-declined", rules)
	}
}

func TestSeatDispatchConflictDeclineHoldsOnlyItsItemAndLoopKeepsPassing(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	var report bytes.Buffer
	l.cfg.Report = &report

	p := Policy{
		DefaultBackend: "pi",
		Seats:          map[string]string{"build": "pi"},
		Backends: map[string]Backend{
			"pi": {Adapter: "pi", Model: "model-pi", TimeoutSeconds: 60},
		},
	}
	l.cfg.Policy = &p
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat = 'build'`)
		return err
	})

	planA := decisionFor(t, l, "item-a")
	if !isDispatch(planA.decision) || planA.decision.RunRequest == nil {
		t.Fatalf("fixture did not plan dispatch for item-a: %+v", planA.decision)
	}
	conflictRunID := planA.decision.RunRequest.RunID

	// Record a conflicting seat_dispatches row for item-a's run id on an earlier
	// backend with no recorded backend move so dispatch declines on the mismatch.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO seat_dispatches(run_id, seat, backend) VALUES(?, 'build', 'claude')`, conflictRunID)
		return err
	})

	steps := &policyRunSteps{requests: make(chan run.Request, 4), release: make(chan struct{})}
	l.cfg.Steps = steps

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		close(steps.release)
		for len(l.active) > 0 {
			c := <-l.completions
			delete(l.active, c.id)
		}
	}()

	var result LoopResult
	if _, err := l.pass(ctx, &result); err != nil {
		t.Fatalf("pass exited on item-a seat_dispatches conflict: %v; report:\n%s", err, report.String())
	}

	// item-b must still dispatch in the same pass.
	select {
	case req := <-steps.requests:
		if req.ItemID != "item-b" || req.Backend != "pi" {
			t.Fatalf("dispatched = %+v, want item-b on pi", req)
		}
	case <-time.After(hangGuard):
		t.Fatalf("item-b did not dispatch while item-a was held; report:\n%s", report.String())
	}

	// item-a is held with the conflict cause; item-b has no fault.
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		fa, ok := faults["item-a"]
		if !ok {
			t.Fatalf("item-a not held after seat_dispatches conflict; faults=%+v", faults)
		}
		if fa.Reason != ReasonDecisionCollision || !strings.Contains(fa.Detail, "already records seat build backend claude") {
			t.Errorf("item-a fault = %+v, want reason=%s mentioning already records seat build backend claude", fa, ReasonDecisionCollision)
		}
		if fb, ok := faults["item-b"]; ok {
			t.Errorf("item-b unexpectedly held: %+v", fb)
		}
		return nil
	})
	if held := planFor(t, l, "item-a"); held.Action != ActionHold || held.Reason != ReasonDecisionCollision {
		t.Fatalf("next plan for item-a = %+v, want ActionHold/%s", held, ReasonDecisionCollision)
	}

	// A subsequent pass keeps running cleanly without exiting.
	if _, err := l.pass(ctx, &result); err != nil {
		t.Fatalf("subsequent pass failed: %v", err)
	}
}
