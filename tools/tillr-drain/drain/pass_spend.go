package drain

import (
	"context"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Spend belongs to the pass that settles it, but its origin remains the loop
// that started it. One run per pass prevents a replayed settlement inflating it.
const passSpendDDL = `
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

func (l *Loop) recordSettlementSpend(ctx context.Context, passID, origin string, rec run.Record) error {
	if passID == "" {
		return nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
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
	carried := origin == "" || origin != l.sessionID
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO drain_pass_spend(pass_id,run_id,carried_over,settled_loop,usage_tokens,usage_cost_usd) VALUES(?,?,?,?,?,?)
   ON CONFLICT(pass_id,run_id) DO UPDATE SET carried_over=excluded.carried_over,
   settled_loop=excluded.settled_loop,usage_tokens=excluded.usage_tokens,usage_cost_usd=excluded.usage_cost_usd`, passID, rec.RunID, carried, l.sessionID, tokens, cost)
		return err
	})
}
