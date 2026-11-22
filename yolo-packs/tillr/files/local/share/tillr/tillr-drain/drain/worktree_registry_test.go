package drain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-land/land"
)

// The wrapper checks exclusion at the exact registry command, not whether a
// race happened to hit the brief empty-commondir window on this run.
func registryGuardGit(t *testing.T, root string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := scratchUnderDurable(t)
	script := fmt.Sprintf(`#!/usr/bin/env python3
import fcntl, os, sys
args=sys.argv[1:]
def assert_locked(name, action):
    try:
        with open(os.path.join(os.environ['TILLR_TEST_COMMON'],name),'r') as lock:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
    except BlockingIOError:
        return
    except FileNotFoundError:
        pass
    sys.exit('registry command ran without '+name+': '+action)

if 'worktree' in args:
    action=args[args.index('worktree')+1]
    if action in ('add','remove','prune','list'):
        assert_locked('worktree-registry.lock',action)
    if action in ('add','remove','prune'):
        assert_locked('land.lock',action)
os.execv(%q,[%q]+args)
`, real, real)
	if err := writeForkSafe(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_TEST_COMMON", filepath.Join(root, ".git"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestHeavyRegistryCommandsHoldLandLockButJudgeDoesNot(t *testing.T) {
	root, tip := heavyRepo(t)
	registryGuardGit(t, root)
	runner := heavyFixture(t, root, func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
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
		trees, err := listWorktrees(ctx, root)
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
		return []HeavyCheck{{CheckID: "fixture", Result: "pass"}}, nil
	})
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runner.Work); !os.IsNotExist(err) {
		t.Errorf("checkout was not cleaned: %v", err)
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
	trees, err := listWorktrees(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 1 || trees[0].Path != root {
		t.Fatalf("trees=%+v", trees)
	}
}
