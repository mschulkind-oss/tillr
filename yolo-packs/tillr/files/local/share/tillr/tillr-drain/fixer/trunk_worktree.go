package fixer

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// CheckCoreWorktreeConfig inspects the shared repository config (not included,
// per-worktree, global, or command-line settings) for core.worktree.
func CheckCoreWorktreeConfig(ctx context.Context, root string) (configPath, worktree string, set bool, err error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", false, err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", absRoot, "--work-tree="+absRoot,
		"config", "--local", "--no-includes", "--show-origin", "--null", "--get", "core.worktree")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() == 1 && len(exit.Stderr) == 0 {
				return "", "", false, nil // Key not set in the shared config.
			}
			return "", "", false, fmt.Errorf("git config in %s: %w: %s", absRoot, err, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", "", false, err
	}
	origin, value, ok := strings.Cut(string(out), "\x00")
	config, file := strings.CutPrefix(origin, "file:")
	wt, terminated := strings.CutSuffix(value, "\x00")
	if !ok || !file || config == "" || !terminated {
		return "", "", false, fmt.Errorf("unexpected git config output for core.worktree in %s", absRoot)
	}
	if !filepath.IsAbs(config) {
		config = filepath.Join(absRoot, config)
	}
	return config, wt, true, nil
}
