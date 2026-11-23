package heavyrun

import (
	"context"
	"testing"
)

func TestHeavySuccessfulStallRetryIsDurableBeforeNextRetry(t *testing.T) {
	root, _ := heavyRepo(t)
	progressDir := scratchUnderDurable(t)
	tip := heavyGit(t, root, "rev-parse", "HEAD")
	first := true
	calls := 0
	runner := heavyFixture(t, root, func(ctx context.Context, _ string, only string) ([]HeavyCheck, error) {
		if only == "a" {
			calls++
			return []HeavyCheck{{CheckID: "a", Result: "pass"}}, nil
		}
		if only == "b" && first {
			return nil, context.Canceled
		}
		if only == "b" {
			return []HeavyCheck{{CheckID: "b", Result: "pass"}}, nil
		}
		checks := []HeavyCheck{}
		if pass, ok := ReuseHeavyCheck(ctx, "a"); ok {
			checks = append(checks, pass)
		} else {
			calls++
			checks = append(checks, HeavyCheck{CheckID: "a", Result: "stalled"})
		}
		return append(checks, HeavyCheck{CheckID: "b", Result: "stalled"}), nil
	})
	p, err := LoadHeavyProgress(context.Background(), progressDir, progressDir, tip)
	if err != nil {
		t.Fatal(err)
	}
	run := HeavyRun{TipSHA: tip}
	if _, err := runner.judgeTip(context.WithValue(context.Background(), HeavyProgressKey{}, p), run); err == nil {
		t.Fatal("interruption passed")
	}
	first = false
	p, err = LoadHeavyProgress(context.Background(), progressDir, progressDir, tip)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.judgeTip(context.WithValue(context.Background(), HeavyProgressKey{}, p), run); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("a ran %d times; completed retry must be reused", calls)
	}
}
