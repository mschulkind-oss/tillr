package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func clearGitRepoEnv(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
			"GIT_IMPLICIT_WORK_TREE", "GIT_NAMESPACE", "GIT_GRAFT_FILE",
			"GIT_SHALLOW_FILE", "GIT_REPLACE_REF_BASE", "GIT_NO_REPLACE_OBJECTS",
			"GIT_PREFIX", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT":
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = clearGitRepoEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func setupMattTrunkRepo(t *testing.T, dir string) string {
	t.Helper()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "yolo-packs", "matt", "files", "extensions"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.com")

	packJSON := `{"name":"matt","contributions":[]}` + "\n"
	if err := os.WriteFile(filepath.Join(repo, "yolo-packs", "matt", "pack.json"), []byte(packJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "yolo-packs", "matt", "files", "extensions", "trunk-ext.ts"), []byte("export default function() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "yolo-packs", "matt", "files", "extensions", "steering-timer.ts"), []byte("export default function() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "bin", "pi-forks.list"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "initial matt pack")
	return repo
}

func setupDeployedAgentDir(t *testing.T, dir string) string {
	t.Helper()
	deployed := filepath.Join(dir, "deployed-agent")
	for _, sub := range []string{
		"extensions",
		"sessions",
		"yolo-patched/pi-subagents",
		"yolo-patched/pi-background-tasks",
		"yolo-patched/pi-session-search",
		"npm/node_modules/@pify/memory",
	} {
		if err := os.MkdirAll(filepath.Join(deployed, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(deployed, "extensions", "deployed-only.ts"), []byte("export default function() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"packages": []string{
			"~/.pi/agent/yolo-patched/pi-subagents",
			"~/.pi/agent/yolo-patched/pi-session-search",
			"npm:@pify/memory@0.13.0",
		},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployed, "settings.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	return deployed
}

func writeFakePi(t *testing.T, binDir, argvFile, envFile string) {
	t.Helper()
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$STUB_ARGV"
printf 'PI_CODING_AGENT_DIR=%s\n' "${PI_CODING_AGENT_DIR:-}" > "$STUB_ENV"
if [ "${TILLR_PI_AGENT_DIRS_DIR+set}" = set ]; then echo "LEAK_DIRS=1"; else echo "LEAK_DIRS=0"; fi >> "$STUB_ENV"
if [ "${TILLR_EXTENSION_SET_RECORD_FILE+set}" = set ]; then echo "LEAK_REC=1"; else echo "LEAK_REC=0"; fi >> "$STUB_ENV"
cat >/dev/null
`
	writeExecutable(t, filepath.Join(binDir, "pi"), script)
}

func cleanPiEnv(extra ...string) []string {
	drop := map[string]bool{
		"PI_CODING_AGENT_DIR":             true,
		"TILLR_PI_AGENT_DIRS_DIR":         true,
		"TILLR_DEPLOYED_PI_AGENT_DIR":     true,
		"TILLR_EXTENSION_SET_RECORD_FILE": true,
		"TILLR_PI_BUILDS_DIR":             true,
		"TILLR_HARNESS_RECORD_FILE":       true,
		"YOLO_SESSION_HOMES":              true,
		"TILLR_CALL_SUSPENDS":             true,
		"TILLR_RESUME_PROMPT_FILE":        true,
		"TILLR_SESSION_MODE":              true,
		"TILLR_READ_ONLY_GROUP":           true,
		"YOLO_DURABLE_DIR":                true,
	}
	var env []string
	for _, entry := range os.Environ() {
		k, _, _ := strings.Cut(entry, "=")
		if drop[k] {
			continue
		}
		env = append(env, entry)
	}
	return append(env, extra...)
}

func TestPiAdapterSelectsTrunkAgentDirAndRecordsExtensionSet(t *testing.T) {
	adapter, err := filepath.Abs("backends/pi/run-pi")
	if err != nil {
		t.Fatal(err)
	}

	dir := scratch(t, "pi-ext-trunk")
	repo := setupMattTrunkRepo(t, dir)
	deployed := setupDeployedAgentDir(t, dir)
	agentDirs := filepath.Join(dir, "pi-agent-dirs")
	binDir := filepath.Join(dir, "bin")
	argvFile := filepath.Join(dir, "argv")
	envFile := filepath.Join(dir, "env")
	recFile := filepath.Join(dir, "extension-set")
	writeFakePi(t, binDir, argvFile, envFile)

	laneDir := filepath.Join(dir, "lane")
	worktree := filepath.Join(repo, ".worktree-lane")
	runGit(t, repo, "worktree", "add", "-b", "lane-1", worktree, "main")
	if err := os.MkdirAll(laneDir, 0755); err != nil {
		t.Fatal(err)
	}
	brief := filledBrief(t, worktree)
	briefPath := filepath.Join(laneDir, "brief.md")
	if err := os.WriteFile(briefPath, []byte(brief), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(laneDir, "base_sha"), []byte(runGit(t, repo, "rev-parse", "main")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	session := "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	if err := os.WriteFile(filepath.Join(laneDir, "session-id"), []byte(session+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	keyCmd := exec.Command(bin, "pi-build-agent-dir", "--key")
	keyCmd.Dir = worktree
	keyCmd.Env = cleanPiEnv(
		"TILLR_PI_AGENT_DIRS_DIR="+agentDirs,
		"TILLR_DEPLOYED_PI_AGENT_DIR="+deployed,
	)
	keyBytes, err := keyCmd.Output()
	if err != nil {
		t.Fatalf("pi-build-agent-dir --key: %v", err)
	}
	key1 := strings.TrimSpace(string(keyBytes))

	runAdapter := func(extraEnv ...string) (string, string, string) {
		t.Helper()
		_ = os.Remove(recFile)
		cmd := exec.Command(adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "medium", "--brief", briefPath)
		cmd.Dir = worktree
		cmd.Env = cleanPiEnv(append([]string{
			"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
			"STUB_ARGV=" + argvFile,
			"STUB_ENV=" + envFile,
			"TILLR_PI_AGENT_DIRS_DIR=" + agentDirs,
			"TILLR_DEPLOYED_PI_AGENT_DIR=" + deployed,
			"TILLR_EXTENSION_SET_RECORD_FILE=" + recFile,
		}, extraEnv...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run-pi failed: %v\n%s", err, out)
		}
		envBytes, err := os.ReadFile(envFile)
		if err != nil {
			t.Fatal(err)
		}
		argvBytes, err := os.ReadFile(argvFile)
		if err != nil {
			t.Fatal(err)
		}
		recBytes, err := os.ReadFile(recFile)
		if err != nil {
			t.Fatal(err)
		}
		return string(envBytes), string(argvBytes), strings.TrimSpace(string(recBytes))
	}

	// 1. Writer run builds and selects trunk agent dir, unsets internal env vars, records set <key1>.
	gotEnv, gotArgv, gotRec := runAdapter()
	wantAgentDir := filepath.Join(agentDirs, key1, "agent")
	if !strings.Contains(gotEnv, "PI_CODING_AGENT_DIR="+wantAgentDir+"\n") {
		t.Fatalf("expected PI_CODING_AGENT_DIR=%s, got:\n%s", wantAgentDir, gotEnv)
	}
	if !strings.Contains(gotEnv, "LEAK_DIRS=0\n") || !strings.Contains(gotEnv, "LEAK_REC=0\n") {
		t.Fatalf("env vars leaked to pi:\n%s", gotEnv)
	}
	if gotRec != wantAgentDir+" (set "+key1+")" {
		t.Fatalf("record = %q, want %q", gotRec, wantAgentDir+" (set "+key1+")")
	}
	if !strings.Contains(gotArgv, filepath.Join(wantAgentDir, "extensions", "trunk-ext.ts")) {
		t.Fatalf("writer argv missing trunk-ext.ts:\n%s", gotArgv)
	}
	if strings.Contains(gotArgv, "steering-timer.ts") {
		t.Fatalf("writer argv should exclude steering-timer.ts:\n%s", gotArgv)
	}

	// Verify sessions is linked to deployed sessions so session resumption works across rebuilds.
	sessTarget, err := os.Readlink(filepath.Join(wantAgentDir, "sessions"))
	if err != nil || sessTarget != filepath.Join(deployed, "sessions") {
		t.Fatalf("sessions symlink = %q (err=%v), want %q", sessTarget, err, filepath.Join(deployed, "sessions"))
	}
	sessionMarker := filepath.Join(wantAgentDir, "sessions", session+".jsonl")
	if err := os.WriteFile(sessionMarker, []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Non-writer drain run uses --no-extensions + rewritten packages while keeping settings.json linked.
	if err := os.MkdirAll(filepath.Join(wantAgentDir, "yolo-patched", "pi-session-search"), 0755); err != nil {
		t.Fatal(err)
	}
	_, nonWriterArgv, _ := runAdapter("TILLR_READ_ONLY_GROUP=grp-1")
	if !strings.Contains(nonWriterArgv, "--no-extensions\n") {
		t.Fatalf("non-writer built run missing --no-extensions:\n%s", nonWriterArgv)
	}
	if !strings.Contains(nonWriterArgv, filepath.Join(wantAgentDir, "extensions", "trunk-ext.ts")) {
		t.Fatalf("non-writer built run missing trunk-ext.ts:\n%s", nonWriterArgv)
	}
	if !strings.Contains(nonWriterArgv, filepath.Join(wantAgentDir, "yolo-patched", "pi-session-search")) {
		t.Fatalf("non-writer built run missing rewritten yolo-patched package:\n%s", nonWriterArgv)
	}
	if !strings.Contains(nonWriterArgv, filepath.Join(wantAgentDir, "npm", "node_modules", "@pify", "memory")) {
		t.Fatalf("non-writer built run missing resolved npm package:\n%s", nonWriterArgv)
	}
	settingsInfo, err := os.Lstat(filepath.Join(wantAgentDir, "settings.json"))
	if err != nil || settingsInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("settings.json must remain a symlink (mode=%v err=%v)", settingsInfo.Mode(), err)
	}

	// 3. Edit trunk to a broken extension -> build fails, keeps key1, records held status, and resumes session.
	if err := os.WriteFile(filepath.Join(repo, "yolo-packs", "matt", "files", "extensions", "trunk-ext.ts"), []byte("throw new Error('intentional build failure');\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "yolo-packs/matt/files/extensions/trunk-ext.ts")
	runGit(t, repo, "commit", "-m", "break matt extension")

	keyCmd2 := exec.Command(bin, "pi-build-agent-dir", "--key")
	keyCmd2.Dir = worktree
	keyCmd2.Env = cleanPiEnv("TILLR_PI_AGENT_DIRS_DIR="+agentDirs, "TILLR_DEPLOYED_PI_AGENT_DIR="+deployed)
	keyBytes2, err := keyCmd2.Output()
	if err != nil {
		t.Fatalf("pi-build-agent-dir --key 2: %v", err)
	}
	key2 := strings.TrimSpace(string(keyBytes2))
	if key2 == key1 {
		t.Fatalf("expected new key after trunk edit")
	}

	cmdHeld := exec.Command(adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "medium", "--brief", briefPath)
	cmdHeld.Dir = worktree
	cmdHeld.Env = cleanPiEnv(
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_ARGV="+argvFile,
		"STUB_ENV="+envFile,
		"TILLR_PI_AGENT_DIRS_DIR="+agentDirs,
		"TILLR_DEPLOYED_PI_AGENT_DIR="+deployed,
		"TILLR_EXTENSION_SET_RECORD_FILE="+recFile,
		"TILLR_SESSION_MODE=resume",
	)
	heldOut, err := cmdHeld.CombinedOutput()
	if err != nil {
		t.Fatalf("held fallback run-pi failed: %v\n%s", err, heldOut)
	}
	if !strings.Contains(string(heldOut), "pi-extensions-build-failed") || !strings.Contains(string(heldOut), "mover") {
		t.Fatalf("expected diagnostic naming pi-extensions-build-failed and mover on stderr, got:\n%s", heldOut)
	}
	recHeld, err := os.ReadFile(recFile)
	if err != nil {
		t.Fatal(err)
	}
	wantHeldRec := wantAgentDir + " (set " + key1 + "; held pi-extensions-build-failed for " + key2 + ")"
	if strings.TrimSpace(string(recHeld)) != wantHeldRec {
		t.Fatalf("held record = %q, want %q", strings.TrimSpace(string(recHeld)), wantHeldRec)
	}
	heldArgv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(heldArgv), "--session\n"+session+"\n") {
		t.Fatalf("resumed run missing --session %s:\n%s", session, heldArgv)
	}

	// 4. Explicit empty TILLR_PI_AGENT_DIRS_DIR falls back to deployed and records (deployed).
	_, _, depRec := runAdapter("TILLR_PI_AGENT_DIRS_DIR=", "PI_CODING_AGENT_DIR="+deployed)
	if depRec != deployed+" (deployed)" {
		t.Fatalf("explicit empty dirs record = %q, want %q", depRec, deployed+" (deployed)")
	}
}

func TestPiAdapterOutsideMattRepoUsesDeployed(t *testing.T) {
	adapter, err := filepath.Abs("backends/pi/run-pi")
	if err != nil {
		t.Fatal(err)
	}
	dir := scratch(t, "pi-ext-outside")
	binDir := filepath.Join(dir, "bin")
	argvFile := filepath.Join(dir, "argv")
	envFile := filepath.Join(dir, "env")
	recFile := filepath.Join(dir, "extension-set")
	writeFakePi(t, binDir, argvFile, envFile)

	work := filepath.Join(t.TempDir(), "plain-work")
	if err := os.MkdirAll(work, 0755); err != nil {
		t.Fatal(err)
	}
	brief := filledBrief(t, work)
	briefPath := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(briefPath, []byte(brief), 0644); err != nil {
		t.Fatal(err)
	}
	customAgent := filepath.Join(dir, "custom-agent")
	if err := os.MkdirAll(customAgent, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "medium", "--brief", briefPath)
	cmd.Dir = work
	cmd.Env = cleanPiEnv(
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_ARGV="+argvFile,
		"STUB_ENV="+envFile,
		"PI_CODING_AGENT_DIR="+customAgent,
		"TILLR_EXTENSION_SET_RECORD_FILE="+recFile,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run-pi failed: %v\n%s", err, out)
	}
	rec, err := os.ReadFile(recFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(rec)) != customAgent+" (deployed)" {
		t.Fatalf("outside-repo record = %q, want %q", strings.TrimSpace(string(rec)), customAgent+" (deployed)")
	}

	// Plain scratch directory under <mattRepo>/.yolo/durable/ (no .git of its own)
	// must not inherit the enclosing repository's trunk extensions.
	mattRepo := setupMattTrunkRepo(t, dir)
	scratchWork := filepath.Join(mattRepo, ".yolo", "durable", "plain-scratch")
	if err := os.MkdirAll(scratchWork, 0755); err != nil {
		t.Fatal(err)
	}
	scratchBrief := filepath.Join(dir, "scratch-brief.md")
	if err := os.WriteFile(scratchBrief, []byte(filledBrief(t, scratchWork)), 0644); err != nil {
		t.Fatal(err)
	}
	cmdScratch := exec.Command(adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "medium", "--brief", scratchBrief)
	cmdScratch.Dir = scratchWork
	cmdScratch.Env = cleanPiEnv(
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_ARGV="+argvFile,
		"STUB_ENV="+envFile,
		"PI_CODING_AGENT_DIR="+customAgent,
		"TILLR_PI_AGENT_DIRS_DIR="+filepath.Join(dir, "unused-agent-dirs"),
		"TILLR_EXTENSION_SET_RECORD_FILE="+recFile,
	)
	if out, err := cmdScratch.CombinedOutput(); err != nil {
		t.Fatalf("run-pi in scratch dir failed: %v\n%s", err, out)
	}
	recScratch, err := os.ReadFile(recFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(recScratch)) != customAgent+" (deployed)" {
		t.Fatalf("scratch-dir record = %q, want %q", strings.TrimSpace(string(recScratch)), customAgent+" (deployed)")
	}
}

func TestBuildAgentDirReadsConfiguredTrunkBranchWithoutMain(t *testing.T) {
	dir := scratch(t, "pi-ext-dev")
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "yolo-packs", "matt", "files", "extensions"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "dev")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.com")

	if err := os.WriteFile(filepath.Join(repo, ".tillr.toml"), []byte("[trunk]\nbranch = \"dev\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "yolo-packs", "matt", "pack.json"), []byte(`{"name":"matt","contributions":[]}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "yolo-packs", "matt", "files", "extensions", "trunk-ext.ts"), []byte("export default function() { return 'dev'; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "bin", "pi-forks.list"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "init on dev")

	// Check out a different branch with different extension content so HEAD != refs/heads/dev.
	runGit(t, repo, "checkout", "-b", "work")
	if err := os.WriteFile(filepath.Join(repo, "yolo-packs", "matt", "files", "extensions", "trunk-ext.ts"), []byte("export default function() { return 'work'; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "yolo-packs/matt/files/extensions/trunk-ext.ts")
	runGit(t, repo, "commit", "-m", "work divergence")

	deployed := setupDeployedAgentDir(t, dir)
	agentDirs := filepath.Join(dir, "pi-agent-dirs")

	cmd := exec.Command(bin, "pi-build-agent-dir")
	cmd.Dir = repo
	cmd.Env = cleanPiEnv(
		"TILLR_PI_AGENT_DIRS_DIR="+agentDirs,
		"TILLR_DEPLOYED_PI_AGENT_DIR="+deployed,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pi-build-agent-dir failed: %v\n%s", err, out)
	}
	builtDir := strings.TrimSpace(string(out))
	extBytes, err := os.ReadFile(filepath.Join(builtDir, "extensions", "trunk-ext.ts"))
	if err != nil {
		t.Fatalf("read built extension: %v", err)
	}
	if !strings.Contains(string(extBytes), "'dev'") {
		t.Fatalf("built extension = %q, want content from refs/heads/dev (not work HEAD)", string(extBytes))
	}
}
