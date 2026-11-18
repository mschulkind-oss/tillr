package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func cachedStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	t.Cleanup(store.SetEventCache(true))
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func appendN(t *testing.T, s *store.Store, n int, tag string) {
	t.Helper()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for i := 0; i < n; i++ {
			if _, err := tx.Append(event.Event{Kind: writeKind, Payload: []byte(fmt.Sprintf(`{"tag":%q,"n":%d}`, tag, i)), At: time.Unix(int64(i), 0).UTC()}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func readAll(t *testing.T, s *store.Store, since int64) []event.Event {
	t.Helper()
	var out []event.Event
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		out, err = r.Events(since)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// The 2026-11-18 drain stops: a write transaction that folds the event log held the
// store's write lock for a scan of the whole history. With the cache on, a read inside a
// write transaction reads from SQLite only the events committed since the process last
// read the log, and answers exactly what an uncached read answers -- including the
// transaction's own uncommitted events, which never enter the cache.
func TestAWriteTransactionReadsOnlyTheEventsCommittedSinceTheCachedLog(t *testing.T) {
	s, path := cachedStore(t)
	appendN(t, s, 200, "history")
	warm := readAll(t, s, 0) // a read-only transaction fills the cache
	appendN(t, s, 3, "since")
	var inTx []event.Event
	before := store.EventRowsRead()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Append(event.Event{Kind: writeKind, Payload: []byte(`{"own":true}`), At: time.Unix(9, 0).UTC()}); err != nil {
			return err
		}
		var err error
		inTx, err = tx.Reader().Events(0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if read := store.EventRowsRead() - before; read != 4 {
		t.Fatalf("the read under the write lock read %d event rows from SQLite; want the 3 committed since the cache plus the transaction's own", read)
	}
	if len(inTx) != len(warm)+4 {
		t.Fatalf("the write transaction saw %d events, want %d", len(inTx), len(warm)+4)
	}
	// The same answer an uncached handle gives.
	want := uncached(t, path)
	if !reflect.DeepEqual(inTx, want) {
		t.Fatalf("the cached read differs from an uncached read")
	}
	if got := readAll(t, s, 150); !reflect.DeepEqual(got, want[150:]) {
		t.Fatalf("a read from seq 150 differs from the uncached read")
	}
}

// A write transaction's own events are not cached: rolled back, they are gone, and the
// next read does not answer them.
func TestARolledBackTransactionsEventsAreNeverAnswered(t *testing.T) {
	s, path := cachedStore(t)
	appendN(t, s, 5, "history")
	readAll(t, s, 0)
	refused := errors.New("refused")
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Append(event.Event{Kind: writeKind, Payload: []byte(`{"rolled":"back"}`), At: time.Unix(7, 0).UTC()}); err != nil {
			return err
		}
		if _, err := tx.Reader().Events(0); err != nil {
			return err
		}
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatal(err)
	}
	appendN(t, s, 1, "after")
	if got, want := readAll(t, s, 0), uncached(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("after a rollback the cached log differs from the store's:\n got %d events\nwant %d", len(got), len(want))
	}
}

// A store replaced at the same path is read afresh, not answered from the history the
// process cached for the file that was there before.
func TestAStoreReplacedAtThePathIsNotAnsweredFromTheCache(t *testing.T) {
	t.Cleanup(store.SetEventCache(true))
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	first, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	appendN(t, first, 4, "aaaa")
	readAll(t, first, 0)
	first.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	second, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	// Same count, times and payload lengths: only the payload tells the files apart.
	appendN(t, second, 6, "bbbb")
	if got, want := readAll(t, second, 0), uncached(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("the replaced store was answered from the old file's cached history")
	}
}

func uncached(t *testing.T, path string) []event.Event {
	t.Helper()
	restore := store.SetEventCache(false)
	plain, err := store.OpenRead(context.Background(), path)
	restore()
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	var out []event.Event
	if err := plain.ReadTx(context.Background(), func(r *store.Reader) error { out, err = r.Events(0); return err }); err != nil {
		t.Fatal(err)
	}
	return out
}
