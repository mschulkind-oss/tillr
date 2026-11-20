package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	runSupersededKind     = "drain.run-superseded"
	staleRunsRepairedKind = "drain.stale-runs-repaired"
)

func init() {
	for _, kind := range []string{runSupersededKind, staleRunsRepairedKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

type runSuperseded struct {
	Reason   string `json:"reason"`
	PriorSeq int64  `json:"prior_seq,omitempty"`
}

type staleRunsRepaired struct {
	SettledReadOnly []string `json:"settled_read_only,omitempty"`
	EndedNodeRuns   []string `json:"ended_node_runs,omitempty"`
	EndedRuns       []string `json:"ended_runs,omitempty"`
	ClearedPauses   []string `json:"cleared_pauses,omitempty"`
	ReleasedLanes   []string `json:"released_lanes,omitempty"`
}

func tableExists(r *store.Reader, name string) (bool, error) {
	var exists bool
	err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, name).Scan(&exists)
	return exists, err
}

func isTerminalRunState(state string) bool {
	return state == run.StateExited || state == run.StateFailedToStart || state == run.StateTimedOut
}

// endNonTerminalRunRow closes one non-terminal runs row (e.g. paused or running) through
// run.Apply so runs, run.finish, read_only_runs, and matching lanes update canonically.
// When markSuperseded is true, it appends drain.run-superseded with the row's prior
// event_seq first so readPriorRuns does not reorder this run ahead of newer attempts.
func endNonTerminalRunRow(ctx context.Context, tx *store.Tx, runID, reason string, markSuperseded bool, at time.Time) error {
	var itemID, route, harness sql.NullString
	var laneID, backend, state, artifactRefs string
	var startedMS int64
	var usageTokens sql.NullInt64
	var usageCost sql.NullFloat64
	var eventSeq int64
	err := tx.Reader().QueryRow(
		`SELECT item_id, lane_id, backend, route, harness, state, started_at, artifact_refs, usage_tokens, usage_cost_usd, event_seq
		   FROM runs WHERE run_id = ?`,
		runID,
	).Scan(&itemID, &laneID, &backend, &route, &harness, &state, &startedMS, &artifactRefs, &usageTokens, &usageCost, &eventSeq)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if isTerminalRunState(state) {
		return nil
	}

	var rec run.Record
	var finishPayload []byte
	err = tx.Reader().QueryRow(
		`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq DESC LIMIT 1`,
		run.FinishKind, runID,
	).Scan(&finishPayload)
	if err == nil {
		if err := json.Unmarshal(finishPayload, &rec); err != nil {
			return err
		}
	} else if err != sql.ErrNoRows {
		return err
	}

	rec.RunID = runID
	if rec.ItemID == "" && itemID.Valid {
		rec.ItemID = itemID.String
	}
	if rec.LaneID == "" {
		rec.LaneID = laneID
	}
	if rec.Backend == "" {
		rec.Backend = backend
	}
	if rec.Route == "" && route.Valid {
		rec.Route = route.String
	}
	if rec.Harness == "" && harness.Valid {
		rec.Harness = harness.String
	}
	if rec.ArtifactRefs == nil {
		var refs []run.ArtifactRef
		if artifactRefs != "" {
			if err := json.Unmarshal([]byte(artifactRefs), &refs); err != nil {
				refs = []run.ArtifactRef{}
			}
		}
		if refs == nil {
			refs = []run.ArtifactRef{}
		}
		rec.ArtifactRefs = refs
	}
	if rec.Usage == nil && (usageTokens.Valid || usageCost.Valid) {
		u := &run.Usage{}
		if usageTokens.Valid {
			v := usageTokens.Int64
			u.Tokens = &v
		}
		if usageCost.Valid {
			v := usageCost.Float64
			u.CostUSD = &v
		}
		rec.Usage = u
	}

	started := time.UnixMilli(startedMS).UTC()
	if rec.StartedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, rec.StartedAt); err == nil {
			started = parsed.UTC()
		}
	} else {
		rec.StartedAt = started.Format(time.RFC3339)
	}
	finished := at.UTC()
	if finished.Before(started) {
		finished = started
	}

	rec.State = run.StateTimedOut
	rec.ExitCode = nil
	rec.EndCause = run.EndLeaseReclaimed
	rec.SettledAt = ""
	rec.HarnessEndedAt = ""
	rec.RepeatingCall = nil
	rec.EndingException = ""
	rec.FinishedAt = finished.Format(time.RFC3339)

	if markSuperseded {
		if err := appendEffect(tx, runSupersededKind, runID, runSuperseded{Reason: reason, PriorSeq: eventSeq}, at.UTC()); err != nil {
			return err
		}
	}

	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := run.Apply(ctx, tx, record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          run.RecordKind,
		ID:            "run-end-" + runID,
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     at.UTC(),
		Subject:       &record.Subject{Kind: "run", ID: runID},
		Payload:       body,
	}); err != nil {
		return err
	}

	if hasPauses, err := tableExists(tx.Reader(), "quota_pauses"); err != nil {
		return err
	} else if hasPauses {
		if _, err := tx.Exec(`DELETE FROM quota_pauses WHERE run_id = ?`, runID); err != nil {
			return err
		}
	}
	return nil
}

