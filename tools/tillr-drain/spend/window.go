// Package spend reads recorded costs for display; it never controls dispatch.
package spend

import (
	"context"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// Window is the recorded cost of runs started in the fixed last 24 hours.
// Missing counts absent costs, not zero-dollar measurements. A continuation
// updates its existing run row, while a retry owns a separate row: neither
// inherits a previous attempt's cost here.
type Window struct {
	CostUSD float64
	Runs    int
	Missing int
}

// Read includes starts at the lower boundary and excludes future starts. It
// reads the current cumulative measurement once per run, regardless of state.
// A store with no run table has no recorded runs; other read errors are returned.
func Read(ctx context.Context, path string, now time.Time) (Window, error) {
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return Window{}, err
	}
	defer s.Close()
	var sum Window
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var exists bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='runs')`).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return nil
		}
		return r.QueryRow(`SELECT COALESCE(SUM(usage_cost_usd),0), COUNT(*), COALESCE(SUM(usage_cost_usd IS NULL),0)
   FROM runs WHERE started_at >= ? AND started_at <= ?`, now.Add(-24*time.Hour).UnixMilli(), now.UnixMilli()).Scan(&sum.CostUSD, &sum.Runs, &sum.Missing)
	})
	return sum, err
}

func (s Window) String() string {
	cost := fmt.Sprintf("$%.2f", s.CostUSD)
	if s.Runs > 0 && s.Missing == s.Runs {
		cost = "cost unreported"
	}
	noun := "runs"
	if s.Runs == 1 {
		noun = "run"
	}
	line := fmt.Sprintf("spend, last 24h: %s over %d %s", cost, s.Runs, noun)
	if s.Missing > 0 {
		noun := "runs"
		if s.Missing == 1 {
			noun = "run"
		}
		line += fmt.Sprintf("; %d %s reported no cost", s.Missing, noun)
		if s.Missing < s.Runs {
			line += "; lower bound"
		}
	}
	return line
}
