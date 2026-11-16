package item_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWorkflowShowReadsAnUnmigratedStore(t *testing.T) {
	s := syncStore(t)
	root := scratch(t)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs/plan.md"), []byte(goodBlock), 0644); err != nil {
		t.Fatal(err)
	}
	runSync(t, s, root, doc("docs/plan.md", goodBlock))
	exec(t, s, `ALTER TABLE items DROP COLUMN workflow`)
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		env, err := item.Show(r, "item-build-thing", root)
		if err != nil {
			return err
		}
		var p map[string]any
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return err
		}
		if p["workflow"] != "" {
			t.Errorf("legacy workflow: %v", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM pragma_table_info('items') WHERE name='workflow'`); n != 0 {
		t.Fatal("read mutated schema")
	}
}

func TestSyncWorkflow(t *testing.T) {
	root := scratch(t)
	dir := filepath.Join(root, "config/tillr/workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scout-then-build@v1.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ value, rule string }{{"scout-then-build@v1", ""}, {"bad", "invalid-workflow"}, {"missing@v1", "workflow-missing"}, {"", ""}} {
		t.Run(tc.value, func(t *testing.T) {
			s := syncStore(t)
			block := strings.Replace(goodBlock, `paths="tools/thing/"`, `paths="tools/thing/" workflow="`+tc.value+`"`, 1)
			if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "docs/plan.md"), []byte(block), 0644); err != nil {
				t.Fatal(err)
			}
			report := runSync(t, s, root, doc("docs/plan.md", block))
			if tc.rule != "" {
				if len(report.Findings) != 1 || report.Findings[0].Rule != tc.rule || report.Findings[0].ItemID != "item-build-thing" {
					t.Fatalf("findings: %+v", report.Findings)
				}
				return
			}
			if len(report.Findings) != 0 {
				t.Fatal(report.Findings)
			}
			if got := column(t, s, `SELECT workflow FROM items`); got != tc.value {
				t.Fatalf("workflow %q", got)
			}
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				env, err := item.Show(r, "item-build-thing", root)
				if err != nil {
					return err
				}
				var payload map[string]any
				if err = json.Unmarshal(env.Payload, &payload); err != nil {
					return err
				}
				if payload["workflow"] != tc.value {
					t.Fatalf("show: %s", env.Payload)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
