package drain

import (
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestStartNodeRecordsSeatAndActualRoute(t *testing.T) {
	f, _, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		d := Decision{ItemID: "item-a", Attempt: 1, Seat: "verify", Workflow: &Workflow{Stages: []WorkflowStage{{ID: "check", Seat: "verify"}}}, RunRequest: &run.Request{RunID: "cost-run", Backend: "pi", Command: []string{"adapter", "--model", "actual-route"}}}
		if err := startNode(tx, d); err != nil {
			return err
		}
		var seat, model, id string
		if err := tx.QueryRow(`SELECT seat,model,run_id FROM node_runs WHERE item_id='item-a' AND node_id='check'`).Scan(&seat, &model, &id); err != nil {
			return err
		}
		if seat != "verify" || model != "actual-route" || id != "cost-run" {
			t.Errorf("got seat=%q model=%q run=%q", seat, model, id)
		}
		d.RunRequest.RunID = "cost-run-restart"
		d.RunRequest.Command = []string{"adapter", "--model", "new-route"}
		if err := startNode(tx, d); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT seat,model,run_id FROM node_runs WHERE item_id='item-a' AND node_id='check'`).Scan(&seat, &model, &id); err != nil {
			return err
		}
		if seat != "verify" || model != "new-route" || id != "cost-run-restart" {
			t.Errorf("restart: seat=%q model=%q run=%q", seat, model, id)
		}
		return nil
	})
}
