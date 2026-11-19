package drain

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

// A test lander that reports success must leave the receipt the real lander writes.
func fixtureLanding(t *testing.T, path string, c Candidate) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := appendEffect(tx, land.LandingKind, c.RunID, map[string]string{"item_id": c.ItemID, "result": "landed"}, time.Now()); err != nil {
			return err
		}
		var seq int64
		if err := tx.QueryRow(`SELECT MAX(seq) FROM events`).Scan(&seq); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES(?,?,?,'fixture','landed',?,?,?, ?,?)`, "fixture-"+c.RunID, c.ItemID, c.RunID, c.BaseSHA, c.HeadSHA, c.BaseSHA, c.HeadSHA, seq)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestResolveElsewhereUsesCurrentEvidence(t *testing.T) {
	for _, landed := range []bool{false, true} {
		t.Run(map[bool]string{false: "pointer", true: "landing-wins"}[landed], func(t *testing.T) {
			f, l, _ := workflowLoopFixture(t, "")
			c := Candidate{ItemID: "item-a", RunID: "run-evidence", BaseSHA: "abc", HeadSHA: "abcdef"}
			if landed {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE item_criteria SET state='done' WHERE item_id='item-a'`)
					return err
				})
				fixtureLanding(t, f.storePath, c)
			}
			if err := l.resolveElsewhere(context.Background(), c); err != nil {
				t.Fatal(err)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var state, pointer, reason, actor string
				if err := r.QueryRow(`SELECT state,resolves_via,lifecycle_reason,lifecycle_by FROM items WHERE item_id=?`, c.ItemID).Scan(&state, &pointer, &reason, &actor); err != nil {
					return err
				}
				if landed {
					if state != StateLanded {
						t.Fatalf("state=%s", state)
					}
				} else if state != StateResolvedElsewhere || pointer != "commit:"+c.HeadSHA || reason == "" || actor != Utility {
					t.Fatalf("resolution %s %s %s %s", state, pointer, reason, actor)
				}
				return nil
			})
		})
	}
}

func TestNoCommitGraphUsesItemStateRules(t *testing.T) {
	f, l, steps := exitFixture(t, fmt.Sprintf(exitOne, "no_commit"), false, 0)
	steps.realState = true
	result := &LoopResult{}
	for i := 0; i < 4; i++ {
		graphPass(t, l, result)
	}
	if got := itemStateOf(t, f.storePath, "item-a"); got != StateLanded {
		t.Fatalf("state=%s", got)
	}
}
