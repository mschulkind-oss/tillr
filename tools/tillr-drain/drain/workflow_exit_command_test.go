package drain

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowExitCommandCheckRepairCheckAgain(t *testing.T) {
	f, l, steps := commandLoopFixture(t, "pass", "read_only", false)
	steps.repairThird = true
	argv := []string{"python3", "-c", `import sys; sys.exit(0 if open('tools/item-a/result.txt').read() == 'recovered\n' else 1)`}
	commands, _ := json.Marshal(map[string]CommandEntry{"check": {Argv: argv, Flags: map[string]string{}}})
	writeForkFile(t, filepath.Join(f.root, "config/tillr/commands.json"), string(commands))
	raw := `{"name":"scout-then-build","version":1,"stages":[{"id":"edit","kind":"agent","seat":"build","access":"write"},{"id":"check","kind":"command","command_ref":"check","access":"read_only"},{"id":"repair","kind":"agent","seat":"build","access":"write"},{"id":"check-again","kind":"command","command_ref":"check","access":"read_only"}],"edges":[{"from_node":"edit","to_node":"check"},{"from_node":"check","to_node":"repair","when_outcome":"fail"},{"from_node":"repair","to_node":"check-again"}],"exit":{"requires_nodes":["check-again"],"requires_candidate":"commit_required"}}`
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), raw)
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "repair graph")
	result := &LoopResult{}
	for i := 0; i < 10; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 4 || result.Landed != 1 || steps.gates != 1 {
		t.Fatalf("runs %v result %+v", steps.runs, result)
	}
	for i, req := range steps.runs {
		if !strings.HasSuffix(req.RunID, []string{"-n1", "-n2", "-n3", "-n4"}[i]) {
			t.Errorf("run order: %v", steps.runs)
		}
	}
	if steps.runs[1].Backend != "command" || steps.runs[3].Backend != "command" {
		t.Fatal("checks were not commands")
	}
}

func TestWorkflowExitBlockedAgentDoesNotFollowFailure(t *testing.T) {
	_, l, steps := exitFixture(t, exitRepair, true, 2)
	steps.blockedFailure = true
	result := &LoopResult{}
	for i := 0; i < 4; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 2 || result.Landed != 0 {
		t.Fatalf("blocked followed fail edge: %+v %+v", steps.runs, result)
	}
}
