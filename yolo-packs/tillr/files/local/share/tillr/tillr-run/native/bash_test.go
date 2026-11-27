package native

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
)

func initTestGitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "Test User"},
		{"config", "user.email", "test@example.com"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = cleanGitEnvForTest()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "-A"},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = cleanGitEnvForTest()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
}

func cleanGitEnvForTest() []string {
	env := os.Environ()
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE"} {
		env = removeEnvKey(env, key)
	}
	return env
}

func TestBashScrubsProviderSecretsAndKeepsTillrMarkers(t *testing.T) {
	env := newTestFenceEnv(t, []string{"tools/tillr-run/native"})
	baseEnv := append([]string(nil), os.Environ()...)
	baseEnv = append(baseEnv,
		"AWS_ACCESS_KEY_ID=AKIA_TEST_SECRET",
		"AWS_ACCESS_KEY_ID_ALT=AKIA_ALT",
		"AWS_SECRET_ACCESS_KEY=super-secret-key",
		"AWS_SESSION_TOKEN=session-tok",
		"AWS_BEARER_TOKEN_BEDROCK=bedrock-tok",
		"GITHUB_TOKEN=ghp_test_token",
		"GH_TOKEN=gho_test_token",
		"COPILOT_AUTH_FILE=/tmp/copilot-auth.json",
		"ANTHROPIC_API_KEY=sk-ant-test",
		"OPENAI_API_KEY_WORK=sk-openai-test",
		"CUSTOM_CLIENT_SECRET=my-secret",
		"PROVIDER_AUTH_TOKEN_V2=my-token",
		"TILLR_RUN_ID=run-secret-check-1",
		"TILLR_SEAT=build",
		"TILLR_ACCESS=write",
		"SAFE_APP_ENV=preserved-value",
	)

	ts := NewToolsetWithBash(env.policy(false, false), BashConfig{
		BaseEnv:    baseEnv,
		NonLanding: true,
	})

	out, isErr := mustCall(t, ts, "bash", map[string]any{
		"command": "env",
	})
	if isErr {
		t.Fatalf("bash env failed: %s", out)
	}

	forbidden := []string{
		"AWS_ACCESS_KEY_ID=",
		"AWS_ACCESS_KEY_ID_ALT=",
		"AWS_SECRET_ACCESS_KEY=",
		"AWS_SESSION_TOKEN=",
		"AWS_BEARER_TOKEN_BEDROCK=",
		"GITHUB_TOKEN=",
		"GH_TOKEN=",
		"COPILOT_AUTH_FILE=",
		"ANTHROPIC_API_KEY=",
		"OPENAI_API_KEY_WORK=",
		"CUSTOM_CLIENT_SECRET=",
		"PROVIDER_AUTH_TOKEN_V2=",
		"AKIA_TEST_SECRET",
		"super-secret-key",
		"ghp_test_token",
		"sk-ant-test",
	}
	for _, needle := range forbidden {
		if strings.Contains(out, needle) {
			t.Errorf("secret %q leaked into bash child env:\n%s", needle, out)
		}
	}

	required := []string{
		"TILLR_RUN_ID=run-secret-check-1",
		"TILLR_SEAT=build",
		"TILLR_ACCESS=write",
		"TILLR_RUN_EXEC_ID=",
		"SAFE_APP_ENV=preserved-value",
	}
	for _, needle := range required {
		if !strings.Contains(out, needle) {
			t.Errorf("expected non-secret marker %q in bash child env, got:\n%s", needle, out)
		}
	}
}

