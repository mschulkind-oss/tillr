package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

type pausedJudgeHoldInfo struct {
	runID       string
	laneID      string
	backend     string
	seat        string
	workflow    string
	node        string
	baseSHA     string
	headSHA     string
	writerRunID string
	itemState   string
}

func isMissingSchemaErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "no such column")
}

func pausedRunNonQuota(r *store.Reader, runID string) (*run.Record, bool, error) {
	if runID == "" {
		return nil, false, nil
	}
	rec, err := terminalRecord(r, runID)
	if isMissingSchemaErr(err) {
		return nil, false, nil
	}
	if err != nil || rec == nil {
		return nil, false, err
	}
	if rec.State != run.StatePaused || (rec.EndCause != run.EndRestart && rec.EndCause != "") {
		return nil, false, nil
	}
	if judgeQuotaRefusal(*rec) {
		return nil, false, nil
	}
	backend, err := quotaPauseBackend(r, runID)
	if isMissingSchemaErr(err) {
		backend, err = "", nil
	}
	if err != nil || backend != "" {
		return nil, false, err
	}
	return rec, true, nil
}

func reopenPausedJudgeUnreadable(r *store.Reader, s *pendingHistoryState) error {
	_, _, err := reopenPausedJudgeUnreadableSnapshot(r, s, true)
	return err
}

