package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fileDeclaration() map[string]any {
	return map[string]any{
		"id": "fixture", "label": "fixture check", "command": []string{"sh", "-c", "exit 0"},
		"scope": "tree", "tier": "fast", "budget_ms": 100,
		"reads": []string{"src/"}, "tools": []string{"sh"}, "needs": []string{"fixture-resource"},
		"remember": false, "writes": []string{"out/"}, "exclusive": true,
	}
}

func putDeclaration(t *testing.T, dir, name string, declaration map[string]any) {
	t.Helper()
	raw, err := json.Marshal(declaration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func directorySnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(path, dir)] = info.Mode().String() + string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func filePlan(t *testing.T, dir string, changed []string, judgedTree ...string) (int, []byte) {
	t.Helper()
	treeSHA := "tree"
	if len(judgedTree) > 0 {
		treeSHA = judgedTree[0]
	}
	input := write(t, "change.json", envelope(t, "gate.change", "fixture", map[string]any{
		"base_sha": "base", "head_sha": "head", "tree_sha": treeSHA, "changed_paths": changed,
	}))
	cmd := exec.Command(gateBin, "plan", "--registry", dir, "--input", input)
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), out
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, out
}

func decodedFilePlan(t *testing.T, out []byte) []map[string]any {
	t.Helper()
	var plan struct {
		Payload struct {
			Checks []map[string]any `json:"checks"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &plan); err != nil {
		t.Fatalf("decode plan: %v: %s", err, out)
	}
	return plan.Payload.Checks
}

func TestPlanReadsOneFilePerCheckWithoutEditingOtherFiles(t *testing.T) {
	dir := scratch(t, "checks")
	first := fileDeclaration()
	first["id"] = "first"
	putDeclaration(t, dir, "first.json", first)
	// Programs live next to declarations, but are not declarations themselves.
	if err := os.WriteFile(filepath.Join(dir, "program.py"), []byte("not JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := directorySnapshot(t, dir)
	code, out := filePlan(t, dir, []string{"src/new.go"})
	if code != 0 {
		t.Fatalf("plan exit %d: %s", code, out)
	}
	if checks := decodedFilePlan(t, out); len(checks) != 1 || checks[0]["check_id"] != "first" {
		t.Fatalf("first plan: %v", checks)
	}
	putDeclaration(t, dir, "fixture.json", fileDeclaration())
	afterAdd := directorySnapshot(t, dir)
	for path, body := range before {
		if afterAdd[path] != body {
			t.Fatalf("adding a declaration changed %s", path)
		}
	}
	if len(afterAdd) != len(before)+1 {
		t.Fatal("adding a check wrote more than its declaration")
	}
	code, out = filePlan(t, dir, []string{"src/new.go"})
	if code != 0 {
		t.Fatalf("plan exit %d: %s", code, out)
	}
	checks := decodedFilePlan(t, out)
	if len(checks) != 2 || checks[1]["check_id"] != "fixture" {
		t.Fatalf("added check is missing: %v", checks)
	}
	got := checks[1]
	for key, want := range map[string]any{
		"scope": "tree", "tier": "fast", "budget_ms": float64(100), "remember": false, "exclusive": true,
		"reads": []any{"src/"}, "tools": []any{"sh"}, "needs": []any{"fixture-resource"}, "writes": []any{"out/"},
	} {
		if !reflect.DeepEqual(got[key], want) {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
	if !reflect.DeepEqual(afterAdd, directorySnapshot(t, dir)) {
		t.Fatal("planning wrote or changed a declaration or program")
	}
	code, out = filePlan(t, dir, []string{"other/file"})
	if code != 0 || len(decodedFilePlan(t, out)) != 0 {
		t.Fatalf("unrelated change selected a tree check: exit %d: %s", code, out)
	}
}

func TestFileDeclarationsRefuseMissingVerdictFieldsBeforeAnyCheckRuns(t *testing.T) {
	for _, field := range []string{"scope", "tier", "budget_ms", "timeout_ms"} {
		t.Run(field, func(t *testing.T) {
			dir := scratch(t, "invalid-checks")
			marker := filepath.Join(dir, "ran")
			valid := fileDeclaration()
			valid["command"] = []string{"touch", marker}
			putDeclaration(t, dir, "a-valid.json", valid)
			invalid := fileDeclaration()
			if field == "timeout_ms" {
				invalid["tier"] = "heavy"
			}
			delete(invalid, field)
			putDeclaration(t, dir, "z-invalid.json", invalid)
			before := directorySnapshot(t, dir)
			code, out := filePlan(t, dir, []string{"src/file"})
			if code != 4 || !strings.Contains(string(out), "z-invalid.json") || !strings.Contains(string(out), field) {
				t.Fatalf("refusal exit %d: %s; want could-not-judge (4), filename and %s", code, out, field)
			}
			if strings.Contains(string(out), `"gate.plan"`) {
				t.Fatalf("invalid declaration produced a plan: %s", out)
			}
			if !reflect.DeepEqual(before, directorySnapshot(t, dir)) {
				t.Fatal("a refused plan ran a check or changed its input")
			}
		})
	}
}

func TestFileDeclarationDefaultsAndChangeArguments(t *testing.T) {
	dir := scratch(t, "defaults")
	decl := map[string]any{"id": "change", "label": "change check", "command": []string{"program"}, "scope": "change", "tier": "heavy", "timeout_ms": 3000}
	putDeclaration(t, dir, "change.json", decl)
	code, out := filePlan(t, dir, []string{})
	if code != 0 {
		t.Fatalf("plan exit %d: %s", code, out)
	}
	checks := decodedFilePlan(t, out)
	if len(checks) != 1 {
		t.Fatalf("change check not planned: %s", out)
	}
	for key, want := range map[string]any{
		"command": []any{"program", "base", "head"}, "reads": []any{"."}, "remember": true,
		"scope": "change", "tier": "heavy", "timeout_ms": float64(3000), "exclusive": false,
	} {
		if !reflect.DeepEqual(checks[0][key], want) {
			t.Errorf("%s = %v, want %v", key, checks[0][key], want)
		}
	}
}

func TestFileDeclarationsRejectInvalidValuesAndDuplicateIDs(t *testing.T) {
	for _, field := range []string{"id", "label", "command", "scope", "tier", "budget_ms", "timeout_ms", "unknown"} {
		t.Run(field, func(t *testing.T) {
			dir := scratch(t, "invalid-values")
			decl := fileDeclaration()
			switch field {
			case "command":
				decl[field] = []string{}
			case "budget_ms":
				decl[field] = 0
			case "timeout_ms":
				decl["tier"] = "heavy"
				decl[field] = -1
			case "scope", "tier", "unknown":
				decl[field] = "invalid"
			default:
				decl[field] = ""
			}
			putDeclaration(t, dir, "invalid.json", decl)
			code, out := filePlan(t, dir, []string{"src/file"})
			if code != 4 || !strings.Contains(string(out), "invalid.json") || !strings.Contains(string(out), field) {
				t.Fatalf("exit %d: %s", code, out)
			}
		})
	}
	dir := scratch(t, "duplicate")
	putDeclaration(t, dir, "one.json", fileDeclaration())
	putDeclaration(t, dir, "two.json", fileDeclaration())
	code, out := filePlan(t, dir, []string{"src/file"})
	if code != 4 || !strings.Contains(string(out), "two.json") || !strings.Contains(string(out), "id") {
		t.Fatalf("duplicate id exit %d: %s", code, out)
	}
	for name, reads := range map[string][]string{
		"empty-exclusion": {"src/", "!"},
		"invalid-glob":    {"src/", "!src/["},
		"exclusion-only":  {"!src/README.md"},
	} {
		t.Run(name, func(t *testing.T) {
			d := scratch(t, name)
			decl := fileDeclaration()
			decl["reads"] = reads
			putDeclaration(t, d, "invalid.json", decl)
			code, out := filePlan(t, d, []string{"src/file"})
			if code != 4 || !strings.Contains(string(out), "invalid.json") || !strings.Contains(string(out), "reads") {
				t.Fatalf("exit %d: %s", code, out)
			}
		})
	}
}
