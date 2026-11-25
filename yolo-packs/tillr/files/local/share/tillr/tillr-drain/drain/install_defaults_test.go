package drain

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, path, content)
}

func TestProcessStepsRunOmitsDefaultBackendsDir(t *testing.T) {
	f, _ := passFixture(t)
	// Place a decoy tools/tillr-run/backends in the consumer root.
	decoyBackends := filepath.Join(f.root, "tools", "tillr-run", "backends")
	writeTestFile(t, filepath.Join(decoyBackends, "brief-template.md"), "decoy\n")

	argvFile := filepath.Join(scratchUnderDurable(t), "run-argv.txt")
	runStub := executable(t, `
printf '%s\n' "$@" > `+argvFile+`
printf '{"record_version":1,"kind":"run.record","id":"run-1","producer":{"system":"tillr","utility":"tillr-run","version":"0.1.0"},"created_at":"2026-01-01T00:00:00Z","payload":{"run_id":"run-1","item_id":"item-a","lane_id":"lane-1","backend":"command","status":"done","exit_code":0}}\n'
`)

	steps := &ProcessSteps{
		RunBin:   runStub,
		Store:    f.storePath,
		LaneRoot: f.laneRoot,
		Root:     f.root,
		Now:      time.Now,
	}
	steps.ConfigureNonLanding()
	if steps.BackendsDir != "" {
		t.Fatalf("ConfigureNonLanding set BackendsDir = %q, want empty", steps.BackendsDir)
	}

	req := run.Request{
		RunID:      "run-1",
		ItemID:     "item-a",
		LaneID:     "lane-1",
		Backend:    "command",
		Command:    []string{"true"},
		WorkingDir: filepath.Join(f.laneRoot, "lane-1", WorktreeDir),
		TimeoutMS:  10000,
	}
	if _, _, err := steps.Run(context.Background(), req); err != nil {
		t.Fatalf("ProcessSteps.Run with empty BackendsDir: %v", err)
	}
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if slices.Contains(args, "--backends-dir") {
		t.Fatalf("ProcessSteps.Run passed --backends-dir when BackendsDir was empty: %v", args)
	}

	// Explicit relative BackendsDir still resolves against Root.
	steps.BackendsDir = "custom-backends"
	req.RunID = "run-2"
	if _, _, err := steps.Run(context.Background(), req); err != nil {
		t.Fatalf("ProcessSteps.Run with explicit relative BackendsDir: %v", err)
	}
	raw, err = os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	args = strings.Split(strings.TrimSpace(string(raw)), "\n")
	idx := slices.Index(args, "--backends-dir")
	absRoot, _ := filepath.Abs(f.root)
	wantDir := filepath.Join(absRoot, "custom-backends")
	if idx < 0 || idx+1 >= len(args) || args[idx+1] != wantDir {
		t.Fatalf("ProcessSteps.Run explicit relative BackendsDir argv = %v, want --backends-dir %q", args, wantDir)
	}
}

func TestProcessStepsGateUsesTillrDrainForkGate(t *testing.T) {
	f, _ := passFixture(t)
	// Place a decoy tools/tillr-drain/fork-gate.py in the consumer root.
	writeTestFile(t, filepath.Join(f.root, "tools", "tillr-drain", "fork-gate.py"), "import sys; sys.exit(99)\n")

	binDir := scratchUnderDurable(t)
	calledFile := filepath.Join(scratchUnderDurable(t), "fork-gate-called.txt")
	drainStub := filepath.Join(binDir, "tillr-drain")
	if err := os.WriteFile(drainStub, []byte("#!/bin/sh\nprintf '%s' \"$*\" > "+calledFile+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	steps := &ProcessSteps{
		Root:        f.root,
		Store:       f.storePath,
		LaneRoot:    f.laneRoot,
		Now:         time.Now,
		GateProgram: "true",
	}
	worktree := filepath.Join(f.laneRoot, "lane-1", WorktreeDir)
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	c := Candidate{
		ItemID:   "item-a",
		LaneID:   "lane-1",
		RunID:    "run-fork-1",
		Worktree: worktree,
		BaseRef:  "refs/heads/patch/pi",
		BaseSHA:  strings.Repeat("1", 40),
		HeadSHA:  strings.Repeat("2", 40),
	}
	out, err := steps.Gate(context.Background(), c)
	if err != nil || out.Outcome != GatePassed {
		t.Fatalf("fork Gate = %+v, %v", out, err)
	}
	got, err := os.ReadFile(calledFile)
	if err != nil {
		t.Fatalf("tillr-drain fork-gate was not called: %v", err)
	}
	if !strings.Contains(string(got), "fork-gate --fork pi") {
		t.Fatalf("tillr-drain args = %q, want fork-gate --fork pi", string(got))
	}
}
