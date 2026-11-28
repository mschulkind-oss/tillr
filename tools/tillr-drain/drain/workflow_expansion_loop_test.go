package drain

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
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

// briefRecordingSteps keeps each run's brief, read as the run starts.
type briefRecordingSteps struct {
	*restartWorkflowSteps
	briefs []string
}

func (s *briefRecordingSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	brief, err := os.ReadFile(filepath.Join(s.f.laneRoot, req.LaneID, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	s.briefs = append(s.briefs, string(brief))
	return s.restartWorkflowSteps.Run(ctx, req)
}

// Each node's diff is checked against its own write_scope: node one (unscoped) passes, and
// node two, scoped to tools/item-a/other/, escapes on every run. Its first escape goes back
// to the builder, with the escaped path named; the returned run escapes again and, with no
// plan stage to return to, the item is held. Nothing is gated or landed.
func TestWorkflowExpansionScopeChecksEachNodeDiff(t *testing.T) {
	f, l, inner := restartGraphFixture(t)
	steps := &briefRecordingSteps{restartWorkflowSteps: inner}
	l.cfg.Steps = steps
	// The final worker's inline refusal frees the lane for other queued items.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
		return err
	})
	expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"one","kind":"agent","seat":"build","access":"write"},{"id":"two","kind":"agent","seat":"build","access":"write","write_scope":["tools/item-a/other/"]}],"edges":[{"from_node":"one","to_node":"two"}]`)
	runGit(t, f.root, "commit", "-qm", "scope graph")
	result, held := drivePieceScope(t, l)
	if held.Reason != ReasonBoundaryScopeEscape || steps.gates != 0 || result.Landed != 0 {
		t.Fatalf("held %+v gates %d landed %d", held, steps.gates, result.Landed)
	}
	var nodes []string
	for _, req := range steps.runs {
		nodes = append(nodes, req.NodeID)
	}
	if got := strings.Join(nodes, ","); got != "one,two,two" {
		t.Fatalf("nodes ran %s, want one,two,two", got)
	}
	// The first escape is a fence refusal naming the path node two wrote outside its scope.
	escaped := "tools/item-a/" + steps.runs[1].RunID + ".txt"
	refusals := pieceScopeRefusals(t, f)
	if len(refusals) == 0 || refusals[0].Step != RefusalFence || strings.Join(refusals[0].ScopeEscapes, ",") != escaped {
		t.Fatalf("refusals = %+v", refusals)
	}
	if returned := steps.briefs[2]; !strings.Contains(returned, escaped) || !strings.Contains(returned, "inside its write_scope") {
		t.Fatalf("returned brief does not name the escaped path %s:\n%s", escaped, returned)
	}
	// Both runs of node two were refused, and node one's run passed.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		rows, err := tx.Query(`SELECT node_id,outcome FROM node_runs WHERE item_id='item-a' ORDER BY run_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var node, outcome string
			if err := rows.Scan(&node, &outcome); err != nil {
				return err
			}
			got = append(got, node+"="+outcome)
		}
		if strings.Join(got, ",") != "one=passed,two=refused,two=refused" {
			t.Fatalf("node runs %v", got)
		}
		return rows.Err()
	})
}
