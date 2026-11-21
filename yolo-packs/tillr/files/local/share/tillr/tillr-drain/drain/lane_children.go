package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// recheckLaneChildren reads the process table once for all ended runs. Store release
// is independent of rotation: an idle lane must not be reused while an old run's
// marked processes can still write there. This also recovers holds after a crash
// between release and recording the refusal, and after a supervisor restart.
func (l *Loop) recheckLaneChildren(ctx context.Context) error {
	if err := l.loadLaneConditions(ctx); err != nil {
		return err
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	ended := map[string]string{}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		// endedAt is the seq of the latest record that ended each run: its runs row's
		// finish event, or a release. A run.resume after it is a continuation of the same
		// run that has not ended yet (tillr-run claimLane), so that run is live again.
		endedAt := map[string]int64{}
		rows, err := r.Query(`SELECT run_id, lane_id, event_seq FROM runs UNION SELECT run_id, lane_id, 0 FROM read_only_runs WHERE settled=1`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, lane string
			var seq int64
			if err := rows.Scan(&id, &lane, &seq); err != nil {
				return err
			}
			ended[id] = lane
			endedAt[id] = max(endedAt[id], seq)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		// Legacy leases can be released without a terminal row. The recorded
		// release still binds their run to its lane, including after a restart.
		releases, err := r.Query(`SELECT seq, kind, object_id, payload FROM events WHERE kind IN (?, ?) ORDER BY seq`, run.ReleaseKind, laneReleasedKind)
		if err != nil {
			return err
		}
		defer releases.Close()
		for releases.Next() {
			var seq int64
			var kind, id string
			var raw []byte
			if err := releases.Scan(&seq, &kind, &id, &raw); err != nil {
				return err
			}
			if kind == run.ReleaseKind {
				var release run.Release
				if err := json.Unmarshal(raw, &release); err != nil {
					return err
				}
				if release.Result == run.ReleaseReleased && release.RunID != "" {
					ended[release.RunID] = release.LaneID
					endedAt[release.RunID] = max(endedAt[release.RunID], seq)
				}
			} else {
				var release laneReleased
				if err := json.Unmarshal(raw, &release); err != nil {
					return err
				}
				ended[id] = release.LaneID
				endedAt[id] = max(endedAt[id], seq)
			}
		}
		if err := releases.Err(); err != nil {
			return err
		}
		releases.Close()
		resumes, err := r.Query(`SELECT object_id, MAX(seq) FROM events WHERE kind = ? AND object_type = 'run' GROUP BY object_id`, run.ResumeKind)
		if err != nil {
			return err
		}
		defer resumes.Close()
		for resumes.Next() {
			var id string
			var seq int64
			if err := resumes.Scan(&id, &seq); err != nil {
				return err
			}
			if _, ok := ended[id]; ok && seq > endedAt[id] {
				delete(ended, id)
			}
		}
		return resumes.Err()
	})
	s.Close()
	if err != nil {
		return err
	}
	here, err := currentLoopOwner()
	if err != nil {
		return err
	}
	marked, err := liveRunWorkers(l.cfg.Store, "", here)
	if err != nil {
		return err
	}
	pids := map[string][]int{}
	for _, p := range marked {
		lane, ok := ended[p.runID]
		if !ok {
			continue
		}
		inside, err := l.processInLane(p, lane)
		if err != nil {
			return err
		}
		if inside {
			pids[lane] = append(pids[lane], p.PID)
		}
	}
	for _, lane := range l.cfg.Lanes {
		if len(pids[lane]) > 0 {
			if err := l.holdLaneChildren(ctx, lane, pids[lane]); err != nil {
				return err
			}
		} else if l.hasLaneCondition(lane, ReasonLaneChildren) {
			if err := l.restoreLane(ctx, lane); err != nil {
				return err
			}
		}
	}
	return nil
}

// Resolve symlinks and compare path components, not string prefixes: lane-1-other
// is not lane-1. Revalidate start time after reading cwd to reject PID reuse.
func (l *Loop) processInLane(p *runFaceOwner, lane string) (bool, error) {
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", p.PID))
	if processGone(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	root, err := filepath.EvalSymlinks(filepath.Join(l.cfg.LaneRoot, lane))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return false, err
	}
	// Linux appends this suffix when the working directory has been unlinked.
	cwd = strings.TrimSuffix(cwd, " (deleted)")
	rel, err := filepath.Rel(root, cwd)
	if err != nil {
		return false, err
	}
	start, err := processStart(p.PID)
	if processGone(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return start == p.Start && start != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

func holdLaneChildrenRefusal(lane string, pids []int) *LaneRefusalError {
	sort.Ints(pids)
	ids := make([]string, len(pids))
	for i, pid := range pids {
		ids[i] = strconv.Itoa(pid)
	}
	return asLaneRefusal(laneRefusalf(lane, ReasonLaneChildren,
		"wait for these processes to exit, or end them with `kill "+strings.Join(ids, " ")+"`; the next pass rechecks the lane",
		"ended runs still have live marked processes in lane %s: PIDs %s", lane, strings.Join(ids, ", ")))
}

func (l *Loop) holdLaneChildren(ctx context.Context, lane string, pids []int) error {
	return l.takeLaneOutOfRotation(ctx, holdLaneChildrenRefusal(lane, pids))
}
