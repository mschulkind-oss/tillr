package drain

import (
	"database/sql"
	"fmt"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// Read process evidence outside the store writer, with face starts serialized.
func (l *Loop) heldLaneProcesses(c Candidate, owner *LoopOwner) (map[string]bool, string, error) {
	procs, err := liveRunWorkers(l.cfg.Store, "", owner)
	if err != nil {
		return nil, "", err
	}
	live := map[string]bool{}
	for _, proc := range procs {
		live[proc.RunID] = true
		inside, err := l.processInLane(proc, c.LaneID)
		if err == nil && !inside && c.BaseRef != "" {
			inside, err = processInCheckout(proc, c.Worktree)
		}
		if err != nil {
			return nil, "", err
		}
		if inside {
			return nil, fmt.Sprintf("lane has live marked process %d", proc.PID), nil
		}
	}
	return live, "", nil
}

// Called with the store writer and run-start lock held. A terminal worker record
// is not enough: another member can be claimed but not launched, or resumed.
func (l *Loop) candidateResetBlocked(r *store.Reader, c Candidate, here *LoopOwner, live map[string]bool) (string, error) {
	var n int
	if err := r.QueryRow(`SELECT COUNT(*) FROM read_only_runs WHERE lane_id=? AND settled=0`, c.LaneID).Scan(&n); err != nil {
		return "", err
	}
	if n > 0 {
		return "lane has unsettled read-only member claims", nil
	}
	works, history, err := readPendingHistory(r)
	if err != nil {
		return "", err
	}
	membersOfAttempt := map[string]bool{}
	settled := map[string]bool{}
	if current := works[c.ItemID]; current != nil && current.Decision.Workflow != nil {
		rows, err := effectiveNodeRuns(r, c.ItemID, current.Decision.Attempt)
		if err != nil {
			return "", err
		}
		for _, node := range rows {
			membersOfAttempt[node.RunID] = true
			settled[node.RunID] = node.Outcome == "passed" || node.Outcome == "handled-fail" || node.Outcome == "skipped"
		}
	}
	for id, w := range history {
		if !w.Done && !w.LaneReleased && w.Decision.LaneID == c.LaneID && id != c.RunID && !settled[id] &&
			(w == works[w.Decision.ItemID] || membersOfAttempt[id]) {
			return "lane holds another pending run " + id, nil
		}
		if w.Decision.LaneID == c.LaneID || membersOfAttempt[id] {
			if live[id] {
				return "lane has a live run " + id, nil
			}
			locator, _ := l.cfg.Steps.(runStreamLocator)
			for _, owner := range []*LoopOwner{w.Owner, w.Examiner} {
				if fromOtherDomain(owner, here) && foreignRunLive(locator, id, owner.Domain, here.Domain) {
					return "lane may have a live run in another process domain", nil
				}
			}
		}
	}
	rows, err := r.Query(`SELECT run_id,COALESCE(item_id,''),state FROM runs WHERE lane_id=? UNION SELECT run_id,item_id,'exited' FROM read_only_runs WHERE lane_id=?`, c.LaneID, c.LaneID)
	if err != nil {
		return "", err
	}
	type member struct{ id, itemID, state string }
	var members []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.id, &m.itemID, &m.state); err != nil {
			rows.Close()
			return "", err
		}
		members = append(members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	// Completed graph members can still have a live resume on their original
	// lane after this workflow was assigned to another. Check their run rows too.
	for id := range membersOfAttempt {
		var m = member{id: id}
		if err := r.QueryRow(`SELECT COALESCE(item_id,''),state FROM runs WHERE run_id=?`, id).Scan(&m.itemID, &m.state); err == nil {
			members = append(members, m)
		} else if err != sql.ErrNoRows {
			return "", err
		}
	}
	var declared *definition.Record
	itemEnded := map[string]bool{}
	memberItemEnded := func(m member) (bool, error) {
		itemID := m.itemID
		if itemID == "" {
			if w, ok := history[m.id]; ok {
				itemID = w.Decision.ItemID
			}
		}
		return itemEndedInStore(r, itemID, &declared, itemEnded)
	}
	for _, m := range members {
		if live[m.id] {
			return "lane has a live run " + m.id, nil
		}
		if !isTerminalRunState(m.state) {
			if m.state == run.StatePaused {
				ended, err := memberItemEnded(m)
				if err != nil {
					return "", err
				}
				if ended {
					continue
				}
			}
			return "lane has an unfinished run " + m.id, nil
		}
		open, err := resumeUnfinished(r, m.id)
		if err != nil {
			return "", err
		}
		if open {
			ended, err := memberItemEnded(m)
			if err != nil {
				return "", err
			}
			if ended {
				continue
			}
			return "lane has an unfinished resume " + m.id, nil
		}
	}
	return "", nil
}

func itemEndedInStore(r *store.Reader, itemID string, declared **definition.Record, cache map[string]bool) (bool, error) {
	if itemID == "" {
		return false, nil
	}
	if ended, seen := cache[itemID]; seen {
		return ended, nil
	}
	if *declared == nil {
		rec, err := definition.ForReader(r)
		if err != nil {
			return false, err
		}
		*declared = &rec
	}
	var state string
	err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, itemID).Scan(&state)
	if err == sql.ErrNoRows {
		cache[itemID] = false
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ended := state == StateLanded || finishedState((*declared).Parsed, state) || closedState((*declared).Parsed, state)
	cache[itemID] = ended
	return ended, nil
}
