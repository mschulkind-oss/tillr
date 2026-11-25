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

type doneFixture struct {
	t      *testing.T
	bin    string
	repo   string
	log    string
	runner string
	env    []string
	first  string
	second string
	head   string
}

func newDoneFixture(t *testing.T) *doneFixture {
	t.Helper()
	bin := buildRepoGateForTest(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(root, "runner.log")
	runner := filepath.Join(root, "runner")
	runnerScript := `#!/usr/bin/env python3
import json, os, sys
with open(os.environ["RUNNER_LOG"], "a") as log:
    log.write(json.dumps(sys.argv[1:]) + "\n")
kind = "fast" if "--fast-tree" in sys.argv else "heavy"
answer = json.loads(os.environ.get("RUNNER_" + kind.upper(), '{"code": 0, "checks": [["one", "pass"]]}'))
if answer.get("silent"):
    sys.exit(answer["code"])
print(json.dumps({"payload": {"checks": [
    {"check_id": ident, "result": result, "log_ref": "logs/" + ident + ".log"}
    for ident, result in answer["checks"]]}}))
sys.exit(answer["code"])
`
	if err := os.WriteFile(runner, []byte(runnerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	gitconfig := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[user]\n name = fixture\n email = fixture@example.invalid\n[init]\n defaultBranch = main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"RUNNER_LOG="+logFile,
		"GIT_CONFIG_GLOBAL="+gitconfig,
		"GIT_CONFIG_NOSYSTEM=1",
	)
	f := &doneFixture{t: t, bin: bin, repo: repo, log: logFile, runner: runner, env: env}
	f.git("init", "-q")
	f.first = f.commit("first")
	f.second = f.commit("second")
	f.head = f.commit("third")
	return f
}

func (f *doneFixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = f.repo
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *doneFixture) commit(text string) string {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.repo, "file.txt"), []byte(text+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.git("add", "file.txt")
	f.git("commit", "-q", "-m", text)
	return f.git("rev-parse", "HEAD")
}

func (f *doneFixture) runDone(answers map[string]any, args ...string) (int, string, string, [][]string) {
	f.t.Helper()
	_ = os.WriteFile(f.log, nil, 0o644)
	env := append([]string(nil), f.env...)
	for k, v := range answers {
		raw, _ := json.Marshal(v)
		env = append(env, "RUNNER_"+strings.ToUpper(k)+"="+string(raw))
	}
	fullArgs := append([]string{"done", "--runner", f.runner}, args...)
	cmd := exec.Command(f.bin, fullArgs...)
	cmd.Dir = f.repo
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			f.t.Fatal(err)
		}
	}
	logBytes, _ := os.ReadFile(f.log)
	var calls [][]string
	for _, line := range splitLines(string(logBytes)) {
		var c []string
		if json.Unmarshal([]byte(line), &c) == nil {
			calls = append(calls, c)
		}
	}
	return code, stdout.String(), stderr.String(), calls
}

func flagVal(call []string, flag string) string {
	for i := 0; i < len(call)-1; i++ {
		if call[i] == flag {
			return call[i+1]
		}
	}
	return ""
}

func TestDoneTwoPassesRunInOrderAndCleanTreePasses(t *testing.T) {
	f := newDoneFixture(t)
	code, out, errOut, calls := f.runDone(nil)
	if code != 0 || len(calls) != 2 {
		t.Fatalf("code=%d calls=%v out=%s err=%s", code, calls, out, errOut)
	}
	if calls[0][0] != "checks" || calls[0][1] != "--fast-tree" || calls[1][0] != "checks" || calls[1][1] != "--heavy-tree" {
		t.Fatalf("calls=%v", calls)
	}
	if flagVal(calls[0], "--base") != f.second || flagVal(calls[0], "--head") != f.head {
		t.Fatalf("fast range: %v", calls[0])
	}
	if flagVal(calls[1], "--base") != f.head || flagVal(calls[1], "--head") != f.head {
		t.Fatalf("heavy range: %v", calls[1])
	}
	if flagVal(calls[0], "--checks") != "tools/gate/checks" || !strings.HasSuffix(strings.TrimSpace(out), "done: PASSED") {
		t.Fatalf("out=%s", out)
	}
}

