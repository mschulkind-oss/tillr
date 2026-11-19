package main_test

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
)

func TestRedCapOneSamplesClockAfterGate(t *testing.T) {
	f := newContentionFixture(t)
	git(t, f.f.dir, "update-ref", "refs/tillr/green", f.f.baseSHA)
	f.f.advance(t)
	write(t, filepath.Join(f.f.dir, "config/tillr/gate-policy.json"), `{"landing_strategy":"after-merge","red_cap":{"landings":0,"minutes":1}}`)
	seconds, err := strconv.ParseInt(git(t, f.f.dir, "show", "-s", "--format=%ct", f.f.baseSHA), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	req := land.OneRequest{Branch: "candidate", Trunk: f.f.ref, Item: f.req.ItemID, Run: "one-clock", Gate: strings.Fields(gateScript(t)), At: time.Unix(seconds+59, 0)}
	got, err := land.One(context.Background(), f.s, f.trunk, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != land.ResultLanded {
		t.Fatalf("expected landing without red cap: %+v", got)
	}
}

func TestRedCapSamplesLiveClockUnderLandingLock(t *testing.T) {
	f := newContentionFixture(t)
	git(t, f.f.dir, "update-ref", "refs/tillr/green", f.f.baseSHA)
	f.f.advance(t)
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
	// A Git barrier proves the request started while the lock was held.
	entered, release := stallGit(t, "cat-file")
	done := startLanding(t, context.Background(), f)
	awaitGit(t, entered)
	write(t, release, "")
	lock.Release()
	answer := finishLanding(t, done)
	if answer.err != nil {
		t.Fatal(answer.err)
	}
	if answer.landing.Result != land.ResultLanded {
		t.Fatalf("expected landing without red cap: %+v", answer.landing)
	}
}