func TestBashReapsBackgroundGrandchildrenViaExecID(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("grandchild reaping via /proc TILLR_RUN_EXEC_ID runs on Linux")
	}
	env := newTestFenceEnv(t, []string{"tools/tillr-run/native"})
	pidFile := filepath.Join(env.durableDir, "bg.pid")

	ts := NewToolsetWithBash(env.policy(false, false), BashConfig{
		NonLanding: true,
	})

	// Launch a detached background grandchild (setsid/nohup-style, new session
	// or background subshell) and print done immediately. Even with stdout/stderr
	// redirected to /dev/null, the grandchild carries TILLR_RUN_EXEC_ID and must
	// be reaped when bash finishes.
	cmd := "sh -c 'sleep 60 </dev/null >/dev/null 2>&1 & echo $! > " + pidFile + "' && echo parent-done"
	out, isErr := mustCall(t, ts, "bash", map[string]any{
		"command": cmd,
	})
	if isErr {
		t.Fatalf("bash failed: %s", out)
	}
	if !strings.Contains(out, "parent-done") {
		t.Fatalf("expected parent-done in output, got: %q", out)
	}

	rawPID, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("reading bg.pid: %v", err)
	}
	bgPID, err := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if err != nil || bgPID <= 1 {
		t.Fatalf("invalid background pid %q: %v", string(rawPID), err)
	}

	// Verify the grandchild is gone (ESRCH or zombie already reaped).
	const hangGuard = 5 * time.Minute
	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		err := syscall.Kill(bgPID, 0)
		if err == syscall.ESRCH {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(bgPID, syscall.SIGKILL)
	t.Fatalf("background grandchild pid %d survived bash completion", bgPID)
}

func TestBashReadOnlyMountNSBlocksWorkDirWithEROFSAndKeepsWritableDirs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("CLONE_NEWNS read-only mount test runs on Linux")
	}
	if !run.CanUseReadOnlyMountNS() {
		t.Skip("unprivileged CLONE_NEWNS not permitted in this environment")
	}

	// Place workDir, durableDir, and artifactsDir all under repoRoot (matching
	// production layout where repoRoot is mounted read-only and durableDir +
	// artifactsDir are re-mounted read-write).
	base := t.TempDir()
	repoRoot := filepath.Join(base, "repo")
	laneDir := filepath.Join(repoRoot, ".tillr", "lanes", "lane-1")
	workDir := filepath.Join(laneDir, "worktree")
	durableDir := filepath.Join(laneDir, "scratch", "run-ro-1")
	artifactsDir := filepath.Join(repoRoot, ".tillr", "loop", "run-ro-1", "artifacts")
	traceDir := filepath.Join(repoRoot, ".tillr", "loop", "run-ro-1")
	for _, d := range []string{workDir, durableDir, artifactsDir, traceDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workDir, "existing.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	policy := FencePolicy{
		WorkDir:      workDir,
		ArtifactDir:  laneDir,
		ArtifactsDir: artifactsDir,
		DurableDir:   durableDir,
		ReadOnly:     true,
	}
	ts := NewToolsetWithBash(policy, BashConfig{
		RepoRoot:   repoRoot,
		TraceDir:   traceDir,
		NonLanding: true,
	})

	// 1. Writing into <workDir> must fail at the kernel with EROFS ("Read-only file system").
	out, isErr := mustCall(t, ts, "bash", map[string]any{
		"command": "echo nope > should-not-exist.txt",
	})
	if !isErr {
		t.Fatalf("expected write into read_only workDir to fail, got success: %s", out)
	}
	if !strings.Contains(strings.ToLower(out), "read-only file system") {
		t.Errorf("expected EROFS ('Read-only file system') error, got: %s", out)
	}
	if _, err := os.Stat(filepath.Join(workDir, "should-not-exist.txt")); !os.IsNotExist(err) {
		t.Fatalf("file should not exist in workDir, stat err=%v", err)
	}

	// 2. Writing into /tmp, scratch (durableDir), and <artifacts>/ must succeed.
	tmpFile := filepath.Join(os.TempDir(), "tillr-native-ro-test-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	t.Cleanup(func() { _ = os.Remove(tmpFile) })
	scratchFile := filepath.Join(durableDir, "note.txt")
	artifactFile := filepath.Join(artifactsDir, "finding.txt")

	cmd := "printf 'tmp-ok' > " + tmpFile +
		" && printf 'scratch-ok' > " + scratchFile +
		" && printf 'artifact-ok' > " + artifactFile +
		" && cat existing.txt"
	out, isErr = mustCall(t, ts, "bash", map[string]any{
		"command": cmd,
	})
	if isErr {
		t.Fatalf("expected writes to /tmp, scratch, and artifacts to succeed, got error: %s", out)
	}
	if !strings.Contains(out, "keep") {
		t.Errorf("expected existing.txt content in output, got: %q", out)
	}
	for path, want := range map[string]string{
		tmpFile:      "tmp-ok",
		scratchFile:  "scratch-ok",
		artifactFile: "artifact-ok",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("reading %s: %v", path, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, string(got), want)
		}
	}
}

