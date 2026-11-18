package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

const proposalWorkflow = `{"name":"scout-then-build","version":1,"stages":[{"id":"one","kind":"agent","seat":"scout","access":"read_only","outputs":{"findings":"markdown"}},{"id":"two","kind":"agent","seat":"scout","access":"read_only","may_replace":{"three":["replacement@v1"]},"outputs":{"proposal":"graph.proposal","findings":"markdown"}},{"id":"three","kind":"agent","seat":"scout","access":"read_only"},{"id":"four","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"one","to_node":"two"},{"from_node":"two","to_node":"three"},{"from_node":"three","to_node":"four"}]}`
const replacementBody = `"stages":[{"id":"scout","kind":"agent","seat":"scout","access":"read_only"},{"id":"join","kind":"agent","seat":"scout","access":"read_only"}],"edges":[{"from_node":"scout","to_node":"join"}]`

func TestProposalStageDeclaration(t *testing.T) {
	if _, err := decodeWorkflow([]byte(proposalWorkflow), "scout-then-build@v1"); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(map[string]any){
		func(w map[string]any) {
			w["stages"].([]any)[1].(map[string]any)["may_replace"] = map[string]any{"one": []string{"replacement@v1"}}
		},
		func(w map[string]any) {
			w["stages"].([]any)[1].(map[string]any)["outputs"] = map[string]string{"a": "graph.proposal", "b": "graph.proposal"}
		},
	} {
		var w map[string]any
		json.Unmarshal([]byte(proposalWorkflow), &w)
		edit(w)
		raw, _ := json.Marshal(w)
		if _, err := decodeWorkflow(raw, "scout-then-build@v1"); err == nil || !strings.Contains(err.Error(), "workflow-proposal") {
			t.Fatalf("invalid declaration: %v", err)
		}
	}
}

