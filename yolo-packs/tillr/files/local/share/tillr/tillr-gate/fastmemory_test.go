package main

import (
	"testing"
)

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
