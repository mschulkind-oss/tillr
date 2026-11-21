package drain

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUncommittedPathsIncludesIndexWorktreeAndNestedUntracked(t *testing.T) {
	_, l := passFixture(t, "item-a")
	target := l.laneTarget("lane-1")
	writeForkFile(t, filepath.Join(target, "before.txt"), "tracked\n")
	writeForkFile(t, filepath.Join(target, "deleted.txt"), "tracked\n")
	runGit(t, target, "add", "before.txt", "deleted.txt")
	runGit(t, target, "commit", "-qm", "fixture")
	runGit(t, target, "mv", "before.txt", "after space.txt")
	runGit(t, target, "rm", "deleted.txt")
	if err := os.MkdirAll(filepath.Join(target, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(target, "nested", "new.txt"), "untracked\n")
	writeForkFile(t, filepath.Join(target, "unstaged.txt"), "base\n")
	runGit(t, target, "add", "unstaged.txt")
	writeForkFile(t, filepath.Join(target, "unstaged.txt"), "changed\n")
	paths, err := l.uncommittedPaths(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"after space.txt", "before.txt", "deleted.txt", "nested/new.txt", "unstaged.txt"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths=%q want=%q", paths, want)
	}
}

func TestDirtyExitOutsideLegacyItemPathsContinues(t *testing.T) {
	_, l, original, _ := continuationDispatchFixture(t, "echo work > outside.txt", false)
	steps := &commitContinuationSteps{continuationSteps: original}
	l.cfg.Steps = steps
	var result LoopResult
	settleContinuation(t, l, &result)
	if steps.resumed != 1 {
		t.Fatalf("uncommitted path was not returned to the worker: %+v", result)
	}
}
