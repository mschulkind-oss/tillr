package drain

import (
	"context"
	"os"
	"path/filepath"
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

// While the loop's `tillr-item state ... landed` child is running, the loop's repeating
// writers (gathered writes and checkpoint cycles) wait on the claim gate so the child is not
// starved off the store's write lock.
func TestItemStateLandedHoldsClaimGateAcrossChildCall(t *testing.T) {
	f := newRefusalLoopFixture(t)
	base := time.Date(2026, 11, 24, 11, 19, 0, 0, time.UTC)
	now := base

	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	l.cfg.Now = func() time.Time { return now }
	l.keepWALWhileRunning()
	defer l.closeWALKeeper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.refreshSession(ctx, now.Add(PassInterval)); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(ctx, f.storePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	dir := t.TempDir()
	startedFile := filepath.Join(dir, "started")
	releaseFile := filepath.Join(dir, "release")
	itemBin := executable(t,
		"touch '"+startedFile+"'\n"+
			"while [ ! -f '"+releaseFile+"' ]; do sleep 0.01; done\n"+
			"exit 0")

	steps := &ProcessSteps{
		Store:   f.storePath,
		ItemBin: itemBin,
		Now:     func() time.Time { return now },
	}

	itemDone := make(chan error, 1)
	go func() {
		itemDone <- steps.ItemState(ctx, "item-a", "run-a", StateLanded)
	}()
	released := false
	release := func() {
		if !released {
			released = true
			_ = os.WriteFile(releaseFile, []byte("go\n"), 0o644)
		}
	}
	defer func() {
		release()
		cancel()
	}()

	deadline := time.Now().Add(hangGuard)
	for {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for ItemState child to start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	gatheredBegan := make(chan struct{})
	gatheredDone := make(chan error, 1)
	go func() {
		gatheredDone <- writeGathered(ctx, s, func(context.Context, *store.Tx) error {
			close(gatheredBegan)
			return nil
		})
	}()

	now = now.Add(CheckpointInterval)
	ckptBegan := make(chan struct{})
	l.beforeCheckpoint = func() { close(ckptBegan) }
	ckptDone := make(chan error, 1)
	go func() {
		ckptDone <- l.checkpointIfDue(ctx)
	}()

	select {
	case <-gatheredBegan:
		t.Fatal("a gathered write began while ItemState(..., landed) was running")
	case <-ckptBegan:
		t.Fatal("a checkpoint cycle began while ItemState(..., landed) was running")
	case <-time.After(100 * time.Millisecond):
	}

	release()

	if err := <-itemDone; err != nil {
		t.Fatalf("ItemState(landed): %v", err)
	}
	if err := <-gatheredDone; err != nil {
		t.Fatalf("writeGathered after ItemState(landed): %v", err)
	}
	if err := <-ckptDone; err != nil {
		t.Fatalf("checkpointIfDue after ItemState(landed): %v", err)
	}
	select {
	case <-gatheredBegan:
	default:
		t.Fatal("writeGathered did not run after ItemState(landed) finished")
	}
	select {
	case <-ckptBegan:
	default:
		t.Fatal("checkpointIfDue did not run after ItemState(landed) finished")
	}
}

// Non-landed ItemState calls are out of scope and do not open the claim gate.
func TestItemStateNonLandedDoesNotOpenClaimGate(t *testing.T) {
	s := gatheredStore(t)
	ctx := context.Background()

	dir := t.TempDir()
	startedFile := filepath.Join(dir, "started")
	releaseFile := filepath.Join(dir, "release")
	itemBin := executable(t,
		"touch '"+startedFile+"'\n"+
			"while [ ! -f '"+releaseFile+"' ]; do sleep 0.01; done\n"+
			"exit 0")

	steps := &ProcessSteps{
		Store:   s.Path(),
		ItemBin: itemBin,
		Now:     time.Now,
	}

	itemDone := make(chan error, 1)
	go func() {
		itemDone <- steps.ItemState(ctx, "item-a", "run-a", StateBlocked)
	}()
	defer func() {
		_ = os.WriteFile(releaseFile, []byte("go\n"), 0o644)
		<-itemDone
	}()

	deadline := time.Now().Add(hangGuard)
	for {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for ItemState child to start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	gatheredDone := make(chan error, 1)
	go func() {
		gatheredDone <- writeGathered(ctx, s, func(context.Context, *store.Tx) error { return nil })
	}()
	select {
	case err := <-gatheredDone:
		if err != nil {
			t.Fatalf("writeGathered: %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("writeGathered waited on a non-landed ItemState call")
	}
}
