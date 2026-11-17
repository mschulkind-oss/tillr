package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// A running Loop takes the checkpoint owner (and its keeper) before its first session
// write opens the store with create, which is the open that migrates. Startup was the one
// stretch of a Loop's life with no owner: the 13:51Z fault was the loop's own migration
// waiting out the busy timeout while it began.
func TestTheLoopTakesTheOwnerBeforeItsFirstMigration(t *testing.T) {
	f := newRefusalLoopFixture(t)
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	l.keepWALWhileRunning()
	defer l.closeWALKeeper()
	var creates, ownedAtCreate int
	l.beforeSessionOpen = func(create bool) {
		if !create {
			return
		}
		creates++
		running, err := store.OwnerRunning(f.storePath)
		if err != nil {
			t.Error(err)
		}
		if running {
			ownedAtCreate++
		}
	}
	if err := l.refreshSession(context.Background(), l.cfg.Now().Add(PassInterval)); err != nil {
		t.Fatal(err)
	}
	if creates != 1 {
		t.Fatalf("the first session write opened the store with create %d times, want 1", creates)
	}
	if ownedAtCreate != 1 {
		t.Error("the loop's first, migrating open ran before the loop took the checkpoint owner")
	}
}
