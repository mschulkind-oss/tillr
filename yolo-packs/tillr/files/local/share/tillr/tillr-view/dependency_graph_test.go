package main

import (
	"strings"
	"testing"
)

func TestSnapshotDependencyEdgesUnreadable(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`DROP TABLE item_edges`)
	status, _, raw := h.get("/api/v1/snapshot")
	if status == 200 || !strings.Contains(string(raw), "table-absent") {
		t.Fatalf("missing edge table must refuse the snapshot: %d %s", status, raw)
	}
}

func TestSnapshotDependencyGraphEdges(t *testing.T) {
	h := newServeHarness(t)
	status, body, raw := h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("snapshot: %d %s", status, raw)
	}
	items := body["data"].(map[string]any)["items"].([]any)
	found := false
	for _, value := range items {
		item := value.(map[string]any)
		edges, ok := item["blocked_by"].([]any)
		if !ok {
			t.Fatalf("dependency edges missing: %s", raw)
		}
		if item["item_id"] == "item-seed-06" {
			found = true
			if len(edges) != 1 || edges[0] != "item-seed-02" {
				t.Fatalf("edge lost: %v", edges)
			}
		}
	}
	if !found {
		t.Fatal("fixture dependent is missing")
	}
}
