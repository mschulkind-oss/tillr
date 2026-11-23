package heavyrun

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
)

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
			tip := heavyCommit(t, root, "still bad")
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
				// Red at the pointer accuses no landing, but the red still gets a
				// mover: one fixer against the tip (graph design, Section 8.2).
				if len(fixes) != 1 || fixes[0] != tip || !check.GateFault || check.FirstRedLanding != "" {
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
func TestHeavyFirstRedSkipsCommitsNotTouchingReadsAndFilesAgainstTipWhenNoneTouch(t *testing.T) {
	t.Run("skips non-intersecting commits", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		if err := os.WriteFile(filepath.Join(root, "docs.md"), []byte("one\n"), 0644); err != nil {
			t.Fatal(err)
		}
		heavyGit(t, root, "add", "docs.md")
		heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "docs1")
		doc1 := heavyGit(t, root, "rev-parse", "HEAD")

		culprit := heavyCommit(t, root, "bad")

		if err := os.WriteFile(filepath.Join(root, "docs.md"), []byte("two\n"), 0644); err != nil {
			t.Fatal(err)
		}
		heavyGit(t, root, "add", "docs.md")
		heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "docs2")
		doc2 := heavyGit(t, root, "rev-parse", "HEAD")

		var replayed []string
		var fixes []string
		runner := heavyFixture(t, root, func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
			sha := heavyGit(t, dir, "rev-parse", "HEAD")
			if only != "" {
				replayed = append(replayed, sha)
			}
			value, err := os.ReadFile(filepath.Join(dir, "value"))
			if err != nil {
				return nil, err
			}
			result := "pass"
			if string(value) != "good" {
				result = "fail"
			}
			return []HeavyCheck{{CheckID: "unit", Result: result, Reads: []string{"value"}}}, nil
		})
		runner.Fix = func(_ context.Context, sha string, _ []HeavyCheck) error {
			fixes = append(fixes, sha)
			return nil
		}
		out, err := runner.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if out.Runs[0].Checks[0].FirstRedLanding != culprit {
			t.Fatalf("firstRed=%s, want %s", out.Runs[0].Checks[0].FirstRedLanding, culprit)
		}
		if len(fixes) != 1 || fixes[0] != culprit {
			t.Fatalf("fixes=%v, want [%s]", fixes, culprit)
		}
		// The first parent is required evidence even when it did not touch the
		// declared reads. The later unrelated commit is still skipped.
		if strings.Join(replayed, " ") != strings.Join([]string{base, culprit, doc1}, " ") {
			t.Fatalf("replayed=%v, want base %s, candidate %s and parent %s, but not %s", replayed, base, culprit, doc1, doc2)
		}
	})

	t.Run("no commit touches reads", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		if err := os.WriteFile(filepath.Join(root, "docs.md"), []byte("one\n"), 0644); err != nil {
			t.Fatal(err)
		}
		heavyGit(t, root, "add", "docs.md")
		heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "docs1")
		tip := heavyGit(t, root, "rev-parse", "HEAD")

		var replayed []string
		var fixes []string
		var fixedChecks []HeavyCheck
		runner := heavyFixture(t, root, func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
			sha := heavyGit(t, dir, "rev-parse", "HEAD")
			if only != "" {
				replayed = append(replayed, sha)
			}
			result := "pass"
			if sha == tip {
				result = "fail"
			}
			return []HeavyCheck{{CheckID: "unit", Result: result, Reads: []string{"value"}}}, nil
		})
		runner.Fix = func(_ context.Context, sha string, checks []HeavyCheck) error {
			fixes = append(fixes, sha)
			fixedChecks = append(fixedChecks, checks...)
			return nil
		}
		out, err := runner.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(replayed) != 0 {
			t.Fatalf("expected no replays when no commit touches reads, got %v", replayed)
		}
		check := out.Runs[0].Checks[0]
		if !check.GateFault || check.FirstRedLanding != "" {
			t.Fatalf("expected gate fault with no named landing, got %+v", check)
		}
		if !strings.Contains(check.Detail, "touches the check's declared reads") {
			t.Fatalf("detail does not explain no commit touches reads: %q", check.Detail)
		}
		if len(fixes) != 1 || fixes[0] != tip || len(fixedChecks) != 1 {
			t.Fatalf("fixes=%v checks=%+v, want one against tip %s", fixes, fixedChecks, tip)
		}
	})
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

func TestHeavyRunnerDefaultRegistryLocksHoldLandAndRegistryLocks(t *testing.T) {
	root, _ := heavyRepo(t)
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := scratchUnderDurable(t)
	script := fmt.Sprintf(`#!/usr/bin/env python3
import fcntl, os, sys
args=sys.argv[1:]
def assert_locked(name, action):
    try:
        with open(os.path.join(os.environ['TILLR_TEST_COMMON'],name),'r') as lock:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
    except BlockingIOError:
        return
    except FileNotFoundError:
        pass
    sys.exit('registry command ran without '+name+': '+action)

if 'worktree' in args:
    action=args[args.index('worktree')+1]
    if action in ('add','remove','prune'):
        assert_locked('worktree-registry.lock',action)
        assert_locked('land.lock',action)
os.execv(%q,[%q]+args)
`, real, real)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_TEST_COMMON", filepath.Join(root, ".git"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	runner := heavyFixture(t, root, func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		lock, err := land.OpenLock(filepath.Join(root, ".git", "land.lock"))
		if err != nil {
			return nil, err
		}
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, err := lock.Acquire(bounded, 0); err != nil {
			return nil, err
		}
		lock.Release()
		return []HeavyCheck{{CheckID: "fixture", Result: "pass"}}, nil
	})
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}
