package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestReadOnceIsScopedToAnImmutableTransaction(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "store.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	calls := 0
	read := func(r *store.Reader) error {
		for i := 0; i < 2; i++ {
			value, err := r.ReadOnce("fixture-value", func() (any, error) {
				calls++
				var count int
				err := r.QueryRow(`SELECT count(*) FROM meta`).Scan(&count)
				return count, err
			})
			if err != nil || value.(int) != 0 {
				t.Fatalf("value=%v err=%v", value, err)
			}
		}
		return nil
	}
	for want := 1; want <= 2; want++ {
		if err := s.ReadTx(ctx, read); err != nil {
			t.Fatal(err)
		}
		if calls != want {
			t.Fatalf("read transactions loaded %d times, want %d", calls, want)
		}
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		return read(tx.Reader())
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("write transaction cached a mutable value: calls=%d", calls)
	}
}

func TestReadOnceRetainsReadErrors(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "store.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := errors.New("cannot read")
	calls := 0
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		for i := 0; i < 2; i++ {
			_, err := r.ReadOnce("failed-value", func() (any, error) { calls++; return nil, want })
			if !errors.Is(err, want) {
				t.Fatalf("error=%v", err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("failed read retried %d times in one snapshot", calls)
	}
}
