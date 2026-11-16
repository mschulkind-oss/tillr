package item_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestDefinitionUpdatePreservesBlockedMetadataAndReleaseClearsIt(t *testing.T) {
	s := syncStore(t)
	defer s.Close()
	runSync(t, s, "", doc("docs/api.md", proseBlock))
	ctx := context.Background()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.SetState(ctx, tx, "item-read-api", "blocked", syncAt, item.WaitingOn{Waiter: "plan", Reason: "choose the sequence"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	report := runSync(t, s, "", doc("docs/api.md", strings.Replace(proseBlock, "Add the read API", "Add the read API safely", 1)))
	if report.UpdatedCount != 1 {
		t.Fatalf("sync did not update definition: %+v", report)
	}
	p := storedProsePayload(t, s)
	env := record.Envelope{Kind: item.RecordKind, ID: "definition-update", CreatedAt: syncAt}
	if p["waiting_on"].(map[string]any)["waiter"] != "plan" || p["underlying_state"] != "in-queue" {
		t.Fatalf("sync lost metadata: %+v", p)
	}
	p["title"] = "new definition"
	env.Payload, _ = json.Marshal(p)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error { _, err := item.Apply(ctx, tx, env); return err }); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		w, underlying, err := item.ReadWaitingOn(r, "item-read-api")
		if err == nil && (w == nil || w.Waiter != "plan" || underlying != "in-queue") {
			t.Fatalf("apply lost metadata: %+v / %s", w, underlying)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	p["state"] = "in-queue"
	env.Payload, _ = json.Marshal(p)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error { _, err := item.Apply(ctx, tx, env); return err }); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		w, underlying, err := item.ReadWaitingOn(r, "item-read-api")
		if err == nil && (w != nil || underlying != "") {
			t.Fatalf("apply release kept metadata: %+v / %s", w, underlying)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
