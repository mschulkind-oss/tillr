package drain

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const workflowFixture = `{"name":"scout-then-build","version":1,"stages":[{"id":"scout","kind":"agent","seat":"scout","access":"read_only"},{"id":"work","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"scout","to_node":"work"}]}`

func TestWorkflowCheckRuleFixturesAndHolds(t *testing.T) {
	rules := map[string]string{"cycle": "workflow-cycle", "recursive": "workflow-expansion-cycle", "missing": "workflow-missing-reference", "identity": "workflow-identity", "timer": "workflow-unknown-field", "schedule": "workflow-unknown-field", "undo": "workflow-unknown-field", "expression": "workflow-unknown-field", "outcome": "workflow-outcome", "empty-exit": "workflow-exit", "unknown-exit": "workflow-exit", "kind": "workflow-kind"}
	for fixture, rule := range rules {
		t.Run(fixture, func(t *testing.T) {
			f, l, steps := workflowLoopFixture(t, "")
			raw, err := os.ReadFile(filepath.Join("testdata/workflow-check", fixture, "check@v1.json"))
			if err != nil {
				t.Fatal(err)
			}
			// Preserve the target's versioned identity except in the identity fixture.
			raw = bytes.ReplaceAll(raw, []byte("check@v1"), []byte("scout-then-build@v1"))
			if fixture != "identity" {
				raw = bytes.ReplaceAll(raw, []byte(`"name": "check"`), []byte(`"name": "scout-then-build"`))
			}
			path := filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json")
			writeForkFile(t, path, string(raw))
			var out bytes.Buffer
			stage := map[string]string{"kind": "one", "undo": "one", "expression": "two", "outcome": "two", "recursive": "one", "missing": "one", "empty-exit": "exit", "unknown-exit": "exit"}[fixture]
			if stage == "" {
				stage = "graph"
			}
			if CheckWorkflows(f.root, path, &out) || !strings.Contains(out.String(), rule) || !strings.Contains(out.String(), path) || !strings.Contains(out.String(), "stage="+stage+" ") {
				t.Fatalf("check: %s", out.String())
			}
			for _, p := range mustPlan(t, l) {
				if p.decision.ItemID == "item-a" {
					if p.decision.Action != ActionHold || p.decision.RunRequest != nil || !strings.Contains(p.decision.Reason, "workflow-invalid: ") || !strings.Contains(p.decision.Reason, rule) || !strings.Contains(p.decision.Reason, path) {
						t.Fatalf("hold: %+v", p.decision)
					}
				}
			}
			if len(steps.runs) != 0 {
				t.Fatal("invalid workflow started a run")
			}
		})
	}
}

func TestWorkflowCheckTrackedExpansionAndIndirectCycle(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	path := filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json")
	inner := filepath.Join(f.root, "config/tillr/workflows/inner@v1.json")
	writeForkFile(t, inner, strings.Replace(workflowFixture, `"scout-then-build"`, `"inner"`, 1))
	writeForkFile(t, path, `{"name":"scout-then-build","version":1,"stages":[{"id":"nested","kind":"workflow","workflow":"inner@v1"}],"edges":[]}`)
	var out bytes.Buffer
	if CheckWorkflows(f.root, path, &out) || !strings.Contains(out.String(), "workflow-missing-reference") {
		t.Fatalf("untracked reference passed: %s", out.String())
	}
	runGit(t, f.root, "add", "config/tillr/workflows")
	out.Reset()
	if !CheckWorkflows(f.root, path, &out) || !strings.Contains(out.String(), "nodes=2 depth=2 fan-out=1") {
		t.Fatalf("expansion: %s", out.String())
	}
	w, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy)
	if err != nil || w.Stages[1].ID != "nested/work" || len(w.Sources) != 2 {
		t.Fatalf("expanded graph: %+v %v", w, err)
	}
	writeForkFile(t, inner, `{"name":"inner","version":1,"stages":[{"id":"back","kind":"workflow","workflow":"scout-then-build@v1"}],"edges":[]}`)
	out.Reset()
	if CheckWorkflows(f.root, path, &out) || !strings.Contains(out.String(), "workflow-expansion-cycle") {
		t.Fatalf("indirect recursion: %s", out.String())
	}
}

func TestWorkflowInvalidHoldKeepsOnlyFirstFinding(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	path := filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json")
	writeForkFile(t, path, strings.Replace(workflowFixture, `"version":1`, `"version":1,"timer":1,"schedule":"daily"`, 1))
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-a" {
			if !strings.Contains(p.decision.Reason, `"schedule"`) || strings.Contains(p.decision.Reason, `"timer"`) || strings.Contains(p.decision.Reason, "\n") || p.decision.RunRequest != nil {
				t.Fatalf("first finding: %+v", p.decision)
			}
			return
		}
	}
	t.Fatal("missing hold")
}

func TestWorkflowCheckRefusesSymlinkDirectory(t *testing.T) {
	root := scratchUnderDurable(t)
	path := filepath.Join(root, "alias")
	if err := os.Symlink(root, path); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if CheckWorkflows(root, path, &out) || !strings.Contains(out.String(), "workflow-file") {
		t.Fatalf("symlink directory silently passed: %s", out.String())
	}
}

