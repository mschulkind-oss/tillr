package drain_test

import (
	"strings"
	"testing"
)

func TestSweepRefsFaceReportsDryRunAndOperatorOwnership(t *testing.T) {
	f := newLoopFixture(t, stub(t, "unused", "exit 0"))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"), runRecord("finished", "item_041", "lane-1", "fixture", "exited", new(int), ""))
	closeFixtureItem(t, f.store, "item_041")
	base := git(t, f.root, "rev-parse", "main")
	git(t, f.root, "update-ref", "refs/tillr/runs/finished", base)
	// Missing ownership must not be guessed: this unregistered pin remains.
	git(t, f.root, "update-ref", "refs/tillr/runs/unknown", base)
	git(t, f.root, "update-ref", "refs/tillr/held/item_041", base)
	git(t, f.root, "update-ref", "refs/tillr/recovery/operator", base)
	git(t, f.root, "update-ref", "refs/tillr/landed/unrecorded", base)
	before := git(t, f.root, "for-each-ref", "refs/tillr")
	args := []string{"sweep-refs", "--root", f.root, "--store", f.store}
	code, out := invoke(t, append(args, "--dry-run")...)
	if code != 0 {
		t.Fatalf("dry sweep exited %d: %s", code, out)
	}
	for _, want := range []string{"kept refs/tillr/held/item_041", "operator owns this ref; item state=closed; trunk contains commit=true", "kept refs/tillr/recovery/operator", "item state=unknown", "no durable landed row", "unknown run", "would remove refs/tillr/runs/finished", "operator ref still holds commit"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	if got := git(t, f.root, "for-each-ref", "refs/tillr"); got != before {
		t.Fatal("dry run moved refs")
	}
	code, out = invoke(t, args...)
	if code != 0 || !strings.Contains(string(out), "removed refs/tillr/runs/finished") {
		t.Fatalf("sweep exited %d: %s", code, out)
	}
	after := git(t, f.root, "for-each-ref", "refs/tillr")
	code, out = invoke(t, args...)
	if code != 0 || strings.Contains(string(out), "removed ") {
		t.Fatalf("repeat exited %d: %s", code, out)
	}
	if got := git(t, f.root, "for-each-ref", "refs/tillr"); got != after {
		t.Fatal("repeat moved retained refs")
	}
	if code, out := invoke(t, "sweep-refs"); code != 2 || !strings.Contains(string(out), "needs --root") {
		t.Fatalf("usage exited %d: %s", code, out)
	}
	if code, out := invoke(t, "sweep-refs", "--help"); code != 0 || !strings.Contains(string(out), "dry-run") {
		t.Fatalf("help exited %d: %s", code, out)
	}
}
