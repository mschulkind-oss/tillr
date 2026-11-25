package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// A claim (applyPlanGeneration) records its run as started before staging runs, and for
// an item with a conflict it also moves that conflict in the same write: a design-seat
// dispatch marks the conflict awaiting that design run, and a build dispatched against an
// answer closes the answer. Staging can still refuse the claimed run — most often because
// the lane it was offered holds a change nobody landed — and the claim is then cancelled
// (cancelStaging) before any run face starts.
//
// A run that was claimed but never started is no attempt: it has no terminal record, so
// the planner's attempt count does not include it and the next dispatch reuses its run
// id. Its conflict move must not outlive it either. Left in place, an awaiting conflict
// reads as "waits on work: design" with no design run alive, and a closed answer leaves
// the item with no authorization to build, held as run-link-missing or retry-stale for an
// operator. On 2026-11-17 six runs were cancelled that way on refusing lanes.

// conflictBeforeClaim answers the conflict as it stood before d's claim moved it, and
// whether the current record is that claim's move at all. A record any later act wrote —
// an answer, an owner disposition, another dispatch — is not the claim's and is left as is.
func conflictBeforeClaim(current item.Conflict, d Decision) (item.Conflict, bool) {
	if d.RunRequest == nil || current.ItemID != d.ItemID {
		return current, false
	}
	switch {
	case d.BlockConflict != nil:
		if current.ConflictID != d.BlockConflict.ConflictID || current.Status != "awaiting" || current.DesignRunID != d.RunRequest.RunID {
			return current, false
		}
		current.Status, current.WaitingOn, current.DesignRunID = "raised", "design", d.DesignContinuationFrom
		return current, true
	case d.ConflictID != "":
		if current.ConflictID != d.ConflictID || current.Status != "closed" {
			return current, false
		}
		current.Status, current.WaitingOn = "answered", "build"
		return current, true
	}
	return current, false
}

// undoClaimedConflict restores the conflict d's claim moved, in the caller's write. An
// item that went terminal keeps its record: recording a conflict would move its state.
func undoClaimedConflict(tx *store.Tx, d Decision, at time.Time) (bool, error) {
	conflicts, err := item.ReadConflicts(tx.Reader())
	if err != nil {
		return false, err
	}
	c, ok := conflicts[d.ItemID]
	if !ok {
		return false, nil
	}
	before, moved := conflictBeforeClaim(c, d)
	if !moved {
		return false, nil
	}
	if err := refuseTerminalItem(tx.Reader(), d.ItemID, "unlaunched-claim"); err != nil {
		return false, nil
	}
	return true, item.RecordConflict(tx, before, at)
}

