package item_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSyncDesignWithoutItemConfiguredStages(t *testing.T) {
	root := scratch(t)
	config := filepath.Join(root, ".vantage.toml")
	if err := os.WriteFile(config, []byte("[planning.stages]\nCURRENT = 'done'\nSUPERSEDED = 'done'\nGRADUATED = 'done'\nFINISHED = 'built'\nDESIGN = 'open'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"CURRENT", "SUPERSEDED", "GRADUATED", "FINISHED", "DESIGN"} {
		t.Run(stage, func(t *testing.T) {
			s := syncStore(t)
			report := runSync(t, s, root, doc("docs/roadmap.md", "[Design](design/example.md)"), doc("docs/design/example.md", "---\nstage: "+stage+"\n---\n# Design"))
			want := 0
			if stage == "DESIGN" {
				want = 1
			}
			if len(report.Warnings) != want {
				t.Fatalf("stage %s warnings: %+v", stage, report.Warnings)
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

func TestSyncDesignWithoutItemDiscoversRoadmaps(t *testing.T) {
	s := syncStore(t)
	report := runSync(t, s, "", doc("docs/project/roadmap.md", "# Project\n[Design](../design/example.md)\n"), doc("docs/design/example.md", "# Design"))
	if len(report.Warnings) != 1 || report.Warnings[0].Roadmap != "docs/project/roadmap.md" || report.Warnings[0].Line != 2 {
		t.Fatalf("new roadmap was missed: %+v", report.Warnings)
	}
}

func TestSyncDesignWithoutItem(t *testing.T) {
	for _, tc := range []struct {
		name, stage, state string
		block, linked      bool
		want               bool
	}{
		{name: "missing", stage: "draft", linked: true, want: true},
		{name: "block", stage: "draft", linked: true, block: true},
		{name: "active item", stage: "draft", linked: true, state: "in-queue"},
		{name: "only landed items", stage: "draft", linked: true, state: "landed", want: true},
		{name: "closed is not landed", stage: "draft", linked: true, state: "closed"},
		{name: "built", stage: "built", linked: true},
		{name: "uppercase built", stage: "BUILT", linked: true},
		{name: "uppercase done", stage: "DONE", linked: true},
		{name: "done quoted", stage: "'done' # complete", linked: true},
		{name: "missing stage", linked: true, want: true},
		{name: "unlinked", stage: "draft"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := syncStore(t)
			if tc.state != "" {
				runSync(t, s, "", doc("docs/task.md", goodBlock))
				exec(t, s, `UPDATE items SET state=?, design_doc='docs/design/example.md'`, tc.state)
			}
			before := column(t, s, `SELECT COALESCE(json_group_array(json_object('id',item_id,'state',state,'updated',updated_at,'seq',event_seq,'design',design_doc)), '[]') FROM items`)
			design := "---\nstage: " + tc.stage + "\n---\n# Design\n"
			if tc.block {
				design += goodBlock
			}
			roadmap := "# Roadmap\n## Work\n"
			if tc.linked {
				roadmap += "[Design](design/example.md#questions)\n"
			}
			report := runSync(t, s, "", doc("docs/roadmap.md", roadmap), doc("docs/design/example.md", design))
			var warnings []item.SyncNote
			for _, n := range report.Warnings {
				if n.Rule == "design-without-item" {
					warnings = append(warnings, n)
				}
			}
			if (len(warnings) == 1) != tc.want {
				t.Fatalf("warnings: %+v", report.Warnings)
			}
			if tc.want {
				n := warnings[0]
				if n.File != "docs/design/example.md" || !strings.Contains(n.Message, "docs/roadmap.md:3") {
					t.Fatalf("location: %+v", n)
				}
				if len(report.Findings) != 0 || len(report.Created) != 0 || len(report.Updated) != 0 {
					t.Fatalf("warning changed items: %+v", report)
				}
			}
			if !tc.block {
				after := column(t, s, `SELECT COALESCE(json_group_array(json_object('id',item_id,'state',state,'updated',updated_at,'seq',event_seq,'design',design_doc)), '[]') FROM items`)
				if after != before {
					t.Fatalf("items changed: %s -> %s", before, after)
				}
			}
			want := 0
			if tc.want {
				want = 1
			}
			if n := count(t, s, `SELECT count(*) FROM design_without_item`); n != want {
				t.Fatalf("stored warnings %d want %d", n, want)
			}
		})
	}
}

func TestSyncDesignWithoutItemReplacementAndDryRun(t *testing.T) {
	s := syncStore(t)
	docs := []item.SyncDocument{doc("docs/roadmap.md", "# Roadmap\n[Design](design/example.md)\n"), doc("docs/design/example.md", "# Design\n")}
	dry := func(documents []item.SyncDocument) {
		t.Helper()
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			_, err := item.Sync(context.Background(), r, nil, item.SyncInput{Documents: documents, At: syncAt.Add(time.Hour)})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	dry(docs)
	if n := count(t, s, `SELECT count(*) FROM design_without_item`); n != 0 {
		t.Fatal("dry run wrote warning")
	}
	runSync(t, s, "", docs...)
	first := column(t, s, `SELECT CAST(first_seen AS TEXT) FROM design_without_item`)
	docs[0] = doc("docs/roadmap.md", "# Roadmap\n\n[Design](design/example.md)\n[Again](design/example.md#part)\n```\n[Fake](design/fake.md)\n```\n")
	docs = append(docs, doc("docs/design/fake.md", "# Fake"))
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{Documents: docs, At: syncAt.Add(time.Hour)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := column(t, s, `SELECT CAST(first_seen AS TEXT) FROM design_without_item`); got != first {
		t.Fatalf("first seen changed %s -> %s", first, got)
	}
	if n := count(t, s, `SELECT count(*) FROM design_without_item WHERE roadmap_line=3`); n != 1 {
		t.Fatal("location not replaced, duplicate or fenced link counted")
	}
	dry(nil)
	if n := count(t, s, `SELECT count(*) FROM design_without_item`); n != 1 {
		t.Fatal("dry run removed warning")
	}
	docs[1] = doc("docs/design/example.md", goodBlock)
	runSync(t, s, "", docs...)
	if n := count(t, s, `SELECT count(*) FROM design_without_item`); n != 0 {
		t.Fatal("filed block did not remove warning")
	}
	runSync(t, s, "", docs[:1]...)
	if n := count(t, s, `SELECT count(*) FROM design_without_item`); n != 0 {
		t.Fatal("missing design was invented")
	}
	// Every item field stays untouched when a warning is the only work.
	runSync(t, s, "", doc("docs/task.md", goodBlock))
	exec(t, s, `UPDATE items SET design_doc=''`)
	var before, after []byte
	snapshot := func(dst *[]byte) {
		t.Helper()
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			env, err := item.Show(r, "item-build-thing", "")
			if err != nil {
				return err
			}
			*dst = env.Payload
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot(&before)
	runSync(t, s, "", doc("docs/roadmap.md", "[Design](design/new.md)"), doc("docs/design/new.md", "# New"))
	snapshot(&after)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("warning altered item")
	}
}