// Before paused mid-node runs were retained on pause, a paused verify or judge
// run with no eval.output was settled as a judge-unreadable refusal and marked
// examined. While that paused run is still the item's latest run and held only
// as judge-unreadable (excluding quota pauses), reopen its pending work so the
// planner and loop resume it as paused work once a lane is available.
func reopenPausedJudgeUnreadableSnapshot(r *store.Reader, s *pendingHistoryState, alreadyCloned bool) (*pendingHistoryState, []string, error) {
	if r == nil || s == nil {
		return s, nil, nil
	}
	rows, err := r.Query(`SELECT DISTINCT item_id FROM runs WHERE state = ? AND item_id IS NOT NULL AND item_id != ''`, run.StatePaused)
	var candidateItems []string
	if err != nil {
		if !isMissingSchemaErr(err) {
			return s, nil, err
		}
	} else {
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return s, nil, err
			}
			candidateItems = append(candidateItems, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return s, nil, err
		}
		rows.Close()
	}

	hadReopened := false
	for _, w := range s.works {
		if w != nil && w.ReopenedPausedJudge {
			hadReopened = true
			break
		}
	}
	if len(candidateItems) == 0 && !hadReopened {
		return s, nil, nil
	}

	sort.Strings(candidateItems)
	var preMatched []pausedJudgeHoldInfo
	var preMatchedIDs []string
	for _, id := range candidateItems {
		info, ok, err := precheckPausedJudgeUnreadableItem(r, id, s.works[id])
		if err != nil {
			return s, nil, err
		}
		if ok {
			preMatched = append(preMatched, info)
			preMatchedIDs = append(preMatchedIDs, id)
		}
	}
	if len(preMatched) == 0 && !hadReopened {
		return s, nil, nil
	}

	defRec, err := definition.ForReader(r)
	if err != nil {
		if isMissingSchemaErr(err) {
			return s, nil, nil
		}
		return s, nil, err
	}
	def := defRec.Parsed

	eligible := map[string]pausedJudgeHoldInfo{}
	if len(preMatched) > 0 && holdEnabled(def, ReasonJudgeUnreadable) {
		var itemFaults map[string]ItemFault
		var obsHolds map[string]obstacles.Hold
		faultsLoaded := false
		for idx, id := range preMatchedIDs {
			info := preMatched[idx]
			if finishedState(def, info.itemState) || info.itemState != def.Ready {
				continue
			}
			if !faultsLoaded {
				itemFaults, err = ReadItemFaults(r)
				if err != nil {
					return s, nil, err
				}
				obsHolds, _, err = ReadObstacleHolds(r)
				if err != nil {
					return s, nil, err
				}
				faultsLoaded = true
			}
			if fault, ok := itemFaults[id]; ok && holdEnabled(def, fault.Reason) {
				continue
			}
			if obsHold, ok := obsHolds[id]; ok && holdEnabled(def, obsHold.ReasonCode()) {
				continue
			}
			eligible[id] = info
		}
	}

	ensureCloned := func() {
		if !alreadyCloned {
			s = clonePendingHistoryState(s)
			alreadyCloned = true
		}
	}

	var changed []string
	changedSet := map[string]bool{}
	markChanged := func(id string) {
		if !changedSet[id] {
			changedSet[id] = true
			changed = append(changed, id)
		}
	}

	// Revert any previously reopened work whose run is no longer a paused
	// judge-unreadable hold, unless an examination currently holds it in flight.
	for id, w := range s.works {
		if w == nil || !w.ReopenedPausedJudge {
			continue
		}
		if _, ok := eligible[id]; ok {
			continue
		}
		if !w.Examining && (w.Candidate == nil || w.Candidate.Judge == nil || len(w.Candidate.Judge.Raw) == 0) {
			ensureCloned()
			cur := s.works[id]
			cp := *cur
			cp.Done = true
			cp.ReopenedPausedJudge = false
			cp.LaneBusyByOther = false
			replacePendingWorkPtr(s, id, cur, &cp)
			markChanged(id)
		}
	}

	for _, id := range preMatchedIDs {
		info, ok := eligible[id]
		if !ok {
			continue
		}
		w := s.works[id]
		if w != nil {
			if !w.Done && !w.ReopenedPausedJudge {
				continue
			}
			if !workMatchesPausedJudge(w, info) {
				continue
			}
			if !w.ReopenedPausedJudge || w.Done || w.ReopenedHeadSHA != info.headSHA || w.ReopenedBaseSHA != info.baseSHA || w.ReopenedWriterRunID != info.writerRunID {
				ensureCloned()
				cur := s.works[id]
				cp := *cur
				cp.Done = false
				cp.ReopenedPausedJudge = true
				cp.ReopenedHeadSHA = info.headSHA
				cp.ReopenedBaseSHA = info.baseSHA
				cp.ReopenedWriterRunID = info.writerRunID
				if !cp.Examining {
					cp.Examiner = nil
					cp.examinationStartedAt = time.Time{}
				}
				replacePendingWorkPtr(s, id, cur, &cp)
				markChanged(id)
			}
			continue
		}
		synth, err := synthesizePausedJudgeWork(r, def, id, info)
		if err != nil {
			return s, nil, err
		}
		ensureCloned()
		s.works[id] = synth
		s.byRun[info.runID] = synth
		markChanged(id)
	}

	var reopenedIDs []string
	for id, w := range s.works {
		if w != nil && !w.Done && w.ReopenedPausedJudge {
			reopenedIDs = append(reopenedIDs, id)
		}
	}
	if len(reopenedIDs) == 0 {
		return s, changed, nil
	}

	busyLanes := map[string]string{}
	for id, w := range s.works {
		if w == nil || w.Done || w.LaneReleased || w.ReopenedPausedJudge || w.Decision.LaneID == "" {
			continue
		}
		busyLanes[w.Decision.LaneID] = id
	}
	sort.Strings(reopenedIDs)
	for _, id := range reopenedIDs {
		w := s.works[id]
		lane := w.Decision.LaneID
		busy := false
		if lane != "" {
			if holder := busyLanes[lane]; holder != "" && holder != id {
				busy = true
			} else {
				busyLanes[lane] = id
			}
		}
		if w.LaneBusyByOther != busy {
			ensureCloned()
			cur := s.works[id]
			cp := *cur
			cp.LaneBusyByOther = busy
			replacePendingWorkPtr(s, id, cur, &cp)
			markChanged(id)
		}
	}
	return s, changed, nil
}

func replacePendingWorkPtr(s *pendingHistoryState, itemID string, oldWork, newWork *pendingWork) {
	s.works[itemID] = newWork
	for runID, cur := range s.byRun {
		if cur == oldWork {
			s.byRun[runID] = newWork
		}
	}
}

