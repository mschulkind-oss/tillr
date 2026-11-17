package item_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func fromBlock(id, from string) string {
	mark := ""
	if from != "" {
		mark = fmt.Sprintf(` from="%s"`, from)
	}
	return fmt.Sprintf("### %s\n<!-- tillr: item id=\"%s\" paths=\"tools/thing/\"%s -->\n- **c1:** works\n\n", id, id, mark)
}

func fromFixture(t *testing.T, sourceRank, nextRank string) *store.Store {
	t.Helper()
	s := syncStore(t)
	report := runSync(t, s, "", doc("docs/seed.md", fromBlock("item-source", "")+fromBlock("item-next", "")))
	if len(report.Findings) != 0 {
		t.Fatal(report.Findings)
	}
	exec(t, s, `UPDATE items SET rank=? WHERE item_id='item-source'`, sourceRank)
	exec(t, s, `UPDATE items SET rank=? WHERE item_id='item-next'`, nextRank)
	return s
}

func assertFromOrder(t *testing.T, s *store.Store, want string) {
	t.Helper()
	got := column(t, s, `SELECT group_concat(item_id, ',') FROM (SELECT item_id FROM items ORDER BY CASE WHEN rank='' THEN 1 ELSE 0 END,rank,item_id)`)
	if got != want {
		t.Fatalf("order %s; want %s", got, want)
	}
}

func TestSyncFromValidation(t *testing.T) {
	for _, from := range []string{"no-prefix", "item-UPPER", "item-a,item-b", ""} {
		t.Run(from, func(t *testing.T) {
			report := runSync(t, syncStore(t), "", doc("docs/new.md", strings.Replace(fromBlock("item-new", ""), `paths="tools/thing/"`, fmt.Sprintf(`paths="tools/thing/" from="%s"`, from), 1)))
			if len(report.Findings) != 1 || report.Findings[0].Rule != "invalid-from" {
				t.Fatalf("findings %+v", report.Findings)
			}
		})
	}
	for _, from := range []string{"item-missing", "item-source"} {
		t.Run(from, func(t *testing.T) {
			s := fromFixture(t, "", "z")
			report := runSync(t, s, "", doc("docs/new.md", fromBlock("item-new", from)))
			if len(report.Findings) != 0 || report.CreatedCount != 1 {
				t.Fatalf("report %+v", report)
			}
			if rank := column(t, s, `SELECT rank FROM items WHERE item_id='item-new'`); rank != "" {
				t.Fatalf("unknown/unkeyed source placed item at %q", rank)
			}
		})
	}
}

func TestSyncFromSiblingsAcrossSyncs(t *testing.T) {
	s := fromFixture(t, "a", "z")
	first := fromBlock("item-z", "item-source") + fromBlock("item-a", "item-source")
	report := runSync(t, s, "", doc("docs/new.md", first))
	if len(report.Findings) != 0 {
		t.Fatal(report.Findings)
	}
	assertFromOrder(t, s, "item-source,item-z,item-a,item-next")
	// Retirement removes the old blocks; placement receipts must still find siblings.
	report = runSync(t, s, "", doc("docs/later.md", fromBlock("item-third", "item-source")))
	if len(report.Findings) != 0 {
		t.Fatal(report.Findings)
	}
	assertFromOrder(t, s, "item-source,item-z,item-a,item-third,item-next")
}

func TestSyncFromExistingDoesNotMove(t *testing.T) {
	s := fromFixture(t, "a", "z")
	exec(t, s, `UPDATE items SET title='old' WHERE item_id='item-next'`)
	report := runSync(t, s, "", doc("docs/new.md", fromBlock("item-next", "item-source")))
	if len(report.Findings) != 0 || report.UpdatedCount != 1 {
		t.Fatalf("report %+v", report)
	}
	if rank := column(t, s, `SELECT rank FROM items WHERE item_id='item-next'`); rank != "z" {
		t.Fatal(rank)
	}
	assertFromOrder(t, s, "item-source,item-next")
}

func TestSyncFromCrowdedGap(t *testing.T) {
	s := fromFixture(t, "a", "a0")
	report := runSync(t, s, "", doc("docs/new.md", fromBlock("item-new", "item-source")))
	if len(report.Findings) != 0 {
		t.Fatal(report.Findings)
	}
	assertFromOrder(t, s, "item-source,item-new,item-next")
	if rank := column(t, s, `SELECT rank FROM items WHERE item_id='item-source'`); rank == "a" {
		t.Fatal("crowded gap did not respace")
	}
}

