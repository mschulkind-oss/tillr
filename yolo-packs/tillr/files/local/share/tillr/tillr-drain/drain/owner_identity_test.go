package drain

import (
	"context"
	"testing"
)

// The loop's process identity is read from /proc once, before any transaction.
// Settle and judge claims run inside the pass's write transaction, so they must
// answer it from memory (owner's rule: minimal work while holding any lock).
func TestClaimsInsideTheWriterDoNotReadTheProcessTable(t *testing.T) {
	f, l := passFixture(t, "item-a")
	if _, err := NewLoop(l.cfg); err != nil {
		t.Fatal(err)
	}
	before := ownerReads.Load()
	for i := 0; i < 3; i++ {
		if _, err := currentLoopOwner(); err != nil {
			t.Fatal(err)
		}
	}
	claimRun(t, f, l)
	// A pass's write transaction reaps dead loops' claims.
	if _, _, err := l.passPlans(context.Background(), l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	if n := ownerReads.Load() - before; n != 0 {
		t.Fatalf("process identity read %d time(s) after the loop was built, want 0", n)
	}
}
