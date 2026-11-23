package heavyrun

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHeavyProgressOnlyReusesPassOnExactTip(t *testing.T) {
	dir := scratchUnderDurable(t)
	p, err := LoadHeavyProgress(context.Background(), dir, dir, "tip-one")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), HeavyProgressKey{}, p)
	for _, result := range []string{"pass", "fail", "unavailable", "stalled"} {
		if err := SaveHeavyCheck(ctx, HeavyCheck{CheckID: result, Result: result}); err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := LoadHeavyProgress(context.Background(), dir, dir, "tip-one")
	if err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(context.Background(), HeavyProgressKey{}, restarted)
	for _, result := range []string{"pass", "fail", "unavailable", "stalled"} {
		_, ok := ReuseHeavyCheck(ctx, result)
		if ok != (result == "pass") {
			t.Fatalf("reused %s: %v", result, ok)
		}
	}
	next, err := LoadHeavyProgress(context.Background(), dir, dir, "tip-two")
	if err != nil || len(next.Checks) != 0 {
		t.Fatalf("different tip reused: %+v %v", next, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "heavy-progress.json"), []byte("broken"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHeavyProgress(context.Background(), dir, dir, "tip-one"); err == nil {
		t.Fatal("corrupt proof silently discarded")
	}
}

func TestHeavyAttributionDoesNotReuseTipProof(t *testing.T) {
	root, base := heavyRepo(t)
	dir := scratchUnderDurable(t)
	p := &HeavyProgress{TipSHA: base, Checks: map[string]HeavyCheck{"unit": {CheckID: "unit", Result: "pass"}}, dir: dir}
	calls := 0
	runner := heavyFixture(t, root, func(ctx context.Context, _ string, id string) ([]HeavyCheck, error) {
		if _, ok := ReuseHeavyCheck(ctx, id); ok {
			t.Fatal("attribution used saved tip proof")
		}
		calls++
		return []HeavyCheck{{CheckID: id, Result: "fail"}}, nil
	})
	red, err := runner.one(context.WithValue(context.Background(), HeavyProgressKey{}, p), base, "unit")
	if err != nil || !red || calls != 1 {
		t.Fatalf("red=%v calls=%d err=%v", red, calls, err)
	}
}
