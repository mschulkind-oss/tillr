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
