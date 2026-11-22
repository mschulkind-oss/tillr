package drain

import (
	"context"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

const missingCandidateOutcome = "missing-candidate"

// A candidate check can run as a test-run piece, not just as the graph's
// evaluator. Recognize the zero-count report only when drain independently
// sees a clean checkout at the same base; don't turn other tooling errors into
// build instructions. PATHS is immaterial to this Git finding.
func missingCandidateFinding(d Decision, base, head, dirty, report string) (int, string) {
	stage := d.Workflow.Stages[d.NodeIndex]
	if base == "" || head != base || strings.TrimSpace(dirty) != "" || stage.Access != "read_only" ||
		(stage.Seat != "test-run" && stage.Seat != "verify") || reportFooter(report) != "blocked" {
		return -1, ""
	}
	o := parseObstacle(report)
	args := strings.Fields(o.Field("COMMAND"))
	if o.Kind != ObstacleTooling || len(args) != 4 || args[0] != "git" || args[1] != "rev-list" || args[2] != "--count" {
		return -1, ""
	}
	ref, ok := strings.CutSuffix(args[3], "..HEAD")
	finding := o.Field("EVIDENCE")
	if !ok || len(ref) < 7 || !strings.HasPrefix(base, ref) || !strings.HasPrefix(finding, "Returned 0:") {
		return -1, ""
	}
	for i, writer := range d.Workflow.Stages[:d.NodeIndex] {
		if writer.Access == "write" && writer.Kind == "agent" {
			return i, finding
		}
	}
	return -1, ""
}

// Return through the ordinary verify-to-build path. There is no candidate to
// repair, so start the first writer fresh, preserving only its predecessors.
func (l *Loop) returnMissingCandidate(ctx context.Context, d Decision, writer int, base, finding string) error {
	id := d.RunRequest.RunID
	if len(d.Parallel) > 0 {
		// The planner reads the ending of the latest finished member, which
		// need not be either the primary reader or the reader that objected.
		if err := l.readStore(ctx, func(r *store.Reader) error {
			var latest int64
			for _, node := range parallelNodes(d) {
				var seq int64
				runID := node.RunRequest.RunID
				if err := r.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events WHERE object_id=? AND kind=?`, runID, run.FinishKind).Scan(&seq); err != nil {
					return err
				}
				if seq > latest {
					id, latest = runID, seq
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	c := Candidate{RunID: id, ItemID: d.ItemID, LaneID: d.LaneID, Worktree: l.laneTarget(d.LaneID), BaseSHA: base, HeadSHA: base}
	if err := l.pinCandidateHead(ctx, c); err != nil {
		return err
	}
	p := refusalPayload{Step: RefusalVerifyNotDone, ItemID: d.ItemID, LaneID: d.LaneID, BaseSHA: base, HeadSHA: base, Detail: finding,
		WorkerSession: &WorkerSession{RunID: c.RunID, LaneDir: l.laneDirPath(d.LaneID), LaneID: d.LaneID, BaseSHA: base, HeadSHA: base, NodeBaseSHA: base, Workflow: d.Workflow, NodeIndex: writer, Attempt: d.Attempt}}
	if err := appendJudgment(ctx, l.cfg.Store, RefusalKind, "run", c.RunID, p, l.cfg.Now()); err != nil {
		return err
	}
	if stop := l.abandon(ctx, d.LaneID, d.RunRequest.RunID, base, finding); stop != "" {
		return retainedAfter(d.LaneID, stop)
	}
	return l.markExamined(ctx, d)
}

// The retained-graph return can only carry accepted predecessors of the
// writer. Don't discard an unrelated reader failure or unmet graph dependency.
func (l *Loop) canReturnMissingCandidate(ctx context.Context, d Decision, writer int) (bool, error) {
	ready := false
	err := l.readStore(ctx, func(r *store.Reader) error {
		outcomes, err := workflowOutcomes(r, d.ItemID, d.Attempt)
		if err != nil {
			return err
		}
		carried := map[string]string{}
		for _, s := range d.Workflow.Stages[:writer] {
			outcome := outcomes[s.ID]
			if outcome != "passed" && outcome != "handled-fail" {
				return nil
			}
			carried[s.ID] = outcome
		}
		incoming, followed := false, false
		for _, e := range d.Workflow.Edges {
			if e.To != d.Workflow.Stages[writer].ID {
				continue
			}
			incoming = true
			if carried[e.From] == "" {
				return nil
			}
			match := carried[e.From] == "passed" && e.WhenOutcome != "fail" || carried[e.From] == "handled-fail" && e.WhenOutcome == "fail"
			for _, id := range e.RequiresPassed {
				if carried[id] == "" {
					return nil
				}
				match = match && carried[id] == "passed"
			}
			followed = followed || match
		}
		ready = !incoming || followed
		return nil
	})
	return ready, err
}
