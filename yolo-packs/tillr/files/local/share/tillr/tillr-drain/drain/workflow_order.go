package drain

import (
	"context"
	"database/sql"
	"fmt"

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
	var node, outcome string
	err := r.QueryRow(`SELECT n.node_id,n.outcome FROM node_runs n JOIN runs r ON r.run_id=n.run_id
 WHERE n.item_id=? AND n.attempt=(SELECT MAX(attempt) FROM node_runs WHERE item_id=?)
 AND n.outcome IN ('failed','fault') ORDER BY n.rowid DESC LIMIT 1`, id, id).Scan(&node, &outcome)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if outcome == "fault" {
		return ReasonInfraExhausted, nil
	}
	return fmt.Sprintf("node-failed: %s", node), nil
}
