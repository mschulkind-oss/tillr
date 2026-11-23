package heavyrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
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
func TestHeavyRecoversAnInitializingLockedDetachedWorktree(t *testing.T) {
	for _, tc := range []struct {
		name              string
		missing, relative bool
	}{
		{name: "directory-present"},
		{name: "directory-missing", missing: true},
		{name: "directory-missing-relative", missing: true, relative: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, base := heavyRepo(t)
			tip := heavyCommit(t, root, "new tip")
			runner := heavyFixture(t, root, func(_ context.Context, dir, _ string) ([]HeavyCheck, error) {
				if got := heavyGit(t, dir, "rev-parse", "HEAD"); got != tip {
					t.Errorf("proof HEAD=%s, want tip %s (abandoned HEAD was %s)", got, tip, base)
				}
				if branch := heavyGit(t, dir, "branch", "--show-current"); branch != "" {
					t.Errorf("proof is not detached: %s", branch)
				}
				return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
			})
			if err := os.MkdirAll(filepath.Dir(runner.Work), 0755); err != nil {
				t.Fatal(err)
			}
			add := []string{"worktree", "add", "--detach"}
			if tc.relative {
				add = append(add, "--relative-paths")
			}
			heavyGit(t, root, append(add, runner.Work, base)...)
			heavyGit(t, root, "worktree", "lock", "--reason", "initializing", runner.Work)
			lockPath := heavyGit(t, runner.Work, "rev-parse", "--path-format=absolute", "--git-path", "locked")
			// Date the interruption independently of the runner's current clock.
			lockedAt := time.Date(2026, time.November, 22, 2, 40, 29, 123456789, time.UTC)
			if err := os.Chtimes(lockPath, lockedAt, lockedAt); err != nil {
				t.Fatal(err)
			}
			lockInfo, err := os.Stat(lockPath)
			if err != nil {
				t.Fatal(err)
			}
			if tc.missing {
				if err := os.RemoveAll(runner.Work); err != nil {
					t.Fatal(err)
				}
			}

			// A broad prune would also remove this unrelated, unlocked registration.
			unrelated := filepath.Join(filepath.Dir(runner.Work), "unrelated")
			heavyGit(t, root, "worktree", "add", "--detach", unrelated, base)
			unrelatedGitDir := heavyGit(t, unrelated, "rev-parse", "--absolute-git-dir")
			old := time.Unix(1, 0)
			if err := os.Chtimes(filepath.Join(unrelatedGitDir, "gitdir"), old, old); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(unrelated); err != nil {
				t.Fatal(err)
			}
			var progress []string
			runner.Progress = func(line string) { progress = append(progress, line) }
			if _, err := runner.Run(context.Background()); err != nil {
				t.Fatalf("recover initializing lock: %v", err)
			}
			if green := heavyGit(t, root, "rev-parse", GreenRef); green != tip {
				t.Fatalf("green=%s, want %s", green, tip)
			}
			recoveryLines := 0
			for _, line := range strings.Split(strings.Join(progress, ""), "\n") {
				if !strings.Contains(line, "initializing") {
					continue
				}
				recoveryLines++
				if !strings.Contains(line, runner.Work) || !strings.Contains(line, lockInfo.ModTime().UTC().Format(time.RFC3339Nano)) {
					t.Errorf("recovery line must name path, reason and lock mtime: %q", line)
				}
			}
			if recoveryLines != 1 {
				t.Errorf("recovery lines=%d, want one: %q", recoveryLines, progress)
			}
			if listing := heavyGit(t, root, "worktree", "list", "--porcelain"); !strings.Contains(listing, "worktree "+unrelated+"\n") {
				t.Errorf("unrelated missing worktree registration not retained:\n%s", listing)
			}
		})
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
				if out.Runs[0].Result != "red" || fixes != 1 || out.Runs[0].Checks[0].Stalls != 2 || out.Runs[0].Checks[0].FirstRedLanding != "" {
					t.Fatalf("second stall=%+v fixes=%d", out, fixes)
				}
				if green, err := heavy.Green(context.Background(), root); err != nil || green != "" {
					t.Fatalf("stall moved %s %v", green, err)
				}
			}
		})
	}
}
