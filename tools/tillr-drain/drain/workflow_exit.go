package drain

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func workflowCandidatePolicy(w *Workflow) string {
	if w.Exit == nil || w.Exit.RequiresCandidate == "" {
		return "commit_required"
	}
	return w.Exit.RequiresCandidate
}

// Required producers must pass. Propagate reachability under that constraint to
// reject required descendants whose only paths need one of those producers to fail.
func validateWorkflowExit(w *Workflow) error {
	if err := validateEvaluator(w); err != nil {
		return err
	}
	required := map[string]bool{}
	reachable := map[string]bool{}
	for _, id := range requiredWorkflowNodes(w) {
		required[id] = true
	}
	for _, stage := range w.Stages {
		incoming, possible := false, false
		for _, edge := range w.Edges {
			if edge.To == stage.ID {
				incoming = true
				if reachable[edge.From] && (edge.WhenOutcome != "fail" || !required[edge.From]) {
					possible = true
				}
			}
		}
		reachable[stage.ID] = !incoming || possible
	}
	for _, id := range requiredWorkflowNodes(w) {
		if !reachable[id] {
			return fmt.Errorf("workflow-exit: exit: required node %s is reachable only after another required node fails", id)
		}
	}
	return nil
}

func workflowOutcomes(r *store.Reader, id string, attempt int) (map[string]string, error) {
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='node_runs')`).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return map[string]string{}, nil
	}
	rows, err := r.Query(`SELECT node_id,outcome FROM node_runs WHERE item_id=? AND attempt=?`, id, attempt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	outcomes := map[string]string{}
	for rows.Next() {
		var node, outcome string
		if err := rows.Scan(&node, &outcome); err != nil {
			return nil, err
		}
		outcomes[node] = outcome
	}
	return outcomes, rows.Err()
}
func workflowDone(w *Workflow, outcomes map[string]string) bool {
	for _, id := range requiredWorkflowNodes(w) {
		if outcomes[id] != "passed" {
			return false
		}
	}
	return true
}

// Finish a graph only after its declared required nodes pass. A no-commit
// completion records its own evidence and mechanical verdicts, then uses the
// same item-state step as a landing without invoking gate, land, or verdict programs.
func (l *Loop) finishWorkflow(ctx context.Context, d Decision, rec run.Record, base, head string, result *LoopResult) (bool, error) {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return true, err
	}
	defer s.Close()
	done := false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		outcomes, err := workflowOutcomes(tx.Reader(), d.ItemID, d.Attempt)
		if err != nil {
			return err
		}
		done = workflowDone(d.Workflow, outcomes)
		if !done {
			return nil
		}
		for i, stage := range d.Workflow.Stages {
			if outcomes[stage.ID] == "" {
				_, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES(?,?,?,?, 'skipped') ON CONFLICT(item_id,attempt,node_id) DO NOTHING`, d.ItemID, d.Attempt, stage.ID, attemptRunID(d.ItemID, d.Attempt)+"-n"+strconv.Itoa(i+1))
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return true, err
	}
	if !done {
		// Exhausted graph, but its required branch never passed: retain a durable
		// failed-node hold rather than admitting a partial candidate to the gate.
		if err := l.nodeOutcome(ctx, d, "failed"); err != nil {
			return true, err
		}
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, "workflow exit has unpassed required nodes"); stop != "" {
			return true, Externalf("lane %s retained after preservation failed", d.LaneID)
		}
		return true, l.markExamined(ctx, d)
	}
	policy := workflowCandidatePolicy(d.Workflow)
	candidate := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID, Worktree: l.laneTarget(d.LaneID), BaseSHA: base, HeadSHA: head}
	if policy == "no_commit" && head != base {
		candidate.RefusalDetail = "workflow exit forbids a commit"
		if err := l.refuse(ctx, candidate, result, RefusalFence); err != nil {
			return true, err
		}
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, candidate.RefusalDetail); stop != "" {
			return true, Externalf("lane %s retained after preservation failed", d.LaneID)
		}
		return true, l.markExamined(ctx, d)
	}
	if head != base || policy == "commit_required" {
		return false, nil
	}
	dirty, err := l.git(ctx, candidate.Worktree, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return true, err
	}
	if strings.TrimSpace(dirty) != "" {
		candidate.RefusalDetail = "workflow exit without commit left uncommitted changes"
		if err := l.refuse(ctx, candidate, result, RefusalFence); err != nil {
			return true, err
		}
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, candidate.RefusalDetail); stop != "" {
			return true, Externalf("lane %s retained after preservation failed", d.LaneID)
		}
		return true, l.markExamined(ctx, d)
	}
	return true, l.settleGraphExit(ctx, d, candidate)
}

func (l *Loop) settleGraphExit(ctx context.Context, d Decision, c Candidate) error {
	if err := l.completeGraphWithoutCommit(ctx, d, c); err != nil {
		return err
	}
	l.reportf("loop: %s completed its graph; no commit by design, nothing landed\n", d.ItemID)
	if stop := l.abandon(ctx, d.LaneID, "", ""); stop != "" {
		return Externalf("lane %s cleanup failed: %s", d.LaneID, stop)
	}
	return l.markExamined(ctx, d)
}

// completeItem settles a passed, landed candidate through the configured verdict program.
func (l *Loop) completeItem(ctx context.Context, c Candidate) error {
	if c.BlockConflict != nil {
		return l.answerBlock(ctx, c)
	}
	if c.Judge != nil {
		return l.recordLandedJudge(ctx, c)
	}
	if err := l.cfg.Steps.Verdict(ctx, c, VerdictPassed); err != nil {
		return err
	}
	return l.cfg.Steps.ItemState(ctx, c.ItemID, c.RunID, StateLanded)
}
