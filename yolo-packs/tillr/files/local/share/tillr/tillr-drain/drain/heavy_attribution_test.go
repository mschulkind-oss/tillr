package drain

import (
	"context"
	"strings"
	"testing"
)

func TestHeavyGroupsEveryCheckAndRepairPathForOneLanding(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	red := heavyCommit(t, root, "bad")
	runner := heavyFixture(t, root, func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		result := "pass"
		if heavyGit(t, dir, "rev-parse", "HEAD") != base {
			result = "fail"
		}
		checks := []HeavyCheck{{CheckID: "one", Result: result, Reads: []string{"one/"}}, {CheckID: "two", Result: result, Reads: []string{"two/"}}}
		if only != "" {
			for _, check := range checks {
				if check.CheckID == only {
					return []HeavyCheck{check}, nil
				}
			}
		}
		return checks, nil
	})
	calls := 0
	runner.Fix = func(_ context.Context, sha string, checks []HeavyCheck) error {
		calls++
		if sha != red || len(checks) != 2 || checks[0].CheckID != "one" || checks[1].CheckID != "two" || checks[0].Reads[0] != "one/" || checks[1].Reads[0] != "two/" {
			t.Fatalf("fixer loses checks or paths: %s %+v", sha, checks)
		}
		return nil
	}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("fixers=%d", calls)
	}
}
func TestHeavyColdStartDoesNotClaimARedPointer(t *testing.T) {
	root, tip := heavyRepo(t)
	runner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
		return []HeavyCheck{{CheckID: "unit", Result: "fail"}}, nil
	})
	var fixes []string
	runner.Fix = func(_ context.Context, sha string, _ []HeavyCheck) error { fixes = append(fixes, sha); return nil }
	out, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	detail := out.Runs[0].Checks[0].Detail
	if !strings.Contains(detail, "no proved baseline") || strings.Contains(detail, "red at the green pointer") {
		t.Fatalf("false baseline claim: %s", detail)
	}
	// No baseline accuses no landing, and the red still gets its mover against the tip.
	if len(fixes) != 1 || fixes[0] != tip {
		t.Fatalf("fixers=%v, want one against tip %s", fixes, tip)
	}
}
