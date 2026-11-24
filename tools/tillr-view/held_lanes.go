package main

import (
	"github.com/mschulkind-oss/tillr-drain/lanestate"
	"github.com/mschulkind-oss/tillr/store"
)

func addHeldLaneHistory(r *store.Reader, rows []waitingRow) error {
	states, err := lanestate.Read(r)
	if err != nil {
		return err
	}
	for i := range rows {
		row := &rows[i]
		if row.Class == "moving" {
			continue
		}
		if state, ok := states[row.ItemID]; ok {
			row.LaneStatus, row.LaneReason = state.Status, state.Reason
			row.Why += "; lane " + state.Status
			if state.Reason != "" {
				row.Why += ": " + state.Reason
			}
		}
	}
	return nil
}

func readHeldByLane(r *store.Reader) (map[string]lanestate.HeldLane, error) {
	held, err := lanestate.HeldByLane(r)
	if isMissingTable(err) {
		return map[string]lanestate.HeldLane{}, nil
	}
	return held, err
}