// retractClaim undoes every store effect of a claim that never launched, in the caller's
// write. The claim (applyPlanTrunk) wrote, for each node it claimed: a dispatches row, a
// seat_dispatches row, a lane lease, a read_only_runs membership, a node_runs row marked
// running, node_reused rows for a retry's kept readers, and the item's conflict move. None
// of them is a run: no face started, so none has a terminal record, and every one that
// outlived the claim made the item look dispatched. The pending work is the last piece. A
// claim that continued a drain.node-advanced pending work has that work restored, so the
// planner offers the item's next node again; a claim with no such predecessor is examined
// (markExamined), which is what ends a pending work no run will ever finish.
//
// A node that has a terminal record, or a retained finished reader of a resumed parallel
// group, is a run and keeps its rows.
func retractClaim(tx *store.Tx, d Decision, at time.Time, markExamined bool) error {
	runID := d.RunRequest.RunID
	restored, err := restorePendingBeforeClaim(tx, d, at)
	if err != nil {
		return err
	}
	if !restored && markExamined {
		if err := appendEffect(tx, examinationDoneKind, runID, struct{}{}, at); err != nil {
			return err
		}
	}
	for _, node := range parallelNodes(d) {
		id := node.RunRequest.RunID
		// Finished readers may be retained in a resumed parallel group. Only
		// never-started claims lose their share count when staging is canceled.
		if _, err := tx.Exec(`DELETE FROM seat_dispatches WHERE run_id=? AND NOT EXISTS(SELECT 1 FROM runs WHERE run_id=?)`, id, id); err != nil {
			return err
		}
		// The claim wrote each reader's group membership. A never-started reader's
		// membership goes with the claim; a retained finished reader's, reopened by
		// the resume claim, is settled again. Either way no unsettled member is left
		// on a lane this transaction releases, the rule run.freeLane keeps for a
		// released group: an unsettled member reads back as a live run beside
		// whatever claims the lane next.
		if _, err := tx.Exec(`DELETE FROM read_only_runs WHERE run_id=? AND NOT EXISTS(SELECT 1 FROM runs WHERE run_id=?)`, id, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE read_only_runs SET settled=1 WHERE run_id=?`, id); err != nil {
			return err
		}
		// startNode marked the node running; a node with no run behind it is no node.
		if hasNodes, err := tableExists(tx.Reader(), "node_runs"); err != nil {
			return err
		} else if hasNodes {
			if _, err := tx.Exec(`DELETE FROM node_runs WHERE run_id=? AND outcome='running' AND NOT EXISTS(SELECT 1 FROM runs WHERE run_id=?)`, id, id); err != nil {
				return err
			}
		}
	}
	if d.Workflow != nil {
		// The kept readers were recorded at the attempt's claim. Once no node of the
		// attempt ran, the attempt never started and its reuse record goes with it.
		if _, err := tx.Exec(`DELETE FROM node_reused WHERE item_id=? AND attempt=? AND NOT EXISTS(SELECT 1 FROM node_runs WHERE item_id=? AND attempt=? AND outcome<>'skipped')`, d.ItemID, d.Attempt, d.ItemID, d.Attempt); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM dispatches WHERE decision_id=? AND item_id=? AND run_id=?`, d.DecisionID, d.ItemID, runID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=? AND (run_id=? OR run_id=?)`, "idle", d.LaneID, runID, d.RunRequest.ReadOnlyGroup); err != nil {
		return err
	}
	_, err = undoClaimedConflict(tx, d, at)
	return err
}

// clearSupersededNodeClaims removes abandoned claim projections before claiming
// work for the item again. They can occupy the very node/attempt slot being
// offered now, or shadow genuine outcomes at a higher, never-launched ordinal.
// Leave the historical events and every node backed by a run intact.
func clearSupersededNodeClaims(tx *store.Tx, itemID string) error {
	_, err := tx.Exec(`DELETE FROM node_runs WHERE item_id=? AND outcome='superseded'
 AND NOT EXISTS(SELECT 1 FROM runs WHERE run_id=node_runs.run_id)`, itemID)
	return err
}

// restorePendingBeforeClaim re-appends the pending work the claim of d continued, when
// there was one: the latest drain.node-advanced or drain.graph-changed event for d's run
// written before the claim's own drain.dispatch-started. The replay then reads that work
// again — not examined, Ready — as it stood before the claim. An item that has since gone
// terminal keeps no pending work, and the caller examines the run instead.
func restorePendingBeforeClaim(tx *store.Tx, d Decision, at time.Time) (bool, error) {
	runID := d.RunRequest.RunID
	if d.Reason == ReasonConflictFix {
		_, byRun, err := readPendingHistory(tx.Reader())
		if err != nil {
			return false, err
		}
		if fixWork := byRun[runID]; fixWork != nil && fixWork.ParentRunID != "" {
			return restoreConflictCandidateTx(tx, fixWork, at, true)
		}
	}
	var claimSeq int64
	if err := tx.Reader().QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events WHERE kind=? AND object_type='run' AND object_id=?`, dispatchStartedKind, runID).Scan(&claimSeq); err != nil {
		return false, err
	}
	if claimSeq == 0 {
		return false, nil
	}
	var kind string
	var payload []byte
	err := tx.Reader().QueryRow(`SELECT kind,payload FROM events WHERE kind IN (?,?) AND object_type='run' AND object_id=? AND seq<? ORDER BY seq DESC LIMIT 1`, nodeAdvancedKind, graphChangedKind, runID, claimSeq).Scan(&kind, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := refuseTerminalItem(tx.Reader(), d.ItemID, "unlaunched-claim"); err != nil {
		return false, nil
	}
	var work pendingWork
	if err := json.Unmarshal([]byte(payload), &work); err != nil {
		return false, err
	}
	if work.Ready && work.Candidate == nil && !work.LaneReleased && work.Decision.NodeIndex > 0 && awaitingForkClaim(&work) {
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return false, err
		}
		if fault := faults[d.ItemID]; fault.Reason == ReasonStageFailed && fault.RunID == runID {
			// No fork writer has run yet. Keep the classifier's result, not its
			// lane: the next writer can claim any free lane after clearance.
			work.LaneReleased = true
			return true, appendEffect(tx, kind, runID, work, at)
		}
	}
	return true, appendEffect(tx, kind, runID, json.RawMessage(payload), at)
}

// unlaunchedNodeClaims answers the cancelled claims of workflow nodes a crash or an older
// cancelStaging left behind: the item's pending work is examined, the node is still marked
// running with no run behind it, its dispatch row is gone, and no lane is leased to it.
// Each is the decision its pending work carries.
func unlaunchedNodeClaims(r *store.Reader) ([]Decision, error) {
	var hasNodes bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='node_runs')`).Scan(&hasNodes); err != nil || !hasNodes {
		return nil, err
	}
	rows, err := r.Query(`SELECT n.run_id FROM node_runs n WHERE n.outcome='running'
 AND NOT EXISTS(SELECT 1 FROM runs WHERE run_id=n.run_id)
 AND NOT EXISTS(SELECT 1 FROM dispatches WHERE run_id=n.run_id)
 AND NOT EXISTS(SELECT 1 FROM lanes WHERE run_id=n.run_id)
 ORDER BY n.item_id, n.node_id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	works, byRun, err := readPendingHistory(r)
	if err != nil {
		return nil, err
	}
	var found []Decision
	for _, id := range ids {
		work := byRun[id]
		if work == nil || !work.Done || works[work.Decision.ItemID] != work || work.Decision.RunRequest == nil || work.Decision.RunRequest.RunID != id || len(work.Decision.Parallel) > 0 || work.Decision.Workflow == nil {
			continue
		}
		rec, err := terminalRecord(r, id)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			found = append(found, work.Decision)
		}
	}
	return found, nil
}

