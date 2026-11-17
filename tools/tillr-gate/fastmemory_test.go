package main

import (
	"encoding/json"
	"testing"
)

// A carried tree check -- stopped at its budget, its judgment deferred -- has no verdict
// to remember.
func TestFastOverBudgetTreeResultIsNeverRemembered(t *testing.T) {
	root, tree := memoryRepo(t)
	check := PlanCheck{CheckID: "blocked", Scope: "tree", Tier: "fast", BudgetMS: 100,
		Reads: []string{"input/"}, Command: []string{"python3", "-c", "import signal; signal.pause()"}}
	for i := 0; i < 2; i++ {
		envelope, err := RunWith(memoryPlan(t, tree, check), root, t.TempDir(), RunOptions{CarryTreeOverruns: true})
		var verdict VerdictPayload
		if decodeErr := json.Unmarshal(envelope.Payload, &verdict); decodeErr != nil || len(verdict.Checks) != 1 {
			t.Fatalf("run %d: verdict %+v: %v (run: %v)", i, verdict, decodeErr, err)
		}
		outcome := verdict.Checks[0]
		if err != nil || outcome.Result != ResultOverBudget || outcome.Memory == "remembered" || outcome.Fingerprint == "" {
			t.Fatalf("run %d: %+v, %v", i, outcome, err)
		}
	}
	if files := memoryFiles(t, memoryStore(t, root)); len(files) != 0 {
		t.Fatalf("overrun was stored: %v", files)
	}
}

// A kept tree check that ran past its budget still answered, so its pass is a pass and
// is remembered like any other; the overrun is recorded on the run that measured it.
func TestAKeptTreeOverrunPassIsRemembered(t *testing.T) {
	root, tree := memoryRepo(t)
	check := PlanCheck{CheckID: "slow", Scope: "tree", Tier: "fast", BudgetMS: 1,
		Reads: []string{"input/"}, Command: []string{"python3", "-c", "import time; time.sleep(0.02)"}}
	first, err := memoryRun(t, root, tree, check)
	if err != nil || first.Result != ResultPass || first.OverBudgetMS <= 0 || first.Memory != "passed" {
		t.Fatalf("first run: %+v, %v", first, err)
	}
	second, err := memoryRun(t, root, tree, check)
	if err != nil || second.Result != ResultPass || second.Memory != "remembered" {
		t.Fatalf("second run: %+v, %v", second, err)
	}
}
