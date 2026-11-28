package boundary

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestDeliveryPieces(t *testing.T) {
	for _, tc := range []struct {
		decision DeliveryDecision
		want     []string
	}{
		{DeliveryDecision{}, nil},
		{DeliveryDecision{Kind: "repo"}, []string{""}},
		{DeliveryDecision{Kind: "repo", Fork: "pi"}, nil},
		{DeliveryDecision{Kind: "fork", Fork: "pi"}, []string{"pi"}},
		{DeliveryDecision{Kind: "fork", Fork: "unknown"}, nil},
		{DeliveryDecision{Kind: "both", Fork: "pi"}, []string{"pi", ""}},
	} {
		got, err := tc.decision.Pieces(map[string]bool{"pi": true})
		if (err != nil) != (tc.want == nil) || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%+v: got %q, %v; want %q", tc.decision, got, err, tc.want)
		}
	}
}

func TestReadDeliveriesForItems(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	appendDelivery := func(tx *store.Tx, itemID, kind string, d DeliveryDecision) error {
		body, err := json.Marshal(d)
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{
			Kind:       kind,
			ObjectType: "item",
			ObjectID:   &itemID,
			Payload:    body,
			At:         at,
		})
		return err
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := appendDelivery(tx, "item-open-1", DeliveryDecisionKind, DeliveryDecision{Kind: "fork", Fork: "pi"}); err != nil {
			return err
		}
		if err := appendDelivery(tx, "item-open-2", DeliveryDecisionKind, DeliveryDecision{Kind: "both", Fork: "pi"}); err != nil {
			return err
		}
		if err := appendDelivery(tx, "item-open-2", DeliveryPartialKind, DeliveryDecision{Kind: "both", Fork: "pi", ForkLanded: "deadbeef"}); err != nil {
			return err
		}
		return appendDelivery(tx, "item-closed", DeliveryDecisionKind, DeliveryDecision{Kind: "repo"})
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		empty, err := ReadDeliveriesForItems(r, nil)
		if err != nil {
			return err
		}
		if len(empty) != 0 {
			t.Fatalf("empty itemIDs returned %+v, want empty", empty)
		}

		all, err := ReadDeliveries(r)
		if err != nil {
			return err
		}
		scoped, err := ReadDeliveriesForItems(r, []string{"item-open-1", "item-open-2"})
		if err != nil {
			return err
		}
		if len(scoped) != 2 {
			t.Fatalf("len(scoped) = %d, want 2 (%+v)", len(scoped), scoped)
		}
		if _, ok := scoped["item-closed"]; ok {
			t.Fatalf("scoped unexpectedly included item-closed: %+v", scoped)
		}
		for _, id := range []string{"item-open-1", "item-open-2"} {
			if !reflect.DeepEqual(scoped[id], all[id]) {
				t.Errorf("%s: scoped=%+v, all=%+v", id, scoped[id], all[id])
			}
		}
		if scoped["item-open-2"].ForkLanded != "deadbeef" {
			t.Errorf("expected latest partial delivery for item-open-2, got %+v", scoped["item-open-2"])
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
