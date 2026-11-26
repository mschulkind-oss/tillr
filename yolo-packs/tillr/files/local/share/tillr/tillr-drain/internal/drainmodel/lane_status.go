package drainmodel

import (
	"github.com/mschulkind-oss/tillr-drain/lanestate"
	"github.com/mschulkind-oss/tillr/store"
)

// CandidateLaneState describes capacity, not another candidate or item state.
type CandidateLaneState = lanestate.State

// PendingLaneStates shares the hold explanation with historical read faces.
func PendingLaneStates(r *store.Reader) (map[string]CandidateLaneState, error) {
	return lanestate.Read(r)
}
