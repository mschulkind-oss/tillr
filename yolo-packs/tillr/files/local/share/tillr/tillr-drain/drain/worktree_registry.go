package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
)

// Git publishes a worktree's registration before it finishes writing commondir.
// Drain writers keep the lander's existing exclusion, then lock the registry while
// mutating it. A boundary listing needs only the second lock: landing reads the
// registry and moves refs, but does not create or remove registrations. Take land.lock
// first so a writer queued behind a landing never holds up a boundary listing.
func withWorktreeRegistryLock(ctx context.Context, repo string, fn func() error) error {
	if err := authorizeWorktreeRoot(repo); err != nil {
		return err
	}
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
		endLandWait := timeHandoffPhase(ctx, "land_lock_wait")
		unlock, err := acquireRegistryLock(ctx, filepath.Join(common, "land.lock"))
		endLandWait()
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

// unlockRegisteredWorktree runs under the worktree registry lock. Git can still
// hold a locked registration (for example an `initializing` lock left when a
// previous `git worktree add` was killed mid-checkout) even when the checkout's
// directory or `.git` file is gone, so find it by the administrative directory's
// backpointer rather than through the checkout.
func unlockRegisteredWorktree(ctx context.Context, repo, common, work string) (string, time.Time, bool, error) {
	if common == "" {
		resolved, err := boundaryGit(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return "", time.Time{}, false, err
		}
		common = strings.TrimSpace(resolved)
	}
	absWork, err := filepath.Abs(work)
	if err != nil {
		return "", time.Time{}, false, err
	}
	registry := filepath.Join(common, "worktrees")
	entries, err := os.ReadDir(registry)
	if os.IsNotExist(err) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	target := filepath.Join(absWork, ".git")
	canonical := target
	if parent, err := filepath.EvalSymlinks(filepath.Dir(absWork)); err == nil {
		canonical = filepath.Join(parent, filepath.Base(absWork), ".git")
	} else if !os.IsNotExist(err) {
		return "", time.Time{}, false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		admin := filepath.Join(registry, entry.Name())
		lock := filepath.Join(admin, "locked")
		info, statErr := os.Stat(lock)
		if os.IsNotExist(statErr) {
			continue
		}
		backpointer, readErr := os.ReadFile(filepath.Join(admin, "gitdir"))
		if os.IsNotExist(readErr) {
			continue // An incomplete registration without a path cannot name this checkout.
		}
		if readErr != nil {
			return "", time.Time{}, false, readErr
		}
		gitfile := strings.TrimSuffix(string(backpointer), "\n")
		if !filepath.IsAbs(gitfile) {
			gitfile = filepath.Join(admin, gitfile)
		}
		path := filepath.Clean(gitfile)
		if path != canonical && path != target {
			continue
		}
		if statErr != nil {
			return "", time.Time{}, false, statErr
		}
		reason, err := os.ReadFile(lock)
		if err != nil {
			return "", time.Time{}, false, err
		}
		if _, err := heavyGitOutput(ctx, repo, "worktree", "unlock", filepath.Dir(path)); err != nil {
			return "", time.Time{}, false, err
		}
		return strings.TrimSpace(string(reason)), info.ModTime(), true, nil
	}
	return "", time.Time{}, false, nil
}
