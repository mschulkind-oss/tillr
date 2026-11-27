package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

type runningWorkWorktrees struct {
	OnDisk          int   `json:"on_disk"`
	InUse           int   `json:"in_use"`
	Idle            int   `json:"idle"`
	CountLimit      int   `json:"count_limit"`
	WorktreeBytes   int64 `json:"worktree_bytes"`
	ScratchBytes    int64 `json:"scratch_bytes"`
	TotalBytes      int64 `json:"total_bytes"`
	DiskBudgetBytes int64 `json:"disk_budget_bytes"`
	RunLogBytes     int64 `json:"run_log_bytes"`
}

type runningWorkSummary struct {
	OrdinaryRunning int                  `json:"ordinary_running"`
	ExpressRunning  int                  `json:"express_running"`
	CommandRunning  int                  `json:"command_running"`
	AgentLimit      *int                 `json:"agent_limit"`
	Worktrees       runningWorkWorktrees `json:"worktrees"`
}

type runningWorkRow struct {
	RunID         string     `json:"run_id"`
	ItemID        string     `json:"item_id"`
	ItemTitle     string     `json:"item_title,omitempty"`
	Place         int        `json:"place,omitempty"`
	NodeID        string     `json:"node_id,omitempty"`
	NodeKind      string     `json:"node_kind"`
	Seat          string     `json:"seat,omitempty"`
	SeatSource    string     `json:"seat_source,omitempty"`
	Backend       string     `json:"backend,omitempty"`
	Express       bool       `json:"express"`
	LaneID        string     `json:"lane_id,omitempty"`
	StartedAtMS   int64      `json:"started_at_ms"`
	StateSinceMS  int64      `json:"state_since_ms"`
	LeasedUntilMS *int64     `json:"leased_until_ms,omitempty"`
	LeaseNoExpiry bool       `json:"lease_no_expiry,omitempty"`
	LogGrewAtMS   *int64     `json:"log_grew_at_ms,omitempty"`
	LatestAction  laneAction `json:"latest_action"`
	Spend         liveSpend  `json:"spend"`
}

type runningWorkPayload struct {
	Summary runningWorkSummary `json:"summary"`
	Rows    []runningWorkRow   `json:"rows"`
}

func (s *server) runningWorkRoutePayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	now := s.clock().UTC()
	session, _, _, err := readLoopSession(r, now)
	if err != nil {
		return nil, unreadable(err)
	}
	payload, err := buildRunningWork(r, s.root, s.obs.laneRoot, s.obs.loopDir, session, now)
	if err != nil {
		return nil, unreadable(err)
	}
	return payload, nil
}

