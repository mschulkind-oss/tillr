package main

import "testing"

func TestFastOverBudgetTreeResultIsNeverRemembered(t *testing.T) {
	root, tree := memoryRepo(t)
	check := PlanCheck{CheckID: "blocked", Scope: "tree", Tier: "fast", BudgetMS: 100,
		Reads: []string{"input/"}, Command: []string{"python3", "-c", "import signal; signal.pause()"}}
	for i := 0; i < 2; i++ {
		outcome, err := memoryRun(t, root, tree, check)
		if err != nil || outcome.Result != ResultOverBudget || outcome.Memory == "remembered" || outcome.Fingerprint == "" {
			t.Fatalf("run %d: %+v, %v", i, outcome, err)
		}
	}
	if files := memoryFiles(t, memoryStore(t, root)); len(files) != 0 {
		t.Fatalf("overrun was stored: %v", files)
	}
}
