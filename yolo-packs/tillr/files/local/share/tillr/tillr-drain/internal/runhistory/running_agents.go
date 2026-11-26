package runhistory

import (
	"database/sql"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr/store"
)

type LeaseRunKey struct {
	LaneID  string
	GroupID string
}

type ActiveReadOnlyMember struct {
	RunID  string
	ItemID string
}

// ReadUnsettledReadOnlyRuns returns unsettled read_only_runs grouped by (lane_id, group_id).
func ReadUnsettledReadOnlyRuns(r *store.Reader) (map[LeaseRunKey][]ActiveReadOnlyMember, error) {
	roByLease := map[LeaseRunKey][]ActiveReadOnlyMember{}
	hasReadOnly, err := TableExists(r, "read_only_runs")
	if err != nil {
		return nil, storelog.ReadFailure(err)
	}
	if !hasReadOnly {
		return roByLease, nil
	}
	rows, err := r.Query(`SELECT run_id, group_id, item_id, lane_id FROM read_only_runs WHERE settled = 0`)
	if err != nil {
		return nil, storelog.ReadFailure(err)
	}
	for rows.Next() {
		var runID, groupID, itemID, laneID string
		if err := rows.Scan(&runID, &groupID, &itemID, &laneID); err != nil {
			rows.Close()
			return nil, storelog.ReadFailure(err)
		}
		key := LeaseRunKey{LaneID: laneID, GroupID: groupID}
		roByLease[key] = append(roByLease[key], ActiveReadOnlyMember{RunID: runID, ItemID: itemID})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, storelog.ReadFailure(err)
	}
	rows.Close()
	return roByLease, nil
}

func ActiveOrdinaryAgentRuns(active map[string]string) []string {
	if len(active) == 0 {
		return nil
	}
	runs := make([]string, 0, len(active))
	for runKey := range active {
		runs = append(runs, runKey)
	}
	sort.Strings(runs)
	return runs
}

func ActiveOrdinaryAgentSummary(active map[string]string) string {
	return strings.Join(ActiveOrdinaryAgentRuns(active), ", ")
}

func ReadExpressByID(r *store.Reader) (map[string]bool, error) {
	var hasExpress bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name='express')`).Scan(&hasExpress); err != nil {
		return nil, storelog.ReadFailure(err)
	}
	out := map[string]bool{}
	if !hasExpress {
		return out, nil
	}
	rows, err := r.Query(`SELECT item_id FROM items WHERE COALESCE(express, 0) != 0`)
	if err != nil {
		return nil, storelog.ReadFailure(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil && err != sql.ErrNoRows {
			return nil, storelog.ReadFailure(err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, storelog.ReadFailure(err)
	}
	return out, nil
}
