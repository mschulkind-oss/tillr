package main_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRecordFindingAndShowAndReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(scratch(t, "finding"), "state.db")
	seeded := base("item_042", "Investigate failure")
	seeded.State = "in-queue"
	seeded.Criteria = []criterion{{CriterionID: "c1", Text: frozenText}}
	if code, out := apply(t, dbPath, envelope(t, "item.record", "rec-1", seeded)); code != 0 {
		t.Fatalf("apply: %d %s", code, out)
	}
	db, err := store.Open(ctx, dbPath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	at := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

	if err := db.WriteTx(ctx, func(tx *store.Tx) error {
		return item.RecordFinding(ctx, tx, "item_042", item.Finding{Cause: "c", Evidence: "e"})
	}); err == nil {
		t.Fatal("expected rejection for incomplete finding")
	}
	if err := db.WriteTx(ctx, func(tx *store.Tx) error {
		return item.RecordFinding(ctx, tx, "missing", item.Finding{
			Cause: "c", Evidence: "e", WhyNoChange: "w", EvaluatedHead: "abc", WorkerRun: "run-1",
		})
	}); err == nil {
		t.Fatal("expected rejection for unknown item")
	}

	want := item.Finding{
		Cause:         "sibling failure",
		Evidence:      "go test ./drain passed on base",
		WhyNoChange:   "trunk already passes",
		EvaluatedHead: "deadbeef",
		WorkerRun:     "run-item-a-a1",
	}
	if err := db.WriteTx(ctx, func(tx *store.Tx) error {
		if err := item.RecordFinding(ctx, tx, "item_042", want); err != nil {
			return err
		}
		_, err := item.Close(ctx, tx, "item_042", "no_change: "+want.WhyNoChange, "run:run-item-a-a1", at)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	readFinding := func() *item.Finding {
		t.Helper()
		var got struct {
			Finding *item.Finding `json:"finding"`
		}
		if err := db.ReadTx(ctx, func(r *store.Reader) error {
			shown, err := item.Show(r, "item_042", "show-1")
			if err != nil {
				return err
			}
			return json.Unmarshal(shown.Payload, &got)
		}); err != nil {
			t.Fatal(err)
		}
		return got.Finding
	}
	got := readFinding()
	if got == nil || *got != want {
		t.Fatalf("Show finding = %+v, want %+v", got, want)
	}

	if err := db.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.Reopen(ctx, tx, "item_042", "recheck", "operator", at.Add(time.Minute))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := readFinding(); got != nil {
		t.Fatalf("expected finding cleared on reopen, got %+v", got)
	}
}
