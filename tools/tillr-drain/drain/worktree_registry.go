package drain

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-land/land"
)

// Git publishes a worktree's registration before it finishes writing commondir.
// Drain writers keep the lander's existing exclusion, then lock the registry while
// mutating it. A boundary listing needs only the second lock: landing reads the
// registry and moves refs, but does not create or remove registrations. Take land.lock
// first so a writer queued behind a landing never holds up a boundary listing.
func withWorktreeRegistryLock(ctx context.Context, repo string, fn func() error) error {
	return withRegistryLocks(ctx, repo, true, fn)
}

func withWorktreeSnapshotLock(ctx context.Context, repo string, fn func() error) error {
	return withRegistryLocks(ctx, repo, false, fn)
}

func withRegistryLocks(ctx context.Context, repo string, writer bool, fn func() error) error {
	common, err := boundaryGit(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	common = strings.TrimSpace(common)
	if writer {
		unlock, err := acquireRegistryLock(ctx, filepath.Join(common, "land.lock"))
		if err != nil {
			return err
		}
		defer unlock()
	}
	endWait := timeHandoffPhase(ctx, "registry_lock_wait")
	unlock, err := acquireRegistryLock(ctx, filepath.Join(common, "worktree-registry.lock"))
	endWait()
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

func acquireRegistryLock(ctx context.Context, path string) (func(), error) {
	lock, err := land.OpenLock(path)
	if err != nil {
		return nil, Externalf("opening worktree registry coordination: %v", err)
	}
	if _, err := lock.Acquire(ctx, 0); err != nil {
		return nil, Externalf("waiting for worktree registry coordination: %v", err)
	}
	return lock.Release, nil
}
