package main

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
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
	laneRows, err := r.Query(`SELECT lane_id FROM lanes`)
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var laneIDs []string
	for laneRows.Next() {
		var id string
		if err := laneRows.Scan(&id); err != nil {
			laneRows.Close()
			return nil, err
		}
		laneIDs = append(laneIDs, id)
	}
	if err := laneRows.Err(); err != nil {
		laneRows.Close()
		return nil, err
	}
	laneRows.Close()
	if len(laneIDs) == 0 {
		return out, nil
	}
	events, err := r.EventsForObjects(0, laneIDs, "drain.lane-refused", "drain.lane-restored")
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		if e.ObjectType != "lane" || e.ObjectID == nil {
			continue
		}
		lane := *e.ObjectID
		if e.Kind == "drain.lane-restored" {
			delete(out, lane)
			continue
		}
		c := laneCondition{LaneID: lane, AtMS: e.At.UnixMilli()}
		if err := json.Unmarshal(e.Payload, &c); err != nil {
			return nil, err
		}
		c.AtMS = e.At.UnixMilli()
		out[lane] = c
	}
	return out, nil
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
		if rule, ok := holdrules.Rules[c.Reason]; ok {
			class, where = rule.Class, rule.Where
		}
		out = append(out, waitingRow{ItemID: "lane:" + lane, Class: class, Where: where,
			Why: c.Reason + ": " + c.Detail, Mover: c.Remedy,
			Since: time.UnixMilli(c.AtMS).UTC().Format(time.RFC3339), ClassSource: "store"})
	}
	return out, nil
}
