package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestShippedWatchKindsMatchRegistry(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "tillr", "watch", "testdata", "shipped_watch_kinds.json"))
	if err != nil {
		t.Fatalf("read shipped_watch_kinds.json: %v", err)
	}
	var kinds []string
	if err := json.Unmarshal(raw, &kinds); err != nil {
		t.Fatalf("unmarshal shipped_watch_kinds.json: %v", err)
	}
	if len(kinds) == 0 {
		t.Fatal("shipped_watch_kinds.json must not be empty")
	}
	sorted := slices.Clone(kinds)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	if !slices.Equal(kinds, sorted) {
		t.Fatalf("shipped_watch_kinds.json must be sorted and unique")
	}

	listed := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		listed[k] = true
		if _, err := event.Lookup(k); err != nil {
			t.Errorf("shipped_watch_kinds.json kind %q is not registered in tillr-watch: %v", k, err)
		}
	}

	rules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatalf("LoadRules(rules): %v", err)
	}
	for _, r := range rules {
		for _, k := range r.Reads.Events {
			if !listed[k] {
				t.Errorf("rule %q reads event kind %q which is missing from tools/tillr/watch/testdata/shipped_watch_kinds.json", r.ID, k)
			}
			if _, err := event.Lookup(k); err != nil {
				t.Errorf("rule %q reads event kind %q which is not registered in tillr-watch: %v", r.ID, k, err)
			}
		}
	}
}
