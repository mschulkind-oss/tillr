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

func TestDecodeWorkflowJoin(t *testing.T) {
	valid := `{"name":"parallel","version":1,"stages":[
		{"id":"a","kind":"agent","seat":"build","access":"write","write_scope":["docs/"]},
		{"id":"b","kind":"agent","seat":"build","access":"write","write_scope":["tests/"]},
		{"id":"join","kind":"join","access":"write"}
	],"edges":[{"from_node":"a","to_node":"join"},{"from_node":"b","to_node":"join"}]}`
	w, err := DecodeWorkflow([]byte(valid), "parallel@v1")
	if err != nil {
		t.Fatalf("DecodeWorkflow valid join failed: %v", err)
	}
	if len(w.Stages) != 3 || w.Stages[2].Kind != "join" || w.Stages[2].Access != "write" {
		t.Fatalf("unexpected stages: %+v", w.Stages)
	}

	badSeat := `{"name":"bad","version":1,"stages":[{"id":"join","kind":"join","seat":"build","access":"write"}],"edges":[]}`
	if _, err := DecodeWorkflow([]byte(badSeat), "bad@v1"); err == nil || !strings.Contains(err.Error(), "workflow-kind") {
		t.Fatalf("expected workflow-kind for join with seat, got %v", err)
	}
	badAccess := `{"name":"bad","version":1,"stages":[{"id":"join","kind":"join","access":"read_only"}],"edges":[]}`
	if _, err := DecodeWorkflow([]byte(badAccess), "bad@v1"); err == nil || !strings.Contains(err.Error(), "workflow-kind") {
		t.Fatalf("expected workflow-kind for read_only join, got %v", err)
	}
}

func TestWorkflowPieceAfterAndJoinSynthesis(t *testing.T) {
	root := t.TempDir()
	wfDir := filepath.Join(root, "config", "tillr", "workflows")
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		t.Fatal(err)
	}
	bpPath := filepath.Join(wfDir, "build-pieces@v1.json")
	bpJSON := `{
		"name": "build-pieces",
		"version": 1,
		"args": { "pieces": { "type": "pieces", "most": 12 } },
		"stages": [{ "id": "piece", "kind": "agent", "seat": "build", "access": "read_only", "repeat": "pieces" }],
		"edges": [],
		"exit": { "requires_nodes": ["piece"], "requires_candidate": "commit_required" }
	}`
	if err := os.WriteFile(bpPath, []byte(bpJSON), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("omitted after stays sequential with no join", func(t *testing.T) {
		args := map[string]any{
			"pieces": []any{
				map[string]any{"id": "p1", "seat": "build", "access": "write", "write_scope": []string{"docs/"}, "prompt": "one"},
				map[string]any{"id": "p2", "seat": "build", "access": "write", "write_scope": []string{"tests/"}, "prompt": "two"},
			},
		}
		w, err := ExpandWorkflow(root, bpPath, nil, map[string]bool{}, args, nil)
		if err != nil {
			t.Fatalf("ExpandWorkflow failed: %v", err)
		}
		if len(w.Stages) != 2 {
			t.Fatalf("expected 2 stages (no join), got %d: %+v", len(w.Stages), w.Stages)
		}
		if len(w.Edges) != 1 || w.Edges[0].From != "piece-p1" || w.Edges[0].To != "piece-p2" {
			t.Fatalf("expected sequential edge piece-p1 -> piece-p2, got %+v", w.Edges)
		}
	})

	t.Run("explicit after empty fan-out synthesizes join", func(t *testing.T) {
		args := map[string]any{
			"pieces": []any{
				map[string]any{"id": "p1", "seat": "build", "access": "write", "after": []string{}, "write_scope": []string{"docs/"}, "prompt": "one"},
				map[string]any{"id": "p2", "seat": "build", "access": "write", "after": []string{}, "write_scope": []string{"tests/"}, "prompt": "two"},
				map[string]any{"id": "p3", "seat": "build", "access": "write", "after": []string{"p1", "p2"}, "write_scope": []string{"README.md"}, "prompt": "three"},
			},
		}
		w, err := ExpandWorkflow(root, bpPath, nil, map[string]bool{}, args, nil)
		if err != nil {
			t.Fatalf("ExpandWorkflow failed: %v", err)
		}
		var joinID string
		for _, s := range w.Stages {
			if s.Kind == "join" {
				joinID = s.ID
			}
		}
		if joinID == "" {
			t.Fatalf("expected synthesized join stage, got %+v", w.Stages)
		}
		edgeSet := map[string]bool{}
		for _, e := range w.Edges {
			edgeSet[e.From+"->"+e.To] = true
		}
		for _, want := range []string{"piece-p1->" + joinID, "piece-p2->" + joinID, joinID + "->piece-p3"} {
			if !edgeSet[want] {
				t.Fatalf("missing edge %s in %+v", want, w.Edges)
			}
		}
		if !reflect.DeepEqual(w.Exit.RequiresNodes, []string{"piece-p3"}) {
			t.Fatalf("expected requires_nodes [piece-p3], got %v", w.Exit.RequiresNodes)
		}
	})

	t.Run("cycle in after is rejected", func(t *testing.T) {
		args := map[string]any{
			"pieces": []any{
				map[string]any{"id": "p1", "seat": "build", "access": "write", "after": []string{"p2"}, "write_scope": []string{"docs/"}, "prompt": "one"},
				map[string]any{"id": "p2", "seat": "build", "access": "write", "after": []string{"p1"}, "write_scope": []string{"tests/"}, "prompt": "two"},
			},
		}
		if _, err := ExpandWorkflow(root, bpPath, nil, map[string]bool{}, args, nil); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("expected cycle error, got %v", err)
		}
	})
}

