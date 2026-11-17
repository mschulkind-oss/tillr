package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/quota"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWaitingQuotaAnyResetMakesReadingStale(t *testing.T) {
	for _, used := range []float64{.1, .9} {
		t.Run(fmt.Sprint(used), func(t *testing.T) {
			ctx := context.Background()
			s, err := store.Open(ctx, t.TempDir()+"/store.db", true)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			at := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
			short, long := at.Add(time.Second), at.Add(time.Hour)
			windows, _ := json.Marshal([]quota.QuotaWindow{{Kind: "five_hour", Fraction: used, ResetAt: &short}, {Kind: "seven_day", Fraction: used, ResetAt: &long}})
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				if _, err := tx.Exec(quota.DDL); err != nil {
					return err
				}
				_, err := tx.Exec(`INSERT INTO backend_quota VALUES('claude','[]',?,0.2,?,'')`, at.UnixMilli(), string(windows))
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := s.ReadTx(ctx, func(r *store.Reader) error {
				rows, err := waitingBackendQuotas(r, at.Add(2*time.Second))
				if err != nil {
					return err
				}
				if !strings.Contains(rows[0].Why, "reading stale") {
					t.Fatalf("expired window still open: %s", rows[0].Why)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
