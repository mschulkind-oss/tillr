package drain

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr/store"
)

// Worker is one leased worker on a lane, paired with its process ID (0 when no
// process is visible in this PID namespace) and whether the process is alive now,
// judged by the same evidence the loop uses for a dead run.
type Worker struct {
	Lane  string `json:"lane"`
	Item  string `json:"item"`
	Run   string `json:"run"`
	PID   int    `json:"pid"`
	Alive bool   `json:"alive"`
}

type workerKey struct {
	lane string
	run  string
}

type liveWorkerProcess struct {
	pid  int
	face bool
}

// liveRunWorkerPIDs scans /proc once for live non-zombie processes of this user
// carrying TILLR_RUN_ID and TILLR_LOOP_STORE=<resolved storePath>. When a run has
// a live run face (isRunFaceArgv), that face's PID wins over adapters and children;
// ties break to the lowest PID.
func liveRunWorkerPIDs(storePath string) (map[string]int, error) {
	resolved, err := processStoreIdentity(storePath)
	if err != nil {
		return nil, err
	}
	procScans.Add(1)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	uid := uint32(os.Geteuid())
	self := os.Getpid()
	wantStore := "TILLR_LOOP_STORE=" + resolved
	best := map[string]liveWorkerProcess{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		start, err := processStart(pid)
		if processGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking process %d: %w", pid, err)
		}
		if start == "" {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		env, err := runProcessEnvironment(dir, uid)
		if processGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking process %d: %w", pid, err)
		}
		runID, hasStore := "", false
		for _, v := range bytes.Split(env, []byte{0}) {
			s := string(v)
			if id, ok := strings.CutPrefix(s, "TILLR_RUN_ID="); ok {
				runID = id
			} else if s == wantStore {
				hasStore = true
			}
		}
		if !hasStore || runID == "" {
			continue
		}
		argv, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if processGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking process %d: %w", pid, err)
		}
		face := isRunFaceArgv(argv)
		prev, seen := best[runID]
		if !seen || (face && !prev.face) || (face == prev.face && pid < prev.pid) {
			best[runID] = liveWorkerProcess{pid: pid, face: face}
		}
	}
	pids := make(map[string]int, len(best))
	for runID, proc := range best {
		pids[runID] = proc.pid
	}
	return pids, nil
}

// Workers lists the currently leased workers by lane, with each worker's item,
// run, process ID, and whether the process is alive now. It opens nothing for
// writing and is right even while no loop runs.
func Workers(r *store.Reader, storePath, loopDir string) ([]Worker, error) {
	byKey := map[workerKey]*Worker{}
	add := func(lane, item, runID string) {
		key := workerKey{lane: lane, run: runID}
		if existing, ok := byKey[key]; ok {
			if existing.Item == "" && item != "" {
				existing.Item = item
			}
			return
		}
		byKey[key] = &Worker{Lane: lane, Item: item, Run: runID}
	}

	rows, err := r.Query(`SELECT lane_id, run_id FROM lanes WHERE state = 'leased' AND run_id IS NOT NULL AND run_id != ''`)
	if err != nil {
		return nil, readFailure(err)
	}
	for rows.Next() {
		var laneID, runID string
		if err := rows.Scan(&laneID, &runID); err != nil {
			rows.Close()
			return nil, readFailure(err)
		}
		add(laneID, "", runID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, readFailure(err)
	}
	rows.Close()

	var hasROR int
	if err := r.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='read_only_runs'`).Scan(&hasROR); err != nil {
		return nil, readFailure(err)
	}
	if hasROR > 0 {
		rRows, err := r.Query(`SELECT lane_id, item_id, run_id FROM read_only_runs WHERE settled = 0`)
		if err != nil {
			return nil, readFailure(err)
		}
		for rRows.Next() {
			var laneID, itemID, runID string
			if err := rRows.Scan(&laneID, &itemID, &runID); err != nil {
				rRows.Close()
				return nil, readFailure(err)
			}
			add(laneID, itemID, runID)
		}
		if err := rRows.Err(); err != nil {
			rRows.Close()
			return nil, readFailure(err)
		}
		rRows.Close()
	}

	if len(byKey) == 0 {
		return []Worker{}, nil
	}

	works, err := readPending(r)
	if err != nil {
		return nil, err
	}
	workByRun := map[string]*pendingWork{}
	itemByRun := map[string]string{}
	for _, work := range works {
		if work.Decision.RunRequest != nil && work.Decision.RunRequest.RunID != "" {
			workByRun[work.Decision.RunRequest.RunID] = work
			itemByRun[work.Decision.RunRequest.RunID] = work.Decision.ItemID
		}
		for _, node := range parallelNodes(work.Decision) {
			if node.RunRequest != nil && node.RunRequest.RunID != "" {
				workByRun[node.RunRequest.RunID] = work
				if node.RunRequest.ItemID != "" {
					itemByRun[node.RunRequest.RunID] = node.RunRequest.ItemID
				} else if work.Decision.ItemID != "" {
					itemByRun[node.RunRequest.RunID] = work.Decision.ItemID
				}
			}
		}
	}

	here, err := currentLoopOwner()
	if err != nil {
		return nil, err
	}
	livePIDs, err := liveRunWorkerPIDs(storePath)
	if err != nil {
		return nil, err
	}

	out := make([]Worker, 0, len(byKey))
	for _, w := range byKey {
		if w.Item == "" {
			if itemID := itemByRun[w.Run]; itemID != "" {
				w.Item = itemID
			} else if itemID, err := lookupWorkerItem(r, w.Run); err != nil {
				return nil, readFailure(err)
			} else {
				w.Item = itemID
			}
		}
		if pid, ok := livePIDs[w.Run]; ok && pid > 0 {
			w.PID = pid
			w.Alive = true
		} else if work := workByRun[w.Run]; work != nil && here != nil {
			ownerDomain := ""
			if fromOtherDomain(work.Owner, here) {
				ownerDomain = work.Owner.Domain
			} else if fromOtherDomain(work.Examiner, here) {
				ownerDomain = work.Examiner.Domain
			}
			if ownerDomain != "" {
				paths := []string{
					filepath.Join(loopDir, w.Run, "run.record.json"),
					filepath.Join(loopDir, w.Run, "run.stderr"),
				}
				w.Alive = presence.ForeignRunLive(paths, loopDir != "", ownerDomain, here.Domain)
			}
		}
		out = append(out, *w)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lane != out[j].Lane {
			return out[i].Lane < out[j].Lane
		}
		return out[i].Run < out[j].Run
	})
	return out, nil
}

func lookupWorkerItem(r *store.Reader, runID string) (string, error) {
	var itemID sql.NullString
	err := r.QueryRow(`SELECT item_id FROM runs WHERE run_id = ?`, runID).Scan(&itemID)
	if err == nil && itemID.Valid && itemID.String != "" {
		return itemID.String, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var payload []byte
	err = r.QueryRow(`SELECT payload FROM events WHERE kind = 'run.allocate' AND object_id = ? ORDER BY seq DESC LIMIT 1`, runID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var alloc struct {
		ItemID string `json:"item_id"`
	}
	if err := json.Unmarshal(payload, &alloc); err != nil {
		return "", err
	}
	return alloc.ItemID, nil
}
