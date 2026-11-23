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

func TestPiBuildStaleAlarm(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tip := "54175817b6000000000000000000000000000000"
	now := time.Date(2026, 11, 22, 13, 0, 0, 0, time.UTC)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       PiBuildStaleKind,
			ObjectType: "commit",
			ObjectID:   &tip,
			Payload:    []byte(`{"stale_key":"24c7e4f89e08","wanted_key":"54175817b600","tip":"` + tip + `","reason":"failed","detail":"build-pi exited 2"}`),
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
	if len(active) != 1 || active[0].Kind != PiBuildStale {
		t.Fatalf("alarms = %+v, want one %s", active, PiBuildStale)
	}
	rendered := active[0].String()
	for _, want := range []string{"24c7e4f89e08", "54175817b600", "failed", "build-pi exited 2"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("alarm %q missing %q", rendered, want)
		}
	}

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       PiBuildCurrentKind,
			ObjectType: "commit",
			ObjectID:   &tip,
			Payload:    []byte(`{"key":"54175817b600","tip":"` + tip + `"}`),
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
		t.Fatalf("expected stale pi build alarm to clear, got %+v", active)
	}
}
