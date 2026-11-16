package store_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// writeKind is the kind the write-transaction tests append. It is registered here
// rather than borrowed from the store's own kinds so a failure names the write
// transaction and not the seed marker.
const writeKind = "store.write_test"

func init() {
	if err := event.Register(writeKind, "store"); err != nil {
		panic(err)
	}
}

func writeStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.InitFixture(context.Background(), filepath.Join(t.TempDir(), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A write transaction commits the row and the event describing it together, so a
// reader never sees one without the other (design §5.1).
func TestAWriteTransactionCommitsTheRowAndItsEventTogether(t *testing.T) {
	s := writeStore(t)
	var seq int64
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('written', 'yes')`); err != nil {
			return err
		}
		var err error
		seq, err = tx.Append(event.Event{
			Kind:    writeKind,
			Payload: []byte(`{"n":1}`),
			At:      time.Unix(0, 0).UTC(),
		})
		return err
	})
	if err != nil {
		t.Fatalf("the write transaction failed: %v", err)
	}
	value, err := s.Meta("written")
	if err != nil {
		t.Fatalf("reading the written row failed: %v", err)
	}
	if value == nil || *value != "yes" {
		t.Fatalf("the committed row reads %v, want yes", value)
	}
	events, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("the write transaction left %d events, want 1", len(events))
	}
	if events[0].Kind != writeKind {
		t.Fatalf("the committed event is %q, want %q", events[0].Kind, writeKind)
	}
	if events[0].Seq != seq {
		t.Fatalf("Append answered seq %d but the committed event is at %d", seq, events[0].Seq)
	}
}

// A write transaction that fails leaves neither the row nor the event: they are one
// transaction, so a rollback takes both. This is the half of "never one without the
// other" that a commit-only test cannot see.
func TestARolledBackWriteLeavesNeitherTheRowNorTheEvent(t *testing.T) {
	s := writeStore(t)
	refused := errors.New("refused")
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('written', 'yes')`); err != nil {
			return err
		}
		if _, err := tx.Append(event.Event{Kind: writeKind, At: time.Unix(0, 0).UTC()}); err != nil {
			return err
		}
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatalf("the write transaction answered %v, want the caller's refusal", err)
	}
	value, err := s.Meta("written")
	if err != nil {
		t.Fatalf("reading the row failed: %v", err)
	}
	if value != nil {
		t.Fatalf("a rolled-back write left the row %q", *value)
	}
	events, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("a rolled-back write left %d events", len(events))
	}
}

// An unregistered kind is refused inside the write transaction, and the refusal
// takes the row with it: a kind the conformance suite cannot check is not recorded,
// and a row whose event was refused is not recorded either.
func TestAWriteTransactionRefusesAnUnregisteredEventKind(t *testing.T) {
	s := writeStore(t)
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('written', 'yes')`); err != nil {
			return err
		}
		_, err := tx.Append(event.Event{Kind: "store.invented", At: time.Unix(0, 0).UTC()})
		return err
	})
	if err == nil {
		t.Fatal("a write transaction appended an unregistered kind")
	}
	value, err := s.Meta("written")
	if err != nil {
		t.Fatalf("reading the row failed: %v", err)
	}
	if value != nil {
		t.Fatalf("a refused append left the row %q", *value)
	}
}

// A write face reads the state it is about to change inside its own transaction, so
// the read and the write describe one moment. A reader built over the store's own
// handle instead of the transaction would not see the uncommitted row.
func TestAWriteTransactionReadsItsOwnUncommittedRows(t *testing.T) {
	s := writeStore(t)
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('pending', 'yes')`); err != nil {
			return err
		}
		var value string
		if err := tx.Reader().QueryRow(`SELECT value FROM meta WHERE key = 'pending'`).Scan(&value); err != nil {
			return fmt.Errorf("reading the uncommitted row: %w", err)
		}
		if value != "yes" {
			return fmt.Errorf("the uncommitted row reads %q, want yes", value)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the write transaction failed: %v", err)
	}
}

// A second writer on one store waits for the first instead of failing. Two processes
// open one store and each reads and then writes; the second's transaction must wait out
// the handle's busy timeout and then commit, because its begin takes the write lock
// before it reads anything. A deferred begin reads first and then asks to become the
// writer, and SQLite answers that upgrade with SQLITE_BUSY rather than waiting, since
// waiting there could deadlock.
//
// The 250ms window is the time the second writer is given to *fail* while the first
// still holds the store; it is a readiness window, not a measured duration. A
// transaction that waited returns as soon as the first commits, however long that took.
func TestASecondWriterWaitsForTheFirstInsteadOfFailing(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	first, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	t.Cleanup(func() { first.Close() })
	second, err := store.Open(ctx, path, false)
	if err != nil {
		t.Fatalf("opening the second writable handle failed: %v", err)
	}
	t.Cleanup(func() { second.Close() })

	release := make(chan struct{})
	firstWrote := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.WriteTx(ctx, func(tx *store.Tx) error {
			var count int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM meta`).Scan(&count); err != nil {
				return fmt.Errorf("the first writer's read: %w", err)
			}
			if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('first', '1')`); err != nil {
				return fmt.Errorf("the first writer's write: %w", err)
			}
			close(firstWrote)
			<-release
			return nil
		})
	}()
	<-firstWrote

	attempting := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(attempting)
		secondDone <- second.WriteTx(ctx, func(tx *store.Tx) error {
			var count int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM meta`).Scan(&count); err != nil {
				return fmt.Errorf("the second writer's read: %w", err)
			}
			if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('second', '2')`); err != nil {
				return fmt.Errorf("the second writer's write: %w", err)
			}
			return nil
		})
	}()
	<-attempting
	select {
	case err := <-secondDone:
		t.Fatalf("the second writer failed while the first held the store instead of waiting: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("the first writer failed: %v", err)
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("the second writer failed after the first committed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the second writer never finished after the first committed")
	}
	for _, key := range []string{"first", "second"} {
		value, err := first.Meta(key)
		if err != nil {
			t.Fatalf("reading %q failed: %v", key, err)
		}
		if value == nil {
			t.Fatalf("the %q row did not commit", key)
		}
	}
}
