package item_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSyncRefusesCloneOfHeldItem(t *testing.T) {
	for _, state := range []string{"not-started", "in-queue", "blocked", "in-progress"} {
		for _, suffix := range []string{"-v2", "-v12", "-v0", "-v002"} {
			t.Run(state+suffix, func(t *testing.T) {
				s := syncStore(t)
				runSync(t, s, "", doc("docs/original.md", goodBlock))
				exec(t, s, `UPDATE items SET state = ? WHERE item_id = 'item-build-thing'`, state)
				before := count(t, s, `SELECT count(*) FROM events`)
				cloneID := "item-build-thing" + suffix
				clone := strings.ReplaceAll(goodBlock, "item-build-thing", cloneID)
				sibling := strings.ReplaceAll(goodBlock, "item-build-thing", "item-sibling")
				other := strings.ReplaceAll(goodBlock, "item-build-thing", "item-other")
				report := runSync(t, s, "", doc("docs/clone.md", clone+sibling), doc("docs/other.md", other))
				if len(report.Findings) != 1 || report.Findings[0].Rule != "clone-of-held-item" {
					t.Fatalf("want clone-of-held-item: %+v", report)
				}
				finding := report.Findings[0]
				for _, want := range []string{cloneID, "item-build-thing", "tillr drain retry", "--item item-build-thing"} {
					if !strings.Contains(finding.Message, want) {
						t.Fatalf("finding %q omits %q", finding.Message, want)
					}
				}
				if finding.File != "docs/clone.md" || strings.Join(report.SkippedFiles, ",") != "docs/clone.md" || strings.Join(report.Created, ",") != "item-other" {
					t.Fatalf("file isolation: %+v", report)
				}
				if count(t, s, `SELECT count(*) FROM items WHERE item_id IN (?, 'item-sibling')`, cloneID) != 0 || count(t, s, `SELECT count(*) FROM events`) != before+1 {
					t.Fatal("refused file wrote items or events")
				}
				if got := column(t, s, `SELECT state FROM items WHERE item_id = 'item-build-thing'`); got != state {
					t.Fatalf("original state changed to %s", got)
				}
			})
		}
	}
}

func TestSyncCloneExceptions(t *testing.T) {
	for _, state := range []string{"landed", "resolved-elsewhere", "closed", "absent"} {
		t.Run(state, func(t *testing.T) {
			s := syncStore(t)
			if state != "absent" {
				runSync(t, s, "", doc("docs/original.md", goodBlock))
				exec(t, s, `UPDATE items SET state = ? WHERE item_id = 'item-build-thing'`, state)
			}
			clone := strings.ReplaceAll(goodBlock, "item-build-thing", "item-build-thing-v2")
			report := runSync(t, s, "", doc("docs/clone.md", clone))
			if len(report.Findings) != 0 || strings.Join(report.Created, ",") != "item-build-thing-v2" {
				t.Fatalf("exception refused: %+v", report)
			}
		})
	}
}

func TestSyncLeavesExistingCloneUntouched(t *testing.T) {
	s := syncStore(t)
	clone := strings.ReplaceAll(goodBlock, "item-build-thing", "item-build-thing-v2")
	runSync(t, s, "", doc("docs/clone.md", clone))
	runSync(t, s, "", doc("docs/original.md", goodBlock))
	before := count(t, s, `SELECT count(*) FROM events`)
	report := runSync(t, s, "", doc("docs/clone.md", clone))
	if len(report.Findings) != 0 || report.CreatedCount != 0 || report.UpdatedCount != 0 || strings.Join(report.Unchanged, ",") != "item-build-thing-v2" || count(t, s, `SELECT count(*) FROM events`) != before {
		t.Fatalf("existing clone touched: %+v", report)
	}
}

func TestSyncClonePreviewRefusesWithoutWriting(t *testing.T) {
	s := syncStore(t)
	runSync(t, s, "", doc("docs/original.md", goodBlock))
	before := count(t, s, `SELECT count(*) FROM events`)
	clone := strings.ReplaceAll(goodBlock, "item-build-thing", "item-build-thing-v2")
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		report, err := item.Sync(context.Background(), r, nil, item.SyncInput{Documents: []item.SyncDocument{doc("docs/clone.md", clone)}, At: syncAt})
		if err == nil && (len(report.Findings) != 1 || report.Findings[0].Rule != "clone-of-held-item" || report.CreatedCount != 0) {
			t.Fatalf("preview accepted clone: %+v", report)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT count(*) FROM events`) != before {
		t.Fatal("preview wrote events")
	}
}

func TestSyncCloneSuffixMustBeNumericAndAtEnd(t *testing.T) {
	for _, suffix := range []string{"-v", "-v2-extra", "-version2", "-v2a"} {
		t.Run(suffix, func(t *testing.T) {
			s := syncStore(t)
			runSync(t, s, "", doc("docs/original.md", goodBlock))
			id := "item-build-thing" + suffix
			report := runSync(t, s, "", doc("docs/new.md", strings.ReplaceAll(goodBlock, "item-build-thing", id)))
			if len(report.Findings) != 0 || strings.Join(report.Created, ",") != id {
				t.Fatalf("non-clone refused: %+v", report)
			}
		})
	}
}
