package drain

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWhyChangesWithPlannerHoldRule(t *testing.T) {
	f, l := passFixture(t, "blocker", "dependent")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='custom-done' WHERE item_id='blocker'; INSERT INTO item_edges(item_id,blocked_by) VALUES ('dependent','blocker'); INSERT INTO lanes(lane_id,state) VALUES ('lane-1','idle')`)
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	read := func(want string) {
		t.Helper()
		err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
			if err != nil {
				return err
			}
			envs, err := Plan(r, Pass{}, l.cfg.Policy, nil)
			if err != nil {
				return err
			}
			var plannerDecision Decision
			if err := json.Unmarshal(envs[1].Payload, &plannerDecision); err != nil {
				return err
			}
			for _, row := range answer.Rows {
				if row.ItemID == "dependent" {
					if row.Reason != want || row.Reason != plannerDecision.Reason {
						t.Fatalf("why %q; planner %q; want %q", row.Reason, plannerDecision.Reason, want)
					}
					return nil
				}
			}
			t.Fatal("dependent missing")
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	read(ReasonBlocked)
	// Change the planner's rule for which states satisfy an edge. No why rule changes.
	d, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	d.States = append(d.States, "custom-done")
	d.Finished = append(d.Finished, "custom-done")
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := definition.Set(context.Background(), writer, raw); err != nil {
		t.Fatal(err)
	}
	read(ReasonReady)
}

func TestWhyConflictContextDoesNotReplaceCurrentMover(t *testing.T) {
	f, l := passFixture(t, "conflicted")
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='blocked'; INSERT INTO lanes(lane_id,state,run_id,leased_until) VALUES ('lane-1','leased','busy-run',NULL)`); err != nil {
			return err
		}
		return item.RecordConflict(tx, item.Conflict{ConflictID: "conflict-a", ItemID: "conflicted", RunID: "blocked-run", BodyRef: "docs/a.md", Text: "requires design", Status: "raised", WaitingOn: "design", BlockedCriteria: []string{}}, l.cfg.Now())
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	for _, test := range []struct {
		policy        *Policy
		reason, mover string
	}{{l.cfg.Policy, ReasonNoLane, "lane-1"}, {nil, ReasonNoPolicy, "supply the loop's --policy file"}} {
		err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			answer, err := Why(r, Pass{}, test.policy, nil)
			if err != nil {
				return err
			}
			row := answer.Rows[0]
			if row.Reason != test.reason || row.Mover != test.mover || row.Decision.ConflictID != "conflict-a" {
				t.Fatalf("current cause lost behind ancillary conflict: %+v, want %s / %s", row, test.reason, test.mover)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestWhyAccountsForBusyLanesAndPendingRun(t *testing.T) {
	f, l := passFixture(t, "running", "queued", "held")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='blocked' WHERE item_id='held'; INSERT INTO lanes(lane_id,state,run_id,leased_until) VALUES ('lane-1','leased','live-run',NULL),('lane-2','idle',NULL,NULL)`)
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	// A pending dispatch is the same durable fact that Plan advances on its next pass.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		d := Decision{ItemID: "running", LaneID: "lane-1", Action: ActionDispatch, Reason: ReasonReady, Attempt: 1, From: "queued", To: "running", RunRequest: &run.Request{}}
		d.RunRequest.ItemID, d.RunRequest.RunID, d.RunRequest.LaneID = "running", "live-run", "lane-1"
		identifyDecision(&d)
		raw, err := json.Marshal(pendingWork{Decision: d})
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (0,?,'run','live-run',?)`, dispatchStartedKind, string(raw))
		return err
	})
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		total := answer.Summary.Running + answer.Summary.CouldRun
		for _, n := range answer.Summary.Held {
			total += n
		}
		if total != len(answer.Rows) || answer.Summary.LanesBusy != 1 || answer.Summary.LanesOffered != 2 || answer.Summary.Running != 1 {
			t.Fatalf("answer: %+v", answer)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWhyCountsPlacesWithoutExposingKeys(t *testing.T) {
	f, l := passFixture(t, "first", "a", "z", "parked", "done")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET rank=''; UPDATE items SET rank='secretkey' WHERE item_id='first'; UPDATE items SET state='not-started' WHERE item_id='parked'; UPDATE items SET state='landed' WHERE item_id='done'")
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			raw, err := json.Marshal(row)
			if err != nil {
				return err
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				return err
			}
			if _, ok := fields["rank"]; ok {
				t.Fatalf("key leaked: %s", raw)
			}
			want := map[string]any{"first": float64(1), "a": float64(2), "z": float64(3)}[row.ItemID]
			if fields["place"] != want {
				t.Fatalf("%s place %v want %v", row.ItemID, fields["place"], want)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWhyPlacesFollowKeysNotPlannerPromotions(t *testing.T) {
	f, l := passFixture(t, "ordinary", "fixer-main-test", "rewrite-item-test")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET rank=CASE item_id WHEN 'ordinary' THEN 'a' WHEN 'fixer-main-test' THEN 'z' ELSE 'zz' END")
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			want := map[string]int{"ordinary": 1, "fixer-main-test": 2, "rewrite-item-test": 3}[row.ItemID]
			if row.Place != want {
				t.Fatalf("%s place %d want %d", row.ItemID, row.Place, want)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
