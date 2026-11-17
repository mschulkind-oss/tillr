package drain

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowExpansionOutputPromiseHasSameCheckFinding(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	expansionFile(t, f.root, "inner", `"stages":[{"id":"work","kind":"agent","seat":"scout","access":"read_only","outputs":{"result":"markdown"}}],"edges":[]`)
	expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"nested","kind":"workflow","workflow":"inner@v1","outputs":{"result":"markdown"}}],"edges":[]`)
	_, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy)
	if err == nil || !strings.Contains(err.Error(), "workflow-output-promise") {
		t.Fatalf("promise %v", err)
	}
	var out bytes.Buffer
	if CheckWorkflows(f.root, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), &out) || !strings.Contains(out.String(), "workflow-output-promise") {
		t.Fatal(out.String())
	}
}

func TestWorkflowExpansionForwardsTypedArgsAndAllowsEmptyCopies(t *testing.T) {
	for _, list := range []string{`["first","second"]`, `[]`} {
		f, l, _ := workflowLoopFixture(t, "")
		expansionFile(t, f.root, "inner", `"args":{"prompts":{"type":"strings","most":2},"paths":{"type":"paths"},"check":{"type":"command"}},"stages":[{"id":"before","kind":"agent","seat":"scout","access":"read_only"},{"id":"scout","kind":"agent","seat":"scout","access":"read_only","repeat":"prompts","prompt":"${item}","outputs":{"findings":"markdown"}},{"id":"check","kind":"command","command_ref":"${args.check}","access":"read_only"},{"id":"work","kind":"agent","seat":"build","access":"write","write_scope":"${args.paths}"}],"edges":[{"from_node":"before","to_node":"scout"},{"from_node":"scout","to_node":"check"},{"from_node":"check","to_node":"work"}]`)
		writeForkFile(t, filepath.Join(f.root, "config/tillr/commands.json"), `{"check":{"argv":["true"]}}`)
		expansionFile(t, f.root, "forward", `"args":{"prompts":{"type":"strings","most":2}},"stages":[{"id":"nested","kind":"workflow","workflow":"inner@v1","args":{"prompts":"${args.prompts}","paths":["tools/item-a/"],"check":"check"}}],"edges":[]`)
		expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"forward","kind":"workflow","workflow":"forward@v1","args":{"prompts":`+list+`}}],"edges":[]`)
		w, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy)
		if err != nil {
			t.Fatal(err)
		}
		want := 5
		if list == `[]` {
			want = 3
		}
		if len(w.Stages) != want || w.Stages[len(w.Stages)-1].WriteScope.Paths[0] != "tools/item-a/" || w.Stages[1].Parent != "forward/nested" && list == `[]` {
			t.Fatalf("typed args %+v", w.Stages)
		}
		if list == `[]` {
			found := false
			for _, e := range w.Edges {
				if e.From == "forward/nested/before" && e.To == "forward/nested/check" {
					found = true
				}
			}
			if !found {
				t.Fatalf("empty repeat lost ordering: %+v", w.Edges)
			}
		}
	}
}
