package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestOrderFaceTextAndLastHandMove(t *testing.T) {
	path, s := placeFixture(t, "a", "b", "c")
	code, out := run(t, "order", "--store", path)
	if code != 0 || string(out) != "# last_hand_move_event_seq: 0\n0\n1\n2\n" {
		t.Fatalf("exit %d: %s", code, out)
	}
	code, out = run(t, "place", "--store", path, "--item", "2", "--to", "first")
	if code != 0 {
		t.Fatal(out)
	}
	var receipt struct {
		Payload struct {
			Seq int64 `json:"event_seq"`
		}
	}
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO items(item_id,title,state,rank,updated_at) VALUES('parked','Parked','not-started','',0),('done','Done','landed','z',0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	code, out = run(t, "order", "--store", path)
	want := fmt.Sprintf("# last_hand_move_event_seq: %d\n2\n0\n1\n", receipt.Payload.Seq)
	if code != 0 || string(out) != want {
		t.Fatalf("exit %d: %s want %s", code, out, want)
	}
	code, out = run(t, "order")
	if code != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
}
