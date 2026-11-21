// Architecture and invariants: docs/reference/tillr-build-commit.md
package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// continuationStarted reads the durable claim rather than only a terminal flag:
// a continued face may die before run.finish, leaving the first exit as the latest
// terminal record. Identity checks match the accepted resume history in readPending.
func (l *Loop) continuationStarted(ctx context.Context, req run.Request) (bool, error) {
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, readFailure(err)
	}
	defer r.Close()
	started := false
	err = r.ReadTx(ctx, func(reader *store.Reader) error {
		rows, err := reader.Query(`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ?`, run.ResumeKind, req.RunID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var body []byte
			if err := rows.Scan(&body); err != nil {
				return err
			}
			var segment struct {
				RunID     string `json:"run_id"`
				ItemID    string `json:"item_id"`
				LaneID    string `json:"lane_id"`
				Backend   string `json:"backend"`
				SessionID string `json:"session_id"`
				ResumedAt string `json:"resumed_at"`
			}
			if err := json.Unmarshal(body, &segment); err != nil {
				return err
			}
			if segment.RunID == req.RunID && segment.ItemID == req.ItemID && segment.LaneID == req.LaneID && segment.Backend == req.Backend && run.IsSessionID(segment.SessionID) && segment.ResumedAt != "" {
				started = true
			}
		}
		return rows.Err()
	})
	if err != nil {
		return false, readFailure(err)
	}
	return started, nil
}

// uncommittedPaths includes index, worktree and individual untracked files; ignored
// build output is not work to commit. NUL output preserves whitespace in filenames.
func (l *Loop) uncommittedPaths(ctx context.Context, target string) ([]string, error) {
	unstaged, err := l.git(ctx, target, "ls-files", "--modified", "--deleted", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	staged, err := l.git(ctx, target, "diff", "--cached", "--no-renames", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, path := range strings.Split(unstaged+staged, "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

func (l *Loop) fencedUncommittedPaths(ctx context.Context, lane, target string, paths []string) ([]string, error) {
	body, err := os.ReadFile(filepath.Join(l.laneDirPath(lane), FenceFile))
	if err != nil {
		return nil, Externalf("reading the lane's fence before commit continuation: %v", err)
	}
	fence := fenceEntries(string(body))
	prefix := l.fencePrefix(ctx, target)
	fenced := []string{}
	for _, path := range paths {
		if insideFence(prefix+path, fence) {
			fenced = append(fenced, path)
		}
	}
	return fenced, nil
}
