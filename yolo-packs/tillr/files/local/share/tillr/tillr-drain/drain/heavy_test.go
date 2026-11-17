package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func heavyGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func heavyRepo(t *testing.T) (string, string) {
	t.Helper()
	root := scratchUnderDurable(t)
	heavyGit(t, root, "init", "-q", "-b", "main")
	return root, heavyCommit(t, root, "good")
}
func heavyCommit(t *testing.T, root, value string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "value"), []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, root, "add", ".")
	heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", value)
	return heavyGit(t, root, "rev-parse", "HEAD")
}
func heavyFixture(t *testing.T, root string, judge func(context.Context, string, string) ([]HeavyCheck, error)) HeavyRunner {
	t.Helper()
	return HeavyRunner{Root: root, Work: filepath.Join(scratchUnderDurable(t), "worktree"), Trunk: "refs/heads/main", Judge: judge}
}
func TestHeavyCoalescesLandingsAndHasOneWriter(t *testing.T) {
	root, base := heavyRepo(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var tips []string
	runner := heavyFixture(t, root, func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		tip := heavyGit(t, dir, "rev-parse", "HEAD")
		if branch := heavyGit(t, dir, "branch", "--show-current"); branch != "" {
			t.Errorf("not detached: %s", branch)
		}
		mu.Lock()
		tips = append(tips, tip)
		n := len(tips)
		mu.Unlock()
		if n == 1 {
			close(entered)
			<-release
		}
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	})
	done := make(chan error, 1)
	go func() { _, err := runner.Run(context.Background()); done <- err }()
	<-entered
	other := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
		t.Error("second writer ran")
		return nil, nil
	})
	out, err := other.Run(context.Background())
	if err != nil || !out.Busy {
		t.Fatalf("second: %+v %v", out, err)
	}
	heavyCommit(t, root, "one")
	heavyCommit(t, root, "two")
	newest := heavyCommit(t, root, "three")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(tips) != 2 || tips[0] != base || tips[1] != newest {
		t.Fatalf("runs=%v", tips)
	}
	if green := heavyGit(t, root, "rev-parse", GreenRef); green != newest {
		t.Fatalf("green=%s", green)
	}
}
func TestHeavyRefusesRewrittenGreenAndCAS(t *testing.T) {
	root, base := heavyRepo(t)
	tip := heavyCommit(t, root, "new")
	heavyGit(t, root, "update-ref", GreenRef, base)
	runner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
		heavyGit(t, root, "update-ref", GreenRef, tip)
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	})
	if _, err := runner.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "compare-and-swap") {
		t.Fatalf("CAS=%v", err)
	}
	heavyGit(t, root, "checkout", "--orphan", "other")
	alien := heavyCommit(t, root, "alien")
	heavyGit(t, root, "checkout", "main")
	heavyGit(t, root, "update-ref", GreenRef, alien)
	runner.Judge = func(context.Context, string, string) ([]HeavyCheck, error) {
		t.Error("rewritten pointer ran")
		return nil, nil
	}
	if _, err := runner.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "not an ancestor") {
		t.Fatalf("rewrite=%v", err)
	}
	status, err := ReadHeavyStatus(context.Background(), root, "")
	if err != nil || !strings.Contains(status.Detail, "not an ancestor") {
		t.Fatalf("status=%+v %v", status, err)
	}
}
func TestHeavyAttributesFirstRedAndGateFault(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(map[bool]string{false: "landing", true: "gate-fault"}[fault], func(t *testing.T) {
			root, base := heavyRepo(t)
			red := heavyCommit(t, root, "bad")
			heavyCommit(t, root, "still bad")
			heavyGit(t, root, "update-ref", GreenRef, base)
			var fixes []string
			runner := heavyFixture(t, root, func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
				value, err := os.ReadFile(filepath.Join(dir, "value"))
				if err != nil {
					return nil, err
				}
				result := "pass"
				if fault || string(value) != "good" {
					result = "fail"
				}
				return []HeavyCheck{{CheckID: "unit", Result: result, Reads: []string{"value"}, Needs: []string{"fixture"}, Tools: []string{"sh"}}}, nil
			})
			runner.Fix = func(_ context.Context, sha string, checks []HeavyCheck) error { fixes = append(fixes, sha); return nil }
			out, err := runner.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			check := out.Runs[0].Checks[0]
			if fault {
				if len(fixes) != 0 || !check.GateFault {
					t.Fatalf("fault=%+v fixes=%v", check, fixes)
				}
			} else if check.FirstRedLanding != red || len(fixes) != 1 || fixes[0] != red {
				t.Fatalf("red=%+v fixes=%v", check, fixes)
			}
			if green := heavyGit(t, root, "rev-parse", GreenRef); green != base {
				t.Fatalf("red moved %s", green)
			}
		})
	}
}
func TestHeavyOverrunIsReportedButGreen(t *testing.T) {
	root, tip := heavyRepo(t)
	var runner HeavyRunner
	runner = heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
		started := runner.Now()
		measured := runner.Now().Sub(started).Milliseconds()
		return []HeavyCheck{{CheckID: "unit", Result: "pass", DurationMS: measured, BudgetMS: 100}, {CheckID: "optional", Result: "unavailable"}}, nil
	})
	at := time.UnixMilli(0)
	runner.Now = func() time.Time { at = at.Add(125 * time.Millisecond); return at }
	out, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Runs[0].Checks[0].OverBudgetMS != 25 {
		t.Fatalf("overrun=%+v", out)
	}
	status, err := ReadHeavyStatus(context.Background(), root, "")
	if err != nil || status.GreenCommit != tip || len(status.UnavailableChecks) != 1 || len(status.Overruns) != 1 {
		t.Fatalf("status=%+v %v", status, err)
	}
}
