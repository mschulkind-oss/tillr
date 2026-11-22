package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const trunkCoreWorktreeKind = "drain.trunk-core-worktree"

func init() {
	if err := event.Register(trunkCoreWorktreeKind, Utility); err != nil {
		panic(err)
	}
}

// Observe only the shared repository config, not an included, per-worktree,
// global, or command-line setting. Override the work tree for this read so the
// setting being inspected cannot redirect Git or make it chdir into a stale path.
func (l *Loop) observeTrunkCoreWorktree(ctx context.Context) error {
	if l.nonLanding {
		return nil
	}
	root, err := filepath.Abs(l.cfg.Root)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "--work-tree="+root,
		"config", "--local", "--no-includes", "--show-origin", "--null", "--get", "core.worktree")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() == 1 && len(exit.Stderr) == 0 {
				return nil // Key not set in the shared config.
			}
			return fmt.Errorf("git config in %s: %w: %s", root, err, strings.TrimSpace(string(exit.Stderr)))
		}
		return err
	}
	origin, value, ok := strings.Cut(string(out), "\x00")
	config, file := strings.CutPrefix(origin, "file:")
	worktree, terminated := strings.CutSuffix(value, "\x00")
	if !ok || !file || config == "" || !terminated {
		return fmt.Errorf("unexpected git config output for core.worktree in %s", root)
	}
	if !filepath.IsAbs(config) {
		config = filepath.Join(root, config)
	}
	body, err := json.Marshal(map[string]string{"config_path": config, "worktree": worktree})
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, false)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: trunkCoreWorktreeKind, ObjectType: "repository", ObjectID: &config, Payload: body, At: l.cfg.Now()})
		return err
	})
}
