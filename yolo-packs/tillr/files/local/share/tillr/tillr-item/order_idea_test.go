package main_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestOrderFaceIncludesIdeaSavedAtNoOpPlace(t *testing.T) {
	root, path, s := ideaFixture(t)
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec("DELETE FROM items")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	code, out := ideaRun(t, []byte("An idea"), "--root", root, "--store", path, "--to", "last")
	if code != 0 {
		t.Fatalf("idea exit %d: %s", code, out)
	}
	var seq int64
	var id string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow("SELECT seq,object_id FROM events WHERE kind='item.update' AND json_extract(payload,'$.placed_via') <> ''").Scan(&seq, &id)
	}); err != nil {
		t.Fatal(err)
	}
	code, out = run(t, "order", "--store", path)
	want := fmt.Sprintf("# last_hand_move_event_seq: %d\n%s\n", seq, id)
	if code != 0 || string(out) != want {
		t.Fatalf("exit %d: %s want %s", code, out, want)
	}
}
