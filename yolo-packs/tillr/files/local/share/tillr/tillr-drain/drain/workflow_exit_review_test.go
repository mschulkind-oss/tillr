package drain

import (
	"path/filepath"
	"testing"
)

func TestWorkflowExitWallAfterEarlierCommitNeverAdmitsCandidate(t *testing.T) {
	raw := `{"name":"scout-then-build","version":1,"stages":[{"id":"edit","kind":"agent","seat":"build","access":"write"},{"id":"check","kind":"agent","seat":"build","access":"read_only"},{"id":"finish","kind":"agent","seat":"build","access":"read_only"}],"edges":[{"from_node":"edit","to_node":"check"},{"from_node":"check","to_node":"finish"}]}`
	f, l, steps := exitFixture(t, raw, true, 0)
	steps.wallNode = 2
	base := runGit(t, f.root, "rev-parse", "HEAD")
	result := &LoopResult{}
	for i := 0; i < 6; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 3 || !steps.runs[2].Resume || steps.runs[2].RunID != steps.runs[1].RunID || steps.gates != 0 || result.Landed != 0 || runGit(t, f.root, "rev-parse", "HEAD") != base {
		t.Fatalf("wall admitted unfinished graph: runs %v gates %d landed %d", steps.runs, steps.gates, result.Landed)
	}
}

func TestWorkflowExitCompositeRequiresAllInnerExitNodes(t *testing.T) {
	raw := `{"name":"scout-then-build","version":1,"stages":[{"id":"inner","kind":"workflow","workflow":"inner@v1"},{"id":"finish","kind":"agent","seat":"build","access":"read_only"}],"edges":[{"from_node":"inner","to_node":"finish"}],"exit":{"requires_nodes":["finish"]}}`
	f, l, steps := exitFixture(t, raw, true, 1)
	inner := `{"name":"inner","version":1,"stages":[{"id":"a","kind":"agent","seat":"build","access":"write"},{"id":"b","kind":"agent","seat":"build","access":"read_only"},{"id":"repair","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"a","to_node":"repair","when_outcome":"fail"}],"exit":{"requires_nodes":["a","b"]}}`
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/inner@v1.json"), inner)
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "inner graph")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	result := &LoopResult{}
	for i := 0; i < 10; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 3 || steps.gates != 0 || result.Landed != 0 || runGit(t, f.root, "rev-parse", "HEAD") != base {
		t.Fatalf("partial inner exit admitted successor: runs %v gates %d landed %d", steps.runs, steps.gates, result.Landed)
	}
}
