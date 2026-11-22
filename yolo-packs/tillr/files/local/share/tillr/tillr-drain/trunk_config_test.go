package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func TestSharedWorktreeConfigStopsGateAndLandAndIsNamedInWhy(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	// An explicit empty value is still a setting, not an absent key. Use an
	// alternate spelling so a text search for the literal key isn't enough.
	config := filepath.Join(root, ".git", "config")
	f, err := os.OpenFile(config, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("[CoRe]\n\tWorkTree = \"\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// A started step answers with a distinct infrastructure error, without
	// performing Git operations or needing a saved gate judgment.
	step := filepath.Join(root, "step")
	if err := os.WriteFile(step, []byte("#!/bin/sh\nexit 97\n"), 0755); err != nil {
		t.Fatal(err)
	}
	store := fixture(t)
	steps := drain.ProcessSteps{Root: root, Store: store, GateProgram: step, LandProgram: step, Now: time.Now}
	candidate := drain.Candidate{Worktree: root}
	ctx := context.Background()
	_, gateErr := steps.Gate(ctx, candidate)
	_, landErr := steps.Land(ctx, candidate)
	_, boundaryErr := drain.TakeBoundary(ctx, drain.BoundarySpec{Repo: root, Targets: []string{root}})
	namesConfig := func(s string) bool {
		return strings.Contains(s, "core.worktree") && strings.Contains(s, config)
	}
	for step, err := range map[string]error{"gate": gateErr, "land": landErr, "snapshot": boundaryErr} {
		if err == nil || !namesConfig(err.Error()) {
			t.Errorf("%s did not refuse the shared config: %v", step, err)
		}
	}
	code, out := invoke(t, "loop", "--store", store, "--root", root,
		"--policy", policy(t, step), "--gate", step, "--land", step, "--verdict", step)
	if code == 0 || !namesConfig(string(out)) {
		t.Errorf("loop did not refuse the shared config (exit %d): %s", code, out)
	}
	code, out = invoke(t, "why", "--store", store, "--root", root)
	if code == 0 || !namesConfig(string(out)) {
		t.Errorf("why did not name the shared config (exit %d): %s", code, out)
	}
}
