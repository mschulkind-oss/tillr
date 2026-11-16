package event_test

import (
	"database/sql"
	"testing"

	"github.com/mschulkind-oss/tillr/event"

	_ "modernc.org/sqlite"
)

// The event utility's conformance case: the append-only invariant its DDL carries.
// The triggers are what make "an event is written once and never rewritten" true
// rather than a convention, and a second consumer of this package would break it by
// linking a DDL without them.
//
// It lives in the utility's own package so the conformance suite grows one case per
// utility as each lands, and no central file is edited to add one.
func TestTheEventUtilitysDDLIsAppendOnly(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening an in-memory store failed: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(event.EventsDDL); err != nil {
		t.Fatalf("applying the events DDL failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO events (ts, kind) VALUES (0, 'event.conformance')`); err != nil {
		t.Fatalf("appending an event failed: %v", err)
	}
	if _, err := db.Exec(`UPDATE events SET kind = 'event.rewritten'`); err == nil {
		t.Fatal("an event was rewritten")
	}
	if _, err := db.Exec(`DELETE FROM events`); err == nil {
		t.Fatal("an event was deleted")
	}
}
