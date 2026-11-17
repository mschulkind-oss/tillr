package spend

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWindowBoundariesAndRetryWithoutNewOutput(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "spend.db")
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state) VALUES('lane','free')`); err != nil {
			return err
		}
		for _, row := range []struct {
			id   string
			at   time.Time
			cost any
		}{
			{"interrupted", now.Add(-time.Hour), 41.2},
			{"retry", now.Add(-time.Minute), nil},
			{"boundary", now.Add(-24 * time.Hour), 2.0},
			{"before-boundary", now.Add(-24*time.Hour - time.Millisecond), 500.0},
			{"future", now.Add(time.Millisecond), 900.0},
		} {
			if _, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,usage_cost_usd) VALUES(?,'same-item','lane','pi','interrupted',?,?,?)`, row.id, row.at.UnixMilli(), row.at.UnixMilli(), row.cost); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	check := func(want Window) {
		t.Helper()
		got, err := Read(ctx, path, now)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
	check(Window{CostUSD: 43.2, Runs: 3, Missing: 1})
	// A resume with no new model output leaves the same cumulative row intact.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE runs SET finished_at=?,state='exited' WHERE run_id IN ('interrupted','retry')`, now.UnixMilli())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	check(Window{CostUSD: 43.2, Runs: 3, Missing: 1})
	var retryCost any
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT usage_cost_usd FROM runs WHERE run_id='retry'`).Scan(&retryCost)
	}); err != nil {
		t.Fatal(err)
	}
	if retryCost != nil {
		t.Fatalf("retry inherited cost: %v", retryCost)
	}
}

func TestWindowNeverLabelsMissingCostAsZero(t *testing.T) {
	for _, tc := range []struct {
		sum  Window
		want string
	}{
		{Window{}, "spend, last 24h: $0.00 over 0 runs"},
		{Window{Runs: 1}, "spend, last 24h: $0.00 over 1 run"},
		{Window{Runs: 1, Missing: 1}, "spend, last 24h: cost unreported over 1 run; 1 run reported no cost"},
		{Window{Runs: 2, Missing: 2}, "spend, last 24h: cost unreported over 2 runs; 2 runs reported no cost"},
		{Window{CostUSD: 41.2, Runs: 2, Missing: 1}, "spend, last 24h: $41.20 over 2 runs; 1 run reported no cost; lower bound"},
	} {
		if got := tc.sum.String(); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
	if strings.Contains((Window{Runs: 1, Missing: 1}).String(), "$0") {
		t.Fatal("missing cost presented as zero dollars")
	}
}
