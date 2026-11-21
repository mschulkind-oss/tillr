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

func TestFinishExistingLandingChecksReverts(t *testing.T) {
	cases := []struct {
		name, objectType, objectID, item, run, result string
		rewrite, second, mismatch, wantFinish         bool
		unrelatedMalformed                            bool
	}{
		{name: "unreverted", wantFinish: true},
		{name: "reverted", objectType: "landing", objectID: "commit", item: "item_042", run: "run-1", result: "reverted"},
		{name: "rewritten", objectType: "landing", objectID: "new-commit", item: "item_042", run: "run-1", result: "reverted", rewrite: true},
		{name: "unrelated receipt is not read", objectType: "landing", objectID: "commit", item: "item_042", run: "run-1", result: "reverted", unrelatedMalformed: true},
		{name: "not possible", objectType: "landing", objectID: "commit", item: "item_042", run: "run-1", result: "not-possible", wantFinish: true},
		{name: "wrong object", objectType: "item", objectID: "commit", item: "item_042", run: "run-1", result: "reverted", wantFinish: true},
		{name: "mismatched payload", objectType: "landing", objectID: "commit", item: "item_042", run: "run-1", result: "reverted", mismatch: true, wantFinish: true},
		{name: "landing id not commit", objectType: "landing", objectID: "landing-1", item: "item_042", run: "run-1", result: "reverted", wantFinish: true},
		{name: "wrong run", objectType: "landing", objectID: "commit", item: "item_042", run: "run-2", result: "reverted", wantFinish: true},
		{name: "wrong item", objectType: "landing", objectID: "commit", item: "other", run: "run-1", result: "reverted", wantFinish: true},
		{name: "later landing", objectType: "landing", objectID: "commit", item: "item_042", run: "run-1", result: "reverted", second: true, wantFinish: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := filepath.Join(scratch(t, "reverted-finish"), "state.db")
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
			code, out := findingState(t, db, "item_042", "landed", "--finish-existing-landing", "--reason", "covered", "--by", "operator", "--resolves-via", "commit:abcd1234")
			if tc.wantFinish {
				if code != 0 {
					t.Fatalf("finish with unreverted landing: %d %s", code, out)
				}
			} else {
				assertRefused(t, code, out, "landed-receipt-missing")
			}
		})
	}
}
