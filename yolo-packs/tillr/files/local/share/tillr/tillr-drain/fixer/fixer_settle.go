package fixer

import (
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// TableExists reports whether table name exists in SQLite.
func TableExists(r *store.Reader, name string) (bool, error) {
	var exists bool
	err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, name).Scan(&exists)
	return exists, err
}

// IsTerminalRunState reports whether a run state is terminal.
func IsTerminalRunState(state string) bool {
	return state == run.StateExited || state == run.StateFailedToStart || state == run.StateTimedOut
}

// RunBelongsToItem reports whether runID belongs to itemID across run tables and events.
func RunBelongsToItem(r *store.Reader, runID, itemID string, hasReadOnly, hasNodes, hasDispatches bool) (bool, error) {
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
	if err := r.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_type='run' AND object_id=? AND json_valid(payload) AND json_extract(payload,'$.decision.item_id')=?)`,
		dispatchStartedKind, runID, itemID,
	).Scan(&found); err != nil {
		return false, err
	}
	if found {
		return true, nil
	}
	if err := r.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND json_valid(payload) AND json_extract(payload,'$.run_id')=? AND json_extract(payload,'$.item_id')=?)`,
		run.AllocateKind, runID, itemID,
	).Scan(&found); err != nil {
		return false, err
	}
	return found, nil
}

// ResolvedFixerIDs returns all trunk fixer item IDs in StateResolvedElsewhere.
func ResolvedFixerIDs(r *store.Reader) ([]string, error) {
	rows, err := r.Query(`SELECT item_id FROM items WHERE item_id LIKE 'fixer-main-%' AND state=? ORDER BY item_id`, StateResolvedElsewhere)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// HasUnfinishedResolvedFixerRuns reports whether any resolved trunk fixer still has
// unfinished runs, pauses, unexamined dispatches, or leased lanes.
func HasUnfinishedResolvedFixerRuns(r *store.Reader) (bool, error) {
	fixers, err := ResolvedFixerIDs(r)
	if err != nil || len(fixers) == 0 {
		return false, err
	}
	hasReadOnly, err := TableExists(r, "read_only_runs")
	if err != nil {
		return false, err
	}
	hasNodes, err := TableExists(r, "node_runs")
	if err != nil {
		return false, err
	}
	hasPauses, err := TableExists(r, "quota_pauses")
	if err != nil {
		return false, err
	}
	hasDispatches, err := TableExists(r, "dispatches")
	if err != nil {
		return false, err
	}
	for _, fixerID := range fixers {
		if hasReadOnly {
			var exists bool
			if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM read_only_runs WHERE item_id=? AND settled=0)`, fixerID).Scan(&exists); err != nil {
				return false, err
			}
			if exists {
				return true, nil
			}
		}
		if hasNodes {
			var exists bool
			if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM node_runs WHERE item_id=? AND outcome='running')`, fixerID).Scan(&exists); err != nil {
				return false, err
			}
			if exists {
				return true, nil
			}
		}
		if hasPauses {
			var exists bool
			if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM quota_pauses WHERE item_id=?)`, fixerID).Scan(&exists); err != nil {
				return false, err
			}
			if exists {
				return true, nil
			}
		}
		rows, err := r.Query(`SELECT run_id, state, event_seq FROM runs WHERE item_id=?`, fixerID)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var runID, state string
			var eventSeq int64
			if err := rows.Scan(&runID, &state, &eventSeq); err != nil {
				rows.Close()
				return false, err
			}
			if !IsTerminalRunState(state) {
				rows.Close()
				return true, nil
			}
			var resumed bool
			if err := r.QueryRow(
				`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_type='run' AND object_id=? AND seq>?)`,
				run.ResumeKind, runID, eventSeq,
			).Scan(&resumed); err != nil {
				rows.Close()
				return false, err
			}
			if resumed {
				rows.Close()
				return true, nil
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, err
		}
		rows.Close()
		var unexamined bool
		if err := r.QueryRow(
			`SELECT EXISTS(
				SELECT 1 FROM events e
				WHERE e.kind=? AND e.object_type='run'
				  AND json_valid(e.payload) AND json_extract(e.payload,'$.decision.item_id')=?
				  AND NOT EXISTS(SELECT 1 FROM events x WHERE x.kind=? AND x.object_type='run' AND x.object_id=e.object_id)
			)`,
			dispatchStartedKind, fixerID, examinationDoneKind,
		).Scan(&unexamined); err != nil {
			return false, err
		}
		if unexamined {
			return true, nil
		}
	}

	laneRows, err := r.Query(`SELECT run_id FROM lanes WHERE state='leased' AND run_id IS NOT NULL`)
	if err != nil {
		return false, err
	}
	var leasedRuns []string
	for laneRows.Next() {
		var runID string
		if err := laneRows.Scan(&runID); err != nil {
			laneRows.Close()
			return false, err
		}
		leasedRuns = append(leasedRuns, runID)
	}
	if err := laneRows.Err(); err != nil {
		laneRows.Close()
		return false, err
	}
	laneRows.Close()

	for _, runID := range leasedRuns {
		for _, fixerID := range fixers {
			belongs, err := RunBelongsToItem(r, runID, fixerID, hasReadOnly, hasNodes, hasDispatches)
			if err != nil {
				return false, err
			}
			if belongs {
				return true, nil
			}
		}
	}
	return false, nil
}
