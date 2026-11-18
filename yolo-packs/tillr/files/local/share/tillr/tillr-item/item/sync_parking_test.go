package item_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func parkingLists(t *testing.T, report item.SyncReport, parked, unparked string) {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var lists map[string]json.RawMessage
	if err := json.Unmarshal(data, &lists); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"parked": parked, "unparked": unparked} {
		var got []string
		if err := json.Unmarshal(lists[name], &got); err != nil {
			t.Fatalf("%s missing from report: %s", name, data)
		}
		if got == nil || strings.Join(got, ",") != want {
			t.Fatalf("%s = %v, want %q", name, got, want)
		}
		if want != "" && !strings.Contains(item.SyncText(report, syncAt), name+": "+want+"\n") {
			t.Fatalf("text output omits %s move", name)
		}
	}
}

func previewParking(t *testing.T, s *store.Store, docs []item.SyncDocument) item.SyncReport {
	t.Helper()
	var report item.SyncReport
	before := count(t, s, `SELECT count(*) FROM events`)
	rows := column(t, s, `SELECT group_concat(item_id||'|'||state||'|'||rank) FROM (SELECT * FROM items ORDER BY item_id)`)
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		report, err = item.Sync(context.Background(), r, nil, item.SyncInput{Documents: docs, At: syncAt})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !report.DryRun || count(t, s, `SELECT count(*) FROM events`) != before || column(t, s, `SELECT group_concat(item_id||'|'||state||'|'||rank) FROM (SELECT * FROM items ORDER BY item_id)`) != rows {
		t.Fatal("preview wrote store content")
	}
	return report
}

func TestSyncParkingStoredItems(t *testing.T) {
	for _, section := range []string{"Icebox 🧊", "Exploration ideas"} {
		for _, inline := range []bool{false, true} {
			for _, rank := range []string{"", "a"} {
				t.Run(section+"/inline="+map[bool]string{true: "yes", false: "no"}[inline]+"/rank="+rank, func(t *testing.T) {
					s := syncStore(t)
					docs := []item.SyncDocument{doc("docs/roadmap.md", "## Active\n[Work](work.md)\n"), doc("docs/work.md", goodBlock)}
					if inline {
						docs = []item.SyncDocument{doc("docs/roadmap.md", "## Active\n"+strings.TrimPrefix(goodBlock, "# Plan\n"))}
					}
					runSync(t, s, "", docs...)
					exec(t, s, `UPDATE items SET rank=? WHERE item_id='item-build-thing'`, rank)
					exec(t, s, `INSERT INTO items(item_id,title,state,rank,updated_at) VALUES('item-terminal','Terminal','closed','zz',0)`)
					// A completed run belongs to the run subsystem, not to sync. Preserve every column.
					exec(t, s, `CREATE TABLE lanes(lane_id TEXT PRIMARY KEY,state TEXT,run_id TEXT,leased_until INTEGER)`)
					exec(t, s, `CREATE TABLE runs(run_id TEXT PRIMARY KEY,item_id TEXT,lane_id TEXT,state TEXT,artifact_refs TEXT)`)
					exec(t, s, `INSERT INTO lanes VALUES('lane-1','idle','run-1',NULL)`)
					exec(t, s, `INSERT INTO runs VALUES('run-1','item-build-thing','lane-1','pass','["receipt"]')`)
					runs := column(t, s, `SELECT json_group_array(json_array(run_id,item_id,lane_id,state,artifact_refs)) FROM runs`)
					lanes := column(t, s, `SELECT json_group_array(json_array(lane_id,state,run_id,leased_until)) FROM lanes`)
					if inline {
						docs[0] = doc("docs/roadmap.md", "## "+section+"\n"+strings.TrimPrefix(goodBlock, "# Plan\n"))
					} else {
						docs[0] = doc("docs/roadmap.md", "## "+section+"\n[Work](work.md)\n")
					}
					dry := previewParking(t, s, docs)
					parkingLists(t, dry, "item-build-thing", "")
					before := count(t, s, `SELECT count(*) FROM events`)
					report := runSync(t, s, "", docs...)
					parkingLists(t, report, "item-build-thing", "")
					if len(report.Findings) != 0 || column(t, s, `SELECT state||'|'||rank FROM items WHERE item_id='item-build-thing'`) != "not-started|" {
						t.Fatalf("parking failed: %+v", report)
					}
					if count(t, s, `SELECT count(*) FROM events`) != before+1 || column(t, s, `SELECT kind FROM events ORDER BY seq DESC LIMIT 1`) != "item.update" {
						t.Fatal("parking must append exactly one item.update")
					}
					if runs != column(t, s, `SELECT json_group_array(json_array(run_id,item_id,lane_id,state,artifact_refs)) FROM runs`) || lanes != column(t, s, `SELECT json_group_array(json_array(lane_id,state,run_id,leased_until)) FROM lanes`) {
						t.Fatal("parking changed runs or lanes")
					}
					repeat := runSync(t, s, "", docs...)
					parkingLists(t, repeat, "", "")
					if count(t, s, `SELECT count(*) FROM events`) != before+1 {
						t.Fatal("repeat parking appended an event")
					}
					// Even a legacy parked item with a retained key must get a new place at the end.
					exec(t, s, `UPDATE items SET rank=? WHERE item_id='item-build-thing'`, rank)
					if inline {
						docs[0] = doc("docs/roadmap.md", "## Active\n"+strings.TrimPrefix(goodBlock, "# Plan\n"))
					} else {
						docs[0] = doc("docs/roadmap.md", "## Active\n[Work](work.md)\n")
					}
					dry = previewParking(t, s, docs)
					parkingLists(t, dry, "", "item-build-thing")
					report = runSync(t, s, "", docs...)
					parkingLists(t, report, "", "item-build-thing")
					if column(t, s, `SELECT state FROM items WHERE item_id='item-build-thing'`) != "in-queue" || column(t, s, `SELECT rank FROM items WHERE item_id='item-build-thing'`) <= "zz" {
						t.Fatal("unpark did not append after all stored ranks")
					}
					if !reflect.DeepEqual(dry.Placed, report.Placed) {
						t.Fatalf("preview %v, actual %v", dry.Placed, report.Placed)
					}
					before = count(t, s, `SELECT count(*) FROM events`)
					parkingLists(t, runSync(t, s, "", docs...), "", "")
					if count(t, s, `SELECT count(*) FROM events`) != before {
						t.Fatal("repeat unparking appended an event")
					}
				})
			}
		}
	}
}

