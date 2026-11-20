package drain

import (
	"context"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// While a run the loop just started is claiming its lane, the loop's gathered writes do not
// begin an attempt: the claim's write goes first, and the write begins once the gate closes.
func TestAGatheredWriteWaitsWhileAStartedRunClaimsItsLane(t *testing.T) {
	s := gatheredStore(t)
	end := claimGateFor(s.Path()).begin()
	began := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- writeGathered(context.Background(), s, func(context.Context, *store.Tx) error {
			close(began)
			return nil
		})
	}()
	select {
	case <-began:
		t.Fatal("a gathered write began while a started run was claiming its lane")
	case <-time.After(100 * time.Millisecond):
	}
	end()
	end() // closing twice is harmless
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-began:
	default:
		t.Fatal("the write did not run after the claim")
	}
}

// A goroutine already holding a store write does not wait for the gate: the claim cannot
// proceed while it holds the lock, so waiting would only run the window out.
func TestTheClaimGateDoesNotHoldAWriterThatAlreadyHoldsTheLock(t *testing.T) {
	s := gatheredStore(t)
	end := claimGateFor(s.Path()).begin()
	defer end()
	waited := make(chan error, 1)
	err := s.WriteTx(context.Background(), func(*store.Tx) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		waited <- claimGateFor(s.Path()).wait(ctx)
		return nil
	})
	if err != nil || <-waited != nil {
		t.Fatalf("a writer holding the lock waited on the gate: %v", err)
	}
}