func TestBashPlanQuickWayUnlocksWorkDirAfterDoItNow(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("CLONE_NEWNS read-only mount test runs on Linux")
	}
	if !run.CanUseReadOnlyMountNS() {
		t.Skip("unprivileged CLONE_NEWNS not permitted in this environment")
	}

	env := newTestFenceEnv(t, []string{"allowed"})
	if err := os.MkdirAll(filepath.Join(env.workDir, "allowed"), 0o755); err != nil {
		t.Fatal(err)
	}

	ts := NewToolsetWithBash(env.policy(true, true), BashConfig{
		NonLanding: true,
	})

	// Before do-it-now, bash cannot write to workDir.
	out, isErr := mustCall(t, ts, "bash", map[string]any{
		"command": "echo blocked > allowed/file.txt",
	})
	if !isErr || !strings.Contains(strings.ToLower(out), "read-only file system") {
		t.Fatalf("expected EROFS before do-it-now, got isErr=%v out=%q", isErr, out)
	}

	// Write artifacts/do-it-now using write_file, unlocking workDir writes.
	out, isErr = mustCall(t, ts, "write_file", map[string]any{
		"path":    filepath.Join(env.artifactsDir, "do-it-now"),
		"content": "quick-way\n",
	})
	if isErr {
		t.Fatalf("writing do-it-now failed: %s", out)
	}

	// Now bash can write into workDir.
	out, isErr = mustCall(t, ts, "bash", map[string]any{
		"command": "echo unlocked > allowed/file.txt",
	})
	if isErr {
		t.Fatalf("expected write after do-it-now to succeed, got: %s", out)
	}
	got, err := os.ReadFile(filepath.Join(env.workDir, "allowed", "file.txt"))
	if err != nil || strings.TrimSpace(string(got)) != "unlocked" {
		t.Fatalf("allowed/file.txt = %q (err=%v), want unlocked", string(got), err)
	}
}

func TestBashReadOnlyFallbackUsesTriageGuard(t *testing.T) {
	env := newTestFenceEnv(t, []string{"allowed"})
	// Set TMPDIR to a sibling directory outside workDir so triage-guard's
	// TMPDIR allowlist does not cover workDir.
	siblingTmp := filepath.Join(env.root, "tmp")
	if err := os.MkdirAll(siblingTmp, 0o755); err != nil {
		t.Fatal(err)
	}
	baseEnv := setEnvKey(os.Environ(), "TMPDIR", siblingTmp)
	ts := NewToolsetWithBash(env.policy(true, false), BashConfig{
		BaseEnv:        baseEnv,
		NonLanding:     true,
		DisableMountNS: true,
	})

	out, isErr := mustCall(t, ts, "bash", map[string]any{
		"command": "touch should-be-blocked.txt",
	})
	if !isErr {
		t.Fatalf("expected triage guard fallback to refuse touch in workDir, got success: %s", out)
	}
	if !strings.Contains(out, "triage boundary") {
		t.Errorf("expected triage boundary refusal message, got: %s", out)
	}
}

func TestBashWriterWarnsOnOutOfFenceDirtyFiles(t *testing.T) {
	env := newTestFenceEnv(t, []string{"allowed"})
	initTestGitRepo(t, env.workDir)

	ts := NewToolsetWithBash(env.policy(false, false), BashConfig{})

	// 1. Dirtying an in-fence file produces no out-of-fence warning.
	out, isErr := mustCall(t, ts, "bash", map[string]any{
		"command": "mkdir -p allowed && echo ok > allowed/in-fence.txt",
	})
	if isErr {
		t.Fatalf("in-fence bash failed: %s", out)
	}
	if strings.Contains(out, "WARNING: out-of-fence") {
		t.Errorf("unexpected out-of-fence warning for in-fence file:\n%s", out)
	}

	// 2. Dirtying an out-of-fence file appends a prominent warning naming the file.
	out, isErr = mustCall(t, ts, "bash", map[string]any{
		"command": "mkdir -p stray && echo oops > stray/outside.txt",
	})
	if isErr {
		t.Fatalf("bash failed: %s", out)
	}
	if !strings.Contains(out, "WARNING: out-of-fence dirty files detected after bash command:") {
		t.Fatalf("expected out-of-fence warning in output, got:\n%s", out)
	}
	if !strings.Contains(out, "stray/outside.txt") {
		t.Errorf("expected stray/outside.txt in warning, got:\n%s", out)
	}
	if strings.Contains(out, "allowed/in-fence.txt") {
		t.Errorf("in-fence file should not be listed in out-of-fence warning, got:\n%s", out)
	}
}