func workMatchesPausedJudge(w *pendingWork, info pausedJudgeHoldInfo) bool {
	if w.Decision.RunRequest != nil && w.Decision.RunRequest.RunID == info.runID {
		return true
	}
	if w.Candidate != nil {
		if judgeRunID(w.Candidate.RunID) == info.runID {
			return true
		}
		if w.Candidate.Judge != nil && w.Candidate.Judge.Request.RunID == info.runID {
			return true
		}
		if info.writerRunID != "" && w.Candidate.RunID == info.writerRunID {
			return true
		}
	}
	return false
}

func precheckPausedJudgeUnreadableItem(r *store.Reader, itemID string, work *pendingWork) (pausedJudgeHoldInfo, bool, error) {
	if work != nil && !work.Done && !work.ReopenedPausedJudge {
		return pausedJudgeHoldInfo{}, false, nil
	}
	var state string
	err := r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, itemID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) || isMissingSchemaErr(err) {
		return pausedJudgeHoldInfo{}, false, nil
	}
	if err != nil {
		return pausedJudgeHoldInfo{}, false, err
	}

	latest, ok, err := latestItemRunForPausedCheck(r, itemID)
	if err != nil || !ok || latest.state != run.StatePaused {
		return pausedJudgeHoldInfo{}, false, err
	}
	if _, nonQuota, err := pausedRunNonQuota(r, latest.runID); err != nil || !nonQuota {
		return pausedJudgeHoldInfo{}, false, err
	}

	var refusalSeq int64
	var rawPayload []byte
	err = r.QueryRow(
		`SELECT seq, payload FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq DESC LIMIT 1`,
		RefusalKind, latest.runID,
	).Scan(&refusalSeq, &rawPayload)
	if errors.Is(err, sql.ErrNoRows) || isMissingSchemaErr(err) {
		return pausedJudgeHoldInfo{}, false, nil
	}
	if err != nil {
		return pausedJudgeHoldInfo{}, false, err
	}
	var payload struct {
		Step        string `json:"step"`
		BaseSHA     string `json:"base_sha"`
		HeadSHA     string `json:"head_sha"`
		WriterRunID string `json:"writer_run_id"`
	}
	if err := json.Unmarshal(rawPayload, &payload); err != nil || payload.Step != ReasonJudgeUnreadable {
		return pausedJudgeHoldInfo{}, false, nil
	}

	// Ignore if a later gate verdict, landing, item verdict, or operator retry superseded this hold.
	checkIDs := []string{latest.runID}
	if payload.WriterRunID != "" && payload.WriterRunID != latest.runID {
		checkIDs = append(checkIDs, payload.WriterRunID)
	}
	for _, id := range checkIDs {
		var later bool
		if err := r.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM events WHERE seq > ? AND object_id = ? AND kind IN (?, ?, ?))`,
			refusalSeq, id, GateVerdictKind, "land.landing", item.VerdictRecordKind,
		).Scan(&later); err != nil {
			return pausedJudgeHoldInfo{}, false, err
		}
		if later {
			return pausedJudgeHoldInfo{}, false, nil
		}
		var retried bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM retries WHERE item_id = ? AND supersedes_run_id = ?)`, itemID, id).Scan(&retried); err != nil && !isMissingSchemaErr(err) {
			return pausedJudgeHoldInfo{}, false, err
		}
		if retried {
			return pausedJudgeHoldInfo{}, false, nil
		}
	}

	return pausedJudgeHoldInfo{
		runID:       latest.runID,
		laneID:      latest.laneID,
		backend:     latest.backend,
		seat:        latest.seat,
		workflow:    latest.workflow,
		node:        latest.node,
		baseSHA:     payload.BaseSHA,
		headSHA:     payload.HeadSHA,
		writerRunID: payload.WriterRunID,
		itemState:   state,
	}, true, nil
}

type itemRunOrderRow struct {
	runID     string
	laneID    string
	backend   string
	seat      string
	workflow  string
	node      string
	state     string
	startedAt int64
	seq       int64
	rowID     int64
}

