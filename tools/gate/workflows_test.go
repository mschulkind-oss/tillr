package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func setupWorkflowRepo(t *testing.T) (string, string, string) {
	t.Helper()
	root, id := loadTestContext(t)
	personal := id[0]
	repo := t.TempDir()
	bin := buildRepoGateForTest(t)

	gitEnv := append(os.Environ(),
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=user.name",
		"GIT_CONFIG_VALUE_0="+personal[0],
		"GIT_CONFIG_KEY_1=user.email",
		"GIT_CONFIG_VALUE_1="+personal[1],
	)
	runGit := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = gitEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	runGit("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	toolsDir := filepath.Join(repo, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tillr-watch", "tillr-drain", "tillr-item", "tillr-run", "tillr-land"} {
		if err := os.Symlink(filepath.Join(root, "tools", name), filepath.Join(toolsDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	tillrDir := filepath.Join(toolsDir, "tillr")
	if err := os.MkdirAll(tillrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "tools", "tillr"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "examples" {
			exDir := filepath.Join(tillrDir, "examples")
			if err := os.MkdirAll(exDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := copyDirRecursive(filepath.Join(root, "tools", "tillr", "examples", "recorded-verdict"), filepath.Join(exDir, "recorded-verdict")); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.Symlink(filepath.Join(root, "tools", "tillr", e.Name()), filepath.Join(tillrDir, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}

	cfgDir := filepath.Join(repo, "config", "tillr")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyDirRecursive(filepath.Join(root, "config", "tillr", "workflows"), filepath.Join(cfgDir, "workflows")); err != nil {
		t.Fatal(err)
	}
	if err := copyDirRecursive(filepath.Join(root, "config", "tillr", "watch"), filepath.Join(cfgDir, "watch")); err != nil {
		t.Fatal(err)
	}
	for _, fName := range []string{"backend-policy.json", "commands.json"} {
		data, err := os.ReadFile(filepath.Join(root, "config", "tillr", fName))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, fName), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	runGit("add", ".")
	runGit("commit", "-qm", "chore: fixture")
	base := runGit("rev-parse", "HEAD")
	return repo, bin, base
}

func commitWorkflowChange(t *testing.T, repo, msg string) string {
	t.Helper()
	_, id := loadTestContext(t)
	personal := id[0]
	gitEnv := append(os.Environ(),
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=user.name",
		"GIT_CONFIG_VALUE_0="+personal[0],
		"GIT_CONFIG_KEY_1=user.email",
		"GIT_CONFIG_VALUE_1="+personal[1],
	)
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func runWorkflowsCheck(t *testing.T, bin, repo, base, head string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin, "workflows", base, head)
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	return code, stdout.String(), stderr.String()
}

func TestWorkflowsRegistry(t *testing.T) {
	root, _ := loadTestContext(t)
	raw, err := os.ReadFile(filepath.Join(root, "tools", "gate", "checks", "workflows.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Scope     string   `json:"scope"`
		TimeoutMS int      `json:"timeout_ms"`
		Reads     []string `json:"reads"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}
	if reg.Scope != "change" || reg.TimeoutMS != 20000 {
		t.Fatalf("unexpected registry %+v", reg)
	}
	readSet := make(map[string]bool)
	for _, r := range reg.Reads {
		readSet[r] = true
	}
	for _, req := range []string{"definition.json", "config/tillr/", "config/tillr/workflows/", "config/tillr/watch/", "tools/tillr-watch/"} {
		if !readSet[req] {
			t.Fatalf("missing %s in workflows.json reads", req)
		}
	}
}

func TestWorkflowsUnrelatedSkips(t *testing.T) {
	repo, bin, base := setupWorkflowRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("unrelated change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := commitWorkflowChange(t, repo, "docs: unrelated")
	if code, out, errOut := runWorkflowsCheck(t, bin, repo, base, head); code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestWorkflowsCleanChangePassesAllSteps(t *testing.T) {
	repo, bin, base := setupWorkflowRepo(t)
	rule := `[[rule]]
id = "test-clean-rule"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.refusal"]

[rule.when]
pattern = "count_in_window"
match = "event.kind == \"drain.refusal\""
group_by = "item_id"
threshold = 5
window = "10m"

[rule.delivery]
receiver = "foreground"

[rule.guidance]
summary = "Test clean rule for {{subject}}"
check = ["Check {{rule_id}}"]
do = ["Inspect {{subject}}"]
`
	if err := os.WriteFile(filepath.Join(repo, "config", "tillr", "watch", "test-clean.toml"), []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}
	head := commitWorkflowChange(t, repo, "feat: valid watch rule")
	if code, out, errOut := runWorkflowsCheck(t, bin, repo, base, head); code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestWorkflowsInvalidWorkflowFailsStep1(t *testing.T) {
	root, _ := loadTestContext(t)
	repo, bin, base := setupWorkflowRepo(t)
	cycleData, err := os.ReadFile(filepath.Join(root, "tools", "tillr-drain", "drain", "testdata", "workflow-check", "cycle", "check@v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(repo, "config", "tillr", "workflows", "check@v1.json")
	if err := os.WriteFile(p, cycleData, 0o644); err != nil {
		t.Fatal(err)
	}
	head := commitWorkflowChange(t, repo, "feat: cyclic workflow")
	code, out, errOut := runWorkflowsCheck(t, bin, repo, base, head)
	if code != 1 || !strings.Contains(out, p) || !strings.Contains(out, "workflow-cycle") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestWorkflowsInvalidWatchRuleFailsStep2(t *testing.T) {
	repo, bin, base := setupWorkflowRepo(t)
	badRule := `[[rule]]
id = "bad-rule"
scope = "core"
severity = "warning"

[rule.reads]
events = ["nonexistent.event.kind"]

[rule.when]
pattern = "count_in_window"
match = "event.kind == \"nonexistent.event.kind\""
group_by = "item_id"
threshold = 2
window = "5m"

[rule.delivery]
receiver = "foreground"

[rule.guidance]
summary = "Bad rule {{subject}}"
check = ["Check"]
do = ["Do"]
`
	if err := os.WriteFile(filepath.Join(repo, "config", "tillr", "watch", "bad-rule.toml"), []byte(badRule), 0o644); err != nil {
		t.Fatal(err)
	}
	head := commitWorkflowChange(t, repo, "feat: invalid watch rule")
	code, out, errOut := runWorkflowsCheck(t, bin, repo, base, head)
	if code != 1 || !strings.Contains(out, "watch-lint-") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestWorkflowsInvalidRecordedVerdictFailsStep3(t *testing.T) {
	repo, bin, base := setupWorkflowRepo(t)
	defnPath := filepath.Join(repo, "tools", "tillr", "examples", "recorded-verdict", "definition.json")
	raw, err := os.ReadFile(defnPath)
	if err != nil {
		t.Fatal(err)
	}
	var defn map[string]any
	if err := json.Unmarshal(raw, &defn); err != nil {
		t.Fatal(err)
	}
	defn["exit"] = map[string]any{"requires_candidate": "commit_required"}
	updated, _ := json.MarshalIndent(defn, "", "  ")
	if err := os.WriteFile(defnPath, append(updated, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	head := commitWorkflowChange(t, repo, "feat: invalid recorded-verdict charter")
	code, out, errOut := runWorkflowsCheck(t, bin, repo, base, head)
	if code != 1 || !strings.Contains(out, "watch-lint-") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestWorkflowsNewBackendPolicyVariantTriggersAndFails(t *testing.T) {
	repo, bin, base := setupWorkflowRepo(t)
	variant := "{\n  \"default_backend\": \"pi\",\n  \"backends\": {\"pi\": {\"adapter\": \"pi\", \"model\": \"m\"}},\n  \"seats\": {\"scout\": \"pi\"}\n}\n"
	if err := os.WriteFile(filepath.Join(repo, "config", "tillr", "backend-policy.variant.json"), []byte(variant), 0o644); err != nil {
		t.Fatal(err)
	}
	head := commitWorkflowChange(t, repo, "feat: invalid backend-policy variant")
	code, out, errOut := runWorkflowsCheck(t, bin, repo, base, head)
	if code != 1 || !strings.Contains(out, "watch-lint-seat-unmapped") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestWorkflowsRecordedVerdictCommitRequiredWorkflowFailsStep3(t *testing.T) {
	repo, bin, base := setupWorkflowRepo(t)
	wfPath := filepath.Join(repo, "tools", "tillr", "examples", "recorded-verdict", "config", "tillr", "workflows", "recorded-verdict@v1.json")
	raw, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var wf map[string]any
	if err := json.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	wf["exit"] = map[string]any{"requires_candidate": "commit_required", "requires_nodes": []string{"investigate"}}
	updated, _ := json.MarshalIndent(wf, "", "  ")
	if err := os.WriteFile(wfPath, append(updated, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	head := commitWorkflowChange(t, repo, "feat: commit_required workflow in recorded-verdict")
	code, out, errOut := runWorkflowsCheck(t, bin, repo, base, head)
	if code != 1 || !strings.Contains(out, "watch-lint-charter-candidate") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}
