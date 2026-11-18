package item_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func shownPlace(t *testing.T, s *store.Store, id string) any {
	t.Helper()
	var fields map[string]any
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var design string
		if err := r.QueryRow("SELECT COALESCE(design_doc,'') FROM items WHERE item_id=?", id).Scan(&design); err != nil {
			return err
		}
		root := scratch(t)
		if design != "" {
			path := filepath.Join(root, design)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte("# Test\n"), 0600); err != nil {
				return err
			}
		}
		env, err := item.Show(r, id, root)
		if err != nil {
			return err
		}
		return json.Unmarshal(env.Payload, &fields)
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["rank"]; ok {
		t.Fatalf("show exposed key: %v", fields)
	}
	return fields["place"]
}

func TestSyncKeysMissingOrderWithoutMovingPlaces(t *testing.T) {
	s := syncStore(t)
	exec(t, s, `INSERT INTO items(item_id,title,state,rank,updated_at) VALUES
 ('item-z','Z','in-queue','',0),('item-b','B','blocked','',0),('item-a','A','in-progress','',0),
 ('item-first','First','in-queue','k',0),('item-parked','Parked','not-started','',0),('item-done','Done','landed','z',0)`)
	// Opening an existing store must not key a row or append a record.
	reopened, err := store.Open(context.Background(), s.Path(), false)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Close()
	if n := count(t, s, `SELECT count(*) FROM items WHERE rank=''`); n != 4 {
		t.Fatal(n)
	}
	before := map[string]any{}
	for i, id := range []string{"item-first", "item-a", "item-b", "item-z"} {
		before[id] = shownPlace(t, s, id)
		if before[id] != float64(i+1) {
			t.Fatalf("%s place %v", id, before[id])
		}
	}
	for _, id := range []string{"item-parked", "item-done"} {
		if shownPlace(t, s, id) != nil {
			t.Fatalf("%s has place", id)
		}
	}
	runSync(t, s, "")
	if got := column(t, s, `SELECT group_concat(item_id,',') FROM (SELECT item_id FROM items WHERE state NOT IN ('not-started','landed','closed','resolved-elsewhere') ORDER BY rank,item_id)`); got != "item-first,item-a,item-b,item-z" {
		t.Fatal(got)
	}
	if n := count(t, s, `SELECT count(*) FROM events WHERE kind='item.update'`); n != 3 {
		t.Fatalf("%d key records, want 3", n)
	}
	for id, place := range before {
		if got := shownPlace(t, s, id); !reflect.DeepEqual(got, place) {
			t.Fatalf("%s moved %v -> %v", id, place, got)
		}
	}
	head := count(t, s, `SELECT count(*) FROM events`)
	runSync(t, s, "")
	if got := count(t, s, `SELECT count(*) FROM events`); got != head {
		t.Fatalf("second sync wrote %d records", got-head)
	}
	if got := column(t, s, `SELECT rank FROM items WHERE item_id='item-first'`); got != "k" {
		t.Fatal(got)
	}
}

func TestSyncPlacesNewAndUnparkedItemsLast(t *testing.T) {
	s := syncStore(t)
	block := func(id string) string {
		return fmt.Sprintf("### %s\n<!-- tillr: item id=\"%s\" paths=\"tools/\" -->\n- **c1:** Works\n", id, id)
	}
	exec(t, s, `INSERT INTO items(item_id,title,state,rank,updated_at) VALUES('item-first','First','in-queue','z',0)`)
	parked := doc("docs/roadmap.md", "## Icebox\n- [Idea](idea.md)\n")
	idea := doc("docs/idea.md", block("item-idea"))
	runSync(t, s, "", parked, idea, doc("docs/new.md", block("item-new")))
	if got := shownPlace(t, s, "item-new"); got != float64(2) {
		t.Fatal(got)
	}
	if got := shownPlace(t, s, "item-idea"); got != nil {
		t.Fatal(got)
	}
	runSync(t, s, "", doc("docs/roadmap.md", "## Next\n- [Idea](idea.md)\n"), idea)
	if got := shownPlace(t, s, "item-idea"); got != float64(3) {
		t.Fatal(got)
	}
	if got := column(t, s, `SELECT rank FROM items WHERE item_id='item-first'`); got != "z" {
		t.Fatal(got)
	}
}

func TestSyncKeysRollBackWithItsTransaction(t *testing.T) {
	s := syncStore(t)
	exec(t, s, "INSERT INTO items(item_id,title,state,updated_at) VALUES('item-a','A','in-queue',0)")
	rollback := errors.New("roll back sync")
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{At: syncAt}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if key := column(t, s, "SELECT rank FROM items WHERE item_id='item-a'"); key != "" {
		t.Fatalf("key escaped transaction: %q", key)
	}
	if n := count(t, s, "SELECT count(*) FROM events"); n != 0 {
		t.Fatalf("%d records escaped transaction", n)
	}
}

