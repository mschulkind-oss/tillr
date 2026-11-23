package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// One read-only snapshot cannot change between two reads, so a fold that asks for a kind
// another fold already asked for is answered from the first read. A one-shot program
// (`tillr drain why`) read the same worker-history kinds four times in one snapshot, about
// 550 MB of payloads for a 300 KB answer, measured 2026-11-22. The answers stay exactly
// what the unshared read answers.
func TestSelectedReadsInOneSnapshotReadEachKindOnce(t *testing.T) {
	t.Cleanup(store.SetEventCache(false))
	s, err := store.InitFixture(context.Background(), filepath.Join(t.TempDir(), "board.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	appendMixed(t, s, 5, 10, "history")
	selections := []store.EventSelection{
		{Kinds: []string{foldKind}},
		{Kinds: []string{foldKind}},
		{Kinds: []string{foldKind, otherKind}},
		{Kinds: []string{recordKind}, ObjectTypes: []string{"item"}},
	}
	for _, since := range []int64{0, 7} {
		var all []event.Event
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			var err error
			all, err = r.Events(since)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		before := store.EventRowsRead()
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			for i, sel := range selections {
				got, err := r.EventsSelected(since, sel)
				if err != nil {
					return err
				}
				var want []event.Event
				for _, e := range all {
					if len(sel.Kinds) > 0 && contains(sel.Kinds, e.Kind) || len(sel.ObjectTypes) > 0 && contains(sel.ObjectTypes, e.ObjectType) {
						want = append(want, e)
					}
				}
				if !sameEvents(got, want) {
					t.Fatalf("since %d selection %d answered %d events, want %d (or in another order)", since, i, len(got), len(want))
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		// Each distinct key (the three kinds and the item object type) is read once.
		var distinct int64
		for _, e := range all {
			if e.Kind == foldKind {
				distinct++
			}
			if e.Kind == otherKind {
				distinct++
			}
			if e.Kind == recordKind {
				distinct++
			}
			if e.ObjectType == "item" {
				distinct++
			}
		}
		if read := store.EventRowsRead() - before; read != distinct {
			t.Fatalf("since %d: %d event rows read for four selections, want %d (each key once)", since, read, distinct)
		}
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