func runBelongsToItem(r *store.Reader, runID, itemID string, hasReadOnly, hasNodes, hasDispatches bool) (bool, error) {
	var found bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs WHERE run_id=? AND item_id=?)`, runID, itemID).Scan(&found); err != nil {
		return false, err
	}
	if found {
		return true, nil
	}
	if hasReadOnly {
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM read_only_runs WHERE (run_id=? OR group_id=?) AND item_id=?)`, runID, runID, itemID).Scan(&found); err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	if hasNodes {
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM node_runs WHERE run_id=? AND item_id=?)`, runID, itemID).Scan(&found); err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	if hasDispatches {
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM dispatches WHERE run_id=? AND item_id=?)`, runID, itemID).Scan(&found); err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

func itemHasUnfinishedRuns(r *store.Reader, itemID, keepExaminingRunID string) (bool, error) {
	hasReadOnly, err := tableExists(r, "read_only_runs")
	if err != nil {
		return false, err
	}
	if hasReadOnly {
		var exists bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM read_only_runs WHERE item_id=? AND settled=0)`, itemID).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	hasNodes, err := tableExists(r, "node_runs")
	if err != nil {
		return false, err
	}
	if hasNodes {
		var exists bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM node_runs WHERE item_id=? AND outcome='running')`, itemID).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	hasPauses, err := tableExists(r, "quota_pauses")
	if err != nil {
		return false, err
	}
	if hasPauses {
		var exists bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM quota_pauses WHERE item_id=?)`, itemID).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	rows, err := r.Query(`SELECT run_id, state FROM runs WHERE item_id=?`, itemID)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var runID, state string
		if err := rows.Scan(&runID, &state); err != nil {
			rows.Close()
			return false, err
		}
		if runID != keepExaminingRunID && !isTerminalRunState(state) {
			rows.Close()
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()

	hasDispatches, err := tableExists(r, "dispatches")
	if err != nil {
		return false, err
	}
	keepGroupID := ""
	if keepExaminingRunID != "" && hasReadOnly {
		_ = r.QueryRow(`SELECT group_id FROM read_only_runs WHERE run_id=?`, keepExaminingRunID).Scan(&keepGroupID)
	}
	laneRows, err := r.Query(`SELECT lane_id, run_id FROM lanes WHERE state='leased' AND run_id IS NOT NULL`)
	if err != nil {
		return false, err
	}
	type leasedLane struct{ laneID, runID string }
	var leased []leasedLane
	for laneRows.Next() {
		var l leasedLane
		if err := laneRows.Scan(&l.laneID, &l.runID); err != nil {
			laneRows.Close()
			return false, err
		}
		leased = append(leased, l)
	}
	if err := laneRows.Err(); err != nil {
		laneRows.Close()
		return false, err
	}
	laneRows.Close()
	for _, l := range leased {
		if l.runID == keepExaminingRunID || (keepGroupID != "" && l.runID == keepGroupID) {
			continue
		}
		belongs, err := runBelongsToItem(r, l.runID, itemID, hasReadOnly, hasNodes, hasDispatches)
		if err != nil {
			return false, err
		}
		if belongs {
			return true, nil
		}
	}
	return false, nil
}

func (l *Loop) endItemUnfinishedRuns(ctx context.Context, itemID, keepExaminingRunID string) error {
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var needed bool
	err = r.ReadTx(ctx, func(rd *store.Reader) error {
		var err error
		needed, err = itemHasUnfinishedRuns(rd, itemID, keepExaminingRunID)
		return err
	})
	r.Close()
	if err != nil || !needed {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		return endItemUnfinishedRunsInTx(ctx, tx, itemID, keepExaminingRunID, l.cfg.Now().UTC())
	})
}

// endItemUnfinishedRunsInTx settles all unfinished run records for a terminal item
// (read_only_runs, node_runs, quota_pauses, non-terminal runs rows, and stranded lanes).
func endItemUnfinishedRunsInTx(ctx context.Context, tx *store.Tx, itemID, keepExaminingRunID string, at time.Time) error {
	r := tx.Reader()
	hasReadOnly, err := tableExists(r, "read_only_runs")
	if err != nil {
		return err
	}
	keepGroupID := ""
	if keepExaminingRunID != "" && hasReadOnly {
		_ = r.QueryRow(`SELECT group_id FROM read_only_runs WHERE run_id=?`, keepExaminingRunID).Scan(&keepGroupID)
	}
	if hasReadOnly {
		if _, err := tx.Exec(`UPDATE read_only_runs SET settled=1 WHERE item_id=? AND settled=0`, itemID); err != nil {
			return err
		}
	}
	hasNodes, err := tableExists(r, "node_runs")
	if err != nil {
		return err
	}
	if hasNodes {
		if _, err := tx.Exec(`UPDATE node_runs SET outcome='superseded' WHERE item_id=? AND outcome='running'`, itemID); err != nil {
			return err
		}
	}
	hasPauses, err := tableExists(r, "quota_pauses")
	if err != nil {
		return err
	}
	if hasPauses {
		if _, err := tx.Exec(`DELETE FROM quota_pauses WHERE item_id=?`, itemID); err != nil {
			return err
		}
	}

	rows, err := r.Query(`SELECT run_id, state FROM runs WHERE item_id=?`, itemID)
	if err != nil {
		return err
	}
	var toEnd []string
	for rows.Next() {
		var runID, state string
		if err := rows.Scan(&runID, &state); err != nil {
			rows.Close()
			return err
		}
		if runID != keepExaminingRunID && !isTerminalRunState(state) {
			toEnd = append(toEnd, runID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, runID := range toEnd {
		if err := endNonTerminalRunRow(ctx, tx, runID, "item "+itemID+" finished", false, at); err != nil {
			return err
		}
	}

	hasDispatches, err := tableExists(r, "dispatches")
	if err != nil {
		return err
	}
	laneRows, err := r.Query(`SELECT lane_id, run_id FROM lanes WHERE state='leased' AND run_id IS NOT NULL`)
	if err != nil {
		return err
	}
	type leasedLane struct{ laneID, runID string }
	var leased []leasedLane
	for laneRows.Next() {
		var l leasedLane
		if err := laneRows.Scan(&l.laneID, &l.runID); err != nil {
			laneRows.Close()
			return err
		}
		leased = append(leased, l)
	}
	if err := laneRows.Err(); err != nil {
		laneRows.Close()
		return err
	}
	laneRows.Close()

	for _, l := range leased {
		if l.runID == keepExaminingRunID || (keepGroupID != "" && l.runID == keepGroupID) {
			continue
		}
		belongs, err := runBelongsToItem(r, l.runID, itemID, hasReadOnly, hasNodes, hasDispatches)
		if err != nil {
			return err
		}
		if belongs {
			if _, err := run.ReleaseLaneInTx(ctx, tx, l.laneID, "item "+itemID+" finished", at); err != nil {
				return err
			}
		}
	}
	return nil
}

// supersedeItemUnfinishedRunsInTx ends unfinished run records for itemID that are not in keep
// (and node_runs from attempts < attempt). Writes 0 events when nothing is superseded.
func supersedeItemUnfinishedRunsInTx(ctx context.Context, tx *store.Tx, itemID string, attempt int, keep map[string]bool, reason string, at time.Time) error {
	return supersedeItemUnfinishedRunsInTxAt(ctx, tx, itemID, attempt, false, keep, reason, at)
}

func supersedeItemUnfinishedRunsInTxAt(ctx context.Context, tx *store.Tx, itemID string, attempt int, includeAttempt bool, keep map[string]bool, reason string, at time.Time) error {
	r := tx.Reader()
	hasNodes, err := tableExists(r, "node_runs")
	if err != nil {
		return err
	}
	if hasNodes {
		op := `<`
		if includeAttempt {
			op = `<=`
		}
		if _, err := tx.Exec(`UPDATE node_runs SET outcome='superseded' WHERE item_id=? AND attempt`+op+`? AND outcome='running'`, itemID, attempt); err != nil {
			return err
		}
	}
	hasReadOnly, err := tableExists(r, "read_only_runs")
	if err != nil {
		return err
	}
	if hasReadOnly {
		roRows, err := r.Query(`SELECT run_id FROM read_only_runs WHERE item_id=? AND settled=0`, itemID)
		if err != nil {
			return err
		}
		var settleIDs []string
		for roRows.Next() {
			var id string
			if err := roRows.Scan(&id); err != nil {
				roRows.Close()
				return err
			}
			if !keep[id] {
				settleIDs = append(settleIDs, id)
			}
		}
		if err := roRows.Err(); err != nil {
			roRows.Close()
			return err
		}
		roRows.Close()
		for _, id := range settleIDs {
			if _, err := tx.Exec(`UPDATE read_only_runs SET settled=1 WHERE run_id=?`, id); err != nil {
				return err
			}
		}
	}
	hasPauses, err := tableExists(r, "quota_pauses")
	if err != nil {
		return err
	}
	if hasPauses {
		pRows, err := r.Query(`SELECT run_id FROM quota_pauses WHERE item_id=?`, itemID)
		if err != nil {
			return err
		}
		var pauseIDs []string
		for pRows.Next() {
			var id string
			if err := pRows.Scan(&id); err != nil {
				pRows.Close()
				return err
			}
			if !keep[id] {
				pauseIDs = append(pauseIDs, id)
			}
		}
		if err := pRows.Err(); err != nil {
			pRows.Close()
			return err
		}
		pRows.Close()
		for _, id := range pauseIDs {
			if _, err := tx.Exec(`DELETE FROM quota_pauses WHERE run_id=?`, id); err != nil {
				return err
			}
		}
	}

	runRows, err := r.Query(`SELECT run_id, state FROM runs WHERE item_id=?`, itemID)
	if err != nil {
		return err
	}
	var toEnd []string
	for runRows.Next() {
		var runID, state string
		if err := runRows.Scan(&runID, &state); err != nil {
			runRows.Close()
			return err
		}
		if !keep[runID] && !isTerminalRunState(state) {
			toEnd = append(toEnd, runID)
		}
	}
	if err := runRows.Err(); err != nil {
		runRows.Close()
		return err
	}
	runRows.Close()
	for _, runID := range toEnd {
		if err := endNonTerminalRunRow(ctx, tx, runID, reason, true, at); err != nil {
			return err
		}
	}
	return nil
}

type nodeRunKey struct {
	itemID  string
	attempt int
	nodeID  string
	runID   string
}

type staleRepairPlan struct {
	settleReadOnly []string
	supersedeNodes []nodeRunKey
	deleteNodes    []nodeRunKey
	endRuns        []struct {
		runID          string
		reason         string
		markSuperseded bool
	}
	clearPauses  []string
	releaseLanes []struct {
		laneID string
		reason string
	}
}

func (p *staleRepairPlan) empty() bool {
	return len(p.settleReadOnly) == 0 &&
		len(p.supersedeNodes) == 0 &&
		len(p.deleteNodes) == 0 &&
		len(p.endRuns) == 0 &&
		len(p.clearPauses) == 0 &&
		len(p.releaseLanes) == 0
}

func decisionKeepSet(d Decision) map[string]bool {
	keep := map[string]bool{}
	for _, n := range parallelNodes(d) {
		if n.RunRequest != nil {
			keep[n.RunRequest.RunID] = true
		}
	}
	return keep
}

func decisionNodeSet(d Decision) map[string]bool {
	nodes := map[string]bool{}
	for _, n := range parallelNodes(d) {
		if n.RunRequest != nil && n.RunRequest.NodeID != "" {
			nodes[n.RunRequest.NodeID] = true
		} else if n.Workflow != nil && n.NodeIndex >= 0 && n.NodeIndex < len(n.Workflow.Stages) {
			nodes[n.Workflow.Stages[n.NodeIndex].ID] = true
		}
	}
	return nodes
}

func planStaleRunRepairs(r *store.Reader) (*staleRepairPlan, error) {
	plan := &staleRepairPlan{}
	declared, err := definition.ForReader(r)
	if err != nil {
		return nil, err
	}
	items, err := readItems(r)
	if err != nil {
		return nil, err
	}
	terminalItems := map[string]bool{}
	for _, row := range items {
		terminalItems[row.ItemID] = finishedState(declared.Parsed, row.State)
	}
	works, err := readPending(r)
	if err != nil {
		return nil, err
	}

	hasReadOnly, err := tableExists(r, "read_only_runs")
	if err != nil {
		return nil, err
	}
	hasNodes, err := tableExists(r, "node_runs")
	if err != nil {
		return nil, err
	}
	hasPauses, err := tableExists(r, "quota_pauses")
	if err != nil {
		return nil, err
	}
	hasDispatches, err := tableExists(r, "dispatches")
	if err != nil {
		return nil, err
	}

	maxAttempt := map[string]int{}
	for id, w := range works {
		if w.Decision.Attempt > maxAttempt[id] {
			maxAttempt[id] = w.Decision.Attempt
		}
	}
	if hasNodes {
		aRows, err := r.Query(`SELECT item_id, COALESCE(MAX(attempt), 0) FROM node_runs GROUP BY item_id`)
		if err != nil {
			return nil, err
		}
		for aRows.Next() {
			var id string
			var att int
			if err := aRows.Scan(&id, &att); err != nil {
				aRows.Close()
				return nil, err
			}
			if att > maxAttempt[id] {
				maxAttempt[id] = att
			}
		}
		if err := aRows.Err(); err != nil {
			aRows.Close()
			return nil, err
		}
		aRows.Close()
	}

	// Leased lanes map: lane_id -> run_id
	leasedLane := map[string]string{}
	laneRows, err := r.Query(`SELECT lane_id, run_id FROM lanes WHERE state='leased' AND run_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	for laneRows.Next() {
		var laneID, runID string
		if err := laneRows.Scan(&laneID, &runID); err != nil {
			laneRows.Close()
			return nil, err
		}
		leasedLane[laneID] = runID
	}
	if err := laneRows.Err(); err != nil {
		laneRows.Close()
		return nil, err
	}
	laneRows.Close()

	// All runs rows for quick lookup of existence, state, and latest per-item run.
	runState := map[string]string{}
	itemHasNewerRun := map[string]map[string]bool{}
	{
		type runInfo struct {
			runID     string
			itemID    string
			state     string
			startedAt int64
			rowid     int64
		}
		byItem := map[string][]runInfo{}
		rRows, err := r.Query(`SELECT run_id, COALESCE(item_id, ''), state, started_at, rowid FROM runs`)
		if err != nil {
			return nil, err
		}
		for rRows.Next() {
			var ri runInfo
			if err := rRows.Scan(&ri.runID, &ri.itemID, &ri.state, &ri.startedAt, &ri.rowid); err != nil {
				rRows.Close()
				return nil, err
			}
			runState[ri.runID] = ri.state
			if ri.itemID != "" {
				byItem[ri.itemID] = append(byItem[ri.itemID], ri)
			}
		}
		if err := rRows.Err(); err != nil {
			rRows.Close()
			return nil, err
		}
		rRows.Close()
		for itemID, list := range byItem {
			sort.SliceStable(list, func(i, j int) bool {
				if list[i].startedAt != list[j].startedAt {
					return list[i].startedAt < list[j].startedAt
				}
				return list[i].rowid < list[j].rowid
			})
			w := works[itemID]
			var decKeep map[string]bool
			if w != nil {
				decKeep = decisionKeepSet(w.Decision)
			}
			// Mark any run that has a subsequent run not in the same decision group.
			newerNonMemberSeen := false
			itemHasNewerRun[itemID] = map[string]bool{}
			for i := len(list) - 1; i >= 0; i-- {
				ri := list[i]
				if newerNonMemberSeen {
					itemHasNewerRun[itemID][ri.runID] = true
				}
				if decKeep == nil || !decKeep[ri.runID] {
					newerNonMemberSeen = true
				}
			}
		}
	}

	// 1. read_only_runs
	if hasReadOnly {
		roRows, err := r.Query(`SELECT run_id, group_id, item_id, lane_id FROM read_only_runs WHERE settled=0 ORDER BY run_id`)
		if err != nil {
			return nil, err
		}
		for roRows.Next() {
			var runID, groupID, itemID, laneID string
			if err := roRows.Scan(&runID, &groupID, &itemID, &laneID); err != nil {
				roRows.Close()
				return nil, err
			}
			w := works[itemID]
			active := w != nil && !w.Done
			var keep map[string]bool
			if active {
				keep = decisionKeepSet(w.Decision)
			}
			if terminalItems[itemID] {
				if active && keep[runID] {
					continue
				}
				plan.settleReadOnly = append(plan.settleReadOnly, runID)
				continue
			}
			if active && keep[runID] {
				continue
			}
			if active && leasedLane[laneID] == groupID {
				continue
			}
			plan.settleReadOnly = append(plan.settleReadOnly, runID)
		}
		if err := roRows.Err(); err != nil {
			roRows.Close()
			return nil, err
		}
		roRows.Close()
	}

	// 2. node_runs
	if hasNodes {
		nRows, err := r.Query(`SELECT item_id, attempt, node_id, run_id FROM node_runs WHERE outcome='running' ORDER BY item_id, attempt, node_id`)
		if err != nil {
			return nil, err
		}
		for nRows.Next() {
			var k nodeRunKey
			if err := nRows.Scan(&k.itemID, &k.attempt, &k.nodeID, &k.runID); err != nil {
				nRows.Close()
				return nil, err
			}
			w := works[k.itemID]
			active := w != nil && !w.Done
			var keep map[string]bool
			if active {
				keep = decisionKeepSet(w.Decision)
			}
			if terminalItems[k.itemID] {
				if active && keep[k.runID] {
					continue
				}
				plan.supersedeNodes = append(plan.supersedeNodes, k)
				continue
			}
			if k.attempt < maxAttempt[k.itemID] {
				plan.supersedeNodes = append(plan.supersedeNodes, k)
				continue
			}
			if active && !keep[k.runID] && !(w.Ready && k.attempt == w.Decision.Attempt && decisionNodeSet(w.Decision)[k.nodeID]) {
				plan.supersedeNodes = append(plan.supersedeNodes, k)
				continue
			}
			if !active {
				if _, hasRun := runState[k.runID]; !hasRun {
					plan.deleteNodes = append(plan.deleteNodes, k)
				}
			}
		}
		if err := nRows.Err(); err != nil {
			nRows.Close()
			return nil, err
		}
		nRows.Close()
	}

	// 3. non-terminal runs rows
	rRows, err := r.Query(`SELECT run_id, COALESCE(item_id, ''), state FROM runs WHERE state NOT IN ('exited','failed-to-start','timed-out') ORDER BY run_id`)
	if err != nil {
		return nil, err
	}
	for rRows.Next() {
		var runID, itemID, state string
		if err := rRows.Scan(&runID, &itemID, &state); err != nil {
			rRows.Close()
			return nil, err
		}
		if itemID == "" {
			continue
		}
		w := works[itemID]
		active := w != nil && !w.Done
		var activeKeep map[string]bool
		if active {
			activeKeep = decisionKeepSet(w.Decision)
		}
		if terminalItems[itemID] {
			if active && activeKeep[runID] {
				continue
			}
			plan.endRuns = append(plan.endRuns, struct {
				runID          string
				reason         string
				markSuperseded bool
			}{runID: runID, reason: "item " + itemID + " finished", markSuperseded: false})
			continue
		}
		if w == nil {
			plan.endRuns = append(plan.endRuns, struct {
				runID          string
				reason         string
				markSuperseded bool
			}{runID: runID, reason: "stale superseded run repaired", markSuperseded: true})
			continue
		}
		if active {
			if !activeKeep[runID] {
				plan.endRuns = append(plan.endRuns, struct {
					runID          string
					reason         string
					markSuperseded bool
				}{runID: runID, reason: "stale superseded run repaired", markSuperseded: true})
			}
			continue
		}
		// w.Done is true. Keep only a resumable paused run in parallelNodes(w.Decision) with no newer attempt/run.
		decKeep := decisionKeepSet(w.Decision)
		hasNewer := maxAttempt[itemID] > w.Decision.Attempt || itemHasNewerRun[itemID][runID]
		if state == run.StatePaused && decKeep[runID] && !hasNewer {
			continue
		}
		plan.endRuns = append(plan.endRuns, struct {
			runID          string
			reason         string
			markSuperseded bool
		}{runID: runID, reason: "stale superseded run repaired", markSuperseded: true})
	}
	if err := rRows.Err(); err != nil {
		rRows.Close()
		return nil, err
	}
	rRows.Close()

	// 4. quota_pauses
	if hasPauses {
		pRows, err := r.Query(`SELECT run_id, item_id FROM quota_pauses ORDER BY run_id`)
		if err != nil {
			return nil, err
		}
		for pRows.Next() {
			var runID, itemID string
			if err := pRows.Scan(&runID, &itemID); err != nil {
				pRows.Close()
				return nil, err
			}
			w := works[itemID]
			active := w != nil && !w.Done
			var keep map[string]bool
			if active {
				keep = decisionKeepSet(w.Decision)
			}
			if terminalItems[itemID] {
				if active && keep[runID] {
					continue
				}
				plan.clearPauses = append(plan.clearPauses, runID)
				continue
			}
			if st, exists := runState[runID]; exists && isTerminalRunState(st) {
				plan.clearPauses = append(plan.clearPauses, runID)
				continue
			}
			if w == nil || (w.Done && !decisionKeepSet(w.Decision)[runID]) || (active && !keep[runID]) {
				plan.clearPauses = append(plan.clearPauses, runID)
			}
		}
		if err := pRows.Err(); err != nil {
			pRows.Close()
			return nil, err
		}
		pRows.Close()
	}

	// 5. stranded lanes for terminal items
	endedByRun := map[string]bool{}
	for _, er := range plan.endRuns {
		endedByRun[er.runID] = true
	}
	var laneIDs []string
	for laneID := range leasedLane {
		laneIDs = append(laneIDs, laneID)
	}
	sort.Strings(laneIDs)
	for _, laneID := range laneIDs {
		runID := leasedLane[laneID]
		if endedByRun[runID] {
			continue
		}
		for itemID, isTerm := range terminalItems {
			if !isTerm {
				continue
			}
			w := works[itemID]
			if w != nil && !w.Done {
				keep := decisionKeepSet(w.Decision)
				keepGroupID := ""
				if w.Decision.RunRequest != nil {
					if keep[runID] || runID == w.Decision.RunRequest.RunID {
						continue
					}
					if hasReadOnly {
						_ = r.QueryRow(`SELECT group_id FROM read_only_runs WHERE run_id=?`, w.Decision.RunRequest.RunID).Scan(&keepGroupID)
						if keepGroupID != "" && runID == keepGroupID {
							continue
						}
					}
				}
			}
			belongs, err := runBelongsToItem(r, runID, itemID, hasReadOnly, hasNodes, hasDispatches)
			if err != nil {
				return nil, err
			}
			if belongs {
				plan.releaseLanes = append(plan.releaseLanes, struct {
					laneID string
					reason string
				}{laneID: laneID, reason: "item " + itemID + " finished"})
				break
			}
		}
	}

	return plan, nil
}

