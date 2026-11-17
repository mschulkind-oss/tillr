package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeavyProcessJudgeIncludesDeclarationsAndLegacyAndReplaysOneCheck(t *testing.T) {
	root, base := heavyRepo(t)
	work := scratchUnderDurable(t)
	bin := filepath.Join(work, "tillr-gate")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join("..", "..", "tillr-gate")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, raw)
	}
	checks := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checks, 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(checks, "unit.json"), `{"id":"unit","label":"unit","scope":"tree","tier":"heavy","budget_ms":1,"timeout_ms":1000,"command":["sh","-c","echo declaration; sleep 0.01"],"reads":["value"],"remember":false}`)
	// declarations are part of the tree judged, not a configuration in the scratch checkout.
	tip := heavyCommit(t, root, "good")
	legacy := executable(t, `cat > "$TILLR_GATE_SPANS_OUT" <<'JSON'
{"check_spans":[{"check_id":"legacy","status":"CACHED","attributes_json":"{\"writes\":[\"value\"]}"}],"skipped_checks":[{"check_id":"external","reason":"fixture tool missing"},{"check_id":"fast","owed":true}]}
JSON`)
	steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: bin, RollingProgram: legacy, ReplayProgram: executable(t, "echo '{only}' >/dev/null\nexit 0")}
	var replay []HeavyCheck
	runner := HeavyRunner{Root: root, Work: filepath.Join(work, ".rolling", "worktree"), Trunk: TrunkRef, Judge: func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		checks, err := steps.JudgeHeavy(ctx, dir, only)
		if err != nil {
			return nil, err
		}
		replay, err = steps.JudgeHeavy(ctx, dir, "unit")
		return checks, err
	}}
	out, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Runs) != 1 || len(out.Runs[0].Checks) != 3 || out.Runs[0].Checks[0].OverBudgetMS == 0 {
		t.Fatalf("checks=%+v", out)
	}
	if green := heavyGit(t, root, "rev-parse", GreenRef); green != tip {
		t.Fatalf("green=%s", green)
	}
	// Selective judgment executes only the declaration named, under runner ownership.
	if len(replay) != 1 || replay[0].CheckID != "unit" {
		t.Fatalf("replay=%+v", replay)
	}
	_ = base
}
func TestHeavyCancellationEndsDeclarationChildrenBeforeRemovingTree(t *testing.T) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	bin := filepath.Join(work, "tillr-gate")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join("..", "..", "tillr-gate")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, raw)
	}
	checks := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checks, 0755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(work, "child.pid")
	marker := filepath.Join(work, "child-alive")
	writeForkFile(t, filepath.Join(checks, "unit.json"), `{"id":"unit","label":"unit","scope":"tree","tier":"heavy","budget_ms":1,"timeout_ms":1000,"command":["sh","-c","sleep 1000 & echo $! > `+child+`; touch `+marker+`; wait"],"reads":["value"],"remember":false}`)
	heavyCommit(t, root, "ready")
	steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: bin}
	runner := HeavyRunner{Root: root, Work: filepath.Join(work, ".rolling", "worktree"), Trunk: TrunkRef, Judge: steps.JudgeHeavy}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := runner.Run(ctx); done <- err }()
	deadline := time.After(20 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("ended before child: %v", err)
		case <-deadline:
			t.Fatal("child did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	raw, err := os.ReadFile(child)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancellation passed")
	}
	// ps can retain a zombie until PID 1 reaps it; it must not still be running.
	ps := exec.Command("ps", "-o", "stat=", "-p", strings.TrimSpace(string(raw)))
	state, _ := ps.Output()
	if len(strings.TrimSpace(string(state))) > 0 && !strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
		t.Fatalf("child remains alive: %s", state)
	}
	if _, err := os.Stat(runner.Work); !os.IsNotExist(err) {
		t.Fatalf("worktree remains: %v", err)
	}
}
