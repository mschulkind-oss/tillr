package drain

import (
	"database/sql"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

type leaseRunKey struct {
	laneID  string
	groupID string
}

type activeReadOnlyMember struct {
	runID  string
	itemID string
}

// collectActiveOrdinaryAgents returns the set of ordinary agent runs currently
// in progress, mapped from run key to lane id.
//
// Ordinary agent runs exclude:
//   - express items (item.Express == true) and express-* lanes,
//   - desk-* lanes (comment desks and desk-trunk-fixer),
//   - command stages (stage.Kind == "command" or Backend == "command").
//
// Parallel read-only agent members sharing one lane lease count individually
// (one entry per unsettled agent member in read_only_runs). An ordinary
// workflow continuing between two nodes keeps its place in the count across the
// settle pass and the ready-for-next-node pass so another item cannot take its
// slot during the handoff.
func collectActiveOrdinaryAgents(r *store.Reader, at time.Time, works, byRun map[string]*pendingWork, expressByID map[string]bool) (map[string]string, error) {
	active := map[string]string{}
	rawLanes, err := readRawLanes(r)
	if err != nil {
		return nil, err
	}
	roByLease := map[leaseRunKey][]activeReadOnlyMember{}
	hasReadOnly, err := tableExists(r, "read_only_runs")
	if err != nil {
		return nil, readFailure(err)
	}
	if hasReadOnly {
		rows, err := r.Query(`SELECT run_id, group_id, item_id, lane_id FROM read_only_runs WHERE settled = 0`)
		if err != nil {
			return nil, readFailure(err)
		}
		for rows.Next() {
			var runID, groupID, itemID, laneID string
			if err := rows.Scan(&runID, &groupID, &itemID, &laneID); err != nil {
				rows.Close()
				return nil, readFailure(err)
			}
			key := leaseRunKey{laneID: laneID, groupID: groupID}
			roByLease[key] = append(roByLease[key], activeReadOnlyMember{runID: runID, itemID: itemID})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, readFailure(err)
		}
		rows.Close()
	}

	nowMS := at.UnixMilli()
	leasedLanes := map[string]bool{}
	for _, row := range rawLanes {
		if row.state != LaneStateLeased {
			continue
		}
		if row.leasedUntil.Valid && row.leasedUntil.Int64 <= nowMS {
			continue
		}
		leasedLanes[row.laneID] = true
		if isExpressLane(row.laneID) || strings.HasPrefix(row.laneID, "desk-") {
			continue
		}
		runID := row.runID.String
		if members := roByLease[leaseRunKey{laneID: row.laneID, groupID: runID}]; len(members) > 0 {
			for _, m := range members {
				if expressByID[m.itemID] {
					continue
				}
				work := lookupPendingWork(works, byRun, m.itemID, m.runID, row.laneID)
				if work != nil && isCommandRunInWork(work, m.runID) {
					continue
				}
				active[m.runID] = row.laneID
			}
			continue
		}
		work := lookupPendingWork(works, byRun, "", runID, row.laneID)
		if work != nil {
			if expressByID[work.Decision.ItemID] {
				continue
			}
			if isCommandRunInWork(work, runID) {
				continue
			}
		}
		key := runID
		if key == "" {
			key = "lane:" + row.laneID
		}
		active[key] = row.laneID
	}

	// Keep ordinary workflows continuing between two nodes in the count across
	// both the settle pass (before nodeAdvancedKind) and the ready pass (after
	// nodeAdvancedKind, before the next node's lease is claimed).
	for itemID, work := range works {
		if work == nil || work.Done || work.LaneReleased || work.LaneBusyByOther {
			continue
		}
		if work.Candidate != nil || work.BoundaryFailed {
			continue
		}
		if work.DirtyHold != nil || work.RedCapHold != nil || work.RepairHold != nil || work.GateStall != nil {
			continue
		}
		if expressByID[itemID] {
			continue
		}
		lane := work.Decision.LaneID
		if lane == "" || isExpressLane(lane) || strings.HasPrefix(lane, "desk-") {
			continue
		}
		if work.Decision.Workflow == nil {
			continue
		}
		if work.Ready {
			nodes := parallelNodes(work.Decision)
			for _, node := range nodes {
				if node.RunRequest == nil || isCommandDecision(node) {
					continue
				}
				if len(nodes) > 1 {
					rec, err := terminalRecord(r, node.RunRequest.RunID)
					if err != nil {
						return nil, err
					}
					if rec != nil {
						continue
					}
				}
				active[node.RunRequest.RunID] = lane
			}
			continue
		}
		if leasedLanes[lane] {
			continue
		}
		paused, err := pausedPendingWork(r, work)
		if err != nil {
			return nil, err
		}
		if paused {
			continue
		}
		// Only hold a slot on the settle pass when the current node actually
		// finished (has a terminal record) and the workflow has a subsequent
		// ordinary agent node to run. An expired lease without a terminal
		// record does not hold a slot.
		finished, err := workCurrentNodeTerminal(r, work)
		if err != nil {
			return nil, err
		}
		if !finished {
			continue
		}
		hasNext, err := workflowHasNextAgentStage(r, work.Decision)
		if err != nil {
			return nil, err
		}
		if !hasNext {
			continue
		}
		key := workRunID(work)
		if key == "" {
			key = "continuing:" + itemID
		}
		active[key] = lane
	}

	return active, nil
}

func lookupPendingWork(works, byRun map[string]*pendingWork, itemID, runID, laneID string) *pendingWork {
	if runID != "" && byRun != nil {
		if w := byRun[runID]; w != nil {
			return w
		}
	}
	if itemID != "" && works != nil {
		if w := works[itemID]; w != nil {
			return w
		}
	}
	if works != nil && laneID != "" {
		for _, w := range works {
			if w == nil || w.Done || w.LaneReleased || w.Decision.LaneID != laneID {
				continue
			}
			if runID == "" || workMatchesRunID(w, runID) {
				return w
			}
		}
	}
	return nil
}

func workMatchesRunID(w *pendingWork, runID string) bool {
	if w == nil || runID == "" {
		return false
	}
	for _, node := range parallelNodes(w.Decision) {
		if node.RunRequest != nil && (node.RunRequest.RunID == runID || node.RunRequest.ReadOnlyGroup == runID) {
			return true
		}
	}
	return false
}

func isCommandDecision(d Decision) bool {
	return commandStage(d) || (d.RunRequest != nil && d.RunRequest.Backend == "command")
}

func isCommandRunInWork(work *pendingWork, runID string) bool {
	if work == nil {
		return false
	}
	if runID != "" {
		for _, node := range parallelNodes(work.Decision) {
			if node.RunRequest != nil && node.RunRequest.RunID == runID {
				return isCommandDecision(node)
			}
		}
	}
	return isCommandDecision(work.Decision)
}

func workCurrentNodeTerminal(r *store.Reader, work *pendingWork) (bool, error) {
	if work == nil {
		return false, nil
	}
	nodes := parallelNodes(work.Decision)
	sawRequest := false
	for _, node := range nodes {
		if node.RunRequest == nil {
			continue
		}
		sawRequest = true
		rec, err := terminalRecord(r, node.RunRequest.RunID)
		if err != nil {
			return false, err
		}
		if rec == nil {
			return false, nil
		}
	}
	return sawRequest, nil
}

func workflowHasOutgoingEdge(d Decision) bool {
	if d.Workflow == nil {
		return false
	}
	current := map[string]bool{}
	for _, node := range parallelNodes(d) {
		if node.NodeIndex >= 0 && node.NodeIndex < len(d.Workflow.Stages) {
			current[d.Workflow.Stages[node.NodeIndex].ID] = true
		}
	}
	evalSlot := evaluatorSlot(d.Workflow)
	for _, edge := range d.Workflow.Edges {
		if current[edge.From] && edge.To != evalSlot {
			return true
		}
	}
	return false
}

func workflowHasNextAgentStage(r *store.Reader, d Decision) (bool, error) {
	if d.Workflow == nil {
		return false, nil
	}
	evalSlot := evaluatorSlot(d.Workflow)
	stageByID := make(map[string]WorkflowStage, len(d.Workflow.Stages))
	for _, s := range d.Workflow.Stages {
		stageByID[s.ID] = s
	}
	current := map[string]bool{}
	for _, node := range parallelNodes(d) {
		if node.NodeIndex >= 0 && node.NodeIndex < len(d.Workflow.Stages) {
			current[d.Workflow.Stages[node.NodeIndex].ID] = true
		}
	}
	for _, edge := range d.Workflow.Edges {
		if !current[edge.From] || edge.To == evalSlot {
			continue
		}
		if s, ok := stageByID[edge.To]; ok && s.Kind != "command" {
			return true, nil
		}
	}
	if len(d.Workflow.Stages) <= 1 {
		return false, nil
	}
	outcomes, err := workflowOutcomes(r, d.ItemID, d.Attempt)
	if err != nil {
		return false, err
	}
	for _, s := range d.Workflow.Stages {
		if current[s.ID] || s.ID == evalSlot || s.Kind == "command" {
			continue
		}
		if outcomes[s.ID] == "" {
			return true, nil
		}
	}
	return false, nil
}

func expressByIDFromRows(items []itemRow) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, row := range items {
		if row.Express {
			out[row.ItemID] = true
		}
	}
	return out
}

func expressByIDFromMap(items map[string]itemRow) map[string]bool {
	out := make(map[string]bool, len(items))
	for id, row := range items {
		if row.Express {
			out[id] = true
		}
	}
	return out
}

func readExpressByID(r *store.Reader) (map[string]bool, error) {
	var hasExpress bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name='express')`).Scan(&hasExpress); err != nil {
		return nil, readFailure(err)
	}
	out := map[string]bool{}
	if !hasExpress {
		return out, nil
	}
	rows, err := r.Query(`SELECT item_id FROM items WHERE COALESCE(express, 0) != 0`)
	if err != nil {
		return nil, readFailure(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil && err != sql.ErrNoRows {
			return nil, readFailure(err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, readFailure(err)
	}
	return out, nil
}
