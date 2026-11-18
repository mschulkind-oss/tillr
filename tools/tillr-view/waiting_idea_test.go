package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWaitingIdeaRefusals(t *testing.T) {
	for _, reason := range []string{"words-missing", "words-altered", "idea-no-outcome"} {
		t.Run(reason, func(t *testing.T) {
			h := newServeHarness(t)
			h.exec(`DELETE FROM dispatches`)
			h.exec(`DELETE FROM candidates`)
			h.exec(`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES('idea-hold','item-seed-02','hold',?,2000)`, reason)
			h.exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES('idea-run','item-seed-02','lane-a','pi','finished',1000,1500,'[]',1)`)
			h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(1500,'drain.refusal','run','idea-run',?)`, fmt.Sprintf(`{"item_id":"item-seed-02","step":%q,"detail":"docs/ideas/words.txt"}`, reason))
			s, err := store.OpenRead(context.Background(), h.store)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			err = s.ReadTx(context.Background(), func(r *store.Reader) error {
				rows, err := assembleWaiting(r, nil, h.store)
				if err != nil {
					return err
				}
				for _, row := range rows {
					if row.ItemID == "item-seed-02" {
						if row.Class != "needs a cause" || row.Where != "fence" || row.Why != reason+": docs/ideas/words.txt" || row.ClassSource != "rule" {
							t.Fatalf("unclassified: %+v", row)
						}
						return nil
					}
				}
				t.Fatal("missing waiting row")
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
