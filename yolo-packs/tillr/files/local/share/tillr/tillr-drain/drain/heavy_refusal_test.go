package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func heavyPointer(t *testing.T, root string) string {
	t.Helper()
	return heavyGit(t, root, "for-each-ref", "--format=%(objectname)", GreenRef)
}

func TestHeavyProofOfNoChecksIsRefusedAndKeepsThePointer(t *testing.T) {
	root, _ := heavyRepo(t)
	runner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) { return nil, nil })
	runner.Lock = loopHeld(t, root)
	out, err := runner.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no checks") || !strings.Contains(err.Error(), "gate program is empty") {
		t.Fatalf("empty proof: out=%+v err=%v", out, err)
	}
	if got := heavyPointer(t, root); got != "" {
		t.Fatalf("pointer moved to %s", got)
	}
	if len(out.Runs) != 1 || out.Runs[0].Result == "green" {
		t.Fatalf("runs=%+v", out.Runs)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".git", "tillr-gate", "green-"+out.Runs[0].TipSHA+".json")); statErr == nil {
		t.Fatal("a green proof file was written for an empty proof")
	}
}

func TestHeavyProofFromLinkedWorktreeRefusesAndLeavesTheRepository(t *testing.T) {
	root, _ := heavyRepo(t)
	linked := filepath.Join(scratchUnderDurable(t), "linked")
	heavyGit(t, root, "worktree", "add", "-q", "--detach", linked)
	called := false
	runner := HeavyRunner{Root: linked, Work: filepath.Join(scratchUnderDurable(t), "w"), Trunk: "refs/heads/main",
		Judge: func(context.Context, string, string) ([]HeavyCheck, error) {
			called = true
			return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
		}}
	if _, err := runner.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "linked worktree") {
		t.Fatalf("err=%v", err)
	}
	if _, _, err := acquireLoopLock(context.Background(), linked); err == nil {
		t.Fatal("a linked worktree took the loop lock")
	}
	if called || heavyPointer(t, root) != "" {
		t.Fatalf("judged=%v pointer=%q", called, heavyPointer(t, root))
	}
	for _, name := range []string{"tillr-gate", "tillr-loop.lock"} {
		if _, err := os.Stat(filepath.Join(root, ".git", name)); err == nil {
			t.Fatalf("%s written under the common directory", name)
		}
	}
}

func TestHeavyProofWithoutTheLoopLockRefuses(t *testing.T) {
	root, _ := heavyRepo(t)
	runner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
		t.Fatal("judged without the loop lock")
		return nil, nil
	})
	runner.Lock = nil
	if _, err := runner.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "loop lock") {
		t.Fatalf("err=%v", err)
	}
	if heavyPointer(t, root) != "" {
		t.Fatal("pointer moved")
	}
}

func TestSuiteGuardFailsAProofAimedAtTheRealRepository(t *testing.T) {
	if heavyCommonGuard == nil {
		t.Skip("not inside a repository")
	}
	// A scratch directory inside an enclosing repository resolves to that repository, as does cwd.
	targets := []string{"."}
	if dir := scratchUnderDurable(t); len(realCommonDirs(dir)) > 0 {
		targets = append(targets, dir)
	}
	for _, target := range targets {
		if _, err := heavyDirectory(context.Background(), target); err == nil || !strings.Contains(err.Error(), "real repository") {
			t.Fatalf("guard did not fire for %s: %v", target, err)
		}
	}
}

func TestSuiteGuardProtectsOuterRepositoryWhenRunningInsideNestedClone(t *testing.T) {
	outer, _ := heavyRepo(t)
	rollWork := filepath.Join(outer, ".yolo", "durable", "tillr-roll-fixture")
	nested := filepath.Join(rollWork, "source")
	siblingScratch := filepath.Join(rollWork, "go-shards-fixture", "package-1-shard-0", "drain-timing-fixture")
	if err := os.MkdirAll(siblingScratch, 0o755); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, outer, "clone", "-q", "--shared", outer, nested)
	nestedPkg := filepath.Join(nested, "tools", "tillr-drain", "drain")
	if err := os.MkdirAll(nestedPkg, 0o755); err != nil {
		t.Fatal(err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	origGuard := heavyCommonGuard
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
		heavyCommonGuard = origGuard
	})
	t.Setenv("YOLO_DURABLE_DIR", filepath.Dir(siblingScratch))
	if err := os.Chdir(nestedPkg); err != nil {
		t.Fatal(err)
	}
	installRealRepoGuard()

	for _, target := range []string{nestedPkg, siblingScratch} {
		if _, err := heavyDirectory(context.Background(), target); err == nil || !strings.Contains(err.Error(), "real repository") {
			t.Fatalf("guard did not fire for %s: %v", target, err)
		}
	}
	isolated, _ := heavyRepo(t)
	if _, err := heavyDirectory(context.Background(), isolated); err != nil {
		t.Fatalf("guard rejected isolated fixture repository: %v", err)
	}
}