func TestSyncParkingWithFrozenDefinition(t *testing.T) {
	s := syncStore(t)
	docs := []item.SyncDocument{doc("docs/roadmap.md", "## Active\n[Work](work.md)\n"), doc("docs/work.md", goodBlock)}
	runSync(t, s, "", docs...)
	exec(t, s, `UPDATE item_criteria SET state='done' WHERE item_id='item-build-thing'`)
	before := count(t, s, `SELECT count(*) FROM events`)
	docs[0] = doc("docs/roadmap.md", "## Icebox\n[Work](work.md)\n")
	docs[1] = doc("docs/work.md", strings.Replace(goodBlock, "Build the thing", "Build the revised thing", 1))
	for _, parked := range []bool{true, false} {
		if !parked {
			docs[0] = doc("docs/roadmap.md", "## Active\n[Work](work.md)\n")
		}
		dry := previewParking(t, s, docs)
		report := runSync(t, s, "", docs...)
		if parked {
			parkingLists(t, dry, "item-build-thing", "")
			parkingLists(t, report, "item-build-thing", "")
			if column(t, s, `SELECT state||'|'||rank FROM items`) != "not-started|" {
				t.Fatal("frozen definition prevented parking")
			}
		} else {
			parkingLists(t, dry, "", "item-build-thing")
			parkingLists(t, report, "", "item-build-thing")
			if column(t, s, `SELECT state FROM items`) != "in-queue" {
				t.Fatal("frozen definition prevented unparking")
			}
		}
		before++
		if len(report.Frozen) != 1 || len(report.Findings) != 0 || column(t, s, `SELECT title FROM items`) != "Build the thing" || column(t, s, `SELECT text||'|'||state FROM item_criteria`) != "The thing builds.|done" || count(t, s, `SELECT count(*) FROM events`) != before {
			t.Fatalf("frozen definition/routing: %+v", report)
		}
	}
}

