package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/checkresult"
)

func TestChangeGateProbesInstalledRunnerBeforeOptionalFlag(t *testing.T) {
	bin := buildRepoGateForTest(t)
	_, id := loadTestContext(t)
	personal := id[0]

	for _, tc := range []struct {
		supported bool
		changed   string
	}{
		{false, "README.md"},
		{true, "README.md"},
		{true, "tools/tillr-gate/main.go"},
	} {
		repo := t.TempDir()
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
		if err := os.MkdirAll(filepath.Join(repo, "tools", "gate", "checks"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "checks", "go-build.json"), []byte(`{"reads":["tools/tillr-gate/"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit("add", ".")
		runGit("commit", "-qm", "base")
		base := runGit("rev-parse", "HEAD")

		target := filepath.Join(repo, filepath.FromSlash(tc.changed))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit("add", ".")
		runGit("commit", "-qm", "head")
		head := runGit("rev-parse", "HEAD")

		runnerArgsLog := filepath.Join(t.TempDir(), "runner-args.log")
		runner := filepath.Join(t.TempDir(), "runner")
		helpFlag := "  -scope-to-change-later\n"
		if tc.supported {
			helpFlag = "  -scope-to-change\n    skip unrelated checks\n"
		}
		script := "#!/usr/bin/env python3\nimport os, sys\n" +
			"with open(os.environ['RUNNER_LOG'], 'a') as f: f.write(' '.join(sys.argv[1:]) + '\\n')\n" +
			"if sys.argv[1:] == ['checks', '--help']:\n" +
			"    sys.stderr.write('Usage of checks:\\n  -fast-tree\\n" + strings.ReplaceAll(helpFlag, "\n", "\\n") + "')\n" +
			"    sys.exit(0)\n" +
			"assert not os.environ.get('TILLR_FAST_TREE_TIMING')\n"
		if err := os.WriteFile(runner, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}

		// Stub tools/gate/repo-gate in repo so warmTrimmedGoCache doesn't run real go-build.
		warmLog := filepath.Join(t.TempDir(), "warm.log")
		stubGate := filepath.Join(repo, "tools", "gate", "repo-gate")
		if err := os.WriteFile(stubGate, []byte("#!/bin/sh\necho warm >> "+warmLog+"\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(bin, "change-gate", "--base", base, "--head", head, "--runner", runner, "--logs", filepath.Join(repo, "logs"))
		cmd.Dir = repo
		cmd.Env = append(gitEnv, "RUNNER_LOG="+runnerArgsLog, "TILLR_FAST_VERDICT_OUT=")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("supported=%v changed=%s: %v: %s", tc.supported, tc.changed, err, out)
		}
		logBytes, _ := os.ReadFile(runnerArgsLog)
		lines := splitLines(string(logBytes))
		if len(lines) != 2 || lines[0] != "checks --help" {
			t.Fatalf("runner calls = %v", lines)
		}
		hasScope := strings.Contains(lines[1], "--scope-to-change")
		if hasScope != tc.supported {
			t.Fatalf("supported=%v: hasScope=%v line=%q", tc.supported, hasScope, lines[1])
		}
		_, warmErr := os.Stat(warmLog)
		wantWarm := !tc.supported || strings.HasPrefix(tc.changed, "tools/")
		if (warmErr == nil) != wantWarm {
			t.Fatalf("supported=%v changed=%s: warmed=%v want=%v", tc.supported, tc.changed, warmErr == nil, wantWarm)
		}
	}
}

func TestChangeGateStallRecordAndExitSurviveAdapter(t *testing.T) {
	bin := buildRepoGateForTest(t)
	_, id := loadTestContext(t)
	personal := id[0]
	repo := t.TempDir()
	gitEnv := append(os.Environ(),
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=user.name",
		"GIT_CONFIG_VALUE_0="+personal[0],
		"GIT_CONFIG_KEY_1=user.email",
		"GIT_CONFIG_VALUE_1="+personal[1],
	)
	for _, args := range [][]string{
		{"init", "-q"},
		{"commit", "--allow-empty", "-qm", "base"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = repo
	headOut, _ := headCmd.Output()
	head := strings.TrimSpace(string(headOut))

	output := filepath.Join(t.TempDir(), "fast.json")
	record := `{"payload":{"result":"stalled","checks":[{"check_id":"blocked","result":"stalled","duration_ms":60000,"silent_samples":60,"cpu_time_ms":12,"last_output_line":"last"}]}}` + "\n"
	runner := filepath.Join(t.TempDir(), "stub-checks")
	script := "#!/usr/bin/env python3\nimport sys\nif sys.argv[1:] == ['checks', '--help']:\n    sys.exit(0)\nsys.stdout.write(" + `'` + record[:len(record)-1] + `\n'` + ")\nsys.exit(6)\n"
	if err := os.WriteFile(runner, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "tools", "gate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "repo-gate"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "change-gate", "--base", head, "--head", head, "--runner", runner, "--logs", filepath.Join(repo, "logs"))
	cmd.Dir = repo
	cmd.Env = append(gitEnv,
		"TILLR_FAST_VERDICT_OUT="+output,
		"TILLR_GATE_SPANS_OUT="+filepath.Join(filepath.Dir(output), "spans.json"),
		"TILLR_ORDINARY_GATE_TIMING=1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 6 {
		t.Fatalf("expected exit 6, got err=%v stderr=%s", err, stderr.String())
	}
	written, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != string(written) {
		t.Fatalf("stdout != written file")
	}
	var parsed struct {
		Payload struct {
			Result string `json:"result"`
			Checks []struct {
				CheckID        string `json:"check_id"`
				Result         string `json:"result"`
				DurationMS     int    `json:"duration_ms"`
				SilentSamples  int    `json:"silent_samples"`
				CPUTimeMS      int    `json:"cpu_time_ms"`
				LastOutputLine string `json:"last_output_line"`
			} `json:"checks"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(written, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Payload.Result != "stalled" || len(parsed.Payload.Checks) != 1 || parsed.Payload.Checks[0].CheckID != "blocked" {
		t.Fatalf("unexpected payload: %+v", parsed)
	}
}

func TestTreeCheckClassifiesExitAndWordPairAndIdentity(t *testing.T) {
	root := t.TempDir()
	origWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	checksDir := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checksDir, "check.json"), []byte(`{"scope":"tree","tier":"fast","id":"one"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_DURABLE_DIR", root)

	cases := []struct {
		code     int
		word     string
		identity string
		expected int
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
		payload, _ := json.Marshal(map[string]any{
			"payload": map[string]any{
				"checks": []map[string]any{
					{"check_id": tc.identity, "result": tc.word},
				},
			},
		})
		stub := func(runner string, args []string) (int, string, string, error) {
			return tc.code, string(payload), "", nil
		}
		got := runTreeCheck([]string{"--only", "one", "--base", "base", "--runner", "stub"}, stub)
		if got != tc.expected {
			t.Fatalf("code=%d word=%q id=%q: got %d want %d", tc.code, tc.word, tc.identity, got, tc.expected)
		}
	}
}

func TestTreeCheckRejectsMissingOrInvalidRunnerOutput(t *testing.T) {
	root := t.TempDir()
	origWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	checksDir := filepath.Join(root, "tools", "gate", "checks")
	_ = os.MkdirAll(checksDir, 0o755)
	_ = os.WriteFile(filepath.Join(checksDir, "check.json"), []byte(`{"scope":"tree","tier":"fast","id":"one"}`), 0o644)
	t.Setenv("YOLO_DURABLE_DIR", root)

	for _, badOut := range []string{"", "not json", `{"payload":{"checks":[]}}`, `{"payload":{"checks":[{"check_id":"one","result":""}]}}`} {
		stub := func(runner string, args []string) (int, string, string, error) {
			return 0, badOut, "", nil
		}
		if got := runTreeCheck([]string{"--only", "one", "--base", "base", "--runner", "stub"}, stub); got != 4 {
			t.Fatalf("badOut=%q: got %d want 4", badOut, got)
		}
	}
}

func TestChangeCommonCachedRecordAndUnencodableWord(t *testing.T) {
	store := t.TempDir()
	_ = os.WriteFile(filepath.Join(store, "key.pass.json"), []byte(`{"result":"pass","time":"now"}`), 0o644)
	_ = os.WriteFile(filepath.Join(store, "key.fail.json"), []byte(`{"result":"pass","time":"now"}`), 0o644)
	if readStoredResult(store, "key", "fail", "") != nil {
		t.Fatalf("expected fail suffix with pass word to be rejected")
	}
	if readStoredResult(store, "key", "pass", "") == nil {
		t.Fatalf("expected valid pass record to be accepted")
	}

	// Escaped lone surrogate in JSON decodes to U+FFFD in Go, which is not "pass".
	_ = os.WriteFile(filepath.Join(store, "surrogate.pass.json"), []byte(`{"result":"\ud800","time":"now","tool":"1"}`), 0o644)
	if readStoredResult(store, "surrogate", "pass", "") != nil {
		t.Fatalf("expected surrogate record to be rejected")
	}
}

func TestChangeCommonStoredResultSendsRecordedCheckID(t *testing.T) {
	store := t.TempDir()
	writeStoredResult(store, "mod", "pass", 3, "tree", "go-build-module-1", "go-build")
	rec := readStoredResult(store, "mod", "pass", "")
	if rec == nil || rec.CheckID != "go-build" {
		t.Fatalf("expected go-build check_id, got %+v", rec)
	}
}

func TestCheckResultClassifyProtocolsForGate(t *testing.T) {
	zero, one, four := 0, 1, 4
	if r := checkresult.Classify(checkresult.Observation{Check: "go-build", Source: checkresult.JudgingProgram, ExitCode: &zero}); r.Outcome != checkresult.Pass {
		t.Fatalf("judging-program 0: %+v", r)
	}
	if r := checkresult.Classify(checkresult.Observation{Check: "go-build", Source: checkresult.GoModuleBuild, ExitCode: &four}); r.Outcome != checkresult.Fail {
		t.Fatalf("go-module-build 4: %+v", r)
	}
	if r := checkresult.Classify(checkresult.Observation{Check: "documents", Source: checkresult.DocumentRender, ExitCode: &zero, ReportRead: true, HasFinding: false}); r.Outcome != checkresult.Pass {
		t.Fatalf("document-render 0: %+v", r)
	}
	if r := checkresult.Classify(checkresult.Observation{Check: "documents", Source: checkresult.DocumentRender, ExitCode: &one, ReportRead: true, HasFinding: true}); r.Outcome != checkresult.Fail {
		t.Fatalf("document-render 1: %+v", r)
	}
}
