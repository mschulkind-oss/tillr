package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func metaCount(t *testing.T, r *store.Reader) int {
	t.Helper()
	var n int
	if err := r.QueryRow(`SELECT count(*) FROM meta`).Scan(&n); err != nil {
		t.Fatalf("counting meta rows: %v", err)
	}
	return n
}

// With more than one read connection, a read that is still in flight does not hold up
// another: the second finishes while the first is blocked.
func TestAHandleWithReadConnectionsOverlapsReads(t *testing.T) {
	w := writeStore(t)
	r, err := store.OpenRead(context.Background(), w.Path(), store.WithReadConnections(2))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	inside, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- r.ReadTx(context.Background(), func(*store.Reader) error {
			close(inside)
			<-release
			return nil
		})
	}()
	<-inside
	second := make(chan error, 1)
	go func() { second <- r.ReadTx(context.Background(), func(*store.Reader) error { return nil }) }()
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("the second read failed: %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("the second read waited for the first")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The default handle keeps its one connection: reads queue.
func TestADefaultReadHandleStillSerializesReads(t *testing.T) {
	w := writeStore(t)
	r, err := store.OpenRead(context.Background(), w.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	inside, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- r.ReadTx(context.Background(), func(*store.Reader) error {
			close(inside)
			<-release
			return nil
		})
	}()
	<-inside
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := r.ReadTx(ctx, func(*store.Reader) error { return nil }); err == nil {
		t.Fatal("a second read ran beside the first on a one-connection handle")
	}
	close(release)
	<-done
}

// An answer is one read transaction on one connection: a writer committing in the middle
// of it, and another pooled read starting after the commit, do not change what it sees.
func TestAnAnswerStaysConsistentWhileAWriterCommitsMidAnswer(t *testing.T) {
	w := writeStore(t)
	r, err := store.OpenRead(context.Background(), w.Path(), store.WithReadConnections(4))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	err = r.ReadTx(context.Background(), func(rd *store.Reader) error {
		before := metaCount(t, rd)
		if err := w.WriteTx(context.Background(), func(tx *store.Tx) error {
			if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('mid-answer', 'yes')`); err != nil {
				return err
			}
			_, err := tx.Append(event.Event{Kind: writeKind, Payload: []byte(`{}`), At: time.Unix(0, 0).UTC()})
			return err
		}); err != nil {
			t.Fatalf("the writer was blocked by a reader: %v", err)
		}
		// A second answer on another connection sees the commit.
		if err := r.ReadTx(context.Background(), func(other *store.Reader) error {
			if got := metaCount(t, other); got != before+1 {
				t.Errorf("a read begun after the commit saw %d rows, want %d", got, before+1)
			}
			return nil
		}); err != nil {
			return err
		}
		if after := metaCount(t, rd); after != before {
			t.Errorf("the answer changed mid-way: %d rows, then %d", before, after)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func dataVersionOf(t *testing.T, s *store.Store) int64 {
	t.Helper()
	var v int64
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		v, err = r.DataVersion()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return v
}

// The store's change stamp must not depend on which pooled connection answered: with no
// commit it is the same on every read (even when reads overlap on different connections),
// and a writer's commit moves it.
func TestTheDataVersionStampMovesOnACommitWhateverConnectionReads(t *testing.T) {
	w := writeStore(t)
	r, err := store.OpenRead(context.Background(), w.Path(), store.WithReadConnections(4))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// Make the pool open several connections, each with its own history.
	hold, entered := make(chan struct{}), make(chan struct{}, 4)
	done := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			done <- r.ReadTx(context.Background(), func(rd *store.Reader) error {
				metaCount(t, rd)
				entered <- struct{}{}
				<-hold
				return nil
			})
		}()
	}
	for i := 0; i < 4; i++ {
		<-entered
	}
	close(hold)
	for i := 0; i < 4; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	first := dataVersionOf(t, r)
	for i := 0; i < 8; i++ {
		if got := dataVersionOf(t, r); got != first {
			t.Fatalf("read %d: stamp %d with no commit, want %d", i, got, first)
		}
	}
	for commit := 0; commit < 3; commit++ {
		before := dataVersionOf(t, r)
		if err := w.WriteTx(context.Background(), func(tx *store.Tx) error {
			_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, 'x')`, time.Now().String()+string(rune('a'+commit)))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 8; i++ {
			if got := dataVersionOf(t, r); got == before {
				t.Fatalf("commit %d: read %d still stamped %d after the commit", commit, i, got)
			}
		}
	}
}
