package drainmodel

import (
	"crypto/sha256"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestItemPinRefNormalAndFallback(t *testing.T) {
	if got := ItemPinRef("item-a"); got != "refs/tillr/items/item-a" {
		t.Fatalf("ItemPinRef(item-a) = %q, want refs/tillr/items/item-a", got)
	}
	exactMax := strings.Repeat("a", MaxRefComponent)
	if got := ItemPinRef(exactMax); got != ItemPinPrefix+exactMax {
		t.Fatalf("ItemPinRef(len=%d) = %q, want direct ref", MaxRefComponent, got)
	}

	fallbackCases := []string{
		strings.Repeat("b", MaxRefComponent+1),
		"item/nested",
		"item..bad",
		"item@{0}",
		".leading-dot",
		"trailing-dot.",
		"item.lock",
		"@",
		"has space",
		"has~tilde",
		"has^caret",
		"has:colon",
		"has?qmark",
		"has*star",
		"has[bracket",
		`has\backslash`,
		"has\x01ctrl",
		"has\x7fdel",
	}
	for _, id := range fallbackCases {
		got := ItemPinRef(id)
		want := fmt.Sprintf("%s%x", ItemPinLongPrefix, sha256.Sum256([]byte(id)))
		if got != want {
			t.Errorf("ItemPinRef(%q) = %q, want %q", id, got, want)
		}
		if out, err := exec.Command("git", "check-ref-format", got).CombinedOutput(); err != nil {
			t.Errorf("git check-ref-format %q failed: %v (%s)", got, err, strings.TrimSpace(string(out)))
		}
	}
}

func TestNodeKeyPinRefAndBranch(t *testing.T) {
	key := NodeKey("work/piece-a")
	if strings.Contains(key, "/") || len(key) > MaxRefComponent {
		t.Fatalf("NodeKey(%q) = %q, must be single component <= %d", "work/piece-a", key, MaxRefComponent)
	}
	if !strings.HasPrefix(key, "work-piece-a-") {
		t.Fatalf("NodeKey(%q) = %q, expected sanitized prefix", "work/piece-a", key)
	}
	if NodeKey("work/piece-a") != NodeKey("work/piece-a") {
		t.Fatal("NodeKey is not deterministic")
	}
	if NodeKey("work/piece-a") == NodeKey("work-piece-a") {
		t.Fatal("NodeKey hash suffix failed to disambiguate slash vs dash")
	}

	ref := NodePinRef("item-a", "work/piece-a")
	if !strings.HasPrefix(ref, NodePinPrefix+"item-a/") {
		t.Fatalf("NodePinRef = %q", ref)
	}
	if out, err := exec.Command("git", "check-ref-format", ref).CombinedOutput(); err != nil {
		t.Fatalf("git check-ref-format %q failed: %v (%s)", ref, err, strings.TrimSpace(string(out)))
	}

	longRef := NodePinRef("item/nested", "work/piece-a")
	if !strings.HasPrefix(longRef, NodePinLongPrefix) {
		t.Fatalf("NodePinRef fallback = %q", longRef)
	}
	if out, err := exec.Command("git", "check-ref-format", longRef).CombinedOutput(); err != nil {
		t.Fatalf("git check-ref-format %q failed: %v (%s)", longRef, err, strings.TrimSpace(string(out)))
	}

	branch := NodeLaneBranch("items-12345678", "item-a", "work/piece-a")
	if !strings.HasPrefix(branch, "tillr/items-12345678-nodes/item-a/") {
		t.Fatalf("NodeLaneBranch = %q", branch)
	}
	if out, err := exec.Command("git", "check-ref-format", "refs/heads/"+branch).CombinedOutput(); err != nil {
		t.Fatalf("git check-ref-format %q failed: %v (%s)", branch, err, strings.TrimSpace(string(out)))
	}
}
