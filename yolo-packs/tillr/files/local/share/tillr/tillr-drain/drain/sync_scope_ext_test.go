package drain_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestLoopSyncRoadmapsScope(t *testing.T) {
	for _, tc := range []struct {
		value       string
		want        []string
		wantSkipped string
	}{
		{`["docs/a/roadmap.md"]`, []string{"item-a:in-queue"}, ", skipped 3; unshared_open: 0"},
		{`[]`, []string{"item-a:in-queue", "item-b:in-queue", "item-a-parked:not-started", "item-b-parked:not-started"}, ""},
		{`["docs/b/roadmap.md","docs/a/roadmap.md"]`, []string{"item-b:in-queue", "item-a:in-queue"}, ", skipped 2; unshared_open: 0"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			f := newLoopFixture(t, stub(t, "adapter", "exit 0"))
			for _, name := range []string{"a", "b"} {
				commitDocument(t, f, "docs/"+name+"/work.md", "# Work\n\n### Work\n\n<!-- tillr: item id=\"item-"+name+"\" paths=\"candidate.txt\" -->\n\n- **c1:** candidate.txt exists.\n")
				commitDocument(t, f, "docs/"+name+"/parked.md", "# Parked\n\n### Parked\n\n<!-- tillr: item id=\"item-"+name+"-parked\" paths=\"candidate.txt\" -->\n\n- **c1:** candidate.txt exists.\n")
				commitDocument(t, f, "docs/"+name+"/roadmap.md", "# Roadmap\n\n## Active\n\n- [Work](work.md)\n\n## Icebox\n\n- [Parked](parked.md)\n")
			}
			fixtureAt(t, f.store)
			writeFile(t, filepath.Join(f.root, ".tillr.toml"), "[sync]\nroadmaps = "+tc.value)
			code, out := invokeSyncScopeLoop(t, f, sibling(t, "item"))
			if code != 0 {
				t.Fatalf("exit %d: %s", code, out)
			}
			if !strings.Contains(string(out), "sync.roadmaps="+tc.value) || !strings.Contains(string(out), "queue-drained") {
				t.Fatal(string(out))
			}
			if tc.wantSkipped == "" {
				if strings.Contains(string(out), ", skipped ") {
					t.Fatalf("unscoped sync reported skipped blocks: %s", out)
				}
			} else if !strings.Contains(string(out), tc.wantSkipped) {
				t.Fatalf("missing %q in:\n%s", tc.wantSkipped, out)
			}
			st, err := store.OpenRead(context.Background(), f.store)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			var ids []string
			err = st.ReadTx(context.Background(), func(r *store.Reader) error {
				rows, err := r.Query("SELECT item_id||':'||state FROM items ORDER BY CASE WHEN rank='' THEN 1 ELSE 0 END, rank, item_id")
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var id string
					if err := rows.Scan(&id); err != nil {
						return err
					}
					ids = append(ids, id)
				}
				return rows.Err()
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("synced ids = %v, want %v", ids, tc.want)
			}
		})
	}
}

func invokeSyncScopeLoop(t *testing.T, f loopFixture, itemBin string) (int, []byte) {
	t.Helper()
	policy := filepath.Join(scratch(t, "sync-scope-policy"), "policy.json")
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policy, string(raw))
	questions := indexStub(t,
		map[string]any{"path": "docs/a/work.md", "questions": []map[string]any{{"id": "OQ-A", "status": "open"}}},
		map[string]any{"path": "docs/b/work.md", "questions": []map[string]any{{"id": "OQ-B", "status": "open"}}},
	)
	return invoke(t, "loop", "--root", f.root, "--store", f.store, "--policy", policy,
		"--lane-root", f.laneRoot, "--item-bin", itemBin, "--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true",
		"--questions-cmd", questions, "--rolling=", "--exit-when-drained")
}

func TestLoopSyncRoadmapsPathRefusal(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", "exit 0"))
	fixtureAt(t, f.store)
	writeFile(t, filepath.Join(f.root, ".tillr.toml"), "[sync]\nroadmaps = ['docs/missing/roadmap.md']")
	code, out := invokeSyncScopeLoop(t, f, sibling(t, "item"))
	if code != 2 || !strings.Contains(string(out), `--roadmap "docs/missing/roadmap.md" must name a tracked Markdown file`) {
		t.Fatalf("exit %d: %s", code, out)
	}
	if strings.Contains(string(out), "loop: tick") || len(lines(t, f.count)) != 0 {
		t.Fatalf("loop ran after refused first sync: %s", out)
	}
}
