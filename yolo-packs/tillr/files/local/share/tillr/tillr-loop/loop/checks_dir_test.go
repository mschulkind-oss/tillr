package loop

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initLoopChecksDirFixture(t *testing.T, checksDir string, writeSettings bool) (string, string) {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	if writeSettings {
		write(".tillr.toml", "[gate]\nchecks_dir = \""+checksDir+"\"\n")
	}
	fastDecl, _ := json.Marshal(map[string]any{
		"id": "fast-tree-check", "label": "Fast tree", "scope": "tree", "tier": "fast", "budget_ms": 1000, "command": []string{"true"},
	})
	heavyDecl, _ := json.Marshal(map[string]any{
		"id": "heavy-tree-check", "label": "Heavy tree", "scope": "tree", "tier": "heavy", "timeout_ms": 1000, "command": []string{"true"},
	})
	write(filepath.Join(checksDir, "fast.json"), string(fastDecl))
	write(filepath.Join(checksDir, "heavy.json"), string(heavyDecl))
	write("README.md", "# Repo\n")
	git("add", ".")
	git("commit", "-qm", "init")
	head := git("rev-parse", "HEAD")

	installRoot := t.TempDir()
	gateInstall := filepath.Join(installRoot, "tillr-gate")
	if err := os.MkdirAll(gateInstall, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(gateInstall, "change-gate.py"), []byte("# stub\n"), 0o644)
	_ = os.WriteFile(filepath.Join(gateInstall, "tree-check.py"), []byte("# stub\n"), 0o644)
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)

	return root, head
}

func TestChecksDirGateAndSelfCheckAndPrecedence(t *testing.T) {
	root, head := initLoopChecksDirFixture(t, "custom/checks", true)

	// Clear TILLR_LOOP_REGISTRY so .tillr.toml is used.
	t.Setenv(registryEnv, "")
	var parseOut, parseErr bytes.Buffer
	parsed, handled, code := parseArgs("gate", []string{
		"--item", "i1", "--lane", "l1", "--run", "r1",
		"--worktree", root, "--base", head, "--head", head, "--root", root,
		"--tillr-gate", "tillr-gate",
	}, &parseOut, &parseErr)
	if handled {
		t.Fatalf("parseArgs failed: handled=%v code=%d stderr=%s", handled, code, parseErr.String())
	}
	ctx, err := buildContext("self-check", parsed)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := composeGate(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	planArgv := strings.Join(steps[1].Argv, " ")
	wantReg := filepath.Join(root, "custom", "checks")
	if !strings.Contains(planArgv, "--registry "+wantReg) {
		t.Fatalf("plan argv = %q, want --registry %s", planArgv, wantReg)
	}
	// No --checks forwarded to change-gate.py when neither --registry nor TILLR_LOOP_REGISTRY is set.
	if strings.Contains(ctx.GateProgram, "--checks") {
		t.Fatalf("GateProgram = %q, should not forward --checks when reading .tillr.toml", ctx.GateProgram)
	}

	// TILLR_LOOP_REGISTRY directory forwards --checks <abs dir> to change-gate.py.
	t.Setenv(registryEnv, "env/checks")
	ctxEnv, err := buildContext("gate", parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctxEnv.GateProgram, "--checks ") || !strings.Contains(ctxEnv.GateProgram, "env/checks") {
		t.Fatalf("GateProgram with dir TILLR_LOOP_REGISTRY = %q, want --checks .../env/checks", ctxEnv.GateProgram)
	}

	// TILLR_LOOP_REGISTRY .json file does NOT forward --checks to change-gate.py.
	t.Setenv(registryEnv, "env/registry.json")
	ctxJSON, err := buildContext("gate", parsed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ctxJSON.GateProgram, "--checks") {
		t.Fatalf("GateProgram with .json TILLR_LOOP_REGISTRY = %q, should not forward --checks", ctxJSON.GateProgram)
	}

	// Explicit --registry directory wins over TILLR_LOOP_REGISTRY and forwards --checks.
	parsedReg, handled, code := parseArgs("gate", []string{
		"--item", "i1", "--lane", "l1", "--run", "r1",
		"--worktree", root, "--base", head, "--head", head, "--root", root,
		"--registry", "flag/checks", "--tillr-gate", "tillr-gate",
	}, &parseOut, &parseErr)
	if handled {
		t.Fatalf("parseArgs failed: handled=%v code=%d stderr=%s", handled, code, parseErr.String())
	}
	ctxReg, err := buildContext("gate", parsedReg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctxReg.GateProgram, "flag/checks") {
		t.Fatalf("GateProgram with --registry = %q, want flag/checks", ctxReg.GateProgram)
	}

	// Absent .tillr.toml falls back to tools/gate/checks.
	t.Setenv(registryEnv, "")
	defRoot, defHead := initLoopChecksDirFixture(t, "tools/gate/checks", false)
	parsedDef, handled, code := parseArgs("gate", []string{
		"--item", "i1", "--lane", "l1", "--run", "r1",
		"--worktree", defRoot, "--base", defHead, "--head", defHead, "--root", defRoot,
		"--tillr-gate", "tillr-gate",
	}, &parseOut, &parseErr)
	if handled {
		t.Fatalf("parseArgs failed: handled=%v code=%d stderr=%s", handled, code, parseErr.String())
	}
	ctxDef, err := buildContext("gate", parsedDef)
	if err != nil {
		t.Fatal(err)
	}
	stepsDef, err := composeGate(ctxDef, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(stepsDef[1].Argv, " "), filepath.Join(defRoot, "tools", "gate", "checks")) {
		t.Fatalf("absent .tillr.toml plan argv = %v", stepsDef[1].Argv)
	}

	// Invalid .tillr.toml refuses with ExitUsage.
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[gate]\nchecks_dir = \"../bad\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := composeGate(ctx, true); err == nil {
		t.Fatalf("expected error for invalid .tillr.toml")
	} else if ref, ok := err.(*Refusal); !ok || ref.Code != ExitUsage {
		t.Fatalf("got %T %v, want Refusal with ExitUsage", err, err)
	}
}

