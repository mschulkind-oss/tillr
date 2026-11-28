package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const readCounterTestKind = "store.read_counter_test.add"

func init() {
	if err := event.Register(readCounterTestKind, "store"); err != nil {
		panic(err)
	}
}

func TestReadCountersAndEventsForObjects(t *testing.T) {
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(t.TempDir(), "board.sqlite3"))
	if err != nil {
		t.Fatalf("init fixture: %v", err)
	}
	defer s.Close()

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, id := range []string{"item-a", "item-b", "item-c"} {
			id := id
			if _, err := tx.Append(event.Event{
				Kind:       readCounterTestKind,
				ObjectType: "item",
				ObjectID:   &id,
				Payload:    []byte(`{}`),
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	ro, err := store.OpenRead(ctx, s.Path())
	if err != nil {
		t.Fatalf("open read: %v", err)
	}
	defer ro.Close()

	qBefore := store.SQLQueriesIssued()
	rBefore := store.SQLRowsRead()
	evBefore := store.EventRowsRead()

	var evs []event.Event
	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		evs, err = r.EventsForObjects(0, []string{"item-a", "item-c", "item-a"})
		return err
	}); err != nil {
		t.Fatalf("read: %v", err)
	}

	if len(evs) != 2 {
		t.Fatalf("EventsForObjects returned %d events, want 2", len(evs))
	}
	if got := store.SQLQueriesIssued() - qBefore; got != 1 {
		t.Fatalf("SQLQueriesIssued = %d, want 1", got)
	}
	if got := store.SQLRowsRead() - rBefore; got != 2 {
		t.Fatalf("SQLRowsRead = %d, want 2", got)
	}
	if got := store.EventRowsRead() - evBefore; got != 2 {
		t.Fatalf("EventRowsRead = %d, want 2", got)
	}
}
