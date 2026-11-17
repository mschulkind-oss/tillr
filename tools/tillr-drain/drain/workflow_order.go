package drain

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mschulkind-oss/tillr/store"
)

// nextWorkflowNode walks the existing serial order, admitting a node only when
// all incoming edges match the producer's recorded outcome.
func (l *Loop) nextWorkflowNode(ctx context.Context, d Decision) (int, error) {
	s, err := store.Open(ctx, l.cfg.Store, false)
	if err != nil {
		return -1, err
	}
	defer s.Close()
	outcomes := map[string]string{}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT node_id,outcome FROM node_runs WHERE item_id=? AND attempt=?`, d.ItemID, d.Attempt)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, outcome string
			if err := rows.Scan(&id, &outcome); err != nil {
				return err
			}
			outcomes[id] = outcome
		}
		return rows.Err()
	})
	if err != nil {
		return -1, err
	}
	for i := d.NodeIndex + 1; i < len(d.Workflow.Stages); i++ {
		ready := true
		for _, e := range d.Workflow.Edges {
			if e.To != d.Workflow.Stages[i].ID {
				continue
			}
			want := "passed"
			if e.WhenOutcome == "fail" {
				want = "handled-fail"
			}
			if outcomes[e.From] != want {
				ready = false
				break
			}
		}
		if ready {
			return i, nil
		}
	}
	return -1, nil
}

func commandNodeHold(r *store.Reader, id string) (string, error) {
	var node, outcome string
	err := r.QueryRow(`SELECT n.node_id,n.outcome FROM node_runs n JOIN runs r ON r.run_id=n.run_id
 WHERE n.item_id=? AND r.backend='command' AND n.attempt=(SELECT MAX(attempt) FROM node_runs WHERE item_id=?)
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
