package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultLaneRootUsesItemsOnFreshCheckoutAndKeepsExistingLanes(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), ".tillr")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	wantItems := filepath.Join(storeDir, "items")
	if got := defaultLaneRoot(storeDir); got != wantItems {
		t.Fatalf("defaultLaneRoot(%q) on fresh checkout = %q, want %q", storeDir, got, wantItems)
	}

	wantLanes := filepath.Join(storeDir, "lanes")
	if err := os.MkdirAll(wantLanes, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := defaultLaneRoot(storeDir); got != wantLanes {
		t.Fatalf("defaultLaneRoot(%q) with existing lanes = %q, want %q", storeDir, got, wantLanes)
	}
}
