package drain

import "sort"

// DrainingLane is a lane above the loop's lane count that still holds unfinished work: a
// live or paused run, or a candidate nobody has landed or released. Lowering --lanes does
// not strand that work. The loop keeps serving the lane (its lease is observed and its
// examination runs like any other) until the work lands or is released, and offers the
// lane no new item, so the count falls as the work on the lanes above it finishes.
type DrainingLane struct {
	LaneID string `json:"lane_id"`
	ItemID string `json:"item_id"`
	RunID  string `json:"run_id,omitempty"`
}

// drainingLanes answers the lanes outside offered that unfinished work still holds, in lane
// order. A document desk is not a lane of the count and is never listed; a lane whose work
// released it holds nothing.
func drainingLanes(works map[string]*pendingWork, offered []string) []DrainingLane {
	in := make(map[string]bool, len(offered))
	for _, lane := range offered {
		in[lane] = true
	}
	var out []DrainingLane
	seen := map[string]bool{}
	for _, work := range works {
		lane := work.Decision.LaneID
		if work.Done || work.LaneReleased || lane == "" || in[lane] || seen[lane] || documentDesk(lane) {
			continue
		}
		seen[lane] = true
		d := DrainingLane{LaneID: lane, ItemID: work.Decision.ItemID}
		if work.Decision.RunRequest != nil {
			d.RunID = work.Decision.RunRequest.RunID
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LaneID < out[j].LaneID })
	return out
}
