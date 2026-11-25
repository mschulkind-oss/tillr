package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildTillrLoopBin(t *testing.T, outPath string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", outPath, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, string(out))
	}
}

func resolveSymlinks(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return r
}

func runGitMain(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func TestMainBinaryDescribeAndCheckArgv(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "tillr-loop")
	buildTillrLoopBin(t, bin)

	out, err := exec.Command(bin, "describe").Output()
	if err != nil {
		t.Fatalf("describe failed: %v", err)
	}
	var doc struct {
		Hooks map[string][]string `json:"hooks"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("invalid describe JSON: %v\n%s", err, string(out))
	}
	for _, h := range []string{"gate", "land", "verdict", "fixer-gate", "replay", "rolling"} {
		if _, ok := doc.Hooks[h]; !ok {
			t.Fatalf("describe missing hook %s: %v", h, doc.Hooks)
		}
	}

	cmd := exec.Command(bin, "gate", "--check-argv",
		"--item", "item-1", "--lane", "lane-1", "--run", "run-1",
		"--worktree", "/tmp/wt", "--base", "abc", "--head", "def")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gate --check-argv failed: %v\n%s", err, string(out))
	}
}

func TestMainAdapterBuildIdentification(t *testing.T) {
	tmp := resolveSymlinks(t, t.TempDir())
	bin := filepath.Join(tmp, "tillr-loop")
	buildTillrLoopBin(t, bin)
	binBytes, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	binSum := sha256.Sum256(binBytes)
	binDigest := hex.EncodeToString(binSum[:])

	// Set up a minimal repo with a candidate and a missing-claim_class tillr-item stub.
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	ident := []string{
		"GIT_AUTHOR_NAME=tillr loop test",
		"GIT_AUTHOR_EMAIL=tillr-loop@example.invalid",
		"GIT_COMMITTER_NAME=tillr loop test",
		"GIT_COMMITTER_EMAIL=tillr-loop@example.invalid",
	}
	runGitMain(t, repo, ident, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitMain(t, repo, ident, "add", "file.txt")
	runGitMain(t, repo, ident, "commit", "-q", "-m", "base")
	base := runGitMain(t, repo, nil, "rev-parse", "HEAD")

	stubDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	itemShow := filepath.Join(tmp, "item-show.json")
	showDoc := map[string]any{
		"record_version": 1,
		"kind":           "item.record",
		"id":             "item-1",
		"payload": map[string]any{
			"item_id": "item-1",
			"title":   "t",
			"state":   "ready",
			"criteria": []map[string]any{
				{"criterion_id": "c1", "text": "missing class", "state": "pending"},
			},
		},
	}
	showBytes, _ := json.Marshal(showDoc)
	if err := os.WriteFile(itemShow, showBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	stubItem := "#!/bin/sh\nif [ \"$1\" = show ]; then cat " + itemShow + "; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(stubDir, "tillr-item"), []byte(stubItem), 0o755); err != nil {
		t.Fatal(err)
	}

	workDir := filepath.Join(tmp, "work")
	runDir := filepath.Join(workDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "gate.verdict.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TILLR_LOOP_ADAPTER_BUILD", "")
	_ = os.Unsetenv("TILLR_LOOP_ADAPTER_BUILD")
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	runVerdict := func(exe string) (int, string) {
		t.Helper()
		cmd := exec.Command(exe, "verdict",
			"--item", "item-1", "--lane", "lane-1", "--run", "run-1",
			"--worktree", repo, "--base", base, "--head", base,
			"--root", repo, "--store", filepath.Join(tmp, "state.db"),
			"--work-dir", workDir, "--now", "2026-01-01T00:00:00Z",
			"--verdict", "pass",
		)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return 0, string(out)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), string(out)
		}
		t.Fatal(err)
		return -1, ""
	}

	// Untracked binary names its path and digest.
	code, out := runVerdict(bin)
	if code != 4 || !strings.Contains(out, bin) || !strings.Contains(out, binDigest) {
		t.Fatalf("untracked binary build name mismatch: code=%d out=%s", code, out)
	}

	// Clean tracked binary in a git repo names its revision.
	trackedRepo := filepath.Join(tmp, "tracked")
	if err := os.MkdirAll(trackedRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitMain(t, trackedRepo, ident, "init", "-q", "-b", "main")
	trackedBin := filepath.Join(trackedRepo, "tillr-loop")
	if err := os.WriteFile(trackedBin, binBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitMain(t, trackedRepo, ident, "add", "tillr-loop")
	runGitMain(t, trackedRepo, ident, "commit", "-q", "-m", "tracked adapter")
	rev := runGitMain(t, trackedRepo, nil, "rev-parse", "HEAD")

	code, out = runVerdict(trackedBin)
	if code != 4 || !strings.Contains(out, "revision "+rev) {
		t.Fatalf("tracked binary build name mismatch: code=%d rev=%s out=%s", code, rev, out)
	}
}
