package event_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"

	_ "modernc.org/sqlite"
)

// Append assigns the seq: it is the event's position in the log, and a caller that
// could set it could write two events at one position or skip ahead. The field is
// carried on the event for a reader's cursor, so the write side ignoring it is
// documented and pinned rather than left to be discovered.
func TestAppendIgnoresACallerSetSeq(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening an in-memory store failed: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(event.EventsDDL); err != nil {
		t.Fatalf("creating the events table failed: %v", err)
	}
	if err := event.Register("test.appended", "test"); err != nil {
		t.Fatalf("registering a kind failed: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("beginning a transaction failed: %v", err)
	}
	defer tx.Rollback()
	seq, err := event.Append(ctx, tx, event.Event{Kind: "test.appended", Seq: 99, At: time.Unix(0, 0).UTC()})
	if err != nil {
		t.Fatalf("appending failed: %v", err)
	}
	if seq != 1 {
		t.Fatalf("Append answered seq %d for a caller-set 99, want the store's 1", seq)
	}
	var stored int64
	if err := tx.QueryRow(`SELECT seq FROM events`).Scan(&stored); err != nil {
		t.Fatalf("reading the stored seq failed: %v", err)
	}
	if stored != 1 {
		t.Fatalf("the stored seq is %d, want 1", stored)
	}
}
