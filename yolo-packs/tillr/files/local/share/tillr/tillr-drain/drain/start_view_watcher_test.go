package drain

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
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
	defer view.watch(context.Background(), time.Millisecond, nil, func(ctx context.Context, _ startWatchChange) (startSnapshot, error) {
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

func TestStartViewWatcherDocumentAndIndexUpdatesRepairProposals(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("filesystem wakes require inotify")
	}
	for _, updateIndex := range []bool{false, true} {
		name := "editing_design_doc"
		if updateIndex {
			name = "replacing_saved_index"
		}
		t.Run(name, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			doc := "docs/design.md"
			if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(f.root, doc), "# Design\n\nOPEN\n")
			runGit(t, f.root, "add", doc)
			runGit(t, f.root, "commit", "-qm", "design doc")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET design_doc=? WHERE item_id='item-a'`, doc)
				return err
			})
			index := func(state string) string {
				// Keep both payloads the same size so the rename proof does not
				// depend on a changed length or clock resolution.
				return `{"index":{"documents":[{"path":"docs/design.md","questions":[{"id":"OQ-1","state":"` + state + `","title":"Which?"}]}]}}` + strings.Repeat(" ", len("answered")-len(state))
			}
			dir := t.TempDir()
			countPath := filepath.Join(dir, "calls")
			l.cfg.QuestionsIndex = filepath.Join(dir, "index.json")
			l.cfg.QuestionsCmd = []string{"sh", "-c", `echo call >> "$1"; if grep -q ANSWERED docs/design.md; then printf '%s' "$2"; else printf '%s' "$3"; fi`, "index-fixture", countPath, index("answered"), index("open")}
			if updateIndex {
				writeForkFile(t, l.cfg.QuestionsIndex, index("open"))
				l.questions = vantage.Preloaded(f.root, vantage.ParseIndex([]byte(index("open")), "pass fixture"))
			}
			view := newStartView()
			defer l.watchStartView(context.Background(), view, startViewRefreshInterval)()
			awaitStartViewSignal(t, view.updated)
			seed := view.snapshot()
			if seed.holds["item-a"] != ReasonQuestionOpen {
				t.Fatalf("initial holds: %v", seed.holds)
			}
			if updateIndex {
				replacement := filepath.Join(dir, "replacement")
				writeForkFile(t, replacement, index("answered"))
				oldIndex, err := os.Stat(l.cfg.QuestionsIndex)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(replacement, oldIndex.ModTime(), oldIndex.ModTime()); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, l.cfg.QuestionsIndex); err != nil {
					t.Fatal(err)
				}
			} else {
				writeForkFile(t, filepath.Join(f.root, doc), "# Design\n\nANSWERED\n")
			}
			awaitStartViewSignal(t, view.updated)
			next := view.snapshot()
			if next.holds["item-a"] != "" || next.entries["item-a"].decision.RunRequest == nil || next.pending != seed.pending || seed.holds["item-a"] != ReasonQuestionOpen {
				t.Fatalf("refresh failed to repair the proposal without reseeding: %v -> %v", seed.holds, next.holds)
			}
			saved, err := os.ReadFile(l.cfg.QuestionsIndex)
			if err != nil || string(saved) != index("answered") {
				t.Fatalf("saved index: %s, %v", saved, err)
			}
			calls, err := os.ReadFile(countPath)
			if updateIndex {
				if got := decisionFor(t, l, "item-a").decision.Reason; got != ReasonQuestionOpen {
					t.Fatalf("shadow index changed the pass's independent question source: %q", got)
				}
				if !os.IsNotExist(err) {
					t.Fatalf("external index unexpectedly ran the scanner: %s, %v", calls, err)
				}
			} else if err != nil || strings.Count(string(calls), "call") != 2 {
				t.Fatalf("want initial and document-edit scans: %s, %v", calls, err)
			}
		})
	}
}

func TestStartViewWatcherRefreshesBasesWithoutStoreChanges(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("filesystem wakes require inotify")
	}
	f, l := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "next")
	tip := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "update-ref", GreenRef, base)
	view := newStartView()
	defer l.watchStartView(context.Background(), view, startViewRefreshInterval)()
	awaitStartViewSignal(t, view.updated)
	seed := view.snapshot()
	if seed.tip != tip || seed.green != base {
		t.Fatalf("initial bases: %s, %s", seed.tip, seed.green)
	}
	runGit(t, f.root, "update-ref", GreenRef, tip)
	awaitStartViewSignal(t, view.updated)
	next := view.snapshot()
	if next.tip != tip || next.green != tip || next.pending != seed.pending || seed.green != base {
		t.Fatalf("green-only update changed history or missed the base: %s, %s", next.tip, next.green)
	}
}

func TestStartViewPausedRefreshDoesNotBlockLegacyDispatch(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	paused, exited := make(chan struct{}), make(chan struct{})
	view := newStartView()
	l.startView.Store(view)
	seeded := false
	stop := view.watch(context.Background(), time.Millisecond, nil, func(ctx context.Context, _ startWatchChange) (startSnapshot, error) {
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
	if got := view.snapshot(); got.holds["item-a"] != ReasonRunLive || !got.occupied["lane-1"] || got.pending.works["item-a"] == nil {
		t.Fatal("successful legacy dispatch did not publish its claim while refresh was paused")
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
