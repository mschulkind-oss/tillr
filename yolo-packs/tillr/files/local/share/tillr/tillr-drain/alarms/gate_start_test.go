package alarms

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestGateStartFailedAlarm(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	gateID := "gate"
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	missing := "/opt/tillr/tillr-gate/change-gate.py"
	detail := "the gate program could not start against this candidate: installed tillr-gate runner " + missing + " is missing or not a regular file"
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       GateStartFailedKind,
			ObjectType: "gate",
			ObjectID:   &gateID,
			Payload:    []byte(`{"item_id":"item-a","run_id":"run-1","detail":"` + detail + `"}`),
			At:         now,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var active []Alarm
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		active, err = Read(r, now, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Kind != GateStartFailed || active[0].Backend != "gate" {
		t.Fatalf("alarms = %+v, want one %s (gate)", active, GateStartFailed)
	}
	rendered := active[0].String()
	for _, want := range []string{GateStartFailed, missing, "missing or not a regular file"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("alarm %q missing %q", rendered, want)
		}
	}

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       GateStartClearedKind,
			ObjectType: "gate",
			ObjectID:   &gateID,
			Payload:    []byte(`{}`),
			At:         now.Add(time.Minute),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		active, err = Read(r, now.Add(time.Minute), nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("expected gate-start-failed alarm to clear, got %+v", active)
	}
}
