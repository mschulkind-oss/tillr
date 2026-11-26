package storelog

import (
	"context"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// PassSpendDDL is the table attributing run spend to the pass that settles it.
// Spend belongs to the pass that settles it, but its origin remains the loop
// that started it. One run per pass prevents a replayed settlement inflating it.
const PassSpendDDL = `
CREATE TABLE IF NOT EXISTS drain_pass_spend (
 pass_id TEXT NOT NULL,
 run_id TEXT NOT NULL,
 carried_over INTEGER NOT NULL,
 settled_loop TEXT NOT NULL,
 usage_tokens INTEGER,
 usage_cost_usd REAL,
 PRIMARY KEY(pass_id, run_id)
);
`

// RecordSettlementSpend writes or refreshes the drain_pass_spend row for a settled run.
func RecordSettlementSpend(ctx context.Context, storePath, sessionID, passID, origin string, rec run.Record) error {
	if passID == "" {
		return nil
	}
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return err
	}
	defer s.Close()
	var tokens, cost any
	if rec.Usage != nil {
		if rec.Usage.Tokens != nil {
			tokens = *rec.Usage.Tokens
		}
		if rec.Usage.CostUSD != nil {
			cost = *rec.Usage.CostUSD
		}
	}
	carried := origin == "" || origin != sessionID
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		// A retried reader settles alongside finished siblings. Their original
		// accounting belongs to its first pass, not every subsequent group pass.
		var prior int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM drain_pass_spend WHERE run_id=? AND pass_id!=? AND EXISTS(SELECT 1 FROM read_only_runs WHERE run_id=?)`, rec.RunID, passID, rec.RunID).Scan(&prior); err != nil {
			return err
		}
		if prior > 0 {
			// Refresh changed cumulative usage under the loop that settled the
			// continuation, without adding a second charge. An unchanged sibling
			// keeps its previous attribution and is not charged to this loop.
			_, err := tx.Exec(`UPDATE drain_pass_spend SET usage_tokens=?,usage_cost_usd=?,carried_over=?,settled_loop=?
 WHERE run_id=? AND (usage_tokens IS NOT ? OR usage_cost_usd IS NOT ?)`, tokens, cost, carried, sessionID, rec.RunID, tokens, cost)
			return err
		}
		_, err := tx.Exec(`INSERT INTO drain_pass_spend(pass_id,run_id,carried_over,settled_loop,usage_tokens,usage_cost_usd) VALUES(?,?,?,?,?,?)
   ON CONFLICT(pass_id,run_id) DO UPDATE SET carried_over=excluded.carried_over,
   settled_loop=excluded.settled_loop,usage_tokens=excluded.usage_tokens,usage_cost_usd=excluded.usage_cost_usd`, passID, rec.RunID, carried, sessionID, tokens, cost)
		return err
	})
}
