package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestChecksScopeToChangedReads(t *testing.T) {
	root := scratch(t, "changed-reads")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name string, body []byte) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("tools/tïllr/main.go", []byte("package main\n"))
	write("docs/README.md", []byte("# Base\n"))
	git("add", ".")
	git("commit", "-qm", "base")
	write("docs/README.md", []byte("# Candidate\n"))
	git("commit", "-qam", "docs only")

	for _, tc := range []struct {
		id, scope string
		reads     []string
	}{
		{"go-build", "tree", []string{"tools/tïllr/"}},
		{"source-change", "change", []string{"tools/tïllr/main.go"}},
		{"repo-wide", "tree", []string{"."}},
		{"undeclared", "tree", nil},
	} {
		decl := map[string]any{"id": tc.id, "label": tc.id, "scope": tc.scope, "tier": "fast",
			"budget_ms": 5000, "command": []string{"touch", tc.id + ".ran"}}
		if tc.reads != nil {
			decl["reads"] = tc.reads
			decl["remember"] = false
		}
		raw, err := json.Marshal(decl)
		if err != nil {
			t.Fatal(err)
		}
		write("checks/"+tc.id+".json", raw)
	}

	run := func(base string, scoped, readsGo bool) {
		t.Helper()
		args := []string{"checks", "--fast-tree", "--base", base, "--head", "HEAD", "--worktree", root,
			"--checks", "checks", "--logs", scratch(t, "logs")}
		if scoped {
			args = append(args, "--scope-to-change")
		}
		var stderr bytes.Buffer
		cmd := exec.Command(gateBin, args...)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("checks: %v\n%s", err, &stderr)
		}
		got := map[string]string{}
		want := map[string]string{"repo-wide": "pass", "undeclared": "pass"}
		for _, id := range []string{"go-build", "source-change"} {
			want[id] = "pass"
			if !readsGo {
				want[id] = "skipped: no changed path is read"
			}
		}
		for _, check := range decodeVerdict(t, out).Payload.Checks {
			got[check.CheckID] = check.Result
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("results = %v, want %v", got, want)
		}
		for id, result := range want {
			marker := filepath.Join(root, id+".ran")
			if err := os.Remove(marker); result == "pass" && err != nil || result != "pass" && !os.IsNotExist(err) {
				t.Fatalf("execution marker for %s (%s): %v", id, result, err)
			}
		}
		if scoped && !strings.Contains(stderr.String(), "checks with no declared reads (always run): undeclared\n") {
			t.Fatalf("missing undeclared reads notice:\n%s", &stderr)
		}
		if !readsGo && !strings.Contains(stderr.String(), "skipped: no changed path is read: go-build, source-change\n") {
			t.Fatalf("missing skip notice:\n%s", &stderr)
		}
	}

	run("HEAD^", false, true) // Trunk proof does not opt in.
	run("HEAD^", true, false)
	run("HEAD^", true, false) // No reads means run, even with a remembered pass.
	git("mv", "tools/tïllr/main.go", "docs/moved.go")
	git("commit", "-qm", "rename out of declared reads")
	run("HEAD^", true, true) // Both the old path and the new path are considered.
}

func TestChecksScopeSkipIsNotCountedAsFailureOnStall(t *testing.T) {
	t.Setenv("TILLR_FAST_VERDICT_OUT", filepath.Join(scratch(t, "stalled-fast"), "fast.json"))
	gate := fakeGate(t, `#!/bin/sh
printf '%s\n' '{"payload":{"checks":[{"check_id":"go-build","result":"skipped: no changed path is read"},{"check_id":"blocked","result":"stalled","silent_samples":60}]}}' > "$TILLR_FAST_VERDICT_OUT"
exit 6
`)
	plan := planRecord(t, planIn{PlanID: "p-skip", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "d"})
	cmd := exec.Command(gateBin, "run", "--input", plan, "--worktree", tree(t), "--gate", gate,
		"--output", filepath.Join(scratch(t, "verdict"), "verdict.json"))
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 6 {
		t.Fatalf("exit = %v, want stalled\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("1 skipped, 1 failed")) {
		t.Fatalf("the skipped check must not be counted as failed:\n%s", out)
	}
}
