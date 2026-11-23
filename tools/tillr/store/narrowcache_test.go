package store_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	bulkKind    = "store.narrow_test.bulk"
	startedKind = "store.narrow_test.started"
	doneKind    = "store.narrow_test.done"
	itemKind    = "store.narrow_test.item"
)

func init() {
	for _, kind := range []string{bulkKind, startedKind, doneKind, itemKind} {
		if err := event.Register(kind, "store"); err != nil {
			panic(err)
		}
	}
}

func appendKinds(t *testing.T, s *store.Store, kinds ...string) {
	t.Helper()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for i, kind := range kinds {
			e := event.Event{Kind: kind, Payload: []byte(`{}`), At: time.Unix(int64(i), 0).UTC()}
			if kind == itemKind {
				id := "item-1"
				e.ObjectType, e.ObjectID = "item", &id
			}
			if _, err := tx.Append(e); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func selectedRead(t *testing.T, s *store.Store, since int64, sel store.EventSelection) []event.Event {
	t.Helper()
	var out []event.Event
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		out, err = r.EventsSelected(since, sel)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// The observatory's snapshot folds the same few kinds on every request over a log that is
// mostly worker records. With the narrow cache on, a repeated selected read reads from SQLite
// only the selected events committed since, answers exactly what an uncached read answers,
// and never holds or reads the unselected bulk.
func TestSelectedEventCacheReadsOnlyTheSelectedEventsCommittedSince(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	bulk := make([]string, 300)
	for i := range bulk {
		bulk[i] = bulkKind
	}
	appendKinds(t, s, append(bulk, startedKind, itemKind, doneKind)...)
	sel := store.EventSelection{Kinds: []string{startedKind, doneKind}, ObjectTypes: []string{"item"}}
	want := selectedRead(t, s, 0, sel) // uncached: the reference answer
	if len(want) != 3 {
		t.Fatalf("fixture: %d selected events, want 3", len(want))
	}

	t.Cleanup(store.SetSelectedEventCache(true))
	cached, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cached.Close() })
	if got := selectedRead(t, cached, 0, sel); !reflect.DeepEqual(got, want) {
		t.Fatalf("the first cached read = %v, want %v", got, want)
	}
	appendKinds(t, s, bulkKind, startedKind, bulkKind, itemKind)
	before := store.EventRowsRead()
	got := selectedRead(t, cached, 0, sel)
	if read := store.EventRowsRead() - before; read != 2 {
		t.Fatalf("the repeat read %d event rows from SQLite; want the 2 selected ones committed since", read)
	}
	if ref := selectedRead(t, s, 0, sel); !reflect.DeepEqual(got, ref) || len(got) != 5 {
		t.Fatalf("the repeat read = %d events, want the uncached answer %d (5): %v", len(got), len(ref), got)
	}
	// since still narrows a cached answer, and a whole-log read is not served from the cache.
	if got := selectedRead(t, cached, want[1].Seq, sel); len(got) != 3 || got[0].Seq <= want[1].Seq {
		t.Fatalf("the read since seq %d answered %v", want[1].Seq, got)
	}
}
