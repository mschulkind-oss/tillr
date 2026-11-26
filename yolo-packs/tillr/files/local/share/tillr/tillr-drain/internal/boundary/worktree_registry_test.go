package boundary

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr-land/land"
)

func TestHeavyRegistryCommandsHoldLandLockButJudgeDoesNot(t *testing.T) {
	root, tip := heavyRepo(t)
	registryGuardGit(t, root)
	runner := heavyFixture(t, root, func(ctx context.Context, dir, only string) ([]heavyrun.HeavyCheck, error) {
		// A landing can acquire the actual protocol while judging is in flight.
		lock, err := land.OpenLock(filepath.Join(root, ".git", "land.lock"))
		if err != nil {
			return nil, err
		}
		bounded, cancel := context.WithTimeout(ctx, hangGuard)
		defer cancel()
		if _, err := lock.Acquire(bounded, 0); err != nil {
			return nil, err
		}
		lock.Release()
		trees, err := ListWorktrees(ctx, root)
		if err != nil {
			return nil, err
		}
		found := false
		for _, tree := range trees {
			if tree.Path == dir && tree.Head == tip {
				found = true
			}
		}
		if !found {
			t.Errorf("heavy checkout absent from protected listing: %+v", trees)
		}
		return []heavyrun.HeavyCheck{{CheckID: "fixture", Result: "pass"}}, nil
	})
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runner.Work); err != nil {
		t.Errorf("proof worktree should be retained for reuse: %v", err)
	}
}

func TestBoundaryWorktreeListingDoesNotWaitForLanding(t *testing.T) {
	root, _ := heavyRepo(t)
	registryGuardGit(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	lock, err := land.OpenLock(filepath.Join(root, ".git", "land.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Acquire(ctx, 0); err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	trees, err := ListWorktrees(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 1 || trees[0].Path != root {
		t.Fatalf("trees=%+v", trees)
	}
}
