package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func seedPendingHistoryFixture(tb testing.TB, path string, items int) {
	tb.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, path, true)
	if err != nil {
		tb.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		for i := range items {
			itemID := fmt.Sprintf("item-hist-%04d", i)
			runID := fmt.Sprintf("run-hist-%04d-a1", i)
			work := pendingWork{
				Decision: Decision{
					ItemID: itemID,
					LaneID: fmt.Sprintf("lane-%d", (i%10)+1),
					RunRequest: &run.Request{
						RunID:  runID,
						ItemID: itemID,
						LaneID: fmt.Sprintf("lane-%d", (i%10)+1),
					},
				},
				BaseSHA: "0123456789abcdef0123456789abcdef01234567",
			}
			if err := appendEffect(tx, dispatchStartedKind, runID, work, now); err != nil {
				return err
			}
			cand := Candidate{
				RunID:   runID,
				ItemID:  itemID,
				LaneID:  work.Decision.LaneID,
				HeadSHA: "fedcba9876543210fedcba9876543210fedcba98",
			}
			if err := appendEffect(tx, candidateReadyKind, runID, cand, now); err != nil {
				return err
			}
			if err := appendEffect(tx, candidateGatedKind, runID, cand, now); err != nil {
				return err
			}
			if i < items-1 {
				if err := appendEffect(tx, examinationDoneKind, runID, map[string]bool{"done": true}, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
}

func TestPendingHistoryCacheSkipsUnchangedHistoryAndExtendsIncrementally(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	const itemCount = 200
	seedPendingHistoryFixture(t, path, itemCount)
	lastRunID := fmt.Sprintf("run-hist-%04d-a1", itemCount-1)
	lastItemID := fmt.Sprintf("item-hist-%04d", itemCount-1)

	ro, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	// 1. Cold read folds the seeded pending-history events once.
	before := pendingEventsFolded.Load()
	var snap1 *pendingHistoryState
	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		snap1, err = readPendingHistoryState(r)
		if err != nil {
			return err
		}
		// Second call inside the same ReadTx reuses ReadOnce.
		snap1b, err := readPendingHistoryState(r)
		if err != nil {
			return err
		}
		if snap1b != snap1 {
			t.Fatalf("second call in same ReadTx returned different pointer")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	coldFolded := pendingEventsFolded.Load() - before
	if want := int64(itemCount*4 - 1); coldFolded != want {
		t.Fatalf("cold fold processed %d events, want %d", coldFolded, want)
	}
	if snap1.byRun[lastRunID].Done {
		t.Fatalf("expected %s to be pending before completion", lastRunID)
	}

	// 2. Subsequent ReadTxs with unchanged history fold zero events.
	before = pendingEventsFolded.Load()
	for range 10 {
		if err := ro.ReadTx(ctx, func(r *store.Reader) error {
			got, err := readPendingHistoryState(r)
			if err != nil {
				return err
			}
			if got != snap1 {
				t.Fatalf("unchanged ReadTx rebuilt cached state")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if folded := pendingEventsFolded.Load() - before; folded != 0 {
		t.Fatalf("10 unchanged ReadTxs folded %d events, want 0", folded)
	}

	// 3. A non-pending event advances EventHeadSeq without rebuilding state.
	rw, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer rw.Close()
	if err := rw.WriteTx(ctx, func(tx *store.Tx) error {
		obj := lastRunID
		_, err := tx.Append(event.Event{
			At:         time.Now().UTC(),
			Kind:       "run.progress",
			ObjectType: "run",
			ObjectID:   &obj,
			Payload:    json.RawMessage(`{"phase":"running"}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before = pendingEventsFolded.Load()
	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		got, err := readPendingHistoryState(r)
		if err != nil {
			return err
		}
		if got != snap1 {
			t.Fatalf("non-pending event rebuilt cached state")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if folded := pendingEventsFolded.Load() - before; folded != 0 {
		t.Fatalf("read after non-pending event folded %d events, want 0", folded)
	}

	// 4. An uncommitted WriteTx that rolls back must see its own event without
	//    polluting the committed cache.
	rollbackErr := fmt.Errorf("rollback")
	if err := rw.WriteTx(ctx, func(tx *store.Tx) error {
		if err := appendEffect(tx, examinationDoneKind, lastRunID, map[string]bool{"done": true}, time.Now().UTC()); err != nil {
			return err
		}
		got, err := readPendingHistoryState(tx.Reader())
		if err != nil {
			return err
		}
		if !got.byRun[lastRunID].Done {
			t.Fatalf("WriteTx reader did not observe its own uncommitted event")
		}
		return rollbackErr
	}); err != rollbackErr {
		t.Fatalf("WriteTx err = %v, want rollbackErr", err)
	}
	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		got, err := readPendingHistoryState(r)
		if err != nil {
			return err
		}
		if got.byRun[lastRunID].Done {
			t.Fatalf("rolled-back event leaked into committed cache")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 5. Committing one new pending-history event folds only that 1 event via COW,
	//    leaving snap1 completely untouched for any concurrent reader.
	if err := rw.WriteTx(ctx, func(tx *store.Tx) error {
		return appendEffect(tx, examinationDoneKind, lastRunID, map[string]bool{"done": true}, time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
	before = pendingEventsFolded.Load()
	var snap2 *pendingHistoryState
	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		snap2, err = readPendingHistoryState(r)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if folded := pendingEventsFolded.Load() - before; folded != 1 {
		t.Fatalf("incremental update folded %d events, want 1", folded)
	}
	if !snap2.byRun[lastRunID].Done || !snap2.works[lastItemID].Done {
		t.Fatalf("incremental update did not mark %s done", lastRunID)
	}
	if snap1.byRun[lastRunID].Done || snap1.works[lastItemID].Done {
		t.Fatalf("incremental update mutated prior immutable snapshot")
	}

	// 6. Concurrent readers during incremental updates must remain race-free.
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for range 10 {
				_ = ro.ReadTx(ctx, func(r *store.Reader) error {
					st, err := readPendingHistoryState(r)
					if err != nil {
						return err
					}
					_ = len(st.works)
					_ = st.byRun[lastRunID].Done
					return nil
				})
			}
		}(g)
	}
	for i := range 5 {
		if err := rw.WriteTx(ctx, func(tx *store.Tx) error {
			runID := fmt.Sprintf("run-conc-%d", i)
			itemID := fmt.Sprintf("item-conc-%d", i)
			work := pendingWork{
				Decision: Decision{
					ItemID:     itemID,
					LaneID:     "lane-1",
					RunRequest: &run.Request{RunID: runID, ItemID: itemID, LaneID: "lane-1"},
				},
			}
			return appendEffect(tx, dispatchStartedKind, runID, work, time.Now().UTC())
		}); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()

	// 7. Replacing the database file at the same path invalidates the cache.
	ro.Close()
	rw.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
	seedPendingHistoryFixture(t, path, 3)
	ro2, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro2.Close()
	if err := ro2.ReadTx(ctx, func(r *store.Reader) error {
		got, err := readPendingHistoryState(r)
		if err != nil {
			return err
		}
		if len(got.works) != 3 {
			t.Fatalf("replaced store had %d works, want 3", len(got.works))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkReadPendingHistoryState(b *testing.B) {
	ctx := context.Background()
	path := filepath.Join(b.TempDir(), "bench.sqlite3")
	seedPendingHistoryFixture(b, path, 1000)

	ro, err := store.OpenRead(ctx, path)
	if err != nil {
		b.Fatal(err)
	}
	defer ro.Close()

	// Warm the cache once before benchmarking steady-state reads.
	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		_, err := readPendingHistoryState(r)
		return err
	}); err != nil {
		b.Fatal(err)
	}

	b.Run("uncached_full_fold", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := ro.ReadTx(ctx, func(r *store.Reader) error {
				return foldPendingHistory(r, 0, newPendingHistoryState(), nil)
			}); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("cached_steady_readtx", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := ro.ReadTx(ctx, func(r *store.Reader) error {
				_, err := readPendingHistoryState(r)
				return err
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
