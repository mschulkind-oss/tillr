package heavyrun

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHeavyUnconfirmedCleanupRetainsTreeAndBlocksRecovery(t *testing.T) {
	root, _ := heavyRepo(t)
	guard := filepath.Join(root, ".git", "tillr-gate", HeavyCleanupGuard)
	runner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
		if err := os.WriteFile(guard, []byte("fixture cleanup not acknowledged"), 0600); err != nil {
			t.Fatal(err)
		}
		return nil, &heavyCleanupUnconfirmed{detail: "fixture cleanup unconfirmed"}
	})
	if _, err := runner.Run(context.Background()); !CleanupLost(err) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(runner.Work); err != nil {
		t.Fatalf("unconfirmed checkout removed: %v", err)
	}
	calls := 0
	runner.Judge = func(context.Context, string, string) ([]HeavyCheck, error) {
		calls++
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	}
	if _, err := runner.Run(context.Background()); !CleanupLost(err) {
		t.Fatalf("recovery err=%v", err)
	}
	if calls != 0 {
		t.Fatalf("unconfirmed cleanup bypassed: calls=%d", calls)
	}
	if _, err := os.Stat(runner.Work); err != nil {
		t.Fatalf("blocked recovery removed checkout: %v", err)
	}
	// This fixture never spawned a check process; independent cleanup is known done.
	if err := os.Remove(guard); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("recovery calls=%d", calls)
	}
}
