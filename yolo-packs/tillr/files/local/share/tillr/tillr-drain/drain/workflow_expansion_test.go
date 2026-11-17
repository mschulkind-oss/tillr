package drain

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func expansionFile(t *testing.T, root, name, body string) {
	t.Helper()
	writeForkFile(t, filepath.Join(root, "config/tillr/workflows", name+"@v1.json"), `{"name":"`+name+`","version":1,`+body+`}`)
	runGit(t, root, "add", "config/tillr/workflows")
}

func TestWorkflowExpansionPortsParentsAndBarrier(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	expansionFile(t, f.root, "inner", `"inputs":{"source":"markdown"},"stages":[{"id":"read","kind":"agent","seat":"scout","access":"read_only","inputs":{"source":"markdown"}},{"id":"write","kind":"agent","seat":"build","access":"write","outputs":{"result":"markdown"}}],"edges":[{"from_node":"read","to_node":"write"}],"exit":{"requires_nodes":["read","write"],"outputs":{"result":{"node":"write","output":"result"}}}`)
	expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"before","kind":"agent","seat":"scout","access":"read_only","outputs":{"source":"markdown"}},{"id":"middle","kind":"workflow","workflow":"inner@v1","inputs":{"source":"markdown"},"outputs":{"result":"markdown"},"write_scope":["tools/item-a/"]},{"id":"after","kind":"agent","seat":"build","access":"write","inputs":{"result":"markdown"}}],"edges":[{"from_node":"before","from_output":"source","to_node":"middle","to_input":"source"},{"from_node":"middle","from_output":"result","to_node":"after","to_input":"result"}]`)
	w, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Stages) != 4 || w.Stages[1].ID != "middle/read" || w.Stages[1].Parent != "middle" || w.Stages[2].Parent != "middle" {
		t.Fatalf("stages: %+v", w.Stages)
	}
	if len(w.Stages[2].WriteScope.Paths) != 1 {
		t.Fatal("scope not inherited")
	}
	if err := validateOutputWiring(w); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range w.Edges {
		seen[e.From+"->"+e.To] = true
	}
	for _, edge := range []string{"before->middle/read", "middle/read->middle/write", "middle/read->after", "middle/write->after"} {
		if !seen[edge] {
			t.Fatalf("missing barrier %s: %+v", edge, w.Edges)
		}
	}
}

func TestWorkflowExpansionArgumentsAndCopies(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	expansionFile(t, f.root, "inner", `"args":{"prompts":{"type":"strings","most":3},"paths":{"type":"paths"},"check":{"type":"command"}},"stages":[{"id":"scout","kind":"agent","seat":"scout","access":"read_only","repeat":"prompts","prompt":"${item}"}],"edges":[]`)
	writeForkFile(t, filepath.Join(f.root, "config/tillr/commands.json"), `{"check":{"argv":["true"]}}`)
	var out bytes.Buffer
	if !CheckWorkflows(f.root, filepath.Join(f.root, "config/tillr/workflows/inner@v1.json"), &out) {
		t.Fatal(out.String())
	}
	for _, tc := range []struct{ args, rule string }{
		{`{"prompts":["a","b"],"paths":["tools/"],"check":"check"}`, ""},
		{`{"paths":[],"check":"check"}`, "workflow-argument-missing"},
		{`{"prompts":1,"paths":[],"check":"check"}`, "workflow-argument-type"},
		{`{"prompts":["a","b","c","d"],"paths":[],"check":"check"}`, "workflow-argument-most"},
		{`{"prompts":[],"paths":["../escape"],"check":"check"}`, "workflow-argument-type"},
		{`{"prompts":[],"paths":[],"check":"absent"}`, "command-reference"},
	} {
		expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"nested","kind":"workflow","workflow":"inner@v1","args":`+tc.args+`}],"edges":[]`)
		w, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy)
		if tc.rule != "" {
			if err == nil || !strings.Contains(err.Error(), tc.rule) {
				t.Fatalf("%s: %v", tc.rule, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(w.Stages) != 2 || w.Stages[0].ID != "nested/scout-1" || w.Stages[1].ID != "nested/scout-2" || w.Stages[1].Prompt != "b" || w.Stages[0].Parent != "nested" || w.Stages[1].Parent != "nested" {
			t.Fatalf("copies %+v", w.Stages)
		}
	}
}

func TestWorkflowExpansionDeepAndWide(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	nodes := []string{}
	for i := 0; i < 150; i++ {
		nodes = append(nodes, fmt.Sprintf(`{"id":"n%d","kind":"agent","seat":"scout","access":"read_only"}`, i))
	}
	expansionFile(t, f.root, "level9", `"stages":[`+strings.Join(nodes, ",")+`],"edges":[]`)
	for i := 8; i >= 0; i-- {
		expansionFile(t, f.root, fmt.Sprintf("level%d", i), fmt.Sprintf(`"stages":[{"id":"child","kind":"workflow","workflow":"level%d@v1"}],"edges":[]`, i+1))
	}
	w, err := loadWorkflow(f.root, "level0@v1", l.cfg.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Stages) != 150 || len(w.Sources) != 10 || w.ExpansionDepth != 9 {
		t.Fatalf("size %d sources %d depth %d", len(w.Stages), len(w.Sources), w.ExpansionDepth)
	}
	for i, s := range w.Stages {
		if s.ID != strings.Repeat("child/", 9)+fmt.Sprintf("n%d", i) {
			t.Fatalf("lost node: %s", s.ID)
		}
	}
	var out bytes.Buffer
	if !CheckWorkflows(f.root, filepath.Join(f.root, "config/tillr/workflows/level0@v1.json"), &out) || !strings.Contains(out.String(), "expansion-depth=9") {
		t.Fatal(out.String())
	}
}
