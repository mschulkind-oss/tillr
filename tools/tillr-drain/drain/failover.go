// Reference: docs/reference/tillr-muse-backend.md
package drain

import (
	"strings"

	"github.com/mschulkind-oss/tillr-drain/internal/policy"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// readPriorRuns answers every item's terminal runs, oldest first, keyed by item id.
//
// The runs table is the item→run link (D-UT19), so the planner reads the link rather than
// inferring it from the event log: a run's backend and its exit are what the failover
// decision turns on, and both are columns here.
//
// The log is read for the footer test and for nothing else. It is a store-referenced
// artifact — the record's own artifact_refs name it — so the plan stays a function of the
// store and what the store points at, and a log that is gone reads as "no report" rather
// than as an error.
func readPriorRuns(r *store.Reader) (map[string][]priorRun, error) {
	return readPriorRunsFrom(r, nil)
}

func readPriorRunsFrom(r *store.Reader, byRun map[string]*pendingWork) (map[string][]priorRun, error) {
	return readPriorRunsWithOutcomes(r, byRun, nil, nil)
}

func readPriorRunsWithOutcomes(r *store.Reader, byRun map[string]*pendingWork, cached *backendOutcomesState, latestFinished map[string]string) (map[string][]priorRun, error) {
	var outcomes []backendOutcome
	var err error
	if cached != nil {
		outcomes = cached.Entries
	} else {
		outcomes, err = readBackendOutcomesFrom(r, byRun)
		if err != nil {
			return nil, err
		}
	}
	return policy.ReadPriorRunsWith(r, outcomes, latestFinished, func(rec run.Record, saved bool, class AttemptClass) (bool, error) {
		piErr := false
		if (!saved || class == AttemptInfrastructure) && (byRun == nil || isDesignWork(byRun[rec.RunID])) {
			piErr = hasPiTransportError(rec)
			if piErr && byRun == nil {
				if _, byRun, err = readPendingHistory(r); err != nil {
					return false, err
				}
			}
		}
		return piErr && isDesignWork(byRun[rec.RunID]), nil
	})
}

func readPriorRunsWithOutcomesForItems(r *store.Reader, itemIDs, runIDs []string, byRun map[string]*pendingWork, latestFinished map[string]string) (map[string][]priorRun, error) {
	outcomes, unsaved, err := policy.QueryBackendOutcomesForRuns(r, runIDs)
	if err != nil {
		return nil, err
	}
	if unsaved > 0 {
		var unsavedSeqs []int64
		for _, o := range outcomes {
			if !o.Saved {
				unsavedSeqs = append(unsavedSeqs, o.Seq)
			}
		}
		offered, err := queryOfferedForSeqs(r, unsavedSeqs)
		if err != nil {
			return nil, err
		}
		if err := classifyUnsavedBackendOutcomes(r, outcomes, offered, byRun); err != nil {
			return nil, err
		}
	}
	return policy.ReadPriorRunsForItems(r, itemIDs, runIDs, outcomes, latestFinished, func(rec run.Record, saved bool, class AttemptClass) (bool, error) {
		piErr := false
		if (!saved || class == AttemptInfrastructure) && (byRun == nil || isDesignWork(byRun[rec.RunID])) {
			piErr = hasPiTransportError(rec)
			if piErr && byRun == nil {
				if _, byRun, err = readPendingHistoryForItems(r, itemIDs, runIDs); err != nil {
					return false, err
				}
			}
		}
		return piErr && isDesignWork(byRun[rec.RunID]), nil
	})
}

func queryOfferedForSeqs(r *store.Reader, seqs []int64) (map[int64]bool, error) {
	offered := map[int64]bool{}
	if len(seqs) == 0 {
		return offered, nil
	}
	const chunk = 400
	for start := 0; start < len(seqs); start += chunk {
		end := start + chunk
		if end > len(seqs) {
			end = len(seqs)
		}
		batch := seqs[start:end]
		args := make([]any, 0, 1+len(batch))
		args = append(args, callOffered)
		for _, seq := range batch {
			args = append(args, seq)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := r.Query(`SELECT json_extract(payload,'$.terminal_seq') FROM events WHERE kind=? AND json_valid(payload) AND json_extract(payload,'$.terminal_seq') IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var seq int64
			if err := rows.Scan(&seq); err == nil && seq > 0 {
				offered[seq] = true
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return offered, nil
}