func TestWorkflowCheckExplicitOutcomeAndSourceOnlyFields(t *testing.T) {
	for _, outcome := range []string{`""`, `null`, `1`, `"skip"`} {
		raw := strings.Replace(workflowFixture, `"to_node":"work"`, `"to_node":"work","when_outcome":`+outcome, 1)
		if _, err := decodeWorkflow([]byte(raw), "scout-then-build@v1"); err == nil || !strings.Contains(err.Error(), "workflow-outcome") {
			t.Fatalf("outcome %s: %v", outcome, err)
		}
	}
	for _, edit := range []struct{ old, replacement, rule string }{
		{`"id":"work"`, `"id":"scout/work"`, "workflow-stage-id"},
		{`"version":1`, `"version":1,"sha256":"forged"`, "workflow-unknown-field"},
	} {
		raw := strings.Replace(workflowFixture, edit.old, edit.replacement, 1)
		if _, err := decodeWorkflow([]byte(raw), "scout-then-build@v1"); err == nil || !strings.Contains(err.Error(), edit.rule) {
			t.Fatalf("%s: %v", edit.rule, err)
		}
	}
}

func TestWorkflowSizeCountsDistinctSuccessors(t *testing.T) {
	w, err := decodeWorkflow([]byte(workflowFixture), "scout-then-build@v1")
	if err != nil {
		t.Fatal(err)
	}
	w.Edges = append(w.Edges, w.Edges[0])
	nodes, depth, fanout := workflowSize(w)
	if nodes != 2 || depth != 2 || fanout != 1 {
		t.Fatalf("size=%d,%d,%d", nodes, depth, fanout)
	}
}

func TestWorkflowCheckPreservesNamedOutputWiring(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(map[bool]string{false: "flat", true: "nested"}[nested], func(t *testing.T) {
			f, l, _ := workflowLoopFixture(t, "")
			path := filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json")
			prefix := ""
			writeForkFile(t, path, namedWorkflow)
			if nested {
				inner := filepath.Join(f.root, "config/tillr/workflows/inner@v1.json")
				writeForkFile(t, inner, strings.Replace(namedWorkflow, `"scout-then-build"`, `"inner"`, 1))
				writeForkFile(t, path, `{"name":"scout-then-build","version":1,"stages":[{"id":"nested","kind":"workflow","workflow":"inner@v1"}],"edges":[]}`)
				runGit(t, f.root, "add", "config/tillr/workflows")
				prefix = "nested/"
			}
			var out bytes.Buffer
			if !CheckWorkflows(f.root, path, &out) || !strings.Contains(out.String(), "nodes=3 depth=3 fan-out=2") {
				t.Fatalf("named workflow check: %s", out.String())
			}
			w, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateOutputWiring(w); err != nil {
				t.Fatalf("expanded wiring: %v", err)
			}
			for _, edge := range w.Edges {
				if edge.From == prefix+"first" && edge.To == prefix+"work" && edge.FromOutput == "findings" && edge.ToInput == "evidence" {
					return
				}
			}
			t.Fatalf("named edge lost: %+v", w.Edges)
		})
	}
}

func TestWorkflowWrapperValidatesNamedFiles(t *testing.T) {
	raw := `{"name":"check","version":1,"stages":[{"id":"nested","kind":"workflow","workflow":"inner@v1","inputs":{"findings":"json"}}],"edges":[]}`
	if _, err := decodeWorkflow([]byte(raw), "check@v1"); err == nil || !strings.Contains(err.Error(), "workflow-input") {
		t.Fatalf("unfed wrapper input: %v", err)
	}
	raw = `{"name":"check","version":1,"stages":[{"id":"nested","kind":"workflow","workflow":"inner@v1","outputs":{"findings":"json"}}],"edges":[]}`
	if _, err := decodeWorkflow([]byte(raw), "check@v1"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowCheckNamedFindingStage(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement, stage, rule string }{
		{"absent output", `"from_output":"findings"`, `"from_output":"absent"`, "first", "workflow-output"},
		{"duplicate input", `"from_node":"second","to_node":"work"`, `"from_node":"first","from_output":"findings","to_node":"work","to_input":"evidence"`, "work", "workflow-input"},
		{"unfed input", `,"from_output":"findings"`, ``, "work", "workflow-input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := scratchUnderDurable(t)
			path := filepath.Join(root, "scout-then-build@v1.json")
			raw := strings.Replace(namedWorkflow, tc.old, tc.replacement, 1)
			if tc.name == "unfed input" {
				raw = strings.Replace(raw, `,"to_input":"evidence"`, ``, 1)
			}
			writeForkFile(t, path, raw)
			var out bytes.Buffer
			if CheckWorkflows(root, path, &out) || !strings.Contains(out.String(), "stage="+tc.stage+" "+tc.rule+":") {
				t.Fatalf("finding: %s", out.String())
			}
		})
	}
}

func TestWorkflowCheckReportsEveryUnknownField(t *testing.T) {
	root := scratchUnderDurable(t)
	path := filepath.Join(root, "scout-then-build@v1.json")
	writeForkFile(t, path, strings.Replace(workflowFixture, `"version":1`, `"version":1,"timer":1,"schedule":"daily"`, 1))
	var out bytes.Buffer
	if CheckWorkflows(root, path, &out) || strings.Count(out.String(), "workflow-unknown-field") != 2 {
		t.Fatalf("findings: %s", out.String())
	}
}
