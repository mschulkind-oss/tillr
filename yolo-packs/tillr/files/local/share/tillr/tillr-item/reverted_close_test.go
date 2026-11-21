package main_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestCloseAfterLandingRevert(t *testing.T) {
	cases := []struct {
		name, objectType, objectID, item, run, result string
		rewrite, second, claim, mismatch, wantClose   bool
		unrelatedMalformed                            bool
	}{
		{name: "unreverted", wantClose: false},
		{name: "reverted", objectType: "landing", objectID: "commit", item: "item_042", run: "run-1", result: "reverted", wantClose: true},
		{name: "rewritten", objectType: "landing", objectID: "new-commit", item: "item_042", run: "run-1", result: "reverted", rewrite: true, wantClose: true},
		{name: "unrelated receipt is not read", objectType: "landing", objectID: "commit", item: "item_042", run: "run-1", result: "reverted", unrelatedMalformed: true, wantClose: true},
		{name: "reverted but publishing", objectType: "landing", objectID: "commit", item: "item_042", run: "run-1", result: "reverted", claim: true},
		{name: "not possible", objectType: "landing", objectID: "commit", item: "item_042", run: "run-1", result: "not-possible"},
		{name: "wrong object", objectType: "item", objectID: "commit", item: "item_042", run: "run-1", result: "reverted"},
		{name: "mismatched payload", objectType: "landing", objectID: "commit", item: "item_042", run: "run-1", result: "reverted", mismatch: true},
		{name: "landing id not commit", objectType: "landing", objectID: "landing-1", item: "item_042", run: "run-1", result: "reverted"},
		{name: "wrong run", objectType: "landing", objectID: "commit", item: "item_042", run: "run-2", result: "reverted"},
		{name: "wrong item", objectType: "landing", objectID: "commit", item: "other", run: "run-1", result: "reverted"},
		{name: "later landing", objectType: "landing", objectID: "commit", item: "item_042", run: "run-1", result: "reverted", second: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := filepath.Join(scratch(t, "reverted-close"), "state.db")
			if code, out := apply(t, db, envelope(t, "item.record", "rec", base("item_042", "an item"))); code != 0 {
				t.Fatalf("apply: %d %s", code, out)
			}
			ctx := context.Background()
			_ = event.Register("drain.revert", "tillr-drain")
			s, err := store.Open(ctx, db, false)
			if err != nil {
				t.Fatal(err)
			}
			err = s.WriteTx(ctx, func(tx *store.Tx) error {
				if _, err := tx.Exec(`CREATE TABLE landings (landing_id TEXT, item_id TEXT, run_id TEXT, result TEXT, trunk_after TEXT)`); err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT INTO landings VALUES ('landing-1','item_042','run-1','landed','commit')`); err != nil {
					return err
				}
				if tc.second {
					if _, err := tx.Exec(`INSERT INTO landings VALUES ('landing-2','item_042','run-2','landed','commit-2')`); err != nil {
						return err
					}
				}
				if tc.rewrite {
					if _, err := tx.Exec(`CREATE TABLE trunk_rewrites (old_sha TEXT, new_sha TEXT)`); err != nil {
						return err
					}
					if _, err := tx.Exec(`INSERT INTO trunk_rewrites VALUES ('commit','intermediate'),('intermediate','new-commit')`); err != nil {
						return err
					}
				}
				if tc.claim {
					if _, err := tx.Exec(`CREATE TABLE landing_claims (item_id TEXT, run_id TEXT)`); err != nil {
						return err
					}
					if _, err := tx.Exec(`INSERT INTO landing_claims VALUES ('item_042','run-2')`); err != nil {
						return err
					}
				}
				if tc.unrelatedMalformed {
					other := "unrelated-commit"
					if _, err := tx.Append(event.Event{Kind: "drain.revert", ObjectType: "landing", ObjectID: &other, Payload: json.RawMessage(`"not a receipt"`), At: time.Now()}); err != nil {
						return err
					}
				}
				if tc.objectType != "" {
					landing := tc.objectID
					if tc.mismatch {
						landing = "other-commit"
					}
					body, _ := json.Marshal(map[string]string{"landing": landing, "item_id": tc.item, "run_id": tc.run, "result": tc.result})
					_, err := tx.Append(event.Event{Kind: "drain.revert", ObjectType: tc.objectType, ObjectID: &tc.objectID, Payload: body, At: time.Now()})
					return err
				}
				return nil
			})
			s.Close()
			if err != nil {
				t.Fatal(err)
			}
			code, out := lifecycle(t, db, "close", "not wanted")
			if tc.wantClose && code != 0 {
				t.Fatalf("close after revert: %d %s", code, out)
			}
			if !tc.wantClose {
				rule := "already-landed"
				if tc.claim {
					rule = "landing-in-progress"
				}
				if code != 1 || decode(t, out).Payload.Rule != rule {
					t.Fatalf("close must refuse with %s: %d %s", rule, code, out)
				}
			}
		})
	}
}
