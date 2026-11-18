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
	if l.laneConditions == nil {
		l.laneConditions = map[string]*laneCondition{}
	}
	if !l.laneConditionsLoaded {
		if err := l.loadLaneConditions(ctx); err != nil {
			return err
		}
		l.laneConditionsLoaded = true
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	ended := map[string]string{}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT run_id, lane_id FROM runs UNION SELECT run_id, lane_id FROM read_only_runs WHERE settled=1`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, lane string
			if err := rows.Scan(&id, &lane); err != nil {
				return err
			}
			ended[id] = lane
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		// Legacy leases can be released without a terminal row. The recorded
		// release still binds their run to its lane, including after a restart.
		releases, err := r.Query(`SELECT kind, object_id, payload FROM events WHERE kind IN (?, ?) ORDER BY seq`, run.ReleaseKind, laneReleasedKind)
		if err != nil {
			return err
		}
		defer releases.Close()
		for releases.Next() {
			var kind, id string
			var raw []byte
			if err := releases.Scan(&kind, &id, &raw); err != nil {
				return err
			}
			if kind == run.ReleaseKind {
				var release run.Release
				if err := json.Unmarshal(raw, &release); err != nil {
					return err
				}
				if release.Result == run.ReleaseReleased && release.RunID != "" {
					ended[release.RunID] = release.LaneID
				}
			} else {
				var release laneReleased
				if err := json.Unmarshal(raw, &release); err != nil {
					return err
				}
				ended[id] = release.LaneID
			}
		}
		return releases.Err()
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
		} else if c := l.laneConditions[lane]; c != nil && c.reason == ReasonLaneChildren {
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

func (l *Loop) holdLaneChildren(ctx context.Context, lane string, pids []int) error {
	sort.Ints(pids)
	ids := make([]string, len(pids))
	for i, pid := range pids {
		ids[i] = strconv.Itoa(pid)
	}
	refusal := asLaneRefusal(laneRefusalf(lane, ReasonLaneChildren,
		"wait for these processes to exit, or end them with `kill "+strings.Join(ids, " ")+"`; the next pass rechecks the lane",
		"ended runs still have live marked processes in lane %s: PIDs %s", lane, strings.Join(ids, ", ")))
	return l.takeLaneOutOfRotation(ctx, refusal)
}
