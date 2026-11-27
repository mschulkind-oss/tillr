package item_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

func indexForStages(stages map[string]string, docs map[string]string) *vantage.VantageIndex {
	var b strings.Builder
	b.WriteString(`{"index":{"config":{"stages":{`)
	first := true
	for k, v := range stages {
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(`"` + k + `":"` + v + `"`)
	}
	b.WriteString(`}},"documents":[`)
	first = true
	for p, stage := range docs {
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(`{"path":"` + p + `","stage":"` + stage + `","questions":[]}`)
	}
	b.WriteString(`]}}`)
	idx := vantage.ParseIndex([]byte(b.String()), "test-fixture")
	return &idx
}

func TestSyncDesignWithoutItemConfiguredStages(t *testing.T) {
	root := scratch(t)
	config := filepath.Join(root, ".vantage.toml")
	if err := os.WriteFile(config, []byte("[planning.stages]\nCURRENT = 'done'\nSUPERSEDED = 'done'\nGRADUATED = 'done'\nFINISHED = 'built'\nDESIGN = 'open'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stages := map[string]string{
		"CURRENT":    "done",
		"SUPERSEDED": "done",
		"GRADUATED":  "done",
		"FINISHED":   "built",
		"DESIGN":     "open",
	}
	for _, stage := range []string{"CURRENT", "SUPERSEDED", "GRADUATED", "FINISHED", "DESIGN"} {
		t.Run(stage, func(t *testing.T) {
			s := syncStore(t)
			var report item.SyncReport
			if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
				var err error
				report, err = item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{
					Root:  root,
					Index: indexForStages(stages, map[string]string{"docs/design/example.md": stage}),
					Documents: []item.SyncDocument{
						doc("docs/roadmap.md", "[Design](design/example.md)"),
						doc("docs/design/example.md", "---\nstage: "+stage+"\n---\n# Design"),
					},
					At: syncAt,
				})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if len(report.Warnings) != 0 {
				t.Fatalf("mirrored roadmap emitted design-without-item warnings: %+v", report.Warnings)
			}
			wantCreated := 0
			if stage == "DESIGN" {
				wantCreated = 1
			}
			if len(report.Created) != wantCreated {
				t.Fatalf("stage %s created: %+v, want %d", stage, report.Created, wantCreated)
			}
		})
	}
	if err := os.WriteFile(config, []byte("[planning.stages\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := syncStore(t)
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		_, err := item.Sync(context.Background(), r, nil, item.SyncInput{Root: root, At: syncAt})
		return err
	})
	if err == nil || !strings.Contains(err.Error(), ".vantage.toml") {
		t.Fatalf("malformed config did not refuse explicitly: %v", err)
	}
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		_, err := item.Sync(context.Background(), r, nil, item.SyncInput{Root: root, At: syncAt})
		return err
	})
	if err == nil || !strings.Contains(err.Error(), ".vantage.toml") {
		t.Fatalf("unreadable config did not refuse explicitly: %v", err)
	}
}

func TestSyncRetiresDesignWithoutItemRowsForMirroredRoadmaps(t *testing.T) {
	s := syncStore(t)
	// Seed a stale pre-Slice-1 design_without_item row.
	exec(t, s, `INSERT INTO design_without_item (document, roadmap, roadmap_line, first_seen) VALUES ('docs/design/example.md', 'docs/project/roadmap.md', 2, ?)`, syncAt.UnixMilli())

	var report item.SyncReport
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var err error
		report, err = item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{
			Index: indexForStages(map[string]string{"DESIGN": "open"}, map[string]string{"docs/design/example.md": "DESIGN"}),
			Documents: []item.SyncDocument{
				doc("docs/project/roadmap.md", "# Project\n[Design](../design/example.md)\n"),
				doc("docs/design/example.md", "---\nstage: DESIGN\n---\n# Design\n"),
			},
			At: syncAt.Add(time.Hour),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("expected zero design-without-item warnings, got %+v", report.Warnings)
	}
	if n := count(t, s, `SELECT count(*) FROM design_without_item`); n != 0 {
		t.Fatalf("expected stale design_without_item rows to be cleared, got %d", n)
	}
	if len(report.Created) != 1 || report.Created[0] != "item-design-design-example" {
		t.Fatalf("created = %+v, want [item-design-design-example]", report.Created)
	}
}
