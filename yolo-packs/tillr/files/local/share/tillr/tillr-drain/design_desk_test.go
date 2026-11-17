package main_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func TestWhyReadsDocumentDeskSettings(t *testing.T) {
	rows := []string{}
	for _, name := range []string{"first", "second"} {
		rows = append(rows, envelope("item.record", "item-"+name, map[string]any{
			"item_id": name, "title": "write " + name, "state": "in-queue", "rank": name, "blocked_by": []string{}, "allowed_paths": []string{"docs/design/" + name + ".md"}, "criteria": []any{}, "seat": "design",
		}))
	}
	store := fixture(t, rows...)
	root := scratch(t, "desk-root")
	writeFile(t, filepath.Join(root, ".tillr.toml"), "[comments]\nvantage_url='http://localhost:1'\npaths=['docs/design/']\nmax_running=1\n")
	policy := policyDoc(t, map[string]any{"default_backend": "fixture", "preference": []string{"fixture"}, "seats": map[string]string{"design": "fixture"}, "backends": map[string]any{"fixture": map[string]any{"adapter": "true", "model": "fixture-model"}}})
	code, out := invoke(t, "why", "--store", store, "--root", root, "--policy", policy, "--lanes", "lane-1", "--json")
	if code != 0 {
		t.Fatalf("why %d %s", code, out)
	}
	var answer drain.WhyAnswer
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Rows) != 2 || answer.Rows[0].Decision.LaneID != "desk-first" || answer.Rows[1].Reason != "waits on a desk" || answer.Rows[1].Mover != "docs/design/second.md" {
		t.Fatalf("why desk rows: %+v", answer.Rows)
	}
}
