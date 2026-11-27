package procguard

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

// LaneChildrenHooks connects RecheckLaneChildren to drain's lane-rotation state.
type LaneChildrenHooks struct {
	LoadLaneConditions func(ctx context.Context) error
	Lanes              func() []string
	HeldChildrenLanes  func() []string
	ResolveRunLeftBy   func(ctx context.Context, lane, runID string) string
	HoldLaneChildren   func(ctx context.Context, lane string, pids []int, leftBy string) error
	HasLaneCondition   func(lane string) bool
	RestoreLane        func(ctx context.Context, lane string) error
}

// RecheckLaneChildren reads the process table once for all ended runs. Store release
// is independent of rotation: an idle lane must not be reused while an old run's
// marked processes can still write there. This also recovers holds after a crash
// between release and recording the refusal, and after a supervisor restart.
func RecheckLaneChildren(ctx context.Context, storePath, laneRoot string, h LaneChildrenHooks) error {
	if err := h.LoadLaneConditions(ctx); err != nil {
		return err
	}
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return err
	}
	ended := map[string]string{}
	endedAt := map[string]int64{}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		// endedAt is the seq of the latest record that ended each run: its runs row's
		// finish event, or a release. A run.resume after it is a continuation of the same
		// run that has not ended yet (tillr-run claimLane), so that run is live again.
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
		releases, err := r.Query(`SELECT seq, kind, object_id, payload FROM events WHERE kind IN (?, ?) ORDER BY seq`, run.ReleaseKind, LaneReleasedKind)
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
				var release laneReleasedPayload
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
		if err := resumes.Err(); err != nil {
			return err
		}
		resumes.Close()
		// A run that currently holds a lane lease or is an unsettled read-only group
		// member is live right now, even if an earlier run with the same ID ended.
		active, err := r.Query(`SELECT run_id FROM lanes WHERE state = ? AND run_id IS NOT NULL AND run_id != '' UNION SELECT run_id FROM read_only_runs WHERE settled = 0`, run.LaneStateLeased)
		if err != nil {
			return err
		}
		defer active.Close()
		for active.Next() {
			var id string
			if err := active.Scan(&id); err != nil {
				return err
			}
			delete(ended, id)
		}
		return active.Err()
	})
	s.Close()
	if err != nil {
		return err
	}
	here, err := CurrentLoopOwner()
	if err != nil {
		return err
	}
	marked, err := LiveRunWorkers(storePath, "", here)
	if err != nil {
		return err
	}
	pids := map[string][]int{}
	bestRun := map[string]string{}
	bestSeq := map[string]int64{}
	hasBest := map[string]bool{}
	for _, p := range marked {
		lane, ok := ended[p.RunID]
		if !ok {
			continue
		}
		inside, err := ProcessInCheckout(p, filepath.Join(laneRoot, lane))
		if err != nil {
			return err
		}
		if inside {
			pids[lane] = append(pids[lane], p.PID)
			seq := endedAt[p.RunID]
			if !hasBest[lane] || seq > bestSeq[lane] || (seq == bestSeq[lane] && p.RunID > bestRun[lane]) {
				bestRun[lane] = p.RunID
				bestSeq[lane] = seq
				hasBest[lane] = true
			}
		}
	}
	checkLanes := map[string]bool{}
	for _, lane := range h.Lanes() {
		checkLanes[lane] = true
	}
	for lane := range pids {
		checkLanes[lane] = true
	}
	if h.HeldChildrenLanes != nil {
		for _, lane := range h.HeldChildrenLanes() {
			checkLanes[lane] = true
		}
	}
	sortedLanes := make([]string, 0, len(checkLanes))
	for lane := range checkLanes {
		sortedLanes = append(sortedLanes, lane)
	}
	sort.Strings(sortedLanes)
	for _, lane := range sortedLanes {
		if len(pids[lane]) > 0 {
			leftBy := h.ResolveRunLeftBy(ctx, lane, bestRun[lane])
			if err := h.HoldLaneChildren(ctx, lane, pids[lane], leftBy); err != nil {
				return err
			}
		} else if h.HasLaneCondition(lane) {
			if err := h.RestoreLane(ctx, lane); err != nil {
				return err
			}
		}
	}
	return nil
}

// ProcessInCheckout resolves symlinks and compares path components, not string
// prefixes: lane-1-other is not lane-1. Revalidates start time after reading cwd
// to reject PID reuse.
func ProcessInCheckout(p *RunFaceOwner, path string) (bool, error) {
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", p.PID))
	if ProcessGone(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	root, err := filepath.EvalSymlinks(path)
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
	start, err := ProcessStart(p.PID)
	if ProcessGone(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return start == p.Start && start != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// FormatLaneChildrenRefusal formats the sorted-PID remediation and detail message
// for a lane held due to surviving children of an ended run.
func FormatLaneChildrenRefusal(lane string, pids []int) (remediation, message string) {
	sort.Ints(pids)
	ids := make([]string, len(pids))
	for i, pid := range pids {
		ids[i] = strconv.Itoa(pid)
	}
	remediation = "wait for these processes to exit, or end them with `kill " + strings.Join(ids, " ") + "`; the next pass rechecks the lane"
	message = fmt.Sprintf("ended runs still have live marked processes in lane %s: PIDs %s", lane, strings.Join(ids, ", "))
	return remediation, message
}
