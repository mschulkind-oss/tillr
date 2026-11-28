package main

import (
	"encoding/json"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/lanestate"
	"github.com/mschulkind-oss/tillr/store"
)

func addHeldLaneHistory(r *store.Reader, rows []waitingRow) error {
	if len(rows) == 0 {
		return nil
	}
	itemIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Class != "moving" && row.ItemID != "" {
			itemIDs = append(itemIDs, row.ItemID)
		}
	}
	states, err := lanestate.ReadForItems(r, itemIDs)
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

func readHeldByLaneForOpenItems(r *store.Reader) (map[string]lanestate.HeldLane, error) {
	out := map[string]lanestate.HeldLane{}
	items, err := readOpenItems(r)
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return out, nil
	}
	openItemIDs := make([]string, len(items))
	for i, it := range items {
		openItemIDs[i] = it.ItemID
	}
	states, err := lanestate.ReadForItems(r, openItemIDs)
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var keptIDs []string
	for id, st := range states {
		if st.Status == "kept" {
			keptIDs = append(keptIDs, id)
		}
	}
	if len(keptIDs) == 0 {
		return out, nil
	}
	type latestDecision struct {
		seq    int64
		laneID string
		runID  string
	}
	latestByItem := map[string]latestDecision{}
	const chunk = 400
	for start := 0; start < len(keptIDs); start += chunk {
		end := start + chunk
		if end > len(keptIDs) {
			end = len(keptIDs)
		}
		batch := keptIDs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		for _, kind := range []string{"drain.started", "drain.node-advanced", "drain.graph-changed"} {
			args := make([]any, 0, 1+len(batch))
			args = append(args, kind)
			for _, id := range batch {
				args = append(args, id)
			}
			rows, err := r.Query(`SELECT seq, payload FROM events WHERE kind = ? AND json_valid(payload) AND json_extract(payload, '$.decision.item_id') IN (`+placeholders+`)`, args...)
			if isMissingTable(err) {
				return out, nil
			}
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var seq int64
				var raw []byte
				if err := rows.Scan(&seq, &raw); err != nil {
					rows.Close()
					return nil, err
				}
				var body struct {
					Decision struct {
						ItemID     string `json:"item_id"`
						LaneID     string `json:"lane_id"`
						RunRequest *struct {
							RunID string `json:"run_id"`
						} `json:"run_request"`
					} `json:"decision"`
				}
				if err := json.Unmarshal(raw, &body); err != nil {
					rows.Close()
					return nil, err
				}
				itemID := body.Decision.ItemID
				if prev, ok := latestByItem[itemID]; !ok || seq > prev.seq {
					runID := ""
					if body.Decision.RunRequest != nil {
						runID = body.Decision.RunRequest.RunID
					}
					latestByItem[itemID] = latestDecision{seq: seq, laneID: body.Decision.LaneID, runID: runID}
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
		}
	}
	bestSeq := map[string]int64{}
	for itemID, dec := range latestByItem {
		if dec.laneID == "" {
			continue
		}
		if prev, ok := out[dec.laneID]; ok && (dec.seq < bestSeq[dec.laneID] || (dec.seq == bestSeq[dec.laneID] && itemID > prev.ItemID)) {
			continue
		}
		bestSeq[dec.laneID] = dec.seq
		out[dec.laneID] = lanestate.HeldLane{
			LaneID: dec.laneID,
			ItemID: itemID,
			RunID:  dec.runID,
			Reason: states[itemID].Reason,
		}
	}
	return out, nil
}