func TestDoneUncommittedChangesRefusedBeforeAnyCheck(t *testing.T) {
	f := newDoneFixture(t)
	if err := os.WriteFile(filepath.Join(f.repo, "file.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut, calls := f.runDone(nil)
	if code != 4 || len(calls) != 0 || !strings.Contains(errOut, "uncommitted changes") {
		t.Fatalf("code=%d calls=%v err=%s", code, calls, errOut)
	}
}

func TestDoneFailedCheckNamedWithLogAndHeavyStillRuns(t *testing.T) {
	f := newDoneFixture(t)
	code, out, errOut, calls := f.runDone(map[string]any{
		"fast": map[string]any{"code": 1, "checks": [][]string{{"one", "pass"}, {"two", "fail"}}},
	})
	if code != 1 || len(calls) != 2 || !strings.Contains(out, "FAIL two: fail  log: logs/two.log") || !strings.Contains(out, "ok   one: pass") || !strings.HasSuffix(strings.TrimSpace(out), "done: FAILED") {
		t.Fatalf("code=%d calls=%v out=%s err=%s", code, calls, out, errOut)
	}
	code, out, _, _ = f.runDone(map[string]any{
		"heavy": map[string]any{"code": 0, "checks": [][]string{{"three", "stalled"}}},
	})
	if code != 1 {
		t.Fatalf("expected fail on stalled word, got %d out=%s", code, out)
	}
}

func TestDoneSkippedAndRememberedCountAsPassed(t *testing.T) {
	f := newDoneFixture(t)
	code, out, _, _ := f.runDone(map[string]any{
		"heavy": map[string]any{"code": 0, "checks": [][]string{{"a", "skipped: no changed path is read"}, {"b", "remembered pass"}}},
	})
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestDoneNoVerdictIsCouldNotJudgeAndStopsBeforeHeavy(t *testing.T) {
	f := newDoneFixture(t)
	for _, ans := range []map[string]any{
		{"code": 4, "silent": true},
		{"code": 4, "checks": [][]string{{"one", "pass"}}},
	} {
		code, out, _, calls := f.runDone(map[string]any{"fast": ans})
		if code != 4 || len(calls) != 1 || !strings.HasSuffix(strings.TrimSpace(out), "done: COULD NOT JUDGE") {
			t.Fatalf("ans=%v: code=%d calls=%v out=%s", ans, code, calls, out)
		}
	}
}

func TestDoneFastAndOnlyEachRunOnePass(t *testing.T) {
	f := newDoneFixture(t)
	code, _, _, calls := f.runDone(nil, "--fast")
	if code != 0 || len(calls) != 1 || calls[0][1] != "--fast-tree" {
		t.Fatalf("fast: code=%d calls=%v", code, calls)
	}
	code, _, _, calls = f.runDone(nil, "--only", "go-test-example")
	if code != 0 || len(calls) != 1 || calls[0][1] != "--heavy-tree" || flagVal(calls[0], "--only") != "go-test-example" {
		t.Fatalf("only: code=%d calls=%v", code, calls)
	}
}

func TestDoneBaseIsSinceThenProvedRefWhenHeadDescends(t *testing.T) {
	f := newDoneFixture(t)
	f.git("update-ref", "refs/tillr/green", f.first)
	_, _, _, calls := f.runDone(nil, "--fast")
	if flagVal(calls[0], "--base") != f.first {
		t.Fatalf("expected green base %s, got %s", f.first, flagVal(calls[0], "--base"))
	}
	_, _, _, calls = f.runDone(nil, "--fast", "--since", f.second)
	if flagVal(calls[0], "--base") != f.second {
		t.Fatalf("expected --since base %s, got %s", f.second, flagVal(calls[0], "--base"))
	}
	f.git("checkout", "-q", "--orphan", "other")
	other := f.commit("unrelated")
	f.git("checkout", "-q", "main")
	f.git("update-ref", "refs/tillr/green", other)
	_, _, _, calls = f.runDone(nil, "--fast")
	if flagVal(calls[0], "--base") != f.second {
		t.Fatalf("expected fallback to parent %s, got %s", f.second, flagVal(calls[0], "--base"))
	}
}
