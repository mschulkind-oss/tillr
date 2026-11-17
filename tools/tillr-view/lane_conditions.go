package main

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// laneCondition is a lane the drain took out of rotation because staging it refused on the
// lane's own state: its newest drain.lane-refused with no later drain.lane-restored.
type laneCondition struct {
	LaneID string `json:"lane_id"`
	Reason string `json:"reason"`
	Detail string `json:"detail"`
	Remedy string `json:"remedy"`
	AtMS   int64  `json:"at_ms"`
}

// readLaneConditions answers each lane's standing condition. A store with no events table,
// or one that never recorded a lane condition, answers none.
func readLaneConditions(r *store.Reader) (map[string]laneCondition, error) {
	out := map[string]laneCondition{}
	rows, err := r.Query(`SELECT kind, object_id, payload, ts FROM events WHERE kind IN ('drain.lane-refused', 'drain.lane-restored') AND object_type = 'lane' ORDER BY seq`)
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, lane, raw string
		var ts int64
		if err := rows.Scan(&kind, &lane, &raw, &ts); err != nil {
			return nil, err
		}
		if kind == "drain.lane-restored" {
			delete(out, lane)
			continue
		}
		c := laneCondition{LaneID: lane, AtMS: ts}
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		c.AtMS = ts
		out[lane] = c
	}
	return out, rows.Err()
}

// waitingLaneConditions is one waiting row per lane out of rotation. Its mover is the
// remedy the drain recorded, which is an operator's act.
func waitingLaneConditions(r *store.Reader) ([]waitingRow, error) {
	conditions, err := readLaneConditions(r)
	if err != nil {
		return nil, err
	}
	lanes := make([]string, 0, len(conditions))
	for lane := range conditions {
		lanes = append(lanes, lane)
	}
	sort.Strings(lanes)
	out := make([]waitingRow, 0, len(lanes))
	for _, lane := range lanes {
		c := conditions[lane]
		class, where := "needs you", "lane"
		if rule, ok := waitingRules[c.Reason]; ok {
			class, where = rule.class, rule.where
		}
		out = append(out, waitingRow{ItemID: "lane:" + lane, Class: class, Where: where,
			Why: c.Reason + ": " + c.Detail, Mover: c.Remedy,
			Since: time.UnixMilli(c.AtMS).UTC().Format(time.RFC3339), ClassSource: "store"})
	}
	return out, nil
}
