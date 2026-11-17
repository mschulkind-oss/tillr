package main_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestForkDeliveryRequiresABuildCheckInTheVerdict(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "branch", "patch/pi", f.baseSHA)
	verdict := f.verdict("pass")
	verdict["checks"] = []map[string]any{{"check_id": "go-test", "result": "pass"}}
	path := input(t, envelope(t, "gate.verdict", "verdict_001", verdict))
	_, code, out := f.land(t, "run-no-build", "pass", "--base-ref", "patch/pi", "--verdict", path)
	if code != 1 || decodeLanding(t, out).Payload.Reason != "verdict-mismatch" {
		t.Fatalf("buildless verdict exited %d: %s", code, out)
	}
	if tip := git(t, f.dir, "rev-parse", "patch/pi"); tip != f.baseSHA {
		t.Fatalf("buildless verdict moved branch to %s", tip)
	}
}

func TestForkDeliveryNeverOverwritesDirtyCacheWork(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "branch", "patch/pi", f.baseSHA)
	cache := filepath.Join(f.dir, ".forks/pi")
	git(t, f.dir, "clone", "--quiet", "--no-checkout", f.dir, cache)
	git(t, cache, "checkout", "-b", "main", f.baseSHA)
	write(t, filepath.Join(cache, "candidate.txt"), "unlanded cache work\n")
	code, out, stderr := passBoth(t, f.passArgs(t, filepath.Join(scratch(t, "cache-store"), "state.db"), "item_042", "run-dirty-cache", "--base-ref", "patch/pi"))
	if code != 4 || !strings.Contains(stderr, "uncommitted work") {
		t.Fatalf("dirty cache landing exited %d: %s", code, out)
	}
	if tip := git(t, f.dir, "rev-parse", "patch/pi"); tip != f.baseSHA {
		t.Fatalf("dirty cache moved delivery to %s", tip)
	}
}

func TestForkDeliveryRecoversAMoveBeforeTheReceiptAndRefreshesCache(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "branch", "patch/pi", f.headSHA)
	git(t, f.dir, "update-ref", "refs/tillr/landed/run-recover", f.headSHA)
	cache := filepath.Join(f.dir, ".forks/pi")
	git(t, f.dir, "clone", "--quiet", "--no-checkout", f.dir, cache)
	git(t, cache, "checkout", "-b", "main", f.baseSHA)
	_, code, out := f.land(t, "run-recover", "pass", "--base-ref", "patch/pi")
	if code != 0 {
		t.Fatalf("recovered delivery exited %d: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.TrunkAfter != f.headSHA {
		t.Fatalf("recovery rewrote delivery: %s", out)
	}
	if tip := git(t, cache, "rev-parse", "main"); tip != f.headSHA {
		t.Fatalf("recovery left cache at %s", tip)
	}
}
