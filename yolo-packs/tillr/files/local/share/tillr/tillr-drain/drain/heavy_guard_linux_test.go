//go:build linux

package drain

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func startHeldProcess(t *testing.T) (heavyProcess, func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "read _ || true")
	cmd.Stdin = r
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		t.Fatal(err)
	}
	r.Close()
	proc, err := readHeavyProcess(cmd.Process.Pid)
	if err != nil {
		w.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			w.Close()
			_ = cmd.Wait()
		})
	}
	t.Cleanup(stop)
	return proc, stop
}

func writeSupervisorMarker(t *testing.T, guard string, pid int, startTicks, checkout string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(guard), 0755); err != nil {
		t.Fatal(err)
	}
	body := (heavyCleanupMarker{PID: pid, StartTicks: startTicks, Checkout: checkout}).format()
	if err := os.WriteFile(guard, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestHeavyCleanupGuardRecoversDeadSupervisorAndHoldsLiveSupervisor(t *testing.T) {
	t.Run("dead-supervisor", func(t *testing.T) {
		root, tip := heavyRepo(t)
		calls := 0
		runner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
			calls++
			return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
		})
		abandoned := filepath.Join(scratchUnderDurable(t), "abandoned-worktree")
		heavyGit(t, root, "worktree", "add", "--detach", abandoned, tip)
		canonical, err := filepath.EvalSymlinks(abandoned)
		if err != nil {
			t.Fatal(err)
		}
		proc, stop := startHeldProcess(t)
		stop()

		guard := filepath.Join(root, ".git", "tillr-gate", heavyCleanupGuard)
		writeSupervisorMarker(t, guard, proc.pid, proc.start, abandoned)
		if err := os.WriteFile(guard+".start-error", []byte("stale start error"), 0600); err != nil {
			t.Fatal(err)
		}

		if _, err := runner.Run(context.Background()); err != nil {
			t.Fatalf("dead supervisor blocked proof: %v", err)
		}
		if calls != 1 {
			t.Fatalf("calls=%d, want 1", calls)
		}
		if _, err := os.Stat(guard); !os.IsNotExist(err) {
			t.Fatalf("guard remained: %v", err)
		}
		if _, err := os.Stat(guard + ".start-error"); !os.IsNotExist(err) {
			t.Fatalf("start-error remained: %v", err)
		}
		if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
			t.Fatalf("abandoned checkout remained: %v", err)
		}
		if list := heavyGit(t, root, "worktree", "list", "--porcelain"); strings.Contains(list, "worktree "+canonical+"\n") {
			t.Fatalf("abandoned worktree remained registered:\n%s", list)
		}
		if green := heavyGit(t, root, "rev-parse", GreenRef); green != tip {
			t.Fatalf("green=%s, want %s", green, tip)
		}
	})

	t.Run("live-supervisor", func(t *testing.T) {
		root, tip := heavyRepo(t)
		calls := 0
		runner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
			calls++
			return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
		})
		retained := filepath.Join(scratchUnderDurable(t), "live-worktree")
		heavyGit(t, root, "worktree", "add", "--detach", retained, tip)
		canonical, err := filepath.EvalSymlinks(retained)
		if err != nil {
			t.Fatal(err)
		}
		proc, stop := startHeldProcess(t)

		guard := filepath.Join(root, ".git", "tillr-gate", heavyCleanupGuard)
		writeSupervisorMarker(t, guard, proc.pid, proc.start, retained)

		if _, err := runner.Run(context.Background()); !cleanupLost(err) {
			t.Fatalf("live supervisor err=%v, want cleanupLost", err)
		}
		if calls != 0 {
			t.Fatalf("live supervisor bypassed: calls=%d", calls)
		}
		if _, err := os.Stat(guard); err != nil {
			t.Fatalf("live supervisor guard removed: %v", err)
		}
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("live supervisor checkout removed: %v", err)
		}
		if list := heavyGit(t, root, "worktree", "list", "--porcelain"); !strings.Contains(list, "worktree "+canonical+"\n") {
			t.Fatalf("live supervisor worktree deregistered:\n%s", list)
		}

		stop()
		if _, err := runner.Run(context.Background()); err != nil {
			t.Fatalf("recovery after supervisor exit err=%v", err)
		}
		if calls != 1 {
			t.Fatalf("calls after supervisor exit=%d, want 1", calls)
		}
		if _, err := os.Stat(guard); !os.IsNotExist(err) {
			t.Fatalf("guard remained after exit: %v", err)
		}
		if _, err := os.Stat(retained); !os.IsNotExist(err) {
			t.Fatalf("checkout remained after exit: %v", err)
		}
		if green := heavyGit(t, root, "rev-parse", GreenRef); green != tip {
			t.Fatalf("green=%s, want %s", green, tip)
		}
	})

	t.Run("reused-pid-different-start-ticks", func(t *testing.T) {
		root, tip := heavyRepo(t)
		calls := 0
		runner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
			calls++
			return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
		})
		abandoned := filepath.Join(scratchUnderDurable(t), "reused-pid-worktree")
		heavyGit(t, root, "worktree", "add", "--detach", abandoned, tip)
		proc, _ := startHeldProcess(t)
		otherStart := proc.start + "1"

		guard := filepath.Join(root, ".git", "tillr-gate", heavyReplayGuard)
		writeSupervisorMarker(t, guard, proc.pid, otherStart, abandoned)

		if _, err := runner.Run(context.Background()); err != nil {
			t.Fatalf("reused pid blocked proof: %v", err)
		}
		if calls != 1 {
			t.Fatalf("calls=%d, want 1", calls)
		}
		if _, err := os.Stat(guard); !os.IsNotExist(err) {
			t.Fatalf("replay guard remained: %v", err)
		}
		if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
			t.Fatalf("abandoned replay checkout remained: %v", err)
		}
		if current, err := readHeavyProcess(proc.pid); err != nil || current.start != proc.start || current.state == "Z" {
			t.Fatalf("unrelated process sharing pid was disturbed: %+v / %v", current, err)
		}
		if green := heavyGit(t, root, "rev-parse", GreenRef); green != tip {
			t.Fatalf("green=%s, want %s", green, tip)
		}
	})

	t.Run("rolling-ownership", func(t *testing.T) {
		root, tip := heavyRepo(t)
		retained := filepath.Join(scratchUnderDurable(t), "rolling-worktree")
		heavyGit(t, root, "worktree", "add", "--detach", retained, tip)
		proc, stop := startHeldProcess(t)
		guard := filepath.Join(root, ".git", "tillr-gate", heavyCleanupGuard)
		writeSupervisorMarker(t, guard, proc.pid, proc.start, retained)

		if _, release, err := rollingOwnership(context.Background(), root); err == nil {
			release()
			t.Fatal("live supervisor allowed rolling ownership")
		}
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("live supervisor rolling checkout removed: %v", err)
		}
		stop()

		_, release, err := rollingOwnership(context.Background(), root)
		if err != nil {
			t.Fatalf("dead supervisor blocked rolling ownership: %v", err)
		}
		release()
		if _, err := os.Stat(guard); !os.IsNotExist(err) {
			t.Fatalf("rolling guard remained: %v", err)
		}
		if _, err := os.Stat(retained); !os.IsNotExist(err) {
			t.Fatalf("rolling checkout remained: %v", err)
		}
	})

	t.Run("refuses-overlapping-root", func(t *testing.T) {
		root, _ := heavyRepo(t)
		runner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
			t.Fatal("judge ran on overlapping checkout")
			return nil, nil
		})
		proc, stop := startHeldProcess(t)
		stop()
		guard := filepath.Join(root, ".git", "tillr-gate", heavyCleanupGuard)
		writeSupervisorMarker(t, guard, proc.pid, proc.start, root)

		if _, err := runner.Run(context.Background()); !cleanupLost(err) {
			t.Fatalf("err=%v, want cleanupLost", err)
		}
		if _, err := os.Stat(filepath.Join(root, "value")); err != nil {
			t.Fatalf("repository root modified: %v", err)
		}
	})
}