func latestItemRunForPausedCheck(r *store.Reader, itemID string) (itemRunOrderRow, bool, error) {
	rows, err := r.Query(
		`SELECT run_id, COALESCE(lane_id,''), COALESCE(backend,''), COALESCE(seat,''), COALESCE(workflow,''), COALESCE(node,''), state, started_at, event_seq, rowid
		   FROM runs WHERE item_id = ?`,
		itemID,
	)
	var all []itemRunOrderRow
	if err != nil {
		if !isMissingSchemaErr(err) {
			return itemRunOrderRow{}, false, err
		}
		rows, err = r.Query(
			`SELECT run_id, COALESCE(lane_id,''), COALESCE(backend,''), state, started_at, event_seq, rowid
			   FROM runs WHERE item_id = ?`,
			itemID,
		)
		if err != nil {
			if isMissingSchemaErr(err) {
				return itemRunOrderRow{}, false, nil
			}
			return itemRunOrderRow{}, false, err
		}
		for rows.Next() {
			var row itemRunOrderRow
			if err := rows.Scan(&row.runID, &row.laneID, &row.backend, &row.state, &row.startedAt, &row.seq, &row.rowID); err != nil {
				rows.Close()
				return itemRunOrderRow{}, false, err
			}
			all = append(all, row)
		}
	} else {
		for rows.Next() {
			var row itemRunOrderRow
			if err := rows.Scan(&row.runID, &row.laneID, &row.backend, &row.seat, &row.workflow, &row.node, &row.state, &row.startedAt, &row.seq, &row.rowID); err != nil {
				rows.Close()
				return itemRunOrderRow{}, false, err
			}
			all = append(all, row)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return itemRunOrderRow{}, false, err
	}
	rows.Close()
	if len(all) == 0 {
		return itemRunOrderRow{}, false, nil
	}

	presentIDs := make(map[string]bool, len(all))
	for _, row := range all {
		presentIDs[row.runID] = true
	}
	filtered := all[:0]
	for _, row := range all {
		// A standalone judge run follows its own writer run within the same attempt;
		// late usage events on the writer row do not make the writer newer than its judge.
		if !strings.HasSuffix(row.runID, "-judge") && presentIDs[judgeRunID(row.runID)] {
			continue
		}
		filtered = append(filtered, row)
	}
	all = filtered
	if len(all) == 0 {
		return itemRunOrderRow{}, false, nil
	}

	for i := range all {
		var endingSeq sql.NullInt64
		if err := r.QueryRow(
			`SELECT MAX(seq) FROM events WHERE object_id = ? AND kind IN (?, ?, ?, ?)`,
			all[i].runID, run.FinishKind, RefusalKind, GateVerdictKind, "land.landing",
		).Scan(&endingSeq); err != nil && !isMissingSchemaErr(err) {
			return itemRunOrderRow{}, false, err
		}
		if endingSeq.Valid && endingSeq.Int64 > all[i].seq {
			all[i].seq = endingSeq.Int64
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].seq != all[j].seq {
			return all[i].seq > all[j].seq
		}
		if all[i].startedAt != all[j].startedAt {
			return all[i].startedAt > all[j].startedAt
		}
		return all[i].rowID > all[j].rowID
	})
	return all[0], true, nil
}

