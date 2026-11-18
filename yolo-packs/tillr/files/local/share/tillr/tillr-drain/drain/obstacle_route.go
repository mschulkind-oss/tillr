package drain

import (
	"context"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
)

// workerContract answers whether a run is a worker's under the obstacle contract
// (obstacle.go): a build-seat run of an item, not a design answer, a comment desk, or a
// command node.
func workerContract(d Decision, req run.Request) bool {
	return req.Backend != "command" && d.BlockConflict == nil && !item.IsCommentItem(req.ItemID) && (d.Seat == "" || d.Seat == "build")
}

// laneAttempted answers whether a run left an attempt in its lane: a commit off the base, or
// an uncommitted change. A read failure counts as an attempt, so a git fault never turns a
// stated obstacle into a missing output.
func (l *Loop) laneAttempted(ctx context.Context, lane, mainSHA string) bool {
	if l.headOf(ctx, lane) != l.settleBase(lane, mainSHA) {
		return true
	}
	paths, err := l.uncommittedPaths(ctx, l.laneTarget(lane))
	return err != nil || len(paths) > 0
}

// routeObstacle takes a complete obstacle the loop acts on itself: trunk, tooling and seat.
// It answers false for a kind the blocked route takes (reportedBlock): definition, fence
// and owner go to the design seat, or to the owner for a generated repair item, and so do a
// generated item's tooling obstacle and a seat obstacle past the end of the escalation list.
func (l *Loop) routeObstacle(ctx context.Context, d Decision, req run.Request, lane, tick, mainSHA string, o Obstacle, result *LoopResult) (laneOutcome, bool) {
	target := l.laneTarget(lane)
	head := l.headOf(ctx, lane)
	candidate := Candidate{ItemID: req.ItemID, LaneID: lane, RunID: req.RunID, TickID: tick, Worktree: target, BaseRef: l.lanePatchRef(lane), BaseSHA: l.settleBase(lane, mainSHA), HeadSHA: head}
	returnToWorker := func(detail, key string) laneOutcome {
		candidate.RefusalDetail, candidate.EvidenceKey = detail, key
		if err := l.refuse(ctx, candidate, result, RefusalNoOp); err != nil {
			return laneOutcome{err: err}
		}
		l.forgetSegment(req.RunID)
		return laneOutcome{stop: l.abandon(ctx, lane, req.RunID, head, "returned to its worker: "+o.Kind+" obstacle")}
	}
	switch o.Kind {
	case ObstacleTrunk:
		if IsTrunkFixer(req.ItemID) {
			return returnToWorker(missingOutputDetail("this item is the trunk's repair, so a red trunk is its work and `trunk` is not an obstacle open to it"), ""), true
		}
		// The replay runs in a scratch worktree of its own and holds no store transaction;
		// it holds this lane only because a refuted claim continues this lane's session.
		checks := o.List("CHECKS")
		gate := GateOutcome{FailingChecks: checks}
		redSHA, red, ok, err := l.trunkAlreadyRed(ctx, candidate, gate)
		if err == nil && ok {
			if err = l.acquitRed(ctx, candidate, gate, redSHA, red, result); err == nil {
				l.reportf("loop: %s named a trunk obstacle the base replay confirmed: held behind the trunk repair\n", req.ItemID)
				return laneOutcome{stop: l.abandon(ctx, lane, req.RunID, head, "trunk obstacle confirmed on the base")}, true
			}
		}
		saw := "red: " + strings.Join(red, ", ")
		if len(red) == 0 {
			saw = "none of them red"
		}
		if err != nil {
			saw = "the replay or the repair filing failed: " + err.Error()
		} else if _, known := l.cfg.Steps.(ReplayStep); !known {
			saw = "this loop has no replay step, so the claim cannot be confirmed"
		}
		base := candidate.BaseSHA
		note := ""
		if claimed := o.Field("BASE"); claimed != "" && !strings.HasPrefix(base, claimed) && !strings.HasPrefix(claimed, base) {
			note = fmt.Sprintf(" The report named base %s; this run's base is %s, which is the one replayed.", claimed, shortSHA(base))
		}
		l.reportf("loop: %s named a trunk obstacle the base replay did not confirm (%s): returned to its worker\n", req.ItemID, saw)
		return returnToWorker(fmt.Sprintf("The run's trunk obstacle named checks %s. Replayed on this run's base %s, the loop found %s, so the failure is not the trunk's: it is this change's to repair, or the ids are not gate check ids the replay knows.%s Continue the work and commit. %s", strings.Join(checks, ", "), shortSHA(base), saw, note, ObstacleContract), ""), true
	case ObstacleTooling:
		if IsGeneratedRepair(req.ItemID) {
			return laneOutcome{}, false
		}
		defect, err := l.fileDefectItem(ctx, req.ItemID, o)
		if err != nil {
			return laneOutcome{err: err}, true
		}
		if err := l.holdBehindDefect(ctx, candidate, defect, o.Field("COMMAND")); err != nil {
			return laneOutcome{err: err}, true
		}
		l.reportf("loop: %s named a tooling obstacle: held behind %s\n", req.ItemID, defect)
		return laneOutcome{stop: l.abandon(ctx, lane, req.RunID, head, "tooling obstacle: waits on "+defect)}, true
	case ObstacleSeat:
		next, err := l.escalateSeat(ctx, req.ItemID, req.RunID, d.Seat, req.Backend)
		if err != nil {
			return laneOutcome{err: err}, true
		}
		if next == "" {
			return laneOutcome{}, false
		}
		l.reportf("loop: %s named a seat obstacle on %s: its next run is a fresh run on %s\n", req.ItemID, req.Backend, next)
		return returnToWorker(fmt.Sprintf("The run on backend %s named a seat obstacle, so this item moves to backend %s for every later run until it lands, and that run starts fresh with this record. Tried: %s Missing: %s The previous run's work is kept as described below; reuse what is sound. %s", req.Backend, next, o.Field("TRIED"), o.Field("MISSING"), ObstacleContract), "seat:"+req.Backend), true
	}
	return laneOutcome{}, false
}
