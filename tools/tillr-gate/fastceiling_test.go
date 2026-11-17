package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// A fast check's ceiling -- the point where a check that ran past its budget is stopped
// with no verdict -- lies in [budget_ms, FastTierCapMS]. With no timeout_ms of its own it
// is the cap: one check may not take longer than the whole tier is allowed. A timeout_ms
// above the cap cannot lift it (a fast check with a ten-minute ceiling is not fast), and
// one below the budget cannot stop the check before its own budget.
func TestFastCeilingLiesBetweenTheBudgetAndTheTierCap(t *testing.T) {
	for _, tc := range []struct {
		name               string
		budget, timeout, w int
	}{
		{"no timeout_ms", 2000, 0, FastTierCapMS},
		{"a timeout_ms inside the range", 2000, 5000, 5000},
		{"a timeout_ms above the cap", 2000, 600000, FastTierCapMS},
		{"a timeout_ms below the budget", 2000, 500, 2000},
	} {
		for _, scope := range []string{"change", "tree"} {
			got := fastCeilingMS(PlanCheck{Scope: scope, Tier: "fast", BudgetMS: tc.budget, TimeoutMS: tc.timeout})
			if got != tc.w {
				t.Errorf("%s, %s: ceiling %d, want %d", scope, tc.name, got, tc.w)
			}
		}
	}
}

// The cap is declared twice: here, where the runner stops a check, and in the admission
// test that sums the fast budgets. The two must be one number, or a check admitted under
// one cap is stopped under another.
func TestTheTierCapIsTheAdmissionTestsLimit(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "gate", "checks", "gate-budget.py"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^LIMIT_MS = (\d+)$`).FindSubmatch(source)
	if match == nil {
		t.Fatal("gate-budget.py declares no LIMIT_MS")
	}
	limit, _ := strconv.Atoi(string(match[1]))
	if limit != FastTierCapMS {
		t.Fatalf("gate-budget.py LIMIT_MS is %d, FastTierCapMS is %d", limit, FastTierCapMS)
	}
}
