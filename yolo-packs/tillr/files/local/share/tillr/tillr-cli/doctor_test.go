package main_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func buildDoctorFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, ".tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(root, ".tillr", "state.db")
	s, err := store.Open(context.Background(), storePath, true)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[lanes]\nexpress = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	adapterDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(adapterDir, 0o755); err != nil {
		t.Fatal(err)
	}
	adapterPath := filepath.Join(adapterDir, "run-fixture")
	if err := os.WriteFile(adapterPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	policyDir := filepath.Join(root, "config", "tillr")
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	policyBody := fmt.Sprintf(`{
  "default_backend": "fixture",
  "seats": {"build": "fixture"},
  "backends": {
    "fixture": {
      "adapter": "bin/run-fixture",
      "model": "example/model"
    }
  }
}`)
	if err := os.WriteFile(filepath.Join(policyDir, "backend-policy.json"), []byte(policyBody), 0o644); err != nil {
		t.Fatal(err)
	}

	wfDir := filepath.Join(policyDir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "planned-build@v1.json"), []byte(`{"id":"planned-build@v1"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	rulesDir := filepath.Join(policyDir, "watch")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ruleTOML := `[[rule]]
id = "test-rule"
scope = "core"
severity = "warning"

[rule.reads]
events = ["fixture.seed"]

[rule.when]
group_by = "item_id"
pattern = "predicate_match"
window = "1h"

[rule.guidance]
summary = "The drain recorded a fault for {{subject}}."
check = ["Run tillr drain why {{subject}}."]
do = ["Read the fault detail."]

[rule.delivery]
receiver = "foreground"
`
	if err := os.WriteFile(filepath.Join(rulesDir, "rule.toml"), []byte(ruleTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	checksDir := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	checkJSON := `{"id":"go-test","label":"go test","command":["go","test","./..."],"scope":"tree","tier":"fast"}`
	if err := os.WriteFile(filepath.Join(checksDir, "go-test.json"), []byte(checkJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	briefPath := filepath.Join(root, "brief-template.md")
	if err := os.WriteFile(briefPath, []byte("# Brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	return root
}

func doctorLines(t *testing.T, stdout string) []string {
	t.Helper()
	trimmed := strings.TrimSuffix(stdout, "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func TestDoctorPassesCompleteFixture(t *testing.T) {
	root := buildDoctorFixture(t)
	briefPath := filepath.Join(root, "brief-template.md")

	code, stdout, stderr := runWrapper(t, t.TempDir(), "", "doctor", "--root", root, "--brief-template", briefPath)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	lines := doctorLines(t, stdout)
	if len(lines) != 7 {
		t.Fatalf("got %d lines, want 7:\n%s", len(lines), stdout)
	}
	wantOrder := []string{"store", "settings", "policy", "brief-template", "default-workflow", "watch-rules", "gate-checks"}
	for i, name := range wantOrder {
		if !strings.HasPrefix(lines[i], "ok   "+name+":") {
			t.Errorf("line %d = %q, want prefix %q", i, lines[i], "ok   "+name+":")
		}
	}
}

func TestDoctorHelpAndUsage(t *testing.T) {
	code, stdout, stderr := runWrapper(t, t.TempDir(), "", "doctor", "--help")
	if code != 0 {
		t.Fatalf("--help exit code = %d, want 0 (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stdout, "Usage: tillr doctor") {
		t.Errorf("--help stdout = %q, want Usage: tillr doctor", stdout)
	}

	code, stdout, stderr = runWrapper(t, t.TempDir(), "", "doctor", "--unknown-flag")
	if code != 2 {
		t.Errorf("unknown flag exit code = %d, want 2", code)
	}
	if stdout != "" || stderr == "" {
		t.Errorf("unknown flag stdout=%q stderr=%q", stdout, stderr)
	}

	code, stdout, stderr = runWrapper(t, t.TempDir(), "", "doctor", "extra-arg")
	if code != 2 {
		t.Errorf("extra arg exit code = %d, want 2", code)
	}
	if stdout != "" || stderr == "" {
		t.Errorf("extra arg stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestDoctorReadOnlyOnEmptyDir(t *testing.T) {
	emptyDir := t.TempDir()
	code, stdout, stderr := runWrapper(t, t.TempDir(), "", "doctor", "--root", emptyDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	lines := doctorLines(t, stdout)
	if len(lines) != 7 {
		t.Fatalf("got %d lines, want 7:\n%s", len(lines), stdout)
	}
	entries, err := os.ReadDir(emptyDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("doctor modified emptyDir: found %v", entries)
	}
}

func TestDoctorFailingChecks(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(t *testing.T, root string) []string
		wantName string
		wantFix  string
	}{
		{
			name: "missing store",
			mutate: func(t *testing.T, root string) []string {
				if err := os.Remove(filepath.Join(root, ".tillr", "state.db")); err != nil {
					t.Fatal(err)
				}
				return nil
			},
			wantName: "store",
			wantFix:  "tillr store init --store",
		},
		{
			name: "malformed settings",
			mutate: func(t *testing.T, root string) []string {
				if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("not = [valid\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return nil
			},
			wantName: "settings",
			wantFix:  ".tillr.toml",
		},
		{
			name: "missing policy",
			mutate: func(t *testing.T, root string) []string {
				if err := os.Remove(filepath.Join(root, "config", "tillr", "backend-policy.json")); err != nil {
					t.Fatal(err)
				}
				return nil
			},
			wantName: "policy",
			wantFix:  "--policy",
		},
		{
			name: "missing adapter",
			mutate: func(t *testing.T, root string) []string {
				if err := os.Remove(filepath.Join(root, "bin", "run-fixture")); err != nil {
					t.Fatal(err)
				}
				return nil
			},
			wantName: "policy",
			wantFix:  "bin/run-fixture",
		},
		{
			name: "non-executable adapter",
			mutate: func(t *testing.T, root string) []string {
				if err := os.Chmod(filepath.Join(root, "bin", "run-fixture"), 0o644); err != nil {
					t.Fatal(err)
				}
				return nil
			},
			wantName: "policy",
			wantFix:  "chmod +x bin/run-fixture",
		},
		{
			name: "missing brief template",
			mutate: func(t *testing.T, root string) []string {
				return []string{"--brief-template", filepath.Join(root, "missing-brief.md")}
			},
			wantName: "brief-template",
			wantFix:  "--brief-template",
		},
		{
			name: "missing default workflow",
			mutate: func(t *testing.T, root string) []string {
				if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[workflows.default]\nbuild = \"missing-wf@v1\"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return nil
			},
			wantName: "default-workflow",
			wantFix:  "config/tillr/workflows/missing-wf@v1.json",
		},
		{
			name: "missing watch rules",
			mutate: func(t *testing.T, root string) []string {
				if err := os.RemoveAll(filepath.Join(root, "config", "tillr", "watch")); err != nil {
					t.Fatal(err)
				}
				return nil
			},
			wantName: "watch-rules",
			wantFix:  "config/tillr/watch",
		},
		{
			name: "invalid watch rule",
			mutate: func(t *testing.T, root string) []string {
				if err := os.WriteFile(filepath.Join(root, "config", "tillr", "watch", "bad.toml"), []byte("not = [valid\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return nil
			},
			wantName: "watch-rules",
			wantFix:  "bad.toml",
		},
		{
			name: "missing gate checks",
			mutate: func(t *testing.T, root string) []string {
				if err := os.RemoveAll(filepath.Join(root, "tools", "gate", "checks")); err != nil {
					t.Fatal(err)
				}
				return nil
			},
			wantName: "gate-checks",
			wantFix:  "tools/gate/checks",
		},
		{
			name: "empty gate checks",
			mutate: func(t *testing.T, root string) []string {
				if err := os.Remove(filepath.Join(root, "tools", "gate", "checks", "go-test.json")); err != nil {
					t.Fatal(err)
				}
				return nil
			},
			wantName: "gate-checks",
			wantFix:  "tools/gate/checks",
		},
		{
			name: "malformed gate check",
			mutate: func(t *testing.T, root string) []string {
				if err := os.WriteFile(filepath.Join(root, "tools", "gate", "checks", "go-test.json"), []byte(`{"id":"only-id"}`), 0o644); err != nil {
					t.Fatal(err)
				}
				return nil
			},
			wantName: "gate-checks",
			wantFix:  "go-test.json",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := buildDoctorFixture(t)
			extraArgs := tc.mutate(t, root)
			args := []string{"doctor", "--root", root}
			hasBrief := false
			for _, a := range extraArgs {
				if a == "--brief-template" {
					hasBrief = true
				}
			}
			if !hasBrief {
				args = append(args, "--brief-template", filepath.Join(root, "brief-template.md"))
			}
			args = append(args, extraArgs...)

			code, stdout, _ := runWrapper(t, t.TempDir(), "", args...)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1\nstdout:\n%s", code, stdout)
			}
			lines := doctorLines(t, stdout)
			if len(lines) != 7 {
				t.Fatalf("got %d lines, want 7:\n%s", len(lines), stdout)
			}
			var found string
			for _, line := range lines {
				if strings.HasPrefix(line, "FAIL "+tc.wantName+":") {
					found = line
					break
				}
			}
			if found == "" {
				t.Fatalf("no FAIL line for %q in:\n%s", tc.wantName, stdout)
			}
			if !strings.Contains(found, tc.wantFix) {
				t.Errorf("FAIL line %q does not contain %q", found, tc.wantFix)
			}
		})
	}
}

func TestDoctorAcceptsProducerWatchRules(t *testing.T) {
	root := buildDoctorFixture(t)
	ruleTOML := `[[rule]]
id = "drain-backend-closed"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.backend-outcome"]

[rule.when]
group_by = "object_id"
pattern = "predicate_match"
window = "1h"

[rule.guidance]
summary = "Backend {{subject}} closed."
check = ["Run tillr drain status."]
do = ["Inspect backend health."]

[rule.delivery]
receiver = "foreground"
`
	if err := os.WriteFile(filepath.Join(root, "config", "tillr", "watch", "drain-backend-closed.toml"), []byte(ruleTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runWrapper(t, t.TempDir(), "",
		"doctor", "--root", root, "--brief-template", filepath.Join(root, "brief-template.md"))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	lines := doctorLines(t, stdout)
	if len(lines) != 7 {
		t.Fatalf("got %d lines, want 7:\n%s", len(lines), stdout)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "ok   ") {
			t.Errorf("expected ok line, got: %q", line)
		}
	}

	// Also verify the repository's own shipped watch rules directory passes.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWrapper(t, t.TempDir(), "",
		"doctor", "--root", root,
		"--brief-template", filepath.Join(root, "brief-template.md"),
		"--rules", filepath.Join(repoRoot, "config", "tillr", "watch"))
	if code != 0 {
		t.Fatalf("shipped watch rules exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}
