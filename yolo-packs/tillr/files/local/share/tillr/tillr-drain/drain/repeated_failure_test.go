package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRepeatedOutputBarrierWaitsForEveryPassingCopy(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	body := strings.Replace(repeatedFindingsBody, `}}],"edges"`, `}},{"id":"repair","kind":"agent","seat":"design","access":"write"}],"edges"`, 1)
	body = strings.Replace(body, `"edges":[`, `"edges":[{"from_node":"scout","to_node":"repair","when_outcome":"fail"},`, 1)
	body += `,"exit":{"requires_nodes":["join"],"requires_candidate":"commit_optional"}`
	expansionFile(t, f.root, "repeated-failure", body)
	w, err := expandWorkflow(f.root, filepath.Join(f.root, "config/tillr/workflows/repeated-failure@v1.json"), nil, map[string]bool{}, map[string]any{"prompts": []any{"one", "two"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES('item-a',1,'scout-1','first','passed'),('item-a',1,'scout-2','second','handled-fail')`)
		return err
	})
	next, err := l.nextWorkflowNode(context.Background(), Decision{ItemID: "item-a", Attempt: 1, Workflow: w})
	if err != nil {
		t.Fatal(err)
	}
	if next != 3 || w.Stages[next].ID != "repair" {
		t.Fatalf("handled failure released findings consumer: next=%d stages=%+v", next, w.Stages)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var outcome string
		if err := r.QueryRow(`SELECT outcome FROM node_runs WHERE item_id='item-a' AND node_id='join'`).Scan(&outcome); err != nil {
			return err
		}
		if outcome != "skipped" {
			t.Errorf("join=%s", outcome)
		}
		return nil
	})
}
