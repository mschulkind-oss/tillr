package drain

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/synctest"
	"time"
)

func TestStartViewWatcherDocumentAndIndexUpdates(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("filesystem wakes require inotify")
	}
	for _, tc := range []struct {
		name, path string
		change     startWatchChange
		atomic     bool
		newDir     bool
	}{
		{"editing_design_doc_wakes_watcher", "docs/nested/design.md", startWatchExternal, false, false},
		{"editing_referenced_doc_outside_docs_wakes_watcher", "plans/design.md", startWatchExternal, false, false},
		{"replacing_saved_index_wakes_watcher", ".tillr/loop/questions-index.json", startWatchExternal, true, false},
		{"creating_green_ref_wakes_watcher", ".git/refs/tillr/green", startWatchExternal, true, true},
		{"updating_trunk_ref_wakes_watcher", ".git/refs/heads/main", startWatchExternal, true, false},
		{"writing_store_wal_wakes_watcher", ".tillr/store.db-wal", startWatchStore, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, tc.path)
			dir := filepath.Dir(path)
			if tc.newDir {
				dir = filepath.Dir(dir)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if !tc.newDir {
				if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var snapshot startSnapshot
			if tc.path == "plans/design.md" {
				snapshot.itemsByID = map[string]itemRow{"item": {DesignDoc: tc.path}}
			}
			refreshed := make(chan startWatchChange, 8)
			view := newStartView()
			defer view.watch(context.Background(), time.Hour, func(context.Context) startWatcher {
				return newStartWatcher(startWatchPaths{
					root: root, gitDir: filepath.Join(root, ".git"), commonDir: filepath.Join(root, ".git"),
					store: filepath.Join(root, ".tillr/store.db"), index: filepath.Join(root, ".tillr/loop/questions-index.json"),
				})
			}, func(_ context.Context, change startWatchChange) (startSnapshot, error) {
				select {
				case refreshed <- change:
				default:
				}
				return snapshot, nil
			})()
			awaitStartViewSignal(t, view.updated) // registrations precede publication
			if seed := <-refreshed; seed != 0 {
				t.Fatalf("seed reason = %v", seed)
			}
			if tc.atomic {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path+".lock", []byte("after\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path+".lock", path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			select {
			case change := <-refreshed:
				if change&tc.change == 0 {
					t.Fatalf("wake reason = %v, want %v", change, tc.change)
				}
			case <-time.After(hangGuard):
				t.Fatal("edit did not wake refresh with the safety timer out of play")
			}
		})
	}
}

func TestStartViewWatcherTimerOnly(t *testing.T) {
	view := newStartView()
	reads := 0
	defer view.watch(context.Background(), time.Millisecond, func(context.Context) startWatcher { return nil }, func(_ context.Context, change startWatchChange) (startSnapshot, error) {
		if (reads == 0 && change != 0) || (reads != 0 && change != startWatchExternal) {
			return startSnapshot{}, context.Canceled
		}
		reads++
		return startSnapshot{lastEventSeq: int64(reads)}, nil
	})()
	awaitStartViewSignal(t, view.updated)
	for view.snapshot().lastEventSeq < 2 {
		awaitStartViewSignal(t, view.updated)
	}
}

type testStartWatcher chan startWatchChange

func (w testStartWatcher) events() <-chan startWatchChange { return w }
func (testStartWatcher) updateDocs(map[string]itemRow)     {}
func (testStartWatcher) close()                            {}

func TestStartViewWatcherBatchesAndFallsBackOnClosure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := make(testStartWatcher, 2)
		var reads []startWatchChange
		const safety = time.Second
		view := newStartView()
		defer view.watch(context.Background(), safety, func(context.Context) startWatcher { return w }, func(_ context.Context, change startWatchChange) (startSnapshot, error) {
			reads = append(reads, change)
			return startSnapshot{}, nil
		})()
		synctest.Wait()
		w <- startWatchStore
		w <- startWatchExternal
		synctest.Wait()
		if len(reads) != 1 {
			t.Fatalf("refresh before batch was due: %v", reads)
		}
		close(w)
		time.Sleep(startViewWatchBatch)
		synctest.Wait()
		if len(reads) != 2 || reads[1] != startWatchStore|startWatchExternal {
			t.Fatalf("batch did not merge both wakes: %v", reads)
		}
		time.Sleep(safety - startViewWatchBatch)
		synctest.Wait()
		if len(reads) != 3 || reads[2] != startWatchExternal {
			t.Fatalf("safety timer stopped after the filesystem source closed: %v", reads)
		}
	})
}

func TestStartViewWatcherCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	view := newStartView()
	w := newStartWatcher(startWatchPaths{root: t.TempDir()})
	stop := view.watch(ctx, time.Hour, func(context.Context) startWatcher { return w }, func(context.Context, startWatchChange) (startSnapshot, error) {
		return startSnapshot{}, nil
	})
	defer stop()
	awaitStartViewSignal(t, view.updated)
	cancel()
	joined := make(chan struct{})
	go func() { stop(); close(joined) }()
	awaitStartViewSignal(t, joined)
	if w != nil {
		select {
		case _, ok := <-w.events():
			if ok {
				t.Fatal("unexpected filesystem wake")
			}
		default:
			t.Fatal("inotify reader was not joined")
		}
	}
}
