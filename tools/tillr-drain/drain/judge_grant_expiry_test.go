package drain

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func persistFixtureJudgeSelection(t *testing.T, f refusalLoopFixture) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return recordWorkflowSelection(tx, Decision{ItemID: "item-a", SelectedWorkflow: singleWorkerWorkflow, WorkflowSource: "implicit"})
	})
}

func TestLegacyJudgeGrantHoldDoesNotStopSavedWork(t *testing.T) {
	f, l, s := judgeRouteFixture(t, "pass")
	l.cfg.Lanes = []string{"lane-1"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at)
VALUES('old-grant-hold','item-a','hold','judge-grant-absent',?)`, l.cfg.Now().UnixMilli())
		return err
	})
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	// Leave the saved worker candidate to the next pass, but replay the old
	// refusal that previously blocked it pending a renewal.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO events(ts,kind,object_type,object_id,payload)
VALUES(?, 'drain.refusal', 'run', 'run-item-a-a1-judge',
'{"step":"judge-grant-absent","item_id":"item-a","writer_run_id":"run-item-a-a1"}')`, l.cfg.Now().UnixMilli())
		return err
	})
	drainOwned(t, l, result)
	at := l.cfg.Now()
	l.cfg.Now = func() time.Time { return at.Add(25 * time.Hour) }
	for range 4 {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if want := []string{"worker", "judge", "gate", "landing"}; !reflect.DeepEqual(s.order, want) {
		t.Fatalf("saved work repeated or stopped: got %v, want %v", s.order, want)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var count int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind='item.verdict' AND json_extract(payload,'$.evaluated_head')=? AND json_type(payload,'$.grant_id') IS NULL`, s.workerHead).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("expected one verdict on the saved candidate, got %d", count)
		}
		if err := r.QueryRow(`SELECT count(*) FROM dispatches WHERE reason='judge-grant-absent'`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("expected only the historical grant hold, got %d", count)
		}
		return nil
	})
}
