package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiAdapterScopesChildDiscoveryAndBriefNamesProgress(t *testing.T) {
	adapter, err := filepath.Abs("backends/pi/run-pi")
	if err != nil {
		t.Fatal(err)
	}
	base := scratch(t, "children-env")
	bin := filepath.Join(base, "bin")
	envLog := filepath.Join(base, "env.txt")
	writeExecutable(t, filepath.Join(bin, "pi"), "#!/bin/sh\ncat >/dev/null\nenv > \"$STUB_ENV\"\n")
	worktree := filepath.Join(base, "lane", "worktree")
	if err = os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	brief := filledBrief(t, worktree)
	if !strings.Contains(brief, "TILLR_PROGRESS_FILE") || !strings.Contains(brief, "run.progress") || !strings.Contains(brief, "claimed") {
		t.Fatalf("brief does not tell worker how to claim criteria")
	}
	if err = os.WriteFile(filepath.Join(base, "lane", "brief.md"), []byte(brief), 0644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(base, "recorded")
	root := filepath.Join(base, "pi-subagents")
	cmd := exec.Command(adapter, "--backend", "pi", "--model", "m", "--effort", "medium")
	cmd.Dir = worktree
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "STUB_ENV="+envLog, "PI_SUBAGENTS_TEMP_ROOT=parent-runtime", "TILLR_CHILD_SESSIONS_ROOT="+root, "TILLR_CHILD_SESSIONS_MARKER="+marker)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("adapter: %v %s", err, out)
	}
	raw, err := os.ReadFile(envLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "PI_SUBAGENTS_TEMP_ROOT="+root+"\n") {
		t.Fatalf("child runtime is not run scoped: %s", raw)
	}
	raw, err = os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "pi-subagents-v1\n" {
		t.Fatalf("marker: %s", raw)
	}
}
