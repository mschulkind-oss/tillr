package run

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func setupTriageGuardFixture(t *testing.T) (durableDir, repoDir, tmpDir string, env []string) {
	t.Helper()
	base := t.TempDir()
	durableDir = filepath.Join(base, "durable")
	repoDir = filepath.Join(base, "repo")
	tmpDir = filepath.Join(base, "tmp")
	stubBin := filepath.Join(base, "stub-bin")
	for _, d := range []string{durableDir, repoDir, tmpDir, stubBin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Stub tillr binaries that echo their invocation and exit 0.
	stubScript := "#!/bin/sh\nprintf 'PASS:%s:%s\\n' \"$(basename \"$0\")\" \"$*\"\n"
	for _, name := range []string{
		"tillr", "tillr-item", "tillr-watch", "tillr-drain",
		"tillr-run", "tillr-store", "tillr-trace", "tillr-view",
		"tillr-land", "tillr-gate", "tillr-keep",
		"git", "just", "sqlite3", "kill", "pkill", "killall", "systemctl", "service",
	} {
		if err := os.WriteFile(filepath.Join(stubBin, name), []byte(stubScript), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	baseEnv := []string{
		"PATH=" + stubBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR=" + tmpDir,
		"HOME=" + base,
	}
	var err error
	env, err = installTriageGuard(durableDir, baseEnv)
	if err != nil {
		t.Fatalf("installTriageGuard: %v", err)
	}
	return durableDir, repoDir, tmpDir, env
}

func runShWithEnv(t *testing.T, dir string, env []string, script string) (int, string, string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = dir
	cmd.Env = env
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("exec error: %v", err)
		}
	}
	return code, outBuf.String(), errBuf.String()
}

func TestTriageGuardTillrAllowlistAndRefusals(t *testing.T) {
	_, repoDir, _, env := setupTriageGuardFixture(t)

	allowed := []string{
		"tillr view status",
		"tillr item list",
		"tillr item show item-1",
		"tillr item request --filed-by tillr-triage --alert a-1 --dedup-key r:k",
		"tillr item request --filed-by=tillr-triage --alert=a-1 --dedup-key=r:k",
		"tillr item dep --add a b",
		"tillr run status",
		"tillr run lanes",
		"tillr-run pi-build-agent-dir",
		"tillr-run pi-stop-at-gap",
		"tillr-run pi-compact",
		"tillr-run extract-report",
		"tillr drain why item-1",
		"tillr drain workers",
		"tillr drain policy",
		"tillr drain workflow",
		"tillr watch status",
		"tillr watch lint",
		"tillr watch record-triage --alert a-1",
		"tillr store inventory",
		"tillr store definition",
		"tillr store check",
		"tillr store events",
		"tillr trace tick",
		"tillr trace audit",
		"tillr trace run",
		"tillr trace cost",
		"tillr trace discrepancies",
		"tillr --help",
	}
	for _, c := range allowed {
		code, out, stderr := runShWithEnv(t, repoDir, env, c)
		if code != 0 || !strings.HasPrefix(out, "PASS:") {
			t.Fatalf("expected %q to pass, got code=%d out=%q stderr=%q", c, code, out, stderr)
		}
	}

	forbidden := []string{
		"tillr view serve",
		"tillr item request --alert a-1 --dedup-key r:k",
		"tillr item request --filed-by other --alert a-1 --dedup-key r:k",
		"tillr item request --filed-by tillr-triage --dedup-key r:k",
		"tillr item request --filed-by tillr-triage --alert a-1",
		"tillr item dep --remove a b",
		"tillr item dep a b",
		"tillr item drop item-1",
		"tillr run exec",
		"tillr drain stop",
		"tillr drain loop",
		"tillr drain retry item-1",
		"tillr drain clear-fault",
		"tillr drain push",
		"tillr watch silence",
		"tillr watch rule",
		"tillr watch ack",
		"tillr watch settle",
		"tillr watch wait",
		"tillr watch seen",
		"tillr store init",
		"tillr store checkpoint",
		"tillr land",
		"tillr gate",
		"tillr-land",
		"tillr-gate",
		"tillr-keep",
	}
	for _, c := range forbidden {
		code, _, stderr := runShWithEnv(t, repoDir, env, c)
		if code != 2 || !strings.Contains(stderr, "triage boundary") {
			t.Fatalf("expected %q to exit 2 with triage boundary, got code=%d stderr=%q", c, code, stderr)
		}
	}
}

func TestTriageGuardGitAndProcessRefusals(t *testing.T) {
	_, repoDir, _, env := setupTriageGuardFixture(t)

	allowedGit := []string{
		"git status",
		"git log -n 1",
		"git show HEAD",
		"git diff",
		"git rev-parse HEAD",
		"git branch --list",
		"git tag -l",
		"git worktree list",
		"git config --get user.name",
		"git remote -v",
	}
	for _, c := range allowedGit {
		code, out, stderr := runShWithEnv(t, repoDir, env, c)
		if code != 0 || !strings.HasPrefix(out, "PASS:git:") {
			t.Fatalf("expected %q to pass, got code=%d out=%q stderr=%q", c, code, out, stderr)
		}
	}

	forbidden := []string{
		"git commit -m x",
		"git add .",
		"git rm foo",
		"git reset --hard",
		"git checkout main",
		"git switch main",
		"git push",
		"git fetch",
		"git worktree add ../wt",
		"git config user.name foo",
		"git branch -D foo",
		"just",
		"sqlite3 foo.db",
		"env kill -0 $$",
		"pkill foo",
		"killall foo",
		"systemctl status",
		"service foo status",
	}
	for _, c := range forbidden {
		code, _, stderr := runShWithEnv(t, repoDir, env, c)
		if code != 2 || !strings.Contains(stderr, "triage boundary") {
			t.Fatalf("expected %q to exit 2 with triage boundary, got code=%d stderr=%q", c, code, stderr)
		}
	}

	// Verify bash kill builtin is refused even when BASH_ENV is reset to bash-shim.sh.
	shimPath, err := filepath.Abs(filepath.Join("..", "..", "tillr-trace", "bash-shim.sh"))
	if err != nil {
		t.Fatal(err)
	}
	bashEnv := append([]string{}, env...)
	bashEnv = append(withoutEnv(bashEnv, "BASH_ENV"), "BASH_ENV="+shimPath)
	cmd := exec.Command("bash", "-c", "kill -0 $$")
	cmd.Dir = repoDir
	cmd.Env = bashEnv
	out, err := cmd.CombinedOutput()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 2 || !strings.Contains(string(out), "triage boundary") {
		t.Fatalf("expected bash kill builtin to exit 2 with triage boundary, got err=%v out=%q", err, out)
	}
}

func TestTriageGuardFileMutatorsBoundary(t *testing.T) {
	durableDir, repoDir, tmpDir, env := setupTriageGuardFixture(t)

	repoFile := filepath.Join(repoDir, "tracked.txt")
	if err := os.WriteFile(repoFile, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Writes inside durableDir and TMPDIR succeed.
	okScript := `
set -e
touch "$1/a.txt"
cp "$2" "$1/b.txt"
chmod 0600 "$1/b.txt"
printf 'hi\n' | tee "$1/c.txt" >/dev/null
truncate -s 0 "$1/c.txt"
mv "$1/a.txt" "$1/a2.txt"
ln -s "$1/b.txt" "$1/b.link"
rm "$1/a2.txt" "$1/b.txt" "$1/c.txt" "$1/b.link"
touch "$3/tmp.txt"
rm "$3/tmp.txt"
`
	cmd := exec.Command("sh", "-c", okScript, "sh", durableDir, repoFile, tmpDir)
	cmd.Dir = repoDir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("in-scratch file operations failed: %v: %s", err, out)
	}

	// Writes targeting outside scratch (repoDir) are refused with exit 2.
	badCmds := []string{
		"touch tracked.txt",
		"touch " + filepath.Join(repoDir, "new.txt"),
		"rm " + repoFile,
		"mv " + repoFile + " " + filepath.Join(durableDir, "stolen.txt"),
		"cp " + filepath.Join(durableDir, "nope") + " " + filepath.Join(repoDir, "out.txt"),
		"chmod 0644 " + repoFile,
		"truncate -s 0 " + repoFile,
		"printf 'x\\n' | tee " + repoFile,
		"ln -s " + repoFile + " " + filepath.Join(repoDir, "link.txt"),
	}
	for _, c := range badCmds {
		code, _, stderr := runShWithEnv(t, repoDir, env, c)
		if code != 2 || !strings.Contains(stderr, "triage boundary") {
			t.Fatalf("expected out-of-scratch %q to exit 2 with triage boundary, got code=%d stderr=%q", c, code, stderr)
		}
	}
}

func TestTriageExecInstallsGuardAndReadOnlyView(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	repoRoot := filepath.Join(base, "repo")
	laneRoot := filepath.Join(repoRoot, ".tillr", "lanes")
	for _, d := range []string{repoRoot, laneRoot, filepath.Join(repoRoot, ".yolo")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	storePath := filepath.Join(repoRoot, ".tillr", "state.db")
	s, err := store.InitFixture(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	adapterPath := filepath.Join(base, "triage-adapter.sh")
	adapterScript := `#!/bin/sh
set -e
# Verify env vars.
[ "$GIT_OPTIONAL_LOCKS" = "0" ]
[ -z "${TILLR_PI_AGENT_DIRS_DIR:-}" ]
[ -n "$BASH_ENV" ]
[ "$BASH_ENV" = "$TILLR_SEAT_BASH_ENV" ]
# Verify read-only view has README.md and hides .tillr / .yolo (in symlink fallback) or is read-only.
test -r README.md
# Verify writing to work dir fails.
if touch should-fail.txt 2>/dev/null; then
    echo "unexpected write in work dir succeeded" >&2
    exit 1
fi
# Verify writing in durable dir succeeds.
touch "$TILLR_PROGRESS_FILE"
printf 'RESULT: done\n'
`
	if err := os.WriteFile(adapterPath, []byte(adapterScript), 0o755); err != nil {
		t.Fatal(err)
	}

	req := Request{
		ItemID:     "alert-1",
		RunID:      "run-triage-exec-1",
		LaneID:     "triage-1",
		Seat:       "triage",
		Backend:    "pi-scout",
		WorkingDir: "work",
		Command:    []string{adapterPath},
		TimeoutMS:  10000,
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(laneRoot, "triage-1", "work"), 0o755)
	})
	env, err := Exec(ctx, s, req, ExecOptions{
		LaneRoot: laneRoot,
		Now:      time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Exec(triage): %v", err)
	}
	var payload struct {
		ExitCode *int `json:"exit_code"`
	}
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ExitCode == nil || *payload.ExitCode != 0 {
		logBytes, _ := os.ReadFile(filepath.Join(repoRoot, ".tillr", "lanes", "triage-1", "run-triage-exec-1.log"))
		got := -1
		if payload.ExitCode != nil {
			got = *payload.ExitCode
		}
		t.Fatalf("Exec(triage) exit_code = %d, want 0; log:\n%s", got, string(logBytes))
	}
}

func TestTriageSymlinkFallbackView(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	work := filepath.Join(base, "lane", "work")
	for _, d := range []string{root, filepath.Join(root, ".tillr"), filepath.Join(root, ".yolo"), filepath.Join(root, "docs")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := prepareTriageSymlinkView(root, work); err != nil {
		t.Fatalf("prepareTriageSymlinkView: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(work, 0o755) })

	info, err := os.Stat(work)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o555 {
		t.Fatalf("work dir mode = %o, want 0555", info.Mode().Perm())
	}
	if _, err := os.Lstat(filepath.Join(work, ".tillr")); !os.IsNotExist(err) {
		t.Fatalf(".tillr must be excluded from symlink view, got err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(work, ".yolo")); !os.IsNotExist(err) {
		t.Fatalf(".yolo must be excluded from symlink view, got err=%v", err)
	}
	target, err := os.Readlink(filepath.Join(work, "README.md"))
	if err != nil || target != filepath.Join(root, "README.md") {
		t.Fatalf("README.md symlink = %q (%v), want %q", target, err, filepath.Join(root, "README.md"))
	}
}

func TestTriageRepoRootRecognizesItemsAndLanes(t *testing.T) {
	root := t.TempDir()
	if got := triageRepoRoot(filepath.Join(root, ".tillr", "items"), nil); got != root {
		t.Fatalf("triageRepoRoot(.tillr/items) = %q, want %q", got, root)
	}
	if got := triageRepoRoot(filepath.Join(root, ".tillr", "lanes"), nil); got != root {
		t.Fatalf("triageRepoRoot(.tillr/lanes) = %q, want %q", got, root)
	}
	explicit := filepath.Join(root, "explicit-repo")
	if got := triageRepoRoot(filepath.Join(root, ".tillr", "items"), []string{"TILLR_REPO_ROOT=" + explicit}); got != explicit {
		t.Fatalf("triageRepoRoot(TILLR_REPO_ROOT) = %q, want %q", got, explicit)
	}
}