func (l *Loop) repairStaleRunRecords(ctx context.Context) error {
	if l.staleRunsRepaired {
		return nil
	}
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var alreadyRepaired bool
	var plan *staleRepairPlan
	err = r.ReadTx(ctx, func(rd *store.Reader) error {
		if err := rd.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind = ?)`, staleRunsRepairedKind).Scan(&alreadyRepaired); err != nil {
			return err
		}
		if alreadyRepaired {
			return nil
		}
		var err error
		plan, err = planStaleRunRepairs(rd)
		return err
	})
	r.Close()
	if err != nil {
		return err
	}
	if alreadyRepaired || plan.empty() {
		l.staleRunsRepaired = true
		return nil
	}

	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	at := l.cfg.Now().UTC()
	var summary staleRunsRepaired
	var didRepair bool
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var done bool
		if err := tx.Reader().QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind = ?)`, staleRunsRepairedKind).Scan(&done); err != nil {
			return err
		}
		if done {
			l.staleRunsRepaired = true
			return nil
		}
		p, err := planStaleRunRepairs(tx.Reader())
		if err != nil {
			return err
		}
		if p.empty() {
			l.staleRunsRepaired = true
			return nil
		}
		for _, id := range p.settleReadOnly {
			if _, err := tx.Exec(`UPDATE read_only_runs SET settled=1 WHERE run_id=?`, id); err != nil {
				return err
			}
			summary.SettledReadOnly = append(summary.SettledReadOnly, id)
		}
		for _, k := range p.supersedeNodes {
			if _, err := tx.Exec(`UPDATE node_runs SET outcome='superseded' WHERE item_id=? AND attempt=? AND node_id=? AND outcome='running'`, k.itemID, k.attempt, k.nodeID); err != nil {
				return err
			}
			summary.EndedNodeRuns = append(summary.EndedNodeRuns, fmt.Sprintf("%s#%d/%s(%s)", k.itemID, k.attempt, k.nodeID, k.runID))
		}
		for _, k := range p.deleteNodes {
			if _, err := tx.Exec(`DELETE FROM node_runs WHERE item_id=? AND attempt=? AND node_id=? AND outcome='running'`, k.itemID, k.attempt, k.nodeID); err != nil {
				return err
			}
			summary.EndedNodeRuns = append(summary.EndedNodeRuns, fmt.Sprintf("%s#%d/%s(%s)", k.itemID, k.attempt, k.nodeID, k.runID))
		}
		for _, er := range p.endRuns {
			if err := endNonTerminalRunRow(ctx, tx, er.runID, er.reason, er.markSuperseded, at); err != nil {
				return err
			}
			summary.EndedRuns = append(summary.EndedRuns, er.runID)
		}
		for _, id := range p.clearPauses {
			if _, err := tx.Exec(`DELETE FROM quota_pauses WHERE run_id=?`, id); err != nil {
				return err
			}
			summary.ClearedPauses = append(summary.ClearedPauses, id)
		}
		for _, rl := range p.releaseLanes {
			if _, err := run.ReleaseLaneInTx(ctx, tx, rl.laneID, rl.reason, at); err != nil {
				return err
			}
			summary.ReleasedLanes = append(summary.ReleasedLanes, rl.laneID)
		}
		objID := l.sessionID
		if objID == "" {
			objID = "stale-runs-repair"
		}
		if err := appendEffectOn(tx, staleRunsRepairedKind, "drain", objID, summary, at); err != nil {
			return err
		}
		didRepair = true
		return nil
	})
	if err != nil {
		return err
	}
	l.staleRunsRepaired = true
	if didRepair {
		l.reportf("loop: repaired stale unfinished run records (read_only=%d, node_runs=%d, runs=%d, pauses=%d, lanes=%d)\n",
			len(summary.SettledReadOnly), len(summary.EndedNodeRuns), len(summary.EndedRuns), len(summary.ClearedPauses), len(summary.ReleasedLanes))
	}
	return nil
}
