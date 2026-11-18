package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func waitForWriters(t *testing.T, path string, n int64) {
	t.Helper()
	deadline := time.Now().Add(hangGuard)
	for store.WritersWaiting(path) != n {
		if time.Now().After(deadline) {
			t.Fatalf("never saw %d writers waiting (now %d)", n, store.WritersWaiting(path))
		}
		time.Sleep(time.Millisecond)
	}
}

// The 2026-11-18 drain stops: a pass's back-to-back write transactions won every gap
// SQLite's polling busy handler left, so the loop's heartbeat, on another connection in
// the same process, waited out its busy timeout and failed. In-process writers now take
// turns first come, first served: with one transaction holding the lock, a heartbeat
// that asks first is served before the next of the pass's transactions, however short
// the gap between them.
func TestThisProcesssWritersAreServedInTheOrderTheyAsked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	open := func() *store.Store {
		s, err := store.Open(context.Background(), path, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	holder, heartbeat, next := open(), open(), open()
	holding, release := make(chan struct{}), make(chan struct{})
	held := make(chan error, 1)
	go func() {
		held <- holder.WriteTx(context.Background(), func(*store.Tx) error { close(holding); <-release; return nil })
	}()
	<-holding
	order := make(chan string, 2)
	done := make(chan error, 2)
	write := func(s *store.Store, name string) {
		done <- s.WriteTx(context.Background(), func(*store.Tx) error { order <- name; return nil })
	}
	go write(heartbeat, "heartbeat")
	waitForWriters(t, path, 1)
	go write(next, "next")
	waitForWriters(t, path, 2)
	close(release)
	for _, c := range []chan error{held, done, done} {
		if err := <-c; err != nil {
			t.Fatal(err)
		}
	}
	if first, second := <-order, <-order; first != "heartbeat" || second != "next" {
		t.Fatalf("writers were served %s then %s; the heartbeat asked first", first, second)
	}
}

// A write transaction begun inside another's callback on the same goroutine does not wait
// for its own turn: it meets SQLite's lock exactly as it did before the queue, so a
// nested writer is refused as busy rather than hanging.
func TestANestedWriterDoesNotWaitForItsOwnTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	outer, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer outer.Close()
	inner, err := store.Open(context.Background(), path, false, store.WithBusyTimeout(0))
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	result := make(chan error, 1)
	go func() {
		result <- outer.WriteTx(context.Background(), func(*store.Tx) error {
			return inner.WriteTx(context.Background(), func(*store.Tx) error { return nil })
		})
	}()
	select {
	case err := <-result:
		if !store.IsBusy(err) {
			t.Fatalf("the nested writer answered %v, want SQLite's busy refusal", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("a nested writer waited for its own turn")
	}
}
