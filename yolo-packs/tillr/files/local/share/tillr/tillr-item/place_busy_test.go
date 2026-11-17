package main_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestPlaceBusyRefusesWithoutChangingRows(t *testing.T) {
	path, s := placeFixture(t, "a", "b")
	before, _ := placeRows(t, s)
	var code int
	var out []byte
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		code, out = run(t, "place", "--store", path, "--item", "1", "--to", "first")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var answer struct{ Payload struct{ Rule string } }
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatalf("exit %d: %s (%v)", code, out, err)
	}
	if code != 1 || answer.Payload.Rule != "store-busy" {
		t.Fatalf("exit %d: %s", code, out)
	}
	after, _ := placeRows(t, s)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("busy refusal changed rows")
	}
}
