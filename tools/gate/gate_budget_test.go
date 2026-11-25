package main

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildRepoGateForTest(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "repo-gate")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}
	return bin
}

func invokeGateBudgetFixture(t *testing.T, bin string, budgets []struct {
	name string
	tier string
	cost int
}) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	for _, b := range budgets {
		decl := map[string]any{
			"id":      b.name,
			"label":   b.name,
			"command": []string{"true"},
			"scope":   "tree",
			"tier":    b.tier,
		}
		if b.tier == "fast" {
			decl["budget_ms"] = b.cost
		} else {
			decl["timeout_ms"] = b.cost
		}
		raw, _ := json.Marshal(decl)
		if err := os.WriteFile(filepath.Join(dir, b.name+".json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "program.py"), []byte("not JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "gate-budget", dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	return code, stdout.String(), stderr.String()
}

func TestGateBudgetPassesAt9000(t *testing.T) {
	bin := buildRepoGateForTest(t)
	code, out, errOut := invokeGateBudgetFixture(t, bin, []struct {
		name string
		tier string
		cost int
	}{
		{"one", "fast", 4000},
		{"two", "fast", 5000},
		{"slow", "heavy", 90001},
	})
	if code != 0 || !strings.Contains(out, "9000") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestGateBudgetFailsWhenAddedCheckExceeds9000(t *testing.T) {
	bin := buildRepoGateForTest(t)
	code, out, errOut := invokeGateBudgetFixture(t, bin, []struct {
		name string
		tier string
		cost int
	}{
		{"one", "fast", 4000},
		{"two", "fast", 5000},
		{"added", "fast", 1},
	})
	if code != 1 || !strings.Contains(out, "9001") || !strings.Contains(out, "9000") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestGateBudgetEveryFastTimeoutIsRefused(t *testing.T) {
	bin := buildRepoGateForTest(t)
	cases := []struct {
		timeout any
		code    int
	}{
		{nil, 0},
		{1000, 1},
		{5000, 1},
		{9000, 1},
		{999, 1},
		{9001, 1},
		{600000, 1},
		{0, 1},
		{true, 1},
		{"5000", 1},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		decl := map[string]any{"id": "ceiling", "tier": "fast", "budget_ms": 1000}
		if tc.timeout != nil {
			decl["timeout_ms"] = tc.timeout
		}
		raw, _ := json.Marshal(decl)
		if err := os.WriteFile(filepath.Join(dir, "ceiling.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bin, "gate-budget", dir)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != tc.code {
			t.Fatalf("timeout=%v: code=%d want=%d out=%s err=%s", tc.timeout, code, tc.code, stdout.String(), stderr.String())
		}
		if tc.code != 0 && (!strings.Contains(stdout.String()+stderr.String(), "ceiling.json") || !strings.Contains(stdout.String()+stderr.String(), "timeout_ms")) {
			t.Fatalf("timeout=%v: expected ceiling.json and timeout_ms in output", tc.timeout)
		}
	}
}

func TestGateBudgetInvalidCostIsNotZeroBudget(t *testing.T) {
	bin := buildRepoGateForTest(t)
	for _, val := range []any{nil, 0, -1, true, 1.5, "100"} {
		dir := t.TempDir()
		decl := map[string]any{"id": "bad", "tier": "fast", "budget_ms": val}
		raw, _ := json.Marshal(decl)
		if err := os.WriteFile(filepath.Join(dir, "invalid.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bin, "gate-budget", dir)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != 4 || !strings.Contains(stderr.String(), "invalid.json") || !strings.Contains(stderr.String(), "budget_ms") {
			t.Fatalf("val=%v: code=%d out=%s err=%s", val, code, stdout.String(), stderr.String())
		}
	}
}

func TestGateBudgetDoesNotReadAClock(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "gate_budget.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) == "time" {
			t.Fatalf("gate_budget.go imports time; budget admission must not read a clock")
		}
	}
}
