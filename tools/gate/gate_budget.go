package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LimitMS is the fast tier's cap. `tools/tillr-gate/run.go` declares the same number as
// FastTierCapMS, only an arithmetic admission cap, and its
// TestTheTierCapIsTheAdmissionTestsLimit fails when the two disagree.
const LimitMS = 9000

func totalBudget(directory string) (int, []string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0, nil, fmt.Errorf("%s: %w", directory, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	total := 0
	var refusals []string
	for _, name := range names {
		p := filepath.Join(directory, name)
		data, err := os.ReadFile(p)
		if err != nil {
			return 0, nil, fmt.Errorf("%s: %w", p, err)
		}
		var decl map[string]any
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.UseNumber()
		if err := dec.Decode(&decl); err != nil || decl == nil {
			if err != nil {
				return 0, nil, fmt.Errorf("%s: %w", p, err)
			}
			return 0, nil, fmt.Errorf("%s: expected one declaration object", p)
		}
		tier, _ := decl["tier"].(string)
		if tier != "fast" && tier != "heavy" {
			return 0, nil, fmt.Errorf("%s: tier must be fast or heavy", p)
		}
		if tier == "fast" {
			rawBudget, hasBudget := decl["budget_ms"]
			num, isNum := rawBudget.(json.Number)
			if !hasBudget || !isNum {
				return 0, nil, fmt.Errorf("%s: budget_ms must be a positive integer for tier fast", p)
			}
			budget, err := num.Int64()
			if err != nil || budget <= 0 {
				return 0, nil, fmt.Errorf("%s: budget_ms must be a positive integer for tier fast", p)
			}
			total += int(budget)
			if _, hasTimeout := decl["timeout_ms"]; hasTimeout {
				refusals = append(refusals, fmt.Sprintf("%s: timeout_ms is not allowed for tier fast; use budget_ms as the target", p))
			}
		}
	}
	return total, refusals, nil
}

func cmdGateBudget(args []string) int {
	directory := ""
	switch len(args) {
	case 0, 2:
		directory = filepath.Join("tools", "gate", "checks")
	case 1, 3:
		directory = args[0]
	default:
		fmt.Fprintln(os.Stderr, "gate-budget: expected [directory] [base head]")
		return 2
	}

	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "gate-budget: %s: declaration directory is missing\n", directory)
		return 4
	}
	total, refusals, err := totalBudget(directory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gate-budget: %v\n", err)
		return 4
	}
	status := "within"
	if total > LimitMS {
		status = "exceeds"
	}
	fmt.Printf("gate-budget: fast budget sum %d ms %s %d ms\n", total, status, LimitMS)
	for _, r := range refusals {
		fmt.Printf("gate-budget: %s\n", r)
	}
	if total > LimitMS || len(refusals) > 0 {
		return 1
	}
	return 0
}
