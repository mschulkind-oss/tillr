package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func TestProcessStepsSetsGitCeilingDirectoriesForWorkerGateProofAndHook(t *testing.T) {
	ctx := context.Background()
	tmp := scratchUnderDurable(t)
	outer := filepath.Join(tmp, "outer")
	if err := os.MkdirAll(outer, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, outer, "init", "-q", "-b", "main")
	runGit(t, outer, "config", "user.name", "Fixture")
	runGit(t, outer, "config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(outer, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, outer, "add", "base.txt")
	runGit(t, outer, "commit", "-qm", "base")
	before := runGit(t, outer, "rev-parse", "HEAD")

	laneParent := filepath.Join(outer, ".tillr")
	laneRoot := filepath.Join(laneParent, "lanes")
	nonRepo := filepath.Join(laneRoot, "lane-1", "durable", "nonrepo")
	if err := os.MkdirAll(nonRepo, 0o755); err != nil {
		t.Fatal(err)
	}

	s, err := store.InitFixture(ctx, filepath.Join(tmp, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Create a script that records GIT_CEILING_DIRECTORIES and attempts git commit in nonRepo.
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(binDir, "probe-git-ceiling")
	probeScript := `#!/bin/sh
set -u
out="$1"
shift
printf '%s' "${GIT_CEILING_DIRECTORIES:-}" > "$out"
cd ` + "`" + `printf '%s' '` + nonRepo + `'` + "`" + `
printf 'stray\n' > stray.txt
if git add stray.txt 2>/dev/null && git commit -qm escape 2>/dev/null; then
  exit 99
fi
`
	if err := writeForkSafe(probe, []byte(probeScript), 0o755); err != nil {
		t.Fatal(err)
	}

	steps := &ProcessSteps{
		Root:     outer,
		Store:    filepath.Join(tmp, "store.db"),
		LaneRoot: laneRoot,
		RunBin:   probe,
	}

	t.Setenv("GIT_CEILING_DIRECTORIES", "")

	// 1. Worker process env (cmd built for RunBin, plus stepCommand).
	workerCmd := steps.stepCommand(ctx, []string{probe, filepath.Join(tmp, "worker.ceiling")}, nil)
	if out, err := workerCmd.CombinedOutput(); err != nil {
		t.Fatalf("worker probe failed: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(filepath.Join(tmp, "worker.ceiling")); string(got) != laneParent {
		t.Fatalf("worker GIT_CEILING_DIRECTORIES = %q, want %q", string(got), laneParent)
	}

	// 2. Gate process env (programWithEnv for gate step).
	gateCeiling := filepath.Join(tmp, "gate.ceiling")
	cand := Candidate{
		ItemID:   "item-1",
		RunID:    "run-1",
		LaneID:   "lane-1",
		Worktree: nonRepo,
		BaseSHA:  before,
		HeadSHA:  before,
	}
	steps.hooks = &definition.Hooks{Argv: []string{probe, gateCeiling}, Names: []string{"gate"}}
	steps.hookCommands = map[string][]string{"hook-gate": {probe, gateCeiling}}
	code, _, _, err := steps.programWithEnv(ctx, "gate", nonRepo, "hook-gate", cand, nil)
	if err != nil {
		t.Fatalf("gate probe error: %v", err)
	}
	if code == 99 {
		t.Fatal("gate probe committed to outer repo from non-repo under lane")
	}
	if got, _ := os.ReadFile(gateCeiling); string(got) != laneParent {
		t.Fatalf("gate GIT_CEILING_DIRECTORIES = %q, want %q", string(got), laneParent)
	}

	// 3. Proof process env (checkProcessEnv).
	proofCeiling := filepath.Join(tmp, "proof.ceiling")
	proofCmd := exec.CommandContext(ctx, probe, proofCeiling)
	proofCmd.Dir = nonRepo
	proofCmd.Env = steps.checkProcessEnv(os.Environ())
	if out, err := proofCmd.CombinedOutput(); err != nil {
		t.Fatalf("proof probe failed: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(proofCeiling); string(got) != laneParent {
		t.Fatalf("proof GIT_CEILING_DIRECTORIES = %q, want %q", string(got), laneParent)
	}

	// 4. Hook process env (checkHooks & CompleteItem).
	hookCeiling := filepath.Join(tmp, "hook.ceiling")
	hookProbe := filepath.Join(binDir, "hook-probe")
	hookScript := `#!/bin/sh
set -u
printf '%s' "${GIT_CEILING_DIRECTORIES:-}" > ` + "'" + hookCeiling + "'" + `
cd ` + "'" + nonRepo + "'" + `
printf 'stray\n' > stray.txt
if git add stray.txt 2>/dev/null && git commit -qm escape 2>/dev/null; then
  exit 99
fi
printf '{"hooks":{"complete-item":["--item","--run","--store","--root","--artifact-dir"]}}\n'
`
	if err := writeForkSafe(hookProbe, []byte(hookScript), 0o755); err != nil {
		t.Fatal(err)
	}
	itemStub := filepath.Join(binDir, "item-stub")
	if err := writeForkSafe(itemStub, []byte("#!/bin/sh\nprintf -- '--require-verdicts --store --item --state --run --reason --by --now --resolves-via\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	steps.ItemBin = itemStub
	steps.hooks = &definition.Hooks{Argv: []string{hookProbe}, Names: []string{"complete-item"}}
	if err := steps.checkHooks(ctx); err != nil {
		t.Fatalf("checkHooks failed: %v", err)
	}
	if got, _ := os.ReadFile(hookCeiling); string(got) != laneParent {
		t.Fatalf("hook GIT_CEILING_DIRECTORIES = %q, want %q", string(got), laneParent)
	}

	if after := runGit(t, outer, "rev-parse", "HEAD"); after != before {
		t.Fatalf("outer repo HEAD moved from %s to %s", before, after)
	}
}

type ceilingRecordingTB struct {
	testing.TB
	failed bool
	msg    string
}

func (r *ceilingRecordingTB) Helper() {}
func (r *ceilingRecordingTB) Fatalf(format string, args ...any) {
	r.failed = true
	r.msg = format
	panic("fatalf")
}

func TestRequireFixtureCommitRepoFailsOutsideOwnRepo(t *testing.T) {
	tmp := scratchUnderDurable(t)
	outer := filepath.Join(tmp, "outer")
	if err := os.MkdirAll(outer, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, outer, "init", "-q", "-b", "main")
	nonRepo := filepath.Join(outer, "not-a-repo")
	if err := os.MkdirAll(nonRepo, 0o755); err != nil {
		t.Fatal(err)
	}

	// Own repository succeeds.
	recOK := &ceilingRecordingTB{TB: t}
	requireFixtureCommitRepo(recOK, outer, []string{"commit", "-qm", "ok"})
	if recOK.failed {
		t.Fatalf("expected own repo to pass, got %s", recOK.msg)
	}

	// Non-repository inside outer repo fails the test.
	recFail := &ceilingRecordingTB{TB: t}
	func() {
		defer func() { _ = recover() }()
		requireFixtureCommitRepo(recFail, nonRepo, []string{"commit", "-qm", "escape"})
	}()
	if !recFail.failed {
		t.Fatal("expected requireFixtureCommitRepo to fail when directory is not its own repository")
	}
}

func TestLoopAndStagerGitRefuseLaneWorktreeWithoutDotGit(t *testing.T) {
	ctx := context.Background()
	tmp := scratchUnderDurable(t)
	outer := filepath.Join(tmp, "outer")
	if err := os.MkdirAll(outer, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, outer, "init", "-q", "-b", "main")
	if err := writeForkSafe(filepath.Join(outer, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, outer, "add", "base.txt")
	runGit(t, outer, "commit", "-qm", "base")

	laneRoot := filepath.Join(outer, ".tillr", "lanes")
	worktree := filepath.Join(laneRoot, "desk-trunk-fixer", "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}

	l := &Loop{cfg: LoopConfig{Root: outer, LaneRoot: laneRoot}}
	if _, err := l.git(ctx, worktree, "rev-parse", "HEAD"); err == nil {
		t.Fatal("Loop.git succeeded in a lane worktree without .git; expected refusal")
	}
	s := &LaneStager{Root: outer, LaneRoot: laneRoot}
	if _, err := s.git(ctx, worktree, "rev-parse", "HEAD"); err == nil {
		t.Fatal("LaneStager.git succeeded in a lane worktree without .git; expected refusal")
	}
}
