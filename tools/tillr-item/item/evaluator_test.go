package item_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Direct package calls must not bypass persisted-selection checks used by CLI intake.
func TestRecordVerdictAndCheckJudgeShareEvaluatorBinding(t *testing.T) {
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(scratch(t), "item.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	text := "criterion"
	graph := `{"stages":[{"id":"verify","kind":"agent","access":"read_only","judge_classes":["verification-only"]}],"exit":{"evaluator_slot":"verify","requires_nodes":["verify"]}}`
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, q := range []string{
			`INSERT INTO items(item_id,title,state,body_ref,updated_at) VALUES('test','test','not-started','',0)`,
			`INSERT INTO item_criteria(item_id,criterion_id,text,state,claim_class) VALUES('test','c1','criterion','','verification-only')`,
			`CREATE TABLE workflow_selections(item_id TEXT PRIMARY KEY, workflow TEXT,source TEXT,graph TEXT)`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO workflow_selections VALUES('test','review@v1','block',?)`, graph)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	head, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	output, _ := json.Marshal(item.JudgeOutput{EvaluatedHead: head, EvaluatedTree: tree, Outcome: "pass", Verdicts: []item.JudgeEntry{{CriterionID: "c1", CriterionHash: item.CriterionHash(text), Verdict: "done", ClaimClass: item.ClaimVerification, Reason: "checked", Evidence: []string{"commit:" + head}}}})
	for _, wrong := range []bool{false, true} {
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			binding, err := item.ReadEvaluatorBinding(tx.Reader(), "test")
			if err != nil {
				return err
			}
			if wrong {
				binding.GraphSHA256 = item.CriterionHash(graph + " ")
			}
			options := item.JudgeOptions{EvaluatorBinding: binding, ItemID: "test", Head: head, Tree: tree, Actor: "judge-run", JudgedActor: "writer-run", At: at}
			result, checkErr := item.CheckJudge(ctx, tx.Reader(), output, options)
			payload := map[string]any{"item_id": "test", "criterion_id": "c1", "criterion_hash": item.CriterionHash(text), "claim_class": item.ClaimVerification, "verdict": "done", "reason": "checked", "evidence": []string{"commit:" + head}, "actor_kind": "agent", "actor": "judge-run", "judged_actor": "writer-run", "evaluated_head": head, "evaluated_tree": tree, "workflow": binding.Workflow, "evaluator_node": binding.EvaluatorNode, "graph_sha256": binding.GraphSHA256}
			raw, _ := json.Marshal(payload)
			_, writeErr := item.RecordVerdict(ctx, tx, record.Envelope{Kind: item.VerdictRecordKind, CreatedAt: at, Payload: raw})
			if wrong {
				for _, err := range []error{checkErr, writeErr} {
					if rejection, ok := item.RejectionOf(err); !ok || rejection.Rule != "evaluator-binding" {
						t.Fatalf("shared refusal: %v", err)
					}
				}
			} else {
				if checkErr != nil || writeErr != nil || len(result.Records) != 1 {
					t.Fatalf("valid intake: %v %v", checkErr, writeErr)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
