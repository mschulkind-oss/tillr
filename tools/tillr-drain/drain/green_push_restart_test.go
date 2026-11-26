package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/settings"
)

func TestSuccessorPublishesGreenWhileNextProofRuns(t *testing.T) {
	h := newRestartHarness(t, []byte("next build"), func(context.Context, string) error { return nil }, "")
	h.l.cfg.Report = &strings.Builder{}
	root := h.f.root
	remote := filepath.Join(t.TempDir(), "upstream.git")
	runGit(t, root, "init", "--bare", remote)
	runGit(t, root, "remote", "add", "origin", remote)
	runGit(t, root, "push", "-u", "origin", TrunkRef)
	remoteTip := func() string { return heavyGit(t, root, "--git-dir="+remote, "rev-parse", "refs/heads/"+TrunkRef) }
	base := remoteTip()
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip := heavyCommit(t, root, "proved")
	recordFixtureLandings(t, h.f.storePath, tip)
	h.l.cfg.PostLanding.Push.What = settings.PushGreen
	h.l.gatePolicy.LandingStrategy = "after-merge"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	h.l.cfg.Steps = &gateCountingHeavySteps{judge: func(ctx context.Context, _, _ string) ([]HeavyCheck, error) {
		close(entered)
		select {
		case <-release:
			return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	if err := h.l.startHeavy(ctx, tip); err != nil {
		t.Fatal(err)
	}
	awaitRestartTest(t, entered, "first proof")
	req := h.request(t, 1)[0]
	h.l.offerStartupGreen(ctx)
	if h.l.greenPushAtStart.Load() {
		t.Fatal("startup publication was offered while the restart was pending")
	}
	next := heavyCommit(t, root, "not yet proved")
	recordFixtureLandings(t, h.f.storePath, next)
	close(release)
	h.l.heavyWG.Wait()
	if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); !handled || stop != StopRestarted || err != nil {
		t.Fatalf("gateRestart = %v, %q, %v; want replacement", handled, stop, err)
	}
	if got := heavyGit(t, root, "rev-parse", GreenRef); got != tip || remoteTip() != base {
		t.Fatalf("at replacement: green=%s, remote=%s; want %s, %s", got, remoteTip(), tip, base)
	}

	t.Setenv(restart.RequestEnv, req.ID)
	t.Setenv(restart.FromEnv, "old-build")
	enteredNext := make(chan struct{})
	cfg := h.l.cfg
	cfg.Report = &strings.Builder{}
	cfg.Steps = &gateCountingHeavySteps{judge: func(ctx context.Context, _, _ string) ([]HeavyCheck, error) {
		close(enteredNext)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	successor, err := NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	successor.gatePolicy = h.l.gatePolicy
	done := make(chan struct{})
	var loopErr error
	go func() { defer close(done); _, loopErr = successor.Run(ctx) }()
	defer func() {
		cancel()
		awaitRestartTest(t, done, "successor exit")
	}()
	select {
	case <-enteredNext:
	case <-done:
		t.Fatalf("successor exited before its next proof: %v", loopErr)
	case <-time.After(hangGuard):
		t.Fatal("successor did not start the next proof")
	}
	waitAction(t, &successor.pushAction)
	if got := remoteTip(); got != tip {
		t.Errorf("remote while proof of %s is running = %s, want %s", next, got, tip)
	}
	if got := strings.Count(h.l.reported(), "loop: push of green "+shortSHA(tip)+" deferred to the replacement loop"); got != 1 {
		t.Errorf("old loop logged %d deferrals, want 1: %s", got, h.l.reported())
	}
	if got := strings.Count(successor.reported(), "loop: pushed green "+shortSHA(tip)); got != 1 {
		t.Errorf("successor logged %d pushes, want 1: %s", got, successor.reported())
	}
}

func TestGreenAdvanceWithPushDisabledIsReported(t *testing.T) {
	l, git, commit := actionFixture(t)
	tip := commit("proved")
	git("update-ref", GreenRef, tip)
	l.offerGreenAdvance(context.Background(), tip)
	if l.pushAction.busy() || strings.Count(l.reported(), "loop: push of green "+shortSHA(tip)+" skipped: [push] what is not green") != 1 {
		t.Fatalf("expected a skip and no push, got: %s", l.reported())
	}
}

func TestGreenPushReportsCoalescedAdvance(t *testing.T) {
	l, git, commit := actionFixture(t)
	remote := filepath.Join(t.TempDir(), "upstream.git")
	git("init", "--bare", remote)
	git("remote", "add", "origin", remote)
	git("push", "-u", "origin", TrunkRef)
	l.cfg.PostLanding.Push.What = settings.PushGreen
	ctx := context.Background()
	hook := filepath.Join(l.cfg.Root, ".git", "hooks", "pre-push")
	if err := writeForkSafe(hook, []byte("#!/bin/sh\ntouch push-started\nwhile [ ! -f release-push ]; do sleep 0.01; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	release := func() {
		if err := os.WriteFile(filepath.Join(l.cfg.Root, "release-push"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { release(); waitAction(t, &l.pushAction) }()
	active := commit("active proof")
	git("update-ref", GreenRef, active)
	l.afterGreen(ctx, active)
	waitActionCondition(t, &l.pushAction, func() bool {
		_, err := os.Stat(filepath.Join(l.cfg.Root, "push-started"))
		return err == nil
	})
	first := commit("first proof")
	git("update-ref", GreenRef, first)
	l.afterGreen(ctx, first)
	latest := commit("latest proof")
	git("update-ref", GreenRef, latest)
	l.afterGreen(ctx, latest)
	release()
	waitAction(t, &l.pushAction)

	if got := git("--git-dir="+remote, "rev-parse", "refs/heads/"+TrunkRef); got != latest {
		t.Fatalf("remote = %s, want %s", got, latest)
	}
	for _, line := range []string{
		"loop: push of green " + shortSHA(first) + " skipped: superseded by green " + shortSHA(latest),
		"loop: pushed green " + shortSHA(latest),
	} {
		if got := strings.Count(l.reported(), line); got != 1 {
			t.Errorf("want one %q, got %d: %s", line, got, l.reported())
		}
	}

	// A push turn can also find that another publisher already advanced the remote.
	l.afterGreen(ctx, latest)
	waitAction(t, &l.pushAction)
	if got := strings.Count(l.reported(), "loop: push of green "+shortSHA(latest)+" skipped: remote already holds the green commit"); got != 1 {
		t.Errorf("want one already-published outcome, got %d: %s", got, l.reported())
	}
}
