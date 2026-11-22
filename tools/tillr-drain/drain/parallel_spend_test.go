package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestParallelReaderContinuationUpdatesSpendWithoutChargingSiblings(t *testing.T) {
	f, l, _ := parallelFixture(t)
	result := &LoopResult{}
	for i := 0; i < 2; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	id := attemptRunID("item-a", 1) + "-n2"
	tokens, cost := int64(30), 0.03
	rec := run.Record{RunID: id, Usage: &run.Usage{Tokens: &tokens, CostUSD: &cost}}
	if err := l.recordSettlementSpend(context.Background(), "continued-pass", l.sessionID, rec); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var rows int
		var total int64
		if err := tx.QueryRow(`SELECT COUNT(*),SUM(usage_tokens) FROM drain_pass_spend`).Scan(&rows, &total); err != nil {
			return err
		}
		if rows != 4 || total != 60 {
			t.Fatalf("continued cumulative usage lost or siblings charged twice: rows=%d tokens=%d", rows, total)
		}
		return nil
	})
}

func TestParallelReaderCrossLoopContinuationKeepsSettlementSpend(t *testing.T) {
	f, l, _ := parallelFixture(t)
	l.sessionID = "original-loop"
	result := &LoopResult{}
	for i := 0; i < 2; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	l.sessionID = "continuation-loop"
	id := attemptRunID("item-a", 1) + "-n2"
	tokens, cost := int64(30), 0.03
	rec := run.Record{RunID: id, Usage: &run.Usage{Tokens: &tokens, CostUSD: &cost}}
	if err := l.recordSettlementSpend(context.Background(), "continued-pass", "original-loop", rec); err != nil {
		t.Fatal(err)
	}
	// Re-examining a finished sibling must not attribute it to this new loop.
	tokens, cost = 10, 0.01
	rec.RunID = attemptRunID("item-a", 1) + "-n3"
	if err := l.recordSettlementSpend(context.Background(), "continued-pass", "original-loop", rec); err != nil {
		t.Fatal(err)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var rows, carried, total int
		if err := r.QueryRow(`SELECT COUNT(*), COALESCE(SUM(carried_over),0), COALESCE(SUM(usage_tokens),0)
 FROM drain_pass_spend WHERE settled_loop=?`, l.sessionID).Scan(&rows, &carried, &total); err != nil {
			return err
		}
		if rows != 1 || carried != 1 || total != 30 {
			t.Fatalf("continued usage omitted or unchanged siblings charged again: rows=%d carried=%d tokens=%d", rows, carried, total)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestParallelReadersAccumulateSpendForEverySiblingOnce(t *testing.T) {
	_, l, steps := parallelFixture(t)
	l.sessionID = "parallel-spend-loop"
	result := &LoopResult{}
	for i := 0; i < 2; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.started != 4 || result.Tokens != 40 || result.StartedSpend.Tokens != 40 || result.StartedSpend.Runs != 4 || result.CarriedSpend.Runs != 0 {
		t.Fatalf("accumulated spend lost or repeated siblings: readers=%d result=%+v", steps.started, result)
	}
}
