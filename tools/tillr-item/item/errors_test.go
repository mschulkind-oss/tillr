package item_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
)

func TestRejectionEnvelopeUniqueIDs(t *testing.T) {
	at := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)

	// Rejections raised before an item id exists (e.g. too-many-criteria) must still
	// carry a unique, non-degenerate id rather than bare "rejection-".
	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		rej, ok := item.RejectionOf(item.Rejectionf("too-many-criteria", "", "too many criteria"))
		if !ok {
			t.Fatal("RejectionOf returned false")
		}
		env := item.RejectionEnvelope(rej, at)
		if !strings.HasPrefix(env.ID, "rejection-") || env.ID == "rejection-" {
			t.Fatalf("degenerate rejection id %q", env.ID)
		}
		if seen[env.ID] {
			t.Fatalf("duplicate rejection id %q", env.ID)
		}
		seen[env.ID] = true
	}

	// Rejections for the same item also carry distinct ids across invocations.
	for i := 0; i < 8; i++ {
		rej, ok := item.RejectionOf(item.Rejectionf("not-found", "item-1", "missing"))
		if !ok {
			t.Fatal("RejectionOf returned false")
		}
		env := item.RejectionEnvelope(rej, at)
		if !strings.HasPrefix(env.ID, "rejection-item-1-") || env.ID == "rejection-item-1-" {
			t.Fatalf("unexpected item rejection id %q", env.ID)
		}
		if seen[env.ID] {
			t.Fatalf("duplicate rejection id %q", env.ID)
		}
		seen[env.ID] = true
	}
}
