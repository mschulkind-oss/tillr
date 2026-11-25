package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupChecksEveryStepArgumentBranch(t *testing.T) {
	root := scratchUnderDurable(t)
	log := filepath.Join(root, "calls.jsonl")
	program := filepath.Join(root, "step.py")
	writeForkFile(t, program, `import json, sys
from pathlib import Path
args = sys.argv[1:]
if '--help' in args:
    print('tillr-step-argv-check-v1', file=sys.stderr)
else:
    assert '--check-argv' in args
    with Path(`+"'"+log+"'"+`).open('a') as stream:
        stream.write(json.dumps(args) + '\n')
`)
	steps := &ProcessSteps{Root: root, Store: filepath.Join(root, "absent-store"),
		GateProgram:      "python3 " + program + " gate",
		FixerGateProgram: "python3 " + program + " fixer-gate",
		LandProgram:      "python3 " + program + " land", VerdictProgram: "python3 " + program + " verdict"}
	var warnings bytes.Buffer
	if err := steps.CheckPrograms(context.Background(), &warnings); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(warnings.String(), "unchecked") {
		t.Fatalf("supported programs were unchecked: %s", &warnings)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 5 {
		t.Fatalf("want gate, land, two verdicts and one named fixer check; got %s", raw)
	}
	for i, line := range lines {
		var argv []string
		if err := json.Unmarshal([]byte(line), &argv); err != nil {
			t.Fatal(err)
		}
		want := []string{"--item", "--lane", "--run", "--worktree", "--base", "--head", "--root", "--check-argv"}
		switch i {
		case 1:
			want = append(want, "--observed-trunk", "--base-ref", "--message-from")
		case 2, 3:
			want = append(want, "--store", "--verdict")
		case 4:
			want = append(want, "--only", "fixture/check")
		}
		for _, flag := range want {
			found := false
			for _, word := range argv {
				if word == flag {
					found = true
				}
			}
			if !found {
				t.Errorf("startup argv %d lacks %s: %v", i, flag, argv)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".tillr")); !os.IsNotExist(err) {
		t.Fatalf("startup check wrote step records: %v", err)
	}
	if _, err := os.Stat(steps.Store); !os.IsNotExist(err) {
		t.Fatalf("startup check opened the store: %v", err)
	}
}

func TestStartupChecksTheRealAdapterWithoutCandidateArtifacts(t *testing.T) {
	_, _ = repositoryFile(t, "tools/tillr-loop/go.mod")
	root := scratchUnderDurable(t)
	steps := &ProcessSteps{Root: root, Store: filepath.Join(root, "absent-store"),
		GateProgram: "tillr-loop gate", LandProgram: "tillr-loop land",
		VerdictProgram: "tillr-loop verdict", FixerGateProgram: "tillr-loop fixer-gate"}
	var warnings bytes.Buffer
	if err := steps.CheckPrograms(context.Background(), &warnings); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(warnings.String(), "unchecked") {
		t.Fatalf("adapter lacks startup support: %s", &warnings)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("parse-only check wrote artifacts: %v %v", entries, err)
	}
}
