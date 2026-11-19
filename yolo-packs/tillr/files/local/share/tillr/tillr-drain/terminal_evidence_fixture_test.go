package main_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// fixtureTerminalEvidence seeds the evidence for a command-line loop whose shell
// lander and verdict stubs cannot write it themselves.
func fixtureTerminalEvidence(t *testing.T, path, id string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO item_criteria(item_id,criterion_id,text,state) SELECT ?,'fixture','fixture criterion','done' WHERE NOT EXISTS(SELECT 1 FROM item_criteria WHERE item_id=?)`, id, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE item_criteria SET state='done' WHERE item_id=?`, id); err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]string{"item_id": id, "result": "landed"})
		seq, err := tx.Append(event.Event{Kind: land.LandingKind, ObjectType: "landing", ObjectID: &id, Payload: body, At: time.Now()})
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES(?,?,'fixture','fixture','landed','b','h','b','h',?)`, "fixture-"+id, id, seq)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The process fixtures substitute successful shell stubs for the lander and
// verdict writer. Model their store effects as well as their exit status.
type evidenceProcessSteps struct{ *drain.ProcessSteps }

func (s *evidenceProcessSteps) Land(ctx context.Context, c drain.Candidate) (drain.LandOutcome, error) {
	out, err := s.ProcessSteps.Land(ctx, c)
	if err != nil || out.Result != drain.LandLanded {
		return out, err
	}
	st, err := store.Open(ctx, s.Store, true)
	if err != nil {
		return out, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error {
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM landings WHERE run_id=? AND result='landed')`, c.RunID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		body, _ := json.Marshal(map[string]string{"item_id": c.ItemID, "result": "landed"})
		seq, err := tx.Append(event.Event{Kind: land.LandingKind, ObjectType: "landing", ObjectID: &c.RunID, Payload: body, At: time.Now()})
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES(?,?,?,'fixture','landed',?,?,?,?,?)`, "fixture-"+c.RunID, c.ItemID, c.RunID, c.BaseSHA, c.HeadSHA, c.BaseSHA, c.HeadSHA, seq)
		return err
	})
	return out, err
}
func (s *evidenceProcessSteps) Verdict(ctx context.Context, c drain.Candidate, verdict string) error {
	if err := s.ProcessSteps.Verdict(ctx, c, verdict); err != nil {
		return err
	}
	if verdict != drain.VerdictPassed {
		return nil
	}
	st, err := store.Open(ctx, s.Store, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO item_criteria(item_id,criterion_id,text,state) SELECT ?,'fixture','fixture criterion','done' WHERE NOT EXISTS(SELECT 1 FROM item_criteria WHERE item_id=?)`, c.ItemID, c.ItemID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE item_criteria SET state='done' WHERE item_id=?`, c.ItemID)
		return err
	})
}
