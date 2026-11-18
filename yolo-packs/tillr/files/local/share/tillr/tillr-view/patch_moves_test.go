package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/patchmoves"
	"github.com/mschulkind-oss/tillr/store"
)

func TestUnexpectedPatchMoveNeedsOwnerUntilAcknowledged(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (5000,'boundary.check','run','observing-run','{"clean":true,"runs":[{"run_id":"observing-run"}],"patch_moves":[{"ref":"refs/heads/patch/pi","old":"old-commit","new":"new-commit"}]}')`)
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printWaiting(&out, rows); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"needs you", "patch/pi", "observing-run", "old-commit", "new-commit", "acknowledge-patch-move", "--by <owner>"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("waiting misses %q: %s", want, out.String())
		}
	}
	for _, row := range rows {
		if row.Where == "publication" {
			if row.ItemID != "" || row.Document != "" {
				t.Errorf("publication observation masquerades as an item/document: %+v", row)
			}
			if !strings.Contains(row.Mover, "--store "+h.store) {
				t.Errorf("acknowledgment command misses actual store: %s", row.Mover)
			}
		}
	}
	status, _, raw := h.get("/api/v1/waiting")
	if status != 200 || !strings.Contains(string(raw), "publication held") {
		t.Fatalf("API waiting: %d %s", status, raw)
	}
	s, err := store.Open(context.Background(), h.store, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		holds, err := patchmoves.Pending(tx.Reader())
		if err != nil {
			return err
		}
		if len(holds) != 1 {
			t.Fatalf("holds: %v", holds)
		}
		return patchmoves.Acknowledge(tx, holds[0].Observation, "pi", "owner", time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Where == "publication" {
			t.Fatalf("acknowledged move still waiting: %v", row)
		}
	}
	_, _, raw = h.get("/api/v1/waiting")
	if strings.Contains(string(raw), "publication held") {
		t.Fatalf("acknowledged move still on API: %s", raw)
	}
}
