package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHeavyRecoversAnAbandonedDetachedWorktree(t *testing.T) {
	root, tip := heavyRepo(t)
	runner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	})
	if err := os.MkdirAll(filepath.Dir(runner.Work), 0755); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, root, "worktree", "add", "--detach", runner.Work, tip)
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if green := heavyGit(t, root, "rev-parse", GreenRef); green != tip {
		t.Fatalf("green=%s", green)
	}
}
func TestHeavyOnlyASecondStallIsRed(t *testing.T) {
	for _, second := range []string{"pass", "stalled"} {
		t.Run(second, func(t *testing.T) {
			root, tip := heavyRepo(t)
			calls, fixes := 0, 0
			runner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
				calls++
				result := "stalled"
				if calls > 1 {
					result = second
				}
				return []HeavyCheck{{CheckID: "unit", Result: result}}, nil
			})
			runner.Fix = func(_ context.Context, sha string, _ []HeavyCheck) error {
				if sha != tip {
					t.Errorf("stall on %s attributed to %s", tip, sha)
				}
				fixes++
				return nil
			}
			out, err := runner.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("attempts=%d", calls)
			}
			if second == "pass" {
				if out.Runs[0].Result != "green" || fixes != 0 || out.Runs[0].Checks[0].Stalls != 1 {
					t.Fatalf("first stall=%+v fixes=%d", out, fixes)
				}
			} else {
				if out.Runs[0].Result != "red" || fixes != 1 || out.Runs[0].Checks[0].Stalls != 2 {
					t.Fatalf("second stall=%+v fixes=%d", out, fixes)
				}
				if green, err := heavyGreen(context.Background(), root); err != nil || green != "" {
					t.Fatalf("stall moved %s %v", green, err)
				}
			}
		})
	}
}
func TestHeavyProofRefMovementDoesNotConvictALane(t *testing.T) {
	if !landerRef(GreenRef) {
		t.Fatal("heavy proof ref is not recognized as runner-owned")
	}
	if landerRef("refs/tillr/unassigned") {
		t.Fatal("excused an unassigned ref")
	}
}
