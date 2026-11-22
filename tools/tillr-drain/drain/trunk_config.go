package drain

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CheckTrunkWorktree refuses an override in the trunk's shared Git config.
// Inspect the file explicitly, outside the checkout: repository discovery
// there would already be trusting the setting we're checking.
func CheckTrunkWorktree(ctx context.Context, root string) error {
	if root == "" {
		return nil
	}
	config, err := filepath.Abs(filepath.Join(root, ".git", "config"))
	if err != nil {
		return Externalf("locating the shared Git config: %v", err)
	}
	// A record-only root (or a linked worktree, which cannot own the trunk)
	// has no shared config here.
	gitDir, err := os.Stat(filepath.Dir(config))
	if errors.Is(err, os.ErrNotExist) || err == nil && !gitDir.IsDir() {
		return nil
	}
	if err != nil {
		return Externalf("locating shared Git config %s: %v", config, err)
	}
	cmd := exec.CommandContext(ctx, "git", "config", "--file", config, "--no-includes", "--get", "core.worktree")
	cmd.Dir = string(os.PathSeparator)
	cmd.Env = boundaryGitEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return Usagef("trunk checkout unsafe: %s sets core.worktree; inspect and remove the override before running the drain", config)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) == 0 {
		return nil // Git found no such key.
	}
	return Externalf("checking core.worktree in %s: %v: %s", config, err, strings.TrimSpace(string(out)))
}
