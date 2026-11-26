package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/settings"
)

func initDrainChecksDirRepo(t *testing.T, checksDir string, writeSettings bool) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-q", "-b", "main")
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
	if writeSettings {
		write(".tillr.toml", "[gate]\nchecks_dir = \""+checksDir+"\"\n")
	}
	write(filepath.Join(checksDir, "heavy-check.json"),
		`{"id":"heavy-check","label":"Heavy check","scope":"tree","tier":"heavy","timeout_ms":1000,"reads":["src/"],"command":["true"]}`)
	write(filepath.Join(checksDir, "fast.json"),
		`{"id":"fast-check","label":"Fast check","scope":"tree","tier":"fast","budget_ms":1000,"reads":["src/"],"writes":["out/"],"command":["true"]}`)
	write("src/app.go", "package src\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "init")
	return root
}

func TestChecksDirHeavyDeclarationsAndJudgeHeavy(t *testing.T) {
	root := initDrainChecksDirRepo(t, "custom/checks", true)

	decls, err := heavyCheckDeclarations(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decls["heavy-check"]; !ok {
		t.Fatalf("heavyCheckDeclarations missing heavy-check: %+v", decls)
	}

	// Stub tillr-gate to capture argv and return a passing heavy verdict.
	binDir := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" +
		`printf '{"record_version":1,"kind":"gate.verdict","id":"v1","producer":{"system":"tillr","utility":"tillr-gate","version":"0.1.0"},"created_at":"2026-11-26T00:00:00Z","payload":{"verdict":"pass","checks":[{"check_id":"heavy-check","result":"pass","duration_ms":1}]}}'` + "\n"
	writeForkFile(t, filepath.Join(binDir, "tillr-gate"), stub)
	if err := os.Chmod(filepath.Join(binDir, "tillr-gate"), 0o755); err != nil {
		t.Fatal(err)
	}

	steps := &ProcessSteps{
		Root:         root,
		HeavyGateBin: filepath.Join(binDir, "tillr-gate"),
		LaneRoot:     t.TempDir(),
	}
	var checks []HeavyCheck
	runner := HeavyRunner{
		Root:  root,
		Lock:  loopHeld(t, root),
		Work:  filepath.Join(t.TempDir(), ".rolling", "worktree"),
		Trunk: "refs/heads/main",
		Judge: func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
			var err error
			checks, err = steps.JudgeHeavy(ctx, dir, only)
			return checks, err
		},
	}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].CheckID != "heavy-check" || checks[0].Result != "pass" {
		t.Fatalf("JudgeHeavy got %+v", checks)
	}
	gotArgs, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(gotArgs), "--checks\ncustom/checks\n") {
		t.Fatalf("JudgeHeavy argv missing --checks custom/checks: %q", string(gotArgs))
	}

	// Invalid .tillr.toml refuses rather than falling back.
	writeForkFile(t, filepath.Join(root, ".tillr.toml"), "[gate]\nchecks_dir = \"../bad\"\n")
	if _, err := heavyCheckDeclarations(root); err == nil {
		t.Fatal("expected heavyCheckDeclarations to refuse invalid .tillr.toml")
	}
	if _, err := steps.JudgeHeavy(context.Background(), root, ""); err == nil {
		t.Fatal("expected JudgeHeavy to refuse invalid .tillr.toml")
	}
}

func TestChecksDirFixerGateAndFence(t *testing.T) {
	root := initDrainChecksDirRepo(t, "custom/checks", true)

	if !fixerHeavyDeclaration(root, "heavy-check") {
		t.Fatal("expected fixerHeavyDeclaration to find heavy-check in custom/checks")
	}

	l := &Loop{cfg: LoopConfig{Root: root}}
	gatePaths := l.fixerGateCodePaths()
	fence := TrunkFixerFenceWithGate([]string{"src/app.go", "custom/checks/heavy-check.json", ".tillr.toml"}, []string{"custom/checks/fast.json"}, gatePaths)
	if len(fence) != 1 || fence[0] != "src/app.go" {
		t.Fatalf("fixer fence = %v, want only [src/app.go]", fence)
	}

	// Broad parent directory containing custom/checks/ is expanded so custom/checks/ is excluded.
	rev := strings.TrimSpace(runGitOut(t, root, "rev-parse", "HEAD"))
	expanded, err := l.expandFixerPaths(context.Background(), rev, []string{"custom"})
	if err != nil {
		t.Fatal(err)
	}
	expandedFence := TrunkFixerFenceWithGate(expanded, nil, gatePaths)
	if len(expandedFence) != 0 {
		t.Fatalf("expanded fixer fence for broad parent of custom/checks = %v, want empty", expandedFence)
	}

	// Invalid .tillr.toml causes fixerCheck to return "error".
	writeForkFile(t, filepath.Join(root, ".tillr.toml"), "[gate]\nchecks_dir = \".git\"\n")
	steps := &ProcessSteps{}
	res, out := steps.fixerCheck(context.Background(), Candidate{Worktree: root}, "fast-check")
	if res != "error" || !strings.Contains(out, ".tillr.toml") {
		t.Fatalf("fixerCheck invalid .tillr.toml got (%q, %q), want error naming .tillr.toml", res, out)
	}
}

