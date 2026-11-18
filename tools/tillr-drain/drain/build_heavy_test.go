package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestHeavyExecOwnerHelper(t *testing.T) {
	if os.Getenv("TILLR_TEST_HEAVY_EXEC") != "1" {
		return
	}
	root, work, child := os.Getenv("TILLR_TEST_HEAVY_ROOT"), os.Getenv("TILLR_TEST_HEAVY_WORK"), os.Getenv("TILLR_TEST_HEAVY_CHILD")
	steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: filepath.Join(work, "tillr-gate")}
	runner := HeavyRunner{Root: root, Work: filepath.Join(work, ".rolling", "worktree"), Judge: steps.JudgeHeavy}
	ended := make(chan error, 1)
	go func() {
		if program := os.Getenv("TILLR_TEST_ROLLING_PROGRAM"); program != "" {
			steps.RollingProgram = program
			_, err := steps.Rolling(context.Background(), "", heavyGit(t, root, "rev-parse", "HEAD"))
			ended <- err
		} else {
			_, err := runner.Run(context.Background())
			ended <- err
		}
	}()
	for {
		if _, err := os.Stat(child); err == nil {
			break
		}
		select {
		case err := <-ended:
			t.Fatalf("old heavy run ended before exec: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Exec(self, []string{self, "-test.run=^TestHeavyExecReplacementHelper$"}, os.Environ()); err != nil {
		t.Fatal(err)
	}
}

func TestHeavyExecReplacementHelper(t *testing.T) {
	if os.Getenv("TILLR_TEST_HEAVY_EXEC") != "1" {
		return
	}
	root, work, child := os.Getenv("TILLR_TEST_HEAVY_ROOT"), os.Getenv("TILLR_TEST_HEAVY_WORK"), os.Getenv("TILLR_TEST_HEAVY_CHILD")
	if os.Getenv("TILLR_TEST_ROLLING_PROGRAM") != "" {
		steps := &ProcessSteps{Root: root, LaneRoot: work, RollingProgram: "true"}
		out, err := steps.Rolling(context.Background(), "", heavyGit(t, root, "rev-parse", "HEAD"))
		if err != nil || !out.Known || !out.Green {
			t.Fatalf("rolling recovery=%+v/%v", out, err)
		}
		heavyChildDead(t, child)
		if err := os.WriteFile(filepath.Join(work, "replacement-verdict"), []byte("one rolling verdict; unchanged tip\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	verdicts, fixers := 0, 0
	runner := HeavyRunner{Root: root, Work: filepath.Join(work, ".rolling", "worktree"), Judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		heavyChildDead(t, child)
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	}, Report: func(r HeavyRun) {
		verdicts++
		if r.Result != "green" {
			t.Errorf("replacement published %s", r.Result)
		}
	}, Fix: func(context.Context, string, []HeavyCheck) error { fixers++; return nil }}
	for {
		out, err := runner.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !out.Busy {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if verdicts != 1 || fixers != 0 {
		t.Fatalf("verdicts=%d fixers=%d", verdicts, fixers)
	}
	if got := heavyGit(t, root, "rev-parse", GreenRef); got != heavyGit(t, root, "rev-parse", "HEAD") {
		t.Fatalf("tip not proved after exec: %s", got)
	}
	if err := os.WriteFile(filepath.Join(work, "replacement-verdict"), []byte("one green verdict; no red, stall or fixer; unchanged tip\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestHeavyExecClosesOwnershipAndReplacementProvesTipWithoutAnotherLanding(t *testing.T) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	heavyBuildGate(t, work)
	child := heavyBackgroundDeclaration(t, root, work, true)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestHeavyExecOwnerHelper$")
	cmd.Env = append(os.Environ(), "TILLR_TEST_HEAVY_EXEC=1", "TILLR_TEST_HEAVY_ROOT="+root, "TILLR_TEST_HEAVY_WORK="+work, "TILLR_TEST_HEAVY_CHILD="+child)
	log, err := os.Create(filepath.Join(work, "exec-owner.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		raw, _ := os.ReadFile(log.Name())
		t.Fatalf("exec replacement: %v\n%s", err, raw)
	}
	raw, err := os.ReadFile(filepath.Join(work, "replacement-verdict"))
	if err != nil || string(raw) != "one green verdict; no red, stall or fixer; unchanged tip\n" {
		t.Fatalf("replacement proof=%q/%v", raw, err)
	}
	heavyChildDead(t, child)
}