func TestSyncFromPreservesBlockOrderWithDependencies(t *testing.T) {
	s := fromFixture(t, "a", "z")
	blocks := strings.Replace(fromBlock("item-first", "item-source"), `paths="tools/thing/"`, `paths="tools/thing/" blocked_by="item-second"`, 1) + fromBlock("item-second", "item-source")
	report := runSync(t, s, "", doc("docs/new.md", blocks))
	if len(report.Findings) != 0 {
		t.Fatal(report.Findings)
	}
	assertFromOrder(t, s, "item-source,item-first,item-second,item-next")
}

func TestSyncFromPreservesBlockOrderAcrossDependentFiles(t *testing.T) {
	s := fromFixture(t, "a", "z")
	first := strings.Replace(fromBlock("item-first", "item-source"), `paths="tools/thing/"`, `paths="tools/thing/" blocked_by="item-second"`, 1)
	report := runSync(t, s, "", doc("docs/a.md", first), doc("docs/z.md", fromBlock("item-second", "item-source")))
	if len(report.Findings) != 0 {
		t.Fatal(report.Findings)
	}
	assertFromOrder(t, s, "item-source,item-first,item-second,item-next")
}

func TestSyncFromRespacingSurvivesLaterDefinitionUpdate(t *testing.T) {
	s := fromFixture(t, "a", "a0")
	exec(t, s, `UPDATE items SET title='old' WHERE item_id='item-next'`)
	report := runSync(t, s, "", doc("docs/a.md", fromBlock("item-new", "item-source")), doc("docs/z.md", fromBlock("item-next", "")))
	if len(report.Findings) != 0 || report.UpdatedCount != 1 {
		t.Fatalf("report %+v", report)
	}
	assertFromOrder(t, s, "item-source,item-new,item-next")
}

func TestSyncFromRefusedFileIsNotPlaced(t *testing.T) {
	s := fromFixture(t, "a", "a0")
	badA := strings.Replace(fromBlock("item-bad-a", "item-source"), `paths="tools/thing/"`, `paths="tools/thing/" blocked_by="item-bad-b"`, 1)
	badB := strings.Replace(fromBlock("item-bad-b", "item-source"), `paths="tools/thing/"`, `paths="tools/thing/" blocked_by="item-bad-a"`, 1)
	report := runSync(t, s, "", doc("docs/a.md", badA+badB), doc("docs/z.md", fromBlock("item-good", "item-source")))
	if len(report.Findings) != 1 || report.CreatedCount != 1 || report.Created[0] != "item-good" {
		t.Fatalf("report %+v", report)
	}
	assertFromOrder(t, s, "item-source,item-good,item-next")
	if n := count(t, s, `SELECT count(*) FROM events WHERE object_id IN ('item-bad-a','item-bad-b')`); n != 0 {
		t.Fatalf("refused file left %d events", n)
	}
}

func TestSyncFromRepeatIsReadOnly(t *testing.T) {
	s := fromFixture(t, "a", "z")
	blocks := doc("docs/new.md", fromBlock("item-new", "item-source"))
	runSync(t, s, "", blocks)
	before := count(t, s, `SELECT count(*) FROM events`)
	report := runSync(t, s, "", blocks)
	if len(report.Findings) != 0 || report.UnchangedCount != 1 {
		t.Fatalf("report %+v", report)
	}
	if after := count(t, s, `SELECT count(*) FROM events`); before != after {
		t.Fatalf("events %d -> %d", before, after)
	}
}

func TestSyncFromLandedSource(t *testing.T) {
	s := fromFixture(t, "a", "z")
	exec(t, s, `UPDATE items SET state='landed' WHERE item_id='item-source'`)
	report := runSync(t, s, "", doc("docs/new.md", fromBlock("item-new", "item-source")))
	if len(report.Findings) != 0 {
		t.Fatal(report.Findings)
	}
	assertFromOrder(t, s, "item-source,item-new,item-next")
	if state := column(t, s, `SELECT state FROM items WHERE item_id='item-source'`); state != "landed" {
		t.Fatal(state)
	}
}
