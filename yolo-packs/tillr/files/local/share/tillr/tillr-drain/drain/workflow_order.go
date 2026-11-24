package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/mschulkind-oss/tillr-run/run"

	"github.com/mschulkind-oss/tillr/store"
)

// nextWorkflowNode waits for incoming producers to settle, then follows any
// matching edge. This admits joins of mutually exclusive pass/fail branches;
// a node with no followed incoming edge is skipped, never dispatched.
func (l *Loop) nextWorkflowNode(ctx context.Context, d Decision) (int, error) {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return -1, err
	}
	defer s.Close()
	index := -1
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		outcomes, err := workflowOutcomes(tx.Reader(), d.ItemID, d.Attempt)
		if err != nil {
			return err
		}
		if workflowDone(d.Workflow, outcomes) {
			return nil
		}
		for i, stage := range d.Workflow.Stages {
			if outcomes[stage.ID] != "" {
				continue
			}
			incoming, settled, followed := false, true, false
			for _, edge := range d.Workflow.Edges {
				if edge.To != stage.ID {
					continue
				}
				incoming = true
				outcome := outcomes[edge.From]
				if outcome == "" || outcome == "running" {
					settled = false
				}
				matches := outcome == "passed"
				if edge.WhenOutcome == "fail" {
					matches = outcome == "handled-fail"
				}
				for _, id := range edge.RequiresPassed {
					matches = matches && outcomes[id] == "passed"
					if outcomes[id] == "" || outcomes[id] == "running" {
						settled = false
					}
				}
				followed = followed || matches
			}
			if !settled {
				continue
			}
			if !incoming || followed {
				index = i
				return nil
			}
			_, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES(?,?,?,?, 'skipped') ON CONFLICT(item_id,attempt,node_id) DO NOTHING`, d.ItemID, d.Attempt, stage.ID, workflowNodeRunID(d, i))
			if err != nil {
				return err
			}
			outcomes[stage.ID] = "skipped"
		}
		return nil
	})
	return index, err
}

func commandNodeHold(r *store.Reader, id string) (string, error) {
	var node, outcome, runID string
	err := r.QueryRow(`SELECT n.node_id,n.outcome,n.run_id FROM node_runs n JOIN runs r ON r.run_id=n.run_id
 WHERE n.item_id=? AND n.attempt=(SELECT MAX(attempt) FROM node_runs WHERE item_id=?)
 AND n.outcome IN ('failed','fault','refused','unjudged','repeating-call') ORDER BY n.rowid DESC LIMIT 1`, id, id).Scan(&node, &outcome, &runID)
	if err == sql.ErrNoRows {
		return unreadAnswerHold(r, id)
	}
	if err != nil {
		return "", err
	}
	if outcome == "unjudged" {
		return ReasonRunUnjudged, nil
	}
	if outcome == "refused" {
		// A build step that reported the plan wrong refused nothing about its checkout: the
		// item goes back to the plan step (RefusalPlanWrong) rather than holding.
		var step string
		err := r.QueryRow(`SELECT json_extract(payload,'$.step') FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, RefusalKind, runID).Scan(&step)
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
		if step == RefusalPlanWrong {
			return "", nil
		}
		return ReasonBoundaryScopeEscape, nil
	}
	if outcome == "fault" {
		return ReasonCommandFault, nil
	}
	if outcome == ReasonRepeatingCall {
		return ReasonRepeatingCall, nil
	}
	// A successful harness with a missing declared file is not a failed task.
	// Read the refusal for this producer, not the last sibling to finish.
	var step string
	err = r.QueryRow(`SELECT json_extract(payload,'$.step') FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, RefusalKind, runID).Scan(&step)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if step == RefusalPlanOutput {
		return "", nil
	}
	if step == RefusalNodeOutput {
		return RefusalNodeOutput, nil
	}
	return fmt.Sprintf("%s: %s", ReasonNodeFailed, node), nil
}

// unreadAnswerHold names a node recorded `blocked` whose own report does not say blocked.
// A settlement that misread a footer — `**RESULT: done**` read as "done**" before
// reportFooter read through emphasis — recorded exactly that, raised no conflict and
// recorded no cause, so the planner had nothing to name but run-link-missing. The run's
// recorded report is the node's answer, so it is the evidence read here; the hold names
// the node, and its row names the retry that keeps every read-only node that passed.
func unreadAnswerHold(r *store.Reader, id string) (string, error) {
	var node, runID, refs string
	err := r.QueryRow(`SELECT n.node_id,n.run_id,r.artifact_refs FROM node_runs n JOIN runs r ON r.run_id=n.run_id
 WHERE n.item_id=? AND n.attempt=(SELECT MAX(attempt) FROM node_runs WHERE item_id=?)
 AND n.outcome='blocked' ORDER BY n.rowid DESC LIMIT 1`, id, id).Scan(&node, &runID, &refs)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if isObstacleCallRun(r, runID) {
		return "", nil
	}
	var artifacts []run.ArtifactRef
	if err := json.Unmarshal([]byte(refs), &artifacts); err != nil {
		return "", nil
	}
	report := logTail(artifacts)
	if report == "" {
		return "", nil
	}
	switch reportFooter(report) {
	case "blocked", "trunk-broken":
		return "", nil
	}
	return fmt.Sprintf("%s: %s", ReasonNodeAnswerUnread, node), nil
}

// retainedReaders answers what a retry of a workflow item keeps from attempt prev: every
// read-only node that passed there, provided no write node of that attempt had run. A
// read-only node changed no file, so its retained outputs are still the answer to its
// brief; a write node's work is the candidate a retry replaces, and whatever read the tree
// for it is read again. The new attempt starts at the first node not kept. An attempt with
// nothing to keep, or with a write node run, keeps nothing and starts at node 0.
func retainedReaders(r *store.Reader, id string, prev int, w *Workflow) (int, []string, error) {
	if prev < 1 {
		return 0, nil, nil
	}
	outcomes, err := workflowOutcomes(r, id, prev)
	if err != nil {
		return 0, nil, err
	}
	first, kept := retainedReadersFromOutcomes(outcomes, prev, w)
	return first, kept, nil
}

func retainedReadersFromOutcomes(outcomes map[string]string, prev int, w *Workflow) (int, []string) {
	if prev < 1 {
		return 0, nil
	}
	kept, first := []string{}, -1
	for i, stage := range w.Stages {
		if stage.Access != "read_only" && outcomes[stage.ID] != "" {
			return 0, nil
		}
		if stage.Access == "read_only" && outcomes[stage.ID] == "passed" {
			kept = append(kept, stage.ID)
		} else if first < 0 {
			first = i
		}
	}
	if len(kept) == 0 || first < 0 {
		return 0, nil
	}
	return first, kept
}

// carryRetainedReaders records a retry's kept nodes against the new attempt at claim, each
// pointing at the attempt whose run passed it, so outcomes, sibling selection and input
// paths read them as this attempt's own (effectiveNodeRuns, nodeInputPaths).
func carryRetainedReaders(tx *store.Tx, d Decision) error {
	for _, id := range d.ReusedNodes {
		source, err := nodeSourceAttempt(tx.Reader(), d.ItemID, d.ReusedAttempt, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO node_reused(item_id,attempt,node_id,source_attempt) VALUES(?,?,?,?) ON CONFLICT(item_id,attempt,node_id) DO NOTHING`, d.ItemID, d.Attempt, id, source); err != nil {
			return err
		}
	}
	return nil
}