func proposalFixture(t *testing.T) (refusalLoopFixture, *Loop, Decision) {
	t.Helper()
	f, l, _ := workflowLoopFixture(t, "")
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), proposalWorkflow)
	expansionFile(t, f.root, "replacement", replacementBody)
	w, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy)
	if err != nil {
		t.Fatal(err)
	}
	d := Decision{ItemID: "item-a", LaneID: "lane-1", Attempt: 1, NodeIndex: 1, Workflow: w, RunRequest: &run.Request{RunID: "proposal-run"}}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES('item-a',1,'one','first-run','passed'),('item-a',1,'two','proposal-run','running')`)
		return err
	})
	return f, l, d
}

func proposalArtifact(t *testing.T, root, runID, raw string) nodeArtifact {
	t.Helper()
	path := filepath.Join(nodeOutputsDir(root, runID), "proposal")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, path, raw)
	return nodeArtifact{Name: "proposal", Type: "graph.proposal", SHA256: "test", Location: path}
}

func TestProposalTransactionRules(t *testing.T) {
	for _, tc := range []struct {
		name, rule string
		change     func(*testing.T, refusalLoopFixture, *Decision)
		raw        string
	}{
		{name: "accepted"},
		{name: "self", rule: "proposal-target-order", raw: `{"target":"two","workflow":"replacement@v1"}`},
		{name: "earlier", rule: "proposal-target-order", raw: `{"target":"one","workflow":"replacement@v1"}`},
		{name: "started", rule: "proposal-target-started", change: func(t *testing.T, f refusalLoopFixture, d *Decision) {
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES('item-a',1,'three','target-run','running')`)
				return err
			})
		}},
		{name: "expanded started", rule: "proposal-target-started", change: func(t *testing.T, f refusalLoopFixture, d *Decision) {
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES('item-a',1,'three/child','target-run','passed')`)
				return err
			})
		}},
		{name: "not allowed", rule: "proposal-workflow-not-allowed", raw: `{"target":"three","workflow":"other@v1"}`},
		{name: "untracked", rule: "proposal-workflow-untracked", change: func(t *testing.T, f refusalLoopFixture, d *Decision) {
			runGit(t, f.root, "reset", "--", "config/tillr/workflows/replacement@v1.json")
		}},
		{name: "bounds", rule: "proposal-argument-bounds", raw: `{"target":"three","workflow":"replacement@v1","args":{"prompts":["a","b"]}}`, change: func(t *testing.T, f refusalLoopFixture, d *Decision) {
			expansionFile(t, f.root, "replacement", `"args":{"prompts":{"type":"strings","most":1}},`+replacementBody)
		}},
		{name: "scope", rule: "proposal-scope-outside-fence", change: func(t *testing.T, f refusalLoopFixture, d *Decision) {
			expansionFile(t, f.root, "replacement", `"stages":[{"id":"write","kind":"agent","seat":"build","access":"write","write_scope":["outside/"]}],"edges":[]`)
		}},
		{name: "promise", rule: "proposal-output-promise", change: func(t *testing.T, f refusalLoopFixture, d *Decision) {
			d.Workflow.Stages[2].Outputs = map[string]string{"result": "markdown"}
			d.Workflow.Stages[3].Inputs = map[string]string{"result": "markdown"}
			d.Workflow.Edges[2].FromOutput = "result"
			d.Workflow.Edges[2].ToInput = "result"
		}},
		{name: "malformed", rule: "proposal-json", raw: `{"target":"three","target":"four","workflow":"replacement@v1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, d := proposalFixture(t)
			if tc.change != nil {
				tc.change(t, f, &d)
			}
			before, _ := json.Marshal(d.Workflow)
			raw := tc.raw
			if raw == "" {
				raw = `{"target":"three","workflow":"replacement@v1"}`
			}
			artifact := proposalArtifact(t, f.root, d.RunRequest.RunID, raw)
			if err := l.acceptNodeSettlement(context.Background(), &d, []nodeArtifact{artifact}); err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var rule, old, after, outcome string
				var oldN, newN int
				if err := tx.QueryRow(`SELECT rule,before_graph,after_graph,before_nodes,after_nodes FROM graph_revisions WHERE proposing_run='proposal-run'`).Scan(&rule, &old, &after, &oldN, &newN); err != nil {
					return err
				}
				if rule != tc.rule || old != string(before) || oldN != 4 {
					t.Fatalf("revision: rule=%s nodes=%d->%d", rule, oldN, newN)
				}
				if tc.rule != "" {
					current, _ := json.Marshal(d.Workflow)
					if after != old || string(current) != old || newN != 4 {
						t.Fatal("refusal changed graph")
					}
				} else if newN != 5 || d.Workflow.Stages[2].ID != "three/scout" || d.Workflow.Stages[2].Parent != "three" {
					t.Fatalf("graph: %+v", d.Workflow)
				}
				if err := tx.QueryRow(`SELECT outcome FROM node_runs WHERE run_id='proposal-run'`).Scan(&outcome); err != nil {
					return err
				}
				if outcome != "passed" {
					t.Fatalf("refusal failed proposer: %s", outcome)
				}
				return nil
			})
			// Settlement replay cannot add a revision or rewire the graph twice.
			if err := l.acceptNodeSettlement(context.Background(), &d, []nodeArtifact{artifact}); err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var n int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM graph_revisions`).Scan(&n); err != nil {
					return err
				}
				if n != 1 {
					t.Fatalf("replay added revision %d", n)
				}
				return nil
			})
		})
	}
}

func TestProposalAppendOnlyAndNoProposal(t *testing.T) {
	f, l, d := proposalFixture(t)
	if err := l.acceptNodeSettlement(context.Background(), &d, nil); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM graph_revisions`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatal("no proposal changed graph")
		}
		return nil
	})
	for i := 0; i < 2; i++ {
		if i == 1 {
			d.NodeIndex = 0
			d.Workflow.Stages[0].MayReplace = map[string][]string{"three": {"replacement@v1"}}
		}
		d.RunRequest = &run.Request{RunID: fmt.Sprintf("refused-%d", i)}
		a := proposalArtifact(t, f.root, d.RunRequest.RunID, `{"target":"one","workflow":"replacement@v1"}`)
		if err := l.acceptNodeSettlement(context.Background(), &d, []nodeArtifact{a}); err != nil {
			t.Fatal(err)
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM graph_revisions`).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			t.Fatalf("second proposal: %d rows", n)
		}
		for _, query := range []string{`UPDATE graph_revisions SET rule='changed'`, `DELETE FROM graph_revisions`} {
			if _, err := tx.Exec(query); err == nil {
				t.Fatalf("allowed mutation: %s", query)
			}
		}
		return nil
	})
}

func TestProposalOptionalAndNotAllowed(t *testing.T) {
	f, _, d := proposalFixture(t)
	// A missing proposal file is an intentional no-op, not a missing artifact.
	d.Workflow.Stages[1].Outputs = map[string]string{"proposal": "graph.proposal"}
	if _, err := checkNodeOutputs(f.root, d); err != nil {
		t.Fatal(err)
	}
	proposalArtifact(t, f.root, d.RunRequest.RunID, `{}`)
	d.Workflow.Stages[1].MayReplace = nil
	if _, err := checkNodeOutputs(f.root, d); err == nil || !strings.Contains(err.Error(), "proposal-not-allowed") {
		t.Fatalf("unauthorized output: %v", err)
	}
}

type proposalSteps struct {
	*workflowSteps
	mode  string
	nodes []string
}

func (s *proposalSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	p := pendingForTest(s.t, s.f, "item-a")
	node := p.Decision.Workflow.Stages[p.Decision.NodeIndex]
	s.nodes = append(s.nodes, node.ID)
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if len(s.runs) == 1 {
		s.firstHead = runGit(s.t, target, "rev-parse", "HEAD")
	}
	for name, typ := range node.Outputs {
		if typ == "graph.proposal" {
			if s.mode == "none" {
				continue
			}
			raw := `{"target":"three","workflow":"replacement@v1"}`
			if s.mode == "refused" {
				raw = `{"target":"three","workflow":"not-allowed@v1"}`
			}
			if s.mode == "not allowed" {
				raw = `{}`
			}
			proposalArtifact(s.t, s.f.root, req.RunID, raw)
		} else {
			writeForkFile(s.t, filepath.Join(nodeOutputsDir(s.f.root, req.RunID), name), "immutable findings "+node.ID)
		}
	}
	if node.Access == "write" {
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/result.txt"), "built\n")
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "build")
	}
	report := "RESULT: done\n"
	path := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	writeForkFile(s.t, path, report)
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func TestProposalLoopOrderCompletedOutputsAndRestart(t *testing.T) {
	for _, mode := range []string{"accepted", "none", "refused", "not allowed"} {
		t.Run(mode, func(t *testing.T) {
			f, l, base := workflowLoopFixture(t, "")
			raw := proposalWorkflow
			if mode == "not allowed" {
				raw = strings.Replace(raw, `"may_replace":{"three":["replacement@v1"]},`, "", 1)
			}
			writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), raw)
			expansionFile(t, f.root, "replacement", replacementBody)
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "proposal fixture")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state='done' WHERE item_id='item-b'`)
				return err
			})
			steps := &proposalSteps{workflowSteps: base, mode: mode}
			l.cfg.Steps = steps
			result := &LoopResult{}
			for i := 0; i < 20; i++ {
				graphPass(t, l, result)
				if mode == "accepted" && len(steps.runs) == 2 {
					// The accepted snapshot must survive source edits and loop reconstruction.
					if p := pendingForTest(t, f, "item-a"); p.Decision.Workflow.Revision == 1 {
						writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/replacement@v1.json"), `broken source`)
						next := f.loop(t, steps, l.cfg.Stage)
						next.cfg = l.cfg
						l = next
					}
				}
				if result.Landed > 0 || mode == "not allowed" && len(steps.runs) == 2 && pendingForTest(t, f, "item-a").Done {
					break
				}
			}
			want := []string{"one", "two", "three", "four"}
			if mode == "accepted" {
				want = []string{"one", "two", "three/scout", "three/join", "four"}
			}
			if mode == "not allowed" {
				want = []string{"one", "two"}
			}
			if !reflect.DeepEqual(steps.nodes, want) {
				t.Fatalf("order %v want %v", steps.nodes, want)
			}
			if mode != "not allowed" && result.Landed != 1 {
				t.Fatalf("refusal did not continue: %+v", result)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				for _, id := range []string{"one", "two"} {
					var n int
					if err := tx.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE node_id=?`, id).Scan(&n); err != nil {
						return err
					}
					if n != 1 {
						t.Fatalf("reran %s: %d", id, n)
					}
				}
				var n int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM graph_revisions`).Scan(&n); err != nil {
					return err
				}
				expected := 1
				if mode == "none" || mode == "not allowed" {
					expected = 0
				}
				if n != expected {
					t.Fatalf("revision count %d", n)
				}
				if mode == "not allowed" {
					var outcome string
					if err := tx.QueryRow(`SELECT outcome FROM node_runs WHERE node_id='two'`).Scan(&outcome); err != nil {
						return err
					}
					if outcome != "failed" {
						t.Fatal(outcome)
					}
					return nil
				}
				for _, id := range []string{"one", "two"} {
					var location string
					if err := tx.QueryRow(`SELECT location FROM node_artifacts WHERE node_id=? AND name='findings'`, id).Scan(&location); err != nil {
						return err
					}
					raw, err := os.ReadFile(location)
					if err != nil {
						return err
					}
					if string(raw) != "immutable findings "+id {
						t.Fatalf("changed output %s", raw)
					}
				}
				return nil
			})
		})
	}
}

