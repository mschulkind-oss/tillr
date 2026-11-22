package drain

import (
	"bytes"
	"context"
	"strings"
	"testing"

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