func synthesizePausedJudgeWork(r *store.Reader, def definition.Definition, itemID string, info pausedJudgeHoldInfo) (*pendingWork, error) {
	itemSeat := info.seat
	if itemSeat == "" {
		var s sql.NullString
		if err := r.QueryRow(`SELECT seat FROM items WHERE item_id = ?`, itemID).Scan(&s); err == nil && s.String != "" {
			itemSeat = s.String
		}
	}
	req := run.Request{
		RunID:    info.runID,
		ItemID:   itemID,
		LaneID:   info.laneID,
		Backend:  info.backend,
		Seat:     itemSeat,
		Workflow: info.workflow,
		Node:     info.node,
		NodeID:   info.node,
	}
	d := Decision{
		ItemID:      itemID,
		LaneID:      info.laneID,
		Seat:        itemSeat,
		Attempt:     1,
		NodeBaseSHA: info.headSHA,
		RunRequest:  &req,
	}
	if _, _, rawGraph, err := item.ReadWorkflowSelection(r, itemID); err == nil && rawGraph != "" && info.node != "" {
		var graph Workflow
		if json.Unmarshal([]byte(rawGraph), &graph) == nil {
			for idx, st := range graph.Stages {
				if st.ID == info.node {
					d.Workflow = &graph
					d.NodeIndex = idx
					if d.Seat == "" {
						d.Seat = st.Seat
						req.Seat = st.Seat
					}
					break
				}
			}
		}
	}
	var cand *Candidate
	if d.Workflow == nil && (strings.HasSuffix(info.runID, "-judge") || (info.writerRunID != "" && info.writerRunID != info.runID)) {
		writerID := info.writerRunID
		if writerID == "" {
			writerID = strings.TrimSuffix(info.runID, "-judge")
		}
		judgeReq := req
		if judgeReq.Seat == "" {
			judgeReq.Seat = "review"
		}
		cand = &Candidate{
			ItemID:  itemID,
			LaneID:  info.laneID,
			RunID:   writerID,
			BaseSHA: info.baseSHA,
			HeadSHA: info.headSHA,
			Judge:   &JudgeReceipt{Request: judgeReq},
		}
		writerReq := run.Request{
			RunID:   writerID,
			ItemID:  itemID,
			LaneID:  info.laneID,
			Backend: info.backend,
			Seat:    itemSeat,
		}
		d.RunRequest = &writerReq
	}
	identifyDecision(&d)
	return &pendingWork{
		Decision:            d,
		BaseSHA:             info.baseSHA,
		Candidate:           cand,
		Ready:               false,
		LaneReleased:        info.laneID == "",
		ReopenedPausedJudge: true,
		ReopenedHeadSHA:     info.headSHA,
		ReopenedBaseSHA:     info.baseSHA,
		ReopenedWriterRunID: info.writerRunID,
	}, nil
}

