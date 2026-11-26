package procguard

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

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr-run/run"
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

// LiveRunWorkerPIDs scans /proc once for live non-zombie processes of this user
// carrying TILLR_RUN_ID and TILLR_LOOP_STORE=<resolved storePath>. When a run has
// a live run face (IsRunFaceArgv), that face's PID wins over adapters and children;
// ties break to the lowest PID.
func LiveRunWorkerPIDs(storePath string) (map[string]int, error) {
	resolved, err := ProcessStoreIdentity(storePath)
	if err != nil {
		return nil, err
	}
	ProcScans.Add(1)
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
		start, err := ProcessStart(pid)
		if ProcessGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking process %d: %w", pid, err)
		}
		if start == "" {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		env, err := RunProcessEnvironment(dir, uid)
		if ProcessGone(err) {
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
		if ProcessGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking process %d: %w", pid, err)
		}
		face := IsRunFaceArgv(argv)
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

func workerReadFailure(err error) error {
	if strings.Contains(err.Error(), "no such table") {
		return drainmodel.Incompatiblef("%v", err)
	}
	return drainmodel.Internalf("%v", err)
}

type workerPendingState struct {
	owner    *LoopOwner
	examiner *LoopOwner
	done     bool
}

type workerPendingPayload struct {
	Decision struct {
		ItemID     string `json:"item_id"`
		RunRequest *struct {
			RunID  string `json:"run_id"`
			ItemID string `json:"item_id"`
		} `json:"run_request"`
		Parallel []struct {
			RunRequest *struct {
				RunID  string `json:"run_id"`
				ItemID string `json:"item_id"`
			} `json:"run_request"`
		} `json:"parallel"`
	} `json:"decision"`
	Owner    *LoopOwner `json:"loop_owner"`
	Examiner *LoopOwner `json:"examiner_owner"`
	Done     bool       `json:"done"`
}

func readWorkerPending(r *store.Reader) (map[string]*workerPendingState, map[string]string, error) {
	events, err := r.EventsOfKind(0,
		DispatchStartedKind,
		NodeAdvancedKind,
		GraphChangedKind,
		ExaminationClaimedKind,
		ExaminationOwnerKind,
		ExaminationDoneKind,
		run.ResumeKind,
	)
	if err != nil {
		return nil, nil, err
	}
	byItem := map[string]*workerPendingState{}
	byRun := map[string]*workerPendingState{}
	itemByRun := map[string]string{}
	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		switch e.Kind {
		case DispatchStartedKind, NodeAdvancedKind, GraphChangedKind:
			var p workerPendingPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, nil, err
			}
			st := &workerPendingState{owner: p.Owner, examiner: p.Examiner, done: p.Done}
			byItem[p.Decision.ItemID] = st
			byRun[*e.ObjectID] = st
			if p.Decision.RunRequest != nil && p.Decision.RunRequest.RunID != "" {
				byRun[p.Decision.RunRequest.RunID] = st
				itemByRun[p.Decision.RunRequest.RunID] = p.Decision.ItemID
			}
			for _, node := range p.Decision.Parallel {
				if node.RunRequest != nil && node.RunRequest.RunID != "" {
					byRun[node.RunRequest.RunID] = st
					if node.RunRequest.ItemID != "" {
						itemByRun[node.RunRequest.RunID] = node.RunRequest.ItemID
					} else if p.Decision.ItemID != "" {
						itemByRun[node.RunRequest.RunID] = p.Decision.ItemID
					}
				}
			}
		case ExaminationClaimedKind:
			if st := byRun[*e.ObjectID]; st != nil {
				if err := json.Unmarshal(e.Payload, &st.owner); err != nil {
					return nil, nil, err
				}
			}
		case ExaminationOwnerKind:
			if st := byRun[*e.ObjectID]; st != nil {
				if err := json.Unmarshal(e.Payload, &st.examiner); err != nil {
					return nil, nil, err
				}
			}
		case ExaminationDoneKind:
			if st := byRun[*e.ObjectID]; st != nil {
				st.done = true
			}
		case run.ResumeKind:
			if st := byRun[*e.ObjectID]; st != nil && e.ObjectType == "run" {
				resumingOwner := st.examiner
				if resumingOwner == nil {
					resumingOwner = st.owner
				}
				if st.done {
					resumingOwner = nil
				}
				st.done = false
				st.owner = resumingOwner
				st.examiner = nil
			}
		}
	}
	// Keep only the latest work state per item, matching readPending's item-keyed map.
	activeStates := map[*workerPendingState]bool{}
	for _, st := range byItem {
		activeStates[st] = true
	}
	outByRun := map[string]*workerPendingState{}
	for runID, st := range byRun {
		if activeStates[st] {
			outByRun[runID] = st
		}
	}
	return outByRun, itemByRun, nil
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
		return nil, workerReadFailure(err)
	}
	for rows.Next() {
		var laneID, runID string
		if err := rows.Scan(&laneID, &runID); err != nil {
			rows.Close()
			return nil, workerReadFailure(err)
		}
		add(laneID, "", runID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, workerReadFailure(err)
	}
	rows.Close()

	var hasROR int
	if err := r.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='read_only_runs'`).Scan(&hasROR); err != nil {
		return nil, workerReadFailure(err)
	}
	if hasROR > 0 {
		rRows, err := r.Query(`SELECT lane_id, item_id, run_id FROM read_only_runs WHERE settled = 0`)
		if err != nil {
			return nil, workerReadFailure(err)
		}
		for rRows.Next() {
			var laneID, itemID, runID string
			if err := rRows.Scan(&laneID, &itemID, &runID); err != nil {
				rRows.Close()
				return nil, workerReadFailure(err)
			}
			add(laneID, itemID, runID)
		}
		if err := rRows.Err(); err != nil {
			rRows.Close()
			return nil, workerReadFailure(err)
		}
		rRows.Close()
	}

	if len(byKey) == 0 {
		return []Worker{}, nil
	}

	workByRun, itemByRun, err := readWorkerPending(r)
	if err != nil {
		return nil, err
	}

	here, err := CurrentLoopOwner()
	if err != nil {
		return nil, err
	}
	livePIDs, err := LiveRunWorkerPIDs(storePath)
	if err != nil {
		return nil, err
	}

	out := make([]Worker, 0, len(byKey))
	for _, w := range byKey {
		if w.Item == "" {
			if itemID := itemByRun[w.Run]; itemID != "" {
				w.Item = itemID
			} else if itemID, err := lookupWorkerItem(r, w.Run); err != nil {
				return nil, workerReadFailure(err)
			} else {
				w.Item = itemID
			}
		}
		if pid, ok := livePIDs[w.Run]; ok && pid > 0 {
			w.PID = pid
			w.Alive = true
		} else if work := workByRun[w.Run]; work != nil && here != nil {
			ownerDomain := ""
			if FromOtherDomain(work.owner, here) {
				ownerDomain = work.owner.Domain
			} else if FromOtherDomain(work.examiner, here) {
				ownerDomain = work.examiner.Domain
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
