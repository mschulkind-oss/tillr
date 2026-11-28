// Reference: docs/reference/tillr-muse-backend.md
package drain

import (
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
	outcomes, _, err := policy.QueryBackendOutcomesForRuns(r, runIDs)
	if err != nil {
		return nil, err
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
