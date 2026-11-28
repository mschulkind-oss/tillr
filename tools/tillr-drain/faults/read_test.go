package faults

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestReadForItems(t *testing.T) {
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	appendEvent := func(kind, objType, objID string, payload any) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(`INSERT INTO events (ts, kind, object_type, object_id, payload) VALUES (1, ?, ?, ?, ?)`, kind, objType, objID, string(raw))
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	// item-a: active fault held.
	appendEvent(HeldKind, "item", "item-a", Hold{ItemID: "item-a", RunID: "run-a", Reason: "dirty-worktree", Detail: "untracked file"})
	// item-b: out-of-scope active fault held.
	appendEvent(HeldKind, "item", "item-b", Hold{ItemID: "item-b", RunID: "run-b", Reason: "dirty-worktree", Detail: "other item"})
	// item-c: held then cleared.
	appendEvent(HeldKind, "item", "item-c", Hold{ItemID: "item-c", RunID: "run-c", Reason: "dirty-worktree", Detail: "cleared later"})
	appendEvent(ClearedKind, "item", "item-c", Hold{Reason: "dirty-worktree"})
	// item-d: dirty boundary.check conviction referencing item-d and item-b.
	appendEvent("boundary.check", "run", "run-d", map[string]any{
		"clean": false,
		"runs": []map[string]string{
			{"item_id": "item-d", "run_id": "run-d"},
			{"item_id": "item-b", "run_id": "run-b"},
		},
		"first_violation": map[string]string{
			"kind":   "path",
			"target": "/outside",
			"detail": "wrote outside target",
		},
	})

	ro, err := store.OpenRead(ctx, s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		qBefore := store.SQLQueriesIssued()
		empty, err := ReadForItems(r, nil, nil)
		if err != nil {
			return err
		}
		if len(empty) != 0 {
			t.Fatalf("empty ReadForItems = %+v, want empty", empty)
		}
		if got := store.SQLQueriesIssued() - qBefore; got != 0 {
			t.Fatalf("empty ReadForItems issued %d queries, want 0", got)
		}

		got, err := ReadForItems(r, []string{"item-a", "item-c", "item-d"}, []string{"run-a", "run-d"})
		if err != nil {
			return err
		}
		if len(got) != 2 {
			t.Fatalf("ReadForItems returned %d holds, want 2: %+v", len(got), got)
		}
		if got["item-a"].Reason != "dirty-worktree" {
			t.Fatalf("item-a = %+v, want dirty-worktree", got["item-a"])
		}
		if got["item-d"].Reason != "host-boundary-violation" {
			t.Fatalf("item-d = %+v, want host-boundary-violation", got["item-d"])
		}
		if _, ok := got["item-b"]; ok {
			t.Fatalf("unexpected out-of-scope item-b in holds: %+v", got)
		}
		if _, ok := got["item-c"]; ok {
			t.Fatalf("cleared item-c still present in holds: %+v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
