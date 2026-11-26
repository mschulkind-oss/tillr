package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestRepo(t *testing.T, dir string) (string, string) {
	t.Helper()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_COUNT=2",
			"GIT_CONFIG_KEY_0=user.name",
			"GIT_CONFIG_VALUE_0=Fixture",
			"GIT_CONFIG_KEY_1=user.email",
			"GIT_CONFIG_VALUE_1=fixture@example.invalid",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-qm", "base")
	base := runGit("rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Head\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-qm", "head")
	head := runGit("rev-parse", "HEAD")
	return base, head
}

func buildTillrGateBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tillr-gate")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build tillr-gate: %v: %s", err, out)
	}
	return bin
}

func TestChangeGateProbesInstalledRunnerBeforeOptionalFlag(t *testing.T) {
	bin := buildTillrGateBinary(t)
	for _, tc := range []struct {
		supported bool
		changed   string
	}{
		{false, "README.md"},
		{true, "README.md"},
		{true, "tools/tillr-gate/main.go"},
	} {
		repo := t.TempDir()
		base, _ := initTestRepo(t, repo)

		target := filepath.Join(repo, filepath.FromSlash(tc.changed))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitEnv := append(os.Environ(),
			"GIT_CONFIG_COUNT=2",
			"GIT_CONFIG_KEY_0=user.name",
			"GIT_CONFIG_VALUE_0=Fixture",
			"GIT_CONFIG_KEY_1=user.email",
			"GIT_CONFIG_VALUE_1=fixture@example.invalid",
		)
		for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "target"}} {
			c := exec.Command("git", args...)
			c.Dir = repo
			c.Env = gitEnv
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
		headCmd := exec.Command("git", "rev-parse", "HEAD")
		headCmd.Dir = repo
		headOut, _ := headCmd.Output()
		head := strings.TrimSpace(string(headOut))

		if err := os.MkdirAll(filepath.Join(repo, "tools", "gate", "checks"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "checks", "go-build.json"), []byte(`{"reads":["tools/tillr-gate/"]}`), 0o644); err != nil {
			t.Fatal(err)
		}

		warmLog := filepath.Join(t.TempDir(), "warm.log")
		stubGate := filepath.Join(repo, "tools", "gate", "repo-gate")
		if err := os.WriteFile(stubGate, []byte("#!/bin/sh\necho warm >> "+warmLog+"\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		runnerLog := filepath.Join(t.TempDir(), "runner.log")
		helpFlag := "  -scope-to-change-later\n"
		if tc.supported {
			helpFlag = "  -scope-to-change\n    skip unrelated checks\n"
		}
		runner := filepath.Join(t.TempDir(), "runner")
		script := "#!/bin/sh\n" +
			"printf '%s\\n' \"$*\" >> " + runnerLog + "\n" +
			"if [ \"$1\" = \"checks\" ] && [ \"$2\" = \"--help\" ]; then\n" +
			"  printf 'Usage of checks:\\n  -fast-tree\\n" + strings.ReplaceAll(helpFlag, "\n", "\\n") + "' >&2\n" +
			"  exit 0\n" +
			"fi\n" +
			"if [ -n \"$TILLR_FAST_TREE_TIMING\" ]; then exit 99; fi\n" +
			"exit 0\n"
		if err := os.WriteFile(runner, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(bin, "change-gate", "--base", base, "--head", head, "--runner", runner, "--logs", filepath.Join(repo, "logs"))
		cmd.Dir = repo
		cmd.Env = append(gitEnv, "TILLR_FAST_VERDICT_OUT=")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("supported=%v changed=%s: %v: %s", tc.supported, tc.changed, err, out)
		}
		logBytes, _ := os.ReadFile(runnerLog)
		lines := strings.Split(strings.TrimSpace(string(logBytes)), "\n")
		if len(lines) != 2 || lines[0] != "checks --help" {
			t.Fatalf("runner calls = %v", lines)
		}
		if strings.Contains(lines[1], "--scope-to-change") != tc.supported {
			t.Fatalf("supported=%v line=%q", tc.supported, lines[1])
		}
		_, warmErr := os.Stat(warmLog)
		wantWarm := !tc.supported || strings.HasPrefix(tc.changed, "tools/")
		if (warmErr == nil) != wantWarm {
			t.Fatalf("supported=%v changed=%s: warmed=%v want=%v", tc.supported, tc.changed, warmErr == nil, wantWarm)
		}
	}
}

func TestChangeGateStallRecordAndExitSurviveAdapter(t *testing.T) {
	bin := buildTillrGateBinary(t)
	repo := t.TempDir()
	base, head := initTestRepo(t, repo)

	output := filepath.Join(t.TempDir(), "fast.json")
	record := `{"payload":{"result":"stalled","checks":[{"check_id":"blocked","result":"stalled","duration_ms":60000,"silent_samples":60,"cpu_time_ms":12,"last_output_line":"last"}]}}`
	runner := filepath.Join(t.TempDir(), "stub-checks")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"checks\" ] && [ \"$2\" = \"--help\" ]; then exit 0; fi\n" +
		"printf '%s\\n' '" + record + "'\n" +
		"exit 6\n"
	if err := os.WriteFile(runner, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "change-gate", "--base", base, "--head", head, "--runner", runner, "--logs", filepath.Join(repo, "logs"))
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"TILLR_FAST_VERDICT_OUT="+output,
		"TILLR_GATE_SPANS_OUT="+filepath.Join(filepath.Dir(output), "spans.json"),
		"TILLR_ORDINARY_GATE_TIMING=1",
	)
	out, err := cmd.Output()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 6 {
		t.Fatalf("expected exit 6, got err=%v out=%s", err, out)
	}
	saved, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(saved) {
		t.Fatalf("stdout != saved file")
	}
	var parsed struct {
		Payload struct {
			Result string            `json:"result"`
			Checks []json.RawMessage `json:"checks"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(saved, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Payload.Result != "stalled" || len(parsed.Payload.Checks) != 1 {
		t.Fatalf("unexpected payload: %s", saved)
	}
}

func TestTreeCheckClassificationPairsAndIdentity(t *testing.T) {
	repo := t.TempDir()
	_, head := initTestRepo(t, repo)
	checks := filepath.Join(repo, "tools", "gate", "checks")
	if err := os.MkdirAll(checks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checks, "check.json"), []byte(`{"scope":"tree","tier":"fast","id":"one"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	cases := []struct {
		code     int
		word     string
		identity string
		want     int
	}{
		{0, "pass", "one", 0},
		{1, "fail", "one", 1},
		{6, "stalled", "one", 6},
		{0, "fail", "one", 4},
		{1, "pass", "one", 4},
		{6, "error", "one", 4},
		{4, "error", "one", 4},
		{0, "PASS", "one", 4},
		{0, "ok", "one", 4},
		{1, "FAIL", "one", 4},
		{6, "STALLED", "one", 4},
		{0, "pass", "other", 4},
	}
	for _, tc := range cases {
		got := runTreeCheck([]string{"--only", "one", "--base", head, "--head", head, "--runner", "stub"}, func(runner string, args []string) (int, string, string, error) {
			payload, _ := json.Marshal(map[string]any{
				"payload": map[string]any{
					"checks": []map[string]any{
						{"check_id": tc.identity, "result": tc.word},
					},
				},
			})
			return tc.code, string(payload) + "\n", "", nil
		})
		if got != tc.want {
			t.Fatalf("code=%d word=%q id=%q: got %d, want %d", tc.code, tc.word, tc.identity, got, tc.want)
		}
	}
}

func TestChangeGateConsumerRepoWithoutGoBuild(t *testing.T) {
	bin := buildTillrGateBinary(t)
	repo := t.TempDir()
	base, head := initTestRepo(t, repo)

	checks := filepath.Join(repo, "tools", "gate", "checks")
	if err := os.MkdirAll(checks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checks, "ok.json"), []byte(`{"id":"ok","label":"Consumer check","scope":"change","tier":"fast","budget_ms":1000,"command":["true"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "change-gate", "--base", base, "--head", head, "--runner", bin, "--logs", filepath.Join(repo, "logs"))
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("change-gate failed: %v: %s", err, out)
	}
	if strings.Contains(string(out), "warming the trimmed Go build cache") {
		t.Fatalf("unexpected warm cache message: %s", out)
	}
	matches, _ := filepath.Glob(filepath.Join(repo, ".git", "tillr-gate", "go-build-warm-*"))
	if len(matches) != 0 {
		t.Fatalf("unexpected warm markers: %v", matches)
	}
}

func TestChangeGateAndTreeCheckCustomChecksDirAndInvalidSettings(t *testing.T) {
	bin := buildTillrGateBinary(t)
	repo := t.TempDir()
	base, head := initTestRepo(t, repo)

	if err := os.WriteFile(filepath.Join(repo, ".tillr.toml"), []byte("[gate]\nchecks_dir = \"custom/checks\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(repo, "custom", "checks")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "ok.json"), []byte(`{"id":"ok","label":"Custom change check","scope":"change","tier":"fast","budget_ms":1000,"command":["true"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "tree.json"), []byte(`{"id":"tree-ok","label":"Custom tree check","scope":"tree","tier":"fast","budget_ms":1000,"command":["true"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cg := exec.Command(bin, "change-gate", "--base", base, "--head", head, "--runner", bin, "--logs", filepath.Join(repo, "logs"))
	cg.Dir = repo
	if out, err := cg.CombinedOutput(); err != nil {
		t.Fatalf("change-gate custom checks_dir: %v: %s", err, out)
	}

	tc := exec.Command(bin, "tree-check", "--only", "tree-ok", "--base", base, "--head", head, "--runner", bin)
	tc.Dir = repo
	if out, err := tc.CombinedOutput(); err != nil {
		t.Fatalf("tree-check custom checks_dir: %v: %s", err, out)
	}

	override := filepath.Join(repo, "override", "checks")
	if err := os.MkdirAll(override, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(override, "over.json"), []byte(`{"id":"over","label":"Override check","scope":"tree","tier":"fast","budget_ms":1000,"command":["true"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cgOver := exec.Command(bin, "change-gate", "--base", base, "--head", head, "--checks", "override/checks", "--runner", bin, "--logs", filepath.Join(repo, "logs-over"))
	cgOver.Dir = repo
	if out, err := cgOver.CombinedOutput(); err != nil {
		t.Fatalf("change-gate --checks override: %v: %s", err, out)
	}

	tcOver := exec.Command(bin, "tree-check", "--only", "over", "--base", base, "--head", head, "--checks", "override/checks", "--runner", bin)
	tcOver.Dir = repo
	if out, err := tcOver.CombinedOutput(); err != nil {
		t.Fatalf("tree-check --checks override: %v: %s", err, out)
	}

	if err := os.WriteFile(filepath.Join(repo, ".tillr.toml"), []byte("[gate]\nchecks_dir = \"../escape\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"change-gate", "--base", base, "--head", head, "--runner", bin, "--logs", filepath.Join(repo, "logs-bad")},
		{"tree-check", "--only", "tree-ok", "--base", base, "--head", head, "--runner", bin},
	} {
		bad := exec.Command(bin, args...)
		bad.Dir = repo
		out, err := bad.CombinedOutput()
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 4 {
			t.Fatalf("%v: expected exit 4, got err=%v out=%s", args, err, out)
		}
	}
}
