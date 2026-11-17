package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func TestExternalDeskLeaseIsNotQueueDrained(t *testing.T) {
	f, l := passFixture(t, "item-comment")
	l.cfg.Comments = &settings.Comments{Paths: []string{"docs/design/"}, MaxRunning: 3}
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	setDesignFence(t, f, "item-comment", "docs/design/example.md")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := run.ClaimLease(context.Background(), tx, run.Request{RunID: "external-run", ItemID: "external-item", Backend: "fixture"}, "desk-example", l.cfg.Now())
		return err
	})
	plans, leases, err := l.passPlans(context.Background(), l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	stop, err := l.planNothing(context.Background(), []Decision{plans[0].decision}, leases, l.cfg.Now(), &LoopResult{})
	if err != nil || stop != StopLanesLeased {
		t.Fatalf("desk wait stop=%q leases=%v err=%v", stop, leases, err)
	}
}

func TestDeskCapacityRecheckedAtApplication(t *testing.T) {
	for _, kind := range []string{"lease", "pending candidate"} {
		t.Run(kind, func(t *testing.T) {
			f, l := passFixture(t, "item-write")
			l.cfg.Comments = &settings.Comments{Paths: []string{"docs/design/"}, MaxRunning: 1}
			l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
			setDesignFence(t, f, "item-write", "docs/design/example.md")
			p := mustPlan(t, l)[0]
			req := run.Request{RunID: "external-run", ItemID: "external-item", LaneID: "desk-other", Backend: "fixture"}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if kind == "lease" {
					_, err := run.ClaimLease(context.Background(), tx, req, req.LaneID, l.cfg.Now())
					return err
				}
				return appendEffect(tx, dispatchStartedKind, req.RunID, &pendingWork{Decision: Decision{ItemID: req.ItemID, LaneID: req.LaneID, RunRequest: &req}, BaseSHA: runGit(t, f.root, "rev-parse", "HEAD")}, l.cfg.Now())
			})
			before := eventCount(t, f.storePath)
			applied, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", "HEAD"), l.cfg.Now())
			if err != nil || applied || eventCount(t, f.storePath) != before {
				t.Fatalf("stale cap plan applied=%v err=%v events before=%d after=%d", applied, err, before, eventCount(t, f.storePath))
			}
		})
	}
}
