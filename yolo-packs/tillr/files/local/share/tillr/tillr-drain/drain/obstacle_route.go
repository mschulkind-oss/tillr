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
	if !IsGeneratedItem(req.ItemID) {
		if words, err := requestWordsFor(ctx, l.cfg.Store, req.ItemID); err == nil && words != nil {
			return laneOutcome{}, false
		}
	}
	target := l.laneTarget(lane)
	head := l.headOf(ctx, lane)
	candidate := Candidate{ItemID: req.ItemID, LaneID: lane, RunID: req.RunID, TickID: tick, Worktree: target, BaseRef: l.lanePatchRef(lane), BaseSHA: l.settleBase(lane, mainSHA), HeadSHA: head}
	if IsTrunkFixer(req.ItemID) && head == candidate.BaseSHA {
		if resolved, err := l.resolveKeptFixerWithoutCommit(ctx, req.ItemID); err != nil {
			return laneOutcome{err: err}, true
		} else if resolved {
			l.forgetSegment(req.RunID)
			return laneOutcome{stop: l.abandon(ctx, lane, req.RunID, head, "resolved elsewhere: rerun passed")}, true
		}
	}
	returnToWorker := func(detail, key string) laneOutcome {
		candidate.RefusalDetail, candidate.EvidenceKey = detail, key
		if err := l.refuse(ctx, candidate, result, RefusalNoOp); err != nil {
			return laneOutcome{err: err}
		}
		l.forgetSegment(req.RunID)
		return laneOutcome{stop: l.abandon(ctx, lane, req.RunID, head, "returned to its worker: "+o.Kind+" obstacle")}
	}
	switch o.Kind {
	case ObstacleFence:
		if !IsTrunkFixer(req.ItemID) {
			return laneOutcome{}, false
		}
		// A trunk fixer's fence is the loop's own guess at where the red can be repaired
		// (the red range and the failing checks' watches). A worker that names the path
		// the repair needs corrects that guess, and nothing in it is the owner's to rule:
		// the paths are granted and the same worker continues. The gate's own code is never
		// granted, so a request that names any of it still reaches the owner.
		added, refused, err := l.widenFixerFence(ctx, req.ItemID, o.List("PATHS"))
		if err != nil {
			return laneOutcome{err: err}, true
		}
		if len(refused) > 0 || len(added) == 0 {
			return laneOutcome{}, false
		}
		l.reportf("loop: %s named a fence obstacle: its fence grows by %s, and it goes back to its worker\n", req.ItemID, strings.Join(added, ", "))
		return returnToWorker(fmt.Sprintf("The fence now includes %s, the paths this run named as outside it (%s). Continue the repair and commit. %s", strings.Join(added, ", "), o.Field("EVIDENCE"), ObstacleContract), ""), true
	case ObstacleTrunk:
		if IsTrunkFixer(req.ItemID) {
			return returnToWorker(missingOutputDetail("this item is the trunk's repair, so a red trunk is its work and `trunk` is not an obstacle open to it"), ""), true
		}
		// The replay runs in a scratch worktree of its own and holds no store transaction;
		// it holds this lane only because a refuted claim continues this lane's session.
		checks := o.List("CHECKS")
		gate := GateOutcome{FailingChecks: checks}
		reading, err := l.trunkAlreadyRed(ctx, candidate, gate)
		red := reading.Red
		if err == nil && reading.Confirmed {
			for id, ref := range reading.CheckLogs {
				if gate.CheckLogs[id] == "" && ref != "" {
					if gate.CheckLogs == nil {
						gate.CheckLogs = map[string]string{}
					}
					gate.CheckLogs[id] = ref
				}
			}
			if err = l.acquitRed(ctx, candidate, gate, reading.RedSHA, red, result); err == nil {
				l.reportf("loop: %s named a trunk obstacle the replay confirmed on %s: held behind the trunk repair\n", req.ItemID, shortSHA(reading.RedSHA))
				return laneOutcome{stop: l.abandonFromBase(ctx, lane, req.RunID, head, candidate.BaseSHA, "trunk obstacle confirmed on the base")}, true
			}
		}
		if err == nil && reading.RepairedAt != "" {
			// The checks are red only on the base this run started from: the trunk is
			// repaired at its tip. The same session continues, on the tip.
			oldBase := candidate.BaseSHA
			moved, moveErr := l.moveLaneOntoTip(ctx, lane, head, reading.RepairedAt)
			if moveErr != nil {
				return laneOutcome{err: moveErr}, true
			}
			if moved != "" {
				head, candidate.HeadSHA, candidate.BaseSHA = moved, moved, reading.RepairedAt
			}
			l.reportf("loop: %s named a trunk obstacle red only on its base %s; the trunk is repaired at %s: returned to its worker\n", req.ItemID, shortSHA(oldBase), shortSHA(reading.RepairedAt))
			return returnToWorker(repairedAtTipDetail(checks, oldBase, reading.RepairedAt, moved)+" "+ObstacleContract, ""), true
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
		return laneOutcome{stop: l.abandonFromBase(ctx, lane, req.RunID, head, candidate.BaseSHA, "tooling obstacle: waits on "+defect)}, true
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