func TestBashWiresTraceShimAndGitGuard(t *testing.T) {
	env := newTestFenceEnv(t, []string{"allowed"})
	initTestGitRepo(t, env.workDir)
	traceDir := filepath.Join(env.root, "trace")
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shimPath, err := filepath.Abs(filepath.Join("..", "..", "tillr-trace", "bash-shim.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shimPath); err != nil {
		t.Fatalf("bash-shim.sh missing at %s: %v", shimPath, err)
	}

	ts := NewToolsetWithBash(env.policy(false, false), BashConfig{
		TraceDir:     traceDir,
		BashShimPath: shimPath,
	})

	// 1. bash-shim.sh records proc.exec in traceDir/proc-exec.jsonl.
	out, isErr := mustCall(t, ts, "bash", map[string]any{
		"command": "echo trace-check",
	})
	if isErr {
		t.Fatalf("bash failed: %s", out)
	}
	rawTrace, err := os.ReadFile(filepath.Join(traceDir, "proc-exec.jsonl"))
	if err != nil {
		t.Fatalf("reading proc-exec.jsonl: %v", err)
	}
	if !strings.Contains(string(rawTrace), `"kind":"proc.exec"`) || !strings.Contains(string(rawTrace), "trace-check") {
		t.Errorf("expected proc.exec trace entry for trace-check, got:\n%s", string(rawTrace))
	}

	// 2. installGitGuard blocks `git config --local` targeting the repo's common git dir via --git-dir.
	out, isErr = mustCall(t, ts, "bash", map[string]any{
		"command": `git --git-dir="$PWD/.git" config --local user.name 'Hijack'`,
	})
	if !isErr {
		t.Fatalf("expected installGitGuard to refuse git config --local targeting protected common dir, got success: %s", out)
	}
	if !strings.Contains(strings.ToLower(out), "tillr-git-guard") {
		t.Errorf("expected tillr-git-guard refusal message, got: %s", out)
	}
}

func TestBashTimeoutAndBoundedTailCapture(t *testing.T) {
	env := newTestFenceEnv(t, []string{"allowed"})
	ts := NewToolsetWithBash(env.policy(false, false), BashConfig{
		NonLanding:     true,
		MaxOutputBytes: 64,
	})

	// 1. Bounded tail capture keeps only the last MaxOutputBytes.
	out, isErr := mustCall(t, ts, "bash", map[string]any{
		"command": "printf 'HEAD_MARKER_00000000000000000000000000000000000000000000000000000000000000000000TAIL_MARKER'",
	})
	if isErr {
		t.Fatalf("bash failed: %s", out)
	}
	if !strings.Contains(out, "[output truncated; showing last 64 bytes]") {
		t.Errorf("expected truncation header, got: %q", out)
	}
	if !strings.Contains(out, "TAIL_MARKER") {
		t.Errorf("expected TAIL_MARKER preserved, got: %q", out)
	}
	if strings.Contains(out, "HEAD_MARKER") {
		t.Errorf("expected HEAD_MARKER truncated, got: %q", out)
	}

	// 2. timeout_seconds terminates long-running commands cleanly.
	markerPath := filepath.Join(env.workDir, "after_sleep.txt")
	raw, err := json.Marshal(map[string]any{
		"command":         "sleep 30; touch after_sleep.txt",
		"timeout_seconds": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, isErr = ts.Execute(context.Background(), ContentBlock{
		Type:  BlockToolUse,
		ID:    "call-timeout",
		Name:  "bash",
		Input: raw,
	})
	if !isErr || !strings.Contains(out, "timed out") {
		t.Fatalf("expected timeout error, got isErr=%v out=%q", isErr, out)
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("expected command to be terminated before creating %s, stat err=%v", markerPath, err)
	}
}
