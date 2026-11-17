package main

import "testing"

// With no timeout_ms of its own, a fast change check's ceiling is the fast tier's
// admission cap: one check may not take longer than the whole tier is allowed.
func TestFastChangeCeilingDefaultsToTheTierCap(t *testing.T) {
	if got := changeCeilingMS(PlanCheck{Scope: "change", Tier: "fast", BudgetMS: 2000}); got != FastTierCapMS {
		t.Fatalf("default ceiling %d, want %d", got, FastTierCapMS)
	}
	if got := changeCeilingMS(PlanCheck{Scope: "change", Tier: "fast", BudgetMS: 2000, TimeoutMS: 5000}); got != 5000 {
		t.Fatalf("declared ceiling %d, want 5000", got)
	}
}
