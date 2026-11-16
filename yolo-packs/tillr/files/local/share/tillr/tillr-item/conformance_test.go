package main_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// The item utility's conformance case: the two things a second consumer of this module
// would break by linking it -- the item event kinds registered with their owner, and
// the item tables present in the composed migration list.
//
// It lives in the utility's own package so the conformance suite grows one case per
// utility as each lands, and no central file is edited to add one.
func TestTheItemUtilityRegistersItsOwnContract(t *testing.T) {
	for _, kind := range []string{"item.add", "item.update"} {
		found, err := event.Lookup(kind)
		if err != nil {
			t.Fatalf("the item event kind %q is not registered: %v", kind, err)
		}
		if found.Owner != "item" {
			t.Fatalf("%s is owned by %q, want item", kind, found.Owner)
		}
	}
	s, err := store.InitFixture(context.Background(), filepath.Join(scratch(t, "conformance"), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	counts, err := s.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	for _, table := range []string{"items", "item_criteria", "item_edges"} {
		if _, ok := counts[table]; !ok {
			t.Fatalf("the item table %q is not in the composed migration list: %v", table, counts)
		}
	}
}

// The decision and mail faces' conformance case: the two things a second consumer of
// this module would break by linking it — the four event kinds registered with their
// owner, and the two ledgers present in the composed migration list.
//
// The negative control is the registration: drop one kind from schema.go's init and
// the lookup below fails; drop the decisions or mail_deliveries DDL and the table
// assertion fails on a store that has no such table.
func TestTheItemUtilityRegistersTheDecisionAndMailContract(t *testing.T) {
	for _, kind := range []string{"decision.record", "decision.revoke", "mail.send", "mail.ack"} {
		found, err := event.Lookup(kind)
		if err != nil {
			t.Fatalf("the item event kind %q is not registered: %v", kind, err)
		}
		if found.Owner != "item" {
			t.Fatalf("%s is owned by %q, want item", kind, found.Owner)
		}
	}
	s, err := store.InitFixture(context.Background(), filepath.Join(scratch(t, "conformance-decisions"), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	counts, err := s.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	for _, table := range []string{"decisions", "mail_deliveries"} {
		if _, ok := counts[table]; !ok {
			t.Fatalf("the item table %q is not in the composed migration list: %v", table, counts)
		}
	}
}

// The absence of stored question state is part of this module's contract: open
// questions live in design documents, and a later lane that "helpfully" cached them
// would be the second copy design §5.3 forbids. The case pins the absence, because an
// absence nothing checks is an absence that comes back.
//
// The negative control is the registration: register a question.read kind, or add a
// questions table to ItemDDL, and the assertion below fails.
func TestTheItemUtilityStoresNoQuestionState(t *testing.T) {
	for _, kind := range event.Kinds() {
		if strings.HasPrefix(kind.Name, "question.") {
			t.Fatalf("the event registry holds %q, owned by %q: questions are not board state", kind.Name, kind.Owner)
		}
	}
	s, err := store.InitFixture(context.Background(), filepath.Join(scratch(t, "conformance-questions"), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	counts, err := s.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	if _, ok := counts["questions"]; ok {
		t.Fatalf("the composed migration list holds a questions table: %v", counts)
	}
}
