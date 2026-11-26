package drainmodel

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
