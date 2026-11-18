package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestReadFacesCountPlacesBeforeAndAfterKeying(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`ALTER TABLE items ADD COLUMN rank TEXT NOT NULL DEFAULT ''`)
	h.exec(`UPDATE items SET rank='secretkey' WHERE item_id='item-seed-02'`)
	h.exec(`INSERT INTO items(item_id,title,state,updated_at,rank) VALUES('item-a','A','in-queue',0,''),('item-z','Z','not-started',0,'')`)
	check := func() {
		_, body, raw := h.get("/api/v1/snapshot")
		for _, value := range body["data"].(map[string]any)["items"].([]any) {
			row := value.(map[string]any)
			if _, ok := row["rank"]; ok {
				t.Fatalf("snapshot leaks rank: %s", raw)
			}
			want := map[string]any{"item-seed-02": float64(1), "item-a": float64(2), "item-seed-06": float64(3)}[row["item_id"].(string)]
			if row["place"] != want {
				t.Fatalf("place %v want %v: %s", row["place"], want, raw)
			}
		}
		var rows []waitingRow
		if err := h.srv.store.ReadTx(context.Background(), func(r *store.Reader) error { var err error; rows, err = assembleWaiting(r, nil, h.store); return err }); err != nil {
			t.Fatal(err)
		}
		rawRows, _ := json.Marshal(rows)
		if bytes.Contains(rawRows, []byte(`"rank"`)) || bytes.Contains(rawRows, []byte("secretkey")) {
			t.Fatalf("waiting leaks key: %s", rawRows)
		}
		var decoded []map[string]any
		json.Unmarshal(rawRows, &decoded)
		for _, row := range decoded {
			if row["item_id"] == "item-a" && row["place"] != float64(2) {
				t.Fatal(row)
			}
			if row["item_id"] == "item-z" && row["place"] != nil {
				t.Fatal(row)
			}
		}
		var out bytes.Buffer
		if err := printWaiting(&out, rows); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(out.Bytes(), []byte("secretkey")) || !bytes.HasPrefix(out.Bytes(), []byte("#")) {
			t.Fatal(out.String())
		}
	}
	check()
	h.exec(`UPDATE items SET rank='secretkeym' WHERE item_id='item-a'`)
	h.exec(`UPDATE items SET rank='secretkeymm' WHERE item_id='item-seed-06'`)
	check()
}

func TestWaitingOrderSurvivesSyncMissingKeys(t *testing.T) {
	h := newServeHarness(t)
	h.exec("ALTER TABLE items ADD COLUMN rank TEXT NOT NULL DEFAULT ''")
	h.exec("UPDATE items SET rank='k' WHERE item_id='item-seed-02'")
	h.exec("INSERT INTO items(item_id,title,state,updated_at,rank) VALUES('item-z','Z','in-queue',0,''),('item-a','A','in-queue',0,'')")
	before := map[string]int{}
	read := func() map[string]int {
		t.Helper()
		_, body, raw := h.get("/api/v1/waiting")
		result := map[string]int{}
		for _, v := range body["data"].([]any) {
			row := v.(map[string]any)
			if _, ok := row["rank"]; ok {
				t.Fatalf("waiting leaks key: %s", raw)
			}
			if p, ok := row["place"].(float64); ok {
				result[row["item_id"].(string)] = int(p)
			}
		}
		return result
	}
	before = read()
	want := map[string]int{"item-seed-02": 1, "item-a": 2, "item-seed-06": 3, "item-z": 4}
	if !reflect.DeepEqual(before, want) {
		t.Fatalf("before %v want %v", before, want)
	}
	bin := filepath.Join(t.TempDir(), "tillr-item")
	command := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "TILLR_SQLITE_TRACE=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	command("../tillr-item", "go", "build", "-o", bin, ".")
	command(h.dir, "git", "init", "-q")
	sync := func() {
		command(h.dir, bin, "sync", "--store", h.store, "--root", h.dir, "--now", "2026-11-17T00:00:00Z", "--json")
	}
	sync()
	if after := read(); !reflect.DeepEqual(before, after) {
		t.Fatalf("sync moved places: %v -> %v", before, after)
	}
	var head int64
	if err := h.srv.store.ReadTx(context.Background(), func(r *store.Reader) error { var err error; head, err = r.EventHeadSeq(); return err }); err != nil {
		t.Fatal(err)
	}
	sync()
	if err := h.srv.store.ReadTx(context.Background(), func(r *store.Reader) error {
		after, err := r.EventHeadSeq()
		if after != head {
			t.Fatalf("second sync wrote records: %d -> %d", head, after)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStatusPreservesPlacesFromShowRecords(t *testing.T) {
	v := view{records: []record.Envelope{
		{Kind: itemRecordKind, Payload: json.RawMessage(`{"item_id":"z","title":"Z","state":"in-queue","place":4}`)},
		{Kind: itemRecordKind, Payload: json.RawMessage(`{"item_id":"a","title":"A","state":"in-queue","place":7}`)},
	}}
	board, err := v.assembleBoard()
	if err != nil {
		t.Fatal(err)
	}
	if len(board.items) != 2 || board.items[0].ItemID != "z" || board.items[0].Place != 4 || board.items[1].Place != 7 {
		t.Fatalf("Show places changed: %+v", board.items)
	}
	subject, err := v.assembleSubject("a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if subject.item.Place != 7 {
		t.Fatalf("subject place %d", subject.item.Place)
	}
	v.records = v.records[1:]
	subject, err = v.assembleSubject("a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if subject.item.Place != 7 {
		t.Fatalf("one Show record renumbered: %d", subject.item.Place)
	}
}
