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

func TestForkDeliveryAcceptsRestackedCacheWithSameTreeParent(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "branch", "patch/pi", f.baseSHA)
	// Simulate a patch branch that was restacked to f.baseSHA with the same tree
	// while the fork cache stayed at the pre-restack tip.
	oldTip := git(t, f.dir, "commit-tree", f.baseSHA+"^{tree}", "-m", "pre-restack tip")
	cache := filepath.Join(f.dir, ".forks/pi")
	git(t, f.dir, "clone", "--quiet", "--no-checkout", f.dir, cache)
	git(t, cache, "fetch", "--quiet", "--no-tags", f.dir, oldTip)
	git(t, cache, "checkout", "-b", "main", oldTip)
	git(t, cache, "update-ref", "refs/heads/patch/pi", oldTip)

	_, code, out := f.land(t, "run-restacked-cache", "pass", "--base-ref", "patch/pi")
	if code != 0 {
		t.Fatalf("restacked same-tree cache landing exited %d: %s", code, out)
	}
	if tip := git(t, f.dir, "rev-parse", "patch/pi"); tip != f.headSHA {
		t.Fatalf("delivery left patch/pi at %s, want %s", tip, f.headSHA)
	}
	if tip := git(t, cache, "rev-parse", "main"); tip != f.headSHA {
		t.Fatalf("delivery left cache main at %s, want %s", tip, f.headSHA)
	}
	if tip := git(t, cache, "rev-parse", "patch/pi"); tip != f.headSHA {
		t.Fatalf("delivery left cache patch/pi at %s, want %s", tip, f.headSHA)
	}
	if status := git(t, cache, "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("delivery left dirty cache: %s", status)
	}
}

func TestForkDeliveryRefusesDivergentCacheWithDifferentTree(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "branch", "patch/pi", f.baseSHA)
	cache := filepath.Join(f.dir, ".forks/pi")
	git(t, f.dir, "clone", "--quiet", "--no-checkout", f.dir, cache)
	git(t, cache, "checkout", "-b", "main", f.baseSHA)
	write(t, filepath.Join(cache, "local.txt"), "divergent work\n")
	git(t, cache, "add", "local.txt")
	git(t, cache, "commit", "-m", "local cache commit")
	before := git(t, cache, "rev-parse", "main")

	code, out, stderr := passBoth(t, f.passArgs(t, filepath.Join(scratch(t, "cache-store"), "state.db"), "item_042", "run-divergent-cache", "--base-ref", "patch/pi"))
	if code != 4 || !strings.Contains(stderr, "unlanded or divergent commits") {
		t.Fatalf("divergent cache landing exited %d: out=%s stderr=%s", code, out, stderr)
	}
	if tip := git(t, f.dir, "rev-parse", "patch/pi"); tip != f.baseSHA {
		t.Fatalf("divergent cache moved delivery to %s", tip)
	}
	if tip := git(t, cache, "rev-parse", "main"); tip != before {
		t.Fatalf("divergent cache moved main from %s to %s", before, tip)
	}
}
