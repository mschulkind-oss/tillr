package workflow

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUnitCheckWorkflows(t *testing.T) {
	root := t.TempDir()
	wfDir := filepath.Join(root, "config", "tillr", "workflows")
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		t.Fatal(err)
	}
	validPath := filepath.Join(wfDir, "sample@v1.json")
	if err := os.WriteFile(validPath, []byte(`{"name":"sample","version":1,"stages":[{"id":"scout","kind":"agent","seat":"scout","access":"read_only"},{"id":"work","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"scout","to_node":"work"}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if !CheckWorkflows(root, validPath, &out) {
		t.Fatalf("expected valid workflow to pass: %s", out.String())
	}
	if !strings.Contains(out.String(), "nodes=2 depth=2 fan-out=1") {
		t.Fatalf("unexpected CheckWorkflows output: %s", out.String())
	}

	missingPath := filepath.Join(wfDir, "missing@v1.json")
	out.Reset()
	if CheckWorkflows(root, missingPath, &out) || !strings.Contains(out.String(), "workflow-file") {
		t.Fatalf("expected missing file failure, got: %s", out.String())
	}
}

func TestUnitBindWorkflow(t *testing.T) {
	root := t.TempDir()
	cmdDir := filepath.Join(root, "config", "tillr")
	if err := os.MkdirAll(cmdDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "commands.json"), []byte(`{"lint":{"argv":["echo","ok"],"flags":{}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	tmpl := `{
		"name":"tmpl",
		"version":1,
		"args":{"targets":{"type":"paths"},"notes":{"type":"strings","most":2},"cmd":{"type":"command"}},
		"stages":[
			{"id":"note","kind":"agent","seat":"scout","access":"read_only","repeat":"notes","prompt":"check ${item}"},
			{"id":"run","kind":"command","command_ref":"${args.cmd}","access":"read_only"},
			{"id":"work","kind":"agent","seat":"build","access":"write","write_scope":"${args.targets}"}
		],
		"edges":[{"from_node":"note","to_node":"run"},{"from_node":"run","to_node":"work"}]
	}`
	bound, err := BindWorkflow(root, []byte(tmpl), map[string]any{
		"targets": []any{"tools/tillr-drain/workflow/"},
		"notes":   []any{"a", "b"},
		"cmd":     "lint",
	})
	if err != nil {
		t.Fatalf("BindWorkflow failed: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(bound, &decoded); err != nil {
		t.Fatal(err)
	}
	stages := decoded["stages"].([]any)
	if stages[1].(map[string]any)["command_ref"] != "lint" {
		t.Fatalf("command_ref not substituted: %+v", stages[1])
	}

	if _, err := BindWorkflow(root, []byte(tmpl), map[string]any{
		"targets": []any{"tools/tillr-drain/workflow/"},
		"notes":   []any{"a", "b", "c"},
		"cmd":     "lint",
	}); err == nil || !strings.Contains(err.Error(), "workflow-argument-most") {
		t.Fatalf("expected workflow-argument-most, got %v", err)
	}

	if _, err := BindWorkflow(root, []byte(tmpl), map[string]any{
		"targets": []any{"tools/tillr-drain/workflow/"},
		"cmd":     "lint",
	}); err == nil || !strings.Contains(err.Error(), "workflow-argument-missing") {
		t.Fatalf("expected workflow-argument-missing, got %v", err)
	}

	if _, err := BindWorkflow(root, []byte(tmpl), map[string]any{
		"targets": []any{"tools/tillr-drain/workflow/"},
		"notes":   []any{"a"},
		"cmd":     "lint",
		"extra":   "bad",
	}); err == nil || !strings.Contains(err.Error(), "workflow-argument-unknown") {
		t.Fatalf("expected workflow-argument-unknown, got %v", err)
	}
}

func TestUnitParseWorkflowPiece(t *testing.T) {
	root := t.TempDir()
	piece, err := ParseWorkflowPiece(root, map[string]any{
		"id":          "piece-a",
		"seat":        "build",
		"access":      "write",
		"write_scope": []any{"tools/tillr-drain/workflow/"},
		"prompt":      "implement workflow",
		"outputs":     map[string]any{"report": "markdown"},
	})
	if err != nil {
		t.Fatalf("ParseWorkflowPiece failed: %v", err)
	}
	if piece.ID != "piece-a" || piece.Seat != "build" || piece.Access != "write" {
		t.Fatalf("unexpected piece: %+v", piece)
	}

	for _, tc := range []struct {
		name string
		in   map[string]any
		want string
	}{
		{
			name: "read-only seat writing",
			in:   map[string]any{"id": "p", "seat": "verify", "access": "write", "write_scope": []any{"tools/"}, "prompt": "x"},
			want: "workflow-read-only-seat-writes",
		},
		{
			name: "read_only with write_scope",
			in:   map[string]any{"id": "p", "seat": "scout", "access": "read_only", "write_scope": []any{"tools/"}, "prompt": "x"},
			want: "invalid piece write_scope",
		},
		{
			name: "parent traversal in write_scope",
			in:   map[string]any{"id": "p", "seat": "build", "access": "write", "write_scope": []any{"../escape"}, "prompt": "x"},
			want: "command-path",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseWorkflowPiece(root, tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestUnitScopeWithinAndScopeForLane(t *testing.T) {
	if !ScopeWithin([]string{"tools/tillr-drain/workflow/"}, []string{"tools/tillr-drain/"}) {
		t.Fatal("expected inner dir to be within outer dir")
	}
	if ScopeWithin([]string{"tools/tillr-drain/workflow/", "tools/other/"}, []string{"tools/tillr-drain/"}) {
		t.Fatal("expected sibling dir to fail ScopeWithin")
	}
	if ScopeWithin([]string{"tools/tillr-drain/../other/"}, []string{"tools/tillr-drain/"}) {
		t.Fatal("expected cleaned traversal outside outer dir to fail ScopeWithin")
	}

	got := ScopeForLane([]string{".forks/lanes/lane-1/pi-subagents/src/", "tools/tillr-drain/"}, "lane-6")
	want := []string{".forks/lanes/lane-6/pi-subagents/src/", "tools/tillr-drain/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScopeForLane got %v want %v", got, want)
	}
	if ScopeForLane(nil, "lane-6") != nil {
		t.Fatal("expected nil ScopeForLane(nil)")
	}
}

func TestUnitValidateEvaluatorAndExit(t *testing.T) {
	valid := &Workflow{
		Stages: []WorkflowStage{
			{ID: "work", Kind: "agent", Seat: "build", Access: "write"},
			{ID: "eval", Kind: "agent", Seat: "verify", Access: "read_only"},
		},
		Edges: []WorkflowEdge{{From: "work", To: "eval"}},
		Exit:  &WorkflowExit{EvaluatorSlot: "eval", RequiresNodes: []string{"work", "eval"}},
	}
	if err := ValidateEvaluator(valid); err != nil {
		t.Fatalf("ValidateEvaluator valid failed: %v", err)
	}
	if err := ValidateWorkflowExit(valid); err != nil {
		t.Fatalf("ValidateWorkflowExit valid failed: %v", err)
	}

	writeEval := &Workflow{
		Stages: []WorkflowStage{{ID: "eval", Kind: "agent", Seat: "build", Access: "write"}},
		Exit:   &WorkflowExit{EvaluatorSlot: "eval", RequiresNodes: []string{"eval"}},
	}
	if err := ValidateEvaluator(writeEval); err == nil || !strings.Contains(err.Error(), "workflow-evaluator-access") {
		t.Fatalf("expected workflow-evaluator-access, got %v", err)
	}

	notRequired := &Workflow{
		Stages: []WorkflowStage{
			{ID: "work", Kind: "agent", Seat: "build", Access: "write"},
			{ID: "eval", Kind: "agent", Seat: "verify", Access: "read_only"},
		},
		Edges: []WorkflowEdge{{From: "work", To: "eval"}},
		Exit:  &WorkflowExit{EvaluatorSlot: "eval", RequiresNodes: []string{"work"}},
	}
	if err := ValidateEvaluator(notRequired); err == nil || !strings.Contains(err.Error(), "workflow-evaluator-required") {
		t.Fatalf("expected workflow-evaluator-required, got %v", err)
	}

	wrongOrder := &Workflow{
		Stages: []WorkflowStage{
			{ID: "eval", Kind: "agent", Seat: "verify", Access: "read_only"},
			{ID: "work", Kind: "agent", Seat: "build", Access: "write"},
		},
		Edges: []WorkflowEdge{{From: "eval", To: "work"}},
		Exit:  &WorkflowExit{EvaluatorSlot: "eval", RequiresNodes: []string{"eval", "work"}},
	}
	if err := ValidateEvaluator(wrongOrder); err == nil || !strings.Contains(err.Error(), "workflow-evaluator-order") {
		t.Fatalf("expected workflow-evaluator-order, got %v", err)
	}

	failOnlyExit := &Workflow{
		Stages: []WorkflowStage{
			{ID: "first", Kind: "agent", Seat: "scout", Access: "read_only"},
			{ID: "second", Kind: "agent", Seat: "build", Access: "write"},
		},
		Edges: []WorkflowEdge{{From: "first", To: "second", WhenOutcome: "fail"}},
		Exit:  &WorkflowExit{RequiresNodes: []string{"first", "second"}},
	}
	if err := ValidateWorkflowExit(failOnlyExit); err == nil || !strings.Contains(err.Error(), "workflow-exit") {
		t.Fatalf("expected workflow-exit, got %v", err)
	}
}
