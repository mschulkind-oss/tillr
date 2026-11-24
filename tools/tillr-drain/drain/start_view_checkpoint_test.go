package drain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const startIndexCheckpointStore = "TILLR_TEST_START_INDEX_CHECKPOINT_STORE"

func TestStartIndexCheckpointHelper(t *testing.T) {
	path := os.Getenv(startIndexCheckpointStore)
	if path == "" {
		return
	}
	ctx := context.Background()
	owner, err := store.OwnCheckpoints(ctx, path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cycle, err := owner.Checkpoint(ctx)
	if err != nil || !cycle.Restarted {
		fmt.Fprintf(os.Stderr, "checkpoint during question indexing did not restart: %+v, %v\n", cycle, err)
		os.Exit(1)
	}
	fmt.Println(`{"index":{"documents":[{"path":"docs/design.md","questions":[{"id":"OQ-1","state":"open","title":"Which?"}]}]}}`)
	os.Exit(0) // The external scanner's stdout must contain only the index.
}

func TestStartViewReleasesStoreBeforeIndexingNewDesignDocument(t *testing.T) {
	for _, seed := range []bool{true, false} {
		name := "new_document"
		if seed {
			name = "initial_snapshot"
		}
		t.Run(name, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			t.Setenv(startIndexCheckpointStore, f.storePath)
			ctx := context.Background()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := []string{exe, "-test.run=^TestStartIndexCheckpointHelper$"}
			var refresh startRefresh
			t.Cleanup(refresh.store.close)
			read := func(prev startSnapshot) startSnapshot {
				t.Helper()
				next, err := refresh.read(ctx, l.cfg.Now(), l.currentPolicy(), l.startViewPass(), f.root, command, "", prev, startWatchStore)
				if err != nil {
					t.Fatal(err)
				}
				return next
			}
			var prev startSnapshot
			if !seed {
				prev = read(prev)
			}
			doc := "docs/design.md"
			if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(f.root, doc), "# Design\n")
			runGit(t, f.root, "add", doc)
			runGit(t, f.root, "commit", "-qm", "design doc")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET design_doc=? WHERE item_id='item-a'`, doc)
				return err
			})
			next := read(prev)
			if _, err := refresh.docs.questions.Document(doc); err != nil {
				t.Fatal(err)
			}
			if got := next.holds["item-a"]; got != ReasonQuestionOpen {
				t.Fatalf("new document's open question did not hold the proposal: %q", got)
			}
		})
	}
}

const (
	startSyncShimRealGitEnv    = "TILLR_TEST_START_SYNC_REAL_GIT"
	startSyncShimReadMarkerEnv = "TILLR_TEST_START_SYNC_READ_MARKER"
	startSyncShimStoreEnv      = "TILLR_TEST_START_SYNC_STORE"
	startSyncShimLogEnv        = "TILLR_TEST_START_SYNC_GIT_LOG"
	startSyncShimOnceEnv       = "TILLR_TEST_START_SYNC_CHECKPOINT_ONCE"
)

func TestStartSyncGitShimHelper(t *testing.T) {
	realGit := os.Getenv(startSyncShimRealGitEnv)
	if realGit == "" {
		return
	}
	var gitArgs []string
	for i, arg := range os.Args {
		if arg == "--" {
			gitArgs = os.Args[i+1:]
			break
		}
	}
	if marker := os.Getenv(startSyncShimReadMarkerEnv); marker != "" {
		if _, err := os.Stat(marker); err == nil {
			fmt.Fprintf(os.Stderr, "subprocess git %v started while start sync ReadTx is open\n", gitArgs)
			os.Exit(1)
		}
	}
	if logPath := os.Getenv(startSyncShimLogEnv); logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			_, _ = fmt.Fprintln(f, strings.Join(gitArgs, " "))
			_ = f.Close()
		}
	}
	if oncePath := os.Getenv(startSyncShimOnceEnv); oncePath != "" {
		if f, err := os.OpenFile(oncePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
			_ = f.Close()
			if storePath := os.Getenv(startSyncShimStoreEnv); storePath != "" {
				origStderr := os.Stderr
				devNull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
				if devNull != nil {
					os.Stderr = devNull
				}
				ctx := context.Background()
				owner, err := store.OwnCheckpoints(ctx, storePath)
				var restarted bool
				var cycleSummary string
				if err == nil {
					cycle, cpErr := owner.Checkpoint(ctx)
					restarted = cycle.Restarted
					cycleSummary = fmt.Sprintf("%+v", cycle)
					_, _ = owner.Close(ctx)
					err = cpErr
				}
				os.Stderr = origStderr
				if devNull != nil {
					_ = devNull.Close()
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "owning/running checkpoint during subprocess: %v\n", err)
					os.Exit(1)
				}
				if !restarted {
					fmt.Fprintf(os.Stderr, "checkpoint during start sync subprocess did not restart: %s\n", cycleSummary)
					os.Exit(1)
				}
			}
		}
	}
	cmd := exec.Command(realGit, gitArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

type startSyncSubprocessGuard struct {
	dir        string
	readMarker string
	gitLog     string
	onceFile   string

	mu         sync.Mutex
	violations []string
}

func installStartSyncSubprocessGuard(t *testing.T, s *startStoreSync, storePath string) *startSyncSubprocessGuard {
	t.Helper()
	realGit := os.Getenv(startSyncShimRealGitEnv)
	if realGit == "" {
		var err error
		realGit, err = exec.LookPath("git")
		if err != nil {
			t.Fatalf("locating git: %v", err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locating test executable: %v", err)
	}
	dir := scratchUnderDurable(t)
	g := &startSyncSubprocessGuard{
		dir:        dir,
		readMarker: filepath.Join(dir, "read-tx-open"),
		gitLog:     filepath.Join(dir, "git.log"),
		onceFile:   filepath.Join(dir, "checkpoint-once"),
	}
	shim := filepath.Join(dir, "git")
	script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestStartSyncGitShimHelper$ -- \"$@\"\n", exe)
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatalf("writing git shim: %v", err)
	}
	t.Setenv(startSyncShimRealGitEnv, realGit)
	t.Setenv(startSyncShimReadMarkerEnv, g.readMarker)
	t.Setenv(startSyncShimStoreEnv, storePath)
	t.Setenv(startSyncShimLogEnv, g.gitLog)
	t.Setenv(startSyncShimOnceEnv, g.onceFile)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var (
		activeMu   sync.Mutex
		forkLocked bool
		stopCh     chan struct{}
		doneCh     chan struct{}
	)
	s.onReadTx = func(open bool) {
		activeMu.Lock()
		defer activeMu.Unlock()
		if open {
			if err := os.WriteFile(g.readMarker, []byte("open\n"), 0o644); err != nil {
				t.Errorf("writing read marker: %v", err)
			}
			syscall.ForkLock.RLock()
			forkLocked = true
			stop, done := make(chan struct{}), make(chan struct{})
			stopCh, doneCh = stop, done
			go func() {
				defer close(done)
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-stop:
						return
					case <-ticker.C:
						buf := make([]byte, 1<<20)
						n := runtime.Stack(buf, true)
						stacks := string(buf[:n])
						if strings.Contains(stacks, "syscall.acquireForkLock") {
							g.mu.Lock()
							g.violations = append(g.violations, stacks)
							g.mu.Unlock()
							activeMu.Lock()
							if forkLocked {
								syscall.ForkLock.RUnlock()
								forkLocked = false
							}
							activeMu.Unlock()
							return
						}
					}
				}
			}()
			return
		}
		if stopCh != nil {
			close(stopCh)
			activeMu.Unlock()
			<-doneCh
			activeMu.Lock()
			stopCh, doneCh = nil, nil
		}
		_ = os.Remove(g.readMarker)
		if forkLocked {
			syscall.ForkLock.RUnlock()
			forkLocked = false
		}
	}
	return g
}

func (g *startSyncSubprocessGuard) resetCycle() {
	_ = os.Remove(g.gitLog)
	_ = os.Remove(g.onceFile)
	g.mu.Lock()
	g.violations = nil
	g.mu.Unlock()
}

func (g *startSyncSubprocessGuard) assertClean(t *testing.T, phase string, wantSubstrings ...string) {
	t.Helper()
	g.mu.Lock()
	violations := append([]string(nil), g.violations...)
	g.mu.Unlock()
	if len(violations) > 0 {
		t.Fatalf("%s started a subprocess while startStoreSync's read transaction was open:\n%s", phase, violations[0])
	}
	raw, err := os.ReadFile(g.gitLog)
	if err != nil {
		t.Fatalf("%s recorded no git subprocesses after ReadTx closed: %v", phase, err)
	}
	logged := string(raw)
	for _, want := range wantSubstrings {
		if !strings.Contains(logged, want) {
			t.Fatalf("%s git log missing %q; got:\n%s", phase, want, logged)
		}
	}
}

func TestStartSyncReleasesStoreBeforeSubprocesses(t *testing.T) {
	f, l := passFixture(t, "item-blocker", "item-a")
	ctx := context.Background()
	trunkSHA := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, trunkSHA)

	wfDir := filepath.Join(f.root, "config", "tillr", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfRel := filepath.Join("config", "tillr", "workflows", "check@v1.json")
	writeForkFile(t, filepath.Join(f.root, wfRel), `{"version":1,"stages":[{"id":"work","seat":"build","access":"write"}],"edges":[],"exit":{"requires_nodes":["work"]}}`+"\n")
	if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "docs", "queue.md"), "# Queue\n")
	runGit(t, f.root, "add", wfRel, "docs/queue.md")
	runGit(t, f.root, "commit", "-qm", "seed workflow and docs")
	trunkSHA = runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, trunkSHA)
	l.cfg.WorkflowDefaults = map[string]string{"build": "check@v1"}

	at := l.cfg.Now()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='item-blocker'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO item_edges VALUES ('item-a','item-blocker')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES('land-blocker','item-blocker','run-blocker','verdict-blocker','landed',?,?,?,?,1)`, trunkSHA, trunkSHA, trunkSHA, trunkSHA)
		return err
	})

	var refresh startRefresh
	t.Cleanup(refresh.store.close)
	guard := installStartSyncSubprocessGuard(t, &refresh.store, f.storePath)

	// Phase 1: initial refresh exercises refreshTrunk inside startRefresh.read's sync callback
	// as well as repairEntriesAt (documentPrerequisites, unprovedBlockers, selectWorkflow).
	guard.resetCycle()
	seed, err := refresh.read(ctx, at, l.currentPolicy(), l.startViewPass(), f.root, nil, "", startSnapshot{}, startWatchStore)
	if err != nil {
		t.Fatalf("initial refresh.read failed: %v", err)
	}
	guard.assertClean(t, "initial refreshTrunk + repairEntriesAt", "rev-parse", "merge-base --is-ancestor", "ls-files")

	// Phase 2: incremental store wake (prev.pending != nil, definition unchanged) skips refreshTrunk
	// and runs only repairEntriesAt with changed item content.
	at = at.Add(time.Second)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET title='updated title', updated_at=? WHERE item_id='item-a'`, at.UnixMilli()); err != nil {
			return err
		}
		id := "item-a"
		_, err := tx.Append(event.Event{Kind: "item.update", ObjectType: "item", ObjectID: &id, At: at, Payload: []byte(`{"title":"updated title"}`)})
		return err
	})
	guard.resetCycle()
	next, err := refresh.read(ctx, at, l.currentPolicy(), l.startViewPass(), f.root, nil, "", seed, startWatchStore)
	if err != nil {
		t.Fatalf("incremental repairEntriesAt refresh.read failed: %v", err)
	}
	_ = next
	guard.assertClean(t, "incremental repairEntriesAt", "ls-files", "merge-base --is-ancestor")

	// Phase 3: direct syncStore + prepareRepair (bases == nil) exercises refreshTrunk inside repairEntriesAt.
	var direct startStoreSync
	t.Cleanup(direct.close)
	directGuard := installStartSyncSubprocessGuard(t, &direct, f.storePath)
	directGuard.resetCycle()
	tick := l.startViewPass()
	questions := &VantageQuestions{Root: f.root}
	if _, err := direct.syncStore(ctx, f.storePath, at, l.currentPolicy(), startSnapshot{}, func(r *store.Reader, s *startSnapshot, touched map[string]bool) (func() error, error) {
		return s.prepareRepair(ctx, r, at, l.currentPolicy(), tick, questions, startSnapshot{}, touched)
	}); err != nil {
		t.Fatalf("direct syncStore + prepareRepair failed: %v", err)
	}
	directGuard.assertClean(t, "direct syncStore + prepareRepair", "rev-parse", "ls-files", "merge-base --is-ancestor")

	// Phase 4: positive control proving the ForkLock guard catches any subprocess (even non-git)
	// started while startStoreSync's read transaction is open.
	directGuard.resetCycle()
	if _, err := direct.syncStoreWithRefresh(ctx, f.storePath, at, l.currentPolicy(), startSnapshot{}, true, nil, func(*store.Reader, *startSnapshot, map[string]bool) (func() error, error) {
		return nil, exec.Command("/bin/sh", "-c", ":").Run()
	}); err != nil {
		t.Fatalf("positive-control subprocess failed to run after guard release: %v", err)
	}
	directGuard.mu.Lock()
	caught := len(directGuard.violations)
	directGuard.mu.Unlock()
	if caught == 0 {
		t.Fatal("startSyncSubprocessGuard did not catch a subprocess started inside ReadTx")
	}
}
