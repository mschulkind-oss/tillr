package main

import (
	"os"
	"path/filepath"
	"testing"
)

// plannerFixtureDir isolates read-face tests from trunk ancestry. These fixtures
// contain stores, not repositories, and some deliberately run without Git on PATH.
// They select the supported tip policy explicitly; green-base policy is exercised
// by the drain's repository-backed tests and the view's heavy status test.
func plannerFixtureDir(t *testing.T) string {
	t.Helper()
	parent := os.Getenv("YOLO_DURABLE_DIR")
	if parent == "" {
		parent = filepath.Join("..", "..", ".yolo", "durable")
	}
	dir, err := os.MkdirTemp(parent, "view-planner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	config := filepath.Join(dir, "config", "tillr")
	if err := os.MkdirAll(config, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "gate-policy.json"), []byte(`{"lane_base":"tip"}`), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}
