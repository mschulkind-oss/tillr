package remotesync

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRebindForkFenceAndRefLines(t *testing.T) {
	from := ForkTarget{Path: ".forks/lanes/lane-1/pi-fork"}
	to := ForkTarget{Path: ".forks/lanes/lane-2/pi-fork"}
	fence := strings.Join([]string{
		".forks/lanes/lane-1/pi-fork",
		".forks/lanes/lane-1/pi-fork/src/index.ts",
		"docs/readme.md",
	}, "\n")
	want := strings.Join([]string{
		".forks/lanes/lane-2/pi-fork",
		".forks/lanes/lane-2/pi-fork/src/index.ts",
		"docs/readme.md",
	}, "\n")
	if got := RebindForkFence(fence, from, to); got != want {
		t.Fatalf("RebindForkFence =\n%s\nwant:\n%s", got, want)
	}

	if got := ForkRefLines(""); got != nil {
		t.Fatalf("ForkRefLines(\"\") = %v, want nil", got)
	}
	lines := ForkRefLines("refs/tags/v1 abc\nrefs/remotes/upstream/main def")
	wantLines := []string{"refs/tags/v1 abc", "refs/remotes/upstream/main def"}
	if !reflect.DeepEqual(lines, wantLines) {
		t.Fatalf("ForkRefLines = %v, want %v", lines, wantLines)
	}
}

func TestHeldForkTargetAndPathHelpers(t *testing.T) {
	root := t.TempDir()
	target, err := ForkTargetAtLane(root, "refs/heads/patch/pi-fork", "lane-3")
	if err != nil {
		t.Fatal(err)
	}
	if target.Fork != "pi-fork" || target.Path != ".forks/lanes/lane-3/pi-fork" || target.Checkout != filepath.Join(root, ".forks", "lanes", "lane-3", "pi-fork") {
		t.Fatalf("ForkTargetAtLane = %+v", target)
	}
	if _, err := ForkTargetAtLane(root, "refs/heads/main", "lane-3"); err == nil {
		t.Fatal("expected error for non-patch ref")
	}
	if _, err := ForkTargetAtLane(root, "refs/heads/patch/pi-fork", "../escape"); err == nil {
		t.Fatal("expected error for invalid lane")
	}

	pinFallback := CandidatePinRef("run-1", "", "abc", "refs/tillr/runs/run-1")
	if pinFallback != "refs/tillr/runs/run-1" {
		t.Fatalf("fallback pin = %q", pinFallback)
	}
	pinHeld := CandidatePinRef("run-1", "refs/heads/patch/pi-fork", "abc", "refs/tillr/runs/run-1")
	if !strings.HasPrefix(pinHeld, "refs/tillr/held-forks/") {
		t.Fatalf("held pin = %q", pinHeld)
	}
	inputs := &HeldForkInputs{Upstream: "https://example.com/u.git", Refs: "refs/tags/v1 abc"}
	if !strings.HasPrefix(inputs.Prefix(pinHeld), "refs/tillr/held-fork-inputs/") {
		t.Fatalf("inputs prefix = %q", inputs.Prefix(pinHeld))
	}

	if got := RebaseHeldPath("/a/b/c", "/a/b", "/x/y"); got != filepath.Join("/x/y", "c") {
		t.Fatalf("RebaseHeldPath = %q", got)
	}
	if got := RebaseHeldPath("/other/c", "/a/b", "/x/y"); got != "/other/c" {
		t.Fatalf("RebaseHeldPath non-matching = %q", got)
	}

	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckHeldStagePath(filepath.Join(realDir, "missing-leaf")); err != nil {
		t.Fatalf("CheckHeldStagePath on clean dir failed: %v", err)
	}
	linkDir := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	if err := CheckHeldStagePath(filepath.Join(linkDir, "leaf")); err == nil {
		t.Fatal("expected CheckHeldStagePath to reject symlinked ancestor")
	}
}