func TestLegacyRankRecordsStillApplyAndSeed(t *testing.T) {
	for _, seed := range []bool{false, true} {
		t.Run(fmt.Sprintf("seed=%v", seed), func(t *testing.T) {
			s, err := store.InitFixture(context.Background(), filepath.Join(scratch(t), "legacy.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var env record.Envelope
			if err := json.Unmarshal(itemRecord(t, "item-legacy", "Legacy"), &env); err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			json.Unmarshal(env.Payload, &payload)
			payload["rank"] = "legacykey"
			payload["state"] = "in-queue"
			env.Payload, _ = json.Marshal(payload)
			raw, _ := json.Marshal(env)

			if seed {
				err = s.Seed(context.Background(), seedBundle(t, raw))
			} else {
				err = s.WriteTx(context.Background(), func(tx *store.Tx) error { _, err := item.Apply(context.Background(), tx, env); return err })
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := column(t, s, "SELECT rank FROM items WHERE item_id='item-legacy'"); got != "legacykey" {
				t.Fatal(got)
			}
			if got := shownPlace(t, s, "item-legacy"); got != float64(1) {
				t.Fatal(got)
			}
		})
	}
}

func TestPartialSyncDoesNotUnparkSiblingBlocks(t *testing.T) {
	s := syncStore(t)
	idea := doc("docs/idea.md", "### Idea\n<!-- tillr: item id=\"item-idea\" paths=\"tools/\" -->\n- **c1:** Works\n")
	runSync(t, s, "", doc("docs/roadmap.md", "## Icebox\n- [Idea](idea.md)\n"), idea)
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{Documents: []item.SyncDocument{idea}, At: syncAt, Partial: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if state := column(t, s, "SELECT state FROM items WHERE item_id='item-idea'"); state != "not-started" {
		t.Fatalf("partial sync unparked item: %s", state)
	}
	if key := column(t, s, "SELECT rank FROM items WHERE item_id='item-idea'"); key != "" {
		t.Fatalf("partial sync keyed parked item: %q", key)
	}
}

func TestSyncDefinitionUpdateKeepsItsFreshlySuppliedKey(t *testing.T) {
	s := syncStore(t)
	exec(t, s, "INSERT INTO items(item_id,title,state,updated_at,rank) VALUES('item-first','First','in-queue',0,'k'),('item-update','Old','in-queue',0,'')")
	runSync(t, s, "", doc("docs/update.md", "### Updated\n<!-- tillr: item id=\"item-update\" paths=\"tools/\" -->\n- **c1:** Works\n"))
	if got := column(t, s, "SELECT rank FROM items WHERE item_id='item-update'"); got != "km" {
		t.Fatalf("definition update replaced supplied key: %q", got)
	}
	if n := count(t, s, "SELECT count(*) FROM events WHERE object_id='item-update' AND json_extract(payload,'$.rank')='km'"); n != 2 {
		t.Fatalf("key and definition receipts: %d", n)
	}
}

func TestSyncUnparkReplacesAnExistingKeyAtTheEnd(t *testing.T) {
	s := syncStore(t)
	idea := doc("docs/idea.md", "### Idea\n<!-- tillr: item id=\"item-idea\" paths=\"tools/\" -->\n- **c1:** Works\n")
	runSync(t, s, "", doc("docs/roadmap.md", "## Icebox\n- [Idea](idea.md)\n"), idea)
	exec(t, s, "UPDATE items SET rank='a' WHERE item_id='item-idea'; INSERT INTO items(item_id,title,state,updated_at,rank) VALUES('item-first','First','in-queue',0,'z')")
	runSync(t, s, "", doc("docs/roadmap.md", "## Next\n- [Idea](idea.md)\n"), idea)
	if got := column(t, s, "SELECT rank FROM items WHERE item_id='item-idea'"); got <= "z" {
		t.Fatal(got)
	}
	if got := shownPlace(t, s, "item-idea"); got != float64(2) {
		t.Fatal(got)
	}
}

func TestSyncKeysExistingItemDespiteRefusedDocument(t *testing.T) {
	s := syncStore(t)
	exec(t, s, "INSERT INTO items(item_id,title,state,updated_at) VALUES('item-a','Old','in-queue',0)")
	report := runSync(t, s, "", doc("docs/refused.md", "### New\n<!-- tillr: item id=\"item-a\" paths=\"tools/\" rank=\"bad\" -->\n- **c1:** Works\n"))
	if len(report.Findings) != 1 {
		t.Fatalf("findings %+v", report)
	}
	if got := column(t, s, "SELECT title||'|'||rank FROM items WHERE item_id='item-a'"); got != "Old|m" {
		t.Fatal(got)
	}
	if n := count(t, s, "SELECT count(*) FROM events WHERE kind='item.update'"); n != 1 {
		t.Fatalf("key receipts %d", n)
	}
}
