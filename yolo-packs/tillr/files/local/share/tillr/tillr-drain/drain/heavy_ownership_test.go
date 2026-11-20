package drain

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHeavyBusyDoesNotConsumeASameTipTrigger(t *testing.T) {
	root, tip := heavyRepo(t)
	dir := filepath.Join(root, ".git", "tillr-gate")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "heavy.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	calls := 0
	steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		calls++
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	}}
	l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: io.Discard}}
	if err := l.startHeavy(context.Background(), tip); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err := l.startHeavy(context.Background(), tip); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	if calls != 1 {
		t.Fatalf("unexecuted trigger consumed: calls=%d", calls)
	}
}
func heavyBuildGate(t *testing.T, work string) string {
	t.Helper()
	bin := filepath.Join(work, "tillr-gate")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join("..", "..", "tillr-gate")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, raw)
	}
	return bin
}
func heavyBackgroundDeclaration(t *testing.T, root, work string, wait bool) string {
	t.Helper()
	checks := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checks, 0755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(work, "child.pid")
	last := "exit 0"
	if wait {
		last = "wait"
	}
	writeForkFile(t, filepath.Join(checks, "unit.json"), `{"id":"unit","label":"unit","scope":"tree","tier":"heavy","timeout_ms":3600000,"command":["sh","-c","sleep 1000 & echo $! > `+child+`.part; mv `+child+`.part `+child+`; `+last+`"],"reads":["value"],"remember":false}`)
	heavyCommit(t, root, "ready")
	t.Cleanup(func() {
		raw, err := os.ReadFile(child)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	return child
}
func heavyChildDead(t *testing.T, child string) {
	t.Helper()
	raw, err := os.ReadFile(child)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := exec.Command("ps", "-o", "stat=", "-p", strings.TrimSpace(string(raw))).Output()
	if strings.TrimSpace(string(state)) != "" && !strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
		t.Fatalf("child survived checkout cleanup: %s", state)
	}
}
func TestHeavySuccessfulDeclarationEndsBackgroundChildren(t *testing.T) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	bin := heavyBuildGate(t, work)
	child := heavyBackgroundDeclaration(t, root, work, false)
	steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: bin}
	runner := heavyFixture(t, root, steps.JudgeHeavy)
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	heavyChildDead(t, child)
}

// This is a real killed drain-owner process, not just an abandoned empty checkout.
func TestHeavyKilledOwnerHelper(t *testing.T) {
	if os.Getenv("TILLR_TEST_HEAVY_OWNER") != "1" {
		return
	}
	root, work := os.Getenv("TILLR_TEST_HEAVY_ROOT"), os.Getenv("TILLR_TEST_HEAVY_WORK")
	steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: filepath.Join(work, "tillr-gate"), RollingProgram: os.Getenv("TILLR_TEST_HEAVY_LEGACY")}
	runner := HeavyRunner{Root: root, Lock: loopHeld(t, root), Work: filepath.Join(work, ".rolling", "worktree"), Judge: steps.JudgeHeavy}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestHeavyKilledOwnerReapsJudgesBeforeCheckoutRecovery(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "declaration"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) { testHeavyKilledOwner(t, legacy) })
	}
}
func testHeavyKilledOwner(t *testing.T, legacy bool) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	heavyBuildGate(t, work)
	child := heavyBackgroundDeclaration(t, root, work, true)
	legacyProgram := ""
	if legacy {
		writeForkFile(t, filepath.Join(root, "tools", "gate", "checks", "unit.json"), `{"id":"unit","label":"unit","scope":"tree","tier":"fast","budget_ms":1,"command":["true"]}`)
		heavyCommit(t, root, "legacy ready")
		source, err := filepath.Abs(filepath.Join("..", "..", "fork-check.py"))
		if err != nil {
			t.Fatal(err)
		}
		driver := filepath.Join(work, "legacy.py")
		writeForkFile(t, driver, `import ast,contextlib,ctypes,json,os,pathlib,platform,signal,struct,subprocess,sys,threading,time
from pathlib import Path
source=sys.argv[1]
module=ast.parse(Path(source).read_text())
names={'environ','Cancellation','_kill_process_group','_run_cancellable','_start_cpu_sampler','_start_process_sampler','progress'}
constants={'REUSE_ENV','GATE_ENV_DROP','_CPU_SHIM','WATCHDOG_SAMPLE_INTERVAL','WATCHDOG_SILENT_SAMPLES','CHECK_STALLED_RC'}
nodes=[node for node in module.body if (isinstance(node,(ast.FunctionDef,ast.ClassDef)) and node.name in names) or (isinstance(node,ast.Assign) and any(isinstance(target,ast.Name) and target.id in constants for target in node.targets))]
exec(compile(ast.Module(body=nodes,type_ignores=[]),source,'exec'),globals())
_run_cancellable(Path.cwd(),['sh','-c','sleep 1000 & echo $! > '+sys.argv[2]+'.part; mv '+sys.argv[2]+'.part '+sys.argv[2]+'; wait'],1000,None,(),Cancellation(),False)
`)
		legacyProgram = "python3 " + driver + " " + source + " " + child
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestHeavyKilledOwnerHelper$")
	cmd.Env = append(os.Environ(), "TILLR_TEST_HEAVY_OWNER=1", "TILLR_TEST_HEAVY_ROOT="+root, "TILLR_TEST_HEAVY_WORK="+work, "TILLR_TEST_HEAVY_LEGACY="+legacyProgram)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	// Readiness is the child's pid file, which the check publishes whole by rename. There is
	// no wall-clock bound: a helper that dies before publishing it ends the wait, and a hang
	// is stopped by the test binary's own timeout.
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	for {
		if _, err := os.Stat(child); err == nil {
			break
		}
		select {
		case <-exited:
			t.Fatal("the owner process ended before its judge child started")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-exited
	// The inherited lock remains held until the surviving supervisor has ended the judge.
	lock, err := os.OpenFile(filepath.Join(root, ".git", "tillr-gate", "heavy.lock"), os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	heavyChildDead(t, child)
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	runner := HeavyRunner{Root: root, Lock: loopHeld(t, root), Work: filepath.Join(work, ".rolling", "worktree"), Judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		heavyChildDead(t, child)
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	}}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	green := heavyGit(t, root, "rev-parse", GreenRef)
	if green != heavyGit(t, root, "rev-parse", "HEAD") {
		t.Fatalf("green=%s", green)
	}
}