func TestHeavyLoopRecoversCleanupGuardAfterSupervisorExits(t *testing.T) {
	root, tip := heavyRepo(t)
	work := scratchUnderDurable(t)
	s, err := store.InitFixture(context.Background(), filepath.Join(work, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	abandoned := filepath.Join(work, ".rolling", "worktree")
	if err := os.MkdirAll(filepath.Dir(abandoned), 0755); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, root, "worktree", "add", "--detach", abandoned, tip)
	proc, stop := startHeldProcess(t)
	guard := filepath.Join(root, ".git", "tillr-gate", heavyCleanupGuard)
	writeSupervisorMarker(t, guard, proc.pid, proc.start, abandoned)

	var log bytes.Buffer
	steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	}}
	l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: work, Steps: steps, Now: time.Now, Report: &log}}

	if err := l.maybeRolling(context.Background(), tip, false, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	if green, err := heavyGreen(context.Background(), root); err != nil || green != "" {
		t.Fatalf("green advanced while supervisor live: %q / %v", green, err)
	}

	stop()
	if err := l.maybeRolling(context.Background(), tip, false, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	if green := heavyGit(t, root, "rev-parse", GreenRef); green != tip {
		t.Fatalf("green=%s, want %s\nlog:\n%s", green, tip, log.String())
	}
	if _, err := os.Stat(guard); !os.IsNotExist(err) {
		t.Fatalf("guard remained: %v", err)
	}
}
