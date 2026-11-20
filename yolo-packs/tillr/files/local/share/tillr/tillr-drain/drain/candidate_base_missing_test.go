package drain

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A candidate whose recorded base is not a commit in its own checkout holds that one item.
// On 2026-11-20 fork item item-request-ab90e5da8f was settled with this repository's trunk
// commit as its base inside the pi fork checkout; `git diff` there failed with "Invalid
// revision range" and the error ended the loop as a fault on every restart.
func TestACandidateWhoseBaseIsNotInItsCheckoutHoldsTheItem(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	c.BaseSHA = "a89b3767732c5642733b81e1b872dd4e6a2ed9c8"
	var result LoopResult
	if _, _, err := l.Verify(context.Background(), c, &result, nil); !errors.Is(err, errItemFaultHeld) {
		t.Fatalf("Verify = %v, want the item held", err)
	}
	fault, ok := faultOf(t, f, c.ItemID)
	if !ok || fault.Reason != ReasonCandidateMismatch || !strings.Contains(fault.Detail, c.BaseSHA) || !strings.Contains(fault.Detail, c.Worktree) {
		t.Fatalf("fault = %+v (%v), want a candidate mismatch naming the base and the checkout", fault, ok)
	}
}