// releaseUnlaunchedClaims recognizes the records a cancelled claim left before
// cancelStaging undid its conflict move: the item's latest dispatch is examined, its run
// has no terminal record, and the item's conflict is still as that claim moved it. The
// move is undone, so the planner dispatches the item again — the design seat for an
// awaiting conflict, the build for a closed answer — under the same run id. No operator
// command is involved, and nothing is written for a record that is not that shape.
func (l *Loop) releaseUnlaunchedClaims(ctx context.Context) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	found := []Decision{}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		conflicts, err := item.ReadConflicts(r)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(conflicts))
		for id := range conflicts {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			work := works[id]
			if work == nil || !work.Done || work.Decision.RunRequest == nil || len(work.Decision.Parallel) > 0 {
				continue
			}
			if _, moved := conflictBeforeClaim(conflicts[id], work.Decision); !moved {
				continue
			}
			rec, err := terminalRecord(r, work.Decision.RunRequest.RunID)
			if err != nil {
				return err
			}
			if rec == nil {
				found = append(found, work.Decision)
			}
		}
		nodes, err := unlaunchedNodeClaims(r)
		if err != nil {
			return err
		}
		for _, d := range nodes {
			dup := false
			for _, have := range found {
				dup = dup || have.RunRequest.RunID == d.RunRequest.RunID
			}
			if !dup {
				found = append(found, d)
			}
		}
		return nil
	})
	if err != nil || len(found) == 0 {
		return err
	}
	at := l.cfg.Now().UTC()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		for _, d := range found {
			// Re-read under the write: a dispatch claimed since is not this record.
			if w := works[d.ItemID]; w == nil || !w.Done || w.Decision.RunRequest == nil || w.Decision.RunRequest.RunID != d.RunRequest.RunID {
				continue
			}
			if rec, err := terminalRecord(tx.Reader(), d.RunRequest.RunID); err != nil || rec != nil {
				if err != nil {
					return err
				}
				continue
			}
			// The claim's examination already stands; retract the rest of what it wrote.
			// A node claim also gets its pending work back, so the item is offered again.
			if err := retractClaim(tx, d, at, false); err != nil {
				return err
			}
			l.reportf("loop: %s run %s was claimed but never started; its claim is retracted for the next dispatch\n", d.ItemID, d.RunRequest.RunID)
		}
		return nil
	})
}
