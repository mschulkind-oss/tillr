package item_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestOrderRecordIDIsNoLongerReserved(t *testing.T) {
	s := syncStore(t)
	ctx := context.Background()
	payload, _ := json.Marshal(map[string]any{"item_id": "order-record", "title": "updated", "state": "in-queue"})
	env := record.Envelope{Kind: item.RecordKind, ID: "formerly-reserved", CreatedAt: syncAt, Payload: payload}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error { _, err := item.Apply(ctx, tx, env); return err }); err != nil {
		t.Fatalf("new item with formerly reserved id: %v", err)
	}
	if got := column(t, s, "SELECT title FROM items WHERE item_id='order-record'"); got != "updated" {
		t.Fatal(got)
	}
}