func TestSyncParkingSourcePreviewMatchesWrite(t *testing.T) {
	s := syncStore(t)
	docs := []item.SyncDocument{doc("docs/roadmap.md", "## Active\n[Work](work.md)\n"), doc("docs/work.md", fromBlock("item-source", ""))}
	runSync(t, s, "", docs...)
	runSync(t, s, "", doc("docs/last.md", fromBlock("item-last", "")))
	docs[0] = doc("docs/roadmap.md", "## Icebox\n[Work](work.md)\n")
	docs = append(docs, doc("docs/new.md", fromBlock("item-child", "item-source")))
	dry := previewParking(t, s, docs)
	report := runSync(t, s, "", docs...)
	if !reflect.DeepEqual(dry.Placed, report.Placed) {
		t.Fatalf("preview %v, actual %v", dry.Placed, report.Placed)
	}
	if len(report.Placed) != 1 || report.Placed[0].ItemID != "item-child" || report.Placed[0].Place != 2 || report.Placed[0].From != "" {
		t.Fatal(report.Placed)
	}
}

func TestSyncParkingRequiresWholeUnparkObservation(t *testing.T) {
	s := syncStore(t)
	docs := []item.SyncDocument{
		doc("docs/roadmap.md", "## Active\n[Work](work.md)\n"),
		doc("docs/other/roadmap.md", "## Icebox\n[Work](../work.md)\n"),
		doc("docs/work.md", goodBlock),
	}
	runSync(t, s, "", docs...)
	if got := column(t, s, `SELECT state FROM items`); got != "not-started" {
		t.Fatalf("one parked link must suffice: %s", got)
	}
	// Removing one roadmap from a partial read cannot establish that no
	// roadmap parks the document. Positive parking evidence still applies.
	partial := func(input []item.SyncDocument) item.SyncReport {
		var report item.SyncReport
		if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
			var err error
			report, err = item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{Partial: true, Documents: input, At: syncAt})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return report
	}
	before := count(t, s, `SELECT count(*) FROM events`)
	parkingLists(t, partial([]item.SyncDocument{docs[0], docs[2]}), "", "")
	if column(t, s, `SELECT state||'|'||rank FROM items`) != "not-started|" || count(t, s, `SELECT count(*) FROM events`) != before {
		t.Fatal("partial sync unparked an item")
	}
	docs[1] = doc("docs/other/roadmap.md", "## Active\n[Work](../work.md)\n")
	parkingLists(t, runSync(t, s, "", docs...), "", "item-build-thing")
	docs[1] = doc("docs/other/roadmap.md", "## Icebox\n[Work](../work.md)\n")
	parkingLists(t, partial([]item.SyncDocument{docs[1], docs[2]}), "item-build-thing", "")
}

func TestSyncParkingPreservesOtherStates(t *testing.T) {
	for _, state := range []string{"blocked", "landed", "closed", "resolved-elsewhere"} {
		t.Run(state, func(t *testing.T) {
			s := syncStore(t)
			docs := []item.SyncDocument{doc("docs/roadmap.md", "## Active\n[Work](work.md)\n"), doc("docs/work.md", goodBlock)}
			runSync(t, s, "", docs...)
			exec(t, s, `UPDATE items SET state=?,rank='a' WHERE item_id='item-build-thing'`, state)
			before := count(t, s, `SELECT count(*) FROM events`)
			for _, heading := range []string{"Icebox", "Active"} {
				docs[0] = doc("docs/roadmap.md", "## "+heading+"\n[Work](work.md)\n")
				parkingLists(t, previewParking(t, s, docs), "", "")
				parkingLists(t, runSync(t, s, "", docs...), "", "")
				if column(t, s, `SELECT state||'|'||rank FROM items WHERE item_id='item-build-thing'`) != state+"|a" || count(t, s, `SELECT count(*) FROM events`) != before {
					t.Fatal("sync moved another state")
				}
			}
		})
	}
}
