package store_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	recordKind = "store.select_test.record"
	foldKind   = "store.select_test.fold"
	otherKind  = "store.select_test.other"
)

func init() {
	for _, kind := range []string{recordKind, foldKind, otherKind} {
		if err := event.Register(kind, "store"); err != nil {
			panic(err)
		}
	}
}

// appendMixed appends n worker-record-like events for every one event of the kinds a fold
// reads, the shape of the 2026-11-19 log: 1,267,132 of 1,294,976 events were run.child-session.
func appendMixed(t *testing.T, s *store.Store, rounds, n int, tag string) {
	t.Helper()
	item := "item-1"
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for r := 0; r < rounds; r++ {
			for i := 0; i < n; i++ {
				if _, err := tx.Append(event.Event{Kind: recordKind, ObjectType: "run", Payload: []byte(fmt.Sprintf(`{"tag":%q,"r":%d,"i":%d}`, tag, r, i)), At: time.Unix(int64(r), 0).UTC()}); err != nil {
					return err
				}
			}
			if _, err := tx.Append(event.Event{Kind: foldKind, ObjectType: "run", Payload: []byte(fmt.Sprintf(`{"tag":%q,"r":%d}`, tag, r)), At: time.Unix(int64(r), 0).UTC()}); err != nil {
				return err
			}
			if _, err := tx.Append(event.Event{Kind: otherKind, ObjectType: "item", ObjectID: &item, Payload: []byte(fmt.Sprintf(`{"tag":%q,"r":%d}`, tag, r)), At: time.Unix(int64(r), 0).UTC()}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// sameEvents compares two answers; an empty answer is the same however it is spelled.
func sameEvents(a, b []event.Event) bool {
	return len(a) == 0 && len(b) == 0 || reflect.DeepEqual(a, b)
}

func after(events []event.Event, since int64) []event.Event {
	var out []event.Event
	for _, e := range events {
		if e.Seq > since {
			out = append(out, e)
		}
	}
	return out
}

func filterEvents(events []event.Event, sel store.EventSelection) []event.Event {
	var out []event.Event
	for _, e := range events {
		match := false
		for _, k := range sel.Kinds {
			match = match || e.Kind == k
		}
		for _, o := range sel.ObjectTypes {
			match = match || e.ObjectType == o
		}
		if match {
			out = append(out, e)
		}
	}
	return out
}

// A selected read answers exactly the events a whole read answers that the selection
// selects, in seq order and once each, whether it is cached or not, read-only or inside a
// write transaction that holds events of its own.
func TestASelectedReadAnswersWhatTheWholeReadSelects(t *testing.T) {
	selections := []store.EventSelection{
		{Kinds: []string{foldKind}},
		{Kinds: []string{foldKind, otherKind}},
		{ObjectTypes: []string{"item"}},
		{Kinds: []string{foldKind, otherKind}, ObjectTypes: []string{"item"}}, // otherKind events match both
		{Kinds: []string{"store.select_test.absent"}},
	}
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached=%v", cached), func(t *testing.T) {
			s, path := cachedStore(t)
			if !cached {
				t.Cleanup(store.SetEventCache(false))
				s.Close()
				var err error
				if s, err = store.Open(context.Background(), path, true); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { s.Close() })
			}
			appendMixed(t, s, 20, 5, "history")
			readAll(t, s, 0) // fills the cache when it is on
			appendMixed(t, s, 3, 5, "since")
			for _, sel := range selections {
				for _, since := range []int64{0, 7, 60} {
					whole := uncached(t, path)
					var got []event.Event
					if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
						var err error
						got, err = r.EventsSelected(since, sel)
						return err
					}); err != nil {
						t.Fatal(err)
					}
					if want := filterEvents(after(whole, since), sel); !sameEvents(got, want) {
						t.Fatalf("read-only %+v since %d: got %d events, want %d", sel, since, len(got), len(want))
					}
					own := "own"
					var inTx []event.Event
					if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
						if _, err := tx.Append(event.Event{Kind: foldKind, ObjectType: "run", ObjectID: &own, Payload: []byte(`{"own":true}`), At: time.Unix(9, 0).UTC()}); err != nil {
							return err
						}
						var err error
						inTx, err = tx.Reader().EventsSelected(since, sel)
						return err
					}); err != nil {
						t.Fatal(err)
					}
					if want := filterEvents(after(uncached(t, path), since), sel); !sameEvents(inTx, want) {
						t.Fatalf("in a write %+v since %d: got %d events, want %d", sel, since, len(inTx), len(want))
					}
				}
			}
		})
	}
}

// The cost of a selected read inside a write transaction is the events it answers: the
// cached log is not copied and the tail is read narrowed. A whole-log read there is counted,
// because its cost is the log's length while every other writer waits.
func TestASelectedReadUnderTheWriteLockReadsOnlyWhatItAnswers(t *testing.T) {
	s, _ := cachedStore(t)
	appendMixed(t, s, 50, 100, "history")
	readAll(t, s, 0)
	appendMixed(t, s, 1, 100, "since")
	before, wholeBefore := store.EventRowsRead(), store.WholeLogReadsUnderWrite()
	var got []event.Event
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var err error
		got, err = tx.Reader().EventsOfKind(0, foldKind)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 51 {
		t.Fatalf("the selected read answered %d events, want 51", len(got))
	}
	if read := store.EventRowsRead() - before; read != 1 {
		t.Fatalf("the selected read under the write lock read %d event rows from SQLite; want the 1 selected event committed since the cache", read)
	}
	if n := store.WholeLogReadsUnderWrite() - wholeBefore; n != 0 {
		t.Fatalf("a selected read counted as %d whole-log reads", n)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Reader().Events(0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := store.WholeLogReadsUnderWrite() - wholeBefore; n != 1 {
		t.Fatalf("a whole-log read under the write lock counted %d times, want 1", n)
	}
	readAll(t, s, 0)
	if n := store.WholeLogReadsUnderWrite() - wholeBefore; n != 1 {
		t.Fatalf("a read-only whole-log read was counted as under the write lock")
	}
}
