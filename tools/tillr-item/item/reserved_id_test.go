package item_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestOrderRecordIDReservedOnlyForNewItems(t *testing.T) {
	s := syncStore(t)
	defer s.Close()
	ctx := context.Background()
	payload, _ := json.Marshal(map[string]any{"item_id": "order-record", "title": "updated", "state": "in-queue"})
	env := record.Envelope{Kind: item.RecordKind, ID: "reserved", CreatedAt: syncAt, Payload: payload}
	err := s.WriteTx(ctx, func(tx *store.Tx) error { _, err := item.Apply(ctx, tx, env); return err })
	if rejection, ok := item.RejectionOf(err); !ok || rejection.Rule != "reserved-id" {
		t.Fatalf("new reserved id: %v", err)
	}
	if got := column(t, s, "SELECT count(*) FROM items WHERE item_id='order-record'"); got != "0" {
		t.Fatalf("refusal wrote item: %s", got)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec("INSERT INTO items(item_id,title,state,updated_at) VALUES('order-record','legacy','in-queue',0)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error { _, err := item.Apply(ctx, tx, env); return err }); err != nil {
		t.Fatalf("existing reserved id update: %v", err)
	}
	if got := column(t, s, "SELECT title FROM items WHERE item_id='order-record'"); got != "updated" {
		t.Fatal(got)
	}
}
