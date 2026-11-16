package event_test

import (
	"testing"

	"github.com/mschulkind-oss/tillr/event"
)

// A kind that is not registered is a kind the conformance suite cannot check, so
// appending one is refused rather than recorded. The registry is the list the
// design's boundary contract names.
func TestAnUnregisteredKindIsRefused(t *testing.T) {
	if err := event.Register("gate.verdict", "gate"); err != nil {
		t.Fatalf("registering a kind failed: %v", err)
	}
	if _, err := event.Lookup("gate.verdict"); err != nil {
		t.Fatalf("a registered kind was not found: %v", err)
	}
	if _, err := event.Lookup("gate.invented"); err == nil {
		t.Fatal("an unregistered kind was found")
	}
}

// Two utilities registering one kind is the "two writers of one state" defect the
// design's four-question test exists to catch, so it is refused at registration.
func TestTwoUtilitiesCannotOwnOneKind(t *testing.T) {
	if err := event.Register("land.attempt", "land"); err != nil {
		t.Fatalf("registering a kind failed: %v", err)
	}
	if err := event.Register("land.attempt", "drain"); err == nil {
		t.Fatal("a second utility registered a kind another already owns")
	}
}
