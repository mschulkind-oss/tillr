package drain

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr/store"
	"testing"
)

func TestWorkflowSnapshotSurvivesSourceEditAndRestart(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	graphPass(t, l, result)
	graphPass(t, l, result)
	if len(steps.runs) != 1 {
		t.Fatalf("first node: %+v", steps.runs)
	}
	before := pendingForTest(t, f, "item-a").Decision.Workflow
	path := filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(raw))
	if before.SHA256 != want || before.Stages[1].ID != "two" {
		t.Fatalf("snapshot: %+v", before)
	}
	writeForkFile(t, path, workflowFixture)
	if want == fmt.Sprintf("%x", sha256.Sum256([]byte(workflowFixture))) {
		t.Fatal("fixture did not change")
	}
	next := replaceGraphLoop(t, f, l, steps)
	graphPass(t, next, result)
	graphPass(t, next, result)
	after := pendingForTest(t, f, "item-a").Decision
	if len(steps.runs) != 2 || after.Workflow.Stages[1].ID != "two" || after.Workflow.SHA256 != want || after.Workflow.Sources["scout-then-build@v1"] != want {
		t.Fatalf("source edit replaced the graph: runs=%+v decision=%+v", steps.runs, after)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var node string
		if err := tx.QueryRow(`SELECT node_id FROM node_runs WHERE run_id=?`, steps.runs[1].RunID).Scan(&node); err != nil {
			return err
		}
		if node != "two" {
			t.Fatalf("dispatched second node %q, want original two", node)
		}
		return nil
	})
}
