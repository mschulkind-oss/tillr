package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerationReportsAreBothFoundAndNewestIsNumeric(t *testing.T) {
	base := os.Getenv("YOLO_DURABLE_DIR")
	if base == "" {
		base = filepath.Join("..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "view-pass-results-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	for _, name := range []string{"drain-9-9.json", "drain-10-10.json"} {
		raw, err := json.Marshal(map[string]any{"record_version": 1, "kind": "drain.loop", "id": name, "producer": map[string]string{"system": "tillr", "utility": "drain", "version": "0.1.0"}, "created_at": "2026-11-16T00:00:00Z", "payload": map[string]string{"stop": "queue-drained"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "drain-*.json"))
	if err != nil || len(files) != 2 {
		t.Fatalf("glob: %v / %v", files, err)
	}
	obs := &observations{loopDir: root}
	got := obs.newestLoopResult()
	if filepath.Base(got.Path) != "drain-10-10.json" || got.Stop != "queue-drained" {
		t.Fatalf("newest: %+v", got)
	}
}