func readRunningAgentLimit(r *store.Reader, session *loopSession) (*int, error) {
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='drain_runtime_config')`).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		var raw string
		err := r.QueryRow(`SELECT value_json FROM drain_runtime_config WHERE key='ordinary_lanes'`).Scan(&raw)
		if err == nil {
			if n, convErr := strconv.Atoi(strings.TrimSpace(raw)); convErr == nil && n >= 0 {
				return &n, nil
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	if session != nil {
		ordinary := 0
		for _, laneID := range session.Lanes {
			if laneID == "" || strings.HasPrefix(laneID, "express-") || strings.HasPrefix(laneID, "desk-") {
				continue
			}
			ordinary++
		}
		if ordinary > 0 {
			return &ordinary, nil
		}
		fallback := 2
		return &fallback, nil
	}
	return nil, nil
}

func readExpressItemSet(r *store.Reader) (map[string]bool, error) {
	out := map[string]bool{}
	var hasExpress bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name='express')`).Scan(&hasExpress); err != nil {
		return nil, err
	}
	if !hasExpress {
		return out, nil
	}
	rows, err := r.Query(`SELECT item_id FROM items WHERE express = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func readStartedNodeKinds(r *store.Reader) map[string]string {
	out := map[string]string{}
	rows, err := r.Query(`SELECT object_id, payload FROM events WHERE kind='drain.started' AND object_type='run' ORDER BY seq`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var runID string
		var raw []byte
		if err := rows.Scan(&runID, &raw); err != nil {
			continue
		}
		var d struct {
			Decision struct {
				NodeIndex int        `json:"node_index"`
				Workflow  *itemGraph `json:"workflow"`
			} `json:"decision"`
		}
		if json.Unmarshal(raw, &d) != nil || d.Decision.Workflow == nil {
			continue
		}
		idx := d.Decision.NodeIndex
		if idx >= 0 && idx < len(d.Decision.Workflow.Stages) {
			if kind := d.Decision.Workflow.Stages[idx].Kind; kind != "" {
				out[runID] = kind
			}
		}
	}
	return out
}

func buildRunningWork(r *store.Reader, root, laneRoot, loopDir string, session *loopSession, now time.Time) (runningWorkPayload, error) {
	agentLimit, err := readRunningAgentLimit(r, session)
	if err != nil {
		return runningWorkPayload{}, err
	}
	items, err := readItems(r)
	if err != nil {
		return runningWorkPayload{}, err
	}
	places := itemPlaces(items)
	itemsByID := make(map[string]storeItem, len(items))
	for _, it := range items {
		itemsByID[it.ItemID] = it
	}
	expressItems, err := readExpressItemSet(r)
	if err != nil {
		return runningWorkPayload{}, err
	}
	laneRows, err := readLanesIn(r, loopDir)
	if err != nil && !isMissingTable(err) {
		return runningWorkPayload{}, err
	}
	runs, err := readRuns(r)
	if err != nil && !isMissingTable(err) {
		return runningWorkPayload{}, err
	}
	allocations, err := readAllocations(r)
	if err != nil {
		return runningWorkPayload{}, err
	}
	allRuns := append([]storeRun(nil), runs...)
	allRuns = append(allRuns, liveRuns(laneRows, runs, allocations)...)

	runsByID := make(map[string]storeRun, len(allRuns))
	for _, run := range allRuns {
		runsByID[run.RunID] = run
	}
	dispatchedSeats, err := dispatchSeats(r)
	if err != nil {
		return runningWorkPayload{}, err
	}
	nodeKinds := readStartedNodeKinds(r)

	dead, err := deadRunsReader(r, loopDir)()
	if err != nil && !isMissingTable(err) {
		return runningWorkPayload{}, err
	}

	type activeEntry struct {
		runID string
		lane  *storeLane
	}
	var entries []activeEntry
	seenRuns := map[string]bool{}
	inUse := map[string]bool{}
	for i := range laneRows {
		l := &laneRows[i]
		if l.State != laneStateLeased {
			continue
		}
		inUse[l.LaneID] = true
		if len(l.ActiveNodes) > 0 {
			for _, runID := range l.ActiveNodes {
				if runID == "" || seenRuns[runID] || dead[runID] {
					continue
				}
				seenRuns[runID] = true
				entries = append(entries, activeEntry{runID: runID, lane: l})
			}
		} else if l.RunID != "" && !seenRuns[l.RunID] && !dead[l.RunID] {
			seenRuns[l.RunID] = true
			entries = append(entries, activeEntry{runID: l.RunID, lane: l})
		}
	}
	for _, run := range allRuns {
		if run.State != "running" || seenRuns[run.RunID] || dead[run.RunID] {
			continue
		}
		seenRuns[run.RunID] = true
		var lanePtr *storeLane
		for i := range laneRows {
			if laneRows[i].LaneID == run.LaneID {
				lanePtr = &laneRows[i]
				if laneRows[i].State == laneStateLeased {
					inUse[laneRows[i].LaneID] = true
				}
				break
			}
		}
		entries = append(entries, activeEntry{runID: run.RunID, lane: lanePtr})
	}

	rows := make([]runningWorkRow, 0, len(entries))
	summary := runningWorkSummary{
		AgentLimit: agentLimit,
	}

	for _, entry := range entries {
		run := runsByID[entry.runID]
		alloc, hasAlloc := allocations[entry.runID]
		itemID := run.ItemID
		if itemID == "" && hasAlloc {
			itemID = alloc.ItemID
		}
		laneID := run.LaneID
		if laneID == "" && entry.lane != nil {
			laneID = entry.lane.LaneID
		}
		if laneID == "" && hasAlloc {
			laneID = alloc.LaneID
		}
		if itemID != "" {
			inUse[itemID] = true
		}
		nodeID := run.Node
		if nodeID == "" && hasAlloc {
			nodeID = alloc.Node
		}
		backend := run.Backend
		if backend == "" && hasAlloc {
			backend = alloc.Backend
		}
		nodeKind := nodeKinds[entry.runID]
		if nodeKind == "" {
			if backend == "command" {
				nodeKind = "command"
			} else {
				nodeKind = "agent"
			}
		}
		seat := run.Seat
		seatSource := ""
		if seat != "" {
			seatSource = "run"
		} else if hasAlloc && alloc.Seat != "" {
			seat = alloc.Seat
			seatSource = "run"
		} else if ds := dispatchedSeats[entry.runID]; ds != "" {
			seat = ds
			seatSource = "dispatch"
		}
		startedAt := run.StartedAtMS
		if startedAt == 0 && hasAlloc {
			startedAt = alloc.StartedAtMS
		}
		isExpress := expressItems[itemID] || strings.HasPrefix(laneID, "express-")
		var leasedUntil *int64
		leaseNoExpiry := false
		if entry.lane != nil && entry.lane.State == laneStateLeased {
			leasedUntil = entry.lane.LeasedUntilMS
			leaseNoExpiry = entry.lane.LeasedUntilMS == nil
		}

		logPath := runLogPath(laneRoot, laneID, entry.runID)
		action, _, grewAt := readLaneActionCounted(logPath, backend, nil)
		action = withLogAge(action, entry.runID, grewAt, now)
		spend, _ := readLiveRunStream(logPath)
		if spend.Tokens == nil && run.UsageTokens != nil {
			v := *run.UsageTokens
			spend.Tokens = &v
		}
		if spend.CostUSD == nil && run.CostUSD != nil {
			v := *run.CostUSD
			spend.CostUSD = &v
			spend.Note = ""
		}

		it := itemsByID[itemID]
		rows = append(rows, runningWorkRow{
			RunID:         entry.runID,
			ItemID:        itemID,
			ItemTitle:     it.Title,
			Place:         places[itemID],
			NodeID:        nodeID,
			NodeKind:      nodeKind,
			Seat:          seat,
			SeatSource:    seatSource,
			Backend:       backend,
			Express:       isExpress,
			LaneID:        laneID,
			StartedAtMS:   startedAt,
			StateSinceMS:  startedAt,
			LeasedUntilMS: leasedUntil,
			LeaseNoExpiry: leaseNoExpiry,
			LogGrewAtMS:   grewAt,
			LatestAction:  action,
			Spend:         spend,
		})

		if nodeKind == "command" {
			summary.CommandRunning++
		} else if isExpress {
			summary.ExpressRunning++
		} else if !strings.HasPrefix(laneID, "desk-") {
			summary.OrdinaryRunning++
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].StartedAtMS != rows[j].StartedAtMS {
			return rows[i].StartedAtMS < rows[j].StartedAtMS
		}
		return rows[i].RunID < rows[j].RunID
	})

	wtSnap, err := measureLaneRoot(laneRoot, inUse)
	if err != nil {
		return runningWorkPayload{}, err
	}
	runLogBytes := measureRunLogs(root)
	var cfg settings.Settings
	if root != "" {
		if loaded, loadErr := settings.Read(root); loadErr == nil {
			cfg = loaded
		}
	}
	limitVal := 0
	if agentLimit != nil {
		limitVal = *agentLimit
	}
	summary.Worktrees = runningWorkWorktrees{
		OnDisk:          wtSnap.OnDisk,
		InUse:           wtSnap.InUse,
		Idle:            wtSnap.Idle,
		CountLimit:      cfg.Worktrees.EffectiveMaxCount(limitVal),
		WorktreeBytes:   wtSnap.WorktreeBytes,
		ScratchBytes:    wtSnap.ScratchBytes,
		TotalBytes:      wtSnap.TotalBytes,
		DiskBudgetBytes: cfg.Worktrees.EffectiveDiskBudgetBytes(limitVal),
		RunLogBytes:     runLogBytes,
	}

	return runningWorkPayload{
		Summary: summary,
		Rows:    rows,
	}, nil
}

func readNodeRunSegments(r *store.Reader, runID string, startedAtMS int64) []itemNodeSegment {
	segs := []itemNodeSegment{{Ordinal: 1, StartedAtMS: startedAtMS}}
	rows, err := r.Query(`SELECT ts FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq`, resumeKind, runID)
	if err != nil {
		return segs
	}
	defer rows.Close()
	ord := 2
	for rows.Next() {
		var ts int64
		if err := rows.Scan(&ts); err == nil {
			segs = append(segs, itemNodeSegment{Ordinal: ord, StartedAtMS: ts})
			ord++
		}
	}
	return segs
}

func readRunEndCause(r *store.Reader, runID string) string {
	var cause sql.NullString
	err := r.QueryRow(`SELECT end_cause FROM runs WHERE run_id = ?`, runID).Scan(&cause)
	if err == nil && cause.Valid {
		return cause.String
	}
	return ""
}

func resolveNodeLog(laneRoot, laneID, runID string) *itemNodeLog {
	if runID == "" {
		return nil
	}
	p := runLogPath(laneRoot, laneID, runID)
	out := &itemNodeLog{
		TranscriptURL: "#/run/" + runID,
	}
	if p != "" {
		out.Path = filepath.Base(p)
		if info, err := os.Stat(p); err == nil {
			out.Present = true
			out.Bytes = info.Size()
		}
	}
	return out
}

type worktreeMeasure struct {
	OnDisk        int
	InUse         int
	Idle          int
	WorktreeBytes int64
	ScratchBytes  int64
	TotalBytes    int64
}

func measureLaneRoot(laneRoot string, inUse map[string]bool) (worktreeMeasure, error) {
	var snap worktreeMeasure
	if laneRoot == "" {
		return snap, nil
	}
	entries, err := os.ReadDir(laneRoot)
	if os.IsNotExist(err) {
		return snap, nil
	}
	if err != nil {
		return snap, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		laneID := entry.Name()
		laneDir := filepath.Join(laneRoot, laneID)
		var wtBytes, scratchBytes int64
		hasWT := false
		wtPath := filepath.Join(laneDir, "worktree")
		if st, err := os.Stat(wtPath); err == nil && st.IsDir() {
			hasWT = true
			wtBytes = dirBytesCount(wtPath)
		}
		for _, sub := range []string{"scratch", "durable"} {
			subPath := filepath.Join(laneDir, sub)
			subEntries, err := os.ReadDir(subPath)
			if err != nil {
				continue
			}
			for _, se := range subEntries {
				p := filepath.Join(subPath, se.Name())
				if se.IsDir() {
					scratchBytes += dirBytesCount(p)
				} else if fi, err := se.Info(); err == nil {
					scratchBytes += fi.Size()
				}
			}
		}
		if hasWT {
			snap.OnDisk++
			if inUse[laneID] {
				snap.InUse++
			} else {
				snap.Idle++
			}
		}
		snap.WorktreeBytes += wtBytes
		snap.ScratchBytes += scratchBytes
		snap.TotalBytes += wtBytes + scratchBytes
	}
	return snap, nil
}

func measureRunLogs(root string) int64 {
	if root == "" {
		return 0
	}
	return dirBytesCount(filepath.Join(root, ".tillr", "loop"))
}

func dirBytesCount(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, infoErr := d.Info(); infoErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
