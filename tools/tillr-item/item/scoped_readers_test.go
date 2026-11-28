package item

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestReadConflictsForItems(t *testing.T) {
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		appendConflict := func(c Conflict) error {
			raw, err := json.Marshal(c)
			if err != nil {
				return err
			}
			id := c.ItemID
			_, err = tx.Append(event.Event{
				Kind:       ConflictKind,
				ObjectType: "item",
				ObjectID:   &id,
				Payload:    raw,
			})
			return err
		}
		if err := appendConflict(Conflict{ConflictID: "c1", ItemID: "item-a", Status: "raised", WaitingOn: "owner", Text: "first"}); err != nil {
			return err
		}
		if err := appendConflict(Conflict{ConflictID: "c1", ItemID: "item-a", Status: "answered", WaitingOn: "owner", Text: "resolved"}); err != nil {
			return err
		}
		if err := appendConflict(Conflict{ConflictID: "c2", ItemID: "item-b", Status: "raised", WaitingOn: "owner", Text: "other"}); err != nil {
			return err
		}
		if err := appendConflict(Conflict{ConflictID: "c3", ItemID: "item-c", Status: "raised", WaitingOn: "owner", Text: "kept"}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ro, err := store.OpenRead(ctx, s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		qBefore := store.SQLQueriesIssued()
		empty, err := ReadConflictsForItems(r, nil)
		if err != nil {
			return err
		}
		if len(empty) != 0 {
			t.Fatalf("empty ReadConflictsForItems = %+v, want empty", empty)
		}
		if got := store.SQLQueriesIssued() - qBefore; got != 0 {
			t.Fatalf("empty ReadConflictsForItems issued %d queries, want 0", got)
		}

		got, err := ReadConflictsForItems(r, []string{"item-a", "item-c"})
		if err != nil {
			return err
		}
		if len(got) != 2 {
			t.Fatalf("ReadConflictsForItems returned %d entries, want 2: %+v", len(got), got)
		}
		if got["item-a"].Status != "answered" || got["item-a"].Text != "resolved" {
			t.Fatalf("item-a = %+v, want answered/resolved", got["item-a"])
		}
		if got["item-c"].Status != "raised" || got["item-c"].Text != "kept" {
			t.Fatalf("item-c = %+v, want raised/kept", got["item-c"])
		}
		if _, ok := got["item-b"]; ok {
			t.Fatalf("unexpected out-of-scope item-b in result: %+v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadSplitsForItems(t *testing.T) {
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	st1 := SplitStatement{
		Parts: []SplitPart{{Label: "a", Title: "Part A"}},
	}
	st2 := SplitStatement{
		Parts: []SplitPart{{Label: "b", Title: "Part B"}},
	}
	raw1, err := json.Marshal(st1)
	if err != nil {
		t.Fatal(err)
	}
	raw2, err := json.Marshal(st2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items (item_id, title, state, updated_at) VALUES ('item-parent-1', 'P1', 'blocked', 1), ('item-parent-2', 'P2', 'blocked', 1)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO item_splits (parent_id, statement) VALUES (?, ?), (?, ?)`,
			"item-parent-1", raw1, "item-parent-2", raw2); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ro, err := store.OpenRead(ctx, s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		qBefore := store.SQLQueriesIssued()
		empty, err := ReadSplitsForItems(r, nil)
		if err != nil {
			return err
		}
		if len(empty) != 0 {
			t.Fatalf("empty ReadSplitsForItems = %+v, want empty", empty)
		}
		if got := store.SQLQueriesIssued() - qBefore; got != 0 {
			t.Fatalf("empty ReadSplitsForItems issued %d queries, want 0", got)
		}

		// Parent in itemIDs reads only that parent row.
		rowsBefore := store.SQLRowsRead()
		byParent, err := ReadSplitsForItems(r, []string{"item-parent-1"})
		if err != nil {
			return err
		}
		if len(byParent) != 1 || len(byParent["item-parent-1"].Parts) != 1 || byParent["item-parent-1"].Parts[0].Label != "a" {
			t.Fatalf("byParent = %+v, want only item-parent-1", byParent)
		}
		if got := store.SQLRowsRead() - rowsBefore; got != 1 {
			t.Fatalf("byParent read %d rows, want 1", got)
		}

		// Part ID whose parent is not in itemIDs falls back to ReadAllSplits.
		part2ID := SplitPartID("item-parent-2", "b")
		byPart, err := ReadSplitsForItems(r, []string{part2ID})
		if err != nil {
			return err
		}
		if len(byPart) != 2 || len(byPart["item-parent-2"].Parts) != 1 || byPart["item-parent-2"].Parts[0].Label != "b" {
			t.Fatalf("byPart = %+v, want fallback with item-parent-2", byPart)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