func (l *Loop) restoreReopenedPausedLane(ctx context.Context, work *pendingWork, d Decision) error {
	if l.nonLanding || work == nil || !work.ReopenedPausedJudge || d.LaneID == "" {
		return nil
	}
	laneDir := l.laneDirPath(d.LaneID)
	worktree := l.lanePath(d.LaneID)
	if _, err := os.Stat(worktree); err == nil {
		dirty, err := l.uncommittedPaths(ctx, worktree)
		if err != nil {
			return err
		}
		if len(dirty) > 0 {
			return laneRefusalf(d.LaneID, ReasonLaneUncommittedWork,
				"commit or remove the files, or reset the lane deliberately (tillr-drain README, \"Resetting a lane\")",
				"lane %s has %d uncommitted paths (%s) before restoring paused candidate", d.LaneID, len(dirty), dirty[0])
		}
	}

	stageRunID := ""
	pinRunID := ""
	wantHead := strings.TrimSpace(work.ReopenedHeadSHA)
	wantBase := strings.TrimSpace(work.ReopenedBaseSHA)
	if work.Candidate != nil {
		stageRunID = work.Candidate.RunID
		pinRunID = work.Candidate.RunID
		if work.Candidate.HeadSHA != "" {
			wantHead = work.Candidate.HeadSHA
		}
		if work.Candidate.BaseSHA != "" {
			wantBase = work.Candidate.BaseSHA
		}
	} else if d.RunRequest != nil {
		stageRunID = d.RunRequest.RunID
		if work.ReopenedWriterRunID != "" {
			stageRunID = work.ReopenedWriterRunID
		}
		pinRunID = d.RunRequest.RunID
		if wantHead == "" {
			wantHead = strings.TrimSpace(d.NodeBaseSHA)
		}
	}
	if wantBase == "" {
		wantBase = strings.TrimSpace(work.BaseSHA)
	}

	if stageRunID != "" {
		runDir := RunDir(l.cfg.Root, stageRunID)
		if info, err := os.Stat(runDir); err == nil && info.IsDir() {
			for _, name := range []string{TargetFile, PatchRefFile} {
				src := filepath.Join(runDir, name)
				dst := filepath.Join(laneDir, name)
				if raw, readErr := os.ReadFile(src); readErr == nil {
					if writeErr := os.WriteFile(dst, raw, 0644); writeErr != nil {
						return writeErr
					}
				} else if os.IsNotExist(readErr) {
					if rmErr := os.Remove(dst); rmErr != nil && !os.IsNotExist(rmErr) {
						return rmErr
					}
				} else {
					return readErr
				}
			}
			for _, name := range []string{BriefFile, FenceFile, BaseSHAFile, ItemBaseSHAFile, ItemFenceFile, SessionIDFile, endingCapabilityFile, braceLiteralsFile, "conflict.json"} {
				src := filepath.Join(runDir, name)
				raw, readErr := os.ReadFile(src)
				if os.IsNotExist(readErr) {
					continue
				}
				if readErr != nil {
					return readErr
				}
				if writeErr := os.WriteFile(filepath.Join(laneDir, name), raw, 0644); writeErr != nil {
					return writeErr
				}
			}
		}
	}
	if wantBase != "" && l.laneBase(d.LaneID) == "" {
		if err := os.MkdirAll(laneDir, 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(laneDir, BaseSHAFile), []byte(wantBase+"\n"), 0644); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(laneDir, ItemBaseSHAFile)); os.IsNotExist(err) {
			if err := os.WriteFile(filepath.Join(laneDir, ItemBaseSHAFile), []byte(wantBase+"\n"), 0644); err != nil {
				return err
			}
		}
	}

	target := l.laneTarget(d.LaneID)
	if target != worktree {
		if _, err := os.Stat(target); err == nil {
			dirty, err := l.uncommittedPaths(ctx, target)
			if err != nil {
				return err
			}
			if len(dirty) > 0 {
				return laneRefusalf(d.LaneID, ReasonLaneUncommittedWork,
					"commit or remove the files, or reset the lane deliberately (tillr-drain README, \"Resetting a lane\")",
					"lane %s target has %d uncommitted paths (%s) before restoring paused candidate", d.LaneID, len(dirty), dirty[0])
			}
		}
	}

	pinRepo := l.lanePinRepository(d.LaneID)
	if wantHead == "" && pinRunID != "" {
		if pinned, err := l.revParse(ctx, pinRepo, pinRef(pinRunID)+"^{commit}"); err == nil {
			wantHead = strings.TrimSpace(pinned)
		}
	}
	if wantHead == "" {
		return nil
	}

	if _, err := os.Stat(target); os.IsNotExist(err) && target == worktree {
		trunk, err := l.revParse(ctx, l.cfg.Root, l.trunkRef())
		if err != nil {
			return err
		}
		stager := &LaneStager{Root: l.cfg.Root, LaneRoot: l.cfg.LaneRoot, Store: l.cfg.Store, Now: l.cfg.Now}
		if err := stager.ensureWorktree(ctx, StageSpec{LaneID: d.LaneID, ItemID: d.ItemID, RunID: stageRunID, BaseSHA: trunk}, laneDir, worktree); err != nil {
			return err
		}
	}

	curHead, err := l.revParse(ctx, target, "HEAD")
	if err == nil && strings.TrimSpace(curHead) == wantHead {
		return nil
	}
	if _, err := l.git(ctx, target, "cat-file", "-e", wantHead+"^{commit}"); err != nil {
		return fmt.Errorf("paused candidate commit %s is missing from %s: %w", wantHead, d.LaneID, err)
	}
	if target == worktree {
		if _, err := l.git(ctx, target, "checkout", "--force", "-B", LaneBranch(l.cfg.LaneRoot, d.LaneID), wantHead); err != nil {
			return fmt.Errorf("restoring paused candidate %s on %s: %w", shortSHA(wantHead), d.LaneID, err)
		}
		return nil
	}
	if _, err := l.git(ctx, target, "checkout", "--force", wantHead); err != nil {
		return fmt.Errorf("restoring paused fork candidate %s on %s: %w", shortSHA(wantHead), d.LaneID, err)
	}
	return nil
}
