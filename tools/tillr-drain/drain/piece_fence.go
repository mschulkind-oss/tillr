package drain

import (
	"context"
	"path"
	"slices"
)

func workflowHasPlannedPiece(w *Workflow) bool {
	return w != nil && slices.ContainsFunc(w.Stages, plannedWritePiece)
}

// A planned piece's paths describe the division of work, not a second fence
// that can override the item's current authority.
func (s WorkflowStage) runScopePaths(allowed []string) ([]string, error) {
	if plannedWritePiece(s) {
		return fencePaths(allowed), nil
	}
	return s.scopePaths(allowed)
}

func (l *Loop) currentItemFence(ctx context.Context, c Candidate) ([]string, error) {
	recorded, err := readStagedItem(ctx, l.cfg.Store, l.cfg.Root, c.ItemID)
	if err != nil {
		return nil, err
	}
	allowed := recorded.AllowedPaths
	if ref := l.lanePatchRef(c.LaneID); ref != "" {
		fork, err := forkTargetAtLane(l.cfg.Root, ref, c.LaneID)
		if err != nil {
			return nil, err
		}
		if fork.Checkout == c.Worktree {
			allowed = itemDeliveryFence(fork, allowed)
		}
	}
	return scopeForLane(fencePaths(allowed), c.LaneID), nil
}

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
	if stage := d.effectiveNode(); plannedWritePiece(stage) {
		// A worker can still report that its saved plan excluded an in-scope
		// path. Retry the same piece and record the requested scope adjustment.
		pieceFence = fencePaths(scopeForLane(stage.WriteScope.Paths, d.LaneID))
	}
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