func TestConcurrentWriterAndJoinRespectFailAndExclusiveBranches(t *testing.T) {
	t.Run("fail edge orders repair after edit", func(t *testing.T) {
		w := &Workflow{
			Stages: []WorkflowStage{
				{ID: "edit", Kind: "agent", Seat: "build", Access: "write"},
				{ID: "check", Kind: "agent", Seat: "build", Access: "read_only"},
				{ID: "repair", Kind: "agent", Seat: "build", Access: "write"},
				{ID: "check-again", Kind: "agent", Seat: "build", Access: "read_only"},
			},
			Edges: []WorkflowEdge{
				{From: "edit", To: "check"},
				{From: "check", To: "repair", WhenOutcome: "fail"},
				{From: "repair", To: "check-again"},
			},
			Exit: &WorkflowExit{RequiresNodes: []string{"check"}},
		}
		if HasConcurrentWriter(w, "edit") || HasConcurrentWriter(w, "repair") {
			t.Fatalf("expected edit and repair not to be concurrent")
		}
		SynthesizeJoinStages(w)
		for _, s := range w.Stages {
			if s.Kind == "join" {
				t.Fatalf("unexpected synthesized join: %+v", w.Stages)
			}
		}
		if err := ValidateWorkflowExit(w); err != nil {
			t.Fatalf("ValidateWorkflowExit failed: %v", err)
		}
	})

	t.Run("mutually exclusive pass and fail branches are not concurrent", func(t *testing.T) {
		w := &Workflow{
			Stages: []WorkflowStage{
				{ID: "work", Kind: "agent", Seat: "build", Access: "write"},
				{ID: "check", Kind: "agent", Seat: "build", Access: "read_only"},
				{ID: "s1", Kind: "agent", Seat: "build", Access: "read_only"},
				{ID: "success", Kind: "agent", Seat: "build", Access: "write"},
				{ID: "r1", Kind: "agent", Seat: "build", Access: "read_only"},
				{ID: "recover", Kind: "agent", Seat: "build", Access: "write"},
			},
			Edges: []WorkflowEdge{
				{From: "work", To: "check"},
				{From: "check", To: "s1", WhenOutcome: "pass"},
				{From: "s1", To: "success"},
				{From: "check", To: "r1", WhenOutcome: "fail"},
				{From: "r1", To: "recover"},
			},
		}
		for _, id := range []string{"work", "success", "recover"} {
			if HasConcurrentWriter(w, id) {
				t.Fatalf("expected %s not to have a concurrent writer", id)
			}
		}
		SynthesizeJoinStages(w)
		for _, s := range w.Stages {
			if s.Kind == "join" {
				t.Fatalf("unexpected synthesized join: %+v", w.Stages)
			}
		}
	})

	t.Run("spliced join reconcile is ordered after parallel writers", func(t *testing.T) {
		w := &Workflow{
			Stages: []WorkflowStage{
				{ID: "a", Kind: "agent", Seat: "build", Access: "write"},
				{ID: "b", Kind: "agent", Seat: "build", Access: "write"},
				{ID: "join", Kind: "join", Access: "write"},
			},
			Edges: []WorkflowEdge{
				{From: "a", To: "join"},
				{From: "b", To: "join"},
			},
		}
		spliced, recID := SpliceReconcileStage(w, "join", "reconcile", WorkflowScope{})
		if !HasConcurrentWriter(spliced, "a") || !HasConcurrentWriter(spliced, "b") {
			t.Fatalf("expected a and b to remain concurrent writers")
		}
		if HasConcurrentWriter(spliced, recID) {
			t.Fatalf("expected %s not to be a concurrent writer", recID)
		}
	})

	t.Run("parallel writers on the same outcome branch remain concurrent", func(t *testing.T) {
		w := &Workflow{
			Stages: []WorkflowStage{
				{ID: "check", Kind: "agent", Seat: "build", Access: "read_only"},
				{ID: "w1", Kind: "agent", Seat: "build", Access: "write"},
				{ID: "w2", Kind: "agent", Seat: "build", Access: "write"},
			},
			Edges: []WorkflowEdge{
				{From: "check", To: "w1", WhenOutcome: "pass"},
				{From: "check", To: "w2", WhenOutcome: "pass"},
			},
		}
		if !HasConcurrentWriter(w, "w1") || !HasConcurrentWriter(w, "w2") {
			t.Fatalf("expected w1 and w2 to be concurrent writers")
		}
		SynthesizeJoinStages(w)
		foundJoin := false
		for _, s := range w.Stages {
			if s.Kind == "join" {
				foundJoin = true
			}
		}
		if !foundJoin {
			t.Fatalf("expected synthesized join for w1 and w2, got %+v", w.Stages)
		}
	})
}
