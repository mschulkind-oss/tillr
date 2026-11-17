package item_test

import (
	"context"
	"encoding/json"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncJudgeMode(t *testing.T) {
	for _, tc := range []struct{ attr, mode, rule string }{
		{` judge="agent"`, "agent", ""}, {"", "", ""}, {` judge=""`, "", "invalid-judge"}, {` judge="mechanical"`, "", "invalid-judge"},
	} {
		t.Run(tc.attr, func(t *testing.T) {
			s := syncStore(t)
			root := scratch(t)
			block := strings.Replace(goodBlock, `paths="tools/thing/"`, `paths="tools/thing/"`+tc.attr, 1)
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
			if got := column(t, s, `SELECT judge_mode FROM items`); got != tc.mode {
				t.Fatalf("mode %q", got)
			}
			again := runSync(t, s, root, doc("docs/plan.md", block))
			if again.UpdatedCount != 0 || again.UnchangedCount != 1 {
				t.Fatalf("repeat sync: %+v", again)
			}
			if tc.mode == "agent" {
				optedOut := strings.Replace(block, ` judge="agent"`, "", 1)
				removed := runSync(t, s, root, doc("docs/plan.md", optedOut))
				if removed.UpdatedCount != 1 || column(t, s, `SELECT judge_mode FROM items`) != "" {
					t.Fatalf("opt-out: %+v", removed)
				}
				runSync(t, s, root, doc("docs/plan.md", block))
			}
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				env, err := item.Show(r, "item-build-thing", root)
				if err != nil {
					return err
				}
				var p map[string]any
				if err := json.Unmarshal(env.Payload, &p); err != nil {
					return err
				}
				if p["judge_mode"] != tc.mode {
					t.Fatalf("show: %s", env.Payload)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestJudgeModeShowLegacy(t *testing.T) {
	s := syncStore(t)
	root := scratch(t)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs/plan.md"), []byte(goodBlock), 0644); err != nil {
		t.Fatal(err)
	}
	runSync(t, s, root, doc("docs/plan.md", goodBlock))
	exec(t, s, `ALTER TABLE items DROP COLUMN judge_mode`)
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error { _, err := item.Show(r, "item-build-thing", root); return err }); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM pragma_table_info('items') WHERE name='judge_mode'`); n != 0 {
		t.Fatal("show migrated legacy store")
	}
}
