package main_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeclarationDigestBindsContentNotAbsoluteDirectory(t *testing.T) {
	one, two := scratch(t, "checks-one"), scratch(t, "checks-two")
	putDeclaration(t, one, "fixture.json", fileDeclaration())
	putDeclaration(t, two, "fixture.json", fileDeclaration())
	code, first := filePlan(t, one, []string{"src/file"})
	if code != 0 {
		t.Fatalf("first plan: %d: %s", code, first)
	}
	code, second := filePlan(t, two, []string{"src/file"})
	if code != 0 || !bytes.Equal(first, second) {
		t.Fatalf("identical directory contents produced different plans: %d: %s", code, second)
	}
	decl := fileDeclaration()
	decl["budget_ms"] = 101
	putDeclaration(t, two, "fixture.json", decl)
	code, second = filePlan(t, two, []string{"src/file"})
	if code != 0 || bytes.Equal(first, second) {
		t.Fatalf("changed declaration did not change plan: %d: %s", code, second)
	}
}

func TestDeclarationDirectoryRefusesEmptyAndMalformedInputs(t *testing.T) {
	for _, content := range []string{"", "null", "{", `{"id":"x"} {"id":"y"}`} {
		t.Run(content, func(t *testing.T) {
			dir := scratch(t, "malformed-checks")
			if content != "" {
				if err := os.WriteFile(filepath.Join(dir, "invalid.json"), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			code, out := filePlan(t, dir, []string{"src/file"})
			if code != 4 || strings.Contains(string(out), `"gate.plan"`) {
				t.Fatalf("invalid directory produced exit %d: %s", code, out)
			}
		})
	}
}

func TestEmptyReadsSelectsTheWholeTree(t *testing.T) {
	dir := scratch(t, "empty-reads")
	decl := fileDeclaration()
	decl["reads"] = []string{}
	putDeclaration(t, dir, "tree.json", decl)
	code, out := filePlan(t, dir, []string{"unrelated/file"})
	if code != 0 {
		t.Fatalf("plan exit %d: %s", code, out)
	}
	checks := decodedFilePlan(t, out)
	if len(checks) != 1 || checks[0]["check_id"] != "fixture" {
		t.Fatalf("empty reads skipped a whole-tree check: %s", out)
	}
	reads, ok := checks[0]["reads"].([]any)
	if !ok || len(reads) != 1 || reads[0] != "." {
		t.Fatalf("empty reads did not default to the whole tree: %v", checks[0]["reads"])
	}
}

// Heavy and generated targets are exercised with an injected clock in watchdog_test.go.

func TestChangeDeclarationRefusesRecordWithoutHeadCommit(t *testing.T) {
	dir := scratch(t, "change-checks")
	decl := fileDeclaration()
	decl["scope"] = "change"
	putDeclaration(t, dir, "change.json", decl)
	input := write(t, "old-change.json", envelope(t, "gate.change", "old", change{
		BaseSHA: "base", TreeSHA: "tree-not-a-commit", ChangedPaths: []string{"src/file"},
	}))
	cmd := exec.Command(gateBin, "plan", "--registry", dir, "--input", input)
	out, err := cmd.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 4 || !strings.Contains(string(out), "head_sha") {
		t.Fatalf("old record refusal: %v: %s", err, out)
	}
	if strings.Contains(string(out), `"gate.plan"`) {
		t.Fatalf("old record produced a plan: %s", out)
	}
}
