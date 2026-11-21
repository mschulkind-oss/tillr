package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestFaultRecordedAfterPlanningPreventsApplication(t *testing.T) {
	_, l := passFixture(t, "item-a")
	plan := mustPlan(t, l)[0]
	if err := l.holdItemFault(context.Background(), Candidate{ItemID: "item-a"}, ReasonDefinitionSyncFailed, "new finding"); err != nil {
		t.Fatal(err)
	}
	before := eventCount(t, l.cfg.Store)
	ok, work, err := l.applyPlan(context.Background(), plan, runGit(t, l.cfg.Root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || ok || work != nil {
		t.Fatalf("stale plan advanced a held item: applied=%v work=%+v err=%v", ok, work, err)
	}
	assertOnlyDispatchDecline(t, l.cfg.Store, before, plan.decision, "item or lane state changed since planning")
}

func TestClearingOneKindOfFaultDoesNotReleaseAnother(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ctx := context.Background()
	c := Candidate{ItemID: "item-a", RunID: "run-fault", LaneID: "lane-1"}
	if err := l.holdItemFault(ctx, c, ReasonDefinitionSyncFailed, "bad definition"); err != nil {
		t.Fatal(err)
	}
	if err := l.holdItemFault(ctx, c, StopGateHarnessError, "gate unavailable"); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		if faults[c.ItemID].Reason != StopGateHarnessError {
			t.Fatalf("latest fault missing: %+v", faults)
		}
		return ClearItemFault(tx, c.ItemID, faults[c.ItemID].Seq, l.cfg.Now())
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if faults[c.ItemID].Reason != ReasonDefinitionSyncFailed {
			t.Errorf("clear lost independent fault: %+v", faults)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionMeasurementWriteFailureIsAGlobalFault(t *testing.T) {
	f, l := passFixture(t, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER refuse_measurements BEFORE UPDATE ON drain_passes BEGIN SELECT RAISE(ABORT,'fixture store write failure'); END;
   INSERT INTO drain_passes(tick_id,started_at,stage_ms,dispatch_ms,gate_ms,land_ms,gate_budget_ms,gate_over_budget) VALUES('gen-1',1,0,0,0,0,100,0)`)
		return err
	})
	_, err := l.takeCompletion(runCompletion{id: "fixture", timing: &PassTiming{TickID: "gen-1", Gates: 1, GateMS: 1}}, &LoopResult{})
	if err == nil {
		t.Fatal("store write failure was swallowed instead of stopping the loop")
	}
	// The failure is global; it is not attributed to an arbitrary queue item.
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if len(faults) != 0 {
			t.Errorf("global fault held items: %+v", faults)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
