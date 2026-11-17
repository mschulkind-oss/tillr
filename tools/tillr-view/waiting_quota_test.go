package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/quota"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWaitingQuotaFailureShowsErrorInsteadOfFractions(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, t.TempDir()+"/store.db", true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Now().UTC()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(quota.DDL); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO backend_quota VALUES('claude','[]',?,0.2,'[]','quota command failed: invalid quota JSON')`, at.UnixMilli())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := waitingBackendQuotas(r, at)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Fatalf("rows=%+v", rows)
		}
		row := rows[0]
		if row.Class != "waits on work" || row.Mover != "a successful quota reading" || row.Quota == nil || row.Quota.Error == "" {
			t.Fatalf("failed reading not visible: %+v", row)
		}
		for _, want := range []string{"reserve 0.2", "invalid quota JSON", "reopening unknown", at.Add(quota.ProbeInterval).Format(time.RFC3339)} {
			if !strings.Contains(row.Why, want) {
				t.Errorf("missing %q: %s", want, row.Why)
			}
		}
		if strings.Contains(row.Why, "used ") || strings.Contains(row.Why, "open now") {
			t.Fatalf("failed reading claims usage or availability: %s", row.Why)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingQuotaShowsOpenReserveAndAllWindows(t *testing.T) {
	path := t.TempDir() + "/store.db"
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Now().UTC()
	short, long := at.Add(time.Hour), at.Add(7*24*time.Hour)
	windows, _ := json.Marshal([]quota.QuotaWindow{{Kind: "five_hour", Fraction: .8, ResetAt: &short}, {Kind: "seven_day", Fraction: .9, ResetAt: &long}})
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(quota.DDL); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO backend_quota VALUES('claude','[]',?,0.2,?,'')`, at.UnixMilli(), string(windows))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := waitingBackendQuotas(r, at)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Fatalf("rows=%+v", rows)
		}
		row := rows[0]
		for _, want := range []string{"five_hour used 0.8", "seven_day used 0.9", "reserve 0.2", long.Format(time.RFC3339)} {
			if !strings.Contains(row.Why, want) {
				t.Errorf("missing %q: %s", want, row.Why)
			}
		}
		if row.Quota == nil || len(row.Quota.Windows) != 2 {
			t.Fatalf("missing JSON reading: %+v", row)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := waitingBackendQuotas(r, at.Add(quota.ProbeInterval+time.Second))
		if err == nil && (!strings.Contains(rows[0].Why, "stale") || strings.Contains(rows[0].Why, "open now")) {
			t.Errorf("stale=%+v", rows)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE backend_quota SET windows='[{"kind":"five_hour","fraction":0,"reset_at":null}]'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := waitingBackendQuotas(r, at)
		if err == nil && (!strings.Contains(rows[0].Why, "open now") || !strings.Contains(rows[0].Why, "idle")) {
			t.Errorf("idle=%+v", rows)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
