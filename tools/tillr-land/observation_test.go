package main_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

func TestLockObservationIsOptInAndDoesNotChangeTheReceipt(t *testing.T) {
	f := newFixture(t)
	db := filepath.Join(scratch(t, "store"), "state.db")
	t.Setenv("TILLR_LAND_OBSERVE", "1")
	code, out, stderr := passBoth(t, f.passArgs(t, db, "observation-item", "observation-run"))
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, stderr)
	}
	var result struct {
		Payload struct {
			LockObservation *land.LockObservation `json:"lock_observation"`
		}
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	o := result.Payload.LockObservation
	if o == nil || o.RunID != "observation-run" {
		t.Fatalf("missing observation: %s", out)
	}
	kinds := []string{}
	for i, e := range o.Events {
		kinds = append(kinds, e.Kind)
		if i > 0 && e.AtNS < o.Events[i-1].AtNS {
			t.Fatal("events out of order")
		}
	}
	if strings.Join(kinds, ",") != "acquired,squash,trunk-move,receipt,released" {
		t.Fatal(kinds)
	}
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var raw string
	if err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE kind='land.landing' ORDER BY seq DESC LIMIT 1`).Scan(&raw)
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "lock_observation") {
		t.Fatalf("receipt contains a live measurement: %s", raw)
	}
	t.Setenv("TILLR_LAND_OBSERVE", "")
	code, out, stderr = passBoth(t, f.passArgs(t, db, "observation-item", "observation-run"))
	if code != 0 || strings.Contains(string(out), "lock_observation") {
		t.Fatalf("default retry changed receipt: %d %s %s", code, out, stderr)
	}
}
