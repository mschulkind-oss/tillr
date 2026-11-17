package main_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
)

func TestRedCapOneSamplesClockAfterGate(t *testing.T) {
	f := newContentionFixture(t)
	git(t, f.f.dir, "update-ref", "refs/tillr/green", f.f.baseSHA)
	tip := f.f.advance(t)
	write(t, filepath.Join(f.f.dir, "config/tillr/gate-policy.json"), `{"landing_strategy":"after-merge","red_cap":{"landings":0,"minutes":1}}`)
	seconds, err := strconv.ParseInt(git(t, f.f.dir, "show", "-s", "--format=%ct", f.f.baseSHA), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(scratch(t, "gate-probe"), "probe")
	t.Setenv("LAND_PROBE", probe)
	req := land.OneRequest{Branch: "candidate", Trunk: f.f.ref, Item: f.req.ItemID, Run: "one-clock", Gate: strings.Fields(gateScript(t)), At: time.Unix(seconds+59, 0), CapNow: func() time.Time {
		if raw, err := os.ReadFile(probe); err != nil || string(raw) != "free" {
			t.Errorf("minute clock sampled before gate completed: %q / %v", raw, err)
		}
		return time.Unix(seconds+61, 0)
	}}
	got, err := land.One(context.Background(), f.s, f.trunk, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != land.ResultRefused || got.Reason != land.ReasonRedCap || git(t, f.f.dir, "rev-parse", f.f.ref) != tip {
		t.Fatalf("gate-start clock authorized move: %+v", got)
	}
}

func TestRedCapSamplesLiveClockUnderLandingLock(t *testing.T) {
	f := newContentionFixture(t)
	git(t, f.f.dir, "update-ref", "refs/tillr/green", f.f.baseSHA)
	tip := f.f.advance(t)
	write(t, filepath.Join(f.f.dir, "config/tillr/gate-policy.json"), `{"landing_strategy":"after-merge","red_cap":{"landings":0,"minutes":1}}`)
	seconds, err := strconv.ParseInt(git(t, f.f.dir, "show", "-s", "--format=%ct", f.f.baseSHA), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	f.req.At = time.Unix(seconds+59, 0)
	// Holding the lock models a request whose authorization clock cannot be
	// sampled until another landing releases it. No sleep or elapsed-time assertion.
	lock, err := f.trunk.LandLock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Acquire(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	var released atomic.Bool
	f.req.CapNow = func() time.Time {
		if !released.Load() {
			t.Error("minute clock sampled before landing lock acquired")
		}
		return time.Unix(seconds+61, 0)
	}
	// A Git barrier proves the request started while the lock was held.
	entered, release := stallGit(t, "cat-file")
	done := startLanding(t, context.Background(), f)
	awaitGit(t, entered)
	write(t, release, "")
	released.Store(true)
	lock.Release()
	answer := finishLanding(t, done)
	if answer.err != nil {
		t.Fatal(answer.err)
	}
	if answer.landing.Result != land.ResultRefused || answer.landing.Reason != land.ReasonRedCap || git(t, f.f.dir, "rev-parse", f.f.ref) != tip {
		t.Fatalf("stale record clock authorized move: %+v", answer.landing)
	}
}
