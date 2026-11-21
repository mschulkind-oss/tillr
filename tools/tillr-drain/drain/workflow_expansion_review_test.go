package drain

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

type crossLaneWorkflowSteps struct{ *workflowSteps }

func (s *crossLaneWorkflowSteps) Land(_ context.Context, c Candidate) (LandOutcome, error) {
	if c.Worktree != s.fork {
		s.t.Errorf("fork target %s", c.Worktree)
	}
	return LandOutcome{Result: LandLanded}, nil
}

func TestWorkflowExpansionForkScopeFollowsSelectedLane(t *testing.T) {
	f, l, old := workflowLoopFixture(t, "")
	l.cfg.Lanes = []string{"lane-2"}
	fork := filepath.Join(f.root, ".forks/lanes/lane-2/pi")
	if err := os.MkdirAll(fork, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, fork, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(fork, "base.txt"), "fork base\n")
	runGit(t, fork, "add", ".")
	runGit(t, fork, "commit", "-qm", "fork base")
	writeForkFile(t, filepath.Join(f.root, ".gitignore"), ".forks/\n")
	runGit(t, f.root, "add", ".gitignore")
	runGit(t, f.root, "commit", "-qm", "ignore fork")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='item-b'`); err != nil {
			return err
		}
		return appendEffectOn(tx, deliveryDecisionKind, "item", "item-a", deliveryDecision{Kind: "fork", Fork: "pi"}, l.cfg.Now())
	})
	old.fork = fork
	l.cfg.Steps = &crossLaneWorkflowSteps{old}
	runGit(t, f.root, "fetch", "--no-tags", fork, "refs/heads/main:refs/heads/patch/pi")
	result := &LoopResult{}
	for i := 0; i < 8 && result.Landed == 0; i++ {
		graphPass(t, l, result)
	}
	var outcome string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return tx.QueryRow(`SELECT outcome FROM node_runs WHERE item_id='item-a' AND node_id='work'`).Scan(&outcome)
	})
	if len(old.runs) != 2 || old.gates != 1 || result.Landed != 1 {
		t.Fatalf("scope refused cross-lane workflow: runs %d gates %d landed %d outcome %s", len(old.runs), old.gates, result.Landed, outcome)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var outcome string
		if err := tx.QueryRow(`SELECT outcome FROM node_runs WHERE item_id='item-a' AND node_id='work'`).Scan(&outcome); err != nil {
			return err
		}
		if outcome != "passed" {
			t.Fatalf("work %s", outcome)
		}
		return nil
	})
}

func TestWorkflowExpansionRejectsTrailingJSON(t *testing.T) {
	for _, tail := range []string{`{}`, `garbage`} {
		f, l, _ := workflowLoopFixture(t, "")
		writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), workflowFixture+tail)
		if _, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy); err == nil || !strings.Contains(err.Error(), "workflow-json") {
			t.Fatalf("trailing %s: %v", tail, err)
		}
	}
}

func TestWorkflowScopeEquivalentDirectorySpellings(t *testing.T) {
	for _, pair := range [][2]string{{"tools/item-a", "tools/item-a/"}, {"tools/item-a/", "tools/item-a"}} {
		if !scopeWithin([]string{pair[0]}, []string{pair[1]}) {
			t.Fatalf("scope %v", pair)
		}
	}
}

func TestWorkflowScopeParentTraversalCannotEscapeContainer(t *testing.T) {
	if scopeWithin([]string{"src/../outside/"}, []string{"src/"}) {
		t.Fatal("inner scope escaped its container through parent traversal")
	}
	f, l, _ := workflowLoopFixture(t, "")
	expansionFile(t, f.root, "inner", `"stages":[{"id":"work","kind":"agent","seat":"build","access":"write","write_scope":["tools/item-a/../elsewhere/"]}],"edges":[]`)
	expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"middle","kind":"workflow","workflow":"inner@v1","write_scope":["tools/item-a/"]}],"edges":[]`)
	if _, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy); err == nil || !strings.Contains(err.Error(), "workflow-write-scope") {
		t.Fatalf("container scope escape was not refused: %v", err)
	}
}

func TestWorkflowCheckParameterizedScopeConstraint(t *testing.T) {
	f, _, _ := workflowLoopFixture(t, "")
	expansionFile(t, f.root, "inner", `"args":{"paths":{"type":"paths"}},"stages":[{"id":"write","kind":"agent","seat":"build","access":"write","write_scope":"${args.paths}"}],"edges":[]`)
	expansionFile(t, f.root, "wrapper", `"args":{"paths":{"type":"paths"}},"stages":[{"id":"nested","kind":"workflow","workflow":"inner@v1","args":{"paths":"${args.paths}"},"write_scope":["tools/"]}],"edges":[]`)
	var out bytes.Buffer
	if !CheckWorkflows(f.root, filepath.Join(f.root, "config/tillr/workflows/wrapper@v1.json"), &out) {
		t.Fatal(out.String())
	}
	for _, path := range []string{"tools/item-a/", "config/", workflowCheckPathPrefix + "paths/"} {
		expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"wrapper","kind":"workflow","workflow":"wrapper@v1","args":{"paths":["`+path+`"]}}],"edges":[]`)
		_, err := loadWorkflow(f.root, "scout-then-build@v1", nil)
		out.Reset()
		checked := CheckWorkflows(f.root, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), &out)
		if checked != (path == "tools/item-a/") {
			t.Fatalf("concrete scope checker %s: %s", path, out.String())
		}
		if path == "tools/item-a/" && err != nil || path != "tools/item-a/" && (err == nil || !strings.Contains(err.Error(), "workflow-write-scope")) {
			t.Fatalf("runtime scope %s: %v", path, err)
		}
	}
}

func TestWorkflowExpansionBarrierGatesEveryDownstreamEntry(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	expansionFile(t, f.root, "source", `"stages":[{"id":"passed","kind":"agent","seat":"scout","access":"read_only","outputs":{"result":"markdown"}},{"id":"required","kind":"agent","seat":"scout","access":"read_only"}],"edges":[{"from_node":"passed","to_node":"required","when_outcome":"pass"}],"exit":{"requires_nodes":["passed","required"],"outputs":{"result":{"node":"passed","output":"result"}}}`)
	expansionFile(t, f.root, "sink", `"inputs":{"result":"markdown"},"stages":[{"id":"entry","kind":"agent","seat":"scout","access":"read_only"},{"id":"reader","kind":"agent","seat":"build","access":"write","inputs":{"result":"markdown"}}],"edges":[{"from_node":"entry","to_node":"reader"}]`)
	expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"source","kind":"workflow","workflow":"source@v1","outputs":{"result":"markdown"}},{"id":"sink","kind":"workflow","workflow":"sink@v1","inputs":{"result":"markdown"}}],"edges":[{"from_node":"source","from_output":"result","to_node":"sink","to_input":"result"}]`)
	w, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy)
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES('item-a',1,'source/passed','run-source','passed'),('item-a',1,'source/required','run-required','running')`)
		return err
	})
	d := Decision{ItemID: "item-a", Attempt: 1, NodeIndex: 0, Workflow: w}
	index, err := l.nextWorkflowNode(context.Background(), d)
	if err != nil || index != -1 {
		t.Fatalf("downstream started before required exit: index %d err %v edges %+v", index, err, w.Edges)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE node_runs SET outcome='skipped' WHERE node_id='source/required'`)
		return err
	})
	index, err = l.nextWorkflowNode(context.Background(), d)
	if err != nil || index != -1 {
		t.Fatalf("downstream started with a skipped required exit: index %d err %v", index, err)
	}
}
