package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// A git that is killed while it holds a lane's index.lock leaves the lock behind, and every
// later git write in that lane refuses with "index.lock: File exists": on 2026-11-18 a
// worker's tool timeout killed a git in lane-18 and the zero-byte lock stood for ten hours,
// failing the lane's preservation and its reset. Git holds the lock file open from the
// moment it creates it until it renames it into place, so a lock no process holds open,
// in a lane no process is working in, has no writer left: it is stale. The loop removes
// it and records the removal (staleIndexLockKind), so the act can be read back. A lock a
// live process holds stays, and the failure names that process, which is then the mover.
// Nothing here reads the lock's age: a judgment from a timestamp would be a guess.

// staleIndexLockKind records one stale index.lock the loop removed from a lane.
const staleIndexLockKind = "drain.stale-index-lock-removed"

func init() {
	if err := event.Register(staleIndexLockKind, Utility); err != nil {
		panic(err)
	}
}

// staleIndexLock is the removal's record: the lock as it was found.
type staleIndexLock struct {
	LaneID     string `json:"lane_id"`
	RunID      string `json:"run_id,omitempty"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

// clearStaleIndexLock removes lane's index.lock when no live process holds it or works in
// the lane, and records the removal. A held lock is answered as an error naming its
// holders; an absent lock is nothing.
func (l *Loop) clearStaleIndexLock(ctx context.Context, lane, runID string) error {
	target := l.laneTarget(lane)
	out, err := l.git(ctx, target, "rev-parse", "--path-format=absolute", "--git-path", "index.lock", "--git-dir")
	if err != nil {
		return err
	}
	fields := strings.Split(strings.TrimSpace(out), "\n")
	if len(fields) != 2 {
		return Internalf("reading lane %s's git paths: %q", lane, out)
	}
	path, gitDir := fields[0], fields[1]
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	holders, err := lockHolders(path, []string{target, gitDir})
	if err != nil {
		return Externalf("lane %s has %s, and whether a process holds it cannot be read: %v; remove it once no git runs in the lane", lane, path, err)
	}
	if len(holders) > 0 {
		pids := make([]string, len(holders))
		for i, pid := range holders {
			pids[i] = strconv.Itoa(pid)
		}
		return Externalf("lane %s's %s is held by PID %s; the lane's git writes wait for it to exit", lane, path, strings.Join(pids, ", "))
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	record := staleIndexLock{LaneID: lane, RunID: runID, Path: path, Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339)}
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	id := lane
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: staleIndexLockKind, ObjectType: "lane", ObjectID: &id, Payload: body, At: l.cfg.Now().UTC()})
		return err
	}); err != nil {
		return err
	}
	l.reportf("loop: lane %s: removed stale %s (%d bytes, modified %s): no process held it open or worked in the lane\n", lane, path, record.Size, record.ModifiedAt)
	return nil
}

// lockHolders answers the processes, other than this one, that hold path open or whose
// working directory is inside one of roots. It reads /proc; a system without it cannot
// tell, and says so. A process this user cannot inspect is not this user's git.
func lockHolders(path string, roots []string) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("reading /proc: %w", err)
	}
	resolved := []string{}
	for _, root := range roots {
		if real, err := filepath.EvalSymlinks(root); err == nil {
			resolved = append(resolved, real)
		}
	}
	if real, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		path = filepath.Join(real, filepath.Base(path))
	}
	self := os.Getpid()
	var holders []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		if cwd, err := os.Readlink(filepath.Join(dir, "cwd")); err == nil {
			cwd = strings.TrimSuffix(cwd, " (deleted)")
			if within(cwd, resolved) {
				holders = append(holders, pid)
				continue
			}
		}
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			// Gone since the listing, or not this user's process.
			continue
		}
		for _, fd := range fds {
			if target, err := os.Readlink(filepath.Join(dir, "fd", fd.Name())); err == nil && target == path {
				holders = append(holders, pid)
				break
			}
		}
	}
	return holders, nil
}

func within(path string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