func TestChecksDirFormatCheck(t *testing.T) {
	root := initDrainChecksDirRepo(t, "custom/checks", true)

	// Absent format.json returns nil, nil.
	files, err := unformattedRepoFiles(context.Background(), root)
	if err != nil || files != nil {
		t.Fatalf("absent format.json got (%v, %v), want (nil, nil)", files, err)
	}

	// Present <checks_dir>/format.json is executed.
	decl, _ := json.Marshal(map[string]any{"command": []string{"sh", "-c", "printf 'src/app.go\\n'; exit 1"}})
	writeForkFile(t, filepath.Join(root, "custom", "checks", "format.json"), string(decl))
	files, err = unformattedRepoFiles(context.Background(), root)
	if err != nil || len(files) != 1 || files[0] != "src/app.go" {
		t.Fatalf("custom format.json got (%v, %v), want [src/app.go]", files, err)
	}

	// Invalid .tillr.toml returns Externalf error.
	writeForkFile(t, filepath.Join(root, ".tillr.toml"), "[gate]\nchecks_dir = \"a//b\"\n")
	if _, err := unformattedRepoFiles(context.Background(), root); err == nil {
		t.Fatal("expected unformattedRepoFiles to refuse invalid .tillr.toml")
	}
}

func TestChecksDirSettingsReloadAndReaders(t *testing.T) {
	root := initDrainChecksDirRepo(t, "custom/checks", true)

	// Settings reload updates PostLanding.Gate without raising a drift alarm.
	l := &Loop{
		cfg: LoopConfig{
			Root: root,
			PostLanding: settings.Settings{
				Trunk: settings.Trunk{Branch: settings.DefaultTrunkBranch},
			},
			Report: os.Stderr,
		},
	}
	writeForkFile(t, filepath.Join(root, ".tillr.toml"), "[gate]\nchecks_dir = \"next/checks\"\n")
	runGit(t, root, "add", ".tillr.toml")
	runGit(t, root, "commit", "-qm", "update checks_dir")
	tip := strings.TrimSpace(runGitOut(t, root, "rev-parse", "HEAD"))
	l.reloadSettingsOnLanding(context.Background(), tip)
	if got := l.postLandingSettings().ChecksDir(); got != "next/checks" {
		t.Fatalf("PostLanding.ChecksDir() = %q, want next/checks", got)
	}

	// Reset .tillr.toml to custom/checks for inventory and steps reader checks.
	writeForkFile(t, filepath.Join(root, ".tillr.toml"), "[gate]\nchecks_dir = \"custom/checks\"\n")

	proof, other, err := l.gateCheckInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !proof["heavy-check"] || other["fast-check"] != "tree/fast" {
		t.Fatalf("gateCheckInventory got proof=%v other=%v", proof, other)
	}

	steps := &ProcessSteps{Root: root}
	_, _, watches := steps.gateFailureContext(Candidate{Worktree: root, RunID: "r1"})
	if len(watches["fast-check"]) != 2 {
		t.Fatalf("gateFailureContext fast-check = %v, want reads+writes", watches["fast-check"])
	}
	reads := steps.gateCheckReads(Candidate{Worktree: root, RunID: "r1"}, nil)
	if len(reads["fast-check"]) != 1 || reads["fast-check"][0] != "src/" {
		t.Fatalf("gateCheckReads fast-check = %v, want [src/]", reads["fast-check"])
	}

	// Invalid .tillr.toml refuses in gateCheckInventory.
	writeForkFile(t, filepath.Join(root, ".tillr.toml"), "[gate]\nchecks_dir = \"../bad\"\n")
	if _, _, err := l.gateCheckInventory(context.Background()); err == nil {
		t.Fatal("expected gateCheckInventory to refuse invalid .tillr.toml")
	}
}

func runGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = clearGitRepoEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