func TestProposalSecondAcceptedRevisionAndPorts(t *testing.T) {
	f, l, d := proposalFixture(t)
	d.Workflow.Stages[0].MayReplace = map[string][]string{"three": {"replacement@v1"}}
	d.NodeIndex = 0
	d.RunRequest = &run.Request{RunID: "first-run"}
	for i := 0; i < 2; i++ {
		a := proposalArtifact(t, f.root, d.RunRequest.RunID, `{"target":"three","workflow":"replacement@v1"}`)
		if err := l.acceptNodeSettlement(context.Background(), &d, []nodeArtifact{a}); err != nil {
			t.Fatal(err)
		}
		if d.Workflow.Revision != i+1 {
			t.Fatalf("revision %d", d.Workflow.Revision)
		}
		d.NodeIndex = 1
		d.RunRequest = &run.Request{RunID: "proposal-run"}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM graph_revisions WHERE rule=''`).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			t.Fatalf("accepted rows %d", n)
		}
		return nil
	})
	// Static expansion retains the target interface; replacement must preserve both
	// its incoming input and its promised outgoing file, even after re-expansion.
	expansionFile(t, f.root, "ported", `"inputs":{"source":"markdown"},"stages":[{"id":"read","kind":"agent","seat":"scout","access":"read_only","inputs":{"source":"markdown"}},{"id":"produce","kind":"agent","seat":"scout","access":"read_only","outputs":{"result":"markdown"}}],"edges":[{"from_node":"read","to_node":"produce"}],"exit":{"requires_nodes":["read","produce"],"outputs":{"result":{"node":"produce","output":"result"}}}`)
	expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"open","kind":"agent","seat":"scout","access":"read_only","outputs":{"source":"markdown"}},{"id":"write","kind":"workflow","workflow":"ported@v1","inputs":{"source":"markdown"},"outputs":{"result":"markdown"}},{"id":"end","kind":"agent","seat":"scout","access":"read_only","inputs":{"result":"markdown"}}],"edges":[{"from_node":"open","from_output":"source","to_node":"write","to_input":"source"},{"from_node":"write","from_output":"result","to_node":"end","to_input":"result"}]`)
	w, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := expandWorkflow(f.root, filepath.Join(f.root, "config/tillr/workflows/ported@v1.json"), l.cfg.Policy, map[string]bool{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	next, err := replaceProposalGraph(w, "write", inner)
	if err != nil {
		t.Fatal(err)
	}
	seenIn, seenOut := false, false
	for _, e := range next.Edges {
		if e.From == "open" && e.To == "write/read" && e.ToInput == "source" {
			seenIn = true
		}
		if e.From == "write/produce" && e.To == "end" && e.FromOutput == "result" && len(e.RequiresPassed) == 2 {
			seenOut = true
		}
	}
	if !seenIn || !seenOut {
		t.Fatalf("lost boundary: %+v", next.Edges)
	}
	if next.Groups["write"].Outputs["result"].Node != "write/produce" {
		t.Fatal(next.Groups)
	}
}

func TestProposalKeepsEnclosingSiblingBarriers(t *testing.T) {
	f, l, d := proposalFixture(t)
	w := &Workflow{Stages: []WorkflowStage{
		{ID: "G/P", Kind: "agent", Access: "read_only"}, {ID: "G/X", Kind: "agent", Access: "read_only"}, {ID: "G/Y", Kind: "agent", Access: "read_only"}, {ID: "end", Kind: "agent", Access: "read_only"},
	}, Edges: []WorkflowEdge{
		{From: "G/P", To: "end", RequiresPassed: []string{"G/P", "G/X", "G/Y"}},
		{From: "G/X", To: "end", RequiresPassed: []string{"G/P", "G/X", "G/Y"}},
		{From: "G/Y", To: "end", RequiresPassed: []string{"G/P", "G/X", "G/Y"}},
	}, Exit: &WorkflowExit{RequiresNodes: []string{"end"}, RequiresCandidate: "no_commit"}}
	inner, err := loadWorkflow(f.root, "replacement@v1", l.cfg.Policy)
	if err != nil {
		t.Fatal(err)
	}
	next, err := replaceProposalGraph(w, "G/X", inner)
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range next.Edges {
		if edge.To != "end" {
			continue
		}
		required := map[string]bool{}
		for _, id := range edge.RequiresPassed {
			required[id] = true
		}
		for _, id := range []string{"G/P", "G/Y", "G/X/scout", "G/X/join"} {
			if !required[id] {
				t.Fatalf("lost sibling barrier %s on %+v", id, edge)
			}
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES('item-a',1,'G/P','gp','passed'),('item-a',1,'G/X/scout','gs','passed'),('item-a',1,'G/X/join','gj','passed'),('item-a',1,'G/Y','gy','handled-fail')`)
		return err
	})
	d.Workflow = next
	index, err := l.nextWorkflowNode(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if index >= 0 {
		t.Fatalf("ran downstream node after required sibling failed: %s", next.Stages[index].ID)
	}
}

func TestProposalExpandedInputFanout(t *testing.T) {
	f, l, _ := proposalFixture(t)
	expansionFile(t, f.root, "readers", `"inputs":{"source":"markdown"},"stages":[{"id":"a","kind":"agent","seat":"scout","access":"read_only","inputs":{"source":"markdown"}},{"id":"b","kind":"agent","seat":"scout","access":"read_only","inputs":{"source":"markdown"}}],"edges":[]`)
	expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"open","kind":"agent","seat":"scout","access":"read_only","outputs":{"source":"markdown"}},{"id":"G","kind":"workflow","workflow":"readers@v1","inputs":{"source":"markdown"}}],"edges":[{"from_node":"open","from_output":"source","to_node":"G","to_input":"source"}]`)
	w, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := expandWorkflow(f.root, filepath.Join(f.root, "config/tillr/workflows/readers@v1.json"), l.cfg.Policy, map[string]bool{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	next, err := replaceProposalGraph(w, "G", inner)
	if err != nil {
		t.Fatal(err)
	}
	fed := map[string]int{}
	for _, edge := range next.Edges {
		if edge.ToInput == "source" {
			fed[edge.To]++
		}
	}
	if fed["G/a"] != 1 || fed["G/b"] != 1 {
		t.Fatalf("duplicate fanout: %+v", next.Edges)
	}
}
