package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// The guard refuses I/O from inside a write transaction's callback, and only there: the
// same goroutine before and after the write, and another goroutine while the write is
// open, hold nothing and are let through.
func TestTheIOGuardRefusesIOOnlyInsideAWrite(t *testing.T) {
	s := writeStore(t)
	if err := store.CheckIO("git rev-parse"); err != nil {
		t.Fatalf("no write is open, yet the guard refused: %v", err)
	}
	var inside, elsewhere error
	var open int64
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		inside = store.CheckIO("git update-ref")
		open = store.OpenWriteTransactions()
		done := make(chan error)
		go func() { done <- store.CheckIO("git status") }()
		elsewhere = <-done
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(inside, store.ErrIOUnderWrite) {
		t.Fatalf("git started inside a write was not refused: %v", inside)
	}
	if open != 1 {
		t.Fatalf("open write transactions inside the write = %d, want 1", open)
	}
	if elsewhere != nil {
		t.Fatalf("another goroutine holds no write, yet was refused: %v", elsewhere)
	}
	if err := store.CheckIO("git rev-parse"); err != nil || store.OpenWriteTransactions() != 0 {
		t.Fatalf("after the commit: guard %v, open %d", err, store.OpenWriteTransactions())
	}
}

// A write that fails still ends: its rollback releases the guard as its commit would.
func TestTheIOGuardIsReleasedByARolledBackWrite(t *testing.T) {
	s := writeStore(t)
	boom := errors.New("refused")
	if err := s.WriteTx(context.Background(), func(*store.Tx) error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if err := store.CheckIO("git rev-parse"); err != nil {
		t.Fatalf("a rolled-back write left the guard held: %v", err)
	}
}
