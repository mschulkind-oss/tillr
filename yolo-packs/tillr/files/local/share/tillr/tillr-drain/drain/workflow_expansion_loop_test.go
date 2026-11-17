package drain

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWorkflowExpansionFourRunsOneLandingAndFrozenInner(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	expansionFile(t, f.root, "inner", `"stages":[{"id":"one","kind":"agent","seat":"build","access":"write"},{"id":"two","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"one","to_node":"two"}]`)
	expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"before","kind":"agent","seat":"build","access":"write"},{"id":"middle","kind":"workflow","workflow":"inner@v1"},{"id":"after","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"before","to_node":"middle"},{"from_node":"middle","to_node":"after"}]`)
	runGit(t, f.root, "commit", "-qm", "nested graph")
	before, _ := strconv.Atoi(runGit(t, f.root, "rev-list", "--count", "HEAD"))
	result := &LoopResult{}
	graphPass(t, l, result)
	graphPass(t, l, result)
	w := pendingForTest(t, f, "item-a").Decision.Workflow
	innerPath := filepath.Join(f.root, "config/tillr/workflows/inner@v1.json")
	raw, err := os.ReadFile(innerPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	if w.Sources["inner@v1"] != digest || len(w.Sources) != 2 {
		t.Fatalf("sources %+v", w.Sources)
	}
	writeForkFile(t, innerPath, `{"name":"inner","version":1,"stages":[{"id":"changed","kind":"agent","seat":"build","access":"write"}],"edges":[]}`)
	next := replaceGraphLoop(t, f, l, steps)
	for i := 0; i < 10 && result.Landed == 0; i++ {
		graphPass(t, next, result)
	}
	if len(steps.runs) != 4 || result.Landed != 1 || steps.gates != 1 {
		t.Fatalf("runs %d landed %d gates %d", len(steps.runs), result.Landed, steps.gates)
	}
	for i, req := range steps.runs {
		if req.RunID != fmt.Sprintf("run-item-a-a1-n%d", i+1) {
			t.Fatalf("order %+v", steps.runs)
		}
	}
	for i, s := range w.Stages {
		want := []string{"before", "middle/one", "middle/two", "after"}[i]
		if s.ID != want || strings.HasPrefix(s.ID, "middle/") && s.Parent != "middle" {
			t.Fatalf("parent/order %+v", w.Stages)
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for i, id := range []string{"before", "middle/one", "middle/two", "after"} {
			var runID, outcome string
			if err := tx.QueryRow(`SELECT run_id,outcome FROM node_runs WHERE item_id='item-a' AND node_id=?`, id).Scan(&runID, &outcome); err != nil {
				return err
			}
			if runID != steps.runs[i].RunID || outcome != "passed" {
				t.Fatalf("node %s %s %s", id, runID, outcome)
			}
		}
		return nil
	})
	after, _ := strconv.Atoi(runGit(t, f.root, "rev-list", "--count", "HEAD"))
	if after != before+1 {
		t.Fatalf("commits %d -> %d", before, after)
	}
}

func TestWorkflowExpansionScopeRefusedBeforeRun(t *testing.T) {
	f, l, steps := workflowLoopFixture(t, "")
	expansionFile(t, f.root, "inner", `"stages":[{"id":"work","kind":"agent","seat":"build","access":"write","write_scope":["tools/elsewhere/"]}],"edges":[]`)
	expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"middle","kind":"workflow","workflow":"inner@v1","write_scope":["tools/item-a/"]}],"edges":[]`)
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-a" {
			if p.decision.Action != ActionHold || !strings.Contains(p.decision.Reason, "workflow-write-scope") {
				t.Fatalf("scope %+v", p.decision)
			}
		}
	}
	if len(steps.runs) != 0 {
		t.Fatal("escaped scope started")
	}
}

func TestWorkflowExpansionNarrowFinalScopeDoesNotConvictEarlierNodes(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"one","kind":"agent","seat":"build","access":"write"},{"id":"two","kind":"agent","seat":"build","access":"write","write_scope":["tools/item-a/run-item-a-a1-n2.txt"]}],"edges":[{"from_node":"one","to_node":"two"}]`)
	runGit(t, f.root, "commit", "-qm", "scope graph")
	result := &LoopResult{}
	for i := 0; i < 7 && result.Landed == 0; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 2 || steps.gates != 1 || result.Landed != 1 {
		t.Fatalf("runs %d gates %d landed %d", len(steps.runs), steps.gates, result.Landed)
	}
}

func TestWorkflowExpansionScopeChecksEachNodeDiff(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"one","kind":"agent","seat":"build","access":"write"},{"id":"two","kind":"agent","seat":"build","access":"write","write_scope":["tools/item-a/other/"]}],"edges":[{"from_node":"one","to_node":"two"}]`)
	runGit(t, f.root, "commit", "-qm", "scope graph")
	result := &LoopResult{}
	for i := 0; i < 4; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 2 || steps.gates != 0 || result.Landed != 0 {
		t.Fatalf("runs %d gates %d landed %d", len(steps.runs), steps.gates, result.Landed)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var outcome string
		if err := tx.QueryRow(`SELECT outcome FROM node_runs WHERE node_id='two'`).Scan(&outcome); err != nil {
			return err
		}
		if outcome != "refused" {
			t.Fatalf("scope outcome %s", outcome)
		}
		return nil
	})
}
