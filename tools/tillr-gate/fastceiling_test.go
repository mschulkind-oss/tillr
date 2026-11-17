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
