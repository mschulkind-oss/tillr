package main_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func faultOf(t *testing.T, path, itemID string) drain.ItemFault {
	t.Helper()
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var fault drain.ItemFault
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		faults, err := drain.ReadItemFaults(r)
		fault = faults[itemID]
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return fault
}

func clearFault(t *testing.T, path, itemID string) {
	t.Helper()
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		faults, err := drain.ReadItemFaults(r)
		seq = faults[itemID].Seq
		return err
	})
	s.Close()
	if err != nil || seq == 0 {
		t.Fatalf("reading fault for %s: %d/%v", itemID, seq, err)
	}
	if code, out := invoke(t, "clear-fault", "--store", path, "--item", itemID, "--fault", fmt.Sprint(seq)); code != 0 {
		t.Fatalf("clear fault %d: %s", code, out)
	}
}

func TestClearFaultRequiresTheCurrentEvidenceAndDoesNotEraseIt(t *testing.T) {
	path := scratch(t, "fault-store") + "/state.db"
	fixtureAt(t, path, itemRecord("item_041", "in-queue"))
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		id := "item_041"
		for i := 0; i < 2; i++ {
			if _, err := tx.Append(event.Event{Kind: "drain.item-fault", ObjectType: "item", ObjectID: &id, Payload: []byte(fmt.Sprintf(`{"item_id":"item_041","reason":"gate-harness-error","detail":"fault %d"}`, i)), At: time.Now()}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if code, out := invoke(t, "clear-fault", "--store", path, "--item", "item_041", "--fault", "1"); code != 2 || !strings.Contains(string(out), "inspect waiting again") {
		t.Fatalf("stale clear %d %s", code, out)
	}
	clearFault(t, path, "item_041")
	s, err = store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		faults, err := drain.ReadItemFaults(r)
		if err != nil {
			return err
		}
		if len(faults) != 0 {
			t.Errorf("fault remains: %+v", faults)
		}
		var n int
		err = r.QueryRow(`SELECT count(*) FROM events WHERE kind='drain.item-fault'`).Scan(&n)
		if n != 2 {
			t.Errorf("diagnosis erased evidence: %d", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if code, _ := invoke(t, "clear-fault", "--store", path, "--item", "item_041", "--fault", "1"); code != 2 {
		t.Fatalf("absent fault clear %d", code)
	}
}
