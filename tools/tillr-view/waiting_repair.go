package main

import (
	"encoding/json"

	"github.com/mschulkind-oss/tillr/store"
)

type waitingRepair struct {
	ItemID     string `json:"item_id"`
	Cause      string `json:"cause"`
	NextAction string `json:"next_action"`
}

// The event is the cause, not the original successful run's termination. Keep it
// visible even before the next pass records its hold decision; examination clears it.
func waitingRepairHold(r *store.Reader, itemID, runID string) (*waitingRepair, int64, error) {
	events, err := r.Events(0)
	if err != nil {
		return nil, 0, err
	}
	var hold *waitingRepair
	var since int64
	for _, e := range events {
		if e.ObjectID == nil || *e.ObjectID != runID {
			continue
		}
		switch e.Kind {
		case "drain.repair-held":
			var payload waitingRepair
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				return nil, 0, err
			}
			if payload.ItemID == itemID {
				hold = &payload
				since = e.At.UnixMilli()
			}
		case "drain.examined":
			hold = nil
		}
	}
	return hold, since, nil
}