func TestChecksDirRevisionChecks(t *testing.T) {
	root, head := initLoopChecksDirFixture(t, "custom/checks", true)

	// Stub tillr-gate on PATH that records argv.
	binDir := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "argv.txt")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(argsFile) + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "tillr-gate"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	// TILLR_LOOP_REGISTRY set to a .json file must not affect revisionCheck.
	t.Setenv(registryEnv, filepath.Join(root, "ignored-registry.json"))

	// fixer-gate heavy check uses custom/checks.
	var out, errBuf bytes.Buffer
	parsedFixer, handled, code := parseArgs("fixer-gate", []string{
		"--item", "i1", "--run", "r1", "--base", head,
		"--only", "heavy-tree-check", "--worktree", root, "--head", head,
	}, &out, &errBuf)
	if handled {
		t.Fatalf("parseArgs fixer-gate failed: handled=%v code=%d stderr=%s", handled, code, errBuf.String())
	}
	code, err := revisionCheck("fixer-gate", parsedFixer, strings.NewReader(""), &out, &errBuf)
	if err != nil || code != 0 {
		t.Fatalf("fixer-gate heavy failed: code=%d err=%v", code, err)
	}
	gotArgs, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(gotArgs), "custom/checks") {
		t.Fatalf("fixer-gate argv = %q, want custom/checks", string(gotArgs))
	}

	// replay and rolling in cwd use custom/checks.
	origDir, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	parsedRolling, handled, code := parseArgs("rolling", []string{"--all"}, &out, &errBuf)
	if handled {
		t.Fatalf("parseArgs rolling failed: handled=%v code=%d stderr=%s", handled, code, errBuf.String())
	}
	code, err = revisionCheck("rolling", parsedRolling, strings.NewReader(""), &out, &errBuf)
	if err != nil || code != 0 {
		t.Fatalf("rolling failed: code=%d err=%v", code, err)
	}
	gotArgs, _ = os.ReadFile(argsFile)
	if !strings.Contains(string(gotArgs), "custom/checks") {
		t.Fatalf("rolling argv = %q, want custom/checks", string(gotArgs))
	}

	// Invalid .tillr.toml refuses with ExitExternal.
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[gate]\nchecks_dir = \".git\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = revisionCheck("rolling", parsedRolling, strings.NewReader(""), &out, &errBuf)
	if ref, ok := err.(*Refusal); !ok || ref.Code != ExitExternal {
		t.Fatalf("invalid .tillr.toml got %T %v, want Refusal with ExitExternal", err, err)
	}
}
