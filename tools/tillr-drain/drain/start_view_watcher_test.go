package drain

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func awaitStartViewSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(hangGuard):
		t.Fatal("timed out waiting for the shadow watcher")
	}
}

func TestStartViewWatcherFilesAndReordersRows(t *testing.T) {
	f, l := passFixture(t, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO lanes (lane_id,state,run_id) VALUES ('lane-1','leased','foreign')`)
		return err
	})
	view := newStartView()
	defer view.watch(context.Background(), time.Millisecond, func(ctx context.Context) (startSnapshot, error) {
		return readStartSnapshot(ctx, f.storePath, l.cfg.Now(), l.currentPolicy())
	})()

	awaitStartViewSignal(t, view.updated)
	seed := view.snapshot()
	if !slices.Equal(seed.items, []startQueueRow{{id: "item-a"}}) || !seed.occupied["lane-1"] {
		t.Fatalf("seed: %+v", seed)
	}
	id := fileFixtureRequest(t, f, "build", item.SingleWorkerWorkflow, []string{"docs/notes.md"})
	awaitStartViewSignal(t, view.updated)
	filed := view.snapshot()
	if len(filed.items) != 2 || filed.items[0].id != id || filed.items[0].rank == "" || filed.items[1].id != "item-a" {
		t.Fatalf("filed rows: %+v", filed.items)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := item.Place(context.Background(), tx, id, item.Destination{After: "item-a"}, l.cfg.Now(), "owner", "cli"); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state='idle', run_id=NULL WHERE lane_id='lane-1'`)
		return err
	})
	awaitStartViewSignal(t, view.updated)
	placed := view.snapshot()
	if len(placed.items) != 2 || placed.items[0].id != "item-a" || placed.items[1].id != id ||
		placed.items[0].rank == "" || placed.items[0].rank >= placed.items[1].rank || placed.occupied["lane-1"] {
		t.Fatalf("after place: %+v", placed)
	}
	if filed.items[0].id != id || !filed.occupied["lane-1"] {
		t.Fatalf("refresh changed an already published snapshot: %+v", filed)
	}
}

func TestStartViewIncrementalUpdatesAndZeroChurn(t *testing.T) {
	f, l := passFixture(t, "item-a")
	id := fileFixtureRequest(t, f, "build", item.SingleWorkerWorkflow, []string{"docs/notes.md"})
	var sync startStoreSync
	t.Cleanup(sync.close)
	ctx, at := context.Background(), l.cfg.Now()
	tick := l.startViewPass()
	questions := &VantageQuestions{Root: f.root}
	var previous startSnapshot
	refresh := func() startSnapshot {
		t.Helper()
		next, err := sync.syncStore(ctx, tick.StorePath, at, l.currentPolicy(), previous, func(r *store.Reader, next *startSnapshot, touched map[string]bool) error {
			return next.repairEntries(ctx, r, at, l.currentPolicy(), tick, questions, previous, touched)
		})
		if err != nil {
			t.Fatal(err)
		}
		previous = next
		return next
	}
	seed := refresh()
	at = at.Add(startViewRefreshInterval)
	if idle := refresh(); idle.pending != seed.pending || idle.lastEventSeq != seed.lastEventSeq {
		t.Fatal("unchanged safety refresh refolded history or advanced the cursor")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		runID := "progress-run"
		_, err := tx.Append(event.Event{Kind: "run.progress", ObjectType: "run", ObjectID: &runID, Payload: []byte(`{}`), At: at})
		return err
	})
	progress := refresh()
	if progress.pending != seed.pending || progress.lastEventSeq <= seed.lastEventSeq {
		t.Fatal("progress did not advance the cursor without refolding history")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Place(ctx, tx, id, item.Destination{After: "item-a"}, at.Add(time.Second), "owner", "test")
		return err
	})
	at = at.Add(time.Second)
	placed := refresh()
	if placed.pending != seed.pending || placed.items[0].id != "item-a" || placed.items[1].id != id || seed.items[0].id != id ||
		!placed.readyAt[id].Equal(seed.readyAt[id]) || placed.contentVersions[id] != seed.contentVersions[id] {
		t.Fatalf("rerank changed cached facts or failed to change order: %+v -> %+v", seed.items, placed.items)
	}
	if entry, ok := placed.nextStart(); !ok || entry.decision.ItemID != "item-a" {
		t.Fatalf("proposal after place: %+v, %v", entry, ok)
	}
}

func TestStartViewPausedRefreshDoesNotBlockLegacyDispatch(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	paused, exited := make(chan struct{}), make(chan struct{})
	view := newStartView()
	seeded := false
	stop := view.watch(context.Background(), time.Millisecond, func(ctx context.Context) (startSnapshot, error) {
		if !seeded {
			seeded = true
			return readStartSnapshot(ctx, f.storePath, l.cfg.Now(), l.currentPolicy())
		}
		close(paused)
		<-ctx.Done()
		close(exited)
		return startSnapshot{}, ctx.Err()
	})
	defer stop()
	awaitStartViewSignal(t, paused)

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	result := &LoopResult{}
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatal(err)
	}
	if result.Dispatched != 1 {
		t.Fatalf("dispatches with refresh paused: %d", result.Dispatched)
	}
	if len(l.active) > 0 {
		select {
		case completion := <-l.completions:
			if _, err := l.takeCompletion(completion, result); err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("the dispatched worker did not complete")
		}
	}
	stop()
	select {
	case <-exited:
	default:
		t.Fatal("watcher stop returned before refresh exited")
	}
}
