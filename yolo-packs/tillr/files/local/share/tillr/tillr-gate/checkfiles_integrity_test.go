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

func TestHeavyDeclarationTimeoutIsNoJudgment(t *testing.T) {
	dir := scratch(t, "heavy-timeout")
	decl := map[string]any{
		"id": "timed-out", "label": "heavy timeout", "command": []string{"sh", "-c", "exec sleep 10"},
		"scope": "tree", "tier": "heavy", "timeout_ms": 100,
	}
	putDeclaration(t, dir, "timeout.json", decl)
	root := gitRepo(t)
	code, out := filePlan(t, dir, []string{"file"}, git(t, root, "rev-parse", "HEAD^{tree}"))
	if code != 0 {
		t.Fatalf("plan exit %d: %s", code, out)
	}
	plan := filepath.Join(scratch(t, "heavy-plan"), "plan.json")
	if err := os.WriteFile(plan, out, 0o644); err != nil {
		t.Fatal(err)
	}
	code, raw := runPlan(t, plan, root)
	verdict := decodeVerdict(t, raw)
	if code != 4 || verdict.Payload.Result != "error" || len(verdict.Payload.Checks) != 1 || verdict.Payload.Checks[0].Result != "error" {
		t.Fatalf("heavy timeout was judged as a candidate failure: exit %d: %s", code, raw)
	}
	log, err := os.ReadFile(verdict.Payload.Checks[0].LogRef)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "timeout_ms") || !strings.Contains(string(log), "timed-out") {
		t.Fatalf("timeout log did not name the check and bound: %s", log)
	}
}

func TestLegacyRegistryTimeoutRemainsAFailedCheck(t *testing.T) {
	plan := planRecord(t, planIn{
		PlanID: "legacy-timeout", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "registry",
		Checks: []planCheck{{CheckID: "legacy", Command: []string{"sh", "-c", "exec sleep 10"}, TimeoutMS: 100, Tier: "landing"}},
	})
	code, raw := runPlan(t, plan, scratch(t, "legacy-root"))
	verdict := decodeVerdict(t, raw)
	if code != 1 || verdict.Payload.Result != "fail" || len(verdict.Payload.Checks) != 1 || verdict.Payload.Checks[0].Result != "fail" {
		t.Fatalf("legacy timeout policy changed: exit %d: %s", code, raw)
	}
}

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
