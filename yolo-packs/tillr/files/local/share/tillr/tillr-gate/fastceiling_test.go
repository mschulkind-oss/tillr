package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// The runner names the admission cap; only the arithmetic admission test uses it.
// The two declarations must agree without imposing an execution deadline.
func TestTheTierCapIsTheAdmissionTestsLimit(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "gate", "gate_budget.go"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^const LimitMS = (\d+)$`).FindSubmatch(source)
	if match == nil {
		t.Fatal("gate_budget.go declares no LimitMS")
	}
	limit, _ := strconv.Atoi(string(match[1]))
	if limit != FastTierCapMS {
		t.Fatalf("gate_budget.go LimitMS is %d, FastTierCapMS is %d", limit, FastTierCapMS)
	}
}
