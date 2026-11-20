package drain

import "path"

// effectiveNode leaves the graph unchanged: a fence grant belongs only to the
// dispatched run, and is saved with its decision for restaging and resume.
func (d Decision) effectiveNode() WorkflowStage {
	stage := d.Workflow.Stages[d.NodeIndex]
	if len(d.NodeFencePaths) > 0 {
		stage.WriteScope.Paths = append(append([]string(nil), stage.WriteScope.Paths...), d.NodeFencePaths...)
	}
	return stage
}

func (l *Loop) missingPieceFencePaths(d Decision, obs Obstacle) []string {
	if obs.Kind != ObstacleFence || obs.Problem != "" {
		return nil
	}
	itemFence := fenceEntries(l.laneFileBody(d.LaneID, ItemFenceFile))
	pieceFence := fenceEntries(l.laneFileBody(d.LaneID, FenceFile))
	if len(pieceFence) == 0 {
		return nil
	}
	var missing []string
	for _, reported := range scopeForLane(obs.List("PATHS"), d.LaneID) {
		clean := path.Clean(reported)
		// Validate derived companions too: widening must not grant anything
		// beyond the item's staged scope.
		if !scopeWithin(fencePaths([]string{clean}), itemFence) {
			return nil
		}
		if !scopeWithin([]string{clean}, pieceFence) {
			missing = append(missing, clean)
		}
	}
	return missing
}
