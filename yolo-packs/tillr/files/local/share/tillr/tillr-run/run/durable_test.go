package run_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Each run gets its own durable scratch directory, so two lanes running at once cannot
// clobber each other's scratch even when their git fences are disjoint.
//
// The fence describes tracked paths; the durable directory is gitignored and outside every
// fence, so a lane that inherits the coordinator's `YOLO_DURABLE_DIR` writes the same tree
// the other lane and the loop's own `just tillr-build` write, and no boundary check can see
// it. `--lanes 2` is unsafe until the child's environment carries a per-run path.
//
// The negative control is the behaviour before this landed: `cmd.Env = append(os.Environ(),
// ...)` passed the coordinator's value straight through, so both children answered the
// coordinator's path and every assertion below fails. The two runs are launched
// concurrently because the defect is a shared directory between two live lanes, not a
// property of one run.
func TestEachLaneGetsItsOwnDurableDirectory(t *testing.T) {
	ctx := context.Background()
	dir := scratch(t)
	coordinator := filepath.Join(dir, "coordinator-durable")
	if err := os.MkdirAll(coordinator, 0o755); err != nil {
		t.Fatalf("creating the coordinator's durable directory failed: %v", err)
	}
	// The coordinator's value is set in this process's environment, which is exactly where
	// the child inherits it from: leaving it in the environment is the bug, and the case
	// only means something if the value it must not inherit is really there.
	t.Setenv("YOLO_DURABLE_DIR", coordinator)

	s, err := store.InitFixture(ctx, filepath.Join(dir, "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	laneRoot := filepath.Join(dir, "lanes")
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

	lanes := []string{"lane-1", "lane-2"}
	seen := make([]string, len(lanes))
	failures := make([]error, len(lanes))
	var wg sync.WaitGroup
	for i, lane := range lanes {
		wg.Add(1)
		go func(i int, lane string) {
			defer wg.Done()
			runID := "run-" + lane
			_, failures[i] = run.Exec(ctx, s, run.Request{
				RunID: runID, LaneID: lane, Backend: "pi",
				// The child reports the value it actually received, in the log exec already
				// keeps for it: asking the process is the only answer that cannot be a
				// restatement of what exec meant to do.
				Command:    []string{"/bin/sh", "-c", `printf '%s' "$YOLO_DURABLE_DIR"`},
				WorkingDir: run.WorktreeDir, TimeoutMS: 60000,
			}, run.ExecOptions{LaneRoot: laneRoot, Now: now})
			if failures[i] != nil {
				return
			}
			body, err := os.ReadFile(filepath.Join(laneRoot, lane, runID+".log"))
			if err != nil {
				failures[i] = err
				return
			}
			seen[i] = string(body)
		}(i, lane)
	}
	wg.Wait()

	for i, lane := range lanes {
		if failures[i] != nil {
			t.Fatalf("the %s run did not finish: %v", lane, failures[i])
		}
		want := filepath.Join(laneRoot, lane, "durable", "run-"+lane)
		if seen[i] != want {
			t.Fatalf("the %s run's YOLO_DURABLE_DIR is %q, want its own per-run path %q", lane, seen[i], want)
		}
		if seen[i] == coordinator {
			t.Fatalf("the %s run inherited the coordinator's durable directory %q: two lanes would write one scratch tree", lane, coordinator)
		}
		// It is the lane's own tree, and it is outside the worktree: a scratch directory
		// inside the tree would be a change the fence has to admit.
		laneDir := filepath.Join(laneRoot, lane)
		if !strings.HasPrefix(seen[i], laneDir+string(filepath.Separator)) {
			t.Fatalf("the %s run's scratch %q is outside its lane directory %q", lane, seen[i], laneDir)
		}
		if strings.HasPrefix(seen[i], filepath.Join(laneDir, run.WorktreeDir)+string(filepath.Separator)) {
			t.Fatalf("the %s run's scratch %q is inside its worktree, where the fence would have to admit it", lane, seen[i])
		}
		info, err := os.Stat(seen[i])
		if err != nil {
			t.Fatalf("the %s run's scratch %q does not exist: %v", lane, seen[i], err)
		}
		if !info.IsDir() {
			t.Fatalf("the %s run's scratch %q is not a directory", lane, seen[i])
		}
	}
	if seen[0] == seen[1] {
		t.Fatalf("both lanes were handed the durable directory %q: a shared scratch tree is what this case exists to prevent", seen[0])
	}
}
