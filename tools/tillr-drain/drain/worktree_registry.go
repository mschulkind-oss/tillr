package drain

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-land/land"
)

// Git publishes a worktree's registration before it finishes writing commondir.
// Coordinate registry commands with lander's existing lock, including its guard
// and stale-holder protocol. Never hold it while a worker or judge runs.
func withWorktreeRegistryLock(ctx context.Context, repo string, fn func() error) error {
	common, err := boundaryGit(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	lock, err := land.OpenLock(filepath.Join(strings.TrimSpace(common), "land.lock"))
	if err != nil {
		return Externalf("opening worktree registry coordination: %v", err)
	}
	if _, err := lock.Acquire(ctx, 0); err != nil {
		return Externalf("waiting for worktree registry coordination: %v", err)
	}
	defer lock.Release()
	return fn()
}
