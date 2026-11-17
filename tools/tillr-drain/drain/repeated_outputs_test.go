package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

const repeatedFindingsBody = `"args":{"prompts":{"type":"strings","most":8}},"stages":[{"id":"scout","kind":"agent","seat":"scout","access":"read_only","repeat":"prompts","prompt":"${item}","outputs":{"findings":"markdown"}},{"id":"join","kind":"agent","seat":"design","access":"write","inputs":{"findings":"markdown"}}],"edges":[{"from_node":"scout","from_output":"findings","to_node":"join","to_input":"findings"}]`

func TestRepeatedOutputCopiesAndProducerPaths(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	expansionFile(t, f.root, "repeated", repeatedFindingsBody)
	w, err := expandWorkflow(f.root, filepath.Join(f.root, "config/tillr/workflows/repeated@v1.json"), nil, map[string]bool{}, map[string]any{"prompts": []any{"a", "b", "c"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOutputWiring(w); err != nil {
		t.Fatal(err)
	}
	// Persisted graphs retain the fact that one template edge expanded to copies.
	raw, _ := json.Marshal(w)
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	if err := validateOutputWiring(w); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for i := 1; i <= 3; i++ {
			node := fmt.Sprintf("scout-%d", i)
			// Use a continuation's run id rather than the original dispatch id.
			if _, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES('item-a',1,?,?,'passed')`, node, "accepted-"+node); err != nil {
				return err
			}
			location := filepath.Join(nodeOutputsDir(f.root, "accepted-"+node), "findings")
			if _, err := tx.Exec(`INSERT INTO node_artifacts(item_id,attempt,node_id,run_id,name,type,sha256,location) VALUES('item-a',1,?,?,'findings','markdown','hash',?)`, node, "accepted-"+node, location); err != nil {
				return err
			}
		}
		return nil
	})
	d := Decision{ItemID: "item-a", Attempt: 1, NodeIndex: 3, Workflow: w, RunRequest: &run.Request{RunID: "join"}}
	paths, err := l.nodeInputPaths(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths["findings"]) != 3 {
		t.Fatalf("want exactly three files: %+v", paths)
	}
	encoded, _ := json.Marshal(paths)
	previous := -1
	for i := 1; i <= 3; i++ {
		node := fmt.Sprintf("scout-%d", i)
		want := filepath.Join(nodeOutputsDir(f.root, "accepted-"+node), "findings")
		index := strings.Index(string(encoded), want)
		if index <= previous || !strings.Contains(string(encoded), `"producer":"`+node+`"`) {
			t.Fatalf("unordered or unbound input %s: %s", node, encoded)
		}
		previous = index
	}
	// Evaluator receipts retain their string-valued format, with every repeated
	// file listed in copy order. A single producer retains the original path.
	evaluatorFiles := evaluatorInputFiles(paths)
	wantFiles := []string{}
	for _, input := range paths["findings"] {
		wantFiles = append(wantFiles, input.Path)
	}
	if evaluatorFiles["findings"] != strings.Join(wantFiles, "\n") {
		t.Fatalf("evaluator lost repeated inputs: %+v", evaluatorFiles)
	}
	single := evaluatorInputFiles(map[string][]NodeInput{"findings": paths["findings"][:1]})
	if single["findings"] != wantFiles[0] {
		t.Fatalf("evaluator changed single input: %+v", single)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE node_runs SET outcome='failed' WHERE run_id='accepted-scout-2'`)
		return err
	})
	if _, err := l.nodeInputPaths(context.Background(), d); err == nil || !strings.Contains(err.Error(), "node-input-missing") {
		t.Fatalf("missing passed producer: %v", err)
	}
}

func TestRepeatedOutputRefusalsAndCheckBothCardinalities(t *testing.T) {
	f, _, _ := workflowLoopFixture(t, "")
	for _, tc := range []struct{ name, body, rule string }{
		{"duplicate", strings.Replace(repeatedFindingsBody, `"edges":[`, `"edges":[{"from_node":"scout","from_output":"findings","to_node":"join","to_input":"findings"},`, 1), "workflow-input"},
		{"exit", repeatedFindingsBody + `,"exit":{"requires_nodes":["scout","join"],"outputs":{"findings":{"node":"scout","output":"findings"}}}`, "workflow-output-promise"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expansionFile(t, f.root, tc.name, tc.body)
			// Exiting a repeated output must be refused even with one copy.
			args := map[string]any{"prompts": []any{"one"}}
			_, err := expandWorkflow(f.root, filepath.Join(f.root, "config/tillr/workflows/"+tc.name+"@v1.json"), nil, map[string]bool{}, args, nil)
			if err == nil || !strings.Contains(err.Error(), tc.rule) {
				t.Fatalf("want %s: %v", tc.rule, err)
			}
		})
	}
	// One copy has id scout-1, which only collides with this literal id at two copies.
	expansionFile(t, f.root, "collision", `"args":{"prompts":{"type":"strings","most":2}},"stages":[{"id":"scout","kind":"agent","seat":"scout","access":"read_only","repeat":"prompts"},{"id":"scout-2","kind":"agent","seat":"design","access":"write"}],"edges":[]`)
	var out bytes.Buffer
	if CheckWorkflows(f.root, filepath.Join(f.root, "config/tillr/workflows/collision@v1.json"), &out) || !strings.Contains(out.String(), "workflow-duplicate-id") {
		t.Fatalf("checker missed second copy: %s", out.String())
	}
}
