package main_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPrintsFourCountsAndFetchCommand(t *testing.T) {
	root := gitRepo(t)
	tree := git(t, root, "rev-parse", "HEAD^{tree}")
	check := func(id, command string) map[string]any {
		return map[string]any{"check_id": id, "scope": "tree", "reads": []string{"file"}, "command": []string{"sh", "-c", command}}
	}
	warm := check("warm", "exit 0")
	payload := map[string]any{"plan_id": "count-plan", "base_sha": "base", "tree_sha": tree, "registry_digest": "registry", "checks": []any{warm}}
	execute := func() (int, string) {
		input := write(t, "counts-plan.json", envelope(t, "gate.plan", "counts-plan", payload))
		cmd := exec.Command(gateBin, "run", "--input", input, "--worktree", root, "--output", filepath.Join(scratch(t, "counts"), "verdict.json"))
		out, err := cmd.CombinedOutput()
		if err == nil {
			return 0, string(out)
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), string(out)
		}
		t.Fatal(err)
		return -1, ""
	}
	if code, out := execute(); code != 0 {
		t.Fatalf("warm: %d: %s", code, out)
	}
	absent := check("absent", "exit 99")
	absent["needs"] = []string{"ref:refs/heads/patch/missing"}
	payload["checks"] = []any{warm, check("cold", "exit 0"), absent}
	code, out := execute()
	if code != 0 || !strings.Contains(out, "checks: 1 passed, 1 remembered, 1 unavailable, 0 failed") || !strings.Contains(out, "absent: unavailable; needs ref:refs/heads/patch/missing; provide with: git fetch origin 'refs/heads/patch/missing:refs/heads/patch/missing'") {
		t.Fatalf("unavailable-only nonpass: exit %d: %s", code, out)
	}
	payload["checks"] = []any{warm, check("new", "exit 0"), absent, check("red", "exit 1")}
	code, out = execute()
	if code != 1 || !strings.Contains(out, "checks: 1 passed, 1 remembered, 1 unavailable, 1 failed") {
		t.Fatalf("failed count: exit %d: %s", code, out)
	}
}
