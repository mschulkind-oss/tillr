package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHeavyDeclarationsUseInjectedClockAndKeepOverrunVerdict(t *testing.T) {
	parent := os.Getenv("YOLO_DURABLE_DIR")
	if parent == "" {
		parent = filepath.Join("..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatal(err)
	}
	parent, err := filepath.Abs(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", parent)
	root, tree := memoryRepo(t)
	reg := &Registry{Digest: "fixture", Declarations: []Declaration{{ID: "slow", Scope: "tree", Tier: "heavy", BudgetMS: 10, TimeoutMS: 1000, Command: []string{"sh", "-c", "true"}, Watches: []Pattern{{Path: "input/"}}}, {ID: "change", Scope: "change", Tier: "fast"}, {ID: "fast", Scope: "tree", Tier: "fast"}}}
	if err := selectHeavyDeclarations(reg, ""); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(reg, Change{BaseSHA: "base", TreeSHA: tree}, root)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	clock := func() time.Time { calls++; return time.UnixMilli(int64(calls * 25)) }
	verdict, err := RunWith(plan, root, filepath.Join(t.TempDir(), "logs"), RunOptions{watchdog: &watchSources{Now: clock}})
	if err != nil {
		t.Fatal(err)
	}
	var payload VerdictPayload
	if err := json.Unmarshal(verdict.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Result != "pass" || len(payload.Checks) != 1 || payload.Checks[0].DurationMS != 25 || payload.Checks[0].OverBudgetMS != 15 {
		t.Fatalf("verdict=%+v", payload)
	}
}
func TestHeavyLegacyTimeoutRemainsATargetWithoutABudget(t *testing.T) {
	if target := checkTargetMS(PlanCheck{Tier: "heavy", TimeoutMS: 1000}); target != 1000 {
		t.Fatalf("legacy target = %d", target)
	}
}

func TestHeavySelectionRefusesMissingCheck(t *testing.T) {
	reg := &Registry{Declarations: []Declaration{{ID: "fast", Scope: "tree", Tier: "fast"}}}
	if err := selectHeavyDeclarations(reg, "fast"); err == nil {
		t.Fatal("accepted a non-heavy check")
	}
}
